// 本文件的作用：本包认得出来的那几种失败。
//
// 新增: 整个文件都是本仓库自有的，理由见 doc.go。

package datastore

import (
	"errors"
	"fmt"
)

// 本包分得出来的几种失败。别的失败（连不上库、事务被中止、死锁）没有对应的哨兵，
// 原样往上冒。
//
// 新增: 用哨兵而不是分类码枚举，因为适配层要把它们翻成各自那套词汇
// （storage 有自己的封闭码表，session/persistence 有自己的哨兵），
// 而 errors.Is 是两边都认的那一种。
var (
	// ErrStreamNotFound 是「这条流不在这份介质里」。这是正常控制流，不是介质坏了。
	ErrStreamNotFound = errors.New("datastore: 流不在这份介质里")

	// ErrHeadConflict 是「这条流已经在了，而且它的头和这次要建的这份不一样」。
	//
	// 同一个流名底下换一份头是撞号，不是重试——重试给的是同一份头，本包认得出来。
	ErrHeadConflict = errors.New("datastore: 同一条流名底下有两份不一样的头")

	// ErrVersionMismatch 是「介质上盖着的版本号和这次要开的不一样」。
	//
	// 本包**只拒绝，一个字都不改**：一次被拒的打开要是顺手动了介质，
	// 「升级失败」就会连带把旧版本的数据毁掉。
	ErrVersionMismatch = errors.New("datastore: 介质上盖的版本号对不上")

	// ErrMalformedName 是「这个名字进不了 SQL 标识符」。见 [ValidName]。
	ErrMalformedName = errors.New("datastore: 名字不合法")

	// ErrMalformedMedium 是「介质上的东西不是本包写下去的样子」。
	ErrMalformedMedium = errors.New("datastore: 介质里的值坏了")

	// ErrClosed 是「这份介质或这个单元已经关掉了」。
	ErrClosed = errors.New("datastore: 已经关闭")

	// ErrAlreadyOpen 是「同一个单元名没关就开了第二次」。
	//
	// 这必须响：放过的话两个句柄各自持有一份状态，后写的把先写的覆盖掉，
	// 而两次写都「成功」了。
	ErrAlreadyOpen = errors.New("datastore: 这个单元已经开着了")
)

// failf 裹一个哨兵，附上说得清是哪一处的话。
func failf(sentinel error, format string, arguments ...any) error {
	return fmt.Errorf("%w：%s", sentinel, fmt.Sprintf(format, arguments...))
}
