// Package imagestore 把 [github.com/snight1983/ds-harness-go/attachment.Store]
// 这条接缝实现在 [github.com/snight1983/ds-harness-go/fs.FileSystem] 上：
// 内容寻址、去重、读回时逐字节核对。
//
// 新增: DSH 那边挂在这条缝上的是 attachment-local
// （packages/attachment/attachment-local）。它整包在本仓库的裁决里是范围外——
// 它建在 `node:path` 加 `DSH_HOME` 上，而这台服务没有硬盘、也没有目录这个概念。
// 但那只是**减法**：接缝仍然是空的，装配方无从装配。这个包补上加法，
// 做法和 [github.com/snight1983/ds-harness-go/adapter/objectstore] 那次一样——
// 换介质，不换语义。
//
// 所以本包的 `// 源:` 注释指向的是那个范围外的包：**内容寻址这套办法是从它来的**，
// 换掉的只是它落到哪儿。分歧一律另起 `// 新增:` 写明。
//
// # 引用就是元数据，存储里不放第二份
//
// [github.com/snight1983/ds-harness-go/attachment.ImageRef] 会跟着会话日志一起落盘，
// 读的时候是调用方交回来的。所以存储里**只有字节**：`<Root>/objects/<sha[:2]>/<sha>`，
// 一张图一个对象，没有伴生的元数据文件。
//
// 读回时把字节重新摘要一遍、再把文件头重新认一遍，然后和交进来的引用逐项核对。
// 两份都对得上，这些字节就是当初存进去的那些。
//
// 存一份元数据文件反而更糟：那样存储里有两个对象，它们会不一致，而不一致的时候
// **没有任何办法知道哪一份是对的**。摘要没有这个问题——它自己就是判据。
// 这一条照 packages/attachment/attachment-local/src/store.ts:265-307 的读路径来。
//
// # 去重塌成一次条件写
//
// 内容寻址天然会撞：同一张图存两次，第二次的键和第一次一模一样。attachment-local
// 那边用「临时文件 + hard link + fsync 目录项」来抢这一下，因为它要在本机磁盘上
// 自己保证发布的原子性。
//
// 这条缝上不需要那一套：[github.com/snight1983/ds-harness-go/fs.CreateIfAbsent]
// 就是「不覆盖地发布」，由后端保证。撞上了后端报
// [github.com/snight1983/ds-harness-go/fs.CodeNotObserved]，那正是「已经有了」，
// 于是把已经在的那份读回来重算摘要：一致就直接发引用（这就是去重），
// 不一致说明存储里那份坏了或者被人动过，报 [attachment.CodeAttachmentCorrupt]。
//
// # 派生请求图：存的是原样，送的是按预算重编的那一份
//
// 存和送是两件事。存下来的是操作者传上来的那些字节（见下面「不做归一化缩放」），
// 而每条模型路由各有自己的像素和字节预算——[attachment.RequestPolicy]，
// openaicompat 那条缺省是 2048×2048 像素、1 MiB。两者对不上时要重新编码。
//
// 所以本包实现 [attachment.RequestImageProjector]，照
// packages/attachment/attachment-local/src/request-image.ts 的形状来：
//
//   - **先走快路**。[attachment.RequestImageDimensions] 算出来的尺寸就是原尺寸、
//     字节又在预算内，就把存储里那些字节原样交出去。重编只会掉画质，
//     而绝大多数截图和商品图本来就在预算里。
//   - **超了才上阶梯**。不带 alpha 的走 JPEG 的 85/75/60 三级质量，
//     和 attachment-local 同一组数；带 alpha 的走 PNG，把像素预算依次砍半——
//     Go 没有 WebP 编码器，而换 JPEG 会把抠好的商品图垫上一块黑底。
//     第一个落进预算的就是结果，全都超了就交最小的那一份。
//   - **结果按变体标识缓存**在 `<Root>/request-images/<sha[:2]>/<sha>`。
//     标识覆盖「附件 + 策略 + 本包实际用的那套编码参数」，所以它是确定性的；
//     没有这层缓存的话，历史里每张图会在每一轮重编一次。
//
// # 本轮画出来的两条边界
//
// 这两件事**不做**，是决定，不是遗漏：
//
//   - **不做归一化缩放**。attachment-local 会把大图按一份策略重新编码、缩到
//     预算之内再存。这里只在派生请求图时重编，存的那一份永远是原样，
//     [attachment.ImageRef.OriginalDimensions] 恒为 nil——按那个字段的定义，
//     「没有缩小过」正是它为 nil 的意思，所以这是合法值而不是缺口。
//   - **不认 EXIF 旋转**。attachment-local 报的宽高是**观感上**的宽高
//     （orientation 5-8 会把栅格转置）。这里没有 EXIF 解析器，报的是栅格自己的宽高。
//     受影响的是引用上记的那两个数、按它们判的边长限额，以及派生时投影出来的尺寸。
//
// # WebP 要多一个依赖
//
// 新增: [attachment.MediaTypeWebP] 在接缝上已经声明了，而 Go 标准库认不出 WebP。
// 认不出的话这个媒体类型就是假的：调用方声称 image/webp，这边解不出格式，
// 报的是「这不是一张图」——而它是。所以引 golang.org/x/image/webp，只为它的
// 文件头解析。png / jpeg / gif 三种走标准库。
//
// # 不做什么
//
//   - **不做归一化缩放。**存下来的就是操作者传上来的那些字节；重新编码只发生在
//     派生请求图那一步，改不到对象树里的任何一份。
//   - **不认 EXIF 旋转。**报的是栅格自己的宽高。
//   - **不做别的图像处理。**没有裁剪、旋转、加水印、抠图；重新编码只为把一张图
//     压进一条路由的预算，不为改变它是什么。
//   - **不决定模型收不收图。**那是模型路由那一层的事，本包只管字节的落与取。
//   - **不碰本机磁盘。**它建在 [github.com/snight1983/ds-harness-go/fs.FileSystem] 上，
//     服务端部署下面接的是
//     [github.com/snight1983/ds-harness-go/adapter/objectstore]。
//   - **不在存储里放第二份元数据。**引用跟着会话日志走；两个对象会不一致，而不一致
//     的时候没有办法知道哪一份是对的。
package imagestore
