# Agent

## 模块定位

`core/agent` 定义一个“活 Agent”在进程内的公共契约，并管理它从登记、公布到摘除的生命周期。

它位于宿主、协议层、多 Agent 协作模块和具体 ReAct 循环之间。上层只依赖这里的 `Agent`、`Factory` 和 `Registry`，不需要知道循环的具体实现；真正创建 Agent、恢复会话并驱动回合的是 `core/agentloop`。

一句话概括：`core/agent` 管“Agent 是什么、现在有哪些 Agent、消息如何进入 Agent、运行过程如何被扩展”，不管“模型和工具具体怎样跑”。

## 干了什么

该模块提供七组能力：

1. 用 `Agent` 接口描述活 Agent 的身份、会话、状态、作用域和输入操作。
2. 用 `Factory` 隔离 Agent 的创建/恢复与具体循环实现。
3. 用 `Registry` 管理活 Agent 的登记、公布、查询、归属和摘除。
4. 用 `Inbox` 表示尚未进入模型历史的待处理消息，并把每次改动写入会话事件日志。
5. 提供 12 组 Observer，让宿主或其他模块扩展生命周期、步骤、模型请求和错误处理。
6. 用 `context.Context` 传递调用链的 Agent 发起者。
7. 提供动态模型选择和已消费工作折叠等运行时辅助能力。

## 架构

```text
宿主 / ACP / 子 Agent / 后台任务
                 |
                 | 只依赖公共契约
                 v
        +-------------------+
        |    core/agent     |
        |-------------------|
        | Agent / Factory   |
        | Registry          |
        | Inbox             |
        | Observers         |
        | Initiator         |
        +-------------------+
          |              ^
          | Factory 实现 | 生命周期与运行事件
          v              |
        core/agentloop ----+
          |
          +-- core/session
          +-- core/systemprompt
          +-- core/tools
          +-- llm
          +-- session event log
```

这个切分让两侧保持稳定：

- 消费方只面对 `Agent` 和 `Registry`，不绑定 ReAct 循环的实现细节。
- 循环通过实现 `Factory`、`Agent` 并调用 Registry 的派发方法接入公共运行时。
- 宿主通过 Observer 和作用域注册扩展能力，不需要修改 Agent 核心接口。

## 核心对象

| 对象 | 职责 | 不承担的职责 |
|---|---|---|
| `Agent` | 暴露一个活 Agent 的身份、会话、状态、作用域和输入控制面 | 不规定 ReAct 循环实现 |
| `Factory` | 创建新 Agent，或从持久化会话恢复 Agent | 不由本模块提供具体实现 |
| `Registry` | 管理活 Agent、Factory 和 Observer | 不持久化 Agent，不驱动回合 |
| `Handle` | 同时交付 `Agent` 与一次性 `Dispose` 能力 | 不向普通查询方暴露销毁权限 |
| `Inbox` | 维护 `next-turn`、`next-step` 两条待办队列的当前状态 | 不是模型可见的会话历史 |
| `ModelSelectionRef` | 在步骤边界一致地切换模型和推理档位 | 不实现模型路由器 |
| Initiator Context | 标记一条调用链由哪个 Agent 发起 | 不代表存活证明或授权 |

## Agent 公共契约

`Agent` 接口提供以下能力：

| 方法 | 含义 |
|---|---|
| `ID` | Agent 与会话共用的运行身份 |
| `Options` | 默认模型提供方、模型和最大输出 Token |
| `Session` | 当前活会话；会话事件日志是耐久事实来源 |
| `Inbox` | 尚未进入模型历史的待处理消息状态 |
| `Status` | 当前为 `idle` 或 `running` |
| `Scope` | Agent 局部资源与 Observer 的作用域 |
| `Cancel` | 中止当前活动，并按选项清理或保留 Inbox |
| `WhenIdle` | 等待 Agent 的回合和维护任务全部静止 |
| `RunMaintenance` | 在真正空闲期串行执行非回合维护任务 |
| `Send` | 向指定 Inbox 队列发送消息，并决定是否唤醒 |
| `Followup` | 排入下一回合并唤醒驱动 |
| `Steer` | 向最近的步骤边界加入引导并唤醒驱动 |
| `Inject` | 向下一步骤注入模型可见上下文，但不主动唤醒 |
| `Prepend` | 把消息放回指定队列头部，但不主动唤醒 |

Agent 状态只有 `idle` 和 `running`。Dispose 不是第三种状态：Dispose 完成后，Agent 会从 Registry 中消失。

## 创建、恢复与生命周期

### 创建和恢复

`Registry` 自身不会构造 Agent。`core/agentloop` 等实现方先通过 `SetFactory` 注册 `Factory`，消费方再调用：

- `Registry.Create`：用 `CreateOptions` 创建新会话和 Agent。
- `Registry.Resume`：用 `ResumeOptions` 读取持久化会话并恢复 Agent。

`CreateOptions` 包含会话 ID、工作目录、分叉信息、委派深度、初始事件、Agent 模型选项和创建期 `Setup`。`ResumeOptions` 包含待恢复会话 ID、Agent 模型选项和恢复期 `Setup`。

`Setup` 在 Agent 尚未公布时执行，用于向 Agent 作用域装配工具、提示词、变量和监听器。它只能组装环境，不能提前驱动 Agent。可选的 commit 会在正式登记和公布前同步执行；失败时整条创建事务回滚。

### Registry 生命周期

```text
Registry.Create / Registry.Resume
              |
              v
        Factory 创建或恢复
              |
              v
       Setup -> commit
              |
              v
       Registry.Enter
       已登记，尚未公布
              |
              v
       Registry.Announce
       created Observer
              |
              v
       session-start -> 循环启动
              |
              v
       Handle.Dispose
       停止、排干、摘除并释放作用域
```

`Register` 是 `Enter + Announce` 的普通入口。需要把摘除动作编入更大回滚链的 Factory 使用 `Enter` 和 `Announce` 两步形式。

关键不变式：

- Agent ID 必须与其 Session ID 一致。
- 同一 ID 同时只能存在一个活登记。
- `Announce` 对同一登记只能执行一次。
- `created` Observer 可以否决公布；失败后登记回滚，并对已经看到创建事件的 Observer 配对发出 `disposed`。
- `Enter` 返回的摘除函数是幂等的，而且旧生命周期的摘除能力不能删除后来创建的同名 Agent。
- `Handle.Dispose` 是能力对象；`Registry.Get` 只返回 `Agent`，不会把销毁权限交给任意查询方。

### 查询与归属

Registry 支持：

- `Get`：按会话/Agent ID 查找活 Agent。
- `List`：按登记顺序返回所有活 Agent。
- `Roots`：按登记顺序返回运行期没有 owner 的根 Agent。
- `IsOwnedBy`：判断某个活 Agent 是否由指定 Agent 的作用域创建。

运行期 owner 与持久化会话的分叉血统是两套关系。一个从分叉会话恢复的 Agent，也可以在当前进程中成为根 Agent。

## Inbox 与事件日志

Inbox 是根据事件记录整理出的当前待办状态，源码中称为 projection。它不是聊天历史，包含两条有序队列：

| 队列 | 用途 |
|---|---|
| `NextTurn` / `next-turn` | 等待各自开启新回合的普通输入 |
| `NextStep` / `next-step` | 等待最近一个步骤边界消费的引导或附加上下文 |

主要操作包括 `NextTurn`、`NextStep`、`HasPending`、`Clear`、`Claim`、`Append`、`Prepend`、`Replace`、`Remove` 和 `Splice`。

每次变更都先向会话追加 `agent/inbox/spliced` 事件，再更新内存中的当前状态。事件记录目标队列、规整后的起点、删除数量、插入消息和是否属于取消。因此：

- Agent 恢复时可以从事件日志重建 Inbox。
- 认领与未运行即取消可以被区分。
- `FoldConsumedWork` 可以只依赖日志判断最后一个已交代的回合，以及是否仍有被丢弃但未运行的工作。
- 同一消息 ID 不允许同时或重复出现在两条队列中。

`Claim(NextStep, turn)` 会认领整条 `next-step`；`Claim(NextTurn, turn)` 还会认领 `next-turn` 队首的一条消息。消息真正进入模型可见历史时，由循环写入正式的用户消息事件。

## Observer 扩展模型

Registry 提供 12 组按作用域登记的 Observer：

| 派发方式 | Observer | 语义 |
|---|---|---|
| 通知 | `created` | 创建公布；唯一可以返回错误并否决的通知 |
| 通知 | `disposed`、`status` | 生命周期摘除和状态跃迁 |
| 通知 | `inbox-inserted`、`inbox-claimed`、`inbox-discarded` | Inbox 消息变化 |
| 通知 | `session-start`、`error` | 会话启动和运行错误 |
| 串行 | `turn-stopping` | 回合关闭前按登记顺序执行；可通过 `Steer` 追加工作 |
| Waterfall | `pre-step` | 接受、拒绝或改写提议步骤携带的消息 |
| Waterfall | `request` | 改写模型调用配置，但不能改模型可见消息 |
| Waterfall | `request-error` | 决定是否认领失败并重试 |

Waterfall 中先登记的 Observer 位于外层，通过 `next` 决定是否继续调用内层。最内层的 `next` 返回运行时原本的决定。

除 `created`、`turn-stopping` 和 Waterfall 的显式错误路径外，纯通知 Observer 的 panic 会被隔离并记录，不会阻止后续 Observer，也不会逆转已经发生的状态。

## 作用域设计

Observer 通过 `scope.Scope` 登记并按 Agent 的载体作用域过滤：

- 全局作用域中的 Observer 能看到全部 Agent。
- 某个作用域中的 Observer 只能看到该作用域及其子作用域下的 Agent。
- 派发顺序为全局层、祖先层，最后是 Agent 自身层。
- Agent Dispose 时，其局部作用域资源随之释放。

这种设计用显式作用域替代隐式全局插件状态，使宿主级能力、父 Agent 能力和单 Agent 能力可以同时存在而不互相泄漏。

## 发起者上下文

模块使用 Go 的 `context.Context` 传递“这条调用链由哪个 Agent 发起”：

- `WithInitiator`：派生带 Agent 发起者的 Context。
- `WithoutInitiator`：明确切断继承来的发起者。
- `CurrentInitiator`：可选读取发起者。
- `RequireInitiator`：要求存在发起者，否则返回错误。

发起者只是一条因果归属线索，不证明 Agent 仍然存活，也不构成身份认证或权限授权。外部请求仍需先完成身份校验，再将已解析的活 Agent 放入 Context。

## 动态模型选择

`ModelSelectionRef` 保存当前选择和“本步骤装配时的快照”。`InstallModelSelection` 同时接入系统提示词装配与模型请求路由，保证一次与步骤并发发生的模型切换只会整体作用于当前步骤或下一步骤，不会出现提示词写的是模型 A、请求却发给模型 B 的撕裂状态。

选择内容包括 Provider、Model 和 Reasoning Effort。没有选择时保持 Agent 原始配置。

## 作用域与默认模型

`core/scope` 是 Agent 控制面的基础设施。每个作用域拥有不透明身份、可选父作用域和一组按后进先出顺序释放的资源。它同时解决两类问题：

- 工具、提示词、Skill 和 Observer 沿父链继承，离 Agent 更近的同名注册覆盖外层注册。
- Agent 事件只向自己的作用域和祖先作用域传播，不向子作用域或兄弟作用域传播。
- 父链绑定拒绝重复绑定和成环；改链只能使用首次绑定返回的句柄。
- `Scope.Dispose` 幂等执行全部清理，并汇总清理错误。

`core/agentdefaultmodel` 管理部署级默认模型。组合配置给出基础选择，动态设置可以覆盖 Provider、Model 和 Reasoning Effort；保存后，新选择立即被读取，不要求重建 Agent。它只提供默认值，不替代单个 Agent 的显式模型选择，也不负责模型路由和凭据管理。

## 并发设计

- `Registry` 可以被循环、协议桥和子 Agent 等多个 goroutine 并发使用，内部状态由互斥锁保护。
- Observer 始终在 Registry 锁外执行，允许 Observer 安全地回查同一 Registry，也避免把用户回调纳入临界区。
- `ModelSelectionRef` 自带互斥锁，保证模型选择与步骤装配之间没有数据竞争。
- `Inbox` 不加锁。它遵循单写者模型，只应由对应 Agent 的循环修改。
- 外部代码不应并发直接修改 `Agent.Inbox()` 返回的对象；发送、引导、注入和放回消息应通过 `Agent` 的方法完成。

## 能力边界

`core/agent` 已经负责：

- 活 Agent 公共接口与运行状态。
- 创建/恢复工厂契约。
- 活 Agent 注册表、运行期归属和生命周期公布。
- 耐久 Inbox 当前状态及未运行工作记账。
- 生命周期、步骤、模型请求和错误扩展点。
- 调用链发起者传递。
- 步骤一致的动态模型选择。

`core/agent` 明确不负责：

- ReAct 回合、步骤和工具循环：由 `core/agentloop` 负责。
- 模型调用和提供方协议：由 `llm` 及其适配器负责。
- 工具定义、校验和执行：由 `core/tools` 负责。
- 系统提示词组装：由 `core/systemprompt` 负责。
- 会话持久化后端：由 `session/persistence` 和 `storage` 负责。
- SDK、ACP、HTTP 或其他传输协议：由 `sdk/*`、`acp/acp` 和宿主负责。
- 业务工具、Skill、人格或行业逻辑：由宿主及对应扩展模块负责。
- 顶层“一行接入”Builder：当前仍需宿主显式装配各组件。

## 相关源码

| 文件 | 内容 |
|---|---|
| `core/agent/runtime.go` | `Agent`、状态、运行时选项和步骤/请求决策类型 |
| `core/agent/registry.go` | Factory、Registry、生命周期、查询和 Observer 派发 |
| `core/agent/inbox.go` | Inbox 当前状态、认领与变更 |
| `core/agent/types.go` | Inbox 事件类型和持久化负载 |
| `core/agent/observer.go` | 12 组 Observer 的签名和语义 |
| `core/agent/initiator.go` | 调用链发起者 Context |
| `core/agent/modelselection.go` | 动态模型选择及提示词/请求一致性接线 |
| `core/agent/consumedwork.go` | 从事件日志折叠已消费和被取消的工作 |
| `core/agent/doc.go` | 包级设计说明和移植裁决 |
| `core/scope/scope.go` | 作用域身份、父链、事件准入和资源释放 |
| `core/scope/layers.go` | 分层注册、继承与近层覆盖 |
| `core/agentdefaultmodel/config.go` | 部署默认模型与动态设置覆盖 |
