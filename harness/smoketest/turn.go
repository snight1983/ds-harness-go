// 本文件的作用：驱一轮，把那一轮的最终文本和 token 记账收出来。
//
// 源: packages/test-support/loader-smoke/src/agent-turn.ts

package smoketest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// Deps 是驱一轮要用到的那几样。
//
// 源: packages/test-support/loader-smoke/src/agent-turn.ts:56（那个 ctx 入参）
//
// 新增: 上游从一份 cordis 上下文上取 `agents` 和 `sessions`。Go 里没有那张服务表，
// 于是摊成三个字段——顺带把「事件观察者登记在谁名下」这件在 cordis 里隐含的事
// 挑明了：[scope.NewRoot] 造的作用域看得见每一个会话，有身份的只看得见自己那一层。
type Deps struct {
	// Agents 是那棵树的 agent 注册表，顶层 agent 从它身上取。
	Agents *agent.Registry
	// Sessions 是会话存储：事件从它身上听，刷盘也走它。
	Sessions *session.Store
	// Owner 是登记事件观察者的作用域，跑完这一轮就撤销。
	Owner *scope.Scope
}

// TurnOptions 是一轮冒烟的输入。
//
// 源: packages/test-support/loader-smoke/src/agent-turn.ts:20-23（FixtureTurnOptions）
type TurnOptions struct {
	// Task 是送进去的那句话。
	Task string
	// OnEvent 按落地次序看见这一轮里的每一条会话事件；nil 表示不看。
	//
	// 新增: 它在收集器那把锁里被调用，为的是「看见的次序就是落地的次序」。
	// 所以它只该记账：在里面反手往同一个会话上追加事件会当场自锁。
	OnEvent func(id sessionlog.SessionID, event sessionlog.Event)
}

// TurnResult 是一轮冒烟的产出。
//
// 源: packages/test-support/loader-smoke/src/agent-turn.ts:12-17（FixtureTurnResult）
//
// 新增: 上游那个 `type: 'result'` 判别标签不移——它是给 JS 侧联合类型认身份用的，
// Go 里这个结构体自己就是那个身份。
type TurnResult struct {
	// SessionID 是跑这一轮的那个会话。
	SessionID sessionlog.SessionID
	// Output 是这一轮里最后那条助手消息的可见文本。
	Output string
	// Usage 是这一轮各步骤记账之和；nil 表示一个步骤都没报过。
	//
	// 新增: 用指针的理由和 [sessionlog.AssistantMessageData.Usage] 一样——
	// 「没人报过」和「报了一份全零」不是一回事。
	Usage *llm.TokenUsage
}

// RunTurn 送一句话进那棵树，等它整个静下来，把最终文本和用量交回来。
//
// 源: packages/test-support/loader-smoke/src/agent-turn.ts:56-100（runFixtureTurn）
//
// 次序是刻意的：先等静下来，再挂观察者，然后才把消息送进去——反过来的话，这一轮
// 收到的第一批事件可能属于上一轮还没收敛的活动。
func RunTurn(ctx context.Context, deps Deps, options TurnOptions) (TurnResult, error) {
	if deps.Agents == nil || deps.Sessions == nil || deps.Owner == nil {
		return TurnResult{}, errors.New("harness/smoketest: agent 注册表、会话存储和作用域都得给")
	}
	live, err := onlyRoot(deps.Agents)
	if err != nil {
		return TurnResult{}, err
	}
	if err := live.WhenIdle(ctx); err != nil {
		return TurnResult{}, fmt.Errorf("harness/smoketest: 等 agent 先静下来失败：%w", err)
	}

	message := llm.NewUserMessage(llm.Content{llm.TextBlock{Text: options.Task}}, llm.UserSource{})
	collector := &turnCollector{
		session:   live.Session(),
		messageID: message.ID,
		onEvent:   options.OnEvent,
		usage:     map[stepKey]llm.TokenUsage{},
	}
	detach, err := deps.Sessions.OnEvent(ctx, deps.Owner, collector.observe)
	if err != nil {
		return TurnResult{}, fmt.Errorf("harness/smoketest: 装事件观察者失败：%w", err)
	}

	live.Followup(message)
	idleErr := live.WhenIdle(ctx)
	// 观察者无论这一轮怎么收场都要撤掉，否则它会跟着作用域一直听下去。
	detachErr := detach(ctx)

	if idleErr != nil {
		return TurnResult{}, fmt.Errorf("harness/smoketest: 等这一轮跑完失败：%w", idleErr)
	}
	if detachErr != nil {
		return TurnResult{}, fmt.Errorf("harness/smoketest: 撤事件观察者失败：%w", detachErr)
	}
	if _, err := deps.Sessions.Flush(ctx, live.Session()); err != nil {
		return TurnResult{}, fmt.Errorf("harness/smoketest: 刷盘失败：%w", err)
	}
	return collector.result(live.Session().ID())
}

// onlyRoot 取那棵树上唯一的顶层 agent。
//
// 源: packages/test-support/loader-smoke/src/agent-turn.ts:41-48（onlyRootAgent）
//
// 不止一个就报错而不是挑第一个：一次冒烟里冒出第二个顶层 agent，说明装配和用例
// 想的不是一棵树，随便挑一个只会让后面那些断言验的是另一个东西。
func onlyRoot(registry *agent.Registry) (agent.Agent, error) {
	roots := registry.Roots()
	if len(roots) != 1 {
		return nil, fmt.Errorf("harness/smoketest: 驱一轮要求恰好一个顶层 agent，找到 %d 个", len(roots))
	}
	return roots[0], nil
}

// stepKey 是一个步骤的坐标：同一个步骤的记账只算一次。
type stepKey struct {
	turn int
	step int
}

// turnCollector 听着一个会话，把这一轮的最终文本和各步骤记账攒起来。
//
// 新增: 上游那份是几个闭包变量，单线程事件循环保证了不会有人和它们交错。Go 这边
// 观察者跑在驱动那条协程上、而调用方在自己那条上等，所以这些账必须由一把锁护着。
type turnCollector struct {
	session   *session.Session
	messageID llm.MessageID
	onEvent   func(id sessionlog.SessionID, event sessionlog.Event)

	mutex sync.Mutex
	// received 在看见「那条消息进了收件箱」之前一直是假，此前的事件一概不算数。
	received bool
	output   string
	usage    map[stepKey]llm.TokenUsage
	// decodeErr 是第一条读不回来的负载。观察者交不出错误，所以攒到这里，
	// 由 [turnCollector.result] 交出去。
	decodeErr error
}

// observe 是挂在会话存储上的那个追加观察者。
//
// 源: packages/test-support/loader-smoke/src/agent-turn.ts:67-84
func (c *turnCollector) observe(live *session.Session, event sessionlog.Event) {
	if live != c.session {
		return
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if !c.received {
		// 认领这一轮的起点：那条消息真的进了收件箱。在它之前落下的事件属于别的
		// 活动——WhenIdle 等的是「这一层静下来」，不是「我这条消息落地了」。
		if event.Type != agent.EventInboxSpliced {
			return
		}
		var splice agent.SplicedData
		if err := json.Unmarshal(event.Data, &splice); err != nil {
			c.fail(fmt.Errorf("读收件箱改动失败：%w", err))
			return
		}
		if !containsMessage(splice.Inserted, c.messageID) {
			return
		}
		c.received = true
	}

	if c.onEvent != nil {
		c.onEvent(live.ID(), event)
	}

	switch event.Type {
	case sessionlog.EventAssistantChunk:
		var data sessionlog.AssistantChunkData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			c.fail(fmt.Errorf("读助手分块失败：%w", err))
			return
		}
		if chunk, ok := data.Chunk.(llm.UsageChunk); ok {
			c.usage[stepKey{turn: data.Turn, step: data.Step}] = chunk.Usage
		}
	case sessionlog.EventAssistantMessage:
		var data sessionlog.AssistantMessageData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			c.fail(fmt.Errorf("读助手消息失败：%w", err))
			return
		}
		// 一条没有可见文本的助手消息（比如只要了工具）不该把上一条的文本抹掉。
		if text, ok := visibleText(data.Message); ok {
			c.output = text
		}
		if data.Usage != nil {
			c.usage[stepKey{turn: data.Turn, step: data.Step}] = *data.Usage
		}
	}
}

// fail 记下第一条读不回来的负载。
func (c *turnCollector) fail(err error) {
	if c.decodeErr == nil {
		c.decodeErr = err
	}
}

// result 把攒下来的东西凑成一份结果。
func (c *turnCollector) result(id sessionlog.SessionID) (TurnResult, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.decodeErr != nil {
		return TurnResult{}, fmt.Errorf("harness/smoketest: %w", c.decodeErr)
	}
	result := TurnResult{SessionID: id, Output: c.output}
	if len(c.usage) == 0 {
		return result, nil
	}
	// 新增: 上游那个 addUsage 要为三个可选字段各判一次「两边在不在」，因为 JS 里
	// 缺席和零是两回事。Go 的 TokenUsage 五个字段都是 int，那套分支塌成直加。
	total := llm.TokenUsage{}
	for _, step := range c.usage {
		total.InputTokens += step.InputTokens
		total.OutputTokens += step.OutputTokens
		total.CacheReadTokens += step.CacheReadTokens
		total.CacheWriteTokens += step.CacheWriteTokens
		total.ReasoningTokens += step.ReasoningTokens
	}
	result.Usage = &total
	return result, nil
}

// containsMessage 判断这批插进去的消息里有没有那一条。
func containsMessage(inserted []llm.Message, id llm.MessageID) bool {
	for _, message := range inserted {
		if message.ID == id {
			return true
		}
	}
	return false
}

// visibleText 把一条助手消息里的文本块连起来；一块都没有时第二个返回值是假。
//
// 源: packages/test-support/loader-smoke/src/agent-turn.ts:36-39（assistantText）
func visibleText(message llm.Message) (string, bool) {
	var builder strings.Builder
	found := false
	for _, block := range message.Content {
		text, ok := block.(llm.TextBlock)
		if !ok {
			continue
		}
		found = true
		builder.WriteString(text.Text)
	}
	return builder.String(), found
}
