// 本文件的作用：线上内容的两道关——进来的提示词怎么验、怎么落成耐久内容，
// 以及已提交的助手内容怎么翻回线上去。
//
// 源: packages/acp/acp/src/content.ts

package acp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	wire "github.com/coder/acp-go-sdk"

	"ds-harness-go/attachment"
	"ds-harness-go/core/agent"
	"ds-harness-go/llm"
)

// imageMediaTypes 是 ACP 图片块和核心附件词汇共有的那几种栅格格式。
//
// 源: packages/acp/acp/src/content.ts:11-16
var imageMediaTypes = []attachment.MediaType{
	attachment.MediaTypePNG,
	attachment.MediaTypeJPEG,
	attachment.MediaTypeWebP,
	attachment.MediaTypeGIF,
}

// ContentFailureKind 是内容准入失败的分类，协议处理那一层照它决定回哪个错误码。
//
// 源: packages/acp/acp/src/content.ts:22
type ContentFailureKind string

const (
	// ContentInvalid 是客户端给错了东西，线上报 invalid params。
	ContentInvalid ContentFailureKind = "invalid"
	// ContentInternal 是这一端自己没办成，线上报 internal error。
	ContentInternal ContentFailureKind = "internal"
)

// ContentError 是一次带稳定分类的内容失败，消息里**绝不夹原始二进制**。
//
// 源: packages/acp/acp/src/content.ts:25-39
//
// Message 面向协议对面，原样保留英文：它会被塞进 JSON-RPC 的错误消息里送出去，
// 而对面是一个程序，不是一个人。
type ContentError struct {
	// Kind 决定线上报 invalid params 还是 internal error。
	Kind ContentFailureKind
	// Message 是那句能安全送出去的说明。
	Message string
	// Err 是底层原因，只进日志，不上线。
	Err error
}

// Error 实现 error。
func (e *ContentError) Error() string { return e.Message }

// Unwrap 交出底层原因。
func (e *ContentError) Unwrap() error { return e.Err }

// invalidContent 造一个客户端错的内容失败。
func invalidContent(message string) *ContentError {
	return &ContentError{Kind: ContentInvalid, Message: message}
}

// internalContent 造一个这一端没办成的内容失败。
func internalContent(message string, cause error) *ContentError {
	return &ContentError{Kind: ContentInternal, Message: message, Err: cause}
}

// ModelResolver 是本包用得到的那一小块 LLM 服务：只问"这条路由上的模型收不收图"。
//
// 源: packages/acp/acp/src/content.ts:67, 100
//
// 新增: DSH 是 `ctx.get('llm')`——整个服务注入进来，用到的只有 resolveModelInfo 这一个
// 方法。这里写成一个单方法接口（窄口子的成例见
// [ds-harness-go/sdk/sdkserver.ProviderLister]），交进来的 [llm.Runtime] 自然满足它。
// 它可以为 nil，对应 DSH 那个 `?.`：这条线上根本没挂 LLM 服务。
type ModelResolver interface {
	// ResolveModelInfo 解算一条精确路由上的模型元数据。
	ResolveModelInfo(ctx context.Context, provider, model string) (llm.ResolvedModelInfo, error)
}

// imageMediaType 把一个线上 MIME 串收窄到耐久的栅格词汇里。
//
// 源: packages/acp/acp/src/content.ts:42-44
func imageMediaType(value string) (attachment.MediaType, bool) {
	mediaType := attachment.MediaType(value)
	if slices.Contains(imageMediaTypes, mediaType) {
		return mediaType, true
	}
	return "", false
}

// decodeImage 严格地解一个 ACP 内联图，不接受任何 base64 的别写法。
//
// 源: packages/acp/acp/src/content.ts:47-60
//
// 新增: DSH 先用一条正则判规范形，再解码、再编码回去和原串比——Node 的 Buffer.from
// 太宽松，它只能靠往返来发现问题。Go 的 base64.StdEncoding.Strict() 一趟就拒掉了
// 字母表外的字符、错的长度、错的填充和尾部非零余位，只差一处：`\r` 和 `\n` 被
// 解码器**有意忽略**（为了读 PEM 那类分行编码），而 DSH 那条正则拒它们。所以这里
// 补一条显式检查。
//
// 空串两边都放行：DSH 那条正则匹配空串，往返比较也过得去，于是它一路走到附件存储
// 那边被当成"这不是一张图"拒掉。这里保持一致——把它提前拒掉会换掉那句错误消息，
// 而那句消息是部署方定的。
func decodeImage(block *wire.ContentBlockImage) (attachment.ImageInput, error) {
	mediaType, ok := imageMediaType(block.MimeType)
	if !ok {
		return attachment.ImageInput{}, invalidContent("image mimeType must be image/png, image/jpeg, image/webp, or image/gif")
	}
	if strings.ContainsAny(block.Data, "\r\n") {
		return attachment.ImageInput{}, invalidContent("image data must be canonical base64")
	}
	data, err := base64.StdEncoding.Strict().DecodeString(block.Data)
	if err != nil {
		// 底层原因带着出错的字节偏移，那个偏移不该送到对面去。
		return attachment.ImageInput{}, invalidContent("image data must be canonical base64")
	}
	return attachment.ImageInput{Data: data, MediaType: mediaType}, nil
}

// routeOf 交出这个 agent **当下**那条精确路由：日志里最后一份请求头优先，
// 没有就退回它建出来时那份选项。
//
// 源: packages/acp/acp/src/content.ts:64-66
//
// 新增: DSH 的 `agent.session.requestHeader()` 不会失败。Go 这边它还会交回一个错误
// （日志坏了折不出来），这里把那种情形报成 internal 而不是当作"没有头"——静悄悄退回
// 建出来时那份选项，会让一张图被送到一条早就换掉的路由上去。
func routeOf(target agent.Agent) (provider string, model string, err error) {
	options := target.Options()
	provider, model = options.Provider, options.Model
	header, ok, headerErr := target.Session().RequestHeader()
	if headerErr != nil {
		return "", "", internalContent("the current model route could not be read for image input", headerErr)
	}
	if !ok {
		return provider, model, nil
	}
	if header.Config.Provider != "" {
		provider = header.Config.Provider
	}
	if header.Config.Model != "" {
		model = header.Config.Model
	}
	return provider, model, nil
}

// assertImageRoute 解出当下那条精确路由，并要求它**显式**声明收图。
//
// 源: packages/acp/acp/src/content.ts:63-80
func assertImageRoute(ctx context.Context, models ModelResolver, target agent.Agent) error {
	provider, model, err := routeOf(target)
	if err != nil {
		return err
	}
	if provider == "" || model == "" || models == nil {
		return invalidContent("the current model route could not be resolved for image input")
	}
	info, err := models.ResolveModelInfo(ctx, provider, model)
	if err != nil {
		return internalContent("the current model route could not be verified for image input", err)
	}
	// 一份 nil 的模态清单是"不知道"，一份显式给出、却没列图的清单是"不收图"。
	// 两种都不足以往上送一张图。
	if !slices.Contains(info.InputModalities, llm.ModalityImage) {
		return invalidContent(fmt.Sprintf("model %q does not declare image input", model))
	}
	return nil
}

// SupportsImagePrompts 判这条连接握手时能不能**如实**声明支持内联图提示词。
//
// 源: packages/acp/acp/src/content.ts:90-105
//
// 说不清的一律算否：没挂附件存储、没挂 LLM 服务、路由没配、部署根本不收这几种
// 栅格格式、模型没声明收图、解算失败——每一条都返回假。一句声明出去之后客户端会
// 照它发图，所以这里宁可说没有。
func SupportsImagePrompts(
	ctx context.Context,
	attachments attachment.Store,
	models ModelResolver,
	provider string,
	model string,
) bool {
	if attachments == nil || models == nil || provider == "" || model == "" {
		return false
	}
	limits := attachments.ImageLimits()
	if !slices.ContainsFunc(limits.MediaTypes, func(mediaType attachment.MediaType) bool {
		return slices.Contains(imageMediaTypes, mediaType)
	}) {
		return false
	}
	info, err := models.ResolveModelInfo(ctx, provider, model)
	if err != nil {
		return false
	}
	return slices.Contains(info.InputModalities, llm.ModalityImage)
}

// jsonQuote 把一个字符串按 JSON 字符串字面量的写法引起来。
//
// 新增: DSH 用 JSON.stringify。Go 的 [strconv.Quote] 不等价——它会把非 ASCII 转义成
// \uXXXX，而 JSON.stringify 原样留着；[encoding/json.Marshal] 也不等价——它默认把
// `<`、`>`、`&` 转义成 < 这类写法，而资源链接的 uri 里带查询串是常事，一个 `&`
// 变了写法，两侧记下来的就不是同一个链接。所以这里显式关掉 HTML 转义。
func jsonQuote(value string) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	// 编码一个 string 不会失败。
	_ = encoder.Encode(value)
	// Encode 会在末尾补一个换行。
	return strings.TrimSuffix(buffer.String(), "\n")
}

// resourceLinkText 把一条基线资源链接渲染成核心当下那套纯文本词汇。
//
// 源: packages/acp/acp/src/content.ts:108-110
func resourceLinkText(block *wire.ContentBlockResourceLink) string {
	return "\n[resource_link name=" + jsonQuote(block.Name) + " uri=" + jsonQuote(block.Uri) + "]\n"
}

// AdmitPrompt 把一条 ACP 提示词准入成有序的耐久核心内容。
//
// 源: packages/acp/acp/src/content.ts:124-205
//
// 次序是定死的：**每一个线上块和每一张图都先验完**，那批图才开始写。取消如果落在
// 一次已经成功的内容寻址写入之后，可能在存储里留下一个没人指得到的对象，但它绝不会
// 让一条迟到的用户消息排进队。
//
// 新增: DSH 的取消口是一个 AbortSignal，Go 里就是这个 ctx。
func AdmitPrompt(
	ctx context.Context,
	attachments attachment.Store,
	models ModelResolver,
	target agent.Agent,
	prompt []wire.ContentBlock,
	imageEnabled bool,
) (llm.Content, error) {
	var images []attachment.ImageInput
	for index := range prompt {
		block := &prompt[index]
		switch {
		case block.Text != nil, block.ResourceLink != nil:
			// 基线内容，一定收。
		case block.Image != nil:
			if !imageEnabled {
				return nil, invalidContent("inline image prompts were not advertised by this connection")
			}
			input, err := decodeImage(block.Image)
			if err != nil {
				return nil, err
			}
			images = append(images, input)
		case block.Audio != nil:
			return nil, invalidContent("audio prompt content is not supported")
		case block.Resource != nil:
			return nil, invalidContent("embedded resource prompt content is not supported")
		default:
			// 一个哪一支都没填的联合值：线上给的是本构建认不出的块标签。
			return nil, invalidContent("unsupported ACP prompt content")
		}
	}

	var refs []attachment.ImageRef
	if len(images) > 0 {
		if attachments == nil {
			return nil, invalidContent("no attachment store is mounted")
		}
		if err := assertImageRoute(ctx, models, target); err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		saved, err := attachment.SaveImages(ctx, attachments, images)
		if err != nil {
			if attachment.IsImageAdmissionError(err) {
				// 准入错误是部署方写给操作者看的话，原样转给对面。
				return nil, &ContentError{Kind: ContentInvalid, Message: err.Error(), Err: err}
			}
			return nil, internalContent("unable to persist the prompt image batch", err)
		}
		refs = saved
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}

	content := make(llm.Content, 0, len(prompt))
	var pendingText strings.Builder
	imageIndex := 0
	flushText := func() {
		if pendingText.Len() == 0 {
			return
		}
		content = append(content, llm.TextBlock{Text: pendingText.String()})
		pendingText.Reset()
	}
	for index := range prompt {
		block := &prompt[index]
		switch {
		case block.Text != nil:
			pendingText.WriteString(block.Text.Text)
		case block.ResourceLink != nil:
			pendingText.WriteString(resourceLinkText(block.ResourceLink))
		case block.Image != nil:
			flushText()
			content = append(content, llm.ImageBlock{Attachment: refs[imageIndex]})
			imageIndex++
		}
	}
	flushText()

	if !slices.ContainsFunc(content, func(block llm.ContentBlock) bool {
		if _, isImage := block.(llm.ImageBlock); isImage {
			return true
		}
		text, isText := block.(llm.TextBlock)
		return isText && strings.TrimSpace(text.Text) != ""
	}) {
		return nil, invalidContent("empty prompt")
	}
	return content, nil
}

// AssistantBlockToACP 把一个已提交的助手内容块翻成 ACP 线上内容。
//
// 源: packages/acp/acp/src/content.ts:215-238
//
// 第二个返回值为假表示这个块**不上线**：空文本，以及一切不是文本也不是图的块——
// 推理、工具调用这些是呈现和轨迹数据，不属于这条自动化线。
//
// 图会被重新读一遍、核对过完整性之后才内联送出去：读回来这一步本身就在验字节和
// 引用对不对得上（见 [attachment.Store.ReadImage]）。
func AssistantBlockToACP(
	ctx context.Context,
	attachments attachment.Store,
	block llm.ContentBlock,
) (wire.ContentBlock, bool, error) {
	switch typed := block.(type) {
	case llm.TextBlock:
		if typed.Text == "" {
			return wire.ContentBlock{}, false, nil
		}
		return wire.TextBlock(typed.Text), true, nil
	case llm.ImageBlock:
		if attachments == nil {
			return wire.ContentBlock{}, false, internalContent("cannot deliver assistant image: no attachment store is mounted", nil)
		}
		stored, err := attachments.ReadImage(ctx, typed.Attachment)
		if err != nil {
			return wire.ContentBlock{}, false, internalContent("cannot deliver assistant image: the attachment is unavailable or corrupt", err)
		}
		encoded := base64.StdEncoding.EncodeToString(stored.Data)
		return wire.ImageBlock(encoded, string(stored.Ref.MediaType)), true, nil
	default:
		return wire.ContentBlock{}, false, nil
	}
}
