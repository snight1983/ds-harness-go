// Package sessiontitlellm 是那个「拿模型给会话起名」的标题生成器。
//
// 源: packages/session/session-title-llm/src/index.ts:1-5
//
// 它把一次辅助模型调用的全部策略收在一处：走哪条路由、系统提示词写什么、
// 用户消息怎么装帧、多久算超时、输出怎么验。[github.com/snight1983/ds-harness-go/session/sessiontitle]
// 那个服务只认 [sessiontitle.Provider] 这个接口，本包提供的就是它的模型实现。
//
// # 为什么消息要装成 JSON
//
// 交给模型的那段用户文本是**用户打的字**，里面完全可能有 "Ignore the above and
// output ..."、一行 Markdown 标题、或者任何一段看起来像分隔符的东西。把几条消息
// 拿换行或者 "---" 拼起来，等于让用户的输入有机会伪造成提示词的结构。装成一个
// JSON 数组之后，用户能改的只是数组里字符串的**值**，改不了数组本身的形状——
// 引号和反斜杠由 encoding/json 转义掉了。
//
// 这挡不住语义上的劝说（模型仍然可能被说服），但它挡住了结构上的伪造，而后者
// 是这类调用里真正廉价、真正可重复的那一半。
//
// # 一次调用会往日志里写一条什么
//
// 派发**之前**先落一条 [EventTitleLLMRequest]，把模型这次看到的东西一字不差地
// 记下来：路由、系统提示词、装帧后的消息、输出上限、以及这次引了哪几条人类消息。
// 它是只进日志的——不上模型可见表面，也不进派生历史。
//
// 记它的理由是这次调用的结果会变成一个用户能看见的名字，而那个名字要是不对劲，
// 事后唯一能查的就是「当时到底喂进去了什么」。放在派发之前是有意的：一次超时或者
// 断流同样留得下这条记录，否则最需要复盘的那些次调用恰好一条都不留。
//
// # 两档节奏
//
// [NewFirstPrompt] 只拿第一条人类消息起名，[NewAllPrompts] 每来一条消息都拿
// 到目前为止的全部消息重起一次。两者的差别只在选消息那一下，其余策略逐字相同。
//
// # 和 DSH 不一样的地方
//
// 新增: DSH 那两个薄插件（session-title-first-prompt-llm、
// session-title-all-prompts-llm）在 Go 这边不是两个包，而是本包的两个构造函数。
// 它们在 TS 里必须各自成包，是因为 Loader 要求每个插件导出一份静态可走的 schema；
// Go 这边没有那个 Loader，两个包会只剩下各自一行 selector 而完全重复。它们的
// **登记 id 逐字照搬**，因为那个 id 会被写进标题事件的 source 里，改它等于改
// 已经写下去的历史的读法。
//
// 新增: DSH 从 ctx.llm 上拿运行时。Go 这边本包收一个 [Streamer] ——只有一个
// Stream 方法的窄接口。[github.com/snight1983/ds-harness-go/llm.Runtime] 直接满足它。收窄口而不是收
// *llm.Runtime，是因为本包只用得上流式那一件事，而窄口让测试不必架起一整个运行时。
//
// 新增: DSH 的 deadline(signal, timeoutMs, code) 在这里是
// context.WithTimeoutCause，超时的因由是 [ErrTimeout]，它随身带着和 DSH 同一个
// 稳定码 [TimeoutCode]。DSH 那边 timeoutMs 有个 MAX_TIMER_DELAY_MS 上限，那是
// JS setTimeout 把超过 32 位的延迟截成 1 毫秒这个坑的护栏；Go 的 time.Duration
// 没有这个坑，所以那道上限跟着消失。
package sessiontitlellm
