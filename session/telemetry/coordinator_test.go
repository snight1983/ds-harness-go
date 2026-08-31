// 本文件的作用：那个采集协调器——建它要什么、五个采集点各自在什么条件下
// 才动、那个固定的分块投影、交接游标怎么往前走，以及失败被兜住之后留下的痕迹。
//
// 源: packages/session/session-telemetry/src/coordinator.ts

package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"ds-harness-go/llm"
	"ds-harness-go/session"
)

func TestBuildingACoordinatorNeedsSomewhereToPutTheRecords(t *testing.T) {
	t.Parallel()

	// 没有接收器的协调器采到的每一条都无处可去。这是唯一一个必填项，
	// 缺了它就该在装配期炸，而不是等到第一条记录采出来才发现。
	if _, err := New(Options{}); err == nil {
		t.Fatalf("没有接收器该建不出来")
	}
}

func TestTheClockAndTheLoggerBothHaveDefaults(t *testing.T) {
	t.Parallel()

	// 这两样留空是常态：真时钟和 slog.Default() 就是大多数装配方要的东西。
	// 留空**不该**变成「不记」——那几条 Warn 是失败被兜住之后唯一的痕迹。
	coordinator, err := New(Options{Sink: &recorder{}})
	if err != nil {
		t.Fatalf("建协调器不该失败：%v", err)
	}
	if coordinator.now == nil || coordinator.logger == nil {
		t.Fatalf("时钟和 logger 都该有兜底")
	}
	if coordinator.now() <= 0 {
		t.Fatalf("兜底的时钟该给出真时刻")
	}
}

func TestTheRuleChainIsCopiedAtConstruction(t *testing.T) {
	t.Parallel()

	// 规则链是部署方的配置，建好之后再往调用方那个切片上追加一条，不该能
	// 悄悄改到一个已经在跑的协调器——那等于绕过装配，从侧面挂一条脱敏规则。
	rules := []Rule{
		func(record Record, next func() (Record, error)) (Record, error) { return next() },
	}
	sink := &recorder{}
	coordinator, err := New(Options{Sink: sink, Rules: rules, Logger: quiet()})
	if err != nil {
		t.Fatalf("建协调器不该失败：%v", err)
	}
	rules = append(rules, func(Record, func() (Record, error)) (Record, error) {
		t.Fatalf("后加的规则不该跑到已经建好的协调器上")
		return Record{}, nil
	})
	_ = rules

	coordinator.Observe(newView("s1", userEvent(t, 1, 5)), userEvent(t, 1, 5))
	if sink.count() != 1 {
		t.Fatalf("那条记录该照样交出去：%d", sink.count())
	}
}

func TestAdoptingASessionHandsOffTheLogAboveTheSeed(t *testing.T) {
	t.Parallel()

	// 种子那一段早就以另一个身份离开过进程，再交一遍就是重复上报。
	// 所以没有游标时重放从 FirstLiveSeq 减一开始，而不是从 0 开始。
	fixture := newFixture(t)
	view := newView("s1", userEvent(t, 1, 11), userEvent(t, 2, 12), userEvent(t, 3, 13))
	view.firstLive = 3

	fixture.coordinator.Adopt(view)

	records := fixture.sink.taken()
	if len(records) != 1 {
		t.Fatalf("只该交种子之上那一条：%d", len(records))
	}
	if records[0].Attributes["event.seq"] != 3 {
		t.Fatalf("交出去的该是第 3 条：%v", records[0].Attributes)
	}
}

func TestAdoptingTheSameSessionTwiceHandsOffNothingMore(t *testing.T) {
	t.Parallel()

	// 装配方在建会话时调一次 Adopt，装配好之后又扫一遍已经活着的会话——
	// 同一个会话被认领两次是常态，第二次必须是空操作。
	fixture := newFixture(t)
	view := newView("s1", userEvent(t, 1, 11))

	fixture.coordinator.Adopt(view)
	fixture.coordinator.Adopt(view)

	if fixture.sink.count() != 1 {
		t.Fatalf("第二次认领不该再交一遍：%d", fixture.sink.count())
	}
}

func TestCapturingOnDemandLeavesNoOperationalTrace(t *testing.T) {
	t.Parallel()

	// on-demand 采集的全部就是这一次调用：它不认领，于是这个会话退休或者
	// 整个协调器关掉时都不会有 ops 记录——「on-demand 不产生 ops 记录」
	// 这条行为是这么自动成立的，不靠任何开关字段。
	fixture := newFixture(t)
	view := newView("s1", userEvent(t, 1, 11))

	fixture.coordinator.CaptureSession(view)
	fixture.coordinator.Retire(view)
	if err := fixture.coordinator.Close(context.Background()); err != nil {
		t.Fatalf("关掉不该失败：%v", err)
	}

	records := fixture.sink.taken()
	if len(records) != 1 || records[0].Channel != ChannelLedger {
		t.Fatalf("只该有那一条 ledger 记录：%#v", records)
	}
}

func TestCapturingThroughStopsAtTheRequestedSeqInclusive(t *testing.T) {
	t.Parallel()

	fixture := newFixture(t)
	view := newView("s1", userEvent(t, 1, 11), userEvent(t, 2, 12), userEvent(t, 3, 13))

	fixture.coordinator.CaptureThrough(view, 2)

	records := fixture.sink.taken()
	if len(records) != 2 {
		t.Fatalf("该停在第 2 条（含）：%d", len(records))
	}
	if records[1].Attributes["event.seq"] != 2 {
		t.Fatalf("最后一条该是第 2 条：%v", records[1].Attributes)
	}
}

func TestObservingAnEventHandsItOffAndAdvancesTheCursor(t *testing.T) {
	t.Parallel()

	// 游标标的是「交出去了」。它往前走之后，同一条事件再重放一遍不该再交。
	fixture := newFixture(t)
	view := newView("s1", userEvent(t, 1, 11))

	fixture.coordinator.Observe(view, view.events[0])
	fixture.coordinator.CaptureSession(view)

	if fixture.sink.count() != 1 {
		t.Fatalf("游标之下那条不该再交一遍：%d", fixture.sink.count())
	}
}

func TestTheLedgerRecordCarriesTheEventsOwnTimeAndPayload(t *testing.T) {
	t.Parallel()

	// ledger 记录的 time 是源事件的追加时刻，不是发出这条记录的时刻——
	// 接收方靠它排出会话里真实发生的先后。
	fixture := newFixture(t)
	view := newView("s1", userEvent(t, 1, 11))

	fixture.coordinator.Observe(view, view.events[0])

	records := fixture.sink.taken()
	if records[0].Time != 11 {
		t.Fatalf("该用事件自己的时刻：%d", records[0].Time)
	}
	if !json.Valid(records[0].Body) || string(records[0].Body) != string(view.events[0].Data) {
		t.Fatalf("body 该是那条事件的负载：%s", records[0].Body)
	}
}

func TestTheRecordsBodyIsACopyOfTheEventsBytes(t *testing.T) {
	t.Parallel()

	// 会话日志是权威事实，交出去的那一份**永远不许**反过来改到它。
	// 一条脱敏规则原地改 body 是完全合法的，所以这里必须是副本。
	fixture := newFixture(t)
	view := newView("s1", userEvent(t, 1, 11))
	original := string(view.events[0].Data)

	fixture.coordinator.Observe(view, view.events[0])
	fixture.sink.taken()[0].Body[0] = ' '

	if string(view.events[0].Data) != original {
		t.Fatalf("动到记录不该动到日志：%s", view.events[0].Data)
	}
}

func TestOnlyTheFirstChunkOfAStepGoesOut(t *testing.T) {
	t.Parallel()

	// 固定的分块投影：一个 (turn, step) 只交第一条，当作「这个步骤开始出字了」
	// 这一个信号。内容不会丢——它在这个步骤装配好的 assistant/message 里是完整的。
	fixture := newFixture(t)
	view := newView("s1",
		chunkEvent(t, 1, 11, 0, 0),
		chunkEvent(t, 2, 12, 0, 0),
		chunkEvent(t, 3, 13, 0, 1),
		chunkEvent(t, 4, 14, 1, 0))

	fixture.coordinator.CaptureSession(view)

	records := fixture.sink.taken()
	seqs := []any{}
	for _, record := range records {
		seqs = append(seqs, record.Attributes["event.seq"])
	}
	if !reflect.DeepEqual(seqs, []any{1, 3, 4}) {
		t.Fatalf("每个步骤只该出第一条：%v", seqs)
	}
}

func TestADroppedChunkDoesNotAdvanceTheCursor(t *testing.T) {
	t.Parallel()

	// 被投影丢掉的分块**不推进游标**，于是一个重新认领的协调器会确定性地
	// 再丢一遍同一批，而不是把它们当成「没交过」补上去。
	fixture := newFixture(t)
	view := newView("s1", chunkEvent(t, 1, 11, 0, 0), chunkEvent(t, 2, 12, 0, 0))

	fixture.coordinator.CaptureSession(view)

	if got := fixture.coordinator.cursor["s1"]; got != 1 {
		t.Fatalf("游标该停在交出去的那一条上：%d", got)
	}
}

func TestEventsBelowTheCursorStillFeedTheChunkProjection(t *testing.T) {
	t.Parallel()

	// 游标之下那一半只喂投影、不再交一遍。这一条的意义在于：一个从中途
	// 接手的协调器，丢掉的是和当初看着这个步骤开始的那个协调器**完全一样**
	// 的那批分块——同一个步骤的第二条分块在两边都被丢。
	fixture := newFixture(t)
	view := newView("s1",
		userEvent(t, 1, 11),
		chunkEvent(t, 2, 12, 0, 0),
		chunkEvent(t, 3, 13, 0, 0))
	view.firstLive = 3

	fixture.coordinator.CaptureSession(view)

	if fixture.sink.count() != 0 {
		t.Fatalf("第 3 条那个步骤的第一条已经在种子里出过了：%#v", fixture.sink.taken())
	}
}

func TestFeedingTheProjectionIgnoresEverythingItCannotKey(t *testing.T) {
	t.Parallel()

	// 游标之下的非分块事件、以及负载读不回来的分块，在喂投影这一步都是
	// 静默的空操作：它们本来就不会被交出去，读不出键也就没有什么要记的。
	fixture := newFixture(t)
	view := newView("s1",
		userEvent(t, 1, 11),
		broken(session.EventAssistantChunk, 2),
		chunkEvent(t, 3, 13, 0, 0))
	view.firstLive = 3

	fixture.coordinator.CaptureSession(view)

	if fixture.sink.count() != 1 {
		t.Fatalf("只该交第 3 条：%#v", fixture.sink.taken())
	}
	if len(fixture.logs.messages()) != 0 {
		t.Fatalf("游标之下的事件不该留下任何日志：%v", fixture.logs.messages())
	}
}

func TestAChunkWhosePayloadIsUnreadableIsWithheld(t *testing.T) {
	t.Parallel()

	// 拼不出投影键就无从判重，放行等于让一次追加侧的缺陷把同一个步骤的
	// 每一条分块都送上去。扣下丢掉的只是「这个步骤开始出字了」这一个信号。
	fixture := newFixture(t)
	view := newView("s1", broken(session.EventAssistantChunk, 1))

	fixture.coordinator.CaptureSession(view)

	if fixture.sink.count() != 0 {
		t.Fatalf("该被扣下：%#v", fixture.sink.taken())
	}
	if len(fixture.logs.messages()) != 1 {
		t.Fatalf("扣下该留一条痕迹：%v", fixture.logs.messages())
	}
	if fixture.logs.attr(0, "session") != "s1" {
		t.Fatalf("日志上该说清是哪个会话：%q", fixture.logs.attr(0, "session"))
	}
}

func TestSeverityComesFromTheEventsOwnOutcomeBit(t *testing.T) {
	t.Parallel()

	// 采集时就把级别定下来，接收方零配置就能报警。只有两种事件说得出
	// 「我出问题了」，别的一律 info——包括这个构建根本没听说过的类型。
	cases := map[string]struct {
		event session.Event
		want  Severity
	}{
		"工具结果说自己出错了": {toolResultEvent(t, 1, 11, true), SeverityError},
		"工具结果说自己没事":  {toolResultEvent(t, 1, 11, false), SeverityInfo},
		"工具结果的负载读不回来": {
			broken(session.EventToolResult, 1), SeverityInfo,
		},
		"回合以出错收场": {
			turnEndEvent(t, 1, 11, session.ErrorTurnEnd{
				Error: llm.Failure{Message: "boom"},
			}),
			SeverityError,
		},
		"回合正常收场": {
			turnEndEvent(t, 1, 11, session.CompletedTurnEnd{}), SeverityInfo,
		},
		"回合结束的负载读不回来": {
			broken(session.EventTurnEnd, 1), SeverityInfo,
		},
		"没有结果位的事件": {userEvent(t, 1, 11), SeverityInfo},
		"没听说过的类型": {
			session.Event{Type: "vendor/thing", Seq: 1, Time: 11}, SeverityInfo,
		},
	}

	for name, item := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := severityOf(item.event); got != item.want {
				t.Fatalf("级别该是 %q，实际 %q", item.want, got)
			}
		})
	}
}

func TestTheIdentityAttributesAreTheEnvelopePlusWhatTheHeaderReallyHas(t *testing.T) {
	t.Parallel()

	// 身份属性刻意只留最少的几个：凡是从 body 里就能拿回来的一律不重复一遍。
	// 会话头上那三样只在真有值的时候才出现——缺席和零值在介质上本来就是同一件事。
	bare := identityOf("s1", session.SessionHeader{Version: 1}, userEvent(t, 7, 11))
	want := map[string]any{"session.id": "s1", "event.type": "user/message", "event.seq": 7}
	if !reflect.DeepEqual(bare, want) {
		t.Fatalf("光秃秃的会话头只该给出信封那三样：%v", bare)
	}

	full := identityOf("s1", session.SessionHeader{
		Version: 1, Cwd: "/w", ParentSession: "p1", SeedLength: 4,
	}, userEvent(t, 7, 11))
	if full["session.cwd"] != "/w" || full["session.parent_id"] != "p1" ||
		full["session.seed_length"] != 4 {
		t.Fatalf("会话头上有的那几样该带上：%v", full)
	}
}

func TestAnEventWithNoPayloadStillHasABody(t *testing.T) {
	t.Parallel()

	// 空负载是 `{}`，和 [session.Event.MarshalJSON] 同一条规则。补齐一次，
	// body 就永远是一段合法 JSON，也就不会因为一条本来没有负载的事件被扣下。
	fixture := newFixture(t)
	view := newView("s1", session.Event{Type: session.EventTurnStart, Seq: 1, Time: 11})

	fixture.coordinator.CaptureSession(view)

	records := fixture.sink.taken()
	if len(records) != 1 || string(records[0].Body) != `{}` {
		t.Fatalf("空负载该补成 {}：%#v", records)
	}
}

func TestTheFlushHintOnlyReachesASinkThatAsksForItOnAnAdoptedSession(t *testing.T) {
	t.Parallel()

	// 只对已认领的会话有效：on-demand 采集没有「回合」这个概念。
	flushing := &flushingRecorder{recorder: &recorder{}}
	coordinator, err := New(Options{Sink: flushing, Now: func() int64 { return 1000 }, Logger: quiet()})
	if err != nil {
		t.Fatalf("建协调器不该失败：%v", err)
	}
	adopted := newView("s1")
	stranger := newView("s2")

	coordinator.HintFlush(stranger)
	if flushing.flushed() != 0 {
		t.Fatalf("没认领的会话不该触发排空：%d", flushing.flushed())
	}

	coordinator.Adopt(adopted)
	coordinator.HintFlush(adopted)
	if flushing.flushed() != 1 {
		t.Fatalf("认领过的会话该转一次：%d", flushing.flushed())
	}
}

func TestASinkThatDoesNotWantTheHintNeverSeesIt(t *testing.T) {
	t.Parallel()

	// 大多数接收器就该不实现 [Flusher]，让 SDK 自己的攒批节奏说了算。
	// 这条路必须是纯粹的空操作，不能因为接收器「少实现了一个方法」就报错。
	fixture := newFixture(t)
	view := newView("s1")

	fixture.coordinator.Adopt(view)
	fixture.coordinator.HintFlush(view)

	if len(fixture.logs.messages()) != 0 {
		t.Fatalf("不该有任何抱怨：%v", fixture.logs.messages())
	}
}

func TestRetiringAnAdoptedSessionLeavesAShutdownMarker(t *testing.T) {
	t.Parallel()

	fixture := newFixture(t)
	view := newView("s1")

	fixture.coordinator.Adopt(view)
	fixture.coordinator.Retire(view)

	records := fixture.sink.taken()
	if len(records) != 1 {
		t.Fatalf("该补一条退休记录：%#v", records)
	}
	want := Record{
		Channel: ChannelOps, Time: 1000, Severity: SeverityInfo,
		Attributes: map[string]any{"telemetry.op": "shutdown", "session.id": "s1"},
		Body:       json.RawMessage(`{"op":"shutdown"}`),
	}
	if !reflect.DeepEqual(records[0], want) {
		t.Fatalf("退休记录的样子不对：%#v", records[0])
	}
}

func TestRetiringASessionNobodyAdoptedIsANoOp(t *testing.T) {
	t.Parallel()

	fixture := newFixture(t)

	fixture.coordinator.Retire(newView("s1"))

	if fixture.sink.count() != 0 {
		t.Fatalf("没认领过的会话退休不该有记录：%#v", fixture.sink.taken())
	}
}

func TestRetiringForgetsTheSessionSoTheNextAdoptionStartsOver(t *testing.T) {
	t.Parallel()

	// 两张表按会话 id 归档，不在退休时删掉就是泄漏。删掉之后同一个 id 再被
	// 认领，会从种子长度重新交起——和 DSH 那边拿到一个新 Session 对象时一样。
	fixture := newFixture(t)
	view := newView("s1", userEvent(t, 1, 11))

	fixture.coordinator.Adopt(view)
	fixture.coordinator.Retire(view)
	fixture.coordinator.Adopt(view)

	ledger := 0
	for _, record := range fixture.sink.taken() {
		if record.Channel == ChannelLedger {
			ledger++
		}
	}
	if ledger != 2 {
		t.Fatalf("重新认领该整段重新交：%d", ledger)
	}
	if _, tracked := fixture.coordinator.cursor["s1"]; !tracked {
		t.Fatalf("重新认领之后游标该又建起来了")
	}
}

func TestRelayingAnAgentErrorProducesTheOperationalRecord(t *testing.T) {
	t.Parallel()

	// 这是唯一一个在会话日志里没有家的信号，所以只能走 ops 通道。
	fixture := newFixture(t)

	fixture.coordinator.RelayError(newView("s1"), "a1", 2, 3, errors.New("炸了"))

	records := fixture.sink.taken()
	if len(records) != 1 {
		t.Fatalf("该有一条记录：%#v", records)
	}
	got := records[0]
	if got.Channel != ChannelOps || got.Severity != SeverityError || got.Time != 1000 {
		t.Fatalf("信封不对：%#v", got)
	}
	want := map[string]any{
		"telemetry.op": "agent-error", "session.id": "s1", "agent.id": "a1",
		"error.name": "*errors.errorString", "turn": 2, "step": 3,
	}
	if !reflect.DeepEqual(got.Attributes, want) {
		t.Fatalf("属性不对：%v", got.Attributes)
	}
	if string(got.Body) != `{"name":"*errors.errorString","message":"炸了"}` {
		t.Fatalf("负载不对：%s", got.Body)
	}
}

func TestAnErrorsNameIsItsDynamicType(t *testing.T) {
	t.Parallel()

	// Go 的 error 没有名字，最接近的东西是它的动态类型：同一类失败的类型是
	// 同一个，正好用来分组，而这正是 name 在接收方那边的用途。
	name, message := errorDetail(nil)
	if name != "nil" || message != "" {
		t.Fatalf("没有错误时该给出一对确定的值：%q %q", name, message)
	}
}

func TestClosingLeavesAShutdownMarkerForEverySessionStillAdopted(t *testing.T) {
	t.Parallel()

	// 走到这里还认领着的，是一路活到整个应用关闭的那些会话。在接收器静默
	// 之前先把它们的记录留下，否则这批会话在介质上看起来就是「没有结尾」。
	fixture := newFixture(t)
	fixture.coordinator.Adopt(newView("s2"))
	fixture.coordinator.Adopt(newView("s1"))

	if err := fixture.coordinator.Close(context.Background()); err != nil {
		t.Fatalf("关掉不该失败：%v", err)
	}

	ids := []string{}
	for _, record := range fixture.sink.taken() {
		ids = append(ids, record.Attributes["session.id"].(string))
	}
	// 排序是为了产出可复现：Go 的 map 遍历顺序是随机的，一串顺序不定的
	// 记录进了断言就会随机地不一样。
	if !slices.Equal(ids, []string{"s1", "s2"}) {
		t.Fatalf("退休记录该按会话 id 排好序：%v", ids)
	}
	if fixture.sink.shutdowns != 1 {
		t.Fatalf("接收器该被排空一次：%d", fixture.sink.shutdowns)
	}
}

func TestClosingTwiceOnlySaysGoodbyeOnce(t *testing.T) {
	t.Parallel()

	fixture := newFixture(t)
	fixture.coordinator.Adopt(newView("s1"))

	if err := fixture.coordinator.Close(context.Background()); err != nil {
		t.Fatalf("第一次关不该失败：%v", err)
	}
	if err := fixture.coordinator.Close(context.Background()); err != nil {
		t.Fatalf("第二次关不该失败：%v", err)
	}

	if fixture.sink.count() != 1 || fixture.sink.shutdowns != 1 {
		t.Fatalf("第二次该是空操作：%d %d", fixture.sink.count(), fixture.sink.shutdowns)
	}
}

func TestEveryCapturePointGoesQuietAfterClose(t *testing.T) {
	t.Parallel()

	// 关掉之后接收器已经静默了，再往它身上交东西没有意义，也可能已经不安全。
	fixture := newFixture(t)
	view := newView("s1", userEvent(t, 1, 11))
	fixture.coordinator.Adopt(view)
	if err := fixture.coordinator.Close(context.Background()); err != nil {
		t.Fatalf("关掉不该失败：%v", err)
	}
	before := fixture.sink.count()

	fixture.coordinator.Adopt(newView("s2", userEvent(t, 1, 11)))
	fixture.coordinator.CaptureSession(view)
	fixture.coordinator.Observe(view, view.events[0])
	fixture.coordinator.HintFlush(view)
	fixture.coordinator.Retire(view)
	fixture.coordinator.RelayError(view, "a1", 0, 0, errors.New("炸了"))

	if fixture.sink.count() != before {
		t.Fatalf("关掉之后不该再有记录：%#v", fixture.sink.taken()[before:])
	}
}

func TestASinkThatFailsToDrainIsBothLoggedAndReported(t *testing.T) {
	t.Parallel()

	// 排空失败只记一条警告并原样返回：由调用方决定要不要理它，但按契约它
	// 不该让装配方的卸载失败——尽力而为的上报没有资格拆掉应用的关闭流程。
	refused := errors.New("排不空")
	fixture := newFixtureOn(t, &recorder{shutdownErr: refused})

	err := fixture.coordinator.Close(context.Background())

	if !errors.Is(err, refused) {
		t.Fatalf("该把接收器的错误交上来：%v", err)
	}
	if len(fixture.logs.messages()) != 1 ||
		!strings.Contains(fixture.logs.messages()[0], "接收器排空失败") {
		t.Fatalf("该留一条痕迹：%v", fixture.logs.messages())
	}
}

func TestAShutdownMarkerWithheldAtCloseIsStillRecorded(t *testing.T) {
	t.Parallel()

	// 关闭途中一条退休记录被扣下，剩下的会话照样收尾。它是唯一一条不走
	// contain 的交付，所以那条警告得在 Close 自己身上。
	fixture := newFixture(t, func(Record, func() (Record, error)) (Record, error) {
		return Record{}, errors.New("一条都不许出去")
	})
	fixture.coordinator.Adopt(newView("s1"))

	if err := fixture.coordinator.Close(context.Background()); err != nil {
		t.Fatalf("关掉不该失败：%v", err)
	}

	if len(fixture.logs.messages()) != 1 ||
		!strings.Contains(fixture.logs.messages()[0], "退休记录交不出去") {
		t.Fatalf("该留一条痕迹：%v", fixture.logs.messages())
	}
}

func TestARuleThatWithholdsARecordLeavesAWarning(t *testing.T) {
	t.Parallel()

	// 兜住了和根本没发生，在别的地方分不出来，只能在日志里分。
	fixture := newFixture(t, func(Record, func() (Record, error)) (Record, error) {
		return Record{}, errors.New("这条不许出去")
	})
	view := newView("s1", userEvent(t, 1, 11))

	fixture.coordinator.Observe(view, view.events[0])

	if fixture.sink.count() != 0 {
		t.Fatalf("该被扣下：%#v", fixture.sink.taken())
	}
	if fixture.logs.attr(0, "step") != "投影一条事件" {
		t.Fatalf("日志上该说清是哪一步：%q", fixture.logs.attr(0, "step"))
	}
}

func TestARuleThatProducesAnUnserializableRecordIsCaughtBeforeHandoff(t *testing.T) {
	t.Parallel()

	// 这正是 [Record.Validate] 存在的理由：一条属性值类型不对的记录会在
	// 接收器排 OTLP 的时候炸，那时候它已经离开采集侧了，没人查得出来是谁写坏的。
	fixture := newFixture(t, func(record Record, next func() (Record, error)) (Record, error) {
		inner, err := next()
		if err != nil {
			return Record{}, err
		}
		inner = inner.Clone()
		inner.Attributes["session.live"] = true
		return inner, nil
	})
	view := newView("s1", userEvent(t, 1, 11))

	fixture.coordinator.Observe(view, view.events[0])

	if fixture.sink.count() != 0 {
		t.Fatalf("该在交出去之前被拦住：%#v", fixture.sink.taken())
	}
	if len(fixture.logs.messages()) != 1 {
		t.Fatalf("该留一条痕迹：%v", fixture.logs.messages())
	}
}

func TestASinkThatPanicsDoesNotTakeTheLoopWithIt(t *testing.T) {
	t.Parallel()

	// 采集同步跑在 agent 循环的事件路径上，而规则和接收器都是部署方挂上来的
	// 代码。它们炸了不该把循环一起炸掉——上报是尽力而为的观察，不是业务。
	fixture := newFixtureOn(t, &recorder{emitPanic: "接收器炸了"})
	view := newView("s1", userEvent(t, 1, 11))

	fixture.coordinator.Observe(view, view.events[0])

	if len(fixture.logs.messages()) != 1 ||
		!strings.Contains(fixture.logs.messages()[0], "采集这一步炸了") {
		t.Fatalf("panic 该被兜住并记下来：%v", fixture.logs.messages())
	}
	if fixture.logs.attr(0, "panic") != "接收器炸了" {
		t.Fatalf("日志上该带着 panic 的内容：%q", fixture.logs.attr(0, "panic"))
	}
}

func TestOneBadEventDoesNotStopTheRestOfAReplay(t *testing.T) {
	t.Parallel()

	// 兜是逐条的：重放里一条被扣下，后面的照跑。整段重放**因为一条坏事件**
	// 而中断，等于让日志里靠后的那一整段永远交不出去。
	fixture := newFixture(t)
	view := newView("s1",
		userEvent(t, 1, 11),
		broken(session.EventAssistantChunk, 2),
		userEvent(t, 3, 13))

	fixture.coordinator.CaptureSession(view)

	if fixture.sink.count() != 2 {
		t.Fatalf("坏的那条之后该接着跑：%#v", fixture.sink.taken())
	}
}

func TestTheCoordinatorTakesEventsFromManyGoroutinesAtOnce(t *testing.T) {
	t.Parallel()

	// 同一个协调器会被多个会话的推进线程同时调。游标和分块表都是读改写，
	// 这条用例在 -race 下跑的就是那把锁到底罩没罩住它们。
	fixture := newFixture(t)
	var workers sync.WaitGroup
	for worker := range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()

			view := newView(session.SessionID(string(rune('a'+worker))), userEvent(t, 1, 11))
			fixture.coordinator.Adopt(view)
			fixture.coordinator.Observe(view, view.events[0])
			fixture.coordinator.HintFlush(view)
			fixture.coordinator.Retire(view)
		}()
	}
	workers.Wait()

	if err := fixture.coordinator.Close(context.Background()); err != nil {
		t.Fatalf("关掉不该失败：%v", err)
	}
}

func TestTheDefaultLoggerIsNotADiscard(t *testing.T) {
	t.Parallel()

	// 留空 Logger 记的正是没人会主动去查、却必须留下痕迹的那类事，
	// 所以兜底必须是 slog.Default() 而不是丢弃。
	coordinator, err := New(Options{Sink: &recorder{}})
	if err != nil {
		t.Fatalf("建协调器不该失败：%v", err)
	}
	if coordinator.logger != slog.Default() {
		t.Fatalf("兜底该是 slog.Default()")
	}
}
