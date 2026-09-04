// 本文件的作用：分叉那一半的测试——五种拒绝各自怎么触发，边界怎么算，以及分出来
// 的子会话身上带了父会话的哪些东西。

package session

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/sessionlog"
)

// forkCode 取出一次分叉拒绝的分类，不是分叉错误就当场失败。
func forkCode(t *testing.T, err error) ForkErrorCode {
	t.Helper()
	var rejected *ForkError
	if !errors.As(err, &rejected) {
		t.Fatalf("这不是一条分叉拒绝：%v", err)
	}
	return rejected.Code
}

// boundaryOf 把一个字面量借成指针，好往 boundary 参数上填。
func boundaryOf(value int) *int { return &value }

func TestForkRefusesASourceItCannotIdentify(t *testing.T) {
	store := newStore(t)
	owner := rootScope(t)
	ctx := context.Background()

	if _, err := store.Fork(ctx, owner, nil, nil, ""); forkCode(t, err) != ForkSessionNotFound {
		t.Fatalf("诊断是 %v", err)
	}
	if _, err := store.ForkByID(ctx, owner, "nope", nil, ""); forkCode(t, err) != ForkSessionNotFound {
		t.Fatalf("诊断是 %v", err)
	}
	// 一个 prepare 出来但没登记的会话：名字在存储里查不到。
	loose, err := store.Prepare("loose", CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Fork(ctx, owner, loose, nil, ""); forkCode(t, err) != ForkSessionNotFound {
		t.Fatalf("诊断是 %v", err)
	}
}

func TestForkRefusesAStaleObjectWhoseNameIsLive(t *testing.T) {
	// 名字在，但活着的是**另一个**对象——只有按对象分叉这条路说得出这句话。
	store := newStore(t)
	owner := rootScope(t)
	ctx := context.Background()

	liveSession(t, store, owner, "a", CreateOptions{})
	other := newStore(t)
	shadow, err := other.Prepare("a", CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Fork(ctx, owner, shadow, nil, "")
	if forkCode(t, err) != ForkSessionNotLive {
		t.Fatalf("诊断是 %v", err)
	}
	if !strings.Contains(err.Error(), "is not the live store instance") {
		t.Fatalf("诊断文字是 %q", err.Error())
	}
}

func TestForkRefusesATakenChildName(t *testing.T) {
	store := newStore(t)
	owner := rootScope(t)
	ctx := context.Background()

	source := liveSession(t, store, owner, "a", CreateOptions{})
	liveSession(t, store, owner, "b", CreateOptions{})

	_, err := store.Fork(ctx, owner, source, nil, "b")
	if forkCode(t, err) != ForkSessionAlreadyExists {
		t.Fatalf("诊断是 %v", err)
	}
}

func TestForkingAnEmptySourceGivesAnEmptyChildThatStillGetsTheMarker(t *testing.T) {
	store := newStore(t)
	owner := rootScope(t)
	ctx := context.Background()

	source := liveSession(t, store, owner, "a", CreateOptions{})
	child, err := store.Fork(ctx, owner, source, nil, "child")
	if err != nil {
		t.Fatal(err)
	}
	events := child.Events()
	if len(events) != 1 || events[0].Type != sessionlog.EventSessionEndSeed {
		t.Fatalf("子会话的日志是 %#v", events)
	}
	if child.Header().SeedLength != 0 {
		t.Fatalf("血统边界是 %d", child.Header().SeedLength)
	}
}

func TestForkCopiesThePrefixAndTheLineage(t *testing.T) {
	store := newStore(t)
	owner := rootScope(t)
	ctx := context.Background()

	source := liveSession(t, store, owner, "a", CreateOptions{WorkspaceID: testWorkspaceID})
	for _, event := range []sessionlog.Event{
		turnStart(1), userEvent(t, "你好"), assistantEvent(t, 1, 1, "在"), turnEnd(1),
	} {
		if _, err := source.Append(event); err != nil {
			t.Fatal(err)
		}
	}

	// 不给边界就取到最后一条。
	child, err := store.Fork(ctx, owner, source, nil, "child")
	if err != nil {
		t.Fatal(err)
	}
	// 四条继承来的加一条 end-seed 标记。
	if events := child.Events(); len(events) != 5 || events[4].Type != sessionlog.EventSessionEndSeed {
		t.Fatalf("子会话的日志是 %#v", events)
	}
	header := child.Header()
	if header.ParentSession != "a" || header.SeedLength != 4 || header.WorkspaceID != testWorkspaceID {
		t.Fatalf("子会话的头是 %#v", header)
	}
	if child.FirstLiveSeq() != 4 {
		t.Fatalf("firstLiveSeq 是 %d", child.FirstLiveSeq())
	}
	// 派生历史跟着前缀走。
	messages, err := child.DeriveMessages()
	if err != nil || len(messages) != 2 {
		t.Fatalf("派生历史是 %#v err=%v", messages, err)
	}

	// 再走一个回合，好让边界有地方落。
	for _, event := range []sessionlog.Event{turnStart(2), userEvent(t, "再问一句"), turnEnd(2)} {
		if _, err := source.Append(event); err != nil {
			t.Fatal(err)
		}
	}

	// 给了边界就停在那一条上（含）。按名字分叉走的是同一段。
	trimmed, err := store.ForkByID(ctx, owner, "a", boundaryOf(3), "trimmed")
	if err != nil {
		t.Fatal(err)
	}
	if events := trimmed.Events(); len(events) != 5 {
		t.Fatalf("截到 seq=3 的日志是 %#v", events)
	}
	if trimmed.Header().SeedLength != 4 {
		t.Fatalf("血统边界是 %d", trimmed.Header().SeedLength)
	}

	// 不给名字就铸一个。
	minted, err := store.Fork(ctx, owner, source, boundaryOf(6), "")
	if err != nil {
		t.Fatal(err)
	}
	if minted.ID() == "" {
		t.Fatal("没铸出标识")
	}
	if _, live := store.Get(minted.ID()); !live {
		t.Fatal("铸出来的子会话没登记进存储")
	}
}

func TestForkRejectsABoundaryThatIsNotThere(t *testing.T) {
	store := newStore(t)
	owner := rootScope(t)
	ctx := context.Background()

	empty := liveSession(t, store, owner, "empty", CreateOptions{})
	_, err := store.Fork(ctx, owner, empty, boundaryOf(0), "")
	if forkCode(t, err) != ForkInvalidBoundary {
		t.Fatalf("诊断是 %v", err)
	}
	// 一条都没有的时候那句话里报的是 "none"。
	if !strings.Contains(err.Error(), "last seq: none") {
		t.Fatalf("诊断文字是 %q", err.Error())
	}

	source := liveSession(t, store, owner, "a", CreateOptions{})
	if _, err := source.Append(userEvent(t, "你好")); err != nil {
		t.Fatal(err)
	}
	_, err = store.Fork(ctx, owner, source, boundaryOf(9), "")
	if forkCode(t, err) != ForkInvalidBoundary {
		t.Fatalf("诊断是 %v", err)
	}
	if !strings.Contains(err.Error(), "last seq: 0") {
		t.Fatalf("诊断文字是 %q", err.Error())
	}

	_, err = store.Fork(ctx, owner, source, boundaryOf(-1), "")
	if forkCode(t, err) != ForkInvalidBoundary {
		t.Fatalf("诊断是 %v", err)
	}
	if !strings.Contains(err.Error(), "non-negative safe integer") {
		t.Fatalf("诊断文字是 %q", err.Error())
	}
}

func TestForkRejectsABoundaryInsideAnOpenTurn(t *testing.T) {
	store := newStore(t)
	owner := rootScope(t)
	ctx := context.Background()

	source := liveSession(t, store, owner, "a", CreateOptions{})
	for _, event := range []sessionlog.Event{turnStart(7), userEvent(t, "你好")} {
		if _, err := source.Append(event); err != nil {
			t.Fatal(err)
		}
	}
	_, err := store.Fork(ctx, owner, source, nil, "")
	if forkCode(t, err) != ForkOpenTurn {
		t.Fatalf("诊断是 %v", err)
	}
	if !strings.Contains(err.Error(), "open turn 7") {
		t.Fatalf("诊断文字是 %q", err.Error())
	}

	// 回合关掉之后同一条边界就放行了。
	if _, err := source.Append(turnEnd(7)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Fork(ctx, owner, source, nil, "child"); err != nil {
		t.Fatal(err)
	}

	// 再开一个回合，边界落在开着的这一段里又被拒——往回找只看最近那条边界事件。
	if _, err := source.Append(turnStart(8)); err != nil {
		t.Fatal(err)
	}
	_, err = store.Fork(ctx, owner, source, nil, "")
	if forkCode(t, err) != ForkOpenTurn {
		t.Fatalf("诊断是 %v", err)
	}
}

func TestForkRejectsAnOpenTurnWhosePayloadIsUnreadable(t *testing.T) {
	// 读不出回合号只是让诊断少一个数字，不改变「不许分」这件事。
	store := newStore(t)
	owner := rootScope(t)
	ctx := context.Background()

	source := liveSession(t, store, owner, "a", CreateOptions{})
	if _, err := source.Append(sessionlog.Event{
		Type: sessionlog.EventTurnStart, Data: json.RawMessage(`[]`),
	}); err != nil {
		t.Fatal(err)
	}
	_, err := store.Fork(ctx, owner, source, nil, "")
	if forkCode(t, err) != ForkOpenTurn {
		t.Fatalf("诊断是 %v", err)
	}
	if !strings.Contains(err.Error(), "payload is unreadable") {
		t.Fatalf("诊断文字是 %q", err.Error())
	}
}

func TestAForkedChildIsIndependentOfItsSource(t *testing.T) {
	store := newStore(t)
	owner := rootScope(t)
	ctx := context.Background()

	source := liveSession(t, store, owner, "a", CreateOptions{})
	if _, err := source.Append(userEvent(t, "你好")); err != nil {
		t.Fatal(err)
	}
	child, err := store.Fork(ctx, owner, source, nil, "child")
	if err != nil {
		t.Fatal(err)
	}
	// 往子会话里追加动不了父会话，反过来也一样。
	if _, err := child.Append(userEvent(t, "只在子会话里")); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Append(userEvent(t, "只在父会话里")); err != nil {
		t.Fatal(err)
	}
	if source.Seq() != 2 || child.Seq() != 3 {
		t.Fatalf("父 seq=%d 子 seq=%d", source.Seq(), child.Seq())
	}
	// 子会话的第一条是复制过的，改父会话那份底层数组动不了它。
	sourceEvents := source.Events()
	childEvents := child.Events()
	if &sourceEvents[0].Data[0] == &childEvents[0].Data[0] {
		t.Fatal("分叉出来的 seed 没有被复制")
	}
}

func TestForkErrorCarriesItsCodeAndMessage(t *testing.T) {
	rejected := forkError(ForkOpenTurn, "边界 %d 不行", 3)
	if rejected.Code != ForkOpenTurn || rejected.Error() != "边界 3 不行" {
		t.Fatalf("造出来的是 %#v", rejected)
	}
}
