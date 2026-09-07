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
   +-- LLM Provider Mount
   +-- Subagent Runtime
```

协议请求包括 `initialize`、`session/prompt` 和 `shutdown`；通知包括 Session 事件、Agent 状态、子 Agent 启动和子 Agent 结束。`serverInfo.name` 是稳定线上值，不能作为普通展示字符串随意修改。

## 请求语义

- `initialize` 设置这条服务连接共用的工作目录、provider、model 和可选输出上限。
- `session/prompt` 找到或创建 Agent，把用户输入排队，并立即返回入队消息 ID。
- 实际执行结果不在 `session/prompt` 响应中等待，而是通过事件和状态通知流返回。
- `shutdown` 释放本 Server 创建的 Agent，但不退出宿主进程。

坏 JSON 行会被跳过，后续合法帧继续处理；请求 ID 配对、标准错误码和连接断开后的等待者释放由 `github.com/sourcegraph/jsonrpc2` 负责。

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
- 不内置生产会话持久化后端。

## 对应的 DSH 能力

下表由 [`docs/packages.md`](../packages.md) 与 [能力覆盖表](../portmap/capability-coverage.tsv) 机器 join 得到：本篇覆盖的 Go 包，承接的是上游 DSH 的哪几条能力，以及各自还缺什么。落点列由源码里的 `// 源:` 注释反查，不是手写的。

| 上游能力 | DSH 包 | 裁决 | 落在哪个 Go 包 | 这里缺什么 |
|---|---|---|---|---|
| Host 的 ctx.sessionController 服务与生成的 Client session／skills／fileReferences namespace，负责会话生命周期与历史、模型目录、工作区路径打开、可调用 skill 发现与文件引用 | `api/session-controller` | 抄形状 | `protocol/sdk/sdkprotocol` | 缺口：sdkprotocol 只有 session/prompt。create／resume／fork／rename／list／search／cancel／select-model／update-queue 与历史分页、实时投影推送都没有 |
| SDK stdio 应用 profile 组合包，在 base 之上设 coding persona，命令提供方接受调用后才启动 JSON-RPC server | `bundle/sdk-app` | 抄形状 | `protocol/sdk/sdkserver` | 走 HTTP 不走 stdio JSON-RPC，要的只是「协议服务端／进程生命周期」的分界 |
| DeepSeek Harness SDK运行时共享协议格式，换行分帧JSON-RPC | `sdk/protocol` | 需要 | `protocol/sdk/sdkprotocol` | — |
| stdio JSON-RPC服务器插件使进程外SDK客户端驱动harness agent | `sdk/server` | 需要 | `protocol/sdk/sdkprotocol` `protocol/sdk/sdkserver` | — |

## 相关源码

- `protocol/sdk/sdkprotocol/`
- `protocol/sdk/sdkserver/`
