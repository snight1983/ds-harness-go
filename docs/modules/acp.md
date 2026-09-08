# ACP 接入

对应包：`protocol/acp`

## 定位

除了人坐在界面前面敲字，还有一类调用方：**另一个程序**。它想把这套 agent 当成一个可编程的服务用——建一段会话、扔一条提示词进去、等它跑完、把结果收走。

ACP（Agent Client Protocol）就是这类调用方说话的那套规矩。这个包实现它的**agent 端**，也就是被调用的那一半。

```mermaid
flowchart LR
    subgraph C["客户端那一半（不在本包）"]
        A(("一个自动化程序"))
    end
    subgraph S["agent 端（本包）"]
        B["桥"]
    end
    A -->|"建会话 / 提交提示词 / 取消 / 授权"| B
    B -->|"会话更新 / 请求授权"| A
    B --> RT["这套运行时"]
```

一句话概括这个包的性格：**它只搬已经落定的那一半**。

---

## 架构

```mermaid
flowchart TD
    CLI(("ACP 客户端")) <-->|"JSON-RPC / NDJSON<br/>编解码由现成的 SDK 负责"| CONN["协议连接"]
    CONN <--> BR["桥"]
    BR --> R1["agent 注册表"]
    BR --> R2["附件存储"]
    BR --> R3["模型解析"]
    BR --> R4["审批登记"]
    BR --> LOG[("会话日志<br/>发什么全看它")]
```

### 线上格式一行都不写

```mermaid
flowchart LR
    A["线上类型"] --> SDK["现成的 ACP Go SDK"]
    B["NDJSON 分帧"] --> SDK
    C["JSON-RPC 错误码"] --> SDK
    SDK --> D["本包只管：这一端怎么办事"]
```

那个 SDK 是由官方 schema 生成的，零间接依赖，协议版本对得上 DSH 那条线。

### 造和装分两步，因为这里有个环

```mermaid
sequenceDiagram
    participant H as 装配方
    participant B as 桥
    participant C as 协议连接
    H->>B: ① 造出桥（它就是那个 agent 实现）
    H->>C: ② 拿桥去造连接
    H->>B: ③ 把连接当对端交回去
    Note over B,C: 桥要连接才能往外发<br/>连接要桥才能造出来<br/>——环是协议本身的形状
```

---

## 只搬已提交的东西

```mermaid
flowchart TD
    LOG[("会话日志上已提交的事件")] --> Y1["助手消息 ✓"]
    LOG --> Y2["工具调用与结果 ✓"]
    LOG --> Y3["回合收尾 ✓"]
    X["还没落进日志的"] --> N1["原始流片段 ✗"]
    X --> N2["计划 ✗"]
    X --> N3["标题 ✗"]
    X --> N4["重试标记 ✗"]
```

**这不是「少发一点」，是「发出去的每一条都已经是事实」**：

```mermaid
flowchart LR
    A["流片段是过程中的猜测"] --> B["下一个片段就可能把它改掉"]
    B --> C["一个人看见了会自己脑补「它还在想」"]
    C --> D["一个程序看见了会照着它做决定"]
    D -.->|"然后做错"| D
```

---

## 能力跟着协作者走，不是跟着开关走

```mermaid
flowchart TD
    A["挂了 MCP 宿主"] --> A1["握手才声明 MCP 那项能力"]
    B["挂了持久化"] --> B1["握手才声明「列会话」和「续会话」"]
    C["挂了模型目录"] --> C1["才摆得出模型和推理档位这两个配置项"]
    D["哪一样没挂"] --> D1["对应的方法交回「未实现」<br/>而不是一句「找不到」"]
```

```mermaid
flowchart LR
    A["一个守规矩的客户端"] --> B["读一遍握手就够了<br/>不会去调没声明的方法"]
    C["这里的拒绝"] --> D["是留给不守规矩的那种的"]
    D --> E["而且它说的<br/>必须和握手说的是同一件事"]
```

---

## 一次提示词的来回

```mermaid
sequenceDiagram
    participant C as 客户端
    participant B as 桥
    participant A as agent
    C->>B: 提交一条提示词（文本 / 资源链接 / 内联图）
    B->>B: 全部校验一遍
    B->>A: 排进去，开一个回合
    A-->>B: 已提交的助手输出
    B-->>C: 会话更新（一条一条推）
    A-->>B: 回合结束
    B-->>C: 停止原因
```

### 图片先全验完再落盘

```mermaid
flowchart LR
    subgraph BAD["边验边写"]
        A1["第 1 张：写进附件存储"] --> A2["第 2 张：模型不支持"] --> A3["第 1 张已经落盘了<br/>半批提交"]
    end
```

```mermaid
flowchart LR
    subgraph GOOD["先全验再全写（当前做法）"]
        B1["三张全过一遍模型能力和附件准入"] --> B2{"都行吗"}
        B2 -->|"是"| B3["一起写"]
        B2 -->|"有一张不行"| B4["一张都不写，整条拒掉"]
    end
```

### 资源链接只传引用

```mermaid
flowchart LR
    A["客户端给一个资源链接"] --> B["本包原样往下传"]
    B -.->|"绝不自己去读一个任意的本地路径"| C(("×"))
```

---

## 授权：这条线上只有一次性的两档

```mermaid
flowchart TD
    A["某个工具要动手"] --> B["向客户端要一次授权决定"]
    B --> C{"客户端回了什么"}
    C -->|"这一次放行"| D["放行这一次"]
    C -->|"驳回"| E["驳回"]
    C -->|"一个认不出的回答"| F["不升格"]
    F -.->|"绝不折成一份耐久授权"| F
```

耐久的那一种在权限那一层，不在这条线上。

---

## 生命周期与并发

```mermaid
flowchart TD
    A["装上去：登记监听器"] --> B["交回一个幂等的清理函数"]
    C["收摊"] --> C1["① 先不再接新活儿"]
    C1 --> C2["② 排空还在续的那些"]
    C2 --> C3["③ 逐个拆掉自己建的 agent"]
```

### 它拥有什么

```mermaid
flowchart LR
    Y["拥有：它自己建出来的那些 agent"] --> Y1["收摊时逐个拆掉"]
    N1["不拥有：这套运行时"] --> W["因为只有装配方知道<br/>这个进程还有没有别的活儿"]
    N2["不拥有：那条通道"] --> W
    N3["不拥有：本进程"] --> W
```

DSH 那边直接读进程的标准输入输出来造流；这里那两样是装配方交进来的入参。

### 并发

```mermaid
flowchart LR
    A["多个请求可能同时进来"] --> B["agent 自己那条「一次一个回合」的规矩照旧"]
    B --> C["注册表的生命周期规矩也照旧"]
    C --> D["本包不额外放宽任何一条"]
```

---

## 失败语义

```mermaid
flowchart TD
    A["出了岔子"] --> B{"哪一类"}
    B -->|"配置缺了 / 会话不认得 / 内容非法 / 图片不受支持"| C["明确的协议错误"]
    B -->|"一批图片里有一张不行"| D["整批拒掉<br/>绝不半批提交"]
    B -->|"这套部署没接那个协作者"| E["交回「未实现」<br/>不伪造一次成功"]
    B -->|"客户端断了"| F["不等于这个 Go 服务要退出<br/>要不要重建连接由装配方决定"]
```

---

## 能力边界

```mermaid
flowchart TD
    Y["这个包做的"] --> Y1["ACP 的 agent 端语义：会话、提示词、取消、授权、输出投影"]
    Y --> Y2["把运行时的回合结束原因翻成协议的停止原因"]
    N["不做的"] --> N1["不做客户端那一半"]
    N --> N2["不建监听端口，也不管本进程的存活"]
    N --> N3["不做认证体系，也不做租户隔离<br/>凭据的解算在别处"]
    N --> N4["不自带会话持久化后端"]
    N --> N5["不发没落进会话日志的东西<br/>思维过程、工具轨迹的中间态、原始流片段都不发"]
    N --> N6["不把一次性审批折成耐久授权"]
```

## 对应的 DSH 能力

下表由 [`docs/packages.md`](../packages.md) 与 [能力覆盖表](../portmap/capability-coverage.tsv) 机器 join 得到：本篇覆盖的 Go 包，承接的是上游 DSH 的哪几条能力，以及各自还缺什么。落点列由源码里的 `// 源:` 注释反查，不是手写的。

| 上游能力 | DSH 包 | 裁决 | 落在哪个 Go 包 | 这里缺什么 |
|---|---|---|---|---|
| Agent Client Protocol仅自动化JSON-RPC服务器，通过stdio驱动harness agent | `acp/acp` | 需要 | `protocol/acp` | — |
| 纯自动化 ACP stdio 应用 profile 组合包，在 base 之上设 coding persona 与默认模型路由，命令提供方接受调用后才启动 ACP bridge | `bundle/acp-app` | 取形重写 | `protocol/acp` | 走 HTTP 不走 stdio，进程外壳那半不要。ACP 场景关掉 session-title-llm 这条取舍已记在裁决里 |

## 相关源码

- `protocol/acp/config.go`
- `protocol/acp/bridge.go` —— 桥本身：攥着哪些会话、怎么装上去、握手与收摊
- `protocol/acp/bridgesession.go` —— 会话台账：开、恢复、列出、关掉、配置项
- `protocol/acp/bridgeprompt.go` —— 一次提示词从准入到结算的状态机
- `protocol/acp/bridgeevents.go` —— 运行时那几条边翻成线上的话
- `protocol/acp/content.go`
- `protocol/acp/codec.go`
- `protocol/acp/invariant.go`
