// 本文件的作用：那把按耐久孩子 id 串操作的锁的测试——互斥、不同孩子之间互不相干、
// 放开的幂等，以及等待期间被取消时的答复。

package subagent

import (
	"context"
	"testing"
)

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
