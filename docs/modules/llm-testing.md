# LLM 测试与回放

## 定位

`feature/replay`、`llm/mockserver` 和 `cmd/llmmockserver` 为模型适配器提供确定性测试环境：前者直接在进程内按脚本回放，后者启动 OpenAI 兼容 HTTP 假服务，命令包提供可独立运行入口。

## 架构与两种测试方式

| 方式 | 适用范围 | 特点 |
|---|---|---|
| `feature/replay` | Agent Loop、重试和日志回归 | 无网络、按调用顺序消费脚本、可从 Session 日志派生 |
| `llm/mockserver` | HTTP、SSE、超时和协议适配 | 真 HTTP 服务，可模拟分块、断流、挂起和错误 |

## Replay

脚本项是封闭的 `Entry`：`ChunksEntry` 返回流片段，`ThrowEntry` 返回错误，`HangEntry` 等待取消。`Install` 向 `llm.Runtime` 注册临时 Adapter 并返回 `Handle`；`AssertConsumed` 验证预期调用没有遗漏。

`DeriveScript` 从事件日志提取可重放响应，`ResolveScriptedEntry` 用当前消息替换脚本占位符。`LoadSessionScripts` 支持按会话配置多组脚本。

## Mock Server

`mockserver.Start` 启动本地 HTTP 服务，`Behavior` 选择正常流、错误、延迟、断开或随机组合。`Requests` 返回请求快照用于断言；`Close` 停止监听并等待服务退出。CLI 由 `ParseCLIArgs` 校验参数，`cmd/llmmockserver` 只负责进程入口。

## 生命周期与并发

- Replay Handle 必须释放；脚本耗尽或结束时仍有未消费项都属于测试失败。
- Mock Server 可并发收请求，请求记录以副本形式返回。

## 失败语义

- `HangEntry` 和挂起 HTTP 行为必须由 Context 或关闭动作解除，测试要设置上限。
- 连接拒绝等无法由已监听 HTTP Server 内部模拟的行为由 CLI 模式明确处理。

## 能力边界

- 这些包用于测试和复现，不是生产模型代理。
- 回放验证调用形状和状态机，不评价模型输出质量。
- Mock Server 只覆盖本项目使用的 OpenAI 兼容表面，不追求实现完整 API。
- 脚本和捕获日志可能包含敏感内容，存档前由调用方脱敏。

## 对应的 DSH 能力

下表由 [`docs/packages.md`](../packages.md) 与 [能力覆盖表](../portmap/capability-coverage.tsv) 机器 join 得到：本篇覆盖的 Go 包，承接的是上游 DSH 的哪几条能力，以及各自还缺什么。落点列由源码里的 `// 源:` 注释反查，不是手写的。

| 上游能力 | DSH 包 | 裁决 | 落在哪个 Go 包 | 这里缺什么 |
|---|---|---|---|---|
| 可编脚本的 OpenAI 兼容 HTTP 服务器，无需密钥即可测试 LLM 适配器和 agent 循环 | `test-support/llm-mock-server` | 需要 | `cmd/llmmockserver` `llm/mockserver` | — |
| 从已记录会话日志回放 LLM 模型流，使快照测试无需 API 密钥 | `test-support/llm-replay` | 需要 | `feature/replay` | — |

## 相关源码

- `feature/replay/`
- `llm/mockserver/`
- `cmd/llmmockserver/`
