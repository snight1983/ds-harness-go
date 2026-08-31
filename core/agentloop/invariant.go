// 本文件的作用：这个包自己拥有的那条运行期不变量——一份由循环装配出来的模型
// 请求，必须和它那个会话在**派发这一刻**的耐久日志推导得出的东西完全一致。
//
// 源: packages/core/agent-loop/src/invariant.ts:1-63

package agentloop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"iter"

	"ds-harness-go/core/scope"
	"ds-harness-go/core/session"
	"ds-harness-go/invariants"
	"ds-harness-go/llm"
	sessionlog "ds-harness-go/session"
)

// PackageName 是这个包在不变量注册表里占的名字。
//
// 源: packages/core/agent-loop/src/invariant.ts:11
//
// 沿用 DSH 侧的包名字面量而不是换成 Go 的导入路径，理由同 llm、credentials 两个包：
// 注册表按名字预留名额，而这条约定的拥有者在两边是同一个模块。换个名字，
// 两边的诊断日志就对不上了。
const PackageName = "@deepseek-ai/dsh-agent-loop"

// RegisterInvariants 装上「循环装出来的请求和日志对得上」这条检查，返回注销函数。
//
// 源: packages/core/agent-loop/src/invariant.ts:16-63
//
// # 这条检查在查什么
//
// 循环发出去的每一次请求，都必须是**当下这段日志**推导出来的那一份。它守的是本包
// 最核心的那条约定：会话日志是唯一的真相，请求不过是它的一次投影。这条一旦破了，
// 存下来的日志和真正发出去的请求就是两回事——重放读出来的历史，和模型当时看到的
// 历史不一样，而且事后从任何一侧都查不出来。
//
// 具体查六件事：请求带着会话身份；那个会话此刻活着；它的日志里有过 step/start；
// 它折得出一份请求头；请求带的消息和 [ds-harness-go/core/session.Session.DeriveMessages]
// 逐字节一致；请求那几个头字段和折出来的那份头一致。
//
// # 只查循环自己装的请求
//
// 非循环装配的请求（手搓的一次性调用、标题生成、压缩）原样放行。判据是
// [llm.GenerateOptions.AgentLoop] 这一位，见那里关于 DSH 的 WeakSet 的注释。
//
// # 新增: 它必须是这个运行时上**第一条**登记的流规则
//
// DSH 那边是 `ctx.on('llm/stream', ..., { prepend: true })`，注释写明用意是
// 「prepend 让一个短路的重放监听器没法把这条检查静音掉」——插在最外面，任何后来
// 登记的、可能不往里走的中间件都盖不住它。
//
// Go 这边 [llm.Runtime.OnStream] 只有追加（底下的 [scope.AnonymousEntries] 没有
// Prepend），而 [llm.Runtime] 上那个专门的最外层槽位已经归 llm 包自己那条不变量
// 所有。所以这件事在 Go 里成了一条**装配纪律**：本函数必须在任何别的
// [llm.Runtime.OnStream] 之前调用。本仓库的瀑布次序是先登记的在外面，满足这一条
// 就等价于 DSH 的 prepend。
//
// 登记晚了不会让这条检查失效，只会让它可以被一个短路的中间件绕过——也就是退化成
// 「查得到的时候查」。本包没法自己保证这件事（运行时不告诉登记方自己排第几），
// 所以它写在这里，由装配方守。
//
// # 新增: DSH 那两条 Object.isFrozen 检查在这里不存在
//
// 源: packages/core/agent-loop/src/invariant.ts:21、25
//
// DSH 查「请求对象和它的 messages 数组都被冻住了」，防的是下游中间件就地改掉一份
// 已经派发出去的请求。Go 的 [llm.GenerateOptions] 是值，跨函数边界就是一次复制，
// 下游改的从来都是自己那一份——这件事由语言保证，没有可查的运行期事实。
// 唯一还共享的是 Messages 那个切片的底层数组，而那条约定（当只读的用）和本仓库
// 每一处 json.RawMessage 一样，写在文档里，不是不变量管得了的。
func RegisterInvariants(
	ctx context.Context,
	registry *invariants.Registry,
	owner *scope.Scope,
	runtime *llm.Runtime,
	sessions *session.Store,
) (func(), error) {
	if registry == nil {
		return nil, fmt.Errorf("core/agentloop: 注册不变量需要一个不变量注册表")
	}
	if owner == nil {
		return nil, fmt.Errorf("core/agentloop: 注册不变量需要一个持有这次登记的作用域")
	}
	if runtime == nil {
		return nil, fmt.Errorf("core/agentloop: 注册不变量需要一个 llm 运行时")
	}
	if sessions == nil {
		return nil, fmt.Errorf("core/agentloop: 注册不变量需要一个会话存储")
	}

	install := func(installCtx context.Context, invariantScope *invariants.Scope, fail invariants.Fail) error {
		rule := llm.StreamRule(func(
			ruleCtx context.Context,
			options llm.GenerateOptions,
			next func(context.Context) (iter.Seq2[llm.StreamChunk, error], error),
		) (iter.Seq2[llm.StreamChunk, error], error) {
			checkLoopRequest(options, sessions, fail)
			return next(ruleCtx)
		})

		dispose, err := runtime.OnStream(installCtx, owner, rule)
		if err != nil {
			return fmt.Errorf("core/agentloop: 往 llm/stream 上装不变量失败：%w", err)
		}
		// 摘掉这一层登记进 scope：注销之后，一条不该再查的检查必须停下来，
		// 否则它会继续在别人的流上抛。
		invariantScope.Defer(func() { _ = dispose(context.WithoutCancel(installCtx)) })
		return nil
	}

	return registry.Register(ctx, PackageName, install)
}

// checkLoopRequest 是这条不变量的正文：非循环请求直接放过，循环请求逐条查。
//
// 源: packages/core/agent-loop/src/invariant.ts:19-58
//
// 它不返回任何东西——每一条违反都走 [invariants.Fail]，而那是 panic。
// 一份和日志对不上的请求没有「照发不误」这个选项。
func checkLoopRequest(options llm.GenerateOptions, sessions *session.Store, fail invariants.Fail) {
	if !options.AgentLoop {
		return
	}
	if options.SessionID == "" {
		fail("a loop-built request must carry a session id")
		// 走不到：[invariants.Fail] 是 panic，上面那一句不返回。写出来是因为
		// 下面每一行都要拿这个身份去查会话——真让它往下走一步，这条检查就会
		// 顶着一个空身份查出一堆没有意义的诊断，把真正的那一条盖掉。
		// DSH 靠 TS 的 never 让编译器把这里判成死代码，Go 没有那个类型。
		return
	}
	live, ok := sessions.Get(sessionlog.SessionID(options.SessionID))
	if !ok {
		fail(fmt.Sprintf("a loop-built request must carry a live session id, got %q", string(options.SessionID)))
		// 走不到，理由同上；这一句还多挡一次对 nil 会话的解引用。
		return
	}

	// 没有 step/start 的话，这次请求根本不属于任何一个步骤——也就没有任何一段
	// 日志能说明它为什么被发出去。
	if !hasStepStart(live.Events()) {
		fail("a loop-built request with no step/start in its session log")
	}

	header, hasHeader, err := live.RequestHeader()
	if err != nil {
		fail(fmt.Sprintf("llm request for session %q has an unreadable request header: %v", string(live.ID()), err))
	}
	if !hasHeader {
		fail("a loop-built request with no request/header event in its session log")
	}

	expected, err := live.DeriveMessages()
	if err != nil {
		fail(fmt.Sprintf("llm request for session %q has an underivable message history: %v", string(live.ID()), err))
	}
	sameMessages, err := sameJSON(normalizeMessages(options.Messages), normalizeMessages(expected))
	if err != nil {
		fail(fmt.Sprintf("llm request for session %q has unserializable messages: %v", string(live.ID()), err))
	}
	if !sameMessages {
		fail(fmt.Sprintf(
			"llm request for session %q diverges from the dispatch-time durable derivation (log-reconstruction desync)",
			string(live.ID())))
	}

	if !headerMatches(options, header, fail) {
		fail(fmt.Sprintf("llm request for session %q diverges from the folded request header", string(live.ID())))
	}
}

// hasStepStart 判这段日志里有没有出现过一条 step/start。
//
// 源: packages/core/agent-loop/src/invariant.ts:31
func hasStepStart(events []sessionlog.Event) bool {
	for _, event := range events {
		if event.Type == sessionlog.EventStepStart {
			return true
		}
	}
	return false
}

// headerMatches 比请求那几个头字段和折出来的那份头对不对得上。
//
// 源: packages/core/agent-loop/src/invariant.ts:45-52
//
// 新增: DSH 比的是 model、system、temperature、maxTokens、stop、tools 六项，
// **没有** provider 和 reasoningEffort。这里照它的清单来，不多比：本包的
// [ReactLoopAgent.buildRequest] 是从同一份头逐字段装出请求的，多比那两项在正常
// 路径上永远成立，可它同时也是一条 DSH 没有的、可能在别处误报的约束。
func headerMatches(options llm.GenerateOptions, header sessionlog.EpochHeader, fail invariants.Fail) bool {
	if options.Model != header.Config.Model || options.System != header.System {
		return false
	}
	if options.MaxTokens != header.Config.MaxTokens {
		return false
	}
	if !sameTemperature(options.Temperature, header.Config.Temperature) {
		return false
	}
	if !sameStop(options.Stop, header.Config.Stop) {
		return false
	}
	same, err := sameTools(options.Tools, header.Tools)
	if err != nil {
		fail(fmt.Sprintf("a loop-built request carries unserializable tool schemas: %v", err))
	}
	return same
}

// sameTemperature 比两个可选温度：都没给算相等，一边给了算不等，都给了比值。
//
// 新增: DSH 那一句是 `options.temperature === header.config.temperature`，两个
// undefined 相等、两个 0.7 也相等。Go 里这是两个 *float64，直接比会变成比地址，
// 两个各自指向 0.7 的指针会判成不等——每一次请求都误报一条违例。
func sameTemperature(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// sameStop 比两份停止序列，**把 nil 和长度为零的切片看作相等**。
//
// 新增: DSH 那一句是 `JSON.stringify(options.stop) === JSON.stringify(header.config.stop)`，
// 它分得开 undefined 和 []。这里刻意分不开，因为 Go 侧多了一道 DSH 没有的往返：
// 那份头是从会话事件的 JSON 字节里折回来的，而 [llm.CallConfig] 的 `stop,omitempty`
// 把「明确给了一个空清单」和「没给」排成同一段字节（这个缺口由 llm 包的
// TestTheWireCannotTellAnEmptyStopListFromAnAbsentOne 钉着）。
//
// 所以在这里严格地比，等于对每一个解析出空停止清单的适配器报一条**不是违例的
// 违例**：请求手里那份是 []，从日志折回来的那份必然是 nil。等 llm 那个缺口补上，
// 这里就该跟着收紧成逐字节相等。
func sameStop(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for index, stop := range a {
		if stop != b[index] {
			return false
		}
	}
	return true
}

// sameTools 比两份工具 schema，nil 和空表看作相等。
//
// 源: packages/core/agent-loop/src/invariant.ts:51
//
// DSH 那一句自己就写着 `options.tools ?? []` 和 `header.tools ?? []`，两边都把
// 「没给」摊平成空表——[sessionlog.EpochHeader.Tools] 的字段注释说的是同一件事。
//
// Parameters 是 [encoding/json.RawMessage]，逐字节比而不是解出来比：键的顺序是
// 这份 schema 的一部分（见 [llm.ToolSchema] 的字段注释）。
func sameTools(a, b []llm.ToolSchema) (bool, error) {
	if len(a) != len(b) {
		return false, nil
	}
	for index, tool := range a {
		other := b[index]
		if tool.Name != other.Name || tool.Description != other.Description {
			return false, nil
		}
		if !bytes.Equal(tool.Parameters, other.Parameters) {
			return false, nil
		}
	}
	return true, nil
}

// normalizeMessages 把 nil 摊平成空表，好让下面那次逐字节比较不会把「没有消息」
// 排成 `null`、而把「空的消息表」排成 `[]`，然后判成不等。
//
// 新增: DSH 那边 `options.messages` 和 `deriveMessages()` 都是数组，
// JSON.stringify 出来一定是 `[...]`，压根没有第三种形态。
func normalizeMessages(messages []llm.Message) []llm.Message {
	if messages == nil {
		return []llm.Message{}
	}
	return messages
}

// sameJSON 判两份值排成 JSON 之后逐字节相同。
//
// 源: packages/core/agent-loop/src/invariant.ts:41
//
// 用「排成字节再比」而不是逐字段比，理由和 DSH 用 JSON.stringify 一样：
// [llm.Message] 的内容是一棵有内容块联合的树，逐字段比要在这里重写一遍它的形状，
// 而那份重写一旦漏掉某个新块类型，这条检查就会悄悄放过一次真正的偏离。
// 消息在介质上的形状本来就有自己的一整套用例钉着，直接借它。
func sameJSON(left, right any) (bool, error) {
	leftBytes, err := json.Marshal(left)
	if err != nil {
		return false, err
	}
	rightBytes, err := json.Marshal(right)
	if err != nil {
		return false, err
	}
	return bytes.Equal(leftBytes, rightBytes), nil
}
