// 本文件验模型清单那一层：一条路由写下来的那张表怎么落成真正拿去发请求的模型
// 记录，以及推理档位那套「没声明就是不提供」的规矩。

package openaicompat

import (
	"slices"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/llm"
)

// withModels 造一条只换了模型清单的最小路由。
func withModels(models ...ModelProfile) ProviderProfile {
	profile := minimalProfile()
	profile.Models = models
	return profile
}

// TestResolveRouteModelsKeepsWrittenOrder 验模型按配置里的书写次序排着。
//
// 这是清单里唯一一处**不**排序的次序，而它是有意的：模型选择器上的顺序就是部署方
// 写下来的顺序，按字典序重排会把「常用的放前面」这件事悄悄毁掉。
func TestResolveRouteModelsKeepsWrittenOrder(t *testing.T) {
	resolved := resolveOne(t, withModels(
		ModelProfile{ID: "zeta"}, ModelProfile{ID: "alpha"}, ModelProfile{ID: "mid"}))
	var ids []string
	for _, model := range resolved.Models {
		ids = append(ids, model.ID)
	}
	if want := []string{"zeta", "alpha", "mid"}; !slices.Equal(ids, want) {
		t.Errorf("模型次序被重排了：%v", ids)
	}
}

// TestResolveRouteModelsRejections 逐条验模型清单里哪些写法服务不了。
func TestResolveRouteModelsRejections(t *testing.T) {
	cases := []struct {
		name   string
		models []ModelProfile
		wants  string
	}{
		{name: "一条模型都没有", models: nil, wants: "没有列出任何模型"},
		{name: "模型没有 id", models: []ModelProfile{{}}, wants: "没有 id"},
		{
			name:   "同一个 id 列了两次",
			models: []ModelProfile{{ID: "m"}, {ID: "m"}},
			wants:  "列了不止一次",
		},
		{
			name:   "模型容量为负",
			models: []ModelProfile{{ID: "m", ContextWindow: -1}},
			wants:  "contextWindow",
		},
		{
			name:   "模型输出上限为负",
			models: []ModelProfile{{ID: "m", MaxTokens: -1}},
			wants:  "maxTokens",
		},
		{
			name:   "推理档位声明是空的",
			models: []ModelProfile{{ID: "m", ReasoningEfforts: map[llm.ReasoningEffortID]string{}}},
			wants:  "reasoningEfforts 是空的",
		},
		{
			name:   "不认得的推理档位",
			models: []ModelProfile{{ID: "m", ReasoningEfforts: map[llm.ReasoningEffortID]string{"higth": "x"}}},
			wants:  "不认得的档位",
		},
		{
			name: "档位没给线上拼法",
			models: []ModelProfile{{ID: "m", ReasoningEfforts: map[llm.ReasoningEffortID]string{
				ThinkingHigh: "",
			}}},
			wants: "没有给出线上拼法",
		},
		{
			name: "只声明了 off 一档",
			models: []ModelProfile{{ID: "m", ReasoningEfforts: map[llm.ReasoningEffortID]string{
				ThinkingOff: "",
			}}},
			wants: "没有别的档位",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			rejects(t, map[string]ProviderProfile{"acme": withModels(testCase.models...)}, testCase.wants)
		})
	}
}

// TestResolveModelReasoningUsesCanonicalOrder 验落定出来的档位按规范次序排着，
// 而不是按 map 的遍历次序。
//
// Go 的 map 无序，不钉死这个次序的话，同一份配置每次解算给出的档位清单都可能不同
// ——选择器上的排序、诊断里的枚举全都会跟着抖。
func TestResolveModelReasoningUsesCanonicalOrder(t *testing.T) {
	resolved := resolveOne(t, withModels(ModelProfile{
		ID: "m",
		ReasoningEfforts: map[llm.ReasoningEffortID]string{
			ThinkingMax: "ultra", ThinkingLow: "low", ThinkingOff: "", ThinkingHigh: "high",
		},
	}))
	want := []ReasoningEffort{
		{ID: ThinkingOff, Wire: ""},
		{ID: ThinkingLow, Wire: "low"},
		{ID: ThinkingHigh, Wire: "high"},
		{ID: ThinkingMax, Wire: "ultra"},
	}
	if got := resolved.Models[0].ReasoningEfforts; !slices.Equal(got, want) {
		t.Errorf("档位次序或拼法不对：%v", got)
	}
	// 跑很多遍，好让 map 的遍历随机性真的有机会露出来。
	for range 20 {
		again := resolveOne(t, withModels(ModelProfile{
			ID: "m",
			ReasoningEfforts: map[llm.ReasoningEffortID]string{
				ThinkingMax: "ultra", ThinkingLow: "low", ThinkingOff: "", ThinkingHigh: "high",
			},
		}))
		if !slices.Equal(again.Models[0].ReasoningEfforts, want) {
			t.Fatalf("同一份配置解算两次给出了不同的档位清单：%v", again.Models[0].ReasoningEfforts)
		}
	}
}

// TestThinkingLevelsIsOwned 验 [ThinkingLevels] 交出来的是调用方自己的一份。
//
// 它是包级那张表，被诊断文案和上面那次排序共用；交出内部切片的话，一个随手排了个
// 序的调用方会把这个包的规范次序改掉。
func TestThinkingLevelsIsOwned(t *testing.T) {
	levels := ThinkingLevels()
	if len(levels) == 0 {
		t.Fatal("档位表是空的")
	}
	levels[0] = "tampered"
	if thinkingLevels[0] == "tampered" {
		t.Error("ThinkingLevels 把内部那张表原样交了出去")
	}
	if !isThinkingLevel(ThinkingXHigh) || isThinkingLevel("higth") {
		t.Error("档位认定不对")
	}
}

// TestResolvedModelHelpers 验落定模型那几个查询方法。
func TestResolvedModelHelpers(t *testing.T) {
	profile := minimalProfile()
	profile.Models = []ModelProfile{{
		ID:               "m",
		Input:            []llm.ModelModality{llm.ModalityText, llm.ModalityImage},
		ReasoningEfforts: map[llm.ReasoningEffortID]string{ThinkingHigh: "high"},
	}}
	model := resolveOne(t, profile).Models[0]

	if !model.SupportsImages() {
		t.Error("声明了 image 模态的模型该收图")
	}
	if effort, offered := model.Effort(ThinkingHigh); !offered || effort.Wire != "high" {
		t.Errorf("按档位取拼法不对：%+v %v", effort, offered)
	}
	if _, offered := model.Effort(ThinkingLow); offered {
		t.Error("没声明的档位不该被认成提供")
	}

	// Clone 是给要改它的调用方用的，两个切片都得是新的。
	clone := model.Clone()
	clone.Input[0] = llm.ModalityImage
	clone.ReasoningEfforts[0] = ReasoningEffort{ID: ThinkingLow}
	if model.Input[0] != llm.ModalityText || model.ReasoningEfforts[0].ID != ThinkingHigh {
		t.Error("Clone 出来的模型和原本共享着切片")
	}
}

// TestResolveRouteModelsInheritsRouteDefaults 验模型没写的那几项兜到路由的默认值上。
func TestResolveRouteModelsInheritsRouteDefaults(t *testing.T) {
	profile := minimalProfile()
	profile.DefaultContextWindow = 4096
	profile.DefaultMaxTokens = 512
	profile.DefaultInput = []llm.ModelModality{llm.ModalityText, llm.ModalityImage}
	profile.Models = []ModelProfile{
		{ID: "inherits"},
		{ID: "overrides", ContextWindow: 8192, MaxTokens: 1024, Input: []llm.ModelModality{llm.ModalityText}},
	}
	resolved := resolveOne(t, profile)

	inherits := resolved.Models[0]
	if inherits.ContextWindow != 4096 || inherits.MaxTokens != 512 {
		t.Errorf("没写容量的模型没兜到路由默认值：%d/%d", inherits.ContextWindow, inherits.MaxTokens)
	}
	if !inherits.SupportsImages() {
		t.Error("没写模态的模型没兜到路由的 defaultInput")
	}
	overrides := resolved.Models[1]
	if overrides.ContextWindow != 8192 || overrides.MaxTokens != 1024 {
		t.Errorf("写下来的容量没落下来：%d/%d", overrides.ContextWindow, overrides.MaxTokens)
	}
	if overrides.SupportsImages() {
		t.Error("写下来的模态没盖住路由默认值")
	}
}

// TestResolveRouteModelsOwnsModalitySlices 验每条模型的模态清单都是自己的一份。
//
// 兜底那一支尤其要紧：所有兜底的模型共用同一个 defaultInput 切片的话，一个改了
// 某条模型模态的调用方会把这条路由上所有兜底的模型一起改掉。
func TestResolveRouteModelsOwnsModalitySlices(t *testing.T) {
	profile := minimalProfile()
	profile.DefaultInput = []llm.ModelModality{llm.ModalityText}
	profile.Models = []ModelProfile{{ID: "a"}, {ID: "b"}}
	resolved := resolveOne(t, profile)

	resolved.Models[0].Input[0] = llm.ModalityImage
	if resolved.Models[1].Input[0] != llm.ModalityText {
		t.Error("两条兜底的模型共用了同一个模态切片")
	}
	if !slices.Equal(profile.DefaultInput, []llm.ModelModality{llm.ModalityText}) {
		t.Error("落定结果和配置里那份 defaultInput 是同一个切片")
	}
}

// TestInvalidRouteNamesTheRoute 验每一条清单层的诊断都点得出是哪条路由。
//
// 一份几十行的 providers 表里，说不清是谁出事的诊断等于没说。
func TestInvalidRouteNamesTheRoute(t *testing.T) {
	err := invalidRoute("acme", "出了点事")
	if !strings.Contains(err.Error(), `"acme"`) {
		t.Errorf("诊断没有点名路由：%v", err)
	}
}
