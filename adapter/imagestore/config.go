// 本文件的作用：这台存储的装配面——字节落在哪条文件系统的哪个根下、这个部署认哪几种图、
// 以及那几条限额。

package imagestore

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/snight1983/ds-harness-go/attachment"
	"github.com/snight1983/ds-harness-go/fs"
)

// 源: packages/attachment/attachment-local/src/index.ts:28-36
const (
	defaultMaxImageBytes        = 20 * 1024 * 1024
	defaultMaxImagesPerMessage  = 20
	defaultMaxMessageImageBytes = 200 * 1024 * 1024
	defaultMaxImagePixels       = 64_000_000
	defaultMaxImageDimension    = 8192
)

// decodable 是本包认得出的那四种媒体类型，也是缺省的准入名单。
//
// 源: packages/attachment/attachment-local/src/index.ts:177
//
// 它同时是一条**装配期**的判据：配一个不在这张表里的媒体类型，
// [attachment.ValidateImageBatch] 会放它进来，而这边解不出格式，
// 于是报「这不是一张图」——那句话是假的，真正的错在装配上。
// 所以 [New] 当场把它拒掉，见 [Config.Limits]。
func decodable() []attachment.MediaType {
	return []attachment.MediaType{
		attachment.MediaTypePNG,
		attachment.MediaTypeJPEG,
		attachment.MediaTypeWebP,
		attachment.MediaTypeGIF,
	}
}

// Files 是本包用到的那四个原语，语义与 [fs.FileSystem] 上同名的四个方法逐字一致。
//
// 新增: DSH 那边这个位置写的是完整的文件系统。这里把它收窄成使用方自己声明的四个方法，
// 依据是 Go Code Review Comments 的「接口定义在使用方」：本包要的只是「按路径存一团
// 不可变的字节、再按同一条路径取回来」，而 [fs.FileSystem] 另外那十二个方法说的是列目录、
// 改文本、删子树这些内容寻址存储永远不会做的事。
//
// 这不是一次放宽，而是把本来就为真的那句话写进类型里：任何 [fs.FileSystem] 都满足它，
// 已经在传文件系统的调用方一个字都不用改。差别在于**没有**文件系统的宿主——
// 一个只有一张数据库表或者一个键值桶的部署，现在实现四个方法就能接上，
// 不必为四个用得着的原语先补出十二个用不着的。
type Files interface {
	// Resolve 把一条路径解成稳定身份。见 [fs.FileSystem.Resolve]。
	Resolve(ctx context.Context, path string, cwd string) (fs.Target, error)
	// MakeDir 建出一条路径上缺的那几层。见 [fs.FileSystem.MakeDir]。
	MakeDir(ctx context.Context, path string, cwd string) (fs.Target, error)
	// ReadBytes 读回原始字节。见 [fs.FileSystem.ReadBytes]。
	ReadBytes(ctx context.Context, target fs.Target, maxBytes int64) ([]byte, error)
	// WriteBytes 写下原始字节。见 [fs.FileSystem.WriteBytes]。
	WriteBytes(ctx context.Context, target fs.Target, content []byte, expected fs.WriteIntent) (fs.Version, error)
}

// Config 是这台存储的装配面。
type Config struct {
	// FS 是字节落到哪儿，必填。
	//
	// 本包对介质没有任何别的要求：它只用 Resolve / MakeDir / WriteBytes / ReadBytes
	// 四个原语（就是 [Files]），其中 MakeDir 在对象存储那类介质上如实什么都不做
	// （见 [fs.FileSystem.MakeDir]）。
	FS Files

	// Root 是对象树的根，必填。字节落在 `<Root>/objects/<sha[:2]>/<sha>`。
	//
	// 它按 [fs.FileSystem.Resolve] 的规则解释，基准留空，所以在本地磁盘后端上
	// 要给一条绝对路径，在对象存储后端上是键前缀。末尾的斜杠会被剥掉。
	Root string

	// Limits 是这个部署的图片限额。五个数为 0 时各自取
	// attachment-local 的默认值（20 MiB / 20 张 / 200 MiB / 6400 万像素 / 8192）。
	//
	// MediaTypes 为 nil 时取 [decodable] 那四种。给一个**非 nil 的空表**
	// 是合法配置，意思是一张图都不收——这正是 [attachment.ImageLimits.MediaTypes]
	// 上写的语义，nil 和空表在 Go 里分得开，所以这里不必为「没配」另开一个字段。
	//
	// 表里出现本包解不出的媒体类型时 [New] 报错，理由见 [decodable]。
	Limits attachment.ImageLimits
}

// resolve 把默认值填上并把那几条装配规矩查一遍。
func (c Config) resolve() (Config, error) {
	switch {
	case c.FS == nil:
		return Config{}, fmt.Errorf("imagestore: 需要一个文件系统")
	case strings.TrimRight(c.Root, "/") == "":
		return Config{}, fmt.Errorf("imagestore: 需要一个非空的对象树根")
	}
	c.Root = strings.TrimRight(c.Root, "/")

	limits := &c.Limits
	for _, field := range []struct {
		value    *int
		fallback int
		name     string
	}{
		{&limits.MaxImageBytes, defaultMaxImageBytes, "单张字节上限"},
		{&limits.MaxImagesPerMessage, defaultMaxImagesPerMessage, "每条消息张数上限"},
		{&limits.MaxMessageImageBytes, defaultMaxMessageImageBytes, "每条消息字节上限"},
		{&limits.MaxImagePixels, defaultMaxImagePixels, "像素数上限"},
		{&limits.MaxImageDimension, defaultMaxImageDimension, "边长上限"},
	} {
		if *field.value < 0 {
			return Config{}, fmt.Errorf("imagestore: %s不能是负数，收到 %d", field.name, *field.value)
		}
		if *field.value == 0 {
			*field.value = field.fallback
		}
	}

	if limits.MediaTypes == nil {
		limits.MediaTypes = decodable()
	} else {
		limits.MediaTypes = slices.Clone(limits.MediaTypes)
		for _, mediaType := range limits.MediaTypes {
			if !slices.Contains(decodable(), mediaType) {
				return Config{}, fmt.Errorf("imagestore: 解不出媒体类型 %s", mediaType)
			}
		}
	}
	return c, nil
}
