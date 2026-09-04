package agent

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// TestEventTypes 确认本包只往会话日志里写那一种事件。
func TestEventTypes(t *testing.T) {
	types := EventTypes()
	if len(types) != 1 || types[0] != EventInboxSpliced {
		t.Fatalf("事件类型清单不对：%v", types)
	}
	if EventInboxSpliced != sessionlog.EventType("agent/inbox/spliced") {
		t.Fatalf("事件名不对：%q", EventInboxSpliced)
	}
}

// TestSplicedDataMarshalOmits 守住那两处 omitempty：RemovedCount 为 0 时
// removedCount 整个不出现，Canceled 为假时 outcome 整个不出现。这两条精确复刻
// DSH 那两个条件展开，改动会让回放的一方读到一份形状不同的日志。
func TestSplicedDataMarshalOmits(t *testing.T) {
	encoded, err := json.Marshal(SplicedData{Target: NextTurn})
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	got := string(encoded)
	want := `{"target":"next-turn","start":0,"inserted":[]}`
	if got != want {
		t.Fatalf("排出来的形状不对：\n得到 %s\n想要 %s", got, want)
	}
}

// TestSplicedDataMarshalNilInsertedIsArray 守住那条「nil 切片不能排成 null」。
// DSH 那边 inserted 是必填数组，排成 null 会让回放的一方读到一个不是数组的值。
func TestSplicedDataMarshalNilInsertedIsArray(t *testing.T) {
	encoded, err := json.Marshal(SplicedData{Target: NextStep, Start: 1, RemovedCount: 2, Canceled: true})
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	var wire struct {
		Inserted []llm.Message `json:"inserted"`
		Outcome  string        `json:"outcome"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("读不回来：%v", err)
	}
	if wire.Inserted == nil {
		t.Fatalf("inserted 排成了 null：%s", encoded)
	}
	if wire.Outcome != "canceled" {
		t.Fatalf("outcome 该是 canceled，得到 %q", wire.Outcome)
	}
}

// TestSplicedDataMarshalRejectsUnknownTarget 确认排出去的一侧也守清单名。
func TestSplicedDataMarshalRejectsUnknownTarget(t *testing.T) {
	_, err := json.Marshal(SplicedData{Target: "next-era"})
	if !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("该报 ErrMalformedEvent，得到 %v", err)
	}
}

// TestSplicedDataRoundTrip 走一遍完整的往返。
func TestSplicedDataRoundTrip(t *testing.T) {
	message := text("嗨")
	original := SplicedData{
		Target:       NextStep,
		Start:        2,
		RemovedCount: 1,
		Inserted:     []llm.Message{message},
		Canceled:     true,
	}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	var restored SplicedData
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatalf("读不回来：%v", err)
	}
	if restored.Target != original.Target || restored.Start != original.Start ||
		restored.RemovedCount != original.RemovedCount || !restored.Canceled {
		t.Fatalf("往返之后对不上：%+v", restored)
	}
	if len(restored.Inserted) != 1 || restored.Inserted[0].ID != message.ID {
		t.Fatalf("插入的那条消息没回来：%+v", restored.Inserted)
	}
}

// TestSplicedDataUnmarshalRejects 逐条走读进来那一侧的三个拒绝面。
//
// 后两个是本仓库比 DSH 多验的：一个不认识的清单名会让投影往一条不存在的清单上写，
// 一个不认识的结局会把被丢掉的活儿静默记成已完成。
func TestSplicedDataUnmarshalRejects(t *testing.T) {
	// 第一条给的是一段**语法合法但形状不对**的 JSON：语法本身坏掉的字节由
	// encoding/json 在调到 UnmarshalJSON 之前就拒了，那条路走不到本包的包装。
	cases := map[string]string{
		"形状不对":   `[]`,
		"清单名不认识": `{"target":"next-era","start":0,"inserted":[]}`,
		"结局不认识":  `{"target":"next-turn","start":0,"inserted":[],"outcome":"vanished"}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			var restored SplicedData
			if err := json.Unmarshal([]byte(payload), &restored); !errors.Is(err, ErrMalformedEvent) {
				t.Fatalf("该报 ErrMalformedEvent，得到 %v", err)
			}
		})
	}
}
