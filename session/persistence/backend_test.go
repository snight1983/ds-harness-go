// 本文件的作用：三样可选后端能力怎么问出来，尤其是「装进接口的 nil 指针」那道坑。
//
// 源: packages/session/session-persistence/src/coordinator.ts:146-213

package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/session"
)

func TestDefaultsAreTheDshValues(t *testing.T) {
	t.Parallel()

	if DefaultPreparedSessionCacheSize != 5 {
		t.Fatalf("准备好的会话缓存条数该是 5，实际 %d", DefaultPreparedSessionCacheSize)
	}
	if DefaultWriteBatchMaxDelay != 200*time.Millisecond {
		t.Fatalf("攒批窗口该是 200ms，实际 %v", DefaultWriteBatchMaxDelay)
	}
}

func TestOptionalFacetsAreAbsentOnAPlainBackend(t *testing.T) {
	t.Parallel()

	backend := Backend(plainStub{})

	if _, ok := Seekable(backend); ok {
		t.Fatalf("这个后端不能按 seq 寻址")
	}
	if _, ok := Locating(backend); ok {
		t.Fatalf("这个后端不认路")
	}
	if _, ok := Closable(backend); ok {
		t.Fatalf("这个后端没东西要收")
	}
	if _, ok := LocateWith(backend, testHeader(t, "s1")); ok {
		t.Fatalf("不认路的后端不该给出位置")
	}
}

func TestOptionalFacetsAreFoundWhenImplemented(t *testing.T) {
	t.Parallel()

	backend := Backend(&fullStub{locatingStub: locatingStub{path: "/logs/s1.jsonl"}})

	seekable, ok := Seekable(backend)
	if !ok {
		t.Fatalf("该认出能寻址")
	}
	if _, err := seekable.LoadStoredFrom(context.Background(), "s1", 3); err != nil {
		t.Fatalf("寻址读不该报错：%v", err)
	}

	if _, ok := Locating(backend); !ok {
		t.Fatalf("该认出认路")
	}
	closable, ok := Closable(backend)
	if !ok {
		t.Fatalf("该认出有东西要收")
	}
	if err := closable.Close(context.Background()); err != nil {
		t.Fatalf("收不掉：%v", err)
	}

	location, ok := LocateWith(backend, testHeader(t, "s1"))
	if !ok || location.Path != "/logs/s1.jsonl" {
		t.Fatalf("该给出位置：%v %#v", ok, location)
	}
}

func TestOptionalFacetsAskTheTypeNotTheValue(t *testing.T) {
	t.Parallel()

	// 这三个断言函数判的是**类型**能不能，不是**这个值**当下行不行。
	// 一个 (*T)(nil) 装进接口之后接口非 nil、断言照样成功——这是有意留着的：
	// 那样的后端连 Name 都调不动，问题在装配那一侧，在这里悄悄降级成
	// 「不能寻址」会让它一路走到远处才炸，而且换了条错误的路径。
	var typed *fullStub
	backend := Backend(typed)

	if backend == nil {
		t.Fatalf("前提就错了：装了类型化 nil 的接口不该等于 nil")
	}
	if _, ok := Seekable(backend); !ok {
		t.Fatalf("判的是类型，类型化 nil 也该说能寻址")
	}
	if _, ok := Locating(backend); !ok {
		t.Fatalf("判的是类型，类型化 nil 也该说认路")
	}
	if _, ok := Closable(backend); !ok {
		t.Fatalf("判的是类型，类型化 nil 也该说有东西要收")
	}
}

// fullStub 是一个三样可选能力都有的后端。
type fullStub struct {
	locatingStub
}

func (*fullStub) LoadStoredFrom(context.Context, session.SessionID, int) (StoredSuffix, error) {
	return StoredSuffix{}, nil
}

func (*fullStub) Close(context.Context) error { return nil }
