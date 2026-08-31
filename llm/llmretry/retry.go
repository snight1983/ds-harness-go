// 本文件的作用：把「一次模型请求失败之后要不要再试一次、等多久」这件事装到 agent
// 的失败恢复瀑布上，并把排期和熬过去这两件事分别写进会话日志。
//
// 源: packages/llm/llm-retry/src/index.ts
//
// 这个包只认 [ds-harness-go/core/agent.Registry] 一个宿主：策略是适配器在登记路由
// 那一刻定下来的（[llm.ResolveRetryPolicy]），本包只负责执行它。
package llmretry

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"ds-harness-go/core/agent"
	"ds-harness-go/core/scope"
	"ds-harness-go/llm"
	"ds-harness-go/session"
)

// Options 是装这个包时要给的东西。
//
// 源: packages/llm/llm-retry/src/index.ts:24-41
//
// 新增: DSH 那个 Config 是 `Readonly<Record<string, never>>`——一个**只能是空对象**
// 的配置，配上一个 validateConfig 专门拒掉误写进来的 retryPolicy 键（那份策略属于
// 提供方路由，不属于这个插件）。Go 的结构体写不出表外的字段，那一整套连同它的错误
// 消息一起不需要，理由和 [llm.ResolveRetryPolicy] 上那条 validateKeys 逐字相同。
// 剩下的都是 DSH 的 RetryInternals（测试用的注入口）和 Go 这边必须显式交进来的宿主。
type Options struct {
	// Agents 是要挂上去的那个 agent 注册表，必填。
	Agents *agent.Registry
	// Owner 是这次登记的所有者作用域，必填。
	//
	// 新增: DSH 那边观察者的寿命跟着 cordis 的插件作用域走，写插件的人不用交。
	// Go 里没有那个隐式容器，所以由装配方明说这次登记归谁管——它释放的时候，
	// 观察者跟着摘掉。
	Owner *scope.Scope

	// Random 交出 [0,1) 里的一个数，给抖动用；nil 取 [math/rand/v2.Float64]。
	//
	// 源: packages/llm/llm-retry/src/index.ts:39-41（RetryInternals）
	//
	// 它存在只为一件事：让测试能钉住排出来的那段延时。真跑起来没人会给它。
	Random func() float64
	// NewID 发一条新重试链的身份；nil 取 [github.com/google/uuid.NewString]。
	//
	// 新增: DSH 直接调 randomUUID()。抽成一个口子的理由和
	// [ds-harness-go/interaction/userapproval] 上那条一样——不变量要验「链身份一路
	// 不变」，测试得排得出一串认得出来的身份。
	NewID func() string
	// Logger 是诊断日志；nil 取 [log/slog.Default]。
	Logger *slog.Logger
}

// installation 是一次装配的活的那部分。
//
// 源: packages/llm/llm-retry/src/index.ts:99-226
type installation struct {
	random func() float64
	newID  func() string
	logger *slog.Logger

	// lifetime 在拆除时取消，用来打断所有还在等的那几段退避。
	//
	// 源: packages/llm/llm-retry/src/index.ts（那个 lifetime AbortController）
	lifetime context.Context
	stop     context.CancelFunc

	// mutex 护着 disposed，并且把它和 active.Add 锁在一起。
	//
	// 新增: DSH 用一个 Set<Promise> 记在跑的恢复，拆除时 Promise.allSettled 等干净。
	// Go 这边换成 [sync.WaitGroup]，但**不能**在观察者里裸调 Add——那会和拆除那一侧
	// 的 Wait 撞成一次真正的数据竞争（WaitGroup 不许 Wait 期间从零往上加）。
	// 所以进出口都走 [installation.enter]，它在同一把锁下先看拆没拆、再 Add。
	mutex    sync.Mutex
	disposed bool
	active   sync.WaitGroup
}

// enter 认领一次恢复；已经拆过了就返回假。
func (i *installation) enter() bool {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if i.disposed {
		return false
	}
	i.active.Add(1)
	return true
}

// Install 把重试装到 agent 的请求失败瀑布上，返回拆除函数。
//
// 源: packages/llm/llm-retry/src/index.ts:99-226
//
// 拆除按 DSH 的顺序来：先摘掉观察者（不再接新的失败），再取消 lifetime（打断所有
// 还在等的退避），最后等在跑的那几次恢复自己收尾。反过来的话，一次刚熬过退避的重试
// 会在观察者已经被摘掉之后才去追加它那条 llm/retry-started。
func Install(ctx context.Context, options Options) (func(context.Context) error, error) {
	if options.Agents == nil {
		return nil, fmt.Errorf("%w：装重试需要一个 agent 注册表", ErrInvalidConfig)
	}
	// 在这里拒，而不是等作用域那一层报「宿主作用域不能是 nil」：那句话说的是
	// scope 包内部的规矩，看到它的人得先弄明白这次登记的宿主是从哪儿来的。
	if options.Owner == nil {
		return nil, fmt.Errorf("%w：装重试需要一个所有者作用域", ErrInvalidConfig)
	}

	install := &installation{
		random: options.Random,
		newID:  options.NewID,
		logger: options.Logger,
	}
	if install.random == nil {
		install.random = rand.Float64
	}
	if install.newID == nil {
		install.newID = uuid.NewString
	}
	if install.logger == nil {
		install.logger = slog.Default()
	}
	// 这条命脉不挂在 ctx 上：ctx 是**装配**那一刻的上下文，它结束了不代表这次装配
	// 该收摊。命脉只由拆除函数掐断。
	install.lifetime, install.stop = context.WithCancel(context.Background())

	detach, err := options.Agents.OnRequestError(ctx, options.Owner, install.observe)
	if err != nil {
		install.stop()
		return nil, fmt.Errorf("%w：挂请求失败观察者失败：%w", ErrInvalidConfig, err)
	}

	var once sync.Once
	return func(ctx context.Context) error {
		var detachErr error
		once.Do(func() {
			detachErr = detach(ctx)
			install.mutex.Lock()
			install.disposed = true
			install.mutex.Unlock()
			install.stop()
			install.active.Wait()
		})
		return detachErr
	}, nil
}

// observe 是挂在瀑布上的那个观察者。
//
// 源: packages/llm/llm-retry/src/index.ts（那句 ctx.on('agent/request-error', ...)）
func (i *installation) observe(
	ctx context.Context,
	failure agent.RequestFailure,
	next func(context.Context) (agent.RequestErrorAction, error),
) (agent.RequestErrorAction, error) {
	if !i.enter() {
		// 源: packages/llm/llm-retry/src/index.ts（那句 if (lifetime.signal.aborted) 提前返回）
		//
		// 逐字跟着 DSH：这里**不**往下传，直接交出终局。看着像是把别的观察者的机会
		// 也一起掐了，但这条分支的窗口只有「观察者已经被摘掉、这次调用却已经进了门」
		// 那一瞬——摘观察者是拆除的第一步，见 [Install]。
		return agent.RequestErrorAction{}, nil
	}
	defer i.active.Done()
	return i.recover(ctx, failure.Agent.Session(), failure, next)
}

// recover 决定这次失败要不要重试，要的话排一次退避。
//
// 源: packages/llm/llm-retry/src/index.ts（recover）
//
// 新增: 那个会话由调用方交进来，而不是在这里从 failure.Agent 上取。DSH 那边
// agent.session 是同一个对象上的一个属性，取它不花什么；Go 里 [agent.Agent] 是一个
// 十几个方法的接口，要在测试里给出一个只为了拿会话的实现，得把那十几个方法全写一遍。
func (i *installation) recover(
	ctx context.Context,
	live sessionAppender,
	failure agent.RequestFailure,
	next func(context.Context) (agent.RequestErrorAction, error),
) (agent.RequestErrorAction, error) {
	if !failure.HasRetryPolicy {
		return next(ctx)
	}
	policy := failure.RetryPolicy

	switch policy.Mode {
	case llm.RetryAlways:
		// always 档**先**让下游有机会认领。
		//
		// 源: packages/llm/llm-retry/src/index.ts（那段 settleDownstream(next)）
		//
		// 顺序反过来的话，always 就成了一堵墙：任何排在它后面的、更懂这次失败的
		// 观察者（比如换个提供方重来）永远轮不到。
		action, err := next(ctx)
		if err != nil {
			// 下游炸了不算这次重试的事：always 的意思就是「不管怎么失败都再试一次」。
			// 咽下去但要留声，不然一个一直在抛的下游观察者会被这一档完全盖住。
			//
			// 新增: DSH 那边是 settleDownstream 把 reject 收成一个结果、再 ctx.logger.warn。
			// Go 里 next 返回的就是 (值, 错误)，那个结果类型不需要。
			i.logger.Warn("llmretry: always 档忽略了下游的一次恢复失败",
				"provider", failure.Provider, "err", err)
		} else if action.Retry {
			return action, nil
		}
	case llm.RetryNormal:
		if !slices.Contains(policy.RetryableCodes, failure.Failure.Code) {
			return next(ctx)
		}
	default:
		// 解算出来的策略只可能是那两档（[llm.ResolveRetryPolicy] 拒掉别的），
		// 走到这里说明这份策略不是解算出来的。当成「不认」往下传，而不是猜一档。
		return next(ctx)
	}

	policyKey := retryPolicyKey(policy)
	prior, continues, err := lastChainRetry(
		live.Events(), failure.Turn, failure.Step, failure.Provider, policyKey)
	if err != nil {
		// 一条读不回来的 llm/retry 意味着「这条链已经重了几次」没有答案。
		// 咽下去当成零次的话，一份坏日志会换来一串无上限的重试，而每一次都是真花钱的。
		return agent.RequestErrorAction{}, err
	}

	previousRetry := 0
	retryID := RetryID(i.newID())
	if continues {
		previousRetry = prior.Retry
		retryID = prior.RetryID
	}
	// 源: packages/llm/llm-retry/src/index.ts（那句 previousRetry >= policy.maxRetries）
	//
	// 重够了就往下传，而不是交终局：别的观察者也许还有办法。
	if policy.Mode == llm.RetryNormal && previousRetry >= policy.MaxRetries {
		return next(ctx)
	}
	retry := previousRetry + 1

	delay, ok := i.retryDelay(policy, failure.Failure, retry)
	if !ok {
		// 只有 normal 档会走到这里：提供方点名要等的那段比策略的上限还长。
		// 见 [installation.retryDelay]。
		return next(ctx)
	}

	return i.backoff(ctx, live, failure, policy, policyKey, retryID, retry, delay)
}

// retryDelay 定这次重试要等多久；第二个返回值为假表示这次重试该作罢。
//
// 源: packages/llm/llm-retry/src/index.ts（那段 providerRetryAfterMs 分支）
//
// 提供方自己点了名要等多久（HTTP 的 Retry-After 之类）就听它的——它比本地那条曲线
// 更知道自己什么时候缓过来。但它可能点一个离谱的数：超过策略上限时，normal 档作罢
// （那份策略写明了「最多等这么久」，等更久等于替用户改了他的配置），always 档退回
// 本地退避（这一档的承诺是「一直重试」，作罢会把那句承诺废掉）。
func (i *installation) retryDelay(
	policy llm.ResolvedRetryPolicy, failure llm.Failure, retry int,
) (time.Duration, bool) {
	if failure.ProviderRetryAfterMs <= 0 {
		return localDelay(policy, retry, i.random), true
	}
	requested := time.Duration(failure.ProviderRetryAfterMs) * time.Millisecond
	if requested <= policy.MaxDelay {
		return requested, true
	}
	if policy.Mode == llm.RetryNormal {
		return 0, false
	}
	return localDelay(policy, retry, i.random), true
}

// backoff 写下排期、等过那段退避、再写下「熬过去了」。
//
// 源: packages/llm/llm-retry/src/index.ts（backoff）
//
// 两条事件之间隔着一段可以被打断的等待，这正是它们要分成两条的原因，
// 见 [EventRetry] 上那段说明。
func (i *installation) backoff(
	ctx context.Context,
	live sessionAppender,
	failure agent.RequestFailure,
	policy llm.ResolvedRetryPolicy,
	policyKey string,
	retryID RetryID,
	retry int,
	delay time.Duration,
) (agent.RequestErrorAction, error) {
	fused, release := fuse(ctx, i.lifetime)
	defer release()
	if fused.Err() != nil {
		// 已经被取消了就一条事件都不写：写下一条谁也不会去熬的排期，等于在日志里
		// 留下一次没发生过的重试。
		return agent.RequestErrorAction{}, nil
	}

	data := RetryData{
		RetryID:       retryID,
		Turn:          failure.Turn,
		Step:          failure.Step,
		Provider:      failure.Provider,
		Mode:          policy.Mode,
		PolicyKey:     policyKey,
		Retry:         retry,
		MaxRetries:    policy.MaxRetries,
		HasMaxRetries: policy.Mode == llm.RetryNormal,
		Delay:         delay,
		Failure:       failure.Failure,
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return agent.RequestErrorAction{}, err
	}
	if _, err := live.Append(session.Event{Type: EventRetry, Data: payload}); err != nil {
		return agent.RequestErrorAction{}, err
	}

	if !cancellableDelay(fused, delay) {
		// 等到一半被打断：不写 llm/retry-started，因为那次请求确实没有发出去。
		// 交终局而不是往下传——这次失败已经被本包认领了，只是认领的结果是「不重了」。
		return agent.RequestErrorAction{}, nil
	}

	started, err := json.Marshal(RetryStartedData{
		RetryID: retryID,
		Turn:    failure.Turn,
		Step:    failure.Step,
		Retry:   retry,
	})
	if err != nil {
		return agent.RequestErrorAction{}, err
	}
	if _, err := live.Append(session.Event{Type: EventRetryStarted, Data: started}); err != nil {
		return agent.RequestErrorAction{}, err
	}
	return agent.RequestErrorAction{Retry: true}, nil
}

// sessionAppender 是本包用得着的那一小片会话接口。
//
// 新增: DSH 那边 agent.session 就是那个具体的类。Go 里收窄成这两个方法，
// 是为了让 [installation.backoff] 的测试不必抬起一整个会话存储。
type sessionAppender interface {
	Events() []session.Event
	Append(candidate session.Event) (session.Event, error)
}

// lastChainRetry 从日志里翻出这条链上最后那次重试。
//
// 源: packages/llm/llm-retry/src/index.ts（那句 events.findLast）
//
// 四个比较项缺一不可，尤其是 policyKey：换了策略就换一条链，见 [chainKey]。
func lastChainRetry(
	events []session.Event, turn, step int, provider, policyKey string,
) (RetryData, bool, error) {
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if event.Type != EventRetry {
			continue
		}
		data, err := DecodeRetry(event)
		if err != nil {
			return RetryData{}, false, err
		}
		if data.Turn == turn && data.Step == step &&
			data.Provider == provider && data.PolicyKey == policyKey {
			return data, true, nil
		}
	}
	return RetryData{}, false, nil
}

// localDelay 算本地那条有界指数退避加抖动的延时。
//
// 源: packages/llm/llm-retry/src/index.ts（localDelay）
//
// 指数封在 1024 上，是 DSH 为了不让 2**retry 直接变成 Infinity 加的护栏；
// Go 这边 math.Pow(2, 1024) 同样是 +Inf，而 InitialDelay 保证是正数
// （[llm.ResolveRetryPolicy]），所以那个 Inf 会被下一行的上限截回 MaxDelay，
// 不会变成 NaN。护栏照留，两边的曲线才逐点一样。
func localDelay(policy llm.ResolvedRetryPolicy, retry int, random func() float64) time.Duration {
	exponent := min(retry-1, 1024)
	exponential := min(
		float64(policy.InitialDelay)*math.Pow(2, float64(exponent)),
		float64(policy.MaxDelay))
	jitter := 1 - policy.JitterRatio + 2*policy.JitterRatio*random()
	return time.Duration(min(exponential*jitter, float64(policy.MaxDelay)))
}

// retryPolicyKey 给一份解算完的策略按上指纹。
//
// 源: packages/llm/llm-retry/src/index.ts（retryPolicyKey）
//
// 它进 [EventRetry] 的负载、也进 [chainKey]，为的是让「策略在两次失败之间被换掉了」
// 在日志里看得出来。always 档只按退避那三项算，因为 normal 那两个字段这一档根本不读
// （[llm.ResolvedRetryPolicy] 上写明了这一点），把它们算进去会让一次无关的配置改动
// 白白斩断一条链。
//
// 新增: 延时按毫秒进指纹，跟着 DSH 的字段单位走，理由同 [retryWire].DelayMs。
// 失败码排过序再算，所以调换清单顺序不换指纹——那两份策略行为完全一样。
func retryPolicyKey(policy llm.ResolvedRetryPolicy) string {
	initial := float64(policy.InitialDelay) / float64(time.Millisecond)
	maximum := float64(policy.MaxDelay) / float64(time.Millisecond)

	var parts []any
	if policy.Mode == llm.RetryAlways {
		parts = []any{policy.Mode, initial, maximum, policy.JitterRatio}
	} else {
		codes := slices.Clone(policy.RetryableCodes)
		slices.Sort(codes)
		parts = []any{policy.Mode, policy.MaxRetries, codes, initial, maximum, policy.JitterRatio}
	}
	// 这几种取值排不出去是不可能的：全是字符串、整数，以及已经验过不是 NaN 的浮点数。
	encoded, _ := json.Marshal(parts)
	return string(encoded)
}

// cancellableDelay 等过一段延时，中途被取消就返回假。
//
// 源: packages/llm/llm-retry/src/index.ts（cancellableDelay）
func cancellableDelay(ctx context.Context, delay time.Duration) bool {
	// 零延时单独判：让一个立刻就绪的定时器和一个已经取消的上下文一起进 select，
	// Go 会**随机**挑一个，于是「已经取消了还照样重试」会偶发。
	if delay <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// fuse 把请求自己的上下文和本包的命脉并成一个：任一边断了，交出来的这个就断。
//
// 新增: DSH 那边是 AbortSignal.any([signal, lifetime.signal])。Go 的标准库没有这个，
// 但 [context.AfterFunc] 够用了。释放函数必须调（defer 即可）——不调的话那个
// AfterFunc 会一直挂在命脉上，直到整次装配拆除为止，每一次重试漏一个。
func fuse(request, lifetime context.Context) (context.Context, context.CancelFunc) {
	fused, cancel := context.WithCancelCause(request)
	stop := context.AfterFunc(lifetime, func() { cancel(context.Cause(lifetime)) })
	return fused, func() {
		stop()
		cancel(context.Canceled)
	}
}
