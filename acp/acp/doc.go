// Package acp 是 Agent Client Protocol 的**agent 端**这一半：把一个受信任的自动化
// 客户端接到这套运行时上，只搬四样东西——用户提示词（文本、资源链接、内联图）、
// 已提交的助手输出（文本、图）、取消、以及一次性的授权决定。
//
// 源: packages/acp/acp/src/index.ts:1-10
//
// 线上说什么由 [github.com/coder/acp-go-sdk] 定；本包只管**这一端怎么办事**。
//
// # 为什么用现成的 SDK
//
// 新增: DSH 依赖 npm 上的 `@agentclientprotocol/sdk`。Go 这边对应的是
// [github.com/coder/acp-go-sdk]：它由官方那份 schema 生成，协议版本同样是 1
// （和 DSH 钉的 0.25.1 一致，两条版本线各走各的），零间接依赖，而且线上类型、
// NDJSON 分帧、JSON-RPC 错误码这三样都已经在里面了。本包因此一行线上格式都不写。
//
// # 它只搬"自动化"那一半
//
// 呈现和人机交互不在这条线上：原始流片段、推理过程、工具调用轨迹、计划、标题、
// 重试标记，一条都不往外发。它们是给界面看的，不是给一个程序看的。
//
// # 它拥有什么、不拥有什么
//
// 它拥有**它自己建出来的那些 agent**，收摊时逐个拆掉。它不拥有运行时、不拥有那条
// 通道、也不拥有本进程：DSH 在 apply 里直接读 process.stdin/stdout 造流
// （index.ts:443-448），Go 这边那两样是装配方交给 [github.com/coder/acp-go-sdk.NewAgentSideConnection]
// 的入参，理由和 [ds-harness-go/sdk/sdkserver] 那条逐字相同——只有装配方知道这个
// 进程还有没有别的活儿。DSH 那个只在测试里用的 `config.stream` 因此也不移：
// 换一条流本来就是装配方的自由。
//
// # 装配的次序
//
// 造和装是两步，而且这里比别处多一层绕：[New] 先造出 [Bridge]（它就是那个
// [github.com/coder/acp-go-sdk.Agent] 实现），装配方拿它去造连接，再把连接当
// [Peer] 交回 [Bridge.Install]。这个环是协议本身的形状——DSH 用 `makeAgent(connection)`
// 那个闭包绕的也是同一个环。
package acp
