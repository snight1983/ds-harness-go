// 本文件的作用：钉住计量服务那三条基准的挑法、锚点什么时候作废、
// 以及重放在遇到一条坏事件时的姿势。

package tokenmeter

import (
	"testing"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// assistantEventFrom 造一条引用了那几条分块的助手消息。
func assistantEventFrom(t *testing.T, turn, step int, text string, usage *llm.TokenUsage, sources []int) sessionlog.Event {
	t.Helper()

	event := assistantEvent(t, turn, step, text, usage)
	event.SourceEventSeqs = sources
	return event
}

// measure 量一次，量不出来就判测试失败。
func measure(t *testing.T, meter *TokenMeter, view *fakeSession, header *sessionlog.EpochHeader) Measurement {
	t.Helper()

	got, err := meter.Measure(view, header)
	if err != nil {
		t.Fatalf("计量不该失败：%v", err)
	}
	return got
}

// mustEstimateMessage 估一条消息的价，估不出来就判测试失败。
func mustEstimateMessage(t *testing.T, message llm.Message) int {
	t.Helper()

	got, err := EstimateMessage(message)
	if err != nil {
		t.Fatalf("估价不该失败：%v", err)
	}
	return got
}

// anchoredSession 造一份走完整套协议的日志：请求头、一条用户消息、一个步骤，
// 步骤里有三条分块和一条引用它们的助手消息。
func anchoredSession(t *testing.T, usage *llm.TokenUsage) *fakeSession {
	t.Helper()

	events := []sessionlog.Event{
		headerEvent(t, simpleHeader("you are helpful")),
		userEvent(t, "hello world"),
		stepStartEvent(t, 0, 0),
	}
	events = append(events, textChunks(t, 0, 0, "hi there")...)
	events = append(events,
		assistantEventFrom(t, 0, 0, "hi there", usage, []int{3, 4, 5}),
		stepEndEvent(t, 0, 0),
	)
	return newSession(events...)
}

func TestMeasureOfAnEmptyLogHasNoBaseline(t *testing.T) {
	t.Parallel()

	got := measure(t, New(), newSession(), nil)

	if got.Baseline.Kind != BaselineNone {
		t.Fatalf("什么都没发生过时该没有基准可言：%q", got.Baseline.Kind)
	}
	if got.TotalTokens != 0 || got.SurfaceTokens != 0 || got.LogRevision != 0 {
		t.Fatalf("空日志该处处是 0：%+v", got)
	}
	if len(got.Nodes) != 0 {
		t.Fatalf("空日志不该有节点：%v", got.Nodes)
	}
}

// 没有锚（还没有过任何一条落定的助手消息）就整份重新估价，位移归零。
func TestMeasureWithoutAnAnchorEstimatesTheWholeThing(t *testing.T) {
	t.Parallel()

	header := simpleHeader("you are helpful")
	view := newSession(headerEvent(t, header), userEvent(t, "hello world"))
	got := measure(t, New(), view, nil)

	headerTokens, err := EstimateHeader(header)
	if err != nil {
		t.Fatalf("头估价不该失败：%v", err)
	}
	userTokens := mustEstimateMessage(t, textMessage("u", llm.RoleUser, llm.UserSource{}, "hello world"))

	if got.Baseline.Kind != BaselineEstimated {
		t.Fatalf("没有锚就该整份估：%q", got.Baseline.Kind)
	}
	if want := headerTokens + userTokens; got.Baseline.Tokens != want {
		t.Fatalf("基准该是头价加表面价：想要 %d，实际 %d", want, got.Baseline.Tokens)
	}
	if got.SurfaceDeltaTokens != 0 {
		t.Fatalf("整份重估的时候位移该归零：%d", got.SurfaceDeltaTokens)
	}
	if got.TotalTokens != got.Baseline.Tokens {
		t.Fatalf("位移是 0 的时候总量该等于基准：%+v", got)
	}
}

// 一条带用量的助手消息立起锚，后面的测量就锚在提供方那个数上，
// 启发式只负责量锚之后的那段位移。
func TestMeasureAnchorsOnProviderUsageAndPricesOnlyTheDelta(t *testing.T) {
	t.Parallel()

	usage := llm.TokenUsage{InputTokens: 100, OutputTokens: 20, CacheReadTokens: 4, CacheWriteTokens: 1}
	view := anchoredSession(t, &usage)
	meter := New()

	anchored := measure(t, meter, view, nil)
	if anchored.Baseline.Kind != BaselineUsage {
		t.Fatalf("该锚在提供方报的用量上：%q", anchored.Baseline.Kind)
	}
	if want := usageTokens(usage); anchored.Baseline.Tokens != want {
		t.Fatalf("基准该是那次用量的四个桶之和：想要 %d，实际 %d", want, anchored.Baseline.Tokens)
	}
	if anchored.Baseline.Usage != usage {
		t.Fatalf("基准该把那次用量原样带着：%+v", anchored.Baseline.Usage)
	}
	if anchored.SurfaceDeltaTokens != 0 {
		t.Fatalf("刚立完锚时位移该是 0：%d", anchored.SurfaceDeltaTokens)
	}
	if anchored.TotalTokens != usageTokens(usage) {
		t.Fatalf("总量该等于那次用量：%+v", anchored)
	}

	// 再追一条用户消息：基准不动，位移正好是那条消息的估价。
	view.append(userEvent(t, "another question"))
	grown := measure(t, meter, view, nil)

	if grown.Baseline != anchored.Baseline {
		t.Fatalf("追一条消息不该动基准：%+v", grown.Baseline)
	}
	want := mustEstimateMessage(t, textMessage("u", llm.RoleUser, llm.UserSource{}, "another question"))
	if grown.SurfaceDeltaTokens != want {
		t.Fatalf("位移该正好是新那条消息的估价：想要 %d，实际 %d", want, grown.SurfaceDeltaTokens)
	}
	if grown.TotalTokens != anchored.TotalTokens+want {
		t.Fatalf("总量该是基准加位移：%+v", grown)
	}
}

// 提供方报的数比同口径的全量估价还小的时候，宁可整份用估价：
// 从一个偏低的锚上再一刀刀减下去只会越减越离谱。
func TestMeasureFallsBackToEstimateWhenProviderUsageIsSmallerThanTheHeuristic(t *testing.T) {
	t.Parallel()

	usage := llm.TokenUsage{InputTokens: 1, OutputTokens: 1}
	view := anchoredSession(t, &usage)
	got := measure(t, New(), view, nil)

	if got.Baseline.Kind != BaselineEstimated {
		t.Fatalf("提供方那个数太小的时候该退回估价：%q", got.Baseline.Kind)
	}
	if got.Baseline.Tokens <= usageTokens(usage) {
		t.Fatalf("退回来的估价该比那个偏小的用量大：%+v", got.Baseline)
	}
}

// 没有用量的助手消息照样立锚，只是基准是估出来的——立了它，
// 后面的位移逻辑就不用分叉。
func TestMeasureStillAnchorsWhenTheProviderReportedNoUsage(t *testing.T) {
	t.Parallel()

	view := anchoredSession(t, nil)
	meter := New()
	anchored := measure(t, meter, view, nil)

	if anchored.Baseline.Kind != BaselineEstimated {
		t.Fatalf("没有用量的锚该是估出来的：%q", anchored.Baseline.Kind)
	}

	view.append(userEvent(t, "more"))
	grown := measure(t, meter, view, nil)
	if grown.Baseline != anchored.Baseline {
		t.Fatalf("锚立住了就不该被后面的消息改动：%+v", grown.Baseline)
	}
	if grown.SurfaceDeltaTokens <= 0 {
		t.Fatalf("锚立住之后该按位移走：%d", grown.SurfaceDeltaTokens)
	}
}

// 换了请求头（换模型、改系统提示）就不能再拿旧锚当基准——
// 那等于把 A 请求的绝对值配上 B 请求的增量。
func TestMeasureDropsTheAnchorWhenTheHeaderChanges(t *testing.T) {
	t.Parallel()

	usage := llm.TokenUsage{InputTokens: 100, OutputTokens: 20}
	view := anchoredSession(t, &usage)
	meter := New()

	if got := measure(t, meter, view, nil); got.Baseline.Kind != BaselineUsage {
		t.Fatalf("先该锚在用量上：%q", got.Baseline.Kind)
	}

	other := simpleHeader("you are a completely different assistant")
	got := measure(t, meter, view, &other)
	if got.Baseline.Kind != BaselineEstimated {
		t.Fatalf("换了头就该整份重估：%q", got.Baseline.Kind)
	}
	if got.SurfaceDeltaTokens != 0 {
		t.Fatalf("整份重估的时候位移该归零：%d", got.SurfaceDeltaTokens)
	}

	// 传回同一份头，锚该重新对上。
	same := simpleHeader("you are helpful")
	if back := measure(t, meter, view, &same); back.Baseline.Kind != BaselineUsage {
		t.Fatalf("头一样锚就该还能用：%q", back.Baseline.Kind)
	}
}

// 「没有头」和「一份零值的头」在锚点比对上是两件事。
func TestOptionalHeaderEqualsSeparatesAbsentFromEmpty(t *testing.T) {
	t.Parallel()

	empty := sessionlog.EpochHeader{}
	if !optionalHeaderEquals(empty, false, empty, false) {
		t.Fatal("都没有该算相等")
	}
	if optionalHeaderEquals(empty, true, empty, false) {
		t.Fatal("一边有一边没有该算不等")
	}
	if !optionalHeaderEquals(empty, true, empty, true) {
		t.Fatal("都是同一份空头该算相等")
	}
	if optionalHeaderEquals(simpleHeader("a"), true, simpleHeader("b"), true) {
		t.Fatal("内容不同的两份头该算不等")
	}
}

// 一次压缩把一大段表面换成一小段摘要，位移是负的；总量钳在 0。
func TestMeasureGoesDownAfterCompactionAndClampsAtZero(t *testing.T) {
	t.Parallel()

	usage := llm.TokenUsage{InputTokens: 100, OutputTokens: 20}
	view := anchoredSession(t, &usage)
	meter := New()

	before := measure(t, meter, view, nil)
	view.append(replacementEvent(t, 1, 6, "s"))
	after := measure(t, meter, view, nil)

	if after.SurfaceDeltaTokens >= 0 {
		t.Fatalf("把一大段换成一小段该让位移变负：%d", after.SurfaceDeltaTokens)
	}
	if after.TotalTokens >= before.TotalTokens {
		t.Fatalf("压缩之后总量该掉下来：之前 %d，之后 %d", before.TotalTokens, after.TotalTokens)
	}
	if after.SurfaceTokens >= before.SurfaceTokens {
		t.Fatalf("压缩之后表面价该掉下来：之前 %d，之后 %d", before.SurfaceTokens, after.SurfaceTokens)
	}
	// 钳位在一份合法的日志上走不到，但要看住这条边界：保守规则让锚不小于
	// 「头价加立锚那一刻的表面」，而位移最多把表面减回 0，所以总量下不了 0。
	if after.TotalTokens < 0 {
		t.Fatalf("总量不许是负的：%d", after.TotalTokens)
	}
	if want := after.Baseline.Tokens + after.SurfaceDeltaTokens; after.TotalTokens != want {
		t.Fatalf("没到钳位的时候总量该是基准加位移：想要 %d，实际 %d", want, after.TotalTokens)
	}
}

// 节点表要和当前表面一一对上——压缩那边挑下刀点全靠它。
func TestMeasureNodesMatchTheCurrentSurface(t *testing.T) {
	t.Parallel()

	view := anchoredSession(t, nil)
	view.append(userEvent(t, "again"))
	got := measure(t, New(), view, nil)

	var wantSeqs []int
	total := 0
	for _, event := range view.events {
		if sessionlog.IsSurfaceEvent(event) {
			wantSeqs = append(wantSeqs, event.Seq)
		}
	}
	if len(got.Nodes) != len(wantSeqs) {
		t.Fatalf("节点数该等于表面上的事件数：想要 %d，实际 %d", len(wantSeqs), len(got.Nodes))
	}
	for index, node := range got.Nodes {
		if node.Seq != wantSeqs[index] {
			t.Fatalf("第 %d 个节点的 seq 不对：想要 %d，实际 %d", index, wantSeqs[index], node.Seq)
		}
		total += node.Tokens
	}
	if total != got.SurfaceTokens {
		t.Fatalf("表面价该等于逐节点之和：想要 %d，实际 %d", total, got.SurfaceTokens)
	}
}

// 交出去的节点表是复制品：调用方改它不该反过来把计量器的账改坏。
func TestMeasureReturnsACopyOfTheNodes(t *testing.T) {
	t.Parallel()

	view := anchoredSession(t, nil)
	meter := New()

	first := measure(t, meter, view, nil)
	if len(first.Nodes) == 0 {
		t.Fatal("这局该有节点")
	}
	first.Nodes[0].Tokens = 999999

	second := measure(t, meter, view, nil)
	if second.Nodes[0].Tokens == 999999 {
		t.Fatal("调用方改到了计量器自己那张节点表")
	}
	if second.SurfaceTokens != first.SurfaceTokens {
		t.Fatalf("表面价不该被外面改动：%d 和 %d", first.SurfaceTokens, second.SurfaceTokens)
	}
}

// 日志水位跟着事件走，而且重复量同一份日志不会重复折。
func TestMeasureIsIncrementalAcrossCalls(t *testing.T) {
	t.Parallel()

	view := anchoredSession(t, nil)
	meter := New()

	first := measure(t, meter, view, nil)
	if first.LogRevision != len(view.events) {
		t.Fatalf("水位该等于已折的事件条数：想要 %d，实际 %d", len(view.events), first.LogRevision)
	}

	again := measure(t, meter, view, nil)
	if again.SurfaceTokens != first.SurfaceTokens || again.LogRevision != first.LogRevision {
		t.Fatalf("同一份日志重复量该给出同一个答案：%+v 和 %+v", first, again)
	}

	view.append(userEvent(t, "more"))
	grown := measure(t, meter, view, nil)
	if grown.LogRevision != len(view.events) {
		t.Fatalf("追了事件水位该往上走：想要 %d，实际 %d", len(view.events), grown.LogRevision)
	}
}

// 丢掉缓存之后从头重放，答案一模一样——这条同时钉住「重放是纯的」。
func TestForgetMakesTheNextMeasureReplayFromScratch(t *testing.T) {
	t.Parallel()

	usage := llm.TokenUsage{InputTokens: 100, OutputTokens: 20}
	view := anchoredSession(t, &usage)
	meter := New()

	before := measure(t, meter, view, nil)
	meter.Forget(view.ID())
	after := measure(t, meter, view, nil)

	if before.Baseline != after.Baseline || before.SurfaceTokens != after.SurfaceTokens ||
		before.TotalTokens != after.TotalTokens || before.LogRevision != after.LogRevision {
		t.Fatalf("从头重放该给出同一份结果：%+v 和 %+v", before, after)
	}
}

// 缓存按会话 ID 归档，所以「同一个 ID 换了一份更短的日志」要从头重放，
// 不能拿着一个比日志还高的水位继续往下折。
func TestMeasureReplaysFromScratchWhenTheLogGotShorter(t *testing.T) {
	t.Parallel()

	meter := New()
	long := anchoredSession(t, nil)
	if got := measure(t, meter, long, nil); got.LogRevision != len(long.events) {
		t.Fatalf("先量一份长的：%+v", got)
	}

	short := newSession(userEvent(t, "hello"))
	got := measure(t, meter, short, nil)
	if got.LogRevision != 1 {
		t.Fatalf("短日志该从头重放：想要 1，实际 %d", got.LogRevision)
	}
	if len(got.Nodes) != 1 || got.Nodes[0].Seq != 0 {
		t.Fatalf("节点表该是短日志自己那份：%v", got.Nodes)
	}
}

// 只看长度发现不了起点已经变过：弹掉三条又追加四条，日志反而是长的，
// 而每一个下标指向的都已经是另一条事件了。
func TestMeasureReplaysFromScratchWhenTheLogStartMoved(t *testing.T) {
	t.Parallel()

	meter := New()
	before := newSession(userEvent(t, "一"), userEvent(t, "二"), userEvent(t, "三"))
	if got := measure(t, meter, before, nil); got.LogRevision != 3 {
		t.Fatalf("先量一份从 0 起的：%+v", got)
	}

	after := trimmedSession(3,
		userEvent(t, "四"), userEvent(t, "五"), userEvent(t, "六"), userEvent(t, "七"))
	got := measure(t, meter, after, nil)
	if got.LogRevision != 4 {
		t.Fatalf("起点变了该从头重放：想要 4，实际 %d", got.LogRevision)
	}
	if len(got.Nodes) != 4 || got.Nodes[0].Seq != 3 {
		t.Fatalf("节点表该是弹过头之后那份自己的：%v", got.Nodes)
	}
}

// 一条 seq 不是自己下标的来源：上游直接 `events[seq]` 在这里会取到隔壁那条上去，
// 于是这一步的账被悄悄算成别人的。
func TestProviderAssistantResolvesItsSourcesAgainstTheLogStart(t *testing.T) {
	t.Parallel()

	const base = 40
	events := []sessionlog.Event{
		headerEvent(t, simpleHeader("you are helpful")),
		stepStartEvent(t, 0, 0),
	}
	events = append(events, textChunks(t, 0, 0, "a very long streamed answer indeed")...)
	events = append(events, assistantEventFrom(t, 0, 0, "cut",
		&llm.TokenUsage{InputTokens: 1}, []int{base + 2, base + 3, base + 4}))
	view := trimmedSession(base, events...)

	got := measure(t, New(), view, nil)

	streamed := mustEstimateMessage(t, textMessage("a", llm.RoleAssistant, llm.ModelSource{}, "a very long streamed answer indeed"))
	headerTokens, err := EstimateHeader(simpleHeader("you are helpful"))
	if err != nil {
		t.Fatalf("头估价不该失败：%v", err)
	}
	if want := headerTokens + streamed; got.Baseline.Tokens != want {
		t.Fatalf("锚该按流里那份长的算：想要 %d，实际 %d", want, got.Baseline.Tokens)
	}
}

// 提供方看见的是它自己那趟流产出的内容，不是后来落进日志的那条消息。
// 所以引了来源分块的助手消息按分块重新装配一遍定价。
func TestProviderAssistantIsPricedFromTheSourceChunks(t *testing.T) {
	t.Parallel()

	// 落进日志的那条消息被截短过，而流里产出的是长的那份。
	events := []sessionlog.Event{
		headerEvent(t, simpleHeader("you are helpful")),
		stepStartEvent(t, 0, 0),
	}
	events = append(events, textChunks(t, 0, 0, "a very long streamed answer indeed")...)
	// 必须带一份用量：只有那一支才会去按来源分块重新装配。给一份小的，
	// 好让保守规则挑中估价那一支，锚里那段流的价钱才读得出来。
	events = append(events, assistantEventFrom(t, 0, 0, "cut", &llm.TokenUsage{InputTokens: 1}, []int{2, 3, 4}))
	view := newSession(events...)

	got := measure(t, New(), view, nil)

	streamed := mustEstimateMessage(t, textMessage("a", llm.RoleAssistant, llm.ModelSource{}, "a very long streamed answer indeed"))
	headerTokens, err := EstimateHeader(simpleHeader("you are helpful"))
	if err != nil {
		t.Fatalf("头估价不该失败：%v", err)
	}
	if want := headerTokens + streamed; got.Baseline.Tokens != want {
		t.Fatalf("锚该按流里那份长的算：想要 %d，实际 %d", want, got.Baseline.Tokens)
	}
	// 表面上留下的却是被截短的那条，所以表面价比锚里那一段小。
	if got.SurfaceTokens >= streamed {
		t.Fatalf("表面留的是截短那条，该比流里那份便宜：表面 %d，流 %d", got.SurfaceTokens, streamed)
	}
}

// 没有来源清单的助手消息（比如修复补上的那条）退回按落进日志的那条定价。
func TestProviderAssistantFallsBackToTheDurableMessageWithoutSources(t *testing.T) {
	t.Parallel()

	view := newSession(
		headerEvent(t, simpleHeader("you are helpful")),
		stepStartEvent(t, 0, 0),
		assistantEvent(t, 0, 0, "repaired", &llm.TokenUsage{InputTokens: 1}),
	)
	got := measure(t, New(), view, nil)

	durable := mustEstimateMessage(t, textMessage("a", llm.RoleAssistant, llm.ModelSource{}, "repaired"))
	headerTokens, err := EstimateHeader(simpleHeader("you are helpful"))
	if err != nil {
		t.Fatalf("头估价不该失败：%v", err)
	}
	if want := headerTokens + durable; got.Baseline.Tokens != want {
		t.Fatalf("该按落进日志那条算：想要 %d，实际 %d", want, got.Baseline.Tokens)
	}
}

// 一份**明确为空**的来源清单说的是「这趟流一个字都没产出」，价是 0，
// 连角色开销都不加——它和「没有来源清单」不是一回事。
func TestProviderAssistantWithAnEmptySourceListPricesZero(t *testing.T) {
	t.Parallel()

	view := newSession(
		headerEvent(t, simpleHeader("you are helpful")),
		stepStartEvent(t, 0, 0),
		assistantEventFrom(t, 0, 0, "", &llm.TokenUsage{InputTokens: 1}, []int{}),
	)
	got := measure(t, New(), view, nil)

	headerTokens, err := EstimateHeader(simpleHeader("you are helpful"))
	if err != nil {
		t.Fatalf("头估价不该失败：%v", err)
	}
	if got.Baseline.Tokens != headerTokens {
		t.Fatalf("空的来源清单该定价成 0：想要 %d，实际 %d", headerTokens, got.Baseline.Tokens)
	}
}

func TestReplayRejectsMalformedLogs(t *testing.T) {
	t.Parallel()

	cases := map[string]*fakeSession{
		"步骤没关就又开一个": newSession(
			stepStartEvent(t, 0, 0),
			stepStartEvent(t, 0, 1),
		),
		"step/end 配不上任何 step/start": newSession(
			stepStartEvent(t, 0, 0),
			stepEndEvent(t, 0, 1),
		),
		"没开过步骤就 step/end": newSession(stepEndEvent(t, 0, 0)),
		"助手消息配不上任何 step/start": newSession(
			assistantEvent(t, 0, 0, "hi", nil),
		),
		"助手消息属于另一个步骤": newSession(
			stepStartEvent(t, 0, 0),
			assistantEvent(t, 0, 1, "hi", nil),
		),
		// 下面这几条都要带请求头和用量：只有那一支才会去按来源分块重新装配。
		"引的来源不比自己早": newSession(
			headerEvent(t, simpleHeader("you are helpful")),
			stepStartEvent(t, 0, 0),
			assistantEventFrom(t, 0, 0, "hi", &llm.TokenUsage{InputTokens: 1}, []int{5}),
		),
		"引的来源是个负数": newSession(
			headerEvent(t, simpleHeader("you are helpful")),
			stepStartEvent(t, 0, 0),
			assistantEventFrom(t, 0, 0, "hi", &llm.TokenUsage{InputTokens: 1}, []int{-1}),
		),
		"引的来源重复了": newSession(
			headerEvent(t, simpleHeader("you are helpful")),
			stepStartEvent(t, 0, 0),
			chunkEvent(t, 0, 0, llm.TextDeltaChunk{Index: 0, Text: "hi"}),
			assistantEventFrom(t, 0, 0, "hi", &llm.TokenUsage{InputTokens: 1}, []int{2, 2}),
		),
		"引的来源不是 assistant/chunk": newSession(
			headerEvent(t, simpleHeader("you are helpful")),
			stepStartEvent(t, 0, 0),
			userEvent(t, "hello"),
			assistantEventFrom(t, 0, 0, "hi", &llm.TokenUsage{InputTokens: 1}, []int{2}),
		),
		"引的来源属于另一个步骤": newSession(
			headerEvent(t, simpleHeader("you are helpful")),
			stepStartEvent(t, 0, 0),
			chunkEvent(t, 0, 1, llm.TextDeltaChunk{Index: 0, Text: "hi"}),
			assistantEventFrom(t, 0, 0, "hi", &llm.TokenUsage{InputTokens: 1}, []int{2}),
		),
		"请求头读不回来": {id: "s", events: []sessionlog.Event{
			{Seq: 0, Type: sessionlog.EventRequestHeader, Data: []byte(`{"header":42}`)},
		}},
	}

	for name, view := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := New().Measure(view, nil); err == nil {
				t.Fatal("这份日志该让重放失败")
			}
		})
	}
}

// 折到一半失败的时候水位停在出错那条上：那条事件保持「没被读进来」，
// 而不是被跳过去让整份账悄悄少一段。
func TestReplayLeavesTheBadEventUnconsumed(t *testing.T) {
	t.Parallel()

	view := newSession(
		userEvent(t, "hello"),
		stepEndEvent(t, 0, 0), // 没开过步骤，这条会让重放失败。
	)
	meter := New()

	if _, err := meter.Measure(view, nil); err == nil {
		t.Fatal("这份日志该让重放失败")
	}
	// 第二次问还是同一个错，说明那条坏事件没被跳过去。
	if _, err := meter.Measure(view, nil); err == nil {
		t.Fatal("坏事件该还在原地，第二次问该还是失败")
	}
}

func TestEstimateMessageMethodMatchesThePackageFunction(t *testing.T) {
	t.Parallel()

	message := textMessage("m", llm.RoleUser, llm.UserSource{}, "hello world")
	fromMethod, err := New().EstimateMessage(message)
	if err != nil {
		t.Fatalf("估价不该失败：%v", err)
	}
	if want := mustEstimateMessage(t, message); fromMethod != want {
		t.Fatalf("方法该和包级函数同价：想要 %d，实际 %d", want, fromMethod)
	}
}

func TestUsageTokensSumsAllFourBuckets(t *testing.T) {
	t.Parallel()

	usage := llm.TokenUsage{
		InputTokens: 1, OutputTokens: 2, CacheReadTokens: 4, CacheWriteTokens: 8,
		// 推理已经含在输出里，不该再加一笔。
		ReasoningTokens: 1000,
	}
	if got, want := usageTokens(usage), 15; got != want {
		t.Fatalf("该是四个桶之和：想要 %d，实际 %d", want, got)
	}
}
