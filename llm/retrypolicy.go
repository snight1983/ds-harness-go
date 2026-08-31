// 本文件的作用：一条提供方路由自己拥有的那份请求重试策略——怎么写、怎么解算成
// 一份登记时就定死的策略。
//
// 源: packages/llm/llm/src/retry-policy.ts
//
// 适配器为每一条登记的提供方路由交出一份解算好的策略；真正**执行**它的是重试
// 那个插件，在 agent 的失败步骤扩展点上。本包只负责把一份随手写的配置验成一份
// 没有歧义的策略。

package llm

import (
	"fmt"
	"math"
	"slices"
	"time"
)

// 源: packages/llm/llm/src/retry-policy.ts:14-24。
//
// 新增: DSH 这几个常量不导出，本包照旧——它们是默认值，不是给调用方拿去比较的
// 契约。要知道生效值就读 [ResolveRetryPolicy] 交出来的那份。
const (
	defaultMaxRetries   = 5
	defaultInitialDelay = 500 * time.Millisecond
	defaultMaxDelay     = 10 * time.Second
	defaultJitterRatio  = 0.1
)

// defaultRetryableCodes 是默认认为可以安全重来的那几个失败码。
//
// 源: packages/llm/llm/src/retry-policy.ts:18-24
//
// 每次调用都新造一份：DSH 那边靠 Object.freeze 挡住调用方改动，Go 里没有冻结，
// 交出一份自己的复制是等价的做法。
func defaultRetryableCodes() []string {
	return []string{EmptyResponseCode, "RATE_LIMIT", "SERVER", "TIMEOUT", "TRANSPORT"}
}

// RetryMode 是一条路由重试的档位。
//
// 源: packages/llm/llm/src/retry-policy.ts:39、51
//
// 新增: DSH 那两份配置是靠 mode 这个字面量判别的联合。Go 里把判别标签单独命名，
// 两份配置合成一个结构体——见 [RetryPolicyConfig] 上的理由。
type RetryMode string

const (
	// RetryNormal 只重试配置里点了名的那几个瞬时失败码。
	RetryNormal RetryMode = "normal"
	// RetryAlways 重试这条路由上每一次模型请求失败，直到成功、被取消、或者被处置。
	RetryAlways RetryMode = "always"
)

// BackoffConfig 是有界的指数退避，外加每一次本地延时上下对称的抖动。
//
// 源: packages/llm/llm/src/retry-policy.ts:26-34
type BackoffConfig struct {
	// InitialDelay 是本地指数退避的第一段延时；0 表示没给，默认 500 毫秒。
	//
	// 新增: DSH 是可选的毫秒数。这里用 0 表示「没给」不会和真实取值撞车——
	// 解算会拒掉任何不是正数的延时，所以 0 本来就不是一个能生效的值。
	InitialDelay time.Duration
	// MaxDelay 是本地排期、以及能接受的提供方延时的上限；0 表示没给，默认 10 秒。
	MaxDelay time.Duration
	// JitterRatio 是围绕 1 的对称随机倍率范围；nil 表示没给，默认 0.1。
	//
	// 新增: 这里必须用指针。0 是一个**有意义的取值**（完全不抖动），和「没给
	// 抖动比例」是两件事，而后者要落到 0.1 上。这是本包区分零值与缺失的那条
	// 判据的又一次应用，同 [CallConfig.Temperature]。
	JitterRatio *float64
}

// RetryPolicyConfig 是一条提供方路由自己拥有的那份模型请求重试配置。
//
// 源: packages/llm/llm/src/retry-policy.ts:36-57
//
// 新增: DSH 是 NormalRetryPolicyConfig | AlwaysRetryPolicyConfig 两份配置的联合，
// 各自只带自己那一档用得上的字段。Go 里合成一个结构体，因为 DSH 自己在
// retry-policy.ts:108-112 上写明了这一点：分层配置在切换档位之后会**留着**
// normal 那两个字段，always 档只是忽略这些失效的值，并不拒绝它们。也就是说
// 两份配置的合法键集合本来就一模一样，那个联合分的只是「哪几个字段这一档在读」。
// 一个带判别标签的结构体把这件事说得更直白。
type RetryPolicyConfig struct {
	// Mode 是这条路由的重试档位，必填。
	Mode RetryMode
	// MaxRetries 是第一次请求之后还能重试几次；nil 表示没给，默认 5。
	// Mode 不是 [RetryNormal] 时它不生效。
	//
	// 新增: 用指针的理由同 [BackoffConfig.JitterRatio]——0 是合法且有意义的
	//（一次都不重试），不能拿它表示「没给」。
	MaxRetries *int
	// RetryableCodes 是这一档认的那些稳定失败码；nil 表示没给，用默认集合。
	// Mode 不是 [RetryNormal] 时它不生效。
	//
	// nil 与**长度为零的切片**是两件事：前者取默认集合，后者是明确给了一个空
	// 清单，而那会被拒——一个不认任何码的 normal 策略等价于不重试，让人把
	// MaxRetries 写成 0 说清楚。
	RetryableCodes []string
	// Backoff 是本地指数退避与抖动的配置，零值表示三项全都没给。
	Backoff BackoffConfig
}

// ResolvedRetryBackoff 是两档共用的、已经解算完的退避。
//
// 源: packages/llm/llm/src/retry-policy.ts:59-64
type ResolvedRetryBackoff struct {
	// InitialDelay 是第一段延时，保证是正数。
	InitialDelay time.Duration
	// MaxDelay 是延时上限，保证是正数且不小于 InitialDelay。
	MaxDelay time.Duration
	// JitterRatio 是抖动比例，保证落在 [0, 1] 里。
	JitterRatio float64
}

// ResolvedRetryPolicy 是一条适配器路由登记那一刻定下来的那份策略。
//
// 源: packages/llm/llm/src/retry-policy.ts:66-79
//
// 新增: DSH 是 ResolvedNormalRetryPolicy | ResolvedAlwaysRetryPolicy 的联合，
// always 那一支干脆没有那两个字段。Go 里合成一个结构体，[ResolvedRetryPolicy.Mode]
// 就是那个判别标签：Mode 为 [RetryAlways] 时 MaxRetries 与 RetryableCodes 没有
// 意义，别去读。理由和 [RetryPolicyConfig] 上那条一样。
type ResolvedRetryPolicy struct {
	// Mode 是这份策略的档位，一定是那两个之一。
	Mode RetryMode

	// ResolvedRetryBackoff 是两档共用的退避，内嵌以便直接读它那三个字段——
	// DSH 那两个 Resolved 接口也是 extends 它，取的字段名一样。
	ResolvedRetryBackoff

	// MaxRetries 是第一次请求之后还能重试几次，保证不是负数。
	//
	// Mode 不是 [RetryNormal] 时它没有意义。
	MaxRetries int
	// RetryableCodes 是这一档认的那些稳定失败码，保证非空、逐条非空、且不重复。
	//
	// Mode 不是 [RetryNormal] 时它没有意义。
	//
	// 交回的是一份自己拥有的复制，但**把它当只读的**：本包不再看它一眼，
	// 改动只会影响持有这份策略的调用方自己。
	RetryableCodes []string
}

// ResolveRetryPolicy 验一份提供方自己拥有的重试配置、填上默认值、并和调用方脱钩。
//
// 源: packages/llm/llm/src/retry-policy.ts:143-195
//
// config 为 nil 表示这条路由没写策略，取 normal 档的整套默认值。path 是诊断里
// 用来点名「是哪一份提供方配置带着这个值」的路径。
//
// 新增: DSH 头一件事是遍历配置对象的键、拒绝表外的键（validateKeys）。Go 的
// 结构体写不出表外的字段，那一步连同它那三张键表一起不需要。理由和
// mcp/config.go 里 resolveReconnectPolicy 上那条逐字相同。
func ResolveRetryPolicy(config *RetryPolicyConfig, path string) (ResolvedRetryPolicy, error) {
	if config == nil {
		// 源: packages/llm/llm/src/retry-policy.ts:153-160。没给配置走的是 normal
		// 的整套默认值，而不是「不重试」。
		//
		// 新增: DSH 在这一支里把那四个默认值又写了一遍。这里改成走 normal 的解算：
		// 一份三个可选字段全缺的 normal 配置，逐字就是那四个默认值，而重复一遍
		// 意味着默认值有两处定义、可以各自漂移。
		return resolveNormal(RetryPolicyConfig{Mode: RetryNormal}, path)
	}

	switch config.Mode {
	case RetryNormal:
		return resolveNormal(*config, path)

	case RetryAlways:
		// 源: packages/llm/llm/src/retry-policy.ts:186-191。always 档只解算退避，
		// normal 那两个字段哪怕写了也一眼不看。
		backoff, err := resolveBackoff(config.Backoff, path+".backoff")
		if err != nil {
			return ResolvedRetryPolicy{}, err
		}
		return ResolvedRetryPolicy{Mode: RetryAlways, ResolvedRetryBackoff: backoff}, nil

	default:
		// 源: packages/llm/llm/src/retry-policy.ts:192-193。零值的 Mode（空串）
		// 也落在这里：一份没填档位的配置和一份填错档位的配置要改的是同一个字段。
		return ResolvedRetryPolicy{}, fmt.Errorf(
			"%w：%s.Mode 只能是 %q 或者 %q，收到 %q",
			ErrInvalidConfig, path, RetryNormal, RetryAlways, config.Mode)
	}
}

// resolveNormal 解算 normal 那一档独有的两个字段。
//
// 源: packages/llm/llm/src/retry-policy.ts:163-185
func resolveNormal(config RetryPolicyConfig, path string) (ResolvedRetryPolicy, error) {
	maxRetries := defaultMaxRetries
	if config.MaxRetries != nil {
		maxRetries = *config.MaxRetries
	}
	// 新增: DSH 这里还验 Number.isSafeInteger。Go 的 int 天生是精确整数，
	// 那一半不存在，只剩下界。
	if maxRetries < 0 {
		return ResolvedRetryPolicy{}, fmt.Errorf(
			"%w：%s.MaxRetries 不能是负数，收到 %d", ErrInvalidConfig, path, maxRetries)
	}

	codes := config.RetryableCodes
	if codes == nil {
		codes = defaultRetryableCodes()
	} else {
		codes = slices.Clone(codes)
	}
	if len(codes) == 0 {
		return ResolvedRetryPolicy{}, fmt.Errorf(
			"%w：%s.RetryableCodes 不能是空清单", ErrInvalidConfig, path)
	}
	// 新增: DSH 那条 typeof code !== 'string' 在 Go 里不需要——切片的元素类型
	// 已经保证了它是字符串，只剩「不能是空串」这一半。
	seen := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		if code == "" {
			return ResolvedRetryPolicy{}, fmt.Errorf(
				"%w：%s.RetryableCodes 里不能有空串", ErrInvalidConfig, path)
		}
		if _, duplicate := seen[code]; duplicate {
			return ResolvedRetryPolicy{}, fmt.Errorf(
				"%w：%s.RetryableCodes 里 %q 重复了", ErrInvalidConfig, path, code)
		}
		seen[code] = struct{}{}
	}

	backoff, err := resolveBackoff(config.Backoff, path+".backoff")
	if err != nil {
		return ResolvedRetryPolicy{}, err
	}
	return ResolvedRetryPolicy{
		Mode:                 RetryNormal,
		ResolvedRetryBackoff: backoff,
		MaxRetries:           maxRetries,
		RetryableCodes:       codes,
	}, nil
}

// resolveBackoff 验那三项退避、填上默认值。
//
// 源: packages/llm/llm/src/retry-policy.ts:121-141
//
// 新增: DSH 收的是 `BackoffConfig | undefined`。Go 里零值的 [BackoffConfig] 就是
// 「三项全没给」，和 undefined 完全同义（键表校验已经不需要了），所以收值不收指针。
//
// 新增: DSH 那三条 Number.isFinite 与两条 MAX_TIMER_DELAY_MS 上界都不移。前者是因为
// [time.Duration] 是 int64，没有 NaN 和无穷；后者是因为那个上界只为 Node 存在——
// setTimeout 的延迟超过 32 位有符号整数会被悄悄压成 1 毫秒，于是「设了一个很长的
// 退避」会变成「立刻重试」。Go 的 [time.Timer] 收 int64 纳秒，没有这个悬崖。
// 抖动比例是 float64，NaN 还在，所以那一条留着。
func resolveBackoff(config BackoffConfig, path string) (ResolvedRetryBackoff, error) {
	backoff := ResolvedRetryBackoff{
		InitialDelay: config.InitialDelay,
		MaxDelay:     config.MaxDelay,
		JitterRatio:  defaultJitterRatio,
	}
	if backoff.InitialDelay == 0 {
		backoff.InitialDelay = defaultInitialDelay
	}
	if backoff.MaxDelay == 0 {
		backoff.MaxDelay = defaultMaxDelay
	}
	if config.JitterRatio != nil {
		backoff.JitterRatio = *config.JitterRatio
	}

	if backoff.InitialDelay <= 0 {
		return ResolvedRetryBackoff{}, fmt.Errorf(
			"%w：%s.InitialDelay 必须是正数，收到 %s", ErrInvalidConfig, path, backoff.InitialDelay)
	}
	if backoff.MaxDelay <= 0 {
		return ResolvedRetryBackoff{}, fmt.Errorf(
			"%w：%s.MaxDelay 必须是正数，收到 %s", ErrInvalidConfig, path, backoff.MaxDelay)
	}
	if backoff.InitialDelay > backoff.MaxDelay {
		return ResolvedRetryBackoff{}, fmt.Errorf(
			"%w：%s.InitialDelay 不能大于 MaxDelay（%s > %s）",
			ErrInvalidConfig, path, backoff.InitialDelay, backoff.MaxDelay)
	}
	if math.IsNaN(backoff.JitterRatio) || backoff.JitterRatio < 0 || backoff.JitterRatio > 1 {
		return ResolvedRetryBackoff{}, fmt.Errorf(
			"%w：%s.JitterRatio 必须落在 0 到 1 之间，收到 %v",
			ErrInvalidConfig, path, backoff.JitterRatio)
	}
	return backoff, nil
}
