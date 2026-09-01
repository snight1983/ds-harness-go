// 本文件的作用：把一个来源会话的当前表面投影成纯对话，再把它压进一个精确的
// 字节预算里。
//
// 源: packages/context/session-reference/src/projection.ts:1-172

package sessionref

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/snight1983/ds-harness-go/compaction"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/sessionquery"
	"github.com/snight1983/ds-harness-go/util/outputretention"
)

// projectedItem 是投影出来的一条对话行，外加裁剪要用的那几个记账字段。
//
// 源: packages/context/session-reference/src/projection.ts:10-14
type projectedItem struct {
	// Role 是这条消息的角色，只有 user 和 assistant 两种。
	Role llm.Role
	// Text 是当前留下的文本，可能已经被截断过。
	Text string
	// checkpoint 表示这条是压缩检查点，第一轮丢弃永远绕开它。
	checkpoint bool
	// originalText 是没被动过的原文；每次截断都从它出发，而不是从上一次的结果，
	// 否则反复截断会把通知语句本身也截掉一半。
	originalText string
	// omittedBytes 是这条上因为截断丢掉的 UTF-8 字节数。
	omittedBytes int
}

// ReferencedSessionData 是排进那段不可信提示词里的快照数据。
//
// 源: packages/context/session-reference/src/projection.ts:16-23
type ReferencedSessionData struct {
	// SessionID 是被引用的那个来源会话。
	SessionID string
	// Label 是主机给的显示名。
	Label string
	// Cwd 是那个会话的工作目录；空串表示日志里没记，排出去是 null。
	Cwd string
	// CapturedThroughSeq 是这次观察吃进的最大原始日志 seq。
	CapturedThroughSeq int
	// CapturedAny 为假表示观察到的是一份空日志，排出去是 null。
	//
	// 新增: 和 [Reference.CapturedThroughSeq] 同一个理由——seq 0 是合法序号。
	CapturedAny bool
	// Conversation 是投影并裁剪之后留下的那些对话行。
	Conversation []ConversationItem
}

// referencedSessionDataJSON 是 [ReferencedSessionData] 落到线上的形状，
// 字段名与顺序都和 DSH 逐字相同。
//
// 顺序要紧：它决定排出来的字节，而本包的预算就是按那份字节数算的。
type referencedSessionDataJSON struct {
	SessionID          string             `json:"sessionId"`
	Label              string             `json:"label"`
	Cwd                *string            `json:"cwd"`
	CapturedThroughSeq *int               `json:"capturedThroughSeq"`
	Conversation       []ConversationItem `json:"conversation"`
}

// MarshalJSON 把这份数据排成 DSH 那份形状。
//
// 没有配套的 UnmarshalJSON：这份数据只往提示词里去，从来没有人读它回来。
func (d ReferencedSessionData) MarshalJSON() ([]byte, error) {
	wire := referencedSessionDataJSON{
		SessionID:    d.SessionID,
		Label:        d.Label,
		Conversation: d.Conversation,
	}
	if d.Cwd != "" {
		cwd := d.Cwd
		wire.Cwd = &cwd
	}
	if d.CapturedAny {
		seq := d.CapturedThroughSeq
		wire.CapturedThroughSeq = &seq
	}
	if wire.Conversation == nil {
		// conversation 在 DSH 那边是必填数组。排成 null 的话，模型看见的
		// 「这个会话一句话都没有」和「这个字段坏了」就长得一样了。
		wire.Conversation = []ConversationItem{}
	}
	return json.Marshal(wire)
}

// ReferenceRetentionStats 是裁剪的记账，落在持久来源里。
//
// 源: packages/context/session-reference/src/projection.ts:25-33
type ReferenceRetentionStats struct {
	// Compacted 表示那份日志里出现过压缩检查点。
	Compacted bool
	// OriginalMessages 是投影出来、还没裁之前有多少条。
	OriginalMessages int
	// RetainedMessages 是裁完之后还剩多少条。
	RetainedMessages int
	// OmittedMessages 是被整条丢掉了多少条。
	OmittedMessages int
	// OmittedBytes 是丢掉的和截断掉的加起来有多少 UTF-8 字节。
	OmittedBytes int
	// Truncated 表示这次引用不是原样进去的。
	Truncated bool
}

// RetainedReference 是一次成功的裁剪结果：排出去的数据，加上它的记账。
//
// 新增: DSH 返回一个匿名对象 `{ data, stats }`。Go 里给它一个名字，好让
// [RetainReferencedSession] 的「裁不下」用一个干净的布尔表达，
// 而不是三个零值挤在返回列表里。
type RetainedReference struct {
	// Data 是排进提示词的那份快照数据。
	Data ReferencedSessionData
	// Stats 是这次裁剪的记账。
	Stats ReferenceRetentionStats
}

// projectSessionConversation 投影出当前的用户／助手对话，把工具、推理和注入的
// 上下文都排除在外。
//
// 源: packages/context/session-reference/src/projection.ts:35-60
//
// 用户消息只留两种：压缩检查点，和用户**自己**说的话。别的用户角色消息都是各层
// 注入的上下文（工作区说明、时间、别的会话的引用），把它们抄进另一个会话的提示词
// 既没有信息量，又会让注入内容跨会话叠罗汉。
//
// 新增: DSH 那边表面事件是一个封闭联合，穷尽 switch 之后靠 assertNever 收尾，
// 所以它不会失败。Go 这边事件负载是 json.RawMessage，得解码，而解码是会失败的——
// 一份坏掉的日志必须说出来，不能压成「这条没有可见文本」。
func projectSessionConversation(snapshot sessionquery.SurfaceSnapshot) ([]projectedItem, error) {
	var conversation []projectedItem
	for _, event := range snapshot.Events {
		data, err := session.DecodeData(event)
		if err != nil {
			return nil, wrap(CodeReadFailed, err, "会话引用：来源会话 %q 的 seq %d 负载读不回来",
				snapshot.Session.ID, event.Seq)
		}
		switch payload := data.(type) {
		case session.UserMessageData:
			checkpoint := compaction.IsCheckpointSource(payload.Source)
			if !checkpoint && payload.Source.SourceKind() != llm.SourceUser {
				continue
			}
			if text := contentText(payload.Content); text != "" {
				conversation = append(conversation, projectedItem{
					Role: llm.RoleUser, Text: text, checkpoint: checkpoint, originalText: text,
				})
			}
		case session.AssistantMessageData:
			if text := contentText(payload.Message.Content); text != "" {
				conversation = append(conversation, projectedItem{
					Role: llm.RoleAssistant, Text: text, originalText: text,
				})
			}
		case session.ToolResultData:
			// 工具结果不进引用：它们又长又是给机器看的，而引用要的是这次会话
			// 「谈了什么」。
		default:
			// 表面上只可能有那三种事件，见 [session.IsSurfaceEligibleType]。
			// 多出来的一种说明喂进来的不是一份折好的表面，那是调用方的错，
			// 悄悄跳过会让引用内容凭空少一截。
			return nil, fail(CodeReadFailed, "会话引用：来源会话 %q 的 seq %d 是 %s，不该出现在表面上",
				snapshot.Session.ID, event.Seq, event.Type)
		}
	}
	return conversation, nil
}

// RetainReferencedSession 把一份投影好的快照压进精确的字节上限。
//
// 源: packages/context/session-reference/src/projection.ts:62-138
//
// 第二个返回值为假表示怎么裁都塞不进去——那时连固定字段（会话 id、标签、目录）
// 都超了预算，删对话已经没有意义。
//
// 两轮是有先后的，顺序本身就是策略：先整条整条地丢掉旧消息，实在丢不动了
// （只剩检查点和最新一条）才去截断最长的那条。反过来做的话，一段还完整的旧对话
// 会先被切得七零八落，而读的人宁可看见「少了几条」也不想看见「每条都缺一块」。
func RetainReferencedSession(snapshot sessionquery.SurfaceSnapshot, label string, maxBytes int) (RetainedReference, bool, error) {
	original, err := projectSessionConversation(snapshot)
	if err != nil {
		return RetainedReference{}, false, err
	}
	retained := append([]projectedItem(nil), original...)
	omittedMessages := 0
	droppedOmittedBytes := 0

	data := func() ReferencedSessionData {
		conversation := make([]ConversationItem, len(retained))
		for index, item := range retained {
			conversation[index] = ConversationItem{Role: item.Role, Text: item.Text}
		}
		return ReferencedSessionData{
			SessionID:          string(snapshot.Session.ID),
			Label:              label,
			Cwd:                snapshot.Session.Cwd,
			CapturedThroughSeq: snapshot.CapturedThroughSeq,
			CapturedAny:        snapshot.CapturedAny,
			Conversation:       conversation,
		}
	}
	size := func() (int, error) {
		rendered, err := stringifyTagSafeJSON(data())
		if err != nil {
			return 0, err
		}
		return len(rendered), nil
	}

	current, err := size()
	if err != nil {
		return RetainedReference{}, false, err
	}

	// 第一轮：整条丢。永远跳过检查点（丢了它，读的人会以为这就是全部历史）
	// 和最新那一条（丢了它，引用就答不出「刚才在说什么」）。
	for current > maxBytes {
		newest := len(retained) - 1
		dropIndex := -1
		for index, item := range retained {
			if !item.checkpoint && index != newest {
				dropIndex = index
				break
			}
		}
		if dropIndex < 0 {
			break
		}
		omittedMessages++
		droppedOmittedBytes += len(retained[dropIndex].originalText)
		retained = append(retained[:dropIndex], retained[dropIndex+1:]...)
		if current, err = size(); err != nil {
			return RetainedReference{}, false, err
		}
	}

	// 第二轮：截最长的那条。每次只把它截到「刚好抵掉溢出」那么多，
	// 于是超得少就切得少，而不是一上来把最长的一条腰斩。
	for current > maxBytes {
		longestIndex, longestBytes := -1, 0
		for index, item := range retained {
			if length := len(item.Text); length > longestBytes {
				longestIndex, longestBytes = index, length
			}
		}
		if longestIndex < 0 || longestBytes == 0 {
			return RetainedReference{}, false, nil
		}
		target := max(0, longestBytes-(current-maxBytes))
		shortened, omitted, err := truncateWithNotice(retained[longestIndex].originalText, target)
		if err != nil {
			return RetainedReference{}, false, err
		}
		if shortened == retained[longestIndex].Text {
			// 目标字节数严格变小了，结果却没变——再转一圈也只会得到同一个答案。
			return RetainedReference{}, false, nil
		}
		retained[longestIndex].Text = shortened
		retained[longestIndex].omittedBytes = omitted
		if current, err = size(); err != nil {
			return RetainedReference{}, false, err
		}
	}

	compacted := false
	truncatedBytes := 0
	for _, item := range original {
		if item.checkpoint {
			compacted = true
		}
	}
	for _, item := range retained {
		truncatedBytes += item.omittedBytes
	}
	omittedBytes := truncatedBytes + droppedOmittedBytes
	return RetainedReference{
		Data: data(),
		Stats: ReferenceRetentionStats{
			Compacted:        compacted,
			OriginalMessages: len(original),
			RetainedMessages: len(retained),
			OmittedMessages:  omittedMessages,
			OmittedBytes:     omittedBytes,
			Truncated:        omittedMessages > 0 || omittedBytes > 0,
		},
	}, true, nil
}

// contentText 把一串内容里的可见文本拼起来，别的块一概不要。
//
// 源: packages/context/session-reference/src/projection.ts:140-142
//
// 推理块被排除在外不是省字节：那是模型的草稿，跨会话抄过去只会让另一个模型
// 把别人的中间猜测当成结论。
func contentText(content llm.Content) string {
	var parts []string
	for _, block := range content {
		if text, ok := block.(llm.TextBlock); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// omissionNotice 是截断处那句告诉模型「这里少了多少」的话。
//
// 源: packages/context/session-reference/src/projection.ts:163
//
// 它是给模型看的，所以是英文。
func omissionNotice(omitted int) string {
	return fmt.Sprintf("\n[… omitted %d UTF-8 bytes …]", omitted)
}

// truncateWithNotice 把一段文本连同那句省略通知一起压进 maxOutputBytes。
//
// 源: packages/context/session-reference/src/projection.ts:144-172
//
// 为什么要二分：通知语句本身占字节，而它占多少取决于省略了多少，省略多少又取决于
// 留了多少——三者互相咬着。二分找的是「加上通知之后仍然不超预算」的最大留存量，
// 于是最终那串字节是恰好塞满的，不是估出来的。
//
// 掐头去尾（而不是只留开头）也是有意的：一段对话的结尾往往是结论，只留开头
// 会让引用停在半路上。
func truncateWithNotice(text string, maxOutputBytes int) (string, int, error) {
	if len(text) <= maxOutputBytes {
		return text, 0, nil
	}
	low, high := 0, maxOutputBytes
	bestText, bestOmitted := "", len(text)
	for low <= high {
		retainedBytes := (low + high) / 2
		// 奇数时头比尾多一个字节，和 DSH 的 ceil / floor 一致。
		headBytes := (retainedBytes + 1) / 2
		tailBytes := retainedBytes / 2
		retainer, err := outputretention.NewTextRetainer(outputretention.TextHeadTail(headBytes, tailBytes))
		if err != nil {
			return "", 0, wrap(CodeBudgetExceeded, err, "会话引用：造不出留存器")
		}
		retainer.PushString(text)
		result := retainer.Finish()
		omitted, exact := result.OmittedBytes.Count()
		if !exact {
			// 整段文本是一次性推进去的，留存器数得出每一个字节。
			return "", 0, fail(CodeBudgetExceeded, "会话引用：留存器没有给出精确的丢弃字节数")
		}
		if candidate := result.Text + omissionNotice(omitted); len(candidate) <= maxOutputBytes {
			bestText, bestOmitted = candidate, omitted
			low = retainedBytes + 1
		} else {
			high = retainedBytes - 1
		}
	}
	return bestText, bestOmitted, nil
}
