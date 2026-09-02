// 本文件的作用：预设内容住在哪儿这道缝。本包只声明要它做哪几件事，
// 至于底下是一棵本地目录树、一个对象存储桶、还是一张表，本包不知道也不该知道。
//
// 新增: 上游 DSH 是一个单机 CLI，预设就是用户 home 底下的目录，`node:fs` 直接读。
// 这条移植原样抄过，于是 discovery/metadata/mount/authoring 四个文件里散着二十处
// `os.*` 调用，把「一份预设」这件事钉死在**跑着这个进程的那台机器的磁盘**上。
// 服务化之后这是错的，和会话事件曾经落在本地 session.jsonl 上是同一个病：
// 每个节点都得有这些目录，扩一个副本要先同步一遍磁盘，而这份内容本身和某台机器
// 没有任何关系。
//
// 依赖方向和 datastore 那次一样是反的：适配层认识 [Store]，[Store] 不认识适配层。

package agentpresets

import "context"

// Store 是一棵预设树的读写能力。
//
// **路径一律用斜杠分隔**，并且对本包是不透明的：本包只做 [path.Join] 和 [path.Clean]
// 这类纯字符串拼接，从不假设它能交给操作系统。真实介质上的分隔符、前缀、桶名由
// 实现方在自己那一侧翻译。
//
// 这道缝上没有符号链接、没有权限位、没有修改时间。它们是本地文件系统才有的东西，
// 摆在这里会让每一个非文件系统的实现都得编一份出来——而编出来的那份必然是假的。
// 需要「这两次看到的是不是同一份内容」时用 [Entry.Stamp]。
type Store interface {
	// Stat 看一条路径上是什么；不存在时第二个返回值是 false，且不算错误。
	//
	// 「不存在」和「读不了」必须分得开：前者是常态（用户根在第一份本地创作出现
	// 之前都不存在），后者是这套部署配错了。
	Stat(ctx context.Context, path string) (Entry, bool, error)

	// List 列出一个目录的直接子项，按名字升序。目录不存在时第二个返回值是 false。
	//
	// 交出来的是**这个目录自己记着的那几行**，不跟符号链接走：一个指向目录的链接
	// 在这里是 [Child.Dir] 为假。这和 [Store.Stat] 顺链取实体是有意分开的两种语义，
	// 因为它们的两个调用点要的正好相反——发现预设时一个链接不该算一份预设，
	// 而复制一份预设时一个链接要按它指的那个实体带走。合成一个方法必然让其中
	// 一边悄悄换了行为。
	List(ctx context.Context, dir string) ([]Child, bool, error)

	// ReadFile 读出整份内容。
	ReadFile(ctx context.Context, path string) ([]byte, error)

	// WriteFile 写下整份内容，路径上已经有东西就覆盖。
	//
	// executable 是唯一被带过这道缝的权限概念——一份预设可以带可执行的辅助脚本，
	// 而这件事在对象存储上没有对应物，那里如实忽略它即可。其余的权限收紧
	// （组和其他人的位一律剥掉）归实现方，因为只有它知道自己那份介质有没有这回事。
	WriteFile(ctx context.Context, path string, content []byte, executable bool) error

	// MakeDir 建出一个目录，连同缺掉的上级；已经在了不算错。
	//
	// 对象存储那类没有真目录的介质如实什么都不做——[Store.WriteFile] 在那里
	// 本来就不要求上级先存在。
	MakeDir(ctx context.Context, dir string) error

	// Remove 删掉一条路径上的单个条目；不在不算错。
	Remove(ctx context.Context, path string) error

	// RemoveTree 删掉一棵子树；不在不算错。
	//
	// 它是「一次失败的复制什么都不留下」那条撤销路唯一的依靠，所以**必须**在
	// 部分写入之后仍然清得干净。
	RemoveTree(ctx context.Context, dir string) error
}

// Child 是一个目录里的一行，按这个目录自己记着的样子，**不跟符号链接走**。
//
// 它比 [Entry] 窄，是因为一次列举答得出的就这么多：一个远端实现要为每个孩子
// 补上大小和版本，得逐个再往返一趟，而这道缝上唯一的列举点（发现预设）
// 只看名字和是不是目录。想知道更多的调用点自己 [Store.Stat] 一次。
type Child struct {
	// Name 是这一行的名字，不带任何路径。
	Name string
	// Dir 为真表示这一行本身就是个目录（不是一个指向目录的链接）。
	Dir bool
}

// Entry 是这道缝上一条路径的全部元数据，**顺链取实体**。
//
// 新增: 刻意比 [io/fs.FileInfo] 窄。这里没有 [io/fs.FileMode]、没有修改时间，
// 因为一个对象存储答不出它们，而让它编一个出来，会让上面那些「按修改时间比一比」
// 的判断在那份介质上悄悄地永远成立或者永远不成立。
type Entry struct {
	// Name 是这条路径的最后一段。
	Name string
	// Dir 为真表示它是个目录。
	Dir bool
	// Regular 为真表示它是一份读得出内容的常规文件。
	//
	// Dir 和 Regular 都为假是合法的：本地介质上的设备、套接字、断掉的链接
	// 都落在这里。调用方一律跳过它们——一份预设目录里没有它们的位置。
	Regular bool
	// Executable 为真表示这份文件带可执行位。答不出的介质一律给假。
	Executable bool
	// Stamp 是一个不透明的版本戳：同一份没变过的内容观察多少次都是同一个值，
	// 内容变了就必须变。空串表示这份介质答不出，那时调用方只能当它每次都变了。
	//
	// 本地实现拿修改时间加大小拼，对象存储拿 ETag，数据库拿行版本。
	// 拿它做的唯一一件事是「上次装的那份组合还是不是同一份」，
	// 所以它只需要可比，不需要可解释。
	Stamp string
}
