//go:build !windows

// 本文件的作用：在 POSIX 上问出一份存档的文件系统身份。

package jsonl

import (
	"fmt"
	"os"
	"syscall"
)

// statIdentity 问出一份存档的文件系统身份。
//
// 新增: 上游那五样里的 ctime 这里是零：[syscall.Stat_t] 上那个字段的名字逐平台
// 各不相同（Ctim / Ctimespec），而设备号、索引号、长度、修改时刻这四样已经
// 足以把同一份存档的前后两个状态、以及两个独立的存储分开。
func statIdentity(path string) (fileIdentity, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileIdentity{}, err
	}
	raw, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileIdentity{}, &os.PathError{
			Op:   "stat",
			Path: path,
			Err:  fmt.Errorf("这个平台的 stat 不给设备号和索引号"),
		}
	}
	return fileIdentity{
		device:   fmt.Sprint(raw.Dev),
		index:    fmt.Sprint(raw.Ino),
		size:     info.Size(),
		modified: info.ModTime().UnixNano(),
	}, nil
}
