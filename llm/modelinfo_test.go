package llm

import "testing"

// TestConfigurableProviderCloneIsDeep 钉住那条路径切片和那个三态指针都分了家。
func TestConfigurableProviderCloneIsDeep(t *testing.T) {
	declared := true
	original := ConfigurableProvider{
		Provider:     "acme",
		DisplayName:  "Acme",
		SettingsNs:   "llm",
		SettingsPath: []string{"providers", "acme"},
		Declared:     &declared,
	}

	clone := original.Clone()
	clone.SettingsPath[0] = "改过的"
	*clone.Declared = false

	if original.SettingsPath[0] != "providers" {
		t.Fatalf("设置路径被复制品改动了，得到 %q", original.SettingsPath[0])
	}
	if !declared {
		t.Fatal("Declared 被复制品改动了")
	}
	if (ConfigurableProvider{Provider: "acme"}).Clone().Declared != nil {
		t.Fatal("nil 的 Declared 复制之后该还是 nil——它是三态里的「不作这个区分」")
	}
}

// TestModelInfoCloneKeepsNilModalities 钉住模态清单那个三态里最要紧的一态：
// nil 是「不知道」，一份显式给出的清单是一条能力陈述。复制把 nil 变成空切片的话，
// [ProjectImagesForTextModel] 会读出一条它其实没被告知的「这个模型不收图」。
func TestModelInfoCloneKeepsNilModalities(t *testing.T) {
	if clone := (ModelInfo{ID: "m-1"}).Clone(); clone.InputModalities != nil {
		t.Fatalf("nil 模态清单复制之后该还是 nil，得到 %v", clone.InputModalities)
	}

	original := ModelInfo{ID: "m-1", InputModalities: []ModelModality{ModalityText}}
	clone := original.Clone()
	clone.InputModalities[0] = ModalityImage
	if original.InputModalities[0] != ModalityText {
		t.Fatalf("模态清单被复制品改动了，得到 %q", original.InputModalities[0])
	}
}

// TestModelReasoningInfoCloneIsDeep 钉住档位表分了家。
func TestModelReasoningInfoCloneIsDeep(t *testing.T) {
	original := ModelReasoningInfo{
		Efforts:       []ReasoningEffortInfo{{ID: "low", Name: "低"}},
		DefaultEffort: "low",
	}

	clone := original.Clone()
	clone.Efforts[0].Name = "改过的"

	if original.Efforts[0].Name != "低" {
		t.Fatalf("档位表被复制品改动了，得到 %q", original.Efforts[0].Name)
	}
	if (ModelReasoningInfo{}).Clone().Efforts != nil {
		t.Fatal("nil 档位表复制之后该还是 nil")
	}
}

// TestResolvedModelInfoCloneIsDeep 钉住内嵌那份 ModelInfo 也跟着走了深复制，
// 以及两个可选指针都分了家——这是本包唯一一处三层深的复制。
func TestResolvedModelInfoCloneIsDeep(t *testing.T) {
	original := ResolvedModelInfo{
		ModelInfo: ModelInfo{
			Provider:        "acme",
			ID:              "m-1",
			InputModalities: []ModelModality{ModalityText},
		},
		Context:          &ModelContext{ContextWindow: 8192},
		DefaultMaxTokens: 1024,
		Reasoning: &ModelReasoningInfo{
			Efforts: []ReasoningEffortInfo{{ID: "high", Name: "高"}},
		},
	}

	clone := original.Clone()
	clone.InputModalities[0] = ModalityImage
	clone.Context.ContextWindow = 1
	clone.Reasoning.Efforts[0].Name = "改过的"

	if original.InputModalities[0] != ModalityText {
		t.Fatalf("内嵌那份 ModelInfo 没有深复制，得到 %q", original.InputModalities[0])
	}
	if original.Context.ContextWindow != 8192 {
		t.Fatalf("上下文容量被复制品改动了，得到 %d", original.Context.ContextWindow)
	}
	if original.Reasoning.Efforts[0].Name != "高" {
		t.Fatalf("推理档位被复制品改动了，得到 %q", original.Reasoning.Efforts[0].Name)
	}

	bare := (ResolvedModelInfo{}).Clone()
	if bare.Context != nil || bare.Reasoning != nil {
		t.Fatalf("两个 nil 指针复制之后该还是 nil，得到 %+v", bare)
	}
}
