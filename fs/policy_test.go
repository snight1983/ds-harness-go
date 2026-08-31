// 本文件验那三个策略通道的分发规则：两个决定是单槽的，观察是扇出的。
//
// 源: packages/fs/fs/src/index.ts:44-78
//
// DSH 侧没有这一组用例——那三件事在它那里是 cordis 声明的事件，
// 分发规则（waterfall 单槽、emit 扇出）由容器实现并由容器自己的测试覆盖。
// Go 这边规则落在 [Policy] 上，那么规则就得由这个包自己钉住。

package fs

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// aTarget 造一个身份完好的目标，省得每条用例都写一遍。
func aTarget() Target {
	return Target{TargetKey: TargetKey("file:1"), DisplayPath: "file.txt"}
}

// TestTheZeroPolicyDecidesNothingAndNotifiesNobody 钉住零值可用。
//
// 一张空订阅表上的分发不是一次失败，而是「没有人对这件事有意见」：
// 两个决定给出 nil（也就是无条件），观察静静地散给零个记录方。
func TestTheZeroPolicyDecidesNothingAndNotifiesNobody(t *testing.T) {
	t.Parallel()

	var policy Policy

	intent, err := policy.DecideWriteIntent(t.Context(), aTarget(), nil)
	if err != nil || intent != nil {
		t.Errorf("空表该给出无条件写：intent=%#v err=%v", intent, err)
	}

	editIntent, err := policy.DecideEditIntent(t.Context(), aTarget(), nil)
	if err != nil || editIntent != nil {
		t.Errorf("空表该给出无条件编辑：intent=%#v err=%v", editIntent, err)
	}

	if err := policy.NotifyObserved(aTarget(), Absent{}, nil); err != nil {
		t.Errorf("没有记录方不是一次失败：%v", err)
	}
}

// TestSubscribingNothingIsANoOp 钉住递 nil 进来不会在表里留下一个会炸的空位。
//
// 装配代码里出现 `policy.SubscribeWriteIntent(cfg.Decider)` 而 cfg.Decider 恰好
// 没配是很平常的事。往表里塞一个 nil 的话，下一次分发会在别人的写路径上崩。
func TestSubscribingNothingIsANoOp(t *testing.T) {
	t.Parallel()

	var policy Policy
	policy.SubscribeWriteIntent(nil)()
	policy.SubscribeEditIntent(nil)()
	policy.SubscribeObserved(nil)()

	if _, err := policy.DecideWriteIntent(t.Context(), aTarget(), nil); err != nil {
		t.Errorf("分发不该失败：%v", err)
	}
	if _, err := policy.DecideEditIntent(t.Context(), aTarget(), nil); err != nil {
		t.Errorf("分发不该失败：%v", err)
	}
	if err := policy.NotifyObserved(aTarget(), Absent{}, nil); err != nil {
		t.Errorf("分发不该失败：%v", err)
	}
}

// TestTheFirstDeciderWithAnAnswerOwnsTheDecision 钉住单槽这件事。
//
// 源: packages/fs/fs/src/index.ts:50-66
//
// 给出答案的那个之后，后面的**不再被问到**。合成是没有意义的：
// 两个守卫合起来不构成第三个守卫，它们只会互相覆盖。
func TestTheFirstDeciderWithAnAnswerOwnsTheDecision(t *testing.T) {
	t.Parallel()

	var policy Policy
	var asked []string

	policy.SubscribeWriteIntent(func(context.Context, Target, any) (WriteIntent, error) {
		asked = append(asked, "第一个")
		return nil, nil // 这次决定不归我。
	})
	policy.SubscribeWriteIntent(func(context.Context, Target, any) (WriteIntent, error) {
		asked = append(asked, "第二个")
		return CreateIfAbsent{}, nil
	})
	policy.SubscribeWriteIntent(func(context.Context, Target, any) (WriteIntent, error) {
		asked = append(asked, "第三个")
		return ReplaceIfVersion{Version: Version("v9")}, nil
	})

	intent, err := policy.DecideWriteIntent(t.Context(), aTarget(), nil)
	if err != nil {
		t.Fatalf("决定不该失败：%v", err)
	}
	if _, ok := intent.(CreateIfAbsent); !ok {
		t.Errorf("该是第二个给出的守卫，实际 %#v", intent)
	}
	if len(asked) != 2 {
		t.Errorf("给出答案之后不该继续问，实际问了 %v", asked)
	}
}

// TestTheDecisionOrderFollowsRegistrationOrder 钉住顺序是登记顺序。
//
// 源: packages/fs/fs/src/index.ts:50-58
//
// 订阅存在切片里而不是 map 里，就是为了这件事：「谁先被问到」直接决定了结果，
// 而 Go 的 map 迭代顺序是随机的——用 map 会让同一份装配每次跑出不同的守卫。
// 这条用例反过来跑一遍上一条：先登记的那个给出答案时，后登记的一个都不问。
func TestTheDecisionOrderFollowsRegistrationOrder(t *testing.T) {
	t.Parallel()

	var policy Policy
	second := false

	policy.SubscribeEditIntent(func(context.Context, Target, any) (*EditIntent, error) {
		return &EditIntent{Version: Version("先登记的")}, nil
	})
	policy.SubscribeEditIntent(func(context.Context, Target, any) (*EditIntent, error) {
		second = true
		return &EditIntent{Version: Version("后登记的")}, nil
	})

	intent, err := policy.DecideEditIntent(t.Context(), aTarget(), nil)
	if err != nil {
		t.Fatalf("决定不该失败：%v", err)
	}
	if intent == nil || intent.Version != Version("先登记的") {
		t.Errorf("该是先登记的那个说了算，实际 %#v", intent)
	}
	if second {
		t.Error("后登记的不该被问到")
	}
}

// TestADeciderFailureAbortsInsteadOfFallingBackToUnconditional 钉住这条不留余地的规则。
//
// 源: packages/fs/fs/src/index.ts:50-66
//
// 报错的那个可能正是本该给出守卫的人。接着往下问、或者退回无条件写的话，
// 那次写会**成功**，然后覆盖掉别人刚写的内容——一次本该被拒的操作
// 变成了一次静默的数据丢失。所以这里连「后面还有没有人」都不看。
func TestADeciderFailureAbortsInsteadOfFallingBackToUnconditional(t *testing.T) {
	t.Parallel()

	broken := errors.New("查不到这个执行体观察过什么")

	t.Run("写", func(t *testing.T) {
		t.Parallel()

		var policy Policy
		rescued := false

		policy.SubscribeWriteIntent(func(context.Context, Target, any) (WriteIntent, error) {
			return nil, broken
		})
		policy.SubscribeWriteIntent(func(context.Context, Target, any) (WriteIntent, error) {
			rescued = true
			return CreateIfAbsent{}, nil
		})

		intent, err := policy.DecideWriteIntent(t.Context(), aTarget(), nil)
		if !errors.Is(err, broken) {
			t.Fatalf("该原样报出那个错，实际 %v", err)
		}
		if intent != nil {
			t.Errorf("报错时不该给出任何守卫，实际 %#v", intent)
		}
		if rescued {
			t.Error("报错之后不该继续问后面的人")
		}
	})

	t.Run("编辑", func(t *testing.T) {
		t.Parallel()

		var policy Policy
		rescued := false

		policy.SubscribeEditIntent(func(context.Context, Target, any) (*EditIntent, error) {
			return nil, broken
		})
		policy.SubscribeEditIntent(func(context.Context, Target, any) (*EditIntent, error) {
			rescued = true
			return &EditIntent{Version: Version("v1")}, nil
		})

		intent, err := policy.DecideEditIntent(t.Context(), aTarget(), nil)
		if !errors.Is(err, broken) {
			t.Fatalf("该原样报出那个错，实际 %v", err)
		}
		if intent != nil {
			t.Errorf("报错时不该给出任何守卫，实际 %#v", intent)
		}
		if rescued {
			t.Error("报错之后不该继续问后面的人")
		}
	})
}

// TestTheActorIsPassedThroughUntouched 钉住 actor 对这条接缝是不透明的。
//
// 源: packages/fs/fs/src/index.ts:50-76
//
// 决定方拿它当键去查「这个执行体观察过这个目标吗」。这条接缝对它一无所知，
// 也不该知道——它一旦开始解释 actor，就把某一种执行上下文的形状焊死在这里了。
func TestTheActorIsPassedThroughUntouched(t *testing.T) {
	t.Parallel()

	var policy Policy
	type executionContext struct{ id int }
	actor := &executionContext{id: 7}

	var seen any
	policy.SubscribeWriteIntent(func(_ context.Context, _ Target, got any) (WriteIntent, error) {
		seen = got
		return CreateIfAbsent{}, nil
	})

	if _, err := policy.DecideWriteIntent(t.Context(), aTarget(), actor); err != nil {
		t.Fatalf("决定不该失败：%v", err)
	}
	if seen != any(actor) {
		t.Errorf("该原样递过去，实际 %#v", seen)
	}
}

// TestObservationsFanOutToEveryRecorderAndReportTheFirstFailure 钉住扇出。
//
// 源: packages/fs/fs/src/index.ts:67-76
//
// 和两个决定不同，这里**每一个记录方都会被调到**，即使前面某一个报了错——
// 这次观察是一件已经发生的事实，一个记录方失败不该让其余几个从此缺一条记录，
// 而它们永远不会知道自己缺了。
//
// 返回的是**第一个**错误：它必须被调用方接住并让这次工具调用失败，
// 否则后面那次带守卫的写会以 [CodeNotObserved] 被拒，而现场早就过去了。
func TestObservationsFanOutToEveryRecorderAndReportTheFirstFailure(t *testing.T) {
	t.Parallel()

	var policy Policy
	first := errors.New("第一个记录方炸了")
	second := errors.New("第二个记录方也炸了")
	var reached []string

	policy.SubscribeObserved(func(Target, Observation, any) error {
		reached = append(reached, "一")
		return first
	})
	policy.SubscribeObserved(func(Target, Observation, any) error {
		reached = append(reached, "二")
		return second
	})
	policy.SubscribeObserved(func(Target, Observation, any) error {
		reached = append(reached, "三")
		return nil
	})

	err := policy.NotifyObserved(aTarget(), Present{Version: Version("v1")}, nil)
	if !errors.Is(err, first) {
		t.Errorf("该报出第一个错，实际 %v", err)
	}
	if len(reached) != 3 {
		t.Errorf("三个记录方都该被调到，实际 %v", reached)
	}
}

// TestAnObservationReachesRecordersWithBothItsArms 钉住两种观察都原样送到。
//
// 不在场的观察只授权一次带守卫的创建，永远不授权一次编辑——
// 记录方要能分开这两种，它就必须原样收到那个值。
func TestAnObservationReachesRecordersWithBothItsArms(t *testing.T) {
	t.Parallel()

	var policy Policy
	var seen []Observation

	policy.SubscribeObserved(func(_ Target, observation Observation, _ any) error {
		seen = append(seen, observation)
		return nil
	})

	if err := policy.NotifyObserved(aTarget(), Present{Version: Version("v1")}, nil); err != nil {
		t.Fatalf("分发不该失败：%v", err)
	}
	if err := policy.NotifyObserved(aTarget(), Absent{}, nil); err != nil {
		t.Fatalf("分发不该失败：%v", err)
	}

	if len(seen) != 2 {
		t.Fatalf("该收到两条，实际 %d 条", len(seen))
	}
	if version, ok := seen[0].PresentVersion(); !ok || version != Version("v1") {
		t.Errorf("第一条该是带 v1 的在场观察，实际 %#v", seen[0])
	}
	if _, ok := seen[1].PresentVersion(); ok {
		t.Errorf("第二条该是不在场观察，实际 %#v", seen[1])
	}
}

// TestUnsubscribingTakesTheSubscriberOutOfTheTable 钉住退订真的摘得掉。
//
// 三张表各验一次：摘不掉的话，一个已经收摊的插件会继续在别人的路径上
// 给出守卫、或者继续收观察记录。
func TestUnsubscribingTakesTheSubscriberOutOfTheTable(t *testing.T) {
	t.Parallel()

	var policy Policy
	called := 0

	dropWrite := policy.SubscribeWriteIntent(func(context.Context, Target, any) (WriteIntent, error) {
		called++
		return CreateIfAbsent{}, nil
	})
	dropEdit := policy.SubscribeEditIntent(func(context.Context, Target, any) (*EditIntent, error) {
		called++
		return &EditIntent{Version: Version("v1")}, nil
	})
	dropObserve := policy.SubscribeObserved(func(Target, Observation, any) error {
		called++
		return nil
	})

	dropWrite()
	dropEdit()
	dropObserve()

	if _, err := policy.DecideWriteIntent(t.Context(), aTarget(), nil); err != nil {
		t.Fatalf("分发不该失败：%v", err)
	}
	if _, err := policy.DecideEditIntent(t.Context(), aTarget(), nil); err != nil {
		t.Fatalf("分发不该失败：%v", err)
	}
	if err := policy.NotifyObserved(aTarget(), Absent{}, nil); err != nil {
		t.Fatalf("分发不该失败：%v", err)
	}

	if called != 0 {
		t.Errorf("退订之后一个都不该被调到，实际调了 %d 次", called)
	}
}

// TestUnsubscribingTwiceIsHarmless 钉住重复退订不会误伤别人。
//
// 退订函数经常被塞进 defer 又被显式调一次。第二次调用要是按位置去删，
// 删掉的会是那之后登记的另一个人的订阅。
func TestUnsubscribingTwiceIsHarmless(t *testing.T) {
	t.Parallel()

	var policy Policy
	survivor := false

	drop := policy.SubscribeWriteIntent(func(context.Context, Target, any) (WriteIntent, error) {
		return nil, nil
	})
	drop()

	policy.SubscribeWriteIntent(func(context.Context, Target, any) (WriteIntent, error) {
		survivor = true
		return CreateIfAbsent{}, nil
	})
	drop() // 第二次，此时表里只剩别人。

	if _, err := policy.DecideWriteIntent(t.Context(), aTarget(), nil); err != nil {
		t.Fatalf("分发不该失败：%v", err)
	}
	if !survivor {
		t.Error("重复退订不该把别人的订阅一起摘掉")
	}
}

// TestTheTablesSurviveConcurrentSubscriptionAndDispatch 钉住那把锁真的在守着。
//
// 新增: DSH 是单线程 JS，订阅表不需要任何并发保护。Go 里订阅、退订、分发
// 会来自不同的 goroutine，这一层是 Go 侧的必需品——所以它也得有一条用例。
// 这条用例的价值在 -race 下才完全兑现。
func TestTheTablesSurviveConcurrentSubscriptionAndDispatch(t *testing.T) {
	t.Parallel()

	var policy Policy
	var waiting sync.WaitGroup

	for range 8 {
		waiting.Add(2)

		go func() {
			defer waiting.Done()
			drop := policy.SubscribeObserved(func(Target, Observation, any) error { return nil })
			drop()
		}()

		go func() {
			defer waiting.Done()
			if err := policy.NotifyObserved(aTarget(), Absent{}, nil); err != nil {
				t.Errorf("分发不该失败：%v", err)
			}
		}()
	}

	waiting.Wait()
}
