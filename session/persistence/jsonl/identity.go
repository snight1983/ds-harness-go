// 本文件的作用：一份存档的变更令牌从盘上的哪几样东西算出来。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:93-110

package jsonl

import (
	"fmt"

	"github.com/snight1983/ds-harness-go/session/persistence"
)

// fileIdentity 是一份存档在文件系统眼里的身份，也是它那个变更令牌的全部原料。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:93-99
//
// 挑这五样是为了让令牌**来源限定**（见 [persistence.Revision]）：设备号加索引号
// 把两个各自独立的存储分开，长度和两个时间戳把同一份存档的前后两个状态分开。
// 光靠长度和修改时间不够——一次「截掉坏尾巴再补上收尾」完全可能把长度还原成
// 原来那个数。
type fileIdentity struct {
	// device 是这份存档所在的那个卷。
	device string
	// index 是它在那个卷里的索引号（POSIX 的 inode，Windows 的 file index）。
	index string
	// size 是它此刻的字节数。
	size int64
	// modified 是最后一次写入的时刻，纳秒。
	modified int64
	// created 是创建时刻，纳秒；拿不到这一样的平台上是零。
	created int64
}

// revision 把一份身份折成那个不透明的变更令牌。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:101-110
func (i fileIdentity) revision() persistence.Revision {
	return persistence.Revision(fmt.Sprintf(
		"%s:%s:%d:%d:%d", i.device, i.index, i.size, i.modified, i.created))
}
