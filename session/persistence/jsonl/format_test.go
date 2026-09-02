// 本文件的作用：直接压头那一行的编解、路径怎么编、以及扫描器那条「先记下来、
// 后面撞上回合边界才发作」的判据。
//
// 为什么不从 Store 那一层压：这几条判据里有一半只在畸形输入上才走得到，
// 而畸形输入没法经由写路径产生——写路径写出来的东西按定义是合法的。
// 从盘上那一层压，就得先手工摆一份坏存档，那验的是「我摆坏了没有」，不是判据本身。
//
// 源: packages/session/session-persistence-jsonl/tests/format.spec.ts

package jsonl

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/session/persistence"
)

func TestHeaderLineSurvivesARoundTripWithEveryOptionalField(t *testing.T) {
	original := session.SessionHeader{
		Version:         session.FormatVersion,
		ID:              "full",
		CreatedAt:       1234,
		Cwd:             `C:\proj`,
		ParentSession:   "parent",
		SeedLength:      7,
		Origin:          session.OriginSubagent,
		DelegationDepth: 2,
		AgentPreset:     "reviewer",
	}

	line, err := encodeHeaderLine(original)
	if err != nil {
		t.Fatalf("编头失败：%v", err)
	}
	back, err := parseHeaderMeta(line)
	if err != nil {
		t.Fatalf("解头失败：%v", err)
	}
	if back != original {
		t.Errorf("转一圈之后头变了\n出去：%+v\n回来：%+v", original, back)
	}
}

// 「没给」和「给了零值」在这道缝上不是同一件事，所以空的那几项根本不该出现在
// 盘上。写出来的话，一份没有 cwd 的会话和一份 cwd 是空串的会话就分不开了。
func TestEmptyOptionalFieldsAreOmittedFromTheLine(t *testing.T) {
	line, err := encodeHeaderLine(session.SessionHeader{
		Version: session.FormatVersion, ID: "bare", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("编头失败：%v", err)
	}
	for _, key := range []string{"cwd", "parentSession", "seedLength", "origin", "agentPreset"} {
		if strings.Contains(string(line), `"`+key+`"`) {
			t.Errorf("空的 %s 不该写进盘上那一行：%s", key, line)
		}
	}
	// 这三项相反：它们是必填的，哪怕是零也要写下去，否则读回来分不出「零」和「缺」。
	for _, key := range []string{"version", "id", "createdAt", "delegationDepth"} {
		if !strings.Contains(string(line), `"`+key+`"`) {
			t.Errorf("必填的 %s 该写进盘上那一行：%s", key, line)
		}
	}
}

func TestParseHeaderMetaSeparatesNotAHeaderFromRealRefusals(t *testing.T) {
	// 这一批都是「这一行不是一份会话头」。列举那条路靠这个哨兵跳过它们——
	// 一个根下面完全可能躺着别人的文件，那不是损坏。
	//
	// 版本号一律写成本构建的版本：写别的会先撞上版本拒绝，那是另一条路，
	// 这一批就一条也验不到了。
	notAHeader := map[string]string{
		"根本不是 JSON":     `这不是 json`,
		"是 JSON 但不是对象":  `[1,2,3]`,
		"行标签不对":         `{"type":"event","version":$V,"id":"x","createdAt":0,"delegationDepth":0}`,
		"缺 id":          `{"type":"session","version":$V,"createdAt":0,"delegationDepth":0}`,
		"缺 createdAt":   `{"type":"session","version":$V,"id":"x","delegationDepth":0}`,
		"createdAt 是负的": `{"type":"session","version":$V,"id":"x","createdAt":-1,"delegationDepth":0}`,
		"派生深度是负的":       `{"type":"session","version":$V,"id":"x","createdAt":0,"delegationDepth":-1}`,
		"来源是个没听过的词":     `{"type":"session","version":$V,"id":"x","createdAt":0,"delegationDepth":0,"origin":"nowhere"}`,
		"cwd 是个数":       `{"type":"session","version":$V,"id":"x","createdAt":0,"delegationDepth":0,"cwd":42}`,
		// 版本号不是整数：它既不是本构建的版本，也指不出任何一个别的构建。
		"版本号是小数": `{"type":"session","version":0.5,"id":"x","createdAt":0,"delegationDepth":0}`,
	}
	for name, template := range notAHeader {
		t.Run(name, func(t *testing.T) {
			line := strings.ReplaceAll(template, "$V", strconv.Itoa(session.FormatVersion))
			if _, err := parseHeaderMeta([]byte(line)); !errors.Is(err, errHeaderMalformed) {
				t.Fatalf("该报 errHeaderMalformed，实际 %v", err)
			}
		})
	}

	// 退役字段是另一件事：它不是「跳过」，是「这份存档带着一份本构建不再认的策略基线」。
	t.Run("头上还挂着退役的策略字段", func(t *testing.T) {
		line := fmt.Sprintf(
			`{"type":"session","version":%d,"id":"x","createdAt":0,"delegationDepth":0,"sandboxMode":"off"}`,
			session.FormatVersion)
		_, err := parseHeaderMeta([]byte(line))
		if err == nil || errors.Is(err, errHeaderMalformed) {
			t.Fatalf("该报一句退役字段，实际 %v", err)
		}
		if !strings.Contains(err.Error(), "退役") {
			t.Errorf("错误里该说清是退役字段，实际 %q", err.Error())
		}
	})
}

// 一份未来格式的存档，使用者该看到的是「升级运行时」，绝不是「日志损坏」。
// 所以版本判据必须排在形状检查**之前**——一个未来的格式未必过得了今天这套检查。
func TestAForeignFormatVersionIsRefusedBeforeAnyShapeCheck(t *testing.T) {
	line := fmt.Sprintf(`{"type":"session","version":%d,"id":"future"}`, session.FormatVersion+1)

	_, err := parseHeaderMeta([]byte(line))
	var refusal *persistence.FormatUnsupportedError
	if !errors.As(err, &refusal) {
		t.Fatalf("该报 FormatUnsupportedError，实际 %v", err)
	}
	if refusal.ID != "future" {
		t.Errorf("拒绝里带的会话是 %q，要的是 %q", string(refusal.ID), "future")
	}
	// 这一行连 createdAt 和 delegationDepth 都没有，形状检查一定过不了。
	// 报出来的却是版本拒绝，这正是次序那条要保住的东西。
	if errors.Is(err, errHeaderMalformed) {
		t.Error("版本拒绝被形状检查抢先了")
	}
}

// 这条拒绝发生在拿到会话头**之前**，所以编排层那条靠 Locate 补位置的路走不通。
// 位置没补上的话，使用者只知道「有一份存档格式不对」，不知道是哪一份。
func TestAFormatRefusalGetsTheArtifactPathAttached(t *testing.T) {
	backend, err := NewBackend(Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("造后端失败：%v", err)
	}

	bare := &persistence.FormatUnsupportedError{
		ID:     "x",
		Reason: persistence.FormatVersionRefusal("x", 99),
	}
	located := backend.locateParseRefusal(`C:\somewhere\session.jsonl`, bare)

	var refusal *persistence.FormatUnsupportedError
	if !errors.As(located, &refusal) {
		t.Fatalf("补位置之后还该是同一类拒绝，实际 %v", located)
	}
	if refusal.Location == nil || refusal.Location.Path != `C:\somewhere\session.jsonl` {
		t.Errorf("位置没补上：%+v", refusal.Location)
	}

	// 已经带了位置的不许被改写：那是更靠里的一层认定的，比这里知道得多。
	kept := backend.locateParseRefusal(`C:\elsewhere\session.jsonl`, located)
	if !errors.As(kept, &refusal) || refusal.Location.Path != `C:\somewhere\session.jsonl` {
		t.Errorf("已经带位置的拒绝被改写了：%+v", refusal.Location)
	}

	// 不是格式拒绝的错误原样交回去，不该被包一层。
	plain := errors.New("普通的 I/O 故障")
	if got := backend.locateParseRefusal("whatever", plain); got != plain {
		t.Errorf("普通错误该原样交回，实际 %v", got)
	}
}

// 一份写坏了中段的日志，只要后面再没出现过回合边界，那些残缺的字节就还只是一条
// 长一点的断尾——留着它比丢掉整份好。可一旦后面解出了一条 turn/end，这份日志
// 就会被读成一段「中间那些事根本没发生过」的完整历史，那比读不出来坏得多。
func TestABrokenRecordOnlyBecomesFatalWhenALaterTurnCloses(t *testing.T) {
	header, err := encodeHeaderLine(testMeta("scan", `C:\proj`))
	if err != nil {
		t.Fatalf("编头失败：%v", err)
	}
	prefix := string(header) + "\n" +
		`{"type":"turn/start","seq":0,"time":1,"data":{"turn":1}}` + "\n" +
		`{"这一行解不成一条存储记录":true}` + "\n"

	t.Run("坏记录之后没有回合边界：只丢掉那一截", func(t *testing.T) {
		scan, err := scanLog([]byte(prefix + `{"type":"step/start","seq":1,"time":2,"data":{"turn":1,"step":1}}` + "\n"))
		if err != nil {
			t.Fatalf("这时候还不该拒收整份：%v", err)
		}
		// 已提交的只到坏记录之前那一条。
		if got := seqsOf(scan.events); !slices.Equal(got, []int{0}) {
			t.Fatalf("保住的 seq 是 %v，要的是 [0]", got)
		}
	})

	t.Run("坏记录之后出现回合边界：拒收整份", func(t *testing.T) {
		_, err := scanLog([]byte(prefix + `{"type":"turn/end","seq":1,"time":2,"data":{"turn":1,"reason":{"kind":"completed"}}}` + "\n"))
		if err == nil {
			t.Fatal("后面关掉了一个回合，这份日志必须拒收")
		}
		if !strings.Contains(err.Error(), "解不开") {
			t.Errorf("拒绝该说清是哪一行解不开，实际 %q", err.Error())
		}
	})
}

// seq 断了和「解不开」走同一条推迟规则：断口后面没有回合边界就只是断尾，
// 有就说明这份日志会被读成一段少了中间的完整历史。
func TestASequenceGapFollowsTheSameDeferredRule(t *testing.T) {
	header, err := encodeHeaderLine(testMeta("gap", `C:\proj`))
	if err != nil {
		t.Fatalf("编头失败：%v", err)
	}
	prefix := string(header) + "\n" +
		`{"type":"turn/start","seq":0,"time":1,"data":{"turn":1}}` + "\n" +
		`{"type":"step/start","seq":9,"time":2,"data":{"turn":1,"step":1}}` + "\n"

	scan, err := scanLog([]byte(prefix))
	if err != nil {
		t.Fatalf("断口后面没有回合边界，这时候不该拒收：%v", err)
	}
	if got := seqsOf(scan.events); !slices.Equal(got, []int{0}) {
		t.Fatalf("保住的 seq 是 %v，要的是 [0]", got)
	}

	_, err = scanLog([]byte(prefix +
		`{"type":"turn/end","seq":10,"time":3,"data":{"turn":1,"reason":{"kind":"completed"}}}` + "\n"))
	if err == nil {
		t.Fatal("断口后面关掉了一个回合，这份日志必须拒收")
	}
	if !strings.Contains(err.Error(), "seq 断了") {
		t.Errorf("拒绝该说清是 seq 断了，实际 %q", err.Error())
	}
}

func TestScanLogRefusesALogWithNoHeaderAtAll(t *testing.T) {
	cases := map[string]string{
		"整个是空的":   "",
		"只有一行没换行": `{"type":"session"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := scanLog([]byte(body)); err == nil {
				t.Fatal("没有一条完整的头，该拒")
			}
		})
	}
}

func TestAScannerRefusesToBeWrittenToAfterItFinished(t *testing.T) {
	header, err := encodeHeaderLine(testMeta("done", `C:\proj`))
	if err != nil {
		t.Fatalf("编头失败：%v", err)
	}
	scanner, err := newLogScanner(append(header, '\n'))
	if err != nil {
		t.Fatalf("造扫描器失败：%v", err)
	}
	scanner.finish()

	if err := scanner.write([]byte("再来一段\n")); err == nil {
		t.Fatal("收工之后再写该报错——那说明调用方把两份日志接串了")
	}
}

// 跨越两次 write 的那一截必须**拷**下来：喂进来的那段字节可能是一个解码器复用的
// 输出缓冲，write 一返回它就可以被写花。不拷的话，一条被劈成两半的记录会在
// 下一次 write 时读到已经变了的前半截。
func TestAnEventSplitAcrossTwoWritesIsRejoinedFromACopy(t *testing.T) {
	header, err := encodeHeaderLine(testMeta("split", `C:\proj`))
	if err != nil {
		t.Fatalf("编头失败：%v", err)
	}
	scanner, err := newLogScanner(append(header, '\n'))
	if err != nil {
		t.Fatalf("造扫描器失败：%v", err)
	}

	whole := `{"type":"turn/start","seq":0,"time":1,"data":{"turn":1}}` + "\n"
	buffer := []byte(whole[:20])
	if err := scanner.write(buffer); err != nil {
		t.Fatalf("写前半截失败：%v", err)
	}
	// 把那段缓冲整个写花，模拟解码器复用它。
	for index := range buffer {
		buffer[index] = 'X'
	}
	if err := scanner.write([]byte(whole[20:])); err != nil {
		t.Fatalf("写后半截失败：%v", err)
	}

	scan := scanner.finish()
	if got := seqsOf(scan.events); !slices.Equal(got, []int{0}) {
		t.Fatalf("拼回来的 seq 是 %v，要的是 [0]——前半截多半被写花了", got)
	}
}

// packChunks 是**写**那一侧的排布选择，读那一侧和它无关：一份日志怎么读，
// 不取决于写它的那一刻这个开关是什么。
func TestPackChunksChangesTheLayoutButNotWhatIsRead(t *testing.T) {
	events := []session.Event{
		turnStartEvent(t, 0, 1, 1),
		userMessageEvent(t, 1, 2, "hi"),
		turnEndEvent(t, 2, 3, 1),
	}

	packed, err := eventLines(events, true)
	if err != nil {
		t.Fatalf("压着编失败：%v", err)
	}
	loose, err := eventLines(events, false)
	if err != nil {
		t.Fatalf("摊开编失败：%v", err)
	}
	// 不压的时候恰好一条事件一行。
	if got := strings.Count(string(loose), "\n") + 1; got != len(events) {
		t.Errorf("摊开编出了 %d 行，要的是 %d 行", got, len(events))
	}

	for name, body := range map[string][]byte{"压着编": packed, "摊开编": loose} {
		t.Run(name, func(t *testing.T) {
			header, err := encodeHeaderLine(testMeta("layout", `C:\proj`))
			if err != nil {
				t.Fatalf("编头失败：%v", err)
			}
			log := append(append(header, '\n'), body...)
			scan, err := scanLog(append(log, '\n'))
			if err != nil {
				t.Fatalf("扫描失败：%v", err)
			}
			if got := seqsOf(scan.events); !slices.Equal(got, []int{0, 1, 2}) {
				t.Fatalf("读出来的 seq 是 %v，要的是 [0 1 2]", got)
			}
		})
	}
}

func TestEventLinesRefusesAnEventThatCannotBeMarshalled(t *testing.T) {
	// json.RawMessage 里塞一段不是 JSON 的字节：排到这一条时才会炸。
	broken := []session.Event{{
		Type: session.EventTurnStart, Seq: 0, Time: 1,
		Data: json.RawMessage(`{这不是 json`),
	}}

	for _, packChunks := range []bool{true, false} {
		if _, err := eventLines(broken, packChunks); err == nil {
			t.Errorf("packChunks=%v 时排不出去的事件该报错", packChunks)
		}
	}
}

// 会话标识是一个没验过的字符串。直接当路径用就等于把 `../`、绝对路径、NUL 和
// 分隔符交给了写它的那个人，所以这套编码必须是单射的：两个不同的标识
// 绝不能编出同一段路径。
func TestEncodeSegmentIsInjectiveAndPathSafe(t *testing.T) {
	cases := map[string]string{
		"普通标识":    "abc-123_x.y",
		"点":       ".",
		"两个点":     "..",
		"斜杠":      "a/b",
		"反斜杠":     `a\b`,
		"绝对路径":    "/etc/passwd",
		"盘符":      `C:\Windows`,
		"波浪号本身":   "~0041",
		"字面量 A":   "A",
		"空格":      "a b",
		"NUL":     "a\x00b",
		"中文":      "会话",
		"星号":      "*",
		"BMP 外的字": "𝄞",
	}

	encoded := map[string]string{}
	for name, raw := range cases {
		got, err := encodeSegment(raw)
		if err != nil {
			t.Fatalf("%s：编不出来：%v", name, err)
		}
		for _, forbidden := range []string{"/", `\`, ":", "\x00"} {
			if strings.Contains(got, forbidden) {
				t.Errorf("%s 编出来的 %q 里还留着 %q", name, got, forbidden)
			}
		}
		if got == "." || got == ".." {
			t.Errorf("%s 编出来的还是 %q", name, got)
		}
		if previous, clash := encoded[got]; clash {
			t.Errorf("%q 和 %q 编出了同一段 %q——这套编码不再是单射的", raw, previous, got)
		}
		encoded[got] = raw
	}

	if _, err := encodeSegment(""); err == nil {
		t.Error("空串编不出路径段，该报错")
	}
	// 一段不是合法 UTF-8 的字节没有对应的 UTF-16 码元序列。替换成 U+FFFD 会让
	// 两个不同的标识编出同一段路径，所以这里当场拒。
	if _, err := encodeSegment("\xff\xfe"); err == nil {
		t.Error("不是合法 UTF-8 的标识该被拒掉，不该被替换")
	}
}

// 工程目录名是**有损**的，要的只是「人能在文件管理器里认出这是哪个工程」。
// 真正认身份的是里面那一层。
func TestProjectKeyIsReadableAndBounded(t *testing.T) {
	cases := []struct {
		name string
		cwd  string
		want string
	}{
		{"POSIX 路径", "/home/me/proj", "--home-me-proj--"},
		{"Windows 路径", `C:\Users\me\proj`, "--C-Users-me-proj--"},
		{"连着的分隔符算一个", "/a//b", "--a-b--"},
		{"只有分隔符时兜底成 root", "///", "--root--"},
		{"不安全的字节转义掉", "/a b", "--a~0020b--"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			got, err := projectKey(item.cwd)
			if err != nil {
				t.Fatalf("编不出来：%v", err)
			}
			if got != item.want {
				t.Errorf("编出来是 %q，要的是 %q", got, item.want)
			}
		})
	}

	// 截断是为了不撞上文件系统对单个路径分量的限制（常见是 255），
	// 而且只在安全边界上截——截穿一个多字节字符会造出一段不是 UTF-8 的目录名。
	long, err := projectKey("/" + strings.Repeat("会", 400))
	if err != nil {
		t.Fatalf("长路径编不出来：%v", err)
	}
	if len(long) > projectKeyMaxBytes+4 {
		t.Errorf("截断之后还有 %d 字节，上限是 %d", len(long), projectKeyMaxBytes+4)
	}

	if _, err := projectKey(""); err == nil {
		t.Error("空工作目录该报错——没有工作目录走的是另一条路")
	}
	if _, err := projectKey("\xff"); err == nil {
		t.Error("不是合法 UTF-8 的工作目录该被拒掉")
	}
}

// 没有工作目录的会话有自己的归处，那不是「工程目录是空串」。
func TestSessionsWithoutACwdGetTheirOwnHome(t *testing.T) {
	dir, err := projectDir(`C:\root`, "")
	if err != nil {
		t.Fatalf("编不出来：%v", err)
	}
	if !strings.HasSuffix(dir, noCwdDir) {
		t.Errorf("没有工作目录时该落在 %q 下，实际 %q", noCwdDir, dir)
	}
}

func TestLogSuffixDistinguishesTheTwoEncodings(t *testing.T) {
	if logSuffix(CompressionZstd) == logSuffix(CompressionNone) {
		t.Fatal("两种编码的后缀必须分得开，否则一个根下面的两种存档会互相盖掉")
	}
	// 空串走缺省那一档，缺省是明文。
	if logSuffix("") != logSuffix(CompressionNone) {
		t.Errorf("空编码该按明文算，实际 %q", logSuffix(""))
	}
}
