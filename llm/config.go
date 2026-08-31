// 本文件的作用：一次会话的那几个请求头状态（提供方、模型、采样标量），以及
// 「这次提议算不算一次真的改动」的判据。
//
// 源: packages/llm/llm/src/call-config.ts:1-59
// 源: packages/llm/llm/src/types.ts:326-338（ToolSchema）

package llm

import "encoding/json"

// CallConfig 是一次会话的那些请求的提供方路由、模型、推理档位和采样标量。
//
// 源: packages/llm/llm/src/call-config.ts:17-30
//
// 每个字段都和请求选项里的同名字段一一对应。它是**请求头状态**：这几个值会影响
// 提供方那边的缓存复用，所以循环是从记下来的这份头去装配请求，而不是让每次调用
// 各自带一份——后者会让配置在日志里看不见地漂移。
type CallConfig struct {
	// Provider 是选中适配器实例的那个提供方路由键。
	Provider string `json:"provider"`
	// Model 是提供方的模型标识。
	Model string `json:"model"`
	// ReasoningEffort 是适配器自己拥有的推理档位；空串表示没选。
	ReasoningEffort ReasoningEffortID `json:"reasoningEffort,omitempty"`
	// Temperature 是采样温度；nil 表示没给，由提供方定。
	//
	// 新增: 这里用指针，因为 0 是一个**有意义的温度**（确定性采样），
	// 和「没给温度」是两件事。这是本包区分零值与缺失的那条判据的一次应用，
	// 见 attachment/types.go:79-91。
	Temperature *float64 `json:"temperature,omitempty"`
	// MaxTokens 是本次请求的输出 token 上限；0 表示没给。
	//
	// 用 0 而不用指针：一个上限为零的请求产不出任何东西，没人会那么要求，
	// 所以零值不与任何真实取值撞车。
	MaxTokens int `json:"maxTokens,omitempty"`
	// Stop 是停止序列；nil 表示没给，而**长度为零的切片表示明确给了一个空清单**。
	//
	// 这两者必须分开，见 [CallConfigEquals]。
	Stop []string `json:"stop,omitempty"`
}

// Clone 深复制这份配置。
func (c CallConfig) Clone() CallConfig {
	if c.Temperature != nil {
		temperature := *c.Temperature
		c.Temperature = &temperature
	}
	if c.Stop != nil {
		// 不能写 append([]string(nil), c.Stop...)：源切片长度为零时 append 什么都不
		// 追加，交出来的仍然是 nil，于是「明确给了一个空清单」被复制成了「没给」。
		// 这两者本包是分开的（见 Stop 字段和 [CallConfigEquals]），复制一次就把
		// 它们混掉的话，[PreparedCall.Stream] 那道「配置必须和准备时一致」的比对
		// 会在任何解析出空停止清单的适配器上误判。
		stop := make([]string, len(c.Stop))
		copy(stop, c.Stop)
		c.Stop = stop
	}
	return c
}

// CallConfigAdapterDefaults 记的是：生效配置里哪几个字段是**适配器按确切模型解析出来的**，
// 而不是调用方在请求里提议的。
//
// 源: packages/llm/llm/src/call-config.ts:32-39
//
// 新增: DSH 那边两个字段的类型是 `true`（可选的字面量真），也就是「要么这个键在、
// 值只能是 true，要么这个键不在」——TS 里用它表达一个集合。Go 里 bool 就是那个意思，
// false 就是键不在。
type CallConfigAdapterDefaults struct {
	// ReasoningEffort 表示生效的推理档位来自适配器解析，不是调用方给的。
	ReasoningEffort bool `json:"reasoningEffort,omitempty"`
	// MaxTokens 表示生效的输出上限来自适配器解析，不是调用方给的。
	MaxTokens bool `json:"maxTokens,omitempty"`
}

// CallConfigEquals 按字段比较两份 [CallConfig]。
//
// 源: packages/llm/llm/src/call-config.ts:41-59
//
// 这是调用方用来判断「提议的这份配置是一次真的改动（值得往日志里写一份新的头快照），
// 还是把手上那份又说了一遍」的那次比较。
//
// 新增: Stop 那一段**不能**直接用 slices.Equal——它把 nil 和长度为零的切片看作相等，
// 而这里两者不相等：DSH 在 `a.stop === undefined || b.stop === undefined` 这一支上
// 要求两边同为 undefined 才算相等，一边 undefined 一边 `[]` 是不等的。
// 「没给停止序列」和「明确给了一个空的停止序列」是两次不同的请求头。
func CallConfigEquals(a, b CallConfig) bool {
	if a.Provider != b.Provider ||
		a.Model != b.Model ||
		a.ReasoningEffort != b.ReasoningEffort ||
		a.MaxTokens != b.MaxTokens {
		return false
	}
	if !float64PointerEquals(a.Temperature, b.Temperature) {
		return false
	}
	if a.Stop == nil || b.Stop == nil {
		return a.Stop == nil && b.Stop == nil
	}
	if len(a.Stop) != len(b.Stop) {
		return false
	}
	for index, stop := range a.Stop {
		if stop != b.Stop[index] {
			return false
		}
	}
	return true
}

// float64PointerEquals 比较两个可选温度：都没给算相等，一边给了算不等，都给了比值。
//
// 新增: 不能直接比指针——两个各自指向 0.7 的指针是不同的地址，但它们是同一份配置。
func float64PointerEquals(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// ToolSchema 是一个工具给模型看的 JSON Schema 描述。
//
// 源: packages/llm/llm/src/types.ts:326-338
//
// 它声明在这里而不是在工具那一侧，因为它是一次模型请求的组成部分：
// 工具定义和系统提示装配都从本包引它。
type ToolSchema struct {
	// Name 是工具名。
	Name string `json:"name"`
	// Description 是给模型看的工具说明。
	Description string `json:"description"`
	// Parameters 是参数的 JSON Schema 对象。
	//
	// 新增: DSH 是 Record<string, unknown>。这里用 json.RawMessage：它是一段
	// **原样转发给提供方**的 schema，本包一个字段都不读。解成 map[string]any
	// 再排回去会重排键的顺序，而 JSON Schema 里 required 的顺序、properties 的顺序
	// 都会被某些提供方原样回显，也会进提示词缓存的键。
	Parameters json.RawMessage `json:"parameters"`
}
