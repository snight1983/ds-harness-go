// 本文件的作用：把一份原始日志投影成轻量事件记录、以及可检索的语义文档。
//
// 源: packages/session-query/session-query/src/documents.ts

package sessionquery

import (
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// BuildEventRecords 把一份原始日志投影成带表面位置的轻量记录。
//
// 源: packages/session-query/session-query/src/documents.ts:15-27
//
// 一条事件一条记录，按 seq 升序。日志必须是完整连续的：表面位置是**整份日志**
// 折出来的，喂一段中间的碎片会得到一份看起来正常、实际全错的分类。
func BuildEventRecords(id sessionlog.SessionID, events []sessionlog.Event) ([]EventRecord, error) {
	surfaces, err := classifySurface(events)
	if err != nil {
		return nil, err
	}
	records := make([]EventRecord, 0, len(events))
	for _, event := range events {
		records = append(records, EventRecord{
			SessionID: id,
			Seq:       event.Seq,
			Type:      event.Type,
			Time:      event.Time,
			Surface:   surfaceOf(surfaces, event.Seq),
		})
	}
	return records, nil
}

// BuildEventSearchDocuments 为一份完整原始日志建出第一方语义文档。
//
// 源: packages/session-query/session-query/src/documents.ts:29-53
//
// 按 seq 升序；提不出文字的结构性事件不产出文档——一条空文档在任何查询下
// 都不会命中，留着只会让「总共几条」这个数字骗人。
func BuildEventSearchDocuments(id sessionlog.SessionID, events []sessionlog.Event) ([]EventSearchDocument, error) {
	surfaces, err := classifySurface(events)
	if err != nil {
		return nil, err
	}
	var documents []EventSearchDocument
	for _, event := range events {
		text, err := ExtractEventText(event)
		if err != nil {
			return nil, err
		}
		if text == "" {
			continue
		}
		documents = append(documents, EventSearchDocument{
			EventRecord: EventRecord{
				SessionID: id,
				Seq:       event.Seq,
				Type:      event.Type,
				Time:      event.Time,
				Surface:   surfaceOf(surfaces, event.Seq),
			},
			Text: text,
		})
	}
	return documents, nil
}

// classifySurface 折一次表面，给出每条上过表面的事件此刻在哪。
//
// 源: packages/session-query/session-query/src/documents.ts:55-73
//
// 只收录上过表面的那些 seq。没被收录的就是从来没上过表面的，由
// [surfaceOf] 补成 [SurfaceLogOnly]。
func classifySurface(events []sessionlog.Event) (map[int]EventSurface, error) {
	folded, err := sessionlog.FoldSurface(events, sessionlog.LogBaseSeq(events))
	if err != nil {
		return nil, wrap(CodeInvalidSurface, err, "会话表面折不出来")
	}
	surfaces := make(map[int]EventSurface, len(folded.Nodes))
	for _, seq := range folded.Nodes {
		surfaces[seq] = SurfaceCurrent
	}
	for _, replacement := range folded.Replacements {
		for _, seq := range replacement.ShadowedSeqs {
			surfaces[seq] = SurfaceShadowed
		}
	}
	return surfaces, nil
}

// surfaceOf 查一条事件的表面位置，查不到就是从没上过表面。
func surfaceOf(surfaces map[int]EventSurface, seq int) EventSurface {
	if surface, ok := surfaces[seq]; ok {
		return surface
	}
	return SurfaceLogOnly
}
