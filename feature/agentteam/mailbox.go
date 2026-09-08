// 本文件的作用：队友之间那条收件箱——排队、认领、送到、落账，以及把掉队的认领捡回来。
//
// 源: packages/experimental/agent-team/src/mailbox.ts

package agentteam

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"

	"github.com/snight1983/ds-harness-go/feature/subagent"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/storage"
	"github.com/snight1983/ds-harness-go/storage/domain"
)

// SendRequest 是发一条队友消息要给的东西。
//
// 源: packages/experimental/agent-team/src/types.ts（SendTeamMessageRequest）
type SendRequest struct {
	// Target 是收件人的名字，队长写 [LeadName]。
	Target string
	// Content 是消息正文，送到收件人跟前时会加一句发信人抬头。
	Content llm.Content
	// Delivery 是安静放进去还是排一个回合。
	Delivery Delivery
}

// SendResult 是一次发信的结果。
//
// 源: packages/experimental/agent-team/src/types.ts（SendTeamMessageResult）
type SendResult struct {
	// Message 是这条消息的身份。
	Message MessageID
	// Phase 是这一趟走到了哪一步：当场送到就是 [MessageDelivered]，
	// 没送成就是 [MessageQueued]，等收件人所在的那个副本来捡。
	Phase MessagePhase
}

// Send 把一条消息排进收件人的收件箱，然后当场试着送一次。
//
// 源: packages/experimental/agent-team/src/mailbox.ts:109-143（sendAdmitted）
//
// 「排」和「送」是两件事：排是一次条件写，成了这条消息就跑不掉了；送是尽力而为，
// 收件人不在本副本上就送不成，交回 [MessageQueued]，由 [Service.Deliver] 后面接着送。
// 反过来先送再排的话，一条已经进了收件人历史的消息在介质上不会留下任何痕迹。
func (s *Service) Send(ctx context.Context, who Caller, request SendRequest) (SendResult, error) {
	switch request.Delivery {
	case DeliveryQuiet, DeliveryWakeup:
	default:
		return SendResult{}, newError(CodeInvalidArgument, "送法 %q 不认得", request.Delivery)
	}
	if len(request.Content) == 0 {
		return SendResult{}, newError(CodeInvalidArgument, "发一条消息要有正文")
	}
	roster, err := s.roster(ctx, who.Team)
	if err != nil {
		return SendResult{}, err
	}
	target, err := resolveActiveMember(roster, request.Target)
	if err != nil {
		return SendResult{}, err
	}
	if target == who.ID {
		return SendResult{}, newError(CodeSelfMessage, "自己发给自己没有意义")
	}

	pending, err := s.pendingMessages(ctx, who.Team, target)
	if err != nil {
		return SendResult{}, err
	}
	if len(pending) >= s.config.MaxPendingMessages {
		return SendResult{}, newError(CodeMailboxFull,
			"%q 的收件箱里还压着 %d 条没送到的", request.Target, len(pending))
	}

	message := Message{
		ID:         MessageID(fmt.Sprintf("team-message-%s", s.newID())),
		Team:       who.Team,
		SenderID:   who.ID,
		SenderName: who.Name,
		TargetID:   target,
		Delivery:   request.Delivery,
		Content:    append(llm.Content(nil), request.Content...),
		Phase:      MessageQueued,
		QueuedAt:   s.clock().UnixMilli(),
	}
	framed, err := json.Marshal(deliveryContent(message))
	if err != nil {
		return SendResult{}, wrapError(CodeInvalidArgument, err, "这条消息的正文排不出去")
	}
	// 量的是**加了抬头之后**那一整份，因为那才是收件人真正吃进去的字节。
	if len(framed) > s.config.MaxMessageBytes {
		return SendResult{}, newError(CodeMessageTooLarge,
			"这条消息 %d 字节，超过了 %d", len(framed), s.config.MaxMessageBytes)
	}
	if err := s.messages.Create(ctx, string(message.ID), message); err != nil {
		return SendResult{}, storeError(err, "把消息 %q 排进队没成", message.ID)
	}

	delivered, err := s.deliver(ctx, roster, message)
	if err != nil {
		return SendResult{}, err
	}
	if delivered {
		return SendResult{Message: message.ID, Phase: MessageDelivered}, nil
	}
	return SendResult{Message: message.ID, Phase: MessageQueued}, nil
}

// Deliver 把这支团队里还没送到、而本副本此刻送得动的消息挨个送一遍，交回送成了几条。
//
// 源: packages/experimental/agent-team/src/mailbox.ts:84-97（recoverFor）
//
// 什么时候叫它：一个队友在本副本上活起来之后。DSH 那边是 agent 起来时的一个事件
// 回调，本包多副本，谁把收件人活化起来谁就该来叫这一次——别的副本既看不见这个
// 收件人，也就送不动它那几条。
//
// 一条都送不动不算失败：交回 0 就是「这些消息还得等」。真正的失败只有读不动介质。
func (s *Service) Deliver(ctx context.Context, team TeamID) (int, error) {
	roster, err := s.roster(ctx, team)
	if err != nil {
		return 0, err
	}
	entries, err := s.messages.Entries(ctx)
	if err != nil {
		return 0, storeError(err, "读团队 %q 的收件箱没成", team)
	}
	waiting := make([]Message, 0, len(entries))
	for _, entry := range entries {
		if entry.Value.Team == team && entry.Value.Phase != MessageDelivered {
			waiting = append(waiting, entry.Value)
		}
	}
	// 按排队时刻送，同刻按身份定序：同一个收件人的几条消息该按发出来的次序进它的历史。
	sort.Slice(waiting, func(left, right int) bool {
		if waiting[left].QueuedAt != waiting[right].QueuedAt {
			return waiting[left].QueuedAt < waiting[right].QueuedAt
		}
		return waiting[left].ID < waiting[right].ID
	})

	sent := 0
	for _, message := range waiting {
		delivered, err := s.deliver(ctx, roster, message)
		if err != nil {
			return sent, err
		}
		if delivered {
			sent++
		}
	}
	return sent, nil
}

// deliver 试着把一条消息送到收件人跟前，交回送成没送成。
//
// 源: packages/experimental/agent-team/src/mailbox.ts:227-262（dispatchOnce）
//
// 三步：先把这条消息认领下来（一次条件写，抢输的直接算没送成），再真的送，
// 送成了转 [MessageDelivered]。送不动就把认领放回去，不留着占位。
//
// 送这一步失败**不往上报**，只记一条日志：一条送不出去的消息该留在队里等下一次，
// 而不是让发信人那次调用整个失败——她要的是「排进去了」，那件事已经成了。
func (s *Service) deliver(ctx context.Context, roster Roster, message Message) (bool, error) {
	claimed, ok, err := s.claimMessage(ctx, message.ID)
	if err != nil || !ok {
		return false, err
	}
	sendErr := s.handOff(ctx, roster, claimed)
	if sendErr != nil {
		if releaseErr := s.releaseMessage(ctx, claimed.ID); releaseErr != nil {
			return false, releaseErr
		}
		s.logger.Warn("agentteam: 消息还留在队里",
			slog.String("message", string(claimed.ID)),
			slog.String("target", string(claimed.TargetID)),
			slog.Any("error", sendErr))
		return false, nil
	}
	if err := s.settleDelivered(ctx, claimed.ID); err != nil {
		// 消息已经进了收件人的历史，落账却没成。不重送——那会让收件人看见两遍；
		// 这条记录留在 claimed 上，[Config.ClaimTTL] 到了会有人再送一次，
		// 那正是「至少一次」这句承诺的边界，见 [Message]。
		return false, err
	}
	return true, nil
}

// handOff 是真正把正文交到收件人手上的那一下。
//
// 源: packages/experimental/agent-team/src/mailbox.ts:228-259
//
// 走哪条路由收件人此刻在不在本副本上、以及送法是安静还是唤醒共同决定：
//
//	安静   非要一个活的收件人不可——安静的意思就是「放进它的收件箱，别开新回合」，
//	       而收件箱是进程内的东西。不在本副本上就送不了，留在队里。
//	唤醒   收件人是队长时同上；是队友时走 [Subagents.Followup]，它会替一个没活化的
//	       队友做冷恢复，所以收件人在不在本副本上都送得动——但**队长**必须在，
//	       起一次投递要交出队长那个活 agent 对象。
func (s *Service) handOff(ctx context.Context, roster Roster, message Message) error {
	source, err := NewMessageSource(MessageSource{
		Team:       message.Team,
		Message:    message.ID,
		SenderID:   message.SenderID,
		SenderName: message.SenderName,
	})
	if err != nil {
		return err
	}
	content := deliveryContent(message)
	lead := sessionlog.SessionID(roster.Lead)

	if message.TargetID == lead || message.Delivery == DeliveryQuiet {
		live, found := s.agents.Agent(message.TargetID)
		if !found {
			return newError(CodeInvalidTarget, "收件人 %q 不在本副本上", message.TargetID)
		}
		target := agent.NextStep
		wakeup := false
		if message.Delivery == DeliveryWakeup {
			target = agent.NextTurn
			wakeup = true
		}
		live.Send(llm.NewUserMessage(content, source), target, wakeup)
		return nil
	}

	parent, found := s.agents.Agent(lead)
	if !found {
		return newError(CodeInvalidTarget, "队长 %q 不在本副本上，唤醒不了队友", lead)
	}
	if _, err := s.subagents.Followup(ctx, parent, message.TargetID, content, subagent.FollowupOptions{
		Source: source,
	}); err != nil {
		return wrapError(CodeInvalidTarget, err, "给队友 %q 排回合没成", message.TargetID)
	}
	return nil
}

// deliveryContent 给正文加一句发信人抬头。
//
// 源: packages/experimental/agent-team/src/mailbox.ts:315-322（deliveryContent）
//
// 抬头是英文而且逐字照录 DSH：这段文字直接进模型的历史，是它据以判断「这句话谁说的」
// 的唯一线索，改一个词就是改提示词。
func deliveryContent(message Message) llm.Content {
	content := make(llm.Content, 0, len(message.Content)+1)
	content = append(content, llm.TextBlock{
		Text: fmt.Sprintf("Team message %s from %s:", message.ID, message.SenderName),
	})
	return append(content, message.Content...)
}

// ---- 消息表上那三次条件写 ----

// claimMessage 把一条消息从 [MessageQueued] 认领成 [MessageClaimed]。
//
// 新增: DSH 靠一个进程内的 in-flight 集合保证「同一条消息同时只送一次」。本包多副本，
// 那个集合挡不住另一台机器，所以这一步落盘。掉队的认领（[Message.Expired]）当作
// 排着队处理——抓着它的副本可能整个没了，没有任何通道会通知别人这件事。
//
// 第二个返回值为假表示这条消息此刻轮不到本副本：别人正抓着它、已经送到了，
// 或者刚被别的副本改过（条件写抢输）。这三种都不是失败。
func (s *Service) claimMessage(ctx context.Context, id MessageID) (Message, bool, error) {
	now := s.clock().UnixMilli()
	ttl := s.config.ClaimTTL.Milliseconds()
	claimable := false
	next, err := s.messages.Update(ctx, string(id), func(current Message) (Message, error) {
		claimable = current.Phase == MessageQueued || current.Expired(now, ttl)
		if !claimable {
			return current, nil
		}
		current.Phase = MessageClaimed
		current.Claimant = s.replica
		current.ClaimedAt = now
		return current, nil
	})
	switch {
	case isDomainCode(err, domain.CodeMissingKey), isStorageCode(err, storage.CodeStaleRevision):
		// 消息没了或者刚被别人改过：这一趟不该由本副本送。
		return Message{}, false, nil
	case err != nil:
		return Message{}, false, storeError(err, "认领消息 %q 没成", id)
	}
	return next, claimable, nil
}

// settleDelivered 把一条送到了的消息转成 [MessageDelivered]。
func (s *Service) settleDelivered(ctx context.Context, id MessageID) error {
	_, err := s.messages.Update(ctx, string(id), func(current Message) (Message, error) {
		current.Phase = MessageDelivered
		current.Claimant = ""
		current.ClaimedAt = 0
		return current, nil
	})
	if err != nil {
		return storeError(err, "记消息 %q 送到了没成", id)
	}
	return nil
}

// releaseMessage 把一次没送成的认领放回 [MessageQueued]。
//
// 只放自己那一次：认领已经被别人捡走（TTL 到了）时原样留着，否则这一放会把
// 别人正在送的那条打回队里，于是同一条消息被送两遍。
func (s *Service) releaseMessage(ctx context.Context, id MessageID) error {
	_, err := s.messages.Update(ctx, string(id), func(current Message) (Message, error) {
		if current.Phase != MessageClaimed || current.Claimant != s.replica {
			return current, nil
		}
		current.Phase = MessageQueued
		current.Claimant = ""
		current.ClaimedAt = 0
		return current, nil
	})
	if err != nil && !isStorageCode(err, storage.CodeStaleRevision) {
		return storeError(err, "把消息 %q 放回队里没成", id)
	}
	return nil
}

// pendingMessages 数一个收件人此刻压着几条没送到的。
//
// 源: packages/experimental/agent-team/src/mailbox.ts:122-130
func (s *Service) pendingMessages(ctx context.Context, team TeamID, target sessionlog.SessionID) ([]Message, error) {
	entries, err := s.messages.Entries(ctx)
	if err != nil {
		return nil, storeError(err, "读团队 %q 的收件箱没成", team)
	}
	pending := make([]Message, 0, len(entries))
	for _, entry := range entries {
		if entry.Value.Team == team && entry.Value.TargetID == target && entry.Value.Phase != MessageDelivered {
			pending = append(pending, entry.Value)
		}
	}
	return pending, nil
}

// ---- 来源标记 ----

// messageSourceExtra 是 [MessageSource] 在介质上的样子。
type messageSourceExtra struct {
	Team       TeamID               `json:"teamId"`
	Message    MessageID            `json:"messageId"`
	SenderID   sessionlog.SessionID `json:"senderId"`
	SenderName string               `json:"senderName"`
}

// NewMessageSource 把一份 [MessageSource] 折成消息上带的那条来源。
//
// 源: packages/experimental/agent-team/src/mailbox.ts:233-239
//
// 判别键在 [llm.PluginSource.Plugin]，四个字段在 Extra 里，理由见 [MessageSource]。
func NewMessageSource(source MessageSource) (llm.PluginSource, error) {
	if source.Message == "" {
		return llm.PluginSource{}, newError(CodeInvalidArgument, "队友消息的来源标记缺消息身份")
	}
	extra, err := json.Marshal(messageSourceExtra(source))
	if err != nil {
		// 不可达：四个字段都是字符串。
		return llm.PluginSource{}, wrapError(CodeInvalidArgument, err, "队友消息的来源标记排不出去")
	}
	return llm.PluginSource{Plugin: MessageSourceKind, Extra: extra}, nil
}

// MessageSourceOf 把一条来源上的队友消息标记取回来。
//
// 第二个返回值为假表示这条来源根本不是队友消息（此时第三个返回值一定是 nil）。
// 做法与 [github.com/snight1983/ds-harness-go/feature/compaction.CheckpointSourceOf] 一致。
func MessageSourceOf(source llm.MessageSource) (MessageSource, bool, error) {
	plugin, ok := source.(llm.PluginSource)
	if !ok || plugin.Plugin != MessageSourceKind {
		return MessageSource{}, false, nil
	}
	var extra messageSourceExtra
	if len(plugin.Extra) > 0 {
		if err := json.Unmarshal(plugin.Extra, &extra); err != nil {
			return MessageSource{}, true, wrapError(CodeInvalidArgument, err, "队友消息的来源标记读不回来")
		}
	}
	return MessageSource(extra), true, nil
}
