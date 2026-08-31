# ds-harness-go

一个 Go 模块，给**服务端** agent 用。

装出来是一个**空的 agent 运行时**：给它一个模型、一批工具、一批 skill、一段人格提示词，
它就能跑。今天那四样是「视频创意」，明天换成「股市分析」，这个模块一行都不用改。

能力来源是 [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness)（MIT，下称 DSH）。
**移植的是能力，不是字符**——凡是 Go 有现成办法的，用 Go 的办法，行为一致即可。
每个文件里的 `// 源: packages/...:行号` 注释指向它对应的 DSH 位置，
逐符号裁决记在 `docs/portmap/portmap.tsv`。

它**不是**什么：DSH 是桌面编程助手，一半的包在操作本机文件系统。
这里是服务化、多用户的，**agent 不执行任意代码，也不碰服务器的目录和命令**——
`shell` / `sandbox` / `terminal` / `subprocess` 那几支**整支不装**——
连零本机前置的纯接缝一起，因为执行命令的前置是沙箱，而服务端不提供沙箱。
`fs` 那支的接缝要，后端挂对象存储（S3 / MinIO），`fs-local` 和 `tool-fs` 不装。
详见 `docs/DESIGN.md`。

## 架构

### 分层

```
        ┌─────────────────────────────────────────────────────────────┐
        │  消费方（aiboys-go，或者别的业务）                          │
        │  给四样东西： 模型   工具集   skill 集   人格提示词          │
        └──────────────────────────┬──────────────────────────────────┘
                                   │  HTTP
╔══════════════════════════════════▼══════════════════════════════════╗
║  对外协议    sdk/protocol ──► sdk/server        acp                 ║
║              方法 initialize / session.prompt / shutdown            ║
║              通知 session.event / status / subagent.started|finished║
╟─────────────────────────────────────────────────────────────────────╢
║  装配        boot ──► preset ──► preset/persona                     ║
║              「这个用户挂了哪几个业务包」→ 组装出他这次的            ║
║               工具清单 / skill 目录 / 系统提示词                    ║
╟─────────────────────────────────────────────────────────────────────╢
║  一轮对话    core/agent ──► core/loop                               ║
║                 │                                                   ║
║                 ├─ core/systemprompt ◄── context/* ◄── skill        ║
║                 ├─ core/tools ◄── guard ◄── interaction             ║
║                 └─ llm ──► llm/retry ──► llm/openai ──► 本地模型    ║
╟─────────────────────────────────────────────────────────────────────╢
║  跨轮次      compaction     spill     sessionquery                  ║
║              对话变长了压   结果太大了外置   要翻旧账时查           ║
╟─────────────────────────────────────────────────────────────────────╢
║  多 agent    subagent ──► driver ──► spawn │ fork                   ║
║  与后台活    jobs ──► jobs/local      schedule      workflow        ║
╟─────────────────────────────────────────────────────────────────────╢
║  持久化      session ──► storage/domain ──► storage                 ║
║              credentials     attachment     fs ──► 对象存储         ║
╟─────────────────────────────────────────────────────────────────────╢
║  底座        invariants   util/timeout   util/outputretention       ║
╚══════════════════════════════════╤══════════════════════════════════╝
                                   │
                          storage/postgres
```

**双线框里是 ds-harness-go，一个字的业务都没有。** 上面那个框是消费方。
业务包（工具 + skill + 人格）从上面塞进来，ds-harness-go 只管把它们组装起来跑。

### 一轮对话怎么跑

```
  用户消息
     │
     ▼
  ┌──────────────────────────────────────────────────────────┐
  │ 1. 组装上下文                                            │
  │    systemprompt ← 人格 + 指令 + skill 目录（只有目录）   │
  │    历史消息   ← session/projection（事件日志投影出来的） │
  │    压缩       ← compaction，如果太长                     │
  └────────────────────────┬─────────────────────────────────┘
                           ▼
  ┌──────────────────────────────────────────────────────────┐
  │ 2. 叫模型            llm → retry → openai                │  ◄──┐
  └────────────────────────┬─────────────────────────────────┘     │
                           ▼                                       │
                    模型要调工具？                                 │
                     ┌─────┴─────┐                                 │
                    否           是                                │
                     │            ▼                                │
                     │  ┌────────────────────────────────────┐     │
                     │  │ 3. guard 拦一道（超时、重复调用）  │     │
                     │  │ 4. 要审批？ interaction/userapproval│    │
                     │  │ 5. 执行，结果太大 → spill 外置     │     │
                     │  │ 6. 落盘 ← session/checkpoint       │     │
                     │  └────────────────┬───────────────────┘     │
                     │                   └─────────────────────────┘
                     ▼
                  这轮结束
```

**第 6 步是整个设计的理由。** 落盘点决定「进程死在哪儿丢多少」——
DSH 是桌面工具，人走了进程也就关了，它不需要面对这件事；我们是服务，
用户可能干到一半走人，第二天回来接着干。

### 接缝和实现是分开的两件事

这条是整个移植里最容易搞错的，也是上一版范围表错得最厉害的地方：

```
  storage/            ← 接缝。只是一个接口，零前置条件
     ├── memory/      ← 实现
     └── postgres/    ← 实现

  fs/                 ← 接缝。同样零前置，要
     ├── objectstore/ ← 实现，S3 / MinIO。要
     ├── fs-local     ← 实现，读服务器磁盘。不要
     └── tool-fs      ← 工具，把上面那个给模型用。不要
```

DSH 那 227 个包里，`fs` / `shell` / `sandbox` / `terminal` / `subprocess` /
`coderuntime` / `lsp` / `e2b` 八支共 35 个包，**接缝 11 个 + 实现和工具 24 个**。
上一版按域名整支划掉，把接缝和实现一起砍了——这是错的，接缝本身不碰任何机器资源。

**但接缝零前置不等于接缝就该装。** 逐个定完之后：`fs` 那条要，因为它挂得上一个
不碰机器的后端（对象存储）；`shell` / `sandbox` / `terminal` / `subprocess` /
`coderuntime` / `lsp` / `e2b` 那七条不要——它们的语义是「执行」，执行的前置是沙箱，
服务端不提供沙箱，所以这几条接缝**一个实现方都挂不上**。接缝没有实现方就是空的。

## 目录

两条规则，没有例外：

1. **镜像 DSH 的域分组**，`<域>/<包>`。
2. **接缝包占域名本身**，同域其它包挂在它下面——和标准库 `database/sql` +
   `database/sql/driver` 一个写法。

DSH 227 个包的逐包裁决在 `docs/portmap/rulings.md`：需要 82、抄形状 15、
Go 已有等价物 3、不要 127、**说不清 0**。下面这棵树是那 82 个。

| 标记 | 含义 |
|---|---|
| （无） | 已经有了 |
| `○` | 裁决是**要**，还没建 |

```
ds-harness-go/
│
│  ── 底座 7 ────────────────────────────────────────────────────────
├── invariants/               不变量注册表。程序写错了才会响的那类断言，被到处依赖
├── util/
│   ├── timeout/              超时
│   └── outputretention/      工具输出过长时的截断策略
├── boot/                  ○  启动路径、环境加载、故障处理、Profile
├── settings/              ○  设置
├── workspace/             ○  工作区
├── hooks/                 ○  接缝：钩子协议（只要协议，不要 claudecode/codex 那两个方言桥）
│
│  ── 持久化 15 ──────────────────────────────────────────────────────
├── storage/                  接缝：键值存储。表名 + 键 + 一段不透明 JSON，后端可换
│   ├── storagetest/          所有后端共用的一致性测试套件
│   ├── domain/            ○  领域层：给每个单元一条写链，把并发的写串起来
│   ├── memory/            ○  内存后端。上层包的测试走它，不碰任何数据库
│   └── postgres/          ○  Postgres 后端。生产用的那一个
├── session/               ○  接缝：会话持久化
│   ├── postgres/          ○  Postgres 实现
│   ├── checkpoint/        ○  存档点策略：什么时候该落一次盘
│   ├── projection/        ○  把事件日志投影成喂给模型的消息序列
│   ├── projectioncache/   ○  投影结果缓存
│   ├── stats/             ○  每会话的 turn / step / token 计数。预算闸门的数据源
│   └── telemetry/         ○  接缝：遥测外发（emit / flush / shutdown），后端走 OTLP over HTTP
├── sessionquery/          ○  接缝：会话检索
│   └── tool/              ○  给模型的检索工具
├── spill/                 ○  接缝：过大的工具结果外置。不外置就撑爆上下文
│   └── policy/            ○  多大算大、外置之后拿什么替换
├── credentials/              接缝：凭据。两套不相交的键空间（引用名 / 记录地址）+ 变更通知
├── attachment/               接缝：附件。内容寻址，与存储介质无关
├── fs/                    ○  接缝：readText / writeText / listDir，不规定 target 在哪
│   └── objectstore/       ○  实现：S3 / MinIO。碰机器的 fs-local 和 tool-fs 都不装
│
│  ── 上下文管理 6 ───────────────────────────────────────────────────
├── context/                  （只是分组目录，本身不是包）
│   ├── instructions/      ○  人格与指令
│   ├── sessionref/        ○  会话引用
│   └── timecontext/       ○  当前时间。DSH 采浏览器时区，我们改成由消费方传时区
├── compaction/            ○  接缝：对话变长之后怎么压
│   ├── basic/             ○  基础压缩后端：整段总结，默认 80% 触发
│   └── toolresultpruner/  ○  压缩前先裁掉旧的工具结果
│
│  ── tool 11 ───────────────────────────────────────────────────────
├── core/
│   └── tools/             ○  工具集合
├── guard/                    （分组目录）跑之前挡一道的那些检查
│   ├── timeoutpolicy/     ○  超时策略
│   └── repeattoolreminder/ ○ 同一个工具反复调时提醒模型。死循环每转一轮都是一次付费调用
├── interaction/              （分组目录）需要人介入的那几件事
│   ├── userapproval/      ○  工具执行前要审批
│   ├── askuser/           ○  模型反问用户
│   ├── userquestions/     ○  模型向用户提问
│   ├── commands/          ○  斜杠命令注册表
│   └── permissionpresets/ ○  权限预设
├── mcp/                   ○  MCP 客户端：把外部 MCP 服务器的工具桥进来。只要 HTTP 传输
├── todo/                  ○  给模型的待办清单工具
├── plan/                  ○  计划模式
│
│  ── skill 与业务包 4 ───────────────────────────────────────────────
├── skill/                 ○  接缝：技能注册表（provider 模式，目录查库、正文取 S3）
│   └── toolskill/         ○  给模型的加载工具
├── preset/                ○  接缝：业务包与会话组装
│   └── persona/           ○  人格
│
│  ── loop 14 ──────────────────────────────────────────────────────
├── core/
│   ├── agent/             ○  一个 agent 的身份与配置
│   ├── loop/              ○  拿着上面这些把一轮对话跑完
│   ├── session/           ○  会话对象
│   ├── scope/                作用域分层。全局层 + 作用域链，近的层盖远的层
│   ├── systemprompt/      ○  系统提示词的组装（只管结构，内容由消费方给）
│   └── defaultmodel/      ○  记住默认用哪个模型；多用户下要按用户再叠一层
├── session/
│   └── title/             ○  接缝：会话标题
│       ├── llm/           ○  三个提供方的共享策略与路由
│       ├── firstprompt/   ○  按首条消息生成。默认走这条，最省
│       └── allprompts/    ○  总结全部消息生成。更好也更贵
├── llm/                   ○  接缝：模型调用
│   ├── openai/            ○  OpenAI 兼容适配器。本地模型走这条（DSH 的 llm-pi-ai）
│   ├── retry/             ○  重试
│   └── tokenmeter/        ○  token 计量
│
│  ── 多 agent 与后台活 17 ───────────────────────────────────────────
├── subagent/              ○  接缝：startContinuable / followup / interrupt / reportFrom
│   ├── driver/            ○  进程内驱动，spawn 与 fork 共用
│   ├── spawn/             ○  全新空白子 agent
│   ├── fork/              ○  继承父级已完成轮次的前缀
│   ├── tool/              ○  给模型的委派工具
│   ├── toolcontrol/       ○  send_message / interrupt_agent
│   └── toolreport/        ○  子 agent 向启动者上报
├── jobs/                  ○  接缝：后台任务
│   ├── local/             ○  进程内实现
│   └── tool/              ○  给模型的任务工具
├── schedule/              ○  定时
├── workflow/              ○  接缝：工作流引擎
│   └── ralph/             ○  Ralph 固定工作流
├── goal/                  ○  接缝：会话内的一个长期目标（目标文本、phase、轮数／上限）
│   ├── rounddriver/       ○  续行驱动：agent 一空闲就自动排下一轮，「一直跑」靠它
│   ├── tool/              ○  给模型的 get_goal / create_goal / update_goal
│   └── command/           ○  给人的 /goal pause | resume | clear
│
│  ── 对外协议 3 ─────────────────────────────────────────────────────
├── sdk/                   ○  JSON-RPC 协议类型与方法表
│   └── server/            ○  服务端。DSH 走 stdio，我们换 HTTP，方法表不变
├── acp/                   ○  ACP 协议
│
│  ── 测试脚手架 5 ───────────────────────────────────────────────────
├── llm/
│   └── mockserver/           测试用的假模型服务
├── testsupport/
│   ├── llmreplay/         ○  录制／回放模型响应。没它就写不了崩溃恢复的测试
│   ├── agentloop/         ○  loop 测试套件
│   ├── acpsnapshot/       ○  ACP 快照
│   └── loadersmoke/       ○  装载冒烟
│
├── cmd/
│   └── llmmockserver/        把 llm/mockserver 起成一个进程，手工联调用
│
├── tools/                    本仓库自己的开发工具，不是运行时的一部分
│   ├── portmap/              从 DSH 源码抽出符号清单
│   ├── capmap/               从 DSH 源码抽出能力清单
│   ├── rule/                 维护裁决表
│   └── portcheck/            门禁：查覆盖率和溯源注释
│
└── docs/
    ├── DESIGN.md             设计
    └── portmap/
        ├── functions.md      DSH 2009 条功能，一条一件事
        ├── required.md       五条前提推出来的必需集
        ├── rulings.md        227 个包逐包裁决 + 怎么抄
        └── portmap.tsv       逐符号裁决表，门禁读它
```

`core/`、`guard/`、`interaction/`、`context/` 是分组目录，本身不是包；
`session/` 在树里出现两次是因为它横跨持久化和 loop 两块，实际是一个目录。

**227 行全部有终判，说不清清零。** 已经定完的几件事，免得以后重问：
「执行」那一整支不要——`shell` / `sandbox` / `terminal` / `subprocess` / `coderuntime` /
`lsp` / `e2b` 七支，连它们那几个零本机前置的纯接缝一起出局，因为执行的前置是**沙箱**，
服务端不提供沙箱，接缝一个实现方都挂不上。
联网（`web`）六个**不要，但是推后**——三个搜索提供方全要第三方 API key，现在没有数据源；
接缝零依赖、不在主干挂载序里，拿到数据源再补不动已有代码。
目标（`goal`）四个**要**：一次交代、agent 自己一轮接一轮跑到完成或轮数上限。
但它只数轮数，不计 token、不计钱、不计时间——**预算闸门不在这四个包里，消费它的那一层自己加**
（数据源是 `session/stats`）；而且续行权限从不持久化，恢复会话后目标还在但不自动重跑，
得人点一下 resume。
MCP（`mcp/mcp-client`）**要**：它自己不提供能力，只是个插口，让消费方不用为每个第三方
服务各写一遍工具。**只移 `streamable-http` 传输**，`stdio` 那半要在本机 spawn 子进程，不移。

Go 的包名保持短名（`package credentials`、`package postgres`），只有导入路径带域。

## 门禁

```
gofmt -l .
go build ./...
go vet ./...
go test ./...
GOOS=linux go build ./...     # 交叉编译
GOOS=darwin go build ./...
go run ./tools/portcheck      # 覆盖率与溯源注释
```

裁决表里 `PENDING` 是唯一会让门禁变红的状态。

## 规矩

- 注释、错误信息、测试信息一律中文；面向模型和线上协议的文本保持英文。
- 纯逻辑包覆盖率 ≥99%；有 I/O 的包低于这个数要在源码里写明为什么。
- 溯源注释 `// 源: packages/...:行号` 和 `// 新增: 理由` 由 `tools/portcheck` 机器校验。
