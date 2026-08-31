// 本文件的作用：循环调度器的那些默认值。
//
// 源: packages/core/agent-loop/src/constants.ts:1-6

package agentloop

// DefaultMaxParallelToolCalls 是一个步骤里同时在飞的并行安全调用数的默认上限。
//
// 源: packages/core/agent-loop/src/constants.ts:6
//
// 上限管的是**并行池**：声明成独占的调用一律自己单独跑，不占这个额度，
// 见 [ExecuteToolCalls]。
const DefaultMaxParallelToolCalls = 10
