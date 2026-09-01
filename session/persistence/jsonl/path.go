// 本文件的作用：一个会话标识怎么变成一段安全的路径，以及一份存档摆在哪。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:110-209

package jsonl

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/snight1983/ds-harness-go/session"
)

// Compression 是一份 JSONL 存档的物理编码。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:19
type Compression string

const (
	// CompressionZstd 把每一批事件压成一个独立的、带校验和的 Zstandard 帧。
	CompressionZstd Compression = "zstd"
	// CompressionNone 直接写明文 JSONL。
	CompressionNone Compression = "none"
)

// logSuffix 给出一种物理编码对应的文件后缀。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:26-28
func logSuffix(compression Compression) string {
	if compression == CompressionZstd {
		return ".jsonl.zstd"
	}
	return ".jsonl"
}

// noCwdDir 是那些没有工作目录的会话的归处。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:178
const noCwdDir = "_no-cwd"

// logBaseName 是一份存档去掉物理编码后缀之后的名字。
const logBaseName = "session"

// safePathByte 判一个字节能不能原样出现在路径段里。
//
// 波浪号被排除在外：它是转义引导符，放行它这套编码就不再是单射的。
func safePathByte(b byte) bool {
	switch {
	case b >= 'A' && b <= 'Z', b >= 'a' && b <= 'z', b >= '0' && b <= '9':
		return true
	case b == '.', b == '_', b == '-':
		return true
	default:
		return false
	}
}

// encodeSegment 把任意一个字符串编成单独一段安全路径，而且是单射的。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:120-135
//
// [session.SessionID] 是一个没验过的字符串，直接当路径用就等于把 `../`、绝对路径、
// NUL 和分隔符交给了写它的那个人。安全字节原样留下，别的一律变成 `~XXXX`——四位
// 十六进制的那个数是 **UTF-16 码元**，和上游逐字对齐，于是两边算出来的目录名一样。
//
// 新增: 上游遍历的是 JS 字符串的码元，孤立代理对它是合法输入。Go 的字符串是字节串，
// 一段不是合法 UTF-8 的字节没有对应的码元序列。这里当场拒而不是替换成 U+FFFD：
// 替换会让两个不同的标识编出同一段路径，而一个标识本来也得先排成 JSON 才写得进那行
// 会话头——`encoding/json` 在那一步同样会把它替换掉。真正的边界在更早处，这里只是
// 第一个能看见它的地方。
func encodeSegment(raw string) (string, error) {
	switch raw {
	case "":
		return "", fmt.Errorf("session/persistence/jsonl: 路径段不能是空串")
	case ".":
		return "~002E", nil
	case "..":
		return "~002E~002E", nil
	}
	if !utf8.ValidString(raw) {
		return "", fmt.Errorf("session/persistence/jsonl: 路径段 %q 不是合法的 UTF-8", raw)
	}
	var out strings.Builder
	for index := 0; index < len(raw); {
		if b := raw[index]; b < utf8.RuneSelf {
			if safePathByte(b) {
				out.WriteByte(b)
			} else {
				fmt.Fprintf(&out, "~%04X", b)
			}
			index++
			continue
		}
		char, size := utf8.DecodeRuneInString(raw[index:])
		for _, unit := range utf16.Encode([]rune{char}) {
			fmt.Fprintf(&out, "~%04X", unit)
		}
		index += size
	}
	return out.String(), nil
}

// projectKey 把一个工程目录编成一个人还看得懂的目录名。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:145-169
//
// 分隔符（含 Windows 的盘符冒号）折成一个 `-`，连着的算一个；别的不安全码元用和
// 标识一样的 `~XXXX`。这一步**是有损的**：折分隔符和末尾截断都不可逆。要的就是
// 「人能在文件管理器里认出这是哪个工程」，不是可逆——真正认身份的是里面那一层。
func projectKey(cwd string) (string, error) {
	if cwd == "" {
		return "", fmt.Errorf("session/persistence/jsonl: 工程目录不能是空串")
	}
	if !utf8.ValidString(cwd) {
		return "", fmt.Errorf("session/persistence/jsonl: 工程目录 %q 不是合法的 UTF-8", cwd)
	}
	var readable strings.Builder
	separatorRun := false
	for index := 0; index < len(cwd); {
		if b := cwd[index]; b < utf8.RuneSelf {
			switch {
			case b == '/' || b == '\\' || b == ':':
				if !separatorRun {
					readable.WriteByte('-')
				}
				separatorRun = true
			case safePathByte(b):
				readable.WriteByte(b)
				separatorRun = false
			default:
				fmt.Fprintf(&readable, "~%04X", b)
				separatorRun = false
			}
			index++
			continue
		}
		char, size := utf8.DecodeRuneInString(cwd[index:])
		for _, unit := range utf16.Encode([]rune{char}) {
			fmt.Fprintf(&readable, "~%04X", unit)
		}
		separatorRun = false
		index += size
	}
	slug := strings.TrimLeft(readable.String(), "-")
	if slug == "" {
		slug = "root"
	}
	// 上游截的是 251 个 UTF-16 码元。这里截的是字节，而且只在安全边界上截：
	// 截穿一个多字节字符会造出一段不是 UTF-8 的目录名。
	if len(slug) > projectKeyMaxBytes {
		slug = slug[:projectKeyMaxBytes]
		for len(slug) > 0 && !utf8.ValidString(slug) {
			slug = slug[:len(slug)-1]
		}
	}
	return "--" + slug + "--", nil
}

// projectKeyMaxBytes 是那段可读部分的长度上限，为的是不撞上文件系统对单个路径分量
// 的限制（常见是 255）。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:168
const projectKeyMaxBytes = 251

// projectDir 给出一个工程在这个根下面的目录。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:176-180
func projectDir(root, cwd string) (string, error) {
	if cwd == "" {
		return filepath.Join(root, noCwdDir), nil
	}
	key, err := projectKey(cwd)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, key), nil
}

// sessionDir 给出一个会话自己那个目录。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:189-191
func sessionDir(root, cwd string, id session.SessionID) (string, error) {
	project, err := projectDir(root, cwd)
	if err != nil {
		return "", err
	}
	segment, err := encodeSegment(string(id))
	if err != nil {
		return "", err
	}
	return filepath.Join(project, segment), nil
}

// logPath 给出一个会话那份只追加的事件日志在哪。
//
// 源: packages/session/session-persistence-jsonl/src/format.ts:201-209
func logPath(root, cwd string, id session.SessionID, compression Compression) (string, error) {
	dir, err := sessionDir(root, cwd, id)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, logBaseName+logSuffix(compression)), nil
}
