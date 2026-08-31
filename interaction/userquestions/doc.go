// Package userquestions 是「把 agent 停在这儿，等人回答」这条能力的接缝：一个
// 界面侧的提供方插槽，加上一个 Ask。
//
// 对应 DSH 的 @deepseek-ai/dsh-user-questions（packages/interaction/user-questions）。
//
// 源: packages/interaction/user-questions/src/index.ts:1-8
//
// # 这个包自己不问任何人
//
// 它一个界面都不画。真正把问题摆到人面前的是[Provider]——终端、桌面端、
// 网页端各自实现一个，一个上下文里只准有一个活着的。本包做的是它前面那道门：
// 在请求还没离开进程之前，把那些「送到界面上就已经错了」的请求拦下来。
//
// 拦在这里而不是拦在每一个界面里，理由是错在提问方：一个界面就算发现了，
// 它也只能把一个坏问题画得好看一点。
//
// # 面向模型的那件工具不在这儿
//
// ask_user_question 那件工具在 [ds-harness-go/interaction/askuser]。本包只有接缝，
// 所以一个无界面的装配（批处理、回放）根本不装它，模型也就问不出问题来。
//
// # 和 DSH 不一样的地方
//
// 新增: DSH 的 ask 收一个活的 Agent 对象，然后向 ctx.agents 求证「这就是那个活着的
// 实例吗」「它是不是被另一个 agent 拥有」。Go 这边 agent 在工具层是一个不透明的
// 作用域键（[ds-harness-go/core/scope.Key]），活 agent 注册表是循环那一块的东西，
// 所以这道求证是 [Config.CallerStatus] 这条显式的接缝——装配方接上去它才生效，
// 不接就等于「没有活注册表」，和 DSH 里 ctx.get('agents') 拿到 undefined 是同一件事。
//
// 新增: DSH 的 AskUserQuestionIntent 是一个带 kind 判别字段的联合类型。Go 里它是
// [Intent] 这个封闭接口加 [PlanReviewIntent] 一个变体——封印方法未导出，所以变体
// 只能在本包里加，而一个不认识的变体在界面侧会走通用问答那条路，和 DSH 一样。
//
// 新增: DSH 用 signal?: AbortSignal 传取消。Go 里取消是 [Ask] 的第一个参数，
// 和本仓库其余地方一致。
//
// 新增: DSH 的 UserQuestionError 继承 HarnessError，靠 instanceof 认。Go 里
// [Error] 只是一个普通错误类型，靠 errors.As 认；它同时带上 ErrorName 和
// ErrorCode 两个方法，于是 [ds-harness-go/core/tools] 那道结果收敛能把它的身份
// 原样抄进 Failure.Info，下游不必解析错误文本。
//
// 新增: DSH 里 detail 和 custom 都能是 undefined。Go 的字符串零值就是空串，
// 本包把「没给」和「给了空串」当成同一回事：一份空的计划正文和没有计划正文一样
// 看不见，一段空的自由文本和没写一样没内容。
package userquestions
