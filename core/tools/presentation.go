// 本文件的作用：一套**只有词汇、没有逻辑**的呈现意图——一次工具调用在界面上
// 长成什么卡片，一个结果又长成什么卡片。
//
// 源: packages/core/tools/src/presentation.ts
//
// 这里一行判断都没有。工具用 [Definition.PresentCall] 和 [Definition.PresentResult]
// 交出这些值，界面照着渲染；本包只负责把它们原样运过去。放在这个包而不是协议那一侧，
// 是因为写它们的人是工具作者，而工具定义在这里。
//
// 每种卡片带一个判别标签（card，搜索和网页那两族还多带一层 shape / kind）。
// 标签在 Go 这一侧是接口上的方法而不是结构体字段——字段是可写的，一个能被改掉的
// 判别标签等于没有。排 JSON 时再把它补回去，形状和 DSH 一字不差。

package tools

import (
	"encoding/json"

	"github.com/snight1983/ds-harness-go/llm"
)

// CallKind 是一次调用大致在做哪一类事，给界面挑图标用。
//
// 源: packages/core/tools/src/presentation.ts:15
type CallKind string

const (
	CallRead    CallKind = "read"
	CallEdit    CallKind = "edit"
	CallDelete  CallKind = "delete"
	CallMove    CallKind = "move"
	CallSearch  CallKind = "search"
	CallExecute CallKind = "execute"
	CallFetch   CallKind = "fetch"
	CallOther   CallKind = "other"
)

// FileLocation 是一个文件位置，界面可以拿它做跳转。
//
// 源: packages/core/tools/src/presentation.ts:23-26
type FileLocation struct {
	Path string `json:"path"`
	// Line 是行号，没有就是 nil。
	//
	// 新增: DSH 是 `line?: number`。这里用指针：第 0 行不是一个合法行号，
	// 但把「没给行号」和「给了 0」拧成同一件事仍然会让界面跳到一个它不该跳的地方。
	Line *int `json:"line,omitempty"`
}

// FileDiff 是一处文件改动。
//
// 源: packages/core/tools/src/presentation.ts:34-40
type FileDiff struct {
	Path string `json:"path"`
	// OldText 是改之前的全文；nil 表示这个文件是**新建**的。
	//
	// 这里的 nil 和空串是两件事：空串是「本来就是个空文件」。
	OldText *string `json:"oldText"`
	// NewText 是改之后的全文。
	NewText string `json:"newText"`
}

// CallView 是一次**进行中**的调用在界面上的样子。
//
// 源: packages/core/tools/src/presentation.ts:46
//
// 封闭联合：变体只能在本包里加，理由和 llm.ContentBlock 逐字相同。
type CallView interface {
	// Card 是这张卡片的判别标签。
	Card() string
	sealedCallView()
}

// GenericCallView 是没有专门卡片时的通用样子。
//
// 源: packages/core/tools/src/presentation.ts:53-75
type GenericCallView struct {
	Title string `json:"title"`
	// Kind 是这次调用大致在做什么，空串表示没说。
	Kind CallKind `json:"kind,omitempty"`
	// RawInput 是原样展示的入参。
	RawInput json.RawMessage `json:"rawInput,omitempty"`
	// Content 是要一并展示的内容块。
	Content llm.Content `json:"content,omitempty"`
	// Locations 是这次调用涉及的文件位置。
	Locations []FileLocation `json:"locations,omitempty"`
}

// Card 交出判别标签。
func (GenericCallView) Card() string    { return "generic" }
func (GenericCallView) sealedCallView() {}

// MarshalJSON 排出带 card 标签的那一份。
func (v GenericCallView) MarshalJSON() ([]byte, error) {
	type payload GenericCallView
	return taggedJSON(payload(v), "card", v.Card())
}

// TerminalCallView 是一次终端里的执行。
//
// 源: packages/core/tools/src/presentation.ts:84-100
type TerminalCallView struct {
	Title string `json:"title"`
	// Description 是这条命令在干什么的一句话。
	Description string `json:"description,omitempty"`
	// Cwd 是执行时的工作目录。
	Cwd string `json:"cwd,omitempty"`
}

// Card 交出判别标签。
func (TerminalCallView) Card() string    { return "terminal" }
func (TerminalCallView) sealedCallView() {}

// MarshalJSON 排出带 card 标签的那一份。
func (v TerminalCallView) MarshalJSON() ([]byte, error) {
	type payload TerminalCallView
	return taggedJSON(payload(v), "card", v.Card())
}

// DiffCallView 是一次改文件。
//
// 源: packages/core/tools/src/presentation.ts:110-118
type DiffCallView struct {
	Title     string         `json:"title"`
	Diffs     []FileDiff     `json:"diffs"`
	Locations []FileLocation `json:"locations,omitempty"`
}

// Card 交出判别标签。
func (DiffCallView) Card() string    { return "diff" }
func (DiffCallView) sealedCallView() {}

// MarshalJSON 排出带 card 标签的那一份。
func (v DiffCallView) MarshalJSON() ([]byte, error) {
	type payload DiffCallView
	return taggedJSON(payload(v), "card", v.Card())
}

// ReadFileLine 是读文件结果里的一行。
//
// 源: packages/core/tools/src/presentation.ts:127-130
type ReadFileLine struct {
	Number int    `json:"number"`
	Text   string `json:"text"`
}

// ResultView 是一次**已完成**的调用在界面上的样子。
//
// 源: packages/core/tools/src/presentation.ts:255-267（SearchResultView）
//
// 新增: DSH 那边这一族分了三层：ToolResultView 是六选一的顶层联合，其中
// SearchResultView（presentation.ts:267）和 WebResultView（presentation.ts:347）
// 各自又是两选一的子联合。Go 这边只有这一个接口，六支全部直接实现它——
// 中间那两层在 TS 里的全部作用是类型收窄，没有任何运行期内容，Go 也没有收窄。
//
// 排出去的 JSON 和 DSH 一字不差：子联合的分支靠**第二层标签**分辨，
// card 之下再带一个 shape 或 kind，两层都由 MarshalJSON 补上：
//
//	card=search  +  shape=matches / shape=paths
//	card=web     +  kind=search   / kind=fetch
//
// 代价是签名这一层松了一格：上游一个只收 SearchResultView 的函数，Go 这边
// 只能收 ResultView，是搜索结果这件事得由函数自己断言。本包没有这样的函数
// （这里一行判断都没有），所以代价目前只落在下游。
type ResultView interface {
	// Card 是这张卡片的判别标签。
	Card() string
	sealedResultView()
}

// GenericResultView 是没有专门卡片时的通用结果。
//
// 源: packages/core/tools/src/presentation.ts:146-155
type GenericResultView struct {
	Title   string      `json:"title,omitempty"`
	Content llm.Content `json:"content,omitempty"`
}

// Card 交出判别标签。
func (GenericResultView) Card() string      { return "generic" }
func (GenericResultView) sealedResultView() {}

// MarshalJSON 排出带 card 标签的那一份。
func (v GenericResultView) MarshalJSON() ([]byte, error) {
	type payload GenericResultView
	return taggedJSON(payload(v), "card", v.Card())
}

// TerminalResultView 是一次终端执行的结果。
//
// 源: packages/core/tools/src/presentation.ts:163-176
type TerminalResultView struct {
	Title  string `json:"title,omitempty"`
	Output string `json:"output,omitempty"`
	// ExitCode 是退出码，没有就是 nil——零是「正常退出」，不是「没有退出码」。
	ExitCode *int `json:"exitCode,omitempty"`
	// Signal 是被哪个信号打断的。
	Signal string `json:"signal,omitempty"`
}

// Card 交出判别标签。
func (TerminalResultView) Card() string      { return "terminal" }
func (TerminalResultView) sealedResultView() {}

// MarshalJSON 排出带 card 标签的那一份。
func (v TerminalResultView) MarshalJSON() ([]byte, error) {
	type payload TerminalResultView
	return taggedJSON(payload(v), "card", v.Card())
}

// DiffResultView 是一次改文件的结果。
//
// 源: packages/core/tools/src/presentation.ts:184-190
type DiffResultView struct {
	Title string     `json:"title,omitempty"`
	Diffs []FileDiff `json:"diffs"`
}

// Card 交出判别标签。
func (DiffResultView) Card() string      { return "diff" }
func (DiffResultView) sealedResultView() {}

// MarshalJSON 排出带 card 标签的那一份。
func (v DiffResultView) MarshalJSON() ([]byte, error) {
	type payload DiffResultView
	return taggedJSON(payload(v), "card", v.Card())
}

// SearchLineMatch 是一处命中的行。
//
// 源: packages/core/tools/src/presentation.ts:192-198（SearchLineMatch）
type SearchLineMatch struct {
	LineNumber int    `json:"lineNumber"`
	Line       string `json:"line"`
}

// SearchFileMatches 是一个文件里的全部命中。
//
// 源: packages/core/tools/src/presentation.ts:200-206（SearchFileMatches）
type SearchFileMatches struct {
	Path    string            `json:"path"`
	Matches []SearchLineMatch `json:"matches"`
}

// SearchMatchesResultView 是「按行命中」形状的搜索结果。
//
// 源: packages/core/tools/src/presentation.ts:216-231
type SearchMatchesResultView struct {
	Title string              `json:"title,omitempty"`
	Files []SearchFileMatches `json:"files"`
	// Truncated 表示这份结果被截断过。
	Truncated bool `json:"truncated"`
	// Total 是截断之前一共有多少条。
	Total int `json:"total"`
}

// Card 交出判别标签。
func (SearchMatchesResultView) Card() string      { return "search" }
func (SearchMatchesResultView) sealedResultView() {}

// MarshalJSON 排出带 card 和 shape 两层标签的那一份。
func (v SearchMatchesResultView) MarshalJSON() ([]byte, error) {
	type payload SearchMatchesResultView
	return taggedJSON(payload(v), "card", v.Card(), "shape", "matches")
}

// SearchPathsResultView 是「只有路径」形状的搜索结果。
//
// 源: packages/core/tools/src/presentation.ts:238-253
type SearchPathsResultView struct {
	Title     string   `json:"title,omitempty"`
	Paths     []string `json:"paths"`
	Truncated bool     `json:"truncated"`
	Total     int      `json:"total"`
}

// Card 交出判别标签。
func (SearchPathsResultView) Card() string      { return "search" }
func (SearchPathsResultView) sealedResultView() {}

// MarshalJSON 排出带 card 和 shape 两层标签的那一份。
func (v SearchPathsResultView) MarshalJSON() ([]byte, error) {
	type payload SearchPathsResultView
	return taggedJSON(payload(v), "card", v.Card(), "shape", "paths")
}

// ReadResultView 是一次读文件的结果。
//
// 源: packages/core/tools/src/presentation.ts:281-308
type ReadResultView struct {
	Title string `json:"title,omitempty"`
	Path  string `json:"path"`
	// Offset 是这一段从第几行开始。
	Offset int            `json:"offset"`
	Lines  []ReadFileLine `json:"lines"`
	// TotalLines 是整个文件一共多少行。
	TotalLines int `json:"totalLines"`
	// Lang 是高亮用的语言标识。
	Lang    string      `json:"lang,omitempty"`
	Content llm.Content `json:"content,omitempty"`
}

// Card 交出判别标签。
func (ReadResultView) Card() string      { return "read" }
func (ReadResultView) sealedResultView() {}

// MarshalJSON 排出带 card 标签的那一份。
func (v ReadResultView) MarshalJSON() ([]byte, error) {
	type payload ReadResultView
	return taggedJSON(payload(v), "card", v.Card())
}

// WebSource 是一条网页来源。
//
// 源: packages/core/tools/src/presentation.ts:319-328
type WebSource struct {
	URL     string `json:"url"`
	Title   string `json:"title,omitempty"`
	Snippet string `json:"snippet,omitempty"`
	// PublishedAt 是发布时间，原样保管，本包不解释它的格式。
	PublishedAt string `json:"publishedAt,omitempty"`
}

// WebSearchResultView 是一次联网搜索的结果。
//
// 源: packages/core/tools/src/presentation.ts:355-366
type WebSearchResultView struct {
	Title     string      `json:"title,omitempty"`
	Sources   []WebSource `json:"sources"`
	Answer    string      `json:"answer,omitempty"`
	Truncated bool        `json:"truncated"`
}

// Card 交出判别标签。
func (WebSearchResultView) Card() string      { return "web" }
func (WebSearchResultView) sealedResultView() {}

// MarshalJSON 排出带 card 和 kind 两层标签的那一份。
func (v WebSearchResultView) MarshalJSON() ([]byte, error) {
	type payload WebSearchResultView
	return taggedJSON(payload(v), "card", v.Card(), "kind", "search")
}

// WebFetchResultView 是一次抓取网页的结果。
//
// 源: packages/core/tools/src/presentation.ts:374-389
type WebFetchResultView struct {
	Title      string `json:"title,omitempty"`
	URL        string `json:"url"`
	StatusCode int    `json:"statusCode"`
	Truncated  bool   `json:"truncated"`
}

// Card 交出判别标签。
func (WebFetchResultView) Card() string      { return "web" }
func (WebFetchResultView) sealedResultView() {}

// MarshalJSON 排出带 card 和 kind 两层标签的那一份。
func (v WebFetchResultView) MarshalJSON() ([]byte, error) {
	type payload WebFetchResultView
	return taggedJSON(payload(v), "card", v.Card(), "kind", "fetch")
}

// taggedJSON 把判别标签补进结构体自己那份 JSON 对象里。
//
// tags 是「键, 值」成对给的。补出来的对象键序是排序过的（Go 的 map 排 JSON 就是这样），
// 这一点无所谓：读它的是界面，按键取值。
func taggedJSON(payload any, tags ...string) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(body, &fields); err != nil {
		// 走不到：payload 永远是本包某个卡片类型的同构副本，排出来必是一个 JSON 对象。
		// 留着这条分支是因为忽略这里的 error 会把「排出来不是对象」变成静默丢标签，
		// 那比多一条测不着的语句糟得多。
		return nil, err
	}
	for index := 0; index+1 < len(tags); index += 2 {
		fields[tags[index]] = quoteJSONString(tags[index+1])
	}
	return json.Marshal(fields)
}
