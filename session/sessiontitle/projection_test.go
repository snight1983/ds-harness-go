// 本文件的作用：钉住标题那个投影单元——它折出什么、漏掉什么，以及它和
// [FoldSnapshot] 之间那条「有意分家」的界线。

package sessiontitle

import (
	"testing"

	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/session/projection"
)

// viewOf 从一次读切里取出标题单元当下的客户端视图。
func viewOf(t *testing.T, registry *projection.Registry, view projection.SessionView) TitleView {
	t.Helper()

	snapshot := registry.Snapshot(view)
	value, ok := snapshot.Values[ProjectionKey]
	if !ok {
		t.Fatalf("单元 %q 该出现在读切里", ProjectionKey)
	}
	title, ok := value.(TitleView)
	if !ok {
		t.Fatalf("视图的类型是 %T", value)
	}
	return title
}

func TestRegisterProjectionInstallsTheUnit(t *testing.T) {
	t.Parallel()

	registry := projection.NewRegistry()
	dispose, err := RegisterProjection(registry)
	if err != nil {
		t.Fatalf("登记不该失败：%v", err)
	}

	view := newSession(userEvent(t, "问一句"))
	if _, ok := registry.StateOf(view, ProjectionKey); !ok {
		t.Fatalf("单元 %q 该在表里", ProjectionKey)
	}

	dispose()
	dispose() // 幂等：再调一次不该把别人的键删掉。
	if _, ok := registry.StateOf(view, ProjectionKey); ok {
		t.Fatalf("注销之后单元 %q 该读成「这个能力不在」", ProjectionKey)
	}
}

func TestRegisterProjectionNeedsARegistry(t *testing.T) {
	t.Parallel()

	if _, err := RegisterProjection(nil); err == nil {
		t.Fatal("没有注册表该报错")
	}
}

func TestProjectionStartsEmptyAndFollowsLastWins(t *testing.T) {
	t.Parallel()

	registry := projection.NewRegistry()
	if _, err := RegisterProjection(registry); err != nil {
		t.Fatalf("登记失败：%v", err)
	}

	sess := newSession(userEvent(t, "问一句"))
	if got := viewOf(t, registry, sess); got.Title != "" {
		t.Fatalf("还没有标题时视图是 %+v，要的是空串", got)
	}

	driveTitle(t, registry, sess, EventData{Title: "旧", MessageSeqs: []int{0}, Source: Source{Kind: SourceFallback}})
	if got := viewOf(t, registry, sess); got.Title != "旧" {
		t.Fatalf("视图是 %+v", got)
	}

	driveTitle(t, registry, sess, EventData{Title: "新", Source: Source{Kind: SourceUser}})
	if got := viewOf(t, registry, sess); got.Title != "新" {
		t.Fatalf("视图是 %+v", got)
	}
}

// driveTitle 往日志上追一条标题事件，再把它喂给注册表的水位缓存。
//
// 两步都要做：单元格是按会话身份缓着的，只追加不喂的话读切还停在老水位上。
func driveTitle(t *testing.T, registry *projection.Registry, sess *fakeSession, data EventData) {
	t.Helper()

	sess.append(titleEvent(t, data))
	events := sess.Events()
	registry.Drive(sess, events[len(events)-1])
}

// 这个单元只给客户端那一个字符串，来路一个字都不推。列表行不需要知道标题的
// 来历，而把来历一起推给每一个客户端会让每次改名都多传一份没人读的东西。
func TestProjectionExposesOnlyTheTitle(t *testing.T) {
	t.Parallel()

	state, changed := applyTitle(titleState{}, titleEvent(t, EventData{
		Title:       "名字",
		MessageSeqs: []int{0},
		Source:      Source{Kind: SourceProvider, Provider: "p1", Model: &ModelProvenance{Provider: "a", Model: "b"}},
	}))
	if !changed || state.Title != "名字" {
		t.Fatalf("折出来是 changed=%v state=%+v", changed, state)
	}
}

func TestApplyTitleIgnoresOtherEventTypes(t *testing.T) {
	t.Parallel()

	before := titleState{Title: "立着的"}
	after, changed := applyTitle(before, userEvent(t, "一句话"))
	if changed || after != before {
		t.Fatalf("别的事件改动了状态：changed=%v after=%+v", changed, after)
	}
}

func TestApplyTitleReportsNoChangeWhenTheTitleIsTheSame(t *testing.T) {
	t.Parallel()

	before := titleState{Title: "一样的"}
	after, changed := applyTitle(before, titleEvent(t, EventData{
		Title: "一样的", MessageSeqs: []int{0}, Source: Source{Kind: SourceFallback},
	}))
	if changed || after != before {
		t.Fatalf("重复标题被当成了变更：changed=%v after=%+v", changed, after)
	}
}

// 一条读不回来的标题事件在这里只能保持原值。这和 [FoldSnapshot] 那边**有意分家**：
// 那边报错（调用方接得住），这边只能在「保持旧标题」和「清成空」之间选，而清空
// 会让列表行上的名字突然消失，比停在一个旧名字上难解释得多。
func TestApplyTitleKeepsTheOldTitleOnAnUnreadablePayload(t *testing.T) {
	t.Parallel()

	before := titleState{Title: "立着的"}
	after, changed := applyTitle(before, session.Event{Type: EventSessionTitle, Data: []byte(`{`)})
	if changed || after != before {
		t.Fatalf("读不回来时改动了状态：changed=%v after=%+v", changed, after)
	}
}

// 严格解码是这层壳存在的理由之一：一个裸的 JSON 字符串没有「多出来的字段」
// 这个概念，严格解码在它身上等于没有。
func TestProjectionStateDecodesStrictly(t *testing.T) {
	t.Parallel()

	decode := projectionDefinition().DecodeState
	if _, err := decode([]byte(`{"title":"名字"}`)); err != nil {
		t.Fatalf("正常状态该读得回来：%v", err)
	}
	if _, err := decode([]byte(`{"title":"名字","多余":1}`)); err == nil {
		t.Fatal("多出来的字段该被拒掉")
	}
}
