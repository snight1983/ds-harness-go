# ds-harness-go 文档

这里记录运行时的架构、接入方式、模块职责、设计约束和能力边界。文档以源码当前行为为准，不把规划中的能力写成已经实现。

## 开始阅读

- [总体架构](architecture.md)：分层、请求流程、状态管理、并发和持久化边界。
- [嵌入 Go 服务](embedding.md)：宿主责任、装配顺序、多租户、关闭和失败处理。

## 模块文档

| 模块 | 内容 |
|---|---|
| [Agent](modules/agent.md) | 活 Agent 契约、Registry、Inbox、作用域、生命周期和默认模型 |
| [Agent Loop](modules/agentloop.md) | 回合、步骤、模型请求、工具循环、取消和维护任务 |
| [Session](modules/session.md) | 事件日志、根据事件整理的当前状态、缓存、恢复和分叉 |
| [LLM](modules/llm.md) | 模型值类型、适配器、流式响应、重试、计量和回放 |
| [Tools](modules/tools.md) | 工具定义、Schema、执行管线、审批、Guard 和 MCP 工具 |
| [Skill、提示词与预设](modules/skill.md) | Skill 发现、提示词装配、预设、Persona 和运行时上下文 |
| [多 Agent](modules/subagent.md) | Provider、进程内派生、续行、控制、结算和子树查询 |
| [存储、文件与附件](modules/storage.md) | Backend、会话持久化、对象存储、附件和凭据 |
| [后台任务、目标与工作流](modules/workflow.md) | jobs、goal、schedule 和 Ralph 固定工作流 |
| [协议适配](modules/protocol.md) | SDK JSON-RPC、ACP 和 MCP 的接口与安全边界 |

## 设计与移植

- [详细设计](DESIGN.md)：服务端边界、移植原则和各能力块的底层设计。
- [包级移植裁决](portmap/rulings.md)：上游能力的移植或排除决定。
- [符号级裁决](portmap/decisions.md)：逐符号实现依据。
- [能力覆盖表](portmap/capabilities.md)：能力与当前实现的对应关系。

## 文档站

`docs/` 可直接作为 GitHub Pages 发布目录。Docsify 使用 `_sidebar.md` 生成左侧目录，点击后在右侧显示对应文档；Markdown 文件在仓库中也可以直接阅读。
