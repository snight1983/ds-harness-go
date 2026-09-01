// 本文件是附件失败的分类：一组稳定的机器路由码，和「哪些码是调用方自己能改对的」。
//
// 源: packages/attachment/attachment/src/error.ts

package attachment

import (
	"errors"
	"fmt"
)

// Code 是稳定的机器路由码。它是**线上可见**的：宿主机的 RPC 错误映射直接照它分派，
// 所以取值一律照抄 DSH，不改拼写、不本地化。
//
// 源: packages/attachment/attachment/src/error.ts:18-26
type Code string

const (
	// CodeTooManyImages 表示一批图的张数超过了部署配的上限。
	CodeTooManyImages Code = "TOO_MANY_IMAGES"
	// CodeImagesTooLarge 表示一批图的字节总和超过了部署配的上限。
	CodeImagesTooLarge Code = "IMAGES_TOO_LARGE"
	// CodeUnsupportedImageType 表示这个部署不收这种媒体类型。
	CodeUnsupportedImageType Code = "UNSUPPORTED_IMAGE_TYPE"
	// CodeInvalidImageBase64 表示上传的 base64 不是规范形。
	CodeInvalidImageBase64 Code = "INVALID_IMAGE_BASE64"
	// CodeInvalidImage 表示字节解码不出一张图。
	CodeInvalidImage Code = "INVALID_IMAGE"
	// CodeImageTypeMismatch 表示解出来的格式和调用方声称的媒体类型对不上。
	CodeImageTypeMismatch Code = "IMAGE_TYPE_MISMATCH"
	// CodeImageTooLarge 表示单张图的字节数超过了上限。
	CodeImageTooLarge Code = "IMAGE_TOO_LARGE"
	// CodeImageTooManyPixels 表示单张图的宽乘高超过了上限。
	CodeImageTooManyPixels Code = "IMAGE_TOO_MANY_PIXELS"
	// CodeImageDimensionTooLarge 表示单张图的宽或高超过了上限。
	CodeImageDimensionTooLarge Code = "IMAGE_DIMENSION_TOO_LARGE"

	// CodeInvalidAttachmentRef 表示引用本身不合法。
	CodeInvalidAttachmentRef Code = "INVALID_ATTACHMENT_REF"
	// CodeAttachmentCorrupt 表示读出来的字节和引用记录的摘要对不上。
	CodeAttachmentCorrupt Code = "ATTACHMENT_CORRUPT"
	// CodeAttachmentWriteFailed 表示写入存储失败。
	CodeAttachmentWriteFailed Code = "ATTACHMENT_WRITE_FAILED"
	// CodeAttachmentNotFound 表示存储里没有这个附件。
	CodeAttachmentNotFound Code = "ATTACHMENT_NOT_FOUND"
	// CodeAttachmentReadFailed 表示从存储读取失败。
	CodeAttachmentReadFailed Code = "ATTACHMENT_READ_FAILED"
	// CodeAttachmentProjectionUnsupported 表示挂载的附件实现派生不出模型请求图。
	CodeAttachmentProjectionUnsupported Code = "ATTACHMENT_PROJECTION_UNSUPPORTED"
)

// imageAdmissionCodes 是「调用方自己能改对」的那一组码。
//
// 源: packages/attachment/attachment/src/error.ts:15-16（ImageAdmissionErrorCode）
//
// 它和存储故障的分界线是**谁能让下一次尝试成功**：这一组里的失败，调用方换一张图、
// 少传几张、或者换个格式就能过；不在这一组里的，调用方再试多少次都一样。
// 界面按这条线决定是提示操作者改输入，还是报一次系统故障。
var imageAdmissionCodes = map[Code]struct{}{
	CodeTooManyImages:          {},
	CodeImagesTooLarge:         {},
	CodeUnsupportedImageType:   {},
	CodeInvalidImageBase64:     {},
	CodeInvalidImage:           {},
	CodeImageTypeMismatch:      {},
	CodeImageTooLarge:          {},
	CodeImageTooManyPixels:     {},
	CodeImageDimensionTooLarge: {},
}

// ErrorCoder 是任何「带稳定路由码」的错误。
//
// 源: packages/attachment/attachment/src/error.ts:28-29
//
// 新增: DSH 的 isImageAdmissionError 判的是 `'code' in error && typeof error.code === 'string'`，
// 也就是**结构化**地看一个属性，而不是看原型链。它这么做有明确理由（写在 error.ts:31-39）：
// 基类 HarnessError 在 dsh-llm 里，而 dsh-llm 反过来依赖本包（ImageBlock 引用了
// ImageAttachmentRef），继承基类会成环。Go 有完全一样的禁止导入成环的规则，
// 所以这个约束照样存在。
//
// Go 的结构化类型体现在**方法**上而不是字段上，所以对应物是这个接口：任何包
// 只要给自己的错误加一个 ErrorCode() string 方法就满足它，**不需要 import 本包**，
// 于是环也就不会形成。这正是 DSH 那句「Consumers route on code, never on the
// prototype chain」在 Go 里的说法。
type ErrorCoder interface {
	error
	// ErrorCode 返回这个错误的稳定路由码。
	ErrorCode() string
}

// Error 是附件通道抛出的带类型的失败。
//
// 源: packages/attachment/attachment/src/error.ts:31-54
//
// 调用方用 errors.As 拿到它，**照着 Code 分派**，不要去匹配 Message 里的字。
type Error struct {
	// Code 是稳定的机器路由码。
	Code Code
	// Message 是给人看的描述。
	//
	// 它**不含原始字节，也不含宿主机路径**——这条是 DSH 在构造函数文档里写死的约束
	// （error.ts:45），理由是这段文字会跟着 RPC 一路送到客户端，而客户端不该看见
	// 宿主机的目录结构，也不该收到一段被当成文字渲染的二进制。
	// 也因此这里的文本保持英文：它是线上可见的载荷，不是给本仓库读者看的注释。
	Message string
	// Err 是底层原因，可以为 nil。
	//
	// 新增: DSH 用 ErrorOptions 的 cause 传底层原因，Go 里对应的是可被 errors.Is /
	// errors.As 问到的包装链。它只供日志和排查使用：分派一律看 Code。
	Err error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("attachment: %s（%s）：%v", e.Message, e.Code, e.Err)
	}
	return fmt.Sprintf("attachment: %s（%s）", e.Message, e.Code)
}

// Unwrap 让 errors.Is / errors.As 能问到底层原因。
func (e *Error) Unwrap() error { return e.Err }

// ErrorCode 让 *Error 满足 [ErrorCoder]。
func (e *Error) ErrorCode() string { return string(e.Code) }

// IsImageAdmissionError 区分「调用方自己能改对的图片准入失败」和「存储故障」。
//
// 源: packages/attachment/attachment/src/error.ts:56-68
//
// 它认的是 [ErrorCoder] 而不是 *[Error]：一个来自别的包、带着同一套码的错误
// 同样算数，理由见 [ErrorCoder]。不带码的普通错误一律不算——
// 判不出来就当成存储故障，是这条分界线上唯一安全的偏向：
// 把存储故障误报成「你的图不行」，操作者会去反复换图，而问题根本不在他那边。
//
// 新增: errors.As 会**顺着包装链往下问**，而 DSH 只看最外面那一层。
// Go 里包装错误是常态（fmt.Errorf 带 %w），只看最外层等于让「谁包了一下」
// 决定这个判断的结果。
func IsImageAdmissionError(err error) bool {
	var coder ErrorCoder
	if !errors.As(err, &coder) {
		return false
	}
	_, ok := imageAdmissionCodes[Code(coder.ErrorCode())]
	return ok
}
