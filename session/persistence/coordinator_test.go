// 本文件的作用：编排器的用例——一个内存后端当替身，覆盖登记／写／读／准备／
// 收编／退场这几条链路。
//
// # 这些测试防的是什么错
//
//   - 一个身份在磁盘上已经有存档时还让人再造一次，之后续跑谁就说不准了。
//   - 一批 seq 对不上的事件被写下去，把存档的连续性契约破掉。
//   - 一次准备拿到的视图和存档已经变了，却照样被当成能续跑的那一份。
//   - 一个活会话认领一份没主的状态时，工作目录或者 seed 对不上却照样认下来。
//   - 热替换重载时把开着的回合当成中断掉的补了收尾，把活会话待会儿要写的
//     真收尾挤掉。
//   - 拆装时缓冲里还压着事件就把后端关了。
//   - 一个身份正被准备独占时还放事件写进去，让那份游标状态和存档对不上。
//
// 源: packages/session/session-persistence/src/coordinator.ts

package persistence

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/snight1983/ds-harness-go/core/scope"
	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/session"
)

// memoryLog 是内存后端里一个身份的整份存档。
type memoryLog struct {
	meta     session.SessionHeader
	events   []session.Event
	revision int
	torn     any
}

// memoryBackend 是一个把所有东西放在内存里的后端替身。
type memoryBackend struct {
	mutex sync.Mutex
	logs  map[session.SessionID]*memoryLog

	// appendErr 非 nil 时每一次 AppendBatch 都以它失败。
	appendErr error
	// loadErr 非 nil 时每一次 LoadStored 都以它失败。
	loadErr error
	// revisionErr 非 nil 时每一次 ReadStoredRevision 都以它失败。
	revisionErr error
	// repairErr 非 nil 时每一次 CommitRepair 都以它失败。
	repairErr error
	// closeErr 是 Close 交回的东西。
	closeErr error
	// closed 记下 Close 被调了几次。
	closed int
	// appends 记下 AppendBatch 被调了几次。
	appends int
	// repairs 记下 CommitRepair 收到过的那几次 closers 长度。
	repairs []int

	// revisionSkews 大于零时 ReadStoredRevision 交回一个假的变更令牌并自减。
	//
	// 用来搭「读到的这一份在提交之前就变了」：编排器核对令牌发现对不上就整个
	// 重来。次数有限是必须的——永远都对不上会让那圈重试转到天荒地老。
	revisionSkews int
	// holdAppend 非 nil 时 AppendBatch 先等它关掉再往下走。
	holdAppend chan struct{}
	// blocked 在头一次真被 holdAppend 挡住时关掉，用例靠它知道「挡住了」。
	blocked chan struct{}
}

// 编译期确认这个替身真的是一个后端，而且带得动关闭。
var (
	_ Backend         = (*memoryBackend)(nil)
	_ ClosableBackend = (*memoryBackend)(nil)
)

func newMemoryBackend() *memoryBackend {
	return &memoryBackend{logs: map[session.SessionID]*memoryLog{}}
}

func (b *memoryBackend) Name() string { return "内存后端" }

// seed 直接往存档里塞一份日志，绕过编排器——用来搭「磁盘上已经有东西」的局面。
func (b *memoryBackend) seed(meta session.SessionHeader, events []session.Event, torn any) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.logs[meta.ID] = &memoryLog{
		meta:     meta,
		events:   cloneEvents(events),
		revision: 1,
		torn:     torn,
	}
}

// storedEvents 交出这个身份此刻落盘的那些事件。
func (b *memoryBackend) storedEvents(id session.SessionID) []session.Event {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	log, ok := b.logs[id]
	if !ok {
		return nil
	}
	return cloneEvents(log.events)
}

// fail 把这几个失败开关一次拧上，并保证它们在用例结束前一直有效。
func (b *memoryBackend) fail(load, revision, repair, append error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.loadErr, b.revisionErr, b.repairErr, b.appendErr = load, revision, repair, append
}

func (b *memoryBackend) LoadStored(ctx context.Context, id session.SessionID) (StoredPrefix, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if b.loadErr != nil {
		return StoredPrefix{}, b.loadErr
	}
	log, ok := b.logs[id]
	if !ok {
		return StoredPrefix{}, fmt.Errorf("%w: %q", ErrSessionNotFound, string(id))
	}
	// 起点照契约填现存最早那条，不是写死 0：这个替身也会被弹（见
	// coordinator_trim_test.go），而读的一侧要靠它分辨「被弹掉了」。
	base := 0
	if len(log.events) > 0 {
		base = log.events[0].Seq
	}
	return StoredPrefix{
		Meta:       log.meta,
		Events:     cloneEvents(log.events),
		BaseSeq:    base,
		Revision:   Revision(fmt.Sprintf("r%d", log.revision)),
		TornMarker: log.torn,
	}, nil
}

// drop 把一个身份的整份存档从内存里抹掉，绕过编排器——用来搭「读过一遍之后
// 那份存档就没了」的局面。
func (b *memoryBackend) drop(id session.SessionID) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	delete(b.logs, id)
}

func (b *memoryBackend) ReadStoredRevision(ctx context.Context, id session.SessionID) (Revision, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if b.revisionErr != nil {
		return "", b.revisionErr
	}
	log, ok := b.logs[id]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrSessionNotFound, string(id))
	}
	if b.revisionSkews > 0 {
		b.revisionSkews--
		return Revision(fmt.Sprintf("歪的r%d", log.revision)), nil
	}
	return Revision(fmt.Sprintf("r%d", log.revision)), nil
}

func (b *memoryBackend) AppendBatch(
	ctx context.Context,
	meta session.SessionHeader,
	events []session.Event,
	materialized bool,
) error {
	// 挡在锁外面等：拿着锁睡会把整个后端一起冻住，连用例自己都读不了。
	b.mutex.Lock()
	hold, blocked := b.holdAppend, b.blocked
	b.mutex.Unlock()
	if hold != nil {
		if blocked != nil {
			b.mutex.Lock()
			if b.blocked != nil {
				close(b.blocked)
				b.blocked = nil
			}
			b.mutex.Unlock()
		}
		<-hold
	}

	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.appends++
	if b.appendErr != nil {
		return b.appendErr
	}
	log, ok := b.logs[meta.ID]
	if !ok {
		if materialized {
			return fmt.Errorf("说是已经落地了，可 %q 在存档里根本没有", string(meta.ID))
		}
		log = &memoryLog{meta: meta}
		b.logs[meta.ID] = log
	}
	log.events = append(log.events, cloneEvents(events)...)
	log.revision++
	return nil
}

func (b *memoryBackend) CommitRepair(
	ctx context.Context,
	meta session.SessionHeader,
	torn any,
	closers []session.Event,
) error {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if b.repairErr != nil {
		return b.repairErr
	}
	log, ok := b.logs[meta.ID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrSessionNotFound, string(meta.ID))
	}
	b.repairs = append(b.repairs, len(closers))
	log.torn = nil
	log.events = append(log.events, cloneEvents(closers)...)
	log.revision++
	return nil
}

func (b *memoryBackend) List(ctx context.Context) ([]session.SessionHeader, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	headers := make([]session.SessionHeader, 0, len(b.logs))
	for _, log := range b.logs {
		headers = append(headers, log.meta)
	}
	return headers, nil
}

func (b *memoryBackend) Close(ctx context.Context) error {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.closed++
	return b.closeErr
}

// harness 是一次用例要的那一整套：后端、活会话表、编排器。
type harness struct {
	backend  *memoryBackend
	sessions *coresession.Store
	owner    *scope.Scope
	*Coordinator

	// drainMayFail 让拆装那一趟的失败不算用例失败。
	//
	// 专门给那些「入册就该失败」的用例：那条错留在 [liveState.err] 里，谁刷盘谁
	// 撞上它——用例自己撞过一次之后，拆装时那趟排干还会再撞一次。两次撞的是
	// 同一条错，第二次不该再被记成一次新的失败。
	drainMayFail bool
}

// newHarness 把一整套搭起来并把写路径挂上，用例结束时把它拆掉。
func newHarness(t *testing.T) *harness {
	t.Helper()

	return newHarnessOn(t, nil)
}

// newHarnessOn 和 [newHarness] 一样，只是让用例先把那个内存后端裹一层。
//
// 裹起来是为了试那几条**可选能力**的分支：编排器会用
// [Seekable] 之类的断言去问后端带不带得动某件事，只有把它裹成一个真的实现了
// 那个接口的类型，那条分支才走得到。
func newHarnessOn(t *testing.T, wrap func(*memoryBackend) Backend) *harness {
	t.Helper()

	return newHarnessWith(t, wrap, CoordinatorOptions{})
}

// newHarnessWith 和 [newHarnessOn] 一样，只是让用例自己给一份编排器选项。
//
// 给得出选项才试得了那几条按策略走的分支——比如条数上限，用例要的是一个小得
// 写几条就撞上的值，而不是默认那 1000 条。
func newHarnessWith(
	t *testing.T, wrap func(*memoryBackend) Backend, options CoordinatorOptions,
) *harness {
	t.Helper()

	backend := newMemoryBackend()
	var plugged Backend = backend
	if wrap != nil {
		plugged = wrap(backend)
	}
	clock := int64(0)
	sessions, err := coresession.NewStore(coresession.StoreOptions{
		Now: func() int64 { clock++; return clock },
	})
	if err != nil {
		t.Fatalf("造不出活会话表：%v", err)
	}
	// 攒批时长给一个大得不会自己到点的值：用例要的是显式刷盘那条路，
	// 一个会自己到点的定时器会让断言看运气。
	coordinator, err := NewCoordinator(
		CoordinatorDeps{Backend: plugged, Sessions: sessions},
		options,
	)
	if err != nil {
		t.Fatalf("造不出编排器：%v", err)
	}
	owner := scope.NewRoot()
	undo, err := coordinator.Install(t.Context(), owner)
	if err != nil {
		t.Fatalf("装不上写路径：%v", err)
	}
	h := &harness{backend: backend, sessions: sessions, owner: owner, Coordinator: coordinator}
	t.Cleanup(func() {
		if err := undo(context.WithoutCancel(t.Context())); err != nil && !h.drainMayFail {
			t.Errorf("拆写路径失败：%v", err)
		}
	})
	return h
}

// createLive 造一个活会话并把它登记进存储，于是那几条观察者会跑起来。
func (h *harness) createLive(t *testing.T, id session.SessionID, options coresession.CreateOptions) *coresession.Session {
	t.Helper()

	live, err := h.sessions.Create(t.Context(), h.owner, id, options)
	if err != nil {
		t.Fatalf("造不出活会话 %q：%v", string(id), err)
	}
	return live
}

// settle 等这个活会话的入册和缓冲写全部落定。
//
// 入册跑在它自己的 goroutine 上，[harness.createLive] 一回来它多半还没做完。
// 直接去看后端会看运气，所以凡是要断言存档内容的用例都先在这里等一下。
func (h *harness) settle(t *testing.T, live *coresession.Session) {
	t.Helper()

	if err := h.flush(live); err != nil {
		t.Fatalf("刷盘失败：%v", err)
	}
}

func TestCoordinator造不出来的那几种情形(t *testing.T) {
	t.Parallel()

	sessions, err := coresession.NewStore(coresession.StoreOptions{})
	if err != nil {
		t.Fatalf("造不出活会话表：%v", err)
	}
	cases := []struct {
		name    string
		deps    CoordinatorDeps
		options CoordinatorOptions
	}{
		{"没后端", CoordinatorDeps{Sessions: sessions}, CoordinatorOptions{}},
		{"没会话表", CoordinatorDeps{Backend: newMemoryBackend()}, CoordinatorOptions{}},
		{
			"池子容量是负数",
			CoordinatorDeps{Backend: newMemoryBackend(), Sessions: sessions},
			CoordinatorOptions{PreparedSessionCacheSize: -1},
		},
		{
			"攒批时长是负数",
			CoordinatorDeps{Backend: newMemoryBackend(), Sessions: sessions},
			CoordinatorOptions{WriteBatchMaxDelay: -1},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewCoordinator(testCase.deps, testCase.options); err == nil {
				t.Fatal("这份依赖不该造得出编排器")
			}
		})
	}
}

func TestCoordinator默认值补齐(t *testing.T) {
	t.Parallel()

	backend := newMemoryBackend()
	sessions, err := coresession.NewStore(coresession.StoreOptions{})
	if err != nil {
		t.Fatalf("造不出活会话表：%v", err)
	}
	coordinator, err := NewCoordinator(CoordinatorDeps{Backend: backend, Sessions: sessions}, CoordinatorOptions{})
	if err != nil {
		t.Fatalf("造不出编排器：%v", err)
	}
	if coordinator.writeBatchMaxDelay != DefaultWriteBatchMaxDelay {
		t.Fatalf("攒批时长该补成默认值，拿到的是 %s", coordinator.writeBatchMaxDelay)
	}
	if coordinator.preparations.capacity != DefaultPreparedSessionCacheSize {
		t.Fatalf("池子容量该补成默认值，拿到的是 %d", coordinator.preparations.capacity)
	}
	if len(coordinator.vocabulary.Types()) == 0 {
		t.Fatal("词汇该补成核心那一套")
	}
	if coordinator.Backend() != Backend(backend) {
		t.Fatal("交出来的后端不是传进去那个")
	}
}

func TestCoordinator登记之后第一次追加才落盘(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := session.SessionID("懒的")
	meta := testHeader(t, id)
	if err := h.Create(t.Context(), meta); err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	if _, err := h.backend.LoadStored(t.Context(), id); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("只登记不该在磁盘上留下东西，拿到的是 %v", err)
	}

	if err := h.Append(t.Context(), id, []session.Event{userEvent(t, 0, "甲")}); err != nil {
		t.Fatalf("追加失败：%v", err)
	}
	if got := len(h.backend.storedEvents(id)); got != 1 {
		t.Fatalf("落盘该有 1 条，拿到 %d 条", got)
	}
}

func TestCoordinator重复登记与撞上存档都被拦下(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	meta := testHeader(t, "重复")
	if err := h.Create(t.Context(), meta); err != nil {
		t.Fatalf("头一次登记就失败了：%v", err)
	}
	if err := h.Create(t.Context(), meta); err == nil {
		t.Fatal("同一个身份不该登记得了第二次")
	}

	existing := testHeader(t, "磁盘上有")
	h.backend.seed(existing, []session.Event{userEvent(t, 0, "甲")}, nil)
	if err := h.Create(t.Context(), existing); err == nil {
		t.Fatal("磁盘上已经有一份日志的身份不该造得出来")
	}

	if err := h.Create(t.Context(), session.SessionHeader{
		Version: session.FormatVersion, ID: "负时间", CreatedAt: -1,
	}); err == nil {
		t.Fatal("CreatedAt 是负数不该登记得了")
	}
}

func TestCoordinator追加要接得上存档的尾巴(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := session.SessionID("连续性")
	if err := h.Create(t.Context(), testHeader(t, id)); err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	if err := h.Append(t.Context(), id, []session.Event{userEvent(t, -1, "负号")}); !errors.Is(err, ErrMalformedSeq) {
		t.Fatalf("第一条 seq 是负数该报 %v，拿到 %v", ErrMalformedSeq, err)
	}
	if err := h.Append(t.Context(), id, nil); err != nil {
		t.Fatalf("空批次该是空操作：%v", err)
	}
	if h.backend.appends != 0 {
		t.Fatalf("这两次都不该真的写到后端上，却写了 %d 次", h.backend.appends)
	}

	// 起点是变量：一个还什么都没落盘的身份，第一批的第一条**定下**它的起点，
	// 不必是 0——一个从被弹过头部的来源分叉出来的子会话正是这样，
	// 见 docs/session-log-limit.md 的原则第 1 条。
	if err := h.Append(t.Context(), id, []session.Event{
		userEvent(t, 500, "甲"), userEvent(t, 501, "乙"),
	}); err != nil {
		t.Fatalf("起点不是 0 的第一批该写得下去：%v", err)
	}
	// 定下来之后，接不上尾巴的那些照旧拒掉。
	if err := h.Append(t.Context(), id, []session.Event{userEvent(t, 505, "跳号")}); err == nil {
		t.Fatal("跳过 502..504 的一批不该写得下去")
	}
	if err := h.Append(t.Context(), id, []session.Event{
		userEvent(t, 502, "丙"), userEvent(t, 504, "丁"),
	}); err == nil {
		t.Fatal("批次自己不连续也不该写得下去")
	}
}

func TestCoordinator认领一个只在磁盘上的身份(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := session.SessionID("只在磁盘上")
	h.backend.seed(testHeader(t, id), []session.Event{userEvent(t, 0, "甲")}, nil)

	// 册子上没有这个身份，追加要先把它认领进来。
	if err := h.Append(t.Context(), id, []session.Event{userEvent(t, 1, "乙")}); err != nil {
		t.Fatalf("认领之后的追加失败了：%v", err)
	}
	if got := len(h.backend.storedEvents(id)); got != 2 {
		t.Fatalf("落盘该有 2 条，拿到 %d 条", got)
	}
}

func TestCoordinator加载一份磁盘上的会话(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := session.SessionID("加载")
	h.backend.seed(testHeader(t, id), []session.Event{
		turnStart(t, 0, 1), userEvent(t, 1, "甲"), turnEnd(t, 2, 1),
	}, nil)

	inspection, err := h.Load(t.Context(), id)
	if err != nil {
		t.Fatalf("加载失败：%v", err)
	}
	if inspection.Meta.ID != id {
		t.Fatalf("视图上的身份是 %q，不是 %q", string(inspection.Meta.ID), string(id))
	}
	if len(inspection.Events) != 3 {
		t.Fatalf("视图该有 3 条事件，拿到 %d 条", len(inspection.Events))
	}
}

func TestCoordinator加载一个开着回合的存档会补收尾(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := session.SessionID("回合还开着")
	h.backend.seed(testHeader(t, id), []session.Event{
		turnStart(t, 0, 1), userEvent(t, 1, "甲"),
	}, nil)

	inspection, err := h.Load(t.Context(), id)
	if err != nil {
		t.Fatalf("加载失败：%v", err)
	}
	if len(inspection.Events) <= 2 {
		t.Fatalf("那条中断的回合该被补上收尾，视图却只有 %d 条", len(inspection.Events))
	}
	if len(h.backend.repairs) == 0 {
		t.Fatal("那次修复该落盘")
	}
	// 修复落盘之后视图和存档是同一份：再加载一次不该再修一次。
	before := len(h.backend.repairs)
	if _, err := h.Load(t.Context(), id); err != nil {
		t.Fatalf("第二次加载失败：%v", err)
	}
	if len(h.backend.repairs) != before {
		t.Fatal("同一段存档不该被修第二次")
	}
}

func TestCoordinator准备之后发布那个会话(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := session.SessionID("续跑")
	h.backend.seed(testHeader(t, id), []session.Event{
		turnStart(t, 0, 1), userEvent(t, 1, "甲"), turnEnd(t, 2, 1),
	}, nil)

	preparation, err := h.Prepare(t.Context(), id)
	if err != nil {
		t.Fatalf("准备失败：%v", err)
	}
	live := preparation.Session()
	if live.ID() != id {
		t.Fatalf("准备出来的会话身份是 %q", string(live.ID()))
	}
	// 独占期间不许有别的东西写进这个身份。
	if err := h.Append(t.Context(), id, []session.Event{userEvent(t, 9, "插队")}); err == nil {
		t.Fatal("独占期间不该写得进去")
	}

	if _, err := h.sessions.Enter(h.owner, live); err != nil {
		t.Fatalf("发布失败：%v", err)
	}
	if err := h.sessions.Announce(t.Context(), live); err != nil {
		t.Fatalf("公布失败：%v", err)
	}
	preparation.Release()

	if _, err := live.Append(session.Event{
		Type:      session.EventUserMessage,
		Data:      userEvent(t, 0, "乙").Data,
		SurfaceOp: session.AppendOp{},
	}); err != nil {
		t.Fatalf("发布之后追加失败：%v", err)
	}
	if err := h.flush(live); err != nil {
		t.Fatalf("刷盘失败：%v", err)
	}
	stored := h.backend.storedEvents(id)
	if len(stored) < 4 {
		t.Fatalf("发布之后那条事件该落盘，存档只有 %d 条", len(stored))
	}
}

func TestCoordinator活着的时候准备不了(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := session.SessionID("活着")
	h.createLive(t, id, coresession.CreateOptions{})
	if _, err := h.Prepare(t.Context(), id); err == nil {
		t.Fatal("一个活着的会话不该准备得了")
	}
}

func TestCoordinator查看不刷盘也不拦开着的回合(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := session.SessionID("查看")
	h.backend.seed(testHeader(t, id), []session.Event{turnStart(t, 0, 1), userEvent(t, 1, "甲")}, nil)

	inspection, err := h.Inspect(t.Context(), id)
	if err != nil {
		t.Fatalf("查看失败：%v", err)
	}
	if len(inspection.Events) == 0 {
		t.Fatal("查看该交回那段历史")
	}
	if len(h.backend.repairs) != 0 {
		t.Fatal("查看不该落地任何修复")
	}
}

func TestCoordinator查看一个活着的会话(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := session.SessionID("查看活的")
	live := h.createLive(t, id, coresession.CreateOptions{})
	inspection, err := h.Inspect(t.Context(), id)
	if err != nil {
		t.Fatalf("查看失败：%v", err)
	}
	if inspection.Meta.ID != live.ID() {
		t.Fatal("查看一个活会话该交回它自己的头")
	}
}

func TestCoordinator加载一个活着的会话要先刷盘(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := session.SessionID("加载活的")
	live := h.createLive(t, id, coresession.CreateOptions{})
	if _, err := live.Append(session.Event{
		Type:      session.EventUserMessage,
		Data:      userEvent(t, 0, "甲").Data,
		SurfaceOp: session.AppendOp{},
	}); err != nil {
		t.Fatalf("追加失败：%v", err)
	}

	inspection, err := h.Load(t.Context(), id)
	if err != nil {
		t.Fatalf("加载失败：%v", err)
	}
	if len(inspection.Events) == 0 {
		t.Fatal("加载该交回那段历史")
	}
	if len(h.backend.storedEvents(id)) == 0 {
		t.Fatal("加载一个活会话该先把它刷下去")
	}
}

func TestCoordinator加载一个空的活会话报不存在(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := session.SessionID("空的活会话")
	h.createLive(t, id, coresession.CreateOptions{})
	if _, err := h.Load(t.Context(), id); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("一条事件都没有的活会话该报不存在，拿到的是 %v", err)
	}
}

func TestCoordinator加载活会话时开着的回合被拦下(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := session.SessionID("活的回合开着")
	live := h.createLive(t, id, coresession.CreateOptions{})
	if _, err := live.Append(session.Event{
		Type: session.EventTurnStart, Data: turnStart(t, 0, 1).Data,
	}); err != nil {
		t.Fatalf("追加失败：%v", err)
	}
	if _, err := h.Load(t.Context(), id); err == nil {
		t.Fatal("回合还开着的活会话不该加载得了")
	}
}

func TestCoordinator读后缀(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := session.SessionID("读后缀")
	h.backend.seed(testHeader(t, id), []session.Event{
		userEvent(t, 0, "甲"), userEvent(t, 1, "乙"), userEvent(t, 2, "丙"),
	}, nil)

	suffix, err := h.ReadFrom(t.Context(), id, 1)
	if err != nil {
		t.Fatalf("读后缀失败：%v", err)
	}
	if len(suffix.Events) != 2 {
		t.Fatalf("从 1 起该读到 2 条，拿到 %d 条", len(suffix.Events))
	}
	// 落在存档之外交回空列表，不是错。
	beyond, err := h.ReadFrom(t.Context(), id, 9)
	if err != nil {
		t.Fatalf("越界的 fromSeq 该交回空列表：%v", err)
	}
	if len(beyond.Events) != 0 {
		t.Fatalf("越界该读到 0 条，拿到 %d 条", len(beyond.Events))
	}
	if _, err := h.ReadFrom(t.Context(), id, -1); !errors.Is(err, ErrMalformedSeq) {
		t.Fatalf("负的 fromSeq 该报 %v，拿到的是 %v", ErrMalformedSeq, err)
	}
	if _, err := h.ReadFrom(t.Context(), "没有这个", 0); !errors.Is(err, ErrSessionNotFound) {
		t.Fatal("读一个不存在的身份该报不存在")
	}
}

func TestCoordinator活会话的事件会攒批落盘(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := session.SessionID("攒批")
	live := h.createLive(t, id, coresession.CreateOptions{})
	for _, text := range []string{"甲", "乙", "丙"} {
		if _, err := live.Append(session.Event{
			Type:      session.EventUserMessage,
			Data:      userEvent(t, 0, text).Data,
			SurfaceOp: session.AppendOp{},
		}); err != nil {
			t.Fatalf("追加 %q 失败：%v", text, err)
		}
	}
	if err := h.flush(live); err != nil {
		t.Fatalf("刷盘失败：%v", err)
	}
	if got := len(h.backend.storedEvents(id)); got != 3 {
		t.Fatalf("该落盘 3 条，拿到 %d 条", got)
	}
}

func TestCoordinator收编一份磁盘上的前缀(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := session.SessionID("收编")
	seed := []session.Event{userEvent(t, 0, "甲"), userEvent(t, 1, "乙")}
	h.backend.seed(testHeader(t, id), seed[:1], nil)

	// 活会话带着一段 seed 起来，它头一条和磁盘上那条一样——这是热替换重载。
	live := h.createLive(t, id, coresession.CreateOptions{Seed: seed})
	h.settle(t, live)

	// 收编之后那截多出来的后缀该被写下去。
	stored := h.backend.storedEvents(id)
	if len(stored) < 2 {
		t.Fatalf("收编该把多出来的后缀写下去，存档只有 %d 条", len(stored))
	}
}

func TestCoordinator对不上的存档被当成撞号拒掉(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.drainMayFail = true
	id := session.SessionID("撞号")
	h.backend.seed(testHeader(t, id), []session.Event{userEvent(t, 0, "磁盘上的")}, nil)

	live := h.createLive(t, id, coresession.CreateOptions{
		Seed: []session.Event{userEvent(t, 0, "活会话的")},
	})
	if err := h.flush(live); err == nil {
		t.Fatal("seed 和存档对不上该被拒掉")
	}
}

func TestCoordinator工作目录不同也是撞号(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.drainMayFail = true
	id := session.SessionID("目录不同")
	// 工作目录得是**本机上**的绝对路径（core/session 那道校验跟着平台走），
	// 所以这里向 testing 要两个真目录，而不是写死一个 POSIX 风格的字面量。
	meta := testHeader(t, id)
	meta.Cwd = t.TempDir()
	h.backend.seed(meta, []session.Event{userEvent(t, 0, "甲")}, nil)

	live := h.createLive(t, id, coresession.CreateOptions{
		Seed: []session.Event{userEvent(t, 0, "甲")},
		Cwd:  t.TempDir(),
	})
	if err := h.flush(live); err == nil {
		t.Fatal("工作目录不同该被当成撞号")
	}
}

func TestCoordinator收编时只截尾巴不补收尾(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := session.SessionID("只截")
	seed := []session.Event{turnStart(t, 0, 1), userEvent(t, 1, "甲")}
	h.backend.seed(testHeader(t, id), seed, "坏尾巴")

	live := h.createLive(t, id, coresession.CreateOptions{Seed: seed})
	h.settle(t, live)
	if len(h.backend.repairs) != 1 {
		t.Fatalf("该做一次修复，做了 %d 次", len(h.backend.repairs))
	}
	if h.backend.repairs[0] != 0 {
		t.Fatalf("收编那次修复不该补任何收尾，却补了 %d 条", h.backend.repairs[0])
	}
}

func TestCoordinator认领一份没主的状态(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := session.SessionID("没主的")
	if err := h.Create(t.Context(), testHeader(t, id)); err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	seed := []session.Event{userEvent(t, 0, "甲")}
	if err := h.Append(t.Context(), id, seed); err != nil {
		t.Fatalf("追加失败：%v", err)
	}

	// 一个活会话带着同一段 seed 起来，该把这份没主的状态认领下来。
	live := h.createLive(t, id, coresession.CreateOptions{Seed: seed})
	h.settle(t, live)

	// seed 那一条已经落过盘了，不该被写第二遍；接在它后面的只该是那条
	// session/end-seed 标记——它是活会话日志里实打实的第二条事件，
	// 认领时那截「超出已落盘前缀」的后缀就是它。
	stored := h.backend.storedEvents(id)
	if len(stored) != 2 {
		t.Fatalf("存档该是 2 条（原来那条加一条封种标记），拿到 %d 条", len(stored))
	}
	if stored[0].Type != session.EventUserMessage {
		t.Fatalf("头一条该还是那条用户消息，拿到 %q", stored[0].Type)
	}
	if stored[1].Type != session.EventSessionEndSeed {
		t.Fatalf("第二条该是封种标记，拿到 %q", stored[1].Type)
	}
}

func TestCoordinator认领没主状态时seed对不上被拒(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.drainMayFail = true
	id := session.SessionID("没主但对不上")
	if err := h.Create(t.Context(), testHeader(t, id)); err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	if err := h.Append(t.Context(), id, []session.Event{userEvent(t, 0, "落盘的")}); err != nil {
		t.Fatalf("追加失败：%v", err)
	}

	live := h.createLive(t, id, coresession.CreateOptions{
		Seed: []session.Event{userEvent(t, 0, "活的")},
	})
	if err := h.flush(live); err == nil {
		t.Fatal("seed 和已落盘的前缀对不上该被拒掉")
	}
}

func TestCoordinator退场之后那个身份可以重新准备(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	id := session.SessionID("退场")
	owner := scope.NewRoot()
	live, err := h.sessions.Create(t.Context(), owner, id, coresession.CreateOptions{})
	if err != nil {
		t.Fatalf("造不出活会话：%v", err)
	}
	if _, err := live.Append(session.Event{
		Type:      session.EventUserMessage,
		Data:      userEvent(t, 0, "甲").Data,
		SurfaceOp: session.AppendOp{},
	}); err != nil {
		t.Fatalf("追加失败：%v", err)
	}
	if err := owner.Dispose(context.WithoutCancel(t.Context())); err != nil {
		t.Fatalf("退场失败：%v", err)
	}

	// 退场那一趟跑在自己的 goroutine 上，Load 会等它收手。
	inspection, err := h.Load(t.Context(), id)
	if err != nil {
		t.Fatalf("退场之后加载失败：%v", err)
	}
	if len(inspection.Events) == 0 {
		t.Fatal("退场时该把缓冲刷下去")
	}
}

func TestCoordinator拆装时先排干再关后端(t *testing.T) {
	t.Parallel()

	backend := newMemoryBackend()
	sessions, err := coresession.NewStore(coresession.StoreOptions{})
	if err != nil {
		t.Fatalf("造不出活会话表：%v", err)
	}
	coordinator, err := NewCoordinator(
		CoordinatorDeps{Backend: backend, Sessions: sessions}, CoordinatorOptions{})
	if err != nil {
		t.Fatalf("造不出编排器：%v", err)
	}
	owner := scope.NewRoot()
	if _, err := coordinator.Install(t.Context(), owner); err != nil {
		t.Fatalf("装不上写路径：%v", err)
	}
	id := session.SessionID("排干")
	live, err := sessions.Create(t.Context(), owner, id, coresession.CreateOptions{})
	if err != nil {
		t.Fatalf("造不出活会话：%v", err)
	}
	if _, err := live.Append(session.Event{
		Type:      session.EventUserMessage,
		Data:      userEvent(t, 0, "甲").Data,
		SurfaceOp: session.AppendOp{},
	}); err != nil {
		t.Fatalf("追加失败：%v", err)
	}

	if err := owner.Dispose(context.WithoutCancel(t.Context())); err != nil {
		t.Fatalf("拆装失败：%v", err)
	}
	if got := len(backend.storedEvents(id)); got == 0 {
		t.Fatal("拆装该把缓冲里的事件排干")
	}
	if backend.closed != 1 {
		t.Fatalf("后端该被关掉一次，关了 %d 次", backend.closed)
	}
}

func TestCoordinator没有作用域装不上(t *testing.T) {
	t.Parallel()

	backend := newMemoryBackend()
	sessions, err := coresession.NewStore(coresession.StoreOptions{})
	if err != nil {
		t.Fatalf("造不出活会话表：%v", err)
	}
	coordinator, err := NewCoordinator(
		CoordinatorDeps{Backend: backend, Sessions: sessions}, CoordinatorOptions{})
	if err != nil {
		t.Fatalf("造不出编排器：%v", err)
	}
	if _, err := coordinator.Install(t.Context(), nil); err == nil {
		t.Fatal("没有作用域不该装得上")
	}
}

func TestCoordinator装载时把已有的活会话补种进来(t *testing.T) {
	t.Parallel()

	backend := newMemoryBackend()
	sessions, err := coresession.NewStore(coresession.StoreOptions{})
	if err != nil {
		t.Fatalf("造不出活会话表：%v", err)
	}
	owner := scope.NewRoot()
	t.Cleanup(func() { _ = owner.Dispose(context.WithoutCancel(t.Context())) })

	id := session.SessionID("补种")
	seed := []session.Event{userEvent(t, 0, "甲")}
	if _, err := sessions.Create(t.Context(), owner, id, coresession.CreateOptions{Seed: seed}); err != nil {
		t.Fatalf("造不出活会话：%v", err)
	}

	coordinator, err := NewCoordinator(
		CoordinatorDeps{Backend: backend, Sessions: sessions}, CoordinatorOptions{})
	if err != nil {
		t.Fatalf("造不出编排器：%v", err)
	}
	if _, err := coordinator.Install(t.Context(), owner); err != nil {
		t.Fatalf("装不上写路径：%v", err)
	}

	live, ok := sessions.Get(id)
	if !ok {
		t.Fatal("活会话不见了")
	}
	if err := coordinator.flush(live); err != nil {
		t.Fatalf("补种之后刷盘失败：%v", err)
	}
	if len(backend.storedEvents(id)) == 0 {
		t.Fatal("补种该把那个已经存在的活会话写下去")
	}
}

func TestCoordinator后端写不下去时错传得回来(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.backend.appendErr = errors.New("磁盘满了")
	id := session.SessionID("写不下去")
	if err := h.Create(t.Context(), testHeader(t, id)); err != nil {
		t.Fatalf("登记失败：%v", err)
	}
	err := h.Append(t.Context(), id, []session.Event{userEvent(t, 0, "甲")})
	if err == nil {
		t.Fatal("后端写不下去时该报错")
	}
	if got := len(h.backend.storedEvents(id)); got != 0 {
		t.Fatalf("写失败之后存档该还是空的，拿到 %d 条", got)
	}
}
