// Package fstest 是一个内存里的 [fs.FileSystem]，供各包的测试当被试用。
//
// 源: packages/fs/fs/tests/service.spec.ts:22-83
//
// 它存在的理由和 DSH 那个 FakeFileSystem 一样——真正的原语在后端包里，
// [fs] 这条接缝自己只拥有那份抽象契约，所以验它需要一个刚好够用的实现。
// 「刚好够用」在这里的意思是：守卫、上限、单处匹配这几条**语义**都真的实现了，
// 因为它们正是契约里写着、而实现方最容易漏掉的部分。
//
// # 为什么它是一个可导入的包，而不是某个包里的 _test.go
//
// 新增: 它原来是 fs/fake_test.go，只有 [fs] 自己的用例看得见。把内容读写从两条缝
// （原 preset/agentpresets.Store）并成一条之后，需要一份**能真的读写**的假件的包
// 不止一个：[fs] 自己验契约、preset/agentpresets 验预设树的发现与复制。
// 让它们各写一份的话，两份必然慢慢长歪，而「这个后端到底答应了什么」正是这条接缝
// 的全部价值所在。
//
// 本仓库别处那些只实现两三个方法、其余一律 panic 的窄假件（见 workspace、
// context/instructions）不受影响，也不该换成这个：那种窄假件的价值恰恰在于
// 「本包偷偷用了别的方法」会当场炸出来，而这个包什么都答得上，炸不出来。
// 需要一棵**真能读写的内容树**时才用这个。
//
// # 这个假件模型的是一个有真目录的介质
//
// 新增: 它显式记着有哪些目录，于是一个空目录是存在的、列得出来的。生产后端
// [github.com/snight1983/ds-harness-go/adapter/objectstore.Store] 不是这样——那里键空间是平的，
// 目录靠「有没有别的键以它加斜杠开头」推断，空目录压根不存在。
//
// 两者不一致是有意的：接缝上没有哪一句话要求空目录不存在，那是对象存储的局限而不是
// 契约。让假件也照着装一遍，会把一条**后端特有**的局限伪装成消费方可以依赖的语义。
package fstest

import (
	"context"
	"fmt"
	"iter"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/snight1983/ds-harness-go/fs"
)

// chunkSize 是 StreamText 每块的字节数，故意取得很小，
// 好让一份几十字节的内容也走得出多块的路径。
const chunkSize = 4

// file 是内存后端存下来的一份内容加它的版本。
type file struct {
	content []byte
	version fs.Version
}

// FS 是一个内存文件系统，键就是它自己的 [fs.TargetKey]。
//
// 零值不能用，从 [New] 拿。它是并发安全的。
type FS struct {
	mutex sync.Mutex
	files map[fs.TargetKey]file
	// dirs 是显式建出来的目录。有孩子的键不必在这里也算目录，
	// 这张表存在只是为了让**空**目录也存在得下来。
	dirs map[fs.TargetKey]bool
	// links 是「这条路径是一个符号链接」的登记，只供 Lstat 用——
	// Resolve 会跟过去，所以目标那一侧永远看不见它们。
	links map[string]string
	// stamp 递增，用来造出每次写入都不一样的版本令牌。
	stamp int
}

// 编译期钉住：这份假件满足必答的那些原语，也满足可选那道接缝。
var (
	_ fs.FileSystem       = (*FS)(nil)
	_ fs.OSPathFileSystem = (*FS)(nil)
)

// New 造一个空的内存文件系统。
func New() *FS {
	return &FS{
		files: map[fs.TargetKey]file{},
		dirs:  map[fs.TargetKey]bool{},
		links: map[string]string{},
	}
}

// Seed 直接种一份文本进去，不走写入路径（写入路径本身是被验的东西之一）。
func (f *FS) Seed(key fs.TargetKey, content string) { f.SeedBytes(key, []byte(content)) }

// SeedBytes 直接种一份字节进去，语义同 [FS.Seed]。
func (f *FS) SeedBytes(key fs.TargetKey, content []byte) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.stamp++
	f.files[key] = file{content: content, version: fs.Version("v" + strconv.Itoa(f.stamp))}
}

// SeedLink 登记一条符号链接，只有 [FS.Lstat] 看得见它。
func (f *FS) SeedLink(path string, target string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.links[path] = target
}

// SeedDir 直接种一个空目录进去，不走 [FS.MakeDir]。
func (f *FS) SeedDir(key fs.TargetKey) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.markDirLocked(key)
}

// Keys 交出当前所有文件的键，按字典序，供用例断言「介质上还剩下什么」。
func (f *FS) Keys() []fs.TargetKey {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	keys := make([]fs.TargetKey, 0, len(f.files))
	for key := range f.files {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// markDirLocked 把一个键连同它的各级上级记成目录。调用方持锁。
func (f *FS) markDirLocked(key fs.TargetKey) {
	for current := string(key); current != "" && current != "/" && current != "."; {
		f.dirs[fs.TargetKey(current)] = true
		parent := path.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
}

func (f *FS) load(key fs.TargetKey) (file, bool) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	found, ok := f.files[key]
	return found, ok
}

// store 写下一份内容并盖一个新版本，返回新版本。
func (f *FS) store(key fs.TargetKey, content []byte) fs.Version {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.stamp++
	version := fs.Version("v" + strconv.Itoa(f.stamp))
	f.files[key] = file{content: content, version: version}
	f.markDirLocked(fs.TargetKey(path.Dir(string(key))))
	return version
}

// join 把 (路径, 基准) 拼成这个假件用的键，规则和 [FS.Resolve] 共用。
func join(target string, cwd string) string {
	if cwd != "" && !strings.HasPrefix(target, "/") {
		return cwd + "/" + target
	}
	return target
}

// Resolve 实现 [fs.FileSystem]。
func (f *FS) Resolve(_ context.Context, target string, cwd string) (fs.Target, error) {
	if target == "" {
		return fs.Target{}, &fs.Error{Code: fs.CodeNotFound, Message: "empty path"}
	}
	full := join(target, cwd)
	return fs.Target{TargetKey: fs.TargetKey(full), DisplayPath: full}, nil
}

// ProcessPath 实现 [fs.OSPathFileSystem]。
func (f *FS) ProcessPath(target fs.Target) string { return string(target.TargetKey) }

// FileURL 实现 [fs.OSPathFileSystem]。
func (f *FS) FileURL(target fs.Target) string {
	return "file:///" + url.PathEscape(string(target.TargetKey))
}

// Contains 实现 [fs.FileSystem]。
func (f *FS) Contains(parent fs.Target, child fs.Target) bool {
	if child.TargetKey == parent.TargetKey {
		return true
	}
	return strings.HasPrefix(string(child.TargetKey), string(parent.TargetKey)+"/")
}

// Stat 实现 [fs.FileSystem]。
func (f *FS) Stat(_ context.Context, target fs.Target) (fs.Info, bool, error) {
	if f.isDirectory(target.TargetKey) {
		return fs.Info{Version: fs.Version("dir"), Type: fs.TypeDirectory}, true, nil
	}
	found, ok := f.load(target.TargetKey)
	if !ok {
		return fs.Info{}, false, nil
	}
	size := int64(len(found.content))
	return fs.Info{Version: found.version, Type: fs.TypeFile, Size: &size}, true, nil
}

// Lstat 实现 [fs.FileSystem]。
func (f *FS) Lstat(_ context.Context, target string, cwd string) (fs.PathInfo, bool, error) {
	full := join(target, cwd)

	f.mutex.Lock()
	link, isLink := f.links[full]
	f.mutex.Unlock()
	if isLink {
		size := int64(len(link))
		return fs.PathInfo{Version: fs.Version("link"), Type: fs.TypeSymlink, Size: &size}, true, nil
	}

	if f.isDirectory(fs.TargetKey(full)) {
		return fs.PathInfo{Version: fs.Version("dir"), Type: fs.TypeDirectory}, true, nil
	}
	found, ok := f.load(fs.TargetKey(full))
	if !ok {
		return fs.PathInfo{}, false, nil
	}
	size := int64(len(found.content))
	return fs.PathInfo{Version: found.version, Type: fs.TypeFile, Size: &size}, true, nil
}

// ReadText 实现 [fs.FileSystem]。
func (f *FS) ReadText(_ context.Context, target fs.Target) (string, error) {
	found, ok := f.load(target.TargetKey)
	if !ok {
		return "", &fs.Error{Code: fs.CodeNotFound, Message: "not found: " + target.DisplayPath}
	}
	if strings.ContainsRune(string(found.content), 0) {
		return "", &fs.Error{Code: fs.CodeNotText, Message: "not text: " + target.DisplayPath}
	}
	return string(found.content), nil
}

// StreamText 实现 [fs.FileSystem]。
func (f *FS) StreamText(ctx context.Context, target fs.Target) (iter.Seq2[string, error], error) {
	content, err := f.ReadText(ctx, target)
	if err != nil {
		return nil, err
	}
	return func(yield func(string, error) bool) {
		for start := 0; start < len(content); start += chunkSize {
			// 块与块之间也认取消，见 [fs.FileSystem.StreamText]。
			if err := ctx.Err(); err != nil {
				yield("", &fs.Error{Code: fs.CodeAborted, Message: "aborted", Err: err})
				return
			}
			end := min(start+chunkSize, len(content))
			if !yield(content[start:end], nil) {
				return
			}
		}
	}, nil
}

// ReadBytes 实现 [fs.FileSystem]。
func (f *FS) ReadBytes(ctx context.Context, target fs.Target, maxBytes int64) ([]byte, error) {
	found, ok := f.load(target.TargetKey)
	if !ok {
		return nil, &fs.Error{Code: fs.CodeNotFound, Message: "not found: " + target.DisplayPath}
	}
	if int64(len(found.content)) > maxBytes {
		return nil, &fs.Error{Code: fs.CodeTooLarge, Message: "too large: " + target.DisplayPath}
	}
	if err := ctx.Err(); err != nil {
		return nil, &fs.Error{Code: fs.CodeAborted, Message: "aborted", Err: err}
	}
	return slices.Clone(found.content), nil
}

// ListDir 实现 [fs.FileSystem]。
func (f *FS) ListDir(_ context.Context, target fs.Target) ([]fs.DirEntry, error) {
	prefix := string(target.TargetKey) + "/"

	f.mutex.Lock()
	entries := []fs.DirEntry{}
	seen := map[string]bool{}
	for key, found := range f.files {
		name, ok := strings.CutPrefix(string(key), prefix)
		if !ok {
			continue
		}
		if head, _, nested := strings.Cut(name, "/"); nested {
			// 这份内容住在某个子目录里：那个子目录本身才是这一层的一行。
			if !seen[head] {
				seen[head] = true
				child := fs.TargetKey(prefix + head)
				entries = append(entries, fs.DirEntry{
					Name:   head,
					Type:   fs.TypeDirectory,
					Target: fs.Target{TargetKey: child, DisplayPath: string(child)},
				})
			}
			continue
		}
		seen[name] = true
		size := int64(len(found.content))
		entries = append(entries, fs.DirEntry{
			Name:    name,
			Type:    fs.TypeFile,
			Target:  fs.Target{TargetKey: key, DisplayPath: string(key)},
			Version: found.version,
			Size:    &size,
		})
	}
	for key := range f.dirs {
		name, ok := strings.CutPrefix(string(key), prefix)
		if !ok || strings.Contains(name, "/") || seen[name] {
			continue
		}
		seen[name] = true
		entries = append(entries, fs.DirEntry{
			Name:   name,
			Type:   fs.TypeDirectory,
			Target: fs.Target{TargetKey: key, DisplayPath: string(key)},
		})
	}
	known := f.dirs[target.TargetKey]
	f.mutex.Unlock()

	if len(entries) == 0 && !known {
		return nil, &fs.Error{Code: fs.CodeNotDirectory, Message: "not a directory: " + target.DisplayPath}
	}
	// 稳定的名字顺序是契约的一部分，见 [fs.FileSystem.ListDir]。
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name, b.Name) })
	return entries, nil
}

func (f *FS) isDirectory(key fs.TargetKey) bool {
	prefix := string(key) + "/"

	f.mutex.Lock()
	defer f.mutex.Unlock()
	if f.dirs[key] {
		return true
	}
	for candidate := range f.files {
		if strings.HasPrefix(string(candidate), prefix) {
			return true
		}
	}
	return false
}

// guard 把三种写意图折成一次前置条件判定，[FS.WriteText] 和 [FS.WriteBytes] 共用。
//
// 两条写路必须走同一份判定：分开写的话，「守卫在字节那条路上没接」这种故障
// 只有在有人专门为它写一条用例时才现得出形。
func (f *FS) guard(target fs.Target, expected fs.WriteIntent) (file, bool, error) {
	found, exists := f.load(target.TargetKey)

	switch intent := expected.(type) {
	case nil:
		// 无条件：不检查任何前置条件，但仍然是原子发布的。
	case fs.CreateIfAbsent:
		if exists {
			return file{}, false, &fs.Error{
				Code: fs.CodeNotObserved, Message: "already exists: " + target.DisplayPath,
			}
		}
	case fs.ReplaceIfVersion:
		if !exists || found.version != intent.Version {
			return file{}, false, &fs.Error{
				Code: fs.CodeStaleVersion, Message: "stale: " + target.DisplayPath,
			}
		}
	default:
		// [fs.WriteIntent] 是封印接口，fs 包外面造不出第三种；留着这一支是因为
		// 那边哪天真加了第四种成员，漏掉的分支必须当场炸而不是静默放行。
		return file{}, false, fmt.Errorf("fstest: 未知的写意图 %T", expected)
	}
	return found, exists, nil
}

// WriteText 实现 [fs.FileSystem]。
func (f *FS) WriteText(
	_ context.Context, target fs.Target, content string, expected fs.WriteIntent,
) (fs.WriteOutcome, error) {
	found, exists, err := f.guard(target, expected)
	if err != nil {
		return fs.WriteOutcome{}, err
	}

	operation := fs.OperationCreate
	var before *string
	if exists {
		operation = fs.OperationUpdate
		previous := string(found.content)
		before = &previous
	}
	return fs.WriteOutcome{
		Operation: operation,
		Version:   f.store(target.TargetKey, []byte(content)),
		Before:    before,
		After:     content,
	}, nil
}

// WriteBytes 实现 [fs.FileSystem]。
func (f *FS) WriteBytes(
	_ context.Context, target fs.Target, content []byte, expected fs.WriteIntent,
) (fs.Version, error) {
	if _, _, err := f.guard(target, expected); err != nil {
		return "", err
	}
	return f.store(target.TargetKey, slices.Clone(content)), nil
}

// MakeDir 实现 [fs.FileSystem]。
func (f *FS) MakeDir(_ context.Context, target string, cwd string) (fs.Target, error) {
	full := join(target, cwd)
	f.mutex.Lock()
	f.markDirLocked(fs.TargetKey(full))
	f.mutex.Unlock()
	return fs.Target{TargetKey: fs.TargetKey(full), DisplayPath: full}, nil
}

// Remove 实现 [fs.FileSystem]：删掉单个条目；不在不算错。
func (f *FS) Remove(_ context.Context, target fs.Target) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	delete(f.files, target.TargetKey)
	delete(f.dirs, target.TargetKey)
	return nil
}

// RemoveTree 实现 [fs.FileSystem]：删掉一棵子树；不在不算错。
func (f *FS) RemoveTree(_ context.Context, target fs.Target) error {
	prefix := string(target.TargetKey) + "/"

	f.mutex.Lock()
	defer f.mutex.Unlock()
	for key := range f.files {
		if key == target.TargetKey || strings.HasPrefix(string(key), prefix) {
			delete(f.files, key)
		}
	}
	for key := range f.dirs {
		if key == target.TargetKey || strings.HasPrefix(string(key), prefix) {
			delete(f.dirs, key)
		}
	}
	return nil
}

// EditText 实现 [fs.FileSystem]：原子地做一次字面替换。
func (f *FS) EditText(
	_ context.Context, target fs.Target, edit fs.EditRequest, expected *fs.EditIntent,
) (fs.EditOutcome, error) {
	found, exists := f.load(target.TargetKey)
	// 版本校验在匹配**之前**，见 [fs.FileSystem.EditText]。
	if expected != nil && (!exists || found.version != expected.Version) {
		return fs.EditOutcome{}, &fs.Error{Code: fs.CodeStaleVersion, Message: "stale: " + target.DisplayPath}
	}
	if !exists {
		return fs.EditOutcome{}, &fs.Error{Code: fs.CodeStaleVersion, Message: "missing: " + target.DisplayPath}
	}

	before := string(found.content)
	matches := strings.Count(before, edit.OldString)
	switch {
	case matches == 0:
		return fs.EditOutcome{}, &fs.Error{Code: fs.CodeEditNotFound, Message: "no match: " + target.DisplayPath}
	case matches > 1 && !edit.ReplaceAll:
		return fs.EditOutcome{}, &fs.Error{Code: fs.CodeAmbiguousEdit, Message: "ambiguous: " + target.DisplayPath}
	}

	after := strings.ReplaceAll(before, edit.OldString, edit.NewString)
	return fs.EditOutcome{
		Version: f.store(target.TargetKey, []byte(after)),
		Before:  before,
		After:   after,
	}, nil
}
