// 本文件的作用：把本包那几条不透明路径接到 [fs.FileSystem] 上，
// 顺带把「路径 → 目标」这一步的重复收在一处。
//
// 新增: 上游 DSH 是一个单机 CLI，预设就是用户 home 底下的目录，`node:fs` 直接读。
// 这条移植原样抄过，于是 discovery/metadata/mount/authoring 四个文件里散着二十处
// `os.*` 调用，把「一份预设」这件事钉死在**跑着这个进程的那台机器的磁盘**上。
// 服务化之后这是错的，和会话事件曾经落在本地 session.jsonl 上是同一个病：
// 每个节点都得有这些目录，扩一个副本要先同步一遍磁盘，而这份内容本身和某台机器
// 没有任何关系。
//
// 第一版给本包单开了一道 Store 接缝（七个方法）。那是错的第二遍：它和
// [fs.FileSystem] 方法一一对应地重复，于是「来一个新介质」要在两个地方各实现一遍，
// 而两份实现必然慢慢长歪。现在只剩 [fs.FileSystem] 一条缝，本包退回成它的消费方。
//
// 本文件这几个函数都是薄的：它们只做 [fs.FileSystem.Resolve] 那一步，外加把
// 「不在」从错误折回一个布尔——那是本包四个调用点反复要的形状，而接缝上
// 有些方法（[fs.FileSystem.ListDir]）把它表达成一个带码的错误。

package agentpresets

import (
	"context"
	"errors"

	"github.com/snight1983/ds-harness-go/fs"
)

// maxPresetFileBytes 是本包肯读进内存的单份内容上限。
//
// 新增: [fs.FileSystem.ReadBytes] 要求调用方报一个上限，好让后端永远不可能
// 把一个无界大的对象缓冲进来。一份预设是一个装着组合、说明和几个技能文件的
// **配置目录**，正经内容离这个数还差好几个数量级；超过它的那一份会响亮地失败，
// 而不是被截断之后当成完整内容拿去解析。
const maxPresetFileBytes = 8 << 20

// statPath 顺链看一条路径上是什么；不在时第二个返回值是 false，且不算错误。
//
// 「不存在」和「读不了」必须分得开：前者是常态（用户根在第一份本地创作出现
// 之前都不存在），后者是这套部署配错了。
func statPath(ctx context.Context, fsys fs.FileSystem, at string) (fs.Info, bool, error) {
	target, err := fsys.Resolve(ctx, at, "")
	if err != nil {
		return fs.Info{}, false, err
	}
	return fsys.Stat(ctx, target)
}

// listDir 列出一个目录的直接子项，按名字升序；不是一个存在的目录时第二个返回值是 false。
//
// 新增: 接缝那边把「不在」和「不是目录」都说成一个带码的错误（[fs.CodeNotFound]、
// [fs.CodeNotDirectory]），这里折回布尔——本包唯一的列举点（发现预设）对这两种情形
// 的处置一模一样：交出零份预设。
//
// 新增: 交出来的这几行**跟着符号链接**走，而先前那道 Store 缝上是不跟的
// （[fs.DirEntry.Type] 按接缝的约定永远不会是 [fs.TypeSymlink]）。差别只落在
// [ScanRoot]：一个指向目录的链接从此也算一份候选预设。这是有意接受的——
// 唯一的生产后端 [github.com/snight1983/ds-harness-go/fs/objectstore.Store] 上
// 压根没有符号链接这回事，为一种在这套部署里不存在的介质保留一条分支，
// 换来的是本包重新拥有一道自己的缝。
func listDir(ctx context.Context, fsys fs.FileSystem, dir string) ([]fs.DirEntry, bool, error) {
	target, err := fsys.Resolve(ctx, dir, "")
	if err != nil {
		return nil, false, err
	}
	entries, err := fsys.ListDir(ctx, target)
	if err != nil {
		var typed *fs.Error
		if errors.As(err, &typed) && (typed.Code == fs.CodeNotFound || typed.Code == fs.CodeNotDirectory) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return entries, true, nil
}

// readFile 读出整份内容，上限见 [maxPresetFileBytes]。
func readFile(ctx context.Context, fsys fs.FileSystem, at string) ([]byte, error) {
	target, err := fsys.Resolve(ctx, at, "")
	if err != nil {
		return nil, err
	}
	return fsys.ReadBytes(ctx, target, maxPresetFileBytes)
}

// writeFile 写下整份内容，路径上已经有东西就覆盖。
//
// 新增: 先前那道 Store 缝上这里带一个 executable 位。它没有跟过来，理由见
// [fs.FileSystem.WriteBytes]：一个对象存储答不出「这份内容可不可执行」，
// 编一个出来会让上层那些判断在那份介质上悄悄地永远成立或永远不成立。
// 后果是一份带可执行辅助脚本的预设，复制出来的副本上那一位没了。
func writeFile(ctx context.Context, fsys fs.FileSystem, at string, content []byte) error {
	target, err := fsys.Resolve(ctx, at, "")
	if err != nil {
		return err
	}
	_, err = fsys.WriteBytes(ctx, target, content, nil)
	return err
}

// removePath 删掉一条路径上的单个条目；不在不算错。
func removePath(ctx context.Context, fsys fs.FileSystem, at string) error {
	target, err := fsys.Resolve(ctx, at, "")
	if err != nil {
		return err
	}
	return fsys.Remove(ctx, target)
}

// removeTree 删掉一棵子树；不在不算错。
//
// 它是「一次失败的复制什么都不留下」那条撤销路唯一的依靠，所以**必须**在
// 部分写入之后仍然清得干净，见 [fs.FileSystem.RemoveTree]。
func removeTree(ctx context.Context, fsys fs.FileSystem, dir string) error {
	target, err := fsys.Resolve(ctx, dir, "")
	if err != nil {
		return err
	}
	return fsys.RemoveTree(ctx, target)
}
