// 本文件的作用：本包对外交出去的那些记录，以及调用方传进来的那些谓词。
//
// 源: packages/session-query/session-query/src/types.ts
// 源: packages/session-query/session-query/src/cursor.ts

package sessionquery

import (
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// EventSurface 说的是一条事件此刻在表面上的位置。
//
// 源: packages/session-query/session-query/src/types.ts:20-21（SessionEventSurface）
type EventSurface string

const (
	// SurfaceCurrent 是模型现在还看得见这条事件。
	SurfaceCurrent EventSurface = "current"
	// SurfaceShadowed 是这条事件曾经在表面上，被某次替换盖掉了。
	SurfaceShadowed EventSurface = "shadowed"
	// SurfaceLogOnly 是这条事件压根不上表面（回合边界、流式分块、请求信封……）。
	SurfaceLogOnly EventSurface = "log-only"
)

// SearchCursor 是检索后端自己签发的、对本包不透明的续页令牌。
//
// 源: packages/session-query/session-query/src/cursor.ts:6-15
//
// 新增: DSH 那边是 Branded<'SessionSearchCursor'>，配一个恒等构造函数
// 给品牌类型技巧当入口。Go 的具名类型天生就是标称类型，
// `sessionquery.SearchCursor(s)` 就是那个构造，编译期不可互换、运行期零成本，
// 所以那个函数在这里不存在。
//
// 空串表示没有下一页。
type SearchCursor string

// Record 是一个逻辑会话的轻量身份，外加它此刻在哪儿有。
//
// 源: packages/session-query/session-query/src/types.ts:23-31（SessionRecord）
type Record struct {
	// Header 是从「活的优先」那份观察里选出来的会话头，已经脱离。
	Header sessionlog.SessionHeader
	// Live 表示这个 id 此刻在活会话表里。
	Live bool
	// Persisted 表示这个 id 此刻在持久化后端里落着。
	Persisted bool
}

// SurfaceSnapshot 是一次「活的优先」观察里，某个会话当前的模型表面。
//
// 源: packages/session-query/session-query/src/types.ts:33-41（SessionSurfaceSnapshot）
type SurfaceSnapshot struct {
	// Session 是和 Events 出自同一次观察的会话头。
	Session sessionlog.SessionHeader
	// CapturedThroughSeq 是这次观察吃进的最大原始日志 seq。
	//
	// 新增: DSH 是 `number | null`，空日志给 null。Go 这边配一个
	// CapturedAny 布尔——seq 0 是一条真事件的合法序号，拿 0 当「没有」会撞车。
	CapturedThroughSeq int
	// CapturedAny 为假表示这次观察到的是一份空日志，CapturedThroughSeq 无意义。
	CapturedAny bool
	// Events 是按模型可见顺序排好的当前表面事件，已经脱离。
	Events []sessionlog.Event
}

// LogSnapshot 是一次脱离的、验过的完整原始日志观察。
//
// 源: packages/session-query/session-query/src/types.ts:43-49（SessionLogSnapshot）
type LogSnapshot struct {
	// Session 是和 Events 出自同一次观察的会话头。
	Session sessionlog.SessionHeader
	// Events 是修复并重放校验之后，连续的原始事件，已经脱离。
	Events []sessionlog.Event
}

// EventRecord 是一个逻辑会话里某条事件的轻量元数据。
//
// 源: packages/session-query/session-query/src/types.ts:51-63（SessionEventRecord）
type EventRecord struct {
	// SessionID 是拥有这条事件的会话。
	SessionID sessionlog.SessionID
	// Seq 是会话内单调递增的序号。
	Seq int
	// Type 是这条事件的类型。
	Type sessionlog.EventType
	// Time 是 Unix 纪元毫秒。
	Time int64
	// Surface 是它在折好的表面里的位置。
	Surface EventSurface
}

// LineageNode 是一棵血统树上的一个后代节点。
//
// 源: packages/session-query/session-query/src/types.ts:65-71（SessionLineageNode）
type LineageNode struct {
	// Session 是这个后代脱离出来的记录。
	Session Record
	// Descendants 是它自己的直接孩子，各自又带着自己的后代。
	Descendants []LineageNode
}

// LineageTrace 是一个逻辑会话已知的祖先与后代。
//
// 源: packages/session-query/session-query/src/types.ts:73-94（SessionLineageTrace）
//
// 新增: DSH 是一个判别联合——`{complete:true; root} | {complete:false;
// unresolvedParentId}`。Go 的结构体没有这个表达法，所以用一个 Complete 布尔
// 加两个只在对应分支有效的字段。哪个字段在哪个分支有效，写在各自的注释里。
type LineageTrace struct {
	// Target 是被追溯的那个会话。
	Target Record
	// Ancestors 是已知的父链，从直接父亲往外排。
	Ancestors []Record
	// Descendants 是以 Target 的直接孩子为根的、完整的已知后代树。
	Descendants []LineageNode
	// Complete 为真表示整条父链都在这份逻辑语料里。
	Complete bool
	// Root 是这条完整血统最上面的那个；只在 Complete 为真时有效。
	Root Record
	// UnresolvedParentID 是第一个不在逻辑语料里的父 id；只在 Complete 为假时有效。
	UnresolvedParentID sessionlog.SessionID
}

// EventTraceRequest 是一次事件关系追溯的请求。
//
// 源: packages/session-query/session-query/src/types.ts:96-102（SessionEventTraceRequest）
type EventTraceRequest struct {
	// SessionID 是拥有目标事件的会话。
	SessionID sessionlog.SessionID
	// Seq 是目标事件的序号。
	Seq int
}

// EventTrace 是一条事件的直接表面替换关系，加上它和被引用来源事件的关系。
//
// 源: packages/session-query/session-query/src/types.ts:104-118（SessionEventTrace）
type EventTrace struct {
	// Target 是目标的轻量记录。
	Target EventRecord
	// ReplacedBy 是直接盖掉目标的那条事件的 seq；只在 Shadowed 为真时有效。
	ReplacedBy int
	// Shadowed 为假表示目标没有被盖掉，ReplacedBy 无意义。
	//
	// 新增: DSH 是可选字段 `replacedBy?: number`。seq 0 合法，所以这里
	// 必须另外说一句「有没有」，理由同 [SurfaceSnapshot.CapturedAny]。
	Shadowed bool
	// ReplacementChain 是从直接替换者一路到最终替换者的那串 seq。
	ReplacementChain []int
	// ReplacedEventSeqs 是目标自己执行替换时移走的那些表面节点。
	ReplacedEventSeqs []int
	// SourceEventSeqs 是目标直接引用的、更早那些事件，按记录顺序。
	SourceEventSeqs []int
	// DerivedEventSeqs 是更晚的、直接引用目标当来源的那些事件，按日志顺序。
	DerivedEventSeqs []int
}

// EventTraceObservation 把一次事件追溯绑在同一次会话头观察上。
//
// 源: packages/session-query/session-query/src/types.ts:120-124（SessionEventTraceObservation）
type EventTraceObservation struct {
	EventTrace
	// Session 是和这次追溯所用日志一起选出来的会话头。
	Session sessionlog.SessionHeader
}

// EventReadRequest 是一次「读一条事件外加一圈原始上下文」的请求。
//
// 源: packages/session-query/session-query/src/types.ts:126-136（SessionEventReadRequest）
type EventReadRequest struct {
	// SessionID 是拥有目标事件的会话。
	SessionID sessionlog.SessionID
	// Seq 是目标事件的序号。
	Seq int
	// Before 是往前带几条原始事件。
	Before int
	// After 是往后带几条原始事件。
	After int
}

// EventWindow 是一条完整的目标事件，加上一段有界的原始日志窗口。
//
// 源: packages/session-query/session-query/src/types.ts:138-150（SessionEventWindow）
type EventWindow struct {
	// Session 是这次「活的优先」读取所用的会话头。
	Session sessionlog.SessionHeader
	// Target 是完整的目标事件，已经脱离。
	Target sessionlog.Event
	// Events 是 StartSeq 到 EndSeq 之间的完整事件，已经脱离。
	Events []sessionlog.Event
	// StartSeq 是 Events 里的第一个 seq。
	StartSeq int
	// EndSeq 是 Events 里的最后一个 seq。
	EndSeq int
}

// Range 是时间与序号过滤器用的闭区间。
//
// 源: packages/session-query/session-query/src/types.ts:179-185（SessionResultRange）
//
// 新增: 上下界用指针，nil 表示这一侧不设限——0 是合法的 seq、也是合法的
// 纪元毫秒，拿零值当「没给」会撞车。DSH 那边还要验 Number.isFinite，
// 那是因为它的 number 装得下 NaN 和 Infinity；int64 装不下，那道检查在这里消失了。
type Range struct {
	// From 是闭区间下界；nil 表示不设下界。
	From *int64
	// To 是闭区间上界；nil 表示不设上界。
	To *int64
}

// Availability 是逻辑会话过滤器认识的来源谓词。
//
// 源: packages/session-query/session-query/src/types.ts:187-188（SessionAvailability）
type Availability string

const (
	// AvailabilityLive 是「此刻在活会话表里」。
	AvailabilityLive Availability = "live"
	// AvailabilityPersisted 是「此刻在持久化后端里落着」。
	AvailabilityPersisted Availability = "persisted"
)

// SessionFilter 是一条逻辑会话谓词。
//
// 源: packages/session-query/session-query/src/types.ts:190-199（SessionResultFilter）
//
// 一组过滤器之间是**与**，一条过滤器内部的 Values 之间是**或**。
//
// 新增: DSH 是一个在 kind 上判别的联合。Go 用「接口 + 未导出的封印方法」把变体
// 封在包内：过滤器是本包对外契约的一部分，外面自造一个变体没有任何意义，
// 而封住之后 [MaterializeSessionFilters] 里那一大堆运行期类型检查全部消失。
type SessionFilter interface {
	sealedSessionFilter()
}

// IDFilter 只留 id 在 Values 里的那些会话。
type IDFilter struct {
	Values []sessionlog.SessionID
}

func (IDFilter) sealedSessionFilter() {}

// WorkspaceFilter 只留归属工作区在 Values 里的那些会话。
//
// 新增: DSH 那一列是 `(string | null)[]`，null 配的是 `header.cwd ?? null`。
// Go 的 [sessionlog.SessionHeader.WorkspaceID] 用空串表示没有，所以这里空串就是
// 那个 null。名字跟着字段从 `cwd` 改过来：这一列装的是工作区标识，不是路径，
// 理由见 [sessionlog.SessionHeader.WorkspaceID]。
type WorkspaceFilter struct {
	Values []sessionlog.WorkspaceID
}

func (WorkspaceFilter) sealedSessionFilter() {}

// CreatedAtFilter 只留建会话时间落在区间里的那些会话。
type CreatedAtFilter struct {
	Range
}

func (CreatedAtFilter) sealedSessionFilter() {}

// ParentFilter 只留父会话在 Values 里的那些会话。空串表示「没有父会话」。
type ParentFilter struct {
	Values []sessionlog.SessionID
}

func (ParentFilter) sealedSessionFilter() {}

// AvailabilityFilter 只留在指定来源里出现过的那些会话。
type AvailabilityFilter struct {
	Values []Availability
}

func (AvailabilityFilter) sealedSessionFilter() {}

// EventFilter 是一条事件谓词。
//
// 源: packages/session-query/session-query/src/types.ts:201-210（SessionEventResultFilter）
//
// 一组过滤器之间是**与**，一条过滤器内部的 Values 之间是**或**。
type EventFilter interface {
	sealedEventFilter()
}

// EventMetadataFilter 是检索后端在排序之前就能用上的那些事件谓词。
//
// 源: packages/session-query/session-query/src/types.ts:212-213（SessionEventMetadataFilter）
//
// 新增: DSH 是 `Exclude<SessionEventResultFilter, { kind: 'text' }>`——从联合里
// 减掉一个成员。Go 没有类型减法，所以反过来做：四个元数据变体多实现一个封印
// 方法，[TextFilter] 不实现。于是「文本过滤器传不进只收元数据的地方」
// 这件事一样是编译期挡住的。
type EventMetadataFilter interface {
	EventFilter
	sealedEventMetadataFilter()
}

// SeqFilter 只留序号落在区间里的那些事件。
type SeqFilter struct {
	Range
}

func (SeqFilter) sealedEventFilter()         {}
func (SeqFilter) sealedEventMetadataFilter() {}

// TimeFilter 只留时间戳落在区间里的那些事件。
type TimeFilter struct {
	Range
}

func (TimeFilter) sealedEventFilter()         {}
func (TimeFilter) sealedEventMetadataFilter() {}

// TypeFilter 只留类型在 Values 里的那些事件。
type TypeFilter struct {
	Values []sessionlog.EventType
}

func (TypeFilter) sealedEventFilter()         {}
func (TypeFilter) sealedEventMetadataFilter() {}

// SurfaceFilter 只留表面位置在 Values 里的那些事件。
type SurfaceFilter struct {
	Values []EventSurface
}

func (SurfaceFilter) sealedEventFilter()         {}
func (SurfaceFilter) sealedEventMetadataFilter() {}

// TextFilter 是一次字面的、忽略大小写、空白宽松的语义文字扫描。
//
// 它**不是**元数据过滤器：检索后端拿它没用——排序之前那一步只认元数据。
type TextFilter struct {
	Text string
}

func (TextFilter) sealedEventFilter() {}

// EventSearchDocument 是从一条事件里提出来的、可检索的语义文档。
//
// 源: packages/session-query/session-query/src/types.ts:215-219（SessionEventSearchDocument）
type EventSearchDocument struct {
	EventRecord
	// Text 是这条事件的第一方语义文字，扫描过滤和全文索引用的都是它。
	Text string
}

// SearchPage 是一页游标分页结果。
//
// 源: packages/session-query/session-query/src/types.ts:221-227（SessionSearchPage）
type SearchPage[T any] struct {
	// Items 是这一页的结果，顺序由契约定义。
	Items []T
	// NextCursor 是续页令牌；空串表示这是最后一页。
	NextCursor SearchCursor
}

// EventSearchPage 把一页事件检索结果绑在被索引的那次目标会话观察上。
//
// 源: packages/session-query/session-query/src/types.ts:229-233（SessionEventSearchPage）
type EventSearchPage struct {
	SearchPage[EventSearchHit]
	// Session 是和 Items 出自同一个索引世代的目标会话头。
	Session sessionlog.SessionHeader
}

// SearchRequest 是一次跨会话全文检索请求。
//
// 源: packages/session-query/session-query/src/types.ts:241-253（SessionSearchRequest）
type SearchRequest struct {
	// Query 是全文查询串，一律当数据看，永远不当可执行的检索语法。
	Query string
	// SessionFilters 是排序之前先用上的逻辑会话谓词。
	SessionFilters []SessionFilter
	// EventFilters 是排序之前先用上的事件谓词。
	EventFilters []EventMetadataFilter
	// Limit 是这一页最多几个会话；0 表示交给后端定。
	Limit int
	// Cursor 是同一份归一化请求上一次返回的续页令牌。
	Cursor SearchCursor
}

// EventSearchRequest 是一次会话内全文检索请求。
//
// 源: packages/session-query/session-query/src/types.ts:255-267（SessionEventSearchRequest）
type EventSearchRequest struct {
	// SessionID 是要检索哪个会话「活的优先」的那份逻辑日志。
	SessionID sessionlog.SessionID
	// Query 是全文查询串，一律当数据看。
	Query string
	// Filters 是排序之前先用上的事件谓词。
	Filters []EventMetadataFilter
	// Limit 是这一页最多几条事件；0 表示交给后端定。
	Limit int
	// Cursor 是同一份归一化请求上一次返回的续页令牌。
	Cursor SearchCursor
}

// EventSearchHit 是一条事件全文命中，带一段有界的纯文本摘录。
//
// 源: packages/session-query/session-query/src/types.ts:269-273（SessionEventSearchHit）
type EventSearchHit struct {
	EventRecord
	// Snippet 是围绕命中位置选出来的纯文本摘录。
	Snippet string
}

// SearchHit 是一个按最强命中事件排序的、按会话归并的命中。
//
// 源: packages/session-query/session-query/src/types.ts:275-279（SessionSearchHit）
type SearchHit struct {
	Record
	// BestMatch 是这个会话里最强的那条命中事件。
	BestMatch EventSearchHit
}
