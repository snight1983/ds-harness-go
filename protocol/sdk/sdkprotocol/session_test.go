// 本文件的作用：把会话那十个方法的线上形状钉住——每一件事的入参和结果各走一趟
// 编解码，并单独钉住那几个「缺席和零值是两件事」的字段。
//
// # 这些测试防的是什么错
//
//   - **可选整数被折成零值**。分叉点、翻页起点两处都是 `*int`：没给这个字段和
//     给 0 在语义上是两件事，用 `int` 表达前者会让「从第 0 条分」变成「从最后
//     一个回合分」，翻页那一处则会把整页吃掉。
//   - **推理档位的缺席被折成空串**。`session/select-model` 的入参里它是「不选档位」，
//     换成空串会让服务端以为客户端明说了一个档位、只是那个档位是空的。
//   - **改队的三支靠「哪个字段给了」判别**。那条路上排队改动是一个显式的标签，
//     一个认不出的标签必须在解码之后原样留着，好让服务端当场拒——静默折成
//     某一支会让一次拼错的调用悄悄改掉队里的东西。
//   - **没有标题和空标题混成一件事**。列表那一行上它们由两个字段分开表达，
//     `omitempty` 只能省掉前者的文本，不能省掉那个布尔。
//   - **方法名漂**。这十个常量是线上的契约，改一个字对面就调不到了。

package sdkprotocol

import (
	"encoding/json"
	"testing"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// TestSessionMethodNames 把这十个方法名钉死。
//
// 它们是线上的契约：拼写变了对面就调不到，而那种失败在编译期一点痕迹都没有。
func TestSessionMethodNames(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		"create":       MethodSessionCreate,
		"resume":       MethodSessionResume,
		"fork":         MethodSessionFork,
		"rename":       MethodSessionRename,
		"list":         MethodSessionList,
		"search":       MethodSessionSearch,
		"cancel":       MethodSessionCancel,
		"select-model": MethodSessionSelectModel,
		"update-queue": MethodSessionUpdateQueue,
		"history":      MethodSessionHistory,
	}
	for suffix, method := range want {
		if got := "session/" + suffix; got != method {
			t.Errorf("方法名不对：想要 %s，实际 %s", got, method)
		}
	}
	if len(want) != 10 {
		t.Fatalf("这一套是十个方法，实际数出 %d 个", len(want))
	}
}

// roundTrip 把一个值排成 JSON 再解回来，交回解回来的那一个和中间那段字节。
func roundTrip[V any](t *testing.T, value V) (V, string) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	var decoded V
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("解不回来：%v", err)
	}
	return decoded, string(encoded)
}

// ---- 开一条、接着跑一条 ----

func TestSessionCreateCodec(t *testing.T) {
	t.Parallel()
	decoded, encoded := roundTrip(t, SessionCreateParams{SessionID: "s-1"})
	if decoded.SessionID != "s-1" {
		t.Fatalf("会话标识没穿过去：%s", decoded.SessionID)
	}
	// 让服务端起名那一支在线上是这个字段整个缺席，不是一个空串。
	_, empty := roundTrip(t, SessionCreateParams{})
	if empty != "{}" {
		t.Fatalf("不给会话标识时该是空对象，实际 %s", empty)
	}
	if result, _ := roundTrip(t, SessionCreateResult{SessionID: "s-1"}); result.SessionID != "s-1" {
		t.Fatalf("结果里的会话标识没穿过去：%s", result.SessionID)
	}
	if encoded == "" {
		t.Fatal("排出来的不该是空的")
	}
}

func TestSessionResumeCodec(t *testing.T) {
	t.Parallel()
	decoded, _ := roundTrip(t, SessionResumeParams{SessionID: "s-1"})
	if decoded.SessionID != "s-1" {
		t.Fatalf("会话标识没穿过去：%s", decoded.SessionID)
	}
	if result, _ := roundTrip(t, SessionResumeResult{SessionID: "s-1"}); result.SessionID != "s-1" {
		t.Fatalf("结果里的会话标识没穿过去：%s", result.SessionID)
	}
}

// ---- 分出来 ----

func TestSessionForkCodec(t *testing.T) {
	t.Parallel()
	at := 7
	decoded, _ := roundTrip(t, SessionForkParams{
		SessionID: "s-1", AtSeq: &at, ForkSessionID: "s-2",
	})
	if decoded.AtSeq == nil || *decoded.AtSeq != 7 {
		t.Fatalf("分叉点没穿过去：%v", decoded.AtSeq)
	}
	if decoded.ForkSessionID != "s-2" {
		t.Fatalf("新会话标识没穿过去：%s", decoded.ForkSessionID)
	}
	if result, _ := roundTrip(t, SessionForkResult{SessionID: "s-2"}); result.SessionID != "s-2" {
		t.Fatalf("结果里的会话标识没穿过去：%s", result.SessionID)
	}
}

// TestSessionForkKeepsAbsentAtSeqDistinctFromZero 钉住「没给分叉点」和「从第 0 条分」
// 是两件事。
//
// 折成同一件事的话，一次「从头分」的调用会静静地变成「从最后一个收了尾的回合分」
// ——分出来的会话带着整段历史，而调用方以为它是空的。
func TestSessionForkKeepsAbsentAtSeqDistinctFromZero(t *testing.T) {
	t.Parallel()
	absent, encoded := roundTrip(t, SessionForkParams{SessionID: "s-1"})
	if absent.AtSeq != nil {
		t.Fatalf("没给分叉点时它该是缺席的，实际 %v", *absent.AtSeq)
	}
	if encoded != `{"sessionId":"s-1"}` {
		t.Fatalf("缺席的分叉点不该出现在线上：%s", encoded)
	}
	zero := 0
	given, _ := roundTrip(t, SessionForkParams{SessionID: "s-1", AtSeq: &zero})
	if given.AtSeq == nil || *given.AtSeq != 0 {
		t.Fatalf("给了 0 的分叉点该穿过去：%v", given.AtSeq)
	}
}

// ---- 改名 ----

func TestSessionRenameCodec(t *testing.T) {
	t.Parallel()
	decoded, _ := roundTrip(t, SessionRenameParams{SessionID: "s-1", Title: "换个名字"})
	if decoded.Title != "换个名字" {
		t.Fatalf("标题没穿过去：%s", decoded.Title)
	}
	result, _ := roundTrip(t, SessionRenameResult{Title: "换个名字", EventSeq: 12, UpdatedAt: 1700})
	if result.Title != "换个名字" || result.EventSeq != 12 || result.UpdatedAt != 1700 {
		t.Fatalf("改名的回执没穿过去：%+v", result)
	}
}

// ---- 列出来 ----

func TestSessionListCodec(t *testing.T) {
	t.Parallel()
	// 空入参在线上是一个空对象，不是 null——服务端那边把缺席也当空对象看，
	// 但排出去的这一份得是显式的。
	if _, encoded := roundTrip(t, SessionListParams{}); encoded != "{}" {
		t.Fatalf("空入参该排成空对象，实际 %s", encoded)
	}
	result, _ := roundTrip(t, SessionListResult{Sessions: []SessionSummary{{
		SessionID:     "s-2",
		Title:         "有名字",
		Titled:        true,
		CreatedAt:     1700,
		ParentSession: "s-1",
		Live:          true,
		Persisted:     true,
	}}})
	if len(result.Sessions) != 1 {
		t.Fatalf("该有一行，实际 %d 行", len(result.Sessions))
	}
	got := result.Sessions[0]
	if got.SessionID != "s-2" || got.ParentSession != "s-1" || !got.Live || !got.Persisted {
		t.Fatalf("列表那一行没穿过去：%+v", got)
	}
}

// TestSessionSummaryKeepsUntitledDistinctFromEmptyTitle 钉住「没有标题」和「标题是
// 空串」在线上分得开。
//
// 分不开的话，一条从来没被命名的会话和一条被显式改成空标题的会话在界面上长得一样，
// 而后者是人的选择、前者是待办。
func TestSessionSummaryKeepsUntitledDistinctFromEmptyTitle(t *testing.T) {
	t.Parallel()
	untitled, encoded := roundTrip(t, SessionSummary{SessionID: "s-1"})
	if untitled.Titled {
		t.Fatal("没有标题的会话不该说自己有标题")
	}
	if encoded != `{"sessionId":"s-1","titled":false,"createdAt":0,"live":false,"persisted":false}` {
		t.Fatalf("没有标题时那个文本字段该省掉：%s", encoded)
	}
	empty, _ := roundTrip(t, SessionSummary{SessionID: "s-1", Titled: true})
	if !empty.Titled || empty.Title != "" {
		t.Fatalf("空标题该是「有标题、文本为空」：%+v", empty)
	}
}

// ---- 找 ----

func TestSessionSearchCodec(t *testing.T) {
	t.Parallel()
	decoded, _ := roundTrip(t, SessionSearchParams{Query: "找这段", Limit: 20, Cursor: "c-1"})
	if decoded.Query != "找这段" || decoded.Limit != 20 || decoded.Cursor != "c-1" {
		t.Fatalf("检索入参没穿过去：%+v", decoded)
	}
	result, _ := roundTrip(t, SessionSearchResult{
		Hits: []SessionSearchHit{{
			Session: SessionSummary{SessionID: "s-1"},
			Seq:     3,
			Snippet: "命中的那一小段",
		}},
		NextCursor: "c-2",
	})
	if len(result.Hits) != 1 || result.Hits[0].Seq != 3 || result.NextCursor != "c-2" {
		t.Fatalf("检索结果没穿过去：%+v", result)
	}
	// 最后一页的续页令牌整个缺席，不是一个空串。
	if _, encoded := roundTrip(t, SessionSearchResult{Hits: []SessionSearchHit{}}); encoded != `{"hits":[]}` {
		t.Fatalf("最后一页不该带续页令牌：%s", encoded)
	}
}

// ---- 叫停 ----

func TestSessionCancelCodec(t *testing.T) {
	t.Parallel()
	decoded, _ := roundTrip(t, SessionCancelParams{SessionID: "s-1", KeepQueue: true})
	if !decoded.KeepQueue {
		t.Fatal("留着排队那一支没穿过去")
	}
	// 结果在协议上写死是空对象。
	if _, encoded := roundTrip(t, SessionCancelResult{}); encoded != "{}" {
		t.Fatalf("叫停的回执该是空对象，实际 %s", encoded)
	}
}

// ---- 换模型 ----

func TestSessionSelectModelCodec(t *testing.T) {
	t.Parallel()
	effort := llm.ReasoningEffortID("high")
	decoded, _ := roundTrip(t, SessionSelectModelParams{
		SessionID: "s-1", Provider: "p", Model: "m", ReasoningEffort: &effort,
	})
	if decoded.ReasoningEffort == nil || *decoded.ReasoningEffort != "high" {
		t.Fatalf("推理档位没穿过去：%v", decoded.ReasoningEffort)
	}
	result, _ := roundTrip(t, SessionSelectModelResult{
		Provider: "p", Model: "m", ReasoningEffort: "high",
	})
	if result.Provider != "p" || result.Model != "m" || result.ReasoningEffort != "high" {
		t.Fatalf("换模型的结果没穿过去：%+v", result)
	}
}

// TestSessionSelectModelKeepsAbsentEffortDistinctFromEmpty 钉住「不选档位」在入参里
// 是这个字段整个缺席。
//
// 折成空串的话，服务端收到的是「客户端明说了一个档位，而那个档位是空的」，那是一份
// 解不开的调用配置——但它和「不选」在结构上长得一模一样，于是那条错只能在很后面
// 才现形。
func TestSessionSelectModelKeepsAbsentEffortDistinctFromEmpty(t *testing.T) {
	t.Parallel()
	absent, encoded := roundTrip(t, SessionSelectModelParams{
		SessionID: "s-1", Provider: "p", Model: "m",
	})
	if absent.ReasoningEffort != nil {
		t.Fatalf("不选档位时它该是缺席的，实际 %q", *absent.ReasoningEffort)
	}
	if encoded != `{"sessionId":"s-1","provider":"p","model":"m"}` {
		t.Fatalf("缺席的档位不该出现在线上：%s", encoded)
	}
	empty := llm.ReasoningEffortID("")
	given, _ := roundTrip(t, SessionSelectModelParams{
		SessionID: "s-1", Provider: "p", Model: "m", ReasoningEffort: &empty,
	})
	if given.ReasoningEffort == nil {
		t.Fatal("显式给了一个空档位该穿过去，而不是被折成缺席")
	}
}

// ---- 改排队 ----

func TestSessionUpdateQueueCodec(t *testing.T) {
	t.Parallel()
	for _, op := range []QueueOp{QueueRemove, QueueReplace, QueuePrepend} {
		decoded, _ := roundTrip(t, SessionUpdateQueueParams{
			SessionID:     "s-1",
			Op:            op,
			MessageID:     "m-1",
			ContentBlocks: PromptContent{{Durable: llm.TextBlock{Text: "换成这句"}}},
		})
		if decoded.Op != op {
			t.Fatalf("改动的种类没穿过去：想要 %s，实际 %s", op, decoded.Op)
		}
		if decoded.MessageID != "m-1" {
			t.Fatalf("消息标识没穿过去：%s", decoded.MessageID)
		}
		if len(decoded.ContentBlocks) != 1 {
			t.Fatalf("内容没穿过去：%+v", decoded.ContentBlocks)
		}
	}
	result, _ := roundTrip(t, SessionUpdateQueueResult{
		NextTurn: []llm.MessageID{"m-1"},
		NextStep: []llm.MessageID{},
	})
	if len(result.NextTurn) != 1 || result.NextTurn[0] != "m-1" {
		t.Fatalf("改完之后那条队没穿过去：%+v", result)
	}
}

// TestSessionUpdateQueueKeepsAnUnknownOpIntact 钉住一个认不出的标签解完还在。
//
// 静默折成某一支会让一次拼错的调用悄悄改掉队里的东西；留着它，服务端才拒得了。
func TestSessionUpdateQueueKeepsAnUnknownOpIntact(t *testing.T) {
	t.Parallel()
	var decoded SessionUpdateQueueParams
	if err := json.Unmarshal([]byte(`{"sessionId":"s-1","op":"删掉"}`), &decoded); err != nil {
		t.Fatalf("解不回来：%v", err)
	}
	if decoded.Op != QueueOp("删掉") {
		t.Fatalf("认不出的标签该原样留着，实际 %q", decoded.Op)
	}
}

// ---- 翻历史 ----

func TestSessionHistoryCodec(t *testing.T) {
	t.Parallel()
	before := 30
	decoded, _ := roundTrip(t, SessionHistoryParams{
		SessionID: "s-1", BeforeSeq: &before, MaxMessages: 10,
	})
	if decoded.BeforeSeq == nil || *decoded.BeforeSeq != 30 {
		t.Fatalf("翻页起点没穿过去：%v", decoded.BeforeSeq)
	}
	if decoded.MaxMessages != 10 {
		t.Fatalf("一页装几条没穿过去：%d", decoded.MaxMessages)
	}
	result, _ := roundTrip(t, SessionHistoryResult{
		Session: sessionlog.SessionHeader{Version: 1, ID: "s-1", CreatedAt: 1700},
		Events:  []sessionlog.Event{},
		HasMore: true,
	})
	if result.Session.ID != "s-1" || !result.HasMore {
		t.Fatalf("一页历史没穿过去：%+v", result)
	}
}

// TestSessionHistoryKeepsAbsentBeforeSeqDistinctFromZero 钉住「从最新那头翻起」和
// 「翻第 0 条之前」是两件事。
//
// 后者是空的一页；折成同一件事会让第一次翻页什么都取不回来。
func TestSessionHistoryKeepsAbsentBeforeSeqDistinctFromZero(t *testing.T) {
	t.Parallel()
	absent, encoded := roundTrip(t, SessionHistoryParams{SessionID: "s-1"})
	if absent.BeforeSeq != nil {
		t.Fatalf("没给起点时它该是缺席的，实际 %v", *absent.BeforeSeq)
	}
	if encoded != `{"sessionId":"s-1"}` {
		t.Fatalf("缺席的起点不该出现在线上：%s", encoded)
	}
	zero := 0
	given, _ := roundTrip(t, SessionHistoryParams{SessionID: "s-1", BeforeSeq: &zero})
	if given.BeforeSeq == nil || *given.BeforeSeq != 0 {
		t.Fatalf("给了 0 的起点该穿过去：%v", given.BeforeSeq)
	}
}
