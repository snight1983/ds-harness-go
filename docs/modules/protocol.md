# 协议适配

本模块把运行时接到进程外客户端和远端工具服务。协议层只做编解码、生命周期桥接和内容准入，不拥有 Agent 核心运行时。

## 协议边界

```mermaid
flowchart LR
    SDKClient["外部 SDK"] <-->|"按行 JSON-RPC 2.0"| SDK["sdkprotocol / sdkserver"]
    ACPClient["ACP 客户端"] <-->|"ACP v1"| ACP["acp.Bridge"]
    SDK --> Agents["core/agent.Registry"]
    ACP --> Agents
    Agents --> Tools["core/tools"]
    Tools --> MCP["mcp Host / Connection"]
    MCP <-->|"Streamable HTTP"| MCPServer["远端 MCP Server"]
```

SDK 和 ACP 是入站 Agent 接口；MCP 是出站工具接口。MCP Server 不因此获得 Agent、会话或宿主服务的控制权。

## SDK JSON-RPC

`sdk/sdkprotocol` 定义按行分帧的 JSON-RPC 2.0 传输和稳定线上类型。畸形单行会被跳过，不会终止整个连接；并发写入保持每个消息独占一行。请求取消和连接关闭通过 `context.Context` 与传输 Done 信号传播。

`sdk/sdkserver` 处理三个请求：

| 请求 | 行为 |
|---|---|
| `initialize` | 保存该连接的工作目录、Provider、模型和输出上限 |
| `session/prompt` | 首次创建 Agent，把用户输入排入指定会话并返回消息 ID 回执 |
| `shutdown` | 停止接收新会话并释放该 Server 创建的 Agent |

服务端转发四类通知：会话事件、Agent 状态、子 Agent 开始和子 Agent 完成。Prompt 的后续执行结果通过通知流观察，不包含在入队回执中。它不创建 HTTP Listener，不决定进程退出，也不关闭宿主共享的 Runtime；输入输出流和进程模型由宿主提供。

同一会话的并发首次请求通过单飞创建，避免产生两个 Agent。关闭会等待正在创建的 Agent，并把已创建对象按所有权释放。

## ACP

`acp/acp` 实现 ACP 的 Agent 端，面向受信任的自动化客户端。它支持：

- 文本、资源链接和通过附件服务准入的内联图片提示。
- 已提交的助手文本和图片输出。
- 当前会话取消。
- 一次性工具审批决定。

原始流分块、推理内容、工具轨迹、计划、标题和重试标记不发送给 ACP 客户端。图片能力只有在模型路由和附件 Store 都确认可用时才对外声明；一批输入先全部校验，再写入任何图片。

Bridge 只拥有自己创建的 Agent。关闭时先停止接收请求、结算活动 Prompt、排空可继续子 Agent，再释放父 Agent。认证钩子本身不建立用户身份体系，宿主必须在连接进入 Bridge 前完成认证和租户绑定。

## MCP

`mcp` 把远端 MCP Server 的工具注册到 `core/tools`。公开名称按 `mcp__<server>__<tool>` 生成并稳定归一化；线上调用仍使用远端原名。

一次工具清单同步先完整获取下一代定义，再原子替换当前注册。拉取失败时保留上一代；注册冲突时回滚本代，模型不会看到半套工具。连接断开按配置退避重连，`tools/list_changed` 触发重新同步。

当前只支持 Streamable HTTP，不支持 stdio。仓库不启动 MCP 子进程。自定义请求头通过宿主提供的 HTTP Transport 注入，认证内容不能进入工具参数或模型历史。

远端内容经过类型检查和规范化后再变成工具结果。图片只有通过附件准入才能进入模型上下文；否则整批图片降级为诊断文本，原始程序化结果不被改写。

## 所有权与关闭顺序

推荐关闭顺序：

1. 协议入口停止接收新请求。
2. 取消或等待当前 Prompt 和工具调用。
3. 排空可继续子 Agent 与后台活动。
4. 释放协议层创建的 Agent 和 Observer。
5. 关闭 MCP Connection。
6. 由宿主最后关闭共享 Runtime、存储和网络 Listener。

重复关闭必须幂等。某一项释放失败时继续释放其他资源，最后汇总错误。

## 安全边界

- 协议中的会话 ID、工作目录、Provider 和模型名都属于不可信输入，必须映射到已授权资源。
- SDK Server 和 ACP Bridge 不提供完整认证、限流、配额或租户隔离。
- MCP Server 的工具 Schema 不是授权证明，工具运行时仍需 Guard、审批和宿主策略。
- 错误返回稳定协议错误，不把内部堆栈、凭据或后端连接信息发送给客户端。
- 协议层不能绕过 `core/agent` 直接改写会话当前状态。

## 能力边界

本模块不负责：

- 提供开箱即用的 HTTP/gRPC 服务进程。
- 管理 TLS 证书、OAuth、用户目录或 API Key 生命周期。
- 实现 MCP Server 或 stdio MCP 客户端。
- 向 ACP 暴露完整交互式界面事件。
- 保证跨连接、跨进程的会话唯一性；这需要宿主路由和存储约束。

## 相关源码

| 路径 | 内容 |
|---|---|
| `sdk/sdkprotocol/` | 线上类型、按行 JSON-RPC 传输和错误映射 |
| `sdk/sdkserver/` | SDK 请求处理、通知和 Agent 所有权 |
| `acp/acp/` | ACP Bridge、内容准入、取消和审批 |
| `mcp/host.go`、`mcp/connection.go` | MCP 连接、命名空间和重连 |
| `mcp/bridge.go`、`mcp/content.go` | 工具注册、调用和结果内容转换 |
