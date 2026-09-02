# 不变量诊断

## 定位

`invariants` 是运行时诊断注册表。各模块把“这类状态永远必须成立”的检查器安装进来，注册表统一管理生命周期、收集失败并交给宿主日志或失败处理函数。

## 架构与工作模型

```text
模块 RegisterInvariants
        |
        v
Registry.Register(package, Installer)
        |
        +-- Scope.Defer(cleanup)
        +-- Fail(message)
        |
        v
宿主记录诊断 / 测试断言
```

`Installer` 在独立 `Scope` 中挂监听器或执行检查；`Scope.Defer` 记录逆序清理。`Registered` 返回稳定排序的包名，便于测试和运行时自检。

## 生命周期与并发

- 同一个包名不能重复注册，避免两套检查器同时报告同一事实。
- `Register` 返回幂等撤销函数；撤销和 `Registry.Close` 都会释放 Installer 登记的资源。
- Registry 可并发注册、查询和关闭；用户 Installer 与清理函数在内部锁外执行。
- `Close` 后拒绝新注册，已经取得的撤销函数仍可安全调用。

## 失败语义

配置错误、重复注册、关闭后注册和 Installer 安装失败返回 `Error`。这四种是普通的 `error`，调用方照常处理。

运行时不变量失败走的是另一条路：`Fail` **一定 panic**，值一定是 `*invariants.Error`（`invariants.go:315-317`）。这不是可配的——注册表交给 Installer 的就是这一个实现，宿主换不掉。

理由写在 `Fail` 的类型注释里：不变量违例说的是**程序写错了**，不是一个可以处理的运行期状况。如果它返回 error，调用方就可以把它丢掉，而一个可以被丢掉的不变量检查等于没有检查。上游靠 `throw` 沿栈上抛到触发这次观察的人手里，Go 里能做到「不返回并且沿栈上抛」的只有 panic。

有几个模块（`credentials`、`llm`、`settings`、`storage/domain`）在兜宿主回调的 `recover` 里认出这个值之后**重新抛出去**。那是有意的：那道 `recover` 兜的是「宿主的监听器坏了」，而不变量违例是我们自己坏了，不能被降级成一条日志。

宿主手上的开关是**装不装**，不是**失败了怎么办**：`Config.Enabled` 是总开关，`Allowlist` / `Blocklist` 按包名挑。生产上不想让检查有能力打断请求，就在那一侧关掉它，而不是指望它自己降级。

这条 panic 不会带走整个进程：它落在 Agent 回合正文里时被 `core/agentloop` 那道兜底收成这个回合的一次失败（写 turn/end、广播 `agent/error`、相回到 idle），落在别处时由调用方自己的边界负责。

## 能力边界

- 不变量注册表不修复损坏状态。
- 不替代普通输入校验；只有跨事件、跨生命周期的持续约束适合放这里。
- 是否在生产启用、失败是否阻断服务由宿主决定。
- 各模块仍需提供可直接调用的纯校验函数，便于离线检查历史日志。

## 相关源码

- `invariants/invariants.go`
- 各模块的 `invariant.go`
