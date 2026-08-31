// 本文件的作用：在一份会话表面上算工具配对的平衡——哪一刀切下去不会把一次
// 「调用」和它的「结果」劈开。
//
// 源: packages/compaction/compaction/src/tool-pairing.ts

package compaction

import (
	"fmt"

	"ds-harness-go/llm"
	"ds-harness-go/session"
)

// SurfaceView 是算平衡要用到的那点表面状态。
//
// 源: packages/compaction/compaction/src/tool-pairing.ts:116-131（`session` 参数）
//
// 新增: DSH 收一整个 `Session` 活对象，从它身上取 `surface.nodes`、
// `surface.replaceGeneration` 和 `events`。那个类型归循环那一块
// （docs/DESIGN.md 第八节第 6 块），而这里真正要的只有这四样东西，
// 所以收窄成一个由调用方现拼的值——这也是本仓库对「消费方自己声明它需要的
// 那一小片」的一贯做法。
type SurfaceView struct {
	// Nodes 是当前表面上的节点 seq，按表面顺序。取自 [session.SurfaceFolder.Nodes]。
	Nodes []int
	// Generation 是表面的改写代数。取自 [session.SurfaceFolder.ReplaceGeneration]。
	//
	// 一次替换会把表面重排，代数跟着加一。增量状态靠它判断自己是不是过期了。
	Generation int
	// Events 是这段日志的事件，按 seq 连续排列。
	Events []session.Event
	// BaseSeq 是 Events[0] 的 seq。
	//
	// 新增: DSH 直接 `events[seq]`——它那边 seq 就是数组下标，因为一个活着的
	// 会话总是从头持有全部事件。本包的调用方可能只拿着一段后缀（重建、
	// 分页读取），所以下标要减掉这个基准；对不上时报 [ErrSurfaceCorrupt]。
	BaseSeq int
}

// eventForSeq 取出表面某个 seq 对应的那条事件。
func (v SurfaceView) eventForSeq(seq int) (session.Event, error) {
	index := seq - v.BaseSeq
	if index < 0 || index >= len(v.Events) || v.Events[index].Seq != seq {
		return session.Event{}, fmt.Errorf("%w：表面上的 seq %d 在日志里找不到对应的事件",
			ErrSurfaceCorrupt, seq)
	}
	return v.Events[index], nil
}

// BalanceIndex 是一份表面上的工具配对平衡的增量状态。
//
// 源: packages/compaction/compaction/src/tool-pairing.ts:9-23
//
// 零值可用，表示「还没跟任何表面对过」。
//
// 新增: DSH 把这份状态挂在一张以 Session 为键的 WeakMap 上，因为它要给一个
// 活对象挂旁路缓存、还得跟着对象一起被回收。Go 这边做成一个普通的值，
// 谁用谁自己拿着：一个会话就配一份，随会话一起消失，不需要弱引用。
// 拿一份状态去对另一份表面也不会算错——代数对不上就整个重建。
type BalanceIndex struct {
	// generation 是这份状态描述的那个表面改写代数。
	generation int
	// cutBalanced 是当前顺序下每一刀的平衡：N 个节点的表面有 N+1 刀，
	// 第 i 项是第 i 个节点**之前**那一刀，最后一项是表面尾巴**之后**那一刀。
	//
	// 它同时兼任「有没有对过表面」的标记：对过的状态至少有开头那一刀，
	// 所以 nil 就是没对过。这样不必再加一个布尔字段，也就没有「两个字段
	// 说法不一致」的状态。
	cutBalanced []bool
	// indexBySeq 是每个节点 seq 在当前表面上的位置，用来索引 cutBalanced。
	indexBySeq map[int]int
	// inProgressToolCalls 是已处理的表面尾巴之后还开着的调用数。
	inProgressToolCalls int
}

// BalancedBefore 判断某个表面节点**之前**那一刀是不是配平的。
//
// 源: packages/compaction/compaction/src/tool-pairing.ts:117-119
//
// 配平的意思是：没有一次还没等到结果的工具调用跨过这一刀。压缩要从这种地方
// 下刀——一次替换会把刀两侧的东西分到不同命运里，把调用留在外面、结果留在
// 里面（或者反过来）的话，模型会收到一条没有调用的结果，那是提供方直接拒收的。
//
// 这个 seq 不在当前表面上时报 [ErrSurfaceCorrupt]。
func (i *BalanceIndex) BalancedBefore(view SurfaceView, seq int) (bool, error) {
	if err := i.sync(view); err != nil {
		return false, err
	}
	return i.cutBalance(seq, 0)
}

// BalancedAfter 判断某个表面节点**之后**那一刀是不是配平的。
//
// 源: packages/compaction/compaction/src/tool-pairing.ts:129-131
func (i *BalanceIndex) BalancedAfter(view SurfaceView, seq int) (bool, error) {
	if err := i.sync(view); err != nil {
		return false, err
	}
	return i.cutBalance(seq, 1)
}

// sync 把这份状态对到当前表面上。
//
// 源: packages/compaction/compaction/src/tool-pairing.ts:76-97
//
// 三种情况：没对过、代数变了、或者已处理的节点比表面还多（表面缩短了，
// 说明前面那一段被替换掉了），都整个重建；表面只是变长了就增量往前折；
// 一样长就什么都不做。
//
// 重建走的是「从空表面那个状态开始的同一次折叠」——空表面只有开头那一刀，
// 它平凡地配平。
func (i *BalanceIndex) sync(view SurfaceView) error {
	if i.cutBalanced == nil || i.generation != view.Generation ||
		len(i.cutBalanced)-1 > len(view.Nodes) {
		rebuilt := BalanceIndex{
			generation:  view.Generation,
			cutBalanced: []bool{true},
			indexBySeq:  map[int]int{},
		}
		if err := rebuilt.extend(view); err != nil {
			return err
		}
		*i = rebuilt
		return nil
	}
	if len(i.cutBalanced)-1 < len(view.Nodes) {
		return i.extend(view)
	}
	return nil
}

// extend 把还没折进来的那截表面尾巴折进这份状态。
//
// 源: packages/compaction/compaction/src/tool-pairing.ts:53-74
//
// 先把整条尾巴验完再动这份状态：一次坏掉的追加不能留下一个折了一半的状态，
// 否则下一次调用会在一个既不是旧的也不是新的状态上继续算，而且不会报错。
func (i *BalanceIndex) extend(view SurfaceView) error {
	processed := len(i.cutBalanced) - 1
	tail := view.Nodes[processed:]

	pending := make([]bool, 0, len(tail))
	inProgress := i.inProgressToolCalls
	for _, seq := range tail {
		event, err := view.eventForSeq(seq)
		if err != nil {
			return err
		}
		delta, err := eventDelta(event)
		if err != nil {
			return err
		}
		inProgress += delta
		if inProgress < 0 {
			return fmt.Errorf("%w：表面 seq %d 上的工具结果没有在先的调用",
				ErrSurfaceCorrupt, seq)
		}
		pending = append(pending, inProgress == 0)
	}

	for offset, seq := range tail {
		i.indexBySeq[seq] = processed + offset
	}
	i.cutBalanced = append(i.cutBalanced, pending...)
	i.inProgressToolCalls = inProgress
	return nil
}

// cutBalance 取出某个节点位置加偏移之后那一刀的平衡。
//
// 源: packages/compaction/compaction/src/tool-pairing.ts:100-107
func (i *BalanceIndex) cutBalance(seq int, offset int) (bool, error) {
	index, ok := i.indexBySeq[seq]
	if !ok || index+offset >= len(i.cutBalanced) {
		return false, fmt.Errorf("%w：seq %d 不在当前表面上", ErrSurfaceCorrupt, seq)
	}
	return i.cutBalanced[index+offset], nil
}

// eventDelta 算出一条表面事件让开着的调用数变了多少。
//
// 源: packages/compaction/compaction/src/tool-pairing.ts:30-39
//
// 只有两种事件算数：一条助手消息按它里面工具调用块的个数加，一条工具结果减一。
// 别的表面事件（用户消息、注入进来的上下文）不动这个数。
//
// 数的是**内容块**而不是 step/start 这类标记，因为压缩会把表面位置整个重排：
// 步骤边界在重排之后不再对应任何一段连续的表面，而调用和结果是跟着内容走的。
func eventDelta(event session.Event) (int, error) {
	switch event.Type {
	case session.EventAssistantMessage:
		data, err := session.DecodeData(event)
		if err != nil {
			return 0, fmt.Errorf("%w：seq %d 的 assistant/message：%w",
				ErrMalformedEvent, event.Seq, err)
		}
		message, ok := data.(session.AssistantMessageData)
		if !ok {
			// 不可达：[session.DecodeData] 按 Type 分发，assistant/message 只会
			// 得到这一种负载。留着它是因为一次分发错位会让这里把一整条消息里的
			// 调用全数漏掉，于是一刀本该不配平的地方被算成配平——压缩照着下刀，
			// 模型收到一条没有调用的工具结果，而日志上看不出是谁干的。
			return 0, fmt.Errorf("%w：seq %d 声称是 assistant/message，负载却是 %T",
				ErrMalformedEvent, event.Seq, data)
		}
		calls := 0
		for _, block := range message.Message.Content {
			if _, isCall := block.(llm.ToolCallBlock); isCall {
				calls++
			}
		}
		return calls, nil
	case session.EventToolResult:
		return -1, nil
	default:
		return 0, nil
	}
}
