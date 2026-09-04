// 本文件的作用：本包测试共用的那几样——每条用例一份全新介质、一份一回合的日志，
// 以及绕过这一层直接动介质的那几下。
//
// 这一轮跑在哪种库上由 [dbtest] 定：缺省 SQLite，设了 DSH_POSTGRES_DSN 就整批改跑
// Postgres。所以本包这批用例任何时候都跑得起来，不再有「没有 DSN 就整批跳过」
// 那个洞——理由见那个包的包文档。
//
// 不拿 sqlmock 之类的东西把行数刷上去：那验的是「我拼出了我以为我会拼的那句
// SQL」，而这个包里真正会出事的地方（落地与第一批的原子性、可重复读下的令牌
// 一致性、主键冲突）恰恰是假库看不见的。

package sessionstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/snight1983/ds-harness-go/adapter/datastore"
	"github.com/snight1983/ds-harness-go/adapter/datastore/internal/dbtest"
	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Run(m)) }

// freshMedium 取出连接串，再要一个本次用例专用的命名空间名。
//
// 分成两步交出来，是因为「同一份介质重开一次」那几条用例得记住这两个值。
func freshMedium(t *testing.T) (dsn, namespace string) {
	t.Helper()

	dsn = dbtest.DSN()
	return dsn, dbtest.Namespace(t, "sess_test", dsn)
}

// mediumConfig 拼出一份指向这次那个命名空间的介质配置，连接池是新的一条。
func mediumConfig(t *testing.T, dsn, namespace string) (datastore.Config, *sql.DB) {
	t.Helper()

	return dbtest.Config(t, dsn, namespace)
}

// openBackend 在指定介质上开一个后端，成功的话登记好收尾。
//
// 开不出来是**返回**而不是当场 t.Fatal：有用例压的正是「这份介质开不得」。
func openBackend(t *testing.T, dsn, namespace string) (*Backend, error) {
	t.Helper()

	config, db := mediumConfig(t, dsn, namespace)
	backend, err := NewBackend(t.Context(), Config{Medium: config})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	t.Cleanup(func() {
		if closeErr := backend.Close(context.Background()); closeErr != nil {
			t.Errorf("关后端不该失败：%v", closeErr)
		}
	})
	return backend, nil
}

// newBackend 在一份全新介质上开一个后端，测试结束时收掉。
func newBackend(t *testing.T) *Backend {
	t.Helper()

	dsn, namespace := freshMedium(t)
	backend, err := openBackend(t, dsn, namespace)
	if err != nil {
		t.Fatalf("开后端失败：%v", err)
	}
	return backend
}

// newStore 在一份全新介质上装一整套存储出来。
func newStore(t *testing.T) *Store {
	t.Helper()

	dsn, namespace := freshMedium(t)
	config, db := mediumConfig(t, dsn, namespace)

	sessions, err := coresession.NewStore(coresession.StoreOptions{})
	if err != nil {
		_ = db.Close()
		t.Fatalf("造活会话表失败：%v", err)
	}
	store, err := New(t.Context(), Deps{Sessions: sessions}, Config{Medium: config})
	if err != nil {
		_ = db.Close()
		t.Fatalf("造存储失败：%v", err)
	}
	t.Cleanup(func() {
		if closeErr := store.Backend().Close(context.Background()); closeErr != nil {
			t.Errorf("关后端不该失败：%v", closeErr)
		}
	})
	return store
}

// testMeta 造一份版本正确的存储头。
func testMeta(id sessionlog.SessionID) sessionlog.SessionHeader {
	return sessionlog.SessionHeader{
		Version:   sessionlog.FormatVersion,
		ID:        id,
		CreatedAt: 1000,
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

// oneTurnLog 造一份从 base 起连续、回合已经关掉的日志，一共六条。
//
// 起点是参数而不是写死的 0：这份日志的头会被弹掉（见 docs/session-log-limit.md），
// 所以「一份存档从哪个 seq 起」是个变量，用例得摆得出非零的那一种。
func oneTurnLog(t testing.TB, base int) []sessionlog.Event {
	t.Helper()

	return []sessionlog.Event{
		turnStartEvent(t, base, 1),
		userMessageEvent(t, base+1, "hi"),
		stepStartEvent(t, base+2, 1, 1),
		assistantMessageEvent(t, base+3, 1, 1, "hello"),
		stepEndEvent(t, base+4, 1, 1),
		turnEndEvent(t, base+5, 1),
	}
}

func turnStartEvent(t testing.TB, seq, turn int) sessionlog.Event {
	t.Helper()

	return sessionlog.Event{
		Type: sessionlog.EventTurnStart, Seq: seq, Time: int64(seq + 1),
		Data: marshalData(t, "回合开始", sessionlog.TurnStartData{Turn: turn}),
	}
}

func turnEndEvent(t testing.TB, seq, turn int) sessionlog.Event {
	t.Helper()

	return sessionlog.Event{
		Type: sessionlog.EventTurnEnd, Seq: seq, Time: int64(seq + 1),
		Data: marshalData(t, "回合结束", sessionlog.TurnEndData{
			Turn: turn, Reason: sessionlog.CompletedTurnEnd{},
		}),
	}
}

func stepStartEvent(t testing.TB, seq, turn, step int) sessionlog.Event {
	t.Helper()

	return sessionlog.Event{
		Type: sessionlog.EventStepStart, Seq: seq, Time: int64(seq + 1),
		Data: marshalData(t, "步骤开始", sessionlog.StepStartData{Turn: turn, Step: step}),
	}
}

func stepEndEvent(t testing.TB, seq, turn, step int) sessionlog.Event {
	t.Helper()

	return sessionlog.Event{
		Type: sessionlog.EventStepEnd, Seq: seq, Time: int64(seq + 1),
		Data: marshalData(t, "步骤结束", sessionlog.StepEndData{Turn: turn, Step: step}),
	}
}

func userMessageEvent(t testing.TB, seq int, text string) sessionlog.Event {
	t.Helper()

	return sessionlog.Event{
		Type: sessionlog.EventUserMessage, Seq: seq, Time: int64(seq + 1),
		Data: marshalData(t, "用户消息", sessionlog.UserMessageData{Message: llm.Message{
			ID:      llm.MessageID("one-turn-user"),
			Role:    llm.RoleUser,
			Content: llm.Content{llm.TextBlock{Text: text}},
			Source:  llm.UserSource{},
		}}),
		SurfaceOp: sessionlog.AppendOp{},
	}
}

func assistantMessageEvent(t testing.TB, seq, turn, step int, text string) sessionlog.Event {
	t.Helper()

	return sessionlog.Event{
		Type: sessionlog.EventAssistantMessage, Seq: seq, Time: int64(seq + 1),
		Data: marshalData(t, "助手消息", sessionlog.AssistantMessageData{
			Turn: turn, Step: step,
			Message: llm.Message{
				ID:      llm.MessageID("one-turn-assistant"),
				Role:    llm.RoleAssistant,
				Content: llm.Content{llm.TextBlock{Text: text}},
				Source:  llm.ModelSource{Provenance: llm.Provenance{Provider: "mock", Model: "mock"}},
			},
		}),
		SurfaceOp: sessionlog.AppendOp{},
	}
}

// seqsOf 把一串事件的 seq 抽出来，好和期望的那串比。
func seqsOf(events []sessionlog.Event) []int {
	seqs := make([]int, 0, len(events))
	for _, event := range events {
		seqs = append(seqs, event.Seq)
	}
	return seqs
}

// idsOf 把一串头的会话标识抽成字符串并排好序，好和期望的那串比。
func idsOf(headers []sessionlog.SessionHeader) []string {
	ids := make([]string, 0, len(headers))
	for _, header := range headers {
		ids = append(ids, string(header.ID))
	}
	slices.Sort(ids)
	return ids
}

// mustCreate 建一个会话，失败就当场停。
func mustCreate(t *testing.T, store *Store, meta sessionlog.SessionHeader) {
	t.Helper()

	if err := store.Create(t.Context(), meta); err != nil {
		t.Fatalf("建会话 %q 失败：%v", string(meta.ID), err)
	}
}

// mustAppend 追加一批事件，失败就当场停。
func mustAppend(t *testing.T, store *Store, id sessionlog.SessionID, events []sessionlog.Event) {
	t.Helper()

	if err := store.Append(t.Context(), id, events); err != nil {
		t.Fatalf("往 %q 追加失败：%v", string(id), err)
	}
}

// evictHead 绕过 [Backend.TrimBefore] 直接从下面那一层弹掉最老的那几条——
// 那正是 FIFO 弹出留下的样子。
//
// 走下面那一层而不是走本包的 TrimBefore，是为了让「读的一侧答不答得出非零起点」
// 这件事不依赖本包自己那条弹出路径正不正确。
func evictHead(t *testing.T, backend *Backend, id sessionlog.SessionID, belowSeq int) {
	t.Helper()

	if err := backend.log.TrimBefore(t.Context(), string(id), int64(belowSeq)); err != nil {
		t.Fatalf("弹出 %q 的头失败：%v", string(id), err)
	}
}

// writeRaw 绕过本包直接往介质上写一条流，头和负载都是给什么写什么。
//
// 「介质被人绕过本包改过」这件事只能这么造出来：本包写下去的一定解得回来。
func writeRaw(
	t *testing.T, backend *Backend, id sessionlog.SessionID, head json.RawMessage, entries []datastore.Entry,
) {
	t.Helper()

	if err := backend.log.Append(t.Context(), datastore.AppendRequest{
		Stream: string(id), Head: head, EnsureStream: true, Entries: entries,
	}); err != nil {
		t.Fatalf("直接往介质上写 %q 失败：%v", string(id), err)
	}
}
