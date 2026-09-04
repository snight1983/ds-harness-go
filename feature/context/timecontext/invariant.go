// 本文件的作用：本包自己拥有的那条运行期不变量——一条落库的时间读数必须
// 说得出它是在哪个回合的哪一步、按哪个时区、什么时候采的，而且这三件事都对得上。
//
// 源: packages/context/time-context/src/invariant.ts

package timecontext

import (
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/context/time-context/src/invariant.ts:12
const PackageName = "@deepseek-ai/dsh-time-context"

// readingPattern 是一条落库读数的完整形状。
//
// 源: packages/context/time-context/src/invariant.ts:14-20
//
// 它和 [RenderText] 是同一件事的两面：一个负责排出去，一个负责认回来。改了
// 正文就必须同时改这里，否则历史里所有读数会一起被判成伪造的。
//
// 六个捕获组依次是：回合号、步骤号、不带方括号的时间戳、方括号里的时区名、
// 那句时区声明里的时区名、以及耗时的基线名。
//
// 新增: DSH 那份允许时间戳的偏移量写成单个 `Z`。Go 这边 [timestampLayout]
// 永远排出 `+00:00`，所以那条支路去掉了——一条本包排不出来的写法留在这里，
// 等于给伪造留一个合法形状。
var readingPattern = regexp.MustCompile(
	`^Time sampled while preparing turn (\d+), step (\d+): ` +
		`(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}[+-]\d{2}:\d{2})\[([^\]]+)\]\n` +
		`Time zone for this request: (.+)\. Interpret otherwise-unqualified dates and times in this zone\.\n` +
		`Elapsed since the preceding (model-visible message|step context): ` +
		`(?:unavailable|(?:(?:\d+d )?(?:\d+h )?(?:\d+m )?\d+s))\.$`)

// PreparationPosition 算出日志末尾那个「可以追加一条读数」的位置。
//
// 源: packages/context/time-context/src/invariant.ts:28-68
//
// 三条各自独立：必须有一个开着的回合、必须已经开了步骤、而且这一步的请求头
// 还没写下去。第三条是关键——请求头是「这一步发出去的东西定稿了」的标记，
// 定稿之后再往历史里塞一条读数，就意味着日志说的和真正发出去的不是一回事。
func PreparationPosition(events []sessionlog.Event) (int, int, error) {
	openTurn, turnIsOpen := 0, false
	openStep, stepIsOpen := 0, false
	requestStarted := false

	for _, event := range events {
		switch event.Type {
		case sessionlog.EventTurnStart:
			start, err := decodeTurnStart(event)
			if err != nil {
				return 0, 0, err
			}
			openTurn, turnIsOpen = start.Turn, true
			stepIsOpen = false
			requestStarted = false
		case sessionlog.EventStepStart:
			start, err := decodeStepStart(event)
			if err != nil {
				return 0, 0, err
			}
			openStep, stepIsOpen = start.Step, true
			requestStarted = false
		case sessionlog.EventRequestHeader:
			requestStarted = true
		case sessionlog.EventStepEnd:
			stepIsOpen = false
			requestStarted = false
		case sessionlog.EventTurnEnd:
			turnIsOpen = false
			stepIsOpen = false
			requestStarted = false
		}
	}

	if !turnIsOpen {
		return 0, 0, fmt.Errorf("%w：时间读数只能追加在一个开着的回合里", ErrInvariantViolated)
	}
	if !stepIsOpen {
		return 0, 0, fmt.Errorf("%w：时间读数必须跟在 step/start 之后", ErrInvariantViolated)
	}
	if requestStarted {
		return 0, 0, fmt.Errorf("%w：时间读数必须写在 request/header 之前", ErrInvariantViolated)
	}
	return openTurn, openStep, nil
}

// ValidateReading 验一条落库的时间读数。
//
// 源: packages/context/time-context/src/invariant.ts:78-159
//
// history 是这条事件**之前**的那段日志，location 是这套装配配的时区。
//
// # 比 DSH 多出来的那条检查
//
// DSH 只在浏览器时区唯一时才重排一遍时间戳去比对，因为它兜底用的是宿主机进程
// 时区，而不变量那一侧拿不到同一个进程。这里时区由消费方给（见 [RenderText]），
// 装配方注册这条检查时把它一起交进来，所以**每一条**读数都要重排一遍再逐字比对。
// 这一条盖住了两件事：偏移量是不是按这个时区算的，以及方括号里写的是不是它。
//
// # 没有对应物的那条检查
//
// DSH 还要按本回合的用户消息重新推一遍浏览器时区，比对读数里那一行。浏览器
// 时区整个不在范围里，那条检查跟着消失，换成上面那条更强的。
func ValidateReading(history []sessionlog.Event, event sessionlog.Event, location *time.Location) error {
	message, err := decodeUserMessage(event)
	if err != nil {
		return err
	}

	plugin, ok := message.Source.(llm.PluginSource)
	if !ok || plugin.Plugin != PluginName {
		return fmt.Errorf("%w：seq %d 的来源不是本包，验它没有意义", ErrInvariantViolated, event.Seq)
	}

	if len(message.Content) != 1 {
		return fmt.Errorf("%w：seq %d 的读数必须正好是一个文本块，实际有 %d 个",
			ErrInvariantViolated, event.Seq, len(message.Content))
	}
	block, ok := message.Content[0].(llm.TextBlock)
	if !ok {
		return fmt.Errorf("%w：seq %d 的读数里那一块不是文本，是 %T",
			ErrInvariantViolated, event.Seq, message.Content[0])
	}

	match := readingPattern.FindStringSubmatch(block.Text)
	if match == nil {
		return fmt.Errorf("%w：seq %d 的读数对不上落库格式：%q",
			ErrInvariantViolated, event.Seq, block.Text)
	}

	// 正则只放行 `\d+`，所以这里唯一可能的失败是位数多到溢出 int。
	turn, err := strconv.Atoi(match[1])
	if err != nil {
		return fmt.Errorf("%w：seq %d 的回合号 %q 不是一个能用的整数",
			ErrInvariantViolated, event.Seq, match[1])
	}
	step, err := strconv.Atoi(match[2])
	if err != nil {
		return fmt.Errorf("%w：seq %d 的步骤号 %q 不是一个能用的整数",
			ErrInvariantViolated, event.Seq, match[2])
	}
	if turn < 1 || step < 1 {
		return fmt.Errorf("%w：seq %d 的回合号与步骤号都必须从 1 起，读数写的是 %d/%d",
			ErrInvariantViolated, event.Seq, turn, step)
	}

	expectedTurn, expectedStep, err := PreparationPosition(history)
	if err != nil {
		return err
	}
	if turn != expectedTurn || step != expectedStep {
		return fmt.Errorf("%w：seq %d 的读数自称回合 %d 步骤 %d，日志上开着的是回合 %d 步骤 %d",
			ErrInvariantViolated, event.Seq, turn, step, expectedTurn, expectedStep)
	}

	snapshot, ok := plugin.Context.(llm.SnapshotContext)
	if !ok || len(snapshot.Sections) != 1 ||
		snapshot.Sections[0].Name != PluginName || snapshot.Sections[0].Text != block.Text {
		// 来源里记的账必须和模型看见的正文逐字相同，否则日志会替一段没人见过的
		// 文本背书。
		return fmt.Errorf("%w：seq %d 的来源没有原样带上那段快照文本", ErrInvariantViolated, event.Seq)
	}

	if match[5] != location.String() {
		return fmt.Errorf("%w：seq %d 的读数声称时区是 %q，这套装配配的是 %q",
			ErrInvariantViolated, event.Seq, match[5], location.String())
	}

	sampled, err := time.Parse(timestampLayout, match[3])
	if err != nil {
		// 正则已经钉死了每一段的位数，剩下能落到这里的只有「月份 13」这类
		// 数值上不成立的时间。
		return fmt.Errorf("%w：seq %d 的时间戳 %q 解不开：%w",
			ErrInvariantViolated, event.Seq, match[3], err)
	}
	if rendered := FormatTimestamp(sampled, location); rendered != match[3]+"["+match[4]+"]" {
		return fmt.Errorf("%w：seq %d 的时间戳按 %q 重排是 %q，读数写的是 %q",
			ErrInvariantViolated, event.Seq, location.String(), rendered, match[3]+"["+match[4]+"]")
	}
	if sampled.After(eventTime(event)) {
		// 采样发生在落库之前，反过来说明这条读数上的时刻是编的。
		return fmt.Errorf("%w：seq %d 的采样时刻晚于它自己落库的时刻", ErrInvariantViolated, event.Seq)
	}

	baseline := baselineLaterStep
	if step == 1 {
		baseline = baselineFirstStep
	}
	if match[6] != baseline {
		return fmt.Errorf("%w：seq %d 是第 %d 步，耗时基线该是 %q，读数写的是 %q",
			ErrInvariantViolated, event.Seq, step, baseline, match[6])
	}
	return nil
}

// ValidateSession 验一整段日志里本包写下的每一条读数。
//
// 源: packages/context/time-context/src/invariant.ts:187-193（apply）
func ValidateSession(events []sessionlog.Event, location *time.Location) error {
	for index, event := range events {
		reading, err := IsReadingEvent(event)
		if err != nil {
			return err
		}
		if !reading {
			continue
		}
		if err := ValidateReading(events[:index], event, location); err != nil {
			return err
		}
	}
	return nil
}

// decodeStepStart 读回一条 step/start 的负载。
func decodeStepStart(event sessionlog.Event) (sessionlog.StepStartData, error) {
	data, err := sessionlog.DecodeData(event)
	if err != nil {
		return sessionlog.StepStartData{}, fmt.Errorf("%w：seq %d 的 step/start：%w",
			ErrMalformedEvent, event.Seq, err)
	}
	start, ok := data.(sessionlog.StepStartData)
	if !ok {
		// 不可达，理由同 [decodeTurnStart]。
		return sessionlog.StepStartData{}, fmt.Errorf("%w：seq %d 声称是 step/start，负载却是 %T",
			ErrMalformedEvent, event.Seq, data)
	}
	return start, nil
}
