# ACP 接入

## 定位

`protocol/acp` 实现 Agent Client Protocol 的 Agent 端，把可信自动化客户端接到运行时。它负责会话创建、提示词提交、取消、授权决定和已提交助手输出的协议转换，不负责界面渲染，也不输出推理过程、工具轨迹或内部流片段。

## 架构

```text
ACP 客户端
    |
    | JSON-RPC / NDJSON，由 acp-go-sdk 编解码
    v
Bridge ---- Peer
  |          |
  |          +-- session/update 与授权请求
  +-- Agent Registry
  +-- Attachment Store
  +-- Model Resolver
  +-- Approval Registrar
```

线上类型和分帧由 `github.com/coder/acp-go-sdk` 提供，本模块只实现运行时语义。`New` 创建 `Bridge`，宿主创建协议连接后再调用 `Bridge.Install` 注入 `Peer`，以解决 Bridge 与连接互相依赖的问题。

## 核心能力

| 能力 | 行为 |
|---|---|
| `Initialize` | 返回协议能力和 Agent 信息 |
| `NewSession` | 创建本模块拥有的 Agent 与会话 |
| `Prompt` | 校验文本、资源链接和内联图片，排入 Agent 并等待本轮结束 |
| `Cancel` | 取消目标 Agent 当前活动 |
| 授权桥接 | 把一次性授权决定交给工具审批链 |
| 输出投影 | 只转发已提交的文本和图片助手输出 |

图片先经过模型能力判断和 `attachment` 准入；资源链接只传引用，不由 ACP 自行读取任意本地文件。`TurnEndToStopReason` 把运行时结束原因转换为 ACP 停止原因。

## 生命周期与并发

- `Bridge.Install` 注册监听器并返回幂等清理函数。
- Bridge 只拥有自己创建的 Agent；关闭时先停止接收新工作，再排空续行并释放这些 Agent。
- Bridge 不关闭宿主进程、底层传输或共享运行时，这些资源由创建它们的宿主管理。
- 多个请求可能并发进入，Agent 自身的串行回合和 Registry 生命周期规则仍然生效。

## 失败语义

- 配置缺失、未知会话、非法内容和不受支持的图片会返回明确协议错误。
- 一次提示词中的图片先全部校验，再写入附件存储，避免半批提交。
- 客户端断开不等于整个 Go 服务退出；宿主可以决定是否重建连接。
- 不支持的 ACP 会话管理扩展会返回未实现，而不是伪造成功。

## 能力边界

- 这是 ACP Agent 端，不是通用 ACP 客户端。
- 不负责认证体系；`Authenticate` 只表达当前部署支持的协议行为。
- 不负责 Session 的生产级持久化后端。
- 不发送内部思维、工具调用细节和原始流式片段。

## 相关源码

- `protocol/acp/config.go`
- `protocol/acp/bridge.go` —— 桥本身：攥着哪些会话、怎么装上去、握手与收摊
- `protocol/acp/bridgesession.go` —— 会话台账：开、恢复、列出、关掉、配置项
- `protocol/acp/bridgeprompt.go` —— 一次提示词从准入到结算的状态机
- `protocol/acp/bridgeevents.go` —— 运行时那几条边翻成线上的话
- `protocol/acp/content.go`
- `protocol/acp/codec.go`
- `protocol/acp/invariant.go`
