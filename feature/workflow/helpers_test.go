// 本文件的作用：这一包测试共用的小工具——造作用域、造一个只有身份和作用域的
// 假 agent、造一个把警告攒进切片的发射器。

package workflow

import (
	"context"
	"sync"
	"testing"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// rootScope 造一个没有身份的作用域（落全局层），用完自动释放。
func rootScope(t *testing.T) *scope.Scope {
	t.Helper()
	owner := scope.NewRoot()
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })
	return owner
}

// fakeAgent 是这条接缝派发时唯一用得上的那两样：一个身份和一把作用域钥匙。
//
// 其余方法在这一包的用例里走不到——生命周期边只把这个值原样递给观察者，从不调它。
type fakeAgent struct {
	id    sessionlog.SessionID
	scope *scope.Scope
}

var _ agent.Agent = (*fakeAgent)(nil)

// newFakeAgent 造一个挂在 parent 底下的假 agent；parent 为 nil 表示它自己是顶层。
func newFakeAgent(t *testing.T, id sessionlog.SessionID, parent *scope.Key) *fakeAgent {
	t.Helper()
	owner, err := scope.New(scope.NewKey(string(id)), scope.Options{Parent: parent})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })
	return &fakeAgent{id: id, scope: owner}
}

func (a *fakeAgent) ID() sessionlog.SessionID { return a.id }
func (a *fakeAgent) Scope() *scope.Scope      { return a.scope }

func (a *fakeAgent) Options() agent.Options         { return agent.Options{} }
func (a *fakeAgent) Session() *session.Session      { return nil }
func (a *fakeAgent) Inbox() *agent.Inbox            { return nil }
func (a *fakeAgent) Status() agent.Status           { return agent.StatusIdle }
func (a *fakeAgent) WhenIdle(context.Context) error { return nil }

func (a *fakeAgent) Cancel(sessionlog.TurnEndCancelCause, agent.CancelOptions) {}
func (a *fakeAgent) RunMaintenance(context.Context, func(context.Context) error) error {
	return nil
}
func (a *fakeAgent) Send(llm.Message, agent.InboxTarget, bool) {}
func (a *fakeAgent) Followup(llm.Message)                      {}
func (a *fakeAgent) Steer(llm.Message)                         {}
func (a *fakeAgent) Inject(llm.Message)                        {}
func (a *fakeAgent) Prepend(llm.Message, agent.InboxTarget)    {}
func (a *fakeAgent) Remove(llm.MessageID)                      {}
func (a *fakeAgent) Replace(llm.MessageID, llm.Message)        {}

// newLifecycle 造一个把警告攒进切片、而不是往 slog 里写的发射器。
func newLifecycle(t *testing.T) (*Lifecycle, func() []string) {
	t.Helper()
	lifecycle, err := NewLifecycle(nil)
	if err != nil {
		t.Fatalf("造发射器失败：%v", err)
	}
	var mutex sync.Mutex
	var warnings []string
	lifecycle.warn = func(message string) {
		mutex.Lock()
		defer mutex.Unlock()
		warnings = append(warnings, message)
	}
	return lifecycle, func() []string {
		mutex.Lock()
		defer mutex.Unlock()
		return append([]string(nil), warnings...)
	}
}

// startedRun 是这一包用例里那次「已经开过工」的运行，身份齐全。
func startedRun() RunInfo {
	return RunInfo{
		ID:   "run-1",
		Meta: Meta{Name: "整理", Description: "把仓库收拾一遍"},
	}
}
