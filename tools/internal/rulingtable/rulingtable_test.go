// 本文件的作用：钉住「跨上游版本认同一个符号」这件事。
//
// 裁决表里最贵的东西是人填的那三列——decision / go_ref / note。它们不能因为上游
// 发了一版、代码往下挪了两行就丢掉。这正是真实发生过的事：换到 v0.1.2-alpha.3 之后
// 6224 条 PENDING 里有 2947 条其实早就裁决过，只是行号变了配不上了。
//
// 所以这里验的不是「MatchKey 返回什么字符串」，而是那个后果：**只挪了行号的符号，
// 身份必须不变**。
package rulingtable

import "testing"

// TestMatchKeyIgnoresLineDrift 是这个包存在的核心理由。
//
// 行号是派生数据。拿它当身份的一部分，等于让上游的任何一次重排都把人的工作清零。
func TestMatchKeyIgnoresLineDrift(t *testing.T) {
	t.Parallel()

	before := Row{Package: "core", File: "src/index.ts", Line: 425, Kind: "function", Name: "createSession"}
	after := before
	after.Line = 612

	if before.MatchKey() != after.MatchKey() {
		t.Error("只挪了行号的符号必须还是同一个符号，否则人填的裁决会跟着行号一起丢")
	}
	// 反过来，Key 是「同一份清单快照内的精确身份」，它**应当**把行号算进去。
	// 两个键的分工不同，混用哪一个都会出错，所以两边都钉住。
	if before.Key() == after.Key() {
		t.Error("Key 是快照内的精确身份，行号变了就该是不同的键")
	}
}

// TestMatchKeyDistinguishesRealDifferences 验另一半：不该配上的不能配上。
//
// 一个只会说「都一样」的键和没有键是一回事——它会把 GO_NATIVE 的理由贴到一个
// 同名的 type 上去。
func TestMatchKeyDistinguishesRealDifferences(t *testing.T) {
	t.Parallel()

	base := Row{Package: "core", File: "src/index.ts", Line: 10, Kind: "function", Name: "run"}
	tests := []struct {
		name  string
		alter func(Row) Row
	}{
		{"换了包", func(r Row) Row { r.Package = "acp"; return r }},
		{"换了文件", func(r Row) Row { r.File = "src/other.ts"; return r }},
		{"换了种类", func(r Row) Row { r.Kind = "type"; return r }},
		{"换了名字", func(r Row) Row { r.Name = "walk"; return r }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if base.MatchKey() == test.alter(base).MatchKey() {
				t.Error("这是两个不同的符号，键不该相同")
			}
		})
	}
}

// TestMatchIndexPairsDuplicateKeysInLineOrder 处理重复键。
//
// 清单里有 106 行的 MatchKey 是重复的（绝大多数是名字为 `*` 的 star 转发导出）。
// 靠序号分它们，比「重复就整批不配」（那会把这 106 条的裁决全丢掉）和「随便挑一条」
// （结果取决于 map 遍历次序，每次跑都不一样）都强。
//
// 关键是序号按**行序**算，而不是按传进来的次序：上游重排之后，同一个文件里的 9 条
// star 转发在清单里可能是另一个顺序，只有按行号定序两边才对得上。
func TestMatchIndexPairsDuplicateKeysInLineOrder(t *testing.T) {
	t.Parallel()

	star := func(line int) Row {
		return Row{Package: "core", File: "src/index.ts", Line: line, Kind: "star", Name: "*"}
	}

	// 故意乱序传入，序号仍应按行号 10 < 20 < 30 分别是 0 / 1 / 2。
	rows := []Row{star(30), star(10), star(20)}
	indexes := MatchIndex(rows)

	want := map[int]int{30: 2, 10: 0, 20: 1}
	for position, row := range rows {
		if got := indexes[position]; got != want[row.Line] {
			t.Errorf("第 %d 行（line=%d）的序号是 %d，期望 %d", position, row.Line, got, want[row.Line])
		}
	}
}

// TestQualifiedMatchKeyRoundTripsAcrossLineDrift 把上面三条合起来验一遍真实场景：
// 一批重复键的行整体往下漂了 100 行，配出来的键必须和漂之前完全一致。
//
// 这是 runSync 实际依赖的性质。分开验 MatchKey 和 MatchIndex 都对，合起来仍可能错
// ——比如序号按传入次序算的话，这一条就会红。
func TestQualifiedMatchKeyRoundTripsAcrossLineDrift(t *testing.T) {
	t.Parallel()

	old := []Row{
		{Package: "core", File: "src/index.ts", Line: 10, Kind: "star", Name: "*"},
		{Package: "core", File: "src/index.ts", Line: 20, Kind: "star", Name: "*"},
		{Package: "core", File: "src/index.ts", Line: 30, Kind: "function", Name: "run"},
	}
	drifted := make([]Row, len(old))
	for index, row := range old {
		row.Line += 100
		drifted[index] = row
	}

	oldIndexes, newIndexes := MatchIndex(old), MatchIndex(drifted)
	for index := range old {
		oldKey := QualifiedMatchKey(old[index], oldIndexes[index])
		newKey := QualifiedMatchKey(drifted[index], newIndexes[index])
		if oldKey != newKey {
			t.Errorf("第 %d 行漂了 100 行之后键变了：%q → %q", index, oldKey, newKey)
		}
	}
}
