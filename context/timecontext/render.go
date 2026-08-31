// 本文件的作用：一条时间读数面向模型的那三行正文，以及它带的那个来源。
//
// 源: packages/context/time-context/src/index.ts:40-55,110-125,198-207

package timecontext

import (
	"fmt"
	"strings"
	"time"

	"ds-harness-go/llm"
)

// 「上一次」是相对谁算的。这两个字符串进提示词，所以是英文，而且一个字不能改：
// 不变量要靠它们分辨这条读数说的是第一步还是后续步骤。
//
// 源: packages/context/time-context/src/index.ts:120
const (
	// baselineFirstStep 是第一步的基线：上一条模型看得见的消息。
	baselineFirstStep = "model-visible message"
	// baselineLaterStep 是后续步骤的基线：本回合里上一条时间读数。
	baselineLaterStep = "step context"
)

// Reading 是一条时间读数的全部输入。
//
// 新增: DSH 的 `renderText` 收七个位置参数。Go 里把「这次观察到了什么」聚成
// 一个值，剩下的时区从 [ResolvedConfig] 走——好处是不变量复核时可以拼出
// **同一个** Reading 再重排一遍，而七个散参数很容易在复核那一侧漏传一个。
type Reading struct {
	// Now 是这次采样的时刻。
	Now time.Time
	// Turn 是正在准备的回合号，从 1 起。
	Turn int
	// Step 是正在准备的步骤号，从 1 起。
	Step int
	// Previous 是上一个基线时刻；HasPrevious 为假时无意义。
	Previous time.Time
	// HasPrevious 为假表示日志里找不到基线，正文里写 unavailable。
	//
	// 新增: DSH 用 `number | undefined`。这里是值加布尔，理由和会话引用那处
	// 逐字相同：Unix 纪元零点是一个合法时刻，拿零值当「没有」会撞车。
	HasPrevious bool
}

// RenderText 排出一条时间读数的正文。
//
// 源: packages/context/time-context/src/index.ts:110-125
//
// # 少掉的那一层：浏览器时区
//
// DSH 的第二行说的是「这次请求的**浏览器**时区」，取自本回合每条用户消息上由
// Host 带上来的 `clientTimeZone`，还要处理同一回合里几条消息报了不同时区
// （mixed）和一条都没报（missing）两种情况。docs/DESIGN.md 第三节把这条改了：
// **改成由消费方传时区**——服务端没有「当前用户的浏览器」，而那个字段的产出方
// （host/apiproxy）整个不在移植范围里。
//
// 于是时区只有一个来源、也只有一种情况，mixed 和 missing 两条分支连同它们
// 那两句「去问用户」一起消失。第二行保留下来，因为它承担的**模型行为**没有
// 变：不说清楚的日期时间按哪个时区理解，得有人告诉模型。
func RenderText(reading Reading, location *time.Location) string {
	elapsed := "unavailable"
	if reading.HasPrevious {
		elapsed = formatDuration(reading.Now.Sub(reading.Previous))
	}
	baseline := baselineLaterStep
	if reading.Step == 1 {
		baseline = baselineFirstStep
	}
	return fmt.Sprintf(
		"Time sampled while preparing turn %d, step %d: %s\n"+
			"Time zone for this request: %s. Interpret otherwise-unqualified dates and times in this zone.\n"+
			"Elapsed since the preceding %s: %s.",
		reading.Turn, reading.Step, FormatTimestamp(reading.Now, location),
		location.String(), baseline, elapsed)
}

// ReadingSource 造一条时间读数该带的来源。
//
// 源: packages/context/time-context/src/index.ts:204
//
// 分节文本和消息正文是**同一段字节**，这一条被不变量钉死。它要防的是：
// 一个注入方把面向模型的正文写成 A、却在自己的来源里声称贡献了 B，
// 于是日志上留下的账和模型实际看见的东西对不上。
//
// 新增: DSH 那边还得查 `Object.keys(source).length !== 4`，防的是有人往这个
// 来源对象上多挂字段（比如请求授权）夹带进提示词。Go 里 llm.PluginSource
// 是个封闭结构体，多挂不进去，那道检查没有对应物。
func ReadingSource(text string) llm.PluginSource {
	return llm.PluginSource{
		Plugin: PluginName,
		Context: llm.SnapshotContext{
			Sections: []llm.ContextSnapshotSection{{Name: PluginName, Text: text}},
		},
	}
}

// formatDuration 把一段间隔排成读数末尾那个「几天几小时几分几秒」。
//
// 源: packages/context/time-context/src/index.ts:41-55
//
// 秒是必出的，天／时／分只在非零时出现，所以 90 秒是 `1m 30s`，30 秒是 `30s`。
// 负数按零算：DSH 那边 `Math.max(0, elapsedMs)`，为的是日志里时刻乱序时
// 不至于渲染出一个负的间隔——模型读到 `-3s` 只会当成这段上下文坏了。
func formatDuration(elapsed time.Duration) string {
	seconds := int64(max(elapsed, 0) / time.Second)
	days := seconds / 86400
	seconds %= 86400
	hours := seconds / 3600
	seconds %= 3600
	minutes := seconds / 60
	seconds %= 60

	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	parts = append(parts, fmt.Sprintf("%ds", seconds))
	return strings.Join(parts, " ")
}
