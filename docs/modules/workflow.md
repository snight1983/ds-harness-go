# 后台任务、目标与工作流

本篇是汇总：后台作业、长期目标、耐久提醒、固定的多轮子 Agent 工作流，四件事各自的详细文档在最后一节。

## 定位

一次 agent 回合有头有尾：人说一句，模型答一段，结束。可真正的活儿常常不是这个形状——

```mermaid
flowchart LR
    A["「把这个仓库的测试全跑一遍」"] --> A1["要跑二十分钟<br/>人不会坐在那儿等"]
    B["「这周把文档补完」"] --> B1["跨很多个回合<br/>中间还会被别的事打断"]
    C["「三天后提醒我续期」"] --> C1["三天后这个进程<br/>大概率已经不在了"]
    D["「照着这个目标反复改，直到过」"] --> D1["要重复很多轮<br/>每轮不该背着前几轮的聊天记录"]
```

这四件事都超出了单次回合。本模块就是承接它们的地方。

```mermaid
flowchart LR
    A["四件事"] --> B["没有合并成一个<br/>万能工作流引擎"]
    B --> C["因为它们在三件事上不一样"]
    C --> C1["状态存在哪儿"]
    C --> C2["取消是什么意思"]
    C --> C3["谁有权推动它"]
```

---

## 架构：分界线是「状态存在哪儿」

真正的分界不是「做什么」，是**状态存在哪儿**——这一条决定了进程重启之后它还在不在。

```mermaid
flowchart TB
    subgraph Durable["状态写进会话事件日志 → 重启后还在"]
        G["长期目标"]
        S["耐久提醒"]
    end
    subgraph Volatile["状态只在进程内存里 → 重启就没了"]
        J["后台作业"]
        R["Ralph 工作流"]
    end
    Durable --> A["agent 那一层<br/>唤醒 · 送活儿进去"]
    Volatile --> A
    A --> T["工具运行时<br/>各自的模型侧工具"]
```

### 这不是完成度的差别

```mermaid
flowchart TD
    A{"一件事该不该活过重启"}
    A -->|"「三天后提醒我」"| B["必须活过<br/>否则这个承诺就是假的"]
    A -->|"一个正在跑的子进程"| C["活不过<br/>进程没了它就没了"]
    B --> D["所以前者落事件日志"]
    C --> E["所以后者只在内存里"]
    D -.->|"各自该有的样子"| E
```

### 四件事都不自己开循环

```mermaid
flowchart LR
    A["每一件都从 agent 那一层<br/>拿唤醒和送活儿的入口"] --> B["不自己起 goroutine 跑回合"]
    B --> C["否则就会有两个地方<br/>同时觉得自己在推动这个会话"]
```

---

## 后台作业

```mermaid
flowchart LR
    A["模型说：帮我把测试跑了"] --> B["起一个作业，立刻返回一个编号"]
    B --> C["模型接着干别的"]
    C --> D["过一会儿拿编号回来看结果"]
```

### 一个作业的一生

```mermaid
stateDiagram-v2
    [*] --> 运行中: 起
    运行中 --> 收尾中: 请求终止
    运行中 --> 已完成: 自己跑完了
    运行中 --> 已失败: 出错
    运行中 --> 已终止: 被取消
    收尾中 --> 已终止
    收尾中 --> 已失败
    已完成 --> [*]
    已失败 --> [*]
    已终止 --> [*]
```

### 归属决定可见性

```mermaid
flowchart TD
    A["每个作业都记着是谁起的"] --> A1["列举只列自己的"]
    A --> A2["读输出只读自己的"]
    A --> A3["通知只送给自己"]
    A --> A4["并发上限按拥有者各算各的"]
    A --> A5["拥有者的作用域关掉<br/>它起的作业跟着取消回收"]
```

### 输出是可以边跑边读的

```mermaid
flowchart LR
    A["还在跑"] --> B["增量读，读到哪儿算哪儿"]
    C["跑完了"] --> D["最终结果可以反复读"]
```

### 等待超时不是作业失败

```mermaid
flowchart TD
    A["「等这个作业 30 秒」"] --> B{"30 秒到了还没完"}
    B -->|"交出当前快照"| C["调用方知道：还在跑，我等腻了"]
    B -->|"报一个失败"| D["调用方以为：它挂了"]
    D -.->|"这两件事必须分得开"| C
```

### 进程内实现的边界

```mermaid
flowchart LR
    A["仓库自带的那个实现<br/>把记录放在内存里"] --> B["进程退出，作业本身恢复不了"]
    C["要真正耐久的后台执行"] --> D["宿主自己实现那套接口<br/>把执行交给它的任务平台"]
```

---

## 长期目标

```mermaid
flowchart LR
    A["「这周把文档补完」"] --> B["这不是一次回合能做完的"]
    B --> C["它得跨很多轮，还得记得自己是谁"]
```

### 目标状态整个落在事件日志上

```mermaid
flowchart TD
    A["每次改动写一条事件<br/>事件里是完整的目标快照"] --> B["带一个修订号"]
    B --> C["改的时候要求「号必须还是这个」"]
    C --> D{"号变了吗"}
    D -->|"没变"| E["写下去"]
    D -->|"变了"| F["不写，说明有人先改了"]
```

### 四个阶段

```mermaid
stateDiagram-v2
    [*] --> 进行中: 建
    进行中 --> 已暂停: 暂停
    已暂停 --> 进行中: 继续
    进行中 --> 受阻: 卡住了
    受阻 --> 进行中: 解开了
    进行中 --> 已完成
    已完成 --> [*]
```

### 「自动往下推」这个授权不落盘

```mermaid
flowchart TD
    A["恢复一段旧会话 / 分叉一段会话 / 换了个进程"] --> B{"目标会不会自己跑起来"}
    B -->|"不会（当前做法）"| C["必须有人显式说「继续」"]
    B -->|"会"| D["一段两个月前的日志<br/>被读回来的那一刻自己动了起来"]
    D -.->|"旧日志不许自动触发新行为"| D
```

### 续推驱动：只在真正空闲的时候插一轮

```mermaid
sequenceDiagram
    participant D as 续推驱动
    participant A as agent
    D->>A: 它现在闲着吗
    A-->>D: 闲着
    D->>D: 目标还有效吗（复核修订号）
    D->>A: 排一轮提示进去
    A->>A: 认领这一轮
    D->>D: 认领的时候再复核一次
    A-->>D: 跑完，写事件
    D->>D: 写之前再复核一次
```

### 什么时候让步

```mermaid
flowchart TD
    A["正准备推下一轮"] --> B{"有没有这几种情况"}
    B --> B1["人刚说了句新的"] --> Z["这一轮让开"]
    B --> B2["被取消了"] --> Z
    B --> B3["目标阶段变了"] --> Z
    B --> B4["轮数到上限了"] --> Z
```

---

## 定时提醒

```mermaid
flowchart LR
    A["计划和投递都写成会话事件"] --> B["内存里的定时器<br/>只是照着事件重建出来的当前状态"]
    B --> C["丢了可以重建<br/>所以它不是事实的存放处"]
```

### 三种说法

```mermaid
flowchart TD
    A["定一个提醒"] --> A1["过多久之后<br/>一次性"]
    A --> A2["到某个时刻<br/>一次性"]
    A --> A3["每隔多久<br/>反复"]
```

### 时间这件事上的三个决定

```mermaid
flowchart TD
    A["一律按 UTC 写下来，到毫秒"] --> A1["两台机器读同一条记录<br/>算出来是同一个时刻"]
    B["本地时间认时区名<br/>不认时差数字"] --> B1["时差会随夏令时变<br/>时区名不会"]
    C["夏令时那两种怪时刻明确处理"] --> C1["一年里有一小时出现两次<br/>还有一小时根本不存在"]
```

### 会话不在线的时候

```mermaid
flowchart TD
    A["到点了，可那个会话没开着"] --> B["这条提醒挂成「已过期未投递」"]
    B --> C["等那个会话回来了再投"]
    D["周期提醒睡了三天"] --> E["醒来只响最新的那一次"]
    E -.->|"不把错过的几十次<br/>一口气全补出来"| E
```

### 先写事件，再动定时器

```mermaid
flowchart LR
    subgraph BAD["反过来"]
        A1["先改定时器"] --> A2["再写事件"] --> A3["事件没写成"]
        A3 --> A4["于是有一个日志里根本不存在的提醒<br/>在到点的时候响了"]
    end
```

```mermaid
flowchart LR
    subgraph GOOD["当前做法"]
        B1["先提交事件"] --> B2["成了再更新定时器"]
        B2 --> B3["写不成 ＝ 这次计划压根没发生"]
    end
```

### 投递是原会话本地的

```mermaid
flowchart LR
    A["到点了往原来那个会话投"] --> B["不是一套跨集群、保证只投一次的调度服务"]
    B --> C["多实例部署要宿主自己解决<br/>「这个会话归哪台机器」"]
```

---

## Ralph 工作流

```mermaid
flowchart LR
    A["一个不变的目标"] --> B["每一轮开一个全新的子 agent"]
    B --> C["它只拿到：目标 ＋ 上一轮的一份有界报告"]
    C -.->|"不继承前几轮的聊天记录"| C
```

### 为什么每轮都换新的

```mermaid
flowchart TD
    subgraph BAD["一路带着历史往下滚"]
        A1["第 8 轮的上下文里<br/>塞着前 7 轮的全部弯路"]
        A1 --> A2["越滚越长，还越滚越偏<br/>早期那些错误的判断一直在场"]
    end
```

```mermaid
flowchart TD
    subgraph GOOD["每轮从零开始"]
        B1["长期事实放在共享工作区里"]
        B2["轮与轮之间只传一份结构化报告"]
        B1 --> B3["新一轮的上下文<br/>只有目标和刚才那一步的结论"]
        B2 --> B3
    end
```

### 一轮接一轮

```mermaid
sequenceDiagram
    participant T as Ralph 工具
    participant W1 as 子 agent 第 1 轮
    participant W2 as 子 agent 第 2 轮
    T->>W1: 目标（没有上一轮报告）
    W1-->>T: 结构化报告
    T->>T: 校验报告格式
    T->>W2: 目标 ＋ 第 1 轮的报告
    W2-->>T: 结构化报告
    T->>T: 继续 / 完成 / 受阻 / 到上限
```

### 「做完了」只被转述，不被采信

```mermaid
flowchart LR
    A["子 agent 报告说完成了"] --> B["本模块校验这份报告的格式"]
    B --> C["然后原样转述出去"]
    C -.->|"它证明不了<br/>工作区里的结果是对的"| C
```

### 它是个前台工具

```mermaid
flowchart LR
    A["Ralph 跑在一次工具调用里"] --> B["调用方在等它"]
    C["要在后台派活儿"] --> D["用后台作业，或者普通的子 agent 派发"]
```

```mermaid
flowchart LR
    A["实现是一个写死的 Go 循环"] --> B["不跑用户脚本"]
    B --> C["也不是一个通用的脚本工作流引擎"]
```

---

## 生命周期与并发

```mermaid
flowchart TD
    A["状态改动在写入点串起来"] --> A1["回调一律在内部锁外跑"]
    B["目标和提醒从事件日志重整"] --> B1["内存那份可以随时丢弃重建"]
    C["作业和 Ralph 的状态"] --> C1["丢了就是丢了"]
    D["拥有者的作用域关掉"] --> D1["挂在它上面的作业一并取消回收"]
```

### 每一个边界都要复核一次

```mermaid
flowchart LR
    A["排队的时候查一次"] --> B["认领的时候再查一次"] --> C["写事件之前又查一次"]
    C --> D["因为这三步之间隔着时间<br/>人可能在任何一处改了配置"]
    D -.->|"一个旧任务不许盖掉新配置"| D
```

---

## 失败语义

```mermaid
flowchart TD
    A["出了岔子"] --> B{"哪一类"}
    B -->|"取消"| C["取消是一个请求，不是一个结论"]
    C --> C1["最终状态以执行方实际结算为准<br/>中间那段照样可能跑完"]
    B -->|"等待超时"| D["交出当前快照<br/>不伪装成作业失败"]
    B -->|"定时落盘失败"| E["等于这次计划没有发生"]
    B -->|"清理某一项失败"| F["继续清其余的<br/>最后把多个失败汇总交出去"]
    B -->|"模型报告「完成」"| G["只校验格式并转述<br/>不采信"]
```

---

## 能力边界

```mermaid
flowchart TD
    Y["这一块做的"] --> Y1["把超出单次回合的活儿接住"]
    Y --> Y2["按「状态存在哪儿」给出四种不同的耐久性"]
    Y --> Y3["复用同一套 agent 与工具设施"]
    N["不做的"] --> N1["不提供通用的图形化 / 脚本工作流引擎"]
    N --> N2["不保证内存里的作业活过重启"]
    N --> N3["不当分布式定时器、队列或选主服务"]
    N --> N4["不自动认定模型说的「目标完成了」是真的"]
    N --> N5["不绕过 agent、工具和存储那几层的权限"]
```

## 对应的 DSH 能力

本篇是汇总文档，下表是它覆盖的各篇详细模块文档的并集，由 [`docs/packages.md`](../packages.md) 与 [能力覆盖表](../portmap/capability-coverage.tsv) join 得到。

| 上游能力 | DSH 包 | 裁决 | 落在哪个 Go 包 | 这里缺什么 |
|---|---|---|---|---|
| 面向用户的/goal控制，基于ctx.goals实现，提供状态展示和create/edit/pause/resume/clear命令 | `goal/command-goal` | 需要 | `feature/goal/goalcommand` | — |
| 事件溯源的同会话目标状态，维持当前待完成目标和续行权限 | `goal/goal` | 需要 | `feature/goal` `feature/goal/goaltool` | — |
| ctx.goals的同会话续行驱动器，把phase为active且已启用续行的目标转换为连续Goal Round | `goal/goal-round-driver` | 需要 | `feature/goal/goalrounddriver` | — |
| ctx.goals的面向模型控制API：get_goal、create_goal和update_goal | `goal/tool-goal` | 需要 | `feature/goal/goaltool` | — |
| 后台任务注册表约定，为长时间运行的生产方提供共享id、owner隔离、读取、取消、等待、通知和清理 | `jobs/jobs` | 需要 | `feature/jobs` | — |
| ctx.jobs注册表约定的进程本地实现，把每条记录保存在内存中并按kind签发id | `jobs/jobs-local` | 需要 | `adapter/domainjobs` `adapter/localjobs` | — |
| ctx.jobs的面向模型控制器，提供job_output、job_list和job_kill三个与kind无关的工具 | `jobs/tool-jobs` | 需要 | `feature/jobs/jobstool` | — |
| 为未来创建的live根agent提供三个会话范围内的工具管理持久提醒 | `schedule/schedule` | 需要 | `feature/schedule` | — |
| 基于已配置provider的面向模型委派工具，前台或后台执行subagent任务 | `subagent/tool-subagent` | 需要 | `feature/workflow/toolralph` | 缺一角：子 agent 的模型选择授权表。descriptor.go 的 AgentProvider／AgentModel 是装配期定死的，模型自己挑不了，也没有「许挑哪几条路由」的授权表。补的入口：工具 schema 加 provider／model／reasoning_effort，授权表以 subagent/model-selection-policy 事件进日志（只进日志不进模型历史），配一个投影单元读回来 |
| 面向模型的ralph工具，运行固定的前台工作流把目标依次交给多个全新子agent | `workflow/tool-ralph` | 需要 | `feature/workflow/toolralph` | — |
| 工作流seam定义脚本、运行、结果、错误和事件契约，worker-thread是当前引擎实现 | `workflow/workflow` | 需要 | `feature/workflow` | 缺一角：只有接缝没有引擎。脚本正文、meta 校验、组合子那套 API 都留给实现方兑现，本仓库不带产出方；Ralph 不经过这条接缝，它的编排写死在循环里 |

## 相关源码

| 路径 | 内容 |
|---|---|
| `feature/jobs/` | 作业契约、状态和 Registry |
| `adapter/localjobs/` | 进程内作业实现 |
| `feature/jobs/jobstool/` | 模型作业工具 |
| `feature/goal/` | 目标事件、状态、revision 和服务 |
| `feature/goal/goalrounddriver/` | 空闲续推驱动 |
| `feature/goal/goaltool/`、`feature/goal/goalcommand/` | 模型工具与宿主命令入口 |
| `feature/schedule/` | 耐久计划、时间解析和投递运行时 |
| `feature/workflow/` | 工作流能力接缝：开工请求、运行句柄、六条生命周期边 |
| `feature/workflow/toolralph/` | 固定多轮子 Agent 工作流 |

## 深入阅读

[后台作业](jobs.md) · [长期目标](goal.md) · [耐久提醒](schedule.md) · [工作流与 Ralph](ralph.md)
