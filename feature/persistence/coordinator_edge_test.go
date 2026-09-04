// 本文件的作用：编排器那些岔路的用例——后端带得动跳读时走的那条捷径、每一处
// 「读回来的东西不成立」的拒绝、ctx 断掉时各条路各自停在哪儿，以及装载失败、
// 退场失败、撞号这几种收不了场的局面。
//
// # 这些测试防的是什么错
//
//   - 跳读那条捷径上漏掉一道判据，让一份身份／版本／词汇不对的后缀被当成好的。
//   - 一次「版本不认得」被裹成「这份存档坏了」，把「换个版本就能读」误报成
//     没救了。
//   - ctx 断掉之后那几圈重试原地空转。
//   - 装观察者装到一半失败，已经装上的那几条却留在原地。
//   - 排干失败时被一个关闭失败盖住，调用方看不见「有东西没写下去」。
//   - 一趟排干走完了，手上却还压着没落盘的事件，而拆解一声不响地成功了。
//   - 退场刷不下去却照样把状态划掉，之后谁也说不清存档停在哪儿。
//
// 源: packages/session/session-persistence/src/coordinator.ts

package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// seekableMemory 是那个内存后端的带跳读版本。
type seekableMemory struct {
	*memoryBackend

	// suffix 非 nil 时 LoadStoredFrom 直接交回它，绕过真实的切片——
	// 用来搭那几种「读回来的后缀自己不成立」的局面。
	suffix *StoredSuffix
	// suffixErr 非 nil 时 LoadStoredFrom 以它失败。
	suffixErr error
}

var _ SeekableBackend = (*seekableMemory)(nil)

func (b *seekableMemory) LoadStoredFrom(
	ctx context.Context,
	id sessionlog.SessionID,
	fromSeq int,
) (StoredSuffix, error) {
	if b.suffixErr != nil {
		return StoredSuffix{}, b.suffixErr
	}
	if b.suffix != nil {
		return *b.suffix, nil
	}
	stored, err := b.LoadStored(ctx, id)
	if err != nil {
		return StoredSuffix{}, err
	}
	events := stored.Events
	if fromSeq >= len(events) {
		events = nil
	} else {
		events = events[fromSeq:]
	}
	return StoredSuffix{Meta: stored.Meta, Events: events}, nil
}

// seekableHarnessOn 搭一套后端带得动跳读的班子，并把那个替身也交出来。
func seekableHarnessOn(t *testing.T) (*harness, *seekableMemory) {
	t.Helper()

	var seekable *seekableMemory
	h := newHarnessOn(t, func(memory *memoryBackend) Backend {
		seekable = &seekableMemory{memoryBackend: memory}
		return seekable
	})
	return h, seekable
}

// unknownEvent 排一条本运行时不认识、又没标可跳过的事件出来。
func unknownEvent(t *testing.T, seq int) sessionlog.Event {
	t.Helper()

	payload, err := json.Marshal(map[string]string{"什么": "都不是"})
	if err != nil {
		t.Fatalf("负载排不出去：%v", err)
	}
	return sessionlog.Event{Type: sessionlog.EventType("未来/没见过"), Seq: seq, Time: int64(seq), Data: payload}
}

func TestCoordinator带得动跳读时只读那一截后缀(t *testing.T) {
	t.Parallel()

	h, _ := seekableHarnessOn(t)
	id := sessionlog.SessionID("跳读")
	h.backend.seed(testHeader(t, id), []sessionlog.Event{
		userEvent(t, 0, "甲"), userEvent(t, 1, "乙"), userEvent(t, 2, "丙"),
	}, nil)

	suffix, err := h.ReadFrom(t.Context(), id, 2)
	if err != nil {
		t.Fatalf("读后缀失败：%v", err)
	}
	if len(suffix.Events) != 1 || suffix.Events[0].Seq != 2 {
		t.Fatalf("该只读回第 2 条，拿到 %d 条", len(suffix.Events))
	}
}

func TestCoordinator跳读回来的东西不成立就被拒(t *testing.T) {
	t.Parallel()

	id := sessionlog.SessionID("跳读拒绝")
	boom := errors.New("跳不动")
	cases := []struct {
		name  string
		setup func(t *testing.T, b *seekableMemory)
	}{
		{"跳读自己就失败", func(t *testing.T, b *seekableMemory) { b.suffixErr = boom }},
		{"身份对不上", func(t *testing.T, b *seekableMemory) {
			meta := testHeader(t, sessionlog.SessionID("别人的"))
			b.suffix = &StoredSuffix{Meta: meta}
		}},
		{"版本不认得", func(t *testing.T, b *seekableMemory) {
			meta := testHeader(t, id)
			meta.Version = sessionlog.FormatVersion + 1
			b.suffix = &StoredSuffix{Meta: meta}
		}},
		{"词汇不认得", func(t *testing.T, b *seekableMemory) {
			b.suffix = &StoredSuffix{
				Meta:   testHeader(t, id),
				Events: []sessionlog.Event{unknownEvent(t, 0)},
			}
		}},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			t.Parallel()

			h, seekable := seekableHarnessOn(t)
			h.backend.seed(testHeader(t, id), []sessionlog.Event{userEvent(t, 0, "甲")}, nil)
			item.setup(t, seekable)

			if _, err := h.ReadFrom(t.Context(), id, 0); err == nil {
				t.Fatal("该被拒掉")
			}
		})
	}
}

func TestCoordinator读后缀的那几种拦法(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := sessionlog.SessionID("读后缀")

	if _, err := h.ReadFrom(t.Context(), id, -1); !errors.Is(err, ErrMalformedSeq) {
		t.Fatalf("负数水位该被拦下，拿到 %v", err)
	}
	if _, err := h.ReadFrom(t.Context(), id, 0); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("不存在的身份该报不存在，拿到 %v", err)
	}

	// 存档过不了那三道判据时，读也该拒掉。
	broken := sessionlog.SessionID("词汇不认得")
	h.backend.seed(testHeader(t, broken), []sessionlog.Event{unknownEvent(t, 0)}, nil)
	if _, err := h.ReadFrom(t.Context(), broken, 0); !errors.Is(err, ErrFormatUnsupported) {
		t.Fatalf("词汇不认得的存档该被拒掉，拿到 %v", err)
	}

	// 水位落在存档之外交回空事件列表，这是契约不是错误。
	good := sessionlog.SessionID("越界")
	h.backend.seed(testHeader(t, good), []sessionlog.Event{userEvent(t, 0, "甲")}, nil)
	suffix, err := h.ReadFrom(t.Context(), good, 9)
	if err != nil {
		t.Fatalf("越界的水位不该报错：%v", err)
	}
	if len(suffix.Events) != 0 {
		t.Fatalf("越界该交回空事件列表，拿到 %d 条", len(suffix.Events))
	}
}

func TestCoordinator几条读路在ctx断掉时都停下(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := sessionlog.SessionID("断掉")
	h.backend.seed(testHeader(t, id), []sessionlog.Event{userEvent(t, 0, "甲")}, nil)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := h.ReadFrom(ctx, id, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("读后缀该停下，拿到 %v", err)
	}
	if _, err := h.Prepare(ctx, id); !errors.Is(err, context.Canceled) {
		t.Fatalf("准备该停下，拿到 %v", err)
	}
	if _, err := h.Load(ctx, id); !errors.Is(err, context.Canceled) {
		t.Fatalf("加载该停下，拿到 %v", err)
	}
	if _, err := h.Inspect(ctx, id); !errors.Is(err, context.Canceled) {
		t.Fatalf("查看该停下，拿到 %v", err)
	}
	if err := h.Create(ctx, testHeader(t, sessionlog.SessionID("断掉时登记"))); !errors.Is(err, context.Canceled) {
		t.Fatalf("登记该停下，拿到 %v", err)
	}
	if err := h.Append(ctx, id, []sessionlog.Event{userEvent(t, 1, "乙")}); !errors.Is(err, context.Canceled) {
		t.Fatalf("追加该停下，拿到 %v", err)
	}
}

func TestCoordinator准备一份读不出来的存档(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := sessionlog.SessionID("读不出来")

	// 「这个身份不在」原样往上抛，不裹成损坏。
	if _, err := h.Prepare(t.Context(), id); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("该报不存在，拿到 %v", err)
	}

	// 一次 I/O 失败也一样原样抛。
	boom := errors.New("盘坏了")
	h.backend.fail(boom, nil, nil, nil)
	if _, err := h.Prepare(t.Context(), id); !errors.Is(err, boom) {
		t.Fatalf("该原样交回 I/O 那条错，拿到 %v", err)
	}
}

func TestCoordinator准备一份自己不成立的存档会被裹成损坏(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := sessionlog.SessionID("坏掉的")
	h.backend.seed(testHeader(t, id), []sessionlog.Event{userEvent(t, 0, "甲"), userEvent(t, 5, "乙")}, nil)

	_, err := h.Prepare(t.Context(), id)
	var corrupted *CorruptionError
	if !errors.As(err, &corrupted) {
		t.Fatalf("该被裹成损坏，拿到 %v", err)
	}
}

func TestCoordinator版本不认得不许被裹成损坏(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := sessionlog.SessionID("版本新")
	meta := testHeader(t, id)
	meta.Version = sessionlog.FormatVersion + 1
	h.backend.seed(meta, nil, nil)

	_, err := h.Prepare(t.Context(), id)
	if !errors.Is(err, ErrFormatUnsupported) {
		t.Fatalf("该是一条版本不认得，拿到 %v", err)
	}
	var corrupted *CorruptionError
	if errors.As(err, &corrupted) {
		t.Fatal("版本不认得不该再被裹一层损坏：那会把「换个版本就能读」误报成没救了")
	}
}

func TestCoordinator词汇不认得也是版本级的拒绝(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := sessionlog.SessionID("词汇新")
	h.backend.seed(testHeader(t, id), []sessionlog.Event{unknownEvent(t, 0)}, nil)

	if _, err := h.Prepare(t.Context(), id); !errors.Is(err, ErrFormatUnsupported) {
		t.Fatalf("该是一条版本不认得，拿到 %v", err)
	}
}

func TestCoordinator存档没了那份成果就不作数(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := sessionlog.SessionID("读令牌")
	h.backend.seed(testHeader(t, id), []sessionlog.Event{userEvent(t, 0, "甲")}, nil)

	// 读令牌读不动时那条错要原样往上抛。
	boom := errors.New("令牌读不动")
	h.backend.fail(nil, boom, nil, nil)
	if _, err := h.Load(t.Context(), id); !errors.Is(err, boom) {
		t.Fatalf("该原样交回读令牌那条错，拿到 %v", err)
	}
}

func TestCoordinator登记时后端读不动(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	boom := errors.New("盘坏了")
	h.backend.fail(boom, nil, nil, nil)

	if err := h.Create(t.Context(), testHeader(t, sessionlog.SessionID("读不动"))); !errors.Is(err, boom) {
		t.Fatalf("该原样交回那条错，拿到 %v", err)
	}
}

func TestCoordinator追加到一份坏掉的存档上(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := sessionlog.SessionID("认领坏的")
	h.backend.seed(testHeader(t, id), []sessionlog.Event{userEvent(t, 0, "甲"), userEvent(t, 5, "乙")}, nil)

	// 认领要先读一遍，读出来的东西不成立就到此为止。
	if err := h.Append(t.Context(), id, []sessionlog.Event{userEvent(t, 2, "丙")}); err == nil {
		t.Fatal("认领一份坏掉的存档该被拒掉")
	}
}

func TestCoordinator已经有活主人时准备不了(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := sessionlog.SessionID("有主了")
	live := h.createLive(t, id, coresession.CreateOptions{})
	h.settle(t, live)

	if _, err := h.Prepare(t.Context(), id); err == nil {
		t.Fatal("这个身份还活着，准备不了它")
	}
}

func TestCoordinator刷盘观察者把缓冲写落下去(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := sessionlog.SessionID("刷盘观察者")
	live := h.createLive(t, id, coresession.CreateOptions{})
	if _, err := live.Append(sessionlog.Event{
		Type:      sessionlog.EventUserMessage,
		Data:      userEvent(t, 0, "甲").Data,
		SurfaceOp: sessionlog.AppendOp{},
	}); err != nil {
		t.Fatalf("追加失败：%v", err)
	}

	// 走会话存储那条刷盘通路，而不是直接调编排器——要试的正是那条观察者。
	if _, err := h.sessions.Flush(t.Context(), live); err != nil {
		t.Fatalf("刷盘失败：%v", err)
	}
	if got := len(h.backend.storedEvents(id)); got != 1 {
		t.Fatalf("该落盘 1 条，拿到 %d 条", got)
	}
}

// brokenSessions 是一张在第 failAt 次登记观察者时失败的活会话表。
type brokenSessions struct {
	Sessions

	failAt int
	calls  int
}

func (s *brokenSessions) next() error {
	s.calls++
	if s.calls == s.failAt {
		return errors.New("装不上")
	}
	return nil
}

func (s *brokenSessions) OnCreated(
	ctx context.Context, owner *scope.Scope, observer coresession.CreatedObserver,
) (func(context.Context) error, error) {
	if err := s.next(); err != nil {
		return nil, err
	}
	return s.Sessions.OnCreated(ctx, owner, observer)
}

func (s *brokenSessions) OnEvent(
	ctx context.Context, owner *scope.Scope, observer coresession.EventObserver,
) (func(context.Context) error, error) {
	if err := s.next(); err != nil {
		return nil, err
	}
	return s.Sessions.OnEvent(ctx, owner, observer)
}

func (s *brokenSessions) OnFlush(
	ctx context.Context, owner *scope.Scope, observer coresession.FlushObserver,
) (func(context.Context) error, error) {
	if err := s.next(); err != nil {
		return nil, err
	}
	return s.Sessions.OnFlush(ctx, owner, observer)
}

func (s *brokenSessions) OnDisposed(
	ctx context.Context, owner *scope.Scope, observer coresession.DisposedObserver,
) (func(context.Context) error, error) {
	if err := s.next(); err != nil {
		return nil, err
	}
	return s.Sessions.OnDisposed(ctx, owner, observer)
}

func TestCoordinator装到一半失败会把已经装上的摘掉(t *testing.T) {
	t.Parallel()

	for failAt := 1; failAt <= 4; failAt++ {
		t.Run(fmt.Sprintf("第%d条装不上", failAt), func(t *testing.T) {
			t.Parallel()

			store, err := coresession.NewStore(coresession.StoreOptions{})
			if err != nil {
				t.Fatalf("造不出活会话表：%v", err)
			}
			coordinator, err := NewCoordinator(CoordinatorDeps{
				Backend:  newMemoryBackend(),
				Sessions: &brokenSessions{Sessions: store, failAt: failAt},
			}, CoordinatorOptions{})
			if err != nil {
				t.Fatalf("造不出编排器：%v", err)
			}
			if _, err := coordinator.Install(t.Context(), scope.NewRoot()); err == nil {
				t.Fatal("装不上该报错")
			}
		})
	}
}

func TestCoordinator作用域已经拆了就装不上(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	owner := scope.NewRoot()
	if err := owner.Dispose(context.WithoutCancel(t.Context())); err != nil {
		t.Fatalf("拆作用域失败：%v", err)
	}
	if _, err := h.Install(t.Context(), owner); err == nil {
		t.Fatal("往一个已经拆了的作用域上装该报错")
	}
}

func TestCoordinator关后端的错在排干成功时才冒出来(t *testing.T) {
	t.Parallel()

	backend := newMemoryBackend()
	boom := errors.New("关不上")
	backend.closeErr = boom

	store, err := coresession.NewStore(coresession.StoreOptions{})
	if err != nil {
		t.Fatalf("造不出活会话表：%v", err)
	}
	coordinator, err := NewCoordinator(
		CoordinatorDeps{Backend: backend, Sessions: store}, CoordinatorOptions{})
	if err != nil {
		t.Fatalf("造不出编排器：%v", err)
	}
	undo, err := coordinator.Install(t.Context(), scope.NewRoot())
	if err != nil {
		t.Fatalf("装不上：%v", err)
	}
	if err := undo(context.WithoutCancel(t.Context())); !errors.Is(err, boom) {
		t.Fatalf("排干没问题时关闭的错该冒出来，拿到 %v", err)
	}
	if backend.closed != 1 {
		t.Fatalf("后端该被关一次，关了 %d 次", backend.closed)
	}
}

func TestCoordinator排干失败时关闭的错要让位(t *testing.T) {
	t.Parallel()

	backend := newMemoryBackend()
	backend.closeErr = errors.New("关不上")
	appendBoom := errors.New("写不下去")
	backend.appendErr = appendBoom

	store, err := coresession.NewStore(coresession.StoreOptions{})
	if err != nil {
		t.Fatalf("造不出活会话表：%v", err)
	}
	coordinator, err := NewCoordinator(
		CoordinatorDeps{Backend: backend, Sessions: store}, CoordinatorOptions{})
	if err != nil {
		t.Fatalf("造不出编排器：%v", err)
	}
	owner := scope.NewRoot()
	undo, err := coordinator.Install(t.Context(), owner)
	if err != nil {
		t.Fatalf("装不上：%v", err)
	}
	live, err := store.Create(t.Context(), owner, sessionlog.SessionID("排不干"), coresession.CreateOptions{
		Seed: []sessionlog.Event{userEvent(t, 0, "甲")},
	})
	if err != nil {
		t.Fatalf("造不出活会话：%v", err)
	}
	_ = live

	err = undo(context.WithoutCancel(t.Context()))
	if !errors.Is(err, appendBoom) {
		t.Fatalf("调用方最该看见的是「有东西没写下去」，拿到 %v", err)
	}
}

// 上面那一条钉的是「那次写自己的错要冒出来」。这一条钉的是它的另一半：那次写
// 的错**报完了**，事件却还在队列里——而这一趟排干走完就再没有人会去写它们了。
// 拆解要是在这时候一声不响地成功，磁盘上留下的是一份短了一截、事后看不出短的
// 会话日志。见 [ErrWritesAbandoned]。
func TestCoordinator排干走完还压着事件就要点名(t *testing.T) {
	t.Parallel()

	backend := newMemoryBackend()
	store, err := coresession.NewStore(coresession.StoreOptions{})
	if err != nil {
		t.Fatalf("造不出活会话表：%v", err)
	}
	coordinator, err := NewCoordinator(
		CoordinatorDeps{Backend: backend, Sessions: store}, CoordinatorOptions{})
	if err != nil {
		t.Fatalf("造不出编排器：%v", err)
	}
	undo, err := coordinator.Install(t.Context(), scope.NewRoot())
	if err != nil {
		t.Fatalf("装不上：%v", err)
	}

	id := sessionlog.SessionID("排干之后还剩着")
	live, err := store.Create(t.Context(), scope.NewRoot(), id, coresession.CreateOptions{})
	if err != nil {
		t.Fatalf("造不出活会话：%v", err)
	}
	// 先让入册落定：这一条要试的是「写路径好着，只是最后那一批送不下去」，
	// 不是「这个会话从一开始就废了」。
	if err := coordinator.flush(live); err != nil {
		t.Fatalf("刷盘失败：%v", err)
	}

	// 从这里开始写不下去，于是这条事件会一直留在那条攒批的队列里。
	backend.fail(nil, nil, nil, errors.New("写不下去"))
	if _, err := live.Append(sessionlog.Event{
		Type:      sessionlog.EventUserMessage,
		Data:      userEvent(t, 0, "甲").Data,
		SurfaceOp: sessionlog.AppendOp{},
	}); err != nil {
		t.Fatalf("追加失败：%v", err)
	}

	err = undo(context.WithoutCancel(t.Context()))

	if !errors.Is(err, ErrWritesAbandoned) {
		t.Fatalf("排干走完还压着事件时应当报 ErrWritesAbandoned，拿到 %v", err)
	}
	// 点得出是哪个会话：一句「有东西没写下去」帮不了要去捞现场的人。
	if !strings.Contains(err.Error(), string(id)) {
		t.Fatalf("这句话里应当点出是哪个会话：%v", err)
	}
	// 那条写自己的错不该被这条哨兵盖住——两条都要在。
	if !strings.Contains(err.Error(), "写不下去") {
		t.Fatalf("那次写自己的错也该留在里面：%v", err)
	}

	// 拆完之后那条事件仍然在手上：点名不等于丢掉，调用方修好之后还刷得下去。
	backend.fail(nil, nil, nil, nil)
	if err := coordinator.flush(live); err != nil {
		t.Fatalf("修好之后应当还刷得下去：%v", err)
	}
	if len(backend.storedEvents(id)) == 0 {
		t.Fatal("那条事件应当在重试之后真的落了盘")
	}
}

func TestCoordinator入册失败之后写也写不下去(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.drainMayFail = true
	id := sessionlog.SessionID("入册就废了")
	h.backend.seed(testHeader(t, id), []sessionlog.Event{userEvent(t, 0, "磁盘上的")}, nil)

	live := h.createLive(t, id, coresession.CreateOptions{
		Seed: []sessionlog.Event{userEvent(t, 0, "活会话的")},
	})
	// 头一次撞上的是入册那条错。
	if err := h.flush(live); err == nil {
		t.Fatal("入册就该失败")
	}
	// 之后每一次写都还是同一条错——那条结论落定之后不会再变。
	if err := h.flush(live); err == nil {
		t.Fatal("入册的结论该一直有效")
	}
}

func TestCoordinator退场刷不下去就不划掉状态(t *testing.T) {
	t.Parallel()

	backend := newMemoryBackend()
	store, err := coresession.NewStore(coresession.StoreOptions{})
	if err != nil {
		t.Fatalf("造不出活会话表：%v", err)
	}
	coordinator, err := NewCoordinator(
		CoordinatorDeps{Backend: backend, Sessions: store}, CoordinatorOptions{})
	if err != nil {
		t.Fatalf("造不出编排器：%v", err)
	}
	root := scope.NewRoot()
	undo, err := coordinator.Install(t.Context(), root)
	if err != nil {
		t.Fatalf("装不上：%v", err)
	}

	owner := scope.NewRoot()
	live, err := store.Create(t.Context(), owner, sessionlog.SessionID("退不掉"), coresession.CreateOptions{})
	if err != nil {
		t.Fatalf("造不出活会话：%v", err)
	}
	if err := coordinator.flush(live); err != nil {
		t.Fatalf("刷盘失败：%v", err)
	}
	if _, err := live.Append(sessionlog.Event{
		Type:      sessionlog.EventUserMessage,
		Data:      userEvent(t, 0, "甲").Data,
		SurfaceOp: sessionlog.AppendOp{},
	}); err != nil {
		t.Fatalf("追加失败：%v", err)
	}

	// 退场那一趟会先刷盘，而刷盘这时候写不下去——错只落进日志，不往上抛。
	backend.fail(nil, nil, nil, errors.New("写不下去"))
	if err := owner.Dispose(context.WithoutCancel(t.Context())); err != nil {
		t.Fatalf("拆会话作用域失败：%v", err)
	}

	// 那条退场登记该已经收手（下面这句会等它），而这个身份的状态没被划掉。
	if err := coordinator.waitForRetirement(t.Context(), live.ID()); err != nil {
		t.Fatalf("等退场失败：%v", err)
	}
	if coordinator.stateOf(live.ID()) == nil {
		t.Fatal("刷不下去时不该把状态划掉：划掉等于把没落盘的事件和游标一起丢了")
	}

	backend.fail(nil, nil, nil, nil)
	if err := undo(context.WithoutCancel(t.Context())); err != nil {
		t.Fatalf("拆写路径失败：%v", err)
	}
}

func TestCoordinator没登记过的会话退场是空操作(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	live, err := h.sessions.PrepareRestored(sessionlog.SessionID("没登记"), coresession.RestoreOptions{
		Seed:   []sessionlog.Event{userEvent(t, 0, "甲")},
		Header: testHeader(t, sessionlog.SessionID("没登记")),
	})
	if err != nil {
		t.Fatalf("恢复不出会话：%v", err)
	}
	h.retire(live)
	if err := h.waitForRetirement(t.Context(), live.ID()); err != nil {
		t.Fatalf("等退场失败：%v", err)
	}
}

func TestCoordinator收编时那几道判据(t *testing.T) {
	t.Parallel()

	seed := []sessionlog.Event{userEvent(t, 0, "甲")}
	cases := []struct {
		name  string
		setup func(t *testing.T, h *harness, id sessionlog.SessionID)
	}{
		{"存档里的身份对不上", func(t *testing.T, h *harness, id sessionlog.SessionID) {
			meta := testHeader(t, sessionlog.SessionID("别人的"))
			meta.ID = sessionlog.SessionID("别人的")
			h.backend.mutex.Lock()
			h.backend.logs[id] = &memoryLog{meta: meta, events: cloneEvents(seed), revision: 1}
			h.backend.mutex.Unlock()
		}},
		{"版本不认得", func(t *testing.T, h *harness, id sessionlog.SessionID) {
			meta := testHeader(t, id)
			meta.Version = sessionlog.FormatVersion + 1
			h.backend.seed(meta, seed, nil)
		}},
		{"词汇不认得", func(t *testing.T, h *harness, id sessionlog.SessionID) {
			h.backend.seed(testHeader(t, id), []sessionlog.Event{unknownEvent(t, 0)}, nil)
		}},
		{"修复写不下去", func(t *testing.T, h *harness, id sessionlog.SessionID) {
			h.backend.seed(testHeader(t, id), seed, "坏尾巴")
			h.backend.fail(nil, nil, errors.New("修不了"), nil)
		}},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.drainMayFail = true
			id := sessionlog.SessionID("收编判据")
			item.setup(t, h, id)

			live := h.createLive(t, id, coresession.CreateOptions{Seed: seed})
			if err := h.flush(live); err == nil {
				t.Fatal("该被拒掉")
			}
		})
	}
}

func TestCoordinator认领没主状态时工作目录不同是撞号(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.drainMayFail = true
	id := sessionlog.SessionID("没主但目录不同")
	meta := testHeader(t, id)
	meta.WorkspaceID = "ws-这一处"
	if err := h.Create(t.Context(), meta); err != nil {
		t.Fatalf("登记失败：%v", err)
	}

	live := h.createLive(t, id, coresession.CreateOptions{WorkspaceID: "ws-另一处"})
	if err := h.flush(live); err == nil {
		t.Fatal("工作目录不同该被当成撞号")
	}
}

func TestCoordinator这个身份已经绑在另一个活会话上(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.drainMayFail = true
	id := sessionlog.SessionID("两个活的")

	// 会话存储本身不让同一个身份出现两个活会话，所以这里直接把册子摆成
	// 「已经有一个主人、而且它已经落过盘」的样子——那正是编排器要拦的局面。
	other, err := h.sessions.PrepareRestored(sessionlog.SessionID("另一个身份"), coresession.RestoreOptions{
		Seed:   []sessionlog.Event{userEvent(t, 0, "甲")},
		Header: testHeader(t, sessionlog.SessionID("另一个身份")),
	})
	if err != nil {
		t.Fatalf("恢复不出占位的会话：%v", err)
	}
	h.mutex.Lock()
	h.states[id] = &sessionState{meta: testHeader(t, id), nextSeq: 1, started: true, materialized: true, owner: other}
	h.mutex.Unlock()

	live := h.createLive(t, id, coresession.CreateOptions{})
	if err := h.flush(live); err == nil {
		t.Fatal("这个身份已经绑在另一个活会话上，该是撞号")
	}
}

func TestCoordinator真被抛弃的身份可以被接手(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := sessionlog.SessionID("被抛弃")

	// 头一个会话什么都没写，也没落过盘——它是一个真被抛弃的身份。
	abandoned, err := h.sessions.PrepareRestored(id, coresession.RestoreOptions{
		Seed:   []sessionlog.Event{userEvent(t, 0, "甲")},
		Header: testHeader(t, id),
	})
	if err != nil {
		t.Fatalf("恢复不出会话：%v", err)
	}
	h.mutex.Lock()
	h.states[id] = &sessionState{meta: testHeader(t, id), owner: abandoned}
	h.mutex.Unlock()

	live := h.createLive(t, id, coresession.CreateOptions{})
	h.settle(t, live)

	h.mutex.Lock()
	owner := h.states[id].owner
	h.mutex.Unlock()
	if owner != live {
		t.Fatal("一个真被抛弃的身份该让给新来的这个活会话")
	}
}
