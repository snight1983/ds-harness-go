// 本文件的作用：把类型声明为密钥的字段从一个值里摘掉，并记下它们的位置。
//
// 源: packages/settings/settings/src/redact.ts:1-8

package settings

import (
	"reflect"
	"strconv"
	"strings"
)

// SecretTag 是把一个字段标成密钥的结构体标签。
//
// 新增: DSH 那边是 schemastery 的 `role('secret')` 元数据，跟着运行期 schema 对象走。
// Go 里描述字段的现成办法就是结构体标签，和 `json:"..."` 并排写：
//
//	type Config struct {
//	    Endpoint string `json:"endpoint"`
//	    APIKey   string `json:"apiKey" settings:"secret"`
//	}
//
// 标在**字段**上而不是标在类型上，是因为同一个 string 类型在一份配置里既可能是端点
// 也可能是密钥——密钥这件事是这个位置的性质，不是这个类型的性质。
const SecretTag = "settings"

// secretTagValue 是 [SecretTag] 上唯一有意义的取值。
const secretTagValue = "secret"

// Secret 是一份脱敏结果里的一个密钥位置。
//
// 源: packages/settings/settings/src/redact.ts:25-31
type Secret struct {
	// Path 是从段根到这个被摘掉的字段的路径，字典的键和数组的下标都在里面。
	Path []string
	// Set 说明摘掉之前这个位置**有没有**值。
	//
	// 配置界面拿它渲染一个只写输入框：既能显示「已配置」，又从来没收到过值本身。
	Set bool
}

// Redacted 是一个摘掉了全部密钥字段的值，外加那份摘除记录。
//
// 源: packages/settings/settings/src/redact.ts:33-43
type Redacted struct {
	// Value 是输入的一份副本，密钥字段在里面是缺席的。
	Value any
	// Secrets 是每一个**能走到**的密钥位置。
	//
	// 结构体字段一律列出来（哪怕没值，好让表单知道这个格子存在）；
	// 字典条目和数组元素只列值里真有的那些——它们的键和下标本来就由值决定。
	Secrets []Secret
}

// Redact 按类型 T 的声明，把一个 JSON 形状的值里每一个密钥字段摘掉。
//
// 源: packages/settings/settings/src/redact.ts:94-109
//
// 走 struct、map、slice 三种容器。密钥必须直接声明在**这三种容器走得到**的字段上；
// 藏在一个 any 字段底下的密钥走不到，也就摘不掉——所以不要那样建模。
//
// 输入不会被改动。
//
// 新增: DSH 在 default 分支留了一条 TODO，说 union / intersection / transform
// 底下的密钥会被原样返回而且没有任何记录（fail-open）。Go 侧这三种情况不存在，
// 但等价的口子是 `any` 字段：它的内容在编译期没有形状，走不进去。
// 这个口子和 DSH 那条一样是 fail-open 的，所以上面那句「不要那样建模」不是建议。
func Redact[T any](value any) Redacted {
	return redactType(reflect.TypeFor[T](), value)
}

// redactType 是 [Redact] 的非泛型内核。
//
// [Provider] 要在一张登记表上对好几个不同的 T 做同一件事，而 Go 的方法不能带类型参数——
// 泛型入口在 [Register] 里把类型固化成一个 reflect.Type 存进登记项，之后都走这里。
// 在场标记只在遍历内部有用，到了根上一定是 true：这里传进去的就是 true，
// 而 [walkRedaction] 的每一支要么把它原样传回、要么返回 true。
// 根上的「缺席」由 stripped 自己是 nil 表达，不必再看这个 bool。
func redactType(declared reflect.Type, value any) Redacted {
	secrets := []Secret{}
	stripped, _ := walkRedaction(declared, value, true, nil, &secrets)
	return Redacted{Value: stripped, Secrets: secrets}
}

// walkRedaction 是那一趟遍历。
//
// 源: packages/settings/settings/src/redact.ts:50-92
//
// 新增: DSH 用 `undefined` 同时表示「这个位置没有值」和「这个位置的值被摘掉了」，
// 靠 JS 里 undefined 不是一个合法 JSON 值来区分。Go 的 nil 是一个**合法**的 JSON 值
// （null），所以在场与否必须另用一个 bool 带着走——把它们混起来的话，
// 一个显式写成 null 的字段会和一个根本没配的字段长得一样。
func walkRedaction(declared reflect.Type, value any, present bool, path []string, secrets *[]Secret) (any, bool) {
	// 不判 nil：reflect.TypeFor 对任何 T 都给得出一个类型（接口类型也给得出），
	// 指针的 Elem 也一定非 nil，递归下去传的又都是字段 / 元素类型。
	// 加一条 nil 判断只会多一条永远走不到、也验不了的分支。
	for declared.Kind() == reflect.Pointer {
		declared = declared.Elem()
	}

	switch declared.Kind() {
	case reflect.Struct:
		return walkStruct(declared, value, present, path, secrets)

	case reflect.Map:
		// 源: packages/settings/settings/src/redact.ts:73-81
		source, isObject := value.(map[string]any)
		if !present || !isObject {
			return value, present
		}
		rebuilt := make(map[string]any, len(source))
		for key, entry := range source {
			stripped, keep := walkRedaction(declared.Elem(), entry, true, append(path[:len(path):len(path)], key), secrets)
			if keep {
				rebuilt[key] = stripped
			}
		}
		return rebuilt, true

	case reflect.Slice, reflect.Array:
		// 源: packages/settings/settings/src/redact.ts:82-85
		source, isArray := value.([]any)
		if !present || !isArray {
			return value, present
		}
		rebuilt := make([]any, len(source))
		for index, entry := range source {
			stripped, keep := walkRedaction(declared.Elem(), entry, true,
				append(path[:len(path):len(path)], strconv.Itoa(index)), secrets)
			if keep {
				rebuilt[index] = stripped
			}
		}
		return rebuilt, true

	default:
		return value, present
	}
}

// walkStruct 是结构体那一支。
//
// 源: packages/settings/settings/src/redact.ts:57-72
//
// 两条来自 DSH 的规则原样保留：
//
//  1. **值里有而类型没声明的键照样留着。** 存下来的文档可能带着一个已经删掉的旧字段，
//     或者一个更新版本才认识的新字段。摘除这一趟的职责是摘密钥，不是替类型做裁剪——
//     顺手删掉它们会让一次降级运行把用户的配置吃掉。
//  2. **值根本不是对象、而且一个字段都没重建出来时，原样返回。** 这样一个缺席的位置
//     摘完还是缺席，不会凭空变出一个空对象来盖住下层。
func walkStruct(declared reflect.Type, value any, present bool, path []string, secrets *[]Secret) (any, bool) {
	source, isObject := value.(map[string]any)
	available := present && isObject

	fields := jsonFields(declared)
	rebuilt := map[string]any{}
	if available {
		declaredKeys := make(map[string]struct{}, len(fields))
		for _, field := range fields {
			declaredKeys[field.name] = struct{}{}
		}
		for key, entry := range source {
			if _, isDeclared := declaredKeys[key]; isDeclared {
				continue
			}
			rebuilt[key] = entry
		}
	}

	for _, field := range fields {
		var entry any
		entryPresent := false
		if available {
			entry, entryPresent = source[field.name]
		}
		childPath := append(path[:len(path):len(path)], field.name)

		if field.secret {
			*secrets = append(*secrets, Secret{Path: childPath, Set: entryPresent})
			continue
		}
		stripped, keep := walkRedaction(field.declared, entry, entryPresent, childPath, secrets)
		if keep {
			rebuilt[field.name] = stripped
		}
	}

	if !available && len(rebuilt) == 0 {
		return value, present
	}
	return rebuilt, true
}

// jsonField 是一个结构体字段在 JSON 里的样子。
type jsonField struct {
	name     string
	declared reflect.Type
	secret   bool
}

// jsonFields 按 encoding/json 的规则列出一个结构体在 JSON 里有哪些键。
//
// 新增: DSH 直接读 schemastery 节点的 dict，属性名就摆在那里。Go 这边键名由
// `json:"..."` 标签决定，所以得自己解析一遍——**必须和 encoding/json 解释成同一个名字**，
// 否则脱敏走的是一套键名、编解码走的是另一套，密钥会从那道缝里漏出去。
//
// 覆盖三条规则：`json:"-"` 整个跳过；标签名为空时用字段名；未导出字段不参与。
// 匿名内嵌且没给标签名的结构体按 encoding/json 的做法**摊平**到同一层。
//
// 不覆盖的一条写在这里：encoding/json 对同名字段有一套按深度和标签的择优规则，
// 这里遇到重名时先出现的赢。设置类型里内嵌出重名字段本来就该改类型，
// 而不是靠一条谁也记不住的择优规则。
func jsonFields(declared reflect.Type) []jsonField {
	fields := make([]jsonField, 0, declared.NumField())
	seen := map[string]struct{}{}
	collectJSONFields(declared, &fields, seen)
	return fields
}

func collectJSONFields(declared reflect.Type, fields *[]jsonField, seen map[string]struct{}) {
	for index := range declared.NumField() {
		field := declared.Field(index)
		tag := field.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")

		if field.Anonymous && name == "" {
			embedded := field.Type
			for embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}
			if embedded.Kind() == reflect.Struct {
				collectJSONFields(embedded, fields, seen)
				continue
			}
		}
		if !field.IsExported() {
			continue
		}
		if name == "" {
			name = field.Name
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		*fields = append(*fields, jsonField{
			name:     name,
			declared: field.Type,
			secret:   field.Tag.Get(SecretTag) == secretTagValue,
		})
	}
}
