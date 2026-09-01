// 本文件的作用：配置项，以及把配置项化成一份定死了的内部配置的那道校验。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:116-289

package mockserver

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"
)

// 各项没配时用的值。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:192-194,205-236
const (
	defaultHost          = "127.0.0.1"
	defaultSuccessText   = "mock response recovered"
	defaultPartialText   = "discarded partial response"
	defaultReasoningText = "mock reasoning"
	defaultToolName      = "mock_tool"
	defaultToolArguments = `{"value":"mock"}`
	defaultChunkSize     = 8
	defaultChunkDelay    = 25 * time.Millisecond
	defaultDisconnect    = 10 * time.Millisecond
	defaultRetryAfter    = time.Second
)

// Options 是一台服务器实例的全部配置。除 Sequence 之外都可以留零值。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:116-154（MockLlmServerOptions）
//
// 新增: TS 用 `field?:` 把「没配」和「配成空/零」分得清清楚楚，于是它可以既接受
// 「没配 chunkSize」又拒绝「chunkSize 配成 0」。Go 的结构体零值表达不了这个差别，
// 本包的选择是**零值即默认**，而不是给十几个字段套指针。
//
// 这不是把校验丢了，是那些校验在 Go 这边多数已经无从违反：DSH 拒绝空的
// successText 是因为空串和「没配」在 JS 里是两个不同的意图，而在这里空串**就是**
// 没配；DSH 拒绝 0 的 chunkSize 是因为它会让切分循环原地打转，而这里 0 已经被
// 默认值接管，那个循环压根构造不出来。真正还能违反的（负数、越界、坏 JSON、
// 坏权重）一条没少。
//
// 补显式存在标记的判据只有一条：**显式的 0 演出来和默认值不一样**。全部字段里
// 只有三个满足。随机种子：0 是正当的种子，而「重放上次那一跑」是随机模式存在的
// 全部理由，把 0 吃掉会让一部分跑挂的用例永远重放不出来。两项延时：0 的意思是
// 不停顿、立刻断开，和 25ms／10ms 是两种不同的演法，而 `--chunk-delay-ms 0`
// 是人在命令行上真会写的东西（见 [ParseCLIArgs]）——不认它就等于安静地演成别的
// 样子，那正是本服务器要帮别人抓的毛病。
type Options struct {
	// Host 是监听地址，留空是回环地址。
	Host string
	// Port 是监听端口，0 表示让操作系统挑一个。
	Port int
	// APIKey 非空时校验 Authorization 必须是精确的 Bearer <token>；留空则任何令牌都收。
	APIKey string
	// Sequence 是按到达顺序消费的剧本，不能为空。
	Sequence []Behavior
	// RepeatLast 打开之后，剧本消费完了就一直重复最后一条。
	RepeatLast bool
	// RandomSeed 是随机行为选择的种子，要配合 HasRandomSeed 才生效。
	RandomSeed uint32
	// HasRandomSeed 为假时本包生成一个种子，并从 [Server.RandomSeed] 交出来。
	HasRandomSeed bool
	// RandomWeights 是 random 展开时各具体行为的相对权重，留空用 [DefaultRandomWeights]。
	RandomWeights map[Behavior]float64
	// SuccessText 是成功类行为发出的完整文本。
	SuccessText string
	// PartialText 是半截类行为在断掉之前发出的文本。
	PartialText string
	// ReasoningText 是 [BehaviorReasoningSuccess] 发出的思考内容。
	ReasoningText string
	// ChunkSize 是每个文本／思考增量里的码点数。
	ChunkSize int
	// ChunkDelay 是 [BehaviorSlowSuccess] 各增量之间的停顿。
	ChunkDelay time.Duration
	// HasChunkDelay 为真时 ChunkDelay 按字面算，含 0（不停顿）。
	HasChunkDelay bool
	// DisconnectDelay 是发完头／增量之后、强行断开之前的停顿。
	DisconnectDelay time.Duration
	// HasDisconnectDelay 为真时 DisconnectDelay 按字面算，含 0（立刻断开）。
	HasDisconnectDelay bool
	// RetryAfter 是建议的重试间隔；线路上的 Retry-After 按秒向上取整。
	RetryAfter time.Duration
	// RequestID 非空时在 HTTP 失败响应里带上 x-request-id。
	RequestID string
	// ToolName 是 [BehaviorToolCallSuccess] 发出的工具名。
	ToolName string
	// ToolArguments 是那次工具调用的原始 JSON 参数。
	ToolArguments string
	// OnEvent 是可选的遥测观察者；它自己 panic 也不会改变线路上的行为。
	OnEvent func(Event)
}

// weightedBehavior 是一条随机权重，权重为正。
type weightedBehavior struct {
	behavior Behavior
	weight   float64
}

// resolvedOptions 是校验通过之后定死的那一份配置，服务器运行期只读它。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:170-190
type resolvedOptions struct {
	host            string
	port            int
	apiKey          string
	sequence        []Behavior
	lastBehavior    Behavior
	repeatLast      bool
	randomSeed      uint32
	randomWeights   []weightedBehavior
	successText     string
	partialText     string
	reasoningText   string
	chunkSize       int
	chunkDelay      time.Duration
	disconnectDelay time.Duration
	retryAfter      time.Duration
	requestID       string
	toolName        string
	toolArguments   string
	onEvent         func(Event)
}

// boundedDuration 把一项延时限制在 [0, MaxTimerDelay] 里。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:197-202
//
// 新增: TS 那个 boundedInteger 还要先判 Number.isInteger——JS 的数字类型装得下
// 3.5 毫秒这种东西。Go 的 [time.Duration] 是整数纳秒，那一半判断在这里无从违反。
func boundedDuration(name string, value time.Duration) (time.Duration, error) {
	if value < 0 || value > MaxTimerDelay {
		return 0, fmt.Errorf("mockserver: %s 必须落在 0 到 %s 之间，收到 %s", name, MaxTimerDelay, value)
	}
	return value, nil
}

// resolveDuration 在没配时取默认值，否则走边界检查。
//
// configured 是那个显式存在标记：为真时 0 按字面的「不停顿」算，而不是当成没配。
// 没有存在标记的项传 false，零值就是没配。
func resolveDuration(name string, value time.Duration, configured bool, fallback time.Duration) (time.Duration, error) {
	if !configured && value == 0 {
		return fallback, nil
	}
	return boundedDuration(name, value)
}

// resolveOptions 校验一份配置并定死它。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:204-289
func resolveOptions(options Options) (resolvedOptions, error) {
	if len(options.Sequence) == 0 {
		return resolvedOptions{}, fmt.Errorf("mockserver: Sequence 不能为空")
	}
	// 新增: DSH 那边剧本条目的合法性由 TypeScript 的字面量联合在编译期担保，
	// resolveOptions 一个字都没查。Go 的 [Behavior] 只是个 string，认不出来的名字
	// 会从演出那个 switch 底下漏过去——处理器什么都不写就返回，客户端收到一个
	// 空的 200，而剧本已经被消费掉了。这种「安静地演成别的样子」正是本服务器要
	// 帮别人抓的东西，自己身上一条都不能有。
	for index, behavior := range options.Sequence {
		if !IsBehavior(behavior) {
			return resolvedOptions{}, fmt.Errorf("mockserver: Sequence 第 %d 条是不认识的行为 %q", index, behavior)
		}
	}
	if options.Port < 0 || options.Port > 65535 {
		return resolvedOptions{}, fmt.Errorf("mockserver: Port 必须落在 0 到 65535 之间，收到 %d", options.Port)
	}
	if options.ChunkSize < 0 {
		return resolvedOptions{}, fmt.Errorf("mockserver: ChunkSize 不能是负数，收到 %d", options.ChunkSize)
	}
	chunkDelay, err := resolveDuration("ChunkDelay", options.ChunkDelay, options.HasChunkDelay, defaultChunkDelay)
	if err != nil {
		return resolvedOptions{}, err
	}
	disconnectDelay, err := resolveDuration("DisconnectDelay", options.DisconnectDelay, options.HasDisconnectDelay, defaultDisconnect)
	if err != nil {
		return resolvedOptions{}, err
	}
	// RetryAfter 没有存在标记：线路上的 Retry-After 按秒向上取整，0 秒的建议等于
	// 没有建议，DSH 那边同样把下限定在 1 毫秒。
	retryAfter, err := resolveDuration("RetryAfter", options.RetryAfter, false, defaultRetryAfter)
	if err != nil {
		return resolvedOptions{}, err
	}

	toolArguments := orDefault(options.ToolArguments, defaultToolArguments)
	if !json.Valid([]byte(toolArguments)) {
		return resolvedOptions{}, fmt.Errorf("mockserver: ToolArguments 必须是合法 JSON，收到 %q", toolArguments)
	}

	randomWeights, err := resolveRandomWeights(options.RandomWeights)
	if err != nil {
		return resolvedOptions{}, err
	}

	randomSeed := options.RandomSeed
	if !options.HasRandomSeed {
		randomSeed = generateSeed()
	}

	sequence := make([]Behavior, len(options.Sequence))
	copy(sequence, options.Sequence)

	return resolvedOptions{
		host:            orDefault(options.Host, defaultHost),
		port:            options.Port,
		apiKey:          options.APIKey,
		sequence:        sequence,
		lastBehavior:    sequence[len(sequence)-1],
		repeatLast:      options.RepeatLast,
		randomSeed:      randomSeed,
		randomWeights:   randomWeights,
		successText:     orDefault(options.SuccessText, defaultSuccessText),
		partialText:     orDefault(options.PartialText, defaultPartialText),
		reasoningText:   orDefault(options.ReasoningText, defaultReasoningText),
		chunkSize:       orDefaultInt(options.ChunkSize, defaultChunkSize),
		chunkDelay:      chunkDelay,
		disconnectDelay: disconnectDelay,
		retryAfter:      retryAfter,
		requestID:       options.RequestID,
		toolName:        orDefault(options.ToolName, defaultToolName),
		toolArguments:   toolArguments,
		onEvent:         options.OnEvent,
	}, nil
}

// resolveRandomWeights 校验权重表并把它摊平成一个有序切片。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:253-266
//
// 新增: 摊平之后要**排序**。TS 那边 Object.entries 的顺序由插入序定死，同一份
// 配置每次跑出来的名册顺序都一样，于是同一个种子选出同一串行为。Go 的 map 遍历
// 顺序是随机的，照抄就会让「同种子可重放」这条当场失效——而那正是随机模式存在的
// 理由。按行为名排序是能让两次进程之间也稳住的最简单办法。
func resolveRandomWeights(configured map[Behavior]float64) ([]weightedBehavior, error) {
	if len(configured) == 0 {
		configured = DefaultRandomWeights()
	}
	positive := make([]weightedBehavior, 0, len(configured))
	for behavior, weight := range configured {
		if !IsConcreteBehavior(behavior) {
			return nil, fmt.Errorf("mockserver: RandomWeights 里的 %q 不是一种具体行为", behavior)
		}
		if math.IsNaN(weight) || math.IsInf(weight, 0) || weight < 0 {
			return nil, fmt.Errorf("mockserver: %q 的随机权重必须是非负的有限数，收到 %v", behavior, weight)
		}
		if weight > 0 {
			positive = append(positive, weightedBehavior{behavior: behavior, weight: weight})
		}
	}
	if len(positive) == 0 {
		return nil, fmt.Errorf("mockserver: RandomWeights 至少要有一项正权重")
	}
	sort.Slice(positive, func(left, right int) bool {
		return positive[left].behavior < positive[right].behavior
	})
	return positive, nil
}

// orDefault 在字符串为空时给出默认值。
func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// orDefaultInt 在整数为零时给出默认值。
func orDefaultInt(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}
