// 本文件的作用：模型可见那条表面的折叠规则——谁上表面、谁盖掉谁、来源怎么验。
//
// 源: packages/core/session/src/surface.ts

package session

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"ds-harness-go/llm"
)

func TestSurfaceEligibilityIsTheThreeMessageTypes(t *testing.T) {
	t.Parallel()

	cases := map[EventType]bool{
		EventUserMessage:      true,
		EventAssistantMessage: true,
		EventToolResult:       true,
		EventAssistantChunk:   false,
		EventTurnStart:        false,
		EventTodoWrite:        false,
		"compaction/summary":  false,
	}

	for kind, want := range cases {
		if got := IsSurfaceEligibleType(kind); got != want {
			t.Fatalf("%q 够不够格上表面判错了：想要 %v，实际 %v", kind, want, got)
		}
	}
}

func TestSurfaceMembershipNeedsBothTheTypeAndTheMark(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		event                        Event
		onSurface, append_, replaced bool
	}{
		"够格又带了追加标记": {
			event:     Event{Type: EventUserMessage, SurfaceOp: AppendOp{}},
			onSurface: true, append_: true,
		},
		"够格又带了替换标记": {
			event:     Event{Type: EventToolResult, SurfaceOp: ReplaceOp{Start: 1, End: 1}},
			onSurface: true, replaced: true,
		},
		"够格但没带标记":    {event: Event{Type: EventUserMessage}},
		"带了标记但类型不够格": {event: Event{Type: EventTurnStart, SurfaceOp: AppendOp{}}},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := IsSurfaceEvent(testCase.event); got != testCase.onSurface {
				t.Fatalf("在不在表面上判错了：想要 %v，实际 %v", testCase.onSurface, got)
			}
			if got := IsAppendSurfaceEvent(testCase.event); got != testCase.append_ {
				t.Fatalf("是不是追加进来的判错了：想要 %v，实际 %v", testCase.append_, got)
			}
			if got := IsReplacementSurfaceEvent(testCase.event); got != testCase.replaced {
				t.Fatalf("是不是一次替换判错了：想要 %v，实际 %v", testCase.replaced, got)
			}
		})
	}
}

func TestDeriveEventMessageProjectsOnlyTheSurfaceTypes(t *testing.T) {
	t.Parallel()

	t.Run("用户消息原样投影", func(t *testing.T) {
		t.Parallel()

		event := userMessageEvent(t, 0, "hi")
		got, ok, err := DeriveEventMessage(event)
		if err != nil || !ok {
			t.Fatalf("该投影出一条消息：ok=%v err=%v", ok, err)
		}
		if got.Role != llm.RoleUser || len(got.Content) != 1 {
			t.Fatalf("投影出来的消息不对：%#v", got)
		}
	})

	t.Run("助手消息原样投影", func(t *testing.T) {
		t.Parallel()

		event := assistantMessageEvent(t, 1, 1, 1, llm.Content{llm.TextBlock{Text: "ok"}})
		_, ok, err := DeriveEventMessage(event)
		if err != nil || !ok {
			t.Fatalf("该投影出一条消息：ok=%v err=%v", ok, err)
		}
	})

	t.Run("内容为空的助手消息不投影", func(t *testing.T) {
		t.Parallel()

		event := assistantMessageEvent(t, 1, 1, 1, nil)
		if _, ok, err := DeriveEventMessage(event); ok || err != nil {
			t.Fatalf("只用来挂记账的助手消息不该进历史：ok=%v err=%v", ok, err)
		}
	})

	t.Run("工具结果原样投影", func(t *testing.T) {
		t.Parallel()

		event := toolResultEvent(t, 2, 1, 1, "c1", "done")
		_, ok, err := DeriveEventMessage(event)
		if err != nil || !ok {
			t.Fatalf("该投影出一条消息：ok=%v err=%v", ok, err)
		}
	})

	t.Run("不上表面的事件投影不出消息", func(t *testing.T) {
		t.Parallel()

		for _, kind := range []EventType{EventTurnStart, EventAssistantChunk, "x/y"} {
			event := Event{Type: kind, Data: json.RawMessage(`{"turn":1,"step":1}`)}
			if _, ok, err := DeriveEventMessage(event); ok || err != nil {
				t.Fatalf("%q 不该投影出消息：ok=%v err=%v", kind, ok, err)
			}
		}
	})

	t.Run("负载坏掉时报错", func(t *testing.T) {
		t.Parallel()

		for _, kind := range []EventType{EventUserMessage, EventAssistantMessage, EventToolResult} {
			event := Event{Type: kind, Data: json.RawMessage(`7`)}
			if _, _, err := DeriveEventMessage(event); !errors.Is(err, ErrMalformedValue) {
				t.Fatalf("%q 的坏负载想要 ErrMalformedValue，实际 %v", kind, err)
			}
		}
	})
}

func TestSurfaceOpOfEnforcesTheMarkRule(t *testing.T) {
	t.Parallel()

	t.Run("够格的类型必须带标记", func(t *testing.T) {
		t.Parallel()

		_, _, err := SurfaceOpOf(Event{Type: EventUserMessage})
		if !errors.Is(err, ErrSurfaceViolation) {
			t.Fatalf("想要 ErrSurfaceViolation，实际 %v", err)
		}
	})

	t.Run("不够格的类型不许带标记", func(t *testing.T) {
		t.Parallel()

		_, _, err := SurfaceOpOf(Event{Type: EventTurnStart, SurfaceOp: AppendOp{}})
		if !errors.Is(err, ErrSurfaceViolation) {
			t.Fatalf("想要 ErrSurfaceViolation，实际 %v", err)
		}
	})

	t.Run("不够格的类型不许带来源", func(t *testing.T) {
		t.Parallel()

		_, _, err := SurfaceOpOf(Event{Type: EventTurnStart, SourceEventSeqs: []int{1}})
		if !errors.Is(err, ErrSurfaceViolation) {
			t.Fatalf("想要 ErrSurfaceViolation，实际 %v", err)
		}
	})

	t.Run("不够格又什么都没带就是不上表面", func(t *testing.T) {
		t.Parallel()

		operation, onSurface, err := SurfaceOpOf(Event{Type: EventTurnStart})
		if err != nil || onSurface || operation != nil {
			t.Fatalf("不该上表面：op=%#v onSurface=%v err=%v", operation, onSurface, err)
		}
	})
}

func TestFoldSurfaceAppendsInLogOrder(t *testing.T) {
	t.Parallel()

	events := []Event{
		userMessageEvent(t, 0, "hi"),
		{Type: EventTurnStart, Seq: 1, Data: json.RawMessage(`{"turn":1}`)},
		assistantMessageEvent(t, 2, 1, 1, llm.Content{llm.TextBlock{Text: "ok"}}),
		toolResultEvent(t, 3, 1, 1, "c1", "done"),
	}

	got, err := FoldSurface(events)
	if err != nil {
		t.Fatalf("折不出来：%v", err)
	}
	if !reflect.DeepEqual(got.Nodes, []int{0, 2, 3}) {
		t.Fatalf("表面上的节点不对：%v", got.Nodes)
	}
	if len(got.Replacements) != 0 {
		t.Fatalf("这段日志里没有替换，实际 %#v", got.Replacements)
	}
}

func TestFoldSurfaceShadowsTheReplacedRange(t *testing.T) {
	t.Parallel()

	replacement := userMessageEvent(t, 3, "summary")
	replacement.SurfaceOp = ReplaceOp{Start: 0, End: 2}
	replacement.SourceEventSeqs = []int{0, 1, 2}

	events := []Event{
		userMessageEvent(t, 0, "a"),
		assistantMessageEvent(t, 1, 1, 1, llm.Content{llm.TextBlock{Text: "b"}}),
		userMessageEvent(t, 2, "c"),
		replacement,
		userMessageEvent(t, 4, "after"),
	}

	got, err := FoldSurface(events)
	if err != nil {
		t.Fatalf("折不出来：%v", err)
	}
	if !reflect.DeepEqual(got.Nodes, []int{3, 4}) {
		t.Fatalf("被盖掉的节点没走：%v", got.Nodes)
	}
	if len(got.Replacements) != 1 {
		t.Fatalf("该记下一次替换，实际 %d 次", len(got.Replacements))
	}
	want := SurfaceFoldReplacement{Seq: 3, Start: 0, End: 2, ShadowedSeqs: []int{0, 1, 2}}
	if !reflect.DeepEqual(got.Replacements[0], want) {
		t.Fatalf("替换记录不对：\n想要 %#v\n实际 %#v", want, got.Replacements[0])
	}
}

func TestFoldSurfaceRejectsBrokenReplacements(t *testing.T) {
	t.Parallel()

	base := func() []Event {
		return []Event{
			userMessageEvent(t, 0, "a"),
			userMessageEvent(t, 1, "b"),
		}
	}

	cases := map[string]func() []Event{
		"起点不在表面上": func() []Event {
			third := userMessageEvent(t, 2, "s")
			third.SurfaceOp = ReplaceOp{Start: 7, End: 1}
			third.SourceEventSeqs = []int{7, 1}
			return append(base(), third)
		},
		"终点不在表面上": func() []Event {
			third := userMessageEvent(t, 2, "s")
			third.SurfaceOp = ReplaceOp{Start: 0, End: 7}
			third.SourceEventSeqs = []int{0, 7}
			return append(base(), third)
		},
		"起点排在终点后面": func() []Event {
			third := userMessageEvent(t, 2, "s")
			third.SurfaceOp = ReplaceOp{Start: 1, End: 0}
			third.SourceEventSeqs = []int{0, 1}
			return append(base(), third)
		},
		"来源没把被盖掉的都列出来": func() []Event {
			third := userMessageEvent(t, 2, "s")
			third.SurfaceOp = ReplaceOp{Start: 0, End: 1}
			third.SourceEventSeqs = []int{0}
			return append(base(), third)
		},
		"来源里有负数": func() []Event {
			third := userMessageEvent(t, 2, "s")
			third.SurfaceOp = ReplaceOp{Start: 0, End: 1}
			third.SourceEventSeqs = []int{-1, 0, 1}
			return append(base(), third)
		},
		"来源里有重复": func() []Event {
			third := userMessageEvent(t, 2, "s")
			third.SurfaceOp = ReplaceOp{Start: 0, End: 1}
			third.SourceEventSeqs = []int{0, 0, 1}
			return append(base(), third)
		},
		"来源引用了不比自己早的事件": func() []Event {
			third := userMessageEvent(t, 2, "s")
			third.SurfaceOp = ReplaceOp{Start: 0, End: 1}
			third.SourceEventSeqs = []int{0, 1, 2}
			return append(base(), third)
		},
		"seq 不连续": func() []Event {
			third := userMessageEvent(t, 5, "s")
			return append(base(), third)
		},
		"只有助手消息可以给一个空的来源清单": func() []Event {
			third := userMessageEvent(t, 2, "s")
			third.SourceEventSeqs = []int{}
			return append(base(), third)
		},
	}

	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := FoldSurface(build()); !errors.Is(err, ErrSurfaceViolation) {
				t.Fatalf("想要 ErrSurfaceViolation，实际 %v", err)
			}
		})
	}
}

func TestAssistantMessageMayDeclareAnEmptySourceList(t *testing.T) {
	t.Parallel()

	message := assistantMessageEvent(t, 0, 1, 1, llm.Content{llm.TextBlock{Text: "ok"}})
	message.SourceEventSeqs = []int{}

	if _, err := FoldSurface([]Event{message}); err != nil {
		t.Fatalf("助手消息可以声明一个已知为空的来源清单：%v", err)
	}
}

func TestToolResultRewriteMayOnlyChangeTheContent(t *testing.T) {
	t.Parallel()

	original := toolResultEvent(t, 0, 1, 1, "c1", "raw")

	rewriteWith := func(t *testing.T, data ToolResultData) Event {
		t.Helper()

		payload, err := json.Marshal(data)
		if err != nil {
			t.Fatalf("负载排不出去：%v", err)
		}
		return Event{
			Type: EventToolResult, Seq: 1, Data: payload,
			SurfaceOp: ReplaceOp{Start: 0, End: 0}, SourceEventSeqs: []int{0},
		}
	}

	decode := func(t *testing.T, event Event) ToolResultData {
		t.Helper()

		var data ToolResultData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			t.Fatalf("负载读不回来：%v", err)
		}
		return data
	}

	t.Run("只改内容是允许的", func(t *testing.T) {
		t.Parallel()

		data := decode(t, original)
		data.Message.Content = llm.Content{llm.ToolResultBlock{
			ToolCallID: "c1", IsError: false,
			Content: llm.Content{llm.TextBlock{Text: "trimmed"}},
		}}
		if _, err := FoldSurface([]Event{original, rewriteWith(t, data)}); err != nil {
			t.Fatalf("只改内容不该被拒：%v", err)
		}
	})

	cases := map[string]func(*ToolResultData){
		"改了回合":   func(d *ToolResultData) { d.Turn = 9 },
		"改了步骤":   func(d *ToolResultData) { d.Step = 9 },
		"加了错误身份": func(d *ToolResultData) { d.Error = &ToolError{Name: "E", Code: "X"} },
		"改了工具私有负载": func(d *ToolResultData) {
			d.Meta = json.RawMessage(`{"n":1}`)
		},
		"改了消息 id": func(d *ToolResultData) { d.Message.ID = "other" },
		"改了消息角色":  func(d *ToolResultData) { d.Message.Role = llm.RoleAssistant },
		"改了消息来路":  func(d *ToolResultData) { d.Message.Source = llm.UserSource{} },
		"改了调用 id": func(d *ToolResultData) {
			d.Message.Content = llm.Content{llm.ToolResultBlock{ToolCallID: "other"}}
		},
		"把成功改成了失败": func(d *ToolResultData) {
			d.Message.Content = llm.Content{llm.ToolResultBlock{ToolCallID: "c1", IsError: true}}
		},
		"内容块变成了两块": func(d *ToolResultData) {
			d.Message.Content = llm.Content{
				llm.ToolResultBlock{ToolCallID: "c1"},
				llm.TextBlock{Text: "x"},
			}
		},
		"内容块不再是一个工具结果": func(d *ToolResultData) {
			d.Message.Content = llm.Content{llm.TextBlock{Text: "x"}}
		},
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			data := decode(t, original)
			mutate(&data)
			_, err := FoldSurface([]Event{original, rewriteWith(t, data)})
			if !errors.Is(err, ErrSurfaceViolation) {
				t.Fatalf("想要 ErrSurfaceViolation，实际 %v", err)
			}
		})
	}
}

func TestToolResultRewriteMustPointAtASingleToolResult(t *testing.T) {
	t.Parallel()

	t.Run("盖掉的不止一个节点", func(t *testing.T) {
		t.Parallel()

		events := []Event{
			toolResultEvent(t, 0, 1, 1, "c1", "a"),
			toolResultEvent(t, 1, 1, 1, "c2", "b"),
		}
		wide := toolResultEvent(t, 2, 1, 1, "c1", "a")
		wide.SurfaceOp = ReplaceOp{Start: 0, End: 1}
		wide.SourceEventSeqs = []int{0, 1}

		if _, err := FoldSurface(append(events, wide)); !errors.Is(err, ErrSurfaceViolation) {
			t.Fatalf("想要 ErrSurfaceViolation，实际 %v", err)
		}
	})

	t.Run("盖掉的那个不是一条工具结果", func(t *testing.T) {
		t.Parallel()

		rewrite := toolResultEvent(t, 1, 1, 1, "c1", "a")
		rewrite.SurfaceOp = ReplaceOp{Start: 0, End: 0}
		rewrite.SourceEventSeqs = []int{0}

		events := []Event{userMessageEvent(t, 0, "hi"), rewrite}
		if _, err := FoldSurface(events); !errors.Is(err, ErrSurfaceViolation) {
			t.Fatalf("想要 ErrSurfaceViolation，实际 %v", err)
		}
	})
}

func TestToolResultRewriteComparesTheErrorIdentityItself(t *testing.T) {
	t.Parallel()

	// 「一边有一边没有」和「两边都有但不是同一个」是两条不同的判据，
	// 后者只有在被替换的那条本身就带错误身份时才走得到。
	withError := func(t *testing.T, seq int, code string) Event {
		t.Helper()

		event := toolResultEvent(t, seq, 1, 1, "c1", "raw")
		var data ToolResultData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			t.Fatalf("负载读不回来：%v", err)
		}
		data.Error = &ToolError{Name: "E", Code: code}
		payload, err := json.Marshal(data)
		if err != nil {
			t.Fatalf("负载排不出去：%v", err)
		}
		event.Data = payload
		return event
	}

	rewrite := withError(t, 1, "Y")
	rewrite.SurfaceOp = ReplaceOp{Start: 0, End: 0}
	rewrite.SourceEventSeqs = []int{0}

	_, err := FoldSurface([]Event{withError(t, 0, "X"), rewrite})
	if !errors.Is(err, ErrSurfaceViolation) {
		t.Fatalf("想要 ErrSurfaceViolation，实际 %v", err)
	}

	t.Run("同一个错误身份就放过去", func(t *testing.T) {
		t.Parallel()

		same := withError(t, 1, "X")
		same.SurfaceOp = ReplaceOp{Start: 0, End: 0}
		same.SourceEventSeqs = []int{0}
		if _, err := FoldSurface([]Event{withError(t, 0, "X"), same}); err != nil {
			t.Fatalf("错误身份没变不该被拒：%v", err)
		}
	})
}

func TestToolResultRewriteReportsABrokenPayloadOnEitherSide(t *testing.T) {
	t.Parallel()

	// 被替换的那条是靠追加进的表面，追加那一步不读负载，所以一条负载已经坏掉的
	// 工具结果能一路躺到这里——两边各自的读回来都得报错，不能当成「内容变了」。
	broken := func(seq int) Event {
		return Event{
			Type: EventToolResult, Seq: seq, Data: json.RawMessage(`7`),
			SurfaceOp: AppendOp{},
		}
	}

	t.Run("被替换的那条坏了", func(t *testing.T) {
		t.Parallel()

		rewrite := toolResultEvent(t, 1, 1, 1, "c1", "trimmed")
		rewrite.SurfaceOp = ReplaceOp{Start: 0, End: 0}
		rewrite.SourceEventSeqs = []int{0}
		if _, err := FoldSurface([]Event{broken(0), rewrite}); !errors.Is(err, ErrMalformedValue) {
			t.Fatalf("想要 ErrMalformedValue，实际 %v", err)
		}
	})

	t.Run("替换用的那条坏了", func(t *testing.T) {
		t.Parallel()

		rewrite := broken(1)
		rewrite.SurfaceOp = ReplaceOp{Start: 0, End: 0}
		rewrite.SourceEventSeqs = []int{0}
		original := toolResultEvent(t, 0, 1, 1, "c1", "raw")
		if _, err := FoldSurface([]Event{original, rewrite}); !errors.Is(err, ErrMalformedValue) {
			t.Fatalf("想要 ErrMalformedValue，实际 %v", err)
		}
	})
}

func TestSameMessageSourceHandlesAnAbsentSource(t *testing.T) {
	t.Parallel()

	// 从介质上读回来的来路永远不是 nil（认不出的落进 llm.UnknownSource），
	// 所以这道判空是给「负载不经 JSON 直接拼出来」的调用方兜的底。
	same, err := sameMessageSource(nil, nil)
	if err != nil || !same {
		t.Fatalf("两边都没有来路该算相同：same=%v err=%v", same, err)
	}
	same, err = sameMessageSource(llm.UserSource{}, nil)
	if err != nil || same {
		t.Fatalf("一边没有来路该算不同：same=%v err=%v", same, err)
	}
}

// replaceLikeOp 是一个本包之外造不出来的第三个表面操作变体，
// 用来走到 [planSurfaceEvent] 那条「认不得的表面操作」的断言。
type replaceLikeOp struct{}

func (replaceLikeOp) SurfaceOpKind() SurfaceOpKind { return OpReplace }

func (replaceLikeOp) sealedSurfaceOp() {}

func (replaceLikeOp) MarshalJSON() ([]byte, error) { return []byte(`"replace-like"`), nil }

func TestFoldSurfaceRefusesASurfaceOpItCannotName(t *testing.T) {
	t.Parallel()

	stray := userMessageEvent(t, 0, "a")
	stray.SurfaceOp = replaceLikeOp{}

	if _, err := FoldSurface([]Event{stray}); !errors.Is(err, ErrSurfaceViolation) {
		t.Fatalf("想要 ErrSurfaceViolation，实际 %v", err)
	}
}

func TestFoldSurfaceRefusesAnEligibleEventWithoutItsMark(t *testing.T) {
	t.Parallel()

	naked := toolResultEvent(t, 0, 1, 1, "c1", "done")
	naked.SurfaceOp = nil

	if _, err := FoldSurface([]Event{naked}); !errors.Is(err, ErrSurfaceViolation) {
		t.Fatalf("想要 ErrSurfaceViolation，实际 %v", err)
	}
}

func TestSurfaceFolderMatchesTheWholeLogFold(t *testing.T) {
	t.Parallel()

	replacement := userMessageEvent(t, 2, "summary")
	replacement.SurfaceOp = ReplaceOp{Start: 0, End: 1}
	replacement.SourceEventSeqs = []int{0, 1}

	events := []Event{
		userMessageEvent(t, 0, "a"),
		userMessageEvent(t, 1, "b"),
		replacement,
		{Type: EventTurnStart, Seq: 3, Data: json.RawMessage(`{"turn":1}`)},
	}

	whole, err := FoldSurface(events)
	if err != nil {
		t.Fatalf("整段折不出来：%v", err)
	}

	folder := NewSurfaceFolder(0)
	var replacements []SurfaceFoldReplacement
	for _, event := range events {
		if err := folder.ValidateNext(event); err != nil {
			t.Fatalf("seq %d 验不过：%v", event.Seq, err)
		}
		got, replaced, err := folder.Push(event)
		if err != nil {
			t.Fatalf("seq %d 推不进去：%v", event.Seq, err)
		}
		if replaced {
			replacements = append(replacements, got)
		}
	}

	if !reflect.DeepEqual(folder.Nodes(), whole.Nodes) {
		t.Fatalf("增量折出来的表面和整段折的不一样：%v 对 %v", folder.Nodes(), whole.Nodes)
	}
	if !reflect.DeepEqual(replacements, whole.Replacements) {
		t.Fatalf("增量折出来的替换和整段折的不一样：%#v 对 %#v", replacements, whole.Replacements)
	}
	if folder.ReplaceGeneration() != 1 {
		t.Fatalf("替换代数不对：%d", folder.ReplaceGeneration())
	}
}

func TestSurfaceFolderLeavesTheSurfaceAloneOnFailure(t *testing.T) {
	t.Parallel()

	folder := NewSurfaceFolder(0)
	if _, _, err := folder.Push(userMessageEvent(t, 0, "a")); err != nil {
		t.Fatalf("第一条推不进去：%v", err)
	}

	bad := userMessageEvent(t, 9, "b")
	if _, _, err := folder.Push(bad); !errors.Is(err, ErrSurfaceViolation) {
		t.Fatalf("想要 ErrSurfaceViolation，实际 %v", err)
	}
	if !reflect.DeepEqual(folder.Nodes(), []int{0}) {
		t.Fatalf("推失败之后表面被改了：%v", folder.Nodes())
	}

	nodes := folder.Nodes()
	nodes[0] = 99
	if folder.Nodes()[0] != 0 {
		t.Fatalf("Nodes 返回的应该是一份复制")
	}
}

func TestSurfaceFolderStartsAtItsBaseSeq(t *testing.T) {
	t.Parallel()

	folder := NewSurfaceFolder(10)
	if err := folder.ValidateNext(userMessageEvent(t, 0, "a")); !errors.Is(err, ErrSurfaceViolation) {
		t.Fatalf("从 10 起的折叠器不该收下 seq 0，实际 %v", err)
	}
	if _, _, err := folder.Push(userMessageEvent(t, 10, "a")); err != nil {
		t.Fatalf("seq 10 该收下：%v", err)
	}
	if !reflect.DeepEqual(folder.Nodes(), []int{10}) {
		t.Fatalf("表面不对：%v", folder.Nodes())
	}
}

// userMessageEvent 排一条追加进表面的用户消息事件出来。
func userMessageEvent(t *testing.T, seq int, text string) Event {
	t.Helper()

	payload, err := json.Marshal(UserMessageData{Message: llm.Message{
		ID:      llm.MessageID("u" + text),
		Role:    llm.RoleUser,
		Content: llm.Content{llm.TextBlock{Text: text}},
		Source:  llm.UserSource{},
	}})
	if err != nil {
		t.Fatalf("用户消息负载排不出去：%v", err)
	}
	return Event{Type: EventUserMessage, Seq: seq, Data: payload, SurfaceOp: AppendOp{}}
}

// assistantMessageEvent 排一条追加进表面的助手消息事件出来。
func assistantMessageEvent(t *testing.T, seq, turn, step int, content llm.Content) Event {
	t.Helper()

	payload, err := json.Marshal(AssistantMessageData{
		Turn: turn, Step: step,
		Message: llm.Message{
			ID: "a", Role: llm.RoleAssistant, Content: content,
			Source: llm.ModelSource{},
		},
	})
	if err != nil {
		t.Fatalf("助手消息负载排不出去：%v", err)
	}
	return Event{Type: EventAssistantMessage, Seq: seq, Data: payload, SurfaceOp: AppendOp{}}
}

// toolResultEvent 排一条追加进表面的工具结果事件出来。
func toolResultEvent(t *testing.T, seq, turn, step int, callID llm.CallID, text string) Event {
	t.Helper()

	payload, err := json.Marshal(ToolResultData{
		Turn: turn, Step: step,
		Message: llm.Message{
			ID: llm.MessageID("r" + string(callID)), Role: llm.RoleUser,
			Content: llm.Content{llm.ToolResultBlock{
				ToolCallID: callID,
				Content:    llm.Content{llm.TextBlock{Text: text}},
			}},
			Source: llm.ToolSource{CallID: callID},
		},
	})
	if err != nil {
		t.Fatalf("工具结果负载排不出去：%v", err)
	}
	return Event{Type: EventToolResult, Seq: seq, Data: payload, SurfaceOp: AppendOp{}}
}
