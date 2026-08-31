# 必需集与缺口

前提定死之后，「哪些必须有」就不是猜的了。这份从 `functions.md` 那 2009 条里推必需集，
每一条必需和每一个缺口都指得到出处。

## 一、前提

消费方给的四条，原话：

1. 多用户并发
2. 可以恢复历史对话
3. 一个活可能用户干了一半走了
4. 第二天接着干
5. 多 agent 协作

第 3、4 条是决定性的：**进程会在一个活的中间死掉，而这个活得跨天活下来。**
DSH 是桌面单机工具，人走了进程也就关了，它从来不需要面对这件事——
所以下面第三节那些缺口对它不成立，对我们成立。

## 二、必需集

### 第一层：主干

不是我选的，是 DSH 自己写的。`examples/agent-spine-demo` 的挂载清单就是作者定义的
「跑一个 agent 最少要装什么」，而且他在清单里自己标了哪几个是可选的。

| 挂载 | 包 |
|---|---|
| dsh-llm | `llm/llm` |
| dsh-llm-retry | `llm/llm-retry` |
| dsh-session | `core/session` |
| dsh-session-title | `session/session-title` |
| dsh-system-prompt | `core/system-prompt` |
| dsh-tools | `core/tools` |
| dsh-agent | `core/agent` |
| dsh-agent-loop | `core/agent-loop` |
| dsh-agent-instructions | `context/agent-instructions` |
| dsh-skill | `skill/skill` |
| dsh-tool-skill | `skill/tool-skill` |
| dsh-jobs-local | `jobs/jobs-local` |
| dsh-tool-jobs | `jobs/tool-jobs` |
| dsh-invariants + 四个 invariant 配套 | `runtime-diagnostics/*` |

作者自己标可选的：`goal/goal`、`goal/tool-goal`、`goal/goal-round-driver`——
这三个加 `goal/command-goal` 后来由消费方定为**要**，见 `rulings.md`。
带开关的：`shell/tool-bash`（`toolBash=false` 可关）。碰本机的：`skill/skill-filesystem`。

### 第二层：会话可恢复（前提 2、3、4）

| 包 | 为什么必需 |
|---|---|
| `session/session-persistence` | 接缝本身。`create/append/prepare/load/readFrom/list` |
| `session/session-checkpoint-policy` | 三个落盘点：模型请求前、工具分派前、pre-step。决定「崩在哪儿丢多少」 |
| `session/session-projection` | 事件日志投影成喂给模型的消息序列 |
| `compaction/compaction` | 跨天的会话必然变长。带持久锁的崩溃恢复 |
| `storage/storage` + `storage/storage-domain` | 上面这些的落点 |

### 第三层：多用户（前提 1）

| 包 | 为什么必需 |
|---|---|
| `credentials/credentials` | 每次操作 `resolve()`，归属校验在接缝上而不是靠调用方自觉 |
| `attachment/attachment` | 内容寻址，与介质无关 |
| `core/scope` | `ScopedLayers`：全局层 + 作用域链，近的层盖远的层 |

### 第四层：多 agent 协作（前提 5）

| 包 | 为什么必需 |
|---|---|
| `subagent/subagent` | 接缝。`startContinuable` / `followup` / `interrupt` / `reportFrom` |
| `subagent/subagent-in-process-driver` | 进程内驱动，spawn 与 fork 共用 |
| `subagent/subagent-spawn-in-process` | 全新空白子 agent |
| `subagent/subagent-fork-in-process` | 继承父级已完成轮次的前缀 |
| `subagent/tool-subagent` | 给模型的委派工具 |
| `subagent/tool-subagent-control` | `send_message` / `interrupt_agent` |
| `subagent/tool-subagent-report` | 子 agent 向启动者上报 |

四个进程外 provider（`-acp` / `-claude-code` / `-codex` / `-dsh-sdk`）都不在里面：
它们各自自陈「每次运行使用全新进程」「仅支持本地子进程」，且都不支持
`outputSchema` / `depthLimit` / `toolFilter` / `persona` 这四项启动时能力。

## 三、缺口：这五条前提要的东西，DSH 自陈没有

多 agent 那 17 个包，**100 条里 50 条「有」、50 条「自陈无」**——全清单里唯一对半开的域。
缺的那半不是边角，正好是前提 3、4 要的：

| 缺口 | 出处 | 撞上哪条 | 后果 |
|---|---|---|---|
| 驻留仅限进程内 | `subagent/subagent` | 4 | 进程重启，在跑的子 agent 全没 |
| 没有持久化的上报 mailbox | `subagent/subagent` | 3 | 子 agent 出的报告，人不在就丢 |
| 不回放已接受但未记录的消息 | `subagent/subagent` | 4 | 收下但没落盘的消息，恢复后不重放 |
| **约定是进程内的** | `jobs/jobs` | 4 | 是**接缝**自陈，不是实现自陈——换个实现也补不上。而 jobs 在主干里 |
| 任务只存在于进程本地 | `jobs/jobs-local` | 4 | 后台任务不跨天 |
| 待领通知无法在 owner 释放后存活 | `jobs/tool-jobs` | 3 | 任务完成通知投不到已离开的用户 |
| 单进程、共享 checkout | `experimental/agent-team` | 1 4 | 唯一带持久 peer mailbox + 共享任务板（CAS 版本化）的协作原语，用不了 |
| mailbox 不保证跨进程 exactly-once | `experimental/agent-team` | 1 | 同上 |
| 没有日志化或恢复 | `workflow/workflow` | 4 | 工作流中断即丢。（`tool-workflow` 的编排脚本是跑在 worker thread 上的 JavaScript，本来也移不过来） |
| 变更仅进程内可见 | `storage/storage-domain` | 1 | 两个进程看不到对方的写 |
| 无删除或保留接口 | `session/session-persistence` | 1 | 用户会话清理没接口 |
| `list` 无分页无过滤 | `session/session-persistence` | 1 | 用户会话列表拉不动 |

### 不是全空

这几件作者铺好了，不用自己写：

| 能力 | 出处 |
|---|---|
| 会话崩溃修复：合成闭合 | `session-persistence` 的 `interruptedTurnClosers` |
| 崩在哪儿丢多少可控 | `session-checkpoint-policy` 三个拦截点 |
| 压缩的崩溃恢复 | `compaction` 的持久锁信号 |
| 工具结果三态 | `core/agent-loop` 的 `TOOL_OUTCOME_UNKNOWN` 幂等崩溃恢复 |
| **子 agent 冷恢复** | `subagent` 的 `snapshotSubagentDescriptor()` / `foldSubagentDescriptor()` + `ctx.agents.resume()` |

最后一条是这一节里最要紧的分界：**冷恢复这条路作者铺好了，跨进程的驻留和上报投递没铺。**
要补的不是「从头做一套子 agent 持久化」，是「把已有的描述符快照接到一个跨进程的
驻留与投递机制上」。

## 四、这份不做的事

不含任何「不进范围」的裁决。上面没列到的包不等于不要——只等于**这五条前提没要求它**。
要不要，由消费方定。

逐包的裁决在 `docs/portmap/rulings.md`（227 行全部有终判：需要 82 / 抄形状 15 /
Go 已有等价物 3 / 不要 127 / 说不清 0），它以这份和 `functions.md` 为依据。
`docs/DESIGN.md` 第三、四节已按那张表重写，恢复效力。
