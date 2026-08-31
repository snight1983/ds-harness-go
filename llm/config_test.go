// 本文件验请求头状态：按字段的相等判断（这是「配置变没变过」的唯一判据）、
// 深复制、介质形状，以及工具 schema 逐字节穿过。
//
// 源: packages/llm/llm/src/call-config.ts:1-59
// 源: packages/llm/llm/src/types.ts:326-338

package llm

import (
	"encoding/json"
	"testing"
)

// float64Pointer 造一个指向给定温度的指针，用来表达 [CallConfig.Temperature] 的「给了」。
func float64Pointer(value float64) *float64 { return &value }

// baseConfig 是相等判断那组用例的基准，每条用例只动一个字段。
func baseConfig() CallConfig {
	return CallConfig{
		Provider:        "deepseek",
		Model:           "deepseek-chat",
		ReasoningEffort: "high",
		Temperature:     float64Pointer(0.7),
		MaxTokens:       4096,
		Stop:            []string{"\n\n", "END"},
	}
}

// TestCallConfigEqualsComparesEveryField 钉住每个字段都参与比较，逐个字段各一条。
//
// 源: packages/llm/llm/src/call-config.ts:41-59
//
// 漏掉任何一个字段的后果都是同一种：改了它却判成「没变」，于是日志里那份头快照
// 停留在旧值上，而真正发出去的请求已经换了参数——事后没人能从日志里看出这件事。
func TestCallConfigEqualsComparesEveryField(t *testing.T) {
	t.Parallel()

	for name, mutate := range map[string]func(*CallConfig){
		"提供方":     func(c *CallConfig) { c.Provider = "other" },
		"模型":      func(c *CallConfig) { c.Model = "other" },
		"推理档位":    func(c *CallConfig) { c.ReasoningEffort = "low" },
		"温度":      func(c *CallConfig) { c.Temperature = float64Pointer(0.1) },
		"输出上限":    func(c *CallConfig) { c.MaxTokens = 1 },
		"停止序列的内容": func(c *CallConfig) { c.Stop = []string{"\n\n", "OTHER"} },
		"停止序列的长度": func(c *CallConfig) { c.Stop = []string{"\n\n"} },
		"停止序列的顺序": func(c *CallConfig) { c.Stop = []string{"END", "\n\n"} },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			changed := baseConfig()
			mutate(&changed)
			if CallConfigEquals(baseConfig(), changed) {
				t.Errorf("改了%s该判成不等", name)
			}
			if !CallConfigEquals(baseConfig(), baseConfig()) {
				t.Error("没改过该判成相等")
			}
		})
	}
}

// TestCallConfigEqualsDoesNotCompareThePointersThemselves 钉住温度比的是值不是地址。
//
// 两个各自指向 0.7 的指针是不同的地址，但它们是同一份配置。直接比指针的话，
// 每次装配请求都会判成「配置变了」，每次都白写一份头快照、白丢一次提示词缓存。
func TestCallConfigEqualsDoesNotCompareThePointersThemselves(t *testing.T) {
	t.Parallel()

	left := CallConfig{Temperature: float64Pointer(0.7)}
	right := CallConfig{Temperature: float64Pointer(0.7)}
	if left.Temperature == right.Temperature {
		t.Fatal("这条用例要求两个指针是不同地址，否则它测不到想测的东西")
	}
	if !CallConfigEquals(left, right) {
		t.Error("同样的温度该判成相等")
	}
}

// TestCallConfigEqualsTellsAbsentFromZero 钉住「没给温度」和「给了 0」是两件事。
//
// 0 是一个有意义的温度（确定性采样）。把它和「没给」混为一谈，会让一次
// 「请你完全不要随机」的请求被当成「随便你」。这是本包用指针表达可选的全部理由。
func TestCallConfigEqualsTellsAbsentFromZero(t *testing.T) {
	t.Parallel()

	absent := CallConfig{}
	zero := CallConfig{Temperature: float64Pointer(0)}

	if CallConfigEquals(absent, zero) {
		t.Error("没给温度和温度为 0 该判成不等")
	}
	if CallConfigEquals(zero, absent) {
		t.Error("反过来也该判成不等")
	}
	if !CallConfigEquals(zero, CallConfig{Temperature: float64Pointer(0)}) {
		t.Error("都是 0 该判成相等")
	}
	if !CallConfigEquals(absent, CallConfig{}) {
		t.Error("都没给该判成相等")
	}
}

// TestCallConfigEqualsTellsAbsentFromAnEmptyStopList 钉住「没给停止序列」和「明确给了个空清单」不等。
//
// 源: packages/llm/llm/src/call-config.ts:56-58
//
// 这一条是 [CallConfigEquals] 里那段手写循环存在的理由：slices.Equal 把 nil 和
// 长度为零的切片看作相等，而 DSH 在 `a.stop === undefined || b.stop === undefined`
// 这一支上要求两边**同为** undefined 才算相等。一边 undefined 一边 [] 是两次不同的
// 请求头，混起来会让一次真的改动在日志里消失。
func TestCallConfigEqualsTellsAbsentFromAnEmptyStopList(t *testing.T) {
	t.Parallel()

	absent := CallConfig{}
	empty := CallConfig{Stop: []string{}}

	if CallConfigEquals(absent, empty) {
		t.Error("没给和空清单该判成不等")
	}
	if CallConfigEquals(empty, absent) {
		t.Error("反过来也该判成不等")
	}
	if !CallConfigEquals(empty, CallConfig{Stop: []string{}}) {
		t.Error("都是空清单该判成相等")
	}
	if !CallConfigEquals(absent, CallConfig{}) {
		t.Error("都没给该判成相等")
	}
}

// TestCallConfigCloneCopiesTheSlicesToo 钉住深复制真的深：改动复制件不该动到原件。
func TestCallConfigCloneCopiesTheSlicesToo(t *testing.T) {
	t.Parallel()

	t.Run("温度是复制的", func(t *testing.T) {
		t.Parallel()

		original := CallConfig{Temperature: float64Pointer(0.7)}
		cloned := original.Clone()
		if cloned.Temperature == original.Temperature {
			t.Fatal("该是另一个地址")
		}
		*cloned.Temperature = 0.1
		if *original.Temperature != 0.7 {
			t.Errorf("原件被改动了：%v", *original.Temperature)
		}
	})

	t.Run("停止序列是复制的", func(t *testing.T) {
		t.Parallel()

		original := CallConfig{Stop: []string{"a"}}
		cloned := original.Clone()
		cloned.Stop[0] = "b"
		if original.Stop[0] != "a" {
			t.Errorf("原件被改动了：%q", original.Stop[0])
		}
	})

	t.Run("没给的两个字段复制之后还是没给", func(t *testing.T) {
		t.Parallel()

		// 复制不许把「没给」变成「给了个空的」——那正是上面两条相等用例分开的东西。
		cloned := CallConfig{Provider: "p"}.Clone()
		if cloned.Temperature != nil {
			t.Errorf("温度该还是 nil，实际 %v", *cloned.Temperature)
		}
		if cloned.Stop != nil {
			t.Errorf("停止序列该还是 nil，实际 %#v", cloned.Stop)
		}
	})

}

// TestCloneKeepsAnEmptyStopListDistinctFromAnAbsentOne 钉住复制**不许**抹平
// 「明确给了一个空停止清单」和「没给停止序列」这两件事。
//
// 这两者正是 [CallConfigEquals] 花一段手写循环去分开的，而复制是它们最容易
// 被合掉的地方：`append([]string(nil), ...)` 在源切片长度为零时一个元素都不追加，
// 交出来的还是 nil。真让它合掉，后果是手上那份头被复制过一次之后，再拿它和
// 调用方提议的原件比会判成**不等**——每一轮都白写一份头快照、白丢一次提供方的
// 提示词缓存，而两份配置其实一模一样；[PreparedCall.Stream] 那道「配置必须和
// 准备时一致」的比对也会跟着误判。
func TestCloneKeepsAnEmptyStopListDistinctFromAnAbsentOne(t *testing.T) {
	t.Parallel()

	original := CallConfig{Stop: []string{}}
	cloned := original.Clone()
	if cloned.Stop == nil {
		t.Fatal("明确给的空停止清单被复制成了「没给」")
	}
	if len(cloned.Stop) != 0 {
		t.Fatalf("空停止清单复制出了内容：%#v", cloned.Stop)
	}
	if !CallConfigEquals(original, cloned) {
		t.Fatal("复制之后和原件不等了")
	}

	// 复制出来的那份要是真独立的：往它后面追加改不到原件。
	cloned.Stop = append(cloned.Stop, "STOP")
	if len(original.Stop) != 0 {
		t.Fatalf("改副本改到了原件：%#v", original.Stop)
	}
}

// TestCallConfigWireNamesFollowDSH 钉住配置在介质上的字段名。
//
// 它是**写进会话日志**的头快照。名字改一个字，一份存下来的会话就换不回同一份头。
func TestCallConfigWireNamesFollowDSH(t *testing.T) {
	t.Parallel()

	for name, expectation := range map[string]struct {
		config CallConfig
		wire   string
	}{
		"只有必填": {
			CallConfig{Provider: "p", Model: "m"},
			`{"provider":"p","model":"m"}`,
		},
		"填满": {
			CallConfig{
				Provider:        "p",
				Model:           "m",
				ReasoningEffort: "high",
				Temperature:     float64Pointer(0.7),
				MaxTokens:       100,
				Stop:            []string{"a"},
			},
			`{"provider":"p","model":"m","reasoningEffort":"high","temperature":0.7,` +
				`"maxTokens":100,"stop":["a"]}`,
		},
		"温度为零也要写出去": {
			CallConfig{Provider: "p", Model: "m", Temperature: float64Pointer(0)},
			`{"provider":"p","model":"m","temperature":0}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(expectation.config)
			if err != nil {
				t.Fatalf("排出去不该失败：%v", err)
			}
			if string(data) != expectation.wire {
				t.Errorf("介质形状变了：\n想要 %s\n实际 %s", expectation.wire, data)
			}
		})
	}
}

// TestTheWireCannotTellAnEmptyStopListFromAnAbsentOne 钉住一个**已知**的缺口。
//
// [CallConfigEquals] 分得开「没给停止序列」和「明确给了个空清单」，但介质上分不开：
// Stop 的 omitempty 会把长度为零的切片和 nil 排成同一段字节（都是没有这个键）。
// 于是一份 `stop: []` 的头快照存下来再读回来会变成 `stop: undefined`，
// 拿它和原来那份比会判成**不等**——凭空多写一份头，还白丢一次提供方的提示词缓存。
//
// 这条用例断言的是**当前的错误行为**，理由和本仓库 settings/redact_test.go 里那条
// 「已知 fail-open」用例一样：一句写在注释里的缺口，只有配一条会失败的断言才算数。
// 它一旦变红，说明缺口被补上了，这条用例连同这段注释就该删掉。
//
// 补的办法是给 CallConfig 配一对 MarshalJSON/UnmarshalJSON，Stop 在介质上用
// *[]string——nil 排成没有这个键，指向空切片排成 []，和 DSH 那边 JSON.stringify
// 一个 `stop?: string[]` 的结果逐字一致。改不改由调用方裁决，本包先把事实钉在这里。
func TestTheWireCannotTellAnEmptyStopListFromAnAbsentOne(t *testing.T) {
	t.Parallel()

	original := CallConfig{Provider: "p", Model: "m", Stop: []string{}}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("排出去不该失败：%v", err)
	}
	if string(data) != `{"provider":"p","model":"m"}` {
		t.Fatalf("缺口的形状变了，重新看这条用例：%s", data)
	}

	var back CallConfig
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("读回来不该失败：%v", err)
	}
	if back.Stop != nil {
		t.Fatalf("缺口被补上了，该删掉这条用例：%#v", back.Stop)
	}
	if CallConfigEquals(original, back) {
		t.Fatal("缺口被补上了，该删掉这条用例：往返之后又相等了")
	}
}

// TestCallConfigAdapterDefaultsWireNamesFollowDSH 钉住那两个「这个值是适配器解析出来的」标记。
//
// 源: packages/llm/llm/src/call-config.ts:32-39
//
// DSH 那边字段类型是 `?: true`，也就是「要么键在、值只能是 true，要么键不在」——
// 用一个对象表达一个集合。Go 里 bool 配 omitempty 排出来是同一段字节。
func TestCallConfigAdapterDefaultsWireNamesFollowDSH(t *testing.T) {
	t.Parallel()

	for name, expectation := range map[string]struct {
		defaults CallConfigAdapterDefaults
		wire     string
	}{
		"两个都不是":   {CallConfigAdapterDefaults{}, `{}`},
		"只有推理档位是": {CallConfigAdapterDefaults{ReasoningEffort: true}, `{"reasoningEffort":true}`},
		"只有输出上限是": {CallConfigAdapterDefaults{MaxTokens: true}, `{"maxTokens":true}`},
		"两个都是": {
			CallConfigAdapterDefaults{ReasoningEffort: true, MaxTokens: true},
			`{"reasoningEffort":true,"maxTokens":true}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(expectation.defaults)
			if err != nil {
				t.Fatalf("排出去不该失败：%v", err)
			}
			if string(data) != expectation.wire {
				t.Errorf("介质形状变了：\n想要 %s\n实际 %s", expectation.wire, data)
			}
		})
	}
}

// TestToolSchemaKeepsTheSchemaByteForByte 钉住参数 schema 原样穿过，键的顺序不变。
//
// 源: packages/llm/llm/src/types.ts:326-338
//
// 这是用 json.RawMessage 而不是 map[string]any 的全部理由：解成映射再排回去会
// 重排键的顺序，而 required 的顺序、properties 的顺序会被某些提供方原样回显，
// 也会进提示词缓存的键——顺序一变，缓存就整片失效。
func TestToolSchemaKeepsTheSchemaByteForByte(t *testing.T) {
	t.Parallel()

	// 这段 schema 的键**不是**字典序：解成 map[string]any 再排回去一定会重排它们。
	schema := `{"type":"object","required":["path","offset"],` +
		`"properties":{"path":{"type":"string"},"offset":{"type":"number"}}}`
	original := ToolSchema{
		Name:        "read",
		Description: "read a file",
		Parameters:  json.RawMessage(schema),
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("排出去不该失败：%v", err)
	}
	want := `{"name":"read","description":"read a file","parameters":` + schema + `}`
	if string(data) != want {
		t.Errorf("介质形状变了：\n想要 %s\n实际 %s", want, data)
	}

	var back ToolSchema
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("读回来不该失败：%v", err)
	}
	if string(back.Parameters) != schema {
		t.Errorf("schema 不是逐字节回来的：\n想要 %s\n实际 %s", schema, back.Parameters)
	}
}
