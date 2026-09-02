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
| `tools/consumercheck` | `go list`、本仓库 `go.mod` 与 `go.sum` | 在仓库外建一个模块，验公开 module path 和每个公开包对外可引 |

`tools/internal/rulingtable` 是 `portcheck` 与 `rule` 共用的九列 TSV 读写实现，统一列数、排序、键和状态词汇。

## 路径解析

`tools/internal/toolpath` 负责回答两个问题：仓库根在哪、上游快照在哪。

仓库根从当前工作目录逐级向上找 `go.mod`，认的是 `module` 行而不是文件名——只认文件名的话，工具在任何一个碰巧有 `go.mod` 的目录里都会「成功」，然后拿着错误的根去扫源码，把路径错误报成移植遗漏。账本文件一律落在仓库根的 `docs/portmap/` 下，四个工具不再各写一遍路径拼装。

上游快照由 `DSH_ROOT` 环境变量指定，也可以用各工具的 `-root` / `-dsh-root` 覆盖。快照不存在时：`portmap`、`capmap` 和 `portcheck -mode reanchor` 直接退出（读不到源码就产不出清单，硬失败比产出一份空清单安全）；`portcheck -mode check` 要求显式传 `-no-provenance` 才继续，并在报告里打一行横幅说明这一轮没有对过源码。跳过是允许的，静默跳过不是。

## 仓库外消费门禁

`consumercheck` 解决的是一类仓库内部验不出来的错。`go build ./...` 解析 import 时走的是主模块自己的 `module` 行加相对路径，所以 `go.mod` 写 `module ds-harness-go`、代码写 `github.com/snight1983/ds-harness-go/...` 时仓库内部全绿，而一个从 GitHub 路径引入的外部宿主会撞上 `package ds-harness-go/core/scope is not in std`。

它在临时目录里摆出一个模块名不属于本仓库的消费方，`require` 公开 module path 并 `replace` 到本地检出，`go.mod` 的 require 段和 `go.sum` 直接抄本仓库那一份（这道门禁验的是 module path，不是依赖解析，重新联网解一遍只会把网络抖动算进失败里）。生成的程序空引全部可发布包——命令、`internal/` 和 Git 忽略目录除外，口径与 `doccheck` 一致——再跑一遍最小闭环：建作用域、建会话存储、建会话、追加一条用户消息、读回来。`build`、`vet`、`run` 三步各证一件事：引得进来、引进来是像话的、跑得起来。

程序里还带一句 `var _ agentloop.SessionPersistence = (*persistence.Coordinator)(nil)`。这条接缝一度装不起来（编排器交回准备期，而工厂声明的是会话），两边各自的测试却都绿着——外部装配方是唯一撞得上它的人。

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
- `consumercheck` 用 `replace` 指到本地检出，所以它证明不了「这个版本在模块代理上取得到」——那要等打了 tag 之后才验得了。

## 相关源码

- `tools/portmap/`
- `tools/portcheck/`
- `tools/rule/`
- `tools/capmap/`
- `tools/internal/rulingtable/`
- `tools/internal/toolpath/`
- `tools/doccheck/`
- `tools/consumercheck/`
