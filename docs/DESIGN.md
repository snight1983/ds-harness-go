# ds-harness-go 设计

## 一、这是什么

一个 Go 模块，给**服务端** agent 用。`aiboys-go` 只是第一个消费方，不是唯一那个。

能力来源是 DeepSeek Harness（下称 DSH，TypeScript，227 包）。**移植的是能力，不是字符。**
凡是 Go 有现成办法的，用 Go 的办法；行为一致即可，手段不必一致。

### 通用是硬约束，不是口号

ds-harness-go 装出来是一个**空的 agent 运行时**。给它四样东西——一个模型、一批工具、
一批 skill、一段人格提示词——它就能跑。今天那四样是「即梦接口 + 创意技法」，
明天换成「行情接口 + 财报分析技法」，**ds-harness-go 一行代码都不用改**。

落到可执行的规矩上：

1. **业务词汇零进入**，由架构测试卡死。ds-harness-go 里不许出现 video / product / stock /
   创意 这类词。这条在 `credentials` 上已经是这么做的。
2. **不内置任何业务工具。** ds-harness-go 只有框架自己的元工具（加载 skill、反问用户）。
3. **系统提示词只管组装不管内容。** 把时间、指令、skill 目录、工具清单拼成结构，
   内容全部由消费方给。

反过来也要说清楚：**通用不等于功能越多越通用。** 骨架就是第三节那五块，
它已经能跑任何领域的 agent。`mcp`、`subagent`、`schedule` 这些是加分项，
骨架不稳就往上堆，等于把 227 个包的坑重新踩一遍。

## 二、这不是什么

DSH 是**桌面编程助手**：给一台机器上的一个人用，主业是改代码、跑测试、执行 shell。
它一半的包在操作本机文件系统，默认出厂包里装的是 `sandbox-local`（在本机把进程关小黑屋）。

ds-harness-go 不是那个东西：

- **服务化，多用户。** 没有「当前用户的 home 目录」「当前工作目录」这种东西。
- **agent 不执行任意代码，也不碰服务器的目录和命令。** 工具是我们自己写的函数
  （查商品、生成分镜、调视频接口），在服务进程里直接跑。
- **因此不需要沙箱。** 沙箱是为了跑不可信代码存在的。没有那件事，就没有那一层。
  这也是 LangGraph 里根本没有沙箱层的原因——工具就是普通函数。

一句话：DSH 的**接缝**（接口）服务端照用，DSH 的 `-local` / `-sandbox` / `-e2b`
那一整列**实现**全部不要，我们自己挂数据库实现。

## 三、范围：九块

范围由消费方定，不由 DSH 的包列表定。上一版这里写的是「四块」，是在读 README 之前
按印象划的；补上五条前提（多用户并发、恢复历史对话、活干到一半走人、第二天接着干、
多 agent 协作）之后，实际是九块：

| 块 | 数 | 要解决的问题 |
|---|---|---|
| **持久化** | 15 | 会话、凭据、附件、资源文件存哪；崩了怎么恢复；过大的工具结果外置到哪 |
| **上下文管理** | 6 | 对话变长之后喂给模型的东西怎么组织、怎么压缩 |
| **tool** | 11 | 工具怎么声明、怎么校验参数、怎么要审批、结果怎么回给模型 |
| **skill 与业务包** | 4 | 按需加载的提示词：平时只给模型看目录，用到了才把全文塞进上下文 |
| **loop** | 14 | 拿着上面几样把一轮对话跑完 |
| **多 agent 与后台活** | 17 | 前提 5 要的委派与协作；前提 3、4 要的活能跨进程活下来 |
| **底座** | 7 | 启动、设置、工作区、不变量、超时 |
| **对外协议** | 3 | 消费方唯一的入口形状 |
| **测试脚手架** | 5 | 不用真模型跑完整轮次；崩溃路径要能在精确位置断开 |

后四块是上一版整个漏掉的——它们不在「跑一轮对话」这条线上，而在「跑一个**服务**」这条线上。

对应 DSH 的包，**82 个**（不是 227，也不是上一版猜的 46）。这 82 个逐包的理由在
`docs/portmap/rulings.md`，依据是 `functions.md` 那 2009 条功能和 `required.md` 那五条前提。
下面只列结论。

### 持久化（15）
```
storage/storage  storage/storage-domain  session/session-persistence
session/session-checkpoint-policy  session/session-projection
session/session-projection-cache  session/session-stats
session/session-telemetry  session-query/session-query
session-query/tool-session-query  credentials/credentials
attachment/attachment  spill/spill  spill/spill-policy
fs/fs
```
`session/session-stats`（每会话的 turn / step / token 计数投影）和
`session/session-telemetry`（emit / flush / shutdown 的外发接缝）是消费方裁定要的。
前者是预算闸门的数据源——`goal/*` 自陈不管预算，而多用户服务里钱是平台的；
后者零本机前置，DSH 那个往本机日志导出的实现不要，后端换成 **OTLP over HTTP**，
和 `mcp` 只取 `streamable-http` 是同一条界线。
`fs/fs` 是**接缝**，不是本机磁盘：`readText(target)` / `writeText(target, content)` /
`listDir(target)` 不规定 target 在哪，后端挂对象存储（第七节）。碰机器的是
`fs-local` / `fs-sandbox` / `tool-fs` 那一列，不要。
不要 `storage-json` `session-persistence-jsonl` `attachment-local` `credentials-local`
`spill-local`——都是往本机写文件的实现，服务端换成数据库。

`storage-sqlite` 和 `session-persistence-sqlite` 上一版列在范围里，现在**不列**：
后端是 Postgres（第七节），这两个包本身不移。要抄的是它们的**结构**——键值怎么映射成表、
迁移怎么走——那是抄形状，不是移包。

`spill`（过大工具结果外置）和 `session-query`（会话检索）是上一版整个漏掉的两块。
工具结果撑爆上下文这件事和前提 2、4 直接相关。

### 上下文管理（6）
```
context/agent-instructions  context/session-reference  context/time-context
compaction/compaction  compaction/compaction-basic
compaction/compaction-tool-result-pruner
```
`context/time-context` 往系统提示词里注入当前时间——agent 不知道今天几号是真缺陷。
**DSH 采的是浏览器时区，我们改成由消费方传时区**：服务端没有「当前用户的浏览器」。
不要 `file-reference-local` `tmux-context`——读本地文件和终端的。
`compaction-basic`（整段总结）和 `compaction-tool-result-pruner`（压缩前先裁掉旧的工具结果）
是消费方裁定要的：接缝没有实现方就是空的，而这两个都不依赖任何本机资源。
剪枝器和 `spill` 是同一件事的两个时机——`spill` 在写入时把过大的结果外置，
剪枝在压缩时把旧的丢掉；不裁就等于把上下文预算全花在历史工具输出上。

### tool（11）
```
core/tools  guard/timeout-policy  guard/repeat-tool-reminder
interaction/user-approval  interaction/tool-ask-user
interaction/permission-presets  interaction/commands
interaction/user-questions  todo/tool-todo
plan/plan-mode  mcp/mcp-client
```
`guard/repeat-tool-reminder`（同一个工具被反复调用时提醒模型）零前置，而模型卡在
死循环里，每多转一轮就是一次付费的模型调用。
`mcp/mcp-client` 把外部 MCP 服务器的工具桥接进 `ctx.tools`，模型看到的是
`mcp__<server>__<tool>`。它不提供任何能力——能力在对面那台服务器上；它只是一个插口，
让消费方不用为每个第三方服务各写一遍 Go 工具。

**只移 `streamable-http` 传输。** `transport.ts` 两个分支：`stdio` 要在本机
`spawn` 子进程，不移；`streamable-http` 就是 `new URL(url)` 加几个 header，
零本机前置。这和 `fs → 对象存储` 是同一条界线（第七节）：服务进程自己去某处取东西可以，
在本机起进程不行。

多用户下要补一件 DSH 不用管的：**服务器地址和 `headers` 里的凭据是每个用户各自的**，
DSH 写死在配置文件里，我们要挂到 `credentials` 的归属校验上。
`typert/` 整支三个包（`protocol` `loader` `generator`）**全部不要**：它们是 TypeScript
编译期元数据机制（`@Remote` 装饰器、构建期扫源码），Go 有 struct tag、方法集和 `reflect`。
`typert/registry`（运行时反射信息 + 可选 Zod schema 的注册表）归 **Go 已有等价物**：
`reflect` + struct tag 白送，不移包。整支 `typert/` 一个包都不进来。

`core/agent-tool-presentation` 判**不要**：它的职责是在 `native` / `code` / `both`
之间选一个，而 `code` 那两档要 `ctx.codeRuntime`（`code-runtime` 已判不要），
只剩 `native` 一个可选值——而 `native` 本来就是 `core/tools` 的默认。一行插件选唯一值，等于不装。

### skill 与业务包（4）
```
skill/skill  skill/tool-skill  preset/agent-presets  preset/persona
```
不要 `skill/skill-filesystem`（从本机文件夹读）、`skill/skill-badge`（前端角标）。

**skill 不是一种文件格式，是一个 provider 接缝。** `SkillRegistry` 自己不知道 skill 从哪来，
它只认三个方法：

```
SkillProvider {
  name
  list(options) -> []SkillCandidate      // 给目录，不含正文
  get(candidate, options) -> SkillDefinition  // 要正文时才调
}
```

文件夹是一个 provider，数据库是另一个，远程注册表是第三个，它们可以同时挂着。
所以「库还是文件」这个问题在 DSH 那里根本不是二选一——**我们只是不装文件那个 provider**，
接缝一个字都不用改。

字段是 DSH 定死的，不是我们发明的（源: `packages/skill/skill/src/index.ts:55-95`）：

| 字段 | 在哪一层 | 干什么 |
|---|---|---|
| `name` | 目录 | kebab-case，`^[a-z0-9]+(?:-[a-z0-9]+)*$`，模型按它加载 |
| `description` | 目录 | **必填非空**。模型只看这一句决定要不要加载 |
| `whenToUse` | 目录 | 可选，补一句什么时候该用 |
| `invocation` | 目录 | `{modelInvocable, userInvocable}`，谁能调起它 |
| `source` `provider` | 目录 | 从哪来的，用于排障和展示 |
| `resourceBase` | 目录 | 可选，配套资源在哪 |
| `rank` | 候选 | **同一层内**重名时谁赢，小的赢 |
| `locator` | 候选 | provider 自己的不透明句柄，别人不许解释 |
| `content` | 正文 | markdown 全文，`get` 之后才进上下文 |

`description` 是最要命的字段：模型看不到 `content`，只靠这一句路由。
这句写砸了，那篇 skill 就等于不存在——写得再好也永远不会被调用。

`resourceBase` 是三选一：`{kind:'directory', path}` / `{kind:'url', url}` /
`{kind:'opaque', description}`。**服务端永远不产出 directory 那个变体**——
全仓库产出它的只有 `skill-filesystem` 和 `skill-badge`，两个都不移。我们产出的是
`url`（指向对象存储）或 `opaque`（一句描述）。

顺带把这个变体的性质说死，免得以后有人以为它是个口子：`resourceBase` 全仓库
**只被 `renderSkillContent` 读一处**，产物是提示词里的一行说明文本。
没有任何代码拿它去打开文件。它是给模型看的一句话，不是一个句柄。

**真正的挂载单位不是单条 skill，是业务包：**

```
业务包 = 一组工具 + 一组 skill + 一段人格/指令提示词
```

skill 和 tool 必须一起切。做 A 业务的用户不能在工具清单里看见 B 的工具——
模型看见了就会去调。

跑起来是：消费方启动时定义若干业务包 → 用户/工作区记着挂了哪几个 →
开会话时按这个用户挂载的包**组装**出他这次的工具清单、skill 目录、系统提示词。
同一个进程里 A 用户和 B 用户跑的是两个完全不同的 agent，共用一套 ds-harness-go。

DSH 有同一个概念（`preset/agent-presets`：按会话组装 agent），但它的实现绑在
cordis 的 `cordis.yml` 上——描述的是「装哪些插件」。我们不要那个容器，
所以这块**重做**：描述的是「挂哪些工具集、哪些 skill 集、哪段人格」。

**分层这件事 DSH 已经有了，缺的只是归属。** `SkillRegistry` 内部用的是
`ScopedLayers`——一个全局层加一条作用域链，解析时全局在前、链上从最远的祖先往下叠、
最贴近的作用域最后叠，于是**近的那层同名条目直接盖掉远的**；`rank` 只在同一层内
决重名。工具注册表用的是同一条规则。而 `ScopedLayers` 就在 `core/scope` 里，
已经移过来了。

也就是说「A 业务的用户看不见 B 的 skill」不需要新发明机制：把业务包挂成一层就行。
真正要新增的是 DSH 不需要的那一半——它单机单人，不存在「这是谁的」：

- 三层可见性：平台内置 / 租户 / 用户私有，各占一层。
- 加载时校验归属：用户 A 调 `load_skill(B 的技能名)` 必须被拒。
  跟 `credentials` 的归属校验同一个道理——每个操作都带「谁」，不靠调用方自觉。
  只靠分层不够：分层决定「看得见什么」，归属决定「够得着什么」，
  一个猜到名字的调用绕得过前者，绕不过后者。
- skill 目录是**按用户组装**进系统提示词的，不是常量。

最后一条有个必须提前定死的副作用：**每个用户的 skill 目录不同，模型的前缀缓存就分裂了。**
缓解办法是把「所有人都一样」的部分（内置目录、通用指令）排在系统提示词前面，
用户私有的排后面，前缀能共享多少算多少。这个顺序一开始定好，
**后面再改就是全员缓存失效**。

### loop（14）
```
core/agent  core/agent-loop  core/session  core/scope  core/system-prompt
core/agent-default-model  session/session-title  session/session-title-llm
session/session-title-first-prompt-llm  session/session-title-all-prompts-llm
llm/llm  llm/llm-retry  llm/llm-pi-ai  llm/token-meter
```
`session/session-title` 在 DSH 主干挂载清单里，而接缝没有实现方就是空的，
所以三个提供方一起进来：`session-title-llm` 是共享策略与路由，
`-first-prompt-llm` 按首条消息生成（默认走这条，最省），
`-all-prompts-llm` 总结全部消息（更好也更贵）。三个都只调 `ctx.llm`，零本机前置。
不要 `llm-deepseek`——走本地模型（`conn.aaii.chat`）。**要 `llm/llm-pi-ai`**：
它是 OpenAI 兼容协议的通用多提供方适配器，本地模型正好走这条。上一版整个漏了。
`core/agent-default-model` 是消费方裁定要的：它记住默认用哪个模型
（`currentSelection()` / `saveSelection()`），不装的话每开一个会话都得由调用方点名模型。
**它自陈只有一项进程级默认值**，「每个会话的选择仍由入口负责」——多用户下
「这个用户偏好哪个模型」是每人各自的，要在它上面按用户叠一层，做法同 `credentials` 的归属。

### 多 agent 与后台活（17）
```
subagent/subagent  subagent/subagent-in-process-driver
subagent/subagent-spawn-in-process  subagent/subagent-fork-in-process
subagent/tool-subagent  subagent/tool-subagent-control
subagent/tool-subagent-report  workflow/workflow  workflow/tool-ralph
jobs/jobs  jobs/jobs-local  jobs/tool-jobs  schedule/schedule
goal/goal  goal/goal-round-driver  goal/tool-goal  goal/command-goal
```
上一版把 `subagent`(11) `workflow`(4) `jobs`(3) `schedule`(1) 全挂在「未定」。
前提 5（多 agent 协作）+ 前提 3、4（活干到一半跨天）把它们定死了，见 `required.md` 第二层。

四个进程外 provider（`-acp` `-claude-code` `-codex` `-dsh-sdk`）都不要：
它们各自自陈「每次运行使用全新进程」「仅支持本地子进程」。

**这一支同时是缺口最集中的地方**：`functions.md` 里多 agent 域是 50 有 / 50 自陈无，
全清单唯一对半开的域，缺的那半正好是前提 3、4 要的（跨进程驻留、持久上报 mailbox）。
`jobs/jobs` 更麻烦——「约定是进程内的」是**接缝**自陈，换实现也补不上，而 jobs 在主干里。

`goal/*` 四个是消费方定的，DSH 作者自己把它们标成可选。给的能力是
**一次交代、agent 自己一轮接一轮跑到完成／阻塞／轮数上限**：`goal` 存目标状态，
`goal-round-driver` 在 agent 空闲时自动排下一轮，`tool-goal` 让模型声明完成或阻塞，
`command-goal` 让人 `/goal pause`。

这一支带两个必须写在这里的事实：

1. **它只数轮数，不计 token、不计钱、不计时间、不计提供方配额**（四份 README 的
   「已知限制」各自写着）。DSH 是桌面单机——钱是用户自己的、跑飞了 Ctrl-C 就停；
   服务端三条全反过来。**预算闸门不在这四个包里，要在消费它的那一层加。**
2. **续行权限从不持久化。** 会话恢复或 fork 之后目标还在、phase 还是 active，
   但不会自动重启工作，必须显式 `resume`。这是作者有意的设计，不是缺陷——
   但它意味着 `goal` **没有**替前提 3、4 解决跨天续跑，别把它当成那个答案。

### 底座（4）
```
runtime-diagnostics/invariants  util/timeout  util/output-retention
settings/settings
```
`util/brand` 和 `util/atomic-write` 单列一档 **Go 已有等价物**：行为要，手段不要。
type alias + 未导出字段、`os.WriteFile`/`os.Rename` 是白送的，不移包。

**这一块已经做完了。** 原先列在这里的另外三个包，逐个读完源码之后都不在这里了：

- `workspace/workspace` 的实现要 `storageDomain` 和 `sessionPersistence`，
  两者都在持久化那一块。**它不是底座，是持久化的消费方**，挪到第 2 块之后。
- `hooks/hook-protocol` **整包出局**。它是 Claude Code / Codex 两套 `hooks.json`
  配置格式的共享内核，核心动作是在本机跑用户写的 shell 命令、按退出码拦住 agent。
  `shell/*` 全族已因「服务端不提供沙箱」出局，`runHook` 的执行器随之没了；
  两个消费方 `hooks-claude-code` / `hooks-codex` 也已出局。消费方裁定不要这个能力。
- `boot/app-boot` **整包退化成 Go 自带**。它的全部非标准库依赖是 cordis 本体加
  loader / include / group / hmr 四个插件，干的事是读 `cordis.yml` 插件树、读
  `~/.dsh/profiles` 下的 npm bundle、靠 Node 模块解析找到插件包、挂进 Loader、
  监听文件热重载。cordis 整个框架不移植（本节前面已定），它一没，这个包就只剩
  「把 YAML 里写的插件名变成运行期实例」这一件事——Go 里这件事是 `main()` 里
  写构造函数，编译期就查得出来。

### 对外协议（3）
```
acp/acp  sdk/protocol  sdk/server
```
上一版把 `sdk`/`api`(5) `acp`(2) 挂在「未定」或「整包不要」。协议方法表和通知表是
消费方唯一的入口形状，要移；承载从 stdio 换成 HTTP，见 `rulings.md` 第 2.3 节。

### 测试脚手架（5）
```
test-support/llm-replay  test-support/llm-mock-server
test-support/agent-loop-testkit  test-support/acp-snapshot
test-support/loader-smoke
```
上一版把 `test-support` 整支当成「它自己的脚手架」排除了，这是错的。
**没有 `llm-replay`，前提 3、4 那些「进程中途死掉」的路径根本没法写测试**——
崩溃恢复的用例要求在精确的位置断开，真模型给不了这个精确度。

### 不移包，但要抄形状（15）
```
bundle/base  bundle/headless  bundle/web-app
examples/agent-spine-demo  examples/acp-demo  examples/jsonrpc-demo
host/apiproxy  api/gateway  api/remotes  sdk/client
experimental/agent-team  experimental/tool-agent-team
storage/storage-sqlite  session-query/session-query-sqlite
credentials/authorization
```
后三个是这一轮新加的。`storage-sqlite` 和 `session-query-sqlite`：后端已定 Postgres
（第七节），包不移，抄的是**键值怎么映射成表、迁移怎么走、查询表结构与索引**。
`credentials/authorization`（OAuth 与人工授权流程）形状要、实现要重写——
它自陈「flow 不可恢复」是浏览器进程的限制，和前提 3、4 直接相撞。
前十个绑在 cordis / 浏览器 / 子进程驱动上，不移；但里面记着装配顺序、
对外 API 方法清单、协议形状。抄什么、怎么抄，见 `rulings.md` 第二节。

`experimental/agent-team` 是消费方裁定要的能力：**多个 agent 之间的持久信箱 +
共享任务板**。三件形状——Roster 状态机、Quiet（攒着不打断）／Wakeup（立刻唤醒）两档投递、
`expectedRevision` 对不上就返回 `TEAM_TASK_STALE_REVISION` 的 CAS 任务板（任务之间是 DAG）。

**包不移，因为它自陈「单进程、共享 checkout」「mailbox 不保证跨进程 exactly-once」**，
和前提 1、4 直接相撞（`required.md` 第三节把它列成缺口）。照搬进来等于把单进程假设焊死。
但这三样的形状与进程模型无关：换成 Postgres 存储照样成立，CAS 本来就是为并发写设计的。

## 四、不要的（127 个）

227 = 需要 82 + 抄形状 15 + Go 已有等价物 3 + **不要 127** + 说不清 **0**。
**说不清已经清零**，227 行每一行都有终判。

### 不要的 127 个

「不要」的判据只有一条：**它有一个我们给不了的前置**。不是「用不上」，是「装不上」。

| 域 | 数 | 给不了的前置 |
|---|---|---|
| `client/` | 40 | 浏览器 DOM / React。有自己的前端（`C:\code\aiboy`） |
| `shell/` | 10 | 本机命令执行（`bash-local` `pwsh-sandbox` `tool-bash` …）；接缝 `shell` `shell-env` 无沙箱可挂 |
| `host/` | 7 | 桌面对话框 / 本机磁盘 / 内建 HTTP，第六节已删对应目录；`plugin-inventory` 是 cordis 插件清单，Go 里没有这个装载器 |
| `fs/` | 6 | 本机磁盘读写；`fs-observation-policy` 也不要——后端是对象存储，模型不逐个读文件，没有观察对象 |
| `web/` | 6 | 三个搜索提供方都要第三方 API key，现在没有数据源；抓取单独存在没用。**推后，不是永久出局**——接缝零依赖，补回来不动已有代码 |
| `subagent/` | 4 | 本机子进程（四个进程外 provider） |
| `sandbox/` | 4 | 本机沙箱；接缝 `sandbox` `sandbox-policy` 没有实现方 |
| `extensions/` | 4 | cordis 动态插件：浏览器页面 + vm 沙箱执行，两个前置都给不了 |
| `typert/` | 3 | TypeScript 编译期元数据与构建期源码分析（`registry` 另归 Go 已有等价物） |
| `util/` | 3 | 本机路径 / 启动环境 / 原生命令 |
| `session/` | 3 | 本机文件或 SQLite 落盘、OTel 外发 |
| `context/` | 3 | 本机文件引用、tmux 终端 |
| `terminal/` `code-runtime/` `lsp/` `e2b/` | 各 3 | 终端 / Python 与 worker thread / LSP stdio / E2B 远程沙箱；四支的接缝同样无沙箱可挂 |
| `skill/` `feedback/` `subprocess/` `hooks/` `workflow/` | 各 2 | 本机文件夹 / 前端角标 / 起进程 / Claude Code 与 Codex 两个钩子方言桥 / JavaScript 编排脚本与 Node worker thread 引擎 |
| `compaction/` `spill/` `llm/` `credentials/` `attachment/` `session-query/` `storage/` `boot/` `settings/` `identity/` `core/` `test-support/` | 各 1 | `/compact` 手动入口（推后） / 本机文件 / DeepSeek 官方 API / 本机凭据文件 / 本机附件目录 / 浏览器 ZIP 下载 / 本机 JSON 文件 / 桌面启动器 argv / 本机设置文件 / 桌面匿名标识 / `agent-tool-presentation` 只剩默认值 / `client-runtime` 是前端脚手架 |

**上一版这张表有三处是错的，这里更正：**

1. `fs` `shell` `sandbox` `terminal` `e2b` `code-runtime` `subprocess` `lsp` 那 35 个
   上一版是**整支不装**。错了。这八支各自有一个**纯接缝**包（`fs/fs` `shell/shell`
   `sandbox/sandbox` `terminal/terminal` `subprocess/subprocess` `code-runtime/code-runtime`
   `lsp/lsp` `e2b/e2b`，加 `shell/shell-env` `sandbox/sandbox-policy`
   `fs/fs-observation-policy`），是纯服务定义，**零本机前置**。碰机器的是
   `-local` / `-sandbox` / `-stdio` / `-windows-acl` / `-e2b` 那一列实现和 `tool-*`。
   实现那 24 个归 不要。接缝 11 个后来也定完了：`fs/fs` **要**，其余 10 个全部 不要。
   35 = 11 + 24。
2. `test-support` 整支被当成「它自己的脚手架」排除——错了，见第三节。
3. `spill` `acp` 被当成「大输出写本地文件 / 编辑器集成协议」整支排除——
   `spill/spill` 是接缝、`acp/acp` 是协议，都要；只有 `spill-local` 是本机实现。

### 说不清清零

这一档曾经有 20 个，含义是「这五条前提没要求它，要不要由消费方定」——不是「不要」。
**现在一个都不剩，227 行每一行都有终判。**最后那批的走向分三类：

| 走向 | 包 | 判据 |
|---|---|---|
| **需要 7** | `context/time-context` `guard/repeat-tool-reminder` `session/session-stats` `session/session-telemetry` `session-title-llm` `-first-prompt-llm` `-all-prompts-llm` | 零本机前置，且各自补上一个真缺陷：不知道今天几号 / 死循环白烧钱 / 没有预算数据源 / 线上出事没法查 / 接缝没实现方 |
| **抄形状 3** | `storage/storage-sqlite` `session-query/session-query-sqlite` `credentials/authorization` | 结构值得抄，实现要按 Postgres 与可恢复流程重写 |
| **Go 已有等价物 1** | `typert/registry` | `reflect` + struct tag 白送 |
| **不要 9** | `core/agent-tool-presentation` `fs/fs-observation-policy` `extensions/*`(3) `hooks/hooks-claude-code` `hooks/hooks-codex` `host/plugin-inventory` `test-support/client-runtime` | 各自的前置：唯一可选值 / 没有观察对象 / 浏览器加 vm 沙箱 / 别家产品的钩子点 / cordis 装载器 / 前端脚手架 |

**执行那一整支也在这里定完了。** `shell/shell` `shell/shell-env` `sandbox/sandbox`
`sandbox/sandbox-policy` `terminal/terminal` `subprocess/subprocess`
`code-runtime/code-runtime` `lsp/lsp` `e2b/e2b` 九个纯接缝**判不要**：
它们零本机前置不假，但语义是「执行命令／代码／终端」，而那件事的前置是**沙箱**——
服务端不提供沙箱，所以这九条接缝一个实现方都挂不上。接缝没有实现方，就是空的。
`e2b` 本身就是远程沙箱，同理出局。那 24 个实现包也不用写远程版的替代了。

## 五、目录结构

**旧的做法是错的**：根目录平铺，包名每次现起，域名丢掉或粘进包名
（`host/directory-picker-browse` → `dirbrowse`，`typert/protocol` → `typertprotocol`）。
后果是根目录看不出谁跟谁是一伙的，而且同一件事有两套规则（`storage/storagejson` 嵌套了，
`dirbrowse` 没有）。

**新规则，两条，没有例外：**

1. **镜像 DSH 的域分组**，`ds-harness-go/<域>/<包>`。
2. **接缝包占域名本身**，同域的其它包挂在它下面——和标准库 `database/sql` +
   `database/sql/driver` 一个写法。

```
storage/                    ← 接缝：只是一个接口，零前置
  domain/  memory/  postgres/   ← 实现挂在它下面
fs/                         ← 接缝
  objectstore/                  ← 实现（S3 / MinIO）
```

**完整的目录树在 `README.md`**，那棵树是这 82 个包的落点，逐个标了已建／待建。
这里只定规则，不复制一份会走样的副本。

Go 的包名保持短名（`package credentials`、`package postgres`），只有导入路径带域。
迁移时**不动 `package` 声明**，所以 `docs/portmap/portmap.tsv` 里 7909 行的
`go_ref` 列一个字都不用改。

## 六、现有 Go 代码的处置

已经移完 28 个目录，按新范围逐个裁：

**留下并搬位置**

| 现在 | 搬到 | 属于 |
|---|---|---|
| `invariants/` | `invariants/` | 底座 |
| `storage/` | `storage/` | 持久化 |
| `storage/storagetest/` | `storage/storagetest/` | 接缝一致性测试套件 |
| `credentials/` | `credentials/` | 持久化 |
| `attachment/` | `attachment/` | 持久化 |
| `scope/` | `core/scope/` | loop |
| `typertprotocol/` | `typert/` | tool |
| `timeout/` | `util/timeout/` | 底座 |
| `outputretention/` | `util/outputretention/` | 底座（工具输出截断） |
| `llmmockserver/` | `llm/mockserver/` | 测 llm 用的脚手架 |

**删掉**

| 目录 | DSH 来源 | 理由 |
|---|---|---|
| `anonymousid/` | identity/anonymous-user-id | 桌面单机的匿名标识，服务端有自己的用户体系 |
| `atomicwrite/` | util/atomic-write | 只服务本机文件写入 |
| `cmdline/` | boot/cmdline | 桌面版启动参数 |
| `coderuntime/` `coderuntimepython/` | code-runtime | 执行代码 |
| `dirbrowse/` `dirpicker/` | host/directory-picker-* | 让对话挑服务器目录 |
| `frontendstatic/` | host/frontend-static | 桌面版静态文件服务 |
| `webserver/` | host/webserver | 内建 HTTP 宿主，`aiboys-go` 有自己的 |
| `homepaths/` `launchenv/` `nativecmd/` | util/* | 本机 home、启动环境、执行本机命令 |
| `sandboxwinacl/` | sandbox/sandbox-windows-acl | 沙箱 |
| `subprocess/` `e2b/` | subprocess / e2b | 起进程、远程沙箱 |
| `storage/storagejson/` | storage/storage-json | 本地文件实现，服务端用 Postgres |

删掉的东西在 `docs/portmap/portmap.tsv` 里的裁决行同步改成 `OUT_OF_SCOPE`，
理由统一写「服务化范围外」。**不删裁决行**——留着才知道当初为什么没做。

## 七、数据放哪儿

### 服务端没有本地目录，一个都没有

这不是靠沙箱限制出来的，是那一层压根不装：`fs-local` / `bash-local` / `pwsh-local` /
`sandbox-local` / `terminal-bash` / `subprocess-local` / `code-runtime-python` /
`lsp-stdio` 那**一列实现**，加上 `tool-fs` / `tool-bash` / `tool-terminal` / `tool-lsp`
那**一列工具**，共 24 个包划在范围外（第四节）。
agent 的工具清单里没有任何能碰目录或命令的东西，所以「一条删除命令把服务器清了」
这件事没有发生路径——不是被拦住，是没有那个工具。

**接缝不在此列。** `fs/fs` 只是 `readText(target)` / `writeText(target, content)` /
`listDir(target)` 这么一组方法，不规定 target 在哪。它要，后端挂对象存储——
理由就是本节 444 行那条界线：危险的是「模型给个路径就能读写任意目录」，
不是「服务进程自己去某处取一份资源」。

`skill` 的 `resourceBase` 里那个 `{kind:'directory'}` 变体也不是口子：产出它的
`skill-filesystem` 和 `skill-badge` 都不移，而它全仓库只被 `renderSkillContent`
读一处，渲染成提示词里的一行文本。见第三节。

### 按数据形态分，不是一个答案

| 数据 | 形态 | 放哪儿 | 为什么 |
|---|---|---|---|
| 会话事件日志 | 结构化，按会话顺序读 | **数据库** | 要按 `(session, sequence)` 顺序取和追加，对象存储做不了这件事 |
| skill 内容 | `SKILL.md` 加它带的资源文件 | **对象存储**，库里只留一张索引 | 见下一小节 |
| 凭据 | 短字符串 | **数据库 + 环境变量** | 归属校验在库里做，见 `credentials` |
| 图片附件 | 几十 KB ~ 几 MB 二进制 | **对象存储（S3 / MinIO）** | 库里只留 key + sha256 + size；内容按需取 |
| 生成的视频 | 几十 ~ 几百 MB | **对象存储，且不走 `attachment` 接缝** | 那条接缝的方法签名是 `[]byte` 进出，几百 MB 走它等于把整个文件读进内存 |

一条界线：**「要查、要排序、要事务」的进数据库；「只按 key 整块取」的进对象存储。**

### skill 横跨这条界线，所以拆成两半

skill 不是一条记录，是一个**目录**：一个 `SKILL.md` 加上它可能带的资源文件。
这个格式不是我们定的——DSH 的 `skill-filesystem` 读的就是它，
而它读的又是 Agent Skills 那套（`SKILL.md` + YAML 头，`name` 和 `description` 必填）。
照着存，别人写好的 skill 原样丢上来就能用。

所以：**内容整个进对象存储，库里只放一张索引表。**

| 存哪 | 放什么 |
|---|---|
| 对象存储 | `SKILL.md` 原文 + 附带的资源文件，按目录原样存 |
| 数据库（一行几百字节） | `name`、`description`、归属（内置 / 租户 / 用户）、挂载关系、对象存储的前缀 |

`description` 必须进库而不能只待在 `SKILL.md` 里：每开一个会话都要按「这个用户挂了哪些」
查一遍，把那几句话拼成目录塞进系统提示词。对象存储只能按 key 取整个对象，不能查——
放那边的话每次开会话都得先把全部 skill 拉下来读一遍。

**DSH 的接缝本来就是照这条线切的**，我们不用改：`list()` 只给候选（含 `description`，
不含正文），`get()` 才取正文。正文晚一步再取，正是为了让它能待在远处。
`resourceBase` 用 `{kind:'url'}` 指向对象存储前缀，也是现成的。

一件要说清的事：搬过来的第三方 skill 里，**带 `scripts/` 的那部分是死的**——
我们没有执行脚本的能力（第二节）。`allowed-tools` 里点名 `Read`/`Write`/`Bash`
这类工具的 skill 同理，那些工具我们一个都没有。能用的是纯方法论型的 skill。

### 不做「先本地目录，以后换 S3」

这条一度考虑过，放弃了：两个实现比一个实现复杂，而省下的只是一次 MinIO 部署。
skill 本来就是一堆文件，对象存储正是为这件事存在的——直接用它最省事。
自建阶段跑 MinIO，接口和 S3 一样，真上云只换个地址。

**这跟第二节「服务端没有本地目录」也不是同一件事**，别混。危险的是
「agent 有个工具，能按模型给的路径读写任意目录」；而「服务进程自己去某处取一份资源」
是任何程序都在做的事。我们不装前者，后者走对象存储。

### 生产数据库只有 Postgres

`storage` 这条接缝是**键值**，不是文件系统——表名 + 键 + 一段不透明 JSON，
而且它明写「记录键永远不会出现在文件路径里，这是后端的义务」
（`storage/backend.go:186`）。后端换成什么，上面的代码都看不见。

生产后端只有一个：**Postgres**。不做「先 SQLite，撑不住再换」那一档。

判据不是吞吐，是**单机还是多机**。SQLite 是一个文件，靠操作系统的文件锁协调，
所以两台机器共享不了（别信 NFS）。不停机发布、备机、扩一台——这三件里
只要有一件要做，SQLite 当场出局，而那一天要付的不是改个参数，
是换后端 + 一次数据迁移 + 一次停机。既然迟早要走，就不先修一条注定要拆的路。

SQLite 的并发天花板顺带说清，免得以后有人以为那是个性能调优问题：
**它的写锁粒度是整个数据库文件，不是行、也不是表。** A 往自己的会话追一条事件、
B 往自己的追一条，这两条毫不相干的写照样一前一后——不是因为撞上了，
是因为锁就这么粗。原因是 SQLite 嵌在进程里、没有仲裁进程，协调只能靠文件锁，
而文件锁的单位就是文件。Postgres 有那个进程，所以写不同的行是真并行，
而按主体分开的数据本来就不会撞同一行。

代价说清楚，它是真的：本地开发和 CI 都要有一个 Postgres 起着，
连接串和密码要管，备份从「拷一个文件」变成 `pg_dump`。这笔成本是天天付的，
换来的是后面不用再做一次带停机的迁移。

**另装一个内存后端**（`storage/memory`）给上层包的测试用：不碰任何数据库，
起停零成本，所以「必须有个库才能跑测试」的范围被压到只剩 SQL 那一层。

它不是「第二个实现」的浪费，它是 `storage/storagetest` 那套一致性套件的
**第二个受测对象**。套件已经在仓库里了，但只有一个后端可跑的时候，
它的价值一分都没兑现——两个后端过同一套用例，「换后端不用改上层」
才从一句主张变成每次 `go test` 都在验的事实。

**后来补了一个 SQLite 方言**（`datastore.SQLite`），上面那条判据一个字没变：
生产仍然只有 Postgres，理由还是单机还是多机。补它是为了另一件事——`datastore`
底下那几千行**每一行都要一个真的数据库才执行得到**，而只认 Postgres 的时候，
它们在开发机上永远跳过、在 CI 上只由一个环境变量决定跑不跑，于是「跑绿了」和
「一行没跑」长得一模一样。缺省落在一个临时目录里的库文件上之后，这批用例进了
`go test ./...`；设 `DSH_POSTGRES_DSN` 就把同一批用例体换到 Postgres 上再跑一遍。

所以它和内存后端是同一个理由的两次应用：两种方言过同一套用例，`Dialect` 那道缝
才从一句主张变成每次 `go test` 都在验的事实。上面那段 SQLite 的锁粒度依然成立，
而它对一个一次只跑一条用例的测试进程不构成问题。

### 实现那一列

DSH 的实现换成服务端的，接缝一行不动：

| 接缝 | DSH 的实现 | 我们的实现 |
|---|---|---|
| `storage` | 本机 JSON 文件 / SQLite 文件 | Postgres（生产）+ 内存后端（测试） |
| `session-persistence` | JSONL 文件 / SQLite | 同上 |
| `credentials` | 本机文件 + 环境变量 | 数据库 + 环境变量 |
| `attachment` | 本机文件 | 对象存储，库里存元数据 |
| `skill` 的 provider | 从本机文件夹扫（`skill-filesystem`） | 自己写一个：目录查库、正文取对象存储 |
| `llm` | DeepSeek 官方接口 | 本地模型 `conn.aaii.chat` |
| `fs` | 本机磁盘（`fs-local`） | 对象存储；`tool-fs` 那个工具不装 |
| `shell` / `sandbox` | bwrap / Seatbelt / ACL / e2b | **没有** |

## 八、移植顺序

不再按依赖层序（Kahn）从最底下无差别推平——那个顺序不区分「服务端装不装得上」，
会花几周移一堆永远不会被 new 出来的包。

按第三节的九块从下往上，块内按依赖：

```
1. 底座        invariants  util/timeout  util/outputretention
               settings                   ← 已做完
2. 持久化      storage → storage/memory → storage/domain → datastore → datastore/kvstore
               credentials  attachment  fs → fs/objectstore
               session/persistence
               session/projection  session/projectioncache
               session/stats  session/telemetry
               sessionquery
               spill
               workspace                  ← 要 storage/domain + session/persistence
3. 上下文管理  context/agentinstructions  context/sessionreference
               context/timecontext
               compaction → compaction/basic  compaction/toolresultpruner
4. tool        core/tools  guard/timeoutpolicy  guard/repeattoolreminder
               interaction/*  todo/tool  plan/planmode  mcp
               session/checkpointpolicy   ← 从第 2 块挪来，见下
               sessionquery/tool  spill/policy  ← 同上
5. skill       skill → skill/toolskill → preset → preset/persona
6. loop        core/scope  core/systemprompt  core/session  core/agent  core/loop
               core/defaultmodel
               session/title → session/title/llm
               → session/title/firstprompt  session/title/allprompts
               llm → llm/retry → llm/openai  llm/tokenmeter
7. 测试脚手架  testsupport/llmreplay  testsupport/llmmockserver
               testsupport/agentlooptestkit  testsupport/acpsnapshot
               testsupport/loadersmoke
8. 多 agent    subagent → subagent/inprocessdriver
               → subagent/spawninprocess  subagent/forkinprocess
               subagent/tool*  jobs → jobs/local → jobs/tool  schedule
               workflow  workflow/toolralph
               goal → goal/rounddriver  goal/tool  goal/command
9. 对外协议    sdk/protocol → sdk/server   acp
```

**`session/checkpointpolicy` 从第 2 块挪到第 4 块。** 它整个包（113 行）就是三处
拦截：模型请求前、顶层工具分派前、每个 step 前，各自 `await sessions.flush()`。
三处拦截点分别属于 `llm`、`core/tools`、`core/agent`，一个都不在第 2 块。
在第 2 块写它，只能造一个没有任何调用方的 flush 接口出来，然后等第 4、6 块
来决定它长得对不对——那是一次凭空的猜测，不是移植。它拦的是工具副作用，
所以跟着 `core/tools` 走；`agent/pre-step` 那一处在第 6 块补上。
落盘能力本身（[session/persistence.WriteBehind] 的 Flush）第 2 块已经就位，
挪的只是「在哪几个位置调它」这条策略。

**`sessionquery/tool` 和 `spill/policy` 同样从第 2 块挪到第 4 块，理由同上。**
两个包都不是「读侧/外置能力再加一层」，而是**把已有能力接到工具管线上**：
`sessionquery/tool` 通篇是 `defineTool`、`ToolRunContext`、`GenericCallView`，
`spill/policy` 拦的是 `PostToolDecision` 和 `ToolExecution`。这些类型全在
`core/tools`，也就是第 4 块。第 2 块写它们，同样只能凭空造一套工具定义的形状
出来等第 4 块来纠——按 `session/checkpointpolicy` 那条先例，跟着 `core/tools` 走。
它们各自的底座（`sessionquery`、`spill`）留在第 2 块不动。

**第 7 块的位置是故意的，不能往后挪。** 前提 3、4 那些崩溃恢复路径要在精确的位置断开，
真模型给不了这个精确度——`llm-replay` 不先就位，第 8 块的测试就只能靠手动碰运气。
第 1 到 6 块用不上它（纯逻辑，直接构造输入即可），所以放在这里正好。

`typert/*` 不在这个顺序里：整支四个包全部不移（第三节 tool 那段），`typert/` 目录待删。
`mcp` 排在第 4 块末尾——它挂进 `ctx.tools`，`core/tools` 不先就位就没地方挂。

每块做完，消费方应该能立刻用上一部分，而不是等 82 个包全绿。

## 九、不变的规矩

- 裁决表 `docs/portmap/portmap.tsv` 继续维护，一行一个符号，`PENDING` 是唯一会让门禁变红的状态。
- 溯源注释 `// 源: packages/...:行号` 和 `// 新增: 理由` 由 `internal/devtools/portcheck` 机器校验。行号选填，一行可以引好几处，每一处路径都要在上游快照里真实存在。
- 注释、错误信息、测试信息一律中文；面向模型和线上协议的文本保持英文。
- 纯逻辑包覆盖率 ≥99%；有 I/O 的包低于这个数要在源码里写明为什么。
- 门禁：`gofmt -l .`、`go build ./...`、`go vet ./...`、`go test ./...`，
  外加 `GOOS=linux` 与 `GOOS=darwin` 的交叉编译。
