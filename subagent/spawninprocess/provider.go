// Package spawninprocess 是进程内的 SPAWN 后端：把每一个孩子跑成一个**全新**的
// 孩子 agent——自己的会话、自己的系统提示词、父上下文一个字都不继承。
//
// 对应 DSH 的 @deepseek-ai/dsh-subagent-spawn-in-process
// （packages/subagent/subagent-spawn-in-process）。
//
// 源: packages/subagent/subagent-spawn-in-process/src/index.ts:1-7
//
// 这是最便宜的那条传输：它复用 agent 工厂那套静止拆解，自己一行装配都不写。
//
// # 新增: 登记这件事没有对应物
//
// DSH 那个 apply(ctx, config) 干的是 `ctx.subagents.registerProvider(...)`，
// 也就是把这个提供方挂进 cordis 上下文。Go 没有那个运行期容器，装配是
// 组装根里的一句 `subagents.RegisterProvider(spawninprocess.New(...))`，
// 所以这个包只交出 [New]，登记归调用方。
package spawninprocess

import (
	"context"

	"github.com/snight1983/ds-harness-go/subagent/inprocessdriver"
	"github.com/snight1983/ds-harness-go/subagent/subagent"
)

// DefaultProviderName 是不指定时这个提供方在注册表里的名字。
//
// 源: packages/subagent/subagent-spawn-in-process/src/index.ts:31
//
// 新增: DSH 是 schemastery 的 `z.string().default('spawn')`，默认值在运行期
// 解析配置时补。Go 里补默认值的地方是 [New]，这个常量是它的取值。
const DefaultProviderName = "spawn"

// Provider 是 spawn 提供方。
//
// 源: packages/subagent/subagent-spawn-in-process/src/index.ts:41-60
//
// 它支持每一样开工期能力：DepthLimit（孩子是它建的，所以拦得住递归）、
// OutputSchema（那套带作用域的结构化运行时）、以及 ToolFilter 与 Persona
// （孩子创建窗口里带作用域的一次 restrict 和一段盖掉部署人设的提示词小节）。
//
// 新增: DSH 那个类叫 SpawnInProcessProvider，在 Go 里连上包名会变成
// spawninprocess.SpawnInProcessProvider——结巴。按 Go 的习惯叫 Provider。
type Provider struct {
	// name 是注册表里的名字。DSH 那个字段是 `readonly name`，同样只读。
	name string
	// services 是这台部署的进程内驱动服务，逐字交给
	// [inprocessdriver.StartInProcessRun]。
	services inprocessdriver.Services
}

// 编译期确认这个提供方兑现了它自称的那两个接口。
var (
	_ subagent.Provider            = (*Provider)(nil)
	_ subagent.ContinuablePreparer = (*Provider)(nil)
)

// New 造一个 spawn 提供方；name 为空串时用 [DefaultProviderName]。
//
// 源: packages/subagent/subagent-spawn-in-process/src/index.ts:46,62-64
//
// 新增: DSH 的构造入参只有名字，那几样服务由 startInProcessRun 自己从
// `request.parent.ctx` 上取。Go 没有那个容器，所以服务在这里就交进来，
// 而不是每次 Start 现找（理由见 [inprocessdriver.Services]）。
//
// 服务是否齐备不在这里验：那是 [inprocessdriver.StartInProcessRun] 的判断，
// 让它在真正要用的时候一处报错，好过这里和那里各验一遍还可能验得不一样。
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
// 源: packages/subagent/subagent-spawn-in-process/src/index.ts:42
func (p *Provider) Capabilities() subagent.Capabilities {
	return subagent.Capabilities{
		OutputSchema: true,
		DepthLimit:   true,
		ToolFilter:   true,
		Persona:      true,
	}
}

// InheritsParentContext 恒为假：spawn 出来的孩子从头开始，永远看不到父那段对话。
//
// 源: packages/subagent/subagent-spawn-in-process/src/index.ts:44
func (p *Provider) InheritsParentContext() bool { return false }

// Start 立起一个全新的孩子：不带种子。
//
// 源: packages/subagent/subagent-spawn-in-process/src/index.ts:48-53
//
// 共用的那台驱动去发身份、盖上工作目录与血统与深度、驱动这一次一次性运行
// （请求带了 OutputSchema 时连那次结构化捕获一起），再把结果映射出来。
func (p *Provider) Start(ctx context.Context, request subagent.ResolvedStartRequest) (subagent.Run, error) {
	return inprocessdriver.StartInProcessRun(ctx, p.services, request, inprocessdriver.RunOptions{})
}

// PrepareContinuable 交回一份空的创建贡献。
//
// 源: packages/subagent/subagent-spawn-in-process/src/index.ts:55-59
//
// spawn 出来的孩子从头开始，所以它一个种子都不贡献；此后这个孩子身上的每一个
// 操作都归续接管理器。
func (p *Provider) PrepareContinuable(
	ctx context.Context,
	request subagent.ContinuableCreateRequest,
) (subagent.ContinuableCreateSpec, error) {
	return subagent.ContinuableCreateSpec{}, nil
}
