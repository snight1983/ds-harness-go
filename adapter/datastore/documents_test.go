// 本文件验文档集这一种形状：整组换掉是原子的，找一段文字找得准、数得对、
// 排得稳，而两种方言上答的是同一件事。

package datastore

import (
	"errors"
	"strings"
	"testing"
)

// doc 造一条文档，省掉每处都写全五个字段。
func doc(seq, at int64, kind, facet, body string) Doc {
	return Doc{Seq: seq, At: at, Kind: kind, Facet: facet, Body: body}
}

// bodiesOf 把一批命中的正文抽出来。
func bodiesOf(rows []MatchRow) []string {
	bodies := make([]string, 0, len(rows))
	for _, row := range rows {
		bodies = append(bodies, row.Body)
	}
	return bodies
}

func TestDocs整组换掉(t *testing.T) {
	unit := newDocs(t, "d1")
	ctx := t.Context()

	if err := unit.Replace(ctx, "g1", "v1", []Doc{
		doc(0, 100, "note", "current", "第一版的内容"),
		doc(1, 101, "note", "current", "第一版的另一条"),
	}); err != nil {
		t.Fatalf("写第一版不该失败：%v", err)
	}
	if err := unit.Replace(ctx, "g1", "v2", []Doc{
		doc(0, 200, "note", "current", "第二版的内容"),
	}); err != nil {
		t.Fatalf("写第二版不该失败：%v", err)
	}

	rows, err := unit.Match(ctx, MatchRequest{Needle: "内容", Limit: 10})
	if err != nil {
		t.Fatalf("查找不该失败：%v", err)
	}
	if got := bodiesOf(rows); len(got) != 1 || got[0] != "第二版的内容" {
		t.Errorf("旧那一版没被换掉：%v", got)
	}

	tags, err := unit.Groups(ctx)
	if err != nil {
		t.Fatalf("列分组不该失败：%v", err)
	}
	if tags["g1"] != "v2" {
		t.Errorf("印记没盖上：%q", tags["g1"])
	}
}

func TestDocs整组丢掉(t *testing.T) {
	unit := newDocs(t, "d1")
	ctx := t.Context()

	if err := unit.Replace(ctx, "g1", "v1", []Doc{doc(0, 1, "note", "current", "留不住的一条")}); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}

	existed, err := unit.DropGroup(ctx, "g1")
	if err != nil {
		t.Fatalf("丢组不该失败：%v", err)
	}
	if !existed {
		t.Error("丢掉一个在的组，该说它在")
	}

	rows, err := unit.Match(ctx, MatchRequest{Needle: "留不住", Limit: 10})
	if err != nil {
		t.Fatalf("查找不该失败：%v", err)
	}
	if len(rows) != 0 {
		t.Errorf("丢掉之后还找得到：%v", bodiesOf(rows))
	}

	existed, err = unit.DropGroup(ctx, "g1")
	if err != nil {
		t.Fatalf("再丢一次不该失败：%v", err)
	}
	if existed {
		t.Error("丢一个不在的组，该说它不在")
	}
}

// TestDocs大小写和空白都不算数 验折那一下在写和读两侧是同一个函数。
//
// 这一条在两种库上都必须过：SQLite 不带 ICU 时它的 lower() 只认 ASCII，
// 把折那一下落进 SQL 就会让两种方言在这里分岔。
func TestDocs大小写和空白都不算数(t *testing.T) {
	unit := newDocs(t, "d1")
	ctx := t.Context()

	if err := unit.Replace(ctx, "g1", "v1", []Doc{
		doc(0, 1, "note", "current", "Hello\n\tWORLD 你好"),
	}); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}

	for _, needle := range []string{"hello world", "HELLO   WORLD", "  hello\nworld  ", "你好"} {
		rows, err := unit.Match(ctx, MatchRequest{Needle: needle, Limit: 10})
		if err != nil {
			t.Fatalf("查 %q 不该失败：%v", needle, err)
		}
		if len(rows) != 1 {
			t.Errorf("查 %q 该命中一条，拿到 %d 条", needle, len(rows))
		}
	}
}

// TestDocs数出命中次数 验排序赖以成立的那个数是对的。
func TestDocs数出命中次数(t *testing.T) {
	unit := newDocs(t, "d1")
	ctx := t.Context()

	if err := unit.Replace(ctx, "g1", "v1", []Doc{
		doc(0, 1, "note", "current", "会话 会话 会话"),
		doc(1, 2, "note", "current", "会话 一次"),
	}); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}

	rows, err := unit.Match(ctx, MatchRequest{Needle: "会话", Limit: 10})
	if err != nil {
		t.Fatalf("查找不该失败：%v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("该命中两条，拿到 %d 条", len(rows))
	}
	if rows[0].Hits != 3 || rows[1].Hits != 1 {
		t.Errorf("命中次数不对：%d / %d", rows[0].Hits, rows[1].Hits)
	}
	if rows[0].Seq != 0 {
		t.Error("命中次数多的那条该排在前面")
	}
}

// TestDocs通配符只当字面量 验 LIKE 那道预筛不会把 % 和 _ 放出去。
func TestDocs通配符只当字面量(t *testing.T) {
	unit := newDocs(t, "d1")
	ctx := t.Context()

	if err := unit.Replace(ctx, "g1", "v1", []Doc{
		doc(0, 1, "note", "current", "百分之五十"),
		doc(1, 2, "note", "current", "写着 100% 的那条"),
	}); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}

	rows, err := unit.Match(ctx, MatchRequest{Needle: "%", Limit: 10})
	if err != nil {
		t.Fatalf("查找不该失败：%v", err)
	}
	if got := bodiesOf(rows); len(got) != 1 || got[0] != "写着 100% 的那条" {
		t.Errorf("百分号被当成通配符了：%v", got)
	}
}

func TestDocs按标签和区间筛(t *testing.T) {
	unit := newDocs(t, "d1")
	ctx := t.Context()

	if err := unit.Replace(ctx, "g1", "v1", []Doc{
		doc(0, 100, "user", "current", "同一句话"),
		doc(1, 200, "model", "current", "同一句话"),
		doc(2, 300, "user", "shadowed", "同一句话"),
	}); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}

	seqFrom, atTo := int64(1), int64(250)
	for name, request := range map[string]MatchRequest{
		"按 kind":  {Needle: "同一句话", Kinds: []string{"model"}, Limit: 10},
		"按 facet": {Needle: "同一句话", Facets: []string{"shadowed"}, Limit: 10},
		"按 seq":   {Needle: "同一句话", SeqFrom: &seqFrom, AtTo: &atTo, Limit: 10},
	} {
		rows, err := unit.Match(ctx, request)
		if err != nil {
			t.Fatalf("%s：查找不该失败：%v", name, err)
		}
		if len(rows) != 1 {
			t.Errorf("%s：该命中一条，拿到 %d 条", name, len(rows))
		}
	}
}

func TestDocs按分组圈定和排除(t *testing.T) {
	unit := newDocs(t, "d1")
	ctx := t.Context()

	for _, group := range []string{"g1", "g2", "g3"} {
		if err := unit.Replace(ctx, group, "v1", []Doc{
			doc(0, 1, "note", "current", "都有的那句话"),
		}); err != nil {
			t.Fatalf("写 %s 不该失败：%v", group, err)
		}
	}

	rows, err := unit.Match(ctx, MatchRequest{
		Needle:        "都有的",
		Groups:        []string{"g1", "g2"},
		ExcludeGroups: []string{"g2"},
		Limit:         10,
	})
	if err != nil {
		t.Fatalf("查找不该失败：%v", err)
	}
	if len(rows) != 1 || rows[0].Group != "g1" {
		t.Errorf("圈定和排除没都生效：%v", rows)
	}
}

// TestDocs每组只留最好的那一条 验去重发生在切页之前。
//
// 反过来做（先切页再在内存里去重）会让第一页里挤满同一组，而那一组在下一页
// 一条都不剩——这一条压的就是那件事。
func TestDocs每组只留最好的那一条(t *testing.T) {
	unit := newDocs(t, "d1")
	ctx := t.Context()

	// g1 那一组的三条都比 g2 的命中次数多。不做分组去重的话，一页两条会全被 g1 占。
	if err := unit.Replace(ctx, "g1", "v1", []Doc{
		doc(0, 1, "note", "current", "找 找 找 找"),
		doc(1, 2, "note", "current", "找 找 找"),
		doc(2, 3, "note", "current", "找 找"),
	}); err != nil {
		t.Fatalf("写 g1 不该失败：%v", err)
	}
	if err := unit.Replace(ctx, "g2", "v1", []Doc{
		doc(0, 4, "note", "current", "找"),
	}); err != nil {
		t.Fatalf("写 g2 不该失败：%v", err)
	}

	rows, err := unit.Match(ctx, MatchRequest{Needle: "找", BestPerGroup: true, Limit: 2})
	if err != nil {
		t.Fatalf("查找不该失败：%v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("该有两条，拿到 %d 条", len(rows))
	}
	if rows[0].Group != "g1" || rows[0].Seq != 0 {
		t.Errorf("第一条该是 g1 里命中最多的那条：%+v", rows[0])
	}
	if rows[1].Group != "g2" {
		t.Errorf("第二条该是 g2 的那条：%+v", rows[1])
	}
}

// TestDocs翻页翻出同一个序 验游标那一层赖以成立的前提。
func TestDocs翻页翻出同一个序(t *testing.T) {
	unit := newDocs(t, "d1")
	ctx := t.Context()

	// 全部同命中次数、同长度、同时刻，只有 seq 分得开——这是排序最容易不稳的形状。
	docs := make([]Doc, 0, 6)
	for seq := range int64(6) {
		docs = append(docs, doc(seq, 100, "note", "current", "一样长的一句"))
	}
	if err := unit.Replace(ctx, "g1", "v1", docs); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}

	var walked []int64
	for offset := 0; offset < 6; offset += 2 {
		rows, err := unit.Match(ctx, MatchRequest{Needle: "一样长", Limit: 2, Offset: offset})
		if err != nil {
			t.Fatalf("查第 %d 页不该失败：%v", offset, err)
		}
		for _, row := range rows {
			walked = append(walked, row.Seq)
		}
	}

	whole, err := unit.Match(ctx, MatchRequest{Needle: "一样长", Limit: 10})
	if err != nil {
		t.Fatalf("整取不该失败：%v", err)
	}
	if len(walked) != len(whole) {
		t.Fatalf("翻页走出 %d 条，整取 %d 条", len(walked), len(whole))
	}
	for index, row := range whole {
		if walked[index] != row.Seq {
			t.Fatalf("第 %d 条对不上：翻页 %d，整取 %d", index, walked[index], row.Seq)
		}
	}
}

func TestDocs拒掉说不通的查找(t *testing.T) {
	unit := newDocs(t, "d1")
	ctx := t.Context()

	for name, request := range map[string]MatchRequest{
		"空的要找的文字":    {Needle: "   ", Limit: 10},
		"Limit 不为正":  {Needle: "x", Limit: 0},
		"Offset 是负的": {Needle: "x", Limit: 10, Offset: -1},
	} {
		if _, err := unit.Match(ctx, request); !errors.Is(err, ErrMalformedName) {
			t.Errorf("%s：要的是 ErrMalformedName，拿到 %v", name, err)
		}
	}
}

// TestDocs同一个单元名开两次就拒 验它和另外两种形状守着同一条规矩。
func TestDocs同一个单元名开两次就拒(t *testing.T) {
	medium := newMedium(t)
	ctx := t.Context()

	unit, err := medium.OpenDocs(ctx, DocSpec{Name: "d1", Version: 1})
	if err != nil {
		t.Fatalf("第一次打开不该失败：%v", err)
	}
	if _, err := medium.OpenDocs(ctx, DocSpec{Name: "d1", Version: 1}); !errors.Is(err, ErrAlreadyOpen) {
		t.Fatalf("要的是 ErrAlreadyOpen，拿到 %v", err)
	}

	if err := unit.Close(ctx); err != nil {
		t.Fatalf("关不该失败：%v", err)
	}
	again, err := medium.OpenDocs(ctx, DocSpec{Name: "d1", Version: 1})
	if err != nil {
		t.Fatalf("关掉之后该重新开得起来：%v", err)
	}
	_ = again.Close(ctx)
}

// TestDocs形态对不上就拒 验登记处认得出这是第三种形状。
func TestDocs形态对不上就拒(t *testing.T) {
	medium := newMedium(t)
	ctx := t.Context()

	unit, err := medium.OpenDocs(ctx, DocSpec{Name: "u1", Version: 1})
	if err != nil {
		t.Fatalf("打开文档集不该失败：%v", err)
	}
	if err := unit.Close(ctx); err != nil {
		t.Fatalf("关不该失败：%v", err)
	}

	_, err = medium.OpenLog(ctx, LogSpec{Name: "u1", Version: 1})
	if !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("要的是 ErrVersionMismatch，拿到 %v", err)
	}
	if !strings.Contains(err.Error(), "文档集") {
		t.Errorf("错误里该说清它原本是文档集：%v", err)
	}
}

func TestDocs关掉之后每个方法都拒(t *testing.T) {
	unit := newDocs(t, "d1")
	ctx := t.Context()

	if err := unit.Close(ctx); err != nil {
		t.Fatalf("关不该失败：%v", err)
	}
	if err := unit.Close(ctx); err != nil {
		t.Fatalf("再关一次该是空操作：%v", err)
	}

	if err := unit.Replace(ctx, "g1", "v1", nil); !errors.Is(err, ErrClosed) {
		t.Errorf("Replace 要的是 ErrClosed，拿到 %v", err)
	}
	if _, err := unit.DropGroup(ctx, "g1"); !errors.Is(err, ErrClosed) {
		t.Errorf("DropGroup 要的是 ErrClosed，拿到 %v", err)
	}
	if _, err := unit.Groups(ctx); !errors.Is(err, ErrClosed) {
		t.Errorf("Groups 要的是 ErrClosed，拿到 %v", err)
	}
	if _, err := unit.Match(ctx, MatchRequest{Needle: "x", Limit: 1}); !errors.Is(err, ErrClosed) {
		t.Errorf("Match 要的是 ErrClosed，拿到 %v", err)
	}
}

// TestDocs一批写得下大批文档 验分块那一下把绑定参数的上限躲开了。
func TestDocs一批写得下大批文档(t *testing.T) {
	unit := newDocs(t, "d1")
	ctx := t.Context()

	const count = 1200
	docs := make([]Doc, 0, count)
	for seq := range int64(count) {
		docs = append(docs, doc(seq, seq, "note", "current", "第 x 条"))
	}
	if err := unit.Replace(ctx, "g1", "v1", docs); err != nil {
		t.Fatalf("写一大批不该失败：%v", err)
	}

	rows, err := unit.Match(ctx, MatchRequest{Needle: "第 x 条", Limit: count + 1})
	if err != nil {
		t.Fatalf("查找不该失败：%v", err)
	}
	if len(rows) != count {
		t.Errorf("该命中 %d 条，拿到 %d 条", count, len(rows))
	}
}
