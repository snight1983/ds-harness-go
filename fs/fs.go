// Package fs 是**一个执行世界**的文件系统接缝：后端拥有稳定的目标标识、
// 子进程能打开的路径、file: URI、包含关系、文本读取与解码、二进制拒绝，
// 以及原子的写入。
//
// 源: packages/fs/fs/src/index.ts:1-9
//
// # 这条接缝划在哪里
//
// 读取窗口（只读文件的某几行）和「观察过的状态」这套策略**不在这里**，
// 它们在消费方和策略插件里。留在这条接缝上的只有后端才做得了的那些事。
//
// [FileSystem.EditText] 是个例外：它看上去像是策略（找一段字面文本、换掉），
// 但版本校验、字面匹配、重写这三步必须在**同一个临界区**里做完。
// 拆到上层去的话，三步之间任何一次外部写入都会让编辑落在一份已经不存在的内容上。
//
// # 两个标识都是不透明的
//
// [TargetKey] 和 [Version] 都是后端自己定的字符串，**消费方不许解析、不许推断**。
// 本地后端的 key 是 realpath 那样的东西，远端后端可能是工作区 URI 或者文件 id；
// 版本在本地是高精度 stat 身份加新鲜度字段，在远端可能是一个修订号。
// 需要一条真的能给子进程用的路径时，问 [OSPathFileSystem.ProcessPath]——
// 它和 [TargetKey] 故意是两样东西，而且**不是每个后端都有**（见那个接口）。
//
// # 守卫是可选的，原子性不是
//
// 源: packages/fs/fs/README.md:37
//
// [FileSystem.WriteText] 和 [FileSystem.EditText] 的 expected 都可以省略。
// 省掉的是**版本前置条件**，不是原子性：一次无守卫的写照样是原子发布的，
// 它只是不再要求「你写之前看过的还是现在这一份」。
// 这一点必须写明，否则「无条件写」很容易被读成「随便写，写坏了算了」。
//
// # 抽象类怎么变成了接口加一个策略通道
//
// 新增: DSH 那边是 `abstract class FileSystem extends Service`，十二个抽象方法；
// 另外三件事（fs/write-intent、fs/edit-intent、fs/observed）是它在 cordis 上
// **声明**的事件，由容器分发。Go 没有那个容器，所以拆成两半：
// 那些方法落在接口上，三个事件落在 [Policy] 这个具体类型上。
//
// 十二个方法这边又分了一次：十个是每个后端都做得到的，留在 [FileSystem]；
// processPath 和 fileUrl 两个只有「目标在操作系统里也有名字」的后端做得到，
// 挪进可选的 [OSPathFileSystem]。理由写在那个接口上。
//
// 事件那一半不做成接口，理由和 credentials 包里的 [Notifier] 一样：
// 它背后是一张活着的订阅表，那是**状态**，而接口存不下状态。
//
// # 沙箱那一面没有搬
//
// DSH 的 `FileSystem.sandboxMode` 以及 WriteText / EditText 上的 sandboxPolicy
// 参数在这里都没有。sandbox 整支在本仓库的裁决里是范围外，
// 而一个恒返回「不限制」的能力位，比没有这个位更容易让人误以为它在起作用。
//
// # 关于 context
//
// 新增: DSH 的方法收 `signal?: AbortSignal`，Go 这边一律收 context.Context。
// 理由和 attachment、credentials 两个包记的那条一样：取消能力在 Go 里是传染的，
// 接口方法上没有 ctx，实现方内部就没办法把取消传给它的 HTTP 客户端或者
// 文件读取循环。一次连不上远端工作区的 Resolve 会把调用方的 goroutine 一直占着。
//
// 不收 ctx 的三个（[OSPathFileSystem.ProcessPath]、[OSPathFileSystem.FileURL]、
// [FileSystem.Contains]）在 DSH 那边也是同步方法：它们只对一个**已经解析好**的
// 目标做纯计算，没有 I/O 可取消。
package fs

import (
	"context"
	"iter"
)

// FileSystem 是文件系统提供方**必须**实现的那十个原语。
//
// 另外两个（进程路径、file: URI）不是每个后端都有，它们在可选的
// [OSPathFileSystem] 上。
//
// 源: packages/fs/fs/src/index.ts:80-250
//
// 三条贯穿全部实现的约定：
//
//   - **目标标识跨别名保持不变**：同一个文件无论从哪条路径走到，
//     [FileSystem.Resolve] 给出的 [Target.TargetKey] 必须相同。
//     否则陈旧守卫可以被一条软链接绕过去。
//   - **读要么给出常规 UTF-8 文本，要么给出一个带类型的失败**，
//     不许把二进制当文本交出去（见 [CodeNotText]）。
//   - **列目录是稳定顺序且不含内容**，写入是原子的。
type FileSystem interface {
	// Resolve 把一条模型或插件给出的路径解析成一个稳定的 [Target]。
	//
	// 源: packages/fs/fs/src/index.ts:107-116
	//
	// 它**可以做 I/O**——远端或沙箱后端可能要来回一趟才能把路径映射成稳定身份，
	// 所以即使本地后端只做一次规范化加 realpath，这个方法也收 ctx。
	//
	// cwd 是相对路径的基准；留空表示用后端自己的默认基准。
	// 空串不是一条合法的工作目录，所以这个映射不丢信息。
	Resolve(ctx context.Context, path string, cwd string) (Target, error)

	// Contains 判断 child 是不是 parent 本身或者它的后代。
	//
	// 源: packages/fs/fs/src/index.ts:137-144
	//
	// 由后端回答而不是由调用方拿字符串前缀比，是因为只有后端知道自己的
	// [TargetKey] 是什么形状。两个目标都必须来自同一个提供方。
	Contains(parent Target, child Target) bool

	// Stat 给出目标的元数据；目标不存在时第二个返回值是 false。
	//
	// 源: packages/fs/fs/src/index.ts:146-152
	//
	// **只有元数据，永远没有内容**。策略层靠它在读之前就把目录和特殊文件挡掉，
	// 并且靠 [Info.Size] 在 ReadText 和 StreamText 之间选一个——
	// 而不是先按 ReadText 试一次、失败了再改口。
	Stat(ctx context.Context, target Target) (Info, bool, error)

	// Lstat 给出一条**路径**的元数据，最后一段是符号链接时不跟过去。
	//
	// 源: packages/fs/fs/src/index.ts:154-168
	//
	// 它是路径形状的而不是目标形状的，这一点是有意的：[FileSystem.Resolve]
	// 会跟着符号链接走出那个稳定身份，而 Lstat 让消费方在那次跟随**发生之前**
	// 就把这条路径本身拒掉——一条指向仓库外面的链接，跟过去之后就看不出来了。
	//
	// cwd 的规则同 [FileSystem.Resolve]。路径不存在时第二个返回值是 false。
	Lstat(ctx context.Context, path string, cwd string) (PathInfo, bool, error)

	// ReadText 把整个常规文本文件读成一个解码好的字符串。
	//
	// 源: packages/fs/fs/src/index.ts:170-176
	ReadText(ctx context.Context, target Target) (string, error)

	// StreamText 把整个常规文本文件按解码好的文本块流出来，语义同 [FileSystem.ReadText]。
	//
	// 源: packages/fs/fs/src/index.ts:178-187
	//
	// **跨块的 UTF-8 解码和二进制拒绝由后端负责**，所以策略层一个字节都不用碰。
	//
	// 新增: DSH 返回 `AsyncIterable<string>`。Go 这边是 iter.Seq2[string, error]，
	// 不是 io.Reader——io.Reader 交出去的是字节，而这条接缝的全部意义就在于
	// 上层永远不接触字节。错误跟在每一块后面，因为一次流式读取可以读了一半才失败，
	// 而那时候已经有内容交出去了，调用方必须能看见这件事。
	//
	// ctx 取消在**块与块之间**也生效：一个大文件读到一半被取消，
	// 迭代必须停下来，而不是把剩下的读完。
	StreamText(ctx context.Context, target Target) (iter.Seq2[string, error], error)

	// ReadBytes 把整个常规文件读成原始字节，不解码、不做二进制拒绝。
	//
	// 源: packages/fs/fs/src/index.ts:189-199
	//
	// **上限落在这条接缝上**，为的是后端永远不可能把一个无界大的文件缓冲进内存：
	// 已知或者读到一半发现超过 maxBytes 的目标，报 [CodeTooLarge]，
	// 而不是交出一份被截断的结果。截断的那份看上去是成功的，
	// 而它会被当成完整内容去算摘要、去做匹配。
	ReadBytes(ctx context.Context, target Target, maxBytes int64) ([]byte, error)

	// ListDir 按稳定的名字顺序列出一个目录的直接子项。
	//
	// 源: packages/fs/fs/src/index.ts:201-208
	//
	// 只给解析好的子目标加上廉价的元数据，**绝不读文件内容**。
	ListDir(ctx context.Context, target Target) ([]DirEntry, error)

	// WriteText 原子地创建或替换 UTF-8 文本。
	//
	// 源: packages/fs/fs/src/index.ts:210-228
	//
	// expected 为 nil 表示无条件的「创建或覆盖」——它去掉的是版本前置条件，
	// 不是原子性（见包文档）。
	WriteText(ctx context.Context, target Target, content string, expected WriteIntent) (WriteOutcome, error)

	// EditText 原子地做一次字面文本替换。
	//
	// 源: packages/fs/fs/src/index.ts:230-249
	//
	// 给了 expected 时，版本校验在**匹配之前**做，所以内容陈旧时报的是
	// [CodeStaleVersion] 而不是 [CodeEditNotFound]——后者会让调用方以为
	// 是自己的搜索串写错了，然后换个串再试一次，而它每一次都在改别人刚写下的内容。
	//
	// expected 为 nil 表示不带新鲜度前置条件地编辑当前内容。
	EditText(ctx context.Context, target Target, edit EditRequest, expected *EditIntent) (EditOutcome, error)
}

// OSPathFileSystem 是一个**可选**能力：这个后端上的目标在操作系统的文件命名空间里
// 也有一个名字。
//
// 源: packages/fs/fs/src/index.ts:118-135（processPath、fileUrl 两个抽象方法）
//
// 新增: DSH 把这两个方法和别的十个一起写在抽象类上，因为它那边每一个后端都架在
// 一份真的文件系统上。本仓库不是：唯一的生产后端 [github.com/snight1983/ds-harness-go/fs/objectstore.Store]
// 架在对象存储上，一个对象**没有**进程能打开的路径，也没有 file: URI。
//
// 强制它实现只剩三种写法，三种都是坏的：交回对象键或者 s3:// 串是一次静默的说谎，
// 调用方会把它交给一次 open() 然后在很远的地方失败；交回空串是同一个谎，只是失败
// 得更晚；panic 则把「这个后端没有这项能力」这件**静态**的事实，推迟到运行期才说，
// 而且是用一个能带走整个进程的方式说——这个包是嵌在长期运行的服务里跑的。
//
// 所以它单独成一道接缝，语义交给类型系统：做得到的后端实现它，做不到的不实现，
// 调用方类型断言，断言不过就是「这条路在这个部署上走不通」，一个 error 而不是一次
// 崩溃。同样的手法见 [github.com/snight1983/ds-harness-go/session/persistence.SeekableBackend]。
//
// 本仓库目前**没有**调用方：会用到它的消费方（起子进程、拼命令行）整支在裁决里
// 是范围外，见 README 的项目边界。留着这道接缝是为了宿主自己挂本地后端时有个
// 说得清的位置，不是为了本仓库自己用。
type OSPathFileSystem interface {
	// ProcessPath 给出这个执行世界里的子进程能打开的规范绝对路径。
	//
	// 源: packages/fs/fs/src/index.ts:118-126
	//
	// 它和 [Target.TargetKey] **故意是两样东西**：这条路径可以交给别的操作系统能力
	// （起一个进程、传给一个命令行参数），而目标标识必须继续被当成不透明的。
	ProcessPath(target Target) string

	// FileURL 给出这个执行世界里该目标的规范 file: URI。
	//
	// 源: packages/fs/fs/src/index.ts:128-135
	//
	// URI 编码由后端拥有，因为宿主机的平台可能和执行世界的平台不是同一个——
	// 在 Linux 容器里跑的后端，被一台 Windows 宿主机驱动时，编码规则得按容器那边来。
	FileURL(target Target) string
}
