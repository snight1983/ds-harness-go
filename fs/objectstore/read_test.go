// 本文件验只读的那六个原语，全部跑在 [fakeS3] 上。
//
// 跑在真服务端协议上而不是跑在一个假客户端上，是因为这几个方法的行为
// 有一半长在「服务端给了什么头」上面：ETag 的引号、公共前缀的形状、
// 404 的 XML 里那个 Code——桩出来的客户端一个都验不到。

package objectstore

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/fs"
)

// target 把一个世界绝对路径换成目标，用例里出现得太多了。
func target(t *testing.T, store *Store, path string) fs.Target {
	t.Helper()

	resolved, err := store.Resolve(t.Context(), path, "")
	if err != nil {
		t.Fatalf("解析 %q 不该失败：%v", path, err)
	}
	return resolved
}

// TestStatSeesAFileWithItsVersionAndSize 验一个对象被看成带版本带大小的文件。
func TestStatSeesAFileWithItsVersionAndSize(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/a.txt", "hello")
	store := fake.store(t, "w1")

	info, found, err := store.Stat(t.Context(), target(t, store, "/a.txt"))
	if err != nil {
		t.Fatalf("查看不该失败：%v", err)
	}
	if !found {
		t.Fatal("该看到这个对象")
	}
	if info.Type != fs.TypeFile {
		t.Fatalf("该是文件，实际 %v", info.Type)
	}
	if info.Size == nil || *info.Size != 5 {
		t.Fatalf("大小该是 5，实际 %v", info.Size)
	}
	if string(info.Version) != fake.etagOfKey("w1/a.txt") {
		t.Fatalf("版本该是去掉引号的 ETag，实际 %q", info.Version)
	}
	if strings.Contains(string(info.Version), `"`) {
		t.Fatalf("版本里不该有引号：%q", info.Version)
	}
}

// TestStatInfersADirectoryFromItsChildren 验目录是推断出来的，而且不带版本。
//
// 键空间是平的，「目录」这个概念在这个后端上**只有**这一个定义：
// 有别的键以它加斜杠开头。
func TestStatInfersADirectoryFromItsChildren(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/dir/x.txt", "x")
	store := fake.store(t, "w1")

	info, found, err := store.Stat(t.Context(), target(t, store, "/dir"))
	if err != nil {
		t.Fatalf("查看不该失败：%v", err)
	}
	if !found {
		t.Fatal("有子项就该看到这个目录")
	}
	if info.Type != fs.TypeDirectory {
		t.Fatalf("该是目录，实际 %v", info.Type)
	}
	if info.Version != "" {
		t.Fatalf("目录不该带版本，实际 %q", info.Version)
	}
	if info.Size != nil {
		t.Fatalf("目录不该带大小，实际 %v", *info.Size)
	}
}

// TestStatReportsAnEmptyDirectoryAsAbsent 验空目录在这个后端上不存在。
//
// 这不是缺陷，是这个后端的事实：那里没有任何对象，也就没有任何东西能证明它在。
// 用例把它钉住，免得以后有人「顺手修一下」，而那要靠写占位对象——
// 那是往用户的桶里放我们自己的垃圾。
func TestStatReportsAnEmptyDirectoryAsAbsent(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/other/x.txt", "x")
	store := fake.store(t, "w1")

	_, found, err := store.Stat(t.Context(), target(t, store, "/empty"))
	if err != nil {
		t.Fatalf("查看不该失败：%v", err)
	}
	if found {
		t.Fatal("空目录在对象存储上不存在")
	}
}

// TestStatAlwaysSeesTheWorldRoot 验世界根不需要任何对象来证明。
//
// 一个空世界的根照样得能被 ListDir 列出空来，所以它不能由「有没有子项」决定。
func TestStatAlwaysSeesTheWorldRoot(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	store := fake.store(t, "w1")

	info, found, err := store.Stat(t.Context(), target(t, store, "/"))
	if err != nil {
		t.Fatalf("查看根不该失败：%v", err)
	}
	if !found || info.Type != fs.TypeDirectory {
		t.Fatalf("根永远在，而且永远是目录，实际 found=%v type=%v", found, info.Type)
	}
}

// TestLstatSeesTheSameThingAsStatAndNeverASymlink 验 Lstat 在这里的语义。
//
// 对象存储里没有符号链接，所以这个方法一次也不会报 [fs.TypeSymlink]。
// 那不是漏了一支，是这个执行世界里根本没有那种东西。
func TestLstatSeesTheSameThingAsStatAndNeverASymlink(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/a.txt", "hello")
	fake.seed("w1/dir/x.txt", "x")
	store := fake.store(t, "w1")

	for _, item := range []struct {
		path string
		want fs.EntryType
	}{
		{"/a.txt", fs.TypeFile},
		{"/dir", fs.TypeDirectory},
	} {
		info, found, err := store.Lstat(t.Context(), item.path, "")
		if err != nil {
			t.Fatalf("Lstat %q 不该失败：%v", item.path, err)
		}
		if !found || info.Type != item.want {
			t.Fatalf("Lstat %q 该是 %v，实际 found=%v type=%v", item.path, item.want, found, info.Type)
		}
	}

	_, found, err := store.Lstat(t.Context(), "/nope.txt", "")
	if err != nil {
		t.Fatalf("Lstat 不存在的路径不该失败：%v", err)
	}
	if found {
		t.Fatal("不存在的路径该报不在")
	}
}

// TestLstatRefusesToLeaveTheWorld 验 Lstat 走的是和 Resolve 同一套换算。
//
// 两边任何一点不一致，都会让「先 Lstat 再 Resolve」这个再平常不过的用法
// 看到两个不同的东西。
func TestLstatRefusesToLeaveTheWorld(t *testing.T) {
	t.Parallel()

	store := newFakeS3(t).store(t, "w1")

	_, _, err := store.Lstat(t.Context(), "../../w2/secret.txt", "/a")
	requireCode(t, err, fs.CodeNotFound)
}

// TestReadTextDecodesAndNormalizes 验读文本这条路的两件事：解码和行尾。
func TestReadTextDecodesAndNormalizes(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/a.txt", "第一行\r\n第二行\n")
	store := fake.store(t, "w1")

	text, err := store.ReadText(t.Context(), target(t, store, "/a.txt"))
	if err != nil {
		t.Fatalf("读不该失败：%v", err)
	}
	if text != "第一行\n第二行\n" {
		t.Fatalf("CRLF 该被折成 LF，实际 %q", text)
	}
}

// TestReadTextKeepsALoneCarriageReturn 验单独的 CR 不动。
//
// 它在老 Mac 文本里是行尾，但在今天更可能是数据。一条规则只做一件确定的事，
// 比一条覆盖面更广但会改坏数据的规则好。
func TestReadTextKeepsALoneCarriageReturn(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/a.txt", "a\rb")
	store := fake.store(t, "w1")

	text, err := store.ReadText(t.Context(), target(t, store, "/a.txt"))
	if err != nil {
		t.Fatalf("读不该失败：%v", err)
	}
	if text != "a\rb" {
		t.Fatalf("单独的 CR 该原样留着，实际 %q", text)
	}
}

// TestReadTextRefusesBinaryAndOversized 验文本路上两条硬拒绝。
func TestReadTextRefusesBinaryAndOversized(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seedBytes("w1/binary", []byte{0xff, 0xfe, 0x00})
	fake.seed("w1/big.txt", strings.Repeat("x", 64))
	store := fake.storeWith(t, Config{Prefix: "w1", MaxTextBytes: 16})

	_, err := store.ReadText(t.Context(), target(t, store, "/binary"))
	requireCode(t, err, fs.CodeNotText)

	_, err = store.ReadText(t.Context(), target(t, store, "/big.txt"))
	requireCode(t, err, fs.CodeTooLarge)

	_, err = store.ReadText(t.Context(), target(t, store, "/nope.txt"))
	requireCode(t, err, fs.CodeNotFound)
}

// TestReadTextAcceptsContentExactlyAtTheLimit 验「正好到上限」不算超。
//
// 多读一个字节就是为了把这两件事分开；分不开的话上限会莫名其妙地少一个字节。
func TestReadTextAcceptsContentExactlyAtTheLimit(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/a.txt", strings.Repeat("x", 16))
	store := fake.storeWith(t, Config{Prefix: "w1", MaxTextBytes: 16})

	text, err := store.ReadText(t.Context(), target(t, store, "/a.txt"))
	if err != nil {
		t.Fatalf("正好到上限不该失败：%v", err)
	}
	if len(text) != 16 {
		t.Fatalf("该读到 16 字节，实际 %d", len(text))
	}
}

// TestReadBytesTouchesNothing 验原始字节这条路一个字节都不碰。
//
// 不解码、不拒二进制、不折行尾——需要原样内容的调用方（图片、压缩包、
// 要算摘要的东西）全靠这条路。
func TestReadBytesTouchesNothing(t *testing.T) {
	t.Parallel()

	raw := []byte{0xff, 'a', '\r', '\n', 0x00}
	fake := newFakeS3(t)
	fake.seedBytes("w1/blob", raw)
	store := fake.store(t, "w1")

	got, err := store.ReadBytes(t.Context(), target(t, store, "/blob"), 1024)
	if err != nil {
		t.Fatalf("读字节不该失败：%v", err)
	}
	if string(got) != string(raw) {
		t.Fatalf("字节该原样回来，实际 %v", got)
	}

	_, err = store.ReadBytes(t.Context(), target(t, store, "/blob"), 2)
	requireCode(t, err, fs.CodeTooLarge)

	_, err = store.ReadBytes(t.Context(), target(t, store, "/nope"), 1024)
	requireCode(t, err, fs.CodeNotFound)
}

// TestListDirGivesDirectChildrenInAStableOrder 验列目录的三件事。
//
// 顺序、公共前缀被当成目录、以及文件带版本带大小而目录两样都不带。
func TestListDirGivesDirectChildrenInAStableOrder(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/z.txt", "z")
	fake.seed("w1/a.txt", "a")
	fake.seed("w1/m/deep/x.txt", "x")
	fake.seed("w1/m/y.txt", "y")
	store := fake.store(t, "w1")

	entries, err := store.ListDir(t.Context(), target(t, store, "/"))
	if err != nil {
		t.Fatalf("列目录不该失败：%v", err)
	}

	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	if strings.Join(names, ",") != "a.txt,m,z.txt" {
		t.Fatalf("该按名字给出直接子项，实际 %v", names)
	}

	for _, entry := range entries {
		switch entry.Name {
		case "m":
			if entry.Type != fs.TypeDirectory {
				t.Fatalf("m 该是目录，实际 %v", entry.Type)
			}
			if entry.Version != "" || entry.Size != nil {
				t.Fatal("目录项不该带版本或大小")
			}
			if entry.Target.DisplayPath != "/m" {
				t.Fatalf("目录项的展示路径该是 \"/m\"，实际 %q", entry.Target.DisplayPath)
			}
		case "a.txt":
			if entry.Type != fs.TypeFile {
				t.Fatalf("a.txt 该是文件，实际 %v", entry.Type)
			}
			if entry.Version == "" || entry.Size == nil || *entry.Size != 1 {
				t.Fatalf("文件项该带版本和大小，实际 %q / %v", entry.Version, entry.Size)
			}
		}
	}
}

// TestListDirSwallowsThePlaceholderObject 验零字节的 `a/b/` 占位对象。
//
// 有些工具会为目录建这种键。两件事都要成立：列它的父目录时它和公共前缀
// 折成同一项（不能出现两个 "dir"），列它自己时它不能变成一个名字是空串的项。
func TestListDirSwallowsThePlaceholderObject(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/dir/", "")
	fake.seed("w1/dir/x.txt", "x")
	store := fake.store(t, "w1")

	entries, err := store.ListDir(t.Context(), target(t, store, "/"))
	if err != nil {
		t.Fatalf("列根不该失败：%v", err)
	}
	if len(entries) != 1 || entries[0].Name != "dir" || entries[0].Type != fs.TypeDirectory {
		t.Fatalf("占位对象该和公共前缀折成一项目录，实际 %+v", entries)
	}

	inside, err := store.ListDir(t.Context(), target(t, store, "/dir"))
	if err != nil {
		t.Fatalf("列子目录不该失败：%v", err)
	}
	if len(inside) != 1 || inside[0].Name != "x.txt" {
		t.Fatalf("占位对象自己不该冒出来，实际 %+v", inside)
	}
}

// TestListDirCollapsesANameThatIsBothAFileAndADirectory 验同名冲突只出现一次。
//
// 这是对象存储上才有、而文件系统上造不出来的一种局面：`w1/dir` 是一个对象，
// `w1/dir/x.txt` 又让 `w1/dir/` 成为一个公共前缀。同一次列举里，服务端会在
// Contents 里给出一个叫 `dir` 的项，又在 CommonPrefixes 里给出一个叫 `dir` 的项。
//
// 这里**只**断言它们被折成一项。哪一边胜出取决于服务端把两段放在响应里的先后，
// 那不是这个后端能承诺的事；能承诺的是列表里不会出现两个同名项——
// 重名会让任何一个按名字建索引的调用方悄悄丢掉其中一个。
func TestListDirCollapsesANameThatIsBothAFileAndADirectory(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/dir", "我是一个叫 dir 的文件")
	fake.seed("w1/dir/x.txt", "我在一个叫 dir 的目录里")
	store := fake.store(t, "w1")

	entries, err := store.ListDir(t.Context(), target(t, store, "/"))
	if err != nil {
		t.Fatalf("列根不该失败：%v", err)
	}

	named := 0
	for _, entry := range entries {
		if entry.Name == "dir" {
			named++
		}
	}
	if named != 1 {
		t.Fatalf("同一个名字该只出现一次，实际出现 %d 次：%+v", named, entries)
	}
}

// TestListDirRefusesAFileAndAnAbsentTarget 验列目录的两条拒绝。
func TestListDirRefusesAFileAndAnAbsentTarget(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/a.txt", "a")
	store := fake.store(t, "w1")

	_, err := store.ListDir(t.Context(), target(t, store, "/a.txt"))
	requireCode(t, err, fs.CodeNotDirectory)

	_, err = store.ListDir(t.Context(), target(t, store, "/nope"))
	requireCode(t, err, fs.CodeNotFound)
}

// TestListDirOnAnEmptyWorldGivesAnEmptyList 验空世界的根列出空而不是报错。
func TestListDirOnAnEmptyWorldGivesAnEmptyList(t *testing.T) {
	t.Parallel()

	store := newFakeS3(t).store(t, "w1")

	entries, err := store.ListDir(t.Context(), target(t, store, "/"))
	if err != nil {
		t.Fatalf("列空世界的根不该失败：%v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("该是空的，实际 %+v", entries)
	}
}

// collect 把一个文本流拼成一整份，顺便把块数带回来。
func collect(t *testing.T, chunks func(func(string, error) bool)) (string, int) {
	t.Helper()

	var builder strings.Builder
	count := 0
	for chunk, err := range chunks {
		if err != nil {
			t.Fatalf("流里不该有错：%v", err)
		}
		builder.WriteString(chunk)
		count++
	}
	return builder.String(), count
}

// TestStreamTextSplitsRunesAndCRLFAcrossChunks 验流式解码压着的那两条尾巴。
//
// 块大小设成 2，于是每一个中文字符都会被切开，那个 CRLF 也会被切开。
// 压不住的话前者会被报成二进制，后者会漏成一个 CR 加一个 LF。
func TestStreamTextSplitsRunesAndCRLFAcrossChunks(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/a.txt", "中文\r\nabc")
	store := fake.storeWith(t, Config{Prefix: "w1", ChunkBytes: 2})

	chunks, err := store.StreamText(t.Context(), target(t, store, "/a.txt"))
	if err != nil {
		t.Fatalf("起流不该失败：%v", err)
	}

	text, count := collect(t, chunks)
	if text != "中文\nabc" {
		t.Fatalf("拼起来该和整读一致，实际 %q", text)
	}
	if count < 2 {
		t.Fatalf("块大小是 2，该分成好几块，实际 %d 块", count)
	}
}

// TestStreamTextMatchesReadText 验两条读法在同一份内容上给出同一个结果。
//
// 语义一致是接缝的要求：流式只是分块交付，不是另一种语义。
func TestStreamTextMatchesReadText(t *testing.T) {
	t.Parallel()

	content := strings.Repeat("一行文字\r\n", 200)
	fake := newFakeS3(t)
	fake.seed("w1/a.txt", content)
	store := fake.storeWith(t, Config{Prefix: "w1", ChunkBytes: 7})

	whole, err := store.ReadText(t.Context(), target(t, store, "/a.txt"))
	if err != nil {
		t.Fatalf("整读不该失败：%v", err)
	}

	chunks, err := store.StreamText(t.Context(), target(t, store, "/a.txt"))
	if err != nil {
		t.Fatalf("起流不该失败：%v", err)
	}
	streamed, _ := collect(t, chunks)

	if streamed != whole {
		t.Fatal("流式和整读该给出同一份文本")
	}
}

// TestStreamTextGivesBackACarriageReturnItWasHoldingAtTheEnd 验收尾时交出压着的 CR。
//
// 内容以一个孤零零的 CR 结尾：解码器一路压着它等下一个字节，等到的却是流的末尾。
// 它是内容的一部分，收尾时必须交出去——丢掉的话流式读取会比整份读少一个字节，
// 而这一个字节的差别足以让一次「读出来、原样写回去」把文件改短。
func TestStreamTextGivesBackACarriageReturnItWasHoldingAtTheEnd(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/a.txt", "第一行\r第二行\r")
	store := fake.storeWith(t, Config{Prefix: "w1", ChunkBytes: 4})

	whole, err := store.ReadText(t.Context(), target(t, store, "/a.txt"))
	if err != nil {
		t.Fatalf("整读不该失败：%v", err)
	}

	chunks, err := store.StreamText(t.Context(), target(t, store, "/a.txt"))
	if err != nil {
		t.Fatalf("起流不该失败：%v", err)
	}
	streamed, _ := collect(t, chunks)

	if streamed != whole {
		t.Fatalf("流式和整读该给出同一份文本，实际 %q / %q", streamed, whole)
	}
	if !strings.HasSuffix(streamed, "\r") {
		t.Fatalf("末尾那个 CR 该还在，实际 %q", streamed)
	}
}

// TestStreamTextRefusesBinaryAndOversizedAndNonFiles 验流式路上的三条拒绝。
//
// 前两条的判据要和 [Store.ReadText] 逐字相同，第三条是流式特有的：
// 目录上没有内容可流。
func TestStreamTextRefusesBinaryAndOversizedAndNonFiles(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seedBytes("w1/binary", []byte{'a', 'b', 0xff, 0xfe})
	fake.seed("w1/big.txt", strings.Repeat("x", 64))
	fake.seed("w1/dir/x.txt", "x")
	store := fake.storeWith(t, Config{Prefix: "w1", MaxTextBytes: 16, ChunkBytes: 2})

	binary, err := store.StreamText(t.Context(), target(t, store, "/binary"))
	if err != nil {
		t.Fatalf("起流不该失败：%v", err)
	}
	requireCode(t, drain(binary), fs.CodeNotText)

	big, err := store.StreamText(t.Context(), target(t, store, "/big.txt"))
	if err != nil {
		t.Fatalf("起流不该失败：%v", err)
	}
	requireCode(t, drain(big), fs.CodeTooLarge)

	_, err = store.StreamText(t.Context(), target(t, store, "/dir"))
	requireCode(t, err, fs.CodeNotRegularFile)

	// 「在不在」是**外层**的错误：调用方不该先建一个迭代器再发现它是空的。
	_, err = store.StreamText(t.Context(), target(t, store, "/nope.txt"))
	requireCode(t, err, fs.CodeNotFound)
}

// drain 把一个文本流走完，交出里面第一个错误。
func drain(chunks func(func(string, error) bool)) error {
	for _, err := range chunks {
		if err != nil {
			return err
		}
	}
	return nil
}

// TestStreamTextStopsBetweenChunksWhenCancelled 验取消在块与块之间也生效。
//
// 一个大对象读到一半被取消时，迭代必须停下来，而不是把剩下的读完再说。
func TestStreamTextStopsBetweenChunksWhenCancelled(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/a.txt", strings.Repeat("x", 4096))
	store := fake.storeWith(t, Config{Prefix: "w1", ChunkBytes: 8})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	chunks, err := store.StreamText(ctx, target(t, store, "/a.txt"))
	if err != nil {
		t.Fatalf("起流不该失败：%v", err)
	}

	seen := 0
	var failure error
	for _, chunkErr := range chunks {
		if chunkErr != nil {
			failure = chunkErr
			break
		}
		seen++
		cancel()
	}

	if seen == 0 {
		t.Fatal("取消之前该先拿到一块")
	}
	if seen > 4 {
		t.Fatalf("取消之后该很快停下，实际读了 %d 块", seen)
	}
	requireCode(t, failure, fs.CodeAborted)
}

// TestStreamTextStopsWhenTheCallerBreaks 验调用方提前 break 时流干净收场。
//
// 这条钉的是「不迭代完也不会泄漏」：GET 那条连接由迭代函数的 defer 关掉。
func TestStreamTextStopsWhenTheCallerBreaks(t *testing.T) {
	t.Parallel()

	fake := newFakeS3(t)
	fake.seed("w1/a.txt", strings.Repeat("x", 1024))
	store := fake.storeWith(t, Config{Prefix: "w1", ChunkBytes: 8})

	chunks, err := store.StreamText(t.Context(), target(t, store, "/a.txt"))
	if err != nil {
		t.Fatalf("起流不该失败：%v", err)
	}
	for chunk, chunkErr := range chunks {
		if chunkErr != nil {
			t.Fatalf("第一块不该有错：%v", chunkErr)
		}
		if chunk == "" {
			t.Fatal("第一块不该是空的")
		}
		break
	}
}

// TestTextDecoderHoldsBackPartialRunes 直接验解码器压尾巴的行为。
//
// 走接口的用例只能证明「拼起来是对的」；这一条能证明**压住了**——
// 一个被切开的 rune 的前几个字节，在下一块到来之前一个都不该交出去。
func TestTextDecoderHoldsBackPartialRunes(t *testing.T) {
	t.Parallel()

	var decoder textDecoder
	raw := []byte("中")

	first, err := decoder.push(raw[:1])
	if err != nil {
		t.Fatalf("半个 rune 不该被当成二进制：%v", err)
	}
	if first != "" {
		t.Fatalf("半个 rune 该被压住，实际交出了 %q", first)
	}

	second, err := decoder.push(raw[1:])
	if err != nil {
		t.Fatalf("补齐之后不该失败：%v", err)
	}
	if second != "中" {
		t.Fatalf("补齐之后该交出整个字符，实际 %q", second)
	}

	tail, err := decoder.flush()
	if err != nil || tail != "" {
		t.Fatalf("没有尾巴了，实际 %q / %v", tail, err)
	}
}

// TestTextDecoderReportsATruncatedFinalRune 验流末尾压着半个 rune 时报二进制。
//
// 一份文本的最后一个字符是残缺的，那它就不是文本。
func TestTextDecoderReportsATruncatedFinalRune(t *testing.T) {
	t.Parallel()

	var decoder textDecoder
	if _, err := decoder.push([]byte("中")[:1]); err != nil {
		t.Fatalf("推进去不该失败：%v", err)
	}
	if _, err := decoder.flush(); err == nil {
		t.Fatal("末尾压着半个 rune 该报错")
	}
}

// TestTextDecoderRejectsInvalidBytes 验非法字节在推的那一刻就被认出来。
func TestTextDecoderRejectsInvalidBytes(t *testing.T) {
	t.Parallel()

	var decoder textDecoder
	if _, err := decoder.push([]byte{'a', 0xff, 0xfe, 'b'}); err == nil {
		t.Fatal("非法字节该报错")
	}
}

// TestTextDecoderHoldsBackATrailingCarriageReturn 验末尾的 CR 被压到下一块。
//
// 这是行尾规范化在流式读取上的全部难点：CRLF 被切在两块中间时，如果第一块就把
// 那个 CR 交出去，这次折叠就永远补不回来了——调用方会拿到 `a\r` 和 `\nb`，
// 拼起来是 `a\r\nb`，而整份读给的是 `a\nb`。两条路必须给出同一份文本。
func TestTextDecoderHoldsBackATrailingCarriageReturn(t *testing.T) {
	t.Parallel()

	var decoder textDecoder

	first, err := decoder.push([]byte("a\r"))
	if err != nil {
		t.Fatalf("推进去不该失败：%v", err)
	}
	if first != "a" {
		t.Fatalf("末尾的 CR 该压着不给，实际 %q", first)
	}

	second, err := decoder.push([]byte("\nb"))
	if err != nil {
		t.Fatalf("推进去不该失败：%v", err)
	}
	if second != "\nb" {
		t.Fatalf("压着的 CR 该和这一块的 LF 折成一个 LF，实际 %q", second)
	}
}

// TestTextDecoderFlushesAHeldBackCarriageReturn 验压着的 CR 在收尾时交出去。
//
// 内容以一个孤零零的 CR 结尾时，那个字节压到了最后也没等来 LF。它是内容的一部分，
// 得原样交出去——丢掉的话，一次流式读取会比整份读**少一个字节**。
func TestTextDecoderFlushesAHeldBackCarriageReturn(t *testing.T) {
	t.Parallel()

	var decoder textDecoder
	if _, err := decoder.push([]byte("a\r")); err != nil {
		t.Fatalf("推进去不该失败：%v", err)
	}

	rest, err := decoder.flush()
	if err != nil {
		t.Fatalf("收尾不该失败：%v", err)
	}
	if rest != "\r" {
		t.Fatalf("压着的 CR 该原样交出来，实际 %q", rest)
	}

	// 收完就得清空，否则再收一次会把同一个字节交出去第二遍。
	again, err := decoder.flush()
	if err != nil || again != "" {
		t.Fatalf("再收一次该是空的，实际 %q / %v", again, err)
	}
}

// TestRuneLengthAtReadsEveryLeadingByteShape 验 UTF-8 的五种起始字节形状。
//
// 逐个列出来而不是随便挑两个：这个函数是「一个 rune 有没有被切开」的**唯一**判据，
// 判长了会把下一个字符的头几个字节吃进来，判短了会把一个完整的 rune 拆成两半——
// 两种都会让一份好好的文本被报成二进制。
func TestRuneLengthAtReadsEveryLeadingByteShape(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		first byte
		want  int
	}{
		{"ASCII", 'a', 1},
		{"两字节", []byte("é")[0], 2},
		{"三字节", []byte("中")[0], 3},
		{"四字节", []byte("𝄞")[0], 4},
		{"续字节不是起始字节", []byte("中")[1], 0},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			t.Parallel()

			if got := runeLengthAt(item.first); got != item.want {
				t.Fatalf("%#x 该是 %d 个字节，实际 %d", item.first, item.want, got)
			}
		})
	}
}

// TestReadingTheWorldRootReportsAbsenceWithOrWithoutAPrefix 验把根当文件读的结论一致。
//
// 根是目录，这个后端上没有任何对象证明它，所以读它就是读一个不存在的对象。
// 两种世界都验，是因为**没有前缀**时根的键是空串，而空对象名会被 SDK 当成参数错误
// 顶回来——不特意处理的话，同一次操作会因为一行前缀配置的差别报出两个不同的码。
func TestReadingTheWorldRootReportsAbsenceWithOrWithoutAPrefix(t *testing.T) {
	t.Parallel()

	for _, prefix := range []string{"w1", ""} {
		t.Run("前缀"+strconv.Quote(prefix), func(t *testing.T) {
			t.Parallel()

			child := "a.txt"
			if prefix != "" {
				child = prefix + "/a.txt"
			}

			fake := newFakeS3(t)
			// 世界里有东西，好证明「根读不出来」不是因为桶是空的。
			fake.seed(child, "hello")
			store := fake.store(t, prefix)
			root := target(t, store, "/")

			_, err := store.ReadText(t.Context(), root)
			requireCode(t, err, fs.CodeNotFound)

			_, err = store.ReadBytes(t.Context(), root, 1024)
			requireCode(t, err, fs.CodeNotFound)
		})
	}
}
