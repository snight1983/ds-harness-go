// Package todo 是给模型的待办清单：一件**整表替换**的工具，加上它自己拥有的
// 那份投影和那条持久不变量。
//
// 对应 DSH 的 @deepseek-ai/dsh-tool-todo（packages/todo/tool-todo）。
//
// 源: packages/todo/tool-todo/src/index.ts:1-6
//
// # 为什么是整表替换，不是逐条编辑
//
// 每一次 todo_write 都带着**完整的**清单，追加成一条 [ds-harness-go/session.EventTodoWrite]
// 事件；回放时最后写的那份生效。这条规矩是这个包全部设计的地基：
//
//   - 没有「加一条」「改第三条的状态」这类局部操作，也就没有「模型以为的清单」
//     和「日志里的清单」分叉的可能——每一条事件本身就是一份完整的事实。
//   - 折叠因此是 last-wins 的一行代码，任何读的一方（界面、检查点、冷读）
//     都不需要重放一串增量。
//   - 一次被拒的调用不留任何痕迹：校验全部发生在追加**之前**。
//
// # 这个包有三条胳膊，各自独立装
//
//   - [Tool.Install] 把 todo_write 注册进工具注册表。这是必须的那条。
//   - [RegisterProjection] 把 todos 这个投影单元登进
//     [ds-harness-go/session/projection.Registry]。可选：装配里没有投影注册表时
//     就不装，界面读到的就是「这个能力不在」。
//   - [RegisterInvariants] 把持久快照的形状检查登进
//     [ds-harness-go/invariants.Registry]。也是可选的，诊断能力开不开是部署的事。
//
// 新增: DSH 是一个 cordis 插件，三条胳膊在同一个 apply 里靠 ctx.inject 按服务
// 在不在场自动决定装不装。Go 里没有那个容器，装配方自己知道手上有没有投影注册表
// 和不变量注册表，所以这里就是三个显式的函数。
//
// # 单活还是并行，由部署定，且**不进**持久规则
//
// [Config.AllowParallelInProgress] 同时改两样东西：给模型看的那段说明怎么写，
// 以及一次标了好几条 in_progress 的调用收不收。它是一条**当下的**策略。
//
// [ValidateEvent] 因此故意对「有几条在做」保持沉默——一份在允许并行的时候写下的
// 日志，在部署收紧策略之后必须仍然能回放。把不变量绑在当下的配置上，等于让历史
// 因为今天的选择而变得不合法。
//
// 源: packages/todo/tool-todo/src/invariant.ts:15-23
//
// # 和 DSH 不一样的地方
//
// 新增: DSH 的执行体直接写 exec.agent.session.append(...)——它靠结构类型从那个
// agent 对象上摸到一个活会话。Go 这边 [ds-harness-go/core/tools.Execution.Agent]
// 是一个不透明的作用域键，从它到「往哪个会话追加」的映射只有装配方知道，
// 所以那一步是 [Config.Append] 这条显式的接缝。
//
// 新增: DSH 的 Config 是一份 schemastery schema，allowParallelInProgress 标了
// required，好让部署**必须**明确选一个。Go 的 bool 没有「没填」这个状态，
// 零值就是 false，也就是单活那条更紧的纪律——漏填只会更严，不会更松。
//
// 新增: DSH 还有 src/client.ts 和 src/types.ts 两个只做重导出的入口，
// 存在的理由是那边要把 `todos` 这个投影键的类型声明合并进一张全局接口表，
// 同时让浏览器侧只 import 一个 client 命名空间。Go 的投影键是运行期的字符串，
// 类型也就是 []session.TodoItem 本身，不需要这两层。
package todo
