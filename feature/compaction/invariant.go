// 本文件的作用：本包自己拥有的那几条不变量——一次压缩的括号必须配对、必须
// 归属一个说得清的位置、而且那条替换用的检查点必须属于当时开着的那次压缩。
//
// 源: packages/compaction/compaction/src/invariant.ts

package compaction

import (
	"fmt"

	"github.com/snight1983/ds-harness-go/sessionlog"
)

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/compaction/compaction/src/invariant.ts:12
const PackageName = "@deepseek-ai/dsh-compaction"

// OpenCompaction 是当前开着的那次压缩事务。
//
// 源: packages/compaction/compaction/src/invariant.ts:19-25
type OpenCompaction struct {
	// CompactionID 是这次事务的身份。
	CompactionID ID
	// SourceCommandID 是发起它的那条人工命令；空表示不是人工发起的。
	SourceCommandID string
	// StartSeq 是那条 compaction/start 的 seq。
	StartSeq int
	// Turn 是它归属的回合号；Standalone 为真时无意义。
	Turn int
	// Standalone 为真表示它是两个回合之间的一次独立事务。
	Standalone bool
	// Summarized 记着这次事务里已经出现过一条 compaction/summary。
	Summarized bool
}

// Trace 是一个会话为了做压缩检查而记的那点账。
//
// 源: packages/compaction/compaction/src/invariant.ts:27-30
//
// 零值可用：没有开着的回合，也没有开着的压缩。
//
// 新增: DSH 那边这份账挂在一张以 Session 为键的 WeakMap 上，理由和
// [sessionlog.Trace] 那条逐字相同；本包的 Trace 就是一个普通的值，谁拥有它
// 谁自己拿着。把它接到本仓库的 invariants 注册表上是循环那一块的事
// （docs/DESIGN.md 第八节第 6 块）。
type Trace struct {
	// OpenTurn 是当前开着的回合号；TurnIsOpen 为假时无意义。
	OpenTurn   int
	TurnIsOpen bool
	// Open 是当前开着的那次压缩；IsCompacting 为假时无意义。
	Open         OpenCompaction
	IsCompacting bool
}

// Transition 是一条已经验过的事件对这份账的、还没落下去的改动。
//
// 源: packages/compaction/compaction/src/invariant.ts:32-36
//
// 分成「验」和「落」两步，理由和 [sessionlog.Transition] 逐字相同：一条事件在
// 发布前可能被别的监听方否决，验是纯的，扔掉一次转移不会让这份账往前走。
//
// 新增: DSH 的 `CompactionTransition` 是一个带 kind 的四支联合，而 kind 那个
// 字段除了给 `applyCompactionTransition` 分派之外没有别的用处。这里直接装
// 转移**之后**的完整状态，[Trace.Apply] 只管赋值——这也是 [sessionlog.Transition]
// 的做法，两个包里同一件事写成同一个样子。
type Transition struct {
	openTurn     int
	turnIsOpen   bool
	open         OpenCompaction
	isCompacting bool
}

// Validate 验一条候选事件，**不改动**这份账。
//
// 源: packages/compaction/compaction/src/invariant.ts:139-212
//
// 只返回**第一条**违反，理由和 [sessionlog.Trace.Validate] 逐字相同。
func (t Trace) Validate(event sessionlog.Event) (Transition, error) {
	return t.validate(event, false)
}

// validate 是 [Trace.Validate] 的实现，多收一个「当前这个括号已经作废了」的旗子。
//
// staleBracket 为真时不拿开着的压缩去否掉回合边界。它只在 [ValidateLog] 重放
// 一段继承来的日志时为真，见那里的注释。
func (t Trace) validate(event sessionlog.Event, staleBracket bool) (Transition, error) {
	next := Transition{
		openTurn:     t.OpenTurn,
		turnIsOpen:   t.TurnIsOpen,
		open:         t.Open,
		isCompacting: t.IsCompacting,
	}

	// 回合边界不许跨过一个还开着的压缩括号。
	//
	// 源: packages/compaction/compaction/src/invariant.ts:95-108
	//
	// 守的是「一次压缩改的是哪一段」这件事说得清：压缩期间开一个新回合，
	// 那次压缩换掉的范围就横跨了两个回合，而它的 compaction/end 只报得出
	// 一个归属。
	if event.Type == sessionlog.EventTurnStart || event.Type == sessionlog.EventTurnEnd {
		if t.IsCompacting && !staleBracket {
			return Transition{}, fmt.Errorf("%w：seq %d 的 %s 不能跨过%s",
				ErrInvariantViolated, event.Seq, event.Type, describeOwner(t.Open))
		}
	}

	switch event.Type {
	case sessionlog.EventSessionEndSeed:
		// 源: packages/compaction/compaction/src/invariant.ts:141
		//
		// 一道种子边界之前的日志是继承来的，那边还开着的压缩括号在这一侧
		// 永远等不到它的 compaction/end。清掉它就是这条边界的意思。
		next.open, next.isCompacting = OpenCompaction{}, false
		return next, nil

	case sessionlog.EventTurnStart:
		data, err := decodeTurnStart(event)
		if err != nil {
			return Transition{}, err
		}
		next.openTurn, next.turnIsOpen = data.Turn, true
		return next, nil

	case sessionlog.EventTurnEnd:
		next.openTurn, next.turnIsOpen = 0, false
		return next, nil

	case sessionlog.EventUserMessage:
		if !sessionlog.IsReplacementSurfaceEvent(event) {
			return next, nil
		}
		if err := t.validateCheckpoint(event); err != nil {
			return Transition{}, err
		}
		return next, nil

	case EventCompactionStart:
		return t.validateStart(next, event)

	case EventCompactionSummary:
		return t.validateSummary(next, event)

	case EventCompactionEnd:
		return t.validateEnd(next, event)

	default:
		return next, nil
	}
}

// validateStart 验一条 compaction/start。
//
// 源: packages/compaction/compaction/src/invariant.ts:155-172
func (t Trace) validateStart(next Transition, event sessionlog.Event) (Transition, error) {
	data, err := DecodeStart(event)
	if err != nil {
		return Transition{}, err
	}
	if err := requireID(event.Seq, "compaction/start 的 compactionId", string(data.CompactionID)); err != nil {
		return Transition{}, err
	}
	if t.IsCompacting {
		return Transition{}, fmt.Errorf("%w：seq %d 又开了一次压缩，但%s还没做完",
			ErrInvariantViolated, event.Seq, describeOwner(t.Open))
	}
	if err := t.validateOwner(event.Seq, event.Type, data.Turn, data.Standalone); err != nil {
		return Transition{}, err
	}
	next.open = OpenCompaction{
		CompactionID:    data.CompactionID,
		SourceCommandID: data.SourceCommandID,
		StartSeq:        event.Seq,
		Turn:            data.Turn,
		Standalone:      data.Standalone,
	}
	next.isCompacting = true
	return next, nil
}

// validateSummary 验一条 compaction/summary。
//
// 源: packages/compaction/compaction/src/invariant.ts:174-198
func (t Trace) validateSummary(next Transition, event sessionlog.Event) (Transition, error) {
	data, err := DecodeSummary(event)
	if err != nil {
		return Transition{}, err
	}
	if err := requireID(event.Seq, "compaction/summary 的 compactionId", string(data.CompactionID)); err != nil {
		return Transition{}, err
	}
	if err := t.requireOpen(event.Seq, event.Type, data.CompactionID, data.SourceCommandID); err != nil {
		return Transition{}, err
	}
	if err := t.validateOwner(event.Seq, event.Type, t.Open.Turn, t.Open.Standalone); err != nil {
		return Transition{}, err
	}
	if t.Open.Summarized {
		return Transition{}, fmt.Errorf("%w：seq %d 是这次压缩里的第二条 compaction/summary",
			ErrInvariantViolated, event.Seq)
	}

	// 下面三条查的是这条事件和它自己的阴影范围对不对得上。它们是那条影子价格
	// 约定的地基：紧跟其后的那条替换靠这里的 shadowed* 记账，对不上的话
	// 消费方减掉的就不是它真正换掉的那一段。
	if len(data.ShadowedSeqs) == 0 {
		return Transition{}, fmt.Errorf("%w：seq %d 的 shadowedSeqs 不能是空的",
			ErrInvariantViolated, event.Seq)
	}
	if data.ShadowedSeqs[0] != data.ShadowedRange.Start ||
		data.ShadowedSeqs[len(data.ShadowedSeqs)-1] != data.ShadowedRange.End {
		return Transition{}, fmt.Errorf(
			"%w：seq %d 的 shadowedRange 是 %d..%d，shadowedSeqs 的头尾却是 %d..%d",
			ErrInvariantViolated, event.Seq, data.ShadowedRange.Start, data.ShadowedRange.End,
			data.ShadowedSeqs[0], data.ShadowedSeqs[len(data.ShadowedSeqs)-1])
	}
	if data.ShadowedTokenCount < 0 {
		// 新增: DSH 还要查 Number.isSafeInteger——JS 的 number 是浮点，
		// 一个 0.5 或者 2^53 之外的整数都进得来。Go 的 int 已经把这两件事挡掉了，
		// 只剩下这一半。
		return Transition{}, fmt.Errorf("%w：seq %d 的 shadowedTokenCount 是 %d，不能是负数",
			ErrInvariantViolated, event.Seq, data.ShadowedTokenCount)
	}

	next.open.Summarized = true
	return next, nil
}

// validateEnd 验一条 compaction/end。
//
// 源: packages/compaction/compaction/src/invariant.ts:200-212
func (t Trace) validateEnd(next Transition, event sessionlog.Event) (Transition, error) {
	data, err := DecodeEnd(event)
	if err != nil {
		return Transition{}, err
	}
	if err := requireID(event.Seq, "compaction/end 的 compactionId", string(data.CompactionID)); err != nil {
		return Transition{}, err
	}
	if err := t.requireOpen(event.Seq, event.Type, data.CompactionID, data.SourceCommandID); err != nil {
		return Transition{}, err
	}
	if data.Turn != t.Open.Turn || data.Standalone != t.Open.Standalone {
		return Transition{}, fmt.Errorf("%w：seq %d 的 compaction/end 归属%s，compaction/start 归属的是%s",
			ErrInvariantViolated, event.Seq,
			describeOwner(OpenCompaction{Turn: data.Turn, Standalone: data.Standalone}),
			describeOwner(t.Open))
	}
	// 这一条永远成立：回合状态在一个压缩括号期间是冻住的（回合边界跨不过去），
	// 而上面刚查过 end 报的归属和 start 一致。留着它是因为它和
	// [Trace.validateSummary] 那一处是同一条规则——摘要那边真的会失败
	// （一段继承来的日志里，作废的括号让回合边界放行，回合号于是换得掉），
	// 少写一处会让人以为两者守的不是同一件事。
	if err := t.validateOwner(event.Seq, event.Type, t.Open.Turn, t.Open.Standalone); err != nil {
		return Transition{}, err
	}
	if data.Error == "" && !t.Open.Summarized {
		// 一次成功的压缩必须留下它的摘要：没有摘要的成功结束意味着表面被换掉了，
		// 而换上去的是什么、按什么价格算的，日志里查不到。
		return Transition{}, fmt.Errorf("%w：seq %d 是一次成功的 compaction/end，却没有配对的 compaction/summary",
			ErrInvariantViolated, event.Seq)
	}
	next.open, next.isCompacting = OpenCompaction{}, false
	return next, nil
}

// validateCheckpoint 验一条替换用的压缩检查点。
//
// 源: packages/compaction/compaction/src/invariant.ts:56-73
//
// 不是检查点的替换消息（别的层做的替换）原样放过——认不认得出是这里的事，
// 认出来之后合不合规才是不变量的事。
func (t Trace) validateCheckpoint(event sessionlog.Event) error {
	message, err := decodeUserMessage(event)
	if err != nil {
		return err
	}
	checkpoint, isCheckpoint, err := CheckpointSourceOf(message.Source)
	if err != nil {
		return err
	}
	if !isCheckpoint {
		return nil
	}
	if err := requireID(event.Seq, "压缩检查点的 compactionId", string(checkpoint.CompactionID)); err != nil {
		return err
	}
	return t.requireOpen(event.Seq, "压缩检查点", checkpoint.CompactionID, checkpoint.SourceCommandID)
}

// requireOpen 查一条事件报的身份和发起命令，和当前开着的那次压缩对得上。
//
// 源: packages/compaction/compaction/src/invariant.ts:42-54
//
// 新增: DSH 还要单独查一次「sourceCommandId 存在时必须是个非空字符串」。
// 这里 [CheckpointSource.SourceCommandID] 是普通的 string，空串**就是**「没有」，
// 「有但是空的」这个状态压根表达不出来，所以那条检查跟着消失——下面这一句
// 相等比较把它盖住了。
func (t Trace) requireOpen(seq int, kind any, id ID, sourceCommandID string) error {
	if !t.IsCompacting {
		return fmt.Errorf("%w：seq %d 的 %v 没有配对的 compaction/start",
			ErrInvariantViolated, seq, kind)
	}
	if id != t.Open.CompactionID {
		return fmt.Errorf("%w：seq %d 的 %v 报的 compactionId 是 %q，compaction/start 上的是 %q",
			ErrInvariantViolated, seq, kind, id, t.Open.CompactionID)
	}
	if sourceCommandID != t.Open.SourceCommandID {
		return fmt.Errorf("%w：seq %d 的 %v 报的 sourceCommandId 是 %q，compaction/start 上的是 %q",
			ErrInvariantViolated, seq, kind, sourceCommandID, t.Open.SourceCommandID)
	}
	return nil
}

// validateOwner 查一个压缩括号的归属：带回合号的必须严格落在那个回合里，
// 独立事务只能落在两个回合之间。
//
// 源: packages/compaction/compaction/src/invariant.ts:123-137
func (t Trace) validateOwner(seq int, kind sessionlog.EventType, turn int, standalone bool) error {
	if standalone {
		if t.TurnIsOpen {
			return fmt.Errorf("%w：seq %d 的 %s 自称是独立事务，但回合 %d 开着",
				ErrInvariantViolated, seq, kind, t.OpenTurn)
		}
		return nil
	}
	if !t.TurnIsOpen {
		return fmt.Errorf("%w：seq %d 的 %s 归属回合 %d，却追加在任何开着的回合之外",
			ErrInvariantViolated, seq, kind, turn)
	}
	if turn != t.OpenTurn {
		return fmt.Errorf("%w：seq %d 的 %s 说的是回合 %d，开着的却是回合 %d",
			ErrInvariantViolated, seq, kind, turn, t.OpenTurn)
	}
	return nil
}

// Apply 把一次已经验过的转移落到这份账上。
//
// 源: packages/compaction/compaction/src/invariant.ts:214-238
func (t *Trace) Apply(transition Transition) {
	t.OpenTurn, t.TurnIsOpen = transition.openTurn, transition.turnIsOpen
	t.Open, t.IsCompacting = transition.open, transition.isCompacting
}

// ValidateLog 把一整段日志按本包的不变量走一遍，返回走完之后的那份账。
//
// 源: packages/compaction/compaction/src/invariant.ts:300-306（install 里的 seed）
//
// # 为什么要先算一遍作废的括号
//
// 一段继承来的日志里可能有一个再也等不到 compaction/end 的压缩括号，它由后面
// 那条 session/end-seed 作废掉。可是**修复用的回合边界排在那条标记之前**：
// 重放到那里时，那个马上要被清掉的括号还开着，于是它会去否掉修复自己的
// turn/end。所以先扫一遍找出这些括号，重放到它们身上时不拿它们去否回合边界。
func ValidateLog(events []sessionlog.Event) (Trace, error) {
	stale := orphanStartSeqs(events)

	var trace Trace
	for _, event := range events {
		staleBracket := false
		if trace.IsCompacting {
			_, staleBracket = stale[trace.Open.StartSeq]
		}
		transition, err := trace.validate(event, staleBracket)
		if err != nil {
			return Trace{}, err
		}
		trace.Apply(transition)
	}
	return trace, nil
}

// orphanStartSeqs 找出那些「开着的时候撞上一条 session/end-seed」的压缩起点。
//
// 源: packages/compaction/compaction/src/invariant.ts:76-93
func orphanStartSeqs(events []sessionlog.Event) map[int]struct{} {
	stale := map[int]struct{}{}
	openStartSeq, isOpen := 0, false
	for _, event := range events {
		switch event.Type {
		case EventCompactionStart:
			openStartSeq, isOpen = event.Seq, true
		case EventCompactionEnd:
			isOpen = false
		case sessionlog.EventSessionEndSeed:
			if isOpen {
				stale[openStartSeq] = struct{}{}
			}
			isOpen = false
		}
	}
	return stale
}

// requireID 要求一个持久的不透明身份非空。
//
// 源: packages/compaction/compaction/src/invariant.ts:38-40
//
// 新增: DSH 那条还查 `typeof value !== 'string'`，因为它那些负载是 `unknown`
// 解出来的。Go 这一侧类型由 [DecodeStart] 那几个负载结构体钉死了，
// 剩下的只有「空」这一种。
func requireID(seq int, label string, value string) error {
	if value == "" {
		return fmt.Errorf("%w：seq %d 的%s不能是空的", ErrInvariantViolated, seq, label)
	}
	return nil
}

// describeOwner 把一次压缩的归属说成一句人话。
func describeOwner(open OpenCompaction) string {
	if open.Standalone {
		return "一次独立压缩"
	}
	return fmt.Sprintf("回合 %d 的压缩", open.Turn)
}

// decodeTurnStart 读回一条 turn/start 的负载。
func decodeTurnStart(event sessionlog.Event) (sessionlog.TurnStartData, error) {
	data, err := sessionlog.DecodeData(event)
	if err != nil {
		return sessionlog.TurnStartData{}, fmt.Errorf("%w：seq %d 的 turn/start：%w",
			ErrMalformedEvent, event.Seq, err)
	}
	start, ok := data.(sessionlog.TurnStartData)
	if !ok {
		// 不可达：[sessionlog.DecodeData] 按 Type 分发，turn/start 只会得到这一种负载。
		// 留着它是因为一次分发错位会让本包把一道回合边界整个看漏，
		// 于是一个跨过了回合的压缩括号会被判成合规的。
		return sessionlog.TurnStartData{}, fmt.Errorf("%w：seq %d 声称是 turn/start，负载却是 %T",
			ErrMalformedEvent, event.Seq, data)
	}
	return start, nil
}

// decodeUserMessage 读回一条 user/message 的负载。
func decodeUserMessage(event sessionlog.Event) (sessionlog.UserMessageData, error) {
	data, err := sessionlog.DecodeData(event)
	if err != nil {
		return sessionlog.UserMessageData{}, fmt.Errorf("%w：seq %d 的 user/message：%w",
			ErrMalformedEvent, event.Seq, err)
	}
	message, ok := data.(sessionlog.UserMessageData)
	if !ok {
		// 不可达，理由同 [decodeTurnStart]：看漏一条替换消息，等于放过一个
		// 不属于当前压缩的检查点。
		return sessionlog.UserMessageData{}, fmt.Errorf("%w：seq %d 声称是 user/message，负载却是 %T",
			ErrMalformedEvent, event.Seq, data)
	}
	return message, nil
}
