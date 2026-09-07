// 本文件的作用：这一包测试共用的家当——一条内存会话日志、一个装好的审批服务、
// 一份内存设置后端，以及造一个默认预设服务的捷径。

package permissionpresets

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/snight1983/ds-harness-go/feature/interaction/userapproval"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/settings"
)

// memoryLog 是一条只活在内存里的会话日志。
type memoryLog struct {
	mutex  sync.Mutex
	events []sessionlog.Event
}

func (l *memoryLog) Events() []sessionlog.Event {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return append([]sessionlog.Event(nil), l.events...)
}

func (l *memoryLog) Append(kind sessionlog.EventType, data any) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.events = append(l.events, sessionlog.Event{
		Seq: len(l.events), Type: kind, Data: encoded,
	})
	return nil
}

// types 交出这条日志上每条事件的类型，按顺序——写路径的用例断言的正是这个。
func (l *memoryLog) types() []sessionlog.EventType {
	var kinds []sessionlog.EventType
	for _, event := range l.Events() {
		kinds = append(kinds, event.Type)
	}
	return kinds
}

// memoryBackend 是一份可写的、只活在内存里的设置后端。
type memoryBackend struct {
	mutex    sync.Mutex
	document map[string]any
}

func (b *memoryBackend) Writable() bool { return true }

func (b *memoryBackend) Load(context.Context) (map[string]any, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	copied := make(map[string]any, len(b.document))
	for key, value := range b.document {
		copied[key] = value
	}
	return copied, nil
}

func (b *memoryBackend) Persist(_ context.Context, ns settings.Namespace, section map[string]any) error {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if b.document == nil {
		b.document = map[string]any{}
	}
	b.document[string(ns)] = section
	return nil
}

// newSettings 造一个用内存后端的设置服务，stored 是开机时就躺在本包那一段里的东西。
func newSettings(t *testing.T, stored map[string]any) *settings.Provider {
	t.Helper()
	backend := &memoryBackend{}
	if stored != nil {
		backend.document = map[string]any{string(SettingsNamespace): stored}
	}
	provider, err := settings.New(context.Background(), backend,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("造设置服务失败：%v", err)
	}
	t.Cleanup(provider.Close)
	return provider
}

// approvalHarness 是一个装好的审批服务，外加它写出去的那些通知。
type approvalHarness struct {
	service *userapproval.Service
	log     *memoryLog
	key     *scope.Key

	mutex    sync.Mutex
	notified []llm.Message
}

// newApproval 造一个默认策略是 policy 的审批服务，只认一把钥匙、只有一条日志。
func newApproval(t *testing.T, policy userapproval.Policy) *approvalHarness {
	t.Helper()
	harness := &approvalHarness{log: &memoryLog{}, key: scope.NewKey("s1")}
	service, err := userapproval.New(userapproval.Config{
		Policy: policy,
		LogOf: func(*scope.Key) (userapproval.Log, error) {
			return harness.log, nil
		},
		Notify: func(_ *scope.Key, message llm.Message) error {
			harness.mutex.Lock()
			defer harness.mutex.Unlock()
			harness.notified = append(harness.notified, message)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("造审批服务失败：%v", err)
	}
	harness.service = service
	return harness
}

// notifications 交出到目前为止排给模型的那些消息。
func (h *approvalHarness) notifications() []llm.Message {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	return append([]llm.Message(nil), h.notified...)
}

// testPresets 是这一包用例里那张表：两条捆 ask、一条捆 never。
//
// 头两条**捆着同一份旋钮值**，那正是 [EventPreset] 存在的理由——只看旋钮的话
// 它们分不开。
func testPresets() []Preset {
	return []Preset{
		{Name: "guarded", PresetSpec: PresetSpec{
			Approval: userapproval.PolicyAsk, Label: "Guarded", Description: "Ask before acting.",
		}},
		{Name: "guarded-alias", PresetSpec: PresetSpec{Approval: userapproval.PolicyAsk}},
		{Name: "trusted", PresetSpec: PresetSpec{Approval: userapproval.PolicyNever}},
	}
}

// newService 用 [testPresets] 那张表造一个服务，配置的其余部分由调用方补。
func newService(t *testing.T, harness *approvalHarness, adjust func(*Config)) *Service {
	t.Helper()
	config := Config{
		Presets:  testPresets(),
		Approval: harness.service,
		LogOf: func(*scope.Key) (Log, error) {
			return harness.log, nil
		},
	}
	if adjust != nil {
		adjust(&config)
	}
	service, undo, err := New(config)
	if err != nil {
		t.Fatalf("造服务失败：%v", err)
	}
	t.Cleanup(undo)
	return service
}

// presetEvent 造一条负载由调用方原样给的 permission/preset。
func presetEvent(seq int, data string) sessionlog.Event {
	return sessionlog.Event{Seq: seq, Type: EventPreset, Data: json.RawMessage(data)}
}

// preset 造一条负载合法的 permission/preset。
func preset(seq int, name string) sessionlog.Event {
	encoded, _ := json.Marshal(PresetData{Preset: name})
	return sessionlog.Event{Seq: seq, Type: EventPreset, Data: encoded}
}

// policy 造一条 approval/policy。
func policy(seq int, value userapproval.Policy) sessionlog.Event {
	encoded, _ := json.Marshal(userapproval.PolicyData{Policy: value})
	return sessionlog.Event{Seq: seq, Type: userapproval.EventPolicy, Data: encoded}
}
