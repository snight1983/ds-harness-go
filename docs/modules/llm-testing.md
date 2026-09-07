# LLM 测试与回放

## 定位

`feature/replay`、`llm/mockserver` 和 `cmd/llmmockserver` 为模型适配器提供确定性测试环境：前者直接在进程内按脚本回放，后者启动 OpenAI 兼容 HTTP 假服务，命令包提供可独立运行入口。`sessionlog/snapshot` 把一次真实运行压成可以提交进仓库的夹具，供 `feature/replay` 下次回放。

## 架构与两种测试方式

| 方式 | 适用范围 | 特点 |
|---|---|---|
| `feature/replay` | Agent Loop、重试和日志回归 | 无网络、按调用顺序消费脚本、可从 Session 日志派生 |
| `llm/mockserver` | HTTP、SSE、超时和协议适配 | 真 HTTP 服务，可模拟分块、断流、挂起和错误 |

## Replay

脚本项是封闭的 `Entry`：`ChunksEntry` 返回流片段，`ThrowEntry` 返回错误，`HangEntry` 等待取消。`Install` 向 `llm.Runtime` 注册临时 Adapter 并返回 `Handle`；`AssertConsumed` 验证预期调用没有遗漏。

`DeriveScript` 从事件日志提取可重放响应，`ResolveScriptedEntry` 用当前消息替换脚本占位符。`LoadSessionScripts` 支持按会话配置多组脚本。

## 夹具是怎么攒出来的

真跑一次，把日志压成可提交的夹具；以后就拿这份夹具当模型，一分钱不花、也不看网络脸色。

```
真跑一次（要 API Key）          攒下来的夹具              以后每次跑（不要网）
┌──────────────┐            ┌───────────────┐        ┌──────────────┐
│  真模型答一轮   │  ──压──▶   │ session.jsonl │ ──喂──▶ │  按夹具重放    │
└──────────────┘            └───────────────┘        └──────────────┘
   sessionlog/snapshot          提交进仓库、逐字节比      feature/replay
```

压这一步要抹掉三样每跑一次都不一样、又和「这一轮到底发生了什么」无关的东西，
否则每次跑完 diff 全红、没人看得下去：

| 抹掉什么 | 换成什么 | 为什么非抹不可 |
|---|---|---|
| 时钟 | 一律归零 | 时间在走，不抹的话没有两次跑得出同一份夹具 |
| 标识 | `{{session:1}}` `{{message:2}}` 这样按类型编的号 | 标识是随机铸的；编号而不是一个统一的 `{{id}}`，是因为夹具要验的多半是**谁指向谁**——子会话指不指得回父会话、答复认不认得它那条提问 |
| 系统提示词和工具清单 | `{{system}}` `{{tools}}` | 这两段占掉一份夹具九成篇幅，而且每份夹具里一模一样。留一份夹具存原文当权威副本，改了提示词就只有那一处变红 |

还有一样不是「抹」而是「重排」：模型吐字是一小段一小段来的，落盘时凑几段打一个包，
包的边界跟着当时的机器状态走。压的时候整串拆开重打一次，于是两次跑打出来的包一样。

一份夹具旁边还放一张 `snapshot.yml`，写的是**这份夹具归谁管**——系统提示词的原文存在哪个场景、
这次答复是录出来的还是手写的、有没有输入被准入挡在会话外面。这些事光看日志本身补不回来。

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
| 无密钥已记录会话测试的共享支持：封闭 manifest、类型化身份脱敏、规范化、workspace 比较、fixture 保护，以及 headless、SDK、ACP 与 Web 的协议适配器 | `test-support/session-snapshot` | 需要 | `sessionlog/snapshot` | 工作区文件快照、四个协议适配器、vitest 套件工厂都没有：前者要本机硬盘，后两者要起子进程 |

## 相关源码

- `feature/replay/`
- `llm/mockserver/`
- `cmd/llmmockserver/`
- `sessionlog/snapshot/`
