// 本文件的作用：标题那层薄壳——把「一个会话此刻叫什么」从日志里折出来。
//
// 源: packages/session-query/session-query/src/index.ts:167-215
//
// 这三个方法是 [ProjectMany] 加 [sessiontitle.FoldSnapshot] 的一次拼接，本身
// 一行读取逻辑都没有。它们之所以落在本包而不是 session-title 包，是因为 DSH 就把
// 它们挂在引擎上：调用方要的是「跨活会话和落地日志，按一批 id 一次观察出标题」，
// 那件事只有语料这一侧做得到；标题怎么折则完全归 session-title。
//
// 新增: doc.go 第 9 条曾把这层壳记为「本期不带」——那时 session-title 还没移植。
// 现在它在了，这层壳随之补上，[context/sessionref.TitleReader] 那个由装配方
// 满足的窄口子也因此有了现成的实现。

package sessionquery

import (
	"context"

	"ds-harness-go/session"
	"ds-harness-go/session/sessiontitle"
)

// TitleObservation 是一个会话此刻的标题，连同折它用的那份头。
//
// 源: packages/session-query/session-query/src/types.ts:152-158
//
// 头和标题是**同一次**观察里出来的：一个会话在两次读之间可能被改名，分两次读
// 会让调用方拿到一份头配另一份日志的标题。
type TitleObservation struct {
	// Session 是折这次标题用的那份会话头的副本。
	Session session.SessionHeader
	// Title 是日志上最新那条标题；Titled 为假时它没有意义。
	Title sessiontitle.Snapshot
	// Titled 说这个会话有没有过标题。
	//
	// 新增: DSH 那边 title 是可选字段，「没有标题」由它缺席表达。Go 这边
	// [sessiontitle.FoldSnapshot] 已经用第二个返回值表达同一件事，照抄它，
	// 而不是换成一个 *Snapshot——一个空标题和一个没有标题在界面上是两件事，
	// 用零值表达前者会把它们混起来。
	Titled bool
}

// ReadTitleSnapshots 在一次可取消的语料观察里，折出这批互不重复的会话的标题。
//
// 源: packages/session-query/session-query/src/index.ts:195-215
//
// 顺序、失败隔离、取消这三件事的语义全部由 [ProjectMany] 给：结果按 ids 去重后的
// 首次出现顺序排，单个会话的失败落在它自己那条结果的 Err 上，只有取消让整个调用失败。
func (e *Engine) ReadTitleSnapshots(
	ctx context.Context,
	ids []session.SessionID,
) ([]ProjectionResult[TitleObservation], error) {
	return ProjectMany(ctx, e.Corpus(), ids, func(source LogicalSource) (TitleObservation, error) {
		// 一条读不回来的标题事件让**这一个**会话的投影失败，而不是被跳过去露出
		// 更早那个标题，理由见 [sessiontitle.FoldSnapshot]。失败隔离在这条结果上，
		// 同一批里其余会话照常出结果。
		title, titled, err := sessiontitle.FoldSnapshot(source.Events)
		if err != nil {
			return TitleObservation{}, err
		}
		// 头是值类型，赋值就是副本；DSH 那边的 structuredClone 在这里不需要。
		return TitleObservation{Session: source.Header, Title: title, Titled: titled}, nil
	})
}

// ReadTitleSnapshot 折出一个会话的标题，连同折它用的那份头。
//
// 源: packages/session-query/session-query/src/index.ts:180-193
func (e *Engine) ReadTitleSnapshot(ctx context.Context, id session.SessionID) (TitleObservation, error) {
	results, err := e.ReadTitleSnapshots(ctx, []session.SessionID{id})
	if err != nil {
		return TitleObservation{}, err
	}
	if len(results) == 0 {
		// 走不到：一个 id 去重之后还是一个。留着是因为下一行要取下标。
		return TitleObservation{}, notFound(string(id))
	}
	if results[0].Err != nil {
		// 单条读法上，那条被隔离的失败就是这次调用的失败。
		return TitleObservation{}, results[0].Err
	}
	return results[0].Value, nil
}

// ReadTitle 折出一个会话此刻的标题；第二个返回值为假表示它还没有过标题。
//
// 源: packages/session-query/session-query/src/index.ts:167-178
func (e *Engine) ReadTitle(ctx context.Context, id session.SessionID) (sessiontitle.Snapshot, bool, error) {
	observation, err := e.ReadTitleSnapshot(ctx, id)
	if err != nil {
		return sessiontitle.Snapshot{}, false, err
	}
	return observation.Title, observation.Titled, nil
}
