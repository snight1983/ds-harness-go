// 本文件的作用：问和答那几个上线的形状——它们要能原样过 JSON，所以不带任何
// 服务、上下文、活对象。
//
// 源: packages/interaction/user-questions/src/types.ts

package userquestions

// Option 是摆给用户的一个可选项。
//
// 源: packages/interaction/user-questions/src/types.ts:9-14
type Option struct {
	// Label 是给用户看的文字，同时也是答案里回传的那个标识。
	//
	// 它身兼两职是有代价的（改文案等于改协议），但这正是 DSH 的形状：答案里回来的
	// 就是标签本身。好处是一份答案离开界面之后仍然自解释——一个下标回来是 2，
	// 谁都说不出用户当时点的是什么。
	Label string `json:"label"`
	// Description 是有能力的界面额外画出来的一句话。
	Description string `json:"description,omitempty"`
}

// Intent 是提问方声明的呈现意图：这个问题**就是**某一类裁决，认得这个标记的界面
// 可以把它画成那类裁决，而不是一串通用选项。
//
// 源: packages/interaction/user-questions/src/types.ts:16-32
//
// 它只改呈现，永远不改协议：不认识这个标记的界面走通用问答那条路，两边回来的
// 答案编码一模一样。
//
// 新增: DSH 是一个带 kind 判别字段的联合类型。Go 里是这个封闭接口——[sealedIntent]
// 未导出，所以变体只能在本包里加，switch 上认识的、不认识的落到 default，
// 和 DSH 那边的话术一致。
type Intent interface {
	// Kind 是这个意图的标记，进错误文本，也进上线的载荷。
	Kind() string
	// ApproveLabel 是这个意图里那个表示「同意」的选项标签。
	//
	// 具名而不是按位置认，这样没有任何界面会从选项顺序里推断裁决。
	ApproveLabel() string
	sealedIntent()
}

// PlanReviewIntent 表示这个问题是一次计划评审：[Item.Detail] 就是那份计划正文，
// 而这次裁决同意或者不同意它。
//
// 源: packages/interaction/user-questions/src/types.ts:23-32
type PlanReviewIntent struct {
	// Approve 是同意这份计划的那个选项标签；别的选项一律是不同意。
	Approve string `json:"approve"`
}

// Kind 交出这个意图的标记。
func (PlanReviewIntent) Kind() string { return "plan-review" }

// ApproveLabel 交出同意这份计划的那个选项标签。
func (intent PlanReviewIntent) ApproveLabel() string { return intent.Approve }

func (PlanReviewIntent) sealedIntent() {}

// Item 是一次请求里的一个问题。
//
// 源: packages/interaction/user-questions/src/types.ts:34-50
type Item struct {
	// ID 是提问方给的稳定标识，答案里原样回来。
	ID string `json:"id"`
	// Question 是要显示的那句话。
	Question string `json:"question"`
	// Detail 是随问题一起画出来的补充正文，它**不**进选项标签。
	Detail string `json:"detail,omitempty"`
	// Header 是一句短标题或者分组名。
	Header string `json:"header,omitempty"`
	// Options 是界面可以画成菜单的那些选项。空表示只要自由文本。
	Options []Option `json:"options,omitempty"`
	// MultiSelect 表示可以选不止一个。零值是单选。
	MultiSelect bool `json:"multiSelect,omitempty"`
	// Intent 是给有能力的界面的呈现意图；nil 表示要一份通用选项列表。
	Intent Intent `json:"intent,omitempty"`
}

// AnswerItem 是对一个问题的回答。
//
// 源: packages/interaction/user-questions/src/types.ts:52-60
type AnswerItem struct {
	// ID 是被回答的那个问题的标识。
	ID string `json:"id"`
	// Selected 是选中的那些选项标签。多选题里它可以和自由文本同时出现。
	Selected []string `json:"selected"`
	// Custom 是「其它」那一栏里的自由文本。空表示没写。
	Custom string `json:"custom,omitempty"`
}

// Answer 是人给出的那份答案。
//
// 源: packages/interaction/user-questions/src/types.ts:62-66
type Answer struct {
	// Answers 是按问题标识归位的那些回答。
	Answers []AnswerItem `json:"answers"`
}
