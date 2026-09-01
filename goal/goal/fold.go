// 本文件的作用：那条**严格**的回放——把一串会话事件折成此刻的目标状态，
// 顺带把每一条接不上的改动当场拒掉。
//
// 源: packages/goal/goal/src/fold.ts
//
// 这里和 [ApplyProjection] 那条轻量投影是两件事，别混：那一条是「最后写的赢」，
// 读不动就原样返回；这一条一个字节都不放过，因为它是不变量那一侧的判据。

package goal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
)

// maxSafeInteger 是 IEEE 754 双精度还能逐个数清楚的最大整数。
//
// 源: packages/goal/goal/src/fold.ts:58 (Number.isSafeInteger)
//
// 新增: Go 这边这些字段都是 int，本来不需要这条上限。留着是因为它是**介质上的
// 约定**：这些字节要和 DSH 那一侧互读，一个超过安全整数的修订号在那边会悄悄丢
// 精度。所以在解码这一层就把它挡住，而不是等到对面算错。
const maxSafeInteger int64 = 1<<53 - 1

// FoldError 是回放时发现耐久数据本身接不上。
//
// 源: packages/goal/goal/src/fold.ts（那些 throw new Error）
//
// 新增: DSH 那边这一层抛的是**裸** Error，不是 GoalError——因为 [ErrorCode] 那份
// 码表是给调用方分流一次拒收用的，而日志坏了不是调用方能改的事。Go 里给它一个
// 具名类型而不是 [errors.New]，是为了让不变量那一侧（[ValidateStream]）能断言
// 「本包只会因为这一种原因说不」，一条从下游漏上来的别的错就不会被当成日志坏了。
type FoldError struct {
	// Reason 是破掉的那条不变量，中文，给运维看。
	Reason string
}

// Error 让它成为一个 error。
func (e *FoldError) Error() string { return e.Reason }

// foldErrorf 造一条回放失败。
func foldErrorf(format string, args ...any) *FoldError {
	return &FoldError{Reason: fmt.Sprintf(format, args...)}
}

// FoldState 是回放过程中那个可变的累加器。
//
// 源: packages/goal/goal/src/fold.ts:26-34（GoalFoldState）
//
// 新增: DSH 的 createdAt / updatedAt 是 `number | undefined`，而且在几个地方写了
// 「有当前目标就一定有时刻」的断言（那几条它自己标了 v8 ignore，走不到）。Go 里
// 直接把这件事压进类型的用法里：这两个数只在 Goal 非 nil 时有意义，Goal 一旦被
// clear 掉就跟着归零。少两个永远为真的判断，也就少两段测不到的代码。
type FoldState struct {
	// Goal 是此刻的当前目标；nil 表示还没建过、或者已经被清掉。
	Goal *Snapshot
	// RoundsStarted 是当前目标已经准入过的最高轮号。
	RoundsStarted int
	// CreatedAt 是当前目标的建立时刻；Goal 为 nil 时无意义。
	CreatedAt int64
	// UpdatedAt 是当前目标最近一次改动的时刻；Goal 为 nil 时无意义。
	UpdatedAt int64
	// LastRef 是最近一次改动的修订身份，包括那块 clear 墓碑；nil 表示一次都没改过。
	LastRef *Ref
	// seenGoalIDs 是这一段里出现过的每一个目标 id，清掉的也算。
	//
	// 不导出：它只服务「一个 id 不许被建第二次」这一条，露出去只会诱使调用方去改它。
	// 需要整份状态时走 [FoldState.Clone]。
	seenGoalIDs map[ID]bool
}

// EmptyFoldState 造一个空的累加器。
//
// 源: packages/goal/goal/src/fold.ts:40-49
func EmptyFoldState() *FoldState { return &FoldState{} }

// Clone 复制一份互不相干的累加器。
//
// 源: packages/goal/goal/src/invariant.ts:17-26
//
// 不变量那一侧要在一条事件**落盘之前**先拿它试一遍：试失败了原来那份状态不能被
// 动过。指针字段（Goal、LastRef）在这里跟着复制值而不是共享，虽然本包只换指针、
// 从不穿过指针改内容——共享一个指针的那份安全建立在「以后也没人这么干」上，不值。
func (s *FoldState) Clone() *FoldState {
	clone := *s
	if s.Goal != nil {
		snapshot := s.Goal.detached()
		clone.Goal = &snapshot
	}
	if s.LastRef != nil {
		ref := *s.LastRef
		clone.LastRef = &ref
	}
	clone.seenGoalIDs = make(map[ID]bool, len(s.seenGoalIDs))
	for id := range s.seenGoalIDs {
		clone.seenGoalIDs[id] = true
	}
	return &clone
}

// Folded 是一次纯回放脱手交出来的结果。
//
// 源: packages/goal/goal/src/domain.ts:70-82（FoldedGoal）
//
// [Activation] 故意不在这里：它是进程局部的，一次都不落盘（理由见它自己的注释）。
type Folded struct {
	// Goal 是此刻的当前目标；nil 表示还没建过、或者已经被清掉。
	Goal *Snapshot
	// RoundsStarted 是当前目标已经准入过的最高轮号。
	RoundsStarted int
	// CreatedAt 是当前目标的建立时刻；Goal 为 nil 时无意义。
	CreatedAt int64
	// UpdatedAt 是当前目标最近一次改动的时刻；Goal 为 nil 时无意义。
	UpdatedAt int64
	// LastRef 是最近一次改动的修订身份，包括那块 clear 墓碑。
	LastRef *Ref
}

// Folded 把当前累加器脱手成一份结果。
func (s *FoldState) Folded() Folded {
	folded := Folded{
		RoundsStarted: s.RoundsStarted,
		CreatedAt:     s.CreatedAt,
		UpdatedAt:     s.UpdatedAt,
	}
	if s.Goal != nil {
		snapshot := s.Goal.detached()
		folded.Goal = &snapshot
	}
	if s.LastRef != nil {
		ref := *s.LastRef
		folded.LastRef = &ref
	}
	return folded
}

// ---- 严格解码 ----

// decodeObject 把一段耐久 JSON 读成一个对象，数组和 null 都不算对象。
//
// 源: packages/goal/goal/src/fold.ts:52-54 (isRecord)
func decodeObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, false
	}
	return object, true
}

// exactKeys 要求这个对象的键**恰好**是点名的那些，多一个少一个都不行。
//
// 源: packages/goal/goal/src/fold.ts（那几处 Object.keys(value).sort().join(',')）
func exactKeys(object map[string]json.RawMessage, expected ...string) bool {
	if len(object) != len(expected) {
		return false
	}
	for _, key := range expected {
		if _, present := object[key]; !present {
			return false
		}
	}
	return true
}

// decodeSafeInteger 读一个耐久整数，拒绝小数和超出安全整数范围的值。
func decodeSafeInteger(raw json.RawMessage) (int64, bool) {
	// 新增: Go 的 [encoding/json] 会把 `"2"` 这种**带引号**的写法也读进
	// [json.Number]，只要引号里装的是个合法数字。DSH 那边这一层是
	// `typeof value === "number"`，会当场拒掉。这些字节要和 DSH 互读，所以引号
	// 得在这里挡住——否则一条 Go 写下的 `"revision":"2"` 在对面读不回来。
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] == '"' {
		return 0, false
	}
	var number json.Number
	if err := json.Unmarshal(trimmed, &number); err != nil {
		return 0, false
	}
	value, err := number.Int64()
	if err != nil || value > maxSafeInteger || value < -maxSafeInteger {
		return 0, false
	}
	return value, true
}

// decodePositiveInteger 读一个正的安全整数。
//
// 源: packages/goal/goal/src/fold.ts:57-62
func decodePositiveInteger(raw json.RawMessage, field string) (int, error) {
	value, ok := decodeSafeInteger(raw)
	if !ok || value < 1 {
		return 0, foldErrorf("goal 改动的 %s 必须是一个正的安全整数", field)
	}
	return int(value), nil
}

// decodeNonNegativeInteger 读一个非负的安全整数。
//
// 源: packages/goal/goal/src/fold.ts:65-70
func decodeNonNegativeInteger(raw json.RawMessage, field string) (int64, error) {
	value, ok := decodeSafeInteger(raw)
	if !ok || value < 0 {
		return 0, foldErrorf("goal 改动的 %s 必须是一个非负的安全整数", field)
	}
	return value, nil
}

// decodeNormalizedString 读一个非空、且首尾没有空白的字符串。
func decodeNormalizedString(raw json.RawMessage) (string, bool) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	if value == "" || strings.TrimSpace(value) != value {
		return "", false
	}
	return value, true
}

// decodeBlockReason 读一份阻塞原因。
//
// 源: packages/goal/goal/src/fold.ts:73-85
func decodeBlockReason(raw json.RawMessage) (*BlockReason, error) {
	object, ok := decodeObject(raw)
	if !ok || !exactKeys(object, "code", "message") {
		return nil, foldErrorf("goal 改动的 goal.blockedReason 的键必须恰好是 code 和 message")
	}
	var code string
	if err := json.Unmarshal(object["code"], &code); err != nil || !blockCodePattern.MatchString(code) {
		return nil, foldErrorf("goal 改动的 goal.blockedReason.code 必须是 lower-kebab-case")
	}
	message, ok := decodeNormalizedString(object["message"])
	if !ok {
		return nil, foldErrorf("goal 改动的 goal.blockedReason.message 必须非空、而且已经去过首尾空白")
	}
	return &BlockReason{Code: code, Message: message}, nil
}

// decodeSnapshot 读一份快照，键集按阶段分两种，一个都不许多、一个都不许少。
//
// 源: packages/goal/goal/src/fold.ts:88-115
func decodeSnapshot(raw json.RawMessage) (Snapshot, error) {
	object, ok := decodeObject(raw)
	if !ok {
		return Snapshot{}, foldErrorf("goal 改动的 goal 必须是一个对象")
	}
	var id string
	if err := json.Unmarshal(object["id"], &id); err != nil || id == "" {
		return Snapshot{}, foldErrorf("goal 改动的 goal.id 必须是一个非空字符串")
	}
	objective, ok := decodeNormalizedString(object["objective"])
	if !ok {
		return Snapshot{}, foldErrorf("goal 改动的 goal.objective 必须非空、而且已经去过首尾空白")
	}
	var phase Phase
	if err := json.Unmarshal(object["phase"], &phase); err != nil || !phases[phase] {
		return Snapshot{}, foldErrorf("goal 改动的 goal.phase 不合规")
	}
	if phase == PhaseBlocked {
		if !exactKeys(object, "id", "revision", "objective", "phase", "maxGoalRounds", "blockedReason") {
			return Snapshot{}, foldErrorf("阶段是 blocked 的 goal 的键必须恰好是 blockedReason、id、maxGoalRounds、objective、phase、revision")
		}
	} else if !exactKeys(object, "id", "revision", "objective", "phase", "maxGoalRounds") {
		return Snapshot{}, foldErrorf("阶段是 %s 的 goal 的键必须恰好是 id、maxGoalRounds、objective、phase、revision", phase)
	}
	revision, err := decodePositiveInteger(object["revision"], "goal.revision")
	if err != nil {
		return Snapshot{}, err
	}
	maxGoalRounds, err := decodePositiveInteger(object["maxGoalRounds"], "goal.maxGoalRounds")
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{
		Ref:           Ref{ID: ID(id), Revision: revision},
		Objective:     objective,
		Phase:         phase,
		MaxGoalRounds: maxGoalRounds,
	}
	if phase == PhaseBlocked {
		reason, err := decodeBlockReason(object["blockedReason"])
		if err != nil {
			return Snapshot{}, err
		}
		snapshot.BlockedReason = reason
	}
	return snapshot, nil
}

// decodeRef 读一块 clear 墓碑的身份。
//
// 源: packages/goal/goal/src/fold.ts:118-126
func decodeRef(raw json.RawMessage) (Ref, error) {
	object, ok := decodeObject(raw)
	if !ok || !exactKeys(object, "id", "revision") {
		return Ref{}, foldErrorf("goal clear 的墓碑的键必须恰好是 id 和 revision")
	}
	var id string
	if err := json.Unmarshal(object["id"], &id); err != nil || id == "" {
		return Ref{}, foldErrorf("goal clear 的墓碑 id 必须是一个非空字符串")
	}
	revision, err := decodePositiveInteger(object["revision"], "cleared.revision")
	if err != nil {
		return Ref{}, err
	}
	return Ref{ID: ID(id), Revision: revision}, nil
}

// DecodeChange 严格读一份 v1 的 goal/change 负载。
//
// 源: packages/goal/goal/src/fold.ts:134-172
//
// 新增: DSH 那个 decodeGoalChange 有三种结局——不是 goal/change 就交回 undefined，
// 是但坏了就抛，好的就交回来。Go 这边合成两种：不是 goal/change 也当成一次失败。
// 两个调用点本来就把前两种当同一件事办（[ApplyProjection] 都是原样返回，
// [ApplyEvent] 只在事件类型已经是 goal/change 时才走到这里），多出来的那一路在
// Go 里只会变成一个永远为 nil 的返回值和一段测不到的分支。
func DecodeChange(raw json.RawMessage) (Change, error) {
	object, ok := decodeObject(raw)
	if !ok {
		return Change{}, foldErrorf("goal/change 的负载必须是一个对象")
	}
	var kind string
	if err := json.Unmarshal(object["kind"], &kind); err != nil || kind != string(EventChange) {
		return Change{}, foldErrorf("goal/change 的 kind 必须是 %q", EventChange)
	}
	version, versionOK := decodeSafeInteger(object["version"])
	if !versionOK || version != ChangeVersion {
		return Change{}, foldErrorf("认不得的 goal 改动版本号")
	}
	var operation Operation
	if rawOperation, present := object["operation"]; present {
		_ = json.Unmarshal(rawOperation, &operation)
	}
	if operation == OpClear {
		if !exactKeys(object, "kind", "version", "operation", "cleared", "clearedAt") {
			return Change{}, foldErrorf("goal clear 改动的键必须恰好是 cleared、clearedAt、kind、operation、version")
		}
		cleared, err := decodeRef(object["cleared"])
		if err != nil {
			return Change{}, err
		}
		clearedAt, err := decodeNonNegativeInteger(object["clearedAt"], "clearedAt")
		if err != nil {
			return Change{}, err
		}
		return Change{
			Version:   ChangeVersion,
			Operation: OpClear,
			Cleared:   cleared,
			ClearedAt: clearedAt,
		}, nil
	}
	if !snapshotOperations[operation] {
		return Change{}, foldErrorf("goal 改动的 operation 不合规")
	}
	if !exactKeys(object, "kind", "version", "operation", "goal", "roundsStarted", "createdAt", "updatedAt") {
		return Change{}, foldErrorf("goal 快照改动的键必须恰好是 createdAt、goal、kind、operation、roundsStarted、updatedAt、version")
	}
	createdAt, err := decodeNonNegativeInteger(object["createdAt"], "createdAt")
	if err != nil {
		return Change{}, err
	}
	updatedAt, err := decodeNonNegativeInteger(object["updatedAt"], "updatedAt")
	if err != nil {
		return Change{}, err
	}
	if updatedAt < createdAt {
		return Change{}, foldErrorf("goal 改动的 updatedAt 不能早于 createdAt")
	}
	snapshot, err := decodeSnapshot(object["goal"])
	if err != nil {
		return Change{}, err
	}
	roundsStarted, err := decodeNonNegativeInteger(object["roundsStarted"], "roundsStarted")
	if err != nil {
		return Change{}, err
	}
	return Change{
		Version:       ChangeVersion,
		Operation:     operation,
		Goal:          snapshot,
		RoundsStarted: int(roundsStarted),
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
	}, nil
}

// parseSourceStrict 把一个消息来源收窄成本层产出的那一种。
//
// 源: packages/goal/goal/src/fold.ts:175-183 (goalSource)
//
// 三种结局和 DSH 一样：不是本层产出的（false, nil）、是但坏了（false, 一条
// [FoldError]）、好的（true, nil）。[ParseSource] 是它对外那层宽松的壳——调用方
// 只想知道「这一轮是不是目标推的」，坏了和不是本层的对它是同一件事。
func parseSourceStrict(source llm.MessageSource) (Source, bool, error) {
	unknown, ok := source.(llm.UnknownSource)
	if !ok || unknown.Kind != llm.SourceKind(SourceKind) {
		return Source{}, false, nil
	}
	var parsed Source
	if err := json.Unmarshal(unknown.Raw, &parsed); err != nil {
		return Source{}, false, foldErrorf("goal 的消息来源读不回来：%v", err)
	}
	if parsed.GoalID == "" || parsed.Revision < 1 || parsed.Round < 1 {
		return Source{}, false, foldErrorf("goal 的消息来源不合规")
	}
	return parsed, true, nil
}

// ---- 跃迁校验 ----

// requireSameDefinition 要求两份快照保住只有 edit 才准换的那两个字段。
//
// 源: packages/goal/goal/src/fold.ts:186-190
func requireSameDefinition(current *Snapshot, next Snapshot, operation Operation) error {
	if next.Objective != current.Objective || next.MaxGoalRounds != current.MaxGoalRounds {
		return foldErrorf("goal %s 不许改 objective 或者 maxGoalRounds", operation)
	}
	return nil
}

// requireNextRevision 要求这一次恰好是当前目标的下一个修订。
//
// 源: packages/goal/goal/src/fold.ts:193-197
func requireNextRevision(current *Snapshot, next Ref, operation Operation) error {
	if next.ID != current.ID || next.Revision != current.Revision+1 {
		return foldErrorf("goal %s 必须把当前目标恰好推进一个修订", operation)
	}
	return nil
}

// sameBlockReason 问两份阻塞原因是不是同一份，两边都没有也算同一份。
func sameBlockReason(left, right *BlockReason) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

// validateSnapshotTransition 拿前一份投影验一次非 create 的快照改动。
//
// 源: packages/goal/goal/src/fold.ts:200-253
func validateSnapshotTransition(state *FoldState, change Change, current *Snapshot) error {
	next := change.Goal
	if err := requireNextRevision(current, next.Ref, change.Operation); err != nil {
		return err
	}
	if change.CreatedAt != state.CreatedAt || change.UpdatedAt < state.UpdatedAt || change.RoundsStarted != state.RoundsStarted {
		return foldErrorf("goal %s 没保住当前目标的计数和时刻", change.Operation)
	}
	switch change.Operation {
	case OpEdit:
		// edit 是唯一准换 objective / maxGoalRounds 的动词，代价是它一点都不许碰阶段。
		if next.Phase != current.Phase || !sameBlockReason(next.BlockedReason, current.BlockedReason) {
			return foldErrorf("goal edit 不许改阶段或者阻塞原因")
		}
	case OpPause:
		if err := requireSameDefinition(current, next, change.Operation); err != nil {
			return err
		}
		if current.Phase != PhaseActive || next.Phase != PhasePaused {
			return foldErrorf("goal pause 的阶段跃迁不成立")
		}
	case OpResume:
		if err := requireSameDefinition(current, next, change.Operation); err != nil {
			return err
		}
		resumable := current.Phase == PhaseActive || current.Phase == PhasePaused || current.Phase == PhaseBlocked
		if !resumable || next.Phase != PhaseActive || state.RoundsStarted >= next.MaxGoalRounds {
			return foldErrorf("goal resume 的阶段跃迁不成立，或者轮数预算已经用完")
		}
	case OpComplete:
		if err := requireSameDefinition(current, next, change.Operation); err != nil {
			return err
		}
		if current.Phase == PhaseComplete || next.Phase != PhaseComplete {
			return foldErrorf("goal complete 的阶段跃迁不成立")
		}
	case OpBlock:
		if err := requireSameDefinition(current, next, change.Operation); err != nil {
			return err
		}
		if current.Phase != PhaseActive || next.Phase != PhaseBlocked {
			return foldErrorf("goal block 的阶段跃迁不成立")
		}
	default:
		// [DecodeChange] 读出来的动词只可能是那七个，而 create 和 clear 在
		// [ApplyChange] 里已经分走了。走到这里说明调用方自己拼了一个 [Change]。
		return foldErrorf("认不得的 goal 快照动词 %q", change.Operation)
	}
	return nil
}

// ChangeRef 交回一次改动带着的那个修订身份。
//
// 源: packages/goal/goal/src/fold.ts:260-264
func ChangeRef(change Change) Ref {
	if change.Operation == OpClear {
		return change.Cleared
	}
	return change.Goal.Ref
}

// ApplyChange 验一次已解码的改动，然后把它应用到累加器上。
//
// 源: packages/goal/goal/src/fold.ts:271-306
func ApplyChange(state *FoldState, change Change) error {
	ref := ChangeRef(change)
	if change.Operation == OpClear {
		current := state.Goal
		if current == nil {
			return foldErrorf("goal clear 需要一个当前目标")
		}
		if err := requireNextRevision(current, change.Cleared, change.Operation); err != nil {
			return err
		}
		if change.ClearedAt < state.UpdatedAt {
			return foldErrorf("goal clear 的时刻不能早于当前目标那次改动")
		}
		state.Goal = nil
		state.RoundsStarted = 0
		state.CreatedAt = 0
		state.UpdatedAt = 0
		state.LastRef = &ref
		return nil
	}
	if change.Operation == OpCreate {
		if state.seenGoalIDs == nil {
			state.seenGoalIDs = map[ID]bool{}
		}
		// 「一个 id 不许被建第二次」跨越 clear：墓碑之后再拿同一个 id 建一遍，
		// 会让这一段日志里同一个身份对应两段互不相干的历史。
		if change.Goal.Revision != 1 || change.Goal.Phase != PhaseActive || change.RoundsStarted != 0 ||
			(state.Goal != nil && state.Goal.Phase != PhaseComplete) || state.seenGoalIDs[change.Goal.ID] {
			return foldErrorf("goal create 需要一个全新的、修订号为 1、阶段为 active、轮数为 0 的目标")
		}
		state.seenGoalIDs[change.Goal.ID] = true
	} else {
		current := state.Goal
		if current == nil {
			return foldErrorf("goal %s 需要一个当前目标", change.Operation)
		}
		if err := validateSnapshotTransition(state, change, current); err != nil {
			return err
		}
	}
	snapshot := change.Goal
	state.Goal = &snapshot
	state.RoundsStarted = change.RoundsStarted
	state.CreatedAt = change.CreatedAt
	state.UpdatedAt = change.UpdatedAt
	state.LastRef = &ref
	return nil
}

// ApplyEvent 把一条会话事件喂进这条严格回放。
//
// 源: packages/goal/goal/src/fold.ts:313-332
//
// 只有两种事件说得上话：本包自己的那条改动，以及一条**带着目标来源**的用户消息
// ——后者是「这一轮是目标推的」唯一的耐久证据，[FoldState.RoundsStarted] 只由它
// 往上推，普通的人类回合一次都不算。
func ApplyEvent(state *FoldState, event session.Event) error {
	switch event.Type {
	case EventChange:
		change, err := DecodeChange(event.Data)
		if err != nil {
			return err
		}
		return ApplyChange(state, change)
	case session.EventUserMessage:
		var data session.UserMessageData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return foldErrorf("会话事件 %d 的用户消息读不回来：%v", event.Seq, err)
		}
		source, ok, err := parseSourceStrict(data.Source)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		current := state.Goal
		if current == nil || current.Phase != PhaseActive || source.GoalID != current.ID ||
			source.Revision != current.Revision || source.Round != state.RoundsStarted+1 ||
			source.Round > current.MaxGoalRounds {
			return foldErrorf("会话事件 %d 那一轮不是当前 active 目标的下一个准入轮次", event.Seq)
		}
		state.RoundsStarted = source.Round
		return nil
	default:
		return nil
	}
}

// Fold 把一整段连续的会话日志折成此刻的目标状态。
//
// 源: packages/goal/goal/src/fold.ts:339-349
func Fold(events []session.Event) (Folded, error) {
	state := EmptyFoldState()
	for _, event := range events {
		if err := ApplyEvent(state, event); err != nil {
			return Folded{}, err
		}
	}
	return state.Folded(), nil
}
