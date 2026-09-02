// 本文件的作用：事件日志之上的那层「表面」——模型看得见的那条有序视图，
// 以及一条事件能不能上表面、怎么上、上去之后盖掉了谁。
//
// 源: packages/core/session/src/surface.ts

package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/snight1983/ds-harness-go/llm"
)

// surfaceEventTypes 是那三种会产出模型消息的事件类型。
//
// 源: packages/core/session/src/surface.ts:15-19
var surfaceEventTypes = map[EventType]struct{}{
	EventUserMessage:      {},
	EventAssistantMessage: {},
	EventToolResult:       {},
}

// IsSurfaceEligibleType 判断一个事件类型能不能上模型可见的那条表面。
//
// 源: packages/core/session/src/surface.ts:26-28
func IsSurfaceEligibleType(kind EventType) bool {
	_, ok := surfaceEventTypes[kind]
	return ok
}

// IsSurfaceEvent 判断一条事件是不是**在**表面上——类型够格，而且带着必须的标记。
//
// 源: packages/core/session/src/surface.ts:35-38
func IsSurfaceEvent(event Event) bool {
	return IsSurfaceEligibleType(event.Type) && event.SurfaceOp != nil
}

// IsAppendSurfaceEvent 判断一条事件是不是**追加进来**的表面节点：
// 它在自己那个日志位置上进的表面，本身从来不是某次替换的副本。
//
// 源: packages/core/session/src/surface.ts:51-55
//
// 模型可见的表面有意遮住被替换掉的那一段，所以它不是给人看的那份逐字记录的
// 来源——一次落地的替换会把使用者已经看过的对话抹掉。追加进来的那些节点才是
// 那份记录的持久素材，替换出来的副本只给模型看。
func IsAppendSurfaceEvent(event Event) bool {
	if !IsSurfaceEvent(event) {
		return false
	}
	return event.SurfaceOp.SurfaceOpKind() == OpAppend
}

// IsReplacementSurfaceEvent 判断一条事件是不是一次表面替换：它盖掉了表面上
// 已有的一段，而不是加在末尾。是 [IsAppendSurfaceEvent] 在 [SurfaceOp] 两个变体上的
// 另一半。
//
// 源: packages/core/session/src/surface.ts:64-68
func IsReplacementSurfaceEvent(event Event) bool {
	if !IsSurfaceEvent(event) {
		return false
	}
	return event.SurfaceOp.SurfaceOpKind() == OpReplace
}

// DeriveEventMessage 把一条事件投影成它派生出的那条模型消息。
//
// 源: packages/core/session/src/surface.ts:83-114
//
// 第二个返回值为假表示这条事件不产出消息——一条不上表面的事件（分块、边界、
// 只进日志的记录），或者一条内容为空的助手消息（它只是用来挂 usage 的）。
//
// 这就是**逐节点的那条投影规则**：活着的会话在表面上折它，离线重建方在一段
// 日志前缀的表面上折同一个函数，重建出任何一次请求当初是照着哪些消息发的。
//
// 普通提示和注入进来的上下文都以用户角色原样投影，**不要**在这里按类型再包一层
// 框（比如 `<context>`）：包框这件事归产出方——要么由它自己烤进 content 里，
// 要么将来由事件的 meta 加一个专门的渲染器驱动，这个投影本身永远是原样透传。
func DeriveEventMessage(event Event) (llm.Message, bool, error) {
	// 有意**不穷举**：只有产出消息的那几种事件派生历史，回合／步骤边界、
	// 分块、用量、错误都是留痕和回放用的数据。
	switch event.Type {
	case EventUserMessage:
		var data UserMessageData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return llm.Message{}, false, wrapMalformed("用户消息事件的负载读不回来", err)
		}
		return data.Message, true, nil
	case EventAssistantMessage:
		var data AssistantMessageData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return llm.Message{}, false, wrapMalformed("助手消息事件的负载读不回来", err)
		}
		// 内容为空的助手消息跳过：它的存在只是为了挂一个撞了长度上限的步骤的
		// 用量，不能往提供方的记录里插进一个没有内容的助手轮次。
		if len(data.Message.Content) == 0 {
			return llm.Message{}, false, nil
		}
		return data.Message, true, nil
	case EventToolResult:
		var data ToolResultData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return llm.Message{}, false, wrapMalformed("工具结果事件的负载读不回来", err)
		}
		return data.Message, true, nil
	default:
		// 一条不上表面的事件投影不出消息。这个联合是开放的，所以这里没有
		// 「不该走到」的断言。
		return llm.Message{}, false, nil
	}
}

// SurfaceFoldReplacement 是折表面时观察到的一次替换操作。
//
// 源: packages/core/session/src/surface.ts:116-126（SurfaceFoldReplacement）
type SurfaceFoldReplacement struct {
	// Seq 是执行这次替换的那条事件的 seq。
	Seq int
	// Start 是声明的被替换区间起始 seq（含）。
	Start int
	// End 是声明的被替换区间结束 seq（含）。
	End int
	// ShadowedSeqs 是这次操作实际移走的那些表面条目，按表面顺序。
	ShadowedSeqs []int
}

// SurfaceFoldResult 是把一段日志里的表面操作全部重放一遍的结果。
//
// 源: packages/core/session/src/surface.ts:128-134（SurfaceFoldResult）
type SurfaceFoldResult struct {
	// Nodes 是当前表面上的那些事件 seq，按模型可见的顺序。
	Nodes []int
	// Replacements 是那些替换操作，按事件顺序。
	Replacements []SurfaceFoldReplacement
}

// SurfaceOpOf 验一条事件在自己这一层上够不够格上表面，并给出它的操作。
//
// 源: packages/core/session/src/surface.ts:185-208
//
// 不够格的类型带了 SurfaceOp 或 SourceEventSeqs 是错的；够格的类型不带 SurfaceOp
// 也是错的。第二个返回值为假表示这条事件不上表面。
//
// 新增: DSH 那边还要在运行期辨认 surfaceOp 的形状（是不是字符串、是不是恰好
// 三个键的对象），因为它拿到的是 unknown。Go 这边那件事已经在
// [UnmarshalSurfaceOp] 里做完了——能装进 [Event.SurfaceOp] 的只有两个封好的变体，
// 所以这里只剩下「该带的带没带」这一条。
func SurfaceOpOf(event Event) (SurfaceOp, bool, error) {
	if !IsSurfaceEligibleType(event.Type) {
		if event.SurfaceOp != nil {
			return nil, false, fmt.Errorf("%w：事件 %q 不够格上表面，不能带 surfaceOp",
				ErrSurfaceViolation, event.Type)
		}
		if event.SourceEventSeqs != nil {
			return nil, false, fmt.Errorf("%w：事件 %q 不够格上表面，不能带 sourceEventSeqs",
				ErrSurfaceViolation, event.Type)
		}
		return nil, false, nil
	}
	if event.SurfaceOp == nil {
		return nil, false, fmt.Errorf("%w：事件 %q 够格上表面，必须带一个 surfaceOp 标记",
			ErrSurfaceViolation, event.Type)
	}
	return event.SurfaceOp, true, nil
}

// assertProvenance 验这条事件引用的那些来源 seq，对照更早的日志条目和被盖掉的区间。
//
// 源: packages/core/session/src/surface.ts:211-243
func assertProvenance(event Event, shadowedSeqs []int) error {
	sources := make(map[int]struct{}, len(event.SourceEventSeqs))
	if event.SourceEventSeqs != nil {
		if len(event.SourceEventSeqs) == 0 && event.Type != EventAssistantMessage {
			return fmt.Errorf("%w：只有 assistant/message 的 sourceEventSeqs 可以是空的", ErrSurfaceViolation)
		}
		for _, source := range event.SourceEventSeqs {
			if source < 0 {
				return fmt.Errorf("%w：事件 %q 的 sourceEventSeqs 必须都是非负的 seq，实际有 %d",
					ErrSurfaceViolation, event.Type, source)
			}
			if _, seen := sources[source]; seen {
				return fmt.Errorf("%w：sourceEventSeqs 里不能有重复，%d 出现了两次",
					ErrSurfaceViolation, source)
			}
			sources[source] = struct{}{}
		}
		for _, source := range event.SourceEventSeqs {
			if source >= event.Seq {
				return fmt.Errorf("%w：sourceEventSeqs 只能引用更早的事件：%d 不小于当前的 seq %d",
					ErrSurfaceViolation, source, event.Seq)
			}
		}
	}
	var missing []int
	for _, seq := range shadowedSeqs {
		if _, ok := sources[seq]; !ok {
			missing = append(missing, seq)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w：表面替换的 sourceEventSeqs 必须列出每一个被盖掉的节点，少了 %v",
			ErrSurfaceViolation, missing)
	}
	return nil
}

// surfacePlan 是一次验过、但还没落到折叠状态上的表面转移。
type surfacePlan struct {
	// kind 是这次转移的种类；空表示这条事件不上表面。
	kind SurfaceOpKind
	// seq 是执行这次转移的那条事件的 seq。
	seq int
	// start 与 end 是替换声明的区间两端。
	start, end int
	// startIndex 与 endIndex 是那两端在当前表面上的下标。
	startIndex, endIndex int
	// shadowedSeqs 是会被移走的那些表面条目。
	shadowedSeqs []int
}

// surfaceState 是完整折叠与增量折叠共用的那点可变状态。
type surfaceState struct {
	nodes             []int
	replaceGeneration int
}

// replacementRange 在不改动当前折叠状态的前提下定位一次替换的区间。
// 第四个返回值为假表示这个区间在现存的表面上一点都不剩了。
//
// 源: packages/core/session/src/surface.ts:246-266
//
// 新增: 上游一个端点定位不到就一律违规，靠的是「日志一条不删，所以表面上少了东西
// 只能是日志坏了」。本仓库的日志会从最老的一头弹出事件（见 docs/session-log-limit.md），
// 所以一个定位不到的端点得先分两种（原则第 4 条）：它落在 baseSeq 之前，就是**被弹掉了**
// ——正常损耗，不是违规；它不小于 baseSeq 却仍然不在表面上，才是这份日志真的坏了。
//
// 起点被弹掉时区间往前收到表面的最前端：现存的每个节点的 seq 都不小于 baseSeq，
// 也就都晚于那个被弹掉的起点，所以它们要么本来就在这个区间里、要么就在它右边，
// 而右边那一半由终点的下标切掉。收多了收少了都由 [assertProvenance] 兜住——
// 它要求这条事件的 sourceEventSeqs 列出每一个被盖掉的节点。
//
// 终点也被弹掉时（这时起点必然也被弹掉了）整个区间都没了，交回假，
// 调用方把这次替换降级成一次追加。
func (s *surfaceState) replacementRange(op ReplaceOp, baseSeq int) (
	startIndex, endIndex int, shadowed []int, spans bool, err error,
) {
	startIndex = slices.Index(s.nodes, op.Start)
	if startIndex == -1 {
		if op.Start >= baseSeq {
			return 0, 0, nil, false, fmt.Errorf("%w：表面替换的起始 seq %d 不在表面上",
				ErrSurfaceViolation, op.Start)
		}
		startIndex = 0
	}
	endIndex = slices.Index(s.nodes, op.End)
	if endIndex == -1 {
		if op.End >= baseSeq {
			return 0, 0, nil, false, fmt.Errorf("%w：表面替换的结束 seq %d 不在表面上",
				ErrSurfaceViolation, op.End)
		}
		return 0, 0, nil, false, nil
	}
	if startIndex > endIndex {
		return 0, 0, nil, false, fmt.Errorf("%w：表面替换的起始 seq %d（下标 %d）排在结束 seq %d（下标 %d）后面",
			ErrSurfaceViolation, op.Start, startIndex, op.End, endIndex)
	}
	return startIndex, endIndex, slices.Clone(s.nodes[startIndex : endIndex+1]), true, nil
}

// assertToolResultRewrite 把一次工具结果的替换限制成「只改当前那一条结果的内容」。
//
// 源: packages/core/session/src/surface.ts:287-318
//
// 新增: DSH 是把两份负载各复制一份、把内容置空、再做一次 JSON 深比较。
// Go 这边直接逐字段比：除了内容之外的每一个字段都点名比一遍，
// 比 JSON 深比较更清楚，也更严——meta 比的是原始字节，不受键序影响。
func assertToolResultRewrite(event Event, shadowedSeqs []int, events []Event, baseSeq int) error {
	if event.Type != EventToolResult {
		return nil
	}
	if len(shadowedSeqs) != 1 {
		return fmt.Errorf("%w：工具结果的表面替换必须恰好重写当前的一个节点，实际 %d 个",
			ErrSurfaceViolation, len(shadowedSeqs))
	}
	// 减完起点当场校验对上的还是同一条（原则第 2 条）：seq 减 baseSeq 是下标，
	// 这个等式只在事件连续时成立，而连续是 [planSurfaceEvent] 一条条验出来的，
	// 不是这里能假定的。
	index := shadowedSeqs[0] - baseSeq
	if index < 0 || index >= len(events) ||
		events[index].Seq != shadowedSeqs[0] ||
		events[index].Type != EventToolResult {
		return fmt.Errorf("%w：工具结果的表面替换必须指向当前的一条 tool/result", ErrSurfaceViolation)
	}

	var original, replacement ToolResultData
	if err := json.Unmarshal(events[index].Data, &original); err != nil {
		return wrapMalformed("被替换的工具结果读不回来", err)
	}
	if err := json.Unmarshal(event.Data, &replacement); err != nil {
		return wrapMalformed("替换用的工具结果读不回来", err)
	}
	same, err := toolResultsMatchExceptContent(original, replacement)
	if err != nil {
		return err
	}
	if !same {
		return fmt.Errorf("%w：工具结果的表面替换只能改内容", ErrSurfaceViolation)
	}
	return nil
}

// toolResultsMatchExceptContent 比较两份工具结果负载，跳过结果块里的内容。
func toolResultsMatchExceptContent(a, b ToolResultData) (bool, error) {
	if a.Turn != b.Turn || a.Step != b.Step {
		return false, nil
	}
	if (a.Error == nil) != (b.Error == nil) {
		return false, nil
	}
	if a.Error != nil && *a.Error != *b.Error {
		return false, nil
	}
	if !bytes.Equal(a.Meta, b.Meta) {
		return false, nil
	}
	if a.Message.ID != b.Message.ID || a.Message.Role != b.Message.Role {
		return false, nil
	}
	sameSource, err := sameMessageSource(a.Message.Source, b.Message.Source)
	if err != nil || !sameSource {
		return false, err
	}
	if len(a.Message.Content) != 1 || len(b.Message.Content) != 1 {
		return false, nil
	}
	first, firstOK := a.Message.ToolResult()
	second, secondOK := b.Message.ToolResult()
	if !firstOK || !secondOK {
		return false, nil
	}
	return first.ToolCallID == second.ToolCallID && first.IsError == second.IsError, nil
}

// sameMessageSource 比较两条消息的来路。
//
// 新增: [llm.MessageSource] 是一个接口，Go 的 == 对它只在动态类型可比较时有意义，
// 而 UnknownSource 里带着一段切片，直接比会 panic。排成字节再比是稳的：
// 每个变体的 MarshalJSON 都是确定的，而未知变体保管的就是原始字节。
func sameMessageSource(a, b llm.MessageSource) (bool, error) {
	if a == nil || b == nil {
		return a == nil && b == nil, nil
	}
	first, err := json.Marshal(a)
	if err != nil {
		return false, wrapMalformed("消息来路排不出去", err)
	}
	second, err := json.Marshal(b)
	if err != nil {
		return false, wrapMalformed("消息来路排不出去", err)
	}
	return bytes.Equal(first, second), nil
}

// planSurfaceEvent 在重放边界上验一条事件，并备好它那次原子的折叠转移。
//
// 源: packages/core/session/src/surface.ts:321-347
func planSurfaceEvent(
	state *surfaceState,
	event Event,
	expectedSeq int,
	events []Event,
	baseSeq int,
) (surfacePlan, error) {
	if event.Seq != expectedSeq {
		return surfacePlan{}, fmt.Errorf("%w：事件的 seq %d 不连续，期望 %d",
			ErrSurfaceViolation, event.Seq, expectedSeq)
	}
	operation, onSurface, err := SurfaceOpOf(event)
	if err != nil {
		return surfacePlan{}, err
	}
	if !onSurface {
		return surfacePlan{}, nil
	}
	if operation.SurfaceOpKind() == OpAppend {
		if err := assertProvenance(event, nil); err != nil {
			return surfacePlan{}, err
		}
		return surfacePlan{kind: OpAppend, seq: event.Seq}, nil
	}

	// [SurfaceOp] 是个封好的联合，两个变体都在本包里，所以这条断言只可能因为
	// 本包自己加了第三个变体而失败——那时候要的是当场炸，不是往下算出一个错的表面。
	replace, ok := operation.(ReplaceOp)
	if !ok {
		return surfacePlan{}, fmt.Errorf("%w：认不得的表面操作 %q", ErrSurfaceViolation, operation.SurfaceOpKind())
	}
	startIndex, endIndex, shadowed, spans, err := state.replacementRange(replace, baseSeq)
	if err != nil {
		return surfacePlan{}, err
	}
	if err := assertProvenance(event, shadowed); err != nil {
		return surfacePlan{}, err
	}
	if !spans {
		// 要盖的东西全被弹掉了。这条事件自己还在，照旧要上表面——降级成一次追加。
		// 它没盖住任何东西，所以也不进 [SurfaceFoldResult.Replacements]。
		// 工具结果那道重写检查在这里跳过：被它重写的那条原件已经没了，无从比起。
		return surfacePlan{kind: OpAppend, seq: event.Seq}, nil
	}
	if err := assertToolResultRewrite(event, shadowed, events, baseSeq); err != nil {
		return surfacePlan{}, err
	}
	return surfacePlan{
		kind:         OpReplace,
		seq:          event.Seq,
		start:        replace.Start,
		end:          replace.End,
		startIndex:   startIndex,
		endIndex:     endIndex,
		shadowedSeqs: shadowed,
	}, nil
}

// applySurfacePlan 落实一次已经验过的表面转移。
//
// 源: packages/core/session/src/surface.ts:362-379
//
// 第二个返回值为假表示这次转移不是替换（没上表面，或者只是追加）。
func applySurfacePlan(state *surfaceState, plan surfacePlan) (SurfaceFoldReplacement, bool) {
	if plan.kind == OpAppend {
		state.nodes = append(state.nodes, plan.seq)
		return SurfaceFoldReplacement{}, false
	}
	if plan.kind != OpReplace {
		// 空的 kind 表示这条事件不上表面。
		return SurfaceFoldReplacement{}, false
	}
	// 先把尾巴复制出来，再往原地写——否则 append 会踩到还没读的那一段。
	tail := slices.Clone(state.nodes[plan.endIndex+1:])
	state.nodes = append(append(state.nodes[:plan.startIndex], plan.seq), tail...)
	state.replaceGeneration++
	return SurfaceFoldReplacement{
		Seq:          plan.seq,
		Start:        plan.start,
		End:          plan.end,
		ShadowedSeqs: plan.shadowedSeqs,
	}, true
}

// FoldSurface 把一整段会话日志按表面折叠规则重放一遍。
//
// 源: packages/core/session/src/surface.ts:387-395
//
// 事件必须按连续的 seq 顺序给，第一条的 seq 就是 baseSeq。
// 违反表面元数据、来源引用、区间或工具结果重写规则时返回 [ErrSurfaceViolation]。
//
// 新增: 上游这里拿下标当绝对 seq、并写死起点 0，靠的是「日志从 0 起、一条不删」。
// 本仓库的日志会从最老的一头弹出事件（见 docs/session-log-limit.md 的原则第 1 条），
// 起点因此得由调用方说出来——增量那条路（[SurfaceFolder]）本来就有这个数，
// 只有整折这条写死了。
func FoldSurface(events []Event, baseSeq int) (SurfaceFoldResult, error) {
	state := &surfaceState{}
	var replacements []SurfaceFoldReplacement
	for index, event := range events {
		plan, err := planSurfaceEvent(state, event, baseSeq+index, events, baseSeq)
		if err != nil {
			return SurfaceFoldResult{}, err
		}
		if replacement, ok := applySurfacePlan(state, plan); ok {
			replacements = append(replacements, replacement)
		}
	}
	return SurfaceFoldResult{Nodes: slices.Clone(state.nodes), Replacements: replacements}, nil
}

// SurfaceFolder 是那条有序表面的增量视图，兼作追加边界上的校验器。
//
// 源: packages/core/session/src/surface.ts:397-460（SurfaceManager）
//
// 新增: DSH 那个 SurfaceManager 握着调用方那个会自己变长的日志数组，
// 每次读属性时补折一段增量，还缓存一份 _pendingPlan 免得同一条事件规划两遍。
// Go 这边把关系倒过来：事件由 [SurfaceFolder.Push] 交进来，折叠器自己留着它们。
//
// 缓存那份计划的优化去掉了——在状态没变的前提下重新规划是确定的，
// 省下的那点计算换来的是「计划和状态可能对不上」这一整类竞态。
// 折叠器仍然留着看过的事件，因为 [assertToolResultRewrite] 要回头查那条被替换的
// 工具结果。
type SurfaceFolder struct {
	state   surfaceState
	events  []Event
	baseSeq int
}

// NewSurfaceFolder 建一个空的折叠器，baseSeq 是即将交进来的第一条事件的绝对 seq。
func NewSurfaceFolder(baseSeq int) *SurfaceFolder {
	return &SurfaceFolder{baseSeq: baseSeq}
}

// ValidateNext 验下一条候选事件，**不改动**已经落定的表面。
//
// 源: packages/core/session/src/surface.ts:421-429
//
// 这是循环在把一条事件写进日志之前那道检查：验不过就别写。
func (f *SurfaceFolder) ValidateNext(event Event) error {
	_, err := planSurfaceEvent(&f.state, event, f.baseSeq+len(f.events), f.events, f.baseSeq)
	return err
}

// Push 收下一条事件，把它折进表面。
//
// 第二个返回值为假表示这条事件没有替换任何东西（没上表面，或者只是追加）。
// 出错时表面**不动**，这条事件也不会被留下。
func (f *SurfaceFolder) Push(event Event) (SurfaceFoldReplacement, bool, error) {
	plan, err := planSurfaceEvent(&f.state, event, f.baseSeq+len(f.events), f.events, f.baseSeq)
	if err != nil {
		return SurfaceFoldReplacement{}, false, err
	}
	f.events = append(f.events, event)
	replacement, replaced := applySurfacePlan(&f.state, plan)
	return replacement, replaced, nil
}

// Nodes 给出当前表面上的那些事件 seq，按模型可见的顺序。
//
// 返回的是一份复制，调用方改它不会动到折叠器。
func (f *SurfaceFolder) Nodes() []int { return slices.Clone(f.state.nodes) }

// ReplaceGeneration 是已落定的位置替换次数，单调递增。
func (f *SurfaceFolder) ReplaceGeneration() int { return f.state.replaceGeneration }
