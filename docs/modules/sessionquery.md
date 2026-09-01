# Session 查询

## 定位

`sessionquery` 是会话历史读侧，提供一致快照、精确读取、过滤、表面定位、血缘追溯和可选全文搜索；`sessionquery/querytool` 把受限查询能力暴露给模型。

## 架构与逻辑会话

```text
LiveSessions ------+
                   +--> Corpus.Load --> LogicalSession
Persistence -------+         |
                             v
                Engine：read/filter/trace/search
                             |
                             v
                       querytool.Controller
```

同一 ID 同时存在活会话和持久记录时，活会话优先，且一旦确认存活便不访问持久后端。读取持久记录后会再次检查是否刚刚变活，避免返回已经过期的副本。头和事件必须来自同一次逻辑观察。

## 核心能力

| 能力 | 入口 |
|---|---|
| 会话读侧 | `ListSessions`、`ReadSession`、`ReadSurface` |
| 纯过滤 | `FilterSessions`、`FilterEvents` |
| 追溯 | `TraceSession`、`TraceEvent`、`ReadEvent` |
| 投影批读 | `ProjectMany` |
| 搜索 | `SearchSessions`、`SearchEvents` |
| 标题 | `ReadTitle`、`ReadTitleSnapshots` |

每条事件会标记为 `current`、`shadowed` 或 `log-only`。`ExtractEventText` 只提取语义文本，结构边界、流式分块和重复信封不进入检索文档。

## 搜索与工具边界

全文索引由可选 `Searcher` 实现；未配置时只有搜索方法返回 `CodeSearchDisabled`，精确读取和过滤仍可用。`querytool` 再加访问边界、结果数、超时和展示裁剪，不能把底层 Engine 的全部权限直接交给模型。

## 生命周期与并发

- `Corpus` 不拥有 LiveSessions、Persistence 或 Searcher。
- 持久会话批量检查受并发上限控制，取消通过 Context 传播。
- 一次 `ProjectMany` 对每个会话只建立一次观察；单个投影失败不会污染其他结果。

## 失败语义

- 日志损坏、头冲突、非法过滤器、游标失效和搜索关闭使用不同错误码。
- 活/持久头不兼容时拒绝拼接，不能返回混合快照。

## 能力边界

- 不内置搜索索引、排序算法或向量数据库。
- 查询只读，不修复或改写 Session。
- 活会话优先是当前可见性规则，不是跨节点一致性协议。
- 模型工具的结果预算可能小于底层查询结果。

## 相关源码

- `sessionquery/corpus.go`
- `sessionquery/engine.go`
- `sessionquery/filters.go`
- `sessionquery/tracing.go`
- `sessionquery/querytool/`
