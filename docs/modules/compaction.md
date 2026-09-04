# 上下文压缩

## 定位

`compaction` 定义压缩事务、事件、不变量和后端接口；`feature/compaction/basic` 实现基于模型摘要的压缩；`feature/compaction/toolresultpruner` 实现工具结果裁剪。压缩不会删除历史事件，而是追加事件，改变模型当前看到的表面。

## 架构与事务模型

```text
compaction/start
      |
      +-- compaction/summary 或 compaction/prune
      |
      +-- user/message(replace，带 checkpoint source)
      |
compaction/end
```

`StartData`、`SummaryData`、`PruneData` 和 `EndData` 记录事务身份、被遮蔽范围、摘要、计价和结局。`Trace` 与 `ValidateLog` 保证 start/end 配对、归属唯一、替换范围有效，并禁止并行修改同一表面。

## 核心接口

| 对象 | 职责 |
|---|---|
| `Engine` | 自动检查、指定区域压缩和手动压缩的统一接缝 |
| `Maintainer` | Agent 空闲期压缩入口 |
| `BalanceIndex` | 判断切点是否拆开工具调用与工具结果 |
| `basic.Engine` | 根据上下文压力选区、调用摘要模型并提交事务 |
| `toolresultpruner.Pruner` | 对过大的旧工具结果做确定性裁剪 |

`basic.Config` 支持全局策略和按 provider/model 覆盖；`PressureMeter`、`ModelInfoResolver` 与摘要 `Streamer` 均由宿主注入。

## 主流程

1. 从完整事件日志折出当前表面并确认没有活动压缩。
2. 根据目标上下文窗口与保留策略选择可压缩范围。
3. 用 `BalanceIndex` 把边界移动到工具调用配平位置。
4. 生成摘要或裁剪结果，追加 start、结果、replace 和 end 事件。
5. 重新读取日志确认提交后的状态，避免基于过期表面覆盖并发变化。

## 生命周期与并发

- 引擎本身不拥有 Agent；通过安装函数接入维护周期，卸载时只撤监听器。
- 同一会话同时只能有一个活动压缩事务。

## 失败语义

- 摘要失败、日志变化或取消时必须留下可解释的失败结局，不能写成成功摘要。
- `ManualError` 区分用户不可执行、冲突和底层失败；无安全切点时拒绝压缩。

## 能力边界

- 不删除原始日志，不是数据库清理或归档。
- 不保证摘要事实正确；摘要模型和提示词由部署选择。
- `toolresultpruner` 只裁剪模型可见内容，不篡改程序化原始结果。
- 压缩策略不是 Session 持久化实现。

## 相关源码

- `feature/compaction/engine.go`
- `feature/compaction/invariant.go`
- `feature/compaction/toolpairing.go`
- `feature/compaction/basic/`
- `feature/compaction/toolresultpruner/`
