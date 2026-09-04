// 本文件的作用：一次计量交出来的那份结果长什么样，以及它凭什么这么算。
//
// 源: packages/llm/token-meter/src/types.ts

package tokenmeter

import "github.com/snight1983/ds-harness-go/llm"

// Config 是这个计量器的配置。
//
// 源: packages/llm/token-meter/src/types.ts:11-12（TokenMeterConfig）
//
// 它是空的：这套启发式的三个常数是写死的，没有任何一处能配。DSH 那边写成
// `Record<string, never>`（一个不许有任何键的对象），意思一样——**存在一份配置**
// 这件事本身是接口的一部分，装配那一层要能把它排出去、也能把一份写错的配置拒掉。
type Config struct{}

// BaselineKind 说的是一份计量的基准是怎么来的。
//
// 源: packages/llm/token-meter/src/types.ts:19-22
type BaselineKind string

const (
	// BaselineNone 表示没有基准可言：这个会话上还什么都没发生过。
	BaselineNone BaselineKind = "none"
	// BaselineEstimated 表示基准是这套启发式自己算出来的。
	BaselineEstimated BaselineKind = "estimated"
	// BaselineUsage 表示基准锚在提供方报回来的一次真实用量上。
	BaselineUsage BaselineKind = "usage"
)

// MeasurementBaseline 是一次计量的基准：那个「从这里开始往后算增量」的锚点。
//
// 源: packages/llm/token-meter/src/types.ts:14-18（TokenMeasurementBaseline）
//
// 新增: DSH 那边是一个三支的可辨识联合。这里没有按本仓库处理 TS 联合的一贯做法
// （封闭接口加变体，成例是 [llm.ContentBlock]）来写，而是收成一个带判别字段的
// 结构体，理由有三条：这个值**不过任何序列化边界**（它是 [TokenMeter.Measure]
// 在进程内交给压缩那边的），三支都带着同一个 Tokens，唯一多出来的字段（Usage）
// 恰好在 Kind 是 [BaselineUsage] 时才有意义。为这么一个值配一套类型开关，
// 每一处读 Tokens 的地方都要先解一次包，而它们全都只想读那一个数。
type MeasurementBaseline struct {
	// Kind 是这份基准的来路。
	Kind BaselineKind
	// Tokens 是基准处的总量。[BaselineNone] 时恒为 0。
	Tokens int
	// Usage 是锚住这份基准的那次提供方用量，只在 Kind 是 [BaselineUsage] 时有意义。
	Usage llm.TokenUsage
}

// SurfaceNode 是表面上一个节点和它在这把固定尺子下的估价。
//
// 源: packages/llm/token-meter/src/types.ts:37
type SurfaceNode struct {
	// Seq 是这个节点对应事件的 seq。
	Seq int
	// Tokens 是它的估价。
	Tokens int
}

// Measurement 是一次计量的全部结果。
//
// 源: packages/llm/token-meter/src/types.ts:20-34（TokenMeasurement）
//
// 读它的时候要记住这个包的根本立场：TotalTokens **不是**一次测量，而是
// 「一次真实用量」加上「那之后这套启发式量出来的净变化」。基准越新，
// 这个数越可信；一份 [BaselineEstimated] 的基准意味着整份都是估的。
type Measurement struct {
	// LogRevision 是这份结果消化到日志的哪一步了，等于下一个还没读的 seq。
	//
	// 源: packages/llm/token-meter/src/types.ts:31
	//
	// 拿它去判「我手上这份结果还新不新」：会话又追了事件之后它会变大。
	LogRevision int
	// Baseline 是这份结果的锚点。
	Baseline MeasurementBaseline
	// SurfaceDeltaTokens 是锚点之后表面上的净重新定价，**带符号**。
	//
	// 一次压缩会把一大段表面换成一小段摘要，这个数于是是负的。
	SurfaceDeltaTokens int
	// TotalTokens 是最终那个数，钳在 0：max(0, Baseline.Tokens + SurfaceDeltaTokens)。
	//
	// 钳的理由：负数会一路串进预算和压缩触发的算式里，那比丢掉一次记账严重得多。
	TotalTokens int
	// SurfaceTokens 是当前整个表面在这把尺子下的估价，和基准无关。
	SurfaceTokens int
	// Nodes 是表面上每个节点的估价，按表面顺序。
	//
	// 压缩那边靠它挑下刀点，所以它必须和当前表面**一一对上**，
	// 见 compaction/basic.SelectCompactableRange。
	Nodes []SurfaceNode
}

// Clone 交出一份不共享切片的拷贝。
//
// 新增: DSH 那边 measure() 返回的是 deepFreeze(structuredClone(...))——JS 里
// 对象按引用共享，交出去的结果会被收到它的人改掉，冻上是唯一的防线。
// Go 这边结构体赋值就是复制，只有 Nodes 那个切片需要单独复制一份。
func (m Measurement) Clone() Measurement {
	m.Nodes = append([]SurfaceNode(nil), m.Nodes...)
	return m
}
