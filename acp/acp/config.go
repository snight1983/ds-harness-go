// 本文件的作用：这座桥的装配面——它要哪几样协作者、哪几样可以缺、缺什么当场拒，
// 以及它在线上报出来的那份身份。
//
// 源: packages/acp/acp/src/index.ts:43-83, packages/acp/acp/src/index.ts:52-58

package acp

import (
	"context"
	"fmt"
	"log/slog"

	wire "github.com/coder/acp-go-sdk"

	"ds-harness-go/attachment"
	"ds-harness-go/core/agent"
	"ds-harness-go/core/scope"
	coresession "ds-harness-go/core/session"
	"ds-harness-go/interaction/userapproval"
	sessionlog "ds-harness-go/session"
)

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/acp/acp/src/invariant.ts:10
const PackageName = "@deepseek-ai/dsh-acp"

// PluginName 是这个包露面时用的名字。
//
// 源: packages/acp/acp/src/index.ts:43
const PluginName = "acp"

// AgentName 与 AgentVersion 是握手时报给对面的那份身份。
//
// 源: packages/acp/acp/src/index.ts:296
const (
	AgentName    = "deepseek-harness-acp"
	AgentVersion = "0.0.1"
)

// 这两个是这座桥在审批那条线上摆出来的选项标识。
//
// 源: packages/acp/acp/src/index.ts:277-280
//
// 只给一次性的两档：这条线是给机器走的策略通道，一个认不出的回答绝不能被折成
// 一份耐久的授权。
const (
	optionAllowOnce  wire.PermissionOptionId = "allow-once"
	optionRejectOnce wire.PermissionOptionId = "reject-once"
)

// Peer 是这座桥往对面发东西的那一面：一条单向的会话更新，和一次要人拍板的问话。
//
// 源: packages/acp/acp/src/index.ts:150, 274
//
// 新增: DSH 攥着的是整个 AgentSideConnection。这里写成一个两方法的窄口子（成例见
// [ds-harness-go/sdk/sdkserver.ProviderLister]），
// [github.com/coder/acp-go-sdk.AgentSideConnection] 自然满足它——于是这座桥测得动，
// 而且它能不能干的事在类型上就写清楚了：它不读文件、不开终端、不发扩展方法。
type Peer interface {
	// SessionUpdate 往对面推一条会话更新。
	SessionUpdate(ctx context.Context, params wire.SessionNotification) error
	// RequestPermission 问对面要一次工具调用的许可。
	RequestPermission(ctx context.Context, params wire.RequestPermissionRequest) (wire.RequestPermissionResponse, error)
}

// ContinuableDrain 是这座桥收摊时用得到的那一个子 agent 方法。
//
// 源: packages/acp/acp/src/index.ts:52-58
//
// 这是 DSH 自己就写成结构式的那个口子，原样搬过来：为一个收摊钩子去依赖整条子 agent
// 接缝不值当。它可以为 nil，那表示这条线上根本没有物化过任何可续的东西。
// [ds-harness-go/subagent/subagent.Runtime] 满足它。
type ContinuableDrain interface {
	// DrainContinuableDescendants 关掉几个确切的活父底下的可续准入，然后孩子优先地
	// 放掉那片森林。
	DrainContinuableDescendants(ctx context.Context, parents []agent.Agent) error
}

// ApprovalRegistrar 是这座桥用得到的那一小块审批服务：只要「把我这条答复者挂上去」。
//
// 源: packages/acp/acp/src/index.ts:271
//
// 新增: DSH 是 `ctx.on('approval/request', ...)`——挂在 cordis 那道瀑布上。Go 里那道
// 瀑布是 [ds-harness-go/interaction/userapproval.Service.RegisterAnswerer] 的一条显式
// 规则链，这里只取那一个方法。它可以为 nil：这条线上没挂审批服务，那么这座桥就不
// 参与拍板。
//
// 签名里的答复者类型必须是具名的 [ds-harness-go/interaction/userapproval.Answerer]，
// 不能写成结构相同的匿名函数类型——否则 `*Service` 不满足这个接口。
type ApprovalRegistrar interface {
	// RegisterAnswerer 把一条答复者挂进审批瀑布，交回摘掉它的函数。
	RegisterAnswerer(ctx context.Context, owner *scope.Scope, answerer userapproval.Answerer) (func(context.Context) error, error)
}

// Config 是这座桥的装配面。
//
// 源: packages/acp/acp/src/index.ts:71-83（AcpConfig / Config）
//
// 新增: DSH 那个配置只有 provider / model / stream 三项，别的一切都从 cordis 容器里
// 按名字取。Go 里没有容器，所以每一样协作者在这里都是一个显式字段——理由和
// [ds-harness-go/sdk/sdkserver.Config] 上那条逐字相同。
//
// DSH 那个只在测试里用的 `stream` 一项不移：换一条流是装配方的自由，这座桥连接都不
// 拥有（见包文档）。
type Config struct {
	// Agents 是 agent 注册表，必填：这座桥靠它建会话、也靠它验一条会话记录指着的
	// agent 还是不是活着的那一个。
	//
	// 源: packages/acp/acp/src/index.ts:45（inject = ['agents']）
	Agents *agent.Registry

	// Sessions 是会话存储，必填：已提交的助手输出和回合的结束理由都从它那条追加边来。
	//
	// 源: packages/acp/acp/src/index.ts:222
	Sessions *coresession.Store

	// Attachments 是附件存储，可以为 nil。为 nil 时这条线不声明支持内联图，收到图
	// 也一律拒。
	Attachments attachment.Store

	// Models 是那一小块 LLM 服务，可以为 nil，见 [ModelResolver]。
	Models ModelResolver

	// Approvals 是审批服务，可以为 nil，见 [ApprovalRegistrar]。
	Approvals ApprovalRegistrar

	// Subagents 是那一个收摊钩子，可以为 nil，见 [ContinuableDrain]。
	Subagents ContinuableDrain

	// Provider 与 Model 是这条线上每一个 agent 共用的那份路由。
	//
	// 源: packages/acp/acp/src/index.ts:72-75
	Provider string
	Model    string

	// Logger 是记诊断的地方，可以为 nil（那时用 [log/slog.Default]）。
	//
	// 新增: DSH 靠 cordis 上下文自带的 logger。这里往它上面记的都是「报不出去的
	// 失败」：一条发不出去的通知、一次转不动的助手输出、一次拆不干净的可续后代。
	Logger *slog.Logger
}

// warn 记一行诊断。
func (c Config) warn(message string) {
	if c.Logger == nil {
		slog.Default().Warn(message)
		return
	}
	c.Logger.Warn(message)
}

// New 造一座桥，把装配规矩查一遍。
//
// 源: packages/acp/acp/src/index.ts:121-129
//
// 新增: 造和装是两步，而且这里比别处多一层绕——造出来的这个值**就是**要交给
// [github.com/coder/acp-go-sdk.NewAgentSideConnection] 的那个 agent 实现，而连接反过来
// 又是这座桥发通知要用的 [Peer]。所以 Peer 不在 Config 上（那点和
// [ds-harness-go/sdk/sdkserver.Config] 不同），它由 [Bridge.Install] 收。这个环是协议
// 本身的形状，DSH 用 `makeAgent(connection)` 那个闭包绕的也是同一个环。
func New(config Config) (*Bridge, error) {
	switch {
	case config.Agents == nil:
		return nil, fmt.Errorf("acp: 建一座 ACP 桥需要一张 agent 注册表")
	case config.Sessions == nil:
		return nil, fmt.Errorf("acp: 建一座 ACP 桥需要一台会话存储")
	}
	return &Bridge{
		config:   config,
		sessions: map[sessionlog.SessionID]*sessionRecord{},
	}, nil
}
