// 本文件的作用：这台服务器的装配面——它要哪几样协作者、哪几个部署口径的开关，
// 以及缺什么当场拒。
//
// 源: packages/sdk/server/src/index.ts:20-38, packages/sdk/server/src/server.ts:38-41

package sdkserver

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/snight1983/ds-harness-go/attachment"
	"github.com/snight1983/ds-harness-go/core/agent"
	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sdk/sdkprotocol"
	"github.com/snight1983/ds-harness-go/subagent/subagent"
)

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/sdk/server/src/invariant.ts:10
const PackageName = "@deepseek-ai/dsh-sdk-jsonrpc-server"

// PluginName 是这个包露面时用的名字。
//
// 源: packages/sdk/server/src/index.ts:20
const PluginName = "sdk-jsonrpc-server"

// ServerVersion 是握手时交回去的那个版本号。
//
// 源: packages/sdk/server/src/server.ts:124
const ServerVersion = "0.0.1"

// LLMService 是这台服务器用得到的那一小块 LLM 服务：问「这个提供方有没有适配器
// 认领」，以及握手时把那条路由真解算一遍。
//
// 源: packages/sdk/server/src/server.ts:294-296（listProviders）,
// packages/sdk/server/src/server.ts:155-161（resolveCallConfig）
//
// 新增: DSH 两处都是 `ctx.get('llm')`——整个服务注入进来，用到的只有这两个方法。
// 这里写成一个两方法的窄口子（窄口子的成例见
// [github.com/snight1983/ds-harness-go/goal/goalcommand.Service]），交进来的
// [llm.Runtime] 自然满足它。它可以为 nil，对应 DSH 那个 `?.`：这条线上根本没挂
// LLM 服务——那时 [Server.Initialize] 走不完，理由见那边。
type LLMService interface {
	// ListProviders 列出所有登记过的提供方路由。
	ListProviders() []llm.ProviderInfo
	// ResolveCallConfig 拿一份调用配置去对确切模型的能力，把适配器的默认落实进去。
	ResolveCallConfig(ctx context.Context, config llm.CallConfig) (llm.CallConfig, error)
}

// MountAdapter 在点名的提供方**还没有**适配器认领时补上一个，交回撤销这次挂载的
// 函数（可以为 nil，表示没什么要撤的）。
//
// 源: packages/sdk/server/src/server.ts:120-123
//
// 新增: DSH 那两行是写死的——提供方不是 `deepseek-official` 就当场报错，是的话就
// `ctx.plugin(LlmDeepSeek, {})` 挂上官方适配器，并把那个纤程记下来等收摊时拆掉。
// Go 这边没有插件加载器，而「哪个提供方值得兜底、用哪个适配器兜」本来就是部署口径
// 不是协议，所以整条兜底路收敛成这一个可选的钩子：装配方自己决定认哪些名字。
//
// 它为 nil，或者它交回错误，都等于 DSH 那句 `no adapter registered for provider`。
type MountAdapter func(ctx context.Context, provider string) (func(context.Context) error, error)

// Config 是这台服务器的装配面。
//
// 源: packages/sdk/server/src/index.ts:24-34（JsonRpcConfig）,
// packages/sdk/server/src/server.ts:38-41（HarnessSdkJsonRpcServerOptions）
//
// 新增: DSH 把它分成两半——插件配置（JsonRpcConfig，走 schemastery 校验）和服务器
// 选项（HarnessSdkJsonRpcServerOptions，插件从前者里挑出来递给后者）。这里合成一个：
// 那次拆分的理由是 schemastery 只描述得了可序列化的部署配置，而流、退出钩子这些
// 「运行期测试口子」得单开一层绕过它。Go 的结构体没有这个限制。
//
// DSH 那三个测试口子（input / output / exit）在这里一个都没有：流是
// [github.com/snight1983/ds-harness-go/sdk/sdkprotocol.NewLineTransport] 的入参，由装配方交给那条通道；
// 退出这个进程是装配方的事，不是这台服务器的事（理由见包文档）。
type Config struct {
	// Peer 是往对面发通知的那一面，必填。
	//
	// 源: packages/sdk/server/src/server.ts:67
	Peer sdkprotocol.Peer

	// Agents 是 agent 注册表，必填：这台服务器靠它建会话、也靠它验一个会话记录
	// 指着的 agent 还是不是活着的那一个。
	//
	// 源: packages/sdk/server/src/index.ts:22（inject = ['agents']）
	Agents *agent.Registry

	// Sessions 是会话存储，必填：会话日志事件和「新开了一个子会话」两条通知从它来。
	//
	// 源: packages/sdk/server/src/server.ts:71, 78
	Sessions *coresession.Store

	// Subagents 是子 agent 运行时，必填：「一次进程内的子 agent 跑完了」从它来。
	//
	// 源: packages/sdk/server/src/server.ts:87
	Subagents *subagent.Runtime

	// LLM 是那一小块 LLM 服务，可以为 nil，见 [LLMService]。
	LLM LLMService

	// Attachments 是附件库，可以为 nil：那时一轮带内联图片的输入被拒，对应 DSH
	// 那句 `SDK image prompt requires an attachment store`。
	//
	// 源: packages/sdk/server/src/server.ts:42-43
	Attachments attachment.Store

	// MountAdapter 是那条可选的兜底路，可以为 nil，见 [MountAdapter]。
	MountAdapter MountAdapter

	// MaxTokensAsSuccess 为真表示「撞上输出上限」算一个被接受的结果，而不是一次
	// 基础设施错误。
	//
	// 源: packages/sdk/server/src/server.ts:39-40
	//
	// 这是**部署口径**：同一个停止原因，有的部署认它有的部署不认，协议这一层不表态。
	MaxTokensAsSuccess bool

	// Logger 是记诊断的地方，可以为 nil（那时用 [log/slog.Default]）。
	//
	// 新增: DSH 靠 cordis 上下文自带的 logger。这里唯一往它上面记的是「一条通知发不
	// 出去」——那条路不能报错给谁看（见 [Server.notify]），不记的话它就彻底无声了。
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

// New 造一台服务器，把装配规矩查一遍。
//
// 源: packages/sdk/server/src/server.ts:65-69
//
// 新增: DSH 的构造函数当场就把四个监听器挂上去了。Go 这边挂监听要一个
// [context.Context] 和一个作用域，两样都不是构造期的东西，所以挪进了
// [Server.Install]——在那之前这台服务器一条边都不收，也办不了请求。
func New(config Config) (*Server, error) {
	switch {
	case config.Peer == nil:
		return nil, fmt.Errorf("sdkserver: 建一台 SDK 服务器需要一条往对面发东西的通道")
	case config.Agents == nil:
		return nil, fmt.Errorf("sdkserver: 建一台 SDK 服务器需要一张 agent 注册表")
	case config.Sessions == nil:
		return nil, fmt.Errorf("sdkserver: 建一台 SDK 服务器需要一台会话存储")
	case config.Subagents == nil:
		return nil, fmt.Errorf("sdkserver: 建一台 SDK 服务器需要一台子 agent 运行时")
	}
	return &Server{
		config:   config,
		sessions: map[string]agent.Handle{},
	}, nil
}
