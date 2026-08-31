package llm

import (
	"errors"
	"math"
	"slices"
	"testing"
	"time"
)

// ptr 造一个指向给定值的指针，专给那两个「零值有意义」的可选字段用。
func ptr[T any](value T) *T { return &value }

// TestResolveRetryPolicyDefaults 确认「没给配置」和「给了一份三个可选字段全缺的
// normal 配置」解算出来一模一样——这是本包把 DSH 那个重复的默认值块合并掉之后
// 唯一要守的等式。
func TestResolveRetryPolicyDefaults(t *testing.T) {
	fromNil, err := ResolveRetryPolicy(nil, "provider")
	if err != nil {
		t.Fatalf("空配置不该失败：%v", err)
	}
	fromEmpty, err := ResolveRetryPolicy(&RetryPolicyConfig{Mode: RetryNormal}, "provider")
	if err != nil {
		t.Fatalf("空的 normal 配置不该失败：%v", err)
	}

	if fromNil.Mode != RetryNormal {
		t.Fatalf("默认档位该是 %q，得到 %q", RetryNormal, fromNil.Mode)
	}
	if fromNil.MaxRetries != defaultMaxRetries {
		t.Fatalf("默认重试次数该是 %d，得到 %d", defaultMaxRetries, fromNil.MaxRetries)
	}
	if fromNil.InitialDelay != defaultInitialDelay || fromNil.MaxDelay != defaultMaxDelay {
		t.Fatalf("默认退避不对：%s / %s", fromNil.InitialDelay, fromNil.MaxDelay)
	}
	if fromNil.JitterRatio != defaultJitterRatio {
		t.Fatalf("默认抖动该是 %v，得到 %v", defaultJitterRatio, fromNil.JitterRatio)
	}
	if !slices.Equal(fromNil.RetryableCodes, defaultRetryableCodes()) {
		t.Fatalf("默认可重试码不对：%v", fromNil.RetryableCodes)
	}
	if !slices.Equal(fromNil.RetryableCodes, fromEmpty.RetryableCodes) ||
		fromNil.MaxRetries != fromEmpty.MaxRetries ||
		fromNil.ResolvedRetryBackoff != fromEmpty.ResolvedRetryBackoff {
		t.Fatal("空配置和空的 normal 配置解算结果不一致")
	}
}

// TestDefaultRetryableCodesIsFresh 确认默认集合每次都是一份新的：调用方改到手上
// 那份，不能影响下一位调用方。DSH 靠 Object.freeze 挡这件事，Go 里靠复制。
func TestDefaultRetryableCodesIsFresh(t *testing.T) {
	policy, err := ResolveRetryPolicy(nil, "provider")
	if err != nil {
		t.Fatalf("空配置不该失败：%v", err)
	}
	policy.RetryableCodes[0] = "TAMPERED"

	again, err := ResolveRetryPolicy(nil, "provider")
	if err != nil {
		t.Fatalf("空配置不该失败：%v", err)
	}
	if again.RetryableCodes[0] == "TAMPERED" {
		t.Fatal("默认可重试码被上一位调用方改到了")
	}
	if again.RetryableCodes[0] != EmptyResponseCode {
		t.Fatalf("默认集合第一条该是 %q，得到 %q", EmptyResponseCode, again.RetryableCodes[0])
	}
}

// TestResolveRetryPolicyDetaches 确认解算出来的策略和调用方那份切片脱了钩。
func TestResolveRetryPolicyDetaches(t *testing.T) {
	codes := []string{"A", "B"}
	policy, err := ResolveRetryPolicy(&RetryPolicyConfig{
		Mode:           RetryNormal,
		RetryableCodes: codes,
	}, "provider")
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	codes[0] = "CHANGED"
	if policy.RetryableCodes[0] != "A" {
		t.Fatalf("策略跟着调用方那份切片变了：%v", policy.RetryableCodes)
	}
}

// TestResolveRetryPolicyExplicitZeroes 守住那两个「零值有意义」的字段：明确写下的
// 0 次重试和 0 抖动，都不能被当成「没给」而落回默认值。
func TestResolveRetryPolicyExplicitZeroes(t *testing.T) {
	policy, err := ResolveRetryPolicy(&RetryPolicyConfig{
		Mode:       RetryNormal,
		MaxRetries: ptr(0),
		Backoff:    BackoffConfig{JitterRatio: ptr(0.0)},
	}, "provider")
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	if policy.MaxRetries != 0 {
		t.Fatalf("明确写下的 0 次重试被改成了 %d", policy.MaxRetries)
	}
	if policy.JitterRatio != 0 {
		t.Fatalf("明确写下的 0 抖动被改成了 %v", policy.JitterRatio)
	}
}

// TestResolveRetryPolicyAlwaysIgnoresNormalFields 复刻 DSH retry-policy.ts:108-112
// 那条注释说的事：分层配置切到 always 之后会留着 normal 那两个字段，always 档
// 只是不读它们——哪怕它们的取值在 normal 档下会被拒。
func TestResolveRetryPolicyAlwaysIgnoresNormalFields(t *testing.T) {
	policy, err := ResolveRetryPolicy(&RetryPolicyConfig{
		Mode:           RetryAlways,
		MaxRetries:     ptr(-1),
		RetryableCodes: []string{},
		Backoff:        BackoffConfig{InitialDelay: time.Second, MaxDelay: time.Minute},
	}, "provider")
	if err != nil {
		t.Fatalf("always 档不该看 normal 那两个字段：%v", err)
	}
	if policy.Mode != RetryAlways {
		t.Fatalf("档位该是 %q，得到 %q", RetryAlways, policy.Mode)
	}
	if policy.MaxRetries != 0 || policy.RetryableCodes != nil {
		t.Fatalf("always 档不该填 normal 那两个字段：%d / %v", policy.MaxRetries, policy.RetryableCodes)
	}
	if policy.InitialDelay != time.Second || policy.MaxDelay != time.Minute {
		t.Fatalf("退避没被解算：%s / %s", policy.InitialDelay, policy.MaxDelay)
	}
}

// TestResolveRetryPolicyNormalFull 走一份全填的 normal 配置。
func TestResolveRetryPolicyNormalFull(t *testing.T) {
	policy, err := ResolveRetryPolicy(&RetryPolicyConfig{
		Mode:           RetryNormal,
		MaxRetries:     ptr(2),
		RetryableCodes: []string{"SERVER", "TIMEOUT"},
		Backoff: BackoffConfig{
			InitialDelay: 100 * time.Millisecond,
			MaxDelay:     2 * time.Second,
			JitterRatio:  ptr(0.25),
		},
	}, "provider")
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	want := ResolvedRetryPolicy{
		Mode: RetryNormal,
		ResolvedRetryBackoff: ResolvedRetryBackoff{
			InitialDelay: 100 * time.Millisecond,
			MaxDelay:     2 * time.Second,
			JitterRatio:  0.25,
		},
		MaxRetries:     2,
		RetryableCodes: []string{"SERVER", "TIMEOUT"},
	}
	if policy.Mode != want.Mode || policy.MaxRetries != want.MaxRetries ||
		policy.ResolvedRetryBackoff != want.ResolvedRetryBackoff ||
		!slices.Equal(policy.RetryableCodes, want.RetryableCodes) {
		t.Fatalf("解算结果不对：%+v", policy)
	}
}

// TestResolveRetryPolicyRejects 逐条走每一个拒绝面。全都要归到 [ErrInvalidConfig]
// 上——调用方是靠它分派「这是人写错了」的。
func TestResolveRetryPolicyRejects(t *testing.T) {
	cases := map[string]RetryPolicyConfig{
		"档位没填":        {},
		"档位不认识":       {Mode: RetryMode("aggressive")},
		"重试次数是负数":     {Mode: RetryNormal, MaxRetries: ptr(-1)},
		"可重试码是空清单":    {Mode: RetryNormal, RetryableCodes: []string{}},
		"可重试码里有空串":    {Mode: RetryNormal, RetryableCodes: []string{"SERVER", ""}},
		"可重试码重复":      {Mode: RetryNormal, RetryableCodes: []string{"SERVER", "SERVER"}},
		"起始延时是负数":     {Mode: RetryNormal, Backoff: BackoffConfig{InitialDelay: -time.Second}},
		"上限延时是负数":     {Mode: RetryNormal, Backoff: BackoffConfig{MaxDelay: -time.Second}},
		"起始延时大于上限":    {Mode: RetryNormal, Backoff: BackoffConfig{InitialDelay: time.Minute, MaxDelay: time.Second}},
		"抖动是负数":       {Mode: RetryNormal, Backoff: BackoffConfig{JitterRatio: ptr(-0.1)}},
		"抖动大于一":       {Mode: RetryNormal, Backoff: BackoffConfig{JitterRatio: ptr(1.5)}},
		"抖动是 NaN":     {Mode: RetryNormal, Backoff: BackoffConfig{JitterRatio: ptr(math.NaN())}},
		"always 档退避坏": {Mode: RetryAlways, Backoff: BackoffConfig{InitialDelay: -time.Second}},
	}
	for name, config := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ResolveRetryPolicy(&config, "provider"); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("该报 ErrInvalidConfig，得到 %v", err)
			}
		})
	}
}
