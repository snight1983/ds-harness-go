// 本文件的作用：写给**后端实现者**看的那份契约。
//
// 源: packages/storage/storage/src/backend.ts:1-7
//
// 一个后端拥有一份介质（一棵文件树的根、一个数据库文件），并在它上面提供若干组操作。
// 这里定下的每一条规则，都由 storagetest 那份共用一致性测试逐条检查——契约写在注释里
// 而没有测试压着的话，两个后端的行为会在没人察觉的情况下分叉。

package storage

import (
	"context"
	"encoding/json"
	"regexp"
)

// unitNamePattern 是单元名和表名的合法形状。
//
// 源: packages/storage/storage/src/backend.ts:9-10
//
// 这个形状同时要满足两件事：当文件名安全，以及不转义就能当 SQL 标识符的一段。
// 两个现有后端一个落在文件树上、一个落在 SQLite 上，取的是两者的交集。
//
// 新增: DSH 把这个正则本身导出（UNIT_NAME_RE）。Go 里导出一个 *regexp.Regexp 变量
// 等于把它交出去让人改——包级可变状态谁都能重新赋值，而这是一条所有后端共用的规则。
// 所以只导出判定函数 [ValidUnitName]。
var unitNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// ValidUnitName 判断一个单元名或表名是否合法。
//
// 源: packages/storage/storage/src/backend.ts:9-10
func ValidUnitName(name string) bool { return unitNamePattern.MatchString(name) }

// Backend 是一个已注册的后端。
//
// 源: packages/storage/storage/src/backend.ts:12-27
//
// 一个后端**只拥有一份介质**，所有数据形态共享它的生命周期。
//
// 数据形态是可选的：一个后端服务不了某种数据，就干脆不提供它，
// 由解析那一刻明确地失败，而不是提供一个会在第一次调用时炸掉的空壳。
// Go 里这件事的表达方式见 [KV]。
type Backend interface {
	// Close 把所有已打开单元上在途的写排干，然后释放介质。
	//
	// **幂等**：重复调用和并发调用都必须在拆解完成后正常返回，而不是第二次报错。
	// 幂等是必需的，因为关闭常常同时来自正常收尾和错误清理两条路，
	// 而这两条路谁先到是不确定的。
	Close(ctx context.Context) error
}

// KVProvider 是「这个后端提供键值形态」这件事的表达。
//
// 源: packages/storage/storage/src/backend.ts:18-19
//
// DSH 那边是 StorageBackend 上一个可选成员 `kv?`。Go 的结构体没有「可选成员」，
// 接口也没有——所以改成和 dirpicker 同一个做法：基础接口 + 一个更宽的接口，
// 提供得了就满足那个更宽的，提供不了就只满足基础的。
type KVProvider interface {
	Backend

	// KV 返回这个后端的键值操作组。
	KV() KVFacet
}

// KV 在后端确实提供键值形态时把它取出来。
//
// 源: packages/storage/storage/src/backend.ts:12-16
//
// 第二个返回值为 false 表示这个后端服务不了键值数据。**这不是一个错误码**，
// 和 DSH 保持一致：那边写的是 `backend.kv!.open(...)`，缺了就是当场的类型错误，
// 而 StorageErrorCode 那个封闭词汇里从来没有它。
//
// 新增: DSH 靠 `!` 断言，缺了会炸成一句 "cannot read property of undefined"——
// 那句话既说不出是哪个后端，也说不出缺的是哪种形态。这里返回 (facet, ok)，
// 调用方拿 false 自己失败，报什么话由它决定。
func KV(backend Backend) (KVFacet, bool) {
	provider, ok := backend.(KVProvider)
	if !ok {
		return nil, false
	}
	facet := provider.KV()
	// 满足接口但返回 nil 的后端，和不满足接口的后端是同一件事：都服务不了。
	// 不查的话，调用方会拿着一个 nil 接口值走下去，在远处炸。
	if facet == nil {
		return nil, false
	}
	return facet, true
}

// KVFacet 是键值形态：整单元快照读，加上按记录的持久化写。
//
// 源: packages/storage/storage/src/backend.ts:29-43
type KVFacet interface {
	// Open 打开一个单元，介质上还没有它的任何痕迹时就建一个。
	//
	// 允许把真正的落盘推迟到第一次写，但 [KVUnit.LoadAll] **必须立刻**就能给出
	// 那个空形状——「还没建出来」是后端自己的实现细节，不该漏给调用方。
	//
	// 介质上已经盖着的版本号和 descriptor.Version 不一样时，返回 Code 为
	// [CodeVersionMismatch] 的 *[Error]；介质解析不出这个单元该有的形状时，
	// 返回 [CodeMalformedMedium]。
	//
	// 同一个单元名没关就开第二次是调用方的 bug，必须报错——放过的话，
	// 两个句柄会各自持有一份状态，后写的那个把先写的覆盖掉，且两次写都「成功」了。
	Open(ctx context.Context, descriptor KVUnitDescriptor) (KVUnit, error)
}

// KVUnitDescriptor 是一个键值单元的静态身份和形状。
//
// 源: packages/storage/storage/src/backend.ts:45-55
type KVUnitDescriptor struct {
	// Name 是单元名，必须满足 [ValidUnitName]。它同时也是文件名/SQL 标识符的那一段。
	Name string
	// Version 是单元的格式版本，一个非负整数，在第一次落盘时被盖到介质上。
	Version int
	// Tables 是表名，每一个都必须满足 [ValidUnitName]。
	Tables []string
	// HasGlobal 表示这个单元带一个全局单例槽。
	HasGlobal bool
}

// Validate 检查这份描述符本身是否合法。
//
// 新增: DSH 把「必须匹配 UNIT_NAME_RE」写在注释里，由每个后端各自去查。
// 那样两个后端很容易查得不一样（一个查了表名一个没查），而查漏的后果是一个
// 带斜杠的表名被当成文件路径的一段——它会安静地写到另一个目录里去。
// 放在共用的地方查一次，两个后端就不可能分叉。
func (d KVUnitDescriptor) Validate() error {
	if !ValidUnitName(d.Name) {
		return newError(CodeMalformedMedium,
			"单元名 %q 不合法：必须是小写字母开头，之后只能是小写字母、数字或下划线", d.Name)
	}
	if d.Version < 0 {
		return newError(CodeMalformedMedium, "单元 %q 的版本号是 %d，不能是负数", d.Name, d.Version)
	}
	seen := make(map[string]struct{}, len(d.Tables))
	for _, table := range d.Tables {
		if !ValidUnitName(table) {
			return newError(CodeMalformedMedium,
				"单元 %q 的表名 %q 不合法：必须是小写字母开头，之后只能是小写字母、数字或下划线",
				d.Name, table)
		}
		if _, duplicate := seen[table]; duplicate {
			// 重名的表在快照里会塌成一张，而声明它的人以为有两张。
			return newError(CodeMalformedMedium, "单元 %q 里的表名 %q 重复了", d.Name, table)
		}
		seen[table] = struct{}{}
	}
	return nil
}

// Snapshot 是一个单元当前的完整内容。
//
// 源: packages/storage/storage/src/backend.ts:67-72
type Snapshot struct {
	// Tables 是每张表的记录，按表名索引。声明过但一条记录都没有的表，
	// 这里是一个**空 map 而不是缺席**——缺席和空在调用方那里会走不同的分支。
	Tables map[string]map[string]json.RawMessage
	// Global 是全局单例槽。从没写过、或者压根没声明过时为 nil。
	Global json.RawMessage
}

// KVUnit 是一个已打开的单元。
//
// 源: packages/storage/storage/src/backend.ts:57-104
//
// 值对这一层来说是**不透明的 JSON**：没有 schema、没有事件、没有任何领域含义。
//
// 单元**不负责**把并发的写串起来——写的顺序是调用方的事（领域层对每个单元跑一条写链）。
// 单元只保证两件事：每一次单独的调用在介质上是原子的，以及调用返回之后它是持久的
// （返回之后崩溃，重新打开能看到这次写）。
//
// [Close] 之后的任何调用都必须返回 Code 为 [CodeClosed] 的 *[Error]。
//
// 新增: 值的类型是 json.RawMessage 而不是 any。这一层自陈「不解释值」，
// 而 any 会逼着它在某处做一次编解码——那次编解码就是解释。更要命的是，
// 一个 Go 里编不出来的值（chan、func、循环引用）在 any 下要等到写的那一刻才失败，
// 而那时它已经在事务里了。RawMessage 把「这是一段已经成型的 JSON」变成类型层面的事实。
type KVUnit interface {
	// LoadAll 读出当前的完整快照。
	LoadAll(ctx context.Context) (Snapshot, error)

	// PutRecord 持久地写入一条记录。**覆盖语义**：已存在的键被替换。
	//
	// key 可以是任意字符串——记录键永远不会出现在文件路径里，这是后端的义务。
	PutRecord(ctx context.Context, table, key string, value json.RawMessage) error

	// DeleteRecord 持久地删掉一条记录。**幂等**：键不存在就什么也不做，不是错误。
	DeleteRecord(ctx context.Context, table, key string) error

	// SetGlobal 持久地写入全局单例槽。只有描述符声明了 HasGlobal 时才合法。
	SetGlobal(ctx context.Context, value json.RawMessage) error

	// Close 把这个单元在途的写排干并释放它。**幂等**。
	Close(ctx context.Context) error
}

// 编译期确认 KVProvider 确实比 Backend 宽，而不是两个不相干的接口。
// 写反了的话 [KV] 的类型断言永远不成立，而那是一次静默的「所有后端都不提供 KV」。
var _ Backend = KVProvider(nil)
