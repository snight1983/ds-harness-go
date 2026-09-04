// 本文件的作用：钉住两个摘要的形状，以及它们各自把什么当成「同一份内容」。

package instructions

import (
	"strings"
	"testing"
)

func TestContentDigest是四十位小写十六进制(t *testing.T) {
	digest := ContentDigest("随便一段内容")

	if len(digest) != 40 {
		t.Fatalf("SHA-1 的十六进制长度应当是 40，实际是 %d：%s", len(digest), digest)
	}
	if digest != strings.ToLower(digest) {
		t.Fatalf("摘要必须是小写，实际是 %s", digest)
	}
	if strings.Trim(digest, "0123456789abcdef") != "" {
		t.Fatalf("摘要里出现了非十六进制字符：%s", digest)
	}
}

func TestContentDigest同内容同摘要不同内容不同摘要(t *testing.T) {
	//lint:ignore SA4000 两边写成一样的就是本行要验的：同一段内容必须算出同一个摘要
	if ContentDigest("a") != ContentDigest("a") {
		t.Fatal("同一段内容必须算出同一个摘要")
	}
	if ContentDigest("a") == ContentDigest("b") {
		t.Fatal("不同内容不该算出同一个摘要")
	}
}

// 空内容也要有摘要：一份存在但没有内容的指令，和一份不存在的指令是两回事。
func TestContentDigest空内容也有摘要(t *testing.T) {
	if ContentDigest("") == "" {
		t.Fatal("空内容也该算出一个摘要")
	}
}

// 原文摘要**不**忽略首尾空白：它回答的是「这个文件的字节和上次一样吗」。
func TestContentDigest不忽略首尾空白(t *testing.T) {
	if ContentDigest("规则") == ContentDigest("规则\n") {
		t.Fatal("原文摘要必须能分开「末尾多一个换行」的两份内容")
	}
}

// 去空白摘要忽略首尾空白：同一个目录里「一份逐字节拷贝但末尾多一个换行」
// 这类兄弟靠它塌成一份。
func TestTrimmedDigest忽略首尾空白(t *testing.T) {
	if TrimmedDigest("规则") != TrimmedDigest("\n\t 规则 \n") {
		t.Fatal("去空白摘要应当把只差首尾空白的两份内容当成同一份")
	}
}

func TestTrimmedDigest不忽略中间的空白(t *testing.T) {
	if TrimmedDigest("a b") == TrimmedDigest("ab") {
		t.Fatal("中间的空白是内容的一部分，不该被忽略")
	}
}

// 这一条钉的是一处**已知且刻意保留**的与 DSH 的分歧：JS 的 trim 把 U+FEFF
// 也当空白，[strings.TrimSpace] 不当。也就是说带 BOM 的拷贝在这里不折叠。
// 用例写在这里，是为了让这个分歧是被钉住的，而不是某天悄悄变了没人发现。
func TestTrimmedDigestBOM不算空白(t *testing.T) {
	if TrimmedDigest("\ufeff规则") == TrimmedDigest("规则") {
		t.Fatal("Go 的空白集合不含 BOM，这两份内容此处应当算作不同")
	}
}
