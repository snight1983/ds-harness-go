// 本文件的作用：一段 agent 日志是怎么交代它消耗掉的那些活儿的。
//
// 回合和步骤这套词汇本身回答不了这个问题。一个还没进第一个步骤就停掉的回合，
// 它那条 turn/end 的形状和「一次拒绝」或者「一次空认领」产出的那种收支平衡的
// 空回合一模一样，所以孤立地读回合，要么把半途而废的活儿记成已完成，要么把
// 每一个空操作都定罪。缺的那个事实是收件箱自己的记账：[Inbox] 把每一次改动
// 连同 RemovedCount 一起记下来，并且给取消打上 Canceled——那一位把「一个回合
// 认领了它的输入」和「活儿被没跑就丢掉了」分开。
//
// 源: packages/core/agent/src/consumed-work.ts

package agent

import (
	"encoding/json"

	"github.com/snight1983/ds-harness-go/sessionlog"
)

// ConsumedWork 是一段 agent 日志对它消耗掉的活儿的交代。
//
// 源: packages/core/agent/src/consumed-work.ts:17-31
type ConsumedWork struct {
	// End 是最后一个交代得了消耗的**已关闭**回合：要么它进过模型步骤，要么它
	// 认领了收件箱输入然后失败、被停、或者被拒。
	//
	// HasEnd 为假时它没有意义。
	End sessionlog.Event

	// HasEnd 表示确实有这么一个回合。
	//
	// 新增: DSH 是 `end?: SessionEvent<'turn/end'>`，undefined 表示没有。Go 里
	// [sessionlog.Event] 的零值不是一个能和真事件区分开的哨兵（Seq 为 0 是合法
	// 的第一条），所以另拿一位说这件事。理由和 compaction.StartData.Standalone
	// 逐字相同。
	HasEnd bool

	// DroppedUnrun 表示在那个回合之后，还有被接受的活儿没跑就被取消出收件箱了。
	//
	// 这是「一次取消在任何回合能开起来之前就拿走了输入」的唯一交代——没有任何
	// 一条 turn/end 描述得了它。
	DroppedUnrun bool
}

// accountsForClaim 判断「一个消耗了输入但一个步骤都没进的回合」，它的结束方式
// 算不算交代了那份输入。
//
// 源: packages/core/agent/src/consumed-work.ts:33-58
//
// 只有 completed 不算：它那次认领被改写掉之后就没东西可跑了。blocked 算——
// 产出它的那次前置步骤拒绝把认领到的消息丢掉了，那些活儿再也不会跑。
func accountsForClaim(reason sessionlog.TurnEndReason) bool {
	// 源: packages/core/agent/src/consumed-work.ts:51-56。DSH 在 default 上留了
	// 一条注释说明为什么剩下的一律算数：唯一那个没点名的内建理由 max-tokens 要
	// 有步骤才到得了，所以它那个回合在调到这里之前就已经按「进过步骤」短路了；
	// 而 TurnEndReasonMap 是可被合并扩展的，后端新加的变体列不出来——一个说不出
	// 名字的结局压在被消耗掉的输入上，不能读成成功。
	//
	// 本仓库的 [sessionlog.UnknownTurnEnd] 就是那个说不出名字的结局，它自然落进
	// 这里的默认分支。
	return reason.TurnEndReasonKind() != sessionlog.ReasonCompleted
}

// FoldConsumedWork 把一段 agent 日志（或者它自己拥有的一截后缀）折成它对消耗掉
// 的活儿的交代。
//
// 源: packages/core/agent/src/consumed-work.ts:60-108
//
// 单趟扫完，而且**每一样输入都是日志本身**：没有哪个调用方需要在取消之前先去
// 采一遍活状态，于是一次取消不管是谁发的——持有者收尾、祖先中断、一个正在卸载
// 的插件——读出来都一样。
//
// 负载读不回来的事件被跳过而不是报错：这个折叠是一次**尽力而为的交代**，
// 调用它的地方（取消收敛、处置收尾）没有「拒绝这段日志」这个选项。日志本身的
// 完整性由 [sessionlog.ValidateLog] 那一层负责。
func FoldConsumedWork(events []sessionlog.Event) ConsumedWork {
	stepped := make(map[int]struct{})
	claimed := make(map[int]struct{})
	openTurn, hasOpen := 0, false
	var work ConsumedWork

	for _, event := range events {
		switch event.Type {
		case sessionlog.EventTurnStart:
			var data sessionlog.TurnStartData
			if json.Unmarshal(event.Data, &data) == nil {
				openTurn, hasOpen = data.Turn, true
			}

		case sessionlog.EventStepStart:
			var data sessionlog.StepStartData
			if json.Unmarshal(event.Data, &data) == nil {
				stepped[data.Turn] = struct{}{}
			}

		case EventInboxSpliced:
			var data SplicedData
			if json.Unmarshal(event.Data, &data) != nil {
				break
			}
			if data.RemovedCount == 0 {
				break
			}
			switch {
			case data.Canceled:
				// 一次**替换**把活儿以一个新身份留在了待办里，所以只有「取消完
				// 之后什么都没留下」才算把它丢了。
				work.DroppedUnrun = work.DroppedUnrun || len(data.Inserted) == 0
			case hasOpen:
				// 认领是循环自己在步骤边界上的读取，永远发生在一个回合里面。
				claimed[openTurn] = struct{}{}
			}

		case sessionlog.EventTurnEnd:
			var data sessionlog.TurnEndData
			if json.Unmarshal(event.Data, &data) != nil {
				break
			}
			hasOpen = false
			_, hadStep := stepped[data.Turn]
			delete(stepped, data.Turn)
			accounted := hadStep
			if !accounted {
				_, hadClaim := claimed[data.Turn]
				delete(claimed, data.Turn)
				accounted = hadClaim && accountsForClaim(data.Reason)
			}
			if accounted {
				work.End, work.HasEnd = event, true
				// 在这个回合关掉之前丢的任何东西，都由它自己那个结局交代；
				// 只有更晚的那次丢弃才还没人认。
				work.DroppedUnrun = false
			}
		}
	}
	return work
}
