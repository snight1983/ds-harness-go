// Package objectstore 把 [fs.FileSystem] 这条接缝实现在 S3 兼容的对象存储上。
//
// 新增: DSH 没有这个包。它那边挂在 fs 接缝上的实现是 fs-local（读本机磁盘）和
// fs-sandbox（读沙箱里的磁盘），两个都碰机器资源，在本仓库的裁决里是范围外。
// 服务端要的是一个**不碰机器**的后端，那就是对象存储——所以这个包整份是新写的，
// 不是任何一份 TS 的对译，全文没有 `// 源:` 注释。
//
// # 一个桶里的一片前缀就是一个执行世界
//
// [Config.Bucket] 加 [Config.Prefix] 圈出一个执行世界的根。同一个桶里可以放很多个
// 世界，互不可见——[Store.Resolve] 会把任何试图走到根上面去的路径直接拒掉，
// 于是 `../../别人的世界/秘密.txt` 解析不出目标来。
//
// # 对象存储没有目录，这件事影响了三个方法
//
// 键空间是平的，`a/b.txt` 里的斜杠只是名字的一部分。所以：
//
//   - 「目录」是**推断**出来的：一个键是目录，当且仅当有别的对象以它加斜杠开头。
//     空目录在对象存储里不存在，[Store.Stat] 对它报不在。
//   - 目录没有版本，[Info.Version] 在目录上是空串——那里没有任何东西可写，
//     也就没有守卫要认的新鲜度。
//   - 有些工具会为目录建一个零字节的 `a/b/` 占位对象。[Store.ListDir] 把这种键
//     当成目录标记读掉，不会在列表里冒出一个名字是空串的项。
//
// # 没有符号链接，于是两件事跟着简化
//
// 对象存储里没有链接，一个对象只有一个键。所以：
//
//   - [Store.Resolve] **不做任何 I/O**。接缝允许它做（远端后端可能要来回一趟才能
//     把路径映射成稳定身份），但这里「同一个文件从哪条路径走到都得出同一个 key」
//     这条约定，靠纯粹的路径规范化就已经成立了。少一次网络往返。
//   - [Store.Lstat] 和 [Store.Stat] 看到的是同一件事，它永远不会报 [fs.TypeSymlink]。
//
// # 文本一律以 LF 存、以 LF 读
//
// 接缝要求 [fs.WriteOutcome] 的 Before / After 是「行尾规范化成 LF 的存储文本」，
// 并且 [fs.EditRequest.OldString] 是「在行尾规范化之后」精确匹配。要让这两句话同时
// 为真，而且让「写进去什么、读出来什么」是个往返，只有一个办法：**进出两侧都规范化**。
// 写入前把 CRLF 折成 LF 再存，读取时也折一次（为的是别人写进来的对象）。
//
// 代价写明：一个真的要保留 CRLF 字节的文件，走文本这条路会被改掉。
// 需要原样字节的调用方走 [Store.ReadBytes]——那条路一个字节都不碰。
//
// # 两个方法在这里没有意义，它们 panic
//
// [Store.ProcessPath] 和 [Store.FileURL] 直接 panic，见各自的方法注释。
//
// # 并发写靠的是条件写，不是锁
//
// 接缝要求 [fs.FileSystem.EditText] 的「校验版本、匹配字面、重写」三步在同一个
// 临界区里完成。对象存储上没有临界区可用，这里用**乐观并发**做到等价的效果：
// 读的时候记下 ETag，写的时候带 `If-Match: <那个 ETag>`。两次读之间有人插进来写过，
// 我们这次 PUT 就会被服务端以 412 拒掉，报 [fs.CodeStaleVersion]。
// 结果和临界区一致：编辑绝不会落在一份已经不存在的内容上。
//
// # 关于 CreateIfAbsent 与服务端版本
//
// [fs.CreateIfAbsent] 用 `If-None-Match: *` 做，也就是让**服务端**保证不覆盖，
// 而不是先探测再写。先探测再写的话，两个都以为自己在创建的写会有一个覆盖掉另一个，
// 而两次都报成功——那正是这个守卫存在的理由。
//
// 但这个头需要服务端认。AWS S3 一直认；MinIO 从 RELEASE.2024-09-13 起认，
// **在那之前的版本会把它当不存在，静默覆盖并返回 200**。那是一次失败开放，
// 而且客户端在响应里看不出来——创建和覆盖返回的东西一模一样。
//
// 所以这个包提供 [Store.VerifyCreateIfAbsent]：拿一个保留键做一次两写自测，
// 服务端不认那个头就报出来。它**不在** [New] 里自动跑（构造函数不该偷偷写对象），
// 由部署方或集成测试显式调一次。
//
// # 覆盖率差的那三块，以及为什么就停在这里
//
// 本包的语句覆盖率是 98.9%，低于纯逻辑包 99% 的线。差的是**三块、四条语句**，
// 全部是「minio-go 这套组件下走不到」的错误分支，每一块都在原地写了理由：
//
//   - [Store.fetch] 里 GetObject 的错误分支。GetObject 只在桶名或对象名不合法时
//     失败，而那两种在本包里都够不着。
//   - [Store.fetch] 里的第二道字节上限。它守的是「服务端报的大小和实际给出的
//     字节数不一致」，而 minio-go 解不出 Content-Length 时直接报错、不会退回分块读。
//   - [Store.StreamText] 里 GetObject 的错误分支，同第一条。
//
// 三块都是**接住一个 error**，删掉就得忽略它。把它们凑上去只能靠打桩 minio-go，
// 而那样测到的是桩不是行为——这个包的价值恰恰全在真实的请求和响应上，
// 所以整套用例跑在一台进程内的 S3 服务端上，宁可把这四条语句留在外面。
package objectstore

import (
	"fmt"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/snight1983/ds-harness-go/fs"
)

// 默认值。两个都可以被 [Config] 覆盖。
const (
	// defaultMaxTextBytes 是走文本那几条路（ReadText / StreamText / 写入前后的基准）
	// 一次最多缓冲进内存的字节数。
	//
	// 有这个上限，是因为文本这条路必须先拿到完整内容才能解码和匹配，
	// 而对象存储上一个键背后可以是任意大的东西。超过就报 [fs.CodeTooLarge]，
	// 绝不交出一份被截断的结果——截断的那份看上去是成功的，
	// 而它会被当成完整内容去算摘要、去做匹配。
	defaultMaxTextBytes = 8 << 20

	// defaultChunkBytes 是 [Store.StreamText] 每次交出去的块大小。
	defaultChunkBytes = 64 << 10
)

// Config 是建一个 [Store] 要的全部东西。
type Config struct {
	// Endpoint 是对象存储的 host[:port]，不带协议前缀。
	Endpoint string

	// Bucket 是桶名。
	Bucket string

	// Prefix 是这个执行世界在桶里的根前缀，可以为空（整个桶就是一个世界）。
	//
	// 末尾的斜杠加不加都行，[New] 会规范化。根上面的路径一律解析不出目标来。
	Prefix string

	// AccessKey 与 SecretKey 是静态凭据。
	//
	// 这里收的是明文而不是 credentials 包里的凭据接缝，因为那条接缝管的是
	// **模型提供方**的凭据（引用名、变更通知、轮换），而这里要的只是建一个
	// HTTP 客户端时的两个串。把两件事接在一起会让这个包依赖一整套它用不上的东西。
	AccessKey string
	SecretKey string

	// Region 是签名要用的区域；留空时由 SDK 自己探。
	Region string

	// UseTLS 为真时走 https。
	UseTLS bool

	// MaxTextBytes 覆盖文本路径的字节上限；不填或非正数时用内置默认值。
	MaxTextBytes int64

	// ChunkBytes 覆盖 [Store.StreamText] 的块大小；不填或非正数时用内置默认值。
	ChunkBytes int
}

// Store 是挂在 [fs.FileSystem] 上的对象存储后端。
//
// 零值不可用，必须由 [New] 建。建好之后是并发安全的——它自己没有可变状态，
// 底下的 minio 客户端本身就是并发安全的。
type Store struct {
	client *minio.Client
	bucket string

	// prefix 是规范化过的世界根：要么是空串（整个桶），要么以斜杠结尾。
	// 以斜杠结尾这一点是拼键的前提，见 keyOf。
	prefix string

	maxTextBytes int64
	chunkBytes   int
}

// 编译期确认这个后端真的把十二个原语都实现了。
var _ fs.FileSystem = (*Store)(nil)

// New 按 config 建一个后端。
//
// 它**不做任何网络 I/O**：桶在不在、凭据对不对，都要等第一次真的操作才知道。
// 构造函数里连一次探测都不做，是因为一个「建的时候好好的、用的时候坏了」的后端
// 和一个「建的时候就坏了」的后端，调用方要写的错误处理是同一套；
// 而在构造函数里藏一次网络往返，会让装配阶段的失败看上去像是配置错误。
func New(config Config) (*Store, error) {
	if config.Endpoint == "" {
		return nil, fmt.Errorf("objectstore: 必须给出 Endpoint")
	}
	if config.Bucket == "" {
		return nil, fmt.Errorf("objectstore: 必须给出 Bucket")
	}

	client, err := minio.New(config.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(config.AccessKey, config.SecretKey, ""),
		Secure: config.UseTLS,
		Region: config.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("objectstore: 建客户端失败：%w", err)
	}

	store := &Store{
		client:       client,
		bucket:       config.Bucket,
		prefix:       normalizePrefix(config.Prefix),
		maxTextBytes: config.MaxTextBytes,
		chunkBytes:   config.ChunkBytes,
	}
	if store.maxTextBytes <= 0 {
		store.maxTextBytes = defaultMaxTextBytes
	}
	if store.chunkBytes <= 0 {
		store.chunkBytes = defaultChunkBytes
	}
	return store, nil
}

// ProcessPath 实现 [fs.FileSystem]，但在这个后端上没有意义，所以它 panic。
//
// 接缝对这个方法的要求是「**这个执行世界里的子进程能打开的**规范绝对路径」。
// 对象存储上不存在这样的路径：那里没有子进程，也没有文件系统命名空间。
//
// 三个选项里选了 panic：
//
//   - 返回对象键或者 s3:// URL——那是一次静默的说谎。调用方会把它交给一个
//     命令行参数或者一次 open()，然后在离这里很远的地方失败，
//     而现场只剩一条「找不到文件」。
//   - 返回空串——同样是说谎，只是失败得更晚。
//   - panic——调用方在**第一次**用错的地方就停下，而且停在正确的那一行。
//
// 这不是「没实现完」。这个方法在这个后端上不是缺席的能力，是一个不存在的问题：
// 会调它的消费方（起进程、跑命令）在本仓库的裁决里整支都是范围外。
func (s *Store) ProcessPath(target fs.Target) string {
	panic(fmt.Sprintf(
		"objectstore: 对象存储上没有子进程能打开的路径，ProcessPath 在这个后端上不可用（目标 %q）",
		target.DisplayPath))
}

// FileURL 实现 [fs.FileSystem]，但在这个后端上没有意义，所以它 panic。
//
// 理由和 [Store.ProcessPath] 逐字相同：接缝要的是 `file:` URI，而一个对象没有
// `file:` URI。给一个 `s3://` 串回去只会让拿它当 `file:` 用的人在别处失败。
func (s *Store) FileURL(target fs.Target) string {
	panic(fmt.Sprintf(
		"objectstore: 对象没有 file: URI，FileURL 在这个后端上不可用（目标 %q）",
		target.DisplayPath))
}

// Contains 实现 [fs.FileSystem]：判断 child 是不是 parent 本身或者它的后代。
//
// 键空间是平的，所以这就是一次前缀比较。但比的时候必须**带上那个斜杠**：
// 不带的话 `a/b` 会"包含" `a/bc.txt`，于是一条信任边界规则会把边界外的文件
// 当成边界内的放过去。
func (s *Store) Contains(parent fs.Target, child fs.Target) bool {
	parentKey := string(parent.TargetKey)
	childKey := string(child.TargetKey)

	if parentKey == childKey {
		return true
	}
	// 世界根的键是空串（或者就是 prefix），它包含这个世界里的一切。
	if parentKey == s.prefix {
		return strings.HasPrefix(childKey, s.prefix)
	}
	return strings.HasPrefix(childKey, parentKey+"/")
}
