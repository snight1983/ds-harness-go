// 本文件的作用：钉住一次装载读进内存的**总量**上限——缺省值不改变现有行为、
// 超限时整次装载失败而不是悄悄少读几份、那道上限关得掉，以及它不进基线身份串。
//
// 这道上限是本仓库自有的（见 [Config.MaxTotalSourceBytes]）。上游只有每个文件
// 那一道，而一次发现能扫出多少个文件由工作区的形状决定：cwd 到项目根之间有多少
// 层目录、每层有多少个候选名在场。八份各自都在每文件上限之内的文件加起来是八份的
// 量，而这份量会被完整拿在手上直到渲染那一步才裁掉。
//
// 最容易错的一处是「超了就少读几份」：那样产出的是一份**看上去完整**的基线，
// 然后它会被拿去算摘要、和上一份比对，于是丢掉的那几份指令表现成「被删掉了」。
// 所以这里要验的是超限报错，不是超限截断。

package instructions

import (
	"errors"
	"strings"
	"testing"
)

func TestResolve给总量上限补默认值(t *testing.T) {
	if got := (Config{MaxBytes: 1 << 20}).Resolve().MaxTotalSourceBytes; got != DefaultMaxTotalSourceBytes {
		t.Fatalf("留零应当补成 %d，实际是 %d", DefaultMaxTotalSourceBytes, got)
	}
	if got := (Config{MaxBytes: 1 << 20, MaxTotalSourceBytes: 4096}).Resolve().MaxTotalSourceBytes; got != 4096 {
		t.Fatalf("显式给的值应当原样留下，实际是 %d", got)
	}
	// 负数是「明着关掉这一层」，不能被当成没填而补上默认值。
	if got := (Config{MaxBytes: 1 << 20, MaxTotalSourceBytes: -1}).Resolve().MaxTotalSourceBytes; got != -1 {
		t.Fatalf("负数应当原样留下，实际是 %d", got)
	}
}

// 总量上限是一道资源闸，不是「这份基线按什么口径产出」的一部分。它要是进了身份串，
// 运维调一下这个数就会让所有在途会话的基线判成不兼容。
func TestWorkspaceBaselineIdentity不受总量上限影响(t *testing.T) {
	lenient := Config{MaxBytes: 1 << 20, MaxTotalSourceBytes: 1 << 20}.Resolve()
	strict := Config{MaxBytes: 1 << 20, MaxTotalSourceBytes: 4096}.Resolve()

	if a, b := WorkspaceBaselineIdentity(lenient, "/repo", "/repo"),
		WorkspaceBaselineIdentity(strict, "/repo", "/repo"); a != b {
		t.Fatalf("身份串不该因为总量上限而不同：\n%s\n%s", a, b)
	}
}

func TestLoadBaselineSet缺省总量上限放得过真实工作区(t *testing.T) {
	fsys := newFakeFS().
		addDir("/repo/.git").
		addFile("/repo/AGENTS.md", strings.Repeat("根", 2000)).
		addFile("/repo/sub/AGENTS.md", strings.Repeat("子", 2000))

	set, ok, err := LoadBaselineSet(t.Context(), fsys, testConfig(), "/repo/sub", "", false)

	if err != nil || !ok {
		t.Fatalf("缺省上限下这两份文件该装得动：ok=%v err=%v", ok, err)
	}
	requirePaths(t, set.Observed, "AGENTS.md", "sub/AGENTS.md")
}

// 每一份都在每文件上限之内，加起来才超。这一条分得开两道上限：
// 每文件那道要是没被绕过，这里根本不会失败。
func TestLoadBaselineSet总量超了就整次失败(t *testing.T) {
	fsys := newFakeFS().
		addDir("/repo/.git").
		addFile("/repo/AGENTS.md", strings.Repeat("a", 400)).
		addFile("/repo/sub/AGENTS.md", strings.Repeat("b", 400))
	config := Config{MaxBytes: 1 << 20, MaxSourceBytes: 1000, MaxTotalSourceBytes: 600}.Resolve()

	set, ok, err := LoadBaselineSet(t.Context(), fsys, config, "/repo/sub", "", false)

	if !errors.Is(err, ErrInstructionsTooLarge) {
		t.Fatalf("应当报 ErrInstructionsTooLarge，实际是 %v", err)
	}
	if ok {
		t.Fatal("失败的那一次不该同时说「有基线」")
	}
	// 不能交出一份少了几个文件的基线：它和一份完整基线长得一样。
	if len(set.Observed) != 0 || set.Rendered.Text != "" {
		t.Fatalf("失败时不该交出半份基线：%+v", set)
	}
	// 报得出是在哪一份上超的，不然运维只知道「太大了」。
	if !strings.Contains(err.Error(), "sub/AGENTS.md") {
		t.Fatalf("这句话里应当点出是哪一份文件：%v", err)
	}
}

func TestLoadBaselineSet总量上限关得掉(t *testing.T) {
	fsys := newFakeFS().
		addDir("/repo/.git").
		addFile("/repo/AGENTS.md", strings.Repeat("a", 400)).
		addFile("/repo/sub/AGENTS.md", strings.Repeat("b", 400))
	config := Config{MaxBytes: 1 << 20, MaxSourceBytes: 1000, MaxTotalSourceBytes: -1}.Resolve()

	set, ok, err := LoadBaselineSet(t.Context(), fsys, config, "/repo/sub", "", false)

	if err != nil || !ok {
		t.Fatalf("关掉之后这两份该装得动：ok=%v err=%v", ok, err)
	}
	requirePaths(t, set.Observed, "AGENTS.md", "sub/AGENTS.md")
}

// 一份被每文件上限跳过的文件不占总量额度：它根本没进内存。
func TestLoadBaselineSet跳过的那份不占总量额度(t *testing.T) {
	fsys := newFakeFS().
		addDir("/repo/.git").
		addFile("/repo/AGENTS.md", strings.Repeat("a", 400)).
		addFile("/repo/sub/AGENTS.md", strings.Repeat("b", 40))
	// 每文件上限 100：根那份超了，被跳过；总量 100 只够装子那份。
	config := Config{MaxBytes: 1 << 20, MaxSourceBytes: 100, MaxTotalSourceBytes: 100}.Resolve()

	set, ok, err := LoadBaselineSet(t.Context(), fsys, config, "/repo/sub", "", false)

	if err != nil || !ok {
		t.Fatalf("被跳过的那份不该吃掉总量额度：ok=%v err=%v", ok, err)
	}
	requirePaths(t, set.Observed, "sub/AGENTS.md")
}

func TestReconcile总量超了就整次失败(t *testing.T) {
	fixture := newReconcileFixture(t)
	fixture.fsys.
		addFile("/repo/AGENTS.md", strings.Repeat("a", 400)).
		addFile("/repo/sub/AGENTS.md", strings.Repeat("b", 400))
	fixture.config = Config{MaxBytes: 1 << 20, MaxSourceBytes: 1000, MaxTotalSourceBytes: 600}.Resolve()

	_, ok, err := Reconcile(t.Context(), fixture.fsys, fixture.config, ReconcileRequest{
		WorkspaceRoot:         "/repo",
		ProjectRoot:           "/repo",
		Versions:              fixture.versions,
		TouchedPaths:          []string{"/repo/sub/file.go"},
		IncludeBaselineScopes: true,
	})

	if !errors.Is(err, ErrInstructionsTooLarge) {
		t.Fatalf("应当报 ErrInstructionsTooLarge，实际是 %v", err)
	}
	if ok {
		t.Fatal("失败的那一次不该同时说「有东西要发」")
	}
}

// 缓存命中的那份没有被重新读进内存，所以它不该吃这一趟的额度。
// 反过来说：要是记账记在了命中那条路上，一个大文件会让每一趟对账都超限，
// 哪怕它一个字节都没变过。
func TestReconcile缓存命中的那份不占总量额度(t *testing.T) {
	big := strings.Repeat("a", 400)
	small := strings.Repeat("b", 40)
	fixture := newReconcileFixture(t)
	fixture.fsys.
		addFile("/repo/AGENTS.md", big).
		addFile("/repo/sub/AGENTS.md", small)
	previous := fixture.seed("AGENTS.md", big)
	// 额度只够那份小的；根那份要是也记账，这一趟就超了。
	fixture.config = Config{MaxBytes: 1 << 20, MaxSourceBytes: 1000, MaxTotalSourceBytes: 100}.Resolve()

	result, ok, err := Reconcile(t.Context(), fixture.fsys, fixture.config, ReconcileRequest{
		WorkspaceRoot:         "/repo",
		ProjectRoot:           "/repo",
		Versions:              fixture.versions,
		Effective:             []Change{previous},
		TouchedPaths:          []string{"/repo/sub/file.go"},
		IncludeBaselineScopes: true,
	})

	if err != nil {
		t.Fatalf("对账不该失败：%v", err)
	}
	if !ok {
		t.Fatal("新出现的那份指令应当被发出去")
	}
	if len(result.Changes) != 1 || result.Changes[0].Path != "sub/AGENTS.md" {
		t.Fatalf("应当正好发那一条新的：%+v", result.Changes)
	}
}
