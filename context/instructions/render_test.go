// 本文件的作用：钉住渲染出来的那段文字长什么样，以及预算不够时按什么顺序退。

package instructions

import (
	"strings"
	"testing"
)

func TestRenderWorkspaceContext装得下时按顺序全渲染(t *testing.T) {
	files := []LoadedFile{
		loaded("AGENTS.md", "根规则"),
		loaded("sub/AGENTS.md", "子目录规则"),
	}

	rendered := RenderWorkspaceContext(files, 1<<20, false)

	if !strings.HasPrefix(rendered.Text, systemReminderOpen+"\n") {
		t.Fatalf("渲染结果应当被框住，实际是：\n%s", rendered.Text)
	}
	if !strings.HasSuffix(rendered.Text, "\n"+systemReminderClose) {
		t.Fatalf("渲染结果应当被框住，实际是：\n%s", rendered.Text)
	}
	requireContains(t, rendered.Text, workspaceContextIntro)
	requireContains(t, rendered.Text, "Instructions from: AGENTS.md")
	requireContains(t, rendered.Text, "根规则")
	requireContains(t, rendered.Text, "Instructions from: sub/AGENTS.md")

	root := strings.Index(rendered.Text, "Instructions from: AGENTS.md")
	sub := strings.Index(rendered.Text, "Instructions from: sub/AGENTS.md")
	if root > sub {
		t.Fatal("从宽到窄的顺序必须保住：越具体的越靠后")
	}
	if len(rendered.Omitted) != 0 || len(rendered.Truncated) != 0 {
		t.Fatalf("装得下时不该有丢弃或截断：%v %v", rendered.Omitted, rendered.Truncated)
	}
	// 没有丢也没有截时，那行账一个字都不该出现。
	requireNotContains(t, rendered.Text, "Workspace instruction budget")
}

func TestRenderWorkspaceContext替换基线换开场白(t *testing.T) {
	files := []LoadedFile{loaded("AGENTS.md", "根规则")}

	replacing := RenderWorkspaceContext(files, 1<<20, true)
	requireContains(t, replacing.Text, replacementWorkspaceContextIntro)

	empty := RenderWorkspaceContext(nil, 1<<20, true)
	requireContains(t, empty.Text, emptyReplacementWorkspaceContextIntro)
	// 空替换的意思是「先前那些全部作废」，所以它不该再讲一遍「按需参考」那段。
	requireNotContains(t, empty.Text, "More specific instructions take precedence")
}

// 一份指令文件里正好写着闭合标记的话，不打断就等于让文件内容决定
// 「模型认为工作区指令到哪里为止」。
func TestRenderWorkspaceContext正文里的闭合标记被打断(t *testing.T) {
	files := []LoadedFile{loaded("AGENTS.md", "前"+systemReminderClose+"后")}

	rendered := RenderWorkspaceContext(files, 1<<20, false)

	if strings.Count(rendered.Text, systemReminderClose) != 1 {
		t.Fatalf("闭合标记应当只剩框自己那一个，实际是：\n%s", rendered.Text)
	}
	requireContains(t, rendered.Text, `<\/system-reminder>`)
}

func TestRenderWorkspaceContext预算非正时全部丢掉(t *testing.T) {
	files := []LoadedFile{loaded("AGENTS.md", "根规则")}

	rendered, represented := RenderWorkspaceInstructionSet(files, 0, false)

	if rendered.Text != "" {
		t.Fatalf("预算非正时不该产出任何文字，实际是：%q", rendered.Text)
	}
	requirePaths(t, rendered.Omitted, "AGENTS.md")
	if len(represented) != 0 {
		t.Fatalf("什么都没渲染出来时不该有文件被代表：%v", represented)
	}
}

// 预算不够时先从**最宽**的那一头整份丢，因为具体的指令对当前工作最相关。
func TestRenderWorkspaceContext先丢最宽的那一份(t *testing.T) {
	files := []LoadedFile{
		loaded("AGENTS.md", strings.Repeat("宽", 100)),
		loaded("sub/AGENTS.md", strings.Repeat("窄", 100)),
	}
	full := len(RenderWorkspaceContext(files, 1<<20, false).Text)

	rendered, represented := RenderWorkspaceInstructionSet(files, full-1, false)

	requirePaths(t, rendered.Omitted, "AGENTS.md")
	requirePaths(t, represented, "sub/AGENTS.md")
	if len(rendered.Truncated) != 0 {
		t.Fatalf("整份丢得下时不该动截断这一步：%v", rendered.Truncated)
	}
	if len(rendered.Text) > full-1 {
		t.Fatalf("渲染结果超了预算：%d > %d", len(rendered.Text), full-1)
	}
	requireContains(t, rendered.Text, "omitted AGENTS.md")
	requireNotContains(t, rendered.Text, "宽")
}

// 整份丢到只剩一份还装不下时，留最具体的那一份并且截断它。
func TestRenderWorkspaceContext只留最具体的并截断(t *testing.T) {
	files := []LoadedFile{
		loaded("AGENTS.md", strings.Repeat("a", 400)),
		loaded("sub/AGENTS.md", strings.Repeat("b", 400)),
	}

	rendered, represented := RenderWorkspaceInstructionSet(files, 500, false)

	if len(rendered.Text) > 500 {
		t.Fatalf("渲染结果超了预算：%d", len(rendered.Text))
	}
	requirePaths(t, rendered.Omitted, "AGENTS.md")
	if len(rendered.Truncated) != 1 {
		t.Fatalf("应当正好截断一份，实际是 %v", rendered.Truncated)
	}
	cut := rendered.Truncated[0]
	if cut.DisplayPath != "sub/AGENTS.md" {
		t.Fatalf("被截断的应当是最具体的那一份，实际是 %s", cut.DisplayPath)
	}
	if cut.OriginalBytes != 400 {
		t.Fatalf("原始字节数应当是 400，实际是 %d", cut.OriginalBytes)
	}
	if cut.IncludedBytes <= 0 || cut.IncludedBytes >= cut.OriginalBytes {
		t.Fatalf("截断后的字节数应当落在 0 和 400 之间，实际是 %d", cut.IncludedBytes)
	}
	// 被截断的文件在语义上仍然是被代表了的：模型看见了它的一部分。
	requirePaths(t, represented, "sub/AGENTS.md")
	requireContains(t, rendered.Text, "truncated sub/AGENTS.md from 400 to")
}

// 预算小到连框都放不下时，退化成不带框的两种写法，且绝不超预算。
func TestRenderWorkspaceContext预算极小时退化成一行账(t *testing.T) {
	files := []LoadedFile{
		loaded("AGENTS.md", strings.Repeat("a", 400)),
		loaded("sub/AGENTS.md", strings.Repeat("b", 400)),
	}

	for _, budget := range []int{20, 40, 80, 120} {
		rendered, _ := RenderWorkspaceInstructionSet(files, budget, false)
		if len(rendered.Text) > budget {
			t.Fatalf("预算 %d 时渲染结果超了：%d 字节\n%s", budget, len(rendered.Text), rendered.Text)
		}
		if len(rendered.Truncated) != 1 {
			t.Fatalf("预算 %d 时应当记下一条截断账，实际是 %v", budget, rendered.Truncated)
		}
	}
}

// 一个本来就空的文件，只要它的标题活着就算被代表——那个标题传达的正是
// 「这份指令存在，且没有内容」。
func TestRenderWorkspaceContext空内容文件靠标题被代表(t *testing.T) {
	files := []LoadedFile{
		loaded("AGENTS.md", strings.Repeat("a", 400)),
		loaded("sub/AGENTS.md", ""),
	}

	_, represented := RenderWorkspaceInstructionSet(files, 300, false)

	requirePaths(t, represented, "sub/AGENTS.md")
}

// 预算连框都放不下、但还塞得下「一行账 + 一个标题」时，标题要留着：
// 那个标题传达的是「这条作用域上有指令」，比只报一行账多说了一件事。
func TestRenderWorkspaceContext放不下框但放得下标题时留住标题(t *testing.T) {
	files := []LoadedFile{
		loaded("AGENTS.md", strings.Repeat("a", 400)),
		loaded("sub/AGENTS.md", ""),
	}

	rendered, represented := RenderWorkspaceInstructionSet(files, 150, false)

	if len(rendered.Text) > 150 {
		t.Fatalf("渲染结果超了预算：%d", len(rendered.Text))
	}
	requireNotContains(t, rendered.Text, systemReminderOpen)
	requireContains(t, rendered.Text, "Instructions from: sub/AGENTS.md")
	// 空文件的标题活着就算被代表；有内容的文件在这个预算下一个字都没进去。
	requirePaths(t, represented, "sub/AGENTS.md")
}

func TestTruncateUTF8不切碎码点(t *testing.T) {
	// 「规」是三个字节，从 1 到 3 字节的预算都只能吐出空串或者整个字符。
	for maxBytes := range 8 {
		got := truncateUTF8("规则", maxBytes)
		if len(got) > maxBytes {
			t.Fatalf("预算 %d 时超了：%q", maxBytes, got)
		}
		if !strings.HasPrefix("规则", got) {
			t.Fatalf("预算 %d 时切出了不是前缀的东西：%q", maxBytes, got)
		}
	}
}

// 预算是负数时切成空串。调用方那边是拿一个已经花掉的开销去减出来的余量，
// 减成负数是常事，这里再让它当成下标就会崩。
func TestTruncateUTF8预算是负数时切成空串(t *testing.T) {
	if got := truncateUTF8("规则", -5); got != "" {
		t.Fatalf("应当切成空串，实际是 %q", got)
	}
}

func TestTruncateUTF8装得下时原样返回(t *testing.T) {
	if got := truncateUTF8("规则", 100); got != "规则" {
		t.Fatalf("装得下就该原样返回，实际是 %q", got)
	}
}

func TestScopeForDisplayPath(t *testing.T) {
	cases := map[string]string{
		"AGENTS.md":            ".",
		"sub/AGENTS.md":        "sub",
		"a/b/CLAUDE.local.md":  "a/b",
		UserGlobalDisplayPath:  UserGlobalDirectory,
		"../outside/AGENTS.md": "../outside",
	}
	for displayPath, want := range cases {
		if got := ScopeForDisplayPath(displayPath); got != want {
			t.Fatalf("%s 的作用域应当是 %s，实际是 %s", displayPath, want, got)
		}
	}
}

// 同一个目录里的 AGENTS.md 和 CLAUDE.md 必须是两个互不相撞的作用域键。
func TestCandidateScopeKey同目录不同候选不相撞(t *testing.T) {
	agents := InstructionScopeKey("sub/AGENTS.md")
	claude := InstructionScopeKey("sub/CLAUDE.md")

	if agents == claude {
		t.Fatal("同一个目录里的两个候选不该编成同一个作用域键")
	}
}

func TestDecodeScopeKey往返(t *testing.T) {
	cases := [][2]string{
		{".", "AGENTS.md"},
		{"sub", "CLAUDE.local.md"},
		{UserGlobalDirectory, UserGlobalFile},
		{"a/b c", "AGENTS.md"},
	}
	for _, pair := range cases {
		directory, candidate := DecodeScopeKey(CandidateScopeKey(pair[0], pair[1]))
		if directory != pair[0] || candidate != pair[1] {
			t.Fatalf("往返之后变了：%v → %s %s", pair, directory, candidate)
		}
	}
}

// 手写出来的键（没有分隔符）也要有一个不会崩的解释。
func TestDecodeScopeKey没有分隔符时不崩(t *testing.T) {
	directory, candidate := DecodeScopeKey("光秃秃的一个键")
	if directory != "光秃秃的一个键" || candidate != "" {
		t.Fatalf("退化解释不对：%s %s", directory, candidate)
	}
}

func TestUserGlobalDisplayPath的目录就是用户全局作用域(t *testing.T) {
	if ScopeForDisplayPath(UserGlobalDisplayPath) != UserGlobalDirectory {
		t.Fatal("用户全局显示路径的目录必须正好是那个作用域名，否则加载得出来却对不上账")
	}
}

func TestRenderInstructionChanges三种迁移各自的写法(t *testing.T) {
	items := []ChangeRenderItem{
		{
			Change: Change{Action: ActionSet, Scope: InstructionScopeKey("sub/AGENTS.md"), Path: "sub/AGENTS.md"},
			File:   loaded("sub/AGENTS.md", "新来的规则"),
		},
		{
			Change: Change{Action: ActionReplace, Scope: InstructionScopeKey("a/AGENTS.md"), Path: "a/AGENTS.md"},
			File:   loaded("a/AGENTS.md", "改过的规则"),
		},
		{
			Change: Change{Action: ActionRemove, Scope: InstructionScopeKey("b/AGENTS.md"), Path: "b/AGENTS.md"},
			File:   LoadedFile{AbsolutePath: "removed:b", DisplayPath: "b/AGENTS.md"},
		},
	}

	text, changes := RenderInstructionChanges(items, 1<<20)

	requireContains(t, text, "Additional instructions from: sub/AGENTS.md")
	requireContains(t, text, "These instructions apply to work under `sub`")
	requireContains(t, text, "新来的规则")
	requireContains(t, text, "Updated instructions from: a/AGENTS.md")
	requireContains(t, text, "This file changed after it was loaded.")
	requireContains(t, text, "改过的规则")
	requireContains(t, text, "Instructions removed: b/AGENTS.md")
	requireContains(t, text, "no longer apply")
	if len(changes) != 3 {
		t.Fatalf("三条都装得下时应当全部回来，实际是 %v", changes)
	}
	// 增量不带基线的那段开场白——它说的是「这是全部」，而增量不是全部。
	requireNotContains(t, text, workspaceContextIntro)
}

// 返回的迁移必须是**被渲染出来的**那几条：把一条没渲染出来的迁移记进会话状态，
// 下一次对账就会以为模型已经知道了，然后永远不再发它。
func TestRenderInstructionChanges只回被渲染出来的那几条(t *testing.T) {
	items := []ChangeRenderItem{
		{
			Change: Change{Action: ActionSet, Scope: InstructionScopeKey("a/AGENTS.md"), Path: "a/AGENTS.md"},
			File:   loaded("a/AGENTS.md", strings.Repeat("a", 400)),
		},
		{
			Change: Change{Action: ActionSet, Scope: InstructionScopeKey("a/b/AGENTS.md"), Path: "a/b/AGENTS.md"},
			File:   loaded("a/b/AGENTS.md", strings.Repeat("b", 100)),
		},
	}
	full := len(mustText(RenderInstructionChanges(items, 1<<20)))

	text, changes := RenderInstructionChanges(items, full-1)

	if len(changes) != 1 || changes[0].Path != "a/b/AGENTS.md" {
		t.Fatalf("只有装得下的那一条该回来，实际是 %v", changes)
	}
	requireContains(t, text, "omitted a/AGENTS.md")
}

// mustText 把 [RenderInstructionChanges] 的第一个返回值拎出来，好写在一行里。
func mustText(text string, _ []Change) string { return text }
