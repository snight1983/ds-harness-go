// 本文件的作用：一个**受限的** JSON Schema 子集——能写什么、怎么验一份 schema 自己合法、
// 以及拿一个值按它验一遍会得到哪些违规说明。
//
// 源: packages/core/tools/src/json-schema.ts
//
// 子集是故意的：工具的参数和返回值这两处 schema 会被原样发给模型提供方，而各家提供方
// 支持的关键字并不一致。限制在这八个约束关键字上，是「每一家都认得」和「本包验得动」
// 的交集。

package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

// SchemaType 是这个子集允许的七种类型。
//
// 源: packages/core/tools/src/json-schema.ts:21
type SchemaType string

const (
	TypeObject  SchemaType = "object"
	TypeArray   SchemaType = "array"
	TypeString  SchemaType = "string"
	TypeNumber  SchemaType = "number"
	TypeInteger SchemaType = "integer"
	TypeBoolean SchemaType = "boolean"
	TypeNull    SchemaType = "null"
)

// schemaTypes 是上面七个的书写顺序，诊断文本里按这个顺序列出来。
//
// 源: packages/core/tools/src/json-schema.ts:87
var schemaTypes = []SchemaType{TypeObject, TypeArray, TypeString, TypeNumber, TypeInteger, TypeBoolean, TypeNull}

// scalarTypes 是能带 enum / const 的那五种。
//
// 源: packages/core/tools/src/json-schema.ts:315-316（allowedFor 表里 enum/const 那两行）
var scalarTypes = []SchemaType{TypeString, TypeNumber, TypeInteger, TypeBoolean, TypeNull}

// Property 是一个对象 schema 里的一条属性。
//
// 新增: DSH 那边 properties 是一个 JS 对象，天然带插入顺序。Go 的 map 没有顺序，
// 而这里顺序**就是语义**：这份 schema 会原样发给提供方，某些提供方会照原样回显它，
// 它也会进提示词缓存的键（理由和 llm.ToolSchema.Parameters 用 json.RawMessage 逐字相同）。
// 用 map 排出去每次键序都可能不同，缓存就再也命不中了。
type Property struct {
	// Name 是属性名。
	Name string
	// Schema 是这条属性自己的 schema。
	Schema Node
}

// Node 是这个子集里的一个 schema 节点。
//
// 源: packages/core/tools/src/json-schema.ts:31-57
//
// 新增: DSH 那边这是一个「所有键都可选」的 TS 接口，配一整套运行期检查去拒绝
// 拼错的关键字、非字符串的 description、类型数组、非 schema 的 properties……
// 那些检查在 Go 这一侧全部由**字段类型**担着，写不出来就不用验（见 [AssertSupportedSchema]
// 里列的、还剩下哪几条）。
//
// 「没写这个关键字」和「写了但是空的」是两件不同的事，两者的区别在 JSON Schema 里
// 是有意义的（比如 properties 缺席 vs. `properties: {}`）。本类型用零值来分：
//
//   - Type 为空串、OneOf/Properties/Required/Enum/Examples 为 nil、
//     AdditionalProperties/Items/Const/Default 为 nil，都表示**没写**。
//   - 非 nil 的空切片表示**写了一个空的**。
type Node struct {
	// Type 是这个节点的类型，空串表示没写。
	Type SchemaType
	// OneOf 是「恰好命中其中一支」，至少要两支。
	OneOf []Node
	// Properties 是对象的属性表，按书写顺序。
	Properties []Property
	// Required 是必填属性名，每一个都必须在 Properties 里出现过。
	Required []string
	// AdditionalProperties 只有显式写成 false 才会拒绝未声明的属性。
	AdditionalProperties *bool
	// Items 是数组元素的 schema。
	//
	// 这是本类型里**唯一**可能成环的字段：OneOf 和 Properties 装的是值，
	// 赋值即拷贝，环绕不回来；只有这个指针能指回祖先。
	Items *Node
	// Enum 是取值白名单，每一项都必须是 Type 那种标量。
	Enum []json.RawMessage
	// Const 是钉死的取值，必须是 Type 那种标量。
	Const json.RawMessage
	// Description 是给模型看的说明。
	Description string
	// Title 是给模型看的标题。
	Title string
	// Default 是注解性的默认值，本包不拿它做任何判断，只要求它是合法 JSON。
	Default json.RawMessage
	// Examples 是注解性的示例，同上。
	Examples []json.RawMessage
}

// MarshalJSON 按固定的键顺序排出这个节点。
//
// 新增: DSH 直接把作者写的那个对象发出去，键序就是作者写的顺序。Go 的结构体没有
// 「作者写的顺序」，所以这里钉一个固定顺序——**固定**是这里唯一重要的性质，
// 同一份 Node 每次排出来必须一模一样，否则提示词缓存的键每次都不同。
func (n Node) MarshalJSON() ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	first := true
	put := func(key string, raw []byte) {
		if !first {
			out.WriteByte(',')
		}
		first = false
		out.Write(quoteJSONString(key))
		out.WriteByte(':')
		out.Write(raw)
	}
	// putValue 只用在「排得出来才知道」的那几个字段上：enum、items、oneOf、examples
	// 装的是调用方给的 json.RawMessage 或者子节点，它们都可能不合法。
	// 字符串、字符串数组、布尔这三种排不失败，直接走 put——给它们也套一层错误检查
	// 会留下几条**永远走不到**的分支，那种分支既测不着，也让读的人以为它们会发生。
	putValue := func(key string, value any) error {
		raw, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("tools: schema 的 %s 排不出去：%w", key, err)
		}
		put(key, raw)
		return nil
	}

	if n.Type != "" {
		put("type", quoteJSONString(string(n.Type)))
	}
	if n.Description != "" {
		put("description", quoteJSONString(n.Description))
	}
	if n.Title != "" {
		put("title", quoteJSONString(n.Title))
	}
	if n.Enum != nil {
		if err := putValue("enum", n.Enum); err != nil {
			return nil, err
		}
	}
	if n.Const != nil {
		put("const", n.Const)
	}
	if n.Items != nil {
		if err := putValue("items", *n.Items); err != nil {
			return nil, err
		}
	}
	if n.Properties != nil {
		var properties bytes.Buffer
		properties.WriteByte('{')
		for index, property := range n.Properties {
			if index > 0 {
				properties.WriteByte(',')
			}
			raw, err := json.Marshal(property.Schema)
			if err != nil {
				return nil, err
			}
			properties.Write(quoteJSONString(property.Name))
			properties.WriteByte(':')
			properties.Write(raw)
		}
		properties.WriteByte('}')
		put("properties", properties.Bytes())
	}
	if n.Required != nil {
		put("required", stringArrayJSON(n.Required))
	}
	if n.AdditionalProperties != nil {
		raw := []byte("false")
		if *n.AdditionalProperties {
			raw = []byte("true")
		}
		put("additionalProperties", raw)
	}
	if n.OneOf != nil {
		if err := putValue("oneOf", n.OneOf); err != nil {
			return nil, err
		}
	}
	if n.Default != nil {
		put("default", n.Default)
	}
	if n.Examples != nil {
		if err := putValue("examples", n.Examples); err != nil {
			return nil, err
		}
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// UnmarshalJSON 把一段来路不明的 JSON Schema 解进这个节点。
//
// 和 [Node.MarshalJSON] 配对。解**不动**的地方一律变成错误而不是零值：一个键拼错、
// 一个 type 写成了数组、一份 properties 不是对象，都会在这里就被拦下来，见 [ParseSchema]。
func (n *Node) UnmarshalJSON(data []byte) error {
	parsed, err := ParseSchema(data)
	if err != nil {
		return err
	}
	*n = parsed
	return nil
}

// ParseSchema 把一段来路不明的 JSON Schema 解成这个子集里的一个节点。
//
// 源: packages/core/tools/src/json-schema.ts:257-273
//
// 新增: DSH 那边 schema 天生就是一个 JS 对象，assertSupportedJsonSchema 直接对着它
// 逐个键地问「认不认得」。Go 这一侧 [Node] 是结构体，作者在代码里写的 schema 根本
// 写不出表外的键——所以那半张检查清单在 [AssertSupportedSchema] 的注释里被记成
// 「编译期就过不去」。但**从网络上收来**的 schema（一台 MCP 服务器报上来的
// inputSchema）仍然是任意 JSON，那半张清单在解码这一刻又活了过来，于是它落在这里。
//
// 解出来的节点还**没验过语义**：调用方接着自己挑 [AssertSupportedSchema] 还是
// [AssertObjectSchema]。两步分开，是因为「这段字节说的是什么」和「它说的那件事成不成立」
// 是两个问题，混在一起会让诊断分不清是谁的错。
func ParseSchema(raw json.RawMessage) (Node, error) {
	var violations []string
	node := decodeSchemaNode(raw, "schema", &violations)
	if len(violations) > 0 {
		return Node{}, &SchemaError{Violations: violations}
	}
	return node, nil
}

// schemaField 是一个 JSON 对象里的一条键值，按书写顺序。
type schemaField struct {
	name  string
	value json.RawMessage
}

// decodeSchemaFields 按**书写顺序**拆开一个 JSON 对象。
//
// 新增: 用 [json.Decoder] 逐个 token 走，而不是解进 map[string]json.RawMessage——
// Go 的 map 没有顺序，而 properties 的顺序在本包里就是语义（见 [Property] 的注释）。
func decodeSchemaFields(raw json.RawMessage) ([]schemaField, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, false
	}
	fields := []schemaField{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, false
		}
		name, ok := key.(string)
		if !ok {
			// 走不到：JSON 对象的键在语法上只能是字符串，decoder 交出别的东西之前
			// 就已经报错了。这一条是类型断言要求有个否定分支。
			return nil, false
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, false
		}
		fields = append(fields, schemaField{name: name, value: value})
	}
	if _, err := decoder.Token(); err != nil {
		return nil, false
	}
	// 尾巴上不许再有东西：`{} {}` 是两份文档，不是一份。
	if decoder.More() {
		return nil, false
	}
	return fields, true
}

// decodeSchemaNode 解一个节点，把解不动的地方按遍历顺序记进 violations。
//
// 源: packages/core/tools/src/json-schema.ts:245-298
func decodeSchemaNode(raw json.RawMessage, path string, violations *[]string) Node {
	fields, ok := decodeSchemaFields(raw)
	if !ok {
		*violations = append(*violations, path+" must be a schema object")
		return Node{}
	}
	var node Node
	for _, field := range fields {
		switch field.name {
		case "type":
			var name string
			if err := json.Unmarshal(field.value, &name); err != nil {
				*violations = append(*violations, typeViolation(field.value, path))
				continue
			}
			node.Type = SchemaType(name)
		case "oneOf":
			// 解不成数组时也把 OneOf 摆成非 nil：这样「至少两支」那条由
			// [AssertSupportedSchema] 说，措辞和作者手写的那条一模一样。
			node.OneOf = []Node{}
			var branches []json.RawMessage
			if err := json.Unmarshal(field.value, &branches); err != nil {
				continue
			}
			for index, branch := range branches {
				node.OneOf = append(node.OneOf,
					decodeSchemaNode(branch, fmt.Sprintf("%s.oneOf[%d]", path, index), violations))
			}
		case "properties":
			node.Properties = []Property{}
			nested, nestedOK := decodeSchemaFields(field.value)
			if !nestedOK {
				*violations = append(*violations, path+".properties must be an object of schemas")
				continue
			}
			for _, property := range nested {
				node.Properties = append(node.Properties, Property{
					Name:   property.name,
					Schema: decodeSchemaNode(property.value, path+".properties."+property.name, violations),
				})
			}
		case "required":
			var names []string
			if err := json.Unmarshal(field.value, &names); err != nil {
				*violations = append(*violations, path+".required must be an array of strings")
				continue
			}
			node.Required = names
		case "additionalProperties":
			var allowed bool
			if err := json.Unmarshal(field.value, &allowed); err != nil {
				*violations = append(*violations, path+".additionalProperties must be a boolean")
				continue
			}
			node.AdditionalProperties = &allowed
		case "items":
			items := decodeSchemaNode(field.value, path+".items", violations)
			node.Items = &items
		case "enum":
			// 同 oneOf：解不成数组时摆成非 nil 的空切片，让语义检查去说那句话。
			node.Enum = []json.RawMessage{}
			var entries []json.RawMessage
			if err := json.Unmarshal(field.value, &entries); err != nil {
				continue
			}
			node.Enum = entries
		case "const":
			node.Const = field.value
		case "description":
			if err := json.Unmarshal(field.value, &node.Description); err != nil {
				*violations = append(*violations, path+".description must be a string")
			}
		case "title":
			if err := json.Unmarshal(field.value, &node.Title); err != nil {
				*violations = append(*violations, path+".title must be a string")
			}
		case "default":
			node.Default = field.value
		case "examples":
			node.Examples = []json.RawMessage{}
			var entries []json.RawMessage
			if err := json.Unmarshal(field.value, &entries); err != nil {
				*violations = append(*violations, path+".examples annotation must be lossless JSON data")
				continue
			}
			node.Examples = entries
		default:
			*violations = append(*violations, fmt.Sprintf(
				"%s.%s is not a supported keyword (subset: type/oneOf/properties/required/additionalProperties/items/enum/const + annotations)",
				path, field.name))
		}
	}
	return node
}

// typeViolation 区分「type 写成了一个数组」和「type 根本不是那七个之一」。
//
// 源: packages/core/tools/src/json-schema.ts:303-306
//
// 类型数组（`"type": ["string", "null"]`）是 JSON Schema 里极常见的写法，而本子集
// 不支持它，所以它值得一句专门的话——否则作者只会看到「必须是那七个之一」，
// 而他写的每一项确实都在那七个里。
func typeViolation(raw json.RawMessage, path string) string {
	var array []json.RawMessage
	if json.Unmarshal(raw, &array) == nil {
		return path + ".type must be a single type string (type arrays are not supported)"
	}
	return fmt.Sprintf("%s.type must be one of %s", path, joinTypes(schemaTypes))
}

// quoteJSONString 把一个字符串排成 JSON 字面量。
//
// 用 encoding/json 而不是 strconv.Quote：后者排的是 **Go** 的字符串字面量，
// 两者对控制字符和非 ASCII 的转义规则并不一样。
func quoteJSONString(value string) []byte {
	// 这里的 error 一定是 nil：json.Marshal 排一个 string 不会失败，非法 UTF-8 会被
	// 替换字符顶掉而不是报错。忽略它不掩盖任何东西，而给它写一条分支反倒是在
	// 暗示这件事会发生。
	quoted, _ := json.Marshal(value)
	return quoted
}

// stringArrayJSON 把一串字符串排成 JSON 数组。
//
// 理由和 [quoteJSONString] 一样：排一个 []string 不会失败，所以这里没有错误返回。
func stringArrayJSON(values []string) []byte {
	var out bytes.Buffer
	out.WriteByte('[')
	for index, value := range values {
		if index > 0 {
			out.WriteByte(',')
		}
		out.Write(quoteJSONString(value))
	}
	out.WriteByte(']')
	return out.Bytes()
}

// ErrUnsupportedSchema 表示一份 schema 用了这个子集之外的东西。
//
// 新增: DSH 那边是 `JsonSchemaError extends HarnessError`，靠原型链分派。
// Go 这一侧和本仓库其他包一致：具体类型 [SchemaError] 加一个哨兵，
// errors.Is 认哨兵、errors.As 取违规清单。
var ErrUnsupportedSchema = errors.New("tools: schema 超出了支持的子集")

// SchemaCode 是 [SchemaError] 的机器可读代号。
//
// 源: packages/core/tools/src/json-schema.ts:65-73
const SchemaCode = "UNSUPPORTED_SCHEMA"

// SchemaError 是一份 schema 自己不合法。
//
// 源: packages/core/tools/src/json-schema.ts:65-73
type SchemaError struct {
	// Violations 是按遍历顺序排的全部违规说明。
	Violations []string
}

// Error 把全部违规说明串成一条。
func (err *SchemaError) Error() string {
	return fmt.Sprintf("%s: %s", ErrUnsupportedSchema.Error(), strings.Join(err.Violations, "; "))
}

// Unwrap 让 errors.Is 认出 [ErrUnsupportedSchema]。
func (err *SchemaError) Unwrap() error { return ErrUnsupportedSchema }

// AssertSupportedSchema 验一个 schema 节点自己是否合法。
//
// 源: packages/core/tools/src/json-schema.ts:385-390
//
// 「只有注解、什么约束都没写」是合法的，那是「任意 JSON」的标准写法。
// 要求根节点是对象的调用方用 [AssertObjectSchema]。
//
// 新增: DSH 那张检查清单里有一多半在 Go 这边写不出来，所以也验不着——
// 拼错的关键字、非字符串的 description/title、类型数组、非 schema 的 properties、
// 非字符串数组的 required、非布尔的 additionalProperties，这些都是编译期就过不去的。
// 剩下这些还得靠运行期验：
//
//  1. Items 指回祖先（成环）；
//  2. type 和 oneOf 同时写了、或者两个都没写却写了别的约束；
//  3. oneOf 旁边不许有别的约束、且至少要两支；
//  4. type 不在那七个里；
//  5. 某个关键字用在了不支持它的 type 上；
//  6. required 点名了 properties 里没有的属性；
//  7. enum / const 的取值和 type 对不上、const 不在 enum 里；
//  8. 注解（default / examples）不是合法 JSON；
//  9. **新增的一条**：properties 里同一个名字写了两遍。DSH 那边 properties 是 JS 对象，
//     重名在语法上就不可能；Go 的切片装得下，而排出去之后会得到一个有两个同名键的
//     JSON 对象，它读回来是哪一份取决于解码方。
func AssertSupportedSchema(node Node) error {
	var violations []string
	checkSchemaNode(node, "schema", &violations, map[*Node]bool{})
	if len(violations) > 0 {
		return &SchemaError{Violations: violations}
	}
	return nil
}

// AssertObjectSchema 在 [AssertSupportedSchema] 之上再要求根节点是对象。
//
// 源: packages/core/tools/src/json-schema.ts:397-406
//
// 工具参数和结构化输出都走这一条：模型那一侧的「参数」在每一家提供方的协议里
// 都是一个对象，根上写别的类型发出去就是一份对方读不懂的请求。
func AssertObjectSchema(node Node) error {
	var violations []string
	checkSchemaNode(node, "schema", &violations, map[*Node]bool{})
	if len(violations) == 0 && node.Type != TypeObject {
		violations = append(violations, `schema.type must be "object" (structured output is object-rooted)`)
	}
	if len(violations) > 0 {
		return &SchemaError{Violations: violations}
	}
	return nil
}

// siblingKeyword 是一个约束关键字的三件事：叫什么、这个节点写没写它、它只能挂在哪种 type 上。
//
// 三样并排放在一张表里，是因为它们在诊断里总是一起用：先列出「写了哪几个」，
// 再逐个问「这一个能不能挂在这个 type 上」，两处必须给出同一个顺序。
type siblingKeyword struct {
	name      string
	declared  func(node Node) bool
	allowedOn func(schemaType SchemaType) bool
}

// siblingKeywords 是不许和 oneOf 并排出现的那六个关键字，按诊断文本里列出来的顺序。
//
// 源: packages/core/tools/src/json-schema.ts:200, 310-318（allowedFor 表）
//
// 新增: DSH 那边「写没写某个关键字」是 `keyword in node`，一个字符串就问得出来。
// Go 这一侧每个关键字对应一个具名字段，字符串问不动，所以「怎么问」得跟着名字
// 一起写进表里。好处是这里再没有「表外的关键字」这种情况——那在 DSH 那边是一条
// 永远走不到、却看起来会发生的兜底分支。
var siblingKeywords = []siblingKeyword{
	{"properties", func(node Node) bool { return node.Properties != nil }, onlyObjects},
	{"required", func(node Node) bool { return node.Required != nil }, onlyObjects},
	{"additionalProperties", func(node Node) bool { return node.AdditionalProperties != nil }, onlyObjects},
	{"items", func(node Node) bool { return node.Items != nil }, onlyArrays},
	{"enum", func(node Node) bool { return node.Enum != nil }, onlyScalars},
	{"const", func(node Node) bool { return node.Const != nil }, onlyScalars},
}

// onlyObjects 是 properties / required / additionalProperties 的适用范围。
func onlyObjects(schemaType SchemaType) bool { return schemaType == TypeObject }

// onlyArrays 是 items 的适用范围。
func onlyArrays(schemaType SchemaType) bool { return schemaType == TypeArray }

// onlyScalars 是 enum / const 的适用范围。
func onlyScalars(schemaType SchemaType) bool { return containsType(scalarTypes, schemaType) }

// declaredSiblings 说明这个节点写了上表里的哪几个关键字，按表的顺序。
func declaredSiblings(node Node) []siblingKeyword {
	var declared []siblingKeyword
	for _, keyword := range siblingKeywords {
		if keyword.declared(node) {
			declared = append(declared, keyword)
		}
	}
	return declared
}

// checkSchemaNode 把一棵 schema 走一遍，把违规说明按遍历顺序追加到 violations 上。
//
// 源: packages/core/tools/src/json-schema.ts:227-370
//
// 新增: DSH 用一个显式的任务栈走这棵树，理由是 JS 的调用栈很浅、一份深 schema 能把它
// 撑爆。Go 的 goroutine 栈是按需增长的，而这份 schema 本身要么是作者在代码里写的、
// 要么是 encoding/json 解出来的（那一侧自带嵌套深度上限），所以这里就是普通的递归。
//
// seen 装的是**当前这条路径上**的节点地址，用来抓 Items 指回祖先。走完一个节点就撤出来，
// 所以两个兄弟指向同一份 Node 不算成环——那是共享，不是环。
func checkSchemaNode(node Node, path string, violations *[]string, seen map[*Node]bool) {
	// 注解只要求是合法 JSON，和 type/oneOf 那一套无关，所以先查。
	if node.Default != nil && !json.Valid(node.Default) {
		*violations = append(*violations, path+".default annotation must be lossless JSON data")
	}
	for index, example := range node.Examples {
		if !json.Valid(example) {
			*violations = append(*violations, fmt.Sprintf("%s.examples[%d] annotation must be lossless JSON data", path, index))
		}
	}

	hasType := node.Type != ""
	hasOneOf := node.OneOf != nil
	if hasType && hasOneOf {
		*violations = append(*violations, path+" cannot declare both type and oneOf")
		return
	}
	if !hasType && !hasOneOf {
		for _, keyword := range declaredSiblings(node) {
			*violations = append(*violations, fmt.Sprintf("%s.%s requires type or oneOf", path, keyword.name))
		}
		return
	}

	if hasOneOf {
		if len(node.OneOf) < 2 {
			*violations = append(*violations, path+".oneOf must be an array of at least two schemas")
		} else {
			for index := range node.OneOf {
				checkSchemaNode(node.OneOf[index], fmt.Sprintf("%s.oneOf[%d]", path, index), violations, seen)
			}
		}
		for _, keyword := range declaredSiblings(node) {
			*violations = append(*violations, fmt.Sprintf("%s.%s is not supported beside oneOf", path, keyword.name))
		}
		return
	}

	if !containsType(schemaTypes, node.Type) {
		*violations = append(*violations, fmt.Sprintf("%s.type must be one of %s", path, joinTypes(schemaTypes)))
		return
	}
	for _, keyword := range declaredSiblings(node) {
		if !keyword.allowedOn(node.Type) {
			*violations = append(*violations, fmt.Sprintf("%s.%s is not supported on type %q", path, keyword.name, node.Type))
		}
	}

	switch node.Type {
	case TypeObject:
		checkObjectSchema(node, path, violations, seen)
	case TypeArray:
		if node.Items != nil {
			if seen[node.Items] {
				*violations = append(*violations, path+".items is circular")
				break
			}
			seen[node.Items] = true
			checkSchemaNode(*node.Items, path+".items", violations, seen)
			delete(seen, node.Items)
		}
	default:
		checkScalarSchema(node, path, violations)
	}
}

// checkObjectSchema 验对象那几条：属性重名、每条属性自己合法、required 点名的都在。
//
// 源: packages/core/tools/src/json-schema.ts:203-223, 320-337
func checkObjectSchema(node Node, path string, violations *[]string, seen map[*Node]bool) {
	declared := map[string]bool{}
	for _, property := range node.Properties {
		if declared[property.Name] {
			*violations = append(*violations, fmt.Sprintf("%s.properties declares %q twice", path, property.Name))
			continue
		}
		declared[property.Name] = true
	}
	for _, property := range node.Properties {
		checkSchemaNode(property.Schema, path+".properties."+property.Name, violations, seen)
	}
	for _, key := range node.Required {
		if !declared[key] {
			*violations = append(*violations, fmt.Sprintf("%s.required names %q which is not in properties", path, key))
		}
	}
}

// checkScalarSchema 验标量那两条：enum 和 const 的取值和 type 对不对得上。
//
// 源: packages/core/tools/src/json-schema.ts:341-359
func checkScalarSchema(node Node, path string, violations *[]string) {
	enumValid := len(node.Enum) > 0
	if enumValid {
		for _, entry := range node.Enum {
			value, decoded := decodeJSON(entry)
			if !decoded || !valueMatchesType(node.Type, value) {
				enumValid = false
				break
			}
		}
	}
	if node.Enum != nil && !enumValid {
		*violations = append(*violations, fmt.Sprintf("%s.enum must be a non-empty array of %s values", path, node.Type))
	}
	if node.Const == nil {
		return
	}
	// const 只解一遍：下面那条「必须是 enum 里的一个」比的是解出来的值，
	// 再解一次等于给自己留一条「第二次解失败了怎么办」的走不到的分支。
	constValue, decoded := decodeJSON(node.Const)
	if !decoded || !valueMatchesType(node.Type, constValue) {
		*violations = append(*violations, fmt.Sprintf("%s.const must be a %s value", path, node.Type))
		return
	}
	if enumValid && !enumContainsValue(node.Enum, constValue) {
		*violations = append(*violations, fmt.Sprintf("%s.const must be one of %s.enum when both are declared", path, path))
	}
}

// containsType 是「这个类型在不在这张表里」。
func containsType(types []SchemaType, wanted SchemaType) bool {
	for _, candidate := range types {
		if candidate == wanted {
			return true
		}
	}
	return false
}

// joinTypes 按书写顺序用斜杠串起来，给 `type must be one of …` 那条用。
func joinTypes(types []SchemaType) string {
	names := make([]string, len(types))
	for index, schemaType := range types {
		names[index] = string(schemaType)
	}
	return strings.Join(names, "/")
}

// valueMatchesType 说明一个解出来的值是不是这个类型的标量。
//
// 源: packages/core/tools/src/json-schema.ts:178-190
func valueMatchesType(schemaType SchemaType, value any) bool {
	switch schemaType {
	case TypeString:
		_, ok := value.(string)
		return ok
	case TypeNumber:
		_, ok := value.(float64)
		return ok
	case TypeInteger:
		number, ok := value.(float64)
		return ok && number == math.Trunc(number)
	case TypeBoolean:
		_, ok := value.(bool)
		return ok
	case TypeNull:
		return value == nil
	}
	// object 和 array 走不到这里：唯一的调用方是 checkScalarSchema，
	// 而它只在类型分派的标量那一支里被叫到。这一条是 Go 要求函数有返回值，
	// 不是一种会发生的情况——它是本包唯一一处测不着的语句，故此说明。
	return false
}

// decodeJSON 把一段 JSON 解成 encoding/json 的通用形状。
func decodeJSON(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, false
	}
	return decoded, true
}
