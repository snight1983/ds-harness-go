# 运行时上下文

## 定位

`feature/context/instructions`、`feature/context/sessionref` 和 `feature/context/timecontext` 在每一步模型请求前生成可追溯上下文：工作区指令、被引用会话和当前时间。三者都把结果写成带来源的消息，并通过事件日志决定何时重算。

## 架构

```text
文件系统 / 会话查询 / 时钟
          |
          v
 instructions | sessionref | timecontext
          |
          | pre-step / prompt assembly
          v
带 MessageSource 的上下文消息
          |
          v
会话事件日志 -> 下一步重建与校验
```

## 工作区指令

`instructions` 从工作目录向项目根发现候选指令文件，再读取祖先链和本次触及目录的局部指令。它通过内容摘要、版本状态和 `Reconcile` 只注入发生变化的部分，并用字节预算截断输出。

核心入口包括 `FindProjectRoot`、`LoadBaselineSet`、`ProbeScopeInstruction`、`RenderWorkspaceContext` 和 `Install`。文件访问全部走 `fs.FileSystem`，因此可以落在本地、容器或对象存储实现上。

## 会话引用

`sessionref` 使用稳定 URI 表示会话引用。`Resolver` 通过 `sessionquery` 列候选、解析提及并读取目标会话；`RetainReferencedSession` 在字节预算内保留目标表面。引用来源写入消息，恢复后无需依赖进程内对象。

它只允许读取装配方提供的 `SessionSource` 范围，不自行扩大权限。无法解析、越权、循环引用或超过预算都会返回结构化错误。

## 时间上下文

`timecontext` 生成带时区的当前读数，并根据前一条用户消息、步骤上下文和上次注入时间判断是否刷新。`ValidateSession` 校验时间读数的位置和连续性，避免日志中出现无法解释的时间跳变。

## 生命周期与并发

- 三个安装函数都返回清理函数，监听器绑定到显式作用域。
- 状态事实进入会话日志；内存缓存只用于减少重复读取，不是事实来源。
- 文件、会话和时钟可能在装配过程中变化，结果只保证一次步骤装配内部一致。
- 调用方必须把取消通过 `context.Context` 传给文件和查询后端。

## 失败语义

- 指令文件读取失败、项目根解析失败或字节预算不合法时，不生成部分基线。
- 会话引用不存在、越权、循环或超预算时返回结构化错误，不注入猜测内容。
- 时间事件位置或连续性不合法时由 `ValidateSession` 拒绝，不能把损坏历史当作当前读数。

## 能力边界

- 不解析任意自然语言中的隐式会话名称，只处理规定的引用格式。
- 不替宿主决定哪些指令文件可信，也不绕过文件系统策略。
- 不维护长期向量记忆或全文检索索引。
- 时间读数是上下文提示，不是调度器；耐久提醒由 `schedule` 负责。

## 相关源码

- `feature/context/instructions/`
- `feature/context/sessionref/`
- `feature/context/timecontext/`
