# DSH 227 包能力清单

## 零、这份文档为什么存在

在它之前，`docs/DESIGN.md` 第三、四节已经把 227 个包分成了「进范围 46 / 不进 111 / 未定 26」。
那份裁决的依据是两样东西：**包名**，和 `docs/portmap/portmap.tsv` 里 7909 行机器抽出来的
导出符号（只有名字和签名，没有一行逻辑）。DSH 的源码一共 215,147 行，当时真正读过的
不到 2,000 行。

按包名裁决只在一种情况下站得住：**名字本身就说清了能力**。
`fs-local` / `bash-local` / `sandbox-windows-acl` 属于这一类——名字说了它碰本机文件和本机进程，
服务端不装，理由完整。但「未定」那 26 个不属于这一类，它们是**猜的**。
`subagent` 就是猜错的证据：我按名字判定它是「一次性委派」，读完 README 才发现它有
`startContinuable` / `reportFrom(delivery:'next-step')` / `followup` / `interrupt`，
也就是**子 agent 在父 agent 运行中把反馈推回去并 steering 父 agent**——正是要做的那件事，
现成的，被我按名字划掉了。

所以这份清单要解决的不是「补充材料」，是**换掉裁决的依据**。这件事已经做完：
依据链是 `functions.md`（2009 条功能）→ `required.md`（五条前提推出的必需集）→
`rulings.md`（227 行逐包裁决）→ `DESIGN.md` 第三、四节（已按此重写，恢复效力）。

**这份是一个包一行，粒度不够裁决用。** `core/tools` 那一行底下压着 35 件独立的事，
只看它决定不了「工具那块要哪些」。拆到条的那一层在 `docs/portmap/functions.md`，
2009 条，每条只有「有」和「自陈无」两种取值。这份留作总览。

## 一、方法与口径

**读的是什么。** DSH 的 227 个包每个都带一份作者写的 `README.zh.md`，合计 13,637 行。
体量是源码的 1/16，而内容恰好是裁决需要的那种：这个包干什么、它是接缝还是实现、
它自己承认做不到什么。源码回答「怎么实现的」，README 回答「它是什么」——
裁决问的是后者。

**覆盖率。** 227/227，全量，不是抽样。逐包的行来自八个并行子代理按同一套五列格式产出，
缺的一个（`test-support/loader-smoke`）事后单独补读。行数与包数由脚本对账：

```
包数（packages/<域>/<包>/package.json）   227
README.zh.md                              227
README 中文总行数                          13,637
src 总行数                                215,147
清单行数 / 每行字段数                      227 / 5
```

**五列的含义。**

| 列 | 取值 | 说明 |
|---|---|---|
| 包 | `<域>/<包>` | 与 `portmap.tsv` 的 package 列同名 |
| 能力 | 一句话 | 这个包**是什么**，不是它怎么写的 |
| 性质 | 接缝 / 实现 / 工具 / UI / 脚手架 | 接缝＝只定接口；实现＝往接缝上挂后端；工具＝给模型的函数；UI＝浏览器表层；脚手架＝组装或测试用 |
| 服务端障碍 | 本机文件 / 本机进程 / 需要本机终端 / 桌面UI / 浏览器端 / 仅单进程 / 无 | 服务化多用户场景下装不上的前提 |
| 自陈限制 | 原文摘要 | README 自己写的「已知限制与暂缓事项」 |

**分布（口径不互斥，一个包可占多项）：**

- 性质：实现 101、工具 40、接缝 39、UI 33、脚手架 14
- 服务端障碍为「无」：**104**
- 碰本机文件 / 本机进程 / 需要本机终端：**69**
- 浏览器端 / 桌面 UI：**49**
- 自陈「仅单进程」：**19**

## 二、清单

### A. 循环、会话与持久化（44 个）

**`attachment/`（2）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `attachment` | 持久附件服务边界，规范化并持久提交图片 | 接缝 | 无 | 第一版仅接受PNG、JPEG、WebP和GIF；保留策略与垃圾回收未实现；无通用文件或音频视频支持 |
| `attachment-local` | dsh-attachment本地实现，对象存放在DSH_HOME/attachments | 实现 | 本机文件 | 对象无限期保留；假定宿主与提供方适配器共享文件系统；GIF仅保留首帧；编码器版本升级改变地址 |

**`compaction/`（4）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `command-compact` | 通过/compact命令提供面向用户的手动压缩控制 | 工具 | 无 | 仅限空闲状态；不接受范围或策略参数；仅限命令适配器 |
| `compaction` | Service Definition定义压缩做什么，判定历史过大并摘要为单个表层节点 | 接缝 | 无 | 面向用户命令而非模型工具；部分单元溢出不在约定内；单独接近窗口的envelope不属于压缩工作 |
| `compaction-basic` | 基础压缩后端，使用token压力和摘要器实现压缩 | 实现 | 无 | 计量准确度取决于启发式；溢出分类由适配器维护；部分不可分单元仍不在范围内 |
| `compaction-tool-result-pruner` | 不依赖模型的工具结果剪枝服务，改写超大结果为头部加尾部 | 实现 | 无 | 字符预算非token预算；剪枝仅基于语法；字素簇可能被拆分 |

**`context/`（6）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `agent-instructions` | 为会话加载工作区指令文件（AGENTS.md/CLAUDE.md），在第一步或文件变更时注入 | 实现 | 本机文件 | 发现跟随结构化fs工具而非shell导航；刷新由touch驱动；指令内容受限但不会被摘要 |
| `file-reference` | 文件引用发现seam和@file语法，为UI提供文件补全 | 接缝 | 无 | 路径候选仅供参考；没有文件内容引用对象 |
| `file-reference-local` | ctx.fileReferences的本地文件系统实现，为每个agent维护有界的工作区索引 | 实现 | 本机文件 | 宿主本地命名空间；有界的提示性索引；无忽略文件语义 |
| `session-reference` | 把其他会话作为有界只读快照供模型消费的跨会话上下文 | 实现 | 无 | 不支持消息正文检索；只投影文本；没有实时链接 |
| `time-context` | 可选的持久上下文，包含当前时间、浏览器时区和经过时长 | 实现 | 需要本机终端 | 仅限提示词来源信息；混合轮次会询问；整秒显示 |
| `tmux-context` | 可选的持久上下文，记录agent进程所在的tmux session/window/pane及布局 | 实现 | 需要本机终端 | 仅第一个步骤；仅自身位置；只有布局没有尺寸；制表符分隔字段限制 |

**`core/`（8）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `agent` | 定义agent接口、注册表和事件词汇，驱动程序消费方面向此处定义的Agent编程 | 接缝 | 仅单进程 | 发起方作用域仅在进程内有效；环境身份可能比存活状态更久；委派以外的agent间通道尚不支持 |
| `agent-default-model` | 为新建agent提供部署级的默认模型选择 | 实现 | 无 | 仅拥有一项进程级默认值；未挂载设置提供方时saveSelection无法保留 |
| `agent-loop` | agent唯一具体实现和循环驱动器，驱动会话、轮次和步骤的生命周期 | 实现 | 无 | 分类一元；配置agent无逐agent persona字段；没有内置轮次预算 |
| `agent-tool-presentation` | 声明模型看到的工具呈现方式（native/code/both） | 工具 | 无 | 运行时仍在宿主平面；未组装运行时则无法使用code模式 |
| `scope` | 带作用域的注册原语，为每个存活agent创建一个作用域 | 脚手架 | 仅单进程 | 只有感知作用域的表层才能隔离状态；一个上下文只携带一个最近的作用域键 |
| `session` | 事件溯源的会话日志和内存存储，为agent保留全部交互历史 | 实现 | 无 | 会话分支树结构暂缓；fork仅在实时会话稳定边界处切分；SESSION_FORMAT_VERSION固定为0 |
| `system-prompt` | 系统提示词组装注册表，按作用域收集段落、工具schema和变量 | 实现 | 无 | 部署方提示词文本仅来自配置/组合；无字面花括号转义语法；toolOrder配置错误在首轮出现而非启动时 |
| `tools` | 工具注册表与执行流水线，定义工具schema、执行和呈现方式 | 实现 | 无 | 并发策略不是事件门禁；pre-execute不允许改写arguments；运行时SDK仅支持TypeScript和Python |

**`credentials/`（3）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `authorization` | 授权Service Definition，拥有凭据对话及生命周期 | 接缝 | 桌面UI | flow不可恢复；没有吊销；没有flow的键是惰性 |
| `credentials` | 凭据Service Definition，配置引用与授权记录分离存储 | 接缝 | 无 | 引用不提供枚举；限定为环境变量形状；进程环境变化不可见；无scope验证 |
| `credentials-local` | 文件型凭据提供方，四层来源优先级 | 实现 | 本机文件 | 并发写入同一引用后写胜出；同UID进程可读取；环境变化不可见；原子但无崩溃持久性 |

**`session/`（13）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `session-checkpoint-policy` | 在模型适配器前与工具正文前为事件溯源会话创建检查点，持久化前一响应与工具结果 | 实现 | 无 | 流式分片无逐分片检查点；崩溃可能丢失当前批次或未完成写入；恢复无法证明副作用完成 |
| `session-persistence` | 会话持久化能力接缝，定义会话事件存储、加载与列表接口 | 接缝 | 无 | 无删除或保留接口；list()无分页或过滤；修复时仅合成closer作为崩溃恢复方案 |
| `session-persistence-jsonl` | 仅追加JSONL逻辑日志持久化后端，支持Zstandard压缩与分片打包 | 实现 | 本机文件 | 仅单进程；每会话一个活动writer；POSIX需硬链接支持；压缩文件不能直接按行读取 |
| `session-persistence-sqlite` | SQLite持久化后端，存储打包后的assistant分片与delta编码序列 | 实现 | 本机文件；仅单进程 | 过渡性设计，schema不稳定；同步压缩与繁忙等待阻塞事件循环；无删除或后台压缩 |
| `session-projection` | 会话投影Service Definition与驱动注册表，对已提交事件驱动客户端读模型 | 接缝 | 无 | 每尾页携带每个client-visible key；单元表进程级；注册表cell仅内存；同步纪律部分可机械把关 |
| `session-projection-cache` | 持久投影缓存，把投影单元状态保存为检查点 | 实现 | 无 | 不提供淘汰接口；记录按会话累积；间隔节流粗粒度；冷读不去重 |
| `session-stats` | 折叠会话日志事件为step计数、轮数、LLM时间、工具时间等数字 | 实现 | 无 | 步数统计工作而非可见输出；被取消的步计数但不计时；仅挂载于web-app bundle |
| `session-telemetry` | 遥测Service Definition，捕获会话记录传给上报后端 | 接缝 | 无 | 尽力而为投递，不保证可靠性；不内置脱敏规则；按需脱敏用当前状态 |
| `session-telemetry-otel` | OpenTelemetry遥测后端，支持FULL、FEEDBACK_ONLY、DISABLED三种模式 | 实现 | 无 | 上游实验性源码树；真实collector行为属SDK；反馈时快照不保留副本 |
| `session-title` | 日志支持的会话标题，提供确定性回退与可选异步提供方 | 实现 | 无 | 无删除或搜索功能；提供方注册表最多接受一个实现 |
| `session-title-all-prompts-llm` | 通过LLM总结所有用户消息的会话标题提供方 | 实现 | 无 | 输入溢出时保留先前标题；无基于摘要继续的机制 |
| `session-title-first-prompt-llm` | 通过LLM总结第一条用户消息的会话标题提供方 | 实现 | 无 | 第一条消息对长期会话可能不代表；fork保留标题不自动运行 |
| `session-title-llm` | 模型支持的会话标题提供方共享实现 | 实现 | 无 | 仅接受文本输出，拒绝工具调用；对整体封装强制字节上限 |

**`session-query/`（4）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `session-log-export` | Web会话日志ZIP下载控制 | 工具 | 桌面UI；浏览器端 | 下载端点要求持久化后端具有逐会话原始工件；仅浏览器下载非Host路径写入 |
| `session-query` | 会话查询Service Definition，提供精确读取、关系跟踪与过滤 | 接缝 | 无 | 无调用方授权；无注册表或面向模型工具；持久化列表与检查可能失败 |
| `session-query-sqlite` | SQLite全文搜索会话查询提供方实现 | 实现 | 本机文件 | 无调用方授权；同步查询阻塞事件循环；单一所有者派生索引不支持外部写入 |
| `tool-session-query` | 经工作区授权的会话查询模型工具 | 工具 | 无 | 搜索最多返回部署上限；cwd精确相等无符号链接等价；未挂载spill策略接收完整内联输出 |

**`storage/`（4）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `storage` | 非会话数据存储中心，具名后端注册表加数据形式设施 | 接缝 | 无 | kv是唯一数据形状；数据形式按需解析会明确报错 |
| `storage-domain` | 存储中心领域数据形式，在后端上暴露可注入的storageDomain服务 | 实现 | 无 | 变更仅进程内可见；无跨表事务、二级索引或多段键 |
| `storage-json` | 存储中心JSON后端，每个单元一个JSON文件 | 实现 | 本机文件 | 没有跨进程写锁；Windows持久性依赖libuv rename()无显式write-through |
| `storage-sqlite` | 存储中心SQLite后端，单个数据库提供kv facet | 实现 | 本机文件 | DatabaseSync同步阻塞；无忙等待或重试策略；不迁移其他版本；重复打开逻辑 |

### B. 模型、工具与会话内能力（43 个）

**`feedback/`（2）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `command-feedback` | 会话反馈采集与面向用户的/feedback命令，记录feedback/record事件并披露会话共享状态 | UI | 浏览器端 | 没有反馈检索/管理surface，没有结构化字段，不支持修改撤回，新会话上看不到确认 |
| `message-feedback` | 单条assistant消息反馈存储，提供可编辑rating/note的伴随记录与compare-and-set操作 | 实现 | 本机文件 | 缺客户端UI，compare-and-set仅限单进程，没有持久Session删除级联 |

**`goal/`（4）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `command-goal` | 面向用户的/goal控制，基于ctx.goals实现，提供状态展示和create/edit/pause/resume/clear命令 | 工具 | 无 | 仅纯文本交互无模态编辑；没有逐命令Round上限参数；没有持续状态组件 |
| `goal` | 事件溯源的同会话目标状态，维持当前待完成目标和续行权限 | 接缝 | 无 | 只负责状态不负责任务调度；只有Round数量预算；没有独立评估器；只有一个当前目标 |
| `goal-round-driver` | ctx.goals的同会话续行驱动器，把phase为active且已启用续行的目标转换为连续Goal Round | 实现 | 仅单进程 | 没有独立评估器；只在同一会话执行；已接受队列的卸载存在竞态；只有Round上限 |
| `tool-goal` | ctx.goals的面向模型控制API：get_goal、create_goal和update_goal | 工具 | 无 | 语义意图仍由模型判断；阻塞条件是否相同仍由模型判断；Goal Round权限需要驱动器 |

**`guard/`（2）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `repeat-tool-reminder` | 循环中断器，监视连续重复工具调用并注入逐级增强的提醒 | 实现 | 无 | 仅检测精确匹配；压缩不重置链；仅提供建议非强制；subagent间不共享链 |
| `timeout-policy` | 工具调用超时强制执行器，读取工具声明的timeoutMs并在超时时返回TOOL_TIMEOUT | 实现 | 无 | 协作式而非硬终止；没有统一预算；未声明工具无默认值 |

**`interaction/`（5）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `commands` | 由插件注册的用户命令注册表，供交互式UI适配器使用 | 接缝 | 无 | 仅支持非结构化文本输入；副作用采用协作式取消 |
| `permission-presets` | 通过ctx.permissionPresets提供面向用户的权限预设组合沙箱与审批 | 实现 | 无 | 只组合两个可调参数；custom仅可推导不可选中；预设表是进程级配置；已存储默认值必须保留在表中 |
| `tool-ask-user` | 模型侧ask_user_question工具，基于ctx.userQuestions实现 | 实现 | 桌面UI | 待处理问题阻塞工具调用；运行时subagent不能向用户提问；Native回答渲染为JSON文本 |
| `user-approval` | 与通道无关的一次性审批seam，request返回allowed-once/rejected/cancelled/unavailable | 接缝 | 无 | 请求仅在尚未结束的轮次有效；仅存在一次性授权；请求不携带工具参数；没有内置应答者 |
| `user-questions` | 用户交互Service Definition，定义ctx.userQuestions与提供方注册 | 接缝 | 无 | 每个上下文仅一个提供方；词汇仅含问题表单形态 |

**`jobs/`（3）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `jobs` | 后台任务注册表约定，为长时间运行的生产方提供共享id、owner隔离、读取、取消、等待、通知和清理 | 接缝 | 无 | 流输出只有一个消费游标；前台工作无法转为后台；约定是进程内的 |
| `jobs-local` | ctx.jobs注册表约定的进程本地实现，把每条记录保存在内存中并按kind签发id | 实现 | 仅单进程 | 任务只存在于进程本地；静默无效的取消可能使销毁过程停滞 |
| `tool-jobs` | ctx.jobs的面向模型控制器，提供job_output、job_list和job_kill三个与kind无关的工具 | 工具 | 无 | 落在driver退休窗口内的结算仍会让通知搁浅；已花掉的唤醒预算不会随时间恢复 |

**`llm/`（5）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `llm` | 提供方无关的 LLM 词汇与抽象，注册适配器、捕获重试策略、支持模型发现与流式调用 | 实现 | 无 | 本服务不执行重试、缓存或速率限制；采样字段仅含temperature/maxTokens/stop；BlockAssembler仅处理核心块类型 |
| `llm-deepseek` | DeepSeek chat-completions 适配器，直接fetch+SSE转换为StreamChunk，支持图片文件API与思考模式 | 实现 | 本机文件 | settings的models列表整体替换；请求使用原始fetch而非proxy；会跳过插件添加的内容块类型 |
| `llm-pi-ai` | 基于@earendil-works/pi-ai的多提供方通用适配器，支持OpenAI兼容端点与私有网关 | 实现 | 无 | maxRequestImageBytes仅统计base64图片载荷；一次登录仅存活于发起进程；settings能新增或覆盖但不能移除路由 |
| `llm-retry` | 通过agent/request-error事件应用提供方重试策略，支持normal与always两种模式 | 实现 | 无 | agent轮次是唯一重试边界；always mode会重试永久性失败；恢复策略按waterfall顺序组合 |
| `token-meter` | 通过单例ctx.tokenMeter进行回放感知的token测量与上下文占用率投影 | 实现 | 无 | 固定启发式规则是近似值；每次测量克隆表层；提供方用量仅复用完全相同的规范envelope |

**`mcp/`（1）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `mcp-client` | MCP客户端桥接插件，连接外部MCP服务器并把工具注册到ctx.tools | 实现 | 本机进程 | 只桥接工具能力；启动超时继承自MCP SDK；重连仅在传输关闭时触发；图片是唯一持久丰富结果桥接 |

**`plan/`（1）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `plan-mode` | 软引导的plan协作状态，提供/plan命令、/plan off命令和经用户评审的exit_plan_mode退出方式 | 实现 | 桌面UI | Plan mode只进行引导不强制执行；如果进程在另一个轮内pre-step前退出会丢失选择 |

**`preset/`（2）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `agent-presets` | 按preset组装agent，工具和提示词仅存在一份供所有已加入agent使用 | 实现 | 本机文件 | 可写根目录外的preset无法删除；会话产出后无法更换preset；代际不会回收；副本是漂移快照 |
| `persona` | 可组装的agent人设，可遮蔽部署级人设或成为完整系统提示词 | 脚手架 | 无 | 不支持全局挂载；本行只能从带scope的组装中使用 |

**`schedule/`（1）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `schedule` | 为未来创建的live根agent提供三个会话范围内的工具管理持久提醒 | 实现 | 无 | 仅限会话本地交付；活动驱动的重试；显式本地时区无导入；固定间隔而非日历规则 |

**`settings/`（2）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `settings` | 用户设置Service Definition，管理按namespace分节的schema默认值、组合base与用户层解析 | 接缝 | 无 | 单一用户层，redactSecrets并非证明可靠的协议边界，跨进程并发由提供方定义 |
| `settings-file` | 基于YAML/JSON文件的settings提供方，支持外部编辑热发布、并发写锁、YAML注释保留 | 实现 | 本机文件 | 冲突仍后写胜出，watcher漏报需下次事件触发，注释保留仅限YAML，无值间接引用 |

**`skill/`（4）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `skill` | 纯agent skill提供方注册表，Service Definition与策略框架 | 接缝 | 无 | 失效由提供方驱动；提供方依次查询；不保留不完整观测；重名项裁决采用先到先得 |
| `skill-badge` | 可选内置skill提供方dsh-badge，官方markdown片段与PNG | 实现 | 无 | 提供方仅贡献一个固定skill；远程markdown使用Shields.io |
| `skill-filesystem` | ctx.skills的本地文件系统提供方，扫描SKILL.md与平铺Markdown文件 | 实现 | 本机文件 | 发现深度为一层；项目范围为最近.git祖先；格式错误条目随警告消失；缺失根观察每次轮询一段 |
| `tool-skill` | 面向模型的skill目录与skill工具，展示可用skill并加载完整指令 | 实现 | 本机文件 | 目录省略whenToUse与来源；正文无大小上限；资源是指引而非附件；加载是一次性文本 |

**`spill/`（3）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `spill` | spill能力Service Definition，定义后端存储过大工具文本并返回定位信息与取回指引 | 接缝 | 无 | 没有取回/删除API，存储不等于访问控制 |
| `spill-local` | 本地文件系统spill实现，将工具结果保存到会话级私有文件，定位信息是文件路径 | 实现 | 本机文件 | 文件持续存在直到外部清理，定位信息需与消费方位于同一文件系统 |
| `spill-policy` | 工具结果spill策略，对过大纯文本结果执行spill并替换为有界预览与取回指引 | 实现 | 无 | 只能对最终纯文本结果执行spill，通知无法容纳时该次调用替换禁用 |

**`todo/`（1）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `tool-todo` | 面向模型的todo_write工具，agent的完整任务列表每次调用整体替换 | 工具 | 无 | 仅单一所有者scope；条目形状最小；整表替换是唯一操作无部分更新 |

**`web/`（6）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `tool-web` | 面向模型的web_search与web_fetch工具，基于ctx.web能力seam | 实现 | 无 | 没有覆盖整个批次的搜索计数器；HTML转markdown会在深层嵌套上降级；面向模型接口保持精简 |
| `web` | Web访问能力seam的Service Definition，定义WebRuntime接口与提供方注册 | 接缝 | 无 | 没有观测接口；WebSearchRequest仅含query+maxResults；WebFetchBody没有pdf分支 |
| `web-fetch-http` | 匿名公共HTTP(S)抓取提供方，URL验证与内容兜底超时 | 实现 | 本机文件 | SSRF/私有网络防护暂缓；只解码文本内容；charset仅来自Content-Type标头 |
| `web-search-deepseek` | DeepSeek Anthropic兼容Messages API搜索提供方，触发原生web_search服务器工具 | 实现 | 无 | 一次搜索需要完整Messages轮次；动态凭据可用性在操作内部解析；超量返回的源仍消耗token |
| `web-search-exa` | Exa搜索端点提供方，返回高亮摘要映射为规范结果 | 实现 | 无 | 无非空高亮摘要的结果被丢弃；只公开searchType/numResults/highlightsPerResult；按错误形状分类中止 |
| `web-search-perplexity` | Perplexity OpenAI兼容搜索提供方，生成答案与引用映射 | 实现 | 无 | 引用回退源仅含URL；超量返回的来源仍增加token；只公开model/maxTokens/searchRecency |

**`workspace/`（1）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `workspace` | Workspace实体注册表，持久化workspace记录、顺序、会话归属索引，支持创建/删除/排序/归档操作 | 实现 | 本机文件 | 会话删除与破坏性文件夹移除尚未提供，头部索引仅启动时刷新 |

### C. 多 agent 编排（17 个）

**`experimental/`（2）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `agent-team` | 隐式Root Agent Teams领域，维护Lead/teammate roster、持久peer mailbox与共享任务DAG | 实现 | 仅单进程 | 单进程共享checkout；write scope仅作提示；扁平且不可变的roster；mailbox不保证跨进程exactly-once |
| `tool-agent-team` | ctx.agentTeams的scoped模型适配器，在每个隐式Lead和持久teammate scope安装协作工具 | 工具 | 无 | 提示词策略只负责协调不负责confinement；不会自主创建Team；没有Web控制功能 |

**`subagent/`（11）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `subagent` | 提供子agent运行的接缝抽象，管理provider注册、启动、持久化子agent描述符及可继续子级编排 | 接缝 | 无 | — |
| `subagent-acp` | 通过Agent Client Protocol在全新子进程运行subagent，并作为ACP客户端驱动它 | 实现 | 本机进程、需要本机终端 | 远程会话无法映射；不支持可选启用时能力 |
| `subagent-claude-code` | 在发起委托的会话工作区中调用官方Claude Agent SDK运行subagent任务 | 实现 | 本机进程、需要本机终端 | 每次运行新建query和进程；不支持续接、恢复、池化；不支持可选的启动时能力 |
| `subagent-codex` | 在全新临时Codex线程中启动subagent，通过app-server --stdio与Codex通信 | 实现 | 本机进程、需要本机终端 | 每次运行新建进程、线程和轮次；不支持可选启动时能力；必须存在平台载荷 |
| `subagent-dsh-sdk` | 通过TypeScript SDK客户端在全新子进程运行完整DeepSeek Harness运行时作为subagent | 实现 | 本机进程、需要本机终端 | 每次运行新建运行时进程；不支持可选启动时能力；子进程transcript保留在其自身会话根目录 |
| `subagent-fork-in-process` | 在当前进程创建子agent，以父agent已完成的对话轮次作为初始内容 | 实现 | 仅单进程 | fork子agent保留在一次性模式，不支持可继续路径；初始内容只是快照，不会实时共享 |
| `subagent-in-process-driver` | 两个进程内provider共用的运行驱动器，处理深度、创建、定制、结果读取、取消和dispose | 实现 | 仅单进程 | 运行不公开sendMessage/resume；结构化捕获只接受defineTool schema子集 |
| `subagent-spawn-in-process` | 在当前进程创建全新子agent，有自己的会话但以空对话开始运行 | 实现 | 仅单进程 | 全新表示不含父agent transcript；子agent默认继承模型但看不到父agent对话 |
| `tool-subagent` | 基于已配置provider的面向模型委派工具，前台或后台执行subagent任务 | 工具 | 无 | 后台运行不通过本工具公开结果；等待中的一次性实例较晚才发现重复名称；每个实例的子agent策略固定 |
| `tool-subagent-control` | 全局具名send_message、interrupt_agent与list_agents工具，控制可继续subagent的生命周期 | 工具 | 无 | 已排队消息没有独立结果；不对当前轮次进行steering；列表是快照非投递承诺；没有分页或删除 |
| `tool-subagent-report` | 可选的子级作用域report工具，为可继续进程内子级提供向父agent的返回通道 | 工具 | 无 | 父级可能在dispose开始后继续接受报告；接受弱于持久投递；暂存的静默报告无法立即重建 |

**`workflow/`（4）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `tool-ralph` | 面向模型的ralph工具，运行固定的前台工作流把目标依次交给多个全新子agent | 工具 | 无 | 完成由worker自行声明无独立评估；仅支持前台；普通子agent失败会终止运行 |
| `tool-workflow` | 面向模型的workflow工具，运行扇出subagent的JavaScript编排脚本并返回脚本最终值 | 工具 | 无 | 父级轮次会阻塞到整个工作流结算；args必须是对象；持久记录只覆盖顶层 |
| `workflow` | 工作流seam定义脚本、运行、结果、错误和事件契约，worker-thread是当前引擎实现 | 接缝 | 无 | 仅支持前台收集；没有日志化或恢复；没有已保存或嵌套工作流；没有token预算词汇 |
| `workflow-worker-thread` | WorkflowEngine实现，每次运行使用Node worker thread隔离脚本执行 | 实现 | 仅单进程 | worker/vm不是安全边界；每次运行支付worker thread成本；终止只能报告宿主观察到的启动 |

### D. 目录、命令与沙箱（碰本机资源的那一支）（35 个）

**`code-runtime/`（3）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `code-runtime` | 代码运行时 Service Definition，定义异步绑定执行模型编写的程序 | 接缝 | 无 | — |
| `code-runtime-python` | CPython 子进程代码运行时实现，通过 fd 3 JSON-lines 协议交互 | 实现 | 本机进程 | 跨语言 guard 覆盖运行时执行面与帧字段形状、本包不含子进程执行路径 |
| `code-runtime-worker-thread` | Node worker 线程代码运行时实现，支持 TypeScript 并提供隔离与预算 | 实现 | 仅单进程 | 程序派生 OS 进程后续存活、类型剥离依赖 Node 实验性 API、默认 64 MiB 是拒绝边界 |

**`e2b/`（3）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `e2b` | 为 agent 提供远程 Linux 沙箱执行环境，托管文件系统与进程生命周期 | 接缝 | 本机进程、桌面UI、浏览器端 | 不是完整运行时、沙箱状态短暂、无宿主同步、无部署配置、`cwd` 仅是解析约定 |
| `fs-e2b` | 实现在 E2B 沙箱上运行的文件系统操作适配器 | 接缝 | 本机进程 | 不提供宿主同步、变更协调仅限宿主进程、读取会重新打开规范化目标、仍需承担完整文件变更成本、仅面向 E2B 默认镜像 |
| `subprocess-e2b` | 在 E2B 沙箱中执行子进程和 PTY 的提供方 | 接缝 | 本机进程 | SDK 在宿主内存中保留完整输出、不支持需要同步 PID 的消费方、私有状态随沙箱生命周期、控制状态与沙箱用户同 UID、无法精确检查终端 stdin 等待、依赖 Linux 工具与 E2B 传输 |

**`fs/`（7）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `fs` | 定义执行世界的存储原语，抽象路径解析、读取、流式读取、列出、原子写入和字面量编辑操作 | 接缝 | 无 | — |
| `fs-local` | 实现本地文件系统的文件系统接口，支持宿主文件系统的十二个存储原语 | 实现 | 本机文件 | 版本 token 依赖文件系统元数据，editText 整体缓冲文件及编辑副本 |
| `fs-observation-policy` | 记录已观察状态，为写入编辑添加防护并通过事件门禁参与文件系统策略 | 工具 | 无 | 已观察状态无法在会话恢复后保留 |
| `fs-sandbox` | 强制沙箱围栏的文件系统后端，按调用策略限制写入操作 | 实现 | 本机文件、需要本机终端 | 策略围栏而非内核边界，TOCTOU 由重新规范化缩小 |
| `tool-fs` | 面向模型的文件系统工具（read、read_image、write、edit），通过事件门禁支持读取窗口和策略 | 工具 | 本机文件 | 未交付目录列表工具、read 仅处理 UTF-8 文本文件 |
| `tool-fs-search` | 基于 ripgrep 的文件发现工具（glob、grep），通过 subprocess 执行 | 工具 | 本机进程 | 搜索与文件访问没有共享工作区证明、启用采样时仅按搜索根第一段路径分组 |
| `tool-str-replace-editor` | 基于文件系统的编辑器工具（view、create、str_replace、insert），面向模型 | 工具 | 本机文件 | 操作面向 UTF-8 文本、str_replace 刻意拒绝零匹配或多匹配 |

**`lsp/`（3）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `lsp` | LSP 能力 seam，抽象四种语义代码导航操作 | 接缝 | 无 | — |
| `lsp-stdio` | 通用 stdio 语言服务器后端，惰性启动和池化服务器进程 | 实现 | 本机文件、本机进程 | 不提供隔离策略、临时打开兼容性下限、被强制杀死 harness 遗留语言服务器 |
| `tool-lsp` | 面向模型的 LSP 工具，通过四种导航操作执行代码定位 | 工具 | 本机文件、本机进程 | UTF-16 光标坐标模型难以计数、不承诺跨服务器完整性 |

**`sandbox/`（4）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `sandbox` | 进程沙箱 Service Definition，定义限制词汇和运行时拒绝机制 | 接缝 | 无 | — |
| `sandbox-local` | 本地沙箱实现，选择平台 runner（bwrap、Landlock、Seatbelt、ACL） | 实现 | 本机进程、仅单进程 | Windows ACL 部分强制执行、Landlock 可能部分强制、runner 选择缓存于提供方生命周期 |
| `sandbox-policy` | 沙箱策略解析的唯一归属位置，管理部署默认和会话覆盖 | 脚手架 | 无 | — |
| `sandbox-windows-acl` | Windows 写入限制沙箱后端，基于受限令牌和 ACL 实现隔离 | 实现 | 本机进程、仅单进程 | 每个工作区一个写入白名单、清理按设计尽力而为、常驻工作区 ACE 不可见残留 |

**`shell/`（10）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `bash-local` | 本地 bash 执行器，通过 subprocess 提供前台和后台 shell 执行能力 | 实现 | 本机进程 | 自身不提供隔离、没有持久 shell 或 PTY、仅支持 POSIX |
| `bash-sandbox` | 沙箱消费型 bash 执行器，通过 sandbox 限制每次 spawn 并报告拒绝 | 实现 | 本机进程、仅单进程 | 限制只覆盖文件影响、拒绝从 stderr 推断、异步 runner 失败无即时错误通道 |
| `pwsh-local` | 本地 PowerShell 执行器，通过 subprocess 提供前台和后台 shell 执行能力 | 实现 | 本机进程 | 自身不设沙箱、无持久 shell 或 PTY、编码 preamble 位于命令之前 |
| `pwsh-sandbox` | 沙箱消费型 PowerShell 执行器，通过 sandbox 限制每次 spawn 并报告拒绝 | 实现 | 本机进程、仅单进程 | Windows 上读不受限、workspace-write 临时权限按会话/工作区对私有、read-only 不授予显式可写根 |
| `shell` | 定义 shell 执行器接口，不规定如何实现前台命令和后台进程 | 接缝 | 无 | — |
| `shell-env` | 工具无关的 shell 环境插件，管理受信任的 DSH_* 变量供 shell 工具使用 | 脚手架 | 无 | list() 只枚举 contributor 声明变量，不包括内置键 |
| `tool-bash` | 面向模型的 bash 工具，支持前台运行、后台启动和沙箱升权 | 工具 | 本机进程 | 回放退出状态从结果文本解析、bash 工具不采用 timeout-policy 预算 |
| `tool-bash-persistent` | 持久 shell 工具，复用按 agent 隔离的 ctx.terminals shell | 工具 | 需要本机终端 | 工具需要拥有 agent 和真实 PTY 后端、交互式前台子进程等待阻塞 |
| `tool-pwsh` | 面向模型的 pwsh 工具，支持前台运行、后台启动和沙箱升权 | 工具 | 本机进程 | Windows 沙箱下语言模式与 named-pipe 捕获受限、无持久 shell |
| `tool-pwsh-persistent` | 持久 PowerShell 工具，复用按 agent 隔离的 ctx.terminals shell | 工具 | 需要本机终端 | 工具需要拥有 agent 和支持 pwsh 的 terminal backend、输入回显不可避免 |

**`subprocess/`（2）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `subprocess` | 子进程 seam 定义，抽象可执行文件查找、spawn 和终端进程原语 | 接缝 | 无 | — |
| `subprocess-local` | 本地子进程运行时，实现 detached 进程树、按流处置和终止升级 | 实现 | 本机进程 | Windows 进程树支持尽力而为、被强制杀死的 harness 遗留语言服务器、凭据清除依赖名称启发式 |

**`terminal/`（3）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `terminal` | 限定所有者范围的持久 PTY seam，通过具名后端路由终端操作 | 接缝 | 需要本机终端 | — |
| `terminal-bash` | 为 ctx.terminals 提供的持久 shell 后端，在共享沙箱策略下启动交互式 shell | 实现 | 需要本机终端 | 输出按行规范化、Windows 没有精确 stdin-wait 档、pwsh 引导在 read-only 下可能失败 |
| `tool-terminal` | 面向模型的终端工具（terminal_open、terminal_send、terminal_read 等），支持后台模式 | 工具 | 需要本机终端 | 不公开具名按键序列、TUI、BEL、调整大小、自动启动或跨 agent 共享 |

### E. 宿主、前端与对外协议（66 个）

**`acp/`（1）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `acp` | Agent Client Protocol仅自动化JSON-RPC服务器，通过stdio驱动harness agent | 实现 | 本机进程、仅单进程 | 仅新会话；仅光栅图片和一个workspace；仅已提交答案；生命周期由连接管理 |

**`api/`（2）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `gateway` | Typert RPC endpoint提供Host侧ctx.typertGateway与Client侧ctx.remote服务 | 实现 | 无 | Connection分发普通失败为internal code；SRC模式仅支持名称唯一标识符；Client侧仅挂载严格模式 |
| `remotes` | 双侧BFF为Host Remote能力提供客户端外观，包含Goal Remote与插件清单 | 实现 | 无 | 能力集由构建时导入固定确定；要增加能力必须显式导入；剩余BFF配置迁移前仍从旧API Proxy提供 |

**`boot/`（2）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `app-boot` | 供多个bin共用的启动粘合层，提供配置路径解析、环境加载、Profile机制、Loader结算与启动失败处理 | 工具 | 需要本机终端 | 裸包specifier依赖Loader内部机制，快照回放替换仅识别特定basename |
| `cmdline` | dsh启动器交给引导应用的命令行参数接口，提供参数快照读取与退出请求 | 接缝 | 无 | 启动器flag必须写在应用参数之前，用户patch会替换整个config |

**`bundle/`（3）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `base` | 共享dsh核心profile组合包，插入全部基础插件（适配器、持久化、策略、settings等），作为所有profile的第一层 | 脚手架 | 仅单进程 | patch替换整行config，Windows临时目录授权是会话私有子目录 |
| `headless` | 一次性任务组合包，提供编码persona和工具模式，禁用HMR，仅创建一个新Agent执行任务后退出 | 脚手架 | 仅单进程 | 只提交单个任务，ctx.appExit由启动器持有 |
| `web-app` | 浏览器表层组合包，挂载Web宿主行、客户端插件、前端dist服务和web-runtime粘合，支持浏览器打开与URL打印 | 脚手架 | 本机进程、浏览器端 | 前端dist必须已构建，lanAddresses是启动期快照，SSH转发持有浏览器URL |

**`client/`（40）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `connection` | 协议消费层，挂载ctx.connection并管理客户端与主机间的HTTP/WebSocket通信 | 接缝 | 浏览器端 | history恢复未附加会话；/api桥缓冲整体请求体 |
| `hmr` | 为通过脚本加载的客户端插件提供热重载 | 实现 | 无 | 重载保持粗粒度，失败不回滚，重建帧不刷新图rev |
| `locale` | 偏好设置以locale.preference存储，支持中英文切换及本地化文案注册 | 实现 | 无 | 部分界面仍保留内联文案；注册表文本只读取一次翻译 |
| `modules` | 浏览器端ESM loader实现，管理插件bundle的懒加载和依赖解析 | 实现 | 无 | 采用扁平模块图；自身不维护卸载记录 |
| `runtime` | 客户端cordis启动与对象服务，拥有Session/Workspace列表和运行时投影 | 实现 | 无 | loader.unload是stub；scope拆卸由阶段驱动仅单占用者；插件导入需用/client子路径 |
| `ui-agent-preset` | agent preset的各表层，包括选择、标签、管理分区和复制对话框 | UI | 本机文件、桌面UI | 没有元数据的preset按id列出；展示路径是文本非链接；组装编辑对页面不可见 |
| `ui-attachment` | 对话UI的附件呈现，支持输入框草稿图片栏、拖放和灯箱 | UI | 浏览器端 | 仅支持图片；灯箱无缩放与下载；灯箱不锁定焦点 |
| `ui-brand-official` | 仅当构建为official时填充sidebar.brand和conversation.hero.brand | UI | 浏览器端 | 仅提供一组occupant；浏览器标题相互独立 |
| `ui-commands` | 客户端命令API，提供/命令source与派发，支持popupSelect和带参claim | UI | 浏览器端 | 脱离会话后detached result的notice回退到console |
| `ui-conversation` | 会话领域骨架、聊天视图、编辑器与输入区，支持压缩、消息、待处理交互 | UI | 浏览器端 | 统计行回退折算只覆盖窗口内；详情面板没有入口；assistant逐消息分页预留slot |
| `ui-deliverables` | 产出文件与可点击文件引用属主 | UI | 本机文件、浏览器端 | 提及匹配只认精确路径或唯一basename；终端命令创建的文件不在词表；原生文件夹交接需要本机或配置 |
| `ui-directory-picker-browse` | 应用内目录浏览界面，提供浏览式选取交互 | UI | 浏览器端 | 无搜索、无多选、无重命名；隐藏条目过滤在客户端 |
| `ui-directory-picker-native` | 原生目录选择界面，调用Host操作系统选择框 | UI | 本机进程、桌面UI | 无法取消已打开的选择框；仅限本地Host载体 |
| `ui-goal` | Goal界面插件，条带与投影goal状态 | UI | 浏览器端 | 只反映持久phase，无实时activation通道 |
| `ui-input-trigger` | 输入触发流水线，支持/与@检测、分组菜单和pick路由 | UI | 浏览器端 | 只有全局source层；icon以文本渲染；overlay合并与所有权分离 |
| `ui-jobs` | Web后台任务展示，列出运行中和已完成的任务 | UI | 浏览器端 | 行是只读的；列表不等于注册表自己的集合 |
| `ui-layout` | 外壳插件，提供三栏AppFrame和主题呈现器 | UI | 浏览器端 | 面板几何是瞬时状态；让步链自动关闭不改动宽度；挤压重排不提供滚动锚定 |
| `ui-message-feedback` | 单条消息反馈插件，提供Like/Dislike和备注 | UI | 浏览器端 | 备注大小由Host策略；无跨标签页推送；仅限对话视图 |
| `ui-model-selection` | 模型选择插件，两个入口共用会话级目录 | UI | 浏览器端 | 无创建期或已寻址subagent选择；目录名仅供呈现；不能任意输入推理强度 |
| `ui-permission-presets` | 权限界面，包括设置行和/permission命令装饰 | UI | 浏览器端 | Settings行仅在Web中可用 |
| `ui-plan` | Plan mode状态徽章和控制插件 | UI | 浏览器端 | Plan mode是引导非执行沙箱；chip属于默认编辑器；无未激活态plan控件 |
| `ui-primitives` | 纯React原子组件，包括按钮、菜单、Toast、Markdown和终端卡片 | UI | 浏览器端 | 流式期间跨边界引用解析被推迟；字形图标是重绘版本；TerminalBlock不是模拟器 |
| `ui-reference` | 统一的Web@file与@session source | UI | 浏览器端 | 候选失败有意保持静默；浏览器侧不扫描文件；会话搜索仅使用元数据 |
| `ui-renderer` | 负责React渲染层，安装slot渲染器并hydrate启动DOM | UI | 浏览器端 | 首帧等待全部客户端entry；slot渲染无Suspense集成或逐entry懒加载 |
| `ui-settings` | 设置领域底座，提供ctx.settingsScope和slot声明 | 实现 | 浏览器端 | 远程浏览器没有持久化设置；每次写入仅一个字段 |
| `ui-settings-general` | 设置外壳、无特定功能文案与引导namespace | UI | 浏览器端 | 通用分区没有内置行 |
| `ui-settings-models` | 模型设置与产品引导插件，提供Models页面和首次运行步骤 | UI | 浏览器端 | 卡片可编辑仅限API密钥与精选字段；凭据清理范围刻意狭窄；只有pi-ai路由可手工声明 |
| `ui-settings-plugin-inventory` | Web设置中的只读插件列表标签页 | UI | 浏览器端 | 每次Settings挂载只读一份快照；只读Loader视图 |
| `ui-settings-plugins` | 插件设置分区及其配置标签页，为用户插件展示可编辑卡片 | UI | 浏览器端 | 只有宿主平面的插件出现；卡片需要浏览器bundle；被服务命名空间只两种信号重读 |
| `ui-sidebar` | 侧边栏外壳插件，负责品牌行、New Session操作和Settings seat | UI | 浏览器端 | Session状态点渲染由ui-workspace持有；Workspace行为由组合持有；未读标记是本地查看状态 |
| `ui-skill` | skill调用source的浏览器端，支持/name触发和工具行展示 | UI | 浏览器端 | 仅含工具结果的历史页使用通用行；文本是唯一依据；预热落定前的菜单无候选 |
| `ui-slots` | Slot注册表纯核心，提供SlotCore和四share组件props类型 | 实现 | 无 | isLive会线性扫描所有记录；__renders幻象锚点在PropsRenderSlots可见 |
| `ui-subagent` | Web subagent功能属主，提供页头谱系导航和编辑器行为 | UI | 浏览器端 | 目录没有持久化结果；@引用仍是显示标题文本 |
| `ui-theme` | 主题插件，基于--dsw-token的实时主题与持久化偏好 | 实现 | 浏览器端 | 第三方主题是表层非产品；token样式表是唯一权威来源 |
| `ui-tool` | Client工具展示插件，渲染root和子调用的keyed slot分发 | UI | 浏览器端 | Host不把run_code暴露为Code Mode程序binding；第一方工具集中本包可迁移 |
| `ui-trajectory` | Trajectory渲染按轮次组织的事件记录表 | UI | 浏览器端 | 进行中时Time保持空白；无锚点深链接 |
| `ui-user-questions` | Web提问功能插件，渲染问题卡片和答案选择 | UI | 浏览器端 | 未提交草稿不持久；每次只有一个请求拥有编辑器 |
| `ui-workflow-run` | 将持久化顶层工作流运行重建为独立Chat节点 | UI | 浏览器端 | 只有顶层工作流调用生成记录；导航仅面向实时运行；节点不显示脚本和操作 |
| `ui-workspace` | 共享Workspace浏览器与选择器插件，支持添加、重命名、搜索 | UI | 本机文件、浏览器端 | 没有模糊内容搜索或事件深链接；没有Session删除；待处理交互不聚合到折叠分组 |
| `web` | Web启动内核，分两阶段挂载客户端 | 脚手架 | 浏览器端 | 应用等待完整名册 |

**`extensions/`（4）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `cordis-client-runner` | 动态双半插件的浏览器半执行器，负责评估浏览器端闭包源码、装载为活插件、处理运行编排与RPC回调 | 实现 | 浏览器端 | 被拒绝的回答不会重试，槽位准入没有载体，guard白名单是手抄孪生 |
| `cordis-host-runner` | 动态包在host侧的注册表与沙箱执行器，管理定义生命周期、vm求值、浏览器调度与invoke路由 | 实现 | 本机进程 | run成功不等于UI渲染成功，带浏览器半的包在无页面时挂起，run无超时 |
| `tool-cordis` | 五个面向模型的自引用工具（cordis_inspect/define/run/stop/undefine），操作当前DSH进程中的实时Cordis运行时 | 工具 | 本机进程 | 沙箱只约束诚实代码非安全边界，ctx façade不公开effect() |
| `ui-cordis` | Cordis动态插件浏览器界面，全局浮窗面板显示所有定义及其运行控件，记录model调用的define卡片 | UI | 浏览器端 | 展开期间看不到无下发的注册表变化，只有请求时该行会消失，渲染失败是本页读数 |

**`hooks/`（3）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `hook-protocol` | hook协议格式的共享核心库，提供方言无关原语（matcher、codec、hook执行、输出合并、会话事件） | 接缝 | 无 | HookOutput.updatedInput被解析但不应用 |
| `hooks-claude-code` | Claude Code hook配置的方言桥接，在规范拦截点上运行CC command hook子集 | 实现 | 本机进程 | 尚未进行每会话配置发现，23项CC hook事件中仅6项支持 |
| `hooks-codex` | Codex hook配置的方言桥接，在规范拦截点上运行Codex 5项hook点的支持子集 | 实现 | 本机进程 | 仅支持5项Codex hook，无工具前审批/改写，配置仅进程级 |

**`host/`（8）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `apiproxy` | 提供共用API网关协议与HTTP载体，使客户端与服务器间通过JSON-RPC模式通信，处理会话请求/响应、流式事件、文件导出等 | 接缝 | 浏览器端 | 待处理交互无法跨宿主重启存活，搜索失败暴露提供方诊断信息，Linux原生选择器依赖桌面工具 |
| `directory-picker` | 定义目录选择能力接缝，为web宿主抽象原生OS选择器与应用内浏览两种交互方式 | 接缝 | 无 | 不支持多根目录 |
| `directory-picker-auto` | 启动时自适应判定宿主处境并挂载匹配的目录选择后端（原生或浏览） | 实现 | 桌面UI、浏览器端 | 探测仅在启动时执行，无法按连接自适应；SSH脱离会丢失标记 |
| `directory-picker-browse` | 应用内浏览目录选择后端，提供跨平台的单层目录列举、创建与面包屑导航交互 | 实现 | 无 | 不读取Windows隐藏属性，不枚举盘符根，全盘可浏览无限制 |
| `directory-picker-native` | 原生OS选择器后端，打开平台对话框让用户选择目录，支持macOS/Windows/Linux | 实现 | 桌面UI | Linux依赖Zenity/KDialog，Windows没有机制级回退 |
| `frontend-static` | Web壳的SPA静态文件服务器，通过显式index入口返回构建好的前端dist，占据webserver回退席位 | 实现 | 浏览器端 | MIME表精简，pathname路由需显式声明 |
| `plugin-inventory` | 当前Cordis Loader树的只读投影，直接读取Loader条目并通过Remote发布清单视图 | 实现 | 无 | 仅表示调用当下，无修改能力 |
| `webserver` | Web HTTP与upgrade route注册插件，启动node:http服务器并管理具名路由表，提供fallback处理器席位 | 实现 | 本机进程 | 不提供TLS/认证/来源策略，Socket选项固定 |

**`sdk/`（3）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `client` | TypeScript客户端SDK以子进程驱动harness，stdio JSON-RPC通讯 | 工具 | 本机进程 | 无捆绑运行时解析；无轮次中取消；没有逐提示词结果或取消；通知与请求未实现 |
| `protocol` | DeepSeek Harness SDK运行时共享协议格式，换行分帧JSON-RPC | 接缝 | 无 | 无协议版本协商；无取消与会话关闭方法；server→client请求未使用 |
| `server` | stdio JSON-RPC服务器插件使进程外SDK客户端驱动harness agent | 实现 | 本机进程 | 协议无逐会话关闭或提示词取消；无逐提示词结果；stdout纯净性由部署保证；自动挂载仅支持DeepSeek |

### F. 底座与工具库（13 个）

**`identity/`（1）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `anonymous-user-id` | 遥测、反馈确认与DeepSeek提供方共用的匿名身份，按harness home生成UUID v4并持久化 | 工具 | 无 | 删除后无法恢复，best-effort并发，没有跨home身份，已配置gateway会收到该id |

**`runtime-diagnostics/`（1）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `invariants` | 用于包自有运行时不变量检查的可配置注册表服务 | 实现 | 无 | — |

**`typert/`（4）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `generator` | TypeScript项目分析器和模型驱动的Typert代码生成器 | 脚手架 | 无 | 系统跳过包导出中的模式匹配；跨face链接与命名空间重新导出存在限制 |
| `loader` | 生成的Typert产物的Loader集成，发现并注册贡献项 | 脚手架 | 无 | 发现机制仅导入宿主侧产物；嵌套插件需显式配置 |
| `protocol` | 不依赖编译器的Remote服务声明与协议映射 | 接缝 | 无 | 装饰器标记仅包含方法名和调用模式；参数与schema反射需要Typert构建流水线 |
| `registry` | 生成的Typert产物运行时注册表，存储业务反射信息和Zod schema | 实现 | 无 | 注册表不合并宿主侧与客户端侧的图；同名schema会作为重复项拒绝 |

**`util/`（7）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `atomic-write` | 零依赖原子文件替换，用于设置与凭据存储 | 工具 | 本机文件 | 原子但不保证持久；仅支持字符串内容；遗留锁需要操作者恢复 |
| `brand` | 仅类型的Branded<B>名义类型原语，跨包id不可互换 | 工具 | 无 | — |
| `home-paths` | DeepSeek Harness用户数据共享文件系统路径辅助工具 | 工具 | 本机文件 | 展开范围保持狭窄；规范化仅读不修改 |
| `launch-environment` | 本次运行环境冻结为不可变快照，记住每个值来自哪一层 | 工具 | 本机文件 | 快照不是子进程边界；没有按工作区划分的层 |
| `native-command` | 零依赖免shell execFile运行器，直接spawn可执行文件 | 工具 | 本机进程、桌面UI | 不做输出限量 |
| `output-retention` | 轻依赖保留库，为工具提供有界面向模型输出 | 工具 | 无 | 项保留仅支持head；文本保留面向字节 |
| `timeout` | 零依赖超时时序与分类纯函数库 | 工具 | 无 | 仅发出通知；timeoutMs<=0是内部词汇；第一个中止原因决定分类；空闲watchdog不是总deadline |

### G. 测试脚手架与示例（9 个）

**`examples/`（3）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `acp-demo` | 通过 JSON-RPC stdio 提供 ACP 自动化服务器应用，支持会话管理和语义检查点 | 脚手架 | 无 | JSONL 持久化固定不变、同级插件可能破坏 stdout、只支持新建自动化会话 |
| `agent-spine-demo` | 最小 agent 主干组合包，包含 LLM、会话、提示词、工具、skill、loop 等固定服务集 | 脚手架 | 无 | 大部分主干集合固定在代码、不变式服务与配套插件仍是固定成员 |
| `jsonrpc-demo` | 只包含 bin 的应用，通过 JSON-RPC stdio 为 SDK 客户端启动外部 cordis.yml 配置 | 脚手架 | 无 | bin 无法证明配置提供服务、不存在默认配置、stdin EOF 会截断正在处理的工作 |

**`test-support/`（6）**

| 包 | 能力 | 性质 | 服务端障碍 | 自陈限制 |
|---|---|---|---|---|
| `acp-snapshot` | 为 ACP 快照测试提供无密钥层、规范化器、启动器和测试套件工厂 | 工具 | 无 | JSONL 持久化固定不变、构建 mode 需要当前产物、后端覆盖仍使用 ACP 驱动器 |
| `agent-loop-testkit` | 共享挂载 agent loop 测试的先决依赖（LLM、会话、提示词、工具、agent） | 工具 | 无 | 只共享必需的先决主干，adapter/插件/loop/agent/cleanup 由调用方负责 |
| `client-runtime` | 为客户端 UI 功能测试提供 jsdom 运行时，含真实 Cordis Context 和生产 slot 注册表 | 工具 | 无 | 仅可经仓内源码别名消费、会话快照是 fixture 数据不是重放历史 |
| `llm-mock-server` | 可编脚本的 OpenAI 兼容 HTTP 服务器，无需密钥即可测试 LLM 适配器和 agent 循环 | 工具 | 无 | 随机权重建模测试压力而非生产频率、按到达顺序执行脚本、真实连接拒绝仅在监听器生命周期阶段发生 |
| `llm-replay` | 从已记录会话日志回放 LLM 模型流，使快照测试无需 API 密钥 | 工具 | 无 | 首次调用顺序脚本绑定假设串行委托、只有普通 loop 分片和带标记压缩输出能派生 |
| `loader-smoke` | 通过 Cordis Loader 与 cordis.yml 启动应用的共享子进程冒烟 harness，runFixtureTurn 驱动一次完整任务并返回最终文本与用量 | 脚手架 | 本机进程 | 构建模式需事先构建；stdout/stderr 受 execa 100MB maxBuffer 约束；超时只终止直接子进程 |

## 三、第四列不可单独作为裁决依据

第二列（能力）和第五列（自陈限制）是从 README 正文摘的，可核对。
**第四列（服务端障碍）是子代理的判断，不是原文，有噪声。** 抽了三行去核 README 原文，三行全错：

| 包 | 清单里写的 | README 原文 | 实际 |
|---|---|---|---|
| `e2b/e2b` | 本机进程、桌面UI、浏览器端 | 「一个 E2B 沙箱的共享生命周期所有者……处于同一个**远程 Linux** 工作树与进程环境中」 | 远程 SaaS 沙箱，没有本机前提 |
| `context/time-context` | 需要本机终端 | 「附加到当前开放请求的**浏览器时区**」 | 靠浏览器时区，与终端无关 |
| `web/web-fetch-http` | 本机文件 | 「匿名公共 HTTP(S) `WebFetchProvider`……URL 验证、HTTP 传输、重定向策略」 | 纯出网抓取，不碰文件 |

三抽三错，说明这一列的错误率不低。**结论：任何「不进范围」的理由如果只落在第四列上，
都必须先回 README 原文核一遍再定。** 这条不是谨慎，是不重犯第零节那个错误——
一个没被核过的判断被当成依据用，就是当初「按包名裁决」的同一件事换了个壳。

除上面三条外，以下各行的第四列与第二列的能力描述对不上，标记为**待核**，
在它们各自被裁决之前必须逐个回原文：

`boot/app-boot`（需要本机终端？做的是配置路径解析与环境加载）、
`host/apiproxy`（浏览器端？它是服务端的 HTTP 载体）、
`feedback/message-feedback`（本机文件？走 storage 接缝）、
`llm/llm-deepseek`（本机文件——可能对，图片走文件 API，需核）、
`client/ui-agent-preset`（本机文件？是浏览器表层）、
`util/native-command`（桌面UI？是免 shell 的 execFile 运行器）、
`subagent/subagent-acp` `subagent-claude-code` `subagent-codex` `subagent-dsh-sdk`
（需要本机终端？它们起的是子进程，不是 PTY）、
`session-query/session-log-export`（桌面UI＋浏览器端，两个都需核）。

## 四、清单推翻了什么

以下每一条都是**读完之后才知道、按包名不可能知道**的事实。列在这里是为了说明
第零节那句「换掉裁决依据」不是形式主义。

### 1. 多 agent 协作是现成的，而且有两条路

`subagent/subagent` 不是「一次性委派」。它区分**一次性**与**可继续**：
可继续的子级有自己的持久 Session、自己的 FIFO 收件箱、跨轮次存活，父级可以继续给它发消息。
`reportFrom(child, content, {delivery})` 是子→父的推送通道，`delivery: 'next-step'`
会在父 agent 最近的步骤边界上 **steering 父 agent**；`silent` 则只注入上下文不唤醒。
「一个 agent 写脚本 + 多个审查 agent 持续把反馈推回去驱动它改」这件事，
DSH 已经做完了，不需要我们设计。

`experimental/agent-team` + `tool-agent-team` 是**第二条、此前完全不知道存在的路**：
隐式 Root Agent Team、Lead/teammate 名册、**持久 peer mailbox**、共享任务 DAG。
它不是 `subagent` 的封装，是并行的另一套。

### 2. DSH 自己给出了两份「最小可跑」的定义，而它们都包含我列在「未定」里的包

- `examples/agent-spine-demo`——DSH 自己的最小 agent 主干，成员固定在代码里：
  timer、llm、session、session-title、system-prompt、tools、skill、skill-filesystem、
  agent、agent-loop、agent-instructions、invariants、llm-retry、**goal + goal-round-driver + tool-goal**、
  **jobs-local + tool-jobs**、tool-skill、tool-bash。
  加粗那两组在我的「未定 26」里。**DSH 认为它们是最小集的一部分，我认为它们可能不要**——
  这个分歧必须由证据而不是由默认值解决。
- `bundle/headless`——另一端的最小：`bundle/base` + 关掉 HMR 的覆盖 + `headless-runner`，
  不挂 webserver、不挂 apiproxy、不挂 web 客户端、不挂 workspace。
  这是「无界面、提交一个任务、跑完退出」的官方组合。

两份独立的「最小」摆在一起，比我按直觉圈出来的 46 个可信得多。

### 3. 「整支不装」这一刀同时砍掉了接缝和实现，而它们不是一回事

`fs`、`shell`、`sandbox`、`subprocess`、`lsp`、`code-runtime`、`terminal`、`web`
每一个域里都有一个**纯接缝包**，性质＝接缝、服务端障碍＝无：
`fs/fs`、`shell/shell`、`sandbox/sandbox`、`subprocess/subprocess`、`lsp/lsp`、
`code-runtime/code-runtime`、`web/web`。它们只定接口，不碰任何本机资源。

`DESIGN.md` 第四节把这些域整支划出去，是**用实现的理由砍掉了接缝**。
两者要不要，是两个独立问题：本机实现服务端确实装不上；接缝要不要留，
取决于我们会不会挂自己的后端（比如 `fs` 接缝挂对象存储、`web` 接缝挂我们自己的检索服务）。
这一刀当时没有区分。

### 4. `web/` 这一支根本没被考虑过，而它没有任何本机前提

`web/web`（接缝）+ `web/tool-web`（web_search / web_fetch 两个模型工具）+
三个搜索提供方（DeepSeek / Exa / Perplexity）+ `web-fetch-http`。
除 `web-fetch-http` 那条待核的错标外，全域服务端障碍为「无」。
一个服务端 agent 要联网检索，这就是现成的那一套，而它在 46 个进范围的包里一个都没有。

### 5. 有一个包在 Go 里做不出来

`workflow/tool-workflow` 的编排脚本是 **JavaScript，跑在 Node worker thread 上**
（`workflow/workflow-worker-thread`）。Go 没有等价物。这不是「要不要」的问题，
是**移植不了**——如果要工作流编排，那一层得重新设计，不能对译。
`workflow/tool-ralph`（固定流程、顺序交给多个全新子 agent）不依赖脚本引擎，是可移植的。

### 6. `mcp/mcp-client` 有一个真实的服务端障碍

它通过 stdio 连接**外部 MCP 服务器进程**。多用户服务端上，
「每个会话起一批用户指定的子进程」是需要单独设计的，不是装上就能用。
它此前在「未定」里，未定的理由不明确；现在理由明确了。

### 7. `llm/llm-pi-ai` 直接对应我们的模型接入方式

它是基于 `@earendil-works/pi-ai` 的**多提供方通用适配器**，可以手工声明路由
（`api: openai-completions` + `baseURL`）接任何 OpenAI 兼容端点。
`DESIGN.md` 只说了「不要 `llm-deepseek`，走本地模型 `conn.aaii.chat`」，
没提这个包——而它正是接本地模型的那一个。自陈限制：`supportedProtocols()`
刻意窄于 pi-ai 全集，只保留「用密钥、端点与标头能完整描述」的协议。

## 五、DSH 自陈的限制里，与我们架构直接冲突的

这些是 DSH 作者自己写下的「做不到」。它们不影响「要不要移植」，
但**决定了移植过来之后还欠什么**，必须在设计里认领，不能等撞上再说。

| 冲突 | DSH 的原话 | 我们这边的后果 |
|---|---|---|
| 单进程 vs 多机 | `core/agent`：「发起方作用域仅在进程内有效……委派以外的 agent 间通道尚不支持」；`subagent`：「驻留仅限进程内……对单个持久化存储的并发访问仍然需要持久化邮箱和跨进程租约协议」 | 我们定了 Postgres + 多机。**持久化邮箱和跨进程租约 DSH 没有，得我们做。**19 个包自陈「仅单进程」 |
| 会话删不掉 | `session/session-persistence`：「无删除或保留接口」；`workspace`：「会话删除与破坏性文件夹移除尚未提供」；`session-title`：「无删除或搜索功能」 | 多用户服务必须能删用户数据。**这条接缝要我们自己加，且要贯穿投影、缓存、标题、附件** |
| 存储能力下限 | `storage/storage-domain`：「无跨表事务、二级索引或多段键」；`storage/storage`：「kv 是唯一数据形状」 | 我们的 Postgres 后端如果照这个接缝做，就拿不到事务和索引。要么接受，要么在接缝上加——**这是设计决定，不是实现细节** |
| 并发写后写胜出 | `credentials-local`：「并发写入同一引用后写胜出」；`settings-file`：「冲突仍后写胜出」；`feedback/message-feedback`：「compare-and-set 仅限单进程」 | 这些实现我们都不移（本机文件），但**接缝上没有并发语义**，我们的 Postgres 实现要自己定 |
| 人工介入不能跨轮次挂起 | `interaction/user-approval`：「请求仅在尚未结束的轮次有效；仅存在一次性授权」 | 与已定的「异步审批不做」一致，不冲突。但要记住：**轮次一结束，挂起的请求就作废** |
| 崩溃恢复不证明副作用 | `session/session-checkpoint-policy`：「恢复无法证明副作用完成」 | 与 `aiboys-go` 那边「视频生成花钱、必须区分未开始/结果未知」是同一个问题。DSH 不解决它 |
| spill 存了取不回 | `spill/spill`：「没有取回/删除 API，存储不等于访问控制」 | 若要用，取回和归属校验都得补 |

## 六、这份清单还不是裁决

它只是把裁决**能站在什么上面**这件事补齐了。接下来是三步，按顺序：

1. **核第三节那批「待核」行**，把服务端障碍这一列变成可依赖的。
2. **重出范围表**：每个包一条结论 + 一句理由，理由必须落在本文档的第二列或第五列上，
   不许落在包名上。`DESIGN.md` 第三、四节整体替换。
3. **同步 `portmap.tsv`**：范围变化会改动裁决行，`OUT_OF_SCOPE` 的 note 要从
   「服务化范围外」换成具体理由。（另有一条已知过期：`storage/storage-json` 的 note
   还写着「服务端换成 SQLite」，应为 Postgres。）

在第 2 步完成之前，`DESIGN.md` 第三、四节不作数。
