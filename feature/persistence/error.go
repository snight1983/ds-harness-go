// 本文件的作用：本包会报的那几种错误，以及「这份日志本构建读不了」那句拒绝的措辞。
//
// 源: packages/session/session-persistence/src/coordinator.ts:34-79

package persistence

import (
	"errors"
	"fmt"

	"github.com/snight1983/ds-harness-go/sessionlog"
)

var (
	// ErrSessionNotFound 表示这个身份在存储里没有存档。
	//
	// 新增: DSH 用 `undefined` 表示「不在」，用 throw 表示「出事了」，
	// 两件事在返回值里分得开。Go 的两值返回把「不在」也变成一个错误，
	// 所以这里立一个哨兵——它是**正常控制流**：建会话前的撞号探测走的就是
	// errors.Is(err, ErrSessionNotFound)。
	ErrSessionNotFound = errors.New("feature/persistence: 存储里没有这个会话")

	// ErrIdentityMismatch 表示读回来那份头上的会话身份不是请求的那个。
	//
	// 该做的事：这是**后端**的缺陷，不是日志坏了——后端把 a 的存档当成 b 的
	// 交了出来。发布任何状态之前必须先拦住，否则一个会话会被另一个的历史覆盖。
	ErrIdentityMismatch = errors.New("feature/persistence: 存档里的会话身份和请求的不是同一个")

	// ErrCorrupted 表示后端把字节读回来了，但这份内容过不了校验。
	//
	// 该做的事：这份存档坏了。带着 [CorruptionError] 里的原因一起报出去。
	ErrCorrupted = errors.New("feature/persistence: 存档读回来之后没通过校验")

	// ErrFormatUnsupported 表示日志是完好的，但本运行时没法忠实地解释它：
	// 头上的格式版本号本构建不读，或者里面有一条不认识又没标可跳过的事件。
	//
	// 该做的事：**不是**损坏，什么都没坏。原始日志还在原地躺着，
	// 后端有逐会话存档时 [FormatUnsupportedError.Location] 会指出在哪。
	// 提示升级运行时，绝不要提示「文件损坏」。
	ErrFormatUnsupported = errors.New("feature/persistence: 本构建没法忠实解释这份日志")

	// ErrMalformedSeq 表示调用方给了一个负的 seq 水位。
	//
	// 该做的事：这是**调用方**的缺陷，和存储里有什么无关。
	//
	// 新增: DSH 把这条判据写在编排层（「a non-negative safe integer,
	// validated by the coordinator before this hook runs」），后端只管信。
	// 编排层在本仓库落在第 6 块，所以这个哨兵先立在这里，让实现方在那之前
	// 也有一条统一的说法可用。
	ErrMalformedSeq = errors.New("feature/persistence: seq 水位不许是负的")

	// ErrRawArtifactsUnsupported 表示这个后端不提供逐会话的原始存档。
	//
	// 一个把所有会话装进同一个数据库的后端就没有「这个会话那份文件」可给。
	// 调用方先问 [Store.SupportsRawArtifacts]，问过了还调就得到这条。
	ErrRawArtifactsUnsupported = errors.New("feature/persistence: 这个后端不提供逐会话的原始存档")
)

// CorruptionError 是一份读回来之后没通过校验的存档。
//
// 源: packages/session/session-persistence/src/coordinator.ts:36-46（SessionPersistenceCorruptionError）
//
// 和 [FormatUnsupportedError] 分开：那一个是「完好但本构建读不了」，
// 这一个是「真的坏了」。两者给使用者的下一步动作不一样，所以不能并成一条。
type CorruptionError struct {
	// ID 是这份存档声称属于的会话。
	ID sessionlog.SessionID
	// Cause 是把它判死的那条校验错误。
	Cause error
}

// Error 给出这条错误的文字。
func (e *CorruptionError) Error() string {
	return fmt.Sprintf("会话 %q 的存档没通过校验：%v", string(e.ID), e.Cause)
}

// Unwrap 同时接上哨兵和底层原因。
//
// 新增: 返回切片而不是单个错误，这样 errors.Is(err, ErrCorrupted) 和
// errors.Is(err, sessionlog.ErrTraceViolation) 两问都能命中——调用方既要分辨
// 「是不是损坏」，也要知道是**哪一条**校验把它判死的。
func (e *CorruptionError) Unwrap() []error {
	return []error{ErrCorrupted, e.Cause}
}

// FormatUnsupportedError 是一份完好但本运行时解释不了的日志。
//
// 源: packages/session/session-persistence/src/coordinator.ts:48-66（SessionFormatUnsupportedError）
type FormatUnsupportedError struct {
	// ID 是这份日志属于的会话。
	ID sessionlog.SessionID
	// Reason 是拒绝的理由，措辞是稳定的，可以直接给使用者看。
	Reason string
	// Location 是这份日志的原始存档在哪；nil 表示这个后端没有逐会话存档。
	Location *Location
}

// Error 给出这条错误的文字，后端有存档位置时把路径缀在后面。
func (e *FormatUnsupportedError) Error() string {
	if e.Location == nil {
		return e.Reason
	}
	return fmt.Sprintf("%s（原始日志：%s）", e.Reason, e.Location.Path)
}

// Unwrap 接上哨兵。
func (e *FormatUnsupportedError) Unwrap() error {
	return ErrFormatUnsupported
}

// WithLocation 返回一份指明了原始存档位置的副本。
//
// 源: packages/session/session-persistence/src/coordinator.ts:1067-1074
//
// 新增: DSH 那边是编排层的 unsupported() 方法在造错误时顺手把路径拼进消息里，
// 本包的判据函数是纯的、不认识任何后端，所以改成「先造出理由，
// 拿得到位置的那一层再补上去」。返回副本而不是原地改，是因为一条错误
// 可能已经被别处引用了。
func (e *FormatUnsupportedError) WithLocation(location Location) *FormatUnsupportedError {
	copied := *e
	copied.Location = &location
	return &copied
}

// FormatVersionRefusal 给出一份格式版本本构建不读的存档的拒绝理由。
//
// 源: packages/session/session-persistence/src/coordinator.ts:68-82（sessionFormatVersionRefusal）
//
// 措辞**分方向**，这是它单独立成一个函数的全部理由：比本构建新说的是
// 「升级运行时」，比本构建旧说的是「本构建不带这条升级路径」。两句话对应的
// 下一步动作完全不同，合并成一句就等于让使用者自己猜。
//
// 它是导出的，因为后端要在**解码任何依赖版本的结构之前**就先拒——一个未来的
// 格式可能连今天的结构检查都过不去，而那时使用者该看到的是「升级运行时」，
// 绝不是「日志损坏」。
func FormatVersionRefusal(id sessionlog.SessionID, version int) string {
	if version > sessionlog.FormatVersion {
		return fmt.Sprintf(
			"会话 %q 的日志格式是 v%d，而本运行时只读 v%d：这份日志是更新的运行时写的，升级之后才能打开它",
			string(id), version, sessionlog.FormatVersion,
		)
	}
	return fmt.Sprintf(
		"会话 %q 的日志格式是 v%d，比支持的 v%d 旧，而本构建不带它的升级路径",
		string(id), version, sessionlog.FormatVersion,
	)
}
