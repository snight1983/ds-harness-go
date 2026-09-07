# 逐包裁决

一包一行，四种取值。依据是 `functions.md` 那 2009 条和 `required.md` 那五条前提，
不是包名。

对着 `deepseek-harness-dsh-v0.1.2-alpha.3` 快照，当前 **250 个包**；表里另有 **7 行**是
上游后来删掉的包，保留不删（理由见第零节），所以表长 257 行。

| 取值 | 含义 | 数 |
|---|---|---|
| **需要** | 移过来 | 83 |
| **抄形状** | 不移这个包，抄它的接口／装配顺序／协议形状 | 23 |
| **Go 已有等价物** | 行为要，手段不要——Go 标准库里是白送的 | 8 |
| **不需要** | 有一个我们没有的前置条件 | 143 |
| **说不清** | 缺信息，理由里写明缺的是什么 | **0** |

「不需要」不等于「这功能没用」，等于**它有一个我们给不了的前置**（本机磁盘、桌面对话框、
浏览器 DOM、Node worker thread、本机子进程、**沙箱**）。

**说不清已经清零。**「说不清」当时的含义是「这五条前提没要求它，要不要由消费方定」，
不是「不要」。257 行现在每一行都有终判。

## 零、这一版对着哪个快照重对过（2026-09-04）

**前 227 行是对着一份更旧的 DSH 判的。** 重跑 `internal/devtools/capmap` 抽当前快照，
数出来是 250 个包，不是 227——多 30 个、少 7 个，另有 28 个包体量变化超过四分之一。
判断的依据变了，判断就得重看，否则表上写的「不需要」是在说一个已经不长那样的包。

**7 个消失的包里只有 1 个是真删，其余 6 个是上游自己重组。** 逐个对过去向：

| 消失的包 | 原裁决 | 去了哪 |
|---|---|---|
| `host/apiproxy` | 抄形状 | 拆成 `api/session-controller`＋`api/settings-controller`＋`api/workspace-controller` |
| `examples/acp-demo` | 抄形状 | 换成 `bundle/acp-app` 的 patch 清单 |
| `examples/agent-spine-demo` | 抄形状 | 换成 `bundle/base/cordis.patch.yml`（86 行有序挂载）＋`bundle/sdk-minimal` |
| `examples/jsonrpc-demo` | 抄形状 | 换成 `bundle/sdk-app` 的 patch 清单 |
| `client/runtime` | 不需要 | 拆成 `client/store`＋`client/ui-session`，仍然是浏览器 |
| `test-support/acp-snapshot` | 需要 | 扩成 `test-support/session-snapshot`（ACP 只是它的一个适配器） |
| `session/session-persistence-sqlite` | 不需要 | **真删了**，上游只留 JSONL 一个落盘实现 |

这 7 行不删。删掉的话，「我们当初判过它」这件事就没人看得见了，而下一个人会重新判一遍。

**`examples/agent-spine-demo` 的消失牵动 14 行裁决**，因为那 14 行的理由都写着
「DSH 自己的主干挂载清单（`examples/agent-spine-demo`）」。核过替代出处，**14 行一行没翻**：
`bundle/base/cordis.patch.yml` 那 86 行按 id 有序列着每个插件和它的配置，比一个示例应用
更硬；`bundle/sdk-minimal` 自陈「不叠在 dsh-base 上，这一份 insert 就是完整的 Cordis 树」，
33 行，是当前快照里作者亲手定义的「跑一个 agent 最少要装什么」。理由列已改指这两处。

**28 个体量漂移的包里，10 个当初判了要（需要 7＋抄形状 3）**，那批的判断依据可能已经不成立，
逐个记在第五节。剩下 18 个判的是「不需要」，理由是缺前置条件（浏览器、本机磁盘、本机进程），
体量变大不会长出一块硬盘来，所以不重判。

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

### 2.1 装配顺序（出处 `bundle/base/cordis.patch.yml` + `bundle/sdk-minimal` + `bundle/headless`）

**出处换过一次。** 原来引的是 `examples/agent-spine-demo`，上游已经删了那整棵 `examples/`
树，现在作者把「装什么、按什么顺序装、每个插件配什么」直接写在 bundle 的 patch 文件里——
那是可执行的清单，比示例应用更硬。

- `bundle/base/cordis.patch.yml`：**86 行按 id 有序**，共享核心的完整挂载清单，每行带配置。
- `bundle/sdk-minimal/cordis.patch.yml`：**33 行**，自陈「不叠在 dsh-base 上，这一份 insert
  就是完整的 Cordis 树」。这是当前快照里唯一一份作者亲口说「完整」的最小树：
  ```
  sdk-app → sdk-jsonrpc-server → llm-deepseek → sandbox-local → session-projection
    → sandbox-policy → subprocess-local → terminal → fs-local
    → timer → llm → session → session-title → system-prompt → tools → agent
    → llm-retry → jobs-local → invariants（＋session/agent/scope/agent-loop 各自的 invariant）
    → agent-loop → 三个工具 → session-persistence-jsonl
  ```
  **`agent-loop` 排在几乎最后**，`agent` 在它前面十几行——这不是笔误，是因为 cordis 按服务
  可用性激活，不按行序加载。抄的时候别把行序当成初始化序。

`headless` 那份连启动步骤都记全了：

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

- **说不清已经清零**：最后那批 20 个由消费方逐条定完，257 行全部有终判。
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

### `experimental/`（8）— 抄形状 2、不需要 6

| 包 | 裁决 | 理由 |
|---|---|---|
| `experimental/agent-team-profile` | 不需要 | cordis profile bundle，34 行，内容是一份「装哪几个插件」的清单。接缝 `agent-team` 已判抄形状，装配清单本身不是能力 |
| `experimental/agent-team-web-profile` | 不需要 | 同上，而且它挂的是浏览器面板 |
| `experimental/client-ui-agent-team` | 不需要 | 浏览器里的 roster／任务板／队友导航面板，前置是浏览器 |
| `experimental/inspector` | 不需要 | 跨 realm 的 Chrome DevTools Protocol 调试中枢：Console 求值、Sources、Network 抓包、Elements 树。前置是 CDP 与浏览器 realm，两样都没有 |
| `experimental/webworker-packer` | 不需要 | 给浏览器运行时打 VFS 镜像的构建期工具，前置是浏览器 |
| `experimental/webworker-runtime` | 不需要 | 纯浏览器运行时：内存 VFS、模块变换、postMessage 隧道，外加一层让宿主树原样跑起来的 Node 兼容层。前置是浏览器 Web Worker |
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
| `jobs/jobs-local` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`bundle/base/cordis.patch.yml` 与 `bundle/sdk-minimal`），作者定义的「跑一个 agent 最少要装什么」 |
| `jobs/tool-jobs` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`bundle/base/cordis.patch.yml` 与 `bundle/sdk-minimal`），作者定义的「跑一个 agent 最少要装什么」 |

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

### `util/`（12）— 需要 2、Go 已有等价物 7、不需要 3

**这一支是「Go 已有等价物」最密的地方，五个新包全落在这一档。** 它们解决的都是
JS 运行时自己的缺口：没有 UUID（安全上下文外 `crypto.randomUUID` 不可用）、没有双端队列、
没有时区校验、没有防原型伪造的 JSON 快照、没有平台无关的路径拼接。Go 这五样都是标准库。

| 包 | 裁决 | 理由 |
|---|---|---|
| `util/crypto` | **Go 已有等价物** | 浏览器安全的 UUID 与字节编码。Go：`crypto/rand` + `encoding/hex`／`encoding/base64` |
| `util/deque` | **Go 已有等价物** | 摊还常数时间的循环双端队列，还要自己管空位释放。Go：切片本身就是，`container/list` 也在标准库 |
| `util/time` | **Go 已有等价物** | 只做 IANA 时区名的校验与规范化，不做格式化。Go：`time.LoadLocation` 校验，`Location.String()` 规范化 |
| `util/values` | **Go 已有等价物** | 无损 JSON 校验、脱钩快照、深冻结、结构相等、穷尽联合。整包在防 JS 对象图的危险（伪造原型、取值器、稀疏数组、环、`-0`、非有限数），Go 里这些要么不存在要么 `encoding/json` 自己就拒。裁决依据与逐条对照已写在 `sessionlog/doc.go` 里 |
| `util/workspace-path` | **Go 已有等价物** | 相对路径拼接、POSIX home 缩写、显示标题。Go：`path` 包（注意不是 `path/filepath`，本仓库 `oscheck` 禁它） |
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
| `runtime-diagnostics/invariants` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`bundle/base/cordis.patch.yml` 与 `bundle/sdk-minimal`），作者定义的「跑一个 agent 最少要装什么」 |

### `test-support/`（7）— 需要 6、不需要 1

| 包 | 裁决 | 理由 |
|---|---|---|
| `test-support/session-snapshot` | 需要 | **`acp-snapshot` 的继任者**，上游把它扩成了通用的会话日志快照核心，ACP 只是它的一个协议适配器。要的是三样：清单化的夹具、身份脱敏（`identity redaction`）、期望输出归一化——没有归一化，任何带时间戳和 ID 的会话日志都没法做快照比对。**它的 workspace 文件快照那部分不要**（二进制文件、符号链接、空目录），那部分的前置是本机文件系统。<br><br>**已落地在 `sessionlog/snapshot`**，三样齐了：`ParseManifest` 读那张归属声明，`RedactIDs` 把标识换成按类型编号的记号，`Normalize` 归零时钟、把请求头膨胀换成记号、不看落盘边界重打包分块。没有移的有四处：workspace 文件快照（如上）；headless／SDK／ACP／Web 四个协议适配器和那个 vitest 套件工厂（都要起子进程，且绑死一个 JS 测试框架）；整套 cwd 归一化（上游把宿主绝对路径钉在会话头上，本仓库那一格是不透明的 `WorkspaceID`，macOS 的 `/private` 别名和 Windows 的长短路径两种拼法在这里都无从谈起）；JSON-RPC 转写稿归一化（跟着子进程一起没有移）。另有一处与上游不同：上游的记号编号跟着 JS 对象的插入顺序走，Go 的 map 迭代顺序是随机的，照抄会让同一份日志压两次得到两套编号，所以改成按键名字典序认领——编号和上游对不上，但它是确定的，而确定正是这个包存在的理由 |
| `test-support/acp-snapshot` | 需要 | **上游已删**，扩成了 `test-support/session-snapshot`。原判理由：ACP快照测试harness。"launchAcpTestAgent启动器、通过SDK客户端收集会话、runScenario驱动、normalizer + scrubber + defineAcpSnapshotSuite"。如果你用ACP，需要能运行集成测试验证round-trip行为。这个工具让你在不连真model下跑完整agent回合（见下）。 |
| `test-support/agent-loop-testkit` | 需要 | agent loop测试依赖挂载工具。"mountAgentLoopTestDependencies按序挂LLM、session、system-prompt、tools、agent"。你需要能在单元/集成测试中隔离地测试loop逻辑。这直接支持"跨天活跨进程活下来"的持久化测试。<br><br>**已落地在 `harness/harnesstest`**，三处与上游不同：上游把五样挂到 cordis 上下文上、函数本身返回 void，本仓库没有那张服务表，于是显式交回一份结构体；多透了作用域与时钟两项配置——作用域在 Go 里是显式的值，而真时钟会让同一毫秒里落的两条事件拿到相同时间戳、快照比对因此不稳；日志默认丢掉而不是走 `slog.Default()`，因为一次 `go test ./...` 里这套骨架会被立起来上千次，默认那个 logger 会把用例真正的失败信息淹掉 |
| `test-support/client-runtime` | **不需要** | cordis + jsdom 的浏览器测试脚手架，前置是 DOM |
| `test-support/llm-mock-server` | 需要 | 可编脚本OpenAI兼容mock HTTP服务器。"行为脚本(connection_reset/stream_disconnect/.../success/tool_call_success)、时序与内容控制"。这是**不连真模型情况下跑完整round-trip**的工具——正是你需要的。它让"跨天活"的测试不依赖API key和配额。 |
| `test-support/llm-replay` | 需要 | 无密钥快照测试的LLM回放插件。"根据已记录session JSONL fixture重建模型流、installLlmReplay返回ReplayHandle"。这是**不连真模型跑回合**的主要方式——用既有fixture驱动测试，省掉真实API成本。条目"首次调用顺序脚本绑定假设串行委托、只有普通loop分片和标记本地压缩输出能派生"——限制在"什么场景能用"，不是"用不了"。 |
| `test-support/loader-smoke` | 需要 | 烟雾测试harness。"resolveExampleLaunch、runLoaderSmoke、runFixtureTurn单轮驱动"。这是"启动 + 执行single turn + 查收output"的端到端脚手架。你需要它验证"应用能启动、能跑、能shutdown"的完整周期。**那条断言链已落地在 `harness/smoketest`**；子进程那一半没有移：上游启动的是一棵 `cordis.yml` 装出来的树，靠 `DSH_EXAMPLE_MODE` 在「tsx 跑 src」和「node 跑 lib」之间二选一，Go 里没有 Loader、没有那份配置文件，也没有源码态与构建态两条启动路径。剩下的「驱一轮、收最终文本和用量」和宿主是不是子进程无关，那部分照抄了，包括那道「看见自己那条消息进收件箱才开始记账」的闸。 |

### `examples/`（3）— 抄形状 3 · **整支上游已删**

三个包连同 `packages/examples/` 整棵树在当前快照里都不存在了。行保留，因为裁决本身没错，
错的只是出处——装配顺序现在读 `bundle/*/cordis.patch.yml`，见第 2.1 节。

| 包 | 裁决 | 理由 |
|---|---|---|
| `examples/acp-demo` | **抄形状** | **上游已删**，换成 `bundle/acp-app` 的 patch 清单。原判：示例应用，价值在它记录的装配顺序，不在代码本身 |
| `examples/agent-spine-demo` | **抄形状** | **上游已删**，换成 `bundle/base/cordis.patch.yml` 与 `bundle/sdk-minimal`。原判：示例应用，价值在它记录的装配顺序，不在代码本身 |
| `examples/jsonrpc-demo` | **抄形状** | **上游已删**，换成 `bundle/sdk-app` 的 patch 清单。原判：示例应用，价值在它记录的装配顺序，不在代码本身 |

### `llm/`（7）— 需要 4、抄形状 1、不需要 2

| 包 | 裁决 | 理由 |
|---|---|---|
| `llm/deepseek-llm-api-extensions` | **抄形状** | 「附加请求字段注册表」：让别的插件往模型请求的**顶层**塞自己的字段，字段的生命周期归贡献方管。这个形状我们要——`adapter/openaicompat` 现在没有任何让宿主追加提供方私有字段的接缝，宿主要加一个字段就得改适配器。要的是注册表这个形状，不是 DeepSeek 官方接口那套具体字段 |
| `llm/plugin-package-inventory-deepseek` | 不需要 | 靠 cordis Loader 反查「当前进程装了哪些插件包」，再作为元数据附进官方 DeepSeek 请求。两个前置都没有：cordis 插件容器、官方接口 |
| `llm/llm` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`bundle/base/cordis.patch.yml` 与 `bundle/sdk-minimal`），作者定义的「跑一个 agent 最少要装什么」 |
| `llm/llm-deepseek` | 不需要 | 依赖 DeepSeek 官方接口，题目明确不走官方接口 |
| `llm/llm-pi-ai` | 需要 | 通用多提供方适配器支持 OpenAI 兼容协议，服务自建本地推理 |
| `llm/llm-retry` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`bundle/base/cordis.patch.yml` 与 `bundle/sdk-minimal`），作者定义的「跑一个 agent 最少要装什么」 |
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
| `skill/skill` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`bundle/base/cordis.patch.yml` 与 `bundle/sdk-minimal`），作者定义的「跑一个 agent 最少要装什么」 |
| `skill/skill-badge` | 不需要 | 仅内置 DSH 标志资源，不服务五条前提 |
| `skill/skill-filesystem` | 不需要 | 本地文件系统 skill 扫描，读写本地目录违反规则 |
| `skill/tool-skill` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`bundle/base/cordis.patch.yml` 与 `bundle/sdk-minimal`），作者定义的「跑一个 agent 最少要装什么」 |

### `interaction/`（5）— 需要 5

| 包 | 裁决 | 理由 |
|---|---|---|
| `interaction/commands` | 需要 | 用户命令注册表，前提3交互场景 |
| `interaction/permission-presets` | 需要 | 权限预设管理，前提1多用户并发。**落地时缺一角**：DSH 的 `PresetSpec` 捆 `sandbox` + `approval` 两个旋钮，且构造函数在执行器不约束时直接抛。沙箱那一整支（`sandbox/*`、`shell/bash-sandbox`、`shell/pwsh-sandbox`）本仓库全判为不需要，所以 Go 版本只捆审批策略一个旋钮。DSH 那两条默认预设（`workspace-write`、`danger-full-access`）**两个键都是照沙箱模式起的名**，照抄等于给用户看一个名叫「完全访问」却根本不管文件访问的选项，所以 Go 这边不带默认表，预设表必填。计划模式没有折进来当第二个旋钮——DSH 是刻意把它挡在这个捆包外面的。剩下一个旋钮时这张表仍然不多余：部署方起名的档位单、钉进新会话的默认选择、捆包打平手时保住用户意图的那条日志事实、界面投影、`/permission` 命令，以及 webhook 那一侧要的可命名手柄 |
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
| `core/agent` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`bundle/base/cordis.patch.yml` 与 `bundle/sdk-minimal`），作者定义的「跑一个 agent 最少要装什么」 |
| `core/agent-default-model` | **需要** | 记住默认用哪个模型：`currentSelection()` / `saveSelection()`，一份 `{provider, model, reasoningEffort}`，创建 agent 时没指定就用它。不装的话每开一个会话都得由调用方点名模型。**它自陈只有一项进程级默认值**，「每个会话的选择仍由入口负责」——多用户下「这个用户偏好哪个模型」是每人各自的，要在它上面按用户叠一层（和 `credentials` 同一个归属做法） |
| `core/agent-loop` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`bundle/base/cordis.patch.yml` 与 `bundle/sdk-minimal`），作者定义的「跑一个 agent 最少要装什么」 |
| `core/agent-tool-presentation` | **不需要** | 它的职责是在 `native` / `code` / `both` 三者里选一个。`code` 类模式要 `ctx.codeRuntime`（`code-runtime-worker-thread`，已判不要），所以只剩 `native` 一个可选值——而 `native` 本来就是 `dsh-tools` 的默认。一行插件选唯一值，等于不装 |
| `core/scope` | 需要 | required.md 第二层第 3 组（前提 1 多用户）：`ScopedLayers`：全局层 + 作用域链，近的层盖远的层 |
| `core/session` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`bundle/base/cordis.patch.yml` 与 `bundle/sdk-minimal`），作者定义的「跑一个 agent 最少要装什么」 |
| `core/system-prompt` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`bundle/base/cordis.patch.yml` 与 `bundle/sdk-minimal`），作者定义的「跑一个 agent 最少要装什么」 |
| `core/tools` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`bundle/base/cordis.patch.yml` 与 `bundle/sdk-minimal`），作者定义的「跑一个 agent 最少要装什么」 |

### `session/`（15）— 需要 11、不需要 4

| 包 | 裁决 | 理由 |
|---|---|---|
| `session/session-turn-outline` | **需要** | 整份日志的回合大纲投影（`turnOutline`）：每个回合一条，够客户端做全会话回合导航而不必拉整份日志。**零前置条件**——它是纯投影，输入是事件日志，输出是一个列表。我们的 `sessionlog/projection` 没有这个单元，而「几千轮的会话，客户端要能跳到第 300 轮」这件事在服务端比在单机 CLI 更要紧 |
| `session/session-log-deepseek` | 不需要 | 把会话日志增量无损上传进 DeepSeek 官方请求的元数据字段。两个前置都没有：官方接口、`llm/deepseek-llm-api-extensions` 那套具体字段。**注意别和 `llm/deepseek-llm-api-extensions` 混了**——那个判了抄形状，要的是注册表形状；这个是往注册表里塞的一条具体内容 |
| `session/session-checkpoint-policy` | 需要 | required.md 第二层第 2 组（前提 2/3/4 会话可恢复）：三个落盘点决定「崩在哪儿丢多少」 |
| `session/session-persistence` | 需要 | required.md 第二层第 2 组（前提 2/3/4 会话可恢复）：接缝本身：`create/append/prepare/load/readFrom/list` |
| `session/session-persistence-jsonl` | 不需要 | "项目目录保留规范化 cwd 的可读形式"、"平铺文件布局不加载"、"不删除会话文件"、"POSIX 需硬链接支持"——全部依赖本机文件系统与路径操作，消费方已决定用 Postgres 后端。 |
| `session/session-persistence-sqlite` | 不需要 | **上游已删**，是 7 个消失的包里唯一一个真删、没有继任者的——当前快照只剩 JSONL 一个落盘实现。原判："SQLite 会话存储后端"、"物理打包行存储"——服务端已定用 Postgres，此为本机 SQLite 实现。 |
| `session/session-projection` | 需要 | required.md 第二层第 2 组（前提 2/3/4 会话可恢复）：事件日志投影成喂给模型的消息序列 |
| `session/session-projection-cache` | 需要 | 文件明写"持久投影缓存"、"每会话一条记录"在 storage-domain 中落地，跨进程恢复投影状态直接服务"进程重启后活要跨天活下来"。 |
| `session/session-stats` | **需要** | 每会话的 turn / step / token 计数投影。多用户服务里钱是平台的，而 `goal/*` 自陈不管预算——这是那个预算闸门的数据源 |
| `session/session-telemetry` | **需要** | 遥测外发接缝（emit / flush / shutdown），零本机前置。DSH 那个往本机日志导出的实现不要，后端换成 OTLP over HTTP——和 `mcp` 只取 `streamable-http` 是同一条界线 |
| `session/session-telemetry-otel` | 不需要 | "OpenTelemetry 后端"、"OTLP/HTTP 导出"纯粹向外部服务投递遥测，与服务端 agent 运行时无关。 |
| `session/session-title` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`bundle/base/cordis.patch.yml` 与 `bundle/sdk-minimal`），作者定义的「跑一个 agent 最少要装什么」 |
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
| `context/agent-instructions` | 需要 | required.md 第二层第 1 组：DSH 自己的主干挂载清单（`bundle/base/cordis.patch.yml` 与 `bundle/sdk-minimal`），作者定义的「跑一个 agent 最少要装什么」 |
| `context/file-reference` | 不需要 | "@file 语法"、"文件补全查询"依赖工作区文件导航，但服务端"不开放本机资源"禁止 agent 访问文件系统。 |
| `context/file-reference-local` | 不需要 | "本地文件系统实现"明确"工作区索引"、"cwd 为根目录"依赖本地文件系统遍历，违反"服务端不开放本机资源"。 |
| `context/session-reference` | 需要 | "@[label](uri) 跨会话 mention"、"prepare/listCandidates"直接支撑多 agent 协作时对其他会话上下文的注入，服务前提 5。 |
| `context/time-context` | **需要** | 往系统提示词里注入当前时间。agent 不知道今天几号是真缺陷。DSH 采的是浏览器时区，**我们改成由消费方传时区**——服务端没有「当前用户的浏览器」 |
| `context/tmux-context` | 不需要 | "Tmux 位置上下文"通过 ctx.shell 读取 tmux 状态，文件明写"仅第一个步骤"且"仅自身位置"，纯粹桌面/终端能力，服务端无 tmux 环境。 |

### `host/`（8）— 抄形状 1、不需要 7

| 包 | 裁决 | 理由 |
|---|---|---|
| `host/apiproxy` | **抄形状** | **上游已删**，拆成 `api/` 底下三个控制器，裁决已继承过去（见 `api/` 那一组）。原判：会话管理 / 历史分页 / 投影推送 / 待处理队列 / 后台任务——这份方法清单就是对外 API 的形状，值得照抄；但 DSH 的实现绑在 cordis Remote 上，不移植 |
| `host/directory-picker` | **不需要** | 本机目录选择能力，服务端不开放本机资源。DESIGN.md 第六节已删 `dirpicker/` |
| `host/directory-picker-auto` | **不需要** | 只是在上面两个后端之间选，两个后端都不要，它没有可选对象 |
| `host/directory-picker-browse` | **不需要** | 同上；`host.listDirectory/createDirectory` 直接读写服务器磁盘。DESIGN.md 第六节已删 `dirbrowse/` |
| `host/directory-picker-native` | **不需要** | 依赖 osascript / zenity / kdialog 桌面工具 |
| `host/frontend-static` | **不需要** | 发 SPA dist 的静态文件服务器。DESIGN.md 第六节已删 `frontendstatic/` |
| `host/plugin-inventory` | **不需要** | cordis 插件加载状态的只读投影，我们不要 cordis 那个容器 |
| `host/webserver` | **不需要** | DSH 的 HTTP/upgrade 路由注册与端口绑定。DESIGN.md 第六节已删 `webserver/` |

### `boot/`（2）— 不需要 2

| 包 | 裁决 | 理由 |
|---|---|---|
| `boot/app-boot` | **不需要** | **本轮改判，原判「需要」。** 改的理由不是 DSH 变了，是这一行和符号账本打架：`portmap.tsv` 里这个包 66 个符号**全部** `GO_NATIVE`，理由写的是「cordis Loader 的启动胶水，Go 里这件事是 main() 里写构造函数」。原来那条「需要」给的是五条前提的泛论，没落到任何一个符号上。见第五节 |
| `boot/cmdline` | **不需要** | 桌面启动器的命令行参数注入（`ctx.cmdlineArgs` / `ctx.appExit`），服务端的入参走 HTTP 不走 argv |

### `bundle/`（6）— 抄形状 6

**`examples/` 删掉之后，这一支成了装配顺序的唯一出处。** 每个 bundle 都是一份
`cordis.patch.yml`，按 id 列着装什么、配什么，代码只有几十行——**值钱的是那份 yml，不是代码**。

| 包 | 裁决 | 理由 |
|---|---|---|
| `bundle/base` | **抄形状** | 共享核心插件行，**86 行有序挂载**，是当前快照里最全的一份装配清单。要抄的是这份顺序，不是这个 bundle。见第 2.1 节 |
| `bundle/sdk-minimal` | **抄形状** | **当前快照里唯一一份作者亲口说「完整」的最小树**：自陈「不叠在 dsh-base 上，这一份 insert 就是完整的 Cordis 树」，33 行。它顶替了已删的 `examples/agent-spine-demo`，而且比后者硬——示例应用可以省事，这个是真跑的 profile |
| `bundle/sdk-app` | **抄形状** | SDK 的 stdio JSON-RPC 进程外壳，90 行。顶替已删的 `examples/jsonrpc-demo`。我们走 HTTP 不走 stdio，要的只是「协议服务端 + 进程生命周期」这两件的分界 |
| `bundle/acp-app` | **抄形状** | ACP 的 stdio 外壳，76 行。顶替已删的 `examples/acp-demo`。patch 里有一条值得记：它**显式关掉 `session-title-llm`**——自动化场景不该为起标题额外烧一次模型调用 |
| `bundle/headless` | **抄形状** | DSH 自陈的第二份「最小可跑」定义，见第 2.1 节 |
| `bundle/web-app` | **抄形状** | 浏览器表层组合，里面挂的 webserver / web-runtime 都判了不需要 |

### `extensions/`（4）— 不需要 4

| 包 | 裁决 | 理由 |
|---|---|---|
| `extensions/cordis-client-runner` | **不需要** | 浏览器端动态包装载，前置是浏览器页面 |
| `extensions/cordis-host-runner` | **不需要** | 宿主端动态包运行时，靠 vm 沙箱执行，且「带浏览器半的包在没有页面连接时挂起」。执行 + 浏览器两个前置都给不了 |
| `extensions/tool-cordis` | **不需要** | 让模型在运行时定义并执行插件（`cordis_define` / `run`），正是我们判掉的那件事：执行任意代码的前置是沙箱 |
| `extensions/ui-cordis` | 不需要 | 浏览器侧cordis UI面板。"覆盖整个框架的面板"、"shell.overlay条目"、"卡片在对话流里"——全是DSH Web桌面UI的表现层。你说有自己前端且无桌面UI，这整个包是Web exclusive。 |

### `hooks/`（3）— 不需要 3

| 包 | 裁决 | 理由 |
|---|---|---|
| `hooks/hook-protocol` | **不需要** | **本轮改判，原判「需要」。** 改的理由和 `boot/app-boot` 那条一样：这一行和符号账本打架，而且打不过。`portmap.tsv` 里这个包 49 个符号**全部** `OUT_OF_SCOPE`，`docs/DESIGN.md` 第四节也早写了「整包出局」——只有这一行还是「需要」。读源码后符号账本是对的：`src/runner.ts` 第一句就 `import type { ShellExecutor } from '@deepseek-ai/dsh-shell'`，整个包的动作是**经 `ctx.shell` 在本机跑用户写的命令**；`types.ts` 的 `HookDialect` 和 `matcher.ts` 的 `MatcherMode` 都只有 `'claude-code' \| 'codex'` 两个取值，`codec.ts` 解的是这两个产品的 `hookSpecificOutput`。`shell/*` 全族已因「服务端不提供沙箱」出局，执行器随之没有；两个消费方也已出局。原判给的是「hook 作为可选 extension 不违反前提」的泛论，没落到任何一个符号上 |
| `hooks/hooks-claude-code` | **不需要** | Claude Code hook 方言桥，只映射对方产品的钩子点 |
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

### `webhook/`（2）— 需要 1、抄形状 1

**上游新增的一支，而且它是少见的「服务端正命题」——DSH 其余部分都在假设有人坐在屏幕前，
这一支假设的正好相反：外部事件到了，没有人在场，自动开一个会话把活干了。**

| 包 | 裁决 | 理由 |
|---|---|---|
| `webhook/webhook` | **需要** | 规则运行时：一条规则把「什么外部事件」映射到「用哪个工作区、哪个预设、哪个模型、哪套权限，开一个会话跑什么提示词」，fire-and-forget。**零本机前置**，需要的东西（`workspace`／`agent-presets`／`agent-default-model`／`permission-presets`／`session-title`）我们全有。**已落地在 `feature/webhook`**，四处与上游不同：六个 cordis 服务改成五个窄接口加一个函数接缝（改标题那件事接口两头对不上）；会话与工作区的归属事实由装配方注入，本包不自己去查；失败记录多一个「回滚失败」维度，让「为什么开不成」和「收拾现场时又出了什么问题」分两条报；工作区路径的绝对路径断言删掉了——那条路径交给 `fs` 解析，而它背后可以是对象存储 |
| `webhook/webhook-github` | **抄形状** | GitHub 的签名校验与事件路由。要抄的是形状（HMAC 验签 → 解事件 → 交给规则运行时），不是这个包——它绑在 `host/webserver` 上，而本仓库不强制宿主用哪个 HTTP 框架。凭据取用要挂到 `credentials` 的归属校验上，理由同 `mcp/mcp-client` 那条 |

### `acp/`（1）— 需要 1

| 包 | 裁决 | 理由 |
|---|---|---|
| `acp/acp` | 需要 | 可创建新会话、接收提示词、返回已提交答案。5条前提都适用：多用户（一个连接多会话）、恢复（跨重启会话持久化）、中途离开（连接关闭重新取消）、跨天运行（可创建新会话）、多agent协作（subagent-acp生产客户端）。"已提交答案"与无逐token实时数据一致与设计。 |

### `api/`（5）— 抄形状 5

**这一支是已删的 `host/apiproxy` 拆出来的。** 原来那一个包判了抄形状，理由是
「会话管理／历史分页／投影推送／待处理队列／后台任务——这份方法清单就是对外 API 的形状」。
现在同一份清单拆成三个控制器，裁决跟着继承：**要的是方法表和状态传输的形状，不是 cordis Remote 的实现**。

| 包 | 裁决 | 理由 |
|---|---|---|
| `api/session-controller` | **抄形状** | 三个里最重的一个（7299 行 / 32 文件），会话控制面的完整方法表：create／resume／prompt／fork／rename／list／search／cancel／select-model／update-queue，外加历史分页（`SessionPage`／`SessionChunkRun`）和实时投影推送（`ProjectionsBaseline` + 增量）。**「baseline + 增量」这个形状是要点**：客户端断线重连时先要一份基线再续增量，不是从头重放整份日志。我们的 `protocol/sdk/sdkserver` 现在只有 prompt 一条路径，这份方法表是缺口清单 |
| `api/settings-controller` | **抄形状** | 设置与凭据的远程属主。**要的是「脱敏读」这一条**：凭据能被列出、能被引用、但读回来是打码的。我们的 `credentials` 有归属校验，没有这条读路径 |
| `api/workspace-controller` | **抄形状** | 工作区的远程命令与**断线重连安全**的状态传输（`WorkspaceBaseline` + `WorkspaceFollowSink`）。同样是 baseline + 增量的形状。它依赖的 `host-directory-picker` 是本机目录选择框，那一半不要 |
| `api/gateway` | **抄形状** | Host 侧注册业务能力 + Client 侧挂生成的贡献项，是协议形状；实现绑在 cordis 上。**体量从 1406 涨到 4381 行**，重看结论见第五节 |
| `api/remotes` | **抄形状** | 双侧 BFF 与身份解析，同上 |

### `sdk/`（3）— 需要 2、抄形状 1

| 包 | 裁决 | 理由 |
|---|---|---|
| `sdk/client` | **抄形状** | 「子进程方式驱动 Harness 运行时，走 stdio JSON-RPC」——子进程驱动这件事我们不做，客户端形状可抄 |
| `sdk/protocol` | 需要 | JSON-RPC wire format与协议类型定义。"按换行分帧的JSON-RPC 2.0、协议方法表(initialize/session/prompt/shutdown、session.event/status/subagent*)、错误响应"。这是你server与client的契约，所有多frontend都经过这套协议。 |
| `sdk/server` | 需要 | JSON-RPC服务器插件。"通过stdio提供JSON-RPC、initialize等待树加载完成、session/prompt排队、shutdown刷新退出、session.event流式发出、session.status全局转换"。你的服务进程必须expose协议——要么自己实现，要么用这个插件。条目"自动挂载适配器仅支持DeepSeek"是可配项，不是hard barrier。 |

### `client/`（45）— 不需要 45

整支的前置都是浏览器。五个新包不改变这一点，逐行列出只为让「一个都没漏判」看得见。

| 包 | 裁决 | 理由 |
|---|---|---|
| `client/store` | 不需要 | React-free 的可观察快照存储契约，底下是 Zustand/Immer。前置是浏览器 |
| `client/ui-approval` | 不需要 | 浏览器审批面板，答宿主的权限请求。审批**接缝**在 `interaction/user-approval`，已判需要；这是它的浏览器表现层 |
| `client/ui-chat` | 不需要 | 浏览器聊天界面，9167 行 / 70 文件，是 `client/` 里最大的一个。渲染会话对话节点、详情、历史图片、滚动位置 |
| `client/ui-schedule` | 不需要 | 会话头部那个只读的定时提醒目录。`feature/schedule` 已判需要，这是它的浏览器表现层 |
| `client/ui-session` | 不需要 | 会话控制器的 React 适配器与会话作用域插槽。**和已删的 `client/runtime` 是同一件事**——上游把它拆成了这个加 `client/store` |
| `client/connection` | 不需要 | 浏览器 HTTP POST 与双条只下行 WebSocket、Host 头信任栅栏 |
| `client/hmr` | 不需要 | 浏览器订阅 SSE 通道接收 rebuilt 帧，React 状态重载 |
| `client/locale` | 不需要 | 浏览器 navigator 语言、DSH_HOME/settings.yaml 仅限桌面 UI |
| `client/modules` | 不需要 | window.__ModuleLoader__ bundle 物化属浏览器侧模块加载 |
| `client/runtime` | 不需要 | **上游已删**，拆成 `client/store`＋`client/ui-session`，两个都还是浏览器。原判：SlotRegistry 与 SessionRuntime 拥有 Session，client-side 投影消费 |
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

### `subprocess/`（3）— 不需要 3

| 包 | 裁决 | 理由 |
|---|---|---|
| `subprocess/win32-process` | 不需要 | Win32 的进程、stdio 和 Job Object 底层原语，给 Windows ACL 沙箱用。三重前置全缺：本机进程、Windows、沙箱 |
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


## 五、体量漂移的 10 个包重看（2026-09-04）

第零节点出 28 个包体量变化超过四分之一，其中 **10 个当初判了要**（需要 7＋抄形状 3）。
这一节是那 10 个的重看结果。

**方法是比导出面，不是比行数。** 拿 `dsh-capabilities.md` 的上一版和这一版对着比
**导出的类与接口**——行数涨了不代表长出新能力，测试、注释、内部拆文件都会让它涨。
只有导出面变了，才说明作者对外承诺的东西变了。

| 包 | 老→新 | 导出面 | 结论 |
|---|---|---|---|
| `session/session-projection` | 559→734 | **没变** | 原判不动 |
| `attachment/attachment` | 385→484 | **没变** | 原判不动 |
| `bundle/headless` | 237→309 | **没变** | 原判不动 |
| `sdk/client` | 952→1201 | ＋2 | 涨的正是原判排掉的那块，原判更站得住 |
| `api/gateway` | 1406→4381 | ＋29 | 同上 |
| `boot/app-boot` | 1298→1756 | ＋2 | **改判**，但改的理由和漂移无关 |
| `acp/acp` | 847→1854 | ＋6 | 原判不动；揪出 5 行账本错记 |
| `subagent/tool-subagent` | 506→1257 | ＋5 | 原判不动；是一处真缺口 |
| `llm/token-meter` | 1027→1478 | ＋5 −1 | 原判不动；是两处真缺口 |
| `preset/agent-presets` | 1684→2495 | ＋7 −6 | 原判不动；**减掉的 6 个我们已经抄了** |

### 5.1 三个包白涨，一个符号没多

`session/session-projection`、`attachment/attachment`、`bundle/headless` 三个包体量涨了
四分之一到三分之一，导出的类和接口**一个没加也一个没减**。涨的全在包内部。
它们的原判依据是导出面，导出面没动，判断就没有重看的余地。

### 5.2 两个包涨的正好是原判排掉的那一半

- `sdk/client` 多出 `DshNodeLaunch` 和 `RuntimeProcessOptions`——「起一个 node 子进程来跑运行时」。
  原判「抄形状：子进程驱动这件事我们不做」排掉的就是这块，它长大了不改变我们没有本机进程这件事。
- `api/gateway` 多出一整层远程事件流复用（`RemoteStreamMuxClient`／`RemoteStreamMuxServer`／
  `RemoteJournalStream`／`RemoteSnapshotStream` 等 29 个符号），实现仍然绑在 cordis 上。
  原判「抄形状」指的就是「协议形状抄、cordis 实现不抄」，多出来的这层照样落在形状那一侧。

两条原判不但不翻，证据比原来更足。

### 5.3 重看真正揪出来的：两处账本自相矛盾

漂移本身一条判断都没翻。但为了核对漂移，把这 10 个包的符号行逐条读了一遍，
撞出两处**账本内部打架**——这两处和 DSH 变没变无关，是我们自己的记录出了错。

#### （1）`acp/acp` 有 5 行写着「我们没做」，其实做了

| 符号 | `portmap.tsv` 现在写的 | Go 里实际在哪 |
|---|---|---|
| `AcpMcpConfigError` | SKIP：「按会话 MCP 服务器整体不承诺」 | `protocol/acp/mcp.go` 的 `MCPConfigError` |
| `mountAcpMcpServers` | SKIP：「不承诺按会话挂载 MCP」 | `protocol/acp/mcp.go` 的 `MountMCPServers` |
| `AcpModelConfigError` | SKIP：「一个会话配置项都不摆」 | `protocol/acp/modelcontrol.go` 的 `ModelConfigError` |
| `AcpModelControl` | SKIP：「模型路由固定在构造时，运行期不可改」 | `protocol/acp/modelcontrol.go` 的 `ModelControl` |
| `ResumeAcpSessionOptions` | SKIP：「不声明 `sessionCapabilities.resume`」 | `Bridge.ResumeSession` |

这 5 行的理由列还在说 `SetSessionConfigOption` 回 `MethodNotFound`、`resume` 能力不声明，
而 `protocol/acp/bridge.go` 现在两样都声明了，`Bridge.SetSessionConfigOption` 也真的在改配置项。
**这 5 行的出处路径写的还是 `acp/acp/...`**，那是 `protocol/` 那次搬家之前的坐标——
写下它们的时候是对的，后来我们把活干了，账本没跟上。

**这类错比漏判更糟。** 漏判是「有个坑」，这类是「账本说有坑，其实没有」——照着它走，
会有人去做一件已经做完的事，做完发现重了，然后开始怀疑整张表。

处置：这 5 行改 `PORTED` 并填 `go_ref`。

#### （2）`boot/app-boot` 的包级裁决和它 66 个符号全反着

包级写着「需要：启动路径、环境加载、故障处理、Profile 机制」，而 `portmap.tsv` 里这个包
**66 个符号全部 `GO_NATIVE`**，理由一致写着「cordis Loader 的启动胶水……Go 里这件事是
main() 里写构造函数，编译期就查得出来，不需要包」。

哪一边对？符号那边。它落到了具体的 `node:fs`／`~/.dsh/profiles`／`process.exit(1)`／
cordis Loader 上；包级那条给的是五条前提的泛论，没落到任何一个符号。而且我们的
`harness/` 底下确实一个 profile 机制都没有，也不打算有——装配在宿主的 `main()` 里。

处置：包级改判「不需要」，本节表头计数已跟着改（需要 85→84，不需要 141→142）。

### 5.4 顺带确认的三处真缺口

漂移不改裁决，但它让三块「包要、包里缺一角」的地方浮出来。三处在 `portmap.tsv` 里
都已经有行、有理由、有补的入口——不是新发现，是这一轮把它们从几千行里捞出来点了名，
好让第一步那张能力覆盖表的「缺口说明」列有东西可写。

| 缺口 | DSH 出处 | 我们现在有的 | 补的入口 |
|---|---|---|---|
| 子 agent 的模型选择授权表 | `subagent/tool-subagent` 的 `model-selection*.ts` 共 21 个符号 | `descriptor.go` 上的 `AgentProvider`／`AgentModel` 是装配期定死的：模型自己挑不了，也没有一张「许挑哪几条路由」的授权表 | 三处一起：工具 schema 加 provider／model／reasoning_effort 三个字段；授权表以 `subagent/model-selection-policy` 事件进日志（只进日志、不进模型历史）；配一个投影单元读回来 |
| 单回合精确用量 | `llm/token-meter` 的 `deriveTurnTokenUsage` | `feature/tokenmeter` 是整份日志**累计**，同一 turn/step 的重复采样 last-wins，切不出「这一个回合花了多少、走了哪几条路由」 | `feature/tokenmeter` 加一个吃 `turn/start`..`turn/end` 事件切片的纯函数 |
| 路由感知的图片计价 | `llm/token-meter` 的 `priceSurface` ＋ `llm` 的 `LlmImageRequestPricing` | 图片按固定启发式估价，`llm` 包里根本没有计价这条接缝 | `llm` 加一个图片计价接口，`feature/tokenmeter` 按路由把图片节点重估一遍 |

### 5.5 反向的那一类：我们抄了上游已经删掉的东西

`preset/agent-presets` 是这 10 个里唯一**减了导出面**的：上游删掉 6 个符号，我们其中 5 个
已经抄过来了，而且账本上是 `PORTED`。

| 上游删掉的 | 我们这边 |
|---|---|
| `InvalidPresetIdError` | `agentpresets.InvalidPresetIDError` |
| `PresetExistsError` | `agentpresets.PresetExistsError` |
| `PresetMountError` | `agentpresets.PresetMountError` |
| `PresetNotWritableError` | `agentpresets.PresetNotWritableError` |
| `UnknownPresetError` | `agentpresets.UnknownPresetError` |
| `PresetBearingSession` | 判的 `GO_NATIVE`，本来就没抄 |

`llm/token-meter` 的 `SurfaceTokenFold` 同理，我们抄成了 `tokenmeter.surfaceTokenFold`。

**这不是错误，抄的那一刻上游有。** 账本也已经记着了：这几行的 `kind` 列是
`STALE:class`／`STALE:reexport`，意思是「机器清单里已经没有这个符号，但这一行不删」。
全表这样的行有 **1423 条**，其中 **34 条的裁决是 `PORTED`**——那 34 条才是要逐条看的：
它们说的是「我们抄了一个上游已经没有的东西」。

**看不等于删。** `PresetMountError` 这一类我们自己在用，上游删掉它是因为上游那边的调用点
没了，不是因为这个概念错了。要判的是「我们这边还需不需要它」，答案多半是需要。

### 5.6 那 34 条逐条看完了（2026-09-04）

**结论先说：34 条里只有 3 条真是「上游没了」，其余 31 条是账本坐标过期。**
上一节把这 34 条整体叫「我们抄了上游已经删掉的东西」，看完之后这个说法太粗——
`STALE` 的含义是「按记着的坐标找不到」，而找不到有六种原因，只有最后一种才是真删。

做法：拿这 34 行的符号名，先在它自己那个包的 `src` 里搜，搜不到再搜整个 250 包快照，
再搜不到才判真删。

| 情形 | 行数 | 说明 | 该怎么办 |
|---|---|---|---|
| **A. 符号还在原包，只是换了文件** | 10 | `core/agent` 的 `Agent`、`credentials` 的 `CredentialInfo`、`session-title` 六个、`subagent` 的 `SubagentListEntry` 两行 | 重挂坐标，跟 NOT_FOUND 1805 条同一批活 |
| **B. 符号搬去了别的包** | 2 | `TodoItem` 从 `core/session` 搬到 `todo/tool-todo`；`deepEqualJson` 从 `settings` 搬进新包 `util/values` | 重挂到新包 |
| **C. 改名或换形态，概念没变** | 4 | `CallId`→`ToolCallId`；`OFFLOADED_IMAGE_TEXT` 从常量变函数 `offloadedImageText()`；`offloadRequestImages`→`offloadRequestImagesWithPolicy`；`settingsNamespace` 变成私有的 `parseSettingsNamespace` | 重挂到新名 |
| **D. 上游取消公共导出，下放给各消费方各写一份** | 1 | `isTokenDelta`：`llm/llm` 不再导出，`session/session-stats` 和 `client/ui-trajectory` 各自留了一份私有的 | 保留我们的 `llm.IsTokenDelta`，见下 |
| **E. 上游换了整套形状** | 14 | `agent-presets` 五个错误类加 `resolveSessionPreset`（各 class／reexport 两行，共 12 行）、`PERSONA_ORDER`、`UserQuestionProvider` | 要判，见下 |
| **F. 全快照无继任者，真没了** | 3 | `tokenmeter` 的 `SurfaceTokenFold` 与 `foldSurfaceTokens`；`plan-mode` 的 `foldPlanMode` | 要判，见下 |

**A、B、C 共 16 行不是分歧，是坐标过期**，和 reanchor 那 1805 条是一回事，
下一轮重挂坐标时一起清。这一节不动它们。

#### 剩下 18 行的判断

**D · `llm.IsTokenDelta` 留着。** 上游把它从 `llm/llm` 的公共导出撤下来，改成两个消费方
各自私有一份。那是 TypeScript 单仓库内部的整理——两处复制在 monorepo 里不痛。
我们这边 `llm` 是**契约包**，「一个流式分片算不算 token 增量」是契约语义不是消费方细节，
放契约里只有一份定义。**不跟这次下放。**

**E · `agent-presets` 五个错误类留着，这是 Go 与 TS 的错误模型之差。**
上游现在统一抛 `RemoteError<'agent-preset/read-only'>` 这种**带字符串码的单一类型**。
Go 里对应的做法是 `errors.Is` / `errors.As` 配具名类型，字符串码要靠比字符串——
`internal/devtools` 那几个门禁里没有一条能防住拼错的字符串码，具名类型编译期就挡住了。
**五个类型不合并。**

**E · `resolveSessionPreset` 留着。** 上游把它折进了 `agentPresetProjectionDefinition`，
包外不再看得见这个函数。折进投影是 cordis 投影模型的做法；我们这边「按会话解析出该用哪个
预设」是 `webhook`、`subagent`、会话创建三处共同的入口，**收成私有会让它们各写一遍**。

**E · `PERSONA_ORDER` 与 `UserQuestionProvider` 两条要跟。**
前者上游拆成了 `PERSONA_SECTION` 常量加 `SECTION_ORDERS` / `CONTEXT_ORDERS` 两张序表——
从「一个写死的顺序」变成「一组具名顺序，装配时挑一个」，这是能力变强，该跟。
后者上游从 `Provider` 接口改成了 `UserQuestionService` 服务类，形状变了，**但那是 cordis
服务模型**，我们没有 cordis，接缝仍按 Go 惯例定在使用方。**跟它的能力，不跟它的载体。**

**F · 三条真没了，全部留着。**
`SurfaceTokenFold` / `foldSurfaceTokens` 是把多路 token 计数折成一份面向展示的汇总；
上游删它是因为它的调用点在 `client/` 的展示层，那一支我们整支不要，**但服务端要
按会话汇总用量**（`session-stats` 是预算闸门的数据源，第三节写过），所以这个折叠本身要。
`foldPlanMode` 上游现在只剩测试文件里的一个私有 helper，`src` 不再导出；
我们的 `planmode.FoldMode` 是从事件流判「当前是不是计划模式」，那是运行期要答的问题，留。

**这 18 行的裁决不变，全部维持 `PORTED`。** 变的是理由：从「抄的那一刻上游有」
变成「看过继任者了，我们这边有不跟的理由」。理由写在这一节，不逐行改 `portmap.tsv`——
`kind` 列的 `STALE` 是机器算出来的事实，不该手改。
