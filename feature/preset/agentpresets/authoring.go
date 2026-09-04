// 本文件的作用：本地创作——复制、读取、删除一份预设，以及为什么这三件事全都
// 只碰得到 user 那个根。
//
// 源: packages/preset/agent-presets/src/authoring.ts

package agentpresets

import (
	"context"
	"errors"
	"fmt"
	"path"

	"github.com/snight1983/ds-harness-go/fs"
)

// ErrInvalidPresetID 是「这个 id 当不了根底下的目录名」。
var ErrInvalidPresetID = errors.New("agent-presets: 预设 id 不合文法")

// InvalidPresetIDError 带上被拒的那个 id。
//
// 源: packages/preset/agent-presets/src/authoring.ts:23-33
type InvalidPresetIDError struct {
	// PresetID 是被拒的那个 id。
	PresetID string
}

func (e *InvalidPresetIDError) Error() string {
	return fmt.Sprintf(
		"agent-presets: preset id %q must match %s — "+
			"the id is a directory name, so anything else could escape the preset root",
		e.PresetID, PresetIDPattern)
}

func (e *InvalidPresetIDError) Unwrap() error { return ErrInvalidPresetID }

// ErrPresetExists 是「复制的落点已经被占了」。
var ErrPresetExists = errors.New("agent-presets: 这个 id 已经被占了")

// PresetExistsError 带上那个被占的 id。
//
// 源: packages/preset/agent-presets/src/authoring.ts:37-47（presetExists）
type PresetExistsError struct {
	// PresetID 是那个已经被占的 id。
	PresetID string
}

func (e *PresetExistsError) Error() string {
	return fmt.Sprintf(
		"agent-presets: preset %q already exists — "+
			"a copy never overwrites; delete the existing preset first or choose another id",
		e.PresetID)
}

func (e *PresetExistsError) Unwrap() error { return ErrPresetExists }

// ErrPresetNotWritable 是「在一个这套部署不许写的地方动手了」。
var ErrPresetNotWritable = errors.New("agent-presets: 这里不许写")

// PresetNotWritableError 带上动了谁、以及为什么不许。
//
// 源: packages/preset/agent-presets/src/authoring.ts:49-57
type PresetNotWritableError struct {
	// PresetID 是被动的那一份；这套部署压根没有可写根时是空串。
	PresetID string
	// Reason 是不许的原因。
	Reason string
}

func (e *PresetNotWritableError) Error() string {
	return fmt.Sprintf("agent-presets: preset %q cannot be written: %s", e.PresetID, e.Reason)
}

func (e *PresetNotWritableError) Unwrap() error { return ErrPresetNotWritable }

// WritableRoot 交出本地创作出来的预设写去的那个根。
//
// 源: packages/preset/agent-presets/src/authoring.ts:49-62（writableRoot）
//
// 也就是按优先级排下来第一个 user 根。一个都没有时报 [PresetNotWritableError]：
// 随部署发出去的那一套属于部署，让一个浏览器改得动它，等于把「重置回一份已知的
// 预设」变成同一个调用方先前就能弄坏的东西。
func WritableRoot(roots []Root) (string, error) {
	for _, root := range roots {
		if root.Trust == TrustUser {
			return path.Clean(root.Path), nil
		}
	}
	return "", &PresetNotWritableError{
		Reason: "this deployment configures no user-writable preset root",
	}
}

// ReadComposition 读一份预设的组合文本。
//
// 源: packages/preset/agent-presets/src/authoring.ts:64-71（readComposition）
func ReadComposition(ctx context.Context, fsys fs.FileSystem, preset Preset) (string, error) {
	content, err := readFile(ctx, fsys, preset.Path)
	if err != nil {
		return "", fmt.Errorf("agent-presets: 读不了 %s 的组合：%w", preset.ID, err)
	}
	return string(content), nil
}

// occupied 判一条路径上有没有东西占着。
//
// 源: packages/preset/agent-presets/src/authoring.ts:83-93
//
// 看不成在这里和不存在是同一件事：这次复制没法确认这条路径被占着，那就往下走，
// 让真正的建目录去撞。
//
// 新增: 上游这一支是 `lstatSync`，**不跟链接**，于是一条指不到东西的断链也算占着。
// 这里走的是 [statPath]，它顺链（[fs.FileSystem.Resolve] 会跟过去），所以一条断链
// 在这里报成没占着。代价只是那种情形拿到的不再是 [PresetExistsError] 而是随后那次
// 建目录的失败——一条断链占着一个预设 id 本来就是一种要人去现场看的状态。
//
// 接缝上是有 [fs.FileSystem.Lstat] 的，这里刻意不用：它按定义看不见目标那一侧，
// 而这里问的正是「这个 id 上有没有一份能当预设用的东西」。
func occupied(ctx context.Context, fsys fs.FileSystem, at string) bool {
	_, found, err := statPath(ctx, fsys, at)
	return err == nil && found
}

// copyTree 把 source 整个目录复制到 target，顺链取实体、并把权限收成只给属主。
//
// 源: packages/preset/agent-presets/src/authoring.ts:101-112, 149-152
//
// 新增: DSH 是 `cp(..., { recursive, dereference, errorOnExist })` 加一趟事后
// tightenModes。这里只剩**顺链**这一件：判类型走 [statPath] 而不是列举给的那一行，
// 于是一个符号链接按它指的那个实体复制过来，副本自成一体，而不是一堆指回被复制的
// 那套安装的链接。收权限和可执行位都没有跟过来，理由见 [writeFile]。
//
// 整份读进内存再整份写出去，而不是流式对拷：一份预设是一个装着组合、说明和几个
// 技能文件的配置目录，而整份取回本来就是一个对象存储上的常态。
func copyTree(ctx context.Context, fsys fs.FileSystem, source, target string) error {
	info, found, err := statPath(ctx, fsys, source)
	if err != nil {
		return err
	}
	if !found || info.Type != fs.TypeDirectory {
		return fmt.Errorf("agent-presets: 源不是一个目录：%s", source)
	}
	if _, err := fsys.MakeDir(ctx, target, ""); err != nil {
		return err
	}
	children, _, err := listDir(ctx, fsys, source)
	if err != nil {
		return err
	}
	for _, child := range children {
		from := path.Join(source, child.Name)
		to := path.Join(target, child.Name)
		// 走 statPath 不看 child.Type：这里要的是这一行指到的那个实体——
		// 一个指向目录的链接要按目录整个复制过来。
		childInfo, childFound, err := statPath(ctx, fsys, from)
		if err != nil {
			return err
		}
		if !childFound {
			// 断链：源目录里的一个指不到东西的链接，跳过。把它复制成一个同样
			// 断掉的链接，等于让副本带上一件本来就坏的东西。
			continue
		}
		if childInfo.Type == fs.TypeDirectory {
			if err := copyTree(ctx, fsys, from, to); err != nil {
				return err
			}
			continue
		}
		if childInfo.Type != fs.TypeFile {
			// 设备、套接字、FIFO：一份预设目录里没有它们的位置，复制过去只会
			// 在副本里留下一件读者解释不了的东西。
			continue
		}
		content, err := readFile(ctx, fsys, from)
		if err != nil {
			return err
		}
		if err := writeFile(ctx, fsys, to, content); err != nil {
			return err
		}
	}
	return nil
}

// CopyComposition 靠整目录复制一份已有的预设来建一份新的。
//
// 源: packages/preset/agent-presets/src/authoring.ts:105-165（copyComposition）
//
// 副本带走源目录里的一切——组合、元数据、技能目录、素材——因为一份预设是它那个
// 目录，不是那一个文件。符号链接顺链取实体，好让副本自成一体。
//
// 复制过来的元数据随后被重写：源的**说明**留着（那个文件此后归作者自己编辑），
// 它的**名字**和名册**位次**不留——一份和源长得一模一样、或者被排进发出去那一套
// 声明位次里的副本，会让名册不再区分得开它们。没给名字、又没有说明可留时，那个
// 文件被删掉，于是副本什么都不发布，而不是发布一片空白。
//
// name 传空串表示不给名字，回落到显示 id。
//
// 新增: DSH 那次元数据写用的是 dsh-atomic-write。这里是一次普通的
// [fs.FileSystem.WriteBytes]——它本身就是原子发布的，而且落在一个**刚刚才建出来、
// 失败就整棵 [fs.FileSystem.RemoveTree] 掉**的目录里，写一半的风险已经由外面那层
// 全有或全无兜住了，再套一层临时文件加改名只是多一道看不出效果的动作。
func CopyComposition(
	ctx context.Context,
	fsys fs.FileSystem,
	roots []Root,
	source Preset,
	id string,
	name string,
) (string, error) {
	if !IsPresetID(id) {
		return "", &InvalidPresetIDError{PresetID: id}
	}
	root, err := WritableRoot(roots)
	if err != nil {
		return "", err
	}
	dir := path.Join(root, id)
	// 上游名册那道检查只看得见被发现出来的预设；一个没有组合文件的目录仍旧占着
	// 这个名字，它值得一句读得懂的拒绝，而不是一个存储层的错误码。
	if occupied(ctx, fsys, dir) {
		return "", &PresetExistsError{PresetID: id}
	}
	if err := copyAndStampMetadata(ctx, fsys, dir, source, name); err != nil {
		// 一个复制到一半的目录，好一点是发现看不见它，坏一点是一份装得起来却
		// 不完整的预设；一次失败的复制什么都不留下。撤销不带调用方的取消：
		// ctx 已经废了也得把写下去的收回来。
		_ = removeTree(context.WithoutCancel(ctx), fsys, dir)
		return "", err
	}
	return dir, nil
}

// copyAndStampMetadata 是 [CopyComposition] 里那段「失败就整个撤掉」的正文。
func copyAndStampMetadata(
	ctx context.Context, fsys fs.FileSystem, dir string, source Preset, name string,
) error {
	if err := copyTree(ctx, fsys, path.Dir(source.Path), dir); err != nil {
		return fmt.Errorf("agent-presets: 复制 %s 失败：%w", source.ID, err)
	}
	rendered, hasContent := RenderMetadata(Metadata{Name: name, Description: source.Description})
	metadataPath := path.Join(dir, MetadataFile)
	if !hasContent {
		if err := removePath(ctx, fsys, metadataPath); err != nil {
			return fmt.Errorf("agent-presets: 删不掉复制过来的展示文字：%w", err)
		}
		return nil
	}
	if err := writeFile(ctx, fsys, metadataPath, []byte(rendered)); err != nil {
		return fmt.Errorf("agent-presets: 写不了展示文字：%w", err)
	}
	return nil
}

// DeleteComposition 删掉一份本地创作的预设。
//
// 源: packages/preset/agent-presets/src/authoring.ts:167-191
//
// 随部署发出去的那一份被拒：它属于部署。一份**正有活会话装着**的预设**不拒**——
// 那份组合在建会话时就读完了、此后不再重读，所以那个会话照原样接着跑。
func DeleteComposition(ctx context.Context, fsys fs.FileSystem, roots []Root, preset Preset) error {
	if preset.Trust != TrustUser {
		return &PresetNotWritableError{PresetID: preset.ID, Reason: "it ships with the deployment"}
	}
	root, err := WritableRoot(roots)
	if err != nil {
		return err
	}
	dir := path.Join(root, preset.ID)
	// 在 id 文法之外再兜一道：不管发现报的是什么，这份预设的组合文件必须正好是
	// 可写根底下这个 id 的那一份。
	//
	// 新增: 上游那一支是 `isAbsolute` 加一次前缀比较。前缀比较在这里换成逐字相等，
	// 因为发现拼出来的就是这一条（见 [ScanRoot]）：一个前缀判据会让 `<root>/名字备份`
	// 通过 `<root>/名字` 那道检查，而这道检查守的正是一次 [fs.FileSystem.RemoveTree]
	// 的落点。
	// 「是不是绝对路径」这一问随之去掉——这道缝上的路径对本包不透明，只有实现方
	// 知道自己那份介质上什么叫绝对。
	if path.Clean(preset.Path) != path.Join(dir, CompositionFile) {
		return &PresetNotWritableError{
			PresetID: preset.ID,
			Reason:   "it does not live under the writable preset root",
		}
	}
	if err := removeTree(ctx, fsys, dir); err != nil {
		return fmt.Errorf("agent-presets: 删不掉 %s：%w", preset.ID, err)
	}
	return nil
}
