// 本文件的作用：把一次 MCP 工具调用回来的内容块，投影成本装置的核心内容词汇表——
// 包括那条「图存得下就给模型看图、存不下就整批降级成诊断文本」的分支。
//
// 源: packages/mcp/mcp-client/src/tools.ts:195-208、364-559

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"ds-harness-go/attachment"
	"ds-harness-go/core/tools"
	"ds-harness-go/llm"
)

// Result 是一次 MCP 工具调用那份**权威的**值：既是给程序化调用方的原件，
// 也是本包按输出契约验过、据以渲染的那个东西。
//
// 源: packages/mcp/mcp-client/src/tools.ts:24-27
//
// Content 里每一项是一块 MCP 内容原样序列化回去的 JSON。留着这一份的理由是
// 「图这一路」：一次结果里的图降级成文本之后，原始的图片字节仍然在这里，
// 程序化调用方读得到。
type Result struct {
	// Content 是那一列 MCP 内容块。
	Content []json.RawMessage `json:"content"`
	// StructuredContent 是对方给的结构化返回值，没有就为 nil。
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
}

// contentBlock 是本包从每一块 MCP 内容里读出来的那几个字段。
//
// 源: packages/mcp/mcp-client/src/tools.ts:201-208
//
// 新增: DSH 这个接口比 SDK 的 ContentBlock 松，因为它站在网络信任边界上——
// 规范里声明必填的字段，一台有 bug 的服务器照样可能不给。Go 的 SDK 把这一层
// typed 化了，缺字段解出来是零值，所以这里的字段全是值不是指针；
// 「零值」在本包里就当「没有」讲。
//
// 新增: 因此有一处**分不开**的地方：DSH 区分得了「text 是空串」和「根本没有 text」
// （前者贡献一个空行，后者什么都不贡献），Go 这边两者都是零值，一律按「没有」走。
// audio 的 mimeType、resource_link 的 name/uri 同理。要分开它得绕开 SDK 自己解
// JSON-RPC，那正是包文档里说的「不要照抄别人造的轮子」要避免的事。
type contentBlock struct {
	// Type 是这一块的判别标签，取 MCP 线上的那个名字。
	Type string
	// Text 是 text 块的正文。
	Text string
	// MediaType 是 image/audio 块声称的媒体类型。
	MediaType string
	// Data 是 image/audio 块的字节，SDK 已经按 base64 解过。
	Data []byte
	// Name 和 URI 是 resource_link 块的两个必填字段。
	Name string
	URI  string
}

// 那几个本包认得的 MCP 内容标签。
//
// 源: packages/mcp/mcp-client/src/tools.ts:517-556
const (
	blockText         = "text"
	blockImage        = "image"
	blockAudio        = "audio"
	blockResourceLink = "resource_link"
	blockResource     = "resource"
)

// unsupportedBlock 是「这一块根本不是一个 MCP 内容对象」的内部标签。
//
// 源: packages/mcp/mcp-client/src/tools.ts:525-527
//
// 开头那个 NUL 保证它撞不上任何一个真实的线上标签：MCP 的 type 是 JSON 字符串，
// 而本包只会在自己读不动那一块时才造出这个值。
const unsupportedBlock = "\x00not-an-object"

// imageMediaTypes 是能进持久附件仓库的那几种图。
//
// 源: packages/mcp/mcp-client/src/tools.ts:61-66
var imageMediaTypes = map[string]attachment.MediaType{
	string(attachment.MediaTypePNG):  attachment.MediaTypePNG,
	string(attachment.MediaTypeJPEG): attachment.MediaTypeJPEG,
	string(attachment.MediaTypeWebP): attachment.MediaTypeWebP,
	string(attachment.MediaTypeGIF):  attachment.MediaTypeGIF,
}

// normalizeContent 把 SDK 解出来的那一列内容块，变成本包读的那份扁平记录，
// 同时把每一块原样序列化回去，凑出 [Result.Content]。
//
// 两件事一趟做完，是为了保证「读的那一份」和「留给程序化调用方的那一份」
// 一定来自同一块内容，不会因为两次遍历各走各的而对不上。
func normalizeContent(blocks []sdk.Content) ([]contentBlock, []json.RawMessage, error) {
	// 两个切片都造成非 nil：一次没有内容的结果，它的 content 是一个空列表，
	// 不是「没有列表」——后者序列化出去是 null，那会让输出 schema 的 content 必填项落空。
	normalized := make([]contentBlock, 0, len(blocks))
	raw := make([]json.RawMessage, 0, len(blocks))
	for _, block := range blocks {
		encoded, err := block.MarshalJSON()
		if err != nil {
			return nil, nil, fmt.Errorf("mcp: 这一块内容序列化不回去：%w", err)
		}
		normalized = append(normalized, normalizeBlock(block, encoded))
		raw = append(raw, encoded)
	}
	return normalized, raw, nil
}

// normalizeBlock 把一块 typed 的 SDK 内容读成本包那份扁平记录。
//
// 源: packages/mcp/mcp-client/src/tools.ts:201-208
func normalizeBlock(block sdk.Content, encoded json.RawMessage) contentBlock {
	switch typed := block.(type) {
	case *sdk.TextContent:
		return contentBlock{Type: blockText, Text: typed.Text}
	case *sdk.ImageContent:
		return contentBlock{Type: blockImage, MediaType: typed.MIMEType, Data: typed.Data}
	case *sdk.AudioContent:
		return contentBlock{Type: blockAudio, MediaType: typed.MIMEType, Data: typed.Data}
	case *sdk.ResourceLink:
		return contentBlock{Type: blockResourceLink, Name: typed.Name, URI: typed.URI}
	case *sdk.EmbeddedResource:
		return contentBlock{Type: blockResource}
	default:
		// 采样那两块（tool_use / tool_result）以及以后 SDK 新加的块都落这里。
		// 标签从它自己序列化出来的字节里读，好让那句「不支持的内容类型」说得出
		// 到底是什么类型，而不是印一个 Go 的类型名给模型看。
		return contentBlock{Type: wireType(encoded)}
	}
}

// wireType 从一块内容序列化出来的字节里读出它的 type 标签。
func wireType(encoded json.RawMessage) string {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(encoded, &probe); err != nil || probe.Type == "" {
		return "unknown"
	}
	return probe.Type
}

// imageProjector 把第 index 块图投影成一块核心内容。
//
// 源: packages/mcp/mcp-client/src/tools.ts:512-515
type imageProjector func(block contentBlock, index int) llm.ContentBlock

// projectContent 把有序的 MCP 内容块投影进核心内容词汇表。
//
// 源: packages/mcp/mcp-client/src/tools.ts:509-559
//
// 连着的文本块按换行合并成一块；被准入的图在**原来的位置**把那一串文本劈开。
// image 为 nil 时用默认投影器，也就是「这张图没能进持久上下文」那句诊断。
func projectContent(blocks []contentBlock, toolName string, image imageProjector) llm.Content {
	if image == nil {
		image = func(block contentBlock, _ int) llm.ContentBlock {
			return llm.TextBlock{Text: imageDiagnostic(block, "this result was not admitted to durable model context")}
		}
	}
	projected := make(llm.Content, 0, len(blocks))
	var text []string
	flushText := func() {
		if len(text) == 0 {
			return
		}
		projected = append(projected, llm.TextBlock{Text: strings.Join(text, "\n")})
		text = nil
	}

	for index, block := range blocks {
		switch block.Type {
		case blockText:
			if block.Text != "" {
				text = append(text, block.Text)
			}
		case blockImage:
			flushText()
			projected = append(projected, image(block, index))
		case blockResourceLink:
			if block.Name == "" || block.URI == "" {
				text = append(text, "[resource link unavailable: the MCP block is missing its name or URI]")
			} else {
				text = append(text, fmt.Sprintf("Resource link: %s (%s)", block.Name, block.URI))
			}
		case blockAudio:
			mediaType := block.MediaType
			if mediaType == "" {
				mediaType = "unknown media type"
			}
			text = append(text, fmt.Sprintf(
				"[audio result unsupported: %s; raw audio data remains available to programmatic callers]", mediaType))
		case unsupportedBlock:
			text = append(text, "[unsupported MCP content block: expected an object]")
		case blockResource:
			text = append(text,
				"[embedded resource unsupported; raw resource data remains available to programmatic callers]")
		default:
			text = append(text, fmt.Sprintf("[unsupported MCP content type: %s]", block.Type))
		}
	}
	flushText()
	if len(projected) == 0 {
		// 一次什么都没给的结果仍然要有一句话：模型看见空内容和看见「没有内容」
		// 是两回事，后者说得出是哪个工具没给东西。
		return llm.Content{llm.TextBlock{Text: fmt.Sprintf("(%s returned no model-visible content)", toolName)}}
	}
	return projected
}

// extractText 把一列 MCP 内容抽成一段文本。
//
// 源: packages/mcp/mcp-client/src/tools.ts:497-502
//
// 走的是默认投影器，所以每一块出来都是文本，拿换行接起来就是全部。
func extractText(blocks []contentBlock, toolName string) string {
	projected := projectContent(blocks, toolName, nil)
	parts := make([]string, 0, len(projected))
	for _, block := range projected {
		// 默认投影器只产文本块，所以这个断言走不到 false 分支。
		if text, ok := block.(llm.TextBlock); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// containsImage 判一列 MCP 内容里有没有声称自己是图的块。
//
// 源: packages/mcp/mcp-client/src/tools.ts:364-366
func containsImage(blocks []contentBlock) bool {
	for _, block := range blocks {
		if block.Type == blockImage {
			return true
		}
	}
	return false
}

// decodeImage 验一块来路不明的 MCP 图。
//
// 源: packages/mcp/mcp-client/src/tools.ts:379-391
//
// 新增: DSH 在这里还要判 base64 是不是规范的（拒 URL-safe 别名和多余空白）。
// Go 的 SDK 拿 `encoding/json` 按 base64.StdEncoding 解 []byte，解不动整条请求就失败了，
// 所以那一支在这里走不到，只剩下媒体类型这一支。
func decodeImage(block contentBlock) (attachment.ImageInput, error) {
	mediaType, ok := imageMediaTypes[block.MediaType]
	if !ok {
		return attachment.ImageInput{}, errors.New("the declared media type is not PNG, JPEG, WebP, or GIF")
	}
	return attachment.ImageInput{Data: block.Data, MediaType: mediaType}, nil
}

// imageDiagnostic 是一张没能被准入的图那句稳定的诊断文本。
//
// 源: packages/mcp/mcp-client/src/tools.ts:423-426
func imageDiagnostic(block contentBlock, reason string) string {
	mediaType := block.MediaType
	if mediaType == "" {
		mediaType = "unknown media type"
	}
	return fmt.Sprintf(
		"[image unavailable: %s; %s; raw image data remains available to programmatic callers]", mediaType, reason)
}

// prepareImageProjection 解码、预检并持久保存一次结果里那一批有序的图。
//
// 源: packages/mcp/mcp-client/src/tools.ts:433-487
//
// 任何一步拒绝，都把这次结果里的**每一张**图都投影成文本，而那份权威的原始值
// 一个字节不改地留给程序化调用方。整批一起降级是要紧的：只降级坏的那一张，
// 模型会以为剩下的图是它看见的全部。
func prepareImageProjection(
	ctx context.Context,
	admit ImageAdmission,
	exec tools.Execution,
	blocks []contentBlock,
	toolName string,
) llm.Content {
	var inputs []attachment.ImageInput
	validationErrors := map[int]string{}
	var imageIndexes []int
	for index, block := range blocks {
		if block.Type != blockImage {
			continue
		}
		imageIndexes = append(imageIndexes, index)
		input, err := decodeImage(block)
		if err != nil {
			validationErrors[index] = err.Error()
			continue
		}
		inputs = append(inputs, input)
	}
	if len(validationErrors) > 0 {
		return projectContent(blocks, toolName, func(block contentBlock, index int) llm.ContentBlock {
			reason, ok := validationErrors[index]
			if !ok {
				reason = "another image in the same result was invalid"
			}
			return llm.TextBlock{Text: imageDiagnostic(block, reason)}
		})
	}

	store, err := resolveImageAdmission(ctx, admit, exec)
	if err != nil {
		reason := err.Error()
		return projectContent(blocks, toolName, func(block contentBlock, _ int) llm.ContentBlock {
			return llm.TextBlock{Text: imageDiagnostic(block, reason)}
		})
	}

	refs, err := attachment.SaveImages(ctx, store, inputs)
	if err != nil {
		reason := "durable image storage rejected the result"
		var admission *attachment.Error
		if attachment.IsImageAdmissionError(err) && errors.As(err, &admission) {
			// 取 Message 而不是 Error()：后者缀着包名和错误码，那两样是给日志
			// 和调用方分流用的，不该出现在一句给模型看的诊断里。DSH 取的也是 error.message。
			reason = fmt.Sprintf("image admission rejected the result: %s", admission.Message)
		}
		return projectContent(blocks, toolName, func(block contentBlock, _ int) llm.ContentBlock {
			return llm.TextBlock{Text: imageDiagnostic(block, reason)}
		})
	}

	byIndex := make(map[int]attachment.ImageRef, len(imageIndexes))
	for offset, index := range imageIndexes {
		byIndex[index] = refs[offset]
	}
	return projectContent(blocks, toolName, func(_ contentBlock, index int) llm.ContentBlock {
		return llm.ImageBlock{Attachment: byIndex[index]}
	})
}

// resolveImageAdmission 问一次装配方：这次执行能不能收图、收的话存到哪。
//
// 源: packages/mcp/mcp-client/src/tools.ts:399-420
//
// 新增: DSH 在这里自己读 `exec.agent.session.requestHeader()` 和
// `llm.resolveModelInfo(...)`，为的是证明「这次请求真正路由到的那个模型收图」。
// 本包在第 4 块，活会话和 llm 服务都在后面的块里，够不着，所以那一整段换成
// [Options.ImageAdmission] 这个接缝。判断的**责任**没变，只是搬到了装配方那边。
//
// 交回的错误那句话会原样进模型看的诊断文本，所以它得是一句人话。
func resolveImageAdmission(ctx context.Context, admit ImageAdmission, exec tools.Execution) (attachment.Store, error) {
	if admit == nil {
		return nil, errors.New("no attachment store is mounted")
	}
	store, err := admit(ctx, exec)
	if err != nil {
		return nil, err
	}
	if store == nil {
		// 一条 (nil, nil) 的答复往下走就是解引用 panic；把它当成一次拒绝，
		// 理由用 DSH 那句「没装仓库」——对模型来说这两件事是同一件事。
		return nil, errors.New("no attachment store is mounted")
	}
	if ctx.Err() != nil {
		// 准入可能等一段慢存储；调用方在那期间撤回了，就不该再往仓库里写字节。
		return nil, errors.New("the tool call was canceled before image storage")
	}
	return store, nil
}
