// 本文件的作用：验四条 compaction/* 事件的负载在介质上排得出、读得回，
// 而且那几处「Go 的零值和 DSH 的 null／缺键不是同一件事」的地方没有把意思弄丢。

package compaction

import (
	"encoding/json"
	"errors"
	"testing"

	"ds-harness-go/llm"
	"ds-harness-go/session"
)

func TestEventTypes交出的是那四条(t *testing.T) {
	t.Parallel()

	want := []session.EventType{
		EventCompactionStart, EventCompactionSummary, EventCompactionEnd, EventCompactionPrune,
	}
	got := EventTypes()
	if len(got) != len(want) {
		t.Fatalf("交出来 %d 种", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 种是 %s，要的是 %s", i, got[i], want[i])
		}
	}

	// 装配方拼进词汇表之后，一段带压缩的日志才不会被整个拒掉。
	vocabulary := session.CoreVocabulary().With(EventTypes()...)
	for _, kind := range want {
		if !vocabulary.Knows(kind) {
			t.Fatalf("拼进去之后词汇表还是不认得 %s", kind)
		}
	}
}

func TestStartData往返(t *testing.T) {
	t.Parallel()

	for name, item := range map[string]struct {
		data     StartData
		wantTurn string
	}{
		"归属某个回合":  {StartData{CompactionID: "c-1", Turn: 3}, `3`},
		"独立事务":    {StartData{CompactionID: "c-1", Standalone: true}, `null`},
		"人工发起的":   {StartData{CompactionID: "c-1", SourceCommandID: "cmd-1", Turn: 1}, `1`},
		"回合号恰好是零": {StartData{CompactionID: "c-1", Turn: 0}, `0`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			raw, err := json.Marshal(item.data)
			if err != nil {
				t.Fatalf("排不出去：%v", err)
			}
			var wire map[string]json.RawMessage
			if err := json.Unmarshal(raw, &wire); err != nil {
				t.Fatalf("排出来的不是对象：%v", err)
			}
			// 回合号零和独立事务在介质上必须分得开——这正是拆成
			// Turn + Standalone 两个字段要守住的东西。
			if string(wire["turn"]) != item.wantTurn {
				t.Fatalf("turn 排成了 %s，要的是 %s", wire["turn"], item.wantTurn)
			}

			var back StartData
			if err := json.Unmarshal(raw, &back); err != nil {
				t.Fatalf("读不回来：%v", err)
			}
			if back != item.data {
				t.Fatalf("读回来的是 %+v，排出去的是 %+v", back, item.data)
			}
		})
	}
}

func TestStartData读不回来的几种形状(t *testing.T) {
	t.Parallel()

	for name, raw := range map[string]string{
		"整个不是对象":            `"start"`,
		"turn 这个键缺了":        `{"compactionId":"c-1"}`,
		"turn 既不是数也不是 null": `{"compactionId":"c-1","turn":"第三回合"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var data StartData
			if err := json.Unmarshal([]byte(raw), &data); !errors.Is(err, ErrMalformedEvent) {
				t.Fatalf("报的是 %v", err)
			}
		})
	}
}

func TestEndData往返(t *testing.T) {
	t.Parallel()

	for name, data := range map[string]EndData{
		"成功":     {CompactionID: "c-1", Turn: 2},
		"失败":     {CompactionID: "c-1", Turn: 2, Error: "上游超时"},
		"独立事务失败": {CompactionID: "c-1", Standalone: true, Error: "取消了"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			raw, err := json.Marshal(data)
			if err != nil {
				t.Fatalf("排不出去：%v", err)
			}
			var back EndData
			if err := json.Unmarshal(raw, &back); err != nil {
				t.Fatalf("读不回来：%v", err)
			}
			if back != data {
				t.Fatalf("读回来的是 %+v，排出去的是 %+v", back, data)
			}
		})
	}
}

func TestEndData读不回来的几种形状(t *testing.T) {
	t.Parallel()

	for name, raw := range map[string]string{
		"整个不是对象":     `42`,
		"turn 这个键缺了": `{"compactionId":"c-1"}`,
		"turn 是个对象":  `{"compactionId":"c-1","turn":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var data EndData
			if err := json.Unmarshal([]byte(raw), &data); !errors.Is(err, ErrMalformedEvent) {
				t.Fatalf("报的是 %v", err)
			}
		})
	}
}

func TestSummaryData往返(t *testing.T) {
	t.Parallel()

	data := summaryOf("c-1", 4, 5, 6)
	data.SourceCommandID = "cmd-1"
	data.MaxTokens = 512
	data.Usage = &llm.TokenUsage{InputTokens: 900, OutputTokens: 120}

	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	var back SummaryData
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("读不回来：%v", err)
	}
	if back.CompactionID != data.CompactionID || back.SourceCommandID != data.SourceCommandID ||
		back.ShadowedRange != data.ShadowedRange || back.ShadowedTokenCount != data.ShadowedTokenCount ||
		back.Provider != data.Provider || back.Model != data.Model || back.MaxTokens != data.MaxTokens {
		t.Fatalf("读回来的是 %+v", back)
	}
	if back.Usage == nil || *back.Usage != *data.Usage {
		t.Fatalf("用量读回来的是 %+v", back.Usage)
	}
	if len(back.ShadowedSeqs) != 3 || back.ShadowedSeqs[0] != 4 || back.ShadowedSeqs[2] != 6 {
		t.Fatalf("被遮节点读回来的是 %v", back.ShadowedSeqs)
	}
	if back.LLMStreamCall {
		t.Fatal("没标过的 llmStreamCall 读回来成了真")
	}
	if back.RawOutput != nil {
		t.Fatalf("没带过的 rawOutput 读回来是 %v", back.RawOutput)
	}
}

func TestSummaryData的空原始输出不会被省掉(t *testing.T) {
	t.Parallel()

	// DSH 的交叉类型里 `rawOutput: []` 配 `llmStreamCall: true` 是合法的。
	// omitempty 会把空切片和 nil 一起省掉，那样一次往返就把它改写成
	// 「没带原始输出」，于是再读回来变成一条违规的事件。
	data := summaryOf("c-1", 4)
	data.RawOutput = llm.Content{}
	data.LLMStreamCall = true

	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	var back SummaryData
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("读不回来：%v", err)
	}
	if !back.LLMStreamCall {
		t.Fatal("标记丢了")
	}
	if back.RawOutput == nil {
		t.Fatal("空的原始输出被省掉了")
	}
}

func TestSummaryData标了流式调用就必须带原始输出(t *testing.T) {
	t.Parallel()

	// 排出去的那一刻就报，而不是排成一条少一个键的事件——那种事件读回来是
	// 违规的，而写它的那一刻没有任何地方会报警。
	data := summaryOf("c-1", 4)
	data.LLMStreamCall = true

	if _, err := json.Marshal(data); !errors.Is(err, ErrInvariantViolated) {
		t.Fatalf("报的是 %v", err)
	}
}

func TestSummaryData读不回来的几种形状(t *testing.T) {
	t.Parallel()

	for name, item := range map[string]struct {
		raw     string
		wantErr error
	}{
		"整个不是对象": {`[]`, ErrMalformedEvent},
		"llmStreamCall 写死了 false": {
			// DSH 那半个交叉类型是 `llmStreamCall: true`，另一半是
			// `llmStreamCall?: never`：写死的 false 两边都不满足。
			`{"compactionId":"c-1","llmStreamCall":false}`, ErrMalformedEvent,
		},
		"标了流式调用却没带原始输出": {
			`{"compactionId":"c-1","llmStreamCall":true}`, ErrInvariantViolated,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var data SummaryData
			if err := json.Unmarshal([]byte(item.raw), &data); !errors.Is(err, item.wantErr) {
				t.Fatalf("报的是 %v", err)
			}
		})
	}
}

func TestPruneData往返(t *testing.T) {
	t.Parallel()

	data := PruneData{
		ShadowedRange:      ShadowedRange{Start: 7, End: 9},
		ShadowedSeqs:       []int{7, 8, 9},
		ShadowedTokenCount: 42,
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	back, err := DecodePrune(logEventAt(10, EventCompactionPrune, raw))
	if err != nil {
		t.Fatalf("读不回来：%v", err)
	}
	if back.ShadowedRange != data.ShadowedRange || back.ShadowedTokenCount != data.ShadowedTokenCount ||
		len(back.ShadowedSeqs) != 3 {
		t.Fatalf("读回来的是 %+v", back)
	}
}

func TestDecode认类型(t *testing.T) {
	t.Parallel()

	// 一条事件报的类型和要解的负载对不上时，先拒掉再说：JSON 是结构宽松的，
	// 硬解很可能得到一个字段全是零值、看起来完全正常的负载。
	event := logEventAt(3, EventCompactionEnd, json.RawMessage(`{"compactionId":"c-1","turn":1}`))
	if _, err := DecodeStart(event); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("报的是 %v", err)
	}
	if _, err := DecodeSummary(event); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("报的是 %v", err)
	}
	if _, err := DecodePrune(event); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("报的是 %v", err)
	}
	if _, err := DecodeEnd(event); err != nil {
		t.Fatalf("对得上的反而报了：%v", err)
	}
}

func TestDecode空负载当成空对象(t *testing.T) {
	t.Parallel()

	// 一条 Data 为空的 compaction/prune 解成全零值的负载，不报错——「负载缺了」
	// 和「负载读不回来」不是一回事，前者交给不变量那一侧去说。
	got, err := DecodePrune(logEventAt(3, EventCompactionPrune, nil))
	if err != nil {
		t.Fatalf("报了：%v", err)
	}
	if got.ShadowedTokenCount != 0 || got.ShadowedSeqs != nil {
		t.Fatalf("解出来的是 %+v", got)
	}
}

func TestDecode没有自定义解码的负载也挂上哨兵(t *testing.T) {
	t.Parallel()

	// [PruneData] 是纯 struct tag，读不回来时那条错误上没有本包的哨兵，
	// 得由 decodePayload 那一侧补上，否则调用方 errors.Is 一条都不成立。
	event := logEventAt(3, EventCompactionPrune, json.RawMessage(`{"shadowedSeqs":"四五六"}`))
	if _, err := DecodePrune(event); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("报的是 %v", err)
	}
}

func TestDecode不给同一条错误挂两个哨兵(t *testing.T) {
	t.Parallel()

	// 负载自己的 UnmarshalJSON 已经挂过哨兵了。再挂第二个的话，一条错误同时
	// 满足两个哨兵，调用方 errors.Is 两边都成立，也就分不出该按哪一种处理。
	event := logEventAt(3, EventCompactionSummary,
		json.RawMessage(`{"compactionId":"c-1","llmStreamCall":true}`))
	_, err := DecodeSummary(event)
	if !errors.Is(err, ErrInvariantViolated) {
		t.Fatalf("报的是 %v", err)
	}
	if errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("同时挂上了两个哨兵：%v", err)
	}
}
