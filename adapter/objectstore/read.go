// 本文件是只读的那六个原语：Stat / Lstat / ReadText / StreamText / ReadBytes / ListDir。

package objectstore

import (
	"context"
	"errors"
	"io"
	"iter"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/minio/minio-go/v7"

	"github.com/snight1983/ds-harness-go/fs"
)

// errNotUTF8 是解码器内部的哨兵，走到调用点会被换成一条带 [fs.CodeNotText] 的
// *[fs.Error]。解码器自己不造那条错误，是因为它手上没有目标的展示路径。
var errNotUTF8 = errors.New("objectstore: 不是合法的 UTF-8")

// etagOf 把服务端给的 ETag 折成一个可以当 [fs.Version] 用的串。
//
// 引号要去掉：HTTP 的 ETag 语法带引号，而有的服务端在有的接口上给的是裸串。
// 同一个对象在 Stat 和 PUT 两条路上给出不同形状的版本，会让一次
// 「读出来、原样带回去」的守卫必定失配。
func etagOf(etag string) string {
	return strings.Trim(etag, `"`)
}

// isRootKey 判断一个键是不是这个世界的根。
func (s *Store) isRootKey(key string) bool {
	return key == s.prefix
}

// Stat 实现 [fs.FileSystem]：给出目标的元数据，不存在时第二个返回值是 false。
//
// 对象存储没有目录，所以这里分两步：先当对象查一次，查不到再看有没有别的键
// 以「它加斜杠」开头。后一步就是「目录」在这个后端上的**全部**定义，
// 也意味着**空目录不存在**——那里没有任何对象，也就没有任何东西能证明它在。
func (s *Store) Stat(ctx context.Context, target fs.Target) (fs.Info, bool, error) {
	key := string(target.TargetKey)
	if s.isRootKey(key) {
		// 世界根永远在，而且永远是目录。它不由任何一个对象证明——
		// 一个空世界的根照样得能被 ListDir 列出空来。
		return fs.Info{Type: fs.TypeDirectory}, true, nil
	}

	info, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err == nil {
		size := info.Size
		return fs.Info{
			Version: fs.Version(etagOf(info.ETag)),
			Type:    fs.TypeFile,
			Size:    &size,
		}, true, nil
	}
	if !isNotFound(err) {
		return fs.Info{}, false, translate(err, fs.CodeIOError, "查看目标失败："+target.DisplayPath)
	}

	hasChildren, err := s.hasChildren(ctx, key)
	if err != nil {
		return fs.Info{}, false, err
	}
	if !hasChildren {
		return fs.Info{}, false, nil
	}
	// 目录不带版本：那里没有任何东西可写，也就没有守卫要认的新鲜度。
	return fs.Info{Type: fs.TypeDirectory}, true, nil
}

// hasChildren 判断有没有任何键以 key 加斜杠开头。
func (s *Store) hasChildren(ctx context.Context, key string) (bool, error) {
	// 列举在 minio-go 里是一个后台 goroutine 往通道里推，只有 ctx 结束才会停。
	// 拿到第一条就得取消，否则一个大目录会让那个 goroutine 一直翻页翻下去。
	listing, stop := context.WithCancel(ctx)
	defer stop()

	object, listed := <-s.client.ListObjects(listing, s.bucket, minio.ListObjectsOptions{
		Prefix:    s.dirPrefixOf(key),
		Recursive: true,
		MaxKeys:   1,
	})
	if !listed {
		return false, nil
	}
	if object.Err != nil {
		return false, translate(object.Err, fs.CodeIOError, "列举子项失败："+key)
	}
	return true, nil
}

// Lstat 实现 [fs.FileSystem]：给出一条**路径**的元数据，不跟随最后一段的符号链接。
//
// 对象存储里没有符号链接，所以这里和 [Store.Stat] 看到的是同一件事，
// 它**永远不会**报 [fs.TypeSymlink]。方法照样实现，因为接缝上那些用它做
// 信任边界检查的消费方不需要知道自己挂的是哪个后端——在这里它们的检查
// 一次也不会命中，那不是错，是这个执行世界里根本没有那种东西。
func (s *Store) Lstat(ctx context.Context, path string, cwd string) (fs.PathInfo, bool, error) {
	display, err := s.resolvePath(path, cwd)
	if err != nil {
		return fs.PathInfo{}, false, err
	}

	info, found, err := s.Stat(ctx, fs.Target{
		TargetKey:   fs.TargetKey(s.keyOf(display)),
		DisplayPath: display,
	})
	if err != nil || !found {
		return fs.PathInfo{}, false, err
	}
	// 两个类型的字段逐个对得上，所以能直接转。用转换而不是逐字段抄：以后哪一边
	// 多长出一个字段，这里就编译不过，而逐字段抄会把新字段悄悄丢掉。
	return fs.PathInfo(info), true, nil
}

// ReadText 实现 [fs.FileSystem]：把整个对象读成解码好的字符串。
//
// 二进制内容报 [fs.CodeNotText]，超过文本上限报 [fs.CodeTooLarge]，
// 两个都不会交出半份内容。行尾按包文档规范化成 LF。
func (s *Store) ReadText(ctx context.Context, target fs.Target) (string, error) {
	raw, _, found, err := s.fetch(ctx, target, s.maxTextBytes)
	if err != nil {
		return "", err
	}
	if !found {
		return "", &fs.Error{Code: fs.CodeNotFound, Message: "目标不存在：" + target.DisplayPath}
	}
	if !utf8.Valid(raw) {
		return "", &fs.Error{
			Code:    fs.CodeNotText,
			Message: "目标不是合法的 UTF-8 文本：" + target.DisplayPath,
		}
	}
	return normalizeLF(string(raw)), nil
}

// ReadBytes 实现 [fs.FileSystem]：把整个对象读成原始字节。
//
// 这条路**一个字节都不碰**：不解码、不做二进制拒绝、不规范化行尾。
// 需要原样内容（图片、压缩包、要算摘要的东西）的调用方走这里。
func (s *Store) ReadBytes(ctx context.Context, target fs.Target, maxBytes int64) ([]byte, error) {
	raw, _, found, err := s.fetch(ctx, target, maxBytes)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, &fs.Error{Code: fs.CodeNotFound, Message: "目标不存在：" + target.DisplayPath}
	}
	return raw, nil
}

// fetch 把一个对象整份读进内存，最多 limit 字节。
//
// 超过 limit 报 [fs.CodeTooLarge]，**绝不返回截断的内容**：截断的那份看上去是
// 成功的，而它会被当成完整内容去算摘要、去做字面匹配。
//
// 已知大小时先按大小判一次（省掉整次传输），大小报不出时再按读到的字节数判。
// 两道都要有：第一道快，第二道是唯一靠得住的那道。
func (s *Store) fetch(ctx context.Context, target fs.Target, limit int64) ([]byte, string, bool, error) {
	key := string(target.TargetKey)
	if s.isRootKey(key) {
		// 根是目录，在这个后端上没有任何对象证明它，所以读它就是读一个不存在的对象。
		//
		// 这一支非有不可，因为世界没有前缀时根的键是**空串**，而空对象名会被 SDK
		// 当成参数错误顶回来。少了它，同一次「把根当文件读」会因为一行前缀配置的
		// 差别报出两个不同的码：有前缀时 NOT_FOUND，没前缀时 IO_ERROR。
		return nil, "", false, nil
	}

	object, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		// 这里不判「不在」：GetObject 只在参数校验上失败（桶名、对象名），
		// 那两种都是 400，不可能是一次「那个键不在」。目标在不在由下面那次
		// object.Stat 说了算——它才是真正发出去的那个请求。
		return nil, "", false, translate(err, fs.CodeIOError, "读取目标失败："+target.DisplayPath)
	}
	defer object.Close()

	info, err := object.Stat()
	if err != nil {
		if isNotFound(err) {
			return nil, "", false, nil
		}
		return nil, "", false, translate(err, fs.CodeIOError, "读取目标失败："+target.DisplayPath)
	}
	if info.Size > limit {
		return nil, "", false, tooLarge(target, info.Size, limit)
	}

	// 多读一个字节，好把「正好到上限」和「超了」分开。
	raw, err := io.ReadAll(io.LimitReader(object, limit+1))
	if err != nil {
		// 同上，这里也不判「不在」：能读到这里说明 object.Stat 已经把这个对象
		// 的响应头拿回来了，此刻再失败只可能是传输本身出了事。
		return nil, "", false, translate(err, fs.CodeIOError, "读取目标失败："+target.DisplayPath)
	}
	// 这一支在**当前这套组件**下走不到，故意留着。minio-go 在响应里解不出
	// Content-Length 时直接报 InternalError，不会退回分块读，所以上面那次
	// info.Size 判定一定成立、而且一定和实际字节数一致。
	//
	// 留着而不是删掉（连同 limit+1），是因为它守的是「服务端报的大小和实际
	// 给出的字节数不一致」。删了的话那种情况会**静默返回 limit 字节**——
	// 一份截断的内容，看上去是成功的，而它会被当成完整内容去算摘要、去做匹配。
	// 这一支响一次的代价是一条 TOO_LARGE，不响的代价是一份被悄悄改短的内容。
	if int64(len(raw)) > limit {
		return nil, "", false, tooLarge(target, -1, limit)
	}
	return raw, etagOf(info.ETag), true, nil
}

// tooLarge 造一条超限的诊断。size 为负表示读到一半才发现超了，那时候还不知道总大小。
func tooLarge(target fs.Target, size int64, limit int64) error {
	message := "目标超过了本次读取的字节上限：" + target.DisplayPath
	if size >= 0 {
		message += "（" + strconv.FormatInt(size, 10) + " > " + strconv.FormatInt(limit, 10) + "）"
	} else {
		message += "（> " + strconv.FormatInt(limit, 10) + "）"
	}
	return &fs.Error{Code: fs.CodeTooLarge, Message: message}
}

// ListDir 实现 [fs.FileSystem]：按名字顺序列出直接子项，绝不读内容。
//
// 三件在对象存储上才需要说明的事：
//
//   - 目录项来自服务端的公共前缀（`Recursive: false` 时 minio-go 把它们当成
//     以斜杠结尾的键给出来），它们不带版本也不带大小——那两样得再翻一遍
//     整棵子树才算得出来，而列目录是廉价操作。
//   - 有些工具会为目录建一个零字节的 `a/b/` 占位对象。它换算出来的名字是空串，
//     直接读掉，否则列表里会冒出一个名字是空串的项。
//   - 同一个名字可能既作为公共前缀、又作为占位对象出现两次，所以要去重。
func (s *Store) ListDir(ctx context.Context, target fs.Target) ([]fs.DirEntry, error) {
	info, found, err := s.Stat(ctx, target)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, &fs.Error{Code: fs.CodeNotFound, Message: "目标不存在：" + target.DisplayPath}
	}
	if info.Type != fs.TypeDirectory {
		return nil, &fs.Error{
			Code:    fs.CodeNotDirectory,
			Message: "目标不是目录：" + target.DisplayPath,
		}
	}

	prefix := s.dirPrefixOf(string(target.TargetKey))
	seen := make(map[string]struct{})
	entries := make([]fs.DirEntry, 0, 16)

	for object := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: false,
	}) {
		if object.Err != nil {
			return nil, translate(object.Err, fs.CodeIOError, "列举目录失败："+target.DisplayPath)
		}

		isDirectory := strings.HasSuffix(object.Key, "/")
		name := strings.TrimSuffix(strings.TrimPrefix(object.Key, prefix), "/")
		if name == "" || strings.Contains(name, "/") {
			// 空串是那个目录占位对象自己；带斜杠的说明这次列举不是我们要的那一层，
			// 两种都不是这个目录的直接子项。
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}

		childKey := prefix + name
		entry := fs.DirEntry{
			Name: name,
			Type: fs.TypeFile,
			Target: fs.Target{
				TargetKey:   fs.TargetKey(childKey),
				DisplayPath: s.displayOf(childKey),
			},
		}
		if isDirectory {
			entry.Type = fs.TypeDirectory
		} else {
			size := object.Size
			entry.Size = &size
			entry.Version = fs.Version(etagOf(object.ETag))
		}
		entries = append(entries, entry)
	}

	// 服务端按键的字典序给，去重和目录后缀处理之后顺序可能被打乱，所以自己排一次。
	// 「稳定顺序」是接缝的硬要求：顺序变了，一次列目录的结果就没法拿来比对差异。
	slices.SortFunc(entries, func(left, right fs.DirEntry) int {
		return strings.Compare(left.Name, right.Name)
	})
	return entries, nil
}

// StreamText 实现 [fs.FileSystem]：把对象按解码好的文本块流出来。
//
// 语义和 [Store.ReadText] 完全一致，区别只在内容是分块交出去的。
// 三件事由这里负责，于是上层一个字节都不用碰：
//
//   - **跨块的 UTF-8 解码**：一个 rune 被切在两块之间时，尾巴留到下一块再解。
//   - **跨块的行尾规范化**：CRLF 被切开时同理，末尾的 CR 要压着等下一个字节。
//   - **二进制拒绝**和字节上限，判据和 ReadText 逐字相同。
//
// 「在不在」先用一次 HEAD 问掉，这样一个不存在的目标是**外层**的错误，
// 调用方不用先建一个迭代器再发现它是空的。真正的 GET 推迟到第一次迭代——
// 拿到迭代器却从不遍历是很平常的写法，那时候不该有一条连接挂在那里。
func (s *Store) StreamText(ctx context.Context, target fs.Target) (iter.Seq2[string, error], error) {
	info, found, err := s.Stat(ctx, target)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, &fs.Error{Code: fs.CodeNotFound, Message: "目标不存在：" + target.DisplayPath}
	}
	if info.Type != fs.TypeFile {
		return nil, &fs.Error{
			Code:    fs.CodeNotRegularFile,
			Message: "目标不是常规文件：" + target.DisplayPath,
		}
	}

	return func(yield func(string, error) bool) {
		// 这一支走不到：GetObject 只在桶名或对象名不合法时失败，而上面那次
		// Stat 已经确认这个键是一个存在的常规文件——空对象名过不了那一关。
		// 照样接住，是因为忽略一个 error 会让「这里为什么可以不处理」变成
		// 一件要靠读三个函数才能重建的事，而它随 SDK 的一次改动就会失效。
		object, err := s.client.GetObject(ctx, s.bucket, string(target.TargetKey), minio.GetObjectOptions{})
		if err != nil {
			yield("", translate(err, fs.CodeIOError, "读取目标失败："+target.DisplayPath))
			return
		}
		defer object.Close()

		var decoder textDecoder
		buffer := make([]byte, s.chunkBytes)
		var total int64

		for {
			// 取消在**块与块之间**也要生效：一个大对象读到一半被取消时，
			// 迭代必须停下来，而不是把剩下的读完再说。
			if err := ctx.Err(); err != nil {
				yield("", translate(err, fs.CodeAborted, "读取被取消："+target.DisplayPath))
				return
			}

			read, err := object.Read(buffer)
			if read > 0 {
				total += int64(read)
				if total > s.maxTextBytes {
					yield("", tooLarge(target, -1, s.maxTextBytes))
					return
				}
				text, decodeErr := decoder.push(buffer[:read])
				if decodeErr != nil {
					yield("", notText(target))
					return
				}
				if text != "" && !yield(text, nil) {
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					yield("", translate(err, fs.CodeIOError, "读取目标失败："+target.DisplayPath))
					return
				}
				text, flushErr := decoder.flush()
				if flushErr != nil {
					yield("", notText(target))
					return
				}
				if text != "" {
					yield(text, nil)
				}
				return
			}
		}
	}, nil
}

// notText 造一条「这不是文本」的诊断。
func notText(target fs.Target) error {
	return &fs.Error{
		Code:    fs.CodeNotText,
		Message: "目标不是合法的 UTF-8 文本：" + target.DisplayPath,
	}
}

// textDecoder 是一个流式的 UTF-8 解码加行尾规范化器。
//
// 它压着两种「还不能交出去」的尾巴：
//
//   - 一个被切开的 rune 的前几个字节。直接解的话会解出替换字符，
//     于是一份完好的中文文件被报成二进制。
//   - 一个末尾的 CR。下一个字节是 LF 的话这是一次 CRLF 要折成 LF，
//     不是的话它就是个普通的 CR。先交出去就晚了。
type textDecoder struct {
	carry []byte
}

// push 吃进一块原始字节，交出可以立刻给出去的那段文本。
func (d *textDecoder) push(raw []byte) (string, error) {
	buffer := raw
	if len(d.carry) > 0 {
		buffer = append(d.carry, raw...)
	}

	complete, carry := splitAtRuneBoundary(buffer)
	if !utf8.Valid(complete) {
		return "", errNotUTF8
	}
	// 末尾的 CR 要压到下一块，理由见类型注释。
	if len(complete) > 0 && complete[len(complete)-1] == '\r' {
		complete = complete[:len(complete)-1]
		carry = append([]byte{'\r'}, carry...)
	}

	d.carry = slices.Clone(carry)
	return normalizeLF(string(complete)), nil
}

// flush 在流结束时把压着的尾巴交出去。
//
// 这时候还压着一个不完整的 rune，说明这个对象的最后一个字符是残缺的——
// 那就是二进制，不是文本。
func (d *textDecoder) flush() (string, error) {
	if len(d.carry) == 0 {
		return "", nil
	}
	if !utf8.Valid(d.carry) {
		return "", errNotUTF8
	}
	text := normalizeLF(string(d.carry))
	d.carry = nil
	return text, nil
}

// splitAtRuneBoundary 把一段字节切成「整 rune 的前缀」和「尾巴」。
//
// 从末尾往回最多看 [utf8.UTFMax] 个字节找一个起始字节：那个 rune 在这段里齐了，
// 整段就都是完整的；没齐，就从它开始留给下一块。
func splitAtRuneBoundary(buffer []byte) (complete []byte, carry []byte) {
	for back := 1; back <= utf8.UTFMax && back <= len(buffer); back++ {
		at := len(buffer) - back
		size := runeLengthAt(buffer[at])
		if size == 0 {
			continue // continuation 字节，继续往回找。
		}
		if at+size <= len(buffer) {
			return buffer, nil // 最后一个 rune 是齐的。
		}
		return buffer[:at], buffer[at:]
	}
	return buffer, nil
}

// runeLengthAt 按 UTF-8 的起始字节给出这个 rune 一共几个字节；不是起始字节时给 0。
func runeLengthAt(first byte) int {
	switch {
	case first&0x80 == 0x00:
		return 1
	case first&0xE0 == 0xC0:
		return 2
	case first&0xF0 == 0xE0:
		return 3
	case first&0xF8 == 0xF0:
		return 4
	default:
		return 0
	}
}

// normalizeLF 把 CRLF 折成 LF，见包文档。
//
// 单独的 CR **不动**：它在老 Mac 文本里是行尾，但在今天更可能是数据。
// 一条规则只做一件确定的事，比一条覆盖面更广但会改坏数据的规则好。
func normalizeLF(text string) string {
	if !strings.Contains(text, "\r\n") {
		return text
	}
	return strings.ReplaceAll(text, "\r\n", "\n")
}
