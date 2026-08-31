// 本文件验那几条只在「东西已经坏了」之后才走得到的边：
// 锁放开又拿回来之间世界变了、后端递进来的东西根本不是 JSON、段在登记之后才坏掉。
//
// 这些路径在正常运行里一次都不会走到，但它们全都是**别人**出错时本包的收场方式，
// 没有用例钉住的话，改动它们不会有任何东西变红。

package settings

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

// stubBackend 是一个可以递出任意东西的后端，包括 [memoryBackend] 造不出来的那些：
// 一份 nil 文档、一段装着 chan 的值。
type stubBackend struct {
	writable bool
	document map[string]any
	// gate 非 nil 时，Writable 会在这里停住，并先往 entered 上报一声。
	// 用来把一次写**确定地**卡在「已经看过 stopped 和登记表、还没排进写队列」的那一刻——
	// 少了 entered 这一声，用例就只能靠睡一觉赌那个 goroutine 已经跑到这儿了。
	gate    chan struct{}
	entered chan struct{}
}

var _ Backend = (*stubBackend)(nil)

func (s *stubBackend) Writable() bool {
	if s.gate != nil {
		s.entered <- struct{}{}
		<-s.gate
	}
	return s.writable
}

func (s *stubBackend) Load(context.Context) (map[string]any, error) { return s.document, nil }

func (s *stubBackend) Persist(context.Context, Namespace, map[string]any) error { return nil }

// bootStub 用一个 stubBackend 起服务。
func bootStub(t *testing.T, backend *stubBackend) *Provider {
	t.Helper()

	provider, err := New(t.Context(), backend, quietLogger())
	if err != nil {
		t.Fatalf("建服务不该失败：%v", err)
	}
	t.Cleanup(provider.Close)
	return provider
}

// TestNewFillsInAMissingDocument 钉住后端递出一份 nil 文档时补成空文档。
//
// 一个刚初始化、还没人写过任何设置的存储，读出来就是「什么都没有」。
// 不补的话后面每一处读段的地方都得自己判一次 nil，漏一处就是一次 panic。
func TestNewFillsInAMissingDocument(t *testing.T) {
	t.Parallel()

	provider := bootStub(t, &stubBackend{writable: true, document: nil})
	scope := register(t, provider, "core", coreConfig{Label: "默认"}, nil)

	if scope.Get().Label != "默认" {
		t.Fatalf("空文档下该拿到默认值，实际 %q", scope.Get().Label)
	}
}

// TestRegisterRejectsATypeThatIsNotAJSONObject 钉住 T 必须投影成一个键值对象。
//
// 一段设置就是一个对象。T 是 int 或者切片时，它编出来的是数或数组，
// 而这条接缝从上到下都拿 map[string]any 当段——放进来的话，
// 分层合并、脱敏、按路径改，每一处都会在一个它没预料到的形状上静默走空。
func TestRegisterRejectsATypeThatIsNotAJSONObject(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, nil)
	if _, _, err := Register(provider, "core", 0, nil); err == nil {
		t.Fatal("T 编不成对象该让登记失败")
	}
	if _, _, err := Register(provider, "core", []string{"a"}, nil); err == nil {
		t.Fatal("T 编成数组也该让登记失败")
	}
}

// TestRegisterAcceptsATypeThatEncodesToNull 钉住编出 null 的 T 补成空段。
//
// 一个 *Config 类型的零值是 nil 指针，编码成 "null"。它是合法 JSON，
// 只是没有任何字段——所以不是拒绝的理由，补成空段接着走。
func TestRegisterAcceptsATypeThatEncodesToNull(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, map[string]any{"core": map[string]any{"label": "存下来的"}})
	scope := register2[*coreConfig](t, provider, "core", nil)

	if scope.Get() == nil || scope.Get().Label != "存下来的" {
		t.Fatalf("空段之上该叠得出存下来的值，实际 %#v", scope.Get())
	}
}

// register2 是 [register] 的任意类型版本。
func register2[T any](t *testing.T, p *Provider, ns Namespace, defaults T) *Scope[T] {
	t.Helper()

	scope, dispose, err := Register(p, ns, defaults, nil)
	if err != nil {
		t.Fatalf("登记 %q 不该失败：%v", string(ns), err)
	}
	t.Cleanup(dispose)
	return scope
}

// pickyString 是一个只对某一个值编码失败的类型。
//
// 它存在的理由：要走到「解析出来的值投影不回原始形状」那条边，
// 就得让**同一个类型**在零值上编得出来、在存下来的那个值上编不出来。
type pickyString string

func (p pickyString) MarshalJSON() ([]byte, error) {
	if p == "炸" {
		return nil, errors.New("这个值编不出来")
	}
	return json.Marshal(string(p))
}

// TestRegisterFailsWhenTheResolvedValueCannotBeProjectedBack 钉住投影不回去就是登记失败。
//
// 解析值要投影回原始形状，后面的比较、脱敏、描述才都在同一种形状上做。
// 投影不回去而放行的话，登记项里会留一个 nil 的原始值，
// 于是下一次写的「变没变」比较是拿新值和 nil 比——永远算变了。
func TestRegisterFailsWhenTheResolvedValueCannotBeProjectedBack(t *testing.T) {
	t.Parallel()

	type pickyConfig struct {
		Label pickyString `json:"label"`
	}
	_, provider := boot(t, map[string]any{"core": map[string]any{"label": "炸"}})

	if _, _, err := Register(provider, "core", pickyConfig{}, nil); err == nil {
		t.Fatal("解析值投影不回去该让登记失败")
	}
}

// TestRegisterFailsWhenTheBackendHandedOutSomethingThatIsNotJSON 钉住后端递进来的段也要过 JSON 这一关。
//
// [Backend.Load] 的返回类型是 map[string]any，编译器拦不住里面塞一个 chan。
// 这条边是本包对后端的最后一道校验：不拦的话，一个编不出来的值会一路走到
// 「解析值」的位置上，而它在任何一次序列化时都会炸——炸在离现场很远的地方。
func TestRegisterFailsWhenTheBackendHandedOutSomethingThatIsNotJSON(t *testing.T) {
	t.Parallel()

	provider := bootStub(t, &stubBackend{
		writable: true,
		document: map[string]any{"core": map[string]any{"label": make(chan int)}},
	})

	if _, _, err := Register(provider, "core", coreConfig{}, nil); err == nil {
		t.Fatal("段里有编不出来的值该让登记失败")
	}
}

// TestRegisterRechecksTheWorldAfterResolving 钉住解析期间放开的那把锁必须重新查一遍。
//
// 解析要跑拥有者给的 Validate，那是用户代码，不能持锁跑（持着它调用户代码，
// 用户代码里再碰一次服务就死锁）。锁一放开，这两件事就都可能在那段窗口里发生：
// 服务被关掉、同一个命名空间被别人抢先登记上。
// 不重查的话，前者会往一个已经停了的服务里塞登记项，后者会把先到的那个覆盖掉。
func TestRegisterRechecksTheWorldAfterResolving(t *testing.T) {
	t.Parallel()

	t.Run("窗口里被别人抢先登记了", func(t *testing.T) {
		t.Parallel()

		_, provider := boot(t, nil)
		var once sync.Once
		_, _, err := Register(provider, "core", coreConfig{}, &Options[coreConfig]{
			Validate: func(coreConfig) error {
				once.Do(func() {
					_, dispose, err := Register(provider, "core", coreConfig{Label: "抢先的"}, nil)
					if err != nil {
						t.Errorf("抢先那次登记不该失败：%v", err)
						return
					}
					t.Cleanup(dispose)
				})
				return nil
			},
		})
		if !errors.Is(err, ErrAlreadyRegistered) {
			t.Fatalf("该报 ErrAlreadyRegistered，实际 %v", err)
		}
		raw, _ := provider.Get("core")
		if raw["label"] != "抢先的" {
			t.Fatalf("抢先的那个该留在原地，实际 %#v", raw)
		}
	})

	t.Run("窗口里服务被关掉了", func(t *testing.T) {
		t.Parallel()

		_, provider := boot(t, nil)
		_, _, err := Register(provider, "core", coreConfig{}, &Options[coreConfig]{
			Validate: func(coreConfig) error {
				provider.Close()
				return nil
			},
		})
		if !errors.Is(err, ErrStopped) {
			t.Fatalf("该报 ErrStopped，实际 %v", err)
		}
	})
}

// TestWriteRechecksTheWorldAfterWaitingInTheQueue 钉住排队期间世界变了要重查。
//
// 写是按命名空间串起来的，排在后面的那次可能等了很久。等的这段时间里
// 服务可能关了、这次登记可能已经注销、名字可能已经归了继任者。
// 拿排队之前那一眼看到的世界接着写，等于把一份过期的判断落进存储。
//
// 卡点用后端的 Writable 做：那正好是「已经查过 stopped 和登记表、还没排进队列」的那一刻。
func TestWriteRechecksTheWorldAfterWaitingInTheQueue(t *testing.T) {
	t.Parallel()

	t.Run("等出来的时候服务已经关了", func(t *testing.T) {
		t.Parallel()

		backend := &stubBackend{writable: true, gate: make(chan struct{}), entered: make(chan struct{})}
		provider := bootStub(t, backend)
		scope := register(t, provider, "core", coreConfig{}, nil)

		failed := make(chan error, 1)
		go func() { failed <- scope.Update(context.Background(), map[string]any{"label": "迟到的"}) }()

		<-backend.entered
		provider.Close()
		close(backend.gate)

		if err := <-failed; !errors.Is(err, ErrStopped) {
			t.Fatalf("该报 ErrStopped，实际 %v", err)
		}
	})

	t.Run("等出来的时候这次登记已经注销了", func(t *testing.T) {
		t.Parallel()

		backend := &stubBackend{writable: true, gate: make(chan struct{}), entered: make(chan struct{})}
		provider := bootStub(t, backend)
		scope, dispose, err := Register(provider, "core", coreConfig{}, nil)
		if err != nil {
			t.Fatalf("登记不该失败：%v", err)
		}

		failed := make(chan error, 1)
		go func() { failed <- scope.Update(context.Background(), map[string]any{"label": "迟到的"}) }()

		<-backend.entered
		dispose()
		_, successor, err := Register(provider, "core", coreConfig{Label: "继任者"}, nil)
		if err != nil {
			t.Fatalf("继任者登记不该失败：%v", err)
		}
		t.Cleanup(successor)
		close(backend.gate)

		if err := <-failed; !errors.Is(err, ErrNotRegistered) {
			t.Fatalf("该报 ErrNotRegistered，实际 %v", err)
		}
		raw, _ := provider.Get("core")
		if raw["label"] != "继任者" {
			t.Fatalf("迟到的那次写不该落到继任者头上，实际 %#v", raw)
		}
	})
}

// TestWriteRefusesWhenTheStoredSectionWentMalformed 钉住段在登记之后坏掉时写会拒。
//
// 登记时段是好的，之后有人在存储上把它编辑成了一个字符串（[Provider.Publish]
// 只会保留上一个好值并记一条日志，不会把文档挡在外面）。
// 此时接着写的话，补丁会叠在一个「当成空段」的东西上，
// 于是一次本意是改一个字段的写，实际后果是把那段坏数据整个换掉——用户没要求过这件事。
func TestWriteRefusesWhenTheStoredSectionWentMalformed(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, nil)
	scope := register(t, provider, "core", coreConfig{}, nil)

	provider.Publish(map[string]any{"core": "不是对象"}, SourceProvider)

	if err := scope.Update(t.Context(), map[string]any{"label": "还想写"}); !errors.Is(err, ErrMalformedSection) {
		t.Fatalf("该报 ErrMalformedSection，实际 %v", err)
	}
}

// TestPublishFillsInItsTwoDefaults 钉住 Publish 的两个缺省。
//
// nil 文档补成空文档，是因为 Publish 的语义是「存储上现在就是这样」——
// 存储被清空了是一件真事，不是一次无效调用。
// 来源为空补成 [SourceProvider]，是因为通知里那个来源字段会被订阅方拿去分流
// （比如「不是我改的才刷界面」），空字符串谁都对不上。
func TestPublishFillsInItsTwoDefaults(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, map[string]any{"core": map[string]any{"label": "存下来的"}})
	scope := register(t, provider, "core", coreConfig{Label: "默认"}, nil)

	sources := make(chan Source, 1)
	t.Cleanup(provider.SubscribeUpdated(func(_ Namespace, _, _ map[string]any, source Source) {
		sources <- source
	}))

	provider.Publish(nil, "")

	if scope.Get().Label != "默认" {
		t.Fatalf("文档清空之后该退回默认值，实际 %q", scope.Get().Label)
	}
	select {
	case source := <-sources:
		if source != SourceProvider {
			t.Fatalf("空来源该补成 %q，实际 %q", string(SourceProvider), string(source))
		}
	default:
		t.Fatal("该发出一次变更通知")
	}
}

// TestPublishTreatsAMalformedPreviousSectionAsAbsent 钉住「之前」读不出来时当成缺席。
//
// 修订号比的是「存下来的东西变没变」，而一段形状不对的旧值读不出一个「之前」。
// 当成缺席之后，任何一个形状正确的后继都会把修订号推上去——
// 反过来（读不出就当没变）的话，从坏段修回好段这一次会不动修订号，
// 而那恰恰是配置界面最需要重读的一次。
func TestPublishTreatsAMalformedPreviousSectionAsAbsent(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, nil)
	register(t, provider, "core", coreConfig{}, nil)

	provider.Publish(map[string]any{"core": "不是对象"}, SourceProvider)
	broken := findDescriptor(t, provider, "core").Revision

	provider.Publish(map[string]any{"core": map[string]any{"label": "修回来了"}}, SourceProvider)
	fixed := findDescriptor(t, provider, "core").Revision

	if fixed == broken {
		t.Fatalf("从坏段修回好段该推动修订号，两次都是 %d", broken)
	}
}

// TestDescribeDropsAUserSectionItCannotDetach 钉住描述不出去的用户段交 nil。
//
// 描述这一面交出去的东西会离开这个包，所以必须是脱钩的副本。
// 一段装着 chan 的值（只可能来自后端）复制不出来——此时交 nil 而不是交原件：
// 交原件等于把服务内部的活数据递给了一个配置界面，它顺手改一下就改了服务。
func TestDescribeDropsAUserSectionItCannotDetach(t *testing.T) {
	t.Parallel()

	provider := bootStub(t, &stubBackend{writable: true})
	register(t, provider, "core", coreConfig{}, nil)

	provider.Publish(map[string]any{"core": map[string]any{"label": make(chan int)}}, SourceProvider)

	if user := findDescriptor(t, provider, "core").User; user != nil {
		t.Fatalf("脱钩不出来的用户段该交 nil，实际 %#v", user)
	}
}
