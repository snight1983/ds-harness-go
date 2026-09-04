// 本文件的作用：这台循环对它造出来的 Agent 的所有权——还活着没有、在飞的启动
// 怎么等、收摊时按什么次序释放，以及一场等待怎么被中止叫醒。
//
// 源: packages/core/agent-loop/src/index.ts:1-713

package agentloop

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/snight1983/ds-harness-go/sessionlog"
)

// ---- 配置启动失败的通报 ----

// ConfigStartFailedObserver 收一次「一个声明式的 agent 项在公布出活 agent 之前就失败了」。
//
// 源: packages/core/agent-loop/src/index.ts:170-179
//
// 那些为这个身份缓存活儿的消费方拿这个瞬时信号去拒掉那些活儿，而不是永远等下去。
// 工厂正常拆除时被取消掉的那次启动**不**通报。
//
// 新增: DSH 是 cordis 的一条 `emit` 事件。Go 里按本仓库一贯的规矩换成显式的
// 观察者登记，见 [AgentLoop.OnConfigStartFailed]。
type ConfigStartFailedObserver func(sessionID sessionlog.SessionID, err error)

// ---- 工厂级归属 ----

// factoryOwnership 是工厂这一级的归属：活着的那些 agent 的拆除，加上配置驱动的启动活儿。
//
// 源: packages/core/agent-loop/src/index.ts:95-146（FactoryOwnership）
//
// 新增: DSH 那个类里有三样东西——一个 accepting 标志、一个 AbortController（拆除
// 开始时以 `agent loop is not active` 中止）、以及一个 `Promise.withResolvers<void>`
// （waitWhileActive 用来在拆除开始时停止等待）。Go 里后两样合成**一个**
// [context.WithCancelCause]：Done() 顶那个 promise，Cause() 顶那个中止原因。
// DSH 需要两个对象，是因为 AbortSignal 的 reason 和一个 resolve 掉的 promise
// 在 JS 里是两种东西。
//
// 新增: DSH 还查 `!INACTIVE_STATES.has(this.fiber.state)`——那是 cordis 的纤程状态。
// Go 里没有纤程，「这个工厂还接不接活」完全由 accepting 这一位说了算，
// 而它由拥有这个工厂的那个作用域在拆除时翻掉。
type factoryOwnership struct {
	mutex     sync.Mutex
	accepting bool
	// liveAgents 里是那些活着的 agent 各自那份**共享的**拆除函数。
	// 用 map 而不是切片：一份拆除跑完之后要按身份摘掉，见 track 交出来的 untrack。
	liveAgents map[*agentTeardown]struct{}

	teardown context.Context
	cancel   context.CancelCauseFunc

	// startup 等的是那些在任何 agent 存在之前就开跑的配置启动活儿。
	startup sync.WaitGroup
}

// agentTeardown 是一份活 agent 的共享拆除，取地址当身份用。
type agentTeardown struct {
	dispose func(context.Context) error
}

// errLoopNotActive 是工厂拆除时盖到那个归属上下文上的原因。
//
// 源: packages/core/agent-loop/src/index.ts:82
var errLoopNotActive = errors.New("agent loop is not active")

// newFactoryOwnership 造一份工厂归属。
func newFactoryOwnership() *factoryOwnership {
	teardown, cancel := context.WithCancelCause(context.Background())
	return &factoryOwnership{
		accepting:  true,
		liveAgents: make(map[*agentTeardown]struct{}),
		teardown:   teardown,
		cancel:     cancel,
	}
}

// isActive 判这个工厂还接不接新的生命周期。
//
// 源: packages/core/agent-loop/src/index.ts:55-57
func (o *factoryOwnership) isActive() bool {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	return o.accepting
}

// track 记下一个活 agent 那份共享的拆除，直到它跑过为止；返回摘掉这一份的函数。
//
// 源: packages/core/agent-loop/src/index.ts:59-63
func (o *factoryOwnership) track(dispose func(context.Context) error) func() {
	handle := &agentTeardown{dispose: dispose}

	o.mutex.Lock()
	o.liveAgents[handle] = struct{}{}
	o.mutex.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			o.mutex.Lock()
			delete(o.liveAgents, handle)
			o.mutex.Unlock()
		})
	}
}

// trackStartup 把一件在任何 agent 存在之前就开跑的配置启动活儿并进来。
//
// 源: packages/core/agent-loop/src/index.ts:65-70
//
// 新增: DSH 那边收的是一个 Promise，并在它落定时从集合里摘掉自己。Go 里同一件事
// 就是 [sync.WaitGroup]——本包只需要「拆除时等它们跑完」，不需要枚举它们。
func (o *factoryOwnership) trackStartup(job func()) {
	o.startup.Add(1)
	go func() {
		defer o.startup.Done()
		job()
	}()
}

// waitWhileActive 等这件活儿跑完，或者在工厂拆除开始时不再等。
//
// 源: packages/core/agent-loop/src/index.ts:77-79
func (o *factoryOwnership) waitWhileActive(done <-chan struct{}) {
	select {
	case <-done:
	case <-o.teardown.Done():
	}
}

// dispose 停掉这个工厂：不再接活、把归属上下文取消掉、把每一个活 agent 拆掉、
// 等所有启动活儿落定。
//
// 源: packages/core/agent-loop/src/index.ts:81-89
func (o *factoryOwnership) dispose(ctx context.Context) error {
	o.mutex.Lock()
	o.accepting = false
	pending := make([]*agentTeardown, 0, len(o.liveAgents))
	for handle := range o.liveAgents {
		pending = append(pending, handle)
	}
	o.mutex.Unlock()

	o.cancel(errLoopNotActive)

	// 每个 agent 的拆除各自跑到底，一份失败不能把其余的落下——它们各自持有的
	// 注册表登记必须都摘掉，否则那些身份永远占着。
	var failures []error
	for _, handle := range pending {
		if err := handle.dispose(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	o.startup.Wait()
	return errors.Join(failures...)
}

// ---- 竞速 ----

// raceAbort 跑一件可能拖很久的活儿，并在 ctx 取消时立刻不再等它。
//
// 源: packages/core/agent-loop/src/index.ts:92-130（raceAbort 与 raceAbortCall）
//
// release 非 nil 时，一份在取消之后才到的结果会交给它——DSH 那个
// releaseAbandoned 参数说的是同一件事：一个「已经没人要了」的资源必须有人收，
// 否则它就是一次静默的泄漏。
//
// 新增: DSH 把 raceAbort（等一个已经在跑的 promise）和 raceAbortCall（起一件活儿
// 再等）分成两个函数，因为 JS 里「起」和「等」天然分开。Go 里起一件并发的活儿
// 必然要开一个 goroutine，两者合成一个函数反而更少一处走样。
func raceAbort[T any](
	ctx context.Context,
	id sessionlog.SessionID,
	run func() (T, error),
	release func(T),
) (T, error) {
	var zero T
	if err := abortCause(ctx, id); err != nil {
		return zero, err
	}

	type outcome struct {
		value T
		err   error
	}
	// 缓冲一格：竞速输掉之后这个 goroutine 照样要能把结果放下并退出，
	// 否则它会一直挂在发送上，泄漏一个 goroutine 外加它扣着的那份资源。
	results := make(chan outcome, 1)
	go func() {
		value, err := run()
		results <- outcome{value: value, err: err}
	}()

	select {
	case done := <-results:
		return done.value, done.err
	case <-ctx.Done():
		if release != nil {
			go func() {
				done := <-results
				if done.err == nil {
					release(done.value)
				}
			}()
		}
		return zero, abortCause(ctx, id)
	}
}

// abortCause 把一个已经取消的上下文翻译成这条路上该抛的那个错误；没取消时返回 nil。
//
// 源: packages/core/agent-loop/src/index.ts:94-97
//
// DSH 那段是「reason 本来就是 Error 就原样抛，否则包一层 `agent "<id>" creation
// aborted`」。Go 里 [context.Cause] 交出来的一定是 error，所以只剩下「有没有一个
// 比 context.Canceled 更有信息量的原因」这一层判断。
func abortCause(ctx context.Context, id sessionlog.SessionID) error {
	if ctx.Err() == nil {
		return nil
	}
	cause := context.Cause(ctx)
	if cause == nil || errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return fmt.Errorf("agent %q creation aborted: %w", string(id), cause)
	}
	return cause
}
