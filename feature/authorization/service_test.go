package authorization

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/credentials"
)

// TestBeginAuthorizesWhenFlowCommits 钉住一条正常跑完的流程报成功，而且槽位放开了。
func TestBeginAuthorizesWhenFlowCommits(t *testing.T) {
	h := newHarness(t)
	service := h.open("alpha")
	h.register(service, func(ctx context.Context, session Session) error {
		session.Notify(Notice{Message: "去这个页面", URL: "https://example.invalid", Code: "ABCD"})
		answer, err := session.Prompt(ctx, Prompt{Kind: PromptText, Message: "敲完了输个 ok"})
		if err != nil {
			return err
		}
		if answer != "ok" {
			t.Errorf("流程收到的答复该是 ok，实际 %q", answer)
		}
		h.creds.commit(testKey)
		return nil
	})

	interaction := &scriptedInteraction{answers: []string{"ok"}}
	outcome, err := service.Begin(t.Context(), Request{Key: testKey, Interaction: interaction})
	if err != nil {
		t.Fatalf("授权不该失败：%v", err)
	}
	if outcome.Status != StatusAuthorized {
		t.Fatalf("该是 authorized，实际 %q", outcome.Status)
	}
	if outcome.AttemptID == "" {
		t.Fatal("结果里该带这次尝试的身份")
	}
	if notices := interaction.heard(); len(notices) != 1 || notices[0].Code != "ABCD" {
		t.Fatalf("界面该收到那一条通告，实际 %+v", notices)
	}
	if _, found := h.attemptOn(service); found {
		t.Fatal("结算之后介质上不该还留着那条尝试")
	}

	entry, ok, err := service.Describe(t.Context(), testKey)
	if err != nil {
		t.Fatalf("描述不该失败：%v", err)
	}
	if !ok {
		t.Fatal("这条流程还登记着，该描述得出来")
	}
	if entry.InFlight || entry.Stalled {
		t.Fatalf("槽位该放开了，实际 %+v", entry)
	}
}

// TestBeginUsesFirstMethodWhenUnspecified 钉住不点名方式时用最推荐的那一种。
func TestBeginUsesFirstMethodWhenUnspecified(t *testing.T) {
	h := newHarness(t)
	service := h.open("alpha")
	var seen string
	h.register(service, func(context.Context, Session) error { return nil })
	// 重新登记一条能看见 method 的：上面那条已经占住了键，所以换一个服务。
	service2 := h.open("beta")
	_, err := service2.RegisterFlow(Flow{
		Key:     "demo/other",
		Label:   "另一条",
		Methods: []Method{{ID: "device", Label: "设备码"}, {ID: "paste", Label: "粘贴"}},
		Run: func(_ context.Context, session Session) error {
			seen = session.Method()
			h.creds.commit("demo/other")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("登记流程不该失败：%v", err)
	}
	if _, err := service2.Begin(t.Context(), Request{
		Key:         "demo/other",
		Interaction: &scriptedInteraction{},
	}); err != nil {
		t.Fatalf("授权不该失败：%v", err)
	}
	if seen != "device" {
		t.Fatalf("该用第一种方式 device，实际 %q", seen)
	}
	_ = service
}

// TestBeginRejectsUnknownMethod 钉住点名一个没提供的方式当场拒。
func TestBeginRejectsUnknownMethod(t *testing.T) {
	h := newHarness(t)
	service := h.open("alpha")
	h.register(service, func(context.Context, Session) error {
		t.Fatal("方式不认得的时候流程不该跑起来")
		return nil
	})
	_, err := service.Begin(t.Context(), Request{
		Key:         testKey,
		Method:      "carrier-pigeon",
		Interaction: &scriptedInteraction{},
	})
	if !errors.Is(err, CodeUnknownMethod) {
		t.Fatalf("该报 UNKNOWN_METHOD，实际 %v", err)
	}
}

// TestBeginRejectsUnclaimedKey 钉住没人认领的键当场拒。
func TestBeginRejectsUnclaimedKey(t *testing.T) {
	h := newHarness(t)
	service := h.open("alpha")
	_, err := service.Begin(t.Context(), Request{Key: "nobody/home", Interaction: &scriptedInteraction{}})
	if !errors.Is(err, CodeNoFlow) {
		t.Fatalf("该报 NO_FLOW，实际 %v", err)
	}
}

// TestRegisterFlowRejectsDuplicateKey 钉住一个键上只许有一条流程。
func TestRegisterFlowRejectsDuplicateKey(t *testing.T) {
	h := newHarness(t)
	service := h.open("alpha")
	h.register(service, func(context.Context, Session) error { return nil })
	_, err := service.RegisterFlow(Flow{
		Key:     testKey,
		Label:   "又一条",
		Methods: []Method{{ID: "paste", Label: "粘贴"}},
		Run:     func(context.Context, Session) error { return nil },
	})
	if !errors.Is(err, CodeDuplicateFlow) {
		t.Fatalf("该报 DUPLICATE_FLOW，实际 %v", err)
	}
}

// TestBeginReturnsCancelledForAlreadyDoneContext 钉住一个已经撤了的 ctx 不占槽位。
//
// 源: packages/credentials/authorization/src/index.ts:290-295
func TestBeginReturnsCancelledForAlreadyDoneContext(t *testing.T) {
	h := newHarness(t)
	service := h.open("alpha")
	h.register(service, func(context.Context, Session) error {
		t.Fatal("ctx 已经撤了的时候流程不该跑起来")
		return nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	outcome, err := service.Begin(ctx, Request{Key: testKey, Interaction: &scriptedInteraction{}})
	if err != nil {
		t.Fatalf("这条路不该报错：%v", err)
	}
	if outcome.Status != StatusCancelled {
		t.Fatalf("该是 cancelled，实际 %q", outcome.Status)
	}
	if _, found := h.attemptOn(service); found {
		t.Fatal("这条路不该在介质上留下记录")
	}
}

// TestDeclineSettlesAsCancelled 钉住人说不是取消，不是故障。
func TestDeclineSettlesAsCancelled(t *testing.T) {
	h := newHarness(t)
	service := h.open("alpha")
	var settled []Settled
	t.Cleanup(service.OnSettled(func(event Settled) { settled = append(settled, event) }))
	h.register(service, func(ctx context.Context, session Session) error {
		_, err := session.Prompt(ctx, Prompt{Kind: PromptSecret, Message: "把密钥贴进来"})
		return err
	})

	outcome, err := service.Begin(t.Context(), Request{
		Key:         testKey,
		Interaction: &scriptedInteraction{declined: true},
	})
	if err != nil {
		t.Fatalf("一次拒绝不该以错误交回来：%v", err)
	}
	if outcome.Status != StatusCancelled {
		t.Fatalf("该是 cancelled，实际 %q", outcome.Status)
	}
	if len(settled) != 1 || settled[0].Settlement != SettlementCancelled {
		t.Fatalf("旁观者该看到一次 cancelled，实际 %+v", settled)
	}
}

// TestFlowFailureSettlesAsFailed 钉住只有旁观者那条路上才有 failed。
func TestFlowFailureSettlesAsFailed(t *testing.T) {
	h := newHarness(t)
	service := h.open("alpha")
	var settled []Settled
	t.Cleanup(service.OnSettled(func(event Settled) { settled = append(settled, event) }))
	boom := errors.New("远端把门关了")
	h.register(service, func(context.Context, Session) error { return boom })

	_, err := service.Begin(t.Context(), Request{Key: testKey, Interaction: &scriptedInteraction{}})
	if !errors.Is(err, boom) {
		t.Fatalf("流程自己的错该原样交回来，实际 %v", err)
	}
	if len(settled) != 1 || settled[0].Settlement != SettlementFailed {
		t.Fatalf("旁观者该看到一次 failed，实际 %+v", settled)
	}
	if _, found := h.attemptOn(service); found {
		t.Fatal("失败也要放开槽位")
	}
}

// TestCleanRunWithoutCommitFails 钉住「跑完了但没写下去」不算成功。
//
// 源: packages/credentials/authorization/src/index.ts:425-431
func TestCleanRunWithoutCommitFails(t *testing.T) {
	h := newHarness(t)
	service := h.open("alpha")
	h.register(service, func(context.Context, Session) error { return nil })
	_, err := service.Begin(t.Context(), Request{Key: testKey, Interaction: &scriptedInteraction{}})
	if !errors.Is(err, CodeNotCommitted) {
		t.Fatalf("该报 NOT_COMMITTED，实际 %v", err)
	}
}

// TestCommitThatDisappearsFails 钉住第二道核实：尝试之后那条记录还得在。
func TestCommitThatDisappearsFails(t *testing.T) {
	h := newHarness(t)
	service := h.open("alpha")
	h.register(service, func(context.Context, Session) error {
		h.creds.commit(testKey)
		h.creds.forget(testKey)
		return nil
	})
	_, err := service.Begin(t.Context(), Request{Key: testKey, Interaction: &scriptedInteraction{}})
	if !errors.Is(err, CodeNotCommitted) {
		t.Fatalf("该报 NOT_COMMITTED，实际 %v", err)
	}
}

// TestCommitBeforeAttemptDoesNotCount 钉住订阅生效之前的提交不算这次尝试的功劳。
func TestCommitBeforeAttemptDoesNotCount(t *testing.T) {
	h := newHarness(t)
	h.creds.commit(testKey)
	service := h.open("alpha")
	h.register(service, func(context.Context, Session) error { return nil })
	_, err := service.Begin(t.Context(), Request{Key: testKey, Interaction: &scriptedInteraction{}})
	if !errors.Is(err, CodeNotCommitted) {
		t.Fatalf("一条早就配好的记录不该让流程白捡一次成功，实际 %v", err)
	}
}

// TestSecondBeginWhileFirstRuns 钉住同一时刻只许有一次尝试。
func TestSecondBeginWhileFirstRuns(t *testing.T) {
	h := newHarness(t)
	service := h.open("alpha")
	entered := make(chan struct{})
	release := make(chan struct{})
	h.register(service, func(context.Context, Session) error {
		close(entered)
		<-release
		h.creds.commit(testKey)
		return nil
	})

	var (
		wg       sync.WaitGroup
		outcome  Outcome
		firstErr error
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		outcome, firstErr = service.Begin(t.Context(), Request{Key: testKey, Interaction: &scriptedInteraction{}})
	}()
	<-entered

	_, err := service.Begin(t.Context(), Request{Key: testKey, Interaction: &scriptedInteraction{}})
	if !errors.Is(err, CodeAlreadyInFlight) {
		t.Fatalf("该报 ALREADY_IN_FLIGHT，实际 %v", err)
	}
	close(release)
	wg.Wait()
	if firstErr != nil || outcome.Status != StatusAuthorized {
		t.Fatalf("第一次该正常跑完，实际 %q / %v", outcome.Status, firstErr)
	}
}

// TestBeginRejectsStalledAttempt 钉住一次停着的尝试不许被 Begin 悄悄盖掉。
func TestBeginRejectsStalledAttempt(t *testing.T) {
	h := newHarness(t)
	alpha := h.open("alpha")
	h.register(alpha, func(ctx context.Context, session Session) error {
		if err := session.Checkpoint(ctx, json.RawMessage(`{"step":"waiting"}`)); err != nil {
			return err
		}
		// 演一次「进程在这儿没了」：不结算，直接把错交回去。
		return errStoppedForTest
	})
	if _, err := alpha.Begin(t.Context(), Request{Key: testKey, Interaction: &scriptedInteraction{}}); err == nil {
		t.Fatal("这次尝试该以错误收场")
	}
	// 上面那次结算掉了，所以手动摆一条停着的记录出来：直接写介质。
	now := h.now()
	if err := alpha.table.Create(t.Context(), string(testKey), Attempt{
		ID:        "stalled-1",
		Key:       testKey,
		Method:    "device",
		Holder:    "ghost",
		HeldUntil: now.Add(-time.Minute),
		StartedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("摆一条停着的记录不该失败：%v", err)
	}

	_, err := alpha.Begin(t.Context(), Request{Key: testKey, Interaction: &scriptedInteraction{}})
	if !errors.Is(err, CodeStalled) {
		t.Fatalf("该报 STALLED，实际 %v", err)
	}
	entry, _, err := alpha.Describe(t.Context(), testKey)
	if err != nil {
		t.Fatalf("描述不该失败：%v", err)
	}
	if !entry.Stalled || entry.InFlight {
		t.Fatalf("该显示成停着，实际 %+v", entry)
	}
	if entry.Attempt != "stalled-1" {
		t.Fatalf("该指认那次停着的尝试，实际 %q", entry.Attempt)
	}
}

// errStoppedForTest 是用例里演「进程在这儿没了」用的那个错。
var errStoppedForTest = errors.New("用例把进程掐了")

// TestResumePicksUpWhereItStopped 钉住方案要的那条验收：起一次、丢掉进程、
// 用新实例按 id 接上并跑完。
func TestResumePicksUpWhereItStopped(t *testing.T) {
	h := newHarness(t)

	// 第一个副本：留下脚印然后「没了」。
	alpha := h.open("alpha")
	h.register(alpha, func(ctx context.Context, session Session) error {
		if session.Resumed() != nil {
			t.Fatal("头一回跑不该读到脚印")
		}
		if err := session.Checkpoint(ctx, json.RawMessage(`{"verifier":"v-1"}`)); err != nil {
			return err
		}
		return errStoppedForTest
	})
	if _, err := alpha.Begin(t.Context(), Request{Key: testKey, Interaction: &scriptedInteraction{}}); !errors.Is(err, errStoppedForTest) {
		t.Fatalf("第一次该以那个错收场，实际 %v", err)
	}
	// 一次失败会结算并收走记录，所以这里把它按「进程真的没了」重新摆回去：
	// 脚印在，租约过期，没人攥着。
	stalled := Attempt{
		ID:        "attempt-1",
		Key:       testKey,
		Method:    "device",
		Holder:    "alpha",
		HeldUntil: h.now().Add(-time.Minute),
		Progress:  json.RawMessage(`{"verifier":"v-1"}`),
		StartedAt: h.now(),
		UpdatedAt: h.now(),
	}
	if err := alpha.table.Create(t.Context(), string(testKey), stalled); err != nil {
		t.Fatalf("摆回那条停着的记录不该失败：%v", err)
	}

	// 第二个副本：同一份介质，另一个身份。
	beta := h.open("beta")
	var resumed json.RawMessage
	h.register(beta, func(_ context.Context, session Session) error {
		resumed = session.Resumed()
		if session.Method() != "device" {
			t.Errorf("方式该跟着记录走，实际 %q", session.Method())
		}
		h.creds.commit(testKey)
		return nil
	})

	outcome, err := beta.Resume(t.Context(), ResumeRequest{
		Key:         testKey,
		AttemptID:   "attempt-1",
		Interaction: &scriptedInteraction{},
	})
	if err != nil {
		t.Fatalf("接手不该失败：%v", err)
	}
	if outcome.Status != StatusAuthorized {
		t.Fatalf("该是 authorized，实际 %q", outcome.Status)
	}
	if string(resumed) != `{"verifier":"v-1"}` {
		t.Fatalf("接手时该读回那份脚印，实际 %s", resumed)
	}
	if _, found := h.attemptOn(beta); found {
		t.Fatal("接手跑完之后该收走记录")
	}
}

// TestResumeRejectsMismatchedAttempt 钉住点名一个过期的身份接不上。
func TestResumeRejectsMismatchedAttempt(t *testing.T) {
	h := newHarness(t)
	service := h.open("alpha")
	h.register(service, func(context.Context, Session) error {
		t.Fatal("身份对不上的时候流程不该跑起来")
		return nil
	})
	now := h.now()
	if err := service.table.Create(t.Context(), string(testKey), Attempt{
		ID:        "current",
		Key:       testKey,
		Method:    "device",
		Holder:    "ghost",
		HeldUntil: now.Add(-time.Minute),
		StartedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("摆一条记录不该失败：%v", err)
	}
	_, err := service.Resume(t.Context(), ResumeRequest{
		Key:         testKey,
		AttemptID:   "stale",
		Interaction: &scriptedInteraction{},
	})
	if !errors.Is(err, CodeNoAttempt) {
		t.Fatalf("该报 NO_ATTEMPT，实际 %v", err)
	}
}

// TestResumeWithoutAttemptFails 钉住键上什么都没有的时候接不上。
func TestResumeWithoutAttemptFails(t *testing.T) {
	h := newHarness(t)
	service := h.open("alpha")
	h.register(service, func(context.Context, Session) error { return nil })
	_, err := service.Resume(t.Context(), ResumeRequest{Key: testKey, Interaction: &scriptedInteraction{}})
	if !errors.Is(err, CodeNoAttempt) {
		t.Fatalf("该报 NO_ATTEMPT，实际 %v", err)
	}
}

// TestTwoReplicasRaceForOneStalledAttempt 钉住方案要的另一条验收：
// 两个副本同时抢同一次停着的尝试，只有一个抢到。
func TestTwoReplicasRaceForOneStalledAttempt(t *testing.T) {
	h := newHarness(t)
	alpha := h.open("alpha")
	beta := h.open("beta")

	// 两个副本各自登记同一条流程；跑起来就卡住，好让「谁抢到了」看得见。
	entered := make(chan string, 2)
	release := make(chan struct{})
	run := func(replica string) func(context.Context, Session) error {
		return func(context.Context, Session) error {
			entered <- replica
			<-release
			h.creds.commit(testKey)
			return nil
		}
	}
	if _, err := alpha.RegisterFlow(Flow{
		Key: testKey, Label: "演示令牌",
		Methods: []Method{{ID: "device", Label: "设备码"}},
		Run:     run("alpha"),
	}); err != nil {
		t.Fatalf("登记不该失败：%v", err)
	}
	if _, err := beta.RegisterFlow(Flow{
		Key: testKey, Label: "演示令牌",
		Methods: []Method{{ID: "device", Label: "设备码"}},
		Run:     run("beta"),
	}); err != nil {
		t.Fatalf("登记不该失败：%v", err)
	}

	now := h.now()
	if err := alpha.table.Create(t.Context(), string(testKey), Attempt{
		ID:        "contested",
		Key:       testKey,
		Method:    "device",
		Holder:    "ghost",
		HeldUntil: now.Add(-time.Minute),
		StartedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("摆一条停着的记录不该失败：%v", err)
	}

	type result struct {
		outcome Outcome
		err     error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, service := range []*Service{alpha, beta} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outcome, err := service.Resume(t.Context(), ResumeRequest{
				Key:         testKey,
				AttemptID:   "contested",
				Interaction: &scriptedInteraction{},
			})
			results <- result{outcome: outcome, err: err}
		}()
	}

	// 抢到的那个会进 run 并卡住；抢输的那个会立刻拿到一个错。
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("等了五秒没人抢到")
	}
	first := <-results
	if first.err == nil {
		t.Fatalf("先回来的该是抢输的那个，实际拿到 %+v", first.outcome)
	}
	if !errors.Is(first.err, CodeAlreadyInFlight) {
		t.Fatalf("抢输的该报 ALREADY_IN_FLIGHT，实际 %v", first.err)
	}
	close(release)
	second := <-results
	if second.err != nil {
		t.Fatalf("抢到的那个该跑完，实际 %v", second.err)
	}
	if second.outcome.Status != StatusAuthorized {
		t.Fatalf("抢到的那个该是 authorized，实际 %q", second.outcome.Status)
	}
	wg.Wait()

	// 只有一个副本进过 run。
	select {
	case replica := <-entered:
		t.Fatalf("不该有第二个副本进 run，%q 也进来了", replica)
	default:
	}
}

// TestCancelStalledAttemptSettlesInPlace 钉住撤一次停着的尝试就地收干净。
func TestCancelStalledAttemptSettlesInPlace(t *testing.T) {
	h := newHarness(t)
	service := h.open("alpha")
	h.register(service, func(context.Context, Session) error { return nil })
	var settled []Settled
	t.Cleanup(service.OnSettled(func(event Settled) { settled = append(settled, event) }))

	now := h.now()
	if err := service.table.Create(t.Context(), string(testKey), Attempt{
		ID:        "stalled-1",
		Key:       testKey,
		Method:    "device",
		Holder:    "ghost",
		HeldUntil: now.Add(-time.Minute),
		StartedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("摆一条停着的记录不该失败：%v", err)
	}

	if err := service.Cancel(t.Context(), testKey, "stalled-1"); err != nil {
		t.Fatalf("撤不该失败：%v", err)
	}
	if _, found := h.attemptOn(service); found {
		t.Fatal("撤完之后介质上不该还留着那条尝试")
	}
	if len(settled) != 1 || settled[0].Settlement != SettlementCancelled {
		t.Fatalf("旁观者该看到一次 cancelled，实际 %+v", settled)
	}
	// 收干净了，所以下一次起得来。
	h.register2(service)
}

// TestCancelRunningAttemptStopsIt 钉住撤一次正跑着的尝试会把它叫停。
func TestCancelRunningAttemptStopsIt(t *testing.T) {
	h := newHarness(t)
	service := h.open("alpha")
	entered := make(chan struct{})
	h.register(service, func(ctx context.Context, session Session) error {
		close(entered)
		<-ctx.Done()
		// 撤销是写在介质上的，留脚印的时候读得回来。
		return session.Checkpoint(context.WithoutCancel(ctx), json.RawMessage(`{}`))
	})

	done := make(chan Outcome, 1)
	go func() {
		outcome, err := service.Begin(t.Context(), Request{Key: testKey, Interaction: &scriptedInteraction{}})
		if err != nil {
			t.Errorf("被撤的那次不该以错误交回来：%v", err)
		}
		done <- outcome
	}()
	<-entered

	if err := service.Cancel(t.Context(), testKey, ""); err != nil {
		t.Fatalf("撤不该失败：%v", err)
	}
	select {
	case outcome := <-done:
		if outcome.Status != StatusCancelled {
			t.Fatalf("该是 cancelled，实际 %q", outcome.Status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("等了五秒那次尝试还没停")
	}
	if _, found := h.attemptOn(service); found {
		t.Fatal("撤完之后介质上不该还留着那条尝试")
	}
}

// TestCancelWithoutAttemptFails 钉住键上什么都没有的时候撤不动。
func TestCancelWithoutAttemptFails(t *testing.T) {
	h := newHarness(t)
	service := h.open("alpha")
	if err := service.Cancel(t.Context(), testKey, ""); !errors.Is(err, CodeNoAttempt) {
		t.Fatalf("该报 NO_ATTEMPT，实际 %v", err)
	}
}

// TestListSortsByKey 钉住列举的次序是稳定的。
func TestListSortsByKey(t *testing.T) {
	h := newHarness(t)
	service := h.open("alpha")
	for _, key := range []credentials.Key{"z/last", "a/first", "m/middle"} {
		if _, err := service.RegisterFlow(Flow{
			Key: key, Label: string(key),
			Methods: []Method{{ID: "paste", Label: "粘贴"}},
			Run:     func(context.Context, Session) error { return nil },
		}); err != nil {
			t.Fatalf("登记不该失败：%v", err)
		}
	}
	entries, err := service.List(t.Context())
	if err != nil {
		t.Fatalf("列举不该失败：%v", err)
	}
	want := []credentials.Key{"a/first", "m/middle", "z/last"}
	if len(entries) != len(want) {
		t.Fatalf("该有 %d 条，实际 %d", len(want), len(entries))
	}
	for index, key := range want {
		if entries[index].Key != key {
			t.Fatalf("第 %d 条该是 %q，实际 %q", index, key, entries[index].Key)
		}
	}
}

// TestSettledListenerPanicIsContained 钉住一个炸了的旁观者不影响别的旁观者。
func TestSettledListenerPanicIsContained(t *testing.T) {
	h := newHarness(t)
	service := h.open("alpha")
	h.register(service, func(context.Context, Session) error {
		h.creds.commit(testKey)
		return nil
	})
	var reached bool
	t.Cleanup(service.OnSettled(func(Settled) { panic("旁观者炸了") }))
	t.Cleanup(service.OnSettled(func(Settled) { reached = true }))

	if _, err := service.Begin(t.Context(), Request{Key: testKey, Interaction: &scriptedInteraction{}}); err != nil {
		t.Fatalf("一个炸了的旁观者不该让这次尝试失败：%v", err)
	}
	if !reached {
		t.Fatal("后一个旁观者该照样收到")
	}
}

// TestNotifyPanicIsContained 钉住一个炸了的界面不算这次尝试出事。
func TestNotifyPanicIsContained(t *testing.T) {
	h := newHarness(t)
	service := h.open("alpha")
	h.register(service, func(_ context.Context, session Session) error {
		session.Notify(Notice{Message: "喂"})
		h.creds.commit(testKey)
		return nil
	})
	outcome, err := service.Begin(t.Context(), Request{
		Key:         testKey,
		Interaction: &explodingInteraction{},
	})
	if err != nil {
		t.Fatalf("界面炸了不该让这次尝试失败：%v", err)
	}
	if outcome.Status != StatusAuthorized {
		t.Fatalf("该是 authorized，实际 %q", outcome.Status)
	}
}

// explodingInteraction 是一个一说话就炸的界面。
type explodingInteraction struct{}

func (explodingInteraction) Notify(Notice) { panic("界面炸了") }
func (explodingInteraction) Prompt(context.Context, Prompt) (string, error) {
	panic("界面炸了")
}

// register2 再登记一条键不同的流程，用来确认服务还活着。
func (h *harness) register2(service *Service) {
	h.t.Helper()
	if _, err := service.RegisterFlow(Flow{
		Key: "demo/second", Label: "第二条",
		Methods: []Method{{ID: "paste", Label: "粘贴"}},
		Run:     func(context.Context, Session) error { return nil },
	}); err != nil {
		h.t.Fatalf("撤完之后还该能登记新流程：%v", err)
	}
}
