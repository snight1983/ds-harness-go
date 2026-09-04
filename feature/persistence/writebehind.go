// 本文件的作用：一个活会话的事件怎么攒成批写下去——有界的等待窗口、
// 写失败之后按顺序留住、以及一道显式的静默屏障。
//
// 源: packages/session/session-persistence/src/write-behind.ts

package persistence

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/snight1983/ds-harness-go/sessionlog"
)

// WriteBehindOptions 是一个写控制器的调度策略和落盘出口。
//
// 源: packages/session/session-persistence/src/write-behind.ts:8-16
type WriteBehindOptions struct {
	// MaxDelay 是一个空闲队列收到活儿之后，最多有意等多久才开始写。
	//
	// 等是为了攒批：一次模型流式输出会在几十毫秒内产出上百条分块事件，
	// 一条一次 fsync 和一百条一次 fsync 差着两个数量级。
	//
	// 小于等于零表示不等，收到就写。
	MaxDelay time.Duration

	// Write 把一段稳定的、有序的前缀写下去，返回时必须已经真的落盘。
	//
	// 新增: DSH 这里是 `(events) => Promise<void>`，没有取消通道。
	// Go 这边也不给 ctx：这个出口是装配方用闭包造出来的，它自己带着
	// 想要的 context——而写下去这件事一旦开始，中途取消只会留下半截。
	Write func(events []sessionlog.Event) error

	// ReportBackgroundFailure 观察一次脱离的后台写失败，不把它甩给生产方。
	//
	// 生产方是那个往队列里塞事件的循环，它早就走远了；这条失败要有人看见，
	// 但不该让下一次 Enqueue 报错——事件还在队列里留着，下一次还会再试。
	ReportBackgroundFailure func(err error)
}

// WriteBehind 攥着一个活会话待写的事件、一个固定的攒批窗口、当前那次写、
// 失败之后留住的批次，以及一道显式的静默屏障。
//
// 源: packages/session/session-persistence/src/write-behind.ts:18-24
//
// 零值不能用，用 [NewWriteBehind] 造。
//
// 新增: DSH 那份是单线程事件循环上的实现，靠「JS 不会在两句之间被打断」
// 免掉了全部同步。Go 这边每一处状态改动都在 mu 底下，定时器回调、后台写的
// goroutine、和调用方的 Flush 三方并发。语义按 DSH 那份逐条对齐，
// 差别只在下面这两处：
//
//   - 定时器取消。DSH 的 clearTimeout 在单线程里是确定的：清掉了就一定不会
//     再跑。Go 的 [time.Timer.Stop] 返回假时回调可能已经在跑、正卡在 mu 上，
//     所以这里另配一个代数计数器 generation，回调进来先看自己的代数还是不是
//     当前那一代，不是就当自己已经被取消了。
//
//   - 屏障期间的定时器。DSH 的 flush 先 clearTimeout 再往下走，单线程保证
//     此后没有定时器回调；Go 里一个已经在跑的回调仍可能在屏障立起来之后拿到
//     mu，所以 onDeadline 里多一道「屏障立着就什么都不做」。
type WriteBehind struct {
	maxDelay time.Duration
	write    func(events []sessionlog.Event) error
	report   func(err error)

	mu sync.Mutex
	// pending 是还没开始写的事件，按 seq 顺序。
	pending []sessionlog.Event
	// timer 是当前那个攒批窗口；nil 表示没有窗口开着。
	timer *time.Timer
	// generation 是攒批窗口的代数，用来认出一个已经过气的定时器回调。
	generation uint64
	// active 非 nil 表示有一次写在飞，它写完时这个通道会被关掉。
	active chan struct{}
	// barrier 非 nil 表示有一次 Flush 正在排空。
	barrier *flushBarrier
	// deadlineExpired 记着「窗口到点时正好有一次写在飞」——那次写完之后要
	// 立刻接着写，而不是再开一个新窗口白等一轮。
	deadlineExpired bool
	// automaticPaused 记着自动那条路因为一次写失败停下了。
	//
	// 停下来是有意的：一批写不下去的事件，立刻按原节奏再试一遍只会以同样的
	// 理由再失败一次，而且每失败一次就多喊一声。下一次 Enqueue 会把它重新开起来。
	automaticPaused bool
	// closed 表示这个控制器已经封了：没有任何一条自动的路会再写它。
	closed bool
}

// ErrWritesAbandoned 表示一条写路径走到尽头时，手上还有事件没落盘。
//
// 新增: 上游没有这条哨兵。[WriteBehind.HasWork] 那句「销毁之前要等它变假」在
// DSH 那边只是一句注释，谁忘了排空，最后那一批就无声无息地没了——而**一份少了
// 最后几条事件的会话日志和一份完整的长得一模一样**：它读得开、折得出状态、
// seq 也连续，只是停在了别的地方，事后没有任何一处看得出来它本该更长。
// 这条哨兵把那件事变成一个说得出口的失败。
var ErrWritesAbandoned = errors.New("feature/persistence: 写路径关闭时还有事件没落盘")

// flushBarrier 是一次 Flush 的会合点，并发的调用方共用同一个。
type flushBarrier struct {
	done chan struct{}
	err  error
}

// NewWriteBehind 造一个写控制器。
func NewWriteBehind(options WriteBehindOptions) *WriteBehind {
	return &WriteBehind{
		maxDelay: options.MaxDelay,
		write:    options.Write,
		report:   options.ReportBackgroundFailure,
	}
}

// HasWork 说明这个控制器手上还有没有排着的事件、或者有没有一次写在飞。
//
// 源: packages/session/session-persistence/src/write-behind.ts:33-38
//
// 销毁一个会话之前要等它变假，否则会丢掉最后那一批。
func (w *WriteBehind) HasWork() bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	return len(w.pending) > 0 || w.active != nil
}

// Enqueue 把一条事件复制进持久化自己的队列，自动那条路空闲时顺手开一个窗口。
//
// 源: packages/session/session-persistence/src/write-behind.ts:40-56
//
// 复制是有意的：入了队之后这条事件就和生产方无关了，生产方后续再动它
// （或者动它负载那段字节）都影响不到将要落盘的内容。
func (w *WriteBehind) Enqueue(event sessionlog.Event) {
	w.mu.Lock()

	if w.closed {
		// 封住之后没有任何一条自动的路会把它写下去。仍然收进队列，是为了让
		// [WriteBehind.HasWork] 说得出「还有东西没落盘」；同时当场喊一声，
		// 因为无声地把它丢掉正是这条哨兵要消灭的那种失败。
		w.pending = append(w.pending, cloneEvent(event))
		report := w.report
		w.mu.Unlock()
		if report != nil {
			report(fmt.Errorf("%w：写路径已经封了，seq %d 这条不会落盘", ErrWritesAbandoned, event.Seq))
		}
		return
	}
	defer w.mu.Unlock()

	wasEmpty := len(w.pending) == 0
	w.pending = append(w.pending, cloneEvent(event))
	if w.barrier != nil {
		// 屏障正排着队，它自己会把这条一起写掉，不必另开窗口。
		return
	}
	if w.automaticPaused {
		w.automaticPaused = false
		w.deadlineExpired = false
		w.armTimerLocked()
		return
	}
	if wasEmpty {
		w.armTimerLocked()
	}
}

// Flush 取消攒批的等待，一路写到静默为止。并发的调用方会合在同一道屏障上。
//
// 源: packages/session/session-persistence/src/write-behind.ts:58-72
//
// 返回的错误来自屏障自己那次写。屏障排空期间发生的失败会让屏障当场结束，
// 事件按顺序留在队列里。
func (w *WriteBehind) Flush() error {
	w.mu.Lock()
	if w.barrier != nil {
		joined := w.barrier
		w.mu.Unlock()
		<-joined.done
		return joined.err
	}
	w.cancelTimerLocked()
	w.deadlineExpired = false
	w.automaticPaused = false
	barrier := &flushBarrier{done: make(chan struct{})}
	w.barrier = barrier
	overlapping := w.active
	w.mu.Unlock()

	w.drainBarrier(barrier, overlapping)
	return barrier.err
}

// Close 封掉这个控制器：此后 Enqueue 进来的事件不再有人写，会被当场报出去。
//
// 新增: 上游没有这个方法，见 [ErrWritesAbandoned]。
//
// 它**自己不写盘**：调用方先 [WriteBehind.Flush] 排空，再 Close 封口。分成两步
// 是因为 Close 常常在一段串行区里调（会话退场那条路就是），而写盘自己也要占
// 同一把串行锁——在那里面再排空一次就是死锁。
//
// 手上还有活儿时返回一个包着 [ErrWritesAbandoned] 的错误，并且**不封**：
// 封了的话，一个决定「先不拆、留着重试」的调用方手上这个控制器也已经废了。
//
// 已经封住之后再调是空操作。关闭常常同时来自正常收尾和错误清理两条路，
// 谁先到是不确定的，所以幂等是必需的。
//
// Flush 在封住之后仍然写得动，那是留给恢复的口子：封掉的是「会有人自动来排空」
// 这个承诺，不是写下去的能力。
func (w *WriteBehind) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}
	if len(w.pending) > 0 || w.active != nil {
		inFlight := 0
		if w.active != nil {
			inFlight = 1
		}
		return fmt.Errorf("%w：还排着 %d 条，另有 %d 次写在飞",
			ErrWritesAbandoned, len(w.pending), inFlight)
	}
	w.cancelTimerLocked()
	w.closed = true
	return nil
}

// CancelAutomaticWait 取消当前那个自动窗口，但不排空已经留住的活儿。
//
// 源: packages/session/session-persistence/src/write-behind.ts:74-78
//
// 用在「这个会话要交给别人了，别再自己往下写」那种交接上。
func (w *WriteBehind) CancelAutomaticWait() {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.cancelTimerLocked()
	w.deadlineExpired = false
}

// drainBarrier 等掉可能重叠的那次自动写，排空到静默，然后结掉这道屏障。
//
// 源: packages/session/session-persistence/src/write-behind.ts:113-133
func (w *WriteBehind) drainBarrier(barrier *flushBarrier, overlapping chan struct{}) {
	if overlapping != nil {
		// 不管它成没成——它要是失败了，那一批已经按顺序回到队列里，
		// 下面这个循环会连它一起再写一次。
		<-overlapping
		w.mu.Lock()
		w.automaticPaused = false
		w.mu.Unlock()
	}

	for {
		w.mu.Lock()
		if len(w.pending) == 0 {
			// 在**看见队列空了的同一个临界区里**把屏障摘掉，然后才放行等的人。
			// 晚一步摘，一条后来的 Enqueue 会以为屏障还立着而不开窗口，
			// 于是被搁在一道已经结束的屏障后面，谁也不写它。
			w.barrier = nil
			w.mu.Unlock()
			close(barrier.done)
			return
		}
		w.mu.Unlock()

		if err := w.startWrite(false); err != nil {
			w.mu.Lock()
			w.barrier = nil
			w.mu.Unlock()
			barrier.err = err
			close(barrier.done)
			return
		}
	}
}

// armTimerLocked 为当前这段待写前缀开一个窗口。调用方必须已经持有 mu。
//
// 源: packages/session/session-persistence/src/write-behind.ts:80-83
func (w *WriteBehind) armTimerLocked() {
	w.generation++
	generation := w.generation
	if w.maxDelay <= 0 {
		// 不等：直接开一次后台写。不能在这里同步调 startWrite——mu 还攥在手里。
		go w.startBackground()
		return
	}
	w.timer = time.AfterFunc(w.maxDelay, func() { w.onDeadline(generation) })
}

// cancelTimerLocked 取消当前那个窗口。调用方必须已经持有 mu。
//
// 源: packages/session/session-persistence/src/write-behind.ts:85-90
//
// 代数照样往前走：Stop 返回假时那个回调可能已经在跑了，靠代数把它认出来。
func (w *WriteBehind) cancelTimerLocked() {
	w.generation++
	if w.timer == nil {
		return
	}
	w.timer.Stop()
	w.timer = nil
}

// onDeadline 窗口到点：当场开一次后台写，或者记下「这次写用掉了这轮预算」。
//
// 源: packages/session/session-persistence/src/write-behind.ts:92-100
func (w *WriteBehind) onDeadline(generation uint64) {
	w.mu.Lock()
	if generation != w.generation {
		// 这一枪是过气那一代打的，已经被取消了。
		w.mu.Unlock()
		return
	}
	w.timer = nil
	if w.barrier != nil {
		// 屏障接管了排空，自动这条路让开。
		w.mu.Unlock()
		return
	}
	if w.active != nil {
		w.deadlineExpired = true
		w.mu.Unlock()
		return
	}
	w.mu.Unlock()

	go w.startBackground()
}

// startBackground 开一次脱离的写，它的失败会被报出去并且把那一批留住。
//
// 源: packages/session/session-persistence/src/write-behind.ts:102-106
func (w *WriteBehind) startBackground() {
	if err := w.startWrite(true); err != nil {
		return
	}
	w.continueAutomatic()
}

// continueAutomatic 一次超了预算的写完成之后立刻接着写，否则留着它的窗口。
//
// 源: packages/session/session-persistence/src/write-behind.ts:108-111
func (w *WriteBehind) continueAutomatic() {
	w.mu.Lock()
	if w.barrier != nil || len(w.pending) == 0 || !w.deadlineExpired {
		w.mu.Unlock()
		return
	}
	w.deadlineExpired = false
	w.mu.Unlock()

	w.startBackground()
}

// startWrite 写掉当前这段稳定的待写前缀，落盘失败就按原顺序把它留回去。
//
// 源: packages/session/session-persistence/src/write-behind.ts:135-155
func (w *WriteBehind) startWrite(background bool) error {
	w.mu.Lock()
	batch := w.pending
	w.pending = nil
	w.cancelTimerLocked()
	w.deadlineExpired = false
	done := make(chan struct{})
	w.active = done
	w.mu.Unlock()

	err := w.write(batch)

	w.mu.Lock()
	if err != nil {
		// 放回**队头**，不是队尾：日志是只追加的，seq 必须连续，
		// 把失败的这一批排到后来那些事件后面等于写出一份乱序的日志。
		w.pending = append(batch, w.pending...)
		w.cancelTimerLocked()
		w.deadlineExpired = false
		w.automaticPaused = true
	}
	w.active = nil
	w.mu.Unlock()
	close(done)

	if err != nil && background && w.report != nil {
		w.report(err)
	}
	return err
}

// cloneEvent 把一条事件里那两段可变的切片复制一份出来。
//
// 新增: DSH 用 structuredClone 深拷整张对象图，因为 JS 里事件是一张随手可改的
// 图。Go 的 [sessionlog.Event] 是值类型，赋值就已经复制了标量字段；
// [sessionlog.SurfaceOp] 的两个变体也都是值结构体。剩下会跟生产方共享的只有
// Data 和 SourceEventSeqs 两段切片，复制它们两个就够，而这比深拷一张图便宜得多。
func cloneEvent(event sessionlog.Event) sessionlog.Event {
	event.Data = bytes.Clone(event.Data)
	if event.SourceEventSeqs != nil {
		// 复制而不是 slices.Clone：长度为零但非 nil 的清单要保住「明确给了一个
		// 空清单」这层意思，见 [sessionlog.Event.SourceEventSeqs]。
		seqs := make([]int, len(event.SourceEventSeqs))
		copy(seqs, event.SourceEventSeqs)
		event.SourceEventSeqs = seqs
	}
	return event
}
