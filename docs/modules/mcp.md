# MCP 客户端

## 定位

`mcp` 把外部 Model Context Protocol 服务器的工具接入 `core/tools.Runtime`：建立连接、同步工具清单、转换输入输出、监听清单变化，并在断线后重连。

## 架构

```text
MCP Streamable HTTP Server
          |
          v
Host.Connect -> Connection
          |
          +-- tools/list 分页同步
          +-- tools/call 转发
          +-- list_changed 重新同步
          +-- 退避重连
          |
          v
core/tools.Runtime
```

每个远端工具的稳定身份是 `(serverName, rawName)`，模型看到的名称由 `PublicToolName` 生成：`mcp__<服务器名>__<原名>`。公开名只用于本地注册，线上调用仍使用服务器原名。

## 同步与调用

同步分成“取”和“换”两步：先抽干 `tools/list` 所有分页并在内存构建下一代定义；全部成功后再撤销旧代、注册新代。取清单失败时保留旧代，注册撞名时回滚本代，避免模型看到半套工具。

工具调用把 MCP 内容块转换为本运行时的文本、图片和资源链接。图片通过可选 `ImageAdmission` 进入附件仓库；不可接收时降级为诊断文本，程序化原始结果保持不变。

## 生命周期与并发

- `Host` 管理服务器命名空间占用，防止同名连接覆盖。
- `Host.Connect` 返回 `Connection`；`Connection.Close` 停止重连、关闭会话并撤销本连接的全部工具。
- 清单变化通知合并处理，避免短时间重复刷新产生并发代际。
- 自定义请求头通过 `http.RoundTripper` 注入，不能让远端响应改写后续请求头。

## 失败语义

- 初次连接失败直接返回；已连接后的断线按 `ReconnectPolicy` 退避重试。
- 未知 MCP 内容类型可能在 SDK 解码阶段使整次调用失败；已知但字段损坏的内容块降级为诊断。
- 远端 `isError` 作为工具错误结果返回，不升级为 Go 进程错误。
- 关闭超时会返回错误，但仍撤销本地注册，避免继续暴露失效工具。

## 能力边界

- 只支持 Streamable HTTP，不支持 stdio，也不启动子进程。
- 不实现 MCP Server。
- 不提供 OAuth 登录流程；请求头和凭据由宿主配置。
- 当前 SDK 无法暴露 task-support 元数据，因此不在注册前拒绝 task-only 工具。

## 相关源码

- `mcp/host.go`
- `mcp/connection.go`
- `mcp/bridge.go`
- `mcp/content.go`
- `mcp/naming.go`
