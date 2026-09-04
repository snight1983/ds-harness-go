// 本文件的作用：两个纯粹的读日志函数——挑出哪些人类消息够格当标题素材，
// 以及把日志上最新那条标题折出来。
//
// 它们不碰服务、不碰锁、不追加任何东西，所以一份**回放出来的**（已经不活了的）
// 日志照样能用它们答话。这正是标题这个域的立场：日志是唯一的真相，服务只是
// 一个往日志里写的东西。
//
// 源: packages/session/session-title/src/index.ts:167-201

package sessiontitle

import (
	"encoding/json"
	"strings"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// CollectMessages 按日志顺序挑出够格当标题素材的人类文本消息。
//
// 源: packages/session/session-title/src/index.ts:237-257（collectSessionTitleMessages）
//
// 三道筛子：
//
//   - 只要 user/message 这一种事件；
//   - 那条消息的来源必须是**用户自己**（[llm.SourceUser]）。插件注入的、工具
//     回填的、压缩摘要塞进来的消息都是 user 角色，但它们不是人打的字，
//     拿它们起名会得到一堆一模一样的系统文案；
//   - 它的文本块拼起来洗完之后不能是空的。一条只有图片附件的消息、或者一条
//     只有转义序列的消息都进不来。
//
// throughSeq 是一道**含端点**的边界：只看 seq 不超过它的事件。传负数表示不设
// 边界。它存在的理由是自动生成要钉死自己那次的输入范围——生成期间日志还在长，
// 没有这道边界的话，交给生成器的消息和最后记进 MessageSeqs 的会对不上。
//
// 新增: DSH 那边判「洗完是不是空」用的是
// normalizeSessionTitle(text, Number.MAX_SAFE_INTEGER).length === 0——拿一个
// 天文数字当「不设上限」。这里直接调 [cleanTitleText]：它就是那句话真正想问的
// 东西，而且不用为了一次「有没有可见字符」的判断去走一遍截断。
func CollectMessages(events []sessionlog.Event, throughSeq int) []UserMessage {
	var messages []UserMessage
	for _, event := range events {
		if throughSeq >= 0 && event.Seq > throughSeq {
			break
		}
		text, eligible := titleTextOf(event)
		if !eligible {
			continue
		}
		messages = append(messages, UserMessage{Seq: event.Seq, Text: text})
	}
	return messages
}

// titleTextOf 取出一条事件够格当标题素材的那段文本。
//
// 第二个返回值为假表示这条事件根本不该进 [CollectMessages]。
func titleTextOf(event sessionlog.Event) (string, bool) {
	if event.Type != sessionlog.EventUserMessage {
		return "", false
	}
	var data sessionlog.UserMessageData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		// 新增: DSH 那边负载是解好的，读不回来这件事不存在。Go 这边把它咽掉
		// 而不是报错：这个函数是一道**筛子**，一条读不回来的用户消息在
		// 「能不能拿来起名」这个问题上的答案就是「不能」。让它报错会把整条
		// 读链变成可失败的，而唯一的处理方式还是跳过这条。
		return "", false
	}
	if data.Source == nil || data.Source.SourceKind() != llm.SourceUser {
		return "", false
	}

	var parts []string
	for _, block := range data.Content {
		if text, isText := block.(llm.TextBlock); isText {
			parts = append(parts, text.Text)
		}
	}
	// 用换行连接：DSH 那边是 join('\n')。多个文本块在原始输入里本来就是分段的，
	// 拿空串粘会把两段的末尾和开头黏成一个词。反正下一步的洗白会把换行折成
	// 一个空格，这里只是不许它们黏起来。
	text := strings.Join(parts, "\n")
	if cleanTitleText(text) == "" {
		return "", false
	}
	return text, true
}

// FoldSnapshot 折出日志上最新那条标题，不去看任何可变的旁路元数据。
//
// 源: packages/session/session-title/src/index.ts:276-291（foldSessionTitle）
//
// 从后往前找第一条 [EventSessionTitle]：标题是 last-wins 的，最新那条就是答案。
// 第二个返回值为假表示这个会话还没有过标题。
//
// 新增: DSH 的 foldSessionTitle 不会失败——那边负载是解好的。Go 这边负载是原始
// 字节，所以多一个错误：一条读不回来的标题事件必须让整次折叠失败，而不是被跳过
// 去露出**更早**那个标题。露出旧标题是最坏的一种降级：界面上会显示一个看起来
// 完全正常、但其实已经被改过的名字，没有任何迹象说它是错的。
func FoldSnapshot(events []sessionlog.Event) (Snapshot, bool, error) {
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if event.Type != EventSessionTitle {
			continue
		}
		data, err := decodeEventData(event)
		if err != nil {
			return Snapshot{}, false, err
		}
		// 复制一份再交出去：调用方改动它不该反过来改到别人手上那份快照。
		// DSH 那边这件事是 deepFreeze + 展开，理由一样。
		return Snapshot{
			EventData: EventData{
				Title:       data.Title,
				MessageSeqs: append([]int(nil), data.MessageSeqs...),
				Source:      data.Source.Clone(),
			},
			EventSeq:  event.Seq,
			UpdatedAt: event.Time,
		}, true, nil
	}
	return Snapshot{}, false, nil
}
