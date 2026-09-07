# 凭据

## 定位

`credentials` 定义凭据引用、记录和提供方接缝，让模型配置只保存稳定引用，而不是把 API Key 或授权材料写进会话、设置或日志。

## 架构与数据模型

```text
配置中的 Ref
    |
    v
Provider.Resolve
    |
    +-- APIKeyRecord
    +-- GrantRecord
    |
    v
Resolved（仅在调用边界短暂使用）
```

`Ref` 是公开引用名；`Key` 由 scope 与 id 组成，用于后端记录寻址。`Record` 是封闭接口，当前支持 API Key 和 Grant 两种记录。`Info` 与 `RecordInfo` 只提供可展示元数据，不能泄露秘密值。

## 核心接口

`Provider` 负责解析引用、读取记录、列举元数据和原子更新；`Mutator` 根据当前记录决定是否写入。`Observer` 与 `Notifier` 把引用或记录变化通知给模型路由等消费者，监听器通过返回的取消函数解除。

`NewRef`、`NewKey` 和 `ParseKey` 执行严格语法校验，避免把路径、控制字符或含糊分隔符带入后端键空间。

## 生命周期与并发

- Provider 的存储寿命由宿主管理，本包不创建数据库连接。
- Notifier 可以被并发调用；订阅和取消必须与通知安全共存。
- 监听器失败或 panic 不应阻断其他监听器，具体记录方式由宿主日志器承担。

## 失败语义

- 未找到、记录类型不匹配、引用损坏和后端失败必须区分；不得把后端错误伪装成“没有凭据”。

## 能力边界

- 记录序列化只用于受保护后端，不能写入普通应用日志。
- 展示接口必须使用脱敏信息，不能通过 `Info` 返回密钥正文。
- 本包不实现加密、密钥轮换、OAuth 流程或访问控制；这些由 Provider 与宿主负责。
- 凭据变更通知只表示需要刷新，不携带秘密值。

## 对应的 DSH 能力

下表由 [`docs/packages.md`](../packages.md) 与 [能力覆盖表](../portmap/capability-coverage.tsv) 机器 join 得到：本篇覆盖的 Go 包，承接的是上游 DSH 的哪几条能力，以及各自还缺什么。落点列由源码里的 `// 源:` 注释反查，不是手写的。

| 上游能力 | DSH 包 | 裁决 | 落在哪个 Go 包 | 这里缺什么 |
|---|---|---|---|---|
| 浏览器配置界面的 Host Remote 属主，提供脱敏 settings 与凭据元数据读取、不回传密钥的写入，并在 Host 桌面打开 settings 或 preset 位置 | `api/settings-controller` | 抄形状 | `credentials` | 缺口：credentials 有归属校验，没有「凭据可列出、可引用、读回来打码」这条脱敏读路径 |
| 凭据Service Definition，配置引用与授权记录分离存储 | `credentials/credentials` | 需要 | `credentials` | — |

## 相关源码

- `credentials/credentials.go`
- `credentials/record.go`
- `credentials/provider.go`
- `credentials/notifier.go`
- `credentials/invariant.go`
