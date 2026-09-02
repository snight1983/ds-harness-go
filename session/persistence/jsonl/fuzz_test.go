// 本文件的作用：拿任意字节压断尾恢复那条路，和把会话标识编成路径段那一下。
//
// 为什么这两处要 fuzz：它们各自守着一条只在坏输入上才成立的不变量。
// 扫描器守的是「保住的那一段一定是真提交过的」——一份被崩溃、被半截磁盘写、
// 被别人的编辑器改过的日志长什么样，用例举不全。编路径段守的是单射——
// 会话标识是一个没验过的字符串，两个不同的标识编出同一段路径，两份历史就会
// 落在同一个文件上，而那件事在盘上是不可逆的。

package jsonl

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/snight1983/ds-harness-go/session"
)

// FuzzScanLog 压那条断尾恢复：扫描器要么拒收整份，要么交回一段**真提交过的**前缀。
//
// 它绝不能崩，也绝不能交回一个越界的 committedBytes——那个数字是后面截断和
// 续写的落点，越界就等于把好字节砍掉，或者在一段坏字节后面接着写。
func FuzzScanLog(f *testing.F) {
	header, err := encodeHeaderLine(session.SessionHeader{
		Version: session.FormatVersion, ID: "fuzz", CreatedAt: 1,
	})
	if err != nil {
		f.Fatalf("编头失败：%v", err)
	}
	good := string(header) + "\n"

	f.Add(good)
	f.Add(good + `{"type":"turn/start","seq":0,"time":1,"data":{"turn":1}}` + "\n")
	// 断在半路的最后一行：一次崩溃留下的就是这个，它**不是**损坏。
	f.Add(good + `{"type":"turn/start","seq":0,"time":1,"data":{"turn`)
	// 中间坏了一行，后面又关掉了一个回合：这份必须拒收。
	f.Add(good + `{"type":"turn/start","seq":0,"time":1,"data":{"turn":1}}` + "\n" +
		"坏行\n" +
		`{"type":"turn/end","seq":1,"time":2,"data":{"turn":1,"reason":{"kind":"completed"}}}` + "\n")
	// seq 断口。
	f.Add(good + `{"type":"turn/start","seq":7,"time":1,"data":{"turn":1}}` + "\n")
	f.Add("")
	f.Add("\n")
	f.Add(good + "\n\n\n")
	f.Add(good + strings.Repeat("x", 4096) + "\n")

	f.Fuzz(func(t *testing.T, input string) {
		buffer := []byte(input)
		scan, err := scanLog(buffer)
		if err != nil {
			// 拒收是允许的答案。要的是它别崩、别挂。
			return
		}

		// committedBytes 是「可以从这里接着写」的落点。它必须落在这段字节里面，
		// 而且必须正好在一个换行之后——落在一行中间，续写就会把那一行劈开。
		if scan.committedBytes < 0 || scan.committedBytes > int64(len(buffer)) {
			t.Fatalf("committedBytes 是 %d，输入只有 %d 字节", scan.committedBytes, len(buffer))
		}
		if scan.committedBytes > 0 && buffer[scan.committedBytes-1] != '\n' {
			t.Fatalf("committedBytes=%d 落在一行中间，前一个字节是 %q",
				scan.committedBytes, buffer[scan.committedBytes-1])
		}
		// 保住的那一段必须是 seq 从零开始连续的。断一格就说明交出去的是一段
		// 「中间那些事根本没发生过」的历史，那比读不出来坏得多。
		for index, event := range scan.events {
			if event.Seq != index {
				t.Fatalf("保住的第 %d 条事件 seq 是 %d，已提交区间不连续", index, event.Seq)
			}
		}
		// 头必须是本构建读得了的版本：别的版本走的是版本拒绝那条路，到不了这里。
		if scan.meta.Version != session.FormatVersion {
			t.Fatalf("交回来的头版本是 %d，本构建只读 %d", scan.meta.Version, session.FormatVersion)
		}

		// 断尾恢复是幂等的：把保住的那一段单独再扫一遍，结果必须一样。不一样就
		// 说明「截断到 committedBytes 再续写」之后，同一份日志会被读成另一个样子。
		again, err := scanLog(buffer[:scan.committedBytes])
		if err != nil {
			t.Fatalf("保住的那一段自己扫不动了：%v", err)
		}
		if len(again.events) != len(scan.events) || again.committedBytes != scan.committedBytes {
			t.Fatalf("截断之后再扫结果变了：%d 条/%d 字节 → %d 条/%d 字节",
				len(scan.events), scan.committedBytes, len(again.events), again.committedBytes)
		}
	})
}

// FuzzEncodeSegment 压那套把会话标识编成路径段的编码。
//
// 单射是它存在的全部理由：会话标识是一个没验过的字符串，两个不同的标识编出
// 同一段路径，两份历史就落在同一个文件上。路径安全是第二条：`../`、绝对路径、
// NUL 和分隔符都必须编掉，否则一个会话标识就能写到根外面去。
func FuzzEncodeSegment(f *testing.F) {
	for _, seed := range []string{
		"abc-123", ".", "..", "a/b", `a\b`, "/etc/passwd", `C:\Windows`,
		"~0041", "A", "a b", "a\x00b", "会话", "*", "𝄞", "", "\xff\xfe",
		strings.Repeat("会", 300),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, id string) {
		encoded, err := encodeSegment(id)
		if err != nil {
			// 空串和不是合法 UTF-8 的标识当场拒——那是对的，替换成 U+FFFD
			// 会让两个不同的标识编出同一段路径。
			return
		}
		if encoded == "" {
			t.Fatal("编出来是空串，那不是一段路径")
		}
		for _, forbidden := range []string{"/", `\`, ":", "\x00"} {
			if strings.Contains(encoded, forbidden) {
				t.Fatalf("%q 编出来的 %q 里还留着 %q", id, encoded, forbidden)
			}
		}
		if encoded == "." || encoded == ".." {
			t.Fatalf("%q 编出来的还是 %q——那指向上一级", id, encoded)
		}

		// 单射用可逆来压：能一路解回原串，就不可能有两个标识撞在一起。
		// 直接两两比要一个跨次调用的表，而 fuzz 每次只跑一个输入。
		decoded, err := decodeSegmentForFuzz(encoded)
		if err != nil {
			t.Fatalf("%q 编成 %q 之后解不回去：%v", id, encoded, err)
		}
		if decoded != id {
			t.Fatalf("转一圈之后变了\n出去：%q\n编成：%q\n回来：%q", id, encoded, decoded)
		}
	})
}

// decodeSegmentForFuzz 把 [encodeSegment] 编出来的那段解回原串。
//
// 只给 fuzz 用：生产路径从不需要从路径段反推会话标识（身份来自头那一行）。
// 它在这里是为了把「单射」压成一条测得动的判据——可逆蕴含单射，而两两比
// 需要一张跨调用的表，fuzz 每次只喂一个输入，那张表建不起来。
func decodeSegmentForFuzz(segment string) (string, error) {
	var units []uint16
	for index := 0; index < len(segment); {
		if segment[index] != '~' {
			// 编出来的一定只有 ASCII（非 ASCII 一律走 ~XXXX），所以按字节走是对的。
			if segment[index] >= utf8.RuneSelf {
				return "", fmt.Errorf("编出来的段里有非 ASCII 字节 %#x", segment[index])
			}
			units = append(units, uint16(segment[index]))
			index++
			continue
		}
		if index+5 > len(segment) {
			return "", errors.New("一段 ~ 转义不足五个字符")
		}
		unit, err := strconv.ParseUint(segment[index+1:index+5], 16, 16)
		if err != nil {
			return "", fmt.Errorf("转义 %q 解不开：%w", segment[index:index+5], err)
		}
		units = append(units, uint16(unit))
		index += 5
	}
	return string(utf16.Decode(units)), nil
}
