// 本文件的作用：这一层的主体——挂起的选择存在哪儿、它在什么时候落进日志、
// 那段提示词什么时候出现，以及五条胳膊怎么一次装齐、一次摘干净。
//
// 源: packages/plan/plan-mode/src/index.ts:165-462

package planmode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/snight1983/ds-harness-go/feature/interaction/commands"
	"github.com/snight1983/ds-harness-go/feature/interaction/userquestions"
	"github.com/snight1983/ds-harness-go/harness/agent"
	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/harness/systemprompt"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/sessionlog/projection"
	"github.com/snight1983/ds-harness-go/tools"
)

// sectionName 是那段计划指引在系统提示词里的段名。
//
// 源: packages/plan/plan-mode/src/index.ts:244
const sectionName = "plan:policy"

// sectionOrder 是那段指引的拼接次序。
//
// 源: packages/plan/plan-mode/src/index.ts:245
//
// 50 落在部署方人设（0）和工具指引（100–199）之间：计划模式是一层加在人设上的
// 协作约定，但它管不到具体某件工具怎么用。
const sectionOrder = 50

// Config 是这个控制器的部署配置。
//
// 源: packages/plan/plan-mode/src/index.ts:62-66（PlanModeConfig）
type Config struct {
	// Section 是计划模式开着时作为 `plan:policy` 段落发出去的那段指引。
	//
	// 它是部署方拥有的：本包一个字都不替它写。空的或者全是空白在 [New] 就被拒。
	Section string

	// AgentOf 从一把作用域钥匙找到那个 agent。
	//
	// 新增: 顶掉 DSH 的 `exec.agent` / `invocation.agent` / `context.agent`——那三处
	// 在 DSH 里都是同一个结构类型的 agent 对象。Go 这三处拿到的是不透明的
	// [scope.Key]，从它到 agent 的映射只有装配方知道（成例见
	// [github.com/snight1983/ds-harness-go/feature/interaction/commands.Options.LogOf]）。
	//
	// 它是必填的：退出工具、`/plan` 命令、那段提示词都要靠它找到会话，
	// 一条都少不了。认不出那把钥匙时返回错误——各处对「认不出」的反应不同，
	// 见各自的注释。
	AgentOf func(agent *scope.Key) (agent.Agent, error)

	// Logger 记那一件被兜住的事：步骤开头那次追加没写进去。为 nil 时用 [slog.Default]。
	Logger *slog.Logger
}

// Deps 是装这五条胳膊要用到的协作者。
//
// 新增: DSH 那边它们全从 cordis 容器里取——三个必需的走 static inject，
// 三个可选的走 ctx.inject 子节点和 ctx.get。Go 里没有那个容器，所以摊成一个
// 结构体，「在不在场」就是这个字段是不是 nil。
type Deps struct {
	// Agents 是活 agent 注册表，必填：步骤边界那条胳膊挂在它上面。
	Agents *agent.Registry
	// Tools 是工具注册表，必填：退出工具挂在它上面。
	Tools *tools.Runtime
	// Prompts 是系统提示词注册表，必填：那段指引挂在它上面。
	Prompts *systemprompt.Registry

	// Commands 是命令注册表，可以为 nil。为 nil 时不登记 `/plan`，别的胳膊照装——
	// 一个没有命令入口的装配（无头的、只跑模型的）不该因此整个装不上。
	Commands *commands.Runtime
	// Projections 是投影注册表，可以为 nil。为 nil 时界面读不到 plan 这个键，
	// 那正是「这个装配里没有投影能力」该有的样子。
	Projections *projection.Registry
	// Questions 是提问服务，可以为 nil。
	//
	// 它不在的时候退出工具**照样注册**——工具表在进出计划模式时必须纹丝不动，
	// 而「有没有人能评审」是调用那一刻才知道的事。真调到它时会失败，
	// 并且那句失败告诉模型改让用户自己切模式。
	Questions *userquestions.Service
}

// pendingIntent 是一次还没落进日志的选择。
//
// 源: packages/plan/plan-mode/src/index.ts:213
type pendingIntent struct {
	// active 是这次选择指向的状态。
	active bool
	// narrate 为真表示落盘时要给模型补一句旁白。
	//
	// 用户自己选的那几次是真（模型不知道人在界面上按了什么）；退出工具那一次是假
	// （那次调用的结果本身已经把这件事讲清楚了，再补一句是同一件事说两遍）。
	narrate bool
}

// Controller 拥有日志上的计划状态、在步骤开头把选定的状态落盘并旁白、
// 那段 `plan:policy` 指引、`/plan` 命令，以及那件一直挂着的退出工具。
//
// 源: packages/plan/plan-mode/src/index.ts:165-462
//
// 界面通过 session/event 看到已经提交的翻转；本包**不**维护任何活着的镜像。
type Controller struct {
	section  string
	agentOf  func(*scope.Key) (agent.Agent, error)
	logger   *slog.Logger
	question atomic.Pointer[userquestions.Service]

	// disposed 记这个控制器有没有被摘掉。
	//
	// 一次评审可能活得比这次装配还长；摘掉之后步骤前置那条胳膊没了，一个被同意的
	// 选择就再也没有机会落进日志。所以评审回来之后要再问一次。
	//
	// 新增: DSH 是构造函数闭包里的一个 let。Go 这里必须原子：评审是在工具执行体
	// 那条 goroutine 上等的，而摘除发生在装配方自己那条上。
	disposed atomic.Bool

	mutex   sync.Mutex
	pending map[sessionlog.SessionID]pendingIntent
}

// New 验一份配置，造出这个控制器。
//
// 源: packages/plan/plan-mode/src/index.ts:215-217
func New(config Config) (*Controller, error) {
	section, err := resolveSection(config.Section)
	if err != nil {
		return nil, err
	}
	if config.AgentOf == nil {
		return nil, fmt.Errorf("%w: 需要一条从作用域钥匙找到 agent 的路", ErrInvalidConfig)
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Controller{
		section: section,
		agentOf: config.AgentOf,
		logger:  logger,
		pending: make(map[sessionlog.SessionID]pendingIntent),
	}, nil
}

// Install 把这五条胳膊一次装齐，返回把它们一起摘下来的函数。
//
// 源: packages/plan/plan-mode/src/index.ts:218-431
//
// 中途装不上就把已经装上的按反序摘干净再报错。半装上去比装不上更坏：那意味着
// 模型手上有一件退出工具、而步骤边界上没有人接它选定的状态，一次「同意」会
// 悄无声息地丢掉。
//
// 同一个控制器只装一次：[Controller.disposed] 是整个值上的一面旗，装第二份的话
// 摘掉其中一份就会让另一份的评审全部开始报「服务已经被重载」。
func (c *Controller) Install(
	ctx context.Context,
	owner *scope.Scope,
	deps Deps,
) (func(context.Context) error, error) {
	switch {
	case deps.Agents == nil:
		return nil, fmt.Errorf("%w: 需要一个 agent 注册表", ErrInvalidConfig)
	case deps.Tools == nil:
		return nil, fmt.Errorf("%w: 需要一个工具注册表", ErrInvalidConfig)
	case deps.Prompts == nil:
		return nil, fmt.Errorf("%w: 需要一个系统提示词注册表", ErrInvalidConfig)
	}
	if c.disposed.Load() {
		return nil, fmt.Errorf("%w: 这个控制器已经被摘掉了，再装上去也接不住任何评审", ErrInvalidConfig)
	}
	c.question.Store(deps.Questions)

	var installed []func(context.Context) error
	undo := func(undoCtx context.Context) error {
		c.disposed.Store(true)
		var failures []error
		// 反序摘：装的时候后来的在里层，摘的时候先摘里层。
		for index := len(installed) - 1; index >= 0; index-- {
			if err := installed[index](undoCtx); err != nil {
				failures = append(failures, err)
			}
		}
		installed = nil
		return errors.Join(failures...)
	}

	steps := []struct {
		what    string
		install func() (func(context.Context) error, error)
	}{
		{"步骤边界", func() (func(context.Context) error, error) {
			return deps.Agents.OnPreStep(ctx, owner, c.preStep)
		}},
		{"计划指引段落", func() (func(context.Context) error, error) {
			return deps.Prompts.Section(ctx, owner, systemprompt.PromptSection{
				Name:  sectionName,
				Order: sectionOrder,
				Text:  c.sectionText,
			})
		}},
		{"plan 投影单元", func() (func(context.Context) error, error) {
			if deps.Projections == nil {
				return noopDisposer, nil
			}
			dispose, err := RegisterProjection(deps.Projections)
			if err != nil {
				return nil, err
			}
			return func(context.Context) error { dispose(); return nil }, nil
		}},
		{"/plan 命令", func() (func(context.Context) error, error) {
			if deps.Commands == nil {
				return noopDisposer, nil
			}
			return deps.Commands.Register(ctx, owner, c.commandDefinition())
		}},
		{"退出工具", func() (func(context.Context) error, error) {
			return deps.Tools.Register(ctx, owner, c.exitDefinition())
		}},
	}
	for _, step := range steps {
		dispose, err := step.install()
		if err != nil {
			// 摘的时候不带调用方的取消：ctx 已经废了也得把装上去的收回来。
			_ = undo(context.WithoutCancel(ctx))
			return nil, fmt.Errorf("planmode: 装「%s」失败：%w", step.what, err)
		}
		installed = append(installed, dispose)
	}
	return undo, nil
}

// noopDisposer 是一条没装上去的胳膊交出来的摘除函数。
func noopDisposer(context.Context) error { return nil }

// Get 读日志上的计划状态，以及那次还在等下一个被接受的、回合之内的步骤前置的选择。
//
// 源: packages/plan/plan-mode/src/index.ts:440-444
func (c *Controller) Get(target agent.Agent) State {
	if target == nil {
		return State{}
	}
	sess := target.Session()
	state := State{Active: FoldMode(sess.Events())}
	if pending, ok := c.pendingOf(sess.ID()); ok {
		value := pending.active
		state.Pending = &value
	}
	return state
}

// Set 选定计划模式开还是关。
//
// 源: packages/plan/plan-mode/src/index.ts:396-432
//
// 回合之间**立刻**落盘：在下一段提示开起一个新回合之前，不会再有回合之内的步骤
// 前置来接这次选择了。判断依据是日志里有没有一个开着的回合（[hasOpenTurn]），
// 不是 agent 的状态——一个 agent 在回合结束后的检查点期间状态仍然是 running。
// 一个开着的回合里，这次选择挂起来等下一个被接受的、回合之内的步骤前置。
// 重复选当下那个状态、或者已经挂着的那个状态，都是空操作。
func (c *Controller) Set(target agent.Agent, active bool) (Outcome, error) {
	if target == nil {
		return OutcomeNoop, errors.New("planmode: 需要一个 agent 才能切换计划模式")
	}
	sess := target.Session()
	events := sess.Events()
	logged := FoldMode(events)
	wanted := logged
	if pending, ok := c.pendingOf(sess.ID()); ok {
		wanted = pending.active
	}
	if active == wanted {
		return OutcomeNoop, nil
	}
	if hasOpenTurn(events) {
		c.setPending(sess.ID(), pendingIntent{active: active, narrate: true})
		if logged == active {
			return OutcomeCancelled, nil
		}
		return OutcomeQueued, nil
	}
	if active == logged {
		c.clearPending(sess.ID())
		return OutcomeCancelled, nil
	}
	// 追加成功之后才清挂起：一次没写下去的耐久写留着这条选择可以重试，而不是被丢掉。
	if err := appendMode(sess, active); err != nil {
		return OutcomeNoop, err
	}
	c.clearPending(sess.ID())
	// 旁白在追加**之后**算，和 DSH 同序：那条刚落下的 plan/mode 排在最后一条请求头
	// 后面，所以它不会改变 [modeAtLastHeader] 的答案，但同序省掉了「为什么这里能提前算」
	// 这个问题。
	if narration, ok := c.narration(sess.Events(), active); ok {
		target.Inject(narration)
	}
	return OutcomeCommitted, nil
}

// OnSessionDisposed 把这个会话上那条挂起的选择清掉。
//
// 新增: DSH 用 `WeakMap<Session, ...>` 记挂起的选择，会话被回收时那一条跟着没了。
// Go 没有弱引用，所以由装配方在会话散掉时叫这个方法（成例见
// [github.com/snight1983/ds-harness-go/feature/sessiontitle.Service.OnSessionDisposed]）。
//
// 不叫它的后果只是留下一条永远不会被读到的记录：会话身份不复用，所以它既不会
// 被误当成另一个会话的选择，也不会改变任何行为——只是一点不回收的内存。
func (c *Controller) OnSessionDisposed(id sessionlog.SessionID) {
	c.clearPending(id)
}

// preStep 是步骤边界上那条胳膊。
//
// 源: packages/plan/plan-mode/src/index.ts:223-240
//
// 它在**里层决定回来之后**才动手，因为要落盘的正是「这一步真的要进」这个条件。
// 步骤前置在 Session.Append 的发布路径之外，所以它可以在一个开着的回合里追加这条
// 只进日志的事件而不必重入会话。
//
// 一次没写下去的追加保持挂起，等下一个被接受的、回合之内的步骤前置再试，
// 并且**绝不**因此把这一步拦下来：计划指引没跟上是一件该被记一条警告的事，
// 不是一件该让 agent 停摆的事。
func (c *Controller) preStep(
	ctx context.Context,
	step agent.PreStep,
	next func(context.Context) (agent.PreStepDecision, error),
) (agent.PreStepDecision, error) {
	decision, err := next(ctx)
	if err != nil {
		return decision, err
	}
	if step.Agent == nil {
		return decision, nil
	}
	sess := step.Agent.Session()
	pending, ok := c.pendingOf(sess.ID())
	if !decision.Enter || ctx.Err() != nil || !ok {
		return decision, nil
	}
	// 旁白在追加之前算：这样它看到的是「上一次告诉模型的是哪种模式」，
	// 而不是刚刚写下去的那条。两者其实同值（新事件排在最后一条请求头后面），
	// 同序是为了少一处需要论证的地方。
	narration, hasNarration := c.narration(sess.Events(), pending.active)
	if err := c.onBoundary(sess); err != nil {
		c.logger.Warn("planmode: 步骤开头没能把选定的计划模式写进日志",
			"session", string(sess.ID()), "error", err)
		return decision, nil
	}
	if !pending.narrate || !hasNarration {
		return decision, nil
	}
	// 复制一份再追加：里层交出来的那张切片可能还被别人拿着。
	messages := make([]llm.Message, 0, len(decision.Messages)+1)
	messages = append(messages, decision.Messages...)
	decision.Messages = append(messages, narration)
	return decision, nil
}

// onBoundary 在下一次请求装配之前把那一条挂起的选择落进日志。
//
// 源: packages/plan/plan-mode/src/index.ts:434-447
func (c *Controller) onBoundary(sess *coresession.Session) error {
	pending, ok := c.pendingOf(sess.ID())
	if !ok {
		return nil
	}
	if pending.active == FoldMode(sess.Events()) {
		c.clearPending(sess.ID())
		return nil
	}
	if err := appendMode(sess, pending.active); err != nil {
		return err
	}
	// 追加成功之后才清：一次没写下去的耐久写留给下一个被接受的、回合之内的步骤
	// 前置去重试。
	c.clearPending(sess.ID())
	return nil
}

// narration 在「最后一次告诉模型的是另一种模式」时造一条切换通知。
//
// 源: packages/plan/plan-mode/src/index.ts:449-461
//
// 第一次请求之前不通知（模型还没被告知过任何模式，没有可纠正的印象），
// 已经是那种模式时也不通知。
func (c *Controller) narration(events []sessionlog.Event, target bool) (llm.Message, bool) {
	told, known := modeAtLastHeader(events)
	if !known || told == target {
		return llm.Message{}, false
	}
	text := "The user switched this session back to the default mode."
	if target {
		text = "The user switched this session to plan mode."
	}
	return llm.NewUserMessage(
		llm.Content{llm.TextBlock{Text: text}},
		// 这句旁白本来就只有一句，所以它自己就是自己的摘要。
		llm.PluginSource{Plugin: "plan-mode", Context: llm.NoticeContext{Summary: text}},
	), true
}

// sectionText 求那段 `plan:policy` 指引。
//
// 源: packages/plan/plan-mode/src/index.ts:243-251
//
// 挂起的选择盖过日志：一次在回合中途选下的进入，从**下一次**装配起就要带上指引，
// 而那次装配就发生在把它落盘的那个步骤前置紧后面。
//
// 认不出这次装配算谁的（没有作用域，或者那把钥匙不是一个活 agent）就贡献空串，
// 和 DSH 的 `context.agent === undefined` 一样：那种装配没有会话，也就没有可折的
// 计划状态。这不是把错误吞掉——它是这段指引本来就该缺席的那种情形。
func (c *Controller) sectionText(_ context.Context, assemble systemprompt.AssembleContext) (string, error) {
	if assemble.Scope == nil {
		return "", nil
	}
	target, err := c.agentOf(assemble.Scope)
	if err != nil || target == nil {
		return "", nil
	}
	sess := target.Session()
	active := FoldMode(sess.Events())
	if pending, ok := c.pendingOf(sess.ID()); ok {
		active = pending.active
	}
	if !active {
		return "", nil
	}
	return c.section, nil
}

// pendingOf 读一个会话上那条挂起的选择。
func (c *Controller) pendingOf(id sessionlog.SessionID) (pendingIntent, bool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	pending, ok := c.pending[id]
	return pending, ok
}

// setPending 记下一个会话上那条挂起的选择，顶掉之前那条。
func (c *Controller) setPending(id sessionlog.SessionID, pending pendingIntent) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.pending[id] = pending
}

// clearPending 清掉一个会话上那条挂起的选择。
func (c *Controller) clearPending(id sessionlog.SessionID) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	delete(c.pending, id)
}

// appendMode 往日志里写一条 [EventMode]。
func appendMode(sess *coresession.Session, active bool) error {
	payload, err := json.Marshal(ModeData{Active: active})
	if err != nil {
		return err
	}
	if _, err := sess.Append(sessionlog.Event{Type: EventMode, Data: payload}); err != nil {
		return fmt.Errorf("planmode: 计划模式写不进日志：%w", err)
	}
	return nil
}
