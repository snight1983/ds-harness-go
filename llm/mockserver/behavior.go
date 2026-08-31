// 本文件的作用：剧本里能点的名字，以及随机模式那份默认权重。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:15-73

package mockserver

import "time"

// Behavior 是剧本里的一条：一次请求要演的故障或者成功。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:44
//
// 新增: TS 是 typeof MOCK_LLM_BEHAVIORS[number] 这种从数组反推出来的字面量联合，
// 编译期就能拦住写错的名字。Go 没有等价的类型运算，换成具名 string 加一组常量：
// 拼错的名字挡不到编译期，但 [Options] 的校验会在服务器起来之前就拒掉它，而
// 剧本里的名字本来也常常是从命令行字符串来的（见 [ParseCLIArgs]），那一侧无论
// 如何都得在运行期认一遍。
type Behavior string

// 剧本里能点的全部行为。分档排列，和 README 的那张表同序。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:16-41
const (
	// BehaviorConnectionReset 在发出任何 HTTP 头之前就把 socket 掐掉。
	BehaviorConnectionReset Behavior = "connection_reset"
	// BehaviorStreamDisconnect 发完 SSE 头、在第一个事件之前重置连接。
	BehaviorStreamDisconnect Behavior = "stream_disconnect"
	// BehaviorEmpty 发一个合法但没有内容的停止帧，再补 [DONE]。
	BehaviorEmpty Behavior = "empty"
	// BehaviorEmptyBody 发完 SSE 头就干净地结束，一个事件都没有。
	BehaviorEmptyBody Behavior = "empty_body"
	// BehaviorStreamEOF 只发一个宣告角色的增量就结束，不给 [DONE]。
	BehaviorStreamEOF Behavior = "stream_eof"
	// BehaviorPartialEOF 发一段文本增量就结束，不给 [DONE]。
	BehaviorPartialEOF Behavior = "partial_eof"
	// BehaviorPartialDisconnect 发一段文本增量之后重置连接。
	BehaviorPartialDisconnect Behavior = "partial_disconnect"
	// BehaviorStall 发完 SSE 头就挂着不动，直到客户端或者服务器取消。
	BehaviorStall Behavior = "stall"
	// BehaviorMalformedJSON 发一个 data: 行，内容不是合法 JSON。
	BehaviorMalformedJSON Behavior = "malformed_json"
	// BehaviorMalformedEvent 发一个合法 JSON、但形状不是供应商约定的事件。
	BehaviorMalformedEvent Behavior = "malformed_event"
	// BehaviorWrongContentType 用 application/json 发一段其实是 SSE 的正文。
	BehaviorWrongContentType Behavior = "wrong_content_type"
	// BehaviorRateLimit 回 429，并带上 Retry-After。
	BehaviorRateLimit Behavior = "rate_limit"
	// BehaviorServerError 回 500。
	BehaviorServerError Behavior = "server_error"
	// BehaviorServiceUnavailable 回 503。
	BehaviorServiceUnavailable Behavior = "service_unavailable"
	// BehaviorAuthError 回 401。
	BehaviorAuthError Behavior = "auth_error"
	// BehaviorInvalidRequest 回 400。
	BehaviorInvalidRequest Behavior = "invalid_request"
	// BehaviorContextOverflow 回 400，错误码是上下文超长。
	BehaviorContextOverflow Behavior = "context_overflow"
	// BehaviorQuotaExceeded 回 429，错误码是余额不足——和限流同码不同因。
	BehaviorQuotaExceeded Behavior = "quota_exceeded"
	// BehaviorSuccess 流式发完整段文本，正常收尾。
	BehaviorSuccess Behavior = "success"
	// BehaviorReasoningSuccess 先发一段思考内容，再发正文。
	BehaviorReasoningSuccess Behavior = "reasoning_success"
	// BehaviorToolCallSuccess 分两段发一次工具调用，以 tool_calls 收尾。
	BehaviorToolCallSuccess Behavior = "tool_call_success"
	// BehaviorMaxTokens 发完整段文本，但以 length 收尾。
	BehaviorMaxTokens Behavior = "max_tokens"
	// BehaviorSlowSuccess 和 [BehaviorSuccess] 一样，但每个增量之间有停顿。
	BehaviorSlowSuccess Behavior = "slow_success"
	// BehaviorRandom 不是一种行为，是「按权重挑一种具体行为」。
	BehaviorRandom Behavior = "random"

	// BehaviorScriptExhausted 不能写进剧本，它是剧本用完之后记录里出现的那个名字。
	//
	// 源: packages/test-support/llm-mock-server/src/index.ts:83
	BehaviorScriptExhausted Behavior = "script_exhausted"
)

// behaviorOrder 是全部可写进剧本的行为，含 [BehaviorRandom]，按声明序。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:16-41
//
// 新增: TS 那个 as const 数组同时承担三件事——推出字面量类型、给 CLI 查名字、
// 给随机权重校验查名字。Go 这边类型那一份由常量承担，剩下两件事由本切片和
// [Behaviors]／[IsBehavior]／[IsConcreteBehavior] 承担。
var behaviorOrder = []Behavior{
	BehaviorConnectionReset,
	BehaviorStreamDisconnect,
	BehaviorEmpty,
	BehaviorEmptyBody,
	BehaviorStreamEOF,
	BehaviorPartialEOF,
	BehaviorPartialDisconnect,
	BehaviorStall,
	BehaviorMalformedJSON,
	BehaviorMalformedEvent,
	BehaviorWrongContentType,
	BehaviorRateLimit,
	BehaviorServerError,
	BehaviorServiceUnavailable,
	BehaviorAuthError,
	BehaviorInvalidRequest,
	BehaviorContextOverflow,
	BehaviorQuotaExceeded,
	BehaviorSuccess,
	BehaviorReasoningSuccess,
	BehaviorToolCallSuccess,
	BehaviorMaxTokens,
	BehaviorSlowSuccess,
	BehaviorRandom,
}

// Behaviors 交出全部可写进剧本的行为名，按声明序。返回的是副本。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:16
//
// 新增: TS 直接导出那个冻结的数组。Go 的切片没有只读形态，导出变量等于把内部
// 名册交给调用方随便改，所以这里交副本——本包自己查名字走的是 [IsBehavior]，
// 不经过这个函数。
func Behaviors() []Behavior {
	names := make([]Behavior, len(behaviorOrder))
	copy(names, behaviorOrder)
	return names
}

// IsBehavior 判一个名字是不是剧本里能点的行为（含 random）。
func IsBehavior(name Behavior) bool {
	for _, known := range behaviorOrder {
		if known == name {
			return true
		}
	}
	return false
}

// IsConcreteBehavior 判一个名字是不是**具体**行为，也就是 random 之外的那些。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:47,195
//
// 随机权重只能挂在具体行为上：给 random 配权重是一句自指的话，挑中它之后还要
// 再挑一次，没有终点。
func IsConcreteBehavior(name Behavior) bool {
	return name != BehaviorRandom && IsBehavior(name)
}

// DefaultRandomWeights 是随机模式的默认压力配方。返回的是副本。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:56-70
//
// 这份权重是**测试压力**，不是对线上故障频率的估计。成功占大头是为了让一次长跑
// 里大部分请求走通常路径，剩下的额度平摊给重置、断流、半截输出、空补全、挂死、
// 429/5xx 和坏 JSON——每一种都留一点，这样跑得够久就都会撞上。
//
// connection_refused 不在里面：那是监听器还没起来时的 TCP 层拒绝，一个已经接下
// 请求的处理器演不出来（见 [BehaviorConnectionRefused]）。
func DefaultRandomWeights() map[Behavior]float64 {
	return map[Behavior]float64{
		BehaviorSuccess:            48,
		BehaviorSlowSuccess:        10,
		BehaviorMaxTokens:          2,
		BehaviorConnectionReset:    5,
		BehaviorStreamDisconnect:   5,
		BehaviorPartialDisconnect:  10,
		BehaviorEmpty:              5,
		BehaviorStall:              2,
		BehaviorRateLimit:          5,
		BehaviorServerError:        4,
		BehaviorServiceUnavailable: 2,
		BehaviorPartialEOF:         1,
		BehaviorMalformedJSON:      1,
	}
}

// MaxTimerDelay 是各项延时选项接受的上限。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:73
//
// 新增: 在 Node 那边这是硬约束——超过 2^31-1 毫秒的延时会被 setTimeout **静默
// 截断成 1 毫秒**，一个想挂十年的测试会立刻返回。Go 的 [time.Timer] 没有这个坑，
// 所以这里留着它不是为了防截断，是为了让同一份剧本在两边被接受和被拒绝的集合
// 一样：一个在 DSH 下报错的配置，不该在 Go 下悄悄跑起来演出别的东西。
const MaxTimerDelay = 2147483647 * time.Millisecond
