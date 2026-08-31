// 本文件的作用：一次完整装配好的模型请求——发给哪条路由、带哪些消息、
// 带哪些工具、以及那几个只给传输层看的标记。
//
// 源: packages/llm/llm/src/types.ts:339-377

package llm

// SessionID 是一个会话的身份，循环把它盖在请求上，用来路由。
//
// 源: packages/llm/llm/src/types.ts:370
//
// 新增: DSH 在 GenerateOptions 上直接内联写 Branded<'SessionId'>，不从会话那个包
// 引。Go 这边同样引不了——ds-harness-go/session 引的是本包，反过来引会成环。
// 所以这里另有一个同名类型，底下都是 string，跨包时显式转一次。这不是重复定义
// 一份真理：会话那个包拥有的是「一个会话在存储里的身份」，本包这一个只是
// 「盖在请求上的一个不透明标记」，本包一个字都不解释它。
type SessionID string

// CallPurpose 是一次辅助模型调用的、与提供方无关的分类。
//
// 源: packages/llm/llm/src/types.ts:371-377
//
// 适配器可以把它映射成模型看不见的传输层元数据，或者按用途走不同的生成策略。
// 普通的对话请求不设它。
type CallPurpose string

const (
	// PurposeCompaction 是一次为了压缩历史而发的调用。
	PurposeCompaction CallPurpose = "compaction"
	// PurposeSessionTitle 是一次为了给会话起标题而发的调用。
	PurposeSessionTitle CallPurpose = "session-title"
)

// GenerateOptions 是一次装配完整的模型请求。
//
// 源: packages/llm/llm/src/types.ts:339-377
//
// 新增: DSH 那个 signal?: AbortSignal 字段在这里不存在——按本仓库一贯的规矩，
// 取消走每个方法的第一个 context.Context 参数。
type GenerateOptions struct {
	// Provider 是登记过的那条提供方路由，它选出具体那个适配器实例。
	Provider string
	// Model 是那条路由上的模型 id。
	Model string
	// ReasoningEffort 是给这个确切模型选中的、适配器自己拥有的推理档位。
	// 空串表示没选。
	ReasoningEffort ReasoningEffortID
	// Messages 是有序的对话消息，就是提供方看到的那一份（system 槽之外的部分）。
	//
	// 循环装出来的请求带的是推导出来的历史；手搓的一次性调用爱传什么传什么。
	Messages []Message
	// System 是系统提示词文本，适配器把它映射到提供方的 system 槽。
	System string
	// Tools 是工具的 schema，适配器把它映射到提供方的 tools 字段。
	Tools []ToolSchema
	// Temperature 是采样温度，nil 表示不设、由提供方自己决定。
	//
	// 新增: 和 [CallConfig].Temperature 一样是 *float64——0 是一个有意义的取值
	// （贪心解码），拿零值当「没设」会把它和「设成 0」混掉。
	Temperature *float64
	// MaxTokens 是这次请求的输出 token 上限，0 表示不设。
	MaxTokens int
	// Stop 是停止串：模型一吐出其中任意一个就立刻停下。适配器把它映射到提供方
	// 的停止字段（比如 OpenAI 的 stop）。停止串本身不进输出。
	Stop []string
	// SessionID 是循环盖上来的会话身份，用来路由。重放靠它区分各自的游标；
	// 适配器可以把它映射成模型看不见的传输层元数据。空串表示没盖。
	SessionID SessionID
	// Purpose 是这次辅助调用的分类，空串表示这是一次普通的对话请求。
	Purpose CallPurpose
	// AgentLoop 表示这份请求是 agent 循环装配出来的。
	//
	// 源: packages/llm/llm/src/call-config.ts:66、76
	//
	// 新增: DSH 那边这件事是一张 WeakSet：markAgentLoopRequest 把那个**请求对象
	// 本身**记进去，isAgentLoopRequest 再拿同一个对象去问。它认的是 JS 里对象的
	// 身份——两份字段完全一样的请求是两个不同的键。Go 里 GenerateOptions 是值，
	// 复制一次就是另一个，压根没有可供旁路表去认的身份，所以这件事只能是请求
	// 自己身上的一位。
	//
	// 语义因此有一处对不上，而且是往宽的方向：DSH 那边把一份请求复制一遍，
	// 副本不在 WeakSet 里；Go 这边 [GenerateOptions.Clone] 会把这一位一起带走。
	// 两个读它的地方（agentloop 的不变量检查、session-title 判「这不是我发的」）
	// 要的都是「这份请求的内容是循环装出来的」，复制一份仍然成立，所以放宽不伤。
	AgentLoop bool
}

// Clone 复制一份，好让交出去的那一份改不动这一份。
//
// 新增: 消息内容那棵树跟着一起复制（走 [Message.Clone]），理由和本包
// [Message] 那边一样：拿到一份请求的人改不动别人手里那份。
func (o GenerateOptions) Clone() GenerateOptions {
	clone := o
	if o.Messages != nil {
		clone.Messages = make([]Message, len(o.Messages))
		for index, message := range o.Messages {
			clone.Messages[index] = message.Clone()
		}
	}
	if o.Tools != nil {
		clone.Tools = append([]ToolSchema(nil), o.Tools...)
	}
	if o.Temperature != nil {
		temperature := *o.Temperature
		clone.Temperature = &temperature
	}
	if o.Stop != nil {
		// 和 [CallConfig.Clone] 里同一处讲究：append 到一个 nil 上复制不出「长度为零
		// 的非 nil 切片」，而本包把「没给停止串」和「明确给了一个空清单」当两件事。
		clone.Stop = make([]string, len(o.Stop))
		copy(clone.Stop, o.Stop)
	}
	return clone
}

// CallConfig 摘出这次请求里那几个属于调用配置的字段。
//
// 源: packages/llm/llm/src/index.ts:854
//
// [PreparedCall] 拿它和自己那份配置比对，比不上就是 INVALID_PREPARED_CALL
// ——一份准备好的调用只能用来发它自己那次请求。
//
// 新增: DSH 那边直接 callConfigEquals(options, resolvedConfig)——TS 是结构化类型，
// 一个 GenerateOptions 本来就满足 LlmCallConfig 的形状。Go 是标称类型，两者是两个
// 不同的结构体，所以要显式摘一次。
func (o GenerateOptions) CallConfig() CallConfig {
	return CallConfig{
		Provider:        o.Provider,
		Model:           o.Model,
		ReasoningEffort: o.ReasoningEffort,
		Temperature:     o.Temperature,
		MaxTokens:       o.MaxTokens,
		Stop:            o.Stop,
	}
}
