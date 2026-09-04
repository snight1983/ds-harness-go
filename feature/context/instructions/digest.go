// 本文件的作用：指令内容的两个身份摘要——原文的和去掉首尾空白的。
//
// 源: packages/context/agent-instructions/src/digest.ts:1-28

package instructions

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
)

// ContentDigest 算一段指令内容的身份摘要，小写十六进制。
//
// 源: packages/context/agent-instructions/src/digest.ts:9-16
//
// 这里用 SHA-1 不是疏忽：这个摘要是一个**身份**，用来回答「这份内容和上次
// 是不是同一份」，不是签名也不是校验和。攻击者能改文件内容的时候，
// 他改的是模型要读的指令本身，构造一次摘要碰撞没有任何额外收益。
// 换成 SHA-256 只会让所有在途会话里已经记下的摘要全部对不上。
func ContentDigest(content string) string {
	sum := sha1.Sum([]byte(content))
	return hex.EncodeToString(sum[:])
}

// TrimmedDigest 算去掉首尾空白之后的身份摘要，同一个目录里的重复候选靠它折叠。
//
// 源: packages/context/agent-instructions/src/digest.ts:18-28
//
// 去空白再算，是为了让「一个符号链接」「一份逐字节拷贝但末尾多一个换行」
// 这类同目录兄弟塌成一份，而不是把同样的指令给模型看两遍。
//
// 新增: JS 的 String.prototype.trim 和 [strings.TrimSpace] 的空白集合不完全一样
// ——前者还把 U+FEFF（BOM）算作空白，后者不算。也就是说一份带 BOM 的拷贝
// 在 DSH 那边会和不带 BOM 的那份折叠，在这里不会。这里**不去补那一个码点**：
// 补上等于自己维护一套「空白是什么」的定义，而 Go 的定义（[unicode.IsSpace]）
// 是这门语言里所有人共用的那一套。多渲染一份重复指令是可见的、可诊断的；
// 一套私有的空白定义不是。
func TrimmedDigest(content string) string {
	return ContentDigest(strings.TrimSpace(content))
}
