// 本文件的作用：按码点数把一条过长的工具结果砍成「头 + 标记 + 尾」，
// 以及一趟砍下来的账目长什么样。
//
// 源: packages/compaction/compaction-tool-result-pruner/src/index.ts

package toolresultpruner

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"ds-harness-go/llm"
)

// ErrPruneFailed 表示砍出来的结果自己不自洽。
//
// 给一份验过的预算，这条错误报不出来——见 [Pruner.PruneContent] 里那两处判定
// 各自上面的说明。做成哨兵值是为了万一它真报出来了，调用方分得清这不是配置问题。
var ErrPruneFailed = errors.New("compaction/toolresultpruner: 砍出来的结果不自洽")

// PrunedEntry 是一次落地的表面替换的出处和账目。
//
// 源: packages/compaction/compaction-tool-result-pruner/src/types.ts:22-33
type PrunedEntry struct {
	// OriginalSeq 是被这次替换遮住的那条完整工具结果事件。
	OriginalSeq int
	// ReplacementSeq 是新追加进去的那条砍过的工具结果事件。
	ReplacementSeq int
	// CallID 是原件和替换件共有的那次工具调用。
	CallID llm.CallID
	// CharsBefore 是原来的正文有多少个码点。
	CharsBefore int
	// CharsAfter 是替换件的正文有多少个码点。
	CharsAfter int
}

// PruneResult 是一趟「在一份稳定的表面快照上砍一遍」的总账。
//
// 源: packages/compaction/compaction-tool-result-pruner/src/types.ts:35-40
type PruneResult struct {
	// Pruned 是这一趟的全部替换，按快照时的表面顺序。
	Pruned []PrunedEntry
	// CharsRemoved 是这一趟一共砍掉了多少个码点。
	CharsRemoved int
}

// Pruner 按固定的头／中／尾规则砍当前表面上的工具结果。
//
// 源: packages/compaction/compaction-tool-result-pruner/src/index.ts:44
//
// 它**不问模型**，所以同一份输入砍出来的永远是同一个结果——这正是它能在重放里
// 原样复现的原因，也是它和 compaction/basic 那个要发请求的后端的根本区别。
//
// 新增: DSH 是 `extends Service`，构造时把自己注册进 cordis 上下文。Go 里没有那个
// 注册表，这就是个普通的值，由装配方拿着。DSH 声明的 `static inject = ['tokenMeter']`
// 是给 pruneSession 用的——那一半归 docs/DESIGN.md 第八节第 6 块，见 [Pruner] 的
// 包文档说明。
type Pruner struct {
	config ResolvedConfig
}

// New 用一份写下来的配置造一个 [Pruner]，配置在这里就验过。
//
// 源: packages/compaction/compaction-tool-result-pruner/src/index.ts:58-61
func New(config Config) (*Pruner, error) {
	resolved, err := config.Resolve()
	if err != nil {
		return nil, err
	}
	return &Pruner{config: resolved}, nil
}

// Config 交出这个 [Pruner] 验过的字符预算。
//
// 源: packages/compaction/compaction-tool-result-pruner/src/index.ts:56
//
// 新增: DSH 那边是一个 readonly 字段。Go 里字段导出了就改得动，所以收成不可导出的
// 字段加这个读取方法——交出去的是一份拷贝。
func (p *Pruner) Config() ResolvedConfig { return p.config }

// MeasureContent 数一段内容里的文本有多少个码点；非文本的块一律算零。
//
// 源: packages/compaction/compaction-tool-result-pruner/src/index.ts:68-74
//
// 只数最外层的文本块。一条工具结果块里嵌的内容不计——那和 DSH 一致，
// 因为砍的也只是最外层的文本，两边用的必须是同一把尺子。
func (p *Pruner) MeasureContent(blocks llm.Content) int {
	chars := 0
	for _, block := range blocks {
		if text, ok := block.(llm.TextBlock); ok {
			chars += utf8.RuneCountInString(text.Text)
		}
	}
	return chars
}

// PruneContent 把超预算的那段文本中间换成标记，非文本的块原样留着、顺序不动。
//
// 源: packages/compaction/compaction-tool-result-pruner/src/index.ts:83-122
//
// 第二个返回值为假表示这段内容在预算之内，**没有砍**——那不是错误。
//
// 切的单位是码点不是字节，所以留下的边界不会把一个字符劈成半个。
// 更大的字素簇（比如带修饰符的 emoji 序列）仍然可能被切开，这一条和 DSH 相同：
// 那需要一整套字素分段，而这里要的只是「别砍出乱码」。
func (p *Pruner) PruneContent(blocks llm.Content) (llm.Content, bool, error) {
	totalChars := p.MeasureContent(blocks)
	if totalChars <= p.config.ThresholdChars {
		return nil, false, nil
	}

	// 要砍掉的是全体文本码点里 [removedStart, removedEnd) 这一段，
	// 下面按块把这个区间投影到每一块自己的下标上。
	removedStart := p.config.HeadChars
	removedEnd := totalChars - p.config.TailChars

	pruned := make(llm.Content, 0, len(blocks)+1)
	consumed := 0
	markerInserted := false
	for _, block := range blocks {
		text, ok := block.(llm.TextBlock)
		if !ok {
			pruned = append(pruned, block)
			continue
		}

		points := []rune(text.Text)
		blockStart := consumed
		blockEnd := blockStart + len(points)
		headEnd := min(len(points), max(0, removedStart-blockStart))
		tailStart := min(len(points), max(0, removedEnd-blockStart))

		marker := ""
		if blockStart < removedEnd && blockEnd > removedStart && !markerInserted {
			marker, markerInserted = PruneMarker, true
		}
		kept := string(points[:headEnd]) + marker + string(points[tailStart:])
		// 整块都落在被砍的区间里时它就空了，空块不留——一条内容里多出几个空文本块
		// 会让下游按块数做的判断（比如「这条结果只有一块」）无声地变意思。
		if kept != "" {
			pruned = append(pruned, llm.TextBlock{Text: kept})
		}
		consumed = blockEnd
	}

	// 下面两条给一份验过的预算都报不出来，留着是因为它们报不出来这件事**靠的是
	// 上面那段下标算术本身**：改动它而算错了，后果是一条正文被悄悄砍成了别的样子，
	// 而日志、事件、账目全都读得回来，没有任何地方会报警。
	//
	// 走不到的理由：总数 > 压力线 ≥ 头 + 标记 + 尾，所以 removedStart 严格小于
	// removedEnd，那一段里必定有文本，标记必定插得进去；而砍完之后剩下的恰好是
	// 头 + 标记 + 尾，它既不超过压力线，也严格小于总数。
	if !markerInserted {
		return nil, false, fmt.Errorf("%w：找不到要砍掉的那段文本", ErrPruneFailed)
	}
	charsAfter := p.MeasureContent(pruned)
	if charsAfter > p.config.ThresholdChars || charsAfter >= totalChars {
		return nil, false, fmt.Errorf("%w：砍完是 %d 个码点，砍之前是 %d，压力线是 %d",
			ErrPruneFailed, charsAfter, totalChars, p.config.ThresholdChars)
	}
	return pruned, true, nil
}
