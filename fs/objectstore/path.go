// 本文件是路径和对象键之间的换算：世界根怎么定、一条路径怎么规范化成
// 一个稳定的键、以及为什么这一步不需要任何 I/O。

package objectstore

import (
	"context"
	"path"
	"strings"

	"github.com/snight1983/ds-harness-go/fs"
)

// normalizePrefix 把用户给的世界根前缀折成内部形式：空串，或者以斜杠结尾。
//
// 以斜杠结尾这一点是 keyOf 拼键的前提。前后多余的斜杠一律吃掉，
// 于是 "w1"、"/w1"、"w1/"、"//w1//" 落到同一个世界——否则同一份配置写法不同
// 会开出几个互不可见的世界，而运维在两次部署之间是看不出这件事的。
func normalizePrefix(prefix string) string {
	trimmed := strings.Trim(prefix, "/")
	if trimmed == "" {
		return ""
	}
	return trimmed + "/"
}

// Resolve 实现 [fs.FileSystem]：把一条路径解析成稳定的 [fs.Target]。
//
// 这里**不做 I/O**，理由见包文档：对象存储没有符号链接，一个对象只有一个键，
// 所以「同一个文件从哪条路径走到都得出同一个 key」这条约定，
// 靠纯粹的路径规范化就已经成立了。
//
// 目标**不存在也照样解析得出来**，这是接缝要的：[fs.CreateIfAbsent] 那条路上
// 调用方必须先拿到一个还不存在的目标的 [fs.Target] 才写得下去。
// 「在不在」是 [Store.Stat] 回答的问题。
//
// cwd 是相对路径的基准，留空表示世界根。走到根上面去（`..` 太多）报
// [fs.CodeNotFound]——从这个世界的角度看那里什么都没有；用
// [fs.CodePermissionDenied] 会让人以为是一条可以靠改权限打开的路。
func (s *Store) Resolve(_ context.Context, target string, cwd string) (fs.Target, error) {
	display, err := s.resolvePath(target, cwd)
	if err != nil {
		return fs.Target{}, err
	}
	return fs.Target{TargetKey: fs.TargetKey(s.keyOf(display)), DisplayPath: display}, nil
}

// resolvePath 把 (路径, 基准) 规范化成一条世界绝对路径，形如 "/" 或者 "/a/b.txt"。
//
// 单独抽出来，是因为 Lstat 也要走同一套换算，而两边任何一点不一致
// 都会让「先 Lstat 再 Resolve」这个再平常不过的用法看到两个不同的东西。
func (s *Store) resolvePath(target string, cwd string) (string, error) {
	base := "/"
	if cwd != "" {
		base = path.Clean("/" + strings.ReplaceAll(cwd, "\\", "/"))
	}

	cleaned := strings.ReplaceAll(target, "\\", "/")
	joined := cleaned
	if !strings.HasPrefix(cleaned, "/") {
		joined = base + "/" + cleaned
	}

	// path.Clean 会把 ".." 逐段吃掉，走到根上面时结果停在 "/"——
	// 那意味着信息丢了，所以越界要在 Clean **之前**判，不能看结果。
	if escapesRoot(joined) {
		return "", &fs.Error{
			Code:    fs.CodeNotFound,
			Message: "路径走到了这个执行世界的根上面：" + target,
		}
	}
	return path.Clean(joined), nil
}

// escapesRoot 判断一条以斜杠开头的路径在逐段走完之后有没有跑到根上面去。
//
// 自己数而不是靠 path.Clean 的结果，是因为 Clean("/../x") 给的是 "/x"——
// 它把越界这件事**修好了**，于是 `../../别人的世界/秘密.txt` 会解析成
// 本世界里的一个普通目标，而不是被拒掉。
func escapesRoot(absolute string) bool {
	depth := 0
	for segment := range strings.SplitSeq(strings.TrimPrefix(absolute, "/"), "/") {
		switch segment {
		case "", ".":
		case "..":
			depth--
			if depth < 0 {
				return true
			}
		default:
			depth++
		}
	}
	return false
}

// keyOf 把一条世界绝对路径换成桶里的对象键。
//
// 世界根（"/"）换出来的是 prefix 本身，可能是空串。那不是一个对象键，
// 而是一个目录键——Stat 和 ListDir 都把它当目录处理。
func (s *Store) keyOf(display string) string {
	return s.prefix + strings.TrimPrefix(display, "/")
}

// displayOf 是 keyOf 的逆向：把一个桶里的键换回世界绝对路径。
//
// 只用在 ListDir 上：那里拿到的是服务端给的键，得换回展示路径才能装进
// [fs.Target]。键不在本世界前缀下时给出空串，调用方据此跳过——
// 那种键不该出现，但一次配置改动（prefix 改了、桶里混进了别的世界）
// 就能让它出现，而把它当成本世界的东西展示出去是更坏的结果。
func (s *Store) displayOf(key string) string {
	rest, ok := strings.CutPrefix(key, s.prefix)
	if !ok {
		return ""
	}
	return "/" + strings.TrimSuffix(rest, "/")
}

// dirPrefixOf 给出「列这个目录的直接子项」要用的键前缀，一定以斜杠结尾。
//
// 世界根的前缀就是 prefix 本身（可能是空串），不能再拼一个斜杠上去——
// 拼了的话前缀会变成 "/"，而桶里没有任何键以斜杠开头，于是根目录永远列出空。
func (s *Store) dirPrefixOf(key string) string {
	if key == "" || strings.HasSuffix(key, "/") {
		return key
	}
	return key + "/"
}
