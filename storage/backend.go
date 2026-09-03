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

// Revision 是一条记录（或全局槽）在介质上的**不透明**修订标识。
//
// 新增: DSH 没有这个概念。它那边整个存储层跑在一个进程里，读出来的值到写回去
// 之间不会有第二个写者，所以「我读的还是不是最新的」这个问题问不出来。
// 这个服务是多副本的，那个问题不但问得出来，而且不问就会丢更新。
//
// 不透明是有意的：调用方只能拿它原样回传给 [ReplaceIfRevision]，不能比大小、
// 不能自己造一个。后端可以拿它装一个自增计数、一个 ETag 或者一个时间戳，
// 换实现时不必担心有人依赖了它的内部形状。空串表示「这条记录不存在」。
type Revision string

// WriteIntent 是一次写的**前置条件**，封闭的两种。
//
// 新增: 形状照 [github.com/snight1983/ds-harness-go/fs.WriteIntent] 抄，不发明第二套——
// 同一个仓库里两处「条件写」长得不一样的话，装配方要记两遍。
//
// 传 nil 表示无条件覆盖，**它不是第三个成员**：没有前置条件这件事的表达方式是
// 这个值不存在，而不是一个叫「无条件」的成员。多一个成员就多一处要分派的分支，
// 而那个分支和 nil 分支永远做同一件事。
type WriteIntent interface {
	sealedWriteIntent()
}

// CreateIfAbsent 要求这条记录此刻**不存在**。
//
// 已经存在时返回 Code 为 [CodeStaleRevision] 的 *[Error]。后端必须让「不存在才写」
// 在介质上是一次原子操作（SQL 的 ON CONFLICT DO NOTHING 一类），**不许**先查一次
// 再写一次——那两步之间正是别的副本插进来的地方。
type CreateIfAbsent struct{}

func (CreateIfAbsent) sealedWriteIntent() {}

// ReplaceIfRevision 要求这条记录此刻的修订标识**正好**是 Revision。
//
// 对不上（包括记录已经被删掉）时返回 Code 为 [CodeStaleRevision] 的 *[Error]。
// 这是读-改-写唯一安全的收尾方式：拿 [KVUnit.ReadRecord] 给出的那个修订标识回传，
// 中间被别的副本改过就写不进去。
type ReplaceIfRevision struct {
	Revision Revision
}

func (ReplaceIfRevision) sealedWriteIntent() {}

// KVUnit 是一个已打开的单元。
//
// 源: packages/storage/storage/src/backend.ts:66-115（KvUnit）
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
// 新增: 单条读（[KVUnit.ReadRecord] / [KVUnit.ReadGlobal]）和写上那个前置条件参数
// 都是本仓库加的。DSH 只有整单元快照读加无条件写，因为它上面那一层把权威状态放在
// 进程内存里，读根本不到这一层来。这个服务是多副本的，进程内存不再是权威，
// 每次读都得穿到介质；而穿到介质的读一旦要改回去，就必须能说清「我改的是哪一版」。
type KVUnit interface {
	// LoadAll 读出当前的完整快照。
	LoadAll(ctx context.Context) (Snapshot, error)

	// LoadTable 只读出其中一张表的全部记录。
	//
	// 声明过而一条记录都没有的表、以及压根没声明过的表，都交出一张**空 map 而不是
	// nil**——调用方问的是「这张表里有什么」，答案是「什么都没有」。
	//
	// 新增: DSH 没有这个方法，因为那边整张表本来就躺在进程内存里，要哪张挑哪张。
	// 读穿到介质之后，「列一张表」如果只能走 [KVUnit.LoadAll]，就得把同一个单元里
	// 其余的表全部白读一遍。这一条把那份浪费去掉。
	//
	// 它**不保证**跨表一致：要几张表在同一时刻的样子，走 [KVUnit.LoadAll]。
	LoadTable(ctx context.Context, table string) (map[string]json.RawMessage, error)

	// ReadRecord 读出单独一条记录，连同它此刻的修订标识。
	//
	// 第三个返回值为 false 表示这条记录不存在，此时值为 nil、修订标识为空串。
	// **不存在不是错误**：调用方问的就是「在不在」。
	ReadRecord(ctx context.Context, table, key string) (json.RawMessage, Revision, bool, error)

	// ReadGlobal 读出全局单例槽，连同它此刻的修订标识。
	//
	// 只有描述符声明了 HasGlobal 时才合法。从没写过时值为 nil、修订标识为空串。
	ReadGlobal(ctx context.Context) (json.RawMessage, Revision, error)

	// PutRecord 持久地写入一条记录，返回写完之后的修订标识。
	//
	// expected 为 nil 时是**覆盖语义**：已存在的键被替换。给了前置条件而条件不成立时，
	// 介质上一个字都不许改，并返回 Code 为 [CodeStaleRevision] 的 *[Error]。
	//
	// 每一次成功的写都必须换一个新的修订标识，**即使写进去的值和原来一模一样**。
	// 不换的话，一个「读到 rev=3、写了个相同的值、还是 rev=3」的序列会让另一个副本
	// 的守卫误判成「没人动过」。
	//
	// key 可以是任意字符串——记录键永远不会出现在文件路径里，这是后端的义务。
	PutRecord(ctx context.Context, table, key string, value json.RawMessage, expected WriteIntent) (Revision, error)

	// DeleteRecord 持久地删掉一条记录，返回它删之前在不在。
	//
	// expected 为 nil 时**幂等**：键不存在就什么也不做，返回 false 而不是错误。
	// 给了修订标识而对不上（包括记录已经不在了）时返回 [CodeStaleRevision]，
	// 且介质上一个字都不许改。
	//
	// 新增: 参数是 *[ReplaceIfRevision] 而不是 [WriteIntent]。删这一侧只有
	// 「必须还是那一版」讲得通——[CreateIfAbsent] 落到删上是「删一个必须不存在的
	// 东西」，那句话没有意义，而收下一个没有意义的输入就得在运行期把它拒掉。
	// 用更窄的类型，它在编译期就写不出来。
	//
	// 返回「删之前在不在」也是本仓库加的：上面那一层的 Delete 要回答这件事，
	// 而它以前是拿进程内存里那份状态回答的。读穿到介质之后，只有真正执行删除的
	// 那条语句知道答案，先查一次再删会在两步之间被别的副本抢走。
	DeleteRecord(ctx context.Context, table, key string, expected *ReplaceIfRevision) (bool, error)

	// SetGlobal 持久地写入全局单例槽，返回写完之后的修订标识。
	//
	// 只有描述符声明了 HasGlobal 时才合法。前置条件的语义和 [KVUnit.PutRecord] 一致。
	SetGlobal(ctx context.Context, value json.RawMessage, expected WriteIntent) (Revision, error)

	// Close 把这个单元在途的写排干并释放它。**幂等**。
	Close(ctx context.Context) error
}

// 编译期确认 KVProvider 确实比 Backend 宽，而不是两个不相干的接口。
// 写反了的话 [KV] 的类型断言永远不成立，而那是一次静默的「所有后端都不提供 KV」。
var _ Backend = KVProvider(nil)
