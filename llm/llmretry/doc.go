// Package llmretry 回答一件事：一次模型请求失败之后，要不要再试一次、等多久。
//
// 对应 DSH 的 @deepseek-ai/dsh-llm-retry（packages/llm/llm-retry）。
//
// 源: packages/llm/llm-retry/src/index.ts
//
// # 这个包管什么
//
// [Install] 把重试挂到 agent 的请求失败瀑布上并交出拆除函数；那两条落进会话日志的
// 事件是 [EventRetry] 和 [EventRetryStarted]，负载是 [RetryData] 与 [RetryStartedData]，
// 由 [DecodeRetry] 和 [DecodeRetryStarted] 读回来。[EventTypes] 交出这两个类型，
// 好让装配方把它们并进会话词汇。[ProviderForOpenStep] 从一段日志里读出某个还开着的
// 步骤当时选中的提供方。[RegisterInvariants]、[ValidateLog] 和 [Trace] 是这几条事件
// 自己的持久检查。
//
// 不管的：**重试几次、退避多久**。那份策略是适配器在登记路由那一刻定下来的
// （[github.com/snight1983/ds-harness-go/llm.ResolveRetryPolicy]），本包只负责执行它。也不管什么算失败——
// 失败分类和可不可重试是 [github.com/snight1983/ds-harness-go/llm.Error] 上的事实。
//
// 本包只认 [github.com/snight1983/ds-harness-go/core/agent.Registry] 一个宿主。
//
// # 为什么排期和熬过去是两条事件
//
// 源: packages/llm/llm-retry/src/types.ts:6-13
//
// 中间那段等待可以被取消。只写一条的话，一段等到一半就被中止的重试，和一次真的重发
// 出去了的请求，在日志里长得一模一样——而那两件事对「这个步骤到底发过几次请求」是两个
// 不同的答案。两条都**不上表面**：一次重试不改动模型看得见的历史。
//
// # 一条链，不是一个计数器
//
// 源: packages/llm/llm-retry/src/invariant.ts
//
// 同一个步骤上、同一个提供方、同一份策略排出来的那几次重试算一条链，链上的序号接着
// 往下数。策略在两次失败之间被换掉时必须换一条新链：接着上一份策略的账往下数的话，
// 一次把 maxRetries 从 2 调到 5 的改动会被算成「已经重试过 2 次」，而用户看到的是一份
// 写着 5 的新策略。链的身份不许被别人借走，这条由本包的不变量守着。
//
// # 这里和 DSH 不一样的地方
//
//  1. **事件词汇要装配方自己拼。**DSH 靠 `declare module` 把这两个类型合并进
//     SessionEventMap，全局登记表自动认得。Go 没有声明合并，
//     [github.com/snight1983/ds-harness-go/session.Vocabulary] 也是个闭合的值，所以本包交出 [EventTypes]：
//
//     vocabulary := session.CoreVocabulary().With(llmretry.EventTypes()...)
//
//     不这么做的话，一段带重试的日志会被 session.CheckVocabulary 判成
//     「有不认识的事件类型」而整个拒掉。
//
//  2. **不变量那两条胳膊由装配方交进来。**DSH 从 cordis 上拿——ctx.sessions.list()
//     取历史、ctx.on('internal/dispatch') 截住后来的。活会话服务是循环那一块的东西，
//     本包在它下面，所以 [RegisterInvariants] 收两个函数。
//
//  3. **那个「只能是空对象」的 Config 整个消失。**DSH 的 Config 是
//     `Readonly<Record<string, never>>`，配一个 validateConfig 专门拒掉误写进来的
//     retryPolicy 键。Go 的结构体写不出表外的字段，那一整套连同它的错误消息一起不需要。
//     [Options] 里剩下的是 DSH 的 RetryInternals（测试注入口）和 Go 这边必须显式交进来
//     的宿主。
//
//  4. **拆除等在跑的恢复，换了个等法。**DSH 用一个 Set<Promise> 记账，
//     Promise.allSettled 等干净。Go 这边是 [sync.WaitGroup]，但进出口都要走同一把锁——
//     在观察者里裸调 Add 会和拆除那一侧的 Wait 撞成一次真正的数据竞争。
//     拆除的次序照搬 DSH：先摘观察者、再取消 lifetime、最后等收尾，理由见 [Install]。
//
//  5. **[ProviderForOpenStep] 多一个 error。**DSH 那个函数只有一个返回值，
//     `undefined` 同时表达「步骤没开着」和「没有表头」，而负载读不回来在 TS 里根本
//     不可能发生。Go 这边负载是一段字节，读不回来是真会出现的第三种结果——把它和
//     「没找到」混成同一个返回值的话，一份坏掉的日志会被静静地当成「这个步骤没开着」。
package llmretry
