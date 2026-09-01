// 本文件的作用：五件工具各自那一趟活——从模型写下的参数，到调引擎，到交出去
// 的那段文本。
//
// 源: packages/session-query/tool-session-query/src/operations.ts
//
// 每一趟的骨架都一样：认出调用方 → 画边界 → 洗参数 → 过 [call] 调引擎 →
// 再验一遍交回来的那份观察 → 读标题 → 排版。顺序不能随便换：授权必须在调用
// 之前，复核必须在调用之后。

package querytool

import (
	"context"

	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/sessionquery"
)

// executeSessionSearch 跑一次跨会话检索。
//
// 源: packages/session-query/tool-session-query/src/operations.ts:276-283（operations）
func (c *Controller) executeSessionSearch(
	ctx context.Context,
	args sessionSearchArgs,
	exec *tools.RunContext,
) (string, error) {
	from, err := c.callerOf(exec)
	if err != nil {
		return "", err
	}
	// 跨会话检索的整个边界就是调用方的工作目录。没有工作目录时不存在「同一个
	// 工作区」这回事，于是这件工具对这个调用方根本不可用——而不是退化成
	// 一次无边界的全库检索。
	if from.header.Cwd == "" {
		return "", fail(CodeUnauthorized,
			"cross-session search is unavailable because the caller session has no workspace")
	}
	query, err := normalizeQuery(args.Query)
	if err != nil {
		return "", err
	}
	sessionFilters, err := buildSessionFilters(args)
	if err != nil {
		return "", err
	}
	eventFilters, err := buildEventFilters(eventFilterInput{
		seqFrom:    args.EventSeqFrom,
		seqTo:      args.EventSeqTo,
		timeFrom:   args.EventTimeFrom,
		timeTo:     args.EventTimeTo,
		eventTypes: args.EventTypes,
		surfaces:   args.EventSurfaces,
	})
	if err != nil {
		return "", err
	}
	requestedParents, requestedParentsGiven, err := materializeParentSessionIDs(args.ParentSessionIDs)
	if err != nil {
		return "", err
	}
	if requestedParentsGiven || args.IncludeRootSessions {
		// 父会话 id 本身就是情报：一个越界的父 id 如果原样交给引擎，它筛出来的
		// 结果会反过来证实那个会话存在。所以先把这张表过一遍授权，只留下
		// 调用方本来就看得见的。
		authorizedParents := map[session.SessionID]struct{}{}
		if requestedParentsGiven {
			authorizedParents, err = c.authorizeSessionIDs(ctx, from, requestedParents)
			if err != nil {
				return "", err
			}
		}
		var parentValues []session.SessionID
		for _, id := range requestedParents {
			if _, ok := authorizedParents[id]; ok {
				parentValues = append(parentValues, id)
			}
		}
		if args.IncludeRootSessions {
			// 空串在 [sessionquery.ParentFilter] 里就是「没有父会话」。
			parentValues = append(parentValues, "")
		}
		// 一个值都不剩说明模型问的那些父会话全在界外。这时候不该去调引擎——
		// 一张空取值表的过滤器筛不出任何东西，跑一趟只是白白告诉后端我们在找什么。
		if len(parentValues) == 0 {
			return formatEmptySessionSearch(), nil
		}
		sessionFilters = append(sessionFilters, sessionquery.ParentFilter{Values: parentValues})
	}
	sessionFilters = append(sessionFilters, sessionquery.CwdFilter{Values: []string{from.header.Cwd}})

	collected, err := collectPages(ctx, c.maxSearchResults,
		func(cursor sessionquery.SearchCursor) ([]sessionquery.SearchHit, sessionquery.SearchCursor, error) {
			page, err := call(ctx, c, "session search", func() (sessionquery.SearchPage[sessionquery.SearchHit], error) {
				return c.service.SearchSessions(ctx, sessionquery.SearchRequest{
					Query:          query,
					SessionFilters: sessionFilters,
					EventFilters:   eventFilters,
					Cursor:         cursor,
				})
			})
			if err != nil {
				return nil, "", err
			}
			return page.Items, page.NextCursor, nil
		},
		// 调用方自己那个会话被排掉：模型正在里面，跨会话检索是用来找**别的**
		// 会话的，把当前会话混进来只会挤掉真正有用的那几条。
		func(hit sessionquery.SearchHit) bool {
			return hit.Header.ID != from.id && recordAuthorized(hit.Record, from)
		})
	if err != nil {
		return "", err
	}

	var parentIDs []session.SessionID
	ids := make([]session.SessionID, 0, len(collected.items))
	for _, hit := range collected.items {
		ids = append(ids, hit.Header.ID)
		if hit.Header.ParentSession != "" {
			parentIDs = append(parentIDs, hit.Header.ParentSession)
		}
	}
	authorizedParents, err := c.authorizeSessionIDs(ctx, from, parentIDs)
	if err != nil {
		return "", err
	}
	titles, err := c.readTitles(ctx, from, ids)
	if err != nil {
		return "", err
	}
	return formatSessionSearch(collected, titles, authorizedParents), nil
}

// executeEventSearch 跑一次会话内检索。
//
// 源: packages/session-query/tool-session-query/src/operations.ts:116-166
func (c *Controller) executeEventSearch(
	ctx context.Context,
	args eventSearchArgs,
	exec *tools.RunContext,
) (string, error) {
	from, err := c.callerOf(exec)
	if err != nil {
		return "", err
	}
	sessionID := targetID(args.SessionID, from)
	if err := c.authorizeTarget(ctx, from, sessionID); err != nil {
		return "", err
	}
	query, err := normalizeQuery(args.Query)
	if err != nil {
		return "", err
	}
	seqRange, err := sequenceRange(args.SeqFrom, args.SeqTo)
	if err != nil {
		return "", err
	}
	if sessionID == from.id {
		// 检索自己这个会话时，上界被钉在**当前这一步开始之前**。理由是模型
		// 已经看得见当前步骤里的东西，把它们再检索一遍等于让模型读自己刚写的
		// 字，还会把真正在找的旧事件挤出结果。
		stepStart, ok := lastStepStart(from.events)
		if !ok {
			// 一个还没开过步骤的会话没有这条边界可用。给不出边界就不许检索：
			// 退化成「全都能搜」正好是上面那条要防的事。
			return "", fail(CodeNoCurrentStep, "current-session search requires an active step boundary")
		}
		bound := int64(stepStart.Seq - 1)
		if seqRange.To == nil || *seqRange.To > bound {
			seqRange.To = &bound
		}
	}
	title, err := c.readTitle(ctx, from, sessionID)
	if err != nil {
		return "", err
	}
	// 上界被钉到下界之前是完全正常的（比如模型指定了一段全在当前步骤里的
	// seq）。这时候一条都不可能命中，直接排一份空结果，不去打扰引擎。
	if seqRange.From != nil && seqRange.To != nil && *seqRange.From > *seqRange.To {
		return formatEventSearch(sessionID, title, searchCollection[sessionquery.EventSearchHit]{}), nil
	}
	filters, err := buildEventFilters(eventFilterInput{
		seqFrom:    intBound(seqRange.From),
		seqTo:      intBound(seqRange.To),
		timeFrom:   args.TimeFrom,
		timeTo:     args.TimeTo,
		eventTypes: args.EventTypes,
		surfaces:   args.Surfaces,
	})
	if err != nil {
		return "", err
	}
	collected, err := collectPages(ctx, c.maxSearchResults,
		func(cursor sessionquery.SearchCursor) ([]sessionquery.EventSearchHit, sessionquery.SearchCursor, error) {
			page, err := call(ctx, c, "event search", func() (sessionquery.EventSearchPage, error) {
				return c.service.SearchEvents(ctx, sessionquery.EventSearchRequest{
					SessionID: sessionID,
					Query:     query,
					Filters:   filters,
					Cursor:    cursor,
				})
			})
			if err != nil {
				return nil, "", err
			}
			// 每一页都复核一次，不是只查第一页：翻页之间这个会话可能被挪出
			// 工作区，后面那些页就不该再交出去了。
			if err := assertObservedTargetAuthorized(from, sessionID, page.Session); err != nil {
				return nil, "", err
			}
			return page.Items, page.NextCursor, nil
		},
		func(sessionquery.EventSearchHit) bool { return true })
	if err != nil {
		return "", err
	}
	return formatEventSearch(sessionID, title, collected), nil
}

// executeSessionTrace 跑一次血统追溯。
//
// 源: packages/session-query/tool-session-query/src/operations.ts:168-198
func (c *Controller) executeSessionTrace(
	ctx context.Context,
	args sessionTargetArgs,
	exec *tools.RunContext,
) (string, error) {
	from, err := c.callerOf(exec)
	if err != nil {
		return "", err
	}
	sessionID := targetID(args.SessionID, from)
	if err := c.authorizeTarget(ctx, from, sessionID); err != nil {
		return "", err
	}
	trace, err := call(ctx, c, "session lineage trace", func() (sessionquery.LineageTrace, error) {
		return c.service.TraceSession(ctx, sessionID)
	})
	if err != nil {
		return "", err
	}
	if err := assertObservedTargetAuthorized(from, sessionID, trace.Target.Header); err != nil {
		return "", err
	}

	// 祖先链**从第一个越界的地方就断掉**，不是把越界的挑出来、把更远的留下：
	// 一条父链是有序的，跳过中间某一环再显示更上面的那些，画出来的血统是假的。
	var ancestors []sessionquery.Record
	ancestorBoundary := false
	for _, ancestor := range trace.Ancestors {
		if !recordAuthorized(ancestor, from) {
			ancestorBoundary = true
			break
		}
		ancestors = append(ancestors, ancestor)
	}
	// 还有第二种「往上还有」：引擎自己就没追到根（父 id 不在这份语料里）。
	// 只有在没被授权截断过的时候才轮到它——否则那个边界已经画出来了。
	if len(ancestors) == len(trace.Ancestors) && !trace.Complete {
		ancestorBoundary = true
	}
	descendants := authorizeDescendants(trace.Descendants, from)
	visibleIDs := make([]session.SessionID, 0, 1+len(ancestors))
	visibleIDs = append(visibleIDs, trace.Target.Header.ID)
	for _, record := range ancestors {
		visibleIDs = append(visibleIDs, record.Header.ID)
	}
	visibleIDs = append(visibleIDs, descendantIDs(descendants)...)
	titles, err := c.readTitles(ctx, from, visibleIDs)
	if err != nil {
		return "", err
	}
	return formatSessionTrace(trace, ancestors, ancestorBoundary, descendants, titles), nil
}

// executeEventTrace 跑一次事件关系追溯。
//
// 源: packages/session-query/tool-session-query/src/operations.ts:200-214
func (c *Controller) executeEventTrace(
	ctx context.Context,
	args eventTargetArgs,
	exec *tools.RunContext,
) (string, error) {
	// 参数检查排在认调用方之前：一个写错的 seq 和这次调用是谁发的没关系，
	// 先说出来省掉一趟授权。
	if err := assertNonNegative("seq", args.Seq); err != nil {
		return "", err
	}
	from, err := c.callerOf(exec)
	if err != nil {
		return "", err
	}
	sessionID := targetID(args.SessionID, from)
	if err := c.authorizeTarget(ctx, from, sessionID); err != nil {
		return "", err
	}
	trace, err := call(ctx, c, "event trace", func() (sessionquery.EventTraceObservation, error) {
		return c.service.TraceEvent(ctx, sessionquery.EventTraceRequest{SessionID: sessionID, Seq: args.Seq})
	})
	if err != nil {
		return "", err
	}
	if err := assertObservedTargetAuthorized(from, sessionID, trace.Session); err != nil {
		return "", err
	}
	title, err := c.readTitle(ctx, from, sessionID)
	if err != nil {
		return "", err
	}
	return formatEventTrace(sessionID, title, trace), nil
}

// executeEventRead 跑一次事件精读。
//
// 源: packages/session-query/tool-session-query/src/operations.ts:216-237
func (c *Controller) executeEventRead(
	ctx context.Context,
	args eventReadArgs,
	exec *tools.RunContext,
) (string, error) {
	if err := assertNonNegative("seq", args.Seq); err != nil {
		return "", err
	}
	if args.Before != nil {
		if err := assertNonNegative("before", *args.Before); err != nil {
			return "", err
		}
	}
	if args.After != nil {
		if err := assertNonNegative("after", *args.After); err != nil {
			return "", err
		}
	}
	from, err := c.callerOf(exec)
	if err != nil {
		return "", err
	}
	sessionID := targetID(args.SessionID, from)
	if err := c.authorizeTarget(ctx, from, sessionID); err != nil {
		return "", err
	}
	request := sessionquery.EventReadRequest{SessionID: sessionID, Seq: args.Seq}
	// 新增: DSH 靠展开一个可能为空的对象来表达「这个字段没给」。Go 侧
	// [sessionquery.EventReadRequest] 的两个窗口大小是普通的 int，零值就是
	// 引擎那边的默认，所以没给时不写、给了才写。
	if args.Before != nil {
		request.Before = *args.Before
	}
	if args.After != nil {
		request.After = *args.After
	}
	window, err := call(ctx, c, "event read", func() (sessionquery.EventWindow, error) {
		return c.service.ReadEvent(ctx, request)
	})
	if err != nil {
		return "", err
	}
	if err := assertObservedTargetAuthorized(from, sessionID, window.Session); err != nil {
		return "", err
	}
	title, err := c.readTitle(ctx, from, sessionID)
	if err != nil {
		return "", err
	}
	return formatEventRead(sessionID, title, window)
}

// collectPages 一路翻页收结果，收满上限就停。
//
// 源: packages/session-query/tool-session-query/src/operations.ts:239-272
//
// 上限是数**收下来的**那些，不是数引擎返回的那些：被 accept 筛掉的越界命中
// 不占额度，否则一个工作区外的大会话能把整页额度吃光，模型什么都看不到。
//
// 游标重复一律当引擎坏了报出去，不是默默停下：默默停下交出去的是一份看起来
// 完整、实际上少了一截的结果，模型没有任何办法察觉。
func collectPages[T any](
	ctx context.Context,
	maxResults int,
	request func(cursor sessionquery.SearchCursor) ([]T, sessionquery.SearchCursor, error),
	accept func(T) bool,
) (searchCollection[T], error) {
	var collected searchCollection[T]
	seen := map[sessionquery.SearchCursor]struct{}{}
	var cursor sessionquery.SearchCursor
	for {
		if err := ctx.Err(); err != nil {
			return searchCollection[T]{}, err
		}
		items, next, err := request(cursor)
		if err != nil {
			return searchCollection[T]{}, err
		}
		if err := ctx.Err(); err != nil {
			return searchCollection[T]{}, err
		}
		for _, item := range items {
			if !accept(item) {
				continue
			}
			if len(collected.items) == maxResults {
				collected.capped = true
				return collected, nil
			}
			collected.items = append(collected.items, item)
		}
		if next == "" {
			return collected, nil
		}
		if _, ok := seen[next]; ok {
			return searchCollection[T]{}, &sessionquery.Error{
				Code:    sessionquery.CodeInvalidCursor,
				Message: "session-search provider repeated a continuation cursor",
			}
		}
		seen[next] = struct{}{}
		cursor = next
	}
}

// lastStepStart 找调用方日志里最后一条 step/start。
//
// 源: packages/session-query/tool-session-query/src/operations.ts:128
//
// 从后往前找：要的是「当前这一步」，也就是最晚的那条。
func lastStepStart(events []session.Event) (session.Event, bool) {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Type == session.EventStepStart {
			return events[index], true
		}
	}
	return session.Event{}, false
}

// intBound 把一个 int64 范围端点换回参数那一侧的 int 指针。
//
// [sessionquery.Range] 用 int64 说毫秒，而 [eventFilterInput] 收的是模型写下的
// 那种 int。seq 这一侧两边表示的是同一个数，转换是无损的。
func intBound(value *int64) *int {
	if value == nil {
		return nil
	}
	bound := int(*value)
	return &bound
}
