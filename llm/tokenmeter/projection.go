// 本文件的作用：三个投影单元交给客户端的那三份视图长什么样，以及每一份**不是**
// 什么。这一层没有任何逻辑，全是形状和读法。
//
// 源: packages/llm/token-meter/src/projection.ts

package tokenmeter

// TokenUsageView 是整份日志累计下来的、提供方报回来的用量。
//
// 源: packages/llm/token-meter/src/projection.ts:13-18
//
// 四个桶**互不重叠**。特别是推理 token 已经含在 OutputTokens 里，这里不再另加一笔
// ——[llm.TokenUsage].ReasoningTokens 说的是「输出里有多少花在推理上」，
// 把它加进来就是把同一批 token 算两遍。
type TokenUsageView struct {
	// UncachedInputTokens 是真正按输入计费的那部分，不含缓存命中和写入。
	UncachedInputTokens int `json:"uncachedInputTokens"`
	// OutputTokens 是产出的 token，已含推理。
	OutputTokens int `json:"outputTokens"`
	// CacheReadTokens 是命中提示词缓存的那部分。
	CacheReadTokens int `json:"cacheReadTokens"`
	// CacheWriteTokens 是写进提示词缓存的那部分。
	CacheWriteTokens int `json:"cacheWriteTokens"`
}

// ContextPressureView 是给状态栏看的那份上下文占用估算。
//
// 源: packages/llm/token-meter/src/projection.ts:30-48
//
// 三个字段在场的时候**有意不构成一次原子的请求观测**：每一个都是各自那一刻的
// last-wins 记录。所以换模型之后，一个新的容量会和上一条路由的压力配在一起，
// 直到下一次请求报回用量为止。这是明知的取舍——它是给人看的参考值，
// 不是计费依据，也不是任何一道闸门的输入。
type ContextPressureView struct {
	// PressureTokens 是最近一次请求的提示词规模：未缓存输入加上缓存读写。
	//
	// **不含**响应输出，所以当前这个回合正在流式产出时它是不动的。
	// 在提供方第一次报回用量之前它是缺失的。
	PressureTokens *int `json:"pressureTokens,omitempty"`
	// ProjectedTokens 是**下一次**请求的提示词大概要花多少：PressureTokens 加上
	// 那次采样之后表面上的净重新定价。
	//
	// 只有增量是估的，所以这个数始终锚在提供方那边，同时又能在一次压缩盖掉
	// 一段表面的当下立刻反应过来——那件事 PressureTokens 自己看不见，
	// 因为压缩不产生任何用量。同样在第一次采样之前缺失。
	ProjectedTokens *int `json:"projectedTokens,omitempty"`
	// ContextWindow 是最近记下的那条路由的容量；没有适配器公告过就是缺失。
	ContextWindow *int `json:"contextWindow,omitempty"`
}

// ContextBreakdownView 是下一次请求的上下文由什么组成——不是它值多少钱。
//
// 源: packages/llm/token-meter/src/projection.ts:59-66
//
// 三个数全部用这个计量器那把固定尺子量，所以它们**加起来不等于**
// [ContextPressureView.ProjectedTokens]：这套启发式系统性地低估中日韩文本、
// 高估 JSON schema，而把占用值锚在提供方那边正是为了把这份误差挡在外面。
// 这三个数只能当成「组成比例」看，绝不能当成一个总量。
type ContextBreakdownView struct {
	// SystemTokens 是最新那份请求头里系统提示的估价；第一次请求之前是 0。
	SystemTokens int `json:"systemTokens"`
	// ToolsTokens 是最新那份请求头里工具表的估价；第一次请求之前是 0。
	ToolsTokens int `json:"toolsTokens"`
	// MessageTokens 是当前这条模型可见表面的估价。
	MessageTokens int `json:"messageTokens"`
}
