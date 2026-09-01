// 本文件的作用：验那条持久来源的线上形状、它的宽进读回，以及可空 seq 的折叠。

package sessionref

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/llm"
)

func TestReference把捕获seq折成可空的数(t *testing.T) {
	captured, err := json.Marshal(Reference{SessionID: "s1", CapturedThroughSeq: 0, CapturedAny: true})
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	// seq 0 是一条真事件的合法序号，必须排成 0 而不是 null。
	if !strings.Contains(string(captured), `"capturedThroughSeq":0`) {
		t.Fatalf("seq 0 没排成 0：%s", captured)
	}

	empty, err := json.Marshal(Reference{SessionID: "s1"})
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	if !strings.Contains(string(empty), `"capturedThroughSeq":null`) {
		t.Fatalf("空日志没排成 null：%s", empty)
	}
}

func TestReference往返之后每个字段都还在(t *testing.T) {
	original := Reference{
		SessionID: "s1", Label: "上一次调研",
		CapturedThroughSeq: 7, CapturedAny: true,
		Compacted: true, OriginalMessages: 10, RetainedMessages: 4,
		OmittedMessages: 6, OmittedBytes: 123, Truncated: true, InputIndex: 2,
	}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	var back Reference
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatalf("读不回来：%v", err)
	}
	if back != original {
		t.Fatalf("往返之后是 %+v，原来是 %+v", back, original)
	}
}

func TestReference的null捕获seq读回来是没捕获(t *testing.T) {
	var back Reference
	if err := json.Unmarshal([]byte(`{"sessionId":"s1","capturedThroughSeq":null}`), &back); err != nil {
		t.Fatalf("读不回来：%v", err)
	}
	if back.CapturedAny {
		t.Fatal("null 被读成了「捕获过」")
	}
}

func TestReference读不回来时报出这条账坏了(t *testing.T) {
	var back Reference
	err := json.Unmarshal([]byte(`{"sessionId":123}`), &back)
	if err == nil || !strings.Contains(err.Error(), "读不回来") {
		t.Fatalf("应当报出这条账坏了，得到 %v", err)
	}
}

func TestSource排出去带上kind与form(t *testing.T) {
	encoded, err := json.Marshal(Source{References: []Reference{{SessionID: "s1"}}})
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	for _, want := range []string{`"kind":"` + Name + `"`, `"form":"` + sourceForm + `"`, `"version":1`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("排出来缺 %s：%s", want, encoded)
		}
	}
}

func TestSource没有引用时排成空数组而不是null(t *testing.T) {
	encoded, err := json.Marshal(Source{})
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	if !strings.Contains(string(encoded), `"references":[]`) {
		t.Fatalf("空引用没排成空数组：%s", encoded)
	}
}

func TestSource宽进只丢读不懂的那一条(t *testing.T) {
	// 这些字节来自一份已经写下的日志，可能是别的版本写的。整份拒绝会让
	// 一次本来能续上的会话丢掉全部引用记录。
	raw := `{"kind":"` + Name + `","references":[{"sessionId":"good"},{"sessionId":123},{"sessionId":"also-good"}]}`
	var source Source
	if err := json.Unmarshal([]byte(raw), &source); err != nil {
		t.Fatalf("宽进失败：%v", err)
	}
	if len(source.References) != 2 {
		t.Fatalf("留下 %d 条，要的是 2 条：%+v", len(source.References), source.References)
	}
	if source.References[0].SessionID != "good" || source.References[1].SessionID != "also-good" {
		t.Fatalf("留下的不对：%+v", source.References)
	}
}

func TestSource读回来时认kind(t *testing.T) {
	var source Source
	err := json.Unmarshal([]byte(`{"kind":"别的层","references":[]}`), &source)
	if err == nil || !strings.Contains(err.Error(), "kind") {
		t.Fatalf("kind 不对应当被拒，得到 %v", err)
	}
}

func TestSource不是一个JSON对象时被拒(t *testing.T) {
	var source Source
	if err := json.Unmarshal([]byte(`"一段字符串"`), &source); err == nil {
		t.Fatal("非对象应当被拒")
	}
}

func TestSource包进消息来源再原样取回来(t *testing.T) {
	original := Source{References: []Reference{
		{SessionID: "s1", Label: "甲", CapturedThroughSeq: 3, CapturedAny: true, InputIndex: 0},
		{SessionID: "s2", Label: "乙", InputIndex: 1},
	}}
	messageSource, err := original.MessageSource()
	if err != nil {
		t.Fatalf("包不进去：%v", err)
	}
	if messageSource.SourceKind() != llm.SourceKind(Name) {
		t.Fatalf("来源自称是 %q", messageSource.SourceKind())
	}
	parsed, ok := ParseSource(messageSource)
	if !ok {
		t.Fatal("取不回来")
	}
	if len(parsed.References) != 2 || parsed.References[0].Label != "甲" || parsed.References[1].Label != "乙" {
		t.Fatalf("取回来的不对：%+v", parsed.References)
	}
}

func TestParseSource不认别的层的来源(t *testing.T) {
	for name, source := range map[string]llm.MessageSource{
		"根本不是未知来源":  llm.UserSource{},
		"未知来源但不是本层": llm.UnknownSource{Kind: "别的层", Raw: []byte(`{}`)},
		"是本层但字节坏了":  llm.UnknownSource{Kind: llm.SourceKind(Name), Raw: []byte(`{{{`)},
	} {
		if _, ok := ParseSource(source); ok {
			t.Fatalf("%s：不该被认成本层的来源", name)
		}
	}
}
