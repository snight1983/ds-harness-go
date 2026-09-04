// 本文件验这个包做事的那一半：那条退避曲线、策略的指纹、可打断的等待，以及
// 一次失败到底走成「再试一次」还是「让给下一个观察者」。
package llmretry

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// fixedRandom 造一个总是交出同一个数的抖动源。
func fixedRandom(value float64) func() float64 {
	return func() float64 { return value }
}

// newInstallation 造一份不挂在任何注册表上的装配，给那几条不经过 [Install] 的用例用。
func newInstallation(t *testing.T, random func() float64) *installation {
	t.Helper()

	install := &installation{
		random: random,
		newID:  func() string { return "r-new" },
		logger: testLogger(t),
	}
	install.lifetime, install.stop = context.WithCancel(context.Background())
	t.Cleanup(install.stop)
	return install
}

// requestFailure 造一次落在 [openStepLog] 那个步骤上的失败。
func requestFailure(policy llm.ResolvedRetryPolicy) agent.RequestFailure {
	return agent.RequestFailure{
		Turn:           1,
		Step:           1,
		Provider:       "甲",
		Failure:        sampleFailure(),
		RetryPolicy:    policy,
		HasRetryPolicy: true,
	}
}

// TestLocalDelayGrowsExponentiallyAndStopsAtTheCeiling 钉住那条退避曲线：
// 逐次翻倍，到上限就不再涨。
//
// 源: packages/llm/llm-retry/src/index.ts（localDelay）
func TestLocalDelayGrowsExponentiallyAndStopsAtTheCeiling(t *testing.T) {
	t.Parallel()

	policy := normalPolicy(10)
	want := []time.Duration{
		500 * time.Millisecond,
		time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		10 * time.Second, // 到顶了
		10 * time.Second,
	}
	for index, expected := range want {
		retry := index + 1
		if got := localDelay(policy, retry, fixedRandom(0.5)); got != expected {
			t.Errorf("第 %d 次该等 %v，等了 %v", retry, expected, got)
		}
	}
}

// TestLocalDelayJitters 钉住抖动把延时摊在 [1-r, 1+r] 这个区间上，两头都取得到。
//
// 抖动存在只为一件事：让同时失败的一批请求不要在同一毫秒一起重来。倍率算错方向
// （比如漏掉那个 2）的话，所有客户端仍然在同一时刻回来，这一档就白设了。
func TestLocalDelayJitters(t *testing.T) {
	t.Parallel()

	policy := normalPolicy(10)
	policy.JitterRatio = 0.5

	// random 取 0 是区间的下沿，取 1 是上沿（[math/rand/v2.Float64] 取不到 1，
	// 这里逐字验那条公式的两端）。
	if got := localDelay(policy, 1, fixedRandom(0)); got != 250*time.Millisecond {
		t.Errorf("下沿该是 250ms，是 %v", got)
	}
	if got := localDelay(policy, 1, fixedRandom(1)); got != 750*time.Millisecond {
		t.Errorf("上沿该是 750ms，是 %v", got)
	}
	if got := localDelay(policy, 1, fixedRandom(0.5)); got != 500*time.Millisecond {
		t.Errorf("正中该是 500ms，是 %v", got)
	}
}

// TestTheJitterNeverPushesTheDelayOverTheCeiling 钉住抖动也不能把延时顶过上限。
//
// 源: packages/llm/llm-retry/src/index.ts（localDelay 最后那个 Math.min）
func TestTheJitterNeverPushesTheDelayOverTheCeiling(t *testing.T) {
	t.Parallel()

	policy := normalPolicy(10)
	policy.JitterRatio = 0.5
	if got := localDelay(policy, 20, fixedRandom(1)); got != policy.MaxDelay {
		t.Errorf("该被上限截回 %v，是 %v", policy.MaxDelay, got)
	}
}

// TestAHugeRetryCountStillYieldsTheCeiling 钉住次数大到指数溢出时也不会算出个
// 荒唐的延时。
//
// 源: packages/llm/llm-retry/src/index.ts（那句 Math.min(retry - 1, 1024)）
//
// math.Pow(2, 1024) 是 +Inf。InitialDelay 保证是正数（[llm.ResolveRetryPolicy]），
// 所以那个 Inf 不会变成 NaN，会被上限截回 MaxDelay。护栏拆掉的话，
// 一次第 5000 次的重试会等出一个负的 [time.Duration]（浮点转整数的溢出）。
func TestAHugeRetryCountStillYieldsTheCeiling(t *testing.T) {
	t.Parallel()

	policy := normalPolicy(10)
	got := localDelay(policy, 5000, fixedRandom(0.5))
	if got != policy.MaxDelay {
		t.Errorf("该被上限截回 %v，是 %v", policy.MaxDelay, got)
	}
	// 只判负数。NaN 在这里判不出来：got 已经是 [time.Duration] 了，一个整数转成
	// float64 永远不是 NaN——真出了 NaN，那次浮点转整数在上一层就已经落成某个
	// 具体的整数了，能看见的表现就是这个负数。
	if got < 0 {
		t.Errorf("算出来的延时不该是这个样子：%v", got)
	}
}

// TestThePolicyKeyIgnoresTheNormalOnlyFieldsInAlwaysMode 钉住 always 档的指纹
// 不看 maxRetries 和失败码清单。
//
// 源: packages/llm/llm-retry/src/index.ts（retryPolicyKey）
//
// 这一档根本不读那两个字段。算进去的话，一次和它无关的配置改动会白白斩断一条
// 正在走的链，而那条链的重试次数会从头再数一遍。
func TestThePolicyKeyIgnoresTheNormalOnlyFieldsInAlwaysMode(t *testing.T) {
	t.Parallel()

	base := alwaysPolicy()
	other := alwaysPolicy()
	other.MaxRetries = 99
	other.RetryableCodes = []string{"WHATEVER"}
	if retryPolicyKey(base) != retryPolicyKey(other) {
		t.Errorf("always 档不该看那两个字段：\n%s\n%s", retryPolicyKey(base), retryPolicyKey(other))
	}
}

// TestThePolicyKeyIsInsensitiveToTheOrderOfTheCodes 钉住调换失败码顺序不换指纹。
//
// 那两份策略行为完全一样。顺序敏感的话，一次纯粹的配置重排会让所有在走的链断掉。
func TestThePolicyKeyIsInsensitiveToTheOrderOfTheCodes(t *testing.T) {
	t.Parallel()

	base := normalPolicy(3)
	shuffled := normalPolicy(3)
	shuffled.RetryableCodes = []string{"RATE_LIMIT", "TIMEOUT"}
	if retryPolicyKey(base) != retryPolicyKey(shuffled) {
		t.Errorf("顺序不该换指纹：\n%s\n%s", retryPolicyKey(base), retryPolicyKey(shuffled))
	}

	// 而排序不许就地改掉调用方那份清单。
	if !slices.Equal(shuffled.RetryableCodes, []string{"RATE_LIMIT", "TIMEOUT"}) {
		t.Errorf("算指纹不该动到策略本身：%v", shuffled.RetryableCodes)
	}
}

// TestChangingThePolicyChangesTheKey 钉住真的改了策略时指纹跟着换。
func TestChangingThePolicyChangesTheKey(t *testing.T) {
	t.Parallel()

	base := retryPolicyKey(normalPolicy(3))
	cases := map[string]llm.ResolvedRetryPolicy{
		"换了上限": normalPolicy(5),
		"换了档位": alwaysPolicy(),
	}
	slower := normalPolicy(3)
	slower.InitialDelay = time.Second
	cases["换了退避"] = slower
	jittered := normalPolicy(3)
	jittered.JitterRatio = 0.2
	cases["换了抖动"] = jittered
	fewer := normalPolicy(3)
	fewer.RetryableCodes = []string{"TIMEOUT"}
	cases["换了失败码"] = fewer

	for label, policy := range cases {
		if retryPolicyKey(policy) == base {
			t.Errorf("%s 该换指纹，没换：%s", label, base)
		}
	}
}

// TestProviderRetryAfterWins 钉住提供方点了名要等多久就听它的。
//
// 源: packages/llm/llm-retry/src/index.ts（那段 providerRetryAfterMs 分支）
//
// 它比本地那条曲线更知道自己什么时候缓过来。
func TestProviderRetryAfterWins(t *testing.T) {
	t.Parallel()

	install := newInstallation(t, fixedRandom(0.5))
	failure := sampleFailure()
	failure.ProviderRetryAfterMs = 2500
	delay, ok := install.retryDelay(normalPolicy(3), failure, 1)
	if !ok || delay != 2500*time.Millisecond {
		t.Fatalf("该听提供方的 2.5 秒，得到 %v（ok=%v）", delay, ok)
	}
}

// TestAnOutrageousRetryAfterIsHandledPerMode 钉住提供方点了个离谱的数时两档分头处理。
//
// normal 档作罢（那份策略写明了「最多等这么久」，等更久等于替用户改了他的配置），
// always 档退回本地退避（这一档的承诺是「一直重试」，作罢会把那句承诺废掉）。
func TestAnOutrageousRetryAfterIsHandledPerMode(t *testing.T) {
	t.Parallel()

	install := newInstallation(t, fixedRandom(0.5))
	failure := sampleFailure()
	failure.ProviderRetryAfterMs = 60_000

	if _, ok := install.retryDelay(normalPolicy(3), failure, 1); ok {
		t.Error("normal 档该作罢")
	}
	delay, ok := install.retryDelay(alwaysPolicy(), failure, 1)
	if !ok || delay != 500*time.Millisecond {
		t.Errorf("always 档该退回本地退避，得到 %v（ok=%v）", delay, ok)
	}
}

// TestNoRetryAfterFallsBackToTheLocalCurve 钉住提供方没点名时走本地曲线。
//
// [llm.Failure] 用 0 表示这个字段缺席，所以这条也顺带钉住 0 不会被当成「等 0 秒」。
func TestNoRetryAfterFallsBackToTheLocalCurve(t *testing.T) {
	t.Parallel()

	install := newInstallation(t, fixedRandom(0.5))
	delay, ok := install.retryDelay(normalPolicy(3), sampleFailure(), 2)
	if !ok || delay != time.Second {
		t.Fatalf("该走本地曲线的第 2 档（1 秒），得到 %v（ok=%v）", delay, ok)
	}
}

// TestCancellableDelayWaitsOutTheClock 钉住没人打断时等满了返回真。
func TestCancellableDelayWaitsOutTheClock(t *testing.T) {
	t.Parallel()

	if !cancellableDelay(context.Background(), time.Millisecond) {
		t.Error("等满了该返回真")
	}
}

// TestCancellableDelayStopsWhenCancelled 钉住等到一半被取消时返回假。
func TestCancellableDelayStopsWhenCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(time.Millisecond)
		cancel()
	}()
	if cancellableDelay(ctx, time.Minute) {
		t.Error("被取消了该返回假")
	}
}

// TestAZeroDelayStillHonoursCancellation 钉住零延时那条捷径也看取消。
//
// 源: packages/llm/llm-retry/src/index.ts（cancellableDelay）
//
// 让一个立刻就绪的定时器和一个已经取消的上下文一起进 select，Go 会**随机**挑一个，
// 于是「已经取消了还照样重试」会偶发——一个跑一万次才漏一次的毛病。
func TestAZeroDelayStillHonoursCancellation(t *testing.T) {
	t.Parallel()

	if !cancellableDelay(context.Background(), 0) {
		t.Error("零延时且没被取消该返回真")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range 100 {
		if cancellableDelay(ctx, 0) {
			t.Fatal("已经取消了就不该放行，哪怕一次")
		}
	}
}

// TestTheFuseBreaksOnEitherSide 钉住并出来的那个上下文两边任一断了都断。
func TestTheFuseBreaksOnEitherSide(t *testing.T) {
	t.Parallel()

	t.Run("命脉断了", func(t *testing.T) {
		t.Parallel()
		lifetime, stop := context.WithCancel(context.Background())
		fused, release := fuse(context.Background(), lifetime)
		defer release()
		stop()
		<-fused.Done()
	})

	t.Run("请求断了", func(t *testing.T) {
		t.Parallel()
		request, cancel := context.WithCancel(context.Background())
		fused, release := fuse(request, context.Background())
		defer release()
		cancel()
		<-fused.Done()
	})
}

// TestReleasingTheFuseDetachesItFromTheLifetime 钉住释放之后命脉上不再挂着东西。
//
// 不释放的话，那个 [context.AfterFunc] 会一直挂到整次装配拆除为止，每一次重试漏一个。
// 释放之后再断命脉，不该再动到已经放掉的那个上下文。
func TestReleasingTheFuseDetachesItFromTheLifetime(t *testing.T) {
	t.Parallel()

	lifetime, stop := context.WithCancel(context.Background())
	defer stop()
	fused, release := fuse(context.Background(), lifetime)
	release()
	if !errors.Is(context.Cause(fused), context.Canceled) {
		t.Fatalf("释放之后该是取消掉的：%v", context.Cause(fused))
	}
	stop()
	if !errors.Is(context.Cause(fused), context.Canceled) {
		t.Errorf("命脉后来断掉不该改写它的取消因由：%v", context.Cause(fused))
	}
}

// TestLastChainRetryFindsTheLatestMatch 钉住翻的是最后那条同链的重试。
//
// 源: packages/llm/llm-retry/src/index.ts（那句 events.findLast）
func TestLastChainRetryFindsTheLatestMatch(t *testing.T) {
	t.Parallel()

	events := append(openStepLog(t, "甲"),
		retryEvent(t, 4, normalRetry("r-1", 1, 3)),
		retryEvent(t, 5, normalRetry("r-1", 2, 3)),
	)
	data, continues, err := lastChainRetry(events, 1, 1, "甲", "k-1")
	if err != nil || !continues || data.Retry != 2 {
		t.Fatalf("该翻到第 2 次（continues=%v err=%v）：%+v", continues, err, data)
	}
}

// TestLastChainRetrySkipsOtherChains 钉住四个比较项缺一不可。
func TestLastChainRetrySkipsOtherChains(t *testing.T) {
	t.Parallel()

	events := append(openStepLog(t, "甲"), retryEvent(t, 4, normalRetry("r-1", 1, 3)))
	cases := map[string]struct {
		turn, step          int
		provider, policyKey string
	}{
		"另一个回合":  {2, 1, "甲", "k-1"},
		"另一个步骤":  {1, 2, "甲", "k-1"},
		"另一个提供方": {1, 1, "乙", "k-1"},
		"另一份策略":  {1, 1, "甲", "k-2"},
	}
	for label, testCase := range cases {
		_, continues, err := lastChainRetry(
			events, testCase.turn, testCase.step, testCase.provider, testCase.policyKey)
		if continues || err != nil {
			t.Errorf("%s 不该算同一条链（continues=%v err=%v）", label, continues, err)
		}
	}
}

// TestLastChainRetryReportsAnUnreadableEntry 钉住坏掉的那条报出来，不咽下去。
//
// 咽下去当成零次的话，一份坏日志会换来一串无上限的重试，而每一次都是真花钱的。
func TestLastChainRetryReportsAnUnreadableEntry(t *testing.T) {
	t.Parallel()

	events := append(openStepLog(t, "甲"), rawEvent(4, EventRetry, `{"retry":"一"}`))
	if _, _, err := lastChainRetry(events, 1, 1, "甲", "k-1"); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("该被拒并认得出哨兵：%v", err)
	}
}

// TestAFailureWithoutAPolicyIsPassedDown 钉住没有策略时直接往下传。
func TestAFailureWithoutAPolicyIsPassedDown(t *testing.T) {
	t.Parallel()

	install := newInstallation(t, fixedRandom(0.5))
	live := &fakeSession{}
	passed := false
	next := func(context.Context) (agent.RequestErrorAction, error) {
		passed = true
		return agent.RequestErrorAction{}, nil
	}

	failure := requestFailure(normalPolicy(3))
	failure.HasRetryPolicy = false
	if _, err := install.recover(context.Background(), live, failure, next); err != nil {
		t.Fatalf("不该出错：%v", err)
	}
	if !passed {
		t.Error("该让给下一个观察者")
	}
	if len(live.Events()) != 0 {
		t.Errorf("不该写下任何事件：%v", live.types())
	}
}

// TestANonRetryableCodeIsPassedDown 钉住不在清单里的失败码往下传。
//
// 认领了却不重的话，排在后面那些更懂这次失败的观察者（比如换个提供方重来）
// 永远轮不到。
func TestANonRetryableCodeIsPassedDown(t *testing.T) {
	t.Parallel()

	install := newInstallation(t, fixedRandom(0.5))
	live := &fakeSession{log: openStepLog(t, "甲")}
	passed := false
	next := func(context.Context) (agent.RequestErrorAction, error) {
		passed = true
		return agent.RequestErrorAction{}, nil
	}

	failure := requestFailure(normalPolicy(3))
	failure.Failure.Code = "BAD_REQUEST"
	if _, err := install.recover(context.Background(), live, failure, next); err != nil {
		t.Fatalf("不该出错：%v", err)
	}
	if !passed {
		t.Error("该让给下一个观察者")
	}
}

// TestARetryableFailureWritesBothEventsAndClaimsTheRecovery 钉住一次正常的重试
// 写下那两条事件、并认领这次恢复。
//
// 源: packages/llm/llm-retry/src/index.ts（backoff）
func TestARetryableFailureWritesBothEventsAndClaimsTheRecovery(t *testing.T) {
	t.Parallel()

	install := newInstallation(t, fixedRandom(0.5))
	policy := normalPolicy(3)
	policy.InitialDelay = time.Millisecond
	policy.MaxDelay = time.Millisecond
	live := &fakeSession{log: openStepLog(t, "甲")}

	action, err := install.recover(context.Background(), live, requestFailure(policy), refuseNext(t))
	if err != nil {
		t.Fatalf("不该出错：%v", err)
	}
	if !action.Retry {
		t.Error("该认领这次恢复")
	}

	written := live.Events()[3:]
	if len(written) != 2 ||
		written[0].Type != EventRetry || written[1].Type != EventRetryStarted {
		t.Fatalf("该写下排期和熬过去这两条：%v", live.types())
	}

	// 而写下来的那条排期得经得起本包自己那几条不变量。
	if _, err := ValidateLog(live.Events()); err != nil {
		t.Errorf("自己写出来的日志该过得了自己的不变量：%v", err)
	}

	data, err := DecodeRetry(written[0])
	if err != nil {
		t.Fatalf("读不回来：%v", err)
	}
	if data.Retry != 1 || data.RetryID != "r-new" || !data.HasMaxRetries || data.MaxRetries != 3 {
		t.Errorf("排期的内容不对：%+v", data)
	}
}

// TestASecondFailureContinuesTheSameChain 钉住同一条链上的第二次接着往下数、
// 而且身份不变。
//
// 身份变了的话，llm/retry-started 那一边就再也认不出它配对的是哪一次排期。
func TestASecondFailureContinuesTheSameChain(t *testing.T) {
	t.Parallel()

	install := newInstallation(t, fixedRandom(0.5))
	install.newID = func() string { return "r-另一个" }
	policy := normalPolicy(3)
	policy.InitialDelay = time.Millisecond
	policy.MaxDelay = time.Millisecond

	first := normalRetry("r-1", 1, 3)
	first.PolicyKey = retryPolicyKey(policy)
	first.Delay = time.Millisecond
	live := &fakeSession{log: append(openStepLog(t, "甲"),
		retryEvent(t, 4, first),
		startedEvent(t, 5, startedFor(first)),
	)}

	if _, err := install.recover(context.Background(), live, requestFailure(policy), refuseNext(t)); err != nil {
		t.Fatalf("不该出错：%v", err)
	}
	data, err := DecodeRetry(live.Events()[5])
	if err != nil {
		t.Fatalf("读不回来：%v", err)
	}
	if data.Retry != 2 || data.RetryID != "r-1" {
		t.Errorf("该是同一条链上的第 2 次：%+v", data)
	}
}

// TestExhaustingTheRetriesPassesDown 钉住重够了之后让给下一个观察者，而不是交终局。
//
// 源: packages/llm/llm-retry/src/index.ts（那句 previousRetry >= policy.maxRetries）
//
// 交终局的话，一个还有办法的观察者（比如切到备用提供方）就再也没机会了。
func TestExhaustingTheRetriesPassesDown(t *testing.T) {
	t.Parallel()

	install := newInstallation(t, fixedRandom(0.5))
	policy := normalPolicy(1)
	policy.InitialDelay = time.Millisecond
	policy.MaxDelay = time.Millisecond

	spent := normalRetry("r-1", 1, 1)
	spent.PolicyKey = retryPolicyKey(policy)
	spent.Delay = time.Millisecond
	live := &fakeSession{log: append(openStepLog(t, "甲"), retryEvent(t, 4, spent))}

	passed := false
	next := func(context.Context) (agent.RequestErrorAction, error) {
		passed = true
		return agent.RequestErrorAction{}, nil
	}
	if _, err := install.recover(context.Background(), live, requestFailure(policy), next); err != nil {
		t.Fatalf("不该出错：%v", err)
	}
	if !passed {
		t.Error("重够了该让给下一个观察者")
	}
	if len(live.Events()) != 4 {
		t.Errorf("不该再写下任何事件：%v", live.types())
	}
}

// TestAlwaysModeLetsTheDownstreamClaimFirst 钉住 always 档先让下游有机会认领。
//
// 源: packages/llm/llm-retry/src/index.ts（那段 settleDownstream(next)）
//
// 顺序反过来的话，always 就成了一堵墙：任何排在它后面的、更懂这次失败的观察者
// 永远轮不到。
func TestAlwaysModeLetsTheDownstreamClaimFirst(t *testing.T) {
	t.Parallel()

	install := newInstallation(t, fixedRandom(0.5))
	live := &fakeSession{log: openStepLog(t, "甲")}
	next := func(context.Context) (agent.RequestErrorAction, error) {
		return agent.RequestErrorAction{Retry: true}, nil
	}

	action, err := install.recover(context.Background(), live, requestFailure(alwaysPolicy()), next)
	if err != nil || !action.Retry {
		t.Fatalf("该交出下游那个结论：%+v（err=%v）", action, err)
	}
	if len(live.Events()) != 3 {
		t.Errorf("下游认领了就不该写下重试事件：%v", live.types())
	}
}

// TestAlwaysModeRetriesEvenWhenTheDownstreamBlowsUp 钉住下游炸了 always 档照样重。
//
// 下游炸了不算这次重试的事：always 的意思就是「不管怎么失败都再试一次」。
func TestAlwaysModeRetriesEvenWhenTheDownstreamBlowsUp(t *testing.T) {
	t.Parallel()

	install := newInstallation(t, fixedRandom(0.5))
	policy := alwaysPolicy()
	policy.InitialDelay = time.Millisecond
	policy.MaxDelay = time.Millisecond
	live := &fakeSession{log: openStepLog(t, "甲")}
	next := func(context.Context) (agent.RequestErrorAction, error) {
		return agent.RequestErrorAction{}, errors.New("下游自己炸了")
	}

	action, err := install.recover(context.Background(), live, requestFailure(policy), next)
	if err != nil || !action.Retry {
		t.Fatalf("该照样重：%+v（err=%v）", action, err)
	}
	data, err := DecodeRetry(live.Events()[3])
	if err != nil {
		t.Fatalf("读不回来：%v", err)
	}
	if data.HasMaxRetries {
		t.Errorf("always 档不该带上限：%+v", data)
	}
}

// TestAnAlreadyCancelledContextWritesNothing 钉住已经取消了就一条事件都不写。
//
// 写下一条谁也不会去熬的排期，等于在日志里留下一次没发生过的重试。
func TestAnAlreadyCancelledContextWritesNothing(t *testing.T) {
	t.Parallel()

	install := newInstallation(t, fixedRandom(0.5))
	live := &fakeSession{log: openStepLog(t, "甲")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	action, err := install.recover(ctx, live, requestFailure(normalPolicy(3)), refuseNext(t))
	if err != nil || action.Retry {
		t.Fatalf("该交终局：%+v（err=%v）", action, err)
	}
	if len(live.Events()) != 3 {
		t.Errorf("不该写下任何事件：%v", live.types())
	}
}

// TestBeingCancelledDuringTheBackoffWritesOnlyTheSchedule 钉住等到一半被打断时
// 只留下那条排期。
//
// 那次请求确实没有发出去，所以不写 llm/retry-started——两条事件分开写，
// 为的正是让「排好期了」和「真的熬过去了」在日志里分得开。
func TestBeingCancelledDuringTheBackoffWritesOnlyTheSchedule(t *testing.T) {
	t.Parallel()

	install := newInstallation(t, fixedRandom(0.5))
	policy := normalPolicy(3)
	policy.InitialDelay = time.Minute
	policy.MaxDelay = time.Minute

	ctx, cancel := context.WithCancel(context.Background())
	live := &fakeSession{log: openStepLog(t, "甲"), onAppend: func(event sessionlog.Event) {
		if event.Type == EventRetry {
			cancel()
		}
	}}

	action, err := install.recover(ctx, live, requestFailure(policy), refuseNext(t))
	if err != nil || action.Retry {
		t.Fatalf("该交终局：%+v（err=%v）", action, err)
	}
	if got := live.types(); len(got) != 4 || got[3] != EventRetry {
		t.Errorf("只该留下那条排期：%v", got)
	}
}

// TestTearingDownInterruptsAWaitingBackoff 钉住拆除会打断还在等的那段退避。
//
// 源: packages/llm/llm-retry/src/index.ts（那个 lifetime AbortController）
//
// 打不断的话，一次拆除会被一段长达上限的等待拖住，而拆除那边还在 Wait。
func TestTearingDownInterruptsAWaitingBackoff(t *testing.T) {
	t.Parallel()

	install := newInstallation(t, fixedRandom(0.5))
	policy := normalPolicy(3)
	policy.InitialDelay = time.Minute
	policy.MaxDelay = time.Minute

	live := &fakeSession{log: openStepLog(t, "甲"), onAppend: func(event sessionlog.Event) {
		if event.Type == EventRetry {
			install.stop()
		}
	}}

	action, err := install.recover(context.Background(), live, requestFailure(policy), refuseNext(t))
	if err != nil || action.Retry {
		t.Fatalf("该交终局：%+v（err=%v）", action, err)
	}
	if got := live.types(); len(got) != 4 {
		t.Errorf("只该留下那条排期：%v", got)
	}
}

// TestAnUnreadablePriorRetrySurfaces 钉住日志里躺着一条坏的重试时报出来。
func TestAnUnreadablePriorRetrySurfaces(t *testing.T) {
	t.Parallel()

	install := newInstallation(t, fixedRandom(0.5))
	live := &fakeSession{log: append(openStepLog(t, "甲"),
		rawEvent(4, EventRetry, `{"retry":"一"}`))}

	if _, err := install.recover(
		context.Background(), live, requestFailure(normalPolicy(3)), refuseNext(t),
	); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("该被拒并认得出哨兵：%v", err)
	}
}

// TestAFailingAppendSurfaces 钉住会话拒收时那条错误交出去，不装作重成功了。
//
// 咽下去返回 Retry:true 的话，循环会真的再发一次请求，而日志里没有任何痕迹。
func TestAFailingAppendSurfaces(t *testing.T) {
	t.Parallel()

	install := newInstallation(t, fixedRandom(0.5))
	policy := normalPolicy(3)
	policy.InitialDelay = time.Millisecond
	policy.MaxDelay = time.Millisecond

	for _, kind := range []sessionlog.EventType{EventRetry, EventRetryStarted} {
		live := &fakeSession{log: openStepLog(t, "甲"), failOn: kind}
		action, err := install.recover(
			context.Background(), live, requestFailure(policy), refuseNext(t))
		if err == nil {
			t.Errorf("%s 追加失败时该报出来", kind)
		}
		if action.Retry {
			t.Errorf("%s 追加失败时不该认领这次恢复", kind)
		}
	}
}

// TestInstallNeedsBothHosts 钉住注册表和所有者作用域都得交进来。
func TestInstallNeedsBothHosts(t *testing.T) {
	t.Parallel()

	registry, err := agent.NewRegistry(agent.RegistryOptions{Logger: testLogger(t)})
	if err != nil {
		t.Fatalf("造 agent 注册表失败：%v", err)
	}
	owner := scope.NewRoot()
	defer func() { _ = owner.Dispose(context.Background()) }()

	cases := map[string]Options{
		"没给注册表": {Owner: owner},
		"没给所有者": {Agents: registry},
	}
	for label, options := range cases {
		if _, err := Install(context.Background(), options); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("%s 时该被拒并认得出哨兵：%v", label, err)
		}
	}
}

// TestTearingDownTwiceOnlyDetachesOnce 钉住拆除函数调第二遍是个空操作。
//
// 装配方常常把它挂在自己的 [github.com/snight1983/ds-harness-go/scope.Scope] 上、同时也在出错路径上
// 手动调一遍。第二遍真的去摘一次观察者的话，摘掉的会是**后来装上去的**那一个。
func TestTearingDownTwiceOnlyDetachesOnce(t *testing.T) {
	t.Parallel()

	registry, err := agent.NewRegistry(agent.RegistryOptions{Logger: testLogger(t)})
	if err != nil {
		t.Fatalf("造 agent 注册表失败：%v", err)
	}
	owner := scope.NewRoot()
	defer func() { _ = owner.Dispose(context.Background()) }()

	dispose, err := Install(context.Background(), Options{
		Agents: registry,
		Owner:  owner,
		Logger: testLogger(t),
	})
	if err != nil {
		t.Fatalf("装不上：%v", err)
	}
	if err := dispose(context.Background()); err != nil {
		t.Fatalf("拆不掉：%v", err)
	}
	if err := dispose(context.Background()); err != nil {
		t.Fatalf("再拆一遍该什么都不做：%v", err)
	}
}

// TestAnObserverAfterTeardownIsTerminalAndNeverCallsNext 钉住拆除之后进来的那次
// 调用直接交终局。
//
// 源: packages/llm/llm-retry/src/index.ts（那句 if (lifetime.signal.aborted) 提前返回）
//
// 逐字跟着 DSH。这条分支的窗口只有「观察者已经被摘掉、这次调用却已经进了门」那一瞬。
func TestAnObserverAfterTeardownIsTerminalAndNeverCallsNext(t *testing.T) {
	t.Parallel()

	install := newInstallation(t, fixedRandom(0.5))
	install.mutex.Lock()
	install.disposed = true
	install.mutex.Unlock()

	action, err := install.observe(
		context.Background(), requestFailure(normalPolicy(3)), refuseNext(t))
	if err != nil || action.Retry {
		t.Fatalf("该交终局：%+v（err=%v）", action, err)
	}
}

// refuseNext 造一个「一旦被调用就算测试失败」的下游。
func refuseNext(t *testing.T) func(context.Context) (agent.RequestErrorAction, error) {
	t.Helper()

	return func(context.Context) (agent.RequestErrorAction, error) {
		t.Error("不该往下传")
		return agent.RequestErrorAction{}, nil
	}
}
