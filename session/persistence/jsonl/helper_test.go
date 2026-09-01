// 本文件的作用：本包测试共用的那几个搭建器——一个落在临时目录上的存储、
// 一份一回合的日志、以及往盘上直接写字节的那几下。
//
// 源: packages/session/session-persistence-jsonl/tests/jsonl.spec.ts:76-100
// 源: packages/session/session-persistence/tests/contract.ts:24-58

package jsonl

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
)

// newTestStore 造一个落在一个全新临时根上的存储，并交回那个根。
func newTestStore(t testing.TB, config Config) (*Store, string) {
	t.Helper()

	if config.Root == "" {
		config.Root = filepath.Join(t.TempDir(), "sessions")
	}
	sessions, err := coresession.NewStore(coresession.StoreOptions{})
	if err != nil {
		t.Fatalf("造活会话表失败：%v", err)
	}
	store, err := New(Deps{Sessions: sessions}, config)
	if err != nil {
		t.Fatalf("造存储失败：%v", err)
	}
	return store, config.Root
}

// testCwd 把上游用例里那些 POSIX 写法的工作目录折成本平台上一个绝对路径。
//
// 新增: [session.SessionHeader.Cwd] 要求绝对路径，而 "/proj" 在 Windows 上
// 不是绝对路径——上游用例的字面量在这里过不了那道校验。
func testCwd(t testing.TB, path string) string {
	t.Helper()

	absolute, err := filepath.Abs(filepath.FromSlash(path))
	if err != nil {
		t.Fatalf("%q 折不成绝对路径：%v", path, err)
	}
	return absolute
}

// testMeta 造一份版本正确的存储头。
//
// 源: packages/session/session-persistence/tests/contract.ts:24-31
func testMeta(id session.SessionID, cwd string) session.SessionHeader {
	return session.SessionHeader{
		Version:   session.FormatVersion,
		ID:        id,
		CreatedAt: 1000,
		Cwd:       cwd,
	}
}

// oneTurnLog 造一份 seq 从零连续、回合已经关掉的日志：seq 0..5，turn/end 在 5。
//
// 源: packages/session/session-persistence/tests/contract.ts:34-58
func oneTurnLog(t testing.TB) []session.Event {
	t.Helper()

	return []session.Event{
		turnStartEvent(t, 0, 1, 1),
		userMessageEvent(t, 1, 2, "hi"),
		stepStartEvent(t, 2, 3, 1, 1),
		assistantMessageEvent(t, 3, 4, 1, 1, "hello"),
		stepEndEvent(t, 4, 5, 1, 1),
		turnEndEvent(t, 5, 6, 1),
	}
}

// marshalData 排一条事件负载，排不出去就当场失败。
func marshalData(t testing.TB, what string, data any) json.RawMessage {
	t.Helper()

	payload, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("%s负载排不出去：%v", what, err)
	}
	return payload
}

func turnStartEvent(t testing.TB, seq, at, turn int) session.Event {
	t.Helper()

	return session.Event{
		Type: session.EventTurnStart, Seq: seq, Time: int64(at),
		Data: marshalData(t, "回合开始", session.TurnStartData{Turn: turn}),
	}
}

func turnEndEvent(t testing.TB, seq, at, turn int) session.Event {
	t.Helper()

	return session.Event{
		Type: session.EventTurnEnd, Seq: seq, Time: int64(at),
		Data: marshalData(t, "回合结束", session.TurnEndData{
			Turn: turn, Reason: session.CompletedTurnEnd{},
		}),
	}
}

func stepStartEvent(t testing.TB, seq, at, turn, step int) session.Event {
	t.Helper()

	return session.Event{
		Type: session.EventStepStart, Seq: seq, Time: int64(at),
		Data: marshalData(t, "步骤开始", session.StepStartData{Turn: turn, Step: step}),
	}
}

func stepEndEvent(t testing.TB, seq, at, turn, step int) session.Event {
	t.Helper()

	return session.Event{
		Type: session.EventStepEnd, Seq: seq, Time: int64(at),
		Data: marshalData(t, "步骤结束", session.StepEndData{Turn: turn, Step: step}),
	}
}

func userMessageEvent(t testing.TB, seq, at int, text string) session.Event {
	t.Helper()

	return session.Event{
		Type: session.EventUserMessage, Seq: seq, Time: int64(at),
		Data: marshalData(t, "用户消息", session.UserMessageData{Message: llm.Message{
			ID:      llm.MessageID("one-turn-user"),
			Role:    llm.RoleUser,
			Content: llm.Content{llm.TextBlock{Text: text}},
			Source:  llm.UserSource{},
		}}),
		SurfaceOp: session.AppendOp{},
	}
}

func assistantMessageEvent(t testing.TB, seq, at, turn, step int, text string) session.Event {
	t.Helper()

	return session.Event{
		Type: session.EventAssistantMessage, Seq: seq, Time: int64(at),
		Data: marshalData(t, "助手消息", session.AssistantMessageData{
			Turn: turn, Step: step,
			Message: llm.Message{
				ID:      llm.MessageID("one-turn-assistant"),
				Role:    llm.RoleAssistant,
				Content: llm.Content{llm.TextBlock{Text: text}},
				Source:  llm.ModelSource{Provenance: llm.Provenance{Provider: "mock", Model: "mock"}},
			},
		}),
		SurfaceOp: session.AppendOp{},
	}
}

// storedPath 是某个会话那份明文存档在盘上的路径。
//
// 源: packages/session/session-persistence-jsonl/tests/jsonl.spec.ts:82-84
func storedPath(t testing.TB, root, cwd string, id session.SessionID) string {
	t.Helper()

	path, err := logPath(root, cwd, id, CompressionNone)
	if err != nil {
		t.Fatalf("拼不出 %q 的存档路径：%v", string(id), err)
	}
	return path
}

// appendRawBytes 绕过后端，直接往一份存档尾巴上贴字节——那正是一次崩溃留下的样子。
func appendRawBytes(t testing.TB, path string, content string) {
	t.Helper()

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("打不开 %q 往上贴字节：%v", path, err)
	}
	if _, err := file.WriteString(content); err != nil {
		t.Fatalf("往 %q 贴字节失败：%v", path, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("关不掉 %q：%v", path, err)
	}
}

// readStored 读出一份存档此刻的全部字节。
func readStored(t testing.TB, path string) string {
	t.Helper()

	buffer, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读不出 %q：%v", path, err)
	}
	return string(buffer)
}

// seqsOf 把一串事件的 seq 抽出来，好和期望的那串比。
func seqsOf(events []session.Event) []int {
	seqs := make([]int, 0, len(events))
	for _, event := range events {
		seqs = append(seqs, event.Seq)
	}
	return seqs
}

// mustCreate 建一个会话，失败就当场停。
func mustCreate(t testing.TB, store *Store, meta session.SessionHeader) {
	t.Helper()

	if err := store.Create(context.Background(), meta); err != nil {
		t.Fatalf("建会话 %q 失败：%v", string(meta.ID), err)
	}
}

// mustAppend 追加一批事件，失败就当场停。
func mustAppend(t testing.TB, store *Store, id session.SessionID, events []session.Event) {
	t.Helper()

	if err := store.Append(context.Background(), id, events); err != nil {
		t.Fatalf("往 %q 追加失败：%v", string(id), err)
	}
}
