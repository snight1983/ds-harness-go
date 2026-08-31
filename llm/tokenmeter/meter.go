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
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"ds-harness-go/llm"
	"ds-harness-go/session"
	"ds-harness-go/session/projection"
)

// measurementAnchor 是一次「提供方亲口报过」的锚点。
//
// 源: packages/llm/token-meter/src/index.ts:28-32
//
// 它由一条带用量的助手消息立起来，记住立锚那一刻的请求头和表面总价。
// 后来的每一次测量，只要请求头和它一致，就用这个锚当基准，再加上表面从那时起
// 的带符号位移——于是启发式只用来量**变化的那一小段**，绝对值始终锚在提供方那边。
type measurementAnchor struct {
	// header 是立锚那一刻在手的请求头。
	header session.EpochHeader
	// hasHeader 说明立锚时到底有没有请求头。
	//
	// 新增: DSH 那边是 `EpochHeader | undefined`。这里必须把「有没有」单独拿出来
	// ——[optionalHeaderEquals] 里「都没有」算相等、「一边有一边没有」算不等，
	// 而一份零值的 [session.EpochHeader] 和「没有头」在这个判断上是两件事。
	hasHeader bool
	// surfaceTokens 是立锚那一刻的表面总价。
	surfaceTokens int
	// baseline 是这个锚交出去的基准，它的 Kind **一定不是** [BaselineNone]。
	baseline MeasurementBaseline
}

// stepMark 是一个开着的步骤，外加它开起来那一刻的表面总价。
//
// 源: packages/llm/token-meter/src/index.ts:38
//
// 那个总价是锚的**起算点**：一次请求看见的表面是「这个步骤开始之前的全部」
// 加上「这一步自己产出的那条助手消息」，中间那些工具结果是这一步之后才进去的。
type stepMark struct {
	turn          int
	step          int
	surfaceTokens int
}

// replayState 是一个会话在计量器这边的重放状态。
//
// 源: packages/llm/token-meter/src/index.ts:34-41
type replayState struct {
	// consumedEvents 是已经折进来的事件条数，同时充当 [Measurement.LogRevision]。
	consumedEvents int
	// header 是最新那份请求头（已规范化）。
	header session.EpochHeader
	// hasHeader 说明有没有见过请求头。
	hasHeader bool
	// surface 是整张表面节点表，逐节点带价。
	surface []SurfaceNode
	// surfaceTokens 是整张表的合计。
	surfaceTokens int
	// stepStart 是当前开着的那个步骤；nil 表示没有步骤开着。
	stepStart *stepMark
	// anchor 是最近立起来的那个锚；nil 表示还没有过。
	anchor *measurementAnchor
}

// TokenMeter 是 token 计量服务。
//
// 源: packages/llm/token-meter/src/index.ts:74-311
//
// 它按会话缓一份重放状态，[TokenMeter.Measure] 每次调用先把新事件折进来再答话，
// 所以同一份日志重复问不会重复折。
//
// 零值不可用，用 [New] 建。它可以被多个 goroutine 同时使用。
type TokenMeter struct {
	mu     sync.Mutex
	states map[session.SessionID]*replayState
}

// New 建一个计量器。
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
func New() *TokenMeter {
	return &TokenMeter{states: map[session.SessionID]*replayState{}}
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

// Forget 丢掉一个会话缓着的重放状态。
//
// 新增: DSH 那边这份缓存是 `WeakMap<Session, ReplayState>`——会话对象一被回收，
// 它那格就跟着没了。Go 的映射按 [session.SessionID] 归档，键是个值，不会自己消失，
// 所以关掉一个会话的一方要来说一声。不说也只是白占一格内存，读不出错。
func (m *TokenMeter) Forget(id session.SessionID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.states, id)
}

// Measure 量出一个会话当下的 token 账。
//
// 源: packages/llm/token-meter/src/index.ts:116-147
//
// requestHeader 给了就用它当「下一次请求要发的那份头」，nil 表示用日志里最新的那份。
// 三种基准按这个次序挑：
//
//   - 手上那个锚的头和这次要问的头**一致**：用锚的基准，位移是表面从立锚起的净变化。
//     这是常态，也是唯一一条让绝对值锚在提供方那边的路。
//   - 没有头、表面也是空的：[BaselineNone]，整张账是 0。
//   - 其余（换过模型、改过系统提示、或者还没有过任何一次带用量的响应）：
//     整份重新估价，位移归零。锚对不上就不能再拿它当基准——那等于把 A 请求的
//     绝对值配上 B 请求的增量。
func (m *TokenMeter) Measure(view projection.SessionView, requestHeader *session.EpochHeader) (Measurement, error) {
	state, err := m.sync(view)
	if err != nil {
		return Measurement{}, err
	}

	header, hasHeader := state.header, state.hasHeader
	if requestHeader != nil {
		header, hasHeader = session.CanonicalHeader(*requestHeader), true
	}

	var baseline MeasurementBaseline
	surfaceDeltaTokens := 0
	switch anchor := state.anchor; {
	case anchor != nil && optionalHeaderEquals(anchor.header, anchor.hasHeader, header, hasHeader):
		baseline = anchor.baseline
		surfaceDeltaTokens = state.surfaceTokens - anchor.surfaceTokens
	case !hasHeader && state.surfaceTokens == 0:
		baseline = MeasurementBaseline{Kind: BaselineNone}
	default:
		headerTokens, estimateErr := EstimateHeader(header)
		if estimateErr != nil {
			return Measurement{}, estimateErr
		}
		baseline = MeasurementBaseline{
			Kind:   BaselineEstimated,
			Tokens: headerTokens + state.surfaceTokens,
		}
	}

	measurement := Measurement{
		LogRevision:        state.consumedEvents,
		Baseline:           baseline,
		SurfaceDeltaTokens: surfaceDeltaTokens,
		TotalTokens:        max(0, baseline.Tokens+surfaceDeltaTokens),
		SurfaceTokens:      state.surfaceTokens,
		Nodes:              state.surface,
	}
	// 交出去的节点表必须是一份复制品：调用方（压缩那边）会把它留着，而这里那一张
	// 还要继续被后面的折叠改。DSH 那边同样的位置是 deepFreeze(structuredClone(...))。
	return measurement.Clone(), nil
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

	state, tracked := m.states[id]
	// 新增: DSH 按会话对象的身份缓存，一个从存储里重新装出来的会话天然是另一个键。
	// Go 按 [session.SessionID] 缓存，那样两次装配会撞在同一格上。日志变短是这件事
	// 唯一看得见的形态（一份日志只会往后长），撞上就从头重放。
	if tracked && state.consumedEvents > len(events) {
		tracked = false
	}
	if !tracked {
		state = &replayState{}
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
func (m *TokenMeter) foldEvent(view projection.SessionView, state *replayState, event session.Event) error {
	nextHeader, nextHasHeader := state.header, state.hasHeader
	nextStepStart := state.stepStart
	nextAnchor := state.anchor

	switch event.Type {
	case session.EventRequestHeader:
		var data session.RequestHeaderData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return fmt.Errorf("token 计量器：seq %d 的请求头读不回来：%w", event.Seq, err)
		}
		nextHeader, nextHasHeader = session.CanonicalHeader(data.Header), true
	case session.EventStepStart:
		if state.stepStart != nil {
			return fmt.Errorf("token 计量器：seq %d 的 step/start 来得比回合 %d／步骤 %d 结束还早",
				event.Seq, state.stepStart.turn, state.stepStart.step)
		}
		var data session.StepStartData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return fmt.Errorf("token 计量器：seq %d 的 step/start 读不回来：%w", event.Seq, err)
		}
		// 记下这个步骤开起来那一刻的表面：锚就是从这里起算的。
		nextStepStart = &stepMark{turn: data.Turn, step: data.Step, surfaceTokens: state.surfaceTokens}
	case session.EventStepEnd:
		var data session.StepEndData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return fmt.Errorf("token 计量器：seq %d 的 step/end 读不回来：%w", event.Seq, err)
		}
		if state.stepStart == nil || state.stepStart.turn != data.Turn || state.stepStart.step != data.Step {
			return fmt.Errorf("token 计量器：seq %d 的 step/end 配不上任何一条 step/start", event.Seq)
		}
		nextStepStart = nil
	}

	var surface surfaceTokenFold
	folded := session.IsSurfaceEvent(event)
	if folded {
		fold, err := foldSurfaceTokens(state.surface, event)
		if err != nil {
			return err
		}
		surface = fold
	}

	if event.Type == session.EventAssistantMessage {
		var data session.AssistantMessageData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return fmt.Errorf("token 计量器：seq %d 的助手消息读不回来：%w", event.Seq, err)
		}
		stepStart := state.stepStart
		if stepStart == nil || stepStart.turn != data.Turn || stepStart.step != data.Step {
			return fmt.Errorf("token 计量器：seq %d 的 assistant/message 配不上任何一条 step/start", event.Seq)
		}
		if !folded {
			// 走不到：assistant/message 是上表面的类型。真走到了说明
			// [session.IsSurfaceEvent] 和这里对「哪些事件上表面」的认识分了家，
			// 那时候把它当 0 个 token 会让整个锚偏掉，不如当场断掉。
			return fmt.Errorf("token 计量器：seq %d 的 assistant/message 没能折进表面", event.Seq)
		}

		var anchorSurfaceTokens int
		var baseline MeasurementBaseline
		if data.Usage != nil && nextHasHeader {
			// 提供方看见的是它自己那趟流产出的内容，而落进日志的那条消息可能已经
			// 被改写过（比如打断只留了一个前缀）。锚要跟提供方对齐，所以按来源
			// 那些分块重新装配一遍。
			providerAssistantTokens, err := m.estimateProviderAssistant(view, event, data, surface.tokens)
			if err != nil {
				return err
			}
			anchorSurfaceTokens = stepStart.surfaceTokens + providerAssistantTokens

			headerTokens, err := EstimateHeader(nextHeader)
			if err != nil {
				return err
			}
			estimatedAnchorTokens := headerTokens + anchorSurfaceTokens
			providerTokens := usageTokens(*data.Usage)

			// 带符号的启发式增量只有挂在一个**不小于**同口径全量估价的锚上才是保守的：
			// 锚比估价小的时候，后面每一次「减掉一段」都会从一个本来就偏低的绝对值上
			// 再减一刀，越减越离谱。这种时候宁可整份用估价，至少口径是自洽的。
			baseline = MeasurementBaseline{Kind: BaselineEstimated, Tokens: estimatedAnchorTokens}
			if providerTokens >= estimatedAnchorTokens {
				baseline = MeasurementBaseline{
					Kind:   BaselineUsage,
					Tokens: providerTokens,
					Usage:  *data.Usage,
				}
			}
		} else {
			// 没有用量（或者还没见过请求头）：照样立锚，只是基准是估出来的。
			// 立了它，后面的测量至少有个起算点，位移那一路的逻辑不用分叉。
			anchorSurfaceTokens = stepStart.surfaceTokens + surface.tokens
			headerTokens, err := EstimateHeader(nextHeader)
			if err != nil {
				return err
			}
			baseline = MeasurementBaseline{
				Kind:   BaselineEstimated,
				Tokens: headerTokens + anchorSurfaceTokens,
			}
		}
		nextAnchor = &measurementAnchor{
			header:        nextHeader,
			hasHeader:     nextHasHeader,
			surfaceTokens: anchorSurfaceTokens,
			baseline:      baseline,
		}
	}

	state.header, state.hasHeader = nextHeader, nextHasHeader
	state.stepStart = nextStepStart
	if folded {
		state.surface = surface.nodes
		state.surfaceTokens += surface.deltaTokens
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
	event session.Event,
	data session.AssistantMessageData,
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

		// DSH 那边直接 `session.events[seq]!`——日志里 seq 就是下标。这里同样按
		// 下标取，但先验一下界：Go 的越界是 panic，而这条路上的输入来自日志。
		if seq < 0 || seq >= len(events) {
			return 0, fmt.Errorf("token 计量器：seq %d 的 assistant/message 引的来源 %d 不在日志里",
				event.Seq, seq)
		}
		source := events[seq]
		if source.Type != session.EventAssistantChunk {
			return 0, fmt.Errorf("token 计量器：seq %d 的 assistant/message 引的来源 %d 不是 assistant/chunk",
				event.Seq, seq)
		}
		var sourceData session.AssistantChunkData
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
func optionalHeaderEquals(left session.EpochHeader, leftOK bool, right session.EpochHeader, rightOK bool) bool {
	if !leftOK || !rightOK {
		return leftOK == rightOK
	}
	return session.HeaderEquals(left, right)
}
