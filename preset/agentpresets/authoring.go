// 本文件的作用：本地创作——复制、读取、删除一份预设，以及为什么这三件事全都
// 只碰得到 user 那个根。
//
// 源: packages/preset/agent-presets/src/authoring.ts

package agentpresets

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
// 源: packages/preset/agent-presets/src/authoring.ts:36-46
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
// 源: packages/preset/agent-presets/src/authoring.ts:65-71
//
// 也就是按优先级排下来第一个 user 根。一个都没有时报 [PresetNotWritableError]：
// 随部署发出去的那一套属于部署，让一个浏览器改得动它，等于把「重置回一份已知的
// 预设」变成同一个调用方先前就能弄坏的东西。
func WritableRoot(roots []Root) (string, error) {
	for _, root := range roots {
		if root.Trust == TrustUser {
			return filepath.Clean(root.Path), nil
		}
	}
	return "", &PresetNotWritableError{
		Reason: "this deployment configures no user-writable preset root",
	}
}

// ReadComposition 读一份预设的组合文本。
//
// 源: packages/preset/agent-presets/src/authoring.ts:78-80
func ReadComposition(preset Preset) (string, error) {
	content, err := os.ReadFile(preset.Path)
	if err != nil {
		return "", fmt.Errorf("agent-presets: 读不了 %s 的组合：%w", preset.ID, err)
	}
	return string(content), nil
}

// occupied 判一条路径上有没有东西占着。
//
// 源: packages/preset/agent-presets/src/authoring.ts:83-93
//
// 任何 stat 失败在这里是同一件事：没有可用的东西占着这条路径，复制可以认领它。
func occupied(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// copyTree 把 source 整个目录复制到 target，顺链取实体、并把权限收成只给属主。
//
// 源: packages/preset/agent-presets/src/authoring.ts:101-112, 149-152
//
// 新增: DSH 是 `cp(..., { recursive, dereference, errorOnExist })` 加一趟事后
// tightenModes。Go 的标准库没有递归复制——[io/fs.WalkDir] 加 [io.Copy] 就是它的
// 办法——所以两件事合成一趟走完：**顺链**（用 os.Stat 而不是 Lstat 判类型，于是
// 一个符号链接按它指的那个实体复制过来）让副本自成一体，而不是一堆指回被复制的
// 那套安装的链接；**收权限**是因为随部署发出去的预设在它的安装里通常是所有人可读，
// 而副本和它旁边那份设置文档是同一个分量，所以组和其他人的位一律剥掉。文件的属主
// 执行位留着——一份预设可以带可执行的辅助脚本。
func copyTree(source, target string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("agent-presets: 源不是一个目录：%s", source)
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	children, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, child := range children {
		from := filepath.Join(source, child.Name())
		to := filepath.Join(target, child.Name())
		// 用 Stat 不用 child.IsDir()：后者说的是链接本身，而这里要的是它指的
		// 那个实体——一个指向目录的链接要按目录整个复制过来。
		childInfo, err := os.Stat(from)
		if err != nil {
			// 断链：源目录里的一个指不到东西的链接，跳过。把它复制成一个同样
			// 断掉的链接，等于让副本带上一件本来就坏的东西。
			continue
		}
		if childInfo.IsDir() {
			if err := copyTree(from, to); err != nil {
				return err
			}
			continue
		}
		if !childInfo.Mode().IsRegular() {
			// 设备、套接字、FIFO：一份预设目录里没有它们的位置，复制过去只会
			// 在副本里留下一件读者解释不了的东西。
			continue
		}
		mode := os.FileMode(0o600)
		if childInfo.Mode()&0o100 != 0 {
			mode = 0o700
		}
		if err := copyFile(from, to, mode); err != nil {
			return err
		}
	}
	return nil
}

// copyFile 把一个常规文件复制过去，落成指定权限。
func copyFile(from, to string, mode os.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// CopyComposition 靠整目录复制一份已有的预设来建一份新的。
//
// 源: packages/preset/agent-presets/src/authoring.ts:136-170
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
// 新增: DSH 那次元数据写用的是 dsh-atomic-write。这里是普通的 os.WriteFile——
// 它落在一个**刚刚才建出来、失败就整个 RemoveAll 掉**的目录里，写一半的风险已经
// 由外面那层全有或全无兜住了，再套一层临时文件加改名只是多一道看不出效果的动作。
func CopyComposition(roots []Root, source Preset, id string, name string) (string, error) {
	if !IsPresetID(id) {
		return "", &InvalidPresetIDError{PresetID: id}
	}
	root, err := WritableRoot(roots)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, id)
	// 上游名册那道检查只看得见被发现出来的预设；一个没有组合文件的目录仍旧占着
	// 这个名字，它值得一句读得懂的拒绝，而不是一个文件系统错误码。
	if occupied(dir) {
		return "", &PresetExistsError{PresetID: id}
	}
	if err := copyAndStampMetadata(dir, source, name); err != nil {
		// 一个复制到一半的目录，好一点是发现看不见它，坏一点是一份装得起来却
		// 不完整的预设；一次失败的复制什么都不留下。
		_ = os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

// copyAndStampMetadata 是 [CopyComposition] 里那段「失败就整个撤掉」的正文。
func copyAndStampMetadata(dir string, source Preset, name string) error {
	if err := copyTree(filepath.Dir(source.Path), dir); err != nil {
		return fmt.Errorf("agent-presets: 复制 %s 失败：%w", source.ID, err)
	}
	rendered, hasContent := RenderMetadata(Metadata{Name: name, Description: source.Description})
	metadataPath := filepath.Join(dir, MetadataFile)
	if !hasContent {
		if err := os.Remove(metadataPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("agent-presets: 删不掉复制过来的展示文字：%w", err)
		}
		return nil
	}
	if err := os.WriteFile(metadataPath, []byte(rendered), 0o600); err != nil {
		return fmt.Errorf("agent-presets: 写不了展示文字：%w", err)
	}
	return nil
}

// DeleteComposition 删掉一份本地创作的预设。
//
// 源: packages/preset/agent-presets/src/authoring.ts:182-196
//
// 随部署发出去的那一份被拒：它属于部署。一份**正有活会话装着**的预设**不拒**——
// 那份组合在建会话时就读完了、此后不再重读，所以那个会话照原样接着跑。
func DeleteComposition(roots []Root, preset Preset) error {
	if preset.Trust != TrustUser {
		return &PresetNotWritableError{PresetID: preset.ID, Reason: "it ships with the deployment"}
	}
	root, err := WritableRoot(roots)
	if err != nil {
		return err
	}
	dir := filepath.Join(root, preset.ID)
	// 在 id 文法之外再兜一道：不管发现报的是什么，解算出来的这个目录必须仍旧是
	// 可写根拥有的那一个。
	if !filepath.IsAbs(preset.Path) || !strings.HasPrefix(filepath.Clean(preset.Path), dir) {
		return &PresetNotWritableError{
			PresetID: preset.ID,
			Reason:   "it does not live under the writable preset root",
		}
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("agent-presets: 删不掉 %s：%w", preset.ID, err)
	}
	return nil
}
