# 耐久提醒

## 定位

`schedule/schedule` 把一次性和固定频率提醒写入会话日志，并在原会话存活时驱动投递。定时器只是事件日志的进程内派生状态，随时可以丢弃和重建。

## 架构与数据流程

```text
create after / at / every
          |
          v
schedule/change 事件 -> FoldEvents -> Record / View
          |                              |
          |                              v
          +------------------------ Runtime 定时器
                                         |
                                         v
                                  Agent Inbox + dispatch 事件
```

`after` 和 `at` 只投递一次；`every` 在机器休眠后只投递最新到期批次，不补发全部错过次数。dispatch 记录 `acceptedAt`，使恢复时能重新计算同一结果。

## 时间规则

持久时间统一使用 UTC、毫秒精度的 RFC 3339。输入可以带偏移或 IANA 时区；创建时立即规范化。夏令时回拨产生两个瞬间时选较早值，前拨导致不存在的本地时刻直接拒绝。

核心函数包括 `FormatInstant`、`ParseInstant`、`CanonicalizeTimeZone`、`ResolveEveryOccurrence` 和 `FoldEvents`。

## 生命周期与并发

- `Install` 注册工具、事件和运行时；`Runtime.Start` 启动驱动协程。
- `RequestDrive` 合并重新计算请求，避免每次事件变化都并发建立定时器。
- `Runtime.Dispose` 停止定时器和协程，但不删除日志中的提醒事实。
- 每次触发前重新读取并折叠日志，防止基于旧 Record 投递已删除或修改的提醒。

## 失败语义

- 输入时间、间隔和状态转换错误返回 `InputError` 或 `ToolError`。
- 日志无法折叠返回 `LogError`，驱动器不会猜测当前提醒。
- 事件写入或检查点失败时不报告创建成功。
- 会话不在线时提醒保持 overdue，等原会话恢复后再投递。

## 能力边界

- 当前投递模式是 session-local，不是独立分布式调度服务。
- 进程关闭期间没有外部唤醒，恢复后依靠日志重新计算。
- 不支持 cron 表达式、日历规则或跨会话目标。
- 定时器触发不保证业务动作成功，只保证提醒被交给活 Agent。

## 相关源码

- `schedule/schedule/domain.go`
- `schedule/schedule/runtime.go`
- `schedule/schedule/install.go`
- `schedule/schedule/timeparse.go`
- `schedule/schedule/invariant.go`
