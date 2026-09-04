// 本文件的作用：钉住那道关闭契约——一条写路径走到尽头时，手上要是还有没落盘的
// 事件，这件事必须**说得出口**。
//
// 为什么这件事值得单独一个文件：一份少了最后几条事件的会话日志和一份完整的
// 长得一模一样。它读得开、折得出状态、seq 也连续，只是停在了别的地方。没有任何
// 事后的校验看得出它本该更长——所以唯一的机会在关闭那一刻。
//
// 这里要分清三件事：
//
//   - **排空**（[WriteBehind.Flush]）把手上的活儿写下去；
//   - **封口**（[WriteBehind.Close]）宣布此后没人会再自动来排空；
//   - 封不上，说明前一步没做或者没做成，那是一个错误，不是一个可以咽下去的状态。

package persistence

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCloseSealsAQuietController(t *testing.T) {
	t.Parallel()

	log := &recorder{}
	behind := NewWriteBehind(WriteBehindOptions{Write: log.write, ReportBackgroundFailure: log.report})

	behind.Enqueue(event(1))
	if err := behind.Flush(); err != nil {
		t.Fatalf("排空不该失败：%v", err)
	}

	if err := behind.Close(); err != nil {
		t.Fatalf("排空过的控制器应当封得上：%v", err)
	}
	// 关闭常常同时来自正常收尾和错误清理两条路，谁先到是不确定的。
	if err := behind.Close(); err != nil {
		t.Fatalf("再封一次应当是空操作：%v", err)
	}
}

func TestCloseRefusesWhenEventsAreStillQueued(t *testing.T) {
	t.Parallel()

	// 窗口开得很长，所以这一条稳稳地停在「排着队还没开始写」那个状态上。
	log := &recorder{}
	behind := NewWriteBehind(WriteBehindOptions{
		MaxDelay: time.Hour,
		Write:    log.write,
	})

	behind.Enqueue(event(1))

	err := behind.Close()

	if !errors.Is(err, ErrWritesAbandoned) {
		t.Fatalf("应当报 ErrWritesAbandoned，实际是 %v", err)
	}
	// 数字要说得出来：一句「还有活儿」帮不了看日志的人判断丢了多少。
	if !strings.Contains(err.Error(), "还排着 1 条") {
		t.Fatalf("这句话里应当说清楚还剩多少：%v", err)
	}
	// 封不上就不能封：调用方完全可以决定「先不拆，再排空一次」，
	// 而一个已经废掉的控制器让那条退路也没了。
	if err := behind.Flush(); err != nil {
		t.Fatalf("拒绝封口之后应当还排空得动：%v", err)
	}
	if got := log.flattened(); len(got) != 1 || got[0] != 1 {
		t.Fatalf("那条事件应当还在，并且被写了下去：%v", got)
	}
	if err := behind.Close(); err != nil {
		t.Fatalf("排空之后应当封得上：%v", err)
	}
}

func TestCloseRefusesWhileAWriteIsInFlight(t *testing.T) {
	t.Parallel()

	// 「排着队」和「正在飞」是两种不同的没落盘：后者的那一批已经离开队列了，
	// 只看 pending 的实现会在这里报绿。
	log := gated()
	behind := NewWriteBehind(WriteBehindOptions{Write: log.write, ReportBackgroundFailure: log.report})

	behind.Enqueue(event(1))
	<-log.entered

	err := behind.Close()

	if !errors.Is(err, ErrWritesAbandoned) {
		t.Fatalf("有一次写在飞的时候应当封不上，实际是 %v", err)
	}
	if !strings.Contains(err.Error(), "1 次写在飞") {
		t.Fatalf("这句话里应当点出有写在飞：%v", err)
	}

	close(log.release)
	waitFor(t, "那次写落地", func() bool { return !behind.HasWork() })
	if err := behind.Close(); err != nil {
		t.Fatalf("落地之后应当封得上：%v", err)
	}
}

// 封住之后再进来的事件是这条契约最难看见的那一半：它们进的是一个再也没人
// 排空的队列。留在队列里让 HasWork 说得出话，同时当场喊一声。
func TestEnqueueAfterCloseIsReportedNotSwallowed(t *testing.T) {
	t.Parallel()

	log := &recorder{}
	behind := NewWriteBehind(WriteBehindOptions{Write: log.write, ReportBackgroundFailure: log.report})
	if err := behind.Close(); err != nil {
		t.Fatalf("空控制器应当封得上：%v", err)
	}

	behind.Enqueue(event(7))

	if log.batchCount() != 0 {
		t.Fatalf("封住之后不该再有自动的写：%v", log.written())
	}
	if log.reportCount() != 1 {
		t.Fatalf("应当正好报一声：%v", log.reported)
	}
	reported := log.reported[0]
	if !errors.Is(reported, ErrWritesAbandoned) {
		t.Fatalf("报出去的应当是 ErrWritesAbandoned：%v", reported)
	}
	// 点得出是哪一条，不然看日志的人只知道「丢了点东西」。
	if !strings.Contains(reported.Error(), "seq 7") {
		t.Fatalf("这句话里应当点出是哪一条：%v", reported)
	}
	// 它仍然算「手上还有东西」：一次拆解的点名要看得见它。
	if !behind.HasWork() {
		t.Fatal("封住之后进来的事件仍然算没落盘")
	}
}

// 封口不该把一次已经排上的自动窗口留在身后：那个定时器到点时会去写一个
// 已经宣布没人管的控制器。
func TestCloseCancelsThePendingWindow(t *testing.T) {
	t.Parallel()

	log := &recorder{}
	behind := NewWriteBehind(WriteBehindOptions{
		MaxDelay: 20 * time.Millisecond,
		Write:    log.write,
	})

	behind.Enqueue(event(1))
	if err := behind.Flush(); err != nil {
		t.Fatalf("排空不该失败：%v", err)
	}
	if err := behind.Close(); err != nil {
		t.Fatalf("应当封得上：%v", err)
	}
	behind.Enqueue(event(2))

	time.Sleep(60 * time.Millisecond)

	if got := log.flattened(); len(got) != 1 || got[0] != 1 {
		t.Fatalf("封住之后那条不该被写下去：%v", got)
	}
}
