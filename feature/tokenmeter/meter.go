// 本文件的作用：计量器这个服务本身——它按会话缓着一份重放状态，把日志往前折，
// 并在每一条落定的助手消息上立一个**锚**：提供方亲口报的那个数，加上从那一刻起
// 表面的带符号位移。
//
// 三个投影单元（usageprojection.go / breakdownprojection.go）和这里各走各的：
// 它们的状态要落盘所以必须 O(1)，这里的状态活在内存里所以留得起整张节点表——
// 而压缩那边挑下刀点正需要那张表。
//
// 源: packages/llm/token-meter/src/index.ts

package tokenmeter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/sessionlog/projection"
)

// measurementAnchor 是一次「提供方亲口报过」的锚点，记的是**原始事实**。
//
// 源: packages/llm/token-meter/src/index.ts:35-48（MeasurementAnchor）
//
// 它由一条落定的助手消息立起来，记住立锚那一刻的请求头、那一刻的表面快照、
// 提供方那一侧输出的固定估价，以及它报回来的那份用量（报了才有）。
//
// 这里刻意**不存基准**：基准由 [TokenMeter.Measure] 每次现算。表面的价是
// 跟着路由变的（见 routepricing.go），锚那张快照必须和当次拿来比的当前表面
// 在**同一条路由**下重新定价，那个带符号的差才是拿同一把尺子量出来的。
// 立锚时把基准算死，等于把立锚那一刻的路由永久钉在这个锚上。
type measurementAnchor struct {
	// header 是立锚那一刻在手的请求头。
	header sessionlog.EpochHeader
	// hasHeader 说明立锚时到底有没有请求头。
	//
	// 新增: DSH 那边是 `EpochHeader | undefined`。这里必须把「有没有」单独拿出来
	// ——[optionalHeaderEquals] 里「都没有」算相等、「一边有一边没有」算不等，
	// 而一份零值的 [sessionlog.EpochHeader] 和「没有头」在这个判断上是两件事。
	hasHeader bool
	// nodes 是这次请求所依据的那张表面快照。
	nodes []meterNode
	// assistantTokens 是提供方那一侧输出在固定尺子下的价。
	//
	// 它不跟着路由重新定价：那是模型吐出来的文本，里面没有请求图片。
	assistantTokens int
	// usage 是这次调用报回来的用量；nil 表示没报、或者报的时候还没见过请求头。
	usage *llm.TokenUsage
}

// stepMark 是一个开着的步骤，外加它开起来那一刻的表面快照。
//
// 源: packages/llm/token-meter/src/index.ts:54
//
// 那张快照是锚的**起算点**：一次请求看见的表面是「这个步骤开始之前的全部」
// 加上「这一步自己产出的那条助手消息」，中间那些工具结果是这一步之后才进去的。
type stepMark struct {
	turn  int
	step  int
	nodes []meterNode
}

// replayState 是一个会话在计量器这边的重放状态。
//
// 源: packages/llm/token-meter/src/index.ts:34-41
type replayState struct {
	// consumedEvents 是已经折进来的事件条数，同时充当 [Measurement.LogRevision]。
	consumedEvents int
	// baseSeq 是折这一段时那份日志的起点。
	//
	// 新增: 上游没有这一条——它的日志从 0 起、一条不删，起点是常数。本仓库的日志
	// 会从最老的一头被弹掉一截（见 docs/session-log-limit.md），起点一变，
	// consumedEvents 这个条数就不再指向同一条事件了。记下来是为了发现这件事，
	// 见 [TokenMeter.sync]。
	baseSeq int
	// header 是最新那份请求头（已规范化）。
	header sessionlog.EpochHeader
	// hasHeader 说明有没有见过请求头。
	hasHeader bool
	// surface 是整张表面节点表，逐节点带固定估价和那次出现的图片引用。
	surface []meterNode
	// stepStart 是当前开着的那个步骤；nil 表示没有步骤开着。
	stepStart *stepMark
	// anchor 是最近立起来的那个锚；nil 表示还没有过。
	anchor *measurementAnchor
}

// TokenMeter 是 token 计量服务。
//
// 源: packages/llm/token-meter/src/index.ts:88-332（TokenMeter）
//
// 它按会话缓一份重放状态，[TokenMeter.Measure] 每次调用先把新事件折进来再答话，
// 所以同一份日志重复问不会重复折。
//
// 零值不可用，用 [New] 建。它可以被多个 goroutine 同时使用。
type TokenMeter struct {
	mu     sync.Mutex
	states map[sessionlog.SessionID]*replayState
	// llmRuntime 是解那条路由的图片计价用的；nil 表示一律用固定估价。
	llmRuntime *llm.Runtime
}

// New 建一个计量器。
//
// llmRuntime 给了才有路由感知的图片计价：一条路由的适配器报价时，那条路由下的
// 每一张图按它报的视觉 token 加那段模型可见文本计价。为 nil 时（以及路由不报价时）
// 每个节点都留着固定估价——这正是 DSH `ctx.get('llm')?.` 那个可选取用的意思。
//
// 新增: DSH 那边构造函数里还做两件事，Go 这边都挪走了：
//
//   - 它在 ctx 上登记三个投影单元。Go 没有那个容器，「投影服务在不在场」
//     就是装配方手上有没有那张注册表，所以那件事成了显式的
//     [RegisterProjections]（成例见 todo.RegisterProjection）。
//   - 它订阅 session/event，好在事件到达的当下就把已经在跟的会话往前折。
//     那纯粹是保温：[TokenMeter.Measure] 自己会把落后的部分补上，答案一模一样。
//     Go 这边不订阅，代价只是折叠的时机从「事件到达」推到「有人来问」，
//     顺带把折叠的报错也一起推到那时候——那正是调用方接得住错误的地方。
func New(llmRuntime *llm.Runtime) *TokenMeter {
	return &TokenMeter{
		states:     map[sessionlog.SessionID]*replayState{},
		llmRuntime: llmRuntime,
	}
}

// RegisterProjections 把三个投影单元一起登进注册表，返回把它们一起注销的函数。
//
// 源: packages/llm/token-meter/src/index.ts:87-91
//
// 注销函数是幂等的，中途某一个登记失败时前面登上的会被回滚掉，
// 所以调用方拿到错误就等于「一个都没登上」。
func RegisterProjections(registry *projection.Registry) (func(), error) {
	if registry == nil {
		return nil, errors.New("token 计量器：需要一个投影注册表")
	}

	var undos []func()
	rollback := func() {
		for _, undo := range undos {
			undo()
		}
	}

	usageUndo, err := projection.Register(registry, tokenUsageDefinition())
	if err != nil {
		return nil, err
	}
	undos = append(undos, usageUndo)

	pressureUndo, err := projection.Register(registry, contextPressureDefinition())
	if err != nil {
		rollback()
		return nil, err
	}
	undos = append(undos, pressureUndo)

	breakdownUndo, err := projection.Register(registry, contextBreakdownDefinition())
	if err != nil {
		rollback()
		return nil, err
	}
	undos = append(undos, breakdownUndo)

	var once sync.Once
	return func() { once.Do(rollback) }, nil
}

// Sessions 是本服务要的那道会话退场广播。
//
// 新增: 收接口不收 [github.com/snight1983/ds-harness-go/harness/session.Store]
// 这个具体类型，理由和 compaction/basic 的 Sessions 一样：把用到的那一面写出来，
// 读的人一眼看见本服务到底碰了那张表的什么。
type Sessions interface {
	// OnDisposed 登记一个「一个会话退场了」的观察者。
	OnDisposed(
		ctx context.Context, owner *scope.Scope, observer coresession.DisposedObserver,
	) (func(context.Context) error, error)
}

// Install 把这份缓存接到会话退场那条边上，返回把它摘下来的函数。
//
// 新增: 整条是本仓库自有的。DSH 那份缓存是 `WeakMap<Session, ReplayState>`，会话
// 对象一被回收那格就跟着没了，所以那边没有这个装配点。Go 的映射按
// [sessionlog.SessionID] 归档，键是个值、不会自己消失——没有这条边，一台长期在跑
// 的服务每量过一个会话就多留一张**随会话长度线性增长**的表面节点表，只增不减。
// 成例是 compaction/basic.Install。
//
// 它和 [RegisterProjections] 是两件事：那个登记的是三个 O(1) 的投影单元，
// 这个收的是本服务自己那份 O(表面) 的重放状态。
func Install(ctx context.Context, owner *scope.Scope, meter *TokenMeter, sessions Sessions) (func(context.Context) error, error) {
	switch {
	case meter == nil:
		return nil, errors.New("token 计量器：装它要有一个计量器")
	case sessions == nil:
		return nil, errors.New("token 计量器：装它要有一道会话退场广播")
	}
	return sessions.OnDisposed(ctx, owner, func(live *coresession.Session) {
		meter.Forget(live.ID())
	})
}

// Forget 丢掉一个会话缓着的重放状态。
//
// 新增: DSH 那边这份缓存是 `WeakMap<Session, ReplayState>`——会话对象一被回收，
// 它那格就跟着没了。Go 的映射按 [sessionlog.SessionID] 归档，键是个值，不会自己消失，
// 所以关掉一个会话的一方要来说一声。装配走 [Install]；不说也只是白占一格内存，
// 读不出错。
func (m *TokenMeter) Forget(id sessionlog.SessionID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.states, id)
}

// Measure 量出一个会话当下的 token 账。
//
// 源: packages/llm/token-meter/src/index.ts:116-147
//
// requestHeader 给了就用它当「下一次请求要发的那份头」，nil 表示用日志里最新的那份。
// 那份头里的提供方／模型**同时决定了这次测量按哪条路由给图片定价**：一条报价的
// 路由把每一张留下来的图按它自己报的价算，别的路由留着固定估价。
//
// 三种基准按这个次序挑：
//
//   - 手上那个锚的头和这次要问的头**一致**：拿锚那张表面快照按**同一条路由**
//     重新定一遍价当起算点，位移是当前表面减掉它。这是常态，也是唯一一条让
//     绝对值锚在提供方那边的路。
//   - 没有头、表面也是空的：[BaselineNone]，整张账是 0。
//   - 其余（换过模型、改过系统提示、或者还没有过任何一次响应）：整份重新估价，
//     位移归零。锚对不上就不能再拿它当基准——那等于把 A 请求的绝对值配上
//     B 请求的增量。
func (m *TokenMeter) Measure(view projection.SessionView, requestHeader *sessionlog.EpochHeader) (Measurement, error) {
	state, err := m.sync(view)
	if err != nil {
		return Measurement{}, err
	}

	header, hasHeader := state.header, state.hasHeader
	if requestHeader != nil {
		header, hasHeader = sessionlog.CanonicalHeader(*requestHeader), true
	}

	pricing := m.routeImagePricing(header, hasHeader)
	surface, err := priceSurface(state.surface, pricing)
	if err != nil {
		return Measurement{}, err
	}

	var baseline MeasurementBaseline
	surfaceDeltaTokens := 0
	switch anchor := state.anchor; {
	case anchor != nil && optionalHeaderEquals(anchor.header, anchor.hasHeader, header, hasHeader):
		// 两份头一致就意味着两边是同一条路由，锚那张快照于是能按当前这份计价
		// 重新定价，那个带符号的差才是拿同一把尺子量出来的。
		anchorSurface, priceErr := priceSurface(anchor.nodes, pricing)
		if priceErr != nil {
			return Measurement{}, priceErr
		}
		anchorSurfaceTokens := anchorSurface.surfaceTokens + anchor.assistantTokens
		headerTokens, estimateErr := EstimateHeader(header)
		if estimateErr != nil {
			return Measurement{}, estimateErr
		}
		estimatedAnchorTokens := headerTokens + anchorSurfaceTokens

		// 带符号的启发式增量只有挂在一个**不小于**同口径全量估价的锚上才是保守的：
		// 锚比估价小的时候，后面每一次「减掉一段」都会从一个本来就偏低的绝对值上
		// 再减一刀，越减越离谱。这种时候宁可整份用估价，至少口径是自洽的。
		baseline = MeasurementBaseline{Kind: BaselineEstimated, Tokens: estimatedAnchorTokens}
		if anchor.usage != nil {
			if providerTokens := usageTokens(*anchor.usage); providerTokens >= estimatedAnchorTokens {
				baseline = MeasurementBaseline{
					Kind:   BaselineUsage,
					Tokens: providerTokens,
					Usage:  *anchor.usage,
				}
			}
		}
		surfaceDeltaTokens = surface.surfaceTokens - anchorSurfaceTokens
	case !hasHeader && surface.surfaceTokens == 0:
		baseline = MeasurementBaseline{Kind: BaselineNone}
	default:
		headerTokens, estimateErr := EstimateHeader(header)
		if estimateErr != nil {
			return Measurement{}, estimateErr
		}
		baseline = MeasurementBaseline{
			Kind:   BaselineEstimated,
			Tokens: headerTokens + surface.surfaceTokens,
		}
	}

	// 节点表是 priceSurface 当场新造的，不和重放状态共享，所以这里不必再复制一次
	// ——DSH 那边同样的位置是 deepFreeze(structuredClone(...))，防的是它那份
	// 按引用共享的 state.surface。
	return Measurement{
		LogRevision:        state.consumedEvents,
		Baseline:           baseline,
		SurfaceDeltaTokens: surfaceDeltaTokens,
		TotalTokens:        max(0, baseline.Tokens+surfaceDeltaTokens),
		SurfaceTokens:      surface.surfaceTokens,
		Nodes:              surface.nodes,
	}, nil
}

// routeImagePricing 解出这份请求头那条路由的图片计价，没有就是 nil。
//
// 源: packages/llm/token-meter/src/index.ts:179-184（_routeImagePricing）
//
// 三种情况都落到 nil：没有请求头、没交 llm 运行时、那条路由不报价。三者对
// 定价的后果一样（整张表面留着固定估价），所以不分。
func (m *TokenMeter) routeImagePricing(header sessionlog.EpochHeader, hasHeader bool) llm.ImageRequestPricing {
	if !hasHeader || m.llmRuntime == nil {
		return nil
	}
	pricing, ok := m.llmRuntime.ImageRequestPricing(header.Config.Provider, header.Config.Model)
	if !ok {
		return nil
	}
	return pricing
}

// EstimateMessage 按计量器那把尺子给一条消息估价。
//
// 源: packages/llm/token-meter/src/index.ts:155-157
//
// 它是方法不是直接用包级的 [EstimateMessage]，因为调用方（压缩那边）拿到的是
// 这个服务，让它去够一个包级函数就等于让它对着两个东西编程。
func (m *TokenMeter) EstimateMessage(message llm.Message) (int, error) {
	return EstimateMessage(message)
}

// sync 把一个会话身上还没折的事件折完，交出它的重放状态。
//
// 源: packages/llm/token-meter/src/index.ts:160-181
//
// 折到一半出错时 consumedEvents **停在出错那条上**：那条事件下次还会被重折。
// 这是有意的——它让一条坏事件保持「没被读进来」的状态，而不是被跳过去，
// 后者会让整份账悄悄少一段。
func (m *TokenMeter) sync(view projection.SessionView) (*replayState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := view.ID()
	events := view.Events()

	baseSeq := sessionlog.LogBaseSeq(events)
	state, tracked := m.states[id]
	// 新增: DSH 按会话对象的身份缓存，一个从存储里重新装出来的会话天然是另一个键。
	// Go 按 [sessionlog.SessionID] 缓存，那样两次装配会撞在同一格上。日志变短是这件事
	// 唯一看得见的形态（一份日志只会往后长），撞上就从头重放。
	//
	// 新增: 起点变了也要重放。consumedEvents 是个条数，它指的是「从 baseSeq 数起
	// 的第 n 条」；日志被弹过头之后（见 docs/session-log-limit.md）同一个条数指向的
	// 是另一条事件，而只看长度是发现不了的——弹掉三条又追加五条，长度反而是长的。
	if tracked && (state.consumedEvents > len(events) || state.baseSeq != baseSeq) {
		tracked = false
	}
	if !tracked {
		state = &replayState{baseSeq: baseSeq}
		m.states[id] = state
	}

	for state.consumedEvents < len(events) {
		if err := m.foldEvent(view, state, events[state.consumedEvents]); err != nil {
			return nil, err
		}
		state.consumedEvents++
	}
	return state, nil
}

// foldEvent 把一条事件折进重放状态。
//
// 源: packages/llm/token-meter/src/index.ts:188-270
//
// 会失败的每一步都在**动状态之前**跑完：算好放在一边的局部变量里，全过了才一次性
// 落回 state。半途失败的折叠会让这份跨事件累积的状态从此和日志对不上，而它没有
// 任何办法自己发现这件事。
func (m *TokenMeter) foldEvent(view projection.SessionView, state *replayState, event sessionlog.Event) error {
	nextHeader, nextHasHeader := state.header, state.hasHeader
	nextStepStart := state.stepStart
	nextAnchor := state.anchor

	switch event.Type {
	case sessionlog.EventRequestHeader:
		var data sessionlog.RequestHeaderData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return fmt.Errorf("token 计量器：seq %d 的请求头读不回来：%w", event.Seq, err)
		}
		nextHeader, nextHasHeader = sessionlog.CanonicalHeader(data.Header), true
	case sessionlog.EventStepStart:
		if state.stepStart != nil {
			return fmt.Errorf("token 计量器：seq %d 的 step/start 来得比回合 %d／步骤 %d 结束还早",
				event.Seq, state.stepStart.turn, state.stepStart.step)
		}
		var data sessionlog.StepStartData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return fmt.Errorf("token 计量器：seq %d 的 step/start 读不回来：%w", event.Seq, err)
		}
		// 记下这个步骤开起来那一刻的表面快照：锚就是从这里起算的。快照要复制一份，
		// 后面的折叠会把 state.surface 换掉。
		nextStepStart = &stepMark{
			turn:  data.Turn,
			step:  data.Step,
			nodes: append([]meterNode(nil), state.surface...),
		}
	case sessionlog.EventStepEnd:
		var data sessionlog.StepEndData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return fmt.Errorf("token 计量器：seq %d 的 step/end 读不回来：%w", event.Seq, err)
		}
		if state.stepStart == nil || state.stepStart.turn != data.Turn || state.stepStart.step != data.Step {
			return fmt.Errorf("token 计量器：seq %d 的 step/end 配不上任何一条 step/start", event.Seq)
		}
		nextStepStart = nil
	}

	var surface surfaceTokenFold
	folded := sessionlog.IsSurfaceEvent(event)
	if folded {
		fold, err := foldSurfaceTokens(state.surface, event, state.baseSeq)
		if err != nil {
			return err
		}
		surface = fold
	}

	if event.Type == sessionlog.EventAssistantMessage {
		var data sessionlog.AssistantMessageData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return fmt.Errorf("token 计量器：seq %d 的助手消息读不回来：%w", event.Seq, err)
		}
		stepStart := state.stepStart
		if stepStart == nil || stepStart.turn != data.Turn || stepStart.step != data.Step {
			return fmt.Errorf("token 计量器：seq %d 的 assistant/message 配不上任何一条 step/start", event.Seq)
		}
		if !folded {
			// 走不到：assistant/message 是上表面的类型。真走到了说明
			// [sessionlog.IsSurfaceEvent] 和这里对「哪些事件上表面」的认识分了家，
			// 那时候把它当 0 个 token 会让整个锚偏掉，不如当场断掉。
			return fmt.Errorf("token 计量器：seq %d 的 assistant/message 没能折进表面", event.Seq)
		}

		assistantTokens := surface.tokens
		var usage *llm.TokenUsage
		if data.Usage != nil && nextHasHeader {
			// 提供方看见的是它自己那趟流产出的内容，而落进日志的那条消息可能已经
			// 被改写过（比如打断只留了一个前缀）。锚要跟提供方对齐，所以按来源
			// 那些分块重新装配一遍。
			providerAssistantTokens, err := m.estimateProviderAssistant(view, event, data, surface.tokens)
			if err != nil {
				return err
			}
			assistantTokens = providerAssistantTokens
			// 复制一份：data 是这次折叠的局部变量，但这个指针要活到后面每一次测量。
			reported := *data.Usage
			usage = &reported
		}
		// 没有用量（或者还没见过请求头）：照样立锚，只是后面每次测量都会算出一个
		// 估出来的基准。立了它，位移那一路的逻辑不用分叉。
		nextAnchor = &measurementAnchor{
			header:          nextHeader,
			hasHeader:       nextHasHeader,
			nodes:           stepStart.nodes,
			assistantTokens: assistantTokens,
			usage:           usage,
		}
	}

	state.header, state.hasHeader = nextHeader, nextHasHeader
	state.stepStart = nextStepStart
	if folded {
		state.surface = surface.nodes
	}
	state.anchor = nextAnchor
	return nil
}

// estimateProviderAssistant 按来源分块重新装配一遍，估出提供方那一侧看见的
// 助手内容值多少。
//
// 源: packages/llm/token-meter/src/index.ts:277-310
//
// 没有来源清单就退回落进日志的那条消息的估价——一条自己造出来的助手消息
// （比如修复补上的那条）本来就没有对应的流。
func (m *TokenMeter) estimateProviderAssistant(
	view projection.SessionView,
	event sessionlog.Event,
	data sessionlog.AssistantMessageData,
	durableEventTokens int,
) (int, error) {
	if event.SourceEventSeqs == nil {
		return durableEventTokens, nil
	}

	events := view.Events()
	assembler := llm.NewBlockAssembler()
	seen := make(map[int]struct{}, len(event.SourceEventSeqs))
	for _, seq := range event.SourceEventSeqs {
		if seq >= event.Seq {
			return 0, fmt.Errorf("token 计量器：seq %d 的 assistant/message 引的来源 %d 不比它早",
				event.Seq, seq)
		}
		if _, duplicate := seen[seq]; duplicate {
			return 0, fmt.Errorf("token 计量器：seq %d 的 assistant/message 把来源 %d 引了两遍",
				event.Seq, seq)
		}
		seen[seq] = struct{}{}

		// DSH 那边直接 `session.events[seq]!`——它的日志从 0 起、一条不删，seq 就是
		// 下标。本仓库的日志会从最老的一头被弹掉一截（见 docs/session-log-limit.md），
		// 那个式子会取到隔壁那条上去，于是这一步的账被悄悄算成别人的。换算走
		// [sessionlog.SeqIndex]，它减完还核一遍 seq 对不对得上。
		index, ok := sessionlog.SeqIndex(events, seq)
		if !ok {
			return 0, fmt.Errorf("token 计量器：seq %d 的 assistant/message 引的来源 %d 不在日志里",
				event.Seq, seq)
		}
		source := events[index]
		if source.Type != sessionlog.EventAssistantChunk {
			return 0, fmt.Errorf("token 计量器：seq %d 的 assistant/message 引的来源 %d 不是 assistant/chunk",
				event.Seq, seq)
		}
		var sourceData sessionlog.AssistantChunkData
		if err := json.Unmarshal(source.Data, &sourceData); err != nil {
			return 0, fmt.Errorf("token 计量器：seq %d 引的来源 %d 读不回来：%w", event.Seq, seq, err)
		}
		if sourceData.Turn != data.Turn || sourceData.Step != data.Step {
			return 0, fmt.Errorf("token 计量器：seq %d 的 assistant/message 引的来源 %d 属于另一个步骤",
				event.Seq, seq)
		}
		assembler.Push(sourceData.Chunk)
	}

	blocks, err := assembler.Blocks()
	if err != nil {
		return 0, err
	}
	// 一份空内容不占一条消息的位置，所以连角色开销都不加——这和
	// [EstimateMessage] 对一条空消息的算法有意分开：那边量的是日志上真实存在的
	// 一格，这边量的是提供方那趟流里到底有没有东西。
	if len(blocks) == 0 {
		return 0, nil
	}
	contentTokens, err := EstimateContent(llm.Content(blocks))
	if err != nil {
		return 0, err
	}
	return contentTokens + RoleOverhead, nil
}

// usageTokens 把一份提供方用量摊平成「这一次请求前后一共经手多少 token」。
//
// 源: packages/llm/token-meter/src/index.ts:44-49
//
// 输入、缓存读、缓存写、输出全都算进来——它当的是锚，量的是那次请求的完整规模，
// 和 [pressureFrom] 那个只算提示词侧的数是两个用途。推理 token 不另加，
// 它已经含在输出里了。
func usageTokens(usage llm.TokenUsage) int {
	return usage.InputTokens + usage.CacheReadTokens + usage.CacheWriteTokens + usage.OutputTokens
}

// optionalHeaderEquals 比较两份「可能不存在」的请求头：都不在算相等，
// 一边在一边不在算不等，都在就比内容。
//
// 源: packages/llm/token-meter/src/index.ts:52-58
func optionalHeaderEquals(left sessionlog.EpochHeader, leftOK bool, right sessionlog.EpochHeader, rightOK bool) bool {
	if !leftOK || !rightOK {
		return leftOK == rightOK
	}
	return sessionlog.HeaderEquals(left, right)
}
