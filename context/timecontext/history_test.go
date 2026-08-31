// 本文件的作用：验从日志里取基线、取上一次注入、以及节流的判定。

package timecontext

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"ds-harness-go/session"
)

func Test上一条模型可见消息认三种事件(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		event session.Event
	}{
		{"用户消息", userText(t, 7, "你好")},
		{"助手消息", assistantText(t, 7, "好的")},
		{"工具结果", toolResult(t, 7)},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			events := []session.Event{turnStart(t, 1, 1), one.event, requestHeader(9)}
			found, ok := PrecedingMessageTime(events)
			if !ok {
				t.Fatalf("%s 该算作模型可见消息", one.name)
			}
			if !found.Equal(at(7)) {
				t.Fatalf("时刻该是 %s，得到 %s", at(7), found)
			}
		})
	}
}

func Test模型看不见的事件不算基线(t *testing.T) {
	t.Parallel()

	events := []session.Event{turnStart(t, 1, 1), stepStart(t, 2, 1, 1), requestHeader(3), stepEnd(t, 4, 1, 1)}
	if _, ok := PrecedingMessageTime(events); ok {
		t.Fatal("这段日志里一条模型可见消息都没有，不该找出基线")
	}
}

func Test本包自己的读数也算模型可见消息(t *testing.T) {
	t.Parallel()

	events := []session.Event{
		userText(t, 1, "你好"),
		readingAt(t, 5, Reading{Now: at(5), Turn: 1, Step: 1}, time.UTC),
	}
	found, ok := PrecedingMessageTime(events)
	if !ok || !found.Equal(at(5)) {
		t.Fatalf("基线该是读数那一条 %s，得到 %s（ok=%v）", at(5), found, ok)
	}
}

func Test本回合内的上一条读数是后续步骤的基线(t *testing.T) {
	t.Parallel()

	events := []session.Event{
		turnStart(t, 1, 1),
		readingAt(t, 2, Reading{Now: at(2), Turn: 1, Step: 1}, time.UTC),
		stepStart(t, 3, 1, 2),
	}
	found, ok, err := PrecedingStepContextTime(events, 1)
	if err != nil {
		t.Fatalf("这段日志该读得回来，却报了 %v", err)
	}
	if !ok || !found.Equal(at(2)) {
		t.Fatalf("基线该是 %s，得到 %s（ok=%v）", at(2), found, ok)
	}
}

func Test取步骤基线时不越过本回合的开头(t *testing.T) {
	t.Parallel()

	events := []session.Event{
		turnStart(t, 1, 1),
		readingAt(t, 2, Reading{Now: at(2), Turn: 1, Step: 1}, time.UTC),
		turnEnd(3),
		turnStart(t, 4, 2),
	}
	_, ok, err := PrecedingStepContextTime(events, 2)
	if err != nil {
		t.Fatalf("这段日志该读得回来，却报了 %v", err)
	}
	if ok {
		t.Fatal("上一个回合里的读数不是本回合的基线")
	}
}

func Test别的回合的开头不挡住往回扫(t *testing.T) {
	t.Parallel()

	// 往回扫时先遇到回合 2 的开头，那不是要找的边界，得继续往前。
	events := []session.Event{
		turnStart(t, 1, 3),
		readingAt(t, 2, Reading{Now: at(2), Turn: 3, Step: 1}, time.UTC),
		turnStart(t, 4, 2),
	}
	found, ok, err := PrecedingStepContextTime(events, 3)
	if err != nil {
		t.Fatalf("这段日志该读得回来，却报了 %v", err)
	}
	if !ok || !found.Equal(at(2)) {
		t.Fatalf("基线该是 %s，得到 %s（ok=%v）", at(2), found, ok)
	}
}

func Test日志里没有读数时取不到步骤基线(t *testing.T) {
	t.Parallel()

	events := []session.Event{userText(t, 1, "你好"), otherPluginText(t, 2, "别人的注入")}
	_, ok, err := PrecedingStepContextTime(events, 1)
	if err != nil {
		t.Fatalf("这段日志该读得回来，却报了 %v", err)
	}
	if ok {
		t.Fatal("这里一条本包的读数都没有")
	}
}

func Test上一次注入跨得过回合边界(t *testing.T) {
	t.Parallel()

	events := []session.Event{
		turnStart(t, 1, 1),
		readingAt(t, 2, Reading{Now: at(2), Turn: 1, Step: 1}, time.UTC),
		turnEnd(3),
		turnStart(t, 4, 2),
		userText(t, 5, "接着聊"),
	}
	found, ok, err := LatestInjectionTime(events)
	if err != nil {
		t.Fatalf("这段日志该读得回来，却报了 %v", err)
	}
	if !ok || !found.Equal(at(2)) {
		t.Fatalf("上一次注入该是 %s，得到 %s（ok=%v）", at(2), found, ok)
	}
}

func Test没注入过时上一次注入取不到(t *testing.T) {
	t.Parallel()

	_, ok, err := LatestInjectionTime([]session.Event{userText(t, 1, "你好")})
	if err != nil {
		t.Fatalf("这段日志该读得回来，却报了 %v", err)
	}
	if ok {
		t.Fatal("这里一条本包的读数都没有")
	}
}

func Test基线按步骤号在两种来源之间切换(t *testing.T) {
	t.Parallel()

	events := []session.Event{
		turnStart(t, 1, 1),
		userText(t, 2, "你好"),
		readingAt(t, 3, Reading{Now: at(3), Turn: 1, Step: 1}, time.UTC),
	}

	first, ok, err := PreviousBaseline(events[:2], 1, 1)
	if err != nil || !ok || !first.Equal(at(2)) {
		t.Fatalf("第一步该取那条用户消息 %s，得到 %s（ok=%v err=%v）", at(2), first, ok, err)
	}

	later, ok, err := PreviousBaseline(events, 1, 2)
	if err != nil || !ok || !later.Equal(at(3)) {
		t.Fatalf("第二步该取那条读数 %s，得到 %s（ok=%v err=%v）", at(3), later, ok, err)
	}
}

func Test间隔不为正时永远注入(t *testing.T) {
	t.Parallel()

	events := []session.Event{readingAt(t, 1, Reading{Now: at(1), Turn: 1, Step: 1}, time.UTC)}
	for _, interval := range []time.Duration{0, -time.Minute} {
		inject, err := ShouldInject(events, at(2), interval)
		if err != nil {
			t.Fatalf("判定该成功，却报了 %v", err)
		}
		if !inject {
			t.Fatalf("间隔为 %s 时该注入", interval)
		}
	}
}

func Test第一次注入不受节流拦(t *testing.T) {
	t.Parallel()

	inject, err := ShouldInject([]session.Event{userText(t, 1, "你好")}, at(2), time.Hour)
	if err != nil {
		t.Fatalf("判定该成功，却报了 %v", err)
	}
	if !inject {
		t.Fatal("从没注入过时该注入")
	}
}

func Test间隔没走满就跳过(t *testing.T) {
	t.Parallel()

	events := []session.Event{readingAt(t, 10, Reading{Now: at(10), Turn: 1, Step: 1}, time.UTC)}
	inject, err := ShouldInject(events, at(40), time.Minute)
	if err != nil {
		t.Fatalf("判定该成功，却报了 %v", err)
	}
	if inject {
		t.Fatal("上一次注入之后才过了 30 秒，配的是一分钟，该跳过")
	}
}

func Test间隔正好走满就注入(t *testing.T) {
	t.Parallel()

	events := []session.Event{readingAt(t, 10, Reading{Now: at(10), Turn: 1, Step: 1}, time.UTC)}
	inject, err := ShouldInject(events, at(70), time.Minute)
	if err != nil {
		t.Fatalf("判定该成功，却报了 %v", err)
	}
	if !inject {
		t.Fatal("整整一分钟已经走满，该注入")
	}
}

func Test时钟往回跳时照样注入(t *testing.T) {
	t.Parallel()

	// 此刻早于上一次注入，减出来是负数，光比大小会把读数永远节流掉。
	events := []session.Event{readingAt(t, 100, Reading{Now: at(100), Turn: 1, Step: 1}, time.UTC)}
	inject, err := ShouldInject(events, at(40), time.Hour)
	if err != nil {
		t.Fatalf("判定该成功，却报了 %v", err)
	}
	if !inject {
		t.Fatal("时刻早于上一次注入时该注入，不该被当成刚注入过")
	}
}

func Test读数的判据是署名不是正文(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		event session.Event
		want  bool
	}{
		{"本包的读数", readingAt(t, 1, Reading{Now: at(1), Turn: 1, Step: 1}, time.UTC), true},
		{"用户自己说的话", userText(t, 1, "你好"), false},
		{"别的插件的注入", otherPluginText(t, 1, "别人的"), false},
		{"助手消息", assistantText(t, 1, "好的"), false},
		{"回合开头", turnStart(t, 1, 1), false},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			got, err := IsReadingEvent(one.event)
			if err != nil {
				t.Fatalf("判定该成功，却报了 %v", err)
			}
			if got != one.want {
				t.Fatalf("%s 的判定该是 %v，得到 %v", one.name, one.want, got)
			}
		})
	}
}

func Test坏掉的用户消息负载被报出来(t *testing.T) {
	t.Parallel()

	broken := eventAt(1, session.EventUserMessage, json.RawMessage(`{`))

	if _, err := IsReadingEvent(broken); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("IsReadingEvent 该报 ErrMalformedEvent，得到 %v", err)
	}
	if _, _, err := LatestInjectionTime([]session.Event{broken}); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("LatestInjectionTime 该报 ErrMalformedEvent，得到 %v", err)
	}
	if _, _, err := PrecedingStepContextTime([]session.Event{broken}, 1); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("PrecedingStepContextTime 该报 ErrMalformedEvent，得到 %v", err)
	}
	if _, err := ShouldInject([]session.Event{broken}, at(1), time.Minute); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("ShouldInject 该报 ErrMalformedEvent，得到 %v", err)
	}
	if _, _, err := PreviousBaseline([]session.Event{broken}, 1, 2); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("PreviousBaseline 该报 ErrMalformedEvent，得到 %v", err)
	}
}

func Test坏掉的回合开头负载被报出来(t *testing.T) {
	t.Parallel()

	broken := eventAt(1, session.EventTurnStart, json.RawMessage(`{"turn":"一"}`))

	if _, _, err := PrecedingStepContextTime([]session.Event{broken}, 1); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("PrecedingStepContextTime 该报 ErrMalformedEvent，得到 %v", err)
	}
	if _, _, err := PreparationPosition([]session.Event{broken}); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("PreparationPosition 该报 ErrMalformedEvent，得到 %v", err)
	}
}
