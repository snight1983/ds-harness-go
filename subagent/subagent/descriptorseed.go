// 本文件的作用：给一个可续孩子的耐久描述符事件做种——在它第一次请求之前，
// 把这个孩子那份声明出来的组装记成一条不给模型看的记录，好让日后一次冷恢复
// 只读它自己的日志就能把它重建出来。
//
// 源: packages/subagent/subagent/src/descriptor-seed.ts

package subagent

import (
	"encoding/json"
	"fmt"

	coresession "ds-harness-go/core/session"
	"ds-harness-go/session"
)

// SeedDescriptorTurn 拼出孩子的创建种子：继承来的那段父历史前缀，后面跟**一条**
// 不给模型看的、回合之间的描述符事件。
//
// 源: packages/subagent/subagent/src/descriptor-seed.ts:22-30
//
// 借道一个游离的 [ds-harness-go/core/session.Session] 排演，是为了让序号由会话
// 自己盖上，并且落进耐久日志同一套无损 JSON 规矩。交回的事件从 seq 0 起连续。
//
// seed 为 nil 表示这是一个全新的孩子（**nil 和空切片不是一回事**，见
// [ds-harness-go/core/session.Options.Seed]）。
func SeedDescriptorTurn(
	childID session.SessionID,
	seed []session.Event,
	descriptor DescriptorData,
) ([]session.Event, error) {
	staged, err := coresession.NewSession(childID, coresession.Options{Seed: seed})
	if err != nil {
		return nil, fmt.Errorf("排演子 agent 描述符种子失败：%w", err)
	}
	data, err := json.Marshal(descriptor)
	if err != nil {
		return nil, fmt.Errorf("%w：描述符排不成无损 JSON：%w", ErrInvalidRequest, err)
	}
	if _, err := staged.Append(session.Event{
		Type: EventDescriptor,
		Data: data,
	}); err != nil {
		return nil, fmt.Errorf("追加子 agent 描述符失败：%w", err)
	}
	return staged.Events(), nil
}
