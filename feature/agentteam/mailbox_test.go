// 本文件的作用：队友收件箱的用例——排队与投递、安静与唤醒各走哪条路、
// 掉队的认领怎么捡回来，以及来源标记折进折出。

package agentteam

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
)

// body 造一份消息正文。
func body(text string) llm.Content { return llm.Content{llm.TextBlock{Text: text}} }

func TestSend安静送给活着的队友当场就到(t *testing.T) {
	box := boot(t)
	view := box.spawn(t, "reviewer")
	lead := box.leader(t)

	result, err := box.service.Send(t.Context(), lead, SendRequest{
		Target: "reviewer", Content: body("看一眼这个"), Delivery: DeliveryQuiet,
	})
	if err != nil {
		t.Fatalf("发信不该失败：%v", err)
	}
	if result.Phase != MessageDelivered {
		t.Fatalf("收件人活着，该当场送到，得到 %q", result.Phase)
	}
	if stored := box.message(t, result.Message); stored.Phase != MessageDelivered {
		t.Fatalf("落盘的那一条该是 %q，得到 %q", MessageDelivered, stored.Phase)
	}

	child, _ := box.agents.Agent(view.ID)
	inbox := child.(*stubAgent).inbox()
	if len(inbox) != 1 {
		t.Fatalf("该收到一条，得到 %d 条", len(inbox))
	}
	if inbox[0].target != agent.NextStep || inbox[0].wakeup {
		t.Fatalf("安静送该进下一步且不叫醒，得到 %+v", inbox[0])
	}
}

func TestSend送到的那条正文前面有发信人抬头(t *testing.T) {
	box := boot(t)
	view := box.spawn(t, "reviewer")
	lead := box.leader(t)

	result, err := box.service.Send(t.Context(), lead, SendRequest{
		Target: "reviewer", Content: body("看一眼这个"), Delivery: DeliveryQuiet,
	})
	if err != nil {
		t.Fatalf("发信不该失败：%v", err)
	}

	child, _ := box.agents.Agent(view.ID)
	got := text(child.(*stubAgent).inbox()[0].message.Content)
	header := "Team message " + string(result.Message) + " from " + LeadName + ":"
	if !strings.HasPrefix(got, header) {
		t.Fatalf("抬头该是 %q，得到 %q", header, got)
	}
	if !strings.HasSuffix(got, "看一眼这个") {
		t.Fatalf("正文该跟在抬头后面，得到 %q", got)
	}
}

func TestSend送到的那条带得回来源标记(t *testing.T) {
	box := boot(t)
	view := box.spawn(t, "reviewer")
	lead := box.leader(t)

	result, err := box.service.Send(t.Context(), lead, SendRequest{
		Target: "reviewer", Content: body("看一眼这个"), Delivery: DeliveryQuiet,
	})
	if err != nil {
		t.Fatalf("发信不该失败：%v", err)
	}

	child, _ := box.agents.Agent(view.ID)
	source, ok, err := MessageSourceOf(child.(*stubAgent).inbox()[0].message.Source)
	if err != nil {
		t.Fatalf("读来源不该失败：%v", err)
	}
	if !ok {
		t.Fatal("这条该认得出是队友消息")
	}
	if source.Message != result.Message || source.SenderName != LeadName || source.SenderID != leadID {
		t.Fatalf("来源上该记着谁发的哪一条，得到 %+v", source)
	}
	if source.Team != box.team() {
		t.Fatalf("来源上该记着哪支团队，得到 %q", source.Team)
	}
}

func TestSend唤醒队友走的是排回合那条路(t *testing.T) {
	box := boot(t)
	view := box.spawn(t, "reviewer")
	lead := box.leader(t)

	result, err := box.service.Send(t.Context(), lead, SendRequest{
		Target: "reviewer", Content: body("醒醒"), Delivery: DeliveryWakeup,
	})
	if err != nil {
		t.Fatalf("发信不该失败：%v", err)
	}
	if result.Phase != MessageDelivered {
		t.Fatalf("该当场送到，得到 %q", result.Phase)
	}

	_, followups, _ := box.subagents.calls()
	if len(followups) != 1 || followups[0].child != view.ID {
		t.Fatalf("该给 %q 排一个回合，得到 %+v", view.ID, followups)
	}
	want := "Team message " + string(result.Message) + " from " + LeadName + ":醒醒"
	if got := text(followups[0].content); got != want {
		t.Fatalf("排回合那份该是加了抬头的正文 %q，得到 %q", want, got)
	}
	child, _ := box.agents.Agent(view.ID)
	if len(child.(*stubAgent).inbox()) != 0 {
		t.Fatal("唤醒队友不走收件箱那条路")
	}
}

func TestSend唤醒队友时收件人不在本副本上也送得动(t *testing.T) {
	box := boot(t)
	view := box.spawn(t, "reviewer")
	box.agents.drop(view.ID)
	lead := box.leader(t)

	result, err := box.service.Send(t.Context(), lead, SendRequest{
		Target: "reviewer", Content: body("醒醒"), Delivery: DeliveryWakeup,
	})
	if err != nil {
		t.Fatalf("发信不该失败：%v", err)
	}
	if result.Phase != MessageDelivered {
		t.Fatalf("排回合会替它冷恢复，该送得到，得到 %q", result.Phase)
	}
}

func TestSend唤醒队友时队长不在本副本上就送不动(t *testing.T) {
	box := boot(t)
	box.spawn(t, "reviewer")
	lead := box.leader(t)
	box.agents.drop(leadID)

	result, err := box.service.Send(t.Context(), lead, SendRequest{
		Target: "reviewer", Content: body("醒醒"), Delivery: DeliveryWakeup,
	})
	if err != nil {
		t.Fatalf("送不动不该让发信整个失败：%v", err)
	}
	if result.Phase != MessageQueued {
		t.Fatalf("该留在队里，得到 %q", result.Phase)
	}
	if stored := box.message(t, result.Message); stored.Phase != MessageQueued || stored.Claimant != "" {
		t.Fatalf("没送成的那次认领该放回去，得到 %+v", stored)
	}
}

func TestSend安静送时收件人不在本副本上就留在队里(t *testing.T) {
	box := boot(t)
	view := box.spawn(t, "reviewer")
	box.agents.drop(view.ID)
	lead := box.leader(t)

	result, err := box.service.Send(t.Context(), lead, SendRequest{
		Target: "reviewer", Content: body("看一眼"), Delivery: DeliveryQuiet,
	})
	if err != nil {
		t.Fatalf("送不动不该让发信整个失败：%v", err)
	}
	if result.Phase != MessageQueued {
		t.Fatalf("安静送要一个活的收件人，该留在队里，得到 %q", result.Phase)
	}
}

func TestSend给队长唤醒也走收件箱(t *testing.T) {
	box := boot(t)
	view := box.spawn(t, "reviewer")
	teammate := box.caller(t, view.ID)

	result, err := box.service.Send(t.Context(), teammate, SendRequest{
		Target: LeadName, Content: body("我做完了"), Delivery: DeliveryWakeup,
	})
	if err != nil {
		t.Fatalf("发信不该失败：%v", err)
	}
	if result.Phase != MessageDelivered {
		t.Fatalf("队长活着，该当场送到，得到 %q", result.Phase)
	}

	inbox := box.lead.inbox()
	if len(inbox) != 1 {
		t.Fatalf("队长该收到一条，得到 %d 条", len(inbox))
	}
	if inbox[0].target != agent.NextTurn || !inbox[0].wakeup {
		t.Fatalf("唤醒该进下一回合且叫醒，得到 %+v", inbox[0])
	}
	if _, followups, _ := box.subagents.calls(); len(followups) != 0 {
		t.Fatal("送给队长不走排回合那条路")
	}
}

func TestSend入参不合式一律当场拒(t *testing.T) {
	box := boot(t)
	box.spawn(t, "reviewer")
	lead := box.leader(t)

	cases := map[string]struct {
		request SendRequest
		code    Code
	}{
		"送法不认得":  {SendRequest{Target: "reviewer", Content: body("话"), Delivery: "shout"}, CodeInvalidArgument},
		"正文空着":   {SendRequest{Target: "reviewer", Delivery: DeliveryQuiet}, CodeInvalidArgument},
		"收件人不在册": {SendRequest{Target: "查无此人", Content: body("话"), Delivery: DeliveryQuiet}, CodeMemberNotFound},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := box.service.Send(t.Context(), lead, testCase.request); !errors.Is(err, testCase.code) {
				t.Fatalf("该报 %q，得到 %v", testCase.code, err)
			}
		})
	}
}

func TestSend自己发给自己没有意义(t *testing.T) {
	box := boot(t)
	view := box.spawn(t, "reviewer")

	_, err := box.service.Send(t.Context(), box.caller(t, view.ID), SendRequest{
		Target: "reviewer", Content: body("自言自语"), Delivery: DeliveryQuiet,
	})
	if !errors.Is(err, CodeSelfMessage) {
		t.Fatalf("该报 %q，得到 %v", CodeSelfMessage, err)
	}
}

func TestSend收件箱压满了就不许再发(t *testing.T) {
	box := boot(t, func(config *Config) { config.MaxPendingMessages = 1 })
	view := box.spawn(t, "reviewer")
	box.agents.drop(view.ID)
	lead := box.leader(t)

	if _, err := box.service.Send(t.Context(), lead, SendRequest{
		Target: "reviewer", Content: body("第一条"), Delivery: DeliveryQuiet,
	}); err != nil {
		t.Fatalf("第一条不该失败：%v", err)
	}

	_, err := box.service.Send(t.Context(), lead, SendRequest{
		Target: "reviewer", Content: body("第二条"), Delivery: DeliveryQuiet,
	})
	if !errors.Is(err, CodeMailboxFull) {
		t.Fatalf("该报 %q，得到 %v", CodeMailboxFull, err)
	}
}

func TestSend送到了的那几条不占收件箱(t *testing.T) {
	box := boot(t, func(config *Config) { config.MaxPendingMessages = 1 })
	box.spawn(t, "reviewer")
	lead := box.leader(t)

	for _, line := range []string{"第一条", "第二条"} {
		result, err := box.service.Send(t.Context(), lead, SendRequest{
			Target: "reviewer", Content: body(line), Delivery: DeliveryQuiet,
		})
		if err != nil {
			t.Fatalf("%s 不该失败：%v", line, err)
		}
		if result.Phase != MessageDelivered {
			t.Fatalf("%s 该当场送到，得到 %q", line, result.Phase)
		}
	}
}

func TestSend太大的消息发不出去(t *testing.T) {
	box := boot(t, func(config *Config) { config.MaxMessageBytes = 64 })
	box.spawn(t, "reviewer")
	lead := box.leader(t)

	_, err := box.service.Send(t.Context(), lead, SendRequest{
		Target: "reviewer", Content: body(strings.Repeat("字", 200)), Delivery: DeliveryQuiet,
	})
	if !errors.Is(err, CodeMessageTooLarge) {
		t.Fatalf("该报 %q，得到 %v", CodeMessageTooLarge, err)
	}
}

func TestSend还没在岗的队友收不了消息(t *testing.T) {
	box := boot(t)
	box.spawn(t, "reviewer")
	lead := box.leader(t)
	box.subagents.startErr = errors.New("提供方不认得")
	_, _ = box.service.Spawn(t.Context(), leadID, SpawnRequest{
		Name: "broken", Description: "起不起来的", Provider: "in-process",
		Context: ContextFresh, Prompt: body("开工"),
	})

	_, err := box.service.Send(t.Context(), lead, SendRequest{
		Target: "broken", Content: body("话"), Delivery: DeliveryQuiet,
	})
	if !errors.Is(err, CodeMemberNotFound) {
		t.Fatalf("该报 %q，得到 %v", CodeMemberNotFound, err)
	}
}

func TestDeliver收件人活起来之后把压着的那几条送出去(t *testing.T) {
	box := boot(t)
	view := box.spawn(t, "reviewer")
	box.agents.drop(view.ID)
	lead := box.leader(t)

	for _, line := range []string{"第一条", "第二条"} {
		if _, err := box.service.Send(t.Context(), lead, SendRequest{
			Target: "reviewer", Content: body(line), Delivery: DeliveryQuiet,
		}); err != nil {
			t.Fatalf("%s 不该失败：%v", line, err)
		}
	}

	child := &stubAgent{id: view.ID}
	box.agents.add(child)
	sent, err := box.service.Deliver(t.Context(), box.team())
	if err != nil {
		t.Fatalf("投递不该失败：%v", err)
	}
	if sent != 2 {
		t.Fatalf("该送出去两条，得到 %d 条", sent)
	}

	inbox := child.inbox()
	if len(inbox) != 2 {
		t.Fatalf("该收到两条，得到 %d 条", len(inbox))
	}
	if !strings.HasSuffix(text(inbox[0].message.Content), "第一条") {
		t.Fatalf("该按排队次序送，第一条得到 %q", text(inbox[0].message.Content))
	}
	if !strings.HasSuffix(text(inbox[1].message.Content), "第二条") {
		t.Fatalf("该按排队次序送，第二条得到 %q", text(inbox[1].message.Content))
	}
}

func TestDeliver一条都送不动交回零而不是报错(t *testing.T) {
	box := boot(t)
	view := box.spawn(t, "reviewer")
	box.agents.drop(view.ID)
	lead := box.leader(t)
	if _, err := box.service.Send(t.Context(), lead, SendRequest{
		Target: "reviewer", Content: body("话"), Delivery: DeliveryQuiet,
	}); err != nil {
		t.Fatalf("发信不该失败：%v", err)
	}

	sent, err := box.service.Deliver(t.Context(), box.team())
	if err != nil {
		t.Fatalf("送不动不算失败：%v", err)
	}
	if sent != 0 {
		t.Fatalf("该一条都没送出去，得到 %d 条", sent)
	}
}

func TestDeliver送到过的不会再送一遍(t *testing.T) {
	box := boot(t)
	view := box.spawn(t, "reviewer")
	lead := box.leader(t)
	if _, err := box.service.Send(t.Context(), lead, SendRequest{
		Target: "reviewer", Content: body("话"), Delivery: DeliveryQuiet,
	}); err != nil {
		t.Fatalf("发信不该失败：%v", err)
	}

	sent, err := box.service.Deliver(t.Context(), box.team())
	if err != nil {
		t.Fatalf("投递不该失败：%v", err)
	}
	if sent != 0 {
		t.Fatalf("已经送到的不该再送，得到 %d 条", sent)
	}
	child, _ := box.agents.Agent(view.ID)
	if len(child.(*stubAgent).inbox()) != 1 {
		t.Fatalf("收件人该只见过一遍，得到 %d 条", len(child.(*stubAgent).inbox()))
	}
}

func TestDeliver别人正抓着的那条这一趟不碰(t *testing.T) {
	box := boot(t)
	view := box.spawn(t, "reviewer")
	box.agents.drop(view.ID)
	lead := box.leader(t)
	result, err := box.service.Send(t.Context(), lead, SendRequest{
		Target: "reviewer", Content: body("话"), Delivery: DeliveryQuiet,
	})
	if err != nil {
		t.Fatalf("发信不该失败：%v", err)
	}
	// 演一个别的副本刚把它抓走。
	if _, err := box.service.messages.Update(t.Context(), string(result.Message),
		func(current Message) (Message, error) {
			current.Phase = MessageClaimed
			current.Claimant = "replica-b"
			current.ClaimedAt = box.now.UnixMilli()
			return current, nil
		}); err != nil {
		t.Fatalf("改消息不该失败：%v", err)
	}

	box.agents.add(&stubAgent{id: view.ID})
	sent, err := box.service.Deliver(t.Context(), box.team())
	if err != nil {
		t.Fatalf("投递不该失败：%v", err)
	}
	if sent != 0 {
		t.Fatalf("别人抓着的这一趟不该动，得到 %d 条", sent)
	}
	if stored := box.message(t, result.Message); stored.Claimant != "replica-b" {
		t.Fatalf("别人那次认领该原样留着，得到 %q", stored.Claimant)
	}
}

func TestDeliver掉队的认领过了期就捡得回来(t *testing.T) {
	box := boot(t, func(config *Config) { config.ClaimTTL = time.Minute })
	view := box.spawn(t, "reviewer")
	box.agents.drop(view.ID)
	lead := box.leader(t)
	result, err := box.service.Send(t.Context(), lead, SendRequest{
		Target: "reviewer", Content: body("话"), Delivery: DeliveryQuiet,
	})
	if err != nil {
		t.Fatalf("发信不该失败：%v", err)
	}
	// 演一个抓着这条消息就整个消失的副本。
	if _, err := box.service.messages.Update(t.Context(), string(result.Message),
		func(current Message) (Message, error) {
			current.Phase = MessageClaimed
			current.Claimant = "replica-gone"
			current.ClaimedAt = box.now.UnixMilli()
			return current, nil
		}); err != nil {
		t.Fatalf("改消息不该失败：%v", err)
	}

	child := &stubAgent{id: view.ID}
	box.agents.add(child)
	box.now = box.now.Add(2 * time.Minute)

	sent, err := box.service.Deliver(t.Context(), box.team())
	if err != nil {
		t.Fatalf("投递不该失败：%v", err)
	}
	if sent != 1 {
		t.Fatalf("过了期该捡回来重送，得到 %d 条", sent)
	}
	if len(child.inbox()) != 1 {
		t.Fatalf("收件人该收到那一条，得到 %d 条", len(child.inbox()))
	}
	if stored := box.message(t, result.Message); stored.Phase != MessageDelivered {
		t.Fatalf("送到之后该落成 %q，得到 %q", MessageDelivered, stored.Phase)
	}
}

func TestDeliver没有这支团队时说清楚(t *testing.T) {
	box := boot(t)

	if _, err := box.service.Deliver(t.Context(), TeamID("没起过的")); !errors.Is(err, CodeTeamNotFound) {
		t.Fatalf("该报 %q，得到 %v", CodeTeamNotFound, err)
	}
}

func TestMessageSource折进去再折回来是同一份(t *testing.T) {
	want := MessageSource{
		Team:       "team-1",
		Message:    "team-message-1",
		SenderID:   "session-lead",
		SenderName: LeadName,
	}

	source, err := NewMessageSource(want)
	if err != nil {
		t.Fatalf("折来源不该失败：%v", err)
	}
	if source.Plugin != MessageSourceKind {
		t.Fatalf("判别键该是 %q，得到 %q", MessageSourceKind, source.Plugin)
	}
	got, ok, err := MessageSourceOf(source)
	if err != nil {
		t.Fatalf("折回来不该失败：%v", err)
	}
	if !ok || got != want {
		t.Fatalf("折回来该是同一份，得到 %+v", got)
	}
}

func TestMessageSource缺消息身份就折不出去(t *testing.T) {
	if _, err := NewMessageSource(MessageSource{Team: "team-1"}); !errors.Is(err, CodeInvalidArgument) {
		t.Fatalf("该报 %q，得到 %v", CodeInvalidArgument, err)
	}
}

func TestMessageSource别人的来源认不出来也不报错(t *testing.T) {
	if _, ok, err := MessageSourceOf(llm.PluginSource{Plugin: "别的插件"}); ok || err != nil {
		t.Fatalf("不是队友消息该安静交回假，得到 ok=%v err=%v", ok, err)
	}
	if _, ok, err := MessageSourceOf(nil); ok || err != nil {
		t.Fatalf("空来源该安静交回假，得到 ok=%v err=%v", ok, err)
	}
}
