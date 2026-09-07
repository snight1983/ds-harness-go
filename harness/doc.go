// Package harness 是这套运行期对外的门面：宿主装配、生命周期，以及底下那几个运行期包的容器。
//
// 新增: DSH 没有对应物。它那边是一个 CLI 进程，装配散在启动脚本里；本仓库要被别人的
// 服务当组件嵌进去，于是「按什么顺序把这些组件拼起来、又按什么顺序拆掉」本身就得是
// 一段可编译、可运行的代码，而不是一份文档里的编号列表。
//
// # 这个包管什么
//
// [New] 按 docs/embedding.md 那份顺序拼出一份最小闭环，并交出拆除函数。它不接存储
// 后端、不接持久化、不接协议入口——那几样各自需要一个真的介质，只能由宿主自己决定。
//
// # 运行期包在它下面
//
// 底下五个子包是运行期本身：
//
//   - [github.com/snight1983/ds-harness-go/harness/agent]：一个活 agent 长什么样。
//   - [github.com/snight1983/ds-harness-go/harness/agentloop]：驱动它跑回合的 ReAct 循环。
//   - [github.com/snight1983/ds-harness-go/harness/session]：会话活着的那一半。
//   - [github.com/snight1983/ds-harness-go/harness/systemprompt]：模型这一步该看到什么。
//   - [github.com/snight1983/ds-harness-go/harness/agentdefaultmodel]：没自带模型选择的 agent 用哪个模型。
//
// 新增: 它们**没有**收进 internal。收进去的话，一个外部宿主既拿不到 agent.Registry
// 也实现不了 agent.Factory，而那正是这个库唯一的用途；再在这里逐个转发出去，等于用一层
// 一百多个别名换回原样，而 agent.Options、session.Options、systemprompt.Options 三处同名
// 在一个包里还摊不平——转发层要么改名（公开 API 就变了），要么和子包一模一样（那层就白加）。
// 所以它们是公开的，harness 这一层给的是**装配**，不是可见性。
//
// # 不做什么
//
//   - **不接存储后端、不做持久化、不开协议入口。**这三样各自要一个真的介质，只能
//     由宿主决定：落盘归 [github.com/snight1983/ds-harness-go/feature/persistence]
//     和 [github.com/snight1983/ds-harness-go/adapter/datastore]，线上入口归
//     [github.com/snight1983/ds-harness-go/protocol/acp] 与
//     [github.com/snight1983/ds-harness-go/protocol/sdk/sdkserver]。
//   - **不是生产模板，也没有把上面这些一并包办的顶层 Builder。**[New] 只装得出
//     docs/embedding.md 那份顺序里不碰外部介质的那一段。
//   - **不拥有宿主的进程生命周期和请求入口。**服务启停、HTTP/RPC/消息队列、用户
//     身份、租户和权限全部留在宿主那边，本包是被嵌进去的一个组件。
//   - **不管模型账号与路由配置，也不管密钥。**模型路由归
//     [github.com/snight1983/ds-harness-go/llm]，凭据归
//     [github.com/snight1983/ds-harness-go/credentials]。
//   - **不驱动任何回合。**拼完就把句柄交出去了，跑循环的只有
//     [github.com/snight1983/ds-harness-go/harness/agentloop] 一处。
package harness
