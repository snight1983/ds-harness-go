// 本文件的作用：本包自己拥有的那几个标识。
//
// 源: packages/llm/llm/src/brand.ts:1-64

package llm

// MessageID 是一条消息跨越收件箱、日志、模型请求三道边界时始终不变的身份。
//
// 源: packages/llm/llm/src/brand.ts:16-25
//
// 新增: DSH 是 Branded<'MessageId'> 加一个恒等构造函数。Go 的具名类型已经是
// 标称类型，两样都不需要，理由见包文档。
type MessageID string

// CallID 把模型发起的一次工具调用和它的结果对应起来。
//
// 源: packages/llm/llm/src/brand.ts:31-40
//
// 真实适配器上它由提供方签发；模拟实现和装配兜底会自己造一个。
type CallID string

// ProviderRequestID 是提供方签发的请求标识，跨包留着只为诊断。
//
// 源: packages/llm/llm/src/brand.ts:43-52
type ProviderRequestID string

// ReasoningEffortID 是适配器自己拥有的、某个模型可选推理档位的标识。
//
// 源: packages/llm/llm/src/brand.ts:55-64
type ReasoningEffortID string
