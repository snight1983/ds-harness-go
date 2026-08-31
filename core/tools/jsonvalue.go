// 本文件的作用：拿一个值按一份**已经验过**的 schema 走一遍，把违规说明按路径列出来。
//
// 源: packages/core/tools/src/json-schema.ts:408-656
//
// 这些说明是**给模型看的**：工具参数不合法时它们会被拼进 `invalid arguments: …`，
// 工具返回值不合法时会被拼进 `tool "X" returned invalid output: …`，两条都会作为
// 工具结果回给模型，让它自己改了重试。所以这一整套文本保持英文，逐字对齐 DSH。

package tools

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// ValidateValue 按 schema 验一个值，交出全部违规说明；空表示通过。
//
// 源: packages/core/tools/src/json-schema.ts:654-656
//
// schema 必须是 [AssertSupportedSchema] 放过的那种；对任意 value 都是全函数，不会 panic。
//
// path 是诊断文本里的根名字。空串是参数校验专用的哨兵，显示成 arguments，
// 而且根一层的属性名前面不带点。
func ValidateValue(schema Node, value json.RawMessage, path string) []string {
	decoded, ok := decodeJSON(value)
	if !ok {
		// 新增: DSH 那一整套「lossless JSON」判定（getter 抛异常、稀疏数组、
		// undefined、Symbol、被改过的原型……）在 Go 这边一件都不存在——值是一段
		// json.RawMessage，它要么是合法 JSON、要么不是。这就是那套判定剩下的全部。
		return []string{fmt.Sprintf("%q must be a lossless JSON value", diagnosticPath(path))}
	}
	return checkValue(schema, decoded, path)
}

// diagnosticPath 把参数校验那个空串哨兵显示成 arguments。
//
// 源: packages/core/tools/src/json-schema.ts:417-419
func diagnosticPath(path string) string {
	if path == "" {
		return "arguments"
	}
	return path
}

// propertyPath 在隐式的根上不加前导的点。
//
// 源: packages/core/tools/src/json-schema.ts:422-424
func propertyPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// checkValue 走一个已经解好的值。
//
// 源: packages/core/tools/src/json-schema.ts:487-645
//
// 新增: DSH 用一个显式的帧栈走这棵树，理由和 checkSchemaNode 那边一样（JS 调用栈浅）。
// 这里是普通递归：值是 encoding/json 解出来的，那一侧自带嵌套深度上限。
func checkValue(node Node, value any, path string) []string {
	if node.OneOf != nil {
		matches := 0
		for _, branch := range node.OneOf {
			if len(checkValue(branch, value, path)) == 0 {
				matches++
			}
		}
		if matches == 1 {
			return nil
		}
		return []string{fmt.Sprintf("%q must match exactly one oneOf branch (matched %d)", diagnosticPath(path), matches)}
	}
	if node.Type == "" {
		// 只有注解、没有约束：任何合法 JSON 都过。
		return nil
	}

	switch node.Type {
	case TypeObject:
		return checkObjectValue(node, value, path)
	case TypeArray:
		return checkArrayValue(node, value, path)
	case TypeString:
		text, ok := value.(string)
		if !ok {
			return []string{fmt.Sprintf("%q must be a string", diagnosticPath(path))}
		}
		return checkScalarValue(node, text, path)
	case TypeNumber:
		number, ok := value.(float64)
		if !ok {
			return []string{fmt.Sprintf("%q must be a number", diagnosticPath(path))}
		}
		if !isJSONNumber(number) {
			return []string{fmt.Sprintf("%q must be a finite JSON number", diagnosticPath(path))}
		}
		return checkScalarValue(node, number, path)
	case TypeInteger:
		number, ok := value.(float64)
		if !ok || !isJSONNumber(number) || number != math.Trunc(number) {
			return []string{fmt.Sprintf("%q must be an integer", diagnosticPath(path))}
		}
		return checkScalarValue(node, number, path)
	case TypeBoolean:
		flag, ok := value.(bool)
		if !ok {
			return []string{fmt.Sprintf("%q must be a boolean", diagnosticPath(path))}
		}
		return checkScalarValue(node, flag, path)
	case TypeNull:
		if value != nil {
			return []string{fmt.Sprintf("%q must be null", diagnosticPath(path))}
		}
		return checkScalarValue(node, nil, path)
	}
	// 走不到：node 是 AssertSupportedSchema 放过的，type 只可能是那七个之一。
	return nil
}

// checkObjectValue 验对象：必填齐不齐、每条声明过的属性自己合不合法、有没有多出来的键。
//
// 源: packages/core/tools/src/json-schema.ts:553-580
//
// 三段的顺序是有意的，和 DSH 一致：先必填，再逐条属性，最后未声明的键。
func checkObjectValue(node Node, value any, path string) []string {
	fields, ok := value.(map[string]any)
	if !ok {
		return []string{fmt.Sprintf("%q must be an object", diagnosticPath(path))}
	}

	var violations []string
	for _, key := range node.Required {
		if _, present := fields[key]; !present {
			violations = append(violations, fmt.Sprintf("missing required property %q", propertyPath(path, key)))
		}
	}
	declared := map[string]bool{}
	for _, property := range node.Properties {
		declared[property.Name] = true
		child, present := fields[property.Name]
		if !present {
			continue
		}
		violations = append(violations, checkValue(property.Schema, child, propertyPath(path, property.Name))...)
	}
	if node.AdditionalProperties != nil && !*node.AdditionalProperties {
		// 新增: DSH 按 JS 对象的插入顺序报这些多余的键。Go 的 map 没有顺序，
		// 不排一遍的话同一份输入两次跑出来的诊断顺序都不一样。
		keys := make([]string, 0, len(fields))
		for key := range fields {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if !declared[key] {
				violations = append(violations, fmt.Sprintf("%q is not a declared property (additionalProperties: false)", propertyPath(path, key)))
			}
		}
	}
	return violations
}

// checkArrayValue 验数组：是不是数组、以及每个元素。
//
// 源: packages/core/tools/src/json-schema.ts:586-600
func checkArrayValue(node Node, value any, path string) []string {
	entries, ok := value.([]any)
	if !ok {
		return []string{fmt.Sprintf("%q must be an array", diagnosticPath(path))}
	}
	if node.Items == nil {
		return nil
	}
	var violations []string
	for index, entry := range entries {
		violations = append(violations, checkValue(*node.Items, entry, fmt.Sprintf("%s[%d]", path, index))...)
	}
	return violations
}

// checkScalarValue 验标量落没落在 enum / const 里。
//
// 源: packages/core/tools/src/json-schema.ts:479-489
func checkScalarValue(node Node, value any, path string) []string {
	if node.Enum != nil && !enumContainsValue(node.Enum, value) {
		return []string{fmt.Sprintf("%q must be one of %s", diagnosticPath(path), renderRawList(node.Enum))}
	}
	if node.Const != nil {
		wanted, ok := decodeJSON(node.Const)
		if !ok || wanted != value {
			return []string{fmt.Sprintf("%q must be %s", diagnosticPath(path), renderRaw(node.Const))}
		}
	}
	return nil
}

// isJSONNumber 说明这个数排出去再读回来还是它自己。
//
// 源: packages/core/tools/src/json-schema.ts:175-177
//
// DSH 那边还挡 NaN 和无穷。这里不挡：值是 encoding/json 解出来的，而 JSON 里压根
// 没有这两样的字面量，那条分支永远走不到——留着它只会让读的人以为它会发生。
//
// 剩下真正挡得住的只有负零：它排出去是 `0`，读回来就成了正零。Go 这一侧值本身是
// json.RawMessage、不会因为验一遍而变，保留这一条是为了对同一个输入和 DSH 给同一个答案。
func isJSONNumber(value float64) bool {
	return !(value == 0 && math.Signbit(value))
}

// enumContainsValue 说明一个解好的值在不在 enum 里。
func enumContainsValue(allowed []json.RawMessage, value any) bool {
	for _, entry := range allowed {
		if candidate, ok := decodeJSON(entry); ok && candidate == value {
			return true
		}
	}
	return false
}

// renderRaw 把一段 JSON 压掉空白再显示，对应 DSH 的 JSON.stringify。
func renderRaw(raw json.RawMessage) string {
	compacted, ok := decodeJSON(raw)
	if !ok {
		return string(raw)
	}
	// 解得出来就一定排得回去：compacted 是 encoding/json 自己交出来的通用形状，
	// 里面不会有它排不动的东西。所以这里没有失败分支。
	encoded, _ := json.Marshal(compacted)
	return string(encoded)
}

// renderRawList 把 enum 显示成一个 JSON 数组，对应 DSH 的 JSON.stringify(allowed)。
func renderRawList(allowed []json.RawMessage) string {
	parts := make([]string, len(allowed))
	for index, entry := range allowed {
		parts[index] = renderRaw(entry)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
