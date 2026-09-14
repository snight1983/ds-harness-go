# MCP 协议规范中文版

版本：`2026-07-28`

本篇对应 MCP 官方稳定规范 `2026-07-28`，覆盖基础协议、版本协商、传输、消息模式、鉴权、服务端能力、客户端能力、通用能力、错误和废弃项。协议字段名保留英文，因为线上 JSON 必须使用这些原名。

官方规范：<https://modelcontextprotocol.io/specification/2026-07-28>

官方机器可读 Schema：<https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/schema/2026-07-28/schema.json>

本文使用以下强度：

| 中文 | 官方用词 | 含义 |
|---|---|---|
| 必须 | MUST / REQUIRED | 不满足就不符合协议 |
| 禁止 | MUST NOT | 协议明确不允许 |
| 应该 | SHOULD | 通常要做，只有充分理由才能偏离 |
| 不应该 | SHOULD NOT | 通常不能做，只有充分理由才能偏离 |
| 可以 | MAY / OPTIONAL | 可选能力 |

## 1. MCP 是什么

MCP 是 LLM 应用与外部数据和能力之间的标准协议。它规定通信格式和双方行为，不规定业务逻辑如何实现。

MCP 建立在 JSON-RPC 2.0 上，包含三个角色：

| 角色 | 职责 |
|---|---|
| Host | LLM 应用；创建 Client、执行权限和用户授权、汇总上下文、连接模型 |
| Client | Host 内的连接器；一个 Client 只连接一个 MCP 对端 |
| Server | 公布 Resources、Prompts、Tools，并处理调用 |

一个 Host 可以创建多个 Client。同一份 Client 代码可以创建多条连接，每条连接仍然只对应一个对端。

核心设计原则：

1. Server 应该容易实现，只关注自己的能力。
2. 多个 Server 可以组合使用。
3. Server 不能看到完整对话，也不能看到其他 Server；Host 控制隔离边界。
4. 能力按需声明，双方可以独立升级。
5. `2026-07-28` 是无状态协议。每次请求都必须自带版本和客户端能力，不能依赖之前的连接或请求。

## 2. 能力方向

Server 可以向 Client 提供：

| 能力 | 控制者 | 作用 |
|---|---|---|
| Tools | 模型 | 可执行函数，例如查询、写文件、调用 API |
| Resources | 应用 | 可读取的上下文，例如文件、文档、数据库内容 |
| Prompts | 用户 | 用户主动选择的提示词模板或工作流入口 |
| Completions | 应用 | 为 Prompt 参数和 Resource URI 模板补全参数 |
| Logging | 应用 | 请求范围内的结构化日志；本版已废弃 |

Client 可以向 Server 提供：

| 能力 | 作用 | 状态 |
|---|---|---|
| Elicitation | 让 Server 请求用户填写表单或打开 URL | 有效 |
| Sampling | 让 Server 请求 Host 调用模型 | 已废弃，仍可兼容 |
| Roots | 提供相关文件或目录列表 | 已废弃，仍可兼容 |

只有双方声明支持的能力才能使用。Server 不能要求 Client 提供未声明的能力。

## 3. JSON-RPC 基础消息

所有 MCP 消息必须符合 JSON-RPC 2.0。

### 3.1 请求

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/list",
  "params": {}
}
```

规则：

- `jsonrpc` 必须是 `"2.0"`。
- `id` 必须是字符串或整数，禁止为 `null`。
- 尚未完成的请求之间，`id` 必须唯一。
- `method` 是协议方法名。
- `params` 可选；存在时是参数对象。
- `2026-07-28` 的请求必须在 `params._meta` 中携带版本和客户端能力。

### 3.2 成功响应

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "resultType": "complete"
  }
}
```

规则：

- `id` 必须与请求一致。
- `result` 必须存在。
- 所有结果必须有 `resultType`。
- 普通完成结果使用 `"complete"`。
- 需要额外输入的中间结果使用 `"input_required"`。
- Client 收到旧版本中缺少 `resultType` 的响应时，必须按 `"complete"` 处理。

### 3.3 错误响应

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "error": {
    "code": -32602,
    "message": "Invalid params",
    "data": {}
  }
}
```

`error` 必须包含整数 `code` 和字符串 `message`，`data` 可选。成功响应和错误响应不能同时出现。

标准 JSON-RPC 错误：

| code | 含义 |
|---:|---|
| `-32700` | Parse error，JSON 无法解析 |
| `-32600` | Invalid Request，请求结构非法 |
| `-32601` | Method not found，方法不存在 |
| `-32602` | Invalid params，参数非法 |
| `-32603` | Internal error，内部错误 |

MCP 本版错误：

| code | 名称 | 含义 |
|---:|---|---|
| `-32020` | `HeaderMismatch` | HTTP 头缺失、非法或与 JSON 正文不一致 |
| `-32021` | `MissingRequiredClientCapability` | 请求缺少处理所需的 Client 能力 |
| `-32022` | `UnsupportedProtocolVersion` | 请求的协议版本不受支持 |

`-32000` 至 `-32019` 留给实现自定义；`-32020` 至 `-32099` 由 MCP 规范保留。旧版资源不存在错误 `-32002` 不得由本版产生，但 Client 应兼容接收。

### 3.4 通知

```json
{
  "jsonrpc": "2.0",
  "method": "notifications/progress",
  "params": {}
}
```

通知没有 `id`，接收方禁止回复。

本版所有交互都由 Client 发起：Client 发送请求和通知，Server 返回响应，并可在请求的响应流中发送相关通知。Server 禁止主动发送 JSON-RPC 请求。

## 4. 每个请求必须携带的 `_meta`

现代 MCP 请求的 `params._meta` 形状：

```json
{
  "_meta": {
    "io.modelcontextprotocol/protocolVersion": "2026-07-28",
    "io.modelcontextprotocol/clientInfo": {
      "name": "ExampleClient",
      "version": "1.0.0"
    },
    "io.modelcontextprotocol/clientCapabilities": {
      "elicitation": {
        "form": {},
        "url": {}
      }
    }
  }
}
```

| Key | 必需 | 含义 |
|---|---|---|
| `io.modelcontextprotocol/protocolVersion` | 是 | 本请求使用的协议版本 |
| `io.modelcontextprotocol/clientCapabilities` | 是 | 本请求允许使用的 Client 能力 |
| `io.modelcontextprotocol/clientInfo` | 否，但应该提供 | Client 名称与版本 |
| `io.modelcontextprotocol/logLevel` | 否 | 本请求希望接收的最低日志级别 |
| `progressToken` | 否 | 订阅本请求的进度通知 |
| `traceparent`、`tracestate`、`baggage` | 否 | OpenTelemetry/W3C 追踪上下文 |

缺少必需字段时，Server 必须返回 `-32602`；HTTP 状态必须为 `400`。

Server 应该在每个成功结果的 `_meta.io.modelcontextprotocol/serverInfo` 中提供名称和版本。`clientInfo` 与 `serverInfo` 都是对方自报信息，只能用于展示、日志和诊断，不能用于安全决策。

`_meta` 自定义 Key 应使用反向域名前缀，例如 `com.example/foo`。第二段为 `modelcontextprotocol` 或 `mcp` 的前缀由 MCP 保留。

## 5. 无状态要求

`2026-07-28` 不存在协议级 Session，也没有 `initialize` 握手。

- 每次请求必须自带协议版本和 Client 能力。
- Server 禁止从同一连接的前序请求推断身份、版本、能力或上下文。
- 一个 stdio 进程或 HTTP 连接不代表一次对话。
- 同一个连接可以交错承载不同任务、线程和对话。
- 跨请求状态必须用显式 ID 或句柄传递。
- Client 不应该把一次对话当作 stdio 子进程的生命周期。
- HTTP 不再使用 `Mcp-Session-Id`。

## 6. 版本与扩展协商

### 6.1 `server/discover`

Server 必须实现 `server/discover`。Client 可以在其他请求前调用，也可以直接调用业务方法并在版本错误后重试。

```json
{
  "jsonrpc": "2.0",
  "id": "discover-1",
  "method": "server/discover",
  "params": {
    "_meta": {
      "io.modelcontextprotocol/protocolVersion": "2026-07-28",
      "io.modelcontextprotocol/clientCapabilities": {}
    }
  }
}
```

```json
{
  "jsonrpc": "2.0",
  "id": "discover-1",
  "result": {
    "resultType": "complete",
    "supportedVersions": ["2026-07-28"],
    "capabilities": {
      "tools": {},
      "resources": {}
    },
    "instructions": "可选的使用说明",
    "ttlMs": 3600000,
    "cacheScope": "public"
  }
}
```

版本不支持时，Server 必须返回 `-32022`，并在 `data.supported` 中列出支持版本，在 `data.requested` 中返回请求版本。Client 应选择共同支持的版本重试。

### 6.2 扩展

额外能力通过双方 `capabilities.extensions` 协商。扩展 ID 必须使用带前缀的 `_meta` 命名规则，例如 `io.modelcontextprotocol/tasks`。只有一方支持时，必须退回核心协议行为或明确拒绝请求。

### 6.3 兼容旧版

`2025-11-25` 及更早版本是旧式协议，通过 `initialize` 和 `notifications/initialized` 建立 Session。兼容新旧两套的实现称为 dual-era。

- stdio：先用现代 `_meta` 调 `server/discover`。收到 `DiscoverResult` 或现代标准错误时继续现代协议；收到其他错误或超时才回退到 `initialize`。
- HTTP：先发一次现代请求。`400` 正文是现代标准错误时修正现代请求；正文为空或不是现代错误时才回退到 `initialize`。
- 识别结果属于整个进程或 HTTP origin，Client 应缓存。

## 7. 传输层

MCP 定义 stdio 和 Streamable HTTP。自定义传输可以存在，但必须保留 JSON-RPC 消息和协议语义，并记录连接与鉴权方式。

### 7.1 stdio

Client 启动一个子进程：

- Client 向子进程 `stdin` 写 JSON-RPC。
- Server 从 `stdin` 读，从 `stdout` 写 JSON-RPC。
- 一行一条消息，消息内部禁止换行。
- `stdout` 只能输出 MCP 消息；日志写 `stderr`。
- Client 可以捕获、转发或忽略 `stderr`，不能仅凭 `stderr` 判断请求失败。
- 所有响应和通知共用一条输出流，依靠请求 `id`、`progressToken` 和 `subscriptionId` 关联。
- 取消请求时发送 `notifications/cancelled`。
- 关闭时 Client 先关子进程输入，等待退出，超时后再强制终止。
- 子进程异常退出后可以重启；进行中的请求丢失，订阅必须重新建立。
- stdio 凭据应从受控环境变量或进程配置取得，不使用 HTTP OAuth 流程。

### 7.2 Streamable HTTP

Server 提供一个接受 POST 的 MCP URL，例如 `https://example.com/mcp`。

每条 JSON-RPC 请求使用一次独立 POST：

- `Accept` 必须同时包含 `application/json` 和 `text/event-stream`。
- 正文只能有一条 JSON-RPC 请求或通知，不能批量发送。
- 请求的普通响应可以是一个 `application/json` JSON 对象。
- 需要进度、日志或流式通知时，可以返回 `text/event-stream`；最终 JSON-RPC 响应通常结束 SSE 流。
- SSE 中只能出现与本请求有关的通知和最终响应，禁止出现 Server 主动请求。
- 长期变化通知通过 `subscriptions/listen` 请求的 SSE 响应流传送。
- SSE 断线不可通过 `Last-Event-ID` 恢复。进行中的请求丢失后，Client 必须使用新 `id` 重新请求。
- 取消请求就是关闭该请求的 SSE 响应流。
- Server 应发送 `X-Accel-Buffering: no`，并可用 SSE 注释行保活。

安全要求：

- Server 必须校验 `Origin`；存在但不可信时返回 `403`。
- 本地服务应该只绑定 `127.0.0.1`，不应默认绑定 `0.0.0.0`。
- 应对所有连接执行鉴权。

### 7.3 HTTP 必需头

每个 POST 必须携带：

| Header | 规则 |
|---|---|
| `Content-Type` | `application/json` |
| `Accept` | 同时支持 `application/json, text/event-stream` |
| `MCP-Protocol-Version` | 与正文 `_meta` 的版本完全一致 |
| `Mcp-Method` | 与正文 `method` 完全一致 |
| `Mcp-Name` | `tools/call`、`prompts/get` 使用 `params.name`；`resources/read` 使用 `params.uri` |
| `Authorization` | 启用 OAuth 时使用 `Bearer <token>` |

Header 名不区分大小写，值区分大小写。Server 必须比较头与正文；缺失、不合法或不一致时返回 HTTP `400` 和 MCP `-32020`。

不能安全放入 HTTP 头的非 ASCII 值、控制字符、首尾空白，以及本身符合哨兵形式的值，必须编码成：

```text
=?base64?{UTF-8 字节的 Base64}?=
```

### 7.4 `x-mcp-header`

Tool 的 `inputSchema` 可以在字符串、整数或布尔属性上写 `x-mcp-header`，要求 Client 把该参数同步到 `Mcp-Param-{Name}` HTTP 头。

```json
{
  "type": "object",
  "properties": {
    "region": {
      "type": "string",
      "x-mcp-header": "Region"
    }
  }
}
```

传入 `region: "us-west1"` 时必须产生 `Mcp-Param-Region: us-west1`。

约束：名称不能为空，必须是合法 HTTP token，禁止控制字符，Schema 内大小写不敏感地唯一；只能标记可从根对象沿 `properties` 静态到达的 primitive 属性，禁止穿过数组、组合关键字或 `$ref`。`number` 禁止，`integer` 必须在 JavaScript 安全整数范围。HTTP Client 必须排除定义非法的 Tool，并应该记录原因。敏感参数不应该映射到 Header。

## 8. 三种消息模式

### 8.1 请求—响应

Client 发请求，Server 返回结果或错误。响应前可以发送属于该请求的 `notifications/progress` 或 `notifications/message`。

### 8.2 多轮往返请求 MRTR

当处理 `tools/call`、`resources/read` 或 `prompts/get` 时还需要用户输入、模型生成或 Roots，Server 返回：

```json
{
  "resultType": "input_required",
  "inputRequests": {
    "login": {
      "method": "elicitation/create",
      "params": {
        "mode": "form",
        "message": "请输入用户名",
        "requestedSchema": {
          "type": "object",
          "properties": { "name": { "type": "string" } },
          "required": ["name"]
        }
      }
    }
  },
  "requestState": "opaque-value"
}
```

Client 收集输入后，用新的 JSON-RPC `id` 重发原始方法和原始参数，同时添加：

```json
{
  "inputResponses": {
    "login": {
      "action": "accept",
      "content": { "name": "octocat" }
    }
  },
  "requestState": "opaque-value"
}
```

规则：

- `inputRequests` 的 Key 由 Server 指定，在本次响应内必须唯一。
- Value 只能是 `elicitation/create`、`sampling/createMessage` 或 `roots/list`。
- Server 只能请求 Client 已声明的能力。
- 每个 `InputRequiredResult` 至少有 `inputRequests` 或 `requestState` 之一。
- `requestState` 是 Server 的不透明字符串；Client 必须原样回传，禁止解析或修改。
- Server 必须把回传的 `requestState` 当作攻击者输入。影响权限或业务时必须使用 HMAC/AEAD 等完整性保护，并应绑定用户、原请求和短 TTL。
- 初次请求与重试是两次独立请求，必须使用不同 `id`。
- Client 不保证会提供输入或重试。

### 8.3 订阅—通知

Client 使用 `subscriptions/listen` 打开长期通知流：

```json
{
  "method": "subscriptions/listen",
  "params": {
    "notifications": {
      "toolsListChanged": true,
      "promptsListChanged": true,
      "resourcesListChanged": true,
      "resourceSubscriptions": ["file:///project/config.json"]
    }
  }
}
```

Server 必须先发送 `notifications/subscriptions/acknowledged`，说明接受了哪些过滤条件。此后的每条通知都必须在 `_meta.io.modelcontextprotocol/subscriptionId` 中携带原 `subscriptions/listen` 的请求 `id`。

Server 禁止发送 Client 未订阅的通知类型。可以同时存在多个订阅。断线后 Client 必须重新订阅。Server 主动正常关闭时应该先返回该长期请求的 `resultType: "complete"`；直接断流表示异常关闭。

## 9. 取消与超时

- 所有请求都应该有可配置超时。
- 收到进度后可以延长普通超时，但仍应该有绝对最大超时。
- stdio 取消使用 `notifications/cancelled`，参数包含 `requestId` 和可选 `reason`。
- HTTP 取消通过关闭对应 SSE 响应流表达。
- Server 应尽快停止、释放资源，并禁止再为该请求发消息。
- 已完成、未知或无法取消的请求可以忽略取消通知。
- Client 应忽略取消之后迟到的响应。
- Server 只能主动取消 `subscriptions/listen`，不能用取消通知结束其他请求。

## 10. 进度

Client 想接收进度时，在请求 `_meta.progressToken` 中提供字符串或整数。活动请求之间 Token 必须唯一。

```json
{
  "method": "notifications/progress",
  "params": {
    "progressToken": "job-1",
    "progress": 50,
    "total": 100,
    "message": "处理中"
  }
}
```

Server 可以不发进度。发送时 `progress` 必须单调增加，`total` 可省略，二者可以是浮点数。请求结束后必须停止发送。双方应该限流，防止通知洪泛。

## 11. Tools

Server 声明：

```json
{
  "capabilities": {
    "tools": { "listChanged": true }
  }
}
```

### 11.1 列表

`tools/list` 获取 Tool，可带不透明 `cursor`。结果包含 `tools`、可选 `nextCursor`，以及必需的缓存字段 `ttlMs`、`cacheScope`。

Tool 定义字段：

| 字段 | 必需 | 含义 |
|---|---|---|
| `name` | 是 | Tool 唯一名称 |
| `title` | 否 | 给人看的标题 |
| `description` | 否 | 能力说明，模型通常据此决定是否调用 |
| `inputSchema` | 是 | 参数 JSON Schema |
| `outputSchema` | 否 | `structuredContent` 的 JSON Schema |
| `annotations` | 否 | 行为提示，来自不可信来源时不能当安全依据 |
| `icons` | 否 | UI 图标 |

Tool 名应该为 1 至 128 个字符、区分大小写，只使用 ASCII 字母、数字、下划线、连字符和点，并在同一来源内唯一。聚合多个来源的 Client 必须自行处理重名。

无参数 Tool 推荐使用：

```json
{ "type": "object", "additionalProperties": false }
```

### 11.2 调用

```json
{
  "method": "tools/call",
  "params": {
    "name": "get_weather",
    "arguments": { "location": "Shanghai" }
  }
}
```

完成结果：

```json
{
  "resultType": "complete",
  "content": [
    { "type": "text", "text": "晴，28°C" }
  ],
  "structuredContent": {
    "condition": "sunny",
    "temperature": 28
  },
  "isError": false
}
```

`content` 可以包含：

- `text`：文本。
- `image`：Base64 数据与 `mimeType`。
- `audio`：Base64 数据与 `mimeType`。
- `resource_link`：URI、名称及可选说明。
- `resource`：内嵌的文本或二进制 Resource。

`structuredContent` 可以是任意 JSON 值。有 `outputSchema` 时 Server 必须保证它通过校验，Client 应该再次校验。为兼容旧 Client，返回结构化结果时还应该在 `TextContent` 中带一份序列化 JSON。

业务执行失败使用 `isError: true` 的 Tool Result，使模型能够看到并修正；协议错误、参数结构错误和方法不存在使用 JSON-RPC Error。

Tool 集合必须由当前请求的鉴权决定，不能因同一连接上之前的调用而变化。列表应该稳定排序，以利缓存和模型 Prompt Cache。

列表变化时，通过已订阅的流发送 `notifications/tools/list_changed`，Client 随后重新调用 `tools/list`。

## 12. Resources

Server 声明：

```json
{
  "capabilities": {
    "resources": {
      "subscribe": true,
      "listChanged": true
    }
  }
}
```

方法：

| 方法 | 作用 |
|---|---|
| `resources/list` | 分页列出 Resource |
| `resources/read` | 按 URI 读取内容 |
| `resources/templates/list` | 分页列出 RFC 6570 URI 模板 |

Resource 定义包含 `uri`、`name`，以及可选 `title`、`description`、`mimeType`、`size`、`icons`、`annotations`。

内容有两种：

```json
{ "uri": "file:///a.txt", "mimeType": "text/plain", "text": "内容" }
```

```json
{ "uri": "file:///a.png", "mimeType": "image/png", "blob": "BASE64" }
```

一次 `resources/read` 可以返回多个内容。不存在的 Resource 必须返回 `-32602`，禁止用空 `contents` 冒充不存在；Client 应兼容旧码 `-32002`。

通用 URI 包括 `https://`、`file://`、`git://`，自定义 Scheme 必须符合 RFC 3986。`https://` 只应该用于 Client 能直接抓取的公开网络内容；否则应通过 `resources/read` 或自定义 Scheme。

`annotations` 可包含：

- `audience`: `user`、`assistant`。
- `priority`: `0.0` 至 `1.0`。
- `lastModified`: ISO 8601 时间。

Resource URI 必须校验，文件路径必须防目录穿越，读取前必须检查权限。二进制必须正确编码。

列表变化发送 `notifications/resources/list_changed`。具体 URI 更新通过订阅过滤器注册，随后发送 `notifications/resources/updated`；Client 再次读取获取新内容。

## 13. Prompts

Server 声明：

```json
{
  "capabilities": {
    "prompts": { "listChanged": true }
  }
}
```

`prompts/list` 分页列出 Prompt。每项包括 `name`，以及可选 `title`、`description`、`arguments`、`icons`。参数项包含 `name`、可选 `description` 和 `required`。

`prompts/get` 传入 `name` 和字符串参数 Map，返回可选说明与 `messages`。消息角色只能是 `user` 或 `assistant`，内容可以是文本、图片、音频、Resource Link 或内嵌 Resource。

Prompt 输入输出必须校验，防止注入和越权读取。列表变化通过 `notifications/prompts/list_changed` 通知。

## 14. Completion

Server 声明 `capabilities.completions` 后，Client 可调用 `completion/complete`，为 Prompt 参数或 Resource URI 模板参数请求补全。

```json
{
  "method": "completion/complete",
  "params": {
    "ref": { "type": "ref/prompt", "name": "code_review" },
    "argument": { "name": "language", "value": "py" },
    "context": {
      "arguments": { "framework": "django" }
    }
  }
}
```

引用类型只有 `ref/prompt` 和 `ref/resource`。结果的 `completion.values` 最多 100 条，并可带 `total` 与 `hasMore`。Server 应按相关度排序、校验输入、限流和保护敏感建议；Client 应防抖。

## 15. Elicitation

Client 在每次请求声明：

```json
{
  "elicitation": {
    "form": {},
    "url": {}
  }
}
```

Server 只能在 MRTR 的 `InputRequiredResult.inputRequests` 中提出 `elicitation/create`。

### 15.1 Form 模式

```json
{
  "method": "elicitation/create",
  "params": {
    "mode": "form",
    "message": "请填写联系信息",
    "requestedSchema": {
      "type": "object",
      "properties": {
        "email": { "type": "string", "format": "email" }
      },
      "required": ["email"]
    }
  }
}
```

Schema 只允许平坦对象中的 primitive 字段：字符串、数字、整数、布尔、单选枚举，以及 primitive 枚举数组。支持字符串长度和 `email`、`uri`、`date`、`date-time` 格式；支持数值上下界、数组项数和默认值。禁止嵌套对象和对象数组。

Form 模式禁止索要密码、API Key、Access Token、支付凭据等秘密。

### 15.2 URL 模式

```json
{
  "method": "elicitation/create",
  "params": {
    "mode": "url",
    "url": "https://example.com/secure-flow",
    "message": "请完成授权"
  }
}
```

秘密、支付和第三方授权必须使用 URL 模式，使数据不经过 Client。Client 必须展示目标域名并获得用户同意后才能打开。URL 模式不用于 MCP Client 自身连接 MCP 的 OAuth 鉴权。

用户结果有三种：`accept`、`decline`、`cancel`。Form 的 `accept` 带通过 Schema 的 `content`；URL 的 `accept` 不带敏感结果，只表示用户同意打开，外部流程是否完成由 Server 在重试时判断。

## 16. Sampling（已废弃）

Sampling 允许 Server 通过 MRTR 的 `sampling/createMessage` 请求 Host 调用模型。新实现不应该增加此能力，应直接使用模型供应商 API。

请求可包含：`messages`、`modelPreferences`、`systemPrompt`、`maxTokens`、`temperature`、`stopSequences`、`metadata`、`tools`、`toolChoice`。模型偏好包含 `hints`、`costPriority`、`speedPriority`、`intelligencePriority`。

`toolChoice.mode`：

- `auto`：模型决定是否调用 Tool。
- `required`：必须至少调用一个 Tool。
- `none`：禁止调用 Tool。

Sampling 消息角色只有 `user` 与 `assistant`。Assistant 的 `tool_use` 必须由下一条只包含 `tool_result` 的 User 消息完整配对；同一条 Tool Result 消息禁止混入文本、图片或其他内容。并行 Tool Use 必须逐个匹配 `id` 与 `toolUseId`。

Client 必须保留模型选择权、用户审批权，并校验消息、Tool 和结果。`includeContext` 的 `thisServer` 与 `allServers` 已废弃。

## 17. Roots（已废弃）

Roots 是 Client 提供给 Server 的相关目录或文件提示，不是访问控制。新实现应该改用 Tool 参数、Resource URI 或 Server 配置。

Server 通过 MRTR 请求 `roots/list`；Client 返回：

```json
{
  "roots": [
    {
      "uri": "file:///home/user/project",
      "name": "Project"
    }
  ]
}
```

当前 Root URI 必须是 `file://`。Client 必须征得用户同意并校验 URI 和权限；Server 仍需自己执行真正的路径与权限限制。

## 18. 缓存

以下 `resultType: "complete"` 的结果必须携带 `ttlMs` 与 `cacheScope`：

- `server/discover`
- `tools/list`
- `prompts/list`
- `resources/list`
- `resources/templates/list`
- `resources/read`

`ttlMs` 是毫秒 freshness 提示，必须大于等于 0；`0` 表示立即过期。Client 在需要使用数据时检查是否过期，不应把 TTL 自动当轮询间隔。

`cacheScope`：

- `public`：可以跨调用者共享，结果不得包含用户专属数据。
- `private`：只能在同一鉴权上下文中复用，Token 或用户变化必须使用独立缓存。

缓存 Key 必须包含 method 与所有影响结果的 params。包含 `inputResponses` 或 `requestState` 的 MRTR 重试结果禁止缓存。通知会立即使对应缓存失效。

分页的每页独立缓存；同一列表各页的 `cacheScope` 必须相同。分页变化不保证跨页快照一致，需要一致快照时从第一页重新读取。

## 19. 分页

支持分页的方法：`tools/list`、`prompts/list`、`resources/list`、`resources/templates/list`。

Server 决定页大小。响应有更多数据时返回 `nextCursor`，Client 把它原样作为下一次请求的 `cursor`。Cursor 是不透明字符串，禁止解析、修改或根据内容判断；空字符串也是有效 Cursor。没有 `nextCursor` 才表示结束。非法 Cursor 应返回 `-32602`。

## 20. Logging（已废弃）

Server 声明 `capabilities.logging`。Client 只有在请求 `_meta.io.modelcontextprotocol/logLevel` 中指定最低级别时，Server 才可以在该请求的响应流发送 `notifications/message`。

级别从低到高：`debug`、`info`、`notice`、`warning`、`error`、`critical`、`alert`、`emergency`。

日志只能属于当前请求，禁止放进订阅流；禁止包含凭据、秘密和个人身份信息。Server 必须限流。新实现应使用 stdio 的 `stderr` 或 OpenTelemetry。

## 21. 图标与内容安全

`Implementation`、Tool、Prompt 和 Resource 都可带 `icons`。图标包含 `src`，以及可选 `mimeType`、`sizes`、`theme`。

支持图标渲染的 Client 必须至少支持 PNG、JPEG，应该支持 SVG 和 WebP。必须只接受安全的 HTTPS 或 `data:` URI，禁止危险 Scheme 和跨来源重定向；抓取时不能携带 Cookie、Authorization 或 Client 凭据；必须限制大小、尺寸、帧数，校验 magic bytes 与 MIME，并对 SVG 做隔离或清洗。

## 22. JSON Schema 规则

- 未写 `$schema` 时默认 JSON Schema 2020-12。
- 双方必须支持 2020-12，可以支持其他 Dialect。
- 写了 `$schema` 时必须按对应 Dialect 校验；不支持时必须明确报错。
- Schema 本身必须合法。
- 禁止默认联网解析 `$ref`。可选联网模式必须显式开启，并限制域名、内网地址、超时和大小。
- 无法解析外部 `$ref` 时应该拒绝 Schema，不能悄悄按宽松规则接受。
- `anyOf`、`oneOf`、`allOf`、条件和 `$defs` 可能消耗大量资源，实现必须限制深度、子 Schema 数量或校验时间。

## 23. HTTP OAuth 鉴权

鉴权是可选能力。启用 HTTP 鉴权时应遵循 OAuth 2.1；stdio 不走此流程。

角色：MCP Client 是 OAuth Client；受保护的 MCP 入口是 Resource Server；Authorization Server 负责登录、授权和签发 Token。

### 23.1 发现

1. 未带 Token 的请求收到 `401 Unauthorized` 和 `WWW-Authenticate: Bearer`。
2. Client 从 `resource_metadata` 找到 RFC 9728 Protected Resource Metadata。
3. Metadata 给出 Authorization Server 地址。
4. Client 必须同时支持 RFC 8414 OAuth Metadata 和 OpenID Connect Discovery。

### 23.2 Client 注册优先级

1. 已有预注册 Client ID 时优先使用。
2. Authorization Server 支持时，使用 Client ID Metadata Document：Client ID 本身是 HTTPS JSON 文档 URL。
3. 仅为兼容旧实现，可以使用已废弃的 RFC 7591 Dynamic Client Registration。
4. 都不可用时，让用户配置 Client 信息。

Client ID Metadata Document 必须使用带路径的 HTTPS URL，文档必须含 `client_id`、`client_name`、`redirect_uris`，且 `client_id` 必须与文档 URL 完全相同。Authorization Server 必须校验文档、精确匹配 Redirect URI，并防 SSRF。

### 23.3 授权码流程

- 必须使用 Authorization Code + PKCE。
- 必须验证 Metadata 中支持 PKCE；缺少 `code_challenge_methods_supported` 时拒绝继续。
- 技术上可行时必须使用 `S256`。
- Authorization 与 Token 请求都必须带 RFC 8707 `resource`，绑定目标 MCP URI。
- 打开浏览器前必须记录已验证的 `issuer`。
- 返回存在 `iss` 时必须与记录值逐字比较；声明支持 `iss` 却缺失时必须拒绝。
- Redirect URI 必须精确匹配预注册值；Client 应生成并校验 `state`。

### 23.4 Token 使用

每个 HTTP 请求都使用：

```http
Authorization: Bearer <access-token>
```

禁止把 Token 放入 URL Query。Server 必须验证签名、有效期、Scope 和 Audience，并确保 Token 专门签发给自己。Server 调用上游 API 时必须获取上游自己的 Token，禁止透传 Client 给 MCP 的 Token。

无 Token 或 Token 无效返回 `401`；权限或 Scope 不足返回 `403`。Scope 以当前 `WWW-Authenticate` Challenge 为准，Client 应在保留原有权限的同时执行增量授权。刷新 Token 必须安全存储；公共 Client 的 Refresh Token 必须轮换。

凭据必须按 Authorization Server 的 `issuer` 隔离，禁止把一个 Authorization Server 签发或注册的凭据用于另一个。

## 24. 安全要求

- Tool 调用可能执行任意代码，Host 应让用户看到可用 Tool、调用行为和审批入口。
- Host 必须在向外提供用户数据前取得同意。
- Tool 描述与 annotations 是不可信输入，不能据此授予权限。
- 所有输入、URI、Schema、Header 和内容块都必须校验并限制大小。
- OAuth Token 必须绑定 Audience，安全存储，禁止记录或透传。
- 必须防止 DNS Rebinding、SSRF、目录穿越、Header 注入、开放重定向、OAuth Mix-Up 和 Confused Deputy。
- Server 从 Client 收回的 `requestState` 必须按攻击者可修改的数据处理。
- 订阅、进度、日志和补全都必须限流。
- 模型选择调用 Tool 不等于获得执行权限；Host 仍然负责权限和用户同意。

## 25. 废弃与移除

本版已废弃：

- Roots。
- Sampling。
- Logging。
- `includeContext: "thisServer"` 与 `"allServers"`。
- 旧 HTTP+SSE 传输。
- OAuth Dynamic Client Registration，保留作兼容回退。

本版已移除：

- `initialize` 与 `notifications/initialized`。
- 协议级 Session 与 `Mcp-Session-Id`。
- 独立 HTTP GET SSE 通道。
- SSE `Last-Event-ID` 恢复和重放。
- `ping`。
- `logging/setLevel`。
- `notifications/roots/list_changed`。
- `resources/subscribe` 与 `resources/unsubscribe`。
- Server 主动发送 `roots/list`、`sampling/createMessage`、`elicitation/create` 请求的旧模式，改为 MRTR。
- 核心 Tasks，移至 `io.modelcontextprotocol/tasks` 扩展。

## 26. 完整 RPC 与通知索引

| 方法 | 方向 | 所属能力 | 说明 |
|---|---|---|---|
| `server/discover` | Client → Server | Base | 查询版本、能力和身份；Server 必须实现 |
| `tools/list` | Client → Server | Tools | 列出 Tool |
| `tools/call` | Client → Server | Tools | 调用 Tool |
| `resources/list` | Client → Server | Resources | 列出 Resource |
| `resources/read` | Client → Server | Resources | 读取 Resource |
| `resources/templates/list` | Client → Server | Resources | 列出 URI 模板 |
| `prompts/list` | Client → Server | Prompts | 列出 Prompt |
| `prompts/get` | Client → Server | Prompts | 获取 Prompt 消息 |
| `completion/complete` | Client → Server | Completion | 参数补全 |
| `subscriptions/listen` | Client → Server | Subscription | 打开长期通知流 |
| `elicitation/create` | MRTR 内嵌 | Elicitation | 请求用户输入或打开 URL |
| `sampling/createMessage` | MRTR 内嵌 | Sampling | 请求 Host 调模型；已废弃 |
| `roots/list` | MRTR 内嵌 | Roots | 请求相关目录；已废弃 |
| `notifications/cancelled` | Client → Server；Server 仅用于订阅 | Base | stdio 取消请求或结束订阅 |
| `notifications/progress` | Server → Client | Base | 请求范围内的进度 |
| `notifications/subscriptions/acknowledged` | Server → Client | Subscription | 确认订阅过滤器 |
| `notifications/tools/list_changed` | Server → Client | Tools | Tool 清单变化 |
| `notifications/prompts/list_changed` | Server → Client | Prompts | Prompt 清单变化 |
| `notifications/resources/list_changed` | Server → Client | Resources | Resource 清单变化 |
| `notifications/resources/updated` | Server → Client | Resources | 指定 Resource 更新 |
| `notifications/message` | Server → Client | Logging | 请求范围内日志；已废弃 |

## 27. 一次 Tool 调用的完整线上形状

```http
POST /mcp HTTP/1.1
Host: example.com
Content-Type: application/json
Accept: application/json, text/event-stream
MCP-Protocol-Version: 2026-07-28
Mcp-Method: tools/call
Mcp-Name: query_product
Authorization: Bearer <token>
```

```json
{
  "jsonrpc": "2.0",
  "id": "call-1",
  "method": "tools/call",
  "params": {
    "name": "query_product",
    "arguments": {
      "productId": "SKU-100"
    },
    "_meta": {
      "io.modelcontextprotocol/protocolVersion": "2026-07-28",
      "io.modelcontextprotocol/clientInfo": {
        "name": "example-client",
        "version": "1.0.0"
      },
      "io.modelcontextprotocol/clientCapabilities": {}
    }
  }
}
```

```json
{
  "jsonrpc": "2.0",
  "id": "call-1",
  "result": {
    "resultType": "complete",
    "content": [
      {
        "type": "text",
        "text": "{\"id\":\"SKU-100\",\"name\":\"Keyboard\",\"price\":59900}"
      }
    ],
    "structuredContent": {
      "id": "SKU-100",
      "name": "Keyboard",
      "price": 59900
    },
    "isError": false,
    "_meta": {
      "io.modelcontextprotocol/serverInfo": {
        "name": "product-mcp",
        "version": "1.0.0"
      }
    }
  }
}
```

这就是同一份 MCP Client 代码能够调用不同能力的原因：连接地址和鉴权由配置给出，线上方法、Header、JSON-RPC 结构、Tool 清单与结果形状由 MCP 标准统一。
