// 本文件验通知器：订阅、退订、分发次序，以及订阅者炸掉之后会发生什么。
//
// 这一层压的是「一次已经提交的变更，不会因为旁边有人看崩了就变成失败」。
// 提交已经发生在存储上了，此刻唯一还能出错的是分发本身——而分发出错的正确处理
// 只有一种：每个订阅者都跑到，失败记进日志，不改变那次提交的结果。
// 唯一的例外是不变量违例，它意味着程序写错了，必须传到发起方手里。

package credentials

import (
	"log/slog"
	"strings"
	"sync"
	"testing"

	"ds-harness-go/invariants"
)

// quiet 是一个把日志全丢掉的记录器，给不检查日志的用例用。
func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

// TestNewNotifierFallsBackToTheDefaultLogger 钉住 logger 留空时**不是**静音。
//
// 这里记的是「一个订阅者炸了但提交是好的」，正是没人会主动去查、却必须留下痕迹的
// 那类事（见 [NewNotifier]）。默认静音等于把它们藏起来。
func TestNewNotifierFallsBackToTheDefaultLogger(t *testing.T) {
	t.Parallel()

	notifier := NewNotifier(nil)
	if notifier.logger == nil {
		t.Fatal("logger 留空该回落到 slog.Default()，而不是留一个 nil")
	}
}

// TestSubscribingNilIsANoOp 钉住递 nil 监听器不会在分发时崩掉。
//
// 分发路径上一个 nil 回调会 panic 在**别人**那次提交里，而登记它的人早就返回了。
func TestSubscribingNilIsANoOp(t *testing.T) {
	t.Parallel()

	notifier := NewNotifier(quiet())
	notifier.SubscribeReference(nil)()
	notifier.SubscribeRecord(nil)()

	notifier.NotifyReferenceUpdated(Ref("DEEPSEEK_API_KEY"))
	notifier.NotifyRecordUpdated(Key("llm-pi-ai/openai-codex"))
}

// TestListenersRunInRegistrationOrder 钉住分发按登记顺序。
//
// 源: packages/credentials/credentials/src/index.ts:289-313
//
// 这一条值得单独钉：订阅表是切片而不是 map 正是为了它（见 [Notifier.SubscribeReference]）。
// 换成 map 之后用例仍然会「大部分时候通过」，而那种测试比没有测试更糟。
func TestListenersRunInRegistrationOrder(t *testing.T) {
	t.Parallel()

	notifier := NewNotifier(quiet())

	var mutex sync.Mutex
	var reached []string
	record := func(name string) func(Ref) {
		return func(Ref) {
			mutex.Lock()
			defer mutex.Unlock()
			reached = append(reached, name)
		}
	}

	t.Cleanup(notifier.SubscribeReference(record("一")))
	t.Cleanup(notifier.SubscribeReference(record("二")))
	t.Cleanup(notifier.SubscribeReference(record("三")))

	notifier.NotifyReferenceUpdated(Ref("DEEPSEEK_API_KEY"))

	mutex.Lock()
	defer mutex.Unlock()
	if len(reached) != 3 || reached[0] != "一" || reached[1] != "二" || reached[2] != "三" {
		t.Fatalf("该按登记顺序跑，实际 %v", reached)
	}
}

// TestUnsubscribeStopsDeliveryAndIsIdempotent 钉住退订，两套键空间各一遍。
//
// 幂等这一条不是锦上添花：退订函数会被存进各种 defer 和析构器里，多调一次很平常，
// 而一次「摘错别人」的退订会让另一个订阅者从此和存储不一致，且它永远不会知道。
func TestUnsubscribeStopsDeliveryAndIsIdempotent(t *testing.T) {
	t.Parallel()

	notifier := NewNotifier(quiet())

	var mutex sync.Mutex
	var refCount, recordCount, survivorCount int

	// 留一个不退订的，用来证明退订摘掉的是自己那一项而不是整张表。
	t.Cleanup(notifier.SubscribeReference(func(Ref) {
		mutex.Lock()
		defer mutex.Unlock()
		survivorCount++
	}))
	dropRef := notifier.SubscribeReference(func(Ref) {
		mutex.Lock()
		defer mutex.Unlock()
		refCount++
	})
	dropRecord := notifier.SubscribeRecord(func(Key) {
		mutex.Lock()
		defer mutex.Unlock()
		recordCount++
	})

	notifier.NotifyReferenceUpdated(Ref("A"))
	notifier.NotifyRecordUpdated(Key("s/i"))

	dropRef()
	dropRef() // 第二次是空操作
	dropRecord()
	dropRecord()

	notifier.NotifyReferenceUpdated(Ref("A"))
	notifier.NotifyRecordUpdated(Key("s/i"))

	mutex.Lock()
	defer mutex.Unlock()
	if refCount != 1 {
		t.Errorf("退订之后引用监听器不该再收到，实际收到 %d 次", refCount)
	}
	if recordCount != 1 {
		t.Errorf("退订之后记录监听器不该再收到，实际收到 %d 次", recordCount)
	}
	if survivorCount != 2 {
		t.Errorf("没退订的那个该两次都收到，实际 %d 次", survivorCount)
	}
}

// TestTheTwoKeySpacesDoNotCrossTalk 钉住两套键空间的分发互不串线。
//
// 源: packages/credentials/credentials/src/types.ts:61-88
//
// 两个事件分开而不是合成一个，是因为两套键空间的语法不相交（见 [Observer]）：
// 一个同时收到两者的监听器分不出手上这个主体属于哪一边。串线之后的症状是
// 一个引用名被当成记录地址去解析，而它一定解析不出来——错误会指向解析那一行。
func TestTheTwoKeySpacesDoNotCrossTalk(t *testing.T) {
	t.Parallel()

	notifier := NewNotifier(quiet())

	var mutex sync.Mutex
	var refs []Ref
	var keys []Key
	t.Cleanup(notifier.SubscribeReference(func(ref Ref) {
		mutex.Lock()
		defer mutex.Unlock()
		refs = append(refs, ref)
	}))
	t.Cleanup(notifier.SubscribeRecord(func(key Key) {
		mutex.Lock()
		defer mutex.Unlock()
		keys = append(keys, key)
	}))

	notifier.NotifyReferenceUpdated(Ref("DEEPSEEK_API_KEY"))
	notifier.NotifyRecordUpdated(Key("llm-pi-ai/openai-codex"))

	mutex.Lock()
	defer mutex.Unlock()
	if len(refs) != 1 || refs[0] != Ref("DEEPSEEK_API_KEY") {
		t.Errorf("引用订阅者该只收到那个引用，实际 %v", refs)
	}
	if len(keys) != 1 || keys[0] != Key("llm-pi-ai/openai-codex") {
		t.Errorf("记录订阅者该只收到那个地址，实际 %v", keys)
	}
}

// TestAPanickingListenerDoesNotStopTheOthers 钉住分发的第 1、2 条规则。
//
// 源: packages/credentials/credentials/src/index.ts:289-313
//
// 变更已经提交了，没跑到的订阅者从此和存储不一致，而它们永远不会知道。
// 两套键空间各走一遍：它们共用 [Notifier.fanOut]，但入口是两个，
// 只验一边的话，另一边漏掉兜底也照样通过。
func TestAPanickingListenerDoesNotStopTheOthers(t *testing.T) {
	t.Parallel()

	notifier := NewNotifier(quiet())

	var mutex sync.Mutex
	var reached []string
	record := func(name string) {
		mutex.Lock()
		defer mutex.Unlock()
		reached = append(reached, name)
	}

	t.Cleanup(notifier.SubscribeReference(func(Ref) { record("引用前"); panic("订阅者炸了") }))
	t.Cleanup(notifier.SubscribeReference(func(Ref) { record("引用后") }))
	t.Cleanup(notifier.SubscribeRecord(func(Key) { record("记录前"); panic("订阅者炸了") }))
	t.Cleanup(notifier.SubscribeRecord(func(Key) { record("记录后") }))

	notifier.NotifyReferenceUpdated(Ref("A"))
	notifier.NotifyRecordUpdated(Key("s/i"))

	mutex.Lock()
	defer mutex.Unlock()
	want := []string{"引用前", "引用后", "记录前", "记录后"}
	if len(reached) != len(want) {
		t.Fatalf("四个订阅者都该跑到，实际 %v", reached)
	}
	for index, name := range want {
		if reached[index] != name {
			t.Fatalf("该按 %v 的顺序跑到，实际 %v", want, reached)
		}
	}
}

// TestAnInvariantFailureIsRethrownAfterEveryListenerRan 钉住分发的第 3 条规则。
//
// 源: packages/credentials/credentials/src/index.ts:289-313
//
// 不变量违例意味着程序写错了，它必须传到发起方手里；但传出去之前，
// 其余订阅者仍然要各自跑到——它们和那个 bug 没关系。
func TestAnInvariantFailureIsRethrownAfterEveryListenerRan(t *testing.T) {
	t.Parallel()

	notifier := NewNotifier(quiet())

	var mutex sync.Mutex
	var reached []string
	record := func(name string) {
		mutex.Lock()
		defer mutex.Unlock()
		reached = append(reached, name)
	}

	first := &invariants.Error{PackageName: PackageName, Message: "第一条违例"}
	second := &invariants.Error{PackageName: PackageName, Message: "第二条违例"}
	t.Cleanup(notifier.SubscribeReference(func(Ref) { record("一"); panic(first) }))
	t.Cleanup(notifier.SubscribeReference(func(Ref) { record("二"); panic(second) }))
	t.Cleanup(notifier.SubscribeReference(func(Ref) { record("三") }))

	thrown := func() (recovered any) {
		defer func() { recovered = recover() }()
		notifier.NotifyReferenceUpdated(Ref("A"))
		return nil
	}()

	failure, ok := thrown.(*invariants.Error)
	if !ok {
		t.Fatalf("该重新抛出 *invariants.Error，实际 %#v", thrown)
	}
	if failure != first {
		// 只留第一条：后面的多半是同一个原因的连锁反应，抛最早的那个离现场最近。
		t.Fatalf("该抛第一条违例，实际 %q", failure.Message)
	}

	mutex.Lock()
	defer mutex.Unlock()
	if len(reached) != 3 {
		t.Fatalf("三个订阅者都该跑到，实际 %v", reached)
	}
}

// TestListenerFailuresAreLoggedWithEventAndSubject 钉住那条警告说得清是哪一次分发。
//
// 源: packages/credentials/credentials/src/index.ts:316-320
//
// 事件名加主体是这条诊断的全部内容，也是它能带的上限：主体是引用名或记录地址，
// 两者都不是秘密；再多就有把密钥写进日志的风险（见 [Notifier.warnListenerFailure]）。
func TestListenerFailuresAreLoggedWithEventAndSubject(t *testing.T) {
	t.Parallel()

	var buffer strings.Builder
	notifier := NewNotifier(slog.New(slog.NewTextHandler(&buffer, nil)))

	t.Cleanup(notifier.SubscribeReference(func(Ref) { panic("订阅者炸了") }))
	notifier.NotifyReferenceUpdated(Ref("DEEPSEEK_API_KEY"))

	line := buffer.String()
	if !strings.Contains(line, "credentials/reference-updated") {
		t.Errorf("该记下是哪个事件：%q", line)
	}
	if !strings.Contains(line, "DEEPSEEK_API_KEY") {
		t.Errorf("该记下是哪个主体：%q", line)
	}
}
