// 本文件的作用：这一层的字符预算词汇，以及把它补完默认值、验一遍的那一步。
//
// 源: packages/compaction/compaction-tool-result-pruner/src/types.ts
// 源: packages/compaction/compaction-tool-result-pruner/src/config.ts

package toolresultpruner

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

// PruneMarker 是顶替被砍掉那一段中间的固定标记。
//
// 源: packages/compaction/compaction-tool-result-pruner/src/config.ts:6
//
// 英文原样照抄：它落进工具结果的正文，是给模型读的。
const PruneMarker = "\n\n[... tool result middle pruned ...]\n\n"

// 三档字符预算的默认值，按编程助手的工具输出定的。
//
// 源: packages/compaction/compaction-tool-result-pruner/src/config.ts:10-13
const (
	// DefaultThresholdChars 是「一条工具结果的正文超过多少个码点就该砍」。
	DefaultThresholdChars = 8192
	// DefaultHeadChars 是最多留下多少个开头的码点。
	DefaultHeadChars = 4096
	// DefaultTailChars 是最多留下多少个结尾的码点。
	DefaultTailChars = 1024
)

// ErrInvalidConfig 表示这一层的配置不合法。
//
// 新增: DSH 那边配置校验一律抛裸 Error，靠文案区分。Go 里没有异常，做成哨兵值
// 是为了让调用方用 errors.Is 判定，而不是去匹配错误文案。
var ErrInvalidConfig = errors.New("compaction/toolresultpruner: 配置不合法")

// Config 是这一层写下来的配置。
//
// 源: packages/compaction/compaction-tool-result-pruner/src/types.ts:3-11（ToolResultPruneConfig）
type Config struct {
	// ThresholdChars 是超过多少个码点才砍，正数；零表示这一层不说话。
	ThresholdChars int
	// HeadChars 是最多留多少个开头的码点，非负；nil 表示不说话。
	//
	// 新增: 指针而不是 int。理由是它的**零是有意义的**——一个码点都不留头，
	// 而默认值是 4096。拿零当「没给」会把这个明确的意思静默改写成默认值。
	// ThresholdChars 不需要指针：合法取值是 ≥1，零本来就不在里面。
	HeadChars *int
	// TailChars 是最多留多少个结尾的码点，非负；nil 表示不说话。理由同 HeadChars。
	TailChars *int
}

// ResolvedConfig 是验过的字符预算。
//
// 源: packages/compaction/compaction-tool-result-pruner/src/types.ts:13-18（ResolvedConfig）
//
// 新增: 构造它的唯一入口是 [Config.Resolve]，一份没验过的预算在类型上就传不进来
// ——和 compaction/basic、context/timecontext 那几份解析后配置同一个理由。
// DSH 那边靠 deepFreeze 加 structuredClone 做到「脱离原对象且不可变」，
// Go 的结构体值本来就是拷贝，这两件事都是白送的。
type ResolvedConfig struct {
	// ThresholdChars 是正数。
	ThresholdChars int
	// HeadChars 非负。
	HeadChars int
	// TailChars 非负。
	TailChars int
}

// Defaults 交出那份默认预算。
//
// 源: packages/compaction/compaction-tool-result-pruner/src/config.ts:10-13
//
// 新增: DSH 那个 DEFAULTS 是一个 deepFreeze 过的对象常量。Go 里包级变量拦不住
// 调用方改它，所以写成函数——每次交出去的都是一份新的拷贝。
func Defaults() ResolvedConfig {
	return ResolvedConfig{
		ThresholdChars: DefaultThresholdChars,
		HeadChars:      DefaultHeadChars,
		TailChars:      DefaultTailChars,
	}
}

// Resolve 补上默认值并把这份预算验一遍。
//
// 源: packages/compaction/compaction-tool-result-pruner/src/config.ts:36-64
//
// 新增: DSH 那个 CONFIG_KEYS 的拼错键检查整个消失——那边配置解出来是 unknown，
// 一个 `threshold: 10` 会被静默忽略；Go 这一侧字段由 [Config] 钉死了，
// 拼错的字段编译期就过不去。`Number.isInteger` 那一半同理，Go 的 int 已经挡掉了。
func (c Config) Resolve() (ResolvedConfig, error) {
	resolved := Defaults()
	if c.ThresholdChars != 0 {
		resolved.ThresholdChars = c.ThresholdChars
	}
	if c.HeadChars != nil {
		resolved.HeadChars = *c.HeadChars
	}
	if c.TailChars != nil {
		resolved.TailChars = *c.TailChars
	}

	if resolved.ThresholdChars <= 0 {
		return ResolvedConfig{}, configFailure("thresholdChars（%d）必须是正整数", resolved.ThresholdChars)
	}
	if resolved.HeadChars < 0 {
		return ResolvedConfig{}, configFailure("headChars（%d）必须是非负整数", resolved.HeadChars)
	}
	if resolved.TailChars < 0 {
		return ResolvedConfig{}, configFailure("tailChars（%d）必须是非负整数", resolved.TailChars)
	}

	// 砍完之后交出去的是「头 + 标记 + 尾」。它超过压力线的话，一条刚砍过的结果
	// 仍然在线上，下一趟又会去砍——而它已经砍无可砍了。
	emitted := resolved.HeadChars + utf8.RuneCountInString(PruneMarker) + resolved.TailChars
	if emitted > resolved.ThresholdChars {
		return ResolvedConfig{}, configFailure(
			"headChars + 标记 + tailChars（%d）不能超过 thresholdChars（%d）",
			emitted, resolved.ThresholdChars)
	}
	return resolved, nil
}

// configFailure 造一条配置失败。
func configFailure(format string, args ...any) error {
	return fmt.Errorf("%w：%s", ErrInvalidConfig, fmt.Sprintf(format, args...))
}
