// 本文件的作用：验配置那三步——补默认值、按路由合并、按窗口折算——
// 每一步该拒的都拒了，以及那几处「零是有意义的」没有被默认值悄悄改写。

package basic

import (
	"errors"
	"math"
	"testing"
)

func TestResolve什么都不给时补上默认值(t *testing.T) {
	t.Parallel()

	resolved, err := Config{}.Resolve()
	if err != nil {
		t.Fatalf("验不过：%v", err)
	}
	if resolved.ThresholdRatio != DefaultThresholdRatio {
		t.Fatalf("压力线是 %v", resolved.ThresholdRatio)
	}
	if !resolved.Retention.ByRatio() || resolved.Retention.Ratio != DefaultRetainRatio {
		t.Fatalf("保留是 %+v", resolved.Retention)
	}
	if resolved.MaxTokens != DefaultMaxTokens ||
		resolved.CompactionRetries != DefaultCompactionRetries ||
		resolved.MaxOverflowRetries != DefaultMaxOverflowRetries {
		t.Fatalf("三个计数是 %+v", resolved.Policy)
	}
	if !resolved.Summarization.IsZero() {
		t.Fatalf("摘要路由是 %+v", resolved.Summarization)
	}
	// 自动压缩默认开着：没配过的会话也该在压力线上自己压一次。
	if !resolved.Auto {
		t.Fatal("自动压缩默认是关的")
	}
	if resolved.ModelPolicies != nil {
		t.Fatalf("覆盖表是 %+v", resolved.ModelPolicies)
	}
}

func TestResolve那几个有意义的零不会被默认值改写(t *testing.T) {
	t.Parallel()

	// 这四个字段的零都是明确的意思，而它们的默认值恰好都不是零——
	// 用零值当「没给」的话，这四种意思会被静默改写。
	resolved, err := Config{
		PolicyConfig: PolicyConfig{
			RetainTokens:       intOf(0),
			CompactionRetries:  intOf(0),
			MaxOverflowRetries: intOf(0),
		},
		Auto: boolOf(false),
	}.Resolve()
	if err != nil {
		t.Fatalf("验不过：%v", err)
	}
	if resolved.Retention.ByRatio() || resolved.Retention.Tokens != 0 {
		t.Fatalf("保留是 %+v", resolved.Retention)
	}
	if resolved.CompactionRetries != 0 || resolved.MaxOverflowRetries != 0 {
		t.Fatalf("两个重试是 %+v", resolved.Policy)
	}
	if resolved.Auto {
		t.Fatal("显式关掉的自动压缩又被打开了")
	}
}

func TestResolve验不过的几种配置(t *testing.T) {
	t.Parallel()

	for name, config := range map[string]Config{
		"压力线超出范围":      {PolicyConfig: PolicyConfig{ThresholdRatio: 1.5}},
		"压力线是负的":       {PolicyConfig: PolicyConfig{ThresholdRatio: -0.1}},
		"压力线是 NaN":     {PolicyConfig: PolicyConfig{ThresholdRatio: math.NaN()}},
		"压力线是无穷":       {PolicyConfig: PolicyConfig{ThresholdRatio: math.Inf(1)}},
		"保留比例超出范围":     {PolicyConfig: PolicyConfig{RetainRatio: 2}},
		"保留 token 是负的": {PolicyConfig: PolicyConfig{RetainTokens: intOf(-1)}},
		"两种保留一起给了": {PolicyConfig: PolicyConfig{
			RetainRatio: 0.1, RetainTokens: intOf(100),
		}},
		"生成上限是负的":        {PolicyConfig: PolicyConfig{MaxTokens: -1}},
		"重试次数是负的":        {PolicyConfig: PolicyConfig{CompactionRetries: intOf(-1)}},
		"超窗补救次数是负的":      {PolicyConfig: PolicyConfig{MaxOverflowRetries: intOf(-1)}},
		"摘要路由只给了一半":      {PolicyConfig: PolicyConfig{Summarization: &Target{Provider: "openai"}}},
		"保留比例不小于压力线":     {PolicyConfig: PolicyConfig{ThresholdRatio: 0.5, RetainRatio: 0.5}},
		"覆盖的 provider 空": {ModelPolicies: []ModelPolicyConfig{{Target: Target{Model: "m"}}}},
		"覆盖的 model 空":    {ModelPolicies: []ModelPolicyConfig{{Target: Target{Provider: "p"}}}},
		"覆盖自己的比例不合法": {ModelPolicies: []ModelPolicyConfig{{
			PolicyConfig: PolicyConfig{ThresholdRatio: 3},
			Target:       Target{Provider: "p", Model: "m"},
		}}},
		"同一条路由有两条覆盖": {ModelPolicies: []ModelPolicyConfig{
			{Target: Target{Provider: "p", Model: "m"}},
			{Target: Target{Provider: "p", Model: "m"}},
		}},
		// 覆盖只改了保留比例、没改压力线，冲突要在这一步就报出来——这两个数的关系
		// 和模型窗口无关，等到折算那一步再报就成了一条只在某些模型上出现的失败。
		"覆盖只改保留就和继承来的压力线冲突": {
			PolicyConfig: PolicyConfig{ThresholdRatio: 0.5},
			ModelPolicies: []ModelPolicyConfig{{
				PolicyConfig: PolicyConfig{RetainRatio: 0.6},
				Target:       Target{Provider: "p", Model: "m"},
			}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := config.Resolve(); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("报的是 %v", err)
			}
		})
	}
}

func TestResolve摘要路由(t *testing.T) {
	t.Parallel()

	resolved, err := Config{PolicyConfig: PolicyConfig{
		Summarization: &Target{Provider: "openai", Model: "gpt-4o-mini"},
	}}.Resolve()
	if err != nil {
		t.Fatalf("验不过：%v", err)
	}
	if resolved.Summarization != (Target{Provider: "openai", Model: "gpt-4o-mini"}) {
		t.Fatalf("摘要路由是 %+v", resolved.Summarization)
	}
	// 两个字段都空是「清掉」的意思，不是写错了。
	cleared, err := Config{PolicyConfig: PolicyConfig{Summarization: &Target{}}}.Resolve()
	if err != nil {
		t.Fatalf("验不过：%v", err)
	}
	if !cleared.Summarization.IsZero() {
		t.Fatalf("清掉之后是 %+v", cleared.Summarization)
	}
}

func TestForTarget没有覆盖时原样用默认策略(t *testing.T) {
	t.Parallel()

	resolved, err := Config{}.Resolve()
	if err != nil {
		t.Fatalf("验不过：%v", err)
	}
	target := Target{Provider: "openai", Model: "gpt-4o"}
	policy := resolved.ForTarget(target)
	if policy.Target != target || policy.Policy != resolved.Policy ||
		policy.Retention != resolved.Retention {
		t.Fatalf("合出来的是 %+v", policy)
	}
}

func TestForTarget只盖住覆盖说了的那几个字段(t *testing.T) {
	t.Parallel()

	resolved, err := Config{
		PolicyConfig: PolicyConfig{
			ThresholdRatio: 0.9,
			RetainRatio:    0.2,
			MaxTokens:      4096,
			Summarization:  &Target{Provider: "openai", Model: "gpt-4o-mini"},
		},
		ModelPolicies: []ModelPolicyConfig{{
			PolicyConfig: PolicyConfig{
				RetainTokens:      intOf(1000),
				CompactionRetries: intOf(3),
				// 空的一对：这条路由上不要那个全局的摘要模型，跟着对话自己走。
				Summarization: &Target{},
			},
			Target: Target{Provider: "openai", Model: "gpt-4o"},
		}},
	}.Resolve()
	if err != nil {
		t.Fatalf("验不过：%v", err)
	}

	policy := resolved.ForTarget(Target{Provider: "openai", Model: "gpt-4o"})
	if policy.ThresholdRatio != 0.9 || policy.MaxTokens != 4096 {
		t.Fatalf("没被覆盖的字段变了：%+v", policy.Policy)
	}
	if policy.Retention.ByRatio() || policy.Retention.Tokens != 1000 {
		t.Fatalf("保留是 %+v", policy.Retention)
	}
	if policy.CompactionRetries != 3 || policy.MaxOverflowRetries != DefaultMaxOverflowRetries {
		t.Fatalf("两个计数是 %+v", policy.Policy)
	}
	if !policy.Summarization.IsZero() {
		t.Fatalf("那条覆盖没把继承来的摘要路由清掉：%+v", policy.Summarization)
	}

	// 别的路由不受这条覆盖影响。
	other := resolved.ForTarget(Target{Provider: "openai", Model: "gpt-4o-mini"})
	if !other.Retention.ByRatio() || other.Retention.Ratio != 0.2 {
		t.Fatalf("另一条路由的保留是 %+v", other.Retention)
	}
	if other.Summarization.IsZero() {
		t.Fatal("另一条路由的摘要路由也被清掉了")
	}
}

func TestSpec折算成具体预算(t *testing.T) {
	t.Parallel()

	resolved, err := Config{PolicyConfig: PolicyConfig{
		ThresholdRatio: 0.8, RetainRatio: 0.16,
	}}.Resolve()
	if err != nil {
		t.Fatalf("验不过：%v", err)
	}
	target := Target{Provider: "openai", Model: "gpt-4o"}
	spec, err := resolved.ForTarget(target).Spec(100_000)
	if err != nil {
		t.Fatalf("算不出来：%v", err)
	}
	if spec.ContextWindow != 100_000 || spec.ThresholdTokens != 80_000 || spec.RetainTokens != 16_000 {
		t.Fatalf("算出来的是 %+v", spec)
	}
	if spec.Target != target {
		t.Fatalf("路由是 %+v", spec.Target)
	}
}

func TestSpec绝对保留预算不按窗口缩放(t *testing.T) {
	t.Parallel()

	resolved, err := Config{PolicyConfig: PolicyConfig{RetainTokens: intOf(1234)}}.Resolve()
	if err != nil {
		t.Fatalf("验不过：%v", err)
	}
	spec, err := resolved.ForTarget(Target{Provider: "p", Model: "m"}).Spec(10_000)
	if err != nil {
		t.Fatalf("算不出来：%v", err)
	}
	if spec.RetainTokens != 1234 {
		t.Fatalf("保留预算是 %d", spec.RetainTokens)
	}
}

func TestSpec算不出来时报的是路由那一类失败(t *testing.T) {
	t.Parallel()

	for name, item := range map[string]struct {
		config        Config
		contextWindow int
	}{
		"窗口是零":  {Config{}, 0},
		"窗口是负的": {Config{}, -1},
		// 留的比压力线还多：压完一次仍然在线上，下一步又去压，成了一个永远降不到
		// 线下、每步都做一次总结调用的循环。
		"绝对保留预算大过压力线": {Config{PolicyConfig: PolicyConfig{RetainTokens: intOf(9000)}}, 10_000},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			resolved, err := item.config.Resolve()
			if err != nil {
				t.Fatalf("验不过：%v", err)
			}
			_, err = resolved.ForTarget(Target{Provider: "p", Model: "m"}).Spec(item.contextWindow)
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("报的不是配置失败：%v", err)
			}
			// 调用方靠这个类型对同一条路由只警告一次，所以它必须取得出来。
			var pressure *TargetPressureError
			if !errors.As(err, &pressure) {
				t.Fatalf("取不出路由：%v", err)
			}
			if pressure.TargetKey != "p/m" {
				t.Fatalf("路由是 %q", pressure.TargetKey)
			}
			if pressure.Error() == "" {
				t.Fatal("错误文案是空的")
			}
		})
	}
}
