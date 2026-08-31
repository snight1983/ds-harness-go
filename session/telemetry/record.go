// 本文件的作用：交出去的那份逻辑记录本身——两条通道、三档级别、那套共享政策
// 词汇、接收器契约，以及记录在交出去之前要过的那道校验。
//
// 源: packages/session/session-telemetry/src/index.ts:47-176

package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
)

// Channel 是一条记录走的通道。
//
// 源: packages/session/session-telemetry/src/index.ts:65-66
type Channel string

const (
	// ChannelLedger 是和会话日志一一对应的那条通道，记录带 event.seq。
	ChannelLedger Channel = "ledger"
	// ChannelOps 是运维信号那条通道：日志里没有家的 agent-error 与 shutdown。
	//
	// 它**故意不带** event.seq 那类身份，免得被接收方当成 ledger 的行。
	ChannelOps Channel = "ops"
)

// Valid 判断一个通道值落不落在这个封闭词汇里。
func (c Channel) Valid() bool { return c == ChannelLedger || c == ChannelOps }

// Severity 是采集时就定好的告警级别，好让接收方零配置就能报警。
//
// 源: packages/session/session-telemetry/src/index.ts:47-54
type Severity string

const (
	// SeverityInfo 是缺省档：事件自己没说出了问题。
	SeverityInfo Severity = "info"
	// SeverityWarn 采集侧自己不产出，留给 [Rule] 和接收器用。
	SeverityWarn Severity = "warn"
	// SeverityError 是事件自己的结果位说了出问题：工具结果块的 IsError、
	// [session.ReasonError] 那种回合结束、以及 agent-error 那条运维记录。
	SeverityError Severity = "error"
)

// Valid 判断一个级别值落不落在这个封闭词汇里。
func (s Severity) Valid() bool {
	return s == SeverityInfo || s == SeverityWarn || s == SeverityError
}

// SharingStatus 是部署方选定的会话共享政策，由挂上来的接收器向面向真人的
// 告知界面披露。
//
// 源: packages/session/session-telemetry/src/index.ts:130-137
//
// 这套词汇归本包所有，正是为了让任何一个接收器都能披露一份政策，而不必
// 反过来依赖某个具体的上报实现。**没有任何接收器挂着**和「挂着但设成
// [SharingDisabled]」是两件事：前者由装配方自己答，后者是一次明确的部署选择。
type SharingStatus string

const (
	// SharingFull 是整份会话都会离开进程。
	SharingFull SharingStatus = "full"
	// SharingFeedbackOnly 是只有真人明确提交的反馈会离开进程。
	SharingFeedbackOnly SharingStatus = "feedback-only"
	// SharingDisabled 是什么都不离开进程。
	SharingDisabled SharingStatus = "disabled"
)

// Valid 判断一个共享政策值落不落在这个封闭词汇里。
func (s SharingStatus) Valid() bool {
	return s == SharingFull || s == SharingFeedbackOnly || s == SharingDisabled
}

// Record 是交给接收器的一条逻辑记录，也就是采集侧对外的全部词汇。
//
// 源: packages/session/session-telemetry/src/index.ts:56-88
type Record struct {
	// Channel 是这条记录走哪条通道。
	Channel Channel `json:"channel"`

	// Time 是 Unix 纪元毫秒：ledger 记录用源事件的追加时刻，
	// ops 记录用发出这条记录的时刻。
	Time int64 `json:"time"`

	// Severity 是采集时就定好的告警级别。
	Severity Severity `json:"severity"`

	// Attributes 是身份属性，**刻意只留最少的几个**。
	//
	// ledger 记录带 session.id / event.type / event.seq，外加会话头上真的有
	// 那几样时的 session.cwd / session.parent_id / session.seed_length。
	// ops 记录带 telemetry.op 与 session.id，agent-error 那条还带
	// agent.id / turn / step / error.name。**凡是从 body 里就能拿回来的东西
	// 一律不在这里重复一遍。**
	//
	// 新增: DSH 那边的类型是 Record<string, string | number>。Go 的
	// map[string]any 表达不了那个约束，所以它改由 [Record.Validate] 在
	// 交出去之前验一次，见本包文档第 7 条。
	Attributes map[string]any `json:"attributes"`

	// Body 是完整负载：ledger 记录是那条会话事件 Data 的一份深拷贝，
	// ops 记录是那条运维负载。交出去之后**不再改动**。
	//
	// 新增: DSH 是 unknown 加一次 structuredClone。Go 这边负载本来就是字节，
	// 复制一份就是深拷贝，两条通道也因此共用同一个类型。
	Body json.RawMessage `json:"body"`
}

// Validate 检查一条记录能不能交给接收器。
//
// 新增: DSH 没有这一步——它靠 TypeScript 的类型保证同样的事。Go 的
// map[string]any 保证不了，而一条属性值类型不对的记录会在接收器把它排成
// OTLP 的时候炸，那时候已经离开采集侧了，没人查得出来是谁写坏的。
// 所以在交出去之前验一次，验不过就扣下。见本包文档第 7 条。
func (r Record) Validate() error {
	if !r.Channel.Valid() {
		return fmt.Errorf("session/telemetry: 通道只能是 %q 或 %q，给的是 %q",
			ChannelLedger, ChannelOps, r.Channel)
	}
	if !r.Severity.Valid() {
		return fmt.Errorf("session/telemetry: 级别只能是 %q、%q 或 %q，给的是 %q",
			SeverityInfo, SeverityWarn, SeverityError, r.Severity)
	}
	for name, value := range r.Attributes {
		if !validAttribute(value) {
			return fmt.Errorf("session/telemetry: 属性 %q 的值只能是字符串或数字，给的是 %T",
				name, value)
		}
	}
	if !json.Valid(r.Body) {
		return fmt.Errorf("session/telemetry: body 不是一段合法的 JSON")
	}
	return nil
}

// validAttribute 判断一个属性值的类型能不能上介质。
//
// 收下的这几种正好是 DSH 那个 `string | number` 在 Go 里的全部落点：
// 字符串，加上 [encoding/json] 会排成 JSON 数字的那些整数与浮点类型。
// 不收 bool——DSH 的类型里没有它，收下等于单方面把词汇扩宽了。
func validAttribute(value any) bool {
	switch value.(type) {
	case string,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return true
	default:
		return false
	}
}

// Clone 复制一份记录，属性表和 body 都是新的。
//
// 给 [Rule] 用：一条规则要改属性时复制一份再改，就不必去想「原来那份
// 还有没有别人拿着」。
func (r Record) Clone() Record {
	cloned := r
	if r.Attributes != nil {
		cloned.Attributes = maps.Clone(r.Attributes)
	}
	if r.Body != nil {
		cloned.Body = append(json.RawMessage(nil), r.Body...)
	}
	return cloned
}

// Sink 是这个协调器对接收器的最低要求。
//
// 源: packages/session/session-telemetry/src/index.ts:90-128
type Sink interface {
	// Emit 把一条记录交给接收器的管线。**必须是一次不阻塞的入队**——
	// 协调器是在 agent 循环的事件路径上同步调它的，任何比「往队列里推一下」
	// 更慢的动作都会直接变成回合延迟。
	//
	// 这里 panic 会被协调器兜住并记一条日志，不会传到循环那边去。
	//
	// **不许在这里回调协调器的任何方法**：协调器采集一条记录的整个过程
	// 持着自己那把锁，回调会当场死锁。
	Emit(record Record)

	// Shutdown 把卸载转给接收器：把排着的东西刷出去、静默下来。
	//
	// 在这次调用之前 [Sink.Emit] 过的每一条都还必须被送到。
	// 由 [Coordinator.Close] 等待；它返回的错误只会被记成一条警告，
	// 绝不会让装配方的卸载失败——尽力而为的上报没有资格拆掉应用的关闭流程。
	Shutdown(ctx context.Context) error
}

// Flusher 是一个还认「回合结束了」这个提示的接收器。
//
// 源: packages/session/session-telemetry/src/index.ts:104-116
//
// 新增: DSH 那边 flush 是 [Sink] 上的一个可选成员（`flush?(): void`）。
// Go 的接口没有可选成员，所以按本仓库既有的办法拆成两个接口：
// 基础的那个人人都得满足，宽的那个提供得了才满足。同样的做法见
// storage.KVProvider。这里**不配一个取facet的函数**（storage.KV 那种），
// 因为要取的不是一个可能为 nil 的值，就是一个方法，一次类型断言就够了。
//
// 大多数接收器该**不实现**它，让 SDK 自己的攒批节奏决定什么时候导出：
// 实现了它，就得自己去管这些并发的 flush 和 [Sink.Shutdown] 那次排空之间
// 谁先谁后。
type Flusher interface {
	Sink

	// Flush 是一个即发即忘的提示，说一个回合刚结束。不许阻塞。
	Flush()
}

// Rule 是一条脱敏规则：记录交给接收器之前，最后能改它的地方。
//
// 源: packages/session/session-telemetry/src/index.ts:22-42
//
// 本包**一条规则都不带**：一个装配方不挂规则，记录就原样到达接收器——
// 导出去的数据有多干净，完全等于部署方挂了哪些规则。
//
// 语义逐条对齐 cordis 的 waterfall：每条规则拿到的 record 都是那份原始记录，
// next 跑的是**排在它下面**的全部规则并给出它们的结果；规则可以再加工那个
// 结果，也可以**根本不调 next**，那就把底下全部替换掉。
//
// 返回错误表示这条记录被**扣下**（fail-closed），不交给接收器，只记一条日志。
// panic 同样被兜住，效果一样。
//
// 脱敏只作用在导出的那一份上，会话日志本身**永远不会**被改写。
type Rule func(record Record, next func() (Record, error)) (Record, error)

// runRules 从最外面那条规则开始跑完整条链。
//
// 一条规则都没有时原样返回，也就是 DSH 那个最内层 `() => record`。
func runRules(rules []Rule, record Record) (Record, error) {
	var run func(index int) (Record, error)
	run = func(index int) (Record, error) {
		if index >= len(rules) {
			return record, nil
		}
		return rules[index](record, func() (Record, error) { return run(index + 1) })
	}
	return run(0)
}
