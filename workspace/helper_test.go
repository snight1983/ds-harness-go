// 本文件的作用：这个包的用例要用的三个假件（文件系统、会话持久化、活会话表），
// 以及把它们和一份内存介质装成一个登记册的那个夹具。
//
// 三个假件都必须能被**逐次注入失败**：本包大半的契约（后端故障绝不冒充「不存在」、
// 两次写中途崩掉之后还能恢复、候选被筛掉只记日志不抹数据）只有在某一次调用失败时
// 才观察得到。

package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/fs"
	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/storage"
	"github.com/snight1983/ds-harness-go/storage/domain"
	"github.com/snight1983/ds-harness-go/storage/storagetest"
)

// quiet 是用例默认用的 logger：本包的日志是诊断用的，跑用例时不该刷屏。
func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

// logSink 把日志摊平成一行行文本，供用例断言「这件事有没有留下痕迹」。
//
// 本包有两处**只记日志、不报错**的行为（候选被筛掉、删成了但标记没清掉），
// 它们的全部可观察后果就是这条日志。没有这个接收器，那两条路径在用例里
// 和「什么都没发生」完全一样。
type logSink struct {
	mutex sync.Mutex
	lines []string
}

func (s *logSink) logger() *slog.Logger { return slog.New(s) }

func (s *logSink) Enabled(context.Context, slog.Level) bool { return true }

func (s *logSink) Handle(_ context.Context, record slog.Record) error {
	line := record.Message
	record.Attrs(func(attr slog.Attr) bool {
		line += " " + attr.Key + "=" + attr.Value.String()
		return true
	})
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.lines = append(s.lines, line)
	return nil
}

func (s *logSink) WithAttrs([]slog.Attr) slog.Handler { return s }
func (s *logSink) WithGroup(string) slog.Handler      { return s }

// contains 报告有没有哪一行同时含住给定的全部片段。
func (s *logSink) contains(fragments ...string) bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for _, line := range s.lines {
		hit := true
		for _, fragment := range fragments {
			if !strings.Contains(line, fragment) {
				hit = false
				break
			}
		}
		if hit {
			return true
		}
	}
	return false
}

// dump 交出全部日志行，断言失败时打出来好定位。
func (s *logSink) dump() []string {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return append([]string(nil), s.lines...)
}

// fakeFS 是一个只认目录树的假文件系统。
//
// 它只实现 [fs.FileSystem] 里本包真正用到的两个方法（Resolve 和 Stat），
// 其余全部 panic——一个悄悄返回零值的桩会让「本包偷偷用了别的方法」这件事查不出来。
type fakeFS struct {
	mutex sync.Mutex
	// aliases 是「别名路径 → 规范路径」，用来造 TargetKey 跨别名相同这件事。
	aliases map[string]string
	// dirs 是存在的目录，键是规范路径。
	dirs map[string]bool
	// files 是存在的常规文件，键是规范路径。
	files map[string]bool
	// resolveErr 按规范路径注入 Resolve 失败。
	resolveErr map[string]error
	// statErr 按规范路径注入 Stat 失败。
	statErr map[string]error
}

func newFakeFS() *fakeFS {
	return &fakeFS{
		aliases:    map[string]string{},
		dirs:       map[string]bool{},
		files:      map[string]bool{},
		resolveErr: map[string]error{},
		statErr:    map[string]error{},
	}
}

// canonical 把一条路径折成规范形：先查别名表，再去掉末尾的斜杠。
func (f *fakeFS) canonical(path string) string {
	if mapped, ok := f.aliases[path]; ok {
		path = mapped
	}
	trimmed := strings.TrimRight(path, "/")
	if trimmed == "" {
		return path
	}
	return trimmed
}

func (f *fakeFS) addDir(path string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.dirs[f.canonical(path)] = true
}

func (f *fakeFS) removeDir(path string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	delete(f.dirs, f.canonical(path))
}

func (f *fakeFS) addFile(path string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.files[f.canonical(path)] = true
}

func (f *fakeFS) alias(from, to string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.aliases[from] = to
}

func (f *fakeFS) failResolve(path string, err error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.resolveErr[f.canonical(path)] = err
}

func (f *fakeFS) failStat(path string, err error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.statErr[f.canonical(path)] = err
}

func (f *fakeFS) Resolve(_ context.Context, path string, _ string) (fs.Target, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	canonical := f.canonical(path)
	if err, ok := f.resolveErr[canonical]; ok {
		return fs.Target{}, err
	}
	return fs.Target{TargetKey: fs.TargetKey("key:" + canonical), DisplayPath: canonical}, nil
}

func (f *fakeFS) Stat(_ context.Context, target fs.Target) (fs.Info, bool, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	path := target.DisplayPath
	if err, ok := f.statErr[path]; ok {
		return fs.Info{}, false, err
	}
	if f.dirs[path] {
		return fs.Info{Type: fs.TypeDirectory}, true, nil
	}
	if f.files[path] {
		return fs.Info{Type: fs.TypeFile}, true, nil
	}
	return fs.Info{}, false, nil
}

func (f *fakeFS) ProcessPath(fs.Target) string { panic("workspace 的用例不该用到 ProcessPath") }
func (f *fakeFS) FileURL(fs.Target) string     { panic("workspace 的用例不该用到 FileURL") }
func (f *fakeFS) Contains(fs.Target, fs.Target) bool {
	panic("workspace 的用例不该用到 Contains")
}
func (f *fakeFS) ReadText(context.Context, fs.Target) (string, error) {
	panic("workspace 的用例不该用到 ReadText")
}

func (f *fakeFS) Lstat(context.Context, string, string) (fs.PathInfo, bool, error) {
	panic("workspace 的用例不该用到 Lstat")
}

func (f *fakeFS) StreamText(context.Context, fs.Target) (iter.Seq2[string, error], error) {
	panic("workspace 的用例不该用到 StreamText")
}

func (f *fakeFS) ReadBytes(context.Context, fs.Target, int64) ([]byte, error) {
	panic("workspace 的用例不该用到 ReadBytes")
}

func (f *fakeFS) ListDir(context.Context, fs.Target) ([]fs.DirEntry, error) {
	panic("workspace 的用例不该用到 ListDir")
}

func (f *fakeFS) WriteText(context.Context, fs.Target, string, fs.WriteIntent) (fs.WriteOutcome, error) {
	panic("workspace 的用例不该用到 WriteText")
}

func (f *fakeFS) EditText(context.Context, fs.Target, fs.EditRequest, *fs.EditIntent) (fs.EditOutcome, error) {
	panic("workspace 的用例不该用到 EditText")
}

// fakePersistence 是一份可注入失败的已落地会话列举面。
type fakePersistence struct {
	mutex   sync.Mutex
	headers []session.SessionHeader
	err     error
	calls   int
}

func (p *fakePersistence) set(headers ...session.SessionHeader) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.headers = headers
}

func (p *fakePersistence) fail(err error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.err = err
}

// count 是到此刻为止被列举了几次，用来钉「这一次打开有没有白花一次 I/O」。
func (p *fakePersistence) count() int {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.calls
}

func (p *fakePersistence) List(context.Context) ([]session.SessionHeader, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	return append([]session.SessionHeader(nil), p.headers...), nil
}

// fakeLive 是一张活会话表。
type fakeLive struct {
	mutex   sync.Mutex
	headers map[session.SessionID]session.SessionHeader
	order   []session.SessionID
}

func newFakeLive() *fakeLive {
	return &fakeLive{headers: map[session.SessionID]session.SessionHeader{}}
}

func (l *fakeLive) add(header session.SessionHeader) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	if _, exists := l.headers[header.ID]; !exists {
		l.order = append(l.order, header.ID)
	}
	l.headers[header.ID] = header
}

func (l *fakeLive) Header(id session.SessionID) (session.SessionHeader, bool) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	header, ok := l.headers[id]
	return header, ok
}

func (l *fakeLive) Headers() []session.SessionHeader {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	headers := make([]session.SessionHeader, 0, len(l.order))
	for _, id := range l.order {
		headers = append(headers, l.headers[id])
	}
	return headers
}

// flakyKV 包住参考内存后端，允许注入落盘失败。
//
// 建/删工作区那两条可恢复的两次写，只有在「第几次写失败」可控的时候才观察得到。
// 记录的写是一次性的（putErr/deleteErr 打完就清），全局槽的写按**第几次**点名
// （globalFailOn），因为一次回滚失败要求同一条路径上连着两次全局写都失败，
// 而一次性的哨兵表达不了「连着两次」。
type flakyKV struct {
	inner *storagetest.MemoryBackend

	mutex     sync.Mutex
	putErr    error
	deleteErr error
	// closeErr 让**关闭**这一步失败一次，用来看关域失败时登记册怎么办。
	closeErr error
	// globalFailOn 是「第 n 次全局写失败」，n 从 1 开始数，由 armGlobal 归零重排。
	globalFailOn map[int]error
	globalCalls  int
}

func newFlakyKV(medium *storagetest.MemoryMedium) *flakyKV {
	return &flakyKV{inner: storagetest.NewMemoryBackend(medium)}
}

func (b *flakyKV) set(mutate func(*flakyKV)) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	mutate(b)
}

// armGlobal 重排全局槽的失败计划：计数归零，failures 的键是从 1 开始的第几次写。
//
// 归零是必需的——用例关心的「第几次」是从它布这个局的那一刻数起，
// 而打开登记册本身也会写全局槽。
func (b *flakyKV) armGlobal(failures map[int]error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.globalCalls = 0
	b.globalFailOn = failures
}

func (b *flakyKV) KV() storage.KVFacet             { return b }
func (b *flakyKV) Close(ctx context.Context) error { return b.inner.Close(ctx) }

func (b *flakyKV) Open(ctx context.Context, descriptor storage.KVUnitDescriptor) (storage.KVUnit, error) {
	unit, err := b.inner.KV().Open(ctx, descriptor)
	if err != nil {
		return nil, err
	}
	return &flakyUnit{backend: b, inner: unit}, nil
}

type flakyUnit struct {
	backend *flakyKV
	inner   storage.KVUnit
}

func (u *flakyUnit) LoadAll(ctx context.Context) (storage.Snapshot, error) {
	return u.inner.LoadAll(ctx)
}

func (u *flakyUnit) PutRecord(ctx context.Context, table, key string, value json.RawMessage) error {
	u.backend.mutex.Lock()
	err := u.backend.putErr
	u.backend.putErr = nil
	u.backend.mutex.Unlock()
	if err != nil {
		return err
	}
	return u.inner.PutRecord(ctx, table, key, value)
}

func (u *flakyUnit) DeleteRecord(ctx context.Context, table, key string) error {
	u.backend.mutex.Lock()
	err := u.backend.deleteErr
	u.backend.deleteErr = nil
	u.backend.mutex.Unlock()
	if err != nil {
		return err
	}
	return u.inner.DeleteRecord(ctx, table, key)
}

func (u *flakyUnit) SetGlobal(ctx context.Context, value json.RawMessage) error {
	u.backend.mutex.Lock()
	u.backend.globalCalls++
	err := u.backend.globalFailOn[u.backend.globalCalls]
	u.backend.mutex.Unlock()
	if err != nil {
		return err
	}
	return u.inner.SetGlobal(ctx, value)
}

func (u *flakyUnit) Close(ctx context.Context) error {
	u.backend.mutex.Lock()
	err := u.backend.closeErr
	u.backend.closeErr = nil
	u.backend.mutex.Unlock()
	if err != nil {
		return err
	}
	return u.inner.Close(ctx)
}

// harness 是一整套装好的登记册：一份内存介质、一个域设施、三个假件。
type harness struct {
	t           *testing.T
	medium      *storagetest.MemoryMedium
	backend     *flakyKV
	facility    *domain.Facility
	filesystem  *fakeFS
	persistence *fakePersistence
	live        *fakeLive
	registry    *Registry
	// logger 是这套夹具装给登记册的 logger，默认静音；要断言日志的用例换成 logSink。
	logger *slog.Logger

	// ids 是 [Config.NewID] 发号器的计数，让用例里的工作区 id 是可预期的。
	idMutex sync.Mutex
	ids     int
	// clock 是可推的时钟，让时间戳在用例里是确定的。
	clockMutex sync.Mutex
	clockAt    time.Time
}

// newHarness 装好除登记册之外的一切；登记册由 [harness.open] 打开。
func newHarness(t *testing.T) *harness {
	t.Helper()
	medium := storagetest.NewMemoryMedium()
	backend := newFlakyKV(medium)
	hub := storage.New()
	if _, err := hub.Backend.Register("main", backend); err != nil {
		t.Fatalf("注册后端不该失败：%v", err)
	}
	facility, err := domain.New(domain.Config{Storage: hub, Backend: "main", Logger: quiet()})
	if err != nil {
		t.Fatalf("建域设施不该失败：%v", err)
	}
	return &harness{
		t:           t,
		medium:      medium,
		backend:     backend,
		facility:    facility,
		filesystem:  newFakeFS(),
		persistence: &fakePersistence{},
		live:        newFakeLive(),
		logger:      quiet(),
		clockAt:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func (h *harness) nextID() string {
	h.idMutex.Lock()
	defer h.idMutex.Unlock()
	h.ids++
	return "ws-" + itoa(h.ids)
}

func (h *harness) now() time.Time {
	h.clockMutex.Lock()
	defer h.clockMutex.Unlock()
	// 每次读都往前走一毫秒：这样 UpdatedAt 的推进本身就是可断言的。
	h.clockAt = h.clockAt.Add(time.Millisecond)
	return h.clockAt
}

// config 是这套夹具对应的登记册配置。
func (h *harness) config() Config {
	return Config{
		Domain:      h.facility,
		Persistence: h.persistence,
		FS:          h.filesystem,
		Live:        h.live,
		NewID:       h.nextID,
		Now:         h.now,
		Logger:      h.logger,
	}
}

// open 打开登记册，失败即用例失败。
func (h *harness) open(ctx context.Context) *Registry {
	h.t.Helper()
	registry, err := Open(ctx, h.config())
	if err != nil {
		h.t.Fatalf("打开登记册不该失败：%v", err)
	}
	h.registry = registry
	h.t.Cleanup(func() { _ = registry.Close(context.Background()) })
	return registry
}

// tryOpen 打开登记册但**不断言成功**：给那些专门看打开失败的用例用。
func (h *harness) tryOpen(ctx context.Context) (*Registry, error) {
	registry, err := Open(ctx, h.config())
	if err != nil {
		return nil, err
	}
	h.registry = registry
	h.t.Cleanup(func() { _ = registry.Close(context.Background()) })
	return registry, nil
}

// close 关掉当前登记册，失败即用例失败。
func (h *harness) close(ctx context.Context) {
	h.t.Helper()
	if h.registry == nil {
		return
	}
	if err := h.registry.Close(ctx); err != nil {
		h.t.Fatalf("关登记册不该失败：%v", err)
	}
	h.registry = nil
}

// reopen 关掉再打开，用来观察「重启之后介质上还剩下什么」。
func (h *harness) reopen(ctx context.Context) *Registry {
	h.t.Helper()
	if h.registry != nil {
		if err := h.registry.Close(ctx); err != nil {
			h.t.Fatalf("关登记册不该失败：%v", err)
		}
	}
	return h.open(ctx)
}

// header 造一条会话头。
func header(id string, cwd string, createdAt int64) session.SessionHeader {
	return session.SessionHeader{ID: session.SessionID(id), Cwd: cwd, CreatedAt: createdAt}
}

// itoa 是给 id 发号器用的十进制转换，不引 strconv 是为了让这个文件的依赖一目了然。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := make([]byte, 0, 8)
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// errBackend 是用例注入落盘失败时用的那条哨兵。
var errBackend = errors.New("假后端故意失败")
