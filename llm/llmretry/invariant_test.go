// 本文件验本包自己拥有的那几条持久不变量：一条重试落在哪里、接着哪条链往下数、
// 那条链的身份归谁，以及一条 llm/retry-started 必须找得到它配对的那次排期。
package llmretry

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
)

// rejects 把一段日志走一遍，断言它被拒并交出那句诊断。
func rejects(t *testing.T, events []session.Event) string {
	t.Helper()

	_, err := ValidateLog(events)
	if !errors.Is(err, ErrInvariantViolated) {
		t.Fatalf("该被拒并认得出哨兵：%v", err)
	}
	return err.Error()
}

// rejectsWith 在 [rejects] 之上再断言诊断里提到了什么。
func rejectsWith(t *testing.T, events []session.Event, want string) {
	t.Helper()

	if message := rejects(t, events); !strings.Contains(message, want) {
		t.Errorf("诊断里该提到 %q：%s", want, message)
	}
}

// TestAWholeChainValidates 钉住一条正常走完的链整段都过得去。
//
// 源: packages/llm/llm-retry/src/invariant.ts:26-171
func TestAWholeChainValidates(t *testing.T) {
	t.Parallel()

	first := normalRetry("r-1", 1, 3)
	second := normalRetry("r-1", 2, 3)
	events := append(openStepLog(t, "甲"),
		retryEvent(t, 4, first),
		startedEvent(t, 5, startedFor(first)),
		retryEvent(t, 6, second),
		startedEvent(t, 7, startedFor(second)),
	)
	if _, err := ValidateLog(events); err != nil {
		t.Fatalf("这条链该整段过得去：%v", err)
	}
}

// TestARetryOutsideAnyOpenTurnIsRejected 钉住回合外的重试被拒。
//
// 源: packages/llm/llm-retry/src/invariant.ts（那段 last-boundary 检查）
func TestARetryOutsideAnyOpenTurnIsRejected(t *testing.T) {
	t.Parallel()

	rejectsWith(t, []session.Event{retryEvent(t, 1, normalRetry("r-1", 1, 3))}, "开着的回合之外")
}

// TestARetryNamingAnotherTurnIsRejected 钉住说的回合和开着的对不上时被拒。
func TestARetryNamingAnotherTurnIsRejected(t *testing.T) {
	t.Parallel()

	data := normalRetry("r-1", 1, 3)
	data.Turn = 2
	rejectsWith(t, append(openStepLog(t, "甲"), retryEvent(t, 4, data)), "开着的却是回合 1")
}

// TestARetryAfterTheStepClosedIsRejected 钉住步骤收掉之后的重试被拒。
func TestARetryAfterTheStepClosedIsRejected(t *testing.T) {
	t.Parallel()

	events := append(openStepLog(t, "甲"),
		stepEnd(t, 4, 1, 1),
		retryEvent(t, 5, normalRetry("r-1", 1, 3)),
	)
	rejectsWith(t, events, "开着的步骤之外")
}

// TestARetryNamingAnotherStepIsRejected 钉住说的步骤和开着的对不上时被拒。
func TestARetryNamingAnotherStepIsRejected(t *testing.T) {
	t.Parallel()

	events := append(openStepLog(t, "甲"),
		stepEnd(t, 4, 1, 1),
		stepStart(t, 5, 1, 2),
		retryEvent(t, 6, normalRetry("r-1", 1, 3)),
	)
	rejectsWith(t, events, "开着的却是步骤 1/2")
}

// TestARetryReportingAnotherProviderIsRejected 钉住提供方对不上时被拒。
//
// 源: packages/llm/llm-retry/src/invariant.ts（那句 providerForOpenStep 比较）
//
// 这条守的是「这串重试次数记在谁头上」：记错了人的话，一个从没失败过的提供方会
// 带着别人的账很快被判成重够了。
func TestARetryReportingAnotherProviderIsRejected(t *testing.T) {
	t.Parallel()

	data := normalRetry("r-1", 1, 3)
	data.Provider = "乙"
	rejectsWith(t, append(openStepLog(t, "甲"), retryEvent(t, 4, data)), "路由到的是")
}

// TestARetryOnAStepWithoutARoutedProviderIsRejected 钉住步骤压根没路由出去时被拒。
func TestARetryOnAStepWithoutARoutedProviderIsRejected(t *testing.T) {
	t.Parallel()

	events := []session.Event{
		turnStart(t, 1, 1),
		stepStart(t, 2, 1, 1),
		retryEvent(t, 3, normalRetry("r-1", 1, 3)),
	}
	rejectsWith(t, events, "没有路由出去的提供方")
}

// TestRetryNumbersMustFollowTheChain 钉住序号必须接着这条链往下数。
//
// 源: packages/llm/llm-retry/src/invariant.ts（那句 findLast + retry 比较）
//
// 跳号意味着中间那次排期没落库。放过去的话，「这个步骤发过几次请求」在重放时
// 会少算一次，而那次请求是真的发出去过、也真的花了钱的。
func TestRetryNumbersMustFollowTheChain(t *testing.T) {
	t.Parallel()

	rejectsWith(t,
		append(openStepLog(t, "甲"), retryEvent(t, 4, normalRetry("r-1", 2, 3))),
		"这条链上一次是第 0 次")
}

// TestChangingThePolicyKeyStartsAFreshChain 钉住换了策略就是另一条链，从 1 重新数。
//
// 源: packages/llm/llm-retry/src/invariant.ts（那句 findLast 的四个比较项）
//
// 接着上一份策略的账往下数的话，一次把 maxRetries 从 2 调到 5 的改动会让新策略
// 一上来就被算成「已经重了 2 次」。
func TestChangingThePolicyKeyStartsAFreshChain(t *testing.T) {
	t.Parallel()

	first := normalRetry("r-1", 1, 3)
	second := normalRetry("r-2", 1, 3)
	second.PolicyKey = "k-2"
	events := append(openStepLog(t, "甲"),
		retryEvent(t, 4, first),
		retryEvent(t, 5, second),
	)
	if _, err := ValidateLog(events); err != nil {
		t.Fatalf("换了策略该另起一条链：%v", err)
	}

	// 而接着上一条链的号往下数是不行的。
	continued := normalRetry("r-2", 2, 3)
	continued.PolicyKey = "k-2"
	rejectsWith(t,
		append(openStepLog(t, "甲"), retryEvent(t, 4, first), retryEvent(t, 5, continued)),
		"这条链上一次是第 0 次")
}

// TestANewChainCannotBorrowAUsedIdentity 钉住新链不许借一个已经出现过的身份。
//
// 借了的话，llm/retry-started 那一边就再也认不出它配对的是哪一次排期——
// 那条配对靠的正是 retryId 加序号。
func TestANewChainCannotBorrowAUsedIdentity(t *testing.T) {
	t.Parallel()

	borrowed := normalRetry("r-1", 1, 3)
	borrowed.PolicyKey = "k-2"
	events := append(openStepLog(t, "甲"),
		retryEvent(t, 4, normalRetry("r-1", 1, 3)),
		retryEvent(t, 5, borrowed),
	)
	rejectsWith(t, events, "已经被用过")
}

// TestAChainCannotChangeItsIdentity 钉住同一条链上的身份不许中途换掉。
func TestAChainCannotChangeItsIdentity(t *testing.T) {
	t.Parallel()

	events := append(openStepLog(t, "甲"),
		retryEvent(t, 4, normalRetry("r-1", 1, 3)),
		retryEvent(t, 5, normalRetry("r-2", 2, 3)),
	)
	rejectsWith(t, events, "换了链身份")
}

// TestANormalRetryCannotExceedItsMaxRetries 钉住越过上限的那条事件本身就被拒。
//
// 策略说最多重 2 次、日志里却躺着第 3 次，说明要么排期那一边算错了账，要么有人
// 把两条链的事件混进了同一条。当场喊出来，因为再往后就只剩下一次多花的请求。
func TestANormalRetryCannotExceedItsMaxRetries(t *testing.T) {
	t.Parallel()

	data := normalRetry("r-1", 4, 3)
	rejectsWith(t, append(openStepLog(t, "甲"), retryEvent(t, 4, data)), "策略只允许 3 次")
}

// TestTheModeAndMaxRetriesMustAgree 钉住档位和 maxRetries 对不上时被拒。
//
// 这几种形状 [RetryData.MarshalJSON] 排不出去（见 types_test.go），所以只能手写
// 介质上的样子——而它们**读得回来**正是为了让这条不变量指得出毛病在哪。
func TestTheModeAndMaxRetriesMustAgree(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		payload string
		want    string
	}{
		"always 档带了上限": {
			payload: `{"retryId":"r-1","turn":1,"step":1,"provider":"甲","mode":"always",` +
				`"policyKey":"k-1","retry":1,"maxRetries":3,"delayMs":500,` +
				`"failure":{"message":"上游超时了","code":"TIMEOUT"}}`,
			want: "不能带 maxRetries",
		},
		"normal 档没写上限": {
			payload: `{"retryId":"r-1","turn":1,"step":1,"provider":"甲","mode":"normal",` +
				`"policyKey":"k-1","retry":1,"delayMs":500,` +
				`"failure":{"message":"上游超时了","code":"TIMEOUT"}}`,
			want: "却没写 maxRetries",
		},
		"normal 档一次都不许重": {
			payload: `{"retryId":"r-1","turn":1,"step":1,"provider":"甲","mode":"normal",` +
				`"policyKey":"k-1","retry":1,"maxRetries":0,"delayMs":500,` +
				`"failure":{"message":"上游超时了","code":"TIMEOUT"}}`,
			want: "至少要允许重 1 次",
		},
		"不认得的档位": {
			payload: `{"retryId":"r-1","turn":1,"step":1,"provider":"甲","mode":"偶尔",` +
				`"policyKey":"k-1","retry":1,"delayMs":500,` +
				`"failure":{"message":"上游超时了","code":"TIMEOUT"}}`,
			want: "只能是",
		},
		"负的延时": {
			payload: `{"retryId":"r-1","turn":1,"step":1,"provider":"甲","mode":"normal",` +
				`"policyKey":"k-1","retry":1,"maxRetries":3,"delayMs":-1,` +
				`"failure":{"message":"上游超时了","code":"TIMEOUT"}}`,
			want: "不能是负的",
		},
	}
	for label, testCase := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			events := append(openStepLog(t, "甲"), rawEvent(4, EventRetry, testCase.payload))
			rejectsWith(t, events, testCase.want)
		})
	}
}

// TestARetryMustCarryItsOwnIdentifiers 钉住那几个认链认账的字段一个都不能缺。
func TestARetryMustCarryItsOwnIdentifiers(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		mutate func(*RetryData)
		want   string
	}{
		"没有链身份":   {func(d *RetryData) { d.RetryID = "" }, "没有链身份"},
		"没有提供方":   {func(d *RetryData) { d.Provider = "" }, "没有提供方"},
		"没有策略指纹":  {func(d *RetryData) { d.PolicyKey = "" }, "没有策略指纹"},
		"序号从 0 起": {func(d *RetryData) { d.Retry = 0 }, "重试序号从 1 起"},
	}
	for label, testCase := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			data := normalRetry("r-1", 1, 3)
			testCase.mutate(&data)
			rejectsWith(t, append(openStepLog(t, "甲"), retryEvent(t, 4, data)), testCase.want)
		})
	}
}

// TestTheFailureShapeIsChecked 钉住那份规整过的失败也要验一遍形状。
//
// 源: packages/llm/llm-retry/src/invariant.ts（validateFailure）
//
// 一条没有失败码的重试在重放时说不出「为什么重」，而下一次是否该重正是按失败码
// 判的（见 [installation.recover] 那句 RetryableCodes 比较）。
func TestTheFailureShapeIsChecked(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		failure llm.Failure
		want    string
	}{
		"没有描述":  {llm.Failure{Code: "TIMEOUT"}, "没有描述"},
		"没有失败码": {llm.Failure{Message: "上游超时了"}, "没有失败码"},
		"状态码不是状态码": {
			llm.Failure{Message: "上游超时了", Code: "TIMEOUT", Status: 42},
			"不是一个 HTTP 状态码",
		},
		"点名要等的时间是负的": {
			llm.Failure{Message: "上游超时了", Code: "TIMEOUT", ProviderRetryAfterMs: -1},
			"不能是负的",
		},
	}
	for label, testCase := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			data := normalRetry("r-1", 1, 3)
			data.Failure = testCase.failure
			rejectsWith(t, append(openStepLog(t, "甲"), retryEvent(t, 4, data)), testCase.want)
		})
	}
}

// TestAnHTTPStatusAtTheEdgesIsAccepted 钉住 100 和 599 这两头是收的。
//
// 边界写反的话（比如写成 `< 100 || >= 599`），一条带着 599 的真实失败会被判成违例，
// 而那种日志装载时会整个被拒——症状是「这个会话打不开」，离毛病很远。
func TestAnHTTPStatusAtTheEdgesIsAccepted(t *testing.T) {
	t.Parallel()

	for _, status := range []int{100, 599} {
		data := normalRetry("r-1", 1, 3)
		data.Failure.Status = status
		if _, err := ValidateLog(append(openStepLog(t, "甲"), retryEvent(t, 4, data))); err != nil {
			t.Errorf("状态码 %d 该收下：%v", status, err)
		}
	}
}

// TestAStartedWithoutItsScheduleIsRejected 钉住找不到配对排期的 llm/retry-started 被拒。
//
// 源: packages/llm/llm-retry/src/invariant.ts:142-171
func TestAStartedWithoutItsScheduleIsRejected(t *testing.T) {
	t.Parallel()

	events := append(openStepLog(t, "甲"),
		startedEvent(t, 4, startedFor(normalRetry("r-1", 1, 3))),
	)
	rejectsWith(t, events, "没有配对的")
}

// TestAStartedOnAnotherStepIsRejected 钉住配对上了但步骤对不上时被拒。
func TestAStartedOnAnotherStepIsRejected(t *testing.T) {
	t.Parallel()

	data := normalRetry("r-1", 1, 3)
	started := startedFor(data)
	started.Step = 2
	events := append(openStepLog(t, "甲"),
		retryEvent(t, 4, data),
		startedEvent(t, 5, started),
	)
	rejectsWith(t, events, "排在步骤 1/1 上")
}

// TestAScheduleIsOnlyStartedOnce 钉住同一次排期只熬得过去一次。
//
// 两条的话，「这个步骤发过几次请求」会多算一次——而那次请求根本没发生过。
func TestAScheduleIsOnlyStartedOnce(t *testing.T) {
	t.Parallel()

	data := normalRetry("r-1", 1, 3)
	events := append(openStepLog(t, "甲"),
		retryEvent(t, 4, data),
		startedEvent(t, 5, startedFor(data)),
		startedEvent(t, 6, startedFor(data)),
	)
	rejectsWith(t, events, "那段等待只熬得过去一次")
}

// TestAStartedWithoutAnIdentityIsRejected 钉住没有链身份的 llm/retry-started 被拒。
func TestAStartedWithoutAnIdentityIsRejected(t *testing.T) {
	t.Parallel()

	data := normalRetry("r-1", 1, 3)
	started := startedFor(data)
	started.RetryID = ""
	events := append(openStepLog(t, "甲"),
		retryEvent(t, 4, data),
		startedEvent(t, 5, started),
	)
	rejectsWith(t, events, "没有链身份")
}

// TestAnUnreadableRetryPayloadIsAViolation 钉住读不回来的负载报成违例，不是别的。
func TestAnUnreadableRetryPayloadIsAViolation(t *testing.T) {
	t.Parallel()

	message := rejects(t, []session.Event{rawEvent(1, EventRetry, `{"retry":"一"}`)})
	if !strings.Contains(message, "1") {
		t.Errorf("诊断该指得出是哪一条：%s", message)
	}
}

// TestAnEmptyLogIsValid 钉住空日志走得通，交出一条空轨迹。
func TestAnEmptyLogIsValid(t *testing.T) {
	t.Parallel()

	trace, err := ValidateLog(nil)
	if err != nil || trace == nil {
		t.Fatalf("空日志该走得通：trace=%v err=%v", trace, err)
	}
}

// TestValidateLeavesTheTraceAlone 钉住 [Trace.Validate] 不动这条轨迹。
//
// 源: packages/llm/llm-retry/src/invariant.ts:52-56
//
// 验和改分成两步，为的是让一条**被拒**的事件不在轨迹上留下任何痕迹。混在一起的话,
// 一次失败的追加会把链的序号推上去，之后那条真正合法的重试反而会被判成跳号。
func TestValidateLeavesTheTraceAlone(t *testing.T) {
	t.Parallel()

	trace, err := ValidateLog(openStepLog(t, "甲"))
	if err != nil {
		t.Fatalf("前缀该走得通：%v", err)
	}

	event := retryEvent(t, 4, normalRetry("r-1", 1, 3))
	if _, err := trace.Validate(event); err != nil {
		t.Fatalf("第一次该过：%v", err)
	}
	if _, err := trace.Validate(event); err != nil {
		t.Fatalf("同一条再验一次该给一样的答案：%v", err)
	}

	// 一条被拒的事件也不该留下痕迹：拒掉之后，那条合法的第 1 次仍然过得去。
	if _, err := trace.Validate(retryEvent(t, 4, normalRetry("r-1", 9, 3))); err == nil {
		t.Fatal("跳号的那条该被拒")
	}
	if _, err := trace.Validate(event); err != nil {
		t.Fatalf("被拒的那条不该动到轨迹：%v", err)
	}
}

// TestRegisteredInvariantsFireOnAlreadyLoadedHistory 钉住装的时候就把历史走一遍。
//
// 源: packages/llm/llm-retry/src/invariant.ts:173-174
//
// 一份历史里就带着拆了对的重试的会话，必须在装载这一刻响；等到下一条事件才响的话，
// 一个再也不会被写入的会话可以永远躲过检查。
func TestRegisteredInvariantsFireOnAlreadyLoadedHistory(t *testing.T) {
	t.Parallel()

	harness := newRetryInvariantHarness(t,
		append(openStepLog(t, "甲"), retryEvent(t, 4, normalRetry("r-1", 2, 3)))...)
	failure := retryViolation(t, func() { harness.register(t) })
	if !strings.Contains(failure.Error(), "这条链上一次是第 0 次") {
		t.Errorf("该报那条跳号：%v", failure)
	}
}

// TestRegisteredInvariantsFireOnLaterEvents 钉住后来追加的事件也走这套检查。
func TestRegisteredInvariantsFireOnLaterEvents(t *testing.T) {
	t.Parallel()

	harness := newRetryInvariantHarness(t)
	undo := harness.register(t)
	defer undo()

	for _, event := range openStepLog(t, "甲") {
		harness.emit(event)
	}
	failure := retryViolation(t, func() {
		harness.emit(retryEvent(t, 4, normalRetry("r-1", 1, 3)))
		data := normalRetry("r-1", 1, 3)
		data.Provider = "乙"
		harness.emit(retryEvent(t, 5, data))
	})
	if !strings.Contains(failure.Error(), "路由到的是") {
		t.Errorf("该报提供方对不上：%v", failure)
	}
}

// TestUnregisteringTheRetryInvariantsStopsTheCheck 钉住注销时退订。
func TestUnregisteringTheRetryInvariantsStopsTheCheck(t *testing.T) {
	t.Parallel()

	harness := newRetryInvariantHarness(t)
	harness.register(t)()
	if harness.unsubscribed != 1 {
		t.Fatalf("注销时该退订，退订了 %d 次", harness.unsubscribed)
	}
}

// TestRegisterRetryInvariantsNeedsAllThreeSeams 钉住三个口子一个都不能缺。
func TestRegisterRetryInvariantsNeedsAllThreeSeams(t *testing.T) {
	t.Parallel()

	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("造注册表失败：%v", err)
	}
	loaded := func() []session.Event { return nil }
	subscribe := func(func(session.Event)) func() { return func() {} }

	cases := map[string]func() error{
		"没给注册表": func() error {
			_, err := RegisterInvariants(context.Background(), nil, loaded, subscribe)
			return err
		},
		"没给已装载日志": func() error {
			_, err := RegisterInvariants(context.Background(), registry, nil, subscribe)
			return err
		},
		"没给订阅": func() error {
			_, err := RegisterInvariants(context.Background(), registry, loaded, nil)
			return err
		},
	}
	for label, run := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			if err := run(); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("该被拒并认得出哨兵：%v", err)
			}
		})
	}
}

// retryInvariantHarness 是一次不变量测试要的家当：一个开着的注册表、一段假的
// 已装载日志，以及一串挂上来的观察者。
type retryInvariantHarness struct {
	registry  *invariants.Registry
	loaded    []session.Event
	observers []func(session.Event)
	// unsubscribed 记下退订被调了几次。
	unsubscribed int
}

// newRetryInvariantHarness 造一份家当，loaded 是装的时候就已经在的那段日志。
func newRetryInvariantHarness(t *testing.T, loaded ...session.Event) *retryInvariantHarness {
	t.Helper()

	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("造注册表失败：%v", err)
	}
	return &retryInvariantHarness{registry: registry, loaded: loaded}
}

// register 把本包的检查装进去。
func (h *retryInvariantHarness) register(t *testing.T) func() {
	t.Helper()

	undo, err := RegisterInvariants(
		context.Background(),
		h.registry,
		func() []session.Event { return h.loaded },
		func(observer func(session.Event)) func() {
			h.observers = append(h.observers, observer)
			return func() { h.unsubscribed++ }
		},
	)
	if err != nil {
		t.Fatalf("装不变量失败：%v", err)
	}
	return undo
}

// emit 把一条事件推给所有还在的观察者。
func (h *retryInvariantHarness) emit(event session.Event) {
	for _, observer := range h.observers {
		observer(event)
	}
}

// retryViolation 跑一段会违例的代码，交出那条违例。
func retryViolation(t *testing.T, run func()) *invariants.Error {
	t.Helper()

	var caught *invariants.Error
	func() {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			failure, ok := recovered.(*invariants.Error)
			if !ok {
				panic(recovered)
			}
			caught = failure
		}()
		run()
	}()
	if caught == nil {
		t.Fatal("该抛出一条违例")
	}
	return caught
}
