// 本文件的作用：把「当前的动态运行期上下文」投影成会话表面上的一条用户消息，
// 并且只在它**变了**的时候才提出一条新的。
//
// 源: packages/core/agent-loop/src/runtime-context.ts:1-76

package agentloop

import (
	"context"
	"fmt"
	"sync"

	"ds-harness-go/core/scope"
	"ds-harness-go/core/session"
	"ds-harness-go/llm"
	sessionlog "ds-harness-go/session"
)

// RuntimeContextSource 是这些投影出来的消息署的插件名。
//
// 源: packages/core/agent-loop/src/runtime-context.ts:12
//
// 这个字符串照抄 DSH，一个字符都没改：它进了会话日志，是本投影**认领自己那些
// 消息**的唯一判据（见 isOwnedRuntimeContext）。改掉它等于让一份既有日志里的
// 快照全部变成别人的消息——投影会以为一条都没投过，于是重新投一遍，
// 而旧的那些还留在表面上。
const RuntimeContextSource = "@deepseek-ai/dsh-system-prompt"

// runtimeContextCleared 是「现在没有动态上下文」这件事的措辞。
//
// 源: packages/core/agent-loop/src/runtime-context.ts:13
//
// 它必须是一句**正面陈述**而不是「不发」：早先那些快照还留在表面上，模型看得见。
// 不发新的等于让模型继续按一份已经作废的快照行事。
const runtimeContextCleared = "Current runtime context: none. Earlier runtime-context snapshots no longer apply."

// retainedSnapshot 是表面上那条还有效的快照。
type retainedSnapshot struct {
	// seq 是它那条事件的序号，用来认出「哪一次替换把它盖掉了」。
	seq int
	// text 是它的正文。
	text string
	// hasText 表示这条消息取得出正文——它的内容恰好是一块文本。
	//
	// 新增: DSH 那边 textOf 返回 `string | undefined`，取不出来时是 undefined，
	// 而 undefined 和任何一份新快照都不相等，于是必然重投一条。Go 的空串是一个
	// 合法的正文，所以「取不出来」得另外记一位，否则一条内容为空的快照会被当成
	// 「正文是空串」，和一份空的新快照比出相等，然后**不投**——那条本该刷新的
	// 上下文就永远停在旧值上。
	hasText bool
}

// RuntimeContextProjection 记着表面上最后那份留存的运行期上下文快照，
// 但不负责提交任何东西。
//
// 源: packages/core/agent-loop/src/runtime-context.ts:24-76
//
// 「提出」和「提交」分开是这个类型存在的理由：[RuntimeContextProjection.Project]
// 只交出一条**候选**消息，真正把它追加进日志的是循环那一步的前置钩子——那条消息
// 得和这一步认领到的那批消息一起、按同一个次序进表面。投影自己去追加的话，
// 它会插在那批消息中间某个说不清的位置。
//
// 新增: 这个类型是并发安全的。DSH 是单线程 JS，投影和事件回调不可能同时跑；
// Go 这边追加事件的观察者由 [ds-harness-go/core/session.Session.Append] 在
// 追加方那个 goroutine 上叫起来，而 Project 由循环那个 goroutine 叫，
// 两者是两条链，所以状态用锁护住。
type RuntimeContextProjection struct {
	// mutex 护住下面那两个字段，理由见类型注释。
	mutex sync.Mutex
	// retained 是表面上那条还有效的快照；nil 表示一条都不留。
	retained *retainedSnapshot
	// everSeen 表示这个会话上**曾经**投出过快照。
	//
	// 新增: DSH 用 `undefined | null | value` 三态表达同一件事，Go 里没有第三种
	// 空，所以拆成一个指针加一位布尔。这一位不是冗余的：从没投过的时候，
	// 一份空的上下文什么都不用说（没人需要被告知「作废」）；投过又清空的时候，
	// 必须发那句 runtimeContextCleared。两种情况的 retained 都是 nil。
	everSeen bool
}

// NewRuntimeContextProjection 从日志里恢复一次投影状态，之后跟着权威事件走。
//
// 源: packages/core/agent-loop/src/runtime-context.ts:31-58
//
// 恢复是**从后往前**扫的：本投影只关心最后那一条自己的消息，扫到第一条还在
// 表面上的就停。扫到了自己的消息、但它已经不在表面上（被压缩之类的替换盖掉了），
// 那就只记下 everSeen——曾经有过，但现在一条都不留。
//
// 事件订阅挂在 owner 这个作用域上，随它一起释放，所以这里不把撤销函数交出去。
func NewRuntimeContextProjection(
	ctx context.Context,
	owner *scope.Scope,
	sessions *session.Store,
	live *session.Session,
) (*RuntimeContextProjection, error) {
	if sessions == nil || live == nil {
		return nil, fmt.Errorf("core/agentloop: 运行期上下文投影要有会话存储和一个活会话")
	}
	projection := &RuntimeContextProjection{}
	projection.restore(live)

	if _, err := sessions.OnEvent(ctx, owner, func(subject *session.Session, event sessionlog.Event) {
		if subject != live {
			return
		}
		projection.observe(event)
	}); err != nil {
		return nil, fmt.Errorf("core/agentloop: 挂运行期上下文投影的事件观察者失败：%w", err)
	}
	return projection, nil
}

// restore 从既有日志里把投影状态扫回来。
//
// 源: packages/core/agent-loop/src/runtime-context.ts:32-42
func (p *RuntimeContextProjection) restore(live *session.Session) {
	surface := make(map[int]struct{})
	for _, seq := range live.SurfaceNodes() {
		surface[seq] = struct{}{}
	}

	events := live.Events()
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		message, ok := ownedRuntimeContext(event)
		if !ok {
			continue
		}
		p.everSeen = true
		if _, onSurface := surface[event.Seq]; onSurface {
			text, hasText := textOfMessage(message)
			p.retained = &retainedSnapshot{seq: event.Seq, text: text, hasText: hasText}
			break
		}
	}
}

// observe 跟着一条刚追加的权威事件更新投影状态。
//
// 源: packages/core/agent-loop/src/runtime-context.ts:44-57
func (p *RuntimeContextProjection) observe(event sessionlog.Event) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if message, ok := ownedRuntimeContext(event); ok {
		text, hasText := textOfMessage(message)
		p.everSeen = true
		p.retained = &retainedSnapshot{seq: event.Seq, text: text, hasText: hasText}
		return
	}
	if p.retained == nil || !sessionlog.IsReplacementSurfaceEvent(event) {
		return
	}
	// 一次替换盖掉了留存的那条，表面上就没有快照了——但 everSeen 不回退，
	// 下一次清空仍然要发那句作废陈述。
	for _, seq := range event.SourceEventSeqs {
		if seq == p.retained.seq {
			p.retained = nil
			return
		}
	}
}

// Project 只在留存的那份和现在这份不一样时，造一条**还没提交**的候选消息。
//
// 源: packages/core/agent-loop/src/runtime-context.ts:59-75
//
// current 是整份渲染好的动态上下文，sections 是拼出它的那些具名贡献。
// 第二个返回值为假表示这一步不需要更新。
func (p *RuntimeContextProjection) Project(
	current string,
	sections []llm.ContextSnapshotSection,
) (llm.Message, bool) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if !p.everSeen && current == "" {
		// 从没投过、现在也没有：没人需要被告知任何事。
		return llm.Message{}, false
	}
	snapshot := current
	if snapshot == "" {
		snapshot = runtimeContextCleared
	}
	if p.retained != nil && p.retained.hasText && p.retained.text == snapshot {
		return llm.Message{}, false
	}

	// 那句作废陈述没有任何贡献可归属，所以它不带 snapshot 形态。
	var source llm.MessageSource = llm.PluginSource{Plugin: RuntimeContextSource}
	if len(sections) > 0 {
		source = llm.PluginSource{
			Plugin:  RuntimeContextSource,
			Context: llm.SnapshotContext{Sections: sections},
		}
	}
	return llm.NewUserMessage(llm.Content{llm.TextBlock{Text: snapshot}}, source), true
}

// ownedRuntimeContext 判断一条事件是不是本投影自己投出去的那种消息。
//
// 源: packages/core/agent-loop/src/runtime-context.ts:15-17
func ownedRuntimeContext(event sessionlog.Event) (llm.Message, bool) {
	if event.Type != sessionlog.EventUserMessage {
		return llm.Message{}, false
	}
	data, err := sessionlog.DecodeData(event)
	if err != nil {
		// 走不到：这条事件已经在追加时排成过 JSON。真出了岔子也只能当成
		// 「不是我的」跳过——投影没有报错的出口，而误认成自己的更坏。
		return llm.Message{}, false
	}
	payload, ok := data.(sessionlog.UserMessageData)
	if !ok {
		return llm.Message{}, false
	}
	plugin, isPlugin := payload.Message.Source.(llm.PluginSource)
	if !isPlugin || plugin.Plugin != RuntimeContextSource {
		return llm.Message{}, false
	}
	return payload.Message, true
}

// textOfMessage 取出一条恰好由一块文本组成的消息的正文。
//
// 源: packages/core/agent-loop/src/runtime-context.ts:19-22
func textOfMessage(message llm.Message) (string, bool) {
	if len(message.Content) != 1 {
		return "", false
	}
	block, ok := message.Content[0].(llm.TextBlock)
	if !ok {
		return "", false
	}
	return block.Text, true
}
