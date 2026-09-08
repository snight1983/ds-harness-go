// 本文件的作用：两个检索方法本身——请求怎么归一化、事件谓词怎么下推到库里、
// 库里那一半和内存里那一半怎么并起来排、这一页怎么切。
//
// 源: packages/session-query/session-query-sqlite/src/query.ts
// 源: packages/session-query/session-query-sqlite/src/index.ts:256-314（searchSessions、searchEvents）

package searchstore

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/snight1983/ds-harness-go/adapter/datastore"
	"github.com/snight1983/ds-harness-go/feature/sessionquery"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// scored 是一条参与排序的命中：文档本身，加上它那两个排序赖以成立的数。
type scored struct {
	document sessionquery.EventSearchDocument
	// hits 是要找的那段文字在正文里出现了几次（不计重叠）。
	hits int64
	// length 是折过之后的正文有多少个字。同样多次命中时短的更贴题。
	length int
}

// compareScored 是本包唯一的那个次序。
//
// 源: packages/session-query/session-query-sqlite/src/index.ts:661-694（三处 ORDER BY）
//
// 它必须和 [datastore.DocUnit.Match] 在库里用的那个次序**逐项一致**：库里排好的
// 那一半和内存里排好的那一半要并成一串，两边的次序但凡差一项，并出来的顺序就不是
// 任何一边承诺的顺序，翻页也就跟着错位。
//
// 末尾拿会话 id 和 seq 兜底，是为了让完全同分的那些条目也有一个定死的先后——
// 没有它，同一份语料翻两次页会翻出两个顺序。
func compareScored(a, b scored) int {
	if order := cmp.Compare(b.hits, a.hits); order != 0 {
		return order
	}
	if order := cmp.Compare(a.length, b.length); order != 0 {
		return order
	}
	if order := cmp.Compare(b.document.Time, a.document.Time); order != 0 {
		return order
	}
	if order := cmp.Compare(a.document.SessionID, b.document.SessionID); order != 0 {
		return order
	}
	return cmp.Compare(b.document.Seq, a.document.Seq)
}

// SearchSessions 检索整份「活的优先」语料，按会话归并。
//
// 源: packages/session-query/session-query-sqlite/src/index.ts:256-282
//
// 一个会话最多出一条，出的是它里面最强的那条命中。这件事在库里那一侧由
// [datastore.MatchRequest.BestPerGroup] 做——不能取回来再在内存里挑：翻页是在库里
// 切的，先切页再挑每组最好的那条，会让一页里挤满同一个会话，而那个会话在下一页
// 一条都不剩。
func (s *Store) SearchSessions(
	ctx context.Context,
	request sessionquery.SearchRequest,
) (sessionquery.SearchPage[sessionquery.SearchHit], error) {
	var empty sessionquery.SearchPage[sessionquery.SearchHit]

	query, err := normalizeQuery(request.Query)
	if err != nil {
		return empty, err
	}
	limit, err := s.normalizeLimit(request.Limit)
	if err != nil {
		return empty, err
	}
	sessionFilters, err := sessionquery.MaterializeSessionFilters(request.SessionFilters)
	if err != nil {
		return empty, err
	}
	eventFilters, err := materializeMetadataFilters(request.EventFilters)
	if err != nil {
		return empty, err
	}

	fingerprint := sessionsFingerprint(query, sessionFilters, eventFilters, limit)
	seen, err := s.observe(ctx, "")
	if err != nil {
		return empty, err
	}
	offset, err := decodeCursor(request.Cursor, scopeSessions, fingerprint, seen.generation)
	if err != nil {
		return empty, err
	}

	allowed, err := allowedSessions(seen, sessionFilters)
	if err != nil {
		return empty, err
	}
	down, ok, err := pushDownEventFilters(eventFilters)
	if err != nil {
		return empty, err
	}
	if !ok || len(allowed) == 0 {
		// 谓词自相矛盾（比如两条类型过滤器交出来是空集），或者一个会话都没过关。
		// 这两种都不必去问库，答案已经定了。
		return empty, nil
	}

	// 多要一条：这一页要几条是知道的，「后面还有没有」只能靠多取一条看出来。
	need := offset + limit + 1
	gathered, err := s.gatherPersisted(ctx, seen, allowed, query, down, need)
	if err != nil {
		return empty, err
	}
	gathered = append(gathered, s.gatherLive(seen, allowed, query, eventFilters, true)...)
	slices.SortFunc(gathered, compareScored)

	page := slice(gathered, offset, limit)
	items := make([]sessionquery.SearchHit, 0, len(page))
	for _, hit := range page {
		record, ok := recordOf(seen, hit.document.SessionID)
		if !ok {
			// 会话在这次观察之后被删掉了。跳过而不是报错：一次检索本来就只承诺
			// 「照这份观察答」，而观察永远比答案早一点点。
			continue
		}
		items = append(items, sessionquery.SearchHit{
			Record:    record,
			BestMatch: s.hitOf(hit, query),
		})
	}
	return sessionquery.SearchPage[sessionquery.SearchHit]{
		Items:      items,
		NextCursor: s.nextCursor(len(gathered), offset, limit, scopeSessions, fingerprint, seen.generation),
	}, nil
}

// SearchEvents 检索某一个「活的优先」逻辑会话里的事件。
//
// 源: packages/session-query/session-query-sqlite/src/index.ts:284-314
//
// 目标活着就**根本不问库**：活着的那份才是最新的，而落地的那份此刻一定旧了。
func (s *Store) SearchEvents(
	ctx context.Context,
	request sessionquery.EventSearchRequest,
) (sessionquery.EventSearchPage, error) {
	var empty sessionquery.EventSearchPage

	query, err := normalizeQuery(request.Query)
	if err != nil {
		return empty, err
	}
	limit, err := s.normalizeLimit(request.Limit)
	if err != nil {
		return empty, err
	}
	filters, err := materializeMetadataFilters(request.Filters)
	if err != nil {
		return empty, err
	}

	fingerprint := eventsFingerprint(request.SessionID, query, filters, limit)
	seen, err := s.observe(ctx, request.SessionID)
	if err != nil {
		return empty, err
	}
	offset, err := decodeCursor(request.Cursor, scopeEvents, fingerprint, seen.generation)
	if err != nil {
		return empty, err
	}

	header, ok := headerOf(seen, request.SessionID)
	if !ok {
		return empty, fail(sessionquery.CodeSessionNotFound, "找不到会话 %q", request.SessionID)
	}
	down, ok, err := pushDownEventFilters(filters)
	if err != nil {
		return empty, err
	}
	var gathered []scored
	if !ok {
		gathered = nil
	} else if _, live := seen.live[request.SessionID]; live {
		gathered = s.gatherLive(seen, map[sessionlog.SessionID]sessionquery.Record{
			request.SessionID: {Header: header, Live: true},
		}, query, filters, false)
	} else {
		gathered, err = s.matchPersisted(ctx, datastore.MatchRequest{
			Groups: []string{string(request.SessionID)},
			Needle: query,
			// 多要一条，理由见 [Store.SearchSessions] 里 need 那一处。
			Limit: offset + limit + 1,
		}, down)
		if err != nil {
			return empty, err
		}
	}
	slices.SortFunc(gathered, compareScored)

	page := slice(gathered, offset, limit)
	items := make([]sessionquery.EventSearchHit, 0, len(page))
	for _, hit := range page {
		items = append(items, s.hitOf(hit, query))
	}
	return sessionquery.EventSearchPage{
		SearchPage: sessionquery.SearchPage[sessionquery.EventSearchHit]{
			Items:      items,
			NextCursor: s.nextCursor(len(gathered), offset, limit, scopeEvents, fingerprint, seen.generation),
		},
		Session: header,
	}, nil
}

// ---- 归一化 ----

// normalizeQuery 把查询串收拾成一份能拿去比对的文字。
//
// 源: packages/session-query/session-query-sqlite/src/query.ts:318-336（normalizeQuery）
//
// 空白折成一个空格，这样查询里的空白怎么打都能对上正文里的换行和多空格——这和
// [sessionquery.CompileTextFilter] 把段与段之间换成 `\s+` 是同一件事，只是一个
// 用正则表达、一个用折过的文本表达。
func normalizeQuery(text string) (string, error) {
	if strings.ContainsRune(text, 0) {
		return "", fail(sessionquery.CodeInvalidQuery, "检索文字里不能有 U+0000")
	}
	normalized := strings.Join(strings.Fields(text), " ")
	if normalized == "" {
		return "", fail(sessionquery.CodeInvalidQuery, "检索文字必须含有非空白文字")
	}
	return normalized, nil
}

// normalizeLimit 定下这一页要几条。
//
// 源: packages/session-query/session-query-sqlite/src/query.ts:369-383（normalizeLimit）
func (s *Store) normalizeLimit(limit int) (int, error) {
	if limit == 0 {
		return s.limit, nil
	}
	if limit < 0 || limit > s.maxLimit {
		return 0, fail(sessionquery.CodeInvalidLimit,
			"一页要 %d 条，必须落在 1 到 %d 之间", limit, s.maxLimit)
	}
	return limit, nil
}

// materializeMetadataFilters 复制并验一遍事件谓词。
//
// 源: packages/session-query/session-query-sqlite/src/query.ts:346-367（materializeMetadataFilters）
//
// 收进来的是 [sessionquery.EventMetadataFilter]，交出去的是
// [sessionquery.EventFilter]：类型系统已经挡住了文本过滤器，所以 DSH 那边那道
// 「元数据槽里塞了 text」的运行期检查在这里不存在。
func materializeMetadataFilters(
	filters []sessionquery.EventMetadataFilter,
) ([]sessionquery.EventFilter, error) {
	widened := make([]sessionquery.EventFilter, 0, len(filters))
	for _, filter := range filters {
		widened = append(widened, filter)
	}
	return sessionquery.MaterializeEventFilters(widened)
}

// ---- 事件谓词下推 ----

// pushed 是一组已经折成介质那几个字段的事件谓词。
type pushed struct {
	kinds  []string
	facets []string
	seq    sessionquery.Range
	at     sessionquery.Range
}

// pushDownEventFilters 把事件谓词折成 [datastore.MatchRequest] 认得的那几项。
//
// 源: packages/session-query/session-query-sqlite/src/query.ts:193-221（buildEventWhere）
//
// 第二个返回值为假表示这组谓词自相矛盾，一条都不会命中——比如两条类型过滤器交出来
// 是空集。这件事必须在这里判：[datastore.MatchRequest] 里空切片的意思是**不限**，
// 把一个空集合原样传下去会变成「什么都不筛」，于是一次本该返回零条的查询返回了
// 全部。
//
// 同一种谓词出现两次是**交**（一组过滤器之间是与），区间取两头最紧的那个。
func pushDownEventFilters(filters []sessionquery.EventFilter) (pushed, bool, error) {
	var down pushed
	for _, filter := range filters {
		switch typed := filter.(type) {
		case sessionquery.TypeFilter:
			values := make([]string, 0, len(typed.Values))
			for _, value := range typed.Values {
				values = append(values, string(value))
			}
			down.kinds = intersect(down.kinds, values)
			if len(down.kinds) == 0 {
				return pushed{}, false, nil
			}
		case sessionquery.SurfaceFilter:
			values := make([]string, 0, len(typed.Values))
			for _, value := range typed.Values {
				values = append(values, string(value))
			}
			down.facets = intersect(down.facets, values)
			if len(down.facets) == 0 {
				return pushed{}, false, nil
			}
		case sessionquery.SeqFilter:
			down.seq = tighten(down.seq, typed.Range)
		case sessionquery.TimeFilter:
			down.at = tighten(down.at, typed.Range)
		default:
			// 文本过滤器进不了这里（类型系统挡着），别的变体也不存在。走到这儿
			// 说明本包漏接了一个新变体，宁可当场报错也不要静默放行——后者会让
			// 一次查询悄悄返回比调用方要的多的结果。
			return pushed{}, false, fail(sessionquery.CodeInvalidFilter,
				"会话检索后端不认识这种事件过滤器：%T", filter)
		}
	}
	if emptyRange(down.seq) || emptyRange(down.at) {
		return pushed{}, false, nil
	}
	return down, true, nil
}

// apply 把折好的谓词填进一次查找。
func (p pushed) apply(request *datastore.MatchRequest) {
	request.Kinds = p.kinds
	request.Facets = p.facets
	request.SeqFrom, request.SeqTo = p.seq.From, p.seq.To
	request.AtFrom, request.AtTo = p.at.From, p.at.To
}

// intersect 求两组取值的交；kept 为 nil 表示还没设过限，直接换成新的这一组。
func intersect(kept, incoming []string) []string {
	if kept == nil {
		if len(incoming) == 0 {
			// 空的一组就是「一个都不许」。给一个非 nil 的空切片，好和「没设过限」
			// 分开——调用方看到的是 len 为 0，两种情况在这里只差一个 nil。
			return []string{}
		}
		return incoming
	}
	narrowed := make([]string, 0, min(len(kept), len(incoming)))
	for _, value := range kept {
		if slices.Contains(incoming, value) {
			narrowed = append(narrowed, value)
		}
	}
	return narrowed
}

// tighten 把两个区间收成两头都最紧的那一个。
func tighten(kept, incoming sessionquery.Range) sessionquery.Range {
	if incoming.From != nil && (kept.From == nil || *incoming.From > *kept.From) {
		kept.From = incoming.From
	}
	if incoming.To != nil && (kept.To == nil || *incoming.To < *kept.To) {
		kept.To = incoming.To
	}
	return kept
}

// emptyRange 判断一个区间收得太紧、已经装不下任何值。
func emptyRange(r sessionquery.Range) bool {
	return r.From != nil && r.To != nil && *r.From > *r.To
}

// ---- 库里那一半 ----

// gatherPersisted 从库里取够这一页要的那些命中。
//
// 圈定哪些会话这件事，能塞进查找就塞：一串会话名传下去，筛在库里做，取回来的每
// 一条都是要的。塞不下（见 [maxPushedGroups]）就一页页往上取、在这边筛，慢一点
// 但不会撞上绑定参数的上限。
//
// 只取前 need 条是对的：库里已经按同一个次序排好了，排在第 need 条之后的那些，
// 并上活着的那些之后也进不了前 need 条。
func (s *Store) gatherPersisted(
	ctx context.Context,
	seen observation,
	allowed map[sessionlog.SessionID]sessionquery.Record,
	query string,
	down pushed,
	need int,
) ([]scored, error) {
	targets := make([]string, 0, len(allowed))
	for id, record := range allowed {
		if !record.Persisted || record.Live {
			// 活着的那份盖住落地的那份：同一个会话两边都有时，落地的一定旧了。
			continue
		}
		targets = append(targets, string(id))
	}
	if len(targets) == 0 {
		return nil, nil
	}
	slices.Sort(targets)

	request := datastore.MatchRequest{
		Needle:       query,
		BestPerGroup: true,
		Limit:        max(need, 1),
	}
	if len(targets) <= maxPushedGroups {
		request.Groups = targets
		return s.matchPersisted(ctx, request, down)
	}

	// 塞不下就把活着的那些排除掉、一页页取，在这边按放行名单筛。
	for id := range seen.live {
		request.ExcludeGroups = append(request.ExcludeGroups, string(id))
	}
	slices.Sort(request.ExcludeGroups)

	var gathered []scored
	for offset := 0; len(gathered) < need; offset += request.Limit {
		request.Offset = offset
		batch, err := s.matchPersisted(ctx, request, down)
		if err != nil {
			return nil, err
		}
		for _, hit := range batch {
			if _, ok := allowed[hit.document.SessionID]; ok {
				gathered = append(gathered, hit)
			}
		}
		if len(batch) < request.Limit {
			break
		}
	}
	return gathered, nil
}

// matchPersisted 发一次查找，把交回来的行折成待排序的命中。
func (s *Store) matchPersisted(
	ctx context.Context,
	request datastore.MatchRequest,
	down pushed,
) ([]scored, error) {
	down.apply(&request)
	rows, err := s.docs.Match(ctx, request)
	if err != nil {
		if abort := abortedFailure(ctx, err); abort != nil {
			return nil, abort
		}
		return nil, wrap(sessionquery.CodeIndexFailed, err, "在会话索引里查找失败")
	}
	hits := make([]scored, 0, len(rows))
	for _, row := range rows {
		hits = append(hits, scored{
			document: fromRow(row),
			hits:     row.Hits,
			// 库里那一列没交回来，在这边照同一个定义算一遍。同一个折法、同一个
			// 数法，所以两边算出来的是同一个数。
			length: utf8.RuneCountInString(datastore.Fold(row.Body)),
		})
	}
	return hits, nil
}

// ---- 内存里那一半 ----

// gatherLive 给活着的那些会话就地算分。
//
// bestPerSession 为真时每个会话只留最强的那一条，对应库里那侧的
// [datastore.MatchRequest.BestPerGroup]。
func (s *Store) gatherLive(
	seen observation,
	allowed map[sessionlog.SessionID]sessionquery.Record,
	query string,
	filters []sessionquery.EventFilter,
	bestPerSession bool,
) []scored {
	needle := datastore.Fold(query)
	if needle == "" {
		return nil
	}
	var gathered []scored
	for _, id := range sortedKeys(seen.live) {
		if _, ok := allowed[id]; !ok {
			continue
		}
		documents, err := sessionquery.FilterEventDocuments(seen.live[id].documents, filters)
		if err != nil {
			// 谓词已经在 materializeMetadataFilters 那一步验过了，编不出判定
			// 函数是不可能的。真出了，把这个会话跳过好过让整次检索失败。
			s.logger.Warn("活会话的过滤器编不出来，跳过它", "session", string(id), "error", err)
			continue
		}
		var best scored
		found := false
		for _, document := range documents {
			folded := datastore.Fold(document.Text)
			count := int64(strings.Count(folded, needle))
			if count == 0 {
				continue
			}
			hit := scored{
				document: document,
				hits:     count,
				length:   utf8.RuneCountInString(folded),
			}
			if !bestPerSession {
				gathered = append(gathered, hit)
				continue
			}
			if !found || compareScored(hit, best) < 0 {
				best, found = hit, true
			}
		}
		if bestPerSession && found {
			gathered = append(gathered, best)
		}
	}
	return gathered
}

// ---- 收尾 ----

// allowedSessions 算出这次检索允许命中的那些会话。
//
// 会话这一层的谓词在**内存里**判，不下推到库里：会话头不在索引里——索引里只有
// 文档。这不是省事，是分工——那些头归 [sessionquery] 和持久化后端管，把它们再写
// 一份进检索的表，就等于让同一件事有两个说法。
func allowedSessions(
	seen observation,
	filters []sessionquery.SessionFilter,
) (map[sessionlog.SessionID]sessionquery.Record, error) {
	records := make([]sessionquery.Record, 0, len(seen.persisted)+len(seen.live))
	for _, id := range sortedKeys(seen.persisted) {
		if _, live := seen.live[id]; live {
			continue
		}
		records = append(records, sessionquery.Record{
			Header: seen.persisted[id].Header, Persisted: true,
		})
	}
	for _, id := range sortedKeys(seen.live) {
		_, persisted := seen.persisted[id]
		records = append(records, sessionquery.Record{
			Header: seen.live[id].header, Live: true, Persisted: persisted,
		})
	}
	kept, err := sessionquery.FilterSessions(records, filters)
	if err != nil {
		return nil, err
	}
	allowed := make(map[sessionlog.SessionID]sessionquery.Record, len(kept))
	for _, record := range kept {
		allowed[record.Header.ID] = record
	}
	return allowed, nil
}

// recordOf 交出一个会话在这次观察里的轻量身份。
func recordOf(seen observation, id sessionlog.SessionID) (sessionquery.Record, bool) {
	_, persisted := seen.persisted[id]
	if entry, live := seen.live[id]; live {
		return sessionquery.Record{Header: entry.header, Live: true, Persisted: persisted}, true
	}
	if persisted {
		return sessionquery.Record{Header: seen.persisted[id].Header, Persisted: true}, true
	}
	return sessionquery.Record{}, false
}

// headerOf 交出一个会话「活的优先」的那份头。
func headerOf(seen observation, id sessionlog.SessionID) (sessionlog.SessionHeader, bool) {
	if entry, live := seen.live[id]; live {
		return entry.header, true
	}
	if snapshot, persisted := seen.persisted[id]; persisted {
		return snapshot.Header, true
	}
	return sessionlog.SessionHeader{}, false
}

// hitOf 把一条命中折成对外那条带摘录的结果。
func (s *Store) hitOf(hit scored, query string) sessionquery.EventSearchHit {
	return sessionquery.EventSearchHit{
		EventRecord: hit.document.EventRecord,
		Snippet:     snippet(hit.document.Text, query, s.snippetChars),
	}
}

// slice 从排好的整串里切出这一页。
func slice(gathered []scored, offset, limit int) []scored {
	if offset >= len(gathered) {
		return nil
	}
	return gathered[offset:min(offset+limit, len(gathered))]
}

// nextCursor 签发续页令牌；这一页已经到底就交回空串。
func (s *Store) nextCursor(
	total, offset, limit int,
	scope, fingerprint, generation string,
) sessionquery.SearchCursor {
	if offset+limit >= total {
		return ""
	}
	return encodeCursor(cursorPayload{
		Version:     cursorVersion,
		Scope:       scope,
		Fingerprint: fingerprint,
		Generation:  generation,
		Offset:      offset + limit,
	})
}
