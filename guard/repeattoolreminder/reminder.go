// 本文件的作用：配置怎么验、链的状态怎么存怎么推进，以及这一层怎么挂到执行后瀑布上。
//
// 源: packages/guard/repeat-tool-reminder/src/index.ts:17-233
//
// 文本的拼法和匹配那几件事在 text.go 里；这里只管状态和接线。

package repeattoolreminder

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"weak"

	"ds-harness-go/core/scope"
	"ds-harness-go/core/tools"
	"ds-harness-go/llm"
)

// pluginName 是这一层给自己的消息盖的那个来源标签。
//
// 源: packages/guard/repeat-tool-reminder/src/index.ts:57
//
// 这个标签是**承重**的：一条没有来源的上下文在派生历史里会被当成用户自己说的话，
// 于是模型下一轮会以为是用户在催它。
const pluginName = "repeat-tool-reminder"

// 缺省配置。
//
// 源: packages/guard/repeat-tool-reminder/src/index.ts:45-50
const (
	// defaultPreviewChars 是详细提醒里引用参数的字数上限。
	defaultPreviewChars = 500
	// minThreshold 是一个阈值能取的最小值：两次才谈得上「重复」。
	minThreshold = 2
)

// defaultThresholds 是不配置时的三级阈值。
//
// 源: packages/guard/repeat-tool-reminder/src/index.ts:46
var defaultThresholds = []int{3, 5, 8}

// ErrInvalidConfig 表示配置本身不成立，构造被拒。
//
// 源: packages/guard/repeat-tool-reminder/src/index.ts:128-146
//
// 配置错了要**响亮地**失败，不许静默退回缺省值：一份写着 `thresholds: []` 的配置
// 表达的是「我想改这件事」，把它读成「按默认来」等于让一次配置错误变成运行时的沉默。
var ErrInvalidConfig = errors.New("repeattoolreminder: 配置不成立")

// Config 是这一层的配置。
//
// 源: packages/guard/repeat-tool-reminder/src/index.ts:28-43
type Config struct {
	// Thresholds 是触发提醒的连续次数，nil 表示按缺省的 3/5/8。
	//
	// 每一个都必须 >= 2 且不重复；给了一个**空的**切片是错误，不是「按缺省」——
	// 这两件事的区别见 [ErrInvalidConfig]。
	Thresholds []int

	// Include 是要跟踪的工具名模式，空表示所有工具都跟踪。
	Include []string

	// Exclude 是对这条链完全透明的工具名模式：既不计数，也不断链。
	Exclude []string

	// ArgumentsPreviewChars 是详细提醒里引用参数的字数上限，0 表示按缺省的 500。
	//
	// 它只约束**给模型看的那段文字**，不约束判定：链的键永远比的是完整的规范化串。
	// 没有这个上限的话，一次 write 的正文或者一条长命令会原样搭车进下一次请求——
	// 而那恰好发生在一个已经在打转的场景里。
	//
	// 新增: DSH 那边 0 是一个**非法值**（必须 >= 1），所以 Go 这一侧拿零值当
	// 「没给」不会丢掉任何能表达的东西：0 在两边都不是一个能生效的配置。
	ArgumentsPreviewChars int
}

// chain 是一个 agent 当前那条连续重复：上一次被跟踪的调用的身份，和它连着出现的次数。
//
// 源: packages/guard/repeat-tool-reminder/src/index.ts:152-155
type chain struct {
	key   string
	count int
}

// Reminder 是这一层的状态：验好的配置，加上每个 agent 各自那条链。
//
// 源: packages/guard/repeat-tool-reminder/src/index.ts:162-174
type Reminder struct {
	thresholds   []int
	thresholdSet map[int]struct{}
	include      []pattern
	exclude      []pattern
	previewChars int

	mu sync.Mutex
	// chains 是每个 agent 的链，键是**弱**引用。
	//
	// 新增: DSH 用 WeakMap，键是 agent 对象。Go 里对应的是 weak.Pointer——
	// 一个用完就不再有人引用的 agent 不该因为这张表而留在内存里。表里的键
	// 死掉之后那一条会在下一次写入时被扫掉，见 advance。
	chains map[weak.Pointer[scope.Key]]chain
}

// New 验一份配置，造出这一层。
//
// 源: packages/guard/repeat-tool-reminder/src/index.ts:162-174
func New(config Config) (*Reminder, error) {
	thresholds, err := normalizeThresholds(config.Thresholds)
	if err != nil {
		return nil, err
	}
	previewChars := config.ArgumentsPreviewChars
	if previewChars == 0 {
		previewChars = defaultPreviewChars
	}
	if previewChars < 0 {
		return nil, fmt.Errorf("%w: ArgumentsPreviewChars 是 %d，必须是正数", ErrInvalidConfig, previewChars)
	}
	thresholdSet := make(map[int]struct{}, len(thresholds))
	for _, value := range thresholds {
		thresholdSet[value] = struct{}{}
	}
	return &Reminder{
		thresholds:   thresholds,
		thresholdSet: thresholdSet,
		include:      compilePatterns(config.Include),
		exclude:      compilePatterns(config.Exclude),
		previewChars: previewChars,
		chains:       map[weak.Pointer[scope.Key]]chain{},
	}, nil
}

// normalizeThresholds 验阈值并按升序排好。
//
// 源: packages/guard/repeat-tool-reminder/src/index.ts:128-146
//
// 排序在这里做一次，因为升级规则读的是 thresholds[0]——「最小的那一级说得客气些」
// 是这条规则的本意，而不是「作者写在最前面的那一级」。
func normalizeThresholds(values []int) ([]int, error) {
	if values == nil {
		return defaultThresholds, nil
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("%w: Thresholds 给了一个空的切片；想按缺省就别给（留 nil）", ErrInvalidConfig)
	}
	seen := make(map[int]struct{}, len(values))
	sorted := make([]int, 0, len(values))
	for _, value := range values {
		if value < minThreshold {
			return nil, fmt.Errorf("%w: 阈值 %d 太小，每一级都必须 >= %d", ErrInvalidConfig, value, minThreshold)
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, fmt.Errorf("%w: 阈值 %d 写了两遍", ErrInvalidConfig, value)
		}
		seen[value] = struct{}{}
		sorted = append(sorted, value)
	}
	slices.Sort(sorted)
	return sorted, nil
}

// Install 把这一层挂到一个工具注册表的执行后瀑布上，返回撤销它的函数。
//
// 源: packages/guard/repeat-tool-reminder/src/index.ts:213-227
//
// owner 决定它管哪些 agent：[scope.NewRoot] 造的作用域没有身份，规则落全局层、
// 管所有 agent；有身份的作用域只管那条链下面的。
func (r *Reminder) Install(ctx context.Context, runtime *tools.Runtime, owner *scope.Scope) (func(context.Context) error, error) {
	if runtime == nil {
		return nil, errors.New("repeattoolreminder: 需要一个工具注册表")
	}
	rule := func(exec tools.Execution, _ tools.Result, next func() (tools.PostDecision, error)) (tools.PostDecision, error) {
		// 先计数再往下走：链的状态只记「模型又调了一次」这件事，
		// 和后面那些层最终怎么裁决无关。
		reminder, remind := r.Observe(exec)
		decision, err := next()
		if err != nil || !remind {
			return decision, err
		}
		// 插在最前面，后面那些层自己挂的上下文原样留在后面。
		//
		// 新增: DSH 那边 accept 和 block 是两个不同形状的对象，得分别重建；
		// Go 的 PostDecision 是一个结构体，两种裁决共用 AdditionalContexts 这个字段，
		// 所以这里只有一条路——被拦下的调用一样收得到这句提醒。
		decision.AdditionalContexts = append([]llm.Message{reminder}, decision.AdditionalContexts...)
		return decision, nil
	}
	return runtime.PostExecute(ctx, owner, rule)
}

// Observe 为一次调用推进它那个 agent 的链，命中阈值时给出要捎的那条提醒。
//
// 源: packages/guard/repeat-tool-reminder/src/index.ts:189-211
//
// 导出它是为了让「什么算重复」这条规则能被单独测、也能被别的接线方式复用；
// [Reminder.Install] 挂上去的那条规则做的就是调它一次。
func (r *Reminder) Observe(exec tools.Execution) (llm.Message, bool) {
	// 直接调 Runtime.Execute 的调用方没有模型可提醒，也没有身份可以作为链的键；
	// 只有 agent 循环发起的调用参与这件事。
	if exec.Agent == nil || !r.tracked(exec.Name) {
		return llm.Message{}, false
	}
	canonical := canonicalize(exec.Arguments)
	count := r.advance(exec.Agent, callKey(exec.Name, canonical))
	if _, hit := r.thresholdSet[count]; !hit {
		return llm.Message{}, false
	}
	text := detailedReminder(exec.Name, count, previewArguments(canonical, r.previewChars))
	if count == r.thresholds[0] {
		text = gentleReminder
	}
	return llm.NewUserMessage(
		llm.Content{llm.TextBlock{Text: text}},
		llm.PluginSource{
			Plugin:  pluginName,
			Context: llm.NoticeContext{Summary: fmt.Sprintf("%s × %d", exec.Name, count)},
		},
	), true
}

// NoticeStep 是 agent 循环每走一步之前该调的那一下：用户插了话就把链断掉。
//
// 源: packages/guard/repeat-tool-reminder/src/index.ts:229-232
//
// 「什么算用户插话」这条判断留在本包，见包文档里那段说明。
func (r *Reminder) NoticeStep(agent *scope.Key, messages []llm.Message) {
	if agent == nil {
		return
	}
	for _, message := range messages {
		if message.Source == nil || message.Source.SourceKind() != llm.SourceUser {
			continue
		}
		r.mu.Lock()
		delete(r.chains, weak.Make(agent))
		r.mu.Unlock()
		return
	}
}

// tracked 说明一个工具参不参与这条链。
//
// 源: packages/guard/repeat-tool-reminder/src/index.ts:176-179
func (r *Reminder) tracked(name string) bool {
	if len(r.include) > 0 && !matchesAny(r.include, name) {
		return false
	}
	return !matchesAny(r.exclude, name)
}

// advance 把一个 agent 的链推进一次，交出这次调用的连续次数。
//
// 源: packages/guard/repeat-tool-reminder/src/index.ts:196-199
//
// 顺手把键已经死掉的那些条目扫掉。扫的代价是一次全表遍历，而这张表的规模是
// **活着的 agent 数**（几十条），比维护一个「攒够多少条才扫」的阈值便宜得多，
// 也少了一个调不准就会退化成泄漏的旋钮。
func (r *Reminder) advance(agent *scope.Key, key string) int {
	handle := weak.Make(agent)
	r.mu.Lock()
	defer r.mu.Unlock()
	for existing := range r.chains {
		if existing != handle && existing.Value() == nil {
			delete(r.chains, existing)
		}
	}
	count := 1
	if previous, ok := r.chains[handle]; ok && previous.key == key {
		count = previous.count + 1
	}
	r.chains[handle] = chain{key: key, count: count}
	return count
}
