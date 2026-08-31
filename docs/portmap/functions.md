# DeepSeek Harness 功能清单

227 个包，2009 条功能，一条一件事，每条只有「有」和「自陈无」两种取值。

来源只有一处：每个包自己的 `README.zh.md`（共 13,637 行）。**不是从包名、目录名、符号表推的。**
`docs/portmap/portmap.tsv` 那 7909 行是机器抽的符号名和签名，说不出这些符号合起来能干什么；
`docs/portmap/capabilities.md` 一个包一行，`core/tools` 那一行底下压着 35 件独立的事。
这份是拆到条的那一层。

- **有**：README 明写它做这件事。1354 条。
- **自陈无**：README 自己写明它不做这件事、或做不到、或有已知限制。655 条。
  这一类不是我加的判断，是作者写的。它决定的是「移过来之后你还得自己补什么」。

清单本身不含裁决。逐包裁决在 `docs/portmap/rulings.md`，以这份和 `required.md` 为依据。

## A. 循环、会话与持久化

44 个包，535 条，其中有 396、自陈无 139。

### `attachment/attachment`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 持久附件服务（ctx.attachments） | ImageAttachmentRef | 有 |
| 验证图片准入（不执行持久化） | validateImage() | 有 |
| 批次保存（全部或零） | saveImages() | 有 |
| 单张图片保存 | saveImage() | 有 |
| 根据元数据读取规范化附件 | readImage() | 有 |
| 确定性派生请求版本（像素和字节预算） | readImageRequest() | 有 |
| base64 编码图片批量准入（wire 入口） | admitEncodedImages() | 有 |
| 第一版仅接受 PNG、JPEG、WebP、GIF | — | 自陈无 |
| 保留策略与垃圾回收尚未实现 | — | 自陈无 |
| 通用文件、音频、视频需单独生命周期 | — | 自陈无 |

### `attachment/attachment-local`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 本地文件系统附件存储（sha256 寻址） | DSH_HOME/attachments/v1 | 有 |
| 每条消息最多 20 张、源图总 200MiB | 限额配置 | 有 |
| 规范化附件大小限制（默认 4MiB） | normalizedImageMaxBytes | 有 |
| 应用 EXIF 方向、删除元数据、8-bit sRGB | 规范化处理 | 有 |
| 宽高比缩放到 2048px（可配） | normalizedImageMaxDimension | 有 |
| 编码格式选择（PNG/WebP/JPEG 按色数） | 质量 85/80/75 尝试 | 有 |
| 请求版本投影（缩放到预算内） | readImageRequest() | 有 |
| 并发限流和 singleflight 缓存 | imageCompressionConcurrency | 有 |
| 原子写入和跨进程原子发布 | hard link 方式 | 有 |
| 对象无限期保留（垃圾回收未实现） | — | 自陈无 |
| 本地后端假定共享文件系统 | — | 自陈无 |
| 动态 GIF 只保留首帧 | — | 自陈无 |

### `compaction/command-compact`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 用户压缩命令 | /compact命令 | 有 |
| 手动压缩 | compactNow() | 有 |
| 命令无参数 | /compact(无参数) | 有 |
| 命令生命周期事件 | command/run done | 有 |
| 仅空闲执行 | busy错误 | 有 |
| 压缩报告tokens | 代码替换历史项数与token | 有 |
| 仅命令适配器 | — | 自陈无 |
| 无范围或策略参数 | — | 自陈无 |

### `compaction/compaction`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 压缩能力seam | ctx.compaction | 有 |
| 自动压缩判定 | compactIfNeeded() | 有 |
| 显式压缩 | compactNow() | 有 |
| 强制压缩范围 | compactRegion() | 有 |
| 压缩结果返回 | CompactionResult | 有 |
| 工具配对边界验证 | toolPairingBalancedBefore/After() | 有 |
| 压缩事件 | compaction/start summary end | 有 |
| 表层替换 | replace操作 | 有 |
| 压缩锁 | compaction/start未匹配阻塞 | 有 |
| 崩溃恢复 | 持久锁信号 | 有 |
| 面向用户命令而非工具 | — | 自陈无 |
| 部分单元溢出无法拆分 | — | 自陈无 |
| 单独envelope溢出不属范围 | — | 自陈无 |

### `compaction/compaction-basic`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 基础压缩后端 | BasicCompactionEngine | 有 |
| 压力检查 | ctx.tokenMeter测量 | 有 |
| 路由策略 | 适配器容量解析 | 有 |
| 工具结果剪枝 | ctx.toolResultPruner | 有 |
| 保留尾部 | retainRatio retainTokens | 有 |
| 工具对平衡 | 调整切分点 | 有 |
| 收敛重试 | compactionRetries | 有 |
| 摘要生成 | llm/stream调用 | 有 |
| 系统提示词回放 | 复用提供方缓存 | 有 |
| 框定摘要 | <compacted-summary>标签 | 有 |
| 溢出恢复 | 最大平衡缩减 | 有 |
| 失败处理 | 错误闭合持久锁 | 有 |
| 计量启发式 | — | 自陈无 |
| 溢出分类由适配器维护 | — | 自陈无 |
| 部分不可分仅envelope溢出 | — | 自陈无 |
| compactRegion需开放轮次 | — | 自陈无 |
| 摘要失败保留最新表层 | — | 自陈无 |

### `compaction/compaction-tool-result-pruner`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 工具结果剪枝服务 | ctx.toolResultPruner | 有 |
| 工具结果改写 | pruneSession() | 有 |
| 头部尾部保留 | headChars tailChars | 有 |
| 省略标记 | [...tool result middle pruned...] | 有 |
| 原始事件保留 | 日志完整保留 | 有 |
| 表层替换 | replace操作 | 有 |
| 字符预算非token预算 | — | 自陈无 |
| 剪枝仅基于语法 | — | 自陈无 |
| 字素簇可能被拆分 | — | 自陈无 |

### `context/agent-instructions`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 工作区指令加载 | AGENTS.md CLAUDE.md | 有 |
| 基线指令组合 | 全局与项目指令链 | 有 |
| 嵌套指令发现 | 文件系统工具触发 | 有 |
| 指令变更通知 | Updated instructions removed | 有 |
| 提示词预算 | maxBytes限制 | 有 |
| 提示词预算省略与截断 | 宽泛文件省略具体截断 | 有 |
| 本地overlay | AGENTS.local.md | 有 |
| 内容去重 | 字节完全一致折叠 | 有 |
| 项目根标记 | projectRootMarkers | 有 |
| Symlink跨越信任边界 | 最终组件跟随 | 有 |
| 发现跟随fs工具非shell | — | 自陈无 |
| 刷新由touch驱动无watcher | — | 自陈无 |
| 候选语义简单无import | — | 自陈无 |
| 指令内容受限无摘要 | — | 自陈无 |

### `context/file-reference`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 文件引用发现seam | ctx.fileReferences | 有 |
| 文件补全查询 | list(agent, query) | 有 |
| @语法识别 | activeAtToken() | 有 |
| 文件提及格式 | formatFileMention() | 有 |
| 远程API | fileReferences/list | 有 |
| 路径候选仅参考 | — | 自陈无 |
| 无文件内容引用对象 | — | 自陈无 |

### `context/file-reference-local`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 本地文件系统实现 | WorkspaceFileSearch | 有 |
| 工作区索引 | cwd为根目录 | 有 |
| 模糊排序 | 递归索引排序 | 有 |
| 不跟随目录symlink | 符号链接策略 | 有 |
| 索引失效 | 工具结果后失效 | 有 |
| 文件引用指引 | 系统提示词段 | 有 |
| 有界索引 | maxEntries限制 | 有 |
| 排除目录 | excludedDirectories | 有 |
| 宿主本地命名空间 | — | 自陈无 |
| 无.gitignore语义 | — | 自陈无 |

### `context/session-reference`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 会话引用解析 | ctx.sessionReferenceResolver | 有 |
| 候选列表 | listCandidates() | 有 |
| 引用准备 | prepare() | 有 |
| 快照语义 | readSurface()快照 | 有 |
| URI编码 | encodeSessionReferenceUri() | 有 |
| Markdown提及 | @[label](uri) | 有 |
| 规范mention解析 | parseSessionReferenceText() | 有 |
| 最多三个不同源 | maxReferences限制 | 有 |
| 候选上限 | candidateLimit配置 | 有 |
| 字节预算 | maxReferenceBytes限制 | 有 |
| 不支持消息正文检索 | — | 自陈无 |
| 受信任调用方边界 | — | 自陈无 |
| 仅投影文本 | — | 自陈无 |
| 无实时链接 | — | 自陈无 |

### `context/time-context`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 时间上下文注入 | ctx.time-context | 有 |
| 浏览器时区采样 | 每轮采样 | 有 |
| 时区回退 | timeZone配置 | 有 |
| 刷新间隔 | refreshIntervalMs | 有 |
| IANA时区格式 | ISO时间戳 | 有 |
| 经过时长 | 整秒单位 | 有 |
| 混合轮次询问 | 多时区询问澄清 | 有 |
| 仅第一步 | pre-step前置监听器 | 有 |
| 时间倒退限制 | 经过时长限零 | 有 |
| 仅提示词源信息 | — | 自陈无 |
| 混合轮次询问非猜测 | — | 自陈无 |
| 回退值无权威 | — | 自陈无 |
| 压缩间历史成本 | — | 自陈无 |

### `context/tmux-context`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Tmux位置上下文 | 会话window pane信息 | 有 |
| Tmux检查 | ctx.shell执行tmux命令 | 有 |
| TTY验证 | pane_tty与进程tty匹配 | 有 |
| 状态刷新 | 每轮采样 | 有 |
| 变化抑制 | 状态相同无重复注入 | 有 |
| 间隔调度 | refreshIntervalMs | 有 |
| 窗口布局描述 | pane树紧凑描述 | 有 |
| 仅第一步 | 轮次开始采样 | 有 |
| 仅自身位置 | 不采集相邻pane | 自陈无 |
| 仅布局无尺寸 | 省略像素尺寸 | 自陈无 |
| 制表符分隔错误 | — | 自陈无 |
| 基于tty判定 | — | 自陈无 |

### `core/agent`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Agent 创建与基本接口 | ctx.agents.register() | 有 |
| Agent 注册表追踪实时agent | ctx.agents | 有 |
| 带作用域agent创建 | ctx.agents.enter(agent, owner) | 有 |
| 发起方作用域管理 | ctx.agents.currentInitiator() | 有 |
| withInitiator边界执行 | ctx.agents.withInitiator(agent, operation) | 有 |
| 工厂API创建agent | ctx.agents.create(options) | 有 |
| 工厂API恢复持久化agent | ctx.agents.resume(options) | 有 |
| Agent消息收件箱操作 | agent.inbox.append/claim/replace | 有 |
| Agent followup排队 | agent.followup(message) | 有 |
| Agent steering引导消息 | agent.steer(message) | 有 |
| Agent inject注入上下文 | agent.inject(message) | 有 |
| Agent 取消操作 | agent.cancel(cause, options?) | 有 |
| Agent 空闲观察 | agent.whenIdle() | 有 |
| agent/created事件 | agent/created事件 | 有 |
| agent/disposed事件 | agent/disposed事件 | 有 |
| agent/pre-step waterfall | agent/pre-step | 有 |
| agent/request-error恢复 | agent/request-error | 有 |
| agent/turn-stopping决策 | agent/turn-stopping | 有 |
| agent/inbox事件投影 | agent/inbox/spliced | 有 |
| 折叠已消费工作 | foldConsumedWork(events) | 有 |
| 发起方作用域只存在于进程内 | — | 自陈无 |
| 代理间通道仅限于委派 | — | 自陈无 |
| 取消默认清空inbox | — | 自陈无 |
| 多消息源合并 | — | 自陈无 |
| 会话启动设置门禁 | — | 自陈无 |

### `core/agent-default-model`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 部署级模型默认值 | ctx.agentDefaultModel | 有 |
| 当前模型选择查询 | ctx.agentDefaultModel.currentSelection() | 有 |
| 模型选择保存 | ctx.agentDefaultModel.saveSelection() | 有 |
| 推理强度配置 | reasoningEffort | 有 |
| 未挂载设置提供方时saveSelection无操作 | — | 自陈无 |

### `core/agent-loop`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Agent循环驱动器 | ctx.agentLoop | 有 |
| Agent创建 | ctx.agentLoop.create() | 有 |
| Agent恢复 | ctx.agents.resume() | 有 |
| 工厂API | AgentFactory | 有 |
| 并行工具调用 | maxParallelToolCalls | 有 |
| Agent配置声明 | agents配置 | 有 |
| 发起方作用域隔离 | withInitiator边界 | 有 |
| 流式助手消息 | assistant/message | 有 |
| 工具调用与结果处理 | tool/call tool/result | 有 |
| 取消中止流式输出 | agent.cancel() | 有 |
| 不可分工具对保护 | 工具调用／结果配对 | 有 |
| 请求头记录 | request/header | 有 |
| 幂等崩溃恢复 | TOOL_OUTCOME_UNKNOWN | 有 |
| 独占与并发调用分类 | isConcurrencySafe | 有 |
| 轮次预算限制 | — | 自陈无 |
| 配置agent无逐agent persona | — | 自陈无 |
| 配置agent无逐agent setup钩子 | — | 自陈无 |

### `core/agent-tool-presentation`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 工具呈现模式选择 | ctx.tools.presentAs() | 有 |
| Native模式 | mode: native | 有 |
| Code Mode | mode: code | 有 |
| Both模式 | mode: both | 有 |
| Code Mode等待代码运行时 | ctx.codeRuntime | 有 |
| 每agent一次呈现方式声明 | — | 自陈无 |
| 运行时仍在宿主平面 | — | 自陈无 |

### `core/scope`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 带作用域注册原语 | createScope(ctx, key) | 有 |
| 作用域键关联 | scopeOf(ctx) | 有 |
| 作用域事件路由 | scopeTarget(base, key) | 有 |
| 作用域父关系链 | bindScopeParent(key, parent) | 有 |
| 作用域继承与遮蔽 | 子作用域继承祖先注册 | 有 |
| 作用域dispose | Scope.dispose() | 有 |
| 作用域层存储 | ScopedLayers<L> | 有 |
| 只有感知作用域表层隔离 | — | 自陈无 |
| 单个上下文单最近作用域键 | — | 自陈无 |
| 服务可达性来自创建者 | — | 自陈无 |

### `core/session`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 事件溯源会话日志 | Session | 有 |
| 会话创建 | ctx.sessions.create() | 有 |
| 会话持久化检查点 | ctx.sessions.flush(session) | 有 |
| 会话fork | ctx.sessions.fork() | 有 |
| 会话派生消息 | session.deriveMessages() | 有 |
| 会话surface投影 | session.surface | 有 |
| 表层事件操作 | session.append(type, data) | 有 |
| 有序生命周期 | prepare/enter/announce | 有 |
| session/created和session/disposed事件 | session生命周期事件 | 有 |
| session/event追加 | session事件流 | 有 |
| session/flush检查点 | 显式持久化屏障 | 有 |
| 会话头部元数据 | SessionHeader | 有 |
| 轮次\/步骤关系 | turn/start turn/end | 有 |
| 工具调用结果对 | tool/call tool/result | 有 |
| Assistant消息分片 | assistant/chunk | 有 |
| 请求头记录 | request/header | 有 |
| 消息标识 | MessageId | 有 |
| 崩溃恢复合成结果 | TOOL_NOT_STARTED TOOL_OUTCOME_UNKNOWN | 有 |
| 表层替换操作 | replace surfaceOp | 有 |
| 事件版本机制 | SESSION_FORMAT_VERSION | 有 |
| 会话分支\/树结构 | — | 自陈无 |
| fork仅在稳定边界 | — | 自陈无 |
| 格式版本预发布无迁移 | — | 自陈无 |
| TurnEndReasonMap无ACP变体 | — | 自陈无 |

### `core/system-prompt`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 系统提示词组装注册表 | ctx.systemPrompt | 有 |
| 提示词段贡献 | ctx.systemPrompt.section() | 有 |
| 动态上下文贡献 | ctx.systemPrompt.context() | 有 |
| 上下文抑制 | ctx.systemPrompt.suppressRuntimeContext() | 有 |
| 工具schema提供 | ctx.systemPrompt.tools() | 有 |
| 提示词变量 | ctx.systemPrompt.variable() | 有 |
| 提示词组装 | ctx.systemPrompt.assemble() | 有 |
| system-prompt/assemble waterfall | 提示词变换 | 有 |
| 完整段约束 | complete: true | 有 |
| 工具顺序配置 | toolOrder配置 | 有 |
| Harness身份 | includeHarnessIdentity | 有 |
| 运行时上下文包含 | includeRuntimeContext | 有 |
| 部署persona | persona配置 | 有 |
| 变量插值 | {{variable}} | 有 |
| 按作用域遮蔽 | agent.ctx段与变量遮蔽 | 有 |
| 表达字面花括号缺转义 | — | 自陈无 |
| toolOrder配置错误在提示词组装时出现 | — | 自陈无 |
| 共享同order值的段按注册顺序排序 | — | 自陈无 |
| 部署只从配置与组合来源 | — | 自陈无 |

### `core/tools`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 工具注册表 | ctx.tools.register() | 有 |
| 工具schema声明 | ToolDefinition | 有 |
| 工具执行流水线 | tools/pre-execute execute post-execute | 有 |
| 工具预执行门禁 | tools/pre-execute | 有 |
| 工具守卫 | ctx.tools.guard() | 有 |
| 工具执行 | ctx.tools.execute() | 有 |
| 工具执行模式分类 | executionMode() parallel\/exclusive | 有 |
| 工具schema组装 | ctx.systemPrompt.tools() | 有 |
| 工具呈现模式 | presentAs mode配置 | 有 |
| Native函数调用 | mode: native | 有 |
| Code Mode | mode: code | 有 |
| Code Mode SDK生成 | tools:sdk段 | 有 |
| 运行代码 | run_code传输 | 有 |
| Code Mode子调用分派 | tool/code-dispatch事件 | 有 |
| 工具结果呈现 | presentResult返回意图 | 有 |
| 工具调用呈现 | presentCall返回意图 | 有 |
| 通用卡片视图 | card: generic | 有 |
| 终端卡片视图 | card: terminal | 有 |
| Diff卡片视图 | card: diff | 有 |
| 搜索卡片视图 | card: search | 有 |
| 文件读取卡片视图 | card: read | 有 |
| Web查询卡片视图 | card: web | 有 |
| JSON Schema工具定义 | defineTool() | 有 |
| 类型化工具参数 | ParameterSchemaSpec | 有 |
| 工具参数验证 | validateArgs() | 有 |
| 工具结果规范化 | validateJsonSchemaValue() | 有 |
| 协作式取消 | exec.signal | 有 |
| 取消等待完全停稳 | — | 自陈无 |
| 并发策略非事件门禁 | — | 自陈无 |
| pre-execute无参数改写 | — | 自陈无 |
| subagent结构输出要求对象根 | — | 自陈无 |
| timeoutMs仅声明无强制 | — | 自陈无 |
| Code Mode SDK语言由运行时决定 | — | 自陈无 |
| Code Mode中间值无字节上限 | — | 自陈无 |
| Code Mode每次运行新状态 | — | 自陈无 |

### `credentials/authorization`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 授权 Service Definition（ctx.authorization） | AuthorizationSession | 有 |
| 注册授权 flow（key、label、methods） | registerFlow() | 有 |
| flow 运行期间可交互提示和通知 | notify(), prompt() | 有 |
| 同一键同时只允许一次尝试（inFlight） | ALREADY_IN_FLIGHT 错误 | 有 |
| 取消正在进行的授权 | cancel() 方法 | 有 |
| 授权结算事件（授权、取消、失败） | authorization/settled 事件 | 有 |
| flow 不可恢复（刷新丢弃） | — | 自陈无 |
| 没有吊销（logout = deleteRecord） | — | 自陈无 |
| 没有 flow 的键是惰性的 | — | 自陈无 |

### `credentials/credentials`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 凭据 Service Definition（ctx.credentials） | CredentialRef 和 CredentialKey | 有 |
| 按操作解析凭据（无跨操作缓存） | resolve() 每次调用 | 有 |
| 空存储值等于不存在 | — | 自陈无 |
| CredentialRef：环境变量引用（分层覆盖） | credentialRef() 函数 | 有 |
| CredentialKey：插件拥有的记录（/） | credentialKey() 函数 | 有 |
| 引用设置和取消 | set(), unset() 方法 | 有 |
| 记录修改与独占写入 | modifyRecord() 方法 | 有 |
| 记录列举（提供方无法列举引用） | listRecords() | 有 |
| 引用变更事件 | credentials/reference-updated 事件 | 有 |
| 遮蔽规则明确报错 | writable 标志 | 有 |
| 引用不提供枚举 | — | 自陈无 |
| 引用限定为环境变量形状 | — | 自陈无 |
| 进程环境变化不可见 | — | 自陈无 |

### `credentials/credentials-local`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 文件型凭据提供方（四层来源） | 环境、credentials.yaml、.env | 有 |
| 进程环境优先且只读 | source: env, writable: false | 有 |
| DSH_HOME/.credentials.yaml 受管存储 | 可写层 | 有 |
| 项目和用户 .env 回退层 | 读取不写入 | 有 |
| 版本化 YAML 文档格式 | version: 1 | 有 |
| 老布局自动升级（缺省 version 则升级） | 迁移逻辑 | 有 |
| 原子写入与跨进程锁 | 补丁修改 | 有 |
| 热重载外部编辑 | watch, debounceMs 配置 | 有 |
| 同一引用并发写入是后写胜出 | — | 自陈无 |
| 同 UID 进程可读取该文档 | — | 自陈无 |
| 环境变化不可见（快照启动时冻结） | — | 自陈无 |

### `session-query/session-log-export`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 会话导出下载 | /export命令 | 有 |
| ZIP生成与下载 | ApiProxy端点 | 有 |
| 预检与浏览器下载 | HEAD预检GET下载 | 有 |
| 并发折叠 | 同时仅一项下载 | 有 |
| 命令与按钮入口 | /export和Header按钮 | 有 |
| 仅浏览器下载 | — | 自陈无 |
| 需逐session原始工件 | — | 自陈无 |
| 预检仅报前缀错误 | — | 自陈无 |

### `session-query/session-query`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 会话查询引擎 | ctx.sessionQuery | 有 |
| 列表会话 | listSessions() | 有 |
| 读取会话 | readSession() | 有 |
| 过滤会话 | filterSessions() | 有 |
| 过滤事件 | filterEvents() | 有 |
| 读取标题快照 | readTitleSnapshots() | 有 |
| 列表事件 | listEvents() | 有 |
| 读取表层 | readSurface() | 有 |
| 读取事件 | readEvent() | 有 |
| 追踪会话 | traceSession() | 有 |
| 追踪事件 | traceEvent() | 有 |
| 搜索会话 | searchSessions() | 有 |
| 搜索事件 | searchEvents() | 有 |
| 过滤器组合与AND语义 | 过滤器数组 | 有 |
| 文本过滤Unicode正则 | 不区分大小写正则 | 有 |
| 无调用方授权 | — | 自陈无 |
| 无注册表或面向模型工具 | — | 自陈无 |

### `session-query/session-query-sqlite`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| SQLite会话查询后端 | SqliteSessionQueryEngine | 有 |
| FTS5全文搜索 | 搜索实现 | 有 |
| 跨会话分组 | 最强事件分组 | 有 |
| 准确排序 | span数降序长度升序 | 有 |
| 摘录生成 | snippet基于高亮 | 有 |
| unicode61分词器 | token召回 | 有 |
| 实时优先逻辑语料库 | TEMP表遮蔽 | 有 |
| 派生数据库 | 独立索引数据库 | 有 |
| openAt配置 | startup first-search never | 有 |
| 持久化观察 | 非修改式检查 | 有 |
| 分页游标 | 不透明SessionSearchCursor | 有 |
| 无调用方授权 | — | 自陈无 |
| 同步查询执行 | — | 自陈无 |
| Token召回非子字符串 | — | 自陈无 |
| 单一所有者派生索引 | — | 自陈无 |

### `session-query/tool-session-query`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 会话查询工具 | session_search等五个工具 | 有 |
| 跨会话搜索 | session_search | 有 |
| 单会话事件搜索 | session_event_search | 有 |
| 会话血缘追踪 | session_trace | 有 |
| 事件血缘追踪 | session_event_trace | 有 |
| 事件读取 | session_event_read | 有 |
| 工作区权限检查 | cwd相等性检查 | 有 |
| 互斥搜索执行 | 搜索排他分发 | 有 |
| 精确跟踪并行执行 | 跟踪读取可并行 | 有 |
| 系统提示词指引 | 既往历史指引 | 有 |
| 搜索最多部署上限 | maxSearchResults | 有 |
| 工作区身份字符串相等 | — | 自陈无 |
| 未挂载通用spill | — | 自陈无 |

### `session/session-checkpoint-policy`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 持久性检查点策略 | session-checkpoint-policy | 有 |
| 模型请求前检查点 | llm/stream拦截 | 有 |
| 工具分派前检查点 | tools/execute拦截 | 有 |
| 预步骤检查点 | agent/pre-step | 有 |
| 检查点失败阻止处理 | — | 自陈无 |
| 流分片无逐分片检查点 | — | 自陈无 |
| 已持久化调用结果未知无法证明 | — | 自陈无 |

### `session/session-persistence`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 会话持久化能力seam | SessionPersistence服务 | 有 |
| 后端元数据读取 | locate() | 有 |
| 原始工件读取 | readRaw() | 有 |
| 会话创建 | create() | 有 |
| 事件追加 | append() | 有 |
| 会话准备恢复 | prepare() | 有 |
| 会话加载 | load() | 有 |
| 会话检查 | inspect() | 有 |
| 会话后缀读取 | readFrom() | 有 |
| 会话列表 | list() | 有 |
| 快照修订查询 | listSnapshots() | 有 |
| 崩溃修复合成闭合 | interruptedTurnClosers | 有 |
| 写入协调器 | PersistenceCoordinator | 有 |
| 有界批处理 | writeBatchMaxDelayMs | 有 |
| 无删除或保留接口 | — | 自陈无 |
| list无分页无过滤 | — | 自陈无 |
| 修复时仅合成closer | — | 自陈无 |

### `session/session-persistence-jsonl`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| JSONL会话存储后端 | JSON行存储 | 有 |
| 压缩支持 | compression: zstd | 有 |
| 分片打包行 | packChunks配置 | 有 |
| Zstandard帧 | frame校验 | 有 |
| 项目目录布局 | cwd规范化目录 | 有 |
| 会话id编码 | 路径安全转义 | 有 |
| 延迟实体化 | create不写入 | 有 |
| 仅追加 | 已flush不重写 | 有 |
| 崩溃恢复保留有效尾部 | frame校验合成closer | 有 |
| 非修改式检查 | inspect不截断 | 有 |
| 轻量修订 | device inode size时间戳 | 有 |
| 仅加载当前编码与版本 | — | 自陈无 |
| 平铺文件布局不加载 | — | 自陈无 |
| 压缩文件不能直接行读取 | — | 自陈无 |
| 不删除会话文件 | — | 自陈无 |
| 每会话一个活动writer | — | 自陈无 |
| POSIX需硬链接支持 | — | 自陈无 |

### `session/session-persistence-sqlite`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| SQLite会话存储后端 | SQLite数据库 | 有 |
| 打包行存储 | 物理打包标签 | 有 |
| 选择性压缩 | Zstandard level3 | 有 |
| Delta编码 | varint编码 | 有 |
| Schema版本 | schema17 | 有 |
| 字段映射 | TEXT BLOB存储 | 有 |
| 修复检查 | 崩溃恢复 | 有 |
| 过渡性设计 | 预发布schema不稳定 | 自陈无 |

### `session/session-projection`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 会话投影注册表 | ctx.sessionProjections | 有 |
| 投影单元注册 | register() | 有 |
| 投影变更订阅 | onChanged() | 有 |
| 单元状态查询 | stateOf() | 有 |
| 投影快照 | snapshot() | 有 |
| 全量值事件规则 | 表层carry完整状态 | 有 |
| 同引用无工作 | Object.is把守变更流 | 有 |
| 每个尾页携带每个client-visible key | — | 自陈无 |
| 单元表进程级未逐会话 | — | 自陈无 |
| 主动驱动逐事件触达 | — | 自陈无 |
| 注册表cell仅内存 | — | 自陈无 |
| 单元同步纪律仅部分可机械把关 | — | 自陈无 |

### `session/session-projection-cache`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 持久投影缓存 | ctx.sessionProjectionCache | 有 |
| 检查点折叠 | (key → {ver, seq, val}) | 有 |
| Fail-soft写入 | 写失败记警告 | 有 |
| 版本不匹配丢弃 | stateVersion校验 | 有 |
| 状态schema校验 | parse()验证 | 有 |
| 记录绑定日志生命周期 | header验证 | 有 |
| 日志领先缓存跟随 | 先flush再缓存 | 有 |
| 必写点 | turn/end会话释放 | 有 |
| 条数节流 | writeEveryEvents | 有 |
| 间隔节流 | writeIntervalMs | 有 |
| 列表读零IO | cachedSnapshot() | 有 |
| 冷读阶梯 | readFrom anchor | 有 |
| 不提供淘汰或保留 | — | 自陈无 |
| 间隔节流粗粒度控制 | — | 自陈无 |
| coldSnapshot读取不去重 | — | 自陈无 |

### `session/session-stats`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 会话数字统计投影 | sessionStats | 有 |
| 轮次计数 | turn统计 | 有 |
| 步骤计数 | step统计 | 有 |
| LLM耗时 | llmMs | 有 |
| 首token延迟 | ttftMs ttftSteps | 有 |
| 解码耗时 | decodeMs decodeTokens | 有 |
| 工具耗时 | toolMs | 有 |
| 仅Web bundle | — | 自陈无 |
| 步数统计已发生工作 | — | 自陈无 |
| 被取消步计数不计时 | — | 自陈无 |
| 计数日志口径不surface | — | 自陈无 |

### `session/session-telemetry`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 遥测能力seam | SessionTelemetrySink | 有 |
| 记录交接 | emit()入队 | 有 |
| 轮次结束提示 | flush() | 有 |
| 关闭排空 | shutdown() | 有 |
| 脱敏waterfall | sessionTelemetry/record | 有 |
| 共享披露 | SessionTelemetrySharingStatus | 有 |
| 实时捕获 | live mode | 有 |
| 按需捕获 | on-demand mode | 有 |
| 分片投影 | 首分片投影其余丢弃 | 有 |
| 游标水位线 | 已交接最高seq | 有 |
| 尽力投递 | — | 自陈无 |
| 无内置脱敏规则 | — | 自陈无 |
| 按需脱敏使用当前状态 | — | 自陈无 |

### `session/session-telemetry-otel`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| OpenTelemetry后端 | OTel SDK | 有 |
| FULL模式 | 实时投递所有记录 | 有 |
| FEEDBACK_ONLY模式 | 反馈时回放 | 有 |
| DISABLED模式 | 默认本地 | 有 |
| OTLP/HTTP导出 | exporter.url | 有 |
| 身份信息 | service.name version user.id | 有 |
| 上游实验性源码树 | — | 自陈无 |
| 真实collector由SDK负责 | — | 自陈无 |
| 反馈时快照无前缀副本 | — | 自陈无 |

### `session/session-title`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 日志支持会话标题 | ctx.sessionTitle | 有 |
| 回退标题生成 | fallbackMaxWords maxBytes | 有 |
| 提供方注册 | register() | 有 |
| 标题刷新 | refresh() | 有 |
| 用户显式标题 | rename() | 有 |
| 标题钉住 | 用户来源标题钉住 | 有 |
| session/title事件 | 日志标题事件 | 有 |
| 删除标题无解钉 | — | 自陈无 |
| 提供方注册最多一个 | — | 自陈无 |

### `session/session-title-all-prompts-llm`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 全消息标题提供方 | all-prompts节奏 | 有 |
| 所有用户消息总结 | ctx.llm调用 | 有 |
| 输入溢出失败 | maxInputBytes检查 | 有 |
| 输入溢出无摘要继续 | — | 自陈无 |
| 无权重或过滤 | — | 自陈无 |

### `session/session-title-first-prompt-llm`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 首消息标题提供方 | first-prompt节奏 | 有 |
| 首条消息总结 | ctx.llm调用 | 有 |
| 全新会话一次自动 | 首次创建回退时 | 有 |
| 第一消息可能无代表性 | — | 自陈无 |
| fork保留标题绝不自动 | — | 自陈无 |

### `session/session-title-llm`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 共享标题生成策略 | registerSessionTitleLlmProvider() | 有 |
| 路由覆盖 | provider model | 有 |
| 输入验证 | maxInputBytes检查 | 有 |
| 超时与取消 | GenerateOptions.signal | 有 |
| 请求记录 | session/title-llm-request | 有 |
| 目标词数配置 | targetWords targetCjkCharacters | 有 |
| 仅接受文本输出 | — | 自陈无 |
| 字节上限无剪裁 | — | 自陈无 |

### `storage/storage`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 非会话数据存储中心 | ctx.storage | 有 |
| 后端注册 | backend表 | 有 |
| 数据形式挂载 | mount() form() | 有 |
| KV数据分面 | kv facet | 有 |
| 后端并排挂载 | json sqlite | 有 |
| 数据形式扩展 | StorageForms合并 | 有 |
| 数据形式按需解析 | form缺失抛错 | 自陈无 |

### `storage/storage-domain`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 领域KV存储 | ctx.storageDomain | 有 |
| 领域定义 | defineDomain | 有 |
| 领域打开 | Domain.open() | 有 |
| 内存状态权威 | 同步读异步串行写 | 有 |
| 变更事件 | domain/changed | 有 |
| 后端路由 | routes配置 | 有 |
| 无跨表事务 | — | 自陈无 |
| 无二级索引或多段键 | — | 自陈无 |
| 变更仅进程内可见 | — | 自陈无 |

### `storage/storage-json`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| JSON后端 | json后端注册 | 有 |
| 单元JSON文件 | .json存储 | 有 |
| 原子写入 | fsync rename | 有 |
| 延迟实体化 | 首次写入物化 | 有 |
| 外来文件拒绝 | malformed-medium | 有 |
| Windows持久性libuv rename | — | 自陈无 |
| 无跨进程写锁 | — | 自陈无 |

### `storage/storage-sqlite`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| SQLite后端 | sqlite后端注册 | 有 |
| 物理STRICT表 | u__ | 有 |
| JSON值存储 | TEXT字段 | 有 |
| 单元版本跟踪 | units表 | 有 |
| 逐语句原子性 | 单预处理语句 | 有 |
| DatabaseSync同步 | — | 自陈无 |
| 无忙等待重试 | — | 自陈无 |
| Schema版本预发布 | — | 自陈无 |
| 重复了会话持久化逻辑 | — | 自陈无 |

## B. 模型、工具与会话内能力

43 个包，362 条，其中有 243、自陈无 119。

### `feedback/command-feedback`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 用户反馈采集：/feedback命令通过recordFeedback追加feedback/record事件 | /feedback / recordFeedback | 有 |
| 会话共享披露：确认文本点名会话id及共享状态 | SessionTelemetrySharingStatus | 有 |
| 已知限制：没有反馈检索或管理surface | 无反馈聚合/分类工具 | 自陈无 |
| 已知限制：没有结构化字段 | 自由文本字符串 | 自陈无 |
| 已知限制：不支持修改或撤回 | 会话日志仅追加 | 自陈无 |

### `feedback/message-feedback`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 消息反馈伴随记录：ctx.messageFeedback为单条已完成assistant消息的可编辑反馈 | messageFeedback.list/put/delete | 有 |
| 数据模型：MessageFeedbackItem包含messageId/rating/note/version/createdAt/updatedAt | 数据结构 | 有 |
| 持久存储：storage-domain中按SessionId每个一行 | message_feedback存储域 | 有 |
| 版本冲突检查：put支持ifVersion参数实现compare-and-set | version-conflict | 有 |
| 已知限制：缺少客户端聚合与UI | Host Remote契约已发布 | 自陈无 |
| 已知限制：Compare-and-set仅限单进程 | 多Host进程可能丢失更新 | 自陈无 |
| 已知限制：没有持久Session删除级联 | 会保留空行 | 自陈无 |

### `goal/command-goal`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 用户命令 `/goal` 控制（基于 ctx.goals） | `/goal`, `/goal edit`, `/goal pause/resume/clear` 命令 | 有 |
| 目标创建、编辑、暂停、恢复、清除 | 命令处理与验证 | 有 |
| 目标附加参考图片 | 图片块随 create/edit 提交 | 有 |
| 仅纯文本交互 | — | 自陈无 |
| 没有逐命令 Round 上限参数 | — | 自陈无 |

### `goal/goal`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Goal 服务：同会话目标状态（`ctx.goals`） | `create()`, `edit()`, `pause()`, `resume()`, `complete()`, `block()`, `clear()` | 有 |
| 目标生命周期与持久化（goal/change 事件） | Phase: active/paused/completed/blocked | 有 |
| Round 预算与续行启用 | `defaultMaxGoalRounds` 配置，进程本地续行状态 | 有 |
| 严格回放与不变量检查 | 日志回放与损坏检测 | 有 |
| 只负责状态，不负责任务调度 | — | 自陈无 |
| 只有 Round 数量预算 | — | 自陈无 |
| 没有独立评估器 | — | 自陈无 |
| 信任进程内生产方 | — | 自陈无 |

### `goal/goal-round-driver`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Goal Round 续行驱动（基于 ctx.goals） | `agent/pre-step` 监听与 Round 预留 | 有 |
| 目标 Round 提示词注入 | `` 标签块 | 有 |
| Idle 检查点与 Round 准入 | 驱动器状态机 | 有 |
| 持久性与 flush 检查点 | 会话持久化 barrier | 有 |
| 生命周期与插件卸载竞态 | Teardown 取消与 goal 停用 | 有 |
| 没有独立评估器 | — | 自陈无 |
| 只在同一会话执行 | — | 自陈无 |
| 已接受队列的卸载竞态 | — | 自陈无 |
| 异常情况不自动重试 | — | 自陈无 |

### `goal/tool-goal`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Goal 工具（`get_goal`, `create_goal`, `update_goal`） | 三个工具与权限检查 | 有 |
| 目标权限（require user/message 或 steering） | 权限验证 | 有 |
| Blocked 阈值机制 | `blockedAfterConsecutiveRounds` 配置 | 有 |
| 语义意图仍由模型判断 | — | 自陈无 |
| 阻塞条件是否相同仍由模型判断 | — | 自陈无 |
| 不负责调度或直接面向人类呈现 | — | 自陈无 |

### `guard/repeat-tool-reminder`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 重复工具调用检测与提醒 | 按工具与规范参数跟踪 | 有 |
| 递增阈值与详细提醒 | `thresholds` 配置，参数预览 | 有 |
| 被排除工具对链透明 | `include`/`exclude` 模式 | 有 |
| 被拒绝调用也计数 | `tools/post-execute` 位置 | 有 |
| 仅检测精确匹配 | — | 自陈无 |
| 压缩不会重置链 | — | 自陈无 |
| 仅提供建议 | — | 自陈无 |
| Subagent 之间不共享链 | — | 自陈无 |
| 合理幂等超过阈值后仍收提醒 | — | 自陈无 |

### `guard/timeout-policy`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 工具调用超时强制执行 | `tools/execute` 包装层监听 | 有 |
| 工具自声明的 timeoutMs 预算 | `ToolDefinition.timeoutMs` 读取 | 有 |
| 协作式取消通知（非硬终止） | `exec.signal` 派生截止时间 | 有 |
| 与其他 tools/execute 包装层组合 | 注册顺序决定语义 | 有 |
| 协作式，绝不硬终止 | — | 自陈无 |
| 没有统一预算 | — | 自陈无 |

### `interaction/commands`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 面向用户命令注册表（ctx.commands） | CommandRegistry | 有 |
| 命令注册和执行 | ctx.commands.register(), execute() | 有 |
| 命令名称、描述、输入描述符和图片支持 | CommandDefinition | 有 |
| 持久化命令日志事件（command/run, command/done） | 事件记录 | 有 |
| 命令解析和非结构化文本输入 | parseCommand() | 有 |
| 仅支持非结构化文本输入 | — | 自陈无 |
| 副作用采用协作式取消 | — | 自陈无 |

### `interaction/permission-presets`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 权限预设组合（sandbox/mode + approval/policy） | ctx.permissionPresets | 有 |
| 预设切换和持久化 | set(), current() 方法 | 有 |
| settings 命名空间与默认值 | defaultPreset 配置 | 有 |
| session 投影和命令支持 | permissions 投影单元, /permissionPresets 命令 | 有 |
| 只组合两个机制级可调参数 | — | 自陈无 |
| custom 只能推导得出 | — | 自陈无 |
| 预设表是进程级配置 | — | 自陈无 |

### `interaction/tool-ask-user`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 模型侧 ask_user_question 工具 | ToolRuntime 工具 | 有 |
| 问题数组、id、问题文本、标题、选项、多选标志 | 工具参数 | 有 |
| 推荐选项标签末尾加 (Recommended) | UI 呈现 | 有 |
| 自由填写回答与选项组合 | custom 字段 | 有 |
| 待处理问题会阻塞工具调用 | — | 自陈无 |
| 子 agent 不能向用户提问（DELEGATED_CALLER 拒绝） | — | 自陈无 |
| 回答渲染为 JSON 文本 | — | 自陈无 |

### `interaction/user-approval`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 与通道无关的一次性审批 seam（ctx.approval） | ApprovalPolicy ask/never | 有 |
| ask 策略：咨询已配置应答者 | 运行时上下文快照 | 有 |
| never 策略：自动拒绝（不请求升权） | 拒绝通知 | 有 |
| 审批事件记录（approval/asked, approval/decided） | 仅日志不进模型 | 有 |
| 审批策略变更事件 | approval/policy 事件 | 有 |
| 请求只在尚未结束的轮次内有效 | — | 自陈无 |
| 仅存在一次性授权 | — | 自陈无 |
| 请求不携带工具参数 | — | 自陈无 |
| 没有内置应答者 | — | 自陈无 |

### `interaction/user-questions`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 用户交互 Service Definition（ctx.userQuestions） | UserQuestionService | 有 |
| 注册 UI 侧提供方 | ctx.userQuestions.registerProvider() | 有 |
| 向活跃提供方提问并等待回答 | ctx.userQuestions.ask() | 有 |
| 问题表单词汇（text/secret/select） | AskUserQuestionRequest | 有 |
| 呈现意图声明（plan-review 等） | intent 字段 | 有 |
| 每个上下文只能有一个提供方 | — | 自陈无 |
| 词汇仅包含问题表单形态 | — | 自陈无 |

### `jobs/jobs`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Job 注册表约定（`ctx.jobs`） | `start()`, `get()`, `list()`, `read()`, `kill()`, `wait()`, `onJobDone()`, `onJobsChanged()` | 有 |
| Job 所有者隔离与容量管理 | 按 owner 的并发限制 | 有 |
| Job 生命周期与取消 | Running/stopping/done 状态 | 有 |
| Job 输出限制与流读取 | `outputLimitBytes` 配置 | 有 |
| 流输出只有一个消费游标 | — | 自陈无 |
| 约定是进程内的 | — | 自陈无 |

### `jobs/jobs-local`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 本地 Job 注册表实现 | `LocalJobRegistry` 内存存储 | 有 |
| 每 owner 的并发限制与容量 | `maxConcurrentJobsPerOwner` 配置 | 有 |
| Job 生命周期与 Agent scope 绑定 | Effect 附加与清理 | 有 |
| 任务只存在于进程本地 | — | 自陈无 |
| 静默无效的取消可能使销毁停滞 | — | 自陈无 |

### `jobs/tool-jobs`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Job 工具（`job_output`, `job_list`, `job_kill`） | 三个工具集 | 有 |
| 多 preset 隔离与每 agent 一条通知 | 作用域监听器路由 | 有 |
| 后台任务指引系统提示词 | 固定指导文本 | 有 |
| 落在 driver 退休窗口的结算搁浅 | — | 自陈无 |
| 已花预算不会随时间恢复 | — | 自陈无 |
| 待领通知无法在 owner 释放后存活 | — | 自陈无 |
| 流读取只有单一消费方 | — | 自陈无 |

### `llm/llm`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 注册 LLM 提供方适配器 | ctx.llm.registerAdapter() | 有 |
| 按注册顺序列举已注册的提供方 | ctx.llm.listProviders() | 有 |
| 声明可通过配置激活的可选提供方路由 | ctx.llm.registerConfigurableProviders() | 有 |
| 列举可配置的提供方目录 | ctx.llm.listConfigurableProviders() | 有 |
| 为 settings namespace 注册模型发现端点 | ctx.llm.registerModelDiscovery() | 有 |
| 列举可以询问端点的 namespace | ctx.llm.listModelDiscoveryNamespaces() | 有 |
| 询问某提供方的公布模型列表 | ctx.llm.discoverModels() | 有 |
| 获取已注册提供方的重试策略 | ctx.llm.providerRetryPolicy() | 有 |
| 列举某已注册提供方当前公布的模型 | ctx.llm.listModels() | 有 |
| 解析并校验确切模型的身份、容量、输出默认值和推理元数据 | ctx.llm.resolveModelInfo() | 有 |
| 校验推理强度并填入适配器配置默认值 | ctx.llm.resolveCallConfig() | 有 |
| 在一次精确模型查询中完整解析配置和元数据 | ctx.llm.prepareCall() | 有 |
| 将一次模型调用流式输出为原始分片 | ctx.llm.stream() | 有 |
| 拦截、包装或缓存每次流式模型调用 | llm/stream waterfall 事件 | 有 |
| 支持 text 内容块 | ContentBlockMap | 有 |
| 支持 reasoning 内容块（思维链） | ContentBlockMap | 有 |
| 支持 image 内容块（需附件服务） | ContentBlockMap | 有 |
| 支持 tool-call 内容块 | ContentBlockMap | 有 |
| 支持 tool-result 内容块 | ContentBlockMap | 有 |
| 推理强度采样参数（off/low/high/max） | GenerateOptions.reasoningEffort | 有 |
| 温度采样参数 | GenerateOptions.temperature | 有 |
| 最大 token 输出参数 | GenerateOptions.maxTokens | 有 |
| stop sequence 参数 | GenerateOptions.stop | 有 |
| 历史消息推理内容回传机制 | 消息 MessageSource | 有 |
| 应用身份 HTTP 标头发送 | attributionHeaders() | 有 |
| API 密钥格式校验（非空可打印 ASCII） | normalizeApiKey() | 有 |
| 错误分类体系（HarnessError 基类） | LlmError 继承 | 有 |
| 上下文溢出错误归一化 | CONTEXT_WINDOW_EXCEEDED_CODE | 有 |
| 配额耗尽错误归一化 | QUOTA_EXCEEDED_CODE | 有 |
| 空响应错误归一化 | EMPTY_RESPONSE_CODE | 有 |
| 无效凭据错误归一化 | INVALID_CREDENTIAL_CODE | 有 |
| 不执行重试、缓存或速率限制 | — | 自陈无 |
| GenerateOptions 采样不包含 tool_choice、top_p 或 penalty | — | 自陈无 |
| BlockAssembler 不处理插件添加的非核心块类型 | — | 自陈无 |

### `llm/llm-deepseek`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 直接 fetch + SSE 分帧 DeepSeek chat-completions 请求 | DeepSeekAdapter | 有 |
| 注册 deepseek-official 提供方路由 | ctx.llm.registerAdapter() | 有 |
| 解析 retryPolicy 配置为 normal 或 always 模式 | Config.retryPolicy | 有 |
| 列举或配置 DeepSeek 模型目录 | Config.models | 有 |
| 支持推理强度 off/low/high/max | thinking/reasoningEffort | 有 |
| 禁用思考模式配置 | thinking: disabled | 有 |
| 支持图片输入并设置 inputModalities 和像素预算 | imagePixelBudget, imageMaxBytes | 有 |
| 自动选择 PNG/WebP/JPEG 编码格式 | 请求编码策略 | 有 |
| 通过 Files API 上传图片 | POST /files | 有 |
| Files API 上传失败时回退到 base64 内联 | maxInlineRequestImageBytes | 有 |
| 图片超过限制时自动删除最旧前缀 | imageOffloadByteQuantum, imageOffloadCountQuantum | 有 |
| 记录上传 ID 和过期时间 | DSH_HOME 下持久化 | 有 |
| 设置每张图片的 Files 解析超时 | filesApiTimeoutMs | 有 |
| 配置模型的上下文窗口大小 | contextWindow | 有 |
| 配置模型的最大 token 输出 | maxTokens | 有 |
| 支持动态配置（settings + credentials） | ctx.settings, ctx.credentials seam | 有 |
| 应用归因标头并记录用户 id 和会话 id | User-Agent + x-deepseek-harness-* | 有 |
| 流空闲超时配置 | streamIdleTimeoutMs | 有 |
| Settings 的 models 列表整体替换组合列表 | — | 自陈无 |
| 未映射 tool_choice | — | 自陈无 |
| 不支持直接外部 URL 和 assistant 图片输出 | — | 自陈无 |

### `llm/llm-pi-ai`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 通用多提供方适配器基于 pi-ai 库 | PiAiAdapter | 有 |
| 注册可配置的提供方路由字典 | Config.providers | 有 |
| 按提供方覆盖或收窄 catalog 模型 | Config.models | 有 |
| 按模型覆盖单个 catalog 条目 | Config.modelOverrides | 有 |
| 声明模型可选的推理档位和转换关系 | reasoningEfforts | 有 |
| 协议兼容性开关（thinkingFormat、supportsDeveloperRole 等） | Config.compat | 有 |
| 支持 OpenAI、Anthropic、Bedrock、Azure 等协议 | supportedProtocols() | 有 |
| 动态 settings 配置和每操作凭据解析 | ctx.settings, ctx.credentials | 有 |
| 模型发现端点询问（仅 OpenAI 兼容） | ctx.llm.registerModelDiscovery() | 有 |
| 回放状态恢复原生 API 保真度 | ReplayEnvelope | 有 |
| 禁用 SDK 重试且单次尝试 | maxRetries 强制为零 | 有 |
| 工具参数从已解析对象重新字符串化 | 词汇差异 | 有 |
| 不支持 stop sequence | — | 自陈无 |
| 未映射 max_retries 和 maxRetryDelayMs | — | 自陈无 |
| 分层合并对字典键无删除语义 | — | 自陈无 |

### `llm/llm-retry`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 在 agent 失败步骤应用确切提供方重试策略 | agent/request-error waterfall | 有 |
| 支持 normal mode（有次数限制） | mode: normal | 有 |
| 支持 always mode（无次数限制） | mode: always | 有 |
| 有界指数退避与对称 jitter | backoff 配置 | 有 |
| 记录重试事件和 retryId | llm/retry 事件 | 有 |
| 直接调用 ctx.llm.stream() 仍只尝试一次 | — | 自陈无 |
| always mode 会重试永久性失败（身份验证、配额等） | — | 自陈无 |

### `llm/token-meter`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 具备回放感知的 token 测量 | ctx.tokenMeter | 有 |
| 从持久日志推进隔离 fold | measure() 方法 | 有 |
| 固定启发式规则估算（4 字符 = 1 token） | estimateMessage() | 有 |
| 提供方 token 用量复用和缓存读写分离 | sourceEventSeqs 序列 | 有 |
| session projections 投影三个单元 | tokenUsage、contextPressure、contextBreakdown | 有 |
| 固定启发式规则是近似值 | — | 自陈无 |

### `mcp/mcp-client`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| MCP 客户端桥接（工具注册到 ctx.tools） | 服务器限定名称 mcp__<serverName>__<rawName> | 有 |
| 支持 stdio 和 streamable-http 两种传输 | transport 配置 | 有 |
| HMR 热替换支持 | 编辑配置后自动重连 | 有 |
| 工具名称规范化与碰撞避免（hash 后缀） | 确定性 12 位十六进制 hash | 有 |
| 工具列表变更监听和重新同步 | notifications/tools/list_changed | 有 |
| 支持超时和中止的工具执行 | toolCallTimeoutMs, exec.signal | 有 |
| 成功值验证与结构化内容支持 | outputSchema 验证 | 有 |
| 图片块在确切能力得到证明后持久化 | ctx.attachments 校验 | 有 |
| 指数退避重连（初始延迟、上限、尝试次数） | reconnect 配置 | 有 |
| 中断预算耗尽后停止重连 | maxAttempts 控制 | 有 |
| 只桥接 MCP 的工具能力 | — | 自陈无 |
| 启动超时继承自 MCP SDK（60 秒） | — | 自陈无 |
| 重连在传输关闭时触发 | — | 自陈无 |
| 图片是唯一的持久丰富结果桥接 | — | 自陈无 |

### `plan/plan-mode`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Plan 协作状态与命令（`ctx.planMode`） | `set()`, `get()` 方法 | 有 |
| Plan 模式开启与退出流程 | `/plan [message]`, `/plan off` 命令 | 有 |
| 用户评审权限流程（exit_plan_mode） | `ctx.userQuestions` 集成 | 有 |
| 会话投影支持（plan 单元） | 投影注册与生命周期 | 有 |
| Plan 只进行引导，不强制执行 | — | 自陈无 |
| 进程退出前作出的选择会丢失 | — | 自陈无 |
| Fork 继承已记录状态，spawn 从未激活开始 | — | 自陈无 |
| 子级无法打开 exit_plan_mode 审阅 | — | 自陈无 |

### `preset/agent-presets`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 按 preset 组装 agent（ctx.agentPresets） | AgentPresets 服务 | 有 |
| 发现和列举 preset（无缓存） | list(), resolve() | 有 |
| 挂载 preset 到 agent（常驻组装） | mount() 方法 | 有 |
| 子 agent 加入父 agent 的常驻组装 | composeFrom() 方法 | 有 |
| 重链 agent 到另一个 preset | recompose() 仅空白 agent | 有 |
| 获取活着 agent 的 preset | composedPreset() 方法 | 有 |
| 冷读（无 agent）的常驻 scope key | standingKeyFor() | 有 |
| 创作 preset（整目录复制） | copy() 方法 | 有 |
| 删除本地创作的 preset | remove() 方法 | 有 |
| 按 id 重新挂载时拒绝已删除 | — | 自陈无 |
| 代际只以组装文件 stamp 为键 | — | 自陈无 |
| 被替代的代际永不回收 | — | 自陈无 |

### `preset/persona`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| agent 个性化人设行（仅限 scope 内） | text 配置 | 有 |
| 人设文本作为 deployment:persona 段落渲染 | 提示词变量解析 | 有 |
| 完整模式将人设恢复为唯一系统提示词 | complete: true | 有 |
| 可选禁用 runtime-context 快照 | includeRuntimeContext | 有 |
| 不支持全局挂载 | — | 自陈无 |

### `schedule/schedule`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 持久提醒服务（会话范围工具） | `schedule_create`, `schedule_list`, `schedule_delete` | 有 |
| 三种提醒类型（after、at、every） | `afterSeconds`, 绝对时间、固定间隔 | 有 |
| 持久状态与折叠（schedule/change 事件） | 版本 1 事件类型 | 有 |
| 绝对时间的日历规范化（时区支持） | IANA 时区与 DST 处理 | 有 |
| 交付生命周期与维护任务 | Overdue 批处理与 follow-up | 有 |
| 仅限会话本地交付 | — | 自陈无 |
| 活动驱动的重试 | — | 自陈无 |
| 显式本地时区 | — | 自陈无 |
| 固定间隔，而非日历规则 | — | 自陈无 |
| 只追赶最新一次 | — | 自陈无 |
| 存在狭窄的崩溃重复窗口 | — | 自陈无 |

### `settings/settings`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 用户设置Service：ctx.settings按namespace分节，支持schema默认值/base/user层解析 | ctx.settings / register / describe / get / update等 | 有 |
| 分层解析：schema默认值→组合base→用户文档 | 三层解析 | 有 |
| 脱敏与Secrets：describe支持redactSecrets脱敏，fields在user层标记被覆盖 | redactSecrets / role(secret) | 有 |
| 写入与冲突检查：update深合并/replace整体替换/mutate按路径编辑，支持expectedRevision | update / replace / mutate / revision | 有 |
| 观察者与事件：watch支持异步回调与隔离，settings/updated和settings/document-updated事件 | watch / settings/updated / settings/document-updated | 有 |
| 已知限制：单一用户层 | 无per-layer记录 | 自陈无 |
| 已知限制：redactSecrets并非可被证明的协议边界 | union等抵达的secret无法遍历 | 自陈无 |
| 已知限制：跨进程并发由提供方定义 | seam仅在进程内按namespace串行化 | 自陈无 |

### `settings/settings-file`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 基于文件提供方：YAML/JSON文档承载全部namespace分节 | path / dshHome / watch / debounceMs | 有 |
| 热重载：watcher监听文档并热发布外部编辑 | watch配置 | 有 |
| 读-改-写原子性：persist先重读文档再原子写回，保留YAML注释 | 原子性 | 有 |
| 跨进程写锁：使用.lock文件保护读-渲染-rename流程 | 跨进程同步 | 有 |
| 已知限制：同namespace冲突仍是后写胜出 | 无深度合并 | 自陈无 |
| 已知限制：漏掉的watcher事件在下一个信号前不可见 | 无stat重读 | 自陈无 |
| 已知限制：注释保留仅限YAML且仅限map形状 | JSON重新序列化 | 自陈无 |

### `skill/skill`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 纯 agent skill 提供方注册表（ctx.skills） | SkillRegistry | 有 |
| 按 scope 的分层结构（宿主+按 scope） | 调用方上下文所在层 | 有 |
| 注册 skill 提供方 | ctx.skills.registerProvider() | 有 |
| 获取 skill 快照和摘要列表 | ctx.skills.snapshot(), list() | 有 |
| 获取和加载单个 skill 定义 | ctx.skills.get() | 有 |
| 嵌入式运行时 skill 注册 | ctx.skills.register() | 有 |
| 调用策略（modelInvocable / userInvocable） | SkillSummary.invocation | 有 |
| 渲染为规范 skill_content 块 | renderSkillContent() | 有 |
| 失效通知事件 | skills/change 无载荷失效通知 | 有 |
| 失效由提供方驱动 | — | 自陈无 |
| 提供方依次查询（无超时中断） | — | 自陈无 |
| 重名项裁决采用先到先得 | — | 自陈无 |

### `skill/skill-badge`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 内置 dsh-badge skill 提供方 | 官方「powered by dsh」Markdown | 有 |
| 随包分发的 PNG 资源（726×120） | assets/dsh-badge.png | 有 |

### `skill/skill-filesystem`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 本地文件系统 skill 提供方 | SKILL.md 或 Markdown 文件 | 有 |
| 扫描项目、自定义和用户 skill 根目录 | includeDefaultRoots, customSkillDirs | 有 |
| 按 rank 优先级解析五个默认根 | project-dsh/agents, custom, user-dsh/agents | 有 |
| frontmatter 解析必填 name/description 和可选元数据 | name, description, whenToUse, metadata | 有 |
| 调用策略字段（disable-model-invocation, user-invocable） | 布尔值配置 | 有 |
| 目录变更检测（Chokidar 监视） | watch, watchUsePolling 等配置 | 有 |
| 缺失根观察和逐级推进 | fs.watchFile 探测机制 | 有 |
| 只识别一层深（//SKILL.md 或 .md） | — | 自陈无 |
| 项目范围为最近 .git 祖先 | — | 自陈无 |
| 格式错误的条目随警告消失 | — | 自陈无 |

### `skill/tool-skill`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 面向模型的 skill 目录和 skill 工具 | skill 工具参数 name | 有 |
| 持久初始目录和替换目录生命周期 | agent/pre-step 监听 | 有 |
| 目录条目规范化和长度上限 | catalogDescriptionMaxLength | 有 |
| 资源指引支持（目录路径、URL、不透明描述） | resourceBase 解析 | 有 |
| 用户显式调用注入（/name token） | 手势边界内联注入 | 有 |
| 目录省略 whenToUse、来源和提供方元数据 | — | 自陈无 |
| 已加载指令正文没有大小上限 | — | 自陈无 |
| 资源是指引而非附件 | — | 自陈无 |

### `spill/spill`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Spill 存储服务定义（`ctx.spillStore`） | `saveText()` 方法 | 有 |
| 过大工具结果的持久化与定位 | `SpillRef`, `SpillLocator`, `retrievalHint` | 有 |
| 该 seam 没有取回或删除 API | — | 自陈无 |
| 存储不等于访问控制 | — | 自陈无 |

### `spill/spill-local`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 本地文件系统 Spill 实现 | 会话级私有目录与文件 | 有 |
| Spill 文件的安全布局与符号链接防护 | 随机前缀与排他写入 | 有 |
| 本地 spill 文件持续存在 | — | 自陈无 |
| 定位信息需与消费方同一文件系统 | — | 自陈无 |

### `spill/spill-policy`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 工具结果 spill 策略（工具后处理） | `tools/post-execute` 监听器与结果替换 | 有 |
| 过大纯文本结果的预览与替换 | `maxInlineBytes` 配置，首尾预览 | 有 |
| 尽力而为的 spill 失败处理 | 存储失败不改变工具结果 | 有 |
| 只能对最终纯文本结果执行 spill | — | 自陈无 |
| 通知无法容纳时功能禁用 | — | 自陈无 |

### `todo/tool-todo`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Todo 列表工具（`todo_write`） | 整表替换工具 | 有 |
| 三态任务状态（pending、in_progress、completed） | 状态枚举与验证 | 有 |
| 单一/多活跃任务纪律配置 | `allowParallelInProgress` 配置 | 有 |
| 会话投影支持（todos 单元） | 投影与 turn/start 清除 | 有 |
| 仅单一所有者 scope | — | 自陈无 |
| 条目形状最小化（无 id、优先级） | — | 自陈无 |
| 整表替换是唯一操作 | — | 自陈无 |

### `web/tool-web`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 面向模型的 web_search 工具（并发多查询） | ToolRuntime | 有 |
| 面向模型的 web_fetch 工具 | ToolRuntime | 有 |
| 搜索结果数量和查询数量上限配置 | searchMaxResults, searchMaxQueries | 有 |
| 工具调用超时预算配置 | fetchTimeoutMs, searchTimeoutMs | 有 |
| 输出字符数上限配置 | fetchMaxOutputChars | 有 |
| HTML 转 markdown 渲染（turndown + GFM） | 转换策略 | 有 |
| 抓取结果截断提示 | fetchMaxOutputChars 触发 | 有 |
| 搜索多查询去重和按排名合并 | 查询和来源逻辑 | 有 |
| 没有覆盖整个批次的原生搜索计数器 | — | 自陈无 |
| HTML→markdown 转换在深层嵌套上降级 | — | 自陈无 |
| 面向模型的接口有意保持精简 | — | 自陈无 |
| 没有 web 专用权限策略 | — | 自陈无 |

### `web/web`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 服务定义：搜索和抓取 web 能力 seam | ctx.web | 有 |
| 注册搜索提供方 | ctx.web.registerSearchProvider() | 有 |
| 注册抓取提供方 | ctx.web.registerFetchProvider() | 有 |
| 执行搜索并强制执行结果数量限制 | ctx.web.search() | 有 |
| 获取 URL 内容（非 2xx 响应是结果不是错误） | ctx.web.fetch() | 有 |
| 提供方选择策略（配置 id 或自动选择） | searchProvider / fetchProvider 环境变量 | 有 |
| 没有观测接口或能力状态查询 | — | 自陈无 |
| WebSearchRequest 只携带 query + maxResults | — | 自陈无 |
| WebFetchBody 没有 PDF 分支 | — | 自陈无 |

### `web/web-fetch-http`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 匿名公共 HTTP(S) 抓取提供方 | WebFetchProvider | 有 |
| URL 验证（拒绝凭据、过长/格式错误） | URL 安全检查 | 有 |
| 强制执行响应字节上限和解码字符上限 | maxResponseBytes, maxBodyChars | 有 |
| 同源重定向限制（跨源拒绝） | maxRedirects | 有 |
| 发送产品 User-Agent（不伪装浏览器） | userAgent 配置 | 有 |
| 拒绝不支持的内容类型（二进制） | 内容类型检查 | 有 |
| SSRF/私有网络防护暂缓 | — | 自陈无 |
| 只解码文本内容（不支持可提取文本的 PDF） | — | 自陈无 |

### `web/web-search-deepseek`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| DeepSeek Anthropic 兼容 Messages API 搜索 | WebSearchProvider | 有 |
| 启用原生 web_search_20250305 服务器工具 | 服务器侧搜索 | 有 |
| 限制搜索次数和结果去重 | maxUses, maxResults | 有 |
| 从 web_search_tool_result 块解析结构化结果 | 搜索结果映射 | 有 |
| 记录辅助搜索请求到会话日志 | web/deepseek-search-llm-request 事件 | 有 |
| 一次搜索需要完整 Messages 模型轮次 | — | 自陈无 |
| 动态凭据可用性在操作内部解析 | — | 自陈无 |

### `web/web-search-exa`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Exa 搜索端点提供方 | WebSearchProvider | 有 |
| 检索模式配置（auto/keyword/neural） | searchType | 有 |
| 高亮摘要内容映射为 snippet | highlightsPerResult | 有 |
| 没有高亮摘要的结果被整个丢弃 | — | 自陈无 |
| 只公开 searchType/numResults/highlightsPerResult | — | 自陈无 |

### `web/web-search-perplexity`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Perplexity OpenAI 兼容 chat/completions 搜索 | WebSearchProvider | 有 |
| 生成答案与结构化/回退引用映射 | search_results[] 或 citations[] | 有 |
| 新近程度窗口过滤配置 | searchRecency | 有 |
| 引用回退源只含 URL | — | 自陈无 |
| 超量返回的来源仍消耗 token 和延迟 | — | 自陈无 |

### `workspace/workspace`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Workspace实体注册表：ctx.workspaceRegistry按namespace分节存储持久workspace记录 | ctx.workspaceRegistry | 有 |
| Workspace生命周期：create/get/list/resolveByPath/insertBefore/delete/archiveSession等操作 | 操作API | 有 |
| 会话记账：Workspace.attachSession/detachSession/insertSessionBefore/sessionIds管理会话归属 | 会话操作 | 有 |
| 归档管理：archiveSession/archivedSessionIds处理全局归档集合 | workspace.archiveSession | 有 |
| 已知限制：会话删除与破坏性文件夹移除是独立功能 | 删除Workspace注册记录不影响日志 | 自陈无 |
| 已知限制：头部索引会在启动时刷新 | 另一进程的删除会在下次刷新后发现 | 自陈无 |

## C. 多 agent 编排

17 个包，100 条，其中有 50、自陈无 50。

### `experimental/agent-team`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Agent Teams 服务：多成员协作（`ctx.agentTeams`） | `spawnTeammate()`, `sendMessage()`, `interrupt()`, `listMembers()`, `waitForChange()` | 有 |
| 持久 Team roster 与成员生命周期 | Provisioning/active/failed/inactive 状态 | 有 |
| 持久 peer mailbox | Quiet 与 wakeup 投递模式 | 有 |
| 共享任务板（创建、读取、claim、编辑、完成、删除） | 版本化任务快照与 CAS update | 有 |
| 持久 wait 机制 | `waitForChange()` 等待 roster/task/mailbox 变化 | 有 |
| 单进程、共享 checkout | — | 自陈无 |
| Write scope 仅作提示 | — | 自陈无 |
| 扁平且不可变的 roster | — | 自陈无 |
| 不会自动释放 owner | — | 自陈无 |
| Mailbox 不保证跨进程 exactly-once | — | 自陈无 |

### `experimental/tool-agent-team`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Agent Teams 工具适配器 | `spawn_teammate`, `send_message`, `interrupt_agent`, `wait_agent`, `team_task_*` 工具集 | 有 |
| 每个 Agent 作用域独立的 Team 注册 | 作用域工具安装与权限 | 有 |
| 提示词策略说明协调与共享 cwd | `tool:team-strategy` 系统提示词 | 有 |
| 提示词策略只负责协调，不负责 confinement | — | 自陈无 |
| 不会自主创建 Team | — | 自陈无 |
| 没有 Web 控制功能 | — | 自陈无 |

### `subagent/subagent`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 子 agent 生命周期管理服务 `ctx.subagents` 注册表 | `registerProvider()`, `start()`, `startContinuable()`, `followup()`, `interrupt()`, `reportFrom()`, `registerContinuableSetup()`, `drainContinuableDescendants()`, `drainContinuableChildren()`, `listChildren()`, `listDescendants()` | 有 |
| 一次性子 agent 启动与支持的能力声明（outputSchema、depthLimit、toolFilter、persona） | `provider.capabilities` | 有 |
| 子 agent 委派时的沙箱与审批策略注入 | `captureDelegatedPolicyOverrides()`, `appendDelegatedPolicyOverrides()` | 有 |
| 可继续子 agent 与 Activation 的驻留管理 | Agent Activation 状态 (running/waiting/settled) 与 inbox | 有 |
| 子 agent 会话的生命周期事件 (subagent/start, subagent/end) | 提供方事件监听 | 有 |
| 结算通知：子 agent 完成时向父级投递消息 | `subagent/settled` 通知 | 有 |
| 一次性所有权与资源生命周期 (dispose) | `SubagentRun.dispose()` | 有 |
| 子 agent 会话持久化描述符 | `snapshotSubagentDescriptor()`, `foldSubagentDescriptor()` | 有 |
| 委派深度限制与检查 | `assertSubagentMaxDepth()`, `delegationDepthOf()` | 有 |
| 进程内子 agent 的 sessionProjections 投影（subagentTiming、subagent） | 投影单元注册 | 有 |
| 冷恢复一个可继续子 agent 时的会话复用 | `ctx.agents.resume()` | 有 |
| ACP 子 agent 仍为一次性，且无法通过追踪枚举 | — | 自陈无 |
| 无 host-user 继续执行（只有 interrupt 接受用户地址） | — | 自陈无 |
| 继续执行消息绝不 steering | — | 自陈无 |
| 取消收敛期间的唤醒缺口 | — | 自陈无 |
| 驻留仅限进程内 | — | 自陈无 |
| 不回放已接受但未记录的消息 | — | 自陈无 |
| 没有持久化的上报 mailbox | — | 自陈无 |
| 生命周期事件只供观察，不影响运行 | — | 自陈无 |

### `subagent/subagent-acp`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 进程外子 agent：ACP 协议驱动的全新子进程 | `start()` 遍历 spawn→ACP initialize→newSession | 有 |
| 工作目录解析与环境隔离 | 从父会话继承 cwd，清除凭据 | 有 |
| 每次运行使用全新进程 | — | 自陈无 |
| 仅支持本地工作区 | — | 自陈无 |
| 不支持可选启动时能力（outputSchema、depthLimit、toolFilter、persona） | — | 自陈无 |

### `subagent/subagent-claude-code`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 进程外子 agent：Claude Code SDK 驱动 | `start()` 调用官方 SDK query() | 有 |
| 不支持可选启动时能力 | — | 自陈无 |
| 每次运行均新建一个 query 和一个进程 | — | 自陈无 |
| 委派时必须存在 SDK 平台载荷 | — | 自陈无 |

### `subagent/subagent-codex`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 进程外子 agent：Codex 0.147.0 驱动单次轮次 | `start()` spawn→Codex initialize→thread/start | 有 |
| 不支持可选启动时能力 | — | 自陈无 |
| 每次运行均新建进程、线程和轮次 | — | 自陈无 |
| 每次运行必须存在原生平台载荷 | — | 自陈无 |

### `subagent/subagent-dsh-sdk`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 进程外子 agent：完整 DeepSeek Harness 运行时驱动 | `start()` spawn→initialize 握手→SDK 活动 | 有 |
| 子进程 harness 可配置的提供方/模型/token上限 | `provider`, `model`, `maxTokens` 配置 | 有 |
| 不支持可选启动时能力 | — | 自陈无 |
| 每次运行使用全新的运行时进程 | — | 自陈无 |
| 仅支持本地子进程 | — | 自陈无 |

### `subagent/subagent-fork-in-process`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 进程内子 agent：继承父级已完成轮次的对话前缀 | `startInProcessRun()` 传入已平衡轮次 | 有 |
| 支持结构化输出、深度限制、工具过滤、persona 能力 | `{ outputSchema: true, depthLimit: true, toolFilter: true, persona: true }` | 有 |
| 没有任何随附组合会创建可继续的 fork 子 agent | — | 自陈无 |

### `subagent/subagent-in-process-driver`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 共享的进程内子 agent 运行驱动（spawn 与 fork 使用） | `startInProcessRun(request, options)` | 有 |
| 深度检查与推导子 agent 深度 | `delegationDepthOf()` 读取父深度 | 有 |
| 取消与所有权管理 | 必需信号覆盖启动与实时运行 | 有 |
| 结构化输出运行时安装 | `attachStructuredRuntime()` 注册工具和提示词 | 有 |
| 运行不公开 sendMessage/resume | — | 自陈无 |
| 结构化捕获只接受 defineTool schema 子集 | — | 自陈无 |

### `subagent/subagent-spawn-in-process`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 进程内子 agent：全新空白状态创建 | `start()` 委托 startInProcessRun，不传 seed | 有 |
| 支持结构化输出、深度限制、工具过滤、persona 能力 | `{ outputSchema: true, depthLimit: true, toolFilter: true, persona: true }` | 有 |
| 全新表示不含父 agent 对话 | — | 自陈无 |

### `subagent/tool-subagent`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 面向模型的子 agent 委派工具 | `subagent` 工具基于 ctx.subagents 提供方 | 有 |
| 独立的 persona、工具过滤、深度限制配置 | `persona`, `toolFilter`, `maxDepth` 配置项 | 有 |
| 工具结果后台任务通知 | Task 完成通知或 continuable 子 agent 结算通知 | 有 |
| 后台运行不通过本工具公开结果 | — | 自陈无 |
| 等待中的一次性实例较晚才发现重复名称 | — | 自陈无 |

### `subagent/tool-subagent-control`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 全局 send_message 工具：向可继续子 agent 投递消息 | `send_message(subagent_id, message)` | 有 |
| 全局 interrupt_agent 工具：停止可继续子 agent 的当前轮次 | `interrupt_agent(agent_id)` | 有 |
| 已排队消息没有独立结果 | — | 自陈无 |
| 不对当前轮次进行 steering | — | 自陈无 |
| 列表是快照，不是投递承诺 | — | 自陈无 |
| 没有分页或删除 | — | 自陈无 |

### `subagent/tool-subagent-report`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 可继续子 agent 作用域 report 工具：向启动者上报内容 | `report(output: string)` 子级作用域工具 | 有 |
| 父级可能在 dispose 后继续接受报告 | — | 自陈无 |
| 接受弱于持久投递（无 exactly-once） | — | 自陈无 |
| 嵌套上报只向直接父级到达 | — | 自陈无 |
| 没有速率限制 | — | 自陈无 |

### `workflow/tool-ralph`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Ralph 工具：固定脚本运行多轮全新子 agent，共享工作区 | `ralph(objective, maxRounds)` 工具 | 有 |
| 每轮向子 agent 投递不可变目标与结构化交接内容 | Ralph Round 固定提示词与结构化输出 | 有 |
| 完成由 worker 自行声明 | — | 自陈无 |
| 仅支持前台 | — | 自陈无 |

### `workflow/tool-workflow`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 工作流工具：执行 JavaScript 编排脚本，扇出 subagent | `workflow(meta, script, args)` 工具 | 有 |
| 脚本钩子（agent、parallel、pipeline、phase、log） | JavaScript 钩子 API | 有 |
| 工作流投影到会话（run-start/run-end 事件） | 仅顶层，不包括嵌套 dispatch | 有 |
| 父级轮次阻塞到整个工作流结算 | — | 自陈无 |
| 每次工具注册的策略固定 | — | 自陈无 |

### `workflow/workflow`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 工作流 seam：服务定义（`ctx.workflowEngine`） | `WorkflowEngine.start(request)` | 有 |
| 工作流运行与取消 | `WorkflowRun.cancel()`, `dispose()` | 有 |
| 工作流事件（workflow/start、workflow/end、workflow/phase、workflow/log、workflow/agent-start、workflow/agent-end） | 生命周期观察器 | 有 |
| 仅支持前台收集 | — | 自陈无 |
| 没有日志化或恢复 | — | 自陈无 |

### `workflow/workflow-worker-thread`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Worker thread 引擎实现（`WorkflowEngine` 提供方） | Worker 与主机通信协议 | 有 |
| Worker thread 脚本执行隔离 | 同步超时、取消、值边界校验 | 有 |
| 子 agent 启动与结果投影 | Worker RPC 命令/应答 | 有 |
| Worker/vm 不是安全边界 | — | 自陈无 |
| 每次运行都要支付 worker thread 成本 | — | 自陈无 |

## D. 底座与工具库

13 个包，85 条，其中有 60、自陈无 25。

### `identity/anonymous-user-id`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 匿名身份生成：getOrCreateAnonymousUserId()返回限定于harness home的随机UUID v4 | getOrCreateAnonymousUserId | 有 |
| 持久化存储：.anonymous-user-id文件存储身份 | $DSH_HOME/.anonymous-user-id | 有 |
| 遥测与反馈使用：OpenTelemetry/直接反馈/DeepSeek提供方请求共用该身份 | user.id / x-deepseek-harness-user-id | 有 |
| 已知限制：删除后无法恢复 | 新匿名身份按设计生成 | 自陈无 |
| 已知限制：Best-effort并发 | 并发进程可能暂时使用不同UUID | 自陈无 |
| 已知限制：没有跨home身份 | 不同DSH_HOME不关联 | 自陈无 |

### `runtime-diagnostics/invariants`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| InvariantRegistry 服务 ctx.invariants 注册包自有运行时不变量检查 | InvariantRegistry 服务 | 有 |
| ctx.invariants.register() 为完整 npm 包名保留活动注册并返回 disposer | register 方法 | 有 |
| 配置通过 package_allowlist 和 package_blocklist 正则表达式过滤包 | 包过滤配置 | 有 |
| 已启用贡献在专用子 Cordis fiber 中运行 | fiber 执行机制 | 有 |
| InvariantError 扩展 Error，携带稳定 code 和 packageName | InvariantError 异常 | 有 |
| 可执行配套入口保护会话、agent、scope、loop、llm、tools、compaction、hook、fs 和 workflow 等包之间的关系 | 配套入口检查 | 有 |
| 请求重建覆盖 loop 在冻结前显式标记的请求，直接 LLM 调用不在约定内 | — | 自陈无 |
| 仅实时生命周期配套入口无法重建自身重新加载前开始的操作 | — | 自陈无 |

### `typert/generator`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 将 TypeScript 项目分析为编译器独立的 FaceModel 和 TypeGraph | WorkspaceAnalyzer | 有 |
| 生成包含支持的 Zod schema 和 TYPERT contribution 的可执行 JavaScript | FaceModelEmitter | 有 |
| 生成声明文件并通过包公开导出将 schema 标注为 z.ZodType | FaceModelEmitter | 有 |
| 遍历 Cordis Context/Events 扩充声明和显式 @typert 声明的包公开导出 | WorkspaceTypertGenerator | 有 |
| 跳过包导出中的模式匹配 | — | 自陈无 |
| 跨 face 链接会在模型中表示，但生成的 schema 均不需跨 face 的运行时 Zod 导入 | — | 自陈无 |
| 泛型 schema 声明和以条件类型或映射类型为根的计算构造会失败 | — | 自陈无 |

### `typert/loader`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 扫描现有 Loader 配置项并监听 Cordis plugin 生命周期通知 | 插件激活时的监听机制 | 有 |
| 解析每个配置项所属包的 package.json 和 ./typert 子路径 | 包产物发现 | 有 |
| 校验 TYPERT manifest 并注册贡献项 | manifest 校验与注册 | 有 |
| 通过 packages 配置为嵌套插件额外注册产物 | packages 配置项 | 有 |
| 未导出 ./typert 子路径的包会被跳过 | — | 自陈无 |
| 发现机制只会导入宿主侧产物，客户端侧需要独立的发现机制 | — | 自陈无 |

### `typert/protocol`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| @Remote 装饰器将公开实例方法标记为可在其注册的 Cordis 服务上直接调用 | @Remote 装饰器 | 有 |
| @RemoteScope(key) 标记接收者选自合并声明的作用域 Context 类型的方法 | @RemoteScope 装饰器 | 有 |
| TypertRemoteService 将 Cordis 键绑定到同一默认协议命名空间 | TypertRemoteService 基类 | 有 |
| bindTypertRemote 为无法继承 TypertRemoteService 的服务提供绑定 | bindTypertRemote 函数 | 有 |
| remoteMethods 返回与内部状态分离的快照供 Gateway SRC 回退路径使用 | remoteMethods 函数 | 有 |
| 通过将 signal: AbortSignal 声明为最后一个参数启用协作式取消 | 协作式取消机制 | 有 |
| 装饰器标记仅包含方法名和直接调用或 Context 调用模式 | — | 自陈无 |
| Remote 装饰器只接受具有字符串名称的公开非静态实例方法，SRC 执行无法表示重载签名 | — | 自陈无 |

### `typert/registry`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 注册项包含包在某个 face 上的业务反射信息和可选的运行时 Zod schema | 注册项存储 | 有 |
| ctx.typert.lookups.register() 注册业务包拥有的协议声明和默认解析器 | lookups.register API | 有 |
| ctx.typert.lookups.configure() 注册由宿主组合拥有的可异步执行的解析器 | lookups.configure API | 有 |
| ctx.typert.contexts.registerHost() 和 configureHost() 处理具作用域的上下文身份 | contexts 注册 API | 有 |
| ctx.typert.contexts.registerClient() 提供客户端上下文绑定器 | contexts.registerClient API | 有 |
| register(contribution) 拒绝格式错误的标识和重复的包 face 组合键或 schema 键 | register 验证 | 有 |
| get(key)、resolve(key) 和 list(filter?) 查询当前有效的 schema | schema 查询 API | 有 |
| getPackage(packageName, face?) 和 listPackages(filter?) 查询生成的服务事件和对象反射信息 | 包级查询 API | 有 |
| toJSONSchema(key, params?) 使用 z.toJSONSchema() 投影当前有效的 schema | JSON Schema 投影 | 有 |
| typertKey() 和 typertPackageKey() 构造两种稳定的标识形式 | 标识构造函数 | 有 |
| 注册表不会合并宿主侧与客户端侧的图，也不会解析 TypeScript 引用 | — | 自陈无 |
| 若在同一上下文中注册来自两个 face 的同名 schema，系统会将其作为重复项拒绝 | — | 自陈无 |

### `util/atomic-write`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 原子文件替换提供零依赖实现 | writeFileAtomic 函数 | 有 |
| 通过独占创建临时文件防止符号链接劫持 | writeFileAtomic 机制 | 有 |
| 全新 inode 携带 mode 参数走完 rename 防止权限过宽 | mode 参数处理 | 有 |
| 跨进程串行化同一文件的写入方 | withFileLock 函数 | 有 |
| 原子但不保证持久，崩溃后可能观察到 rename 被回退 | — | 自陈无 |
| 仅支持字符串内容，不提供 Buffer 或流式形态 | — | 自陈无 |
| 遗留锁需要操作者恢复 | — | 自陈无 |

### `util/brand`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 提供 Branded<B> 名义类型原语用于跨包边界 id 的类型安全 | Branded 类型 | 有 |
| 为跨包边界的混淆 id 添加品牌，但无需为每个字符串都添加 | 品牌策略 | 有 |

### `util/home-paths`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 解析 DeepSeek Harness 的单根主目录 | resolveDshHome 函数 | 有 |
| 将子路径段拼接到解析后的主目录下 | dshHomePath 函数 | 有 |
| 以符号方式表示当前根目录用于面向用户的路径，不泄露机器绝对路径 | dshHomeDisplay 函数 | 有 |
| 使用操作系统主目录拼接 .dsh 并返回默认主目录 | defaultDshHome 函数 | 有 |
| 展开 ~ 和 Windows 风格的 ~\ 前缀 | expandHomePath 函数 | 有 |
| 为原生文件系统 watcher 提供稳定的目标路径表示 | canonicalizeWatchPath 函数 | 有 |
| 展开范围刻意保持狭窄，仅 ~ 和 ~\ 使用当前操作系统主目录，指定用户形式保持不变 | — | 自陈无 |

### `util/launch-environment`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 把本次运行的环境冻结为不可变快照并记住每个值来自哪一层 | launchEnvironmentOf 函数 | 有 |
| 按可信度从高到低搜索所有层（进程、项目 .env、用户 ~/.dsh/.env） | get 方法 | 有 |
| 仅搜索指定层的 getFrom(name, sources) 方法 | getFrom 方法 | 有 |
| 快照不是子进程边界，每一层同样会被物化进 process.env | — | 自陈无 |
| 没有按工作区划分的层，项目层是调用目录在启动时固定 | — | 自陈无 |

### `util/native-command`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 零依赖免 shell execFile 运行器直接 spawn 可执行文件 | runNativeCommand 函数 | 有 |
| 以 utf8 捕获 stdout/stderr 并把调用方的 abort 传播为子进程终止 | 输出捕获与信号传播 | 有 |
| 在 Windows 上隐藏瞬时控制台窗口 | Windows 控制台隐藏 | 有 |
| 失败时附带退出 code 与两路已捕获输出供调用方分类 | 错误处理与分类 | 有 |
| 不做输出限量，两路流在内存中无界缓冲 | — | 自陈无 |

### `util/output-retention`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| ItemRetainer 限制有序逻辑单元并支持 head 保留 | ItemRetainer 类 | 有 |
| TextRetainer 限制面向字节的文本流并在 finish() 时保留 UTF-8 边界 | TextRetainer 类 | 有 |
| describeOmitted 生成标准化的省略子句 | describeOmitted 函数 | 有 |
| formatRetentionNotice 将标准化的省略子句与工具自有的恢复指引连接起来 | formatRetentionNotice 函数 | 有 |
| 项保留只支持 head，tail 和 head/tail 由工具负责 | — | 自陈无 |
| 文本保留面向字节而非字符，行窗口需要单独的渲染器 | — | 自陈无 |

### `util/timeout`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| clampTimeout 验证调用方的超时提示并限制在范围内 | clampTimeout 函数 | 有 |
| deadline 将 upstream 取消与超时融合为一个 AbortSignal | deadline 函数 | 有 |
| idleWatchdog 保持稳定的融合信号并仅在异步迭代器未完成时启动 timer | idleWatchdog 函数 | 有 |
| timeoutOf 从已中止的信号中恢复 TimeoutReason 进行分类 | timeoutOf 函数 | 有 |
| 只发出通知而无法停止忽略信号的工作 | — | 自陈无 |
| 第一个中止原因决定分类，后续超时无法再报告 | — | 自陈无 |

## E. 目录、命令与沙箱

35 个包，368 条，其中有 217、自陈无 151。

### `code-runtime/code-runtime`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 代码运行时 Service Definition（ctx.codeRuntime） | ctx.codeRuntime | 有 |
| run(request) 执行一段程序，所有失败都通过 resolve 结果中的 error 报告 | run(request) | 有 |
| language：只读描述符（'typescript'、'python'） | language | 有 |
| isolation：只读描述符（'worker-thread'、'process'、'container'） | isolation | 有 |
| 绑定调用会桥接完整的无损 JSON 参数与 resolve 值 | — | 有 |
| 不同运行之间不保留任何状态 | — | 有 |
| run() 是一次性的，logs 只有在 CodeRunResult resolve 后才能获得 | — | 自陈无 |
| 持久 REPL 风格内核已记录为未来工作 | — | 自陈无 |
| 目前只提供 worker 线程后端 | — | 自陈无 |
| 中间绑定值没有字节上限 | — | 自陈无 |

### `code-runtime/code-runtime-python`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| CPython 子进程实现 | — | 有 |
| Wire protocol：host 与 CPython 子进程在 fd 3 上交换 JSON-lines | — | 有 |
| host 把每个入站帧当作敌意输入，validateChildFrame 形状校验 | — | 有 |
| lossless-JSON 穿越完成值与 binding 参数 | — | 有 |
| 共享截断标记确保被截断的日志逐字节一致 | — | 有 |
| 跨语言 guard 覆盖运行时执行面与帧字段形状（mirror 测试） | — | 有 |
| src/index.ts 只导出协议词汇 | — | 自陈无 |

### `code-runtime/code-runtime-worker-thread`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Worker 线程实现（WorkerThreadCodeRuntime） | WorkerThreadCodeRuntime | 有 |
| 每次运行使用一个全新 worker，不设池化 | — | 有 |
| 由宿主侧剥离 TypeScript 类型（stripTypeScriptTypes） | — | 有 |
| 端口把对端视为不可信（伪造通信无法绕过外层上限） | — | 有 |
| 绑定调用被拒绝时使用的异常类属于请求数据 | — | 有 |
| 两个独立预算：computeMs（忙碌时间）与 maxWallMs（墙钟上限） | config: { computeMs, maxWallMs, maxOutputBytes, maxOldGenerationSizeMb } | 有 |
| 中间绑定值是完整 JSON | — | 有 |
| 日志主动流入外层账本，抛出的百万字节 stack 变成 output-limit 诊断 | — | 有 |
| 空环境（env: {}、execArgv: []） | — | 有 |
| dispose 时等待完全停稳 | — | 有 |
| 程序派生的 OS 进程在程序终止后仍会存活 | — | 自陈无 |
| 类型剥离依赖 Node 的实验性 stripTypeScriptTypes API | — | 自陈无 |
| computeMs 到期最多可能超过一个轮询间隔 | — | 自陈无 |
| 程序获得一个含 5 个方法的 console shim | — | 自陈无 |
| 中间绑定值没有字节上限 | — | 自陈无 |
| 默认 64 MiB 是拒绝边界，不是可恢复存储 | — | 自陈无 |

### `e2b/e2b`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| E2B 沙箱的共享生命周期所有者 | ctx.e2b | 有 |
| 构造阶段启动一次沙箱创建，创建 cwd 和私有 cwd/.dsh-e2b | — | 有 |
| 配置：apiKey（可省略，读取 E2B_API_KEY）、cwd（默认 /home/user/workspace）、timeoutMs（默认 5 分钟） | config: { apiKey?, cwd, timeoutMs } | 有 |
| 这不是完整的 harness 运行时 | — | 自陈无 |
| 沙箱状态是短暂的，无持久化、pause/leave 保留、模板、卷和快照 | — | 自陈无 |
| 没有配置部署平台（网络策略、宿主工作区同步、沙箱发现） | — | 自陈无 |
| cwd 是解析约定，而不是包含边界 | — | 自陈无 |

### `e2b/fs-e2b`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| E2B 文件系统实现（ctx.fs 提供方） | — | 有 |
| 远程身份与元数据（使用 GNU realpath -mz、base64、chmod） | — | 有 |
| 执行环境路径（绝对 POSIX 路径、百分号编码的 file: URI） | — | 有 |
| UTF-8 读取（跨分片解码连续性、NUL 样本检测） | — | 有 |
| 有界原始字节读取（readBytes 取消溢出分片） | — | 有 |
| 原子变更（随机同级暂存目录、E2B 原子重命名、ln -T 不替换创建） | — | 有 |
| 失败与取消映射到 FsError 词汇 | — | 有 |
| 不提供宿主同步 | — | 自陈无 |
| 变更协调仅限宿主进程内 | — | 自陈无 |
| 读取会按路径重新打开规范化目标 | — | 自陈无 |
| 仍需承担完整文件变更成本 | — | 自陈无 |
| 该 POC 面向 E2B 默认 Linux 镜像 | — | 自陈无 |

### `e2b/subprocess-e2b`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| E2B 子进程实现（ctx.subprocess 提供方） | — | 有 |
| 异步远程启动，pid 为 -1 直到分配完成 | — | 有 |
| 执行世界坐标（cwd、runtimeRoot、可执行文件查找） | — | 有 |
| Linux 进程组（exec setsid --wait、记录进程组 ID） | — | 有 |
| 环境边界（凭据清除、DSH_* 清理、严格 UTF-8 解码） | — | 有 |
| stdio 投影（分流到 spill、base64 ASCII 帧、增量恢复） | — | 有 |
| 终端会话（E2B PTY API、前台进程组、信号发送、清理） | — | 有 |
| 沙箱消失（SandboxNotFoundError 视为完全停稳） | — | 有 |
| SDK 仍会在宿主内存中保留完整命令输出 | — | 自陈无 |
| 不支持需要同步 PID 的消费方 | — | 自陈无 |
| 私有状态随沙箱生命周期存在 | — | 自陈无 |
| 控制状态与沙箱用户同 UID（后台进程可改写 pid/exit-code） | — | 自陈无 |
| 数值进程身份没有复用围栏 | — | 自陈无 |
| 初始环境探测会继承沙箱默认值 | — | 自陈无 |
| E2B 不公开信号事实 | — | 自陈无 |
| 无法精确检查终端 stdin 等待状态 | — | 自陈无 |
| 依赖 Linux 工具与 E2B 传输语义 | — | 自陈无 |

### `fs/fs`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 解析路径为稳定的目标标识与规范路径 | resolve(path, opts?) | 有 |
| 返回规范化绝对路径供子进程打开 | processPath(target) | 有 |
| 返回规范化 file: URI | fileUrl(target) | 有 |
| 检查目标包含关系 | contains(parent, child) | 有 |
| 获取文件元数据（版本、类型、大小） | stat(target, signal?) | 有 |
| 获取不跟随链接的元数据 | lstat(path, opts?, signal?) | 有 |
| 完整读取文本文件 | readText(target, signal?) | 有 |
| 流式读取大文本文件 | streamText(target, signal?) | 有 |
| 有界读取原始字节 | readBytes(target, signal, maxBytes) | 有 |
| 列出目录内容（单层） | listDir(target, signal?) | 有 |
| 原子创建或替换文件 | writeText(target, content, expected?, signal?) | 有 |
| 字面量编辑文本文件 | editText(target, edit, expected?, signal?) | 有 |
| 文件系统策略事件词汇（fs/write-intent, fs/edit-intent, fs/observed） | fs/* 事件 | 有 |
| 没有删除、重命名、移动、复制或监视操作 | — | 自陈无 |
| 没有 I/O deadline（仅尽力而为的 AbortSignal） | — | 自陈无 |
| 先解析后操作导致远程后端每次工具调用需要两次往返 | — | 自陈无 |

### `fs/fs-local`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 本地文件系统实现 resolve()、processPath()、fileUrl()、contains() | LocalFileSystem | 有 |
| 本地文件系统实现 stat()、lstat() | LocalFileSystem | 有 |
| 本地文件系统实现 readText()、streamText()、readBytes() | LocalFileSystem | 有 |
| 本地文件系统实现 listDir() | LocalFileSystem | 有 |
| 本地文件系统实现 writeText()，支持原子不替换创建 | LocalFileSystem | 有 |
| 本地文件系统实现 editText()，支持字面量编辑与 CRLF/LF 风格保留 | LocalFileSystem | 有 |
| config.cwd 不是沙箱，是解析默认值 | — | 自陈无 |
| 版本 token 依赖文件系统元数据（dev:ino:size:mtimeNs:ctimeNs），可能被绕过 | — | 自陈无 |
| editText 会把整个文件及编辑后的副本保存在内存中 | — | 自陈无 |
| 低于上限的覆写仍会缓冲上下文基础（config.diffBasisMaxBytes） | — | 自陈无 |
| 二进制检测不对称（读取仅前 8192 字节采样，编辑扫描整个 buffer） | — | 自陈无 |
| 每目标变更锁仅限进程内 | — | 自陈无 |
| 带防护的创建要求支持硬链接 | — | 自陈无 |

### `fs/fs-observation-policy`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 记录观测到的存在或缺失状态 | fs/observed 事件监听 | 有 |
| 在 fs/write-intent 上添加编辑前读取和带防护的写入 | fs/write-intent 门禁 | 有 |
| 在 fs/edit-intent 上添加编辑前读取和版本防护 | fs/edit-intent 门禁 | 有 |
| 已观察状态无法在会话恢复后保留 | — | 自陈无 |
| 没有 agent 会话的参与者无法满足策略（编辑会抛出 FS_NOT_OBSERVED） | — | 自陈无 |
| 直接 ctx.fs 读取不会发出 fs/observed | — | 自陈无 |
| 授权依据是版本新鲜度，而非视图完整性 | — | 自陈无 |

### `fs/fs-sandbox`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 强制沙箱的文件系统后端，继承 LocalFileSystem 的所有操作 | SandboxedFileSystem | 有 |
| read-only 模式下拒绝所有变更（FS_SANDBOX_DENIED） | — | 有 |
| workspace-write 模式下只允许目标位于工作区根目录下的变更 | — | 有 |
| danger-full-access 模式下不加围栏直接委托 | — | 有 |
| 策略围栏而非内核边界，存在 TOCTOU 写入前重新规范化的风险 | — | 自陈无 |
| 围栏与 runner 的一致性由单一所有方派生（writableRoots 函数） | — | 自陈无 |
| 要求 ctx.sandboxPolicy 否则不会实施约束 | — | 自陈无 |

### `fs/tool-fs`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| read 工具：带行号的 UTF-8 内容读取与分页 | read(file_path, offset?, limit?) | 有 |
| read_image 工具：通过有界字节读取图像文件，经 ctx.attachments 持久保存 | read_image(file_path) | 有 |
| write 工具：创建文件或完整替换文件 | write(file_path, content) | 有 |
| edit 工具：字面量替换文本 | edit(file_path, old_string, new_string, replace_all?) | 有 |
| 读取窗口逻辑（offset、limit、行号） | readLimit, readMaxLineLength, readMaxBytes | 有 |
| 流式读取大文件（>= readStreamMinSize） | readStreamMinSize | 有 |
| 工具通过 fs/* 事件门禁与政策插件交互 | fs/write-intent, fs/edit-intent, fs/observed | 有 |
| 沙箱模式下公开 sandbox_permissions 与 justification 参数 | — | 有 |
| 并发读取工具调用（只有观察记录是同步操作） | — | 有 |
| 未交付面向模型的目录列表工具 | — | 自陈无 |
| read 只处理 UTF-8 文本文件 | — | 自陈无 |
| 媒体类型按扩展名声明 | — | 自陈无 |
| 工具结果卡片没有内嵌图像预览 | — | 自陈无 |
| 没有附件局部读取工具 | — | 自陈无 |
| 没有超时接口 | — | 自陈无 |

### `fs/tool-fs-search`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| glob 工具：通过 ripgrep 发现文件路径 | glob(pattern, path?) | 有 |
| grep 工具：通过 ripgrep 搜索文件内容 | grep(pattern, path?, include?) | 有 |
| glob 与 grep 结果超过上限时的采样与分页选项 | sampleOverCapGlobResults, globMaxResults, grepMaxMatches | 有 |
| 行预览长度限制 | grepMaxLineBytes | 有 |
| 原始输出与 stderr 上限 | rawOutputMaxBytes, stderrMaxBytes | 有 |
| 工具调用超时与终止宽限期 | timeoutMs, graceMs | 有 |
| 工具与文件访问没有共享工作区证明 | — | 自陈无 |
| 打包二进制固定在依赖版本上 | — | 自陈无 |
| schema 只暴露一个有界页面，无分页 | — | 自陈无 |
| 启用采样时仅按搜索根正下方的第一段路径分组 | — | 自陈无 |

### `fs/tool-str-replace-editor`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| view 命令：查看文件或目录 | view(path) | 有 |
| create 命令：创建文件 | create(path, content) | 有 |
| str_replace 命令：字面量替换（要求唯一匹配） | str_replace(path, old_str, new_str) | 有 |
| insert 命令：在指定位置插入内容 | insert(path, line, content) | 有 |
| 文件查看使用从 1 开始的行号，保留制表符 | — | 有 |
| 目录查看下探两层，忽略隐藏与缓存条目 | — | 有 |
| 发生元数据未命中时记录确认缺失 | — | 有 |
| 查看结果保留前缀并追加截断提示（maxOutputChars） | maxOutputChars | 有 |
| 操作面向 UTF-8 文本，不支持二进制文件 | — | 自陈无 |
| str_replace 刻意拒绝零匹配或多匹配，没有 replace_all 参数 | — | 自陈无 |
| 每个修改操作都经过 fs/write-intent 或 fs/edit-intent 门禁 | — | 有 |

### `lsp/lsp`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| LSP 能力 seam（ctx.lsp），定义语义代码导航能力 | ctx.lsp | 有 |
| 四种操作：goToDefinition、findReferences、goToImplementation、hover | query(request, signal?) | 有 |
| registerProvider(provider) 注册后端，原子保留 id 与文件扩展名 | registerProvider(provider) | 有 |
| 扩展名 key 规范化为小写且以点开头 | — | 有 |
| 选择逐查询进行且与顺序无关 | — | 有 |
| 同一运行时内扩展名归属互斥 | — | 自陈无 |
| 仅四种操作，symbol 与 call hierarchy 暂缓 | — | 自陈无 |
| 没有观测表层（只能通过运行 query() 并按抛出的 LspError code 路由来观测） | — | 自陈无 |

### `lsp/lsp-stdio`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 通用 stdio 语言服务器后端 | — | 有 |
| 在注册前解析每项服务器局部设置；无效或冲突会回滚 | — | 有 |
| 每个 (server id, 规范工作区) 惰性 single-flight 一个服务器进程 | — | 有 |
| 兼容性优先的临时打开序列（textDocument/didOpen、操作、didClose） | — | 有 |
| 通过逐 Workspace 可中止的队列串行执行查询 | — | 有 |
| 协议 shutdown 失败后，经由子进程 seam 终止服务器后代树 | — | 有 |
| 配置：servers（服务器表）必填，每项配置 command、args、env、extensionToLanguage、initializationOptions、configuration 等 | config: { servers: { [id]: { command, args?, env?, extensionToLanguage, initializationOptions?, configuration?, maxMessageBytes, maxStderrBytes, maxDocumentBytes, shutdownTimeoutMs, killGraceMs } } } | 有 |
| 协议初始化会声明 UTF-16 位置编码、workspace 文件夹、configuration、hover 内容格式与定义/实现的 linkSupport | — | 有 |
| 不提供隔离策略，信任所配置的服务器 | — | 自陈无 |
| 临时打开兼容性下限（同步能力省略打开/关闭的服务器不受支持） | — | 自陈无 |
| 逐服务器/Workspace 串行化延迟 | — | 自陈无 |
| 被强制杀死的 harness 会遗留语言服务器 | — | 自陈无 |

### `lsp/tool-lsp`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| lsp 工具：operation、file_path、line、character | lsp(operation, file_path, line, character) | 有 |
| 四种 operation：goToDefinition、findReferences、goToImplementation、hover | — | 有 |
| line 与 character 是正的、从 1 开始的 UTF-16 光标坐标 | — | 有 |
| 工具要求从会话 header.cwd 取得工作区根目录 | — | 有 |
| 原生渲染以 `path:line:character` 条目投影位置 | — | 有 |
| 配置：maxLocations、maxResultChars、timeoutMs（由 timeout-policy 强制） | config: { maxLocations, maxResultChars, timeoutMs } | 有 |
| LSP 定位指引（system prompt 顺序 112） | — | 有 |
| 格式错误的提供方载荷仍是结构化错误（LSP_MALFORMED_RESPONSE） | — | 有 |
| UTF-16 光标坐标难以计数，非 BMP 字符周围的位置可能返回空结果 | — | 自陈无 |
| 不承诺跨服务器完整性 | — | 自陈无 |

### `sandbox/sandbox`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 进程沙箱 Service Definition（ctx.sandbox）与共享的限制词汇 | ctx.sandbox.confine(argv, policy) | 有 |
| SandboxMode：read-only、workspace-write、danger-full-access（仅限文件操作） | — | 有 |
| SandboxEnforcement：full、partial（针对每种内核 ABI） | — | 有 |
| SandboxExecutionPolicy：每次调用的完整模式及工作区根目录 | — | 有 |
| SANDBOX_UNAVAILABLE 错误：无可用后端时 fail-closed | — | 有 |
| 文件操作是完整的策略词汇 | — | 自陈无 |
| 只支持与宿主共享文件系统和内核的限制 | — | 自陈无 |
| 拒绝报告是 stderr 方言，非类型化运行时拒绝通道 | — | 自陈无 |
| Runner 诊断使用带内通道，退出状态与 stderr 无法证明来源 | — | 自陈无 |
| 每个上下文只有一个提供方 | — | 自陈无 |

### `sandbox/sandbox-local`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 本地沙箱实现，选择平台 runner（Linux: bwrap/Landlock, macOS: Seatbelt, Windows: ACL） | LocalSandboxProvider | 有 |
| 不受支持的平台和不可用 runner 会以 SANDBOX_UNAVAILABLE 拒绝 | — | 有 |
| 每次包装都报告强制执行完整度（full/partial）、拒绝签名和 runner 失败规则 | — | 有 |
| bwrap profile 将只读宿主根目录、全新的 /dev 与 /proc 组合起来 | — | 有 |
| workspace-write 另加临时的 /tmp 与可写工作区绑定挂载 | — | 有 |
| Seatbelt profile 默认允许，但带 (deny file-write*) 和写入 allow-list | — | 有 |
| Windows ACL 为每个工作区保留确定性写入 SID，为每个活跃的会话/工作区对分配随机私有临时目录 | — | 有 |
| runner 选择在提供方生命周期内缓存 | — | 自陈无 |
| Windows ACL 只能实现部分强制执行（受限令牌必须保留 Everyone） | — | 自陈无 |
| Landlock 可能只实现部分强制执行（较旧 ABI 有限制） | — | 自陈无 |
| Seatbelt 依赖已弃用的 sandbox-exec | — | 自陈无 |

### `sandbox/sandbox-policy`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 沙箱策略解析的唯一归属位置（ctx.sandboxPolicy） | ctx.sandboxPolicy | 有 |
| 部署默认 SandboxMode 与回退根目录 | config: { mode, workspaceRoot } | 有 |
| 每个会话的持久模式覆盖和不可变工作区根目录 | — | 有 |
| 逐会话存储：运行时切换是追加的 sandbox/mode 事件 | setSandboxMode(session, mode) | 有 |
| 接口 resolve()：解析完整的逐调用策略 | resolve({ session?, mode? }) | 有 |
| 接口 defaultMode、workspaceRoot、effectiveSandboxMode()、SANDBOX_MODES | — | 有 |
| sandbox:policy 上下文贡献：当前文件沙箱策略 | — | 有 |
| 每个会话只有一个主要工作区根目录 | — | 自陈无 |
| 仅限文件操作模式，网络和进程策略不在其词汇中 | — | 自陈无 |
| 有意概述临时区域（强制执行后端才会选定） | — | 自陈无 |

### `sandbox/sandbox-windows-acl`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Windows 写入限制沙箱后端，koffi 实现受限令牌机制 | AclSandbox | 有 |
| workspace-write 模式（登录 SID、Everyone、工作区 SID、临时 SID） | — | 有 |
| read-only 模式（登录 SID、Everyone——不含写入 SID） | — | 有 |
| 工作区复用与临时隔离（工作区 ACE 常驻，临时 SID 可回收） | — | 有 |
| ACL runner 入口契约（argv 前缀包装） | runner.js --workspace  --temp  --mode | 有 |
| Everyone 授权仍是环保中的写权限来源 | — | 自陈无 |
| 硬链接是文件对象别名，使工作区 DACL 传播到外部路径 | — | 自陈无 |
| 写入受限；读取、网络与进程可见性不受限 | — | 自陈无 |
| 控制台隔离不可用 | — | 自陈无 |
| ACL 授权是对真实目录的驻留改动 | — | 自陈无 |
| 被授权目录必须由调用者拥有 | — | 自陈无 |
| 环境临时根目录绝不会被隐式授权 | — | 自陈无 |
| 受限子进程的临时能力按每个活跃的会话/工作区对私有 | — | 自陈无 |
| 受限令牌下 whoami 与令牌检查 cmdlet 会失败 | — | 自陈无 |
| 每个工作区一个写入白名单（write SID 就是工作区身份） | — | 自陈无 |
| 清理按设计尽力而为，临时 ACE 可能残留 | — | 自陈无 |
| 常驻工作区 ACE 是不可见残留（改名后旧 ACE 留在原地） | — | 自陈无 |
| NULL-DACL 目录在 grant+revoke 往返下不保持身份 | — | 自陈无 |
| 受限孙进程的管道 stdio 捕获不可用（named pipe 的默认 SD 模板） | — | 自陈无 |
| 授权物化是急切的全树传播，大型工作区树上可能慢数十秒 | — | 自陈无 |
| 宽目录与 FAT 卷警告已推迟；FAT 类目标保持可写 | — | 自陈无 |
| PowerShell 语言模式因受限模式而异 | — | 自陈无 |

### `shell/bash-local`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 本地 bash 执行器实现 run()、start() | LocalBashExecutor | 有 |
| 每次调用都启动新的非登录 bash -c，不保留 shell 状态 | — | 有 |
| 组装条目是一层，settings 提供方的用户段会叠加其上 | — | 有 |
| 在受管进程组之上应用配置预算（cwd、timeoutMs、stdoutMaxBytes） | config: { cwd, timeoutMs, maxTimeoutMs, maxOutputBytes, maxSpillBytes, graceMs } | 有 |
| 超时与取消分类：timedOut 对应执行器超时，aborted 对应上游取消 | — | 有 |
| 适合模型的终端环境（NO_COLOR=1 TERM=dumb PAGER=cat GIT_PAGER=cat） | — | 有 |
| 后台进程：readOutput() 把基于偏移的 stdout/stderr 读取合并为消费式增量 | — | 有 |
| 自身不提供隔离，需要组合 dsh-bash-sandbox | — | 自陈无 |
| 没有持久 shell 或 PTY | — | 自陈无 |
| 仅支持 POSIX，不支持 Windows | — | 自陈无 |
| 后台 spawn 失败提示只交付一次 | — | 自陈无 |

### `shell/bash-sandbox`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 沙箱消费型的 bash 执行器，限制 writeText/editText 的文件影响 | SandboxBashExecutor | 有 |
| read-only 模式：任何位置都不可写（仅 /dev/null 节点可写） | — | 有 |
| workspace-write 模式：只能写入 workspaceRoot + /tmp | — | 有 |
| danger-full-access 模式：不作限制直接委托 | — | 有 |
| 拒绝是结果事实（ShellRunResult.sandbox.denied: true） | — | 有 |
| Runner 路径或 syscall 必须匹配才能识别缺失/不可执行 runner | — | 有 |
| 部署回退与每次调用策略（会话覆盖优于执行器默认） | — | 有 |
| 只限制文件影响，网络仍不受限制 | — | 自陈无 |
| 拒绝从失败命令 stderr 推断，可能漂移 | — | 自陈无 |
| 异步观测的后台 runner 失败没有即时错误通道 | — | 自陈无 |
| danger-full-access 有意绕过 ctx.sandbox | — | 自陈无 |

### `shell/pwsh-local`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| PowerShell 执行器实现 run()、start() | PwshLocalExecutor | 有 |
| 每次调用新建进程，无 shell 状态 | — | 有 |
| UTF-8 输出固定（[Console]::OutputEncoding） | — | 有 |
| 可执行文件解析（prioritize pwshPath, 然后在 Windows 上探测安装位置与 PATH） | resolvePwshPath | 有 |
| 受管进程组之上的配置预算 | config: { cwd, timeoutMs, maxTimeoutMs, maxOutputBytes, maxSpillBytes, graceMs, pwshPath } | 有 |
| 超时与取消分类 | — | 有 |
| 适合模型的终端环境（NO_COLOR=1 PAGER=cat GIT_PAGER=cat） | — | 有 |
| 后台进程支持 | — | 有 |
| 自身不设沙箱 | — | 自陈无 |
| 无持久 shell 或 PTY | — | 自陈无 |
| 命令字符串是 PowerShell 文本（-Command 域没有 shell 引号层） | — | 自陈无 |
| 后台 spawn 失败提示只投递一次 | — | 自陈无 |
| Windows 终止不报告信号（以退出码 1 结束） | — | 自陈无 |
| 编码 preamble 位于命令之前，影响 param()、#requires 和 using 语句 | — | 自陈无 |
| Windows PowerShell 5.1 下的非 ASCII stdin 可能被错误解码 | — | 自陈无 |

### `shell/pwsh-sandbox`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| PowerShell 沙箱执行器，消费 ctx.sandbox 限制每次 spawn | SandboxPwshExecutor | 有 |
| 支持 read-only、workspace-write、danger-full-access 模式 | — | 有 |
| Windows 读不受限（ACL runner 只限写） | — | 自陈无 |
| Windows workspace-write 的临时权限按每个活跃的会话/工作区对私有 | — | 自陈无 |
| Windows read-only 不授予任何显式可写根目录，但仍为部分强制执行 | — | 自陈无 |

### `shell/shell`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 前台执行：run(spec)，命令完成时 resolve，只因基础设施失败而 reject | run(spec) | 有 |
| 后台执行：start(spec)，立即返回不含任务语义的 ShellProcess 句柄 | start(spec) | 有 |
| 沙箱执行器能力标志：sandboxMode（基类中为 undefined） | sandboxMode | 有 |
| ShellProcess.readOutput()：增量读取输出，连续读取绝不重复交付 | readOutput() | 有 |
| ShellProcess.kill()：终止进程组 | kill() | 有 |
| 没有交互式输入词汇（stdin 仅在 spawn 时写入一次） | — | 自陈无 |
| 前台超时始终由执行器负责 | — | 自陈无 |

### `shell/shell-env`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 注册表管理受信任的受管 DSH_* 环境变量 | ctx.shellEnv | 有 |
| 内置 shell 事实（DSH_HOME、DSH_SHELL=1、DSH_SESSION_ID） | — | 有 |
| 其他插件可注册额外的可枚举事实，注册随插件纤维释放 | register(contributor) | 有 |
| list() 只枚举 contributor 声明的变量，不包括内置键 | — | 自陈无 |

### `shell/tool-bash`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| bash 工具参数：command（必填）、description（必填）、timeoutMs、workdir、run_in_background、sandbox_permissions、justification | bash(command, description, timeoutMs?, workdir?, run_in_background?, sandbox_permissions?, justification?) | 有 |
| 托管 shell 环境：每次调用收集新的 DSH_* 变量 | — | 有 |
| 前台执行结果：stdout、可选 [stderr] 段与沙箱拒绝、超时、信号、退出代码标记 | — | 有 |
| 后台执行：立即返回 job id，通过 ctx.jobs 任务运行时管理 | run_in_background: true | 有 |
| 沙箱拒绝与升权：enableRunInBackground、拒绝型沙箱报告事实、升权参数 | sandbox_permissions, justification | 有 |
| tool:bash 提示词段落（顺序 105）：检查退出状态 marker | — | 有 |
| 回放退出状态 pill 从结果文本解析（残留问题） | — | 自陈无 |
| bash 工具不采用 timeout-policy 预算 | — | 自陈无 |
| 后台进程没有执行器超时 | — | 自陈无 |

### `shell/tool-bash-persistent`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 持久 bash 工具：command（必填）、description（必填）、timeoutMs、workdir、run_in_background、sandbox_permissions、justification | bash(command, description, timeoutMs?, workdir?, run_in_background?, sandbox_permissions?, justification?) | 有 |
| 命令共享每个 Agent 的一个 shell，跨调用保留状态 | — | 有 |
| cwd、导出的环境变量、已激活环境、函数和后台任务跨调用保留 | — | 有 |
| 结果包含 exit code marker（[exit code: N] 或 [shell exited: code N]） | — | 有 |
| 长输出保留最早的前缀并追加截断提示 | maxOutputChars | 有 |
| 工具需要拥有它的 Agent 和真实 PTY 后端 | — | 自陈无 |
| 交互式前台子进程只有在进程管理提供方能证明其 stdin 等待时才会提前返回部分输出 | — | 自陈无 |
| 显式 exit 与超时会丢弃 shell 状态 | — | 自陈无 |
| 取消会重置 shell 并丢弃结果 | — | 自陈无 |

### `shell/tool-pwsh`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| pwsh 工具参数：command（必填）、description（必填）、timeoutMs、workdir、run_in_background、sandbox_permissions、justification | pwsh(command, description, timeoutMs?, workdir?, run_in_background?, sandbox_permissions?, justification?) | 有 |
| 模型看到生成的 pwsh schema，托管 shell 环境与沙箱支持 | — | 有 |
| tool:pwsh 提示词段落（顺序 105）：非零退出与 Windows 中断约定 | — | 有 |
| 后台模式依赖 ctx.jobs 与 dsh-tool-jobs | — | 有 |
| 前台结果：terminus 卡片，条件行精确为 [output truncated]、[sandbox] 标记、[timed out]、[killed by signal]、[exit code] | — | 有 |
| Windows 沙箱下的语言模式与 named-pipe 捕获限制 | — | 自陈无 |
| 无持久 shell（对应物是 tool-pwsh-persistent） | — | 自陈无 |
| PowerShell 方言约定（原生路径、$env: 变量） | — | 自陈无 |
| 会话 cwd 身份不做规范化（parity 差距留待共享 shell 工具基座提取时解决） | — | 自陈无 |

### `shell/tool-pwsh-persistent`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 持久 pwsh 工具：command（必填）、description（必填）、timeoutMs、workdir、run_in_background、sandbox_permissions、justification | pwsh(command, description, timeoutMs?, workdir?, run_in_background?, sandbox_permissions?, justification?) | 有 |
| 命令共享每个 Agent 的一个 shell，跨调用保留状态 | — | 有 |
| 输入回显不可避免（PSReadLine 会把提交的输入渲染回终端流） | — | 自陈无 |
| 模型命令中的裸 ESC 字符不受支持 | — | 自陈无 |
| 模型重定义 prompt 函数会移除就绪标记 | — | 自陈无 |
| 命令执行期间没有交互 stdin | — | 自陈无 |
| SIGTSTP/SIGHUP 在 Windows 不可用，SIGINT 以 Ctrl-C 投递 | — | 自陈无 |
| Windows ACL 沙箱只读模式下，pwsh 以 ConstrainedLanguage 启动 | — | 自陈无 |
| BEL 终结的 OSC 标记仅是就绪信号 | — | 自陈无 |

### `subprocess/subprocess`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 子进程 seam（ctx.subprocess）执行世界的进程部分 | ctx.subprocess | 有 |
| spawn(spec) 立即返回活动句柄，done 在进程关闭时 resolve | spawn(spec) | 有 |
| resolveExecutable(command, env?, signal?) 验证绝对命令或根据 PATH 解析 | resolveExecutable(command, env?, signal?) | 有 |
| 终止与存活等待：terminate()（SIGTERM→宽限→SIGKILL 升级）、waitForExit(signal?) | terminate(), waitForExit(signal?) | 有 |
| spawnTerminal(spec) 非管道原语，分配真实 PTY、UTF-8 I/O、前台进程组、信号发送 | spawnTerminal(spec) | 有 |
| stdio 处置方式：pipe、inherit、collect（有界内存尾部 + 可选 spill） | — | 有 |
| scrubbedParentEnv()、SENSITIVE_ENV_PATTERN 共享环境清理定义 | — | 有 |
| 由 SDK 管理的 spawn 仍在服务之外 | — | 自陈无 |
| 拆卸阶梯归消费方所有 | — | 自陈无 |

### `subprocess/subprocess-local`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 本地子进程实现（LocalSubprocessRuntime） | LocalSubprocessRuntime | 有 |
| 以平台适配方式发送信号的 detached 进程树（POSIX 进程组/Windows taskkill） | — | 有 |
| 按流划分的处置方式（pipe、inherit、collect） | — | 有 |
| 凭据清除 + 显式合并（移除 *KEY*/*PASSWORD*/*SECRET*/*TOKEN* 和 DSH_*） | — | 有 |
| 基于偏移量的读取（服务自身不持有游标） | — | 有 |
| 可执行文件查找与终端进程所有权 | — | 有 |
| 先终止再等待退出的 dispose | — | 有 |
| 同步宿主退出最终清理（Node exit listener） | — | 有 |
| Windows 进程树支持仅为尽力而为 | — | 自陈无 |
| Windows 终端信号是控制台级的 | — | 自陈无 |
| 守护化的终端后代仍可能逃出可观察边界 | — | 自陈无 |
| 进程内清理要求退出阶段仍能执行 JavaScript | — | 自陈无 |
| 凭据清除依赖名称启发式规则 | — | 自陈无 |
| 不会删除已完成的 spill 文件 | — | 自陈无 |

### `terminal/terminal`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 限定所有者范围的持久 PTY seam（ctx.terminals） | TerminalSessionService | 有 |
| 生成不透明的会话 id，通过具名后端路由创建操作 | — | 有 |
| 每个操作限制在完全相同的活跃 Agent 内 | — | 有 |
| 会话只存在于进程本地，harness 重启后不会恢复 | — | 自陈无 |
| 系统有意不支持跨 agent 共享 | — | 自陈无 |

### `terminal/terminal-bash`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 持久 shell 后端（ctx.terminals 提供方）基于 ctx.subprocess.spawnTerminal | terminal-bash | 有 |
| 启动交互式 shell，保留有界的逐行输出并检测就绪状态 | — | 有 |
| bash 方言通过环境安装提示符（PS1 加 OSC 133;D;） | — | 有 |
| pwsh 方言通过会话写入 prompt 函数，并等待受控提示符可见 | shellDialect: pwsh | 有 |
| 就绪检测结合私有提示符标记、前台 stdin 等待事实、静默回退和绝对超时 | — | 有 |
| UTF-8 编码钉（pwsh 下的 [Console]::OutputEncoding） | — | 有 |
| 取消操作会向当前前台进程组发送 SIGINT | — | 有 |
| 关闭操作会等待由句柄提供方负责的完整会话终止 | — | 有 |
| 输出按行规范化；不支持全屏备用缓冲区交互 | — | 自陈无 |
| 精确 stdin 等待检测取决于已挂载的进程管理提供方 | — | 自陈无 |
| pwsh 引导可能在 Windows ACL 沙箱只读模式下被拒绝 | — | 自陈无 |
| 清理保证以 SubprocessTerminalHandle 的保证为准 | — | 自陈无 |

### `terminal/tool-terminal`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 6 个面向模型的工具：terminal_open、terminal_send、terminal_read、terminal_signal、terminal_close、terminal_list | terminal_open, terminal_send, terminal_read, terminal_signal, terminal_close, terminal_list | 有 |
| 操作要求提供完全相同的发起 Agent（agent-scoped access control） | — | 有 |
| terminal_send(run_in_background: true) 复用 ctx.jobs | — | 有 |
| 配置：enableRunInBackground、maxResultBytes | — | 有 |
| 终端指引（system prompt）与工具 schema | — | 有 |
| 结果包含有界的 MOTD、发送增量、scrollback 页、就绪原因和清理错误 | — | 有 |
| 不公开具名按键序列、TUI、BEL、调整大小、自动启动或跨 agent 共享 schema | — | 自陈无 |
| 后台模式同时依赖 @deepseek-ai/dsh-jobs 及其面向模型的控制器 | — | 自陈无 |

## F. 宿主、前端与对外协议

66 个包，454 条，其中有 300、自陈无 154。

### `acp/acp`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Agent Client Protocol（ACP）服务器：通过stdio JSON-RPC提供仅面向自动化的ACP服务 | AgentSideConnection | 有 |
| 协议方法：initialize/authenticate/session/new/session/prompt/session/cancel/session/update/session/request_permission | 10个协议方法 | 有 |
| 多会话支持：一个连接可拥有多个会话 | 连接管理 | 有 |
| 已提交答案输出：提交消息输出牺牲逐token低延迟以换取干净自动化结果 | 批量交付 | 有 |
| 已知限制：仅新会话 | 不支持加载/列出/恢复/删除/fork | 自陈无 |
| 已知限制：仅光栅图片和一个workspace | 音频/嵌入/非空附加目录被拒绝 | 自陈无 |
| 已知限制：仅已提交答案 | 无逐token进度/推理/工具活动等实时数据 | 自陈无 |
| 已知限制：连接生命周期管理 | 一个连接释放其所有会话 | 自陈无 |

### `api/gateway`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Typert RPC端点：为Host与Client提供Cordis环境，Host提供ctx.typertGateway，Client提供ctx.remote | TypertGatewayService | 有 |
| 严格模式与SRC模式：严格从ctx.typert.local读取描述符，SRC是开发回退路径 | 描述符解析 | 有 |
| 业务服务继承：服务继承TypertRemoteService并用@Remote/@RemoteScope标记方法 | 装饰器标记 | 有 |
| 查找参数解析：使用ctx.typert.lookups中有效的resolver | resolver配置 | 有 |
| 取消支持：Remote方法可声明signal: AbortSignal作最后参数 | AbortSignal注入 | 有 |
| 已知限制：连接分发故障映射为internal代码 | 无详细信息 | 自陈无 |
| 已知限制：SRC模式仅支持名称唯一的标识符参数 | 无解构/默认值/剩余参数 | 自陈无 |
| 已知限制：Client侧仅能挂载严格模式生成的贡献项 | 无SRC客户端编解码器 | 自陈无 |

### `api/remotes`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 双侧BFF：Host入口负责身份策略，Client入口导入生成/remote产物 | API Remote能力 | 有 |
| 身份解析：createApiRemoteAgentResolver()复用live Agent、恢复冷会话、去重并发恢复 | 身份解析策略 | 有 |
| 转发事件管理：API_REMOTE_FORWARDED_EVENTS明确指定转发的Host事件 | 转发事件名单 | 有 |
| 构建边界：Host与Client face拆分，src/remote-events.ts与src/types.ts双列两个face | 编译分离 | 有 |
| 已知限制：能力集合由构建时显式导入固定 | 无运行时发现 | 自陈无 |
| 已知限制：增加能力需显式导入与挂载 | 无自动激活 | 自陈无 |

### `boot/app-boot`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 启动路径解析：resolveConfigPath()生成绝对配置路径，snapshot模式映射cordis.yml/yaml到cordis.snapshot.yml | resolveConfigPath | 有 |
| 环境加载：loadEnv()加载.env，loadLayeredEnv()构建继承环境>项目.env>用户.env快照 | loadEnv / loadLayeredEnv | 有 |
| 启动故障处理：installFailLoud()转换未处理rejection为stderr消息并exit(1)，支持可选release清理钩子 | installFailLoud / FAIL_LOUD_RELEASE_TIMEOUT_MS | 有 |
| 加载检查：assertEntriesLoaded()检查已启用配置项是否有fiber，assertEntriesActivated()检查是否激活 | assertEntriesLoaded / assertEntriesActivated | 有 |
| Patch加载：loadOptionalPatches()解析可选patch列表文件，loadOverlayPatches()解析必需顶层YAML数组 | loadOptionalPatches / loadOverlayPatches | 有 |
| Include挂载与HMR：mountRootInclude()注册cordis:include/group builtin并挂载，watchUserPatches()向Cordis HMR注册patch文件 | mountRootInclude / watchUserPatches | 有 |
| 启动合成：boot()创建根上下文、挂载Loader、执行可选准备操作、等待条目激活并返回根上下文 | boot | 有 |
| Profile机制：resolveProfileDir/initProfile/loadProfile等Profile相关函数 | Profile数据结构 | 有 |
| Harness源码段落：addHarnessSourceSection()添加harness:source系统提示词段落 | addHarnessSourceSection / HARNESS_SOURCE_SECTION | 有 |
| 已知限制：裸包specifier依赖Loader内部机制 | 需要node-addon-require-builtin | 自陈无 |
| 已知限制：快照回放替换仅识别特定basename | 自定义配置名需调用方自行选择 | 自陈无 |
| 已知限制：环境发现以启动为界 | loadLayeredEnv只读一次调用目录与harness home | 自陈无 |
| 已知限制：用户patch会替换匹配到的整个配置 | 不做深度合并 | 自陈无 |

### `boot/cmdline`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 启动器提供值：ctx.cmdlineArgs和ctx.appExit，前者get()返回内层参数快照 | ctx.cmdlineArgs / ctx.appExit | 有 |
| 命令行解析适配：parseCmdline()仅适配commander，校验与发布归program自己的action | parseCmdline | 有 |
| 已知限制：启动器flag必须写在应用参数之前 | 切分按位置进行 | 自陈无 |
| 已知限制：应用自有服务没有静态声明提供方 | 缺少提供方时加载失败 | 自陈无 |
| 已知限制：用户patch整体替换会丢掉表达式 | flag胜过的是表达式旁写着的值 | 自陈无 |

### `bundle/base`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 共享核心插件行：插入全部基础插件（模型适配器、agent-default-model、工具、持久化等） | cordis.patch.yml | 有 |
| 平台门控shell栈：bash-sandbox/tool-bash禁用于win32，pwsh-sandbox/tool-pwsh仅在win32挂载 | process.platform | 有 |
| 已知限制：patch会替换整行config | profile覆盖必须重述需保留字段 | 自陈无 |
| 已知限制：Windows临时目录授权是按会话的私有子目录 | workspace-write限制在会话temp子目录 | 自陈无 |

### `bundle/headless`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 一次性任务组合包：禁用HMR，将Code Mode worker挂载，插入headless-runner插件 | cordis.patch.yml | 有 |
| 任务执行：runner读取ctx.agentDefaultModel，创建新Agent，提交任务作为用户消息，等待停稳 | headless-runner插件 | 有 |
| 输出处理：将最后一条非空assistant文本写入stdout，最终结束原因为error时将code/message写入stderr | stdout/stderr输出 | 有 |
| 已知限制：只提交一个任务 | 无交互式后续输入surface | 自陈无 |
| 已知限制：ctx.appExit由启动器持有 | 在dsh启动器之外启动失败 | 自陈无 |

### `bundle/web-app`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 浏览器表层组合包：设置coding persona，插入Web宿主行（webserver/API网关/workspace等），挂载web-runtime插件 | cordis.patch.yml | 有 |
| 前端dist服务：通过dsh-web-frontend exports解析已构建dist，挂载frontend-static回退席位所有者 | ctx.webServer | 有 |
| 浏览器自动打开：openBrowser为true且非SSH启动时用默认浏览器打开规范宿主机URL | openBrowser配置 | 有 |
| 命令行解析：web-startup提供方解析--host/--port/--trusted-host/--no-open等flag | --host / --port / --trusted-host / --no-open | 有 |
| 已知限制：前端dist必须已构建 | 无从源码直接服务回退 | 自陈无 |
| 已知限制：lanAddresses是启动期快照 | 启动后网卡变化不重新公告 | 自陈无 |
| 已知限制：只观测交接启动 | 浏览器打开后不上报 | 自陈无 |
| 已知限制：SSH转发持有浏览器URL | 打印的URL指向远端loopback | 自陈无 |
| 已知限制：浏览器命令覆盖只能来自启动环境 | .env不能设置BROWSER | 自陈无 |

### `client/connection`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 客户端协议消费层：挂载共享 API 客户端、当前页面 loopback 状态、可观察的 hostDescription 与单消费方流循环启动器 | AbstractApiClient, ClientTransportHooks, host.describe | 有 |
| 浏览器携带 HTTP POST 与双条只下行 WebSocket（events.mux 与 events.host）；进程内满足同一双流抽象 | /api/events.mux, /api/events.host | 有 |
| /api 路由中的特权方法集（host.pickDirectory、host.openPath、settings 与 credentials 全套、agentPreset 创作面）以空信任表过信任栅栏 | —> | 有 |
| 非浏览器客户端经由 loopback 地址或已声明权威通过信任栅栏 | trustedHosts | 有 |
| History 会恢复未附加的会话，打开 history 可能创建宿主侧 agent，增加首次打开延迟 | —> | 自陈无 |
| /api 桥把每个请求体整体缓冲在内存，maxRequestBodyBytes 默认 300 MiB | —> | 自陈无 |

### `client/hmr`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 浏览器侧订阅 SSE 通道接收 rebuilt 帧，通过队列串行执行重载 | /plugins/events | 有 |
| 重载驱动器会创建全新 fiber 和组件，React 状态会丢失 | —> | 自陈无 |
| 失败的重载使配置项处于 FAILED 状态，系统不会自动回滚 | —> | 自陈无 |

### `client/locale`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| LocaleRuntime 支持 zh/en 偏好存储在 $DSH_HOME/settings.yaml | locale.preference | 有 |
| 浏览器暂时使用 navigator 请求的语言，Host 读取后实时替换 | —> | 有 |
| 提供 ns×locale 字典注册表与 slot 系统 LocaleFace，支撑框架注入的 t 席位 | ctx.slots.installLocale, Translate, TranslateNS | 有 |
| 部分界面仍保留内联文案，注册表文本只读取一次翻译 | —> | 自陈无 |

### `client/modules`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 惰性 CJS 表实现：执行插件 bundle 只注册 factory，物化时运行副作用 | window.__ModuleLoader__.load() | 有 |
| 会话 factory 递归物化依赖，图组合把动态提供方排在消费者之前 | dsh.client.external | 有 |
| prefetch 与 invalidate 支持 HMR | —> | 有 |
| 有意采用扁平模块图，卸载记录由 HMR 驱动器维护 | —> | 自陈无 |

### `client/runtime`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| SlotRegistry 与 SessionRuntime 拥有 Session 对象、列表与 scope 状态，共享 Host 流事件分发 | ctx.sessions, ctx.remote.$on, agent/remote-event | 有 |
| 客户端会话一律由 Host 创建，agent scope 在列表镜像时创建 | session.create, session.list | 有 |
| 每个 Session 持有 ProjectionValueStore，由历史尾部 projections 播种并按 session/projection 帧更新 | ConversationSnapshot.projections, projections.faceOf, useProjection | 有 |
| 浏览器采样当前 Intl.DateTimeFormat().resolvedOptions().timeZone 并只附加到本次提示词 RPC | —> | 有 |
| 设置所有者共用 SettingsScopeSpec、SettingsScope 与快照类型 | ctx.settingsScope, SettingsScope | 有 |
| Slot 声明注入：ctx.slots.inject 把完整 SlotMap key 作为依赖，未声明时等待 | ctx.slots.inject | 有 |
| Workspace 与 Session 列表各有单调的 pending→ready 基线与刷新活动/错误状态 | WorkspaceListState, SessionListState | 有 |
| SessionSummary.pendingInteraction 分类待处理交互为 approval、plan-review 或 question | pendingInteraction | 有 |
| WorkspaceRuntime.delete 从客户端投影移除注册记录后，对应 host/workspace-removed 帧负责同步其他标签页 | host/workspace-removed | 有 |
| 已移除的 Workspace id 保留进程本地删除标记，避免延迟 changed 帧复活 | —> | 有 |
| ConversationSnapshot.queue 是 Host 权威 agent.inbox.nextTurn 快照，待处理 next-step steering 不进入投影 | agent.inbox.nextTurn, Session.updateQueue | 有 |
| 每个 Session 通过 ConversationNodeAssembler 处理连续事件窗口，折叠有关联的 update 为稳定节点 | ConversationNodeAssembler, ConversationNodeDefinition | 有 |
| Chat 业务行彼此独立注册，每个行呈现稳定事件 id 与完整 Location 索引 | ctx.conversationEvents, ChatNodeDataMap | 有 |
| Trajectory 组装出按时间顺序排列、以用途为判别字段的提供方请求流 | —> | 有 |
| 每个 ToolCallBlock 通过 subCalls 递归拥有自己的子调用树 | ToolCallBlock.subCalls | 有 |
| SessionManager 独立保留最近一次验证的 session/title 快照，seq 高者胜 | SessionSummary.title, displayTitle | 有 |
| Host LLM retry invariant 验证按提供方路由的 llm/retry 与 llm/retry-started 记录 | llm/retry, llm/retry-started | 有 |
| 每个常驻 Session 拥有 modelSelection 快照，包含当前模型选择、按提供方分组的目录、逐提供方失败记录 | modelSelection | 有 |
| ISessions.fork 只在子会话摘要已能在本地寻址后才完成，支持 increaseTitle 重命名 | ISessions.fork, SessionForkError | 有 |
| loader.unload 是 stub，客户端没有从 fiber dispose 到注册与样式移除的卸载链 | —> | 自陈无 |
| scope 拆卸由阶段驱动且目前只能有一个占用者 | list.current | 自陈无 |

### `client/ui-agent-preset`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 选择新建会话据以组装的 preset，在 General 设置行呈现 | agentPreset.list, agentPreset.select | 有 |
| 新建会话界面上的 chip 用于选择下一个会话的 preset，暂存值在空白会话到达时抵达 | —> | 有 |
| 会话标题旁的只读标签显示本会话所运行的 preset | preset, agent/preset/selected | 有 |
| 设置页分区管理名单，支持复制、删除、默认值设置与打开 preset 文件 | agentPreset.read, agentPreset.copy, agentPreset.remove, agentPreset.openDocument | 有 |
| 损坏的 preset 行在名单中标记为加载失败，不能成为默认或被复制 | broken | 自陈无 |

### `client/ui-attachment`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 附件栏将待发送草稿图片渲染为 64px 缩略图，滚动条始终隐藏 | AttachmentRail | 有 |
| 消息图片与灯箱：MessageImage 加载会话授权 URL，ImageLightbox 文档级模态预览 | MessageImage, ImageLightbox | 有 |
| 拖放遮罩在文件拖拽悬停时显示邀请层，插画与上限说明 | DropOverlay | 有 |
| 仅支持图片，非图片文件尚无附件栏卡片与历史渲染 | —> | 自陈无 |
| 灯箱无缩放与下载，仅以适配视口的尺寸渲染原图 | —> | 自陈无 |
| 灯箱不锁定焦点，Tab 仍可移动到背后的页面 | —> | 自陈无 |

### `client/ui-brand-official`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 仅当 DSH_CLIENT_BUILD_PROFILE 为 official 时填充 sidebar.brand.mark、sidebar.brand.name 和 conversation.hero.brand.mark | —> | 有 |
| 三个占位者通过嵌套 slots.inject 声明感知注册，无论包激活顺序如何都能工作 | sidebar.brand.mark, sidebar.brand.name, conversation.hero.brand.mark | 有 |
| 本包只提供一组 occupant，其他呈现应由占用相同 slot 的另一个包提供 | —> | 自陈无 |

### `client/ui-commands`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 客户端命令 API：会话 key 的命令目录缓存、/command source 与派发 | ctx.commandUi, CommandUiContract.register, CommandUiSpec | 有 |
| 贡献项是客户端自有命令，装饰项为已存在的 host 命令添加裸调用 popup | ctx.commandUi.decorate | 有 |
| 命令类型按每次派发派生，从不在注册时定型 | —> | 有 |
| 菜单查询按顺序且不区分大小写地模糊匹配命令名子序列 | matchSpace, matchEnter | 有 |
| matchEnter 强制执行提交信封，图片附件提交时只有声明 input.images 的命令继续 | input.images | 有 |
| command.execute 返回后发布本地 command/executed，其他客户端只通过 Host 事件流收到 | command/executed | 有 |
| 脱离会话后 detached result 的 notice 回退到 console | SessionInput.notify | 自陈无 |

### `client/ui-conversation`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 会话领域骨架、聊天视图、编辑器 dock、输入区 dock、详情壳层 | ChatFlow, ConversationController | 有 |
| 压缩在检查点自身消息流位置渲染为折叠标记，不替换其上方 transcript | compaction/summary | 有 |
| 常驻会话壳跨无会话与有会话状态保留，Workspace picker 入口 | conversation.hero.workspace | 有 |
| 输入框与用户气泡中的引用使用聊天气泡图标加业务色文字 | reference, ReferenceInsert | 有 |
| 非用户消息渲染为默认折叠的展开项，标题栏显示角色与生产者名称 | —> | 有 |
| Think 行默认折叠，摘要从结算后的首行切换到最新非空行的实时推理 | thinking | 有 |
| 聊天视图保留工具消息流位置但委托其展示 | tool-call, conversation.chat.node | 有 |
| 审批通过本包声明的链条接管编辑器，ApprovalPanel 取代 InputBar | ApprovalPanel, approval/resolved | 有 |
| Access 席位挂载 PermissionSelect，选中预设经 /permission 命令提交 | PermissionSelect, /permission | 有 |
| TodoDock 读取 host 计算的 todos 投影并渲染 TodoPanel | useProjection('goal'), todo/write | 有 |
| QueueDock 是末端 input-dock 条目，队列为空时隐藏 | session/queue, QueueDock | 有 |
| 键盘消息提交按运行状态与 steering 能力解析投递方式 | ui-conversation.busyEnter | 有 |
| 图片经粘贴与拖放进入，输入栏绑定 document 级拖拽监听 | imageLimits, attachment-error | 有 |
| 聊天统计行的 token 账目来自 tokenUsage 投影与 sessionStats 投影 | tokenUsage, sessionStats | 有 |
| ContextMeter 渲染上下文占用率，由 contextPressure 与 contextBreakdown 投影供数 | contextPressure, contextBreakdown, projectedTokens | 有 |
| 统计行的回退折算只覆盖窗口内消息流 | —> | 自陈无 |
| 详情面板没有入口，ChatViewInjected.openDetails 无人调用 | —> | 自陈无 |
| assistant 逐消息分页是预留 slot | —> | 自陈无 |
| 已发送的 user 消息无法编辑 | —> | 自陈无 |
| 审批面板的「始终允许此类」暂缓 | —> | 自陈无 |
| TodoPanel 将过长条目截成单行省略号 | —> | 自陈无 |
| Queue 编辑仅支持文本 | —> | 自陈无 |
| Queue 严格 steering 会保留完整消息 | —> | 自陈无 |

### `client/ui-deliverables`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 产出文件与可点击文件引用的属主，向系统提示词注册文件指引 | deliverablesDefinition, producedForClosing | 有 |
| ProducedFiles 在收尾消息正文与 IconActions 之间渲染产出文件行 | ProducedFiles, openFile | 有 |
| 收尾正文承载文件词表，chatFileMentions 服务供 chat 视图按收尾消息查询 | chatFileMentions, producedFileMentions | 有 |
| Node 侧注册静态系统提示词段落 ui:deliverable-file-references | ui:deliverable-file-references | 有 |
| 提及匹配只认精确路径或唯一 basename | —> | 自陈无 |
| 终端命令间接创建的文件不在匹配词表内 | —> | 自陈无 |
| 原生文件夹交接以 Host 桌面为目标 | —> | 自陈无 |

### `client/ui-directory-picker-browse`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 应用内目录浏览界面，680×500 的 Miller 分栏视图 | host.listDirectory, host.createDirectory | 有 |
| 新建文件夹、打开、隐藏条目过滤等操作 | —> | 有 |
| 无搜索、无多选、无重命名或删除 | —> | 自陈无 |

### `client/ui-directory-picker-native`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 原生目录选择界面，使用 ctx.workspaces.pickDirectory 驱动 OS 选择框 | ctx.workspaces.pickDirectory | 有 |
| 占位者在每个 open 上升沿武装一次 | —> | 有 |
| 无法取消已打开的选择框 | —> | 自陈无 |
| 仅限本地 Host 载体 | —> | 自陈无 |

### `client/ui-goal`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Goal 界面插件：GoalBar 条带是 conversation.input.dock 上的第二张独立卡片 | useProjection('goal'), ctx.remote.goals | 有 |
| 注册每条持久 /goal command/run 为 command-input Chat Node | command-input | 有 |
| 只反映持久 phase，投影省略进程本地 activation | —> | 自陈无 |

### `client/ui-input-trigger`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 输入触发流水线：光标处 / 与 @ 检测、分组候选菜单、分发到已注册 source | ctx.inputTriggers, /command source | 有 |
| 分层设计：纯内核与壳层分离，支持 ReferenceInsert.appearance 标记 | ClientSessionContext, InputTriggerSource | 有 |
| MenuView 渲染进 conversation.input.overlay slot，指针落在菜单外关闭 | conversation.input.overlay | 有 |
| combobox 模式：焦点留在 textarea，行在 mousedown 时完成 pick | aria-activedescendant | 有 |
| 只有全局 source 层，会话 scope source 注册已有设计但未启用 | —> | 自陈无 |
| InputTriggerCandidate.icon 以文本渲染 | —> | 自陈无 |

### `client/ui-jobs`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Web 后台任务特性的归属方，向 conversation.session.header.actions 贡献任务列表 | ctx.jobs, session/jobs | 有 |
| 只有会话至少有一个任务时才渲染触发器 | jobsBySession | 有 |
| 任务行是只读的，中断还额外欠一个 seam | —> | 自陈无 |
| 列表不等于注册表自己的集合 | —> | 自陈无 |

### `client/ui-layout`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 外壳插件：三栏 AppFrame 加 ctx.layout 面板几何服务 | ctx.layout, LayoutController | 有 |
| 主题呈现器消费 ctx.theme 快照投影到 document | ctx.theme, color-scheme, body[data-ds-dark-theme] | 有 |
| 面板几何信息是瞬时状态 | —> | 自陈无 |
| 让步链自动关闭通过推导零宽度实现 | —> | 自陈无 |

### `client/ui-message-feedback`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 单条消息反馈：Like/Dislike 按钮加可选备注 | conversation.chat.assistant-actions, messageFeedback | 有 |
| 每个 Session 一个 MessageFeedbackController 支撑所有消息反馈 | messageFeedback.list, ctx.remote.messageFeedback | 有 |
| 变更按条目 compare-and-set 由 Host 负责，version-conflict 响应带回权威条目 | put, delete, version-conflict | 有 |
| 备注大小是 Host 策略，maxNoteBytes 默认 8192 | maxNoteBytes | 自陈无 |
| 无跨标签页推送，另一个标签页的评分要等重连或冲突响应才可见 | —> | 自陈无 |

### `client/ui-model-selection`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 模型选择插件：两个入口共用一份会话级目录 | ctx.modelDirectories, ModelDirectoryResolver | 有 |
| /model popupSelect 贡献项与 composer 的 conversation.input.model slot | session.models, session.selectModel | 有 |
| 紧凑型 composer 触发器打开两级 Model/Effort 菜单 | reasoning | 有 |
| 当宿主报告没有适配器服务路由时，本插件注册 composer 阻塞块 | session.models.routable | 有 |
| 已寻址 subagent 会话不公开任何入口 | —> | 自陈无 |
| 无创建期或已寻址 subagent 选择 | —> | 自陈无 |
| 目录名仅供呈现 | —> | 自陈无 |
| 不能任意输入推理强度 | —> | 自陈无 |

### `client/ui-permission-presets`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 浏览器权限界面：「通用」设置行与当前会话 popupSelect 装饰 | permission, /permission | 有 |
| Settings 行读取显式暴露的 permission 描述符，推导选项与默认值 | defaultPreset | 有 |
| 当前会话界面挂在 /permission 命令上的 popupSelect 装饰 | settings.mutate | 有 |
| Settings 行仅在 Web 中可用 | —> | 自陈无 |

### `client/ui-plan`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Plan mode 状态徽章纯浏览器 surface 插件 | conversation.input.plan | 有 |
| plan mode 通过 /plan 命令路径进入，composer placeholder 随之切换 | /plan, placeholder.plan | 有 |
| 模型通过稳定的 exit_plan_mode 工具退出 plan mode | exit_plan_mode | 有 |
| Plan mode 是引导而非执行沙箱 | —> | 自陈无 |

### `client/ui-primitives`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 纯 React 原子组件：StateDot、DisclosureRow、ic_ds_* 图标、Button/Pill/Menu 等 | HoverCard, Toast, MarkdownText | 有 |
| MarkdownText 渲染 GFM、TeX 公式、micromark CJK 扩展 | MarkdownText, extractMarkdownPlainText | 有 |
| 表格按列数决定尺寸，四列及以上的表格保持自然宽度、在包裹层内横向滚动 | md-table-wide | 有 |
| TerminalBlock 将 shell 命令渲染为终端表层，支持 ANSI 转义序列与光标移动 | TerminalBlock | 有 |
| ReadBlock 渲染文件窗口为带行号、语法高亮的代码 | ReadBlock | 有 |
| DiffBlock 渲染文件改动为内联 diff | DiffBlock | 有 |
| SearchBlock 渲染已完成的搜索，区分 grep 与 glob 结果 | SearchBlock | 有 |
| WebBlock 渲染已完成的 web 检索，显示提供方回答与源引用列表 | WebBlock | 有 |
| 流式期间跨边界引用解析被推迟 | —> | 自陈无 |
| 字形级图标是重新绘制的近似版本 | —> | 自陈无 |
| Pill 与 Input 没有设计来源 | —> | 自陈无 |
| StateDot 没有 Active 变体 | —> | 自陈无 |
| 面向用户的文案经 label props 本地化 | —> | 自陈无 |
| TerminalBlock 不是终端模拟器 | —> | 自陈无 |

### `client/ui-reference`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 统一的 Web @file 与 @session source | @file, @session, fileReferences/list | 有 |
| 选择文件会关闭补全，显示为文件图标加业务色文件名 | @path | 有 |
| 选择会话插入原子行内引用，隐藏 ref 与剪贴板表示为规范 mention | dsh-session:…, session-reference | 有 |
| 候选失败有意保持静默 | —> | 自陈无 |
| 浏览器侧不扫描文件 | —> | 自陈无 |
| 会话搜索仅使用元数据 | —> | 自陈无 |

### `client/ui-renderer`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 负责 React 渲染层的浏览器 Cordis 插件 | ctx.uiRenderer.mount | 有 |
| 所有 entry 激活后调用 ctx.uiRenderer.mount，hydrate 现有启动 DOM | slot, sessions, layout | 有 |
| 应用首帧等待全部客户端 entry | —> | 自陈无 |

### `client/ui-settings`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 设置领域底座，不含任何呈现内容 | ctx.settingsScope, ctx.settingsSchema | 有 |
| 本地唯一的 settings.describe 读取方，在转发事件与 connection/reset 时刷新 | settings.describe, settings/document-updated | 有 |
| 冷启动读取次数由 apps/web/tests/startup-rpc-budget.e2e.ts 钉住 | —> | 有 |
| 远程浏览器没有持久化设置 | —> | 自陈无 |
| 每次写入仅一个字段 | —> | 自陈无 |

### `client/ui-settings-general`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 设置外壳、无特定功能归属文案与产品引导 | sidebar.settings, settings.section, settings.onboarding | 有 |
| 外壳不自带引导文案，所有文本来自注册方 | —> | 有 |
| 回环浏览器加载提供方 hasDocument 能力，打开配置文件操作 | settings.openDocument, hasDocument | 有 |
| 「通用」分区没有内置行 | —> | 自陈无 |

### `client/ui-settings-models`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 模型设置与产品引导插件 | llm.providers, settings.describe, credentials.describe | 有 |
| 提供方行是已配置的，整分节提供方初次运行会展开设置卡片 | profile, credentials.set | 有 |
| 获取可用模型针对表单当前显示的端点调用 llm.discoverModels | llm.discoverModels | 有 |
| 密钥通过 credentials 领域写入 | credentials.set | 有 |
| 卡片上可编辑的只有 API 密钥与精选折叠区字段 | baseURL, models, displayName, api | 自陈无 |
| 凭据清理范围刻意保持狭窄 | —> | 自陈无 |
| 只有 pi-ai 路由可以手工声明 | llm-pi-ai | 自陈无 |
| 询问只覆盖 OpenAI 兼容端点 | —> | 自陈无 |
| 未声明的存活路由无处渲染 | —> | 自陈无 |

### `client/ui-settings-plugin-inventory`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Web 设置中的只读插件列表标签页 | ctx.remote.pluginInventory.list | 有 |
| 以可搜索的双列紧凑折叠卡片展示清单 | settings.plugins.tab | 有 |
| 每次 Settings 挂载或重试只读取一份快照 | —> | 自陈无 |
| 只读 Loader 视图 | —> | 自陈无 |

### `client/ui-settings-plugins`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 插件设置分区及其插件配置标签页 | settings.plugins.tab, settings.plugin.item | 有 |
| 为每个配置由用户拥有的 Host 插件展示可展开卡片 | —> | 有 |
| 本包自带卡片覆盖 bash、agent-loop 与 web-search-deepseek | bash, agent-loop, web-search-deepseek | 有 |
| 卡片暂存用户输入，只有用户保存时才写入 | settings.mutate | 有 |
| 只有宿主平面的插件会出现，agent preset 挂载的插件无法注册 | —> | 自陈无 |

### `client/ui-sidebar`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 侧边栏外壳插件：品牌行、New Session 操作、折叠控件、可感知滚动的区域 seat | sidebar.brand.mark, sidebar.brand.name | 有 |
| New Session 启动运行时的页面局部前端 Session Intent | startSession | 有 |
| 实时收起淡出展开内容，四个上方控件与底部 settings 控件进入 56px 轨道 | —> | 有 |
| 栏内滚动条是指针可供性，指针不在栏内时重新绑定为 transparent | transparent | 有 |
| Session 状态点渲染由 ui-workspace 持有 | —> | 自陈无 |
| Workspace 浏览行为由组合持有 | —> | 自陈无 |

### `client/ui-skill`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| skill 调用 source 的浏览器端，/skill source 注册进 ctx.inputTriggers | skill.list, ctx.inputTriggers | 有 |
| 普通会话候选来自 skill.list RPC，可继续 subagent 解析为没有候选 | —> | 有 |
| 浏览器插件把 skill wire 名称注册进 ui-tool 的 keyed tool.call.toolview slot | tool.call.toolview | 有 |
| 仅含工具结果的 history 页使用通用行 | —> | 自陈无 |
| 文本是唯一依据，没有 occurrence 身份或提示词协议上的结构化引用载荷 | —> | 自陈无 |

### `client/ui-slots`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Slot 注册表纯核心、slot 终端设计 | SlotMap, SlotCore, register | 有 |
| SlotCore 强制执行加载时验证，条目 disposer 递归移除子 slot | declaration epoch | 有 |
| isLive 会线性扫描所有记录 | —> | 自陈无 |

### `client/ui-subagent`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Web subagent 功能 owner：向 conversation.session.header.lineage 贡献谱系导航 | ctx.inputTriggers, @session | 有 |
| 页头谱系 renderer 通过 useSessions 钩子读取 subagentsByParent | subagentsByParent | 有 |
| 选择任意深度的条目调用 SessionRuntime.openSubagent | SessionRuntime.openSubagent | 有 |
| one-shot child 始终选用只读编辑器，可继续 child 按 parent 可用性选择 | subagent.prompt, subagent.interrupt | 有 |
| @session 引用保持独立且惰性，候选是运行中 child | —> | 有 |
| 目录没有持久化结果 | —> | 自陈无 |
| @引用仍是显示标题文本，重复或改名后会有歧义 | —> | 自陈无 |

### `client/ui-theme`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 主题插件：基于 --dsw-* token 的 ThemeRuntime | ctx.theme, ThemeRuntime | 有 |
| 回环浏览器先以 system 立即提供服务，随后加载 ui-theme.preference | ui-theme.preference | 有 |
| 推送 settings 变更时或重连后重新拉取设置 | settings/document-updated, connection/reset | 有 |
| 五张样式表由动态客户端 entry 依次导入，客户端 bundle 注入为全局样式 | base.css, design-platform.css, scrollbar.css | 有 |
| 滚动条重新绑定约定：--dsh-scrollbar-thumb 与 --dsh-scrollbar-thumb-hover 绑定 token | --dsh-scrollbar-thumb, --dsh-scrollbar-width | 有 |
| 第三方主题是表层，不是产品 | —> | 自陈无 |
| token 样式表是颜色值的唯一权威来源 | —> | 自陈无 |

### `client/ui-tool`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Client 工具展示插件，负责 root 及其 Code Dispatch 子调用展示 | tool-call, conversation.chat.node | 有 |
| 业务 UI 包只注册 wire 工具名称和原子视图 | tool.call.toolview, ToolCallOwnerProps | 有 |
| ToolCallTree 接收已包含递归 subCalls 的 root，让 root 与 child 经同一原子分发 | ToolCallBlock.subCalls | 有 |
| 本包通过 ToolDetails 填充 conversation.details.tool | conversation.details.tool | 有 |
| 通用行把已知工具名称归类为 search、read、shell、write 等变体 | —> | 有 |
| Host 不把 run_code 暴露为 Code Mode 程序 binding | —> | 自陈无 |

### `client/ui-trajectory`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Trajectory 渲染按轮次组织的事件记录表 | ChatFlow, ConversationNodeDefinition | 有 |
| 较粗分割线标示轮次边界，紧凑行内标记标识步骤 | —> | 有 |
| 长记录表定位于尾部，用户到达已加载范围顶部时加载更早历史 | —> | 有 |
| 选择、时间线导航、折叠、搜索仅覆盖已加载窗口 | —> | 有 |
| Overview 区域从左到右投影记录的开始时间与耗时 | —> | 有 |
| 进行中时 Time 保持空白 | —> | 自陈无 |

### `client/ui-user-questions`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Web 提问功能插件，为会话拥有的 conversation.composer slot 贡献 question 条目 | dsh-tool-ask-user | 有 |
| 组件每次渲染一个问题，提供进度导航、单选/多选选项、自定义答案 | ASK_CANCELLED | 有 |
| 若某个请求的唯一问题声明了呈现意图，改为渲染该意图自己的界面 | plan-review | 有 |
| Approve 与 Refuse 用提问方自己的选项标签回答 | —> | 有 |
| 编辑器外框文案是双语的 | dsh-client-locale, question | 有 |
| 未提交的草稿不持久 | —> | 自陈无 |
| 每次只有一个请求拥有编辑器 | —> | 自陈无 |

### `client/ui-workflow-run`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 浏览器插件把持久化的顶层工作流运行重建为独立 Chat 节点 | tool-workflow/*, conversation.chat.node | 有 |
| ConversationNodeDefinition 消费四类 tool-workflow/* 事件 | tool-workflow/run-start | 有 |
| 运行和每个阶段在所有状态下都是受控 disclosure | —> | 有 |
| 成员打开子 Session 需要同时满足多个实时条件 | —> | 有 |
| 只有经 dsh-tool-workflow 发起的顶层调用会生成这些记录 | —> | 自陈无 |
| 导航刻意只面向实时运行 | —> | 自陈无 |

### `client/ui-workspace`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 共享 Workspace 浏览器与选择器插件 | WorkspaceBrowser, WorkspacePicker | 有 |
| WorkspaceBrowser 填充 sidebar.workspaces slot，通过全局运行时钩子渲染 Session | sidebar.workspaces, useWorkspaces | 有 |
| 每个 Workspace 记住自身是关闭还是显示 Session | WorkspaceView.sessionIds | 有 |
| 视图选项把分组方式与浏览器持久化 Session 顺序放在一起 | —> | 有 |
| 手动排序和最近更新两种模式都可用 | —> | 有 |
| WorkspacePicker 通过全局 useWorkspaces hook 列出 Host Workspace 实体 | useWorkspaces | 有 |
| 每个注册声明一个目录流子 slot | conversation.hero.workspace.directoryFlow, sidebar.workspaces.directoryFlow | 有 |
| Workspace 行内的 Delete 操作打开确认框，成功后分组移除且 Session 留在 Ungrouped | —> | 有 |
| Session 行内的 Rename 操作打开对话框预填当前显示标题 | —> | 有 |
| Session 行内的 Archive 操作不经确认对话框直接提交 | ctx.workspaces.archiveSession | 有 |
| Session 行渲染运行时的实时 pendingInteraction 分类 | pendingInteraction | 有 |
| Workspace 和 Session 悬浮卡片会复制被截断的值 | —> | 有 |
| Session 行内的 fork 操作在源会话最后一个已完成轮次处 fork | SessionRuntime.openSubagent | 有 |
| 没有模糊内容搜索或事件深链接 | —> | 自陈无 |
| 没有 Session 删除与取消归档控件 | —> | 自陈无 |
| 待处理的用户交互不会聚合到折叠的分组上 | —> | 自陈无 |
| 原生文件夹选择依赖本地 Host 载体 | —> | 自陈无 |

### `client/web`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Web 启动内核：分两个阶段挂载客户端 | new AppWebEntry(el).run | 有 |
| 模块阶段调用 window.__ModuleLoader__.create，插件阶段挂载 Cordis Loader | window.__ModuleLoader__, dsh.client.external | 有 |
| 启动页只使用原生 DOM 与本地 CSS | —> | 有 |
| PLATFORM_MODULES 是外壳播种共享模块的唯一事实来源 | PLATFORM_MODULES, PRELOADED_CLIENT_EXTERNALS | 有 |
| 可选覆盖参数 seams 为外部脚本转发 loadBundle 传输覆盖 | BootSeams, __DSH_TRANSPORT__ | 有 |
| 应用会等待完整名册，只要一个 entry 失败就保留启动页 | —> | 自陈无 |

### `extensions/cordis-client-runner`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 浏览器半运行：订阅cordis/request-run等Host事件，闭包求值，装入loader | 闭包运行 | 有 |
| Guard门面：apply收到白名单代理，生命周期动词加自己inject声明的服务 | ctx代理 | 有 |
| Loader条目：加guard的插件塞进模块表，经loader.create挂载 | loader.create | 有 |
| Run编排：按顺序host半→取源码→浏览器半，最后回答带结果 | 编排流程 | 有 |
| 包内RPC：host.call经dynamicCordisRunner Remote转给host半 | host.call | 有 |
| 渲染期失败回流：supervision接缝对entry边界崩溃通知，分两出口上行host和发布到本包 | reportRenderFailure | 有 |
| 已知限制：被拒绝的回答不会重试 | resolveRequestRun的ack不读 | 自陈无 |

### `extensions/cordis-host-runner`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 定义与运行分离：define仅登记元数据，run/runHostHalf/getClientCode处理执行 | define / run / stop / undefine | 有 |
| 存活定义注册表：define对元数据校验与语法预检，undefine先停掉再忘掉 | 进程内存注册表 | 有 |
| Run往返机制：run emit cordis/request-run，挂起直到某页面允许/拒绝或被取消 | cordis/request-run | 有 |
| 存储立场：注册表仅在进程内存，重启后无定义，id无法解析的卡片会说明 | 进程内存唯一真源 | 有 |
| 信任立场：vm沙箱隔离全局变量，但不是安全边界，重定向到Cordis服务 | vm沙箱 | 有 |
| 转发事件：cordis/request-run/resolved、dynamicCordisRunner/package/retract由本包声明 | 转发事件 | 有 |
| 已知限制：run成功不等于UI渲染成功 | React渲染发生在run返回之后 | 自陈无 |
| 已知限制：带浏览器半的包在无页面连接处挂起 | headless与ACP部署无页面 | 自陈无 |
| 已知限制：挂起的run请求没有超时 | 一直等人直到提问轮次被取消 | 自陈无 |
| 已知限制：vmTimeoutMs只约束同步求值 | async的host半函数体会逃出上限 | 自陈无 |

### `extensions/tool-cordis`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Cordis工具集：cordis_inspect/cordis_define/cordis_run/cordis_stop/cordis_undefine五个工具 | 工具schema | 有 |
| 只读报告：cordis_inspect返回运行时报告，包括服务/fiber/工具/动态包等 | cordis_inspect | 有 |
| 动态包生命周期：定义/运行/停止/卸载，每个动词按会话为界 | 工具集动词 | 有 |
| 已知限制：沙箱不是安全边界 | 可访问host realm helper | 自陈无 |
| 已知限制：ctx facade不公开effect() | 无定制disposer注册 | 自陈无 |

### `extensions/ui-cordis`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 全局面板：shell.overlay条目，列出每个定义及其运行控件 | 面板UI | 有 |
| 只读卡片：cordis_define调用的卡片记录定义名/用途/源码/运行状态 | 卡片记录 | 有 |
| 已知限制：已展开面板看不到无下发公告的变化 | cordis_define和非运行状态undefine无公告 | 自陈无 |
| 已知限制：只有请求、没有清单的行可应答但不可操作 | 需要注册表读取的行 | 自陈无 |
| 已知限制：若编排跑在读取之前，该行会消失一次读取的时长 | 读取延迟 | 自陈无 |

### `hooks/hook-protocol`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 共享原语库：matcher验证/运行/合并、hook执行/输出解码、脱离运行追踪等 | matcherDiagnostic / runHook / parseHookOutput等 | 有 |
| Matcher校验与匹配：claude mode区分字面量/正则，codex mode始终正则 | 两种mode | 有 |
| Hook执行：runHook通过ctx.shell执行，支持timeout和AbortSignal | runHook | 有 |
| 输出解码：parseHookOutput解码退出码与结构化stdout | exitCode / HookOutput | 有 |
| 脱离运行追踪：createDetachedRuns跟踪emit形式脱离运行，drain()排空 | createDetachedRuns / drain | 有 |
| Hook会话事件：hook/invoked和hook/result会话事件，按handlerId配对 | hook/invoked / hook/result | 有 |
| 已知限制：HookOutput.updatedInput会被解析但不会应用 | 输入改写暂缓 | 自陈无 |

### `hooks/hooks-claude-code`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Claude Code方言：映射CC command hook子集的兼容路径 | hook点映射 | 有 |
| 配置发现：configPath指定hooks.json或settings文件的hooks key，可选pluginRoot/projectDir | configPath / pluginRoot / projectDir | 有 |
| Hook点映射：SessionStart/UserPromptSubmit/PreToolUse/PostToolUse/Stop/SubagentStart/SubagentStop → Harness点 | 6个映射 | 有 |
| 上下文源标记：注入上下文携带显式{ kind: plugin, plugin: hooks-claude-code } | 来源归因 | 有 |
| 已知限制：不支持23个hook事件 | Setup等不支持 | 自陈无 |
| 已知限制：SessionStart只支持部分功能 | 脱离运行、无纯stdout等 | 自陈无 |

### `hooks/hooks-codex`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| Codex方言：映射Codex hook子集的兼容路径，仅支持5个hook点 | hook点映射 | 有 |
| 配置发现：configPath指定.codex/hooks.json，可选model字段 | configPath / model | 有 |
| 仅正则matcher：没有字面量快速路径 | regex-only | 有 |
| Snake_case payload：stderr payload携带turn_id/model字段，不带尾随换行符 | snake_case stdin | 有 |
| 已知限制：不支持5个hook事件 | PermissionRequest等不支持 | 自陈无 |
| 已知限制：仅支持5个hook点 | PreToolUse/PostToolUse/SessionStart/UserPromptSubmit/Stop | 自陈无 |

### `host/apiproxy`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 支持约定层协议：客户端请求（ClientRequest）/ 服务器响应（ServerResponse）/ 服务器请求（ServerRequest）/ 客户端响应（ClientResponse），RPC方法通过RpcMethodMap注册 | 约定层协议 | 有 |
| 会话模型选择：读取并保存模型选择（provider/model/reasoningEffort），三级解析（本进程选择→会话日志→组合默认值） | session.selectModel / session.models | 有 |
| 会话历史导出（含子代理）：GET /api/session.export流式返回ZIP，包含会话原始工件、子代理及媒体资源 | GET /api/session.export / HEAD /api/session.export | 有 |
| 会话历史分页查询：session.history按追加消息分页，最后一页包含投影块（ctx.sessionProjections快照） | session.history / session/projection | 有 |
| 会话投影管理：ctx.sessionProjections注册单元，网关订阅变更流并生成session/projection mux帧 | ctx.sessionProjections / session/projection | 有 |
| 会话标题管理：session.rename接受用户显式标题，返回规范化标题及事件seq | session.rename / session/title | 有 |
| 会话分支（fork）：session.fork将可选事件锚点映射到该锚点或后续的首个turn/end | session.fork | 有 |
| 待处理消息队列管理：session.updateQueue按MessageId寻址项目，编辑/移除通过Agent.Inbox.splice | session.updateQueue | 有 |
| 会话取消：session.cancel仅中止活动轮次，保留待处理inbox工作 | session.cancel | 有 |
| 后台任务投影：当ctx.jobs存在时，网关订阅并广播session/jobs快照和每会话订阅baseline | ctx.jobs / session/jobs | 有 |
| Workspace管理：workspace.create接受规范目录，workspace.insertBefore/delete处理注册表顺序 | workspace.create / workspace.insertBefore / workspace.delete | 有 |
| 会话归档：workspace.archiveSession添加到全局归档集合，workspace.list包含该集合作为重连baseline | workspace.archiveSession / host/archived-sessions-changed | 有 |
| 会话搜索投影：session.search在可见会话范围内进行有界内容搜索，最多返回20个可见会话/snippet对 | session.search | 有 |
| Agent预设管理：agentPreset.list/select/read/copy/openDocument/remove管理组装 | agentPreset.list / agentPreset.select / agentPreset.read / agentPreset.copy / agentPreset.openDocument / agentPreset.remove | 有 |
| 命令执行与列表：command.list返回会话Agent的命令列表，command.execute在宿主侧运行斜杠命令行 | command.list / command.execute / commands/change | 有 |
| 技能列表：skill.list返回用户可调用的skill及modelInvocable标志 | skill.list | 有 |
| 设置管理：settings.describe/update/replace/mutate处理分节配置，支持脱敏与版本冲突检查 | settings.describe / settings.update / settings.replace / settings.mutate / settings/document-updated | 有 |
| 凭据管理：credentials.describe/set/unset管理身份验证凭据 | credentials.describe / credentials.set / credentials.unset / credentials/reference-updated | 有 |
| LLM提供方与模型发现：llm.providers返回可配置提供方与存活路由，llm.models返回模型目录，llm.discoverModels询问端点 | llm.providers / llm.models / llm.discoverModels / llm/adapters-updated | 有 |
| 目录选择能力委托：host.pickDirectory/listDirectory/createDirectory根据ctx.directoryPicker的能力类型调用 | host.pickDirectory / host.listDirectory / host.createDirectory | 有 |
| 文件打开：host.openPath用操作系统默认应用打开路径，宣告能力canOpenPath | host.openPath / host.describe.canOpenPath | 有 |
| 家目录获取：host.describe.home返回宿主账户家目录 | host.describe.home | 有 |
| 响应验证：首个回答认领前校验问题响应，拒绝标签重复/未知/id不匹配/批次不完整/自定义文本为空 | RPC方法入口校验 | 有 |
| 已知限制：转发的Remote事件寄居在legacy帧联合里 | host/remote-event | 自陈无 |
| 已知限制：待处理交互状态位于宿主侧 | POST /api/respond | 自陈无 |
| 已知限制：待回答提问无法跨宿主重启存活 | events.mux重放待处理提问 | 自陈无 |
| 已知限制：预留seam不进入RpcMethodMap | prompt.mode: inject等 | 自陈无 |
| 已知限制：冷列表提示只向"保持可见、排序偏旧"降级 | coldBlankProbeMaxBytes | 自陈无 |

### `host/directory-picker`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 能力seam抽象：DirectoryPicker服务返回可辨识联合（native/browse） | ctx.directoryPicker / capability() | 有 |
| 后端变体可扩展性：DirectoryPickerCapabilities通过可合并扩展添加新后端 | DirectoryPickerCapabilities | 有 |
| 已知限制：不支持多根目录 | 浏览约定每次列举只公开一条祖先链 | 自陈无 |

### `host/directory-picker-auto`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 自适应选择器：启动时判定宿主处境，挂载native或browse后端 | resolveDirectoryPickerBackend | 有 |
| native资格判定：需要回环绑定、非SSH启动、带有显示会话 | SSH_CONNECTION/DISPLAY/WAYLAND_DISPLAY | 有 |
| 已知限制：探测是从启动上下文推断位置，无法证明实际操作者位置 | SSH_*标记缺失、Aqua外会话 | 自陈无 |
| 已知限制：Linux选择器探查只读PATH | zenity/kdialog可用性 | 自陈无 |
| 已知限制：仅在启动时判定 | 无按客户端自适应 | 自陈无 |

### `host/directory-picker-browse`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 应用内浏览后端：BrowseDirectoryPicker以browse能力注册，提供列举与创建 | host.listDirectory / host.createDirectory | 有 |
| 列举行为：只返回目录，按名称排序，跟随符号链接，携带hidden标志 | DirectoryListing | 有 |
| 创建目录：不递归，校验名称为单个非空白段，拒绝非完全限定路径 | host.createDirectory | 有 |
| 有界列举：maxEntries上限（默认1000），层级以流式方式经过有界窗口 | maxEntries / truncated | 有 |
| 双面包：浏览器侧提供选择工作区目录对话框（Miller双列视图） | ./client UI组件 | 有 |
| 已知限制：不读取Windows隐藏属性 | hidden仅表示点前缀 | 自陈无 |
| 已知限制：不枚举盘符根 | 祖先链止于盘符根 | 自陈无 |
| 已知限制：全盘可浏览 | 没有按部署限定的浏览根 | 自陈无 |

### `host/directory-picker-native`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 原生OS选择器后端：NativeDirectoryPicker以native能力注册，pick(signal)打开选择器 | host.pickDirectory | 有 |
| 平台工具：macOS使用osascript，Linux使用Zenity/KDialog回退，Windows使用IFileOpenDialog | 平台特定实现 | 有 |
| 中止信号转发：AbortSignal会终止原生进程 | AbortSignal处理 | 有 |
| 双面包：浏览器端注册无渲染流程占用者，驱动host.pickDirectory | ./client 流程注册 | 有 |
| 已知限制：Linux依赖桌面工具 | Zenity/KDialog未安装时失败 | 自陈无 |
| 已知限制：Windows没有机制级回退 | COM拒绝或对话框崩溃直接上报 | 自陈无 |

### `host/frontend-static`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| SPA dist服务器：占据webserver唯一回退席位，通过显式index入口服务已构建前端 | distIndex配置 | 有 |
| Index渲染管道：先结构化注入行，后原始index转换器 | renderIndex / collectIndexInjections | 有 |
| MIME类型支持：精简表覆盖Vite输出资产及实际交付的PWA manifest | MIME类型表 | 有 |
| 已知限制：初始MIME表很精简 | 其他扩展名回退到application/octet-stream | 自陈无 |
| 已知限制：pathname路由是显式的 | 当前无History API pathname路由 | 自陈无 |

### `host/plugin-inventory`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 只读Host投影：PluginInventoryGateway注册pluginInventory服务，发布pluginInventory/list Remote | ctx.loader.entries() | 有 |
| 快照内容：返回Loader条目id/模块标识/启用状态/根Fiber阶段 | pending/loading/active/failed/unloading | 有 |
| 已知限制：仅表示调用当下 | 无持久失败历史或订阅 | 自陈无 |
| 已知限制：无来源与修改能力 | 服务不识别插件来源，无启用/停用/添加/移除接口 | 自陈无 |

### `host/webserver`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| HTTP与upgrade路由注册：register(route)添加具名exact/prefix路由，registerUpgrade(route)添加精确pathname | ctx.webServer.register / registerUpgrade | 有 |
| 回退处理器：registerFallback(handler)处理未被具名路由命中的请求，第二次注册报错 | ctx.webServer.registerFallback | 有 |
| Index渲染管道：collectIndexInjections收集行，renderIndex先结构化渲染后应用原始转换 | collectIndexInjections / renderIndex / tapIndex | 有 |
| 路由匹配顺序：先精确路由，再最长前缀，最后fallback handler，upgrade只做精确匹配 | 固定匹配顺序 | 有 |
| 端口与绑定：port读取监听端口（0时读OS分配值），host读取配置绑定宿主 | ctx.webServer.port / host | 有 |
| 错误处理：监听失败从激活过程抛出，请求处理错误响应400或销毁socket，upgrade错误记warning | 错误处理约定 | 有 |
| 资源释放：启动close()与closeAllConnections()，销毁upgrade socket后返回 | 资源释放流程 | 有 |
| 已知限制：不提供TLS、认证或来源策略 | 绑定非回环需自行加固 | 自陈无 |
| 已知限制：Socket选项固定不变 | 配置仅选择绑定宿主与端口 | 自陈无 |

### `sdk/client`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| TypeScript SDK客户端：子进程方式驱动Harness运行时，走stdio JSON-RPC | DeepSeekHarness / HarnessClient | 有 |
| 高层API：DeepSeekHarness自有运行API，支持run(input)获取最终响应 | run / close | 有 |
| 低层API：HarnessClient协议客户端，显式start/initialize/prompt/request/close | 协议原语 | 有 |
| 通知订阅：subscribe(filter?)返回NotificationSubscription，subscribeSessionTree(id)限定会话范围 | subscribe / subscribeSessionTree | 有 |
| 错误类型：JsonRpcResponseError/RequestTimeoutError/SdkProtocolError/TransportClosedError | 错误种类 | 有 |
| 优雅关闭：close()先请求shutdown再走stdin-EOF→SIGTERM→SIGKILL阶梯 | 关闭序列 | 有 |
| 已知限制：无捆绑运行时解析 | 调用方显式指定运行时 | 自陈无 |
| 已知限制：无轮次中取消 | 放弃意味着关闭运行时 | 自陈无 |
| 已知限制：没有逐提示词结果或取消 | prompt仅返回入队回执 | 自陈无 |

### `sdk/protocol`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| JSON-RPC协议格式：按换行分帧的JSON-RPC 2.0传输，加上协议两端共用的具名类型 | JsonRpcLineTransport | 有 |
| 协议方法：initialize/session/prompt/shutdown客户端→服务器，session.event/status/subagent.*通知服务器→客户端 | 协议方法表 | 有 |
| 错误响应：缺失请求处理器应答-32601，处理器拒绝应答-32603 | JSON-RPC错误码 | 有 |
| 已知限制：无协议版本协商 | 握手仅携带serverInfo.version | 自陈无 |
| 已知限制：无取消与会话关闭方法 | 放弃的方式是关闭运行时 | 自陈无 |
| 已知限制：server→client请求未使用 | 传输层支持但服务器不发送 | 自陈无 |

### `sdk/server`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| JSON-RPC服务器插件：通过stdio提供JSON-RPC使进程外SDK客户端驱动harness | HarnessSdkJsonRpcServer | 有 |
| 协议响应：initialize等待插件树加载完成，session/prompt排入队列并返回messageId，shutdown刷新并exit | 协议响应约定 | 有 |
| 事件流：session.event流式发出每个会话事件，session.status发出整个agent的running/idle转换 | 事件通知 | 有 |
| 已知限制：协议没有逐会话关闭或提示词取消方法 | SDK创建的agent一直存活 | 自陈无 |
| 已知限制：没有逐提示词结果 | MessageId仅标识inbox准入 | 自陈无 |
| 已知限制：stdout纯净性由部署保证 | 外围配置仍可能加载stdout logger | 自陈无 |
| 已知限制：自动挂载适配器仅支持DeepSeek | 唯一的回退是dsh-llm-deepseek | 自陈无 |

## G. 测试脚手架与示例

9 个包，105 条，其中有 88、自陈无 17。

### `examples/acp-demo`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 挂载 dsh-agent-spine-demo 作为不含提供方的 agent 主干 | 主干挂载 | 有 |
| 挂载 dsh-session-persistence-jsonl 提供检查点、可观测性和快照回放 | JSONL 持久化 | 有 |
| 挂载 dsh-session-checkpoint-policy 在模型调用和顶层工具 effect 前建立持久性屏障 | 检查点策略 | 有 |
| 挂载 dsh-session-query-sqlite 提供派生的精确／FTS 会话查询服务 | SQLite 查询服务 | 有 |
| 挂载 dsh-acp 通过 stdin/stdout 提供纯自动化 ACP 传输 | ACP 传输 | 有 |
| 配置 provider 和 model 参数指定每个由 ACP 创建的 agent 所用的提供方和模型 | 提供方和模型配置 | 有 |
| 配置 maxParallelToolCalls 为正整数工具调用并发上限 | 工具并发配置 | 有 |
| 配置 persona 供 dsh-system-prompt 使用的部署 persona 模板 | persona 配置 | 有 |
| 配置 toolOrder 为供 dsh-system-prompt 使用的显式面向模型工具顺序 | 工具顺序配置 | 有 |
| 配置 tools 为 Native、Code Mode 或组合式模型工具传输 | 工具传输配置 | 有 |
| 配置 dshHome 指定 bash 与本地 skill 发现共享的 harness 主目录 | 主目录配置 | 有 |
| 配置 sessionTitle 为持久后备标题限制 | 会话标题配置 | 有 |
| 配置 persistenceRoot 指定 JSONL 后端根目录 | 持久化根目录配置 | 有 |
| 配置 packChunks 控制是否在存储中打包连续增量分片事件 | 分片打包配置 | 有 |
| 配置 persistenceCompression 为 zstd 或原始 none | 持久化压缩配置 | 有 |
| 配置 workspaceContext 指定工作区指令字节预算或关闭 | 工作区上下文配置 | 有 |
| 配置 skills 指定 skill 注册表、本地提供方和模型工具 | skill 配置 | 有 |
| 配置 toolBash 为面向模型的 bash 工具配置或关闭 | bash 工具配置 | 有 |
| 配置 jobs 为进程内按 owner 限制活动任务的准入配置 | 任务限制配置 | 有 |
| 配置 toolJobs 为通用后台任务控制配置或关闭 | 工具任务配置 | 有 |
| 配置 goals 为持久化的同会话目标领域与模型工具或关闭 | 目标配置 | 有 |
| JSONL 持久化固定不变，使用其他后端需要另一种组合 | — | 自陈无 |
| 同级插件可能破坏 stdout，应用无法阻止另一个 Cordis 配置项写入非协议字节 | — | 自陈无 |

### `examples/agent-spine-demo`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 挂载 cordis-plugin-timer 提供 timer 服务 | timer 服务 | 有 |
| 挂载 dsh-llm 提供抽象 LLM 服务和 content-block 词汇 | LLM 服务 | 有 |
| 挂载 dsh-session 提供事件溯源的会话日志与存储 | 会话日志与存储 | 有 |
| 挂载 dsh-session-title 提供日志支持的标题服务与确定性回退 | 会话标题服务 | 有 |
| 挂载 dsh-system-prompt 提供提示词段落与工具 schema 组装 | 系统提示词组装 | 有 |
| 挂载 dsh-tools 提供注册表与受保护的前/环/后/最终结果管道 | 工具管道 | 有 |
| 挂载 dsh-skill 提供 skill 提供方注册表 | skill 注册表 | 有 |
| 挂载 dsh-skill-filesystem 提供本地文件系统 skill 提供方 | 文件系统 skill | 有 |
| 挂载 dsh-agent 提供 agent 注册表、启动器 scope 和 agent 事件 | agent 注册表 | 有 |
| 挂载 dsh-goal 提供可选的持久化同会话目标领域 | 目标领域 | 有 |
| 挂载 dsh-tool-goal 提供可选的模型面向 goal 控制 | goal 工具 | 有 |
| 挂载 dsh-goal-round-driver 提供可选的同会话目标轮次驱动 | 目标轮次驱动 | 有 |
| 挂载 dsh-llm-retry 提供提供方路由的请求重试策略 | 重试策略 | 有 |
| 挂载 dsh-jobs-local 提供通用后台任务注册表 | 任务注册表 | 有 |
| 挂载 dsh-invariants 提供可配置的不变量注册表服务 | 不变量注册表 | 有 |
| 挂载 dsh-session/invariant、dsh-agent/invariant、dsh-scope/invariant、dsh-agent-loop/invariant 配套入口 | 配套入口 | 有 |
| 挂载 dsh-tool-bash 提供面向模型的 bash schema（除非 toolBash=false） | bash 工具 schema | 有 |
| 挂载 dsh-agent-instructions 提供 AGENTS.md 和 CLAUDE.md 工作区上下文加载 | 工作区指令加载 | 有 |
| 挂载 dsh-tool-skill 提供会话前缀 skill 目录和模型面向加载 schema | skill 工具 | 有 |
| 挂载 dsh-tool-jobs 提供 job_output、job_list、job_kill schema 和完成通知 | 任务工具 | 有 |
| 挂载 dsh-agent-loop 提供具体的 loop 并转发 agents 和 persona 配置 | agent loop | 有 |
| 大部分主干集合固定在代码中，要替换循环或删除其他主干成员需组合另一个组合包 | — | 自陈无 |
| 不变式服务与配套插件是固定成员，enabled: false 或包筛选器会抑制检查但不会移除服务 | — | 自陈无 |

### `examples/jsonrpc-demo`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| bin 启动外部 cordis.yml 并通过 jsonrpc 入口为 SDK 客户端提供服务 | bin 启动 | 有 |
| 配置负责组合主干、后端和服务插件 | 配置责任 | 有 |
| 第一个非空通道生效：先 $DSH_CORDIS_CONFIG，再位置参数 argv[2] | 配置发现 | 有 |
| 不含 dsh-sdk-jsonrpc-server 的配置仍然有效但不提供任何服务 | 可选服务 | 有 |
| stdin EOF 和 SIGTERM 会 dispose 根上下文并以 0 退出 | 退出生命周期 | 有 |
| SIGINT 完成同样 dispose 后以 130 退出 | 中断退出 | 有 |
| stdout 只承载 JSON-RPC 帧，诊断输出到 stderr | stdout 协议 | 有 |
| bin 无法证明配置提供 JSON-RPC 服务 | — | 自陈无 |
| 不存在内置或默认配置 | — | 自陈无 |
| stdin EOF 会截断正在处理的工作 | — | 自陈无 |

### `test-support/acp-snapshot`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| launchAcpTestAgent 启动器从指定 cwd 在 tsx 或已构建 Node 下启动源 agent | 启动器 | 有 |
| 通过原始字节 stdout tee 连接 SDK 客户端并收集会话更新和 stderr | 会话收集 | 有 |
| 负责优雅或带信号关闭并等待进程退出与 ACP parser 耗尽 | 关闭管理 | 有 |
| runScenario harness 通过启动器从确定性 input.json 脚本驱动 ACP JSON-RPC stdio | harness | 有 |
| 规范化器 normalizeStdout 将 JSON-RPC id 映射为首次出现序列，UUID 和 cwd 路径进行 tokenize | 标准化 Stdout | 有 |
| normalizeSessionLog 规范化日志并将时间归零 | 标准化会话日志 | 有 |
| normalizeSessionSnapshot 执行会话日志规范化、request header 清理和正文 envelope 投影 | 标准化会话快照 | 有 |
| scrubSystemPrompts、scrubToolSchemas 和 scrubRequestHeaders 去除敏感内容 | 内容去除 | 有 |
| stabilizeFixtureMessageIds 仅改写 surface 和 inbox 中完整消息的 ID 字段 | 消息 ID 稳定化 | 有 |
| defineAcpSnapshotSuite 为场景表注册完整 describe/it 树 | 套件工厂 | 有 |
| 会话收集需要原始 JSONL mode，压缩 JSONL 和 SQLite 组合没有快照收集路径 | — | 自陈无 |
| 构建 mode 需要当前产物，先运行 pnpm run build | — | 自陈无 |

### `test-support/agent-loop-testkit`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| mountAgentLoopTestDependencies 按依赖顺序安装 LLM、会话、系统提示词、工具和 agent 服务 | 依赖挂载函数 | 有 |
| 只共享必需的先决主干，适配器、可选插件、AgentLoop 和清理由调用方负责 | — | 自陈无 |

### `test-support/client-runtime`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 真实 Cordis Context、生产 SlotRegistry 和 UI 渲染器的 jsdom 测试运行时 | 测试运行时 | 有 |
| TestSessions 和 TestWorkspaces 实现生产接口，fixture session 实现 SessionFace | 测试替身 | 有 |
| 提供带类型的 provide() 将 fake 约束为该服务对外面的 Partial 子集 | 类型化 fake 提供 | 有 |
| declare(children) 注册自动 frame，renderSlot(key, owner) 返回局部视图 | 局部 DOM 快照 | 有 |
| mount(plugin) 在真实 fiber 上运行并对缺失服务先行报错 | fiber 插件挂载 | 有 |
| dispose() 沿单一轴拆除视图、feature fiber、已铸 scope 与持久化 store 状态 | 资源释放 | 有 |
| 仅可经仓内源码别名消费，lib/ 再导出无 Node ESM 导出的浏览器 loader 脚本 | — | 自陈无 |

### `test-support/llm-mock-server`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| startMockLlmServer(options) 启动可编脚本的 OpenAI 兼容 HTTP/SSE 服务器 | 启动函数 | 有 |
| 接受 POST /chat/completions 和 POST /v1/chat/completions 请求 | 请求路由 | 有 |
| 支持 connection_reset、stream_disconnect、partial_disconnect 等连接故障行为 | 连接故障行为 | 有 |
| 支持 stall、empty、malformed_json 等协议异常行为 | 协议异常行为 | 有 |
| 支持 rate_limit、server_error、auth_error 等提供方错误行为 | 提供方错误行为 | 有 |
| 支持 success、slow_success、tool_call_success 等正常完成行为 | 正常完成行为 | 有 |
| 支持 random 行为模式按带权重的分布随机选择 | 随机行为模式 | 有 |
| CLI 公开时序与内容控制选项如 --success-text、--partial-text、--chunk-delay-ms | 时序与内容控制 | 有 |
| 随机权重建模测试压力而非生产事故频率 | — | 自陈无 |
| 请求脚本按到达顺序执行，并发调用方共享一个游标 | — | 自陈无 |

### `test-support/llm-replay`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| 根据已记录的会话 JSONL fixture 重建模型流供无密钥快照测试 | 回放机制 | 有 |
| 配置 providers 后注册仅用于回放的适配器 | 回放适配器注册 | 有 |
| 未配置 providers 时安装无需模型发现的 catch-all llm/stream waterfall | catch-all 监听器 | 有 |
| fixture 保留会话事件 payload 但省略 seq/time envelope | fixture 格式 | 有 |
| 压缩摘要器成功时可以在 compaction/summary 的位置重建规范成功流 | 压缩流重建 | 有 |
| 通过伴随文件 replay.override.json 提供派生脚本的替换或补丁 | 脚本覆写 | 有 |
| 支持在脚本字符串中内嵌 {{fromRequest:}} 来填入静态文件不可能预知的值 | 动态占位符替换 | 有 |
| 多个子会话按首次调用顺序绑定到已记录脚本 | 嵌套 agent 脚本绑定 | 有 |
| installLlmReplay(ctx, config) 安装回放适配器并返回包含 dispose 和 assertConsumed 的 ReplayHandle | 回放安装函数 | 有 |
| loadSessionScripts 和 loadReplayScript 解析会话脚本供绑定 | 脚本加载函数 | 有 |
| 首次调用顺序脚本绑定假设串行委托，并发 subagent 会非确定性绑定 | — | 自陈无 |
| 只有普通 loop 分片和带标记的本地压缩输出才能派生，异常和取消需要伴随文件 | — | 自陈无 |

### `test-support/loader-smoke`

| 功能 | API / 导出名 / 配置项 | 有 |
|---|---|---|
| resolveExampleLaunch 选择本地 src 或 CI lib 启动模式 | 启动模式选择 | 有 |
| runLoaderSmoke 接受可执行文件路径和配置路径、环境变量覆盖、标准输入、运行前准备和清理前检查 | 烟雾测试 harness | 有 |
| 负责隔离工作目录、DSH 主目录、诊断、截止时间、终止、EOF 和清理 | 进程隔离管理 | 有 |
| runFixtureTurn 通过已配置的根 agent 驱动一项任务并返回最终 assistant 文本和累计用量 | 单轮驱动 | 有 |
| 构建模式需要事先构建，配置还必须能通过 examples/node_modules 向上解析 | — | 自陈无 |
| 捕获的 stdout 和 stderr 仅受 execa 默认 100 MB maxBuffer 约束 | — | 自陈无 |
