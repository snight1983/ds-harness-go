// 本文件的作用：收件箱那条耐久事件的词汇——两条待办清单各自的名字、
// 事件类型、以及它的负载。
//
// 源: packages/core/agent/src/types.ts

package agent

import (
	"encoding/json"
	"fmt"

	"ds-harness-go/llm"
	sessionlog "ds-harness-go/session"
)

// InboxTarget 是一个 agent 手上那两条有序待办清单之一。
//
// 源: packages/core/agent/src/types.ts:10
type InboxTarget string

const (
	// NextTurn 是排着队等各自回合的提示。
	NextTurn InboxTarget = "next-turn"
	// NextStep 是等下一个步骤边界的输入。
	NextStep InboxTarget = "next-step"
)

// EventInboxSpliced 是一次规整过的收件箱改动。
//
// 源: packages/core/agent/src/types.ts:12-27
//
// 它**不上表面**：收件箱里的消息还没进模型可见历史，进去那一刻记的是
// [sessionlog.EventUserMessage]。这一条记的是「谁在什么位置删了几条、插了几条」，
// 而这份记账是 [FoldConsumedWork] 唯一的依据。
const EventInboxSpliced sessionlog.EventType = "agent/inbox/spliced"

// EventTypes 是本包往会话日志里写的那些事件类型。
//
// 新增: DSH 靠 `declare module` 把这个类型合并进 SessionEventMap，全局登记表
// 因此自动认得它。Go 没有声明合并，[sessionlog.Vocabulary] 也是个闭合的值，
// 所以改成由本包交出这张单子，装配方自己拼：
//
//	vocabulary := sessionlog.CoreVocabulary().With(agent.EventTypes()...)
//
// 不这么做的话，一段带收件箱改动的日志会被 [sessionlog.CheckVocabulary] 判成
// 「有不认识的事件类型」而整个拒掉。
func EventTypes() []sessionlog.EventType {
	return []sessionlog.EventType{EventInboxSpliced}
}

// SplicedData 是 [EventInboxSpliced] 的负载。
//
// 源: packages/core/agent/src/types.ts:19-25
//
// 这里的坐标是**规整过的**：Start 已经被夹进 [0, 长度]，RemovedCount 已经被夹进
// [0, 长度-Start]。JS 的 Array.prototype.splice 接受负数下标和超界的删除数，
// [Inbox.Splice] 在写这条事件之前就把它们折算完了，所以日志里存的永远是一份
// 可以直接照做的坐标——回放的一方不必再实现一遍 JS 那套夹取规则。
type SplicedData struct {
	// Target 是这次改动落在哪条清单上。
	Target InboxTarget
	// Start 是改动的起点下标。
	Start int
	// RemovedCount 是从 Start 起删掉的条数，0 表示这是一次纯插入。
	RemovedCount int
	// Inserted 是在 Start 处插进去的那些消息，可以是空的。
	Inserted []llm.Message
	// Canceled 表示删掉的那些是被**取消**掉的，不是被认领走的。
	//
	// 新增: DSH 是 `outcome?: 'canceled'`，一个只有一个成员的可选联合。Go 里
	// 一个布尔说的是同一件事，而且不必为「读到一个没见过的 outcome 值怎么办」
	// 留一条路——见 [SplicedData.UnmarshalJSON] 上的注释。
	//
	// 这个区分是 [FoldConsumedWork] 的地基：被认领走的工作由它所属那个回合的
	// turn/end 交代，被取消掉的工作没有任何 turn/end 交代得了，只能靠这一位。
	Canceled bool
}

// splicedOutcomeCanceled 是 Canceled 为真时 outcome 字段的值。
const splicedOutcomeCanceled = "canceled"

// splicedWire 是 [SplicedData] 在介质上的样子。
type splicedWire struct {
	Target       InboxTarget   `json:"target"`
	Start        int           `json:"start"`
	RemovedCount int           `json:"removedCount,omitempty"`
	Inserted     []llm.Message `json:"inserted"`
	Outcome      string        `json:"outcome,omitempty"`
}

// MarshalJSON 把这条负载排出去。
//
// RemovedCount 为 0 时 removedCount 整个不出现，Canceled 为假时 outcome 整个
// 不出现——两处的 omitempty 都精确复刻 DSH 那两个条件展开
// （types.ts:22、24 是可选字段，inbox.ts:181、183 是那两个 `? {} :` 三元）。
func (d SplicedData) MarshalJSON() ([]byte, error) {
	if d.Target != NextTurn && d.Target != NextStep {
		return nil, fmt.Errorf("%w：%s：清单名 %q 不认识",
			ErrMalformedEvent, EventInboxSpliced, d.Target)
	}
	inserted := d.Inserted
	if inserted == nil {
		// inserted 在 DSH 那边是必填数组，空的时候是 `[]` 不是 `undefined`。
		// Go 的 nil 切片排出来是 null，那会让回放的一方读到一个不是数组的值。
		inserted = []llm.Message{}
	}
	outcome := ""
	if d.Canceled {
		outcome = splicedOutcomeCanceled
	}
	return json.Marshal(splicedWire{
		Target:       d.Target,
		Start:        d.Start,
		RemovedCount: d.RemovedCount,
		Inserted:     inserted,
		Outcome:      outcome,
	})
}

// UnmarshalJSON 把一段字节读回这条负载。
//
// 新增: DSH 这条负载在读的一侧一次都没验过——它是 SessionEventMap 上的一个
// 类型声明，运行期只有 [Inbox.validate] 顺手验了坐标。Go 这边多验两样：
//
//   - 清单名必须是那两个之一。读到第三个名字，投影会往一条不存在的清单上写，
//     那份 agent 的收件箱从此和日志对不上，而且不会有任何东西报警。
//   - outcome 只认识空和 "canceled"。这个字段直接决定 [FoldConsumedWork] 报不报
//     droppedUnrun，读到一个没学过的结局而当它是「没取消」，等于把一段被丢掉的
//     工作静默记成已完成。按 [sessionlog.FormatVersion] 的规矩，新增一个结局
//     是一次核心事件语义改动，版本检查会先一步拦住，这里是第二道。
func (d *SplicedData) UnmarshalJSON(data []byte) error {
	var wire splicedWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("%w：%s：%w", ErrMalformedEvent, EventInboxSpliced, err)
	}
	if wire.Target != NextTurn && wire.Target != NextStep {
		return fmt.Errorf("%w：%s：清单名 %q 不认识",
			ErrMalformedEvent, EventInboxSpliced, wire.Target)
	}
	if wire.Outcome != "" && wire.Outcome != splicedOutcomeCanceled {
		return fmt.Errorf("%w：%s：结局 %q 不认识",
			ErrMalformedEvent, EventInboxSpliced, wire.Outcome)
	}
	d.Target = wire.Target
	d.Start = wire.Start
	d.RemovedCount = wire.RemovedCount
	d.Inserted = wire.Inserted
	d.Canceled = wire.Outcome == splicedOutcomeCanceled
	return nil
}
