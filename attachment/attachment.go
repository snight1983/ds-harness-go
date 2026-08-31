// Package attachment 是「持久附件存储」这件事的接缝：把一批图片校验、提交，
// 换回一组不可变的引用；以后再按引用把字节读回来。
//
// 源: packages/attachment/attachment/src/index.ts:1-2
//
// # 这个包不存字节
//
// 它只定义词汇和**次序规则**，真正的落盘由实现方在别的包里做。这么分的理由是
// 「一批图怎么排队校验和提交」和「字节存在磁盘还是对象存储」是两件独立变化的事，
// 而前者一旦写错，代价是脏数据；把它留在接缝上，所有实现方就只能是对的。
//
// # 抽象类怎么变成了接口加几个函数
//
// 新增: DSH 那边是一个抽象类：imageLimits / validateImage / saveImage / readImage
// 四个抽象成员，validateImageBatch / saveImages / readImageRequest 三个已经写好的方法，
// 子类继承就自动拿到后三个。Go 没有实现继承，接口里也放不了方法体。
//
// 对应做法是把两半拆开：抽象的那四个是 [Store] 接口，已经写好的那三个是包级函数
// （[ValidateImageBatch]、[SaveImages]、[ReadImageRequest]），它们接一个 Store 参数。
// 换来的东西和继承一样——次序规则只有一份、实现方绕不过去——而且比继承更硬：
// 继承来的方法子类能覆盖掉，包级函数不能。
//
// # 一批图的次序规则
//
// 源: packages/attachment/attachment/src/index.ts:53-60
//
// [SaveImages] 的次序是**先把整批都校验完，再开始写第一张**。这不是风格问题：
// 一批图属于同一条消息，操作者要么整条消息发出去、要么整条不发。边校验边写的话，
// 第三张不合格时前两张已经落盘了，而那两张永远不会被任何消息引用——
// 存储里就多了两块没人认领又不敢删的字节。
//
// 反过来，**写**这一段没有事务：写到第三张失败时，前两张确实已经在存储里了。
// 这一层不假装能回滚，它保证的是**一个部分成功的批次不会返回任何引用**——
// 没有引用就没有人能指向它们，那些对象等以后的保留策略来收。
//
// # 派生请求图是可选能力
//
// 源: packages/attachment/attachment/src/index.ts:110-129
//
// 「把存下来的图按某条模型路由的预算重新编码一份」需要一个图像处理库，
// 不是每个实现方都装得起（也不是每个部署都需要）。DSH 的做法是在基类上放一个
// 默认实现，无条件抛 ATTACHMENT_PROJECTION_UNSUPPORTED，能做的子类去覆盖它。
//
// Go 这边对应成一个可选能力接口 [RequestImageProjector] 加一个包级函数
// [ReadImageRequest]：实现方满足这个接口就走它，不满足就得到同一个码。
// 对调用方来说行为完全一样，而实现方不必为了「我不支持」去写一个只会报错的方法。
// 这条路子在本仓库已有先例，见 dirpicker 包里按形态取能力的写法。
//
// # 关于 context
//
// 新增: DSH 只给 readImage / readImageRequest 配了 AbortSignal，validateImage 和
// saveImage 没有。Go 这边四个方法都收 context.Context。
//
// 理由是两边的代价不一样：Node 里一次没法取消的写入只是占着 libuv 的一个槽；
// Go 里它占着调用方自己的 goroutine，而且**取消能力在 Go 里是传染的**——
// 接口方法上没有 ctx，实现方内部再想把取消传给 HTTP 客户端或数据库驱动就没有来源，
// 这个缺口补不回来。传一个上传到一半的大图取消不掉，正是这个接缝最容易出的问题。
package attachment

import (
	"context"
	"slices"
)

// Store 是不可变二进制附件服务。实现方必须**先验字节、再发引用**：
// 一个已经发出去的 [ImageRef] 就是一句承诺，说这些字节存在、并且确实是它描述的样子。
//
// 源: packages/attachment/attachment/src/index.ts:36-131
//
// 这个接口里只有 DSH 那边**抽象**的那四个成员。已经写好的那三个是包级函数，
// 理由见包头「抽象类怎么变成了接口加几个函数」。
type Store interface {
	// ImageLimits 返回部署方定下的图片限额，权威校验和快路径校验都用它。
	//
	// 源: packages/attachment/attachment/src/index.ts:42-43
	//
	// 同一个 Store 上多次调用应当给出同一组限额：[SaveImages] 会先按它判整批，
	// 中途变化会让「判过了」和「真的写」按两套规矩走。
	ImageLimits() ImageLimits

	// ValidateImage 校验一张图但不落盘。
	//
	// 源: packages/attachment/attachment/src/index.ts:45-51
	//
	// 它必须把编码后的栅格**完整解码**——单张字节数、像素数、边长、以及
	// 「声称的媒体类型和真实格式对不对得上」这几条，都只有解码之后才知道，
	// 而它们正是 [ValidateImageBatch] 在接缝上判不了的那几条。
	ValidateImage(ctx context.Context, input ImageInput) error

	// SaveImage 校验并持久提交一张图，返回内容寻址的归一化引用。
	//
	// 源: packages/attachment/attachment/src/index.ts:91-99
	//
	// 返回的引用描述的是**存下来的那一张**。归一化如果缩小了栅格，
	// 就把应用旋转之后、缩放之前的尺寸记在 [ImageRef.OriginalDimensions] 里。
	SaveImage(ctx context.Context, input ImageInput) (ImageRef, error)

	// ReadImage 读回一张图，并核对字节仍然和引用记录的一致。
	//
	// 源: packages/attachment/attachment/src/index.ts:101-108
	//
	// 核对这一步不能省：附件是内容寻址的，字节和引用对不上就说明存储被改过或者坏了，
	// 这种情况必须报 [CodeAttachmentCorrupt]，而不是把不对的字节交出去。
	ReadImage(ctx context.Context, ref ImageRef) (StoredImage, error)
}

// RequestImageProjector 是可选能力：从存下来的归一化图派生出一份确定性的模型请求版本。
//
// 源: packages/attachment/attachment/src/index.ts:110-129
//
// 实现方满足它就有这个能力，不满足 [ReadImageRequest] 会报
// [CodeAttachmentProjectionUnsupported]。理由见包头「派生请求图是可选能力」。
type RequestImageProjector interface {
	// ReadImageRequest 按 policy 生成或读取一份请求版本。
	//
	// 它必须是**确定性**的：同样的 ref 加同样的 policy，必须得到同样的
	// [RequestImage.VariantID] 和同样的字节，否则缓存和上传索引会各自为政。
	ReadImageRequest(ctx context.Context, ref ImageRef, policy RequestPolicy) (RequestImage, error)
}

// ValidateImageBatch 判一批图里**光看输入就能判**的那三条：张数、字节总和、媒体类型。
//
// 源: packages/attachment/attachment/src/index.ts:53-75
//
// 三条的次序是有意的，也是被测试钉住的：先张数、再字节总和、最后媒体类型。
// 一批图同时犯多条时，报出去的是最外层那条——「你传得太多了」比
// 「第七张的格式不对」更接近操作者要做的下一个动作。
//
// 剩下的三条限额（单张字节、像素数、边长）不在这里判：它们要把栅格解码出来才知道，
// 而用哪个解码库是实现方的选择，见 [Store.ValidateImage]。
//
// DSH 那边这是一个 protected 方法，好让子类自己也能调。Go 里它是导出的包级函数，
// 达到同样的目的：别的包里的实现方照样调得到。
func ValidateImageBatch(limits ImageLimits, inputs []ImageInput) error {
	if len(inputs) > limits.MaxImagesPerMessage {
		return &Error{
			Code:    CodeTooManyImages,
			Message: "Image batch exceeds the configured image-count limit.",
		}
	}
	totalBytes := 0
	for _, input := range inputs {
		totalBytes += len(input.Data)
	}
	if totalBytes > limits.MaxMessageImageBytes {
		return &Error{
			Code:    CodeImagesTooLarge,
			Message: "Image batch exceeds the configured aggregate image-byte limit.",
		}
	}
	for _, input := range inputs {
		if !slices.Contains(limits.MediaTypes, input.MediaType) {
			return &Error{
				Code:    CodeUnsupportedImageType,
				Message: "Image type " + string(input.MediaType) + " is not accepted by this deployment.",
			}
		}
	}
	return nil
}

// SaveImages 校验并持久提交一批有序的图，按**输入顺序**返回引用。
//
// 源: packages/attachment/attachment/src/index.ts:77-89
//
// 三段次序（整批规则 → 逐张校验 → 逐张提交）以及「为什么校验要全做完才开始写」，
// 见包头「一批图的次序规则」。
//
// 任何一步失败都返回 nil 引用切片，不返回已经成功的那几张——见包头对部分成功的说明。
func SaveImages(ctx context.Context, store Store, inputs []ImageInput) ([]ImageRef, error) {
	if err := ValidateImageBatch(store.ImageLimits(), inputs); err != nil {
		return nil, err
	}
	for _, input := range inputs {
		if err := store.ValidateImage(ctx, input); err != nil {
			return nil, err
		}
	}
	// 返回的切片非 nil，即使一张图都没有。空批次是合法输入（一条不带图的消息），
	// 它得到的是一个空列表而不是「没有列表」，调用方不必为这一种情况多写一条分支。
	refs := make([]ImageRef, 0, len(inputs))
	for _, input := range inputs {
		ref, err := store.SaveImage(ctx, input)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// ReadImageRequest 生成或读取一份确定性的模型请求版本。
//
// 源: packages/attachment/attachment/src/index.ts:110-129
//
// store 不具备 [RequestImageProjector] 时报 [CodeAttachmentProjectionUnsupported]。
//
// 取消**优先于**「不支持」：ctx 已经结束时返回 ctx 的错误，而不是那个码。
// 这个次序是 DSH 明确摆出来的（signal?.throwIfAborted() 写在抛错之前，且有测试钉住），
// 道理是调用方已经走了的时候，「这个实现支持不支持」根本不是它要知道的事——
// 报「不支持」会让日志里出现一条其实从没被需要过的能力缺口。
func ReadImageRequest(
	ctx context.Context, store Store, ref ImageRef, policy RequestPolicy,
) (RequestImage, error) {
	if err := ctx.Err(); err != nil {
		return RequestImage{}, err
	}
	projector, ok := store.(RequestImageProjector)
	if !ok {
		return RequestImage{}, &Error{
			Code:    CodeAttachmentProjectionUnsupported,
			Message: "The mounted attachment provider cannot derive model-request images.",
		}
	}
	return projector.ReadImageRequest(ctx, ref, policy)
}
