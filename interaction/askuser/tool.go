// 本文件的作用：ask_user_question 这件工具——给模型看的那份 schema、参数到接缝
// 形状的翻译、以及答案回来之后的反向翻译。
//
// 源: packages/interaction/tool-ask-user/src/index.ts:13-101

package askuser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"ds-harness-go/core/scope"
	"ds-harness-go/core/tools"
	"ds-harness-go/interaction/userquestions"
	"ds-harness-go/llm"
)

// ToolName 是这件工具在注册表里的名字。
//
// 源: packages/interaction/tool-ask-user/src/index.ts:21
const ToolName = "ask_user_question"

// ErrInvalidConfig 表示配置本身不成立，构造被拒。
var ErrInvalidConfig = errors.New("askuser: 配置不成立")

// description 是给模型看的那段说明。
//
// 源: packages/interaction/tool-ask-user/src/index.ts:16-17
//
// 它是给模型看的载荷，所以保持英文，和本仓库其余面向模型的文字同一条界线。
const description = "Ask the user a concise question when you need confirmation, a choice, " +
	"or missing information before proceeding. " +
	"Send one or more questions, each with a stable id that will be echoed in the answer."

// Asker 是这件工具要的那道能力接缝。
//
// 新增: DSH 直接吃 ctx.userQuestions 这个具体服务。Go 里收一个只有 Ask 的接口，
// 于是测试能换成假的，而装配方交进来的通常就是 *[userquestions.Service]。
type Asker interface {
	// Ask 把这批问题交给活着的界面，等人回答。
	Ask(ctx context.Context, request userquestions.Request) (userquestions.Answer, error)
}

// Config 是这件工具的装配配置。
type Config struct {
	// Questions 是那道「问人」的能力接缝。
	//
	// 新增: DSH 靠 inject = ['tools', 'userQuestions'] 让 cordis 在运行期把它注进来，
	// 缺了就整个插件装不上。Go 里它是一个显式依赖：nil 就造不出这件工具。
	Questions Asker
}

// Tool 是验好的配置。
type Tool struct {
	questions Asker
}

// New 验一份配置，造出这件工具。
func New(config Config) (*Tool, error) {
	if config.Questions == nil {
		return nil, fmt.Errorf("%w: 需要一道问人的能力接缝", ErrInvalidConfig)
	}
	return &Tool{questions: config.Questions}, nil
}

// Install 把 ask_user_question 注册进一个工具注册表，返回注销它的函数。
//
// 源: packages/interaction/tool-ask-user/src/index.ts:19-101
func (t *Tool) Install(ctx context.Context, runtime *tools.Runtime, owner *scope.Scope) (func(context.Context) error, error) {
	if runtime == nil {
		return nil, errors.New("askuser: 需要一个工具注册表")
	}
	return runtime.Register(ctx, owner, t.definition())
}

// askArgs 是模型写出来的参数。
//
// 字段名是 snake_case，因为那是给模型看的那份 schema 里的名字；接缝那边用的是
// [userquestions.Item] 的驼峰形状，两者靠 [toRequest] 翻译。
//
// 源: packages/interaction/tool-ask-user/src/index.ts:24-56
type askArgs struct {
	Questions []struct {
		ID       string `json:"id"`
		Question string `json:"question"`
		Header   string `json:"header"`
		Options  []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
		MultiSelect bool `json:"multi_select"`
	} `json:"questions"`
}

// answerItem 是交回给模型的一条回答。
//
// 源: packages/interaction/tool-ask-user/src/index.ts:66-74
type answerItem struct {
	ID       string   `json:"id"`
	Selected []string `json:"selected"`
	Custom   string   `json:"custom,omitempty"`
}

// askValue 是这件工具那份权威的返回值。
//
// 源: packages/interaction/tool-ask-user/src/index.ts:62-76
type askValue struct {
	Answers []answerItem `json:"answers"`
}

// 这两个指针给 [tools.Node.AdditionalProperties] 用：它只有**显式**写成 false
// 才拒绝未声明的属性，所以两个方向都得有个能取地址的值。
var (
	trueValue  = true
	falseValue = false
)

// questionNode 是一个问题的 schema。
//
// 源: packages/interaction/tool-ask-user/src/index.ts:28-55
//
// additionalProperties 显式写成 true，和 DSH 一样：一个多写了字段的问题宁可放进来
// 由接缝去看，也不要在 schema 这道门上把整批问题拒掉——问人这件事本来就是模型
// 卡住了才做的，这时再拒一次它就彻底走不下去了。
func questionNode() tools.Node {
	option := tools.Node{
		Type:                 tools.TypeObject,
		AdditionalProperties: &trueValue,
		Properties: []tools.Property{
			{Name: "label", Schema: tools.Node{
				Type:        tools.TypeString,
				Description: "Short user-facing option label.",
			}},
			{Name: "description", Schema: tools.Node{
				Type:        tools.TypeString,
				Description: "One sentence explaining the tradeoff or impact.",
			}},
		},
		Required: []string{"label"},
	}
	return tools.Node{
		Type:                 tools.TypeObject,
		AdditionalProperties: &trueValue,
		Properties: []tools.Property{
			{Name: "id", Schema: tools.Node{
				Type:        tools.TypeString,
				Description: "Stable id for this question; echoed in the answer.",
			}},
			{Name: "question", Schema: tools.Node{
				Type:        tools.TypeString,
				Description: "The specific question to ask the user.",
			}},
			{Name: "header", Schema: tools.Node{
				Type:        tools.TypeString,
				Description: `Optional short heading for the question, such as "Confirm" or "Choose Mode".`,
			}},
			{Name: "options", Schema: tools.Node{
				Type: tools.TypeArray,
				Description: "Optional choices to show the user. If you recommend one, " +
					`put it first and append "(Recommended)" to that label.`,
				Items: &option,
			}},
			{Name: "multi_select", Schema: tools.Node{
				Type:        tools.TypeBoolean,
				Description: "Whether the user may select more than one option. Defaults to false.",
			}},
		},
		Required: []string{"id", "question"},
	}
}

// 那份 schema 里**没有**的字段和它们缺席的理由。
//
// 源: packages/interaction/tool-ask-user/tests/tool-ask-user.spec.ts:73-75
//
// 选项只有 label 和 description 两个字段：没有 value（标签自己就是答案里回传的
// 那个标识，再给一个下标只会让一份离开界面的答案不再自解释），没有 recommended
// （推荐写进标签文本，界面不必认一个额外的布尔），也没有 preview。这三条是 DSH
// 的规格测试逐条钉住的，改 schema 之前先看那三行。

// answerNode 是一条回答的 schema。
//
// 源: packages/interaction/tool-ask-user/src/index.ts:66-74
func answerNode() tools.Node {
	return tools.Node{
		Type:                 tools.TypeObject,
		AdditionalProperties: &falseValue,
		Properties: []tools.Property{
			{Name: "id", Schema: tools.Node{Type: tools.TypeString}},
			{Name: "selected", Schema: tools.Node{
				Type:  tools.TypeArray,
				Items: &tools.Node{Type: tools.TypeString},
			}},
			{Name: "custom", Schema: tools.Node{Type: tools.TypeString}},
		},
		Required: []string{"id", "selected"},
	}
}

// definition 造这件工具的定义。
//
// 源: packages/interaction/tool-ask-user/src/index.ts:20-100
func (t *Tool) definition() *tools.Definition {
	question := questionNode()
	answer := answerNode()
	return &tools.Definition{
		Name:        ToolName,
		Description: description,
		Parameters: tools.Node{
			Type: tools.TypeObject,
			Properties: []tools.Property{{
				Name: "questions",
				Schema: tools.Node{
					Type:        tools.TypeArray,
					Description: "Questions to ask the user before continuing.",
					Items:       &question,
				},
			}},
			Required: []string{"questions"},
		},
		Output: tools.OutputDefinition{
			Schema: tools.Node{
				Type:                 tools.TypeObject,
				AdditionalProperties: &falseValue,
				Properties: []tools.Property{{
					Name:   "answers",
					Schema: tools.Node{Type: tools.TypeArray, Items: &answer},
				}},
				Required: []string{"answers"},
			},
			Render: render,
		},
		Execute:     t.execute,
		PresentCall: presentCall,
	}
}

// render 把那份值交给模型。
//
// 源: packages/interaction/tool-ask-user/src/index.ts:78
//
// 就是那份值的 JSON 原文：一份答案的结构本身**就是**要给模型的信息（哪个问题、
// 选了哪几个标签、有没有自由文本），换成散文只会把这几件事糊掉。
func render(_ json.RawMessage, value json.RawMessage) (llm.Content, error) {
	var decoded askValue
	if err := json.Unmarshal(value, &decoded); err != nil {
		return nil, err
	}
	// 先读回来再写出去，而不是把那段原始 JSON 直接抄给模型：这一步顺带把空白和
	// 键序规范掉，于是同一份答案不论从哪条路回来，进模型上下文的字节都一样。
	// [askValue] 里全是字符串和切片，编码不会失败。
	text, _ := json.Marshal(decoded)
	return llm.Content{llm.TextBlock{Text: string(text)}}, nil
}

// presentCall 是这次调用进行中在界面上的样子。
//
// 新增: DSH 没给这件工具写呈现，界面拿到的是通用卡片。Go 里 [tools.Definition]
// 要一个纯函数，所以这里写一张只带原始问题列表的卡片——它必须是纯函数（实时流式
// 和会话重放都会调它），所以只看 args，拿不出那批问题就只给标题。
func presentCall(args json.RawMessage) tools.CallView {
	view := tools.GenericCallView{Title: "Ask the user", Kind: tools.CallOther}
	var raw struct {
		Questions json.RawMessage `json:"questions"`
	}
	if err := json.Unmarshal(args, &raw); err == nil {
		view.RawInput = raw.Questions
	}
	return view
}

// toRequest 把模型那份参数翻成接缝的形状。
//
// 源: packages/interaction/tool-ask-user/src/index.ts:82-90
//
// 一批空问题在这里**不**拦——它由 [userquestions.Service.Ask] 拒（EMPTY_QUESTIONS）,
// 好让拒绝的话术只有一份，界面自己发起的请求和模型发起的请求撞同一堵墙。
func toRequest(decoded askArgs, agent *scope.Key) userquestions.Request {
	questions := make([]userquestions.Item, 0, len(decoded.Questions))
	for _, question := range decoded.Questions {
		item := userquestions.Item{
			ID:          question.ID,
			Question:    question.Question,
			Header:      question.Header,
			MultiSelect: question.MultiSelect,
		}
		if len(question.Options) > 0 {
			item.Options = make([]userquestions.Option, 0, len(question.Options))
			for _, option := range question.Options {
				item.Options = append(item.Options, userquestions.Option{
					Label:       option.Label,
					Description: option.Description,
				})
			}
		}
		questions = append(questions, item)
	}
	return userquestions.Request{Questions: questions, Agent: agent}
}

// toValue 把界面回来的答案翻回模型看得懂的形状。
//
// 源: packages/interaction/tool-ask-user/src/index.ts:92-98
//
// selected 逐条复制而不是把界面那张切片直接交出去：那份答案是提供方的对象，
// 而这里交出去的东西会被序列化进日志，两边不该共享同一块底层数组。
// 它同时保证一份空的选择编码成 []，不是 null——输出 schema 说 selected 必填。
func toValue(answer userquestions.Answer) askValue {
	answers := make([]answerItem, 0, len(answer.Answers))
	for _, item := range answer.Answers {
		selected := make([]string, len(item.Selected))
		copy(selected, item.Selected)
		answers = append(answers, answerItem{ID: item.ID, Selected: selected, Custom: item.Custom})
	}
	return askValue{Answers: answers}
}

// execute 跑一次已经放行的调用。
//
// 源: packages/interaction/tool-ask-user/src/index.ts:80-99
//
// 接缝报的错原样返回：[userquestions.Error] 带着 ErrorName/ErrorCode，
// [ds-harness-go/core/tools] 那道结果收敛会把它抄进 Failure.Info，下游按代号分流，
// 不必解析错误文本。
func (t *Tool) execute(ctx context.Context, args json.RawMessage, exec *tools.RunContext) (json.RawMessage, error) {
	var decoded askArgs
	if err := json.Unmarshal(args, &decoded); err != nil {
		return nil, err
	}
	answer, err := t.questions.Ask(ctx, toRequest(decoded, exec.Agent))
	if err != nil {
		return nil, err
	}
	return json.Marshal(toValue(answer))
}
