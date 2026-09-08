// 本文件的作用：对账那一步——把介质里的索引追平已落地的会话，把活着的会话就地
// 折成文档，并算出这次观察的世代。
//
// 源: packages/session-query/session-query-sqlite/src/index.ts:395-482（_reconcile 那一路）

package searchstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"

	"github.com/snight1983/ds-harness-go/adapter/datastore"
	"github.com/snight1983/ds-harness-go/feature/persistence"
	"github.com/snight1983/ds-harness-go/feature/sessionquery"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// liveEntry 是一个活会话此刻折出来的那一份，只在一次查询里有效。
type liveEntry struct {
	header    sessionlog.SessionHeader
	documents []sessionquery.EventSearchDocument
	// fingerprint 是这些文档的内容指纹，拌进世代里。
	fingerprint string
}

// observation 是一次检索赖以成立的那份语料观察。
//
// 新增: DSH 那边这三样（persistenceBinding、persisted、live）也是一起取的，
// 但它的世代是进程内的自增计数器。这里换成内容指纹，理由见包文档。
type observation struct {
	// persisted 是已落地的那些会话，各带它的变更令牌。
	persisted map[sessionlog.SessionID]persistence.Snapshot
	// live 是此刻活着的那些会话，各带它就地折出来的文档。
	live map[sessionlog.SessionID]liveEntry
	// generation 是这份观察的世代，游标靠它判断续页还作不作数。
	generation string
}

// observe 取一次语料观察：先对账，再折活的，最后算世代。
func (s *Store) observe(ctx context.Context, scope sessionlog.SessionID) (observation, error) {
	seen := observation{
		persisted: make(map[sessionlog.SessionID]persistence.Snapshot),
		live:      make(map[sessionlog.SessionID]liveEntry),
	}
	if err := s.reconcile(ctx, &seen); err != nil {
		return observation{}, err
	}
	for _, source := range s.live.List() {
		id := source.Header.ID
		if scope != "" && id != scope {
			continue
		}
		documents, err := sessionquery.BuildEventSearchDocuments(id, source.Events)
		if err != nil {
			return observation{}, wrap(sessionquery.CodeIndexFailed, err, "折不出活会话 %q 的文档", id)
		}
		seen.live[id] = liveEntry{
			header:      source.Header,
			documents:   documents,
			fingerprint: fingerprintDocuments(documents),
		}
	}
	seen.generation = seen.computeGeneration()
	return seen, nil
}

// reconcile 把介质里的索引追平已落地的会话。
//
// 令牌对得上就跳过，对不上才重读、重折、整组换掉；索引里还留着、但已经不落地的
// 那些组整组丢掉。
//
// 一次重建里读日志和写文档不是同一个事务，所以两个副本可能同时重建同一组。那不
// 是问题：整组换掉在库里是原子的，而两边算出来的内容一样。这里那把锁只是省掉
// 本进程内白读一遍整份日志。
func (s *Store) reconcile(ctx context.Context, seen *observation) error {
	if s.store == nil {
		return nil
	}
	snapshots, err := s.store.ListSnapshots(ctx)
	if err != nil {
		if abort := abortedFailure(ctx, err); abort != nil {
			return abort
		}
		return wrap(sessionquery.CodePersistenceFailed, err, "列举落地会话失败")
	}
	for _, snapshot := range snapshots {
		seen.persisted[snapshot.Header.ID] = snapshot
	}

	s.syncing.Lock()
	defer s.syncing.Unlock()

	indexed, err := s.docs.Groups(ctx)
	if err != nil {
		if abort := abortedFailure(ctx, err); abort != nil {
			return abort
		}
		return wrap(sessionquery.CodeIndexFailed, err, "读不出会话检索的索引状态")
	}
	for _, snapshot := range snapshots {
		if err := checkAborted(ctx.Err()); err != nil {
			return err
		}
		id := snapshot.Header.ID
		if tag, ok := indexed[string(id)]; ok && tag == string(snapshot.Revision) {
			continue
		}
		if err := s.reindex(ctx, snapshot); err != nil {
			return err
		}
	}
	for group := range indexed {
		if _, ok := seen.persisted[sessionlog.SessionID(group)]; ok {
			continue
		}
		if _, err := s.docs.DropGroup(ctx, group); err != nil {
			if abort := abortedFailure(ctx, err); abort != nil {
				return abort
			}
			return wrap(sessionquery.CodeIndexFailed, err, "丢不掉会话 %q 的索引", group)
		}
	}
	return nil
}

// reindex 重建一个已落地会话的那一组文档。
//
// 读日志时撞上「这份存档坏了」不让整次检索失败：一份读不了的存档不该让另外几百
// 份搜不了。那一组被丢掉，于是它在这次以及之后的检索里都不出现，直到它被修好。
func (s *Store) reindex(ctx context.Context, snapshot persistence.Snapshot) error {
	id := snapshot.Header.ID
	loaded, err := s.store.Inspect(ctx, id)
	if err != nil {
		if abort := abortedFailure(ctx, err); abort != nil {
			return abort
		}
		var corruption *persistence.CorruptionError
		if errors.As(err, &corruption) {
			s.logger.Warn("落地的会话坏了，把它从检索索引里摘掉",
				"session", string(id), "error", err)
			if _, dropErr := s.docs.DropGroup(ctx, string(id)); dropErr != nil {
				if abort := abortedFailure(ctx, dropErr); abort != nil {
					return abort
				}
				return wrap(sessionquery.CodeIndexFailed, dropErr, "丢不掉会话 %q 的索引", id)
			}
			return nil
		}
		return wrap(sessionquery.CodePersistenceFailed, err, "读取会话 %q 失败", id)
	}
	documents, err := sessionquery.BuildEventSearchDocuments(id, loaded.Events)
	if err != nil {
		return wrap(sessionquery.CodeIndexFailed, err, "折不出会话 %q 的文档", id)
	}
	docs := make([]datastore.Doc, 0, len(documents))
	for _, document := range documents {
		docs = append(docs, toDoc(document))
	}
	// 印记盖的就是这次索引依据的那个令牌：下次对账拿它和新令牌比，相等就跳过。
	if err := s.docs.Replace(ctx, string(id), string(snapshot.Revision), docs); err != nil {
		if abort := abortedFailure(ctx, err); abort != nil {
			return abort
		}
		return wrap(sessionquery.CodeIndexFailed, err, "写不进会话 %q 的索引", id)
	}
	return nil
}

// toDoc 把一条语义文档折成介质认得的那五个字段。
//
// 新增: [datastore] 那边不认识「会话」「事件」这些词（见它的包文档），所以这次
// 翻译必须在这一侧做。对应关系是：会话 → 分组，序号 → Seq，时刻 → At，
// 事件类型 → Kind，表面位置 → Facet，语义文字 → Body。
func toDoc(document sessionquery.EventSearchDocument) datastore.Doc {
	return datastore.Doc{
		Seq:   int64(document.Seq),
		At:    document.Time,
		Kind:  string(document.Type),
		Facet: string(document.Surface),
		Body:  document.Text,
	}
}

// fromRow 把介质交回来的一条命中折回一条语义文档。
func fromRow(row datastore.MatchRow) sessionquery.EventSearchDocument {
	return sessionquery.EventSearchDocument{
		EventRecord: sessionquery.EventRecord{
			SessionID: sessionlog.SessionID(row.Group),
			Seq:       int(row.Seq),
			Type:      sessionlog.EventType(row.Kind),
			Time:      row.At,
			Surface:   sessionquery.EventSurface(row.Facet),
		},
		Text: row.Body,
	}
}

// computeGeneration 把这份观察压成一个所有副本都算得出的世代。
//
// 新增: 顺序必须定死，否则同一份语料在两次遍历里会哈出两个数——map 的遍历顺序
// 在 Go 里是随机的，那种世代每查一次都过期一次。
func (o observation) computeGeneration() string {
	digest := sha256.New()
	for _, id := range sortedKeys(o.persisted) {
		fmt.Fprintf(digest, "p\x00%s\x00%s\x00", id, o.persisted[id].Revision)
	}
	for _, id := range sortedKeys(o.live) {
		fmt.Fprintf(digest, "l\x00%s\x00%s\x00", id, o.live[id].fingerprint)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// fingerprintDocuments 给一批就地折出来的文档算一个内容指纹。
//
// 活会话没有变更令牌可用——它压根没落地。所以这里拿内容本身当令牌：文档的每个
// 字段都进哈希，会话每多说一句话，指纹就变一次，游标跟着过期一次。那正是要的：
// 一边翻页一边还在往下说，翻出来的两页本来就拼不成一个完整的答案。
func fingerprintDocuments(documents []sessionquery.EventSearchDocument) string {
	digest := sha256.New()
	for _, document := range documents {
		fmt.Fprintf(digest, "%d\x00%d\x00%s\x00%s\x00%s\x00",
			document.Seq, document.Time, document.Type, document.Surface, document.Text)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// sortedKeys 按会话 id 排出一份确定的遍历顺序。
func sortedKeys[V any](values map[sessionlog.SessionID]V) []sessionlog.SessionID {
	keys := make([]sessionlog.SessionID, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// checkAborted 把 ctx 的取消翻成本包报得出的那个码。
func checkAborted(err error) error {
	if err == nil {
		return nil
	}
	return wrap(sessionquery.CodeAborted, err, "会话检索被取消")
}

// abortedFailure 判断一次后端失败该不该算成取消；不是就返回 nil。
//
// 两条都算：ctx 已经取消，或者后端把取消原样报了上来（它自己内部的超时也走这条）。
func abortedFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return checkAborted(ctx.Err())
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, sessionquery.CodeAborted) {
		return checkAborted(err)
	}
	return nil
}
