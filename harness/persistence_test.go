// 本文件的作用：钉住 [harness.Options].Persistence 那道钩子真的接到了循环工厂上——
// 不接它续跑当场报错，接了它续跑就能把一段存下来的会话起回来。
//
// 新增: DSH 没有对应物，理由见 harness 的包文档。

package harness_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/snight1983/ds-harness-go/harness"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// 本文件自造的那个事件类型，只用来验交给钩子的词汇是**并过** ExtraEventTypes 的
// 那一份。交一份没并过的进去，恢复时这条事件会被判成未知事件。
const eventHostOwn sessionlog.EventType = "宿主自己的/事件"

// archivePersistence 是一份最小的会话持久化替身。
//
// 它不落任何介质：一段「存档」就是这个会话标识在 archived 里，读回来的是一个空的
// 活会话。够用了——本文件要证的是那道**接线**，不是某个后端的正确性，后者由
// adapter/datastore/sessionstore 自己的测试和接入方那侧的跨重启测试管。
type archivePersistence struct {
	sessions *session.Store
	archived sessionlog.SessionID

	mutex    sync.Mutex
	prepared int
}

func (p *archivePersistence) Prepare(
	_ context.Context, id sessionlog.SessionID,
) (*session.Preparation, error) {
	p.mutex.Lock()
	p.prepared++
	p.mutex.Unlock()

	live, err := p.sessions.Prepare(id, session.CreateOptions{})
	if err != nil {
		return nil, err
	}
	return session.NewPreparation(live, session.PreparationOptions{}), nil
}

func (p *archivePersistence) List(context.Context) ([]sessionlog.SessionHeader, error) {
	return []sessionlog.SessionHeader{{
		Version: sessionlog.FormatVersion,
		ID:      p.archived,
	}}, nil
}

// prepareCount 报这份持久化被要求读回几次会话。
func (p *archivePersistence) prepareCount() int {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.prepared
}

// TestResumeFailsWithoutThePersistenceHook 钉住不接钩子时续跑当场报错。
//
// 这一条是另一条的对照：没有它，那一条过了也说明不了钩子起了作用。
func TestResumeFailsWithoutThePersistenceHook(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	_, err := w.host.Agents.Resume(t.Context(), w.host.Scope, agent.ResumeOptions{
		ResumeSessionID: "存过的",
		AgentOptions:    agent.Options{Provider: "甲", Model: "m-1"},
	})
	if err == nil {
		t.Fatal("没接持久化时续跑该报错")
	}
	if !strings.Contains(err.Error(), "session persistence is not configured") {
		t.Fatalf("报错说的不是这件事：%v", err)
	}
}

// TestPersistenceHookReachesResume 钉住钩子交出来的那份持久化真的接到了续跑路上。
//
// 这是 [harness.New] 里那一步存在的全部理由：持久化后端要活会话存储和事件词汇才造
// 得出来，而循环工厂要持久化才接得上续跑，两头都在那个函数里面——宿主自己在外面
// 拼的话，会得到一台永远续不了跑的循环。
func TestPersistenceHookReachesResume(t *testing.T) {
	t.Parallel()

	const archived sessionlog.SessionID = "存过的"
	var backend *archivePersistence
	var seen harness.PersistenceDeps

	adapter := &scriptedAdapter{reply: "接着上次说。"}
	host, unwind, err := harness.New(t.Context(), harness.Options{
		Provider:        "甲",
		Model:           "m-1",
		Adapter:         adapter,
		ExtraEventTypes: []sessionlog.EventType{eventHostOwn},
		Persistence: func(
			_ context.Context, deps harness.PersistenceDeps,
		) (harness.SessionPersistence, error) {
			seen = deps
			backend = &archivePersistence{sessions: deps.Sessions, archived: archived}
			return backend, nil
		},
	})
	if err != nil {
		t.Fatalf("装最小闭环失败：%v", err)
	}
	t.Cleanup(func() {
		if err := unwind(context.Background()); err != nil {
			t.Errorf("拆除失败：%v", err)
		}
	})

	// 钩子拿到的三样都得是真的：没有作用域就没处登记写路径，没有活会话存储就没法
	// 对账，而词汇必须是并过 ExtraEventTypes 的那一份。
	if seen.Scope != host.Scope || seen.Sessions != host.Sessions {
		t.Error("钩子拿到的作用域或者活会话存储不是这次装配的那一份")
	}
	if !seen.Vocabulary.Knows(eventHostOwn) {
		t.Error("交给钩子的词汇没并过 ExtraEventTypes")
	}

	handle, err := host.Agents.Resume(t.Context(), host.Scope, agent.ResumeOptions{
		ResumeSessionID: archived,
		AgentOptions:    agent.Options{Provider: "甲", Model: "m-1"},
	})
	if err != nil {
		t.Fatalf("续跑失败：%v", err)
	}
	t.Cleanup(func() { _ = handle.Dispose(context.Background()) })

	if got := handle.Agent.ID(); got != archived {
		t.Errorf("续起来的会话身份是 %q，要的是 %q", string(got), string(archived))
	}
	if got := backend.prepareCount(); got != 1 {
		t.Errorf("持久化被要求读回 %d 次，要的是 1 次", got)
	}
}
