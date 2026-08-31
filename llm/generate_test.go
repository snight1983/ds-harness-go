package llm

import (
	"encoding/json"
	"testing"
)

// TestGenerateOptionsCloneIsDeep 钉住 Clone 那四条切片／指针的分家：改到复制品上
// 的任何一笔都不许回流到原件。这是本类型交出去之后唯一的保护——[GenerateOptions]
// 会被瀑布里每一层中间件拿到手。
func TestGenerateOptionsCloneIsDeep(t *testing.T) {
	temperature := 0.7
	original := GenerateOptions{
		Provider:        "acme",
		Model:           "m-1",
		ReasoningEffort: "high",
		Messages: []Message{
			NewUserMessage(Content{TextBlock{Text: "原来那句"}}, UserSource{}),
		},
		System:      "系统提示",
		Tools:       []ToolSchema{{Name: "read", Parameters: json.RawMessage(`{}`)}},
		Temperature: &temperature,
		MaxTokens:   256,
		Stop:        []string{"\n\n"},
		SessionID:   SessionID("s-1"),
		Purpose:     PurposeCompaction,
	}

	clone := original.Clone()
	clone.Messages[0].Content[0] = TextBlock{Text: "改过的"}
	clone.Tools[0].Name = "write"
	*clone.Temperature = 0.1
	clone.Stop[0] = "STOP"

	if block := original.Messages[0].Content[0].(TextBlock); block.Text != "原来那句" {
		t.Fatalf("消息被复制品改动了，得到 %q", block.Text)
	}
	if original.Tools[0].Name != "read" {
		t.Fatalf("工具表被复制品改动了，得到 %q", original.Tools[0].Name)
	}
	if temperature != 0.7 {
		t.Fatalf("温度被复制品改动了，得到 %v", temperature)
	}
	if original.Stop[0] != "\n\n" {
		t.Fatalf("停止串被复制品改动了，得到 %q", original.Stop[0])
	}
}

// TestGenerateOptionsCloneKeepsNil 钉住 nil 复制之后还是 nil。这不是洁癖：
// [ModelInfo].InputModalities 那种「nil 表示不知道」的区分在本包不止一处，
// 一趟复制把 nil 变成空切片会让下游读出一个它其实没被告知的事实。
func TestGenerateOptionsCloneKeepsNil(t *testing.T) {
	clone := GenerateOptions{Provider: "acme"}.Clone()

	if clone.Messages != nil || clone.Tools != nil || clone.Stop != nil || clone.Temperature != nil {
		t.Fatalf("nil 字段复制之后该还是 nil，得到 %+v", clone)
	}
}

// TestGenerateOptionsCallConfig 钉住摘出来的正好是那六个字段——[PreparedCall]
// 靠这份摘录判「这份准备是不是给这次请求的」，多摘一个字段会让一次合法的准备被拒，
// 少摘一个会让一份别处的准备混进来。
func TestGenerateOptionsCallConfig(t *testing.T) {
	temperature := 0.3
	options := GenerateOptions{
		Provider:        "acme",
		Model:           "m-1",
		ReasoningEffort: "low",
		Temperature:     &temperature,
		MaxTokens:       128,
		Stop:            []string{"END"},
		// 下面这些不属于调用配置，摘录里不该出现它们的影响。
		System:    "系统提示",
		Messages:  []Message{NewUserMessage(Content{TextBlock{Text: "喂"}}, UserSource{})},
		SessionID: SessionID("s-1"),
	}

	config := options.CallConfig()
	want := CallConfig{
		Provider:        "acme",
		Model:           "m-1",
		ReasoningEffort: "low",
		Temperature:     &temperature,
		MaxTokens:       128,
		Stop:            []string{"END"},
	}
	if !CallConfigEquals(config, want) {
		t.Fatalf("摘出来的调用配置不对：%+v", config)
	}

	// 换掉任何一个属于调用配置的字段，摘录都该跟着变。
	options.Model = "m-2"
	if CallConfigEquals(options.CallConfig(), want) {
		t.Fatal("换了模型之后摘录该和原来那份不等")
	}
}
