// Package agentloop 是驱动一个 agent 跑回合的那一层：ReAct 循环本身、它调度
// 一个步骤里那些工具调用的办法、以及把动态运行期上下文投影进会话日志的投影器。
//
// 对应 DSH 的 @deepseek-ai/dsh-agent-loop（packages/core/agent-loop）。
//
// 源: packages/core/agent-loop/src/index.ts:1-10
//
// # 这一层和 core/agent 的分工
//
// [ds-harness-go/core/agent] 定义「一个活 agent 长什么样」——[agent.Agent] 这个
// 面、它的收件箱投影、装着活 agent 的注册表、以及别人往循环上挂钩子的那些扩展点。
// 它**不造** agent，也不驱动任何东西。
//
// 本包是那张契约的实现方：[ReactLoopAgent] 实现 [agent.Agent]，[AgentLoop] 实现
// [agent.Factory] 并由 [agent.Registry.SetFactory] 登记进去。于是「谁能看见一个
// agent」和「谁能驱动一个 agent」被分在两个包里，前者不必知道后者存在。
//
// # 一个回合是什么
//
// 源: packages/core/agent-loop/src/agent.ts:246-425
//
// 一个**回合**（turn）从收件箱里认领一批消息开始，到「模型不再要求调工具」为止。
// 回合里的每一次模型调用是一个**步骤**（step）。一个步骤要么以一条不带工具调用的
// 助手消息收尾（回合结束），要么派发它请求的那些工具、把结果送回去、进下一个步骤。
//
// 这两层循环的每一步都往会话日志上留痕：turn/start、step/start、user/message、
// assistant/chunk、assistant/message、tool/call、tool/result、step/end、turn/end。
// 日志是权威的——[ReactLoopAgent] 自己身上除了「现在跑到第几回合第几步」之外
// 不留状态，重建一个 agent 就是从日志重建。
//
// # 取消
//
// 新增: DSH 用 AbortController／AbortSignal 表达取消，一个 agent 身上挂着当前
// 那一次的 controller。Go 里同一件事是 [context.Context] 加
// [context.CancelCauseFunc]：驱动那一路自己派生一个可取消的 ctx，
// [ReactLoopAgent.Cancel] 调它的 cancel 并把原因带上，跑在里面的每一层
// （模型流、工具派发、提示词装配）顺着 ctx 参数拿到同一次取消。
//
// 取消的**原因**是 [ds-harness-go/session.TurnEndCancelCause]，它会被记进
// turn/end 那条事件，所以「这一轮为什么停」在日志里读得出来。
package agentloop
