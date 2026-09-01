// Package mcp 把一台外部 MCP 服务器上的工具接进本装置的工具注册表：连上去、
// 把对方报的工具按 `mcp__<服务器名>__<原名>` 这个公开名注册进来、对方改了工具清单
// 就重新同步一遍、断了就按退避策略重连。
//
// 对应 DSH 的 @deepseek-ai/dsh-mcp-client（packages/mcp/mcp-client）。
//
// 源: packages/mcp/mcp-client/src/index.ts:1-14
//
// # 命名契约
//
// 每个 MCP 工具的稳定身份是 `(服务器名, 原名)` 这一对。模型看见的是
// `mcp__<服务器名>__<原名>`，按提供方对函数名的约束（至多 64 个字符、只许
// `[A-Za-z0-9_-]`）归一化过。原名**只**出现在线上（`tools/call` 的 name 字段）；
// 公开名从不被反解回去。见 [PublicToolName]。
//
// # 一次同步分两步
//
//  1. **取**。把 `tools/list` 的分页抽干，先在内存里把下一代工具定义全部造好。
//     这一步失败（网络断了、对方的清单里同一个名字出现两遍），上一代注册原样留着。
//  2. **换**。先撤上一代，再注册下一代。这一步撞名只可能是别人占了这台服务器的
//     `mcp__<服务器名>__` 命名空间——那就把这一代已经注册进去的全部回滚，
//     让模型要么看见完整的一代、要么一个都看不见，绝不看见半代。
//
// # 和 DSH 不一样的地方
//
// 新增: **只支持 Streamable HTTP，不支持 stdio。** DSH 的 stdio 传输要起一个子进程，
// 而子进程那一块（`@deepseek-ai/dsh-subprocess`，含环境变量洗白）在本仓库整块不移，
// 见 docs/portmap/decisions.md。少了那一块，stdio 这条腿就没有落脚点。
//
// 新增: DSH 是一个 cordis 插件：`apply(ctx, config)` 里用 `ctx.effect` 登记
// 「serverName 命名空间占用」和「连接的释放」两件事，靠 fiber 的销毁把它们收回去。
// Go 这边没有那个容器，所以换成一对普通的类型：[Host] 拥有那张占用表，
// [Host.Connect] 交回一个 [Connection]，[Connection.Close] 就是那两条 effect 的析构。
//
// 新增: DSH 绕开 SDK 自己发 `tools/list` 和 `tools/call`（`listToolsUncached`、
// `callToolUncached`），为的是躲开 SDK 那个按页缓存的输出校验器。Go 的 SDK 没有那件事：
// 它的清单缓存只在服务器显式给了 TTL 时才生效，而且 `tools/list_changed` 通知会在
// 用户回调**之前**把它作废；`CallTool` 也不拿工具的 outputSchema 预校验返回值。
// 所以这里直接用 SDK 的 `ListTools` / `CallTool`。
//
// 新增: DSH 把 `tools/call` 的返回值当成一段**完全不可信**的 JSON 记录来收
// （`z.record(z.string(), z.unknown())`），于是对方回了一个本装置不认得的内容块时，
// 那一块降级成一句诊断，整次调用照样成功。Go 的 SDK 没有留这个口子：`CallToolResult`
// 的解码在遇到不认得的 `type` 时整条失败，而绕开它就得自己手写 Streamable HTTP 的
// 握手、关联和续传——那正是「移植时用 Go 现成的办法，不要照抄别人造的轮子」要避免的。
// 所以这里接受一处行为差异：**不认得的内容类型**会让整次调用失败（模型看到一条
// isError 结果，里面写着解码失败），而不是逐块降级。反过来，**认得但缺字段**的块
// （没有 text 的 text、mimeType 不对的 image、缺 name/uri 的 resource_link、
// audio、resource）仍然逐块降级，和 DSH 一字不差——那些块解出来是零值字段，
// 而 [projectContent] 本来就按零值分流。
//
// 新增: 同一条链上，DSH 的 `decodeImage` 要自己判 base64 是不是规范的（拒绝
// URL-safe 别名和多余空白）。Go 的 `encoding/json` 把 `[]byte` 字段按
// `base64.StdEncoding` 解，解不动就整条失败，所以那一支在这里走不到，只留下
// 「声明的媒体类型不是 PNG/JPEG/WebP/GIF」那一支。
//
// 新增: DSH 的 Tool 类型带 `execution.taskSupport`，它据此拒绝跑一个要求
// task 式执行的工具。Go SDK v1.7.0 的 `mcp.Tool` 不解这个字段，拿不到这条信息，
// 所以那道检查在这里不存在——那样一个工具会被照常调用，然后由对方自己报错。
//
// 新增: 自定义请求头。DSH 通过 `requestInit.headers` 传，Go SDK 的
// `StreamableClientTransport` 没有这个字段，所以换成一个包一层的
// [net/http.RoundTripper]——那是 Go 里加请求头的常规做法。
//
// # 图片这一路
//
// 一次 MCP 结果里带图时，本包会试着把图存进持久附件仓库，好让模型真的看见它。
// 存不下（没装仓库、当前模型不收图、限额超了、字节坏了）就把这次结果里的**每一张**
// 图都投影成一句诊断文本，而那份原始的 MCP 值一个字节不改地留给程序化调用方。
//
// 新增: DSH 在这里读 `exec.agent.session.requestHeader()` 和
// `ctx.get('llm').resolveModelInfo(...)`，为的是拿到「当前这次请求真正路由到了哪个
// 模型、那个模型收不收图」。Go 这边 [github.com/snight1983/ds-harness-go/core/tools.ExecutionInput.Agent]
// 只是一个作用域键，活会话和 llm 服务都在后面的块里，本包够不着。所以那一整段
// 换成 [Options.ImageAdmission] 这个接缝：装配方自己回答「这次执行能不能收图、
// 收的话存到哪」。它为 nil 时图一律降级成文本——那正是 DSH 在没装附件仓库时的行为。
// # 覆盖率封顶在 95.3%
//
// 本包有 I/O，按仓库的规矩要写明为什么到不了 99%。差的那 22 个语句块逐个查过，
// 分五类，没有一类是「懒得写测试」：
//
//   - **撤销注册失败**（bridge.go 的 171／180、connection.go 的 137／323、
//     host.go 的 137／143）。这些是撤工具时又出错的那条日志。撤销走的是
//     [github.com/snight1983/ds-harness-go/core/tools.Runtime] 交回来的那个函数，本包自己注册进去的
//     东西撤起来不会失败；要造出失败得换掉整张注册表，那时测的就不是本包了。
//     错误照样接住并且记下来，不接住是更糟的代码。
//
//   - **对本地拼出来的值调 json.Marshal 失败**（bridge.go 的 377／388／398／
//     421／428、content.go 的 105）。被排的是 [Result]、一块 [sdk.TextContent]、
//     和一列刚从线上解下来的内容块，里面没有接口、没有 map、没有环、没有非有限
//     浮点，`encoding/json` 没有失败的余地。
//
//   - **一台不守规矩的服务器才走得到的支**（bridge.go 的 152、381）。一条是
//     「同一个名字在 tools/list 里报了两遍」，一条是「结果里连 content 这一格
//     都没有」。Go SDK 的服务器造不出这两样：AddTool 按名字去重，而线上编码把
//     「没有内容」归一成一个空列表。要覆盖它们得自己手写一台不合规的 HTTP
//     服务器，那测的是那台假服务器写得对不对，不是本包。
//
//   - **一代连接关不掉**（connection.go 的 283／284／290）。要一条 Close 之后
//     [sdk.ClientSession.Wait] 五秒内不返回的传输。真造出来的话，这个测试自己
//     就得挂五秒。
//
//   - **在一条活着的连接上重新取清单失败**（connection.go 的 147／246／249／252）。
//     清单变更是从那条常驻 SSE 流上来的，所以要走到这里，服务器得「SSE 流还活着、
//     但紧接着的 tools/list 失败」。同一台 httptest 服务器上，把它弄坏会先把那条
//     流弄断——于是走的是重连那条路，不是这条。重连那条路本身是覆盖了的。
//
// 剩下 connection.go:199 那个 `default:`（清单变更 channel 满了就丢）在覆盖率
// 里算零个语句，但它同样只在两条通知贴着到达时才走得到，靠不住地复现不了。
package mcp
