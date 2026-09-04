// 本文件的作用：把那三件工具钉在它们真会出错的边上——「恰好一个选择器」这条
// schema 表达不出来的约束、两道屏障各自漏掉会怎样、一条坏掉的流怎么被关在包里、
// 删一个不存在的 id 为什么不算错误，以及那份给模型的字节为什么不许被转义改掉。
//
// # 这些测试防的是什么错
//
//   - **半个选择器或者两个选择器被放行**。参数根在 DSH 那边是开的，schema 拦不住
//     「一个都没给」和「给了两个」，只有这一层拦得住；漏掉就等于让模型随手写出一条
//     语义不明的提醒。
//   - **后置屏障被省掉**。省掉它，模型会拿到一条「建好了」的回执，而那条提醒在它
//     下次开会话时根本不在——这是这三件工具最贵的一种错。
//   - **内部诊断被带给模型**。折日志报出来的那句话里有日志内部的形状；工具结果里
//     只许出现那句固定的英文加一个码。
//   - **删一个不存在的 id 被报成错误**。那是一次正常的、幂等的操作，报成错误会逼
//     模型去处理一件根本不用处理的事。
//   - **空清单排成 null**。模型看见 null 读不出「一个都没有」，那份输出契约也验不过。
//   - **给模型的字节被 HTML 转义改掉**。Go 默认把 `<` 转成 `<`，DSH 不转。
//   - **这三件工具替别人的会话写东西**。一次装配错误会让它们落在别人的作用域上。

package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/tools"
)

// toolWorld 是一次工具用例要的全部家当。
type toolWorld struct {
	t        *testing.T
	root     *scope.Scope
	owner    *stubAgent
	sessions *stubSessions
	set      *toolSet
	// notified 是那条「有东西落盘了」的通知被叫过几次。
	notified int
	// now 是这个世界里的墙上时钟，用例可以直接改。
	now time.Time
}

// baseNow 是所有工具用例共用的那个时刻。
var baseNow = time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

func newToolWorld(t *testing.T) *toolWorld {
	t.Helper()
	root := scopeOf(t, "tools-root", nil)
	owner := newStubAgent(t, "owner", root, nil)
	world := &toolWorld{
		t: t, root: root, owner: owner, sessions: newStubSessions(), now: baseNow,
	}
	world.set = &toolSet{
		owner:           owner,
		sessions:        world.sessions,
		transactions:    newTransactions(),
		now:             func() time.Time { return world.now },
		onDurableChange: func() { world.notified++ },
	}
	return world
}

// exec 是一份落在属主身上的执行上下文。
func (w *toolWorld) exec() *tools.RunContext { return execOn(w.owner.Scope()) }

// call 跑一件工具，并要求它没有以 Go 错误的形式失败。
func (w *toolWorld) call(
	body func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error),
	args string,
) json.RawMessage {
	w.t.Helper()
	raw, err := body(w.t.Context(), json.RawMessage(args), w.exec())
	if err != nil {
		w.t.Fatalf("工具本该把失败排进结果里，却交回了 Go 错误：%v", err)
	}
	return raw
}

// createView 建一条提醒并把结果读成一份视图。
func (w *toolWorld) createView(args string) View {
	w.t.Helper()
	var view View
	decodeInto(w.t, w.call(w.set.create, args), &view)
	if view.ID == "" {
		w.t.Fatalf("这份结果不是一份视图：%s", w.call(w.set.create, args))
	}
	return view
}

func TestCreateToolBuildsEachKindOfRule(t *testing.T) {
	world := newToolWorld(t)

	after := world.createView(`{"prompt":"喝水","after_seconds":60}`)
	if after.Kind != KindAfter || after.ScheduledAt != "2026-08-30T12:01:00.000Z" {
		t.Fatalf("after 建出来的是 %+v", after)
	}
	if after.State != StateScheduled || after.DeliveryMode != DeliverySessionLocal {
		t.Fatalf("after 的投影是 %+v", after)
	}

	at := world.createView(`{"prompt":"开会","at":"2026-08-30T13:00:00Z"}`)
	if at.Kind != KindAt || at.ScheduledAt != "2026-08-30T13:00:00.000Z" {
		t.Fatalf("at 建出来的是 %+v", at)
	}

	local := world.createView(
		`{"prompt":"下班","at":{"date":"2026-08-30","time":"20:30:00","time_zone":"Asia/Shanghai"}}`)
	if local.ScheduledAt != "2026-08-30T12:30:00.000Z" {
		t.Fatalf("本地日历那一支建出来的是 %+v", local)
	}

	every := world.createView(`{"prompt":"站起来","every_seconds":300}`)
	if every.Kind != KindEvery || every.EverySeconds != 300 ||
		every.ScheduledAt != "2026-08-30T12:05:00.000Z" {
		t.Fatalf("every 建出来的是 %+v", every)
	}

	// 四条各占一个新号，一个都不重用。
	if after.ID != "schedule-1" || at.ID != "schedule-2" ||
		local.ID != "schedule-3" || every.ID != "schedule-4" {
		t.Fatalf("分到的 id 是 %q %q %q %q", after.ID, at.ID, local.ID, every.ID)
	}
	if world.owner.changeCount() != 4 {
		t.Fatalf("日志里落了 %d 条改动", world.owner.changeCount())
	}
}

func TestCreateToolAcceptsIntegralFloat(t *testing.T) {
	// 60.0 和 60 在 JS 那边是同一个数，模型两种都写得出来。收下它是有意的。
	world := newToolWorld(t)
	view := world.createView(`{"prompt":"p","after_seconds":60.0}`)
	if view.AfterSeconds != 60 {
		t.Fatalf("60.0 折成了 %d", view.AfterSeconds)
	}
}

func TestCreateToolRejectsSelectorShapes(t *testing.T) {
	world := newToolWorld(t)
	for _, args := range []string{
		`{"prompt":"p"}`, // 一个都没给
		`{"prompt":"p","after_seconds":60,"at":"x"}`,  // 给了两个
		`{"prompt":"p","after_seconds":60,"extra":1}`, // 多了一个不认得的键
		`[1]`,  // 根本不是对象
		`null`, // 是 null
	} {
		failure := toolErrorOf(t, world.call(world.set.create, args))
		if failure.Code != CodeInvalidSelector {
			t.Fatalf("create(%s) 报的是 %v，本该是 invalid_selector", args, failure.Code)
		}
	}
	if world.owner.changeCount() != 0 {
		t.Fatal("被拒的调用不该在日志里留下任何东西")
	}
}

func TestCreateToolRejectsBadArguments(t *testing.T) {
	world := newToolWorld(t)
	for _, each := range []struct {
		args string
		code ErrorCode
	}{
		{`{"prompt":"   ","after_seconds":60}`, CodeInvalidPrompt},
		{`{"prompt":7,"after_seconds":60}`, CodeInvalidPrompt},
		{`{"prompt":"p","after_seconds":0}`, CodeInvalidRule},
		{`{"prompt":"p","after_seconds":1.5}`, CodeInvalidRule},
		{`{"prompt":"p","after_seconds":"60"}`, CodeInvalidRule},
		{`{"prompt":"p","after_seconds":9007199254740993}`, CodeInvalidRule},
		{`{"prompt":"p","every_seconds":1.5}`, CodeInvalidRule},
		{`{"prompt":"p","every_seconds":299}`, CodeFrequencyTooHigh},
		{`{"prompt":"p","at":42}`, CodeInvalidRule},
		{`{"prompt":"p","at":"2026-08-30T11:00:00Z"}`, CodeNotFuture},
		{`{"prompt":"p","at":{"date":"2026-08-30","time":"20:00:00","time_zone":"Local"}}`,
			CodeInvalidTimeZone},
	} {
		failure := toolErrorOf(t, world.call(world.set.create, each.args))
		if failure.Code != each.code {
			t.Fatalf("create(%s) 报的是 %v，本该是 %v", each.args, failure.Code, each.code)
		}
	}
}

func TestCreateToolReportsUncertainPersistenceOnBothBarriers(t *testing.T) {
	// 前置屏障：还没分配 id，所以那份结果里没有 id。
	world := newToolWorld(t)
	world.sessions.flushed = false
	failure := toolErrorOf(t, world.call(world.set.create, `{"prompt":"p","after_seconds":60}`))
	if failure.Code != CodePersistenceUncertain || failure.Operation != OperationCreate || failure.ID != "" {
		t.Fatalf("前置屏障报的是 %+v", failure)
	}
	if world.owner.changeCount() != 0 {
		t.Fatal("前置屏障没过就不该写日志")
	}

	// 后置屏障：事件已经落进日志了，所以那份结果必须带上这条提醒的 id——模型据此
	// 知道该去查哪一条。
	world = newToolWorld(t)
	world.sessions.err = nil
	first := true
	world.set.sessions = flushFunc(func() (bool, error) {
		if first {
			first = false
			return true, nil
		}
		return false, nil
	})
	failure = toolErrorOf(t, world.call(world.set.create, `{"prompt":"p","after_seconds":60}`))
	if failure.Code != CodePersistenceUncertain || failure.ID != "schedule-1" {
		t.Fatalf("后置屏障报的是 %+v", failure)
	}
	if world.owner.changeCount() != 1 {
		t.Fatal("后置屏障之前那条事件本该已经落进日志")
	}
}

func TestCreateToolReportsBarrierFailureAsUncertain(t *testing.T) {
	// 屏障自己报错和「没人监听」在模型那一侧是同一件事：都只说不确定，不说为什么。
	world := newToolWorld(t)
	world.sessions.err = errors.New("盘满了")
	failure := toolErrorOf(t, world.call(world.set.create, `{"prompt":"p","after_seconds":60}`))
	if failure.Code != CodePersistenceUncertain {
		t.Fatalf("报的是 %+v", failure)
	}
	if strings.Contains(failure.Message, "盘满了") {
		t.Fatalf("屏障自己的诊断漏给了模型：%q", failure.Message)
	}
}

func TestToolsReportCorruptLogWithoutLeakingDiagnostics(t *testing.T) {
	world := newToolWorld(t)
	if _, err := world.owner.log.Append(sessionlog.Event{
		Type: EventChange, Data: json.RawMessage(`{"version":1,"operation":"delete","id":"ghost"}`),
	}); err != nil {
		t.Fatalf("往日志里塞坏事件失败：%v", err)
	}
	for name, raw := range map[string]json.RawMessage{
		"create": world.call(world.set.create, `{"prompt":"p","after_seconds":60}`),
		"list":   world.call(world.set.list, `{}`),
		"delete": world.call(world.set.delete, `{"id":"schedule-1"}`),
	} {
		failure := toolErrorOf(t, raw)
		if failure.Code != CodeCorruptLog {
			t.Fatalf("%s 报的是 %v，本该是 corrupt_log", name, failure.Code)
		}
		if failure.Message != "The session schedule log is corrupt." {
			t.Fatalf("%s 把诊断漏了出去：%q", name, failure.Message)
		}
	}
}

func TestListToolRendersEmptyArrayNotNull(t *testing.T) {
	world := newToolWorld(t)
	raw := world.call(world.set.list, `{}`)
	if string(raw) != "[]" {
		t.Fatalf("空清单排成了 %s", raw)
	}
}

func TestListToolProjectsStateAgainstNow(t *testing.T) {
	world := newToolWorld(t)
	world.createView(`{"prompt":"快到了","after_seconds":60}`)
	world.createView(`{"prompt":"还早","after_seconds":600}`)

	// 把墙上时钟推过第一条的目标：它变成 overdue，第二条还是 scheduled。
	world.now = baseNow.Add(2 * time.Minute)
	var views []View
	decodeInto(t, world.call(world.set.list, `{}`), &views)
	if len(views) != 2 {
		t.Fatalf("列出来 %d 条", len(views))
	}
	if views[0].State != StateOverdue || views[1].State != StateScheduled {
		t.Fatalf("两条的状态是 %v / %v", views[0].State, views[1].State)
	}
}

func TestDeleteToolIsIdempotentForUnknownID(t *testing.T) {
	world := newToolWorld(t)
	var result DeleteResult
	decodeInto(t, world.call(world.set.delete, `{"id":"schedule-9"}`), &result)
	if result.Deleted || result.Code != CodeScheduleNotFound || result.ID != "schedule-9" {
		t.Fatalf("删一个不存在的 id 得到 %+v", result)
	}
	if world.owner.changeCount() != 0 {
		t.Fatal("没删掉任何东西却写了日志")
	}
}

func TestDeleteToolRemovesLiveRecord(t *testing.T) {
	world := newToolWorld(t)
	view := world.createView(`{"prompt":"p","after_seconds":60}`)

	var result DeleteResult
	decodeInto(t, world.call(world.set.delete, `{"id":"`+string(view.ID)+`"}`), &result)
	if !result.Deleted || result.Code != "" {
		t.Fatalf("删掉之后得到 %+v", result)
	}
	raw := world.call(world.set.list, `{}`)
	if string(raw) != "[]" {
		t.Fatalf("删完清单还是 %s", raw)
	}
}

func TestDeleteToolRejectsBlankOrPaddedID(t *testing.T) {
	world := newToolWorld(t)
	for _, args := range []string{`{"id":""}`, `{"id":" a"}`, `{"id":"a "}`, `{}`, `{"id":7}`} {
		failure := toolErrorOf(t, world.call(world.set.delete, args))
		if failure.Code != CodeInvalidRule {
			t.Fatalf("delete(%s) 报的是 %v", args, failure.Code)
		}
	}
	// 这一条在过屏障之前就被挡住了，所以一次落盘都不该发生。
	if world.sessions.flushCalls() != 0 {
		t.Fatalf("形状不对的 id 走了 %d 次屏障", world.sessions.flushCalls())
	}
}

func TestDeleteToolReportsUncertainPersistence(t *testing.T) {
	world := newToolWorld(t)
	view := world.createView(`{"prompt":"p","after_seconds":60}`)
	world.sessions.flushed = false
	failure := toolErrorOf(t, world.call(world.set.delete, `{"id":"`+string(view.ID)+`"}`))
	if failure.Code != CodePersistenceUncertain || failure.Operation != OperationDelete ||
		failure.ID != view.ID {
		t.Fatalf("报的是 %+v", failure)
	}
}

func TestListToolReportsUncertainPersistence(t *testing.T) {
	// 「读」也要过屏障：一份建立在随时会消失的前缀上的清单，模型照着它做判断是错的。
	world := newToolWorld(t)
	world.sessions.flushed = false
	failure := toolErrorOf(t, world.call(world.set.list, `{}`))
	if failure.Code != CodePersistenceUncertain || failure.Operation != OperationList {
		t.Fatalf("报的是 %+v", failure)
	}
}

func TestToolsRefuseCallsFromAnotherScope(t *testing.T) {
	world := newToolWorld(t)
	stranger := scopeOf(t, "stranger", world.root)
	for name, body := range map[string]func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error){
		"create": world.set.create,
		"list":   world.set.list,
		"delete": world.set.delete,
	} {
		raw, err := body(t.Context(), json.RawMessage(`{"prompt":"p","after_seconds":60,"id":"a"}`),
			execOn(stranger))
		if err != nil {
			t.Fatalf("%s 交回了 Go 错误：%v", name, err)
		}
		if failure := toolErrorOf(t, raw); failure.Code != CodeInternal {
			t.Fatalf("%s 对陌生作用域报的是 %v，本该是 internal", name, failure.Code)
		}
	}
	// 三次都不该碰日志。
	if world.owner.changeCount() != 0 {
		t.Fatal("不属于自己的调用写了日志")
	}
}

func TestToolsRefuseCallsWithoutExecution(t *testing.T) {
	// exec 为 nil 只会是调用方绕过了运行时；那和「别人在调」一样处理。
	world := newToolWorld(t)
	raw, err := world.set.list(t.Context(), json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatalf("交回了 Go 错误：%v", err)
	}
	if failure := toolErrorOf(t, raw); failure.Code != CodeInternal {
		t.Fatalf("报的是 %v", failure.Code)
	}
}

func TestToolsNotifyDurableChangeOnEachBarrier(t *testing.T) {
	// 每一次成功的屏障之后都要叫一次：漏掉任何一次，那份定时器投影就会拿着一份
	// 过时的日志继续睡。
	world := newToolWorld(t)
	world.createView(`{"prompt":"p","after_seconds":60}`)
	if world.notified != 2 {
		t.Fatalf("一次 create 通知了 %d 次，本该是两次", world.notified)
	}
	world.call(world.set.list, `{}`)
	if world.notified != 3 {
		t.Fatalf("一次 list 之后累计通知了 %d 次", world.notified)
	}
}

func TestToolsHonourCancellationBeforeWriting(t *testing.T) {
	// ctx 在轮到自己之前就废了的话，一个字节都不许写出去。
	world := newToolWorld(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := world.set.create(ctx, json.RawMessage(`{"prompt":"p","after_seconds":60}`),
		world.exec()); err == nil {
		t.Fatal("被取消的调用本该交回 Go 错误")
	}
	if world.owner.changeCount() != 0 {
		t.Fatal("被取消的调用写了日志")
	}
}

func TestCreateToolKeepsModelFacingBytesUnescaped(t *testing.T) {
	// 那份权威值就是模型看到的字节（见 renderValue），所以 < > & 必须原样出现。
	world := newToolWorld(t)
	raw := world.call(world.set.create, `{"prompt":"<b>喝水</b> & 休息","after_seconds":60}`)
	if !strings.Contains(string(raw), `<b>喝水</b> & 休息`) {
		t.Fatalf("排出来的是 %s", raw)
	}
}

func TestRenderValuePassesTheAuthoritativeValueThrough(t *testing.T) {
	// 呈现这一层不许再排一遍：那份权威值已经是最终字节了，再排一次会把它包成
	// 一段带引号的字符串，模型看到的就不再是一个对象。
	content, err := renderValue(nil, json.RawMessage(`{"a":1}`))
	if err != nil {
		t.Fatalf("呈现报了 %v", err)
	}
	if len(content) != 1 {
		t.Fatalf("排出来 %d 块", len(content))
	}
	text, ok := content[0].(llm.TextBlock)
	if !ok {
		t.Fatalf("排出来的是 %T", content[0])
	}
	if text.Text != `{"a":1}` {
		t.Fatalf("排出来的是 %q", text.Text)
	}
}

func TestPresentCallCarriesTheTitleAndRawInput(t *testing.T) {
	world := newToolWorld(t)
	create := world.set.newCreateTool()
	view, ok := create.PresentCall(json.RawMessage(`{"prompt":"喝水"}`)).(tools.GenericCallView)
	if !ok {
		t.Fatalf("交回的卡片是 %T", create.PresentCall(json.RawMessage(`{}`)))
	}
	if view.Title != "Create reminder" || view.Kind != tools.CallOther {
		t.Fatalf("卡片是 %+v", view)
	}
	if string(view.RawInput) != `"喝水"` {
		t.Fatalf("原始入参排成了 %s", view.RawInput)
	}

	// 一次读没有原始入参，那个字段就该是空的，而不是一段 `""`。
	list := world.set.newListTool()
	listView := list.PresentCall(nil).(tools.GenericCallView)
	if listView.Kind != tools.CallRead || listView.RawInput != nil {
		t.Fatalf("list 的卡片是 %+v", listView)
	}

	remove := world.set.newDeleteTool()
	removeView := remove.PresentCall(json.RawMessage(`{"id":"schedule-1"}`)).(tools.GenericCallView)
	if string(removeView.RawInput) != `"schedule-1"` {
		t.Fatalf("delete 的卡片是 %+v", removeView)
	}
}

func TestRegisterToolsInstallsAndRemovesAllThree(t *testing.T) {
	world := newToolWorld(t)
	runtime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造工具运行时失败：%v", err)
	}
	undo, err := registerTools(t.Context(), runtime, world.owner.Scope(), world.set)
	if err != nil {
		t.Fatalf("装工具失败：%v", err)
	}
	for _, name := range []string{CreateToolName, ListToolName, DeleteToolName} {
		if !hasTool(runtime, world.owner.Scope(), name) {
			t.Fatalf("%s 没装上", name)
		}
	}
	if err := undo(t.Context()); err != nil {
		t.Fatalf("摘工具失败：%v", err)
	}
	for _, name := range []string{CreateToolName, ListToolName, DeleteToolName} {
		if hasTool(runtime, world.owner.Scope(), name) {
			t.Fatalf("%s 没摘干净", name)
		}
	}
}

func TestRegisterToolsUndoesPartialInstall(t *testing.T) {
	// 第二次装同一套会在头一件上就撞名。那时候前面装上的必须整个收回去——模型手上
	// 留着一件建得了提醒、却查不了清单的工具，比一件都没有更难解释。
	world := newToolWorld(t)
	runtime, err := tools.NewRuntime(tools.Options{})
	if err != nil {
		t.Fatalf("造工具运行时失败：%v", err)
	}
	undo, err := registerTools(t.Context(), runtime, world.owner.Scope(), world.set)
	if err != nil {
		t.Fatalf("第一次装失败：%v", err)
	}
	t.Cleanup(func() { _ = undo(context.Background()) })

	if _, err := registerTools(t.Context(), runtime, world.owner.Scope(), world.set); err == nil {
		t.Fatal("撞名那一次本该失败")
	}
	// 第一套还完好：撞名那一次不该把别人装上去的也摘走。
	for _, name := range []string{CreateToolName, ListToolName, DeleteToolName} {
		if !hasTool(runtime, world.owner.Scope(), name) {
			t.Fatalf("%s 被撞名那一次误摘了", name)
		}
	}
}

// hasTool 问这把钥匙上此刻看不看得见这件工具。
func hasTool(runtime *tools.Runtime, owner *scope.Scope, name string) bool {
	_, present := runtime.Get(name, owner.Key())
	return present
}

// flushFunc 是一道行为由闭包决定的落盘屏障。
type flushFunc func() (bool, error)

func (f flushFunc) Flush(context.Context, *coresession.Session) (bool, error) { return f() }
