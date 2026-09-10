// 本文件的作用：可选能力 [attachment.RequestImageProjector]——把存下来的那一张，
// 按某条模型路由自己的像素和字节预算，派生成一份确定性的请求版本。
//
// 源: packages/attachment/attachment-local/src/request-image.ts
// 源: packages/attachment/attachment-local/src/encoding.ts

package imagestore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/jpeg"
	"image/png"
	"strings"

	"golang.org/x/image/draw"

	"github.com/snight1983/ds-harness-go/attachment"
	"github.com/snight1983/ds-harness-go/fs"
)

// transformVersion 是每一个变体标识里都带的那个变换版本号。
//
// 源: packages/attachment/attachment-local/src/request-image.ts:25
//
// 编码参数变了就要动它——不动的话，同一个标识会指向两份不同的字节，
// 而缓存和上传索引都拿它当键。
const transformVersion = "request-image-v5"

// encodingQualities 是有损那一档的质量阶梯，一级一级都要买到看得见的体积下降。
//
// 源: packages/attachment/attachment-local/src/encoding.ts:6
var encodingQualities = []int{85, 75, 60}

// alphaPixelDivisors 是带 alpha 那一档的像素预算阶梯。
//
// 新增: attachment-local 给带 alpha 的图走的是**有损 WebP** 的质量阶梯。
// Go 这边没有 WebP 编码器（golang.org/x/image 只有解码器），换成 JPEG 又会把
// alpha 通道整个丢掉——一张抠好的商品图会平白多出一块黑底。所以这一档用 PNG，
// 而 PNG 是无损的、没有质量旋钮，唯一还剩的杠杆就是继续缩小：像素预算依次砍半。
var alphaPixelDivisors = []int{1, 2, 4}

// 编译期钉住：这台存储具备那个可选能力。
var _ attachment.RequestImageProjector = (*Store)(nil)

// requestVersion 是阶梯上的一个候选，或者最后选中的那一份。
type requestVersion struct {
	data      []byte
	mediaType attachment.MediaType
	width     int
	height    int
	hasAlpha  bool
	// verbatim 表示这就是存储里那些字节本身，一个 bit 都没重新编码过。
	// 它决定要不要写缓存：源字节已经在对象树里了，再存一份是白占地方。
	verbatim bool
}

// ReadImageRequest 实现 [attachment.RequestImageProjector]。
//
// 源: packages/attachment/attachment-local/src/request-image.ts:176-207
//
// 确定性来自两处：变体标识覆盖了「附件 + 策略 + 本包实际用的那套编码参数」，
// 而派生本身只看这三样。所以同样的 ref 加同样的 policy，跨进程、跨部署都得到同一份字节。
func (s *Store) ReadImageRequest(
	ctx context.Context, ref attachment.ImageRef, policy attachment.RequestPolicy,
) (attachment.RequestImage, error) {
	if err := ctx.Err(); err != nil {
		return attachment.RequestImage{}, err
	}
	// 源: packages/attachment/attachment-local/src/request-image.ts:49-52
	if policy.MaxPixels <= 0 || policy.MaxBytes <= 0 {
		return attachment.RequestImage{}, &attachment.Error{
			Code:    attachment.CodeInvalidAttachmentRef,
			Message: "Image request policy must carry positive pixel and byte budgets.",
		}
	}
	stored, err := s.ReadImage(ctx, ref)
	if err != nil {
		return attachment.RequestImage{}, err
	}
	variant := requestImageVariantID(ref, policy)
	width, height := attachment.RequestImageDimensions(ref.Width, ref.Height, policy.MaxPixels)

	version, cached := s.cachedRequestImage(ctx, variant, width, height, policy.MaxBytes)
	if !cached {
		version, err = projectRequestImage(stored, policy, width, height)
		if err != nil {
			return attachment.RequestImage{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return attachment.RequestImage{}, err
	}
	// 超预算那一份不入缓存：读回来的时候要给 ReadBytes 一个上限，而在那一侧
	// 唯一能自证的上限就是预算本身，存一份注定读不回来的字节没有意义。
	if !cached && !version.verbatim && len(version.data) <= policy.MaxBytes {
		s.cacheRequestImage(ctx, variant, version.data)
	}
	return attachment.RequestImage{
		VariantID:  variant,
		Attachment: ref,
		Data:       version.data,
		MediaType:  version.mediaType,
		Bytes:      len(version.data),
		Width:      version.width,
		Height:     version.height,
		Depth:      attachment.DepthUChar,
		Space:      attachment.SpaceSRGB,
		HasAlpha:   version.hasAlpha,
	}, nil
}

// projectRequestImage 派生一份请求版本：先试那条快路，走不通才落到阶梯上。
//
// 源: packages/attachment/attachment-local/src/request-image.ts:92-113
func projectRequestImage(
	stored attachment.StoredImage, policy attachment.RequestPolicy, width, height int,
) (requestVersion, error) {
	found, err := probe(stored.Data)
	if err != nil {
		return requestVersion{}, err
	}
	// 快路：投影出来的尺寸就是原尺寸、字节又在预算内，那就没有任何理由重新编码——
	// 重编只会掉画质。绝大多数截图和商品图走的都是这一条。
	if width == stored.Ref.Width && height == stored.Ref.Height && len(stored.Data) <= policy.MaxBytes {
		return requestVersion{
			data:      stored.Data,
			mediaType: found.mediaType,
			width:     width,
			height:    height,
			hasAlpha:  found.hasAlpha,
			verbatim:  true,
		}, nil
	}
	source, _, err := image.Decode(bytes.NewReader(stored.Data))
	if err != nil {
		return requestVersion{}, &attachment.Error{
			Code:    attachment.CodeInvalidImage,
			Message: "Unsupported or malformed image data.",
			Err:     err,
		}
	}
	return encodeWithinLimit(source, found.hasAlpha, policy, width, height)
}

// encodeWithinLimit 按次序试阶梯上的候选，第一个落进预算的就是结果；
// 全都超了就交出其中最小的那一份。
//
// 源: packages/attachment/attachment-local/src/encoding.ts:56-72
func encodeWithinLimit(
	source image.Image, hasAlpha bool, policy attachment.RequestPolicy, width, height int,
) (requestVersion, error) {
	var smallest requestVersion
	// pick 记下迄今最小的那一份，并回答这一份够不够小。
	pick := func(candidate requestVersion) bool {
		if smallest.data == nil || len(candidate.data) < len(smallest.data) {
			smallest = candidate
		}
		return len(candidate.data) <= policy.MaxBytes
	}
	if hasAlpha {
		for _, divisor := range alphaPixelDivisors {
			stepWidth, stepHeight := attachment.RequestImageDimensions(width, height, max(1, policy.MaxPixels/divisor))
			data, err := encodePNG(resize(source, stepWidth, stepHeight))
			if err != nil {
				return requestVersion{}, err
			}
			candidate := requestVersion{
				data:      data,
				mediaType: attachment.MediaTypePNG,
				width:     stepWidth,
				height:    stepHeight,
				hasAlpha:  true,
			}
			if pick(candidate) {
				return candidate, nil
			}
		}
		return smallest, nil
	}
	sized := resize(source, width, height)
	for _, quality := range encodingQualities {
		data, err := encodeJPEG(sized, quality)
		if err != nil {
			return requestVersion{}, err
		}
		candidate := requestVersion{data: data, mediaType: attachment.MediaTypeJPEG, width: width, height: height}
		if pick(candidate) {
			return candidate, nil
		}
	}
	return smallest, nil
}

// resize 把栅格缩到给定尺寸；尺寸没变就原样交回，不白走一遍重采样。
//
// 源: packages/attachment/attachment-local/src/request-image.ts:83-90
//
// 新增: sharp 那边 `fit: 'inside'` 只给上界、由它自己算最终尺寸；这里尺寸是
// [attachment.RequestImageDimensions] 先算好的，所以缩放是精确到像素的一步。
// 重采样核取 CatmullRom：缩图这件事上它比双线性锐，而且不像 Lanczos 那样在
// 高对比边上留下振铃——商品图的边缘正是这一类。
func resize(source image.Image, width, height int) image.Image {
	bounds := source.Bounds()
	if bounds.Dx() == width && bounds.Dy() == height {
		return source
	}
	target := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(target, target.Bounds(), source, bounds, draw.Src, nil)
	return target
}

func encodeJPEG(raster image.Image, quality int) ([]byte, error) {
	buffer := &bytes.Buffer{}
	if err := jpeg.Encode(buffer, raster, &jpeg.Options{Quality: quality}); err != nil {
		return nil, encodeFailed(err)
	}
	return buffer.Bytes(), nil
}

func encodePNG(raster image.Image) ([]byte, error) {
	buffer := &bytes.Buffer{}
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	if err := encoder.Encode(buffer, raster); err != nil {
		return nil, encodeFailed(err)
	}
	return buffer.Bytes(), nil
}

// requestImageVariantID 是「一个附件 + 一条路由自己的策略」那份完整的确定性标识。
//
// 源: packages/attachment/attachment-local/src/request-image.ts:54-81
//
// 新增: 描述符里的 encoding 那一段写的是**本包实际用的**那套参数，不是
// attachment-local 的那套（见 [alphaPixelDivisors]）。这一条是必须的：
// 标识的全部意义就是「同样的键指向同样的字节」，照抄一段自己并没有执行的参数，
// 等于让两套不同的编码器共用一个键。
func requestImageVariantID(ref attachment.ImageRef, policy attachment.RequestPolicy) attachment.VariantID {
	descriptor, err := json.Marshal(variantDescriptor{
		TransformVersion:  transformVersion,
		AttachmentID:      string(ref.ID),
		RoutePixelBudget:  policy.MaxPixels,
		EncodedByteBudget: policy.MaxBytes,
		Encoding: variantEncoding{
			JPEGQualities:      encodingQualities,
			AlphaPixelDivisors: alphaPixelDivisors,
			Order:              []string{"alpha:png", "opaque:jpeg"},
			Colourspace:        "srgb",
		},
	})
	if err != nil {
		// 这个结构里只有字符串、整数和整数切片，marshal 不可能失败。
		panic("imagestore: 变体描述符编不成 JSON：" + err.Error())
	}
	sum := sha256.Sum256(descriptor)
	return attachment.VariantID(idPrefix + hex.EncodeToString(sum[:]))
}

// variantDescriptor 是被摘要的那份描述符。字段次序就是 JSON 里的次序，
// 而摘要认字节——所以调换字段等于换掉全部已有的键。
type variantDescriptor struct {
	TransformVersion  string          `json:"transformVersion"`
	AttachmentID      string          `json:"attachmentId"`
	RoutePixelBudget  int             `json:"routePixelBudget"`
	EncodedByteBudget int             `json:"encodedByteBudget"`
	Encoding          variantEncoding `json:"encoding"`
}

// variantEncoding 是本包那套固定的编码参数。
type variantEncoding struct {
	JPEGQualities      []int    `json:"jpegQualities"`
	AlphaPixelDivisors []int    `json:"alphaPixelDivisors"`
	Order              []string `json:"order"`
	Colourspace        string   `json:"colourspace"`
}

// cachedRequestImage 取缓存里那一份；没有、读不回来、或者对不上尺寸上界，都算没有。
//
// 源: packages/attachment/attachment-local/src/request-image.ts:119-139
//
// 新增: attachment-local 另外还核对位深、色彩空间和 alpha 兼容性，靠的是 sharp
// 报的解码元数据。这边判据收在尺寸上界这一条：键已经覆盖了附件、策略和编码参数，
// 所以一份键对得上、尺寸又在界内的字节，只可能是本包自己写下的那一份。
func (s *Store) cachedRequestImage(
	ctx context.Context, variant attachment.VariantID, width, height, maxBytes int,
) (requestVersion, bool) {
	target, err := s.fs.Resolve(ctx, s.requestPath(variant), "")
	if err != nil {
		return requestVersion{}, false
	}
	data, err := s.fs.ReadBytes(ctx, target, int64(maxBytes))
	if err != nil {
		return requestVersion{}, false
	}
	found, err := probe(data)
	if err != nil || found.width > width || found.height > height {
		return requestVersion{}, false
	}
	return requestVersion{
		data:      data,
		mediaType: found.mediaType,
		width:     found.width,
		height:    found.height,
		hasAlpha:  found.hasAlpha,
	}, true
}

// cacheRequestImage 把派生结果写下来，写不进去就算了。
//
// 源: packages/attachment/attachment-local/src/request-image.ts:157-166
//
// 新增: attachment-local 那边写失败会把整次请求带崩。这里不：这一份是**缓存**，
// 源字节和引用都还在对象树里，写不进去的后果只是下一次再算一遍。
// 为一个缓存写把一次本来已经成功的模型请求打掉，代价和收益不成比例。
func (s *Store) cacheRequestImage(ctx context.Context, variant attachment.VariantID, data []byte) {
	if _, err := s.fs.MakeDir(ctx, s.requestBucketPath(variant), ""); err != nil {
		return
	}
	target, err := s.fs.Resolve(ctx, s.requestPath(variant), "")
	if err != nil {
		return
	}
	// 撞上就是别的进程已经写过同一个键——内容寻址下那份和这份必然一样。
	_, _ = s.fs.WriteBytes(ctx, target, data, fs.CreateIfAbsent{})
}

// requestBucketPath 是这个变体落到的那一层。
//
// 源: packages/attachment/attachment-local/src/request-image.ts:115-117
func (s *Store) requestBucketPath(variant attachment.VariantID) string {
	hash := strings.TrimPrefix(string(variant), idPrefix)
	return s.root + "/request-images/" + hash[:2]
}

// requestPath 是这个变体那一份字节的路径。
func (s *Store) requestPath(variant attachment.VariantID) string {
	hash := strings.TrimPrefix(string(variant), idPrefix)
	return s.requestBucketPath(variant) + "/" + hash
}

func encodeFailed(err error) error {
	return &attachment.Error{
		Code:    attachment.CodeAttachmentWriteFailed,
		Message: "Unable to encode the model-request image.",
		Err:     err,
	}
}
