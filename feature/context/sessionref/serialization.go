// 本文件的作用：把引用数据排成模型看得见的那段 JSON，并保证它拼不出一个像样的
// XML 开标签。
//
// 源: packages/context/session-reference/src/serialization.ts:1-12

package sessionref

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// stringifyTagSafeJSON 排出 JSON，并保证数据里不出现字面的 `<`。
//
// 源: packages/context/session-reference/src/serialization.ts:8-12
//
// 为什么要躲开 `<`：这段 JSON 会被夹在 `<referenced-sessions>` 和它的闭标签之间
// 送进提示词，而里面的内容是**不可信**的。放任一个字面的 `<` 进去，被引用的会话
// 就能自己写出一个闭标签，把「以下是不可信材料」这条边界从内部关掉。
// 换成转义写法之后 JSON 解出来一模一样，但那段字节里再也拼不出标签。
//
// 新增: Go 的 encoding/json 默认就会把 `<`、`>`、`&` 转义掉，看着像是白送的，
// 但那样排出来的字节比 DSH 多——`>` 和 `&` 各会从 1 字节变成 6 字节。本包的预算
// 是按**排完之后的字节数**算的，多出来的字节会把裁剪点挪到别处。所以这里关掉
// Go 自己那套转义，只做 DSH 做的那一件事。
//
// 还有一处对不齐：Go 无条件转义 U+2028 / U+2029，JSON.stringify 原样排出。
// 这只会让字节数偏大，也就是裁得更狠一点，不会让不可信内容多漏出来，
// 而关掉它需要绕开整个 encoding/json，不值当。
func stringifyTagSafeJSON(value any) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", fmt.Errorf("会话引用：这份数据排不成 JSON：%w", err)
	}
	// Encoder.Encode 会在末尾补一个换行，json.Marshal 不会；这里要的是后者那份字节。
	serialized := strings.TrimSuffix(buffer.String(), "\n")
	return strings.ReplaceAll(serialized, "<", escapedLessThan), nil
}

// escapedLessThan 是 `<` 的 JSON 转义写法：一个反斜杠接 u003c。
const escapedLessThan = "\\" + "u003c"
