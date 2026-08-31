// 本文件是这条接缝的被试：一个内存后端。
//
// 源: packages/fs/fs/tests/service.spec.ts:22-83
//
// 它存在的理由和 DSH 那个 FakeFileSystem 一样——真正的原语和策略在别的包里，
// 这条接缝只拥有那份抽象契约，所以验它需要一个刚好够用的实现。
// 「刚好够用」在这里的意思是：守卫、上限、单处匹配这几条**语义**都真的实现了，
// 因为它们正是契约里写着、而实现方最容易漏掉的部分。

package fs

import (
	"context"
	"fmt"
	"iter"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// fakeChunkSize 是 StreamText 每块的字节数，故意取得很小，
// 好让一份几十字节的内容也走得出多块的路径。
const fakeChunkSize = 4

// fakeFile 是内存后端存下来的一份内容加它的版本。
type fakeFile struct {
	content string
	version Version
}

// fakeFS 是一个内存文件系统，键就是它自己的 [TargetKey]。
type fakeFS struct {
	mutex sync.Mutex
	files map[TargetKey]fakeFile
	// links 是「这条路径是一个符号链接」的登记，只供 Lstat 用——
	// Resolve 会跟过去，所以目标那一侧永远看不见它们。
	links map[string]string
	// stamp 递增，用来造出每次写入都不一样的版本令牌。
	stamp int
}

func newFakeFS() *fakeFS {
	return &fakeFS{files: map[TargetKey]fakeFile{}, links: map[string]string{}}
}

// seed 直接种一份内容进去，不走写入路径（写入路径本身是被验的东西之一）。
func (f *fakeFS) seed(key TargetKey, content string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.stamp++
	f.files[key] = fakeFile{content: content, version: Version("v" + strconv.Itoa(f.stamp))}
}

func (f *fakeFS) load(key TargetKey) (fakeFile, bool) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	file, ok := f.files[key]
	return file, ok
}

// store 写下一份内容并盖一个新版本，返回新版本。
func (f *fakeFS) store(key TargetKey, content string) Version {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.stamp++
	version := Version("v" + strconv.Itoa(f.stamp))
	f.files[key] = fakeFile{content: content, version: version}
	return version
}

func (f *fakeFS) Resolve(_ context.Context, path string, cwd string) (Target, error) {
	if path == "" {
		return Target{}, &Error{Code: CodeNotFound, Message: "empty path"}
	}
	full := path
	if cwd != "" && !strings.HasPrefix(path, "/") {
		full = cwd + "/" + path
	}
	return Target{TargetKey: TargetKey(full), DisplayPath: full}, nil
}

func (f *fakeFS) ProcessPath(target Target) string { return string(target.TargetKey) }

func (f *fakeFS) FileURL(target Target) string {
	return "file:///" + url.PathEscape(string(target.TargetKey))
}

func (f *fakeFS) Contains(parent Target, child Target) bool {
	if child.TargetKey == parent.TargetKey {
		return true
	}
	return strings.HasPrefix(string(child.TargetKey), string(parent.TargetKey)+"/")
}

func (f *fakeFS) Stat(_ context.Context, target Target) (Info, bool, error) {
	if f.isDirectory(target.TargetKey) {
		return Info{Version: Version("dir"), Type: TypeDirectory}, true, nil
	}
	file, ok := f.load(target.TargetKey)
	if !ok {
		return Info{}, false, nil
	}
	size := int64(len(file.content))
	return Info{Version: file.version, Type: TypeFile, Size: &size}, true, nil
}

func (f *fakeFS) Lstat(_ context.Context, path string, cwd string) (PathInfo, bool, error) {
	full := path
	if cwd != "" && !strings.HasPrefix(path, "/") {
		full = cwd + "/" + path
	}

	f.mutex.Lock()
	link, isLink := f.links[full]
	f.mutex.Unlock()
	if isLink {
		size := int64(len(link))
		return PathInfo{Version: Version("link"), Type: TypeSymlink, Size: &size}, true, nil
	}

	file, ok := f.load(TargetKey(full))
	if !ok {
		return PathInfo{}, false, nil
	}
	size := int64(len(file.content))
	return PathInfo{Version: file.version, Type: TypeFile, Size: &size}, true, nil
}

func (f *fakeFS) ReadText(_ context.Context, target Target) (string, error) {
	file, ok := f.load(target.TargetKey)
	if !ok {
		return "", &Error{Code: CodeNotFound, Message: "not found: " + target.DisplayPath}
	}
	if strings.ContainsRune(file.content, 0) {
		return "", &Error{Code: CodeNotText, Message: "not text: " + target.DisplayPath}
	}
	return file.content, nil
}

func (f *fakeFS) StreamText(ctx context.Context, target Target) (iter.Seq2[string, error], error) {
	content, err := f.ReadText(ctx, target)
	if err != nil {
		return nil, err
	}
	return func(yield func(string, error) bool) {
		for start := 0; start < len(content); start += fakeChunkSize {
			// 块与块之间也认取消，见 [FileSystem.StreamText]。
			if err := ctx.Err(); err != nil {
				yield("", &Error{Code: CodeAborted, Message: "aborted", Err: err})
				return
			}
			end := min(start+fakeChunkSize, len(content))
			if !yield(content[start:end], nil) {
				return
			}
		}
	}, nil
}

func (f *fakeFS) ReadBytes(ctx context.Context, target Target, maxBytes int64) ([]byte, error) {
	file, ok := f.load(target.TargetKey)
	if !ok {
		return nil, &Error{Code: CodeNotFound, Message: "not found: " + target.DisplayPath}
	}
	if int64(len(file.content)) > maxBytes {
		return nil, &Error{Code: CodeTooLarge, Message: "too large: " + target.DisplayPath}
	}
	if err := ctx.Err(); err != nil {
		return nil, &Error{Code: CodeAborted, Message: "aborted", Err: err}
	}
	return []byte(file.content), nil
}

func (f *fakeFS) ListDir(_ context.Context, target Target) ([]DirEntry, error) {
	prefix := string(target.TargetKey) + "/"

	f.mutex.Lock()
	var entries []DirEntry
	for key, file := range f.files {
		name, ok := strings.CutPrefix(string(key), prefix)
		if !ok || strings.Contains(name, "/") {
			continue
		}
		size := int64(len(file.content))
		entries = append(entries, DirEntry{
			Name:    name,
			Type:    TypeFile,
			Target:  Target{TargetKey: key, DisplayPath: string(key)},
			Version: file.version,
			Size:    &size,
		})
	}
	f.mutex.Unlock()

	if len(entries) == 0 {
		return nil, &Error{Code: CodeNotDirectory, Message: "not a directory: " + target.DisplayPath}
	}
	// 稳定的名字顺序是契约的一部分，见 [FileSystem.ListDir]。
	slices.SortFunc(entries, func(a, b DirEntry) int { return strings.Compare(a.Name, b.Name) })
	return entries, nil
}

func (f *fakeFS) isDirectory(key TargetKey) bool {
	prefix := string(key) + "/"

	f.mutex.Lock()
	defer f.mutex.Unlock()
	for candidate := range f.files {
		if strings.HasPrefix(string(candidate), prefix) {
			return true
		}
	}
	return false
}

func (f *fakeFS) WriteText(
	_ context.Context, target Target, content string, expected WriteIntent,
) (WriteOutcome, error) {
	file, exists := f.load(target.TargetKey)

	switch intent := expected.(type) {
	case nil:
		// 无条件：不检查任何前置条件，但仍然是原子发布的。
	case CreateIfAbsent:
		if exists {
			return WriteOutcome{}, &Error{
				Code: CodeNotObserved, Message: "already exists: " + target.DisplayPath,
			}
		}
	case ReplaceIfVersion:
		if !exists || file.version != intent.Version {
			return WriteOutcome{}, &Error{
				Code: CodeStaleVersion, Message: "stale: " + target.DisplayPath,
			}
		}
	default:
		// [WriteIntent] 是封印接口，本包外面造不出第三种；留着这一支是因为
		// 本包内部哪天真加了第四种成员，漏掉的分支必须当场炸而不是静默放行。
		return WriteOutcome{}, fmt.Errorf("fs: 未知的写意图 %T", expected)
	}

	operation := OperationCreate
	var before *string
	if exists {
		operation = OperationUpdate
		previous := file.content
		before = &previous
	}
	return WriteOutcome{
		Operation: operation,
		Version:   f.store(target.TargetKey, content),
		Before:    before,
		After:     content,
	}, nil
}

func (f *fakeFS) EditText(
	_ context.Context, target Target, edit EditRequest, expected *EditIntent,
) (EditOutcome, error) {
	file, exists := f.load(target.TargetKey)
	// 版本校验在匹配**之前**，见 [FileSystem.EditText]。
	if expected != nil && (!exists || file.version != expected.Version) {
		return EditOutcome{}, &Error{Code: CodeStaleVersion, Message: "stale: " + target.DisplayPath}
	}
	if !exists {
		return EditOutcome{}, &Error{Code: CodeStaleVersion, Message: "missing: " + target.DisplayPath}
	}

	matches := strings.Count(file.content, edit.OldString)
	switch {
	case matches == 0:
		return EditOutcome{}, &Error{Code: CodeEditNotFound, Message: "no match: " + target.DisplayPath}
	case matches > 1 && !edit.ReplaceAll:
		return EditOutcome{}, &Error{Code: CodeAmbiguousEdit, Message: "ambiguous: " + target.DisplayPath}
	}

	after := strings.ReplaceAll(file.content, edit.OldString, edit.NewString)
	return EditOutcome{
		Version: f.store(target.TargetKey, after),
		Before:  file.content,
		After:   after,
	}, nil
}
