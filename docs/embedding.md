# 嵌入现有 Go 服务

## 接入目标

宿主把 `ds-harness-go` 当作进程内组件使用，而不是启动一个固定形态的独立应用。宿主继续拥有：

- 服务启动和关闭。
- HTTP、WebSocket、RPC 或消息队列。
- 用户身份、租户和权限。
- 模型账号与路由配置。
- 业务工具、Skill 和人格。
- 数据库、对象存储和密钥服务。

运行时拥有 Agent 执行和会话语义，但不接管以上职责。

## 最小能力闭环

一个能完成普通对话和工具调用的部署至少需要：

| 能力 | 主要组件 |
|---|---|
| 作用域和生命周期 | `scope` |
| 活 Agent 管理 | `harness/agent.Registry` |
| 活会话管理 | `harness/session.Store` |
| Agent 循环 | `harness/agentloop.AgentLoop` |
| 模型路由 | `llm.Runtime` 与至少一个 `llm.Adapter` |
| 提示词组装 | `harness/systemprompt.Registry` |
| 工具运行时 | `tools.Runtime` |
| 会话事件词汇 | `sessionlog.Vocabulary` |

生产部署通常还会接入 `feature/persistence`、`storage/domain`、状态缓存、附件、凭据、遥测和协议适配。会话日志 Store 与通用 KV Storage 是不同接口。

## 推荐装配顺序

```text
1. 创建宿主根作用域
2. 创建不变量、设置、存储和日志设施
3. 注册存储后端并打开 storage/domain
4. 提供会话 Store，并接好活会话创建、事件、Flush 和释放边界
5. 创建状态计算注册表，并在已打开的 storage/domain 上创建状态缓存
6. 创建 LLM Runtime 并注册模型适配器
7. 创建 System Prompt、Tools、Session、Agent Registry
8. 注册事件词汇、状态计算单元和运行时扩展
9. 装配 Skill、Persona、业务工具、Guard 和人工介入
10. 创建 Agent Loop，Factory 随之注册进 Agent Registry
11. 挂载 SDK、ACP 或宿主自己的入口
12. 开始接收请求
```

第 10 步的注册由 `agentloop.New` 自己完成，撤销也折在它返回的拆除函数里；宿主不要再调一次 `agent.Registry.SetFactory`，那会被「已经登记过一个 agent 造法」拒掉。

依赖应显式传入，不要通过全局变量隐藏。构造函数返回的注销、Dispose 或 Close 句柄要由创建方保存。

前 10 步中不需要外部介质的那部分，`harness.New` 就是它的可编译、可运行版本。它不是生产模板（没有存储后端、持久化和协议入口），但它保证这份装配顺序不会和代码各走各的。「外部宿主真的调得动它」这件事由 `internal/devtools/consumercheck` 守着：那道门禁在仓库**外面**建一个模块，只用导出的构造函数、导出的选项字段和一个外部自己实现的模型适配器把同一份闭环拼出来，任何一次收窄导出面的改动都会在那里编译不过。

## 事件词汇

核心 `session` 只认识核心事件。启用扩展模块时，要把对应事件类型加入会话词汇，例如 Agent Inbox、LLM 重试、压缩、Goal、Schedule 和 Plan Mode。

原则是：写入日志的模块必须同时登记自己的事件类型；否则恢复时会把这些事件判定为未知事件。

## 模型接入

1. 创建 `llm.Runtime`。
2. 实现并注册 `llm.Adapter`，或使用 `adapter/openaicompat`。
3. 为每个路由声明 Provider、Model、上下文窗口、最大输出和重试策略。
4. 将 API Key 交给凭据服务或宿主配置，不要写进会话事件。
5. 设置 Agent 默认模型，必要时安装动态模型选择。

`adapter/openaicompat` 面向 OpenAI Chat Completions 兼容服务，不等于支持所有 OpenAI、Anthropic 或 Responses API 特性。

## 工具接入

每个工具需要提供名称、描述、输入 JSON Schema、调用种类和执行函数。运行时负责：

- 按作用域解析可见工具。
- 校验模型参数。
- 执行前策略、审批和 Guard。
- 调度执行。
- 规范化错误和结果。
- 执行后策略、外置和附加上下文。

工具实现必须接受并传递 `context.Context`。超时策略只能发出取消信号，不能强行停止忽略 Context 的代码。

## Skill 与人格

- Persona 是系统提示词中的部署方身份和行为约束。
- Skill Registry 保存可发现、可读取的 Skill。
- Skill Tool 让模型按需加载正文，而不是把全部 Skill 内容塞进每次请求。
- Agent Preset 把 Persona、Skill、工具和其他贡献组合成可选预设。
- 所有内容都可以按作用域安装，避免一个租户或 Agent 的配置泄漏到另一个。

## 会话与恢复

创建新 Agent 时，Agent ID 与 Session ID 必须一致。恢复流程应先读取并校验持久化事件，再创建活 Session、Inbox 和 Agent Loop。

事件日志是权威事实。状态缓存只是加速恢复：缓存不可用时必须能够回退到事件重放。

但重放的对象是**现存的那一段日志**，不是全部历史——一个会话的事件超过上限时，最老的一段会被删掉（见 `docs/session-log-limit.md`）。所以宿主要接受两件事：一是恢复出来的状态可能残缺（最后一次更新落在被删区间里的那些），二是**残缺不阻断读**，不要把它当成一次失败去重试或者拒绝这个会话。日志里事件的序号也因此不再从 0 起，宿主拿序号去定位时要用仓库提供的换算，不要当成数组下标。

当前仓库提供 `feature/persistence.Store` / `Backend`、`WriteBehind`、恢复原语和 `Coordinator`。宿主为 `Coordinator` 注入具体 `Backend` 与 `harness/session.Store`，再调用 `Install` 接上创建、事件、Flush 和释放观察者。`Coordinator.Prepare` 返回带释放语义的 `harness/session.Preparation`；接入 Agent Loop 时仍需由装配层把 Preparation 的发布/释放和后端 `List` 适配到消费方接口。

宿主不要直接修改会话事件切片、Inbox 内部切片或状态缓存记录。

## 多租户

运行时不会替宿主完成租户鉴权。宿主必须：

- 在外部 ID 转换为 Session ID 前校验访问权。
- 为租户选择正确的模型、凭据、工具、Skill、存储和作用域。
- 限制会话查询、子 Agent、后台任务和附件访问范围。
- 不把 `agent.WithInitiator` 当成授权；它只记录调用链来源。
- 不从模型提供的字符串直接解析内部资源句柄。

## 请求入口

宿主入口通常执行：

```text
认证用户
  -> 解析租户和会话
  -> Registry.Get 或 Registry.Create/Resume
  -> 构造带超时和取消原因的 Context
  -> Agent.Followup / Steer / Inject
  -> 等待状态、事件或最终响应
  -> 转换为宿主协议
```

`protocol/sdk/sdkserver` 和 `protocol/acp` 可以提供现成协议语义，但宿主仍决定网络监听、连接管理和认证方式。

## 关闭顺序

关闭应停止新请求，再从外向内释放：

```text
协议入口
  -> 后台任务、定时器和子 Agent
  -> Agent Handle
  -> Agent Loop
  -> 状态缓存与会话持久化
  -> Storage Domain
  -> Storage Backend
  -> 根作用域
```

关闭过程中要等待正在运行的回合、写后队列和持久化屏障。注销注册表项不等于关闭其背后的资源。

## 失败处理

- 模型失败通过结构化 `llm.Failure` 和重试策略处理。
- 工具失败转换成模型可见的工具结果，不让第三方工具 panic 终止整个服务。
- Observer 的失败语义不同：有的仅记录，有的可以否决，有的会中止当前边界。
- 状态缓存写失败只影响性能，不应破坏已提交事件。
- 持久化事件失败不能报告为成功。
- 外置失败默认保留原结果，不能把成功工具调用改成失败。

## 当前限制

- 模块路径是 `ds-harness-go`，不是一个可被 `go get` 解析的地址。外部宿主接入时要在自己的 `go.mod` 里写一条 `replace ds-harness-go => <本仓库路径>`（或者把本仓库作为 workspace 成员）。这是有意的：模块名不跟着某个托管地址走。
- `harness.New` 只装得出上面第 10 步为止那段不碰外部介质的闭环；存储后端、持久化和协议入口仍要宿主显式连接，没有一个把这些也一并包办的顶层 Builder。
- 没有内置生产会话持久化 Backend；`Coordinator` 只负责编排，不决定介质。
- 没有任意代码执行、Shell、本地终端或本地文件工具。
- PostgreSQL 集成测试需要真实数据库连接。
- `internal/devtools/portcheck` 仍可能报告尚未完成最终裁决的 DSH 能力。
