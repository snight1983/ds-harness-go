// 本文件的作用：攒批窗口、失败之后的顺序保持、以及那道静默屏障各自的行为。
//
// 源: packages/session/session-persistence/src/write-behind.ts

package persistence

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"ds-harness-go/session"
)

// recorder 记下每一次写进来的批次，并且可以把写卡住，好让测试在
// 「一次写正在飞」这个状态上做确定的观察。
//
// 后台写是脱离出去的 goroutine，没有句柄可等；卡住写是这里唯一能把并发状态
// 钉死的办法——靠 sleep 去撞时间窗口的测试迟早会在别人的机器上红。
type recorder struct {
	mu sync.Mutex
	// batches 是每次写收到的那批事件的 seq，进写函数的那一刻就记下。
	batches [][]int
	// failures 是还剩几次写要失败。
	failures int
	// failure 是安排出来的那个失败。
	failure error
	// reported 是被报出去的后台失败。
	reported []error

	// entered 非 nil 时，每次写一进来就把这批 seq 送进来。
	entered chan []int
	// release 非 nil 时，每次写都要等它给个信号才往下走。
	release chan struct{}
}

func (r *recorder) write(events []session.Event) error {
	seqs := make([]int, 0, len(events))
	for _, event := range events {
		seqs = append(seqs, event.Seq)
	}

	r.mu.Lock()
	r.batches = append(r.batches, seqs)
	entered, release := r.entered, r.release
	fails := r.failures > 0
	if fails {
		r.failures--
	}
	failure := r.failure
	r.mu.Unlock()

	if entered != nil {
		entered <- seqs
	}
	if release != nil {
		<-release
	}
	if fails {
		return failure
	}
	return nil
}

func (r *recorder) report(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.reported = append(r.reported, err)
}

// written 按顺序给出每一次写各自收到的那批 seq。
func (r *recorder) written() [][]int {
	r.mu.Lock()
	defer r.mu.Unlock()

	copied := make([][]int, len(r.batches))
	copy(copied, r.batches)
	return copied
}

// flattened 按顺序给出已经交给写函数的全部 seq。
func (r *recorder) flattened() []int {
	var all []int
	for _, batch := range r.written() {
		all = append(all, batch...)
	}
	return all
}

// batchCount 是已经发生过多少次写。
func (r *recorder) batchCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.batches)
}

// reportCount 是被报出去过多少次后台失败。
func (r *recorder) reportCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.reported)
}

// gated 造一个把写卡住的记录器。
func gated() *recorder {
	return &recorder{entered: make(chan []int, 8), release: make(chan struct{})}
}

// event 造一条只带 seq 的事件。
func event(seq int) session.Event {
	return session.Event{Type: session.EventUserMessage, Seq: seq, Data: json.RawMessage(`{}`)}
}

// waitFor 等一个条件成立，超时就判失败。
func waitFor(t *testing.T, why string, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("等不到：%s", why)
}

func TestEnqueueBatchesWithinTheWindow(t *testing.T) {
	t.Parallel()

	// 攒批是这个控制器存在的全部理由：一次流式输出的上百条分块，
	// 一条一次 fsync 和一百条一次 fsync 差着两个数量级。
	log := &recorder{}
	behind := NewWriteBehind(WriteBehindOptions{
		MaxDelay: 30 * time.Millisecond,
		Write:    log.write,
	})

	behind.Enqueue(event(1))
	behind.Enqueue(event(2))
	behind.Enqueue(event(3))

	if log.batchCount() != 0 {
		t.Fatalf("窗口还开着就不该写")
	}
	waitFor(t, "窗口到点写下去", func() bool { return log.batchCount() == 1 })

	if got := log.written(); len(got[0]) != 3 {
		t.Fatalf("三条该攒成一批：%v", got)
	}
}

func TestZeroDelayWritesImmediately(t *testing.T) {
	t.Parallel()

	log := &recorder{}
	behind := NewWriteBehind(WriteBehindOptions{Write: log.write})

	behind.Enqueue(event(1))
	waitFor(t, "不等就该写下去", func() bool { return log.batchCount() >= 1 })
}

func TestHasWorkGoesFalseOnlyAfterTheLastWriteLands(t *testing.T) {
	t.Parallel()

	// 销毁一个会话之前要等它变假，否则会丢掉最后那一批。
	log := &recorder{}
	behind := NewWriteBehind(WriteBehindOptions{MaxDelay: time.Hour, Write: log.write})

	if behind.HasWork() {
		t.Fatalf("空的时候该没活儿")
	}
	behind.Enqueue(event(1))
	if !behind.HasWork() {
		t.Fatalf("排着一条就该有活儿")
	}
	if err := behind.Flush(); err != nil {
		t.Fatalf("排空不该报错：%v", err)
	}
	if behind.HasWork() {
		t.Fatalf("排空之后该没活儿了")
	}
}

func TestHasWorkIsTrueWhileAWriteIsInFlight(t *testing.T) {
	t.Parallel()

	// 队列已经空了，但那一批还没落盘——这时候销毁会话仍然会丢东西，
	// 所以「有没有活儿」必须把在飞的那次写算进去。
	log := gated()
	behind := NewWriteBehind(WriteBehindOptions{Write: log.write})

	behind.Enqueue(event(1))
	<-log.entered
	if !behind.HasWork() {
		t.Fatalf("写还在飞就该算有活儿")
	}
	close(log.release)
	waitFor(t, "那次写落地", func() bool { return !behind.HasWork() })
}

func TestFlushCancelsTheWaitAndDrainsToQuiet(t *testing.T) {
	t.Parallel()

	log := &recorder{}
	behind := NewWriteBehind(WriteBehindOptions{
		// 一个长得不可能到点的窗口：写下去只可能是 Flush 干的。
		MaxDelay: time.Hour,
		Write:    log.write,
	})

	behind.Enqueue(event(1))
	behind.Enqueue(event(2))
	if err := behind.Flush(); err != nil {
		t.Fatalf("排空不该报错：%v", err)
	}

	if got := log.flattened(); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("该把两条都写下去：%v", got)
	}
}

func TestFlushOnAnEmptyQueueIsQuiet(t *testing.T) {
	t.Parallel()

	log := &recorder{}
	behind := NewWriteBehind(WriteBehindOptions{MaxDelay: time.Hour, Write: log.write})

	if err := behind.Flush(); err != nil {
		t.Fatalf("空队列排空不该报错：%v", err)
	}
	if log.batchCount() != 0 {
		t.Fatalf("空队列不该发生写：%d", log.batchCount())
	}
}

func TestASecondFlushJoinsTheLiveBarrier(t *testing.T) {
	t.Parallel()

	// 会合是靠「拿到的是同一个错误」认出来的：真会合了，两个调用方拿到的
	// 就是屏障自己那次失败；没会合的那个会另起一道屏障、把留住的那批重写一遍，
	// 于是拿到 nil。
	boom := errors.New("盘满了")
	log := gated()
	log.failures, log.failure = 1, boom

	behind := NewWriteBehind(WriteBehindOptions{MaxDelay: time.Hour, Write: log.write})
	behind.Enqueue(event(1))

	first := make(chan error, 1)
	go func() { first <- behind.Flush() }()
	<-log.entered

	// 第一次写卡着，屏障就一定还立着——它要等这次写回来才可能拆掉。
	// 所以这段时间里进来的第二个调用方必然走会合那条路。
	second := make(chan error, 1)
	go func() { second <- behind.Flush() }()
	time.Sleep(20 * time.Millisecond)

	close(log.release)

	if err := <-first; !errors.Is(err, boom) {
		t.Fatalf("发起屏障的那个该拿到那次失败：%v", err)
	}
	if err := <-second; !errors.Is(err, boom) {
		t.Fatalf("会合上来的那个该拿到同一个失败：%v", err)
	}
	if log.batchCount() != 1 {
		t.Fatalf("会合了就只该发生一次写：%d", log.batchCount())
	}
}

func TestEnqueueDuringABarrierIsPickedUpByIt(t *testing.T) {
	t.Parallel()

	// 屏障排到静默为止，所以它在跑的时候进来的事件由它一起写掉，不另开窗口——
	// 另开了也没用：那条事件会被搁在一道已经结束的屏障后面，谁也不写它。
	log := gated()
	behind := NewWriteBehind(WriteBehindOptions{MaxDelay: time.Hour, Write: log.write})
	behind.Enqueue(event(1))

	done := make(chan error, 1)
	go func() { done <- behind.Flush() }()
	<-log.entered

	behind.Enqueue(event(2))
	close(log.release)

	if err := <-done; err != nil {
		t.Fatalf("排空不该报错：%v", err)
	}
	if behind.HasWork() {
		t.Fatalf("排空之后不该还留着活儿")
	}
	if got := log.flattened(); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("屏障期间进来的那条也该被写掉，且排在后面：%v", got)
	}
}

func TestFlushWaitsForAnOverlappingAutomaticWrite(t *testing.T) {
	t.Parallel()

	// 一次自动写已经在飞的时候来了 Flush：必须先等它回来，
	// 否则两次写会同时在飞，落盘顺序就不再是 seq 顺序。
	log := gated()
	behind := NewWriteBehind(WriteBehindOptions{Write: log.write})

	behind.Enqueue(event(1))
	<-log.entered

	done := make(chan error, 1)
	go func() { done <- behind.Flush() }()

	// 自动那次还卡着，屏障不许自己先写起来。
	time.Sleep(20 * time.Millisecond)
	if log.batchCount() != 1 {
		t.Fatalf("重叠的那次写没回来之前不许再起一次：%d", log.batchCount())
	}

	behind.Enqueue(event(2))
	close(log.release)

	if err := <-done; err != nil {
		t.Fatalf("排空不该报错：%v", err)
	}
	if got := log.flattened(); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("两批该按 seq 顺序落下去：%v", got)
	}
}

func TestAFailedBatchGoesBackToTheHeadOfTheQueue(t *testing.T) {
	t.Parallel()

	// 日志是只追加的，seq 必须连续。把失败的那一批排到后来的事件后面
	// 等于写出一份乱序的日志——这是这个控制器里最要命的一条。
	boom := errors.New("盘满了")
	log := &recorder{failures: 1, failure: boom}
	behind := NewWriteBehind(WriteBehindOptions{MaxDelay: time.Hour, Write: log.write})

	behind.Enqueue(event(1))
	behind.Enqueue(event(2))
	if err := behind.Flush(); !errors.Is(err, boom) {
		t.Fatalf("该把那次失败交出来：%v", err)
	}

	// 失败之后事件按原顺序留着，下一次还会再试。
	behind.Enqueue(event(3))
	if err := behind.Flush(); err != nil {
		t.Fatalf("第二次不该报错：%v", err)
	}

	batches := log.written()
	if len(batches) != 2 {
		t.Fatalf("该发生两次写（一次失败、一次重试）：%v", batches)
	}
	retried := batches[1]
	if len(retried) != 3 || retried[0] != 1 || retried[1] != 2 || retried[2] != 3 {
		t.Fatalf("重试时 seq 必须还是连续的，留住的那批排在最前：%v", retried)
	}
}

func TestABarrierFailureLeavesTheEventsInOrder(t *testing.T) {
	t.Parallel()

	// 屏障排空期间失败会让屏障当场结束，事件按顺序留在队列里——
	// 不是丢掉，也不是排到后面。
	boom := errors.New("盘满了")
	log := &recorder{failures: 1, failure: boom}
	behind := NewWriteBehind(WriteBehindOptions{MaxDelay: time.Hour, Write: log.write})

	behind.Enqueue(event(1))
	if err := behind.Flush(); !errors.Is(err, boom) {
		t.Fatalf("该交出那次失败：%v", err)
	}
	if !behind.HasWork() {
		t.Fatalf("失败的那批该还留着")
	}
}

func TestABackgroundFailureIsReportedNotThrownAtTheProducer(t *testing.T) {
	t.Parallel()

	// 生产方早就走远了；这条失败要有人看见，但不该让下一次 Enqueue 报错。
	boom := errors.New("盘满了")
	log := &recorder{failures: 1, failure: boom}
	behind := NewWriteBehind(WriteBehindOptions{
		Write:                   log.write,
		ReportBackgroundFailure: log.report,
	})

	behind.Enqueue(event(1))
	waitFor(t, "后台失败被报出来", func() bool { return log.reportCount() == 1 })

	// 自动那条路停下了：一批写不下去的事件，立刻按原节奏再试只会以同样的
	// 理由再失败一次，而且每失败一次就多喊一声。
	before := log.batchCount()
	time.Sleep(20 * time.Millisecond)
	if log.batchCount() != before {
		t.Fatalf("失败之后自动那条路该停住，不该反复重试")
	}

	// 下一次 Enqueue 把它重新开起来。
	behind.Enqueue(event(2))
	waitFor(t, "重新开起来", func() bool { return log.batchCount() > before })

	batches := log.written()
	retried := batches[len(batches)-1]
	if len(retried) != 2 || retried[0] != 1 || retried[1] != 2 {
		t.Fatalf("留住的那条该排在前面：%v", retried)
	}
}

func TestABackgroundFailureWithoutAReporterDoesNotPanic(t *testing.T) {
	t.Parallel()

	// ReportBackgroundFailure 是可以不给的；不给就是没人看，但不许炸。
	boom := errors.New("盘满了")
	log := &recorder{failures: 1, failure: boom}
	behind := NewWriteBehind(WriteBehindOptions{Write: log.write})

	behind.Enqueue(event(1))
	waitFor(t, "那次失败发生", func() bool { return log.batchCount() == 1 })
	waitFor(t, "失败的那批回到队列", func() bool { return behind.HasWork() })
}

func TestCancelAutomaticWaitKeepsTheWorkButStopsTheClock(t *testing.T) {
	t.Parallel()

	// 用在「这个会话要交给别人了，别再自己往下写」那种交接上：
	// 活儿留着，但这个控制器不再自己动手。
	log := &recorder{}
	behind := NewWriteBehind(WriteBehindOptions{
		MaxDelay: 10 * time.Millisecond,
		Write:    log.write,
	})

	behind.Enqueue(event(1))
	behind.CancelAutomaticWait()

	time.Sleep(40 * time.Millisecond)
	if log.batchCount() != 0 {
		t.Fatalf("取消了窗口就不该再写：%d", log.batchCount())
	}
	if !behind.HasWork() {
		t.Fatalf("活儿该留着，取消的只是那次等待")
	}

	if err := behind.Flush(); err != nil {
		t.Fatalf("排空不该报错：%v", err)
	}
	if got := log.flattened(); len(got) != 1 {
		t.Fatalf("留着的活儿该能被排空写掉：%v", got)
	}
}

func TestCancelAutomaticWaitOnAnIdleControllerIsQuiet(t *testing.T) {
	t.Parallel()

	log := &recorder{}
	behind := NewWriteBehind(WriteBehindOptions{MaxDelay: time.Hour, Write: log.write})

	// 没有窗口开着的时候取消，什么都不该发生。
	behind.CancelAutomaticWait()
	if behind.HasWork() || log.batchCount() != 0 {
		t.Fatalf("空闲时取消不该有任何动静")
	}
}

func TestADeadlineThatLandsOnAnInFlightWriteContinuesAfterIt(t *testing.T) {
	t.Parallel()

	// 窗口到点时正好有一次写在飞：那次写完之后要立刻接着写，
	// 而不是再开一个新窗口白等一轮——那一轮是白等的，预算已经用掉了。
	log := gated()
	behind := NewWriteBehind(WriteBehindOptions{
		MaxDelay: 10 * time.Millisecond,
		Write:    log.write,
	})

	behind.Enqueue(event(1))
	<-log.entered

	// 第一次写卡着，这条会另开一个窗口，那个窗口在写还没回来时就到点。
	behind.Enqueue(event(2))
	time.Sleep(60 * time.Millisecond)
	if log.batchCount() != 1 {
		t.Fatalf("前一次写没回来之前不许再起一次：%d", log.batchCount())
	}

	close(log.release)
	waitFor(t, "第二批接着写下去", func() bool { return log.batchCount() == 2 })
	waitFor(t, "静默", func() bool { return !behind.HasWork() })

	if got := log.flattened(); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("两批该按 seq 顺序落下去：%v", got)
	}
}

func TestAStaleDeadlineStandsDown(t *testing.T) {
	t.Parallel()

	// Go 的 Timer.Stop 返回假时回调可能已经在跑、正卡在 mu 上，所以取消
	// 不能只靠 Stop。代数计数器就是为这件事立的：回调进来先认自己是不是
	// 当前那一代。这里直接拿一个过气的代数打进去，它必须一动不动。
	log := &recorder{}
	behind := NewWriteBehind(WriteBehindOptions{MaxDelay: time.Hour, Write: log.write})

	behind.mu.Lock()
	behind.pending = []session.Event{event(1)}
	behind.generation = 7
	behind.mu.Unlock()

	behind.onDeadline(6)

	time.Sleep(20 * time.Millisecond)
	if log.batchCount() != 0 {
		t.Fatalf("过气的定时器回调不许写任何东西：%d", log.batchCount())
	}
}

func TestADeadlineDuringABarrierStandsDown(t *testing.T) {
	t.Parallel()

	// 屏障接管了排空，自动这条路必须让开：两边同时写起来，落盘顺序就散了。
	// 这个状态在正常路径上撞不出来（立屏障时会把代数推走），所以直接把它
	// 摆出来打一枪——这是一道防御，防御也得有人验过它真的挡得住。
	log := &recorder{}
	behind := NewWriteBehind(WriteBehindOptions{MaxDelay: time.Hour, Write: log.write})

	behind.mu.Lock()
	behind.pending = []session.Event{event(1)}
	behind.barrier = &flushBarrier{done: make(chan struct{})}
	generation := behind.generation
	behind.mu.Unlock()

	behind.onDeadline(generation)

	time.Sleep(20 * time.Millisecond)
	if log.batchCount() != 0 {
		t.Fatalf("屏障立着的时候自动那条路不许写：%d", log.batchCount())
	}
}

func TestEnqueueClonesTheMutableSlices(t *testing.T) {
	t.Parallel()

	// 入了队之后这条事件就和生产方无关了：生产方后续再动它那两段字节，
	// 影响不到将要落盘的内容。
	captured := make(chan session.Event, 1)
	behind := NewWriteBehind(WriteBehindOptions{
		Write: func(events []session.Event) error {
			captured <- events[0]
			return nil
		},
	})

	original := session.Event{
		Type:            session.EventUserMessage,
		Seq:             1,
		Data:            json.RawMessage(`{"a":1}`),
		SourceEventSeqs: []int{7},
	}
	behind.Enqueue(original)

	// 入队之后立刻把原来那份改掉。
	original.Data[2] = 'b'
	original.SourceEventSeqs[0] = 99

	got := <-captured
	if string(got.Data) != `{"a":1}` {
		t.Fatalf("负载该是入队那一刻的样子：%s", got.Data)
	}
	if got.SourceEventSeqs[0] != 7 {
		t.Fatalf("来源清单该是入队那一刻的样子：%v", got.SourceEventSeqs)
	}
}

func TestCloneEventKeepsAnEmptyButPresentSourceList(t *testing.T) {
	t.Parallel()

	// 长度为零但非 nil 的清单要保住「明确给了一个空清单」这层意思——
	// 它和 nil（「没给」）在介质上是两个不同的东西。
	cloned := cloneEvent(session.Event{SourceEventSeqs: []int{}})
	if cloned.SourceEventSeqs == nil {
		t.Fatalf("空清单不许被复制成 nil")
	}
	if len(cloned.SourceEventSeqs) != 0 {
		t.Fatalf("空清单该还是空的：%v", cloned.SourceEventSeqs)
	}

	if cloneEvent(session.Event{}).SourceEventSeqs != nil {
		t.Fatalf("没给就该还是没给")
	}
}
