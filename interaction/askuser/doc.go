// Package askuser 是 ask_user_question 那件面向模型的工具：它把模型写出来的一批
// 问题交给 [github.com/snight1983/ds-harness-go/interaction/userquestions] 那道接缝，等人回答，再把答案
// 当成一次普通的工具结果送回循环里。
//
// 对应 DSH 的 @deepseek-ai/dsh-tool-ask-user（packages/interaction/tool-ask-user）。
//
// 源: packages/interaction/tool-ask-user/src/index.ts:1-7
//
// # 它自己什么都不判断
//
// 这个包是那条能力的**消费方**，不是它的守门人：一批空问题、一个被拥有的子 agent、
// 一个对不上的呈现意图，全部由 [userquestions.Service.Ask] 拒掉。这里只做形状转换
// ——把模型那份 snake_case 的参数翻成接缝的形状，再把答案翻回模型看得懂的形状。
//
// 拒绝的话术因此也只有一份：接缝报的错原样成为这次工具调用的失败，而
// [userquestions.Error] 带着的 ErrorName/ErrorCode 会被 [github.com/snight1983/ds-harness-go/core/tools]
// 那道结果收敛抄进 Failure.Info，模型和上层都不必解析错误文本。
//
// # 和 DSH 不一样的地方
//
// 新增: DSH 靠 cordis 的 ctx.userQuestions 拿到那道接缝。Go 里它是 [Config.Questions]
// 这个显式依赖——没有它就造不出这件工具。
//
// 新增: DSH 把 exec.signal 塞进请求对象一起传下去。Go 里取消是 Execute 的第一个
// 参数，原样传给 [userquestions.Service.Ask]，和本仓库其余地方一致。
//
// 新增: DSH 用 `...x !== undefined ? { k: x } : {}` 这种展开来省掉没给的可选字段。
// Go 的零值天然就是「没给」，翻过去时直接赋值即可——一个空的 header 和没有 header
// 在界面上是同一件事。
package askuser
