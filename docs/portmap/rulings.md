# 逐包裁决

227 个包，一包一行，四种取值。依据是 `functions.md` 那 2009 条和 `required.md` 那五条前提，
不是包名。

| 取值 | 含义 | 数 |
|---|---|---|
| **需要** | 移过来 | 82 |
| **抄形状** | 不移这个包，抄它的接口／装配顺序／协议形状 | 15 |
| **Go 已有等价物** | 行为要，手段不要——Go 标准库里是白送的 | 3 |
| **不需要** | 有一个我们没有的前置条件 | 127 |
| **说不清** | 缺信息，理由里写明缺的是什么 | **0** |

「不需要」不等于「这功能没用」，等于**它有一个我们给不了的前置**（本机磁盘、桌面对话框、
浏览器 DOM、Node worker thread、本机子进程、**沙箱**）。

**说不清已经清零。**「说不清」当时的含义是「这五条前提没要求它，要不要由消费方定」，
不是「不要」。227 行现在每一行都有终判。

## 一、这张表怎么来的

六批子 agent 逐包读 README 出的初判，然后我逐条对过。**41 行被订正**（表里加粗的那些）。
订正不是润色，是原判和证据打架：

- **`host/*` 六个**——初判的论点是「不开放本机资源只约束 agent 的工具清单，不约束前端调的 API」。
  这条论点和硬约束直接冲突，而且 `DESIGN.md` 第六节早就把 `dirbrowse/`、`dirpicker/`、
  `frontendstatic/`、`webserver/` 四个目录删掉了。论点站不住，跟着它走的六行全部重判。
- **`subagent/subagent-dsh-sdk`**——初判 需要，但它 README 自陈「仅支持本地子进程」
  「每次运行使用全新的运行时进程」，和同一个 agent 判了 不需要 的 `-acp`／`-codex` 是同一类。
  同一批里自己和自己打架。
- **`workflow/tool-workflow`**——初判 需要，但它的编排脚本是 JavaScript，唯一引擎
  `workflow-worker-thread` 跑在 Node worker thread 上，而同一个 agent 把那个引擎判了 说不清。
- **批 6 那 22 个 需要**——它把「值得抄」当成了「要移植」。**第四档 `抄形状` 就是为这个加的。**
- **`web/*` 五个、`goal/*` 四个**——初判 需要，但五条前提没有一条要求联网搜索；`goal/*` 是
  DSH 作者自己在主干清单里标成可选的。有用不等于前提要它，这两组当时降成 说不清 交给消费方；
  后来分别定为 不需要（`web`，推后）与 需要（`goal`，见第三节）。
- **`util/brand`、`util/atomic-write`**——初判里 agent 自己的理由文字就写着「Go 有等价原语」。
  这不该判 不需要（那意味着功能不要），单开第五档 `Go 已有等价物`：行为要，手段换。

必需集那 30 个不走 agent，直接取 `required.md` 的四层，理由列里标着是哪一层。

## 二、怎么抄

裁决只回答「要不要」。这一节回答「要的那些，形状是什么」——**这三块是整个仓库里最值钱的
三份设计，抄错了后面全歪。**

### 2.1 装配顺序（出处 `bundle/headless` + `examples/agent-spine-demo`）

DSH 自己写了两份「最小可跑」的定义，`headless` 那份连启动步骤都记全了：

```
app-boot（启动路径、环境加载、故障处理、Profile）
  → bundle/base（模型适配器、default-model 选择、工具、持久化）
    → spine 挂载序（llm → llm-retry → session → session-title → system-prompt
                     → tools → agent → agent-loop → agent-instructions
                     → skill → tool-skill → jobs-local → tool-jobs → invariants）
      → headless 表层（禁用 HMR → Code Mode worker → headless-runner）
        → runner：读 ctx.agentDefaultModel → 建 Agent → 交任务 → 等停稳 → 写 stdout
```

**「等停稳」是这条链里唯一不显然的一步。** 不是等最后一个 token，是等整棵服务树静止——
后台 job、子 agent、压缩都可能还在跑。抄的时候这一步不能省成「等模型返回」。

### 2.2 多 agent 协作原语（出处 `experimental/agent-team`）

**全仓库唯一一个带持久 peer mailbox + 共享任务板的协作原语。** 三件东西：

| 件 | 形状 |
|---|---|
| Roster 状态机 | 成员的加入／在岗／离场是状态机，不是一个数组 |
| 投递模式 | **Quiet**（攒着，不打断对方当前轮次）／ **Wakeup**（立刻唤醒）两档 |
| 任务板 | 版本化快照 + CAS：`expectedRevision` 对不上就返回 `TEAM_TASK_STALE_REVISION`，任务之间是 DAG |

它自陈的限制是「单进程、共享 checkout」「mailbox 不保证跨进程 exactly-once」——当时判了 说不清。
**消费方已裁定要，判 抄形状**：能力要，包不移。
**这三样的形状与进程模型无关**：Roster 状态机、两档投递、CAS + 版本号，换成 Postgres
存储照样成立，而且 CAS 那一套本来就是为并发写设计的。要抄的是这三样，不是它的进程内实现。

### 2.3 对外协议形状（出处 `sdk/protocol` + `sdk/server`）

```
分帧：按换行分帧的 JSON-RPC 2.0
方法：initialize / session/prompt / shutdown
通知：session.event / session.status / subagent.started / subagent.finished
```

`sdk/server` 的三条时序值得连着抄：`initialize` **等整棵树加载完成**再返回、
`session/prompt` **排队**不并发、`shutdown` **刷新后**再退出。

DSH 走的是 stdio（子进程驱动），我们走 HTTP——**换的是承载，不是方法表和通知表**。
那四条通知正好是前提 2（恢复历史）和前提 5（多 agent）要往外推的东西。

### 2.4 不用真模型跑完整轮次（出处 `test-support/llm-replay`）

录制／回放 LLM 响应。**没有这个，前提 3、4 那些「进程中途死掉」的路径根本没法写测试**——
崩溃恢复的用例要求在精确的位置断开，真模型给不了这个精确度。这一件优先级不低于上面三件。

## 三、还没解决的

- **说不清已经清零**：最后那批 20 个由消费方逐条定完，227 行全部有终判。
- `mcp/mcp-client` 的 待核 已经销掉：`transport.ts` 里两个分支，`stdio` 走
  `StdioClientTransport` 要 spawn 子进程，`streamable-http` 走
  `StreamableHTTPClientTransport(new URL(url), {headers})` 就是普通 HTTP。
  **移后者，不移前者**——和 `fs → 对象存储` 是同一条界线：服务进程自己去某处取东西可以，
  在本机起进程不行。消费方裁定要。
- `mcp/mcp-client` 有一件 DSH 不用管而我们要管的：**这台 MCP 服务器是谁挂的、用谁的 token。**
  DSH 单机单人，服务器配置和 `headers` 里的凭据写死在 `cordis.yml` 里；多用户下这是每个
  用户各自的配置，要挂到 `credentials` 的归属校验上，否则 A 的 token 会被 B 的会话用到。
- `capabilities.md` 第三节那 11 行 待核 仍然待核。
- `goal/*` 四个判 需要，但**带一个必须自己补的缺口**：这一支只数轮数，不计 token、
  不计钱、不计时间、不计提供方配额（四份 README 的「已知限制」各自写着）。DSH 是单机单人、
  钱是用户自己的、进程就在眼前；服务端不是，一个目标能把额度跑干而无人在场。
  **预算闸门不在这四个包里，要在消费它的那一层自己加。**
- `goal/*` 的第二件事记在这里免得以后当成 bug：**续行权限从不持久化。**
  会话恢复或 fork 之后目标还在、phase 还是 active，但不会自动重启工作，必须显式 `resume`。
  这是作者有意的，不是缺陷——但它意味着 `goal` **没有**替前提 3、4 解决跨天续跑。
- `goal/*` 排在移植顺序最后：它要 `core/loop` 的 pre-step 钩子和 inbox，两者不稳就上不去。
- `web/*` 六个判 不需要 的是**现在**：接缝零依赖、不在主干挂载序里、已定的 67 个包
  没有一个依赖它，所以拿到搜索数据源之后补回来是新增两个包加挂一个提供方，不动已有代码。
- `docs/DESIGN.md` 第三、四节**已按本表重写，恢复效力**。上一版那两张范围表里有三处是错的
  （`fs`/`shell` 那八支整支不装、`test-support` 整支排除、`spill`/`acp` 整支排除），
  更正记在 DESIGN.md 第四节。
- **第一类「纯接缝，无本机前置」那 11 个已经定完了**：`fs/fs` 需要（后端对象存储），
  `shell` `shell-env` `sandbox` `sandbox-policy` `terminal` `subprocess` `code-runtime`
  `lsp` `e2b` 九个不需要（消费方裁定：执行命令／代码／终端的前置是沙箱，服务端不提供沙箱，
  接缝没有实现方），`fs/fs-observation-policy` 不需要（它记的是「模型读过哪些文件、有没有被改过」，
  服务端的 `fs` 后端是对象存储，模型不逐个读文件，这个策略没有观察对象）。
  连带那 24 个实现包不用再写远程版的替代了。

## 四、逐包

### `subagent/`（11）— 需要 7、不需要 4

| 包 | 裁决 | 理由 |
|---|---|---|
| `subagent/subagent` | 需要 | required.md 第二层第 4 组（前提 5 多 agent 协作）：接缝：`startContinuable` / `followup` / `interrupt` / `reportFrom` |
| `subagent/subagent-acp` | 不需要 | 每次运行使用全新进程，且主要用于执行 ACP 协议驱动的子进程，这涉及本机命令执行 spawn、子进程管理、工作目录解析，违反了"服务端不开放本机资源"的约束。 |
| `subagent/subagent-claude-code` | 不需要 | 本包驱动官方 Claude Code SDK 在本机执行进程，涉及本机工作目录、环境变量、文件读写和 SDK CLI 执行，且原生账户状态与产品配置都读自本机用户设置，违反了服务端本机资源隔离的约束。 |
| `subagent/subagent-codex` | 不需要 | 每次运行都 spawn 原生 Codex CLI 进程，涉及子进程管理、本机工作目录解析和命令行执行，违反了"服务端不开放本机资源"的约束。 |
| `subagent/subagent-dsh-sdk` | **不需要** | README 自陈「仅支持本地子进程」「每次运行使用全新的运行时进程」，与 `-acp`/`-codex` 同类，原判 需要 与同一 agent 对同类包的判断自相矛盾 |
| `subagent/subagent-fork-in-process` | 需要 | required.md 第二层第 4 组（前提 5 多 agent 协作）：继承父级已完成轮次的前缀 |
| `subagent/subagent-in-process-driver` | 需要 | required.md 第二层第 4 组（前提 5 多 agent 协作）：进程内驱动，spawn 与 fork 共用 |
| `subagent/subagent-spawn-in-process` | 需要 | required.md 第二层第 4 组（前提 5 多 agent 协作）：全新空白子 agent |
| `subagent/tool-subagent` | 需要 | required.md 第二层第 4 组（前提 5 多 agent 协作）：给模型的委派工具 |
| `subagent/tool-subagent-control` | 需要 | required.md 第二层第 4 组（前提 5 多 agent 协作）：`send_message` / `interrupt_agent` |
| `subagent/tool-subagent-report` | 需要 | required.md 第二层第 4 组（前提 5 多 agent 协作）：子 agent 向启动者上报 |

### `workflow/`（4）— 需要 2、不需要 2

| 包 | 裁决 | 理由 |
|---|---|---|
| `workflow/tool-ralph` | 需要 | Ralph 固定工作流依赖 `ctx.workflowEngine` 和 `ctx.subagents` 两个已定服务，直接服务于 functions.md 中"多成员协作"相关的前提 5，提供结构化的多轮迭代 agent 协作原语。 |
| `workflow/tool-workflow` | **不需要** | 编排脚本是 JavaScript，唯一引擎 `workflow-worker-thread` 跑在 Node worker thread 上；Go 侧没有产出方 |
| `workflow/workflow` | 需要 | 工作流 seam 的服务定义提供 `ctx.workflowEngine`，functions.md 记录有"生命周期观察器"与事件投影，支持前提 5 多 agent 协作的基础设施。 |
| `workflow/workflow-worker-thread` | **不需要** | `WorkflowEngine` 的唯一实现，但它跑的是 JavaScript 编排脚本、载体是 Node worker thread，Go 侧两样都没有。自陈「每次运行都要支付 worker thread 成本」「不是安全边界」。**暂时不要**——接缝 `workflow/workflow` 已定为需要，`tool-ralph` 是固定工作流不依赖脚本引擎，所以缺这个实现不挡路；以后要跑用户自定义编排再补一个 Go 引擎，接缝不动 |

### `experimental/`（2）— 抄形状 2

| 包 | 裁决 | 理由 |
|---|---|---|
| `experimental/agent-team` | **抄形状** | 全仓库唯一带「持久 peer mailbox + 共享任务板（版本化快照 + CAS）」的多 agent 协作原语：Roster 状态机、Quiet／Wakeup 两档投递、`expectedRevision` 对不上返回 `TEAM_TASK_STALE_REVISION` 的 CAS 任务板（任务之间是 DAG）。**能力要，包不移**——它自陈「单进程、共享 checkout」「mailbox 不保证跨进程 exactly-once」，与前提 1、4 直接相撞，照搬进来等于把单进程假设焊死。但这三样的形状与进程模型无关，换成 Postgres 存储照样成立，CAS 本来就是为并发写设计的。抄形状见第 2.2 节 |
| `experimental/tool-agent-team` | **抄形状** | `agent-team` 的模型侧工具。抄工具形状（发消息／领任务／改任务带 `expectedRevision`），实现跟着重写的接缝走 |

### `goal/`（4）— 需要 4

| 包 | 裁决 | 理由 |
|---|---|---|
| `goal/command-goal` | **需要** | 面向用户的 `/goal`／`pause`／`resume`／`clear`，挂在已定为需要的 `interaction/commands` 上。目标由人设、也得由人叫停，这条是唯一不经模型的通道 |
| `goal/goal` | **需要** | 会话内持有一个长期目标（目标文本、phase、已跑轮数／上限），事件溯源写进会话日志。消费方裁定：一次交代、agent 自己跑完，是运行时该给的语义，不该让每个消费方各写一套 |
| `goal/goal-round-driver` | **需要** | 续行驱动：agent 一进 idle 就自动排下一轮。没有它，`goal` 只是一条存起来的状态，「一直跑」这件事不成立 |
| `goal/tool-goal` | **需要** | 给模型的 `get_goal`／`create_goal`／`update_goal`，模型自己声明完成或阻塞。没有它，目标只能停在轮数上限上 |

### `jobs/`（3）— 需要 3

| 包 | 裁决 | 理由 |
|---|---|---|
| `jobs/jobs` | 需要 | Job 注册表约定，前提3用户中途离开相关 |
| `jobs/jobs-local` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`examples/agent-spine-demo`），作者定义的「跑一个 agent 最少要装什么」 |
| `jobs/tool-jobs` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`examples/agent-spine-demo`），作者定义的「跑一个 agent 最少要装什么」 |

### `schedule/`（1）— 需要 1

| 包 | 裁决 | 理由 |
|---|---|---|
| `schedule/schedule` | 需要 | 持久提醒服务支持 schedule/change 事件，前提4跨进程恢复 |

### `plan/`（1）— 需要 1

| 包 | 裁决 | 理由 |
|---|---|---|
| `plan/plan-mode` | 需要 | Plan 状态完全持久化到日志，前提3和4相关 |

### `todo/`（1）— 需要 1

| 包 | 裁决 | 理由 |
|---|---|---|
| `todo/tool-todo` | 需要 | Todo 工具支持 agent 协作任务记录 |

### `guard/`（2）— 需要 2

| 包 | 裁决 | 理由 |
|---|---|---|
| `guard/repeat-tool-reminder` | **需要** | 同一个工具被反复调用时提醒模型。零前置，而模型卡在死循环里每一轮都是一次付费的模型调用 |
| `guard/timeout-policy` | 需要 | 工具超时协作式取消，防止资源泄漏 |

### `spill/`（3）— 需要 2、不需要 1

| 包 | 裁决 | 理由 |
|---|---|---|
| `spill/spill` | 需要 | 过大工具结果持久化服务定义 |
| `spill/spill-local` | 不需要 | 本地文件系统实现，会话级私有目录写操作违反规则 |
| `spill/spill-policy` | 需要 | 工具结果 spill 后处理，超大结果替换策略 |

### `typert/`（4）— Go 已有等价物 1、不需要 3

| 包 | 裁决 | 理由 |
|---|---|---|
| `typert/generator` | 不需要 | TypeScript 项目分析与代码生成工具，functions.md 自陈"仅在构建或测试时运行"。Go 目标语言有现成的反射系统 (`reflect` 包) 和代码生成工具（如 protoc、stringer），无需搬运 TypeScript 专用的编译期类型提取基础设施。 |
| `typert/loader` | 不需要 | 该包扫描现有 Loader 配置项监听 Cordis plugin 生命周期、解析 package.json、校验 TYPERT manifest 并注册贡献项。这是 TypeScript/Node 插件生态的特定工件，Go 中使用现成的 reflection 包即可处理运行时类型信息，无需搬运该层基础设施。 |
| `typert/protocol` | 不需要 | 提供 `@Remote` 装饰器、`TypertRemoteService` 基类等 TypeScript 编译期元数据标记机制。Go 已有 struct tag、方法集和接口反射作为运行时替代，无需搬运 TypeScript 装饰器与类型擦除模式。 |
| `typert/registry` | **Go 已有等价物** | 运行时反射信息与可选 Zod schema 的注册表。Go 的 `reflect` + struct tag 白送，不移包。同 `util/brand` 的处理 |

### `util/`（7）— 需要 2、Go 已有等价物 2、不需要 3

| 包 | 裁决 | 理由 |
|---|---|---|
| `util/atomic-write` | **Go 已有等价物** | 原子替换 + 跨进程锁；Go 有 `os.WriteFile` / `os.Rename` / `flock` 等价原语。同 `util/brand`，换手段不换行为 |
| `util/brand` | **Go 已有等价物** | 原 agent 自己的理由文字就写着「Go 的 type alias 和 unexported 字段可以实现同等效果」。不是砍掉，是换手段 |
| `util/home-paths` | 不需要 | 解析 DSH 主目录、处理 ~ 展开、读取 $DSH_HOME 环境变量等，functions.md 明确涉及"解析 DeepSeek Harness 的单根主目录"和"为原生文件系统 watcher 提供稳定目标路径"。这是本机路径管理工具，违反服务端不开放本机资源的约束。 |
| `util/launch-environment` | 不需要 | functions.md 明确表述"把本次运行的环境冻结为不可变快照"、"搜索所有层（进程、项目 .env、用户 ~/.dsh/.env）"。涉及项目 .env 读取、用户主目录文件访问和启动环境快照，违反服务端本机资源隔离的约束。 |
| `util/native-command` | 不需要 | 零依赖免 shell execFile 运行器，functions.md 明确表述"直接 spawn 可执行文件"、"以 utf8 捕获 stdout/stderr"。这是本机子进程执行工具，违反了"服务端不开放本机资源"的约束。 |
| `util/output-retention` | 需要 | 为必须限制返回上下文量的工具提供有界的面向模型输出。functions.md 记录有 ItemRetainer/TextRetainer 类，这是纯逻辑的保留策略库，独立于本机资源。支持前提 2（恢复历史对话时的输出截断）和模型窗口管理。 |
| `util/timeout` | 需要 | 超时的时序与分类部分，functions.md 记录有 clampTimeout、deadline、idleWatchdog、timeoutOf 等纯函数。这是纯逻辑的超时管理库，与前提 1（多用户并发）和工具调用的生命周期管理直接相关。 |

### `runtime-diagnostics/`（1）— 需要 1

| 包 | 裁决 | 理由 |
|---|---|---|
| `runtime-diagnostics/invariants` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`examples/agent-spine-demo`），作者定义的「跑一个 agent 最少要装什么」 |

### `test-support/`（6）— 需要 5、不需要 1

| 包 | 裁决 | 理由 |
|---|---|---|
| `test-support/acp-snapshot` | 需要 | ACP快照测试harness。"launchAcpTestAgent启动器、通过SDK客户端收集会话、runScenario驱动、normalizer + scrubber + defineAcpSnapshotSuite"。如果你用ACP，需要能运行集成测试验证round-trip行为。这个工具让你在不连真model下跑完整agent回合（见下）。 |
| `test-support/agent-loop-testkit` | 需要 | agent loop测试依赖挂载工具。"mountAgentLoopTestDependencies按序挂LLM、session、system-prompt、tools、agent"。你需要能在单元/集成测试中隔离地测试loop逻辑。这直接支持"跨天活跨进程活下来"的持久化测试。 |
| `test-support/client-runtime` | **不需要** | cordis + jsdom 的浏览器测试脚手架，前置是 DOM |
| `test-support/llm-mock-server` | 需要 | 可编脚本OpenAI兼容mock HTTP服务器。"行为脚本(connection_reset/stream_disconnect/.../success/tool_call_success)、时序与内容控制"。这是**不连真模型情况下跑完整round-trip**的工具——正是你需要的。它让"跨天活"的测试不依赖API key和配额。 |
| `test-support/llm-replay` | 需要 | 无密钥快照测试的LLM回放插件。"根据已记录session JSONL fixture重建模型流、installLlmReplay返回ReplayHandle"。这是**不连真模型跑回合**的主要方式——用既有fixture驱动测试，省掉真实API成本。条目"首次调用顺序脚本绑定假设串行委托、只有普通loop分片和标记本地压缩输出能派生"——限制在"什么场景能用"，不是"用不了"。 |
| `test-support/loader-smoke` | 需要 | 烟雾测试harness。"resolveExampleLaunch、runLoaderSmoke、runFixtureTurn单轮驱动"。这是"启动 + 执行single turn + 查收output"的端到端脚手架。你需要它验证"应用能启动、能跑、能shutdown"的完整周期。 |

### `examples/`（3）— 抄形状 3

| 包 | 裁决 | 理由 |
|---|---|---|
| `examples/acp-demo` | **抄形状** | 示例应用，价值在它记录的装配顺序，不在代码本身 |
| `examples/agent-spine-demo` | **抄形状** | 示例应用，价值在它记录的装配顺序，不在代码本身 |
| `examples/jsonrpc-demo` | **抄形状** | 示例应用，价值在它记录的装配顺序，不在代码本身 |

### `llm/`（5）— 需要 4、不需要 1

| 包 | 裁决 | 理由 |
|---|---|---|
| `llm/llm` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`examples/agent-spine-demo`），作者定义的「跑一个 agent 最少要装什么」 |
| `llm/llm-deepseek` | 不需要 | 依赖 DeepSeek 官方接口，题目明确不走官方接口 |
| `llm/llm-pi-ai` | 需要 | 通用多提供方适配器支持 OpenAI 兼容协议，服务自建本地推理 |
| `llm/llm-retry` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`examples/agent-spine-demo`），作者定义的「跑一个 agent 最少要装什么」 |
| `llm/token-meter` | 需要 | token 测量与 session 投影单元，支持恢复历史对话 |

### `web/`（6）— 不需要 6

| 包 | 裁决 | 理由 |
|---|---|---|
| `web/web` | **不需要** | 接缝本身零前置，但唯一能挂上去的搜索提供方都要第三方 API key，现在没有数据源。整支推后 |
| `web/tool-web` | **不需要** | 随接缝出局 |
| `web/web-fetch-http` | **不需要** | 抓取不要 key，但它只能「给一个 URL 取这一个 URL」；没有搜索就没人给 URL，单独存在没用。另：源码自陈 SSRF／私有网络防护未实现（`policy.ts:18`） |
| `web/web-search-deepseek` | **不需要** | 要 DeepSeek 官方 API key，没有 |
| `web/web-search-exa` | **不需要** | 要 Exa API key，没有 |
| `web/web-search-perplexity` | **不需要** | 要 Perplexity API key，没有 |

### `mcp/`（1）— 需要 1

| 包 | 裁决 | 理由 |
|---|---|---|
| `mcp/mcp-client` | **需要** | 桥接外部 MCP 服务器的工具到 `ctx.tools`（`mcp__<server>__<tool>`）。**只移 `streamable-http` 传输**：一个 URL 加几个 header，零本机前置。`stdio` 传输不移——它在本机 spawn 子进程，且服务端上没有要挂的东西 |

### `skill/`（4）— 需要 2、不需要 2

| 包 | 裁决 | 理由 |
|---|---|---|
| `skill/skill` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`examples/agent-spine-demo`），作者定义的「跑一个 agent 最少要装什么」 |
| `skill/skill-badge` | 不需要 | 仅内置 DSH 标志资源，不服务五条前提 |
| `skill/skill-filesystem` | 不需要 | 本地文件系统 skill 扫描，读写本地目录违反规则 |
| `skill/tool-skill` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`examples/agent-spine-demo`），作者定义的「跑一个 agent 最少要装什么」 |

### `interaction/`（5）— 需要 5

| 包 | 裁决 | 理由 |
|---|---|---|
| `interaction/commands` | 需要 | 用户命令注册表，前提3交互场景 |
| `interaction/permission-presets` | 需要 | 权限预设管理，前提1多用户并发 |
| `interaction/tool-ask-user` | 需要 | ask_user_question 工具，前提3用户反问 |
| `interaction/user-approval` | 需要 | 审批 seam，前提3审批流程 |
| `interaction/user-questions` | 需要 | 用户交互 seam 定义，提供 ask() API |

### `preset/`（2）— 需要 2

| 包 | 裁决 | 理由 |
|---|---|---|
| `preset/agent-presets` | 需要 | Preset 组装，多用户可能不同 preset |
| `preset/persona` | **需要** | persona 是本运行时四个必填输入之一（模型、工具、技能、人格），不是可选装饰 |

### `credentials/`（3）— 需要 1、抄形状 1、不需要 1

| 包 | 裁决 | 理由 |
|---|---|---|
| `credentials/authorization` | **抄形状** | OAuth 与人工授权流程。形状要，实现要重写——它自陈「flow 不可恢复」是浏览器进程的限制，与前提 3、4（干一半走人、第二天接着干）直接相撞，服务端要自己的可恢复流程 |
| `credentials/credentials` | 需要 | required.md 第二层第 3 组（前提 1 多用户）：每次操作 `resolve()`，归属校验在接缝上而不是靠调用方自觉 |
| `credentials/credentials-local` | 不需要 | "文件型凭据提供方"从 credentials.yaml/.env 读取，文件明写"四层来源"依赖本机文件路径与环境变量，违反"服务端不开放本机资源"。 |

### `attachment/`（2）— 需要 1、不需要 1

| 包 | 裁决 | 理由 |
|---|---|---|
| `attachment/attachment` | 需要 | required.md 第二层第 3 组（前提 1 多用户）：内容寻址，与介质无关 |
| `attachment/attachment-local` | 不需要 | "本地文件系统附件存储"、"DSH_HOME/attachments 文件操作"依赖本机目录，违反"服务端不开放本机资源"。 |

### `core/`（8）— 需要 7、不需要 1

| 包 | 裁决 | 理由 |
|---|---|---|
| `core/agent` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`examples/agent-spine-demo`），作者定义的「跑一个 agent 最少要装什么」 |
| `core/agent-default-model` | **需要** | 记住默认用哪个模型：`currentSelection()` / `saveSelection()`，一份 `{provider, model, reasoningEffort}`，创建 agent 时没指定就用它。不装的话每开一个会话都得由调用方点名模型。**它自陈只有一项进程级默认值**，「每个会话的选择仍由入口负责」——多用户下「这个用户偏好哪个模型」是每人各自的，要在它上面按用户叠一层（和 `credentials` 同一个归属做法） |
| `core/agent-loop` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`examples/agent-spine-demo`），作者定义的「跑一个 agent 最少要装什么」 |
| `core/agent-tool-presentation` | **不需要** | 它的职责是在 `native` / `code` / `both` 三者里选一个。`code` 类模式要 `ctx.codeRuntime`（`code-runtime-worker-thread`，已判不要），所以只剩 `native` 一个可选值——而 `native` 本来就是 `dsh-tools` 的默认。一行插件选唯一值，等于不装 |
| `core/scope` | 需要 | required.md 第二层第 3 组（前提 1 多用户）：`ScopedLayers`：全局层 + 作用域链，近的层盖远的层 |
| `core/session` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`examples/agent-spine-demo`），作者定义的「跑一个 agent 最少要装什么」 |
| `core/system-prompt` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`examples/agent-spine-demo`），作者定义的「跑一个 agent 最少要装什么」 |
| `core/tools` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`examples/agent-spine-demo`），作者定义的「跑一个 agent 最少要装什么」 |

### `session/`（13）— 需要 10、不需要 3

| 包 | 裁决 | 理由 |
|---|---|---|
| `session/session-checkpoint-policy` | 需要 | required.md 第二层第 2 组（前提 2/3/4 会话可恢复）：三个落盘点决定「崩在哪儿丢多少」 |
| `session/session-persistence` | 需要 | required.md 第二层第 2 组（前提 2/3/4 会话可恢复）：接缝本身：`create/append/prepare/load/readFrom/list` |
| `session/session-persistence-jsonl` | 不需要 | "项目目录保留规范化 cwd 的可读形式"、"平铺文件布局不加载"、"不删除会话文件"、"POSIX 需硬链接支持"——全部依赖本机文件系统与路径操作，消费方已决定用 Postgres 后端。 |
| `session/session-persistence-sqlite` | 不需要 | "SQLite 会话存储后端"、"物理打包行存储"——服务端已定用 Postgres，此为本机 SQLite 实现。 |
| `session/session-projection` | 需要 | required.md 第二层第 2 组（前提 2/3/4 会话可恢复）：事件日志投影成喂给模型的消息序列 |
| `session/session-projection-cache` | 需要 | 文件明写"持久投影缓存"、"每会话一条记录"在 storage-domain 中落地，跨进程恢复投影状态直接服务"进程重启后活要跨天活下来"。 |
| `session/session-stats` | **需要** | 每会话的 turn / step / token 计数投影。多用户服务里钱是平台的，而 `goal/*` 自陈不管预算——这是那个预算闸门的数据源 |
| `session/session-telemetry` | **需要** | 遥测外发接缝（emit / flush / shutdown），零本机前置。DSH 那个往本机日志导出的实现不要，后端换成 OTLP over HTTP——和 `mcp` 只取 `streamable-http` 是同一条界线 |
| `session/session-telemetry-otel` | 不需要 | "OpenTelemetry 后端"、"OTLP/HTTP 导出"纯粹向外部服务投递遥测，与服务端 agent 运行时无关。 |
| `session/session-title` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`examples/agent-spine-demo`），作者定义的「跑一个 agent 最少要装什么」 |
| `session/session-title-all-prompts-llm` | **需要** | 总结全部消息生成标题，质量更好、代价更高。和上一条是同一接缝的两个可选后端 |
| `session/session-title-first-prompt-llm` | **需要** | 按首条消息生成会话标题。纯 `ctx.llm` 调用，零本机前置。默认走这条，最省 |
| `session/session-title-llm` | **需要** | 三个标题提供方的共享策略与路由。`session/session-title` 在 DSH 主干挂载清单里，接缝没有实现方就是空的 |

### `session-query/`（4）— 需要 2、抄形状 1、不需要 1

| 包 | 裁决 | 理由 |
|---|---|---|
| `session-query/session-log-export` | 不需要 | "浏览器下载"、"ZIP 生成与下载"纯粹 Web 前端 UI 能力，文件明写"仅浏览器下载"，"需逐 session 原始工件"，与服务端无关。 |
| `session-query/session-query` | 需要 | "会话查询引擎"的 listSessions/readSession/filterSessions/searchSessions 直接服务"可以恢复历史对话"与多用户并发时的会话列表查询，支撑前提 1、2。 |
| `session-query/session-query-sqlite` | **抄形状** | 同上，抄查询表结构与索引形状，实现写 Postgres 版 |
| `session-query/tool-session-query` | 需要 | "会话查询工具"的五个工具（session_search/session_event_search/session_trace 等）直接向 agent 暴露会话历史与血缘查询，支撑前提 2 的"恢复历史对话"。 |

### `storage/`（4）— 需要 2、抄形状 1、不需要 1

| 包 | 裁决 | 理由 |
|---|---|---|
| `storage/storage` | 需要 | required.md 第二层第 2 组（前提 2/3/4 会话可恢复）：上面这些的落点 |
| `storage/storage-domain` | 需要 | required.md 第二层第 2 组（前提 2/3/4 会话可恢复）：上面这些的落点 |
| `storage/storage-json` | 不需要 | "JSON 后端"、"<unit>.json 文件"、"原子写入 rename"依赖本机文件系统，违反"不开放本机资源"。 |
| `storage/storage-sqlite` | **抄形状** | 后端已定 Postgres（`DESIGN.md` 第七节），这个包不移；要抄的是它的**结构**——键值怎么映射成表、迁移怎么走 |

### `compaction/`（4）— 需要 3、不需要 1

| 包 | 裁决 | 理由 |
|---|---|---|
| `compaction/command-compact` | **不需要** | 给人的 `/compact`，手动触发一次压缩。**暂时不要**——压缩由接缝按阈值自动跑，手动入口只是个便利；接缝和两个后端都已在范围内，以后要补就是加一条命令注册，不动已有代码 |
| `compaction/compaction` | 需要 | required.md 第二层第 2 组（前提 2/3/4 会话可恢复）：跨天会话必然变长；带持久锁的崩溃恢复 |
| `compaction/compaction-basic` | **需要** | 接缝 `compaction/compaction` 的默认后端：整段总结。接缝已定为需要，而接缝没有实现方就是空的——这是唯一不依赖任何本机资源的压缩后端 |
| `compaction/compaction-tool-result-pruner` | **需要** | 压缩前先裁掉旧的工具结果。工具结果是上下文里最占地方的一块（和 `spill` 是同一件事的两个时机：`spill` 在写入时外置，剪枝在压缩时丢弃），不裁就等于把预算全花在历史工具输出上 |

### `context/`（6）— 需要 3、不需要 3

| 包 | 裁决 | 理由 |
|---|---|---|
| `context/agent-instructions` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`examples/agent-spine-demo`），作者定义的「跑一个 agent 最少要装什么」 |
| `context/file-reference` | 不需要 | "@file 语法"、"文件补全查询"依赖工作区文件导航，但服务端"不开放本机资源"禁止 agent 访问文件系统。 |
| `context/file-reference-local` | 不需要 | "本地文件系统实现"明确"工作区索引"、"cwd 为根目录"依赖本地文件系统遍历，违反"服务端不开放本机资源"。 |
| `context/session-reference` | 需要 | "@[label](uri) 跨会话 mention"、"prepare/listCandidates"直接支撑多 agent 协作时对其他会话上下文的注入，服务前提 5。 |
| `context/time-context` | **需要** | 往系统提示词里注入当前时间。agent 不知道今天几号是真缺陷。DSH 采的是浏览器时区，**我们改成由消费方传时区**——服务端没有「当前用户的浏览器」 |
| `context/tmux-context` | 不需要 | "Tmux 位置上下文"通过 ctx.shell 读取 tmux 状态，文件明写"仅第一个步骤"且"仅自身位置"，纯粹桌面/终端能力，服务端无 tmux 环境。 |

### `host/`（8）— 抄形状 1、不需要 7

| 包 | 裁决 | 理由 |
|---|---|---|
| `host/apiproxy` | **抄形状** | 会话管理 / 历史分页 / 投影推送 / 待处理队列 / 后台任务——这份方法清单就是对外 API 的形状，值得照抄；但 DSH 的实现绑在 cordis Remote 上，不移植 |
| `host/directory-picker` | **不需要** | 本机目录选择能力，服务端不开放本机资源。DESIGN.md 第六节已删 `dirpicker/` |
| `host/directory-picker-auto` | **不需要** | 只是在上面两个后端之间选，两个后端都不要，它没有可选对象 |
| `host/directory-picker-browse` | **不需要** | 同上；`host.listDirectory/createDirectory` 直接读写服务器磁盘。DESIGN.md 第六节已删 `dirbrowse/` |
| `host/directory-picker-native` | **不需要** | 依赖 osascript / zenity / kdialog 桌面工具 |
| `host/frontend-static` | **不需要** | 发 SPA dist 的静态文件服务器。DESIGN.md 第六节已删 `frontendstatic/` |
| `host/plugin-inventory` | **不需要** | cordis 插件加载状态的只读投影，我们不要 cordis 那个容器 |
| `host/webserver` | **不需要** | DSH 的 HTTP/upgrade 路由注册与端口绑定。DESIGN.md 第六节已删 `webserver/` |

### `boot/`（2）— 需要 1、不需要 1

| 包 | 裁决 | 理由 |
|---|---|---|
| `boot/app-boot` | 需要 | 启动路径、环境加载、故障处理、Profile机制。五条前提都需要：多用户共享启动配置、恢复冷会话需要环境加载、跨进程重启需要Profile持久化。 |
| `boot/cmdline` | **不需要** | 桌面启动器的命令行参数注入（`ctx.cmdlineArgs` / `ctx.appExit`），服务端的入参走 HTTP 不走 argv |

### `bundle/`（3）— 抄形状 3

| 包 | 裁决 | 理由 |
|---|---|---|
| `bundle/base` | **抄形状** | 共享核心插件行。要抄的是装配顺序，不是这个 bundle |
| `bundle/headless` | **抄形状** | DSH 自陈的第二份「最小可跑」定义，见下文「怎么抄」第二节 |
| `bundle/web-app` | **抄形状** | 浏览器表层组合，里面挂的 webserver / web-runtime 都判了不需要 |

### `extensions/`（4）— 不需要 4

| 包 | 裁决 | 理由 |
|---|---|---|
| `extensions/cordis-client-runner` | **不需要** | 浏览器端动态包装载，前置是浏览器页面 |
| `extensions/cordis-host-runner` | **不需要** | 宿主端动态包运行时，靠 vm 沙箱执行，且「带浏览器半的包在没有页面连接时挂起」。执行 + 浏览器两个前置都给不了 |
| `extensions/tool-cordis` | **不需要** | 让模型在运行时定义并执行插件（`cordis_define` / `run`），正是我们判掉的那件事：执行任意代码的前置是沙箱 |
| `extensions/ui-cordis` | 不需要 | 浏览器侧cordis UI面板。"覆盖整个框架的面板"、"shell.overlay条目"、"卡片在对话流里"——全是DSH Web桌面UI的表现层。你说有自己前端且无桌面UI，这整个包是Web exclusive。 |

### `hooks/`（3）— 需要 1、不需要 2

| 包 | 裁决 | 理由 |
|---|---|---|
| `hooks/hook-protocol` | 需要 | 共享hook原语库。"Matcher校验、Hook执行、输出解码、脱离运行追踪"——这些是无前端依赖的纯协议原语，被hook-claude-code和hooks-codex导入。Hook本质是"agent进程中特定点执行用户脚本"的能力。虽然条目没提multi-agent/恢复，但hook作为可选extension不违反前提，且两个方言桥可能有不同需求。 |
| `hooks/hooks-claude-code` | **不需要** | Claude Code hook 方言桥。接缝 `hooks/hook-protocol` 已定为需要，两个方言实现不要——它们各自只映射对方产品的钩子点 |
| `hooks/hooks-codex` | **不需要** | Codex hook 方言桥，同上 |

### `settings/`（2）— 需要 1、不需要 1

| 包 | 裁决 | 理由 |
|---|---|---|
| `settings/settings` | 需要 | 用户设置服务，前提1多用户个人配置 |
| `settings/settings-file` | 不需要 | 基于文件提供方，原子写入和跨进程写锁都涉及本地文件 |

### `identity/`（1）— 不需要 1

| 包 | 裁决 | 理由 |
|---|---|---|
| `identity/anonymous-user-id` | 不需要 | functions.md 明确表述"getOrCreateAnonymousUserId() 返回限定于 harness home 的随机 UUID v4"、"持久化存储：$DSH_HOME/.anonymous-user-id 文件"。涉及本机家目录写入和身份文件管理，违反服务端本机资源隔离。此包目的是遥测与反馈确认，不属于服务端 agent 运行时的核心职责。 |

### `feedback/`（2）— 不需要 2

| 包 | 裁决 | 理由 |
|---|---|---|
| `feedback/command-feedback` | 不需要 | 可选反馈机制，functions.md 说没有检索或管理界面 |
| `feedback/message-feedback` | 不需要 | 可选反馈机制，functions.md 说缺少客户端聚合与 UI |

### `workspace/`（1）— 需要 1

| 包 | 裁决 | 理由 |
|---|---|---|
| `workspace/workspace` | 需要 | 工作区实体注册表，前提1多用户会话组织 |

### `acp/`（1）— 需要 1

| 包 | 裁决 | 理由 |
|---|---|---|
| `acp/acp` | 需要 | 可创建新会话、接收提示词、返回已提交答案。5条前提都适用：多用户（一个连接多会话）、恢复（跨重启会话持久化）、中途离开（连接关闭重新取消）、跨天运行（可创建新会话）、多agent协作（subagent-acp生产客户端）。"已提交答案"与无逐token实时数据一致与设计。 |

### `api/`（2）— 抄形状 2

| 包 | 裁决 | 理由 |
|---|---|---|
| `api/gateway` | **抄形状** | Host 侧注册业务能力 + Client 侧挂生成的贡献项，是协议形状；实现绑在 cordis 上 |
| `api/remotes` | **抄形状** | 双侧 BFF 与身份解析，同上 |

### `sdk/`（3）— 需要 2、抄形状 1

| 包 | 裁决 | 理由 |
|---|---|---|
| `sdk/client` | **抄形状** | 「子进程方式驱动 Harness 运行时，走 stdio JSON-RPC」——子进程驱动这件事我们不做，客户端形状可抄 |
| `sdk/protocol` | 需要 | JSON-RPC wire format与协议类型定义。"按换行分帧的JSON-RPC 2.0、协议方法表(initialize/session/prompt/shutdown、session.event/status/subagent*)、错误响应"。这是你server与client的契约，所有多frontend都经过这套协议。 |
| `sdk/server` | 需要 | JSON-RPC服务器插件。"通过stdio提供JSON-RPC、initialize等待树加载完成、session/prompt排队、shutdown刷新退出、session.event流式发出、session.status全局转换"。你的服务进程必须expose协议——要么自己实现，要么用这个插件。条目"自动挂载适配器仅支持DeepSeek"是可配项，不是hard barrier。 |

### `client/`（40）— 不需要 40

| 包 | 裁决 | 理由 |
|---|---|---|
| `client/connection` | 不需要 | 浏览器 HTTP POST 与双条只下行 WebSocket、Host 头信任栅栏 |
| `client/hmr` | 不需要 | 浏览器订阅 SSE 通道接收 rebuilt 帧，React 状态重载 |
| `client/locale` | 不需要 | 浏览器 navigator 语言、DSH_HOME/settings.yaml 仅限桌面 UI |
| `client/modules` | 不需要 | window.__ModuleLoader__ bundle 物化属浏览器侧模块加载 |
| `client/runtime` | 不需要 | SlotRegistry 与 SessionRuntime 拥有 Session，client-side 投影消费 |
| `client/ui-agent-preset` | 不需要 | chip 选择与设置页属 UI，preset 打开操作驱动宿主桌面 |
| `client/ui-attachment` | 不需要 | 图片缩略图、灯箱预览、拖放遮罩属浏览器 DOM/React 交互 |
| `client/ui-brand-official` | 不需要 | 填充 sidebar.brand 与 conversation.hero.brand 占位者属 UI |
| `client/ui-commands` | 不需要 | 客户端命令 API、菜单查询、按键派发属 UI 层 |
| `client/ui-conversation` | 不需要 | 聊天视图、编辑器 dock 属 React 组件、键盘消息提交、图片拖放 |
| `client/ui-deliverables` | 不需要 | 产出文件行呈现、可点击文件引用属 UI，openFile 驱动宿主桌面 |
| `client/ui-directory-picker-browse` | 不需要 | 应用内浏览界面 (Miller 分栏视图) 属浏览器 UI 组件 |
| `client/ui-directory-picker-native` | 不需要 | ctx.workspaces.pickDirectory 驱动 OS 选择框、占位者属浏览器层 |
| `client/ui-goal` | 不需要 | GoalBar 条带属 conversation.input.dock 卡片、command-input 属 UI |
| `client/ui-input-trigger` | 不需要 | 输入触发流水线 (/ @ 检测)、MenuView 挂载 slot 属浏览器交互 |
| `client/ui-jobs` | 不需要 | Web 后台任务列表属 conversation.session.header.actions、任务行只读 |
| `client/ui-layout` | 不需要 | AppFrame 三栏外壳、ctx.theme 投影到 document 属浏览器 DOM |
| `client/ui-message-feedback` | 不需要 | Like/Dislike 按钮属 UI、compare-and-set 由 Host 负责 |
| `client/ui-model-selection` | 不需要 | 会话级模型目录 UI、/model popupSelect 与 conversation.input.model slot |
| `client/ui-permission-presets` | 不需要 | 浏览器权限界面、popupSelect 装饰、PermissionSelect 组件属 UI |
| `client/ui-plan` | 不需要 | Plan mode 状态徽章属浏览器 surface、placeholder 切换属 UI |
| `client/ui-primitives` | 不需要 | 纯 React 原子组件 (StateDot/Button/Pill/MarkdownText/TerminalBlock/ReadBlock/DiffBlock) |
| `client/ui-reference` | 不需要 | Web @file/@session source、文件选择属浏览器交互、会话搜索仅使用元数据 |
| `client/ui-renderer` | 不需要 | React 渲染层浏览器 Cordis 插件、ctx.uiRenderer.mount hydrate 启动 DOM |
| `client/ui-settings` | 不需要 | 设置领域底座、settings.describe 读取方、connection/reset 时刷新属浏览器 |
| `client/ui-settings-general` | 不需要 | 设置外壳呈现、sidebar.settings 插件、settings.openDocument 属浏览器打开 |
| `client/ui-settings-models` | 不需要 | 模型设置 UI、提供方行展开卡片、表单编辑属浏览器表单 |
| `client/ui-settings-plugin-inventory` | 不需要 | Web 设置中只读插件列表、双列紧凑卡片属浏览器 UI |
| `client/ui-settings-plugins` | 不需要 | 插件设置分区、可展开卡片属浏览器 UI、用户输入暂存属客户端 |
| `client/ui-sidebar` | 不需要 | 侧边栏外壳、品牌行、New Session 启动属浏览器 UI |
| `client/ui-skill` | 不需要 | /skill source 注册属 ctx.inputTriggers、wire 名称注册属 tool.call.toolview slot |
| `client/ui-slots` | 不需要 | Slot 注册表纯核心、declaration epoch 属浏览器层 Cordis 组件系统 |
| `client/ui-subagent` | 不需要 | Web subagent 功能、谱系导航、SessionRuntime.openSubagent 调用属浏览器 UI |
| `client/ui-theme` | 不需要 | 主题插件 --dsw-* token、五张样式表由客户端 entry 导入、document 主题属性 |
| `client/ui-tool` | 不需要 | Client 工具展示插件、root 与子调用展示属 UI |
| `client/ui-trajectory` | 不需要 | Trajectory 按轮次组织事件记录表、分割线与标记属浏览器 UI |
| `client/ui-user-questions` | 不需要 | Web 提问功能、progress 导航、单选/多选选项属浏览器交互 |
| `client/ui-workflow-run` | 不需要 | 顶层工作流运行重建为 Chat 节点属 UI、成员打开子 Session 属受控 |
| `client/ui-workspace` | **不需要** | `client/*` 整支是浏览器 DOM / React 表层 |
| `client/web` | 不需要 | Web 启动内核、window.__ModuleLoader__ 与 Cordis Loader 挂载属浏览器 |

### `fs/`（7）— 需要 1、不需要 6

| 包 | 裁决 | 理由 |
|---|---|---|
| `fs/fs` | **需要** | 纯接缝（Service Definition），方法签名不规定 target 在哪，无本机前置；后端挂对象存储（S3 / MinIO），见 `DESIGN.md` 第七节 |
| `fs/fs-local` | 不需要 | 本地文件系统实现（1827-1832），读写本机文件系统，违反硬约束 |
| `fs/fs-observation-policy` | **不需要** | 控制 fs 读取往模型上下文里注入什么。但 `tool-fs` 不装，模型根本碰不到 fs，`fs` 只被服务进程自己用——没有可观察的东西 |
| `fs/fs-sandbox` | 不需要 | 沙箱约束文件系统（1857-1860），继承LocalFileSystem读写本机，违反硬约束 |
| `fs/tool-fs` | 不需要 | 面向模型文件工具（1869-1876），消费ctx.fs读写本机文件，违反硬约束 |
| `fs/tool-fs-search` | 不需要 | ripgrep搜索工具（1889-1893），搜索本机文件系统，违反硬约束 |
| `fs/tool-str-replace-editor` | 不需要 | 文件编辑工具（1904-1914），查看编辑本机文件，违反硬约束 |

### `shell/`（10）— 不需要 10

| 包 | 裁决 | 理由 |
|---|---|---|
| `shell/shell` | **不需要** | 接缝本身零本机前置，但它的语义是执行命令。沙箱：执行命令/代码/终端的前置是沙箱，服务端不提供沙箱（消费方裁定）
| `shell/shell-env` | **不需要** | 只服务 `shell`，随它一起出局
| `shell/bash-local` | 不需要 | 本地bash实现（2038-2045），启动bash子进程，违反硬约束 |
| `shell/bash-sandbox` | 不需要 | 沙箱bash（2054-2060），消费ctx.sandbox限制spawn，底层依赖bash-local |
| `shell/pwsh-local` | 不需要 | PowerShell实现（2070-2084），启动pwsh子进程，违反硬约束 |
| `shell/pwsh-sandbox` | 不需要 | 沙箱PowerShell（2090-2094），消费ctx.sandbox限制spawn，底层依赖pwsh-local |
| `shell/tool-bash` | 不需要 | bash工具（2120-2129），执行本机bash命令，违反硬约束 |
| `shell/tool-bash-persistent` | 不需要 | 持久bash工具（2135-2143），保持本机shell会话，违反硬约束 |
| `shell/tool-pwsh` | 不需要 | PowerShell工具（2149-2157），执行本机PowerShell，违反硬约束 |
| `shell/tool-pwsh-persistent` | 不需要 | 持久PowerShell工具（2163-2170），保持本机shell会话，违反硬约束 |

### `sandbox/`（4）— 不需要 4

| 包 | 裁决 | 理由 |
|---|---|---|
| `sandbox/sandbox` | **不需要** | 沙箱接缝。服务端不提供沙箱（消费方裁定），接缝没有实现方
| `sandbox/sandbox-local` | 不需要 | 本机沙箱实现（1980-1986），使用bwrap/Landlock/Seatbelt/ACL，违反硬约束 |
| `sandbox/sandbox-policy` | **不需要** | 只服务 `sandbox`，随它一起出局
| `sandbox/sandbox-windows-acl` | 不需要 | Windows ACL沙箱实现（2011-2015），修改本机文件系统ACL，违反硬约束 |

### `terminal/`（3）— 不需要 3

| 包 | 裁决 | 理由 |
|---|---|---|
| `terminal/terminal` | **不需要** | 交互式终端＝长活的 shell 会话。沙箱：执行命令/代码/终端的前置是沙箱，服务端不提供沙箱（消费方裁定）
| `terminal/terminal-bash` | 不需要 | PTY后端实现（2220-2231），启动交互bash PTY，违反硬约束 |
| `terminal/tool-terminal` | 不需要 | 终端工具（2237-2244），提供6个交互式终端工具，违反硬约束 |

### `subprocess/`（2）— 不需要 2

| 包 | 裁决 | 理由 |
|---|---|---|
| `subprocess/subprocess` | **不需要** | 起进程。沙箱：执行命令/代码/终端的前置是沙箱，服务端不提供沙箱（消费方裁定）
| `subprocess/subprocess-local` | 不需要 | 本地子进程实现（2191-2198），启动本机子进程，违反硬约束 |

### `code-runtime/`（3）— 不需要 3

| 包 | 裁决 | 理由 |
|---|---|---|
| `code-runtime/code-runtime` | **不需要** | 执行代码。沙箱：执行命令/代码/终端的前置是沙箱，服务端不提供沙箱（消费方裁定）
| `code-runtime/code-runtime-python` | 不需要 | CPython子进程实现（1722），启动本机CPython子进程，违反硬约束"不开放本机资源" |
| `code-runtime/code-runtime-worker-thread` | 不需要 | Worker线程实现（1734-1735），程序派生OS进程存活于程序终止后（1744），违反硬约束 |

### `lsp/`（3）— 不需要 3

| 包 | 裁决 | 理由 |
|---|---|---|
| `lsp/lsp` | **不需要** | LSP 服务器要以子进程方式起。沙箱：执行命令/代码/终端的前置是沙箱，服务端不提供沙箱（消费方裁定）
| `lsp/lsp-stdio` | 不需要 | stdio语言服务器实现（1933-1939），每个服务器启动本机进程，违反硬约束 |
| `lsp/tool-lsp` | 不需要 | LSP工具（1950-1957），消费ctx.lsp提供代码导航，依赖lsp-stdio |

### `e2b/`（3）— 不需要 3

| 包 | 裁决 | 理由 |
|---|---|---|
| `e2b/e2b` | **不需要** | E2B 本身就是远程沙箱。服务端不做沙箱（消费方裁定）
| `e2b/fs-e2b` | 不需要 | E2B文件系统实现（1767），虽非本机但向E2B远程服务读写，五条前提不要求文件系统 |
| `e2b/subprocess-e2b` | **不需要** | E2B 远程沙箱的子进程实现，五条前提不要求子进程 |

