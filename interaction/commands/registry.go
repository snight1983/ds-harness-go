// 本文件的作用：那张由插件自己填的人类命令注册表——一行斜杠命令的解析、
// 全局与按 agent 作用域的登记与遮蔽、以及一次执行从准入走到生命周期落地的全程。
//
// 源: packages/interaction/commands/src/index.ts

package commands

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"

	"ds-harness-go/attachment"
	"ds-harness-go/core/scope"
	"ds-harness-go/llm"
	"ds-harness-go/session"
)

// ErrInvalidDefinition 表示一份命令定义本身不合法，登记被拒。
var ErrInvalidDefinition = errors.New("commands: 命令定义不合法")

// ErrInvalidResult 表示处理器交回来的结果不合法。
//
// 它走的是「处理器炸了」那条路：落一条 command/done 的 error，然后把错误抛给调用方。
var ErrInvalidResult = errors.New("commands: 处理器交回来的结果不合法")

// ErrInvalidConfig 表示配置本身不成立，构造被拒。
var ErrInvalidConfig = errors.New("commands: 配置不成立")

// commandName 是一个合法命令名的形状。
//
// 源: packages/interaction/commands/src/index.ts:28
var commandName = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// commandHead 咬掉一行开头的斜杠和命令名。
//
// 源: packages/interaction/commands/src/index.ts:117
//
// 新增: DSH 那条正则用 `(?=$|[\t\n\r ])` 这个前瞻断言把「命令名后面必须是行尾或者
// 一个空白」这条边界写进正则里。Go 的 regexp 不支持前瞻（RE2 无回溯），所以那条边界
// 由 [Parse] 在匹配之后自己判一次——行为一字不差，只是换了个地方写。
var commandHead = regexp.MustCompile(`^/([a-z][a-z0-9_-]*)`)

// Parse 解出一行确切的斜杠命令，**不对**尾巴上的输入做任何归一化。
//
// 源: packages/interaction/commands/src/index.ts:116-123
//
// 不归一化是要紧的：命令名和输入之间那点分隔空白原样留在 RawInput 里，一条按列
// 对齐读参数的命令因此还看得见它。
func Parse(line string) (Parsed, bool) {
	match := commandHead.FindStringSubmatch(line)
	if match == nil {
		return Parsed{}, false
	}
	rest := line[len(match[0]):]
	if rest != "" {
		switch rest[0] {
		case '\t', '\n', '\r', ' ':
		default:
			// 命令名后面紧跟着别的字符，这一行就不是一条命令（`/goal/path`、`/goal🔥`）。
			return Parsed{}, false
		}
	}
	return Parsed{Name: match[1], RawInput: rest}, true
}

// Invocation 是交给某一条已登记命令的处理器的那次调用。
//
// 源: packages/interaction/commands/src/index.ts:34-51
//
// 新增: DSH 把 AbortSignal 挂在这个对象上。Go 里取消是 [Handler] 的第一个参数。
type Invocation struct {
	// ID 是已经写进这次调用那条 command/run 的配对号。
	ID ID
	// Agent 是收到这条命令的那个界面所属的 agent。
	Agent *scope.Key
	// RawInput 是登记的命令名后面那段一字不改的文字，分隔空白也在内。
	RawInput string
	// Attachments 是随这次调用一起准入的持久图块，按提交顺序；除非定义声明了
	// Input.Images，否则一定是空的。
	//
	// 它们要不要给模型看由处理器自己决定——注册表**从不**替它安排；一条语法上
	// 用不了这些图的调用应当交回一个错误结果，好让派发它的编辑器把原图留住。
	Attachments []llm.ImageBlock
}

// Handler 直接对着收到命令的那个 agent 干活，不把这条命令送给模型。
//
// 源: packages/interaction/commands/src/index.ts:68
//
// 返回一个错误等于 DSH 那边的处理器抛异常：会落一条 command/done 的 error，
// 然后这个错误原样抛给调用方。要表达一次「预期之内的失败」，交回
// Result{Kind: ResultError, Text: ...} 而不是错误。
type Handler func(ctx context.Context, invocation Invocation) (Result, error)

// Definition 是一次由插件发起的命令登记。
//
// 源: packages/interaction/commands/src/index.ts:54-69
type Definition struct {
	// Name 是不带斜杠的小写命令名。
	Name string
	// Description 是发现界面上那句人读的摘要。
	Description string
	// Input 是那点可选自由输入的元数据；nil 表示这条命令不收输入。
	Input *InputDescriptor
	// SkipInputRecord 为真时，command/run 不记 RawInput。
	//
	// 新增: DSH 是 `recordInput?: boolean`，缺省为真。Go 的零值是 false，直接照抄
	// 会让一个漏填的定义把输入悄悄从日志里抹掉；所以这里取反成 SkipInputRecord——
	// 零值就等于「记」，和 DSH 的默认行为对齐。
	//
	// 有一条自己的权威领域事件拥有这份负载的命令才把它设成真，免得在会话日志里
	// 把同一份负载抄两遍。
	SkipInputRecord bool
	// Handler 是那个直接干活的处理器。
	Handler Handler
}

// registered 是一条登记进来、已经验过、并且已经算好描述符的命令。
type registered struct {
	definition Definition
	descriptor Descriptor
}

// commandLayer 是一个全局层或者作用域层上的全部命令登记。
//
// 源: packages/interaction/commands/src/index.ts:85-102
type commandLayer struct {
	commands *scope.NamedEntries[registered]
}

// newCommandLayer 造一层，重名诊断按这一层的归属说话。
func newCommandLayer(key *scope.Key) (*commandLayer, error) {
	return &commandLayer{commands: scope.NewNamedEntries[registered](func(name string) error {
		if key == nil {
			return fmt.Errorf(
				"%w: command %q is already registered (for a per-agent variant, register it on that agent's own scope)",
				ErrInvalidDefinition, name)
		}
		return fmt.Errorf("%w: command %q is already registered in this scope", ErrInvalidDefinition, name)
	})}, nil
}

// IsEmpty 实现 [ds-harness-go/core/scope.Layer]。
func (l *commandLayer) IsEmpty() bool { return l.commands.IsEmpty() }

// Log 是一条会话日志：追加得进新事件。
//
// 新增: DSH 直接拿 `agent.session`。Go 里活会话是循环那一块的东西（见
// docs/DESIGN.md 第八节），本包在第 4 块，所以这里只声明自己真正要用的那一件事。
// 本包从不读日志——生命周期那一对是纯写，配对检查归 [Trace]。
type Log interface {
	// Append 往日志尾巴上追加一条事件。
	Append(kind session.EventType, data any) error
}

// Options 是造一个 [Runtime] 的选项。
//
// 源: packages/interaction/commands/src/index.ts:250-263
type Options struct {
	// LogOf 从一个 agent 找到它那条会话日志。
	//
	// 新增: 顶掉 DSH 的 `agent.session`。它是必填的：生命周期那一对不落日志，
	// 一次执行在回放时就再也说不清发生过。
	LogOf func(agent *scope.Key) (Log, error)

	// Attachments 是图片附件仓库，可以为 nil。
	//
	// 源: packages/interaction/commands/src/index.ts:362（`ctx.get('attachments')`）
	//
	// 为 nil 时，一次带图的调用会当场结算成错误结果——一条声明了收图的命令拿不到
	// 仓库就干不了活，这件事必须让用户看见，而不是把图默默丢掉。
	Attachments attachment.Store

	// Logger 记那两件被兜住的事：变更观察者自己炸了、以及失败路径上那条
	// command/done 也没写进去。为 nil 时用 [slog.Default]。
	Logger *slog.Logger

	// OnChange 在可见命令集发生变化时被调一次，可以为 nil。
	//
	// 源: packages/interaction/commands/src/types.ts:80（`commands/change`）
	//
	// 它是**不能否决**的：一个炸了的观察者不会让那次登记回滚，只会被记一条警告。
	// 界面刷新不该变成登记路径上的承重结构。
	OnChange func()

	// NewToken 生成这个实例的令牌；留空回落到 uuid 的前八位。
	//
	// 新增: DSH 直接调 crypto.randomUUID().slice(0, 8)。留一个口子是为了让测试
	// 断言得了配对号的**完整形状**，而不是只能断言「两个号不相等」。
	NewToken func() string
}

// Runtime 是人类命令的注册表与执行器。
//
// 源: packages/interaction/commands/src/index.ts:250-455
//
// 普通登记落在全局层；通过某个 agent 自己的作用域做的登记落在那一层，对那个 agent
// 遮蔽掉同名的全局命令。
type Runtime struct {
	layers      *scope.Layers[*commandLayer]
	logOf       func(*scope.Key) (Log, error)
	attachments attachment.Store
	logger      *slog.Logger
	onChange    func()

	// token 是这个实例的令牌，让配对号跨进程重启、在同一条恢复出来的日志上也不重复。
	token string
	// seq 是 [Runtime.mint] 背后那个实例内单调的计数器。
	//
	// 新增: 它只在 Execute 里递增，而 Execute 是可以被并发调用的（几个界面各按各的
	// 命令），所以这里比 DSH 那个裸计数器多一把锁——JS 是单线程，那边不需要。
	seq int
	// mutex 护住 seq。
	mutex chan struct{}
}

// NewRuntime 验一份选项，造出这张注册表。
func NewRuntime(options Options) (*Runtime, error) {
	if options.LogOf == nil {
		return nil, fmt.Errorf("%w: 需要一条从 agent 找到会话日志的路", ErrInvalidConfig)
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	newToken := options.NewToken
	if newToken == nil {
		newToken = func() string { return uuid.NewString()[:8] }
	}
	runtime := &Runtime{
		logOf:       options.LogOf,
		attachments: options.Attachments,
		logger:      logger,
		onChange:    options.OnChange,
		token:       newToken(),
		mutex:       make(chan struct{}, 1),
	}
	layers, err := scope.NewLayers(newCommandLayer, func() error {
		runtime.notifyChange()
		return nil
	})
	if err != nil {
		// 走不到：scope.NewLayers 只在造全局层时失败，而 newCommandLayer 不会失败。
		// 它是 scope 那一侧的签名，本包无权改；照实转出去比在这里吞掉它诚实。
		return nil, err
	}
	runtime.layers = layers
	return runtime, nil
}

// Register 登记一条全局的、或者属于某个 agent 作用域的命令，返回撤销它的函数。
//
// 源: packages/interaction/commands/src/index.ts:270-277
//
// 落在哪一层由 owner 的身份决定：[ds-harness-go/core/scope.NewRoot] 造的作用域落在
// 全局层；有身份的作用域落在自己那一层，对那个 agent 及其子孙遮蔽掉同名的全局命令。
func (r *Runtime) Register(
	ctx context.Context,
	owner *scope.Scope,
	definition Definition,
) (func(context.Context) error, error) {
	entry, err := normalizeDefinition(definition)
	if err != nil {
		return nil, err
	}
	return r.layers.Effect(ctx, owner, func(layer *commandLayer) (func(), error) {
		return layer.commands.Insert(entry.definition.Name, entry)
	}, scope.EffectOptions{Label: "commands.Register()"})
}

// List 交出某个 agent 眼里那份有效的命令描述符，按名字排序。
//
// 源: packages/interaction/commands/src/index.ts:284-290
//
// 排序在遮蔽**之后**做：一个作用域覆盖掉某个名字，那个名字在清单里的位置不该跟着变，
// 但清单交出去时按名字排，界面因此不必自己再排一遍。
func (r *Runtime) List(agent *scope.Key) []Descriptor {
	view := r.view(agent)
	list := make([]Descriptor, 0, len(view))
	for _, item := range view {
		list = append(list, item.Value.descriptor)
	}
	// 有效视图里名字唯一，所以不会有相等的两项。
	sort.Slice(list, func(left, right int) bool { return list[left].Name < list[right].Name })
	return list
}

// Find 解出某个 agent 眼里那条有效的命令定义。
//
// 源: packages/interaction/commands/src/index.ts:298-300
func (r *Runtime) Find(agent *scope.Key, name string) (Definition, bool) {
	for _, item := range r.view(agent) {
		if item.Name == name {
			return item.Value.definition, true
		}
	}
	return Definition{}, false
}

// Execute 解析并执行一条认得的命令，不把它送给模型。
//
// 源: packages/interaction/commands/src/index.ts:328-396
//
// 一条解析得出来的命令，它的生命周期是有记录的：处理器跑之前落 command/run，
// 结算之后落 command/done（一个炸了的、或者被取消的处理器落成 kind=error）。两条都是
// 直接的、只进日志的追加——**没有**回合把它们包起来，持久化在平常的检查点上把它们
// 排出去。准入没过（语法不成立、或者名字不认得）什么都不写：那两种情况从没进过处理器。
//
// 图片准入在**这里**判，不在编辑器那边判：把图发给一条没声明收图的命令、仓库没装、
// 以及超了限额，三种都在处理器跑起来之前结算成错误结果，而一批被拒的图不发布任何
// 持久对象。
//
// 交回 nil 表示语法或者名字没解析出来。
func (r *Runtime) Execute(
	ctx context.Context,
	agent *scope.Key,
	line string,
	images []attachment.EncodedImage,
) (*Execution, error) {
	parsed, ok := Parse(line)
	if !ok {
		return nil, nil
	}
	command, found := r.Find(agent, parsed.Name)
	if !found {
		return nil, nil
	}
	// 已经取消了就一个字节都不写：这次执行从没开始过。
	if cancelled := cancellation(ctx); cancelled != nil {
		return nil, cancelled
	}
	log, err := r.resolve(agent)
	if err != nil {
		return nil, err
	}
	id := r.mint()
	run := RunData{ID: id, Name: parsed.Name, Source: Source{Kind: SourceUser}}
	if !command.SkipInputRecord {
		run.Args = parsed.RawInput
	}
	if err := log.Append(EventRun, run); err != nil {
		// command/run 没写进去就大声失败：后面那条 command/done 会变成一条配不上对的
		// 孤儿记录，而那正是 [Trace] 要拦的东西。
		return nil, err
	}

	attachments, settled, err := r.admit(ctx, log, command, parsed.Name, id, images)
	if err != nil {
		return nil, err
	}
	if settled != nil {
		return settled, nil
	}

	result, err := r.invoke(ctx, command, Invocation{
		ID:          id,
		Agent:       agent,
		RawInput:    parsed.RawInput,
		Attachments: attachments,
	})
	if err != nil {
		r.settleThrown(log, parsed.Name, id, err)
		return nil, err
	}
	return r.settle(log, id, result)
}

// admit 走完图片准入这一段。
//
// 源: packages/interaction/commands/src/index.ts:357-385
//
// 三个返回值分别是：准入下来的图块、一次**已经结算完**的执行（那就不必再跑处理器了）、
// 以及要抛给调用方的错误。三者至多有一个非零。
func (r *Runtime) admit(
	ctx context.Context,
	log Log,
	command Definition,
	name string,
	id ID,
	images []attachment.EncodedImage,
) ([]llm.ImageBlock, *Execution, error) {
	if len(images) == 0 {
		return nil, nil, nil
	}
	if command.Input == nil || !command.Input.Images {
		settled, err := r.settle(log, id, Result{
			Kind: ResultError,
			Text: fmt.Sprintf("/%s does not accept image attachments", name),
		})
		return nil, settled, err
	}
	if r.attachments == nil {
		settled, err := r.settle(log, id, Result{
			Kind: ResultError,
			Text: fmt.Sprintf(
				"/%s: image attachments are unavailable because no attachment store is composed", name),
		})
		return nil, settled, err
	}
	refs, err := attachment.AdmitEncodedImages(ctx, r.attachments, images)
	if err != nil {
		var admission *attachment.Error
		if errors.As(err, &admission) {
			// 一条限额或者格式违规是**预期之内**的失败：把它当作这次执行的结果说给
			// 用户听，而不是把一个错误抛回派发它的界面。
			//
			// 取 Message 而不是 Error()：后者会在前面缀上包名、在后面缀上错误码，
			// 那两样是给日志和调用方分流用的，不该出现在一句说给操作者听的话里。
			// DSH 取的也是 error.message。
			settled, settleErr := r.settle(log, id, Result{Kind: ResultError, Text: admission.Message})
			return nil, settled, settleErr
		}
		r.settleThrown(log, name, id, err)
		return nil, nil, err
	}
	blocks := make([]llm.ImageBlock, 0, len(refs))
	for _, ref := range refs {
		blocks = append(blocks, llm.ImageBlock{Attachment: ref})
	}
	// 取消必须在**处理器跑起来之前**认掉：准入可能等一段慢存储，而一个在调用方
	// 已经撤回之后才进去的处理器，会动到那些状态，重试的调用方接着又动一遍。
	//（已经提交下去的图片对象成了没人引用的东西，那是延迟回收该管的事。）
	if cancelled := cancellation(ctx); cancelled != nil {
		r.settleThrown(log, name, id, cancelled)
		return nil, nil, cancelled
	}
	return blocks, nil, nil
}

// invoke 跑一次处理器，兜住它的 panic，并且把取消从它手里抢过来。
//
// 源: packages/interaction/commands/src/index.ts:146-167（withAbort）、388-394
//
// 新增: DSH 拿 promise 和 signal 赛跑。Go 里处理器是同步调用，所以赛跑写成
// 「处理器跑在一个 goroutine 里，结果送进一个容量 1 的 channel」——容量 1 保证取消
// 赢了之后那个 goroutine 仍然送得出去，不会挂在发送上泄漏。
//
// 赛跑结束之后再问一次 ctx：一个自己把这次请求取消掉、然后照常返回成功的处理器
// （DSH 那条 'observes an abort triggered synchronously inside the handler' 钉的就是它）
// 必须算取消，而不是让 select 去掷骰子。
func (r *Runtime) invoke(ctx context.Context, command Definition, invocation Invocation) (Result, error) {
	type outcome struct {
		result Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := contain(ctx, command.Handler, invocation)
		done <- outcome{result: result, err: err}
	}()

	var settled outcome
	select {
	case settled = <-done:
	case <-ctx.Done():
	}
	// 取消的判定放在 select **外面**，两条路合流之后只写一次：处理器自己把这次请求
	// 取消掉、然后照常返回成功时，两个 case 会同时就绪，而 select 在这种时候是掷骰子的。
	// 合流之后再问一次 ctx，那一局就变成确定的——取消永远赢。
	if cancelled := cancellation(ctx); cancelled != nil {
		return Result{}, cancelled
	}
	if settled.err != nil {
		return Result{}, settled.err
	}
	return normalizeResult(command.Name, settled.result)
}

// contain 跑一次处理器，把它的 panic 变成一个错误。
//
// 新增: DSH 那一层挡的是「同步就抛的处理器」。Go 的对应物是 panic：一条命令的处理器
// 是第三方插件代码，它炸了该让**这次执行**失败并留下审计，而不是把整个进程带走。
func contain(ctx context.Context, handler Handler, invocation Invocation) (result Result, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result, err = Result{}, fmt.Errorf("commands: 处理器 panic 了：%v", recovered)
		}
	}()
	return handler(ctx, invocation)
}

// settle 落这条 command/done 并交出这次执行。
//
// 源: packages/interaction/commands/src/index.ts:347-356
//
// 这一条**不兜**追加失败：走到这里说明处理器自己是好的，那条记录没写进去就是一次
// 真正的失败，得让调用方知道。被兜住的是 [Runtime.settleThrown] 那条路。
func (r *Runtime) settle(log Log, id ID, result Result) (*Execution, error) {
	done := DoneData{ID: id, Kind: result.Kind, Text: result.Text}
	if result.Kind == ResultSuccess {
		done.SourceEventSeq = result.SourceEventSeq
	}
	if err := log.Append(EventDone, done); err != nil {
		return nil, err
	}
	return &Execution{ID: id, Result: result}, nil
}

// settleThrown 在失败路径上落一条 command/done 的 error，追加失败被兜住。
//
// 源: packages/interaction/commands/src/index.ts:399-408
//
// 兜住是因为这里已经有一个要抛出去的错误了：让追加的失败盖掉处理器自己那条错误，
// 会把调用方引到完全错误的方向上去。
func (r *Runtime) settleThrown(log Log, name string, id ID, cause error) {
	if err := log.Append(EventDone, DoneData{ID: id, Kind: ResultError, Text: cause.Error()}); err != nil {
		r.logger.Warn("commands: command/done 没写进去", "command", name, "commandId", id, "error", err)
	}
}

// resolve 找出这个 agent 那条会话日志。
func (r *Runtime) resolve(agent *scope.Key) (Log, error) {
	log, err := r.logOf(agent)
	if err != nil {
		return nil, err
	}
	if log == nil {
		// 一条 (nil, nil) 的答复是装配方的 bug，而它往下走就是解引用 panic。
		return nil, fmt.Errorf("%w: 这个 agent 没有可写生命周期的会话日志", ErrInvalidConfig)
	}
	return log, nil
}

// mint 发下一个配对号。
//
// 源: packages/interaction/commands/src/index.ts:411-414
//
// 实例令牌打头，所以一条恢复出来的日志里、重启前后发出来的号绝不会撞上。
func (r *Runtime) mint() ID {
	r.mutex <- struct{}{}
	defer func() { <-r.mutex }()
	r.seq++
	return ID(fmt.Sprintf("cmd-%s-%d", r.token, r.seq))
}

// view 解出全局登记加上确切的作用域遮蔽。
//
// 源: packages/interaction/commands/src/index.ts:435-437
func (r *Runtime) view(agent *scope.Key) []scope.NamedValue[registered] {
	return scope.MergeNamed(r.layers, agent, func(layer *commandLayer) *scope.NamedEntries[registered] {
		return layer.commands
	})
}

// notifyChange 通知那个变更观察者，并且兜住它自己炸掉这件事。
//
// 源: packages/interaction/commands/src/index.ts:440-454
func (r *Runtime) notifyChange() {
	if r.onChange == nil {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			r.logger.Warn("commands: commands/change 观察者 panic 了", "panic", recovered)
		}
	}()
	r.onChange()
}

// cancellation 交出这个 ctx 此刻的取消原因，没取消就交出 nil。
//
// 源: packages/interaction/commands/src/index.ts:126-134（abortError / cancellationOf）
//
// 新增: DSH 把 signal.reason 归一化成一个 Error（不是 Error 也不是字符串时用
// 'command aborted'）。Go 里那件事是 [context.Cause] 天生就做的：装配方用
// [context.WithCancelCause] 带上自己那句话，没带就是 [context.Canceled]。
func cancellation(ctx context.Context) error {
	if ctx.Err() == nil {
		return nil
	}
	return context.Cause(ctx)
}

// normalizeDefinition 在这份元数据能走到任何界面协议之前，把不合法的挡回去。
//
// 源: packages/interaction/commands/src/index.ts:170-214
//
// 新增: DSH 那边一多半的检查是在验类型（description 是不是字符串、handler 是不是
// 函数、images 是不是布尔），因为一个 JS 插件递得进任何东西。Go 的类型系统已经把
// 那几条判掉了，剩下的是**类型表达不了**的那几条：名字的形状、以及两个不能全是空白
// 的字符串。
func normalizeDefinition(definition Definition) (registered, error) {
	if !commandName.MatchString(definition.Name) {
		return registered{}, fmt.Errorf(
			"%w: command name %q must match %s", ErrInvalidDefinition, definition.Name, commandName)
	}
	if strings.TrimSpace(definition.Description) == "" {
		return registered{}, fmt.Errorf(
			"%w: command %q description must not be empty", ErrInvalidDefinition, definition.Name)
	}
	if definition.Handler == nil {
		return registered{}, fmt.Errorf(
			"%w: command %q handler must not be nil", ErrInvalidDefinition, definition.Name)
	}
	descriptor := Descriptor{Name: definition.Name, Description: definition.Description}
	if definition.Input != nil {
		if strings.TrimSpace(definition.Input.Hint) == "" {
			return registered{}, fmt.Errorf(
				"%w: command %q input hint must not be empty", ErrInvalidDefinition, definition.Name)
		}
		// 复制一份：登记之后调用方再改自己那个结构体，不该把已经发布出去的描述符也改了。
		input := *definition.Input
		definition.Input = &input
		copied := input
		descriptor.Input = &copied
	}
	return registered{definition: definition, descriptor: descriptor}, nil
}

// normalizeResult 在注册表边界上验一份来路不明的处理器结果。
//
// 源: packages/interaction/commands/src/index.ts:217-243
//
// 新增: DSH 那边这个函数还要判 sourceEventSeq 是不是一个非负的安全整数——那两条在
// Go 里一条白送（int 就是整数）、一条留着（int 可以是负数）。至于「kind 是不是
// 那两个值之一」，Go 的 [ResultKind] 是个开放的字符串类型，所以照样要判。
func normalizeResult(name string, result Result) (Result, error) {
	switch result.Kind {
	case ResultSuccess:
		if result.SourceEventSeq != nil && *result.SourceEventSeq < 0 {
			return Result{}, fmt.Errorf(
				"%w: command %q success sourceEventSeq must be non-negative", ErrInvalidResult, name)
		}
		return result, nil
	case ResultError:
		if strings.TrimSpace(result.Text) == "" {
			return Result{}, fmt.Errorf(
				"%w: command %q error text must be a non-empty string", ErrInvalidResult, name)
		}
		// 一条错误结果不带 sourceEventSeq：那个槽的意思是「有一条更权威的成功呈现」。
		return Result{Kind: ResultError, Text: result.Text}, nil
	default:
		return Result{}, fmt.Errorf(
			"%w: command %q returned unknown result kind %q", ErrInvalidResult, name, result.Kind)
	}
}
