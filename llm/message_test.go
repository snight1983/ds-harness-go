// 本文件验消息这个值：四种来源和六种上下文形态的往返、两个构造函数钉死的收窄、
// 两个读取面把那个收窄取回来、以及一行陈述按字收。
//
// 源: packages/llm/llm/src/message.ts:1-241

package llm

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestSourceKindIsTheTypeItself 钉住四个来源标签各归各的类型。
//
// 源: packages/llm/llm/src/message.ts:100-105
func TestSourceKindIsTheTypeItself(t *testing.T) {
	t.Parallel()

	for want, source := range map[SourceKind]MessageSource{
		SourceUser:   UserSource{},
		SourcePlugin: PluginSource{},
		SourceModel:  ModelSource{},
		SourceTool:   ToolSource{},
	} {
		if got := source.SourceKind(); got != want {
			t.Errorf("%#v 的标签该是 %q，实际 %q", source, want, got)
		}
	}
	if got := (UnknownSource{Kind: "daemon"}).SourceKind(); got != "daemon" {
		t.Errorf("不认识的来源该自称 %q，实际 %q", "daemon", got)
	}
}

// TestContextFormIsTheTypeItself 钉住六个形态标签各归各的类型。
//
// 源: packages/llm/llm/src/message.ts:32-60
func TestContextFormIsTheTypeItself(t *testing.T) {
	t.Parallel()

	for want, context := range map[ContextForm]Context{
		FormInstructions: InstructionsContext{},
		FormCatalog:      CatalogContext{},
		FormSnapshot:     SnapshotContext{},
		FormNotice:       NoticeContext{},
		FormRelay:        RelayContext{},
		FormRecall:       RecallContext{},
	} {
		if got := context.ContextForm(); got != want {
			t.Errorf("%#v 的形态该是 %q，实际 %q", context, want, got)
		}
	}
	if got := (UnknownContext{Form: "diff"}).ContextForm(); got != "diff" {
		t.Errorf("不认识的形态该自称 %q，实际 %q", "diff", got)
	}
}

// TestEverySourceSurvivesTheRoundTrip 逐个钉住四种来源排出去再读回来还是自己。
//
// 插件来源的六种形态各占一条：形态是**摊平**在同一个对象里的（照 DSH 的交叉类型），
// 摊平那一步要是漏了哪个字段，往返会安静地把它丢掉。
func TestEverySourceSurvivesTheRoundTrip(t *testing.T) {
	t.Parallel()

	for name, original := range map[string]MessageSource{
		"用户":              UserSource{},
		"模型":              ModelSource{Provenance: Provenance{Provider: "deepseek", Model: "chat"}},
		"模型带重放状态":         ModelSource{Provenance: Provenance{Provider: "p", Model: "m", ReplayState: json.RawMessage(`{"id":1}`)}},
		"工具":              ToolSource{CallID: "call-1"},
		"插件没形态":           PluginSource{Plugin: "memory"},
		"插件 instructions": PluginSource{Plugin: "memory", Context: InstructionsContext{}},
		"插件 catalog":      PluginSource{Plugin: "skills", Context: CatalogContext{}},
		"插件 snapshot": PluginSource{Plugin: "workspace", Context: SnapshotContext{
			Sections: []ContextSnapshotSection{{Name: "git", Text: "clean"}},
		}},
		"插件 notice": PluginSource{Plugin: "loop", Context: NoticeContext{Summary: "工具已更新"}},
		"插件 relay":  PluginSource{Plugin: "swarm", Context: RelayContext{}},
		"插件 recall": PluginSource{Plugin: "recall", Context: RecallContext{}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(original)
			if err != nil {
				t.Fatalf("排出去不该失败：%v", err)
			}
			back, err := UnmarshalMessageSource(data)
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

// TestSourceWireNamesFollowDSH 钉住来源在介质上的字段名，理由同 [TestWireNamesFollowDSH]。
func TestSourceWireNamesFollowDSH(t *testing.T) {
	t.Parallel()

	for name, expectation := range map[string]struct {
		source MessageSource
		wire   string
	}{
		"用户": {UserSource{}, `{"kind":"user"}`},
		"工具": {ToolSource{CallID: "c"}, `{"kind":"tool","callId":"c"}`},
		"模型": {
			ModelSource{Provenance: Provenance{Provider: "p", Model: "m"}},
			`{"kind":"model","provider":"p","model":"m"}`,
		},
		"插件摊平了形态": {
			PluginSource{Plugin: "loop", Context: NoticeContext{Summary: "s"}},
			`{"kind":"plugin","plugin":"loop","form":"notice","summary":"s"}`,
		},
		"插件的快照分节": {
			PluginSource{Plugin: "w", Context: SnapshotContext{Sections: []ContextSnapshotSection{{Name: "n", Text: "t"}}}},
			`{"kind":"plugin","plugin":"w","form":"snapshot","sections":[{"name":"n","text":"t"}]}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(expectation.source)
			if err != nil {
				t.Fatalf("排出去不该失败：%v", err)
			}
			if string(data) != expectation.wire {
				t.Errorf("介质形状变了：\n想要 %s\n实际 %s", expectation.wire, data)
			}
		})
	}
}

// TestAnUnknownSourceIsKeptVerbatim 钉住不认识的来源逐字节保管，理由同内容块那一条。
func TestAnUnknownSourceIsKeptVerbatim(t *testing.T) {
	t.Parallel()

	raw := `{"kind":"daemon","daemonId":"d-1"}`
	source, err := UnmarshalMessageSource([]byte(raw))
	if err != nil {
		t.Fatalf("不认识的来源不该报错：%v", err)
	}
	unknown, ok := source.(UnknownSource)
	if !ok {
		t.Fatalf("该收进 UnknownSource，实际 %#v", source)
	}
	data, err := json.Marshal(unknown)
	if err != nil {
		t.Fatalf("排出去不该失败：%v", err)
	}
	if string(data) != raw {
		t.Errorf("不是逐字节吐回：\n想要 %s\n实际 %s", raw, data)
	}
	if _, err := json.Marshal(UnknownSource{Kind: "daemon"}); !errorIsMalformed(err) {
		t.Errorf("没有原始字节时该报 ErrMalformedValue，实际 %v", err)
	}
}

// TestAnUnknownContextKeepsOnlyItsForm 钉住不认识的**形态**只在 Context 上留名字，
// 载荷则原样落进 Extra，抄写一遍日志不掉字节。
//
// [UnknownContext] 自己不带载荷，压的是那段注释里写的判断：形态的载荷是呈现用的
// 元数据，消费方 switch 到 default 那一支本来就不读它。但介质上形态的字段和注入方
// 自己的字段摊在同一个对象里，分不出来，所以它们一起被保住——面向模型的内容不受
// 影响，那全在 Content 里。
func TestAnUnknownContextKeepsOnlyItsForm(t *testing.T) {
	t.Parallel()

	source, err := UnmarshalMessageSource([]byte(`{"kind":"plugin","plugin":"p","form":"diff","hunks":3}`))
	if err != nil {
		t.Fatalf("不认识的形态不该报错：%v", err)
	}
	plugin, ok := source.(PluginSource)
	if !ok {
		t.Fatalf("该是 PluginSource，实际 %#v", source)
	}
	unknown, ok := plugin.Context.(UnknownContext)
	if !ok {
		t.Fatalf("形态该收进 UnknownContext，实际 %#v", plugin.Context)
	}
	if unknown.Form != "diff" {
		t.Errorf("该自称 diff，实际 %q", unknown.Form)
	}

	if string(plugin.Extra) != `{"hunks":3}` {
		t.Errorf("形态的载荷该原样落进 Extra，实际 %s", plugin.Extra)
	}

	data, err := json.Marshal(plugin)
	if err != nil {
		t.Fatalf("排出去不该失败：%v", err)
	}
	if string(data) != `{"form":"diff","hunks":3,"kind":"plugin","plugin":"p"}` {
		t.Errorf("排回去该一个字节不差，实际 %s", data)
	}
}

// TestAnAbsentFormIsNotAnError 钉住插件来源没声明形态时给 nil，那是有文档的默认。
//
// 源: packages/llm/llm/src/message.ts:44-46
func TestAnAbsentFormIsNotAnError(t *testing.T) {
	t.Parallel()

	source, err := UnmarshalMessageSource([]byte(`{"kind":"plugin","plugin":"p"}`))
	if err != nil {
		t.Fatalf("没声明形态不该报错：%v", err)
	}
	if plugin := source.(PluginSource); plugin.Context != nil {
		t.Errorf("形态该是 nil，实际 %#v", plugin.Context)
	}
}

// TestAPluginSourceHasNoExtraByDefault 钉住只有本包认识的键时 Extra 是 nil，
// 而不是一个空对象——空对象排出去会多一次无谓的解码，也让「有没有额外字段」
// 这个判断多一种写法。
func TestAPluginSourceHasNoExtraByDefault(t *testing.T) {
	t.Parallel()

	source, err := UnmarshalMessageSource(
		[]byte(`{"kind":"plugin","plugin":"p","form":"snapshot","sections":[]}`))
	if err != nil {
		t.Fatalf("这段字节该读得回来：%v", err)
	}
	if extra := source.(PluginSource).Extra; extra != nil {
		t.Errorf("Extra 该是 nil，实际 %s", extra)
	}
}

// TestPluginExtraSurvivesARoundTrip 钉住注入方自己挂的持久字段读得回来也排得回去。
//
// 这一条是压缩检查点赖以工作的东西：它的 compactionId 就挂在这里，
// 一次读出来再写回去把它丢掉，日志上就再也说不清哪条检查点属于哪次压缩。
func TestPluginExtraSurvivesARoundTrip(t *testing.T) {
	t.Parallel()

	const wire = `{"compactionId":"c-1","kind":"plugin","plugin":"compact"}`

	source, err := UnmarshalMessageSource([]byte(wire))
	if err != nil {
		t.Fatalf("这段字节该读得回来：%v", err)
	}
	plugin, ok := source.(PluginSource)
	if !ok {
		t.Fatalf("该是 PluginSource，实际 %#v", source)
	}
	if string(plugin.Extra) != `{"compactionId":"c-1"}` {
		t.Fatalf("额外字段该原样收起来，实际 %s", plugin.Extra)
	}

	data, err := json.Marshal(plugin)
	if err != nil {
		t.Fatalf("排出去不该失败：%v", err)
	}
	if string(data) != wire {
		t.Errorf("排回去该一个字节不差，实际 %s", data)
	}
}

// TestPluginExtraMustBeAnObject 钉住 Extra 不是对象时当场报错。
func TestPluginExtraMustBeAnObject(t *testing.T) {
	t.Parallel()

	_, err := json.Marshal(PluginSource{Plugin: "p", Extra: json.RawMessage(`["a"]`)})
	if !errors.Is(err, ErrMalformedValue) {
		t.Fatalf("该报 ErrMalformedValue，实际 %v", err)
	}
}

// TestPluginExtraCannotShadowKnownKeys 钉住额外字段不许和本包自己的键撞车。
//
// 撞了还照排的话，那个对象上会有两个同名键，读回来是哪一个取决于解码方——
// 一条来源会因为读它的人不同而变成两种东西。
func TestPluginExtraCannotShadowKnownKeys(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"kind", "plugin", "form", "sections", "summary"} {
		_, err := json.Marshal(PluginSource{
			Plugin: "p",
			Extra:  json.RawMessage(`{"` + name + `":"x"}`),
		})
		if !errors.Is(err, ErrMalformedValue) {
			t.Errorf("额外字段叫 %q 时该报 ErrMalformedValue，实际 %v", name, err)
		}
	}
}

// TestPluginExtraHelpersRefuseNonObjects 直接钉住那两个内部帮手的第二道防线：
// 交给它们的**底稿**不是一个 JSON 对象时报错，而不是把额外字段悄悄丢掉。
//
// 走公开的那条路到不了这两行：[PluginSource.MarshalJSON] 交给 mergePluginExtra 的
// 底稿是本包自己刚排出来的那个对象，[UnmarshalMessageSource] 交给 pluginExtraOf 的
// 字节刚刚成功解进过一个结构体。所以这两行拦的是**本包自己以后写错**——比如有人
// 把这两个帮手挪去处理一段来路不明的字节。少了这条用例，那次改动会把「额外字段
// 静默消失」当成正常行为；插件的私有状态丢了，而没有任何一处报错。
func TestPluginExtraHelpersRefuseNonObjects(t *testing.T) {
	t.Parallel()

	if _, err := mergePluginExtra([]byte(`["不是对象"]`), json.RawMessage(`{"a":1}`)); err == nil {
		t.Error("底稿不是对象时 mergePluginExtra 该报错")
	}
	if _, err := pluginExtraOf([]byte(`["不是对象"]`)); !errors.Is(err, ErrMalformedValue) {
		t.Errorf("底稿不是对象时 pluginExtraOf 该报 ErrMalformedValue，实际 %v", err)
	}
}

// TestAnEmptyPluginExtraObjectIsHarmless 钉住 Extra 是个空对象时排出来的字节
// 和没有它一模一样。
func TestAnEmptyPluginExtraObjectIsHarmless(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(PluginSource{Plugin: "p", Extra: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("排出去不该失败：%v", err)
	}
	if string(data) != `{"kind":"plugin","plugin":"p"}` {
		t.Errorf("空的额外字段不该改变字节，实际 %s", data)
	}
}

// TestMalformedSourceBytesAreRefused 钉住排不成来源形状的字节报 [ErrMalformedValue]。
func TestMalformedSourceBytesAreRefused(t *testing.T) {
	t.Parallel()

	for name, data := range map[string]string{
		"根本不是 JSON": `{`,
		"标签不是字符串":   `{"kind":7}`,
		"插件名不是字符串":  `{"kind":"plugin","plugin":7}`,
		"提供方不是字符串":  `{"kind":"model","provider":7}`,
		"调用标识不是字符串": `{"kind":"tool","callId":7}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := UnmarshalMessageSource([]byte(data)); !errors.Is(err, ErrMalformedValue) {
				t.Errorf("该报 ErrMalformedValue，实际 %v", err)
			}
		})
	}
}

// TestAnInvalidReplayStateFailsLoudly 钉住不合法的重放状态当场报错，不排成 null。
//
// 这是本包里少数几处「宁可失败」的地方：一条重放状态是 null 的消息，
// 在适配器看来就是一次静默的重放丢失，而那件事在日志里看不出来。
func TestAnInvalidReplayStateFailsLoudly(t *testing.T) {
	t.Parallel()

	source := ModelSource{Provenance: Provenance{Provider: "p", Model: "m", ReplayState: []byte("{oops")}}
	if _, err := json.Marshal(source); !errorIsMalformed(err) {
		t.Errorf("该报 ErrMalformedValue，实际 %v", err)
	}
}

// TestAMessageSurvivesTheRoundTrip 钉住整条消息的往返。
func TestAMessageSurvivesTheRoundTrip(t *testing.T) {
	t.Parallel()

	for name, original := range map[string]Message{
		"用户说的话": NewUserMessage(Content{TextBlock{Text: "hi"}}, UserSource{}),
		"助手回答": NewAssistantMessage(
			Content{TextBlock{Text: "ok"}, ToolCallBlock{ID: "c", Name: "read", Arguments: "{}"}},
			Provenance{Provider: "p", Model: "m"},
		),
		"工具结果":   NewToolResultMessage("c", Content{TextBlock{Text: "done"}}, false),
		"工具结果出错": NewToolResultMessage("c", Content{TextBlock{Text: "boom"}}, true),
		"插件注入":   NewUserMessage(nil, PluginSource{Plugin: "memory", Context: InstructionsContext{}}),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(original)
			if err != nil {
				t.Fatalf("排出去不该失败：%v", err)
			}
			var back Message
			if err := json.Unmarshal(data, &back); err != nil {
				t.Fatalf("读回来不该失败：%v", err)
			}
			if back.ID != original.ID || back.Role != original.Role {
				t.Errorf("身份或角色变了：%#v", back)
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

// TestMalformedMessageBytesAreRefused 钉住读不回来的消息报 [ErrMalformedValue]。
func TestMalformedMessageBytesAreRefused(t *testing.T) {
	t.Parallel()

	for name, data := range map[string]string{
		"根本不是 JSON": `{`,
		"角色不是字符串":   `{"role":7}`,
		"来源读不回来":    `{"role":"user","source":{"kind":7}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// 直接叫 UnmarshalJSON 而不走 json.Unmarshal：后者会先整段验一遍语法，
			// 语法坏掉时根本不会叫到本包的方法，那样测的就不是本包了。
			var message Message
			if err := message.UnmarshalJSON([]byte(data)); !errors.Is(err, ErrMalformedValue) {
				t.Errorf("该报 ErrMalformedValue，实际 %v", err)
			}
		})
	}
}

// TestAMessageThatCannotBeMarshalledFails 钉住来源排不出去时整条消息也排不出去。
func TestAMessageThatCannotBeMarshalledFails(t *testing.T) {
	t.Parallel()

	message := Message{Role: RoleAssistant, Source: UnknownSource{Kind: "x"}}
	if _, err := json.Marshal(message); !errorIsMalformed(err) {
		t.Errorf("该报 ErrMalformedValue，实际 %v", err)
	}
}

// TestConstructorsPinTheNarrowing 钉住三个构造函数把该钉死的字段钉死。
//
// 源: packages/llm/llm/src/message.ts:187-241
//
// 这是 DSH 那三个子类型在 Go 里**写的一侧**：调用方给不出一条角色和来源对不上的消息。
func TestConstructorsPinTheNarrowing(t *testing.T) {
	t.Parallel()

	t.Run("用户消息", func(t *testing.T) {
		t.Parallel()

		message := NewUserMessage(nil, UserSource{})
		if message.Role != RoleUser {
			t.Errorf("角色该是 user，实际 %q", message.Role)
		}
	})

	t.Run("助手消息", func(t *testing.T) {
		t.Parallel()

		message := NewAssistantMessage(nil, Provenance{Provider: "p", Model: "m"})
		if message.Role != RoleAssistant {
			t.Errorf("角色该是 assistant，实际 %q", message.Role)
		}
		source, ok := message.Source.(ModelSource)
		if !ok {
			t.Fatalf("来源该是 ModelSource，实际 %#v", message.Source)
		}
		if source.Provider != "p" || source.Model != "m" {
			t.Errorf("产出方信息没带上：%#v", source)
		}
	})

	t.Run("工具结果消息的角色是 user", func(t *testing.T) {
		t.Parallel()

		message := NewToolResultMessage("c", Content{TextBlock{Text: "x"}}, true)
		if message.Role != RoleUser {
			t.Errorf("角色该是 user，实际 %q", message.Role)
		}
		if source := message.Source.(ToolSource); source.CallID != "c" {
			t.Errorf("来源上的调用标识该是 c，实际 %q", source.CallID)
		}
		block, ok := message.ToolResult()
		if !ok {
			t.Fatalf("该取得出工具结果")
		}
		if block.ToolCallID != "c" || !block.IsError {
			t.Errorf("块里的调用相关性或错误位不对：%#v", block)
		}
	})

	t.Run("每条消息都有全新的身份", func(t *testing.T) {
		t.Parallel()

		first := NewUserMessage(nil, UserSource{})
		second := NewUserMessage(nil, UserSource{})
		if first.ID == "" || first.ID == second.ID {
			t.Errorf("身份该是全新且互不相同的：%q / %q", first.ID, second.ID)
		}
	})

	t.Run("内容被复制了一份", func(t *testing.T) {
		t.Parallel()

		content := Content{TextBlock{Text: "before"}}
		message := NewUserMessage(content, UserSource{})
		content[0] = TextBlock{Text: "after"}

		if got := message.Content[0].(TextBlock).Text; got != "before" {
			t.Errorf("调用方改得动这条消息：%q", got)
		}
	})
}

// TestTheReadSideRecoversTheNarrowing 钉住两个读取面，取不到就是 false。
//
// 源: packages/llm/llm/src/message.ts:145-156
//
// 每一条 false 都对应 DSH 那两个子类型里的一个约束条件，逐条钉而不是合并成一句
// 「不对的都返回 false」——它们各自坏在不同的地方。
func TestTheReadSideRecoversTheNarrowing(t *testing.T) {
	t.Parallel()

	t.Run("模型来源", func(t *testing.T) {
		t.Parallel()

		for name, expectation := range map[string]struct {
			message Message
			want    bool
		}{
			"模型产出的助手消息":      {NewAssistantMessage(nil, Provenance{Provider: "p"}), true},
			"角色不是 assistant": {NewUserMessage(nil, UserSource{}), false},
			"助手消息但来源不是模型":    {Message{Role: RoleAssistant, Source: UserSource{}}, false},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				if _, ok := expectation.message.ModelSource(); ok != expectation.want {
					t.Errorf("该是 %v，实际 %v", expectation.want, ok)
				}
			})
		}
	})

	t.Run("工具结果", func(t *testing.T) {
		t.Parallel()

		for name, expectation := range map[string]struct {
			message Message
			want    bool
		}{
			"工具结果消息": {NewToolResultMessage("c", nil, false), true},
			"角色不是 user": {
				Message{Role: RoleAssistant, Source: ToolSource{CallID: "c"}, Content: Content{ToolResultBlock{}}},
				false,
			},
			"来源不是工具": {
				Message{Role: RoleUser, Source: UserSource{}, Content: Content{ToolResultBlock{}}},
				false,
			},
			"内容不止一块": {
				Message{Role: RoleUser, Source: ToolSource{}, Content: Content{ToolResultBlock{}, TextBlock{}}},
				false,
			},
			"唯一那一块不是工具结果": {
				Message{Role: RoleUser, Source: ToolSource{}, Content: Content{TextBlock{}}},
				false,
			},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				if _, ok := expectation.message.ToolResult(); ok != expectation.want {
					t.Errorf("该是 %v，实际 %v", expectation.want, ok)
				}
			})
		}
	})
}

// TestMessageCloneCopiesTheSlicesToo 钉住消息的深复制覆盖三处切片。
//
// 三处分别在三种来源里，各走一条 [Message.Clone] 的分支，所以逐条钉。
func TestMessageCloneCopiesTheSlicesToo(t *testing.T) {
	t.Parallel()

	t.Run("内容", func(t *testing.T) {
		t.Parallel()

		original := Message{Content: Content{TextBlock{Text: "before"}}, Source: UserSource{}}
		cloned := original.Clone()
		cloned.Content[0] = TextBlock{Text: "after"}

		if got := original.Content[0].(TextBlock).Text; got != "before" {
			t.Errorf("原件被改动了：%q", got)
		}
	})

	t.Run("不认识的来源的原始字节", func(t *testing.T) {
		t.Parallel()

		original := Message{Source: UnknownSource{Kind: "x", Raw: []byte(`{"kind":"x"}`)}}
		cloned := original.Clone()
		cloned.Source.(UnknownSource).Raw[0] = 'X'

		if got := original.Source.(UnknownSource).Raw[0]; got != '{' {
			t.Errorf("原件被改动了：%q", got)
		}
	})

	t.Run("模型来源的重放状态", func(t *testing.T) {
		t.Parallel()

		original := Message{Source: ModelSource{Provenance: Provenance{ReplayState: []byte(`{"a":1}`)}}}
		cloned := original.Clone()
		cloned.Source.(ModelSource).ReplayState[0] = 'X'

		if got := original.Source.(ModelSource).ReplayState[0]; got != '{' {
			t.Errorf("原件被改动了：%q", got)
		}
	})

	t.Run("插件来源的快照分节", func(t *testing.T) {
		t.Parallel()

		original := Message{Source: PluginSource{
			Plugin:  "w",
			Context: SnapshotContext{Sections: []ContextSnapshotSection{{Name: "before"}}},
		}}
		cloned := original.Clone()
		cloned.Source.(PluginSource).Context.(SnapshotContext).Sections[0].Name = "after"

		got := original.Source.(PluginSource).Context.(SnapshotContext).Sections[0].Name
		if got != "before" {
			t.Errorf("原件被改动了：%q", got)
		}
	})

	t.Run("其它形态的插件来源原样过", func(t *testing.T) {
		t.Parallel()

		original := Message{Source: PluginSource{Plugin: "p", Context: NoticeContext{Summary: "s"}}}
		cloned := original.Clone()
		if cloned.Source.(PluginSource).Context.(NoticeContext).Summary != "s" {
			t.Errorf("形态没带过来：%#v", cloned.Source)
		}
	})
}

// TestBoundContextSummaryCountsRunes 钉住一行陈述按**字**收，不按字节。
//
// 源: packages/llm/llm/src/message.ts:114-123
//
// 这是本包对 DSH 的一处有意分歧：那边按 UTF-16 码元算。按字节收会把一行中文陈述
// 在第四十个字上砍断，而这个上限守的是「一行折叠的对话行放得下」，那件事按字算才成立。
func TestBoundContextSummaryCountsRunes(t *testing.T) {
	t.Parallel()

	t.Run("不超长的原样返回", func(t *testing.T) {
		t.Parallel()

		summary := strings.Repeat("字", ContextSummaryMaxChars)
		if got := BoundContextSummary(summary); got != summary {
			t.Errorf("刚好到上限不该动它")
		}
	})

	t.Run("超长的按字截断并补省略号", func(t *testing.T) {
		t.Parallel()

		got := BoundContextSummary(strings.Repeat("字", ContextSummaryMaxChars+1))
		runes := []rune(got)
		if len(runes) != ContextSummaryMaxChars {
			t.Errorf("该收到 %d 个字，实际 %d", ContextSummaryMaxChars, len(runes))
		}
		if runes[len(runes)-1] != '…' {
			t.Errorf("最后一个字该是省略号，实际 %q", runes[len(runes)-1])
		}
	})

	t.Run("空串原样返回", func(t *testing.T) {
		t.Parallel()

		if got := BoundContextSummary(""); got != "" {
			t.Errorf("空串该原样返回，实际 %q", got)
		}
	})
}
