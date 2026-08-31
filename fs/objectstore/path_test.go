// 本文件验路径与对象键之间的换算，以及那两个 panic 的方法。
//
// 这一组**一次网络都不发**：换算是纯函数，这正是把它单独抽出来的意义。

package objectstore

import (
	"errors"
	"strings"
	"testing"

	"ds-harness-go/fs"
)

// requireCode 断言 err 是一个 *[fs.Error] 并且带着期望的那个码。
//
// 只看 Code 不看 Message：分派看的是码，用例也就只钉码。
func requireCode(t *testing.T, err error, want fs.ErrorCode) {
	t.Helper()

	var failure *fs.Error
	if !errors.As(err, &failure) {
		t.Fatalf("该是一个 *fs.Error，实际 %#v", err)
	}
	if failure.Code != want {
		t.Fatalf("该报 %s，实际 %s（%s）", want, failure.Code, failure.Message)
	}
}

// newStore 建一个不连任何服务端的后端，专给纯换算用例用。
func newStore(t *testing.T, prefix string) *Store {
	t.Helper()

	store, err := New(Config{
		Endpoint:  "example.invalid",
		Bucket:    "world",
		Prefix:    prefix,
		AccessKey: "key",
		SecretKey: "secret",
	})
	if err != nil {
		t.Fatalf("建后端不该失败：%v", err)
	}
	return store
}

// TestNewRejectsAConfigThatNamesNoWorld 验少了端点或桶时构造就失败。
//
// 这两样缺一个都定位不到任何东西，让它在装配阶段响，比让第一次读失败要好。
func TestNewRejectsAConfigThatNamesNoWorld(t *testing.T) {
	t.Parallel()

	if _, err := New(Config{Bucket: "world"}); err == nil {
		t.Fatal("没有 Endpoint 该建不出来")
	}
	if _, err := New(Config{Endpoint: "example.invalid"}); err == nil {
		t.Fatal("没有 Bucket 该建不出来")
	}
}

// TestNewFillsInTheDefaultLimits 验两个上限在不填时落到内置默认值。
func TestNewFillsInTheDefaultLimits(t *testing.T) {
	t.Parallel()

	store := newStore(t, "")
	if store.maxTextBytes != defaultMaxTextBytes {
		t.Fatalf("文本上限该是 %d，实际 %d", defaultMaxTextBytes, store.maxTextBytes)
	}
	if store.chunkBytes != defaultChunkBytes {
		t.Fatalf("块大小该是 %d，实际 %d", defaultChunkBytes, store.chunkBytes)
	}

	custom, err := New(Config{
		Endpoint:     "example.invalid",
		Bucket:       "world",
		MaxTextBytes: 7,
		ChunkBytes:   9,
	})
	if err != nil {
		t.Fatalf("建后端不该失败：%v", err)
	}
	if custom.maxTextBytes != 7 || custom.chunkBytes != 9 {
		t.Fatalf("填了的值该被用上，实际 %d / %d", custom.maxTextBytes, custom.chunkBytes)
	}
}

// TestPrefixWritingsAllLandInTheSameWorld 验四种写法折成同一个内部前缀。
//
// 这条不是洁癖：写法不同就开出几个互不可见的世界，而那件事在两次部署之间
// 是看不出来的——库里多了一份数据，没有任何报错。
func TestPrefixWritingsAllLandInTheSameWorld(t *testing.T) {
	t.Parallel()

	for _, written := range []string{"w1", "/w1", "w1/", "//w1//"} {
		if got := normalizePrefix(written); got != "w1/" {
			t.Fatalf("%q 该折成 \"w1/\"，实际 %q", written, got)
		}
	}
	if got := normalizePrefix(""); got != "" {
		t.Fatalf("空前缀该还是空串，实际 %q", got)
	}
	if got := normalizePrefix("///"); got != "" {
		t.Fatalf("只有斜杠也该折成空串，实际 %q", got)
	}
}

// TestResolveNormalizesWithoutAnyIO 验解析的换算规则。
//
// 建这个后端的端点是 example.invalid，任何一次网络往返都会失败——
// 用例能过本身就是「解析不做 I/O」的证据。
func TestResolveNormalizesWithoutAnyIO(t *testing.T) {
	t.Parallel()

	store := newStore(t, "w1")

	cases := []struct {
		name        string
		path        string
		cwd         string
		wantDisplay string
		wantKey     string
	}{
		{"绝对路径", "/a/b.txt", "", "/a/b.txt", "w1/a/b.txt"},
		{"相对路径接在 cwd 上", "b.txt", "/a", "/a/b.txt", "w1/a/b.txt"},
		{"点段被吃掉", "/a/./b.txt", "", "/a/b.txt", "w1/a/b.txt"},
		{"双点回上一层", "/a/c/../b.txt", "", "/a/b.txt", "w1/a/b.txt"},
		{"反斜杠当分隔符", `\a\b.txt`, "", "/a/b.txt", "w1/a/b.txt"},
		{"世界根", "/", "", "/", "w1/"},
		{"空路径落在 cwd 上", "", "/a", "/a", "w1/a"},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			t.Parallel()

			target, err := store.Resolve(t.Context(), item.path, item.cwd)
			if err != nil {
				t.Fatalf("解析 %q 不该失败：%v", item.path, err)
			}
			if target.DisplayPath != item.wantDisplay {
				t.Fatalf("展示路径该是 %q，实际 %q", item.wantDisplay, target.DisplayPath)
			}
			if string(target.TargetKey) != item.wantKey {
				t.Fatalf("对象键该是 %q，实际 %q", item.wantKey, target.TargetKey)
			}
		})
	}
}

// TestResolveWithoutPrefixUsesBareKeys 验空前缀时键就是去掉开头斜杠的路径。
func TestResolveWithoutPrefixUsesBareKeys(t *testing.T) {
	t.Parallel()

	store := newStore(t, "")

	target, err := store.Resolve(t.Context(), "/a/b.txt", "")
	if err != nil {
		t.Fatalf("解析不该失败：%v", err)
	}
	if string(target.TargetKey) != "a/b.txt" {
		t.Fatalf("对象键该是 \"a/b.txt\"，实际 %q", target.TargetKey)
	}

	root, err := store.Resolve(t.Context(), "/", "")
	if err != nil {
		t.Fatalf("解析根不该失败：%v", err)
	}
	if string(root.TargetKey) != "" {
		t.Fatalf("空前缀下根的键该是空串，实际 %q", root.TargetKey)
	}
}

// TestResolveRefusesToLeaveTheWorld 验走到根上面去的路径被拒。
//
// 这条是这个包唯一的隔离手段：同一个桶里的两个世界靠前缀分开，
// 一条 `../../` 能解析出目标来的话，那道分隔就不存在了。
func TestResolveRefusesToLeaveTheWorld(t *testing.T) {
	t.Parallel()

	store := newStore(t, "w1")

	for _, path := range []string{"/..", "/../x", "/a/../../x", "../../w2/secret.txt"} {
		_, err := store.Resolve(t.Context(), path, "/a")
		requireCode(t, err, fs.CodeNotFound)
	}
}

// TestResolveAllowsWalkingUpAndBackDown 验没越界的 `..` 照样能走。
//
// 和上一条配对：越界判定是自己数段数得出的，数错了会把合法路径也拒掉。
func TestResolveAllowsWalkingUpAndBackDown(t *testing.T) {
	t.Parallel()

	store := newStore(t, "w1")

	target, err := store.Resolve(t.Context(), "../b.txt", "/a")
	if err != nil {
		t.Fatalf("没越界的路径不该被拒：%v", err)
	}
	if target.DisplayPath != "/b.txt" {
		t.Fatalf("展示路径该是 \"/b.txt\"，实际 %q", target.DisplayPath)
	}
}

// TestKeysAndDisplayPathsRoundTrip 验换算是可逆的。
//
// 不可逆的话 ListDir 会把服务端给的键换成一条错的展示路径，
// 而调用方拿那条路径再解析一次会落到另一个对象上。
func TestKeysAndDisplayPathsRoundTrip(t *testing.T) {
	t.Parallel()

	for _, prefix := range []string{"", "w1"} {
		store := newStore(t, prefix)
		for _, display := range []string{"/", "/a.txt", "/a/b.txt"} {
			if got := store.displayOf(store.keyOf(display)); got != display {
				t.Fatalf("前缀 %q 下 %q 换回来该还是自己，实际 %q", prefix, display, got)
			}
		}
	}
}

// TestDisplayOfRejectsKeysFromAnotherWorld 验不在本世界前缀下的键换出空串。
//
// 这种键不该出现，但改一次配置就能让它出现；把它当成本世界的东西展示出去
// 是更坏的结果，所以调用方靠空串把它跳过。
func TestDisplayOfRejectsKeysFromAnotherWorld(t *testing.T) {
	t.Parallel()

	store := newStore(t, "w1")
	if got := store.displayOf("w2/secret.txt"); got != "" {
		t.Fatalf("别的世界的键该换出空串，实际 %q", got)
	}
}

// TestDirPrefixAlwaysEndsWithASlash 验列举用的前缀形状。
//
// 世界根那一条是重点：给根再拼一个斜杠的话前缀会变成 "/"，
// 而桶里没有任何键以斜杠开头，于是根目录永远列出空。
func TestDirPrefixAlwaysEndsWithASlash(t *testing.T) {
	t.Parallel()

	store := newStore(t, "w1")
	if got := store.dirPrefixOf("w1/"); got != "w1/" {
		t.Fatalf("已经以斜杠结尾的前缀不该再加，实际 %q", got)
	}
	if got := store.dirPrefixOf("w1/a"); got != "w1/a/" {
		t.Fatalf("该补上斜杠，实际 %q", got)
	}

	bare := newStore(t, "")
	if got := bare.dirPrefixOf(""); got != "" {
		t.Fatalf("空前缀下的根该还是空串，实际 %q", got)
	}
}

// TestContainsComparesWholeSegments 验包含判定按整段比，不按字符前缀比。
//
// `a/b` 不包含 `a/bc.txt`。少了那个斜杠的话，一条信任边界规则会把边界外的
// 文件当成边界内的放过去——这是这个方法唯一会出的错，也是它存在的理由。
func TestContainsComparesWholeSegments(t *testing.T) {
	t.Parallel()

	store := newStore(t, "w1")
	target := func(key string) fs.Target { return fs.Target{TargetKey: fs.TargetKey(key)} }

	cases := []struct {
		name   string
		parent string
		child  string
		want   bool
	}{
		{"自己包含自己", "w1/a/b", "w1/a/b", true},
		{"后代", "w1/a/b", "w1/a/b/c.txt", true},
		{"同名前缀不算", "w1/a/b", "w1/a/bc.txt", false},
		{"兄弟不算", "w1/a/b", "w1/a/c.txt", false},
		{"世界根包含一切", "w1/", "w1/a/b.txt", true},
		{"世界根不含别的世界", "w1/", "w2/a.txt", false},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			t.Parallel()

			if got := store.Contains(target(item.parent), target(item.child)); got != item.want {
				t.Fatalf("Contains(%q, %q) 该是 %v", item.parent, item.child, item.want)
			}
		})
	}
}

// TestProcessPathAndFileURLPanic 验那两个方法真的会炸，而且炸的时候说得清楚。
//
// 用例把这件事记下来，是因为「它 panic」在这里是**有意的契约**而不是没写完：
// 对象存储上没有子进程能打开的路径，也没有 file: URI。返回一个假的串
// 会让调用方在离这里很远的地方失败，现场只剩一条「找不到文件」。
func TestProcessPathAndFileURLPanic(t *testing.T) {
	t.Parallel()

	store := newStore(t, "w1")
	target := fs.Target{TargetKey: "w1/a.txt", DisplayPath: "/a.txt"}

	for _, item := range []struct {
		name string
		call func()
	}{
		{"ProcessPath", func() { store.ProcessPath(target) }},
		{"FileURL", func() { store.FileURL(target) }},
	} {
		t.Run(item.name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				recovered := recover()
				if recovered == nil {
					t.Fatal("该 panic 的")
				}
				message, ok := recovered.(string)
				if !ok || !strings.Contains(message, "/a.txt") {
					t.Fatalf("panic 里该带上目标路径，实际 %v", recovered)
				}
			}()
			item.call()
		})
	}
}
