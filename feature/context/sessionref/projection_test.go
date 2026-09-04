// 本文件的作用：验投影留下什么、丢掉什么，以及两轮裁剪的先后与那个精确的字节预算。

package sessionref

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/feature/sessionquery"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

func TestProject只留用户自己的话和助手的回答(t *testing.T) {
	snapshot := snapshotOf("s1", "ws-1", []sessionlog.Event{
		userEvent(t, 1, "用户说的"),
		injectedEvent(t, 2, "工作区说明，别的层注入的"),
		assistantEvent(t, 3, llm.TextBlock{Text: "助手答的"}),
		toolResultEvent(t, 4),
		checkpointEvent(t, 5, "这里之前的内容被压缩过"),
	})
	items, err := projectSessionConversation(snapshot)
	if err != nil {
		t.Fatalf("投影失败：%v", err)
	}
	if len(items) != 3 {
		t.Fatalf("留下 %d 条，要的是 3 条：%+v", len(items), items)
	}
	if items[0].Text != "用户说的" || items[1].Text != "助手答的" {
		t.Fatalf("留下的不对：%+v", items)
	}
	if !items[2].checkpoint {
		t.Fatal("检查点没被认出来")
	}
	if items[0].Role != llm.RoleUser || items[1].Role != llm.RoleAssistant {
		t.Fatalf("角色不对：%+v", items)
	}
}

func TestProject跳过没有可见文本的消息(t *testing.T) {
	snapshot := snapshotOf("s1", "", []sessionlog.Event{
		messageEvent(t, 1, llm.Message{
			ID: "u", Role: llm.RoleUser, Content: llm.Content{}, Source: llm.UserSource{},
		}),
		assistantEvent(t, 2, llm.ToolCallBlock{ID: "call-1", Name: "读文件"}),
	})
	items, err := projectSessionConversation(snapshot)
	if err != nil {
		t.Fatalf("投影失败：%v", err)
	}
	if len(items) != 0 {
		t.Fatalf("不该留下任何一条：%+v", items)
	}
}

func TestProject把一条消息里的多个文本块拼起来(t *testing.T) {
	snapshot := snapshotOf("s1", "", []sessionlog.Event{
		assistantEvent(t, 1, llm.TextBlock{Text: "上半"}, llm.TextBlock{Text: "下半"}),
	})
	items, err := projectSessionConversation(snapshot)
	if err != nil {
		t.Fatalf("投影失败：%v", err)
	}
	if len(items) != 1 || items[0].Text != "上半\n下半" {
		t.Fatalf("拼出来不对：%+v", items)
	}
}

func TestProject碰上读不回来的负载就报出来(t *testing.T) {
	// 一份坏掉的日志必须说出来，不能压成「这条没有可见文本」。
	snapshot := snapshotOf("s1", "", []sessionlog.Event{
		{Type: sessionlog.EventUserMessage, Seq: 1, Data: json.RawMessage(`{"role":123}`)},
	})
	_, err := projectSessionConversation(snapshot)
	if !errors.Is(err, CodeReadFailed) {
		t.Fatalf("应当报读失败，得到 %v", err)
	}
}

func TestProject碰上不该出现在表面上的事件就报出来(t *testing.T) {
	// 悄悄跳过会让引用内容凭空少一截，那是调用方喂错了东西。
	snapshot := snapshotOf("s1", "", []sessionlog.Event{
		{Type: sessionlog.EventTurnStart, Seq: 1, Data: json.RawMessage(`{"turn":1}`)},
	})
	_, err := projectSessionConversation(snapshot)
	if !errors.Is(err, CodeReadFailed) {
		t.Fatalf("应当报读失败，得到 %v", err)
	}
	if !strings.Contains(err.Error(), "不该出现在表面上") {
		t.Fatalf("错误里该说清为什么：%v", err)
	}
}

func TestRetain预算够时原样留下(t *testing.T) {
	snapshot := snapshotOf("s1", "ws-1", []sessionlog.Event{
		userEvent(t, 1, "一"),
		assistantEvent(t, 2, llm.TextBlock{Text: "二"}),
	})
	retained, fits, err := RetainReferencedSession(snapshot, "标签", 1<<20)
	if err != nil || !fits {
		t.Fatalf("应当塞得下：fits=%v err=%v", fits, err)
	}
	if retained.Stats.Truncated {
		t.Fatal("没裁过却说裁过了")
	}
	want := ReferenceRetentionStats{OriginalMessages: 2, RetainedMessages: 2}
	if retained.Stats != want {
		t.Fatalf("记账是 %+v，要的是 %+v", retained.Stats, want)
	}
	if retained.Data.SessionID != "s1" || retained.Data.Label != "标签" || retained.Data.WorkspaceID != "ws-1" {
		t.Fatalf("固定字段不对：%+v", retained.Data)
	}
	if retained.Data.CapturedThroughSeq != 2 || !retained.Data.CapturedAny {
		t.Fatalf("捕获点不对：%+v", retained.Data)
	}
}

func TestRetain第一轮从最旧的开始整条丢(t *testing.T) {
	events := []sessionlog.Event{
		userEvent(t, 1, strings.Repeat("旧", 40)),
		userEvent(t, 2, strings.Repeat("中", 40)),
		userEvent(t, 3, strings.Repeat("新", 40)),
	}
	snapshot := snapshotOf("s1", "", events)
	full, _, err := RetainReferencedSession(snapshot, "标签", 1<<20)
	if err != nil {
		t.Fatalf("量原始大小失败：%v", err)
	}
	fullBytes := renderedBytes(t, full.Data)

	// 预算刚好装不下三条，能装下两条。
	retained, fits, err := RetainReferencedSession(snapshot, "标签", fullBytes-1)
	if err != nil || !fits {
		t.Fatalf("应当塞得下：fits=%v err=%v", fits, err)
	}
	if retained.Stats.OmittedMessages != 1 || retained.Stats.RetainedMessages != 2 {
		t.Fatalf("丢的条数不对：%+v", retained.Stats)
	}
	if strings.Contains(retained.Data.Conversation[0].Text, "旧") {
		t.Fatalf("丢的不是最旧那条：%+v", retained.Data.Conversation)
	}
	// 丢掉的字节按原文计数。
	if retained.Stats.OmittedBytes != len(strings.Repeat("旧", 40)) {
		t.Fatalf("丢掉的字节数不对：%+v", retained.Stats)
	}
	if !retained.Stats.Truncated {
		t.Fatal("丢过消息却说没裁过")
	}
}

func TestRetain第一轮永远绕开检查点和最新那条(t *testing.T) {
	events := []sessionlog.Event{
		checkpointEvent(t, 1, strings.Repeat("检", 40)),
		userEvent(t, 2, strings.Repeat("中", 40)),
		userEvent(t, 3, strings.Repeat("新", 40)),
	}
	snapshot := snapshotOf("s1", "", events)
	full, _, err := RetainReferencedSession(snapshot, "标签", 1<<20)
	if err != nil {
		t.Fatalf("量原始大小失败：%v", err)
	}

	retained, fits, err := RetainReferencedSession(snapshot, "标签", renderedBytes(t, full.Data)-1)
	if err != nil || !fits {
		t.Fatalf("应当塞得下：fits=%v err=%v", fits, err)
	}
	if len(retained.Data.Conversation) != 2 {
		t.Fatalf("留下 %d 条：%+v", len(retained.Data.Conversation), retained.Data.Conversation)
	}
	// 丢掉的必须是中间那条：丢了检查点，读的人会以为这就是全部历史；
	// 丢了最新那条，引用就答不出「刚才在说什么」。
	if !strings.Contains(retained.Data.Conversation[0].Text, "检") {
		t.Fatalf("检查点被丢了：%+v", retained.Data.Conversation)
	}
	if !strings.Contains(retained.Data.Conversation[1].Text, "新") {
		t.Fatalf("最新那条被丢了：%+v", retained.Data.Conversation)
	}
	if !retained.Stats.Compacted {
		t.Fatal("出现过检查点却没记上")
	}
}

func TestRetain第二轮才去截断最长的那条(t *testing.T) {
	// 只有一条，第一轮无从下手（它既是最新的又是唯一的），只能走截断。
	long := strings.Repeat("长", 200)
	snapshot := snapshotOf("s1", "", []sessionlog.Event{userEvent(t, 1, long)})
	full, _, err := RetainReferencedSession(snapshot, "标签", 1<<20)
	if err != nil {
		t.Fatalf("量原始大小失败：%v", err)
	}
	budget := renderedBytes(t, full.Data) - 100

	retained, fits, err := RetainReferencedSession(snapshot, "标签", budget)
	if err != nil || !fits {
		t.Fatalf("应当塞得下：fits=%v err=%v", fits, err)
	}
	if retained.Stats.OmittedMessages != 0 {
		t.Fatalf("不该整条丢：%+v", retained.Stats)
	}
	if retained.Stats.OmittedBytes == 0 || !retained.Stats.Truncated {
		t.Fatalf("截断没记上：%+v", retained.Stats)
	}
	if !strings.Contains(retained.Data.Conversation[0].Text, "omitted") {
		t.Fatalf("截断处没留通知：%q", retained.Data.Conversation[0].Text)
	}
	// 预算是按排完的字节算的，必须真的塞进去了。
	if got := renderedBytes(t, retained.Data); got > budget {
		t.Fatalf("排出来 %d 字节，超过了预算 %d", got, budget)
	}
}

func TestRetain预算连固定字段都装不下时说塞不下(t *testing.T) {
	snapshot := snapshotOf("很长的会话名字很长的会话名字", "ws-一个很长的工作区标识", []sessionlog.Event{
		userEvent(t, 1, "一句话"),
	})
	_, fits, err := RetainReferencedSession(snapshot, "标签", 8)
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if fits {
		t.Fatal("这么小的预算不该说塞得下")
	}
}

func TestRetain一条消息都没有时给出空对话(t *testing.T) {
	snapshot := snapshotOf("s1", "", nil)
	retained, fits, err := RetainReferencedSession(snapshot, "标签", 1<<20)
	if err != nil || !fits {
		t.Fatalf("应当塞得下：fits=%v err=%v", fits, err)
	}
	if retained.Data.CapturedAny {
		t.Fatal("空日志不该说捕获过")
	}
	// conversation 是必填数组，排成 null 的话「一句话都没有」和「字段坏了」就一样了。
	encoded, err := json.Marshal(retained.Data)
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	if !strings.Contains(string(encoded), `"conversation":[]`) {
		t.Fatalf("空对话没排成空数组：%s", encoded)
	}
	if !strings.Contains(string(encoded), `"capturedThroughSeq":null`) {
		t.Fatalf("空日志的捕获点没排成 null：%s", encoded)
	}
	if !strings.Contains(string(encoded), `"workspaceId":null`) {
		t.Fatalf("不属于任何工作区时没排成 null：%s", encoded)
	}
}

func TestReferencedSessionData的nil对话排成空数组(t *testing.T) {
	// [RetainReferencedSession] 造出来的对话切片永远非 nil，但这份数据是导出的，
	// 别人也能自己拼一个出来。
	encoded, err := json.Marshal(ReferencedSessionData{SessionID: "s1", Label: "标签"})
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	if !strings.Contains(string(encoded), `"conversation":[]`) {
		t.Fatalf("nil 对话没排成空数组：%s", encoded)
	}
}

func TestRetain坏掉的日志一路报上来(t *testing.T) {
	snapshot := sessionquery.SurfaceSnapshot{
		Session: sessionlog.SessionHeader{ID: "s1"},
		Events:  []sessionlog.Event{{Type: sessionlog.EventUserMessage, Seq: 1, Data: json.RawMessage(`{"role":123}`)}},
	}
	if _, _, err := RetainReferencedSession(snapshot, "标签", 1<<20); !errors.Is(err, CodeReadFailed) {
		t.Fatalf("应当报读失败，得到 %v", err)
	}
}

func TestTruncateWithNotice够放时原样返回(t *testing.T) {
	text, omitted, err := truncateWithNotice("短", 1024)
	if err != nil {
		t.Fatalf("失败：%v", err)
	}
	if text != "短" || omitted != 0 {
		t.Fatalf("原样返回没做到：%q %d", text, omitted)
	}
}

func TestTruncateWithNotice掐头去尾并且恰好塞满(t *testing.T) {
	text := strings.Repeat("a", 200) + "结论在这里"
	for _, budget := range []int{60, 80, 120, 199} {
		got, omitted, err := truncateWithNotice(text, budget)
		if err != nil {
			t.Fatalf("预算 %d：失败 %v", budget, err)
		}
		if len(got) > budget {
			t.Fatalf("预算 %d：排出来 %d 字节", budget, len(got))
		}
		if !strings.Contains(got, "omitted") {
			t.Fatalf("预算 %d：没留通知 %q", budget, got)
		}
		if omitted <= 0 {
			t.Fatalf("预算 %d：丢弃字节数是 %d", budget, omitted)
		}
		// 掐头去尾：一段对话的结尾往往是结论，只留开头会让引用停在半路上。
		if !strings.HasPrefix(got, "a") {
			t.Fatalf("预算 %d：开头丢了 %q", budget, got)
		}
	}
}

func TestTruncateWithNotice预算小到连通知都放不下时交出空串(t *testing.T) {
	got, omitted, err := truncateWithNotice(strings.Repeat("a", 100), 1)
	if err != nil {
		t.Fatalf("失败：%v", err)
	}
	if got != "" {
		t.Fatalf("应当交出空串，得到 %q", got)
	}
	// best 的初值是「整段都丢了」，所以丢弃字节数是原文的长度。
	if omitted != 100 {
		t.Fatalf("丢弃字节数是 %d，要的是 100", omitted)
	}
}

func TestOmissionNotice是给模型看的英文(t *testing.T) {
	if got := omissionNotice(12); got != "\n[… omitted 12 UTF-8 bytes …]" {
		t.Fatalf("通知是 %q", got)
	}
}

func TestContentText只收文本块(t *testing.T) {
	got := contentText(llm.Content{
		llm.TextBlock{Text: "看得见的"},
		llm.ToolCallBlock{ID: "call-1", Name: "读文件"},
		llm.TextBlock{Text: "也看得见的"},
	})
	if got != "看得见的\n也看得见的" {
		t.Fatalf("拼出来是 %q", got)
	}
}

// renderedBytes 量一份数据排完之后有多少字节，和本包的预算口径一致。
func renderedBytes(t *testing.T, data ReferencedSessionData) int {
	t.Helper()

	serialized, err := stringifyTagSafeJSON(data)
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	return len(serialized)
}
