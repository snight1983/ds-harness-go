// 本文件是这条接缝的词汇：不透明的目标与版本标识、Stat 给出的元数据、
// 写意图与写结果、字面编辑的请求与结果。
//
// 源: packages/fs/fs/src/types.ts:1-6

package fs

// TargetKey 是后端给出的不透明目标标识，用来做陈旧守卫和目标查找。
//
// 源: packages/fs/fs/src/types.ts:11-16
//
// **消费方不许解析它，也不许假定它是一条本地绝对路径**：本地后端用的是
// realpath 那样的字符串，远端后端可能用工作区 URI 或者一个文件 id。
// 需要一条子进程真能打开的路径时，问 [OSPathFileSystem.ProcessPath]——
// 那是一道可选接缝，不是每个后端都有。
//
// 新增: DSH 那边是 `Branded<'FsTargetKey'>`，配一个同名工厂函数把裸串标记成它。
// 那个函数**自己写明不做任何校验**（types.ts:23），它存在的全部理由就是 TS
// 没有具名字符串类型。Go 有，所以 fs.TargetKey(s) 这个类型转换就是那个函数，
// 一行不用写。
//
// 代价要写明：Go 的具名类型转换是免费的，挡不住 TargetKey("随便什么")，
// 而 TS 的品牌类型挡得住。所以后端在拿到一个不是自己发出去的 key 时，
// 该做的是报 [CodeNotFound]，不是假定它一定合法。
type TargetKey string

// Version 是后端给出的不透明文件版本令牌，也就是写和编辑守卫的那个新鲜度凭据。
//
// 源: packages/fs/fs/src/types.ts:28-35
//
// 本地后端从高精度的 stat 身份和新鲜度字段派生它，远端后端可能直接用修订号。
// 策略层把它记下来做陈旧检查，消费方可以显示相关的元数据，
// 但**绝不许解释这个令牌本身**。
//
// 具名类型对品牌类型的对应关系同 [TargetKey]。
type Version string

// Observation 是对一个目标的一次**权威**观察，封闭的两种。
//
// 源: packages/fs/fs/src/types.ts:47-54
//
// 在场的观察带着版本，那个版本正是带守卫的替换要用的；
// 不在场的观察只授权一次带守卫的创建，**永远不授权一次编辑**——
// 这条区分让策略层不用做 I/O 就能分开「没看过」和「确认没有」。
//
// 新增: DSH 那边是判别联合。Go 没有和类型，这里和 credentials 包的 Record 一样
// 用「接口 + 一个未导出的封印方法」：本包外面写不出 sealedObservation，
// 于是实现方只可能是 [Present] 和 [Absent] 两个。
//
// 不用「一个带 bool 和一个 Version 字段的结构体」，是因为那种写法允许
// 「不在场却带着版本」——一个联合里根本表达不出来的状态，
// 而每一个读它的人都得再判一次这个版本这次算不算数。
type Observation interface {
	// PresentVersion 给出在场观察带的版本；不在场时第二个返回值是 false。
	PresentVersion() (Version, bool)

	// sealedObservation 把实现方封在本包内，见类型注释。
	sealedObservation()
}

// Present 是一次「它在，版本是这个」的观察。
//
// 源: packages/fs/fs/src/types.ts:53
type Present struct {
	// Version 是观察到的那一刻的新鲜度令牌，**必须非空**（见 [RegisterInvariants]）。
	Version Version
}

// PresentVersion 实现 [Observation]。
func (p Present) PresentVersion() (Version, bool) { return p.Version, true }

func (Present) sealedObservation() {}

// Absent 是一次「确认它不在」的观察。
//
// 源: packages/fs/fs/src/types.ts:54
//
// 它不带任何字段，和「没有观察过」是两回事：后者压根不会产生一条 [Observation]。
type Absent struct{}

// PresentVersion 实现 [Observation]。
func (Absent) PresentVersion() (Version, bool) { return "", false }

func (Absent) sealedObservation() {}

// Target 是一条被后端解析成稳定身份的路径。
//
// 源: packages/fs/fs/src/types.ts:56-68
//
// [FileSystem.Resolve] 产出它，其余每一个操作都收它。
type Target struct {
	// TargetKey 是不透明的目标标识，见 [TargetKey]。
	TargetKey TargetKey

	// DisplayPath 是给模型和界面看的路径。
	//
	// 按后端不同，它可能是本地绝对路径、工作区相对路径、或者一个远端 URI。
	// 它是这个结构体里**唯一**可以直接展示给人看的字段。
	DisplayPath string
}

// EntryType 是一个文件系统项的类别。
//
// 源: packages/fs/fs/src/types.ts:79-80,94-95
//
// 新增: DSH 那边是两个不同的字符串联合：[Info] 的那个**没有** symlink
// （Stat 跟着符号链接走，所以它永远看不到一个链接），[PathInfo] 的那个有。
// Go 里要表达「一个字符串枚举的子集」只能再开一个类型，代价是三个常量各写两遍、
// [Info] 和 [PathInfo] 之间还要来回转换。
//
// 这里合成一个类型。丢掉的只有编译期的那一点：一个消费方可以为 Stat 的结果
// 写一条 [TypeSymlink] 分支，而那条分支永远走不到。那是一段死代码，
// 不是一次错判——它不会让任何一次判断得出错误的结论。
type EntryType string

const (
	// TypeFile 是常规文件。
	TypeFile EntryType = "file"
	// TypeDirectory 是目录。
	TypeDirectory EntryType = "directory"
	// TypeSymlink 是符号链接，**只可能出现在 [PathInfo] 里**，见 [EntryType]。
	TypeSymlink EntryType = "symlink"
	// TypeOther 是上面几种之外的东西（设备、套接字、命名管道……）。
	TypeOther EntryType = "other"
)

// Info 是一个**目标**的元数据，也就是 [FileSystem.Stat] 给出的东西。
//
// 源: packages/fs/fs/src/types.ts:70-83
//
// 有了它，策略层能在读之前就把目录和特殊文件拒掉，并且靠 [Info.Size] 在
// [FileSystem.ReadText] 和 [FileSystem.StreamText] 之间选一个——
// 而不是先按 ReadText 试一次、失败了再改口（那种「靠失败探测」的写法
// 会把一个真正的读取故障当成「文件太大」）。
type Info struct {
	// Version 是此刻的新鲜度令牌。
	Version Version

	// Type 是目标的类别；**不可能是 [TypeSymlink]**，理由见 [EntryType]。
	Type EntryType

	// Size 是常规文件的字节数；后端报不出时是 nil。
	//
	// 新增: DSH 那边是可选属性 `size?`，缺席表示后端报不出。Go 这边用指针，
	// 不用 0 也不用 -1 当哨兵：0 是一个合法的文件大小，而 -1 会让
	// 「size 超过阈值吗」这种判断在大小未知的时候**悄悄取假**，
	// 也就是把一个大小不明的文件整个读进内存。
	// 指针逼着调用方先回答「知不知道」，再回答「多大」。
	Size *int64
}

// PathInfo 是一条**路径**的元数据，最后一段是符号链接时不跟过去。
//
// 源: packages/fs/fs/src/types.ts:85-98
//
// 和 [Info] 不同，这个路径级别的探测报得出 [TypeSymlink]，
// 于是带信任边界规则的消费方可以在解析出目标**之前**就把一条仓库自带的链接拒掉。
type PathInfo struct {
	// Version 是此刻这条路径项的新鲜度令牌。
	Version Version

	// Type 是路径项的类别，可能是四个值中的任何一个。
	Type EntryType

	// Size 是路径项的字节数；后端报不出时是 nil，理由同 [Info.Size]。
	Size *int64
}

// DirEntry 是 [FileSystem.ListDir] 给出的一个直接子项。
//
// 源: packages/fs/fs/src/types.ts:100-115
//
// 列目录只给元数据和解析好的子目标，**绝不读文件内容**。
type DirEntry struct {
	// Name 是这个子项在被列的目录里的基名。
	Name string

	// Type 是子项的类别；和 [Info.Type] 同一套取值，不会是 [TypeSymlink]。
	Type EntryType

	// Target 是解析好的子目标，可以直接拿去做后续操作。
	Target Target

	// Version 是后端能廉价拿到元数据时给出的新鲜度令牌；拿不到时是空串。
	//
	// 新增: DSH 是可选属性。这里用空串表示缺席而不用指针（和 [Info.Size] 不同），
	// 是因为空串**不是**一个合法的 [Version]——这条由 [RegisterInvariants] 守着。
	// 而 0 是一个合法的大小，所以那边没有这条退路。
	Version Version

	// Size 是常规文件的字节数；后端报不出时是 nil，理由同 [Info.Size]。
	Size *int64
}

// WriteIntent 是一次**带守卫**的写意图，封闭的两种。
//
// 源: packages/fs/fs/src/types.ts:117-125
//
// [CreateIfAbsent] 碰到已存在的目标报 [CodeNotObserved]；
// [ReplaceIfVersion] 碰到不在场或者版本对不上报 [CodeStaleVersion]。
//
// **省掉守卫（传 nil）不是第三个成员**，而是这个值不存在：它表示无条件的
// 创建或覆盖。封印接口正好把这件事说对了——nil 是一个接口值的缺席，
// 不是它的一种取值。
type WriteIntent interface {
	// sealedWriteIntent 把实现方封在本包内。
	sealedWriteIntent()
}

// CreateIfAbsent 要求这次写是一次创建：目标已经在了就拒绝。
//
// 源: packages/fs/fs/src/types.ts:124
//
// 后端必须用一次**不覆盖**的发布来做它（本地后端就是 O_EXCL），
// 而不是先探测再写。先探测再写的话，两个都以为自己在创建的写会有一个覆盖掉另一个，
// 而两次都报成功。
type CreateIfAbsent struct{}

func (CreateIfAbsent) sealedWriteIntent() {}

// ReplaceIfVersion 要求这次写落在观察到的那个版本上。
//
// 源: packages/fs/fs/src/types.ts:125
type ReplaceIfVersion struct {
	// Version 是调用方观察到的那个版本，**必须非空**。
	Version Version
}

func (ReplaceIfVersion) sealedWriteIntent() {}

// EditIntent 是一次编辑的版本守卫。
//
// 源: packages/fs/fs/src/types.ts:66,246
//
// 新增: DSH 那边这个形状是就地写的字面类型 `{ version: FsVersion }`，
// 在 index.ts 里出现两次（fs/edit-intent 事件的返回值、editText 的 expected 参数）。
// Go 里给它一个名字，因为那两处必须是同一个东西：策略决定出来的守卫
// 就是原样递给 [FileSystem.EditText] 的那个。
//
// 它没有做成 [WriteIntent] 那样的联合，是因为编辑只有一种守卫——
// 「文件不在的时候创建它」对一次字面替换没有意义。
type EditIntent struct {
	// Version 是调用方观察到的那个版本，**必须非空**。
	Version Version
}

// WriteOperation 说明一次写到底是创建还是替换。
//
// 源: packages/fs/fs/src/types.ts:129-130
type WriteOperation string

const (
	// OperationCreate 表示这次写产生了一个此前不存在的文件。
	OperationCreate WriteOperation = "create"
	// OperationUpdate 表示这次写替换了一个已经存在的文件。
	OperationUpdate WriteOperation = "update"
)

// WriteOutcome 是一次整文件写入的结果。
//
// 源: packages/fs/fs/src/types.ts:127-144
type WriteOutcome struct {
	// Operation 说明这次写是创建还是替换。
	Operation WriteOperation

	// Version 是写完之后文件的不透明版本。
	Version Version

	// Before 是这次写**之前**的文件内容；为 nil 有两种可能：
	// 文件本来不存在（一次创建），或者后端拒绝给出一个上下文基准
	// （比如之前那份是二进制或者非 UTF-8，又或者覆盖的任一侧超过了它的独占上限）。
	//
	// 它是**行尾规范化成 LF 的存储文本，永远不是一段 diff**：调用方在拿到结果之后
	// 自己从 Before / After 算带上下文的 diff，Before 为 nil 时退回整文件 diff。
	//
	// 新增: DSH 是 `string | null`。Go 这边用指针而不是空串，
	// 因为**空文件的内容就是空串**——用空串表示缺席的话，一次「把一个空文件
	// 覆盖成有内容」会被当成没有基准，于是退回整文件 diff，
	// 把一次显然的新增显示成一次全文替换。
	Before *string

	// After 是这次写之后的文件内容，同样行尾规范化成 LF，
	// 和 Before 共用一个 diff 基准。
	After string
}

// EditRequest 是一次字面替换请求。
//
// 源: packages/fs/fs/src/types.ts:146-154
type EditRequest struct {
	// OldString 是要被替换掉的那段**非空**字面文本，必须（在行尾规范化之后）精确匹配。
	OldString string

	// NewString 是替换进去的字面文本；空串表示删掉匹配到的那段。
	NewString string

	// ReplaceAll 为真时替换每一处匹配，为假时要求**恰好**匹配一处。
	//
	// 恰好一处而不是「第一处」：多处匹配却只改第一处的话，调用方会以为改完了，
	// 而剩下的几处还是旧的（见 [CodeAmbiguousEdit]）。
	ReplaceAll bool
}

// EditOutcome 是一次字面编辑的结果。
//
// 源: packages/fs/fs/src/types.ts:156-168
type EditOutcome struct {
	// Version 是编辑完之后文件的不透明版本。
	Version Version

	// Before 是编辑**之前**的文件内容，是后端行尾规范化过的原始存储文本，
	// 永远不是一段 diff。
	//
	// 新增: 这里是值不是指针（和 [WriteOutcome.Before] 不同）：一次编辑必须有
	// 一份被编辑的内容存在，没有「文件本来不在」这种情况——那种情况在编辑这条路上
	// 报的是 [CodeStaleVersion]。
	Before string

	// After 是编辑之后的文件内容。
	After string
}
