// 本文件的作用：往盘上落字节的那几下——建目录、写暂存、发布、追加、回滚、
// 截断，以及在一个可能有人正在往里写的文件上读到一份自洽的字节。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:527-720

package jsonl

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/snight1983/ds-harness-go/session/persistence"
)

// ensureDurableDirectory 建出这个目录和它缺失的祖先，并让每一层的目录项本身耐久。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:552-557
// 源: packages/session/session-persistence-jsonl/src/win32.ts:122-142
//
// 新增: 上游按平台分成两条（POSIX 是 mkdir 加父目录 fsync，Windows 是
// 暂存目录加 MOVEFILE_WRITE_THROUGH）。递归这一层两边是同一份，所以留在这里，
// 只把「durably 建出一级」交给 [createLeafDirectory]。
//
// 不用 [os.MkdirAll]：那一下只保证目录**在**，不保证它的目录项已经耐久，
// 而这条路径存在的全部理由就是后者。
func ensureDurableDirectory(path string) error {
	info, err := os.Stat(path)
	switch {
	case err == nil && info.IsDir():
		return nil
	case err == nil:
		return fmt.Errorf("session/persistence/jsonl: %q 已经存在，但它不是一个目录", path)
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return fmt.Errorf("session/persistence/jsonl: 卷根 %q 不存在", path)
	}
	if err := ensureDurableDirectory(parent); err != nil {
		return err
	}
	return createLeafDirectory(parent, path)
}

// writeSyncedTempFile 把内容写进一个新建的暂存文件并 fsync，交回它的路径。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:622-632
//
// 名字里那六个随机字节是为了让两个同时在发布同一个身份的进程各写各的暂存；
// O_EXCL 兜住万一撞上的那次。
func writeSyncedTempFile(finalPath string, content []byte) (string, error) {
	var nonce [6]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("session/persistence/jsonl: 取不到随机数给暂存文件命名：%w", err)
	}
	tmp := finalPath + "." + hex.EncodeToString(nonce[:]) + ".tmp"
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	writeErr := writeAndSync(file, content)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// writeAndSync 把内容写下去并刷到耐久层。
func writeAndSync(file *os.File, content []byte) error {
	if _, err := file.Write(content); err != nil {
		return err
	}
	return file.Sync()
}

// appendDurably 追加一段字节并 fsync；写坏或者刷不下去就把长度还原成写之前那个。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:665-708
//
// 还原是要紧的：游标没动，这一批会被原样重试一次，留着半截字节就等于让同一个
// seq 在盘上出现两回。
func appendDurably(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return errors.Join(err, file.Close())
	}
	before := info.Size()

	writeErr := writeAndSync(file, content)
	closeErr := file.Close()
	if writeErr == nil {
		return closeErr
	}
	if err := truncateDurably(path, before); err != nil {
		return fmt.Errorf(
			"session/persistence/jsonl: 往 %q 追加失败之后又没能回滚：%w",
			path, errors.Join(writeErr, closeErr, err))
	}
	return errors.Join(writeErr, closeErr)
}

// truncateDurably 把一份存档截到指定长度并 fsync。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:700-720
//
// 追加失败后的回滚和崩溃修复时丢掉坏尾巴走的是同一下：两者都是「把这个位置
// 之后的字节当作从来没有过」。
func truncateDurably(path string, size int64) error {
	file, err := os.OpenFile(path, os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	truncErr := file.Truncate(size)
	if truncErr == nil {
		truncErr = file.Sync()
	}
	return errors.Join(truncErr, file.Close())
}

// readStableFile 读出一份存档此刻自洽的那段字节，连同它的变更令牌。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:294-314
//
// 一次和读并行的追加会让读到的字节横跨两个状态，所以这里读完再问一次令牌，
// 变了就整个重来。
func readStableFile(ctx context.Context, path string) ([]byte, persistence.Revision, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		before, err := statIdentity(path)
		if err != nil {
			return nil, "", err
		}
		buffer, err := os.ReadFile(path)
		if err != nil {
			return nil, "", err
		}
		after, err := statIdentity(path)
		if err != nil {
			return nil, "", err
		}
		if before.revision() == after.revision() {
			return buffer, after.revision(), nil
		}
	}
}

// readFirstLine 只读出一份存档第一条带换行的记录，不把整份日志拉进内存。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:724-753
//
// 列举就靠它：一份巨大的会话日志在那条路径上只花一次头的读。
// 第二个返回值为假表示这个文件是空的，或者根本没有一条完整的第一行。
func readFirstLine(path string) ([]byte, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()

	var line []byte
	chunk := make([]byte, 8192)
	for {
		read, err := file.Read(chunk)
		if read > 0 {
			if offset := bytes.IndexByte(chunk[:read], '\n'); offset >= 0 {
				return append(line, chunk[:offset]...), true, nil
			}
			line = append(line, chunk[:read]...)
		}
		switch {
		case errors.Is(err, io.EOF):
			// 读到头也没见着换行：这个文件没有一条完整的第一行。
			return nil, false, nil
		case err != nil:
			return nil, false, err
		}
	}
}

// exists 判一个路径上有没有东西。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:948-985
//
// 只有「不存在」算不存在。一次权限或者 I/O 故障必须冒出来，绝不能让装载或者
// 撞号检查顶着一个假的「没有」往下走。Windows 把「拿一个普通文件当目录用」
// 也报成不存在，所以那一步之后还要亲自验一眼父目录——不然一个被堵住的会话
// 目录会被读成一句「这个会话没有存档」。
func exists(path string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return true, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, err
	case !info.IsDir():
		return false, fmt.Errorf("session/persistence/jsonl: %q 存在，但它不是一个目录", parent)
	}
	return false, nil
}
