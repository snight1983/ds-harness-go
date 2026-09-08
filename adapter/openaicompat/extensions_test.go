// 本文件验扩展字段那一层：字段的归属在登记那一刻就定死，备好的取值真的落到了发出去
// 那份请求体的顶层，而备不出来的时候请求根本不发。
//
// 落到线上那几条走真服务器（llm/mockserver），因为要考的恰恰是「这个键有没有出现在
// 请求体里」——打桩的话看见的是自己拼的那份参数，而不是 SDK 真正发出去的字节。

package openaicompat

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/llm/mockserver"
)

// staticExtension 造一个每次都交同一份字段的贡献方。
func staticExtension(contributor string, fields map[string]any) Extension {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	return Extension{
		Contributor: contributor,
		Fields:      names,
		Prepare: func(context.Context, ExtensionRequest) (map[string]any, error) {
			return fields, nil
		},
	}
}

// registryOf 造一张登记好的表，登记失败就让用例当场停下。
func registryOf(t *testing.T, extensions ...Extension) *ExtensionRegistry {
	t.Helper()
	registry := NewExtensionRegistry()
	for _, extension := range extensions {
		if err := registry.Register(extension); err != nil {
			t.Fatalf("这个贡献方本该登记得上：%v", err)
		}
	}
	return registry
}

// sentBody 起一台模拟服务器、发一次请求，交出线上收到的那份请求体。
func sentBody(t *testing.T, registry *ExtensionRegistry) map[string]any {
	t.Helper()
	server := startMock(t, mockserver.Options{
		Sequence:    []mockserver.Behavior{mockserver.BehaviorSuccess},
		SuccessText: "ok",
	})
	adapter := newAdapter(t, fixed(profilesOf(t, "acme", routeTo(server))),
		func(options *AdapterOptions) { options.Extensions = registry })

	sequence, err := adapter.Stream(t.Context(), request("acme"))
	if err != nil {
		t.Fatalf("派发失败：%v", err)
	}
	if _, err := drain(sequence); err != nil {
		t.Fatalf("这条流本该读完：%v", err)
	}
	records := server.Requests()
	if len(records) != 1 {
		t.Fatalf("本该只收到一次请求：%d", len(records))
	}
	body, isObject := records[0].Body.(map[string]any)
	if !isObject {
		t.Fatalf("请求体不是一个对象：%#v", records[0].Body)
	}
	return body
}

// TestExtensionFieldsReachTheWire 验两个贡献方各自的字段都落到了请求体顶层。
//
// 这是这张表存在的全部理由：宿主要往请求上加一个提供方私有字段，不必改本包。
func TestExtensionFieldsReachTheWire(t *testing.T) {
	body := sentBody(t, registryOf(t,
		staticExtension("logs", map[string]any{"dsh_session_log": "abc"}),
		staticExtension("budget", map[string]any{"tenant_budget": 42}),
	))

	if body["dsh_session_log"] != "abc" {
		t.Errorf("第一个贡献方的字段没到：%v", body["dsh_session_log"])
	}
	if budget, isNumber := body["tenant_budget"].(float64); !isNumber || budget != 42 {
		t.Errorf("第二个贡献方的字段没到：%#v", body["tenant_budget"])
	}
	// 追加不该动到本包自己拼的那些字段。
	if body["model"] != "m" {
		t.Errorf("适配器自己的字段被动了：%v", body["model"])
	}
	if _, sent := body["messages"]; !sent {
		t.Error("对话历史没了")
	}
}

// TestExtensionSkippedFieldStaysOff 验一个贡献方这一次不交某个字段时，请求体里就没有它。
//
// 「登记过」和「这一次要发」是两件事：一个按会话状态决定发不发的贡献方，只有在
// 不发的那一次请求上真的不出现这个键，才算表达得了「这次没有」。
func TestExtensionSkippedFieldStaysOff(t *testing.T) {
	registry := registryOf(t, Extension{
		Contributor: "logs",
		Fields:      []string{"dsh_session_log"},
		Prepare: func(context.Context, ExtensionRequest) (map[string]any, error) {
			return nil, nil
		},
	})

	if _, sent := sentBody(t, registry)["dsh_session_log"]; sent {
		t.Error("这一次本该不出这个字段")
	}
}

// TestExtensionRequestCarriesThePreparedFacts 验交给贡献方的那份事实是这次请求本身。
func TestExtensionRequestCarriesThePreparedFacts(t *testing.T) {
	var seen ExtensionRequest
	registry := registryOf(t, Extension{
		Contributor: "probe",
		Fields:      []string{"probe_field"},
		Prepare: func(_ context.Context, incoming ExtensionRequest) (map[string]any, error) {
			seen = incoming
			return nil, nil
		},
	})

	server := startMock(t, mockserver.Options{
		Sequence:    []mockserver.Behavior{mockserver.BehaviorSuccess},
		SuccessText: "ok",
	})
	adapter := newAdapter(t, fixed(profilesOf(t, "acme", routeTo(server))),
		func(options *AdapterOptions) { options.Extensions = registry })
	call := request("acme")
	call.SessionID = "s-1"
	call.Purpose = llm.PurposeCompaction
	sequence, err := adapter.Stream(t.Context(), call)
	if err != nil {
		t.Fatalf("派发失败：%v", err)
	}
	if _, err := drain(sequence); err != nil {
		t.Fatalf("这条流本该读完：%v", err)
	}

	if seen.Provider != "acme" || seen.Model != "m" {
		t.Errorf("路由与模型没带上：%+v", seen)
	}
	if seen.SessionID != "s-1" || seen.Purpose != llm.PurposeCompaction {
		t.Errorf("会话身份与用途没带上：%+v", seen)
	}
	var body map[string]any
	if err := json.Unmarshal(seen.Body, &body); err != nil {
		t.Fatalf("交给贡献方的那份事实不是 JSON：%v", err)
	}
	if _, present := body["messages"]; !present {
		t.Errorf("事实里该有对话历史：%v", body)
	}
	// stream 那一位由 SDK 在真正发出去的那一刻补上，这份事实里没有它。
	if _, present := body["stream"]; present {
		t.Errorf("事实里不该有 stream：%v", body)
	}
}

// TestExtensionFailureStopsBeforeDispatch 验备不出来的时候请求根本不发。
//
// 一个贡献方备不出自己的字段，说明这次请求本来要带的东西没带上；把它照旧发出去，
// 换来的是一次「看起来成了、其实少了半份内容」的调用。
func TestExtensionFailureStopsBeforeDispatch(t *testing.T) {
	cases := map[string]Extension{
		"贡献方自己失败": {
			Contributor: "logs",
			Fields:      []string{"dsh_session_log"},
			Prepare: func(context.Context, ExtensionRequest) (map[string]any, error) {
				return nil, errors.New("水位读不出来")
			},
		},
		"交了没认领过的字段": {
			Contributor: "logs",
			Fields:      []string{"dsh_session_log"},
			Prepare: func(context.Context, ExtensionRequest) (map[string]any, error) {
				return map[string]any{"tenant_budget": 1}, nil
			},
		},
	}
	for name, extension := range cases {
		t.Run(name, func(t *testing.T) {
			server := startMock(t, mockserver.Options{
				Sequence:    []mockserver.Behavior{mockserver.BehaviorSuccess},
				SuccessText: "ok",
			})
			adapter := newAdapter(t, fixed(profilesOf(t, "acme", routeTo(server))),
				func(options *AdapterOptions) { options.Extensions = registryOf(t, extension) })

			_, err := adapter.Stream(t.Context(), request("acme"))
			if code := failureCode(t, err); code != ExtensionFailedCode {
				t.Errorf("该报 %s：%s", ExtensionFailedCode, code)
			}
			if records := server.Requests(); len(records) != 0 {
				t.Errorf("这次请求本该没发出去：%d", len(records))
			}
		})
	}
}

// TestRegisterRefusesADisputedField 验两个贡献方抢同一个键在装配时就红。
//
// 字段的生命周期归贡献方管，两个贡献方抢同一个键是装配写错了。等到请求时才发现的话，
// 谁赢取决于登记次序，而那是一份配置根本没法解释的行为。
func TestRegisterRefusesADisputedField(t *testing.T) {
	registry := registryOf(t, staticExtension("logs", map[string]any{"dsh_session_log": "a"}))

	err := registry.Register(staticExtension("audit", map[string]any{"dsh_session_log": "b"}))
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("抢同一个键该被拒：%v", err)
	}
	if registry.Len() != 1 {
		t.Errorf("被拒的登记不该留在表里：%d", registry.Len())
	}
}

// TestRegisterRefusesMalformedEntries 验登记项本身的那几条硬要求。
func TestRegisterRefusesMalformedEntries(t *testing.T) {
	prepare := func(context.Context, ExtensionRequest) (map[string]any, error) { return nil, nil }
	cases := map[string]Extension{
		"名字是空的":      {Contributor: "", Fields: []string{"a"}, Prepare: prepare},
		"名字带首尾空白":    {Contributor: " logs ", Fields: []string{"a"}, Prepare: prepare},
		"没有 Prepare": {Contributor: "logs", Fields: []string{"a"}},
		"一个字段都没认领":   {Contributor: "logs", Prepare: prepare},
		"字段名是空的":     {Contributor: "logs", Fields: []string{""}, Prepare: prepare},
		"字段名是一条路径":   {Contributor: "logs", Fields: []string{"a.b"}, Prepare: prepare},
		"字段名带通配":     {Contributor: "logs", Fields: []string{"a*"}, Prepare: prepare},
		"字段名以数字打头":   {Contributor: "logs", Fields: []string{"1a"}, Prepare: prepare},
		"认领了本包的字段":   {Contributor: "logs", Fields: []string{"messages"}, Prepare: prepare},
		"同一个字段认领两遍":  {Contributor: "logs", Fields: []string{"a", "a"}, Prepare: prepare},
	}
	for name, extension := range cases {
		t.Run(name, func(t *testing.T) {
			registry := NewExtensionRegistry()
			if err := registry.Register(extension); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("这个登记项该被拒：%v", err)
			}
			if registry.Len() != 0 {
				t.Errorf("被拒的登记不该留在表里：%d", registry.Len())
			}
		})
	}
}

// TestRegisterIsAllOrNothing 验一个登记项里有一个字段不合法时，前面那些也不落表。
//
// 半登记上的贡献方比没登记的更难查：它交出来的字段会有一部分静悄悄地不生效。
func TestRegisterIsAllOrNothing(t *testing.T) {
	registry := NewExtensionRegistry()
	err := registry.Register(Extension{
		Contributor: "logs",
		Fields:      []string{"good_field", "bad.field"},
		Prepare: func(context.Context, ExtensionRequest) (map[string]any, error) {
			return nil, nil
		},
	})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("这个登记项该被拒：%v", err)
	}
	// 前一个字段没被占住：换一个贡献方来认领它该成。
	if err := registry.Register(staticExtension("audit", map[string]any{"good_field": 1})); err != nil {
		t.Errorf("这个字段本该还是空着的：%v", err)
	}
}

// TestNilExtensionRegistryIsEmpty 验不装这张表的装配一切照旧。
func TestNilExtensionRegistryIsEmpty(t *testing.T) {
	var registry *ExtensionRegistry
	if registry.Len() != 0 {
		t.Errorf("nil 表该是空的：%d", registry.Len())
	}
	fields, err := registry.Prepare(t.Context(), ExtensionRequest{})
	if err != nil || fields != nil {
		t.Errorf("nil 表不该备出任何字段：%v %v", fields, err)
	}
	if err := registry.Register(staticExtension("logs", map[string]any{"a": 1})); !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("往 nil 表里登记该被拒：%v", err)
	}
	if _, sent := sentBody(t, nil)["dsh_session_log"]; sent {
		t.Error("没装表的请求不该带扩展字段")
	}
}
