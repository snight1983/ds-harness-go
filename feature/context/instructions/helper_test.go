// 本文件的作用：这个包的用例要用的那个假文件系统，以及几个把断言写短的小工具。
//
// 假件必须能被**逐次注入失败**：本包最核心的那条契约——「确认不在」和
// 「提供方暂时问不出来」绝不混为一谈——只有在某一次 Resolve 或者 Stat
// 失败时才观察得到。一个永远成功的假件把这条契约整个藏了起来。

package instructions

import (
	"context"
	"iter"
	"path"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/fs"
)

// fakeEntry 是假文件系统里的一个项。
type fakeEntry struct {
	kind    fs.EntryType
	content string
	version fs.Version
	// sizeUnknown 表示这个后端报不出大小，也就是 [fs.Info.Size] 是 nil。
	sizeUnknown bool
	// sizeOverride 非 nil 时报一个和内容对不上的大小，用来造「元数据陈旧」。
	sizeOverride *int64
}

// fakeFS 是一个只认路径字符串的假文件系统。
//
// 只实现本包真正用到的三个方法（Resolve、Stat、StreamText），其余全部 panic——
// 一个悄悄返回零值的桩会让「本包偷偷用了别的方法」这件事查不出来。
type fakeFS struct {
	entries map[string]*fakeEntry
	// links 是「链接路径 → 它指向的路径」，Resolve 会跟着走。
	links map[string]string

	resolveErr map[string]error
	statErr    map[string]error
	streamErr  map[string]error
	// streamFailAfter 让一次流式读在吐出第 n 块之后失败，用来看半份内容会不会被交出去。
	streamFailAfter map[string]int
	// chunkSize 是流式读每次吐多少字节；零表示一次吐完。
	chunkSize int
	// cancelDuringStream 在吐出第一块之后取消调用方的 ctx。取消可以发生在
	// 一次流式读的**中途**，而那一刻和「读到一半失败」长得很像却必须分开处理，
	// 从外面注入 ctx 观察不到这个时刻。
	cancelDuringStream map[string]context.CancelFunc
	// gates 让一次流式读在开吐之前先停下来等放行。
	//
	// 用来把一次异步投影**稳稳地**停在半路上：几条用例要的是「投影还在飞的那一刻，
	// 别的事情正好走过来」，而那个时刻靠 sleep 撞不准。这道闸不看 ctx——
	// 「被取消的投影自己会收摊」正是其中一条用例要排除掉的干扰。
	gates map[string]<-chan struct{}

	// resolveCalls 记每条路径被 Resolve 了几次，用来钉「有没有白花一次 I/O」。
	resolveCalls map[string]int
}

func newFakeFS() *fakeFS {
	return &fakeFS{
		entries:         map[string]*fakeEntry{},
		links:           map[string]string{},
		resolveErr:      map[string]error{},
		statErr:         map[string]error{},
		streamErr:       map[string]error{},
		streamFailAfter: map[string]int{},
		resolveCalls:    map[string]int{},

		cancelDuringStream: map[string]context.CancelFunc{},
		gates:              map[string]<-chan struct{}{},
	}
}

// addFile 放一个常规文件，版本由内容派生，大小报得出。
func (f *fakeFS) addFile(p string, content string) *fakeFS {
	f.entries[path.Clean(p)] = &fakeEntry{
		kind:    fs.TypeFile,
		content: content,
		version: fs.Version("v:" + ContentDigest(content)),
	}
	return f
}

// addDir 放一个目录。
func (f *fakeFS) addDir(p string) *fakeFS {
	f.entries[path.Clean(p)] = &fakeEntry{kind: fs.TypeDirectory}
	return f
}

// addLink 放一条符号链接。目标不存在时它就是一条断链。
func (f *fakeFS) addLink(from string, to string) *fakeFS {
	f.links[path.Clean(from)] = path.Clean(to)
	return f
}

// setVersion 换掉一个文件的版本令牌，用来造「内容没变但版本变了」和它的反面。
func (f *fakeFS) setVersion(p string, version fs.Version) *fakeFS {
	f.entries[path.Clean(p)].version = version
	return f
}

// hideSize 让这个后端报不出某个文件的大小。
func (f *fakeFS) hideSize(p string) *fakeFS {
	f.entries[path.Clean(p)].sizeUnknown = true
	return f
}

// fakeSize 让元数据报一个和内容对不上的大小。
func (f *fakeFS) fakeSize(p string, size int64) *fakeFS {
	f.entries[path.Clean(p)].sizeOverride = &size
	return f
}

// remove 删掉一个项。
func (f *fakeFS) remove(p string) *fakeFS {
	delete(f.entries, path.Clean(p))
	return f
}

func (f *fakeFS) failResolve(p string, err error) *fakeFS {
	f.resolveErr[path.Clean(p)] = err
	return f
}

func (f *fakeFS) failStat(p string, err error) *fakeFS {
	f.statErr[path.Clean(p)] = err
	return f
}

func (f *fakeFS) failStream(p string, err error) *fakeFS {
	f.streamErr[path.Clean(p)] = err
	return f
}

// cancelAfterFirstChunk 让一次流式读吐出第一块之后调用方的 ctx 就被取消了。
func (f *fakeFS) cancelAfterFirstChunk(p string, cancel context.CancelFunc) *fakeFS {
	f.cancelDuringStream[path.Clean(p)] = cancel
	return f
}

// gateStream 让一条路径的流式读在开吐之前先等这道闸放行。
func (f *fakeFS) gateStream(p string, gate <-chan struct{}) *fakeFS {
	f.gates[path.Clean(p)] = gate
	return f
}

func (f *fakeFS) failStreamAfter(p string, chunks int, err error) *fakeFS {
	f.streamFailAfter[path.Clean(p)] = chunks
	f.streamErr[path.Clean(p)] = err
	return f
}

// followLinks 跟着链接链走到最后一跳，跟 Resolve 的语义一致。
func (f *fakeFS) followLinks(p string) string {
	// 上限挡住用例里不小心造出来的环；真后端靠内核的 ELOOP 挡。
	for range 32 {
		target, ok := f.links[p]
		if !ok {
			return p
		}
		p = target
	}
	return p
}

func (f *fakeFS) Resolve(ctx context.Context, p string, _ string) (fs.Target, error) {
	if err := ctx.Err(); err != nil {
		return fs.Target{}, err
	}
	cleaned := path.Clean(p)
	f.resolveCalls[cleaned]++
	if err, ok := f.resolveErr[cleaned]; ok {
		return fs.Target{}, err
	}
	resolved := f.followLinks(cleaned)
	return fs.Target{TargetKey: fs.TargetKey("key:" + resolved), DisplayPath: resolved}, nil
}

func (f *fakeFS) Stat(ctx context.Context, target fs.Target) (fs.Info, bool, error) {
	if err := ctx.Err(); err != nil {
		return fs.Info{}, false, err
	}
	p := target.DisplayPath
	if err, ok := f.statErr[p]; ok {
		return fs.Info{}, false, err
	}
	entry, ok := f.entries[p]
	if !ok {
		return fs.Info{}, false, nil
	}
	info := fs.Info{Version: entry.version, Type: entry.kind}
	switch {
	case entry.sizeOverride != nil:
		info.Size = entry.sizeOverride
	case !entry.sizeUnknown && entry.kind == fs.TypeFile:
		size := int64(len(entry.content))
		info.Size = &size
	}
	return info, true, nil
}

func (f *fakeFS) StreamText(ctx context.Context, target fs.Target) (iter.Seq2[string, error], error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p := target.DisplayPath
	// 没排失败计划的流，在这里就整个失败；排了计划的留到迭代中途失败。
	if err, ok := f.streamErr[p]; ok {
		if _, delayed := f.streamFailAfter[p]; !delayed {
			return nil, err
		}
	}
	entry, ok := f.entries[p]
	if !ok {
		return nil, errFake
	}
	failAfter, delayed := f.streamFailAfter[p]
	failErr := f.streamErr[p]
	size := f.chunkSize
	cancel := f.cancelDuringStream[p]
	gate := f.gates[p]
	return func(yield func(string, error) bool) {
		if gate != nil {
			<-gate
		}
		emitted := 0
		for chunk := range chunksOf(entry.content, size) {
			if delayed && emitted == failAfter {
				yield("", failErr)
				return
			}
			if !yield(chunk, nil) {
				return
			}
			emitted++
			if emitted == 1 && cancel != nil {
				cancel()
			}
		}
		if delayed && emitted >= failAfter {
			yield("", failErr)
		}
	}, nil
}

// chunksOf 把一段文字切成若干块；size 非正表示一整块。
func chunksOf(content string, size int) iter.Seq[string] {
	return func(yield func(string) bool) {
		if size <= 0 {
			if content != "" {
				yield(content)
			}
			return
		}
		for start := 0; start < len(content); start += size {
			end := min(start+size, len(content))
			if !yield(content[start:end]) {
				return
			}
		}
	}
}

func (f *fakeFS) Contains(fs.Target, fs.Target) bool {
	panic("instructions 的用例不该用到 Contains")
}

func (f *fakeFS) Lstat(context.Context, string, string) (fs.PathInfo, bool, error) {
	panic("instructions 的用例不该用到 Lstat")
}

func (f *fakeFS) ReadText(context.Context, fs.Target) (string, error) {
	panic("instructions 的用例不该用到 ReadText")
}

func (f *fakeFS) ReadBytes(context.Context, fs.Target, int64) ([]byte, error) {
	panic("instructions 的用例不该用到 ReadBytes")
}

func (f *fakeFS) ListDir(context.Context, fs.Target) ([]fs.DirEntry, error) {
	panic("instructions 的用例不该用到 ListDir")
}

func (f *fakeFS) WriteText(context.Context, fs.Target, string, fs.WriteIntent) (fs.WriteOutcome, error) {
	panic("instructions 的用例不该用到 WriteText")
}

func (f *fakeFS) EditText(context.Context, fs.Target, fs.EditRequest, *fs.EditIntent) (fs.EditOutcome, error) {
	panic("instructions 的用例不该用到 EditText")
}

func (f *fakeFS) WriteBytes(context.Context, fs.Target, []byte, fs.WriteIntent) (fs.Version, error) {
	panic("instructions 的用例不该用到 WriteBytes")
}

func (f *fakeFS) MakeDir(context.Context, string, string) (fs.Target, error) {
	panic("instructions 的用例不该用到 MakeDir")
}

func (f *fakeFS) Remove(context.Context, fs.Target) error {
	panic("instructions 的用例不该用到 Remove")
}

func (f *fakeFS) RemoveTree(context.Context, fs.Target) error {
	panic("instructions 的用例不该用到 RemoveTree")
}

// errFake 是用例注入故障时用的那条哨兵。
var errFake = &fakeError{}

type fakeError struct{}

func (*fakeError) Error() string { return "假文件系统故意失败" }

// testConfig 是用例默认的配置：预算给足，用户全局那一层关着。
//
// 预算默认给一个大数，是因为大部分用例关心的**不是**预算裁剪；
// 关心裁剪的用例自己把 MaxBytes 调小。
func testConfig() ResolvedConfig {
	return Config{MaxBytes: 1 << 20}.Resolve()
}

// loaded 造一个已加载文件，省掉每处都写全四个字段。
func loaded(displayPath string, content string) LoadedFile {
	return LoadedFile{
		AbsolutePath: "/root/" + displayPath,
		DisplayPath:  displayPath,
		Content:      content,
	}
}

// displayPaths 把一组文件摊成显示路径，断言顺序时用。
func displayPaths[T File | LoadedFile](files []T) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		switch typed := any(file).(type) {
		case File:
			paths = append(paths, typed.DisplayPath)
		case LoadedFile:
			paths = append(paths, typed.DisplayPath)
		}
	}
	return paths
}

// equalStrings 报告两组字符串是否逐个相等。
func equalStrings(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// requirePaths 断言一组文件的显示路径正好是期望的那几条，顺序也要对。
func requirePaths[T File | LoadedFile](t *testing.T, got []T, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if paths := displayPaths(got); !equalStrings(paths, want) {
		t.Fatalf("路径不对：得到 %v，期望 %v", paths, want)
	}
}

// requireContains 断言一段文字含住给定片段。
func requireContains(t *testing.T, text string, fragment string) {
	t.Helper()
	if !strings.Contains(text, fragment) {
		t.Fatalf("这段文字里应当含有 %q，实际是：\n%s", fragment, text)
	}
}

// requireNotContains 断言一段文字不含给定片段。
func requireNotContains(t *testing.T, text string, fragment string) {
	t.Helper()
	if strings.Contains(text, fragment) {
		t.Fatalf("这段文字里不该含有 %q，实际是：\n%s", fragment, text)
	}
}
