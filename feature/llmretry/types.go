// 本文件的作用：重试这一层的词汇——两条 llm/* 事件的类型、负载，以及它们在
// 介质上的样子。
//
// 源: packages/llm/llm-retry/src/types.ts、packages/llm/llm-retry/src/brand.ts

package llmretry

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

var (
	// ErrInvalidConfig 表示装配本身不成立，[Install] 或者 [RegisterInvariants] 当场被拒。
	ErrInvalidConfig = errors.New("llmretry: 配置不成立")
	// ErrMalformedEvent 表示一条 llm/retry* 事件的负载排不出去或者读不回来。
	ErrMalformedEvent = errors.New("llmretry: 事件负载不成立")
	// ErrInvariantViolated 表示一条事件违反了本包那几条持久不变量，见 [Trace.Validate]。
	ErrInvariantViolated = errors.New("llmretry: 违反重试不变量")
)

// 两条 llm/* 事件的类型。
//
// 源: packages/llm/llm-retry/src/types.ts:6-13
//
// 它们**都不上表面**：一次重试不改动模型看得见的历史，这两条只进日志。
//
// 分成两条而不是一条，是因为中间那段等待可以被取消：只写一条的话，一段等到一半
// 就被中止的重试，和一次真的重发出去了的请求，在日志里长得一模一样——而那两件事
// 对「这个步骤到底发过几次请求」是两个不同的答案。
const (
	// EventRetry 记下一次已经排好期、但那段等待还没开始的重试。
	EventRetry sessionlog.EventType = "llm/retry"
	// EventRetryStarted 记下那段等待熬过去了，下一次请求尝试就要发出去。
	EventRetryStarted sessionlog.EventType = "llm/retry-started"
)

// EventTypes 是本包往会话日志里写的那两种事件类型。
//
// 新增: DSH 靠 `declare module` 把这两个类型合并进 `SessionEventMap`，全局登记表
// 因此自动认得它们。Go 没有声明合并，[sessionlog.Vocabulary] 也是个闭合的值，
// 所以改成由本包交出这张单子，装配方自己拼：
//
//	vocabulary := sessionlog.CoreVocabulary().With(llmretry.EventTypes()...)
//
// 不这么做的话，一段带重试的日志会被 [sessionlog.CheckVocabulary] 判成
// 「有不认识的事件类型」而整个拒掉。做法与 [github.com/snight1983/ds-harness-go/feature/compaction.EventTypes]
// 逐字相同。
func EventTypes() []sessionlog.EventType {
	return []sessionlog.EventType{EventRetry, EventRetryStarted}
}

// RetryID 是一条重试链的身份：同一个提供方策略在同一个步骤上连着排的那几次重试
// 共用一个。
//
// 源: packages/llm/llm-retry/src/brand.ts:4-13
//
// 新增: DSH 那边是一个带品牌的字符串，转换函数不做任何校验。Go 里就是一个具名
// 字符串类型，同样不校验——它的取值由发号器决定（见 [Options.NewID]），本包只
// 负责让它在一条链上从头到尾是同一个。
type RetryID string

// RetryData 是 [EventRetry] 的负载：一次排好期、还没开始等的重试。
//
// 源: packages/llm/llm-retry/src/types.ts:15-40
//
// 新增: DSH 那边是一个按 mode 判别的联合，normal 那一支多一个 maxRetries、
// always 那一支干脆没有这个字段。Go 里合成一个结构体，[RetryData.Mode] 就是那个
// 判别标签——做法和 [llm.ResolvedRetryPolicy] 上那条逐字相同。「always 档不能带
// maxRetries」这件事在介质上必须仍然看得出来，所以额外拿一位 [RetryData.HasMaxRetries]
// 说，见 [RetryData.MarshalJSON]。
type RetryData struct {
	// RetryID 是这条重试链的身份。
	RetryID RetryID
	// Turn 是装着这次失败请求的回合。
	Turn int
	// Step 是装着这次失败尝试的步骤。
	Step int
	// Provider 是这次失败请求选中的那个提供方。
	Provider string
	// Mode 是这条路由的重试档位。
	Mode llm.RetryMode
	// PolicyKey 是这份解算完的策略的指纹，见 [retryPolicyKey]。
	//
	// 它落库是为了让「策略在两次失败之间被换掉了」这件事在日志里看得出来：
	// 换了策略就换一条链，重试次数从头数起，而不是接着上一份策略的账继续加。
	PolicyKey string
	// Retry 是这条链上的第几次重试，从 1 起。
	Retry int
	// MaxRetries 是这份策略允许的重试次数；HasMaxRetries 为假时无意义。
	MaxRetries int
	// HasMaxRetries 为真表示这条事件带着 maxRetries，也就是 normal 档。
	//
	// 新增: 单拿一位而不是拿 MaxRetries == 0 表示「没带」。0 是一个**有意义的
	// 取值**（一次都不重试），和「always 档根本没有这个字段」是两件事，
	// 而后者正是不变量要查的那一条。理由同 [agent.RequestFailure].HasRetryPolicy。
	HasMaxRetries bool
	// Delay 是这次重试之前要等的那段时间。
	Delay time.Duration
	// Failure 是招来这次重试的那次失败，在最后那道适配器边界上规整出来的样子。
	Failure llm.Failure
}

// retryWire 是 [RetryData] 在介质上的样子。
//
// 新增: DelayMs 是 float64 而不是整数毫秒。DSH 的 delayMs 是一个可能带小数的数
// （本地退避把指数值乘上了抖动倍率），而这条事件正是那段等待的耐久凭据——排成
// 整数会把一次 1.5 毫秒的等待记成 1 毫秒，于是重放出来的时间线和真正发生过的
// 对不上。本仓库此前没有把 [time.Duration] 排成毫秒的先例，这里立的就是那条：
// 介质上是毫秒数（跟着 DSH 的字段名走），内存里是 [time.Duration]。
type retryWire struct {
	RetryID    RetryID       `json:"retryId"`
	Turn       int           `json:"turn"`
	Step       int           `json:"step"`
	Provider   string        `json:"provider"`
	Mode       llm.RetryMode `json:"mode"`
	PolicyKey  string        `json:"policyKey"`
	Retry      int           `json:"retry"`
	MaxRetries *int          `json:"maxRetries,omitempty"`
	DelayMs    float64       `json:"delayMs"`
	Failure    llm.Failure   `json:"failure"`
}

// MarshalJSON 把这条负载排出去。
//
// 档位和 maxRetries 对不上时当场报错，而不是排出一条介质上违规的事件：那种事件
// 读回来会被本包的不变量拦下（[Trace.Validate]），而拦下的方式是 panic，
// 现场却没有任何东西指回写它的这一刻。做法和
// [github.com/snight1983/ds-harness-go/feature/compaction.SummaryData.MarshalJSON] 上那条 llmStreamCall 检查一样。
func (d RetryData) MarshalJSON() ([]byte, error) {
	switch d.Mode {
	case llm.RetryNormal:
		if !d.HasMaxRetries {
			return nil, fmt.Errorf("%w：%s 的 %s 档必须写明 maxRetries",
				ErrMalformedEvent, EventRetry, llm.RetryNormal)
		}
	case llm.RetryAlways:
		if d.HasMaxRetries {
			return nil, fmt.Errorf("%w：%s 的 %s 档不能带 maxRetries",
				ErrMalformedEvent, EventRetry, llm.RetryAlways)
		}
	default:
		return nil, fmt.Errorf("%w：%s 的 mode 只能是 %q 或者 %q，收到 %q",
			ErrMalformedEvent, EventRetry, llm.RetryNormal, llm.RetryAlways, d.Mode)
	}

	wire := retryWire{
		RetryID:   d.RetryID,
		Turn:      d.Turn,
		Step:      d.Step,
		Provider:  d.Provider,
		Mode:      d.Mode,
		PolicyKey: d.PolicyKey,
		Retry:     d.Retry,
		DelayMs:   float64(d.Delay) / float64(time.Millisecond),
		Failure:   d.Failure,
	}
	if d.HasMaxRetries {
		maxRetries := d.MaxRetries
		wire.MaxRetries = &maxRetries
	}
	return json.Marshal(wire)
}

// UnmarshalJSON 把一段字节读回这条负载。
//
// 这里**不**查档位和 maxRetries 对不对得上，和 [RetryData.MarshalJSON] 是有意的
// 不对称：本包自己写不出一条违规的事件，但一份别处写来的日志必须读得回来，
// 好让 [Trace.Validate] 在它自己那一层把违规报成「always 档带了 maxRetries」。
// 在这里就拒的话，那条违规会变成一条「负载读不回来」——一句指不到毛病在哪的诊断，
// 而且本包那条不变量里对应的分支从此永远走不到，也就再没验过。
func (d *RetryData) UnmarshalJSON(data []byte) error {
	var wire retryWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("%w：%s：%w", ErrMalformedEvent, EventRetry, err)
	}
	*d = RetryData{
		RetryID:   wire.RetryID,
		Turn:      wire.Turn,
		Step:      wire.Step,
		Provider:  wire.Provider,
		Mode:      wire.Mode,
		PolicyKey: wire.PolicyKey,
		Retry:     wire.Retry,
		Delay:     time.Duration(wire.DelayMs * float64(time.Millisecond)),
		Failure:   wire.Failure,
	}
	if wire.MaxRetries != nil {
		d.MaxRetries, d.HasMaxRetries = *wire.MaxRetries, true
	}
	return nil
}

// RetryStartedData 是 [EventRetryStarted] 的负载：那段等待熬过去了。
//
// 源: packages/llm/llm-retry/src/types.ts:42-48
//
// 它刻意只带得起「是哪一次尝试」这一件事：别的都在它配对的那条 [EventRetry] 上，
// 抄一份过来只会多出一处可以和原件漂移的副本。
type RetryStartedData struct {
	// RetryID 是那条重试链的身份，和配对的 [EventRetry] 上的一样。
	RetryID RetryID `json:"retryId"`
	// Turn 是那次尝试所属的回合。
	Turn int `json:"turn"`
	// Step 是那次尝试所属的步骤。
	Step int `json:"step"`
	// Retry 是链上的第几次重试。
	Retry int `json:"retry"`
}

// DecodeRetry 读回一条 llm/retry 的负载。
func DecodeRetry(event sessionlog.Event) (RetryData, error) {
	return decodePayload[RetryData](event, EventRetry)
}

// DecodeRetryStarted 读回一条 llm/retry-started 的负载。
func DecodeRetryStarted(event sessionlog.Event) (RetryStartedData, error) {
	return decodePayload[RetryStartedData](event, EventRetryStarted)
}

// decodePayload 把一条事件的负载解进 T，先确认它的类型对得上。
//
// 新增: DSH 靠声明合并让 `SessionEvent<'llm/retry'>` 这种写法在编译期就把负载
// 类型收窄了。[sessionlog.EventData] 是个**封闭**接口（带一个不可导出的方法），
// 本包这两种负载进不去那个联合，所以 [sessionlog.DecodeData] 只会把它们交成
// [sessionlog.RawData]。于是这里直接读 [sessionlog.Event.Data]，自己查一遍类型。
// 做法与 [github.com/snight1983/ds-harness-go/feature/compaction] 里那个同名函数逐字相同。
func decodePayload[T any](event sessionlog.Event, kind sessionlog.EventType) (T, error) {
	var decoded T
	if event.Type != kind {
		return decoded, fmt.Errorf("%w：seq %d 是 %s，不是 %s",
			ErrMalformedEvent, event.Seq, event.Type, kind)
	}
	payload := event.Data
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return decoded, wrapDecode(event, kind, err)
	}
	return decoded, nil
}

// wrapDecode 给一次解码失败补上它在日志里的位置。
func wrapDecode(event sessionlog.Event, kind sessionlog.EventType, err error) error {
	if errors.Is(err, ErrMalformedEvent) {
		return fmt.Errorf("seq %d：%w", event.Seq, err)
	}
	return fmt.Errorf("%w：seq %d 的 %s：%w", ErrMalformedEvent, event.Seq, kind, err)
}
