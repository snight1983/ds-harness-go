// 本文件验内容块那个联合：五种块的往返、不认识的块原样保管、深复制真的深、
// 以及那一趟共用的找图递归。
//
// 源: packages/llm/llm/src/types.ts:53-110
// 源: packages/llm/llm/src/content.ts:30-41

package llm

import (
	"encoding/json"
	"errors"
	"testing"

	"ds-harness-go/attachment"
)

// sampleRef 是测试里反复用的那张图，字段填满好让往返有东西可比。
func sampleRef() attachment.ImageRef {
	return attachment.ImageRef{
		ID:                 "sha256:0123456789abcdef",
		MediaType:          attachment.MediaTypePNG,
		Bytes:              1024,
		Width:              64,
		Height:             48,
		Name:               "shot.png",
		OriginalDimensions: &attachment.Dimensions{Width: 128, Height: 96},
	}
}

// TestBlockTypeIsTheTypeItself 钉住五个判别标签各归各的类型。
//
// 源: packages/llm/llm/src/types.ts:99-105
func TestBlockTypeIsTheTypeItself(t *testing.T) {
	t.Parallel()

	for want, block := range map[BlockType]ContentBlock{
		BlockText:       TextBlock{},
		BlockReasoning:  ReasoningBlock{},
		BlockImage:      ImageBlock{},
		BlockToolCall:   ToolCallBlock{},
		BlockToolResult: ToolResultBlock{},
	} {
		if got := block.BlockType(); got != want {
			t.Errorf("%#v 的标签该是 %q，实际 %q", block, want, got)
		}
	}
	if got := (UnknownBlock{Kind: "future"}).BlockType(); got != "future" {
		t.Errorf("不认识的块该自称 %q，实际 %q", "future", got)
	}
}

// TestEveryKnownBlockSurvivesTheRoundTrip 逐个钉住五种块排出去再读回来还是自己。
//
// 工具结果那一条特意套了一层嵌套内容，因为它是唯一一个递归的块：
// 递归那一层要是没接上，往返会安静地把里面的东西丢掉。
func TestEveryKnownBlockSurvivesTheRoundTrip(t *testing.T) {
	t.Parallel()

	for name, original := range map[string]ContentBlock{
		"文本":      TextBlock{Text: "hello"},
		"空文本":     TextBlock{},
		"推理":      ReasoningBlock{Text: "thinking"},
		"图片":      ImageBlock{Attachment: sampleRef()},
		"工具调用":    ToolCallBlock{ID: "call-1", Name: "read", Arguments: `{"path":"a"}`},
		"工具结果":    ToolResultBlock{ToolCallID: "call-1", Content: Content{TextBlock{Text: "ok"}}},
		"工具结果出错":  ToolResultBlock{ToolCallID: "call-2", Content: Content{ImageBlock{Attachment: sampleRef()}}, IsError: true},
		"工具结果空内容": ToolResultBlock{ToolCallID: "call-3"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(original)
			if err != nil {
				t.Fatalf("排出去不该失败：%v", err)
			}
			back, err := UnmarshalContentBlock(data)
			if err != nil {
				t.Fatalf("读回来不该失败：%v", err)
			}
			again, err := json.Marshal(back)
			if err != nil {
				t.Fatalf("再排一次不该失败：%v", err)
			}
			if string(again) != string(data) {
				t.Errorf("往返不闭合：\n第一次 %s\n第二次 %s", data, again)
			}
		})
	}
}

// TestWireNamesFollowDSH 钉住介质上的字段名。
//
// 这一条看着像在测 encoding/json，实际测的是别的：这些名字是**两侧共用的**，
// 改一个字都会让 DSH 写下的会话日志在这里读不通。所以它们必须被一条会失败的
// 断言按住，而不是靠谁记得。
func TestWireNamesFollowDSH(t *testing.T) {
	t.Parallel()

	for name, expectation := range map[string]struct {
		block ContentBlock
		wire  string
	}{
		"文本": {TextBlock{Text: "hi"}, `{"type":"text","text":"hi"}`},
		"推理": {ReasoningBlock{Text: "hi"}, `{"type":"reasoning","text":"hi"}`},
		"工具调用": {
			ToolCallBlock{ID: "c", Name: "n", Arguments: "{}"},
			`{"type":"tool-call","id":"c","name":"n","arguments":"{}"}`,
		},
		"工具结果": {
			ToolResultBlock{ToolCallID: "c", Content: Content{}},
			`{"type":"tool-result","toolCallId":"c","content":[]}`,
		},
		"工具结果出错": {
			ToolResultBlock{ToolCallID: "c", Content: Content{}, IsError: true},
			`{"type":"tool-result","toolCallId":"c","content":[],"isError":true}`,
		},
		"图片": {
			ImageBlock{Attachment: attachment.ImageRef{ID: "sha256:aa", MediaType: attachment.MediaTypePNG, Bytes: 3, Width: 1, Height: 2}},
			`{"type":"image","attachment":{"attachmentId":"sha256:aa","mediaType":"image/png","bytes":3,"width":1,"height":2}}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(expectation.block)
			if err != nil {
				t.Fatalf("排出去不该失败：%v", err)
			}
			if string(data) != expectation.wire {
				t.Errorf("介质形状变了：\n想要 %s\n实际 %s", expectation.wire, data)
			}
		})
	}
}

// TestAnUnknownBlockIsKeptVerbatim 钉住本构建不认识的块读得回来、排得出去，且逐字节不变。
//
// 这是整个「不封读的一侧」决定的全部意义：一个旧构建读一份新构建写的会话日志、
// 再写回去，不许把读不懂的东西抹掉。
func TestAnUnknownBlockIsKeptVerbatim(t *testing.T) {
	t.Parallel()

	raw := `{"type":"video","clipId":"v-1","frames":[1,2,3]}`
	block, err := UnmarshalContentBlock([]byte(raw))
	if err != nil {
		t.Fatalf("不认识的块不该报错：%v", err)
	}
	unknown, ok := block.(UnknownBlock)
	if !ok {
		t.Fatalf("该收进 UnknownBlock，实际 %#v", block)
	}
	if unknown.Kind != "video" {
		t.Errorf("该自称 video，实际 %q", unknown.Kind)
	}
	data, err := json.Marshal(unknown)
	if err != nil {
		t.Fatalf("排出去不该失败：%v", err)
	}
	if string(data) != raw {
		t.Errorf("不是逐字节吐回：\n想要 %s\n实际 %s", raw, data)
	}
}

// TestAnUnknownBlockWithoutRawBytesRefusesToMarshal 钉住自己造出来的空 [UnknownBlock] 当场报错。
//
// 它只可能是代码造的，不可能是读出来的。悄悄写成 null 会在日志里留下一个下次读回来
// 是空的块，那是一次静默的丢失。
func TestAnUnknownBlockWithoutRawBytesRefusesToMarshal(t *testing.T) {
	t.Parallel()

	if _, err := json.Marshal(UnknownBlock{Kind: "video"}); !errorIsMalformed(err) {
		t.Errorf("该报 ErrMalformedValue，实际 %v", err)
	}
	if _, err := json.Marshal(UnknownBlock{Kind: "video", Raw: []byte("{oops")}); !errorIsMalformed(err) {
		t.Errorf("原始字节不是合法 JSON 时该报 ErrMalformedValue，实际 %v", err)
	}
}

// errorIsMalformed 判断一个错误链里有没有 [ErrMalformedValue]。
//
// encoding/json 会把 MarshalJSON 里返回的错误包一层自己的类型，所以不能直接比。
func errorIsMalformed(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrMalformedValue) {
		return true
	}
	var marshalErr *json.MarshalerError
	if errors.As(err, &marshalErr) {
		return errors.Is(marshalErr.Err, ErrMalformedValue)
	}
	return false
}

// TestMalformedBytesAreRefused 钉住排不成形状的字节报 [ErrMalformedValue]，逐种形状各一条。
func TestMalformedBytesAreRefused(t *testing.T) {
	t.Parallel()

	for name, data := range map[string]string{
		"根本不是 JSON":    `{`,
		"标签不是字符串":      `{"type":7}`,
		"文本块的文本不是字符串":  `{"type":"text","text":7}`,
		"推理块的文本不是字符串":  `{"type":"reasoning","text":7}`,
		"图片块的附件不是对象":   `{"type":"image","attachment":7}`,
		"工具调用的参数不是字符串": `{"type":"tool-call","arguments":7}`,
		"工具结果的内容不是数组":  `{"type":"tool-result","content":7}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := UnmarshalContentBlock([]byte(data)); !errors.Is(err, ErrMalformedValue) {
				t.Errorf("该报 ErrMalformedValue，实际 %v", err)
			}
		})
	}
}

// TestContentRoundTripsAsAList 钉住整串内容的往返，包括 null 读回 nil 那一条。
//
// null 那一条是往返闭合的条件：一条内容为 nil 的消息排出去是 null，读回来还得是
// nil，而不是一个长度为零的切片——否则同一条消息排两次得到两份不同的字节。
func TestContentRoundTripsAsAList(t *testing.T) {
	t.Parallel()

	t.Run("null 读回 nil", func(t *testing.T) {
		t.Parallel()

		var content Content
		if err := json.Unmarshal([]byte(`null`), &content); err != nil {
			t.Fatalf("读回来不该失败：%v", err)
		}
		if content != nil {
			t.Errorf("该是 nil，实际 %#v", content)
		}
	})

	t.Run("空数组读回空切片", func(t *testing.T) {
		t.Parallel()

		var content Content
		if err := json.Unmarshal([]byte(`[]`), &content); err != nil {
			t.Fatalf("读回来不该失败：%v", err)
		}
		if content == nil || len(content) != 0 {
			t.Errorf("该是长度为零的非 nil 切片，实际 %#v", content)
		}
	})

	t.Run("多块往返", func(t *testing.T) {
		t.Parallel()

		original := Content{
			TextBlock{Text: "a"},
			ToolResultBlock{ToolCallID: "c", Content: Content{ImageBlock{Attachment: sampleRef()}}},
		}
		data, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("排出去不该失败：%v", err)
		}
		var back Content
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatalf("读回来不该失败：%v", err)
		}
		again, err := json.Marshal(back)
		if err != nil {
			t.Fatalf("再排一次不该失败：%v", err)
		}
		if string(again) != string(data) {
			t.Errorf("往返不闭合：\n第一次 %s\n第二次 %s", data, again)
		}
	})

	t.Run("不是数组", func(t *testing.T) {
		t.Parallel()

		var content Content
		if err := json.Unmarshal([]byte(`7`), &content); !errors.Is(err, ErrMalformedValue) {
			t.Errorf("该报 ErrMalformedValue，实际 %v", err)
		}
	})

	t.Run("里面有读不回来的块", func(t *testing.T) {
		t.Parallel()

		var content Content
		if err := json.Unmarshal([]byte(`[{"type":"text","text":7}]`), &content); !errors.Is(err, ErrMalformedValue) {
			t.Errorf("该报 ErrMalformedValue，实际 %v", err)
		}
	})
}

// TestCloneCopiesTheSlicesToo 钉住深复制真的深：改动复制件不该动到原件。
//
// Go 的结构体赋值是复制，但里面的切片不是——这一条压的正是那个差别。
func TestCloneCopiesTheSlicesToo(t *testing.T) {
	t.Parallel()

	t.Run("nil 复制成 nil", func(t *testing.T) {
		t.Parallel()

		if got := Content(nil).Clone(); got != nil {
			t.Errorf("nil 该复制成 nil，实际 %#v", got)
		}
	})

	t.Run("工具结果的嵌套内容是复制的", func(t *testing.T) {
		t.Parallel()

		original := Content{ToolResultBlock{Content: Content{TextBlock{Text: "before"}}}}
		cloned := original.Clone()
		cloned[0].(ToolResultBlock).Content[0] = TextBlock{Text: "after"}

		if got := original[0].(ToolResultBlock).Content[0].(TextBlock).Text; got != "before" {
			t.Errorf("原件被改动了：%q", got)
		}
	})

	t.Run("不认识的块的原始字节是复制的", func(t *testing.T) {
		t.Parallel()

		original := Content{UnknownBlock{Kind: "x", Raw: []byte(`{"type":"x"}`)}}
		cloned := original.Clone()
		cloned[0].(UnknownBlock).Raw[0] = 'X'

		if got := original[0].(UnknownBlock).Raw[0]; got != '{' {
			t.Errorf("原件被改动了：%q", got)
		}
	})

	t.Run("没有引用类型的块值复制就够", func(t *testing.T) {
		t.Parallel()

		original := Content{TextBlock{Text: "a"}, ImageBlock{Attachment: sampleRef()}}
		cloned := original.Clone()
		if len(cloned) != len(original) {
			t.Fatalf("长度变了：%d", len(cloned))
		}
		for index := range original {
			if cloned[index] != original[index] {
				t.Errorf("第 %d 块变了：%#v", index, cloned[index])
			}
		}
	})
}

// TestContentHasImageWalksNestedResults 钉住那一趟共用的找图递归会走进工具结果。
//
// 它是所有图片策略（能力判定、纯文本序列化、压缩清点）共用的同一趟，
// 所以「嵌套多深算数」这件事只在这里定一次。
func TestContentHasImageWalksNestedResults(t *testing.T) {
	t.Parallel()

	for name, expectation := range map[string]struct {
		content Content
		want    bool
	}{
		"空内容":     {nil, false},
		"只有文本":    {Content{TextBlock{Text: "a"}}, false},
		"顶层有图":    {Content{ImageBlock{Attachment: sampleRef()}}, true},
		"工具结果里有图": {Content{ToolResultBlock{Content: Content{ImageBlock{Attachment: sampleRef()}}}}, true},
		"工具结果套两层有图": {
			Content{ToolResultBlock{Content: Content{ToolResultBlock{Content: Content{ImageBlock{Attachment: sampleRef()}}}}}},
			true,
		},
		"工具结果里没图": {Content{ToolResultBlock{Content: Content{TextBlock{Text: "a"}}}}, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := ContentHasImage(expectation.content); got != expectation.want {
				t.Errorf("该是 %v，实际 %v", expectation.want, got)
			}
		})
	}
}
