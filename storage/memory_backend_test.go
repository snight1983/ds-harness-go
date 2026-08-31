// 本文件的作用：把参考后端的两个构造器接到这个包的用例里。
//
// 实现本身在 storagetest/memory.go——它搬出去是因为每一个数据形态包都要用同一个后端
// （见那个文件顶部的说明），而 _test.go 里的东西别的包导不到。
// 这里留两个短名字，是为了让本包用例里那几十处调用保持读起来像本地的东西。
package storage_test

import "ds-harness-go/storage/storagetest"

func newMemoryMedium() *storagetest.MemoryMedium { return storagetest.NewMemoryMedium() }

func newMemoryBackend(medium *storagetest.MemoryMedium) *storagetest.MemoryBackend {
	return storagetest.NewMemoryBackend(medium)
}
