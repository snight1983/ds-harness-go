// 本文件的作用：那把按耐久孩子 id 串操作的锁的测试——互斥、不同孩子之间互不相干、
// 放开的幂等，以及等待期间被取消时的答复。

package subagent

import (
	"context"
	"sync"
	"testing"
)

// doneProbe 是一个只多做一件事的 ctx：Done 被取出来的那一刻它捎个信出去。
//
// 那把锁的等待写成 select，而 Go 在把一个 select 挂起之前会先按顺序把每个 case
// 的通道求值出来——所以 Done 被调到，就等于「这一位已经放掉互斥锁、走到那道
// select 了，不会再回头去抢」。等到这个信号再放开锁，那次唤醒必然从 waiter 那一
// 支出去：要么它已经挂上了、被 close 叫醒，要么它还没挂上、进 select 时就发现
// waiter 已经就绪。两种收场都走 `case <-waiter`，不看运气。
type doneProbe struct {
	context.Context
	once  sync.Once
	asked chan struct{}
}

func (c *doneProbe) Done() <-chan struct{} {
	c.once.Do(func() { close(c.asked) })
	return c.Context.Done()
}

func TestChildLockSerialisesTheSameChild(t *testing.T) {
	lock := newChildLock()
	ctx := context.Background()

	release, err := lock.acquire(ctx, "child")
	if err != nil {
		t.Fatalf("占锁失败：%v", err)
	}

	entered := make(chan struct{})
	go func() {
		second, err := lock.acquire(ctx, "child")
		if err != nil {
			t.Errorf("第二位占锁失败：%v", err)
			close(entered)
			return
		}
		second()
		close(entered)
	}()

	select {
	case <-entered:
		t.Fatal("锁还占着时第二位不该进得去")
	default:
	}
	release()
	<-entered
}

// 等在一个孩子上的那一位是被上一位放开叫醒的：唤醒之后它回去重抢，并且真的抢得上。
//
// 上面那个用例只钉住「占着的时候进不去」，它那一位有可能压根没等过——主协程
// 抢在它进 acquire 之前就放开了，于是它当场就占上了。这里用 [doneProbe] 把
// 「已经走到那道 select」这件事等实了再放开，被叫醒那一支才是必经的。
func TestChildLockWakesAWaiterOnRelease(t *testing.T) {
	lock := newChildLock()

	release, err := lock.acquire(context.Background(), "child")
	if err != nil {
		t.Fatalf("占锁失败：%v", err)
	}

	waiting := &doneProbe{Context: context.Background(), asked: make(chan struct{})}
	entered := make(chan struct{})
	go func() {
		defer close(entered)
		second, err := lock.acquire(waiting, "child")
		if err != nil {
			t.Errorf("第二位该被叫醒并占上，实际 %v", err)
			return
		}
		second()
	}()

	<-waiting.asked
	release()
	<-entered
}

// 不同孩子之间互不相干：一个孩子被占着，另一个照样当场进得去。
func TestChildLockDoesNotSerialiseDifferentChildren(t *testing.T) {
	lock := newChildLock()
	ctx := context.Background()

	first, err := lock.acquire(ctx, "child-a")
	if err != nil {
		t.Fatalf("占锁失败：%v", err)
	}
	defer first()

	second, err := lock.acquire(ctx, "child-b")
	if err != nil {
		t.Fatalf("另一个孩子该当场占得上，实际 %v", err)
	}
	second()
}

// 放开是幂等的：调用方在一条既有 defer 又有显式放开的路径上重复调它是正常的。
func TestChildLockReleaseIsIdempotent(t *testing.T) {
	lock := newChildLock()
	release, err := lock.acquire(context.Background(), "child")
	if err != nil {
		t.Fatalf("占锁失败：%v", err)
	}
	release()
	release()

	again, err := lock.acquire(context.Background(), "child")
	if err != nil {
		t.Fatalf("放开之后该重新占得上，实际 %v", err)
	}
	again()
}

// 等待期间被取消时**没有**占上，所以那一路不许调放开函数——第一个返回值为 nil
// 已经把这件事说死了。
func TestChildLockReportsCancellationWhileWaiting(t *testing.T) {
	lock := newChildLock()
	release, err := lock.acquire(context.Background(), "child")
	if err != nil {
		t.Fatalf("占锁失败：%v", err)
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	waited, err := lock.acquire(ctx, "child")
	if waited != nil {
		t.Fatal("被取消的等待不该交回放开函数")
	}
	if codeOf(err) != CodeCancelled {
		t.Fatalf("等待期间被取消该报 %s，实际 %v", CodeCancelled, err)
	}
}

// 一个已经取消的 ctx 不该拦住一次**当场就占得上**的获取：那一路压根不用等。
func TestChildLockIgnoresCancellationWhenItNeverWaits(t *testing.T) {
	lock := newChildLock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	release, err := lock.acquire(ctx, "child")
	if err != nil {
		t.Fatalf("没人占着时该当场占得上，实际 %v", err)
	}
	release()
}
