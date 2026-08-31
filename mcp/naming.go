// 本文件的作用：从 `(服务器名, 原名)` 这一对身份算出模型看得见的那个公开工具名。
//
// 源: packages/mcp/mcp-client/src/tools.ts:97-117

package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
)

// maxPublicNameLength 是提供方对函数名的字符上限。这是线上协议的常数，不是配置。
//
// 源: packages/mcp/mcp-client/src/tools.ts:49
const maxPublicNameLength = 64

// hashLength 是名字被改写时补在尾巴上的那段身份哈希有多少个十六进制字符。
//
// 源: packages/mcp/mcp-client/src/tools.ts:55
const hashLength = 12

// invalidNameChars 是提供方对函数名的字符白名单之外的一切。
//
// 源: packages/mcp/mcp-client/src/tools.ts:52
var invalidNameChars = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// PublicToolName 算出一个 MCP 工具在模型那边的名字。
//
// 源: packages/mcp/mcp-client/src/tools.ts:111-117
//
// 它是 `(serverName, rawName)` 的确定性纯函数。干净的那种情况就是
// `mcp__<serverName>__<rawName>` 一字不改；一旦替换字符或者截断改动了这个名字，
// 尾巴上补一段 12 位十六进制的 SHA-256 身份哈希，好让两个不同的 MCP 身份
// 绝不会塌成同一个公开名。
func PublicToolName(serverName, rawName string) string {
	joined := "mcp__" + serverName + "__" + rawName
	normalized := invalidNameChars.ReplaceAllString(joined, "_")
	if normalized == joined && len(normalized) <= maxPublicNameLength {
		return normalized
	}
	// 分隔符用 NUL：它在两段名字里都出现不了，所以 `("a_b", "c")` 和 `("a", "b_c")`
	// 拼不出同一段被哈希的字节。
	sum := sha256.Sum256([]byte(serverName + "\x00" + rawName))
	hash := hex.EncodeToString(sum[:])[:hashLength]
	// 按**字节**截断，和 DSH 的 String.slice 按 UTF-16 码元截断不是一回事；
	// 但走到这里说明 normalized 里已经只剩 `[A-Za-z0-9_-]`，那全是单字节，两者等价。
	//
	// 新增: 长度不够时**不能**直接切。走到这里有两种情况：一种是名字超了长度，
	// 一种是名字只被换过字符、根本没超长（比如 `mcp__a__c.d`）。DSH 的 String.slice
	// 在后一种情况下原样交回整个字符串，Go 的切片会越界 panic，所以这里显式夹一下。
	keep := maxPublicNameLength - hashLength - 1
	if len(normalized) > keep {
		normalized = normalized[:keep]
	}
	return normalized + "_" + hash
}
