// 本文件的作用：本包会报的那几种错误，以及那个把提供方事实随身带着的错误类型。
//
// 前两条是解码那几个联合类型时报的。
//
// 新增: 它们在 DSH 侧没有对应物。TS 的联合类型靠 JSON.parse 加结构化判断就能分派，
// 读不回来的东西在那边通常表现为一个 undefined 字段，而不是一条错误。
// Go 的 encoding/json 解不进接口，分派这一步必须自己写，写了就会有失败面。
//
// 解码只有两条，因为可失败的地方只有两处：字节排不成想要的形状，
// 或者判别标签是一个**封闭**联合里没有的值。三个可扩展的联合
// （[ContentBlock]、[MessageSource]、[FinishReason]）不走第二条——
// 它们把没读懂的那段原样收进各自的 Unknown 变体，理由见包文档。
//
// 第三条是解算调用方那份配置时报的，它和前两条要采取的行动不一样：
// 前两条说这段字节坏了，它说**人**写错了。

package llm

import (
	"errors"
	"fmt"
)

var (
	// ErrMalformedValue 表示这段字节排不成、或者读不回本包的某个值。
	ErrMalformedValue = errors.New("llm: 值的编码格式不对")

	// ErrUnknownChunkType 表示一个流式分块的 type 不是登记过的七个之一。
	//
	// 这一条单独立着而不是并进 [ErrMalformedValue]，是因为两者要采取的行动不一样：
	// 格式不对说明这段字节坏了或者根本不是这个东西；类型不认识说明它**可能**是
	// 一份更新版本写下的、结构完好的分块。前者没救，后者是升级提示。
	ErrUnknownChunkType = errors.New("llm: 流式分块的类型不认识")

	// ErrInvalidConfig 表示交进来的一份配置解算不出一份能用的策略。
	//
	// 该做的事：改配置。包在它后面的那句诊断会点出是哪一个字段、以及它该长什么样。
	//
	// 源: packages/llm/llm/src/retry-policy.ts:128、131、134、137、168、171、174、177、193
	ErrInvalidConfig = errors.New("llm: 配置不合法")
)

// Error 是一次 LLM 失败，随身带着那份可序列化的提供方事实。
//
// 源: packages/llm/llm/src/index.ts:84-117
//
// [Failure] 和这个类型是两样东西，分工也不一样：Failure 是**事实**，会跟着
// [FinishReason] 一起写进会话日志、跨进程传出去；Error 是**活着的那个错误**，
// 只在一次调用里往上抛，用来让 errors.Is/errors.As 找得到它。适配器抛的是
// Error，运行时把它归一成 Failure 再塞进终止分块，见 [NormalizeFailure]。
//
// 新增: DSH 那个构造函数里有五组校验（message 非空串、code 非空串、status 是
// 100–599 的整数、providerRetryAfterMs 是有限正数、requestId 非空串），前两组和
// 后三组的「类型对不对」那一半在 Go 里由编译器管，剩下的是**取值范围**。这些范围
// 检查故意不在这里做：DSH 抛的是一个裸 Error（不是 LlmError），也就是说它把
// 「构造错误时自己又出错了」当成程序 bug 而不是可处理的失败。Go 这边同样的立场
// 就是「别造一份不合法的」，而放任一个越界的 Status 也造不成伤害——它只往
// 诊断和重试策略里走，两边都自己判取值。要在外来字节的接缝上挡住不合法的事实，
// 用 [Failure.Valid]。
type Error struct {
	// Failure 是这次失败那份可序列化的事实。
	Failure Failure
	// Cause 是底下那条错误，没有时为 nil。errors.Unwrap 交出的就是它。
	Cause error
}

// NewError 造一个带码的 LLM 错误。cause 可以为 nil。
//
// 源: packages/llm/llm/src/index.ts:93-117
func NewError(message, code string, cause error) *Error {
	return &Error{Failure: Failure{Message: message, Code: code}, Cause: cause}
}

// Error 交出那句人能读的失败描述。
//
// 新增: 前面缀上码，因为一条错误往上走一路通常只剩下这一行字，而码是上层唯一
// 认得的东西。DSH 那边 Error.message 不带码，但它的 code 在 JS 里跟着对象一路
// 都在，不会像 Go 的 %v 那样只剩一串文本。
func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Failure.Code, e.Failure.Message)
}

// Unwrap 交出底下那条错误，好让 errors.Is/errors.As 一路找下去。
func (e *Error) Unwrap() error { return e.Cause }

// Valid 判一份事实是不是每个字段都在合法取值里。
//
// 源: packages/llm/llm/src/adapter-failure.ts:72-77
//
// 新增: DSH 把这段校验埋在 failureSnapshot 里，只在「从一个外来错误身上摸出一份
// failure」这一条路上用。Go 这边它是 [Failure] 自己的方法：同一份判据在读日志、
// 读适配器交回来的分块时也用得上，而那两处 DSH 是靠别的机制（zod schema、
// 类型系统）挡的。
func (f Failure) Valid() bool {
	if f.Message == "" || f.Code == "" {
		return false
	}
	if f.Status != 0 && (f.Status < 100 || f.Status > 599) {
		return false
	}
	return f.ProviderRetryAfterMs >= 0
}

// NormalizeFailure 从一条适配器抛出来的错误上，摘下那份可以直接塞进终止分块的
// 提供方事实。
//
// 源: packages/llm/llm/src/adapter-failure.ts:16-28
//
// 新增: DSH 那份实现有一多半是 JS 独有的自卫：用 Object.getOwnPropertyDescriptor
// 去读 failure 和 code，为的是**不触发** SDK 自己定义的 getter（那玩意儿可能抛，
// 也可能返回别的东西）；再逐字段验一遍摘下来的载荷，因为跨包复制会保住数据、
// 保不住类的身份，所以「它自称是个 LlmError」这件事不可信；还要比对
// carried.code === ownErrorCode(error)，确认那个 failure 属性真是这条错误自己的
// 而不是别人挂上去的。Go 这边 errors.As 直接按**具体类型**认，认出来的那个
// *Error 就是本包造的，它的 Failure 是一个结构体字段而不是一个可能抛的访问器，
// 上面那一整套都用不上了。
//
// 认不出来时交出一份 UNKNOWN 的事实，理由和 DSH 的 harnessErrorCode 一样：
// 第三方 SDK 自己那套码不是本装置的分类学，照抄进来会让上层按一个它其实不认识的
// 码去路由。
func NormalizeFailure(err error) Failure {
	if err == nil {
		return Failure{Message: adapterFailureMessage, Code: "UNKNOWN"}
	}
	var carrier *Error
	if errors.As(err, &carrier) && carrier.Failure.Valid() {
		return carrier.Failure
	}
	message := err.Error()
	if message == "" {
		message = adapterFailureMessage
	}
	return Failure{Message: message, Code: "UNKNOWN"}
}

// adapterFailureMessage 是一条错误说不出自己是什么时的兜底描述。
//
// 源: packages/llm/llm/src/adapter-failure.ts:34、36、98
const adapterFailureMessage = "LLM adapter failed"
