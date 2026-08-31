// 本文件的作用：那道审批接缝本身——日志与通知这两条能力接缝、答复者链的登记与
// 派发、以及一次询问从回合围栏走到审计落地的全程。
//
// 源: packages/interaction/user-approval/src/index.ts:136-345

package userapproval

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"ds-harness-go/core/scope"
	"ds-harness-go/core/tools"
	"ds-harness-go/llm"
	"ds-harness-go/session"
)

// ErrInvalidConfig 表示配置本身不成立，构造被拒。
var ErrInvalidConfig = errors.New("userapproval: 配置不成立")

// PluginName 是本包注入那条策略切换通知时署的名。
//
// 源: packages/interaction/user-approval/src/index.ts:235
//
// 它进的是一份持久日志里那条消息的来源字段，所以是**上线的**字符串，改它等于改
// 已经写下去的历史的读法。
const PluginName = "user-approval"

// Log 是一条会话日志：读得出已有事件，也追加得进新事件。
//
// 新增: DSH 直接拿 `agent.session`，那是一个活着的 Session 对象。Go 里活会话是
// 循环那一块的东西（见 docs/DESIGN.md 第八节），本包在第 4 块，所以这里只声明
// 自己**真正要用**的那两件事：折策略和判回合围栏要读，写审计要追加。
//
// Append 只收类型和负载，seq 和时间由实现方派——那两个字段是日志自己的账，
// 一个追加方定不了它们。
type Log interface {
	// Events 交出这条日志到目前为止的全部事件，按日志顺序。
	Events() []session.Event
	// Append 往日志尾巴上追加一条事件。
	Append(kind session.EventType, data any) error
}

// Answerer 是答复者链上的一条：要么给出答复认领这次询问，要么调 next 让下一条来。
//
// 源: packages/interaction/user-approval/src/index.ts:22-31（`approval/request` 瀑布）
//
// 新增: DSH 是 cordis 的 waterfall 事件，答复者用 `ctx.on('approval/request', ...)`
// 挂上去。Go 里它是一条显式的规则链，和 [ds-harness-go/core/tools] 那几道瀑布同一个
// 形状：登记进 [Service.RegisterAnswerer]，按「先全局、再从最远的祖先到自己」的顺序
// 组合，最里面那层是那个失败关闭的默认答复。
//
// 新增: DSH 把 AbortSignal 塞在请求对象上。Go 里取消是第一个参数。
type Answerer func(
	ctx context.Context,
	request tools.ApprovalRequest,
	next func() (tools.ApprovalOutcome, error),
) (tools.ApprovalOutcome, error)

// Config 是这个服务的装配配置。
//
// 源: packages/interaction/user-approval/src/index.ts:176-185
type Config struct {
	// Policy 是这个部署给「没有自己切过策略的会话」定的默认值。
	//
	// 空串按 [PolicyAsk] 处理，和 DSH 那份 schema 的 `.default('ask')` 一致；
	// 词汇表外的值当场拒。
	Policy Policy

	// LogOf 从一个 agent 找到它那条会话日志。
	//
	// 新增: 顶掉 DSH 的 `agent.session`，理由见 [Log]。它是必填的：没有日志就写不出
	// 审计那一对，而一次不落审计的审批等于没发生过。
	LogOf func(agent *scope.Key) (Log, error)

	// Notify 把一条消息排进这个 agent 的下一次模型步。
	//
	// 新增: 顶掉 DSH 的 `agent.inject(createUserMessage(...))`。它也是必填的：
	// 一个切得动策略、却没法把这次切换告诉模型的服务是半个服务，而 [New] 正是
	// 发现这件事的地方——留到 [Service.SwitchPolicy] 头上再发现就晚了，那时策略
	// 已经写进日志了。
	Notify func(agent *scope.Key, message llm.Message) error

	// NewID 生成一次询问的标识；留空回落到 uuid。
	//
	// 新增: DSH 直接调 node:crypto 的 randomUUID。留一个口子是为了测得动那条
	// 「每次询问都换一个新 id」——不给定一个可控的发号器，就只能断言「两个 id
	// 不相等」，而那条断言对一个恒定的发号器也成立。
	NewID func() string
}

// Service 是审批这条能力的接缝。
//
// 源: packages/interaction/user-approval/src/index.ts:186-345
//
// 它的 [Service.Request] 就是 [ds-harness-go/core/tools.Approval]，所以装好之后
// 可以直接交给工具运行时当审批后端。
type Service struct {
	policy Policy
	logOf  func(*scope.Key) (Log, error)
	notify func(*scope.Key, llm.Message) error
	newID  func() string

	layers *scope.Layers[*answererLayer]
}

// 编译期钉住：一个装好的服务**就是**工具运行时要的那道审批接缝，中间不需要胶水。
var _ tools.Approval = (*Service)(nil)

// answererLayer 是一个作用域在答复者链上的全部贡献。
//
// 新增: cordis 的作用域分派在 Go 里靠 [ds-harness-go/core/scope.Layers] 表达，
// 做法和 core/tools 那几张规则表逐字相同。
type answererLayer struct {
	answerers *scope.AnonymousEntries[Answerer]
}

// newAnswererLayer 造一层。
func newAnswererLayer(*scope.Key) (*answererLayer, error) {
	return &answererLayer{answerers: scope.NewAnonymousEntries[Answerer]()}, nil
}

// IsEmpty 实现 [ds-harness-go/core/scope.Layer]。
func (l *answererLayer) IsEmpty() bool { return l.answerers.IsEmpty() }

// New 验一份配置，造出这个服务。
func New(config Config) (*Service, error) {
	policy := config.Policy
	if policy == "" {
		policy = PolicyAsk
	}
	if !KnownPolicy(policy) {
		return nil, fmt.Errorf("%w: 不认识的默认策略 %q", ErrInvalidConfig, config.Policy)
	}
	if config.LogOf == nil {
		return nil, fmt.Errorf("%w: 需要一条从 agent 找到会话日志的路", ErrInvalidConfig)
	}
	if config.Notify == nil {
		return nil, fmt.Errorf("%w: 需要一条把策略切换告诉模型的路", ErrInvalidConfig)
	}
	newID := config.NewID
	if newID == nil {
		newID = uuid.NewString
	}
	layers, err := scope.NewLayers(newAnswererLayer, nil)
	if err != nil {
		return nil, err
	}
	return &Service{
		policy: policy,
		logOf:  config.LogOf,
		notify: config.Notify,
		newID:  newID,
		layers: layers,
	}, nil
}

// RegisterAnswerer 往答复者链上接一条，返回撤销它的函数。
//
// 源: packages/interaction/user-approval/src/index.ts:22-31
//
// 落在哪一层由 owner 的身份决定：[ds-harness-go/core/scope.NewRoot] 造的作用域落在
// 全局层，一次询问不论为哪个 agent 发起都会走到它；有身份的作用域落在自己那一层，
// 只有那个 agent 及其子孙的询问看得见——这就是 DSH 那句「界面只答自己拥有的那些
// agent」。
func (s *Service) RegisterAnswerer(
	ctx context.Context,
	owner *scope.Scope,
	answerer Answerer,
) (func(context.Context) error, error) {
	if answerer == nil {
		return nil, fmt.Errorf("%w: 答复者不能是 nil", ErrInvalidConfig)
	}
	return s.layers.Effect(ctx, owner, func(layer *answererLayer) (func(), error) {
		return layer.answerers.Append(answerer), nil
	}, scope.EffectOptions{Label: "userapproval.RegisterAnswerer()", Silent: true})
}

// SetPolicy 往日志里追加一条会话策略覆盖——那是这件事**唯一**的持久表示。
//
// 源: packages/interaction/user-approval/src/index.ts:136-147
//
// 词汇表外的值在日志变化之前就被拒，一个字节都不写。读的一方每次自己折
// （[EffectivePolicy]），所以没有任何需要保持同步的缓存。
//
// 会话初始化用这个自由函数而不是 [Service.SwitchPolicy]：那时候没有一个「之前
// 可见的策略」可谈，也就没有要告诉模型的切换。
func SetPolicy(log Log, policy Policy) error {
	if !KnownPolicy(policy) {
		return fmt.Errorf("approval policy must be one of %q or %q", PolicyAsk, PolicyNever)
	}
	return log.Append(EventPolicy, PolicyData{Policy: policy})
}

// OverrideOf 只读这条会话自己的覆盖，不套用部署默认值。
//
// 源: packages/interaction/user-approval/src/index.ts:288-296
func (s *Service) OverrideOf(log Log) (Policy, bool) {
	return EffectivePolicy(log.Events())
}

// PolicyFor 交出这条会话此刻的有效策略：它自己的覆盖，没有就用部署默认值。
//
// 源: packages/interaction/user-approval/src/index.ts:278-287
//
// 新增: DSH 那边这个方法是私有的，构造函数里那段系统提示片段在闭包里调它。系统提示
// 服务在第 6 块，所以 Go 这边把它公开——装配方要拿它去喂 [PolicyStatement]。
func (s *Service) PolicyFor(log Log) Policy {
	if override, exists := EffectivePolicy(log.Events()); exists {
		return override
	}
	return s.policy
}

// SwitchPolicy 切一个活 agent 的策略，并把这次切换排进它下一次模型步。
//
// 源: packages/interaction/user-approval/src/index.ts:219-237（DSH 名字是 setPolicy）
//
// 值没变就什么都不做：既不写日志，也不拿一条「从 ask 变成 ask」的通知去占模型的
// 上下文。
func (s *Service) SwitchPolicy(agent *scope.Key, policy Policy) error {
	log, err := s.resolve(agent)
	if err != nil {
		return err
	}
	previous := s.PolicyFor(log)
	if previous == policy {
		return nil
	}
	if err := SetPolicy(log, policy); err != nil {
		return err
	}
	return s.notify(agent, llm.NewUserMessage(
		llm.Content{llm.TextBlock{Text: fmt.Sprintf(
			"The approval policy changed from %q to %q (changed by the user).", previous, policy)}},
		llm.PluginSource{Plugin: PluginName},
	))
}

// Request 问一次，交出答复。它实现 [ds-harness-go/core/tools.Approval]。
//
// 源: packages/interaction/user-approval/src/index.ts:239-276
//
// 顺序是：回合围栏 → 写 approval/asked → 判决 → 写 approval/decided。判决那一段
// **一定**给得出答复（取消是 cancelled，没人应答或者应答出错是 unavailable），
// 所以只要围栏过了，审计那一对就一定成双。
//
// 两次追加里任何一次没提交都报错而不是把答复交出去：交出一个没落审计的决定，
// 就是把那一对拆了。
func (s *Service) Request(
	ctx context.Context,
	request tools.ApprovalRequest,
) (tools.ApprovalOutcome, error) {
	log, err := s.resolve(request.Agent)
	if err != nil {
		return "", err
	}
	if !hasOpenTurn(log.Events()) {
		return "", errors.New(
			"approval.request() outside an open turn: the approval/asked + approval/decided " +
				"audit pair must be turn-enclosed (a bare event between turns is crash-tail " +
				"garbage on reload). Ask from inside the turn that needs the decision.")
	}
	id := RequestID(s.newID())
	if err := log.Append(EventAsked, AskedData{
		ID:       id,
		ToolName: request.ToolName,
		CallID:   request.CallID,
		Reason:   request.Reason,
	}); err != nil {
		return "", err
	}
	outcome := s.decide(ctx, request, log)
	if err := log.Append(EventDecided, DecidedData{ID: id, Outcome: outcome}); err != nil {
		return "", err
	}
	return outcome, nil
}

// resolve 找出这个 agent 那条会话日志。
func (s *Service) resolve(agent *scope.Key) (Log, error) {
	log, err := s.logOf(agent)
	if err != nil {
		return nil, err
	}
	if log == nil {
		// 一条 (nil, nil) 的答复是装配方的 bug，而它往下走就是解引用 panic。
		// 在这儿说清楚，比在 hasOpenTurn 里炸掉有用。
		return nil, fmt.Errorf("%w: 这个 agent 没有可写审计的会话日志", ErrInvalidConfig)
	}
	return log, nil
}

// decide 判出这一次询问的答复。它永远给得出一个词汇表里的值。
//
// 源: packages/interaction/user-approval/src/index.ts:298-344
func (s *Service) decide(
	ctx context.Context,
	request tools.ApprovalRequest,
	log Log,
) tools.ApprovalOutcome {
	if ctx.Err() != nil {
		// 已经取消了就不必再惊动任何人。
		return tools.ApprovalCancelled
	}
	// never 在**派发之前**就判掉，不做成一条答复者：一条后登记的答复者完全可能排在
	// 任何「闸」形状的答复者前面，那样「never 必然拒绝」这句承诺就取决于登记顺序了。
	// 只有服务自己的这条请求路径挡得住。
	if s.PolicyFor(log) == PolicyNever {
		return tools.ApprovalRejected
	}
	// 答复者链跑在自己的 goroutine 上，结果送进一个容量 1 的 channel。
	//
	// 新增: DSH 用 answer promise 和 signal 赛跑。Go 里链是同步调用，所以赛跑要
	// 这么写。容量 1 是关键：取消赢了之后没人再收这个 channel，而那次发送仍然
	// 立刻返回，于是那个 goroutine 跑完就走，不会挂在发送上泄漏；迟到的那个答复
	// 由构造本身丢掉，不必再判一次「是不是已经取消了」。
	answer := make(chan tools.ApprovalOutcome, 1)
	go func() { answer <- s.consult(ctx, request) }()
	select {
	case outcome := <-answer:
		return outcome
	case <-ctx.Done():
		return tools.ApprovalCancelled
	}
}

// consult 把这次询问交给答复者链，并把链的答复收敛成一个词汇表里的值。
//
// 源: packages/interaction/user-approval/src/index.ts:317-329
//
// 三种坏答复收敛成同一个失败关闭的值：链走到底也没人认领、某一条报了错、某一条
// 还回来一个词汇表外的字符串。最后一条在 Go 里同样拦得住——
// [ds-harness-go/core/tools.ApprovalOutcome] 是个开放的字符串类型，一条答复者写得出
// 任何值，而调用方那边是按四个常量分流的。
func (s *Service) consult(ctx context.Context, request tools.ApprovalRequest) tools.ApprovalOutcome {
	chain := s.answerers(request.Agent)
	next := func() (tools.ApprovalOutcome, error) { return tools.ApprovalUnavailable, nil }
	for index := len(chain) - 1; index >= 0; index-- {
		answerer, inner := chain[index], next
		next = func() (tools.ApprovalOutcome, error) { return answerer(ctx, request, inner) }
	}
	outcome, err := contain(next)
	if err != nil || !KnownOutcome(outcome) {
		return tools.ApprovalUnavailable
	}
	return outcome
}

// contain 跑一次答复者链，把它的 panic 变成一个错误。
//
// 新增: DSH 那边这一层挡的是「同步就抛的监听器」——它进不了 promise 链，会直接
// 逃到调用方那里。Go 的对应物是 panic：一条答复者多半是界面侧的第三方代码，它炸了
// 该让**这个问题**失败关闭，而不是让调用方那次工具调用连着整条 agent 循环一起炸。
func contain(next func() (tools.ApprovalOutcome, error)) (outcome tools.ApprovalOutcome, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			outcome, err = "", fmt.Errorf("userapproval: 一条答复者 panic 了：%v", recovered)
		}
	}()
	return next()
}

// answerers 按「先全局、再从最远的祖先到自己」的顺序收齐这条链。
//
// 源: packages/interaction/user-approval/src/index.ts:318-319（scopeTarget 的作用域分派）
func (s *Service) answerers(key *scope.Key) []Answerer {
	var chain []Answerer
	for answerer := range s.layers.Global().answerers.Values() {
		chain = append(chain, answerer)
	}
	if key == nil {
		return chain
	}
	for _, layer := range s.layers.ChainLayers(key) {
		for answerer := range layer.answerers.Values() {
			chain = append(chain, answerer)
		}
	}
	return chain
}
