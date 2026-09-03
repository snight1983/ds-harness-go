# ds-harness-go 文档

这里记录运行时的架构、接入方式、模块职责、设计约束和能力边界。文档以源码当前行为为准，不把规划中的能力写成已经实现。

## 开始阅读

- [总体架构](architecture.md)：分层、请求流程、状态管理、并发和持久化边界。
- [嵌入 Go 服务](embedding.md)：宿主责任、装配顺序、多租户、关闭和失败处理。
- [Go 包文档映射](packages.md)：每个可发布 Go 包对应的主文档，由机器门禁校验。

## 模块文档

| 模块 | 内容 |
|---|---|
| [作用域](modules/core-scope.md) | 身份、父子链、事件准入方向、所有权边界和分层注册表骨架 |
| [Agent 控制面](modules/agent.md) | `core/agent`：活 agent 契约、名册、三步生命周期、收件箱和 12 个挂钩点 |
| [Agent Loop](modules/agentloop.md) | `core/agentloop`：回合与步骤两层循环、请求从日志推导、工具调度、取消与失败兜底 |
| [Session](modules/session.md) | 事件日志、根据事件整理的当前状态、缓存、恢复和分叉 |
| [会话日志与派生状态](modules/session-tree.md) | `session/` 一棵树：事件词汇、持久化、当前状态、缓存、统计和遥测 |
| [活会话](modules/core-session.md) | 活 `Session` 与 `Store`：追加、四组观察者、三步生命周期和分叉 |
| [LLM](modules/llm.md) | 模型值类型、适配器、流式响应、重试、计量和回放 |
| [Tools](modules/tools.md) | `core/tools`：工具可见性、四段派发管线、受限 Schema 子集和呈现 |
| [系统提示词装配](modules/systemprompt.md) | `core/systemprompt`：四类提示词登记、装配瀑布、变量插值和工具排序 |
| [Skill、提示词与预设](modules/skill.md) | Skill 发现、预设、Persona 和运行时上下文 |
| [多 Agent](modules/subagent.md) | Provider、进程内派生、续行、控制、结算和子树查询 |
| [存储、文件与附件](modules/storage.md) | Backend、会话持久化、对象存储、附件和凭据 |
| [持久化抽象层](modules/datastore.md) | `datastore/`：唯一挂驱动、唯一写 SQL 的地方，及两种方言的一致性 |
| [后台任务、目标与工作流](modules/workflow.md) | jobs、goal、schedule 和 Ralph 固定工作流 |
| [协议适配](modules/protocol.md) | SDK JSON-RPC、ACP 和 MCP 的接口与安全边界 |

## 详细模块

| 领域 | 文档 |
|---|---|
| 上下文与压缩 | [运行时上下文](modules/context.md)、[上下文压缩](modules/compaction.md) |
| 设置与组合 | [运行时设置](modules/settings.md)、[部署级默认模型](modules/core-agentdefaultmodel.md)、[Agent 预设与 Persona](modules/presets.md) |
| 计划与人工介入 | [计划与待办](modules/planning.md)、[用户交互](modules/interaction.md)、[运行时 Guard](modules/guards.md) |
| 后台执行 | [后台作业](modules/jobs.md)、[长期目标](modules/goal.md)、[耐久提醒](modules/schedule.md)、[Ralph 工作流](modules/ralph.md) |
| 会话读侧 | [Session 查询](modules/sessionquery.md)、[Workspace](modules/workspace.md) |
| 数据接缝 | [文件系统](modules/filesystem.md)、[附件与图片](modules/attachment.md)、[凭据](modules/credentials.md)、[大结果外置](modules/spill.md) |
| 外部协议 | [SDK 协议与服务端](modules/sdk.md)、[ACP 接入](modules/acp.md)、[MCP 客户端](modules/mcp.md) |
| 维护与测试 | [不变量诊断](modules/invariants.md)、[通用运行时工具](modules/utilities.md)、[LLM 测试与回放](modules/llm-testing.md)、[移植与文档门禁工具](modules/migration-tools.md) |

## 设计与移植

- [详细设计](DESIGN.md)：服务端边界、移植原则和各能力块的底层设计。
- [会话日志上限](session-log-limit.md)：存档按条数封顶、从最老的一头丢，以及由此带来的 `Seq` 起点约定。
- [性能与压力基线](performance-baseline.md)：长会话、并发 Agent、持久化、SDK 洪泛和 shutdown 的实测数字与判读方法。
- [包级移植裁决](portmap/rulings.md)：上游能力的移植或排除决定。
- [符号级裁决](portmap/decisions.md)：逐符号实现依据。
- [能力覆盖表](portmap/capabilities.md)：能力与当前实现的对应关系。
- [Go 包文档映射](packages.md)：逐包文档覆盖与自动校验入口。

## 文档站

`docs/` 可直接作为 GitHub Pages 发布目录。Docsify 使用 `_sidebar.md` 生成左侧目录，点击后在右侧显示对应文档；Markdown 文件在仓库中也可以直接阅读。
