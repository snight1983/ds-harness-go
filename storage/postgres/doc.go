// Package postgres 是键值存储中枢的 Postgres 后端：一个库承载所有被路由过来的单元，
// 一条记录一行（key TEXT / value TEXT，值是一段不透明 JSON）。
//
// 源: packages/storage/storage-sqlite/src/index.ts:1-6
//
// # 这是抄形状，不是移包
//
// DSH 那边没有 Postgres 后端，只有 storage-sqlite 和一堆往本机写文件的实现。
// 按 DESIGN.md 第四节，storage-sqlite **不移包**，抄的是它的三样东西：
// 键值怎么映射成表、版本怎么盖、迁移怎么走。所以本包的每一处 `源:` 指的都是
// 「这个形状是从那里来的」，不是「这段代码是从那里翻译的」。
//
// 为什么后端是 Postgres 而不是 SQLite，判据在 DESIGN.md 第七节：不是吞吐，
// 是单机还是多机。SQLite 是一个文件、靠文件锁协调，两台机器共享不了。
//
// # 库里有的三张表
//
//	storage_meta   一行，盖着**物理布局**的版本号，顶替 SQLite 的 PRAGMA user_version
//	units          每个单元一行，盖着这个单元自己的格式版本
//	unit_globals   每个声明了全局槽的单元一行
//
// 加上每个单元的每张表各一张物理表，名字是 u_<单元>_<表>。
//
// 单元名和表名由 [storage.KVUnitDescriptor.Validate] 卡成 `^[a-z][a-z0-9_]*$`，
// 所以拼出来的标识符不用转义就能进 DDL；记录键**永远不会**进入标识符，
// 它只作为参数绑定进去——这是 [storage.KVUnit] 明写的后端义务。
//
// # 和 SQLite 那份形状不一样的几处
//
// 新增: **驱动不在本包里。**[Config] 收的是一个已经建好的 *sql.DB，
// 由装配方决定用哪个驱动（lib/pq、pgx 的 stdlib 包，都行）。本包只用
// database/sql，一个第三方 import 都没有。这不是洁癖：驱动是部署期的选择，
// 而把它焊进一个存储后端里，等于让每一个想换驱动的人来改这个包。
// 连接池也因此是标准库那一个，不用再挑一个。
//
// 新增: **值的列是 TEXT，不是 jsonb。**jsonb 看起来更合适——它验 JSON、还能查。
// 但它会拒掉 JSON 字符串里的 U+0000 转义，而 Go 的 encoding/json 正是用那个转义
// 编码 NUL 的。也就是说，一段带 NUL 的模型输出在内存后端和 SQLite 上存得下、
// 在 jsonb 上会当场失败。这一层自陈「值是不透明的 JSON」，那就不该由它来收窄
// 能存什么。代价是读回来时要自己解析一次，那条路径映射成
// [storage.CodeMalformedMedium]，和 SQLite 那份形状一样。
//
// 新增: **标识符长度要查。**Postgres 的标识符上限是 63 字节，超了**静默截断**，
// 于是两个长名字不同的表会塌成同一张物理表——数据互相覆盖，且没有任何报错。
// SQLite 没有这个上限，所以那份形状里也没有这一查。见 [recordTableName]。
//
// 新增: **建表和盖版本号包在一次带咨询锁的事务里。**选 Postgres 的理由就是多机，
// 而多机意味着两个进程可能同时第一次打开同一份介质：两条 CREATE TABLE IF NOT EXISTS
// 撞在一起，Postgres 会报 duplicate_table 而不是安静地各建各的。pg_advisory_xact_lock
// 把这一段串起来，事务结束自动放锁。SQLite 是单文件单进程，那份形状里没有这件事。
//
// 新增: **所有标识符都带 schema 前缀，不靠 search_path。**连接池里的连接
// 各自带着自己的会话状态，靠 search_path 意味着「这条语句落在哪个 schema」
// 取决于抓到了哪条连接。[Config.Schema] 因此也是这份介质的身份：
// 同一个库里开两个 schema，就是两份互不相干的介质。
//
// # 不在这套失败词汇里的失败
//
// [storage.ErrorCode] 是封闭的，里面没有「连不上库」「事务被中止」这类东西。
// 这类失败原样往上冒，只裹一层说明是哪个单元的哪一步——和 DSH 的 SQLite 后端
// 让 node:sqlite 的错误直接往上冒是同一个做法。调用方分派只看得到那七个码，
// 剩下的一律当成「这次操作没成」。
package postgres
