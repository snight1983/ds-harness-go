// 本文件的作用：本包全部测试共用的那几样——把错误按类型取出来、把一次失败折成它
// 那个封闭错误码，以及那几件替身（一个只满足契约的 agent、一道可摆布的落盘屏障、
// 一张可摆布的 agent 注册表）。
//
// # 这些助手防的是什么错
//
//   - **用 err.Error() 的字面量去断言**。那些话里有一半是给模型看的英文文案，
//     以后改一个词就会让一大片用例红掉，而它们本来想验的是「报的是哪一种失败」。
//     所以断言一律落在 [ErrorCode] 上，不落在句子上。
//   - **把 nil 当成一次失败**。errorCode 收到 nil 会当场 Fatal：一条本该报错的路
//     悄悄成功了，是这份测试最该抓住的那一种回归。
//   - **拿一个假会话糊弄过去**。stubAgent 手里握的是一台**真的**
//     [ds-harness-go/core/session.Session]：本包写下去的每一条事件都要真的过一遍
//     那台会话的信封校验，不然「排出来的字节合法」这件事根本没验到。

package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"ds-harness-go/core/agent"
	"ds-harness-go/core/scope"
	coresession "ds-harness-go/core/session"
	"ds-harness-go/core/tools"
	"ds-harness-go/llm"
	sessionlog "ds-harness-go/session"
)

// asError 是 [errors.As] 的一层泛型薄壳，省掉每个调用点那次 `var x *T` 的声明。
func asError[T error](err error, target *T) bool {
	return errors.As(err, target)
}

// errorCode 把一次失败折成它在那份封闭码表里的位置。
//
// 本包只会交回 [InputError] 和 [LogError] 两种；别的类型说明有一条路把某个下游的
// 错原样漏了出来，那本身就是要抓的错，所以这里 Fatal 而不是交回一个空码。
func errorCode(t *testing.T, err error) ErrorCode {
	t.Helper()
	if err == nil {
		t.Fatal("本该报错，却成功了")
	}
	var inputErr *InputError
	if errors.As(err, &inputErr) {
		return inputErr.Code
	}
	var logErr *LogError
	if errors.As(err, &logErr) {
		return logErr.Code()
	}
	t.Fatalf("交回的是 %T，本包只该交回 *InputError 或 *LogError：%v", err, err)
	return ""
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
// 本包用得着的只有 ID、Session、Scope、Followup、RunMaintenance 和 WhenIdle 这几样；
// 剩下的方法在这里都是空的，被叫到说明本包越界了。
type stubAgent struct {
	id  sessionlog.SessionID
	own *scope.Scope
	log *coresession.Session

	mutex     sync.Mutex
	followups []llm.Message

	// onFollowup 非 nil 时在收下每一条跟进消息**之后**被叫一次。用例拿它回看
	// 那一刻的日志，验「跟进消息先于 dispatch 事件」这条次序。
	onFollowup func()

	// maintenanceErr 非 nil 表示这个 agent 此刻认领不到那个空闲期。
	maintenanceErr error
	// onMaintenance 非 nil 时在认领成功、把活儿交出去**之前**被叫一次。用例拿它
	// 制造「从外面那次判断到认领成功之间，世界变了」这件事。
	onMaintenance func()
	// maintenance 记下 RunMaintenance 被叫过几次。
	maintenance int
	// whenIdle 是 WhenIdle 等的那道门；nil 表示当场返回。
	whenIdle chan struct{}
	// whenIdleErr 是那道门开了之后交回的错。
	whenIdleErr error
}

// newStubAgent 造一个带真会话、真作用域的假 agent。
func newStubAgent(t *testing.T, id string, parent *scope.Scope, seed []sessionlog.Event) *stubAgent {
	t.Helper()
	sessionID := sessionlog.SessionID(id)
	header := sessionlog.SessionHeader{
		Version:   sessionlog.FormatVersion,
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

func (a *stubAgent) ID() sessionlog.SessionID                                  { return a.id }
func (a *stubAgent) Status() agent.Status                                      { return agent.StatusIdle }
func (a *stubAgent) Options() agent.Options                                    { return agent.Options{} }
func (a *stubAgent) Session() *coresession.Session                             { return a.log }
func (a *stubAgent) Inbox() *agent.Inbox                                       { return nil }
func (a *stubAgent) Scope() *scope.Scope                                       { return a.own }
func (a *stubAgent) Cancel(sessionlog.TurnEndCancelCause, agent.CancelOptions) {}
func (a *stubAgent) Send(llm.Message, agent.InboxTarget, bool)                 {}
func (a *stubAgent) Steer(llm.Message)                                         {}
func (a *stubAgent) Inject(llm.Message)                                        {}
func (a *stubAgent) Prepend(llm.Message, agent.InboxTarget) {}

func (a *stubAgent) Followup(message llm.Message) {
	a.mutex.Lock()
	a.followups = append(a.followups, message)
	probe := a.onFollowup
	a.mutex.Unlock()
	// 探针在锁外面叫：它多半要回看日志，那条路会再拿一次这把锁。
	if probe != nil {
		probe()
	}
}

// followupTexts 把收到的那些跟进消息里的文本块摊平。
func (a *stubAgent) followupTexts() []string {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	texts := make([]string, 0, len(a.followups))
	for _, message := range a.followups {
		for _, block := range message.Content {
			if text, ok := block.(llm.TextBlock); ok {
				texts = append(texts, text.Text)
			}
		}
	}
	return texts
}

func (a *stubAgent) WhenIdle(ctx context.Context) error {
	if a.whenIdle == nil {
		return a.whenIdleErr
	}
	select {
	case <-a.whenIdle:
		return a.whenIdleErr
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (a *stubAgent) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	a.mutex.Lock()
	a.maintenance++
	failure := a.maintenanceErr
	probe := a.onMaintenance
	a.mutex.Unlock()
	if failure != nil {
		return failure
	}
	// 探针在锁外面叫：它多半要动这条日志，那条路会再拿一次这把锁。
	if probe != nil {
		probe()
	}
	return task(ctx)
}

// events 是这个 agent 会话日志里此刻的全部事件。
func (a *stubAgent) events() []sessionlog.Event { return a.log.Events() }

// changeCount 数这条日志里属于本包的事件条数。
func (a *stubAgent) changeCount() int {
	count := 0
	for _, event := range a.events() {
		if event.Type == EventChange {
			count++
		}
	}
	return count
}

// stubSessions 是那道可摆布的落盘屏障。
type stubSessions struct {
	mutex sync.Mutex
	// flushed 是 Flush 交回的那个「有人真做了落盘的活儿」。
	flushed bool
	// err 非 nil 时这次屏障直接报错。
	err error
	// calls 是屏障被走过几次。
	calls int
}

func newStubSessions() *stubSessions { return &stubSessions{flushed: true} }

func (s *stubSessions) Flush(context.Context, *coresession.Session) (bool, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.calls++
	if s.err != nil {
		return false, s.err
	}
	return s.flushed, nil
}

func (s *stubSessions) flushCalls() int {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.calls
}

// execOn 造一份落在这把作用域钥匙上的执行上下文。
func execOn(owner *scope.Scope) *tools.RunContext {
	return &tools.RunContext{Execution: tools.Execution{
		ExecutionInput: tools.ExecutionInput{Agent: owner.Key()},
	}}
}

// decodeInto 把一份工具结果读成想要的形状。
func decodeInto(t *testing.T, raw json.RawMessage, target any) {
	t.Helper()
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("读工具结果失败：%v（原文 %s）", err, raw)
	}
}

// toolErrorOf 把一份工具结果读成一次失败。
func toolErrorOf(t *testing.T, raw json.RawMessage) ToolError {
	t.Helper()
	var failure ToolError
	decodeInto(t, raw, &failure)
	if failure.Code == "" {
		t.Fatalf("这份结果不是一次失败：%s", raw)
	}
	return failure
}
