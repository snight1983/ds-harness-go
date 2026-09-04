// 本文件是写的那两个原语：WriteText 和 EditText，外加一次服务端能力自测。
//
// 两个方法的守卫都落在**服务端**：`If-None-Match: *` 保证不覆盖，
// `If-Match: <etag>` 保证只落在读到的那一份上。客户端这边不做任何
// 「先探测再写」——那种写法下两个都以为自己在创建的写会有一个覆盖掉另一个，
// 而两次都报成功。

package objectstore

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/minio/minio-go/v7"

	"github.com/snight1983/ds-harness-go/fs"
)

// probeKeyName 是 [Store.VerifyCreateIfAbsent] 用的保留键名。
//
// 点开头、名字写死，为的是它一眼看得出不是用户数据；自测跑完就删掉。
const probeKeyName = ".ds-harness-go-conditional-write-probe"

// WriteText 实现 [fs.FileSystem]：原子地创建或替换 UTF-8 文本。
//
// expected 的三种取值对应三条不同的路，区别全在**带哪个条件头**上：
//
//   - nil：不带条件头。去掉的是版本前置条件，不是原子性——一次 PUT 本身
//     就是原子发布的，读的人要么看到旧的整份、要么看到新的整份。
//   - [fs.CreateIfAbsent]：带 `If-None-Match: *`，已存在时服务端拒，
//     报 [fs.CodeNotObserved]。
//   - [fs.ReplaceIfVersion]：带 `If-Match: <版本>`，对不上时服务端拒，
//     报 [fs.CodeStaleVersion]。
func (s *Store) WriteText(ctx context.Context, target fs.Target, content string, expected fs.WriteIntent) (fs.WriteOutcome, error) {
	after := normalizeLF(content)

	switch intent := expected.(type) {
	case fs.CreateIfAbsent:
		return s.createIfAbsent(ctx, target, after)
	case fs.ReplaceIfVersion:
		return s.replaceIfVersion(ctx, target, after, intent.Version)
	case nil:
		return s.writeUnconditionally(ctx, target, after)
	default:
		// [fs.WriteIntent] 是封印接口，本包外面造不出第三种实现，所以这条走不到。
		// 留着而不是省掉 default，是因为 fs 包**以后**加了第三支的话，
		// 这里必须是一次编译期就该处理、运行期一定会响的地方，而不是静默当成无条件写。
		return fs.WriteOutcome{}, &fs.Error{
			Code:    fs.CodeIOError,
			Message: "不认识的写意图，这个后端没有为它准备条件头",
		}
	}
}

// createIfAbsent 走 `If-None-Match: *` 那条路。
//
// Before 一定是 nil：这次写要么创建了一个此前不存在的文件（没有基准），
// 要么根本没写成。不去读一遍旧内容，也是为了不给「先探测再写」留任何形状。
func (s *Store) createIfAbsent(ctx context.Context, target fs.Target, after string) (fs.WriteOutcome, error) {
	options := minio.PutObjectOptions{ContentType: "text/plain; charset=utf-8"}
	options.SetMatchETagExcept("*")

	version, err := s.put(ctx, target, after, options)
	if err != nil {
		if isPreconditionFailed(err) {
			return fs.WriteOutcome{}, &fs.Error{
				Code:    fs.CodeNotObserved,
				Message: "目标已经存在，这次写要求的是一次创建：" + target.DisplayPath,
				Err:     err,
			}
		}
		return fs.WriteOutcome{}, translate(err, fs.CodeIOError, "写入目标失败："+target.DisplayPath)
	}
	return fs.WriteOutcome{Operation: fs.OperationCreate, Version: version, After: after}, nil
}

// replaceIfVersion 走 `If-Match: <版本>` 那条路。
//
// 先读一次基准，为的是 [fs.WriteOutcome.Before]——调用方要拿它和 After 算 diff。
// 这次读**顺手**把版本对了一遍，于是「版本早就不对了」能在发出 PUT 之前就报出来，
// 诊断里带得上真实的当前版本。但守卫本身不靠这次读：真正拦住并发写的是
// PUT 上那个 `If-Match`，读和写之间有人插进来的话，服务端会以 412 拒掉。
func (s *Store) replaceIfVersion(ctx context.Context, target fs.Target, after string, want fs.Version) (fs.WriteOutcome, error) {
	before, current, found, err := s.baseline(ctx, target)
	if err != nil {
		return fs.WriteOutcome{}, err
	}
	if !found {
		return fs.WriteOutcome{}, &fs.Error{
			Code:    fs.CodeStaleVersion,
			Message: "目标不在，没有版本可以对上：" + target.DisplayPath,
		}
	}
	if current != string(want) {
		return fs.WriteOutcome{}, &fs.Error{
			Code:    fs.CodeStaleVersion,
			Message: "目标在这次写之前被改过：" + target.DisplayPath,
		}
	}

	options := minio.PutObjectOptions{ContentType: "text/plain; charset=utf-8"}
	options.SetMatchETag(current)

	version, err := s.put(ctx, target, after, options)
	if err != nil {
		if isPreconditionFailed(err) {
			return fs.WriteOutcome{}, &fs.Error{
				Code:    fs.CodeStaleVersion,
				Message: "目标在读到写之间被改过：" + target.DisplayPath,
				Err:     err,
			}
		}
		return fs.WriteOutcome{}, translate(err, fs.CodeIOError, "写入目标失败："+target.DisplayPath)
	}
	return fs.WriteOutcome{
		Operation: fs.OperationUpdate,
		Version:   version,
		Before:    before,
		After:     after,
	}, nil
}

// writeUnconditionally 走不带条件头的那条路。
//
// Operation 这个字段在这条路上是**尽力而为**的：它由写之前那次读得出，
// 而读和写之间别人可以插进来。写明这一点，是因为不带守卫的写本来就没有
// 「这中间没人动过」这个前提——想要那个前提的调用方该带守卫。
// 原子性不受影响：那是 PUT 自己的性质，和这个标签无关。
func (s *Store) writeUnconditionally(ctx context.Context, target fs.Target, after string) (fs.WriteOutcome, error) {
	before, _, found, err := s.baseline(ctx, target)
	if err != nil {
		return fs.WriteOutcome{}, err
	}

	version, err := s.put(ctx, target, after, minio.PutObjectOptions{
		ContentType: "text/plain; charset=utf-8",
	})
	if err != nil {
		return fs.WriteOutcome{}, translate(err, fs.CodeIOError, "写入目标失败："+target.DisplayPath)
	}

	operation := fs.OperationCreate
	if found {
		operation = fs.OperationUpdate
	}
	return fs.WriteOutcome{Operation: operation, Version: version, Before: before, After: after}, nil
}

// baseline 读一次写之前的内容，给 [fs.WriteOutcome.Before] 用。
//
// 三个返回值分别是：可以当基准的文本（拿不到时 nil）、当前版本、目标在不在。
//
// 「读到了内容但给不出基准」是接缝明确允许的：之前那份是二进制、或者超过了
// 文本上限时，返回 nil 而**不是**报错——那次写本身没有任何问题，
// 只是这次结果里没有 diff 基准，调用方会退回整文件 diff。
// 把它报成错误的话，一次完全正常的覆盖会因为旧内容是二进制而失败。
func (s *Store) baseline(ctx context.Context, target fs.Target) (*string, string, bool, error) {
	raw, version, found, err := s.fetch(ctx, target, s.maxTextBytes)
	if err != nil {
		var failure *fs.Error
		if errors.As(err, &failure) && failure.Code == fs.CodeTooLarge {
			// 太大：给不出基准，但目标确实在。版本还得拿到，否则带守卫的替换
			// 会因为一个和内容无关的理由失败。
			info, exists, statErr := s.Stat(ctx, target)
			if statErr != nil {
				return nil, "", false, statErr
			}
			return nil, string(info.Version), exists, nil
		}
		return nil, "", false, err
	}
	if !found {
		return nil, "", false, nil
	}
	if !utf8.Valid(raw) {
		return nil, version, true, nil
	}
	text := normalizeLF(string(raw))
	return &text, version, true, nil
}

// put 发一次 PUT，把新版本带回来。
func (s *Store) put(ctx context.Context, target fs.Target, content string, options minio.PutObjectOptions) (fs.Version, error) {
	uploaded, err := s.client.PutObject(
		ctx, s.bucket, string(target.TargetKey),
		strings.NewReader(content), int64(len(content)), options)
	if err != nil {
		return "", err
	}
	return fs.Version(etagOf(uploaded.ETag)), nil
}

// EditText 实现 [fs.FileSystem]：原子地做一次字面文本替换。
//
// 接缝要求「校验版本、匹配字面、重写」三步在同一个临界区里完成。对象存储上
// 没有临界区可用，这里用乐观并发做到等价的效果：读的时候记下 ETag，
// 写的时候带 `If-Match: <那个 ETag>`。中间有人写过，这次 PUT 就被服务端拒掉，
// 报 [fs.CodeStaleVersion]——编辑绝不会落在一份已经不存在的内容上。
//
// 版本校验在**匹配之前**：内容陈旧时报的是 [fs.CodeStaleVersion] 而不是
// [fs.CodeEditNotFound]，否则调用方会以为是自己的搜索串写错了，
// 换个串再试一次，而它每一次都在改别人刚写下的内容。
func (s *Store) EditText(ctx context.Context, target fs.Target, edit fs.EditRequest, expected *fs.EditIntent) (fs.EditOutcome, error) {
	raw, current, found, err := s.fetch(ctx, target, s.maxTextBytes)
	if err != nil {
		return fs.EditOutcome{}, err
	}
	if !found {
		// 编辑一个不在的目标报陈旧而不是不存在：这条路上「不在」就是
		// 「你看到的那一份已经没了」，和版本对不上是同一件事。
		return fs.EditOutcome{}, &fs.Error{
			Code:    fs.CodeStaleVersion,
			Message: "目标不在，没有内容可以编辑：" + target.DisplayPath,
		}
	}
	if expected != nil && current != string(expected.Version) {
		return fs.EditOutcome{}, &fs.Error{
			Code:    fs.CodeStaleVersion,
			Message: "目标在这次编辑之前被改过：" + target.DisplayPath,
		}
	}
	if !utf8.Valid(raw) {
		return fs.EditOutcome{}, notText(target)
	}

	before := normalizeLF(string(raw))
	after, err := applyEdit(before, edit, target)
	if err != nil {
		return fs.EditOutcome{}, err
	}

	options := minio.PutObjectOptions{ContentType: "text/plain; charset=utf-8"}
	options.SetMatchETag(current)

	version, err := s.put(ctx, target, after, options)
	if err != nil {
		if isPreconditionFailed(err) {
			return fs.EditOutcome{}, &fs.Error{
				Code:    fs.CodeStaleVersion,
				Message: "目标在读到写之间被改过：" + target.DisplayPath,
				Err:     err,
			}
		}
		return fs.EditOutcome{}, translate(err, fs.CodeIOError, "写入目标失败："+target.DisplayPath)
	}
	return fs.EditOutcome{Version: version, Before: before, After: after}, nil
}

// applyEdit 在一份已经规范化过的文本上做一次字面替换。
//
// 匹配数是硬判据：零处报 [fs.CodeEditNotFound]，多处而没说 ReplaceAll 报
// [fs.CodeAmbiguousEdit]。「多处就改第一处」是不行的——调用方会以为改完了，
// 而剩下的几处还是旧的。
func applyEdit(before string, edit fs.EditRequest, target fs.Target) (string, error) {
	if edit.OldString == "" {
		// 空串在每一个位置都匹配，包括长度为零的那些位置。这是调用方的 bug，
		// 但它落在「有歧义」这个码上是准的：这次编辑没法确定要改哪儿。
		return "", &fs.Error{
			Code:    fs.CodeAmbiguousEdit,
			Message: "要被替换的字面文本是空串，它在每一个位置都匹配：" + target.DisplayPath,
		}
	}

	matches := strings.Count(before, edit.OldString)
	switch {
	case matches == 0:
		return "", &fs.Error{
			Code:    fs.CodeEditNotFound,
			Message: "在目标里找不到要替换的字面文本：" + target.DisplayPath,
		}
	case matches > 1 && !edit.ReplaceAll:
		return "", &fs.Error{
			Code:    fs.CodeAmbiguousEdit,
			Message: "要替换的字面文本在目标里匹配到多处：" + target.DisplayPath,
		}
	}

	if edit.ReplaceAll {
		return strings.ReplaceAll(before, edit.OldString, edit.NewString), nil
	}
	return strings.Replace(before, edit.OldString, edit.NewString, 1), nil
}

// VerifyCreateIfAbsent 自测服务端认不认 `If-None-Match: *`。
//
// 它拿一个保留键写两次：第一次该成，第二次该被服务端以 412 拒掉。
// 第二次也成了，说明服务端把那个头**当不存在**——那是一次失败开放，
// 而客户端在响应里看不出来（创建和覆盖返回的东西一模一样），
// 于是 [fs.CreateIfAbsent] 会静默退化成无条件覆盖。
//
// 已知会这样的服务端：MinIO 早于 RELEASE.2024-09-13 的版本。AWS S3 一直是认的。
//
// 这个方法**不在** [New] 里自动跑：构造函数不该偷偷往桶里写对象。
// 由部署方在启动自检里、或者集成测试里显式调一次。
func (s *Store) VerifyCreateIfAbsent(ctx context.Context) error {
	key := s.prefix + probeKeyName
	target := fs.Target{TargetKey: fs.TargetKey(key), DisplayPath: "/" + probeKeyName}

	// 先清一次场：上一轮自测被打断的话，这个键可能还留着。
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil && !isNotFound(err) {
		return translate(err, fs.CodeIOError, "清理条件写自测的保留键失败")
	}
	defer func() {
		// 自测键不是用户数据，删不掉也不该让自测的结论变掉，所以这里吞掉错误。
		_ = s.client.RemoveObject(context.WithoutCancel(ctx), s.bucket, key, minio.RemoveObjectOptions{})
	}()

	if _, err := s.createIfAbsent(ctx, target, "probe"); err != nil {
		return &fs.Error{
			Code:    fs.CodeIOError,
			Message: "条件写自测的第一次写就失败了，这个桶可能不可写",
			Err:     err,
		}
	}

	_, err := s.createIfAbsent(ctx, target, "probe-again")
	if err == nil {
		return &fs.Error{
			Code: fs.CodeIOError,
			Message: "这个服务端不认 If-None-Match: *，CreateIfAbsent 会静默退化成覆盖。" +
				"MinIO 需要 RELEASE.2024-09-13 或更新的版本",
		}
	}

	var failure *fs.Error
	if errors.As(err, &failure) && failure.Code == fs.CodeNotObserved {
		return nil // 服务端认这个头，守卫是真的。
	}
	return err
}
