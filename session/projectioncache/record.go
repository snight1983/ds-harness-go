// 本文件的作用：介质上那条记录长什么样——它绑着哪一段日志、装着哪些检查点行，
// 以及这个域自己的声明。
//
// 源: packages/session/session-projection-cache/src/spec.ts

package projectioncache

import (
	"encoding/json"
	"fmt"

	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/session/projection"
	"github.com/snight1983/ds-harness-go/storage/domain"
)

// DomainName 是这个缓存在存储上占的那个域名。
//
// 源: packages/session/session-projection-cache/src/spec.ts:67
const DomainName = "session_projcache"

// DomainVersion 是这个域的格式版本。
//
// 源: packages/session/session-projection-cache/src/spec.ts:68
//
// 版本一变，介质上那一整份都被丢掉——这正是缓存语义允许的：一份读不回来的
// 缓存只值一次更长的尾巴重放，绝不会换来一个错的值。所以这里永远不会有迁移代码。
const DomainVersion = 3

// TableName 是这个域里唯一那张表，按会话 id 索引。
//
// 源: packages/session/session-projection-cache/src/spec.ts:69
const TableName = "sessions"

// Identity 是一条记录绑定的那段日志的身份。
//
// 源: packages/session/session-projection-cache/src/spec.ts:45-46（CheckpointIdentity）
//
// 会话 id 是一个**槽位**不是一段生命。删掉再重建同一个 id、或者在缓存还活着的
// 时候把持久化根目录换到另一份存储上，都会让一条旧记录通过所有的版本和水位检查，
// 然后把一段毫无关系的日志折出来的状态当成这个会话的当前值端出去。所以每一次读
// 都要拿调用方手上那份头（列表读拿活会话的头，冷读拿存档里的头）来核对身份。
//
// 新增: 这是一个**可比较**的结构体，核对身份就是一次 ==。DSH 那边 cwd 是
// `string | undefined`，要写一个逐字段的 identityMatches；Go 里「没给 cwd」
// 就是空串，两者本来就是同一件事（见 [session.SessionHeader.Cwd]）。
type Identity struct {
	// CreatedAt 是建会话时的 Unix 纪元毫秒。
	CreatedAt int64 `json:"createdAt"`
	// Cwd 是建会话时的绝对工作目录；空串表示没有。
	Cwd string `json:"cwd,omitempty"`
}

// IdentityOf 把一份会话头投影成它的日志身份。
//
// 源: packages/session/session-projection-cache/src/index.ts:289-292
func IdentityOf(header session.SessionHeader) Identity {
	return Identity{CreatedAt: header.CreatedAt, Cwd: header.Cwd}
}

// Record 是一个会话在介质上那条记录：它折自哪一段日志，加上每个单元的检查点行。
//
// 源: packages/session/session-projection-cache/src/spec.ts:59-60（CheckpointRecord）
//
// 每次写都**整条替换**。[projection.Registry.Checkpoint] 交出来的本来就是一个
// 会话上的完整切面，没有「只更新其中一个键」这种写法——那样会写出一条各个键停在
// 不同 seq 上的记录，而读的那一侧没有任何办法发现。
type Record struct {
	// Identity 是这条记录绑定的日志身份。
	Identity Identity `json:"identity"`
	// Rows 是按投影键索引的检查点行。
	Rows projection.Checkpoint `json:"rows"`
}

// ValidateRecord 检查一条记录立不立得住，从介质读回来和往介质写下去都要过它。
//
// 源: packages/session/session-projection-cache/src/spec.ts:24-28
//
// 它守的是 [projection.CheckpointRow] 自己的取值范围：版本号非负，水位不小于
// -1（-1 是「一条都没折过」），值是一段合法的 JSON。这三条在读侧尤其要紧——
// 介质上的字节可能来自任何一个历史构建，而一条越界的行会被恢复路径当成正常行用。
func ValidateRecord(record Record) error {
	if record.Identity.CreatedAt < 0 {
		return fmt.Errorf("记录的建会话时刻是 %d，不能是负数", record.Identity.CreatedAt)
	}
	for key, row := range record.Rows {
		if row.Ver < 0 {
			return fmt.Errorf("投影键 %q 的状态版本号是 %d，不能是负数", key, row.Ver)
		}
		if row.Seq < -1 {
			return fmt.Errorf("投影键 %q 的水位是 %d，不能小于 -1", key, row.Seq)
		}
		if !json.Valid(row.Val) {
			return fmt.Errorf("投影键 %q 的状态不是一段合法的 JSON", key)
		}
	}
	return nil
}

// Spec 是这个缓存要的那份域声明，交给 storage/domain 的设施去打开。
//
// 源: packages/session/session-projection-cache/src/spec.ts:66-70
//
// 新增: DSH 那边 projectionCacheDomainSpec 是一个模块级常量。Go 里做成函数，
// 因为 [domain.Spec] 里带一个切片——一个包级变量会让任何一个拿到它的人改到
// 所有人共用的那一份。
func Spec() domain.Spec {
	return domain.Spec{
		Name:    DomainName,
		Version: DomainVersion,
		Tables:  []domain.TableSpec{domain.DefineTable(TableName, ValidateRecord)},
	}
}
