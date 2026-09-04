// 本文件的作用：给一个可续孩子的耐久描述符事件做种——在它第一次请求之前，
// 把这个孩子那份声明出来的组装记成一条不给模型看的记录，好让日后一次冷恢复
// 只读它自己的日志就能把它重建出来。
//
// 源: packages/subagent/subagent/src/descriptor-seed.ts

package subagent

import (
	"encoding/json"
	"fmt"

	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// SeedDescriptorTurn 拼出孩子的创建种子：继承来的那段父历史前缀，后面跟**一条**
// 不给模型看的、回合之间的描述符事件。
//
// 源: packages/subagent/subagent/src/descriptor-seed.ts:22-30
//
// 借道一个游离的 [github.com/snight1983/ds-harness-go/harness/session.Session] 排演，是为了让序号由会话
// 自己盖上，并且落进耐久日志同一套无损 JSON 规矩。交回的事件从 baseSeq 起连续。
//
// seed 为 nil 表示这是一个全新的孩子（**nil 和空切片不是一回事**，见
// [github.com/snight1983/ds-harness-go/harness/session.Options.Seed]）。
//
// 新增: baseSeq 是 seed 第一条应有的 seq，理由见
// [github.com/snight1983/ds-harness-go/harness/agent.CreateOptions.BaseSeq]。
// 这场排演走的是真会话那道 seed 校验，它核的是每一条的 seq 都等于 baseSeq + 下标；
// 父日志被弹过头时这段前缀不从 0 起，漏掉这个数会让排演当场失败。
func SeedDescriptorTurn(
	childID sessionlog.SessionID,
	seed []sessionlog.Event,
	baseSeq int,
	descriptor DescriptorData,
) ([]sessionlog.Event, error) {
	staged, err := coresession.NewSession(childID, coresession.Options{Seed: seed, BaseSeq: baseSeq})
	if err != nil {
		return nil, fmt.Errorf("排演子 agent 描述符种子失败：%w", err)
	}
	// 走不到：描述符只有整数、字符串和一个 *tools.Restriction，排得出来。
	data, err := json.Marshal(descriptor)
	if err != nil {
		return nil, fmt.Errorf("%w：描述符排不成无损 JSON：%w", ErrInvalidRequest, err)
	}
	// 走不到：负载是刚转出来的合法 JSON，而这次追加落在一个刚排演出来的游离会话上，
	// 没有别的边界会拒它。
	if _, err := staged.Append(sessionlog.Event{
		Type: EventDescriptor,
		Data: data,
	}); err != nil {
		return nil, fmt.Errorf("追加子 agent 描述符失败：%w", err)
	}
	return staged.Events(), nil
}
