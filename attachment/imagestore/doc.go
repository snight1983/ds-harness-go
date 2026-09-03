// Package imagestore 把 [github.com/snight1983/ds-harness-go/attachment.Store]
// 这条接缝实现在 [github.com/snight1983/ds-harness-go/fs.FileSystem] 上：
// 内容寻址、去重、读回时逐字节核对。
//
// 新增: DSH 那边挂在这条缝上的是 attachment-local
// （packages/attachment/attachment-local）。它整包在本仓库的裁决里是范围外——
// 它建在 `node:path` 加 `DSH_HOME` 上，而这台服务没有硬盘、也没有目录这个概念。
// 但那只是**减法**：接缝仍然是空的，装配方无从装配。这个包补上加法，
// 做法和 [github.com/snight1983/ds-harness-go/fs/objectstore] 那次一样——
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
// # 本轮画出来的三条边界
//
// 这三件事**不做**，是决定，不是遗漏：
//
//   - **不做归一化缩放**。attachment-local 会把大图按一份策略重新编码、缩到
//     预算之内再存。缩放要一个编码器（那边是 sharp/libvips），本轮不把它带进来。
//     后果是存下来的就是操作者传上来的那些字节，
//     [attachment.ImageRef.OriginalDimensions] 恒为 nil——按那个字段的定义，
//     「没有缩小过」正是它为 nil 的意思，所以这是合法值而不是缺口。
//   - **不实现 [attachment.RequestImageProjector]**。它是可选能力，同样要编码器。
//     不满足它的实现方，调用方会从
//     [attachment.ReadImageRequest] 拿到 [attachment.CodeAttachmentProjectionUnsupported]，
//     这条路接缝上本来就铺好了。
//   - **不认 EXIF 旋转**。attachment-local 报的宽高是**观感上**的宽高
//     （orientation 5-8 会把栅格转置）。这里没有 EXIF 解析器，报的是栅格自己的宽高。
//     因为本包不重新编码，字节里那段 EXIF 原样留着，渲染方照旧转得过来；
//     受影响的只有引用上记的那两个数，以及按它们判的边长限额。
//
// # WebP 要多一个依赖
//
// 新增: [attachment.MediaTypeWebP] 在接缝上已经声明了，而 Go 标准库认不出 WebP。
// 认不出的话这个媒体类型就是假的：调用方声称 image/webp，这边解不出格式，
// 报的是「这不是一张图」——而它是。所以引 golang.org/x/image/webp，只为它的
// 文件头解析。png / jpeg / gif 三种走标准库。
package imagestore
