// 本文件的作用：验本包那几条不变量——括号必须配对、必须归属一个说得清的位置、
// 那条替换用的检查点必须属于当时开着的那次压缩。
//
// 用例都写成一整段日志喂给 [ValidateLog]：这几条不变量都是**跨事件**的，
// 单看一条事件说不清对错。

package compaction

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ds-harness-go/llm"
	"ds-harness-go/session"
)

func TestValidateLog一次完整的回合内压缩(t *testing.T) {
	t.Parallel()

	trace, err := ValidateLog([]session.Event{
		turnStart(t, 0, 1),
		userText(t, 1, "你好"),
		compactionStart(t, 2, StartData{CompactionID: "c-1", Turn: 1}),
		compactionSummary(t, 3, summaryOf("c-1", 1)),
		checkpointReplacement(t, 4, 1, 1, CheckpointSource{CompactionID: "c-1"}),
		compactionEnd(t, 5, EndData{CompactionID: "c-1", Turn: 1}),
		turnEnd(6),
	})
	if err != nil {
		t.Fatalf("这段日志该是合规的：%v", err)
	}
	if trace.IsCompacting {
		t.Fatal("走完之后还有压缩开着")
	}
	if trace.TurnIsOpen {
		t.Fatal("走完之后还有回合开着")
	}
}

func TestValidateLog一次独立压缩(t *testing.T) {
	t.Parallel()

	// 两个回合之间的一次人工事务：turn 排成 null，回合必须是关着的。
	_, err := ValidateLog([]session.Event{
		turnStart(t, 0, 1),
		userText(t, 1, "你好"),
		turnEnd(2),
		compactionStart(t, 3, StartData{CompactionID: "c-1", SourceCommandID: "cmd-1", Standalone: true}),
		compactionSummary(t, 4, func() SummaryData {
			data := summaryOf("c-1", 1)
			data.SourceCommandID = "cmd-1"
			return data
		}()),
		checkpointReplacement(t, 5, 1, 1,
			CheckpointSource{CompactionID: "c-1", SourceCommandID: "cmd-1"}),
		compactionEnd(t, 6, EndData{CompactionID: "c-1", SourceCommandID: "cmd-1", Standalone: true}),
	})
	if err != nil {
		t.Fatalf("这段日志该是合规的：%v", err)
	}
}

func TestValidateLog一次失败的压缩不需要摘要(t *testing.T) {
	t.Parallel()

	_, err := ValidateLog([]session.Event{
		turnStart(t, 0, 1),
		compactionStart(t, 1, StartData{CompactionID: "c-1", Turn: 1}),
		compactionEnd(t, 2, EndData{CompactionID: "c-1", Turn: 1, Error: "上游超时"}),
		turnEnd(3),
	})
	if err != nil {
		t.Fatalf("这段日志该是合规的：%v", err)
	}
}

func TestValidateLog回合边界不许跨过开着的压缩(t *testing.T) {
	t.Parallel()

	// 守的是「一次压缩改的是哪一段」说得清：压缩期间开一个新回合，
	// 那次压缩换掉的范围就横跨两个回合，而它的 end 只报得出一个归属。
	for name, closer := range map[string]session.Event{
		"开了一个新回合": turnStart(t, 3, 2),
		"关掉了当前回合": turnEnd(3),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := ValidateLog([]session.Event{
				turnStart(t, 0, 1),
				userText(t, 1, "你好"),
				compactionStart(t, 2, StartData{CompactionID: "c-1", Turn: 1}),
				closer,
			})
			if !errors.Is(err, ErrInvariantViolated) {
				t.Fatalf("报的是 %v", err)
			}
			if !strings.Contains(err.Error(), "回合 1 的压缩") {
				t.Fatalf("那句话里说不清是谁挡着：%v", err)
			}
		})
	}
}

func TestValidateLog种子边界作废掉开着的括号(t *testing.T) {
	t.Parallel()

	// 一道种子边界之前的日志是继承来的，那边还开着的压缩括号在这一侧永远等不到
	// 它的 compaction/end。清掉它就是这条边界的意思。
	trace, err := ValidateLog([]session.Event{
		turnStart(t, 0, 1),
		compactionStart(t, 1, StartData{CompactionID: "c-1", Turn: 1}),
		endSeed(2),
		turnStart(t, 3, 2),
		turnEnd(4),
	})
	if err != nil {
		t.Fatalf("这段日志该是合规的：%v", err)
	}
	if trace.IsCompacting {
		t.Fatal("种子边界之后那个括号还开着")
	}
}

func TestValidateLog作废的括号不否掉修复用的回合边界(t *testing.T) {
	t.Parallel()

	// 修复用的 turn/end 排在 session/end-seed **之前**。重放到那里时，那个马上
	// 要被清掉的括号还开着；不先算一遍作废的括号的话，它会去否掉修复自己。
	trace, err := ValidateLog([]session.Event{
		turnStart(t, 0, 1),
		compactionStart(t, 1, StartData{CompactionID: "c-1", Turn: 1}),
		turnEnd(2),
		endSeed(3),
		turnStart(t, 4, 2),
	})
	if err != nil {
		t.Fatalf("这段日志该是合规的：%v", err)
	}
	if !trace.TurnIsOpen || trace.OpenTurn != 2 {
		t.Fatalf("走完之后开着的是回合 %d（TurnIsOpen=%v）", trace.OpenTurn, trace.TurnIsOpen)
	}
}

func TestValidateLog等得到end的括号不算作废(t *testing.T) {
	t.Parallel()

	// orphanStartSeqs 只挑「开着的时候撞上一条 session/end-seed」的那些。
	// 一个正常收口了的括号不在那张表里，所以它仍然否得掉回合边界。
	_, err := ValidateLog([]session.Event{
		turnStart(t, 0, 1),
		compactionStart(t, 1, StartData{CompactionID: "c-1", Turn: 1}),
		compactionSummary(t, 2, summaryOf("c-1", 3)),
		compactionEnd(t, 3, EndData{CompactionID: "c-1", Turn: 1}),
		endSeed(4),
		compactionStart(t, 5, StartData{CompactionID: "c-2", Standalone: true}),
		turnEnd(6),
	})
	if !errors.Is(err, ErrInvariantViolated) {
		t.Fatalf("报的是 %v", err)
	}
}

func TestValidateStart的几种违规(t *testing.T) {
	t.Parallel()

	for name, item := range map[string]struct {
		events []session.Event
		want   string
	}{
		"身份是空的": {
			[]session.Event{
				turnStart(t, 0, 1),
				compactionStart(t, 1, StartData{Turn: 1}),
			},
			"compactionId",
		},
		"上一次还没做完": {
			[]session.Event{
				turnStart(t, 0, 1),
				compactionStart(t, 1, StartData{CompactionID: "c-1", Turn: 1}),
				compactionStart(t, 2, StartData{CompactionID: "c-2", Turn: 1}),
			},
			"还没做完",
		},
		"自称独立事务却有回合开着": {
			[]session.Event{
				turnStart(t, 0, 1),
				compactionStart(t, 1, StartData{CompactionID: "c-1", Standalone: true}),
			},
			"独立事务",
		},
		"归属某个回合却在回合之外": {
			[]session.Event{
				compactionStart(t, 0, StartData{CompactionID: "c-1", Turn: 1}),
			},
			"开着的回合之外",
		},
		"说的回合和开着的不是一个": {
			[]session.Event{
				turnStart(t, 0, 1),
				compactionStart(t, 1, StartData{CompactionID: "c-1", Turn: 7}),
			},
			"开着的却是回合 1",
		},
		"负载读不回来": {
			[]session.Event{
				logEventAt(0, EventCompactionStart, json.RawMessage(`{"compactionId":"c-1"}`)),
			},
			"turn",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := ValidateLog(item.events)
			if err == nil {
				t.Fatal("该报的没报")
			}
			if !strings.Contains(err.Error(), item.want) {
				t.Fatalf("报的是 %v，要的是一句提到 %q 的", err, item.want)
			}
		})
	}
}

func TestValidateSummary的几种违规(t *testing.T) {
	t.Parallel()

	opened := []session.Event{
		turnStart(t, 0, 1),
		compactionStart(t, 1, StartData{CompactionID: "c-1", SourceCommandID: "cmd-1", Turn: 1}),
	}
	withOpen := func(events ...session.Event) []session.Event {
		return append(append([]session.Event{}, opened...), events...)
	}

	shadowedMismatch := summaryOf("c-1", 4, 5)
	shadowedMismatch.SourceCommandID = "cmd-1"
	shadowedMismatch.ShadowedRange = ShadowedRange{Start: 4, End: 9}

	emptyShadowed := summaryOf("c-1", 4)
	emptyShadowed.SourceCommandID = "cmd-1"
	emptyShadowed.ShadowedSeqs = nil

	negativeTokens := summaryOf("c-1", 4)
	negativeTokens.SourceCommandID = "cmd-1"
	negativeTokens.ShadowedTokenCount = -1

	wrongCommand := summaryOf("c-1", 4)
	wrongCommand.SourceCommandID = "cmd-9"

	for name, item := range map[string]struct {
		events []session.Event
		want   string
	}{
		"没有配对的 start": {
			[]session.Event{compactionSummary(t, 0, summaryOf("c-1", 4))},
			"没有配对的 compaction/start",
		},
		"身份是空的": {
			withOpen(compactionSummary(t, 2, summaryOf("", 4))),
			"compactionId",
		},
		"身份对不上": {
			withOpen(compactionSummary(t, 2, summaryOf("c-9", 4))),
			"报的 compactionId",
		},
		"发起命令对不上": {
			withOpen(compactionSummary(t, 2, wrongCommand)),
			"sourceCommandId",
		},
		"第二条摘要": {
			withOpen(
				compactionSummary(t, 2, func() SummaryData {
					data := summaryOf("c-1", 4)
					data.SourceCommandID = "cmd-1"
					return data
				}()),
				compactionSummary(t, 3, func() SummaryData {
					data := summaryOf("c-1", 5)
					data.SourceCommandID = "cmd-1"
					return data
				}()),
			),
			"第二条",
		},
		"被遮节点是空的": {
			withOpen(compactionSummary(t, 2, emptyShadowed)),
			"不能是空的",
		},
		"头尾对不上区间": {
			withOpen(compactionSummary(t, 2, shadowedMismatch)),
			"shadowedRange",
		},
		"估价是负数": {
			withOpen(compactionSummary(t, 2, negativeTokens)),
			"不能是负数",
		},
		"负载读不回来": {
			withOpen(logEventAt(2, EventCompactionSummary, json.RawMessage(`[]`))),
			"读不回来",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := ValidateLog(item.events)
			if err == nil {
				t.Fatal("该报的没报")
			}
			if !strings.Contains(err.Error(), item.want) {
				t.Fatalf("报的是 %v，要的是一句提到 %q 的", err, item.want)
			}
		})
	}
}

func TestValidateSummary归属在做的过程中变了(t *testing.T) {
	t.Parallel()

	// 摘要和结束那两步复核的是**当前开着的那次压缩**报的归属，而不是事件自己带的。
	// 正常情况下回合状态在一个压缩括号期间是冻住的（回合边界跨不过去），
	// 唯一能走到这里的路是一段继承来的日志：那里的括号已经作废，回合边界因此放行，
	// 于是括号还开着、回合号却已经换了一个。
	_, err := ValidateLog([]session.Event{
		turnStart(t, 0, 1),
		compactionStart(t, 1, StartData{CompactionID: "c-1", Turn: 1}),
		turnEnd(2),
		turnStart(t, 3, 2),
		compactionSummary(t, 4, summaryOf("c-1", 6)),
		endSeed(5),
	})
	if !errors.Is(err, ErrInvariantViolated) {
		t.Fatalf("报的是 %v", err)
	}
	if !strings.Contains(err.Error(), "说的是回合 1，开着的却是回合 2") {
		t.Fatalf("那句话里说不清哪里不对：%v", err)
	}
}

func TestValidateEnd的几种违规(t *testing.T) {
	t.Parallel()

	opened := func() []session.Event {
		return []session.Event{
			turnStart(t, 0, 1),
			compactionStart(t, 1, StartData{CompactionID: "c-1", Turn: 1}),
			compactionSummary(t, 2, summaryOf("c-1", 4)),
		}
	}

	for name, item := range map[string]struct {
		events []session.Event
		want   string
	}{
		"没有配对的 start": {
			[]session.Event{compactionEnd(t, 0, EndData{CompactionID: "c-1", Standalone: true})},
			"没有配对的 compaction/start",
		},
		"身份是空的": {
			append(opened(), compactionEnd(t, 3, EndData{Turn: 1})),
			"compactionId",
		},
		"身份对不上": {
			append(opened(), compactionEnd(t, 3, EndData{CompactionID: "c-9", Turn: 1})),
			"报的 compactionId",
		},
		"归属和 start 不一致": {
			append(opened(), compactionEnd(t, 3, EndData{CompactionID: "c-1", Standalone: true})),
			"compaction/start 归属的是",
		},
		"成功却没有摘要": {
			[]session.Event{
				turnStart(t, 0, 1),
				compactionStart(t, 1, StartData{CompactionID: "c-1", Turn: 1}),
				compactionEnd(t, 2, EndData{CompactionID: "c-1", Turn: 1}),
			},
			"没有配对的 compaction/summary",
		},
		"负载读不回来": {
			append(opened(), logEventAt(3, EventCompactionEnd, json.RawMessage(`{"compactionId":"c-1"}`))),
			"turn",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := ValidateLog(item.events)
			if err == nil {
				t.Fatal("该报的没报")
			}
			if !strings.Contains(err.Error(), item.want) {
				t.Fatalf("报的是 %v，要的是一句提到 %q 的", err, item.want)
			}
		})
	}
}

func TestValidateCheckpoint的几种违规(t *testing.T) {
	t.Parallel()

	opened := []session.Event{
		turnStart(t, 0, 1),
		userText(t, 1, "你好"),
		compactionStart(t, 2, StartData{CompactionID: "c-1", SourceCommandID: "cmd-1", Turn: 1}),
	}
	withOpen := func(event session.Event) []session.Event {
		return append(append([]session.Event{}, opened...), event)
	}

	for name, item := range map[string]struct {
		events []session.Event
		want   string
	}{
		"没有开着的压缩": {
			[]session.Event{
				userText(t, 0, "你好"),
				checkpointReplacement(t, 1, 0, 0, CheckpointSource{CompactionID: "c-1"}),
			},
			"没有配对的 compaction/start",
		},
		"身份对不上": {
			withOpen(checkpointReplacement(t, 3, 1, 1,
				CheckpointSource{CompactionID: "c-9", SourceCommandID: "cmd-1"})),
			"报的 compactionId",
		},
		"发起命令对不上": {
			withOpen(checkpointReplacement(t, 3, 1, 1,
				CheckpointSource{CompactionID: "c-1", SourceCommandID: "cmd-9"})),
			"sourceCommandId",
		},
		"身份是空的": {
			// 认得出是检查点、身份却缺了：判定只看产出方名字，所以走得到这里。
			withOpen(replacementWithSource(t, 3, 1, 1, llm.PluginSource{Plugin: CheckpointPlugin})),
			"不能是空的",
		},
		"出处读不回来": {
			// 是个合法的 JSON 对象（[llm.PluginSource] 只查到这一层），
			// 但本包的两个字段读不成想要的类型。
			withOpen(replacementWithSource(t, 3, 1, 1, llm.PluginSource{
				Plugin: CheckpointPlugin,
				Extra:  json.RawMessage(`{"compactionId":42}`),
			})),
			"读不回来",
		},
		"用户消息负载读不回来": {
			func() []session.Event {
				broken := eventAt(3, session.EventUserMessage, json.RawMessage(`{"message":42}`))
				broken.SurfaceOp = session.ReplaceOp{Start: 1, End: 1}
				return withOpen(broken)
			}(),
			"读不回来",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := ValidateLog(item.events)
			if err == nil {
				t.Fatal("该报的没报")
			}
			if !strings.Contains(err.Error(), item.want) {
				t.Fatalf("报的是 %v，要的是一句提到 %q 的", err, item.want)
			}
		})
	}
}

func TestValidateCheckpoint别的层做的替换原样放过(t *testing.T) {
	t.Parallel()

	// 认不认得出是本包的事，认出来之后合不合规才是不变量的事。别的产出方盖的
	// 替换在这里一律放行，哪怕当时压根没有压缩开着。
	_, err := ValidateLog([]session.Event{
		turnStart(t, 0, 1),
		userText(t, 1, "你好"),
		replacementWithSource(t, 2, 1, 1, llm.PluginSource{Plugin: "spill"}),
		turnEnd(3),
	})
	if err != nil {
		t.Fatalf("这段日志该是合规的：%v", err)
	}
}

func TestValidateCheckpoint追加进来的用户消息不查(t *testing.T) {
	t.Parallel()

	// 只有 ReplaceOp 那种才是替换。一条普通的用户消息在压缩期间追加进来，
	// 哪怕它盖着检查点的章，也不走这条检查——不变量守的是替换那件事。
	source, err := NewCheckpointSource(CheckpointSource{CompactionID: "c-9"})
	if err != nil {
		t.Fatalf("造不出来：%v", err)
	}
	appended := eventAt(3, session.EventUserMessage, marshalPayload(t, session.UserMessageData{
		Message: llm.Message{
			ID:      "x",
			Role:    llm.RoleUser,
			Content: llm.Content{llm.TextBlock{Text: "插一句"}},
			Source:  source,
		},
	}))

	if _, err := ValidateLog([]session.Event{
		turnStart(t, 0, 1),
		userText(t, 1, "你好"),
		compactionStart(t, 2, StartData{CompactionID: "c-1", Turn: 1}),
		appended,
	}); err != nil {
		t.Fatalf("这段日志该是合规的：%v", err)
	}
}

func TestValidate不改动这份账(t *testing.T) {
	t.Parallel()

	// 分成「验」和「落」两步：一条事件在发布前可能被别的监听方否决，
	// 验是纯的，扔掉一次转移不会让这份账往前走。
	var trace Trace
	transition, err := trace.Validate(turnStart(t, 0, 1))
	if err != nil {
		t.Fatalf("验不过：%v", err)
	}
	if trace.TurnIsOpen {
		t.Fatal("验的时候就把账改了")
	}

	trace.Apply(transition)
	if !trace.TurnIsOpen || trace.OpenTurn != 1 {
		t.Fatalf("落下去之后开着的是回合 %d（TurnIsOpen=%v）", trace.OpenTurn, trace.TurnIsOpen)
	}
}

func TestValidate不认识的事件原样放过(t *testing.T) {
	t.Parallel()

	var trace Trace
	if _, err := trace.Validate(logEventAt(0, session.EventRequestHeader, json.RawMessage(`{}`))); err != nil {
		t.Fatalf("报了：%v", err)
	}
}

func TestValidate回合边界的负载读不回来(t *testing.T) {
	t.Parallel()

	var trace Trace
	broken := logEventAt(0, session.EventTurnStart, json.RawMessage(`{"turn":"第一回合"}`))
	if _, err := trace.Validate(broken); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("报的是 %v", err)
	}
}

func TestPackageName和DSH的包名一致(t *testing.T) {
	t.Parallel()

	// 它是这个包在不变量注册表里的名字，改了就对不上 DSH 那一侧的记录。
	if PackageName != "@deepseek-ai/dsh-compaction" {
		t.Fatalf("包名是 %q", PackageName)
	}
}
