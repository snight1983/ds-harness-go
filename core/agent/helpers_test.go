// 本文件的作用：这一包测试共用的小工具——造作用域、造会话、造消息，
// 以及一个只为满足 [Agent] 契约而存在的假 agent。

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"ds-harness-go/core/scope"
	"ds-harness-go/core/session"
	"ds-harness-go/llm"
	sessionlog "ds-harness-go/session"
)

// testAbsolutePath 是一条在本机上确实绝对的路径。
//
// 理由和 core/session 那一处逐字相同：写死哪一边的字面量都会让另一个平台上的
// 测试变成假通过。
var testAbsolutePath = filepath.Join(os.TempDir(), "ds-harness-go-agent-test")

// fixedClock 是一个走得可预测的时钟：每读一次加一毫秒。
func fixedClock() func() int64 {
	tick := int64(1000)
	return func() int64 { return atomic.AddInt64(&tick, 1) }
}

// rootScope 造一个没有身份的作用域（落全局层），用完自动释放。
func rootScope(t *testing.T) *scope.Scope {
	t.Helper()
	owner := scope.NewRoot()
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })
	return owner
}

// keyedScope 造一个有身份的作用域，用完自动释放。parent 为 nil 表示它自己是顶层。
func keyedScope(t *testing.T, label string, parent *scope.Key) *scope.Scope {
	t.Helper()
	owner, err := scope.New(scope.NewKey(label), scope.Options{Parent: parent})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })
	return owner
}

// newFreeSession 造一个游离会话，seed 由调用方给（nil 表示不给）。
func newFreeSession(t *testing.T, id sessionlog.SessionID, seed []sessionlog.Event) *session.Session {
	t.Helper()
	header := sessionlog.SessionHeader{ID: id, Cwd: testAbsolutePath, SeedLength: len(seed)}
	live, err := session.NewSession(id, session.Options{
		Seed:   seed,
		Header: &header,
		Now:    fixedClock(),
	})
	if err != nil {
		t.Fatalf("造会话失败：%v", err)
	}
	return live
}

// pokeEventType 是一条只在测试里用的会话事件，专门用来把发布窗口撑开。
const pokeEventType = "test/poke"

// busyInbox 造一个挂在存储上的收件箱，外加一个「趁这个会话正在发布事件的那一瞬
// 跑一段代码」的钩子。
//
// 会话不许在发布窗口里重入追加（见 [ds-harness-go/core/session.Session.Append]），
// 所以那一瞬里**每一次**收件箱改动都写不进日志。[Inbox] 那几条「日志写不下去就
// 把错误原样往上交」的路径是真错误路径而不是防御性分支，这是本包够得着它们的
// 唯一办法。
func busyInbox(t *testing.T) (*Inbox, func(body func())) {
	t.Helper()
	ctx := context.Background()
	owner := rootScope(t)

	store, err := session.NewStore(session.StoreOptions{Now: fixedClock()})
	if err != nil {
		t.Fatalf("造会话存储失败：%v", err)
	}
	live, err := store.Prepare("busy", session.CreateOptions{Cwd: testAbsolutePath})
	if err != nil {
		t.Fatalf("备会话失败：%v", err)
	}
	detachSession, err := store.Enter(owner, live)
	if err != nil {
		t.Fatalf("会话进存储失败：%v", err)
	}
	t.Cleanup(func() { _ = detachSession(ctx) })

	inbox, err := NewInbox(live, InboxNotifications{})
	if err != nil {
		t.Fatalf("造收件箱失败：%v", err)
	}

	// during 是下一次戳到时要跑的那段代码；跑完就摘掉，免得它自己那些追加又把
	// 观察者叫回来。
	var during func()
	detachObserver, err := store.OnEvent(ctx, owner, func(_ *session.Session, event sessionlog.Event) {
		if event.Type != pokeEventType || during == nil {
			return
		}
		body := during
		during = nil
		body()
	})
	if err != nil {
		t.Fatalf("挂追加观察者失败：%v", err)
	}
	t.Cleanup(func() { _ = detachObserver(ctx) })

	return inbox, func(body func()) {
		during = body
		if _, err := live.Append(sessionlog.Event{Type: pokeEventType}); err != nil {
			t.Fatalf("戳一下失败：%v", err)
		}
	}
}

// text 造一条带全新身份的用户消息。
func text(body string) llm.Message {
	return llm.NewUserMessage(llm.Content{llm.TextBlock{Text: body}}, llm.UserSource{})
}

// data 把一份负载排成字节，排不出去当场失败。
func data(t *testing.T, payload any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("排负载失败：%v", err)
	}
	return encoded
}

// fakeAgent 是一个只为满足 [Agent] 契约而存在的假 agent。
//
// 本包不实现 [Agent]（实现在循环那一层），但注册表和收件箱的测试需要一个能放进
// 表里的值。除了 ID、Scope、Session 三个真的被注册表读的字段，其余全是空操作——
// 派发那些方法一个都不碰它们。
type fakeAgent struct {
	id      sessionlog.SessionID
	scope   *scope.Scope
	session *session.Session
	inbox   *Inbox
	options Options
	status  Status
}

// newFakeAgent 造一个挂在自己那把作用域钥匙上的假 agent。
func newFakeAgent(t *testing.T, id string, parent *scope.Key) *fakeAgent {
	t.Helper()
	sessionID := sessionlog.SessionID(id)
	return &fakeAgent{
		id:      sessionID,
		scope:   keyedScope(t, id, parent),
		session: newFreeSession(t, sessionID, nil),
	}
}

func (a *fakeAgent) ID() sessionlog.SessionID       { return a.id }
func (a *fakeAgent) Options() Options               { return a.options }
func (a *fakeAgent) Session() *session.Session      { return a.session }
func (a *fakeAgent) Inbox() *Inbox                  { return a.inbox }
func (a *fakeAgent) Status() Status                 { return a.status }
func (a *fakeAgent) Scope() *scope.Scope            { return a.scope }
func (a *fakeAgent) WhenIdle(context.Context) error { return nil }

func (a *fakeAgent) Cancel(sessionlog.TurnEndCancelCause, CancelOptions) {}

func (a *fakeAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	return task(ctx)
}

func (a *fakeAgent) Send(llm.Message, InboxTarget, bool) {}
func (a *fakeAgent) Followup(llm.Message)                {}
func (a *fakeAgent) Steer(llm.Message)                   {}
func (a *fakeAgent) Inject(llm.Message)                  {}
func (a *fakeAgent) Prepend(llm.Message, InboxTarget)    {}

// newRegistry 造一张空表，造不出来当场失败。
func newRegistry(t *testing.T) *Registry {
	t.Helper()
	registry, err := NewRegistry(RegistryOptions{})
	if err != nil {
		t.Fatalf("造注册表失败：%v", err)
	}
	return registry
}

// live 把一个假 agent 登记进表里并公布出去，返回摘除它的函数。
func live(t *testing.T, registry *Registry, agent Agent, owner Agent) func(context.Context) error {
	t.Helper()
	detach, err := registry.Register(context.Background(), agent, owner)
	if err != nil {
		t.Fatalf("登记 agent 失败：%v", err)
	}
	t.Cleanup(func() { _ = detach(context.Background()) })
	return detach
}
