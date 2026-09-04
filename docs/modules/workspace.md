# Workspace

## 定位

`workspace` 管理宿主中的工作区实体、显示顺序和 Session 归属。它把路径解析结果、标题和会话列表写入 `storage/domain` 事件域，并把活会话与持久记录组合成当前状态。

## 架构

```text
fs.FileSystem.Resolve
        |
        v
workspace.Registry
        +-- storage/domain Facility
        +-- LiveSessions
        +-- Persistence
        |
        v
Workspace 实体 / 排序 / 归档会话
```

`Spec` 定义工作区域事件域的记录、折叠和不变量。`Registry.Open` 从 Domain Facility 恢复实体；`Create`、标题修改、会话挂接和排序都先形成领域操作，再更新可读快照。

## 核心能力

| 能力 | 入口 |
|---|---|
| 工作区管理 | `Create`、`Get`、`List`、`Delete` |
| 路径查找 | `ResolveByPath` |
| 排序 | `InsertBefore`、`Workspace.InsertSessionBefore` |
| 会话归属 | `AttachSession`、`DetachSession`、`SessionIDs` |
| 归档 | `ArchiveSession`、`ArchivedSessionIDs` |
| 活跃状态 | `Workspace.Status` |

路径身份使用 `fs.TargetKey`，展示路径只用于 UI。相同规范目标不能创建两个独立 Workspace。

## 生命周期与并发

- Registry 打开后持有领域设施订阅和实体缓存，`Close` 释放这些资源。
- Workspace 方法返回切片副本，不能从外部修改内部排序。
- 领域事件提交和内存快照更新按顺序串行，读取可并发。
- 删除前检查活会话与持久关联，避免留下无主 Session。

## 失败语义

- 重复路径、未知工作区、排序目标不存在和非法 Session 操作返回稳定 `Code`。
- 持久提交失败时不发布新快照。
- `PendingMutation.Validate` 与 `DomainState.Validate` 防止损坏记录进入折叠。
- 路径解析失败保持文件系统原始原因，不能降级成“未找到工作区”。

## 能力边界

- Workspace 是会话组织层，不创建真实目录，也不负责代码仓库操作。
- 不提供用户、团队或 ACL 模型。
- `Persistence` 只用于确认 Session 归属，不是 Workspace 自己的数据库实现。
- 跨进程一致性取决于 `storage/domain` Facility 后端。

## 相关源码

- `feature/workspace/registry.go`
- `feature/workspace/entity.go`
- `feature/workspace/spec.go`
- `feature/workspace/types.go`
- `feature/workspace/invariant.go`
