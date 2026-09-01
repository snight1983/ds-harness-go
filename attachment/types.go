// 本文件是附件这件事的词汇表：标识、媒体类型、以及在接缝上来回传的那几个值。
//
// 源: packages/attachment/attachment/src/types.ts
// 源: packages/attachment/attachment/src/brand.ts

package attachment

// ID 是一个不可变附件对象的不透明标识。
//
// 源: packages/attachment/attachment/src/brand.ts:5-15
//
// 新增: DSH 用 Branded<'AttachmentId'> 这个类型技巧，配一个恒等转换的构造函数
// AttachmentId(value)。Go 的具名类型天生就是标称类型——ID 和 VariantID 编译期
// 不可互换、运行期零成本，正是那个技巧在模拟的东西，所以两样都不需要：
// 类型就是 type ID string，构造就是 attachment.ID(value) 这个语言自带的转换。
//
// 它是**不透明**的：绝不是文件系统路径，也绝不是带凭证的 URL。这条约束是安全边——
// 附件标识会随会话日志一起落盘、并被送到客户端，一旦它是路径或带签名的 URL，
// 一份历史记录就等于一把长期有效的钥匙。
type ID string

// VariantID 是一次「请求版本」变换的确定性标识。
//
// 源: packages/attachment/attachment/src/brand.ts:17-27
//
// 它覆盖的是「附件 + 策略 + 固定编码参数」这一整组输入，所以同样的输入必然得到
// 同样的 VariantID——缓存和上传索引都靠这一点去重。
type VariantID string

// MediaType 是第一版附件通道接受的栅格图格式。
//
// 源: packages/attachment/attachment/src/types.ts:7-8
//
// 它是**线上可见**的字符串，原样跟着请求和引用走，所以取值照抄 DSH，不做本地化。
// 这里不设「是不是合法媒体类型」的全局校验：DSH 那边这是一个 TS 联合类型，
// 只在编译期成立；真正的准入判据是部署自己配的 [ImageLimits.MediaTypes]，
// 见 [ValidateImageBatch]。
type MediaType string

const (
	// MediaTypePNG 是 image/png。
	MediaTypePNG MediaType = "image/png"
	// MediaTypeJPEG 是 image/jpeg。
	MediaTypeJPEG MediaType = "image/jpeg"
	// MediaTypeWebP 是 image/webp。
	MediaTypeWebP MediaType = "image/webp"
	// MediaTypeGIF 是 image/gif。
	MediaTypeGIF MediaType = "image/gif"
)

// Dimensions 是一对像素尺寸。
//
// 源: packages/attachment/attachment/src/types.ts:28-31
type Dimensions struct {
	// Width 是宽，单位像素。
	Width int `json:"width"`
	// Height 是高，单位像素。
	Height int `json:"height"`
}

// ImageRef 是对一张不可变的、已归一化的图片的持久引用，可以序列化。
//
// 源: packages/attachment/attachment/src/types.ts:10-32
//
// 它是**会被写进会话日志、并送到客户端**的那个东西。所以它只带描述性的事实，
// 不带任何能直接取到字节的东西——要拿字节必须回到 [Store] 上按 ID 去读。
//
// 新增: 本包其余的类型都不带 json 标签，只有它带。理由是只有它真的会被序列化：
// 它嵌在模型内容块里跟着会话日志一起落盘。字段名照 DSH 的写——那边这是一个
// TS 接口，JSON.stringify 直接就是这些名字，改一个字都会让两侧的日志读不通。
type ImageRef struct {
	// ID 是不透明的存储标识；绝不是文件系统路径或带凭证的 URL。
	ID ID `json:"attachmentId"`
	// MediaType 是从**已存储的字节**里验出来的媒体类型，不是调用方声称的那个。
	MediaType MediaType `json:"mediaType"`
	// Bytes 是精确的编码后字节数。
	Bytes int `json:"bytes"`
	// Width 是编码内在宽度，单位像素。
	Width int `json:"width"`
	// Height 是编码内在高度，单位像素。
	Height int `json:"height"`
	// Name 是可选的显示名，已经剥掉本地路径信息。空串表示没有。
	//
	// 新增: DSH 用 name?: string，即「有这个字段」和「没有这个字段」两种状态。
	// Go 里空串就是「没有」：一个空的显示名和没有显示名对界面来说是同一件事，
	// 为此多加一层指针只会让每个调用点都要判 nil。
	//
	// 它**绝不会被当成路径解释**——它来自浏览器或上游提供方，是不可信输入。
	Name string `json:"name,omitempty"`
	// OriginalDimensions 是应用 EXIF 旋转之后、归一化缩放之前的输入尺寸。
	// 只有在归一化确实缩小了这张图时才非 nil。
	//
	// 新增: 这里用指针而不是零值，因为「没有缩小过」和「缩小成了 0×0」必须分得开——
	// 前者是常态，后者是坏数据，用零值表示会让两者长得一模一样。
	OriginalDimensions *Dimensions `json:"originalDimensions,omitempty"`
}

// ImageLimits 是部署方定下的图片限额，上传准入和请求缓冲都按它判。
//
// 源: packages/attachment/attachment/src/types.ts:34-43
//
// 这几条里只有 MaxImagesPerMessage / MaxMessageImageBytes / MediaTypes 由
// [ValidateImageBatch] 在接缝上判——它们光看输入就能判。其余三条要把栅格真解码出来
// 才知道，属于 [Store.ValidateImage] 的事，因为解码用哪个库是实现方的选择。
type ImageLimits struct {
	// MaxImageBytes 是单张图的编码字节上限。
	MaxImageBytes int
	// MaxImagesPerMessage 是一条消息最多带几张图。
	MaxImagesPerMessage int
	// MaxMessageImageBytes 是一条消息里所有图加起来的字节上限。
	MaxMessageImageBytes int
	// MaxImagePixels 是单张图宽乘高的上限。
	MaxImagePixels int
	// MaxImageDimension 是单张图的最大内在宽度和最大内在高度，单位像素。
	MaxImageDimension int
	// MediaTypes 是这个部署接受的媒体类型。空表示一张图都不收。
	MediaTypes []MediaType
}

// EncodedImage 是跟着一次线上请求送进来的、base64 编码的图片上传。
//
// 源: packages/attachment/attachment/src/types.ts:45-53
type EncodedImage struct {
	// MediaType 是调用方声称的媒体类型，准入时会拿解码后的字节去核对。
	MediaType MediaType
	// Data 是图片字节的规范 base64 编码。
	Data string
	// Name 是可选的显示名；它绝不会被当成路径解释。空串表示没有。
	Name string
}

// ImageInput 是「校验并持久提交一张图」这个请求。
//
// 源: packages/attachment/attachment/src/types.ts:75-82（SaveImageAttachment）
//
// 它和 [EncodedImage] 的区别是字节已经解码出来了：base64 那一层由
// [AdmitEncodedImages] 在最外面剥掉，[Store] 只跟原始字节打交道。
type ImageInput struct {
	// Data 是图片的原始字节。
	Data []byte
	// MediaType 是调用方声称的媒体类型，会拿完整解码后的字节去核对。
	MediaType MediaType
	// Name 是可选的浏览器／提供方显示名；它绝不会被当成路径解释。空串表示没有。
	Name string
}

// StoredImage 是引用和摘要都核对过之后返回的存储字节。
//
// 源: packages/attachment/attachment/src/types.ts:84-88（StoredImageAttachment）
type StoredImage struct {
	// Ref 是这些字节对应的那个持久引用。
	Ref ImageRef
	// Data 是核对过的图片字节。
	Data []byte
}

// RequestPolicy 是某一条具体的模型路由选定的请求图策略。
//
// 源: packages/attachment/attachment/src/types.ts:90-96（ImageRequestPolicy）
//
// 它是确定性的：同样的策略作用在同一张存储图上，必须得到同样的 [VariantID] 和字节。
type RequestPolicy struct {
	// MaxPixels 是保持长宽比投影之后，宽乘高的上限。
	MaxPixels int
	// MaxBytes 是编码字节上限，在 base64 膨胀或者 Files API 上传之前算。
	MaxBytes int
}

// Depth 是请求编码之后证明出来的采样位深。
//
// 源: packages/attachment/attachment/src/types.ts:90-91
type Depth string

// DepthUChar 是当前唯一合法的位深。
//
// 新增: DSH 把它写成只有一个成员的字面量联合类型 'uchar'。Go 里用具名类型加一个常量
// 对应：它同样表达「这个字段只有一个合法值」，而且以后真要加第二种编码时，
// 加的是一个常量，不是把字段类型从 string 收窄——后者是破坏性变更。
const DepthUChar Depth = "uchar"

// Space 是请求编码之后证明出来的色彩空间。
//
// 源: packages/attachment/attachment/src/types.ts:92-93
type Space string

// SpaceSRGB 是当前唯一合法的色彩空间。理由同 [DepthUChar]。
const SpaceSRGB Space = "srgb"

// RequestImage 是从一张与提供方无关的归一化附件派生出来的、可缓存的请求版本。
//
// 源: packages/attachment/attachment/src/types.ts:98-116（RequestImageAttachment）
//
// 它和 [ImageRef] 是两层东西：ImageRef 是**存下来的那一张**，与任何模型提供方无关；
// RequestImage 是为**某一条具体路由**按它的像素和字节预算重新编码出来的那一份。
// 分开的理由是同一张图会被送给多个提供方，每个提供方的预算不一样，
// 而重新编码是有成本的——VariantID 就是用来跨请求复用这份成本的。
type RequestImage struct {
	// VariantID 是缓存和上传索引的键，覆盖附件标识、策略、以及固定的编码参数。
	VariantID VariantID
	// Attachment 是这份请求版本所派生自的那个持久归一化附件。
	Attachment ImageRef
	// Data 是编码后的请求字节。
	Data []byte
	// MediaType 是这份请求版本的媒体类型。
	MediaType MediaType
	// Bytes 是这份请求版本的编码字节数。
	Bytes int
	// Width 是这份请求版本的宽，单位像素。
	Width int
	// Height 是这份请求版本的高，单位像素。
	Height int
	// Depth 是请求编码之后证明出来的、提供方兼容的采样位深。
	Depth Depth
	// Space 是请求编码之后证明出来的、提供方兼容的色彩空间。
	Space Space
	// HasAlpha 表示这份编码后的请求版本是否保留了 alpha 通道。
	HasAlpha bool
}
