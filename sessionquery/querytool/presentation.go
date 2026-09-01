// 本文件的作用：五件工具交出去的那段文本，以及它们在界面上的卡片。
//
// 源: packages/session-query/tool-session-query/src/presentation.ts
//
// 这些字符串是**模型契约**的一部分，一个字都不许改译：模型是照着这些行的形状
// 去认 seq、认会话 id、决定下一步调哪件工具的。中文只在注释里。

package querytool

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/sessionquery"
)

// searchCollection 是一次翻页收集下来的结果。
//
// 源: packages/session-query/tool-session-query/src/operations.ts:48-51
type searchCollection[T any] struct {
	// items 是收下来的那些。
	items []T
	// capped 表示还有更多，是被上限截住的。
	capped bool
}

// capNotice 是撞上结果上限时那句话。
//
// 源: packages/session-query/tool-session-query/src/presentation.ts:76
const capNotice = "Result cap reached. Narrow the query or add filters to find additional matches."

// formatSessionSearch 排一次跨会话检索的结果。
//
// 源: packages/session-query/tool-session-query/src/presentation.ts:243-255（presentation）
func formatSessionSearch(
	collected searchCollection[sessionquery.SearchHit],
	titles map[session.SessionID]titleView,
	authorizedParents map[session.SessionID]struct{},
) string {
	if len(collected.items) == 0 {
		return formatEmptySessionSearch()
	}
	lines := []string{fmt.Sprintf("Session search results (%d):", len(collected.items))}
	for index, hit := range collected.items {
		// 父会话有三种说法：没有父亲、父亲在工作区内（报 id）、父亲在工作区外。
		// 第三种必须说出来而不是当成没有父亲：模型据此知道这条血统还有上文，
		// 只是它看不到。
		parent := "root"
		if hit.Header.ParentSession != "" {
			parent = "[outside workspace]"
			if _, ok := authorizedParents[hit.Header.ParentSession]; ok {
				parent = string(hit.Header.ParentSession)
			}
		}
		lines = append(lines,
			"",
			fmt.Sprintf("%d. Session %s — %s", index+1, hit.Header.ID, titleText(titles[hit.Header.ID])),
			"   Created: "+formatTime(hit.Header.CreatedAt),
			"   Parent: "+parent,
			"   Availability: "+availabilityText(hit.Record),
			fmt.Sprintf("   Best match: seq %d | %s | %s | %s",
				hit.BestMatch.Seq, hit.BestMatch.Type, hit.BestMatch.Surface, formatTime(hit.BestMatch.Time)),
			"   Snippet: "+hit.BestMatch.Snippet,
		)
	}
	if collected.capped {
		lines = append(lines, "", capNotice)
	}
	return strings.Join(lines, "\n")
}

// formatEmptySessionSearch 是一次什么都没找到的跨会话检索。
//
// 源: packages/session-query/tool-session-query/src/presentation.ts:81-83
func formatEmptySessionSearch() string {
	return "No prior session matches found."
}

// formatEventSearch 排一次会话内检索的结果。
//
// 源: packages/session-query/tool-session-query/src/presentation.ts:85-107
func formatEventSearch(
	sessionID session.SessionID,
	title titleView,
	collected searchCollection[sessionquery.EventSearchHit],
) string {
	lines := []string{fmt.Sprintf("Session %s — %s", sessionID, titleText(title))}
	if len(collected.items) == 0 {
		lines = append(lines, "", "No prior event matches found.")
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "", fmt.Sprintf("Event search results (%d):", len(collected.items)))
	for index, hit := range collected.items {
		lines = append(lines,
			fmt.Sprintf("%d. seq %d | %s | %s | %s", index+1, hit.Seq, hit.Type, hit.Surface, formatTime(hit.Time)),
			"   Snippet: "+hit.Snippet,
		)
	}
	if collected.capped {
		lines = append(lines, "", capNotice)
	}
	return strings.Join(lines, "\n")
}

// formatSessionTrace 排一次血统追溯的结果。
//
// 源: packages/session-query/tool-session-query/src/presentation.ts:109-133
//
// ancestorBoundary 是那条「往上还有，但你看不见」：它和「这就是根」在输出里
// 必须分开，否则模型会把一条被截断的血统当成完整的。
func formatSessionTrace(
	trace sessionquery.LineageTrace,
	ancestors []sessionquery.Record,
	ancestorBoundary bool,
	descendants []*authorizedDescendant,
	titles map[session.SessionID]titleView,
) string {
	lines := []string{
		fmt.Sprintf("Session %s — %s", trace.Target.Header.ID, titleText(titles[trace.Target.Header.ID])),
		"Created: " + formatTime(trace.Target.Header.CreatedAt),
		"Availability: " + availabilityText(trace.Target),
		"",
		"Ancestors (nearest first):",
	}
	if len(ancestors) == 0 && !ancestorBoundary {
		lines = append(lines, "- none (target is a root session)")
	}
	for _, record := range ancestors {
		lines = append(lines, fmt.Sprintf("- %s — %s | %s | %s",
			record.Header.ID, titleText(titles[record.Header.ID]),
			formatTime(record.Header.CreatedAt), availabilityText(record)))
	}
	if ancestorBoundary {
		lines = append(lines, "- [outside workspace boundary]")
	}
	lines = append(lines, "", "Descendants:")
	if len(descendants) == 0 {
		lines = append(lines, "- none")
	} else {
		lines = append(lines, renderDescendants(descendants, titles)...)
	}
	return strings.Join(lines, "\n")
}

// renderDescendants 把裁过的后代树排成缩进的几行。
//
// 源: packages/session-query/tool-session-query/src/presentation.ts:135-149
func renderDescendants(nodes []*authorizedDescendant, titles map[session.SessionID]titleView) []string {
	var lines []string
	for _, visit := range visitDescendants(nodes) {
		indent := strings.Repeat("  ", visit.depth)
		if visit.node == nil {
			lines = append(lines, indent+"- [outside workspace subtree]")
			continue
		}
		id := visit.node.record.Header.ID
		lines = append(lines, fmt.Sprintf("%s- %s — %s | %s | %s",
			indent, id, titleText(titles[id]),
			formatTime(visit.node.record.Header.CreatedAt), availabilityText(visit.node.record)))
	}
	return lines
}

// formatEventTrace 排一次事件关系追溯的结果。
//
// 源: packages/session-query/tool-session-query/src/presentation.ts:151-165
func formatEventTrace(sessionID session.SessionID, title titleView, trace sessionquery.EventTraceObservation) string {
	replacedBy := "none"
	if trace.Shadowed {
		replacedBy = strconv.Itoa(trace.ReplacedBy)
	}
	return strings.Join([]string{
		fmt.Sprintf("Session %s — %s", sessionID, titleText(title)),
		fmt.Sprintf("Target: seq %d | %s | %s | %s",
			trace.Target.Seq, trace.Target.Type, trace.Target.Surface, formatTime(trace.Target.Time)),
		"Replaced by: " + replacedBy,
		"Replacement chain: " + seqList(trace.ReplacementChain),
		"Events replaced by target: " + seqList(trace.ReplacedEventSeqs),
		"Events cited directly as sources: " + seqList(trace.SourceEventSeqs),
		"Direct derived events: " + seqList(trace.DerivedEventSeqs),
	}, "\n")
}

// formatEventRead 排一次事件精读的结果。
//
// 源: packages/session-query/tool-session-query/src/presentation.ts:167-192
//
// 目标那条是**原样**的 JSON，缩进两格：这件工具存在的全部理由就是让模型看见
// 一条事件的完整字节，任何摘要都会把它想找的那个字段摘掉。
func formatEventRead(sessionID session.SessionID, title titleView, window sessionquery.EventWindow) (string, error) {
	encoded, err := json.MarshalIndent(window.Target, "", "  ")
	if err != nil {
		return "", err
	}
	lines := []string{
		fmt.Sprintf("Session %s — %s", sessionID, titleText(title)),
		fmt.Sprintf("Target event seq %d:", window.Target.Seq),
		"```json",
		string(encoded),
		"```",
	}
	var before, after []session.Event
	for _, event := range window.Events {
		switch {
		case event.Seq < window.Target.Seq:
			before = append(before, event)
		case event.Seq > window.Target.Seq:
			after = append(after, event)
		}
	}
	for _, section := range []struct {
		heading string
		events  []session.Event
	}{{"Before:", before}, {"After:", after}} {
		if len(section.events) == 0 {
			continue
		}
		lines = append(lines, "", section.heading)
		for _, event := range section.events {
			line, err := formatNeighbor(event)
			if err != nil {
				return "", err
			}
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n"), nil
}

// formatNeighbor 排目标前后那一圈里的一条。
//
// 源: packages/session-query/tool-session-query/src/presentation.ts:194-199
//
// 新增: DSH 的 extractSessionEventText 不会失败——那边负载已经是解好的对象。
// Go 侧 [sessionquery.ExtractEventText] 会失败，因为负载是原始字节，解不回来就是
// 日志坏了。这里把它**抛出去**而不是降级成 "(no semantic text)"：那两件事在模型
// 眼里完全不同，一条坏掉的事件被画成「这条没有文字」会让它以为自己已经看全了。
// 这个取舍和 [github.com/snight1983/ds-harness-go/sessionquery] 的 doc.go 第 6 条是同一个。
func formatNeighbor(event session.Event) (string, error) {
	text, err := sessionquery.ExtractEventText(event)
	if err != nil {
		return "", err
	}
	head := fmt.Sprintf("- seq %d | %s | %s", event.Seq, event.Type, formatTime(event.Time))
	if text == "" {
		return head + " | (no semantic text)", nil
	}
	// 正文缩进两格，包括中间每一个换行：这一段可能有好几行，不缩进的话它读起来
	// 就和下一条邻居的那一行分不开。
	return head + "\n  " + strings.ReplaceAll(text, "\n", "\n  "), nil
}

// availabilityText 说一个会话此刻在哪几处。
//
// 源: packages/session-query/tool-session-query/src/presentation.ts:201-208
//
// 两处都没有说明这份记录是从一次已经过时的列举里来的；那种情形要说出来，
// 一个空串会让这一行看起来像是排版出了问题。
func availabilityText(record sessionquery.Record) string {
	var parts []string
	if record.Live {
		parts = append(parts, "live")
	}
	if record.Persisted {
		parts = append(parts, "persisted")
	}
	if len(parts) == 0 {
		return "unavailable"
	}
	return strings.Join(parts, ", ")
}

// seqList 把一串 seq 排成一行。
//
// 源: packages/session-query/tool-session-query/src/presentation.ts:210-212
func seqList(values []int) string {
	if len(values) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.Itoa(value))
	}
	return strings.Join(parts, ", ")
}

// formatTime 把一个纪元毫秒排成 ISO 8601。
//
// 源: packages/session-query/tool-session-query/src/presentation.ts:214-216
//
// 新增: 对齐 JS 的 Date.prototype.toISOString——UTC、恰好三位小数、结尾 Z。
// 换成 time.RFC3339Nano 会在整秒时把小数整个省掉，模型看见的时间戳位数就不齐了。
func formatTime(value int64) string {
	return time.UnixMilli(value).UTC().Format("2006-01-02T15:04:05.000Z")
}

// presentSessionSearchCall 是 session_search 在界面上的卡片。
//
// 源: packages/session-query/tool-session-query/src/presentation.ts:218-220
func presentSessionSearchCall(args json.RawMessage) tools.CallView {
	var decoded sessionSearchArgs
	_ = json.Unmarshal(args, &decoded)
	return tools.GenericCallView{Kind: tools.CallSearch, Title: "Search prior sessions", RawInput: rawText(decoded.Query)}
}

// presentEventSearchCall 是 session_event_search 在界面上的卡片。
//
// 源: packages/session-query/tool-session-query/src/presentation.ts:222-224
func presentEventSearchCall(args json.RawMessage) tools.CallView {
	var decoded eventSearchArgs
	_ = json.Unmarshal(args, &decoded)
	return tools.GenericCallView{Kind: tools.CallSearch, Title: "Search session events", RawInput: rawText(decoded.Query)}
}

// presentSessionTraceCall 是 session_trace 在界面上的卡片。
//
// 源: packages/session-query/tool-session-query/src/presentation.ts:226-234
func presentSessionTraceCall(args json.RawMessage) tools.CallView {
	var decoded sessionTargetArgs
	_ = json.Unmarshal(args, &decoded)
	if decoded.SessionID == "" {
		return tools.GenericCallView{Kind: tools.CallRead, Title: "Trace current session"}
	}
	return tools.GenericCallView{
		Kind:     tools.CallRead,
		Title:    "Trace session " + decoded.SessionID,
		RawInput: rawText(decoded.SessionID),
	}
}

// presentEventTargetCall 是那两件指着一条事件的工具在界面上的卡片。
//
// 源: packages/session-query/tool-session-query/src/presentation.ts:236-250
func presentEventTargetCall(action string, args json.RawMessage) tools.CallView {
	var decoded eventTargetArgs
	_ = json.Unmarshal(args, &decoded)
	view := tools.GenericCallView{Kind: tools.CallRead, Title: fmt.Sprintf("%s %d", action, decoded.Seq)}
	payload := map[string]any{"seq": decoded.Seq}
	if decoded.SessionID != "" {
		payload["session_id"] = decoded.SessionID
	}
	if encoded, err := json.Marshal(payload); err == nil {
		view.RawInput = encoded
	}
	return view
}

// rawText 把一段文字排成 [tools.GenericCallView.RawInput] 要的那种字节。
//
// 新增: DSH 那边 rawInput 的类型是 unknown，一个裸字符串直接就能放。Go 侧它是
// json.RawMessage，所以要先排一遍。排不出去时留空，一张标题还在的卡片总比
// 一次呈现失败强——呈现是纯函数，不许失败。
func rawText(text string) json.RawMessage {
	encoded, err := json.Marshal(text)
	if err != nil {
		return nil
	}
	return encoded
}
