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

	"github.com/snight1983/ds-harness-go/attachment"
	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/core/systemprompt"
	"github.com/snight1983/ds-harness-go/interaction/userapproval"
	sessionlog "github.com/snight1983/ds-harness-go/session"
)

// DefaultSessionListPageSize 是一页 `session/list` 最多交出去多少条摘要。
//
// 源: packages/acp/acp/src/index.ts:58
const DefaultSessionListPageSize = 100

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/acp/acp/src/invariant.ts:10
const PackageName = "@deepseek-ai/dsh-acp"

// PluginName 是这个包露面时用的名字。
//
// 源: packages/acp/acp/src/index.ts:60（name）
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
// [github.com/snight1983/ds-harness-go/sdk/sdkserver.LLMService]），
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
// [github.com/snight1983/ds-harness-go/subagent/subagent.Runtime] 满足它。
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
// 瀑布是 [github.com/snight1983/ds-harness-go/interaction/userapproval.Service.RegisterAnswerer] 的一条显式
// 规则链，这里只取那一个方法。它可以为 nil：这条线上没挂审批服务，那么这座桥就不
// 参与拍板。
//
// 签名里的答复者类型必须是具名的 [github.com/snight1983/ds-harness-go/interaction/userapproval.Answerer]，
// 不能写成结构相同的匿名函数类型——否则 `*Service` 不满足这个接口。
type ApprovalRegistrar interface {
	// RegisterAnswerer 把一条答复者挂进审批瀑布，交回摘掉它的函数。
	RegisterAnswerer(ctx context.Context, owner *scope.Scope, answerer userapproval.Answerer) (func(context.Context) error, error)
}

// SessionCatalog 是这座桥用得到的那一块持久化服务：只问「存档里都有哪些会话」。
//
// 源: packages/acp/acp/src/index.ts:100（ctx.sessionPersistence）、247、302
//
// 新增: DSH 把整个 sessionPersistence 服务注进来，但那三处用到的其实只有 `list()`
// 一个方法——续跑那一步的读回落在 `ctx.agents.resume()` 里面，不经过这座桥。所以
// 这里照本包的成例收窄成一个方法，
// [github.com/snight1983/ds-harness-go/session/persistence.Coordinator] 自然满足它。
//
// 它可以为 nil：这条线上没挂持久化，那么 `session/list` 和 `session/resume` 两项
// 能力就都不声明，对应的方法照旧交回 -32601。
type SessionCatalog interface {
	// List 列出存档里所有已落地会话的头。
	List(ctx context.Context) ([]sessionlog.SessionHeader, error)
}

// WorkspaceResolver 是这座桥用得到的那一块工作区登记册：把线上那条 cwd 和一个
// 工作区标识来回换一次。
//
// 新增: DSH 没有这样东西——它把 `cwd` 原样写进会话头，那条路径在服务端一路往下传。
// 本仓库的会话头记的是一个不透明的工作区标识（见
// [sessionlog.SessionHeader.WorkspaceID]），而线上那条 cwd 是**客户端那台机器**上的
// 写法：它和服务端认识的任何东西都不在一个命名空间里。两者之间怎么对应只有装配方
// 答得出来，所以摆成一个显式的协作者，而这条换算就是 cwd 这个概念在服务端的终点。
//
// 它可以为 nil，那时这条线上每一条 cwd 都换到空工作区：新开的会话不属于任何工作区，
// `session/resume` 和 `session/list` 那两处比较仍然成立（两边都是空串），
// `session/list` 报出去的 cwd 是空串。
type WorkspaceResolver interface {
	// WorkspaceOf 把客户端报上来的一条 cwd 换成一个工作区标识。
	//
	// 第二个返回值为假表示这条 cwd 没有对应的工作区登记。
	WorkspaceOf(ctx context.Context, cwd string) (sessionlog.WorkspaceID, bool, error)

	// WorkspaceDisplay 给出一个工作区标识拿给客户端看的那条路径。
	//
	// 第二个返回值为假表示这个标识在登记册里已经没有了。
	WorkspaceDisplay(ctx context.Context, id sessionlog.WorkspaceID) (string, bool, error)
}

// Config 是这座桥的装配面。
//
// 源: packages/acp/acp/src/index.ts:74-84（AcpConfig / Config）
//
// 新增: DSH 那个配置只有 provider / model / stream 三项，别的一切都从 cordis 容器里
// 按名字取。Go 里没有容器，所以每一样协作者在这里都是一个显式字段——理由和
// [github.com/snight1983/ds-harness-go/sdk/sdkserver.Config] 上那条逐字相同。
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

	// Models 是那一小块 LLM 服务，可以为 nil，见 [ModelCatalog]。
	//
	// 为 nil 时这条线一个会话配置项都不摆：模型和推理档位的选项都得先翻目录才
	// 算得出来。那时 `session/set_config_option` 照旧交回 -32601。
	Models ModelCatalog

	// Prompts 是提示词段落注册表。Models 非 nil 时必填，为 nil 时忽略。
	//
	// 源: packages/acp/acp/src/model-control.ts:54-60（modelControl.install）
	//
	// 新增: DSH 的 `install(agentCtx)` 从那个 cordis 上下文上一次拿到 agent 和提示词
	// 两条接缝。Go 的
	// [github.com/snight1983/ds-harness-go/core/agent.InstallModelSelection] 两张注册表
	// 都要显式收，所以这里多出一个字段——它不是一样新东西，是同一件事在 Go 里的写法。
	Prompts *systemprompt.Registry

	// MCPServers 是 MCP 宿主，可以为 nil，见 [MCPHost]。
	//
	// 为 nil 时这条线不声明 `mcpCapabilities.http`，收到 `mcpServers` 一律拒。
	MCPServers MCPHost

	// Persistence 是那一小块持久化服务，可以为 nil，见 [SessionCatalog]。
	Persistence SessionCatalog

	// Workspaces 是那一小块工作区登记册，可以为 nil，见 [WorkspaceResolver]。
	Workspaces WorkspaceResolver

	// Meter 是那一小块 token 计量，可以为 nil，见 [TokenMeasurer]。
	//
	// 为 nil 时这条线不发上下文占用更新，对应 DSH 那个 `meter === undefined`。
	Meter TokenMeasurer

	// Approvals 是审批服务，可以为 nil，见 [ApprovalRegistrar]。
	Approvals ApprovalRegistrar

	// Subagents 是那一个收摊钩子，可以为 nil，见 [ContinuableDrain]。
	Subagents ContinuableDrain

	// Provider 与 Model 是这条线上每一个 agent 共用的那份路由。
	//
	// 源: packages/acp/acp/src/index.ts:72-75
	Provider string
	Model    string

	// SessionListPageSize 是一页 `session/list` 的上限，非正数表示用
	// [DefaultSessionListPageSize]。
	//
	// 源: packages/acp/acp/src/index.ts:80-81, 463-471
	//
	// 新增: DSH 用 schemastery 的 `Schema.natural().min(1).default(100)` 把「没填」和
	// 「填了个非法值」分开处理——前者取默认，后者在装配时就报错。Go 的零值填不出这个
	// 区别，所以这里把非正数一律当作「没填」。一个部署要的是「别摆一页 0 条」，而不是
	// 一句装配期的抱怨。
	SessionListPageSize int

	// Logger 是记诊断的地方，可以为 nil（那时用 [log/slog.Default]）。
	//
	// 新增: DSH 靠 cordis 上下文自带的 logger。这里往它上面记的都是「报不出去的
	// 失败」：一条发不出去的通知、一次转不动的助手输出、一次拆不干净的可续后代。
	Logger *slog.Logger
}

// sessionListPageSize 交出这条线真正用的那个上限。
func (c Config) sessionListPageSize() int {
	if c.SessionListPageSize <= 0 {
		return DefaultSessionListPageSize
	}
	return c.SessionListPageSize
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
// [github.com/snight1983/ds-harness-go/sdk/sdkserver.Config] 不同），它由 [Bridge.Install] 收。这个环是协议
// 本身的形状，DSH 用 `makeAgent(connection)` 那个闭包绕的也是同一个环。
func New(config Config) (*Bridge, error) {
	switch {
	case config.Agents == nil:
		return nil, fmt.Errorf("acp: 建一座 ACP 桥需要一张 agent 注册表")
	case config.Sessions == nil:
		return nil, fmt.Errorf("acp: 建一座 ACP 桥需要一台会话存储")
	case config.Models != nil && config.Prompts == nil:
		// 摆得出模型选项，就一定要装得上那份选择——不然对面改完配置，下一次提示词
		// 装配照旧走老路由，而线上却报改成功了。
		return nil, fmt.Errorf("acp: 挂了 LLM 目录的 ACP 桥还需要一张提示词注册表")
	}
	return &Bridge{
		config:     config,
		sessions:   map[sessionlog.SessionID]*sessionRecord{},
		activating: map[sessionlog.SessionID]struct{}{},
	}, nil
}
