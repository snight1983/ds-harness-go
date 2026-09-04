// Package toolpath 给门禁工具定位它们要读写的目录。
//
// 新增: 四个门禁工具原先把 `C:\code\ds-harness-go\...` 和
// `C:\codestudy\deepseek-harness-dsh-v0.1.2-alpha.3` 直接写进 flag 默认值里，
// 后果是这套门禁只在写下这些路径的那台机器上成立——换一台开发机、换一个
// 检出目录、或者放到 CI 上，每个工具都会去读一个不存在的路径，然后把
// 「路径错了」报成「移植漏了」。
//
// 这个包把两件事分开：仓库根从 go.mod 现场找，上游快照从环境变量拿。两者都
// 不再依赖任何一台具体机器的目录结构。
package toolpath

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ModulePath 是本仓库的 module path，用来确认找到的 go.mod 确实是这个仓库的，
// 而不是碰巧撞上的某个上层模块。
const ModulePath = "github.com/snight1983/ds-harness-go"

// DSHRootEnv 是指向 DSH 上游快照的环境变量名。
//
// 新增: CI 和别人的开发机上不会有那份快照，路径也不可能一样，所以它必须能从
// 外面指定。
const DSHRootEnv = "DSH_ROOT"

// legacyDSHRoot 是原先写死的那个快照路径，留作 DSHRootEnv 没设时的兜底，
// 这样本机上的既有用法不受影响。
const legacyDSHRoot = `C:\codestudy\deepseek-harness-dsh-v0.1.2-alpha.3`

// ErrNoModuleRoot 表示从当前目录一路向上都没找到本仓库的 go.mod。
var ErrNoModuleRoot = errors.New("toolpath: 没找到 " + ModulePath + " 的 go.mod")

// RepoRoot 从当前工作目录向上走，找到本仓库的 go.mod 所在目录。
//
// 认的是 go.mod 里的 module 行，不是文件名。只认文件名的话，工具在任何一个
// 恰好有 go.mod 的目录里都会「成功」，然后拿着错误的根目录去扫源码，报出一
// 堆莫名其妙的缺失。
func RepoRoot() (string, error) {
	start, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("toolpath: 取当前目录失败：%w", err)
	}
	return repoRootFrom(start)
}

// repoRootFrom 是 RepoRoot 的可测版本。
func repoRootFrom(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("toolpath: 取绝对路径失败：%w", err)
	}

	for {
		candidate := filepath.Join(dir, "go.mod")
		if data, readErr := os.ReadFile(candidate); readErr == nil {
			if moduleLineMatches(data) {
				return dir, nil
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("%w（从 %s 向上找过）", ErrNoModuleRoot, start)
		}
		dir = parent
	}
}

// moduleLineMatches 判断这份 go.mod 的 module 行是不是本仓库。
func moduleLineMatches(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "module ") {
			continue
		}
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "module ")) == ModulePath
	}
	return false
}

// DSHRoot 返回 DSH 上游快照的根目录：先看环境变量，没设就用本机的老默认值。
//
// 第二个返回值说明这个路径当前存不存在。调用方据此决定是硬失败还是跳过溯源
// 校验——**但不能静默降级**，跳过必须打出来。
func DSHRoot() (root string, exists bool) {
	root = os.Getenv(DSHRootEnv)
	if root == "" {
		root = legacyDSHRoot
	}
	info, err := os.Stat(root)
	return root, err == nil && info.IsDir()
}

// PortmapFile 返回仓库根下 docs/portmap/<name> 的绝对路径。
//
// 移植账本的四个工具都往这一个目录读写，把「账本在哪」收在这里，省得每个工具
// 各写一遍自己的路径拼装。
func PortmapFile(name string) (string, error) {
	root, err := RepoRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "docs", "portmap", name), nil
}

// RequireDSHRoot 解析上游快照根目录，并确认它确实存在。
//
// 给那些「没有快照就一步都走不下去」的工具用（清单提取、能力提取、重锚）。
// 它们没有降级模式：读不到源码就产不出清单，硬失败比产出一份空清单安全。
func RequireDSHRoot(override string) (string, error) {
	root := override
	if root == "" {
		root, _ = DSHRoot()
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("找不到 DSH 源码根目录 %s：设 %s 环境变量指向快照，或者用 -root 指定", root, DSHRootEnv)
	}
	return root, nil
}

// Resolve 在 base 为空时返回 fallback，否则原样返回 base。
//
// 给 flag 默认值用：flag 留空表示「按仓库根推」，显式传了就听用户的。
func Resolve(base, fallback string) string {
	if base == "" {
		return fallback
	}
	return base
}
