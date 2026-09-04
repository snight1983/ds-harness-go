// 本文件的作用：本包会报的那几种错误。
//
// 源: packages/session/session-projection/src/index.ts:240-242
// 源: packages/session/session-projection/src/index.ts:249-251
// 源: packages/session/session-projection/src/index.ts:436-441

package projection

import "errors"

var (
	// ErrInvalidDefinition 表示这份单元定义本身就不成立：键是空的、
	// 版本号是负的、或者少了一个必填的函数。
	//
	// 该做的事：这是**登记方**的缺陷，和会话里有什么无关，改代码。
	//
	// 新增: DSH 只在运行时拦了版本号那一项，别的几项由 TypeScript 的类型
	// 在编译期挡住。Go 的结构体字面量允许留空，一个 nil 的函数字段要到第一次
	// 推进时才 panic，那时离登记点已经很远了。登记是本包的边界，所以在这里查。
	ErrInvalidDefinition = errors.New("sessionlog/projection: 单元定义不成立")

	// ErrStateVersionConflict 表示这个键已经被登记过，但版本号和这次的不一样。
	//
	// 该做的事：两个登记方对同一个键的折叠语义有分歧，共用它就等于让其中一方
	// 读到另一方算出来的状态。改代码：要么统一版本号，要么换一个键。
	ErrStateVersionConflict = errors.New("sessionlog/projection: 同一个投影键被登记成了两个不同的状态版本")

	// ErrCheckpointUnusable 表示 [Registry.Restore] 拿到的检查点行用不了
	// （缺行、版本对不上、或者它声称的水位超过了这次给进来的日志末尾），
	// 而这次给进来的又只是日志的一截尾巴。
	//
	// 该做的事：行用不了就得从 [Definition.Init] 重折，而重折只在**完整**日志上
	// 才成立。从 seq 0 重读一遍再调一次就对了。
	//
	// 会走到这里最常见的原因是崩溃收尾把日志截短到了某一行的水位之下。
	ErrCheckpointUnusable = errors.New("sessionlog/projection: 检查点行用不了，得从 seq 0 重读整份日志")
)
