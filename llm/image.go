// 本文件的作用：把持久的图片历史投影成一次具体请求装得下的样子——纯文本模型
// 换成占位文本，超预算的路由按确定的步长把**最旧**的几张换掉。
//
// 源: packages/llm/llm/src/content.ts:7-202

package llm

import (
	"fmt"

	"ds-harness-go/attachment"
)

// OffloadedImageText 是一张为了塞进请求上限而被拿掉的图，留给模型看的替身。
//
// 源: packages/llm/llm/src/content.ts:7-9
//
// 它是**面向模型**的英文原文，一个字都不改：这段话要让模型自己判断「这里本来有张图、
// 旧的先被拿掉、还需要的话去重新读文件或者请用户再传一次」。翻译它等于改提示词。
const OffloadedImageText = "[image omitted to keep the request within its image limit; older images are omitted first. If this image is still needed, read its file again when a path is available; otherwise ask the user to attach it again.]"

// attachmentDigestOffset 是 `sha256:` 前缀的长度，摘要从这里开始。
const attachmentDigestOffset = len("sha256:")

// attachmentDigestLength 是占位文本里露出的摘要长度。
const attachmentDigestLength = 8

// TextOnlyImageText 是给一个收不了图的模型看的、稳定的单图占位文本。
//
// 源: packages/llm/llm/src/content.ts:11-19
//
// 新增: DSH 直接按 7..15 下标切，不检查越界——JS 的 slice 越界返回空串，不会崩。
// Go 的切片越界会 panic，所以这里把两个下标夹到长度以内。对格式正常的标识
// （`sha256:` 开头、摘要够长）两边逐字节一致；标识不正常时这里给一段更短的摘要，
// 而不是崩掉一次请求装配。
func TextOnlyImageText(ref attachment.ImageRef) string {
	id := string(ref.ID)
	start := min(attachmentDigestOffset, len(id))
	end := min(start+attachmentDigestLength, len(id))
	return fmt.Sprintf(
		"[image omitted because this model accepts text only; attachment sha256:%s]",
		id[start:end],
	)
}

// RequestImageHandleText 是一份确切请求版本给模型看的、稳定的把手文本。
//
// 源: packages/llm/llm/src/content.ts:21-28
func RequestImageHandleText(version attachment.RequestImage) string {
	return fmt.Sprintf(
		"Image %s; request image %dx%dpx.",
		version.Attachment.ID, version.Width, version.Height,
	)
}

// base64Length 是若干原始字节内联成 base64 之后的长度，含补位。
//
// 源: packages/llm/llm/src/content.ts:43-46
func base64Length(bytes int) int {
	return ceilDiv(bytes, 3) * 4
}

// ceilDiv 是向上取整的整数除法。
//
// 新增: DSH 用 Math.ceil(a / b)，那是浮点除法再取整。Go 里整数除法本来就截断，
// 补一个 divisor-1 就是同一件事，而且不经过 float64——图片字节数会大到
// float64 的整数精度边界附近，绕开浮点就绕开了那个问题。
func ceilDiv(dividend, divisor int) int {
	if dividend <= 0 {
		return 0
	}
	return (dividend + divisor - 1) / divisor
}

// ImageRepresentation 是一条路由按什么口径算图片字节。
//
// 源: packages/llm/llm/src/content.ts:58-59
type ImageRepresentation string

const (
	// RepresentationRaw 按原始文件字节算。
	RepresentationRaw ImageRepresentation = "raw"
	// RepresentationBase64 按内联 base64 之后的长度算。
	RepresentationBase64 ImageRepresentation = "base64"
)

// RequestImageOffloadPolicy 是一种请求表示形式的字节记账方式和量化移除策略。
//
// 源: packages/llm/llm/src/content.ts:48-62
type RequestImageOffloadPolicy struct {
	// MaxImages 是这条路由接受的图片张数；nil 表示不限张数。
	//
	// 新增: 这里用指针，因为 0 是有意义的——「这条路由一张图都不收」，
	// 和「不限张数」正好相反。
	MaxImages *int
	// MaxBytes 是这条路由接受的图片总字节数；nil 表示不限字节。理由同 MaxImages。
	MaxBytes *int
	// CountQuantum 是一次确定性移除的张数步长；0 表示按 1 算。
	//
	// 用 0 表示默认而不用指针：步长为零会让「移除多少」这个除法没有意义，
	// 不是一个可以被选择的取值。
	CountQuantum int
	// ByteQuantum 是一次确定性移除的字节步长；0 表示按 1 算。理由同 CountQuantum。
	ByteQuantum int
	// Representation 是按哪种口径算字节，必填。
	Representation ImageRepresentation
	// ByteLength 给出一张图编码后的请求版本长度；nil 表示用主附件自己的字节数。
	ByteLength func(ref attachment.ImageRef) int
}

// collectImageLengths 按请求顺序和嵌套顺序，收集每张图在本策略口径下的长度。
//
// 源: packages/llm/llm/src/content.ts:64-80
func collectImageLengths(blocks Content, lengths []int, policy RequestImageOffloadPolicy) []int {
	for _, block := range blocks {
		switch typed := block.(type) {
		case ImageBlock:
			bytes := typed.Attachment.Bytes
			if policy.ByteLength != nil {
				bytes = policy.ByteLength(typed.Attachment)
			}
			if policy.Representation == RepresentationBase64 {
				bytes = base64Length(bytes)
			}
			lengths = append(lengths, bytes)
		case ToolResultBlock:
			lengths = collectImageLengths(typed.Content, lengths, policy)
		}
	}
	return lengths
}

// replaceOldestImages 把最靠前的 remaining 张图换成 [OffloadedImageText]，
// 返回换过之后的内容和「有没有换过」。
//
// 源: packages/llm/llm/src/content.ts:82-106
//
// 新增: DSH 靠「返回的数组是不是同一个对象」判断有没有改过（`content !== block.content`），
// 那是 JS 的引用同一性。Go 的切片比不了同一性，所以这里明说：第二个返回值就是
// 那件事。没改过时返回**原来那个切片**，一次分配都不做——这和 DSH 的意图一致：
// 一次投影不该把没碰过的历史整个复制一遍。
func replaceOldestImages(blocks Content, remaining *int) (Content, bool) {
	var next Content
	changed := false
	for index, block := range blocks {
		if _, isImage := block.(ImageBlock); isImage && *remaining > 0 {
			*remaining--
			if !changed {
				next = append(next, blocks[:index]...)
				changed = true
			}
			next = append(next, TextBlock{Text: OffloadedImageText})
			continue
		}
		if result, isResult := block.(ToolResultBlock); isResult {
			content, nested := replaceOldestImages(result.Content, remaining)
			if nested {
				if !changed {
					next = append(next, blocks[:index]...)
					changed = true
				}
				result.Content = content
				next = append(next, result)
				continue
			}
		}
		if changed {
			next = append(next, block)
		}
	}
	if !changed {
		return blocks, false
	}
	return next, true
}

// replaceImagesForTextModel 把每一张图（含嵌套在工具结果里的）都换成
// [TextOnlyImageText]，返回换过之后的内容和「有没有换过」。
//
// 源: packages/llm/llm/src/content.ts:108-128
func replaceImagesForTextModel(blocks Content) (Content, bool) {
	var next Content
	changed := false
	for index, block := range blocks {
		if image, isImage := block.(ImageBlock); isImage {
			if !changed {
				next = append(next, blocks[:index]...)
				changed = true
			}
			next = append(next, TextBlock{Text: TextOnlyImageText(image.Attachment)})
			continue
		}
		if result, isResult := block.(ToolResultBlock); isResult {
			content, nested := replaceImagesForTextModel(result.Content)
			if nested {
				if !changed {
					next = append(next, blocks[:index]...)
					changed = true
				}
				result.Content = content
				next = append(next, result)
				continue
			}
		}
		if changed {
			next = append(next, block)
		}
	}
	if !changed {
		return blocks, false
	}
	return next, true
}

// ProjectImagesForTextModel 把持久的图片历史投影成一个确切的纯文本模型看得懂的文本。
//
// 源: packages/llm/llm/src/content.ts:130-141
//
// 历史里一张图都没有时原样返回；有图时返回一份浅复制，图的位置换成稳定占位文本。
func ProjectImagesForTextModel(messages []Message) []Message {
	hasImage := false
	for _, message := range messages {
		if ContentHasImage(message.Content) {
			hasImage = true
			break
		}
	}
	if !hasImage {
		return messages
	}

	projected := make([]Message, len(messages))
	for index, message := range messages {
		content, changed := replaceImagesForTextModel(message.Content)
		if changed {
			message.Content = content
		}
		projected[index] = message
	}
	return projected
}

// OffloadRequestImages 把最旧的几张图换掉，直到累计的 base64 载荷装得进给定上限。
//
// 源: packages/llm/llm/src/content.ts:143-161
//
// maxRequestImageBytes 为 nil 时每张图都保留。选谁被换掉只由持久的消息顺序和附件
// 元数据决定，所以是确定的：提供方可以直接序列化返回的这些消息，不必去读被拿掉的字节。
func OffloadRequestImages(messages []Message, maxRequestImageBytes *int) []Message {
	return OffloadRequestImagesWithPolicy(messages, RequestImageOffloadPolicy{
		Representation: RepresentationBase64,
		MaxBytes:       maxRequestImageBytes,
		ByteQuantum:    1,
	})
}

// OffloadRequestImagesWithPolicy 在超出路由预算之后，按**整张数**和**整字节**两个
// 步长把最旧的图换掉，给出一份确定的临时投影。
//
// 源: packages/llm/llm/src/content.ts:163-202
//
// 移除目标只取决于完整的持久历史：129 张一兆的图、128 MiB 上限、64 MiB 步长时，
// 最旧的 65 张被拿掉，剩下 64 MiB；这个被拿掉的前缀会一直固定，直到历史总量超过
// 192 MiB 为止。这一点是缓存复用的前提——同一段历史每次都投影成同一份请求。
func OffloadRequestImagesWithPolicy(messages []Message, policy RequestImageOffloadPolicy) []Message {
	var lengths []int
	for _, message := range messages {
		lengths = collectImageLengths(message.Content, lengths, policy)
	}
	total := 0
	for _, length := range lengths {
		total += length
	}

	excessCount := 0
	if policy.MaxImages != nil {
		excessCount = max(0, len(lengths)-*policy.MaxImages)
	}
	excessBytes := 0
	if policy.MaxBytes != nil {
		excessBytes = max(0, total-*policy.MaxBytes)
	}
	if excessCount == 0 && excessBytes == 0 {
		return messages
	}

	countQuantum := policy.CountQuantum
	if countQuantum <= 0 {
		countQuantum = 1
	}
	byteQuantum := policy.ByteQuantum
	if byteQuantum <= 0 {
		byteQuantum = 1
	}
	removeCount := ceilDiv(excessCount, countQuantum) * countQuantum
	removeBytes := ceilDiv(excessBytes, byteQuantum) * byteQuantum

	count := 0
	removedBytes := 0
	for _, imageBytes := range lengths {
		// 步长为 1 时移到「刚好够」就停，步长大于 1 时要移**过**目标才停。
		// 这个不对称是照抄的：步长大于 1 意味着调用方要的是整块整块地移除，
		// 停在正好等于目标的地方会留下一个不满一整块的尾巴。
		byteTargetMet := removeBytes == 0
		if !byteTargetMet {
			if byteQuantum == 1 {
				byteTargetMet = removedBytes >= removeBytes
			} else {
				byteTargetMet = removedBytes > removeBytes
			}
		}
		if count >= removeCount && byteTargetMet {
			break
		}
		removedBytes += imageBytes
		count++
	}

	remaining := count
	projected := make([]Message, len(messages))
	for index, message := range messages {
		content, changed := replaceOldestImages(message.Content, &remaining)
		if changed {
			message.Content = content
		}
		projected[index] = message
	}
	return projected
}
