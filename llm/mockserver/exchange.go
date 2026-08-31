// 本文件的作用：一次请求选定行为之后，那种行为具体怎么在线路上演出来。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:313-329,374-432,461-589

package mockserver

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"time"
)

// sseContentType 是流式补全该有的内容类型。
const sseContentType = "text/event-stream; charset=utf-8"

// exchange 是一次请求的演出现场：谁在演、演给谁看、演的结果记在哪。
//
// 新增: DSH 把 options／record／request／response 四个参数在十来个函数之间来回传。
// Go 这边收成一个结构体，图的不是少打字——是让「所有写线路的动作都记在同一条
// 记录上」变成结构上的事实，而不是每次调用都得把对的记录传对。
type exchange struct {
	server  *Server
	record  *RequestRecord
	request *http.Request
	writer  http.ResponseWriter
}

// finish 给本次请求定下结局。先到的那个算数，见 [Server.finishRecord]。
func (e *exchange) finish(outcome Outcome) {
	e.server.finishRecord(e.record, outcome)
}

// openSSE 发出流式补全的响应头并把它冲出去。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:313-320
//
// contentType 是参数而不是常量，因为 [BehaviorWrongContentType] 要用
// application/json 发一段其实是 SSE 的正文——那正是它要考的东西。
func (e *exchange) openSSE(contentType string) {
	header := e.writer.Header()
	header.Set("Content-Type", contentType)
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	e.writer.WriteHeader(http.StatusOK)
	// 冲不出去只说明客户端已经走了，那件事由 [exchange.pause] 和守望者记账。
	_ = http.NewResponseController(e.writer).Flush()
}

// writeSSE 发一个 data: 事件，并把已发条数记上。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:322-325
//
// payload 是字符串就原样写（[BehaviorMalformedJSON] 和 [DONE] 走的是这条），
// 否则编码成 JSON。
//
// 新增: 每一帧写完都要 Flush。Node 的 response.write 直接进 socket，DSH 不需要
// 这一步；Go 的 [http.ResponseWriter] 带缓冲，不冲的话「发一半就断线」这种行为
// 会退化成「什么都没发就断线」——半截输出正是本服务器最要紧的那几种故障之一。
func (e *exchange) writeSSE(payload any) {
	data, isText := payload.(string)
	if !isText {
		// 本包发出去的每一种帧都是 chunk.go 里定死的结构体，字段全是字符串、整数
		// 和它们的切片，[json.Marshal] 在这些形状上不会失败。
		encoded, _ := json.Marshal(payload)
		data = string(encoded)
	}
	// 写失败只可能是客户端已经走了，由 [exchange.pause] 和守望者记账。
	_, _ = fmt.Fprintf(e.writer, "data: %s\n\n", data)
	_ = http.NewResponseController(e.writer).Flush()

	e.server.mu.Lock()
	e.record.ChunksSent++
	e.server.mu.Unlock()
}

// writeDone 发流末尾那条 [DONE]，它也算一条事件。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:327-329
func (e *exchange) writeDone() {
	e.writeSSE("[DONE]")
}

// pause 等一段时间，客户端中途走掉就提前返回 false。delay 为 0 时只查一眼死活。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:374-388
//
// 新增: 发现客户端走掉时**就地**把结局记成 [OutcomeClientClosed]，DSH 那边这件事
// 是分开的——一部分由调用方显式记，一部分由 response 的 close 钩子记。Go 照抄会
// 留下一个真实的竞争：守望者协程 select 的两个分支（客户端断开、处理器干完）在
// 客户端确实断开时会同时就绪，Go 的 select 此时是随机挑一个，于是同一次跑有时
// 记成 client_closed 有时什么都不记。就地记账把这件事变成确定的，而 finishRecord
// 的「先到的算数」保证它和守望者不会互相盖掉。
func (e *exchange) pause(delay time.Duration) bool {
	if delay == 0 {
		if e.request.Context().Err() != nil {
			e.finish(OutcomeClientClosed)
			return false
		}
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-e.request.Context().Done():
		e.finish(OutcomeClientClosed)
		return false
	}
}

// streamText 把一段文本切成增量发出去，客户端中途走掉就返回 false。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:390-402
func (e *exchange) streamText(text string, delay time.Duration) bool {
	for _, chunk := range splitText(text, e.server.resolved.chunkSize) {
		e.writeSSE(sseChunk{Choices: []sseChoice{{
			Index:        0,
			Delta:        contentDelta{Content: chunk},
			FinishReason: nil,
		}}})
		if !e.pause(delay) {
			return false
		}
	}
	return true
}

// completeText 发完整段成功文本，补上收尾帧和 [DONE]。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:404-419
//
// 用量里的输出 token 数取的是**码点数**而不是字节数：这是个假数字，但它得跟着
// 文本长度走，不然按用量判断的被测逻辑在中文文本上会看到和英文完全不同的量级。
func (e *exchange) completeText(reason string, delay time.Duration) {
	if !e.streamText(e.server.resolved.successText, delay) {
		return
	}
	e.writeSSE(terminalChunk(reason, len([]rune(e.server.resolved.successText))))
	e.writeDone()
	e.finish(OutcomeCompleted)
}

// disconnect 等一小会儿再把连接掐掉，模拟传输中途断线。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:421-432
func (e *exchange) disconnect() error {
	if !e.pause(e.server.resolved.disconnectDelay) {
		return nil
	}
	e.finish(OutcomeReset)
	return e.hardClose()
}

// hardClose 接管连接并强行掐断，不写 HTTP 层的收尾。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:473,431
//
// 新增: DSH 调 socket.destroy() 就完事。Go 的处理器不拥有连接，正常返回一定会补上
// 分块编码的结束标记，于是「流断在半路」会被 net/http 好心地补成「流正常结束」——
// 而那恰好是被测那一侧最该分辨的两件事。所以必须 [http.Hijacker] 把连接抢过来：
// Hijack 会把已经写出的部分冲干净、但不写结束标记，正是要的效果。
//
// SetLinger(0) 让关闭发出的是 RST 而不是 FIN。差别在客户端那一侧是可见的：FIN 是
// 「我说完了」，RST 是「这条连接没了」，重置类行为要考的是后者。
func (e *exchange) hardClose() error {
	connection, _, err := http.NewResponseController(e.writer).Hijack()
	if err != nil {
		return fmt.Errorf("mockserver: 接管连接失败：%w", err)
	}
	if tcp, isTCP := connection.(*net.TCPConn); isTCP {
		_ = tcp.SetLinger(0)
	}
	// 关不上只可能是它已经没了，而那正是本函数想要的结果。
	_ = connection.Close()
	return nil
}

// stall 发完头就挂着，等客户端取消或者服务器关停。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:508-511
//
// 新增: DSH 的处理器直接 return，Node 那条连接就自己挂在那里，收场由
// closeAllConnections 负责。Go 的处理器一返回，net/http 立刻把响应收干净，
// 「挂死」就演成了「秒回一个空的 200」。所以这里必须真的阻塞住。
//
// 被 [Server.closing] 叫醒时要自己把连接掐掉：[http.Server.Close] 不会去动一个
// 正阻塞着的处理器，等着它自己让路，而它等的就是 Close——不掐就是死锁。
func (e *exchange) stall() error {
	select {
	case <-e.request.Context().Done():
		return nil
	case <-e.server.closing:
		return e.hardClose()
	}
}

// httpError 回一条 HTTP 层的失败响应。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:348-365
//
// 结局是 [OutcomeCompleted]：本端按剧本把该回的都回了。一个 429 在本服务器这一侧
// 是**演完了**，它是不是故障由被测那一侧去判断——这条区分是 [Outcome] 的全部意义。
func (e *exchange) httpError(status int, message, kind, code string) {
	extra := map[string]string{}
	if e.record.Behavior == BehaviorRateLimit {
		seconds := math.Ceil(e.server.resolved.retryAfter.Seconds())
		extra["Retry-After"] = fmt.Sprintf("%d", int64(seconds))
	}
	if e.server.resolved.requestID != "" {
		extra["x-request-id"] = e.server.resolved.requestID
	}
	writeJSONError(e.writer, status, extra, message, kind, code)
	e.finish(OutcomeCompleted)
}

// run 按选定的行为演一遍。返回 error 表示本端自己出了岔子。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:461-589
//
// 唯一能返回 error 的是接管连接失败（见 [exchange.hardClose]）。DSH 对应位置是一段
// 标着 v8 ignore 的兜底 catch——在 Node 那边确实构造不出来，而在 Go 这边它是可达
// 也可测的：拿一个不实现 [http.Hijacker] 的 ResponseWriter 直接调处理器就行。
//
// 新增: 结尾那句 default 是 Go 独有的。DSH 靠 TypeScript 的穷尽性检查保证 switch
// 盖住了每一个字面量，Go 的 [Behavior] 只是 string，漏一种就会从底下静默掉出去，
// 客户端收到一个空的 200——「安静地演成别的样子」正是本服务器要帮别人抓的东西。
// [resolveOptions] 已经把不认识的名字拦在了服务器起来之前，所以这里剩下的可能
// 只有一种：往 [behaviorOrder] 里加了新行为却忘了在这里演它。
func (e *exchange) run() error {
	options := e.server.resolved
	switch e.record.Behavior {
	case BehaviorScriptExhausted:
		e.httpError(http.StatusInternalServerError,
			"mock script exhausted", "mock_error", "MOCK_SCRIPT_EXHAUSTED")

	case BehaviorConnectionReset:
		// 一个 HTTP 头都不发就掐，客户端连状态码都拿不到。
		e.finish(OutcomeReset)
		return e.hardClose()

	case BehaviorStreamDisconnect:
		e.openSSE(sseContentType)
		return e.disconnect()

	case BehaviorEmpty:
		e.openSSE(sseContentType)
		e.writeSSE(terminalChunk("stop", 0))
		e.writeDone()
		e.finish(OutcomeCompleted)

	case BehaviorEmptyBody:
		e.openSSE(sseContentType)
		e.finish(OutcomeCompleted)

	case BehaviorStreamEOF:
		e.openSSE(sseContentType)
		e.writeSSE(sseChunk{Choices: []sseChoice{{
			Index:        0,
			Delta:        roleDelta{Role: "assistant"},
			FinishReason: nil,
		}}})
		e.finish(OutcomeCompleted)

	case BehaviorPartialEOF:
		e.openSSE(sseContentType)
		// 这里不看返回值：客户端要是中途走了，[exchange.pause] 已经把结局记成
		// client_closed 了，紧跟着这句 completed 会被「先到的算数」挡掉。
		_ = e.streamText(options.partialText, 0)
		e.finish(OutcomeCompleted)

	case BehaviorPartialDisconnect:
		e.openSSE(sseContentType)
		if !e.streamText(options.partialText, options.chunkDelay) {
			return nil
		}
		return e.disconnect()

	case BehaviorStall:
		e.openSSE(sseContentType)
		e.finish(OutcomeStalled)
		return e.stall()

	case BehaviorMalformedJSON:
		e.openSSE(sseContentType)
		e.writeSSE("{not-json")
		e.writeDone()
		e.finish(OutcomeCompleted)

	case BehaviorMalformedEvent:
		e.openSSE(sseContentType)
		e.writeSSE(malformedChunk{Choices: []*sseChoice{nil}})
		e.writeDone()
		e.finish(OutcomeCompleted)

	case BehaviorWrongContentType:
		e.openSSE("application/json")
		e.completeText("stop", 0)

	case BehaviorRateLimit:
		e.httpError(http.StatusTooManyRequests,
			"mock rate limit", "mock_error", "rate_limit")

	case BehaviorServerError:
		e.httpError(http.StatusInternalServerError,
			"mock server error", "mock_error", "server_error")

	case BehaviorServiceUnavailable:
		e.httpError(http.StatusServiceUnavailable,
			"mock service unavailable", "mock_error", "service_unavailable")

	case BehaviorAuthError:
		e.httpError(http.StatusUnauthorized,
			"mock authentication failed", "mock_error", "invalid_api_key")

	case BehaviorInvalidRequest:
		e.httpError(http.StatusBadRequest,
			"mock invalid request", "mock_error", "invalid_request")

	case BehaviorContextOverflow:
		e.httpError(http.StatusBadRequest,
			"mock input exceeds the model context window",
			"invalid_request_error", "context_length_exceeded")

	case BehaviorQuotaExceeded:
		// 和限流同为 429，错误码不同：一个等一会儿能好，一个等到天亮也不会好。
		e.httpError(http.StatusTooManyRequests,
			"mock insufficient quota", "mock_error", "insufficient_quota")

	case BehaviorSuccess:
		e.openSSE(sseContentType)
		e.completeText("stop", 0)

	case BehaviorReasoningSuccess:
		e.openSSE(sseContentType)
		for _, chunk := range splitText(options.reasoningText, options.chunkSize) {
			e.writeSSE(sseChunk{Choices: []sseChoice{{
				Index:        0,
				Delta:        reasoningDelta{ReasoningContent: chunk},
				FinishReason: nil,
			}}})
		}
		e.completeText("stop", 0)

	case BehaviorToolCallSuccess:
		e.openSSE(sseContentType)
		for _, chunk := range toolCallChunks(options) {
			e.writeSSE(chunk)
		}
		e.writeSSE(terminalChunk("tool_calls", 2))
		e.writeDone()
		e.finish(OutcomeCompleted)

	case BehaviorMaxTokens:
		e.openSSE(sseContentType)
		e.completeText("length", 0)

	case BehaviorSlowSuccess:
		e.openSSE(sseContentType)
		e.completeText("stop", options.chunkDelay)

	default:
		return fmt.Errorf("mockserver: 没有为行为 %q 写演法", e.record.Behavior)
	}
	return nil
}
