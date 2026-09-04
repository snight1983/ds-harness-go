// Package main 是分层门禁：它保证「A 在 B 上面」这句话有一处会变红的地方。
//
// 新增: DSH 没有对应物。它那边是一个 CLI 进程，层次靠人读文档维持；本仓库的
// 99 个包按角色分了七档，而目录名只是给人读的索引——一个包挪进 feature/ 不会
// 让它变成能力包，只会让这道门禁按能力包的规矩查它。
//
// # 它查什么
//
// 一条规则：**低档不许 import 高档**。档位顺序是
//
//	contract < runtime < feature < adapter < protocol < assembly < cmd
//
// 每个包的档位写在 docs/layers.tsv 里，两列：包路径、档位。仓库里多一个包、
// 表里少一行，或者表里有一行指向已经不存在的包，都判红——否则一个新包可以
// 靠「没登记」绕开整道门禁。
//
// 生产代码和测试一起查。一条 `feature/x` 只在测试里 import `protocol/y` 的边，
// 照样把两个包绑在了一起：那份测试跑不起来，`protocol/y` 就动不得。
//
// # 七档各是什么
//
//   - **contract**——这个模块对外的门面，平铺在顶层：llm、tools、scope、
//     sessionlog、storage、fs、attachment、spill、settings、credentials、
//     invariants，以及它们各自的测试替身。它们不许 import 任何别的档。
//   - **runtime**——harness/ 底下那五个包，运行期本身：一个活 agent 长什么样、
//     驱动它的循环、会话活着的那一半、这一步该看到什么提示词、没自带模型选择时用哪个。
//   - **feature**——feature/ 底下的能力包。它们建在运行期之上，彼此之间可以互引。
//   - **adapter**——adapter/ 底下的生产后端实现。它们实现契约层声明的那些接口。
//   - **protocol**——protocol/ 底下的对外线协议：ACP、MCP、SDK。
//   - **assembly**——harness 那个门面包，它按顺序把上面这些拼起来。
//   - **cmd**——可执行文件、样例宿主和门禁工具自己。装配点在最上面。
//
// # 档位不按目录名判，按表判
//
// 一处刻意的偏离，是因为**实际的 import 方向**说了算：
//
//   - feature/persistence 和 feature/projectioncache 原先摆在 sessionlog/ 和
//     adapter/ 底下。前者在生产代码里 import harness/session，后者被
//     feature/subagent 在生产代码里 import——它们都不可能是契约包或后端实现。
//     搬到 feature/ 之后目录和档位才对得上。
//
// 换句话说，这个表是可以和目录名不一致的，只是眼下没有不一致。真出现不一致时，
// 以表为准，并在这里记下理由。
package main
