// Package sessionstore 把 [github.com/snight1983/ds-harness-go/feature/persistence]
// 的后端契约接到 [github.com/snight1983/ds-harness-go/adapter/datastore] 的日志集上。
//
// 新增: 上游没有对应物。一个会话就是一条流：会话 id 是流名，会话头是流的头，
// 一条 event 是一条条目，event 的 seq 就是条目的 seq。本包里没有一句 SQL、
// 没有一个连接池、没有一句方言——那些全在 datastore 里，理由见那个包的文档。
//
// 依赖的方向：本包认识 persistence 那道业务接口，persistence **不认识本包**。
// session 那棵树里没有、也不许有任何一处提到数据库。
//
// # 它填哪几道缝、不填哪两道
//
// 填 [persistence.Backend]、[persistence.SeekableBackend]（按 seq 寻址在日志集上
// 是走主键的一句读）、[persistence.ClosableBackend]（收介质）、
// [persistence.TrimmingBackend]（从最老那头弹）。
//
// 不填 [persistence.LocatingBackend]：所有会话装在同一份介质里，没有「这个会话
// 那份存档」可指——那个接口的文档自己写着这一条。[Store.SupportsRawArtifacts]
// 同样恒假：一份行式存储里没有「那个会话的原始字节」这回事。
//
// # 它怎么面对一次崩溃
//
// 一次写就是一个事务，所以**不存在断尾**：要么整批提交，要么一条都没有。
// [persistence.StoredPrefix.TornMarker] 因此恒为 nil，[Backend.CommitRepair]
// 收到的 torn 也只可能是 nil——非 nil 说明编排器把别的后端的凭据递错了地方，
// 那时候当场拒绝，不去猜它是什么意思。
//
// # 头是一段不透明的 JSON
//
// datastore 不认识 [session.SessionHeader]，头在那边是一段字节。于是两件事
// 落在本包身上：一是解不回来的头要报成 [persistence.CorruptionError]；
// 二是 [Backend.List] 的排序在本包做——datastore 只按流名排，因为它不知道
// 头里有个 created_at。
//
// 「同一个 id 底下不许换一份头」那一比在 datastore 里比的是**字节**。
// [session.SessionHeader] 没有自定义编解码，encoding/json 排结构体又是定序的，
// 所以字节比等价于逐字段比。
package sessionstore
