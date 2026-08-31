// 本文件是线上那一层的准入口：把 base64 载荷剥成字节，再交给 [Store] 走批次规则。
//
// 源: packages/attachment/attachment/src/admission.ts

package attachment

import (
	"context"
	"encoding/base64"
	"strings"
)

// decodeBase64 解一条上传载荷，同时拒掉一切**非规范形**的 base64。
//
// 源: packages/attachment/attachment/src/admission.ts:8-15
//
// 为什么非要规范形，而不是「能解出来就行」：附件是内容寻址的，标识由字节的摘要算出来。
// 宽松解码下 `AAAA`、`AAAA\n`、`AAA` 可能解出同一串字节，于是同一张图会有多种线上写法。
// 这本身还不致命，致命的是它让「这条请求和那条请求是不是同一张图」这个判断
// 取决于编码时多打了几个空白——而去重、缓存、配额都压在这个判断上。收紧到只认一种写法，
// 这件事就退化成一次字节比较。
//
// 新增: DSH 的做法是解码之后**再编码回去和原串比**（Node 的 Buffer.from 极其宽松，
// 非法字符直接跳过、缺填充自动补，所以它只能靠往返来发现问题）。Go 的
// base64.StdEncoding.Strict() 一趟就能拒掉绝大部分：字母表外的字符、长度不对、
// 填充不对、以及尾部的非零余位。实测（tmp/probeb64）确认它和那个往返检查只差两处：
//
//   - 空串：Strict 认它是合法的零字节，DSH 有一条显式的 length === 0 检查把它拒掉。
//     一张零字节的图不是图，让它进去只会在后面某个解码器里炸开。
//   - `\r` 和 `\n`：Go 的解码器**有意忽略**这两个字符（好读 PEM 那类分行编码），
//     而往返比较会因为它们不见了而报不等。
//
// 所以这里补两条显式检查。空白字符里只判这两个就够了：其余空白（空格、制表符）
// 不在 base64 字母表里，Strict 自己会拒——这一点也在那次实测里确认过（"AA AA" 被拒）。
func decodeBase64(data string) ([]byte, error) {
	if data == "" || strings.ContainsAny(data, "\r\n") {
		return nil, &Error{
			Code:    CodeInvalidImageBase64,
			Message: "Image upload is not canonical base64.",
		}
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(data)
	if err != nil {
		return nil, &Error{
			Code:    CodeInvalidImageBase64,
			Message: "Image upload is not canonical base64.",
			// 底层原因只进日志：它带着出错的字节偏移，而那个偏移不该送到客户端去。
			Err: err,
		}
	}
	return decoded, nil
}

// AdmitEncodedImages 准入一批线上图片：先把每一条的 base64 校验并解码，
// 再把整批交给 [SaveImages] 去走张数、字节总和、媒体类型和逐张校验，最后有序提交。
//
// 源: packages/attachment/attachment/src/admission.ts:26-41
//
// 它是每一个接受浏览器上传的 RPC 端点共用的入口。
//
// 解码放在**任何一次 store 调用之前**全部做完，和 [SaveImages] 里「先校验完再写」
// 是同一条道理的延伸：一条载荷是坏的，这批就发不出去，那就不该为它先动存储。
// 这一点由测试钉住。
//
// 返回的引用顺序和 images 一致。
func AdmitEncodedImages(
	ctx context.Context, store Store, images []EncodedImage,
) ([]ImageRef, error) {
	inputs := make([]ImageInput, 0, len(images))
	for _, image := range images {
		data, err := decodeBase64(image.Data)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, ImageInput{
			Data:      data,
			MediaType: image.MediaType,
			Name:      image.Name,
		})
	}
	return SaveImages(ctx, store, inputs)
}
