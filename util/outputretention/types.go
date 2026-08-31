// Package outputretention 是一个只管「留多少、丢多少」的有界输出库：调用方把一条条
// 逻辑单元或一块块文本喂进来，拿回留下的内容加上**精确的**丢弃元数据。
//
// 对应 DSH 的 @deepseek-ai/dsh-output-retention（packages/util/output-retention）。
//
// 源: packages/util/output-retention/src/index.ts:1-31
//
// # 这个库只回答机械问题
//
// 「留了什么、丢了什么」是全部。文件分组、行号、退出码、上游的部分失败、每行预览的
// 截断、溢出文件、给模型看的措辞——都归工具自己。
//
// 尤其是 [RetainedItems.Truncated] 和 [RetainedText.Truncated] 的含义是
// 「**因为预算**丢掉了本来拿得到的内容」，而不是「上游本身就不完整」。权限失败、
// 跳过的二进制文件、上游的部分失败、读不了的候选项，一律留在工具自己的字段里，
// 绝不折进 Truncated——折进去之后，读的人再也分不出「是我们截的」和「本来就缺」。
//
// # 两个留存器为什么是两个名字
//
// 它们的资源模型不同：[ItemRetainer] 约束的是有序的逻辑单元（路径、grep 命中、
// 检索来源），只有 head 一种策略；[TextRetainer] 约束的是面向字节的文本流
// （bash 的 stdout/stderr、网页正文），有 head / tail / headTail 三种，并在
// [TextRetainer.Finish] 处守住 UTF-8 边界。
//
// # 这不是一个服务
//
// 它不收 ctx、不注册任何东西、不发事件。两个留存器是仅有的有状态部分，而状态是
// 每个实例自己的一次累积，绝不跨调用。
package outputretention

import (
	"fmt"
	"strings"
)

// OmittedKind 是丢弃元数据三种形态的判别标签。
//
// 源: packages/util/output-retention/src/index.ts:33-43
type OmittedKind uint8

const (
	// OmittedKindUnset 是 [Omitted] 的零值，**非法**。
	//
	// 新增: DSH 那边是可判别联合，漏填是编译错误。Go 里结构体字面量可以只写一半，
	// 而如果把 none 放在零值位，一次漏填就会渲染成「什么都没丢」——这个库存在的
	// 全部意义就是不说这种谎。所以零值单独占一个非法标签，由 [Omitted.Kind] 报出来。
	OmittedKindUnset OmittedKind = iota

	// OmittedKindNone 表示一个都没丢。
	OmittedKindNone

	// OmittedKindExact 表示丢了确切的若干个——留存器自己产出的永远是这一种，
	// 因为每一个单元/字节都经过它的手。
	OmittedKindExact

	// OmittedKindUnknown 留给「丢了但数不出来」的调用方；留存器自己**绝不**产出它。
	OmittedKindUnknown
)

// Omitted 是丢弃了多少内容。
//
// 源: packages/util/output-retention/src/index.ts:33-43
//
// 新增: 判别标签是非导出字段，合法值只能经 [OmittedNone] / [OmittedExact] /
// [OmittedUnknown] 造出来，理由见 [OmittedKindUnset]。
type Omitted struct {
	kind  OmittedKind
	count int
}

// OmittedNone 表示一个都没丢。
func OmittedNone() Omitted { return Omitted{kind: OmittedKindNone} }

// OmittedExact 表示丢了确切的 count 个。
func OmittedExact(count int) Omitted {
	return Omitted{kind: OmittedKindExact, count: count}
}

// OmittedUnknown 表示丢了内容但没有计数。
func OmittedUnknown() Omitted { return Omitted{kind: OmittedKindUnknown} }

// Kind 给出这是三者中的哪一个，供调用方分派。
func (o Omitted) Kind() OmittedKind { return o.kind }

// Count 取出 [OmittedKindExact] 携带的计数；不是这个形态时第二个返回值为 false。
func (o Omitted) Count() (int, bool) {
	if o.kind != OmittedKindExact {
		return 0, false
	}
	return o.count, true
}

// PushDecision 是每次 Push 之后调用方拿到的回执。
//
// 源: packages/util/output-retention/src/index.ts:45-53
type PushDecision struct {
	// Kept 表示这一个单元 / 这一块的每一个字节都留下来了，一点没丢。
	Kept bool

	// Truncated 是累计量：到目前为止留存器有没有因为预算丢过东西。
	Truncated bool
}

// RetainedItems 是有序逻辑单元的最终结果。
//
// 源: packages/util/output-retention/src/index.ts:55-68
//
// Seen 是留存器**看见过**的单元数，不一定是上游的总数——上游有多少只有上游知道。
// Kept 就是 len(Items)，单独给出来是为了让格式化的人不用再数一遍。
type RetainedItems[T any] struct {
	Items     []T
	Truncated bool
	Seen      int
	Kept      int
	Omitted   Omitted
}

// RetainedText 是文本流的最终结果。
//
// 源: packages/util/output-retention/src/index.ts:70-84
//
// Text 可以直接交给格式化的人：留存器不加任何工具特有的头部、退出标记、XML 标签或
// 恢复指引。OmittedBytes 数的是**字节**，不是字符也不是行——文本留存是面向字节的，
// 因为进程输出和网页正文的安全边界本来就是字节。每一处切口的 UTF-8 边界都被守住，
// 所以 Text 里绝不会出现**由这次切割引入**的替换字符。
type RetainedText struct {
	Text         string
	Truncated    bool
	OmittedBytes Omitted
}

// ItemRetentionStrategy 是单元留存策略。
//
// 源: packages/util/output-retention/src/index.ts:86-91
//
// v1 只有 head 一种；窗口式、分组式的预算等到有第二个使用方再说。
//
// 新增: 做成零值非法的标签结构体而不是一个裸的 int 字段。maxItems 为 0 是**合法**的
// （表示一个都不留），于是一个漏填的 ItemRetentionStrategy{} 会安静地把所有单元丢光。
type ItemRetentionStrategy struct {
	kind     itemStrategyKind
	maxItems int
}

type itemStrategyKind uint8

const (
	itemStrategyUnset itemStrategyKind = iota
	itemStrategyHead
)

// ItemHead 保留最前面的 maxItems 个单元。glob、grep、检索来源都用它。
func ItemHead(maxItems int) ItemRetentionStrategy {
	return ItemRetentionStrategy{kind: itemStrategyHead, maxItems: maxItems}
}

// Validate 检查策略被显式选过，且预算是非负的。
//
// 新增: DSH 的 assertBudget 还查了 Number.isInteger——Go 这边预算的类型就是 int，
// 那个分支没有产出方，于是只剩下负数这一条。
func (s ItemRetentionStrategy) Validate() error {
	if s.kind == itemStrategyUnset {
		return fmt.Errorf("outputretention: 单元留存策略没有指定（请用 ItemHead 造一个）")
	}
	if s.maxItems < 0 {
		return fmt.Errorf("outputretention: maxItems 必须是非负整数，收到 %d", s.maxItems)
	}
	return nil
}

// TextRetentionStrategy 是文本留存策略：留前缀、留后缀、或者两头都留，按**字节**计。
//
// 源: packages/util/output-retention/src/index.ts:93-110
//
// 新增: 零值非法，理由同 [ItemRetentionStrategy]。
type TextRetentionStrategy struct {
	kind      textStrategyKind
	headBytes int
	tailBytes int
}

type textStrategyKind uint8

const (
	textStrategyUnset textStrategyKind = iota
	textStrategyHead
	textStrategyTail
	textStrategyHeadTail
)

// TextHead 保留最前面的 maxBytes 个字节。
func TextHead(maxBytes int) TextRetentionStrategy {
	return TextRetentionStrategy{kind: textStrategyHead, headBytes: maxBytes}
}

// TextTail 保留最后的 maxBytes 个字节，要求读到流的末尾。
func TextTail(maxBytes int) TextRetentionStrategy {
	return TextRetentionStrategy{kind: textStrategyTail, tailBytes: maxBytes}
}

// TextHeadTail 保留一段稳定的头和一段稳定的尾，丢掉中间，要求读到流的末尾。
func TextHeadTail(headBytes, tailBytes int) TextRetentionStrategy {
	return TextRetentionStrategy{kind: textStrategyHeadTail, headBytes: headBytes, tailBytes: tailBytes}
}

// Validate 检查策略被显式选过，且用到的那几个预算是非负的。
func (s TextRetentionStrategy) Validate() error {
	switch s.kind {
	case textStrategyUnset:
		return fmt.Errorf("outputretention: 文本留存策略没有指定（请用 TextHead / TextTail / TextHeadTail 造一个）")
	case textStrategyHead:
		if s.headBytes < 0 {
			return fmt.Errorf("outputretention: maxBytes 必须是非负整数，收到 %d", s.headBytes)
		}
	case textStrategyTail:
		if s.tailBytes < 0 {
			return fmt.Errorf("outputretention: maxBytes 必须是非负整数，收到 %d", s.tailBytes)
		}
	case textStrategyHeadTail:
		if s.headBytes < 0 {
			return fmt.Errorf("outputretention: headBytes 必须是非负整数，收到 %d", s.headBytes)
		}
		if s.tailBytes < 0 {
			return fmt.Errorf("outputretention: tailBytes 必须是非负整数，收到 %d", s.tailBytes)
		}
	}
	return nil
}

// caps 给出这个策略的前缀上限和后缀上限，供 [TextRetainer] 用一套累加器服务三种策略。
func (s TextRetentionStrategy) caps() (prefixCap, suffixCap int) {
	switch s.kind {
	case textStrategyHead:
		return s.headBytes, 0
	case textStrategyTail:
		return 0, s.tailBytes
	case textStrategyHeadTail:
		return s.headBytes, s.tailBytes
	}
	return 0, 0
}

// NoticeStrategy 是留存通知里那个策略名。
//
// 源: packages/util/output-retention/src/index.ts:112-127
//
// 新增: DSH 是字面量联合类型，写错一个字母是编译错误；Go 的具名字符串类型拦不住
// NoticeStrategy("headtail")，所以由 [RetentionNotice.Validate] 查一遍。
type NoticeStrategy string

const (
	NoticeHead     NoticeStrategy = "head"
	NoticeTail     NoticeStrategy = "tail"
	NoticeHeadTail NoticeStrategy = "headTail"
)

// Valid 判断这是不是上面三个之一。
func (s NoticeStrategy) Valid() bool {
	switch s {
	case NoticeHead, NoticeTail, NoticeHeadTail:
		return true
	}
	return false
}

// Unit 是被丢弃的那个量的名词，会**原样出现在给模型看的文案里**。
//
// 源: packages/util/output-retention/src/index.ts:112-127
type Unit string

const (
	UnitItems Unit = "items"
	UnitBytes Unit = "bytes"
	UnitChars Unit = "chars"
	UnitLines Unit = "lines"
)

// Valid 判断这是不是上面四个之一。
//
// 空串会渲染出 "Omitted 3 ." 这种残句，所以必须查。
func (u Unit) Valid() bool {
	switch u {
	case UnitItems, UnitBytes, UnitChars, UnitLines:
		return true
	}
	return false
}

// LimitKind 是留存上限两种形态的判别标签。
type LimitKind uint8

const (
	// LimitKindUnset 是 [Limit] 的零值，**非法**。
	LimitKindUnset LimitKind = iota

	// LimitKindCount 是单个数字的上限，对应 head 和 tail。
	LimitKindCount

	// LimitKindHeadTail 是一对上限，对应 headTail。
	LimitKindHeadTail
)

// Limit 是通知里那个上限。
//
// 源: packages/util/output-retention/src/index.ts:112-127
//
// 新增: DSH 那边是 `number | { head, tail }`。Go 里做成零值非法的标签结构体，
// 理由同 [Omitted]：漏填会变成一个「上限是 0」的通知，而 0 是个合法上限。
type Limit struct {
	kind LimitKind
	head int
	tail int
}

// LimitCount 是单个数字的上限。
func LimitCount(count int) Limit { return Limit{kind: LimitKindCount, head: count} }

// LimitHeadTail 是头尾各一个的上限。
func LimitHeadTail(head, tail int) Limit {
	return Limit{kind: LimitKindHeadTail, head: head, tail: tail}
}

// Kind 给出这是两者中的哪一个。
func (l Limit) Kind() LimitKind { return l.kind }

// Count 取出 [LimitKindCount] 携带的那个数；不是这个形态时第二个返回值为 false。
func (l Limit) Count() (int, bool) {
	if l.kind != LimitKindCount {
		return 0, false
	}
	return l.head, true
}

// HeadTail 取出 [LimitKindHeadTail] 携带的那一对；不是这个形态时第三个返回值为 false。
func (l Limit) HeadTail() (head, tail int, ok bool) {
	if l.kind != LimitKindHeadTail {
		return 0, 0, false
	}
	return l.head, l.tail, true
}

// RetentionNotice 是一次留存结果的中立描述，也就是 [FormatRetentionNotice] 的输入。
//
// 源: packages/util/output-retention/src/index.ts:112-127
//
// 它只带机械事实（策略、单位、上限、留了多少、丢了多少）；**恢复的措辞由工具给**，
// 因为只有工具知道该怎么恢复（「把 pattern 收窄」「换一个更具体的 URL」
// 「去读那个溢出文件」）。
type RetentionNotice struct {
	// Scope 是工具或范围的标签，比如 grep、web_fetch、bash stdout。
	//
	// 这个库自己不读它，只把整个通知转交给调用方给的恢复措辞函数，所以不校验。
	Scope string

	Strategy NoticeStrategy
	Unit     Unit
	Limit    Limit
	Kept     int
	Omitted  Omitted
}

// Validate 检查通知里那几个会被渲染成文案的字段都填对了。
//
// 新增: DSH 靠类型系统保证这些字段在场且取值合法，Go 里没有等价物。不查的话，
// 一个漏填的 Omitted 会渲染成空子句——footer 看上去像是「什么都没丢」，
// 而这个库存在的全部意义就是不说这种谎。
func (n RetentionNotice) Validate() error {
	if !n.Strategy.Valid() {
		return fmt.Errorf("outputretention: 策略名 %q 不是 head / tail / headTail 之一", n.Strategy)
	}
	if !n.Unit.Valid() {
		return fmt.Errorf("outputretention: 单位 %q 不是 items / bytes / chars / lines 之一", n.Unit)
	}
	if n.Limit.kind == LimitKindUnset {
		return fmt.Errorf("outputretention: 上限没有指定（请用 LimitCount 或 LimitHeadTail 造一个）")
	}
	if n.Kept < 0 {
		return fmt.Errorf("outputretention: 留下的数量不能是负数，收到 %d", n.Kept)
	}
	if n.Omitted.kind == OmittedKindUnset {
		return fmt.Errorf("outputretention: 丢弃元数据没有指定（请用 OmittedNone / OmittedExact / OmittedUnknown 造一个）")
	}
	if count, exact := n.Omitted.Count(); exact && count < 0 {
		return fmt.Errorf("outputretention: 丢弃的数量不能是负数，收到 %d", count)
	}
	return nil
}

// DescribeOmitted 把一个 [Omitted] 渲染成标准化的、不会假装精确的一句话。
//
// 源: packages/util/output-retention/src/index.ts:402-421
//
// exact 印出计数（`Omitted 3 items.`）；unknown **不印计数**，因为调用方压根没给；
// none 是空串。这句文案是给模型看的，所以是英文。
//
// 新增: DSH 那边靠穷尽的 switch，漏一个分支是编译错误，所以它不返回错误。Go 这边
// 零值和非法单位都进得来，而两者都会渲染出残句，于是由第二个返回值报出来。
func DescribeOmitted(omitted Omitted, unit Unit) (string, error) {
	if !unit.Valid() {
		return "", fmt.Errorf("outputretention: 单位 %q 不是 items / bytes / chars / lines 之一", unit)
	}
	switch omitted.kind {
	case OmittedKindNone:
		return "", nil
	case OmittedKindExact:
		return fmt.Sprintf("Omitted %d %s.", omitted.count, unit), nil
	case OmittedKindUnknown:
		return fmt.Sprintf("More %s were omitted.", unit), nil
	}
	return "", fmt.Errorf("outputretention: 丢弃元数据没有指定（请用 OmittedNone / OmittedExact / OmittedUnknown 造一个）")
}

// FormatRetentionNotice 把一个 [RetentionNotice] 拼成一行 footer：库自己那句标准化的
// 丢弃说明（[DescribeOmitted]），后面跟上工具自己的恢复指引。
//
// 源: packages/util/output-retention/src/index.ts:423-443
//
// 这个库**永远不拥有恢复的措辞**——只有工具知道该做什么动作——所以 recovery 由调用方
// 给，并且拿到整个通知去组织语言（kept、limit、omitted 都在里面）。两半哪一半空了都行，
// 中间用一个空格连起来。
func FormatRetentionNotice(notice RetentionNotice, recovery func(RetentionNotice) string) (string, error) {
	if err := notice.Validate(); err != nil {
		return "", err
	}
	clause, err := DescribeOmitted(notice.Omitted, notice.Unit)
	if err != nil {
		return "", err
	}

	parts := make([]string, 0, 2)
	for _, part := range []string{clause, recovery(notice)} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, " "), nil
}
