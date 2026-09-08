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

`Spec` 定义这个域的记录与全局状态。**记录的权威在介质上，进程里不留副本**：一个工作区句柄只攥着一把表键，每一次取值都是一次真的往返。多副本部署下这是必需的——留在进程里的那份副本，在另一台机器改了同一条记录之后就是错的，而它错了不会有任何一步报错。

## 核心能力

| 能力 | 入口 |
|---|---|
| 工作区管理 | `Create`、`Get`、`List`、`Delete` |
| 路径查找 | `ResolveByPath` |
| 排序 | `InsertBefore`、`Workspace.InsertSessionBefore` |
| 会话归属 | `AttachSession`、`DetachSession`、`SessionIDs` |
| 归档 | `ArchiveSession`、`ArchivedSessionIDs` |
| 活跃状态 | `Workspace.Status` |
| 一次读回全部字段 | `Workspace.Snapshot` |

路径身份使用 `fs.TargetKey`，展示路径只用于 UI。相同规范目标不能创建两个独立 Workspace。

## 一次读回全部字段

逐字段读之间**没有原子性**。别人的写可以夹在两次读中间，读到的就是两个时刻拼出来的一份记录——而那份记录在介质上从没有存在过：

```
写手：  改标题        挂一个会话
        ↓             ↓
介质： {旧,无} ──→ {新,无} ──→ {新,有}

逐字段读：  读标题 ───────────────→ 读会话
            拿到「旧」              拿到「有」
            结果 = {旧,有}  ← 介质上从没出现过这一种

Snapshot：       ┌─ 一次读 ─┐
                 拿到 {新,无}  ← 三种真实状态里的一种
```

界面上这种撕裂看得见：新标题配着旧会话列表，两个字段各自都是真的，只是不同时。`Snapshot` 只发一次读，所以交出来的字段同属一个时刻。

它**不含** `Status`——那一位要另外问一次文件系统，混进来就是拿第二次往返的答案冒充同一时刻的事实。它也**只管一个工作区**：连着读两个工作区仍然是两次读。

## 生命周期与并发

- Registry 打开后持有域设施的订阅，`Close` 释放。
- 工作区句柄没有可变状态，可以被多个 goroutine 同时用。
- 交出去的切片都是新的一份，改它碰不到别处。
- 每一次写都在域的写链上跑，别的副本插在中间时按修订号重来一轮。
- 删除前检查活会话与持久关联，避免留下无主 Session。

## 失败语义

- 重复路径、未知工作区、排序目标不存在和非法 Session 操作返回稳定 `Code`。
- 记录被别的副本删掉了报 `CodeWorkspaceGone`，它既不是介质自相矛盾，也不是后端故障——把它折进那两个里，会让「这个工作区没了」读起来像一场事故。
- 落盘失败时不改介质，也不发变更事件。
- `PendingMutation.Validate` 与 `DomainState.Validate` 防止损坏记录进入折叠。
- 路径解析失败保持文件系统原始原因，不能降级成“未找到工作区”。

## 能力边界

- Workspace 是会话组织层，不创建真实目录，也不负责代码仓库操作。
- 不提供用户、团队或 ACL 模型。
- `Persistence` 只用于确认 Session 归属，不是 Workspace 自己的数据库实现。
- 跨进程一致性取决于 `storage/domain` Facility 后端。

## 对应的 DSH 能力

下表由 [`docs/packages.md`](../packages.md) 与 [能力覆盖表](../portmap/capability-coverage.tsv) 机器 join 得到：本篇覆盖的 Go 包，承接的是上游 DSH 的哪几条能力，以及各自还缺什么。落点列由源码里的 `// 源:` 注释反查，不是手写的。

| 上游能力 | DSH 包 | 裁决 | 落在哪个 Go 包 | 这里缺什么 |
|---|---|---|---|---|
| Host 的 ctx.workspaceController 与 Client workspace namespace，负责 Workspace 增删改序、Session 重排与归档、完整 Workspace 投影跟随，并拥有选目录 seam | `api/workspace-controller` | 取形重写 | `feature/workspace` | 缺口已补：一次读回全部字段的自洽快照落在 feature/workspace 的 Snapshot（多带 TargetKey、不带 Status）。baseline＋增量那一半是有意不落——Go 侧同一语义由 sessionlog/projection 的整值投影承担，重连重取整值 |
| Workspace实体注册表，持久化workspace记录、顺序、会话归属索引，支持创建/删除/排序/归档操作 | `workspace/workspace` | 需要 | `feature/workspace` | — |

## 相关源码

- `feature/workspace/registry.go`
- `feature/workspace/entity.go`
- `feature/workspace/spec.go`
- `feature/workspace/types.go`
- `feature/workspace/invariant.go`
