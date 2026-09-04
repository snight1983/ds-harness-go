# 运行时 Guard

## 定位

`guard/timeoutpolicy` 和 `guard/repeattoolreminder` 是工具执行管线上的两类保护：前者统一超时，后者识别机械重复调用并给模型加入提醒。它们通过 `tools.Runtime` 的扩展点安装，不修改具体工具实现。

## 架构

```text
模型工具调用
    |
    v
Tools Pipeline
    +-- timeoutpolicy：派生超时 Context
    +-- 工具执行
    +-- repeattoolreminder：记录调用形状
    |
    v
工具结果 / 下一步提醒
```

## Timeout Policy

`timeoutpolicy.Install` 根据工具定义和运行时约定为执行派生超时上下文。调用方已有更早的截止时间时保留更严格值；取消和 deadline 错误沿工具结果规范返回。它只限制一次调用，不负责取消整个 Agent 回合。

## Repeat Tool Reminder

`Reminder.Observe` 记录同一 Agent 最近的工具名与输入特征，达到配置阈值后生成提醒消息；`NoticeStep` 在步骤边界更新状态。提醒是模型可见上下文，不拦截工具，也不把重复自动判为错误。

`Config` 控制观察窗口、触发次数和提醒文本。状态按 Agent 隔离，并在拥有它的作用域释放时清理。

## 生命周期与并发

- 两个组件都通过 `Install` 返回清理函数，必须跟随作用域释放。
- 重复调用状态受同步保护，可接收并行工具结果。

## 失败语义

- Guard 自身配置无效时安装失败；已执行工具的业务错误不会被 Guard 改写。
- 超时只说明截止时间到达，不能推断底层外部系统已完成回滚。

## 能力边界

- 不提供通用策略语言、内容审核或业务风控。
- 重复提醒是软约束，不保证模型停止重复。
- 超时不是进程隔离；不响应 Context 的工具仍可能继续占用资源。
- 授权和用户审批由 `interaction/userapproval` 与工具管线负责。

## 相关源码

- `guard/timeoutpolicy/`
- `guard/repeattoolreminder/`
- `tools/pipeline.go`
