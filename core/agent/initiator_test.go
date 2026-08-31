// 本文件的作用：「这条调用链是哪个 agent 发起的」那四个函数——传下去、读回来、
// 断掉、以及要求必须有。

package agent

import (
	"context"
	"errors"
	"testing"
)

// TestCurrentInitiatorOnABareContext 一个从没设过发起者的 ctx 读出「没有」，
// 而不是一次失败。
func TestCurrentInitiatorOnABareContext(t *testing.T) {
	agent, present := CurrentInitiator(context.Background())
	if agent != nil || present {
		t.Fatalf("光秃秃的 ctx 上不该有发起者：%v %v", agent, present)
	}
}

// TestWithInitiatorCarriesTheAgent 派生出来的 ctx 上读得回那个 agent。
func TestWithInitiatorCarriesTheAgent(t *testing.T) {
	driver := newFakeAgent(t, "driver", nil)
	ctx := WithInitiator(context.Background(), driver)

	got, present := CurrentInitiator(ctx)
	if !present || got != Agent(driver) {
		t.Fatalf("读回来的发起者不对：%v %v", got, present)
	}
	// 派生只影响派生出来的那一支，父 ctx 一动不动。
	if _, inherited := CurrentInitiator(context.Background()); inherited {
		t.Fatal("父 ctx 不该被派生改动")
	}
}

// TestWithInitiatorNilIsWithoutInitiator 给 nil 就是「没有发起者」，那是它唯一
// 的表达。
func TestWithInitiatorNilIsWithoutInitiator(t *testing.T) {
	outer := WithInitiator(context.Background(), newFakeAgent(t, "outer", nil))
	if _, present := CurrentInitiator(WithInitiator(outer, nil)); present {
		t.Fatal("给 nil 该等价于断掉发起者")
	}
}

// TestWithoutInitiatorHidesAnInheritedOne 断掉的那一层要挡住继续往父 ctx 上找：
// 这正是那层包装存在的全部理由。
func TestWithoutInitiatorHidesAnInheritedOne(t *testing.T) {
	outer := WithInitiator(context.Background(), newFakeAgent(t, "outer", nil))
	if _, present := CurrentInitiator(WithoutInitiator(outer)); present {
		t.Fatal("断掉之后不该再读得到继承来的发起者")
	}
	// 断掉的只是那一支；外面那层还认它自己的发起者。
	if _, present := CurrentInitiator(outer); !present {
		t.Fatal("断掉一支不该动到外面那一层")
	}
}

// TestWithInitiatorRebindsAfterWithout 断掉之后还能再认一个新的。
func TestWithInitiatorRebindsAfterWithout(t *testing.T) {
	inner := newFakeAgent(t, "inner", nil)
	ctx := WithInitiator(
		WithoutInitiator(WithInitiator(context.Background(), newFakeAgent(t, "outer", nil))),
		inner,
	)
	got, present := CurrentInitiator(ctx)
	if !present || got.ID() != inner.ID() {
		t.Fatalf("最里面那次认领该赢：%v", got)
	}
}

// TestRequireInitiator 要求必须有的那一条：有就交出来，没有就报 ErrNoInitiator。
func TestRequireInitiator(t *testing.T) {
	driver := newFakeAgent(t, "driver", nil)
	got, err := RequireInitiator(WithInitiator(context.Background(), driver))
	if err != nil {
		t.Fatalf("有发起者时不该报错：%v", err)
	}
	if got.ID() != driver.ID() {
		t.Fatalf("交出来的发起者不对：%v", got.ID())
	}

	if _, err := RequireInitiator(context.Background()); !errors.Is(err, ErrNoInitiator) {
		t.Fatalf("该报 ErrNoInitiator，得到 %v", err)
	}
}
