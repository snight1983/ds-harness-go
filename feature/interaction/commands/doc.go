// Package commands 是那张由插件自己填的人类命令注册表：一行 `/foo bar` 从解析、
// 按 agent 的作用域解析、图片准入，一路走到处理器与生命周期审计。
//
// 对应 DSH 的 @deepseek-ai/dsh-commands（packages/interaction/commands）。
//
// 源: packages/interaction/commands/src/index.ts:1-4
//
// # 它解决的是什么
//
// 界面上那些斜杠命令**不进模型**。它们是人对着这套装置本身说的话（切个策略、看眼
// 状态、起个任务），所以它们既不该占模型的上下文，也不该由模型来决定跑不跑。
// 这张表把「有哪些命令」和「按下去之后干什么」这两件事从每一个界面适配器里抽出来，
// 变成一份大家共用的、可以被插件扩充的登记。
//
// # 一次执行按这个顺序发生
//
//  1. **解析**。[Parse] 只认 `/名字` 后面紧跟行尾或者一个空白的那种行，并且
//     **不对**尾巴上的输入做任何归一化。
//  2. **解析到定义**。作用域登记盖住全局登记，所以同一个名字对不同的 agent
//     可以是不同的实现。
//  3. **围栏**。准入没过（语法不成立、名字不认得、或者已经取消了）什么都不写：
//     那几种情况从没进过处理器。
//  4. **落 command/run**。这一条写不进去就大声失败——后面那条 command/done 会变成
//     一条配不上对的孤儿记录。
//  5. **图片准入**。没声明收图、仓库没装、超了限额，三种都在处理器跑起来之前结算成
//     错误结果，而一批被拒的图不发布任何持久对象。
//  6. **跑处理器**，然后**落 command/done**。一个炸了的、或者被取消的处理器也在这里
//     落成 kind=error，然后那个错误继续抛给调用方。
//
// 这一对生命周期事件是**直接的、只进日志的**追加：没有回合把它们包起来，持久化在
// 平常的检查点上把它们排出去。这一点由 [Trace] 盯着：同一条日志里配对号不重复，
// 每一条 command/done 都找得到自己那条 command/run。
//
// # 和 DSH 不一样的地方
//
// 新增: DSH 靠 cordis 从 `agent.session` 直接拿到活会话、从 `ctx.get('attachments')`
// 拿到附件仓库。Go 里活会话是循环那一块的东西（见 docs/DESIGN.md 第八节），本包在
// 第 4 块，所以这两条变成 [Options.LogOf] 和 [Options.Attachments]。
//
// 新增: DSH 的 CommandRuntime 继承 TypertRemoteService，`list` 和 `execute` 上挂着
// @Remote 装饰器，好让浏览器侧直接调过来。那套 RPC 传输在第 9 块，本包只是普通的
// Go 方法；把它们摆上线由 sdk/server 那一层负责。
//
// 新增: DSH 用 AbortSignal 和一个 promise 赛跑来甩掉不合作的处理器，并且用
// `signal.reason` 携带那句给人看的取消理由。Go 里这两件事分别是
// [context.Context] 和 [context.Cause]——装配方用 [context.WithCancelCause]
// 带上自己那句话，那句话会原样落进 command/done。
//
// 新增: DSH 那边一多半的登记校验在验类型（description 是不是字符串、handler 是不是
// 函数、images 是不是布尔），因为一个 JS 插件递得进任何东西。Go 的类型系统把那几条
// 判掉了，只剩下类型表达不了的：名字的形状、两个不能全是空白的字符串、以及处理器
// 交回来的那个 kind。
package commands
