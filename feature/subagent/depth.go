// 本文件的作用：派发深度这本账——一个父 agent 传给孩子的那份递归预算。
//
// 源: packages/subagent/subagent/src/depth.ts
//
// 它和服务分开，好让组装用的那些辅助读得到它而不必把注册表拽进来。

package subagent

import (
	"fmt"

	"github.com/snight1983/ds-harness-go/harness/agent"
)

// DelegationDepthOf 读一个 agent 的派发深度，顶层是 0。
//
// 源: packages/subagent/subagent/src/depth.ts:26-36
//
// 新增: DSH 取的是「持久的头」和「运行期 AgentOptions.subagentDepth」两者的较大值，
// 并且专门写明「头是权威且单调的：运行期只许**加深**，绝不许调低」——因为它那个
// AgentOptions 是一个可声明合并的对象，depth.ts 自己往上贴了一个可选字段，而一个
// 冷恢复回来的孩子拿到的是一份全新的选项，从零数起就会让它像顶层一样继续派发。
//
// Go 这边 [github.com/snight1983/ds-harness-go/harness/agent.Options] 是个闭合结构体，贴不上这个字段，
// 而它的值本来就从 [github.com/snight1983/ds-harness-go/harness/agent.CreateOptions.DelegationDepth] 写进
// 会话头。于是那个「取较大值」的动作没有第二个输入可取，头就是唯一的事实，
// 上面那条单调性由「只有一处写得进去」直接兑现，而不是靠每次读的时候比一下。
func DelegationDepthOf(a agent.Agent) int {
	return a.Session().Header().DelegationDepth
}

// AssertMaxDepth 拒掉一个表达不了确切派发深度的递归上限。maxDepth 为 nil 表示不设。
//
// 源: packages/subagent/subagent/src/depth.ts:42-51
//
// 新增: DSH 要验四件事——是不是 number、是不是安全整数、是不是负数、是不是 -0。
// Go 的 *int 把前两件事和「不设上限」都交给了类型，-0 在 int 上不存在，
// 只剩「不许是负数」这一条真的要在运行期验。
func AssertMaxDepth(maxDepth *int) error {
	if maxDepth != nil && *maxDepth < 0 {
		return fmt.Errorf("%w：子 agent 的 maxDepth 不许是负数，实际 %d", ErrInvalidRequest, *maxDepth)
	}
	return nil
}
