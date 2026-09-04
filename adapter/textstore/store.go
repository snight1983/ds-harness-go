// 本文件的作用：[spill.Store] 上那唯一一个方法——起名、建目录、不覆盖地写下去，
// 外加把建议名净化成一个安全片段。
//
// 源: packages/spill/spill-local/src/store.ts
// 源: packages/spill/spill-local/src/index.ts:149-161

package textstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"github.com/snight1983/ds-harness-go/fs"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/spill"
)

// randomLen 是随机那一段的字节数，写成十六进制就是 16 位。
//
// 源: packages/spill/spill-local/src/store.ts:110
//
// 新增: spill-local 取 6 个字节（12 位）。这里取 8：那边的键落在一台机器的
// 私有临时目录里，这边落在一棵所有副本共写的产物树上，撞名的机会不是一个量级。
const randomLen = 8

// sessionHexLen 是会话目录名里那段哈希的长度。
//
// 源: packages/spill/spill-local/src/store.ts:79
const sessionHexLen = 12

// nameLimit 是净化后的建议名的字节上限。
//
// 新增: spill-local 不截断——它那个 encodeSegment 是可逆编码，截断会毁掉可逆性。
// 这边不要可逆（见包文档），于是可以截。不截的话，一个几 KB 长的工具名
// 会拼出一条超过介质单段上限的键。
const nameLimit = 64

// Store 是建在 [fs.FileSystem] 上的外置文本存储。
//
// 零值不能用，从 [New] 拿。它是并发安全的：本身不持有任何可变状态，
// 并发安全由那条文件系统接缝负责（[fs.CreateIfAbsent] 的不覆盖发布由后端保证）。
type Store struct {
	fs            fs.FileSystem
	root          string
	rand          io.Reader
	retrievalHint string
}

// 编译期钉住：这个包补上的正是那条接缝。
var _ spill.Store = (*Store)(nil)

// New 按 config 装出一台存储。
func New(config Config) (*Store, error) {
	resolved, err := config.resolve()
	if err != nil {
		return nil, err
	}
	return &Store{
		fs:            resolved.FS,
		root:          resolved.Root,
		rand:          resolved.Rand,
		retrievalHint: resolved.RetrievalHint,
	}, nil
}

// SaveText 实现 [spill.Store]：把整段文本逐字存成一份归在会话名下的产物。
//
// 源: packages/spill/spill-local/src/index.ts:149-161
// 源: packages/spill/spill-local/src/store.ts:108-131
func (s *Store) SaveText(ctx context.Context, input spill.SaveText) (spill.Ref, error) {
	if err := ctx.Err(); err != nil {
		return spill.Ref{}, err
	}
	suffix := make([]byte, randomLen)
	if _, err := io.ReadFull(s.rand, suffix); err != nil {
		// 取不到随机字节就**不写**：退回一个猜得出来的名字，等于让后来的一次写
		// 有机会顶掉在先那一份。
		return spill.Ref{}, fmt.Errorf("textstore: 取不到随机名：%w", err)
	}
	dir := s.sessionDir(input.Owner.SessionID)
	if _, err := s.fs.MakeDir(ctx, dir, ""); err != nil {
		return spill.Ref{}, fmt.Errorf("textstore: 建不出会话目录：%w", err)
	}
	name := hex.EncodeToString(suffix) + "-" + safeName(input.SuggestedName)
	target, err := s.fs.Resolve(ctx, dir+"/"+name, "")
	if err != nil {
		return spill.Ref{}, fmt.Errorf("textstore: 解析不出产物路径：%w", err)
	}
	data := []byte(input.Content)
	if _, err := s.fs.WriteBytes(ctx, target, data, fs.CreateIfAbsent{}); err != nil {
		// 撞名走的也是这条路，理由见包文档：不覆盖，也不重试。
		return spill.Ref{}, fmt.Errorf("textstore: 写不下这份产物：%w", err)
	}
	return spill.Ref{
		// 句柄取 [fs.Target.DisplayPath] 而不是 TargetKey：它要渲染给模型看，
		// 而 DisplayPath 是 [fs.Target] 上唯一一个可以直接展示给人看的字段。
		Locator:       spill.Locator(target.DisplayPath),
		Bytes:         len(data),
		RetrievalHint: s.retrievalHint,
	}, nil
}

// sessionDir 是这个会话归拢到的那一层。
//
// 源: packages/spill/spill-local/src/store.ts:78-81
func (s *Store) sessionDir(id sessionlog.SessionID) string {
	sum := sha256.Sum256([]byte(id))
	return s.root + "/session-" + hex.EncodeToString(sum[:])[:sessionHexLen]
}

// safeName 把调用方建议的名字净化成一个安全的路径片段。
//
// 源: packages/spill/spill-local/src/store.ts:55-68
//
// 两种分隔符和别的一切都换成下划线，所以这里**拼不出**一段路径分隔符；
// 而调用点在它前面缀了 16 位随机 hex，所以这一段也不可能是 `.` 或者 `..`。
func safeName(value string) string {
	clean := strings.Map(func(char rune) rune {
		switch {
		case char >= 'a' && char <= 'z',
			char >= 'A' && char <= 'Z',
			char >= '0' && char <= '9',
			char == '.', char == '_', char == '-':
			return char
		}
		return '_'
	}, value)
	// 换完之后整段都是 ASCII，所以按字节截不会切开一个码点。
	if len(clean) > nameLimit {
		clean = clean[:nameLimit]
	}
	return clean
}
