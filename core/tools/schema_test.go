// 本文件的作用：把那个受限 JSON Schema 子集的两条边界钉住——一份 schema 自己合不合法，
// 以及拿一个值按它验一遍会说出哪几句话。
//
// # 这两件事的错法
//
//   - **把「没写这个关键字」和「写了但是空的」拧成一件事**。properties 缺席和
//     `properties: {}` 在 JSON Schema 里是两回事，本包用 nil 和空切片分这两者；
//     一旦分不开，一份「什么都不限制」的 schema 就会被当成「一个属性都不许有」。
//   - **让 schema 排出去的键序不固定**。这份 schema 会进提示词缓存的键，
//     顺序一变就再也命不中。
//   - **让违规说明的顺序不确定**。多余属性那一段读的是 Go 的 map，不排一遍的话
//     同一份输入两次跑出来的诊断顺序都不一样，快照测试会随机红。
//   - **让 ValidateValue 对没验过的 schema 炸掉**。它被文档承诺是全函数：
//     参数校验那一路的 schema 来自工具作者，输出校验那一路的值来自执行体，
//     两边都不该因为一份写坏的 schema 让整个进程掉下去。
package tools_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ds-harness-go/core/tools"
)

// boolPtr 造一个布尔指针，给 AdditionalProperties 用。
func boolPtr(value bool) *bool { return &value }

// raw 把一段字面量当成 JSON 原文。
func raw(literal string) json.RawMessage { return json.RawMessage(literal) }

// violationsOf 取出一次 schema 校验的违规清单；通过的话交出 nil。
func violationsOf(t *testing.T, err error) []string {
	t.Helper()
	if err == nil {
		return nil
	}
	if !errors.Is(err, tools.ErrUnsupportedSchema) {
		t.Fatalf("这个错误应该能被 ErrUnsupportedSchema 认出来：%v", err)
	}
	var schemaErr *tools.SchemaError
	if !errors.As(err, &schemaErr) {
		t.Fatalf("这个错误应该能取出违规清单：%v", err)
	}
	return schemaErr.Violations
}

func TestAssertSupportedSchemaAcceptsTheSubset(t *testing.T) {
	t.Parallel()
	cases := map[string]tools.Node{
		"只有注解、什么约束都没写":  {Description: "任意 JSON", Title: "anything"},
		"空对象":           {Type: tools.TypeObject},
		"写了一个空的属性表":     {Type: tools.TypeObject, Properties: []tools.Property{}},
		"数组带元素 schema":  {Type: tools.TypeArray, Items: &tools.Node{Type: tools.TypeString}},
		"数组不带元素 schema": {Type: tools.TypeArray},
		"两支 oneOf": {OneOf: []tools.Node{
			{Type: tools.TypeString},
			{Type: tools.TypeNull},
		}},
		"enum 和 const 都写且对得上": {
			Type:  tools.TypeString,
			Enum:  []json.RawMessage{raw(`"a"`), raw(`"b"`)},
			Const: raw(`"a"`),
		},
		"整数 enum": {Type: tools.TypeInteger, Enum: []json.RawMessage{raw("1"), raw("2")}},
		// 这两条把 valueMatchesType 剩下的两支走到：小数只有 number 认，
		// 而 null 是唯一一个「取值就是没有取值」的标量。
		"小数 enum 和 const": {
			Type:  tools.TypeNumber,
			Enum:  []json.RawMessage{raw("1.5"), raw("2.5")},
			Const: raw("1.5"),
		},
		"null 的 const": {Type: tools.TypeNull, Const: raw("null")},
		"注解是合法 JSON": {
			Type:     tools.TypeString,
			Default:  raw(`"x"`),
			Examples: []json.RawMessage{raw(`"y"`), raw(`"z"`)},
		},
		"两个兄弟指向同一份节点不算成环": func() tools.Node {
			shared := &tools.Node{Type: tools.TypeString}
			return tools.Node{Type: tools.TypeObject, Properties: []tools.Property{
				{Name: "a", Schema: tools.Node{Type: tools.TypeArray, Items: shared}},
				{Name: "b", Schema: tools.Node{Type: tools.TypeArray, Items: shared}},
			}}
		}(),
	}
	for name, node := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if violations := violationsOf(t, tools.AssertSupportedSchema(node)); violations != nil {
				t.Fatalf("这份 schema 应该是合法的，却报了：%v", violations)
			}
		})
	}
}

func TestAssertSupportedSchemaRejectsWhatItCannotSend(t *testing.T) {
	t.Parallel()
	circular := tools.Node{Type: tools.TypeArray}
	circular.Items = &circular

	cases := map[string]struct {
		node   tools.Node
		wanted string
	}{
		"type 和 oneOf 同时写": {
			node:   tools.Node{Type: tools.TypeString, OneOf: []tools.Node{{Type: tools.TypeNull}, {Type: tools.TypeString}}},
			wanted: "schema cannot declare both type and oneOf",
		},
		"两个都没写却写了约束": {
			node:   tools.Node{Properties: []tools.Property{{Name: "a", Schema: tools.Node{Type: tools.TypeString}}}},
			wanted: "schema.properties requires type or oneOf",
		},
		"两个都没写却写了 required": {
			node:   tools.Node{Required: []string{"a"}},
			wanted: "schema.required requires type or oneOf",
		},
		"两个都没写却写了 additionalProperties": {
			node:   tools.Node{AdditionalProperties: boolPtr(false)},
			wanted: "schema.additionalProperties requires type or oneOf",
		},
		"两个都没写却写了 items": {
			node:   tools.Node{Items: &tools.Node{Type: tools.TypeString}},
			wanted: "schema.items requires type or oneOf",
		},
		"两个都没写却写了 enum": {
			node:   tools.Node{Enum: []json.RawMessage{raw(`"a"`)}},
			wanted: "schema.enum requires type or oneOf",
		},
		"两个都没写却写了 const": {
			node:   tools.Node{Const: raw(`"a"`)},
			wanted: "schema.const requires type or oneOf",
		},
		"oneOf 只有一支": {
			node:   tools.Node{OneOf: []tools.Node{{Type: tools.TypeString}}},
			wanted: "schema.oneOf must be an array of at least two schemas",
		},
		"oneOf 是空的": {
			node:   tools.Node{OneOf: []tools.Node{}},
			wanted: "schema.oneOf must be an array of at least two schemas",
		},
		"oneOf 旁边不许有别的约束": {
			node: tools.Node{
				OneOf: []tools.Node{{Type: tools.TypeString}, {Type: tools.TypeNull}},
				Enum:  []json.RawMessage{raw(`"a"`)},
			},
			wanted: "schema.enum is not supported beside oneOf",
		},
		"oneOf 的某一支自己不合法": {
			node:   tools.Node{OneOf: []tools.Node{{Type: tools.TypeString}, {Type: "bogus"}}},
			wanted: `schema.oneOf[1].type must be one of object/array/string/number/integer/boolean/null`,
		},
		"type 不在那七个里": {
			node:   tools.Node{Type: "text"},
			wanted: "schema.type must be one of object/array/string/number/integer/boolean/null",
		},
		"properties 用在字符串上": {
			node: tools.Node{
				Type:       tools.TypeString,
				Properties: []tools.Property{{Name: "a", Schema: tools.Node{Type: tools.TypeString}}},
			},
			wanted: `schema.properties is not supported on type "string"`,
		},
		"items 用在对象上": {
			node:   tools.Node{Type: tools.TypeObject, Items: &tools.Node{Type: tools.TypeString}},
			wanted: `schema.items is not supported on type "object"`,
		},
		"enum 用在对象上": {
			node:   tools.Node{Type: tools.TypeObject, Enum: []json.RawMessage{raw(`{}`)}},
			wanted: `schema.enum is not supported on type "object"`,
		},
		"const 用在数组上": {
			node:   tools.Node{Type: tools.TypeArray, Const: raw(`[]`)},
			wanted: `schema.const is not supported on type "array"`,
		},
		"同一个属性名写了两遍": {
			node: tools.Node{Type: tools.TypeObject, Properties: []tools.Property{
				{Name: "a", Schema: tools.Node{Type: tools.TypeString}},
				{Name: "a", Schema: tools.Node{Type: tools.TypeNumber}},
			}},
			wanted: `schema.properties declares "a" twice`,
		},
		"required 点了不存在的属性": {
			node: tools.Node{
				Type:       tools.TypeObject,
				Properties: []tools.Property{{Name: "a", Schema: tools.Node{Type: tools.TypeString}}},
				Required:   []string{"b"},
			},
			wanted: `schema.required names "b" which is not in properties`,
		},
		"某条属性自己不合法": {
			node: tools.Node{Type: tools.TypeObject, Properties: []tools.Property{
				{Name: "a", Schema: tools.Node{Type: "bogus"}},
			}},
			wanted: "schema.properties.a.type must be one of",
		},
		"数组元素自己不合法": {
			node:   tools.Node{Type: tools.TypeArray, Items: &tools.Node{Type: "bogus"}},
			wanted: "schema.items.type must be one of",
		},
		// 环在第二层才报得出来：seen 装的是**已经从 items 走下去过**的节点，
		// 根节点自己没被走下去过，所以是 schema.items 那一层认出它绕回来了。
		"items 指回祖先": {
			node:   circular,
			wanted: "schema.items.items is circular",
		},
		"enum 是空数组": {
			node:   tools.Node{Type: tools.TypeString, Enum: []json.RawMessage{}},
			wanted: "schema.enum must be a non-empty array of string values",
		},
		"enum 里混进了别的类型": {
			node:   tools.Node{Type: tools.TypeString, Enum: []json.RawMessage{raw(`"a"`), raw("1")}},
			wanted: "schema.enum must be a non-empty array of string values",
		},
		"enum 里有一项不是合法 JSON": {
			node:   tools.Node{Type: tools.TypeString, Enum: []json.RawMessage{raw(`"a"`), raw(`{oops`)}},
			wanted: "schema.enum must be a non-empty array of string values",
		},
		"整数 enum 里混进了小数": {
			node:   tools.Node{Type: tools.TypeInteger, Enum: []json.RawMessage{raw("1.5")}},
			wanted: "schema.enum must be a non-empty array of integer values",
		},
		"const 类型对不上": {
			node:   tools.Node{Type: tools.TypeBoolean, Const: raw(`"true"`)},
			wanted: "schema.const must be a boolean value",
		},
		// 「写了一个空的 RawMessage」和「没写」是两回事：前者非 nil，所以走进检查，
		// 但它一个字节都没有、解不出任何值，按「取值和 type 对不上」处理。
		"const 是一段空的 JSON": {
			node:   tools.Node{Type: tools.TypeString, Const: json.RawMessage{}},
			wanted: "schema.const must be a string value",
		},
		"const 不在 enum 里": {
			node: tools.Node{
				Type:  tools.TypeString,
				Enum:  []json.RawMessage{raw(`"a"`)},
				Const: raw(`"b"`),
			},
			wanted: "schema.const must be one of schema.enum when both are declared",
		},
		"default 不是合法 JSON": {
			node:   tools.Node{Type: tools.TypeString, Default: raw(`{oops`)},
			wanted: "schema.default annotation must be lossless JSON data",
		},
		"examples 里有一项不是合法 JSON": {
			node: tools.Node{
				Type:     tools.TypeString,
				Examples: []json.RawMessage{raw(`"ok"`), raw(`{oops`)},
			},
			wanted: "schema.examples[1] annotation must be lossless JSON data",
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			violations := violationsOf(t, tools.AssertSupportedSchema(testCase.node))
			if violations == nil {
				t.Fatalf("这份 schema 应该被拒绝")
			}
			joined := strings.Join(violations, "\n")
			if !strings.Contains(joined, testCase.wanted) {
				t.Fatalf("违规说明里应该有 %q，实际是：\n%s", testCase.wanted, joined)
			}
		})
	}
}

func TestSchemaViolationsCarryTheirIdentity(t *testing.T) {
	t.Parallel()
	err := tools.AssertSupportedSchema(tools.Node{Type: "bogus"})
	var schemaErr *tools.SchemaError
	if !errors.As(err, &schemaErr) {
		t.Fatalf("应该是一个 SchemaError：%v", err)
	}
	if schemaErr.ErrorName() != "JsonSchemaError" || schemaErr.ErrorCode() != tools.SchemaCode {
		t.Fatalf("身份不对：%s / %s", schemaErr.ErrorName(), schemaErr.ErrorCode())
	}
	// Error() 要把哨兵那句话和全部违规说明都带上，这样只看日志文本也定位得到。
	if !strings.Contains(err.Error(), tools.ErrUnsupportedSchema.Error()) {
		t.Fatalf("错误文本里应该带上哨兵那句话：%q", err.Error())
	}
	if !strings.Contains(err.Error(), "must be one of") {
		t.Fatalf("错误文本里应该带上违规说明：%q", err.Error())
	}
}

func TestAssertObjectSchemaRequiresAnObjectRoot(t *testing.T) {
	t.Parallel()
	if err := tools.AssertObjectSchema(tools.Node{Type: tools.TypeObject}); err != nil {
		t.Fatalf("对象根应该放行：%v", err)
	}

	violations := violationsOf(t, tools.AssertObjectSchema(tools.Node{Type: tools.TypeString}))
	if len(violations) != 1 || !strings.Contains(violations[0], `schema.type must be "object"`) {
		t.Fatalf("应该只报根类型这一条：%v", violations)
	}

	// 根节点本身就不合法时，只报那一条：再补一句「根必须是对象」是噪声，
	// 因为 type 都还没成立，谈不上它是不是 object。
	violations = violationsOf(t, tools.AssertObjectSchema(tools.Node{Type: "bogus"}))
	if len(violations) != 1 || strings.Contains(violations[0], `must be "object"`) {
		t.Fatalf("根类型不合法时不该再补一句：%v", violations)
	}
}

func TestSchemaMarshalsWithAFixedKeyOrder(t *testing.T) {
	t.Parallel()
	node := tools.Node{
		Type:        tools.TypeObject,
		Description: "一份说明",
		Title:       "标题",
		Properties: []tools.Property{
			{Name: "b", Schema: tools.Node{Type: tools.TypeString}},
			{Name: "a", Schema: tools.Node{Type: tools.TypeArray, Items: &tools.Node{Type: tools.TypeNumber}}},
		},
		Required:             []string{"b", "a"},
		AdditionalProperties: boolPtr(false),
		Default:              raw(`{"b":"x"}`),
		Examples:             []json.RawMessage{raw(`{"b":"y"}`)},
	}
	encoded, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	wanted := `{"type":"object","description":"一份说明","title":"标题",` +
		`"properties":{"b":{"type":"string"},"a":{"type":"array","items":{"type":"number"}}},` +
		`"required":["b","a"],"additionalProperties":false,` +
		`"default":{"b":"x"},"examples":[{"b":"y"}]}`
	if string(encoded) != wanted {
		t.Fatalf("排出来的形状不对：\n实际 %s\n期望 %s", encoded, wanted)
	}

	// 属性顺序**就是**语义：作者写的顺序原样发出去，不按字典序重排。
	if strings.Index(string(encoded), `"b":{`) > strings.Index(string(encoded), `"a":{`) {
		t.Fatalf("属性顺序被重排了：%s", encoded)
	}

	// 同一份 Node 排两次必须一模一样，否则提示词缓存的键每次都不同。
	again, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("第二次排不出去：%v", err)
	}
	if string(again) != string(encoded) {
		t.Fatalf("同一份 Node 两次排出来不一样：\n%s\n%s", encoded, again)
	}
}

func TestSchemaMarshalsTheRemainingKeywords(t *testing.T) {
	t.Parallel()
	node := tools.Node{
		Enum:  []json.RawMessage{raw(`"a"`)},
		Const: raw(`"a"`),
	}
	// 这两个关键字要求先有 type，但 MarshalJSON 是纯投影、不做校验——
	// 它排的是作者写下的东西，合不合法由 AssertSupportedSchema 说了算。
	node.Type = tools.TypeString
	encoded, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	if string(encoded) != `{"type":"string","enum":["a"],"const":"a"}` {
		t.Fatalf("形状不对：%s", encoded)
	}

	oneOf := tools.Node{OneOf: []tools.Node{{Type: tools.TypeString}, {Type: tools.TypeNull}}}
	encoded, err = json.Marshal(oneOf)
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	if string(encoded) != `{"oneOf":[{"type":"string"},{"type":"null"}]}` {
		t.Fatalf("oneOf 形状不对：%s", encoded)
	}

	yes := tools.Node{Type: tools.TypeObject, AdditionalProperties: boolPtr(true)}
	encoded, err = json.Marshal(yes)
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	if string(encoded) != `{"type":"object","additionalProperties":true}` {
		t.Fatalf("additionalProperties: true 的形状不对：%s", encoded)
	}

	// 空的属性表和缺席的属性表排出来不一样——这两件事在 JSON Schema 里是有区别的。
	empty, err := json.Marshal(tools.Node{Type: tools.TypeObject, Properties: []tools.Property{}})
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	if string(empty) != `{"type":"object","properties":{}}` {
		t.Fatalf("空属性表排出来不对：%s", empty)
	}
	absent, err := json.Marshal(tools.Node{Type: tools.TypeObject})
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	if string(absent) != `{"type":"object"}` {
		t.Fatalf("缺席的属性表排出来不对：%s", absent)
	}
}

func TestSchemaMarshalReportsWhatItCannotEncode(t *testing.T) {
	t.Parallel()
	bad := raw(`{oops`)
	cases := map[string]tools.Node{
		"enum 里有一段不合法 JSON":      {Type: tools.TypeString, Enum: []json.RawMessage{bad}},
		"items 里有一段不合法 JSON":     {Type: tools.TypeArray, Items: &tools.Node{Type: tools.TypeString, Enum: []json.RawMessage{bad}}},
		"某条属性里有一段不合法 JSON":       {Type: tools.TypeObject, Properties: []tools.Property{{Name: "a", Schema: tools.Node{Enum: []json.RawMessage{bad}}}}},
		"oneOf 的某一支里有一段不合法 JSON": {OneOf: []tools.Node{{Enum: []json.RawMessage{bad}}}},
		"examples 里有一段不合法 JSON":  {Type: tools.TypeString, Examples: []json.RawMessage{bad}},
	}
	for name, node := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := json.Marshal(node); err == nil {
				t.Fatalf("这份 schema 不该排得出去")
			}
		})
	}
}

func TestValidateValueAcceptsWhatTheSchemaAllows(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		schema tools.Node
		value  string
	}{
		"只有注解就什么都过":       {tools.Node{Description: "任意"}, `{"anything":true}`},
		"字符串":             {tools.Node{Type: tools.TypeString}, `"hi"`},
		"数字":              {tools.Node{Type: tools.TypeNumber}, `1.5`},
		"整数":              {tools.Node{Type: tools.TypeInteger}, `7`},
		"布尔":              {tools.Node{Type: tools.TypeBoolean}, `true`},
		"null":            {tools.Node{Type: tools.TypeNull}, `null`},
		"不带元素 schema 的数组": {tools.Node{Type: tools.TypeArray}, `[1,"a",null]`},
		"带元素 schema 的数组": {
			tools.Node{Type: tools.TypeArray, Items: &tools.Node{Type: tools.TypeString}},
			`["a","b"]`,
		},
		"缺席的可选属性":  {objectSchema("text"), `{"text":"x"}`},
		"enum 命中":  {tools.Node{Type: tools.TypeString, Enum: []json.RawMessage{raw(`"a"`)}}, `"a"`},
		"const 命中": {tools.Node{Type: tools.TypeString, Const: raw(`"a"`)}, `"a"`},
		"oneOf 恰好命中一支": {
			tools.Node{OneOf: []tools.Node{{Type: tools.TypeString}, {Type: tools.TypeNull}}},
			`null`,
		},
		"没写 additionalProperties 就不管多余的键": {objectSchema("text"), `{"text":"x","extra":1}`},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			violations := tools.ValidateValue(testCase.schema, raw(testCase.value), "value")
			if len(violations) > 0 {
				t.Fatalf("这个值应该通过，却报了：%v", violations)
			}
		})
	}
}

func TestValidateValueNamesEveryViolation(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		schema tools.Node
		value  string
		path   string
		wanted []string
	}{
		"整段不是合法 JSON": {
			schema: tools.Node{Type: tools.TypeString},
			value:  `{oops`,
			path:   "value",
			wanted: []string{`"value" must be a lossless JSON value`},
		},
		"参数那一路的根显示成 arguments": {
			schema: tools.Node{Type: tools.TypeString},
			value:  `1`,
			path:   "",
			wanted: []string{`"arguments" must be a string`},
		},
		"不是对象": {
			schema: objectSchema("text"),
			value:  `[]`,
			path:   "value",
			wanted: []string{`"value" must be an object`},
		},
		"缺必填属性": {
			schema: objectSchema("text"),
			value:  `{}`,
			path:   "value",
			wanted: []string{`missing required property "value.text"`},
		},
		"根一层的属性名前面不带点": {
			schema: objectSchema("text"),
			value:  `{}`,
			path:   "",
			wanted: []string{`missing required property "text"`},
		},
		"属性自己类型不对": {
			schema: objectSchema("text"),
			value:  `{"text":1}`,
			path:   "value",
			wanted: []string{`"value.text" must be a string`},
		},
		"多出来的键按字典序报": {
			schema: tools.Node{
				Type:                 tools.TypeObject,
				Properties:           []tools.Property{{Name: "a", Schema: tools.Node{Type: tools.TypeString}}},
				AdditionalProperties: boolPtr(false),
			},
			value: `{"a":"x","z":1,"b":2}`,
			path:  "value",
			wanted: []string{
				`"value.b" is not a declared property (additionalProperties: false)`,
				`"value.z" is not a declared property (additionalProperties: false)`,
			},
		},
		"additionalProperties 写成 true 就不报": {
			schema: tools.Node{
				Type:                 tools.TypeObject,
				Properties:           []tools.Property{{Name: "a", Schema: tools.Node{Type: tools.TypeString}}},
				AdditionalProperties: boolPtr(true),
			},
			value:  `{"a":"x","z":1}`,
			path:   "value",
			wanted: nil,
		},
		"不是数组": {
			schema: tools.Node{Type: tools.TypeArray, Items: &tools.Node{Type: tools.TypeString}},
			value:  `{}`,
			path:   "value",
			wanted: []string{`"value" must be an array`},
		},
		"数组元素带下标": {
			schema: tools.Node{Type: tools.TypeArray, Items: &tools.Node{Type: tools.TypeString}},
			value:  `["a",1,"c",2]`,
			path:   "value",
			wanted: []string{`"value[1]" must be a string`, `"value[3]" must be a string`},
		},
		"不是数字": {
			schema: tools.Node{Type: tools.TypeNumber},
			value:  `"1"`,
			path:   "value",
			wanted: []string{`"value" must be a number`},
		},
		"负零不是一个排得出去又读得回来的数": {
			schema: tools.Node{Type: tools.TypeNumber},
			value:  `-0`,
			path:   "value",
			wanted: []string{`"value" must be a finite JSON number`},
		},
		"不是整数": {
			schema: tools.Node{Type: tools.TypeInteger},
			value:  `1.5`,
			path:   "value",
			wanted: []string{`"value" must be an integer`},
		},
		"整数那一路也挡负零": {
			schema: tools.Node{Type: tools.TypeInteger},
			value:  `-0`,
			path:   "value",
			wanted: []string{`"value" must be an integer`},
		},
		"不是布尔": {
			schema: tools.Node{Type: tools.TypeBoolean},
			value:  `"true"`,
			path:   "value",
			wanted: []string{`"value" must be a boolean`},
		},
		"不是 null": {
			schema: tools.Node{Type: tools.TypeNull},
			value:  `0`,
			path:   "value",
			wanted: []string{`"value" must be null`},
		},
		"enum 没命中": {
			schema: tools.Node{Type: tools.TypeString, Enum: []json.RawMessage{raw(`"a"`), raw(`"b"`)}},
			value:  `"c"`,
			path:   "value",
			wanted: []string{`"value" must be one of ["a","b"]`},
		},
		"const 没命中": {
			schema: tools.Node{Type: tools.TypeString, Const: raw(`"a"`)},
			value:  `"b"`,
			path:   "value",
			wanted: []string{`"value" must be "a"`},
		},
		"oneOf 一支也没命中": {
			schema: tools.Node{OneOf: []tools.Node{{Type: tools.TypeString}, {Type: tools.TypeNull}}},
			value:  `1`,
			path:   "value",
			wanted: []string{`"value" must match exactly one oneOf branch (matched 0)`},
		},
		"oneOf 命中了不止一支": {
			schema: tools.Node{OneOf: []tools.Node{{Type: tools.TypeString}, {Description: "任意"}}},
			value:  `"a"`,
			path:   "value",
			wanted: []string{`"value" must match exactly one oneOf branch (matched 2)`},
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			violations := tools.ValidateValue(testCase.schema, raw(testCase.value), testCase.path)
			if len(violations) != len(testCase.wanted) {
				t.Fatalf("违规条数不对：期望 %v，实际 %v", testCase.wanted, violations)
			}
			for index, wanted := range testCase.wanted {
				if violations[index] != wanted {
					t.Fatalf("第 %d 条不对：\n实际 %q\n期望 %q", index, violations[index], wanted)
				}
			}
		})
	}
}

func TestValidateValueStaysTotalOnSchemasItNeverApproved(t *testing.T) {
	t.Parallel()
	// ValidateValue 被文档承诺是全函数。下面这几份 schema 都是 AssertSupportedSchema
	// 会拒掉的，但真到了这里也只能给个答案，不能炸——两条调用路径上都有第三方代码，
	// 一份写坏的 schema 不该把整个进程带走。
	cases := map[string]struct {
		schema tools.Node
		value  string
		wanted string
	}{
		"type 不在那七个里就什么都放过": {
			schema: tools.Node{Type: "bogus"},
			value:  `{"anything":1}`,
			wanted: "",
		},
		"const 不是合法 JSON 就一定不匹配": {
			schema: tools.Node{Type: tools.TypeString, Const: raw(`{oops`)},
			value:  `"a"`,
			wanted: `"value" must be {oops`,
		},
		"enum 里有一段不合法 JSON 就跳过它": {
			schema: tools.Node{Type: tools.TypeString, Enum: []json.RawMessage{raw(`{oops`), raw(`"a"`)}},
			value:  `"b"`,
			wanted: `"value" must be one of [{oops,"a"]`,
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			violations := tools.ValidateValue(testCase.schema, raw(testCase.value), "value")
			if testCase.wanted == "" {
				if len(violations) > 0 {
					t.Fatalf("不该报违规：%v", violations)
				}
				return
			}
			if len(violations) != 1 || violations[0] != testCase.wanted {
				t.Fatalf("违规说明不对：\n实际 %v\n期望 %q", violations, testCase.wanted)
			}
		})
	}
}

func TestParseSchemaReadsTheSubsetBackOutOfJSON(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		text   string
		wanted tools.Node
	}{
		"对象带按序属性": {
			text: `{"type":"object","properties":{"b":{"type":"string"},"a":{"type":"integer"}},"required":["b"],"additionalProperties":false}`,
			wanted: tools.Node{
				Type: tools.TypeObject,
				Properties: []tools.Property{
					{Name: "b", Schema: tools.Node{Type: tools.TypeString}},
					{Name: "a", Schema: tools.Node{Type: tools.TypeInteger}},
				},
				Required:             []string{"b"},
				AdditionalProperties: boolPtr(false),
			},
		},
		"写了一个空的属性表": {
			text:   `{"type":"object","properties":{}}`,
			wanted: tools.Node{Type: tools.TypeObject, Properties: []tools.Property{}},
		},
		"数组带元素 schema": {
			text:   `{"type":"array","items":{"type":"null"}}`,
			wanted: tools.Node{Type: tools.TypeArray, Items: &tools.Node{Type: tools.TypeNull}},
		},
		"两支 oneOf": {
			text:   `{"oneOf":[{"type":"string"},{"type":"null"}]}`,
			wanted: tools.Node{OneOf: []tools.Node{{Type: tools.TypeString}, {Type: tools.TypeNull}}},
		},
		"标量带 enum 和 const": {
			text: `{"type":"string","enum":["a","b"],"const":"a"}`,
			wanted: tools.Node{
				Type:  tools.TypeString,
				Enum:  []json.RawMessage{raw(`"a"`), raw(`"b"`)},
				Const: raw(`"a"`),
			},
		},
		"只有注解": {
			text: `{"description":"任意 JSON","title":"anything","default":"x","examples":["y"]}`,
			wanted: tools.Node{
				Description: "任意 JSON",
				Title:       "anything",
				Default:     raw(`"x"`),
				Examples:    []json.RawMessage{raw(`"y"`)},
			},
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			parsed, err := tools.ParseSchema(raw(testCase.text))
			if err != nil {
				t.Fatalf("这份 schema 应该解得动：%v", err)
			}
			// 比排出去的字节而不是比结构：MarshalJSON 的键序是钉死的，
			// 所以字节相等就等于「解出来的这棵树和期望的那棵一模一样」。
			got, err := json.Marshal(parsed)
			if err != nil {
				t.Fatalf("解出来的节点应该排得出去：%v", err)
			}
			wanted, err := json.Marshal(testCase.wanted)
			if err != nil {
				t.Fatalf("期望的节点应该排得出去：%v", err)
			}
			if string(got) != string(wanted) {
				t.Fatalf("解出来的树不对：\n实际 %s\n期望 %s", got, wanted)
			}
		})
	}
}

func TestParseSchemaRejectsWhatTheStructCannotSay(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		text   string
		wanted []string
	}{
		"根本不是对象":    {text: `[1,2]`, wanted: []string{"schema must be a schema object"}},
		"不是合法 JSON": {text: `{oops`, wanted: []string{"schema must be a schema object"}},
		"尾巴上还有一份文档": {text: `{} {}`, wanted: []string{"schema must be a schema object"}},
		"键少了值":      {text: `{"type"`, wanted: []string{"schema must be a schema object"}},
		"拼错的关键字": {
			text: `{"type":"object","patternProperties":{}}`,
			wanted: []string{
				"schema.patternProperties is not a supported keyword (subset: type/oneOf/properties/required/additionalProperties/items/enum/const + annotations)",
			},
		},
		"type 写成了数组": {
			text:   `{"type":["string","null"]}`,
			wanted: []string{"schema.type must be a single type string (type arrays are not supported)"},
		},
		"type 不是字符串": {
			text:   `{"type":7}`,
			wanted: []string{"schema.type must be one of object/array/string/number/integer/boolean/null"},
		},
		"properties 不是对象": {
			text:   `{"type":"object","properties":[]}`,
			wanted: []string{"schema.properties must be an object of schemas"},
		},
		"某条属性不是 schema 对象": {
			text:   `{"type":"object","properties":{"a":3}}`,
			wanted: []string{"schema.properties.a must be a schema object"},
		},
		"required 不是字符串数组": {
			text:   `{"type":"object","required":[1]}`,
			wanted: []string{"schema.required must be an array of strings"},
		},
		"additionalProperties 不是布尔": {
			text:   `{"type":"object","additionalProperties":"no"}`,
			wanted: []string{"schema.additionalProperties must be a boolean"},
		},
		"description 不是字符串": {
			text:   `{"type":"string","description":7}`,
			wanted: []string{"schema.description must be a string"},
		},
		"title 不是字符串": {
			text:   `{"type":"string","title":7}`,
			wanted: []string{"schema.title must be a string"},
		},
		"examples 不是数组": {
			text:   `{"type":"string","examples":"y"}`,
			wanted: []string{"schema.examples annotation must be lossless JSON data"},
		},
		"items 不是 schema 对象": {
			text:   `{"type":"array","items":true}`,
			wanted: []string{"schema.items must be a schema object"},
		},
		"oneOf 里有一支不是 schema 对象": {
			text:   `{"oneOf":[{"type":"string"},5]}`,
			wanted: []string{"schema.oneOf[1] must be a schema object"},
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := tools.ParseSchema(raw(testCase.text))
			violations := violationsOf(t, err)
			if strings.Join(violations, "\n") != strings.Join(testCase.wanted, "\n") {
				t.Fatalf("违规说明不对：\n实际 %v\n期望 %v", violations, testCase.wanted)
			}
		})
	}
}

func TestParseSchemaLeavesTheSemanticJudgementToTheAsserters(t *testing.T) {
	t.Parallel()
	// 这几份都**解得动**——字节说的是什么很清楚，不成立的是它说的那件事。
	cases := map[string]struct {
		text   string
		wanted string
	}{
		"oneOf 解不成数组": {
			text:   `{"oneOf":"nope"}`,
			wanted: "schema.oneOf must be an array of at least two schemas",
		},
		"oneOf 只有一支": {
			text:   `{"oneOf":[{"type":"string"}]}`,
			wanted: "schema.oneOf must be an array of at least two schemas",
		},
		"enum 解不成数组": {
			text:   `{"type":"string","enum":"a"}`,
			wanted: "schema.enum must be a non-empty array of string values",
		},
		"const 和 type 对不上": {
			text:   `{"type":"string","const":7}`,
			wanted: "schema.const must be a string value",
		},
		"type 不在那七个里": {
			text:   `{"type":"decimal"}`,
			wanted: "schema.type must be one of object/array/string/number/integer/boolean/null",
		},
		"items 挂在了对象上": {
			text:   `{"type":"object","items":{"type":"string"}}`,
			wanted: `schema.items is not supported on type "object"`,
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			parsed, err := tools.ParseSchema(raw(testCase.text))
			if err != nil {
				t.Fatalf("这一份应该解得动，不成立的是语义：%v", err)
			}
			violations := violationsOf(t, tools.AssertSupportedSchema(parsed))
			if len(violations) != 1 || violations[0] != testCase.wanted {
				t.Fatalf("违规说明不对：\n实际 %v\n期望 %q", violations, testCase.wanted)
			}
		})
	}
}

func TestNodeUnmarshalJSONIsTheDecoderUnderAnotherName(t *testing.T) {
	t.Parallel()
	var node tools.Node
	if err := json.Unmarshal(raw(`{"type":"object","properties":{"a":{"type":"string"}}}`), &node); err != nil {
		t.Fatalf("这份 schema 应该解得动：%v", err)
	}
	if node.Type != tools.TypeObject || len(node.Properties) != 1 || node.Properties[0].Name != "a" {
		t.Fatalf("解出来的节点不对：%+v", node)
	}
	// 嵌在别的结构体里也要走同一条路。
	var wrapper struct {
		Schema tools.Node `json:"schema"`
	}
	if err := json.Unmarshal(raw(`{"schema":{"$ref":"#/x"}}`), &wrapper); err == nil {
		t.Fatal("$ref 不在这个子集里，应该在解码这一刻就被拦下来")
	} else if !strings.Contains(err.Error(), "is not a supported keyword") {
		t.Fatalf("错误措辞不对：%v", err)
	}
}
