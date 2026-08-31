// 本文件验这一层的词汇：两条事件的类型名、负载在介质上的样子，以及排出去和
// 读回来那对刻意不对称的检查。
package llmretry

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"ds-harness-go/llm"
	"ds-harness-go/session"
)

// TestEventTypesAreTheDSHNamesVerbatim 钉住这两个类型名一个字符都没改。
//
// 源: packages/llm/llm-retry/src/types.ts:6-13
//
// 它们是**落库的**：改掉之后，所有已经写下来的日志会在下一次装载时被
// [session.CheckVocabulary] 判成「有不认识的事件类型」而整个拒掉。
func TestEventTypesAreTheDSHNamesVerbatim(t *testing.T) {
	t.Parallel()

	if EventRetry != "llm/retry" {
		t.Errorf("llm/retry 的类型名变了：%q", EventRetry)
	}
	if EventRetryStarted != "llm/retry-started" {
		t.Errorf("llm/retry-started 的类型名变了：%q", EventRetryStarted)
	}
}

// TestEventTypesListsBothSoTheVocabularyCanBeOpened 钉住本包交得出那张单子。
//
// 没有它的话，装配方拼不出一份认得这两条事件的词汇表，而一段带重试的日志会被
// 整个拒掉——症状出现在装载那一刻，离写下这条事件的地方很远。
func TestEventTypesListsBothSoTheVocabularyCanBeOpened(t *testing.T) {
	t.Parallel()

	got := EventTypes()
	if len(got) != 2 || got[0] != EventRetry || got[1] != EventRetryStarted {
		t.Fatalf("单子不对：%v", got)
	}

	vocabulary := session.CoreVocabulary().With(got...)
	events := []session.Event{
		eventAt(1, EventRetry, json.RawMessage(`{}`)),
		eventAt(2, EventRetryStarted, json.RawMessage(`{}`)),
	}
	if err := session.CheckVocabulary(events, vocabulary); err != nil {
		t.Fatalf("拼出来的词汇表该认得这两条：%v", err)
	}
}

// TestMarshalRefusesANormalRetryWithoutAMaxRetries 钉住 normal 档必须写明上限。
//
// 源: packages/llm/llm-retry/src/types.ts:15-40
//
// 在排字节这一刻拒，而不是等它读回来被不变量拦下：那种拦法会 panic，而现场没有
// 任何东西指回写它的这一刻。
func TestMarshalRefusesANormalRetryWithoutAMaxRetries(t *testing.T) {
	t.Parallel()

	data := normalRetry("r-1", 1, 3)
	data.HasMaxRetries = false
	if _, err := json.Marshal(data); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("该被拒并认得出哨兵：%v", err)
	}
}

// TestMarshalRefusesAnAlwaysRetryCarryingAMaxRetries 钉住 always 档不能带上限。
func TestMarshalRefusesAnAlwaysRetryCarryingAMaxRetries(t *testing.T) {
	t.Parallel()

	data := normalRetry("r-1", 1, 3)
	data.Mode = llm.RetryAlways
	if _, err := json.Marshal(data); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("该被拒并认得出哨兵：%v", err)
	}
}

// TestMarshalRefusesAnUnknownMode 钉住只认那两档。
func TestMarshalRefusesAnUnknownMode(t *testing.T) {
	t.Parallel()

	data := normalRetry("r-1", 1, 3)
	data.Mode = "偶尔"
	if _, err := json.Marshal(data); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("该被拒并认得出哨兵：%v", err)
	}
}

// TestAnAlwaysRetryHasNoMaxRetriesKeyOnTheWire 钉住「这一档根本没有这个字段」
// 在介质上真的看得出来。
//
// 源: packages/llm/llm-retry/src/types.ts:15-40
//
// 拿 maxRetries == 0 表示「没带」是不行的：0 是一个**有意义的取值**（一次都不重试），
// 而不变量要验的正是「always 档不许出现这个键」。
func TestAnAlwaysRetryHasNoMaxRetriesKeyOnTheWire(t *testing.T) {
	t.Parallel()

	always := normalRetry("r-1", 1, 0)
	always.Mode = llm.RetryAlways
	always.HasMaxRetries = false
	encoded, err := json.Marshal(always)
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	if strings.Contains(string(encoded), "maxRetries") {
		t.Errorf("always 档不该有这个键：%s", encoded)
	}

	// 而一份写着 0 的 normal 策略必须把这个 0 摆在介质上。
	zero := normalRetry("r-1", 1, 0)
	encoded, err = json.Marshal(zero)
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	if !strings.Contains(string(encoded), `"maxRetries":0`) {
		t.Errorf("normal 档的 0 该原样落库：%s", encoded)
	}
}

// TestTheWireKeepsAFractionalMillisecondDelay 钉住延时在介质上是毫秒数，
// 而且小数留得住。
//
// 本地退避把指数值乘上了一个抖动倍率，排出来的延时本来就带小数。排成整数毫秒的话，
// 一次 1.5 毫秒的等待会被记成 1 毫秒，重放出来的时间线和真正发生过的对不上。
func TestTheWireKeepsAFractionalMillisecondDelay(t *testing.T) {
	t.Parallel()

	data := normalRetry("r-1", 1, 3)
	data.Delay = 1500 * time.Microsecond
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	if !strings.Contains(string(encoded), `"delayMs":1.5`) {
		t.Errorf("延时该是 1.5 毫秒：%s", encoded)
	}

	var back RetryData
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatalf("读不回来：%v", err)
	}
	if back != data {
		t.Errorf("往返之后不一样了：\n想要 %+v\n实际 %+v", data, back)
	}
}

// TestUnmarshalAcceptsAWireShapeThatMarshalWouldRefuse 钉住读回来那一步**不**查
// 档位和 maxRetries 对不对得上。
//
// 这条不对称是有意的：本包自己写不出一条违规的事件，但一份别处写来的日志必须读得
// 回来，好让 [Trace.Validate] 在它自己那一层把违规报成「always 档带了 maxRetries」。
// 在读回来这一步就拒的话，那条违规会变成一条「负载读不回来」——一句指不到毛病在哪
// 的诊断，而且本包那条不变量里对应的分支从此永远走不到，也就再没验过。
func TestUnmarshalAcceptsAWireShapeThatMarshalWouldRefuse(t *testing.T) {
	t.Parallel()

	const wire = `{"retryId":"r-1","turn":1,"step":1,"provider":"甲","mode":"always",` +
		`"policyKey":"k-1","retry":1,"maxRetries":3,"delayMs":500,` +
		`"failure":{"message":"上游超时了","code":"TIMEOUT"}}`

	var data RetryData
	if err := json.Unmarshal([]byte(wire), &data); err != nil {
		t.Fatalf("这条违规事件该读得回来：%v", err)
	}
	if !data.HasMaxRetries || data.MaxRetries != 3 {
		t.Errorf("那个越界的 maxRetries 该原样留着，好让不变量指得出来：%+v", data)
	}
	if _, err := json.Marshal(data); !errors.Is(err, ErrMalformedEvent) {
		t.Error("同一份负载再排出去时该被拒")
	}
}

// TestDecodeRefusesAnEventOfTheWrongType 钉住解负载之前先对一次类型。
//
// 对不上还照解的话，一条 llm/retry-started 会被解成一份字段几乎全空的 RetryData，
// 而不变量随后报的是「没有提供方」——一句指错了地方的诊断。
func TestDecodeRefusesAnEventOfTheWrongType(t *testing.T) {
	t.Parallel()

	event := eventAt(7, EventRetryStarted, json.RawMessage(`{"retryId":"r-1"}`))
	if _, err := DecodeRetry(event); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("该被拒并认得出哨兵：%v", err)
	}

	other := eventAt(7, EventRetry, json.RawMessage(`{}`))
	if _, err := DecodeRetryStarted(other); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("该被拒并认得出哨兵：%v", err)
	}
}

// TestDecodeReportsWhereAnUnreadablePayloadIs 钉住读不回来时那句诊断带着位置。
func TestDecodeReportsWhereAnUnreadablePayloadIs(t *testing.T) {
	t.Parallel()

	event := eventAt(42, EventRetry, json.RawMessage(`{"retry":"一"}`))
	_, err := DecodeRetry(event)
	if !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("该被拒并认得出哨兵：%v", err)
	}
	if !strings.Contains(err.Error(), "42") {
		t.Errorf("诊断该指得出是哪一条：%v", err)
	}
}

// TestDecodeTreatsAnEmptyPayloadAsAnEmptyObject 钉住空负载不算读不回来。
//
// 一条负载为空的事件在介质上是合法的，它只是每个字段都缺席；该由不变量去说
// 「没有链身份」，而不是在这里报一句「读不回来」。
func TestDecodeTreatsAnEmptyPayloadAsAnEmptyObject(t *testing.T) {
	t.Parallel()

	data, err := DecodeRetryStarted(session.Event{Type: EventRetryStarted, Seq: 1})
	if err != nil {
		t.Fatalf("空负载该读得回来：%v", err)
	}
	if data != (RetryStartedData{}) {
		t.Errorf("该是个全空的负载：%+v", data)
	}
}
