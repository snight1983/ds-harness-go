// 本文件的作用：那张登记着规则的表，以及「派出去就不管了」这件事本身——
// 一次投递怎么散给当下每一条认领它的规则，一条登记撤掉时怎么把在跑的那些收干净。
//
// 源: packages/webhook/webhook/src/index.ts

package webhook

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// 本包的失败分类。
//
// 新增: DSH 抛的是 `TypeError` 和光的 `Error`，调用方只能比字符串。Go 这边它们是
// 哨兵，[errors.Is] 分得开。
var (
	// ErrInvalidConfig 表示交给 [New] 的那份配置缺了东西。
	ErrInvalidConfig = errors.New("webhook: 配置不完整")
	// ErrInvalidRule 表示这条规则的三个字段有一个不合法。
	ErrInvalidRule = errors.New("webhook: 规则不合法")
	// ErrDuplicateRule 表示这个规则 id 已经登记过一条了。
	ErrDuplicateRule = errors.New("webhook: 这个规则 id 已经登记过了")
	// ErrInvalidDelivery 表示这次投递的身份字段或者事件体不合法。
	ErrInvalidDelivery = errors.New("webhook: 投递不合法")
	// ErrInvalidRequest 表示规则交回来的那个会话请求缺了必填项。
	ErrInvalidRequest = errors.New("webhook: 会话请求不合法")
	// ErrClosing 表示这个运行时正在关，不再收新登记也不再派新投递。
	ErrClosing = errors.New("webhook: 运行时正在关闭")
)

// Config 是造一个 [Runtime] 要交进来的东西。
//
// 源: packages/webhook/webhook/src/index.ts:59-66（`static inject`）
//
// DSH 那六个 cordis 服务在这里全部显式：五个是窄接口，第六个（改名）是一个函数，
// 理由见包文档。除了 [Config.NewSessionID]，每一项都必填。
type Config struct {
	// Workspaces 用来把会话落到一个工作区目录上。
	Workspaces Workspaces
	// Agents 用来造 agent、以及挂那条创建期的模型选择。
	Agents Agents
	// AgentPresets 用来解析并挂上那份 agent 组合。
	AgentPresets AgentPresets
	// DefaultModel 是规则没显式挑路由时用的那一份部署默认选择。
	DefaultModel DefaultModel
	// Permissions 用来验档位名、并在会话建出来之后把它钉上去。
	Permissions Permissions

	// Rename 给这条新会话起标题。
	//
	// 新增: DSH 是 `ctx.sessionTitle.rename(handle.agent.session, title)`。Go 这边
	// [github.com/snight1983/ds-harness-go/feature/sessiontitle.Service.Rename] 收的是
	// 它自己那个窄 Session 接口，而
	// [github.com/snight1983/ds-harness-go/harness/session.Session] 的 Append 签名和
	// 它对不上（一个收候选事件并交出落定的那条，一个收类型加负载），中间那层桥属于
	// 装配。所以这里收一个函数而不是一个接口。
	Rename func(a agent.Agent, title string) error

	// Owner 是造出来的 agent 归哪个作用域管。
	//
	// 提示词放行之后本包就不再碰这个 agent 了，它此后和任何别的会话一样归这里。
	Owner *scope.Scope

	// Report 收每一次派出去之后才发生的失败。
	//
	// 它必填：Dispatch 早就返回了，这是这些失败**唯一**能被看见的地方，
	// 挂空等于把它们全丢掉。
	Report func(Failure)

	// NewSessionID 造这次会话的身份；为 nil 时是 "webhook-" 加一个 uuid。
	//
	// 源: packages/webhook/webhook/src/session.ts:135
	NewSessionID func() sessionlog.SessionID
}

// validate 验这份配置的每一项。
func (c Config) validate() error {
	missing := ""
	switch {
	case c.Workspaces == nil:
		missing = "Workspaces"
	case c.Agents == nil:
		missing = "Agents"
	case c.AgentPresets == nil:
		missing = "AgentPresets"
	case c.DefaultModel == nil:
		missing = "DefaultModel"
	case c.Permissions == nil:
		missing = "Permissions"
	case c.Rename == nil:
		missing = "Rename"
	case c.Owner == nil:
		missing = "Owner"
	case c.Report == nil:
		missing = "Report"
	}
	if missing != "" {
		return fmt.Errorf("%w：还缺 %s", ErrInvalidConfig, missing)
	}
	return nil
}

// registration 是一条登记，外加此刻正在用它的那些调用。
//
// 源: packages/webhook/webhook/src/index.ts:29-36（RuleRegistration）
//
// 新增: DSH 的 AbortController 在这里是一对 ctx/cancel；那个 `active:
// Set<Promise<void>>` 是一个 [sync.WaitGroup]；`disposal ??= (...)` 那次记忆化
// 是一个 [sync.Once]——第二个撤销者会在 Do 上等着，和 DSH 那边 await 同一个
// promise 的效果一样。
type registration struct {
	rule   Rule
	ctx    context.Context
	cancel context.CancelCauseFunc
	active sync.WaitGroup
	once   sync.Once

	// closing 由 [Runtime.mutex] 守着，表示这条登记不再接新调用。
	closing bool
}

// Runtime 是那张规则表。
//
// 源: packages/webhook/webhook/src/index.ts:58（WebhookRuntime）
//
// 新增: DSH 是单线程 JS，那张 Map 裸着用。Go 里登记、派发、撤销分属不同 goroutine，
// 所以有一把锁。规矩和本仓库别处一样：**规则回调和 [Config.Report] 一律在锁外调**，
// 于是一条规则回头再登记一条不会自锁。
type Runtime struct {
	config Config

	mutex   sync.Mutex
	rules   map[RuleID]*registration
	closing bool
}

// New 造一个规则运行时。
func New(config Config) (*Runtime, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	if config.NewSessionID == nil {
		config.NewSessionID = func() sessionlog.SessionID {
			return sessionlog.SessionID("webhook-" + uuid.NewString())
		}
	}
	return &Runtime{config: config, rules: make(map[RuleID]*registration)}, nil
}

// Register 登记一条受信任的程序化规则，返回撤销它的那个函数。
//
// 源: packages/webhook/webhook/src/index.ts:89-119
//
// ctx 是这条登记的寿命：它取消、撤销函数被调、或者 [Runtime.Close] 跑过之后，
// 这条规则不再收投递，正在跑的那些也会收到取消。
//
// 撤销函数**会等**：它先把这条登记藏起来，再取消，最后等在跑的调用全部退出才返回。
// 重复调是空操作，并发调都会等到同一次收尾做完——这正是 DSH 那个 `disposal ??=`
// 记下来的东西。
func (r *Runtime) Register(ctx context.Context, rule Rule) (func(), error) {
	if err := rule.validate(); err != nil {
		return nil, err
	}
	ruleCtx, cancel := context.WithCancelCause(ctx)
	reg := &registration{rule: rule, ctx: ruleCtx, cancel: cancel}

	r.mutex.Lock()
	if r.closing {
		r.mutex.Unlock()
		cancel(ErrClosing)
		return nil, ErrClosing
	}
	if _, exists := r.rules[rule.ID]; exists {
		r.mutex.Unlock()
		cancel(ErrDuplicateRule)
		return nil, fmt.Errorf("%w：%q", ErrDuplicateRule, rule.ID)
	}
	r.rules[rule.ID] = reg
	r.mutex.Unlock()

	// 新增: 登记的上游寿命结束时也要走同一条排干路径，避免已取消的规则继续接收投递。
	context.AfterFunc(ruleCtx, func() { r.dispose(reg) })

	return func() { r.dispose(reg) }, nil
}

// Dispatch 把一次投递散给当下每一条认领它的规则，在任何一条跑完之前就返回。
//
// 源: packages/webhook/webhook/src/index.ts:126-133
//
// 返回的 error 只说这次**派**成不成立——运行时在关、或者这次投递不合法。规则跑出来
// 的失败一律走 [Config.Report]，见包文档。
func (r *Runtime) Dispatch(delivery VerifiedDelivery) error {
	if err := delivery.validate(); err != nil {
		return err
	}
	snapshot := delivery.snapshot()

	r.mutex.Lock()
	if r.closing {
		r.mutex.Unlock()
		return ErrClosing
	}
	matched := make([]*registration, 0, len(r.rules))
	for _, reg := range r.rules {
		if reg.closing || reg.rule.Kind != snapshot.Kind {
			continue
		}
		// Add 必须在锁里：收尾那边是先在锁里把 closing 立起来、再 Wait，
		// 所以这一对配起来之后，没有任何一次 Add 会落在 Wait 开始之后。
		reg.active.Add(1)
		matched = append(matched, reg)
	}
	r.mutex.Unlock()

	for _, reg := range matched {
		go r.run(reg, snapshot)
	}
	return nil
}

// Close 关掉这个运行时：不再收登记、不再派投递，并把每一条还在的登记收干净。
//
// 源: packages/webhook/webhook/src/index.ts:75-81
//
// 它和撤销函数一样**会等**，返回时每一条在跑的规则都已经退出。
func (r *Runtime) Close() {
	r.mutex.Lock()
	r.closing = true
	pending := make([]*registration, 0, len(r.rules))
	for _, reg := range r.rules {
		pending = append(pending, reg)
	}
	r.mutex.Unlock()

	for _, reg := range pending {
		r.dispose(reg)
	}
}

// dispose 收掉一条登记：先藏起来，再取消，最后排干。
//
// 源: packages/webhook/webhook/src/index.ts:165-175
func (r *Runtime) dispose(reg *registration) {
	reg.once.Do(func() {
		r.mutex.Lock()
		reg.closing = true
		if current, exists := r.rules[reg.rule.ID]; exists && current == reg {
			delete(r.rules, reg.rule.ID)
		}
		r.mutex.Unlock()

		reg.cancel(fmt.Errorf("webhook: 规则 %q 的登记被撤了", reg.rule.ID))
		reg.active.Wait()
	})
}

// run 跑一次被兜住的调用，失败交给 [Config.Report]。
//
// 源: packages/webhook/webhook/src/index.ts:136-162
//
// 计数是 [Runtime.Dispatch] 在锁里加上的，这里只负责减。
func (r *Runtime) run(reg *registration, delivery VerifiedDelivery) {
	defer reg.active.Done()
	err := r.invoke(reg, delivery)
	if err == nil {
		return
	}
	r.config.Report(Failure{
		Kind:       delivery.Kind,
		Source:     delivery.Source,
		DeliveryID: delivery.DeliveryID,
		RuleID:     reg.rule.ID,
		Stopped:    reg.ctx.Err() != nil,
		Err:        err,
	})
}

// invoke 是一次调用本身：跑规则，规则要会话就开一个。
//
// 源: packages/webhook/webhook/src/index.ts:137-149
func (r *Runtime) invoke(reg *registration, delivery VerifiedDelivery) error {
	if err := reg.ctx.Err(); err != nil {
		return context.Cause(reg.ctx)
	}
	request, err := reg.rule.Run(reg.ctx, delivery)
	if err != nil {
		return err
	}
	if err := reg.ctx.Err(); err != nil {
		return context.Cause(reg.ctx)
	}
	if request == nil {
		return nil
	}
	return r.createSession(reg.ctx, delivery, reg.rule.ID, *request)
}
