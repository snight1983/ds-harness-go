// 本文件的作用：一次成功响应里那份要落库的提供方原生元数据，以及把它读回来时
// 的那套校验。
//
// 源: packages/llm/llm-pi-ai/src/replay.ts

package openaicompat

import (
	"encoding/json"
	"fmt"
	"slices"

	"ds-harness-go/llm"
)

// ReplayKind 是本适配器写下的重放状态的种别标记。
//
// 源: packages/llm/llm-pi-ai/src/replay.ts:23
//
// 别的适配器族写下的信封会带着别的种别，读到时降级而不是崩——这正是这个字段
// 存在的理由。DSH 那边写的是 "pi-ai"，这边不是那个东西，所以换一个。
const ReplayKind = "openai-compat"

// ReplayVersion 是本构建写得出、也读得懂的那一版信封。
//
// 源: packages/llm/llm-pi-ai/src/replay.ts:24
//
// 从 1 起而不是跟着 DSH 的 2：那个 2 是 pi-ai 那份信封自己的演进史，这一份没有
// 那段历史，写 2 只会让人去找一份不存在的 v1。
const ReplayVersion = 1

// ReplayResponse 是重放信封里带版本的那半边：整条响应级别的元数据。
//
// 源: packages/llm/llm-pi-ai/src/replay.ts:22-31
//
// 新增: DSH 那份多一个 api 字段（这条消息是哪条协议产出的）。只有一条协议之后
// 它恒等于一个常量，不写。
type ReplayResponse struct {
	// Kind 一定是 [ReplayKind]。
	Kind string `json:"kind"`
	// Version 一定是 [ReplayVersion]。
	Version int `json:"version"`
	// Provider 是产出这条消息的路由键。
	Provider string `json:"provider"`
	// Model 是请求里点的那个模型 id。
	Model string `json:"model"`
	// ResponseModel 是提供方**实际**服务的那个模型，它报了的话。
	//
	// 这是这份信封上唯一别处推不出来的事实：一个把 "gpt-4o" 路由到
	// "gpt-4o-2024-11-20" 的网关，只有在这里说得出它当时route到了哪一版。
	// 「同一个会话为什么昨天和今天答得不一样」这个问题靠它回答。
	ResponseModel string `json:"responseModel,omitempty"`
	// ResponseID 是提供方给这次响应的 id，它给了的话。对着提供方的账单和日志
	// 查一次具体请求靠它。
	ResponseID string `json:"responseId,omitempty"`
	// StopReason 是这次响应停下来的原因。
	StopReason llm.FinishKind `json:"stopReason"`
}

// replayStopReasons 是 [ReplayResponse.StopReason] 认的那几个取值。
//
// 源: packages/llm/llm-pi-ai/src/replay.ts:121-123
var replayStopReasons = []llm.FinishKind{
	llm.FinishStop, llm.FinishMaxTokens, llm.FinishToolCalls, llm.FinishError, llm.FinishAborted,
}

// ToReplayState 把一次成功响应投影成那份要落库的重放状态。
//
// 源: packages/llm/llm-pi-ai/src/replay.ts:72-103
//
// 新增: DSH 那份信封还有**每块一条**的另一半（blocks），装的是 textSignature /
// thinkingSignature / thoughtSignature / redacted——Anthropic 那条协议上每个内容块
// 自带的原生签名，下一次请求必须原样回发，否则那条历史会被判成伪造。
// OpenAI 兼容的线上协议没有任何这种东西：一条助手历史消息就是 role/content/
// tool_calls 三样，全都从落库的内容里直接得到。所以这一半整个不写。
//
// [llm.ReplayEnvelope] 的 Blocks 可以为空，为空时它原样穿过
// [llm.BlockAssembler]——那正是为这种协议留的口子。
func ToReplayState(response ReplayResponse) (llm.ReplayEnvelope, error) {
	response.Kind = ReplayKind
	response.Version = ReplayVersion
	raw, err := json.Marshal(response)
	if err != nil {
		return llm.ReplayEnvelope{}, err
	}
	return llm.ReplayEnvelope{Response: raw}, nil
}

// invalidReplay 报一份这个构建用不了的重放状态。
//
// 源: packages/llm/llm-pi-ai/src/replay.ts:105-107
//
// 文案是英文的：它会被 [llm.NormalizeFailure] 规整成一条 [llm.Failure] 的
// Message 写进会话日志，而日志里的失败事实这个仓库一律用英文，理由见
// [llm.AssertUsableAPIKey]。
func invalidReplay(detail string) *llm.Error {
	return llm.NewError("invalid openai-compat replay state: "+detail, "INVALID_REPLAY_STATE", nil)
}

// ReadReplayState 在信封被当真之前把它验一遍。
//
// 源: packages/llm/llm-pi-ai/src/replay.ts:110-141
//
// 每一条拒绝都交 INVALID_REPLAY_STATE，因为调用方靠这个码把「这份状态用不了」
// 和别的失败分开——前者降级，后者往上抛。见 [ReplayStateOf]。
func ReadReplayState(raw json.RawMessage) (ReplayResponse, error) {
	var envelope struct {
		Response json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return ReplayResponse{}, invalidReplay("expected a replay envelope")
	}
	if len(envelope.Response) == 0 {
		return ReplayResponse{}, invalidReplay("expected a response object")
	}
	var response ReplayResponse
	if err := json.Unmarshal(envelope.Response, &response); err != nil {
		return ReplayResponse{}, invalidReplay("expected a response object")
	}
	if response.Kind != ReplayKind {
		return ReplayResponse{}, invalidReplay("unknown state kind")
	}
	if response.Version != ReplayVersion {
		return ReplayResponse{}, invalidReplay(fmt.Sprintf("unsupported version %d", response.Version))
	}
	if response.Provider == "" {
		return ReplayResponse{}, invalidReplay("provider must be a non-empty string")
	}
	if response.Model == "" {
		return ReplayResponse{}, invalidReplay("model must be a non-empty string")
	}
	if !slices.Contains(replayStopReasons, response.StopReason) {
		return ReplayResponse{}, invalidReplay("unknown stopReason")
	}
	return response, nil
}

// ReplayStateOf 从一条落库的助手消息上取出验过的重放状态。
//
// 源: packages/llm/llm-pi-ai/src/replay.ts:237-249
//
// 三种结果：
//   - (状态, true, nil) 这条消息带着一份本构建用得了的状态；
//   - (零值, false, nil) 它没带状态，或者带的那份**用不了**（别的适配器族写的、
//     另一个版本、坏掉的、或者和消息自己的出处对不上）；
//   - (零值, false, err) 出了别的事，往上抛。
//
// 第二种就是 DSH 那条降级：一份用不了的状态不该让这次请求失败，因为落库的内容
// 本身仍然是权威记录，重放状态只是往上补原生保真度。degraded 交出那句诊断，
// 让调用方留一声——一个永远在降级的会话是**有人把二进制降级了**的信号，
// 而它在功能上完全看不出来。
func ReplayStateOf(message llm.Message) (state ReplayResponse, ok bool, degraded string) {
	source, isModel := message.ModelSource()
	if !isModel || len(source.ReplayState) == 0 {
		return ReplayResponse{}, false, ""
	}
	response, err := ReadReplayState(source.ReplayState)
	if err != nil {
		return ReplayResponse{}, false, err.Error()
	}
	// 源: packages/llm/llm-pi-ai/src/replay.ts:181-182
	//
	// 对不上说明这份状态是被搬到别的消息上去的。运行时已经把**别的适配器**写的
	// 状态摘掉了（见 [llm.Runtime] 那段 forAdapter），所以走到这里还对不上，
	// 指的是同一族里跨路由的错配——那种状态里的 responseId 会把人引到另一条路由
	// 的账单上去。
	if response.Provider != source.Provider {
		return ReplayResponse{}, false, invalidReplay("provider does not match assistant source").Error()
	}
	if response.Model != source.Model {
		return ReplayResponse{}, false, invalidReplay("model does not match assistant source").Error()
	}
	return response, true, ""
}
