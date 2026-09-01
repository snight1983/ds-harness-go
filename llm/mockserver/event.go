// 本文件的作用：一次请求在本服务器这一侧的结局，以及为此发出的两条遥测。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:75-114

package mockserver

import (
	"encoding/json"
	"net/http"
)

// Outcome 是一次被接下的请求在本服务器边界上是怎么结束的。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:75-76（MockLlmRequestOutcome）
//
// 「在本服务器这一侧」是这几个词的全部含义：OutcomeCompleted 只说明本端按剧本
// 把该写的都写完了，不说明客户端收全了，更不说明被测的那套恢复策略做对了。
type Outcome string

const (
	// OutcomeCompleted 是本端按剧本写完并正常收尾——**包括**那些故意写出坏数据的剧本。
	OutcomeCompleted Outcome = "completed"
	// OutcomeReset 是本端主动把连接掐了。
	OutcomeReset Outcome = "reset"
	// OutcomeStalled 是本端发完头就挂着，等着谁来取消。
	OutcomeStalled Outcome = "stalled"
	// OutcomeClientClosed 是客户端先走了。
	OutcomeClientClosed Outcome = "client_closed"
	// OutcomeServerError 是处理器自己出了预料之外的错。
	OutcomeServerError Outcome = "server_error"
)

// Event 是一条遥测。只有 [RequestEvent] 和 [ResultEvent] 两种。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:78-94（MockLlmServerEvent）
//
// 新增: TS 用 type 字段做可辨识联合。Go 换成封闭接口——那个非导出的 isEvent
// 方法让包外造不出第三种事件，于是消费方的 type switch 是穷尽的，不需要一个
// 「不认识的事件」兜底分支。TS 那边的 type 字段在 Go 里由具体类型本身承担。
type Event interface {
	isEvent()
}

// RequestEvent 在一个请求被接下、行为已经选定、但还没开始演的时候发出。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:80-86
type RequestEvent struct {
	// Attempt 是从 1 开始的、被接下的聊天补全请求序号。
	Attempt int
	// ScriptBehavior 是剧本里那一条，随机还没展开之前的样子。
	ScriptBehavior Behavior
	// Behavior 是展开之后真正要演的那一种。
	Behavior Behavior
	// Path 是请求原本的路径，客户端带了 /v1 前缀就留着。
	Path string
}

func (RequestEvent) isEvent() {}

// MarshalJSON 把事件写成独立进程那套 JSONL 记录的形状。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:80-86
//
// 新增: TS 那个 type 字段是对象上真实存在的一个属性，JSON.stringify 顺手就带上了。
// Go 这边它由具体类型承担（见 [Event]），线路上却仍然要有——读 JSONL 的那一侧
// 只能按这个字段分辨两种记录。键名保持 DSH 的小驼峰：这些行是给已经存在的脚本
// 读的，改成 Go 习惯的写法等于换了一份协议。
func (e RequestEvent) MarshalJSON() ([]byte, error) {
	type record struct {
		Type           string   `json:"type"`
		Attempt        int      `json:"attempt"`
		ScriptBehavior Behavior `json:"scriptBehavior"`
		Behavior       Behavior `json:"behavior"`
		Path           string   `json:"path"`
	}
	return json.Marshal(record{
		Type:           "request",
		Attempt:        e.Attempt,
		ScriptBehavior: e.ScriptBehavior,
		Behavior:       e.Behavior,
		Path:           e.Path,
	})
}

// ResultEvent 在一个请求走到结局的时候发出，每个请求至多一条。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:87-94
type ResultEvent struct {
	// Attempt 与对应的 [RequestEvent] 相同。
	Attempt int
	// ScriptBehavior 与对应的 [RequestEvent] 相同。
	ScriptBehavior Behavior
	// Behavior 与对应的 [RequestEvent] 相同。
	Behavior Behavior
	// Outcome 是这次请求在本端的结局。
	Outcome Outcome
	// ChunksSent 是本端交出去的 SSE data: 事件条数，含 [DONE] 那一条。
	ChunksSent int
}

func (ResultEvent) isEvent() {}

// MarshalJSON 把事件写成独立进程那套 JSONL 记录的形状。理由见 [RequestEvent.MarshalJSON]。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:87-94
func (e ResultEvent) MarshalJSON() ([]byte, error) {
	type record struct {
		Type           string   `json:"type"`
		Attempt        int      `json:"attempt"`
		ScriptBehavior Behavior `json:"scriptBehavior"`
		Behavior       Behavior `json:"behavior"`
		Outcome        Outcome  `json:"outcome"`
		ChunksSent     int      `json:"chunksSent"`
	}
	return json.Marshal(record{
		Type:           "result",
		Attempt:        e.Attempt,
		ScriptBehavior: e.ScriptBehavior,
		Behavior:       e.Behavior,
		Outcome:        e.Outcome,
		ChunksSent:     e.ChunksSent,
	})
}

// RequestRecord 是一次被接下的请求的存档：线路上收到了什么，以及本端怎么收的场。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:96-114（MockLlmRequestRecord）
//
// 新增: TS 那份记录是活的——测试拿到数组之后，chunksSent 和 outcome 会随着请求
// 推进在原地变。Go 这边 [Server.Requests] 交的是快照：记录会被多个处理器协程
// 并发改写，交出活对象等于把数据竞争送给调用方。想看最新状态就再取一次快照。
type RequestRecord struct {
	// Attempt 是从 1 开始的、被接下的聊天补全请求序号。
	Attempt int
	// ScriptBehavior 是这次请求消费掉的剧本条目，随机展开之前的样子。
	ScriptBehavior Behavior
	// Behavior 是展开之后真正演的那一种；剧本用完时是 [BehaviorScriptExhausted]。
	Behavior Behavior
	// Path 是请求原本的路径。
	Path string
	// Header 是请求头的一份副本。
	Header http.Header
	// Body 是解析出来的请求体。
	Body any
	// HasBody 区分「请求体是空的」和「请求体是一个 JSON null」。
	//
	// 新增: TS 靠 undefined 和 null 两个值天然分开这件事，Go 的 nil 接口把它们
	// 合成了一个。这里补一个布尔把它们再分开——测试要能断言「空请求体照样被接下、
	// 剧本照样被消费」，而那条断言的证据正是这个区分。
	HasBody bool
	// ChunksSent 是截至快照时本端交出去的 SSE data: 事件条数。
	ChunksSent int
	// Outcome 是本端的结局；还挂着没结束时是空串。
	Outcome Outcome
}
