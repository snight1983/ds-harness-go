// 本文件的作用：本包那几种错误各自能被问出什么，以及那句版本拒绝为什么必须分方向。
//
// 源: packages/session/session-persistence/src/coordinator.ts:34-79

package persistence

import (
	"errors"
	"strings"
	"testing"

	"ds-harness-go/session"
)

func TestCorruptionErrorAnswersBothQuestions(t *testing.T) {
	t.Parallel()

	// 调用方既要问「是不是损坏」，也要问「是哪一条校验把它判死的」。
	// 两问都得命中，这正是 Unwrap 返回切片的理由。
	err := &CorruptionError{ID: "s1", Cause: session.ErrTraceViolation}

	if !errors.Is(err, ErrCorrupted) {
		t.Fatalf("该认出这是一次损坏")
	}
	if !errors.Is(err, session.ErrTraceViolation) {
		t.Fatalf("该认出是哪条校验判死的")
	}
	if !strings.Contains(err.Error(), "s1") {
		t.Fatalf("错误文字里该有会话身份：%q", err.Error())
	}
}

func TestFormatUnsupportedErrorIsNotCorruption(t *testing.T) {
	t.Parallel()

	// 这两条分开是有代价的（多一个类型），换来的是使用者拿到的下一步动作不同：
	// 一个是「升级运行时」，一个是「这份存档坏了」。混起来就等于让人白删文件。
	err := &FormatUnsupportedError{ID: "s1", Reason: "读不了"}

	if !errors.Is(err, ErrFormatUnsupported) {
		t.Fatalf("该认出这是一次格式拒绝")
	}
	if errors.Is(err, ErrCorrupted) {
		t.Fatalf("格式拒绝绝不许被当成损坏")
	}
	if err.Error() != "读不了" {
		t.Fatalf("没有位置时错误文字就是理由本身：%q", err.Error())
	}
}

func TestWithLocationLeavesTheOriginalAlone(t *testing.T) {
	t.Parallel()

	original := &FormatUnsupportedError{ID: "s1", Reason: "读不了"}
	located := original.WithLocation(Location{Kind: "file", Path: "/logs/s1.jsonl"})

	if original.Location != nil {
		t.Fatalf("原来那条不许被改：%#v", original.Location)
	}
	if located.Location == nil || located.Location.Path != "/logs/s1.jsonl" {
		t.Fatalf("副本上该有位置：%#v", located.Location)
	}
	if !strings.Contains(located.Error(), "/logs/s1.jsonl") {
		t.Fatalf("有位置时错误文字该缀上路径：%q", located.Error())
	}
	if !errors.Is(located, ErrFormatUnsupported) {
		t.Fatalf("副本也得还认得出是格式拒绝")
	}
}

func TestFormatVersionRefusalPointsInTheRightDirection(t *testing.T) {
	t.Parallel()

	newer := FormatVersionRefusal("s1", session.FormatVersion+1)
	older := FormatVersionRefusal("s1", session.FormatVersion-1)

	// 措辞分方向就是这个函数单独存在的全部理由。合成一句，使用者只能自己猜
	// 该升级还是该放弃。
	if newer == older {
		t.Fatalf("两个方向的拒绝理由被写成同一句了")
	}
	// 比本构建新：使用者升级运行时就能打开它，这是一句**可执行**的指示。
	if !strings.Contains(newer, "升级之后才能打开") {
		t.Fatalf("比本构建新时该指出升级运行时就能打开：%q", newer)
	}
	// 比本构建旧：升级运行时没用，本构建根本不带那条路径。这两句话对应的
	// 下一步动作完全不同，所以措辞不许含糊。
	if !strings.Contains(older, "不带") {
		t.Fatalf("比本构建旧时该指出本构建没有这条路径：%q", older)
	}
	if strings.Contains(older, "升级之后才能打开") {
		t.Fatalf("比本构建旧时不许给出升级就能打开的指示：%q", older)
	}
	for _, text := range []string{newer, older} {
		if !strings.Contains(text, "s1") {
			t.Fatalf("拒绝理由里该有会话身份：%q", text)
		}
		if strings.Contains(text, "损坏") {
			t.Fatalf("格式拒绝里绝不许出现「损坏」：%q", text)
		}
	}
}
