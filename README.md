# ds-harness-go

`ds-harness-go` 是一个面向服务端的 Go Agent 运行时，目标是作为组件嵌入现有 Go 服务。

宿主负责提供模型、工具、Skill、人格提示词和持久化后端；运行时负责 Agent 循环、上下文组装、工具调度、会话事件、恢复、多 Agent 协作及对外协议适配。业务能力留在宿主中，运行时本身不绑定具体行业。

项目参考 [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) 的能力和行为语义，但不逐行翻译 TypeScript。Go 已有成熟机制的部分直接采用 Go 的实现方式；每项移植或排除决定都记录在 `docs/portmap/`。

## 项目边界

- 面向长期运行、多用户的服务端进程，而不是桌面编程助手。
- 不执行任意 Shell 命令，不提供本机终端、子进程、代码沙箱或本地文件工具。
- 文件能力通过 `fs` 接口暴露，生产实现面向 S3、MinIO 等对象存储。
- 模型、工具、Skill、存储和协议适配器都由宿主显式装配。
- SDK 服务端不强制宿主采用指定的 HTTP 框架或进程模型。

## 已实现能力

| 能力 | 主要包 |
|---|---|
| Agent 生命周期与 ReAct 循环 | `core/agent`、`core/agentloop`、`core/session` |
| 系统提示词与工具运行时 | `core/systemprompt`、`core/tools` |
| 模型抽象、OpenAI 兼容协议、重试与计量 | `llm`、`llm/openaicompat`、`llm/llmretry`、`llm/tokenmeter` |
| 模型响应录制与回放 | `llm/replay`、`llm/mockserver` |
| 会话事件、持久化、投影与恢复 | `session`、`session/persistence`、`session/projection`、`session/projectioncache` |
| 检查点、统计、标题与遥测 | `session/checkpointpolicy`、`session/stats`、`session/sessiontitle`、`session/telemetry` |
| 上下文、压缩与大结果外置 | `context/*`、`compaction`、`spill` |
| Skill、计划、待办与人工介入 | `skill`、`plan/planmode`、`todo`、`interaction/*` |
| MCP 工具桥接 | `mcp` |
| 多 Agent、派生、续行与控制 | `subagent/*` |
| 后台任务、定时、目标与固定工作流 | `jobs/*`、`schedule/schedule`、`goal/*`、`workflow/toolralph` |
| SDK JSON-RPC 与 ACP 适配 | `sdk/sdkprotocol`、`sdk/sdkserver`、`acp/acp` |
| 可替换存储与对象存储 | `storage`、`storage/domain`、`storage/postgres`、`fs/objectstore` |
| 设置、凭据、附件与工作区 | `settings`、`credentials`、`attachment`、`workspace` |

## 运行结构

```text
宿主 Go 服务
  |
  | 模型、工具、Skill、人格、存储实现
  v
core/agentloop
  |
  +-- core/systemprompt <- context/* <- skill
  +-- session/projection <- session event log
  +-- compaction / spill
  +-- llm -> llm/llmretry -> llm/openaicompat
  +-- core/tools -> guard/* -> interaction/*
  |
  v
session/persistence -> storage/domain -> storage backend
```

一次回合从 Agent 收件箱认领消息开始。运行时组装系统提示词和历史投影，调用模型；如果模型请求工具，则完成校验、审批、执行和结果记录，再进入下一步模型调用。模型不再请求工具时，本回合结束。

会话事件日志是状态权威来源。Agent 进程内对象不承担长期状态；恢复时由持久化事件和投影重新构建会话及循环状态。

## 包结构

```text
ds-harness-go/
|-- core/
|   |-- agent/                 Agent 接口、注册表和生命周期事件
|   |-- agentloop/             回合、步骤和工具调用循环
|   |-- session/               运行中的会话对象
|   |-- systemprompt/          系统提示词组装
|   `-- tools/                 工具定义、校验和运行时
|-- llm/
|   |-- openaicompat/          OpenAI 兼容模型适配
|   |-- llmretry/              重试策略
|   |-- replay/                响应录制与回放
|   `-- tokenmeter/            Token 统计
|-- session/
|   |-- persistence/           会话持久化协调
|   |-- projection/            事件投影
|   |-- projectioncache/       投影缓存
|   |-- checkpointpolicy/      检查点策略
|   `-- stats/                 会话统计
|-- storage/
|   |-- domain/                串行写入和领域存储
|   |-- postgres/              PostgreSQL 后端
|   `-- storagetest/           后端一致性测试套件
|-- context/                   指令、会话引用和时间上下文
|-- compaction/                上下文压缩
|-- skill/                     Skill 注册表和加载工具
|-- subagent/                  多 Agent 运行时及工具
|-- interaction/               审批、提问和命令
|-- jobs/                      后台任务
|-- goal/                      长期目标和自动续行
|-- schedule/                  定时任务
|-- workflow/                  固定工作流
|-- mcp/                       MCP 客户端桥接
|-- sdk/                       SDK 协议与服务端
|-- acp/                       ACP 适配
|-- fs/                        文件接口与对象存储后端
|-- preset/                    Agent 预设和人格
|-- settings/                  动态设置
|-- workspace/                 工作区注册表
|-- tools/                     移植裁决与门禁工具
`-- docs/                      设计和能力映射文档
```

完整包列表以 `go list ./...` 的输出为准，README 不再维护一份容易过期的逐包完成状态树。

## 当前状态

项目仍处于开发阶段：核心运行时和主要扩展包已经落地，但尚未发布稳定版本，也尚未提供一行代码完成全部装配的顶层 Builder。宿主目前需要按自身需求显式创建并连接各组件。

以下检查当前通过：

```powershell
go build ./...
go vet ./...
go test ./...
go test -race ./...
```

移植完整性门禁以此命令为准：

```powershell
go run ./tools/portcheck
```

`PENDING` 表示对应 DSH 符号仍未完成最终裁决。只要存在 `PENDING`，移植完整性门禁就不会通过；不能把单元测试通过等同于项目已经完成。

PostgreSQL 后端测试需要设置真实数据库连接环境变量；未提供连接时，对应集成测试会跳过。

## 开发与核验

```powershell
gofmt -l .
go build ./...
go vet ./...
go test ./...
go test -race ./...
$env:GOOS = "linux"
go build ./...
$env:GOOS = "darwin"
go build ./...
Remove-Item Env:GOOS
go run ./tools/portcheck
```

关键文档：

- `docs/DESIGN.md`：运行时边界、持久化和装配设计。
- `docs/portmap/rulings.md`：DSH 包级裁决。
- `docs/portmap/decisions.md`：符号级裁决依据。
- `docs/portmap/portmap.tsv`：机器读取的逐符号状态表。

## 移植规则

- 按通用服务端 Agent 运行时是否需要该能力决定范围，不按某个业务项目裁剪。
- Go 有原生等价机制时使用 Go 机制，不复制 TypeScript 基础设施。
- 运行时接口与具体后端分离，宿主决定模型、存储、对象存储和传输实现。
- 源码使用 `// 源: packages/...:行号` 或 `// 新增: 理由` 记录实现依据，并由 `tools/portcheck` 校验。
