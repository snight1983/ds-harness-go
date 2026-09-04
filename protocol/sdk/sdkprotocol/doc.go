// Package sdkprotocol 是进程外 SDK 和这套运行时之间那条线上说的话：一条按行分帧的
// JSON-RPC 2.0 通道，加上两端共用的那三对请求/结果和四种通知的形状。
//
// 源: packages/sdk/protocol/src/index.ts:1-25
//
// # 谁在两端
//
// 服务端是 [github.com/snight1983/ds-harness-go/protocol/sdk/sdkserver]；客户端是各语言的 SDK。`serverInfo.name`
// 是**线上稳定**的 `deepseek-harness-sdk-runtime`，改它等于换协议。
//
// # 为什么用 github.com/sourcegraph/jsonrpc2 而不是照着 DSH 再写一遍
//
// DSH 的 transport.ts 有 279 行，其中真正属于这个协议的只有两件事：按行分帧，以及
// 「坏行直接跳过」。剩下的——id 生成、请求和响应的配对、把一次 handler 失败折成
// -32603 错误帧、连接断开时把还等着的请求一次性打回——是每个 JSON-RPC 实现都要写
// 的同一套东西，Go 这边有现成的。
//
// 所以本包只自己写那一层帧编解码（[lineStream]），别的全交给那个库：
//
//   - 请求/响应配对、超时与取消：走 [context.Context]，DSH 那边是 AbortSignal。
//   - -32601 / -32603：库里就是这两个常量，[Handlers.Request] 交回什么错误就折成什么码。
//   - 断开时打回等待中的请求：库里的 Conn 关闭时自己做。
//
// # 坏行为什么必须跳过而不是断开连接
//
// 这条线的另一端是**别人写的 SDK**。一行断在半路的 JSON、一行调试输出混进了 stdout，
// 都不该让整条会话死掉——那会把一次可恢复的手滑变成一次不可恢复的掉线。这一条是
// DSH 明确写下来的行为（transport.ts:4「Malformed lines are ignored」），而那个库自带的
// [jsonrpc2.NewPlainObjectStream] 在解不动的时候会关掉连接，所以帧这一层得自己写。
//
// # 新增: 两条 DSH 没有的上限
//
// 「另一端是别人写的 SDK」这句话同时意味着：那一端可以写坏，也可以写得没完没了。
// DSH 两处都没设防——一行永远不来的换行符会让它的读缓冲一直攒下去，而每一行都
// `void this.handleLine(line)` 一发了之，同时在办多少件完全由对面说了算。本包给这
// 两处各加了一条可调的上限，见 [TransportOptions]：超长的那一行按坏行处理（丢掉，
// 连接照常），名额满了则让读循环停下来等，把压力顺着流还给对面。
package sdkprotocol
