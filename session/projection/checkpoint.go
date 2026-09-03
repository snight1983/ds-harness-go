// 本文件的作用：一份检查点长什么样，以及不拿活会话、只拿检查点和一截日志
// 尾巴就能走的那三级读法。
//
// 源: packages/session/session-projection/src/index.ts:96-126
// 源: packages/session/session-projection/src/index.ts:342-454

package projection

import (
	"encoding/json"
	"fmt"

	"github.com/snight1983/ds-harness-go/session"
)

// Snapshot 是对一个会话上所有已登记的、客户端可见的单元的一次一致读切。
//
// 源: packages/session/session-projection/src/index.ts:100-110（ProjectionSnapshot）
type Snapshot struct {
	// AsOfSeq 是这些值反映到的最后一条事件的 seq；空日志时为 -1。
	//
	// 它是**共用**的水位：Values 里每一个值都反映到这同一个位置。
	AsOfSeq int
	// Values 是每个已登记的客户端可见键的整值。
	//
	// 一个客户端可见的单元都没登记时它是空的（不是 nil，见 [Registry.Snapshot]）。
	Values map[string]any
}

// CheckpointRow 是一个单元的一次检查点：它的内部状态、折进去的最后一条事件的
// seq、以及产出它的那个 [Definition.StateVersion]。
//
// 源: packages/session/session-projection/src/index.ts:112-127（ProjectionCheckpointRow）
//
// 一行检查点**永远不是权威**，它只是一条折叠捷径：[Registry.Restore] 在版本
// 对不上、或者它声称的水位超过了存储里日志的末尾时会把它丢掉。
type CheckpointRow struct {
	// Ver 是折出这份状态时那个单元的 [Definition.StateVersion]。
	Ver int `json:"ver"`
	// Seq 是折进 Val 的最后一条事件的 seq；空日志时为 -1。
	Seq int `json:"seq"`
	// Val 是这个单元的内部状态，按单元契约必须是纯 JSON。
	Val json.RawMessage `json:"val"`
}

// Checkpoint 是一个会话的那份持久投影缓存：按投影键归档的检查点行。
//
// 源: packages/session/session-projection/src/index.ts:129-130（ProjectionCheckpoint）
type Checkpoint map[string]CheckpointRow

// Restored 是一次冷读的结果：切在给进来那截日志末尾的读面，加上同一个位置上
// 刷新过的检查点行——后者可以直接落盘。
type Restored struct {
	Snapshot   Snapshot
	Checkpoint Checkpoint
}

// RestoreFloor 给出一次 [Registry.Restore] 的日志尾巴该从哪个存储 seq 读起：
// 比最低的那个可用水位再低一条。第二个返回值为假表示一个单元都没登记，
// 这时候根本不需要读——[Registry.Restore] 无论如何都只会给出空值。
//
// 源: packages/session/session-projection/src/index.ts:342-368
//
// 一行是「可用的」当且仅当它的 Ver 和当前单元的 [Definition.StateVersion] 相等；
// 缺行或者版本对不上就把地板拉到 0——那个键必须从头重折一遍。
//
// **往下让一条**是承重的：这样读回来的尾巴就能证明存储里的日志还延伸到哪儿，
// 于是 [Registry.Restore] 能发现一份缩短了的日志（崩溃收尾截过），
// 而不是把一行过期的数据当成当前值端出去。从这个锚点读回来的空尾巴会给出一个
// 低于所有水位的末尾，于是恢复被拒，调用方重读整份。
//
// 新增: 这里交出来的 0 读作「从存档现存的最前面读起」，不读作「从 seq 0 读起」
// ——日志会从最老的一头弹出事件，起点是个变量（见 docs/session-log-limit.md
// 的原则第 1 条）。这个数是一个**请求**，起点的实情由那次读答复：
// [github.com/snight1983/ds-harness-go/session/persistence.StoredSuffix] 带着它，
// 调用方再把它当作 [Registry.Restore] 的 baseSeq 传进去。地板本身问不出起点
// ——它算在读之前，而起点要读了才知道。
func (r *Registry) RestoreFloor(checkpoint Checkpoint) (int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	floor, seen := 0, false
	for _, reg := range r.registrations {
		need := 0
		if row, ok := checkpoint[reg.def.key]; ok && row.Ver == reg.def.stateVersion {
			need = max(row.Seq+1, 0)
		}
		if !seen || need < floor {
			floor, seen = need, true
		}
	}
	if !seen {
		return 0, false
	}
	return max(floor-1, 0), true
}

// ViewCheckpoint 不读任何日志，直接把检查点里的行折成客户端值：每一个已登记的
// 客户端可见单元，只要它的行版本对得上、状态又解得开，就服务出那份视图；
// 版本对不上、解不开、或者压根没有那一行的键就缺席。
//
// 源: packages/session/session-projection/src/index.ts:370-396
//
// 这是读梯子上零 I/O 的那一级——值只可能是**旧的**，不可能是**错的**。
// 一个只做列表或者还没热起来的使用方把缺席的键当成「还没准备好」，
// 更完整的那条读路径会把它重折出来。
func (r *Registry) ViewCheckpoint(checkpoint Checkpoint) map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()

	values := map[string]any{}
	for _, reg := range r.registrations {
		def := reg.def
		if def.view == nil {
			continue
		}
		row, ok := checkpoint[def.key]
		if !ok || row.Ver != def.stateVersion {
			continue
		}
		state, err := def.decodeState(row.Val)
		if err != nil {
			// 解不开就当这个键缺席。这一级是「不做 I/O 也想看点东西」，
			// 为了一行读不动的缓存把整次读拒掉不划算——重折那条路还在。
			continue
		}
		values[def.key] = def.view(state)
	}
	return values
}

// Restore 是冷读：拿一截存储里的日志尾巴，把每一个已登记的单元从它的检查点行
// 接着往下折——「缓存的状态 + 往前重放尾巴 + 出视图」这一条读法，在没有活会话的
// 情况下走一遍。
//
// 源: packages/session/session-projection/src/index.ts:398-454
//
// 调用方式是固定的：先 [Registry.RestoreFloor] 拿到 fromSeq，用它去存储里
// readFrom，再把读回来的 events、**同一个** fromSeq、以及那次读答复的存档起点
// 一起传进来。那个「往下让一条」的锚点让给进来的末尾变得可信，所以一份缩短了的
// 日志会在这里被发现。
//
// baseSeq 是**整份存档**现存最早一条事件的 seq，由存储那一侧说出来
// （[github.com/snight1983/ds-harness-go/session/persistence.StoredSuffix] 的同名
// 字段）。请求的 fromSeq 落在它之前时，读回来的那截就是现存的全部。
//
// 一行是「可用的」当且仅当：版本和当前单元的 [Definition.StateVersion] 相等、
// 它不早于这截尾巴的起点（Seq >= start-1）、而且它不声称自己包含了超过给进来那截
// 日志末尾的事件（Seq <= endSeq）。不可用的行会被丢掉、那个键从
// [Definition.Init] 重折——而重折要求给进来的是现存的**全部**日志，所以只在
// fromSeq 真的切掉了前面一段时才返回 [ErrCheckpointUnusable]，让调用方重读整份。
//
// 新增: 上游那道判据是 `baseSeq > 0`，靠的是「日志从 0 起，所以 0 就是整读」。
// 本仓库的日志会从最老的一头弹出事件（见 docs/session-log-limit.md 的原则第 1
// 条），整读的起点恒大于 0——照旧判的话遇到一行不可用就必报错，而调用方按提示
// 「从 seq 0 重读」永远回不到 0，是一条读不出来也退不回去的死路。判据因此换成
// 「这截尾巴是不是现存的全部」。
//
// 新增: 一份被弹过头部的日志重折出来的状态是**残缺的**——那些最后一次更新落在
// 被弹区间里的单元丢了。这不阻断读，见原则第 5 条。
//
// 一行版本对得上却解不开，是**这个构建自己写坏了**，不是过期。这种情况原样
// 上抛而不是退回重折：悄悄重折会把一个真实的缺陷盖住，而且盖住之后每次冷读
// 都要白折一遍整份日志。
func (r *Registry) Restore(
	checkpoint Checkpoint,
	events []session.Event,
	fromSeq int,
	baseSeq int,
) (Restored, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 这截尾巴真正的起点：请求的水位落在存档起点之前时，读回来的是现存的全部。
	start := max(fromSeq, baseSeq)
	whole := fromSeq <= baseSeq

	endSeq := start - 1
	if len(events) > 0 {
		endSeq = events[len(events)-1].Seq
	}

	values := map[string]any{}
	refreshed := Checkpoint{}
	for _, reg := range r.registrations {
		def := reg.def
		row, present := checkpoint[def.key]
		usable := present &&
			row.Ver == def.stateVersion &&
			row.Seq >= start-1 &&
			row.Seq <= endSeq
		if !usable && !whole {
			return Restored{}, fmt.Errorf(
				"%w：投影键 %q 从 seq %d 恢复不了，它的检查点行缺失、版本对不上、或者超出了给进来的日志末尾 %d",
				ErrCheckpointUnusable, def.key, start, endSeq)
		}

		state := def.init()
		from := start - 1
		if usable {
			decoded, err := def.decodeState(row.Val)
			if err != nil {
				return Restored{}, fmt.Errorf("投影键 %q 的检查点行解不开：%w", def.key, err)
			}
			state, from = decoded, row.Seq
		}
		for _, event := range events {
			if event.Seq > from {
				state, _ = def.apply(state, event)
			}
		}

		if def.view != nil {
			values[def.key] = def.view(state)
		}
		encoded, err := json.Marshal(state)
		if err != nil {
			return Restored{}, fmt.Errorf("投影键 %q 的状态排不出去：%w", def.key, err)
		}
		refreshed[def.key] = CheckpointRow{Ver: def.stateVersion, Seq: endSeq, Val: encoded}
	}

	return Restored{
		Snapshot:   Snapshot{AsOfSeq: endSeq, Values: values},
		Checkpoint: refreshed,
	}, nil
}
