// Package goaltool 是那条目标缝**面向模型**的那一层：get_goal、create_goal、
// update_goal 三件工具，加上一条「谁有资格改目标状态」的执行期授权规矩。
//
// 对应 DSH 的 @deepseek-ai/dsh-tool-goal（packages/goal/tool-goal）。
//
// 源: packages/goal/tool-goal/src/index.ts:1-5
//
// 本包自己不拥有任何耐久状态：改动落不落得下去、修订号接不接得上、阶段跃迁成不
// 成立，全归 [ds-harness-go/goal/goal] 那台服务验。本包只回答一个它管不了的问题
// ——**这次调用够不够格**。
//
// # 为什么授权要在这一层做
//
// 目标是一台会自己往下推的东西：一个 active 目标每一轮都会给模型开一个新回合。
// 于是「建一个目标」和「把一个目标推起来」这两件事，如果模型自己就能做，那它就
// 能给自己批一份无限的预算。所以：
//
//   - create、edit、pause、resume 一律要一次**直接的人类回合**：调用方得是一个
//     顶层 agent，而且当前这个还开着的回合里得有一条 kind 是 user 的用户消息。
//   - complete 和 blocked 松一档：直接人类回合行，**当前那个准入轮次**也行——
//     否则一个自动推进的目标永远没人能宣布它做完了。
//   - blocked 再加一道闸：在一个自动轮次里，非得同一个卡点熬过
//     [Config.BlockedAfterConsecutiveRounds] 轮才准报，免得模型第一次遇上难处就
//     把目标停掉。
//
// 这三条都钉在**会话日志**上而不是钉在调用参数上：日志是唯一的耐久权威，一个
// 伪造不出来的 turn/start 边界加上它后面那几条消息的来源，就是「这一轮是谁开的」
// 全部的证据。
//
// # 一次终局更新之后模型还要说一句话
//
// complete 和 blocked 在一个自动轮次里成立时，本包往这次调用的最终结果上捎一条
// 插件来源的上下文（见 [ds-harness-go/core/tools.RunContext.DeferContext]），
// 让模型在这一轮结束之前正面对用户交代一次。它取代的是 DSH 早先那个「硬停回合」
// 的做法——硬停会让最后一轮的产出无人转述。
package goaltool
