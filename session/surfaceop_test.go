// 本文件的作用：表面操作那两个变体的介质形状与读回来的判据。
//
// 源: packages/core/session/src/surface.ts:172-208

package session

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestSurfaceOpMarshalsToTheAsymmetricWireShape(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		operation SurfaceOp
		want      string
	}{
		"追加排成一个裸字符串": {
			operation: AppendOp{},
			want:      `"append"`,
		},
		"替换排成一个带三个键的对象": {
			operation: ReplaceOp{Start: 3, End: 7},
			want:      `{"op":"replace","start":3,"end":7}`,
		},
		"替换单个节点时两端相同": {
			operation: ReplaceOp{Start: 5, End: 5},
			want:      `{"op":"replace","start":5,"end":5}`,
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(testCase.operation)
			if err != nil {
				t.Fatalf("排不出去：%v", err)
			}
			if string(got) != testCase.want {
				t.Fatalf("排出来的字节不对：想要 %s，实际 %s", testCase.want, got)
			}
		})
	}
}

func TestSurfaceOpKindsAreTheTwoTags(t *testing.T) {
	t.Parallel()

	if (AppendOp{}).SurfaceOpKind() != OpAppend {
		t.Fatalf("追加的标签不对：%q", AppendOp{}.SurfaceOpKind())
	}
	if (ReplaceOp{}).SurfaceOpKind() != OpReplace {
		t.Fatalf("替换的标签不对：%q", ReplaceOp{}.SurfaceOpKind())
	}
}

func TestUnmarshalSurfaceOpReadsBothVariants(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		line string
		want SurfaceOp
	}{
		"追加": {line: `"append"`, want: AppendOp{}},
		"替换": {line: `{"op":"replace","start":0,"end":2}`, want: ReplaceOp{Start: 0, End: 2}},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := UnmarshalSurfaceOp([]byte(testCase.line))
			if err != nil {
				t.Fatalf("读不回来：%v", err)
			}
			if got != testCase.want {
				t.Fatalf("读回来的操作不对：想要 %#v，实际 %#v", testCase.want, got)
			}
		})
	}
}

func TestUnmarshalSurfaceOpRejectsEverythingElse(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"字符串只能是 append":   `"replace"`,
		"既不是字符串也不是对象":     `7`,
		"少一个键":            `{"op":"replace","start":1}`,
		"多一个键":            `{"op":"replace","start":1,"end":2,"why":"x"}`,
		"三个键但名字不对":        `{"op":"replace","start":1,"stop":2}`,
		"op 的取值不对":        `{"op":"squash","start":1,"end":2}`,
		"起点是负的":           `{"op":"replace","start":-1,"end":2}`,
		"终点是负的":           `{"op":"replace","start":1,"end":-2}`,
		"三个键但 start 不是个数": `{"op":"replace","start":"1","end":2}`,
	}

	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := UnmarshalSurfaceOp([]byte(line)); !errors.Is(err, ErrMalformedValue) {
				t.Fatalf("想要 ErrMalformedValue，实际 %v", err)
			}
		})
	}
}
