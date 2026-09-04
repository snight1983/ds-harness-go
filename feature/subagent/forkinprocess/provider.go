// Package forkinprocess 是进程内的 FORK 后端：把每一个孩子跑成一个**拿父会话
// 日志的一段前缀做种**的孩子 agent——于是孩子继承父那段对话，而不是从头开始。
//
// 对应 DSH 的 @deepseek-ai/dsh-subagent-fork-in-process
// （packages/subagent/subagent-fork-in-process）。
//
// 源: packages/subagent/subagent-fork-in-process/src/index.ts:1-8
//
// 那段种子止于最后一次 turn/end：正在进行的那个工具调用回合是不配对的，
// 拿它当孩子会话重放不合法。
//
// # 新增: 登记这件事没有对应物
//
// 同 github.com/snight1983/ds-harness-go/feature/subagent/spawninprocess：DSH 那个 apply 是往 cordis 上下文上
// 挂提供方，Go 里登记是组装根的一句话，所以这个包只交出 [New]。
package forkinprocess

import (
	"context"

	"github.com/snight1983/ds-harness-go/feature/subagent"
	"github.com/snight1983/ds-harness-go/feature/subagent/inprocessdriver"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// DefaultProviderName 是不指定时这个提供方在注册表里的名字。
//
// 源: packages/subagent/subagent-fork-in-process/src/index.ts:37
//
// 新增: DSH 是 schemastery 的 `z.string().default('fork')`，默认值在运行期解析
// 配置时补。Go 里补默认值的地方是 [New]，这个常量是它的取值。
const DefaultProviderName = "fork"

// completedTurnPrefix 交回 parent 那段回合完整的日志前缀：直到并且包含最后一条
// turn/end 的每一条事件。
//
// 源: packages/subagent/subagent-fork-in-process/src/index.ts:48-54
//
// 在飞的那个回合被排除在外；一个回合都还没完成时孩子从头开始（交回 nil）。
// 切出来的结果是一段从这份日志的起点起连续的合法种子，起点由第二个返回值给出。
//
// 新增: 第二个返回值是这段前缀的起点 seq。上游不需要它——那边的日志从 0 起、
// 一条不删。本仓库的日志会从最老的一头被弹掉一截（见 docs/session-log-limit.md），
// 于是这段前缀可能不从 0 起，而会话那道 seed 校验要拿它去核每一条的 seq。
func completedTurnPrefix(parent agent.Agent) ([]sessionlog.Event, int) {
	events := parent.Session().Events()
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != sessionlog.EventTurnEnd {
			continue
		}
		// 切到这一条为止、并且带上它。
		//
		// 新增: 这里曾经写 `events[:seq+1:seq+1]`，照搬了上游「seq 就是下标」那条
		// 契约。本仓库的日志被弹过头之后 seq 比下标大，那个式子会直接越界 panic——
		// 一个活得够久的父会话再也分叉不出孩子。下标就在手里（i），不必从 seq 倒推。
		//
		// 三下标切法把容量也掐到这里。DSH 的 Array.slice 本来就另开一个数组，
		// Go 的切片却和 [github.com/snight1983/ds-harness-go/harness/session.Session.Events] 那份
		// 快照共用底层数组——而那份快照会被重复交给别的调用方。不掐容量的话，
		// 拿到这段种子的人一次 append 就会写进快照第 n 格，别人手里那份跟着变。
		end := i + 1
		return events[:end:end], sessionlog.LogBaseSeq(events)
	}
	return nil, sessionlog.LogBaseSeq(events)
}

// Provider 是 fork 提供方。
//
// 源: packages/subagent/subagent-fork-in-process/src/index.ts:61-90
//
// 它支持 DepthLimit 和 OutputSchema（走共用的那套进程内结构化运行时），
// 外加 ToolFilter 与 Persona（带作用域的一次 restrict 和一段盖掉部署人设的
// 提示词小节）。
//
// 新增: 改名理由同 github.com/snight1983/ds-harness-go/feature/subagent/spawninprocess.Provider。
type Provider struct {
	// name 是注册表里的名字。
	name string
	// services 是这台部署的进程内驱动服务。
	services inprocessdriver.Services
}

// 编译期确认这个提供方兑现了它自称的那两个接口。
var (
	_ subagent.Provider            = (*Provider)(nil)
	_ subagent.ContinuablePreparer = (*Provider)(nil)
)

// New 造一个 fork 提供方；name 为空串时用 [DefaultProviderName]。
//
// 源: packages/subagent/subagent-fork-in-process/src/index.ts:66,92-94
//
// 新增: 服务在这里交进来而不是每次 Start 现找，理由见
// [inprocessdriver.Services]；齐不齐由 [inprocessdriver.StartInProcessRun] 一处判。
func New(name string, services inprocessdriver.Services) *Provider {
	if name == "" {
		name = DefaultProviderName
	}
	return &Provider{name: name, services: services}
}

// Name 交回这个提供方在注册表里的名字。
func (p *Provider) Name() string { return p.name }

// Capabilities 交回这个提供方支持的开工期能力。
//
// 源: packages/subagent/subagent-fork-in-process/src/index.ts:62
func (p *Provider) Capabilities() subagent.Capabilities {
	return subagent.Capabilities{
		OutputSchema: true,
		DepthLimit:   true,
		ToolFilter:   true,
		Persona:      true,
	}
}

// InheritsParentContext 恒为真：fork 出来的孩子**确实**拿父那段回合完整的前缀做种。
//
// 源: packages/subagent/subagent-fork-in-process/src/index.ts:64
func (p *Provider) InheritsParentContext() bool { return true }

// Start 立起一个拿父前缀做种的孩子。
//
// 源: packages/subagent/subagent-fork-in-process/src/index.ts:68-75
//
// 只有真有一个已完成的回合可继承时才给种子：一段空种子等价于一个全新的孩子，
// 所以那种情况下不给，让这个会话保持未做种的样子。
func (p *Provider) Start(ctx context.Context, request subagent.ResolvedStartRequest) (subagent.Run, error) {
	// completedTurnPrefix 在没有已完成回合时交回 nil，正好就是驱动那边
	// 「全新的孩子」的表示法，所以这里不需要 DSH 那个条件展开。
	seed, baseSeq := completedTurnPrefix(request.Parent)
	return inprocessdriver.StartInProcessRun(ctx, p.services, request,
		inprocessdriver.RunOptions{Seed: seed, SeedBaseSeq: baseSeq})
}

// PrepareContinuable 交回这个可续孩子的种子贡献。
//
// 源: packages/subagent/subagent-fork-in-process/src/index.ts:83-89
//
// 这段 fork 前缀**只在创建的那一刻**取一次：它成为孩子自己那份持久抄本的一部分，
// 于是日后一次冷恢复重放的是那段前缀，而不是拿父更新的历史重新 fork 一遍。
//
// DSH 在这里留了一条 TODO(fork-continuable-prefix-reuse)：出厂的那些组装都没有
// 调过它，它们把 fork 绑在一次性背景模式上——因为一个可续孩子的 report 工具和
// 提示词小节排在继承来的历史之前，恰好废掉了 fork 存在的那点前缀复用。
// 要重开这条路，得先让孩子的系统提示词和工具 schema 逐字节一致。这条约束原样搬过来。
func (p *Provider) PrepareContinuable(
	ctx context.Context,
	request subagent.ContinuableCreateRequest,
) (subagent.ContinuableCreateSpec, error) {
	seed, baseSeq := completedTurnPrefix(request.Parent)
	return subagent.ContinuableCreateSpec{Seed: seed, SeedBaseSeq: baseSeq}, nil
}
