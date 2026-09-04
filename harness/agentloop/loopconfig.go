// 本文件的作用：装这台循环要交进来的那份配置——设置命名空间、预置 Agent 身份、
// 会话持久化这道接缝，以及交进来之前先拒掉哪些不成立的组合。
//
// 源: packages/core/agent-loop/src/index.ts:1-713

package agentloop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/settings"
)

// ---- 启动器指定的会话身份 ----

// LauncherAgentIdentity 是启动器为一个配置出来的 agent 选定的那一个会话身份。
//
// 源: packages/core/agent-loop/src/index.ts:245-256（LauncherAgentIdentity）
//
// Resume 把「读回已有的持久化历史」和「用这个确切身份新建一个会话」分开，
// 这两件事在配置里对应 resumeSessionId 和 sessionId 两个键。
type LauncherAgentIdentity struct {
	// ID 是要新建或者续跑的那个确切会话标识。
	ID sessionlog.SessionID
	// Resume 为真表示读回已有的持久化历史，而不是新建这个会话。
	Resume bool
}

// ConfiguredAgentIdentities 是按配置项的 id 索引的那些启动器身份。
//
// 源: packages/core/agent-loop/src/index.ts:258-259（ConfiguredAgentIdentities）
//
// 新增: DSH 那边这份表是启动器在任何 Loader 条目挂载之前经
// `ctx.provide(CONFIGURED_AGENT_IDENTITIES_KEY, ...)` 放到 cordis 上下文上的，
// 目的是「让身份不经过配置键，这样一份改掉模型路由的覆盖配置就冲不掉它」。
// Go 里没有那个万能上下文，这份表就是 [Config.LauncherIdentities] 一个字段；
// 「覆盖配置冲不掉它」在 Go 里由 [applyLauncherIdentities] 的执行次序保证——
// 它在校验之前跑，且**两个身份键一起换掉**。
// 也因此 DSH 那个 CONFIGURED_AGENT_IDENTITIES_KEY 常量在这里没有对应物。
type ConfiguredAgentIdentities map[string]LauncherAgentIdentity

// applyLauncherIdentities 把启动器指定的身份盖到配置出来的那些 agent 上。
//
// 源: packages/core/agent-loop/src/index.ts:213-233
//
// 每一个被启动器点名的条目，**两个身份键一起换掉**——这样一个配置里写的身份
// 永远不可能和一个启动器给的身份并存。没被点名的条目原样保留自己的身份。
func applyLauncherIdentities(
	agents []ConfiguredAgent,
	identities ConfiguredAgentIdentities,
) []ConfiguredAgent {
	if identities == nil {
		return agents
	}
	applied := make([]ConfiguredAgent, len(agents))
	for index, configured := range agents {
		identity, named := identities[configured.ID]
		if !named {
			applied[index] = configured
			continue
		}
		configured.SessionID = ""
		configured.ResumeSessionID = ""
		if identity.Resume {
			configured.ResumeSessionID = identity.ID
		} else {
			configured.SessionID = identity.ID
		}
		applied[index] = configured
	}
	return applied
}

// ---- 设置 ----

// SettingsNamespace 是本包那个设置小节的命名空间。
//
// 源: packages/core/agent-loop/src/index.ts:292-293（AGENT_LOOP_SETTINGS_NAMESPACE）
var SettingsNamespace = mustNamespace("agent-loop")

// mustNamespace 把一个**字面量**命名空间解出来，不合法就 panic。
//
// 新增: DSH 的 settingsNamespace() 在 TS 里就是一次品牌化转换，编译期定死。
// Go 里 [settings.NewNamespace] 会验一遍并返回错误，而这里的入参是一个包级常量
// 字面量——它不合法说明本包写错了，不是运行期可以恢复的情况。
func mustNamespace(value string) settings.Namespace {
	namespace, err := settings.NewNamespace(value)
	if err != nil {
		panic(fmt.Sprintf("harness/agentloop: 命名空间字面量不合法：%v", err))
	}
	return namespace
}

// Settings 是本包里那几个由用户拥有的字段。
//
// 源: packages/core/agent-loop/src/index.ts:295-303（AgentLoopSettings）
//
// 它**刻意**是 [Config] 的一个真子集：Agents 是一份开机时消费一次的组装清单，
// 存下来的改动只会看起来像是生效了。
type Settings struct {
	// MaxParallelToolCalls 是每个步骤里同时在飞的并行安全调用上限。
	MaxParallelToolCalls int `json:"maxParallelToolCalls"`
}

// ---- 配置 ----

// ConfiguredAgent 是配置里声明的一个开机就起的 agent。
//
// 源: packages/core/agent-loop/src/index.ts:260-271
type ConfiguredAgent struct {
	// ID 是这一项在日志里的稳定标签，也是铸新身份时的前缀。
	ID string
	// SessionID 是可选的稳定身份：重挂时读回它已经落地的历史，第一次用则新建它。
	SessionID sessionlog.SessionID
	// WorkspaceID 是新建会话时归属的工作区登记；空串表示不属于任何工作区。
	WorkspaceID sessionlog.WorkspaceID
	// ResumeSessionID 是要续跑的那段持久化会话，给了就不新建。
	ResumeSessionID sessionlog.SessionID
	// Options 是这个 agent 自己的提供方路由与模型。
	Options agent.Options
}

// Config 是这个循环工厂的配置。
//
// 源: packages/core/agent-loop/src/index.ts:310-328（Config）
type Config struct {
	// MaxParallelToolCalls 是每个步骤里同时在飞的并行安全调用上限。
	// 1 表示串行；0 表示没给，用 [DefaultMaxParallelToolCalls]。
	//
	// 新增: DSH 是 `maxParallelToolCalls?: number`，分得开「没给」和「给了 0」。
	// Go 里这里用 0 表示没给，理由和 [github.com/snight1983/ds-harness-go/llm.CallConfig].MaxTokens
	// 那一条一样：上限为零的池一个工具调用都跑不动，没人会那么要求，
	// 所以零值不和任何真实取值撞车。
	MaxParallelToolCalls int

	// Agents 是插件启动时就创建或者续跑的那些 agent。
	Agents []ConfiguredAgent

	// LauncherIdentities 是启动器指定的那些会话身份，按 [ConfiguredAgent.ID] 索引。
	// 为 nil 表示每一项都保留自己配置里的身份。
	LauncherIdentities ConfiguredAgentIdentities

	// Settings 是那份设置提供方，为 nil 表示本部署不让用户改并行上限——那时
	// 上限锁在 MaxParallelToolCalls 解出来的那个值上。
	//
	// 新增: DSH 的 installSettingsSection 拿的是 cordis 上下文，设置服务在不在
	// 由 cordis 决定。Go 里它是一个显式的、可以为 nil 的依赖。
	Settings *settings.Provider

	// Persistence 是会话持久化，为 nil 表示本部署没接持久化——那时
	// [AgentLoop.Resume] 报错，配置里那些要续跑的项也起不来。
	Persistence SessionPersistence

	// Logger 用来报配置驱动的启动失败，为 nil 时用 [slog.Default]。
	Logger *slog.Logger
}

// SessionPersistence 是本包**用得上的那一小块**会话持久化能力。
//
// 源: packages/core/agent-loop/src/index.ts:28（`import type { SessionPersistence }`）
//
// 新增: 这是一个**消费方一侧**声明的接口，不是从持久化那个包引来的。DSH 引的是
// 那边的服务类型，而 Go 这边 [github.com/snight1983/ds-harness-go/feature/persistence.Store] 到目前为止
// 还没有 Prepare（它那份文档说明协调器、会话预备件和 Store.Prepare 一起推迟到收尾
// 那一块做）。在消费方这边按**实际用到的两个方法**声明接口，是 Go 的常规做法：
// 本包因此不必等那个包写完，而那个包写完之后，只要方法名对得上就直接满足这里。
type SessionPersistence interface {
	// Prepare 读回一段持久化会话，交出一段**还没登记进存储**的准备期。
	//
	// 交出来的会话由调用方负责公布或者丢弃，两条路都要调
	// [github.com/snight1983/ds-harness-go/harness/session.Preparation.Release]：
	// 提供方（比如持久化编排器）在准备期里攥着一份预留，公布成了它被接手、
	// 半路放弃了它要还回去。释放是幂等的，所以直接 defer 就行。
	Prepare(ctx context.Context, id sessionlog.SessionID) (*session.Preparation, error)

	// List 列出所有落了地的会话头。
	//
	// [AgentLoop.restoreOrCreateConfigured] 拿它区分「这个存档根本不存在」
	// 和「读它的时候出事了」。
	List(ctx context.Context) ([]sessionlog.SessionHeader, error)
}

// ---- 配置校验 ----

// resolveMaxParallelToolCalls 在拥有这份配置的边界上定下那个部署级的调度上限。
//
// 源: packages/core/agent-loop/src/index.ts:132-139
func resolveMaxParallelToolCalls(value int) (int, error) {
	if value == 0 {
		return DefaultMaxParallelToolCalls, nil
	}
	if value < 1 {
		return 0, errors.New("maxParallelToolCalls must be a positive integer")
	}
	return value, nil
}

// assertAgentOptions 拒掉一个在请求介质上表达不出来的输出上限。
//
// 源: packages/core/agent-loop/src/index.ts:141-147
//
// 新增: DSH 查的是 `Number.isSafeInteger(maxTokens) && maxTokens > 0`——JS 的
// number 是浮点，非整数和超出安全整数范围的值都得挡。Go 的 int 天生是整数，
// 所以只剩下正负这一条。0 在 Go 这边表示「不设」（见 [agent.Options].MaxTokens），
// 对应 DSH 的 undefined，照样放行。
func assertAgentOptions(options agent.Options) error {
	if options.MaxTokens < 0 {
		return errors.New("agent maxTokens must be a positive safe integer")
	}
	return nil
}

// validateConfiguredAgents 在任何配置出来的 agent 起步之前，拒掉这份配置自己内部的身份冲突。
//
// 源: packages/core/agent-loop/src/index.ts:277-293
func validateConfiguredAgents(agents []ConfiguredAgent) error {
	exactIdentities := make(map[sessionlog.SessionID]string, len(agents))
	for _, configured := range agents {
		hasResumeID := configured.ResumeSessionID != ""
		if configured.SessionID != "" && hasResumeID {
			return fmt.Errorf("agent %q: sessionId and resumeSessionId are mutually exclusive", configured.ID)
		}
		exactIdentity := configured.SessionID
		if hasResumeID {
			exactIdentity = configured.ResumeSessionID
		}
		if exactIdentity == "" {
			continue
		}
		if firstID, taken := exactIdentities[exactIdentity]; taken {
			return fmt.Errorf("agents %q and %q use duplicate exact session identity %q",
				firstID, configured.ID, string(exactIdentity))
		}
		exactIdentities[exactIdentity] = configured.ID
	}
	return nil
}
