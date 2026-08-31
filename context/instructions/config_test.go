// 本文件的作用：钉住补默认值那几条规则，以及基线身份串在什么时候必须变、
// 什么时候必须不变。

package instructions

import (
	"strings"
	"testing"
)

func TestResolve留空取默认值(t *testing.T) {
	resolved := Config{MaxBytes: 100}.Resolve()

	if !equalStrings(resolved.ProjectRootMarkers, []string{".git"}) {
		t.Fatalf("根标记应当取默认值，实际是 %v", resolved.ProjectRootMarkers)
	}
	if !equalStrings(resolved.InstructionFileCandidates, []string{"AGENTS.md", "CLAUDE.md"}) {
		t.Fatalf("基础候选应当取默认值，实际是 %v", resolved.InstructionFileCandidates)
	}
	if !equalStrings(resolved.LocalInstructionFileCandidates, []string{"AGENTS.local.md", "CLAUDE.local.md"}) {
		t.Fatalf("本地候选应当取默认值，实际是 %v", resolved.LocalInstructionFileCandidates)
	}
	if resolved.MaxSourceBytes != DefaultMaxSourceBytes {
		t.Fatalf("单文件上限应当取默认值，实际是 %d", resolved.MaxSourceBytes)
	}
	if resolved.MaxBytes != 100 {
		t.Fatalf("预算应当原样带过来，实际是 %d", resolved.MaxBytes)
	}
	if resolved.UserGlobalRoot != "" {
		t.Fatalf("用户全局根没填时应当留空，实际是 %q", resolved.UserGlobalRoot)
	}
}

// 默认值切片交出去之后被调用方改写，绝不能污染到下一次 Resolve——
// 那种污染跨会话生效，而且现场离出错点很远。
func TestResolve交出去的默认值是副本(t *testing.T) {
	first := Config{MaxBytes: 100}.Resolve()
	first.ProjectRootMarkers[0] = "被改了"
	first.InstructionFileCandidates[0] = "被改了"
	first.LocalInstructionFileCandidates[0] = "被改了"

	second := Config{MaxBytes: 100}.Resolve()
	if second.ProjectRootMarkers[0] != ".git" {
		t.Fatalf("根标记的默认值被污染了：%v", second.ProjectRootMarkers)
	}
	if second.InstructionFileCandidates[0] != "AGENTS.md" {
		t.Fatalf("基础候选的默认值被污染了：%v", second.InstructionFileCandidates)
	}
	if second.LocalInstructionFileCandidates[0] != "AGENTS.local.md" {
		t.Fatalf("本地候选的默认值被污染了：%v", second.LocalInstructionFileCandidates)
	}
}

// nil 是「没填」，非 nil 的空切片是「明确要求关掉这一层」。
func TestResolve空切片和nil不是一回事(t *testing.T) {
	off := Config{MaxBytes: 100, LocalInstructionFileCandidates: []string{}}.Resolve()
	if len(off.LocalInstructionFileCandidates) != 0 {
		t.Fatalf("给了空切片就该关掉本地这一层，实际是 %v", off.LocalInstructionFileCandidates)
	}
	if off.LocalInstructionFileCandidates == nil {
		t.Fatal("结果切片必须非 nil，否则身份串会编成 null")
	}
}

func TestResolve筛掉不能当文件名的候选(t *testing.T) {
	resolved := Config{
		MaxBytes:                  100,
		InstructionFileCandidates: []string{"", ".", "..", "a/b.md", `a\b.md`, "AGENTS.md"},
	}.Resolve()

	if !equalStrings(resolved.InstructionFileCandidates, []string{"AGENTS.md"}) {
		t.Fatalf("只该留下 AGENTS.md，实际是 %v", resolved.InstructionFileCandidates)
	}
}

func TestResolve单文件上限填负数表示关掉(t *testing.T) {
	resolved := Config{MaxBytes: 100, MaxSourceBytes: -1}.Resolve()
	if resolved.MaxSourceBytes != -1 {
		t.Fatalf("负数应当原样带过来，实际是 %d", resolved.MaxSourceBytes)
	}
}

// 身份串记的是根**相对 cwd 的位置**，不是绝对路径：同一个仓库挂在不同的
// 绝对路径下仍然是同一份基线。
func TestWorkspaceBaselineIdentity换个挂载点不变(t *testing.T) {
	config := testConfig()
	first := WorkspaceBaselineIdentity(config, "/home/a/repo/sub", "/home/a/repo")
	second := WorkspaceBaselineIdentity(config, "/mnt/b/repo/sub", "/mnt/b/repo")

	if first != second {
		t.Fatalf("换个挂载点不该改变身份串：\n%s\n%s", first, second)
	}
	if !strings.Contains(first, `"projectRoot":".."`) {
		t.Fatalf("身份串里的根应当是相对 cwd 的，实际是：%s", first)
	}
}

func TestWorkspaceBaselineIdentity根相对cwd挪了就变(t *testing.T) {
	config := testConfig()
	first := WorkspaceBaselineIdentity(config, "/repo/sub", "/repo")
	second := WorkspaceBaselineIdentity(config, "/repo/sub/deeper", "/repo")

	if first == second {
		t.Fatal("根相对 cwd 挪了一层，身份串必须变")
	}
}

func TestWorkspaceBaselineIdentity预算和候选都进串(t *testing.T) {
	base := WorkspaceBaselineIdentity(testConfig(), "/repo", "/repo")

	cases := map[string]ResolvedConfig{
		"预算变了":    Config{MaxBytes: 1}.Resolve(),
		"单文件上限变了": Config{MaxBytes: 1 << 20, MaxSourceBytes: 7}.Resolve(),
		"根标记变了":   Config{MaxBytes: 1 << 20, ProjectRootMarkers: []string{".hg"}}.Resolve(),
		"基础候选变了": Config{MaxBytes: 1 << 20,
			InstructionFileCandidates: []string{"AGENTS.md"}}.Resolve(),
		"本地候选关了": Config{MaxBytes: 1 << 20,
			LocalInstructionFileCandidates: []string{}}.Resolve(),
	}
	for name, config := range cases {
		t.Run(name, func(t *testing.T) {
			if got := WorkspaceBaselineIdentity(config, "/repo", "/repo"); got == base {
				t.Fatalf("这一项变了，身份串必须跟着变：%s", got)
			}
		})
	}
}

// 关掉本地那一层之后，串里必须是 []（不是 null）——同一份配置在两次运行里
// 编出不同的串，会让一次本来能续上的会话判成不兼容。
func TestWorkspaceBaselineIdentity空候选编成空数组(t *testing.T) {
	config := Config{MaxBytes: 1, LocalInstructionFileCandidates: []string{}}.Resolve()
	identity := WorkspaceBaselineIdentity(config, "/repo", "/repo")

	requireContains(t, identity, `"localInstructionFileCandidates":[]`)
	requireNotContains(t, identity, "null")
}
