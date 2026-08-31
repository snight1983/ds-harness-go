// 本文件的作用：钉住三个投影单元的折叠——它们各自看见什么、各自漏掉什么，
// 以及它们和服务那份计量之间那条「有意不相等」的界线。

package tokenmeter

import (
	"testing"

	"ds-harness-go/llm"
	"ds-harness-go/session"
	"ds-harness-go/session/projection"
)

// viewOf 从一次读切里取出一个单元当下的客户端视图。
func viewOf(t *testing.T, registry *projection.Registry, view projection.SessionView, key string) any {
	t.Helper()

	snapshot := registry.Snapshot(view)
	value, ok := snapshot.Values[key]
	if !ok {
		t.Fatalf("单元 %q 该出现在读切里", key)
	}
	return value
}

func TestRegisterProjectionsInstallsAllThreeUnits(t *testing.T) {
	t.Parallel()

	registry := projection.NewRegistry()
	dispose, err := RegisterProjections(registry)
	if err != nil {
		t.Fatalf("登记不该失败：%v", err)
	}

	view := newSession(userEvent(t, "hello"))
	for _, key := range []string{TokenUsageProjectionKey, ContextPressureProjectionKey, ContextBreakdownProjectionKey} {
		if _, ok := registry.StateOf(view, key); !ok {
			t.Fatalf("单元 %q 该在表里", key)
		}
	}

	dispose()
	dispose() // 幂等：再调一次不该把别人的键删掉。
	for _, key := range []string{TokenUsageProjectionKey, ContextPressureProjectionKey, ContextBreakdownProjectionKey} {
		if _, ok := registry.StateOf(view, key); ok {
			t.Fatalf("注销之后单元 %q 该读成「这个能力不在」", key)
		}
	}
}

func TestRegisterProjectionsNeedsARegistry(t *testing.T) {
	t.Parallel()

	if _, err := RegisterProjections(nil); err == nil {
		t.Fatal("没有注册表该报错")
	}
}

// 同一个步骤重复报用量是**替换**不是叠加：流中途那条用量分块和它后面那条
// 落定消息报的常常逐字相同，加两遍就把账翻倍了。
func TestTokenUsageReplacesRepeatedSamplesOfTheSameStep(t *testing.T) {
	t.Parallel()

	usage := llm.TokenUsage{InputTokens: 100, OutputTokens: 20, CacheReadTokens: 5, CacheWriteTokens: 3}
	state := tokenUsageState{}

	chunk := chunkEvent(t, 0, 0, llm.UsageChunk{Usage: usage})
	state, changed := applyTokenUsage(state, chunk)
	if !changed {
		t.Fatal("第一次采样该报变化")
	}
	if state.Totals != bucketsFrom(usage) {
		t.Fatalf("第一次采样该原样进账：%+v", state.Totals)
	}

	// 同一个步骤、一模一样的一份：什么都没变。
	settled := assistantEvent(t, 0, 0, "hi", &usage)
	after, changed := applyTokenUsage(state, settled)
	if changed {
		t.Fatal("同一个步骤报了一模一样的一份不该报变化")
	}
	if after.Totals != state.Totals {
		t.Fatalf("重复上报不该改动累计：%+v", after.Totals)
	}

	// 同一个步骤但数字变大了：替换，不叠加。
	grown := llm.TokenUsage{InputTokens: 100, OutputTokens: 50, CacheReadTokens: 5, CacheWriteTokens: 3}
	after, changed = applyTokenUsage(state, assistantEvent(t, 0, 0, "hi there", &grown))
	if !changed {
		t.Fatal("数字变了该报变化")
	}
	if after.Totals != bucketsFrom(grown) {
		t.Fatalf("同一个步骤该被替换掉：想要 %+v，实际 %+v", bucketsFrom(grown), after.Totals)
	}

	// 下一个步骤：这次才是真的往上加。
	next := llm.TokenUsage{InputTokens: 7, OutputTokens: 1}
	after, _ = applyTokenUsage(after, assistantEvent(t, 0, 1, "again", &next))
	if want := grown.InputTokens + next.InputTokens; after.Totals.UncachedInputTokens != want {
		t.Fatalf("下一个步骤该加进来：想要 %d，实际 %d", want, after.Totals.UncachedInputTokens)
	}
}

// 报不出用量的事件一条都不算数。
func TestTokenUsageIgnoresEventsWithoutUsage(t *testing.T) {
	t.Parallel()

	for _, event := range []session.Event{
		userEvent(t, "hello"),
		assistantEvent(t, 0, 0, "hi", nil),
		chunkEvent(t, 0, 0, llm.TextDeltaChunk{Index: 0, Text: "hi"}),
		stepStartEvent(t, 0, 0),
	} {
		if _, changed := applyTokenUsage(tokenUsageState{}, event); changed {
			t.Fatalf("%q 不该被数进累计用量", event.Type)
		}
	}
}

// 占用那个数只算提示词侧，**不含输出**——所以一个回合流着的时候它是不动的。
func TestContextPressureExcludesOutputTokens(t *testing.T) {
	t.Parallel()

	usage := llm.TokenUsage{InputTokens: 100, OutputTokens: 900, CacheReadTokens: 10, CacheWriteTokens: 1}
	if got, want := pressureFrom(usage), 111; got != want {
		t.Fatalf("提示词侧压力该是 %d，实际 %d", want, got)
	}
}

// 容量和压力是两个各自 last-wins 的槽，缺一个就少一个字段，不拿 0 顶。
func TestContextPressureKeepsMissingFieldsAbsent(t *testing.T) {
	t.Parallel()

	state := contextPressureState{}

	fresh := contextPressureViewOf(state).(ContextPressureView)
	if fresh.PressureTokens != nil || fresh.ProjectedTokens != nil || fresh.ContextWindow != nil {
		t.Fatalf("什么都没发生过时三个字段都该缺席：%+v", fresh)
	}

	// 只公告容量：压力和投影仍然缺席。
	state, changed := applyContextPressure(state, contextEvent(t, 8192))
	if !changed {
		t.Fatal("公告容量该报变化")
	}
	only := contextPressureViewOf(state).(ContextPressureView)
	if only.ContextWindow == nil || *only.ContextWindow != 8192 {
		t.Fatalf("容量该记下来：%+v", only.ContextWindow)
	}
	if only.PressureTokens != nil || only.ProjectedTokens != nil {
		t.Fatalf("还没有过采样时压力该缺席：%+v", only)
	}

	// 一条没公告容量的路由（0）读成缺席，而不是「容量是 0」。
	state, _ = applyContextPressure(state, contextEvent(t, 0))
	if got := contextPressureViewOf(state).(ContextPressureView); got.ContextWindow != nil {
		t.Fatalf("0 该读成「这条路由没公告容量」：%+v", got.ContextWindow)
	}
}

// 投影值回答的是**下一次**请求：它等于「那次采样 + 采样之后表面的带符号位移」，
// 所以一次压缩能让它当场掉下来——那件事压力自己看不见，因为压缩不产生用量。
func TestContextPressureProjectsForwardAndReactsToCompaction(t *testing.T) {
	t.Parallel()

	view := newSession(
		userEvent(t, "aaaaaaaaaaaaaaaa"),
		assistantEvent(t, 0, 0, "bbbbbbbbbbbbbbbb", &llm.TokenUsage{InputTokens: 500, OutputTokens: 20}),
	)

	state := contextPressureState{}
	for _, event := range view.events {
		state, _ = applyContextPressure(state, event)
	}

	stamped := contextPressureViewOf(state).(ContextPressureView)
	if stamped.PressureTokens == nil || *stamped.PressureTokens != 500 {
		t.Fatalf("压力该锚在提供方那边：%+v", stamped.PressureTokens)
	}
	// 盖章在这条助手消息**进表面之前**，所以它锚的正是自己那次请求看见的表面；
	// 投影值于是已经把这条助手消息算进去了——它回答的是下一次请求。
	assistantTokens, err := EstimateMessage(textMessage("a", llm.RoleAssistant, llm.ModelSource{}, "bbbbbbbbbbbbbbbb"))
	if err != nil {
		t.Fatalf("估价不该失败：%v", err)
	}
	if stamped.ProjectedTokens == nil || *stamped.ProjectedTokens != 500+assistantTokens {
		t.Fatalf("投影该是压力加上采样之后进表面的那条助手消息：想要 %d，实际 %+v",
			500+assistantTokens, stamped.ProjectedTokens)
	}

	// 再追一条用户消息：投影往前走，压力不动。
	next := userEvent(t, "cccc")
	next.Seq = 2
	state, _ = applyContextPressure(state, next)
	grown := contextPressureViewOf(state).(ContextPressureView)
	if *grown.PressureTokens != 500 {
		t.Fatalf("没有新的用量上报时压力不该动：%d", *grown.PressureTokens)
	}
	if *grown.ProjectedTokens <= *stamped.ProjectedTokens {
		t.Fatalf("表面长了投影就该跟着长：之前 %d，之后 %d",
			*stamped.ProjectedTokens, *grown.ProjectedTokens)
	}

	// 一次带影子价的压缩：投影当场掉回去，压力还是不动。
	var nodes []SurfaceNode
	for _, event := range append(append([]session.Event{}, view.events...), next) {
		if !session.IsSurfaceEvent(event) {
			continue
		}
		fold, err := foldSurfaceTokens(nodes, event)
		if err != nil {
			t.Fatalf("折不进来：%v", err)
		}
		nodes = fold.nodes
	}
	shadowed := nodes[0].Tokens + nodes[1].Tokens

	summary := summaryEvent(t, 0, 1, shadowed)
	summary.Seq = 3
	replacement := replacementEvent(t, 0, 1, "s")
	replacement.Seq = 4

	state, _ = applyContextPressure(state, summary)
	state, _ = applyContextPressure(state, replacement)

	compacted := contextPressureViewOf(state).(ContextPressureView)
	if *compacted.PressureTokens != 500 {
		t.Fatalf("压缩不产生用量，压力不该动：%d", *compacted.PressureTokens)
	}
	if *compacted.ProjectedTokens >= *grown.ProjectedTokens {
		t.Fatalf("压缩之后投影该掉下来：压缩前 %d，压缩后 %d",
			*grown.ProjectedTokens, *compacted.ProjectedTokens)
	}
}

// 投影值钳在 0：影子价比新摘要贵得多的时候，位移是个很负的数。
func TestContextPressureProjectionClampsAtZero(t *testing.T) {
	t.Parallel()

	pressure, sampled := 10, 1000
	state := contextPressureState{
		PressureTokens:       &pressure,
		SampledSurfaceTokens: &sampled,
		SurfaceTokens:        0,
	}
	got := contextPressureViewOf(state).(ContextPressureView)
	if got.ProjectedTokens == nil || *got.ProjectedTokens != 0 {
		t.Fatalf("投影值该钳在 0：%+v", got.ProjectedTokens)
	}
}

// 组成那个单元的信封两个数按请求头 last-wins，消息那个数骑在表面折叠上。
func TestContextBreakdownTracksHeaderAndSurfaceSeparately(t *testing.T) {
	t.Parallel()

	state := contextBreakdownState{}

	state, changed := applyContextBreakdown(state, headerEvent(t, simpleHeader("you are helpful")))
	if !changed {
		t.Fatal("第一份请求头该报变化")
	}
	if state.SystemTokens == 0 {
		t.Fatal("系统提示该被估价")
	}
	if state.MessageTokens != 0 {
		t.Fatalf("请求头不该动消息那个数：%d", state.MessageTokens)
	}

	first := state.SystemTokens
	appended := userEvent(t, "hello")
	appended.Seq = 1
	state, changed = applyContextBreakdown(state, appended)
	if !changed {
		t.Fatal("表面长了该报变化")
	}
	if state.SystemTokens != first {
		t.Fatalf("一条用户消息不该动系统提示那个数：%d", state.SystemTokens)
	}
	if state.MessageTokens == 0 {
		t.Fatal("消息那个数该跟着表面走")
	}

	// 换一份更长的系统提示：last-wins。
	state, _ = applyContextBreakdown(state, headerEvent(t, simpleHeader("you are a very helpful assistant indeed")))
	if state.SystemTokens <= first {
		t.Fatalf("换了更长的系统提示该重新估价：之前 %d，之后 %d", first, state.SystemTokens)
	}
}

// 组成那三个数**加起来不等于**占用那个投影值——这是有意的，
// 一个是启发式的比例，一个锚在提供方那边。这条钉的是「别把它们当同一个总量」。
func TestContextBreakdownViewPublishesOnlyTheThreeNumbers(t *testing.T) {
	t.Parallel()

	registry := projection.NewRegistry()
	dispose, err := RegisterProjections(registry)
	if err != nil {
		t.Fatalf("登记不该失败：%v", err)
	}
	defer dispose()

	view := newSession(
		headerEvent(t, simpleHeader("you are helpful")),
		userEvent(t, "hello"),
	)

	breakdown, ok := viewOf(t, registry, view, ContextBreakdownProjectionKey).(ContextBreakdownView)
	if !ok {
		t.Fatal("读出来的该是一份 ContextBreakdownView")
	}
	if breakdown.SystemTokens == 0 || breakdown.MessageTokens == 0 {
		t.Fatalf("两个数都该有值：%+v", breakdown)
	}
	if breakdown.ToolsTokens != 0 {
		t.Fatalf("这份头没带工具表，该是 0：%d", breakdown.ToolsTokens)
	}
}

// 读不回来的请求头保持原值，而不是把两个数归零——归零会让界面显示成
// 「这次请求没有系统提示、没带工具」，那比偏一点严重得多。
//
// （工具表排不出 JSON 那一支同样是保持原值，但它在真实日志上走不到：
// [session.EpochHeader] 自己的 MarshalJSON 会先失败，那样的事件根本写不出来。）
func TestContextBreakdownKeepsThePreviousFiguresOnAnUnreadableHeader(t *testing.T) {
	t.Parallel()

	good := session.EpochHeader{
		System: "you are helpful",
		Tools:  []llm.ToolSchema{{Name: "ls", Parameters: []byte(`{"type":"object"}`)}},
	}
	state, _ := applyContextBreakdown(contextBreakdownState{}, headerEvent(t, good))
	if state.ToolsTokens == 0 || state.SystemTokens == 0 {
		t.Fatalf("一份好的请求头该被估价：%+v", state)
	}
	before := state

	broken := session.Event{Seq: 1, Type: session.EventRequestHeader, Data: []byte(`{"header":42}`)}
	after, changed := applyContextBreakdown(state, broken)
	if changed {
		t.Fatal("读不回来的请求头不该报变化")
	}
	if after.SystemTokens != before.SystemTokens || after.ToolsTokens != before.ToolsTokens {
		t.Fatalf("读不动的时候该保持原值：之前 %+v，之后 %+v", before, after)
	}
}

// 三个单元的状态都要能原样进出 JSON——那是检查点能落盘的前提。
func TestProjectionStatesSurviveACheckpointRoundTrip(t *testing.T) {
	t.Parallel()

	registry := projection.NewRegistry()
	dispose, err := RegisterProjections(registry)
	if err != nil {
		t.Fatalf("登记不该失败：%v", err)
	}
	defer dispose()

	view := newSession(
		headerEvent(t, simpleHeader("you are helpful")),
		userEvent(t, "hello"),
		assistantEvent(t, 0, 0, "hi", &llm.TokenUsage{InputTokens: 30, OutputTokens: 4}),
	)

	checkpoint, err := registry.Checkpoint(view)
	if err != nil {
		t.Fatalf("落检查点不该失败：%v", err)
	}
	for _, key := range []string{TokenUsageProjectionKey, ContextPressureProjectionKey, ContextBreakdownProjectionKey} {
		if _, ok := checkpoint[key]; !ok {
			t.Fatalf("单元 %q 该出现在检查点里", key)
		}
	}
}
