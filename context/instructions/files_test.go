// 本文件的作用：钉住路径换算、发现顺序、去重规则，以及「读不出来」的每一种
// 各自会退成什么——尤其是「确认不在」和「提供方问不出来」绝不混为一谈这条。

package instructions

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ds-harness-go/fs"
)

func TestRelativeDisplay(t *testing.T) {
	cases := []struct{ root, target, want string }{
		{"/repo", "/repo", ""},
		{"/repo", "/repo/a", "a"},
		{"/repo", "/repo/a/b", "a/b"},
		{"/repo/a", "/repo", ".."},
		{"/repo/a/b", "/repo", "../.."},
		{"/repo/a", "/repo/b", "../b"},
		{"/", "/repo", "repo"},
	}
	for _, item := range cases {
		if got := RelativeDisplay(item.root, item.target); got != item.want {
			t.Fatalf("%s 相对 %s 应当是 %q，实际是 %q", item.target, item.root, item.want, got)
		}
	}
}

// 反斜杠一律换成斜杠：这条路径要交给执行世界里的后端去解析，
// 而那个世界不一定是宿主机的平台。
func TestRelativeDisplay反斜杠当成分隔符(t *testing.T) {
	if got := RelativeDisplay(`\repo`, `\repo\a`); got != "a" {
		t.Fatalf("反斜杠路径应当被规范成斜杠，实际是 %q", got)
	}
}

// 执行世界里只有绝对路径，所以一条不带前导斜杠的路径按「从世界根算起」解释。
func TestRelativeDisplay不带前导斜杠时补上(t *testing.T) {
	if got := RelativeDisplay("repo", "repo/a"); got != "a" {
		t.Fatalf("应当当成 /repo 和 /repo/a，实际是 %q", got)
	}
}

func TestAncestorChain从宽到窄且两头都含(t *testing.T) {
	chain := AncestorChain("/repo", "/repo/a/b")

	if !equalStrings(chain, []string{"/repo", "/repo/a", "/repo/a/b"}) {
		t.Fatalf("祖先链不对：%v", chain)
	}
}

func TestAncestorChain根和cwd相同时只有一段(t *testing.T) {
	if chain := AncestorChain("/repo", "/repo"); !equalStrings(chain, []string{"/repo"}) {
		t.Fatalf("祖先链不对：%v", chain)
	}
}

func TestDescendantDirsBetween(t *testing.T) {
	got := DescendantDirsBetween("/repo", "/repo/a/b/file.md")

	if !equalStrings(got, []string{"/repo/a", "/repo/a/b"}) {
		t.Fatalf("跨过的后代目录不对：%v", got)
	}
}

func TestDescendantDirsBetween根目录里的文件不跨任何目录(t *testing.T) {
	if got := DescendantDirsBetween("/repo", "/repo/file.md"); len(got) != 0 {
		t.Fatalf("不该跨过任何目录，实际是 %v", got)
	}
}

// 根外面的文件不产出任何作用域：那些目录本来就不在这份基线的管辖里。
func TestDescendantDirsBetween根外的文件不产出作用域(t *testing.T) {
	if got := DescendantDirsBetween("/repo", "/other/file.md"); got != nil {
		t.Fatalf("根外的文件不该产出作用域，实际是 %v", got)
	}
}

func TestDescendantDirsBetween相对路径按根解析(t *testing.T) {
	got := DescendantDirsBetween("/repo", "a/b/file.md")

	if !equalStrings(got, []string{"/repo/a", "/repo/a/b"}) {
		t.Fatalf("相对路径应当按根解析，实际是 %v", got)
	}
}

func TestFindProjectRoot往上走到第一个标记(t *testing.T) {
	fsys := newFakeFS().addDir("/repo/.git")

	root, err := FindProjectRoot(t.Context(), fsys, "/repo/a/b", []string{".git"})

	if err != nil {
		t.Fatalf("找根不该失败：%v", err)
	}
	if root != "/repo" {
		t.Fatalf("根应当是 /repo，实际是 %s", root)
	}
}

// 标记在工作树里是目录、在 worktree 和 submodule 里是文件，所以任何一种在场都算数。
func TestFindProjectRoot标记是文件也算(t *testing.T) {
	fsys := newFakeFS().addFile("/repo/.git", "gitdir: ...")

	root, err := FindProjectRoot(t.Context(), fsys, "/repo/a", []string{".git"})

	if err != nil || root != "/repo" {
		t.Fatalf("根应当是 /repo，实际是 %s（err=%v）", root, err)
	}
}

// 一路走到世界根都没找到就退回 cwd：那说明这不是一个有标记的项目，
// 而「以 cwd 为根」是唯一不需要猜的解释。
func TestFindProjectRoot找不到标记时退回cwd(t *testing.T) {
	fsys := newFakeFS()

	root, err := FindProjectRoot(t.Context(), fsys, "/a/b", []string{".git"})

	if err != nil || root != "/a/b" {
		t.Fatalf("应当退回 cwd，实际是 %s（err=%v）", root, err)
	}
}

// 探标记时提供方抖一下只说明「这里没看见标记」，不能当成「标记在这里」——
// 后者会把根定在一个随便哪层的目录上，之后所有显示路径都相对错了的根算。
func TestFindProjectRoot探标记抖动时当成没有并继续往上(t *testing.T) {
	for name, fsys := range map[string]*fakeFS{
		"Resolve 抖": newFakeFS().addDir("/repo/.git").
			addDir("/repo/a/.git").failResolve("/repo/a/.git", errFake),
		"Stat 抖": newFakeFS().addDir("/repo/.git").
			addDir("/repo/a/.git").failStat("/repo/a/.git", errFake),
	} {
		t.Run(name, func(t *testing.T) {
			root, err := FindProjectRoot(t.Context(), fsys, "/repo/a", []string{".git"})

			if err != nil {
				t.Fatalf("单点抖动不该让找根失败：%v", err)
			}
			if root != "/repo" {
				t.Fatalf("应当越过抖掉的那一层继续往上，实际是 %s", root)
			}
		})
	}
}

func TestFindProjectRoot取消时立刻回错(t *testing.T) {
	fsys := newFakeFS().addDir("/repo/.git")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := FindProjectRoot(ctx, fsys, "/repo/a", []string{".git"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消必须原样报出来，实际是 %v", err)
	}
}

func TestDiscoverBaselineFiles按模型优先级顺序(t *testing.T) {
	fsys := newFakeFS().
		addDir("/repo/.git").
		addFile("/home/.dsh/AGENTS.md", "全局").
		addFile("/repo/AGENTS.md", "根 A").
		addFile("/repo/CLAUDE.md", "根 C").
		addFile("/repo/AGENTS.local.md", "根本地").
		addFile("/repo/sub/AGENTS.md", "子 A")
	config := Config{MaxBytes: 1 << 20, UserGlobalRoot: "/home/.dsh"}.Resolve()

	files, err := DiscoverBaselineFiles(t.Context(), fsys, config, "/repo/sub", "")

	if err != nil {
		t.Fatalf("发现不该失败：%v", err)
	}
	requirePaths(t, files,
		UserGlobalDisplayPath,
		"AGENTS.md", "CLAUDE.md", "AGENTS.local.md",
		"sub/AGENTS.md")
}

// 用户全局那一层留空表示这套部署没有它，发现阶段直接跳过而不是去探一条猜出来的路径。
func TestDiscoverBaselineFiles用户全局留空时整层跳过(t *testing.T) {
	fsys := newFakeFS().addDir("/repo/.git").addFile("/AGENTS.md", "世界根上的东西")

	files, err := DiscoverBaselineFiles(t.Context(), fsys, testConfig(), "/repo", "")

	if err != nil {
		t.Fatalf("发现不该失败：%v", err)
	}
	requirePaths(t, files)
	if fsys.resolveCalls["/AGENTS.md"] != 0 {
		t.Fatal("用户全局关着的时候不该去探任何一条用户全局路径")
	}
}

func TestDiscoverBaselineFiles跟着符号链接走到常规文件(t *testing.T) {
	fsys := newFakeFS().
		addDir("/repo/.git").
		addFile("/elsewhere/rules.md", "被链过去的规则").
		addLink("/repo/AGENTS.md", "/elsewhere/rules.md")

	files, err := DiscoverBaselineFiles(t.Context(), fsys, testConfig(), "/repo", "/repo")

	if err != nil {
		t.Fatalf("发现不该失败：%v", err)
	}
	requirePaths(t, files, "AGENTS.md")
}

func TestDiscoverBaselineFiles链到目录或者断链都算不在(t *testing.T) {
	fsys := newFakeFS().
		addDir("/repo/.git").
		addDir("/elsewhere").
		addLink("/repo/AGENTS.md", "/elsewhere").
		addLink("/repo/CLAUDE.md", "/nowhere")

	files, err := DiscoverBaselineFiles(t.Context(), fsys, testConfig(), "/repo", "/repo")

	if err != nil {
		t.Fatalf("发现不该失败：%v", err)
	}
	requirePaths(t, files)
}

// 提供方抖一下只跳过那一个候选，剩下那些互相独立的照常加载——
// 一次抖动不该让整份基线塌掉。
func TestDiscoverBaselineFiles提供方故障只跳过那一个候选(t *testing.T) {
	fsys := newFakeFS().
		addDir("/repo/.git").
		addFile("/repo/AGENTS.md", "根 A").
		addFile("/repo/CLAUDE.md", "根 C").
		failStat("/repo/AGENTS.md", errFake)

	files, err := DiscoverBaselineFiles(t.Context(), fsys, testConfig(), "/repo", "/repo")

	if err != nil {
		t.Fatalf("单个候选的故障不该让发现整个失败：%v", err)
	}
	requirePaths(t, files, "CLAUDE.md")
}

// 用户全局根正好就是项目根时，同一份文件会被两层各发现一次。留先到的那一次，
// 也就是用户全局那条显示路径。
func TestDiscoverBaselineFiles同一个绝对路径只算一次(t *testing.T) {
	fsys := newFakeFS().addDir("/repo/.git").addFile("/repo/AGENTS.md", "两层都指到的规则")
	config := Config{MaxBytes: 1 << 20, UserGlobalRoot: "/repo"}.Resolve()

	files, err := DiscoverBaselineFiles(t.Context(), fsys, config, "/repo", "/repo")

	if err != nil {
		t.Fatalf("发现不该失败：%v", err)
	}
	requirePaths(t, files, UserGlobalDisplayPath)
}

// 取消可能落在发现阶段的任何一步上，每一步都必须原样报出来而不是当成「不在」。
func TestDiscoverBaselineFiles取消不降级成不在(t *testing.T) {
	cases := map[string]struct {
		config      ResolvedConfig
		projectRoot string
	}{
		"探项目里的候选时": {testConfig(), "/repo"},
		"探用户全局那一层时": {
			Config{MaxBytes: 1 << 20, UserGlobalRoot: "/home/.dsh"}.Resolve(), "/repo",
		},
		"根留空要自己去找时": {testConfig(), ""},
	}
	for name, item := range cases {
		t.Run(name, func(t *testing.T) {
			fsys := newFakeFS().addDir("/repo/.git").addFile("/repo/AGENTS.md", "根 A")
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			_, err := DiscoverBaselineFiles(ctx, fsys, item.config, "/repo", item.projectRoot)

			if !errors.Is(err, context.Canceled) {
				t.Fatalf("取消必须原样报出来，实际是 %v", err)
			}
		})
	}
}

func TestDedupByDirectory同目录内容一样只留最靠前的(t *testing.T) {
	files := []LoadedFile{
		loaded("AGENTS.md", "规则"),
		loaded("CLAUDE.md", "规则\n"),
		loaded("AGENTS.local.md", "另一套规则"),
	}

	kept := DedupByDirectory(files)

	requirePaths(t, kept, "AGENTS.md", "AGENTS.local.md")
}

// 留下来的那份渲染的是**原始字节**，不是去过空白的那份。
func TestDedupByDirectory留下来的内容没被去空白(t *testing.T) {
	files := []LoadedFile{loaded("AGENTS.md", "  规则  ")}

	kept := DedupByDirectory(files)

	if kept[0].Content != "  规则  " {
		t.Fatalf("内容不该被动过，实际是 %q", kept[0].Content)
	}
}

// 不同目录之间永远不折叠：那是两条不同作用域上各自成立的指令，只是碰巧写得一样。
func TestDedupByDirectory跨目录不折叠(t *testing.T) {
	files := []LoadedFile{
		loaded("AGENTS.md", "规则"),
		loaded("sub/AGENTS.md", "规则"),
	}

	kept := DedupByDirectory(files)

	requirePaths(t, kept, "AGENTS.md", "sub/AGENTS.md")
}

func TestLoadBaselineSet装出一整条基线(t *testing.T) {
	fsys := newFakeFS().
		addDir("/repo/.git").
		addFile("/repo/AGENTS.md", "根规则").
		addFile("/repo/sub/AGENTS.md", "子规则")

	set, ok, err := LoadBaselineSet(t.Context(), fsys, testConfig(), "/repo/sub", "", false)

	if err != nil || !ok {
		t.Fatalf("装基线不该失败：ok=%v err=%v", ok, err)
	}
	requirePaths(t, set.Included, "AGENTS.md", "sub/AGENTS.md")
	requirePaths(t, set.Observed, "AGENTS.md", "sub/AGENTS.md")
	requireContains(t, set.Rendered.Text, "根规则")
	requireContains(t, set.Rendered.Text, "子规则")
	if set.Included[0].Version == "" {
		t.Fatal("版本令牌应当从提供方带过来")
	}
}

// Observed 是读成功了的全部候选，Included 是去重和裁剪之后真正留下的——
// 这两份账不是一回事，会话状态要靠它们分别回答两个不同的问题。
func TestLoadBaselineSet两份文件清单各记各的(t *testing.T) {
	fsys := newFakeFS().
		addDir("/repo/.git").
		addFile("/repo/AGENTS.md", "规则").
		addFile("/repo/CLAUDE.md", "规则")

	set, ok, err := LoadBaselineSet(t.Context(), fsys, testConfig(), "/repo", "", false)

	if err != nil || !ok {
		t.Fatalf("装基线不该失败：ok=%v err=%v", ok, err)
	}
	requirePaths(t, set.Observed, "AGENTS.md", "CLAUDE.md")
	requirePaths(t, set.Included, "AGENTS.md")
}

func TestLoadBaselineSet预算关着时没有基线(t *testing.T) {
	fsys := newFakeFS().addDir("/repo/.git").addFile("/repo/AGENTS.md", "规则")

	for _, config := range []ResolvedConfig{
		Config{MaxBytes: 0}.Resolve(),
		Config{MaxBytes: 1 << 20, MaxSourceBytes: -1}.Resolve(),
	} {
		_, ok, err := LoadBaselineSet(t.Context(), fsys, config, "/repo", "", false)
		if err != nil {
			t.Fatalf("不该报错：%v", err)
		}
		if ok {
			t.Fatal("上限关着的时候应当是「这一次没有基线」")
		}
	}
}

// 「没有基线」和「一份空基线」是两件事：后者会明确告诉模型先前那些全部作废了。
func TestLoadBaselineSet一份都没找到时看要不要发空替换(t *testing.T) {
	fsys := newFakeFS().addDir("/repo/.git")

	if _, ok, err := LoadBaselineSet(t.Context(), fsys, testConfig(), "/repo", "", false); ok || err != nil {
		t.Fatalf("不要求替换时应当是「没有基线」：ok=%v err=%v", ok, err)
	}

	set, ok, err := LoadBaselineSet(t.Context(), fsys, testConfig(), "/repo", "", true)
	if err != nil || !ok {
		t.Fatalf("要求替换时应当产出一份空基线：ok=%v err=%v", ok, err)
	}
	requireContains(t, set.Rendered.Text, emptyReplacementWorkspaceContextIntro)
	if len(set.Included) != 0 {
		t.Fatalf("空基线里不该有文件：%v", set.Included)
	}
}

func TestLoadBaselineSet元数据报的大小超上限就整份不读(t *testing.T) {
	fsys := newFakeFS().
		addDir("/repo/.git").
		addFile("/repo/AGENTS.md", strings.Repeat("a", 100)).
		addFile("/repo/CLAUDE.md", "短的")
	config := Config{MaxBytes: 1 << 20, MaxSourceBytes: 50}.Resolve()

	set, ok, err := LoadBaselineSet(t.Context(), fsys, config, "/repo", "", false)

	if err != nil || !ok {
		t.Fatalf("装基线不该失败：ok=%v err=%v", ok, err)
	}
	requirePaths(t, set.Observed, "CLAUDE.md")
}

// 元数据里的大小可能报不出来，也可能是陈旧的，所以流式读的过程里还要再数一遍字节。
// 少了这一道，就等于让一个无界大的文件进内存。
func TestLoadBaselineSet大小不可信时靠流里数字节兜住(t *testing.T) {
	for _, name := range []string{"报不出大小", "报了一个小的假大小"} {
		t.Run(name, func(t *testing.T) {
			fsys := newFakeFS().
				addDir("/repo/.git").
				addFile("/repo/AGENTS.md", strings.Repeat("a", 100))
			if name == "报不出大小" {
				fsys.hideSize("/repo/AGENTS.md")
			} else {
				fsys.fakeSize("/repo/AGENTS.md", 1)
			}
			fsys.chunkSize = 10
			config := Config{MaxBytes: 1 << 20, MaxSourceBytes: 50}.Resolve()

			_, ok, err := LoadBaselineSet(t.Context(), fsys, config, "/repo", "", false)

			if err != nil {
				t.Fatalf("不该报错：%v", err)
			}
			if ok {
				t.Fatal("这个文件应当被整份丢掉，于是一份基线都装不出来")
			}
		})
	}
}

// 一次流式读可以读了一半才失败。整份丢掉而不是把读到的那半交出去：
// 半份指令看上去是成功的，然后会被当成完整内容去算摘要、去比对。
func TestLoadBaselineSet流读到一半失败时整份丢掉(t *testing.T) {
	fsys := newFakeFS().
		addDir("/repo/.git").
		addFile("/repo/AGENTS.md", "前半段后半段").
		addFile("/repo/CLAUDE.md", "好好的一份")
	fsys.chunkSize = 3
	fsys.failStreamAfter("/repo/AGENTS.md", 1, errFake)

	set, ok, err := LoadBaselineSet(t.Context(), fsys, testConfig(), "/repo", "", false)

	if err != nil || !ok {
		t.Fatalf("装基线不该失败：ok=%v err=%v", ok, err)
	}
	requirePaths(t, set.Observed, "CLAUDE.md")
	requireNotContains(t, set.Rendered.Text, "前半段")
}

// 元数据探过之后文件可能就没了，或者变得读不了了：那只丢这一个候选。
func TestLoadBaselineSet流整个开不起来时只丢这一个(t *testing.T) {
	fsys := newFakeFS().
		addDir("/repo/.git").
		addFile("/repo/AGENTS.md", "读不了的").
		addFile("/repo/CLAUDE.md", "读得了的").
		failStream("/repo/AGENTS.md", errFake)

	set, ok, err := LoadBaselineSet(t.Context(), fsys, testConfig(), "/repo", "", false)

	if err != nil || !ok {
		t.Fatalf("装基线不该失败：ok=%v err=%v", ok, err)
	}
	requirePaths(t, set.Observed, "CLAUDE.md")
}

// 流读到一半时调用方撤了：这和「读到一半失败」不是一回事，取消要一路报上去，
// 不能悄悄退成「这个文件没读出来」。
func TestLoadBaselineSet流中途被取消时原样报错(t *testing.T) {
	for name, content := range map[string]string{
		"后面还有块要读": "前半段后半段",
		// 正好一块就读完了：循环自己走到头，得靠循环**之后**那一次检查兜住。
		"这一块就是最后一块": "abc",
	} {
		t.Run(name, func(t *testing.T) {
			fsys := newFakeFS().addDir("/repo/.git").addFile("/repo/AGENTS.md", content)
			fsys.chunkSize = 3
			ctx, cancel := context.WithCancel(t.Context())
			fsys.cancelAfterFirstChunk("/repo/AGENTS.md", cancel)

			_, _, err := LoadBaselineSet(ctx, fsys, testConfig(), "/repo", "/repo", false)

			if !errors.Is(err, context.Canceled) {
				t.Fatalf("取消必须原样报出来，实际是 %v", err)
			}
		})
	}
}

func TestLoadBaseline只要渲染结果(t *testing.T) {
	fsys := newFakeFS().addDir("/repo/.git").addFile("/repo/AGENTS.md", "规则")

	rendered, ok, err := LoadBaseline(t.Context(), fsys, testConfig(), "/repo", "", false)

	if err != nil || !ok {
		t.Fatalf("装基线不该失败：ok=%v err=%v", ok, err)
	}
	requireContains(t, rendered.Text, "规则")
}

func TestLoadBaseline取消不降级(t *testing.T) {
	fsys := newFakeFS().addDir("/repo/.git").addFile("/repo/AGENTS.md", "规则")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, _, err := LoadBaseline(ctx, fsys, testConfig(), "/repo", "/repo", false); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消必须原样报出来，实际是 %v", err)
	}
}

func TestProbeScopeInstruction三种结果各自的样子(t *testing.T) {
	fsys := newFakeFS().
		addFile("/repo/sub/AGENTS.md", "子规则").
		failStat("/repo/other/AGENTS.md", errFake).
		addFile("/repo/other/AGENTS.md", "探不到的")

	present, err := ProbeScopeInstruction(t.Context(), fsys, testConfig(),
		CandidateScopeKey("sub", "AGENTS.md"), "/repo")
	if err != nil {
		t.Fatalf("探测不该失败：%v", err)
	}
	if present.Kind != ProbePresent {
		t.Fatalf("这条作用域上有文件，应当是在场，实际是 %v", present.Kind)
	}
	if present.File.DisplayPath != "sub/AGENTS.md" {
		t.Fatalf("显示路径应当相对项目根，实际是 %s", present.File.DisplayPath)
	}
	if present.File.AbsolutePath != "/repo/sub/AGENTS.md" {
		t.Fatalf("绝对路径不对：%s", present.File.AbsolutePath)
	}

	absent, err := ProbeScopeInstruction(t.Context(), fsys, testConfig(),
		CandidateScopeKey("sub", "CLAUDE.md"), "/repo")
	if err != nil || absent.Kind != ProbeAbsent {
		t.Fatalf("这条作用域上没有文件，应当是确认不在，实际是 %v（err=%v）", absent.Kind, err)
	}

	unavailable, err := ProbeScopeInstruction(t.Context(), fsys, testConfig(),
		CandidateScopeKey("other", "AGENTS.md"), "/repo")
	if err != nil || unavailable.Kind != ProbeUnavailable {
		t.Fatalf("提供方报错时应当是问不出来，实际是 %v（err=%v）", unavailable.Kind, err)
	}
}

func TestProbeScopeInstruction根作用域用点表示(t *testing.T) {
	fsys := newFakeFS().addFile("/repo/AGENTS.md", "根规则")

	probe, err := ProbeScopeInstruction(t.Context(), fsys, testConfig(),
		CandidateScopeKey(".", "AGENTS.md"), "/repo")

	if err != nil || probe.Kind != ProbePresent {
		t.Fatalf("根作用域应当探得到：%v（err=%v）", probe.Kind, err)
	}
	if probe.File.DisplayPath != "AGENTS.md" {
		t.Fatalf("根作用域的显示路径不该带目录，实际是 %s", probe.File.DisplayPath)
	}
}

func TestProbeScopeInstruction用户全局的显示路径是那条常数(t *testing.T) {
	fsys := newFakeFS().addFile("/home/.dsh/AGENTS.md", "全局规则")
	config := Config{MaxBytes: 1 << 20, UserGlobalRoot: "/home/.dsh"}.Resolve()

	probe, err := ProbeScopeInstruction(t.Context(), fsys, config,
		CandidateScopeKey(UserGlobalDirectory, UserGlobalFile), "/repo")

	if err != nil || probe.Kind != ProbePresent {
		t.Fatalf("用户全局应当探得到：%v（err=%v）", probe.Kind, err)
	}
	if probe.File.DisplayPath != UserGlobalDisplayPath {
		t.Fatalf("显示路径应当是那条常数，实际是 %s", probe.File.DisplayPath)
	}
}

// 用户全局那一层关着时，它下面的作用域一律确认不在——不这么判的话下面会拼出
// 一条以世界根为基准的路径，探到别的东西上去。
func TestProbeScopeInstruction用户全局关着时确认不在(t *testing.T) {
	fsys := newFakeFS().addFile("/AGENTS.md", "世界根上的东西")

	probe, err := ProbeScopeInstruction(t.Context(), fsys, testConfig(),
		CandidateScopeKey(UserGlobalDirectory, UserGlobalFile), "/repo")

	if err != nil || probe.Kind != ProbeAbsent {
		t.Fatalf("应当确认不在，实际是 %v（err=%v）", probe.Kind, err)
	}
	if fsys.resolveCalls["/AGENTS.md"] != 0 {
		t.Fatal("不该去探世界根上的那条路径")
	}
}

func TestProbeScopeInstructionResolve故障也算问不出来(t *testing.T) {
	fsys := newFakeFS().addFile("/repo/AGENTS.md", "规则").failResolve("/repo/AGENTS.md", errFake)

	probe, err := ProbeScopeInstruction(t.Context(), fsys, testConfig(),
		CandidateScopeKey(".", "AGENTS.md"), "/repo")

	if err != nil || probe.Kind != ProbeUnavailable {
		t.Fatalf("应当是问不出来，实际是 %v（err=%v）", probe.Kind, err)
	}
}

func TestProbeScopeInstruction取消不降级(t *testing.T) {
	fsys := newFakeFS().addFile("/repo/AGENTS.md", "规则")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := ProbeScopeInstruction(ctx, fsys, testConfig(), CandidateScopeKey(".", "AGENTS.md"), "/repo")

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消必须原样报出来，实际是 %v", err)
	}
}

func TestReadScopeInstruction读得出和读不出(t *testing.T) {
	fsys := newFakeFS().addFile("/repo/AGENTS.md", "规则内容")
	probe, err := ProbeScopeInstruction(t.Context(), fsys, testConfig(),
		CandidateScopeKey(".", "AGENTS.md"), "/repo")
	if err != nil {
		t.Fatalf("探测不该失败：%v", err)
	}

	file, ok, err := ReadScopeInstruction(t.Context(), fsys, probe.File, 1<<20)
	if err != nil || !ok {
		t.Fatalf("应当读得出来：ok=%v err=%v", ok, err)
	}
	if file.Content != "规则内容" {
		t.Fatalf("内容不对：%q", file.Content)
	}
	if file.DisplayPath != "AGENTS.md" || file.AbsolutePath != "/repo/AGENTS.md" {
		t.Fatalf("两条路径应当从探测结果原样带过来：%s %s", file.DisplayPath, file.AbsolutePath)
	}
	if file.Version == "" {
		t.Fatal("版本应当从探测结果原样带过来")
	}

	if _, ok, err := ReadScopeInstruction(t.Context(), fsys, probe.File, 1); ok || err != nil {
		t.Fatalf("超了单文件上限时应当读不出来：ok=%v err=%v", ok, err)
	}
}

// 空串不是一个合法的 [fs.Version]，所以它可以安全地当「没有版本」用。
func TestLoadedFile空版本表示没有(t *testing.T) {
	var file LoadedFile
	if file.Version != fs.Version("") {
		t.Fatal("零值就该是那个「没有」")
	}
}
