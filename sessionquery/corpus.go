// 本文件的作用：把「活的」和「落地的」两处观察合成一份逻辑语料，
// 并按「活的优先」这条规则选出每一次读取真正用的那一份。
//
// 源: packages/session-query/session-query/src/corpus.ts

package sessionquery

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/session/persistence"
)

// LogicalSession 是为某一次精确读取选出来的、已经脱离的那一份源。
//
// 源: packages/session-query/session-query/src/corpus.ts:10-16（LogicalSession）
//
// 「脱离」的意思是拿到它的一方怎么改都碰不到活会话或者持久化层里的那一份。
type LogicalSession struct {
	// Header 是脱离出来的会话头。
	Header session.SessionHeader
	// Events 是脱离出来的完整原始日志。
	Events []session.Event
}

// LogicalSource 是只在一次投影调用期间有效的借用观察。
//
// 源: packages/session-query/session-query/src/corpus.ts:18-24
//
// 借用是有意的：一次批量投影要过整份日志，每份都先克隆一遍等于把整个语料
// 复制进内存。投影函数想留住什么，自己克隆。
type LogicalSource struct {
	// Header 是和 Events 一起选出来的会话头，投影期间不许改。
	Header session.SessionHeader
	// Events 是和 Header 一起选出来的原始事件，只在这一次投影调用里有效。
	Events []session.Event
}

// ProjectionResult 是一次批量投影里，某一个源的结果。
//
// 源: packages/session-query/session-query/src/corpus.ts:26-29
//
// 新增: DSH 是 `{status:'fulfilled'|'rejected'}` 这个判别联合。Go 这边
// Err 非 nil 就是 rejected，那正是 Go 表达同一件事的办法。
type ProjectionResult[V any] struct {
	// SessionID 是这一条结果对应的会话。
	SessionID session.SessionID
	// Value 是投影出来的值；只在 Err 为 nil 时有效。
	Value V
	// Err 是这个源解析或者投影时的失败；nil 表示成功。
	Err error
}

// LiveSessions 是此刻在内存里被推进的那些会话。
//
// 新增: DSH 从 `ctx.sessions` 这个 cordis 服务上取活会话表。本仓库不用 cordis，
// 活会话类型本身也还没移植（DESIGN.md 第八节第 6 块），所以这里只声明本包
// 真正用得着的两个动作：按 id 取一份、列举全部。第 6 块的活会话表实现这个
// 接口即可接上，本包不必改。
//
// 交出来的 [LogicalSource] 是借用的：本包在一次调用里用完就不再持有，
// 要留下的东西自己克隆。
type LiveSessions interface {
	// Get 取一份活会话的当前观察；第二个返回值为假表示这个 id 不活着。
	Get(id session.SessionID) (LogicalSource, bool)
	// List 列举此刻所有活会话的当前观察。
	List() []LogicalSource
}

// Persistence 是本包用得着的那一小块持久化后端能力。
//
// 新增: DSH 用 `ctx.inject(['sessionPersistence'])` 做可选注入，拿到的是整个
// 服务。Go 这边只声明读侧真正调的两个方法，[persistence.Store] 结构上
// 天然满足它。窄接口是有理由的：本包永远不该写会话，收一个只读接口
// 就让「写不了」变成编译期事实。
type Persistence interface {
	// List 列举所有已落地会话的头。
	List(ctx context.Context) ([]session.SessionHeader, error)
	// Inspect 读出一个已落地会话验过的逻辑视图。
	Inspect(ctx context.Context, id session.SessionID) (persistence.Inspection, error)
}

// 编译期确认：真正的持久化服务满足本包这个窄接口。
var _ Persistence = (persistence.Store)(nil)

// Corpus 按「活的优先」把两处观察解析成一份逻辑语料。
//
// 源: packages/session-query/session-query/src/corpus.ts:31-51
//
// 新增: DSH 的构造函数把 `ctx.inject(['sessionPersistence'])` 的生命周期
// 也管起来——那个可选服务可能在运行期被挂上、卸下。Go 这边持久化后端是
// 构造时传进来的一个可能为 nil 的接口值，挂载与卸载归装配方管，
// 本包只认「有」或者「没有」。
type Corpus struct {
	live                        LiveSessions
	persistence                 Persistence
	persistedInspectConcurrency int
}

// NewCorpus 构造一份逻辑语料。
//
// live 不能为 nil；persistence 为 nil 表示这次装配没挂持久化后端，
// 那样整份语料就只有活着的那些会话。
func NewCorpus(live LiveSessions, store Persistence, persistedInspectConcurrency int) (*Corpus, error) {
	if live == nil {
		return nil, fail(CodeInvalidConfig, "会话查询必须挂上活会话表")
	}
	if persistedInspectConcurrency < 0 {
		return nil, fail(CodeInvalidConfig, "落地日志的并发读取数不能是负数")
	}
	if persistedInspectConcurrency == 0 {
		persistedInspectConcurrency = DefaultPersistedInspectConcurrency
	}
	return &Corpus{
		live:                        live,
		persistence:                 store,
		persistedInspectConcurrency: persistedInspectConcurrency,
	}, nil
}

// ListSessions 列举整份逻辑语料，头是脱离的，顺序是确定的。
//
// 源: packages/session-query/session-query/src/corpus.ts:53-77
//
// 顺序是「建得晚的在前，同刻按 id」。确定的顺序不是审美：这个结果会进
// 对外协议，一个随枚举顺序漂移的列表会让同一份数据的两次查询给出不同的字节。
func (c *Corpus) ListSessions(ctx context.Context) ([]Record, error) {
	if err := checkAborted(ctx.Err()); err != nil {
		return nil, err
	}
	var persisted []session.SessionHeader
	if c.persistence != nil {
		var err error
		persisted, err = c.listPersisted(ctx)
		if err != nil {
			return nil, err
		}
	}
	if err := checkAborted(ctx.Err()); err != nil {
		return nil, err
	}
	records := make(map[session.SessionID]Record, len(persisted))
	for _, header := range persisted {
		records[header.ID] = Record{Header: header, Persisted: true}
	}
	for _, live := range c.live.List() {
		durable, ok := records[live.Header.ID]
		if ok {
			// 同一个 id 底下的两份观察对不上，只能拒——挑一份用等于把一次
			// 配置事故变成一份看起来正常的假历史。
			if err := AssertHeadersCompatible(live.Header, durable.Header); err != nil {
				return nil, err
			}
		}
		records[live.Header.ID] = Record{Header: live.Header, Live: true, Persisted: ok}
	}
	ordered := make([]Record, 0, len(records))
	for _, record := range records {
		ordered = append(ordered, record)
	}
	slices.SortFunc(ordered, compareRecords)
	return ordered, nil
}

// Load 解析一份逻辑源，优先给出活会话的脱离快照。
//
// 源: packages/session-query/session-query/src/corpus.ts:79-116
//
// 认出目标是活的就**根本不问持久化**：一个可选后端的故障不该让此刻在内存里
// 的历史读不出来。反过来，落地的那份读出来之后还要再问一次这个 id 是不是
// 刚刚变活了——会话在 Load 期间被挂起来是平常事，那时候落地的那份已经旧了。
func (c *Corpus) Load(ctx context.Context, id session.SessionID) (LogicalSession, error) {
	if err := checkAborted(ctx.Err()); err != nil {
		return LogicalSession{}, err
	}
	if live, ok := c.live.Get(id); ok {
		return detach(live), nil
	}
	if c.persistence == nil {
		return LogicalSession{}, notFound(string(id))
	}
	headers, err := c.listPersisted(ctx)
	if err != nil {
		return LogicalSession{}, err
	}
	listed, ok := headerWithID(headers, id)
	if !ok {
		return LogicalSession{}, notFound(string(id))
	}
	loaded, err := c.inspectPersisted(ctx, id)
	if err != nil {
		return LogicalSession{}, err
	}
	if live, ok := c.live.Get(id); ok {
		return detach(live), nil
	}
	if err := AssertHeadersCompatible(loaded.Meta, listed); err != nil {
		return LogicalSession{}, err
	}
	return detach(LogicalSource{Header: loaded.Meta, Events: loaded.Events}), nil
}

// ProjectMany 用一次持久化列举，就地投影一批互不重复的逻辑源。
//
// 源: packages/session-query/session-query/src/corpus.ts:118-220
//
// 新增: DSH 那边这是 `SessionCorpus` 的一个泛型方法。Go 的方法不能带类型参数，
// 所以它在这里是包级函数——而这个 API 的全部价值正在于「一次观察里折出
// 任意一种投影」，把 Value 写死成某个具体类型就等于把它废掉。
//
// 结果按 ids 去重后的首次出现顺序排。单个源的失败被隔离在它自己那条结果的
// Err 上；只有取消会让整个调用失败——前者是「这一个会话读不了」，
// 后者是「这次观察不作数了」，混成一件会让调用方把半份结果当全份用。
//
// 新增: DSH 用 `Promise.allSettled` 起若干个 worker，每个 worker 抢下一个 id。
// Go 这边同样是若干个 goroutine 抢任务，但**投影函数只在调用方这一条
// goroutine 上跑**：JS 是单线程的，DSH 的投影天然不会并发；Go 不做这一步的话，
// 一个捕获了 map 的折叠闭包会当场竞争。让 worker 只负责读、投影留在原地，
// 行为才和 DSH 一致，调用方也不必给投影函数额外加锁。
// 「一次最多几份完整日志活着」这个内存上界没变：worker 数加手上正在投影的那一份。
func ProjectMany[V any](
	ctx context.Context,
	corpus *Corpus,
	ids []session.SessionID,
	project func(LogicalSource) (V, error),
) ([]ProjectionResult[V], error) {
	unique := uniqueIDs(ids)
	if err := checkAborted(ctx.Err()); err != nil {
		return nil, err
	}
	results := make([]ProjectionResult[V], len(unique))
	var pending []int
	for index, id := range unique {
		results[index].SessionID = id
		live, ok := corpus.live.Get(id)
		if !ok {
			pending = append(pending, index)
			continue
		}
		results[index] = projectSource(id, live, project)
	}
	if len(pending) == 0 {
		return results, nil
	}
	if corpus.persistence == nil {
		for _, index := range pending {
			results[index].Err = notFound(string(unique[index]))
		}
		return results, nil
	}
	headers, err := corpus.listPersisted(ctx)
	if err != nil {
		// 列举失败是整份语料的事，不是某一个会话的事；但它仍然被摊到每一条
		// 待解析的结果上，而不是让整个调用失败——已经从活会话表投影出来的
		// 那些结果是好的，扔掉它们没有道理。取消例外，见上面的说明。
		if isAborted(err) {
			return nil, err
		}
		for _, index := range pending {
			results[index].Err = err
		}
		return results, nil
	}
	byID := make(map[session.SessionID]session.SessionHeader, len(headers))
	for _, header := range headers {
		byID[header.ID] = header
	}
	if err := projectPending(ctx, corpus, unique, pending, byID, results, project); err != nil {
		return nil, err
	}
	return results, nil
}

// resolution 是一个 worker 读完之后交回调用方 goroutine 的东西。
type resolution struct {
	index  int
	source LogicalSource
	err    error
}

// projectPending 起若干个 worker 读落地日志，投影仍在调用方 goroutine 上做。
//
// 源: packages/session-query/session-query/src/corpus.ts:195-219
//
// 和 [ProjectMany] 一样是包级函数：Go 的方法不能带类型参数。
func projectPending[V any](
	ctx context.Context,
	c *Corpus,
	unique []session.SessionID,
	pending []int,
	byID map[session.SessionID]session.SessionHeader,
	results []ProjectionResult[V],
	project func(LogicalSource) (V, error),
) error {
	jobs := make(chan int)
	resolutions := make(chan resolution)
	go func() {
		defer close(jobs)
		for _, index := range pending {
			select {
			case jobs <- index:
			case <-ctx.Done():
				return
			}
		}
	}()
	var group sync.WaitGroup
	workers := min(c.persistedInspectConcurrency, len(pending))
	group.Add(workers)
	for range workers {
		go func() {
			defer group.Done()
			for index := range jobs {
				source, err := c.resolvePending(ctx, unique[index], byID)
				select {
				case resolutions <- resolution{index: index, source: source, err: err}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		group.Wait()
		close(resolutions)
	}()
	for resolved := range resolutions {
		if resolved.err != nil {
			results[resolved.index].Err = resolved.err
			continue
		}
		results[resolved.index] = projectSource(unique[resolved.index], resolved.source, project)
	}
	return checkAborted(ctx.Err())
}

// resolvePending 解析一个不在活会话表里的 id。
//
// 源: packages/session-query/session-query/src/corpus.ts:167-194
func (c *Corpus) resolvePending(
	ctx context.Context,
	id session.SessionID,
	byID map[session.SessionID]session.SessionHeader,
) (LogicalSource, error) {
	listed, ok := byID[id]
	if !ok {
		// 列举那一刻不在，此刻可能已经挂起来了。
		if live, ok := c.live.Get(id); ok {
			return live, nil
		}
		return LogicalSource{}, notFound(string(id))
	}
	if err := checkAborted(ctx.Err()); err != nil {
		return LogicalSource{}, err
	}
	loaded, err := c.inspectPersisted(ctx, id)
	if err != nil {
		return LogicalSource{}, err
	}
	if live, ok := c.live.Get(id); ok {
		return live, nil
	}
	if err := AssertHeadersCompatible(loaded.Meta, listed); err != nil {
		return LogicalSource{}, err
	}
	return LogicalSource{Header: loaded.Meta, Events: loaded.Events}, nil
}

// projectSource 跑一次投影，把失败收进结果里。
//
// 源: packages/session-query/session-query/src/corpus.ts:223-239
func projectSource[V any](
	id session.SessionID,
	source LogicalSource,
	project func(LogicalSource) (V, error),
) ProjectionResult[V] {
	value, err := project(source)
	if err != nil {
		return ProjectionResult[V]{SessionID: id, Err: err}
	}
	return ProjectionResult[V]{SessionID: id, Value: value}
}

// listPersisted 列举落地会话，把后端的失败翻成本包的码。
//
// 源: packages/session-query/session-query/src/corpus.ts:252-266
func (c *Corpus) listPersisted(ctx context.Context) ([]session.SessionHeader, error) {
	headers, err := c.persistence.List(ctx)
	if err != nil {
		if abort := abortedFailure(ctx, err); abort != nil {
			return nil, abort
		}
		return nil, wrap(CodePersistenceFailed, err, "列举落地会话失败")
	}
	return headers, nil
}

// inspectPersisted 读一份落地日志，把后端的失败翻成本包的码。
//
// 源: packages/session-query/session-query/src/corpus.ts:268-290
//
// 「坏了」和「读不了」分成两个码：前者说这份存档本身没救，重试没用；
// 后者说这一次没读成，重试可能有用。合成一个码会让调用方无从判断该不该重试。
func (c *Corpus) inspectPersisted(ctx context.Context, id session.SessionID) (persistence.Inspection, error) {
	loaded, err := c.persistence.Inspect(ctx, id)
	if err != nil {
		if abort := abortedFailure(ctx, err); abort != nil {
			return persistence.Inspection{}, abort
		}
		var corruption *persistence.CorruptionError
		if errors.As(err, &corruption) {
			return persistence.Inspection{}, wrap(CodeCorruptSession, err, "落地的会话 %q 坏了", id)
		}
		return persistence.Inspection{}, wrap(CodePersistenceFailed, err, "读取会话 %q 失败", id)
	}
	return loaded, nil
}

// abortedFailure 判断一次后端失败该不该算成取消；不是就返回 nil。
//
// 源: packages/session-query/session-query/src/corpus.ts:259 的 `if (signal?.aborted)`
//
// 两条都算：ctx 已经取消（DSH 看的就是这个），或者后端把取消原样报了上来
// （它自己内部的超时也走这条）。
func abortedFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return checkAborted(ctx.Err())
	}
	if isAborted(err) {
		return checkAborted(err)
	}
	return nil
}

// detach 把一份借用的观察复制成脱离的一份。
//
// 源: packages/session-query/session-query/src/corpus.ts:292-297
//
// 会话头全是标量字段，值拷贝就是克隆；事件里有 json.RawMessage 和切片，
// 得走 [session.Event.Clone]。
func detach(source LogicalSource) LogicalSession {
	events := make([]session.Event, 0, len(source.Events))
	for _, event := range source.Events {
		events = append(events, event.Clone())
	}
	return LogicalSession{Header: source.Header, Events: events}
}

// headerWithID 在一份列举结果里找某个 id。
func headerWithID(headers []session.SessionHeader, id session.SessionID) (session.SessionHeader, bool) {
	for _, header := range headers {
		if header.ID == id {
			return header, true
		}
	}
	return session.SessionHeader{}, false
}

// uniqueIDs 按首次出现顺序去重。
//
// 源: packages/session-query/session-query/src/corpus.ts:133 的 `[...new Set(sessionIds)]`
func uniqueIDs(ids []session.SessionID) []session.SessionID {
	seen := make(map[session.SessionID]bool, len(ids))
	unique := make([]session.SessionID, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	return unique
}

// compareRecords 是「建得晚的在前，同刻按 id」。
//
// 源: packages/session-query/session-query/src/corpus.ts:299-301
func compareRecords(a, b Record) int {
	if a.Header.CreatedAt != b.Header.CreatedAt {
		return int(min(max(b.Header.CreatedAt-a.Header.CreatedAt, -1), 1))
	}
	return strings.Compare(string(a.Header.ID), string(b.Header.ID))
}
