// 本文件的作用：把 [agentpresets.Store] 接到一棵真的本地目录树上。
//
// 新增: 这个包是 agentpresets 从直接 `os.*` 里拆出来那一步的落点。依赖方向是反的
// ——本包认识 [agentpresets.Store]，agentpresets 不认识本包——所以换一份介质
// （对象存储、数据库）是**加一个这样的适配层**，不是改业务包。

package localdir

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/snight1983/ds-harness-go/preset/agentpresets"
)

// Store 是一棵本地目录树上的预设存储。
//
// 它不持有任何东西：每一次调用都是一趟当场的系统调用，没有缓存、没有句柄、
// 没有后台协程，所以也不需要关。
type Store struct{}

// New 造一个落在本地文件系统上的预设存储。
//
// 交进来的路径按 [agentpresets.Store] 那条约定是**斜杠分隔**的，本包在每一次
// 系统调用之前用 [path/filepath.FromSlash] 翻成本机的样子。Windows 上这一步是
// 必需的：`C:/根/预设` 这种混着来的路径 [os.ReadFile] 虽然收得下，
// [path/filepath.Clean] 那一族却不会把它规范成同一个字符串，于是装配方交进来的根
// 和本包拼出来的孩子会比不相等。
func New() *Store { return &Store{} }

var _ agentpresets.Store = (*Store)(nil)

// entryOf 把一次 stat 的结果折成这道缝上的那份元数据。
//
// 戳用「修改时间的纳秒数 + 大小」拼。它只需要可比、不需要可解释（见
// [agentpresets.Entry.Stamp]），而这两个数正是上游拿来判「这份组合换没换过」
// 的那一对。
func entryOf(name string, info os.FileInfo) agentpresets.Entry {
	mode := info.Mode()
	entry := agentpresets.Entry{
		Name:    name,
		Dir:     mode.IsDir(),
		Regular: mode.IsRegular(),
	}
	if mode.IsRegular() {
		entry.Executable = mode.Perm()&0o100 != 0
		entry.Stamp = strconv.FormatInt(info.ModTime().UnixNano(), 10) +
			":" + strconv.FormatInt(info.Size(), 10)
	}
	return entry
}

// Stat 看一条路径上是什么。
//
// 走 [os.Stat] 而不是 [os.Lstat]：**顺链取实体**，于是一个指向目录的符号链接
// 按它指的那个目录算。这是上游的语义，也是「副本自成一体」那条要的。
// 断链在这里如实报成「不存在」——它确实给不出任何内容。
func (s *Store) Stat(_ context.Context, name string) (agentpresets.Entry, bool, error) {
	info, err := os.Stat(filepath.FromSlash(name))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return agentpresets.Entry{}, false, nil
		}
		// 权限、路径中间有一段不是目录、介质掉了：这些都不是「不存在」，
		// 报上去让调用方分得开「这个目录还没建」和「这套部署配错了」。
		return agentpresets.Entry{}, false, fmt.Errorf("localdir: 看不了 %s：%w", name, err)
	}
	return entryOf(filepath.Base(name), info), true, nil
}

// List 列出一个目录的直接子项。
//
// [os.ReadDir] 已经按名字升序，正好是这道缝要的次序，而且它交出来的类型说的是
// **这一行自己**——一个指向目录的符号链接在这里 IsDir 为假。这正是 [agentpresets.Child]
// 要的语义，所以这里不再逐个 stat：那既多一趟系统调用，又会把不跟链接这件事悄悄改掉。
func (s *Store) List(_ context.Context, dir string) ([]agentpresets.Child, bool, error) {
	children, err := os.ReadDir(filepath.FromSlash(dir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("localdir: 读不了目录 %s：%w", dir, err)
	}
	rows := make([]agentpresets.Child, 0, len(children))
	for _, child := range children {
		rows = append(rows, agentpresets.Child{Name: child.Name(), Dir: child.IsDir()})
	}
	return rows, true, nil
}

// ReadFile 读出整份内容。
func (s *Store) ReadFile(_ context.Context, name string) ([]byte, error) {
	content, err := os.ReadFile(filepath.FromSlash(name))
	if err != nil {
		return nil, fmt.Errorf("localdir: 读不了 %s：%w", name, err)
	}
	return content, nil
}

// WriteFile 写下整份内容。
//
// 权限收成只给属主：随部署发出去的预设在它的安装里通常是所有人可读，而一份
// 本地创作出来的副本和它旁边那份设置文档是同一个分量，所以组和其他人的位一律剥掉。
// 属主的执行位按 executable 留——一份预设可以带可执行的辅助脚本。
func (s *Store) WriteFile(_ context.Context, name string, content []byte, executable bool) error {
	mode := os.FileMode(0o600)
	if executable {
		mode = 0o700
	}
	local := filepath.FromSlash(name)
	if err := os.WriteFile(local, content, mode); err != nil {
		return fmt.Errorf("localdir: 写不了 %s：%w", name, err)
	}
	// os.WriteFile 只在**新建**时套用这个权限；路径上已经有一份时它保留原来那份。
	// 覆盖一份所有人可读的文件时不补这一下，收权限就悄悄没做。
	if err := os.Chmod(local, mode); err != nil {
		return fmt.Errorf("localdir: 收不紧 %s 的权限：%w", name, err)
	}
	return nil
}

// MakeDir 建出一个目录，连同缺掉的上级。
func (s *Store) MakeDir(_ context.Context, dir string) error {
	if err := os.MkdirAll(filepath.FromSlash(dir), 0o700); err != nil {
		return fmt.Errorf("localdir: 建不了目录 %s：%w", dir, err)
	}
	return nil
}

// Remove 删掉单个条目；不在不算错。
func (s *Store) Remove(_ context.Context, name string) error {
	if err := os.Remove(filepath.FromSlash(name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("localdir: 删不掉 %s：%w", name, err)
	}
	return nil
}

// RemoveTree 删掉一棵子树；不在不算错（[os.RemoveAll] 本来就这样）。
func (s *Store) RemoveTree(_ context.Context, dir string) error {
	if err := os.RemoveAll(filepath.FromSlash(dir)); err != nil {
		return fmt.Errorf("localdir: 删不掉 %s：%w", dir, err)
	}
	return nil
}
