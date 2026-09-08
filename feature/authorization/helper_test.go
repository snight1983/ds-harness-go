package authorization

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/credentials"
	"github.com/snight1983/ds-harness-go/storage"
	"github.com/snight1983/ds-harness-go/storage/domain"
	"github.com/snight1983/ds-harness-go/storage/storagetest"
)

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

// testKey 是用例里那条被授权的凭据。
const testKey credentials.Key = "demo/token"

// fakeCredentials 是本包用得着的那两件凭据能力的替身。
//
// 它只记「这条记录在不在」，不记值——本包本来也读不到值。
type fakeCredentials struct {
	mu        sync.Mutex
	present   map[credentials.Key]bool
	listeners []credentials.RecordListener
	// describeErr 让用例演一次「核实的时候介质坏了」。
	describeErr error
}

func newFakeCredentials() *fakeCredentials {
	return &fakeCredentials{present: map[credentials.Key]bool{}}
}

func (f *fakeCredentials) DescribeRecord(_ context.Context, key credentials.Key) (credentials.RecordInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.describeErr != nil {
		return credentials.RecordInfo{}, f.describeErr
	}
	return credentials.RecordInfo{Configured: f.present[key], Writable: true}, nil
}

func (f *fakeCredentials) SubscribeRecord(listener credentials.RecordListener) func() {
	f.mu.Lock()
	index := len(f.listeners)
	f.listeners = append(f.listeners, listener)
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.listeners[index] = nil
	}
}

// commit 演一次「流程自己把记录写下去了」。
func (f *fakeCredentials) commit(key credentials.Key) {
	f.mu.Lock()
	f.present[key] = true
	listeners := make([]credentials.RecordListener, len(f.listeners))
	copy(listeners, f.listeners)
	f.mu.Unlock()
	for _, listener := range listeners {
		if listener != nil {
			listener(key)
		}
	}
}

// forget 演一次「提交完又被删掉了」。
func (f *fakeCredentials) forget(key credentials.Key) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.present, key)
}

// scriptedInteraction 是一个照本子答话的界面。
type scriptedInteraction struct {
	mu       sync.Mutex
	answers  []string
	err      error
	notices  []Notice
	prompts  []Prompt
	declined bool
}

func (s *scriptedInteraction) Notify(notice Notice) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notices = append(s.notices, notice)
}

func (s *scriptedInteraction) Prompt(_ context.Context, prompt Prompt) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prompts = append(s.prompts, prompt)
	if s.declined {
		return "", Declined("用例里的人说不")
	}
	if s.err != nil {
		return "", s.err
	}
	if len(s.answers) == 0 {
		return "", errors.New("本子上没词了")
	}
	answer := s.answers[0]
	s.answers = s.answers[1:]
	return answer, nil
}

func (s *scriptedInteraction) seen() []Prompt {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Prompt(nil), s.prompts...)
}

func (s *scriptedInteraction) heard() []Notice {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Notice(nil), s.notices...)
}

// harness 是一份共用同一份介质的夹具：一份介质可以开出好几个 [Service]，
// 用来演「进程没了，另一个副本接上」。
type harness struct {
	t       *testing.T
	medium  *storagetest.MemoryMedium
	creds   *fakeCredentials
	ids     int
	idMutex sync.Mutex

	clockMutex sync.Mutex
	clockAt    time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return &harness{
		t:       t,
		medium:  storagetest.NewMemoryMedium(),
		creds:   newFakeCredentials(),
		clockAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// now 每读一次往前走一毫秒，让时间戳在用例里既确定又单调。
func (h *harness) now() time.Time {
	h.clockMutex.Lock()
	defer h.clockMutex.Unlock()
	h.clockAt = h.clockAt.Add(time.Millisecond)
	return h.clockAt
}

// advance 把时钟往前推，用来让一份租约过期。
func (h *harness) advance(delta time.Duration) {
	h.clockMutex.Lock()
	defer h.clockMutex.Unlock()
	h.clockAt = h.clockAt.Add(delta)
}

// newID 发一个可预期的尝试身份。
func (h *harness) newID() string {
	h.idMutex.Lock()
	defer h.idMutex.Unlock()
	h.ids++
	return "attempt-" + string(rune('0'+h.ids))
}

// open 在同一份介质上开一个副本。
//
// replica 是这个副本的身份；两个不同身份的 [Service] 就是两个副本。
//
// 每个副本一套自己的后端和中枢，共用那一份介质——这正是「两个进程连同一个数据库」
// 的样子。共用一个中枢做不到：[storagetest.MemoryBackend] 会挡住「同一个单元开两次」，
// 而那条守卫说的是一个进程内的 bug，不是两个进程之间的。
func (h *harness) open(replica string) *Service {
	h.t.Helper()
	hub := storage.New()
	if _, err := hub.Backend.Register("main", storagetest.NewMemoryBackend(h.medium)); err != nil {
		h.t.Fatalf("注册后端不该失败：%v", err)
	}
	facility, err := domain.New(domain.Config{Storage: hub, Backend: "main", Logger: quiet()})
	if err != nil {
		h.t.Fatalf("建域设施不该失败：%v", err)
	}
	service, err := Open(h.t.Context(), Config{
		Domain:       facility,
		Credentials:  h.creds,
		Replica:      replica,
		Lease:        time.Minute,
		NewAttemptID: h.newID,
		Now:          h.now,
		Logger:       quiet(),
	})
	if err != nil {
		h.t.Fatalf("打开授权服务不该失败：%v", err)
	}
	h.t.Cleanup(func() { _ = service.Close(context.WithoutCancel(h.t.Context())) })
	return service
}

// register 往一个副本上装一条流程。
func (h *harness) register(service *Service, run func(ctx context.Context, session Session) error) {
	h.t.Helper()
	_, err := service.RegisterFlow(Flow{
		Key:     testKey,
		Label:   "演示令牌",
		Methods: []Method{{ID: "device", Label: "设备码"}, {ID: "paste", Label: "粘贴"}},
		Run:     run,
	})
	if err != nil {
		h.t.Fatalf("登记流程不该失败：%v", err)
	}
}

// attemptOn 直接从介质上读那条尝试记录。
func (h *harness) attemptOn(service *Service) (Attempt, bool) {
	h.t.Helper()
	attempt, found, err := service.table.Get(h.t.Context(), string(testKey))
	if err != nil {
		h.t.Fatalf("读尝试记录不该失败：%v", err)
	}
	return attempt, found
}
