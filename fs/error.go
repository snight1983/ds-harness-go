// 本文件是文件系统失败的分类：一组稳定的机器路由码，和带着它的那个错误类型。
//
// 源: packages/fs/fs/src/types.ts:170-203

package fs

import "fmt"

// ErrorCode 是**封闭**的、可被机器分派的文件系统失败词汇。
//
// 源: packages/fs/fs/src/types.ts:170-188
//
// 封闭是重点：重试层、权限提示层、界面层要照着它分派，
// 而分派一个封闭集合可以逐个列全；分派一段自由文本只能做字符串匹配，
// 那种匹配会在有人改了一句英文文案之后静默失配。
//
// 取值一律照抄 DSH，不改拼写、不本地化：工具注册表会把 `{name, code}`
// 原样放进 isError 结果里交给模型和上层，它是**线上可见**的载荷。
type ErrorCode string

const (
	// CodeNotFound 表示目标不存在。
	CodeNotFound ErrorCode = "FS_NOT_FOUND"
	// CodeNotDirectory 表示被要求列目录的目标不是一个目录。
	CodeNotDirectory ErrorCode = "FS_NOT_DIRECTORY"
	// CodeNotText 表示这份内容不是能按 UTF-8 读出来的文本。
	CodeNotText ErrorCode = "FS_NOT_TEXT"
	// CodeNotRegularFile 表示目标不是常规文件（是目录、设备、套接字之类）。
	CodeNotRegularFile ErrorCode = "FS_NOT_REGULAR_FILE"
	// CodeTooLarge 表示内容超过了这次调用给的字节上限，见 [FileSystem.ReadBytes]。
	CodeTooLarge ErrorCode = "FS_TOO_LARGE"
	// CodePermissionDenied 表示操作系统层面不许这么做。
	CodePermissionDenied ErrorCode = "FS_PERMISSION_DENIED"
	// CodeSandboxDenied 表示沙箱策略挡下了这次操作。
	//
	// 沙箱那一支在本仓库是范围外（见包文档），所以本仓库的后端不会产出这个码。
	// 它仍然留在词汇里：这个词汇是**线上可见**的，一个挂在这条接缝上的外部实现
	// 报出它时，上层的分派表得认得。
	CodeSandboxDenied ErrorCode = "FS_SANDBOX_DENIED"
	// CodeIOError 表示一次底层 I/O 失败。
	CodeIOError ErrorCode = "FS_IO_ERROR"
	// CodeStaleVersion 表示守卫给出的版本和目标此刻的版本对不上（也包括目标已经不在了）。
	CodeStaleVersion ErrorCode = "FS_STALE_VERSION"
	// CodeNotObserved 表示这次操作要求的前置观察不成立，
	// 比如 [CreateIfAbsent] 碰上了一个已经存在的目标。
	CodeNotObserved ErrorCode = "FS_NOT_OBSERVED"
	// CodeAmbiguousEdit 表示 [EditRequest.ReplaceAll] 为假时匹配到了不止一处。
	CodeAmbiguousEdit ErrorCode = "FS_AMBIGUOUS_EDIT"
	// CodeEditNotFound 表示 [EditRequest.OldString] 一处都没匹配上。
	CodeEditNotFound ErrorCode = "FS_EDIT_NOT_FOUND"
	// CodeAborted 表示这次操作被取消了。
	CodeAborted ErrorCode = "FS_ABORTED"
)

// Error 是这条接缝上带类型的失败。
//
// 源: packages/fs/fs/src/types.ts:190-203
//
// 调用方用 errors.As 拿到它，**照着 Code 分派**，不要去匹配 Message 里的字。
//
// 新增: DSH 那边它继承 dsh-llm 的 HarnessError，为的是和别的包共用一套
// 「带稳定码 + 能串 cause」的基类。本仓库 storage 和 attachment 两个包
// 已经各自给出了同一个答案：Go 里每个包自己定 Code 和 Error，不设共同基类。
// 理由是 Go 的错误分派靠 errors.As 认**具体类型**，而不是靠原型链认基类——
// 一个共同基类在这里既拦不住谁，也提供不了任何 errors.As 拿不到的东西。
type Error struct {
	// Code 是稳定的机器路由码，也是唯一该被分派的东西。
	Code ErrorCode

	// Message 是给人看的诊断细节。
	//
	// 它会跟着工具结果一路送到模型和客户端，所以保持英文：
	// 它是线上可见的载荷，不是给本仓库读者看的注释。
	Message string

	// Err 是底层原因，可以为 nil。
	//
	// 对应 DSH 的 ErrorOptions.cause。它只供日志和排查使用：分派一律看 Code，
	// 因为底层错误在不同后端上根本不是同一种东西（一个是 syscall.Errno，
	// 另一个是远端工作区的 HTTP 状态）。
	Err error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("fs: %s（%s）：%v", e.Message, e.Code, e.Err)
	}
	return fmt.Sprintf("fs: %s（%s）", e.Message, e.Code)
}

// Unwrap 让 errors.Is / errors.As 能问到底层原因。
func (e *Error) Unwrap() error { return e.Err }

// ErrorCoder 是任何「带稳定路由码」的错误。
//
// 新增: DSH 侧没有这个接口——它靠 `instanceof HarnessError` 和一个共同基类。
// 本仓库 attachment 包已经论证过为什么 Go 这边用一个只有一个方法的接口
// 代替基类（见 attachment.ErrorCoder）：任何包只要给自己的错误加上
// ErrorCode() string 就满足它，**不需要 import 本包**，于是导入环不会形成。
//
// 这里再声明一次而不是复用 attachment 的那个，正是这套做法的意义所在：
// 它是结构化的，两个同名同签名的接口互不认识也互相满足，而反过来
// 让 fs 去 import attachment 只为借一个接口，才是真的把两个包绑在了一起。
type ErrorCoder interface {
	error
	// ErrorCode 返回这个错误的稳定路由码。
	ErrorCode() string
}

// ErrorCode 让 *[Error] 满足 [ErrorCoder]。
func (e *Error) ErrorCode() string { return string(e.Code) }
