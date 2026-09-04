// 本文件的作用：把 `/goal` 这条命令钉在它真会出错的几处——那点断句、图片附件的
// 去向、一次被目标域按状态拒掉的调用怎么回话，以及那段人读文字里每一行的落点。
//
// # 这些测试防的是什么错
//
//   - **断句把目标吃掉**。`/goal editor 写完文档` 是一个**新目标**，不是一次 edit；
//     `edit` 那条规则一旦松成前缀匹配，用户每一个以 edit 开头的目标都会被吞掉。
//   - **图片被默默丢掉**。人往 `/goal clear` 上拖了几张图，那几张图必须留在编辑器
//     里、并且当场告诉他这条命令用不上；悄悄扔掉是替用户做主。
//   - **图片进了目标域**。它们必须成为一条普通的用户消息，否则之后的目标轮次
//     读不到，而目标域还得凭空多存一份附件状态。
//   - **一次状态不对的调用被当成程序错误抛出去**。人敲错了不该让整条命令炸掉；
//     那是一句回话，不是一次故障。
//   - **续推资格没进那行提示**。一个耐久上 active、进程里已经 disarmed 的目标
//     （续会话、分叉、换驱动之后都是这样）提示 pause 是错的：对着它敲 pause
//     什么都不会发生，而人会以为自己按上了。
//   - **CAS 修订号漏进了给人看的文字**。它是这一层和目标域之间的事，人拿它做不了
//     任何决定。
//   - **一个 blocked 的目标少了那行原因**。那正是人此刻唯一需要看的一行。

package goalcommand

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/attachment"
	"github.com/snight1983/ds-harness-go/feature/goal"
	"github.com/snight1983/ds-harness-go/feature/interaction/commands"
	"github.com/snight1983/ds-harness-go/harness/agent"
	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// ---- 手脚架 ----

// stubAgent 是一个握着**真会话**的假 agent。
//
// 会话必须是真的：目标域全部的状态都折自那条日志，拿一份手搓的事件切片糊弄过去
// 等于跳过了 [coresession.Session.Append] 那道信封校验。
type stubAgent struct {
	id sessionlog.SessionID
	// own 是它自己那一层作用域，[Config.AgentOf] 靠这把钥匙查回来。
	own *scope.Scope
	log *coresession.Session
	// followups 记下本包发进会话的那几条消息。
	followups []llm.Message
}

func newStubAgent(t *testing.T, id string, own *scope.Scope) *stubAgent {
	t.Helper()
	sessionID := sessionlog.SessionID(id)
	header := sessionlog.SessionHeader{Version: sessionlog.FormatVersion, ID: sessionID, CreatedAt: 1}
	log, err := coresession.NewSession(sessionID, coresession.Options{
		Header: &header,
		Now:    func() int64 { return 1 },
	})
	if err != nil {
		t.Fatalf("造会话失败：%v", err)
	}
	return &stubAgent{id: sessionID, own: own, log: log}
}

func (a *stubAgent) ID() sessionlog.SessionID                                  { return a.id }
func (a *stubAgent) Status() agent.Status                                      { return agent.StatusRunning }
func (a *stubAgent) Options() agent.Options                                    { return agent.Options{} }
func (a *stubAgent) Session() *coresession.Session                             { return a.log }
func (a *stubAgent) Inbox() *agent.Inbox                                       { return nil }
func (a *stubAgent) Scope() *scope.Scope                                       { return a.own }
func (a *stubAgent) WhenIdle(context.Context) error                            { return nil }
func (a *stubAgent) Cancel(sessionlog.TurnEndCancelCause, agent.CancelOptions) {}
func (a *stubAgent) Send(llm.Message, agent.InboxTarget, bool)                 {}
func (a *stubAgent) Steer(llm.Message)                                         {}
func (a *stubAgent) Inject(llm.Message)                                        {}
func (a *stubAgent) Prepend(llm.Message, agent.InboxTarget)                    {}

func (a *stubAgent) Followup(message llm.Message) {
	a.followups = append(a.followups, message)
}

func (a *stubAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	return task(ctx)
}

// stubAgents 是目标域要的那张注册表：它只需要回答「我手里这个还是活着的那一个吗」。
type stubAgents struct {
	live map[sessionlog.SessionID]agent.Agent
}

func (a *stubAgents) Get(id sessionlog.SessionID) (agent.Agent, bool) {
	found, present := a.live[id]
	return found, present
}

func (a *stubAgents) OnSessionStart(
	context.Context, *scope.Scope, agent.SessionStartObserver,
) (func(context.Context) error, error) {
	return func(context.Context) error { return nil }, nil
}

// scopeOf 造一层作用域。
func scopeOf(t *testing.T, label string, parent *scope.Scope) *scope.Scope {
	t.Helper()
	options := scope.Options{}
	if parent != nil {
		options.Parent = parent.Key()
	}
	own, err := scope.New(scope.NewKey(label), options)
	if err != nil {
		t.Fatalf("造作用域 %q 失败：%v", label, err)
	}
	t.Cleanup(func() { _ = own.Dispose(context.Background()) })
	return own
}

// world 是一套装好的东西：真目标服务、真命令注册表、一个握着真会话的 agent。
//
// 目标服务是**真的**而不是打桩的：本包这条路上每一个有意思的判断（已完成的目标
// 能不能 edit、一个 armed 的 active 目标能不能 resume、清掉之后还剩什么）都是那套
// 状态机说了算，打了桩就等于把被测的东西换成了我自己写的答案。
type world struct {
	root    *scope.Scope
	caller  *stubAgent
	service *goal.Service
	runtime *commands.Runtime
}

func newWorld(t *testing.T) *world {
	t.Helper()
	root := scopeOf(t, "root", nil)
	caller := newStubAgent(t, "caller", scopeOf(t, "caller", root))
	registry := &stubAgents{live: map[sessionlog.SessionID]agent.Agent{caller.ID(): caller}}
	service, err := goal.New(goal.Config{Agents: registry})
	if err != nil {
		t.Fatalf("造目标服务失败：%v", err)
	}
	runtime, err := commands.NewRuntime(commands.Options{
		LogOf: func(*scope.Key) (commands.Log, error) { return nil, errors.New("用不着") },
	})
	if err != nil {
		t.Fatalf("造命令注册表失败：%v", err)
	}
	return &world{root: root, caller: caller, service: service, runtime: runtime}
}

// agentOf 是那条从作用域钥匙查回 agent 的路。
func (w *world) agentOf(key *scope.Key) (agent.Agent, error) {
	if key == w.caller.own.Key() {
		return w.caller, nil
	}
	return nil, errors.New("这把钥匙不认识")
}

func (w *world) controller(t *testing.T) *Controller {
	t.Helper()
	controller, err := New(Config{Service: w.service, AgentOf: w.agentOf})
	if err != nil {
		t.Fatalf("造控制器失败：%v", err)
	}
	return controller
}

// live 是一台装好了控制器的 world，外加跑一条 `/goal` 的那点方便。
type live struct {
	*world
	controller *Controller
}

func start(t *testing.T) *live {
	t.Helper()
	world := newWorld(t)
	return &live{world: world, controller: world.controller(t)}
}

// run 跑一条 `/goal`，带不带图由调用方定。
func (l *live) run(t *testing.T, input string, images ...llm.ImageBlock) commands.Result {
	t.Helper()
	result, err := l.controller.run(t.Context(), commands.Invocation{
		Agent:       l.caller.own.Key(),
		RawInput:    input,
		Attachments: images,
	})
	if err != nil {
		t.Fatalf("`/goal %s` 不该抛错：%v", input, err)
	}
	return result
}

// image 造一张能塞进调用的图。
func image(id string) llm.ImageBlock {
	return llm.ImageBlock{Attachment: attachment.ImageRef{
		ID:        attachment.ID(id),
		MediaType: "image/png",
		Bytes:     1,
		Width:     1,
		Height:    1,
	}}
}

// currentRef 取此刻那个目标的 CAS 身份，供测试直接调目标域里本包用不到的那几条路。
func (l *live) currentRef(t *testing.T) goal.Ref {
	t.Helper()
	view, err := l.service.Get(l.caller)
	if err != nil || view == nil {
		t.Fatalf("此刻该有一个目标：%v", err)
	}
	return view.Ref
}

// ---- 断句 ----

// TestParseCommandReadsOnlyItsOwnGrammar 逐条钉住那点语法，以及「别的都当目标」。
//
// 最要紧的是 editor 那一条：`edit` 后面必须跟着空白才算子命令，否则用户每一个以
// edit 开头的目标都会被吞掉半截。
func TestParseCommandReadsOnlyItsOwnGrammar(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		input     string
		kind      commandKind
		objective string
	}{
		{"空的", "", kindShow, ""},
		{"全是空白", "   \n\t ", kindShow, ""},
		{"clear", "clear", kindClear, ""},
		{"clear 大小写不敏感", "CLEAR", kindClear, ""},
		{"clear 前后带空白", "  clear  ", kindClear, ""},
		{"pause", "pause", kindPause, ""},
		{"resume", "Resume", kindResume, ""},
		{"光一个 edit", "edit", kindInvalidEdit, ""},
		{"edit 加空白后什么都没有", "edit   ", kindInvalidEdit, ""},
		{"edit 换目标", "edit 把测试补齐", kindEdit, "把测试补齐"},
		{"edit 大小写不敏感", "EDIT 把测试补齐", kindEdit, "把测试补齐"},
		{"edit 后面多几个空格", "edit    把测试补齐", kindEdit, "把测试补齐"},
		{"editor 是一个新目标", "editor 写完文档", kindCreate, "editor 写完文档"},
		{"edits 是一个新目标", "edits", kindCreate, "edits"},
		{"普通目标", "把测试补齐", kindCreate, "把测试补齐"},
		{"目标里带 clear 这个词", "clear the build cache", kindCreate, "clear the build cache"},
		{"目标本身不改大小写", "  Fix The Build  ", kindCreate, "Fix The Build"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			t.Parallel()
			got := parseCommand(each.input)
			if got.kind != each.kind || got.objective != each.objective {
				t.Fatalf("断成了 %+v，该是 {%s %q}", got, each.kind, each.objective)
			}
		})
	}
}

// ---- 看一眼 ----

// TestShowWithoutAGoal 钉住一个还没有目标的会话回的是那句带语法提示的成功。
//
// 成功而不是错误：「还没设目标」不是用户干错了什么。
func TestShowWithoutAGoal(t *testing.T) {
	t.Parallel()
	result := start(t).run(t, "")
	if result.Kind != commands.ResultSuccess {
		t.Fatalf("该是一次成功，实际 %q", result.Kind)
	}
	if result.Text != "No goal is currently set.\n"+usage {
		t.Fatalf("回话不对：%q", result.Text)
	}
}

// TestCreatedGoalRendersEveryLine 钉住那段人读文字的每一行，以及**没有**修订号。
func TestCreatedGoalRendersEveryLine(t *testing.T) {
	t.Parallel()
	session := start(t)
	result := session.run(t, "把测试补齐")
	want := strings.Join([]string{
		"Goal created",
		"Status: active",
		"Objective: 把测试补齐",
		"Rounds: 0/" + itoa(goal.DefaultMaxGoalRounds),
		"Activation: armed",
		"",
		"Commands: /goal edit <objective>, /goal pause, /goal clear",
	}, "\n")
	if result.Kind != commands.ResultSuccess || result.Text != want {
		t.Fatalf("排出来的是：\n%s\n该是：\n%s", result.Text, want)
	}
	if strings.Contains(result.Text, "evision") {
		t.Fatalf("修订号漏进了给人看的文字：%q", result.Text)
	}
	// 再看一眼：同一份状态，只有标题不一样。
	shown := session.run(t, "")
	if shown.Text != strings.Replace(want, "Goal created", "Goal", 1) {
		t.Fatalf("看一眼排出来的是：\n%s", shown.Text)
	}
}

// ---- 建、改、清 ----

// TestCreateRefusesWhileOneIsStillLive 钉住一个没完成的目标挡住新建。
//
// 那句话必须说清此刻是什么阶段、以及两条出路，否则用户只知道「不行」。
func TestCreateRefusesWhileOneIsStillLive(t *testing.T) {
	t.Parallel()
	session := start(t)
	session.run(t, "第一个目标")
	result := session.run(t, "第二个目标")
	if result.Kind != commands.ResultError {
		t.Fatalf("该是一次错误结果，实际 %q", result.Kind)
	}
	want := "A goal is already active. Use /goal edit <objective> to change it or /goal clear before replacing it."
	if result.Text != want {
		t.Fatalf("回话不对：%q", result.Text)
	}
}

// TestCreateReplacesACompletedGoal 钉住上一个已经完成时，直接敲新目标就建得出来。
func TestCreateReplacesACompletedGoal(t *testing.T) {
	t.Parallel()
	session := start(t)
	session.run(t, "第一个目标")
	if _, err := session.service.Complete(session.caller, session.currentRef(t)); err != nil {
		t.Fatalf("标记完成失败：%v", err)
	}
	result := session.run(t, "第二个目标")
	if result.Kind != commands.ResultSuccess || !strings.HasPrefix(result.Text, "Goal created\n") {
		t.Fatalf("该建出一个新目标，实际 %q", result.Text)
	}
	if !strings.Contains(result.Text, "Objective: 第二个目标") {
		t.Fatalf("新目标没换上：%q", result.Text)
	}
}

// TestEditNeedsAReplacementObjective 钉住光敲一个 edit 的回话。
func TestEditNeedsAReplacementObjective(t *testing.T) {
	t.Parallel()
	session := start(t)
	session.run(t, "第一个目标")
	result := session.run(t, "edit")
	if result.Kind != commands.ResultError {
		t.Fatalf("该是一次错误结果，实际 %q", result.Kind)
	}
	if result.Text != "Goal editing requires a replacement objective.\n"+usage {
		t.Fatalf("回话不对：%q", result.Text)
	}
}

// TestEditUpdatesTheObjective 钉住 edit 换掉描述、不动阶段。
func TestEditUpdatesTheObjective(t *testing.T) {
	t.Parallel()
	session := start(t)
	session.run(t, "第一个目标")
	result := session.run(t, "edit 第二个目标")
	if result.Kind != commands.ResultSuccess || !strings.HasPrefix(result.Text, "Goal updated\n") {
		t.Fatalf("该是一次更新，实际 %q", result.Text)
	}
	if !strings.Contains(result.Text, "Objective: 第二个目标") ||
		!strings.Contains(result.Text, "Status: active") {
		t.Fatalf("换完之后的样子不对：%q", result.Text)
	}
}

// TestEditOnACompletedGoalCreatesANewOne 钉住这条捷径。
//
// 目标域不准改一个已完成的目标，而人敲 `/goal edit <新目标>` 的意思显然是「换一个
// 新的接着干」。回「这条命令在当前状态下不成立」是对的但没用。
func TestEditOnACompletedGoalCreatesANewOne(t *testing.T) {
	t.Parallel()
	session := start(t)
	session.run(t, "第一个目标")
	if _, err := session.service.Complete(session.caller, session.currentRef(t)); err != nil {
		t.Fatalf("标记完成失败：%v", err)
	}
	result := session.run(t, "edit 第二个目标")
	if result.Kind != commands.ResultSuccess || !strings.HasPrefix(result.Text, "Goal created\n") {
		t.Fatalf("该建出一个新目标，实际 %q", result.Text)
	}
	if !strings.Contains(result.Text, "Status: active") {
		t.Fatalf("新目标该是 active：%q", result.Text)
	}
}

// TestPauseThenResume 钉住停下和推起来各自的回话，以及那行提示跟着状态变。
func TestPauseThenResume(t *testing.T) {
	t.Parallel()
	session := start(t)
	session.run(t, "把测试补齐")

	paused := session.run(t, "pause")
	if !strings.HasPrefix(paused.Text, "Goal paused\nStatus: paused\n") {
		t.Fatalf("停下之后不对：%q", paused.Text)
	}
	if !strings.Contains(paused.Text, "Activation: disarmed") ||
		!strings.Contains(paused.Text, "Commands: /goal edit <objective>, /goal resume, /goal clear") {
		t.Fatalf("停下之后那行提示该改成 resume：%q", paused.Text)
	}

	resumed := session.run(t, "resume")
	if !strings.HasPrefix(resumed.Text, "Goal resumed\nStatus: active\n") {
		t.Fatalf("推起来之后不对：%q", resumed.Text)
	}
	if !strings.Contains(resumed.Text, "Activation: armed") ||
		!strings.Contains(resumed.Text, "Commands: /goal edit <objective>, /goal pause, /goal clear") {
		t.Fatalf("推起来之后那行提示该改回 pause：%q", resumed.Text)
	}
}

// TestClear 钉住清目标的两种局面。
func TestClear(t *testing.T) {
	t.Parallel()
	session := start(t)
	if empty := session.run(t, "clear"); empty.Kind != commands.ResultSuccess ||
		empty.Text != "No goal to clear." {
		t.Fatalf("没目标可清时该这么说，实际 %q", empty.Text)
	}
	session.run(t, "把测试补齐")
	cleared := session.run(t, "clear")
	if cleared.Kind != commands.ResultSuccess || cleared.Text != "Goal cleared." {
		t.Fatalf("清完该这么说，实际 %q", cleared.Text)
	}
	if shown := session.run(t, ""); !strings.HasPrefix(shown.Text, "No goal is currently set.") {
		t.Fatalf("清完之后该一个目标都没有，实际 %q", shown.Text)
	}
}

// TestOperationsThatNeedAGoalSayWhichOne 钉住 edit / pause / resume 在没目标时那三句话。
//
// 每一句都点名是哪条命令要目标：一句笼统的「没有目标」会让人不知道自己刚才敲的
// 那条命令到底跑没跑。
func TestOperationsThatNeedAGoalSayWhichOne(t *testing.T) {
	t.Parallel()
	for input, action := range map[string]string{
		"edit 新目标": "edit",
		"pause":    "pause",
		"resume":   "resume",
	} {
		t.Run(action, func(t *testing.T) {
			t.Parallel()
			result := start(t).run(t, input)
			if result.Kind != commands.ResultError {
				t.Fatalf("该是一次错误结果，实际 %q", result.Kind)
			}
			want := "No goal is currently set; /goal " + action + " requires one. " + usage
			if result.Text != want {
				t.Fatalf("回话不对：%q", result.Text)
			}
		})
	}
}

// ---- 图片附件 ----

// TestAttachmentsBecomeAUserMessage 钉住那几张图变成一条普通的用户消息。
//
// 图在前、那句写明身份的话在后：目标域因此一个字节的附件状态都不必存，而之后的
// 目标轮次从普通会话历史里就读得到它们。
func TestAttachmentsBecomeAUserMessage(t *testing.T) {
	t.Parallel()
	session := start(t)
	session.run(t, "把测试补齐", image("一"), image("二"))
	if len(session.caller.followups) != 1 {
		t.Fatalf("该正好发一条消息，实际 %d 条", len(session.caller.followups))
	}
	content := session.caller.followups[0].Content
	if len(content) != 3 {
		t.Fatalf("该是两张图加一句话，实际 %d 块", len(content))
	}
	for index, want := range []string{"一", "二"} {
		block, ok := content[index].(llm.ImageBlock)
		if !ok || string(block.Attachment.ID) != want {
			t.Fatalf("第 %d 块该是图 %q，实际 %#v", index, want, content[index])
		}
	}
	text, ok := content[2].(llm.TextBlock)
	if !ok || text.Text != attachmentRole {
		t.Fatalf("最后一块该是那句写明身份的话，实际 %#v", content[2])
	}
	if session.caller.followups[0].Role != llm.RoleUser {
		t.Fatalf("该是一条用户消息，实际 %q", session.caller.followups[0].Role)
	}
}

// TestAttachmentsAlsoRideAnEdit 钉住 edit 那一支同样把图发进去。
func TestAttachmentsAlsoRideAnEdit(t *testing.T) {
	t.Parallel()
	session := start(t)
	session.run(t, "第一个目标")
	session.run(t, "edit 第二个目标", image("一"))
	if len(session.caller.followups) != 1 {
		t.Fatalf("edit 那一支也该发一条消息，实际 %d 条", len(session.caller.followups))
	}
}

// TestAttachmentsOnlyRideAnObjective 钉住别的子命令带图一律当场判错。
//
// 图必须留在编辑器里：默默扔掉是替用户做主，而他多半只是拖错了地方。
func TestAttachmentsOnlyRideAnObjective(t *testing.T) {
	t.Parallel()
	want := "Image attachments only accompany a goal objective: /goal <objective> or /goal edit <objective>."
	for _, input := range []string{"", "clear", "pause", "resume", "edit"} {
		t.Run("`"+input+"`", func(t *testing.T) {
			t.Parallel()
			session := start(t)
			session.run(t, "把测试补齐")
			result := session.run(t, input, image("一"))
			if result.Kind != commands.ResultError || result.Text != want {
				t.Fatalf("该判错，实际 %q / %q", result.Kind, result.Text)
			}
			if len(session.caller.followups) != 0 {
				t.Fatalf("一条消息都不该发出去，实际 %d 条", len(session.caller.followups))
			}
		})
	}
}

// TestNoAttachmentsNoMessage 钉住不带图的调用一条消息都不发。
func TestNoAttachmentsNoMessage(t *testing.T) {
	t.Parallel()
	session := start(t)
	session.run(t, "把测试补齐")
	session.run(t, "edit 换一个")
	if len(session.caller.followups) != 0 {
		t.Fatalf("不带图不该发消息，实际 %d 条", len(session.caller.followups))
	}
}

// ---- 被目标域拒掉 ----

// TestStateRejectionIsAnAnswerNotAFailure 钉住一次状态不对的调用回的是话不是错误。
//
// 这里挑的是「一个已经 active 而且已经点亮的目标再 resume」——目标域会拒，而人
// 敲这一下没有任何错，他只是不知道它已经在跑了。
func TestStateRejectionIsAnAnswerNotAFailure(t *testing.T) {
	t.Parallel()
	session := start(t)
	session.run(t, "把测试补齐")
	result := session.run(t, "resume")
	if result.Kind != commands.ResultError || result.Text != stateRejection {
		t.Fatalf("该是那句笼统的回话，实际 %q / %q", result.Kind, result.Text)
	}
}

// TestNonDomainFailuresAreStillFailures 钉住不是目标域拒收的那一类原样抛出去。
//
// 那说明装配或者会话日志出了事，折成一句给人看的话等于把一次故障藏起来。
func TestNonDomainFailuresAreStillFailures(t *testing.T) {
	t.Parallel()
	world := newWorld(t)
	broken := errors.New("日志读不动")
	controller, err := New(Config{
		Service: brokenService{err: broken},
		AgentOf: world.agentOf,
	})
	if err != nil {
		t.Fatalf("造控制器失败：%v", err)
	}
	_, err = controller.run(t.Context(), commands.Invocation{Agent: world.caller.own.Key()})
	if !errors.Is(err, broken) {
		t.Fatalf("该把那个错误原样抛出去，实际 %v", err)
	}
}

// brokenService 是一台每条路都以一个**非**目标域错误失败的服务。
type brokenService struct{ err error }

func (s brokenService) Get(agent.Agent) (*goal.View, error) { return nil, s.err }
func (s brokenService) Create(agent.Agent, goal.CreateRequest) (*goal.View, error) {
	return nil, s.err
}

func (s brokenService) Edit(agent.Agent, goal.Ref, goal.EditRequest) (*goal.View, error) {
	return nil, s.err
}
func (s brokenService) Pause(agent.Agent, goal.Ref) (*goal.View, error)  { return nil, s.err }
func (s brokenService) Resume(agent.Agent, goal.Ref) (*goal.View, error) { return nil, s.err }
func (s brokenService) Clear(agent.Agent, goal.Ref) (goal.Ref, error)    { return goal.Ref{}, s.err }

// TestEveryMutatingPathSurfacesItsFailure 钉住那五条改动路各自把失败带出来。
//
// 一台每条路都失败的服务能把这几条错误分支一次性走遍；它们各自 return 一次，
// 漏掉任何一条都会让那条路悄悄交回一个零值结果。
func TestEveryMutatingPathSurfacesItsFailure(t *testing.T) {
	t.Parallel()
	world := newWorld(t)
	broken := errors.New("写不进去")
	// Get 得成功，后面那几条才走得到。
	service := &halfBrokenService{live: world.service, err: broken}
	controller, err := New(Config{Service: service, AgentOf: world.agentOf})
	if err != nil {
		t.Fatalf("造控制器失败：%v", err)
	}
	call := func(input string) {
		t.Helper()
		_, err := controller.run(t.Context(), commands.Invocation{
			Agent:    world.caller.own.Key(),
			RawInput: input,
		})
		if !errors.Is(err, broken) {
			t.Fatalf("`/goal %s` 该把那个错误带出来，实际 %v", input, err)
		}
	}
	// create 那一支必须在**还没有目标**的时候试：手上已经有一个活目标时，这条路在
	// 调到服务之前就先被那句「已经有一个了」拦下，根本走不到失败那一行。
	call("另一个目标")
	// 剩下四条要的可改，用真服务建一个出来。
	if _, err := world.service.Create(world.caller, goal.CreateRequest{Objective: "把测试补齐"}); err != nil {
		t.Fatalf("建目标失败：%v", err)
	}
	for _, input := range []string{"edit 换一个", "pause", "resume", "clear"} {
		call(input)
	}
}

// halfBrokenService 的读是真的，写全都失败。
type halfBrokenService struct {
	live *goal.Service
	err  error
}

func (s *halfBrokenService) Get(owner agent.Agent) (*goal.View, error) {
	view, err := s.live.Get(owner)
	if err != nil {
		return nil, err
	}
	// 「已完成」那一支会把 create 走成 edit 的捷径，这里要的是原样的分流。
	return view, nil
}

func (s *halfBrokenService) Create(agent.Agent, goal.CreateRequest) (*goal.View, error) {
	return nil, s.err
}

func (s *halfBrokenService) Edit(agent.Agent, goal.Ref, goal.EditRequest) (*goal.View, error) {
	return nil, s.err
}
func (s *halfBrokenService) Pause(agent.Agent, goal.Ref) (*goal.View, error)  { return nil, s.err }
func (s *halfBrokenService) Resume(agent.Agent, goal.Ref) (*goal.View, error) { return nil, s.err }
func (s *halfBrokenService) Clear(agent.Agent, goal.Ref) (goal.Ref, error) {
	return goal.Ref{}, s.err
}

// ---- 谁在敲这条命令 ----

// TestTheCommandNeedsAnIdentifiableAgent 钉住查不回来发起者就是一次故障。
//
// 那不是用户能改的事——他敲的这行字本身没毛病，是装配没接对。
func TestTheCommandNeedsAnIdentifiableAgent(t *testing.T) {
	t.Parallel()
	session := start(t)
	if _, err := session.controller.run(t.Context(), commands.Invocation{}); err == nil ||
		!strings.Contains(err.Error(), "需要一个发起这条命令的 agent") {
		t.Fatalf("没有 agent 该报错，实际 %v", err)
	}
	_, err := session.controller.run(t.Context(), commands.Invocation{Agent: scope.NewKey("外人")})
	if err == nil || !strings.Contains(err.Error(), "这把钥匙不认识") {
		t.Fatalf("认不出的钥匙该报错，实际 %v", err)
	}

	// 一条查得回来、但查回来一个 nil 的路同样是装配没接对。
	blank, err := New(Config{
		Service: session.service,
		AgentOf: func(*scope.Key) (agent.Agent, error) { return nil, nil },
	})
	if err != nil {
		t.Fatalf("造控制器失败：%v", err)
	}
	_, err = blank.run(t.Context(), commands.Invocation{Agent: session.caller.own.Key()})
	if err == nil || !strings.Contains(err.Error(), "找不到发起这条命令的 agent") {
		t.Fatalf("查回来一个 nil 该报错，实际 %v", err)
	}
}

// ---- 排版 ----

// TestBlockedGoalShowsItsReason 钉住一个撞了墙的目标把那行原因排出来。
//
// 那正是人此刻唯一需要看的一行。
func TestBlockedGoalShowsItsReason(t *testing.T) {
	t.Parallel()
	session := start(t)
	session.run(t, "把测试补齐")
	_, err := session.service.Block(session.caller, session.currentRef(t),
		goal.BlockReason{Code: "needs-credentials", Message: "缺一把仓库的写权限"})
	if err != nil {
		t.Fatalf("标记阻塞失败：%v", err)
	}
	result := session.run(t, "")
	if !strings.Contains(result.Text, "Status: blocked\nBlocker: needs-credentials: 缺一把仓库的写权限\n") {
		t.Fatalf("那行原因该紧跟在状态后面：\n%s", result.Text)
	}
	if !strings.Contains(result.Text, "Commands: /goal edit <objective>, /goal resume, /goal clear") {
		t.Fatalf("阻塞时那行提示不对：%q", result.Text)
	}
}

// TestRenderRefusesABlockedGoalWithoutAReason 钉住那次解指针有护栏。
//
// 耐久回放保证走不到；但它是一次解指针，悄悄跳过会排出一段看不出问题、却少了
// 最关键那行的文字。
func TestRenderRefusesABlockedGoalWithoutAReason(t *testing.T) {
	t.Parallel()
	view := &goal.View{Snapshot: goal.Snapshot{
		Ref:       goal.Ref{ID: "g1", Revision: 1},
		Objective: "把测试补齐",
		Phase:     goal.PhaseBlocked,
	}}
	if _, err := renderGoal("Goal", view); err == nil ||
		!strings.Contains(err.Error(), "没带阻塞原因") {
		t.Fatalf("该报缺原因，实际 %v", err)
	}
}

// TestCommandHintFollowsTheLiveState 逐条钉住那行提示。
//
// active 那一支要看两个东西：一个耐久上 active、进程里已经 disarmed 的目标（续
// 会话、分叉、换驱动之后都是这样）必须提示 resume——对着它敲 pause 什么都不会
// 发生，而人会以为自己按上了。
func TestCommandHintFollowsTheLiveState(t *testing.T) {
	t.Parallel()
	const editable = "/goal edit <objective>, "
	cases := []struct {
		name       string
		phase      goal.Phase
		activation goal.Activation
		want       string
	}{
		{"active 且已点亮", goal.PhaseActive, goal.Armed, editable + "/goal pause, /goal clear"},
		{"active 但没点亮", goal.PhaseActive, goal.Disarmed, editable + "/goal resume, /goal clear"},
		{"paused", goal.PhasePaused, goal.Disarmed, editable + "/goal resume, /goal clear"},
		{"blocked", goal.PhaseBlocked, goal.Disarmed, editable + "/goal resume, /goal clear"},
		{"complete", goal.PhaseComplete, goal.Disarmed, "/goal <objective>, /goal clear"},
		{"认不出的阶段", goal.Phase("weird"), goal.Armed, "/goal, /goal clear"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			t.Parallel()
			view := &goal.View{
				Snapshot:   goal.Snapshot{Phase: each.phase},
				Activation: each.activation,
			}
			if got := commandHint(view); got != each.want {
				t.Fatalf("提示是 %q，该是 %q", got, each.want)
			}
		})
	}
}

// TestDispatchRefusesAnUnknownKind 钉住那个走不到的分支大声失败。
//
// 只有 [parseCommand] 造得出一个 [commandKind]，所以这条路真的走不到；但 Go 挡不住
// 一个认不出的值，让它悄悄交回一个零值结果比什么都不做更糟。
func TestDispatchRefusesAnUnknownKind(t *testing.T) {
	t.Parallel()
	session := start(t)
	_, err := session.controller.dispatch(session.caller, parsedCommand{kind: "weird"}, nil)
	if err == nil || !strings.Contains(err.Error(), "认不出的 /goal 意思") {
		t.Fatalf("该大声失败，实际 %v", err)
	}
}

// ---- 装配 ----

// TestNewRejectsAnUnusableAssembly 钉住缺哪一样都在造的时候就拒。
//
// 等到人敲下第一条 `/goal` 才在处理器里空指针，那时候这条命令已经登记出去了。
func TestNewRejectsAnUnusableAssembly(t *testing.T) {
	t.Parallel()
	world := newWorld(t)
	if _, err := New(Config{AgentOf: world.agentOf}); err == nil ||
		!strings.Contains(err.Error(), "需要一台目标服务") {
		t.Fatalf("缺服务该拒，实际 %v", err)
	}
	if _, err := New(Config{Service: world.service}); err == nil ||
		!strings.Contains(err.Error(), "找回 agent 的路") {
		t.Fatalf("缺 AgentOf 该拒，实际 %v", err)
	}
}

// TestInstallMountsTheCommand 钉住装完之后 `/goal` 真的在那张注册表里，摘完就没了。
func TestInstallMountsTheCommand(t *testing.T) {
	t.Parallel()
	session := start(t)
	undo, err := session.controller.Install(t.Context(), session.root, Deps{Commands: session.runtime})
	if err != nil {
		t.Fatalf("装不上：%v", err)
	}
	definition, ok := session.runtime.Find(session.root.Key(), CommandName)
	if !ok {
		t.Fatalf("装完了却找不到 /goal")
	}
	if definition.Input == nil || !definition.Input.Images {
		t.Fatalf("/goal 必须声明收图，否则带图的调用在注册表那一层就被拒了")
	}
	if definition.Input.Hint == "" || definition.Description == "" {
		t.Fatalf("发现界面上那两句话不能是空的：%+v", definition)
	}
	if err := undo(t.Context()); err != nil {
		t.Fatalf("摘不掉：%v", err)
	}
	if _, ok := session.runtime.Find(session.root.Key(), CommandName); ok {
		t.Fatalf("摘完了 /goal 该没了")
	}
}

// TestInstallRejectsAnIncompleteAssembly 钉住缺注册表和重名两种装不上的局面。
func TestInstallRejectsAnIncompleteAssembly(t *testing.T) {
	t.Parallel()
	session := start(t)
	if _, err := session.controller.Install(t.Context(), session.root, Deps{}); err == nil ||
		!strings.Contains(err.Error(), "需要一张命令注册表") {
		t.Fatalf("缺注册表该拒，实际 %v", err)
	}
	occupy, err := session.runtime.Register(t.Context(), session.root, commands.Definition{
		Name:        CommandName,
		Description: "占位",
		Handler: func(context.Context, commands.Invocation) (commands.Result, error) {
			return commands.Result{Kind: commands.ResultSuccess}, nil
		},
	})
	if err != nil {
		t.Fatalf("占位那条命令装不上：%v", err)
	}
	t.Cleanup(func() { _ = occupy(context.Background()) })
	if _, err := session.controller.Install(t.Context(), session.root,
		Deps{Commands: session.runtime}); err == nil ||
		!strings.Contains(err.Error(), "装 /goal 失败") {
		t.Fatalf("重名该拒，实际 %v", err)
	}
}

// TestRegisterInvariantsReservesThePackage 钉住这个包在注册表里占住了位置，
// 尽管它一条检查都不装。
func TestRegisterInvariantsReservesThePackage(t *testing.T) {
	t.Parallel()
	if _, err := RegisterInvariants(t.Context(), nil); err == nil {
		t.Fatalf("没有注册表该报错")
	}
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("造注册表失败：%v", err)
	}
	unregister, err := RegisterInvariants(t.Context(), registry)
	if err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	unregister()
}

// itoa 把一个数排成十进制，省得为了一行断言引 strconv。
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}

func (a *stubAgent) Remove(llm.MessageID) {}

func (a *stubAgent) Replace(llm.MessageID, llm.Message) {}
