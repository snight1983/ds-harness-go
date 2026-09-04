// 本文件的作用：两个纯读日志函数的测试——挑素材那道筛子，和折出最新标题那次
// 反向扫描。两者都不碰服务，所以这里的日志一律是手写出来的死数据。

package sessiontitle

import (
	"errors"
	"testing"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

func TestCollectMessagesKeepsOnlyHumanText(t *testing.T) {
	sess := newSession(
		userEvent(t, "第一句"),
		// 插件注入的：是 user 角色，但不是人打的字。
		messageEvent(t, llm.PluginSource{Plugin: "p"}, llm.Content{llm.TextBlock{Text: "插件注入"}}),
		// 工具回填的：同上。
		messageEvent(t, llm.ToolSource{CallID: "c1"}, llm.Content{llm.TextBlock{Text: "工具结果"}}),
		// 一条别的类型的事件。
		stepStartEvent(t, 0, 0),
		userEvent(t, "第二句"),
	)

	messages := CollectMessages(sess.Events(), -1)
	if len(messages) != 2 {
		t.Fatalf("挑出了 %d 条，要的是 2 条：%+v", len(messages), messages)
	}
	if messages[0].Seq != 0 || messages[0].Text != "第一句" {
		t.Fatalf("第一条是 %+v", messages[0])
	}
	if messages[1].Seq != 4 || messages[1].Text != "第二句" {
		t.Fatalf("第二条是 %+v", messages[1])
	}
}

func TestCollectMessagesRejectsMessagesWithNoVisibleText(t *testing.T) {
	sess := newSession(
		// 只有附件，一块文本都没有。
		messageEvent(t, llm.UserSource{}, llm.Content{llm.ToolCallBlock{ID: "c1", Name: "t"}}),
		// 有文本，但洗完什么都不剩。
		userEvent(t, "\x1b[31m\x1b[0m"),
		// 空内容。
		messageEvent(t, llm.UserSource{}, nil),
	)

	if messages := CollectMessages(sess.Events(), -1); len(messages) != 0 {
		t.Fatalf("挑出了 %d 条，一条都不该挑出来：%+v", len(messages), messages)
	}
}

func TestCollectMessagesKeepsRawTextNotNormalized(t *testing.T) {
	// 归一化只用来判「这条消息有没有可用的文本」。交给生成器的必须是用户真正
	// 打的那些字——一段被折过空白的东西会让模型看到的和用户打的对不上。
	sess := newSession(userEvent(t, "  前   后  "))

	messages := CollectMessages(sess.Events(), -1)
	if len(messages) != 1 {
		t.Fatalf("挑出了 %d 条，要的是 1 条", len(messages))
	}
	if messages[0].Text != "  前   后  " {
		t.Fatalf("交出来的是 %q，要的是原文", messages[0].Text)
	}
}

func TestCollectMessagesJoinsTextBlocksWithNewline(t *testing.T) {
	// 多个文本块在原始输入里本来就是分段的，拿空串粘会把两段黏成一个词。
	sess := newSession(messageEvent(t, llm.UserSource{}, llm.Content{
		llm.TextBlock{Text: "第一段"},
		llm.TextBlock{Text: "第二段"},
	}))

	messages := CollectMessages(sess.Events(), -1)
	if len(messages) != 1 || messages[0].Text != "第一段\n第二段" {
		t.Fatalf("交出来的是 %+v", messages)
	}
}

func TestCollectMessagesHonorsInclusiveBound(t *testing.T) {
	sess := newSession(userEvent(t, "一"), userEvent(t, "二"), userEvent(t, "三"))

	// 边界含端点：throughSeq 是 1 时，seq 0 和 1 都在里面。
	if messages := CollectMessages(sess.Events(), 1); len(messages) != 2 {
		t.Fatalf("边界 1 挑出了 %d 条，要的是 2 条", len(messages))
	}
	if messages := CollectMessages(sess.Events(), 0); len(messages) != 1 {
		t.Fatalf("边界 0 挑出了 %d 条，要的是 1 条", len(messages))
	}
	// 负数表示不设边界。
	if messages := CollectMessages(sess.Events(), -1); len(messages) != 3 {
		t.Fatalf("不设边界挑出了 %d 条，要的是 3 条", len(messages))
	}
}

func TestCollectMessagesSkipsUnreadablePayload(t *testing.T) {
	// 一条读不回来的用户消息在「能不能拿来起名」这个问题上的答案就是「不能」，
	// 而不是让整条读链失败。
	broken := sessionlog.Event{Type: sessionlog.EventUserMessage, Data: []byte(`{`)}
	sess := newSession(broken, userEvent(t, "好的那条"))

	messages := CollectMessages(sess.Events(), -1)
	if len(messages) != 1 || messages[0].Text != "好的那条" {
		t.Fatalf("挑出来的是 %+v", messages)
	}
}

func TestFoldSnapshotEmptyLog(t *testing.T) {
	snapshot, ok, err := FoldSnapshot(nil)
	if err != nil {
		t.Fatalf("折叠报错：%v", err)
	}
	if ok {
		t.Fatalf("空日志折出了标题：%+v", snapshot)
	}
}

func TestFoldSnapshotTakesTheLastOne(t *testing.T) {
	sess := newSession(
		userEvent(t, "一"),
		titleEvent(t, EventData{Title: "旧", MessageSeqs: []int{0}, Source: Source{Kind: SourceFallback}}),
		titleEvent(t, EventData{Title: "新", MessageSeqs: nil, Source: Source{Kind: SourceUser}}),
	)

	snapshot, ok, err := FoldSnapshot(sess.Events())
	if err != nil || !ok {
		t.Fatalf("折出来是 ok=%v err=%v", ok, err)
	}
	if snapshot.Title != "新" || snapshot.Source.Kind != SourceUser || snapshot.EventSeq != 2 {
		t.Fatalf("折出来的是 %+v", snapshot)
	}
}

func TestFoldSnapshotCarriesEnvelopeFacts(t *testing.T) {
	event := titleEvent(t, EventData{
		Title:       "名字",
		MessageSeqs: []int{0},
		Source: Source{
			Kind:     SourceProvider,
			Provider: "p1",
			Model:    &ModelProvenance{Provider: "prov", Model: "mod"},
		},
	})
	event.Seq = 7
	event.Time = 12345

	snapshot, ok, err := FoldSnapshot([]sessionlog.Event{event})
	if err != nil || !ok {
		t.Fatalf("折出来是 ok=%v err=%v", ok, err)
	}
	if snapshot.EventSeq != 7 || snapshot.UpdatedAt != 12345 {
		t.Fatalf("信封事实是 seq=%d time=%d", snapshot.EventSeq, snapshot.UpdatedAt)
	}
	if snapshot.Source.Model == nil || snapshot.Source.Model.Model != "mod" {
		t.Fatalf("模型出处是 %+v", snapshot.Source.Model)
	}
}

func TestFoldSnapshotCopiesSoCallersCannotMutateIt(t *testing.T) {
	sess := newSession(titleEvent(t, EventData{
		Title:       "名字",
		MessageSeqs: []int{3},
		Source:      Source{Kind: SourceProvider, Provider: "p1", Model: &ModelProvenance{Provider: "a", Model: "b"}},
	}))

	first, _, err := FoldSnapshot(sess.Events())
	if err != nil {
		t.Fatalf("折叠报错：%v", err)
	}
	first.MessageSeqs[0] = 999
	first.Source.Model.Model = "被改了"

	second, _, err := FoldSnapshot(sess.Events())
	if err != nil {
		t.Fatalf("折叠报错：%v", err)
	}
	if second.MessageSeqs[0] != 3 || second.Source.Model.Model != "b" {
		t.Fatalf("第二次折出来被第一次改到了：%+v", second)
	}
}

// 一条读不回来的标题事件必须让整次折叠失败，而不是被跳过去露出**更早**那个标题。
// 露出旧标题是最坏的一种降级：界面上会显示一个看起来完全正常、但其实已经被改过的
// 名字，没有任何迹象说它是错的。
func TestFoldSnapshotFailsOnUnreadableTitleEvent(t *testing.T) {
	sess := newSession(
		titleEvent(t, EventData{Title: "旧", MessageSeqs: []int{0}, Source: Source{Kind: SourceFallback}}),
		sessionlog.Event{Type: EventSessionTitle, Data: []byte(`{`)},
	)

	_, ok, err := FoldSnapshot(sess.Events())
	if err == nil {
		t.Fatalf("读不回来却折成功了，ok=%v", ok)
	}
	if ok {
		t.Fatalf("失败时第二个返回值该是假")
	}
}

func TestCheckEventDataBindsSeqsToSource(t *testing.T) {
	tests := []struct {
		name    string
		data    EventData
		wantErr bool
	}{
		{"兜底引了一条", EventData{Title: "t", MessageSeqs: []int{0}, Source: Source{Kind: SourceFallback}}, false},
		{"生成器引了一条", EventData{Title: "t", MessageSeqs: []int{0}, Source: Source{Kind: SourceProvider}}, false},
		{"用户改名一条不引", EventData{Title: "t", Source: Source{Kind: SourceUser}}, false},
		{"兜底一条都没引", EventData{Title: "t", Source: Source{Kind: SourceFallback}}, true},
		{"用户改名却引了", EventData{Title: "t", MessageSeqs: []int{0}, Source: Source{Kind: SourceUser}}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := CheckEventData(test.data)
			if test.wantErr != (err != nil) {
				t.Fatalf("要 err=%v，得到 %v", test.wantErr, err)
			}
		})
	}
}

func TestEventTypesListsWhatThisPackageWrites(t *testing.T) {
	types := EventTypes()
	if len(types) != 1 || types[0] != EventSessionTitle {
		t.Fatalf("交出来的事件类型是 %v", types)
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name   string
		config Config
		wantOK bool
	}{
		{"齐的", Config{FallbackMaxWords: 5, FallbackMaxBytes: 40, MaxTitleBytes: 80}, true},
		{"词数漏填", Config{FallbackMaxBytes: 40, MaxTitleBytes: 80}, false},
		{"兜底字节漏填", Config{FallbackMaxWords: 5, MaxTitleBytes: 80}, false},
		{"总上限漏填", Config{FallbackMaxWords: 5, FallbackMaxBytes: 40}, false},
		{"兜底比总上限还大", Config{FallbackMaxWords: 5, FallbackMaxBytes: 100, MaxTitleBytes: 80}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.config.Validate()
			if test.wantOK != (err == nil) {
				t.Fatalf("要 ok=%v，得到 %v", test.wantOK, err)
			}
			if err != nil && !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("报的错不是 ErrInvalidConfig：%v", err)
			}
		})
	}
}

func TestSourceCloneDetachesTheModelPointer(t *testing.T) {
	original := Source{Kind: SourceProvider, Provider: "p", Model: &ModelProvenance{Provider: "a", Model: "b"}}
	copied := original.Clone()
	copied.Model.Model = "被改了"

	if original.Model.Model != "b" {
		t.Fatalf("改到原件上了：%+v", original.Model)
	}
}

func TestSourceOmitsEmptyOptionalFields(t *testing.T) {
	// 两个可选字段带 omitempty，排出去的字节要和 DSH 那三支逐字一致。
	raw := mustJSON(t, Source{Kind: SourceUser})
	if string(raw) != `{"kind":"user"}` {
		t.Fatalf("排出来的是 %s", raw)
	}
}
