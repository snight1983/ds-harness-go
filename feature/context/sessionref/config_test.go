// 本文件的作用：验三个预算参数怎么补默认值、怎么被拒，以及失败分类的行为。

package sessionref

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestConfig的零值补上三个默认值(t *testing.T) {
	resolved, err := Config{}.Resolve()
	if err != nil {
		t.Fatalf("空配置应当能补完：%v", err)
	}
	want := ResolvedConfig{
		MaxReferences:     MaxReferences,
		CandidateLimit:    DefaultCandidateLimit,
		MaxReferenceBytes: DefaultMaxReferenceBytes,
	}
	if resolved != want {
		t.Fatalf("补出来是 %+v，要的是 %+v", resolved, want)
	}
}

func TestConfig给了值就用给的值(t *testing.T) {
	resolved, err := Config{MaxReferences: 1, CandidateLimit: 5, MaxReferenceBytes: 128}.Resolve()
	if err != nil {
		t.Fatalf("补完失败：%v", err)
	}
	want := ResolvedConfig{MaxReferences: 1, CandidateLimit: 5, MaxReferenceBytes: 128}
	if resolved != want {
		t.Fatalf("补出来是 %+v，要的是 %+v", resolved, want)
	}
}

func TestConfig的非正数一律被拒(t *testing.T) {
	for name, config := range map[string]Config{
		"maxReferences 是负数":     {MaxReferences: -1},
		"candidateLimit 是负数":    {CandidateLimit: -1},
		"maxReferenceBytes 是负数": {MaxReferenceBytes: -1},
	} {
		_, err := config.Resolve()
		if !errors.Is(err, CodeInvalidConfig) {
			t.Fatalf("%s：应当被拒，得到 %v", name, err)
		}
		if !strings.Contains(err.Error(), "正整数") {
			t.Fatalf("%s：错误里该说清为什么：%v", name, err)
		}
	}
}

func TestConfig的引用上限调不上去(t *testing.T) {
	// 上限是硬的：配置只能往下调。
	_, err := Config{MaxReferences: MaxReferences + 1}.Resolve()
	if !errors.Is(err, CodeInvalidConfig) {
		t.Fatalf("超过硬上限应当被拒，得到 %v", err)
	}
}

func TestErrorCode本身就是可以被errorsIs认出来的哨兵(t *testing.T) {
	err := fail(CodeTooMany, "引用太多了")
	if !errors.Is(err, CodeTooMany) {
		t.Fatal("errors.Is 认不出这次失败的分类")
	}
	if errors.Is(err, CodeCancelled) {
		t.Fatal("errors.Is 把它认成了别的分类")
	}
	if err.Error() != string(CodeTooMany)+"：引用太多了" {
		t.Fatalf("错误文本是 %q", err.Error())
	}
	if CodeTooMany.Error() != string(CodeTooMany) {
		t.Fatalf("分类自己的文本是 %q", CodeTooMany.Error())
	}
}

func TestError把底层原因带上并且解得开(t *testing.T) {
	cause := fmt.Errorf("磁盘坏了")
	err := wrap(CodeReadFailed, cause, "读会话 %q 失败", "s1")
	if !errors.Is(err, CodeReadFailed) {
		t.Fatal("分类丢了")
	}
	if !errors.Is(err, cause) {
		t.Fatal("底层原因解不开")
	}
	if !strings.Contains(err.Error(), "磁盘坏了") || !strings.Contains(err.Error(), `"s1"`) {
		t.Fatalf("错误文本没把两条链都带上：%q", err.Error())
	}
}

func TestErrorIs对非分类的目标一律不认(t *testing.T) {
	err := fail(CodeSelfReference, "自引用")
	if errors.Is(err, errNotFound) {
		t.Fatal("不该认一个不是分类的目标")
	}
}
