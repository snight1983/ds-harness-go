// 本文件的作用：续接这条线上那三种消息归属——一个模型协调方对自己孩子的后续、
// 一个可续孩子对父的显式汇报，以及运行时自己对「这个孩子怎么完的」那份陈述。
//
// 源: packages/subagent/subagent/src/continuation.ts:56-97
//
// 新增: DSH 用 `declare module` 往 llm 那张 MessageSourceMap 上合并三个新 kind，
// 于是它们和内建来源共用一个可判别联合。Go 没有声明合并，本仓库给插件留的口子是
// [github.com/snight1983/ds-harness-go/llm.PluginSource]：kind 落成 Plugin 名，form 落成
// [github.com/snight1983/ds-harness-go/llm.Context]，剩下的自有字段编进 Extra。
// 成例见 [github.com/snight1983/ds-harness-go/compaction.NewCheckpointSource]。

package subagent

import (
	"encoding/json"
	"fmt"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
)

const (
	// CoordinatorPlugin 是一个模型协调方后续消息的 Plugin 名，取值就是 DSH 那个 kind。
	//
	// 源: packages/subagent/subagent/src/continuation.ts:59
	CoordinatorPlugin = "coordinator"
	// ReportPlugin 是一个可续孩子显式汇报的 Plugin 名。
	//
	// 源: packages/subagent/subagent/src/continuation.ts:67
	ReportPlugin = "subagent-report"
	// SettledPlugin 是运行时自己那份结清陈述的 Plugin 名。
	//
	// 源: packages/subagent/subagent/src/continuation.ts:83
	//
	// **有意**和 [ReportPlugin] 分成两个名字：汇报是孩子自己选的内容，而这一条是
	// 管理器在陈述这个孩子的下场；把两者并成一个来源，会让一段抄本把孩子从没写过
	// 的话记在它头上。
	SettledPlugin = "subagent-settled"
)

// senderExtra 是这三种来源共有的那一项在介质上的样子，字段名和 DSH 一致。
//
// 源: packages/subagent/subagent/src/continuation.ts:62, 70, 88
type senderExtra struct {
	SenderSessionID session.SessionID `json:"senderSessionId"`
}

// marshalSenderExtra 把发送方那一项编成 [github.com/snight1983/ds-harness-go/llm.PluginSource] 的 Extra。
func marshalSenderExtra(senderSessionID session.SessionID) (json.RawMessage, error) {
	if senderSessionID == "" {
		return nil, fmt.Errorf("%w：消息来源缺发送方会话 id", ErrInvalidRequest)
	}
	extra, err := json.Marshal(senderExtra{SenderSessionID: senderSessionID})
	if err != nil {
		// 走不到：这个结构只有一个字符串字段。照实转出去比断言它不会失败诚实。
		return nil, err
	}
	return extra, nil
}

// NewCoordinatorSource 造一个模型协调方后续消息的归属。
//
// 源: packages/subagent/subagent/src/continuation.ts:58-65（CoordinatorMessageSource）
//
// 形态是 relay：这是另一个 agent 对本 agent 说的话。
func NewCoordinatorSource(senderSessionID session.SessionID) (llm.PluginSource, error) {
	extra, err := marshalSenderExtra(senderSessionID)
	if err != nil {
		return llm.PluginSource{}, err
	}
	return llm.PluginSource{Plugin: CoordinatorPlugin, Context: llm.RelayContext{}, Extra: extra}, nil
}

// NewReportSource 造一个可续孩子显式汇报的耐久归属。
//
// 源: packages/subagent/subagent/src/continuation.ts:67-74（SubagentReportMessageSource）
func NewReportSource(senderSessionID session.SessionID) (llm.PluginSource, error) {
	extra, err := marshalSenderExtra(senderSessionID)
	if err != nil {
		return llm.PluginSource{}, err
	}
	return llm.PluginSource{Plugin: ReportPlugin, Context: llm.RelayContext{}, Extra: extra}, nil
}

// NewSettledSource 造运行时那份「孩子结清了」陈述的耐久归属。
//
// 源: packages/subagent/subagent/src/continuation.ts:76-91（SubagentSettledMessageSource）
//
// 形态是 notice：一份不展开行就看得见的运行时交代，那句一行陈述收进
// [github.com/snight1983/ds-harness-go/llm.ContextSummaryMaxChars]。
func NewSettledSource(senderSessionID session.SessionID, summary string) (llm.PluginSource, error) {
	extra, err := marshalSenderExtra(senderSessionID)
	if err != nil {
		return llm.PluginSource{}, err
	}
	return llm.PluginSource{
		Plugin:  SettledPlugin,
		Context: llm.NoticeContext{Summary: llm.BoundContextSummary(summary)},
		Extra:   extra,
	}, nil
}

// SenderSessionIDOf 读出这三种来源共有的那个发送方会话 id。
//
// 第二个返回值为假表示这个来源不是本包这三种里的任何一种。
//
// 新增: DSH 那边判别靠 `source.kind === 'subagent-report'` 再直接读字段，
// 两半在同一个对象上。Go 这边自有字段在 Extra 里，所以要解一次。
func SenderSessionIDOf(source llm.MessageSource) (session.SessionID, bool, error) {
	plugin, ok := source.(llm.PluginSource)
	if !ok {
		return "", false, nil
	}
	switch plugin.Plugin {
	case CoordinatorPlugin, ReportPlugin, SettledPlugin:
	default:
		return "", false, nil
	}
	var extra senderExtra
	if err := json.Unmarshal(plugin.Extra, &extra); err != nil {
		return "", false, fmt.Errorf("subagent: 消息来源 %q 的负载读不出来：%w", plugin.Plugin, err)
	}
	return extra.SenderSessionID, true, nil
}
