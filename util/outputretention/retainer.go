// 本文件的作用：两个留存器本身，以及它们在切口上守 UTF-8 边界用的那两个助手。
//
// 源: packages/util/output-retention/src/index.ts:136-400

package outputretention

import (
	"strings"
	"unicode/utf8"
)

// ItemRetainer 约束一条有序的逻辑单元流，保留最前面的 maxItems 个。
//
// 源: packages/util/output-retention/src/index.ts:136-198
//
// 分组、排序、路径映射、每个单元自己的预览截断、以及任何「不完整」状态都留在留存器
// **外面**：它只数数和留下，别的什么都不做。调用方喂进来的是已经准备好的逻辑单元，
// [ItemRetainer.Finish] 之后自己去给留下的那批分组和排序。
type ItemRetainer[T any] struct {
	maxItems     int
	items        []T
	seen         int
	omittedCount int
}

// NewItemRetainer 按策略造一个单元留存器。
//
// 新增: DSH 的构造函数在预算不合法时直接抛异常。Go 这边由返回值报出来——一个库
// 因为调用方传了个负数就把整个进程 panic 掉，是把调用方的配置错误升级成了事故。
func NewItemRetainer[T any](strategy ItemRetentionStrategy) (*ItemRetainer[T], error) {
	if err := strategy.Validate(); err != nil {
		return nil, err
	}
	return &ItemRetainer[T]{maxItems: strategy.maxItems}, nil
}

// Push 递一个单元进来。没到上限就留下，到了就丢掉并记一笔。
//
// 源: packages/util/output-retention/src/index.ts:158-179
//
// 调用方要把**看见的每一个**单元都推进来，最终那个丢弃计数才是精确的。
func (r *ItemRetainer[T]) Push(item T) PushDecision {
	r.seen++
	if len(r.items) < r.maxItems {
		// 只有在上限之下、任何丢弃发生之前才走到这里（items 只增不减，上限是固定的），
		// 所以此刻一定还没丢过东西，Truncated 恒为 false。
		r.items = append(r.items, item)
		return PushDecision{Kept: true, Truncated: false}
	}
	r.omittedCount++
	return PushDecision{Kept: false, Truncated: true}
}

// Finish 收尾，给出留下了什么、丢了什么。
//
// 源: packages/util/output-retention/src/index.ts:181-197
//
// 返回的 Items 就是留存器内部那个切片本身，调用方可以原地排序或分组。
// 代价是 Finish 之后不该再 Push——那会改到已经交出去的切片。
func (r *ItemRetainer[T]) Finish() RetainedItems[T] {
	truncated := r.omittedCount > 0
	omitted := OmittedNone()
	if truncated {
		omitted = OmittedExact(r.omittedCount)
	}
	return RetainedItems[T]{
		Items:     r.items,
		Truncated: truncated,
		Seen:      r.seen,
		Kept:      len(r.items),
		Omitted:   omitted,
	}
}

// replacementChar 是解码时给非法字节的替身。
//
// 源: packages/util/output-retention/src/index.ts:200-201
//
// 新增: DSH 用一个非致命的 TextDecoder，非法字节自动变成 U+FFFD。Go 的 string(bytes)
// 是**保字节**的，非法序列会原样留在字符串里，然后在很远的地方（比如 json.Marshal）
// 才被悄悄换掉。在这里换掉，输出就和 DSH 一样是一个合法的 UTF-8 字符串。
//
// 一处刻意的差异：strings.ToValidUTF8 把**连续一段**非法字节合并成一个 U+FFFD，
// 而 TextDecoder 是每个最大子部件一个。替换字符的**个数**从来不是这个库的契约
// （契约是「不因为切割而引入替换字符」），所以不为了对齐个数再写一遍解码器。
const replacementChar = "�"

// trimTrailingPartialUTF8 砍掉结尾那个**不完整**的 UTF-8 序列，让前缀切口不会渲染出
// 一个替换字符。
//
// 源: packages/util/output-retention/src/index.ts:203-222
//
// 先往回走过续接字节（10xxxxxx）找到首字节，再看从首字节起是不是一个完整的编码。
// 完整的、或者压根就不是合法首字节的，原样返回——真正畸形的内容留给解码器去换成
// 替换字符，那不是这个函数的事。
//
// 新增: 用 utf8.FullRune 而不是照抄 DSH 那个「首字节 → 期望长度」的算式。FullRune 的
// 语义正好是「短且仍可能合法」才算不完整，非法编码一律算完整——这恰恰就是 DSH 注释里
// 写的意图。DSH 自己的算式认不出 0xC0、0xC1、0xF5-0xF7 这些非法首字节，会把结尾一个
// 0xC0 当成「半个两字节序列」砍掉；Go 这一版把它留给解码器。两者都不会因为切割而引入
// 替换字符，而后者和 DSH 自己声明的契约一致。
func trimTrailingPartialUTF8(bytes []byte) []byte {
	index := len(bytes) - 1
	// 续接字节最多往回走 3 个（UTF-8 最长 4 字节）。这个上界不只是省事：没有它，
	// 一段全是续接字节的缓冲区会让这里退化成 O(n)。
	for index >= 0 && !utf8.RuneStart(bytes[index]) && len(bytes)-index <= 3 {
		index--
	}
	if index < 0 {
		return bytes
	}
	if utf8.FullRune(bytes[index:]) {
		return bytes
	}
	return bytes[:index]
}

// trimLeadingContinuationUTF8 砍掉开头的续接字节（10xxxxxx），让后缀切口从一个
// 首字节或 ASCII 字节开始，而不是从半个码点中间开始。
//
// 源: packages/util/output-retention/src/index.ts:224-233
func trimLeadingContinuationUTF8(bytes []byte) []byte {
	index := 0
	for index < len(bytes) && !utf8.RuneStart(bytes[index]) {
		index++
	}
	return bytes[index:]
}

// TextRetainer 约束一条面向字节的文本流，留头、留尾、或者两头都留。
//
// 源: packages/util/output-retention/src/index.ts:235-387
//
// 三种策略共用同一套前缀/后缀累加器：head 只有前缀，tail 只有后缀，headTail 两边都有。
//
// 计的是**字节**不是字符：上限和 OmittedBytes 都是字节数，因为进程输出和网页正文的
// 安全边界本来就是字节。跨码点的分块是被处理过的——[TextRetainer.Finish] 会在每一处
// 切口上砍掉半个码点，于是返回的文本绝不会出现由这次切割引入的替换字符。
//
// 新增: 后缀改成一个上限为 suffixCap 的滑动缓冲，而不是 DSH 那个「分块列表 + 挪走
// 整块 + 再修头一块」的结构。原因是 Go 这边**必须拷贝**（见 [TextRetainer.Push]），
// 既然横竖要拷，就直接维护一段定长的尾巴：内存占用从 DSH 的
// `prefixCap + tailBytes + 一整块` 降到 `prefixCap + suffixCap`，逻辑也少了一半。
type TextRetainer struct {
	prefixCap int
	suffixCap int
	prefix    []byte
	suffix    []byte
	total     int
}

// NewTextRetainer 按策略造一个文本留存器。理由同 [NewItemRetainer]。
func NewTextRetainer(strategy TextRetentionStrategy) (*TextRetainer, error) {
	if err := strategy.Validate(); err != nil {
		return nil, err
	}
	prefixCap, suffixCap := strategy.caps()
	return &TextRetainer{prefixCap: prefixCap, suffixCap: suffixCap}, nil
}

// Push 递一块字节进来。前缀填到上限就不再填；后缀滚动，只留最后 suffixCap 个字节。
// Kept 只有在这一块**一个字节都没被丢掉**时才是 true。
//
// 源: packages/util/output-retention/src/index.ts:278-335
//
// 新增: 这里把字节**拷贝**下来，而 DSH 存的是 subarray——也就是调用方那个缓冲区的视图。
// Go 里读流的标准写法是 `buf := make([]byte, 32*1024)` 然后循环 `r.Read(buf)` 复用同一个
// 缓冲区，存视图的话所有留下来的块最后都指向同一段内存，全部变成最后一次读到的内容。
// 而这个库最主要的使用场景（bash 的 stdout）恰恰就是那个写法，且症状是**静默的**：
// 长度对、丢弃计数对，只有内容是错的。
func (r *TextRetainer) Push(chunk []byte) PushDecision {
	before := r.total
	r.total += len(chunk)

	// 前缀：只收到上限为止，这一块剩下的部分不进前缀。
	if room := r.prefixCap - len(r.prefix); room > 0 {
		r.prefix = append(r.prefix, chunk[:min(room, len(chunk))]...)
	}

	// 后缀：滚动窗口，始终恰好是已见字节的最后 min(total, suffixCap) 个。
	if r.suffixCap > 0 {
		if len(chunk) >= r.suffixCap {
			// 这一块自己就填满了窗口，之前攒的全部作废。
			r.suffix = append(r.suffix[:0], chunk[len(chunk)-r.suffixCap:]...)
		} else {
			if overflow := len(r.suffix) + len(chunk) - r.suffixCap; overflow > 0 {
				// copy 是 memmove 语义，源和目标重叠是安全的。
				r.suffix = r.suffix[:copy(r.suffix, r.suffix[overflow:])]
			}
			r.suffix = append(r.suffix, chunk...)
		}
	}

	// 丢掉的 = 两头都留不下的那些字节。累计丢弃量用和 Finish **同一个**算式
	// （omittedAt）算出来，于是 Push 和 Finish 永远不会各说各话。
	return PushDecision{
		Kept:      r.omittedAt(r.total) <= r.omittedAt(before),
		Truncated: r.omittedAt(r.total) > 0,
	}
}

// PushString 是 [TextRetainer.Push] 的字符串形态，对应 DSH 那个
// `Uint8Array | string` 的重载。Go 的字符串转字节本来就要拷一次，所以没有额外代价。
func (r *TextRetainer) PushString(chunk string) PushDecision {
	return r.Push([]byte(chunk))
}

// omittedAt 给出「已经看见 total 个字节」时丢掉了多少：total 减去留下的头和尾。
//
// 源: packages/util/output-retention/src/index.ts:337-342
func (r *TextRetainer) omittedAt(total int) int {
	prefixLen := min(total, r.prefixCap)
	suffixLen := min(total-prefixLen, r.suffixCap)
	return total - prefixLen - suffixLen
}

// Finish 收尾：把留下的头和尾各自在切口上修到 UTF-8 边界，解码，并给出**精确的**
// 丢弃字节数。
//
// 源: packages/util/output-retention/src/index.ts:344-386
func (r *TextRetainer) Finish() RetainedText {
	prefixLen := min(r.total, r.prefixCap)
	suffixLen := min(r.total-prefixLen, r.suffixCap)

	prefix := r.prefix                           // 恰好 prefixLen 字节
	suffix := r.suffix[len(r.suffix)-suffixLen:] // 滑动窗口里最后 suffixLen 字节

	budgetOmitted := r.omittedAt(r.total)

	keptPrefix, keptSuffix := prefix, suffix
	var text string
	if budgetOmitted > 0 {
		// 真的丢了一段，头和尾各自是一个**真实的**切口：各修各的边界、各自解码，
		// 于是绝不会隔着那个缺口把两半凑成一个码点。
		keptPrefix = trimTrailingPartialUTF8(prefix)
		keptSuffix = trimLeadingContinuationUTF8(suffix)
		text = toValidUTF8(keptPrefix) + toValidUTF8(keptSuffix)
	} else {
		// 预算一个字节都没丢时，前缀和后缀是同一条流上**相邻**的两段
		// （prefixLen + suffixLen == total），头尾之间那道分界线是人为的，
		// 一个码点完全可能跨在上面。所以当成一整块解码——分开修边界或者分开解码，
		// 会在什么内容都没丢的情况下把那个跨界的码点弄坏。
		joined := make([]byte, 0, len(prefix)+len(suffix))
		joined = append(joined, prefix...)
		joined = append(joined, suffix...)
		text = toValidUTF8(joined)
	}

	// 丢弃量按**实际返回的**字节算，而不是按修边界之前的预算算：修边界会把半个码点的
	// 那几个字节也丢掉，只按预算报会高估留下来的内容，那么任何据此写出的
	// 「Omitted N bytes」都是一句谎话。
	omitted := r.total - len(keptPrefix) - len(keptSuffix)
	truncated := omitted > 0

	omittedBytes := OmittedNone()
	if truncated {
		omittedBytes = OmittedExact(omitted)
	}
	return RetainedText{Text: text, Truncated: truncated, OmittedBytes: omittedBytes}
}

// toValidUTF8 把字节解成一个合法的 UTF-8 字符串，非法段换成替换字符。见 [replacementChar]。
func toValidUTF8(bytes []byte) string {
	return strings.ToValidUTF8(string(bytes), replacementChar)
}
