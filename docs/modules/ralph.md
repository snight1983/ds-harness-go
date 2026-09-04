# Ralph 工作流

## 定位

`workflow/toolralph` 实现固定的多轮子 Agent 工作流：父 Agent 发起一个目标，控制器按轮创建子 Agent、收集报告、决定是否继续，并把最终结果作为工具结果返回。

## 架构

```text
父 Agent 调用 Ralph Tool
          |
          v
Controller
  |  round 1 -> Subagent -> RoundReport
  |  round 2 -> Subagent -> RoundReport
  |  ...
  +-> 完成 / 失败 / 达到轮次上限
          |
          v
规范化工具结果
```

`Config` 控制最大轮次、提示模板和输出预算；`Deps.Subagents` 是创建和等待子 Agent 的显式接缝。`RoundReport` 保存每轮状态和可供下一轮使用的摘要。

## 主流程

1. 校验调用参数并建立本次工作流状态。
2. 为当前轮生成提示，创建受父作用域约束的子 Agent。
3. 等待子 Agent 结算，解析本轮报告。
4. 报告要求继续时进入下一轮；完成时返回最终答案。
5. 任一轮失败、取消或超过轮次上限时生成明确终态。

## 生命周期与并发

- 一次工具调用拥有它创建的所有轮次子 Agent，退出时必须释放未结算资源。
- 同一 Controller 可服务多个并发调用，每次调用状态彼此隔离。
- 父 Context 取消会传给当前子 Agent，并阻止创建下一轮。
- 安装函数只注册工具和不变量，卸载不会终止其他作用域已经拥有的调用。

## 失败语义

- 子 Agent 的 `error`、`aborted`、`max-tokens` 或 `refusal` 都不会被写成完成。
- 报告格式不合法属于当前轮失败，并保留可诊断内容。
- 达到轮次上限返回明确限制状态，而不是无限续跑。
- 输出经过统一保留策略，截断会显式说明。

## 能力边界

- Ralph 是固定控制流，不是通用工作流 DSL。
- 不持久化进行中的轮次，进程重启后不能从中间自动恢复。
- 不自行选择外部模型或工具权限，子 Agent 仍受宿主配置约束。
- 不保证子 Agent 报告的事实正确。

## 相关源码

- `feature/workflow/toolralph/config.go`
- `feature/workflow/toolralph/loop.go`
- `feature/workflow/toolralph/report.go`
- `feature/workflow/toolralph/tool.go`
