// 本文件的作用：模型和读引擎之间那道门——每一次调引擎都从这里过，回来的失败
// 在这里被翻译成一句安全的话，原始那条错误链只进日志。
//
// 源: packages/session-query/tool-session-query/src/service-boundary.ts

package querytool

import (
	"context"
	"errors"
	"log/slog"

	"ds-harness-go/sessionquery"
)

// safeFailure 是一条引擎失败对外的样子。
//
// 源: packages/session-query/tool-session-query/src/service-boundary.ts:14-17
type safeFailure struct {
	// code 是对外报的码；[CodeFailed] 表示这条失败连码都不该露。
	code Code
	// message 是那句给模型看的话。
	message string
}

// safeFailures 把引擎的每一个分类码映射成一句模型安全的话。
//
// 源: packages/session-query/tool-session-query/src/service-boundary.ts:21-87
//
// 这张表必须**穷举**引擎的码：漏掉一个，那条失败就塌成通用的一句，模型再也
// 分不清「检索被这个部署关掉了」和「后端挂了」——前者该让它别再试，后者该让它重试。
//
// 两条例外塌成 [CodeFailed]：CodeInvalidConfig 说的是这次装配接错了，
// CodeSourceConflict 说的是活会话表和落地日志对不上。两件都是运维的事，
// 说给模型听既没有用，又暴露了部署内情。
//
// 新增: 引擎的码在 Go 侧就是 [sessionquery.Code]（一个字符串类型），所以这张表是
// 一张 map，不是 DSH 那个 `satisfies Record<...>` 的对象字面量；穷举性由
// [TestTheSafeFailureTableCoversEveryEngineCode] 守着，而不是类型系统。
var safeFailures = map[sessionquery.Code]safeFailure{
	sessionquery.CodeAborted:           {Code(sessionquery.CodeAborted), "session query was cancelled"},
	sessionquery.CodeCorruptSession:    {Code(sessionquery.CodeCorruptSession), "session event history is corrupt"},
	sessionquery.CodeEventNotFound:     {Code(sessionquery.CodeEventNotFound), "session event was not found"},
	sessionquery.CodeIndexFailed:       {Code(sessionquery.CodeIndexFailed), "session search index is unavailable"},
	sessionquery.CodeInvalidConfig:     {CodeFailed, genericFailureText},
	sessionquery.CodeInvalidCursor:     {Code(sessionquery.CodeInvalidCursor), "session search continuation is invalid"},
	sessionquery.CodeInvalidFilter:     {Code(sessionquery.CodeInvalidFilter), "session query filters were rejected"},
	sessionquery.CodeInvalidLimit:      {Code(sessionquery.CodeInvalidLimit), "session query result limit was rejected"},
	sessionquery.CodeInvalidQuery:      {Code(sessionquery.CodeInvalidQuery), "session query was rejected"},
	sessionquery.CodeInvalidLineage:    {Code(sessionquery.CodeInvalidLineage), "session lineage is invalid"},
	sessionquery.CodeInvalidSurface:    {Code(sessionquery.CodeInvalidSurface), "session event history is invalid"},
	sessionquery.CodeInvalidWindow:     {Code(sessionquery.CodeInvalidWindow), "session event window is invalid"},
	sessionquery.CodePersistenceFailed: {Code(sessionquery.CodePersistenceFailed), "session history storage is unavailable"},
	sessionquery.CodeSearchDisabled:    {Code(sessionquery.CodeSearchDisabled), "session search is disabled in this deployment"},
	sessionquery.CodeSessionNotFound:   {Code(sessionquery.CodeSessionNotFound), "session was not found"},
	sessionquery.CodeStaleCursor: {
		Code(sessionquery.CodeStaleCursor),
		"session history changed while paging; retry the complete search call",
	},
	sessionquery.CodeSourceConflict: {CodeFailed, genericFailureText},
}

// genericFailureText 是那句什么都不说的失败。
//
// 源: packages/session-query/tool-session-query/src/service-boundary.ts:140
const genericFailureText = "session query operation failed"

// unauthorizedTarget 造那条「目标在工作区之外」。
//
// 源: packages/session-query/tool-session-query/src/service-boundary.ts:89-94
func unauthorizedTarget() error {
	return fail(CodeUnauthorized, "session target is outside the caller workspace")
}

// genericFailure 造那条通用失败。
//
// 源: packages/session-query/tool-session-query/src/service-boundary.ts:138-143
func genericFailure() error {
	return fail(CodeFailed, genericFailureText)
}

// call 跑一次引擎调用，前后各查一次取消，失败经过清洗才交出去。
//
// 源: packages/session-query/tool-session-query/src/service-boundary.ts:96-112
//
// 前后各查一次的理由和 DSH 一样：调用之前被取消就不该白跑一趟；调用**之后**
// 被取消，那条取消要盖过 invoke 自己报的任何东西——一次被撤掉的调用报出来的
// 失败没有意义，模型照着它改反而是坏的。
//
// 新增: 泛型函数而不是方法，因为 Go 的方法不能带类型参数；控制器由第一个参数
// 显式传进来。
func call[V any](ctx context.Context, c *Controller, operation string, invoke func() (V, error)) (V, error) {
	var zero V
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	value, err := invoke()
	if abort := ctx.Err(); abort != nil {
		return zero, abort
	}
	if err != nil {
		return zero, c.sanitize(operation, err)
	}
	return value, nil
}

// sanitize 把一条引擎失败翻译成模型能看的一句话，原始那条只进日志。
//
// 源: packages/session-query/tool-session-query/src/service-boundary.ts:114-136
//
// 三条出路，按 DSH 的顺序：查得到安全说法的引擎错误换成那句；本包自己那条
// 「越界」原样保留（它本来就是安全的）；其余一律是通用失败。
func (c *Controller) sanitize(operation string, err error) error {
	// 日志先写。它是这条失败唯一的完整记录——下面无论走哪条出路，原始信息都不再
	// 出现在返回值里。
	c.logger.Warn("tool-session-query: 调用失败", slog.String("operation", operation), slog.Any("error", err))

	var engineErr *sessionquery.Error
	if errors.As(err, &engineErr) {
		if failure, ok := safeFailures[engineErr.Code]; ok && failure.code != CodeFailed {
			return fail(failure.code, "%s", failure.message)
		}
	}
	if codeOf(err) == CodeUnauthorized {
		return unauthorizedTarget()
	}
	return genericFailure()
}
