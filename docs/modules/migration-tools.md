# 移植与文档门禁工具

## 定位

`tools/` 下的命令用于维护上游 TypeScript 到 Go 的移植账本、能力覆盖表和文档覆盖。它们是仓库维护工具，不会链接进 Agent 运行时。

## 架构与工具清单

| 工具 | 输入 | 输出或检查 |
|---|---|---|
| `tools/portmap` | 上游 TypeScript 源码 | 导出符号 TSV 清单 |
| `tools/portcheck` | 符号清单、裁决表和 Go 源码 | 同步裁决表或执行移植门禁 |
| `tools/rule` | 单个包、符号和裁决 | 安全更新一行裁决记录 |
| `tools/capmap` | 能力定义与包裁决 | 生成能力覆盖资料 |
| `tools/doccheck` | `go list`、包映射和 Markdown | 检查每个 Go 包都有文档且导航完整 |

`tools/internal/rulingtable` 是 `portcheck` 与 `rule` 共用的九列 TSV 读写实现，统一列数、排序、键和状态词汇。

## 移植账本

每个上游导出只能处于以下状态之一：

- `PENDING`：尚未裁决，门禁失败。
- `PORTED`：已在 Go 中实现，必须给出 Go 符号引用。
- `GO_NATIVE`：由标准库或成熟 Go 机制替代，必须说明依据。
- `SKIP`：明确不移植，必须说明原因。
- `OUT_OF_SCOPE`：属于产品外壳等当前范围外能力。

`rule` 一次只允许修改一行；匹配歧义、覆盖已裁决行或缺少依据时拒绝写入。这样 Git diff 能准确显示每次判断。

## 主流程

```text
扫描上游导出 -> 同步 portmap.tsv -> 逐符号裁决
       |                                |
       +---------- portcheck -----------+
                          |
                    能力表与发布门禁

go list ./... -> packages.md -> doccheck -> 文档覆盖门禁
```

## 生命周期与并发

这些命令按一次性进程运行，不持有后台资源。生成器先在内存完成解析再写文件。

## 失败语义

解析不出的导出保留为 `UNPARSED`，不能静默丢弃。TSV 中的制表符、换行、重复键和非法状态都会使门禁失败。

## 能力边界

- 工具只能证明“有裁决、有映射、格式自洽”，不能证明设计判断正确。
- `PORTED` 的符号存在不等于行为完全等价，仍需测试和人工审查。
- 生成文件不能替代源码与许可证审查。
- `doccheck` 只校验文档覆盖和本地链接，不评价文字质量。

## 相关源码

- `tools/portmap/`
- `tools/portcheck/`
- `tools/rule/`
- `tools/capmap/`
- `tools/internal/rulingtable/`
- `tools/doccheck/`
