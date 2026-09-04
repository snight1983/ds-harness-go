// 本文件的作用：那件派发工具的本体——它跟着提供方的在与不在装上摘掉、一次调用
// 怎么在前台/一次性后台/可续后台三条路里挑一条走，以及把这一整套装上一个作用域
// 的那一步。
//
// 源: packages/subagent/tool-subagent/src/index.ts:247-476

package subagenttool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/snight1983/ds-harness-go/feature/jobs"
	"github.com/snight1983/ds-harness-go/feature/subagent"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/harness/systemprompt"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/tools"
)

// 这三个是那份权威结果值的三种形状，由 kind 判别。
//
// 源: packages/subagent/tool-subagent/src/index.ts:336-365
const (
	kindBackground  = "background"
	kindContinuable = "continuable"
	kindForeground  = "foreground"
)

// Controller 是攥着这一次装配的措辞和默认值、并且知道怎么跟着提供方的在与不在
// 装上摘掉那件工具的那个对象。
//
// 源: packages/subagent/tool-subagent/src/index.ts:276-476
type Controller struct {
	provider          string
	toolName          string
	agentOf           func(agent *scope.Key) (agent.Agent, error)
	logger            *slog.Logger
	backgroundEnabled bool
	continuable       bool
	agentOptions      agent.Options
	persona           string
	toolFilter        tools.Restriction
	maxDepth          *int

	// mutex 罩着下面那两样：提供方来去来自登记它的那条协程，而那段指引的正文
	// 在每一次装配提示词时求值，那又是另一条。
	mutex sync.Mutex
	// deps 是装的那一刻交进来的协作者，摘干净之后归零。
	deps Deps
	// disposeTool 是那件已经装上的工具的摘除函数；nil 表示此刻没装。
	//
	// 源: packages/subagent/tool-subagent/src/index.ts:289
	disposeTool func(context.Context) error
	// owner 是这次装配所在的作用域，提供方晚来时那件工具装在它上面。
	owner *scope.Scope
}

// delegationArgs 是这件工具的参数。
//
// 源: packages/subagent/tool-subagent/src/index.ts:316-335
//
// 新增: RunInBackground 是指针，因为「没写」和「写了 false」在这条路上不是一回事：
// 没写取这次装配的默认（可续走后台、一次性走前台），写了 false 就是要在前台等。
type delegationArgs struct {
	Description     string `json:"description"`
	Prompt          string `json:"prompt"`
	RunInBackground *bool  `json:"run_in_background"`
}

// 这三个是那份权威结果值的三种形状，各排各的。
//
// 源: packages/subagent/tool-subagent/src/index.ts:336-365
//
// 新增: DSH 是三支判别联合。Go 没有判别联合，这里落成三个各自封闭的结构体，
// 而不是一个带 omitempty 的大结构体：那三份 schema 都是
// `additionalProperties: false`，多排一个字段出去当场验不过；而 omitempty 会把
// 一次**合法的空** output 数组也一起省掉，那一支的 `output` 却是必填的。
type backgroundResult struct {
	Kind  string `json:"kind"`
	JobID string `json:"jobId"`
}

type continuableResult struct {
	Kind       string `json:"kind"`
	SubagentID string `json:"subagentId"`
}

type foregroundResult struct {
	Kind   string            `json:"kind"`
	RunID  string            `json:"runId"`
	Output []json.RawMessage `json:"output"`
}

// delegationResult 是渲染那一步把上面三种形状收回来的那个宽松视图：只有本支有的
// 字段会被填上，别的留零值。
type delegationResult struct {
	Kind       string            `json:"kind"`
	JobID      string            `json:"jobId"`
	SubagentID string            `json:"subagentId"`
	RunID      string            `json:"runId"`
	Output     []json.RawMessage `json:"output"`
}

// outputValueText 从那份权威的块数组里把文本摊平，一个不认识的值都不信。
//
// 源: packages/subagent/tool-subagent/src/index.ts:101-109
//
// 新增: DSH 拿类型守卫在 JsonValue 上筛。Go 里那份数组是
// [encoding/json.RawMessage]，所以逐个解成 [github.com/snight1983/ds-harness-go/llm.Content] 里的块，
// 解不动的直接跳过——那正是 DSH 那个 filter 的意思。
func outputValueText(values []json.RawMessage) string {
	blocks := make(llm.Content, 0, len(values))
	for _, value := range values {
		var single llm.Content
		if err := json.Unmarshal([]byte("["+string(value)+"]"), &single); err != nil {
			continue
		}
		blocks = append(blocks, single...)
	}
	return contentText(blocks)
}

// resolveRunInBackground 把模型那个可选的排期请求折成这一次到底走哪条路。
//
// 源: packages/subagent/tool-subagent/src/index.ts:255-274
//
// 那句话是给模型看的，所以是英文。
func (c *Controller) resolveRunInBackground(requested *bool) (bool, error) {
	if !c.backgroundEnabled {
		// 校验器放行没声明的键，所以光把参数从 schema 里拿掉不够，执行期还得再挡
		// 一道。
		if requested != nil && *requested {
			return false, errors.New(
				"run_in_background is disabled for this tool instance (enableRunInBackground: false)")
		}
		return false, nil
	}
	if requested != nil {
		return *requested, nil
	}
	// 可续的活默认自己排期，除非调用方明说下一步就要那个结果。一次性那条路保住
	// 它原来的前台默认，因为它的后台结果得靠 job_output 去收。
	return c.continuable, nil
}

// startRequest 把这次调用摊成一份派发请求。
//
// 源: packages/subagent/tool-subagent/src/index.ts:385-394
func (c *Controller) startRequest(parent agent.Agent, args delegationArgs) subagent.StartRequest {
	return subagent.StartRequest{
		Label:        args.Description,
		Prompt:       llm.Content{llm.TextBlock{Text: args.Prompt}},
		Parent:       parent,
		AgentOptions: c.agentOptions,
		Persona:      c.persona,
		ToolFilter:   c.toolFilter,
		MaxDepth:     c.maxDepth,
	}
}

// parentOf 把这次执行落在的那把钥匙换成那个活 agent。
//
// 源: packages/subagent/tool-subagent/src/index.ts:379-383
//
// 查不回来是错，理由写在 [Config.AgentOf] 上。那句话给模型看，所以是英文。
func (c *Controller) parentOf(exec *tools.RunContext) (agent.Agent, error) {
	if exec == nil || exec.Agent == nil {
		return nil, errors.New("subagent tool requires a calling agent (exec.agent was undefined)")
	}
	parent, err := c.agentOf(exec.Agent)
	if err != nil || parent == nil {
		return nil, errors.New("subagent tool requires a calling agent (exec.agent was undefined)")
	}
	return parent, nil
}

// delegate 是这件工具的体。
//
// 源: packages/subagent/tool-subagent/src/index.ts:378-439
func (c *Controller) delegate(
	ctx context.Context,
	rawArgs json.RawMessage,
	exec *tools.RunContext,
) (json.RawMessage, error) {
	var args delegationArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, err
	}
	parent, err := c.parentOf(exec)
	if err != nil {
		return nil, err
	}
	background, err := c.resolveRunInBackground(args.RunInBackground)
	if err != nil {
		return nil, err
	}
	request := c.startRequest(parent, args)
	switch {
	case background && c.continuable:
		return c.startContinuable(ctx, args.Description, request)
	case background:
		return c.startBackgroundJob(ctx, args.Description, parent, request)
	default:
		return c.runForeground(ctx, request)
	}
}

// startContinuable 走可续那条路：孩子接下初始提示词就返回，既不等结果也不收结果。
//
// 源: packages/subagent/tool-subagent/src/index.ts:398-408
func (c *Controller) startContinuable(
	ctx context.Context,
	label string,
	request subagent.StartRequest,
) (json.RawMessage, error) {
	started, err := c.subagents().StartContinuable(ctx, subagent.ContinuableStartSpec{
		Provider: c.provider,
		Label:    label,
		Request:  request,
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(continuableResult{Kind: kindContinuable, SubagentID: string(started.ChildID)})
}

// startBackgroundJob 走一次性后台那条路：起一件普通的后台作业，当场交回它的 id。
//
// 源: packages/subagent/tool-subagent/src/index.ts:409-431
//
// 新增: DSH 的 done 是一个 Promise，Go 这边 [github.com/snight1983/ds-harness-go/feature/jobs.Hooks.Done]
// 是一条 channel，所以结清那件事挪进一条协程；它必须**恰好**送一个值，作业注册表
// 那条契约就是这么定的。
//
// 新增: ctx 只管**登记**这一步（账本可能在库里，见
// [github.com/snight1983/ds-harness-go/feature/jobs.Registry]）。底下 Run 里那个
// runCtx 仍旧从 [context.Background] 派生，理由见那一句注释：一件后台作业活得比
// 起它的那次调用长。这两个 ctx 是两码事，不许把 ctx 传进去当那个取消口。
func (c *Controller) startBackgroundJob(
	ctx context.Context,
	label string,
	parent agent.Agent,
	request subagent.StartRequest,
) (json.RawMessage, error) {
	registry := c.jobsRegistry()
	if registry == nil {
		return nil, errors.New(
			"background jobs unavailable: load @deepseek-ai/dsh-jobs and @deepseek-ai/dsh-tool-jobs")
	}
	subagents := c.subagents()
	id, err := registry.Start(ctx, jobs.Start{
		Kind:  jobs.KindSubagent,
		Label: label,
		Owner: parent,
		Run: func() (jobs.Hooks, error) {
			// 这件活儿的取消口是作业自己的，不是那次工具调用的：一件后台作业活得
			// 比起它的那次调用长，拿调用方的 ctx 会让它在调用返回的那一刻就被掐掉。
			runCtx, cancel := context.WithCancelCause(context.Background())
			done := make(chan jobs.Outcome, 1)
			go func() {
				defer close(done)
				done <- settleStart(runCtx, func(startCtx context.Context) (subagent.Run, error) {
					return subagents.Start(startCtx, c.provider, request)
				})
			}()
			return jobs.Hooks{
				Cancel: func(reason string) error {
					if reason == "" {
						reason = "background subagent task killed"
					}
					cancel(errors.New(reason))
					return nil
				},
				Done: done,
				// 没有 ReadOutput：中间过程归孩子那段会话所有。
			}, nil
		},
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(backgroundResult{Kind: kindBackground, JobID: string(id)})
}

// runForeground 走前台那条路：等这次运行结清，并且**一定**把它处置掉。
//
// 源: packages/subagent/tool-subagent/src/index.ts:434-438
func (c *Controller) runForeground(
	ctx context.Context,
	request subagent.StartRequest,
) (json.RawMessage, error) {
	run, err := c.subagents().Start(ctx, c.provider, request)
	if err != nil {
		return nil, err
	}
	output, err := settleForegroundRun(ctx, run)
	if err != nil {
		return nil, err
	}
	blocks, err := marshalBlocks(output)
	if err != nil {
		return nil, err
	}
	return json.Marshal(foregroundResult{
		Kind:   kindForeground,
		RunID:  string(run.ID()),
		Output: blocks,
	})
}

// marshalBlocks 把孩子那份内容逐块排成权威值里的那个数组。
//
// 源: packages/subagent/tool-subagent/src/index.ts:186-191
//
// 新增: DSH 直接 `result.output as unknown as JsonValue[]`，靠工具注册表在那一步
// 做无损快照。Go 这边排一遍就是那次快照。逐块排而不是把整份内容排成一个数组再拆
// 开：[github.com/snight1983/ds-harness-go/llm.Content] 只挂了 UnmarshalJSON，编组是逐块进行的，所以两
// 条路的字节完全一样，而这一条不必再把那个数组解回来——那一步的错误分支根本走不到
// （排得出来的一定是个 JSON 数组），留着就是一段验不了的代码。块的形状仍旧归
// [github.com/snight1983/ds-harness-go/llm.Content] 所有，本包一个字都不复述。
func marshalBlocks(content llm.Content) ([]json.RawMessage, error) {
	blocks := make([]json.RawMessage, 0, len(content))
	for _, block := range content {
		encoded, err := json.Marshal(block)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, encoded)
	}
	return blocks, nil
}

// subagents 取那条子 agent 接缝。
func (c *Controller) subagents() Subagents {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.deps.Subagents
}

// jobsRegistry 取那台作业注册表，没装就是 nil。
func (c *Controller) jobsRegistry() Jobs {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.deps.Jobs
}

// outputSchema 是那份权威结果值的契约：三支互斥的形状。
//
// 源: packages/subagent/tool-subagent/src/index.ts:337-365
func outputSchema() tools.Node {
	closed := false
	constOf := func(kind string) json.RawMessage {
		encoded, _ := json.Marshal(kind)
		return encoded
	}
	shape := func(kind string, extra ...tools.Property) tools.Node {
		properties := append([]tools.Property{
			{Name: "kind", Schema: tools.Node{Type: tools.TypeString, Const: constOf(kind)}},
		}, extra...)
		required := make([]string, 0, len(properties))
		for _, property := range properties {
			required = append(required, property.Name)
		}
		return tools.Node{
			Type:                 tools.TypeObject,
			Properties:           properties,
			Required:             required,
			AdditionalProperties: &closed,
		}
	}
	return tools.Node{OneOf: []tools.Node{
		shape(kindBackground, tools.Property{Name: "jobId", Schema: tools.Node{Type: tools.TypeString}}),
		shape(kindContinuable, tools.Property{Name: "subagentId", Schema: tools.Node{Type: tools.TypeString}}),
		shape(kindForeground,
			tools.Property{Name: "runId", Schema: tools.Node{Type: tools.TypeString}},
			// items 是「任意 JSON 值」：孩子交回来的是内容块，块的形状归
			// [github.com/snight1983/ds-harness-go/llm.Content] 所有，本包不在这里复述一遍。
			tools.Property{Name: "output", Schema: tools.Node{Type: tools.TypeArray, Items: &tools.Node{}}},
		),
	}}
}

// newTool 造那件工具，措辞由提供方那句「孩子看不看得到父的历史」定下来。
//
// 源: packages/subagent/tool-subagent/src/index.ts:300-440
func (c *Controller) newTool(inheritsConversation bool) *tools.Definition {
	words := providerWording(inheritsConversation)
	properties := []tools.Property{
		{Name: "description", Schema: tools.Node{Type: tools.TypeString, Description: descriptionDescription}},
		{Name: "prompt", Schema: tools.Node{Type: tools.TypeString, Description: words.promptDescription}},
	}
	if c.backgroundEnabled {
		properties = append(properties, tools.Property{
			Name: "run_in_background",
			Schema: tools.Node{
				Type:        tools.TypeBoolean,
				Description: backgroundDescription(c.continuable),
			},
		})
	}
	return &tools.Definition{
		Name:        c.toolName,
		Description: toolDescription(words.description, c.backgroundEnabled, c.continuable),
		Parameters: tools.Node{
			Type:       tools.TypeObject,
			Properties: properties,
			Required:   []string{"description", "prompt"},
		},
		// 孩子从不改父那段会话；父这边唯一的那次写（起一件作业）是一次同步的、
		// 可交换的插入。
		//
		// 源: packages/subagent/tool-subagent/src/index.ts:375-377
		IsConcurrencySafe: func(json.RawMessage) bool { return true },
		Output: tools.OutputDefinition{
			Schema: outputSchema(),
			Render: func(_ json.RawMessage, value json.RawMessage) (llm.Content, error) {
				var decoded delegationResult
				if err := json.Unmarshal(value, &decoded); err != nil {
					return nil, err
				}
				switch decoded.Kind {
				case kindBackground:
					return llm.Content{llm.TextBlock{
						Text: "started background subagent job " + decoded.JobID,
					}}, nil
				case kindContinuable:
					return llm.Content{llm.TextBlock{Text: "started subagent " + decoded.SubagentID}}, nil
				default:
					return llm.Content{llm.TextBlock{Text: outputValueText(decoded.Output)}}, nil
				}
			},
		},
		Execute: c.delegate,
		PresentCall: func(args json.RawMessage) tools.CallView {
			var decoded delegationArgs
			_ = json.Unmarshal(args, &decoded)
			return tools.GenericCallView{Title: decoded.Description, Kind: tools.CallExecute}
		},
	}
}

// mount 把那件工具装上去。
//
// 源: packages/subagent/tool-subagent/src/index.ts:290-441
//
// 两条能力检查都落在这里，而不是等第一次派发：装的这一刻是提供方的能力**第一次
// 已知**的时刻，一份装不成的配置在这里报出来才找得到人。那两句话是给运维看的，
// 但用词照抄 DSH，因为它们是照着配置字段名写的。
func (c *Controller) mount(ctx context.Context, provider subagent.Provider) error {
	if c.maxDepth != nil && !provider.Capabilities().DepthLimit {
		return fmt.Errorf(
			"tool-subagent: provider %q cannot enforce maxDepth (no depthLimit capability) — "+
				"set ProviderManagedDepth to leave the recursion budget to the provider", provider.Name())
	}
	if _, ok := provider.(subagent.ContinuablePreparer); c.continuable && !ok {
		return fmt.Errorf(
			"tool-subagent: provider %q does not support backgroundMode: continuable", provider.Name())
	}
	c.mutex.Lock()
	runtime, owner := c.deps.Tools, c.owner
	c.mutex.Unlock()
	if runtime == nil || owner == nil {
		return fmt.Errorf("subagenttool: 这次装配已经拆掉了")
	}
	dispose, err := runtime.Register(ctx, owner, c.newTool(provider.InheritsParentContext()))
	if err != nil {
		return err
	}
	c.mutex.Lock()
	c.disposeTool = dispose
	c.mutex.Unlock()
	return nil
}

// unmount 把那件工具摘下来，没装就什么都不做。
//
// 源: packages/subagent/tool-subagent/src/index.ts:452-456
func (c *Controller) unmount(ctx context.Context) error {
	c.mutex.Lock()
	dispose := c.disposeTool
	c.disposeTool = nil
	c.mutex.Unlock()
	if dispose == nil {
		return nil
	}
	return dispose(ctx)
}

// mounted 说明此刻那件工具装着没有。
func (c *Controller) mounted() bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.disposeTool != nil
}

// providerAdded 是那个「提供方来了」的观察者。
//
// 源: packages/subagent/tool-subagent/src/index.ts:449-451
//
// 只认点名的那一个，而且只在还没装的时候装：同一个提供方名重复登记不该装第二件
// 同名工具。
func (c *Controller) providerAdded(provider subagent.Provider) error {
	if provider.Name() != c.provider || c.mounted() {
		return nil
	}
	return c.mount(context.Background(), provider)
}

// providerRemoved 是那个「提供方走了」的观察者。
//
// 源: packages/subagent/tool-subagent/src/index.ts:452-456
func (c *Controller) providerRemoved(name string) {
	if name != c.provider {
		return
	}
	if err := c.unmount(context.Background()); err != nil {
		c.logger.Error("subagenttool: 摘那件派发工具失败", "provider", c.provider, "err", err)
	}
}

// sectionName 是那段后台指引的段名，跟着这次装配的工具名走。
//
// 源: packages/subagent/tool-subagent/src/index.ts:469
func (c *Controller) sectionName() string {
	return "tool:" + c.toolName
}

// sectionTextFor 求那段后台指引：工具不在的时候是空串。
//
// 源: packages/subagent/tool-subagent/src/index.ts:471-473
//
// 空正文会被渲染那一步整段丢掉，所以这一段跟着提供方在与不在自己开关，不用另挂
// 一条生命周期。除了「装着没有」还要问一遍工具运行时：这一段可能在一个看不见那件
// 工具的作用域上被装配。
func (c *Controller) sectionTextFor(_ context.Context, assemble systemprompt.AssembleContext) (string, error) {
	c.mutex.Lock()
	runtime, installed := c.deps.Tools, c.disposeTool != nil
	c.mutex.Unlock()
	if !installed || runtime == nil {
		return "", nil
	}
	if _, visible := runtime.Get(c.toolName, assemble.Scope); !visible {
		return "", nil
	}
	return sectionText(c.toolName), nil
}

// Install 把提供方那两个观察者、那件工具（提供方已经在的话）和那段后台指引一起装上
// 一个作用域，交回把它们一起摘下来的函数。
//
// 源: packages/subagent/tool-subagent/src/index.ts:443-475
//
// 次序照 DSH 的 apply：先挂两个观察者再查在不在——反过来的话，正好落在这中间的
// 那一次登记就丢了。中途失败按反序摘干净。
func (c *Controller) Install(
	ctx context.Context,
	owner *scope.Scope,
	deps Deps,
) (func(context.Context) error, error) {
	switch {
	case deps.Tools == nil:
		return nil, fmt.Errorf("subagenttool: 需要一个工具运行时")
	case deps.Subagents == nil:
		return nil, fmt.Errorf("subagenttool: 需要一条子 agent 接缝")
	case deps.Prompts == nil:
		return nil, fmt.Errorf("subagenttool: 需要一个系统提示词注册表")
	case owner == nil:
		return nil, fmt.Errorf("subagenttool: 需要一个作用域")
	}

	c.mutex.Lock()
	if c.deps.Tools != nil {
		c.mutex.Unlock()
		return nil, fmt.Errorf("subagenttool: 这个控制器已经装上了")
	}
	c.deps, c.owner = deps, owner
	c.mutex.Unlock()

	// 摘工具排在最前面，于是它在反序里跑在**最后**：那时候两个观察者已经摘掉了，
	// 不会再有新的工具被装上来。
	installed := []func(context.Context) error{c.unmount}
	undo := func(undoCtx context.Context) error {
		failures := make([]error, 0, len(installed))
		for index := len(installed) - 1; index >= 0; index-- {
			failures = append(failures, installed[index](undoCtx))
		}
		installed = nil
		c.mutex.Lock()
		c.deps, c.owner = Deps{}, nil
		c.mutex.Unlock()
		return errors.Join(failures...)
	}
	fail := func(what string, err error) (func(context.Context) error, error) {
		// 摘的时候不带调用方的取消：ctx 已经废了也得把装上去的收回来。
		_ = undo(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("subagenttool: 装%s失败：%w", what, err)
	}

	remove, err := deps.Subagents.OnProviderAdded(ctx, owner, c.providerAdded)
	if err != nil {
		return fail("提供方到场的观察者", err)
	}
	installed = append(installed, remove)

	remove, err = deps.Subagents.OnProviderRemoved(ctx, owner, c.providerRemoved)
	if err != nil {
		return fail("提供方离场的观察者", err)
	}
	installed = append(installed, remove)

	if provider, present := deps.Subagents.GetProvider(c.provider); present {
		if err := c.mount(ctx, provider); err != nil {
			return fail("那件派发工具", err)
		}
	} else {
		// 后端那条纤程可能晚一步才活起来；一个拼错的提供方名会一直留在这条日志里。
		c.logger.Info("subagenttool: 点名的子 agent 提供方还没登记，等它出现再装那件工具",
			"provider", c.provider, "tool", c.toolName)
	}

	if c.backgroundEnabled && c.continuable {
		remove, err = deps.Prompts.Section(ctx, owner, systemprompt.PromptSection{
			Name:  c.sectionName(),
			Order: SectionOrder,
			Text:  c.sectionTextFor,
		})
		if err != nil {
			return fail("后台派发指引", err)
		}
		installed = append(installed, remove)
	}
	return undo, nil
}
