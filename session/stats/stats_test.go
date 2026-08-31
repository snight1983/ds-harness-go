// 本文件的作用：把这个单元的每一条折法钉住——数出来的数、算出来的墙上时间、
// 每一处「这条事件不该改变什么」的判断，以及一份落过盘的状态回来时那道校验。
//
// 源: packages/session/session-stats/src/projection.ts

package stats

import (
	"encoding/json"
	"strings"
	"testing"

	"ds-harness-go/llm"
	"ds-harness-go/session"
	"ds-harness-go/session/projection"
)

// event 造一条带着这份负载的事件。
func event(t *testing.T, kind session.EventType, at int64, payload any) session.Event {
	t.Helper()

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("负载排不出去：%v", err)
	}
	return session.Event{Type: kind, Time: at, Data: data}
}

// stepStart 造一条步骤开始。
func stepStart(t *testing.T, turn, step int, at int64) session.Event {
	t.Helper()

	return event(t, session.EventStepStart, at, session.StepStartData{Turn: turn, Step: step})
}

// stepEnd 造一条步骤结束。
func stepEnd(t *testing.T, turn, step int, at int64) session.Event {
	t.Helper()

	return event(t, session.EventStepEnd, at, session.StepEndData{Turn: turn, Step: step})
}

// chunk 造一条助手分块。
func chunk(t *testing.T, turn, step int, at int64, piece llm.StreamChunk) session.Event {
	t.Helper()

	return event(t, session.EventAssistantChunk, at,
		session.AssistantChunkData{Turn: turn, Step: step, Chunk: piece})
}

// text 是一段非空的文本增量，也就是一次货真价实的首字。
func text(body string) llm.StreamChunk { return llm.TextDeltaChunk{Text: body} }

// assistantMessage 造一条装配好的助手消息，usage 为 nil 表示适配器没报记账。
func assistantMessage(t *testing.T, turn, step int, at int64, usage *llm.TokenUsage) session.Event {
	t.Helper()

	return event(t, session.EventAssistantMessage, at, session.AssistantMessageData{
		Turn:    turn,
		Step:    step,
		Message: llm.NewAssistantMessage(llm.Content{llm.TextBlock{Text: "hi"}}, llm.Provenance{}),
		Usage:   usage,
	})
}

// toolCall 造一条工具调用派发。
func toolCall(t *testing.T, turn, step int, at int64, callID llm.CallID) session.Event {
	t.Helper()

	return event(t, session.EventToolCall, at, session.ToolCallData{
		Turn: turn, Step: step, CallID: callID, Name: "read", Arguments: "{}",
	})
}

// toolResult 造一条工具结果。
func toolResult(t *testing.T, turn, step int, at int64, callID llm.CallID) session.Event {
	t.Helper()

	return event(t, session.EventToolResult, at, session.ToolResultData{
		Turn:    turn,
		Step:    step,
		Message: llm.NewToolResultMessage(callID, llm.Content{llm.TextBlock{Text: "ok"}}, false),
	})
}

// turnEnd 造一条回合结束。
func turnEnd(t *testing.T, turn int, at int64) session.Event {
	t.Helper()

	return event(t, session.EventTurnEnd, at,
		session.TurnEndData{Turn: turn, Reason: session.CompletedTurnEnd{}})
}

// fold 把这些事件依次折进一份全新的状态，顺带记下每一步说自己改没改。
func fold(t *testing.T, events ...session.Event) (State, []bool) {
	t.Helper()

	definition := Definition()
	state := definition.Init()
	changes := make([]bool, 0, len(events))
	for _, item := range events {
		var changed bool
		state, changed = definition.Apply(state, item)
		changes = append(changes, changed)
	}
	return state, changes
}

// figuresOf 折完之后只取那组数字。
func figuresOf(t *testing.T, events ...session.Event) Figures {
	t.Helper()

	state, _ := fold(t, events...)
	return state.Figures
}

func TestTheUnitDeclaresItsKeyAndStateVersion(t *testing.T) {
	t.Parallel()

	// 这两个字符串/数字是介质上的约定：键换了客户端就读不到，版本号往回退
	// 会让一批本该作废的旧行被当成好行折下去。
	if Key != "sessionStats" {
		t.Fatalf("投影键不对：%q", Key)
	}
	if StateVersion != 1 {
		t.Fatalf("状态版本号不对：%d", StateVersion)
	}

	definition := Definition()
	if definition.Key != Key || definition.StateVersion != StateVersion {
		t.Fatalf("交出来的单元和常量对不上：%q %d", definition.Key, definition.StateVersion)
	}
}

func TestInitIsAllZerosWithARealEmptyTable(t *testing.T) {
	t.Parallel()

	state := Definition().Init()
	if state.Figures != (Figures{}) {
		t.Fatalf("每个数字在第一条有贡献的事件之前都该是零：%#v", state.Figures)
	}
	if state.LastTurn != nil || state.OpenStep != nil {
		t.Fatalf("空日志上没有在途边界：%#v %#v", state.LastTurn, state.OpenStep)
	}
	if state.PendingCalls == nil {
		t.Fatalf("挂着的调用表该是一张真表，不是 nil")
	}
}

func TestInitHandsOutAFreshTableEveryTime(t *testing.T) {
	t.Parallel()

	// [Definition] 是函数不是包级变量，正因为这张表：一份共用的初始状态会让
	// 一个会话的挂起调用出现在另一个会话的账上。
	definition := Definition()
	first, second := definition.Init(), definition.Init()
	first.PendingCalls["c1"] = 1
	if len(second.PendingCalls) != 0 {
		t.Fatalf("两次 Init 不该共用同一张表：%#v", second.PendingCalls)
	}
}

func TestOneCompleteStepFoldsIntoEveryTimeFigure(t *testing.T) {
	t.Parallel()

	// 一个完整步骤把四段墙上时间一次都摆出来：模型时间 100→400，
	// 首字 100→180，解码 180→400，工具 200→260。
	got := figuresOf(t,
		stepStart(t, 0, 0, 100),
		chunk(t, 0, 0, 180, text("你")),
		toolCall(t, 0, 0, 200, "c1"),
		toolResult(t, 0, 0, 260, "c1"),
		assistantMessage(t, 0, 0, 400, &llm.TokenUsage{OutputTokens: 42}),
		stepEnd(t, 0, 0, 410),
	)
	want := Figures{
		Turns: 1, Steps: 1,
		LLMMs: 300, ToolMs: 60,
		TTFTMs: 80, TTFTSteps: 1,
		DecodeMs: 220, DecodeTokens: 42,
	}
	if got != want {
		t.Fatalf("数字不对：%#v", got)
	}
}

func TestStepsCountEveryClosedStepAndTurnsCountDistinctTurns(t *testing.T) {
	t.Parallel()

	// 数的是 step/end 而不是装配好的助手消息：这三个步骤一条消息都没有，
	// 照样各算一个（撞上 token 上限的、被取消的步骤就是这样）。
	got := figuresOf(t,
		stepEnd(t, 0, 0, 10),
		stepEnd(t, 0, 1, 20),
		stepEnd(t, 1, 0, 30),
		stepEnd(t, 1, 1, 40),
	)
	if got.Steps != 4 || got.Turns != 2 {
		t.Fatalf("该是 4 个步骤 2 个回合：%#v", got)
	}
}

func TestTurnZeroCountsBecauseTheCounterIsNotAZeroValue(t *testing.T) {
	t.Parallel()

	// 「还没数过任何步骤」用 nil 表达而不是用 0：回合号本来就从 0 起，
	// 拿 0 当哨兵会让第一个回合数不进去。
	state, _ := fold(t, stepEnd(t, 0, 0, 10))
	if state.Turns != 1 {
		t.Fatalf("第 0 个回合也该数进去：%d", state.Turns)
	}
	if state.LastTurn == nil || *state.LastTurn != 0 {
		t.Fatalf("最后那个回合号该记下来：%#v", state.LastTurn)
	}
}

func TestGoingBackToAnEarlierTurnStartsANewCount(t *testing.T) {
	t.Parallel()

	// 判据是「和上一个不同」，不是「比上一个大」。回合号在一个会话内单调，
	// 所以这两条判据平时等价；写成前者是因为它不必假设单调也对。
	got := figuresOf(t, stepEnd(t, 1, 0, 10), stepEnd(t, 0, 0, 20))
	if got.Turns != 2 {
		t.Fatalf("换了一个回合号就该另算一个回合：%#v", got)
	}
}

func TestFirstTokenIsTheFirstNonEmptyDeltaAndNothingAfterIt(t *testing.T) {
	t.Parallel()

	// 空文本增量、用量分块都不算模型真的吐出了东西，首字要落在 300 那一条上。
	got := figuresOf(t,
		stepStart(t, 0, 0, 100),
		chunk(t, 0, 0, 150, llm.TextDeltaChunk{Text: ""}),
		chunk(t, 0, 0, 200, llm.UsageChunk{Usage: llm.TokenUsage{OutputTokens: 1}}),
		chunk(t, 0, 0, 300, text("你")),
		chunk(t, 0, 0, 350, text("好")),
		assistantMessage(t, 0, 0, 500, nil),
	)
	if got.TTFTMs != 200 || got.TTFTSteps != 1 {
		t.Fatalf("首字该落在 300 那一条上：%#v", got)
	}
}

func TestReasoningAndToolArgumentDeltasAlsoCountAsFirstToken(t *testing.T) {
	t.Parallel()

	// 首字的判据整个交给 [llm.IsTokenDelta]，这里钉住「本单元没有自己另立一套」。
	name := "read"
	cases := map[string]llm.StreamChunk{
		"推理增量":   llm.ReasoningDeltaChunk{Text: "嗯"},
		"工具参数增量": llm.ToolCallDeltaChunk{ID: "c1", ArgumentsDelta: `{"a`},
		"只带工具名":  llm.ToolCallDeltaChunk{ID: "c1", Name: &name},
	}

	for label, piece := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			got := figuresOf(t,
				stepStart(t, 0, 0, 100),
				chunk(t, 0, 0, 160, piece),
				assistantMessage(t, 0, 0, 300, nil),
			)
			if got.TTFTSteps != 1 || got.TTFTMs != 60 {
				t.Fatalf("这一块该算首字：%#v", got)
			}
		})
	}
}

func TestAStepWithoutAFirstTokenTimesOnlyTheModel(t *testing.T) {
	t.Parallel()

	// 一个字都没流出来（非流式适配器、或者整段被缓存命中）的步骤照样有模型
	// 时间，但没有首字、也没有解码——那两个数是有首字才算得出来的。
	got := figuresOf(t,
		stepStart(t, 0, 0, 100),
		assistantMessage(t, 0, 0, 400, &llm.TokenUsage{OutputTokens: 9}),
	)
	if got.LLMMs != 300 {
		t.Fatalf("模型时间该照算：%#v", got)
	}
	if got.TTFTMs != 0 || got.TTFTSteps != 0 || got.DecodeMs != 0 || got.DecodeTokens != 0 {
		t.Fatalf("没有首字就没有首字和解码这两组数：%#v", got)
	}
}

func TestUsageDecidesOnlyTheDecodeFiguresNotTheTTFTOnes(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		usage        *llm.TokenUsage
		decodeMs     int64
		decodeTokens int
	}{
		"适配器没报记账":  {usage: nil},
		"报了负的输出":   {usage: &llm.TokenUsage{OutputTokens: -1}},
		"报了零个输出":   {usage: &llm.TokenUsage{OutputTokens: 0}, decodeMs: 220},
		"报了正常的输出":  {usage: &llm.TokenUsage{OutputTokens: 42}, decodeMs: 220, decodeTokens: 42},
		"只报了输入侧的数": {usage: &llm.TokenUsage{InputTokens: 7}, decodeMs: 220},
	}

	for label, item := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			// 「没报」和「报了一份零」是两件事：后者说明这次调用确实没吐 token，
			// 前者说明我们不知道，不知道的那一份不该混进解码时间的分母里。
			got := figuresOf(t,
				stepStart(t, 0, 0, 100),
				chunk(t, 0, 0, 180, text("你")),
				assistantMessage(t, 0, 0, 400, item.usage),
			)
			if got.DecodeMs != item.decodeMs || got.DecodeTokens != item.decodeTokens {
				t.Fatalf("解码那两个数不对：%#v", got)
			}
			if got.TTFTMs != 80 || got.TTFTSteps != 1 {
				t.Fatalf("记账不该影响首字那两个数：%#v", got)
			}
		})
	}
}

func TestTheAssembledMessageClosesTheStepSoADuplicateAccruesNothing(t *testing.T) {
	t.Parallel()

	// 一个步骤只装配一条消息。防御性地再来一条时边界已经关了，第二条什么都加不上。
	state, changes := fold(t,
		stepStart(t, 0, 0, 100),
		chunk(t, 0, 0, 180, text("你")),
		assistantMessage(t, 0, 0, 400, &llm.TokenUsage{OutputTokens: 42}),
		assistantMessage(t, 0, 0, 900, &llm.TokenUsage{OutputTokens: 42}),
	)
	if state.LLMMs != 300 || state.TTFTSteps != 1 || state.DecodeTokens != 42 {
		t.Fatalf("重复的那条不该再累加一次：%#v", state.Figures)
	}
	if changes[3] {
		t.Fatalf("重复的那条什么都没改，该说自己没改")
	}
	if state.OpenStep != nil {
		t.Fatalf("消息装配好之后步骤边界就该关掉：%#v", state.OpenStep)
	}
}

func TestChunksAndMessagesOutsideTheOpenStepChangeNothing(t *testing.T) {
	t.Parallel()

	// 边界只认同一个 (turn, step)。跨步骤串进来的一条分块或者一条消息，
	// 会把另一个步骤的时间算到这个步骤头上。
	open := stepStart(t, 1, 2, 100)

	cases := map[string][]session.Event{
		"根本没开着步骤的分块": {chunk(t, 1, 2, 150, text("你"))},
		"根本没开着步骤的消息": {assistantMessage(t, 1, 2, 150, nil)},
		"回合号对不上的分块":  {open, chunk(t, 0, 2, 150, text("你"))},
		"步骤号对不上的分块":  {open, chunk(t, 1, 3, 150, text("你"))},
		"回合号对不上的消息":  {open, assistantMessage(t, 0, 2, 150, nil)},
		"步骤号对不上的消息":  {open, assistantMessage(t, 1, 3, 150, nil)},
		"首字已经记过的又一条": {open, chunk(t, 1, 2, 150, text("你")), chunk(t, 1, 2, 160, text("好"))},
		"不是增量的那种分块":  {open, chunk(t, 1, 2, 150, llm.FinishChunk{Reason: llm.StopFinish{}})},
	}

	for label, events := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			state, changes := fold(t, events...)
			if changes[len(changes)-1] {
				t.Fatalf("这条事件什么都没改，该说自己没改")
			}
			if state.LLMMs != 0 || state.TTFTMs != 0 {
				t.Fatalf("这条事件不该算出任何墙上时间：%#v", state.Figures)
			}
		})
	}
}

func TestTheFirstTokenSurvivesLaterChunksInTheSameStep(t *testing.T) {
	t.Parallel()

	// 这一条和上面那个「首字已经记过的又一条」是同一件事的另一半：那边看的是
	// 「说自己没改」，这边看的是记下来的那个时刻真的没被后一块盖掉。
	state, _ := fold(t,
		stepStart(t, 0, 0, 100),
		chunk(t, 0, 0, 150, text("你")),
		chunk(t, 0, 0, 900, text("好")),
	)
	if state.OpenStep == nil || state.OpenStep.FirstTokenTime == nil ||
		*state.OpenStep.FirstTokenTime != 150 {
		t.Fatalf("首字时刻该停在第一块上：%#v", state.OpenStep)
	}
}

func TestAStepStartReplacesWhateverBoundaryWasOpen(t *testing.T) {
	t.Parallel()

	// 上一个步骤没装配出消息就被下一个 step/start 顶掉（重试、或者一条被吞掉的
	// 步骤结束）。顶掉的那一个不留下任何时间——它本来就没跑完。
	state, _ := fold(t,
		stepStart(t, 0, 0, 100),
		chunk(t, 0, 0, 150, text("你")),
		stepStart(t, 0, 1, 200),
		assistantMessage(t, 0, 1, 500, nil),
	)
	if state.LLMMs != 300 {
		t.Fatalf("模型时间该从新的那个边界起算：%#v", state.Figures)
	}
	if state.TTFTSteps != 0 {
		t.Fatalf("被顶掉的那个步骤的首字不该记到新步骤头上：%#v", state.Figures)
	}
}

func TestStepEndClosesTheBoundarySoACancelledStepIsUntimed(t *testing.T) {
	t.Parallel()

	// 被取消的步骤装配不出消息，它那段流的时间在每一个时间字段里都不出现——
	// 和客户端窗口把它画成一个没有时长的中断节点是同一个决定。
	state, _ := fold(t,
		stepStart(t, 0, 0, 100),
		chunk(t, 0, 0, 150, text("你")),
		stepEnd(t, 0, 0, 900),
		assistantMessage(t, 0, 0, 950, nil),
	)
	if state.Steps != 1 {
		t.Fatalf("被取消的步骤照样算一个步骤：%#v", state.Figures)
	}
	if state.LLMMs != 0 || state.TTFTMs != 0 {
		t.Fatalf("它不该留下任何墙上时间：%#v", state.Figures)
	}
	if state.OpenStep != nil {
		t.Fatalf("步骤结束该把边界关掉：%#v", state.OpenStep)
	}
}

func TestToolTimePairsByCallIDAndReleasesTheSlot(t *testing.T) {
	t.Parallel()

	// 两次调用交错返回，各自按自己的 callId 配对。
	state, _ := fold(t,
		toolCall(t, 0, 0, 100, "c1"),
		toolCall(t, 0, 0, 120, "c2"),
		toolResult(t, 0, 0, 200, "c2"),
		toolResult(t, 0, 0, 300, "c1"),
	)
	if state.ToolMs != 280 {
		t.Fatalf("工具时间该是 80 加 200：%d", state.ToolMs)
	}
	if len(state.PendingCalls) != 0 {
		t.Fatalf("配上对的调用该从表里去掉：%#v", state.PendingCalls)
	}
}

func TestASecondCallWithTheSameIDReplacesTheDispatchTime(t *testing.T) {
	t.Parallel()

	// 同一个 callId 派发两次（提供方重发）时留下的是后一次的时刻：结果配的是
	// 真正派出去的那一次，用第一次的时刻会把等待时间算长。
	state, _ := fold(t,
		toolCall(t, 0, 0, 100, "c1"),
		toolCall(t, 0, 0, 200, "c1"),
		toolResult(t, 0, 0, 260, "c1"),
	)
	if state.ToolMs != 60 {
		t.Fatalf("该按后一次派发算：%d", state.ToolMs)
	}
}

func TestAToolResultWithNoRecordedCallChangesNothing(t *testing.T) {
	t.Parallel()

	state, changes := fold(t, toolResult(t, 0, 0, 300, "c1"))
	if changes[0] || state.ToolMs != 0 {
		t.Fatalf("没派发过的结果配不上对：%#v", state.Figures)
	}
}

func TestAResultMessageWithoutAToolSourceChangesNothing(t *testing.T) {
	t.Parallel()

	// 配对的键在消息来源上。一条工具结果事件挂着一条别的来源的消息说明产出方
	// 写错了，本单元只能认出「配不上」，不能拿别的字段去猜。
	broken := event(t, session.EventToolResult, 300, session.ToolResultData{
		Message: llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "ok"}}, llm.UserSource{}),
	})
	state, changes := fold(t, toolCall(t, 0, 0, 100, "c1"), broken)
	if changes[1] || state.ToolMs != 0 {
		t.Fatalf("来源不是工具就配不上对：%#v", state.Figures)
	}
}

func TestTurnEndDropsCallsThatNeverCameBack(t *testing.T) {
	t.Parallel()

	// 结果始终在自己的回合内落地，所以回合结束时还挂着的调用属于一个被取消
	// 或者失败的回合。不丢掉的话落盘状态会一直长下去。
	state, changes := fold(t,
		toolCall(t, 0, 0, 100, "c1"),
		turnEnd(t, 0, 200),
		turnEnd(t, 1, 300),
	)
	if !changes[1] {
		t.Fatalf("丢掉了一条挂着的调用，该说自己改了")
	}
	if changes[2] {
		t.Fatalf("表本来就是空的，该说自己没改")
	}
	if len(state.PendingCalls) != 0 || state.ToolMs != 0 {
		t.Fatalf("挂着的调用该被丢掉且不留下时间：%#v %#v", state.PendingCalls, state.Figures)
	}
}

func TestClocksThatRunBackwardsClampToZeroInsteadOfSubtracting(t *testing.T) {
	t.Parallel()

	// 墙上时间由产出事件的那一方写，换机器、对时都可能让后一条比前一条早。
	// 一个负的差值会把已经攒下的总数往回扣，那比丢掉这一段还糟。
	got := figuresOf(t,
		stepStart(t, 0, 0, 1000),
		chunk(t, 0, 0, 900, text("你")),
		toolCall(t, 0, 0, 1000, "c1"),
		toolResult(t, 0, 0, 800, "c1"),
		assistantMessage(t, 0, 0, 700, &llm.TokenUsage{OutputTokens: 5}),
	)
	want := Figures{TTFTSteps: 1, DecodeTokens: 5}
	if got != want {
		t.Fatalf("倒流的时钟该被夹到零，而不是往回扣：%#v", got)
	}
}

func TestUnrelatedEventsAreLeftAlone(t *testing.T) {
	t.Parallel()

	cases := map[string]session.Event{
		"回合开始":   {Type: session.EventTurnStart, Time: 10, Data: json.RawMessage(`{"turn":0}`)},
		"用户消息":   {Type: session.EventUserMessage, Time: 10, Data: json.RawMessage(`{}`)},
		"待办":     {Type: session.EventTodoWrite, Time: 10, Data: json.RawMessage(`{}`)},
		"本构建不认识": {Type: "vendor/whatever", Time: 10, Data: json.RawMessage(`{}`)},
	}

	for label, item := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			if _, changes := fold(t, item); changes[0] {
				t.Fatalf("这条事件和会话数字无关，该说自己没改")
			}
		})
	}
}

func TestAPayloadThatCannotBeReadBackIsPassedOver(t *testing.T) {
	t.Parallel()

	// 一条读不回来的负载说明它在被追加的时候就没验过，那是追加那一侧的缺陷。
	// [projection.Definition.Apply] 没有报错的位置，一个统计单元也没有资格
	// 因为它而让整次读失败——放过去，别的字段照样算得出来。
	kinds := []session.EventType{
		session.EventStepStart,
		session.EventStepEnd,
		session.EventAssistantChunk,
		session.EventAssistantMessage,
		session.EventToolCall,
		session.EventToolResult,
	}

	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()

			broken := session.Event{Type: kind, Time: 10, Data: json.RawMessage(`[]`)}
			state, changes := fold(t, stepStart(t, 0, 0, 1), broken)
			if changes[1] {
				t.Fatalf("读不回来的负载该被放过去")
			}
			if state.Figures != (Figures{}) {
				t.Fatalf("放过去的那条不该改出任何数字：%#v", state.Figures)
			}
		})
	}
}

func TestAnEmptyPayloadReadsAsTheZeroValue(t *testing.T) {
	t.Parallel()

	// 一条负载整个缺席的步骤结束读成 (turn 0, step 0)，和 [session.DecodeData]
	// 的处理一致——这是日志里合法的形状，不是坏掉的形状。
	state, changes := fold(t, session.Event{Type: session.EventStepEnd, Time: 10})
	if !changes[0] || state.Steps != 1 || state.Turns != 1 {
		t.Fatalf("空负载该读成零值：%v %#v", changes, state.Figures)
	}
}

func TestApplyDoesNotWriteThroughToTheStateItWasGiven(t *testing.T) {
	t.Parallel()

	// [projection.Definition.Apply] 声明自己是一个纯转移，而挂着的调用表是
	// 引用语义。就地改会把已经交出去的上一份状态（比如刚取过的一份检查点）
	// 一起改掉。
	definition := Definition()
	before, _ := definition.Apply(definition.Init(), toolCall(t, 0, 0, 100, "c1"))

	added, _ := definition.Apply(before, toolCall(t, 0, 0, 200, "c2"))
	if len(before.PendingCalls) != 1 {
		t.Fatalf("加一条不该改到上一份状态：%#v", before.PendingCalls)
	}
	if len(added.PendingCalls) != 2 {
		t.Fatalf("新的那份该有两条：%#v", added.PendingCalls)
	}

	removed, _ := definition.Apply(before, toolResult(t, 0, 0, 300, "c1"))
	if len(before.PendingCalls) != 1 {
		t.Fatalf("去一条不该改到上一份状态：%#v", before.PendingCalls)
	}
	if len(removed.PendingCalls) != 0 {
		t.Fatalf("新的那份该是空的：%#v", removed.PendingCalls)
	}
}

func TestTheViewIsExactlyTheFigures(t *testing.T) {
	t.Parallel()

	// 视图是状态的一个严格子集：在途边界（开着的步骤、挂着的调用）是本单元
	// 自己的账，不该出现在客户端那一侧。
	state, _ := fold(t,
		stepStart(t, 0, 0, 100),
		chunk(t, 0, 0, 180, text("你")),
		toolCall(t, 0, 0, 200, "c1"),
		assistantMessage(t, 0, 0, 400, &llm.TokenUsage{OutputTokens: 42}),
		stepEnd(t, 0, 0, 410),
	)

	view := Definition().View(state)
	figures, ok := view.(Figures)
	if !ok {
		t.Fatalf("视图该是一份 [Figures]：%T", view)
	}
	if figures != state.Figures {
		t.Fatalf("视图该原样是那组数字：%#v", figures)
	}

	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("视图排不出去：%v", err)
	}
	want := `{"turns":1,"steps":1,"llmMs":300,"toolMs":0,` +
		`"ttftMs":80,"ttftSteps":1,"decodeMs":220,"decodeTokens":42}`
	if string(encoded) != want {
		t.Fatalf("视图在介质上的样子不对：%s", encoded)
	}
}

func TestTheStateLaysTheFiguresFlatOnTheWire(t *testing.T) {
	t.Parallel()

	// [Figures] 是内嵌的，所以它那八个字段和三个边界字段排在同一层，
	// 和 DSH 那份 `extends` 排出来的字节一致。改成具名字段会把旧库读废。
	firstToken := int64(180)
	lastTurn := 0
	encoded, err := json.Marshal(State{
		Figures:      Figures{Turns: 1, Steps: 2, LLMMs: 300, ToolMs: 60, TTFTMs: 80, TTFTSteps: 1, DecodeMs: 220, DecodeTokens: 42},
		LastTurn:     &lastTurn,
		OpenStep:     &OpenStep{Turn: 0, Step: 1, StartTime: 100, FirstTokenTime: &firstToken},
		PendingCalls: map[llm.CallID]int64{"c1": 200},
	})
	if err != nil {
		t.Fatalf("状态排不出去：%v", err)
	}
	want := `{"turns":1,"steps":2,"llmMs":300,"toolMs":60,` +
		`"ttftMs":80,"ttftSteps":1,"decodeMs":220,"decodeTokens":42,` +
		`"lastTurn":0,"openStep":{"turn":0,"step":1,"startTime":100,"firstTokenTime":180},` +
		`"pendingCalls":{"c1":200}}`
	if string(encoded) != want {
		t.Fatalf("状态在介质上的样子不对：%s", encoded)
	}
}

func TestTheEmptyBoundariesGoOutAsNull(t *testing.T) {
	t.Parallel()

	// 三个边界字段都没有 omitempty：一份落过盘的行永远端得出这三个键，
	// 于是读的一方读的是值，不是「键在不在」。
	encoded, err := json.Marshal(Definition().Init())
	if err != nil {
		t.Fatalf("状态排不出去：%v", err)
	}
	for _, fragment := range []string{`"lastTurn":null`, `"openStep":null`, `"pendingCalls":{}`} {
		if !strings.Contains(string(encoded), fragment) {
			t.Fatalf("该带上 %s：%s", fragment, encoded)
		}
	}
}

func TestAStateRoundTripsThroughTheDisk(t *testing.T) {
	t.Parallel()

	// 折一半、落一次盘、读回来接着折，得到的数字要和一口气折完一样——
	// 这正是持久检查点存在的前提。
	events := []session.Event{
		stepStart(t, 0, 0, 100),
		chunk(t, 0, 0, 180, text("你")),
		toolCall(t, 0, 0, 200, "c1"),
		toolResult(t, 0, 0, 260, "c1"),
		assistantMessage(t, 0, 0, 400, &llm.TokenUsage{OutputTokens: 42}),
		stepEnd(t, 0, 0, 410),
	}
	whole, _ := fold(t, events...)

	definition := Definition()
	half, _ := fold(t, events[:3]...)
	encoded, err := json.Marshal(half)
	if err != nil {
		t.Fatalf("状态排不出去：%v", err)
	}
	restored, err := definition.DecodeState(encoded)
	if err != nil {
		t.Fatalf("状态该读得回来：%v", err)
	}
	for _, item := range events[3:] {
		restored, _ = definition.Apply(restored, item)
	}
	if restored.Figures != whole.Figures {
		t.Fatalf("接着折出来的数字该和一口气折完一样：%#v", restored.Figures)
	}
}

func TestDecodeStateRefusesBytesThatAreNotThisUnitsState(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"根本不是一个对象": `[]`,
		"多了一个不认识的键": `{"turns":0,"steps":0,"llmMs":0,"toolMs":0,"ttftMs":0,` +
			`"ttftSteps":0,"decodeMs":0,"decodeTokens":0,` +
			`"lastTurn":null,"openStep":null,"pendingCalls":{},"extra":1}`,
		"字段类型对不上": `{"turns":"一"}`,
	}

	for label, body := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			// 盘上的字节可能是另一个构建写的、可能被人手改过。挡不住这些，
			// 一份垃圾状态会被当成好状态往前折。
			if _, err := Definition().DecodeState(json.RawMessage(body)); err == nil {
				t.Fatalf("该拒掉")
			}
		})
	}
}

func TestDecodeStateFillsInAMissingCallTable(t *testing.T) {
	t.Parallel()

	// 一行缺了这个键（或者写的是 null）。Go 的空表和缺席在读上完全等价，
	// 但往一张 nil 表里写会炸，所以在这里补齐一次。
	body := `{"turns":0,"steps":0,"llmMs":0,"toolMs":0,"ttftMs":0,` +
		`"ttftSteps":0,"decodeMs":0,"decodeTokens":0,` +
		`"lastTurn":null,"openStep":null,"pendingCalls":null}`
	state, err := Definition().DecodeState(json.RawMessage(body))
	if err != nil {
		t.Fatalf("该收下：%v", err)
	}
	if state.PendingCalls == nil {
		t.Fatalf("挂着的调用表该被补成一张真表")
	}
	if _, changed := Definition().Apply(state, toolCall(t, 0, 0, 100, "c1")); !changed {
		t.Fatalf("补齐之后该能接着折")
	}
}

func TestDecodeStateRunsTheRangeChecks(t *testing.T) {
	t.Parallel()

	// 校验挂在读的这条路上，所以一份越界的行进不来，而不是被当成好行折下去。
	body := `{"turns":-1,"steps":0,"llmMs":0,"toolMs":0,"ttftMs":0,` +
		`"ttftSteps":0,"decodeMs":0,"decodeTokens":0,` +
		`"lastTurn":null,"openStep":null,"pendingCalls":{}}`
	err := errFrom(Definition().DecodeState(json.RawMessage(body)))
	if err == nil || !strings.Contains(err.Error(), "turns") {
		t.Fatalf("该点出是哪个数越界了：%v", err)
	}
}

// errFrom 只取第二个返回值，好让上面那一句写成一行。
func errFrom(_ State, err error) error { return err }

func TestValidateAcceptsTheStatesTheFoldCanProduce(t *testing.T) {
	t.Parallel()

	zero, firstToken := 0, int64(180)
	cases := map[string]State{
		"全新的状态": {PendingCalls: map[llm.CallID]int64{}},
		"折过一段的状态": {
			Figures:      Figures{Turns: 1, Steps: 1, LLMMs: 300, TTFTMs: 80, TTFTSteps: 1},
			LastTurn:     &zero,
			OpenStep:     &OpenStep{StartTime: 100, FirstTokenTime: &firstToken},
			PendingCalls: map[llm.CallID]int64{"c1": 200},
		},
		"开着步骤但还没有首字": {OpenStep: &OpenStep{StartTime: 100}},
		"时刻是零":       {OpenStep: &OpenStep{StartTime: 0}, LastTurn: &zero},
	}

	for label, item := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			if err := item.Validate(); err != nil {
				t.Fatalf("该收下：%v", err)
			}
		})
	}
}

func TestValidateNamesTheFieldThatIsOutOfRange(t *testing.T) {
	t.Parallel()

	negative, negativeTime := -1, int64(-1)
	cases := map[string]struct {
		state State
		want  string
	}{
		"回合数是负的":       {state: State{Figures: Figures{Turns: -1}}, want: "turns"},
		"步骤数是负的":       {state: State{Figures: Figures{Steps: -1}}, want: "steps"},
		"模型时间是负的":      {state: State{Figures: Figures{LLMMs: -1}}, want: "llmMs"},
		"工具时间是负的":      {state: State{Figures: Figures{ToolMs: -1}}, want: "toolMs"},
		"首字时间是负的":      {state: State{Figures: Figures{TTFTMs: -1}}, want: "ttftMs"},
		"首字步骤数是负的":     {state: State{Figures: Figures{TTFTSteps: -1}}, want: "ttftSteps"},
		"解码时间是负的":      {state: State{Figures: Figures{DecodeMs: -1}}, want: "decodeMs"},
		"解码 token 是负的": {state: State{Figures: Figures{DecodeTokens: -1}}, want: "decodeTokens"},
		"最后那个回合号是负的":   {state: State{LastTurn: &negative}, want: "lastTurn"},
		"开着的回合号是负的":    {state: State{OpenStep: &OpenStep{Turn: -1}}, want: "openStep.turn"},
		"开着的步骤号是负的":    {state: State{OpenStep: &OpenStep{Step: -1}}, want: "openStep.step"},
		"步骤开始时刻是负的":    {state: State{OpenStep: &OpenStep{StartTime: -1}}, want: "openStep.startTime"},
		"首字时刻是负的": {
			state: State{OpenStep: &OpenStep{FirstTokenTime: &negativeTime}},
			want:  "openStep.firstTokenTime",
		},
		"挂着的派发时刻是负的": {
			state: State{PendingCalls: map[llm.CallID]int64{"c1": -1}},
			want:  `pendingCalls["c1"]`,
		},
	}

	for label, item := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			// 一份状态里十几个数，不点名的话拿到错误的人还得自己去猜是哪一个。
			err := item.state.Validate()
			if err == nil {
				t.Fatalf("该拒掉")
			}
			if !strings.Contains(err.Error(), item.want) {
				t.Fatalf("错误里该点出 %s，实际是 %q", item.want, err.Error())
			}
		})
	}
}

func TestTheUnitGoesIntoARegistryAndComesOutAsASnapshot(t *testing.T) {
	t.Parallel()

	// 单独看每一样都对不出问题来：这一条把登记、折叠、取切面、看视图串起来跑一遍。
	registry := projection.NewRegistry()
	dispose, err := projection.Register(registry, Definition())
	if err != nil {
		t.Fatalf("登记不该失败：%v", err)
	}
	t.Cleanup(dispose)

	view := &fakeSession{events: []session.Event{
		stepStart(t, 0, 0, 100),
		chunk(t, 0, 0, 180, text("你")),
		assistantMessage(t, 0, 0, 400, &llm.TokenUsage{OutputTokens: 42}),
		stepEnd(t, 0, 0, 410),
	}}
	for index := range view.events {
		view.events[index].Seq = index
	}

	snapshot := registry.Snapshot(view)
	figures, ok := snapshot.Values[Key].(Figures)
	if !ok {
		t.Fatalf("切面里该有这个键，而且是一份 [Figures]：%#v", snapshot.Values[Key])
	}
	if figures.Steps != 1 || figures.Turns != 1 || figures.LLMMs != 300 || figures.DecodeTokens != 42 {
		t.Fatalf("切面里的数字不对：%#v", figures)
	}

	rows, err := registry.Checkpoint(view)
	if err != nil {
		t.Fatalf("取检查点不该失败：%v", err)
	}
	row, present := rows[Key]
	if !present || row.Ver != StateVersion || row.Seq != 3 {
		t.Fatalf("检查点行不对：%#v", row)
	}

	restored, err := registry.Restore(rows, nil, 4)
	if err != nil {
		t.Fatalf("从检查点恢复不该失败：%v", err)
	}
	if restored.Snapshot.Values[Key] != any(figures) {
		t.Fatalf("恢复出来的视图该和直折的一样：%#v", restored.Snapshot.Values[Key])
	}
}

// fakeSession 是喂给登记表的那份最小活会话。
type fakeSession struct {
	events []session.Event
}

func (s *fakeSession) ID() session.SessionID { return "s1" }

func (s *fakeSession) Events() []session.Event { return s.events }

func (s *fakeSession) NextSeq() int { return len(s.events) }
