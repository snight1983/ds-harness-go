// 本文件的作用：这一层的配置词汇，以及把它补完默认值、验一遍、按路由合并、
// 再按模型窗口折算成具体 token 预算的那三步。
//
// 源: packages/compaction/compaction-basic/src/types.ts
// 源: packages/compaction/compaction-basic/src/config.ts

package basic

import (
	"fmt"
	"math"
)

// 默认的压力线和保留尾巴，两个都是模型窗口的比例。
//
// 源: packages/compaction/compaction-basic/src/config.ts:20-23
const (
	// DefaultThresholdRatio 是「用到窗口的百分之多少就该压一次」。
	DefaultThresholdRatio = 0.8
	// DefaultRetainRatio 是「最近多大一段原样留着不动」。
	DefaultRetainRatio = 0.16
)

// 另外三个不按比例走的默认值。
//
// 源: packages/compaction/compaction-basic/src/config.ts:91-93
const (
	// DefaultMaxTokens 是一次总结调用的生成上限。
	DefaultMaxTokens = 8192
	// DefaultCompactionRetries 是压完一次还在线上时，额外再压几次。
	DefaultCompactionRetries = 1
	// DefaultMaxOverflowRetries 是提供方报了「超窗」之后最多补救几次。
	DefaultMaxOverflowRetries = 1
)

// Target 是一条具体的模型路由。
//
// 源: packages/compaction/compaction-basic/src/types.ts:32-34
//
// 新增: DSH 写成 `Pick<LlmCallConfig, 'provider' | 'model'>`。Go 没有这种类型
// 运算，而 [llm.CallConfig] 整个搬进来只用两个字段是白搭——这里按本仓库
// 「消费方自己声明它需要的那一小片」的一贯做法，现声明一个两字段的值。
// 它同时当 map 的键用，替掉 DSH 那个 `${provider} ${model}` 拼串。
type Target struct {
	// Provider 是注册的提供方路由名。
	Provider string
	// Model 是那个提供方下面的确切模型 id。
	Model string
}

// Key 把一条路由写成 "provider/model"，用在错误文案和警告去重上。
//
// 源: packages/compaction/compaction-basic/src/config.ts:137
func (t Target) Key() string { return t.Provider + "/" + t.Model }

// IsZero 判断这条路由是不是「没说」。
func (t Target) IsZero() bool { return t.Provider == "" && t.Model == "" }

// PolicyConfig 是默认策略和按模型覆盖共用的那几个字段。
//
// 源: packages/compaction/compaction-basic/src/types.ts:9-27（CompactionPolicyConfig）
//
// 全部可选：零值（或 nil）表示「这一层不说话」，由上一层或默认值补上。
//
// 新增: 三个计数字段是 *int 而不是 int。理由是它们的**零是有意义的**——
// RetainTokens 为 0 表示一点尾巴都不留，CompactionRetries 为 0 表示压一次就不再试，
// MaxOverflowRetries 为 0 表示整个关掉超窗补救。拿 0 当「没给」会把这三种
// 明确的意思静默改写成默认值，而默认值恰好都不是 0。比例和 MaxTokens 不需要
// 指针：合法取值分别是 (0,1] 和 ≥1，0 本来就不在里面。
type PolicyConfig struct {
	// ThresholdRatio 是压力线占模型窗口的比例，取值 (0,1]；零取上一层的值。
	ThresholdRatio float64
	// RetainRatio 是保留尾巴占模型窗口的比例，取值 (0,1]；零取上一层的值。
	//
	// 和 RetainTokens 互斥。
	RetainRatio float64
	// RetainTokens 是保留尾巴的绝对预算，非负；nil 表示这一层不说话。
	//
	// 和 RetainRatio 互斥。
	RetainTokens *int
	// Summarization 是写摘要用的那条路由；nil 表示这一层不说话。
	//
	// 指向一个两个字段都为空的 [Target] 表示**显式清掉**上一层配的摘要路由，
	// 回落到对话本身当前路由的那个模型。
	//
	// 新增: DSH 是 summarizationProvider / summarizationModel 两个可选字符串，
	// 外加一条「要么都不给、要么一起给空串、要么一起给非空」的成对校验。
	// Go 这边合成一个指针字段：成对这件事由类型担着，校验只剩「非 nil 时两个
	// 字段要么都空要么都不空」。空串和「没给」在 Go 里分不开，而 DSH 那个
	// 空串对（按模型覆盖时用来清掉全局摘要路由）是真有用的，所以留了这一层指针。
	Summarization *Target
	// MaxTokens 是总结调用的生成上限，正数；零取上一层的值。
	MaxTokens int
	// CompactionRetries 是压完一次仍在线上时额外再压几次，非负；nil 表示不说话。
	CompactionRetries *int
	// MaxOverflowRetries 是超窗之后最多补救几次，非负；nil 表示不说话。
	MaxOverflowRetries *int
}

// ModelPolicyConfig 是盖在默认策略上的一条按模型覆盖。
//
// 源: packages/compaction/compaction-basic/src/types.ts:29-35（ModelCompactPolicyConfig）
type ModelPolicyConfig struct {
	// PolicyConfig 是这条覆盖要改的那些字段；没给的继承默认策略。
	PolicyConfig
	// Target 是这条覆盖匹配的确切路由，两个字段都不许为空。
	Target Target
}

// Config 是这一层的完整配置。
//
// 源: packages/compaction/compaction-basic/src/types.ts:37-43（BasicCompactionConfig）
type Config struct {
	// PolicyConfig 是默认策略。
	PolicyConfig
	// ModelPolicies 是按模型的覆盖表；同一条路由出现两次会被 [Config.Resolve] 拒掉。
	ModelPolicies []ModelPolicyConfig
	// Auto 打开自动压缩（步骤边界的压力检查和超窗补救）；nil 取默认的「打开」。
	//
	// 新增: 指针，理由同 [PolicyConfig] 那三个计数字段——false 是有意义的，
	// 而默认值是 true。
	Auto *bool
}

// Retention 是那两种保留形式里的**恰好一种**。
//
// 源: packages/compaction/compaction-basic/src/types.ts:45-48（ResolvedRetention）
//
// 新增: DSH 是一个「两个字段互相 never」的排他联合。Go 没有这种类型，
// 但这里不需要额外的标志位：Ratio 的合法取值是 (0,1]，所以 Ratio 为零
// 就**只能**意味着这份保留是按绝对 token 数算的。见 [Retention.ByRatio]。
type Retention struct {
	// Ratio 是按模型窗口的比例保留，取值 (0,1]；为零时这一种不生效。
	Ratio float64
	// Tokens 是绝对保留预算；Ratio 为零时用这一个。
	Tokens int
}

// ByRatio 判断这份保留走的是比例那一种。
func (r Retention) ByRatio() bool { return r.Ratio > 0 }

// Policy 是补完默认值、验过之后那几个和路由无关的策略字段。
//
// 源: packages/compaction/compaction-basic/src/types.ts:50-58（ResolvedPolicyFields）
//
// 新增: DSH 那个是模块私有的 interface，只用来给两个导出类型做交叉。Go 里
// 匿名嵌入要求它是个具名类型，所以它跟着导出了。
type Policy struct {
	// ThresholdRatio 落在 (0,1]。
	ThresholdRatio float64
	// Summarization 是写摘要用的路由；零值表示跟着对话当前的路由走。
	Summarization Target
	// MaxTokens 是正数。
	MaxTokens int
	// CompactionRetries 非负。
	CompactionRetries int
	// MaxOverflowRetries 非负。
	MaxOverflowRetries int
}

// ResolvedConfig 是验过的配置：默认策略已经补全，按模型的覆盖还没合并进去。
//
// 源: packages/compaction/compaction-basic/src/types.ts:60-64（ResolvedConfig）
//
// 新增: 构造它的唯一入口是 [Config.Resolve]，一份没验过的配置在类型上就传不进来
// ——和 context/sessionref、context/timecontext 那两份解析后配置同一个理由。
type ResolvedConfig struct {
	// Policy 是补全之后的默认策略。
	Policy
	// Retention 是补全之后的默认保留形式。
	Retention Retention
	// ModelPolicies 是验过、去过重的按模型覆盖表。
	ModelPolicies []ModelPolicyConfig
	// Auto 表示自动压缩开着。
	Auto bool
}

// TargetPolicy 是某一条路由合并完覆盖之后的策略，还没按模型窗口折算。
//
// 源: packages/compaction/compaction-basic/src/types.ts:66-69（ResolvedTargetPolicy）
type TargetPolicy struct {
	// Policy 是合并之后的策略字段。
	Policy
	// Retention 是合并之后的保留形式。
	Retention Retention
	// Target 是这份策略对应的那条路由。
	Target Target
}

// Spec 是某一条路由折算成具体 token 预算之后的压力和保留档。
//
// 源: packages/compaction/compaction-basic/src/types.ts:71-76（ResolvedCompactSpec）
type Spec struct {
	// Policy 是这条路由的策略字段。
	Policy
	// Target 是这份预算对应的那条路由。
	Target Target
	// ContextWindow 是这个模型的窗口大小，正数。
	ContextWindow int
	// ThresholdTokens 是压力线，超过它就该压一次。
	ThresholdTokens int
	// RetainTokens 是保留尾巴的预算，严格小于 ThresholdTokens。
	RetainTokens int
}

// Resolve 补上默认值并把整份配置验一遍。
//
// 源: packages/compaction/compaction-basic/src/config.ts:67-97
//
// DSH 在插件装载时做这件事，验不过插件就装不上。Go 里没有那个装载时机，
// 所以挪到构造配置的地方——性质一样：验不过就没有一份可用的配置，
// 而不是留一个「参数配错了照样每一步都去压一次」的运行期。
func (c Config) Resolve() (ResolvedConfig, error) {
	if err := validatePolicy(c.PolicyConfig, "配置"); err != nil {
		return ResolvedConfig{}, err
	}

	thresholdRatio := defaultedRatio(c.ThresholdRatio, DefaultThresholdRatio)
	retention := resolveRetention(c.PolicyConfig, Retention{Ratio: DefaultRetainRatio})
	if err := validateRatioRetention(thresholdRatio, retention, "配置"); err != nil {
		return ResolvedConfig{}, err
	}

	policies, err := resolveModelPolicies(c.ModelPolicies)
	if err != nil {
		return ResolvedConfig{}, err
	}
	for index, policy := range policies {
		// 一条覆盖只改了保留比例、没改压力线（或者反过来）时，冲突要现在就报出来。
		// 这两个数的关系是**和模型窗口无关**的，等到折算那一步再报，就变成了
		// 一条只在某些模型上才出现的失败。
		name := fmtIndex("配置的 modelPolicies", index)
		if err := validateRatioRetention(
			defaultedRatio(policy.ThresholdRatio, thresholdRatio),
			resolveRetention(policy.PolicyConfig, retention),
			name,
		); err != nil {
			return ResolvedConfig{}, err
		}
	}

	return ResolvedConfig{
		Policy: Policy{
			ThresholdRatio:     thresholdRatio,
			Summarization:      resolveSummarization(c.Summarization, Target{}),
			MaxTokens:          defaultedInt(c.MaxTokens, DefaultMaxTokens),
			CompactionRetries:  defaultedCount(c.CompactionRetries, DefaultCompactionRetries),
			MaxOverflowRetries: defaultedCount(c.MaxOverflowRetries, DefaultMaxOverflowRetries),
		},
		Retention:     retention,
		ModelPolicies: policies,
		Auto:          c.Auto == nil || *c.Auto,
	}, nil
}

// ForTarget 把某条路由的覆盖合并到默认策略上。
//
// 源: packages/compaction/compaction-basic/src/config.ts:105-125
//
// 不返回错误：覆盖表在 [Config.Resolve] 里已经整个验过了，这里只是取值和合并。
func (c ResolvedConfig) ForTarget(target Target) TargetPolicy {
	var override *ModelPolicyConfig
	for index := range c.ModelPolicies {
		if c.ModelPolicies[index].Target == target {
			override = &c.ModelPolicies[index]
			break
		}
	}
	if override == nil {
		return TargetPolicy{Policy: c.Policy, Retention: c.Retention, Target: target}
	}
	return TargetPolicy{
		Policy: Policy{
			ThresholdRatio:     defaultedRatio(override.ThresholdRatio, c.ThresholdRatio),
			Summarization:      resolveSummarization(override.Summarization, c.Summarization),
			MaxTokens:          defaultedInt(override.MaxTokens, c.MaxTokens),
			CompactionRetries:  defaultedCount(override.CompactionRetries, c.CompactionRetries),
			MaxOverflowRetries: defaultedCount(override.MaxOverflowRetries, c.MaxOverflowRetries),
		},
		Retention: resolveRetention(override.PolicyConfig, c.Retention),
		Target:    target,
	}
}

// Spec 按这个模型的窗口大小，把一份策略折算成具体的 token 预算。
//
// 源: packages/compaction/compaction-basic/src/config.ts:133-167
//
// 窗口大小来自适配器那一侧，所以这一步的失败都是 [TargetPressureError]：
// 调用方对同一条路由只警告一次。
func (p TargetPolicy) Spec(contextWindow int) (Spec, error) {
	key := p.Target.Key()
	if contextWindow <= 0 {
		// 新增: DSH 还要查 Number.isInteger——JS 的 number 是浮点，一个 0.5 或者
		// 2^53 之外的整数都进得来。Go 的 int 已经把这两件事挡掉了，只剩这一半。
		return Spec{}, targetPressureFailure(key,
			"%s 的 contextWindow 是 %d，必须是正整数", key, contextWindow)
	}
	thresholdTokens := int(math.Floor(float64(contextWindow) * p.ThresholdRatio))
	retainTokens := p.Retention.Tokens
	if p.Retention.ByRatio() {
		retainTokens = int(math.Floor(float64(contextWindow) * p.Retention.Ratio))
	}
	if retainTokens >= thresholdTokens {
		// 留的比压力线还多，那么压完一次仍然在线上，下一步又会去压——
		// 一个每步都做一次总结调用、却永远降不到线下的循环。
		return Spec{}, targetPressureFailure(key,
			"%s 的保留预算是 %d 个 token，必须小于压力线 %d", key, retainTokens, thresholdTokens)
	}
	return Spec{
		Policy:          p.Policy,
		Target:          p.Target,
		ContextWindow:   contextWindow,
		ThresholdTokens: thresholdTokens,
		RetainTokens:    retainTokens,
	}, nil
}

// resolveRetention 在一层配置里挑出它明说的保留形式，没说就用上一层的。
//
// 源: packages/compaction/compaction-basic/src/config.ts:170-177
func resolveRetention(config PolicyConfig, fallback Retention) Retention {
	if config.RetainTokens != nil {
		return Retention{Tokens: *config.RetainTokens}
	}
	if config.RetainRatio != 0 {
		return Retention{Ratio: config.RetainRatio}
	}
	return fallback
}

// resolveSummarization 在一层配置里挑出它明说的摘要路由，没说就用上一层的。
//
// 指向零值 [Target] 表示显式清掉，于是**继承到的也一起清掉**——这正是
// DSH 那个「空串对」的意思。
func resolveSummarization(configured *Target, fallback Target) Target {
	if configured == nil {
		return fallback
	}
	return *configured
}

// validateRatioRetention 拒掉一处和模型窗口无关的保留冲突。
//
// 源: packages/compaction/compaction-basic/src/config.ts:180-191
//
// 只在保留走比例那一种时查得了：绝对预算和窗口大小的关系要等到
// [TargetPolicy.Spec] 才算得出来。
func validateRatioRetention(thresholdRatio float64, retention Retention, name string) error {
	if retention.ByRatio() && retention.Ratio >= thresholdRatio {
		return configFailure("%s：retainRatio（%v）必须小于合并之后的 thresholdRatio（%v）",
			name, retention.Ratio, thresholdRatio)
	}
	return nil
}

// resolveModelPolicies 验一遍按模型的覆盖表，并拒掉重复的路由。
//
// 源: packages/compaction/compaction-basic/src/config.ts:194-212
//
// 重复要拒而不是后一条盖前一条：两条针对同一个模型、内容却不同的覆盖，
// 无论按哪个顺序取都有一条是**静默失效**的，而写配置的人看不出来。
func resolveModelPolicies(configured []ModelPolicyConfig) ([]ModelPolicyConfig, error) {
	if len(configured) == 0 {
		return nil, nil
	}
	seen := make(map[Target]struct{}, len(configured))
	policies := make([]ModelPolicyConfig, 0, len(configured))
	for index, policy := range configured {
		name := fmtIndex("配置的 modelPolicies", index)
		if policy.Target.Provider == "" || policy.Target.Model == "" {
			return nil, configFailure("%s 的 provider 和 model 都不能为空", name)
		}
		if err := validatePolicy(policy.PolicyConfig, name); err != nil {
			return nil, err
		}
		if _, duplicate := seen[policy.Target]; duplicate {
			return nil, configFailure("配置里 %s 有两条 modelPolicies", policy.Target.Key())
		}
		seen[policy.Target] = struct{}{}
		policies = append(policies, policy)
	}
	return policies, nil
}

// validatePolicy 验一层配置里那几个默认策略和按模型覆盖共用的字段。
//
// 源: packages/compaction/compaction-basic/src/config.ts:227-252
//
// 新增: DSH 那边这几个字段解出来是 unknown，所以每一条都要先查 typeof 再查取值，
// 还要有一个 validateKeys 去拒掉拼错的键。Go 这一侧类型由 [PolicyConfig] 钉死了，
// 拼错的字段编译期就过不去，剩下的只有取值范围这一半。
func validatePolicy(config PolicyConfig, name string) error {
	if config.ThresholdRatio != 0 {
		if err := validateRatio(name+".thresholdRatio", config.ThresholdRatio); err != nil {
			return err
		}
	}
	if config.RetainRatio != 0 {
		if err := validateRatio(name+".retainRatio", config.RetainRatio); err != nil {
			return err
		}
	}
	if config.RetainTokens != nil && *config.RetainTokens < 0 {
		return configFailure("%s.retainTokens（%d）必须是非负整数", name, *config.RetainTokens)
	}
	if config.RetainRatio != 0 && config.RetainTokens != nil {
		return configFailure("%s：retainRatio 和 retainTokens 互斥", name)
	}
	if config.MaxTokens < 0 {
		return configFailure("%s.maxTokens（%d）必须是正整数", name, config.MaxTokens)
	}
	if config.CompactionRetries != nil && *config.CompactionRetries < 0 {
		return configFailure("%s.compactionRetries（%d）必须是非负整数", name, *config.CompactionRetries)
	}
	if config.MaxOverflowRetries != nil && *config.MaxOverflowRetries < 0 {
		return configFailure("%s.maxOverflowRetries（%d）必须是非负整数", name, *config.MaxOverflowRetries)
	}
	return validateSummarization(config.Summarization, name)
}

// validateSummarization 要求一条摘要路由要么两个字段都给、要么两个都空。
//
// 源: packages/compaction/compaction-basic/src/config.ts:255-275
//
// 两个都空是**清掉**的意思，不是写错了；只给一半才是写错了——那样的配置
// 落到总结调用上会发出一个提供方和模型对不上的请求。
func validateSummarization(summarization *Target, name string) error {
	if summarization == nil {
		return nil
	}
	if (summarization.Provider == "") != (summarization.Model == "") {
		return configFailure("%s.summarization 的 provider 和 model 要么都给、要么都留空", name)
	}
	return nil
}

// validateRatio 要求一个比例落在 (0,1] 里。
//
// 源: packages/compaction/compaction-basic/src/config.ts:306-310
//
// 新增: DSH 那条 Number.isFinite 在 Go 这边仍然要留着——float64 一样有
// NaN 和 ±Inf，而 NaN 和任何数比较都是假，光靠上下界拦不住它。
func validateRatio(name string, value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 || value > 1 {
		return configFailure("%s（%v）必须是 (0, 1] 之间的一个数", name, value)
	}
	return nil
}

// defaultedRatio 把零当成「没给」补上上一层的比例。
func defaultedRatio(value float64, fallback float64) float64 {
	if value == 0 {
		return fallback
	}
	return value
}

// defaultedInt 把零当成「没给」补上上一层的值。
func defaultedInt(value int, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

// defaultedCount 取一个可选计数，nil 时用上一层的值。
func defaultedCount(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}

// fmtIndex 把「第几条覆盖」写成错误文案里的下标。
func fmtIndex(prefix string, index int) string {
	return fmt.Sprintf("%s[%d]", prefix, index)
}
