# MCP 和 Tool 到底差在哪

先看 `main.go` 里的 `queryProduct`。它只是普通业务函数：传商品 ID，返回商品。

接着看 `server.AddTool(...)`。这层代码做了三件事：

1. 给函数起一个公开名字 `query_product`；
2. 用 JSON Schema 声明模型应该传什么参数；
3. 收到 MCP 请求后调用 `queryProduct`，再把结果按 MCP 格式返回。

所以两者的关系是：

```text
Tool = 模型可以调用的一个能力

本地 Tool：业务函数直接注册进 Harness
MCP Tool：业务函数注册进 MCP Server，通过标准协议让 Harness 远程发现和调用
```

真正干活的仍然是 `queryProduct`。MCP 只增加了“怎么公布、发现、调用和返回”的统一协议。

## 运行

```powershell
cd C:\code\ds-harness-go\examples\mcp-product-server
go run .
```

服务地址是 `http://localhost:8080/mcp`。当前 Harness 连接它时使用：

```go
mcp.Config{
    ServerName: "products",
    URL:        "http://localhost:8080/mcp",
}
```

连接后，MCP 客户端先向服务端读取工具清单，再把它转成 Harness 内部的 Tool。模型最终看到的名字是：

```text
mcp__products__query_product
```
