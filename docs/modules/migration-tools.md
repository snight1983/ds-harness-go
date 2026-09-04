# 移植与文档门禁工具

## 定位

`internal/devtools/` 下的命令用于维护上游 TypeScript 到 Go 的移植账本、能力覆盖表和文档覆盖。它们是仓库维护工具，不会链接进 Agent 运行时。

## 架构与工具清单

| 工具 | 输入 | 输出或检查 |
|---|---|---|
| `internal/devtools/portmap` | 上游 TypeScript 源码 | 导出符号 TSV 清单 |
| `internal/devtools/portcheck` | 符号清单、裁决表和 Go 源码 | 同步裁决表或执行移植门禁 |
| `internal/devtools/rule` | 单个包、符号和裁决 | 安全更新一行裁决记录 |
| `internal/devtools/capmap` | 能力定义与包裁决 | 生成能力覆盖资料 |
| `internal/devtools/doccheck` | `go list`、包映射和 Markdown | 检查每个 Go 包都有文档且导航完整 |
| `internal/devtools/consumercheck` | `go list`、本仓库 `go.mod` 与 `go.sum` | 在仓库外建一个模块，验公开 module path 和每个公开包对外可引 |
| `internal/devtools/dbcheck` | 整棵目录树的 Go 源码 | 除了 `adapter/datastore`，谁都不许知道下面是个数据库 |
| `internal/devtools/oscheck` | 整棵目录树的非测试 Go 源码 | 业务代码不许碰跑着这个进程的那台机器的磁盘 |

`internal/devtools/rulingtable` 是 `portcheck` 与 `rule` 共用的九列 TSV 读写实现，统一列数、排序、键和状态词汇。

## 路径解析

`internal/devtools/toolpath` 负责回答两个问题：仓库根在哪、上游快照在哪。

仓库根从当前工作目录逐级向上找 `go.mod`，认的是 `module` 行而不是文件名——只认文件名的话，工具在任何一个碰巧有 `go.mod` 的目录里都会「成功」，然后拿着错误的根去扫源码，把路径错误报成移植遗漏。账本文件一律落在仓库根的 `docs/portmap/` 下，四个工具不再各写一遍路径拼装。

上游快照由 `DSH_ROOT` 环境变量指定，也可以用各工具的 `-root` / `-dsh-root` 覆盖。快照不存在时：`portmap`、`capmap` 和 `portcheck -mode reanchor` 直接退出（读不到源码就产不出清单，硬失败比产出一份空清单安全）；`portcheck -mode check` 要求显式传 `-no-provenance` 才继续，并在报告里打一行横幅说明这一轮没有对过源码。跳过是允许的，静默跳过不是。

## 仓库外消费门禁

`consumercheck` 解决的是一类仓库内部验不出来的错。`go build ./...` 解析 import 时走的是主模块自己的 `module` 行加相对路径，所以 `go.mod` 写 `module ds-harness-go`、代码写 `github.com/snight1983/ds-harness-go/...` 时仓库内部全绿，而一个从 GitHub 路径引入的外部宿主会撞上 `package ds-harness-go/scope is not in std`。

它在临时目录里摆出一个模块名不属于本仓库的消费方，`require` 公开 module path 并 `replace` 到本地检出，`go.mod` 的 require 段和 `go.sum` 直接抄本仓库那一份（这道门禁验的是 module path，不是依赖解析，重新联网解一遍只会把网络抖动算进失败里）。生成的程序空引全部可发布包——命令、`internal/` 和 Git 忽略目录除外，口径与 `doccheck` 一致——再跑一遍最小闭环：建作用域、建会话存储、建会话、追加一条用户消息、读回来。`build`、`vet`、`run` 三步各证一件事：引得进来、引进来是像话的、跑得起来。

程序里还带一句 `var _ agentloop.SessionPersistence = (*persistence.Coordinator)(nil)`。这条接缝一度装不起来（编排器交回准备期，而工厂声明的是会话），两边各自的测试却都绿着——外部装配方是唯一撞得上它的人。

## 数据库边界门禁

`dbcheck` 守的是[持久化抽象层](datastore.md)那条界线。它按**路径**分区，不按包名：`datastore/` 底下三条规则全放；`cmd/` 是装配点，可以开连接池、挂驱动，但不许自己拼 SQL；`internal/devtools/` 可以引 `adapter/datastore` 才诊断得了它，但不许开池；其余一律按业务包管。`internal/devtools/dbcheck` 自己整个跳过——它的源码里必然有 `"database/sql"` 这个串和一条认得出 SQL 的正则，而那条正则认得出它自己。

三条规则都是机械可判的事实（某个 import 在不在、某段字符串是不是一句 SQL），没有「可能完全正当」的余地，所以它是阻断式的。SQL 认的是语句骨架而不是单个关键词——一句错误信息里出现「表」「更新」很正常，`INSERT INTO` 这种搭配在自然语言里几乎不会出现。驱动除了写死的名单还有一条按路径命名的兜底：误伤的代价是加一行豁免，漏判的代价是这道墙悄悄没了。

它走文件系统而不是 `go list`：带构建标签、当前平台编译不到的文件也得算数，一道「绝对禁止」的门禁不该因为换了个 `GOOS` 就漏掉半棵树。

## 文件系统边界门禁

`oscheck` 是同一件事的另一半：`dbcheck` 管数据库，它管内容存储。这个服务跑的地方没有可用硬盘，存储全部落在数据库和对象存储上，为的是它能分布式部署；一旦某处代码隐含「有那么一台机器、有那么一个文件系统命名空间」，这个服务就被绑死成单点。内容读写整个收在 `fs.FileSystem` 一条缝上，而这件事光靠约定守不住——只要有一个人写下一行 `os.ReadFile`，那道墙就没了，还是悄悄没的。

两条规则：不许调 `os` 包里那些碰文件系统的函数（打开、读写、建删、改名、取工作目录，完整名单在 `internal/devtools/oscheck/check.go` 的 `bannedOSFuncs`），不许 import `path/filepath` 和 `io/ioutil`。`os` 的其余部分不管——`os.Getenv`、`os.Exit`、`os.Stdout` 和磁盘没有关系。`path/filepath` 是宿主机路径的语法（盘符、反斜杠、符号链接），本仓库的路径一律斜杠分隔、由后端解释，拼路径用 `path`。

只查非 `_test.go` 的文件：测试要造夹具、要临时目录，那是测试进程自己的事。分区同样按路径：`internal/devtools/` 和 `cmd/` 整个放行（它们跑在本机上，是门禁工具和装配点，一个装配点从磁盘读一份配置文件正是它的活儿），其余按业务包管。豁免名单只有两项——`adapter/datastore/dbtest/`（数据库测试夹具，必须供别的包的测试 import，所以不能写成 `_test.go`）和 `feature/replay/`（快照测试用的假模型，从磁盘读回放脚本）。**这份名单是封闭的**；将来真写一个本地磁盘后端 `fs/localdisk` 时再加一条，那时它是唯一一个被允许碰宿主机磁盘的业务包，而这正是它存在的全部理由。

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
- `dbcheck` 认的是 import 和字符串字面量，拦不住把 SQL 拼在运行期的写法；它守的是「别在别处开这扇门」，不是「别写出坏 SQL」。
- `oscheck` 认的是语法上的 `os.X` 调用，拦不住经由反射或第三方库间接摸到磁盘的写法；它守的也是「别在别处开这扇门」。
- `consumercheck` 用 `replace` 指到本地检出，所以它证明不了「这个版本在模块代理上取得到」——那要等打了 tag 之后才验得了。

## 相关源码

- `internal/devtools/portmap/`
- `internal/devtools/portcheck/`
- `internal/devtools/rule/`
- `internal/devtools/capmap/`
- `internal/devtools/rulingtable/`
- `internal/devtools/toolpath/`
- `internal/devtools/doccheck/`
- `internal/devtools/consumercheck/`
- `internal/devtools/dbcheck/`
- `internal/devtools/oscheck/`
