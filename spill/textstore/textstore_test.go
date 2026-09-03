// 本文件的作用：把这台存储钉在「名字是派生出来的、产物一个字都不少」这两件事上。
//
// # 这些测试防的是什么错
//
//   - **建议名被当成了路径**。它一路来自工具名，工具名可由插件自定；
//     这条塌了，一个 `../` 就把产物写到产物树外面去。
//   - **撞名变成了覆盖**。同一个键上写第二次必须报错——静默覆盖会毁掉在先那份
//     产物，而上层敢把结果挪走的依据正是「挪走的那份还在」。
//   - **会话 id 顺着句柄泄漏给模型**。句柄里只许有哈希。
//   - **字节数是估出来的**。保留策略拿它算省下多少上下文，估一下就悄悄偏了。
//   - **内容被截断或者转码**。存进去和读出来必须逐字节相同。

package textstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/fs"
	"github.com/snight1983/ds-harness-go/fs/fstest"
	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/spill"
)

// root 是所有用例共用的产物树根。
const root = "/spill/v1"

// hint 是所有用例共用的取回说明。
const hint = "Fetch this locator through the host's retrieval channel."

// keyShape 钉住键的形状：会话目录是 12 位十六进制，名字前缀是 16 位十六进制，
// 而且中间只有一个分隔符——建议名净化之后再也拼不出第二个。
var keyShape = regexp.MustCompile(`^` + root + `/session-[0-9a-f]{12}/[0-9a-f]{16}-[A-Za-z0-9._-]*$`)

// countingRand 每次读都给出一段确定的、互不相同的字节。
type countingRand struct{ next byte }

func (c *countingRand) Read(out []byte) (int, error) {
	for index := range out {
		out[index] = c.next
	}
	c.next++
	return len(out), nil
}

// stuckRand 每次读都给出同一段字节，用来逼出撞名那条路。
type stuckRand struct{}

func (stuckRand) Read(out []byte) (int, error) {
	for index := range out {
		out[index] = 0xab
	}
	return len(out), nil
}

// brokenRand 取不到随机字节。
type brokenRand struct{}

var errNoEntropy = errors.New("熵没了")

func (brokenRand) Read([]byte) (int, error) { return 0, errNoEntropy }

func newStore(t *testing.T, tune ...func(*Config)) (*Store, *fstest.FS) {
	t.Helper()
	medium := fstest.New()
	config := Config{FS: medium, Root: root, Rand: &countingRand{next: 1}, RetrievalHint: hint}
	for _, apply := range tune {
		apply(&config)
	}
	store, err := New(config)
	if err != nil {
		t.Fatalf("装配存储：%v", err)
	}
	return store, medium
}

// saveText 是一份填好的请求，除了用例要改的那几处。
func saveText(sessionID string, name string, content string) spill.SaveText {
	return spill.SaveText{
		Owner:         spill.Owner{SessionID: session.SessionID(sessionID)},
		Source:        spill.Source{ToolName: "web_fetch", CallID: "call-1", Label: "result"},
		SuggestedName: name,
		Content:       content,
	}
}

// readBack 把介质上那个键的字节读回来。
func readBack(t *testing.T, medium *fstest.FS, key fs.TargetKey) []byte {
	t.Helper()
	target, err := medium.Resolve(context.Background(), string(key), "")
	if err != nil {
		t.Fatalf("解析 %s：%v", key, err)
	}
	data, err := medium.ReadBytes(context.Background(), target, 1<<20)
	if err != nil {
		t.Fatalf("读回 %s：%v", key, err)
	}
	return data
}

func TestSaveTextLandsUnderTheSessionDirectoryAndReportsTheExactByteCount(t *testing.T) {
	store, medium := newStore(t)
	// 特意用一段多字节文本：字节数必须按 UTF-8 算，不是按字符数。
	content := "外置的正是那种大到装不下的结果"

	ref, err := store.SaveText(context.Background(), saveText("session-α", "web_fetch.txt", content))
	if err != nil {
		t.Fatalf("外置：%v", err)
	}

	keys := medium.Keys()
	if len(keys) != 1 {
		t.Fatalf("介质上要且只要一个产物，拿到 %v", keys)
	}
	if !keyShape.MatchString(string(keys[0])) {
		t.Fatalf("键的形状不对：%s", keys[0])
	}
	sum := sha256.Sum256([]byte("session-α"))
	if want := root + "/session-" + hex.EncodeToString(sum[:])[:sessionHexLen] + "/"; !strings.HasPrefix(string(keys[0]), want) {
		t.Fatalf("会话目录要 %s，拿到 %s", want, keys[0])
	}
	if !strings.HasSuffix(string(keys[0]), "-web_fetch.txt") {
		t.Fatalf("建议名没留在尾巴上：%s", keys[0])
	}

	if ref.Bytes != len(content) {
		t.Fatalf("字节数要 %d，拿到 %d", len(content), ref.Bytes)
	}
	if ref.RetrievalHint != hint {
		t.Fatalf("取回说明要原样交出，拿到 %q", ref.RetrievalHint)
	}
	if string(ref.Locator) != string(keys[0]) {
		t.Fatalf("句柄要指向落地的那个键，拿到 %s", ref.Locator)
	}
	if got := readBack(t, medium, keys[0]); string(got) != content {
		t.Fatalf("存下来的不是交进去的那段文本：%q", got)
	}
}

func TestTheSessionIdNeverReachesTheLocator(t *testing.T) {
	store, _ := newStore(t)
	const sessionID = "operator-42-secret-session"

	ref, err := store.SaveText(context.Background(), saveText(sessionID, "web_fetch.txt", "x"))
	if err != nil {
		t.Fatalf("外置：%v", err)
	}
	if strings.Contains(string(ref.Locator), sessionID) {
		t.Fatalf("会话 id 泄漏进了句柄：%s", ref.Locator)
	}
}

func TestTwoSpillsFromOneSessionNeverShareAKey(t *testing.T) {
	store, medium := newStore(t)
	input := saveText("s", "web_fetch.txt", "第一份")

	first, err := store.SaveText(context.Background(), input)
	if err != nil {
		t.Fatalf("第一次外置：%v", err)
	}
	input.Content = "第二份"
	second, err := store.SaveText(context.Background(), input)
	if err != nil {
		t.Fatalf("第二次外置：%v", err)
	}

	if first.Locator == second.Locator {
		t.Fatalf("同一个会话里连着两次外置拿到了同一个句柄：%s", first.Locator)
	}
	if keys := medium.Keys(); len(keys) != 2 {
		t.Fatalf("介质上要有两份产物，拿到 %v", keys)
	}
	if got := readBack(t, medium, fs.TargetKey(first.Locator)); string(got) != "第一份" {
		t.Fatalf("第一份被第二次写覆盖了：%q", got)
	}
}

func TestDifferentSessionsLandInDifferentDirectories(t *testing.T) {
	store, medium := newStore(t)

	for _, sessionID := range []string{"a", "b"} {
		if _, err := store.SaveText(context.Background(), saveText(sessionID, "n.txt", "x")); err != nil {
			t.Fatalf("外置 %s：%v", sessionID, err)
		}
	}

	dirs := map[string]bool{}
	for _, key := range medium.Keys() {
		dirs[string(key)[:strings.LastIndexByte(string(key), '/')]] = true
	}
	if len(dirs) != 2 {
		t.Fatalf("两个会话要落到两层目录，拿到 %v", dirs)
	}
}

func TestACollidingKeyIsAnErrorNotAnOverwrite(t *testing.T) {
	// 随机源卡住不动，于是第二次算出的键和第一次一模一样。
	store, medium := newStore(t, func(config *Config) { config.Rand = stuckRand{} })
	input := saveText("s", "n.txt", "在先的那一份")

	first, err := store.SaveText(context.Background(), input)
	if err != nil {
		t.Fatalf("第一次外置：%v", err)
	}
	input.Content = "后来的那一份"
	if _, err := store.SaveText(context.Background(), input); err == nil {
		t.Fatalf("撞名该报错，而不是覆盖")
	}
	if keys := medium.Keys(); len(keys) != 1 {
		t.Fatalf("介质上仍该只有一份产物，拿到 %v", keys)
	}
	if got := readBack(t, medium, fs.TargetKey(first.Locator)); string(got) != "在先的那一份" {
		t.Fatalf("在先那份被覆盖了：%q", got)
	}
}

func TestAFailingRandomSourceLeavesNothingBehind(t *testing.T) {
	store, medium := newStore(t, func(config *Config) { config.Rand = brokenRand{} })

	_, err := store.SaveText(context.Background(), saveText("s", "n.txt", "x"))
	if !errors.Is(err, errNoEntropy) {
		t.Fatalf("要把随机源的错误交出来，拿到 %v", err)
	}
	if keys := medium.Keys(); len(keys) != 0 {
		t.Fatalf("取不到随机名时不该有产物落地，介质上却有 %v", keys)
	}
}

func TestSafeNameNeutralizesWhatMustNotBecomeAPath(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		value string
		want  string
	}{
		{"就是个名字", "web_fetch.txt", "web_fetch.txt"},
		{"没有名字", "", ""},
		{"POSIX 穿越", "../../etc/passwd", ".._.._etc_passwd"},
		{"Windows 路径", `C:\Users\x\a.txt`, "C__Users_x_a.txt"},
		{"绝对路径", "/etc/passwd", "_etc_passwd"},
		{"当前目录", ".", "."},
		{"上一层", "..", ".."},
		{"空字节", "a\x00b", "a_b"},
		{"空白", "a b\tc", "a_b_c"},
		{"非 ASCII", "工具.txt", "__.txt"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := safeName(testCase.value); got != testCase.want {
				t.Fatalf("要 %q，拿到 %q", testCase.want, got)
			}
		})
	}
}

func TestSafeNameTruncatesToTheByteLimit(t *testing.T) {
	if got := safeName(strings.Repeat("工", 200)); got != strings.Repeat("_", nameLimit) {
		t.Fatalf("截断落错了地方，拿到 %d 字节", len(got))
	}
}

func TestASuggestedNameCanNeverAddASeparator(t *testing.T) {
	store, medium := newStore(t)

	if _, err := store.SaveText(context.Background(), saveText("s", "../../../etc/passwd", "x")); err != nil {
		t.Fatalf("外置：%v", err)
	}
	keys := medium.Keys()
	if len(keys) != 1 || !keyShape.MatchString(string(keys[0])) {
		t.Fatalf("穿越用的名字把键带出了形状：%v", keys)
	}
	if strings.Count(string(keys[0]), "/") != strings.Count(root, "/")+2 {
		t.Fatalf("键上多出了分隔符：%s", keys[0])
	}
}

func TestNewRefusesAnAssemblyItCannotHonour(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		config Config
	}{
		{"没有文件系统", Config{Root: root, RetrievalHint: hint}},
		{"根是空的", Config{FS: fstest.New(), RetrievalHint: hint}},
		{"根只有斜杠", Config{FS: fstest.New(), Root: "///", RetrievalHint: hint}},
		{"没有取回说明", Config{FS: fstest.New(), Root: root}},
		{"取回说明只有空白", Config{FS: fstest.New(), Root: root, RetrievalHint: "  \t"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := New(testCase.config); err == nil {
				t.Fatalf("这份装配该被拒掉")
			}
		})
	}
}

func TestRootKeepsItsShapeWhateverTrailingSlashesItArrivesWith(t *testing.T) {
	store, medium := newStore(t, func(config *Config) { config.Root = root + "//" })

	if _, err := store.SaveText(context.Background(), saveText("s", "n.txt", "x")); err != nil {
		t.Fatalf("外置：%v", err)
	}
	if keys := medium.Keys(); len(keys) != 1 || !keyShape.MatchString(string(keys[0])) {
		t.Fatalf("键的形状不对：%v", keys)
	}
}

func TestContentIsStoredVerbatim(t *testing.T) {
	store, medium := newStore(t)
	// 空行、制表、CR、多字节、以及一段不是合法 UTF-8 的字节序列都要原样留下。
	content := "第一行\r\n\t第二行\n\n" + string([]byte{0xff, 0xfe}) + "\n"

	ref, err := store.SaveText(context.Background(), saveText("s", "n.txt", content))
	if err != nil {
		t.Fatalf("外置：%v", err)
	}
	if got := readBack(t, medium, fs.TargetKey(ref.Locator)); !bytes.Equal(got, []byte(content)) {
		t.Fatalf("存下来的字节和交进去的不一样：%q", got)
	}
	if ref.Bytes != len(content) {
		t.Fatalf("字节数要 %d，拿到 %d", len(content), ref.Bytes)
	}
}

func TestCancellationIsHonoured(t *testing.T) {
	store, medium := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := store.SaveText(ctx, saveText("s", "n.txt", "x")); !errors.Is(err, context.Canceled) {
		t.Fatalf("外置要认取消，拿到 %v", err)
	}
	if keys := medium.Keys(); len(keys) != 0 {
		t.Fatalf("取消之后不该有产物落地，介质上却有 %v", keys)
	}
}
