// 本文件的作用：域声明那一层的契约——三条校验函数各自拒什么、初始状态长什么样、
// 以及 [Spec] 交出来的那份声明装到域上之后确实在守着这些规矩。
//
// 这里的用例全部不碰登记册：校验函数是介质的守门人，它们必须在没有 [Registry]
// 的场合下也说得清自己拒什么。

package workspace

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/storage/domain"
)

// goodRecord 造一条各字段都填齐的记录，给「只改一处看它拒不拒」的用例当底稿。
func goodRecord() Record {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return Record{
		TargetKey:   "key:/a",
		DisplayPath: "/a",
		Title:       "甲",
		SessionIDs:  []sessionlog.SessionID{"s1"},
		CreatedAt:   at,
		UpdatedAt:   at,
	}
}

func TestRecordValidate按能不能用划界(t *testing.T) {
	if err := goodRecord().Validate(); err != nil {
		t.Fatalf("填齐的记录不该被拒：%v", err)
	}

	// 空标题是收的：DSH 那边 z.string() 就收空串，一个没标题的工作区照样能用。
	empty := goodRecord()
	empty.Title = ""
	if err := empty.Validate(); err != nil {
		t.Fatalf("空标题该收下：%v", err)
	}
	// 空账目也是收的：一个刚建出来还没挂过会话的工作区就是这样。
	noSessions := goodRecord()
	noSessions.SessionIDs = nil
	if err := noSessions.Validate(); err != nil {
		t.Fatalf("空账目该收下：%v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Record)
		want   string
	}{
		{"没有目标标识", func(r *Record) { r.TargetKey = "" }, "目标标识"},
		{"没有展示路径", func(r *Record) { r.DisplayPath = "" }, "展示路径"},
		{"创建时刻是零时刻", func(r *Record) { r.CreatedAt = time.Time{} }, "创建时刻"},
		{"更新时刻是零时刻", func(r *Record) { r.UpdatedAt = time.Time{} }, "更新时刻"},
		{"账目里混进空会话id", func(r *Record) {
			r.SessionIDs = []sessionlog.SessionID{"s1", ""}
		}, "第 1 项"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			record := goodRecord()
			one.mutate(&record)
			err := record.Validate()
			if err == nil {
				t.Fatal("该被拒才对")
			}
			if !strings.Contains(err.Error(), one.want) {
				t.Fatalf("这句话该点出 %q，拿到 %v", one.want, err)
			}
		})
	}
}

func TestPendingMutationValidate只认两种操作(t *testing.T) {
	for _, operation := range []Operation{OperationCreate, OperationDelete} {
		marker := PendingMutation{Operation: operation, WorkspaceID: "ws-1"}
		if err := marker.Validate(); err != nil {
			t.Fatalf("%q 该收下：%v", operation, err)
		}
	}

	cases := []struct {
		name   string
		marker PendingMutation
		want   string
	}{
		{"操作是空的", PendingMutation{WorkspaceID: "ws-1"}, "create/delete"},
		{"操作不认识", PendingMutation{Operation: "rename", WorkspaceID: "ws-1"}, "create/delete"},
		{"没说是哪个工作区", PendingMutation{Operation: OperationCreate}, "哪个工作区"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			err := one.marker.Validate()
			if err == nil {
				t.Fatal("该被拒才对")
			}
			if !strings.Contains(err.Error(), one.want) {
				t.Fatalf("这句话该点出 %q，拿到 %v", one.want, err)
			}
		})
	}
}

func TestDomainStateValidate只查这一份值自己(t *testing.T) {
	if err := initialDomainState().Validate(); err != nil {
		t.Fatalf("初始状态不该被自己的校验拒掉：%v", err)
	}

	// 次序里点名一个表里没有的工作区，这一层是**收**的：跨表的检查在
	// [Registry.validateStoredState] 那边，域这一层拿不到那张表。
	crossTable := DomainState{WorkspaceIDs: []WorkspaceID{"根本不存在"}}
	if err := crossTable.Validate(); err != nil {
		t.Fatalf("跨表的事不归这一层管：%v", err)
	}

	full := DomainState{
		Initialized:        true,
		WorkspaceIDs:       []WorkspaceID{"ws-1"},
		ArchivedSessionIDs: []sessionlog.SessionID{"s1"},
		PendingMutation:    &PendingMutation{Operation: OperationDelete, WorkspaceID: "ws-1"},
	}
	if err := full.Validate(); err != nil {
		t.Fatalf("填齐的状态不该被拒：%v", err)
	}

	cases := []struct {
		name  string
		state DomainState
		want  string
	}{
		{"次序里混进空工作区id", DomainState{WorkspaceIDs: []WorkspaceID{"ws-1", ""}}, "次序第 1 项"},
		{"归档集合里混进空会话id", DomainState{ArchivedSessionIDs: []sessionlog.SessionID{""}}, "归档集合第 0 项"},
		// 待恢复标记自己不合法时，这一层直接把那条错误透出来。
		{"待恢复标记不合法", DomainState{PendingMutation: &PendingMutation{Operation: OperationCreate}}, "哪个工作区"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			err := one.state.Validate()
			if err == nil {
				t.Fatal("该被拒才对")
			}
			if !strings.Contains(err.Error(), one.want) {
				t.Fatalf("这句话该点出 %q，拿到 %v", one.want, err)
			}
		})
	}
}

func TestInitialDomainState每次都是新的一份(t *testing.T) {
	first := initialDomainState()
	if first.Initialized {
		t.Fatal("第一次打开还欠一次历史 bootstrap，这一位必须是假")
	}
	if first.PendingMutation != nil {
		t.Fatal("初始状态不该悬着一次写")
	}

	// 这就是 [Spec] 要做成函数而不是包级变量的理由：改到第一份不许影响第二份。
	first.WorkspaceIDs = append(first.WorkspaceIDs, "ws-1")
	first.ArchivedSessionIDs = append(first.ArchivedSessionIDs, "s1")
	second := initialDomainState()
	if len(second.WorkspaceIDs) != 0 || len(second.ArchivedSessionIDs) != 0 {
		t.Fatalf("两份初始状态共用了底层数组：%+v", second)
	}
}

func TestSpec交出的声明就是这个域的身份(t *testing.T) {
	spec := Spec()
	if spec.Name != DomainName {
		t.Fatalf("域名该是 %q，拿到 %q", DomainName, spec.Name)
	}
	if spec.Version != DomainVersion {
		t.Fatalf("版本该是 %d，拿到 %d", DomainVersion, spec.Version)
	}
	if len(spec.Tables) != 1 {
		t.Fatalf("这个域只有一张表，拿到 %d 张", len(spec.Tables))
	}
	if got := spec.Tables[0].Name(); got != TableName {
		t.Fatalf("表名该是 %q，拿到 %q", TableName, got)
	}
}

func TestSpec装到域上之后校验函数确实在守着(t *testing.T) {
	ctx := t.Context()
	h := newHarness(t)
	// 不经过 [Open]：这条用例要的是「这份声明本身」，不是登记册。
	dom, err := h.facility.Open(ctx, Spec())
	if err != nil {
		t.Fatalf("打开域不该失败：%v", err)
	}
	table, err := domain.TableOf[Record](dom, TableName)
	if err != nil {
		t.Fatalf("取表不该失败：%v", err)
	}
	global, err := domain.GlobalOf[DomainState](dom)
	if err != nil {
		t.Fatalf("取全局槽不该失败：%v", err)
	}

	// 全局槽的初值就是 initialDomainState。
	state, err := global.Get(ctx)
	if err != nil {
		t.Fatalf("读全局槽不该失败：%v", err)
	}
	if state.Initialized {
		t.Fatal("一份空介质上的初值该是「还没 bootstrap」")
	}

	// 记录的校验函数接在表上：一条缺目标标识的记录写不进去。
	bad := goodRecord()
	bad.TargetKey = ""
	if err := table.Put(ctx, "ws-1", bad); err == nil {
		t.Fatal("不合法的记录该被表拒掉")
	}
	if err := table.Put(ctx, "ws-1", goodRecord()); err != nil {
		t.Fatalf("合法的记录该写得进去：%v", err)
	}

	// 全局状态的校验函数接在全局槽上。
	if err := global.Set(ctx, DomainState{WorkspaceIDs: []WorkspaceID{""}}); err == nil {
		t.Fatal("不合法的全局状态该被拒掉")
	}
}

// -- 错误值 ----------------------------------------------------------------

func TestError两条链是分开的(t *testing.T) {
	plain := fail(CodeNotStarted, "还没启动")
	if !errors.Is(plain, CodeNotStarted) {
		t.Fatal("errors.Is 该认得这次失败的分类")
	}
	if errors.Is(plain, CodeStorageFailed) {
		t.Fatal("不该认成别的分类")
	}
	if errors.Unwrap(plain) != nil {
		t.Fatal("本包自己判出来的失败底下不该挂着原因")
	}
	// 没有原因时那句话到分类和描述为止。
	if got := plain.Error(); got != string(CodeNotStarted)+"：还没启动" {
		t.Fatalf("拿到 %q", got)
	}

	wrapped := wrap(CodeStorageFailed, errBackend, "写不下去 %d 次", 2)
	if !errors.Is(wrapped, CodeStorageFailed) {
		t.Fatal("errors.Is 该认得分类")
	}
	if !errors.Is(wrapped, errBackend) {
		t.Fatal("errors.Is 该顺着 Cause 找到底层那条")
	}
	if got := wrapped.Error(); !strings.Contains(got, "写不下去 2 次") || !strings.Contains(got, errBackend.Error()) {
		t.Fatalf("描述和原因都该在那句话里，拿到 %q", got)
	}

	// 一个不是 Code 的目标问过来时，Is 要老老实实说不认识。
	if errors.Is(wrapped, errors.New("另一条")) {
		t.Fatal("不该认下一条无关的错误")
	}
}
