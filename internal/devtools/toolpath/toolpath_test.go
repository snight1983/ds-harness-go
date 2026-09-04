package toolpath

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeModule 在 dir 下写一份 go.mod，module 行是 modulePath。
func writeModule(t *testing.T, dir, modulePath string) {
	t.Helper()

	body := "module " + modulePath + "\n\ngo 1.25.0\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(body), 0o600); err != nil {
		t.Fatalf("写 go.mod 失败：%v", err)
	}
}

func TestRepoRootFindsTheModuleFromANestedDirectory(t *testing.T) {
	root := t.TempDir()
	writeModule(t, root, ModulePath)

	nested := filepath.Join(root, "core", "session")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatalf("建子目录失败：%v", err)
	}

	found, err := repoRootFrom(nested)
	if err != nil {
		t.Fatalf("本该从子目录找到仓库根：%v", err)
	}
	// t.TempDir 在 macOS 上会给出 /var 这种符号链接路径，比较前先各自求值。
	if !sameDir(t, found, root) {
		t.Errorf("找到的根是 %s，期望 %s", found, root)
	}
}

// 只认文件名的话，工具会在任何一个恰好有 go.mod 的目录里「成功」，然后拿着
// 错误的根去扫源码。这个用例把一个别的模块摆在更近的位置，确认它被跳过。
func TestRepoRootSkipsAForeignModuleAndKeepsWalkingUp(t *testing.T) {
	root := t.TempDir()
	writeModule(t, root, ModulePath)

	foreign := filepath.Join(root, "vendorish")
	if err := os.MkdirAll(foreign, 0o750); err != nil {
		t.Fatalf("建子目录失败：%v", err)
	}
	writeModule(t, foreign, "example.com/someone/else")

	found, err := repoRootFrom(foreign)
	if err != nil {
		t.Fatalf("本该跳过别人的模块继续往上找：%v", err)
	}
	if !sameDir(t, found, root) {
		t.Errorf("找到的根是 %s，期望 %s（不该停在 %s）", found, root, foreign)
	}
}

func TestRepoRootReportsWhenThereIsNoModuleAnywhereAbove(t *testing.T) {
	orphan := t.TempDir()

	_, err := repoRootFrom(orphan)
	if !errors.Is(err, ErrNoModuleRoot) {
		t.Fatalf("本该报 ErrNoModuleRoot，实际 %v", err)
	}
	// 错误里要带上出发点，否则「没找到」这句话没法排查。
	if !strings.Contains(err.Error(), orphan) {
		t.Errorf("错误里应当带上出发目录 %s，实际 %q", orphan, err.Error())
	}
}

func TestDSHRootPrefersTheEnvironmentVariable(t *testing.T) {
	snapshot := t.TempDir()
	t.Setenv(DSHRootEnv, snapshot)

	root, exists := DSHRoot()
	if root != snapshot {
		t.Errorf("该用环境变量给的 %s，实际 %s", snapshot, root)
	}
	if !exists {
		t.Error("目录是真的，exists 该是 true")
	}
}

func TestDSHRootReportsAMissingSnapshotInsteadOfPretending(t *testing.T) {
	t.Setenv(DSHRootEnv, filepath.Join(t.TempDir(), "并不存在"))

	if _, exists := DSHRoot(); exists {
		t.Error("目录不存在时 exists 必须是 false——否则调用方会拿着一个空目录去验溯源")
	}
}

// 一个存在的**文件**不是快照根。不查 IsDir 的话，误把文件路径设进环境变量会
// 一路走到扫描阶段才炸，而且报的是别的错。
func TestDSHRootRejectsAFileThatIsNotADirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "snapshot.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("写文件失败：%v", err)
	}
	t.Setenv(DSHRootEnv, file)

	if _, exists := DSHRoot(); exists {
		t.Error("普通文件不该被当成快照根")
	}
	if _, err := RequireDSHRoot(""); err == nil {
		t.Error("RequireDSHRoot 对普通文件该报错")
	}
}

func TestRequireDSHRootAcceptsAnExplicitOverride(t *testing.T) {
	t.Setenv(DSHRootEnv, filepath.Join(t.TempDir(), "并不存在"))
	override := t.TempDir()

	root, err := RequireDSHRoot(override)
	if err != nil {
		t.Fatalf("显式传进来的目录是真的，不该报错：%v", err)
	}
	if root != override {
		t.Errorf("该用 %s，实际 %s", override, root)
	}
}

func TestRequireDSHRootErrorNamesTheEnvironmentVariable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "并不存在")

	_, err := RequireDSHRoot(missing)
	if err == nil {
		t.Fatal("目录不存在时该报错")
	}
	// 错误信息要能自己带人走出去：说清楚设哪个环境变量。
	if !strings.Contains(err.Error(), DSHRootEnv) {
		t.Errorf("错误里该提到 %s，实际 %q", DSHRootEnv, err.Error())
	}
}

func TestResolveFallsBackOnlyWhenTheValueIsEmpty(t *testing.T) {
	if got := Resolve("", "兜底"); got != "兜底" {
		t.Errorf("空值该走兜底，实际 %q", got)
	}
	if got := Resolve("显式", "兜底"); got != "显式" {
		t.Errorf("显式值该原样返回，实际 %q", got)
	}
}

func TestPortmapFileHangsOffTheRepoRoot(t *testing.T) {
	got, err := PortmapFile("portmap.tsv")
	if err != nil {
		t.Fatalf("在仓库内跑，本该找得到根：%v", err)
	}
	want := filepath.Join("docs", "portmap", "portmap.tsv")
	if !strings.HasSuffix(got, want) {
		t.Errorf("路径该以 %s 结尾，实际 %s", want, got)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("该给绝对路径，实际 %s", got)
	}
}

// sameDir 比较两个目录是否指向同一处，先各自求符号链接。
func sameDir(t *testing.T, left, right string) bool {
	t.Helper()

	resolve := func(path string) string {
		if evaluated, err := filepath.EvalSymlinks(path); err == nil {
			return evaluated
		}
		return path
	}
	return resolve(left) == resolve(right)
}
