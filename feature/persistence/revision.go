// 本文件的作用：一份存档的变更令牌。
//
// 源: packages/session/session-persistence/src/revision.ts

package persistence

// Revision 是一份存档的**来源限定**变更令牌：同一份没变过的日志被观察多少次
// 都给出同一个值，日志一变（包括一次成功的崩溃修复）就换一个值。
//
// 源: packages/session/session-persistence/src/revision.ts:1-18
//
// 「来源限定」的意思是它还要能区分两个各自独立的存储：两个后端各自的
// 自增计数器不许比出相等，否则一个「从 A 读、拿 B 的令牌去核对」的调用方
// 会以为自己看的是同一份东西。后端通常的做法是往里拌一个自己的实例标识。
//
// 它对使用方是**不透明**的：只能比相等，不能解析、不能排序、不能推断先后。
//
// 空串表示没有令牌，也就是这个身份在存储里不存在。
//
// 新增: DSH 是 Branded<'SessionPersistenceRevision'>。Go 的具名类型天生是
// 标称类型，一个 Revision 不会被误当成随便哪个 string 传进来，
// 理由同 [session.SessionID]。
type Revision string
