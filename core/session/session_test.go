// 本文件的作用：会话本身的测试——构造 seed 怎么验、追加怎么定序、几份增量折叠
// 各自在什么时候重算。

package session

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ds-harness-go/llm"
	sessionlog "ds-harness-go/session"
)

func TestASessionWithoutASeedStartsEmpty(t *testing.T) {
	session, err := NewSession("s", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if session.ID() != "s" {
		t.Fatalf("标识是 %q", session.ID())
	}
	if session.Seq() != 0 || session.FirstLiveSeq() != 0 {
		t.Fatalf("空会话该从 0 起步：seq=%d firstLive=%d", session.Seq(), session.FirstLiveSeq())
	}
	if len(session.Events()) != 0 {
		t.Fatalf("日志该是空的：%#v", session.Events())
	}
	// 没给头就合成一份最小的，版本号盖上当下的格式版本。
	header := session.Header()
	if header.Version != sessionlog.FormatVersion || header.ID != "s" || header.CreatedAt == 0 {
		t.Fatalf("合成的头是 %#v", header)
	}
}

func TestAnEmptySeedStillGetsTheEndSeedMarker(t *testing.T) {
	// nil 和长度为零的非 nil 切片不是一回事：后者是「给了一份空 seed」，
	// 照样补标记——[Store.Fork] 分叉一个空会话走的就是这条路。
	session, err := NewSession("s", Options{Seed: []sessionlog.Event{}})
	if err != nil {
		t.Fatal(err)
	}
	events := session.Events()
	if len(events) != 1 || events[0].Type != sessionlog.EventSessionEndSeed {
		t.Fatalf("该只有一条标记：%#v", events)
	}
	if session.FirstLiveSeq() != 0 {
		t.Fatalf("firstLiveSeq 是 %d", session.FirstLiveSeq())
	}
}

func TestASeedIsCopiedAndClosedWithAMarker(t *testing.T) {
	seed := seedOf(userEvent(t, "你好"), assistantEvent(t, 1, 1, "在"))
	session, err := NewSession("s", Options{Seed: seed})
	if err != nil {
		t.Fatal(err)
	}
	events := session.Events()
	if len(events) != 3 || events[2].Type != sessionlog.EventSessionEndSeed {
		t.Fatalf("日志是 %#v", events)
	}
	if session.FirstLiveSeq() != 2 {
		t.Fatalf("firstLiveSeq 是 %d", session.FirstLiveSeq())
	}
	// 借来的 seed 被复制过：改调用方手里那份底层数组动不了日志。
	seed[0].Data[0] = 'X'
	if string(events[0].Data) == string(seed[0].Data) {
		t.Fatal("seed 没有被复制")
	}
}

func TestASeedAlreadyEndingWithTheMarkerIsNotMarkedAgain(t *testing.T) {
	// 一个冷会话是被第一次触碰时唤醒的，反复打开同一个会话不能让它每开一次长一条。
	seed := seedOf(userEvent(t, "你好"), sessionlog.Event{Type: sessionlog.EventSessionEndSeed})
	session, err := NewSession("s", Options{Seed: seed})
	if err != nil {
		t.Fatal(err)
	}
	if len(session.Events()) != 2 {
		t.Fatalf("日志是 %#v", session.Events())
	}
	if session.FirstLiveSeq() != 2 {
		t.Fatalf("firstLiveSeq 是 %d", session.FirstLiveSeq())
	}
}

func TestRestoreTakesOwnershipAndNeedsAStoredHeader(t *testing.T) {
	header := sessionlog.SessionHeader{Version: sessionlog.FormatVersion, ID: "s", CreatedAt: 7}
	seed := seedOf(userEvent(t, "你好"))
	session, err := RestoreSession("s", seed, header, nil)
	if err != nil {
		t.Fatal(err)
	}
	if session.Header().CreatedAt != 7 {
		t.Fatalf("存下来的头该原样保管：%#v", session.Header())
	}
	// 接手不复制：日志里那一条和调用方交出去的是同一段底层数组。
	if &session.Events()[0].Data[0] != &seed[0].Data[0] {
		t.Fatal("恢复路径不该复制事件")
	}

	if _, err := newSession("s", Options{}, true); !errors.Is(err, ErrInvalidHeader) {
		t.Fatalf("恢复没给头该报 ErrInvalidHeader：%v", err)
	}
	bad := sessionlog.SessionHeader{Version: sessionlog.FormatVersion, ID: "other"}
	if _, err := RestoreSession("s", nil, bad, nil); !errors.Is(err, ErrInvalidHeader) {
		t.Fatalf("头里的标识对不上该报错：%v", err)
	}
}

func TestSessionHeaderValidation(t *testing.T) {
	good := sessionlog.SessionHeader{Version: sessionlog.FormatVersion, ID: "s"}
	cases := map[string]struct {
		mutate func(*sessionlog.SessionHeader)
		want   string
	}{
		"版本号不对":  {func(h *sessionlog.SessionHeader) { h.Version = 99 }, "version must be"},
		"标识对不上":  {func(h *sessionlog.SessionHeader) { h.ID = "other" }, "does not match"},
		"创建时刻为负": {func(h *sessionlog.SessionHeader) { h.CreatedAt = -1 }, "createdAt"},
		"工作目录不是绝对路径": {
			func(h *sessionlog.SessionHeader) { h.Cwd = "relative/path" }, "absolute path",
		},
		"血统边界为负": {func(h *sessionlog.SessionHeader) { h.SeedLength = -1 }, "seedLength"},
		"来路不认识":  {func(h *sessionlog.SessionHeader) { h.Origin = "nope" }, "origin must be"},
		"委派层数为负": {
			func(h *sessionlog.SessionHeader) { h.DelegationDepth = -1 }, "delegationDepth",
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			header := good
			testCase.mutate(&header)
			err := validateSessionHeader("s", header)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("诊断是 %v", err)
			}
		})
	}

	// 一份合法的头（带上一条本机的绝对路径和一个认识的来路）过得去。
	header := good
	header.Origin = sessionlog.OriginSubagent
	header.Cwd = absoluteTestPath()
	if err := validateSessionHeader("s", header); err != nil {
		t.Fatal(err)
	}
}

func TestSeedEventValidation(t *testing.T) {
	cases := map[string]struct {
		event sessionlog.Event
		want  string
	}{
		"旧的增量请求头格式": {
			sessionlog.Event{Type: legacyHeaderDelta}, "legacy request/header-delta",
		},
		"没有类型": {sessionlog.Event{}, "invalid event envelope"},
		"序号为负": {
			sessionlog.Event{Type: sessionlog.EventTurnStart, Seq: -1}, "invalid event envelope",
		},
		"负载排不回去": {
			sessionlog.Event{Type: sessionlog.EventTurnStart, Data: json.RawMessage("{")},
			"not losslessly JSON-serializable",
		},
		"请求头缺提供方": {
			sessionlog.Event{Type: sessionlog.EventRequestHeader, Data: json.RawMessage(`[]`)},
			"lacks provider/model",
		},
		"请求头缺模型": {
			sessionlog.Event{
				Type: sessionlog.EventRequestHeader,
				Data: json.RawMessage(`{"header":{"config":{"provider":"p"}},"reason":"initial"}`),
			},
			"lacks provider/model",
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			err := validateSeedEvent(testCase.event, 0)
			if !errors.Is(err, ErrInvalidSeed) || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("诊断是 %v", err)
			}
		})
	}
}

func TestAdapterDefaultsMustPointAtAPresentConfigField(t *testing.T) {
	// 一个「这一项是适配器解析出来的」标记，指向一个根本不存在的字段，说明写下
	// 这份头的那一方和读它的这一方对不上。
	header := sessionlog.EpochHeader{Config: llm.CallConfig{Provider: "p", Model: "m"}}
	header.AdapterDefaults.ReasoningEffort = true
	if err := validateAdapterDefaults(header, 0); err == nil {
		t.Fatal("空的 reasoningEffort 配上立着的标记该报错")
	}
	header.AdapterDefaults.ReasoningEffort = false
	header.AdapterDefaults.MaxTokens = true
	if err := validateAdapterDefaults(header, 0); err == nil {
		t.Fatal("零的 maxTokens 配上立着的标记该报错")
	}
	header.Config.MaxTokens = 8
	if err := validateAdapterDefaults(header, 0); err != nil {
		t.Fatal(err)
	}
}

func TestMessageEventShapeValidation(t *testing.T) {
	withMessage := func(kind sessionlog.EventType, message llm.Message) sessionlog.Event {
		payload := any(sessionlog.UserMessageData{Message: message})
		switch kind {
		case sessionlog.EventAssistantMessage:
			payload = sessionlog.AssistantMessageData{Turn: 1, Step: 1, Message: message}
		case sessionlog.EventToolResult:
			payload = sessionlog.ToolResultData{Turn: 1, Step: 1, Message: message}
		}
		return sessionlog.Event{Type: kind, Data: data(t, payload), SurfaceOp: sessionlog.AppendOp{}}
	}

	noID := llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "x"}}, llm.UserSource{})
	noID.ID = ""
	wrongRole := llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "x"}}, llm.UserSource{})
	noSource := llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "x"}}, llm.UserSource{})
	noSource.Source = nil
	notModel := llm.NewMessage(llm.RoleAssistant, llm.Content{llm.TextBlock{Text: "x"}}, llm.UserSource{})
	blankModel := llm.NewAssistantMessage(llm.Content{llm.TextBlock{Text: "x"}}, llm.Provenance{})
	notTool := llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "x"}}, llm.UserSource{})
	twoBlocks := llm.NewToolResultMessage("call-1", llm.Content{llm.TextBlock{Text: "ok"}}, false)
	twoBlocks.Content = append(twoBlocks.Content, llm.TextBlock{Text: "多出来的"})
	notResultBlock := llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "x"}}, llm.ToolSource{CallID: "call-1"})
	mismatched := llm.NewToolResultMessage("call-1", llm.Content{llm.TextBlock{Text: "ok"}}, false)
	mismatched.Source = llm.ToolSource{CallID: "call-2"}

	cases := map[string]struct {
		event sessionlog.Event
		want  string
	}{
		"消息没有身份":     {withMessage(sessionlog.EventUserMessage, noID), "lacks an identified message"},
		"角色对不上事件类型":  {withMessage(sessionlog.EventAssistantMessage, wrongRole), "must have role"},
		"消息没有来源":     {withMessage(sessionlog.EventUserMessage, noSource), "invalid source"},
		"助手消息来源不是模型": {withMessage(sessionlog.EventAssistantMessage, notModel), "must have model source"},
		"助手消息来源空着":   {withMessage(sessionlog.EventAssistantMessage, blankModel), "must have model source"},
		"工具结果来源不是工具": {withMessage(sessionlog.EventToolResult, notTool), "must have tool source"},
		"工具结果不止一块":   {withMessage(sessionlog.EventToolResult, twoBlocks), "one tool-result block"},
		"工具结果那一块不是结果": {
			withMessage(sessionlog.EventToolResult, notResultBlock), "one tool-result block",
		},
		"两处调用标识对不上": {withMessage(sessionlog.EventToolResult, mismatched), "mismatched tool call ids"},
		"负载解不开": {
			sessionlog.Event{
				Type: sessionlog.EventUserMessage, Data: json.RawMessage(`[]`),
				SurfaceOp: sessionlog.AppendOp{},
			},
			"lacks an identified message",
		},
		"助手消息负载解不开": {
			sessionlog.Event{
				Type: sessionlog.EventAssistantMessage, Data: json.RawMessage(`[]`),
				SurfaceOp: sessionlog.AppendOp{},
			},
			"lacks an identified message",
		},
		"工具结果负载解不开": {
			sessionlog.Event{
				Type: sessionlog.EventToolResult, Data: json.RawMessage(`[]`),
				SurfaceOp: sessionlog.AppendOp{},
			},
			"lacks an identified message",
		},
		"负载整个空着": {
			sessionlog.Event{Type: sessionlog.EventUserMessage, SurfaceOp: sessionlog.AppendOp{}},
			"lacks an identified message",
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			err := validateSeedEvent(testCase.event, 0)
			if !errors.Is(err, ErrInvalidSeed) || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("诊断是 %v", err)
			}
		})
	}

	// 三种都对的那一份过得去。
	for _, kind := range []sessionlog.EventType{
		sessionlog.EventUserMessage, sessionlog.EventAssistantMessage, sessionlog.EventToolResult,
	} {
		event := userEvent(t, "你好")
		switch kind {
		case sessionlog.EventAssistantMessage:
			event = assistantEvent(t, 1, 1, "在")
		case sessionlog.EventToolResult:
			event = toolResultEvent(t, 1, 1, "call-1")
		}
		if err := validateSeedEvent(event, 0); err != nil {
			t.Fatalf("%s 该过得去：%v", kind, err)
		}
	}
	// 不带消息的类型直接放过——messageOf 说「这条不带消息」，形状检查就无事可做。
	if _, carries, err := messageOf(turnStart(1)); carries || err != nil {
		t.Fatalf("turn/start 不带消息：carries=%v err=%v", carries, err)
	}
	if err := validateMessageEventShape(turnStart(1), "turn/start"); err != nil {
		t.Fatal(err)
	}
}

func TestASeedMustBeContiguousFromZero(t *testing.T) {
	seed := []sessionlog.Event{{Type: sessionlog.EventTurnStart, Seq: 3, Data: json.RawMessage(`{"turn":1}`)}}
	_, err := NewSession("s", Options{Seed: seed})
	if !errors.Is(err, ErrInvalidSeed) || !strings.Contains(err.Error(), "contiguous from 0") {
		t.Fatalf("诊断是 %v", err)
	}
}

func TestASeedEventThatFailsValidationStopsTheConstruction(t *testing.T) {
	// 逐条那道检查在构造里就跑，不是等到表面转移才发现。
	seed := []sessionlog.Event{{Seq: 0, Data: json.RawMessage(`{}`)}}
	_, err := NewSession("s", Options{Seed: seed})
	if !errors.Is(err, ErrInvalidSeed) || !strings.Contains(err.Error(), "invalid event envelope") {
		t.Fatalf("诊断是 %v", err)
	}
}

func TestASeedGoesThroughTheSameSurfaceTransitionAsALiveAppend(t *testing.T) {
	// 一条够格上表面却没带标记的事件，seed 里和活追加里一样不许进。
	seed := []sessionlog.Event{{
		Type: sessionlog.EventUserMessage,
		Data: data(t, sessionlog.UserMessageData{
			Message: llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "x"}}, llm.UserSource{}),
		}),
	}}
	_, err := NewSession("s", Options{Seed: seed})
	if !errors.Is(err, ErrInvalidSeed) || !strings.Contains(err.Error(), "invalid seed event at index 0") {
		t.Fatalf("诊断是 %v", err)
	}
}

func TestASeedRejectsTheLegacyRequestHeaderReason(t *testing.T) {
	seed := []sessionlog.Event{{
		Type: sessionlog.EventRequestHeader,
		Data: json.RawMessage(`{"header":{"config":{"provider":"p","model":"m"}},"reason":"fallback"}`),
	}}
	_, err := NewSession("s", Options{Seed: seed})
	if !errors.Is(err, ErrInvalidSeed) || !strings.Contains(err.Error(), "legacy request/header reason") {
		t.Fatalf("诊断是 %v", err)
	}
}

func TestASynthesizedHeaderIsValidatedToo(t *testing.T) {
	bad := sessionlog.SessionHeader{Version: sessionlog.FormatVersion, ID: "s", Cwd: "relative"}
	if _, err := NewSession("s", Options{Header: &bad}); !errors.Is(err, ErrInvalidHeader) {
		t.Fatalf("诊断是 %v", err)
	}
}

func TestAppendStampsSeqAndTime(t *testing.T) {
	session, err := NewSession("s", Options{Now: fixedClock()})
	if err != nil {
		t.Fatal(err)
	}
	// 1001 那一格被合成会话头的 createdAt 用掉了，追加从 1002 起。
	first, err := session.Append(userEvent(t, "你好"))
	if err != nil {
		t.Fatal(err)
	}
	if first.Seq != 0 || first.Time != 1002 {
		t.Fatalf("第一条盖成了 seq=%d time=%d", first.Seq, first.Time)
	}
	second, err := session.Append(assistantEvent(t, 1, 1, "在"))
	if err != nil {
		t.Fatal(err)
	}
	if second.Seq != 1 || second.Time != 1003 {
		t.Fatalf("第二条盖成了 seq=%d time=%d", second.Seq, second.Time)
	}
	if session.Seq() != 2 {
		t.Fatalf("seq 是 %d", session.Seq())
	}
	if nodes := session.SurfaceNodes(); len(nodes) != 2 {
		t.Fatalf("表面是 %#v", nodes)
	}
	if generation := session.SurfaceReplaceGeneration(); generation != 0 {
		t.Fatalf("还没替换过，代数该是 0，实际 %d", generation)
	}
}

func TestAppendRefusesToSilentlyOverwriteACallerSuppliedSeqOrTime(t *testing.T) {
	session, err := NewSession("s", Options{})
	if err != nil {
		t.Fatal(err)
	}
	withSeq := userEvent(t, "x")
	withSeq.Seq = 5
	if _, err := session.Append(withSeq); !errors.Is(err, ErrInvalidAppend) {
		t.Fatalf("诊断是 %v", err)
	}
	withTime := userEvent(t, "x")
	withTime.Time = 5
	if _, err := session.Append(withTime); !errors.Is(err, ErrInvalidAppend) {
		t.Fatalf("诊断是 %v", err)
	}
	if session.Seq() != 0 {
		t.Fatal("失败的追加动了日志")
	}
}

func TestAppendRejectsBadPayloadsAndSurfaceViolations(t *testing.T) {
	session, err := NewSession("s", Options{})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]sessionlog.Event{
		"负载不是 JSON": {Type: sessionlog.EventTurnStart, Data: json.RawMessage("{")},
		"旧的增量请求头":   {Type: legacyHeaderDelta},
		"旧的请求头原因": {
			Type: sessionlog.EventRequestHeader,
			Data: json.RawMessage(`{"header":{"config":{"provider":"p","model":"m"}},"reason":"fallback"}`),
		},
		"够格上表面却没带标记": {Type: sessionlog.EventUserMessage, Data: json.RawMessage(`{}`)},
	}
	for name, event := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := session.Append(event); !errors.Is(err, ErrInvalidAppend) {
				t.Fatalf("诊断是 %v", err)
			}
		})
	}
	if session.Seq() != 0 {
		t.Fatalf("失败的追加动了日志：%#v", session.Events())
	}
}

func TestAnUnreadableRequestHeaderPayloadIsLeftToTheFold(t *testing.T) {
	// 那道旧词汇检查解不开负载时就当没这回事——负载本身的形状由别处负责。
	session, err := NewSession("s", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Append(sessionlog.Event{
		Type: sessionlog.EventRequestHeader, Data: json.RawMessage(`[]`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := session.RequestHeader(); err == nil {
		t.Fatal("折这一条时该报出来")
	}
}

func TestEventsHandsOutASnapshotThatLaterAppendsDoNotGrow(t *testing.T) {
	session, err := NewSession("s", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Append(userEvent(t, "你好")); err != nil {
		t.Fatal(err)
	}
	before := session.Events()
	// 同一份快照在下一次追加之前会被重复交出。
	if again := session.Events(); &again[0] != &before[0] {
		t.Fatal("同一份快照该被复用")
	}
	if _, err := session.Append(assistantEvent(t, 1, 1, "在")); err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 {
		t.Fatalf("调用方手里那份被追加长了：%#v", before)
	}
	if len(session.Events()) != 2 {
		t.Fatalf("新的快照是 %#v", session.Events())
	}
}

func TestRequestHeaderFoldsIncrementally(t *testing.T) {
	session, err := NewSession("s", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := session.RequestHeader(); ok || err != nil {
		t.Fatalf("一条头都没有：ok=%v err=%v", ok, err)
	}
	if _, err := session.Append(headerEvent(t, "p", "m")); err != nil {
		t.Fatal(err)
	}
	header, ok, err := session.RequestHeader()
	if err != nil || !ok || header.Config.Model != "m" {
		t.Fatalf("折出来的是 %#v ok=%v err=%v", header, ok, err)
	}
	// 再读一次不重折，结果一样。
	if again, ok, err := session.RequestHeader(); err != nil || !ok || again.Config.Model != "m" {
		t.Fatalf("第二次读是 %#v ok=%v err=%v", again, ok, err)
	}
}

func TestRequestContextFoldsIncrementally(t *testing.T) {
	session, err := NewSession("s", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := session.RequestContext(); ok || err != nil {
		t.Fatalf("一条都没有：ok=%v err=%v", ok, err)
	}
	// 中间夹一条别的类型，确认折叠只挑 request/context。
	if _, err := session.Append(turnStart(1)); err != nil {
		t.Fatal(err)
	}
	routed := sessionlog.RequestContext{Provider: "p", Model: "m", ContextWindow: 128}
	if _, err := session.Append(sessionlog.Event{
		Type: sessionlog.EventRequestContext,
		Data: data(t, sessionlog.RequestContextData{RequestContext: routed}),
	}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := session.RequestContext()
	if err != nil || !ok || got != routed {
		t.Fatalf("折出来的是 %#v ok=%v err=%v", got, ok, err)
	}
	if again, ok, _ := session.RequestContext(); !ok || again != routed {
		t.Fatalf("第二次读是 %#v", again)
	}

	if _, err := session.Append(sessionlog.Event{
		Type: sessionlog.EventRequestContext, Data: json.RawMessage(`[]`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := session.RequestContext(); !errors.Is(err, ErrInvalidAppend) {
		t.Fatalf("负载读不回来该报出来：%v", err)
	}
}

func TestDeriveMessagesProjectsTheSurfaceAndCaches(t *testing.T) {
	session, err := NewSession("s", Options{})
	if err != nil {
		t.Fatal(err)
	}
	// 一条不上表面的边界事件不进派生历史。
	if _, err := session.Append(turnStart(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Append(userEvent(t, "你好")); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Append(assistantEvent(t, 1, 1, "在")); err != nil {
		t.Fatal(err)
	}
	// 一条内容为空的助手消息在表面上，却派生不出消息。
	if _, err := session.Append(assistantEvent(t, 1, 2, "")); err != nil {
		t.Fatal(err)
	}

	messages, err := session.DeriveMessages()
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].Role != llm.RoleUser || messages[1].Role != llm.RoleAssistant {
		t.Fatalf("派生历史是 %#v", messages)
	}
	// 交回的切片每次都是新的。
	again, err := session.DeriveMessages()
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 2 || &again[0] == &messages[0] {
		t.Fatal("每次该交出一份新切片")
	}

	// 挂在会话上的那一面就是词汇那一半的同一个函数。
	message, ok, err := session.DeriveEventMessage(session.Events()[1])
	if err != nil || !ok || message.Role != llm.RoleUser {
		t.Fatalf("投影一条事件得到 %#v ok=%v err=%v", message, ok, err)
	}
}

func TestDeriveMessagesReportsAnUnprojectableSurfaceNode(t *testing.T) {
	// 追加路径不验消息形状（那是 seed 边界的事），所以一条负载坏掉的表面事件进得去，
	// 到派生时才报出来。
	session, err := NewSession("s", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Append(sessionlog.Event{
		Type:      sessionlog.EventAssistantMessage,
		Data:      json.RawMessage(`{"turn":1,"step":1,"message":7}`),
		SurfaceOp: sessionlog.AppendOp{},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.DeriveMessages(); err == nil {
		t.Fatal("投影不出来该报错")
	}
}

func TestASurfaceRewriteRebuildsTheDerivedHistory(t *testing.T) {
	session, err := NewSession("s", Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []sessionlog.Event{
		userEvent(t, "第一句"), userEvent(t, "第二句"),
	} {
		if _, err := session.Append(event); err != nil {
			t.Fatal(err)
		}
	}
	if messages, err := session.DeriveMessages(); err != nil || len(messages) != 2 {
		t.Fatalf("压缩前是 %#v err=%v", messages, err)
	}

	compaction := assistantEvent(t, 1, 1, "前面两句的摘要")
	compaction.SurfaceOp = sessionlog.ReplaceOp{Start: 0, End: 1}
	compaction.SourceEventSeqs = []int{0, 1}
	if _, err := session.Append(compaction); err != nil {
		t.Fatal(err)
	}
	if generation := session.SurfaceReplaceGeneration(); generation != 1 {
		t.Fatalf("替换代数是 %d", generation)
	}
	messages, err := session.DeriveMessages()
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Role != llm.RoleAssistant {
		t.Fatalf("被盖住的节点该从派生里消失：%#v", messages)
	}
}

// absoluteTestPath 给出一条本机上成立的绝对路径。
//
// 「绝对」在 Windows 和 POSIX 上不是一回事，所以不能写死一个字面量——这正是
// [validateSessionHeader] 用 filepath.IsAbs 而不是 path.IsAbs 的理由。
func absoluteTestPath() string {
	return testAbsolutePath
}
