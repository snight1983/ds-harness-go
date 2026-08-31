// 本文件的作用：本包全部测试共用的那几样——把一次失败折成它那个封闭错误码、
// 造一台可摆布的时钟，以及那两件替身（一个握着真会话的假 agent、一张可摆布的
// agent 注册表）。
//
// # 这些助手防的是什么错
//
//   - **用 err.Error() 的字面量去断言**。本包交回的那些话有一半是给模型看的英文
//     文案（见 doc.go 的「错误分两种语言」），以后改一个词就会让一大片用例红掉，
//     而它们本来想验的是「报的是哪一种失败」。所以断言一律落在 [ErrorCode] 上。
//   - **把 nil 当成一次失败**。errorCode 收到 nil 会当场 Fatal：一条本该被拒的
//     调用悄悄成功了，是这份测试最该抓住的那一种回归。
//   - **拿一个假会话糊弄过去**。stubAgent 手里握的是一台**真的**
//     [ds-harness-go/core/session.Session]：本包排出去的每一份字节都要真的过一遍
//     那台会话的信封校验，不然「写下的改动读得回来」这件事根本没验到。
//   - **拿墙上时钟去验时刻**。所有用例走的都是 stubClock，于是 createdAt /
//     updatedAt 是可以逐个数字断言的；时钟回拨那一条不变量也才试得出来。

package goal

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"ds-harness-go/core/agent"
	"ds-harness-go/core/scope"
	coresession "ds-harness-go/core/session"
	"ds-harness-go/llm"
	"ds-harness-go/session"
)

// asError 是 [errors.As] 的一层泛型薄壳，省掉每个调用点那次 `var x *T` 的声明。
func asError[T error](err error, target *T) bool {
	return errors.As(err, target)
}

// errorCode 把一次失败折成它在那份封闭码表里的位置。
//
// 本包在边界上只交回 [Error]；交回别的类型说明有一条路把下游的错原样漏了出来，
// 那本身就是要抓的错，所以这里 Fatal 而不是交回一个空码。
func errorCode(t *testing.T, err error) ErrorCode {
	t.Helper()
	if err == nil {
		t.Fatal("本该报错，却成功了")
	}
	var failure *Error
	if !asError(err, &failure) {
		t.Fatalf("交回的是 %T，本该是 *Error：%v", err, err)
	}
	return failure.Code
}

// expectFoldError 断言这次失败是「日志坏了」那一种。
func expectFoldError(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s 本该被拒", what)
	}
	var failure *FoldError
	if !asError(err, &failure) {
		t.Fatalf("%s 交回的是 %T，本该是 *FoldError：%v", what, err, err)
	}
}

// changeEvent 造一条 goal/change 事件；负载原样放进去，不做任何检查。
func changeEvent(payload string) session.Event {
	return session.Event{Type: EventChange, Data: json.RawMessage(payload)}
}

// userEvent 造一条带来源的用户消息事件；source 为 nil 时是一条普通人类回合。
func userEvent(t *testing.T, source *Source) session.Event {
	t.Helper()
	data := session.UserMessageData{}
	if source != nil {
		carried, err := source.MessageSource()
		if err != nil {
			t.Fatalf("包来源失败：%v", err)
		}
		data.Source = carried
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("排用户消息失败：%v", err)
	}
	// 带上表面标记：一条真的用户消息一定是上表面的，会话在追加时会拿这一条去验
	// （少了它 [ds-harness-go/core/session.Session.Append] 当场拒收）。只走折叠的
	// 那些用例不看这个字段，但两边共用同一个造事件的地方，它就该造出那条真会落进
	// 日志的样子。
	return session.Event{
		Type:      session.EventUserMessage,
		Data:      encoded,
		SurfaceOp: session.AppendOp{},
	}
}

// ---- 时钟 ----

// stubClock 是一台可以任意摆布的墙上时钟，单位毫秒。
type stubClock struct {
	mutex sync.Mutex
	// millis 是它此刻指着的毫秒时刻。
	millis int64
}

// newStubClock 造一台从 1000 起步的时钟。
//
// 不从 0 起步：0 同时也是「这个字段没填」的样子，用它做时刻会让一次漏填看起来
// 像一次正常的写入。
func newStubClock() *stubClock { return &stubClock{millis: 1000} }

// Now 是交给 [Config.Now] 的那条路。
func (c *stubClock) Now() time.Time {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return time.UnixMilli(c.millis)
}

// set 把时钟拨到某一刻，往回拨也行——「时钟回拨」那条用例正是靠它。
func (c *stubClock) set(millis int64) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.millis = millis
}

// advance 把时钟往前推一段。
func (c *stubClock) advance(delta int64) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.millis += delta
}

// ---- 替身 ----

// scopeOf 造一个有身份的作用域，用完自动释放。
func scopeOf(t *testing.T, label string, parent *scope.Scope) *scope.Scope {
	t.Helper()
	options := scope.Options{}
	if parent != nil {
		options.Parent = parent.Key()
	}
	owner, err := scope.New(scope.NewKey(label), options)
	if err != nil {
		t.Fatalf("造作用域 %s 失败：%v", label, err)
	}
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })
	return owner
}

// stubAgent 是一个只为满足 [ds-harness-go/core/agent.Agent] 契约而存在的假 agent。
//
// 本包用得着的只有 ID、Session 和 Scope 三样；剩下的方法在这里都是空的，被叫到
// 说明本包越界了。
type stubAgent struct {
	id  session.SessionID
	own *scope.Scope
	log *coresession.Session
}

// newStubAgent 造一个带真会话、真作用域的假 agent。
func newStubAgent(t *testing.T, id string, parent *scope.Scope, seed []session.Event) *stubAgent {
	t.Helper()
	sessionID := session.SessionID(id)
	header := session.SessionHeader{
		Version:   session.FormatVersion,
		ID:        sessionID,
		CreatedAt: 1,
	}
	log, err := coresession.NewSession(sessionID, coresession.Options{
		Seed:   seed,
		Header: &header,
		Now:    func() int64 { return 1 },
	})
	if err != nil {
		t.Fatalf("造会话失败：%v", err)
	}
	return &stubAgent{id: sessionID, own: scopeOf(t, id, parent), log: log}
}

func (a *stubAgent) ID() session.SessionID                                  { return a.id }
func (a *stubAgent) Status() agent.Status                                   { return agent.StatusIdle }
func (a *stubAgent) Options() agent.Options                                 { return agent.Options{} }
func (a *stubAgent) Session() *coresession.Session                          { return a.log }
func (a *stubAgent) Inbox() *agent.Inbox                                    { return nil }
func (a *stubAgent) Scope() *scope.Scope                                    { return a.own }
func (a *stubAgent) Cancel(session.TurnEndCancelCause, agent.CancelOptions) {}
func (a *stubAgent) Send(llm.Message, agent.InboxTarget, bool)              {}
func (a *stubAgent) Steer(llm.Message)                                      {}
func (a *stubAgent) Inject(llm.Message)                                     {}
func (a *stubAgent) Prepend(llm.Message, agent.InboxTarget) {}
func (a *stubAgent) Followup(llm.Message)                                   {}

func (a *stubAgent) WhenIdle(context.Context) error { return nil }

func (a *stubAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	return task(ctx)
}

// events 是这个 agent 会话日志里此刻的全部事件。
func (a *stubAgent) events() []session.Event { return a.log.Events() }

// changes 把这条日志里属于本包的那些事件挑出来。
func (a *stubAgent) changes() []session.Event {
	var picked []session.Event
	for _, event := range a.events() {
		if event.Type == EventChange {
			picked = append(picked, event)
		}
	}
	return picked
}

// append 往这条日志里直接写一条事件，绕开本包——「别人写的改动」那些用例靠它。
func (a *stubAgent) append(t *testing.T, event session.Event) session.Event {
	t.Helper()
	written, err := a.log.Append(event)
	if err != nil {
		t.Fatalf("往日志里写 %s 失败：%v", event.Type, err)
	}
	return written
}

// stubAgents 是那张可摆布的 agent 注册表。
type stubAgents struct {
	mutex sync.Mutex
	// live 是此刻活着的那些 agent，[Service.assertLive] 比的就是这张表里的对象。
	live map[session.SessionID]agent.Agent
	// sessionStart 是登记进来的那些会话起跑观察者。
	sessionStart []agent.SessionStartObserver
	// sessionStartErr 非 nil 时那次登记直接失败。
	sessionStartErr error
	// stopErr 是退订函数交回的错。
	stopErr error
	// stopped 数退订函数被叫过几次。
	stopped int
}

// newStubAgents 造一张装着这些 agent 的注册表。
func newStubAgents(members ...agent.Agent) *stubAgents {
	live := map[session.SessionID]agent.Agent{}
	for _, member := range members {
		live[member.ID()] = member
	}
	return &stubAgents{live: live}
}

func (a *stubAgents) Get(id session.SessionID) (agent.Agent, bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	found, present := a.live[id]
	return found, present
}

func (a *stubAgents) OnSessionStart(
	_ context.Context, _ *scope.Scope, observer agent.SessionStartObserver,
) (func(context.Context) error, error) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if a.sessionStartErr != nil {
		return nil, a.sessionStartErr
	}
	a.sessionStart = append(a.sessionStart, observer)
	return func(context.Context) error {
		a.mutex.Lock()
		a.stopped++
		failure := a.stopErr
		a.mutex.Unlock()
		return failure
	}, nil
}

// drop 把一个 agent 从活表里摘掉：验 [CodeAgentNotLive] 那几条用它。
func (a *stubAgents) drop(id session.SessionID) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	delete(a.live, id)
}

// put 把一个 agent 放上台，可以顶掉同名的旧实例。
func (a *stubAgents) put(member agent.Agent) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.live[member.ID()] = member
}

// emitSessionStart 把一次会话起跑放给每一个观察者。
func (a *stubAgents) emitSessionStart(started agent.Agent, source agent.SessionStartSource) {
	a.mutex.Lock()
	observers := append([]agent.SessionStartObserver(nil), a.sessionStart...)
	a.mutex.Unlock()
	for _, observer := range observers {
		observer(started, source)
	}
}

// ---- 装配 ----

// newTestService 造一台接着这张注册表和这台时钟的服务。
func newTestService(t *testing.T, agents Agents, clock *stubClock) *Service {
	t.Helper()
	service, err := New(Config{Agents: agents, Now: clock.Now})
	if err != nil {
		t.Fatalf("造服务失败：%v", err)
	}
	return service
}

// newSingleAgentService 是那一大半用例的开场白：一个 agent、一张只装着它的注册表、
// 一台从 1000 起步的时钟、一台服务。
func newSingleAgentService(t *testing.T) (*Service, *stubAgent, *stubAgents, *stubClock) {
	t.Helper()
	owner := newStubAgent(t, "session-1", nil, nil)
	agents := newStubAgents(owner)
	clock := newStubClock()
	return newTestService(t, agents, clock), owner, agents, clock
}

// mustCreate 建一个目标，失败就当场 Fatal。
func mustCreate(t *testing.T, service *Service, owner agent.Agent, objective string) *View {
	t.Helper()
	view, err := service.Create(owner, CreateRequest{Objective: objective})
	if err != nil {
		t.Fatalf("建目标失败：%v", err)
	}
	return view
}
