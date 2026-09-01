// 本文件的作用：验本包那条不变量——追加位置、以及一条落库读数的每一项复核。

package timecontext

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
)

// openStepAt 造一段「回合 1 步骤 1 开着、请求头还没写」的日志。
func openStepAt(t *testing.T) []session.Event {
	t.Helper()

	return []session.Event{
		turnStart(t, 1, 1),
		userText(t, 2, "你好"),
		stepStart(t, 3, 1, 1),
	}
}

func Test追加位置认出开着的回合与步骤(t *testing.T) {
	t.Parallel()

	turn, step, err := PreparationPosition(openStepAt(t))
	if err != nil {
		t.Fatalf("这段日志上该有位置，却报了 %v", err)
	}
	if turn != 1 || step != 1 {
		t.Fatalf("位置该是回合 1 步骤 1，得到回合 %d 步骤 %d", turn, step)
	}
}

func Test没有开着的回合就没有追加位置(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		events []session.Event
	}{
		{"空日志", nil},
		{"只有一条用户消息", []session.Event{userText(t, 1, "你好")}},
		{"回合已经关掉", []session.Event{turnStart(t, 1, 1), stepStart(t, 2, 1, 1), turnEnd(3)}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if _, _, err := PreparationPosition(one.events); !errors.Is(err, ErrInvariantViolated) {
				t.Fatalf("%s 该报 ErrInvariantViolated，得到 %v", one.name, err)
			}
		})
	}
}

func Test步骤没开时没有追加位置(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		events []session.Event
	}{
		{"回合刚开", []session.Event{turnStart(t, 1, 1)}},
		{"步骤已经关掉", []session.Event{turnStart(t, 1, 1), stepStart(t, 2, 1, 1), stepEnd(t, 3, 1, 1)}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			_, _, err := PreparationPosition(one.events)
			if !errors.Is(err, ErrInvariantViolated) {
				t.Fatalf("%s 该报 ErrInvariantViolated，得到 %v", one.name, err)
			}
			if !strings.Contains(err.Error(), "step/start") {
				t.Fatalf("错误该说到 step/start，得到 %v", err)
			}
		})
	}
}

func Test请求头写下去之后不能再追加读数(t *testing.T) {
	t.Parallel()

	events := append(openStepAt(t), requestHeader(4))
	_, _, err := PreparationPosition(events)
	if !errors.Is(err, ErrInvariantViolated) {
		t.Fatalf("该报 ErrInvariantViolated，得到 %v", err)
	}
	if !strings.Contains(err.Error(), "request/header") {
		t.Fatalf("错误该说到 request/header，得到 %v", err)
	}
}

func Test新的回合与新的步骤都把请求头的标记清掉(t *testing.T) {
	t.Parallel()

	// 请求头之后又开了新步骤，位置该跟着回到「可以追加」。
	events := []session.Event{
		turnStart(t, 1, 1), stepStart(t, 2, 1, 1), requestHeader(3), stepStart(t, 4, 1, 2),
	}
	turn, step, err := PreparationPosition(events)
	if err != nil || turn != 1 || step != 2 {
		t.Fatalf("位置该是回合 1 步骤 2，得到回合 %d 步骤 %d（err=%v）", turn, step, err)
	}

	// 回合边界同理，而且还得把步骤的标记一起清掉。
	events = append(events, requestHeader(5), turnEnd(6), turnStart(t, 7, 2))
	if _, _, err := PreparationPosition(events); !strings.Contains(err.Error(), "step/start") {
		t.Fatalf("新回合里还没开步骤，错误该说到 step/start，得到 %v", err)
	}
}

func Test一条合规的读数验得过(t *testing.T) {
	t.Parallel()

	shanghai := mustLoad(t, "Asia/Shanghai")
	history := openStepAt(t)
	event := readingAt(t, 4, Reading{
		Now: at(3), Turn: 1, Step: 1, Previous: at(2), HasPrevious: true,
	}, shanghai)

	if err := ValidateReading(history, event, shanghai); err != nil {
		t.Fatalf("这条读数该验得过，却报了 %v", err)
	}
}

func Test后续步骤的读数验得过(t *testing.T) {
	t.Parallel()

	history := append(openStepAt(t), requestHeader(4), stepStart(t, 5, 1, 2))
	event := readingAt(t, 6, Reading{
		Now: at(5), Turn: 1, Step: 2, Previous: at(2), HasPrevious: true,
	}, time.UTC)

	if err := ValidateReading(history, event, time.UTC); err != nil {
		t.Fatalf("这条读数该验得过，却报了 %v", err)
	}
}

func Test不是本包写的事件不该拿来验(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		event session.Event
	}{
		{"用户自己说的话", userText(t, 4, "你好")},
		{"别的插件的注入", otherPluginText(t, 4, "别人的")},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			err := ValidateReading(openStepAt(t), one.event, time.UTC)
			if !errors.Is(err, ErrInvariantViolated) {
				t.Fatalf("%s 该报 ErrInvariantViolated，得到 %v", one.name, err)
			}
		})
	}
}

func Test坏掉的负载在复核时被报出来(t *testing.T) {
	t.Parallel()

	broken := eventAt(4, session.EventUserMessage, json.RawMessage(`{`))
	if err := ValidateReading(openStepAt(t), broken, time.UTC); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("该报 ErrMalformedEvent，得到 %v", err)
	}
}

func Test读数的内容必须正好是一个文本块(t *testing.T) {
	t.Parallel()

	text := RenderText(Reading{Now: at(3), Turn: 1, Step: 1}, time.UTC)
	cases := []struct {
		name    string
		content llm.Content
	}{
		{"两个块", llm.Content{llm.TextBlock{Text: text}, llm.TextBlock{Text: "多出来的"}}},
		{"一个都没有", llm.Content{}},
		{"那一块不是文本", llm.Content{llm.ReasoningBlock{Text: text}}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			event := messageEvent(t, 4, llm.Message{
				ID:      llm.MessageID("r"),
				Role:    llm.RoleUser,
				Content: one.content,
				Source:  ReadingSource(text),
			})
			if err := ValidateReading(openStepAt(t), event, time.UTC); !errors.Is(err, ErrInvariantViolated) {
				t.Fatalf("%s 该报 ErrInvariantViolated，得到 %v", one.name, err)
			}
		})
	}
}

func Test对不上落库格式的正文被判伪造(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		text string
	}{
		{"整段不是读数", "今天天气不错"},
		{"少了第三行", "Time sampled while preparing turn 1, step 1: 1970-01-01T00:00:03+00:00[UTC]\n" +
			"Time zone for this request: UTC. Interpret otherwise-unqualified dates and times in this zone."},
		{"基线名不认识", "Time sampled while preparing turn 1, step 1: 1970-01-01T00:00:03+00:00[UTC]\n" +
			"Time zone for this request: UTC. Interpret otherwise-unqualified dates and times in this zone.\n" +
			"Elapsed since the preceding wall clock: 1s."},
		{"耗时排法不认识", "Time sampled while preparing turn 1, step 1: 1970-01-01T00:00:03+00:00[UTC]\n" +
			"Time zone for this request: UTC. Interpret otherwise-unqualified dates and times in this zone.\n" +
			"Elapsed since the preceding model-visible message: 1500ms."},
		{"偏移量写成单个 Z", "Time sampled while preparing turn 1, step 1: 1970-01-01T00:00:03Z[UTC]\n" +
			"Time zone for this request: UTC. Interpret otherwise-unqualified dates and times in this zone.\n" +
			"Elapsed since the preceding model-visible message: 1s."},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			event := readingWithText(t, 4, one.text)
			if err := ValidateReading(openStepAt(t), event, time.UTC); !errors.Is(err, ErrInvariantViolated) {
				t.Fatalf("%s 该报 ErrInvariantViolated，得到 %v", one.name, err)
			}
		})
	}
}

// forgedReading 按一个模板正文造一条署了本包名字的读数，正文里的某一段被替换掉。
func forgedReading(t *testing.T, old string, replacement string) session.Event {
	t.Helper()

	text := RenderText(Reading{
		Now: at(3), Turn: 1, Step: 1, Previous: at(2), HasPrevious: true,
	}, time.UTC)
	forged := strings.Replace(text, old, replacement, 1)
	if forged == text {
		t.Fatalf("模板里没有 %q，这条测试自己写错了", old)
	}
	return readingWithText(t, 4, forged)
}

func Test回合号或步骤号溢出时被报出来(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		old  string
		new  string
	}{
		{"回合号位数太多", "turn 1,", "turn 99999999999999999999,"},
		{"步骤号位数太多", "step 1:", "step 99999999999999999999:"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			event := forgedReading(t, one.old, one.new)
			err := ValidateReading(openStepAt(t), event, time.UTC)
			if !errors.Is(err, ErrInvariantViolated) {
				t.Fatalf("%s 该报 ErrInvariantViolated，得到 %v", one.name, err)
			}
			if !strings.Contains(err.Error(), "能用的整数") {
				t.Fatalf("错误该说到整数用不了，得到 %v", err)
			}
		})
	}
}

func Test回合号与步骤号必须从一起(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		old  string
		new  string
	}{
		{"回合号是零", "turn 1,", "turn 0,"},
		{"步骤号是零", "step 1:", "step 0:"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			event := forgedReading(t, one.old, one.new)
			err := ValidateReading(openStepAt(t), event, time.UTC)
			if !errors.Is(err, ErrInvariantViolated) {
				t.Fatalf("%s 该报 ErrInvariantViolated，得到 %v", one.name, err)
			}
			if !strings.Contains(err.Error(), "都必须从 1 起") {
				t.Fatalf("错误该说到起始值，得到 %v", err)
			}
		})
	}
}

func Test读数自称的位置和日志对不上(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		old  string
		new  string
	}{
		{"回合号不对", "turn 1,", "turn 2,"},
		{"步骤号不对", "step 1:", "step 3:"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			event := forgedReading(t, one.old, one.new)
			err := ValidateReading(openStepAt(t), event, time.UTC)
			if !errors.Is(err, ErrInvariantViolated) {
				t.Fatalf("%s 该报 ErrInvariantViolated，得到 %v", one.name, err)
			}
			if !strings.Contains(err.Error(), "日志上开着的是") {
				t.Fatalf("错误该说到日志上的位置，得到 %v", err)
			}
		})
	}
}

func Test日志上没有追加位置时读数验不过(t *testing.T) {
	t.Parallel()

	event := readingAt(t, 4, Reading{Now: at(3), Turn: 1, Step: 1}, time.UTC)
	// 这段历史里请求头已经写下去了，位置本身就不成立。
	history := append(openStepAt(t), requestHeader(4))
	err := ValidateReading(history, event, time.UTC)
	if !errors.Is(err, ErrInvariantViolated) {
		t.Fatalf("该报 ErrInvariantViolated，得到 %v", err)
	}
}

func Test来源里的快照必须和正文逐字相同(t *testing.T) {
	t.Parallel()

	text := RenderText(Reading{Now: at(3), Turn: 1, Step: 1}, time.UTC)
	cases := []struct {
		name    string
		context llm.Context
	}{
		{"根本没带上下文", nil},
		{"形态不是快照", llm.NoticeContext{Summary: text}},
		{"一节都没有", llm.SnapshotContext{}},
		{"多出一节", llm.SnapshotContext{Sections: []llm.ContextSnapshotSection{
			{Name: PluginName, Text: text}, {Name: PluginName, Text: "多出来的"},
		}}},
		{"节名不是本包", llm.SnapshotContext{Sections: []llm.ContextSnapshotSection{
			{Name: "workspace-instructions", Text: text},
		}}},
		{"节里的文本和正文不一样", llm.SnapshotContext{Sections: []llm.ContextSnapshotSection{
			{Name: PluginName, Text: text + "（悄悄多一句）"},
		}}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			event := messageEvent(t, 4, llm.Message{
				ID:      llm.MessageID("r"),
				Role:    llm.RoleUser,
				Content: llm.Content{llm.TextBlock{Text: text}},
				Source:  llm.PluginSource{Plugin: PluginName, Context: one.context},
			})
			err := ValidateReading(openStepAt(t), event, time.UTC)
			if !errors.Is(err, ErrInvariantViolated) {
				t.Fatalf("%s 该报 ErrInvariantViolated，得到 %v", one.name, err)
			}
			if !strings.Contains(err.Error(), "原样带上") {
				t.Fatalf("错误该说到快照文本，得到 %v", err)
			}
		})
	}
}

func Test读数声称的时区和装配的不一样(t *testing.T) {
	t.Parallel()

	shanghai := mustLoad(t, "Asia/Shanghai")
	// 这条读数整段是按 UTC 排的，拿上海那份装配去验它。
	event := readingAt(t, 4, Reading{Now: at(3), Turn: 1, Step: 1}, time.UTC)
	err := ValidateReading(openStepAt(t), event, shanghai)
	if !errors.Is(err, ErrInvariantViolated) {
		t.Fatalf("该报 ErrInvariantViolated，得到 %v", err)
	}
	if !strings.Contains(err.Error(), "这套装配配的是") {
		t.Fatalf("错误该说到装配的时区，得到 %v", err)
	}
}

func Test数值上不成立的时间戳解不开(t *testing.T) {
	t.Parallel()

	event := forgedReading(t, "1970-01-01T", "1970-13-01T")
	err := ValidateReading(openStepAt(t), event, time.UTC)
	if !errors.Is(err, ErrInvariantViolated) {
		t.Fatalf("该报 ErrInvariantViolated，得到 %v", err)
	}
	if !strings.Contains(err.Error(), "解不开") {
		t.Fatalf("错误该说到解不开，得到 %v", err)
	}
}

func Test时间戳重排对不上就验不过(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		old  string
		new  string
	}{
		{"偏移量不是这个时区算的", "+00:00[UTC]", "+05:00[UTC]"},
		{"方括号里写的是另一个时区", "+00:00[UTC]", "+00:00[Asia/Shanghai]"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			event := forgedReading(t, one.old, one.new)
			err := ValidateReading(openStepAt(t), event, time.UTC)
			if !errors.Is(err, ErrInvariantViolated) {
				t.Fatalf("%s 该报 ErrInvariantViolated，得到 %v", one.name, err)
			}
			if !strings.Contains(err.Error(), "重排是") {
				t.Fatalf("错误该说到重排，得到 %v", err)
			}
		})
	}
}

func Test采样时刻不能晚于落库时刻(t *testing.T) {
	t.Parallel()

	// 事件落在 seq 4（也就是 4000 毫秒），却自称是第 9 秒采的。
	event := readingAt(t, 4, Reading{Now: at(9), Turn: 1, Step: 1}, time.UTC)
	err := ValidateReading(openStepAt(t), event, time.UTC)
	if !errors.Is(err, ErrInvariantViolated) {
		t.Fatalf("该报 ErrInvariantViolated，得到 %v", err)
	}
	if !strings.Contains(err.Error(), "晚于") {
		t.Fatalf("错误该说到先后，得到 %v", err)
	}
}

func Test耗时基线和步骤号对不上(t *testing.T) {
	t.Parallel()

	// 日志上开着的是第 2 步，读数却写着第一步那个基线名。
	history := append(openStepAt(t), requestHeader(4), stepStart(t, 5, 1, 2))
	event := readingWithText(t, 6,
		"Time sampled while preparing turn 1, step 2: 1970-01-01T00:00:05+00:00[UTC]\n"+
			"Time zone for this request: UTC. Interpret otherwise-unqualified dates and times in this zone.\n"+
			"Elapsed since the preceding model-visible message: 3s.")
	err := ValidateReading(history, event, time.UTC)
	if !errors.Is(err, ErrInvariantViolated) {
		t.Fatalf("该报 ErrInvariantViolated，得到 %v", err)
	}
	if !strings.Contains(err.Error(), "耗时基线") {
		t.Fatalf("错误该说到基线，得到 %v", err)
	}
}

func Test整段日志里的每一条读数都被验过(t *testing.T) {
	t.Parallel()

	events := []session.Event{
		turnStart(t, 1, 1),
		userText(t, 2, "你好"),
		stepStart(t, 3, 1, 1),
		readingAt(t, 4, Reading{Now: at(3), Turn: 1, Step: 1, Previous: at(2), HasPrevious: true}, time.UTC),
		requestHeader(5),
		assistantText(t, 6, "好的"),
		stepStart(t, 7, 1, 2),
		readingAt(t, 8, Reading{Now: at(7), Turn: 1, Step: 2, Previous: at(4), HasPrevious: true}, time.UTC),
	}
	if err := ValidateSession(events, time.UTC); err != nil {
		t.Fatalf("这段日志该验得过，却报了 %v", err)
	}
}

func Test整段日志里有一条伪造读数就验不过(t *testing.T) {
	t.Parallel()

	events := []session.Event{
		turnStart(t, 1, 1),
		userText(t, 2, "你好"),
		stepStart(t, 3, 1, 1),
		readingWithText(t, 4, "这不是一条读数"),
	}
	if err := ValidateSession(events, time.UTC); !errors.Is(err, ErrInvariantViolated) {
		t.Fatalf("该报 ErrInvariantViolated，得到 %v", err)
	}
}

func Test整段日志里坏掉的事件被报出来(t *testing.T) {
	t.Parallel()

	events := []session.Event{eventAt(1, session.EventUserMessage, json.RawMessage(`{`))}
	if err := ValidateSession(events, time.UTC); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("该报 ErrMalformedEvent，得到 %v", err)
	}
}

func Test坏掉的步骤开头负载在算位置时被报出来(t *testing.T) {
	t.Parallel()

	events := []session.Event{
		turnStart(t, 1, 1),
		eventAt(2, session.EventStepStart, json.RawMessage(`{"step":"一"}`)),
	}
	if _, _, err := PreparationPosition(events); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("该报 ErrMalformedEvent，得到 %v", err)
	}
}
