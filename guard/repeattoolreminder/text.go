// 本文件的作用：判定和措辞那几件纯函数的活——参数怎么规范化成链的键、
// 工具名怎么按通配符匹配、以及两级提醒各自怎么说。
//
// 源: packages/guard/repeat-tool-reminder/src/index.ts:63-125
//
// 提醒的正文保持英文、逐字对齐 DSH：它们是发给模型的话，不是给人读的日志。

package repeattoolreminder

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// gentleReminder 是第一级那句客气话。
//
// 源: packages/guard/repeat-tool-reminder/src/index.ts:63-67
//
// 它绑的是 thresholds[0] 而不是某个写死的次数，所以自定义了第一级之后，
// 「先客气、后详细」这个升级关系仍然成立。
const gentleReminder = "You are repeating the exact same tool call with identical arguments. " +
	"Carefully analyze the previous result before calling again: if the task is " +
	"not complete, try a different approach or different arguments instead of " +
	"repeating the call."

// detailedReminder 是后面几级那句：点名工具、连续次数和那份参数。
//
// 源: packages/guard/repeat-tool-reminder/src/index.ts:70-79
func detailedReminder(toolName string, count int, argumentsPreview string) string {
	return "Repeated tool call detected:\n" +
		fmt.Sprintf("- tool: %s\n", toolName) +
		fmt.Sprintf("- consecutive_calls: %d\n", count) +
		fmt.Sprintf("- arguments: %s\n", argumentsPreview) +
		"The repeated calls are not making progress. Do not call this tool with " +
		"these exact arguments again. Inspect the latest result and choose a " +
		"different action, different arguments, or finish the task if enough " +
		"evidence has been gathered."
}

// canonicalize 把一次调用的参数变成一个可比较的规范串。
//
// 源: packages/guard/repeat-tool-reminder/src/index.ts:89-105
//
// 新增: DSH 有一个 sortJsonValue，把解出来的值递归地按键排序，好让两个只是
// 属性顺序不同的参数对象规范化成同一个串。Go 这边不需要它：encoding/json 排
// map[string]any 的时候**本来就**按键排序，而且是递归的。所以「解一遍、再排一遍」
// 就是全部。
//
// 解不动的参数原样当成字符串用，对应 DSH 那条「参数 JSON 坏掉时退回原始串」的
// 兜底。它退化的只是**判定的精度**（两份不同写法的坏 JSON 会被当成不同的调用），
// 而不是正确性。
func canonicalize(arguments json.RawMessage) string {
	var decoded any
	if err := json.Unmarshal(arguments, &decoded); err != nil {
		return string(arguments)
	}
	// 解得出来就一定排得回去：decoded 是 encoding/json 自己交出来的通用形状，
	// 里面不会有它排不动的东西。
	encoded, _ := json.Marshal(decoded)
	return string(encoded)
}

// callKey 把工具名和规范化过的参数合成链的键。
//
// 源: packages/guard/repeat-tool-reminder/src/index.ts:195
//
// 走一趟 JSON 而不是直接拼接，是为了让名字里的分隔符没法伪造出一个撞键——
// 一个叫 `a` 参数是 `["b",1]` 的调用和一个叫 `a","b` 的调用不该被认成同一次。
func callKey(toolName, canonicalArguments string) string {
	// 排一个 []string 不会失败。
	encoded, _ := json.Marshal([]string{toolName, canonicalArguments})
	return string(encoded)
}

// previewArguments 把规范串截到上限，并标出省了多少。
//
// 源: packages/guard/repeat-tool-reminder/src/index.ts:118-121
//
// 它只约束模型看得见的那段文字，链的键永远用完整的串。
//
// 按**字节**截而不是按字符：这串是 JSON，非 ASCII 在里面已经被 encoding/json
// 转义成 \uXXXX 了，所以两者在这里是同一件事；而对解不动的那份原始串按字节截
// 有可能切开一个 UTF-8 序列——那也没关系，这段文字的唯一用途是让模型认出
// 「就是这份参数」，不是给它解码。
func previewArguments(canonical string, limit int) string {
	if len(canonical) <= limit {
		return canonical
	}
	return fmt.Sprintf("%s… (+%d more chars)", canonical[:limit], len(canonical)-limit)
}

// pattern 是一条编译好的工具名模式。
//
// 源: packages/guard/repeat-tool-reminder/src/index.ts:108-111
type pattern struct {
	matcher *regexp.Regexp
}

// compilePatterns 把一串写法编译成模式。
//
// 只有 `*` 有特殊含义，别的正则元字符一律按字面匹配——所以这里先整串转义，
// 再把转义过的 `*` 换回 `.*`。这个顺序保证了转义之后的串一定编译得过，
// 也就没有「模式写错了」这种失败。
//
// 这些写法是**对工具名的判定**，不是对注册表里某一项的引用：一条谁也匹配不上的
// 模式是合法的（一个没装 MCP 的部署里，`Exclude: ["mcp_*"]` 仍然该写得出来）。
func compilePatterns(patterns []string) []pattern {
	compiled := make([]pattern, 0, len(patterns))
	for _, raw := range patterns {
		quoted := strings.ReplaceAll(regexp.QuoteMeta(raw), `\*`, ".*")
		compiled = append(compiled, pattern{matcher: regexp.MustCompile("^" + quoted + "$")})
	}
	return compiled
}

// matchesAny 说明一个工具名命中了其中任意一条模式。
func matchesAny(patterns []pattern, name string) bool {
	for _, candidate := range patterns {
		if candidate.matcher.MatchString(name) {
			return true
		}
	}
	return false
}
