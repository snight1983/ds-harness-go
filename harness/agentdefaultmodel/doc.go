// Package agentdefaultmodel 拥有一份答案：一个没有自带模型选择的 agent，该用哪个模型。
//
// 对应 DSH 的 @deepseek-ai/dsh-agent-default-model（packages/core/agent-default-model）。
//
// 源: packages/core/agent-default-model/src/index.ts:1-107
//
// # 这个包管什么
//
// [New] 用一份装配配置造出 [Service]，[Service.CurrentSelection] 读出当下那份选择，
// [Service.SaveSelection] 整段换掉它。[Settings] 是这份选择存下来的形状，
// [SettingsNamespace] 是它在设置文档里占的那个命名空间。
//
// 不管的：**谁去读它**。哪个 agent 入口在什么时候取这份选择、取到之后怎么用，
// 是那些入口自己的事。本包不认识任何宿主、任何传输层，也不持有任何 agent。
//
// # 为什么它是一个包而不是一个字段
//
// 「默认模型」看着像装配时传一个字符串就完了，但它有一条装配传不了的性质：
// **用户能改，而且改完下一次读就得看见**。如果它是各个 agent 入口自己存的一份值，
// 那么每一处都要自己接设置服务、自己处理「设置服务撤走了怎么办」，
// 而且改一次要通知多少处、有没有漏掉一处，谁也说不清。
//
// 收成一个包之后，这件事只有一个答案，且**每次都重新读**——
// 见 [Service.CurrentSelection] 上那段注释：没有任何登记级的事实需要跟着重建。
//
// # 两层来源，不是一个可变值
//
// 挂了设置服务时，装配那一份作为组装层压进登记里（[settings.Options].Base），
// 用户段叠在它之上。这样配置界面能分别看见「这次部署给的是什么」和「用户改成了什么」，
// 而不是只剩一个合并后的结果。
//
// 没挂设置服务时本包不登记任何东西，装配那一份就是冻住的答案；
// 此时 [Service.SaveSelection] 静默成功而不是报错——这条路上「存不下来」不是故障，
// 是这次部署本来就没有可写的存储。撤销登记会退回装配那一份，而不是冻在最后读到的
// 那个用户值上，理由见 [New]。
//
// # 这里和 DSH 不一样的地方
//
//  1. **那个类改名叫 [Service]。**DSH 叫 AgentDefaultModelConfig，在 Go 里连上包名
//     会变成 agentdefaultmodel.AgentDefaultModelConfig——又结巴、又和本包的 [Config]
//     （装配入口，DSH 里是另一个类型）撞名。
//
//  2. **「挂没挂设置服务」从 ctx.inject 变成一个 nil 判断。**DSH 那边是
//     `ctx.inject(['settings'], ...)`，没挂时整段登记根本不跑；Go 里没有那个万能上下文，
//     所以它就是 [Config].Settings 这个字段是不是 nil。
//
//  3. **可选的推理档位用空串表达，不用指针。**这条约定是 [github.com/snight1983/ds-harness-go/llm.CallConfig]
//     定下的，[github.com/snight1983/ds-harness-go/harness/agent.ModelSelection] 已经照用，这里跟着用，
//     好让三处不必来回翻译指针和零值。
//
//  4. **schema 的 required 变成 [validateSettings]。**Go 里没有那套运行期 schema，
//     「必填」就是这个函数。它同时装在设置登记的 Validate 上，所以用户改出来的值
//     和装配传进来的值走的是同一道检查。
//
//  5. **多了一把锁。**DSH 是单线程 JS，那个 source 函数换来换去不需要同步；
//     Go 里撤登记的一方和读选择的一方是两个 goroutine。锁只护 [Service] 那个
//     scope 字段，entry 是造出来就不变的。
//
// # 这个包没有不变量
//
// DSH 留了一个空的 installer，好让「这里确实什么都不查」在一份组装出来的不变量清单里
// 显式可见。Go 这边没有那种清单，所以只留下 [PackageName]——一个包要在注册表里占住
// 自己的所有权，得先有名字——而没有 RegisterInvariants。原因见 invariant.go。
package agentdefaultmodel
