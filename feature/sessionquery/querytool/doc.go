// Package querytool 把 [github.com/snight1983/ds-harness-go/feature/sessionquery] 那台读引擎摆到模型面前，
// 摆成五件工具：跨会话检索、会话内检索、血统追溯、事件追溯、事件精读。
//
// 对应 DSH 的 @deepseek-ai/dsh-tool-session-query（packages/session-query/tool-session-query）。
//
// 源: packages/session-query/tool-session-query/src/index.ts:1-16
//
// # 本包不是一次引擎移植
//
// 读、过滤、检索、追溯这些事一行都不在这里，它们全在 [github.com/snight1983/ds-harness-go/feature/sessionquery]。
// 本包只做三件引擎不该做的事：
//
//  1. **画一条工作区边界。**引擎能看见整份语料；模型只准看见和调用方**同一个
//     工作目录**的那些会话。每一个 id 在被拿去读之前都要先过
//     [Controller] 的授权，越界的目标一律报成同一句「不在调用方工作区内」，
//     绝不透露那个会话到底存不存在。
//  2. **把失败翻译成模型读得懂、又不泄露内情的一句话。**引擎的 17 个错误码里
//     有两个（CodeInvalidConfig、CodeSourceConflict）说的是装配和后端的事，它们
//     一律塌成一句 "session query operation failed"；其余的各有一句安全说法。
//     原始的那条错误链只进日志。见 boundary.go。
//  3. **把结果排成给模型看的纯文本。**五件工具的输出 schema 全是 string——模型
//     要的是能直接读的一段话，不是一份要它自己再解一遍的 JSON。见 presentation.go。
//
// # 和 DSH 不一样的地方
//
//  1. **包名叫 querytool，不叫 sessionquery。**本仓库的命名规矩是把 `tool-`
//     前缀去掉，那样这个包会和 [github.com/snight1983/ds-harness-go/feature/sessionquery] 撞名。也没有并进那个包：
//     引擎那一层现在只依赖 llm/session/persistence，把 tools、scope、
//     harness/systemprompt 拖进去会让一台纯粹的读引擎背上整套工具运行时。
//  2. **时间戳的上下界用整数取整，不是浮点数的相邻可表示值。**DSH 的
//     nextUpFinite / nextDownFinite 在 float64 的位表示上找相邻值，因为那边的
//     范围端点是浮点毫秒，而 ISO 时间戳可以带亚毫秒小数。Go 侧
//     [github.com/snight1983/ds-harness-go/feature/sessionquery.Range] 的端点是 *int64 毫秒，压根表示不了亚毫秒，
//     所以那套位操作没有对应物：下界向上取整到下一个整毫秒、上界向下取整到上一个，
//     得到的筛选集合和 DSH 逐个事件一致。见 input.go 的 timestampLowerBound。
//  3. **agent 是一把不透明的作用域钥匙。**DSH 的 exec.agent 就是那个 agent 对象，
//     摸得到会话。Go 这边它是 *scope.Key，所以本包走 [Config.AgentOf]，
//     和 [github.com/snight1983/ds-harness-go/feature/plan/planmode] 同一个做法。
//  4. **超时不由本包计时。**DSH 在每次 invoke 外面自己包一个 timeoutMs。Go 侧
//     [github.com/snight1983/ds-harness-go/tools.Definition.Timeout] 就是这件事，由工具运行时统一
//     执行，也统一不发给模型。
//  5. **不变量是一个空壳。**和 DSH 一样：本包不拥有任何事件类型，那个安装器
//     只是把包名占住，好让注册表上「谁负责查什么」这张表是完整的。见 invariant.go。
//  6. **标题读不到时那句占位不带引擎的原始码。**DSH 把被清洗过的错误码拼进
//     `${text} (title unavailable: ${code})`。Go 这边照做，但那个码取自本包
//     自己的 [Code]，不是引擎的——引擎的码是内情，一个字都不该出现在模型眼里。
package querytool
