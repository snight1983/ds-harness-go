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

配置错误、重复注册、关闭后注册和 Installer 安装失败返回 `Error`。运行时不变量失败通过 `Fail` 报告；它是一条诊断通道，不默认 panic，也不自动停止 Agent。宿主可在测试中把 Fail 配成致命断言，在生产中记录并告警。

## 能力边界

- 不变量注册表不修复损坏状态。
- 不替代普通输入校验；只有跨事件、跨生命周期的持续约束适合放这里。
- 是否在生产启用、失败是否阻断服务由宿主决定。
- 各模块仍需提供可直接调用的纯校验函数，便于离线检查历史日志。

## 相关源码

- `invariants/invariants.go`
- 各模块的 `invariant.go`
