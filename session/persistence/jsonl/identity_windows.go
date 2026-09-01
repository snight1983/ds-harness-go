// 本文件的作用：在 Windows 上问出一份存档的文件系统身份。
//
// 新增: DSH 走的是 Node 的 `stat(path, { bigint: true })`，它在两个平台上都给出
// dev/ino。Go 的 [os.FileInfo] 没有这两样的可移植形态，所以按平台各写一份，
// 做法和本包 durable_windows.go 那一对相同。

package jsonl

import (
	"os"
	"strconv"
	"syscall"
)

// statIdentity 问出一份存档的文件系统身份。
//
// 卷序号加文件索引号是 NTFS 上 dev/ino 的对应物，由
// GetFileInformationByHandle 一次性给出——它走的是一个已经打开的句柄，
// 所以不会在路径解析上和一次并发的重命名擦肩。
func statIdentity(path string) (fileIdentity, error) {
	file, err := os.Open(path)
	if err != nil {
		return fileIdentity{}, err
	}
	defer file.Close()

	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(file.Fd()), &info); err != nil {
		return fileIdentity{}, &os.PathError{Op: "GetFileInformationByHandle", Path: path, Err: err}
	}
	return fileIdentity{
		device:   strconv.FormatUint(uint64(info.VolumeSerialNumber), 10),
		index:    strconv.FormatUint(uint64(info.FileIndexHigh)<<32|uint64(info.FileIndexLow), 10),
		size:     int64(info.FileSizeHigh)<<32 | int64(info.FileSizeLow),
		modified: info.LastWriteTime.Nanoseconds(),
		created:  info.CreationTime.Nanoseconds(),
	}, nil
}
