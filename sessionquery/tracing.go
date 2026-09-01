// 本文件的作用：一次性的血统追溯与事件关系追溯。
//
// 源: packages/session-query/session-query/src/tracing.ts

package sessionquery

import (
	"slices"
	"strings"

	"github.com/snight1983/ds-harness-go/session"
)

// eventLogAnalysis 是一份日志折一次表面之后，追溯要用的那几张表。
//
// 源: packages/session-query/session-query/src/tracing.ts:11-16
//
// 折一次、三处共用：表面折叠是整份日志的重放，三个追溯函数各折一遍
// 既慢又可能给出互相矛盾的答案。
type eventLogAnalysis struct {
	records           []EventRecord
	replacedBy        map[int]int
	replacedEventSeqs map[int][]int
	currentSeqs       []int
}

// EventRecords 折一次表面，把一份原始日志分类成轻量记录。
//
// 源: packages/session-query/session-query/src/tracing.ts:18-33
func EventRecords(id session.SessionID, events []session.Event) ([]EventRecord, error) {
	analysis, err := analyzeEventLog(id, events)
	if err != nil {
		return nil, err
	}
	return analysis.records, nil
}

// CurrentSurfaceEvents 验完整份日志之后，交出当前的模型表面。
//
// 源: packages/session-query/session-query/src/tracing.ts:34-56（currentSurfaceEvents）
//
// 交出去的是脱离的副本：调用方拿到之后怎么改都碰不到语料里那一份。
func CurrentSurfaceEvents(id session.SessionID, events []session.Event) ([]session.Event, error) {
	analysis, err := analyzeEventLog(id, events)
	if err != nil {
		return nil, err
	}
	surface := make([]session.Event, 0, len(analysis.currentSeqs))
	for _, seq := range analysis.currentSeqs {
		event, ok := eventAtSeq(events, seq)
		if !ok || !session.IsSurfaceEvent(event) {
			// 走不到：analyzeEventLog 已经验过 seq 连续，FoldSurface 交出来的
			// 节点也只可能是表面事件。留着是因为下标寻址这件事一旦哪天
			// 换了实现（比如允许稀疏日志），这里是唯一会先炸的地方。
			return nil, fail(CodeInvalidSurface, "会话 %q 的表面节点 %d 不是一条表面事件", id, seq)
		}
		surface = append(surface, event.Clone())
	}
	return surface, nil
}

// TraceEvent 折一次表面并验完整份日志之后，追溯一条目标事件的直接关系。
//
// 源: packages/session-query/session-query/src/tracing.ts:58-105（traceEvent）
func TraceEvent(id session.SessionID, events []session.Event, seq int) (EventTrace, error) {
	target, ok := eventAtSeq(events, seq)
	if !ok {
		return EventTrace{}, fail(CodeEventNotFound, "会话 %q 里没有 seq 为 %d 的事件", id, seq)
	}
	analysis, err := analyzeEventLog(id, events)
	if err != nil {
		return EventTrace{}, err
	}

	// 替换链：从直接盖住目标的那条，一路往后走到最终那条。
	// 表面折叠保证每条事件最多被盖一次，所以这条链不会成环。
	var replacementChain []int
	for current, ok := analysis.replacedBy[seq]; ok; current, ok = analysis.replacedBy[current] {
		replacementChain = append(replacementChain, current)
	}

	// 派生：更晚的、把目标列进自己来源清单的那些事件，按日志顺序。
	var derived []int
	for _, event := range events {
		if event.Seq <= seq {
			continue
		}
		if slices.Contains(event.SourceEventSeqs, seq) {
			derived = append(derived, event.Seq)
		}
	}

	replacedBy, shadowed := analysis.replacedBy[seq]
	return EventTrace{
		Target:            analysis.records[seq],
		ReplacedBy:        replacedBy,
		Shadowed:          shadowed,
		ReplacementChain:  replacementChain,
		ReplacedEventSeqs: slices.Clone(analysis.replacedEventSeqs[seq]),
		SourceEventSeqs:   slices.Clone(target.SourceEventSeqs),
		DerivedEventSeqs:  derived,
	}, nil
}

// TraceSession 追溯一个会话已知的祖先与后代。
//
// 源: packages/session-query/session-query/src/tracing.ts:113-172
//
// 语料是一次观察里的全部逻辑会话。父链走到语料外面就停，并把那个停下的
// 父 id 说出来（[LineageTrace.Complete] 为假）——「查不到」和「到顶了」
// 是两件事，混成一件会让调用方把一棵被截断的树当成完整的。
func TraceSession(records []Record, id session.SessionID) (LineageTrace, error) {
	byID := make(map[session.SessionID]Record, len(records))
	for _, record := range records {
		byID[record.Header.ID] = record
	}
	target, ok := byID[id]
	if !ok {
		return LineageTrace{}, notFound(string(id))
	}

	ancestors, unresolved, err := traceAncestors(byID, target)
	if err != nil {
		return LineageTrace{}, err
	}

	childrenByParent := groupChildren(records)
	trace := LineageTrace{
		Target:      target,
		Ancestors:   ancestors,
		Descendants: buildDescendants(childrenByParent, id, map[session.SessionID]bool{id: true}),
	}
	if unresolved != "" {
		trace.Complete = false
		trace.UnresolvedParentID = unresolved
		return trace, nil
	}
	trace.Complete = true
	trace.Root = target
	if len(ancestors) > 0 {
		trace.Root = ancestors[len(ancestors)-1]
	}
	return trace, nil
}

// traceAncestors 从直接父亲往外走，直到走出语料或者到顶。
//
// 源: packages/session-query/session-query/src/tracing.ts:128-146
//
// 第二个返回值非空表示这条链走出了语料，值是那个停下的父 id。
func traceAncestors(byID map[session.SessionID]Record, target Record) ([]Record, session.SessionID, error) {
	var ancestors []Record
	seen := map[session.SessionID]bool{target.Header.ID: true}
	for parentID := target.Header.ParentSession; parentID != ""; {
		if seen[parentID] {
			return nil, "", fail(CodeInvalidLineage, "会话血统在 %q 处成环", parentID)
		}
		seen[parentID] = true
		parent, ok := byID[parentID]
		if !ok {
			return ancestors, parentID, nil
		}
		ancestors = append(ancestors, parent)
		parentID = parent.Header.ParentSession
	}
	return ancestors, "", nil
}

// groupChildren 把语料按父会话分组，组内按「建得早的在前，同刻按 id」排。
//
// 源: packages/session-query/session-query/src/tracing.ts:148-156
//
// 排序不是审美：追溯结果会进对外协议，一个随语料枚举顺序漂移的兄弟顺序
// 会让同一份数据的两次查询给出不同的字节。
func groupChildren(records []Record) map[session.SessionID][]Record {
	childrenByParent := make(map[session.SessionID][]Record)
	for _, record := range records {
		parent := record.Header.ParentSession
		if parent == "" {
			continue
		}
		childrenByParent[parent] = append(childrenByParent[parent], record)
	}
	for _, children := range childrenByParent {
		slices.SortFunc(children, func(a, b Record) int {
			if a.Header.CreatedAt != b.Header.CreatedAt {
				return int(min(max(a.Header.CreatedAt-b.Header.CreatedAt, -1), 1))
			}
			return strings.Compare(string(a.Header.ID), string(b.Header.ID))
		})
	}
	return childrenByParent
}

// buildDescendants 递归展开一个会话底下完整的已知后代树。
//
// 源: packages/session-query/session-query/src/tracing.ts:216-241
//
// 新增: DSH 那边用一个显式的栈把递归摊平，理由是 JS 的调用栈浅。Go 的栈是
// 按需增长的，所以这里直接递归——同样的行为，少一层手写的栈机器。
//
// seen 是防环的：一份语料里 A 的父亲是 B、B 的父亲是 A 这种事只能是存储被写坏了，
// 而 DSH 的 buildDescendants 在这种输入上会**无限循环**（它只在祖先那一侧
// 防了环）。这里往下走的时候也带上已访问集合，遇到环就剪掉那一枝，
// 不把一个坏掉的存储变成一次挂死。
func buildDescendants(
	childrenByParent map[session.SessionID][]Record,
	id session.SessionID,
	seen map[session.SessionID]bool,
) []LineageNode {
	children := childrenByParent[id]
	if len(children) == 0 {
		return nil
	}
	nodes := make([]LineageNode, 0, len(children))
	for _, child := range children {
		childID := child.Header.ID
		if seen[childID] {
			continue
		}
		seen[childID] = true
		nodes = append(nodes, LineageNode{
			Session:     child,
			Descendants: buildDescendants(childrenByParent, childID, seen),
		})
	}
	return nodes
}

// analyzeEventLog 折一次表面，产出追溯要用的那几张表。
//
// 源: packages/session-query/session-query/src/tracing.ts:174-214
func analyzeEventLog(id session.SessionID, events []session.Event) (eventLogAnalysis, error) {
	folded, err := session.FoldSurface(events)
	if err != nil {
		return eventLogAnalysis{}, wrap(CodeInvalidSurface, err, "会话 %q 的表面折不出来", id)
	}
	current := make(map[int]bool, len(folded.Nodes))
	for _, seq := range folded.Nodes {
		current[seq] = true
	}
	replacedBy := make(map[int]int)
	replacedEventSeqs := make(map[int][]int, len(folded.Replacements))
	for _, replacement := range folded.Replacements {
		replacedEventSeqs[replacement.Seq] = replacement.ShadowedSeqs
		for _, shadowed := range replacement.ShadowedSeqs {
			replacedBy[shadowed] = replacement.Seq
		}
	}
	records := make([]EventRecord, 0, len(events))
	for _, event := range events {
		surface := SurfaceLogOnly
		switch {
		case current[event.Seq]:
			surface = SurfaceCurrent
		case func() bool { _, ok := replacedBy[event.Seq]; return ok }():
			surface = SurfaceShadowed
		}
		records = append(records, EventRecord{
			SessionID: id,
			Seq:       event.Seq,
			Type:      event.Type,
			Time:      event.Time,
			Surface:   surface,
		})
	}
	return eventLogAnalysis{
		records:           records,
		replacedBy:        replacedBy,
		replacedEventSeqs: replacedEventSeqs,
		currentSeqs:       folded.Nodes,
	}, nil
}

// eventAtSeq 按 seq 取一条事件。
//
// 源: packages/session-query/session-query/src/tracing.ts:66-72（`events[seq]` 加一次 seq 校验）
//
// DSH 直接拿 seq 当下标，再验一次 `event.seq === seq`——它靠的是「日志从 0 开始
// 连续」这条契约，那次校验就是在防契约被破坏。这里保留同样的做法和同样的校验。
func eventAtSeq(events []session.Event, seq int) (session.Event, bool) {
	if seq < 0 || seq >= len(events) {
		return session.Event{}, false
	}
	event := events[seq]
	if event.Seq != seq {
		return session.Event{}, false
	}
	return event, true
}
