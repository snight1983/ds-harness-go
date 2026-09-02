// 本文件的作用：压读那一侧——列举、按身份定位、原样读回、以及那几道
// 「这份存档不是我该读的东西」的拒绝。
//
// 为什么单独一个文件：recovery_test.go 压的是**写完之后崩了**，它走的是
// 写路径加一次修复。读那一侧另有一整套只有列举和装载才会走到的分支：一个根下
// 有多个工程目录、一个目录里躺着别的编码的存档、一份头写了一半、早先那种平铺
// 排布。这些分支在写路径上一次都碰不到，所以在那边补不出来。
//
// 源: packages/session/session-persistence-jsonl/tests/jsonl.spec.ts:76-604

package jsonl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/session/persistence"
)

// seededStore 造一个存储，并在里面落一个写完了一个回合的会话。
//
// 读那一侧的每条用例都要先有点东西可读，这一步在下面重复十来次。
func seededStore(t *testing.T, config Config) (*Store, string, session.SessionHeader) {
	t.Helper()

	store, root := newTestStore(t, config)
	meta := testMeta("read-me", testCwd(t, "/proj"))
	mustCreate(t, store, meta)
	mustAppend(t, store, meta.ID, oneTurnLog(t))
	return store, root, meta
}

func TestBackendReportsItsNameAndAbsoluteRoot(t *testing.T) {
	store, root := newTestStore(t, Config{})
	backend := store.Backend()

	if backend.Name() != BackendName {
		t.Errorf("后端名是 %q，要的是 %q", backend.Name(), BackendName)
	}
	// 根必须当场解成绝对路径：晚一点再解，本进程工作目录的一次改动就能把同一个
	// 后端劈成两个根。
	if !filepath.IsAbs(backend.Root()) {
		t.Errorf("根 %q 不是绝对路径", backend.Root())
	}
	if !sameFile(t, backend.Root(), root) {
		t.Errorf("根是 %q，要的是 %q", backend.Root(), root)
	}
}

func TestLocateAnswersWithoutTouchingTheDisk(t *testing.T) {
	store, root := newTestStore(t, Config{})
	meta := testMeta("never-written", testCwd(t, "/proj"))

	location, ok := store.Locate(meta)
	if !ok {
		t.Fatal("一个还没落地的会话也该有「它会在哪」这个答案")
	}
	if location.Kind != "jsonl" {
		t.Errorf("存档种类是 %q，要的是 %q", location.Kind, "jsonl")
	}
	if want := storedPath(t, root, meta.Cwd, meta.ID); location.Path != want {
		t.Errorf("位置是 %q，要的是 %q", location.Path, want)
	}
	// 说了不碰盘，那就真的一个字节都不该落下去。
	if _, err := os.Stat(location.Path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Locate 之后 %q 竟然存在了", location.Path)
	}
	if !store.SupportsRawArtifacts() {
		t.Error("这个后端就是逐会话一份文件，SupportsRawArtifacts 该恒真")
	}
}

// 头里的身份编不出一段路径来时，「它在哪」这个问题本身没有答案，
// 那时候返回一个拼错的路径比返回 false 危险得多。
func TestLocateRefusesAHeaderThatEncodesToNoPath(t *testing.T) {
	store, _ := newTestStore(t, Config{})

	if _, ok := store.Locate(testMeta("", testCwd(t, "/proj"))); ok {
		t.Error("空会话标识编不出路径，该报 false")
	}
}

func TestReadRawGivesBackTheBytesTheBackendWrote(t *testing.T) {
	ctx := context.Background()
	store, root, meta := seededStore(t, Config{})

	artifact, err := store.ReadRaw(ctx, meta.ID)
	if err != nil {
		t.Fatalf("原样读失败：%v", err)
	}
	if artifact.Meta.ID != meta.ID {
		t.Errorf("读回来的头是 %q，要的是 %q", string(artifact.Meta.ID), string(meta.ID))
	}
	if artifact.Filename != artifactFilename {
		t.Errorf("文件名是 %q，要的是 %q", artifact.Filename, artifactFilename)
	}
	// 「逐字节原样」是这条路的全部意义：它是给人看、给人比对的，
	// 从解出来的事件重新拼一遍就看不出后端到底写了什么。
	if want := readStored(t, storedPath(t, root, meta.Cwd, meta.ID)); artifact.Content != want {
		t.Errorf("读回来的字节和盘上的对不上\n读到：%q\n盘上：%q", artifact.Content, want)
	}
}

func TestReadRawReportsAMissingSessionAsNotFound(t *testing.T) {
	store, _ := newTestStore(t, Config{})

	_, err := store.ReadRaw(context.Background(), "nobody")
	if !errors.Is(err, persistence.ErrSessionNotFound) {
		t.Fatalf("该报 ErrSessionNotFound，实际 %v", err)
	}
}

// 第一行不是这个会话的头，说明这份存档和它所在的位置对不上。继续读下去会把
// 别人的历史当成这个会话的历史交出去，那比读不出来坏得多。
func TestReadRawRefusesAnArtifactWhoseHeaderIsNotItsOwn(t *testing.T) {
	store, root, meta := seededStore(t, Config{})
	path := storedPath(t, root, meta.Cwd, meta.ID)

	body := readStored(t, path)
	_, rest, _ := strings.Cut(body, "\n")
	// 头换成另一个会话的，剩下的事件一字不动。
	forged := strings.Replace(readStored(t, path), `"id":"read-me"`, `"id":"someone-else"`, 1)
	if forged == body {
		t.Fatalf("没能把头里的身份改掉，盘上是：%q", body)
	}
	_ = rest
	if err := os.WriteFile(path, []byte(forged), 0o600); err != nil {
		t.Fatalf("写回改过的存档失败：%v", err)
	}

	if _, err := store.ReadRaw(context.Background(), meta.ID); err == nil {
		t.Fatal("头里的身份和要读的会话对不上，该拒收")
	}
}

func TestListAndListSnapshotsSeeEveryMaterializedSession(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t, Config{})

	// 两个会话摆在**不同**的工程目录下：列举要走遍整个根，只扫一层就会漏掉一个。
	first := testMeta("alpha", testCwd(t, "/proj-a"))
	second := testMeta("beta", testCwd(t, "/proj-b"))
	for _, meta := range []session.SessionHeader{first, second} {
		mustCreate(t, store, meta)
		mustAppend(t, store, meta.ID, oneTurnLog(t))
	}
	// 第三个只建不写：落地是懒的，一个从没追加过的会话在盘上什么都不留，
	// 所以它**不该**出现在列举里。
	mustCreate(t, store, testMeta("ghost", testCwd(t, "/proj-c")))

	headers, err := store.List(ctx)
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	if got := idsOf(headers); !slices.Equal(got, []string{"alpha", "beta"}) {
		t.Fatalf("列举出来的是 %v，要的是 [alpha beta]", got)
	}

	snapshots, err := store.ListSnapshots(ctx)
	if err != nil {
		t.Fatalf("列举快照失败：%v", err)
	}
	if len(snapshots) != 2 {
		t.Fatalf("快照有 %d 份，要的是 2 份", len(snapshots))
	}
	for _, snapshot := range snapshots {
		// 令牌是「变没变」这个回合的全部依据，空的话那个回合走不通。
		if snapshot.Revision == "" {
			t.Errorf("%q 的变更令牌是空的", string(snapshot.Header.ID))
		}
	}
}

// 令牌要跟着内容动。不动的话，「读一遍、再核对一遍看变没变」永远说「没变」，
// 而那正是并发写检测唯一的抓手。
func TestSnapshotRevisionMovesWhenTheLogGrows(t *testing.T) {
	ctx := context.Background()
	store, _, meta := seededStore(t, Config{})

	before := revisionOf(t, store, meta.ID)
	mustAppend(t, store, meta.ID, []session.Event{
		turnStartEvent(t, 6, 8, 2),
		userMessageEvent(t, 7, 9, "again"),
		turnEndEvent(t, 8, 10, 2),
	})
	if after := revisionOf(t, store, meta.ID); after == before {
		t.Fatalf("追加之后变更令牌还是 %q，没动", string(before))
	}
	_ = ctx
}

func TestListSkipsADirectoryWhoseHeaderIsNotAHeader(t *testing.T) {
	ctx := context.Background()
	store, root, meta := seededStore(t, Config{})

	// 手工摆一个会话目录，里面那份日志的第一行是合法 JSON 但不是一份会话头。
	// 列举该跳过它——这是正常控制流，不是损坏：这个根下面完全可能躺着别人的文件。
	bogus := filepath.Join(filepath.Dir(filepath.Dir(storedPath(t, root, meta.Cwd, meta.ID))), "~006E~006F~0074")
	if err := os.MkdirAll(bogus, 0o750); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	if err := os.WriteFile(filepath.Join(bogus, logBaseName+logSuffix(CompressionNone)),
		[]byte("{\"not\":\"a header\"}\n"), 0o600); err != nil {
		t.Fatalf("写假存档失败：%v", err)
	}

	headers, err := store.List(ctx)
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	if got := idsOf(headers); !slices.Equal(got, []string{"read-me"}) {
		t.Fatalf("列举出来的是 %v，要的是 [read-me]", got)
	}
}

// 一份连第一行都没写完的存档也该被跳过：那是一次崩在落地当中的写，
// 不是一份读得了的存档。
func TestListSkipsAnArtifactWithNoCompleteFirstLine(t *testing.T) {
	ctx := context.Background()
	store, root, meta := seededStore(t, Config{})

	half := filepath.Join(filepath.Dir(filepath.Dir(storedPath(t, root, meta.Cwd, meta.ID))), "~0068~0061~006C~0066")
	if err := os.MkdirAll(half, 0o750); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	if err := os.WriteFile(filepath.Join(half, logBaseName+logSuffix(CompressionNone)),
		[]byte(`{"version":1,"id":"hal`), 0o600); err != nil {
		t.Fatalf("写半截存档失败：%v", err)
	}

	headers, err := store.List(ctx)
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	if got := idsOf(headers); !slices.Equal(got, []string{"read-me"}) {
		t.Fatalf("列举出来的是 %v，要的是 [read-me]", got)
	}
}

// 同一个身份在两个工程目录下各有一份日志，就没法回答「这个会话的历史是哪一份」。
// 挑一份读是最坏的选择：它会安静地把另一份的历史丢掉。
func TestDuplicateSessionAcrossProjectsIsRefusedNotPicked(t *testing.T) {
	ctx := context.Background()
	store, root, meta := seededStore(t, Config{})

	// 双胞胎那份的头必须指向**它自己**所在的工程目录，否则先撞上的是「头里的身份
	// 和它躺的位置对不上」那道判据，重号这条路根本走不到。
	elsewhere := testCwd(t, "/elsewhere")
	twin := storedPath(t, root, elsewhere, meta.ID)
	header, err := encodeHeaderLine(testMeta(meta.ID, elsewhere))
	if err != nil {
		t.Fatalf("编不出双胞胎的头：%v", err)
	}
	_, body, _ := strings.Cut(readStored(t, storedPath(t, root, meta.Cwd, meta.ID)), "\n")
	if err := os.MkdirAll(filepath.Dir(twin), 0o750); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	if err := os.WriteFile(twin, append(append(header, '\n'), body...), 0o600); err != nil {
		t.Fatalf("写双胞胎存档失败：%v", err)
	}

	if _, err := store.ReadRaw(ctx, meta.ID); err == nil ||
		!strings.Contains(err.Error(), "多个工程目录") {
		t.Fatalf("按身份定位该拒掉重号，实际 %v", err)
	}
	if _, err := store.List(ctx); err == nil || !strings.Contains(err.Error(), "多个工程目录") {
		t.Fatalf("列举该拒掉重号，实际 %v", err)
	}
}

// 另一种物理编码的存档必须当场喊出来。安静跳过的话，一个配错了编码的进程会
// 把满盘的历史读成「一个会话都没有」，然后在上面新建一批同号的。
func TestAnArtifactInTheOtherEncodingIsRefusedLoudly(t *testing.T) {
	ctx := context.Background()
	store, root, meta := seededStore(t, Config{})

	zstd := filepath.Join(filepath.Dir(storedPath(t, root, meta.Cwd, meta.ID)),
		logBaseName+logSuffix(CompressionZstd))
	if err := os.WriteFile(zstd, []byte("not really zstd"), 0o600); err != nil {
		t.Fatalf("摆一份别的编码的存档失败：%v", err)
	}

	// 定位、列举两条路各自都要撞上它——只挡住一条，另一条就是那个安静的洞。
	if _, err := store.ReadRaw(ctx, meta.ID); err == nil ||
		!strings.Contains(err.Error(), "换一个根") {
		t.Fatalf("定位该拒掉别的编码，实际 %v", err)
	}
	fresh, _ := newTestStore(t, Config{Root: root})
	if _, err := fresh.List(ctx); err == nil || !strings.Contains(err.Error(), "换一个根") {
		t.Fatalf("列举该拒掉别的编码，实际 %v", err)
	}
}

// 早先那种「工程目录下直接摆一个 <会话>.jsonl」的排布不再支持。它和现在这种
// 排布长得像到能被当成一个会话目录扫过去，所以必须显式拒。
func TestTheLegacyFlatLayoutIsRefusedWithInstructions(t *testing.T) {
	ctx := context.Background()
	store, root, meta := seededStore(t, Config{})

	project := filepath.Dir(filepath.Dir(storedPath(t, root, meta.Cwd, meta.ID)))
	// 用这个会话自己的路径分量命名：按身份定位只查「这个身份的平铺存档在不在」，
	// 换个名字就只有列举那条路撞得上，两条路要一起验。
	segment, err := encodeSegment(string(meta.ID))
	if err != nil {
		t.Fatalf("编不出路径分量：%v", err)
	}
	flat := filepath.Join(project, segment+logSuffix(CompressionNone))
	if err := os.WriteFile(flat, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("摆一份平铺存档失败：%v", err)
	}

	fresh, _ := newTestStore(t, Config{Root: root})
	_, err = fresh.List(ctx)
	if err == nil || !strings.Contains(err.Error(), "平铺排布") {
		t.Fatalf("列举该拒掉平铺排布，实际 %v", err)
	}
	// 拒绝要能自己带人走出去：说清楚该怎么办。
	if !strings.Contains(err.Error(), "挪进") {
		t.Errorf("拒绝里该写清怎么办，实际 %q", err.Error())
	}
	if _, err := store.ReadRaw(ctx, meta.ID); err == nil ||
		!strings.Contains(err.Error(), "平铺排布") {
		t.Fatalf("按身份定位也该拒掉平铺排布，实际 %v", err)
	}
}

func TestInspectReadsWithoutPublishingOrRepairing(t *testing.T) {
	ctx := context.Background()
	store, root, meta := seededStore(t, Config{})
	path := storedPath(t, root, meta.Cwd, meta.ID)

	// 贴一截没写完的尾巴：Inspect 说了不落盘恢复，那盘上的字节就一个都不许动。
	appendRawBytes(t, path, `{"type":"turn/start","seq":6,"ti`)
	before := readStored(t, path)

	inspected, err := store.Inspect(ctx, meta.ID)
	if err != nil {
		t.Fatalf("查看失败：%v", err)
	}
	if got, want := seqsOf(inspected.Events), []int{0, 1, 2, 3, 4, 5}; !slices.Equal(got, want) {
		t.Fatalf("查看到的 seq 是 %v，要的是 %v", got, want)
	}
	if after := readStored(t, path); after != before {
		t.Errorf("Inspect 动了盘上的字节\n之前：%q\n之后：%q", before, after)
	}
}

func TestReadFromGivesBackOnlyTheRequestedSuffix(t *testing.T) {
	ctx := context.Background()
	store, _, meta := seededStore(t, Config{})

	suffix, err := store.ReadFrom(ctx, meta.ID, 3)
	if err != nil {
		t.Fatalf("读后缀失败：%v", err)
	}
	if got, want := seqsOf(suffix.Events), []int{3, 4, 5}; !slices.Equal(got, want) {
		t.Fatalf("后缀的 seq 是 %v，要的是 %v", got, want)
	}
	if suffix.Meta.ID != meta.ID {
		t.Errorf("后缀带的头是 %q，要的是 %q", string(suffix.Meta.ID), string(meta.ID))
	}

	// 水位在末尾之后：空后缀是正常答案，不是错。
	beyond, err := store.ReadFrom(ctx, meta.ID, 99)
	if err != nil {
		t.Fatalf("水位越过末尾该给空后缀，实际报错：%v", err)
	}
	if len(beyond.Events) != 0 {
		t.Errorf("越过末尾还读出了 %d 条", len(beyond.Events))
	}
}

func TestPrepareRebuildsALiveSessionThatIsNotYetPublished(t *testing.T) {
	ctx := context.Background()
	store, _, meta := seededStore(t, Config{})

	prepared, err := store.Prepare(ctx, meta.ID)
	if err != nil {
		t.Fatalf("准备失败：%v", err)
	}
	if prepared == nil {
		t.Fatal("准备出来的会话是 nil")
	}
}

func TestInstallReturnsATeardownThatDetaches(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestStore(t, Config{})

	owner, err := scope.New(scope.NewKey("jsonl-install-test"), scope.Options{})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	detach, err := store.Install(ctx, owner)
	if err != nil {
		t.Fatalf("挂载失败：%v", err)
	}
	if detach == nil {
		t.Fatal("挂载该交回一个摘下来的函数")
	}
	if err := detach(ctx); err != nil {
		t.Fatalf("摘下来失败：%v", err)
	}
}

// readFirstLine 是列举那条路上唯一的读——一份巨大的会话日志在那里只花一次头的读。
// 它的三种回答各有各的含义，混起来就会把「文件是空的」读成「头是空串」。
func TestReadFirstLineSeparatesEmptyFromIncompleteFromWhole(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name     string
		body     string
		want     string
		complete bool
	}{
		{"空文件", "", "", false},
		{"只有半行", `{"version":1`, "", false},
		{"恰好一行", "头\n", "头", true},
		{"一行之后还有别的", "头\n事件\n更多\n", "头", true},
		// 8192 是它内部那个读缓冲的大小，换行落在第二块里才走得到跨块拼接。
		{"换行落在第二个读块里", strings.Repeat("x", 9000) + "\n尾巴\n", strings.Repeat("x", 9000), true},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			path := filepath.Join(dir, item.name)
			if err := os.WriteFile(path, []byte(item.body), 0o600); err != nil {
				t.Fatalf("写文件失败：%v", err)
			}
			line, complete, err := readFirstLine(path)
			if err != nil {
				t.Fatalf("读第一行失败：%v", err)
			}
			if complete != item.complete {
				t.Fatalf("complete 是 %v，要的是 %v", complete, item.complete)
			}
			if complete && string(line) != item.want {
				t.Errorf("读到 %q，要的是 %q", string(line), item.want)
			}
		})
	}

	if _, _, err := readFirstLine(filepath.Join(dir, "根本没有这个文件")); err == nil {
		t.Error("文件不存在该报错，不该说「没有完整的第一行」")
	}
}

// idsOf 把一串头的会话标识抽成字符串并排好序，好和期望的那串比。
func idsOf(headers []session.SessionHeader) []string {
	ids := make([]string, 0, len(headers))
	for _, header := range headers {
		ids = append(ids, string(header.ID))
	}
	slices.Sort(ids)
	return ids
}

// revisionOf 取一个会话此刻的变更令牌。
func revisionOf(t *testing.T, store *Store, id session.SessionID) persistence.Revision {
	t.Helper()

	snapshots, err := store.ListSnapshots(context.Background())
	if err != nil {
		t.Fatalf("列举快照失败：%v", err)
	}
	for _, snapshot := range snapshots {
		if snapshot.Header.ID == id {
			return snapshot.Revision
		}
	}
	t.Fatalf("快照里没有 %q", string(id))
	return ""
}

// sameFile 比较两个路径是否指向同一处，先各自求符号链接。
func sameFile(t *testing.T, left, right string) bool {
	t.Helper()

	resolve := func(path string) string {
		if evaluated, err := filepath.EvalSymlinks(path); err == nil {
			return evaluated
		}
		return path
	}
	return resolve(left) == resolve(right)
}
