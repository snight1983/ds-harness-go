// 本文件的作用：一个构建认识哪些事件类型，以及读到不认识的类型时那条拒绝规则。
//
// 源: packages/core/session/src/known-event-types.ts

package session

import (
	"errors"
	"reflect"
	"slices"
	"testing"
)

func TestCoreVocabularyIsExactlyTheThirteenImplementedTypes(t *testing.T) {
	t.Parallel()

	want := []EventType{
		EventAssistantChunk,
		EventAssistantMessage,
		EventRequestContext,
		EventRequestHeader,
		EventSessionEndSeed,
		EventStepEnd,
		EventStepStart,
		EventTodoWrite,
		EventToolCall,
		EventToolResult,
		EventTurnEnd,
		EventTurnStart,
		EventUserMessage,
	}
	if !slices.IsSorted(want) {
		t.Fatalf("这份期望清单自己没排好序，先修它")
	}

	got := CoreVocabulary().Types()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("核心词汇表不对：\n想要 %v\n实际 %v", want, got)
	}
}

func TestVocabularyWithDerivesWithoutTouchingTheOriginal(t *testing.T) {
	t.Parallel()

	core := CoreVocabulary()
	extended := core.With("compaction/summary", "skill/loaded")

	if core.Knows("compaction/summary") {
		t.Fatalf("派生出来的词汇表改到了原来那个")
	}
	if !extended.Knows("compaction/summary") || !extended.Knows("skill/loaded") {
		t.Fatalf("派生出来的词汇表没认下新类型")
	}
	if !extended.Knows(EventTurnStart) {
		t.Fatalf("派生出来的词汇表把核心类型丢了")
	}
	if len(extended.Types()) != len(core.Types())+2 {
		t.Fatalf("派生之后的规模不对：%d 对 %d", len(extended.Types()), len(core.Types()))
	}

	if again := extended.With(EventTurnStart); len(again.Types()) != len(extended.Types()) {
		t.Fatalf("重复加一个已经认识的类型不该改变规模")
	}
}

func TestCheckVocabularyRefusesAnUnknownRequiredEvent(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		events  []Event
		wantErr bool
	}{
		"全是认识的类型": {
			events: []Event{
				{Type: EventTurnStart, Seq: 0},
				{Type: EventUserMessage, Seq: 1},
			},
		},
		"不认识但标了可跳过": {
			events: []Event{{Type: "plugin/note", Seq: 3, Ignorable: true}},
		},
		"不认识又没标可跳过": {
			events:  []Event{{Type: "compaction/summary", Seq: 5}},
			wantErr: true,
		},
		"认识的类型标了可跳过也照样收下": {
			events: []Event{{Type: EventTodoWrite, Seq: 1, Ignorable: true}},
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := CheckVocabulary(testCase.events, CoreVocabulary())
			if testCase.wantErr {
				if !errors.Is(err, ErrUnknownEventType) {
					t.Fatalf("想要 ErrUnknownEventType，实际 %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("不该报错：%v", err)
			}
		})
	}
}

func TestCheckVocabularyAcceptsWhatAnExtendedBuildKnows(t *testing.T) {
	t.Parallel()

	events := []Event{{Type: "compaction/summary", Seq: 5}}
	if err := CheckVocabulary(events, CoreVocabulary().With("compaction/summary")); err != nil {
		t.Fatalf("扩展过的词汇表该认下它：%v", err)
	}
}
