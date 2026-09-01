// 本文件的作用：那件 `skill` 工具——它摆在工具表上的样子、它交出来的那份值，
// 以及把这三条通路一次装齐、一次摘干净的那两个函数。
//
// 源: packages/skill/tool-skill/src/index.ts:81-161,246-251
//
// 本文件里所有面向模型的文字（工具名、说明、参数描述、卡片标题）都保持英文，
// 和本仓库其余面向模型的载荷同一条界线。中文只在注释里。

package skilltool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/skill"
)

// toolArgs 是 skill 工具收的那一个参数。
type toolArgs struct {
	Name string `json:"name"`
}

// resourceBaseWire 是那份资源基址排到工具输出里的样子。
//
// 源: packages/skill/tool-skill/src/index.ts:94-121,151-153
//
// 新增: [skill.ResourceBase] 在 Go 里是个封闭接口，排不出、也解不回一份判别联合。
// 这个结构体就是介质上那三支的并集，三个载荷字段各带 omitempty，于是排出来的
// 字节和 DSH 的 `{ ...skill.resourceBase }` 逐字一致——而输出 schema 上那三支
// `additionalProperties: false` 会把「多带了别支的字段」挡下来。
type resourceBaseWire struct {
	Kind        skill.ResourceBaseKind `json:"kind"`
	Path        string                 `json:"path,omitempty"`
	URL         string                 `json:"url,omitempty"`
	Description string                 `json:"description,omitempty"`
}

// toolOutput 是 skill 工具那份权威的值。
//
// 源: packages/skill/tool-skill/src/index.ts:148-155
type toolOutput struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	// ResourceBase 为 nil 表示提供方没给，这个键整个不出现——和 DSH 那句
	// `...skill.resourceBase !== undefined ? {...} : {}` 是同一件事。
	ResourceBase *resourceBaseWire `json:"resourceBase,omitempty"`
	Content      string            `json:"content"`
}

// encodeResourceBase 把一份资源基址折成介质上的样子；没有基址交回 nil。
func encodeResourceBase(base skill.ResourceBase) *resourceBaseWire {
	switch typed := base.(type) {
	case skill.DirectoryBase:
		return &resourceBaseWire{Kind: skill.ResourceBaseDirectory, Path: typed.Path}
	case skill.URLBase:
		return &resourceBaseWire{Kind: skill.ResourceBaseURL, URL: typed.URL}
	case skill.OpaqueBase:
		return &resourceBaseWire{Kind: skill.ResourceBaseOpaque, Description: typed.Description}
	default:
		// [skill.ResourceBase] 是封闭的，nil 之外走不到这里。
		return nil
	}
}

// decodeResourceBase 把介质上那份基址读回一个 [skill.ResourceBase]。
//
// 认不出的判别标签交回 nil：渲染是纯投影、不许失败，而一份没有基址的技能
// 只是少掉那两行资源提示，正文照样完整。
func decodeResourceBase(wire *resourceBaseWire) skill.ResourceBase {
	if wire == nil {
		return nil
	}
	switch wire.Kind {
	case skill.ResourceBaseDirectory:
		return skill.DirectoryBase{Path: wire.Path}
	case skill.ResourceBaseURL:
		return skill.URLBase{URL: wire.URL}
	case skill.ResourceBaseOpaque:
		return skill.OpaqueBase{Description: wire.Description}
	default:
		return nil
	}
}

// resourceBaseSchema 是输出里 resourceBase 那三支。
//
// 源: packages/skill/tool-skill/src/index.ts:94-121
//
// 每一支都把 kind 钉成一个常数、并且不许带别的属性：模型据此知道拿到 `directory`
// 就一定有 `path`，不必去猜另外两个字段在不在。
func resourceBaseSchema() tools.Node {
	branch := func(kind skill.ResourceBaseKind, payload string) tools.Node {
		return tools.Node{
			Type: tools.TypeObject,
			Properties: []tools.Property{
				{Name: "kind", Schema: tools.Node{Type: tools.TypeString, Const: rawText(string(kind))}},
				{Name: payload, Schema: tools.Node{Type: tools.TypeString}},
			},
			Required:             []string{"kind", payload},
			AdditionalProperties: falseValue(),
		}
	}
	return tools.Node{OneOf: []tools.Node{
		branch(skill.ResourceBaseDirectory, "path"),
		branch(skill.ResourceBaseURL, "url"),
		branch(skill.ResourceBaseOpaque, "description"),
	}}
}

// falseValue 交出一个指向 false 的指针，给 [tools.Node.AdditionalProperties] 用。
func falseValue() *bool {
	value := false
	return &value
}

// rawText 把一段文字排成 schema 和卡片要的那种字节。
//
// 新增: DSH 那边 const / rawInput 的类型是 unknown，一个裸字符串直接就能放。
// Go 侧它们是 json.RawMessage，所以要先排一遍。排不出去时留空——这两处都在
// 「不许失败」的路径上（一份 schema、一张卡片），空比整个塌掉强。
func rawText(text string) json.RawMessage {
	encoded, err := json.Marshal(text)
	if err != nil {
		return nil
	}
	return encoded
}

// newDefinition 造那件 skill 工具。
//
// 源: packages/skill/tool-skill/src/index.ts:81-160
//
// 这个方法在 [New] 里**只调一次**，造出来的那个指针从头用到尾，理由见
// [Controller.definition] 的注释。
func (c *Controller) newDefinition() *tools.Definition {
	return &tools.Definition{
		Name:        ToolName,
		Description: "Load the full instructions for an available skill. Call this with the exact skill name from the session skill catalog before acting on a task that names or clearly matches that skill.",
		Parameters: tools.Node{
			Type: tools.TypeObject,
			Properties: []tools.Property{
				{Name: "name", Schema: tools.Node{
					Type:        tools.TypeString,
					Description: "The exact skill name from the available skills list.",
				}},
			},
			Required: []string{"name"},
		},
		Output: tools.OutputDefinition{
			Schema: tools.Node{
				Type: tools.TypeObject,
				Properties: []tools.Property{
					{Name: "name", Schema: tools.Node{Type: tools.TypeString}},
					{Name: "provider", Schema: tools.Node{Type: tools.TypeString}},
					{Name: "resourceBase", Schema: resourceBaseSchema()},
					{Name: "content", Schema: tools.Node{Type: tools.TypeString}},
				},
				Required:             []string{"name", "provider", "content"},
				AdditionalProperties: falseValue(),
			},
			Render: renderSkillOutput,
		},
		Execute:     c.executeSkill,
		PresentCall: presentSkillCall,
	}
}

// renderSkillOutput 把那份值投影成模型看的 `<skill_content>` 块。
//
// 源: packages/skill/tool-skill/src/index.ts:125
//
// 走的是 [skill.RenderContent]，和「用户明确调起」那条注入路**逐字共用**同一份
// 输出：模型在两条路上必须看见同一个形状，否则它会以为那是两种不同的东西。
func renderSkillOutput(_ json.RawMessage, value json.RawMessage) (llm.Content, error) {
	var decoded toolOutput
	if err := json.Unmarshal(value, &decoded); err != nil {
		return nil, err
	}
	definition := skill.Definition{
		Summary: skill.Summary{
			Name:         decoded.Name,
			Provider:     decoded.Provider,
			ResourceBase: decodeResourceBase(decoded.ResourceBase),
		},
		Content: decoded.Content,
	}
	return llm.Content{llm.TextBlock{Text: skill.RenderContent(definition)}}, nil
}

// executeSkill 把一份技能读成那份权威的值。
//
// 源: packages/skill/tool-skill/src/index.ts:127-156
//
// 「能不能读」这件事查**两遍**：一遍落在目录里那份摘要上，一遍落在真正读出来的
// 那份定义上。两遍都要，因为两次查找之间注册表可能变了——只查摘要的话，一份刚
// 被关掉模型调用的技能仍然会被整篇交出去。
func (c *Controller) executeSkill(
	ctx context.Context,
	rawArgs json.RawMessage,
	exec *tools.RunContext,
) (json.RawMessage, error) {
	var args toolArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, err
	}
	if !skill.IsName(args.Name) {
		return nil, fmt.Errorf("invalid skill name %q", args.Name)
	}
	options, err := c.executionViewOptions(exec)
	if err != nil {
		return nil, err
	}
	summaries, err := c.skills.List(ctx, options)
	if err != nil {
		return nil, err
	}
	found := false
	for _, summary := range summaries {
		if summary.Name != args.Name {
			continue
		}
		found = true
		if !skill.IsModelInvocable(summary) {
			return nil, fmt.Errorf("skill %q is not available for model invocation", args.Name)
		}
		break
	}
	if !found {
		return nil, fmt.Errorf("skill %q is unknown or no longer available", args.Name)
	}
	definition, err := c.skills.Get(ctx, args.Name, options)
	if err != nil {
		return nil, err
	}
	if definition == nil {
		return nil, fmt.Errorf("skill %q is unknown or no longer available", args.Name)
	}
	if !skill.IsModelInvocable(definition.Summary) {
		return nil, fmt.Errorf("skill %q is not available for model invocation", args.Name)
	}
	return json.Marshal(toolOutput{
		Name:         definition.Name,
		Provider:     definition.Provider,
		ResourceBase: encodeResourceBase(definition.ResourceBase),
		Content:      definition.Content,
	})
}

// executionViewOptions 是这次调用读注册表时用的那份选项。
//
// 源: packages/skill/tool-skill/src/index.ts:131-133
//
// 新增: DSH 的 `exec.agent` 就是 agent 对象本身，直接当作用域钥匙用。Go 这边
// [tools.ExecutionInput.Agent] 是一把不透明的钥匙，要拿工作区就得先查回那个
// agent，所以走 [Config.AgentOf]。没有 agent 的调用（比如一次不带作用域的直调）
// 只读全局层，和 DSH 的 `exec.agent?.session.header.cwd` 落到 undefined 一致。
func (c *Controller) executionViewOptions(exec *tools.RunContext) (skill.ViewOptions, error) {
	if exec == nil || exec.Agent == nil {
		return skill.ViewOptions{}, nil
	}
	caller, err := c.agentOf(exec.Agent)
	if err != nil {
		return skill.ViewOptions{}, err
	}
	if caller == nil {
		return skill.ViewOptions{Scope: exec.Agent}, nil
	}
	return c.viewOptions(caller), nil
}

// presentSkillCall 是这次调用在界面上的卡片。
//
// 源: packages/skill/tool-skill/src/index.ts:157-159
func presentSkillCall(rawArgs json.RawMessage) tools.CallView {
	var args toolArgs
	_ = json.Unmarshal(rawArgs, &args)
	return tools.GenericCallView{
		Kind:     tools.CallRead,
		Title:    "Load skill " + args.Name,
		RawInput: rawText(args.Name),
	}
}

// Deps 是装这三条通路要用到的协作者。
//
// 新增: DSH 那边它们从 cordis 容器里按 inject 取。Go 里没有那个容器，所以摊成
// 一个结构体，做法和 [github.com/snight1983/ds-harness-go/sessionquery/querytool.Deps] 相同。
type Deps struct {
	// Tools 是工具注册表，必填。
	Tools *tools.Runtime
	// Agents 是 agent 注册表，必填，两条 pre-step 胳膊挂在它上面。
	Agents *agent.Registry
}

// Install 把工具和两条胳膊一次装齐，返回把它们一起摘下来的函数。
//
// 源: packages/skill/tool-skill/src/index.ts:161,204,251
//
// 登记顺序是语义的一部分，不能换：
//
//  1. 先登记工具。目录那条胳膊要靠「解算出来的 skill 工具正是这一件」才发目录，
//     工具还没在表上时那次解算必然落空。
//  2. 再登记「用户明确调起」那条。
//  3. 最后登记目录那条。
//
// 先登记的在 pre-step 瀑布的**外层**，所以第 2 条拿到的是第 3 条已经把目录挂
// 上去的那份消息表，它只管往后接——于是模型读到的次序是「目录在前、要照着做的
// 技能正文在后」，最靠近它自己的回答。
//
// 中途装不上就把已经装上的按反序摘干净再报错。
func (c *Controller) Install(
	ctx context.Context,
	owner *scope.Scope,
	deps Deps,
) (func(context.Context) error, error) {
	switch {
	case deps.Tools == nil:
		return nil, fmt.Errorf("skilltool: 需要一个工具注册表")
	case deps.Agents == nil:
		return nil, fmt.Errorf("skilltool: 需要一个 agent 注册表")
	}

	var installed []func(context.Context) error
	undo := func(undoCtx context.Context) error {
		var failures []error
		for index := len(installed) - 1; index >= 0; index-- {
			if err := installed[index](undoCtx); err != nil {
				failures = append(failures, err)
			}
		}
		installed = nil
		return errors.Join(failures...)
	}

	dispose, err := deps.Tools.Register(ctx, owner, c.definition)
	if err != nil {
		return nil, fmt.Errorf("skilltool: 装 %s 工具失败：%w", ToolName, err)
	}
	installed = append(installed, dispose)
	// 记下这张表，目录那条胳膊每个步骤都要拿它做那次身份解算。
	c.lookup = deps.Tools

	arms := []struct {
		what     string
		observer agent.PreStepObserver
	}{
		{"用户明确调起", c.invocationPreStep},
		{"技能目录", c.catalogPreStep},
	}
	for _, arm := range arms {
		remove, err := deps.Agents.OnPreStep(ctx, owner, arm.observer)
		if err != nil {
			// 摘的时候不带调用方的取消：ctx 已经废了也得把装上去的收回来。
			_ = undo(context.WithoutCancel(ctx))
			c.lookup = nil
			return nil, fmt.Errorf("skilltool: 装%s那条失败：%w", arm.what, err)
		}
		installed = append(installed, remove)
	}
	return func(undoCtx context.Context) error {
		err := undo(undoCtx)
		c.lookup = nil
		return err
	}, nil
}
