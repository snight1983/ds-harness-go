// 本文件的作用：凭据提供方要实现的那套操作，以及配置界面读得到的那几组事实。
//
// 源: packages/credentials/credentials/src/index.ts:114-263

package credentials

import "context"

// Resolved 是一次解析出来的凭据值，以及供出它的那一层。
//
// 源: packages/credentials/credentials/src/index.ts:114-120
type Resolved struct {
	// Value 是非空的密钥值。
	Value string
	// Source 是提供方自定的来源层 id（本地提供方用 env、file、project-env、user-env）。
	Source string
}

// Info 是一个引用的来源与可写性，配置界面拿它安全——**永远不含值本身**。
//
// 源: packages/credentials/credentials/src/index.ts:122-130
type Info struct {
	// Configured 表示此刻 [Provider.Resolve] 会不会给出一个值。
	Configured bool

	// Source 是当前供出这个值的来源层；未配置时是空串。
	//
	// 新增: DSH 那边是可选属性 `source?: string`，缺席表示未配置。
	// Go 没有「缺席的字段」，这里用空串表示同一件事——一个存在的来源层
	// 不可能是空串，所以这个映射不丢信息。
	Source string

	// Writable 表示此刻 [Provider.Set] 对这个引用会不会成功。
	Writable bool
}

// RecordInfo 是一条记录的在场与可写性，配置界面拿它安全——**永远不含值本身**。
//
// 源: packages/credentials/credentials/src/index.ts:132-145
type RecordInfo struct {
	// Configured 表示有没有存着一条记录。
	//
	// 和引用那一半不同，光看在场就够回答这件事：一条既没有密钥、也没有环境值的
	// [APIKeyRecord] 陈述的是「拥有方确认这条路由用环境发现认证」，
	// 那是**已配置**，不是空白。
	Configured bool

	// Kind 是存着的那条记录的判别标签；没有记录时是空串。
	//
	// 空串的理由同 [Info.Source]：[RecordKind] 的两个合法值都不是空串。
	Kind RecordKind

	// Writable 表示此刻 [Provider.ModifyRecord] 会不会成功。
	Writable bool
}

// RecordEntry 是枚举时给出的一条记录的地址和标签——**永远不含值**。
//
// 源: packages/credentials/credentials/src/index.ts:147-153
type RecordEntry struct {
	// Key 是这条记录的地址。
	Key Key
	// Kind 是存着的那条记录的判别标签。
	Kind RecordKind
}

// Mutator 是 [Provider.ModifyRecord] 里那段「读—决定—替换」。
//
// 源: packages/credentials/credentials/src/index.ts:243-257
//
// 它拿到的 current 是**写入已经独占的那一刻**的记录，exists 说明当时有没有记录。
// 返回 write 为 false 表示这次不改（此时 next 被忽略）。
//
// 新增: DSH 用 `undefined` 同时表示「没有记录」和「不要改」，靠出现在参数还是
// 返回值上区分。Go 这边参数侧用 exists、返回侧用 write，分成两个 bool——
// 因为它们真的是两件事，而合成一个的代价是「把一条记录删空」和「什么都不做」
// 写出来一模一样。
type Mutator func(ctx context.Context, current Record, exists bool) (next Record, write bool, err error)

// Observer 是这条接缝的订阅面：观察提供方**已经提交**的变更。
//
// 源: packages/credentials/credentials/src/types.ts:61-88
//
// 新增: DSH 那边这是 cordis 上的两个事件（credentials/reference-updated 和
// credentials/record-updated），消费方在 ctx 上订阅。Go 没有那个容器，
// 订阅就落在提供方自己身上。
//
// 两个事件分开而不是合成一个，是因为两套键空间的语法是不相交的：
// 一个同时收到两者的监听器分不出手上这个主体属于哪一边。
//
// 返回的取消订阅函数是**同步**的（不是 Disposer）：退订只是从一张表里摘掉一项，
// 不做 I/O。它是幂等的，多调几次不会摘错别人。
type Observer interface {
	// SubscribeReference 订阅「某个引用的存储值提交了一次变更」。
	//
	// 源: packages/credentials/credentials/src/types.ts:63-75
	//
	// 一次 Set、一次 Unset，或者一次在存储上观察到的外部编辑，都会到这里。
	// **进程环境的变化观察不到，永远不会发**——这一点必须写明，否则消费方会
	// 以为自己订阅了「凭据变了」这件事的全部。
	SubscribeReference(listener RefListener) func()

	// SubscribeRecord 订阅「某条记录的存储值提交了一次变更」。
	//
	// 源: packages/credentials/credentials/src/types.ts:77-87
	//
	// 一次写下去的 ModifyRecord、一次真的删掉了东西的 DeleteRecord，
	// 或者一次在存储上观察到的外部编辑。
	SubscribeRecord(listener RecordListener) func()
}

// Provider 是凭据服务：两套键空间上的九个操作，加上 [Observer] 那两个订阅口。
//
// 源: packages/credentials/credentials/src/index.ts:177-263
//
// 内嵌 [Observer] 而不是把它做成可选能力，是因为 DSH 侧那两个 notify 方法在基类上，
// 每个子类**继承就有**，没有不带通知的凭据提供方。
// 实现方内嵌一个 [*Notifier] 就自动满足这两个方法，见包文档。
type Provider interface {
	Observer

	// Resolve 把一个引用解析成它当前的值。
	//
	// 源: packages/credentials/credentials/src/index.ts:182-190
	//
	// 解析是**每次调用都来一遍**的：消费方在每一次操作时重新解析，
	// 不许跨操作缓存——那一次次的重读正是「换了凭据不用重启」的全部实现。
	//
	// 第二个返回值是「有没有配置」。未配置不是错误：一个还没填的可选凭据
	// 是完全正常的状态，把它报成错误会让日志里堆满不是故障的故障。
	Resolve(ctx context.Context, ref Ref) (Resolved, bool, error)

	// Describe 描述一个引用，供配置界面使用，**不暴露值**。
	//
	// 源: packages/credentials/credentials/src/index.ts:192-198
	Describe(ctx context.Context, ref Ref) (Info, error)

	// Set 把一个值持久存进提供方管着的那个可写来源层。
	//
	// 源: packages/credentials/credentials/src/index.ts:200-208
	//
	// 有一个只读来源层压在这个引用上时必须拒绝：写会看起来成功，
	// 而解析仍旧返回那个压在上面的值——「我明明改了但没生效」。
	// 空值也必须拒绝，清除请用 [Provider.Unset]。
	Set(ctx context.Context, ref Ref, value string) error

	// Unset 把一个引用从提供方管着的可写来源层里删掉；删一个不存在的是空操作。
	//
	// 源: packages/credentials/credentials/src/index.ts:210-216
	//
	// 被只读来源层压着时同样拒绝，理由和 [Provider.Set] 一样。
	Unset(ctx context.Context, ref Ref) error

	// ReadRecord 读一条存着的记录。
	//
	// 源: packages/credentials/credentials/src/index.ts:218-224
	//
	// 值按拥有方写下去的样子返回，[GrantRecord] 的载荷在出来的路上不被解释。
	ReadRecord(ctx context.Context, key Key) (Record, bool, error)

	// DescribeRecord 描述一条记录，供配置界面使用，**不暴露值**。
	//
	// 源: packages/credentials/credentials/src/index.ts:226-231
	DescribeRecord(ctx context.Context, key Key) (RecordInfo, error)

	// ListRecords 枚举每一条存着的记录的地址和标签，**不含值**。
	//
	// 源: packages/credentials/credentials/src/index.ts:233-241
	//
	// 引用那一半没有枚举，因为配置界面从设置的 schema 里就知道有哪些引用；
	// 记录这一半没有这条发现路径——列不出来的界面既没法告诉用户他授权过什么，
	// 也找不出某个已卸载插件留下的孤儿。
	ListRecords(ctx context.Context) ([]RecordEntry, error)

	// ModifyRecord 是对一条记录的串行化「读—改—写」，也是**唯一**的写入路径。
	//
	// 源: packages/credentials/credentials/src/index.ts:243-257
	//
	// 只留这一条路径是因为一次正确的写依赖当前值。互斥在后端支持的地方要跨进程成立，
	// 这正是刷新令牌安全的前提：两个进程同时轮换同一个刷新令牌时，
	// 没有互斥就会丢掉先写的那一个，而那个令牌一旦丢了就再也换不回来。
	//
	// 返回的是写完之后的记录；mutate 谢绝时返回当前那一条。
	ModifyRecord(ctx context.Context, key Key, mutate Mutator) (Record, bool, error)

	// DeleteRecord 删掉一条记录；删一个不存在的是空操作。
	//
	// 源: packages/credentials/credentials/src/index.ts:259-263
	DeleteRecord(ctx context.Context, key Key) error
}
