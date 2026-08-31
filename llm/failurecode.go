// 本文件的作用：那几个与提供方无关的稳定失败码，以及从提供方那段自由文本里
// 认出「上下文窗口超了」和「额度用光了」的两个判据。
//
// 源: packages/llm/llm/src/error.ts:24-100
//
// 这些码是**路由用的**：适配器把各家五花八门的报错归一成其中一个，上层
//（重试策略、压缩触发、凭据提示）只认码，永远不去解析 Message。

package llm

import "regexp"

const (
	// ContextWindowExceededCode 是「这次请求因为超出上下文窗口被拒」的规范码。
	//
	// 源: packages/llm/llm/src/error.ts:24-25
	ContextWindowExceededCode = "CONTEXT_WINDOW_EXCEEDED"

	// QuotaExceededCode 是「账户额度或者余额用完了」的规范码。
	//
	// 源: packages/llm/llm/src/error.ts:27-28
	QuotaExceededCode = "QUOTA"

	// EmptyResponseCode 是「一次正常结束、却一个内容块都没带」的规范码。
	//
	// 源: packages/llm/llm/src/error.ts:30-39
	//
	// 提供方偶尔会吐出这种退化的完成（终止停止 + 零输出）。适配器把它归成一次
	// 失败，而不是交出一条空的助手消息——空消息会让回合悄无声息地结束，用户和
	// 循环都没有东西可接。这次尝试没有留下任何耐久产物，所以重试策略默认认为
	// 它可以安全地重来。
	EmptyResponseCode = "EMPTY_RESPONSE"

	// InvalidCredentialCode 是「给了凭据但用不了」的规范码——是格式坏了，不是没给。
	//
	// 源: packages/llm/llm/src/error.ts:41-48
	//
	// 它和 MISSING_CREDENTIAL 分开，是因为修法不同：一个要改存着的那个值，
	// 另一个要补一个进去。它**有意**不在默认可重试集合里——一个坏掉的凭据
	// 每一次尝试都会以完全相同的方式失败。
	InvalidCredentialCode = "INVALID_CREDENTIAL"
)

// 源: packages/llm/llm/src/error.ts:50-71。三条命名的模式加 isContextWindowExceededError
// 里两条内联的，一起构成「明确点名了上下文这个界」的措辞集合。
//
// 新增: DSH 的正则带 `i` 标志，Go 里写成模式开头的 `(?i)`；RE2 在这个标志下同样
// 会把 `[^a-z0-9]` 这类否定字符组一起折叠大小写，所以两边的判定完全一致。
// 这些模式里没有反向引用和前后瞻，RE2 全都支持。
var (
	// structuredContextOverflow 认的是结构化的码，以及直白点名某个上下文界被超了的说法。
	//
	// 源: packages/llm/llm/src/error.ts:50-55
	structuredContextOverflow = regexp.MustCompile(
		`(?i)(?:^|[^a-z0-9])context[\s_-](?:length|window)[\s_-]` +
			`(?:exceed(?:ed|s)?|overflow(?:ed)?|limit[\s_-]exceeded)(?:$|[^a-z0-9])`)

	// tooLargeForContext 认的是把「太大了」直接系在模型上下文容量上的措辞。
	//
	// 源: packages/llm/llm/src/error.ts:57-63
	tooLargeForContext = regexp.MustCompile(
		`(?i)\b(?:request|prompt|input|messages?)\s+(?:is\s+|are\s+)?` +
			`too\s+(?:large|long)\s+for\s+(?:(?:this|the)\s+)?` +
			`(?:model(?:'s)?\s+)?context(?:\s+window)?\b`)

	// exceedsModelContext 认的是 exceeds 那一族说法——只有它的宾语明确是模型
	// 上下文时才算数。
	//
	// 源: packages/llm/llm/src/error.ts:65-71
	exceedsModelContext = regexp.MustCompile(
		`(?i)\b(?:input|prompt|request|messages?)\b.{0,40}` +
			`\b(?:exceed(?:s|ed)?|overflows?|is\s+larger\s+than)\b.{0,40}` +
			`\b(?:the\s+)?(?:model(?:'s)?\s+)?context(?:\s+(?:length|window))?\b`)

	// maxContextLength 是 isContextWindowExceededError 里第二条内联模式。
	//
	// 源: packages/llm/llm/src/error.ts:82
	maxContextLength = regexp.MustCompile(
		`(?i)\b(?:maximum|max)(?:\s+(?:allowed|supported))?\s+context\s+(?:length|window)\b`)

	// tooLongForModel 是 isContextWindowExceededError 里第四条内联模式。
	//
	// 源: packages/llm/llm/src/error.ts:84
	tooLongForModel = regexp.MustCompile(
		`(?i)\b(?:input|prompt|request)\s+(?:is\s+)?too\s+(?:long|large)\s+for\s+(?:this|the)\s+model\b`)
)

// 源: packages/llm/llm/src/error.ts:94-100。五条模式，认的都是「额度这件事到头了」，
// 而不是「这一瞬请求太密」——后者是 RATE_LIMIT，是可重试的。
var quotaExhausted = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\binsufficient[\s_-]+(?:quota|balance|credits?)\b`),
	regexp.MustCompile(`(?i)\b(?:quota|usage[\s_-]+limit)[\s_-]+(?:exceeded|exhausted|reached)\b`),
	regexp.MustCompile(`(?i)\bexceed(?:ed|s)?[\s_-]+(?:(?:your|the)[\s_-]+)?(?:current[\s_-]+)?quota\b`),
	regexp.MustCompile(`(?i)\b(?:balance|credits?)[\s_-]+(?:exhausted|depleted)\b`),
	regexp.MustCompile(`(?i)\bout[\s_-]+of[\s_-]+(?:credits?|budget)\b`),
}

// IsContextWindowExceededError 认出 OpenAI 兼容提供方与各家库适配器用的那些
// 上下文溢出措辞。
//
// 源: packages/llm/llm/src/error.ts:73-86
//
// 适配器把手上所有的提供方码、类型、消息文本拼成一段交进来，于是「抛出来的」
// 和「随流带回来的」两种报错走同一个判据。
func IsContextWindowExceededError(detail string) bool {
	return structuredContextOverflow.MatchString(detail) ||
		maxContextLength.MatchString(detail) ||
		tooLargeForContext.MatchString(detail) ||
		tooLongForModel.MatchString(detail) ||
		exceedsModelContext.MatchString(detail)
}

// IsQuotaExceededError 认出「账户额度到头了」而不是「这一瞬请求太密」的措辞。
//
// 源: packages/llm/llm/src/error.ts:88-100
//
// 只对额度、余额、信用、预算、用量上限这几族终局说法为真。
func IsQuotaExceededError(detail string) bool {
	for _, pattern := range quotaExhausted {
		if pattern.MatchString(detail) {
			return true
		}
	}
	return false
}
