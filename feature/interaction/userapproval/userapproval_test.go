// 本文件的作用：把这道接缝的全部可观察行为钉住——回合围栏、策略闸、答复者链的
// 分派与收敛、取消那场赛跑、审计那一对的落地形状，以及那条持久不变量。
//
// 逐条对着 DSH 的 tests/approval.spec.ts 与 tests/invariant.spec.ts 走。那边靠 cordis
// 把整套服务装起来，这里换成一条假日志加几条假答复者。
//
// # 这些测试防的是什么错
//
//   - **审计那一对被拆了**。一次判决没落日志、或者只落了一半，回放时就再也说不清
//     那次工具调用当初到底获批没有。
//   - **失败朝「放行」那一侧倒**。没人应答、应答报错、应答还回来一个词汇表外的值，
//     三种都必须落到 unavailable；漏掉任何一种，一次没人看着的调用就跑出去了。
//   - **never 被一条后登记的答复者绕过去了**。那条承诺是「确定地拒绝」，它一旦取决于
//     登记顺序，无人值守的批跑就不再安全。
//   - **一次取消之后迟到的答复还算数**。用户已经撤回的问题不许在几秒后变成一次授权。
package userapproval_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/feature/interaction/userapproval"
	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/tools"
)

// errAppend 是那条假日志被要求失败时报的错。
var errAppend = errors.New("append failed before log growth")

// fakeLog 是一条把每次追加都记下来的假会话日志。
type fakeLog struct {
	events   []sessionlog.Event
	appended []appended
	// failOn 非空时，这种事件的追加失败。
	failOn sessionlog.EventType
}

// appended 是一次被记下来的追加。
type appended struct {
	kind sessionlog.EventType
	raw  json.RawMessage
}

// Events 交出这条日志到目前为止的全部事件。
func (l *fakeLog) Events() []sessionlog.Event { return l.events }

// Append 记下这次追加，并且把它接到日志尾巴上——后面的折叠要看得见它。
func (l *fakeLog) Append(kind sessionlog.EventType, data any) error {
	if l.failOn == kind {
		return errAppend
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	l.appended = append(l.appended, appended{kind: kind, raw: raw})
	l.events = append(l.events, sessionlog.Event{Type: kind, Data: raw})
	return nil
}

// kinds 列出这条日志上被追加过的事件类型。
func (l *fakeLog) kinds() []string {
	list := make([]string, 0, len(l.appended))
	for _, event := range l.appended {
		list = append(list, string(event.kind))
	}
	return list
}

// field 从第 index 次追加的负载里取一个字段的原始 JSON。
func (l *fakeLog) field(t *testing.T, index int, name string) string {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(l.appended[index].raw, &fields); err != nil {
		t.Fatalf("第 %d 次追加的负载读不回来：%v", index, err)
	}
	return string(fields[name])
}

// openTurn 是一条已经打开了回合的日志种子。
func openTurn() []sessionlog.Event {
	return []sessionlog.Event{{Type: sessionlog.EventTurnStart}, {Type: sessionlog.EventUserMessage}}
}

// harness 是一次服务测试要的全套家当。
type harness struct {
	service *userapproval.Service
	log     *fakeLog
	agent   *scope.Key
	root    *scope.Scope
	// notices 是被排进下一次模型步的那些通知。
	notices []llm.Message
	// ids 是发号器发出去的号，按顺序。
	ids int
}

// newHarness 造一个接好假日志的服务。config 里的 LogOf/Notify/NewID 会被接管。
func newHarness(t *testing.T, config userapproval.Config, seed []sessionlog.Event) *harness {
	t.Helper()
	h := &harness{log: &fakeLog{events: seed}, agent: scope.NewKey("agent"), root: scope.NewRoot()}
	config.LogOf = func(*scope.Key) (userapproval.Log, error) { return h.log, nil }
	config.Notify = func(_ *scope.Key, message llm.Message) error {
		h.notices = append(h.notices, message)
		return nil
	}
	if config.NewID == nil {
		config.NewID = func() string {
			h.ids++
			return fmt.Sprintf("req-%d", h.ids)
		}
	}
	service, err := userapproval.New(config)
	if err != nil {
		t.Fatalf("造服务失败：%v", err)
	}
	h.service = service
	return h
}

// request 问一次最普通的问题。
func (h *harness) request(ctx context.Context) (tools.ApprovalOutcome, error) {
	return h.service.Request(ctx, tools.ApprovalRequest{Agent: h.agent, ToolName: "echo"})
}

// answer 往全局层接一条固定答复的答复者。
func (h *harness) answer(t *testing.T, outcome tools.ApprovalOutcome) {
	t.Helper()
	h.register(t, h.root, func(context.Context, tools.ApprovalRequest, func() (tools.ApprovalOutcome, error)) (tools.ApprovalOutcome, error) {
		return outcome, nil
	})
}

// register 接一条答复者，并把注销登记到测试收尾上。
func (h *harness) register(t *testing.T, owner *scope.Scope, answerer userapproval.Answerer) func(context.Context) error {
	t.Helper()
	undo, err := h.service.RegisterAnswerer(context.Background(), owner, answerer)
	if err != nil {
		t.Fatalf("接答复者失败：%v", err)
	}
	t.Cleanup(func() { _ = undo(context.Background()) })
	return undo
}

// textOf 拼出一条消息里的纯文本。
func textOf(message llm.Message) string {
	var parts []string
	for _, block := range message.Content {
		if typed, ok := block.(llm.TextBlock); ok {
			parts = append(parts, typed.Text)
		}
	}
	return strings.Join(parts, "")
}

// eventOf 造一条带负载的事件。
func eventOf(t *testing.T, kind sessionlog.EventType, data any) sessionlog.Event {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("造事件失败：%v", err)
	}
	return sessionlog.Event{Type: kind, Data: raw}
}

func TestRefusesAnIdleAskBeforeAppendingAnything(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userapproval.Config{}, nil)

	_, err := h.request(context.Background())
	if err == nil || !strings.Contains(err.Error(), "outside an open turn") {
		t.Fatalf("一次没有回合的提问该被拒：%v", err)
	}
	// 一个坏请求不许在日志上留下任何痕迹：半对审计比没有审计更糟。
	if len(h.log.appended) != 0 {
		t.Fatalf("被拒了就不该写日志：%+v", h.log.kinds())
	}
}

func TestRefusesAnAskBetweenTurns(t *testing.T) {
	t.Parallel()
	// 一个**关掉了**的回合不满足围栏条件：两个回合之间裸写的一条事件，重新装载时
	// 和一段崩溃残尾长得一模一样。
	h := newHarness(t, userapproval.Config{}, []sessionlog.Event{
		{Type: sessionlog.EventTurnStart}, {Type: sessionlog.EventTurnEnd},
	})

	if _, err := h.request(context.Background()); err == nil {
		t.Fatal("两个回合之间的提问该被拒")
	}
	if len(h.log.appended) != 0 {
		t.Fatalf("被拒了就不该写日志：%+v", h.log.kinds())
	}
}

func TestFailsClosedToUnavailableAndAuditsThePair(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userapproval.Config{}, openTurn())

	outcome, err := h.service.Request(context.Background(), tools.ApprovalRequest{
		Agent: h.agent, ToolName: "echo", CallID: llm.CallID("call-1"), Reason: "hook says ask",
	})
	if err != nil {
		t.Fatalf("这次提问不该失败：%v", err)
	}
	if outcome != tools.ApprovalUnavailable {
		t.Fatalf("一个都没接上来时该失败关闭，拿到 %q", outcome)
	}
	if got := strings.Join(h.log.kinds(), ","); got != "approval/asked,approval/decided" {
		t.Fatalf("审计那一对该成双：%q", got)
	}
	if got := string(h.log.appended[0].raw); got != `{"id":"req-1","toolName":"echo","callId":"call-1","reason":"hook says ask"}` {
		t.Fatalf("那条 asked 的负载不对：%s", got)
	}
	if got := string(h.log.appended[1].raw); got != `{"id":"req-1","outcome":"unavailable"}` {
		t.Fatalf("那条 decided 的负载不对：%s", got)
	}
}

func TestOmitsAbsentOptionalFieldsFromTheAskedEvent(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userapproval.Config{}, openTurn())

	if _, err := h.request(context.Background()); err != nil {
		t.Fatalf("这次提问不该失败：%v", err)
	}
	// 这条日志是要持久保存并回放的：一条不带 callId 的审计事件在介质上必须**没有**
	// 那个键，而不是有一个空串。
	if got := string(h.log.appended[0].raw); got != `{"id":"req-1","toolName":"echo"}` {
		t.Fatalf("没给的可选字段该整个略掉：%s", got)
	}
}

func TestPassesTheExactRequestToTheAnswerer(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userapproval.Config{}, openTurn())
	var seen tools.ApprovalRequest
	h.register(t, h.root, func(_ context.Context, request tools.ApprovalRequest, _ func() (tools.ApprovalOutcome, error)) (tools.ApprovalOutcome, error) {
		seen = request
		return tools.ApprovalAllowedOnce, nil
	})
	want := tools.ApprovalRequest{
		Agent: h.agent, ToolName: "scoped-tool",
		CallID: llm.CallID("scoped-call"), Reason: "scoped reason",
	}

	outcome, err := h.service.Request(context.Background(), want)
	if err != nil {
		t.Fatalf("这次提问不该失败：%v", err)
	}
	if outcome != tools.ApprovalAllowedOnce {
		t.Fatalf("该交出答复者给的那个答复，拿到 %q", outcome)
	}
	if seen != want {
		t.Fatalf("该把那份请求原样交给答复者：%+v", seen)
	}
	if got := h.log.field(t, 1, "outcome"); got != `"allowed-once"` {
		t.Fatalf("那条 decided 该记下同一个答复：%s", got)
	}
	if h.log.field(t, 0, "id") != h.log.field(t, 1, "id") {
		t.Fatalf("那一对该靠同一个 id 配上：%+v", h.log.appended)
	}
}

func TestPropagatesAnAppendFailureThatPreventedAuditLogGrowth(t *testing.T) {
	t.Parallel()
	for label, failOn := range map[string]sessionlog.EventType{
		"asked 没写进去":   userapproval.EventAsked,
		"decided 没写进去": userapproval.EventDecided,
	} {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, userapproval.Config{}, openTurn())
			h.log.failOn = failOn
			h.answer(t, tools.ApprovalAllowedOnce)

			// 交出一个没落审计的决定，就是把那一对拆了：宁可整次提问失败。
			if _, err := h.request(context.Background()); !errors.Is(err, errAppend) {
				t.Fatalf("追加失败该原样报出来：%v", err)
			}
		})
	}
}

func TestReturnsTheFirstAnsweringAnswerer(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userapproval.Config{}, openTurn())
	secondRan := false
	h.answer(t, tools.ApprovalAllowedOnce)
	h.register(t, h.root, func(context.Context, tools.ApprovalRequest, func() (tools.ApprovalOutcome, error)) (tools.ApprovalOutcome, error) {
		secondRan = true
		return tools.ApprovalRejected, nil
	})

	// 只有一个决定的位置：第一条认领了就到此为止。
	outcome, _ := h.request(context.Background())
	if outcome != tools.ApprovalAllowedOnce {
		t.Fatalf("该是第一条给的答复，拿到 %q", outcome)
	}
	if secondRan {
		t.Fatal("第一条认领之后第二条不该再跑")
	}
}

func TestLetsANonOwningAnswererDelegateDownToTheFailClosedDefault(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userapproval.Config{}, openTurn())
	h.register(t, h.root, func(_ context.Context, _ tools.ApprovalRequest, next func() (tools.ApprovalOutcome, error)) (tools.ApprovalOutcome, error) {
		return next()
	})

	if outcome, _ := h.request(context.Background()); outcome != tools.ApprovalUnavailable {
		t.Fatalf("链走到底该落在失败关闭上，拿到 %q", outcome)
	}
}

func TestDispatchesToGlobalAndMatchingScopedAnswerersNeverAForeignScope(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userapproval.Config{}, openTurn())
	other := scope.NewKey("other")
	mine, err := scope.New(h.agent, scope.Options{})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	theirs, err := scope.New(other, scope.Options{})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	var heard []string
	trace := func(label string) userapproval.Answerer {
		return func(_ context.Context, _ tools.ApprovalRequest, next func() (tools.ApprovalOutcome, error)) (tools.ApprovalOutcome, error) {
			heard = append(heard, label)
			return next()
		}
	}
	h.register(t, h.root, trace("global"))
	h.register(t, mine, trace("scoped:mine"))
	h.register(t, theirs, trace("scoped:theirs"))

	if outcome, _ := h.request(context.Background()); outcome != tools.ApprovalUnavailable {
		t.Fatalf("都放行到底了该落在失败关闭上，拿到 %q", outcome)
	}
	// 先全局、再本作用域；别人那一层一次都不该被惊动。
	if got := strings.Join(heard, ","); got != "global,scoped:mine" {
		t.Fatalf("分派的范围或者顺序不对：%q", got)
	}
}

func TestSkipsTheScopedLayersWhenTheRequestCarriesNoAgent(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userapproval.Config{}, openTurn())
	scoped, err := scope.New(scope.NewKey("somebody"), scope.Options{})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	reached := false
	h.register(t, scoped, func(context.Context, tools.ApprovalRequest, func() (tools.ApprovalOutcome, error)) (tools.ApprovalOutcome, error) {
		reached = true
		return tools.ApprovalAllowedOnce, nil
	})

	// 不带 agent 的请求只走全局层：没有身份就没有可继承的父链。
	outcome, err := h.service.Request(context.Background(), tools.ApprovalRequest{ToolName: "echo"})
	if err != nil {
		t.Fatalf("这次提问不该失败：%v", err)
	}
	if outcome != tools.ApprovalUnavailable || reached {
		t.Fatalf("不该走到任何作用域层：%q / %v", outcome, reached)
	}
}

func TestContainsABadAnswererAsUnavailable(t *testing.T) {
	t.Parallel()
	cases := map[string]userapproval.Answerer{
		"报了错": func(context.Context, tools.ApprovalRequest, func() (tools.ApprovalOutcome, error)) (tools.ApprovalOutcome, error) {
			return "", errors.New("transport died")
		},
		"panic 了": func(context.Context, tools.ApprovalRequest, func() (tools.ApprovalOutcome, error)) (tools.ApprovalOutcome, error) {
			panic("sync bug")
		},
		"还回来一个词汇表外的值": func(context.Context, tools.ApprovalRequest, func() (tools.ApprovalOutcome, error)) (tools.ApprovalOutcome, error) {
			return tools.ApprovalOutcome("yolo"), nil
		},
	}
	for label, answerer := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, userapproval.Config{}, openTurn())
			h.register(t, h.root, answerer)

			// 三种坏答复收敛成同一个失败关闭的值，而且这次提问本身不失败——
			// 一条界面侧的答复者炸了，不该把调用方那次工具调用一起带走。
			outcome, err := h.request(context.Background())
			if err != nil {
				t.Fatalf("这次提问不该失败：%v", err)
			}
			if outcome != tools.ApprovalUnavailable {
				t.Fatalf("该收敛成失败关闭，拿到 %q", outcome)
			}
			if got := h.log.field(t, 1, "outcome"); got != `"unavailable"` {
				t.Fatalf("那条 decided 该记下收敛后的值：%s", got)
			}
		})
	}
}

func TestSettlesCancelledImmediatelyWithoutAskingAnyone(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userapproval.Config{}, openTurn())
	asked := false
	h.register(t, h.root, func(context.Context, tools.ApprovalRequest, func() (tools.ApprovalOutcome, error)) (tools.ApprovalOutcome, error) {
		asked = true
		return tools.ApprovalAllowedOnce, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	outcome, err := h.request(ctx)
	if err != nil {
		t.Fatalf("这次提问不该失败：%v", err)
	}
	if outcome != tools.ApprovalCancelled || asked {
		t.Fatalf("已经取消了就该直接结算且不惊动任何人：%q / %v", outcome, asked)
	}
	// 审计那一对照样落地：这次询问确实发生过，结局是被撤回。
	if got := strings.Join(h.log.kinds(), ","); got != "approval/asked,approval/decided" {
		t.Fatalf("审计那一对该成双：%q", got)
	}
	if got := h.log.field(t, 1, "outcome"); got != `"cancelled"` {
		t.Fatalf("那条 decided 该记 cancelled：%s", got)
	}
}

func TestResolvesCancelledMidQuestionAndDiscardsTheLateAnswer(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userapproval.Config{}, openTurn())
	release := make(chan struct{})
	entered := make(chan struct{})
	h.register(t, h.root, func(context.Context, tools.ApprovalRequest, func() (tools.ApprovalOutcome, error)) (tools.ApprovalOutcome, error) {
		close(entered)
		<-release
		return tools.ApprovalAllowedOnce, nil
	})
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		<-entered
		cancel()
	}()
	outcome, err := h.request(ctx)
	if err != nil {
		t.Fatalf("这次提问不该失败：%v", err)
	}
	if outcome != tools.ApprovalCancelled {
		t.Fatalf("取消该赢下这场赛跑，拿到 %q", outcome)
	}
	// 迟到的那个 allowed-once 由构造本身丢掉：容量 1 的 channel 收下它，没人再读，
	// 而那个 goroutine 送完就走。一次被撤回的询问不许在几秒后变成一次授权。
	close(release)
	if got := strings.Join(h.log.kinds(), ","); got != "approval/asked,approval/decided" {
		t.Fatalf("不该多出第二条 decided：%q", got)
	}
	if got := h.log.field(t, 1, "outcome"); got != `"cancelled"` {
		t.Fatalf("那条 decided 该记 cancelled：%s", got)
	}
}

func TestIssuesAFreshIDPerRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userapproval.Config{}, openTurn())

	for range 2 {
		if _, err := h.request(context.Background()); err != nil {
			t.Fatalf("这次提问不该失败：%v", err)
		}
	}
	if first, second := h.log.field(t, 0, "id"), h.log.field(t, 2, "id"); first == second {
		t.Fatalf("每次提问该换一个新 id，两次都是 %s", first)
	}
}

func TestFallsBackToUUIDWithoutAConfiguredGenerator(t *testing.T) {
	t.Parallel()
	log := &fakeLog{events: openTurn()}
	service, err := userapproval.New(userapproval.Config{
		LogOf:  func(*scope.Key) (userapproval.Log, error) { return log, nil },
		Notify: func(*scope.Key, llm.Message) error { return nil },
	})
	if err != nil {
		t.Fatalf("造服务失败：%v", err)
	}

	if _, err := service.Request(context.Background(), tools.ApprovalRequest{ToolName: "echo"}); err != nil {
		t.Fatalf("这次提问不该失败：%v", err)
	}
	// 只断言「发出来了、而且不是空的」：uuid 的具体字节不是本包的契约。
	if got := log.field(t, 0, "id"); len(got) < 10 {
		t.Fatalf("没配发号器时该回落到 uuid，拿到 %s", got)
	}
}

func TestDropsADisposedAnswererFromTheChain(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userapproval.Config{}, openTurn())
	undo := h.register(t, h.root, func(context.Context, tools.ApprovalRequest, func() (tools.ApprovalOutcome, error)) (tools.ApprovalOutcome, error) {
		return tools.ApprovalAllowedOnce, nil
	})
	if outcome, _ := h.request(context.Background()); outcome != tools.ApprovalAllowedOnce {
		t.Fatalf("前置条件：接上来的答复者该答话，拿到 %q", outcome)
	}

	// 热重载把一个插件卸掉之后，它那条答复者必须立刻从链上消失。
	if err := undo(context.Background()); err != nil {
		t.Fatalf("注销失败：%v", err)
	}
	if outcome, _ := h.request(context.Background()); outcome != tools.ApprovalUnavailable {
		t.Fatalf("注销之后该回到失败关闭，拿到 %q", outcome)
	}
}

func TestNeverRejectsDeterministicallyWithoutConsultingAnyAnswerer(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userapproval.Config{Policy: userapproval.PolicyNever}, openTurn())
	consulted := false
	h.register(t, h.root, func(context.Context, tools.ApprovalRequest, func() (tools.ApprovalOutcome, error)) (tools.ApprovalOutcome, error) {
		consulted = true
		return tools.ApprovalAllowedOnce, nil
	})

	// 这一条答复者是**后**接上来的，而且它会当场放行——闸要是做成一条答复者，
	// 它就绕过去了。
	outcome, err := h.request(context.Background())
	if err != nil {
		t.Fatalf("这次提问不该失败：%v", err)
	}
	if outcome != tools.ApprovalRejected || consulted {
		t.Fatalf("never 该在派发之前就判掉：%q / %v", outcome, consulted)
	}
	// 审计那一对照样落到日志上。
	if got := strings.Join(h.log.kinds(), ","); got != "approval/asked,approval/decided" {
		t.Fatalf("审计那一对该成双：%q", got)
	}
}

func TestASessionOverrideOutranksTheConfiguredDefaultInBothDirections(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userapproval.Config{Policy: userapproval.PolicyNever}, openTurn())
	h.answer(t, tools.ApprovalAllowedOnce)

	if _, exists := h.service.OverrideOf(h.log); exists {
		t.Fatal("前置条件：这条会话还没切过策略")
	}
	if got := h.service.PolicyFor(h.log); got != userapproval.PolicyNever {
		t.Fatalf("没有覆盖时该用部署默认值，拿到 %q", got)
	}

	if err := userapproval.SetPolicy(h.log, userapproval.PolicyAsk); err != nil {
		t.Fatalf("切策略失败：%v", err)
	}
	if got, exists := h.service.OverrideOf(h.log); !exists || got != userapproval.PolicyAsk {
		t.Fatalf("该读出那条覆盖：%q / %v", got, exists)
	}
	if outcome, _ := h.request(context.Background()); outcome != tools.ApprovalAllowedOnce {
		t.Fatalf("覆盖成 ask 之后该问得出去，拿到 %q", outcome)
	}

	if err := userapproval.SetPolicy(h.log, userapproval.PolicyNever); err != nil {
		t.Fatalf("切策略失败：%v", err)
	}
	if outcome, _ := h.request(context.Background()); outcome != tools.ApprovalRejected {
		t.Fatalf("覆盖回 never 之后该确定地拒绝，拿到 %q", outcome)
	}
}

func TestSetPolicyRefusesAValueOutsideTheClosedVocabulary(t *testing.T) {
	t.Parallel()
	log := &fakeLog{}

	err := userapproval.SetPolicy(log, userapproval.Policy("sometimes"))
	if err == nil || !strings.Contains(err.Error(), `"ask"`) || !strings.Contains(err.Error(), `"never"`) {
		t.Fatalf("该点名那两个合法值：%v", err)
	}
	// 拒在日志变化之前：一条词汇表外的策略一旦写进去，每一次回放都会被它绊住。
	if len(log.appended) != 0 {
		t.Fatalf("被拒了就不该写日志：%+v", log.kinds())
	}
}

func TestFoldsToTheLastPolicyEventOrNone(t *testing.T) {
	t.Parallel()
	log := &fakeLog{}

	if _, exists := userapproval.EffectivePolicy(log.Events()); exists {
		t.Fatal("一条都没有时该说没有")
	}
	for _, policy := range []userapproval.Policy{userapproval.PolicyNever, userapproval.PolicyAsk} {
		if err := userapproval.SetPolicy(log, policy); err != nil {
			t.Fatalf("切策略失败：%v", err)
		}
	}
	if got, exists := userapproval.EffectivePolicy(log.Events()); !exists || got != userapproval.PolicyAsk {
		t.Fatalf("该折到最后那条：%q / %v", got, exists)
	}
	if got := string(log.appended[1].raw); got != `{"policy":"ask"}` {
		t.Fatalf("一次运行期切换不带 source：%s", got)
	}
}

func TestTheFoldSkipsAPolicyEventItCannotRead(t *testing.T) {
	t.Parallel()
	// 读路径的职责是「当前策略是什么」，不是「这条日志合不合法」——后者归 Trace，
	// 而且那边会把同一条坏事件报成一条明确的违例。
	events := []sessionlog.Event{
		eventOf(t, userapproval.EventPolicy, userapproval.PolicyData{Policy: userapproval.PolicyNever}),
		{Type: userapproval.EventPolicy, Data: json.RawMessage(`"nope"`)},
	}

	if got, exists := userapproval.EffectivePolicy(events); !exists || got != userapproval.PolicyNever {
		t.Fatalf("该跳过读不回来的那条，接着往前找：%q / %v", got, exists)
	}
}

func TestQueuesALivePolicySwitchForTheNextModelStep(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userapproval.Config{}, openTurn())

	if err := h.service.SwitchPolicy(h.agent, userapproval.PolicyNever); err != nil {
		t.Fatalf("切策略失败：%v", err)
	}
	// 值没变就什么都不做：既不写日志，也不拿一条「从 never 变成 never」的通知去占
	// 模型的上下文。
	if err := h.service.SwitchPolicy(h.agent, userapproval.PolicyNever); err != nil {
		t.Fatalf("第二次切策略失败：%v", err)
	}

	if got, _ := userapproval.EffectivePolicy(h.log.Events()); got != userapproval.PolicyNever {
		t.Fatalf("该切到 never，拿到 %q", got)
	}
	if len(h.log.appended) != 1 {
		t.Fatalf("该只写一条：%+v", h.log.kinds())
	}
	if len(h.notices) != 1 {
		t.Fatalf("该只通知一次：%+v", h.notices)
	}
	notice := h.notices[0]
	if got := textOf(notice); got != `The approval policy changed from "ask" to "never" (changed by the user).` {
		t.Fatalf("那句通知不对：%q", got)
	}
	source, ok := notice.Source.(llm.PluginSource)
	if !ok || source.Plugin != userapproval.PluginName {
		t.Fatalf("那条通知该署本包的名：%+v", notice.Source)
	}
	if notice.Role != llm.RoleUser {
		t.Fatalf("那条通知该是一条用户消息，拿到 %q", notice.Role)
	}
}

func TestSwitchPolicyRefusesAValueOutsideTheClosedVocabulary(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userapproval.Config{}, nil)

	if err := h.service.SwitchPolicy(h.agent, userapproval.Policy("sometimes")); err == nil {
		t.Fatal("词汇表外的值该被拒")
	}
	if len(h.log.appended) != 0 || len(h.notices) != 0 {
		t.Fatalf("被拒了就不该留下痕迹：%+v / %+v", h.log.kinds(), h.notices)
	}
}

func TestPropagatesAFailureFromTheLogSeam(t *testing.T) {
	t.Parallel()
	broken := errors.New("这个 agent 没会话")
	service, err := userapproval.New(userapproval.Config{
		LogOf:  func(*scope.Key) (userapproval.Log, error) { return nil, broken },
		Notify: func(*scope.Key, llm.Message) error { return nil },
	})
	if err != nil {
		t.Fatalf("造服务失败：%v", err)
	}

	if _, err := service.Request(context.Background(), tools.ApprovalRequest{ToolName: "echo"}); !errors.Is(err, broken) {
		t.Fatalf("Request 该原样报出接缝的错：%v", err)
	}
	if err := service.SwitchPolicy(nil, userapproval.PolicyNever); !errors.Is(err, broken) {
		t.Fatalf("SwitchPolicy 该原样报出接缝的错：%v", err)
	}
}

func TestRefusesALogSeamThatAnswersWithNothing(t *testing.T) {
	t.Parallel()
	service, err := userapproval.New(userapproval.Config{
		LogOf:  func(*scope.Key) (userapproval.Log, error) { return nil, nil },
		Notify: func(*scope.Key, llm.Message) error { return nil },
	})
	if err != nil {
		t.Fatalf("造服务失败：%v", err)
	}

	// 一条 (nil, nil) 的答复是装配方的 bug；在这儿说清楚，比在折叠里解引用炸掉有用。
	_, err = service.Request(context.Background(), tools.ApprovalRequest{ToolName: "echo"})
	if !errors.Is(err, userapproval.ErrInvalidConfig) {
		t.Fatalf("该报一条说得清的配置错：%v", err)
	}
}

func TestPropagatesANotifyFailure(t *testing.T) {
	t.Parallel()
	broken := errors.New("这个 agent 已经不在了")
	log := &fakeLog{}
	service, err := userapproval.New(userapproval.Config{
		LogOf:  func(*scope.Key) (userapproval.Log, error) { return log, nil },
		Notify: func(*scope.Key, llm.Message) error { return broken },
	})
	if err != nil {
		t.Fatalf("造服务失败：%v", err)
	}

	if err := service.SwitchPolicy(nil, userapproval.PolicyNever); !errors.Is(err, broken) {
		t.Fatalf("通知失败该报出来：%v", err)
	}
}

func TestPolicyStatementSaysTheCompleteCurrentValue(t *testing.T) {
	t.Parallel()
	if got := userapproval.PolicyStatement(userapproval.PolicyNever); got != userapproval.NeverStatement {
		t.Fatalf("never 那句不对：%q", got)
	}
	if !strings.Contains(userapproval.NeverStatement, "sandbox_permissions") {
		t.Fatalf("never 那句该拦住模型去要沙箱升权：%q", userapproval.NeverStatement)
	}
	// 空串（也就是 New 会补成 ask 的那个零值）落在 ask 那一句上。
	for _, policy := range []userapproval.Policy{userapproval.PolicyAsk, ""} {
		if got := userapproval.PolicyStatement(policy); got != userapproval.AskStatement {
			t.Fatalf("%q 该给 ask 那句，拿到 %q", policy, got)
		}
	}
}

func TestPublishesItsOwnEventVocabulary(t *testing.T) {
	t.Parallel()
	vocabulary := sessionlog.CoreVocabulary().With(userapproval.EventTypes()...)

	for _, kind := range userapproval.EventTypes() {
		if !vocabulary.Knows(kind) {
			t.Fatalf("拼进去之后该认得 %q", kind)
		}
		// 拼之前不认得——这正是这张单子存在的理由。
		if sessionlog.CoreVocabulary().Knows(kind) {
			t.Fatalf("核心词汇表不该自己就认得 %q", kind)
		}
	}
}

func TestTheClosedVocabulariesRejectOutsiders(t *testing.T) {
	t.Parallel()
	if got := len(userapproval.Policies()); got != 2 {
		t.Fatalf("策略该恰好有两个，拿到 %d", got)
	}
	if got := len(userapproval.Outcomes()); got != 4 {
		t.Fatalf("答复该恰好有四个，拿到 %d", got)
	}
	for _, policy := range userapproval.Policies() {
		if !userapproval.KnownPolicy(policy) {
			t.Fatalf("%q 该被认得", policy)
		}
	}
	for _, outcome := range userapproval.Outcomes() {
		if !userapproval.KnownOutcome(outcome) {
			t.Fatalf("%q 该被认得", outcome)
		}
	}
	if userapproval.KnownPolicy("sometimes") || userapproval.KnownOutcome("yolo") {
		t.Fatal("表外的值不该被认得")
	}
}

func TestNewRejectsAConfigThatCannotWork(t *testing.T) {
	t.Parallel()
	notify := func(*scope.Key, llm.Message) error { return nil }
	logOf := func(*scope.Key) (userapproval.Log, error) { return &fakeLog{}, nil }
	cases := map[string]userapproval.Config{
		"没有日志":     {Notify: notify},
		"没有通知":     {LogOf: logOf},
		"默认策略不在表里": {LogOf: logOf, Notify: notify, Policy: userapproval.Policy("sometimes")},
	}
	for label, config := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			if _, err := userapproval.New(config); !errors.Is(err, userapproval.ErrInvalidConfig) {
				t.Fatalf("这份配置该被拒：%v", err)
			}
		})
	}
}

func TestDefaultsAnEmptyConfigPolicyToAsk(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userapproval.Config{}, openTurn())
	h.answer(t, tools.ApprovalAllowedOnce)

	if got := h.service.PolicyFor(h.log); got != userapproval.PolicyAsk {
		t.Fatalf("空的默认策略该补成 ask，拿到 %q", got)
	}
	if outcome, _ := h.request(context.Background()); outcome != tools.ApprovalAllowedOnce {
		t.Fatalf("ask 该问得出去，拿到 %q", outcome)
	}
}

func TestRegisterAnswererRejectsNil(t *testing.T) {
	t.Parallel()
	h := newHarness(t, userapproval.Config{}, nil)

	if _, err := h.service.RegisterAnswerer(context.Background(), h.root, nil); !errors.Is(err, userapproval.ErrInvalidConfig) {
		t.Fatalf("nil 答复者该被拒：%v", err)
	}
	if _, err := h.service.RegisterAnswerer(context.Background(), nil, func(context.Context, tools.ApprovalRequest, func() (tools.ApprovalOutcome, error)) (tools.ApprovalOutcome, error) {
		return tools.ApprovalRejected, nil
	}); err == nil {
		t.Fatal("没有宿主作用域该被拒")
	}
}

// ---- 那条持久不变量 ----
//
// 逐条对着 DSH 的 tests/invariant.spec.ts 走。

// askedEvent 造一条 approval/asked。
func askedEvent(t *testing.T, id userapproval.RequestID, toolName string) sessionlog.Event {
	t.Helper()
	return eventOf(t, userapproval.EventAsked, userapproval.AskedData{ID: id, ToolName: toolName})
}

// decidedEvent 造一条 approval/decided。
func decidedEvent(t *testing.T, id userapproval.RequestID, outcome tools.ApprovalOutcome) sessionlog.Event {
	t.Helper()
	return eventOf(t, userapproval.EventDecided, userapproval.DecidedData{ID: id, Outcome: outcome})
}

// brokenEvent 造一条负载读不回来的事件。
func brokenEvent(kind sessionlog.EventType) sessionlog.Event {
	return sessionlog.Event{Type: kind, Data: json.RawMessage(`"not an object"`)}
}

func TestTheTraceAcceptsAPairedAuditAndAClosedPolicyValue(t *testing.T) {
	t.Parallel()
	// DSH: 'accepts paired audit events and closed policy values'
	trace, err := userapproval.ValidateLog([]sessionlog.Event{
		{Type: sessionlog.EventTurnStart},
		askedEvent(t, "ask-1", "bash"),
		decidedEvent(t, "ask-1", tools.ApprovalAllowedOnce),
		eventOf(t, userapproval.EventPolicy, userapproval.PolicyData{Policy: userapproval.PolicyNever}),
		{Type: sessionlog.EventTurnEnd},
	})
	if err != nil {
		t.Fatalf("这段日志该被接受：%v", err)
	}
	if got := trace.Pending(); got != 0 {
		t.Fatalf("走完之后不该还挂着询问，挂着 %d 条", got)
	}
}

func TestTheTraceIgnoresEventsThatAreNotItsOwn(t *testing.T) {
	t.Parallel()
	// 别的包写进同一条日志的事件不归本包管，一条都不许拦。
	trace, err := userapproval.ValidateLog([]sessionlog.Event{
		{Type: sessionlog.EventTurnStart},
		{Type: sessionlog.EventUserMessage},
		{Type: sessionlog.EventType("todo/snapshot"), Data: json.RawMessage(`{"todos":[]}`)},
	})
	if err != nil {
		t.Fatalf("别人的事件该被放过：%v", err)
	}
	if got := trace.Pending(); got != 0 {
		t.Fatalf("不该挂着任何询问，挂着 %d 条", got)
	}
}

func TestTheTraceCarriesAnUnmatchedQuestionForwardAcrossInstallation(t *testing.T) {
	t.Parallel()
	// DSH: 'rebuilds an unmatched question from an existing session'
	//
	// 装载时那条轨迹**接着**往下走，所以一条写在装载之后的 approval/decided
	// 认得出它那条写在装载之前的 approval/asked。
	h := newInvariantHarness(t,
		sessionlog.Event{Type: sessionlog.EventTurnStart},
		askedEvent(t, "ask-resume", "bash"),
	)
	h.register(t)

	h.emit(decidedEvent(t, "ask-resume", tools.ApprovalCancelled))
	h.emit(sessionlog.Event{Type: sessionlog.EventTurnEnd})
}

func TestTheTraceCatchesAnUnenclosedAuditAlreadyInTheLog(t *testing.T) {
	t.Parallel()
	// DSH: 'rejects an unenclosed audit event when replaying an existing session'
	//
	// 一份历史里就带着拆了围栏的审计的会话，必须在装载这一刻就响。
	h := newInvariantHarness(t,
		sessionlog.Event{Type: sessionlog.EventTurnStart},
		sessionlog.Event{Type: sessionlog.EventTurnEnd},
		askedEvent(t, "ask-replay", "bash"),
	)

	failure := violation(t, func() { h.register(t) })
	if failure.PackageName != userapproval.PackageName {
		t.Fatalf("该报在本包名下：%q", failure.PackageName)
	}
	if !strings.Contains(failure.Message, "outside any open turn") {
		t.Fatalf("该带上那条违例本身：%q", failure.Message)
	}
}

func TestTheTraceCatchesAnUnenclosedAuditAppendedLater(t *testing.T) {
	t.Parallel()
	// DSH: 'rejects audit events outside any open turn'
	h := newInvariantHarness(t)
	h.register(t)

	asked := violation(t, func() { h.emit(askedEvent(t, "ask-1", "bash")) })
	if !strings.Contains(asked.Message, "approval/asked appended outside any open turn") {
		t.Fatalf("该点名是提问越了围栏：%q", asked.Message)
	}
	decided := violation(t, func() { h.emit(decidedEvent(t, "ask-1", tools.ApprovalRejected)) })
	if !strings.Contains(decided.Message, "approval/decided appended outside any open turn") {
		t.Fatalf("该点名是结算越了围栏：%q", decided.Message)
	}
}

func TestTheTraceCatchesMalformedAndUnpairedAudits(t *testing.T) {
	t.Parallel()
	// DSH: 'rejects malformed and unpaired audit events'
	//
	// 一条不能落地的事件不许改动轨迹，所以下面这些违例是接着**同一条**轨迹连着报的：
	// 空 toolName 报完之后，那个 id 仍然没被记成未结算。
	h := newInvariantHarness(t, sessionlog.Event{Type: sessionlog.EventTurnStart})
	h.register(t)

	empty := violation(t, func() { h.emit(askedEvent(t, "ask-1", "")) })
	if !strings.Contains(empty.Message, "toolName must be non-empty") {
		t.Fatalf("空工具名该被点名：%q", empty.Message)
	}

	h.emit(askedEvent(t, "ask-1", "bash"))

	repeated := violation(t, func() { h.emit(askedEvent(t, "ask-1", "bash")) })
	if !strings.Contains(repeated.Message, `repeated open id "ask-1"`) {
		t.Fatalf("重复的未结算 id 该被点名：%q", repeated.Message)
	}
	missing := violation(t, func() { h.emit(decidedEvent(t, "missing", tools.ApprovalRejected)) })
	if !strings.Contains(missing.Message, "no matching approval/asked") {
		t.Fatalf("配不上对的结算该被点名：%q", missing.Message)
	}
	outcome := violation(t, func() { h.emit(decidedEvent(t, "ask-1", tools.ApprovalOutcome("maybe"))) })
	if !strings.Contains(outcome.Message, `unknown outcome "maybe"`) {
		t.Fatalf("表外的答复该被点名：%q", outcome.Message)
	}
	policy := violation(t, func() {
		h.emit(eventOf(t, userapproval.EventPolicy, userapproval.PolicyData{Policy: userapproval.Policy("always")}))
	})
	if !strings.Contains(policy.Message, `unknown policy "always"`) {
		t.Fatalf("表外的策略该被点名：%q", policy.Message)
	}

	// 上面每一条都被拒了，那个 id 因此仍然挂着，一条正常的结算还得能把它划掉。
	h.emit(decidedEvent(t, "ask-1", tools.ApprovalAllowedOnce))
}

func TestTheTraceCatchesAnUnreadablePayload(t *testing.T) {
	t.Parallel()
	// 新增: DSH 那边负载由 session 的 schema 挡在前面，读不回来的字节根本进不了检查。
	// Go 这边负载是 json.RawMessage，坏字节走得到这里，所以这三条各自要说清是谁坏了。
	cases := map[sessionlog.EventType]string{
		userapproval.EventAsked:   "approval/asked carries an unreadable payload",
		userapproval.EventDecided: "approval/decided carries an unreadable payload",
		userapproval.EventPolicy:  "approval/policy carries an unreadable payload",
	}
	for kind, want := range cases {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()
			_, err := userapproval.ValidateLog([]sessionlog.Event{
				{Type: sessionlog.EventTurnStart},
				brokenEvent(kind),
			})
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("该报 %q，拿到：%v", want, err)
			}
		})
	}
}

func TestTheTraceReportsWhatIsStillPending(t *testing.T) {
	t.Parallel()
	// 一段停在半路的日志（问了还没结算）本身是合法的——那正是一次还等着人回答的询问。
	trace, err := userapproval.ValidateLog([]sessionlog.Event{
		{Type: sessionlog.EventTurnStart},
		askedEvent(t, "ask-1", "bash"),
		askedEvent(t, "ask-2", "edit"),
		decidedEvent(t, "ask-1", tools.ApprovalRejected),
	})
	if err != nil {
		t.Fatalf("半路上的日志该被接受：%v", err)
	}
	if got := trace.Pending(); got != 1 {
		t.Fatalf("该只剩一条挂着，剩 %d 条", got)
	}
}

func TestUnregisteringTheApprovalInvariantsStopsTheCheck(t *testing.T) {
	t.Parallel()
	h := newInvariantHarness(t)
	undo := h.register(t)
	undo()

	if h.unsubscribed != 1 {
		t.Fatalf("注销时该退订，退订了 %d 次", h.unsubscribed)
	}
}

func TestRegisterApprovalInvariantsNeedsAllThreeSeams(t *testing.T) {
	t.Parallel()
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("造注册表失败：%v", err)
	}
	loaded := func() []sessionlog.Event { return nil }
	subscribe := func(func(sessionlog.Event)) func() { return func() {} }

	cases := map[string]func() error{
		"没给注册表": func() error {
			_, err := userapproval.RegisterInvariants(context.Background(), nil, loaded, subscribe)
			return err
		},
		"没给已装载日志": func() error {
			_, err := userapproval.RegisterInvariants(context.Background(), registry, nil, subscribe)
			return err
		},
		"没给订阅": func() error {
			_, err := userapproval.RegisterInvariants(context.Background(), registry, loaded, nil)
			return err
		},
	}
	for label, run := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			if err := run(); !errors.Is(err, userapproval.ErrInvalidConfig) {
				t.Fatalf("该被拒绝并认得出哨兵：%v", err)
			}
		})
	}
}

// invariantHarness 是一次不变量测试要的家当。
type invariantHarness struct {
	registry  *invariants.Registry
	loaded    []sessionlog.Event
	observers []func(sessionlog.Event)
	// unsubscribed 记下退订被调了几次。
	unsubscribed int
}

// register 把本包的检查装进去。
func (h *invariantHarness) register(t *testing.T) func() {
	t.Helper()
	undo, err := userapproval.RegisterInvariants(
		context.Background(),
		h.registry,
		func() []sessionlog.Event { return h.loaded },
		func(observer func(sessionlog.Event)) func() {
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
func (h *invariantHarness) emit(event sessionlog.Event) {
	for _, observer := range h.observers {
		observer(event)
	}
}

// newInvariantHarness 造一个开着的注册表。
func newInvariantHarness(t *testing.T, loaded ...sessionlog.Event) *invariantHarness {
	t.Helper()
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("造注册表失败：%v", err)
	}
	return &invariantHarness{registry: registry, loaded: loaded}
}

// violation 跑一段会违例的代码，交出那条违例。
func violation(t *testing.T, run func()) *invariants.Error {
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
