// 本文件的作用：把语料解析、投影、过滤、追溯合成一个对外的读侧门面，
// 并给全文检索留一个挂点。
//
// 源: packages/session-query/session-query/src/index.ts
// 源: packages/session-query/session-query/src/config.ts:6-17

package sessionquery

import (
	"context"

	"github.com/snight1983/ds-harness-go/session"
)

// 源: packages/session-query/session-query/src/config.ts:6-9
const (
	// DefaultReadWindowMax 是 [EventReadRequest] 里 Before/After 各自的默认上限。
	DefaultReadWindowMax = 50
	// DefaultPersistedInspectConcurrency 是一次批量读里同时读几份落地日志。
	DefaultPersistedInspectConcurrency = 4
)

// Searcher 是全文检索后端的挂点。
//
// 源: packages/session-query/session-query/src/index.ts:81-127
//
// 新增: DSH 那边 `SessionQueryEngine` 是抽象类，两个检索方法是抽象方法，
// 后端**继承**它。Go 这边换成组合：后端实现这个接口，装配时挂进 [Options]。
// 换法的理由不是「Go 没有继承」，是这两件事本来就该分开——精确读、过滤、
// 追溯是不依赖任何后端的具体行为，观察、对账、排序、游标世代归实现方。
// 继承会让后端有机会覆盖前一半，那正是本包不想给的权力。
//
// 没挂后端时两个检索方法报 [CodeSearchDisabled]，其余方法照常工作。
type Searcher interface {
	// SearchSessions 检索整份「活的优先」语料，按会话归并。
	SearchSessions(ctx context.Context, request SearchRequest) (SearchPage[SearchHit], error)
	// SearchEvents 检索某一个「活的优先」逻辑会话里的事件。
	SearchEvents(ctx context.Context, request EventSearchRequest) (EventSearchPage, error)
}

// Options 是构造 [Engine] 要的东西。
//
// 源: packages/session-query/session-query/src/config.ts:11-17
//
// 新增: DSH 的 Config 只有两个数字，活会话表和持久化后端是 cordis 注入的。
// Go 这边没有容器，装配方把它们一起交进来。
type Options struct {
	// Live 是活会话表，必填。
	Live LiveSessions
	// Persistence 是可选的持久化后端；nil 表示这次装配只有活着的会话。
	Persistence Persistence
	// Searcher 是可选的全文检索后端；nil 表示两个检索方法报 [CodeSearchDisabled]。
	Searcher Searcher
	// ReadWindowMax 是 Before/After 各自能取到多大。
	//
	// 新增: DSH 是 `readWindowMax ?? 50`，显式写 0 表示「一条上下文都不许带」。
	// Go 这边零值就是「没填」，所以 0 走默认值 [DefaultReadWindowMax]，
	// 负数是配置错误。代价是「禁掉上下文窗口」这个配置在这里表达不出来——
	// 那是一个 DSH 也只是顺带允许、没人会用的配置，换来的是装配方不必为了
	// 拿默认值而把 50 抄一遍。
	ReadWindowMax int
	// PersistedInspectConcurrency 是一次批量读里同时读几份落地日志；
	// 0 走默认值 [DefaultPersistedInspectConcurrency]，负数是配置错误。
	PersistedInspectConcurrency int
}

// Engine 是会话历史读侧的门面。
//
// 源: packages/session-query/session-query/src/index.ts:81-357
type Engine struct {
	corpus        *Corpus
	searcher      Searcher
	readWindowMax int
}

// New 构造一个读侧门面。
//
// 源: packages/session-query/session-query/src/index.ts:87-105
func New(options Options) (*Engine, error) {
	if options.ReadWindowMax < 0 {
		return nil, fail(CodeInvalidConfig, "会话查询的读窗口上限不能是负数")
	}
	readWindowMax := options.ReadWindowMax
	if readWindowMax == 0 {
		readWindowMax = DefaultReadWindowMax
	}
	corpus, err := NewCorpus(options.Live, options.Persistence, options.PersistedInspectConcurrency)
	if err != nil {
		return nil, err
	}
	return &Engine{corpus: corpus, searcher: options.Searcher, readWindowMax: readWindowMax}, nil
}

// Corpus 交出底下那份逻辑语料，给 [ProjectMany] 用。
//
// 新增: DSH 把 `_corpus` 藏成私有字段，批量投影通过 readTitleSnapshots
// 这样的具体方法暴露出去。Go 的方法不能带类型参数，[ProjectMany] 只能是
// 包级函数，所以语料本身得能拿到。这不放大权力：[Corpus] 上只有读方法。
func (e *Engine) Corpus() *Corpus { return e.corpus }

// ListSessions 用「活的优先」的记录列举整份逻辑语料。
//
// 源: packages/session-query/session-query/src/index.ts:129-136
func (e *Engine) ListSessions(ctx context.Context) ([]Record, error) {
	return e.corpus.ListSessions(ctx)
}

// ReadSession 读出并重放校验一份完整的逻辑会话日志，但不把它变活。
//
// 源: packages/session-query/session-query/src/index.ts:138-151
//
// 新增: DSH 调 `Session.create(...)` 走这一步——那个构造函数把整份日志重放
// 一遍再建出活会话。Go 这边活会话类型在 DESIGN.md 第八节第 6 块还没到，
// 但重放校验的两半都已经有了：[session.ValidateLog] 验关系约束，
// [session.FoldSurface] 验表面层折不折得出来。这里两半都做，
// 少掉的只是「建出一个活会话」——本方法本来也不需要它。
func (e *Engine) ReadSession(ctx context.Context, id session.SessionID) (LogSnapshot, error) {
	loaded, err := e.corpus.Load(ctx, id)
	if err != nil {
		return LogSnapshot{}, err
	}
	if _, err := session.ValidateLog(loaded.Events); err != nil {
		return LogSnapshot{}, wrap(CodeCorruptSession, err, "会话 %q 的日志重放不过", id)
	}
	if _, err := session.FoldSurface(loaded.Events); err != nil {
		return LogSnapshot{}, wrap(CodeInvalidSurface, err, "会话 %q 的表面折不出来", id)
	}
	// loaded 已经是脱离的一份，本方法不再持有它，所以直接交出去，不必再克隆。
	return LogSnapshot{Session: loaded.Header, Events: loaded.Events}, nil
}

// FilterSessions 用不依赖后端的纯谓词过滤整份逻辑语料。
//
// 源: packages/session-query/session-query/src/index.ts:153-165
func (e *Engine) FilterSessions(ctx context.Context, filters []SessionFilter) ([]Record, error) {
	owned, err := MaterializeSessionFilters(filters)
	if err != nil {
		return nil, err
	}
	records, err := e.corpus.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	return FilterSessions(records, owned)
}

// ListEvents 列举一个逻辑会话里所有事件的轻量记录。
//
// 源: packages/session-query/session-query/src/index.ts:217-225
func (e *Engine) ListEvents(ctx context.Context, id session.SessionID) ([]EventRecord, error) {
	loaded, err := e.corpus.Load(ctx, id)
	if err != nil {
		return nil, err
	}
	return EventRecords(id, loaded.Events)
}

// FilterEvents 用不依赖后端的纯谓词扫一遍第一方语义文档。
//
// 源: packages/session-query/session-query/src/index.ts:227-239
func (e *Engine) FilterEvents(
	ctx context.Context,
	id session.SessionID,
	filters []EventFilter,
) ([]EventSearchDocument, error) {
	owned, err := MaterializeEventFilters(filters)
	if err != nil {
		return nil, err
	}
	loaded, err := e.corpus.Load(ctx, id)
	if err != nil {
		return nil, err
	}
	documents, err := BuildEventSearchDocuments(id, loaded.Events)
	if err != nil {
		return nil, err
	}
	return FilterEventDocuments(documents, owned)
}

// ReadSurface 从一次观察里读出一个会话完整的当前模型表面。
//
// 源: packages/session-query/session-query/src/index.ts:257-270
func (e *Engine) ReadSurface(ctx context.Context, id session.SessionID) (SurfaceSnapshot, error) {
	loaded, err := e.corpus.Load(ctx, id)
	if err != nil {
		return SurfaceSnapshot{}, err
	}
	events, err := CurrentSurfaceEvents(id, loaded.Events)
	if err != nil {
		return SurfaceSnapshot{}, err
	}
	snapshot := SurfaceSnapshot{Session: loaded.Header, Events: events}
	if len(loaded.Events) > 0 {
		snapshot.CapturedThroughSeq = loaded.Events[len(loaded.Events)-1].Seq
		snapshot.CapturedAny = true
	}
	return snapshot, nil
}

// TraceSession 从一次语料观察里追溯已知的祖先与后代。
//
// 源: packages/session-query/session-query/src/index.ts:272-283
func (e *Engine) TraceSession(ctx context.Context, id session.SessionID) (LineageTrace, error) {
	records, err := e.corpus.ListSessions(ctx)
	if err != nil {
		return LineageTrace{}, err
	}
	if err := checkAborted(ctx.Err()); err != nil {
		return LineageTrace{}, err
	}
	return TraceSession(records, id)
}

// TraceEvent 追溯一条事件的直接表面替换关系与来源引用关系。
//
// 源: packages/session-query/session-query/src/index.ts:285-299
func (e *Engine) TraceEvent(ctx context.Context, request EventTraceRequest) (EventTraceObservation, error) {
	loaded, err := e.corpus.Load(ctx, request.SessionID)
	if err != nil {
		return EventTraceObservation{}, err
	}
	if err := checkAborted(ctx.Err()); err != nil {
		return EventTraceObservation{}, err
	}
	trace, err := TraceEvent(request.SessionID, loaded.Events, request.Seq)
	if err != nil {
		return EventTraceObservation{}, err
	}
	return EventTraceObservation{EventTrace: trace, Session: loaded.Header}, nil
}

// ReadEvent 读一条完整事件，外加一段有界的原始日志上下文。
//
// 源: packages/session-query/session-query/src/index.ts:301-345
func (e *Engine) ReadEvent(ctx context.Context, request EventReadRequest) (EventWindow, error) {
	if err := e.checkWindow("before", request.Before); err != nil {
		return EventWindow{}, err
	}
	if err := e.checkWindow("after", request.After); err != nil {
		return EventWindow{}, err
	}
	loaded, err := e.corpus.Load(ctx, request.SessionID)
	if err != nil {
		return EventWindow{}, err
	}
	if err := checkAborted(ctx.Err()); err != nil {
		return EventWindow{}, err
	}
	target, ok := eventAtSeq(loaded.Events, request.Seq)
	if !ok {
		return EventWindow{}, fail(CodeEventNotFound, "会话 %q 里没有 seq 为 %d 的事件", request.SessionID, request.Seq)
	}
	startSeq := max(0, request.Seq-request.Before)
	endSeq := min(len(loaded.Events)-1, request.Seq+request.After)
	// loaded 是本次调用专有的一份脱离拷贝，所以窗口直接切它就行。目标事件
	// 和窗口里的那一条共享同一份负载——DSH 也是同一个对象，行为一致。
	return EventWindow{
		Session:  loaded.Header,
		Target:   target,
		Events:   loaded.Events[startSeq : endSeq+1],
		StartSeq: startSeq,
		EndSeq:   endSeq,
	}, nil
}

// SearchSessions 把跨会话全文检索交给挂上来的后端。
//
// 源: packages/session-query/session-query/src/index.ts:107-116
func (e *Engine) SearchSessions(ctx context.Context, request SearchRequest) (SearchPage[SearchHit], error) {
	if e.searcher == nil {
		return SearchPage[SearchHit]{}, searchDisabled()
	}
	return e.searcher.SearchSessions(ctx, request)
}

// SearchEvents 把会话内全文检索交给挂上来的后端。
//
// 源: packages/session-query/session-query/src/index.ts:118-127
func (e *Engine) SearchEvents(ctx context.Context, request EventSearchRequest) (EventSearchPage, error) {
	if e.searcher == nil {
		return EventSearchPage{}, searchDisabled()
	}
	return e.searcher.SearchEvents(ctx, request)
}

// checkWindow 验一侧上下文窗口的大小。
//
// 源: packages/session-query/session-query/src/index.ts:347-356
func (e *Engine) checkWindow(name string, value int) error {
	if value < 0 || value > e.readWindowMax {
		return fail(CodeInvalidWindow, "%s 必须是 0 到 %d 之间的整数", name, e.readWindowMax)
	}
	return nil
}

// searchDisabled 是「这次装配没挂检索后端」这句话。
func searchDisabled() error {
	return fail(CodeSearchDisabled, "这次装配没有挂上全文检索后端")
}
