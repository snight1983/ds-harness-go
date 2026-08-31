// 本文件的作用：这个包的那几种值——落进日志的快照与改动、给调用方看的视图、
// 那条标记「这一轮是目标推的」的消息来源，以及那份封闭的错误码表。
//
// 源: packages/goal/goal/src/types.ts、packages/goal/goal/src/domain.ts

package goal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"

	"ds-harness-go/llm"
	"ds-harness-go/session"
)

// marshalNoEscape 把一个值排成 JSON，**不**做 HTML 转义。
//
// 新增: [encoding/json.Marshal] 默认把 < > & 转成 < 这类写法，DSH 用的
// JSON.stringify 不转。目标的 objective 和 blockedReason.message 都是人写的自由
// 文本，里面出现尖括号一点都不稀奇；这份字节要落进会话日志并和 DSH 互读，多出来
// 的转义会让同一句话在两侧长得不一样。
//
// 和 [ds-harness-go/schedule.marshalNoEscape] 同一条理由：这件事必须落在最里面
// 那一层，外面那圈 Encoder 上的 SetEscapeHTML(false) 管不着自定义 MarshalJSON
// 已经排好的字节。
func marshalNoEscape(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/goal/goal/src/invariant.ts:9
const PackageName = "@deepseek-ai/dsh-goal"

// SourceKind 是这个包在消息来源上占的那个判别标签。
//
// 源: packages/goal/goal/src/domain.ts:47
const SourceKind = "goal"

// EventChange 是本包拥有的那一种会话事件：一次带版本号的目标改动。
//
// 源: packages/goal/goal/src/domain.ts:66
//
// 拥有它的意思是：只有本包写它，也只有本包解释它。每一条都带着**改完之后的完整
// 状态**——目标状态因此完全不依赖收件箱怎么排、谁认领了哪一条，会话日志是唯一
// 的耐久权威。
const EventChange session.EventType = "goal/change"

// EventTypes 是本包往会话词汇表里加的那些类型。
//
//	vocabulary := session.CoreVocabulary().With(goal.EventTypes()...)
func EventTypes() []session.EventType { return []session.EventType{EventChange} }

// ChangeVersion 是本包实现的那一版耐久协议。
//
// 源: packages/goal/goal/src/runtime.ts:8
const ChangeVersion = 1

// DefaultMaxGoalRounds 是部署方没给上限时用的那个默认轮数。
//
// 源: packages/goal/goal/src/index.ts:190
const DefaultMaxGoalRounds = 256

// ID 是一个目标跨它全部修订的身份。
//
// 源: packages/goal/goal/src/types.ts:16
//
// 新增: DSH 是一个 branded string 加一个 GoalId() 转型函数。Go 里就是一个具名
// 字符串类型——它挡住的东西一样（一个裸 string 传不进来），而且转换在编译期就
// 查得住，不需要那个函数。
type ID string

// Ref 是一次目标修订的 compare-and-set 身份。
//
// 源: packages/goal/goal/src/types.ts:19-25
//
// 每一次耐久改动都把 Revision 加一，所以拿着旧 Ref 的调用方会被当场挡回去，
// 而不是把别人刚写下的那次改动覆盖掉。
type Ref struct {
	// ID 是稳定的目标身份。
	ID ID `json:"id"`
	// Revision 是正的修订号，每一次耐久改动加一。
	Revision int `json:"revision"`
}

// CreateRequest 是一次建目标的入参，轮数上限不给就用部署方的默认值。
//
// 源: packages/goal/goal/src/types.ts:27-30
//
// 新增: DSH 的 `maxGoalRounds?: number` 在 Go 里是一个指针。用零值当「没给」不行：
// 0 本身是一个**非法**的上限（必须是正整数），而调用方显式传 0 该被当场拒掉，
// 不该被悄悄换成 256。
type CreateRequest struct {
	// Objective 是人提出的那个完成目标。
	Objective string `json:"objective"`
	// MaxGoalRounds 是准许的目标轮总数；nil 表示用部署方的默认值。
	MaxGoalRounds *int `json:"maxGoalRounds,omitempty"`
}

// CreateResult 是一次建目标在远端边界上的回执。
//
// 源: packages/goal/goal/src/types.ts:33-35
type CreateResult struct {
	// Ref 是刚建出来那个目标的身份。
	Ref Ref `json:"ref"`
}

// EditRequest 是一次 edit 要换掉的那几个字段，至少得给一个。
//
// 源: packages/goal/goal/src/types.ts:38-41
//
// 两个都是指针，理由同 [CreateRequest.MaxGoalRounds]：「不换」和「换成一个非法值」
// 必须分得开，否则一次只想改轮数的调用会把目标描述清空。
type EditRequest struct {
	// Objective 是新的目标描述；nil 表示不换。
	Objective *string `json:"objective,omitempty"`
	// MaxGoalRounds 是新的轮数上限；nil 表示不换。
	MaxGoalRounds *int `json:"maxGoalRounds,omitempty"`
}

// Phase 是一个目标的耐久生命周期阶段。
//
// 源: packages/goal/goal/src/types.ts:44-48
//
// 「这个进程能不能自动往下推」是另一条轴（[Activation]），故意不落进这里，
// 也永远不落盘。
type Phase string

const (
	// PhaseActive 表示这个目标还在推进。
	PhaseActive Phase = "active"
	// PhasePaused 表示有人主动把它停下了。
	PhasePaused Phase = "paused"
	// PhaseBlocked 表示它撞上了一堵墙，[Snapshot.BlockedReason] 说是哪一堵。
	PhaseBlocked Phase = "blocked"
	// PhaseComplete 表示它已经完成。
	PhaseComplete Phase = "complete"
)

// blockCodePattern 是阻塞码那份 lower-kebab-case 的写法。
//
// 源: packages/goal/goal/src/fold.ts:76
var blockCodePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)

// BlockReason 是一次阻塞的机器可路由分类加一句给人看的解释。
//
// 源: packages/goal/goal/src/types.ts:51-56
//
// 提供方限额、配置预算、执行错误、需要人来拍板——这几件事共用 [PhaseBlocked]
// 这一个耐久阶段，靠 Code 分开，而不是各自再立一个生命周期状态。
type BlockReason struct {
	// Code 是阻塞方自己选的稳定分类，lower-kebab-case。
	Code string `json:"code"`
	// Message 是给人和模型看的非空解释。
	Message string `json:"message"`
}

// Snapshot 是每一次非 clear 改动写下的完整耐久状态。
//
// 源: packages/goal/goal/src/types.ts:59-69
type Snapshot struct {
	Ref
	// Objective 是人提出的那个完成目标。
	Objective string
	// Phase 是耐久生命周期阶段。
	Phase Phase
	// BlockedReason 恰好在 Phase 是 [PhaseBlocked] 时非 nil。
	//
	// 新增: DSH 是一个可选字段，「不在」和「在但为 null」在 TS 里分得开。
	// Go 用指针表达同一件事，而且落到线上时不带 omitempty 以外的花样：
	// 不是 blocked 就整个键不出现，和 DSH 逐字一致。
	BlockedReason *BlockReason
	// MaxGoalRounds 是准许的目标轮总数。
	MaxGoalRounds int
}

// detached 复制一份和原件不共享任何可写内存的快照。
//
// 新增: DSH 那边 `{...goal}` 也只是浅拷，blockedReason 那个对象仍旧共享。Go 这边
// 补上这一趟，是因为 [Snapshot.BlockedReason] 是一个**导出的**指针字段：一份交给
// 调用方的视图如果和缓存共享它，调用方穿过指针写一个字，就把这个进程里的目标状态
// 改掉了，而且日志里一点痕迹都没有。
func (s Snapshot) detached() Snapshot {
	if s.BlockedReason == nil {
		return s
	}
	reason := *s.BlockedReason
	s.BlockedReason = &reason
	return s
}

// snapshotJSON 是 [Snapshot] 落到线上的形状，字段名和 DSH 逐字相同。
type snapshotJSON struct {
	ID            ID           `json:"id"`
	Revision      int          `json:"revision"`
	Objective     string       `json:"objective"`
	Phase         Phase        `json:"phase"`
	MaxGoalRounds int          `json:"maxGoalRounds"`
	BlockedReason *BlockReason `json:"blockedReason,omitempty"`
}

// MarshalJSON 把一份快照排成 DSH 那份形状。
func (s Snapshot) MarshalJSON() ([]byte, error) {
	if s.Phase == PhaseBlocked && s.BlockedReason == nil {
		return nil, fmt.Errorf("goal: 阻塞的快照 %q 没带阻塞原因", s.ID)
	}
	if s.Phase != PhaseBlocked && s.BlockedReason != nil {
		return nil, fmt.Errorf("goal: 阶段是 %q 的快照 %q 却带着阻塞原因", s.Phase, s.ID)
	}
	return marshalNoEscape(snapshotJSON{
		ID:            s.ID,
		Revision:      s.Revision,
		Objective:     s.Objective,
		Phase:         s.Phase,
		MaxGoalRounds: s.MaxGoalRounds,
		BlockedReason: s.BlockedReason,
	})
}

// UnmarshalJSON 把一份快照读回来。
//
// 新增: DSH 那边没有对应物——TS 的对象本身就是介质形状，读回来不需要转一道。Go
// 这边 [Snapshot] 的字段名和线上的键对不齐（Ref 是内嵌的，别的字段一个标签都没有），
// 少了这一趟就只能靠 [encoding/json] 那套大小写不敏感的名字匹配去碰，改一个字段名
// 就会悄悄读不回来。落盘的投影检查点走的正是这条路（见 decodeProjectionState）。
//
// 这里**不**验业务规则（阶段合不合法、blockedReason 在不在），那是
// [decodeSnapshot] 的活儿：这一趟只负责把形状换过来。
func (s *Snapshot) UnmarshalJSON(data []byte) error {
	var wire snapshotJSON
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("goal: 快照不是一个 JSON 对象：%w", err)
	}
	*s = Snapshot{
		Ref:           Ref{ID: wire.ID, Revision: wire.Revision},
		Objective:     wire.Objective,
		Phase:         wire.Phase,
		BlockedReason: wire.BlockedReason,
		MaxGoalRounds: wire.MaxGoalRounds,
	}
	return nil
}

// Activation 是「这个活着的进程可不可以自动接着推一个 active 的目标」。
//
// 源: packages/goal/goal/src/types.ts:71
//
// 它**从不落盘**：一份新的缓存、每一次 agent/session-start 边沿，都会把它打回
// [Disarmed]，哪怕回放出来的耐久阶段是 active。续会话、分叉、换驱动因此都保留
// 目标本身、阶段、修订号和已用轮数，但一次都不会自己动起来——要动起来必须有人
// 显式地再 resume 一次。
type Activation string

const (
	// Armed 表示这个进程此刻可以自动往下推。
	Armed Activation = "armed"
	// Disarmed 表示不可以。
	Disarmed Activation = "disarmed"
)

// View 是当前目标的一份脱手投影：耐久快照加上从日志里算出来的那几个量。
//
// 源: packages/goal/goal/src/types.ts:74-89
type View struct {
	Snapshot
	// RoundsStarted 是这个目标已经准入过的最高轮号。
	RoundsStarted int
	// CreatedAt 是那次 create 改动的毫秒时刻。
	CreatedAt int64
	// UpdatedAt 是最近一次改动的毫秒时刻。
	UpdatedAt int64
	// Activation 是进程局部的续推资格，从不落盘。
	Activation Activation
}

// Projection 是 `goal` 这个投影键的取值：当前的耐久目标加它那几个回放量。
//
// 源: packages/goal/goal/src/types.ts:91-100
//
// [Activation] 是进程局部的，故意不在这里：这份投影只反映耐久阶段。
type Projection struct {
	// Goal 是当前的耐久快照，改动要用的 CAS ref 就骑在它上面。
	Goal Snapshot `json:"goal"`
	// RoundsStarted 是这个目标已经准入过的最高轮号。
	RoundsStarted int `json:"roundsStarted"`
	// CreatedAt 是那次 create 改动的毫秒时刻。
	CreatedAt int64 `json:"createdAt"`
	// UpdatedAt 是最近一次改动的毫秒时刻。
	UpdatedAt int64 `json:"updatedAt"`
}

// Operation 是记进耐久改动的那几个动词。
//
// 源: packages/goal/goal/src/domain.ts:14-21
type Operation string

const (
	// OpCreate 建一个全新的 active 目标。
	OpCreate Operation = "create"
	// OpEdit 只换目标描述和轮数上限，不动阶段。
	OpEdit Operation = "edit"
	// OpPause 把一个 active 的目标停下。
	OpPause Operation = "pause"
	// OpResume 把一个停住的目标重新推起来。
	OpResume Operation = "resume"
	// OpComplete 把当前目标标记为完成。
	OpComplete Operation = "complete"
	// OpBlock 把一个 active 的目标标记为撞墙。
	OpBlock Operation = "block"
	// OpClear 清掉当前目标，留一块带修订号的墓碑。
	OpClear Operation = "clear"
)

// Change 是本包写进 [EventChange] 的那份耐久改动。
//
// 源: packages/goal/goal/src/domain.ts:24-44
//
// 新增: DSH 那边这是 GoalSnapshotChangeMeta | GoalClearChangeMeta 两支联合。
// Go 里合成一个带 Operation 判别的结构体（成例见
// [ds-harness-go/schedule.Change]）：排字节时按 Operation 分支，读回来时先看
// Operation 再验那一支该有的键。
//
// Operation 是 [OpClear] 时用 Cleared / ClearedAt，别的时候用 Goal /
// RoundsStarted / CreatedAt / UpdatedAt——两组字段互斥，填错那一组会在
// [Change.MarshalJSON] 当场报错，不会排出一份读不回来的字节。
type Change struct {
	// Version 是耐久协议版本，必须是 [ChangeVersion]。
	Version int
	// Operation 是这次改动的判别。
	Operation Operation

	// Goal 是改完之后的完整快照；[OpClear] 之外的每一支都带。
	Goal Snapshot
	// RoundsStarted 是改完之后已准入的最高轮号。
	RoundsStarted int
	// CreatedAt 是当前目标的建立时刻。
	CreatedAt int64
	// UpdatedAt 是这次改动的时刻。
	UpdatedAt int64

	// Cleared 是被清掉的那次修订的墓碑 ref，只有 [OpClear] 带。
	Cleared Ref
	// ClearedAt 是那次清除的时刻，只有 [OpClear] 带。
	ClearedAt int64
}

// snapshotChangeJSON 是非 clear 那一支落到线上的形状。
//
// 源: packages/goal/goal/src/domain.ts:24-32
type snapshotChangeJSON struct {
	Kind          string    `json:"kind"`
	Version       int       `json:"version"`
	Operation     Operation `json:"operation"`
	Goal          Snapshot  `json:"goal"`
	RoundsStarted int       `json:"roundsStarted"`
	CreatedAt     int64     `json:"createdAt"`
	UpdatedAt     int64     `json:"updatedAt"`
}

// clearChangeJSON 是墓碑那一支落到线上的形状。
//
// 源: packages/goal/goal/src/domain.ts:35-41
type clearChangeJSON struct {
	Kind      string    `json:"kind"`
	Version   int       `json:"version"`
	Operation Operation `json:"operation"`
	Cleared   Ref       `json:"cleared"`
	ClearedAt int64     `json:"clearedAt"`
}

// MarshalJSON 把一次改动排成 DSH 那份形状。
//
// 认不得的 Operation 当场报错而不是排出一份形状对不上的字节：[Change] 是导出的，
// 调用方硬填一个别的判别做得到，而那份字节会落进日志、在下一次回放时才炸。
func (c Change) MarshalJSON() ([]byte, error) {
	if c.Operation == OpClear {
		return marshalNoEscape(clearChangeJSON{
			Kind:      string(EventChange),
			Version:   c.Version,
			Operation: OpClear,
			Cleared:   c.Cleared,
			ClearedAt: c.ClearedAt,
		})
	}
	if !snapshotOperations[c.Operation] {
		return nil, fmt.Errorf("goal: 认不得的改动动词 %q", c.Operation)
	}
	return marshalNoEscape(snapshotChangeJSON{
		Kind:          string(EventChange),
		Version:       c.Version,
		Operation:     c.Operation,
		Goal:          c.Goal,
		RoundsStarted: c.RoundsStarted,
		CreatedAt:     c.CreatedAt,
		UpdatedAt:     c.UpdatedAt,
	})
}

// snapshotOperations 是那六个带完整快照的动词。
//
// 源: packages/goal/goal/src/fold.ts:16-23
var snapshotOperations = map[Operation]bool{
	OpCreate:   true,
	OpEdit:     true,
	OpPause:    true,
	OpResume:   true,
	OpComplete: true,
	OpBlock:    true,
}

// phases 是那四个合法阶段。
//
// 源: packages/goal/goal/src/fold.ts:24
var phases = map[Phase]bool{
	PhaseActive:   true,
	PhasePaused:   true,
	PhaseBlocked:  true,
	PhaseComplete: true,
}

// Changed 是一次耐久改动提交之后发出去的通知。
//
// 源: packages/goal/goal/src/domain.ts:85-90
type Changed struct {
	// Operation 是这次改动的动词。
	Operation Operation
	// Ref 是这次改动的修订身份；[OpClear] 时是那块墓碑。
	Ref Ref
	// Goal 是改完之后的当前视图；[OpClear] 时为 nil。
	Goal *View
}

// Source 是一条准入续推轮次的消息来源。
//
// 源: packages/goal/goal/src/domain.ts:47-54
//
// 新增: DSH 靠 declare module 把 'goal' 挂进 llm 的 MessageSourceMap。Go 的
// [llm.MessageSource] 是封闭接口（理由见 llm 的包文档），插件挂不进去。这里给出
// 一个普通结构体加它的 JSON 编解码，再靠 [llm.UnknownSource] 那个留出来的口子
// 原样携带它（成例见 [ds-harness-go/context/sessionref.Source]）。
//
// 只有带着这份来源的 user/message 才会把 [View.RoundsStarted] 往上推：普通的
// 人类回合一次都不算。
type Source struct {
	// GoalID 是这一轮属于哪个目标。
	GoalID ID
	// Revision 是发这一轮时那个目标的修订号。
	Revision int
	// Round 是这次准入的轮号，从 1 起。
	Round int
}

// sourceJSON 是 [Source] 落到线上的形状，字段名和 DSH 逐字相同。
type sourceJSON struct {
	Kind     string `json:"kind"`
	GoalID   ID     `json:"goalId"`
	Revision int    `json:"revision"`
	Round    int    `json:"round"`
}

// MarshalJSON 把这份来源排成 DSH 那份形状。
func (s Source) MarshalJSON() ([]byte, error) {
	return marshalNoEscape(sourceJSON{
		Kind:     SourceKind,
		GoalID:   s.GoalID,
		Revision: s.Revision,
		Round:    s.Round,
	})
}

// UnmarshalJSON 把一份来源读回来。
func (s *Source) UnmarshalJSON(data []byte) error {
	var wire sourceJSON
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("goal: 来源不是一个 JSON 对象：%w", err)
	}
	if wire.Kind != SourceKind {
		return fmt.Errorf("goal: 来源的 kind 是 %q，不是 %q", wire.Kind, SourceKind)
	}
	*s = Source{GoalID: wire.GoalID, Revision: wire.Revision, Round: wire.Round}
	return nil
}

// MessageSource 把这份来源包成一个 [llm.MessageSource]。
func (s Source) MessageSource() (llm.MessageSource, error) {
	encoded, err := s.MarshalJSON()
	if err != nil {
		// [Source] 里只有字符串和整数，编码不可能失败。留着这条是因为吞掉它会让
		// 一次排不出去悄悄变成一份空来源，而那条轮次就再也算不进 RoundsStarted。
		return nil, fmt.Errorf("goal: 编码来源失败：%w", err)
	}
	return llm.UnknownSource{Kind: llm.SourceKind(SourceKind), Raw: encoded}, nil
}

// ParseSource 从一个消息来源里把这份状态读回来；不是本层产出的就返回 false。
//
// 「不是本层产出的」和「是但坏了」在这里是同一件事：调用方问的只是「这一轮是不是
// 目标推的」，两种情况下的答案都是不是。要把它们分开的只有严格回放那一侧，走
// [parseSourceStrict]。
func ParseSource(source llm.MessageSource) (Source, bool) {
	parsed, ok, err := parseSourceStrict(source)
	if err != nil {
		return Source{}, false
	}
	return parsed, ok
}

// ErrorCode 是本包拒收一次读或一次改动时给出的稳定码。
//
// 源: packages/goal/goal/src/domain.ts:93-102
type ErrorCode string

const (
	// CodeAgentNotLive 表示交进来的那个 agent 不是注册表里活着的那一个实例。
	CodeAgentNotLive ErrorCode = "GOAL_AGENT_NOT_LIVE"
	// CodeNotFound 表示此刻没有当前目标。
	CodeNotFound ErrorCode = "GOAL_NOT_FOUND"
	// CodeAlreadyExists 表示已经有一个没完成的当前目标。
	CodeAlreadyExists ErrorCode = "GOAL_ALREADY_EXISTS"
	// CodeStaleRevision 表示交进来的 ref 已经过时。
	CodeStaleRevision ErrorCode = "GOAL_STALE_REVISION"
	// CodeInvalidObjective 表示目标描述是空的。
	CodeInvalidObjective ErrorCode = "GOAL_INVALID_OBJECTIVE"
	// CodeInvalidMaxRounds 表示轮数上限不是一个正整数。
	CodeInvalidMaxRounds ErrorCode = "GOAL_INVALID_MAX_ROUNDS"
	// CodeInvalidBlockReason 表示阻塞原因的码或者解释不合规。
	CodeInvalidBlockReason ErrorCode = "GOAL_INVALID_BLOCK_REASON"
	// CodeInvalidEdit 表示一次 edit 一个字段都没给。
	CodeInvalidEdit ErrorCode = "GOAL_INVALID_EDIT"
	// CodeInvalidTransition 表示这次跃迁在当前阶段上不成立。
	CodeInvalidTransition ErrorCode = "GOAL_INVALID_TRANSITION"
)

// Error 是本包边界上交回的那种失败。
//
// 源: packages/goal/goal/src/runtime.ts:20-30
//
// 新增: DSH 那个 GoalError 继承 HarnessError，只为把 code 收窄到
// [ErrorCode]。Go 里直接就是一个带具名码字段的错误类型，收窄由编译器管。
type Error struct {
	// Code 是那份封闭码表里的位置，调用方按它分流。
	Code ErrorCode
	// Message 是给人看的拒收理由。
	Message string
}

// Error 实现 error。
func (e *Error) Error() string { return e.Message }

// newError 造一条本包的失败。
func newError(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}
