// 本文件的作用：这个包自己拥有的那几条持久不变量——一条重试必须落在开着的那个
// 步骤里、必须接着同一条链往下数、而那条链的身份不许被别人借走；一条
// llm/retry-started 必须找得到它配对的那次排期，而且只熬过去一次。
//
// 源: packages/llm/llm-retry/src/invariant.ts

package llmretry

import (
	"context"
	"fmt"
	"strconv"

	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
)

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/llm/llm-retry/src/invariant.ts:11
const PackageName = "@deepseek-ai/dsh-llm-retry"

// chainKey 认一条重试链：同一个步骤上、同一个提供方、同一份策略排出来的那几次重试。
//
// 源: packages/llm/llm-retry/src/invariant.ts（那句 findLast 的四个比较项）
//
// policyKey 也在键里，是因为「策略在两次失败之间被换掉了」必须换一条链：接着上一份
// 策略的账往下数的话，一次把 maxRetries 从 2 调到 5 的改动会被算成「已经重试过 2 次」，
// 而用户看到的是一份写着 5 的新策略。
type chainKey struct {
	turn      int
	step      int
	provider  string
	policyKey string
}

// chainState 是一条链走到当前为止的样子。
type chainState struct {
	// retry 是这条链上最后那次重试的序号。
	retry int
	// retryID 是这条链的身份，链上每一条 llm/retry 都带着它。
	retryID RetryID
}

// attemptKey 认一次具体的重试尝试：哪条链上的第几次。
type attemptKey struct {
	retryID RetryID
	retry   int
}

// Trace 是一条日志走到当前为止、和重试有关的那点状态。
//
// 源: packages/llm/llm-retry/src/invariant.ts:21-171
//
// 新增: DSH 那边每验一条事件都把 `session.events.slice(0, index)` 整段重扫一遍
// （findLast、findLastIndex、some 各扫一趟）。Go 这边全部化成增量状态，一条事件
// 只走一次。这不只是快慢的事：DSH 交给不变量的那段历史**不含**正在验的这条事件
// （collectSessionCallbacks 在 this.log.push 之前跑），而 Go 的
// [github.com/snight1983/ds-harness-go/core/session.Session.Events] 在观察者里**已经含着**它
// （commit 先 append、再叫观察者）。照抄那几句 findLast 会整整差一条——每一次
// 重试都会因为「翻到了自己」而把 retry 算成 prior+1 的下一个。验和改分成两步之后，
// 这个不对称就不存在了：验的那一刻这条事件还没进 Trace。
//
// 零值不可用，用 [NewTrace] 造。
type Trace struct {
	// position 是已经走过的事件条数，给 stepStarts / lastStepClose 做先后比较。
	position int

	// turnOpen / turn 是最后一条 turn/start|turn/end 留下的状态。
	turnOpen bool
	turn     int

	// stepBoundaryOpen / stepTurn / step 是最后一条 step/start|step/end 留下的状态。
	//
	// 它和下面 stepStarts 那一组**不是**一回事：DSH 的步骤边界检查只看这两种事件，
	// 而 providerForOpenStep 里的「开着」把 turn/end 也算成一次收尾。两条检查的宽严
	// 不同，这里逐字跟着 DSH，各记各的。
	stepBoundaryOpen bool
	stepTurn         int
	step             int

	// stepStarts 记每个步骤最后那条 step/start 出现在第几位。
	stepStarts map[stepRef]int
	// lastStepClose 是最后一条 step/end 或者 turn/end 出现在第几位。
	lastStepClose int

	// provider / hasProvider 是最后一条 request/header 报的提供方。
	provider    string
	hasProvider bool

	// chains 是每条重试链走到当前为止的样子。
	chains map[chainKey]chainState
	// owners 是所有在 llm/retry 或者 llm/retry-started 上出现过的链身份。
	owners map[RetryID]struct{}
	// scheduled 记每次已排期的重试落在哪个步骤上。
	scheduled map[attemptKey]stepRef
	// started 记哪几次重试的那段等待已经熬过去了。
	started map[attemptKey]struct{}
}

// NewTrace 造一条空的轨迹：还没进任何回合、任何步骤，也没有任何重试链。
func NewTrace() *Trace {
	return &Trace{
		stepStarts: map[stepRef]int{},
		chains:     map[chainKey]chainState{},
		owners:     map[RetryID]struct{}{},
		scheduled:  map[attemptKey]stepRef{},
		started:    map[attemptKey]struct{}{},
	}
}

// transitionKind 是一条转移要落下去的那类改动。
type transitionKind int

const (
	// transitionNone 表示这条事件只动到路由那点状态（也可能什么都没动）。
	transitionNone transitionKind = iota
	transitionRetry
	transitionStarted
)

// Transition 是一条已经验过、还没落下去的改动。
//
// 源: packages/llm/llm-retry/src/invariant.ts:52-56
//
// 分成「验」和「落」两步，理由和 [github.com/snight1983/ds-harness-go/compaction.Transition] 逐字相同。
//
// 新增: 字段全不导出。[github.com/snight1983/ds-harness-go/interaction/userapproval.Transition] 那边导出了
// 两个字段，因为它落下去的就是「哪次询问、开还是关」这两件人看得懂的事；这里要落的
// 是四张表上的五处改动，导出它们只会把本包的内部记账变成外部可以依赖的东西。
type Transition struct {
	kind    transitionKind
	route   routeDelta
	chain   chainKey
	state   chainState
	attempt attemptKey
	ref     stepRef
}

// Validate 验一条候选事件，交出它被接受之后要做的那点改动，**不改动**这条轨迹。
//
// 源: packages/llm/llm-retry/src/invariant.ts:26-171
//
// 新增: DSH 那边收一个 fail 回调、一条事件里能报几条就报几条。Go 这边返回**第一条**
// 违例，和 [github.com/snight1983/ds-harness-go/session.Trace.Validate] 一致——它因此可以脱离不变量注册表
// 单独用，而 [RegisterInvariants] 只是把这个错误接到 [invariants.Fail] 上。
func (t *Trace) Validate(event session.Event) (Transition, error) {
	route, err := routeTransition(event)
	if err != nil {
		return Transition{}, fmt.Errorf("%w：seq %d：%w", ErrInvariantViolated, event.Seq, err)
	}
	next := Transition{route: route}

	switch event.Type {
	case EventRetry:
		data, err := DecodeRetry(event)
		if err != nil {
			return Transition{}, fmt.Errorf("%w：seq %d：%w", ErrInvariantViolated, event.Seq, err)
		}
		chain, state, err := t.validateRetry(event.Seq, data)
		if err != nil {
			return Transition{}, err
		}
		next.kind = transitionRetry
		next.chain, next.state = chain, state
		next.attempt = attemptKey{retryID: data.RetryID, retry: data.Retry}
		next.ref = stepRef{turn: data.Turn, step: data.Step}
	case EventRetryStarted:
		data, err := DecodeRetryStarted(event)
		if err != nil {
			return Transition{}, fmt.Errorf("%w：seq %d：%w", ErrInvariantViolated, event.Seq, err)
		}
		if err := t.validateStarted(event.Seq, data); err != nil {
			return Transition{}, err
		}
		next.kind = transitionStarted
		next.attempt = attemptKey{retryID: data.RetryID, retry: data.Retry}
	}
	return next, nil
}

// validateRetry 验一条 llm/retry。
//
// 源: packages/llm/llm-retry/src/invariant.ts:26-140
func (t *Trace) validateRetry(seq int, data RetryData) (chainKey, chainState, error) {
	var (
		key   chainKey
		state chainState
	)
	fail := func(format string, args ...any) (chainKey, chainState, error) {
		return key, state, fmt.Errorf("%w：seq %d 的 %s %s",
			ErrInvariantViolated, seq, EventRetry, fmt.Sprintf(format, args...))
	}

	if data.RetryID == "" {
		return fail("没有链身份（retryId）")
	}
	if err := validateFailure(seq, EventRetry, data.Failure); err != nil {
		return key, state, err
	}
	if data.Retry < 1 {
		return fail("的 retry 是 %d，重试序号从 1 起", data.Retry)
	}
	if data.Provider == "" {
		return fail("没有提供方")
	}
	if data.PolicyKey == "" {
		return fail("没有策略指纹（policyKey）")
	}

	// 档位和 maxRetries 必须对得上，而且这次重试不能已经越过上限。
	//
	// 源: packages/llm/llm-retry/src/invariant.ts（那两支 mode 判别）
	//
	// 越过上限的那条查的是「这条事件本身就不该被排出来」：策略说最多重 2 次，
	// 日志里却躺着第 3 次，那么要么是排期那一边算错了账，要么是有人把两条链的
	// 事件混进了同一条链。两种都得当场喊出来，因为再往后就只剩下一次多余的请求，
	// 而多出来的那次请求是真的花了钱的。
	switch data.Mode {
	case llm.RetryNormal:
		if !data.HasMaxRetries {
			return fail("是 %s 档，却没写 maxRetries", llm.RetryNormal)
		}
		if data.MaxRetries < 1 {
			return fail("的 maxRetries 是 %d，至少要允许重 1 次", data.MaxRetries)
		}
		if data.Retry > data.MaxRetries {
			return fail("是第 %d 次重试，策略只允许 %d 次", data.Retry, data.MaxRetries)
		}
	case llm.RetryAlways:
		if data.HasMaxRetries {
			return fail("是 %s 档，不能带 maxRetries", llm.RetryAlways)
		}
	default:
		return fail("的 mode 只能是 %q 或者 %q，写的是 %q",
			llm.RetryNormal, llm.RetryAlways, data.Mode)
	}

	// 新增: DSH 那条查的是 `delayMs` 落在 0..MAX_TIMER_DELAY_MS 之间且是有限数。
	// 上界在 Go 这边整条去掉了，理由见 [github.com/snight1983/ds-harness-go/llm.ResolveRetryPolicy] 那段
	// 说明：MAX_TIMER_DELAY_MS 只是 JS setTimeout 把超过 32 位的延迟截成 1 毫秒
	// 这个实现缺陷的护栏，[time.Timer] 没有那个缺陷。「有限数」也一并去掉——
	// [time.Duration] 是整数，没有 NaN 和 Inf 这两种取值。剩下的就是负数这一条，
	// 而它仍然要查：一段负的等待意味着排期那一边算错了，而重放会当成「没等」。
	if data.Delay < 0 {
		return fail("要等的是 %v，不能是负的", data.Delay)
	}

	// 一条重试必须落在**开着的那个回合、开着的那个步骤**里。
	//
	// 源: packages/llm/llm-retry/src/invariant.ts（那两段 last-boundary 检查）
	//
	// 这两条守的是「这次重试到底替哪一次请求收拾残局」说得清。落在回合外的重试
	// 会在重放时被算进下一个回合的账，于是「这个步骤到底发过几次请求」这个问题
	// 有两个不一样的答案。
	if !t.turnOpen {
		return fail("追加在任何开着的回合之外")
	}
	if t.turn != data.Turn {
		return fail("说的是回合 %d，开着的却是回合 %d", data.Turn, t.turn)
	}
	if !t.stepBoundaryOpen {
		return fail("追加在任何开着的步骤之外")
	}
	if t.stepTurn != data.Turn || t.step != data.Step {
		return fail("说的是步骤 %d/%d，开着的却是步骤 %d/%d",
			data.Turn, data.Step, t.stepTurn, t.step)
	}

	// 提供方必须就是这个步骤当时路由到的那个。
	//
	// 源: packages/llm/llm-retry/src/invariant.ts（那句 providerForOpenStep 比较）
	//
	// 对不上的话，这条重试记的是**另一个提供方**的账：下一次失败会接着它往下数，
	// 于是一个从没失败过的提供方带着一串别人的重试次数，很快就被判成「重够了」。
	routed, present := t.routedProvider(data.Turn, data.Step)
	if !present {
		return fail("落在步骤 %d/%d 上，可那个步骤没有路由出去的提供方", data.Turn, data.Step)
	}
	if routed != data.Provider {
		return fail("报的提供方是 %s，那个步骤路由到的是 %s",
			strconv.Quote(data.Provider), strconv.Quote(routed))
	}

	// 序号必须接着这条链往下数，链的身份必须一路不变。
	//
	// 源: packages/llm/llm-retry/src/invariant.ts（那句 findLast + retryId 比较）
	key = chainKey{
		turn:      data.Turn,
		step:      data.Step,
		provider:  data.Provider,
		policyKey: data.PolicyKey,
	}
	prior, continues := t.chains[key]
	if data.Retry != prior.retry+1 {
		return fail("是第 %d 次重试，这条链上一次是第 %d 次", data.Retry, prior.retry)
	}
	if continues {
		if prior.retryID != data.RetryID {
			return fail("换了链身份：%s，这条链之前是 %s",
				strconv.Quote(string(data.RetryID)), strconv.Quote(string(prior.retryID)))
		}
	} else if _, taken := t.owners[data.RetryID]; taken {
		// 一条**新开**的链不许借用一个已经出现过的身份。借了的话，
		// llm/retry-started 那一边就再也认不出它配对的是哪一次排期——
		// 那条配对靠的正是 retryId 加序号。
		return fail("开了一条新链，身份 %s 却已经被用过", strconv.Quote(string(data.RetryID)))
	}

	state = chainState{retry: data.Retry, retryID: data.RetryID}
	return key, state, nil
}

// validateStarted 验一条 llm/retry-started。
//
// 源: packages/llm/llm-retry/src/invariant.ts:142-171
func (t *Trace) validateStarted(seq int, data RetryStartedData) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%w：seq %d 的 %s %s",
			ErrInvariantViolated, seq, EventRetryStarted, fmt.Sprintf(format, args...))
	}

	if data.RetryID == "" {
		return fail("没有链身份（retryId）")
	}

	// 必须找得到它配对的那次排期。
	//
	// 这两条事件分开写，为的就是把「排好期了」和「真的熬过去了」分开记
	// （见 [EventRetry] 那段说明）。一条找不到排期的 llm/retry-started 会让
	// 「这个步骤到底发过几次请求」多出一个凭空的答案。
	attempt := attemptKey{retryID: data.RetryID, retry: data.Retry}
	ref, scheduled := t.scheduled[attempt]
	if !scheduled {
		return fail("说的是链 %s 上的第 %d 次，可没有配对的 %s",
			strconv.Quote(string(data.RetryID)), data.Retry, EventRetry)
	}
	if ref.turn != data.Turn || ref.step != data.Step {
		return fail("说的是步骤 %d/%d，配对的 %s 排在步骤 %d/%d 上",
			data.Turn, data.Step, EventRetry, ref.turn, ref.step)
	}
	if _, already := t.started[attempt]; already {
		return fail("是链 %s 上第 %d 次的第二条——那段等待只熬得过去一次",
			strconv.Quote(string(data.RetryID)), data.Retry)
	}
	return nil
}

// validateFailure 验一条重试事件里那份规整过的失败。
//
// 源: packages/llm/llm-retry/src/invariant.ts（validateFailure）
//
// 新增: DSH 那边还查 `status`/`providerRetryAfterMs`/`requestId` 在**出现时**的
// 形状。[llm.Failure] 那三个字段用零值表达缺席（0/0/空串），所以：
//   - requestId 那条整条去掉——「出现了但是空串」在 Go 里就是「没出现」，那个分支
//     永远走不到，留着只会变成一条从来没验过的死代码。
//   - status 收窄成「非 0 时必须落在 100..599」。
//   - providerRetryAfterMs 收窄成「不能是负的」。DSH 查的是「出现时必须为正」，
//     而 0 在 Go 这边正是缺席本身。
func validateFailure(seq int, kind session.EventType, failure llm.Failure) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%w：seq %d 的 %s 那份失败%s",
			ErrInvariantViolated, seq, kind, fmt.Sprintf(format, args...))
	}
	if failure.Message == "" {
		return fail("没有描述（message）")
	}
	if failure.Code == "" {
		return fail("没有失败码（code）")
	}
	if failure.Status != 0 && (failure.Status < 100 || failure.Status > 599) {
		return fail("报的状态码是 %d，不是一个 HTTP 状态码", failure.Status)
	}
	if failure.ProviderRetryAfterMs < 0 {
		return fail("报的 providerRetryAfterMs 是 %d，不能是负的", failure.ProviderRetryAfterMs)
	}
	return nil
}

// Apply 把一条已经验过、也已经提交了的事件算进这条轨迹。
//
// 源: packages/llm/llm-retry/src/invariant.ts:52-56
func (t *Trace) Apply(_ session.Event, transition Transition) {
	t.applyRoute(transition.route)
	switch transition.kind {
	case transitionRetry:
		t.chains[transition.chain] = transition.state
		t.owners[transition.attempt.retryID] = struct{}{}
		t.scheduled[transition.attempt] = transition.ref
	case transitionStarted:
		t.owners[transition.attempt.retryID] = struct{}{}
		t.started[transition.attempt] = struct{}{}
	}
}

// ValidateLog 把一整段日志走一遍，交出走完之后的轨迹或者第一条违例。
//
// 源: packages/llm/llm-retry/src/invariant.ts（install 里那段 seed）
func ValidateLog(events []session.Event) (*Trace, error) {
	trace := NewTrace()
	for _, event := range events {
		transition, err := trace.Validate(event)
		if err != nil {
			return nil, err
		}
		trace.Apply(event, transition)
	}
	return trace, nil
}

// RegisterInvariants 装上重试事件那几条检查，返回注销函数。
//
// 源: packages/llm/llm-retry/src/invariant.ts:173-174
//
// 两条胳膊，和 DSH 一样：装的时候把**已经装进来的**日志走一遍（一份历史里就带着
// 拆了对的重试的会话，必须在装载这一刻就响），然后订阅后续的追加。
//
// 新增: DSH 那两条胳膊都从 cordis 上拿——ctx.sessions.list() 取历史，
// ctx.on('internal/dispatch') 截住后来的。Go 里活会话服务是循环那一块的东西，
// 本包在它下面，所以这两条胳膊由装配方以函数交进来，做法和
// [github.com/snight1983/ds-harness-go/interaction/userapproval.RegisterInvariants] 逐字相同。
func RegisterInvariants(
	ctx context.Context,
	registry *invariants.Registry,
	loaded func() []session.Event,
	subscribe func(observer func(session.Event)) func(),
) (func(), error) {
	if registry == nil {
		return nil, fmt.Errorf("%w：注册不变量需要一个不变量注册表", ErrInvalidConfig)
	}
	if loaded == nil {
		return nil, fmt.Errorf("%w：注册不变量需要一条读出已装载日志的路", ErrInvalidConfig)
	}
	if subscribe == nil {
		return nil, fmt.Errorf("%w：注册不变量需要一条订阅后续事件的路", ErrInvalidConfig)
	}

	install := func(_ context.Context, scope *invariants.Scope, fail invariants.Fail) error {
		trace := NewTrace()
		check := func(event session.Event) {
			transition, err := trace.Validate(event)
			if err != nil {
				fail(err.Error())
				return
			}
			trace.Apply(event, transition)
		}
		for _, event := range loaded() {
			check(event)
		}
		scope.Defer(subscribe(check))
		return nil
	}

	return registry.Register(ctx, PackageName, install)
}
