// 本文件的作用：大纲那个折叠的测试——回合边界、第一句人话、回复定稿，
// 以及「一个回合最多推三次」那道闸。

package turnoutline

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/sessionlog/projection"
)

// event 造一条事件；负载排不出去就判失败。
func event(t *testing.T, kind sessionlog.EventType, seq int, payload any) sessionlog.Event {
	t.Helper()

	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("负载排不出去：%v", err)
	}
	return sessionlog.Event{Type: kind, Seq: seq, Data: encoded}
}

// turnStart、turnEnd 是两条回合边界的简写。
func turnStart(t *testing.T, seq, turn int) sessionlog.Event {
	t.Helper()
	return event(t, sessionlog.EventTurnStart, seq, sessionlog.TurnStartData{Turn: turn})
}

func turnEnd(t *testing.T, seq, turn int) sessionlog.Event {
	t.Helper()
	return event(t, sessionlog.EventTurnEnd, seq, sessionlog.TurnEndData{
		Turn: turn, Reason: sessionlog.CompletedTurnEnd{},
	})
}

// prompt 造一条用户自己说的消息。
func prompt(t *testing.T, seq int, blocks ...llm.ContentBlock) sessionlog.Event {
	t.Helper()
	return sourcedMessage(t, seq, llm.UserSource{}, blocks...)
}

// sourcedMessage 造一条指定来源的用户角色消息。
func sourcedMessage(t *testing.T, seq int, source llm.MessageSource, blocks ...llm.ContentBlock) sessionlog.Event {
	t.Helper()
	return event(t, sessionlog.EventUserMessage, seq, sessionlog.UserMessageData{
		Message: llm.NewUserMessage(llm.Content(blocks), source),
	})
}

// assistant 造一条装配好的助手消息。
func assistant(t *testing.T, seq, turn, step int, blocks ...llm.ContentBlock) sessionlog.Event {
	t.Helper()
	return event(t, sessionlog.EventAssistantMessage, seq, sessionlog.AssistantMessageData{
		Turn: turn,
		Step: step,
		Message: llm.NewAssistantMessage(llm.Content(blocks), llm.Provenance{
			Provider: "deepseek-official", Model: "deepseek-v4-flash",
		}),
	})
}

// text 是一个纯文本块的简写。
func text(s string) llm.ContentBlock { return llm.TextBlock{Text: s} }

// fold 把这些事件顺次折过大纲单元，交出最后的状态和每一次报「视图变了」的 seq。
func fold(events ...sessionlog.Event) (outlineState, []int) {
	state := outlineState{Turns: []Entry{}}
	var pushes []int
	for _, each := range events {
		next, changed := applyOutline(state, each)
		state = next
		if changed {
			pushes = append(pushes, each.Seq)
		}
	}
	return state, pushes
}

// ---- 折叠 ----

// 空日志上的大纲是空的。
func TestEmptyLogFoldsToAnEmptyOutline(t *testing.T) {
	state, pushes := fold()
	if len(state.Turns) != 0 || state.Draft != "" || pushes != nil {
		t.Fatalf("空日志该折出一份空大纲，实际 %#v（推了 %v）", state, pushes)
	}
}

// 每个回合记下它的边界 seq、第一句人话、以及回合结束时定稿的回复。
func TestFoldRecordsBoundarySeqFirstPromptAndSettledResponse(t *testing.T) {
	state, _ := fold(
		turnStart(t, 3, 1),
		prompt(t, 4, text("你好世界")),
		prompt(t, 5, text("后来这句插话不该顶掉第一句")),
		assistant(t, 6, 1, 1, text("第一稿")),
		assistant(t, 7, 1, 2, text("第一个回合的最终答复")),
		turnEnd(t, 8, 1),
		turnStart(t, 9, 2),
		prompt(t, 10, text("第二句提示词")),
	)

	want := []Entry{
		{Turn: 1, Seq: 3, Prompt: "你好世界", Response: "第一个回合的最终答复"},
		{Turn: 2, Seq: 9, Prompt: "第二句提示词"},
	}
	if !slices.Equal(state.Turns, want) {
		t.Fatalf("大纲对不上，想要 %#v，实际 %#v", want, state.Turns)
	}
}

// 回合还开着的时候回复是空的，话攒在草稿里，turn/end 才定稿。
func TestResponseStaysEmptyUntilTheTurnEnds(t *testing.T) {
	open, _ := fold(
		turnStart(t, 0, 1),
		prompt(t, 1, text("提示词")),
		assistant(t, 2, 1, 1, text("流出来了但还没定")),
	)
	if open.Turns[0].Response != "" {
		t.Fatalf("回合没结束就不该有回复，实际 %q", open.Turns[0].Response)
	}
	if open.Draft != "流出来了但还没定" {
		t.Fatalf("话该攒在草稿里，实际 %q", open.Draft)
	}

	settled, _ := applyOutline(open, turnEnd(t, 3, 1))
	if settled.Turns[0].Response != "流出来了但还没定" || settled.Draft != "" {
		t.Fatalf("turn/end 该把草稿定稿并清空，实际 %#v", settled)
	}
}

// 一个回合最多推三次：边界、提示词、定稿的回复。中间的草稿一次都不推。
func TestAtMostThreePushesPerTurn(t *testing.T) {
	state, pushes := fold(
		turnStart(t, 0, 1),
		event(t, sessionlog.EventStepStart, 1, sessionlog.StepStartData{Turn: 1, Step: 1}),
		prompt(t, 2, text("你好")),
		prompt(t, 3, text("同一个回合里的第二句人话")),
		assistant(t, 4, 1, 1, text("草稿一")),
		assistant(t, 5, 1, 2, text("草稿二")),
		event(t, sessionlog.EventStepEnd, 6, sessionlog.StepEndData{Turn: 1, Step: 2}),
		turnEnd(t, 7, 1),
	)

	if want := []int{0, 2, 7}; !slices.Equal(pushes, want) {
		t.Fatalf("该在这三条上推，想要 %v，实际 %v", want, pushes)
	}
	if state.Turns[0].Response != "草稿二" {
		t.Fatalf("定稿的该是最后那份草稿，实际 %q", state.Turns[0].Response)
	}
}

// 草稿变了但视图没变：状态照样更新，只是不推。
func TestDraftOnlyChangeUpdatesStateWithoutPushing(t *testing.T) {
	before, _ := fold(turnStart(t, 0, 1))
	after, changed := applyOutline(before, assistant(t, 1, 1, 1, text("话")))

	if changed {
		t.Fatal("只动草稿不该报「视图变了」")
	}
	if after.Draft != "话" {
		t.Fatalf("状态该更新到新草稿，实际 %q", after.Draft)
	}
}

// 不是人说的那些 user/message，以及回合开始之前的提示词，都不进大纲。
func TestNonHumanSourcesAndPreTurnPromptsAreIgnored(t *testing.T) {
	state, _ := fold(
		prompt(t, 0, text("回合还没开就排在这儿了")),
		turnStart(t, 1, 1),
		sourcedMessage(t, 2, llm.PluginSource{Plugin: "test-injector"}, text("注入的上下文")),
	)

	want := []Entry{{Turn: 1, Seq: 1}}
	if !slices.Equal(state.Turns, want) {
		t.Fatalf("这两条都不该进大纲，想要 %#v，实际 %#v", want, state.Turns)
	}
}

// 一句只有空白的提示词洗完是空的，那一行就保持没标注；没有草稿的 turn/end 同样安静。
func TestWhitespaceOnlyPromptAndDraftlessEndStayQuiet(t *testing.T) {
	state, pushes := fold(
		turnStart(t, 0, 1),
		prompt(t, 1, text(" \t  ")),
		turnEnd(t, 2, 1),
	)

	want := []Entry{{Turn: 1, Seq: 0}}
	if !slices.Equal(state.Turns, want) {
		t.Fatalf("这一行该保持没标注，想要 %#v，实际 %#v", want, state.Turns)
	}
	if got := []int{0}; !slices.Equal(pushes, got) {
		t.Fatalf("只该在边界那一条上推，实际 %v", pushes)
	}
}

// 不推进回合号的边界一律跳过：大纲要保持有序，重试的回合填回原来那一行。
func TestBoundaryThatDoesNotAdvanceTheTurnIsSkipped(t *testing.T) {
	before := outlineState{Turns: []Entry{{Turn: 2, Seq: 5, Prompt: "留着"}}}
	after, changed := applyOutline(before, turnStart(t, 9, 2))

	if changed || !slices.Equal(after.Turns, before.Turns) || after.Draft != "" {
		t.Fatalf("回退的边界该被跳过，实际 %#v（changed=%v）", after, changed)
	}
}

// 一份没有行可定稿的草稿在回合结束时把自己清掉；重复定稿同一句话不动那些行。
func TestOrphanDraftClearsAndIdenticalResettleKeepsEntries(t *testing.T) {
	orphan, changed := applyOutline(outlineState{Turns: []Entry{}, Draft: "没主的草稿"}, turnEnd(t, 11, 1))
	if changed || len(orphan.Turns) != 0 || orphan.Draft != "" {
		t.Fatalf("没主的草稿该被清掉且不推，实际 %#v（changed=%v）", orphan, changed)
	}

	settled := outlineState{Turns: []Entry{{Turn: 1, Prompt: "p", Response: "定了"}}, Draft: "定了"}
	again, changed := applyOutline(settled, turnEnd(t, 11, 1))
	if changed || !slices.Equal(again.Turns, settled.Turns) || again.Draft != "" {
		t.Fatalf("重复定稿同一句该只清草稿，实际 %#v（changed=%v）", again, changed)
	}
}

// 同一份草稿再来一次、或者一条压根没文字的消息，什么都不改。
func TestRepeatedAndTextlessDraftsChangeNothing(t *testing.T) {
	base := outlineState{Turns: []Entry{{Turn: 1, Prompt: "p"}}, Draft: "已经是这句了"}

	for name, each := range map[string]sessionlog.Event{
		"同一句":  assistant(t, 9, 1, 1, text("已经是这句了")),
		"没有文字": assistant(t, 9, 1, 1, text("   ")),
	} {
		t.Run(name, func(t *testing.T) {
			after, changed := applyOutline(base, each)
			if changed || after.Draft != base.Draft {
				t.Fatalf("不该改动任何东西，实际 %#v（changed=%v）", after, changed)
			}
		})
	}
}

// 读不回来的负载只能被跳过——折叠没有报错这条路。
func TestUndecodablePayloadsAreSkipped(t *testing.T) {
	base := outlineState{Turns: []Entry{{Turn: 1, Seq: 0}}}
	broken := []sessionlog.EventType{
		sessionlog.EventTurnStart, sessionlog.EventUserMessage, sessionlog.EventAssistantMessage,
	}
	for _, kind := range broken {
		t.Run(string(kind), func(t *testing.T) {
			after, changed := applyOutline(base, sessionlog.Event{
				Type: kind, Seq: 7, Data: json.RawMessage(`7`),
			})
			if changed || !slices.Equal(after.Turns, base.Turns) {
				t.Fatalf("坏负载该被跳过，实际 %#v（changed=%v）", after, changed)
			}
		})
	}
}

// ---- 登记 ----

// 登进注册表之后大纲出现在读切里，注销之后这个键就消失。
func TestRegisterAndUnregisterFlipTheKey(t *testing.T) {
	registry := projection.NewRegistry()
	session := &stubSession{id: "outlined", events: []sessionlog.Event{turnStart(t, 0, 1)}}

	if _, ok := registry.Snapshot(session).Values[ProjectionKey]; ok {
		t.Fatal("没登记之前不该有这个键")
	}

	unregister, err := RegisterProjection(registry)
	if err != nil {
		t.Fatalf("登记不该失败：%v", err)
	}
	// 单元格是懒建的：登记发生在事件已经进日志之后，读的时候现折一遍。
	turns, ok := registry.Snapshot(session).Values[ProjectionKey].([]Entry)
	if !ok || !slices.Equal(turns, []Entry{{Turn: 1, Seq: 0}}) {
		t.Fatalf("该现折出那一行，实际 %#v", registry.Snapshot(session).Values[ProjectionKey])
	}

	unregister()
	if _, ok := registry.Snapshot(session).Values[ProjectionKey]; ok {
		t.Fatal("注销之后这个键该消失")
	}
}

// 没有注册表就没得登记。
func TestRegisterRejectsANilRegistry(t *testing.T) {
	if _, err := RegisterProjection(nil); err == nil {
		t.Fatal("没有注册表该报错")
	}
}

// 空日志上的检查点是一份空大纲，而且排得出去。
func TestCheckpointOfAnEmptyLogRoundTrips(t *testing.T) {
	registry := projection.NewRegistry()
	unregister, err := RegisterProjection(registry)
	if err != nil {
		t.Fatalf("登记不该失败：%v", err)
	}
	defer unregister()

	rows, err := registry.Checkpoint(&stubSession{id: "outlined"})
	if err != nil {
		t.Fatalf("检查点不该失败：%v", err)
	}
	row := rows[ProjectionKey]
	if row.Ver != projectionStateVersion || row.Seq != -1 {
		t.Fatalf("检查点的版本或水位不对：%#v", row)
	}
	if got := string(row.Val); got != `{"turns":[],"draft":""}` {
		t.Fatalf("空大纲该排成一个空数组，实际 %s", got)
	}
}

// ---- 落盘状态读回来 ----

// 回合号不是严格递增的检查点一律拒掉：折叠只看最后一行，乱序不会报错，
// 只会把往后每一个回合的预览填到错的行上。
func TestDecodeStateRejectsOutOfOrderTurns(t *testing.T) {
	if _, err := decodeState(json.RawMessage(
		`{"turns":[{"turn":2,"seq":1,"prompt":"","response":""},{"turn":2,"seq":4,"prompt":"","response":""}],"draft":""}`,
	)); err == nil {
		t.Fatal("回合号不递增该被拒")
	}

	if _, err := decodeState(json.RawMessage(
		`{"turns":[{"turn":1,"seq":1,"prompt":"ok","response":"done"},{"turn":2,"seq":4,"prompt":"","response":""}],"draft":""}`,
	)); err != nil {
		t.Fatalf("递增的检查点该读得回来：%v", err)
	}
}

// 其余那些形状不对的检查点也都拒掉。
func TestDecodeStateRejectsMalformedCheckpoints(t *testing.T) {
	for name, raw := range map[string]string{
		"多出来的字段":       `{"turns":[],"draft":"","extra":1}`,
		"turns 是 null": `{"turns":null,"draft":""}`,
		"回合号是负的":       `{"turns":[{"turn":-1,"seq":0,"prompt":"","response":""}],"draft":""}`,
		"seq 是负的":      `{"turns":[{"turn":0,"seq":-1,"prompt":"","response":""}],"draft":""}`,
		"提示词超预算": `{"turns":[{"turn":0,"seq":0,"prompt":"` +
			strings.Repeat("字", PromptPreviewMaxChars+1) + `","response":""}],"draft":""}`,
		"回复超预算": `{"turns":[{"turn":0,"seq":0,"prompt":"","response":"` +
			strings.Repeat("字", ResponsePreviewMaxChars+1) + `"}],"draft":""}`,
		"草稿超预算": `{"turns":[],"draft":"` +
			strings.Repeat("字", ResponsePreviewMaxChars+1) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeState(json.RawMessage(raw)); err == nil {
				t.Fatalf("这份检查点该被拒：%s", raw)
			}
		})
	}
}

// 折出来的状态排出去再读回来是同一份东西。
func TestFoldedStateSurvivesACheckpointRoundTrip(t *testing.T) {
	state, _ := fold(
		turnStart(t, 0, 1),
		prompt(t, 1, text("提示词")),
		assistant(t, 2, 1, 1, text("答复")),
		turnEnd(t, 3, 1),
		turnStart(t, 4, 2),
		assistant(t, 5, 2, 1, text("还没定的草稿")),
	)

	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("状态排不出去：%v", err)
	}
	decoded, err := decodeState(encoded)
	if err != nil {
		t.Fatalf("状态读不回来：%v", err)
	}
	if !slices.Equal(decoded.Turns, state.Turns) || decoded.Draft != state.Draft {
		t.Fatalf("往返之后不是同一份，想要 %#v，实际 %#v", state, decoded)
	}
}

// stubSession 是一个只把日志摆在那儿的会话，用来喂注册表。
type stubSession struct {
	id     sessionlog.SessionID
	events []sessionlog.Event
}

func (s *stubSession) ID() sessionlog.SessionID { return s.id }

func (s *stubSession) Events() []sessionlog.Event { return s.events }

func (s *stubSession) NextSeq() int {
	if len(s.events) == 0 {
		return 0
	}
	return s.events[len(s.events)-1].Seq + 1
}
