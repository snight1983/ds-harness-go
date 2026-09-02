# 核心能力语义对照

基准：DeepSeek Harness `0.1.2-alpha.3`。  
逐符号和逐方法签名见 [ported-api-review.tsv](ported-api-review.tsv)。

## 判定口径

| 判定 | 含义 |
|---|---|
| 等价 | 输入、输出、错误、状态和生命周期语义一致 |
| Go 化等价 | 行为一致，`AbortSignal`、联合类型、构造器或依赖注入按 Go 习惯改写 |
| 部分等价 | 主路径存在，但缺字段、分支、状态或副作用 |
| 缩水 | 明确只实现上游能力子集 |
| 缺失 | 没有可用对应能力 |
| 接线阻断 | 两边代码存在，但接口无法连接 |
| 有意裁剪 | 与仓库公开边界冲突，未实现是明确范围决定 |
| 待裁决 | 最新版出现，现有裁决和证据不足 |

## 1. Agent 与 Agent Loop

| 上游能力 / 方法 | Go 对应 | 输入与输出 | 错误、状态、副作用、取消与并发 | 判定 |
|---|---|---|---|---|
| `AgentOptions` | `agent.Options` | provider/model/maxTokens 对应；`reasoningEffort` 已补齐（`agent.ModelSelection.ReasoningEffort`、`agent.Runtime`，空串即缺失） | 档位沿 SDK 与子 Agent 路由继承 | Go 化等价 |
| `AgentRegistry.create` | `Registry.Create` | TS Promise/handle 改为 `(Handle, error)` | Go 显式 owner scope；发布和失败回滚有测试 | Go 化等价 |
| `AgentRegistry.resume` | `Registry.Resume` + Agent Loop factory | 公共入口存在 | 持久化接口已改为交付 `*session.Preparation`，接线打通 | Go 化等价 |
| `Agent.send` | `Agent.Send` | message/target/wakeup 一一对应 | Inbox 追加、唤醒和取消收敛语义已实现 | Go 化等价 |
| `followup` / `steer` / `inject` | 同名方法 | 输入消息一致；无返回 | 分别落 next-turn、next-step wake、next-step quiet | 等价 |
| `cancel(cause, keepInbox)` | `Cancel(cause, CancelOptions)` | `keepInbox` 对应 | 首个取消原因胜出；无活动时不为未来上膛 | Go 化等价 |
| `whenIdle` | `WhenIdle(ctx) error` | Promise 变为可取消的 ctx 等待 | 跟随替换驱动和维护任务；Go 多出等待方取消 | Go 化等价 |
| `runMaintenance<T>` | `RunMaintenance(ctx, func(ctx) error)` | Go 接口方法不能泛型，结果由闭包承接 | 真空闲期互斥、取消和唤醒排队语义保留 | Go 化等价 |
| `TurnBoundaryProjection` | 无共享对应 | 上游输出 open turn、last step、last boundary、last turn | Go 个别模块自行扫事件，没有统一可缓存投影 | 缺失 |
| Agent Loop create/publish | `AgentLoop.Create` / `setupAndPublish` | Create options 大体对应 | scope 所有权、失败清理、公布时序较完整 | Go 化等价 |
| Agent Loop resume | `AgentLoop.Resume` | 两边都收准备件：`Prepare` 交回 `*session.Preparation` | 准备期在公布成败之后统一结束，`Release` 幂等，迟到值照样释放 | Go 化等价 |
| 并行工具调用调度 | `executeToolCalls` | 工具调用序列输入、结果序列输出 | 有并行上限、独占屏障、取消排干、提交顺序 | 等价度高 |

## 2. Session 与持久化

| 上游能力 / 方法 | Go 对应 | 输入与输出 | 错误、状态、副作用、取消与并发 | 判定 |
|---|---|---|---|---|
| Session `append` | `Session.Append` | 事件候选变成规范事件并返回 | 阻止重入，通知后落日志，快照失效 | Go 化等价 |
| Session `events` | `Session.Events` | 返回只读语义快照 | Go 用复制缓存隔离调用方写入 | Go 化等价 |
| Store create/prepare/publish | `Store.Prepare` + `Preparation` | Go 显式准备件和发布/丢弃 | 模块内部具备正确原语 | 等价度高 |
| `encodeSeqRanges` / `decodeSeqRanges` | 无 | 上游压缩递增 seq 列表并做有界解码 | 最新持久化格式辅助能力尚未移植 | 缺失 |
| chunk row 判断和长度 | 无 | 上游识别存储 chunk row 并算长度 | 影响新版存储表示；未裁决 | 缺失 |
| Coordinator `create` / `append` | `Coordinator.Create` / `Append` | 签名按 Go 加 ctx | 按 Session ID 串行化、写后攒批和修复逻辑存在 | Go 化等价 |
| Coordinator `load` / `inspect` / `readFrom` | 同名 | 返回 Inspection/StoredSuffix | 错误包装、取消和复制边界较完整 | Go 化等价 |
| Coordinator `prepare` | `Coordinator.Prepare` | 都返回准备件 | Agent Loop 已按准备件消费（`loop.go` 的 `Prepare` 接口） | Go 化等价 |
| `borrowSession` | 无 | 上游返回带 disposer 的 prepared/stored 观察来源 | Go 没有租约和 revision 观察结果 | 缺失 |
| `ensureMaterialized` | 无公开等价 | 上游确保只有内存头的会话落成物理记录 | Go 内部写路径有物化逻辑，但没有同等公开契约 | 部分等价 |
| Backend `list` | `Backend.List` | 一次返回全部 SessionHeader | 无分页；Coordinator 不转发该方法 | 部分等价 |
| 生产 Backend | 无 | 无法执行真实持久化往返 | `storage/postgres` 是另一套 KV 接口 | 缺失 |

## 3. Tools

| 上游能力 / 方法 | Go 对应 | 输入与输出 | 错误、状态、副作用、取消与并发 | 判定 |
|---|---|---|---|---|
| `register` / `restrict` / `guard` | `Runtime.Register` / `Restrict` / `Guard` | Go 增加 ctx、owner 和 error | disposer、scope 覆盖、冲突检查明确 | Go 化等价 |
| `get` / `schemas` | 同名 | optional scope 变为 `*scope.Key` | 可见性解析与稳定顺序存在 | Go 化等价 |
| `execute` | `Runtime.Execute(ctx, input)` | Promise<Result> 变为 Result；取消在 ctx | pre/guard/body/post/finalize、panic 隔离和规范化齐全 | 等价度高 |
| `ToolExecutionInput.signal` | `Execute` 的 ctx | 字段变参数 | Go 父子 context 代替 fused AbortSignal | Go 化等价 |
| `rootCallId` | `RootCallID` | 输入字段存在；Execution 通过嵌入继承 | 机械扫描不能展开嵌入，不是缺失 | Go 化等价 |
| `timeoutMs` | `Definition.Timeout time.Duration` | 毫秒数变 Duration | 由 timeout policy 执行 | Go 化等价 |
| 呈现 `type/kind/shape` 判别字段 | `Card()` / `MarshalJSON` | 字段变只读行为 | 防止调用方改坏判别标签 | Go 化等价 |
| PTC `presentAs`、`run_code`、子派发日志 | 无 | 上游输入程序文本，输出 logs/result | 需要代码运行时、程序取消、嵌套调度和日志 | 有意裁剪 |

## 4. LLM 与图片

| 上游能力 / 方法 | Go 对应 | 输入与输出 | 错误、状态、副作用、取消与并发 | 判定 |
|---|---|---|---|---|
| Model runtime / adapter stream | `llm.Runtime` / `Adapter.Stream` | 流块和失败分类有对应 | ctx 取消、观察者和路由解析存在 | Go 化等价 |
| `ToolCallId` | `llm.CallID` | 品牌字符串变具名 string 类型 | 只是命名变化 | Go 化等价 |
| 纯文本图片投影 | `ProjectImagesForTextModel` | 图片变稳定文字 | 嵌套 ToolResult 递归处理 | 等价度高 |
| 请求图片超限投影 | `OffloadRequestImagesWithPolicy` | 张数/字节/量化策略已实现 | 移除前缀算法与上游一致 | 等价度高 |
| `offloadedImagePrefixCount` | 算法内联在投影函数 | 无独立公开函数 | 行为存在，公共复用面不存在 | Go 化折叠 |
| `ImageAttachmentAccess` / resolver | 无 | 上游提供执行世界只读路径 | 对象存储/远端世界下仍需明确映射 | 缺失 |
| `offloadedImageText(ref, access)` | 统一 `OffloadedImageText` 常量 | Go 不带附件身份、尺寸、格式或可恢复只读路径 | 最新模型可恢复提示语义落后 | 部分等价 |
| `LlmImageRequestPricing.priceImages` | 无 | 上游输出每图 visualTokens/text | 无定价接口 | 缺失 |
| model discovery operation signal | ctx/现有发现接口未形成同一公开形状 | 最新成员未裁决 | 需继续核对 | 待裁决 |

## 5. SDK

| 上游能力 / 方法 | Go 对应 | 输入与输出 | 错误、状态、副作用、取消与并发 | 判定 |
|---|---|---|---|---|
| `initialize` | `Server.Initialize` | cwd/provider/model/maxTokens 对应；`reasoningEffort` 已补齐 | 就绪标记改为初始化成功之后才立，失败可重试 | Go 化等价 |
| `prompt` | `Server.Prompt` | sessionId 对应；内联图片已补齐（`EncodedImageBlock` / `PromptBlock`） | 进来先查就绪，图片准入之后再查一次 Agent 存活 | Go 化等价 |
| `shutdown` | `Server.Shutdown` | 都是幂等收摊 | Go 不处置宿主根运行时，这是嵌入式边界的合理变化 | Go 化等价 |
| JSON-RPC request/notify | `LineTransport` | Go 用 typed result target | 请求取消和错误类型更符合 Go | Go 化等价 |
| transport `flush` | 无 | 上游可等此前 write callback | Go 写入同步到 `io.Writer`，但不能要求外层 buffered writer flush | 部分等价 |
| 行分帧 | `ReadBytes('\n')` | 坏行跳过语义一致 | 已设上限：`DefaultMaxFrameBytes` 16 MiB、`DefaultMaxConcurrentFrames` 64（槽在读循环里取，形成背压） | Go 化等价 |

## 6. ACP

| 上游能力 / 方法 | Go 对应 | 输入与输出 | 错误、状态、副作用、取消与并发 | 判定 |
|---|---|---|---|---|
| initialize/authenticate | `Bridge.Initialize` / `Authenticate` | 协议身份和图片能力对应 | Go 传输认证留给宿主 | Go 化等价 |
| new session | `Bridge.NewSession` | cwd 和 session id 对应 | Go 明确拒绝 additionalDirectories 与 MCP servers | 缩水 |
| prompt | `Bridge.Prompt` | 内容准入、停止原因返回 | 单会话一次一条、取消、事件更新排队已实现 | 等价度高（旧主路径） |
| cancel | `Bridge.Cancel` | 通知无结果 | 准入中取消和运行中取消均处理 | 等价度高 |
| session resume | `Bridge.ResumeSession` | Go 固定 method not found | 最新上游已有 `AcpSession.resume` | 缺失 |
| model config options | `Bridge.SetSessionConfigOption` | Go 固定 method not found | 最新上游已有 `AcpModelControl` 的 snapshot/options/set/pin/release | 缺失 |
| per-session MCP | `validateSessionParams` 拒绝 | 无输出 | 最新上游 `mountAcpMcpServers` 支持挂载 | 缺失 |

## 7. Subagent

| 上游能力 / 方法 | Go 对应 | 输入与输出 | 错误、状态、副作用、取消与并发 | 判定 |
|---|---|---|---|---|
| start continuable | `Runtime.StartContinuable` | request/run 对应 | 生命周期、父子所有权、恢复描述符较完整 | Go 化等价 |
| followup / report / interrupt | 同名运行时能力 | 地址、消息、权限对应 | 包含 quiet/wakeup、祖先权限和取消路径 | 等价度高 |
| list children/descendants | `ListChildren` / `ListDescendants` | child/diagnostic 联合变 Kind 结构体 | 并发读取和取消有处理 | Go 化等价 |
| fork/spawn in process | 独立 provider 包 | 父前缀继承或空白创建 | provider 名、路由继承部分靠装配配置 | 等价度高 |
| 新 `control-types.ts` catalog/prompt/interrupt receipts | 底层能力和模型工具已有，统一 DTO/校验层无直接对应 | 线上形状未完整映射 | 不能据此判底层缺失，但最新版公共面未裁决 | 待裁决 |

## 8. Compaction、Context 与 Storage

| 上游能力 / 方法 | Go 对应 | 输入与输出 | 错误、状态、副作用、取消与并发 | 判定 |
|---|---|---|---|---|
| Compaction engine | `compaction.Engine` + `basic.Engine` | 抽象类拆成接口和安装函数 | 压缩括号、摘要、失败合括号和重试状态已实现 | Go 化等价 |
| tool result pruner | `toolresultpruner` | 剪枝结果和投影对应 | `callId` 可能通过组合类型表达，需保持恢复一致性 | 等价度高 |
| instructions install/projection | `context/instructions` | 加载、作用域、渲染和投影均存在 | 仍有项目根冻结、总读取上限 TODO | 部分等价 |
| generic storage/domain | `storage` / `storage/domain` | KV 和串行领域写入存在 | 与会话事件 Backend 是不同接口 | 等价但不能替代会话持久化 |
| PostgreSQL KV | `storage/postgres` | 真实 SQL 实现存在 | 无 DSN 时集成测试跳过，当前覆盖率 5.2% | 待真实验证 |
| object store filesystem | `fs/objectstore` | 对象读写、列举等存在 | `ProcessPath` / `FileURL` 必定 panic | 部分等价 |

## 9. 总结

核心内存态 ReAct 运行时不是假实现，Agent、工具、会话和子 Agent 的主路径已达到较高完成度。当前最严重的问题不在单个算法，而在四个边界：

1. 上游基线已经变化，但清单、裁决和溯源仍停在旧快照；
2. 持久化准备件的生命周期没有穿过 Agent Loop，导致恢复闭环无法装配；
3. SDK/ACP 仍是旧版能力子集；
4. 公开 Go module、生产会话 Backend 和性能证据没有闭环。

因此当前适合继续作为开发中的 Go Agent 运行时内核，不适合标记为最新版完整移植或生产组件。
