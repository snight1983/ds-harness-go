// 本文件的作用：从一个活会话的稳定前缀分叉出一个子会话——边界怎么算、哪些前缀
// 不许分，以及被拒时说得清是哪一类。
//
// 源: packages/core/session/src/index.ts:760-790、1072-1155

package session

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/snight1983/ds-harness-go/core/scope"
	sessionlog "github.com/snight1983/ds-harness-go/session"
)

// ForkErrorCode 是一次分叉被拒的分类。
//
// 源: packages/core/session/src/index.ts:761-774（SessionForkErrorCode）
type ForkErrorCode string

const (
	// ForkSessionNotFound 是这个来源标识在活存储里不存在。
	ForkSessionNotFound ForkErrorCode = "SESSION_NOT_FOUND"
	// ForkSessionNotLive 是这个来源**对象**不是存储里活着的那一份（同名的另一个）。
	ForkSessionNotLive ForkErrorCode = "SESSION_NOT_LIVE"
	// ForkSessionAlreadyExists 是要给子会话的那个名字已经被占了。
	ForkSessionAlreadyExists ForkErrorCode = "SESSION_ALREADY_EXISTS"
	// ForkInvalidBoundary 是边界不是一个连续存在的 seq。
	ForkInvalidBoundary ForkErrorCode = "INVALID_BOUNDARY"
	// ForkOpenTurn 是选中的前缀停在一个还没关掉的回合中间。
	ForkOpenTurn ForkErrorCode = "OPEN_TURN"
)

// ForkError 是一次分叉被拒。
//
// 源: packages/core/session/src/index.ts:776-782（SessionForkError）
//
// 新增: DSH 是一个 `name = 'SessionForkError'` 的 Error 子类，调用方靠
// `instanceof` 认它。Go 里用 [errors.As] 取出这个类型再读 [ForkError.Code]。
// 五个取值的**用处**在两边一样：调用方按它决定是重试、换个名字、还是把边界往前
// 挪一格。
type ForkError struct {
	// Code 是这次拒绝的分类。
	Code ForkErrorCode
	// Message 是给人看的那句话。
	Message string
}

// Error 实现 error。
func (e *ForkError) Error() string { return e.Message }

// forkError 造一条分叉拒绝。
func forkError(code ForkErrorCode, format string, args ...any) *ForkError {
	return &ForkError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Fork 从一个活会话对象的稳定前缀建一个活的子会话。
//
// 源: packages/core/session/src/index.ts:1072-1108
//
// boundary 是**含**在内的来源事件 seq；传 nil 表示「到来源当前最后一条为止」，
// 而对一个空来源传 nil 就分出一个空子会话。选中的这一段可以停在回合与回合之间的
// 某条事件上，但不许停在一个还开着的回合里。
//
// childID 为空表示让存储自己铸一个名字。
//
// 新增: DSH 的 `fork(source, …)` 收一个 `Session | SessionId` 的联合。Go 里拆成
// 这个方法和 [Store.ForkByID] 两个入口。拆开之后有一件事在签名上就看得见了：
// [ForkSessionNotLive] 只可能从这条路产生——一个只给了名字的调用方压根说不出
// 「我要的是**那一个**对象」。TS 那边这一点要读实现才知道。
func (s *Store) Fork(
	ctx context.Context,
	owner *scope.Scope,
	source *Session,
	boundary *int,
	childID sessionlog.SessionID,
) (*Session, error) {
	if source == nil {
		return nil, forkError(ForkSessionNotFound, "fork source session is nil")
	}
	live, found := s.Get(source.ID())
	if !found {
		return nil, forkError(ForkSessionNotFound, "session %q not found", string(source.ID()))
	}
	if live != source {
		return nil, forkError(ForkSessionNotLive, "session %q is not the live store instance", string(source.ID()))
	}
	return s.forkFrom(ctx, owner, live, boundary, childID)
}

// ForkByID 从一个活会话**标识**的稳定前缀建一个活的子会话。
//
// 源: packages/core/session/src/index.ts:1146-1155（`typeof source === 'string'` 那一支）
//
// 语义和 [Store.Fork] 完全一样，只是来源按名字解析。
func (s *Store) ForkByID(
	ctx context.Context,
	owner *scope.Scope,
	sourceID sessionlog.SessionID,
	boundary *int,
	childID sessionlog.SessionID,
) (*Session, error) {
	live, found := s.Get(sourceID)
	if !found {
		return nil, forkError(ForkSessionNotFound, "session %q not found", string(sourceID))
	}
	return s.forkFrom(ctx, owner, live, boundary, childID)
}

// forkFrom 是两个分叉入口共用的那一段：查子会话重名、切出 seed、建会话。
//
// 源: packages/core/session/src/index.ts:1090-1108
func (s *Store) forkFrom(
	ctx context.Context,
	owner *scope.Scope,
	source *Session,
	boundary *int,
	childID sessionlog.SessionID,
) (*Session, error) {
	// 重名先查：切 seed 是 O(日志长度) 的复制，一个注定要因为重名失败的分叉不必
	// 先把它做一遍。真正权威的那道重名检查在 [Store.Enter] 里，理由见那里。
	if childID != "" {
		if _, taken := s.Get(childID); taken {
			return nil, forkError(ForkSessionAlreadyExists, "session %q already exists", string(childID))
		}
	}
	seed, err := forkSeed(source, boundary)
	if err != nil {
		return nil, err
	}
	header := source.Header()
	return s.Create(ctx, owner, childID, CreateOptions{
		Seed: seed,
		// 子会话继承的是来源那些事件**连同它们的 seq**，所以它的起点就是来源的
		// 起点——来源的头部被弹过一截时那个数不是 0，见 [CreateOptions.BaseSeq]。
		BaseSeq:       source.BaseSeq(),
		Cwd:           header.Cwd,
		ParentSession: source.ID(),
		// 这里 len(seed) 就是血统边界：这一次分叉继承的前缀恰好是它。续跑一个存下来
		// 的会话不走这条路（那条路上 seed 是完整的存储日志，血统边界另有其值），
		// 见 [CreateOptions.SeedLength]。
		SeedLength: len(seed),
	})
}

// forkSeed 切出分叉用的那段前缀。
//
// 源: packages/core/session/src/index.ts:1110-1144
//
// 返回的切片是**非 nil** 的，哪怕它是空的：一个空 seed 说的是「给我建一个带
// session/end-seed 标记的空子会话」，和「压根没给 seed」不是一回事，
// 见 [Options.Seed]。
func forkSeed(session *Session, requested *int) ([]sessionlog.Event, error) {
	events := session.Events()

	var boundary int
	if requested != nil {
		boundary = *requested
	} else {
		if len(events) == 0 {
			return []sessionlog.Event{}, nil
		}
		boundary = events[len(events)-1].Seq
	}

	if boundary < 0 {
		// 新增: DSH 这里验的是 Number.isSafeInteger 加非负。Go 的 int 逐位精确，
		// 只剩下非负这一半；诊断照抄那句话，因为它说的仍然是同一件事。
		return nil, forkError(
			ForkInvalidBoundary,
			"fork boundary for session %q must be a non-negative safe integer, got %d",
			string(session.ID()), boundary,
		)
	}

	// 新增: 上游把 boundary 直接当下标使（`events[boundary]`、`events.slice(0, boundary+1)`），
	// 因为它那边「seq 恒等于下标」。本仓库的日志会从最老的一头弹出事件
	//（见 docs/session-log-limit.md），于是 boundary 是个 seq，要先减掉起点。
	baseSeq := session.BaseSeq()
	index := boundary - baseSeq
	if index < 0 {
		// 这一段已经被弹掉了。分叉要的正是这些事件本身，不是从它们折出来的状态，
		// 所以这里没有「残缺着往下走」这条路，只能如实说它不在了。
		return nil, forkError(
			ForkInvalidBoundary,
			"fork boundary %d has been evicted from session %q (earliest seq: %d)",
			boundary, string(session.ID()), baseSeq,
		)
	}
	if index >= len(events) {
		lastSeq := "none"
		if len(events) > 0 {
			lastSeq = strconv.Itoa(events[len(events)-1].Seq)
		}
		return nil, forkError(
			ForkInvalidBoundary,
			"fork boundary %d does not exist in session %q (last seq: %s)",
			boundary, string(session.ID()), lastSeq,
		)
	}
	if events[index].Seq != boundary {
		// 「seq 恒等于起点加下标」是本包的连续性契约，这里再验一遍是因为分叉是一道会
		// **生出新会话**的边界：一份不连续的日志切出来的 seed 建不起来，与其让
		// 它在构造里报一句关于 seed 的话，不如在这里说清是边界对不上。
		return nil, forkError(
			ForkInvalidBoundary,
			"fork boundary %d does not match a contiguous event seq in session %q",
			boundary, string(session.ID()),
		)
	}

	if err := rejectOpenTurn(session, events, index); err != nil {
		return nil, err
	}
	return slices.Clone(events[:index+1]), nil
}

// rejectOpenTurn 挡住停在一个还开着的回合里的边界。
//
// 源: packages/core/session/src/index.ts:1132-1140
//
// 从边界往回找最近的那条回合边界事件：找到的是 turn/start 就说明这个回合还没关，
// 是 turn/end 或者一条都没有就说明这段前缀停在回合之间。
//
// boundary 在这里是**下标**，不是 seq：调用方已经减过起点了。诊断里报的仍是 seq。
//
// 新增: DSH 是 `slice(0, boundary+1).findLast(…)`，那一步会先复制一份前缀。
// Go 这边倒着扫到第一条就停，不复制。
func rejectOpenTurn(session *Session, events []sessionlog.Event, boundary int) error {
	boundarySeq := events[boundary].Seq
	for index := boundary; index >= 0; index-- {
		event := events[index]
		if event.Type == sessionlog.EventTurnEnd {
			return nil
		}
		if event.Type != sessionlog.EventTurnStart {
			continue
		}
		var data sessionlog.TurnStartData
		if err := json.Unmarshal(nonEmptyData(event.Data), &data); err != nil {
			// 负载读不回来时不放行：这条 turn/start 摆在这儿就说明回合是开着的，
			// 读不出回合号只是让诊断少一个数字，不改变「不许分」这件事。
			return forkError(
				ForkOpenTurn,
				"fork boundary %d in session %q ends inside an open turn whose payload is unreadable: %v",
				boundarySeq, string(session.ID()), err,
			)
		}
		return forkError(
			ForkOpenTurn,
			"fork boundary %d in session %q ends inside open turn %d",
			boundarySeq, string(session.ID()), data.Turn,
		)
	}
	return nil
}
