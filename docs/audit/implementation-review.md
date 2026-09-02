# ds-harness-go 移植完成度审计

审计日期：2026-09-01  
Go 仓库：`C:\code\ds-harness-go` 当前工作树  
Go 基准提交：`74a92b57c83017331ef2aef508a74d6224770a62`  
唯一上游：`C:\codestudy\deepseek-harness-dsh-v0.1.2-alpha.3`  
上游版本：`@deepseek-ai/dsh-root 0.1.2-alpha.3`

> 本报告审查的是“对 DeepSeek Harness 当前版本的 Go 移植”，不是普通 Go 项目代码体检。逐项证据见 [ported-api-review.tsv](ported-api-review.tsv)，关键能力语义对照见 [capability-review.md](capability-review.md)。

## 1. 结论

**当前不是完成移植，也不能标记为 production-ready。**

它也不是空壳或批量假实现。已经落地的 Agent、会话、工具、子 Agent、压缩等模块，代码和测试质量整体较高；当前工作树可以从包外装配一个内存态最小宿主并真实跑完一轮。但“旧版已实现的主体代码质量较高”和“已经完成对 `0.1.2-alpha.3` 的移植”是两件不同的事。

客观状态分三层：

| 判断口径 | 当前结论 |
|---|---|
| 已实现模块的内部质量 | 较高：构建、Vet、单测、竞态测试通过，总语句覆盖率 90.9% |
| 对最新版上游的裁决完整度 | 未完成：9771 个顶层导出中 3286 个（33.6%）没有可继承的裁决 |
| 对最新版上游的方法级证明 | 未完成：已判 `PORTED` 的类/接口仍有 579 个成员只有范围溯源，210 个成员没有直接对应证据 |
| 内存态最小嵌入 | 已实现：`example/minimalhost` 可从包外装配并跑一轮 |
| 可恢复的生产嵌入 | 未完成：Agent Loop 与 Coordinator 接口不兼容，且没有生产会话 Backend |
| 公开 Go module 消费 | 未完成：GitHub import path 编译失败 |
| 性能结论 | 无法给出：0 个 benchmark，没有容量或压力基线 |

若必须给出近似值：

- “已写部分的工程质量”约 **8/10**；
- “第三方 Go 服务可直接导入、装配、持久化恢复、部署”约 **6/10**；
- “对 `0.1.2-alpha.3` 的完整移植比例”**目前不能诚实给出**。33.6% 的顶层导出尚未裁决，剩余 66.4% 只是“继承了旧裁决”，不是 66.4% 已实现。

## 2. 审计基线

### 2.1 上游快照

| 项目 | 值 |
|---|---:|
| `package.json` 版本 | `0.1.2-alpha.3` |
| 包清单 | 257 个 `package.json`；250 个包存在导出符号 |
| 顶层导出符号 | 9771 |
| 扫描源码行 | 299367 |
| `package.json` SHA-256 | `B31CEE4E9AEF1A0CDC4CD5C0DC25D5A35470AA2E8CE7EE4DE1D2287010BCF88A` |
| `README.md` SHA-256 | `A51CA4BFF273518C3F292B2E6800F5F2DE76170AE6F5919FC48E4A0D82B0DA1E` |
| `pnpm-lock.yaml` SHA-256 | `17BBD38216E31A8B821957F77D2E3F57B859046CC3A18076AD16E94CA952A8DA` |

### 2.2 Go 快照

| 项目 | 值 |
|---|---:|
| 可发布 Go 包 | 86 |
| 非测试 Go 文件 | 448 |
| 测试文件 | 286 |
| 测试函数 | 4322 |
| Benchmark | 0 |
| Example 测试 | 0 |
| 当前状态 | `master` 比 `origin/master` 领先 1 个提交，工作树非干净 |

当前工作树包含其他开发中的修改。本报告没有修改业务代码；所有结论以审计时读到的工作树快照为准。

## 3. 审计方法

本次不是按文件名或测试覆盖率猜完成度，而是做三层核对：

1. **顶层符号层**：对最新版 9771 个导出逐项匹配旧裁决，忽略行号变化；先按“包、文件、kind、名称、来源”匹配，再对唯一项按“包、文件、kind、名称”回退匹配。
2. **公开成员层**：对导出的 class/interface 扫描公开方法和字段，并与 Go AST 中的函数、方法、接口方法和结构体字段并排输出签名。
3. **语义层**：对核心路径人工比较输入、输出、错误、状态变化、副作用、取消、并发和生命周期。

成员扫描是词法扫描，不是 TypeScript 编译器。它会漏掉少量复杂计算属性，也不能仅凭名称证明行为等价。因此：

- `PORTED_MEMBER_NAME_MAPPED` 只表示同容器同名成员存在；
- `PORTED_MEMBER_SOURCE_MAPPED` 只表示某个 Go 声明明确引用该成员的上游行；
- `PORTED_MEMBER_UNMAPPED` 表示没有直接证据，不自动等于缺失；
- 最终语义结论以人工对照为准。

## 4. 最新版逐项统计

### 4.1 顶层导出

| 状态 | 数量 | 占最新版导出 |
|---|---:|---:|
| `PORTED_TOP_MAPPED` | 1397 | 14.3% |
| `GO_NATIVE` | 786 | 8.0% |
| `SKIP` | 401 | 4.1% |
| `OUT_OF_SCOPE` | 3901 | 39.9% |
| `PENDING` | 3286 | 33.6% |
| 合计 | 9771 | 100% |

旧裁决表有 7909 行；按稳定键迁移到最新版后，6485 行可继承，1424 行在最新版中找不到稳定对应。旧表写的是 227 个包，当前上游已增长到 257 个包清单。

`PENDING` 不全是必须移植的功能：新版增加了大量 UI、浏览器、Web Worker、Windows 子进程和实验包，部分会继续合理排除。但在逐项重新裁决之前，不能把它们默认为已完成或已排除。

### 4.2 class/interface 公开成员

| 状态 | 数量 |
|---|---:|
| `PENDING_MEMBER` | 3414 |
| `OUT_OF_SCOPE_MEMBER` | 3563 |
| `SKIP_MEMBER` | 299 |
| `GO_NATIVE_MEMBER` | 364 |
| `PORTED_MEMBER_NAME_MAPPED` | 1069 |
| `PORTED_MEMBER_SOURCE_MAPPED` | 579 |
| `PORTED_MEMBER_UNMAPPED` | 210 |
| 合计 | 9498 |

旧 `portmap.tsv` 只登记顶层导出，不登记 class/interface 内的方法。原 `portcheck` 即使通过，也不能证明类内每个方法、字段、构造器和副作用都已移植。

## 5. 阻断问题

### B-01 当前移植门禁没有对准唯一上游

严重度：阻断

- `tools/portmap/main.go:64`、`tools/capmap/main.go:64`、`tools/portcheck/main.go:51` 仍默认指向已经不存在的 `C:\codestudy\deepseek-harness-master`。
- README 声称 `go run ./tools/portcheck` 通过；实际命令退出码为 1，并报告 4617 条“出处不存在”。
- 显式改用 `0.1.2-alpha.3` 后仍退出 1，报告 55 条溯源范围越界。
- 旧裁决表只有 7909 条，最新版有 9771 条；门禁没有扫描或裁决新增导出。

因此，现有门禁通过与否都不能证明 `0.1.2-alpha.3` 的移植完整性。必须先把上游版本、导出清单、裁决表和溯源行号绑定到同一个快照。

### B-02 Agent Loop 与持久化 Coordinator 不能连接

严重度：阻断

最新版上游 `AgentLoop.resumeWith` 获取 `SessionPreparation`，在发布成功或失败后释放准备件；被取消后到达的准备件也必须执行 disposer。这是准备池引用、发布和回收语义的一部分。

Go 端：

- `core/agentloop/loop.go:178` 的 `SessionPersistence` 要求 `Prepare(...) (*session.Session, error)` 和 `List(...)`；
- `session/persistence/coordinator_prepare.go:24` 实际返回 `*session.Preparation`；
- `Coordinator` 没有 `List`，只有它持有的 `Backend` 在 `session/persistence/backend.go:93` 定义 `List`。

独立编译期断言同时确认：缺少 `List`，且 `Prepare` 返回类型错误。这不是文档问题，而是两个已实现模块无法装配。若宿主自行把 `Preparation` 粗暴拆成 `Session`，还可能丢掉发布/释放语义。

### B-03 GitHub import path 无法消费

严重度：阻断

`go.mod:1` 是 `module ds-harness-go`，内部 import 也使用 `ds-harness-go/...`。仓库地址是 `github.com/snight1983/ds-harness-go`。

仓库外测试模块通过 GitHub 路径 import 并 `replace` 到本地仓库时编译失败：

```text
package ds-harness-go/core/scope is not in std
```

项目名和 Go module path 不是同一个概念。项目仍可叫 `ds-harness-go`，但若目标是让任意 Go 服务通过 GitHub 版本依赖加载，必须确定可发布的 module path，或明确采用私有 replace/workspace 分发方式。

### B-04 没有生产会话 Backend

严重度：产品阻断

`session/persistence.Backend` 需要 `LoadStored`、`ReadStoredRevision`、`AppendBatch`、`CommitRepair` 和会话 `List`。生产代码中没有实现该接口的具体介质。

`storage/postgres` 实现的是通用 KV `storage.Backend`，不是会话事件 Backend。`example/minimalhost` 也明确不接持久化、恢复、`storage/domain` 和协议入口。

接口留给宿主实现可以是合理边界；但在官方适配器和端到端恢复测试存在之前，不能宣称“直接嵌入即可恢复长期会话”。

## 6. 高严重度语义差异

### H-01 SDK 初始化状态机与最新版上游不等价

位置：`sdk/sdkserver/server.go:131`、`sdk/sdkserver/server.go:149`、`sdk/sdkserver/server.go:196`

最新版上游：

- 先验证 `reasoningEffort`、`maxTokens`；
- 挂载或确认 Adapter；
- 解析一次真实调用配置；
- 最后才发布 `initialized = true`；
- `prompt` 明确拒绝未初始化状态。

Go 端在 Adapter 挂载前就设置 `initialized=true`，`Prompt` 不检查 initialized。结果是：

1. `Install` 后、`Initialize` 前可以创建空路由 Agent；
2. `Initialize` 与 `Prompt` 并发时，Prompt 可越过尚未完成的挂载；
3. `MountAdapter` 失败后仍保持 initialized，不能重试。

### H-02 SDK 最新输入输出未对齐

- 上游 `InitializeParams.reasoningEffort` 会进入 Agent 路由；Go 的 `agent.Options` 和 SDK 参数均没有该字段。
- 上游 `SdkPromptContentBlock` 接受内联 base64 图片，并在 Followup 前异步准入附件；Go 的 `SessionPromptParams.ContentBlocks` 只能接收已经耐久化的 `llm.Content`。
- 上游附件准入后会再次确认 Agent 仍存活；Go Prompt 没有对应的异步准入边界。

Go 已有 `attachment.AdmitEncodedImages`，因此不是底层能力完全没有，而是 SDK 协议没有接上。

### H-03 ACP 是旧能力子集，不是最新版等价实现

Go ACP 当前明确：

- `mcpServers` 非空即拒绝，见 `acp/acp/bridge.go:697`；
- `session/resume` 返回 method not found，见 `acp/acp/bridge.go:889`；
- `session/set_config_option` 返回 method not found，见 `acp/acp/bridge.go:894`。

最新版上游已经拆出 `AcpSession`、`AcpModelControl` 和 `mountAcpMcpServers`，支持会话恢复、模型选项快照/修改、按会话挂载 MCP。Go 的 NewSession、Prompt、Cancel 和事件转发主体仍有价值，但只是旧版子集。

### H-04 最新核心能力存在未迁移或未裁决项

已经确认的服务端相关差异：

| 上游新增/变化 | Go 当前状态 | 结论 |
|---|---|---|
| `TurnBoundaryProjection` 与 `turnBoundaryProjectionDefinition` | 没有共享投影；个别模块自行扫描事件 | 缺失公共能力 |
| `AgentOptions.reasoningEffort` | `agent.Options` 只有 provider/model/maxTokens | 缺失 |
| `SessionPersistence.borrowSession`、`PreparationLease` | Coordinator 没有 BorrowSession | 缺失最新版观察/租约接口 |
| `ensureMaterialized` | 没有公开等价方法 | 缺失公开能力 |
| `encodeSeqRanges` / `decodeSeqRanges`、chunk row helpers | 没有对应导出 | 未移植 |
| 图片只读执行路径和按图片恢复提示 | Go 仍使用旧的统一占位文字 | 部分实现、语义落后 |
| 图片请求定价 `priceImages` | 无对应接口 | 缺失 |
| Subagent 新 control DTO/校验层 | 底层 List/Followup/Interrupt 已有 | 形状未裁决，不能算底层缺失 |

### H-05 SDK 行协议没有资源上限

`sdk/sdkprotocol/transport.go:213` 用 `ReadBytes('\n')`。对端一直不发换行时，单帧可持续增长。`jsonrpc2.AsyncHandler` 也没有仓库级每连接并发上限。

若只连接受信任同机进程，风险较低；若被宿主暴露给不可信客户端，存在内存和 goroutine 放大风险。

## 7. 有意裁剪，不应误报为偷懒

### 7.1 PTC / `run_code`

最新版上游 `core/tools` 增加 PTC：模型只看到 `run_code`，程序在 TypeScript/Python Code Runtime 中调用实际工具，内部还维护子调用并发、顺序提交、日志和取消。

Go 仓库明确不执行任意代码、不提供沙箱或本机子进程，因此没有实现 PTC。这与 README 边界一致，应判为**有意裁剪**，不是实现 bug；但新版 `ptc.ts` 的符号还没有正式写入裁决表，旧 `code-mode.ts` 的 `SKIP` 不能自动代替新版逐项裁决。

### 7.2 Go 化等价

以下常见差异不应直接算缺失：

- `AbortSignal` 变成方法第一个参数 `context.Context`；
- TS 判别联合变成 Go 具名类型、常量或带 Kind 的结构体；
- `constructor` 变成 `NewXxx` 或构造函数；
- Cordis `inject`、运行时 schema 和默认导出不移植；
- 嵌入结构体字段、`ID/URL` 首字母缩写会使机械名称匹配失败；
- 呈现类型的 `kind/shape/type` 可能由方法和 `MarshalJSON` 固定，而不是可写字段。

这也是 210 个 `PORTED_MEMBER_UNMAPPED` 不能全部直接判缺失的原因。

## 8. 代码质量与“偷懒实现”

### 8.1 正面证据

- `go build ./...`、`go vet ./...`、`go test ./...`、`go test -race ./...` 当前通过；
- 总语句覆盖率 90.9%；`core/agent` 99.5%、`core/session` 99.2%、`core/tools` 99.2%、`session/persistence` 98.0%；
- `example/minimalhost` 从包外装配内存态闭环，验证真实回合、Persona、事件词汇和幂等拆除；
- 工具调用有 panic 隔离、取消、并行上限和按提交顺序落地；
- 会话、投影、准备池和写后队列有大量不变量与竞态测试。

没有发现大面积空函数、统一返回零值、`panic("TODO")` 或只为编译通过而批量生成的假实现。

### 8.2 确认存在的收尾简化

| 类型 | 证据 | 判断 |
|---|---|---|
| 顶层裁决代替方法验收 | 旧表没有成员行 | 完整性门禁不足 |
| 模块各自实现但不接线 | Agent Loop 与 Coordinator 接口冲突 | 实质阻断 |
| 把生产介质留给宿主 | 无会话 Backend | 合理边界，但产品闭环未完成 |
| 用 panic 表达可选能力缺失 | `fs/objectstore.ProcessPath`、`FileURL` 必定 panic | 接口能力表达不安全 |
| 只提供内存态示例 | minimalhost 不含恢复和协议 | 示例有价值，但不足以证明生产嵌入 |
| 不做性能验证 | 0 个 benchmark | 无法声明性能成熟 |

所以准确评价是：**主体模块认真，最新版跟进、跨模块接线、生产介质和性能证据没有收尾。**

## 9. 性能审查

### 9.1 正面设计

- 工具并行有上限，独占调用形成屏障，结果按调用顺序提交；
- 第三方工具 panic 被转换为单次工具失败；
- 会话持久化按 Session ID 串行化写入；
- 竞态测试通过。

### 9.2 风险

| 位置 | 行为 | 风险 |
|---|---|---|
| `session/surface.go:233`、`:238` | 每次替换两次线性查找 | 高频长表面替换为 O(n) |
| `core/session/session.go:265`、`:394` | 追加使快照失效，下一次 `Events()` 复制全部事件 | “追加一次、读一次”累计可接近 O(n²) |
| `session/persistence/backend.go:93` | `List` 一次返回全部会话头 | 大租户内存尖峰，无分页 |
| `session/persistence/coordinator.go:356` | shutdown 为每个活会话起一个 goroutine | 活会话多时 goroutine 无上限 |
| `session/persistence/coordinator.go:255` | 后台耐久写不跟随请求取消 | 后端永久阻塞可拖住 drain/shutdown |
| `sdk/sdkprotocol/transport.go:213` | 单帧无上限 | 不可信对端可持续占用内存 |

这些是复杂度和容量风险，不是已证明的性能故障。仓库没有 benchmark、负载测试或硬件基线，因此不能客观宣称高性能或生产容量。

## 10. 验证结果

| 验证 | 结果 |
|---|---|
| `go build ./...` | 通过 |
| `go vet ./...` | 通过 |
| `go test ./...` | 通过 |
| `go test -race ./...` | 通过 |
| 覆盖率 | 90.9% |
| `gofmt -l .` | 未通过：2 个 Agent Loop 测试文件未格式化 |
| `go run ./tools/doccheck` | 通过：86 包、33 篇主文档、42 个 Markdown |
| `go run ./tools/portcheck` | 失败：默认上游目录不存在，4617 条出处不存在 |
| `portcheck -dsh-root <0.1.2-alpha.3>` | 失败：55 条溯源范围越界；仍只检查旧 7909 行清单 |
| 最新版逐项表 | 9771 个顶层导出、9498 个公开成员，共 19269 行证据 |
| 外部 GitHub import | 失败：内部 `ds-harness-go/...` 无法解析 |
| Coordinator 接口断言 | 失败：缺 `List`，`Prepare` 返回类型不匹配 |
| PostgreSQL 真实集成 | 未验证；无 DSN 时跳过，覆盖率 5.2% |
| Benchmark / 压力测试 | 不存在 |

## 11. 修复顺序

1. 固定唯一上游版本，重新生成 9771 行顶层清单并逐项裁决 3286 个 `PENDING`；同时迁移全部溯源行号。
2. 让 `AgentLoop` 正确消费 `SessionPreparation`，保留发布、放弃、释放和取消后迟到值的语义；补编译期接口断言和恢复集成测试。
3. 决定公开 module path 和分发方式，用仓库外消费测试作为门禁。
4. 修复 SDK ready 状态机，并补 `reasoningEffort`、内联图片、帧大小和请求并发上限。
5. 明确 ACP 是否承诺最新版的 resume、模型配置和按会话 MCP；要么实现，要么逐项记录为范围裁剪。
6. 提供至少一个生产会话 Backend 或官方适配模块，跑真实崩溃恢复测试。
7. 对 PTC、最新 UI/API/experimental 包逐项写新裁决，不能沿用旧包名的一句理由。
8. 建立长会话、并发 Agent、持久化、SDK 洪泛和 shutdown 的 benchmark/压力基线。
9. 在干净最终提交上重跑构建、Vet、单测、竞态、覆盖率、文档、最新版 portcheck、外部消费和恢复闭环。

在第 1 至第 4 项完成前，不应对外声明“`0.1.2-alpha.3` 已完成移植”“任何 Go 服务可直接加载”或“生产可用”。
