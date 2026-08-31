// 本文件的作用：会话的身份和落盘格式版本号。
//
// 源: packages/core/session/src/types.ts:21-56

package session

// SessionID 是一个会话在存储里的身份，它的持久化产物也按这个标识归档。
//
// 源: packages/core/session/src/types.ts:21-31
//
// 新增: DSH 是 Branded<'SessionId'> 加一个恒等构造函数。Go 的具名类型天生是
// 标称类型，两样都不需要，理由见 llm 包文档。
type SessionID string

// FormatVersion 是会话日志的落盘格式版本，写进每一份新建的 [SessionHeader]，
// 由每个持久化后端在装载时校验。
//
// 源: packages/core/session/src/types.ts:33-56
//
// 未发布期间钉在 0：不承诺任何兼容性，不匹配的日志直接拒收，不提供迁移。
//
// 它是一个单调递增的整数，没有主次版本之分。要不要进位**由写的一方决定**，
// 不由「新读者能不能收下」决定：当一个旧构建再也无法在完整语义正确的前提下
// 处理一份新日志时进位——「能解析」不等于正确，静默跳过一条会影响重建的内容
// 就是一次错误的读取。够得上这条线的只有结构性改动：头的形状、[Event] 信封、
// 核心事件语义、以及表面机制（[IsSurfaceEligibleType] 认的那三种类型和
// [SurfaceOp] 的变体集合）。加一个普通的事件类型**不进位**——词汇的增长由
// 每条事件自己的 [Event.Ignorable] 标记兜住。拿不准就进位：多走一步近乎恒等的
// 升级几乎不要钱，漏掉一次进位则会让旧构建默默地把新日志读错。
const FormatVersion = 0
