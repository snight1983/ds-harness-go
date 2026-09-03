// 本文件的作用：认出一段字节是哪种图、内在宽高多少（只读文件头），
// 以及准入时把栅格真解出来一遍。
//
// 源: packages/attachment/attachment-local/src/image.ts

package imagestore

import (
	"bytes"
	"image"

	// 这四个只为注册解码器，本文件不直接引用它们的标识符。
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"

	"github.com/snight1983/ds-harness-go/attachment"
)

// mediaTypes 把 [image] 登记的格式名映射到接缝上的媒体类型。
//
// 源: packages/attachment/attachment-local/src/image.ts:44-49
//
// 这张表同时是**认不认**的判据：解码器登记表里可能有别的格式（本包只导入这四个，
// 但导入是全局的，别的包再导入一个就会多出一种），认不出的一律当成不是图。
var mediaTypes = map[string]attachment.MediaType{
	"png":  attachment.MediaTypePNG,
	"jpeg": attachment.MediaTypeJPEG,
	"webp": attachment.MediaTypeWebP,
	"gif":  attachment.MediaTypeGIF,
}

// detected 是从文件头认出来的那几件事。
type detected struct {
	mediaType attachment.MediaType
	width     int
	height    int
}

// probe 只读文件头，不解像素。
//
// 源: packages/attachment/attachment-local/src/image.ts:91-98
//
// 新增: GIF 这里报的是**逻辑屏幕**的宽高，而不是第一帧的宽高——两者可以不一样。
// 存和读走的是同一个函数，所以引用上记的和读回时算出来的必然一致，
// 这条差异对核对没有影响。
func probe(data []byte) (detected, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return detected{}, &attachment.Error{
			Code:    attachment.CodeInvalidImage,
			Message: "Unsupported or malformed image data.",
			Err:     err,
		}
	}
	mediaType, ok := mediaTypes[format]
	if !ok {
		return detected{}, &attachment.Error{
			Code:    attachment.CodeInvalidImage,
			Message: "Unsupported or malformed image data.",
		}
	}
	return detected{mediaType: mediaType, width: config.Width, height: config.Height}, nil
}

// decodeRaster 把栅格整个解出来，只为证明它解得开。
//
// 源: packages/attachment/attachment-local/src/image.ts:124
//
// [attachment.Store.ValidateImage] 明说准入必须完整解码。文件头查得出格式和宽高，
// 查不出「后面那段字节是不是截断的、是不是坏的」——而一张头部完好、数据段坏掉的图
// 会一路存进去，直到某个模型提供方在很远的地方解不开它。
func decodeRaster(data []byte) error {
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return &attachment.Error{
			Code:    attachment.CodeInvalidImage,
			Message: "Unsupported or malformed image data.",
			Err:     err,
		}
	}
	return nil
}
