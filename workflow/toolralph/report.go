// 本文件的作用：轮与轮之间那份唯一被允许通过的东西——一份结构化的轮次报告：
// 它长什么样、发给孩子的那份 schema 怎么写、以及收回来之后那**一道**校验。
//
// 源: packages/workflow/tool-ralph/src/index.ts:49-65, 91-149, 234-280

package toolralph

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"ds-harness-go/core/tools"
)

// RoundStatus 是一个孩子对自己这一轮的定性。
//
// 源: packages/workflow/tool-ralph/src/index.ts:49
type RoundStatus string

const (
	// RoundContinue 表示还有有意义的活儿可做，下一轮接着来。
	RoundContinue RoundStatus = "continue"
	// RoundComplete 表示目标达到了，而且拿得出证据。
	RoundComplete RoundStatus = "complete"
	// RoundBlocked 表示没有人来插手就再也推不动了。
	RoundBlocked RoundStatus = "blocked"
)

// RoundReport 是一个孩子交上来的那份轮次报告，也是轮与轮之间唯一的载荷。
//
// 源: packages/workflow/tool-ralph/src/index.ts:51-57
//
// 字段名照抄 DSH，因为它们直接进那份发给孩子的 schema，也直接进给下一轮看的
// 那段 JSON。
type RoundReport struct {
	// Status 是这一轮的定性。
	Status RoundStatus `json:"status"`
	// Summary 是这一轮干了什么，一句规范化的非空文字。
	Summary string `json:"summary"`
	// Evidence 是拿得出手的凭据，每一条都是规范化的非空文字。
	Evidence []string `json:"evidence"`
	// NextSteps 是下一轮该接着做的事，每一条都是规范化的非空文字。
	NextSteps []string `json:"nextSteps"`
	// Blocker 是那件挡路的事；不是 [RoundBlocked] 时必须是空串。
	Blocker string `json:"blocker"`
}

// reportKeys 是一份报告**恰好**该有的那五个键，已排序。
//
// 源: packages/workflow/tool-ralph/src/index.ts:249
var reportKeys = []string{"blocker", "evidence", "nextSteps", "status", "summary"}

// reportSchema 是发给每一轮那个孩子的结构化输出契约。
//
// 源: packages/workflow/tool-ralph/src/index.ts:91-102
//
// 它是**封闭**的（additionalProperties 显式为 false）：多一个键就当场验不过，
// 于是「孩子往交接里夹私货」这条路在提供方那一层就断了，轮到本包时只剩形状对的
// 五个字段。
func reportSchema() tools.Node {
	closed := false
	enum := func(values ...RoundStatus) []json.RawMessage {
		encoded := make([]json.RawMessage, 0, len(values))
		for _, value := range values {
			// 排的是一个具名字符串类型，排不失败。
			raw, _ := json.Marshal(string(value))
			encoded = append(encoded, raw)
		}
		return encoded
	}
	stringList := tools.Node{Type: tools.TypeArray, Items: &tools.Node{Type: tools.TypeString}}
	return tools.Node{
		Type: tools.TypeObject,
		Properties: []tools.Property{
			{Name: "status", Schema: tools.Node{
				Type: tools.TypeString,
				Enum: enum(RoundContinue, RoundComplete, RoundBlocked),
			}},
			{Name: "summary", Schema: tools.Node{Type: tools.TypeString}},
			{Name: "evidence", Schema: stringList},
			{Name: "nextSteps", Schema: stringList},
			{Name: "blocker", Schema: tools.Node{Type: tools.TypeString}},
		},
		Required:             []string{"status", "summary", "evidence", "nextSteps", "blocker"},
		AdditionalProperties: &closed,
	}
}

// normalizedText 说明这是不是一段规范化的**非空**文字。
//
// 源: packages/workflow/tool-ralph/src/index.ts:104-106, 238-240
//
// 「规范化」在这里就是前后不带空白。这道闸的用处很实在：模型很爱交
// `"  "`、`"\n"`、`" done "` 这种东西，不挡住的话，下一轮拿到的交接里就会混进
// 一堆看不见的字符，而它们在提示词里是真的占位置的。
func normalizedText(value string) bool {
	return value != "" && value == strings.TrimSpace(value)
}

// normalizedList 说明这一串里是不是每一条都过得了 [normalizedText]。
//
// 源: packages/workflow/tool-ralph/src/index.ts:108-110, 242-244
//
// 空的一串**是**合法的：`evidence: []` 对一个 continue 的报告完全正常。
func normalizedList(values []string) bool {
	for _, value := range values {
		if !normalizedText(value) {
			return false
		}
	}
	return true
}

// jsonChars 数一段文字里有几个字。
//
// 新增: DSH 数的是 JS 的 `.length`，也就是 UTF-16 码元数。Go 没有 UTF-16 字符串，
// 而 len() 数的是字节——对英文两者一样，对中文差三倍，那会让同一份配置在中英文
// 两种交接上宽严完全不同。这里数**码点**：它是「几个字」这件事最接近直觉的度量，
// 也让 [boundResult] 的截断能落在字的边界上而不是把一个字劈两半。
func jsonChars(value string) int {
	return utf8.RuneCountInString(value)
}

// encodeReport 把一份报告排成它在交接和展示里用的那串 JSON。
//
// 源: packages/workflow/tool-ralph/src/index.ts:144, 275
//
// 不交回错误：[RoundReport] 全是字符串和字符串切片，没有 channel、函数、
// NaN 这些排不动的东西，也没有自定义的 MarshalJSON，所以这一步排不失败。
// 硬留一条错误路只会多出一段永远走不到、也验不了的代码。
func encodeReport(report RoundReport) string {
	encoded, _ := json.Marshal(report)
	return string(encoded)
}

// readReport 把孩子交回来的那个结构化值收成一份验过的报告。
//
// 源: packages/workflow/tool-ralph/src/index.ts:112-149（脚本里那道）, 246-280（宿主侧那道）
//
// 新增: DSH 这套检查存在**两份**，因为它那条固定脚本跑在 worker 线程里，脚本的
// 返回值要穿过引擎这道不受信的边界回到宿主，所以两侧各验一遍。本包没有那道边界
// （报告从提供方手里出来直接进本包的内存），所以只留这一份。两份的规则本来就
// 逐条相同，合并不丢任何一条。
//
// expected 是调用方**已经知道**这一份该是什么定性——DSH 宿主侧那道校验就是这么
// 做的（readReport(value, 'complete', …)）。轮次循环里那一道传空串，表示三种都收。
func readReport(value any, expected RoundStatus, maxChars int) (RoundReport, error) {
	fields, err := reportFields(value)
	if err != nil {
		return RoundReport{}, err
	}
	var report RoundReport
	if err := json.Unmarshal(fields, &report); err != nil {
		return RoundReport{}, fmt.Errorf("Ralph round report is malformed: %w", err)
	}
	if expected != "" && report.Status != expected {
		return RoundReport{}, fmt.Errorf(
			"Ralph round report status is %q, expected %q", report.Status, expected)
	}
	if err := validateReport(report); err != nil {
		return RoundReport{}, err
	}
	if chars := jsonChars(encodeReport(report)); chars > maxChars {
		return RoundReport{}, fmt.Errorf(
			"Ralph round report exceeds maxHandoffChars (%d > %d)", chars, maxChars)
	}
	return report, nil
}

// reportFields 把那个来路不明的值收成一份**恰好**带着那五个键的 JSON 对象。
//
// 源: packages/workflow/tool-ralph/src/index.ts:113-115, 248-249
//
// 新增: DSH 是 `isRecord(value) && Object.keys(value).sort().join(',') === '…'`。
// Go 这边那个值是 [ds-harness-go/subagent/subagent.Result.Structured]（一个 any），
// 所以先排回字节再解成键表——这一步同时把「不是对象」（数组、null、标量都解不进
// map）和「键不对」两件事一起办了。
//
// 多一个键就拒，而不是忽略：这份 schema 是封闭的，多出来的键说明它压根没经过
// 那份 schema，那这个值是谁给的就说不清了。
func reportFields(value any) (json.RawMessage, error) {
	malformed := errors.New("Ralph child returned no structured round report")
	if value == nil {
		return nil, malformed
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, malformed
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil, malformed
	}
	present := make([]string, 0, len(fields))
	for key := range fields {
		present = append(present, key)
	}
	slices.Sort(present)
	if !slices.Equal(present, reportKeys) {
		return nil, fmt.Errorf(
			"Ralph round report must carry exactly %s, got %s",
			strings.Join(reportKeys, ", "), strings.Join(present, ", "))
	}
	return encoded, nil
}

// validateReport 把那几条跨字段的规矩查一遍。
//
// 源: packages/workflow/tool-ralph/src/index.ts:116-143, 250-274
//
// 这几条不是形式主义，它们各自堵着一种很具体的退化：
//
//   - continue 必须留下 nextSteps：交不出下一步的「还要继续」等于让循环空转到
//     预算用光。
//   - complete 必须拿出 evidence、而且不许再留 nextSteps：一句光秃秃的「做完了」
//     是这类循环最常见的假阳性。
//   - blocked 必须写清楚 blocker：说不出被什么挡住，那就不是阻塞，是放弃。
//   - 不是 blocked 就不许写 blocker：否则「有没有卡住」这件事有两个互相矛盾的
//     说法，而下游只读 status。
//
// 那几句话是给模型看的，所以是英文。
func validateReport(report RoundReport) error {
	if !normalizedText(report.Summary) {
		return errors.New("Ralph round report summary must be non-empty and normalized")
	}
	if !normalizedList(report.Evidence) || !normalizedList(report.NextSteps) {
		return errors.New(
			"Ralph round report evidence and nextSteps must contain only non-empty normalized strings")
	}
	if report.Blocker != strings.TrimSpace(report.Blocker) {
		return errors.New("Ralph round report blocker must be a normalized string")
	}
	switch report.Status {
	case RoundContinue:
		if len(report.NextSteps) == 0 || report.Blocker != "" {
			return errors.New("a continuing Ralph report needs nextSteps and an empty blocker")
		}
	case RoundComplete:
		if len(report.Evidence) == 0 || len(report.NextSteps) != 0 || report.Blocker != "" {
			return errors.New("a complete Ralph report needs evidence, no nextSteps, and an empty blocker")
		}
	case RoundBlocked:
		if !normalizedText(report.Blocker) {
			return errors.New("a blocked Ralph report needs a concrete blocker")
		}
	default:
		return errors.New("Ralph round report status is invalid")
	}
	return nil
}
