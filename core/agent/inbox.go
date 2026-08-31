// 本文件的作用：一个 agent 那两条待办清单的投影——它怎么从日志里重建出来，
// 以及每一次改动怎么先落进日志再动投影。
//
// 源: packages/core/agent/src/inbox.ts

package agent

import (
	"encoding/json"
	"fmt"
	"slices"

	"ds-harness-go/core/session"
	"ds-harness-go/llm"
	sessionlog "ds-harness-go/session"
)

// InboxNotifications 是收件箱每次改动之后往外报的那三件事。
//
// 源: packages/core/agent/src/inbox.ts:14-22
//
// 三个回调都可以是 nil，那表示这次投影不往外报——回放和诊断走的就是这条。
//
// 新增: DSH 这三个方法的实现是 [Registry] 那三条 emit 的转发。Go 里 [Inbox]
// 不认识注册表（它是一份纯投影），所以这三件事由构造它的那一方接线。
type InboxNotifications struct {
	// Inserted 报一条刚进来的消息。
	Inserted func(message llm.Message)
	// Discarded 报一条被丢掉的消息。
	Discarded func(message llm.Message)
	// Claimed 报一条在它所属回合里被认领走的消息。
	Claimed func(message llm.Message, turn int)
}

// Inbox 是一份「重放一次、之后增量消费」的待办清单投影。
//
// 源: packages/core/agent/src/inbox.ts:24-25
//
// 耐久的事实永远是会话日志里那些 [EventInboxSpliced]；这里这两条切片是它们
// 折出来的当下形状。[NewInbox] 重放一次，之后每一次改动都由本类型自己既写日志
// 又动投影，所以两者不可能分家。
//
// 不加锁，见包文档：它只该被自己那个 agent 的循环碰。
type Inbox struct {
	session *session.Session
	notify  InboxNotifications

	// nextTurn 与 nextStep 是那两条清单。
	//
	// 新增: DSH 是 `Record<InboxTarget, UserMessage[]>` 一张按名字取的表。Go 里
	// 两个具名字段更实在：清单名是一个封闭的二元集合，一张 map 只会多出「取到
	// 一个不存在的键」这条路，而 [listOf] 已经把按名字取那件事收成了一处。
	nextTurn []llm.Message
	nextStep []llm.Message
}

// NewInbox 从一个会话的日志里重建出它的收件箱投影。
//
// 源: packages/core/agent/src/inbox.ts:28-40
//
// 只重放 seed 边界**之后**的那一段：seed 前面那些收件箱改动属于父会话那次
// 生命周期，它们的待办早就被那边认领或者取消掉了，跟着重放会凭空造出一批
// 已经跑过的活儿。
func NewInbox(live *session.Session, notify InboxNotifications) (*Inbox, error) {
	inbox := &Inbox{session: live, notify: notify}
	events := live.Events()
	seedLength := live.Header().SeedLength
	if seedLength > len(events) {
		seedLength = len(events)
	}
	for _, event := range events[seedLength:] {
		if event.Type != EventInboxSpliced {
			continue
		}
		var splice SplicedData
		if err := json.Unmarshal(event.Data, &splice); err != nil {
			return nil, fmt.Errorf("会话 seq %d 上那条收件箱改动读不回来：%w", event.Seq, err)
		}
		if err := inbox.apply(splice); err != nil {
			return nil, fmt.Errorf("会话 seq %d 上那条收件箱改动用不上去：%w", event.Seq, err)
		}
	}
	return inbox, nil
}

// NextTurn 是排着队等各自回合的提示。
//
// 源: packages/core/agent/src/inbox.ts:42-45
//
// 交回的切片自己是一份复制：之后的改动长不了一个调用方已经拿在手里的数组。
// 契约和 [ds-harness-go/core/session.Session.Events] 逐字相同——**把它当只读的**，
// 里面那些消息的内容是共享的，要一份自己拥有的就 [llm.Message.Clone]。
func (i *Inbox) NextTurn() []llm.Message { return slices.Clone(i.nextTurn) }

// NextStep 是等下一个步骤边界的输入，契约同 [Inbox.NextTurn]。
//
// 源: packages/core/agent/src/inbox.ts:47-50
func (i *Inbox) NextStep() []llm.Message { return slices.Clone(i.nextStep) }

// HasPending 表示两条清单里还有活儿。
func (i *Inbox) HasPending() bool { return len(i.nextTurn) > 0 || len(i.nextStep) > 0 }

// listOf 取某条清单当下那个切片的地址。
func (i *Inbox) listOf(target InboxTarget) *[]llm.Message {
	if target == NextTurn {
		return &i.nextTurn
	}
	return &i.nextStep
}

// Clear 耐久地取消掉所有待办输入，先清 next-step 再清 next-turn。
//
// 源: packages/core/agent/src/inbox.ts:57-61
//
// 次序是有意的：next-step 里那些是引导和注入的上下文，它们依附于 next-turn 里
// 那条提示。反过来清的话，中间那一瞬会有一批无主的引导挂在一条已经没有提示的
// 清单上，而每一步都要落一条日志，所以那一瞬是**看得见**的。
func (i *Inbox) Clear() error {
	if _, err := i.Splice(NextStep, 0, len(i.nextStep), nil); err != nil {
		return err
	}
	_, err := i.Splice(NextTurn, 0, len(i.nextTurn), nil)
	return err
}

// Claim 取走一个步骤的整批提议输入，并逐条报出认领。
//
// 源: packages/core/agent/src/inbox.ts:63-78
//
// 落下去的那些改动是**纯删除**，而且不带取消标记——被认领的活儿马上就要跑，
// 它由自己那个回合的 turn/end 交代，见 [FoldConsumedWork]。
//
// target 为 [NextTurn] 时，除了整条 next-step，再多带走队首那一条提示。
func (i *Inbox) Claim(target InboxTarget, turn int) ([]llm.Message, error) {
	claimed, err := i.mutate(NextStep, 0, len(i.nextStep), nil, false)
	if err != nil {
		return nil, err
	}
	if target == NextTurn {
		queued, err := i.mutate(NextTurn, 0, 1, nil, false)
		if err != nil {
			return nil, err
		}
		claimed = append(claimed, queued...)
	}
	if i.notify.Claimed != nil {
		for _, message := range claimed {
			i.notify.Claimed(message, turn)
		}
	}
	return claimed, nil
}

// Append 往一条清单末尾追一条消息，并耐久地记下这次插入。
//
// 源: packages/core/agent/src/inbox.ts:80-88
func (i *Inbox) Append(target InboxTarget, message llm.Message) error {
	_, err := i.Splice(target, len(*i.listOf(target)), 0, []llm.Message{message})
	return err
}

// Prepend 往一条清单开头插一条消息，并耐久地记下这次插入。
//
// 源: packages/core/agent/src/inbox.ts:90-98
func (i *Inbox) Prepend(target InboxTarget, message llm.Message) error {
	_, err := i.Splice(target, 0, 0, []llm.Message{message})
	return err
}

// Replace 原地换掉一条待办消息，身份可以跟着变。
//
// 源: packages/core/agent/src/inbox.ts:100-114
//
// 换成功会把旧的那条报成丢弃、新的那条报成插入。第二个返回值说这条消息当时
// 还在不在待办里。
func (i *Inbox) Replace(messageID llm.MessageID, newMessage llm.Message) (bool, error) {
	target, index, ok := i.locate(messageID)
	if !ok {
		return false, nil
	}
	if _, err := i.Splice(target, index, 1, []llm.Message{newMessage}); err != nil {
		return false, err
	}
	return true, nil
}

// Remove 拿掉一条待办消息，并耐久地记下这次取消。
//
// 源: packages/core/agent/src/inbox.ts:116-126
func (i *Inbox) Remove(messageID llm.MessageID) (bool, error) {
	target, index, ok := i.locate(messageID)
	if !ok {
		return false, nil
	}
	if _, err := i.Splice(target, index, 1, nil); err != nil {
		return false, err
	}
	return true, nil
}

// Splice 按标准的切片改动语义动一条清单，并耐久地记下规整之后的结果。
//
// 源: packages/core/agent/src/inbox.ts:128-146
//
// **耐久事件先提交，投影后动**：于是同步的 session/event 观察者读到的是改动
// **之前**那两条清单，配上事件里那份规整坐标，它自己就能把被删掉的那些消息
// 复原出来。这个先后是这份投影唯一的对外约定。
func (i *Inbox) Splice(target InboxTarget, start, deleteCount int, inserted []llm.Message) ([]llm.Message, error) {
	return i.mutate(target, start, deleteCount, inserted, true)
}

// locate 在两条清单里找一个待办身份。
//
// 源: packages/core/agent/src/inbox.ts:148-155
//
// 次序写死成先 next-turn 后 next-step，和 DSH 一样。两条清单里不可能有同一个
// 身份（[Inbox.validate] 守着这件事），所以这个次序不影响结果，只影响找的快慢。
func (i *Inbox) locate(messageID llm.MessageID) (InboxTarget, int, bool) {
	for _, target := range []InboxTarget{NextTurn, NextStep} {
		list := *i.listOf(target)
		index := slices.IndexFunc(list, func(m llm.Message) bool { return m.ID == messageID })
		if index >= 0 {
			return target, index, true
		}
	}
	return "", 0, false
}

// mutate 提交一次规整过的改动，并报出它那些通知。
//
// 源: packages/core/agent/src/inbox.ts:157-193
//
// discardRemoved 区分两种删除：真的取消（要打 Canceled、要报丢弃）和一次认领
// （不打、不报，由 [Inbox.Claim] 自己报认领）。
func (i *Inbox) mutate(
	target InboxTarget,
	start, deleteCount int,
	inserted []llm.Message,
	discardRemoved bool,
) ([]llm.Message, error) {
	list := i.listOf(target)
	length := len(*list)

	// 源: packages/core/agent/src/inbox.ts:166-175。DSH 在这里复刻了一遍
	// Array.prototype.splice 的夹取规则：负的起点从末尾往回数，超界的往两头夹。
	//
	// 新增: 它那几步 Math.trunc / Number.isNaN 在 Go 里不存在——start 和
	// deleteCount 是 int，既不会是小数也不会是 NaN。剩下的夹取照抄，因为落进
	// 日志的必须是一份可以直接照做的坐标，见 [SplicedData] 上的注释。
	actualStart := start
	if actualStart < 0 {
		actualStart = max(length+actualStart, 0)
	} else {
		actualStart = min(actualStart, length)
	}
	actualDeleteCount := min(max(deleteCount, 0), length-actualStart)

	if actualDeleteCount == 0 && len(inserted) == 0 {
		return nil, nil
	}
	splice := SplicedData{
		Target:       target,
		Start:        actualStart,
		RemovedCount: actualDeleteCount,
		Inserted:     inserted,
		Canceled:     discardRemoved && actualDeleteCount > 0,
	}
	if err := i.validate(splice); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(splice)
	if err != nil {
		return nil, err
	}
	if _, err := i.session.Append(sessionlog.Event{
		Type: EventInboxSpliced,
		Data: payload,
	}); err != nil {
		return nil, err
	}

	removed := slices.Clone((*list)[actualStart : actualStart+actualDeleteCount])
	*list = slices.Concat(
		(*list)[:actualStart],
		cloneMessages(inserted),
		(*list)[actualStart+actualDeleteCount:],
	)

	if discardRemoved && i.notify.Discarded != nil {
		for _, message := range removed {
			i.notify.Discarded(message)
		}
	}
	if i.notify.Inserted != nil {
		for _, message := range inserted {
			i.notify.Inserted(message)
		}
	}
	return removed, nil
}

// apply 把一条已经规整过的耐久改动用到投影上。
//
// 源: packages/core/agent/src/inbox.ts:195-200
func (i *Inbox) apply(splice SplicedData) error {
	if err := i.validate(splice); err != nil {
		return err
	}
	list := i.listOf(splice.Target)
	*list = slices.Concat(
		(*list)[:splice.Start],
		cloneMessages(splice.Inserted),
		(*list)[splice.Start+splice.RemovedCount:],
	)
	return nil
}

// cloneMessages 把一批消息连内容一起复制一份。
//
// 新增: DSH 那边这件事由 session.append 里的 structuredClone 顺手做掉——它往
// 投影里放的是 `event.data.inserted`，也就是那份已经深复制并冻上的负载。Go 的
// [ds-harness-go/core/session.Session.Append] 不碰调用方的值（负载的深复制发生
// 在排 JSON 那一步，产物是字节不是消息），所以这一份得自己复制：不然调用方
// 手上那条消息的内容切片和投影里这条是同一块内存。
func cloneMessages(messages []llm.Message) []llm.Message {
	if len(messages) == 0 {
		return nil
	}
	cloned := make([]llm.Message, len(messages))
	for index, message := range messages {
		cloned[index] = message.Clone()
	}
	return cloned
}

// validate 拿当下这份投影验一条规整过的改动。
//
// 源: packages/core/agent/src/inbox.ts:202-219
//
// 两件事：坐标落在清单里面，以及改完之后两条清单加起来没有重复的消息身份。
// 后一条是**跨清单**验的——同一条消息同时挂在 next-turn 和 next-step 上，
// 会被认领两次。
func (i *Inbox) validate(splice SplicedData) error {
	list := *i.listOf(splice.Target)
	// 新增: DSH 那两处 Number.isSafeInteger 不移——Go 的 int 天生是整数，
	// 而这里的两个值要么来自 [Inbox.mutate] 的夹取、要么来自一条读进来的
	// [SplicedData]，后者的编码格式由它自己的 UnmarshalJSON 管。
	if splice.Start < 0 || splice.Start > len(list) ||
		splice.RemovedCount < 0 || splice.Start+splice.RemovedCount > len(list) {
		return fmt.Errorf("%w：%s 上 start=%d removedCount=%d，清单长 %d",
			ErrInvalidSplice, splice.Target, splice.Start, splice.RemovedCount, len(list))
	}

	candidate := slices.Concat(
		list[:splice.Start],
		splice.Inserted,
		list[splice.Start+splice.RemovedCount:],
	)
	other := i.nextStep
	if splice.Target == NextStep {
		other = i.nextTurn
	}
	seen := make(map[llm.MessageID]struct{}, len(candidate)+len(other))
	for _, message := range slices.Concat(candidate, other) {
		if _, duplicate := seen[message.ID]; duplicate {
			return fmt.Errorf("%w：消息 %q 已经在待办里了", ErrInvalidSplice, message.ID)
		}
		seen[message.ID] = struct{}{}
	}
	return nil
}
