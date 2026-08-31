// 本文件的作用：准备期那一小段的测试——它裹着的会话交得出来，释放恰好发生一次。

package session

import (
	"sync"
	"testing"
)

func TestAPreparationHandsBackItsSession(t *testing.T) {
	store := newStore(t)
	session, err := store.Prepare("a", CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	preparation := NewPreparation(session, PreparationOptions{})
	if preparation.Session() != session {
		t.Fatal("交出来的不是同一个会话")
	}
	// 没给释放回调时照样能调，什么都不做。
	preparation.Release()
	preparation.Release()
}

func TestReleaseHappensExactlyOnce(t *testing.T) {
	// 公布成功那条路上调用方照样会走到 defer，那一次必须什么都不做——不然一份已经
	// 被接手走的提供方状态会被还回去第二次。
	count := 0
	preparation := NewPreparation(nil, PreparationOptions{Release: func() { count++ }})
	preparation.Release()
	preparation.Release()
	preparation.Release()
	if count != 1 {
		t.Fatalf("释放跑了 %d 次", count)
	}
}

func TestConcurrentReleasesStillRunTheCallbackOnce(t *testing.T) {
	// 一段准备期可能被公布路径和一个 defer 两处同时碰到，所以一次性靠的是
	// sync.Once 而不是一个裸布尔。
	var mutex sync.Mutex
	count := 0
	preparation := NewPreparation(nil, PreparationOptions{Release: func() {
		mutex.Lock()
		defer mutex.Unlock()
		count++
	}})

	var group sync.WaitGroup
	for range 16 {
		group.Add(1)
		go func() {
			defer group.Done()
			preparation.Release()
		}()
	}
	group.Wait()
	if count != 1 {
		t.Fatalf("释放跑了 %d 次", count)
	}
}
