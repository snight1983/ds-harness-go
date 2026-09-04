// 本文件的作用：把运行时那几条边翻成线上的话——已提交的会话事件、收件箱认领、
// 回合出错和适配器变更，各自该往对面推什么。
//
// 源: packages/acp/acp/src/index.ts

package acp

import (
	"context"
	"fmt"

	wire "github.com/coder/acp-go-sdk"

	"github.com/snight1983/ds-harness-go/harness/agent"
	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/tools"
)

// notify 把一条会话更新发出去，发不出去只记一行。
//
// 源: packages/acp/acp/src/index.ts:148-156
//
// 通知本来就是单向的：一条发失败的更新改变不了运行时里已经发生的那件事，更不该把发出
// 这条边的那次追加带崩。
func (b *Bridge) notify(ctx context.Context, notification wire.SessionNotification) {
	b.mutex.Lock()
	peer := b.peer
	b.mutex.Unlock()
	if peer == nil {
		// 还没装上（或已经拆干净了）：这条线上没有对面可发。
		return
	}
	if err := peer.SessionUpdate(ctx, notification); err != nil {
		b.config.warn(fmt.Sprintf("acp: session/update 发不出去：%v", err))
	}
}

// onSessionEvent 把**已提交**的助手消息、推理、工具调用与结果翻上线，顺便记下相关
// 回合的结束理由并放掉那个回合上的模型钉住。
//
// 源: packages/acp/acp/src/session.ts:229-268（onSessionEvent）
//
// 原始流片段、计划、标题、重试标记仍然一条都不发：那些是呈现数据，不属于这条自动化
// 线。发出去的这四样都是**已提交**的耐久事实，DSH 上游发的也正是这四样。每条会话一
// 条投递链，好让块与消息的先后次序扛得住中途那些异步的附件读取。
//
// 新增: DSH 那个 `finally` 是为了「助手那一支抛了也照样记结束理由」。Go 这边几种事件
// 类型互斥，一条事件不可能既是 assistant/message 又是 turn/end，所以那层 finally 收敛
// 成了并列的分支。
//
// 新增: 下面那几处类型断言测不到。[sessionlog.DecodeData] 按事件类型派发，
// `assistant/message` 解出来的**只可能**是 [sessionlog.AssistantMessageData]，另外三种
// 同理——要走到那几个 `return` 上，得先把事件类型和负载类型的对应关系改错。那正是留着
// 它们的理由：DSH 那边是 TS 的判别联合，编译器替它挡住了这件事；Go 这边 `any` 挡不住，
// 而一次静默的错配会让这条线**无声地**少发一条更新、或者少记一次回合结束。
func (b *Bridge) onSessionEvent(sess *coresession.Session, event sessionlog.Event) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	record, ok := b.sessions[sess.ID()]
	if !ok || record.agent.Session() != sess {
		return
	}
	data, err := sessionlog.DecodeData(event)
	if err != nil {
		b.config.warn(fmt.Sprintf("acp: %s 解不开：%v", event.Type, err))
		return
	}
	switch event.Type {
	case sessionlog.EventAssistantMessage:
		message, typed := data.(sessionlog.AssistantMessageData)
		if !typed {
			return
		}
		b.scheduleLocked(record, turnOwner(record, message.Turn), func(ctx context.Context) ([]wire.SessionUpdate, error) {
			return AssistantUpdates(ctx, b.config.Attachments, b.config.Meter, sess, message)
		})
	case sessionlog.EventToolCall:
		call, typed := data.(sessionlog.ToolCallData)
		if !typed {
			return
		}
		b.scheduleLocked(record, turnOwner(record, call.Turn), func(context.Context) ([]wire.SessionUpdate, error) {
			return []wire.SessionUpdate{ToolCallUpdate(call)}, nil
		})
	case sessionlog.EventToolResult:
		result, typed := data.(sessionlog.ToolResultData)
		if !typed {
			return
		}
		b.scheduleLocked(record, turnOwner(record, result.Turn), func(ctx context.Context) ([]wire.SessionUpdate, error) {
			update, onWire, err := ToolResultUpdate(ctx, b.config.Attachments, result)
			if err != nil || !onWire {
				return nil, err
			}
			return []wire.SessionUpdate{update}, nil
		})
	case sessionlog.EventTurnEnd:
		end, typed := data.(sessionlog.TurnEndData)
		if !typed {
			return
		}
		inflight := record.inflight
		if inflight != nil && inflight.hasTurn && inflight.turn == end.Turn {
			inflight.endReason = end.Reason
		}
		// 这个回合的每一步都跑完了，钉住的那份路由到此为止——之后装配用的是这条会话
		// 当下**配置成**的那一份。ReleaseTurn 只认这个确切的回合号，所以一次迟到的
		// turn/end 放不掉后面那个回合的钉住。
		if record.control != nil {
			record.control.ReleaseTurn(end.Turn)
		}
	}
}

// scheduleLocked 把一节待发的更新接到这条会话那条投递链的尾巴上。
//
// 源: packages/acp/acp/src/session.ts:232-266
//
// build 在这条链轮到自己的时候才跑：附件读取是异步的，先转完再排队会让两条消息的
// 块次序跟着读取快慢乱掉。owner 为 nil 表示这一节转不动时不算在任何一次提示词头上。
//
// 调用方必须攥着 [Bridge.mutex]。接上去这一步在锁里做完，所以两条同时到的事件拿到的
// 是两个前后相接的位置，谁也插不到谁前面去。
func (b *Bridge) scheduleLocked(
	record *sessionRecord,
	owner *inflightPrompt,
	build func(context.Context) ([]wire.SessionUpdate, error),
) {
	previous := record.outputTail
	next := make(chan struct{})
	record.outputTail = next

	sessionID := record.agent.ID()
	go func() {
		defer close(next)
		<-previous
		b.deliver(sessionID, build, owner)
	}()
}

// deliver 转出这一节的那几条更新并按序发出去。
//
// 源: packages/acp/acp/src/session.ts:236-243
//
// 转换整节一起做：DSH 的 `assistantUpdates` 是一个交回整个数组的异步函数，一个块转
// 不动就整条消息一条都不发。失败记在那次提示词名下并写一行日志。
func (b *Bridge) deliver(
	sessionID sessionlog.SessionID,
	build func(context.Context) ([]wire.SessionUpdate, error),
	owner *inflightPrompt,
) {
	b.mutex.Lock()
	ctx := b.notifyCtx
	b.mutex.Unlock()
	if ctx == nil {
		return
	}
	updates, err := build(ctx)
	if err != nil {
		b.mutex.Lock()
		if owner != nil && owner.outputError == nil {
			owner.outputError = err
		}
		b.mutex.Unlock()
		b.config.warn(fmt.Sprintf("acp: 助手输出转不动：%v", err))
		return
	}
	for _, update := range updates {
		b.notify(ctx, wire.SessionNotification{SessionId: wire.SessionId(sessionID), Update: update})
	}
}

// onAdaptersUpdated 在 LLM 拓扑变了之后，把每条会话那份配置项状态重新推给对面。
//
// 源: packages/acp/acp/src/session.ts:270-284（topologyChanged）
//
// 选项在链**外**算：翻目录要问适配器，那一步可能很慢，不该把这条会话的助手输出堵住。
// 算完之后那条通知才排上链，于是它和已经排在前面的输出之间仍然是有序的。
// 两处失败都只记一行：一份推不出去的配置改变不了运行时里已经变了的拓扑。
func (b *Bridge) onAdaptersUpdated() {
	b.mutex.Lock()
	ctx := b.notifyCtx
	records := make([]*sessionRecord, 0, len(b.sessions))
	for _, record := range b.sessions {
		if record.control != nil {
			records = append(records, record)
		}
	}
	b.mutex.Unlock()
	if ctx == nil {
		return
	}
	for _, record := range records {
		go b.pushConfigOptions(ctx, record)
	}
}

// pushConfigOptions 算一条会话当下那份配置项状态，然后把它排上那条投递链。
//
// 源: packages/acp/acp/src/session.ts:271-283
func (b *Bridge) pushConfigOptions(ctx context.Context, record *sessionRecord) {
	options, err := record.control.Options(ctx)
	if err != nil {
		b.config.warn(fmt.Sprintf("acp: 会话 %s 的配置项算不出来：%v", record.agent.ID(), err))
		return
	}
	b.mutex.Lock()
	if b.sessions[record.agent.ID()] != record {
		// 这条会话在算选项的这段时间里被关掉了。
		b.mutex.Unlock()
		return
	}
	b.scheduleLocked(record, nil, func(context.Context) ([]wire.SessionUpdate, error) {
		return []wire.SessionUpdate{{
			ConfigOptionUpdate: &wire.SessionConfigOptionUpdate{ConfigOptions: options},
		}}, nil
	})
	b.mutex.Unlock()
}

// onInboxClaimed 把这次提示词那条消息落到的那个回合号记下来，并把这条消息排队时钉的
// 那份路由按到这个回合上。
//
// 源: packages/acp/acp/src/session.ts:286-296（onInboxClaimed）
//
// 之后的结束理由、输出失败归属、以及"区间内还是区间外的错误"三件事，全靠这个回合号
// 认人。
//
// 钉住那一半管的是另一件事：一条消息排队和它被认领之间，对面可以改模型。钉住让这个
// 回合的每一步走的都是排队那一刻那条路由——对面看到的和下一次装配抓的仍然是改完之后
// 那一份，所以回合中途改模型不会把正在跑的这一步换掉。
func (b *Bridge) onInboxClaimed(live agent.Agent, message llm.Message, turn int) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	record := b.ownedRecordLocked(live)
	if record == nil {
		return
	}
	if inflight := record.inflight; inflight != nil && inflight.messageID == message.ID {
		inflight.turn = turn
		inflight.hasTurn = true
	}
	if record.control == nil {
		return
	}
	selection, pinned := record.pendingSelections[message.ID]
	if !pinned {
		return
	}
	delete(record.pendingSelections, message.ID)
	record.control.PinTurn(turn, selection)
}

// onAgentError 认领一次落在**相关回合之外**的区间失败，并当场开始结算。
//
// 源: packages/acp/acp/src/index.ts:260-266
//
// 相关回合**之内**的失败不走这里：那种失败会在 turn/end 上留下一个 error 理由，由结算
// 那一步读出来。还没进耐久收件箱的提示词也不算——那时候这个 agent 上跑的一切都和它
// 无关。
func (b *Bridge) onAgentError(failure agent.TurnError) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	record := b.ownedRecordLocked(failure.Agent)
	if record == nil {
		return
	}
	inflight := record.inflight
	if inflight == nil || !inflight.messageQueued {
		return
	}
	if inflight.hasTurn && inflight.turn == failure.Turn {
		return
	}
	inflight.agentError = failure.Err
	b.settleAfterQuiescenceLocked(record, inflight)
}

// answerApproval 把一次要人拍板的问话转给对面，只给一次性的两档。
//
// 源: packages/acp/acp/src/index.ts:271-285
//
// 这条线是给 ACP 客户端走的**机器策略**通道。它绝不从一个认不出的回答里推出一份耐久的
// 授权：不是 allow-once 就是拒。
//
// 新增: DSH 用 `request.agent` 直接查那张会话表——它那边 agent 就是键。Go 里
// [github.com/snight1983/ds-harness-go/tools.ApprovalRequest.Agent] 是一把 [scope.Key]，所以这里按那把钥匙
// 扫一遍自己名下的会话。这张表就是这条连接开出来的那些会话，量级是个位数。
func (b *Bridge) answerApproval(
	ctx context.Context,
	request tools.ApprovalRequest,
	next func() (tools.ApprovalOutcome, error),
) (tools.ApprovalOutcome, error) {
	b.mutex.Lock()
	var record *sessionRecord
	for _, candidate := range b.sessions {
		if candidate.agent.Scope().Key() == request.Agent {
			record = candidate
			break
		}
	}
	peer := b.peer
	b.mutex.Unlock()

	if record == nil || peer == nil || request.CallID == "" {
		return next()
	}
	response, err := peer.RequestPermission(ctx, wire.RequestPermissionRequest{
		SessionId: wire.SessionId(record.agent.ID()),
		ToolCall:  wire.ToolCallUpdate{ToolCallId: wire.ToolCallId(request.CallID)},
		Options: []wire.PermissionOption{
			{OptionId: optionAllowOnce, Name: "Allow once", Kind: wire.PermissionOptionKindAllowOnce},
			{OptionId: optionRejectOnce, Name: "Reject", Kind: wire.PermissionOptionKindRejectOnce},
		},
	})
	if err != nil {
		return "", fmt.Errorf("acp: 问对面要许可失败：%w", err)
	}
	switch {
	case response.Outcome.Cancelled != nil:
		return tools.ApprovalCancelled, nil
	case response.Outcome.Selected != nil && response.Outcome.Selected.OptionId == optionAllowOnce:
		return tools.ApprovalAllowedOnce, nil
	default:
		return tools.ApprovalRejected, nil
	}
}
