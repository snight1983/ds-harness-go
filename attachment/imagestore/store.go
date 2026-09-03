// 本文件的作用：[attachment.Store] 那四个方法——限额、校验、提交、读回，
// 外加内容寻址那几件小事：算键、认引用、把显示名剥干净。
//
// 源: packages/attachment/attachment-local/src/store.ts

package imagestore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/snight1983/ds-harness-go/attachment"
	"github.com/snight1983/ds-harness-go/fs"
)

// idPrefix 是内容寻址标识的前缀，[attachment.ImageRef.ID] 长成 `sha256:<64 位小写十六进制>`。
//
// 源: packages/attachment/attachment-local/src/store.ts:22
const idPrefix = "sha256:"

// nameLimit 是显示名截断后的字节上限。
//
// 源: packages/attachment/attachment-local/src/store.ts:34
const nameLimit = 255

// Store 是建在 [fs.FileSystem] 上的内容寻址图片存储。
//
// 零值不能用，从 [New] 拿。它是并发安全的：本身不持有任何可变状态，
// 并发安全由那条文件系统接缝负责（[fs.CreateIfAbsent] 的不覆盖发布由后端保证）。
type Store struct {
	fs     fs.FileSystem
	root   string
	limits attachment.ImageLimits
}

// 编译期钉住：这个包补上的正是那条接缝。
var _ attachment.Store = (*Store)(nil)

// New 按 config 装出一台存储。
func New(config Config) (*Store, error) {
	resolved, err := config.resolve()
	if err != nil {
		return nil, err
	}
	return &Store{fs: resolved.FS, root: resolved.Root, limits: resolved.Limits}, nil
}

// ImageLimits 实现 [attachment.Store]。
//
// 交出去的是一份拷贝：[attachment.ImageLimits.MediaTypes] 是切片，
// 而这几条限额在这台存储的一生里不许变（[attachment.SaveImages] 先按它判整批、
// 再逐张写，中途变化会让两段按两套规矩走）。
func (s *Store) ImageLimits() attachment.ImageLimits {
	limits := s.limits
	limits.MediaTypes = slices.Clone(s.limits.MediaTypes)
	return limits
}

// ValidateImage 实现 [attachment.Store]：校验一张图但不碰存储。
//
// 源: packages/attachment/attachment-local/src/store.ts:76-82
func (s *Store) ValidateImage(ctx context.Context, input attachment.ImageInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := s.inspect(input)
	return err
}

// inspect 把准入那几条按次序判一遍，返回文件头认出来的事实。
//
// 源: packages/attachment/attachment-local/src/store.ts:56-64,104-107
// 源: packages/attachment/attachment-local/src/image.ts:114-125
//
// 次序照 attachment-local：字节数 → 空 → 认不认得出格式 → 像素数 → 边长 →
// 完整解码 → 声称的类型对不对得上。声称类型那一条排在最后**是上游的次序**，
// 一张既超限又标错类型的图，两边报的是同一个码。
func (s *Store) inspect(input attachment.ImageInput) (detected, error) {
	if len(input.Data) > s.limits.MaxImageBytes {
		return detected{}, &attachment.Error{
			Code:    attachment.CodeImageTooLarge,
			Message: "Image exceeds the configured byte limit.",
		}
	}
	if len(input.Data) == 0 {
		return detected{}, &attachment.Error{
			Code:    attachment.CodeInvalidImage,
			Message: "Image is empty.",
		}
	}
	found, err := probe(input.Data)
	if err != nil {
		return detected{}, err
	}
	// 用 int64 乘：两条边各自都可能接近 int32 的上限，在 32 位平台上按 int 乘会绕回去，
	// 而绕回去的结果是个小数，正好把这条限额判成通过。
	if int64(found.width)*int64(found.height) > int64(s.limits.MaxImagePixels) {
		return detected{}, &attachment.Error{
			Code:    attachment.CodeImageTooManyPixels,
			Message: "Image exceeds the configured decoded-pixel limit.",
		}
	}
	if max(found.width, found.height) > s.limits.MaxImageDimension {
		return detected{}, &attachment.Error{
			Code:    attachment.CodeImageDimensionTooLarge,
			Message: "Image exceeds the configured per-side pixel limit.",
		}
	}
	if err := decodeRaster(input.Data); err != nil {
		return detected{}, err
	}
	if found.mediaType != input.MediaType {
		return detected{}, &attachment.Error{
			Code:    attachment.CodeImageTypeMismatch,
			Message: "Declared image type does not match its bytes.",
		}
	}
	return found, nil
}

// SaveImage 实现 [attachment.Store]：校验并提交一张图。
//
// 源: packages/attachment/attachment-local/src/store.ts:264-271
//
// 新增: 不做归一化缩放，所以 [attachment.ImageRef.OriginalDimensions] 恒为 nil，
// 引用描述的就是操作者交上来的那些字节。理由见 doc.go。
func (s *Store) SaveImage(ctx context.Context, input attachment.ImageInput) (attachment.ImageRef, error) {
	if err := ctx.Err(); err != nil {
		return attachment.ImageRef{}, err
	}
	found, err := s.inspect(input)
	if err != nil {
		return attachment.ImageRef{}, err
	}
	sum := digest(input.Data)
	if err := s.publish(ctx, sum, input.Data); err != nil {
		return attachment.ImageRef{}, err
	}
	return attachment.ImageRef{
		ID:        attachment.ID(idPrefix + sum),
		MediaType: found.mediaType,
		Bytes:     len(input.Data),
		Width:     found.width,
		Height:    found.height,
		Name:      displayName(input.Name),
	}, nil
}

// publish 把字节不覆盖地写进去；已经在了就核对那一份。
//
// 源: packages/attachment/attachment-local/src/store.ts:196-253
//
// 新增: attachment-local 用「临时文件 + hard link + fsync 目录项」自己保证发布的原子性。
// 这条接缝上不需要那一套，[fs.CreateIfAbsent] 就是「不覆盖地发布」，由后端保证。
func (s *Store) publish(ctx context.Context, sum string, data []byte) error {
	if _, err := s.fs.MakeDir(ctx, s.bucketPath(sum), ""); err != nil {
		return writeFailed(err)
	}
	target, err := s.fs.Resolve(ctx, s.objectPath(sum), "")
	if err != nil {
		return writeFailed(err)
	}
	if _, err := s.fs.WriteBytes(ctx, target, data, fs.CreateIfAbsent{}); err != nil {
		if code(err) != fs.CodeNotObserved {
			return writeFailed(err)
		}
		// 撞上了就是「已经有了」，走去重：内容寻址下同一批字节的键本来就该是同一个。
		return s.verifyExisting(ctx, target, sum, len(data))
	}
	return nil
}

// verifyExisting 把已经在的那一份读回来重算摘要，一致就当这次写已经完成。
func (s *Store) verifyExisting(ctx context.Context, target fs.Target, sum string, size int) error {
	// 上限取这次要写的字节数：同一个摘要下不可能有更长的一份，读到更长的只说明那份坏了。
	existing, err := s.fs.ReadBytes(ctx, target, int64(size))
	if err != nil {
		if code(err) == fs.CodeTooLarge {
			return corrupt(nil)
		}
		return &attachment.Error{
			Code:    attachment.CodeAttachmentWriteFailed,
			Message: "Unable to verify the existing image attachment object.",
			Err:     err,
		}
	}
	if digest(existing) != sum {
		return corrupt(nil)
	}
	return nil
}

// ReadImage 实现 [attachment.Store]：读回字节并核对它们仍然是引用描述的那些。
//
// 源: packages/attachment/attachment-local/src/store.ts:281-307
func (s *Store) ReadImage(ctx context.Context, ref attachment.ImageRef) (attachment.StoredImage, error) {
	if err := ctx.Err(); err != nil {
		return attachment.StoredImage{}, err
	}
	sum, err := parseID(ref.ID)
	if err != nil {
		return attachment.StoredImage{}, err
	}
	if ref.Bytes <= 0 {
		// 一张图不可能是零字节，而这个数下面要当读取上限用。
		return attachment.StoredImage{}, invalidRef()
	}
	target, err := s.fs.Resolve(ctx, s.objectPath(sum), "")
	if err != nil {
		return attachment.StoredImage{}, readFailed(err)
	}
	data, err := s.fs.ReadBytes(ctx, target, int64(ref.Bytes))
	if err != nil {
		switch code(err) {
		case fs.CodeNotFound:
			return attachment.StoredImage{}, &attachment.Error{
				Code:    attachment.CodeAttachmentNotFound,
				Message: "Attachment object is missing.",
			}
		case fs.CodeTooLarge:
			// 存储里那份比引用记的长，摘要不可能还对得上。
			return attachment.StoredImage{}, corrupt(nil)
		}
		return attachment.StoredImage{}, readFailed(err)
	}
	if digest(data) != sum {
		return attachment.StoredImage{}, corrupt(nil)
	}
	// 摘要对上了就证明这些字节正是准入时完整解码过的那些，所以这里只重认一遍文件头，
	// 不再解一次栅格——历史回放会一遍遍走这条路。
	found, err := probe(data)
	if err != nil {
		return attachment.StoredImage{}, err
	}
	if found.mediaType != ref.MediaType || len(data) != ref.Bytes ||
		found.width != ref.Width || found.height != ref.Height {
		return attachment.StoredImage{}, &attachment.Error{
			Code:    attachment.CodeAttachmentCorrupt,
			Message: "Stored attachment metadata does not match its reference.",
		}
	}
	return attachment.StoredImage{Ref: ref, Data: data}, nil
}

// bucketPath 是这个摘要落到的那一层。
func (s *Store) bucketPath(sum string) string {
	return s.root + "/objects/" + sum[:2]
}

// objectPath 是这个摘要那一份字节的路径。
//
// 源: packages/attachment/attachment-local/src/store.ts:51-53
//
// sum 一律来自 [digest] 或者 [parseID]，两者都只产出 64 位小写十六进制，
// 所以这条路径里**拼不进**一段路径分隔符或者 `..`——遍历在构造上就不成立。
func (s *Store) objectPath(sum string) string {
	return s.bucketPath(sum) + "/" + sum
}

// digest 算内容寻址用的那个摘要。
//
// 源: packages/attachment/attachment-local/src/store.ts:25-27
func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// parseID 认一个引用标识，交出里面那 64 位十六进制。
//
// 源: packages/attachment/attachment-local/src/store.ts:38-42
func parseID(id attachment.ID) (string, error) {
	value, ok := strings.CutPrefix(string(id), idPrefix)
	if !ok || len(value) != 2*sha256.Size {
		return "", invalidRef()
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return "", invalidRef()
		}
	}
	return value, nil
}

// displayName 把调用方给的名字剥成一个能安全显示的叶名；剥空了就是没有名字。
//
// 源: packages/attachment/attachment-local/src/store.ts:29-36
//
// 两种分隔符都要手工剥：本机的路径规则和交这个名字上来的客户端可能不是同一套，
// 靠本机规则去取叶名的话，一个 Windows 客户端的整条本地路径会原样留在引用里、
// 跟着会话日志落盘。
func displayName(value string) string {
	leaf := value[max(strings.LastIndexByte(value, '/'), strings.LastIndexByte(value, '\\'))+1:]
	clean := strings.TrimSpace(strings.Map(func(char rune) rune {
		if char <= 0x1f || char == 0x7f {
			return -1
		}
		return char
	}, leaf))
	if len(clean) > nameLimit {
		// 截到上限，但不切开一个 UTF-8 码点——切开之后那个字符串显示出来是一个替换符，
		// 而它还会跟着引用一路落盘。
		cut := nameLimit
		for cut > 0 && !utf8.RuneStart(clean[cut]) {
			cut--
		}
		clean = clean[:cut]
	}
	return clean
}

// code 取一个 [fs] 错误的路由码；不是这条接缝的错误时是空串。
func code(err error) fs.ErrorCode {
	var typed *fs.Error
	if errors.As(err, &typed) {
		return typed.Code
	}
	return ""
}

func writeFailed(err error) error {
	return &attachment.Error{
		Code:    attachment.CodeAttachmentWriteFailed,
		Message: "Unable to persist image attachment.",
		Err:     err,
	}
}

func readFailed(err error) error {
	return &attachment.Error{
		Code:    attachment.CodeAttachmentReadFailed,
		Message: "Unable to read image attachment.",
		Err:     err,
	}
}

func corrupt(err error) error {
	return &attachment.Error{
		Code:    attachment.CodeAttachmentCorrupt,
		Message: "Stored attachment failed integrity verification.",
		Err:     err,
	}
}

func invalidRef() error {
	return &attachment.Error{
		Code:    attachment.CodeInvalidAttachmentRef,
		Message: "Attachment reference is invalid.",
	}
}
