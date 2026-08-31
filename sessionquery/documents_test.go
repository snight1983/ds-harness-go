// 本文件的作用：一份原始日志投影成轻量记录与语义文档之后是什么样。
//
// 源: packages/session-query/session-query/src/documents.ts

package sessionquery

import (
	"encoding/json"
	"testing"

	"ds-harness-go/session"
)

func TestBuildEventRecordsClassifiesEverySurfacePosition(t *testing.T) {
	t.Parallel()

	events := append(replacementLog(t), plainEvent(t, session.EventTurnStart, 3, session.TurnStartData{Turn: 1}))

	records, err := BuildEventRecords("s1", events)
	if err != nil {
		t.Fatalf("投影不出记录：%v", err)
	}
	if len(records) != 4 {
		t.Fatalf("记录数不对：想要 4，实际 %d", len(records))
	}
	want := []EventSurface{SurfaceShadowed, SurfaceShadowed, SurfaceCurrent, SurfaceLogOnly}
	for index, record := range records {
		if record.SessionID != "s1" || record.Seq != index {
			t.Fatalf("第 %d 条记录的身份不对：%+v", index, record)
		}
		if record.Surface != want[index] {
			t.Fatalf("第 %d 条记录的表面位置不对：想要 %s，实际 %s", index, want[index], record.Surface)
		}
		if record.Type != events[index].Type || record.Time != events[index].Time {
			t.Fatalf("第 %d 条记录的类型或时间没跟上原事件：%+v", index, record)
		}
	}
}

func TestBuildEventRecordsRefusesALogWhoseSurfaceCannotFold(t *testing.T) {
	t.Parallel()

	events := []session.Event{replacingUserEvent(t, 0, "盖谁", 7, 7, 7)}

	_, err := BuildEventRecords("s1", events)
	requireCode(t, err, CodeInvalidSurface)
}

func TestBuildEventSearchDocumentsSkipsEventsWithoutSemanticText(t *testing.T) {
	t.Parallel()

	events := []session.Event{
		userEvent(t, 0, "第一句"),
		plainEvent(t, session.EventTurnStart, 1, session.TurnStartData{Turn: 1}),
		userEvent(t, 2, "第二句"),
	}

	documents, err := BuildEventSearchDocuments("s1", events)
	if err != nil {
		t.Fatalf("建不出文档：%v", err)
	}
	if len(documents) != 2 {
		t.Fatalf("文档数不对：想要 2，实际 %d", len(documents))
	}
	if documents[0].Seq != 0 || documents[0].Text != "第一句" {
		t.Fatalf("第一篇文档不对：%+v", documents[0])
	}
	if documents[1].Seq != 2 || documents[1].Text != "第二句" {
		t.Fatalf("第二篇文档不对：%+v", documents[1])
	}
}

func TestBuildEventSearchDocumentsRefusesABrokenPayload(t *testing.T) {
	t.Parallel()

	events := []session.Event{{
		Type:      session.EventUserMessage,
		Seq:       0,
		Data:      json.RawMessage(`{`),
		SurfaceOp: session.AppendOp{},
	}}

	_, err := BuildEventSearchDocuments("s1", events)
	requireCode(t, err, CodeCorruptSession)
}

func TestBuildEventSearchDocumentsRefusesALogWhoseSurfaceCannotFold(t *testing.T) {
	t.Parallel()

	events := []session.Event{replacingUserEvent(t, 0, "盖谁", 7, 7, 7)}

	_, err := BuildEventSearchDocuments("s1", events)
	requireCode(t, err, CodeInvalidSurface)
}
