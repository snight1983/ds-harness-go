// 本文件的作用：这台存储的装配面——产物落在哪条文件系统的哪个根下、
// 随机名从哪儿取、以及交给模型的那句取回说明。

package textstore

import (
	"crypto/rand"
	"fmt"
	"io"
	"strings"

	"github.com/snight1983/ds-harness-go/fs"
)

// Config 是这台存储的装配面。
type Config struct {
	// FS 是产物落到哪儿，必填。
	//
	// 本包对介质没有任何别的要求：它只用 Resolve / MakeDir / WriteBytes 三个原语，
	// 其中 MakeDir 在对象存储那类介质上如实什么都不做
	// （见 [fs.FileSystem.MakeDir]）。
	FS fs.FileSystem

	// Root 是产物树的根，必填。产物落在
	// `<Root>/session-<12 位 hex>/<16 位 hex>-<净化过的建议名>`。
	//
	// 它按 [fs.FileSystem.Resolve] 的规则解释，基准留空，所以在本地磁盘后端上
	// 要给一条绝对路径，在对象存储后端上是键前缀。末尾的斜杠会被剥掉。
	Root string

	// Rand 是随机名那一段的字节来源，为 nil 时用 [crypto/rand.Reader]。
	//
	// 它是一条**测试用的接缝**，不是部署旋钮：那 8 个字节是不撞名的唯一来源，
	// 换一个猜得出来的来源，等于把 [fs.CreateIfAbsent] 那道保护一起废掉。
	Rand io.Reader

	// RetrievalHint 是给模型看的取回说明，必填，理由见包文档。
	//
	// 它**面向模型**，所以和本仓库其余面向模型的载荷一样保持英文。
	// 它要说明的是：拿到那个句柄之后走哪条通道、用什么办法把全文读回来。
	RetrievalHint string
}

// resolve 把默认值填上并把那几条装配规矩查一遍。
func (c Config) resolve() (Config, error) {
	switch {
	case c.FS == nil:
		return Config{}, fmt.Errorf("textstore: 需要一个文件系统")
	case strings.TrimRight(c.Root, "/") == "":
		return Config{}, fmt.Errorf("textstore: 需要一个非空的产物树根")
	case strings.TrimSpace(c.RetrievalHint) == "":
		return Config{}, fmt.Errorf("textstore: 需要一句取回说明")
	}
	c.Root = strings.TrimRight(c.Root, "/")
	if c.Rand == nil {
		c.Rand = rand.Reader
	}
	return c, nil
}
