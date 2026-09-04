// 本文件的作用：指令文件怎么找、怎么在字节上限里读出来，以及一整条基线怎么装出来。
//
// 源: packages/context/agent-instructions/src/files.ts:1-521

package instructions

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/snight1983/ds-harness-go/fs"
)

// File 是一个指令候选，绝对路径加模型看得见的显示路径。
//
// 源: packages/context/agent-instructions/src/files.ts:23-27
type File struct {
	// AbsolutePath 是这个文件在执行世界里的绝对路径，斜杠分隔。
	AbsolutePath string
	// DisplayPath 是相对项目根的显示路径，或者用户全局那条固定路径。
	// **模型只看见这一条**，所以它不该泄漏部署的目录结构。
	DisplayPath string
}

// LoadedFile 是一个内容已经读出来的指令文件。
//
// 源: packages/context/agent-instructions/src/files.ts:29-34
type LoadedFile struct {
	AbsolutePath string
	DisplayPath  string
	// Content 是读出来的 UTF-8 原文，一个字节都没动过。
	Content string
	// Version 是读的那一刻提供方给的新鲜度令牌；空串表示没有。
	//
	// 新增: DSH 那边是可选属性 `version?`。Go 这边用空串当「没有」是安全的，
	// 因为空串**不是**一个合法的 [fs.Version]，那条由 fs 包的不变量守着。
	Version fs.Version
}

// ProbedFile 是一个探过元数据、还没读内容的作用域候选。
//
// 源: packages/context/agent-instructions/src/files.ts:42-47
type ProbedFile struct {
	AbsolutePath string
	DisplayPath  string
	Target       fs.Target
	Version      fs.Version
	// Size 是提供方报出来的字节数；报不出时是 nil。
	Size *int64
}

// discoveredFile 是发现阶段找到的一个候选，带着后面要用的提供方元数据。
//
// 源: packages/context/agent-instructions/src/files.ts:36-40
type discoveredFile struct {
	File
	target  fs.Target
	version fs.Version
	size    *int64
}

// RenderedInstructionSet 是一份渲染好的基线，加上它前后两份文件清单。
//
// 源: packages/context/agent-instructions/src/files.ts:65-72
type RenderedInstructionSet struct {
	Rendered RenderedWorkspaceContext
	// Observed 是内容读成功了的全部候选，去重和预算裁剪之前的样子。
	Observed []LoadedFile
	// Included 是去重和预算裁剪之后真正留下的那些。
	Included []LoadedFile
}

// ProbeKind 是一次作用域探测的三种结果。
//
// 源: packages/context/agent-instructions/src/files.ts:73-77
//
// 「确认不在」和「暂时问不出来」必须分开：前者应该让模型收到一条 remove，
// 后者绝对不能——把一次提供方抖动当成「文件被删了」，会让模型丢掉一份还在的指令。
type ProbeKind int

const (
	// ProbePresent 表示这条作用域上确实有一个常规文件。
	ProbePresent ProbeKind = iota
	// ProbeAbsent 表示确认这条作用域上没有可用的常规文件。
	ProbeAbsent
	// ProbeUnavailable 表示提供方这次没答上来，什么都不能推断。
	ProbeUnavailable
)

// ScopeProbe 是一次作用域探测的结果。
//
// 源: packages/context/agent-instructions/src/files.ts:73-77
type ScopeProbe struct {
	Kind ProbeKind
	// File 只在 Kind 是 [ProbePresent] 时有意义。
	File ProbedFile
}

// statProbe 是一次「这条路径上是不是一个常规文件」的探测。
//
// 源: packages/context/agent-instructions/src/files.ts:79-88
type statProbe struct {
	kind    ProbeKind
	target  fs.Target
	version fs.Version
	size    *int64
}

// absPath 把一条路径规范成斜杠分隔的世界绝对路径。
//
// 新增: DSH 用的是 node 的 `resolve()`，相对路径以 `process.cwd()` 为基准。
// 本包没有那个概念（见包文档），相对路径以世界根为基准。反斜杠一律换成斜杠，
// 因为这条路径要交给执行世界里的后端去解析，而那个世界不一定是宿主机的平台。
func absPath(p string) string {
	cleaned := strings.ReplaceAll(p, `\`, "/")
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	return path.Clean(cleaned)
}

// resolvePath 把一条可能是相对的路径按 base 解出来。
//
// 源: packages/context/agent-instructions/src/files.ts:222
func resolvePath(base string, p string) string {
	cleaned := strings.ReplaceAll(p, `\`, "/")
	if strings.HasPrefix(cleaned, "/") {
		return path.Clean(cleaned)
	}
	return path.Clean(absPath(base) + "/" + cleaned)
}

// segments 把一条绝对路径拆成各段。
func segments(abs string) []string {
	trimmed := strings.Trim(abs, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

// RelativeDisplay 把一条绝对路径换算成相对 root 的显示形式。
//
// 源: packages/context/agent-instructions/src/files.ts:229-237
//
// 新增: Go 标准库的 [path] 包**没有** Rel——只有 [path/filepath] 有，
// 而那一个会按宿主机的分隔符干活。本包的路径全是斜杠分隔的世界路径，
// 所以这一段按段比较手写出来。语义跟 node 的 `relative` 对齐：
// 两条路径相同时给空串，target 在 root 外面时给若干个 `..`。
func RelativeDisplay(root string, target string) string {
	from := segments(absPath(root))
	to := segments(absPath(target))
	shared := 0
	for shared < len(from) && shared < len(to) && from[shared] == to[shared] {
		shared++
	}
	parts := make([]string, 0, (len(from)-shared)+(len(to)-shared))
	for range from[shared:] {
		parts = append(parts, "..")
	}
	parts = append(parts, to[shared:]...)
	return strings.Join(parts, "/")
}

// statFile 探一条路径上是不是一个可读的常规文件。
//
// 源: packages/context/agent-instructions/src/files.ts:113-143
//
// [fs.FileSystem.Resolve] 会跟着最后一段的符号链接走到目标的稳定身份，
// Stat 再给那个目标分类：链到常规文件的能加载，链到目录或者断链算「不在」。
//
// 新增: DSH 在没有 `ctx.fs` 时退回 node:fs 直接读宿主机（`nodeStatFile`）。
// 本包没有那条回退：宿主机的文件系统不是执行世界的文件系统。
//
// error 只在 ctx 被取消时非 nil。提供方自己的故障降级成 [ProbeUnavailable]，
// 但**取消绝不降级**——降了的话，一次被取消的对账会被读成「文件全没了」。
func statFile(ctx context.Context, fsys fs.FileSystem, p string) (statProbe, error) {
	target, err := fsys.Resolve(ctx, p, "")
	if err != nil {
		if cause := ctx.Err(); cause != nil {
			return statProbe{}, cause
		}
		return statProbe{kind: ProbeUnavailable}, nil
	}
	info, ok, err := fsys.Stat(ctx, target)
	if err != nil {
		if cause := ctx.Err(); cause != nil {
			return statProbe{}, cause
		}
		return statProbe{kind: ProbeUnavailable}, nil
	}
	if !ok || info.Type != fs.TypeFile {
		return statProbe{kind: ProbeAbsent}, nil
	}
	return statProbe{kind: ProbePresent, target: target, version: info.Version, size: info.Size}, nil
}

// existsAsMarker 判断一条路径上有没有东西，用来认项目根标记。
//
// 源: packages/context/agent-instructions/src/files.ts:145-166
//
// 任何一种在场都算数，不挑类型：`.git` 在普通工作树里是目录，
// 在 worktree 和 submodule 里是文件。
//
// TODO(root-marker-unavailable): 提供方故障这里和「不在」返回同一个答案，
// 于是往上走的过程会继续走，可能跨进一个祖先项目里去。要分开的话，
// 得让这个错误一路传上去并且中止发现。
func existsAsMarker(ctx context.Context, fsys fs.FileSystem, p string) (bool, error) {
	target, err := fsys.Resolve(ctx, p, "")
	if err != nil {
		if cause := ctx.Err(); cause != nil {
			return false, cause
		}
		return false, nil
	}
	_, ok, err := fsys.Stat(ctx, target)
	if err != nil {
		if cause := ctx.Err(); cause != nil {
			return false, cause
		}
		return false, nil
	}
	return ok, nil
}

// FindProjectRoot 从 workspaceRoot 往上走，找到第一个带根标记的目录。
//
// 源: packages/context/agent-instructions/src/files.ts:168-191
//
// 一路走到世界根都没找到就退回 workspaceRoot 本身：那说明这不是一个有标记的项目，
// 而「以 workspaceRoot 为根」是唯一不需要猜的解释。
func FindProjectRoot(ctx context.Context, fsys fs.FileSystem, workspaceRoot string, markers []string) (string, error) {
	current := absPath(workspaceRoot)
	for {
		for _, marker := range markers {
			found, err := existsAsMarker(ctx, fsys, path.Join(current, marker))
			if err != nil {
				return "", err
			}
			if found {
				return current, nil
			}
		}
		parent := path.Dir(current)
		if parent == current {
			return absPath(workspaceRoot), nil
		}
		current = parent
	}
}

// AncestorChain 给出从 root 到 workspaceRoot 的那条闭区间目录链，从宽到窄。
//
// 源: packages/context/agent-instructions/src/files.ts:193-212
func AncestorChain(root string, workspaceRoot string) []string {
	resolvedRoot := absPath(root)
	current := absPath(workspaceRoot)
	chain := make([]string, 0, 8)
	for current != resolvedRoot {
		chain = append(chain, current)
		parent := path.Dir(current)
		if parent == current {
			// 发现阶段给的永远是 workspaceRoot 或者它的某个祖先，所以到不了这里。
			break
		}
		current = parent
	}
	chain = append(chain, resolvedRoot)
	slices.Reverse(chain)
	return chain
}

// DescendantDirsBetween 给出 root 和一个被碰过的文件之间跨过的那些后代目录。
//
// 源: packages/context/agent-instructions/src/files.ts:214-227
//
// 模型读写了一个深处的文件，那一路上的目录才需要临时纳入对账范围——
// 那些目录本来不在基线的祖先链上。
//
// 新增: DSH 还判一次 `isAbsolute(rel)`，防的是 Windows 上两条路径在不同盘符时
// `relative` 会返回一条绝对路径。本包的路径是单根的世界路径，没有盘符这回事，
// [RelativeDisplay] 的结果永远是相对的，所以那一判在这里表达不出来。
func DescendantDirsBetween(root string, touchedPath string) []string {
	resolvedRoot := absPath(root)
	targetDir := path.Dir(resolvePath(resolvedRoot, touchedPath))
	rel := RelativeDisplay(resolvedRoot, targetDir)
	if rel == "" || strings.HasPrefix(rel, "..") {
		return nil
	}
	return AncestorChain(resolvedRoot, targetDir)[1:]
}

// allExistingInstructionFiles 把一个目录里在场的候选全部找出来。
//
// 源: packages/context/agent-instructions/src/files.ts:239-265
func allExistingInstructionFiles(
	ctx context.Context,
	fsys fs.FileSystem,
	dir string,
	root string,
	candidates []string,
) ([]discoveredFile, error) {
	found := make([]discoveredFile, 0, len(candidates))
	for _, candidate := range candidates {
		p := path.Join(dir, candidate)
		probe, err := statFile(ctx, fsys, p)
		if err != nil {
			return nil, err
		}
		// 不在场就跳过这一个；提供方抖了一下也只跳过这一个，
		// 剩下那些互相独立的候选照常加载。
		if probe.kind != ProbePresent {
			continue
		}
		found = append(found, discoveredFile{
			File:    File{AbsolutePath: p, DisplayPath: RelativeDisplay(root, p)},
			target:  probe.target,
			version: probe.version,
			size:    probe.size,
		})
	}
	return found, nil
}

// discoverInstructionFiles 按模型优先级顺序找出全部候选：先用户全局，再根到 workspaceRoot。
//
// 源: packages/context/agent-instructions/src/files.ts:267-309
//
// projectRoot 留空表示「自己去找」。
func discoverInstructionFiles(
	ctx context.Context,
	fsys fs.FileSystem,
	config ResolvedConfig,
	workspaceRoot string,
	projectRoot string,
) ([]discoveredFile, error) {
	var files []discoveredFile
	seen := map[string]struct{}{}
	addFile := func(file discoveredFile) {
		if _, already := seen[file.AbsolutePath]; already {
			return
		}
		seen[file.AbsolutePath] = struct{}{}
		files = append(files, file)
	}

	// 新增: UserGlobalRoot 留空表示这套部署没有用户全局这一层，
	// 直接跳过而不是去探一个猜出来的路径。DSH 那边这条路径一定存在，
	// 因为它是从本机 home 算出来的。
	if config.UserGlobalRoot != "" {
		userGlobal := path.Join(absPath(config.UserGlobalRoot), UserGlobalFile)
		probe, err := statFile(ctx, fsys, userGlobal)
		if err != nil {
			return nil, err
		}
		if probe.kind == ProbePresent {
			addFile(discoveredFile{
				File:    File{AbsolutePath: userGlobal, DisplayPath: UserGlobalDisplayPath},
				target:  probe.target,
				version: probe.version,
				size:    probe.size,
			})
		}
	}

	resolvedWorkspaceRoot := absPath(workspaceRoot)
	root := projectRoot
	if root == "" {
		found, err := FindProjectRoot(ctx, fsys, resolvedWorkspaceRoot, config.ProjectRootMarkers)
		if err != nil {
			return nil, err
		}
		root = found
	}
	root = absPath(root)
	for _, dir := range AncestorChain(root, resolvedWorkspaceRoot) {
		for _, candidates := range [][]string{config.InstructionFileCandidates, config.LocalInstructionFileCandidates} {
			found, err := allExistingInstructionFiles(ctx, fsys, dir, root, candidates)
			if err != nil {
				return nil, err
			}
			for _, file := range found {
				addFile(file)
			}
		}
	}
	return files, nil
}

// DiscoverBaselineFiles 找出基线的全部候选，只要两条路径。
//
// 源: packages/context/agent-instructions/src/files.ts:311-320
//
// 每个目录里在场的候选**全部**返回；内容重复的折叠要等内容读出来之后
// 才做得了，见 [DedupByDirectory]。
func DiscoverBaselineFiles(
	ctx context.Context,
	fsys fs.FileSystem,
	config ResolvedConfig,
	workspaceRoot string,
	projectRoot string,
) ([]File, error) {
	discovered, err := discoverInstructionFiles(ctx, fsys, config, workspaceRoot, projectRoot)
	if err != nil {
		return nil, err
	}
	files := make([]File, 0, len(discovered))
	for _, file := range discovered {
		files = append(files, file.File)
	}
	return files, nil
}

// ErrInstructionsTooLarge 表示一次装载读进来的指令文件加起来超过了总量上限。
//
// 新增: 见 [Config.MaxTotalSourceBytes]。它是**错误**而不是「少读几份」：
// 一份少了几个文件的基线看上去和一份完整基线没有区别，然后会被当成完整内容
// 去算摘要、去和上一份比对——和 [readBounded] 里「不交出半份文件」的理由同源，
// 只是这里的「半份」是半份基线。
var ErrInstructionsTooLarge = errors.New("context/instructions: 指令文件总量超过上限")

// sourceBudget 是一次装载的总量预算，跨文件累计。
//
// 新增: 每个文件那道上限在 [readBounded] 里，那一道超了是**跳过这个文件**
// （上游行为）；这一道超了是整次装载失败。两道语义不同，所以不合并成一个数。
type sourceBudget struct {
	// remaining 是还能读多少字节。
	remaining int
	// unlimited 表示这一层关着，remaining 不看。
	unlimited bool
}

// newSourceBudget 按配置起一份预算。
func newSourceBudget(config ResolvedConfig) *sourceBudget {
	return &sourceBudget{
		remaining: config.MaxTotalSourceBytes,
		unlimited: config.MaxTotalSourceBytes <= 0,
	}
}

// take 记下一份刚读进来的文件；总量超了就报 [ErrInstructionsTooLarge]。
//
// 记账在文件读完**之后**：单份的峰值内存已经被每个文件那道上限压住了，
// 所以这里最多超出一份文件的量，换来的是不必把两道语义不同的上限揉进同一个数。
func (b *sourceBudget) take(displayPath string, bytes int) error {
	if b.unlimited {
		return nil
	}
	if bytes > b.remaining {
		return fmt.Errorf("%w: 读到 %q 时还差 %d 字节就超了总量上限（这一份 %d 字节，剩余额度 %d 字节）",
			ErrInstructionsTooLarge, displayPath, bytes-b.remaining, bytes, b.remaining)
	}
	b.remaining -= bytes
	return nil
}

// readBounded 在字节上限里把一个目标读成文字；超限或者读不出来时第二个返回值是 false。
//
// 源: packages/context/agent-instructions/src/files.ts:327-357
//
// 先看元数据里的大小能挡掉一大半，但那个数可能报不出来，也可能是陈旧的，
// 所以流式读的过程里还要再数一遍字节。两道都要，缺一道就等于让一个
// 无界大的文件进内存。
//
// 这里的上限是**每个文件**的；一整份基线或者一整批对账的总量由
// [sourceBudget] 另外压一道。
func readBounded(
	ctx context.Context,
	fsys fs.FileSystem,
	target fs.Target,
	size *int64,
	maxSourceBytes int,
) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if size != nil && *size > int64(maxSourceBytes) {
		return "", false, nil
	}
	chunks, err := fsys.StreamText(ctx, target)
	if err != nil {
		if cause := ctx.Err(); cause != nil {
			return "", false, cause
		}
		// 元数据探过之后文件可能就没了，或者变得读不了了。
		return "", false, nil
	}
	var builder strings.Builder
	total := 0
	// 这两个标记是从 range-over-func 里带出来的：迭代体里 return 会停掉迭代，
	// 但停掉之后还要再看一眼 ctx，所以先记下来、跳出去再判。
	var aborted error
	rejected := false
	for chunk, chunkErr := range chunks {
		if chunkErr != nil {
			// 一次流式读可以读了一半才失败。整份丢掉而不是把读到的那半交出去：
			// 半份指令看上去是成功的，然后会被当成完整内容去算摘要、去比对。
			aborted = ctx.Err()
			rejected = true
			break
		}
		if cause := ctx.Err(); cause != nil {
			aborted = cause
			break
		}
		total += byteLength(chunk)
		if total > maxSourceBytes {
			rejected = true
			break
		}
		builder.WriteString(chunk)
	}
	if aborted != nil {
		return "", false, aborted
	}
	if rejected {
		return "", false, nil
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	return builder.String(), true, nil
}

// DedupByDirectory 丢掉那些「同一个目录里、去空白内容和更靠前的兄弟一样」的候选。
//
// 源: packages/context/agent-instructions/src/files.ts:359-384
//
// 不同目录之间**永远不折叠**，哪怕内容一模一样：那是两条不同作用域上
// 各自成立的指令，只是碰巧写得一样。同一个目录里留发现顺序最靠前的那份，
// 并且渲染它的原始字节（不是去过空白的那份）。
func DedupByDirectory(files []LoadedFile) []LoadedFile {
	keptDigestsByDir := map[string]map[string]struct{}{}
	kept := make([]LoadedFile, 0, len(files))
	for _, file := range files {
		dir := path.Dir(file.DisplayPath)
		digests, ok := keptDigestsByDir[dir]
		if !ok {
			digests = map[string]struct{}{}
			keptDigestsByDir[dir] = digests
		}
		digest := TrimmedDigest(file.Content)
		if _, duplicate := digests[digest]; duplicate {
			continue
		}
		digests[digest] = struct{}{}
		kept = append(kept, file)
	}
	return kept
}

// LoadBaselineSet 把基线整条装出来：发现、读、去重、渲染。
//
// 源: packages/context/agent-instructions/src/files.ts:399-449
//
// 第二个返回值是 false 表示「这一次没有基线」——预算关着、上限关着，
// 或者一份指令都没找到而且调用方也没要求发一份显式的空替换。
// 「没有基线」和「一份空基线」是两件事：后者会明确告诉模型
// 「先前那些工作区指令全部作废了」。
func LoadBaselineSet(
	ctx context.Context,
	fsys fs.FileSystem,
	config ResolvedConfig,
	workspaceRoot string,
	projectRoot string,
	replacePreviousBaseline bool,
) (RenderedInstructionSet, bool, error) {
	// 新增: DSH 这两处还各判一次 !Number.isFinite，Go 的 int 没有无穷。
	if config.MaxBytes <= 0 || config.MaxSourceBytes <= 0 {
		return RenderedInstructionSet{}, false, nil
	}
	discovered, err := discoverInstructionFiles(ctx, fsys, config, workspaceRoot, projectRoot)
	if err != nil {
		return RenderedInstructionSet{}, false, err
	}
	budget := newSourceBudget(config)
	loaded := make([]LoadedFile, 0, len(discovered))
	for _, file := range discovered {
		content, ok, err := readBounded(ctx, fsys, file.target, file.size, config.MaxSourceBytes)
		if err != nil {
			return RenderedInstructionSet{}, false, err
		}
		if !ok {
			continue
		}
		if err := budget.take(file.DisplayPath, byteLength(content)); err != nil {
			return RenderedInstructionSet{}, false, err
		}
		loaded = append(loaded, LoadedFile{
			AbsolutePath: file.AbsolutePath,
			DisplayPath:  file.DisplayPath,
			Content:      content,
			Version:      file.version,
		})
	}

	deduped := DedupByDirectory(loaded)
	if len(deduped) == 0 {
		if !replacePreviousBaseline {
			return RenderedInstructionSet{}, false, nil
		}
		rendered, included := RenderWorkspaceInstructionSet(nil, config.MaxBytes, true)
		return RenderedInstructionSet{Rendered: rendered, Included: included}, true, nil
	}
	rendered, included := RenderWorkspaceInstructionSet(deduped, config.MaxBytes, replacePreviousBaseline)
	return RenderedInstructionSet{Rendered: rendered, Observed: loaded, Included: included}, true, nil
}

// LoadBaseline 装一份基线，只要渲染结果。
//
// 源: packages/context/agent-instructions/src/files.ts:386-397
func LoadBaseline(
	ctx context.Context,
	fsys fs.FileSystem,
	config ResolvedConfig,
	workspaceRoot string,
	projectRoot string,
	replacePreviousBaseline bool,
) (RenderedWorkspaceContext, bool, error) {
	set, ok, err := LoadBaselineSet(ctx, fsys, config, workspaceRoot, projectRoot, replacePreviousBaseline)
	if err != nil || !ok {
		return RenderedWorkspaceContext{}, false, err
	}
	return set.Rendered, true, nil
}

// scopeDirectoryPath 把一个作用域目录还原成执行世界里的绝对目录。
//
// 源: packages/context/agent-instructions/src/files.ts:468-470
func scopeDirectoryPath(directory string, projectRoot string, config ResolvedConfig) string {
	switch directory {
	case UserGlobalDirectory:
		return absPath(config.UserGlobalRoot)
	case ".":
		return absPath(projectRoot)
	default:
		return path.Join(absPath(projectRoot), directory)
	}
}

// ProbeScopeInstruction 探一条作用域此刻在提供方那边是什么样。
//
// 源: packages/context/agent-instructions/src/files.ts:451-493
//
// Resolve 跟着最后一段的符号链接走，Stat 再给目标分类：不是常规文件
// （不存在、或者链到了一个目录）算**确认不在**；只有提供方自己报错才算
// 暂时问不出来。这条区分是整个对账的地基，见 [ProbeKind]。
func ProbeScopeInstruction(
	ctx context.Context,
	fsys fs.FileSystem,
	config ResolvedConfig,
	scope string,
	projectRoot string,
) (ScopeProbe, error) {
	directory, candidateName := DecodeScopeKey(scope)
	// 用户全局那一层关着的时候，它下面的作用域一律确认不在——
	// 不这么判的话下面会拼出一条以世界根为基准的路径，探到别的东西上去。
	if directory == UserGlobalDirectory && config.UserGlobalRoot == "" {
		return ScopeProbe{Kind: ProbeAbsent}, nil
	}
	absolutePath := path.Join(scopeDirectoryPath(directory, projectRoot, config), candidateName)

	probe, err := statFile(ctx, fsys, absolutePath)
	if err != nil {
		return ScopeProbe{}, err
	}
	if probe.kind != ProbePresent {
		return ScopeProbe{Kind: probe.kind}, nil
	}

	displayPath := RelativeDisplay(absPath(projectRoot), absolutePath)
	if directory == UserGlobalDirectory {
		displayPath = UserGlobalDisplayPath
	}
	return ScopeProbe{Kind: ProbePresent, File: ProbedFile{
		AbsolutePath: absolutePath,
		DisplayPath:  displayPath,
		Target:       probe.target,
		Version:      probe.version,
		Size:         probe.size,
	}}, nil
}

// ReadScopeInstruction 在字节上限里读一个已经探过的作用域候选。
//
// 源: packages/context/agent-instructions/src/files.ts:495-517
func ReadScopeInstruction(
	ctx context.Context,
	fsys fs.FileSystem,
	file ProbedFile,
	maxSourceBytes int,
) (LoadedFile, bool, error) {
	content, ok, err := readBounded(ctx, fsys, file.Target, file.Size, maxSourceBytes)
	if err != nil || !ok {
		return LoadedFile{}, false, err
	}
	return LoadedFile{
		AbsolutePath: file.AbsolutePath,
		DisplayPath:  file.DisplayPath,
		Content:      content,
		Version:      file.Version,
	}, true, nil
}
