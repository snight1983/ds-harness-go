# 用户交互

## 定位

`interaction` 把四类人机交互拆开：斜杠命令、结构化提问、工具审批，以及模型主动调用的 `ask_user` 工具。它们共用 Session 与作用域，但保持不同的协议和失败语义。

## 架构

```text
用户输入 ----------------------> commands.Runtime
模型 ask_user 工具 ------------> userquestions.Service -> Provider
工具执行审批 ------------------> userapproval.Service -> Answerer
计划评审等结构化意图 ----------> userquestions.Intent
```

## 命令

`commands.Parse` 只解析规定的命令形状。`Runtime.Register` 按作用域注册 `Definition`，`List` 和 `Find` 根据 Agent 作用域解析覆盖关系，`Execute` 把 run/done 事件写入会话日志。命令处理器返回结构化 `Result`，不能通过打印输出代替结果。

## 提问与 ask_user

`userquestions.Service` 维护 Provider 列表并用 `Ask` 发送一组 `Item`。每题可以有候选项，也允许自由文本；`Intent` 可表达计划评审等特定语义。`askuser.Tool` 只负责把工具输入转换为该服务请求。

Provider 代表具体 UI、协议桥或宿主回调。没有 Provider、调用方不允许暂停、取消或返回值不合法时，服务返回稳定 `UserQuestionError`。

## 审批

`userapproval.Service` 按作用域查找 `Answerer`，支持 `ask` 与 `never` 策略，以及每个会话的事件化覆盖。每次请求使用唯一 `RequestID`，`asked` 与 `decided` 必须配对；审批结果进入工具管线，但不能绕过工具自身校验。

## 生命周期与并发

- 注册命令、Provider 和 Answerer 都返回撤销函数。
- 同一请求的响应只接收一次，取消通过 `context.Context` 传播。
- 策略变更写入目标会话，恢复后可重新折出有效策略。
- 用户回调不在注册表锁内执行，允许回调安全地再次查询服务。

## 失败语义

- 没有可用 Provider 或 Answerer 时明确失败，不无限等待。
- 用户取消、Context 取消和非法回答保持不同错误语义。
- 命令或审批事件追加失败时不发布成功结果，已经执行的外部副作用也不会被伪造回滚。

## 能力边界

- 不提供 Web、终端或移动端 UI。
- `ask_user` 不能在宿主声明不可暂停的调用位置强行阻塞。
- 审批是一次工具执行决策，不是身份认证或长期权限授予。
- 命令只处理已注册定义，不是 shell 解释器。

## 对应的 DSH 能力

下表由 [`docs/packages.md`](../packages.md) 与 [能力覆盖表](../portmap/capability-coverage.tsv) 机器 join 得到：本篇覆盖的 Go 包，承接的是上游 DSH 的哪几条能力，以及各自还缺什么。落点列由源码里的 `// 源:` 注释反查，不是手写的。

| 上游能力 | DSH 包 | 裁决 | 落在哪个 Go 包 | 这里缺什么 |
|---|---|---|---|---|
| 由插件注册的用户命令注册表，供交互式UI适配器使用 | `interaction/commands` | 需要 | `feature/interaction/commands` | — |
| 模型侧ask_user_question工具，基于ctx.userQuestions实现 | `interaction/tool-ask-user` | 需要 | `feature/interaction/askuser` | — |
| 与通道无关的一次性审批seam，request返回allowed-once/rejected/cancelled/unavailable | `interaction/user-approval` | 需要 | `feature/interaction/userapproval` | — |
| 用户交互Service Definition，定义ctx.userQuestions与提供方注册 | `interaction/user-questions` | 需要 | `feature/interaction/userquestions` | — |

## 相关源码

- `feature/interaction/commands/`
- `feature/interaction/userquestions/`
- `feature/interaction/askuser/`
- `feature/interaction/userapproval/`
