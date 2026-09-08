// 本文件验 [DescribeRefs]：它交出来的和逐个 [Provider.Describe] 一致，
// 而它自己那三道关（扇出上限、语法、重名）都在问提供方之前就走完。

package credentials

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
)

// countingProvider 记下提供方被问了哪几个引用、问了几次。
type countingProvider struct {
	*memoryCredentials

	asked  []Ref
	failOn Ref
}

func newCountingProvider(seed map[Ref]string) *countingProvider {
	return &countingProvider{
		memoryCredentials: newMemoryCredentials(slog.New(slog.NewTextHandler(io.Discard, nil)), seed),
	}
}

func (c *countingProvider) Describe(ctx context.Context, ref Ref) (Info, error) {
	c.asked = append(c.asked, ref)
	if c.failOn != "" && ref == c.failOn {
		return Info{}, fmt.Errorf("后端坏了")
	}
	return c.memoryCredentials.Describe(ctx, ref)
}

func TestDescribeRefs给的和逐个描述一样(t *testing.T) {
	ctx := context.Background()
	provider := newCountingProvider(map[Ref]string{"CONFIGURED": "值"})

	described, err := DescribeRefs(ctx, provider, []string{"CONFIGURED", "BLANK"})
	if err != nil {
		t.Fatalf("批量描述不该失败：%v", err)
	}
	if len(described) != 2 {
		t.Fatalf("要两条，拿到 %d 条", len(described))
	}
	for _, ref := range []Ref{"CONFIGURED", "BLANK"} {
		want, err := provider.memoryCredentials.Describe(ctx, ref)
		if err != nil {
			t.Fatalf("逐个描述不该失败：%v", err)
		}
		if described[ref] != want {
			t.Errorf("引用 %q 对不上：批量给 %+v，逐个给 %+v", ref, described[ref], want)
		}
	}
}

// TestDescribeRefs不交出值 钉住这条路径的全部理由：它是只读的那一半。
//
// [Info] 是结构体，值根本没有地方可以搭车出来——这个测试验的是这件事在
// 类型上就成立，而不是靠实现方自觉。
func TestDescribeRefs不交出值(t *testing.T) {
	ctx := context.Background()
	const secret = "这是密钥正文"
	provider := newCountingProvider(map[Ref]string{"CONFIGURED": secret})

	described, err := DescribeRefs(ctx, provider, []string{"CONFIGURED"})
	if err != nil {
		t.Fatalf("批量描述不该失败：%v", err)
	}
	info := described["CONFIGURED"]
	if !info.Configured {
		t.Error("这一位该是已配置")
	}
	if strings.Contains(fmt.Sprintf("%+v", info), secret) {
		t.Errorf("描述里带出了密钥正文：%+v", info)
	}
}

func TestDescribeRefs超过上限时一个都不问(t *testing.T) {
	ctx := context.Background()
	provider := newCountingProvider(nil)

	names := make([]string, MaxDescribeRefs+1)
	for index := range names {
		names[index] = fmt.Sprintf("REF_%d", index)
	}

	if _, err := DescribeRefs(ctx, provider, names); !errors.Is(err, ErrTooManyRefs) {
		t.Fatalf("要的是 ErrTooManyRefs，拿到 %v", err)
	}
	if len(provider.asked) != 0 {
		t.Errorf("超上限时提供方不该被问：%v", provider.asked)
	}
}

func TestDescribeRefs正好到上限时放行(t *testing.T) {
	ctx := context.Background()
	provider := newCountingProvider(nil)

	names := make([]string, MaxDescribeRefs)
	for index := range names {
		names[index] = fmt.Sprintf("REF_%d", index)
	}

	described, err := DescribeRefs(ctx, provider, names)
	if err != nil {
		t.Fatalf("正好到上限不该被拒：%v", err)
	}
	if len(described) != MaxDescribeRefs {
		t.Errorf("要 %d 条，拿到 %d 条", MaxDescribeRefs, len(described))
	}
}

// TestDescribeRefs有一个坏名字就整次拒绝 验坏名字不会被静悄悄跳过。
//
// 跳过它的话，界面上少的那一行和「这个引用没配置」长得一模一样。
func TestDescribeRefs有一个坏名字就整次拒绝(t *testing.T) {
	ctx := context.Background()
	provider := newCountingProvider(map[Ref]string{"GOOD": "值"})

	if _, err := DescribeRefs(ctx, provider, []string{"GOOD", "带 空格"}); !errors.Is(err, ErrInvalidRef) {
		t.Fatalf("要的是 ErrInvalidRef，拿到 %v", err)
	}
	if len(provider.asked) != 0 {
		t.Errorf("语法关没过时提供方一个都不该被问：%v", provider.asked)
	}
}

func TestDescribeRefs重名只问一次(t *testing.T) {
	ctx := context.Background()
	provider := newCountingProvider(map[Ref]string{"SAME": "值"})

	described, err := DescribeRefs(ctx, provider, []string{"SAME", "SAME", "SAME"})
	if err != nil {
		t.Fatalf("批量描述不该失败：%v", err)
	}
	if len(described) != 1 {
		t.Errorf("要一条，拿到 %d 条", len(described))
	}
	if len(provider.asked) != 1 {
		t.Errorf("重名该折成一次，实际问了 %v", provider.asked)
	}
}

// TestDescribeRefs任何一条失败就整次失败 验不会交出一份缺行的描述。
func TestDescribeRefs任何一条失败就整次失败(t *testing.T) {
	ctx := context.Background()
	provider := newCountingProvider(map[Ref]string{"GOOD": "值"})
	provider.failOn = "BROKEN"

	described, err := DescribeRefs(ctx, provider, []string{"GOOD", "BROKEN"})
	if err == nil {
		t.Fatalf("有一条问不出来时该整次失败，却拿到 %v", described)
	}
	if described != nil {
		t.Errorf("失败时不该交出半份结果：%v", described)
	}
}

func TestDescribeRefs一个都不问时给空表(t *testing.T) {
	ctx := context.Background()
	provider := newCountingProvider(nil)

	described, err := DescribeRefs(ctx, provider, nil)
	if err != nil {
		t.Fatalf("空请求不该失败：%v", err)
	}
	if len(described) != 0 {
		t.Errorf("要空表，拿到 %v", described)
	}
	if len(provider.asked) != 0 {
		t.Errorf("空请求不该问提供方：%v", provider.asked)
	}
}
