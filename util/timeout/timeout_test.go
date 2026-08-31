// 本文件的作用：把 DSH 那份 291 行测试（packages/util/timeout/tests/timeout.spec.ts）
// 钉住的行为事实逐条在 Go 侧重新钉一遍。
//
// 这个包只做归类，不做终止，所以「归类对不对」就是它的全部价值。而归类出错的方式
// 恰恰都是**沉默**的：把上游取消认成自己超时会导致不该有的重试，把外层超时认成自己
// 超时会让报错指向错误的能力。两种都不会崩，只会让人查错方向。
//
// Go 标准库没有假时钟，所以下面用的是真时间。每个时长都留了三倍以上的余量，
// 因为 Windows 的定时器精度大约是 15 毫秒——余量不够的话，测试会偶尔红，
// 而一条偶尔红的测试很快就会被人当成噪音忽略掉，那还不如没有。
package timeout

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func durationPtr(d time.Duration) *time.Duration { return &d }

func TestReasonCarriesTheCodeAndTheElapsedDeadline(t *testing.T) {
	t.Parallel()

	reason := &Reason{Code: "BASH_TIMEOUT", After: 100 * time.Millisecond}
	if got, want := reason.Error(), "BASH_TIMEOUT after 100ms"; got != want {
		t.Errorf("文案该是 %q，实际 %q", want, got)
	}
}

func TestClampResolvesTheEffectiveTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		requested *time.Duration
		def, max  time.Duration
		want      time.Duration
	}{
		{"没给提示就用默认值", nil, 120 * time.Second, 600 * time.Second, 120 * time.Second},
		{"提示超过上限就压到上限", durationPtr(999 * time.Second), 120 * time.Second, 600 * time.Second, 600 * time.Second},
		{"上限内的提示原样保留", durationPtr(5 * time.Second), 120 * time.Second, 600 * time.Second, 5 * time.Second},
		// 这一条是容易漏的：上限对默认值本身也生效，一个配错了的后端不会因为
		// 调用方没传就越过自己的上限。
		{"默认值超过上限时默认值也被压", nil, 900 * time.Second, 600 * time.Second, 600 * time.Second},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := Clamp(test.requested, test.def, test.max, "timeoutMs")
			if err != nil {
				t.Fatalf("Clamp 意外失败：%v", err)
			}
			if got != test.want {
				t.Errorf("生效的超时该是 %v，实际 %v", test.want, got)
			}
		})
	}
}

// TestClampRejectsNonPositiveHints 钉住零不是「关掉超时」的公开写法。
//
// 如果零被当成「不限时」，那么一个把字段漏填成零值的调用方会得到一个永不超时的请求，
// 而这件事不会有任何提示。
func TestClampRejectsNonPositiveHints(t *testing.T) {
	t.Parallel()

	for _, requested := range []time.Duration{0, -1} {
		_, err := Clamp(durationPtr(requested), 100*time.Second, 200*time.Second,
			"bash-local: request.timeoutMs")
		if err == nil {
			t.Fatalf("提示 %v 本该被拒", requested)
		}
		// 报错必须带上调用方给的字段名，否则调用方不知道是哪个字段填错了。
		if !strings.Contains(err.Error(), "bash-local: request.timeoutMs") {
			t.Errorf("报错该带上字段名，实际 %q", err.Error())
		}
	}
}

func TestDeadlineFiresWithAnAttributableReason(t *testing.T) {
	t.Parallel()

	ctx, cleanup := Deadline(context.Background(), 50*time.Millisecond, "BASH_TIMEOUT")
	defer cleanup()

	if OfContext(ctx, "") != nil {
		t.Fatal("刚建好时不该已经超时")
	}
	<-ctx.Done()

	reason := OfContext(ctx, "BASH_TIMEOUT")
	if reason == nil {
		t.Fatal("超时后该认得出是 BASH_TIMEOUT")
	}
	if reason.After != 50*time.Millisecond {
		t.Errorf("该带上过去的那个期限 50ms，实际 %v", reason.After)
	}
}

// TestDeadlineCleanupLeavesNoTimeoutClassification 钉住清理之后不会再冒出一个超时。
//
// 这是 DSH 那条「dispose 清掉定时器」的可观察后果。Go 这边 ctx 会因为清理而结束，
// 但**归类**必须是「取消」而不是「超时」——否则一次正常收尾会被下游当成超时去重试。
func TestDeadlineCleanupLeavesNoTimeoutClassification(t *testing.T) {
	t.Parallel()

	ctx, cleanup := Deadline(context.Background(), 50*time.Millisecond, "BASH_TIMEOUT")
	cleanup()
	time.Sleep(150 * time.Millisecond)

	if reason := OfContext(ctx, ""); reason != nil {
		t.Errorf("清理之后不该有任何超时归类，实际 %v", reason)
	}
	if !errors.Is(context.Cause(ctx), context.Canceled) {
		t.Errorf("清理之后的原因该是普通取消，实际 %v", context.Cause(ctx))
	}
}

// TestUpstreamCancellationIsNeverATimeout 钉住这个包存在的核心理由的一半：
// 用户按了停止，不该被认成超时。
func TestUpstreamCancellationIsNeverATimeout(t *testing.T) {
	t.Parallel()

	upstream, cancelUpstream := context.WithCancelCause(context.Background())
	ctx, cleanup := Deadline(upstream, time.Hour, "BASH_TIMEOUT")
	defer cleanup()

	userStopped := errors.New("用户取消")
	cancelUpstream(userStopped)
	<-ctx.Done()

	if reason := OfContext(ctx, ""); reason != nil {
		t.Errorf("上游取消不该被认成超时，实际 %v", reason)
	}
	if !errors.Is(context.Cause(ctx), userStopped) {
		t.Errorf("原因该原样传下来，实际 %v", context.Cause(ctx))
	}
}

// TestFirstCauseWins 钉住「谁先取消谁定原因」。
//
// 超时已经发生之后上游再取消，归类必须仍然是超时——事情确实是被超时打断的，
// 后来的那次取消改变不了已经发生的事。
func TestFirstCauseWins(t *testing.T) {
	t.Parallel()

	upstream, cancelUpstream := context.WithCancelCause(context.Background())
	defer cancelUpstream(context.Canceled)

	ctx, cleanup := Deadline(upstream, 50*time.Millisecond, "WEB_FETCH_TIMEOUT")
	defer cleanup()
	<-ctx.Done()

	if OfContext(ctx, "WEB_FETCH_TIMEOUT") == nil {
		t.Fatal("超时先到，该被认成 WEB_FETCH_TIMEOUT")
	}
	cancelUpstream(errors.New("太晚了"))
	if OfContext(ctx, "WEB_FETCH_TIMEOUT") == nil {
		t.Error("后来的上游取消不该把已经定下的超时归类改掉")
	}
}

func TestPreCancelledParentPassesStraightThrough(t *testing.T) {
	t.Parallel()

	upstream, cancelUpstream := context.WithCancelCause(context.Background())
	alreadyGone := errors.New("早就没了")
	cancelUpstream(alreadyGone)

	ctx, cleanup := Deadline(upstream, time.Hour, "BASH_TIMEOUT")
	defer cleanup()

	if ctx.Err() == nil {
		t.Fatal("父 context 已经结束，子 context 该立刻也结束")
	}
	if !errors.Is(context.Cause(ctx), alreadyGone) {
		t.Errorf("原因该是父的那个，实际 %v", context.Cause(ctx))
	}
	if reason := OfContext(ctx, ""); reason != nil {
		t.Errorf("这不是超时，实际被认成 %v", reason)
	}
}

// TestNonPositiveTimeoutArmsNoTimer 钉住内部的「不计时」哨兵。
//
// 后台任务需要「不设总时长上限」。此时必须原样返回父 context：造一个不 cancel 的子
// context 是泄漏，cancel 了又等于替调用方结束了父的作用域，两条路都不能走。
func TestNonPositiveTimeoutArmsNoTimer(t *testing.T) {
	t.Parallel()

	for _, timeout := range []time.Duration{0, -5 * time.Second} {
		parent, cancelParent := context.WithCancel(context.Background())
		ctx, cleanup := Deadline(parent, timeout, "BASH_TIMEOUT")
		cleanup() // 空操作：不该把父 context 结束掉

		if ctx != parent {
			t.Errorf("超时 %v 时该原样返回父 context", timeout)
		}
		if ctx.Err() != nil {
			t.Errorf("超时 %v 时清理函数不该结束父 context，实际 %v", timeout, ctx.Err())
		}

		cancelParent()
		if reason := OfContext(ctx, ""); reason != nil {
			t.Errorf("从来没装过定时器，不可能是超时，实际 %v", reason)
		}
	}
}

// TestNestedDeadlineIsNotMisclassified 钉住这个包存在的核心理由的另一半。
//
// 内层的上游本身就是一个已经超时的外层。原因会沿链传到内层，但内层拿**自己的码**
// 去问必须得到 nil——那是上游取消，不是内层这次调用超时了。
// 搞反的后果是内层报出一条自己名下的超时，而真正的期限是外面那个。
func TestNestedDeadlineIsNotMisclassified(t *testing.T) {
	t.Parallel()

	outer, cleanupOuter := Deadline(context.Background(), 50*time.Millisecond, "OUTER_TIMEOUT")
	defer cleanupOuter()
	<-outer.Done()

	inner, cleanupInner := Deadline(outer, time.Hour, "BASH_TIMEOUT")
	defer cleanupInner()

	if reason := OfContext(inner, "BASH_TIMEOUT"); reason != nil {
		t.Errorf("外层的超时不该被认成内层的 BASH_TIMEOUT，实际 %v", reason)
	}
	reason := OfContext(inner, "")
	if reason == nil || reason.Code != "OUTER_TIMEOUT" {
		t.Errorf("不挑代号时该认出 OUTER_TIMEOUT，实际 %v", reason)
	}
}

func TestNewWatchdogRejectsNonPositiveIdle(t *testing.T) {
	t.Parallel()

	for _, idle := range []time.Duration{0, -time.Second} {
		if _, err := NewWatchdog(context.Background(), idle, "IDLE"); err == nil {
			t.Errorf("空闲超时 %v 本该被拒", idle)
		}
	}
}

// TestWatchdogCountsOnlyTimeSpentWaiting 是这个类型存在的全部理由。
//
// 消费方拿到一个值之后自己慢慢处理的那段时间，不是上游空闲。如果把它算进去，
// 一条正常但消费得慢的流会被误判成卡死——而这种误判在压力大的时候最容易发生，
// 也就是最不该出错的时候。
func TestWatchdogCountsOnlyTimeSpentWaiting(t *testing.T) {
	t.Parallel()

	const idle = 150 * time.Millisecond
	watchdog, err := NewWatchdog(context.Background(), idle, "LLM_STREAM_IDLE_TIMEOUT")
	if err != nil {
		t.Fatalf("NewWatchdog 意外失败：%v", err)
	}
	defer watchdog.Stop()

	stream := make(chan int, 1)
	stream <- 1
	if value, ok, err := Receive(watchdog, stream); err != nil || !ok || value != 1 {
		t.Fatalf("该收到 1，实际 value=%v ok=%v err=%v", value, ok, err)
	}

	// 没有等待在进行中，这段时间远超空闲上限，但它是消费方的思考时间。
	time.Sleep(3 * idle)
	if watchdog.Context().Err() != nil {
		t.Fatal("消费方的思考时间不该算成上游空闲")
	}

	stream <- 2
	if value, ok, err := Receive(watchdog, stream); err != nil || !ok || value != 2 {
		t.Fatalf("该收到 2，实际 value=%v ok=%v err=%v", value, ok, err)
	}

	// 这一次真的没人喂了，才该超时。
	_, _, err = Receive(watchdog, stream)
	reason := Of(err, "LLM_STREAM_IDLE_TIMEOUT")
	if reason == nil {
		t.Fatalf("上游真的不吐了，该报空闲超时，实际 %v", err)
	}
	if reason.After != idle {
		t.Errorf("该带上空闲上限 %v，实际 %v", idle, reason.After)
	}
}

// TestWatchdogPulseRearmsAnOutstandingWait 钉住心跳能续命。
//
// 传输层有动静但还没凑出一个完整的值（SSE 心跳、HTTP/2 窗口更新）说明上游活着。
// 不认这个信号的话，一个吐字慢但没死的模型会被当成卡死掐掉。
func TestWatchdogPulseRearmsAnOutstandingWait(t *testing.T) {
	t.Parallel()

	const idle = 150 * time.Millisecond
	watchdog, err := NewWatchdog(context.Background(), idle, "LLM_STREAM_IDLE_TIMEOUT")
	if err != nil {
		t.Fatalf("NewWatchdog 意外失败：%v", err)
	}
	defer watchdog.Stop()

	stream := make(chan int)
	done := make(chan error, 1)
	go func() {
		_, _, receiveErr := Receive(watchdog, stream)
		done <- receiveErr
	}()

	// 累计 240ms 远超 150ms 的空闲上限，但每一段间隔都在上限之内。
	for range 4 {
		time.Sleep(60 * time.Millisecond)
		watchdog.Pulse()
	}
	select {
	case err := <-done:
		t.Fatalf("一直有心跳，不该超时，实际 %v", err)
	default:
	}

	// 停掉心跳，这次才该超时。
	if reason := Of(<-done, "LLM_STREAM_IDLE_TIMEOUT"); reason == nil {
		t.Error("心跳停了之后该报空闲超时")
	}
}

// TestWatchdogPulseWithoutAnOutstandingWaitDoesNothing 钉住空转的心跳不开表。
//
// 没有等待在进行中时本来就没在计时。此时开一个定时器，等于给消费方的思考时间
// 也设了上限——那正好是上一条测试要防的事。
func TestWatchdogPulseWithoutAnOutstandingWaitDoesNothing(t *testing.T) {
	t.Parallel()

	watchdog, err := NewWatchdog(context.Background(), 50*time.Millisecond, "IDLE")
	if err != nil {
		t.Fatalf("NewWatchdog 意外失败：%v", err)
	}
	defer watchdog.Stop()

	watchdog.Pulse()
	time.Sleep(200 * time.Millisecond)
	if watchdog.Context().Err() != nil {
		t.Error("没有等待在进行中时，心跳不该把表开起来")
	}
}

func TestWatchdogUpstreamCancellationIsNotATimeout(t *testing.T) {
	t.Parallel()

	upstream, cancelUpstream := context.WithCancelCause(context.Background())
	watchdog, err := NewWatchdog(upstream, time.Hour, "LLM_STREAM_IDLE_TIMEOUT")
	if err != nil {
		t.Fatalf("NewWatchdog 意外失败：%v", err)
	}
	defer watchdog.Stop()

	userStopped := errors.New("调用方取消")
	cancelUpstream(userStopped)

	_, _, err = Receive(watchdog, make(chan int))
	if Of(err, "LLM_STREAM_IDLE_TIMEOUT") != nil {
		t.Errorf("上游取消不该被认成空闲超时，实际 %v", err)
	}
	if !errors.Is(err, userStopped) {
		t.Errorf("原因该原样传出来，实际 %v", err)
	}
}

// TestWatchdogRejectsConcurrentDemand 钉住同一时刻只能有一次等待。
//
// 两次并发的等待会共用一个定时器，于是「空闲了多久」这个量失去意义：
// 一次等待的重新计时会把另一次的表也拨回去。
func TestWatchdogRejectsConcurrentDemand(t *testing.T) {
	t.Parallel()

	watchdog, err := NewWatchdog(context.Background(), time.Hour, "IDLE")
	if err != nil {
		t.Fatalf("NewWatchdog 意外失败：%v", err)
	}
	defer watchdog.Stop()

	stream := make(chan int)
	started := make(chan struct{})
	go func() {
		close(started)
		_, _, _ = Receive(watchdog, stream)
	}()
	<-started
	// 让第一次等待确实进入 Receive 内部。
	time.Sleep(50 * time.Millisecond)

	if _, _, err := Receive(watchdog, stream); !errors.Is(err, ErrDemandOutstanding) {
		t.Errorf("并发的第二次等待该被拒，实际 %v", err)
	}
	close(stream)
}

func TestWatchdogStopIsIdempotentAndClosesTheDoor(t *testing.T) {
	t.Parallel()

	watchdog, err := NewWatchdog(context.Background(), time.Hour, "IDLE")
	if err != nil {
		t.Fatalf("NewWatchdog 意外失败：%v", err)
	}

	watchdog.Stop()
	watchdog.Stop()
	watchdog.Pulse() // 停了之后的心跳不该炸

	if _, _, err := Receive(watchdog, make(chan int)); !errors.Is(err, ErrWatchdogStopped) {
		t.Errorf("停掉之后的等待该被拒，实际 %v", err)
	}
	if reason := OfContext(watchdog.Context(), ""); reason != nil {
		t.Errorf("主动停止不是超时，实际被认成 %v", reason)
	}
}

// TestReceiveReportsAClosedStream 钉住流正常结束和出错是两回事。
func TestReceiveReportsAClosedStream(t *testing.T) {
	t.Parallel()

	watchdog, err := NewWatchdog(context.Background(), time.Hour, "IDLE")
	if err != nil {
		t.Fatalf("NewWatchdog 意外失败：%v", err)
	}
	defer watchdog.Stop()

	stream := make(chan int)
	close(stream)

	value, ok, err := Receive(watchdog, stream)
	if err != nil {
		t.Fatalf("通道正常关闭不是错误，实际 %v", err)
	}
	if ok {
		t.Error("通道已关闭时 ok 该是 false")
	}
	if value != 0 {
		t.Errorf("通道已关闭时该给零值，实际 %v", value)
	}
}
