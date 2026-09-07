// 本文件的作用：这条缝上的词汇——三个身份、一次投递、一条规则要的东西，
// 以及本包从六个协作方身上各借的那一小块。
//
// 源: packages/webhook/webhook/src/brand.ts, packages/webhook/webhook/src/types.ts

package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/snight1983/ds-harness-go/feature/interaction/permissionpresets"
	"github.com/snight1983/ds-harness-go/feature/preset/agentpresets"
	"github.com/snight1983/ds-harness-go/feature/workspace"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/scope"
)

// RuleID 标识一条程序化规则，全局唯一，只用来诊断。
//
// 源: packages/webhook/webhook/src/brand.ts:6
//
// 新增: DSH 那三个身份都是 `Branded<...>` 加一个恒等构造函数。Go 的具名 string
// 类型天生是标称类型，转换就是构造，用不着那个函数。
type RuleID string

// SourceID 标识一个配好的 webhook 适配器实例，比如 primary-github。
//
// 源: packages/webhook/webhook/src/brand.ts:9
type SourceID string

// DeliveryID 标识提供方的一次投递。
//
// 源: packages/webhook/webhook/src/brand.ts:12
//
// 它是**出处**，不是去重状态：本包不记它、不比它，同一个 id 投两次就跑两次。
type DeliveryID string

// VerifiedDelivery 是一次已经认过身份、也解析好了的提供方投递。
//
// 源: packages/webhook/webhook/src/types.ts:14-25（VerifiedWebhookDelivery）
//
// 名字里的 Verified 是一句前置条件，不是本包做过的事，见包文档。
type VerifiedDelivery struct {
	// Kind 是提供方族，比如 github。[Rule.Kind] 拿它配对。
	Kind string
	// Source 是那个配好的适配器实例。
	Source SourceID
	// DeliveryID 是提供方给这次投递的身份。
	DeliveryID DeliveryID
	// Event 是提供方规整过的事件体，一段无损 JSON。
	//
	// 新增: DSH 是 `WebhookEventOf<K>`——一个靠声明合并按 kind 取到具体类型的
	// 泛型，认不出的 kind 落回 `JsonValue`。Go 没有声明合并，而且这段字节要原样
	// 穿过本包交给规则，所以它是 [encoding/json.RawMessage]：解成 map[string]any
	// 再排回去会磨掉大整数的精度、也会重排键序，而规则很可能拿它算签名或者比对。
	Event json.RawMessage
	// ReceivedAt 是宿主收到它的时刻。
	//
	// 新增: DSH 是 Unix 毫秒整数，校验「非负安全整数」。Go 里它是
	// [time.Time]，对应的校验是「不是零值」。
	ReceivedAt time.Time
}

// validate 验一次投递的身份字段和事件体。
//
// 源: packages/webhook/webhook/src/index.ts:39-55（snapshotDelivery）
func (d VerifiedDelivery) validate() error {
	if d.Kind == "" {
		return fmt.Errorf("%w：投递的 kind 不能是空的", ErrInvalidDelivery)
	}
	if d.Source == "" {
		return fmt.Errorf("%w：投递的 source 不能是空的", ErrInvalidDelivery)
	}
	if d.DeliveryID == "" {
		return fmt.Errorf("%w：投递的 id 不能是空的", ErrInvalidDelivery)
	}
	if d.ReceivedAt.IsZero() {
		return fmt.Errorf("%w：投递的 receivedAt 不能是零值", ErrInvalidDelivery)
	}
	if !json.Valid(d.Event) {
		return fmt.Errorf("%w：投递的事件体不是无损 JSON", ErrInvalidDelivery)
	}
	return nil
}

// snapshot 把这次投递摘下来，好让它同时交给任意多条规则。
//
// 源: packages/webhook/webhook/src/index.ts:52-54
//
// 新增: DSH 用 snapshotJsonValue + deepFreeze 做出一个冻住的深拷贝。Go 里除了
// Event 那段字节以外每一个字段都是值，拷贝是自动的；那段字节要显式复制一份，
// 否则调用方在 Dispatch 返回之后改动那个切片，会同时改掉每一条正在跑的规则手里
// 的事件体。
func (d VerifiedDelivery) snapshot() VerifiedDelivery {
	d.Event = json.RawMessage(append([]byte(nil), d.Event...))
	return d
}

// ModelRoute 是一条规则给这次会话显式挑的模型路由与输出上限。
//
// 源: packages/webhook/webhook/src/types.ts:28-35（WebhookModelSelection）
//
// 新增: 不叫 ModelSelection，是为了不和
// [github.com/snight1983/ds-harness-go/harness/agent.ModelSelection] 撞名——那个
// 是「提供方 + 模型 + 推理档位」，这个是「提供方 + 模型 + 输出上限」，两者字段
// 不一样，同名会让每一个读的人都得先确认自己看的是哪一个。
type ModelRoute struct {
	// Provider 是登记过的提供方路由。
	Provider string
	// Model 是那个提供方自己拥有的模型标识。
	Model string
	// MaxTokens 是每次请求的输出上限；0 表示不设。
	//
	// 新增: DSH 是可选的正安全整数。Go 里 0 就是「不设」，和
	// [github.com/snight1983/ds-harness-go/harness/agent.Options].MaxTokens 同一套
	// 口径；负数当场拒。
	MaxTokens int
}

// SessionRequest 是一条规则唯一能让本包替它做的事：开一个根会话。
//
// 源: packages/webhook/webhook/src/types.ts:38-51（WebhookSessionRequest）
type SessionRequest struct {
	// WorkspacePath 是这次会话落在哪个工作区目录上，交给文件系统后端解析。
	//
	// 新增: DSH 在这里加了一条 `isAbsolute()` 校验。本仓库删掉了，理由见包文档。
	WorkspacePath string
	// Title 是这次会话的标题，必填。
	Title string
	// Prompt 是第一句提示词，必填。
	Prompt string
	// AgentPreset 是发布之前挂上去的那份 agent 组合。
	AgentPreset string
	// PermissionPreset 是放行提示词之前钉上去的那个权限档位。
	PermissionPreset string
	// Model 显式挑一条路由；nil 表示用这份部署当下的默认选择，连推理档位一起。
	//
	// 新增: DSH 是可选属性，缺席与给了空对象分得开。Go 用指针表达同一件事。
	Model *ModelRoute
}

// Rule 是受信任的程序化代码：看一次投递，可选地要一个会话。
//
// 源: packages/webhook/webhook/src/types.ts:54-69（WebhookRule）
//
// 新增: DSH 是一个带 id、kind、run 的对象字面量，`run` 的第二个参数是这条登记的
// AbortSignal。Go 里它是一个结构体，那个 signal 变成 [Rule.Run] 的第一个
// [context.Context]，规矩和本仓库每一处一样。
type Rule struct {
	// ID 是这条规则全局唯一的诊断身份。
	ID RuleID
	// Kind 是它认领的 provider kind，只有 [VerifiedDelivery.Kind] 和它一样的投递
	// 才会跑到它。
	Kind string
	// Run 跑任意受信任的代码，返回一个会话请求或者 nil。
	//
	// ctx 在这条登记被撤掉、或者整个运行时关掉时取消。返回 nil 表示这次不动作，
	// 那是一条正常的路，不是失败。
	Run func(ctx context.Context, delivery VerifiedDelivery) (*SessionRequest, error)
}

// validate 验一条规则的三个字段。
//
// 源: packages/webhook/webhook/src/index.ts:91-99
func (r Rule) validate() error {
	if r.ID == "" {
		return fmt.Errorf("%w：规则 id 不能是空的", ErrInvalidRule)
	}
	if r.Kind == "" {
		return fmt.Errorf("%w：规则 %q 的 kind 不能是空的", ErrInvalidRule, r.ID)
	}
	if r.Run == nil {
		return fmt.Errorf("%w：规则 %q 没有 Run", ErrInvalidRule, r.ID)
	}
	return nil
}

// Workspaces 是本包用得着的那一小块工作区登记能力。
//
// 源: packages/webhook/webhook/src/session.ts:133（`ctx.workspaceRegistry.create`）
//
// 只声明 Create 一个方法：本包既不列举也不删除工作区。
// [github.com/snight1983/ds-harness-go/feature/workspace.Registry] 结构上满足它。
type Workspaces interface {
	// Create 为一个已存在的目录建工作区，或者交出已经在那个目录上的那一个。
	Create(ctx context.Context, path string, title string) (workspace.Workspace, error)
}

// Agents 是本包用得着的那一小块 agent 注册表能力。
//
// 源: packages/webhook/webhook/src/session.ts:136（`ctx.agents.create`）、
// packages/webhook/webhook/src/session.ts:93（`agentCtx.on('agent/request', ...)`）
//
// [github.com/snight1983/ds-harness-go/harness/agent.Registry] 结构上满足它。
type Agents interface {
	// Create 造一个新 agent。
	Create(ctx context.Context, owner *scope.Scope, options agent.CreateOptions) (agent.Handle, error)
	// OnRequest 把一个观察者挂到调用配置那条瀑布上。
	OnRequest(
		ctx context.Context,
		owner *scope.Scope,
		observer agent.RequestObserver,
	) (func(context.Context) error, error)
}

// AgentPresets 是本包用得着的那一小块 agent 预设能力。
//
// 源: packages/webhook/webhook/src/session.ts:129-130,142
//
// [github.com/snight1983/ds-harness-go/feature/preset/agentpresets.Roster] 结构上
// 满足它。
type AgentPresets interface {
	// Resolve 找出这份预设，找不到或者它坏了就报错。
	Resolve(ctx context.Context, id string) (agentpresets.Preset, error)
	// StandingKeyFor 备好这份预设那份常驻组合，装不起来就报错。
	StandingKeyFor(ctx context.Context, id string) (*scope.Key, error)
	// Mount 把这份预设挂到一个 agent 的作用域上。
	Mount(ctx context.Context, agentKey *scope.Key, id string) (agentpresets.Preset, error)
}

// DefaultModel 是本包用得着的那一小块部署级默认模型能力。
//
// 源: packages/webhook/webhook/src/session.ts:64（`ctx.agentDefaultModel.currentSelection()`）
//
// [github.com/snight1983/ds-harness-go/harness/agentdefaultmodel.Service] 结构上
// 满足它。
type DefaultModel interface {
	// CurrentSelection 是这份部署此刻的默认选择，连推理档位一起。
	CurrentSelection() agent.ModelSelection
}

// Permissions 是本包用得着的那一小块权限预设能力。
//
// 源: packages/webhook/webhook/src/session.ts:128,153
//
// 两个方法分处事务的两头：Resolve 在动介质**之前**验一次档位名认不认识，
// SetFor 在会话建出来之后把它钉上去。
// [github.com/snight1983/ds-harness-go/feature/interaction/permissionpresets.Service]
// 结构上满足它。
type Permissions interface {
	// Resolve 查一个档位名，不在表里就报错。
	Resolve(name string) (permissionpresets.PresetSpec, error)
	// SetFor 把档位钉到一个 agent 上，不惊动模型。
	SetFor(agentKey *scope.Key, name string) error
}

// Failure 是一次派出去之后才发生的失败：Dispatch 早就返回了，没人接得住它。
//
// 源: packages/webhook/webhook/src/index.ts:150-157
//
// 新增: DSH 直接往 `ctx.logger` 上写两条不同级别的行。本仓库的包不自带日志器，
// 所以这里把那一行拆成结构化字段交给 [Config.Report]，[Failure.String] 拼出
// 和 DSH 一样的那句话。
type Failure struct {
	// Kind 是这次投递的 provider kind。
	Kind string
	// Source 是那个配好的适配器实例。
	Source SourceID
	// DeliveryID 是这次投递的身份。
	DeliveryID DeliveryID
	// RuleID 是跑这次的那条规则。
	RuleID RuleID
	// Stopped 为真表示这次是**被停掉的**，不是自己出错的：这条登记撤了或者整个
	// 运行时关了。DSH 那边这一支写的是 debug 级，另一支是 warn 级。
	Stopped bool
	// Rollback 非空表示这一条报的不是那次操作的死因，而是**退回去**的时候又
	// 失败了一次，值是退的那件事。
	//
	// 源: packages/webhook/webhook/src/session.ts:87-89
	//
	// 原来那个错照旧交回给调用它的地方，不会被这一条顶掉——把它换成「回滚失败」
	// 会让排查从第二现场开始。
	Rollback string
	// Err 是那个失败本身。
	Err error
}

// String 拼出这次失败的那一行，格式和 DSH 那三条日志一致。
func (f Failure) String() string {
	if f.Rollback != "" {
		return fmt.Sprintf("webhook: provider=%q source=%q delivery=%q rule=%q %s 回滚失败: %v",
			f.Kind, f.Source, f.DeliveryID, f.RuleID, f.Rollback, f.Err)
	}
	outcome := "failed"
	if f.Stopped {
		outcome = "stopped after disposal"
	}
	return fmt.Sprintf("webhook: provider=%q source=%q delivery=%q rule=%q %s: %v",
		f.Kind, f.Source, f.DeliveryID, f.RuleID, outcome, f.Err)
}
