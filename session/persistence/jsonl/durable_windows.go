// 本文件的作用：在 Windows 上把一个新对象耐久地发布到它最终的名字上。
//
// 源: packages/session/session-persistence-jsonl/src/win32.ts:1-155

package jsonl

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"golang.org/x/sys/windows"
)

// stagingPrefix 是那些一造出来就要被改名的暂存目录的名字前缀。
//
// 源: packages/session/session-persistence-jsonl/src/win32.ts:147
//
// 它**不**从目标的名字派生：一个合法的 255 字节目标名会让「目标名加前缀」
// 这种暂存名越过分量长度上限。
const stagingPrefix = ".dsh-mkdir-"

// publishNewFile 把一个已经 fsync 过的暂存对象发布到最终位置。
//
// 源: packages/session/session-persistence-jsonl/src/win32.ts:109-120
//
// 走的是 MoveFileEx 加 MOVEFILE_WRITE_THROUGH：POSIX 那边靠「建目录项再 fsync
// 父目录」拿到的耐久性，Windows 不通过那条契约给，而是由这个原语自己给。
// **不带** MOVEFILE_REPLACE_EXISTING（目标已存在就失败，那正是撞号）、
// 也**不带** MOVEFILE_COPY_ALLOWED（不允许悄悄跨卷复制一份）。
//
// 新增: DSH 用 koffi 把 kernel32 的 MoveFileExW 引进来。这里走
// [windows.MoveFileEx]，同一个调用；标准库的 syscall 只露出不带标志位的
// MoveFile，拿不到写穿，而写穿正是这条路径存在的理由。
func publishNewFile(staging, final string) error {
	from, err := windows.UTF16PtrFromString(staging)
	if err != nil {
		return &os.LinkError{Op: "MoveFileEx", Old: staging, New: final, Err: err}
	}
	to, err := windows.UTF16PtrFromString(final)
	if err != nil {
		return &os.LinkError{Op: "MoveFileEx", Old: staging, New: final, Err: err}
	}
	if err := windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return &os.LinkError{Op: "MoveFileEx", Old: staging, New: final, Err: err}
	}
	return nil
}

// createLeafDirectory 耐久地建出一级目录：先在父目录里造一个随机名的暂存目录，
// 再用写穿的改名把它挪到最终的名字上。
//
// 源: packages/session/session-persistence-jsonl/src/win32.ts:144-155
//
// 和另一个创建者撞上是可以接受的——但只有在确认赢家真的是一个目录之后。
func createLeafDirectory(parent, target string) error {
	staging, err := os.MkdirTemp(parent, stagingPrefix)
	if err != nil {
		return err
	}
	if err := publishNewFile(staging, target); err != nil {
		_ = os.RemoveAll(staging)
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		info, statErr := os.Stat(target)
		if statErr != nil {
			return errors.Join(err, statErr)
		}
		if !info.IsDir() {
			return fmt.Errorf("session/persistence/jsonl: %q 已经存在，但它不是一个目录", target)
		}
	}
	return nil
}
