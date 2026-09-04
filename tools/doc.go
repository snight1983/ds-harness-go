// Package tools 是工具这件事的全部：一个工具怎么被定义、怎么被注册、谁看得见它、
// 一次调用怎么从「模型点了个名」跑到「一份可以落库的结果」，以及这份结果在界面上
// 长成什么卡片。
//
// 源: packages/core/tools/src/index.ts
//
// 上游那一个 index.ts 有 1946 行，本包按「读的人一次要装几套心智模型」切成六份：
//
//   - definition.go   工具、调用、结果这三样东西的形状
//   - runtime.go      注册表：有什么工具、谁看得见哪些
//   - pipeline.go     派发管线：一次调用怎么跑完四段
//   - jsonschema.go   受限 JSON Schema 子集：能写什么、一份 schema 自己合法吗
//   - jsonvalue.go    拿一个值按 schema 验一遍，列出违规说明
//   - presentation.go 一次调用和一份结果在界面上的样子（只有词汇，没有逻辑）
//
// 每份文件开头那条 `本文件的作用：` 讲的是它自己那一份，边界和取舍写在那里；
// 这里只讲整包共有的三件事。
//
// # 一切失败都是结果，不是错误
//
// [Runtime.Execute] 不返回 error。工具抛的、策略拦的、参数不合法、找不到工具、
// 被取消——全都变成 IsError 为真的一份 [Result]。出口是**模型**，而模型只认得
// 工具结果、认不得 Go 的 error；一次失败和一次成功在会话日志里必须是同一种东西，
// 否则重放的时候就少了一条消息。
//
// # 判别联合在这里有两种落法，不是随手挑的
//
// 上游用 TS 的可判别联合表达「几选一」，Go 没有这个东西，本包按**共有字段多不多**
// 分成两种落法：
//
//   - [Result] 是结构体加一个 IsError 判别字段。上游是
//     ToolExecutionSuccess | ToolExecutionFailure，两支共有 Content、
//     ConcludesTurn 这些字段；落成接口的话每一支都得把共有字段再写一遍。
//   - [CallView] 和 [ResultView] 是封闭接口（带一个不可实现的私有方法）。
//     这两族的各支之间几乎没有共有字段，而判别标签必须是**改不掉**的——
//     结构体字段可写，一个能被改掉的判别标签等于没有；接口上的方法改不了。
//
// # schema 的键序是语义，不是格式
//
// 工具的参数和返回值 schema 会原样发给模型提供方，还会进提示词缓存的键。
// Go 的 map 没有顺序，所以 [Node] 的属性表是切片而不是 map，[Node.MarshalJSON]
// 也钉了固定的键顺序——同一份 Node 每次排出来必须一模一样，否则缓存再也命不中。
//
// # 没有 PTC
//
// 上游这个包有一大块是 PTC（alpha.3 之前叫 Code Mode）：把所有工具收拢成一个
// run_code 工具，模型写一段 TypeScript 或 Python，程序在 Node 的 worker thread 里跑，
// 通过生成的 SDK 反过来调那些工具。连同它配套的两个 SDK 渲染器（py-types.ts、
// ts-types.ts）整块不移，理由见 docs/portmap/decisions.md 的
// 「tools —— PTC（run_code）整块不移」。
package tools
