// 本文件验这个包自己那条关系不变量：一条 webhook 放行进来的提示词落进日志的那一刻，
// 它那个会话必须恰好归属在它会话头记的那一个工作区名下。
//
// 检查是**panic**着报的（见 [github.com/snight1983/ds-harness-go/invariants.Fail]），
// 所以走注册表那几条用例都用 mustFail／mustNotFail 把那次 panic 接住再断言。

package webhook

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// newRegistry 造一条全开的不变量注册表。
func newRegistry(t *testing.T) *invariants.Registry {
	t.Helper()
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("建注册表失败：%v", err)
	}
	t.Cleanup(registry.Close)
	return registry
}

// mustFail 断言 act 报了一次违例，并把那句话交回来。
func mustFail(t *testing.T, act func()) string {
	t.Helper()
	var reported string
	func() {
		defer func() {
			recovered := recover()
			if recovered == nil {
				t.Fatal("该报一次违例")
			}
			reported = fmt.Sprint(recovered)
		}()
		act()
	}()
	return reported
}

// mustNotFail 断言 act 一声不响。
func mustNotFail(t *testing.T, act func()) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("这条路不该报违例，实际 %v", recovered)
		}
	}()
	act()
}

// splicedEvent 造一条把给定来源的消息插进收件箱的事件。
func splicedEvent(t *testing.T, sources ...llm.MessageSource) sessionlog.Event {
	t.Helper()
	inserted := make([]llm.Message, 0, len(sources))
	for _, source := range sources {
		inserted = append(inserted, llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "hi"}}, source))
	}
	data, err := json.Marshal(agent.SplicedData{Target: agent.NextTurn, Inserted: inserted})
	if err != nil {
		t.Fatalf("排负载失败：%v", err)
	}
	return sessionlog.Event{Type: agent.EventInboxSpliced, Seq: 1, Data: data}
}

// webhookSource 造一条本包发的来源。
func webhookSource(t *testing.T) llm.MessageSource {
	t.Helper()
	source, err := NewSource(Provenance{
		Provider: "github", Source: "primary-github", DeliveryID: "d-1", RuleID: "open",
	}, "github webhook handled by open")
	if err != nil {
		t.Fatalf("造来源失败：%v", err)
	}
	return source
}

// exactly 是一份「恰好归属在它自己那个工作区名下」的归属事实。
func exactly() Ownership {
	return Ownership{SessionID: "session-1", WorkspaceID: "ws-1", Owners: []sessionlog.WorkspaceID{"ws-1"}}
}

// ---- 纯函数那一半 ----

// 不是这条不变量管的事件，一律放过——归属查询本身有代价，不该为它们跑。
func TestValidateAdmissionIgnoresEventsItDoesNotOwn(t *testing.T) {
	broken := Ownership{SessionID: "session-1"}

	other := sessionlog.Event{Type: sessionlog.EventUserMessage, Seq: 1, Data: json.RawMessage(`{}`)}
	if err := ValidateAdmission(other, broken); err != nil {
		t.Errorf("别的类型该放过，实际 %v", err)
	}

	// 收件箱事件，但插进去的不是本包发的。
	foreign := splicedEvent(t, llm.PluginSource{Plugin: "someone-else", Extra: json.RawMessage(`{}`)})
	if err := ValidateAdmission(foreign, broken); err != nil {
		t.Errorf("别的插件放的消息该放过，实际 %v", err)
	}

	// 读不回来的负载归 agent 那一层的畸形事件管，不该在这儿再响一遍。
	malformed := sessionlog.Event{Type: agent.EventInboxSpliced, Seq: 1, Data: json.RawMessage(`{"target":`)}
	if err := ValidateAdmission(malformed, broken); err != nil {
		t.Errorf("读不回来的负载该放过，实际 %v", err)
	}
}

func TestValidateAdmissionAcceptsExactOwnership(t *testing.T) {
	event := splicedEvent(t, webhookSource(t))
	if err := ValidateAdmission(event, exactly()); err != nil {
		t.Fatalf("这一份该过，实际 %v", err)
	}
}

// 一批里只要有一条是本包发的，这条不变量就要判。
func TestValidateAdmissionChecksMixedBatches(t *testing.T) {
	event := splicedEvent(t,
		llm.PluginSource{Plugin: "someone-else", Extra: json.RawMessage(`{}`)},
		webhookSource(t),
	)
	if err := ValidateAdmission(event, Ownership{SessionID: "session-1"}); err == nil {
		t.Fatal("混在一批里也该被判到")
	}
}

func TestValidateAdmissionRejectsBrokenOwnership(t *testing.T) {
	event := splicedEvent(t, webhookSource(t))

	cases := map[string]struct {
		ownership Ownership
		want      string
	}{
		"会话头上没记工作区": {
			ownership: Ownership{SessionID: "session-1", Owners: []sessionlog.WorkspaceID{"ws-1"}},
			want:      "has no workspace",
		},
		"一个工作区都没认领": {
			ownership: Ownership{SessionID: "session-1", WorkspaceID: "ws-1"},
			want:      "belongs to 0 Workspaces",
		},
		"两个工作区同时认领": {
			ownership: Ownership{SessionID: "session-1", WorkspaceID: "ws-1",
				Owners: []sessionlog.WorkspaceID{"ws-1", "ws-2"}},
			want: "belongs to 2 Workspaces",
		},
		"认领它的不是它记的那一个": {
			ownership: Ownership{SessionID: "session-1", WorkspaceID: "ws-1",
				Owners: []sessionlog.WorkspaceID{"ws-2"}},
			want: "is owned by",
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateAdmission(event, testCase.ownership)
			if err == nil {
				t.Fatal("该报一次违例")
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("说的不是那件事：%v", err)
			}
		})
	}
}

// ---- 装到注册表上 ----

func TestRegisterInvariantsNeedsItsFourArms(t *testing.T) {
	ownershipOf := func() (Ownership, error) { return exactly(), nil }
	loaded := func() []sessionlog.Event { return nil }
	subscribe := func(func(sessionlog.Event)) func() { return func() {} }

	if _, err := RegisterInvariants(t.Context(), nil, ownershipOf, loaded, subscribe); err == nil {
		t.Error("没有注册表该装不上")
	}
	if _, err := RegisterInvariants(t.Context(), newRegistry(t), nil, loaded, subscribe); err == nil {
		t.Error("没有读归属的路该装不上")
	}
	if _, err := RegisterInvariants(t.Context(), newRegistry(t), ownershipOf, nil, subscribe); err == nil {
		t.Error("没有读已装载日志的路该装不上")
	}
	if _, err := RegisterInvariants(t.Context(), newRegistry(t), ownershipOf, loaded, nil); err == nil {
		t.Error("没有订阅后续事件的路该装不上")
	}
}

// 装的那一刻要把已经在日志里的那些走一遍——一段恢复回来的日志同样受这条约束。
func TestRegisterInvariantsWalksTheLoadedLog(t *testing.T) {
	event := splicedEvent(t, webhookSource(t))
	loaded := func() []sessionlog.Event { return []sessionlog.Event{event} }
	subscribe := func(func(sessionlog.Event)) func() { return func() {} }

	mustNotFail(t, func() {
		dispose, err := RegisterInvariants(t.Context(), newRegistry(t),
			func() (Ownership, error) { return exactly(), nil }, loaded, subscribe)
		if err != nil {
			t.Fatalf("装不上：%v", err)
		}
		dispose()
	})

	reported := mustFail(t, func() {
		_, _ = RegisterInvariants(t.Context(), newRegistry(t),
			func() (Ownership, error) { return Ownership{SessionID: "session-1"}, nil }, loaded, subscribe)
	})
	if !strings.Contains(reported, "has no workspace") {
		t.Errorf("说的不是那件事：%s", reported)
	}
}

// 归属事实在每一次判定时现取：一个会话被挂到别的工作区上不写这条日志，
// 拿装载那一刻的快照去判后来的事件，会把一次真的漂移放过去。
func TestRegisterInvariantsRereadsOwnershipPerAdmission(t *testing.T) {
	var observer func(sessionlog.Event)
	subscribe := func(o func(sessionlog.Event)) func() {
		observer = o
		return func() { observer = nil }
	}
	ownership := exactly()

	dispose, err := RegisterInvariants(t.Context(), newRegistry(t),
		func() (Ownership, error) { return ownership, nil },
		func() []sessionlog.Event { return nil }, subscribe)
	if err != nil {
		t.Fatalf("装不上：%v", err)
	}
	t.Cleanup(dispose)
	if observer == nil {
		t.Fatal("装完之后该订上")
	}

	event := splicedEvent(t, webhookSource(t))
	mustNotFail(t, func() { observer(event) })

	// 会话被挪到了别的工作区名下，日志上一个字都没变。
	ownership.Owners = []sessionlog.WorkspaceID{"ws-2"}
	reported := mustFail(t, func() { observer(event) })
	if !strings.Contains(reported, "is owned by") {
		t.Errorf("说的不是那件事：%s", reported)
	}
}

// 取不到归属事实本身就是一次违例：判不了不等于判过了。
func TestRegisterInvariantsReportsAFailedOwnershipLookup(t *testing.T) {
	var observer func(sessionlog.Event)
	dispose, err := RegisterInvariants(t.Context(), newRegistry(t),
		func() (Ownership, error) { return Ownership{}, errors.New("查不到") },
		func() []sessionlog.Event { return nil },
		func(o func(sessionlog.Event)) func() { observer = o; return func() {} })
	if err != nil {
		t.Fatalf("装不上：%v", err)
	}
	t.Cleanup(dispose)

	reported := mustFail(t, func() { observer(splicedEvent(t, webhookSource(t))) })
	if !strings.Contains(reported, "查不到") {
		t.Errorf("说的不是那件事：%s", reported)
	}
}

// 注销之后那条订阅要拆掉，否则它会继续在别人的派发路径上抛。
func TestRegisterInvariantsUnsubscribesOnRelease(t *testing.T) {
	released := false
	dispose, err := RegisterInvariants(t.Context(), newRegistry(t),
		func() (Ownership, error) { return exactly(), nil },
		func() []sessionlog.Event { return nil },
		func(func(sessionlog.Event)) func() { return func() { released = true } })
	if err != nil {
		t.Fatalf("装不上：%v", err)
	}
	dispose()
	if !released {
		t.Fatal("注销该把那条订阅拆掉")
	}
}
