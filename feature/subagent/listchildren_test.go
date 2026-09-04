// 本文件的作用：只读列举那两个入口的测试——服务缺席与取消这两道门、活优先的
// 语料合并与次序、创建窗口那一行的略过、冷孩子那三级身份梯子（缓存闸门、
// 生命见证、重折）、以及整棵树的先序遍历。

package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/feature/persistence"
	"github.com/snight1983/ds-harness-go/feature/projectioncache"
	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/sessionlog/projection"
	"github.com/snight1983/ds-harness-go/storage"
	"github.com/snight1983/ds-harness-go/storage/domain"
	"github.com/snight1983/ds-harness-go/storage/storagetest"
)

// ---- 装配 ----

// errNoFakeMethod 是假持久化那些用不到的方法统一给出的说法：这条路一个都不该调到。
var errNoFakeMethod = errors.New("假持久化没实现这个方法")

// fakePersistence 是一个只有 List 和 Inspect 有意义的假会话持久化。
//
// 它同时记下 Inspect 被谁调过——「哪一级梯子赢了」这件事只有从这里看得出来：
// 一次被缓存挡住的列举，症状就是这张单子是空的。
type fakePersistence struct {
	mu sync.Mutex
	// listed 是 List 交出去的那些头，按登记次序。
	listed []sessionlog.SessionHeader
	// stored 是每个会话在存档里那份逻辑视图。
	stored map[sessionlog.SessionID]persistence.Inspection
	// listErr 非 nil 时 List 报这个错。
	listErr error
	// onList 在这次介质读**之后**、这份结果被判读**之前**跑，用来在两者之间插进
	// 取消这类动作——包括那条「介质自己也报了错」的路，那条路上要看的正是这两件事
	// 谁说了算。
	onList func()
	// inspectErr 是安排给某个会话的探视失败。
	inspectErr map[sessionlog.SessionID]error
	// onInspect 在每次探视**之前**跑，用来在读之间插进取消这类动作。
	onInspect func(sessionlog.SessionID)
	// inspected 是历次探视的目标，按调用顺序。
	inspected []sessionlog.SessionID
}

func newPersistence() *fakePersistence {
	return &fakePersistence{
		stored:     map[sessionlog.SessionID]persistence.Inspection{},
		inspectErr: map[sessionlog.SessionID]error{},
	}
}

// put 把一份存档摆进去：列出来那份头和存档里那份头是同一个。
func (p *fakePersistence) put(header sessionlog.SessionHeader, events ...sessionlog.Event) {
	p.listed = append(p.listed, header)
	p.stored[header.ID] = persistence.Inspection{Meta: header, Events: events}
}

// putStored 让列出来那份头和存档里那份头**不一样**，用来驱动生命见证那道检查。
func (p *fakePersistence) putStored(listed, stored sessionlog.SessionHeader, events ...sessionlog.Event) {
	p.listed = append(p.listed, listed)
	p.stored[listed.ID] = persistence.Inspection{Meta: stored, Events: events}
}

// probes 给出历次探视的目标。
func (p *fakePersistence) probes() []sessionlog.SessionID {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]sessionlog.SessionID(nil), p.inspected...)
}

func (p *fakePersistence) List(context.Context) ([]sessionlog.SessionHeader, error) {
	p.mu.Lock()
	hook := p.onList
	err := p.listErr
	listed := append([]sessionlog.SessionHeader(nil), p.listed...)
	p.mu.Unlock()

	// 钩子在锁外跑：它多半要去动管理器，而那边可能反过来读这份存档。这里排在错误
	// 之前，好让一次「读失败」和一次「读完就走人」凑得到一块儿。
	if hook != nil {
		hook()
	}
	if err != nil {
		return nil, err
	}
	return listed, nil
}

func (p *fakePersistence) Inspect(_ context.Context, id sessionlog.SessionID) (persistence.Inspection, error) {
	p.mu.Lock()
	hook := p.onInspect
	p.inspected = append(p.inspected, id)
	err := p.inspectErr[id]
	inspection, found := p.stored[id]
	p.mu.Unlock()

	if hook != nil {
		hook(id)
	}
	if err != nil {
		return persistence.Inspection{}, err
	}
	if !found {
		return persistence.Inspection{}, persistence.ErrSessionNotFound
	}
	return inspection, nil
}

// 下面这些方法本包一条路都走不到，一律拒绝——真被调到时测试会当场看见。

func (p *fakePersistence) Locate(sessionlog.SessionHeader) (persistence.Location, bool) {
	return persistence.Location{}, false
}

func (p *fakePersistence) SupportsRawArtifacts() bool { return false }

func (p *fakePersistence) ReadRaw(context.Context, sessionlog.SessionID) (persistence.RawArtifact, error) {
	return persistence.RawArtifact{}, errNoFakeMethod
}

func (p *fakePersistence) Create(context.Context, sessionlog.SessionHeader) error {
	return errNoFakeMethod
}

func (p *fakePersistence) Append(context.Context, sessionlog.SessionID, []sessionlog.Event) error {
	return errNoFakeMethod
}

func (p *fakePersistence) Load(context.Context, sessionlog.SessionID) (persistence.Inspection, error) {
	return persistence.Inspection{}, errNoFakeMethod
}

func (p *fakePersistence) ReadFrom(
	context.Context, sessionlog.SessionID, int,
) (persistence.StoredSuffix, error) {
	return persistence.StoredSuffix{}, errNoFakeMethod
}

func (p *fakePersistence) ListSnapshots(context.Context) ([]persistence.Snapshot, error) {
	return nil, errNoFakeMethod
}

// newListing 装出一次列举要用的那两样必填服务：登好两个单元的注册表，
// 加一张空的活会话表。
func newListing(t *testing.T) ListingServices {
	t.Helper()
	registry := projection.NewRegistry()
	dispose, err := RegisterProjections(registry)
	if err != nil {
		t.Fatalf("登记投影失败：%v", err)
	}
	t.Cleanup(dispose)
	sessions, err := coresession.NewStore(coresession.StoreOptions{Now: fixedClock()})
	if err != nil {
		t.Fatalf("造活会话表失败：%v", err)
	}
	return ListingServices{Projections: registry, Sessions: sessions}
}

// coldHeader 造一份冷候选的头。origin 为空串造的是一个普通会话（遍历节点）。
func coldHeader(
	id, parent sessionlog.SessionID,
	createdAt int64,
	origin sessionlog.Origin,
) sessionlog.SessionHeader {
	return sessionlog.SessionHeader{
		Version:       sessionlog.FormatVersion,
		ID:            id,
		CreatedAt:     createdAt,
		WorkspaceID:   testWorkspaceID,
		ParentSession: parent,
		Origin:        origin,
	}
}

// descriptorLog 造一段只有一条描述符的日志，名字由调用方给。
func descriptorLog(t *testing.T, seq int, label string) []sessionlog.Event {
	t.Helper()
	input := continuableInput()
	input.Label = label
	built := event(t, EventDescriptor, input)
	built.Seq = seq
	return []sessionlog.Event{built}
}

// liveChild 在活会话表里建一个子 agent 出身的孩子，seed 的序号在这里补。
func liveChild(
	t *testing.T,
	services ListingServices,
	owner *scope.Scope,
	id, parent sessionlog.SessionID,
	createdAt int64,
	seed []sessionlog.Event,
) *coresession.Session {
	t.Helper()
	seed = append([]sessionlog.Event(nil), seed...)
	for index := range seed {
		seed[index].Seq = index
	}
	live, err := services.Sessions.Create(t.Context(), owner, id, coresession.CreateOptions{
		Seed:          seed,
		WorkspaceID:   testWorkspaceID,
		ParentSession: parent,
		SeedLength:    len(seed),
		Origin:        sessionlog.OriginSubagent,
		CreatedAt:     createdAt,
	})
	if err != nil {
		t.Fatalf("建活孩子失败：%v", err)
	}
	return live
}

// cachedRecord 造一条绑在 bound 那段日志上的缓存记录，装着一份折好的身份。
func cachedRecord(
	t *testing.T,
	bound sessionlog.SessionHeader,
	label string,
	seq int,
) projectioncache.Record {
	t.Helper()
	state := identityState{Identity: &IdentityProjection{
		Mode: ModeContinuable, Label: label, Seq: seq,
	}}
	return projectioncache.Record{
		Identity: projectioncache.IdentityOf(bound),
		Rows: projection.Checkpoint{
			IdentityProjectionKey: {Ver: projectionStateVersion, Seq: seq, Val: data(t, state)},
		},
	}
}

// newCache 造一个**真的**耐久投影缓存，并把这些记录直接写进它那张表。
//
// 这一级不拿假的顶：身份见证和 seq 闸这两道门就是它存在的全部意义，
// 隔着一个假缓存断言不出它们。
func newCache(
	t *testing.T,
	registry *projection.Registry,
	rows map[sessionlog.SessionID]projectioncache.Record,
) *projectioncache.Cache {
	t.Helper()
	hub := storage.New()
	if _, err := hub.Backend.Register("main",
		storagetest.NewMemoryBackend(storagetest.NewMemoryMedium())); err != nil {
		t.Fatalf("注册存储后端失败：%v", err)
	}
	quiet := slog.New(slog.DiscardHandler)
	facility, err := domain.New(domain.Config{Storage: hub, Backend: "main", Logger: quiet})
	if err != nil {
		t.Fatalf("建域设施失败：%v", err)
	}
	opened, err := facility.Open(t.Context(), projectioncache.Spec())
	if err != nil {
		t.Fatalf("打开域失败：%v", err)
	}
	t.Cleanup(func() { _ = opened.Close(context.Background()) })

	table, err := domain.TableOf[projectioncache.Record](opened, projectioncache.TableName)
	if err != nil {
		t.Fatalf("取缓存表失败：%v", err)
	}
	for id, record := range rows {
		if err := table.Put(t.Context(), string(id), record); err != nil {
			t.Fatalf("写缓存行失败：%v", err)
		}
	}

	cache, err := projectioncache.New(opened, projectioncache.Options{
		Registry: registry,
		// 这条路只走 CachedSnapshot（零 I/O），下面这两样只是构造要件。
		Store:            newPersistence(),
		Flush:            func(projectioncache.LiveSession) error { return nil },
		WriteEveryEvents: 1,
		WriteInterval:    time.Second,
	})
	if err != nil {
		t.Fatalf("建投影缓存失败：%v", err)
	}
	t.Cleanup(cache.Close)
	return cache
}

// idsOf 把列举结果摊成 id 序列，好让次序断言只比字符串。
func idsOf(entries []ListEntry) []sessionlog.SessionID {
	ids := make([]sessionlog.SessionID, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	return ids
}

// onlyEntry 断言这次列举恰好给出一行，并交出它。
func onlyEntry(t *testing.T, entries []ListEntry) ListEntry {
	t.Helper()
	if len(entries) != 1 {
		t.Fatalf("该恰好列出一行，实际 %#v", entries)
	}
	return entries[0]
}

// ---- 服务与取消这两道门 ----

// 折叠能力缺席是确定性的部署配置错误，所以在**任何一次读之前**就报，
// 哪怕候选数为零。
func TestListChildrenRefusesWithoutTheProjectionRegistry(t *testing.T) {
	store := newPersistence()
	services := newListing(t)
	services.Projections, services.Persistence = nil, store

	_, err := ListChildren(t.Context(), services, "parent")
	if codeOf(err) != CodeControlProjectionsUnavailable {
		t.Fatalf("该报 %s，实际 %v", CodeControlProjectionsUnavailable, err)
	}
	if probes := store.probes(); len(probes) != 0 {
		t.Fatalf("查这道门之前不该读过任何东西，实际探视了 %#v", probes)
	}
}

func TestListChildrenRefusesWithoutTheLiveSessionStore(t *testing.T) {
	services := newListing(t)
	services.Sessions = nil

	_, err := ListDescendants(t.Context(), services, "root")
	if codeOf(err) != CodeControlSessionStoreUnavailable {
		t.Fatalf("该报 %s，实际 %v", CodeControlSessionStoreUnavailable, err)
	}
}

func TestListChildrenStopsWhenTheCallerAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := ListChildren(ctx, newListing(t), "parent"); codeOf(err) != CodeCancelled {
		t.Fatalf("该报 %s，实际 %v", CodeCancelled, err)
	}
}

// 后端自己那种失败原样穿出去——它不是本包的结局。
func TestListChildrenSurfacesABackendListFailure(t *testing.T) {
	broken := errors.New("列不出来")
	store := newPersistence()
	store.listErr = broken
	services := newListing(t)
	services.Persistence = store

	if _, err := ListChildren(t.Context(), services, "parent"); !errors.Is(err, broken) {
		t.Fatalf("后端那次失败该原样交回，实际 %v", err)
	}
}

// 后端见到取消之后可以用它自己那种失败回绝；取消在本包必须是一个稳定的结局。
func TestListChildrenPrefersCancellationOverABackendListFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	store := newPersistence()
	store.listErr = errors.New("列不出来")
	// 取消落在这次列举里头：入口那道门已经过了，回绝这次列举的只可能是后端那条
	// 失败路上的重认。
	store.onList = cancel
	services := newListing(t)
	services.Persistence = store

	if _, err := ListChildren(ctx, services, "parent"); codeOf(err) != CodeCancelled {
		t.Fatalf("该先认取消，实际 %v", err)
	}
}

// 一次成功的列举之后调用方走人，这次列举就到此为止——后面那些冷读都是白读。
func TestListChildrenStopsWhenTheCallerLeavesAfterASuccessfulList(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	store := newPersistence()
	store.put(coldHeader("child", "parent", 10, sessionlog.OriginSubagent), descriptorLog(t, 0, "冷的")...)
	store.onList = cancel
	services := newListing(t)
	services.Persistence = store

	if _, err := ListChildren(ctx, services, "parent"); codeOf(err) != CodeCancelled {
		t.Fatalf("该报 %s，实际 %v", CodeCancelled, err)
	}
	if probes := store.probes(); len(probes) != 0 {
		t.Fatalf("走人之后不该再探视，实际 %#v", probes)
	}
}

// ---- 语料、筛选与次序 ----

// 只有持久头上 Origin 是 subagent、而且父对得上的候选才会被解读。
func TestListChildrenOnlyReadsSubagentChildrenOfThisParent(t *testing.T) {
	store := newPersistence()
	store.put(coldHeader("mine", "parent", 10, sessionlog.OriginSubagent), descriptorLog(t, 0, "我的")...)
	store.put(coldHeader("someone-else", "别的父", 20, sessionlog.OriginSubagent),
		descriptorLog(t, 0, "别人的")...)
	store.put(coldHeader("plain", "parent", 30, ""), descriptorLog(t, 0, "普通会话")...)
	services := newListing(t)
	services.Persistence = store

	entries, err := ListChildren(t.Context(), services, "parent")
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	if entry := onlyEntry(t, entries); entry.ID != "mine" {
		t.Fatalf("只该列出这个父自己那个子 agent 孩子，实际 %#v", entries)
	}
}

// 按耐久的建会话时间排，同刻用 id 破平。
func TestListChildrenOrdersByCreationThenID(t *testing.T) {
	store := newPersistence()
	// 登记次序有意是乱的：这一行断言的是排序，不是插入顺序。
	store.put(coldHeader("b", "parent", 20, sessionlog.OriginSubagent), descriptorLog(t, 0, "b")...)
	store.put(coldHeader("a", "parent", 20, sessionlog.OriginSubagent), descriptorLog(t, 0, "a")...)
	store.put(coldHeader("early", "parent", 5, sessionlog.OriginSubagent), descriptorLog(t, 0, "早")...)
	services := newListing(t)
	services.Persistence = store

	entries, err := ListChildren(t.Context(), services, "parent")
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	ids := idsOf(entries)
	if len(ids) != 3 || ids[0] != "early" || ids[1] != "a" || ids[2] != "b" {
		t.Fatalf("该按建会话时间排、再用 id 破平，实际 %#v", ids)
	}
}

// 没装持久化时只看活的：一个冷孩子本来就接不上，它的缺席是能力缺席不是错误。
func TestListChildrenSeesOnlyLiveChildrenWithoutPersistence(t *testing.T) {
	services := newListing(t)
	liveChild(t, services, rootScope(t), "child", "parent", 10, descriptorLog(t, 0, "活的"))

	entries, err := ListChildren(t.Context(), services, "parent")
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	entry := onlyEntry(t, entries)
	if entry.Kind != EntryChild || entry.Label != "活的" || entry.Activity != ActivityRunning {
		t.Fatalf("该是一行活着的孩子，实际 %#v", entry)
	}
	if entry.Mode != ModeContinuable || entry.HasChildren {
		t.Fatalf("模式该来自描述符，而它自己没有孩子，实际 %#v", entry)
	}
}

// 活优先的合并，不做任何头的调和：一条活记录整条拿下它那个 id。
func TestListChildrenPrefersTheLiveRecordOverThePersistedOne(t *testing.T) {
	services := newListing(t)
	store := newPersistence()
	store.put(coldHeader("child", "parent", 10, sessionlog.OriginSubagent),
		descriptorLog(t, 0, "存档里那个名字")...)
	services.Persistence = store
	liveChild(t, services, rootScope(t), "child", "parent", 10, descriptorLog(t, 0, "活着那个名字"))

	entries, err := ListChildren(t.Context(), services, "parent")
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	entry := onlyEntry(t, entries)
	if entry.Label != "活着那个名字" || entry.Activity != ActivityRunning {
		t.Fatalf("活的那份该整条赢下这个 id，实际 %#v", entry)
	}
	if probes := store.probes(); len(probes) != 0 {
		t.Fatalf("活孩子零日志读地解出来，不该探视存档，实际 %#v", probes)
	}
}

// 一个正在跑、又还没有身份的候选整行略过：它的描述符可能还没追加（创建窗口）。
func TestListChildrenSkipsALiveChildInTheCreationWindow(t *testing.T) {
	services := newListing(t)
	owner := rootScope(t)
	liveChild(t, services, owner, "still-being-made", "parent", 10, nil)
	liveChild(t, services, owner, "settled", "parent", 20, descriptorLog(t, 0, "立住了"))

	entries, err := ListChildren(t.Context(), services, "parent")
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	if entry := onlyEntry(t, entries); entry.ID != "settled" {
		t.Fatalf("创建窗口里那个该整行略过，实际 %#v", entries)
	}
}

func TestListChildrenMarksAParentThatItselfHasSubagentChildren(t *testing.T) {
	store := newPersistence()
	store.put(coldHeader("child", "parent", 10, sessionlog.OriginSubagent), descriptorLog(t, 0, "孩子")...)
	store.put(coldHeader("grandchild", "child", 20, sessionlog.OriginSubagent),
		descriptorLog(t, 0, "孙子")...)
	services := newListing(t)
	services.Persistence = store

	entries, err := ListChildren(t.Context(), services, "parent")
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	if entry := onlyEntry(t, entries); !entry.HasChildren {
		t.Fatalf("这个孩子自己底下有子 agent，该标出来，实际 %#v", entry)
	}
}

// ---- 冷孩子那三级梯子 ----

func TestListChildrenRefoldsAColdChildFromPersistence(t *testing.T) {
	store := newPersistence()
	store.put(coldHeader("child", "parent", 10, sessionlog.OriginSubagent), descriptorLog(t, 0, "冷的")...)
	services := newListing(t)
	services.Persistence = store

	entries, err := ListChildren(t.Context(), services, "parent")
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	entry := onlyEntry(t, entries)
	if entry.Kind != EntryChild || entry.Label != "冷的" || entry.Activity != ActivityInactive {
		t.Fatalf("该是一行安定下来的孩子，实际 %#v", entry)
	}
}

// 一次失败的探视是一条**暂时的** unavailable 行，而这次列举本身照样成功。
func TestListChildrenReportsAnUnavailableChildWhenTheProbeFails(t *testing.T) {
	store := newPersistence()
	store.put(coldHeader("child", "parent", 10, sessionlog.OriginSubagent), descriptorLog(t, 0, "冷的")...)
	store.inspectErr["child"] = errors.New("读不动")
	services := newListing(t)
	services.Persistence = store

	entries, err := ListChildren(t.Context(), services, "parent")
	if err != nil {
		t.Fatalf("逐孩子兜住之后这次列举该成功，实际 %v", err)
	}
	entry := onlyEntry(t, entries)
	if entry.Kind != EntryDiagnostic || entry.Reason != DiagnosticUnavailable {
		t.Fatalf("该是一条 unavailable 诊断，实际 %#v", entry)
	}
}

// 一个会话 id 点的是一个**槽位**不是一段生命：被删掉又被另一个主人重新发布的
// 孩子，绝不许漏进老父亲那份清单。
func TestListChildrenReportsACorruptChildWhenTheProbePointsAtAnotherLifecycle(t *testing.T) {
	listed := coldHeader("child", "parent", 10, sessionlog.OriginSubagent)
	reborn := listed
	reborn.CreatedAt = 999
	store := newPersistence()
	store.putStored(listed, reborn, descriptorLog(t, 0, "另一段生命")...)
	services := newListing(t)
	services.Persistence = store

	entries, err := ListChildren(t.Context(), services, "parent")
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	entry := onlyEntry(t, entries)
	if entry.Kind != EntryDiagnostic || entry.Reason != DiagnosticCorrupt {
		t.Fatalf("该是一条 corrupt 诊断，实际 %#v", entry)
	}
}

// Origin 和 AgentPreset 也算生命见证：上游的 LIFECYCLE_WITNESS_KEYS 把这两项一并
// 收进去了，而那一组就是「同一个 id 底下区分两次生命」的全部判据。探视回来的头
// 在这两项上对不上，说明它不是当初列出来的那一段，该报 corrupt。
func TestListChildrenTreatsOriginAndPresetAsLifecycleWitnesses(t *testing.T) {
	listed := coldHeader("child", "parent", 10, sessionlog.OriginSubagent)
	listed.AgentPreset = "列出来时是这个"
	stored := listed
	stored.Origin = ""
	stored.AgentPreset = "存档里是另一个"
	store := newPersistence()
	store.putStored(listed, stored, descriptorLog(t, 0, "对不上的那一段")...)
	services := newListing(t)
	services.Persistence = store

	entries, err := ListChildren(t.Context(), services, "parent")
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	entry := onlyEntry(t, entries)
	if entry.Kind != EntryDiagnostic || entry.Reason != DiagnosticCorrupt {
		t.Fatalf("该是一条 corrupt 诊断，实际 %#v", entry)
	}
}

// 一份已经安定下来、却折不出身份的日志是终局，所以报 corrupt——「缺、坏、
// 版本不认识」在这里有意不加区分。
func TestListChildrenReportsACorruptChildWhenNoIdentityFolds(t *testing.T) {
	store := newPersistence()
	for name, log := range map[string][]sessionlog.Event{
		"一条描述符都没有": nil,
		"描述符坏了": {{
			Seq:  0,
			Type: EventDescriptor,
			Data: []byte(`{"version":`),
		}},
	} {
		store.put(coldHeader(sessionlog.SessionID(name), "parent", 10, sessionlog.OriginSubagent), log...)
	}
	services := newListing(t)
	services.Persistence = store

	entries, err := ListChildren(t.Context(), services, "parent")
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("两个候选都该有一行，实际 %#v", entries)
	}
	for _, entry := range entries {
		if entry.Kind != EntryDiagnostic || entry.Reason != DiagnosticCorrupt {
			t.Fatalf("%q 该是一条 corrupt 诊断，实际 %#v", entry.ID, entry)
		}
	}
}

// 宿主自己登的某个投影单元把这次重折搞崩了：这个孩子降级成一条 corrupt 诊断，
// 而整次列举照旧成功——逐孩子隔离对「读不动」和「折不出来」一视同仁。
func TestListChildrenReportsACorruptChildWhenTheRefoldFails(t *testing.T) {
	services := newListing(t)
	// 一份**排不出 JSON** 的状态：折是折得完，写回检查点那一行时崩掉。
	dispose, err := projection.Register(services.Projections, projection.Definition[chan int]{
		Key:         "排不出去的",
		Init:        func() chan int { return make(chan int) },
		Apply:       func(state chan int, _ sessionlog.Event) (chan int, bool) { return state, false },
		DecodeState: func(json.RawMessage) (chan int, error) { return nil, errors.New("读不回来") },
	})
	if err != nil {
		t.Fatalf("登记投影失败：%v", err)
	}
	t.Cleanup(dispose)

	store := newPersistence()
	store.put(coldHeader("child", "parent", 10, sessionlog.OriginSubagent), descriptorLog(t, 0, "冷的")...)
	services.Persistence = store

	entries, err := ListChildren(t.Context(), services, "parent")
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	if entry := onlyEntry(t, entries); entry.Kind != EntryDiagnostic || entry.Reason != DiagnosticCorrupt {
		t.Fatalf("该降级成一条 corrupt 诊断，实际 %#v", entry)
	}
}

// 缓存那一级过了 seq 闸就是终局：那次权威探视根本不发生。
func TestListChildrenTakesTheCachedIdentityWhenItPassesTheSeqGate(t *testing.T) {
	header := coldHeader("child", "parent", 10, sessionlog.OriginSubagent)
	header.SeedLength = 3
	store := newPersistence()
	store.put(header, descriptorLog(t, 3, "重折出来的")...)
	services := newListing(t)
	services.Persistence = store
	services.Cache = newCache(t, services.Projections, map[sessionlog.SessionID]projectioncache.Record{
		"child": cachedRecord(t, header, "缓存里那个", 3),
	})

	entries, err := ListChildren(t.Context(), services, "parent")
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	entry := onlyEntry(t, entries)
	if entry.Label != "缓存里那个" || entry.Activity != ActivityInactive {
		t.Fatalf("该采信那行缓存，实际 %#v", entry)
	}
	if probes := store.probes(); len(probes) != 0 {
		t.Fatalf("过了闸就不该再探视，实际 %#v", probes)
	}
}

// 一份创建窗口里的检查点可能带的是分叉种子里被回放的**祖先**描述符
// （seq 低于 SeedLength），那种东西不许压过重折。
func TestListChildrenRefoldsWhenTheCachedIdentityCameFromTheSeed(t *testing.T) {
	header := coldHeader("child", "parent", 10, sessionlog.OriginSubagent)
	header.SeedLength = 3
	store := newPersistence()
	store.put(header, descriptorLog(t, 3, "孩子自己那条")...)
	services := newListing(t)
	services.Persistence = store
	services.Cache = newCache(t, services.Projections, map[sessionlog.SessionID]projectioncache.Record{
		"child": cachedRecord(t, header, "祖先那条", 2),
	})

	entries, err := ListChildren(t.Context(), services, "parent")
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	if entry := onlyEntry(t, entries); entry.Label != "孩子自己那条" {
		t.Fatalf("闸没过该落到重折那一级，实际 %#v", entry)
	}
	if probes := store.probes(); len(probes) != 1 {
		t.Fatalf("该恰好探视一次，实际 %#v", probes)
	}
}

// 一条绑着另一段日志的记录（同 id 重建、存储被换掉）在缓存自己那一层就被整条挡掉。
func TestListChildrenRefoldsWhenTheCacheRowBelongsToAnotherLog(t *testing.T) {
	header := coldHeader("child", "parent", 10, sessionlog.OriginSubagent)
	elsewhere := header
	elsewhere.CreatedAt = 999
	store := newPersistence()
	store.put(header, descriptorLog(t, 0, "这段日志自己的")...)
	services := newListing(t)
	services.Persistence = store
	services.Cache = newCache(t, services.Projections, map[sessionlog.SessionID]projectioncache.Record{
		"child": cachedRecord(t, elsewhere, "别的日志的", 0),
	})

	entries, err := ListChildren(t.Context(), services, "parent")
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	if entry := onlyEntry(t, entries); entry.Label != "这段日志自己的" {
		t.Fatalf("身份对不上的那行该被整条挡掉，实际 %#v", entry)
	}
}

// 取消在冷读那条路上仍旧是整次列举的结局，而不是降级成一行诊断。
func TestListChildrenReportsCancellationFromAColdProbe(t *testing.T) {
	for name, probeFails := range map[string]bool{"探视成功": false, "探视失败": true} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			store := newPersistence()
			store.put(coldHeader("child", "parent", 10, sessionlog.OriginSubagent),
				descriptorLog(t, 0, "冷的")...)
			if probeFails {
				store.inspectErr["child"] = errors.New("读不动")
			}
			// 取消落在探视里：语料已经建好了，所以停这次列举的只可能是冷读那条路。
			store.onInspect = func(sessionlog.SessionID) { cancel() }
			services := newListing(t)
			services.Persistence = store

			if _, err := ListChildren(ctx, services, "parent"); codeOf(err) != CodeCancelled {
				t.Fatalf("该报 %s，实际 %v", CodeCancelled, err)
			}
		})
	}
}

// 一次候选全是活孩子的解算根本不起冷读，那道并发栅栏于是整个跳过；收尾那一下
// 才是这条路上唯一认得出调用方已经走了的地方。
func TestResolveCandidateRowsHonoursCancellationWithoutAnyColdRead(t *testing.T) {
	services := newListing(t)
	live := liveChild(t, services, rootScope(t), "child", "parent", 10, descriptorLog(t, 0, "活的"))
	prepared, err := prepareListing(t.Context(), services)
	if err != nil {
		t.Fatalf("备列举失败：%v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	rows, err := prepared.resolveCandidateRows(ctx, []corpusRecord{
		{header: live.Header(), live: live},
	})
	if codeOf(err) != CodeCancelled {
		t.Fatalf("该报 %s，实际 %v", CodeCancelled, err)
	}
	if rows != nil {
		t.Fatalf("取消了就不该交回半张表，实际 %#v", rows)
	}
}

// ---- 整棵树 ----

// 普通会话照样是遍历节点，所以挂在它们底下的孩子仍旧找得到。
func TestListDescendantsWalksThePreOrderWithDepths(t *testing.T) {
	store := newPersistence()
	store.put(coldHeader("a", "root", 10, sessionlog.OriginSubagent), descriptorLog(t, 0, "a")...)
	store.put(coldHeader("a1", "a", 20, sessionlog.OriginSubagent), descriptorLog(t, 0, "a1")...)
	store.put(coldHeader("plain", "root", 30, ""), descriptorLog(t, 0, "普通")...)
	store.put(coldHeader("under-plain", "plain", 40, sessionlog.OriginSubagent),
		descriptorLog(t, 0, "藏在普通会话底下")...)
	store.put(coldHeader("elsewhere", "别的根", 50, sessionlog.OriginSubagent),
		descriptorLog(t, 0, "别处")...)
	services := newListing(t)
	services.Persistence = store

	entries, err := ListDescendants(t.Context(), services, "root")
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("该列出三行，实际 %#v", entries)
	}
	want := []struct {
		id     sessionlog.SessionID
		parent sessionlog.SessionID
		depth  int
	}{
		{"a", "root", 1},
		{"a1", "a", 2},
		{"under-plain", "plain", 2},
	}
	for index, expected := range want {
		got := entries[index]
		if got.ID != expected.id || got.ParentID != expected.parent || got.Depth != expected.depth {
			t.Fatalf("第 %d 行该是 %+v，实际 %#v", index, expected, got)
		}
	}
	// 那个普通会话自己不是子 agent，所以它只当路走，不上榜。
	for _, entry := range entries {
		if entry.ID == "plain" {
			t.Fatal("普通会话不该被列出来")
		}
	}
}

// 一个把自己当父的会话不许把遍历卡死。
func TestListDescendantsGuardsAgainstACycle(t *testing.T) {
	store := newPersistence()
	store.put(coldHeader("root", "root", 10, sessionlog.OriginSubagent), descriptorLog(t, 0, "自己指自己")...)
	store.put(coldHeader("child", "root", 20, sessionlog.OriginSubagent), descriptorLog(t, 0, "孩子")...)
	services := newListing(t)
	services.Persistence = store

	entries, err := ListDescendants(t.Context(), services, "root")
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	if len(entries) != 1 || entries[0].ID != "child" {
		t.Fatalf("那个根自己已经走过了，只该列出它的孩子，实际 %#v", entries)
	}
}

// 创建窗口那一行在整棵树这一侧同样是整行略过，位置不许跟着错位。
func TestListDescendantsSkipsTheCreationWindowWithoutShiftingPositions(t *testing.T) {
	services := newListing(t)
	owner := rootScope(t)
	liveChild(t, services, owner, "still-being-made", "root", 10, nil)
	liveChild(t, services, owner, "settled", "root", 20, descriptorLog(t, 0, "立住了"))

	entries, err := ListDescendants(t.Context(), services, "root")
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("该只列出立住了那一个，实际 %#v", entries)
	}
	if entries[0].ID != "settled" || entries[0].Depth != 1 || entries[0].ParentID != "root" {
		t.Fatalf("剩下那一行的位置该原样对上，实际 %#v", entries[0])
	}
}

// 冷读里冒出来的取消要一路穿过整棵树这个入口：整次列举失败，而不是把没读到的
// 那些孩子降级成诊断行。
//
// 候选数有意超过 coldReadConcurrency：并发上限之外那几个候选一定是在前面某次探视
// 已经收工之后才起跑的，于是它们必然撞上探视之前那道重认，那条路才走得到。
func TestListDescendantsReportsCancellationRaisedBeyondTheColdReadLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	store := newPersistence()
	for index := range coldReadConcurrency + 2 {
		id := sessionlog.SessionID(fmt.Sprintf("child-%d", index))
		store.put(coldHeader(id, "root", int64(10+index), sessionlog.OriginSubagent),
			descriptorLog(t, 0, "冷的")...)
	}
	store.onInspect = func(sessionlog.SessionID) { cancel() }
	services := newListing(t)
	services.Persistence = store

	if _, err := ListDescendants(ctx, services, "root"); codeOf(err) != CodeCancelled {
		t.Fatalf("该报 %s，实际 %v", CodeCancelled, err)
	}
	if probes := store.probes(); len(probes) > coldReadConcurrency {
		t.Fatalf("上限之外那几个不该再探视，实际 %#v", probes)
	}
}
