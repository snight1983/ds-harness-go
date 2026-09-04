// 本文件的作用：验在一份稳定的表面快照上真的砍一遍之后，日志里留下的是什么——
// 计价事件和替换件的相邻关系、替换件保住了原件的哪些东西，以及中途失败留下什么。

package toolresultpruner

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/feature/compaction"
	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// # 这些测试防的是什么错
//
//   - compaction/prune 和它那条替换件中间插进了别的东西。一条 replace 的价格由
//     **紧挨在它前面**的那条计价事件给出，插一条就等于这次替换省下来的钱再也
//     查不到——而日志本身**读得回来**，没有任何地方会报警。
//   - 替换件把原件除正文之外的东西也改了。回合号、步骤号、错误身份、工具私有的
//     展示负载、消息身份和那次调用的相关性，任何一样丢了都意味着重放拿不回同一
//     条结果。
//   - sourceEventSeqs 漏了被遮的那个节点，于是重放拿不回这次替换的输入。
//   - 边遍历边追加：新写进去的替换件自己也进了遍历范围，而它按定义已经在预算
//     之内了——真砍第二遍的话砍出来的是「头 + 标记 + 头 + 标记 + 尾」。
//   - 中途失败把前面已经落地的那些账目也丢了。它们是**持久的**，调用方要按这个
//     数决定还要不要重试。
//   - 在预算之内的工具结果被平白追加了一对事件：一次什么都没省下来的替换。

// pruneWorkspaceID 是这些用例里那个会话归属的工作区登记。
//
// 新增: 原来这里是 filepath.Join(os.TempDir(), …)，为的是「在本机上确实绝对」。
// 会话头改用世界路径之后那条理由没有了：绝对性由纯 POSIX 的 [path.IsAbs] 判，
// 不跟着本机平台走，所以一个字面量在哪台机器上读都一样。
var pruneWorkspaceID = sessionlog.WorkspaceID("ws-toolresultpruner")

// flatEstimator 是一台每条消息都报同一个价的假计量器。
type flatEstimator int

func (e flatEstimator) EstimateMessage(llm.Message) (int, error) { return int(e), nil }

// failingEstimator 是一台一开口就失败的假计量器。
type failingEstimator struct{ err error }

func (e failingEstimator) EstimateMessage(llm.Message) (int, error) { return 0, e.err }

// newLive 造一段空的游离会话。
func newLive(t *testing.T) *coresession.Session {
	t.Helper()

	sid := sessionlog.SessionID("s-prune")
	live, err := coresession.NewSession(sid, coresession.Options{
		Header: &sessionlog.SessionHeader{ID: sid, WorkspaceID: pruneWorkspaceID},
	})
	if err != nil {
		t.Fatalf("造会话失败：%v", err)
	}
	return live
}

// appendEvent 往日志里追加一条事件，负载现排。
func appendEvent(t *testing.T, live *coresession.Session, kind sessionlog.EventType,
	payload any, op sessionlog.SurfaceOp, sources []int,
) sessionlog.Event {
	t.Helper()

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("负载排不出去：%v", err)
	}
	event, err := live.Append(sessionlog.Event{
		Type: kind, Data: data, SurfaceOp: op, SourceEventSeqs: sources,
	})
	if err != nil {
		t.Fatalf("%s 写不进去：%v", kind, err)
	}
	return event
}

// toolResultAt 往日志里追加一条工具结果，正文是 text。
func toolResultAt(t *testing.T, live *coresession.Session, callID llm.CallID, text string,
) sessionlog.Event {
	t.Helper()

	message := llm.NewToolResultMessage(callID, llm.Content{llm.TextBlock{Text: text}}, false)
	return appendEvent(t, live, sessionlog.EventToolResult, sessionlog.ToolResultData{
		Turn: 1, Step: 2, Message: message, Meta: json.RawMessage(`{"tool":"read"}`),
	}, sessionlog.AppendOp{}, nil)
}

// decodeToolResult 把一条 tool/result 事件解回它的负载。
func decodeToolResult(t *testing.T, event sessionlog.Event) sessionlog.ToolResultData {
	t.Helper()

	decoded, err := sessionlog.DecodeData(event)
	if err != nil {
		t.Fatalf("tool/result 解不回来：%v", err)
	}
	data, ok := decoded.(sessionlog.ToolResultData)
	if !ok {
		t.Fatalf("解出来是 %T", decoded)
	}
	return data
}

// longText 造一段肯定超预算的文本。
func longText() string { return strings.Repeat("x", 200) }

func TestPruneSession计价事件紧贴在替换件前面(t *testing.T) {
	t.Parallel()

	// 一条 replace 的价格由紧挨在它前面的那条计价事件给出。中间插一条，
	// 这次替换省下来的钱就再也查不到了——而日志本身读得回来。
	live := newLive(t)
	original := toolResultAt(t, live, "call-1", longText())

	result, err := smallPruner(t).PruneSession(live, flatEstimator(777))
	if err != nil {
		t.Fatalf("这一趟该成：%v", err)
	}
	if len(result.Pruned) != 1 {
		t.Fatalf("砍了 %d 条", len(result.Pruned))
	}

	events := live.Events()
	priced, replacement := events[len(events)-2], events[len(events)-1]
	if priced.Type != compaction.EventCompactionPrune {
		t.Fatalf("紧挨在替换件前面的是 %q", priced.Type)
	}
	if replacement.Type != sessionlog.EventToolResult {
		t.Fatalf("最后一条是 %q", replacement.Type)
	}
	if replacement.Seq != priced.Seq+1 {
		t.Fatalf("中间插进了东西：计价 %d、替换 %d", priced.Seq, replacement.Seq)
	}

	prune, err := compaction.DecodePrune(priced)
	if err != nil {
		t.Fatalf("计价事件解不回来：%v", err)
	}
	if prune.ShadowedTokenCount != 777 {
		t.Fatalf("估价记成了 %d", prune.ShadowedTokenCount)
	}
	want := compaction.ShadowedRange{Start: original.Seq, End: original.Seq}
	if prune.ShadowedRange != want || len(prune.ShadowedSeqs) != 1 ||
		prune.ShadowedSeqs[0] != original.Seq {
		t.Fatalf("被遮的那一段记成了 %+v / %v", prune.ShadowedRange, prune.ShadowedSeqs)
	}
}

func TestPruneSession替换件只换正文(t *testing.T) {
	t.Parallel()

	// 回合号、步骤号、工具私有的展示负载、消息身份和那次调用的相关性，
	// 任何一样丢了都意味着重放拿不回同一条结果。
	live := newLive(t)
	original := toolResultAt(t, live, "call-1", longText())
	before := decodeToolResult(t, original)

	if _, err := smallPruner(t).PruneSession(live, flatEstimator(10)); err != nil {
		t.Fatalf("这一趟该成：%v", err)
	}

	events := live.Events()
	replacement := events[len(events)-1]
	after := decodeToolResult(t, replacement)

	if after.Turn != before.Turn || after.Step != before.Step {
		t.Fatalf("回合／步骤变成了 %d/%d", after.Turn, after.Step)
	}
	if string(after.Meta) != string(before.Meta) {
		t.Fatalf("工具私有负载变成了 %s", after.Meta)
	}
	if after.Message.ID != before.Message.ID {
		t.Fatalf("消息身份变了：%q → %q", before.Message.ID, after.Message.ID)
	}
	source, ok := after.Message.Source.(llm.ToolSource)
	if !ok || source.CallID != "call-1" {
		t.Fatalf("那次调用的相关性丢了：%+v", after.Message.Source)
	}
	block, ok := after.Message.Content[0].(llm.ToolResultBlock)
	if !ok || block.ToolCallID != "call-1" {
		t.Fatalf("结果块变成了 %+v", after.Message.Content[0])
	}
	text, ok := block.Content[0].(llm.TextBlock)
	if !ok || !strings.Contains(text.Text, PruneMarker) {
		t.Fatalf("正文没被砍：%+v", block.Content)
	}

	// 被遮的那个节点必须列进去，重放才拿得回这次替换的输入。
	if len(replacement.SourceEventSeqs) != 1 || replacement.SourceEventSeqs[0] != original.Seq {
		t.Fatalf("出处记成了 %v", replacement.SourceEventSeqs)
	}
	if _, ok := replacement.SurfaceOp.(sessionlog.ReplaceOp); !ok {
		t.Fatalf("表面动作是 %T", replacement.SurfaceOp)
	}
}

func TestPruneSession账目对得上砍掉的码点(t *testing.T) {
	t.Parallel()

	live := newLive(t)
	original := toolResultAt(t, live, "call-1", longText())

	result, err := smallPruner(t).PruneSession(live, flatEstimator(10))
	if err != nil {
		t.Fatalf("这一趟该成：%v", err)
	}
	entry := result.Pruned[0]
	if entry.OriginalSeq != original.Seq {
		t.Fatalf("原件记成了 seq %d", entry.OriginalSeq)
	}
	if entry.ReplacementSeq != live.Seq()-1 {
		t.Fatalf("替换件记成了 seq %d", entry.ReplacementSeq)
	}
	if entry.CallID != "call-1" {
		t.Fatalf("调用身份记成了 %q", entry.CallID)
	}
	if entry.CharsBefore != 200 {
		t.Fatalf("砍之前记成了 %d 个码点", entry.CharsBefore)
	}
	if entry.CharsAfter >= entry.CharsBefore {
		t.Fatalf("砍完反而是 %d 个码点", entry.CharsAfter)
	}
	if result.CharsRemoved != entry.CharsBefore-entry.CharsAfter {
		t.Fatalf("总账记成了 %d", result.CharsRemoved)
	}
}

func TestPruneSession预算之内的一个字都不动(t *testing.T) {
	t.Parallel()

	// 一次什么都没省下来的替换是纯亏：日志长了两条，上下文一个 token 没少。
	live := newLive(t)
	toolResultAt(t, live, "call-1", "短的")
	before := live.Seq()

	result, err := smallPruner(t).PruneSession(live, flatEstimator(10))
	if err != nil {
		t.Fatalf("这一趟该成：%v", err)
	}
	if len(result.Pruned) != 0 || result.CharsRemoved != 0 {
		t.Fatalf("动了 %+v", result)
	}
	if live.Seq() != before {
		t.Fatalf("日志从 %d 长到了 %d", before, live.Seq())
	}
}

func TestPruneSession只在快照上砍一遍(t *testing.T) {
	t.Parallel()

	// 边遍历边追加会让新写进去的替换件自己也进遍历范围，而它按定义已经在预算
	// 之内了——真砍第二遍砍出来的是「头 + 标记 + 头 + 标记 + 尾」。
	live := newLive(t)
	toolResultAt(t, live, "call-1", longText())
	toolResultAt(t, live, "call-2", longText())

	result, err := smallPruner(t).PruneSession(live, flatEstimator(10))
	if err != nil {
		t.Fatalf("这一趟该成：%v", err)
	}
	if len(result.Pruned) != 2 {
		t.Fatalf("砍了 %d 条", len(result.Pruned))
	}
	for _, event := range live.Events() {
		if event.Type != sessionlog.EventToolResult {
			continue
		}
		data := decodeToolResult(t, event)
		block, ok := data.Message.Content[0].(llm.ToolResultBlock)
		if !ok {
			continue
		}
		text, ok := block.Content[0].(llm.TextBlock)
		if !ok {
			continue
		}
		if strings.Count(text.Text, PruneMarker) > 1 {
			t.Fatalf("同一条被砍了不止一遍：%q", text.Text)
		}
	}
}

func TestPruneSession不是工具结果的节点一律跳过(t *testing.T) {
	t.Parallel()

	live := newLive(t)
	appendEvent(t, live, sessionlog.EventUserMessage, sessionlog.UserMessageData{
		Message: llm.NewUserMessage(llm.Content{llm.TextBlock{Text: longText()}}, llm.UserSource{}),
	}, sessionlog.AppendOp{}, nil)
	before := live.Seq()

	result, err := smallPruner(t).PruneSession(live, flatEstimator(10))
	if err != nil {
		t.Fatalf("这一趟该成：%v", err)
	}
	if len(result.Pruned) != 0 || live.Seq() != before {
		t.Fatalf("动了一条用户消息：%+v", result)
	}
}

func TestPruneSession形状不对的结果消息跳过(t *testing.T) {
	t.Parallel()

	// 一条 tool/result 的消息按 [llm.NewToolResultMessage] 只有一块结果块。
	// 形状不对说明它不是那个构造器产出的，跳过比猜安全。
	for name, content := range map[string]llm.Content{
		"不止一块":    {llm.ToolResultBlock{ToolCallID: "c"}, llm.TextBlock{Text: longText()}},
		"根本不是结果块": {llm.TextBlock{Text: longText()}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			live := newLive(t)
			appendEvent(t, live, sessionlog.EventToolResult, sessionlog.ToolResultData{
				Message: llm.NewUserMessage(content, llm.ToolSource{CallID: "c"}),
			}, sessionlog.AppendOp{}, nil)
			before := live.Seq()

			result, err := smallPruner(t).PruneSession(live, flatEstimator(10))
			if err != nil {
				t.Fatalf("这一趟该成：%v", err)
			}
			if len(result.Pruned) != 0 || live.Seq() != before {
				t.Fatalf("动了 %+v", result)
			}
		})
	}
}

func TestPruneSession负载读不回来就停在动手之前(t *testing.T) {
	t.Parallel()

	// 快照是先照完再动手的，所以照的过程里出错时日志一个字都没改。
	live := newLive(t)
	toolResultAt(t, live, "call-1", longText())
	if _, err := live.Append(sessionlog.Event{
		Type: sessionlog.EventToolResult, Data: json.RawMessage("[1,2]"), SurfaceOp: sessionlog.AppendOp{},
	}); err != nil {
		t.Fatalf("坏事件写不进去：%v", err)
	}
	before := live.Seq()

	_, err := smallPruner(t).PruneSession(live, flatEstimator(10))
	if !errors.Is(err, compaction.ErrMalformedEvent) {
		t.Fatalf("报的是 %v", err)
	}
	if live.Seq() != before {
		t.Fatalf("已经动手了：日志从 %d 长到了 %d", before, live.Seq())
	}
}

func TestPruneSession估价算不出来时前面那些留着(t *testing.T) {
	t.Parallel()

	// 已经落地的替换是**持久的**，调用方要按这个数决定还要不要重试。
	boom := errors.New("计量器炸了")
	live := newLive(t)
	toolResultAt(t, live, "call-1", longText())

	result, err := smallPruner(t).PruneSession(live, failingEstimator{err: boom})
	if !errors.Is(err, boom) {
		t.Fatalf("原本那条失败查不下去了：%v", err)
	}
	if len(result.Pruned) != 0 {
		t.Fatalf("一条都没落地却记了 %d 条", len(result.Pruned))
	}

	// 第二条上才失败时，第一条那笔账要交出来。
	second := newLive(t)
	toolResultAt(t, second, "call-1", longText())
	toolResultAt(t, second, "call-2", longText())

	partial, err := smallPruner(t).PruneSession(second, &countingEstimator{limit: 1, err: boom})
	if !errors.Is(err, boom) {
		t.Fatalf("原本那条失败查不下去了：%v", err)
	}
	if len(partial.Pruned) != 1 || partial.CharsRemoved <= 0 {
		t.Fatalf("已经落地的那一条丢了：%+v", partial)
	}
}

// countingEstimator 是一台报够 limit 次之后就失败的假计量器。
type countingEstimator struct {
	limit int
	calls int
	err   error
}

func (e *countingEstimator) EstimateMessage(llm.Message) (int, error) {
	e.calls++
	if e.calls > e.limit {
		return 0, e.err
	}
	return 10, nil
}

func TestPruneSession空会话一个字都不动(t *testing.T) {
	t.Parallel()

	live := newLive(t)

	result, err := smallPruner(t).PruneSession(live, flatEstimator(10))
	if err != nil {
		t.Fatalf("这一趟该成：%v", err)
	}
	if len(result.Pruned) != 0 || live.Seq() != 0 {
		t.Fatalf("动了 %+v", result)
	}
}

func TestPruneSession在一次事件通告里写不进去(t *testing.T) {
	t.Parallel()

	// 一个挂在 [coresession.Store.OnEvent] 上的观察者是在**追加还没发布完**的
	// 时候被叫到的，那会儿这段会话不接受任何新的追加。从这里发起砍一遍，
	// 连那条计价事件都写不进去——而它必须和替换件严格相邻，所以只能整趟失败。
	ctx := context.Background()
	owner := scope.NewRoot()
	t.Cleanup(func() { _ = owner.Dispose(ctx) })

	store, err := coresession.NewStore(coresession.StoreOptions{})
	if err != nil {
		t.Fatalf("造 store 失败：%v", err)
	}
	live, err := store.Prepare("s-observed", coresession.CreateOptions{WorkspaceID: pruneWorkspaceID})
	if err != nil {
		t.Fatalf("备不出会话：%v", err)
	}
	detach, err := store.Enter(owner, live)
	if err != nil {
		t.Fatalf("进不去：%v", err)
	}
	t.Cleanup(func() { _ = detach(ctx) })

	armed := false
	var failure error
	if _, err := store.OnEvent(ctx, owner, func(_ *coresession.Session, _ sessionlog.Event) {
		if !armed {
			return
		}
		armed = false
		_, failure = smallPruner(t).PruneSession(live, flatEstimator(10))
	}); err != nil {
		t.Fatalf("挂不上观察者：%v", err)
	}

	armed = true
	toolResultAt(t, live, "call-1", longText())

	if !errors.Is(failure, coresession.ErrInvalidAppend) {
		t.Fatalf("报的是 %v", failure)
	}
	if !strings.Contains(failure.Error(), "写不进日志") {
		t.Fatalf("诊断是 %v", failure)
	}
}
