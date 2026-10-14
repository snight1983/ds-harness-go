// 本文件的作用：把每一次「发给模型的」和「模型返回的」原样各存成一个文件。
//
// # 存什么
//
//   - `<序号>-request.json` —— 发出去的那一整份请求体，一个字节不改。
//   - `<序号>-response.txt` —— 服务端返回的每一条分块，一行一条，原文。
//
// # 为什么要原文
//
// 有一条链路上偶尔会出现这样一步：模型这一轮既没给正文、也没给工具调用，只有一段
// 思考；而那步思考的**文字里**写着一个工具调用的名字和参数。那一步于是什么都没做。
//
// 从适配器交出去的东西上看不出这是谁造成的——模型把调用写成了字、服务端把它塞进了
// 思考字段、还是适配器归错了类，三种可能留下的痕迹一样。只有两边都留着原文才分得清：
// 请求里那件工具在不在清单里、返回里那段字出现在哪个字段、`finish_reason` 是什么。
//
// # 它不改任何行为
//
// 只往文件里写。写不进去就当作没这回事——一条诊断记录绝不该把一次请求弄失败。
//
// # 默认是关的
//
// 开关是 [AdapterOptions.RawLogDir]，空串表示不记，**这是默认**。它一次请求写两个
// 文件、约 1.7 MB，几小时就是几十 MB，所以它只该在查一件事的时候打开。
//
// # 落到哪块介质上是装配的事
//
// 本包不碰跑着这个进程的那台机器的磁盘，只声明它要「建出一层目录、按一条路径写下
// 字节」，所以这里收的是 [RawLogFiles] 那条缝，而不是一个目录句柄。理由与
// [adapter/imagestore] 那边同一条，见 internal/devtools/oscheck。
//
// 新增: DSH 那边这条记录落在宿主进程自己的文件系统上。这边没有一台可依赖的宿主
// 文件系统：存储全在数据库和对象存储上，介质是装配点的事，本包只收三个原语。
package openaicompat

import (
	"context"
	"fmt"
	"path"
	"strings"
	"sync/atomic"

	"github.com/snight1983/ds-harness-go/fs"
)

// RawLogFiles 是本包存原文用到的那三个原语，语义与 [fs.FileSystem] 上同名的方法逐字一致。
//
// 收窄成三个的理由同 [adapter/imagestore.Files]：本包要的只是「建出一层目录、按一条
// 路径写下字节」，而 [fs.FileSystem] 另外那些方法说的是列目录、改文本、删子树——内容
// 寻址存储永远不会做的事。这不是一次放宽，而是把本来就为真的那句话写进类型里：任何
// [fs.FileSystem] 都满足它，差别在于**没有**文件系统的宿主——一个只有一张数据库表的
// 部署，现在实现三个方法就能接上，不必为三个用得到的原语先补出用不着的那些。
type RawLogFiles interface {
	// MakeDir 建出一条路径上缺的那几层。见 [fs.FileSystem.MakeDir]。
	MakeDir(ctx context.Context, path string, cwd string) (fs.Target, error)
	// Resolve 把一条路径解成稳定身份。见 [fs.FileSystem.Resolve]。
	Resolve(ctx context.Context, path string, cwd string) (fs.Target, error)
	// WriteBytes 写下原始字节。见 [fs.FileSystem.WriteBytes]。
	WriteBytes(ctx context.Context, target fs.Target, content []byte, expected fs.WriteIntent) (fs.Version, error)
}

var rawSeq atomic.Int64

// rawSession 是这一次请求的落点。一次请求一个，形影不离。
type rawSession struct {
	files RawLogFiles
	dir   string
	seq   int64

	// response 攒着这一次收到的每一条分块原文。
	//
	// 那条缝上没有「追加」这个原语——[fs.FileSystem.WriteBytes] 的语义是原子地创建
	// 或替换——所以边收边落盘没有对应写法：分块先进内存，收尾时一次写出去。代价是
	// 进程崩在半路时这一次的返回原文就没了；它本来就只是查一件事时临时打开的记录。
	response strings.Builder
}

// rawOpen 按配置里那条路径开一份记录。
//
// 路径为空、或者没给出介质，都交回 nil，后面每一个方法在 nil 上都当空操作——所以
// 调用点不用自己分支。建不出目录也交回 nil：记不下来是小事，为它把一次请求弄失败
// 是大事。
func rawOpen(ctx context.Context, files RawLogFiles, dir string) *rawSession {
	if dir == "" || files == nil {
		return nil
	}
	if _, err := files.MakeDir(ctx, dir, ""); err != nil {
		return nil
	}
	// **不清目录。**
	//
	// 先前这里会先把目录清一遍，本意是「这份记录只属于这一次」。代价是把上一次
	// 的证据也删了——而那正是要找的那一次。文件名带序号，本来就不会互相覆盖。
	return &rawSession{files: files, dir: dir, seq: rawSeq.Add(1)}
}

func (s *rawSession) path(suffix string) string {
	return path.Join(s.dir, fmt.Sprintf("%03d-%s", s.seq, suffix))
}

// write 把一个后缀的内容写下去。任何一步不通都当作没这回事。
func (s *rawSession) write(ctx context.Context, suffix string, body []byte) {
	target, err := s.files.Resolve(ctx, s.path(suffix), "")
	if err != nil {
		return
	}
	// nil 意图就是无条件写。名字里带序号，同一次运行里不会自己撞自己；跨进程重启
	// 之后序号从头数起，那时覆盖掉上一次同序号的那份，与先前 os.WriteFile 一致。
	_, _ = s.files.WriteBytes(ctx, target, body, nil)
}

// request 存下这一次发出去的请求体。
func (s *rawSession) request(ctx context.Context, body []byte) {
	if s == nil {
		return
	}
	s.write(ctx, "request.json", body)
}

// chunk 收下一条返回的分块原文。
func (s *rawSession) chunk(raw string) {
	if s == nil {
		return
	}
	s.response.WriteString(raw)
	s.response.WriteByte('\n')
}

// note 收下一行给读的人看的标注（比如收尾时的裁决）。
func (s *rawSession) note(text string) {
	s.chunk("# " + text)
}

// flush 把攒下来的返回原文一次写出去。
//
// 流走到哪一步都该调它：半路断了的那半截也是要找的证据。
func (s *rawSession) flush(ctx context.Context) {
	if s == nil {
		return
	}
	s.write(ctx, "response.txt", []byte(s.response.String()))
}
