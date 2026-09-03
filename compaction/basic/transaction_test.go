// 本文件的作用：验那一次真的会改日志的压缩事务——括号怎么开、怎么合、
// 失败之后留下什么，以及一份过期的摘要在什么时候会被拦下来。

package basic

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/compaction"
	"github.com/snight1983/ds-harness-go/core/scope"
	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
)

// # 这些测试防的是什么错
//
//   - 括号漏了一半。一次没有 compaction/end 的 start 会把这段会话的压缩锁永远
//     占着，之后每一次压缩都报 busy；而**做砸了的那次尝试留在日志里看得见**
//     正是这一层的约定，所以「失败也要合上括号」和「合不上就别回滚」是两件事。
//   - 摘要事件和那条替换消息中间插进了别的东西。一条 replace 的价格由紧挨在它
//     前面的那条计价事件给出，插一条就等于这次替换的影子价格再也查不到。
//   - 稳定性口径落错档。选中段那一档比整表面松，默默走松的那一档会让一份
//     建立在旧表面上的摘要换掉一段已经变了的历史——**不报错**。
//   - 归属排反了：一次独立的人工压缩排成属于某个回合，或者一次自动压缩排成
//     独立事务。那条括号于是指向另一个位置，而日志本身读得回来。
//   - 人工那一侧的失败分类串了档。busy / changed / summary / commit / persistence
//     会原样进人工命令的结果，上层照着写提示语。
//   - 一份不比原文小的摘要被放行：真历史换掉了，上下文却一点没省下来。

// meteredSurface 是一台照着当前表面现算的假计量器。
//
// 每次都重新读一遍 live.SurfaceNodes()，所以「总结期间表面变了」这件事在它这里
// 自然就体现出来了，不用另外排脚本。
type meteredSurface struct {
	// tokens 是逐个 seq 的定价；没排到的用 fallback。
	tokens map[int]int
	// fallback 是没单独定价的节点的价。
	fallback int
	// priceErr 非 nil 时定价这一步当场失败。
	priceErr error
	// estimate 是裹好那条检查点消息的估价。
	estimate int
	// estimateErr 非 nil 时估价这一步当场失败。
	estimateErr error
	// before 在每次定价**之前**跑一遍，用来模拟「总结期间会话被动了」。
	// 它只跑一次，跑完置空。
	before func()
}

func (m *meteredSurface) PriceSurface(live *coresession.Session) ([]PricedNode, error) {
	if m.before != nil {
		run := m.before
		m.before = nil
		run()
	}
	if m.priceErr != nil {
		return nil, m.priceErr
	}
	nodes := live.SurfaceNodes()
	priced := make([]PricedNode, 0, len(nodes))
	for _, seq := range nodes {
		tokens := m.fallback
		if fixed, ok := m.tokens[seq]; ok {
			tokens = fixed
		}
		priced = append(priced, PricedNode{Seq: seq, Tokens: tokens})
	}
	return priced, nil
}

func (m *meteredSurface) EstimateMessage(llm.Message) (int, error) {
	return m.estimate, m.estimateErr
}

// staticSummary 排一台总是交出同一份摘要的假总结。
func staticSummary(text string) Summarize {
	return func(context.Context, SummarizationInput, compaction.AgentContext) (SummaryResult, error) {
		return SummaryResult{
			Summary:  llm.Content{llm.TextBlock{Text: text}},
			Provider: "openai",
			Model:    "gpt-x",
		}, nil
	}
}

// failingSummary 排一台总是失败的假总结。
func failingSummary(err error) Summarize {
	return func(context.Context, SummarizationInput, compaction.AgentContext) (SummaryResult, error) {
		return SummaryResult{}, err
	}
}

// logSession 是一段真会话，外加它上面那几条事件落定之后的 seq。
type logSession struct {
	live *coresession.Session
	seqs []int
}

// appendTo 往一段真会话上追加一条事件，交回它落定之后的 seq。
func appendTo(t *testing.T, live *coresession.Session, event session.Event) int {
	t.Helper()

	committed, err := live.Append(event)
	if err != nil {
		t.Fatalf("%s 写不进去：%v", event.Type, err)
	}
	return committed.Seq
}

// surfaceMessage 造一条会进表面的消息事件。
func surfaceMessage(t *testing.T, kind session.EventType, payload any) session.Event {
	t.Helper()

	return session.Event{
		Type:      kind,
		Data:      marshalPayload(t, payload),
		SurfaceOp: session.AppendOp{},
	}
}

// openLog 排一段「一个开着的回合 + 四条表面消息」的会话，这是本文件的地基。
//
// 表面上依次是：用户说话、助手说话、用户说话、助手说话。四条全都不带工具调用，
// 所以每一刀都是配平的——工具配对那一层归 region_test.go 验。
func openLog(t *testing.T, headers ...llm.CallConfig) logSession {
	t.Helper()

	live := liveSession(t, "s-tx", headers...)
	appendTo(t, live, session.Event{
		Type: session.EventTurnStart,
		Data: marshalPayload(t, session.TurnStartData{Turn: 1}),
	})

	seqs := make([]int, 0, 4)
	for index := range 4 {
		if index%2 == 0 {
			seqs = append(seqs, appendTo(t, live, surfaceMessage(t, session.EventUserMessage,
				session.UserMessageData{Message: llm.NewUserMessage(
					llm.Content{llm.TextBlock{Text: "第几句"}}, llm.UserSource{})})))
			continue
		}
		seqs = append(seqs, appendTo(t, live, surfaceMessage(t, session.EventAssistantMessage,
			session.AssistantMessageData{
				Turn: 1, Step: index,
				Message: llm.NewAssistantMessage(
					llm.Content{llm.TextBlock{Text: "好"}}, llm.Provenance{}),
			})))
	}
	return logSession{live: live, seqs: seqs}
}

// closeTurn 关掉那个开着的回合，把会话摆成「两个回合之间」。
func closeTurn(t *testing.T, live *coresession.Session) {
	t.Helper()

	appendTo(t, live, session.Event{
		Type: session.EventTurnEnd,
		Data: json.RawMessage(`{"turn":1,"reason":"done"}`),
	})
}

// autoOptions 是自动那一档的选项：跟着回合走、整表面口径。
func autoOptions() TransactionOptions {
	return TransactionOptions{
		Stability: StabilityWholeSurface,
		NewID:     func() string { return "c-fixed" },
	}
}

// manualOptions 是人工那一档的选项：独立事务、选中段口径。
func manualOptions() TransactionOptions {
	return TransactionOptions{
		Standalone: true,
		Stability:  StabilitySelectedSpan,
		NewID:      func() string { return "c-fixed" },
	}
}

// cheapDeps 排一套「被遮的两个节点各值 100，裹好的摘要只值 10」的依赖。
func cheapDeps() RegionDeps {
	return RegionDeps{
		Meter:     &meteredSurface{fallback: 100, estimate: 10},
		Summarize: staticSummary("一份摘要"),
	}
}

// eventsByType 数一段日志里某种事件有几条，并交出最后一条。
func eventsByType(events []session.Event, kind session.EventType) (int, session.Event) {
	count := 0
	var last session.Event
	for _, event := range events {
		if event.Type == kind {
			count++
			last = event
		}
	}
	return count, last
}

func TestCompactSurfaceRegion把四样东西按顺序落下去(t *testing.T) {
	t.Parallel()

	log := openLog(t)
	result, err := CompactSurfaceRegion(t.Context(), cheapDeps(), &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]},
		agentAt(log.live, "openai", "gpt-x"), autoOptions())
	if err != nil {
		t.Fatalf("这次压缩该成：%v", err)
	}

	events := log.live.Events()
	// start、summary、替换消息、end 必须**紧挨着**：摘要和替换之间插进任何一条
	// 事件，这次替换的影子价格就再也查不到了。
	tail := events[len(events)-4:]
	wantOrder := []session.EventType{
		compaction.EventCompactionStart,
		compaction.EventCompactionSummary,
		session.EventUserMessage,
		compaction.EventCompactionEnd,
	}
	for index, want := range wantOrder {
		if tail[index].Type != want {
			t.Fatalf("倒数第 %d 条是 %s，要的是 %s", 4-index, tail[index].Type, want)
		}
	}

	if result.CompactionID != "c-fixed" {
		t.Fatalf("身份铸成了 %q", result.CompactionID)
	}
	if result.StartSeq != tail[0].Seq || result.SummarySeq != tail[1].Seq || result.EndSeq != tail[3].Seq {
		t.Fatalf("三个 seq 对不上：%+v", result)
	}
	if result.ShadowedTokenCount != 200 {
		t.Fatalf("被遮的估价是 %d", result.ShadowedTokenCount)
	}
	if len(result.ShadowedSeqs) != 2 ||
		result.ShadowedSeqs[0] != log.seqs[0] || result.ShadowedSeqs[1] != log.seqs[1] {
		t.Fatalf("被遮的节点是 %v", result.ShadowedSeqs)
	}

	// 表面上那两条被换成了一条，而且换上去的那条盖着本次事务的章。
	nodes := log.live.SurfaceNodes()
	if len(nodes) != 3 || nodes[0] != tail[2].Seq {
		t.Fatalf("表面成了 %v", nodes)
	}
	if op, ok := tail[2].SurfaceOp.(session.ReplaceOp); !ok ||
		op.Start != log.seqs[0] || op.End != log.seqs[1] {
		t.Fatalf("那条替换的表面标记是 %+v", tail[2].SurfaceOp)
	}
	wantSources := []int{tail[0].Seq, tail[1].Seq, log.seqs[0], log.seqs[1]}
	if len(tail[2].SourceEventSeqs) != len(wantSources) {
		t.Fatalf("来源清单是 %v", tail[2].SourceEventSeqs)
	}
	for index, want := range wantSources {
		if tail[2].SourceEventSeqs[index] != want {
			t.Fatalf("来源清单是 %v，要的是 %v", tail[2].SourceEventSeqs, wantSources)
		}
	}

	message, produced, err := log.live.DeriveEventMessage(tail[2])
	if err != nil || !produced {
		t.Fatalf("那条检查点消息派生不出来：%v", err)
	}
	checkpoint, isCheckpoint, err := compaction.CheckpointSourceOf(message.Source)
	if err != nil || !isCheckpoint {
		t.Fatalf("那条消息没盖压缩的章：%+v（%v）", message.Source, err)
	}
	if checkpoint.CompactionID != "c-fixed" {
		t.Fatalf("章上的身份是 %q", checkpoint.CompactionID)
	}
}

func TestCompactSurfaceRegion自动那一档排成属于当前回合(t *testing.T) {
	t.Parallel()

	log := openLog(t)
	if _, err := CompactSurfaceRegion(t.Context(), cheapDeps(), &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]},
		agentAt(log.live, "openai", "gpt-x"), autoOptions()); err != nil {
		t.Fatalf("这次压缩该成：%v", err)
	}

	// 归属排错，这次压缩看起来就发生在另一个位置，而日志本身读得回来。
	_, startEvent := eventsByType(log.live.Events(), compaction.EventCompactionStart)
	start, err := compaction.DecodeStart(startEvent)
	if err != nil {
		t.Fatalf("开括号读不回来：%v", err)
	}
	if start.Standalone || start.Turn != 1 {
		t.Fatalf("归属排成了 %+v", start)
	}
	_, endEvent := eventsByType(log.live.Events(), compaction.EventCompactionEnd)
	end, err := compaction.DecodeEnd(endEvent)
	if err != nil {
		t.Fatalf("闭括号读不回来：%v", err)
	}
	if end.Standalone || end.Turn != 1 || end.Error != "" {
		t.Fatalf("闭括号排成了 %+v", end)
	}
}

func TestCompactSurfaceRegion人工那一档排成独立事务(t *testing.T) {
	t.Parallel()

	log := openLog(t)
	closeTurn(t, log.live)

	options := manualOptions()
	options.SourceCommandID = "cmd-1"
	result, err := CompactSurfaceRegion(t.Context(), cheapDeps(), &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]},
		agentAt(log.live, "openai", "gpt-x"), options)
	if err != nil {
		t.Fatalf("这次压缩该成：%v", err)
	}
	if result.SourceCommandID != "cmd-1" {
		t.Fatalf("命令身份是 %q", result.SourceCommandID)
	}

	_, startEvent := eventsByType(log.live.Events(), compaction.EventCompactionStart)
	start, err := compaction.DecodeStart(startEvent)
	if err != nil {
		t.Fatalf("开括号读不回来：%v", err)
	}
	if !start.Standalone || start.SourceCommandID != "cmd-1" {
		t.Fatalf("归属排成了 %+v", start)
	}
}

func TestCompactSurfaceRegion人工那一档撞上开着的回合就报占着(t *testing.T) {
	t.Parallel()

	// 那道落库的 compaction/start 只挡得住别的压缩事务，挡不住一个正在跑的回合。
	log := openLog(t)
	_, err := CompactSurfaceRegion(t.Context(), cheapDeps(), &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]},
		agentAt(log.live, "openai", "gpt-x"), manualOptions())
	var manual *compaction.ManualError
	if !errors.As(err, &manual) || manual.Code != compaction.ManualErrorBusy {
		t.Fatalf("该报占着，实际是 %v", err)
	}
	if count, _ := eventsByType(log.live.Events(), compaction.EventCompactionStart); count != 0 {
		t.Fatalf("拒了却还写了 %d 条开括号", count)
	}
}

func TestCompactSurfaceRegion自动那一档没有回合就是错(t *testing.T) {
	t.Parallel()

	// 一次自动压缩的那几条事件必须裹在回合里，否则那条括号写不出合法的归属。
	log := openLog(t)
	closeTurn(t, log.live)

	_, err := CompactSurfaceRegion(t.Context(), cheapDeps(), &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]},
		agentAt(log.live, "openai", "gpt-x"), autoOptions())
	if err == nil || !strings.Contains(err.Error(), "没有开着的回合") {
		t.Fatalf("该报没有回合，实际是 %v", err)
	}
}

func TestCompactSurfaceRegion上一个括号还开着就报占着(t *testing.T) {
	t.Parallel()

	log := openLog(t)
	appendTo(t, log.live, session.Event{
		Type: compaction.EventCompactionStart,
		Data: marshalPayload(t, compaction.StartData{CompactionID: "c-old", Turn: 1}),
	})

	_, err := CompactSurfaceRegion(t.Context(), cheapDeps(), &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]},
		agentAt(log.live, "openai", "gpt-x"), autoOptions())
	var manual *compaction.ManualError
	if !errors.As(err, &manual) || manual.Code != compaction.ManualErrorBusy {
		t.Fatalf("该报占着，实际是 %v", err)
	}
}

func TestCompactSurfaceRegion区间验不过就一个字都不写(t *testing.T) {
	t.Parallel()

	log := openLog(t)
	for name, region := range map[string]compaction.ShadowedRange{
		"头不在表面": {Start: 9999, End: log.seqs[1]},
		"尾不在表面": {Start: log.seqs[0], End: 9999},
		"头尾颠倒":  {Start: log.seqs[2], End: log.seqs[0]},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			before := len(log.live.Events())
			if _, err := CompactSurfaceRegion(t.Context(), cheapDeps(), &compaction.BalanceIndex{},
				region, agentAt(log.live, "openai", "gpt-x"), autoOptions()); err == nil {
				t.Fatal("该报区间不合法")
			}
			if len(log.live.Events()) != before {
				t.Fatal("拒了却还往日志里写了东西")
			}
		})
	}
}

func TestCompactSurfaceRegion稳定性口径没填就拒(t *testing.T) {
	t.Parallel()

	// 空口径落到「选中段」那一档是两档里更松的一档，于是一次本该被拦下的
	// 表面改写会安静地通过——所以这里宁可当场拒掉。
	log := openLog(t)
	options := autoOptions()
	options.Stability = ""

	_, err := CompactSurfaceRegion(t.Context(), cheapDeps(), &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]},
		agentAt(log.live, "openai", "gpt-x"), options)
	if err == nil || !strings.Contains(err.Error(), "稳定性口径") {
		t.Fatalf("该报口径不对，实际是 %v", err)
	}
	if count, _ := eventsByType(log.live.Events(), compaction.EventCompactionStart); count != 0 {
		t.Fatalf("拒了却还写了 %d 条开括号", count)
	}
}

func TestCompactSurfaceRegion总结失败也要把括号合上(t *testing.T) {
	t.Parallel()

	// 一次没有 compaction/end 的 start 会把这段会话的压缩锁永远占着。
	boom := errors.New("上游 502")
	log := openLog(t)
	deps := cheapDeps()
	deps.Summarize = failingSummary(boom)

	_, err := CompactSurfaceRegion(t.Context(), deps, &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]},
		agentAt(log.live, "openai", "gpt-x"), autoOptions())
	if !errors.Is(err, boom) {
		t.Fatalf("原本那条失败查不下去了：%v", err)
	}

	events := log.live.Events()
	if count, _ := eventsByType(events, compaction.EventCompactionStart); count != 1 {
		t.Fatalf("开括号有 %d 条", count)
	}
	count, endEvent := eventsByType(events, compaction.EventCompactionEnd)
	if count != 1 {
		t.Fatalf("闭括号有 %d 条", count)
	}
	end, err := compaction.DecodeEnd(endEvent)
	if err != nil {
		t.Fatalf("闭括号读不回来：%v", err)
	}
	if !strings.Contains(end.Error, "上游 502") {
		t.Fatalf("失败原因没落进闭括号：%q", end.Error)
	}
	// 做砸了的这次尝试**不回滚**：摘要和替换都没写，表面一动不动。
	if count, _ := eventsByType(events, compaction.EventCompactionSummary); count != 0 {
		t.Fatalf("没做成却写了 %d 条摘要", count)
	}
	if len(log.live.SurfaceNodes()) != 4 {
		t.Fatalf("表面被动了：%v", log.live.SurfaceNodes())
	}
}

func TestCompactSurfaceRegion人工那一侧按分类交回失败(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		deps RegionDeps
		want compaction.ManualErrorCode
	}{
		"总结那一步没成": {
			deps: RegionDeps{
				Meter:     &meteredSurface{fallback: 100, estimate: 10},
				Summarize: failingSummary(errors.New("上游 502")),
			},
			want: compaction.ManualErrorSummary,
		},
		"摘要没比原文小": {
			deps: RegionDeps{
				Meter:     &meteredSurface{fallback: 100, estimate: 500},
				Summarize: staticSummary("一份很长的摘要"),
			},
			want: compaction.ManualErrorSummary,
		},
		"提交那一步没成": {
			// 标了 llmStreamCall 却没带 rawOutput 的摘要事件排不出去，
			// 而那已经在「提交」这一步里了。
			deps: RegionDeps{
				Meter: &meteredSurface{fallback: 100, estimate: 10},
				Summarize: func(context.Context, SummarizationInput, compaction.AgentContext) (SummaryResult, error) {
					return SummaryResult{
						Summary:       llm.Content{llm.TextBlock{Text: "摘要"}},
						LLMStreamCall: true,
					}, nil
				},
			},
			want: compaction.ManualErrorCommit,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			log := openLog(t)
			closeTurn(t, log.live)

			_, err := CompactSurfaceRegion(t.Context(), testCase.deps, &compaction.BalanceIndex{},
				compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]},
				agentAt(log.live, "openai", "gpt-x"), manualOptions())
			var manual *compaction.ManualError
			if !errors.As(err, &manual) || manual.Code != testCase.want {
				t.Fatalf("该报 %q，实际是 %v", testCase.want, err)
			}
		})
	}
}

func TestCompactSurfaceRegion整表面那一档不许别处也变(t *testing.T) {
	t.Parallel()

	// 自动那一档跑在一个回合里，表面本来就不该有别的写入；有，就说明这份摘要
	// 建立的那份历史已经不是现在这份了。
	log := openLog(t)
	deps := cheapDeps()
	meter := &meteredSurface{fallback: 100, estimate: 10}
	// 定第二次价（稳定性判定那次）之前，往**选中段外面**追加一个节点。
	meter.before = func() {
		meter.before = func() {
			appendTo(t, log.live, surfaceMessage(t, session.EventUserMessage,
				session.UserMessageData{Message: llm.NewUserMessage(
					llm.Content{llm.TextBlock{Text: "插一句"}}, llm.UserSource{})}))
		}
	}
	deps.Meter = meter

	_, err := CompactSurfaceRegion(t.Context(), deps, &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]},
		agentAt(log.live, "openai", "gpt-x"), autoOptions())
	if !errors.Is(err, errSurfaceChanged) {
		t.Fatalf("该报表面变了，实际是 %v", err)
	}
}

func TestCompactSurfaceRegion选中段那一档容得下别处的追加(t *testing.T) {
	t.Parallel()

	// 空闲期里别的层仍然可能往后面追加可见节点。那些节点不会被这次替换动到，
	// 所以它们不该让一份已经做好的摘要作废。
	log := openLog(t)
	closeTurn(t, log.live)
	deps := cheapDeps()
	meter := &meteredSurface{fallback: 100, estimate: 10}
	meter.before = func() {
		meter.before = func() {
			appendTo(t, log.live, surfaceMessage(t, session.EventUserMessage,
				session.UserMessageData{Message: llm.NewUserMessage(
					llm.Content{llm.TextBlock{Text: "插一句"}}, llm.UserSource{})}))
		}
	}
	deps.Meter = meter

	if _, err := CompactSurfaceRegion(t.Context(), deps, &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]},
		agentAt(log.live, "openai", "gpt-x"), manualOptions()); err != nil {
		t.Fatalf("这次压缩该成：%v", err)
	}
}

func TestCompactSurfaceRegion选中段被改写了就拦下来(t *testing.T) {
	t.Parallel()

	for name, mutate := range map[string]func(*meteredSurface, logSession) func(){
		"那一段本身被换掉了": func(meter *meteredSurface, log logSession) func() {
			// 总结跑着的时候另一次替换把选中的那一段盖掉了，这份摘要要换的
			// 目标已经不在表面上。
			return func() {
				appendTo(t, log.live, session.Event{
					Type: session.EventUserMessage,
					Data: marshalPayload(t, session.UserMessageData{
						Message: llm.NewUserMessage(
							llm.Content{llm.TextBlock{Text: "别人先压了"}}, llm.UserSource{})}),
					SurfaceOp:       session.ReplaceOp{Start: log.seqs[0], End: log.seqs[1]},
					SourceEventSeqs: []int{log.seqs[0], log.seqs[1]},
				})
			}
		},
		"那一段的估价变了": func(meter *meteredSurface, log logSession) func() {
			// 节点还在，价变了，说明它被重写过——那份摘要照着的是旧内容。
			return func() { meter.tokens = map[int]int{log.seqs[0]: 7} }
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			log := openLog(t)
			closeTurn(t, log.live)
			meter := &meteredSurface{fallback: 100, estimate: 10}
			change := mutate(meter, log)
			meter.before = func() { meter.before = change }

			deps := cheapDeps()
			deps.Meter = meter
			_, err := CompactSurfaceRegion(t.Context(), deps, &compaction.BalanceIndex{},
				compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]},
				agentAt(log.live, "openai", "gpt-x"), manualOptions())
			var manual *compaction.ManualError
			if !errors.As(err, &manual) || manual.Code != compaction.ManualErrorChanged {
				t.Fatalf("该报那一段变了，实际是 %v", err)
			}
		})
	}
}

func TestCompactSurfaceRegion计量器算的表面短了也算变了(t *testing.T) {
	t.Parallel()

	// DSH 那句 slice 越界只会悄悄给一段短切片，Go 会 panic，所以下标先验一次——
	// 但判定的归属不变：那仍然是「表面变了」。
	log := openLog(t)
	closeTurn(t, log.live)
	deps := cheapDeps()
	deps.Meter = &shortMeter{}

	_, err := CompactSurfaceRegion(t.Context(), deps, &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]},
		agentAt(log.live, "openai", "gpt-x"), manualOptions())
	if !errors.Is(err, errSurfaceChanged) {
		t.Fatalf("该报表面变了，实际是 %v", err)
	}
}

// shortMeter 是一台只报得出一个节点的假计量器。
type shortMeter struct{}

func (shortMeter) PriceSurface(*coresession.Session) ([]PricedNode, error) {
	return []PricedNode{{Seq: 0, Tokens: 1}}, nil
}
func (shortMeter) EstimateMessage(llm.Message) (int, error) { return 1, nil }

func TestCompactSurfaceRegion计量器自己失败就原样交回去(t *testing.T) {
	t.Parallel()

	boom := errors.New("计量器坏了")
	for name, meter := range map[string]*meteredSurface{
		"定价那一步": {priceErr: boom},
		"估价那一步": {fallback: 100, estimateErr: boom},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			log := openLog(t)
			deps := cheapDeps()
			deps.Meter = meter
			_, err := CompactSurfaceRegion(t.Context(), deps, &compaction.BalanceIndex{},
				compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]},
				agentAt(log.live, "openai", "gpt-x"), autoOptions())
			if !errors.Is(err, boom) {
				t.Fatalf("原本那条失败查不下去了：%v", err)
			}
		})
	}
}

func TestCompactSurfaceRegion整表面那一档第二次定价失败也算失败(t *testing.T) {
	t.Parallel()

	boom := errors.New("第二次定价坏了")
	log := openLog(t)
	meter := &meteredSurface{fallback: 100, estimate: 10}
	meter.before = func() { meter.before = func() { meter.priceErr = boom } }
	deps := cheapDeps()
	deps.Meter = meter

	_, err := CompactSurfaceRegion(t.Context(), deps, &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]},
		agentAt(log.live, "openai", "gpt-x"), autoOptions())
	if !errors.Is(err, boom) {
		t.Fatalf("原本那条失败查不下去了：%v", err)
	}
}

func TestCompactSurfaceRegion选中段那一档第二次定价失败也算失败(t *testing.T) {
	t.Parallel()

	boom := errors.New("第二次定价坏了")
	log := openLog(t)
	closeTurn(t, log.live)
	meter := &meteredSurface{fallback: 100, estimate: 10}
	meter.before = func() { meter.before = func() { meter.priceErr = boom } }
	deps := cheapDeps()
	deps.Meter = meter

	_, err := CompactSurfaceRegion(t.Context(), deps, &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]},
		agentAt(log.live, "openai", "gpt-x"), manualOptions())
	if !errors.Is(err, boom) {
		t.Fatalf("原本那条失败查不下去了：%v", err)
	}
}

func TestCompactSurfaceRegion持久化检查点只在合上之后才跑(t *testing.T) {
	t.Parallel()

	log := openLog(t)
	closeTurn(t, log.live)
	flushed := 0
	options := manualOptions()
	options.Flush = func(context.Context) error {
		flushed++
		return nil
	}

	if _, err := CompactSurfaceRegion(t.Context(), cheapDeps(), &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]},
		agentAt(log.live, "openai", "gpt-x"), options); err != nil {
		t.Fatalf("这次压缩该成：%v", err)
	}
	if flushed != 1 {
		t.Fatalf("落库跑了 %d 次", flushed)
	}
}

func TestCompactSurfaceRegion持久化失败单独分一类(t *testing.T) {
	t.Parallel()

	// 这一类和别的不一样：表面已经换掉了、括号也合上了，只是没落到盘上。
	boom := errors.New("盘满了")
	log := openLog(t)
	closeTurn(t, log.live)
	options := manualOptions()
	options.Flush = func(context.Context) error { return boom }

	_, err := CompactSurfaceRegion(t.Context(), cheapDeps(), &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]},
		agentAt(log.live, "openai", "gpt-x"), options)
	var manual *compaction.ManualError
	if !errors.As(err, &manual) || manual.Code != compaction.ManualErrorPersistence {
		t.Fatalf("该报持久化失败，实际是 %v", err)
	}
	if !errors.Is(err, boom) {
		t.Fatal("原本那条失败查不下去了")
	}
	if count, _ := eventsByType(log.live.Events(), compaction.EventCompactionEnd); count != 1 {
		t.Fatal("括号该已经合上了")
	}
}

func TestCompactSurfaceRegion独立事务先看取消(t *testing.T) {
	t.Parallel()

	// 一次已经取消的人工压缩不该往日志里写任何东西。
	log := openLog(t)
	closeTurn(t, log.live)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	before := len(log.live.Events())
	_, err := CompactSurfaceRegion(ctx, cheapDeps(), &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]},
		agentAt(log.live, "openai", "gpt-x"), manualOptions())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("该报取消，实际是 %v", err)
	}
	if len(log.live.Events()) != before {
		t.Fatal("取消了却还往日志里写了东西")
	}
}

func TestCompactSurfaceRegion总结当中被取消就合上括号交回取消(t *testing.T) {
	t.Parallel()

	// 取消原样交回去，不裹成 [compaction.ManualError]：分类是引擎那一层的事。
	log := openLog(t)
	closeTurn(t, log.live)
	ctx, cancel := context.WithCancel(t.Context())
	deps := cheapDeps()
	deps.Summarize = func(context.Context, SummarizationInput, compaction.AgentContext) (SummaryResult, error) {
		cancel()
		return SummaryResult{
			Summary:  llm.Content{llm.TextBlock{Text: "摘要"}},
			Provider: "openai", Model: "gpt-x",
		}, nil
	}

	_, err := CompactSurfaceRegion(ctx, deps, &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]},
		agentAt(log.live, "openai", "gpt-x"), manualOptions())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("该报取消，实际是 %v", err)
	}
	// 括号仍然要合上：取消不是「什么都没发生」，那条 start 已经落库了。
	if count, _ := eventsByType(log.live.Events(), compaction.EventCompactionEnd); count != 1 {
		t.Fatal("被取消了也得把括号合上")
	}
}

func TestCompactSurfaceRegion自动那一档不看取消(t *testing.T) {
	t.Parallel()

	// 跟着回合走的那一档由回合自己的取消管，这里不再看第二遍——
	// 看了会让一次已经提交的替换以取消的名义报错，而它其实做完了。
	log := openLog(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := CompactSurfaceRegion(ctx, cheapDeps(), &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]},
		agentAt(log.live, "openai", "gpt-x"), autoOptions()); err != nil {
		t.Fatalf("这次压缩该成：%v", err)
	}
}

func TestCompactSurfaceRegion重放的输入就是那一段自己(t *testing.T) {
	t.Parallel()

	// 系统提示词、工具表照最近那份请求头原样摆上，后面只跟被遮那些节点的消息。
	// 多一条少一条，这次调用就不再是上一次路由请求的前缀，前缀缓存整个作废。
	log := openLog(t, llm.CallConfig{Provider: "anthropic", Model: "big"})
	appendTo(t, log.live, session.Event{
		Type: session.EventRequestHeader,
		Data: marshalPayload(t, session.RequestHeaderData{
			Header: session.EpochHeader{
				Config: llm.CallConfig{Provider: "anthropic", Model: "big"},
				System: "你是一个助手",
				Tools:  []llm.ToolSchema{{Name: "read", Description: "读文件"}},
			},
			Reason: session.HeaderInitial,
		}),
	})

	var seen SummarizationInput
	deps := cheapDeps()
	deps.Summarize = func(_ context.Context, input SummarizationInput, _ compaction.AgentContext) (SummaryResult, error) {
		seen = input
		return SummaryResult{
			Summary:  llm.Content{llm.TextBlock{Text: "摘要"}},
			Provider: "openai", Model: "gpt-x",
		}, nil
	}

	if _, err := CompactSurfaceRegion(t.Context(), deps, &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]},
		agentAt(log.live, "openai", "gpt-x"), autoOptions()); err != nil {
		t.Fatalf("这次压缩该成：%v", err)
	}
	if seen.System != "你是一个助手" {
		t.Fatalf("系统提示词是 %q", seen.System)
	}
	if len(seen.Tools) != 1 || seen.Tools[0].Name != "read" {
		t.Fatalf("工具表是 %+v", seen.Tools)
	}
	if len(seen.Messages) != 2 {
		t.Fatalf("重放了 %d 条消息", len(seen.Messages))
	}
	if seen.Messages[0].Role != llm.RoleUser || seen.Messages[1].Role != llm.RoleAssistant {
		t.Fatalf("重放的顺序是 %q / %q", seen.Messages[0].Role, seen.Messages[1].Role)
	}
}

func TestCompactSurfaceRegion没有请求头也重放得出来(t *testing.T) {
	t.Parallel()

	// 一段还没路由过任何请求的会话没有系统提示词和工具表，这不是错。
	log := openLog(t)
	var seen SummarizationInput
	deps := cheapDeps()
	deps.Summarize = func(_ context.Context, input SummarizationInput, _ compaction.AgentContext) (SummaryResult, error) {
		seen = input
		return SummaryResult{
			Summary:  llm.Content{llm.TextBlock{Text: "摘要"}},
			Provider: "openai", Model: "gpt-x",
		}, nil
	}

	if _, err := CompactSurfaceRegion(t.Context(), deps, &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]},
		agentAt(log.live, "openai", "gpt-x"), autoOptions()); err != nil {
		t.Fatalf("这次压缩该成：%v", err)
	}
	if seen.System != "" || seen.Tools != nil {
		t.Fatalf("凭空多出了请求头：%q / %+v", seen.System, seen.Tools)
	}
	if len(seen.Messages) != 2 {
		t.Fatalf("重放了 %d 条消息", len(seen.Messages))
	}
}

func TestCompactSurfaceRegion没注入就自己铸一个身份(t *testing.T) {
	t.Parallel()

	log := openLog(t)
	options := autoOptions()
	options.NewID = nil

	result, err := CompactSurfaceRegion(t.Context(), cheapDeps(), &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]},
		agentAt(log.live, "openai", "gpt-x"), options)
	if err != nil {
		t.Fatalf("这次压缩该成：%v", err)
	}
	// 身份为空的检查点落进日志之后，它属于哪次压缩就再也查不出来了。
	if result.CompactionID == "" {
		t.Fatal("没铸出身份")
	}
}

// toolLog 排一段带一次工具往返的会话：用户说话、助手发起一次调用、工具交回结果。
//
// 它专门用来验两头的下刀点：助手和工具结果中间那一刀不配平。
func toolLog(t *testing.T) logSession {
	t.Helper()

	live := liveSession(t, "s-tool")
	appendTo(t, live, session.Event{
		Type: session.EventTurnStart,
		Data: marshalPayload(t, session.TurnStartData{Turn: 1}),
	})
	seqs := []int{
		appendTo(t, live, surfaceMessage(t, session.EventUserMessage,
			session.UserMessageData{Message: llm.NewUserMessage(
				llm.Content{llm.TextBlock{Text: "查一下"}}, llm.UserSource{})})),
		appendTo(t, live, surfaceMessage(t, session.EventAssistantMessage,
			session.AssistantMessageData{
				Turn: 1, Step: 1,
				Message: llm.NewAssistantMessage(llm.Content{
					llm.TextBlock{Text: "我来查"},
					llm.ToolCallBlock{ID: "call-a", Name: "read", Arguments: "{}"},
				}, llm.Provenance{}),
			})),
		appendTo(t, live, surfaceMessage(t, session.EventToolResult,
			session.ToolResultData{
				Turn: 1, Step: 1,
				Message: llm.Message{
					ID:      "t",
					Role:    llm.RoleUser,
					Content: llm.Content{llm.TextBlock{Text: "结果"}},
					Source:  llm.ToolSource{CallID: "call-a"},
				},
			})),
	}
	return logSession{live: live, seqs: seqs}
}

// mutatingSummary 排一台「总结期间顺手把会话改了」的假总结。
func mutatingSummary(text string, change func()) Summarize {
	return func(context.Context, SummarizationInput, compaction.AgentContext) (SummaryResult, error) {
		change()
		return SummaryResult{
			Summary:  llm.Content{llm.TextBlock{Text: text}},
			Provider: "openai",
			Model:    "gpt-x",
		}, nil
	}
}

// replaceNodes 造一条把 shadowed 这些节点换成一句话的替换消息，用来扮演
// 「别人下的刀」。被盖掉的节点要一个不落地列进 SourceEventSeqs，不然表面层直接拒。
func replaceNodes(t *testing.T, shadowed ...int) session.Event {
	t.Helper()

	return session.Event{
		Type: session.EventUserMessage,
		Data: marshalPayload(t, session.UserMessageData{Message: llm.NewUserMessage(
			llm.Content{llm.TextBlock{Text: "别人下的刀"}}, llm.UserSource{})}),
		SurfaceOp:       session.ReplaceOp{Start: shadowed[0], End: shadowed[len(shadowed)-1]},
		SourceEventSeqs: slices.Clone(shadowed),
	}
}

func TestCompactSurfaceRegion空会话压不出东西(t *testing.T) {
	t.Parallel()

	// 一段一条事件都没有的会话没有表面，也就没有任何一段可压。这里顺带钉住
	// 「空日志的基准 seq 是 0」这条约定：基准取成别的数，后面每一次按 seq
	// 取事件都会错位，而错位取到的仍然是一条读得回来的事件。
	live := liveSession(t, "s-empty")

	_, err := CompactSurfaceRegion(t.Context(), cheapDeps(), &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: 0, End: 0},
		agentAt(live, "openai", "gpt-x"), autoOptions())
	if err == nil || !strings.Contains(err.Error(), "压不了这一段的头") {
		t.Fatalf("该报表面上没有这个 seq，实际是 %v", err)
	}
	if live.Seq() != 0 {
		t.Fatalf("空会话上写进去了 %d 条事件", live.Seq())
	}
}

func TestCompactSurfaceRegion两头都得是配平的下刀点(t *testing.T) {
	t.Parallel()

	// 把一次工具调用和它的结果劈开，模型会收到一条没有在先调用的工具结果，
	// 或者一次永远等不到结果的调用——两种都是提供方那边直接拒的请求。
	for name, pick := range map[string]func(logSession) compaction.ShadowedRange{
		"头那一刀劈开了": func(log logSession) compaction.ShadowedRange {
			return compaction.ShadowedRange{Start: log.seqs[2], End: log.seqs[2]}
		},
		"尾那一刀劈开了": func(log logSession) compaction.ShadowedRange {
			return compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			log := toolLog(t)
			_, err := CompactSurfaceRegion(t.Context(), cheapDeps(), &compaction.BalanceIndex{},
				pick(log), agentAt(log.live, "openai", "gpt-x"), autoOptions())
			if err == nil || !strings.Contains(err.Error(), "配平的下刀点") {
				t.Fatalf("该报下刀点不配平，实际是 %v", err)
			}
			if starts, _ := eventsByType(log.live.Events(),
				compaction.EventCompactionStart); starts != 0 {
				t.Fatal("验都没验过就把括号开了")
			}
		})
	}
}

func TestCompactSurfaceRegion表面本身坏了就原样报出来(t *testing.T) {
	t.Parallel()

	// 一条没有在先调用的工具结果坐在表面开头，说明这段日志和它的表面已经对不上账。
	// 这条错要原样交回去（[compaction.ErrSurfaceCorrupt]），不能被含糊成
	// 「这一刀不配平」——后者读起来像是区间选错了，会让人去改区间而不是查日志。
	live := liveSession(t, "s-corrupt")
	appendTo(t, live, session.Event{
		Type: session.EventTurnStart,
		Data: marshalPayload(t, session.TurnStartData{Turn: 1}),
	})
	orphan := appendTo(t, live, surfaceMessage(t, session.EventToolResult,
		session.ToolResultData{
			Turn: 1, Step: 1,
			Message: llm.Message{
				ID:      "t",
				Role:    llm.RoleUser,
				Content: llm.Content{llm.TextBlock{Text: "没人叫过的结果"}},
				Source:  llm.ToolSource{CallID: "call-a"},
			},
		}))

	_, err := CompactSurfaceRegion(t.Context(), cheapDeps(), &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: orphan, End: orphan},
		agentAt(live, "openai", "gpt-x"), autoOptions())
	if !errors.Is(err, compaction.ErrSurfaceCorrupt) {
		t.Fatalf("该报表面坏了，实际是 %v", err)
	}
}

func TestCompactSurfaceRegion回合边界读不回来就停在开工之前(t *testing.T) {
	t.Parallel()

	// 一条读不回来的 turn/start 会让「有没有开着的回合」变成猜——猜错的后果是
	// 一次自动压缩被排成独立事务，或者反过来，而那条括号本身读得回来。
	log := openLog(t)
	appendTo(t, log.live, session.Event{
		Type: session.EventTurnStart,
		Data: json.RawMessage("[1,2]"),
	})
	before := log.live.Seq()

	_, err := CompactSurfaceRegion(t.Context(), cheapDeps(), &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]},
		agentAt(log.live, "openai", "gpt-x"), autoOptions())
	if !errors.Is(err, compaction.ErrMalformedEvent) {
		t.Fatalf("该报回合边界读不回来，实际是 %v", err)
	}
	if log.live.Seq() != before {
		t.Fatal("读不出归属还是把括号开了")
	}
}

func TestCompactSurfaceRegion请求头读不回来就算失败(t *testing.T) {
	t.Parallel()

	// 重放不出系统提示词和工具表，这次总结就不再是上一次路由请求的前缀。
	// 与其发一次前缀对不上的调用，不如当场失败——**但括号已经开了，得合上**。
	log := openLog(t)
	appendTo(t, log.live, session.Event{
		Type: session.EventRequestHeader,
		Data: json.RawMessage("[1,2]"),
	})

	_, err := CompactSurfaceRegion(t.Context(), cheapDeps(), &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]},
		agentAt(log.live, "openai", "gpt-x"), autoOptions())
	if err == nil {
		t.Fatal("请求头读不回来该算失败")
	}
	events := log.live.Events()
	if starts, _ := eventsByType(events, compaction.EventCompactionStart); starts != 1 {
		t.Fatalf("开括号有 %d 个", starts)
	}
	ends, last := eventsByType(events, compaction.EventCompactionEnd)
	if ends != 1 {
		t.Fatalf("闭括号有 %d 个", ends)
	}
	end, decodeErr := compaction.DecodeEnd(last)
	if decodeErr != nil {
		t.Fatalf("闭括号读不回来：%v", decodeErr)
	}
	if end.Error == "" {
		t.Fatal("闭括号上没写失败原因")
	}
}

func TestCompactSurfaceRegion被遮的那一段读不回来就算失败(t *testing.T) {
	t.Parallel()

	// 表面上有这个节点、它的负载却投影不出消息，说明日志坏了。这时候放行等于
	// 拿一段少了几句话的对话去做摘要，而摘要出来读着仍然是完整的。
	live := liveSession(t, "s-derive")
	appendTo(t, live, session.Event{
		Type: session.EventTurnStart,
		Data: marshalPayload(t, session.TurnStartData{Turn: 1}),
	})
	broken := appendTo(t, live, session.Event{
		Type:      session.EventUserMessage,
		Data:      json.RawMessage("[1,2]"),
		SurfaceOp: session.AppendOp{},
	})
	tail := appendTo(t, live, surfaceMessage(t, session.EventAssistantMessage,
		session.AssistantMessageData{
			Turn: 1, Step: 1,
			Message: llm.NewAssistantMessage(llm.Content{llm.TextBlock{Text: "好"}}, llm.Provenance{}),
		}))

	_, err := CompactSurfaceRegion(t.Context(), cheapDeps(), &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: broken, End: tail},
		agentAt(live, "openai", "gpt-x"), autoOptions())
	if err == nil || !strings.Contains(err.Error(), "用户消息事件的负载读不回来") {
		t.Fatalf("该报那条消息读不回来，实际是 %v", err)
	}
	if ends, _ := eventsByType(live.Events(), compaction.EventCompactionEnd); ends != 1 {
		t.Fatalf("闭括号有 %d 个", ends)
	}
}

func TestCompactSurfaceRegion选中段整个被别人换掉了就拦下来(t *testing.T) {
	t.Parallel()

	// 总结期间别人先下了一刀，把这一段整个换走了。这份摘要于是指向一段
	// 已经不在表面上的历史，再替换一次等于把别人那次替换盖掉。
	log := openLog(t)
	closeTurn(t, log.live)
	deps := cheapDeps()
	deps.Summarize = mutatingSummary("一份摘要", func() {
		appendTo(t, log.live, replaceNodes(t, log.seqs...))
	})

	_, err := CompactSurfaceRegion(t.Context(), deps, &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]},
		agentAt(log.live, "openai", "gpt-x"), manualOptions())
	var manual *compaction.ManualError
	if !errors.As(err, &manual) || manual.Code != compaction.ManualErrorChanged {
		t.Fatalf("该分到 changed 那一档，实际是 %v", err)
	}
	if !strings.Contains(manual.Cause.Error(), "不再是一个能用的替换目标") {
		t.Fatalf("失败原因是 %v", manual.Cause)
	}
}

func TestCompactSurfaceRegion选中段中间被别人换掉了就拦下来(t *testing.T) {
	t.Parallel()

	// 两头都还在表面上、两头也都还配平，中间那两个节点却被换成了一个。
	// 只看两头看不出这件事，所以要逐个比被遮的那串 seq——比不出来的话，
	// 这次替换会把别人刚写下的那条检查点也一起遮掉。
	log := openLog(t)
	closeTurn(t, log.live)
	deps := cheapDeps()
	deps.Summarize = mutatingSummary("一份摘要", func() {
		appendTo(t, log.live, replaceNodes(t, log.seqs[1], log.seqs[2]))
	})

	_, err := CompactSurfaceRegion(t.Context(), deps, &compaction.BalanceIndex{},
		compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[3]},
		agentAt(log.live, "openai", "gpt-x"), manualOptions())
	var manual *compaction.ManualError
	if !errors.As(err, &manual) || manual.Code != compaction.ManualErrorChanged {
		t.Fatalf("该分到 changed 那一档，实际是 %v", err)
	}
	if !strings.Contains(manual.Cause.Error(), "选中的那一段变了") {
		t.Fatalf("失败原因是 %v", manual.Cause)
	}
}

func TestCompactSurfaceRegion在一次事件通告里开不了工(t *testing.T) {
	t.Parallel()

	// 一个挂在 [coresession.Store.OnEvent] 上的观察者是在**追加还没发布完**的
	// 时候被叫到的，那会儿这段会话不接受任何新的追加。压缩要是从这里发起，
	// 连开括号都写不进去——这一条钉的是「写不进去就当场停，别接着往下做」：
	// 接着做的话，摘要请求发出去了、钱花了，而日志上没有任何一条记着这件事。
	ctx := context.Background()
	owner := scope.NewRoot()
	t.Cleanup(func() { _ = owner.Dispose(ctx) })

	store, err := coresession.NewStore(coresession.StoreOptions{})
	if err != nil {
		t.Fatalf("造会话存储失败：%v", err)
	}
	live, err := store.Prepare("s-observed", coresession.CreateOptions{WorkspaceID: summarizeWorkspaceID})
	if err != nil {
		t.Fatalf("备会话失败：%v", err)
	}
	detach, err := store.Enter(owner, live)
	if err != nil {
		t.Fatalf("会话进存储失败：%v", err)
	}
	t.Cleanup(func() { _ = detach(ctx) })

	appendTo(t, live, session.Event{
		Type: session.EventTurnStart,
		Data: marshalPayload(t, session.TurnStartData{Turn: 1}),
	})
	head := appendTo(t, live, surfaceMessage(t, session.EventUserMessage,
		session.UserMessageData{Message: llm.NewUserMessage(
			llm.Content{llm.TextBlock{Text: "第一句"}}, llm.UserSource{})}))

	// armed 只让最后那一条追加把压缩叫起来；不然建日志的每一条都会触发一次。
	armed := false
	var failure error
	if _, err := store.OnEvent(ctx, owner, func(_ *coresession.Session, _ session.Event) {
		if !armed {
			return
		}
		armed = false
		_, failure = CompactSurfaceRegion(ctx, cheapDeps(), &compaction.BalanceIndex{},
			compaction.ShadowedRange{Start: head, End: head},
			agentAt(live, "openai", "gpt-x"), autoOptions())
	}); err != nil {
		t.Fatalf("登记观察者失败：%v", err)
	}

	armed = true
	appendTo(t, live, surfaceMessage(t, session.EventAssistantMessage,
		session.AssistantMessageData{
			Turn: 1, Step: 1,
			Message: llm.NewAssistantMessage(llm.Content{llm.TextBlock{Text: "好"}}, llm.Provenance{}),
		}))

	if !errors.Is(failure, coresession.ErrInvalidAppend) {
		t.Fatalf("该报这次追加写不进去，实际是 %v", failure)
	}
	if !strings.Contains(failure.Error(), "写不进日志") {
		t.Fatalf("失败原因是 %v", failure)
	}
	if starts, _ := eventsByType(live.Events(), compaction.EventCompactionStart); starts != 0 {
		t.Fatalf("开括号写进去了 %d 个", starts)
	}
}
