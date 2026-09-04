// 本文件的作用：验 [Install] 那一条边——一个会话退场之后，它在计量器这边缓着的
// 重放状态确实被丢掉了。

package tokenmeter

import (
	"context"
	"testing"

	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// # 这个测试防的是什么错
//
// 计量器按 [sessionlog.SessionID] 缓一份**随会话长度线性增长**的表面节点表。
// 没有这条边，一台长期在跑的服务每量过一个会话就多留一张，只增不减——而这件事
// 从外面完全看不出来：读出来的每一个数都还是对的，只是内存一直涨。

// stubSessions 是一道假的会话退场广播，把挂上来的观察者留在手边。
type stubSessions struct {
	observer coresession.DisposedObserver
	undone   bool
	err      error
}

func (s *stubSessions) OnDisposed(
	_ context.Context, _ *scope.Scope, observer coresession.DisposedObserver,
) (func(context.Context) error, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.observer = observer
	return func(context.Context) error { s.undone = true; return nil }, nil
}

// disposedSession 造一个能递给观察者的真会话，id 和 [newSession] 那个假会话一致。
func disposedSession(t *testing.T) *coresession.Session {
	t.Helper()

	live, err := coresession.NewSession("s", coresession.Options{})
	if err != nil {
		t.Fatalf("造会话失败：%v", err)
	}
	return live
}

func TestInstall少了必填的那两样就拒(t *testing.T) {
	t.Parallel()

	if _, err := Install(t.Context(), scope.NewRoot(), nil, &stubSessions{}); err == nil {
		t.Fatal("没有计量器也装上去了")
	}
	if _, err := Install(t.Context(), scope.NewRoot(), New(), nil); err == nil {
		t.Fatal("没有会话退场广播也装上去了")
	}
}

func TestInstall把摘除函数原样交回去(t *testing.T) {
	t.Parallel()

	sessions := &stubSessions{}
	undo, err := Install(t.Context(), scope.NewRoot(), New(), sessions)
	if err != nil {
		t.Fatalf("装不上：%v", err)
	}
	if err := undo(t.Context()); err != nil {
		t.Fatalf("摘不下来：%v", err)
	}
	if !sessions.undone {
		t.Fatal("摘除函数没落到广播那一侧")
	}
}

func TestInstall会话退场之后那份重放状态被丢掉(t *testing.T) {
	t.Parallel()

	// 判据是「同一个 id 下一次会不会从头重折」。折叠是确定的，所以折出来的数
	// 无论重不重折都一样——唯一能从包外看见差别的地方，是把一份**折得动**的日志
	// 换成一份同样长、却折不动的日志：缓着状态时那一段根本不会被读（
	// [TokenMeter.sync] 的循环一步都不走），丢掉之后才会被重折、才会报错。
	meter := New()
	sessions := &stubSessions{}
	if _, err := Install(t.Context(), scope.NewRoot(), meter, sessions); err != nil {
		t.Fatalf("装不上：%v", err)
	}

	good := newSession(stepStartEvent(t, 1, 1), assistantEvent(t, 1, 1, "答", nil))
	if _, err := meter.Measure(good, nil); err != nil {
		t.Fatalf("头一遍就折不动：%v", err)
	}

	// 同样两条、同样的起点，但第二条助手消息配不上任何一条 step/start。
	broken := newSession(userEvent(t, "问"), assistantEvent(t, 1, 1, "答", nil))

	// 对照组：状态还缓着，这一段压根不会被读，所以不报错。
	if _, err := meter.Measure(broken, nil); err != nil {
		t.Fatalf("状态还缓着的时候就重折了：%v", err)
	}

	sessions.observer(disposedSession(t))

	if _, err := meter.Measure(broken, nil); err == nil {
		t.Fatal("会话退场之后那份状态还在，这一段没有被重折")
	}
}

// 这一条钉的是「观察者认的是会话身份」：退场的是别人，我这份状态不该跟着没。
func TestInstall别的会话退场不动这一份(t *testing.T) {
	t.Parallel()

	meter := New()
	sessions := &stubSessions{}
	if _, err := Install(t.Context(), scope.NewRoot(), meter, sessions); err != nil {
		t.Fatalf("装不上：%v", err)
	}

	good := newSession(stepStartEvent(t, 1, 1), assistantEvent(t, 1, 1, "答", nil))
	if _, err := meter.Measure(good, nil); err != nil {
		t.Fatalf("头一遍就折不动：%v", err)
	}

	other, err := coresession.NewSession(sessionlog.SessionID("别人"), coresession.Options{})
	if err != nil {
		t.Fatalf("造会话失败：%v", err)
	}
	sessions.observer(other)

	broken := newSession(userEvent(t, "问"), assistantEvent(t, 1, 1, "答", nil))
	if _, err := meter.Measure(broken, nil); err != nil {
		t.Fatalf("别人退场把这一份也丢了：%v", err)
	}
}
