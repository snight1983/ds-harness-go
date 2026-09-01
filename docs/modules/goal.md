# 长期目标

## 定位

`goal/goal` 把跨多个回合推进的目标建模为会话事件；`goal/goaltool` 暴露模型工具；`goal/goalcommand` 暴露用户命令；`goal/goalrounddriver` 在回合边界决定继续、暂停或结束。

## 架构

```text
用户命令 / 模型工具
        |
        v
goal.Service
        |
        | append goal/change
        v
Session Event Log -> Fold -> View / Projection
        |
        v
goalrounddriver -> 下一轮 Agent 工作
```

目标事实只存在于 `goal/change` 事件。`Fold` 根据有序变化得到当前 `View`；投影注册表、工具和驱动器都读取同一套折叠结果，不维护第二份目标状态。

## 核心能力

| 能力 | 入口 |
|---|---|
| 创建与编辑 | `Service.Create`、`Service.Edit` |
| 暂停与恢复 | `Service.Pause`、`Service.Resume` |
| 完成与受阻 | `Service.Complete`、`Service.Block` |
| 清理 | `Service.Clear`、`Service.Disarm` |
| 当前视图 | `Service.Get`、`Fold`、`RegisterProjection` |
| 自动推进 | `goalrounddriver.Install` |

`Ref` 带目标 ID 和版本，所有修改都以当前引用为前提，旧引用不能覆盖新状态。`Authority` 区分用户、目标驱动器和普通模型调用能执行的操作。

## 生命周期与并发

- 目标随 Session 日志创建、恢复和分叉，不依赖进程内常驻对象。
- Service 通过 Agent Registry 找到属主并追加事件，变更监听器按作用域注册。
- Round Driver 只在合法回合边界续跑；它不会在当前步骤中途改写目标状态。
- 版本化引用用于检测并发修改，冲突时调用方必须重新读取后再决定。

## 失败语义

- 非法阶段转换、陈旧引用、未知目标和越权操作返回稳定错误码。
- 追加事件失败时操作不生效；不能先更新内存再补日志。
- 驱动器的提示或回合流不满足不变量时停止推进并报告错误。
- `ValidateStream` 和各包的 `RegisterInvariants` 检查历史状态机。

## 能力边界

- 目标是单会话协作状态，不是跨租户项目管理系统。
- 不保证任务一定完成，也不替业务定义成功标准。
- `blocked` 是目标阶段，不是子 Agent 的停止原因。
- 不内置分布式锁或外部工作队列。

## 相关源码

- `goal/goal/`
- `goal/goaltool/`
- `goal/goalcommand/`
- `goal/goalrounddriver/`
