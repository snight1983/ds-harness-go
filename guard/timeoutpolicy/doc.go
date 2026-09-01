// Package timeoutpolicy 给声明了预算的工具装一条**协作式**的超时。
//
// 对应 DSH 的 @deepseek-ai/dsh-tool-call-timeout-policy（packages/guard/timeout-policy）。
//
// 源: packages/guard/timeout-policy/src/index.ts:1-13
//
// # 「协作式」是这个包唯一的设计前提
//
// 它**停不下**任何东西。Go 里没有办法从外面掐死一段正在跑的代码，所以这一层能做的
// 只有两件事：把一条有期限的 context 递给执行体，以及在期限到了之后把那次调用的结果
// 换成一份模型看得懂的超时。真正停下手上的活，是工具自己的事——一个工具在
// [github.com/snight1983/ds-harness-go/core/tools.Definition.Timeout] 上写下预算，就是在断言它会把 ctx
// 转发给一个取消时收得住的实现。
//
// 一个不理会 ctx 的工具，装了这一层之后**仍然会跑到底**；变的只是模型收到的那份结果。
// 这一点必须说在最前面，否则「设了超时」很容易被读成「超时了就不跑了」。
//
// # 为什么要在期限到了之后换掉结果
//
// 执行体观察到取消之后交出来的东西是它自己的说法：可能是一份「已取消」的错误结果，
// 也可能是一份跑了一半的成功值。这两种模型都读不出「是超时」——而超时和用户按停止
// 在模型那边该有完全不同的反应（超时可以换个更小的输入重试，用户停止不该重试）。
//
// 所以期限赢了的时候，这一层把结果整个换成带 [Code] 的那一份。换掉的判据不是「ctx
// 结束了」，而是 [github.com/snight1983/ds-harness-go/util/timeout.OfContext] 认出这条期限**是本包挂的那一条**：
// 外层还有一个更早到期的期限时，那是上游取消，原样往上传。
//
// # 落在哪条缝上
//
// DSH 把它挂在 cordis 的 `tools/execute` 事件上。Go 这边对应的是
// [github.com/snight1983/ds-harness-go/core/tools.Runtime.AroundDispatch] 那条绕派发瀑布——同一个位置：
// 参数已经验过、审批已经过了，再往里就是执行体。
//
// # DSH 那句 `exec.signal = upstream` 在这里没有对应物
//
// DSH 要在 finally 里把 exec 上的信号换回调用方那一条，否则执行后的监听器会看到
// 一条已经 abort 的信号。Go 这边取消是**参数**不是字段：派生出来的那条 ctx 只递给
// next，函数一返回就没人拿得到它了。没有要还原的东西，也就没有还原时机出错的可能。
package timeoutpolicy
