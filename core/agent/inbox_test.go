// 本文件的作用：收件箱那份投影的全部行为——怎么从日志里重建、每一次改动怎么
// 先落日志再动投影、以及它守着的那两条不变量。

package agent

import (
	"encoding/json"
	"errors"
	"testing"

	"ds-harness-go/core/session"
	"ds-harness-go/llm"
	sessionlog "ds-harness-go/session"
)

// recorder 收下收件箱报出来的那三件事，按发生次序排着。
type recorder struct {
	inserted  []llm.MessageID
	discarded []llm.MessageID
	claimed   []claimRecord
}

// claimRecord 是一次认领报出来的那一对。
type claimRecord struct {
	id   llm.MessageID
	turn int
}

// notifications 交出一份把三件事都记下来的接线。
func (r *recorder) notifications() InboxNotifications {
	return InboxNotifications{
		Inserted:  func(m llm.Message) { r.inserted = append(r.inserted, m.ID) },
		Discarded: func(m llm.Message) { r.discarded = append(r.discarded, m.ID) },
		Claimed: func(m llm.Message, turn int) {
			r.claimed = append(r.claimed, claimRecord{id: m.ID, turn: turn})
		},
	}
}

// newInbox 造一个挂在全新游离会话上的收件箱。
func newInbox(t *testing.T, notify InboxNotifications) (*Inbox, *session.Session) {
	t.Helper()
	live := newFreeSession(t, "inbox", nil)
	inbox, err := NewInbox(live, notify)
	if err != nil {
		t.Fatalf("造收件箱失败：%v", err)
	}
	return inbox, live
}

// idsOf 把一条清单摊成身份，好拿来对比。
func idsOf(messages []llm.Message) []llm.MessageID {
	ids := make([]llm.MessageID, len(messages))
	for index, message := range messages {
		ids[index] = message.ID
	}
	return ids
}

// splicesOf 把一个会话日志里那些收件箱改动挑出来读回。
func splicesOf(t *testing.T, live *session.Session) []SplicedData {
	t.Helper()
	var splices []SplicedData
	for _, event := range live.Events() {
		if event.Type != EventInboxSpliced {
			continue
		}
		var splice SplicedData
		if err := json.Unmarshal(event.Data, &splice); err != nil {
			t.Fatalf("seq %d 上那条改动读不回来：%v", event.Seq, err)
		}
		splices = append(splices, splice)
	}
	return splices
}

// mustAppend 往一条清单末尾追一条，追不上去当场失败。
func mustAppend(t *testing.T, inbox *Inbox, target InboxTarget, message llm.Message) {
	t.Helper()
	if err := inbox.Append(target, message); err != nil {
		t.Fatalf("追加失败：%v", err)
	}
}

// TestNewInboxIgnoresSplicesBeforeTheSeedBoundary seed 前面那些改动属于父会话
// 那次生命周期，跟着重放会凭空造出一批已经跑过的活儿。
func TestNewInboxIgnoresSplicesBeforeTheSeedBoundary(t *testing.T) {
	seed := []sessionlog.Event{{
		Seq:  0,
		Type: EventInboxSpliced,
		Data: data(t, SplicedData{Target: NextTurn, Inserted: []llm.Message{text("父会话的活儿")}}),
	}}
	live := newFreeSession(t, "forked", seed)
	inbox, err := NewInbox(live, InboxNotifications{})
	if err != nil {
		t.Fatalf("造收件箱失败：%v", err)
	}
	if inbox.HasPending() {
		t.Fatalf("seed 里那条改动不该被重放：%v", inbox.NextTurn())
	}
}

// TestNewInboxReplaysLiveSplices 重建出来的形状要和当初那一份逐字相同。
func TestNewInboxReplaysLiveSplices(t *testing.T) {
	inbox, live := newInbox(t, InboxNotifications{})
	first, second, guidance := text("一"), text("二"), text("引导")
	mustAppend(t, inbox, NextTurn, first)
	mustAppend(t, inbox, NextTurn, second)
	mustAppend(t, inbox, NextStep, guidance)
	if _, err := inbox.Remove(first.ID); err != nil {
		t.Fatalf("拿掉失败：%v", err)
	}

	replayed, err := NewInbox(live, InboxNotifications{})
	if err != nil {
		t.Fatalf("重建失败：%v", err)
	}
	if got := idsOf(replayed.NextTurn()); len(got) != 1 || got[0] != second.ID {
		t.Fatalf("重建出来的 next-turn 不对：%v", got)
	}
	if got := idsOf(replayed.NextStep()); len(got) != 1 || got[0] != guidance.ID {
		t.Fatalf("重建出来的 next-step 不对：%v", got)
	}
}

// TestNewInboxRejectsUnreadableSplice 一条读不回来的改动让重建整个失败，而不是
// 悄悄跳过——投影和日志分了岔就再也对不上了。
func TestNewInboxRejectsUnreadableSplice(t *testing.T) {
	live := newFreeSession(t, "broken", nil)
	if _, err := live.Append(sessionlog.Event{
		Type: EventInboxSpliced,
		Data: json.RawMessage(`"不是个对象"`),
	}); err != nil {
		t.Fatalf("追加事件失败：%v", err)
	}
	if _, err := NewInbox(live, InboxNotifications{}); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("该报 ErrMalformedEvent，得到 %v", err)
	}
}

// TestNewInboxRejectsUnusableSplice 一条读得回来、却用不到当下这份投影上的改动
// 同样让重建失败。
func TestNewInboxRejectsUnusableSplice(t *testing.T) {
	live := newFreeSession(t, "outofrange", nil)
	if _, err := live.Append(sessionlog.Event{
		Type: EventInboxSpliced,
		Data: data(t, SplicedData{Target: NextTurn, Start: 3}),
	}); err != nil {
		t.Fatalf("追加事件失败：%v", err)
	}
	if _, err := NewInbox(live, InboxNotifications{}); !errors.Is(err, ErrInvalidSplice) {
		t.Fatalf("该报 ErrInvalidSplice，得到 %v", err)
	}
}

// TestNewInboxClampsASeedBoundaryPastTheLog 一条比日志还长的血统边界被夹到日志
// 末尾，而不是让重建下标越界。这种头来自续跑：那边的边界说的是当初分叉的位置，
// 和这个进程里这份日志的长度没有关系。
func TestNewInboxClampsASeedBoundaryPastTheLog(t *testing.T) {
	header := sessionlog.SessionHeader{ID: "short", Cwd: testAbsolutePath, SeedLength: 99}
	live, err := session.NewSession("short", session.Options{Header: &header, Now: fixedClock()})
	if err != nil {
		t.Fatalf("造会话失败：%v", err)
	}
	if _, err := live.Append(sessionlog.Event{
		Type: EventInboxSpliced,
		Data: data(t, SplicedData{Target: NextTurn, Inserted: []llm.Message{text("活儿")}}),
	}); err != nil {
		t.Fatalf("追加事件失败：%v", err)
	}
	inbox, err := NewInbox(live, InboxNotifications{})
	if err != nil {
		t.Fatalf("造收件箱失败：%v", err)
	}
	if inbox.HasPending() {
		t.Fatalf("边界该夹到日志末尾，整段都算 seed：%v", inbox.NextTurn())
	}
}

// TestInboxListsAreCopies 交出去的切片改不动投影。
func TestInboxListsAreCopies(t *testing.T) {
	inbox, _ := newInbox(t, InboxNotifications{})
	mustAppend(t, inbox, NextTurn, text("一"))
	mustAppend(t, inbox, NextStep, text("引导"))

	turn := inbox.NextTurn()
	turn[0] = text("换掉的")
	step := inbox.NextStep()
	step[0] = text("也换掉的")

	if inbox.NextTurn()[0].ID == turn[0].ID || inbox.NextStep()[0].ID == step[0].ID {
		t.Fatal("改交出去的那份切片不该动到投影")
	}
}

// TestInboxDoesNotShareContentWithTheCaller 投影里那条消息的内容是自己的一份：
// 调用方留着的那条消息之后怎么改，都改不动待办里这条。
func TestInboxDoesNotShareContentWithTheCaller(t *testing.T) {
	inbox, _ := newInbox(t, InboxNotifications{})
	message := text("原话")
	mustAppend(t, inbox, NextTurn, message)

	message.Content[0] = llm.TextBlock{Text: "改过的"}

	block, ok := inbox.NextTurn()[0].Content[0].(llm.TextBlock)
	if !ok || block.Text != "原话" {
		t.Fatalf("投影里那条内容被调用方改动了：%#v", inbox.NextTurn()[0].Content[0])
	}
}

// TestInboxHasPending 两条清单任意一条有活儿都算有。
func TestInboxHasPending(t *testing.T) {
	inbox, _ := newInbox(t, InboxNotifications{})
	if inbox.HasPending() {
		t.Fatal("刚造出来不该有待办")
	}
	mustAppend(t, inbox, NextStep, text("引导"))
	if !inbox.HasPending() {
		t.Fatal("next-step 上有活儿也算有待办")
	}
}

// TestInboxAppendAndPrependOrder 追加落在末尾，前插落在开头。
func TestInboxAppendAndPrependOrder(t *testing.T) {
	inbox, _ := newInbox(t, InboxNotifications{})
	middle, last, first := text("中"), text("尾"), text("头")
	mustAppend(t, inbox, NextTurn, middle)
	mustAppend(t, inbox, NextTurn, last)
	if err := inbox.Prepend(NextTurn, first); err != nil {
		t.Fatalf("前插失败：%v", err)
	}
	got := idsOf(inbox.NextTurn())
	want := []llm.MessageID{first.ID, middle.ID, last.ID}
	if len(got) != len(want) {
		t.Fatalf("次序不对：%v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("次序不对：%v", got)
		}
	}
}

// TestInboxClearEmptiesStepBeforeTurn 清空先清 next-step 再清 next-turn：反过来
// 的话中间那一瞬会有一批无主的引导挂在一条已经没有提示的清单上，而每一步都落
// 一条日志，所以那一瞬是看得见的。
func TestInboxClearEmptiesStepBeforeTurn(t *testing.T) {
	var log recorder
	inbox, live := newInbox(t, log.notifications())
	prompt, guidance := text("提示"), text("引导")
	mustAppend(t, inbox, NextTurn, prompt)
	mustAppend(t, inbox, NextStep, guidance)

	if err := inbox.Clear(); err != nil {
		t.Fatalf("清空失败：%v", err)
	}
	if inbox.HasPending() {
		t.Fatal("清完了还有待办")
	}

	splices := splicesOf(t, live)
	if len(splices) != 4 {
		t.Fatalf("该落下四条改动：%+v", splices)
	}
	if splices[2].Target != NextStep || splices[3].Target != NextTurn {
		t.Fatalf("清空的次序不对：%q 然后 %q", splices[2].Target, splices[3].Target)
	}
	if !splices[2].Canceled || !splices[3].Canceled {
		t.Fatalf("清空是取消，两条都该打标记：%+v", splices[2:])
	}
	if len(log.discarded) != 2 || log.discarded[0] != guidance.ID || log.discarded[1] != prompt.ID {
		t.Fatalf("丢弃报得不对：%v", log.discarded)
	}
}

// TestInboxClearOnAnEmptyInboxWritesNothing 没活儿可清时一条日志都不落。
func TestInboxClearOnAnEmptyInboxWritesNothing(t *testing.T) {
	inbox, live := newInbox(t, InboxNotifications{})
	if err := inbox.Clear(); err != nil {
		t.Fatalf("清空失败：%v", err)
	}
	if splices := splicesOf(t, live); len(splices) != 0 {
		t.Fatalf("空收件箱不该落下改动：%+v", splices)
	}
}

// TestInboxClaimNextStepTakesOnlyTheStepList 一次步骤边界上的认领只拿走
// next-step，队里那条提示留给它自己的回合。
func TestInboxClaimNextStepTakesOnlyTheStepList(t *testing.T) {
	var log recorder
	inbox, _ := newInbox(t, log.notifications())
	prompt, guidance := text("提示"), text("引导")
	mustAppend(t, inbox, NextTurn, prompt)
	mustAppend(t, inbox, NextStep, guidance)

	claimed, err := inbox.Claim(NextStep, 7)
	if err != nil {
		t.Fatalf("认领失败：%v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != guidance.ID {
		t.Fatalf("认领到的不对：%v", idsOf(claimed))
	}
	if got := idsOf(inbox.NextTurn()); len(got) != 1 || got[0] != prompt.ID {
		t.Fatalf("提示不该被拿走：%v", got)
	}
	if len(log.claimed) != 1 || log.claimed[0].turn != 7 {
		t.Fatalf("认领报得不对：%+v", log.claimed)
	}
}

// TestInboxClaimNextTurnAlsoTakesTheQueuedPrompt 一次回合边界上的认领在整条
// next-step 之外，再多带走队首那一条提示——只一条。
func TestInboxClaimNextTurnAlsoTakesTheQueuedPrompt(t *testing.T) {
	var log recorder
	inbox, live := newInbox(t, log.notifications())
	first, second, guidance := text("头一条"), text("下一条"), text("引导")
	mustAppend(t, inbox, NextTurn, first)
	mustAppend(t, inbox, NextTurn, second)
	mustAppend(t, inbox, NextStep, guidance)

	claimed, err := inbox.Claim(NextTurn, 3)
	if err != nil {
		t.Fatalf("认领失败：%v", err)
	}
	got := idsOf(claimed)
	if len(got) != 2 || got[0] != guidance.ID || got[1] != first.ID {
		t.Fatalf("认领到的不对：%v", got)
	}
	if left := idsOf(inbox.NextTurn()); len(left) != 1 || left[0] != second.ID {
		t.Fatalf("队里该只剩下一条：%v", left)
	}

	// 认领落下的是纯删除、不打取消标记：被认领的活儿马上就要跑，它由自己那个
	// 回合的 turn/end 交代。
	splices := splicesOf(t, live)
	for _, splice := range splices[3:] {
		if splice.Canceled {
			t.Fatalf("认领不该打取消标记：%+v", splice)
		}
	}
	if len(log.discarded) != 0 {
		t.Fatalf("认领不该报丢弃：%v", log.discarded)
	}
	if len(log.claimed) != 2 || log.claimed[0].turn != 3 || log.claimed[1].turn != 3 {
		t.Fatalf("认领报得不对：%+v", log.claimed)
	}
}

// TestInboxClaimOnAnEmptyInboxTakesNothing 没活儿可认时既不落日志也不报认领。
func TestInboxClaimOnAnEmptyInboxTakesNothing(t *testing.T) {
	var log recorder
	inbox, live := newInbox(t, log.notifications())
	claimed, err := inbox.Claim(NextTurn, 1)
	if err != nil {
		t.Fatalf("认领失败：%v", err)
	}
	if len(claimed) != 0 || len(log.claimed) != 0 {
		t.Fatalf("空收件箱不该认领到任何东西：%v", idsOf(claimed))
	}
	if splices := splicesOf(t, live); len(splices) != 0 {
		t.Fatalf("空收件箱不该落下改动：%+v", splices)
	}
}

// TestInboxReplaceSwapsIdentityInPlace 换掉一条待办：位置不动，身份跟着变，
// 旧的报丢弃、新的报插入。
func TestInboxReplaceSwapsIdentityInPlace(t *testing.T) {
	var log recorder
	inbox, _ := newInbox(t, log.notifications())
	first, second, replacement := text("一"), text("二"), text("换过的")
	mustAppend(t, inbox, NextTurn, first)
	mustAppend(t, inbox, NextTurn, second)

	replaced, err := inbox.Replace(first.ID, replacement)
	if err != nil {
		t.Fatalf("替换失败：%v", err)
	}
	if !replaced {
		t.Fatal("该换掉才对")
	}
	got := idsOf(inbox.NextTurn())
	if len(got) != 2 || got[0] != replacement.ID || got[1] != second.ID {
		t.Fatalf("换完的形状不对：%v", got)
	}
	if len(log.discarded) != 1 || log.discarded[0] != first.ID {
		t.Fatalf("旧的那条该报丢弃：%v", log.discarded)
	}
	if len(log.inserted) != 3 || log.inserted[2] != replacement.ID {
		t.Fatalf("新的那条该报插入：%v", log.inserted)
	}
}

// TestInboxReplaceMissingIsANoop 换一条不在待办里的消息什么都不做。
func TestInboxReplaceMissingIsANoop(t *testing.T) {
	inbox, live := newInbox(t, InboxNotifications{})
	replaced, err := inbox.Replace("没这条", text("换过的"))
	if err != nil {
		t.Fatalf("替换报错了：%v", err)
	}
	if replaced {
		t.Fatal("不在待办里就不该说换掉了")
	}
	if splices := splicesOf(t, live); len(splices) != 0 {
		t.Fatalf("不该落下改动：%+v", splices)
	}
}

// TestInboxRemoveFindsBothLists 拿掉一条待办：两条清单里都找得到。
func TestInboxRemoveFindsBothLists(t *testing.T) {
	var log recorder
	inbox, _ := newInbox(t, log.notifications())
	prompt, guidance := text("提示"), text("引导")
	mustAppend(t, inbox, NextTurn, prompt)
	mustAppend(t, inbox, NextStep, guidance)

	for _, message := range []llm.Message{prompt, guidance} {
		removed, err := inbox.Remove(message.ID)
		if err != nil {
			t.Fatalf("拿掉失败：%v", err)
		}
		if !removed {
			t.Fatalf("该拿掉 %q 才对", message.ID)
		}
	}
	if inbox.HasPending() {
		t.Fatal("两条都拿掉了还有待办")
	}
	if len(log.discarded) != 2 {
		t.Fatalf("两条都该报丢弃：%v", log.discarded)
	}
}

// TestInboxRemoveMissingIsANoop 拿掉一条不在待办里的消息什么都不做。
func TestInboxRemoveMissingIsANoop(t *testing.T) {
	inbox, live := newInbox(t, InboxNotifications{})
	removed, err := inbox.Remove("没这条")
	if err != nil {
		t.Fatalf("拿掉报错了：%v", err)
	}
	if removed {
		t.Fatal("不在待办里就不该说拿掉了")
	}
	if splices := splicesOf(t, live); len(splices) != 0 {
		t.Fatalf("不该落下改动：%+v", splices)
	}
}

// TestInboxSpliceClampsCoordinates 落进日志的必须是一份可以直接照做的坐标，
// 所以调用方给的越界坐标在写日志**之前**就夹好。
func TestInboxSpliceClampsCoordinates(t *testing.T) {
	cases := map[string]struct {
		start, deleteCount int
		wantStart, wantGot int
	}{
		"负的起点从末尾往回数": {start: -1, deleteCount: 1, wantStart: 1, wantGot: 1},
		"负得过头夹到开头":   {start: -9, deleteCount: 1, wantStart: 0, wantGot: 1},
		"起点超界夹到末尾":   {start: 9, deleteCount: 1, wantStart: 2, wantGot: 0},
		"删得比剩下的多":    {start: 1, deleteCount: 9, wantStart: 1, wantGot: 1},
		"负的删除数当零算":   {start: 0, deleteCount: -1, wantStart: 0, wantGot: 0},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			inbox, live := newInbox(t, InboxNotifications{})
			mustAppend(t, inbox, NextTurn, text("一"))
			mustAppend(t, inbox, NextTurn, text("二"))

			// 带上一条插入，好让那两支「夹完删除数为零」的样例也落得下事件。
			removed, err := inbox.Splice(
				NextTurn, testCase.start, testCase.deleteCount, []llm.Message{text("新的")},
			)
			if err != nil {
				t.Fatalf("改动失败：%v", err)
			}
			if len(removed) != testCase.wantGot {
				t.Fatalf("删掉的条数不对：%d", len(removed))
			}
			splices := splicesOf(t, live)
			last := splices[len(splices)-1]
			if last.Start != testCase.wantStart || last.RemovedCount != testCase.wantGot {
				t.Fatalf("落进日志的坐标没夹好：%+v", last)
			}
		})
	}
}

// TestInboxSpliceNoopWritesNothing 既不删也不插的改动一条日志都不落。
func TestInboxSpliceNoopWritesNothing(t *testing.T) {
	inbox, live := newInbox(t, InboxNotifications{})
	removed, err := inbox.Splice(NextTurn, 0, 0, nil)
	if err != nil {
		t.Fatalf("改动失败：%v", err)
	}
	if removed != nil {
		t.Fatalf("什么都没删该交回 nil：%v", idsOf(removed))
	}
	if splices := splicesOf(t, live); len(splices) != 0 {
		t.Fatalf("不该落下改动：%+v", splices)
	}
}

// TestInboxRejectsADuplicateIdentityAcrossLists 同一条消息同时挂在两条清单上
// 会被认领两次，所以这条不变量是**跨清单**验的。
func TestInboxRejectsADuplicateIdentityAcrossLists(t *testing.T) {
	inbox, _ := newInbox(t, InboxNotifications{})
	message := text("同一条")
	mustAppend(t, inbox, NextTurn, message)

	err := inbox.Append(NextStep, message)
	if !errors.Is(err, ErrInvalidSplice) {
		t.Fatalf("该报 ErrInvalidSplice，得到 %v", err)
	}
	if len(inbox.NextStep()) != 0 {
		t.Fatalf("验不过的改动不该动到投影：%v", idsOf(inbox.NextStep()))
	}
}

// TestInboxSpliceRejectsAnUnknownTarget 一个不认识的清单名在排日志那一步就被
// 拦下——[SplicedData] 自己守着那份清单名。
func TestInboxSpliceRejectsAnUnknownTarget(t *testing.T) {
	inbox, live := newInbox(t, InboxNotifications{})
	_, err := inbox.Splice("next-era", 0, 0, []llm.Message{text("新的")})
	if !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("该报 ErrMalformedEvent，得到 %v", err)
	}
	if splices := splicesOf(t, live); len(splices) != 0 {
		t.Fatalf("排不出去的改动不该落进日志：%+v", splices)
	}
	if inbox.HasPending() {
		t.Fatal("排不出去的改动不该动到投影")
	}
}

// TestInboxSurfacesAFailedAppend 日志写不下去时那次改动整个不算数：错误原样往上
// 交，投影一动不动。
//
// 五个入口都要各验一遍，因为「先落日志再动投影」这条约定是每个入口自己守着的
// ——哪一个把错误吞掉，它那一次就会只改投影不留日志，从此这份投影再也重建不出来。
func TestInboxSurfacesAFailedAppend(t *testing.T) {
	cases := map[string]struct {
		// seedStep 决定 next-step 上有没有活儿。Claim 的第二段只有在第一段整个
		// 空转（一条日志都不落）时才轮得到，所以它要一条空的 next-step。
		seedStep bool
		attempt  func(*Inbox) error
	}{
		"清空": {true, func(inbox *Inbox) error { return inbox.Clear() }},
		"认领 next-step": {true, func(inbox *Inbox) error {
			_, err := inbox.Claim(NextStep, 1)
			return err
		}},
		"认领 next-turn 那条队首": {false, func(inbox *Inbox) error {
			_, err := inbox.Claim(NextTurn, 1)
			return err
		}},
		"替换": {false, func(inbox *Inbox) error {
			_, err := inbox.Replace("要换的", text("换过的"))
			return err
		}},
		"拿掉": {false, func(inbox *Inbox) error {
			_, err := inbox.Remove("要换的")
			return err
		}},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			inbox, during := busyInbox(t)
			prompt := text("提示")
			prompt.ID = "要换的"
			mustAppend(t, inbox, NextTurn, prompt)
			if testCase.seedStep {
				mustAppend(t, inbox, NextStep, text("引导"))
			}
			before := len(inbox.NextTurn()) + len(inbox.NextStep())

			var err error
			during(func() { err = testCase.attempt(inbox) })

			if !errors.Is(err, session.ErrInvalidAppend) {
				t.Fatalf("该把日志那条错误原样交出来，得到 %v", err)
			}
			if got := len(inbox.NextTurn()) + len(inbox.NextStep()); got != before {
				t.Fatalf("落不下日志的改动不该动到投影：%d 变成 %d", before, got)
			}
		})
	}
}

// TestInboxWithoutNotificationsWorks 三个回调都不给时每一条路都照跑——回放和
// 诊断走的就是这条。
func TestInboxWithoutNotificationsWorks(t *testing.T) {
	inbox, _ := newInbox(t, InboxNotifications{})
	message := text("一")
	mustAppend(t, inbox, NextTurn, message)
	mustAppend(t, inbox, NextStep, text("引导"))
	if _, err := inbox.Claim(NextTurn, 1); err != nil {
		t.Fatalf("认领失败：%v", err)
	}
	mustAppend(t, inbox, NextTurn, text("二"))
	if err := inbox.Clear(); err != nil {
		t.Fatalf("清空失败：%v", err)
	}
	if inbox.HasPending() {
		t.Fatal("清完了还有待办")
	}
}
