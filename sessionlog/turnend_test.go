// 本文件的作用：回合结束理由与取消来路两个联合的介质形状、开放性与封闭性。
//
// 源: packages/core/session/src/types.ts:236-337

package sessionlog

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/snight1983/ds-harness-go/llm"
)

func TestTurnEndReasonsMarshalWithTheirTag(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		reason TurnEndReason
		want   string
	}{
		"完成":   {reason: CompletedTurnEnd{}, want: `{"kind":"completed"}`},
		"拦下":   {reason: BlockedTurnEnd{}, want: `{"kind":"blocked"}`},
		"撞上限":  {reason: MaxTokensTurnEnd{}, want: `{"kind":"max-tokens"}`},
		"中途死掉": {reason: InterruptedTurnEnd{}, want: `{"kind":"interrupted"}`},
		"取消": {
			reason: AbortedTurnEnd{Reason: UserCancel{}},
			want:   `{"kind":"aborted","reason":{"kind":"user"}}`,
		},
		"钩子取消带一句说明": {
			reason: AbortedTurnEnd{Reason: HookCancel{Reason: "policy denied"}},
			want:   `{"kind":"aborted","reason":{"kind":"hook","reason":"policy denied"}}`,
		},
		"报错": {
			reason: ErrorTurnEnd{Error: llm.Failure{Message: "boom", Code: "E", Status: 500}},
			want:   `{"kind":"error","error":{"message":"boom","code":"E","status":500}}`,
		},
		"不认识的理由原样送回去": {
			reason: UnknownTurnEnd{Kind: "compacted", Raw: json.RawMessage(`{"kind":"compacted","n":3}`)},
			want:   `{"kind":"compacted","n":3}`,
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(testCase.reason)
			if err != nil {
				t.Fatalf("排不出去：%v", err)
			}
			if string(got) != testCase.want {
				t.Fatalf("排出来的字节不对：\n想要 %s\n实际 %s", testCase.want, got)
			}
			if kind := testCase.reason.TurnEndReasonKind(); string(kind) == "" {
				t.Fatalf("理由没有标签")
			}
		})
	}
}

func TestUnmarshalTurnEndReasonKeepsWhatItDoesNotKnow(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		line string
		want TurnEndReason
	}{
		"完成":   {line: `{"kind":"completed"}`, want: CompletedTurnEnd{}},
		"拦下":   {line: `{"kind":"blocked"}`, want: BlockedTurnEnd{}},
		"撞上限":  {line: `{"kind":"max-tokens"}`, want: MaxTokensTurnEnd{}},
		"中途死掉": {line: `{"kind":"interrupted"}`, want: InterruptedTurnEnd{}},
		"取消": {
			line: `{"kind":"aborted","reason":{"kind":"parent"}}`,
			want: AbortedTurnEnd{Reason: ParentCancel{}},
		},
		"报错": {
			line: `{"kind":"error","error":{"message":"boom","code":"E"}}`,
			want: ErrorTurnEnd{Error: llm.Failure{Message: "boom", Code: "E"}},
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := UnmarshalTurnEndReason([]byte(testCase.line))
			if err != nil {
				t.Fatalf("读不回来：%v", err)
			}
			if got != testCase.want {
				t.Fatalf("读回来的理由不对：想要 %#v，实际 %#v", testCase.want, got)
			}
		})
	}

	t.Run("不认识的标签落进 UnknownTurnEnd 且字节一字不差", func(t *testing.T) {
		t.Parallel()

		line := `{"kind":"compacted","summary":"…","n":3}`
		got, err := UnmarshalTurnEndReason([]byte(line))
		if err != nil {
			t.Fatalf("读不回来：%v", err)
		}
		unknown, ok := got.(UnknownTurnEnd)
		if !ok {
			t.Fatalf("想要 UnknownTurnEnd，实际 %T", got)
		}
		if unknown.Kind != "compacted" {
			t.Fatalf("标签不对：%q", unknown.Kind)
		}
		back, err := json.Marshal(unknown)
		if err != nil {
			t.Fatalf("排不出去：%v", err)
		}
		if string(back) != line {
			t.Fatalf("原样保管失败：\n想要 %s\n实际 %s", line, back)
		}
	})
}

func TestUnmarshalTurnEndReasonRejectsBrokenValues(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"不是一个对象":      `7`,
		"没有 kind":     `{"turn":1}`,
		"取消但没带来路":     `{"kind":"aborted"}`,
		"取消的来路读不回来":   `{"kind":"aborted","reason":{"kind":"martian"}}`,
		"取消的来路不是个对象":  `{"kind":"aborted","reason":3}`,
		"报错但 error 坏": `{"kind":"error","error":7}`,
	}

	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := UnmarshalTurnEndReason([]byte(line)); err == nil {
				t.Fatalf("想要报错，实际读回来了")
			}
		})
	}
}

func TestUnknownTurnEndWithoutBytesCannotBeMarshaled(t *testing.T) {
	t.Parallel()

	if _, err := json.Marshal(UnknownTurnEnd{Kind: "x"}); !errors.Is(err, ErrMalformedValue) {
		t.Fatalf("想要 ErrMalformedValue，实际 %v", err)
	}
}

func TestAbortedTurnEndNeedsACause(t *testing.T) {
	t.Parallel()

	if _, err := json.Marshal(AbortedTurnEnd{}); !errors.Is(err, ErrMalformedValue) {
		t.Fatalf("想要 ErrMalformedValue，实际 %v", err)
	}
}

// unmarshalableCause 是一个排不出去的取消来路，用来走到 [AbortedTurnEnd.MarshalJSON] 的错误分支。
type unmarshalableCause struct{}

func (unmarshalableCause) CancelCauseKind() CancelCauseKind { return CancelUser }

func (unmarshalableCause) sealedCancelCause() {}

func (unmarshalableCause) MarshalJSON() ([]byte, error) { return nil, errors.New("排不出去") }

func TestAbortedTurnEndReportsACauseThatCannotBeMarshaled(t *testing.T) {
	t.Parallel()

	_, err := json.Marshal(AbortedTurnEnd{Reason: unmarshalableCause{}})
	if !errors.Is(err, ErrMalformedValue) {
		t.Fatalf("想要 ErrMalformedValue，实际 %v", err)
	}
}

func TestCancelCausesRoundTrip(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		cause TurnEndCancelCause
		want  string
		kind  CancelCauseKind
	}{
		"使用者按的停":   {cause: UserCancel{}, want: `{"kind":"user"}`, kind: CancelUser},
		"上级停的":     {cause: ParentCancel{}, want: `{"kind":"parent"}`, kind: CancelParent},
		"承载它的东西没了": {cause: DisposedCancel{}, want: `{"kind":"disposed"}`, kind: CancelDisposed},
		"旧日志里没记来路": {cause: LegacyCancel{}, want: `{"kind":"legacy"}`, kind: CancelLegacy},
		"钩子拦下的": {
			cause: HookCancel{Reason: "denied"},
			want:  `{"kind":"hook","reason":"denied"}`,
			kind:  CancelHook,
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(testCase.cause)
			if err != nil {
				t.Fatalf("排不出去：%v", err)
			}
			if string(got) != testCase.want {
				t.Fatalf("排出来的字节不对：想要 %s，实际 %s", testCase.want, got)
			}
			back, err := UnmarshalTurnEndCancelCause(got)
			if err != nil {
				t.Fatalf("读不回来：%v", err)
			}
			if back != testCase.cause {
				t.Fatalf("往返之后不一样了：想要 %#v，实际 %#v", testCase.cause, back)
			}
			if back.CancelCauseKind() != testCase.kind {
				t.Fatalf("来路的标签不对：想要 %q，实际 %q", testCase.kind, back.CancelCauseKind())
			}
		})
	}
}

func TestUnmarshalTurnEndCancelCauseIsAClosedUnion(t *testing.T) {
	t.Parallel()

	_, err := UnmarshalTurnEndCancelCause([]byte(`{"kind":"martian"}`))
	if !errors.Is(err, ErrUnknownCancelCause) {
		t.Fatalf("想要 ErrUnknownCancelCause，实际 %v", err)
	}

	_, err = UnmarshalTurnEndCancelCause([]byte(`7`))
	if !errors.Is(err, ErrMalformedValue) {
		t.Fatalf("想要 ErrMalformedValue，实际 %v", err)
	}

	_, err = UnmarshalTurnEndCancelCause([]byte(`{"kind":"hook","reason":7}`))
	if !errors.Is(err, ErrMalformedValue) {
		t.Fatalf("想要 ErrMalformedValue，实际 %v", err)
	}
}
