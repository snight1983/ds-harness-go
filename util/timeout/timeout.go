// Package timeout 提供超时的**计时与归类**，不负责终止。
//
// 对应 DSH 的 @deepseek-ai/dsh-timeout（packages/util/timeout）。
//
// 它要解决的问题是：一次调用被取消了，调用方需要知道**是谁取消的**——是用户按了停止，
// 还是这一层自己的超时到了，还是外层某个更早的超时到了。这三种情况的处理完全不同
// （用户取消不该重试、自己超时该报自己的超时码、外层超时该原样往上传），
// 而如果只有一个「已取消」的布尔值，它们就分不开了。
//
// 所以这个包只做两件事：把一个可归因的超时**挂**到取消上（Deadline / Watchdog），
// 以及把它**认**回来（Of / OfContext）。真正停下手上的活是各个能力自己的事。
//
// # AbortSignal 在 Go 里对应什么
//
// DSH 用 AbortSignal + AbortSignal.any 融合上游取消和本层超时，靠 signal.reason
// 保留「谁先取消的」。Go 的 context.WithCancelCause / context.Cause 是同一件事的
// 原生形态，而且更强：cause 会沿父子链自动传下来，不需要手工 any 融合。
//
//   - AbortSignal            → context.Context
//   - AbortSignal.any([a,b]) → context.WithCancelCause(parent)（父子关系天然就是融合）
//   - signal.reason          → context.Cause(ctx)
//   - 「谁先取消谁定 reason」 → cancel 只有第一次生效，语义完全一致
//
// # 一处刻意的差异：清理函数会取消 context
//
// DSH 的 dispose 只清定时器、**不**触发 abort（tests/timeout.spec.ts:69-76 钉的就是这个）。
// 一个再也没人看的 AbortSignal 被 GC 掉就完了，不会泄漏任何东西。
//
// Go 不是这样：一个 context.WithCancelCause 造出来的子 context 如果不 cancel，
// 它会一直挂在父 context 的子节点表上直到父结束——这是 go vet 的 lostcancel
// 要拦的经典泄漏。所以这里的清理函数是 `timer.Stop() + cancel(context.Canceled)`。
//
// 可观察的归类行为没有变：清理之后 Of 返回 nil，也就是「不是超时」，
// 和 DSH 那条测试断言的 timeoutOf(d.signal) === undefined 一致。
// 变的只是 ctx.Done() 会闭合——而在 Go 里，作用域退出时 defer cancel() 本来就是对的。
package timeout

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Reason 是一次可归因的超时：它带着**是哪个能力的哪条超时**。
//
// 源: packages/util/timeout/src/index.ts:12-22
//
// Code 由能力自己定（BASH_TIMEOUT、LLM_STREAM_IDLE_TIMEOUT 之类），它是嵌套超时能
// 被分辨开的唯一依据：外层超时传到内层时，内层拿 Of(err, 自己的码) 会得到 nil，
// 于是正确地把它当成「上游取消」而不是「我超时了」。
type Reason struct {
	// Code 是能力自己拥有的超时代号。
	Code string
	// After 是已经过去的那个期限。
	After time.Duration
}

// Error 的文案沿用 DSH 的 `<CODE> after <N>ms`。
//
// 保留毫秒而不是用 Go 的 Duration.String()（会打印 100ms / 1m40s），
// 是因为这串文字会进日志、被人和 DSH 侧的日志对着看，换了格式就对不上了。
func (r *Reason) Error() string {
	return fmt.Sprintf("%s after %dms", r.Code, r.After.Milliseconds())
}

// 看门狗的两种误用。做成哨兵值是为了让调用方能用 errors.Is 判定。
var (
	// ErrWatchdogStopped 表示看门狗已经停了，不该再拿它守新的等待。
	ErrWatchdogStopped = errors.New("timeout: 看门狗已经停止")
	// ErrDemandOutstanding 表示上一次等待还没结束就又发起了一次。
	// 源: packages/util/timeout/src/index.ts:151
	ErrDemandOutstanding = errors.New("timeout: 上一次等待还没结束")
)

// Clamp 把调用方给的超时提示夹到后端允许的范围里，返回真正生效的值。
//
// 源: packages/util/timeout/src/index.ts:45-55
//
// 结果是 min(requested ?? def, max)。注意 max 对**默认值本身**也生效——
// 一个配错了的后端不会因为调用方没传就越过自己的上限
// （tests/timeout.spec.ts:35-39 专门钉了这条）。
//
// requested 用指针表示「没给」。零不是「关掉超时」的公开写法：
// 那个含义只在本包内部由 Deadline 的 timeout <= 0 承担，不对外暴露，
// 免得调用方以为传 0 就能把超时关了。
//
// name 会出现在报错里，这样调用方看到的是「bash-local: request.timeoutMs 必须是正数」
// 而不是一句不知道在说哪个字段的「参数非法」。
func Clamp(requested *time.Duration, def, max time.Duration, name string) (time.Duration, error) {
	if requested != nil && *requested <= 0 {
		return 0, fmt.Errorf("timeout: %s 必须是正数，收到 %v", name, *requested)
	}
	effective := def
	if requested != nil {
		effective = *requested
	}
	return min(effective, max), nil
}

// Deadline 在 parent 之下挂一条可归因的超时，返回新的 context 和清理函数。
//
// 源: packages/util/timeout/src/index.ts:91-113
//
// timeout <= 0 是**内部**的「不计时」哨兵：此时原样返回 parent 和一个空操作的清理
// 函数，一个定时器都不装。返回 parent 本身而不是它的子 context，是为了让清理函数
// 真的能是空操作——如果造了子 context 又不 cancel，那就是泄漏；而如果 cancel 了，
// 就等于替调用方把 parent 的作用域结束了。
//
// 有超时时，谁先到谁定 cause：超时先到，cause 是 *Reason；上游先取消，
// cause 是上游的原因，而且会沿父子链原样传下来。这正是 DSH 靠 AbortSignal.any
// 「保留第一个原因」拿到的东西（tests/timeout.spec.ts:95-126），
// 在 Go 里是 context 本来就有的性质。
//
// 清理函数按 Go 的惯例 defer 调用，见本包头部关于「清理会取消 context」的说明。
func Deadline(parent context.Context, timeout time.Duration, code string) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return parent, func() {}
	}

	ctx, cancel := context.WithCancelCause(parent)
	timer := time.AfterFunc(timeout, func() {
		cancel(&Reason{Code: code, After: timeout})
	})
	return ctx, func() {
		timer.Stop()
		cancel(context.Canceled)
	}
}

// Of 从一个错误里把超时原因认出来。
//
// 源: packages/util/timeout/src/index.ts:184-190
//
// code 非空时只认这个代号的超时，空串表示不挑。这个区分是嵌套超时能被正确处理的
// 关键：内层拿自己的码去问，外层超时会返回 nil，于是走「上游取消」那条路
// （tests/timeout.spec.ts:186-197）。
//
// 用 errors.As 而不是类型断言，这样超时原因被别的错误包起来之后照样认得出来。
func Of(err error, code string) *Reason {
	var reason *Reason
	if !errors.As(err, &reason) {
		return nil
	}
	if code != "" && reason.Code != code {
		return nil
	}
	return reason
}

// OfContext 是 Of 的 context 版本，问的是「这个 context 是因为超时结束的吗」。
//
// 新增: DSH 那边 timeoutOf 一个函数同时收 AbortSignal 和 { reason } 两种东西，
// 因为 JS 里它们都只是「有个 reason 字段的对象」。Go 里 context 和 error 是两个类型，
// 与其做一个收 any 的函数，不如分成两个各自类型安全的入口。
func OfContext(ctx context.Context, code string) *Reason {
	return Of(context.Cause(ctx), code)
}

// Watchdog 是一条流的**空闲**超时：只在等下一个值的时候计时。
//
// 源: packages/util/timeout/src/index.ts:65-79（IdleWatchdog）
//
// 和 Deadline 的区别是它管的不是「这件事总共花了多久」，而是「上游多久没吐东西了」。
// 一条正常但很长的流不该被总时长打断，一条卡死的流必须被打断——两者的差别只有
// 「等下一个值等了多久」能分辨。
//
// 关键性质：定时器**只在一次等待进行中时**是开着的。消费方拿到值之后自己慢慢处理的
// 那段时间不算上游空闲（tests/timeout.spec.ts:203-230 钉了这条）。
type Watchdog struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
	idle   time.Duration
	code   string

	mutex       sync.Mutex
	timer       *time.Timer
	outstanding bool
	stopped     bool
}

// NewWatchdog 造一个空闲看门狗。idle 必须为正。
//
// 源: packages/util/timeout/src/index.ts:126-131
//
// 新增: DSH 还要求 idle 不超过 2147483647 毫秒，因为 Node 的 setTimeout 超过 32 位
// 就会把延迟悄悄压成 1 毫秒——一个「设了很长的超时」会变成「立刻超时」。
// Go 的 time.Timer 收 int64 纳秒，没有这个悬崖，所以那条上限连同它的常量一起不需要。
func NewWatchdog(parent context.Context, idle time.Duration, code string) (*Watchdog, error) {
	if idle <= 0 {
		return nil, fmt.Errorf("timeout: 空闲超时必须是正数，收到 %v", idle)
	}
	ctx, cancel := context.WithCancelCause(parent)
	return &Watchdog{ctx: ctx, cancel: cancel, idle: idle, code: code}, nil
}

// Context 返回看门狗守着的 context。它在整个生命周期里是同一个，
// 每次重新计时都不会换掉它——上游把它拿去用一次就够了
// （tests/timeout.spec.ts:213,223 断言的就是这个「同一个信号」）。
func (w *Watchdog) Context() context.Context { return w.ctx }

// Pulse 在一次等待进行中时重新计时。
//
// 源: packages/util/timeout/src/index.ts:162-165
//
// 它是给「传输层有动静、但还没凑出一个完整的值」用的：SSE 的心跳、HTTP/2 的窗口更新。
// 这些说明上游活着，不该算进空闲。没有等待在进行中时它什么都不做——
// 那时候本来就没在计时，凭空开一个定时器等于给消费方的思考时间也设了上限。
func (w *Watchdog) Pulse() {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if w.stopped || !w.outstanding {
		return
	}
	w.arm()
}

// Stop 停掉看门狗，可以多调。见本包头部关于「清理会取消 context」的说明。
func (w *Watchdog) Stop() {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if w.stopped {
		return
	}
	w.stopped = true
	w.disarm()
	w.cancel(context.Canceled)
}

// arm / disarm 必须在持锁时调用。
func (w *Watchdog) arm() {
	w.disarm()
	w.timer = time.AfterFunc(w.idle, func() {
		w.cancel(&Reason{Code: w.code, After: w.idle})
	})
}

func (w *Watchdog) disarm() {
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
}

// begin / end 是一次等待的两端：开始计时、结束计时。
func (w *Watchdog) begin() error {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if w.stopped {
		return ErrWatchdogStopped
	}
	if w.outstanding {
		return ErrDemandOutstanding
	}
	w.outstanding = true
	w.arm()
	return nil
}

func (w *Watchdog) end() {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.disarm()
	w.outstanding = false
}

// Receive 在看门狗守着的前提下等 channel 上的下一个值。
//
// 源: packages/util/timeout/src/index.ts:149-161
//
// 返回值的三种组合：
//   - (值, true, nil)    拿到一个值
//   - (零值, false, nil) 通道正常关闭，流结束了
//   - (零值, false, err) 出事了：看门狗被误用，或者 context 结束了
//     （用 Of(err, code) 判断是不是本看门狗的空闲超时）
//
// 写成自由函数而不是方法，是因为 Go 不允许方法带自己的类型参数。
//
// 新增: DSH 的 next 只 await 迭代器，超时靠迭代器自己观察信号后 reject
//
//	（index.ts:118-119 明说「signal only notifies」）。Go 里没有这个保证：
//	对面如果不理会 ctx，一个裸的 <-ch 会永远阻塞，把 goroutine 一起泄漏掉。
//	所以这里同时 select ctx.Done()——通知方必须自己也能退出。
func Receive[T any](w *Watchdog, ch <-chan T) (T, bool, error) {
	var zero T
	if err := w.begin(); err != nil {
		return zero, false, err
	}
	defer w.end()

	select {
	case value, ok := <-ch:
		return value, ok, nil
	case <-w.ctx.Done():
		return zero, false, context.Cause(w.ctx)
	}
}
