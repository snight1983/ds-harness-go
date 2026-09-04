// 本文件的作用：把那份派发策略写进孩子日志的两件事——直接追加，和在还没公布的
// 创建种子上排演一次追加。
//
// 源: packages/subagent/subagent/src/child-agent.ts:249-268

// Package childseed 把「派发策略怎么落到孩子那条日志上」这件事收成一份。
//
// 需要它的有两处：[github.com/snight1983/ds-harness-go/feature/subagent] 里那条续行
// 激活路，和 [github.com/snight1983/ds-harness-go/feature/subagent/inprocessdriver] 那台
// 进程内驱动。两处都在 feature/subagent 这棵子树里面，子树外面一个都没有，所以
// 它摆在 internal/ 底下：共用不必换成公开面。
//
// 新增: 这段判断原先在上面那两个包里各写了一遍，两份逐字相同。当时不共用的理由是
// 「导出它就等于把一个实现细节的补丁当成契约」——收进 internal/ 之后那条理由不
// 成立了，因为这里根本不构成对外的公开面。
package childseed

import (
	"encoding/json"
	"fmt"

	"github.com/snight1983/ds-harness-go/feature/interaction/userapproval"
	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// AppendPolicy 把这次派发钉下来的审批策略以 `source: delegation` 追加到孩子
// **自己**那条日志上，落在还没公布的创建窗口里，于是这个孩子的有效策略单看它
// 自己的日志就重建得出来。这一笔落在分叉种子之后，所以新策略压过陈旧的种子状态；
// 孩子后来自己那些切换仍旧压过它。
//
// 源: packages/subagent/subagent/src/child-agent.ts:249-268（appendDelegatedPolicyOverrides）
//
// policy 是空串表示这次派发一条都不种。
//
// 新增: DSH 调的是 `childSession.append(type, data)`。Go 的
// [github.com/snight1983/ds-harness-go/harness/session.Session.Append] 收的是一条 Data 已经是
// json.RawMessage 的事件，所以负载在这里先编一次。
// [github.com/snight1983/ds-harness-go/feature/interaction/userapproval.SetPolicy] 那条自由函数用不上：
// 它写的 PolicyData 不带 Source，而 `delegation` 这个来源正是这一笔和一次运行期
// 切换的唯一区别。
func AppendPolicy(childSession *coresession.Session, policy userapproval.Policy) error {
	if policy == "" {
		return nil
	}
	data, err := json.Marshal(userapproval.PolicyData{
		Policy: policy,
		Source: userapproval.PolicySourceDelegation,
	})
	if err != nil {
		// 走不到：PolicyData 两个字段都是字符串。照实转出去比断言它不会失败诚实。
		return err
	}
	_, err = childSession.Append(sessionlog.Event{Type: userapproval.EventPolicy, Data: data})
	return err
}

// Seed 把那份派发策略折进孩子的创建种子；策略为空时原样交回。
//
// 源: packages/subagent/subagent/src/continuation.ts:1058-1063
//
// 只有全新创建才种（分叉种子之后，于是新策略压过陈旧的种子状态）；一次冷恢复
// 回放的是那几条已经落盘的事件。
//
// 新增: DSH 是在 setup 回调里拿 `childCtx.agent.session` 现场追加的。Go 的
// [github.com/snight1983/ds-harness-go/harness/agent.Setup] 只收作用域，而那一刻会话还没登记进
// [github.com/snight1983/ds-harness-go/harness/session.Store]（见 [github.com/snight1983/ds-harness-go/harness/agentloop.AgentLoop]
// 的 setupAndPublish：Prepare 出来的会话到 publish 才登记），所以这里够不着那份
// 会话。改成在种子上排演一次——和
// [github.com/snight1983/ds-harness-go/feature/subagent.SeedDescriptorTurn] 完全同一条路子：那几条
// 事件照样落在 SeedLength 边界**之后**，因此仍旧是这个孩子自己的历史，而且照样在
// 公布之前就定死了。
func Seed(
	childID sessionlog.SessionID,
	seed []sessionlog.Event,
	baseSeq int,
	policy userapproval.Policy,
) ([]sessionlog.Event, error) {
	if policy == "" {
		return seed, nil
	}
	// baseSeq 要跟着传：这场排演走的是真会话那道 seed 校验，而它核的是每一条的
	// seq 都等于 baseSeq + 下标。父日志被弹过头时这段种子不从 0 起，漏掉这个数
	// 会让排演当场以「seed 必须从 0 起连续」失败。
	staged, err := coresession.NewSession(childID, coresession.Options{Seed: seed, BaseSeq: baseSeq})
	if err != nil {
		return nil, fmt.Errorf("排演子 agent 派发策略种子失败：%w", err)
	}
	if err := AppendPolicy(staged, policy); err != nil {
		// 走不到：那份负载是两个字符串转出来的 JSON，而这次追加落在一个刚排演
		// 出来的游离会话上，没有别的边界会拒它。
		return nil, fmt.Errorf("追加子 agent 派发策略失败：%w", err)
	}
	return staged.Events(), nil
}
