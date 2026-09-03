// 本文件是命名空间那三个原语：MakeDir / Remove / RemoveTree，外加字节写 WriteBytes。
//
// 新增: 这四个在 DSH 那边一个都没有。它们是本仓库把第二条内容读写接缝
// （原 preset/agentpresets.Store）并进 [fs.FileSystem] 时补上的缺口，理由见
// fs 包文档。这个后端上它们的形状被「键空间是平的」这一条决定了：
// 没有真目录，于是建目录什么都不做；删一棵子树只能靠前缀列举逐个删。

package objectstore

import (
	"bytes"
	"context"
	"errors"

	"github.com/minio/minio-go/v7"

	"github.com/snight1983/ds-harness-go/fs"
)

// WriteBytes 实现 [fs.FileSystem]：原子地创建或替换原始字节。
//
// 三种守卫和 [Store.WriteText] 走同一套条件头，区别只在两处：
//
//   - 内容一个字节都不碰。文本那条路进出两侧都把 CRLF 折成 LF（见包文档），
//     那对一份二进制内容是破坏性的。
//   - 带守卫的替换**不先读一遍**。文本那条路先读是为了 [fs.WriteOutcome.Before]
//     那个 diff 基准，而这里交回的只有版本，没有基准可给，那次读就是白花的一趟。
//     守卫本身一直靠的是 PUT 上的 `If-Match`，不是那次读。
func (s *Store) WriteBytes(ctx context.Context, target fs.Target, content []byte, expected fs.WriteIntent) (fs.Version, error) {
	options := minio.PutObjectOptions{ContentType: "application/octet-stream"}

	// 412 在两种守卫上是两件不同的事，所以码在这里就定下来，不放到错误处理里再猜。
	conflict := fs.CodeIOError
	message := ""
	switch intent := expected.(type) {
	case fs.CreateIfAbsent:
		options.SetMatchETagExcept("*")
		conflict, message = fs.CodeNotObserved, "目标已经存在，这次写要求的是一次创建："+target.DisplayPath
	case fs.ReplaceIfVersion:
		options.SetMatchETag(string(intent.Version))
		conflict, message = fs.CodeStaleVersion, "目标不在或者在这次写之前被改过："+target.DisplayPath
	case nil:
	default:
		// [fs.WriteIntent] 是封印接口，本包外面造不出第三种实现，所以这条走不到。
		// 理由同 [Store.WriteText] 那一处。
		return "", &fs.Error{
			Code:    fs.CodeIOError,
			Message: "不认识的写意图，这个后端没有为它准备条件头",
		}
	}

	uploaded, err := s.client.PutObject(
		ctx, s.bucket, string(target.TargetKey),
		bytes.NewReader(content), int64(len(content)), options)
	if err != nil {
		if message != "" && isPreconditionFailed(err) {
			return "", &fs.Error{Code: conflict, Message: message, Err: err}
		}
		return "", translate(err, fs.CodeIOError, "写入目标失败："+target.DisplayPath)
	}
	return fs.Version(etagOf(uploaded.ETag)), nil
}

// MakeDir 实现 [fs.FileSystem]：在这个后端上**什么都不做**，只把路径解析出来交回。
//
// 键空间是平的，一个「目录」是推断出来的（有别的对象以它加斜杠开头，见包文档），
// 所以这里没有任何东西可建：接下来那次 [Store.WriteText] 或 [Store.WriteBytes]
// 本来就不要求上级先存在，写完那一刻这个目录自动就在了。
//
// 刻意**不**建一个零字节的 `a/b/` 占位对象。那种键在这个后端上是垃圾：
// [Store.Stat] 判目录靠的是有没有子项，占位对象会让一个已经被清空的目录
// 永远显示成还在；而 [Store.ListDir] 又得专门把它读掉，免得列出一个名字是空串的项。
func (s *Store) MakeDir(_ context.Context, path string, cwd string) (fs.Target, error) {
	display, err := s.resolvePath(path, cwd)
	if err != nil {
		return fs.Target{}, err
	}
	return fs.Target{TargetKey: fs.TargetKey(s.keyOf(display)), DisplayPath: display}, nil
}

// Remove 实现 [fs.FileSystem]：删掉单个目标；不在不算错。
//
// 它只删这一个键，**不会**顺手把以它加斜杠开头的那些删掉——那是
// [Store.RemoveTree] 的事。两件事分开，是因为调用方想删的到底是「这一份内容」
// 还是「这一整棵」在这个后端上分不出来（一个键既可以是对象又可以是别人的前缀），
// 只有调用方自己知道。
func (s *Store) Remove(ctx context.Context, target fs.Target) error {
	key := string(target.TargetKey)
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		if isNotFound(err) {
			return nil
		}
		return translate(err, fs.CodeIOError, "删除目标失败："+target.DisplayPath)
	}
	return nil
}

// RemoveTree 实现 [fs.FileSystem]：删掉一棵子树；不在不算错。
//
// 走前缀列举逐个删，最后把目标自己那个键也删一次——一条路径在这个后端上
// 可以同时是一个对象和别人的前缀，漏掉那一次的话 `a` 这个文件会在
// 「删掉 a 这棵树」之后还在。
//
// 半途删不掉的**不当场停下**：这个方法是「一次失败的写入什么都不留下」那条撤销路
// 唯一的依靠，停在中间会留下一半，而调用方拿到错误之后通常已经没有别的手段清场了。
// 所以继续往下删，最后把攒起来的失败一并报出来。
func (s *Store) RemoveTree(ctx context.Context, target fs.Target) error {
	key := string(target.TargetKey)

	var failures []error
	for object := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix:    s.dirPrefixOf(key),
		Recursive: true,
	}) {
		if object.Err != nil {
			failures = append(failures, object.Err)
			continue
		}
		if err := s.client.RemoveObject(ctx, s.bucket, object.Key, minio.RemoveObjectOptions{}); err != nil && !isNotFound(err) {
			failures = append(failures, err)
		}
	}
	if err := s.Remove(ctx, target); err != nil {
		failures = append(failures, err)
	}

	if len(failures) > 0 {
		return translate(errors.Join(failures...), fs.CodeIOError, "删除子树失败："+target.DisplayPath)
	}
	return nil
}
