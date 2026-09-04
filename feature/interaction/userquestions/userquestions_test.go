// 本文件的作用：把那道门的全部可观察行为钉住——什么请求根本到不了界面、
// 到得了的那些原样传过去没有、以及那个插槽的排他性。
//
// 逐条对着 DSH 的 tests/user-questions.spec.ts 走。那边有两条在 Go 这边合成了一条：
// 「没有活注册表」和「拿一个复用了活 id 的过期对象来问」在 DSH 里是两种查法
// （ctx.get('agents') 拿不到、agents.get(id) !== agent），在 Go 里都是
// [userquestions.CallerUnknown] 这一个答案。
//
// # 这些测试防的是什么错
//
//   - **一个坏请求已经画到了界面上**。全部校验必须走在把请求交出去之前：
//     一个「同意」键点名了根本没提供的选项的问题，一旦画出来，用户点什么都不对。
//   - **一个被拥有的子 agent 把问题摆到了人面前**。它背后没有人在看，问了就是
//     永远等下去，而那个卡住的是它上面整条链。
//   - **第二个界面顶掉了第一个**。正在等人回答的那次提问挂在被换走的那个界面上，
//     新界面根本不知道有这回事。
package userquestions_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/feature/interaction/userquestions"
	"github.com/snight1983/ds-harness-go/scope"
)

// stubProvider 是一个把每次请求都记下来的假界面。
type stubProvider struct {
	seen  []userquestions.Request
	reply string
	// failWith 非 nil 时，每一次提问都以它失败。
	failWith error
}

// Ask 记下这次请求，用固定的那个标签回答第一个问题。
func (p *stubProvider) Ask(_ context.Context, request userquestions.Request) (userquestions.Answer, error) {
	p.seen = append(p.seen, request)
	if p.failWith != nil {
		return userquestions.Answer{}, p.failWith
	}
	id := "missing"
	if len(request.Questions) > 0 {
		id = request.Questions[0].ID
	}
	return userquestions.Answer{
		Answers: []userquestions.AnswerItem{{ID: id, Selected: []string{p.reply}}},
	}, nil
}

// newService 造一个接好假界面的服务。
func newService(t *testing.T, config userquestions.Config, reply string) (*userquestions.Service, *stubProvider) {
	t.Helper()
	service := userquestions.New(config)
	provider := &stubProvider{reply: reply}
	undo, err := service.RegisterProvider(provider)
	if err != nil {
		t.Fatalf("接入提供方失败：%v", err)
	}
	t.Cleanup(undo)
	return service, provider
}

// ask 问一个最普通的问题。
func ask(service *userquestions.Service, agent *scope.Key) (userquestions.Answer, error) {
	return service.Ask(context.Background(), userquestions.Request{
		Questions: []userquestions.Item{{ID: "confirm", Question: "Proceed?"}},
		Agent:     agent,
	})
}

// wantCode 断言这个错误是本包的、且是这个代号。
func wantCode(t *testing.T, err error, code string) *userquestions.Error {
	t.Helper()
	var typed *userquestions.Error
	if !errors.As(err, &typed) {
		t.Fatalf("该是本包的错误，拿到：%v", err)
	}
	if typed.Code != code {
		t.Fatalf("代号该是 %q，拿到 %q（%s）", code, typed.Code, typed.Message)
	}
	if typed.ErrorName() != "UserQuestionError" || typed.ErrorCode() != code {
		// 这两个方法是给 tools 那道结果收敛认的：它靠它们把这条错误的身份
		// 抄进 Failure.Info，下游才不必解析错误文本。
		t.Fatalf("身份不对：%q / %q", typed.ErrorName(), typed.ErrorCode())
	}
	if typed.Error() != typed.Message {
		t.Fatalf("错误文本该就是那句话：%q", typed.Error())
	}
	if got := userquestions.CodeOf(err); got != code {
		t.Fatalf("CodeOf 该认出同一个代号，拿到 %q", got)
	}
	return typed
}

func TestHandsTheRequestToTheRegisteredProvider(t *testing.T) {
	t.Parallel()
	service, provider := newService(t, userquestions.Config{}, "yes")

	answer, err := ask(service, nil)
	if err != nil {
		t.Fatalf("这次提问不该失败：%v", err)
	}
	if len(answer.Answers) != 1 || answer.Answers[0].ID != "confirm" ||
		strings.Join(answer.Answers[0].Selected, ",") != "yes" {
		t.Fatalf("该把界面给的那份答案原样交回来：%+v", answer)
	}
	if len(provider.seen) != 1 || provider.seen[0].Questions[0].Question != "Proceed?" {
		t.Fatalf("该把那份请求原样交给界面：%+v", provider.seen)
	}
}

func TestPassesTheProvidersFailureThrough(t *testing.T) {
	t.Parallel()
	service, provider := newService(t, userquestions.Config{}, "yes")
	provider.failWith = errors.New("界面掉线了")

	if _, err := ask(service, nil); err == nil || !strings.Contains(err.Error(), "界面掉线了") {
		t.Fatalf("界面报的错该原样交出来：%v", err)
	}
}

func TestRefusesToAskWithoutAProvider(t *testing.T) {
	t.Parallel()
	service := userquestions.New(userquestions.Config{})

	_, err := ask(service, nil)
	wantCode(t, err, userquestions.CodeNoProvider)
}

func TestUnregisteringLeavesTheSlotEmptyAndIsIdempotent(t *testing.T) {
	t.Parallel()
	service := userquestions.New(userquestions.Config{})
	undo, err := service.RegisterProvider(&stubProvider{reply: "yes"})
	if err != nil {
		t.Fatalf("接入提供方失败：%v", err)
	}

	undo()
	// 第二次注销必须是空操作：热重载会把同一个注销函数调不止一次，而那时插槽里
	// 可能已经站着**下一个**界面了——再清一次就把它清掉了。
	undo()
	if _, err := ask(service, nil); userquestions.CodeOf(err) != userquestions.CodeNoProvider {
		t.Fatalf("注销之后就没有界面了：%v", err)
	}

	next := &stubProvider{reply: "second"}
	if _, err := service.RegisterProvider(next); err != nil {
		t.Fatalf("腾空之后该接得进来：%v", err)
	}
	undo()
	if len(next.seen) != 0 {
		t.Fatal("前置条件：新界面还没被问过")
	}
	if _, err := ask(service, nil); err != nil {
		t.Fatalf("旧的注销函数不该把新界面清掉：%v", err)
	}
}

func TestRefusesASecondProvider(t *testing.T) {
	t.Parallel()
	service, _ := newService(t, userquestions.Config{}, "first")

	undo, err := service.RegisterProvider(&stubProvider{reply: "second"})
	wantCode(t, err, userquestions.CodeDuplicateProvider)
	if undo != nil {
		t.Fatal("被拒了就不该给出注销函数")
	}
	// 顶掉的话，一次正在等人回答的提问会永远挂在那个被换走的界面上。
	answer, err := ask(service, nil)
	if err != nil {
		t.Fatalf("原来那个界面该还在：%v", err)
	}
	if answer.Answers[0].Selected[0] != "first" {
		t.Fatalf("回答的该还是原来那个界面：%+v", answer)
	}
}

func TestRefusesANilProvider(t *testing.T) {
	t.Parallel()
	service := userquestions.New(userquestions.Config{})

	_, err := service.RegisterProvider(nil)
	wantCode(t, err, userquestions.CodeNoProvider)
}

func TestRefusesAnAlreadyCancelledAsk(t *testing.T) {
	t.Parallel()
	service, provider := newService(t, userquestions.Config{}, "too late")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := service.Ask(ctx, userquestions.Request{
		Questions: []userquestions.Item{{ID: "confirm", Question: "Proceed?"}},
	})
	wantCode(t, err, userquestions.CodeAskAborted)
	if len(provider.seen) != 0 {
		t.Fatal("已经取消了就不该再惊动人")
	}
}

func TestRefusesAnEmptyBatch(t *testing.T) {
	t.Parallel()
	service, provider := newService(t, userquestions.Config{}, "yes")

	_, err := service.Ask(context.Background(), userquestions.Request{})
	wantCode(t, err, userquestions.CodeEmptyQuestions)
	if len(provider.seen) != 0 {
		t.Fatal("一个问题都没有就不该惊动界面")
	}
}

func TestRefusesACallerNoLiveRegistryCanAttest(t *testing.T) {
	t.Parallel()
	// 没接 CallerStatus 就是「这个装配里没有活 agent 注册表」。零值是最紧的那个
	// 答案：宁可拒掉一次合法的提问，也不让一个没人能证实的调用方摆到用户面前。
	service, provider := newService(t, userquestions.Config{}, "yes")

	_, err := ask(service, scope.NewKey("unattested"))
	wantCode(t, err, userquestions.CodeCallerNotLive)
	if len(provider.seen) != 0 {
		t.Fatal("求证不过就不该惊动界面")
	}
}

func TestRefusesAnAgentOwnedByAnotherLiveAgent(t *testing.T) {
	t.Parallel()
	child := scope.NewKey("child")
	service, provider := newService(t, userquestions.Config{
		CallerStatus: func(agent *scope.Key) userquestions.CallerStatus {
			if agent == child {
				return userquestions.CallerDelegated
			}
			return userquestions.CallerRoot
		},
	}, "yes")

	// 一个被拥有的子 agent 背后没有人在看，问了就是永远等下去。
	_, err := ask(service, child)
	failure := wantCode(t, err, userquestions.CodeDelegatedCaller)
	if !strings.Contains(failure.Message, "final result") {
		t.Fatalf("该告诉它把这个未决问题写进自己的最终结果：%q", failure.Message)
	}
	if len(provider.seen) != 0 {
		t.Fatal("被拒了就不该惊动界面")
	}
}

func TestALineageBearingSessionResumedAsARootMayAsk(t *testing.T) {
	t.Parallel()
	// 定这条界线的是运行期归属，不是持久的会话血缘。
	root := scope.NewKey("resumed-root")
	service, provider := newService(t, userquestions.Config{
		CallerStatus: func(*scope.Key) userquestions.CallerStatus { return userquestions.CallerRoot },
	}, "yes")

	if _, err := ask(service, root); err != nil {
		t.Fatalf("一个运行期的根该问得出来：%v", err)
	}
	if len(provider.seen) != 1 || provider.seen[0].Agent != root {
		t.Fatalf("该把那个调用方一起交给界面：%+v", provider.seen)
	}
}

func TestSkipsTheAttestationWhenNoAgentIsSupplied(t *testing.T) {
	t.Parallel()
	// 不带 agent 的请求（界面自己发起的、装配期的）没有可求证的对象，
	// 也就没有「代人受过」的风险。
	asked := false
	service, _ := newService(t, userquestions.Config{
		CallerStatus: func(*scope.Key) userquestions.CallerStatus {
			asked = true
			return userquestions.CallerUnknown
		},
	}, "yes")

	if _, err := ask(service, nil); err != nil {
		t.Fatalf("不带 agent 的请求不该被拦：%v", err)
	}
	if asked {
		t.Fatal("没有 agent 就根本不该去求证")
	}
}

func TestRefusesAnIntentWhoseApproveLabelNamesNoneOfItsOptions(t *testing.T) {
	t.Parallel()
	cases := map[string][]userquestions.Option{
		"选项里没有这个标签": {{Label: "Approve"}},
		"一个选项都没给":   nil,
	}
	for label, options := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			service, provider := newService(t, userquestions.Config{}, "yes")

			_, err := service.Ask(context.Background(), userquestions.Request{
				Questions: []userquestions.Item{{
					ID: "plan-review", Question: "Approve?", Detail: "# Plan",
					Options: options,
					Intent:  userquestions.PlanReviewIntent{Approve: "Ship it"},
				}},
			})
			failure := wantCode(t, err, userquestions.CodeBadIntent)
			if !strings.Contains(failure.Message, `"Ship it"`) ||
				!strings.Contains(failure.Message, "plan-review") {
				t.Fatalf("该点名那个标签和那个意图：%q", failure.Message)
			}
			if len(provider.seen) != 0 {
				t.Fatal("一个用户点什么都不对的问题不该画出来")
			}
		})
	}
}

func TestRefusesAPlanReviewCarryingNoPlan(t *testing.T) {
	t.Parallel()
	service, provider := newService(t, userquestions.Config{}, "yes")

	// Detail **就是**这个意图要评的那份计划，缺了它，一个照着意图画的界面会请用户
	// 同意一样他看不见的东西。
	_, err := service.Ask(context.Background(), userquestions.Request{
		Questions: []userquestions.Item{{
			ID: "plan-review", Question: "Approve?",
			Options: []userquestions.Option{{Label: "Approve"}, {Label: "Keep planning"}},
			Intent:  userquestions.PlanReviewIntent{Approve: "Approve"},
		}},
	})
	failure := wantCode(t, err, userquestions.CodeBadIntent)
	if !strings.Contains(failure.Message, "without the detail it reviews") {
		t.Fatalf("该说缺的是被评的那份正文：%q", failure.Message)
	}
	if len(provider.seen) != 0 {
		t.Fatal("被拒了就不该惊动界面")
	}
}

func TestPassesAWellFormedIntentThrough(t *testing.T) {
	t.Parallel()
	service, provider := newService(t, userquestions.Config{}, "Approve")
	intent := userquestions.PlanReviewIntent{Approve: "Approve"}

	answer, err := service.Ask(context.Background(), userquestions.Request{
		Questions: []userquestions.Item{
			{ID: "plain", Question: "Proceed?"},
			{
				ID: "plan-review", Question: "Approve?", Detail: "# Plan",
				Options: []userquestions.Option{{Label: "Approve"}, {Label: "Keep planning"}},
				Intent:  intent,
			},
		},
	})
	if err != nil {
		t.Fatalf("这份请求该过：%v", err)
	}
	if answer.Answers[0].ID != "plain" {
		t.Fatalf("该把界面那份答案原样交回来：%+v", answer)
	}
	// 意图只改呈现，永远不改协议：它原样到达界面，答案编码和没有意图时一模一样。
	got, ok := provider.seen[0].Questions[1].Intent.(userquestions.PlanReviewIntent)
	if !ok || got != intent {
		t.Fatalf("那个意图该原样到达界面：%+v", provider.seen[0].Questions[1].Intent)
	}
	if got.Kind() != "plan-review" || got.ApproveLabel() != "Approve" {
		t.Fatalf("意图的标记或同意标签不对：%+v", got)
	}
}

func TestCodeOfIgnoresForeignErrors(t *testing.T) {
	t.Parallel()
	if got := userquestions.CodeOf(errors.New("别人的错")); got != "" {
		t.Fatalf("不是本包的错误该交出空串，拿到 %q", got)
	}
	if got := userquestions.CodeOf(nil); got != "" {
		t.Fatalf("nil 该交出空串，拿到 %q", got)
	}
}
