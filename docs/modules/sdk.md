# SDK 协议与服务端

## 定位

`protocol/sdk/sdkprotocol` 定义进程外 SDK 与运行时之间的按行 JSON-RPC 2.0 协议；`protocol/sdk/sdkserver` 实现服务端请求处理和运行时通知转发。

## 架构与协议

```text
外部 SDK
   |
   | 一行一个 JSON-RPC 对象
   v
sdkprotocol.LineTransport
   |
   v
sdkserver.Server
   +-- Agent Registry
   +-- Session Store
   +-- Session Query Engine
   +-- LLM Provider Mount
   +-- Subagent Runtime
```

这条线上有十三个请求和四类通知。`serverInfo.name` 是稳定线上值，不能作为普通展示字符串随意修改。

```text
                握手三件                会话十件
        +-------------------+   +----------------------------+
        | initialize        |   | create   resume   fork     |
        | session/prompt    |   | rename   list     search   |
        | shutdown          |   | cancel   select-model      |
        +-------------------+   | update-queue    history    |
                                +----------------------------+

  通知（单向往回发，服务端不生产事实，只转发）
        session 事件 · agent 状态 · 子 agent 启动 · 子 agent 结束
```

## 请求语义

握手三件：

- `initialize` 设置这条服务连接共用的工作目录、provider、model 和可选输出上限。
- `session/prompt` 找到或创建 Agent，把用户输入排队，并立即返回入队消息 ID。
- 实际执行结果不在 `session/prompt` 响应中等待，而是通过事件和状态通知流返回。
- `shutdown` 释放本 Server 创建的 Agent，但不退出宿主进程。

会话十件自己不产生能力，只是把运行时里已有的能力挂到线上：

| 方法 | 干什么 | 真正干活的是 |
|---|---|---|
| `session/create` | 开一条新会话 | Agent Registry |
| `session/resume` | 把一条旧会话读回这条线上 | Agent Registry |
| `session/fork` | 从某个回合处分出一条新会话 | Query Engine + Registry |
| `session/rename` | 改标题 | 装配方交进来的改名回调 |
| `session/list` | 列出会话，含没活着的 | Query Engine（冷读） |
| `session/search` | 全文找会话 | Query Engine 背后的检索后端 |
| `session/cancel` | 叫停当前回合 | 活 Agent 自己 |
| `session/select-model` | 只给这一条会话换模型 | LLM 适配器 + 会话作用域 |
| `session/update-queue` | 改还没跑的那条队 | 活 Agent 的收件箱 |
| `session/history` | 往老里翻页 | Query Engine（冷读） |

十件分成两路，分界是「这条会话现在活着吗」：

```text
  活的这一路                          冷的这一路
  rename / cancel /                   list / search /
  select-model / update-queue         history / fork
        |                                   |
        v                                   v
  会话不在这条线上 -> 当场拒          不要求会话活着，直接读日志
  （不隐式读回来：一次拼错标识
    的取消不该悄悄启动陈年会话）
```

坏 JSON 行会被跳过，后续合法帧继续处理；请求 ID 配对、标准错误码和连接断开后的等待者释放由 `github.com/sourcegraph/jsonrpc2` 负责。

## 两处「没给」和「给了零值」必须分开

协议里有四个字段是可空的，不是值类型。折成值类型这四处都会静静地错：

| 字段 | 没给的意思 | 给了零值的意思 |
|---|---|---|
| 分叉点 | 从最后一个收了尾的回合分 | 从第 0 条分 |
| 翻页起点 | 从最新那头翻起 | 翻第 0 条之前，那是空的一页 |
| 推理档位 | 不选档位，随适配器的默认 | 明说一个空档位，解不开 |
| 标题 | 这条会话从没被命名过 | 有人把它改成了空标题 |

## 分叉与翻页为什么按下标算而不按序号算

DSH 的会话日志从 0 起、只增不删，所以它全程拿序号当下标使。这个仓库的会话日志有封顶，满了从最老那头弹（见[会话日志封顶](../session-log-limit.md)），弹过之后头一条的序号就不是 0 了：

```text
  DSH：          [0][1][2][3][4][5]...      序号 == 下标
  这个仓库：      [100][101][102]...        序号 = 下标 + 弹掉的那一截
                  ^
                  弹掉 0..99 之后的头一条
```

所以这两处都先取头一条的序号当基准再换算。直接拿序号当下标，弹掉多少条就翻空多少条。

## 生命周期与并发

- `NewLineTransport` 不拥有传入 Reader/Writer；`Close` 只关闭协议连接状态。
- `Server.Install` 注册运行时观察者，返回逆序清理函数。
- Server 只拥有自己按请求创建的 Agent，不关闭共享 Registry、LLM Runtime 或进程。
- 多会话请求可并发，单个 Agent 的回合仍由 Agent Loop 串行推进。

## 失败语义

- 未初始化、重复初始化、未知方法和非法参数返回 JSON-RPC 错误。
- 入队成功只表示消息已接受，不表示模型执行成功。
- 通知发送失败意味着连接不可用，不回滚已经写入的 Session 事实。
- 关闭过程聚合资源释放错误，避免因首个错误漏掉后续清理。

## 能力边界

- 这是运行时私有 SDK 协议，不是 ACP 或 MCP。
- 不提供 HTTP/WebSocket Server；宿主负责选择并接入 Reader/Writer。
- 不认证远端客户端，部署边界必须先建立可信通道。
- 不内置生产会话持久化后端，也不内置检索后端：`session/search` 没挂实现时直接报「检索没开」，不降级成全表扫。
- 不做增量推送。DSH 那侧给浏览器前端准备的流式 patch 与重放游标都不落地，同一语义走 [Session 投影](session.md) 的整值投影——重连就重取整值。
- 一次换模型只改这一条会话，不记成部署的默认：一条线上一个客户端换了模型，不该让别的每一条会话下次都跟着变。

## 对应的 DSH 能力

下表由 [`docs/packages.md`](../packages.md) 与 [能力覆盖表](../portmap/capability-coverage.tsv) 机器 join 得到：本篇覆盖的 Go 包，承接的是上游 DSH 的哪几条能力，以及各自还缺什么。落点列由源码里的 `// 源:` 注释反查，不是手写的。

| 上游能力 | DSH 包 | 裁决 | 落在哪个 Go 包 | 这里缺什么 |
|---|---|---|---|---|
| Host 的 ctx.sessionController 服务与生成的 Client session／skills／fileReferences namespace，负责会话生命周期与历史、模型目录、工作区路径打开、可调用 skill 发现与文件引用 | `api/session-controller` | 取形重写 | `protocol/sdk/sdkprotocol` `protocol/sdk/sdkserver` | 缺口：实时投影推送不做（同一语义走 sessionlog/projection 的整值投影，重连重取整值）；工作区路径打开、skill 发现、文件引用三个 namespace 各有自己的门面，不并进这条线 |
| SDK stdio 应用 profile 组合包，在 base 之上设 coding persona，命令提供方接受调用后才启动 JSON-RPC server | `bundle/sdk-app` | 取形重写 | `protocol/sdk/sdkserver` | 走 HTTP 不走 stdio JSON-RPC，要的只是「协议服务端／进程生命周期」的分界 |
| DeepSeek Harness SDK运行时共享协议格式，换行分帧JSON-RPC | `sdk/protocol` | 需要 | `protocol/sdk/sdkprotocol` | — |
| stdio JSON-RPC服务器插件使进程外SDK客户端驱动harness agent | `sdk/server` | 需要 | `protocol/sdk/sdkprotocol` `protocol/sdk/sdkserver` | — |

## 相关源码

- `protocol/sdk/sdkprotocol/`
- `protocol/sdk/sdkserver/`
