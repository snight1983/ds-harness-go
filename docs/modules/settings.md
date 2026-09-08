# 运行时设置

## 定位

`settings` 提供按 Namespace 注册的类型化配置、JSON 文档合并、修订冲突检测、监听和脱敏展示。

建在它上面的部署级默认模型选择另有一篇：[部署级默认模型](agentdefaultmodel.md)。

## 架构

```text
Backend -> settings.Provider -> Namespace Registration -> Scope[T]
                    |                         |
                    +-- Update/Replace/Mutate+
                    +-- Watch / Describe
                    +-- Redact
```

`Register[T]` 把默认值、解码校验和展示元数据绑定到 Namespace，返回类型化 `Scope[T]`。`Scope.Get` 给出当前快照，`Watch` 观察有效值变化。未注册的文档段可以保留，但只有注册段进入类型化使用。

## 更新语义

- `Update` 合并补丁，`Replace` 替换整个 Namespace，`Mutate` 执行路径操作。
- 可选 `expectedRevision` 提供乐观并发控制，不匹配时返回 `ConflictError`。
- 更新先在内存构造并校验完整下一值，再写 Backend 和发布监听器。
- `Publish` 用于 Backend 外部变化，仍经过注册校验后进入当前文档。

## 脱敏

`Secret` 标记敏感字段，`Redact` 生成可展示的 `Redacted` 文档；描述接口不能返回秘密原文。

## 生命周期与并发

- Provider 启动时读取 Backend，`Close` 后停止发布并拒绝写入。
- 注册、读取、更新和监听可并发；监听器在锁外执行。
- 注销 Namespace 后对应 Scope 失效，不能继续更新。
- Backend 的连接和外部监听寿命由实现方管理。

## 失败语义

- Namespace 语法、JSON 形状、路径操作和类型化校验失败时不发布部分状态。
- 修订冲突要求调用方重新读取，不能自动覆盖。
- Backend 写失败时内存有效值保持旧版本。
- 监听器故障不回滚已经持久提交的设置。

## 能力边界

- 不内置文件、数据库或远程配置 Backend。
- 不负责加密秘密；脱敏只保护展示路径。
- 不提供跨进程共识，修订语义取决于 Backend 实现。

## 对应的 DSH 能力

下表由 [`docs/packages.md`](../packages.md) 与 [能力覆盖表](../portmap/capability-coverage.tsv) 机器 join 得到：本篇覆盖的 Go 包，承接的是上游 DSH 的哪几条能力，以及各自还缺什么。落点列由源码里的 `// 源:` 注释反查，不是手写的。

| 上游能力 | DSH 包 | 裁决 | 落在哪个 Go 包 | 这里缺什么 |
|---|---|---|---|---|
| 浏览器配置界面的 Host Remote 属主，提供脱敏 settings 与凭据元数据读取、不回传密钥的写入，并在 Host 桌面打开 settings 或 preset 位置 | `api/settings-controller` | 取形重写 | `settings` | 缺口：credentials 有归属校验，没有「凭据可列出、可引用、读回来打码」这条脱敏读路径 |
| 用户设置Service Definition，管理按namespace分节的schema默认值、组合base与用户层解析 | `settings/settings` | 需要 | `settings` | — |

## 相关源码

- `settings/settings.go`
- `settings/provider.go`
- `settings/json.go`
- `settings/redact.go`

## 深入阅读

[部署级默认模型](agentdefaultmodel.md) · [凭据](credentials.md)
