// Package jsonl 是一个把会话日志写成盘上文件的持久化后端：一个会话一份只追加的
// 存档，第一行是那份不可变的头，后面每一行是一条（或一串压过的）事件。
//
// 源: packages/session/session-persistence-jsonl/src/index.ts:1-7
//
// 它自己只管**字节**：怎么编码、写到哪、崩在半截怎么认出来、怎么把坏尾巴截掉。
// 攒批、按身份串行、认领活会话、准备池、修复的次序——那些一样都不在这里，
// 全都由 [github.com/snight1983/ds-harness-go/session/persistence.Coordinator] 提供。
// 本包因此是那个编排器的第一个真实使用者，也是本仓库第一个能落盘的
// [github.com/snight1983/ds-harness-go/session/persistence.Store]。
//
// # 一份存档摆在哪
//
//	<root>/<--工程目录-->/<编码过的会话标识>/session.jsonl
//
// 中间那一层是给人看的（见 path.go 里的 projectKey，有损但可读），里面那一层
// 是**单射**的（见 encodeSegment）——认身份靠里面那层，不靠外面那层。
// 一个会话标识是一段没验过的字符串，直接当路径用就等于把 `../`、绝对路径、
// NUL 和分隔符交给了写它的那个人。
//
// # 它怎么面对一次崩溃
//
// 写是「追加一段编码好的字节，然后 fsync」。崩在中间，盘上就留着半条记录。
// 读的时候扫到那条没有换行的尾巴就当它不存在（[logScanner] 的契约），
// 把安全的截断位置作为断尾标记交给编排器；编排器算出该补哪几条收尾事件，
// 再从 [Backend.CommitRepair] 递回来落盘。**已经提交的那一段一个字节都不重写**。
//
// 一条已提交的记录本身坏掉是另一回事：那不是断尾，那是损坏，扫描器把它记下来
// 并在后面遇到一条 turn/end 时拒收整份日志——一份缺了中段却带着完整回合边界的
// 日志会被读成一段「本来就没发生过」的历史，那比读不出来更坏。
//
// # 物理编码
//
// [Compression] 有两档。本包此刻只写 [CompressionNone]；[CompressionZstd]
// 的读写没有移过来，理由和恢复路径逐项记在 docs/portmap/decisions.md。
// 但**那一档的存在本身**是要紧的：一个已经装着 `.jsonl.zstd` 的根被一台按明文
// 配置的后端打开时，必须当场拒绝而不是把那些存档当成不存在——所以两种后缀
// 这里都认得，只是其中一种一律拒。
//
// # 发布一份新存档为什么不用 rename
//
// 建一份存档是「写暂存文件、fsync、发布」。发布那一步在 POSIX 上是
// link()+unlink()、在 Windows 上是 MoveFileEx(MOVEFILE_WRITE_THROUGH)，
// 两者都在目标已存在时失败。rename 会**默默盖掉**它，而这里的目标已存在
// 恰恰意味着「另一个会话在盘上和它撞了号」——那是必须喊出来的事。
package jsonl
