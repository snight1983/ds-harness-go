# 通用运行时工具

## 定位

`util/outputretention` 统一大输出的保留和省略说明，`util/timeout` 统一超时边界、错误分类和空闲看门狗。这些包不依赖 Agent 业务，可被工具、协议和工作流复用。

## 架构与输出保留

`ItemRetainer` 按项目数保留头部；`TextRetainer` 按字节保留头部、尾部或头尾组合。`RetainedItems` 与 `RetainedText` 同时返回保留内容和 `Omitted`，明确省略数量是精确值还是未知值。

`RetentionNotice`、`DescribeOmitted` 和 `FormatRetentionNotice` 生成一致提示，调用方不能用含糊的“内容过长”隐藏实际省略。

策略在创建时校验，`PushDecision` 允许流式生产方在已经不需要更多输入时提前停止收集。

## 超时

`Clamp` 把请求超时限制在默认值和最大值之间；`Deadline` 派生带稳定错误码的 Context；`Of` 与 `OfContext` 把底层取消原因归一为 `Reason`。

`Watchdog` 管理空闲超时，`Pulse` 在收到活动时重置计时，`Receive` 把 channel 接收、父 Context 和空闲截止合成一次选择。`Stop` 幂等释放计时器。

## 生命周期与并发

- Retainer 是单次流的累加器，不保证多 goroutine 并发 Push。
- Watchdog 可在生产者活动点 Pulse，但同一调用方必须遵守其同步契约。
- 所有计时器都必须 Stop，避免测试和长驻服务泄漏资源。
- 工具包不启动长期后台协程。

## 失败语义

- 非法负数、头尾预算溢出和未知枚举在构造时返回错误。
- UTF-8 文本按字节预算保留时保证输出仍是合法 UTF-8。
- 超时错误说明哪个策略触发，但不证明被调用方已停止。

## 能力边界

- 这些工具不提供持久化、压缩、日志采样或分布式 deadline 协议。

## 相关源码

- `util/outputretention/`
- `util/timeout/`
