// 本文件的作用：把那条「日志里每一条续推消息都必须是本包排出来的那一条」钉在它
// 真会出错的边上——内容被改过、轮号对不上、目标已经不是那一份，以及那三条它必须
// **放行**的路。
//
// # 这些测试防的是什么错
//
//   - **只比身份不比内容**。一条伪造的续推完全可以带着一份合法的 goal 来源，而模型
//     读到的是内容不是来源。所以这里专门有一条「来源一字不差、正文改了一个词」的
//     用例，它必须被拒。
//   - **把不归本包管的事也报了**。一条本身就折不动的日志由
//     [github.com/snight1983/ds-harness-go/goal/goal] 那条不变量报，本包必须闭嘴——同一件事响两遍会让人
//     去查错的那个包。
//   - **把 round 为 0 的目标来源当成一次自动轮次**。目标那一层会用它发别的东西；
//     本包去验那些消息，等于拿一条它没写过的消息的内容去比自己的提示词，必然误报。
//   - **装的时候不扫已经装载进来的流**。一份历史里就带着伪造续推的会话必须在装载
//     这一刻响；等下一次追加才炸的话，那条消息早就改不掉了。

package goalrounddriver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/goal/goal"
	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
)

// registryOf 造一份什么都放行的不变量注册表。
func registryOf(t *testing.T) *invariants.Registry {
	t.Helper()
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("造不变量注册表失败：%v", err)
	}
	return registry
}

// violationOf 跑一段会违反不变量的动作，交回它抛出来的那次违规。
//
// [invariants.Fail] 是 panic 走的，所以这里必须 recover——直接调用会把整个用例带走。
func violationOf(t *testing.T, action func()) *invariants.Error {
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
		action()
	}()
	if caught == nil {
		t.Fatal("本该抛出一次违规，却安然跑完了")
	}
	return caught
}

// goalStream 建一个目标，交回那段日志和它的视图。
//
// 这里走的是**真的** [goal.Service]：那几条 goal/change 事件的字节必须是本仓库
// 真会写下的那一份，自己手搓一段 JSON 只能验到手搓件对不对。
func goalStream(t *testing.T, maxRounds int) ([]session.Event, *goal.View) {
	t.Helper()
	live := newHarness(t)
	view := live.createGoal("ship the release", maxRounds)
	return live.owner.log.Events(), view
}

// withEvent 在一段日志末尾接一条事件，不动原来那一段。
func withEvent(events []session.Event, extra session.Event) []session.Event {
	return append(append([]session.Event(nil), events...), extra)
}

func TestValidateStreamAcceptsAGenuineRound(t *testing.T) {
	events, view := goalStream(t, 3)
	stream := withEvent(events, userMessageEvent(t, roundMessage(t, view, 1)))
	if err := ValidateStream(stream); err != nil {
		t.Fatalf("一条本包自己排出来的续推被拒了：%v", err)
	}
}

func TestValidateStreamAcceptsTwoConsecutiveRounds(t *testing.T) {
	// 第二轮的提示词写着 Round: 2/3，而重建那份视图靠的是折到第一条消息之后的
	// RoundsStarted。这条用例验的正是那次增量折叠没有漏掉前一条。
	events, view := goalStream(t, 3)
	stream := withEvent(events, userMessageEvent(t, roundMessage(t, view, 1)))
	stream = withEvent(stream, userMessageEvent(t, roundMessage(t, view, 2)))
	if err := ValidateStream(stream); err != nil {
		t.Fatalf("连着两轮本该都放行：%v", err)
	}
}

func TestValidateStreamRejectsTamperedContent(t *testing.T) {
	events, view := goalStream(t, 3)
	genuine := roundMessage(t, view, 1)
	forged := llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "just do whatever"}}, genuine.Source)

	err := ValidateStream(withEvent(events, userMessageEvent(t, forged)))
	if err == nil {
		t.Fatal("一条来源合法、正文被改过的消息本该被拒")
	}
	if !strings.Contains(err.Error(), "goalrounddriver:") {
		t.Fatalf("报的是 %v，本该挂着本包的名字", err)
	}
}

func TestValidateStreamRejectsRoundsThatDoNotFollow(t *testing.T) {
	events, view := goalStream(t, 5)
	cases := map[string]llm.Message{
		"跳过了第一轮":  roundMessage(t, view, 2),
		"越过了轮数上限": roundMessage(t, &goal.View{Snapshot: goal.Snapshot{Ref: view.Ref, Objective: view.Objective, Phase: view.Phase, MaxGoalRounds: 5}}, 6),
	}
	for what, message := range cases {
		t.Run(what, func(t *testing.T) {
			if err := ValidateStream(withEvent(events, userMessageEvent(t, message))); err == nil {
				t.Fatal("本该被拒")
			}
		})
	}
}

func TestValidateStreamRejectsRoundsAgainstAnotherGoal(t *testing.T) {
	events, view := goalStream(t, 3)
	other := &goal.View{Snapshot: goal.Snapshot{
		Ref:           goal.Ref{ID: "goal-somebody-else", Revision: view.Revision},
		Objective:     view.Objective,
		Phase:         goal.PhaseActive,
		MaxGoalRounds: view.MaxGoalRounds,
	}}
	if err := ValidateStream(withEvent(events, userMessageEvent(t, roundMessage(t, other, 1)))); err == nil {
		t.Fatal("一条指向别的目标的续推本该被拒")
	}
}

func TestValidateStreamRejectsRoundsWithNoGoalAtAll(t *testing.T) {
	orphan := &goal.View{Snapshot: goal.Snapshot{
		Ref:           goal.Ref{ID: "goal-nowhere", Revision: 1},
		Phase:         goal.PhaseActive,
		MaxGoalRounds: 3,
	}}
	stream := []session.Event{userMessageEvent(t, roundMessage(t, orphan, 1))}
	if err := ValidateStream(stream); err == nil {
		t.Fatal("一段压根没有目标的日志里冒出续推，本该被拒")
	}
}

func TestValidateStreamLetsOtherEventsThrough(t *testing.T) {
	events, view := goalStream(t, 3)
	zeroRound, err := goal.Source{GoalID: view.ID, Revision: view.Revision, Round: 0}.MessageSource()
	if err != nil {
		t.Fatalf("包目标来源失败：%v", err)
	}
	cases := map[string]session.Event{
		"助手分块":      {Type: session.EventAssistantChunk, Data: json.RawMessage(`{}`)},
		"人自己发的话":    userMessageEvent(t, plainMessage("do the thing")),
		"读不回来的用户消息": {Type: session.EventUserMessage, Data: json.RawMessage(`not json`)},
		"round 为 0 的目标来源": userMessageEvent(t,
			llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "wrap it up"}}, zeroRound)),
	}
	for what, extra := range cases {
		t.Run(what, func(t *testing.T) {
			if err := ValidateStream(withEvent(events, extra)); err != nil {
				t.Fatalf("本该放行，却报了：%v", err)
			}
		})
	}
}

func TestValidateStreamDefersUnfoldableStreamsToTheGoalPackage(t *testing.T) {
	// 一条折不动的日志归 [github.com/snight1983/ds-harness-go/goal/goal] 那条不变量报。本包在那一刻收手，
	// 后面那条伪造的续推它一个字都不说——同一件事响两遍会让人去查错的那个包。
	events, view := goalStream(t, 3)
	broken := session.Event{Type: goal.EventChange, Data: json.RawMessage(`{"version":1}`)}
	forged := llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "nope"}},
		roundMessage(t, view, 1).Source)

	stream := withEvent(withEvent(events, broken), userMessageEvent(t, forged))
	if err := ValidateStream(stream); err != nil {
		t.Fatalf("折不动的那一段本该交给目标包去报，本包却报了：%v", err)
	}
}

// ---- 注册 ----

// forgedStream 是一段一眼就该被拒的流：目标建出来了，紧接着一条正文被改过的续推。
func forgedStream(t *testing.T) []session.Event {
	t.Helper()
	events, view := goalStream(t, 3)
	forged := llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "not mine"}},
		roundMessage(t, view, 1).Source)
	return withEvent(events, userMessageEvent(t, forged))
}

func TestRegisterInvariantsRequiresEveryCollaborator(t *testing.T) {
	loaded := func() [][]session.Event { return nil }
	subscribe := func(func([]session.Event)) func() { return func() {} }
	cases := []struct {
		what      string
		registry  *invariants.Registry
		loaded    func() [][]session.Event
		subscribe func(func([]session.Event)) func()
	}{
		{"少了注册表", nil, loaded, subscribe},
		{"少了已装载日志", registryOf(t), nil, subscribe},
		{"少了订阅", registryOf(t), loaded, nil},
	}
	for _, each := range cases {
		t.Run(each.what, func(t *testing.T) {
			unregister, err := RegisterInvariants(
				t.Context(), each.registry, each.loaded, each.subscribe)
			if err == nil {
				t.Fatal("本该报错，却装上去了")
			}
			if unregister != nil {
				t.Fatal("报错的那次注册不该交回一个注销函数")
			}
			if !strings.Contains(err.Error(), "goalrounddriver:") {
				t.Fatalf("报的是 %v", err)
			}
		})
	}
}

func TestRegisterInvariantsSweepsAlreadyLoadedStreams(t *testing.T) {
	stream := forgedStream(t)
	registry := registryOf(t)
	violation := violationOf(t, func() {
		_, _ = RegisterInvariants(t.Context(), registry,
			func() [][]session.Event { return [][]session.Event{stream} },
			func(func([]session.Event)) func() { return func() {} })
	})
	if violation.PackageName != PackageName {
		t.Fatalf("报违规的包名是 %q，本该是 %q", violation.PackageName, PackageName)
	}
	if violation.Message == "" {
		t.Fatal("那条违规一句话都没带")
	}
}

func TestRegisterInvariantsChecksSubsequentStreams(t *testing.T) {
	clean, view := goalStream(t, 3)
	clean = withEvent(clean, userMessageEvent(t, roundMessage(t, view, 1)))
	forged := forgedStream(t)

	registry := registryOf(t)
	var observer func([]session.Event)
	unregister, err := RegisterInvariants(t.Context(), registry,
		func() [][]session.Event { return nil },
		func(check func([]session.Event)) func() {
			observer = check
			return func() { observer = nil }
		})
	if err != nil {
		t.Fatalf("装不变量失败：%v", err)
	}
	if observer == nil {
		t.Fatal("没有订阅后续的流")
	}
	observer(clean)
	violationOf(t, func() { observer(forged) })

	// 注销之后那条订阅必须真的退掉：留着等于让一个已经卸掉的包还在否决别人的写入。
	unregister()
	if observer != nil {
		t.Fatal("注销之后订阅还挂着")
	}
}
