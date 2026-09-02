// 本文件验这条接缝的契约：必答的十个原语，加上可选那道接缝上的两个，
// 各自答应了什么。
//
// 源: packages/fs/fs/tests/service.spec.ts:85-161
//
// DSH 那边这一组用例里有三条验的是 cordis 的服务生命周期（注册成 ctx.fs、
// 装第二个实现要报重复服务、提供方的 fiber 释放之后服务从上下文里消失）。
// 那三条在 Go 里没有对应物，也不该有对应物：[FileSystem] 是一个接口，
// 「谁提供它」「装了几个」「什么时候撤掉」全是装配方的事，不是这个包的事。
// 编译期的 var _ FileSystem = ... 就是「这个实现满足契约」的全部内容。
//
// 剩下的用例是契约本身，逐条搬。另外补了 DSH 的 fake 压根没实现、
// 因而它那组用例也验不到的几条：守卫分支、上限、单处匹配、块间取消。

package fs

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeFS 必须满足这条接缝的全部契约，否则下面每一条用例都无从谈起。
var _ FileSystem = (*fakeFS)(nil)

// resolved 是「解析一条路径」这个动作的简写，用例里出现得太多了。
func resolved(t *testing.T, backend *fakeFS, path string) Target {
	t.Helper()

	target, err := backend.Resolve(t.Context(), path, "")
	if err != nil {
		t.Fatalf("解析 %q 不该失败：%v", path, err)
	}
	return target
}

// requireCode 断言 err 是本包的错误并且带着期望的那个码。
//
// 分派看的是 Code 不是 Message（见 [Error]），所以断言也只看 Code。
func requireCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()

	var failure *Error
	if !errors.As(err, &failure) {
		t.Fatalf("该是一个 *fs.Error，实际 %#v", err)
	}
	if failure.Code != want {
		t.Fatalf("该报 %s，实际 %s（%s）", want, failure.Code, failure.Message)
	}
}

// TestTheSeamServesThePrimitives 是最基本的那一条：解析、探测、读。
//
// 源: packages/fs/fs/tests/service.spec.ts:86-95
func TestTheSeamServesThePrimitives(t *testing.T) {
	t.Parallel()

	backend := newFakeFS()
	backend.seed(TargetKey("a.txt"), "hi")

	target := resolved(t, backend, "a.txt")
	info, found, err := backend.Stat(t.Context(), target)
	if err != nil || !found {
		t.Fatalf("刚种下的文件该被探测到：found=%v err=%v", found, err)
	}
	if info.Type != TypeFile {
		t.Errorf("该是 %s，实际 %s", TypeFile, info.Type)
	}
	if info.Size == nil || *info.Size != 2 {
		t.Errorf("大小该是 2，实际 %v", info.Size)
	}

	text, err := backend.ReadText(t.Context(), target)
	if err != nil {
		t.Fatalf("读不该失败：%v", err)
	}
	if text != "hi" {
		t.Errorf("该读出 %q，实际 %q", "hi", text)
	}
}

// TestResolveJoinsRelativePathsOntoTheGivenBase 钉住 cwd 那个参数真的在起作用。
//
// 源: packages/fs/fs/src/index.ts:107-116
//
// 新增: DSH 的 fake 直接忽略 cwd（service.spec.ts:26-28 只用了 path）。
// 这里验它，是因为「留空表示用后端自己的默认基准」这句话在接口文档里，
// 一个不看 cwd 的后端会让每一条相对路径都落在错的地方，而调用方看不出来。
func TestResolveJoinsRelativePathsOntoTheGivenBase(t *testing.T) {
	t.Parallel()

	backend := newFakeFS()

	relative, err := backend.Resolve(t.Context(), "a.txt", "/work")
	if err != nil {
		t.Fatalf("解析不该失败：%v", err)
	}
	if relative.TargetKey != TargetKey("/work/a.txt") {
		t.Errorf("相对路径该落在基准下，实际 %q", relative.TargetKey)
	}

	absolute, err := backend.Resolve(t.Context(), "/etc/hosts", "/work")
	if err != nil {
		t.Fatalf("解析不该失败：%v", err)
	}
	if absolute.TargetKey != TargetKey("/etc/hosts") {
		t.Errorf("绝对路径不该被基准改写，实际 %q", absolute.TargetKey)
	}

	if _, err := backend.Resolve(t.Context(), "", ""); err == nil {
		t.Error("空路径该被拒")
	}
}

// TestProcessPathAndTargetKeyAreDifferentThings 钉住那条「故意是两样东西」。
//
// 源: packages/fs/fs/src/index.ts:118-135
//
// 值相不相等是后端自己的事（这个内存后端就让它们相等）；
// 这条用例钉的是**两个方法都在**，因为需要一条能交给子进程的路径时
// 必须去问 [OSPathFileSystem.ProcessPath]，不许直接把 [TargetKey] 当路径用。
//
// 走的是那道可选接缝的类型断言，也就是真实调用方唯一该走的那条路：
// 断言得过才有这两个方法，断言不过是「这个部署上没有这条路」而不是一次崩溃。
func TestProcessPathAndTargetKeyAreDifferentThings(t *testing.T) {
	t.Parallel()

	concrete := newFakeFS()
	target := resolved(t, concrete, "a b.txt")

	var plain FileSystem = concrete
	backend, ok := plain.(OSPathFileSystem)
	if !ok {
		t.Fatal("这个内存后端的目标在操作系统里也有名字，该认下这道可选接缝")
	}

	if got := backend.ProcessPath(target); got != "a b.txt" {
		t.Errorf("进程路径该是 %q，实际 %q", "a b.txt", got)
	}
	// file: URI 的编码由后端拥有，所以空格在这里被转义了，而 ProcessPath 里没有。
	if got := backend.FileURL(target); got != "file:///a%20b.txt" {
		t.Errorf("URI 该是转义过的，实际 %q", got)
	}
}

// TestContainsAnswersSelfAndDescendants 钉住包含关系由后端回答。
//
// 源: packages/fs/fs/tests/service.spec.ts:31-33
func TestContainsAnswersSelfAndDescendants(t *testing.T) {
	t.Parallel()

	backend := newFakeFS()
	parent := resolved(t, backend, "skills")
	child := resolved(t, backend, "skills/alpha.md")
	outside := resolved(t, backend, "skills-backup/alpha.md")

	if !backend.Contains(parent, parent) {
		t.Error("一个目标该包含它自己")
	}
	if !backend.Contains(parent, child) {
		t.Error("该包含后代")
	}
	if backend.Contains(parent, outside) {
		t.Error("同前缀但不同段的目标不该算在里面")
	}
}

// TestStreamTextYieldsExactlyWhatReadTextReturns 钉住两条读路径给出同一份内容。
//
// 源: packages/fs/fs/tests/service.spec.ts:111-120
//
// 这一条是分块读的地基：策略层靠 [Info.Size] 在两者之间选一个，
// 选哪个都必须得到同一份东西，否则那个选择就成了一次内容变更。
func TestStreamTextYieldsExactlyWhatReadTextReturns(t *testing.T) {
	t.Parallel()

	backend := newFakeFS()
	// 比 fakeChunkSize 长，才走得出多块的路径。
	backend.seed(TargetKey("a.txt"), "one\ntwo\nthree")
	target := resolved(t, backend, "a.txt")

	chunks, err := backend.StreamText(t.Context(), target)
	if err != nil {
		t.Fatalf("开流不该失败：%v", err)
	}

	var streamed strings.Builder
	count := 0
	for chunk, err := range chunks {
		if err != nil {
			t.Fatalf("流中不该出错：%v", err)
		}
		streamed.WriteString(chunk)
		count++
	}
	if count < 2 {
		t.Errorf("该分成多块，实际 %d 块", count)
	}

	whole, err := backend.ReadText(t.Context(), target)
	if err != nil {
		t.Fatalf("读不该失败：%v", err)
	}
	if streamed.String() != whole {
		t.Errorf("流出来的该和整读的一致：%q vs %q", streamed.String(), whole)
	}
}

// TestStreamTextStopsBetweenChunksWhenCancelled 钉住块与块之间也认取消。
//
// 源: packages/fs/fs/src/index.ts:178-187
//
// 新增: DSH 的 fake 一次 yield 整份内容（service.spec.ts:51），
// 所以它那组用例验不到这件事。这里验它的理由写在接口文档里：
// 一个大文件读到一半被取消，迭代必须停下来，而不是把剩下的读完。
func TestStreamTextStopsBetweenChunksWhenCancelled(t *testing.T) {
	t.Parallel()

	backend := newFakeFS()
	backend.seed(TargetKey("a.txt"), "one\ntwo\nthree")
	target := resolved(t, backend, "a.txt")

	ctx, cancel := context.WithCancel(t.Context())
	chunks, err := backend.StreamText(ctx, target)
	if err != nil {
		t.Fatalf("开流不该失败：%v", err)
	}
	cancel()

	for chunk, err := range chunks {
		if err == nil {
			t.Fatalf("取消之后不该再交出内容，却拿到了 %q", chunk)
		}
		requireCode(t, err, CodeAborted)
		break
	}
}

// TestStreamTextStopsWhenTheConsumerBreaks 钉住调用方提前退出时迭代真的停下来。
//
// 新增: 这条是 iter.Seq2 自己的约定（yield 返回 false 就得 return），
// DSH 侧不存在——AsyncIterable 的提前退出由 for-await 的 return() 负责。
// 验它是因为漏掉那个 return 的话，后面的块会被继续算出来然后丢掉，
// 而在一个真后端上那意味着继续读盘。
func TestStreamTextStopsWhenTheConsumerBreaks(t *testing.T) {
	t.Parallel()

	backend := newFakeFS()
	backend.seed(TargetKey("a.txt"), "one\ntwo\nthree")
	target := resolved(t, backend, "a.txt")

	chunks, err := backend.StreamText(t.Context(), target)
	if err != nil {
		t.Fatalf("开流不该失败：%v", err)
	}

	seen := 0
	for range chunks {
		seen++
		break
	}
	if seen != 1 {
		t.Errorf("该只取到 1 块，实际 %d 块", seen)
	}
}

// TestStreamTextRefusesWhatReadTextRefuses 钉住开流之前的失败原样报出来。
//
// 源: packages/fs/fs/src/index.ts:178-187
//
// 「语义同 ReadText」这句话里就包含这一条：不存在的目标在两条路径上
// 报的是同一个码，调用方不用为选了哪条路径准备两套分派。
func TestStreamTextRefusesWhatReadTextRefuses(t *testing.T) {
	t.Parallel()

	backend := newFakeFS()
	target := resolved(t, backend, "missing.txt")

	_, err := backend.StreamText(t.Context(), target)
	requireCode(t, err, CodeNotFound)
}

// TestReadTextRefusesBinaryContent 钉住二进制拒绝落在后端这一侧。
//
// 源: packages/fs/fs/src/index.ts:170-176
//
// 「读要么给出常规 UTF-8 文本，要么给出一个带类型的失败」——
// 交出去一份带 NUL 的字符串的话，上面每一层都会把它当文本处理。
func TestReadTextRefusesBinaryContent(t *testing.T) {
	t.Parallel()

	backend := newFakeFS()
	backend.seed(TargetKey("a.bin"), "PK\x00\x03")
	target := resolved(t, backend, "a.bin")

	_, err := backend.ReadText(t.Context(), target)
	requireCode(t, err, CodeNotText)
}

// TestReadBytesEnforcesTheByteCap 钉住上限落在这条接缝上。
//
// 源: packages/fs/fs/tests/service.spec.ts:122-130
//
// 超限报 [CodeTooLarge]，**不交出截断的结果**——截断的那份看上去是成功的，
// 而它会被当成完整内容去算摘要、去做匹配。
func TestReadBytesEnforcesTheByteCap(t *testing.T) {
	t.Parallel()

	backend := newFakeFS()
	backend.seed(TargetKey("a.bin"), "hi")
	target := resolved(t, backend, "a.bin")

	raw, err := backend.ReadBytes(t.Context(), target, 2)
	if err != nil {
		t.Fatalf("刚好到上限不该失败：%v", err)
	}
	if !bytes.Equal(raw, []byte("hi")) {
		t.Errorf("该读出 %q，实际 %q", "hi", raw)
	}

	_, err = backend.ReadBytes(t.Context(), target, 1)
	requireCode(t, err, CodeTooLarge)

	_, err = backend.ReadBytes(t.Context(), resolved(t, backend, "missing.bin"), 1024)
	requireCode(t, err, CodeNotFound)
}

// TestReadBytesHonoursCancellation 钉住取消在这条路径上也有效。
func TestReadBytesHonoursCancellation(t *testing.T) {
	t.Parallel()

	backend := newFakeFS()
	backend.seed(TargetKey("a.bin"), "hi")
	target := resolved(t, backend, "a.bin")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := backend.ReadBytes(ctx, target, 1024)
	requireCode(t, err, CodeAborted)
}

// TestListDirGivesChildTargetsInAStableOrderWithoutContent 钉住列目录的三条。
//
// 源: packages/fs/fs/tests/service.spec.ts:132-144
//
// 稳定顺序、解析好的子目标、**没有内容**。顺序不稳的话，同一个目录
// 两次列出来会得到两份不同的历史，而模型看得见那个差异。
func TestListDirGivesChildTargetsInAStableOrderWithoutContent(t *testing.T) {
	t.Parallel()

	backend := newFakeFS()
	// 故意按乱序种进去（map 的迭代顺序本来也是随机的）。
	backend.seed(TargetKey("skills/gamma.md"), "ccc")
	backend.seed(TargetKey("skills/alpha.md"), "a")
	backend.seed(TargetKey("skills/beta.md"), "bb")
	// 更深一层的不算直接子项。
	backend.seed(TargetKey("skills/nested/deep.md"), "d")

	entries, err := backend.ListDir(t.Context(), resolved(t, backend, "skills"))
	if err != nil {
		t.Fatalf("列目录不该失败：%v", err)
	}

	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	if strings.Join(names, ",") != "alpha.md,beta.md,gamma.md" {
		t.Errorf("该按名字排好且只含直接子项，实际 %v", names)
	}

	first := entries[0]
	if first.Target.TargetKey != TargetKey("skills/alpha.md") {
		t.Errorf("子目标该是解析好的，实际 %q", first.Target.TargetKey)
	}
	if first.Version == "" {
		t.Error("这个后端拿得到元数据，版本不该缺席")
	}
	if first.Size == nil || *first.Size != 1 {
		t.Errorf("大小该是 1，实际 %v", first.Size)
	}

	_, err = backend.ListDir(t.Context(), resolved(t, backend, "a.txt"))
	requireCode(t, err, CodeNotDirectory)
}

// TestStatReportsAbsenceWithoutAnError 钉住「不存在」不是一次失败。
//
// 源: packages/fs/fs/tests/service.spec.ts:146-151
//
// 新增: DSH 返回 `FsInfo | undefined`，Go 这边是 (Info, bool, error)。
// 分成两个返回值的理由是「探不到」和「探测本身出错了」必须分得开：
// 前者是一个答案，后者是没有答案。
func TestStatReportsAbsenceWithoutAnError(t *testing.T) {
	t.Parallel()

	backend := newFakeFS()

	_, found, err := backend.Stat(t.Context(), resolved(t, backend, "missing.txt"))
	if err != nil {
		t.Fatalf("探不到不该是一次失败：%v", err)
	}
	if found {
		t.Error("不存在的目标不该被报成在场")
	}
}

// TestStatNeverReportsASymlink 钉住 [Info.Type] 上那条「不可能是 symlink」。
//
// 源: packages/fs/fs/src/types.ts:79-80
//
// Stat 跟着链接走，所以它看到的永远是链接指向的那个东西。
// 这条差异正是 [FileSystem.Lstat] 存在的理由。
func TestStatNeverReportsASymlink(t *testing.T) {
	t.Parallel()

	backend := newFakeFS()
	backend.seed(TargetKey("real.txt"), "hi")
	backend.links["link.txt"] = "real.txt"

	// Resolve 跟过去，于是目标那一侧根本看不见这条链接。
	info, found, err := backend.Stat(t.Context(), resolved(t, backend, "real.txt"))
	if err != nil || !found {
		t.Fatalf("目标该在：found=%v err=%v", found, err)
	}
	if info.Type == TypeSymlink {
		t.Error("Stat 不该报出符号链接")
	}
}

// TestStatReportsDirectories 钉住目录也能被探测到。
//
// 策略层靠这一条在读之前就把目录挡掉，而不是让 ReadText 去撞一个错。
func TestStatReportsDirectories(t *testing.T) {
	t.Parallel()

	backend := newFakeFS()
	backend.seed(TargetKey("skills/alpha.md"), "a")

	info, found, err := backend.Stat(t.Context(), resolved(t, backend, "skills"))
	if err != nil || !found {
		t.Fatalf("目录该在：found=%v err=%v", found, err)
	}
	if info.Type != TypeDirectory {
		t.Errorf("该是 %s，实际 %s", TypeDirectory, info.Type)
	}
}

// TestLstatSeesTheLinkItselfBeforeAnyResolution 钉住路径级探测的那条关键差异。
//
// 源: packages/fs/fs/tests/service.spec.ts:153-160
//
// 它报得出 [TypeSymlink]，于是带信任边界规则的消费方可以在
// [FileSystem.Resolve] 跟过去**之前**就把一条链接拒掉——跟过去之后就看不出来了。
func TestLstatSeesTheLinkItselfBeforeAnyResolution(t *testing.T) {
	t.Parallel()

	backend := newFakeFS()
	backend.seed(TargetKey("a.txt"), "hi")
	backend.links["link.txt"] = "a.txt"

	info, found, err := backend.Lstat(t.Context(), "a.txt", "")
	if err != nil || !found {
		t.Fatalf("路径该在：found=%v err=%v", found, err)
	}
	if info.Type != TypeFile {
		t.Errorf("该是 %s，实际 %s", TypeFile, info.Type)
	}

	link, found, err := backend.Lstat(t.Context(), "link.txt", "")
	if err != nil || !found {
		t.Fatalf("链接该在：found=%v err=%v", found, err)
	}
	if link.Type != TypeSymlink {
		t.Errorf("该报出 %s，实际 %s", TypeSymlink, link.Type)
	}

	// cwd 的规则和 Resolve 一样。
	if _, found, _ := backend.Lstat(t.Context(), "a.txt", "/work"); found {
		t.Error("换了基准之后不该还找得到")
	}

	if _, found, err := backend.Lstat(t.Context(), "missing.txt", ""); err != nil || found {
		t.Errorf("不存在的路径该安静地报不在：found=%v err=%v", found, err)
	}
}

// TestAnUnconditionalWriteCreatesThenReplaces 钉住无守卫写的两种结果。
//
// 源: packages/fs/fs/tests/service.spec.ts:72-76
//
// [WriteOutcome.Operation] 分开创建和替换，[WriteOutcome.Before] 只在替换时有值。
func TestAnUnconditionalWriteCreatesThenReplaces(t *testing.T) {
	t.Parallel()

	backend := newFakeFS()
	target := resolved(t, backend, "a.txt")

	created, err := backend.WriteText(t.Context(), target, "hi", nil)
	if err != nil {
		t.Fatalf("创建不该失败：%v", err)
	}
	if created.Operation != OperationCreate {
		t.Errorf("该是 %s，实际 %s", OperationCreate, created.Operation)
	}
	if created.Before != nil {
		t.Errorf("创建没有基准，实际 %q", *created.Before)
	}
	if created.After != "hi" {
		t.Errorf("写后内容该是 %q，实际 %q", "hi", created.After)
	}

	updated, err := backend.WriteText(t.Context(), target, "bye", nil)
	if err != nil {
		t.Fatalf("替换不该失败：%v", err)
	}
	if updated.Operation != OperationUpdate {
		t.Errorf("该是 %s，实际 %s", OperationUpdate, updated.Operation)
	}
	if updated.Before == nil || *updated.Before != "hi" {
		t.Errorf("基准该是上一份内容，实际 %v", updated.Before)
	}
	if updated.Version == created.Version {
		t.Error("每次写都该盖一个新版本")
	}
}

// TestAnEmptyFileIsRealContentNotAMissingBaseline 钉住 [WriteOutcome.Before] 用指针的理由。
//
// 源: packages/fs/fs/src/types.ts:127-144
//
// 空文件的内容**就是**空串。用空串表示缺席的话，一次「把空文件写成有内容」
// 会被当成没有基准，于是退回整文件 diff，把一次显然的新增显示成一次全文替换。
func TestAnEmptyFileIsRealContentNotAMissingBaseline(t *testing.T) {
	t.Parallel()

	backend := newFakeFS()
	target := resolved(t, backend, "a.txt")

	if _, err := backend.WriteText(t.Context(), target, "", nil); err != nil {
		t.Fatalf("写空文件不该失败：%v", err)
	}

	outcome, err := backend.WriteText(t.Context(), target, "hi", nil)
	if err != nil {
		t.Fatalf("覆盖不该失败：%v", err)
	}
	if outcome.Before == nil {
		t.Fatal("空文件是一份真实的基准，不该是缺席")
	}
	if *outcome.Before != "" {
		t.Errorf("那份基准该是空串，实际 %q", *outcome.Before)
	}
}

// TestCreateIfAbsentRefusesAnExistingTarget 钉住这个守卫报的是 [CodeNotObserved]。
//
// 源: packages/fs/fs/src/types.ts:117-125
func TestCreateIfAbsentRefusesAnExistingTarget(t *testing.T) {
	t.Parallel()

	backend := newFakeFS()
	target := resolved(t, backend, "a.txt")

	if _, err := backend.WriteText(t.Context(), target, "hi", CreateIfAbsent{}); err != nil {
		t.Fatalf("第一次创建不该失败：%v", err)
	}

	_, err := backend.WriteText(t.Context(), target, "again", CreateIfAbsent{})
	requireCode(t, err, CodeNotObserved)

	// 被拒之后内容一个字都没变。
	text, err := backend.ReadText(t.Context(), target)
	if err != nil {
		t.Fatalf("读不该失败：%v", err)
	}
	if text != "hi" {
		t.Errorf("被拒的写不该落地，实际 %q", text)
	}
}

// TestReplaceIfVersionRefusesAStaleOrAbsentTarget 钉住版本守卫的两种拒绝。
//
// 源: packages/fs/fs/src/types.ts:117-125
//
// 目标不在也报 [CodeStaleVersion]：调用方拿着一个版本来，
// 说明它以为那份内容还在——「不在了」和「变了」对它是同一件事。
func TestReplaceIfVersionRefusesAStaleOrAbsentTarget(t *testing.T) {
	t.Parallel()

	backend := newFakeFS()
	target := resolved(t, backend, "a.txt")

	_, err := backend.WriteText(t.Context(), target, "hi", ReplaceIfVersion{Version: Version("v1")})
	requireCode(t, err, CodeStaleVersion)

	created, err := backend.WriteText(t.Context(), target, "hi", nil)
	if err != nil {
		t.Fatalf("创建不该失败：%v", err)
	}

	replaced, err := backend.WriteText(
		t.Context(), target, "bye", ReplaceIfVersion{Version: created.Version},
	)
	if err != nil {
		t.Fatalf("拿着当前版本替换不该失败：%v", err)
	}
	if replaced.Operation != OperationUpdate {
		t.Errorf("该是 %s，实际 %s", OperationUpdate, replaced.Operation)
	}

	// 同一个版本用第二次就陈旧了。
	_, err = backend.WriteText(
		t.Context(), target, "third", ReplaceIfVersion{Version: created.Version},
	)
	requireCode(t, err, CodeStaleVersion)
}

// TestEditTextReplacesExactlyOneLiteralMatch 钉住字面编辑的正常路径。
//
// 源: packages/fs/fs/src/types.ts:146-168
func TestEditTextReplacesExactlyOneLiteralMatch(t *testing.T) {
	t.Parallel()

	backend := newFakeFS()
	backend.seed(TargetKey("a.txt"), "alpha beta gamma")
	target := resolved(t, backend, "a.txt")

	outcome, err := backend.EditText(
		t.Context(), target, EditRequest{OldString: "beta", NewString: "BETA"}, nil,
	)
	if err != nil {
		t.Fatalf("编辑不该失败：%v", err)
	}
	if outcome.Before != "alpha beta gamma" {
		t.Errorf("基准该是编辑前的整份内容，实际 %q", outcome.Before)
	}
	if outcome.After != "alpha BETA gamma" {
		t.Errorf("编辑后该是 %q，实际 %q", "alpha BETA gamma", outcome.After)
	}
}

// TestEditTextRefusesZeroOrAmbiguousMatches 钉住两个匹配失败各自的码。
//
// 源: packages/fs/fs/src/types.ts:146-154
//
// 多处匹配却只改第一处的话，调用方会以为改完了，而剩下的几处还是旧的。
func TestEditTextRefusesZeroOrAmbiguousMatches(t *testing.T) {
	t.Parallel()

	backend := newFakeFS()
	backend.seed(TargetKey("a.txt"), "x y x")
	target := resolved(t, backend, "a.txt")

	_, err := backend.EditText(
		t.Context(), target, EditRequest{OldString: "zzz", NewString: "!"}, nil,
	)
	requireCode(t, err, CodeEditNotFound)

	_, err = backend.EditText(
		t.Context(), target, EditRequest{OldString: "x", NewString: "!"}, nil,
	)
	requireCode(t, err, CodeAmbiguousEdit)

	outcome, err := backend.EditText(
		t.Context(), target, EditRequest{OldString: "x", NewString: "!", ReplaceAll: true}, nil,
	)
	if err != nil {
		t.Fatalf("ReplaceAll 该放行：%v", err)
	}
	if outcome.After != "! y !" {
		t.Errorf("该改掉每一处，实际 %q", outcome.After)
	}
}

// TestEditTextChecksTheVersionBeforeMatching 钉住那条顺序。
//
// 源: packages/fs/fs/src/index.ts:230-249
//
// 内容陈旧时报的必须是 [CodeStaleVersion] 而不是 [CodeEditNotFound]——
// 后者会让调用方以为是自己的搜索串写错了，然后换个串再试一次，
// 而它每一次都在改别人刚写下的内容。所以这里故意用一个**匹配不上**的串：
// 顺序反了的话，报出来的会是 EditNotFound。
func TestEditTextChecksTheVersionBeforeMatching(t *testing.T) {
	t.Parallel()

	backend := newFakeFS()
	backend.seed(TargetKey("a.txt"), "alpha")
	target := resolved(t, backend, "a.txt")

	_, err := backend.EditText(
		t.Context(),
		target,
		EditRequest{OldString: "这段内容根本不在文件里", NewString: "!"},
		&EditIntent{Version: Version("对不上的版本")},
	)
	requireCode(t, err, CodeStaleVersion)
}

// TestEditingAnAbsentTargetIsAStaleVersion 钉住编辑路径上没有「文件本来不在」这一说。
//
// 源: packages/fs/fs/src/types.ts:156-168
//
// 这正是 [EditOutcome.Before] 是值而不是指针的理由：一次成功的编辑
// 必然有一份被编辑的内容，不存在没有基准的情况。
func TestEditingAnAbsentTargetIsAStaleVersion(t *testing.T) {
	t.Parallel()

	backend := newFakeFS()
	target := resolved(t, backend, "missing.txt")

	_, err := backend.EditText(
		t.Context(), target, EditRequest{OldString: "a", NewString: "b"}, nil,
	)
	requireCode(t, err, CodeStaleVersion)
}

// TestEditTextAcceptsTheCurrentVersion 钉住带对了版本的编辑走得通。
//
// 只验拒绝不验放行的话，一条恒拒的实现也能通过上面那两条。
func TestEditTextAcceptsTheCurrentVersion(t *testing.T) {
	t.Parallel()

	backend := newFakeFS()
	target := resolved(t, backend, "a.txt")

	written, err := backend.WriteText(t.Context(), target, "alpha", nil)
	if err != nil {
		t.Fatalf("写不该失败：%v", err)
	}

	outcome, err := backend.EditText(
		t.Context(),
		target,
		EditRequest{OldString: "alpha", NewString: "omega"},
		&EditIntent{Version: written.Version},
	)
	if err != nil {
		t.Fatalf("拿着当前版本编辑不该失败：%v", err)
	}
	if outcome.After != "omega" {
		t.Errorf("该改成 %q，实际 %q", "omega", outcome.After)
	}
	if outcome.Version == written.Version {
		t.Error("编辑该盖一个新版本")
	}
}
