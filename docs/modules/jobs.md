# 后台作业

## 定位

`jobs/jobs` 定义后台作业的公共契约，`jobs/localjobs` 提供进程内注册表，`jobs/jobstool` 向模型暴露列举、读取、等待和终止能力，并在作业完成时把通知投回属主 Agent。

## 架构

```text
生产方 -- Start + Hooks --> localjobs.Registry
                               |
             +-----------------+----------------+
             |                                  |
        jobstool                           宿主观察者
             |
       Agent Inbox / 唤醒
```

`Start` 声明种类、属主和取消钩子；`Snapshot` 是不可变公开状态；`Read` 额外返回受控输出。`JobStatus` 的终态只由生产方结算或注册表在取消失败时确定。

## 主流程

1. 生产方调用 `Registry.Start`，获得 `JobID`。
2. 运行中可以更新状态；消费者用 `List`、`Get` 或 `Read` 读取快照。
3. `Wait` 等待终态，支持取消和超时。
4. `Kill` 调用生产方 `Hooks.Cancel`，但不把“已请求取消”误报成“已经停止”。
5. 结算监听器决定向 Agent 唤醒、注入或仅标记已汇报。

`jobstool.Config.MaxConsecutiveWakes` 限制作业完成触发的自激唤醒链；用户新输入被认领时预算重置。

## 生命周期与并发

- `localjobs.Registry` 用互斥锁保护记录，所有监听器和生产方钩子都在锁外调用。
- 属主作用域释放或 Registry Dispose 时请求取消，并等待守规矩的生产方结算。
- 每个作业用关闭 channel 广播结算，多个等待者不会相互消费通知。
- 返回的 Snapshot 和 Read 都是副本，调用方不能修改活状态。

## 失败语义

- 未知、无权访问和已经终止的作业分别返回明确错误。
- Cancel 钩子失败时记录可能成为孤儿，并把可观察状态判为失败。
- Context 超时只结束等待者，不会伪造作业终态。
- 输出经过保留上限处理，截断会显式标记，不能静默冒充完整结果。

## 能力边界

- `localjobs` 不是持久化或分布式队列，进程退出后记录消失。
- 不负责启动 goroutine；生产方自己拥有实际工作。
- Kill 是协作式取消，不是强杀进程。
- 作业通知不等同于用户消息，投递策略由装配配置。

## 相关源码

- `jobs/jobs/`
- `jobs/localjobs/`
- `jobs/jobstool/`
