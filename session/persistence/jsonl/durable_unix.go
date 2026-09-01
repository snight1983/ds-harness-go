//go:build !windows

// 本文件的作用：在 POSIX 上把一个新对象耐久地发布到它最终的名字上。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:544-586

package jsonl

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// publishNewFile 把一个已经 fsync 过的暂存对象发布到最终位置。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:559-584
//
// 用 link 而**不是** rename：目标已存在时 link 报 EEXIST，于是两个同时在发布
// 同一个身份的进程不可能互相盖掉。rename 会默默盖掉它——而这里的目标已存在
// 恰恰意味着盘上另有一个会话和它撞了号。
//
// 发布之后必须 fsync 那个目录：新建的目录项在父目录的元数据落盘之前挺不过一次
// 掉电。暂存那条硬链接留到那之后才删，于是一次删除失败不可能否掉一份已经
// 发布好的日志。
func publishNewFile(staging, final string) error {
	if err := os.Link(staging, final); err != nil {
		return err
	}
	if err := syncDir(filepath.Dir(final)); err != nil {
		return err
	}
	// 日志已经发布、已经耐久，那条多出来的暂存链接删不掉也不许否掉这次追加。
	_ = os.Remove(staging)
	return nil
}

// createLeafDirectory 耐久地建出一级目录：建出来，再 fsync 它的父目录。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:552-557
func createLeafDirectory(parent, target string) error {
	if err := os.Mkdir(target, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return syncDir(parent)
}

// syncDir fsync 一个目录，让刚建出来或者刚改名过去的目录项挺过一次掉电。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:653-663
func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	syncErr := handle.Sync()
	return errors.Join(syncErr, handle.Close())
}
