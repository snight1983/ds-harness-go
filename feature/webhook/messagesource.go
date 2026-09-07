// 本文件的作用：一条 webhook 放行进来的提示词，在日志里记的是从哪来的。
//
// 源: packages/webhook/webhook/src/types.ts:71-84

package webhook

import (
	"encoding/json"
	"fmt"

	"github.com/snight1983/ds-harness-go/llm"
)

// Plugin 是本包这条消息来源的 Plugin 名，取值就是 DSH 那个 kind。
//
// 源: packages/webhook/webhook/src/types.ts:75
//
// 新增: DSH 用 `declare module` 往 llm 那张 MessageSourceMap 上合并一个新 kind。
// Go 没有声明合并，本仓库给插件留的口子是
// [github.com/snight1983/ds-harness-go/llm.PluginSource]：kind 落成 Plugin 名，
// form 落成 [github.com/snight1983/ds-harness-go/llm.Context]，剩下四个自有字段编进
// Extra。成例见
// [github.com/snight1983/ds-harness-go/feature/subagent.NewCoordinatorSource]。
const Plugin = "webhook"

// Provenance 是本包那条来源上的四个自有字段在介质上的样子，字段名和 DSH 一致。
//
// 源: packages/webhook/webhook/src/types.ts:76-80
type Provenance struct {
	// Provider 是提供方族，取值是 [VerifiedDelivery.Kind]。
	Provider string `json:"provider"`
	// Source 是那个配好的适配器实例。
	Source SourceID `json:"source"`
	// DeliveryID 是提供方给这次投递的身份。
	DeliveryID DeliveryID `json:"deliveryId"`
	// RuleID 是放行这条消息的那条规则。
	RuleID RuleID `json:"ruleId"`
}

// NewSource 造一条 webhook 放行进来的提示词的归属。
//
// 源: packages/webhook/webhook/src/session.ts:156-165
//
// 形态是 notice：一次刚发生的外部事件的陈述，它不顶掉任何东西。那句一行陈述收进
// [github.com/snight1983/ds-harness-go/llm.ContextSummaryMaxChars]。
func NewSource(provenance Provenance, summary string) (llm.PluginSource, error) {
	if provenance.Provider == "" || provenance.Source == "" ||
		provenance.DeliveryID == "" || provenance.RuleID == "" {
		return llm.PluginSource{}, fmt.Errorf("%w：消息来源的四个出处字段都不能是空的", ErrInvalidDelivery)
	}
	extra, err := json.Marshal(provenance)
	if err != nil {
		// 走不到：这个结构只有四个字符串字段。照实转出去比断言它不会失败诚实。
		return llm.PluginSource{}, err
	}
	return llm.PluginSource{
		Plugin:  Plugin,
		Context: llm.NoticeContext{Summary: llm.BoundContextSummary(summary)},
		Extra:   extra,
	}, nil
}

// ProvenanceOf 读出一条消息来源上本包那四个字段。
//
// 第二个返回值为假表示这条来源不是本包发的。
//
// 新增: DSH 那边判别靠 `source.kind === 'webhook'` 再直接读字段，两半在同一个对象
// 上。Go 这边自有字段在 Extra 里，所以要解一次。
func ProvenanceOf(source llm.MessageSource) (Provenance, bool, error) {
	plugin, ok := source.(llm.PluginSource)
	if !ok || plugin.Plugin != Plugin {
		return Provenance{}, false, nil
	}
	var provenance Provenance
	if err := json.Unmarshal(plugin.Extra, &provenance); err != nil {
		return Provenance{}, false, fmt.Errorf("webhook: 消息来源的负载读不出来：%w", err)
	}
	return provenance, true, nil
}
