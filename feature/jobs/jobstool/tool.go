// 本文件的作用：job_output、job_list、job_kill 这三件工具的本体——它们给模型看的
// 说明、那份「这次调用能看见多大预算」的旁路记账、一条结算通知怎么找到它的属主，
// 以及把这一整套装上一个作用域的那一步。
//
// 源: packages/jobs/tool-jobs/src/index.ts:184-401

package jobstool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/snight1983/ds-harness-go/feature/jobs"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/harness/systemprompt"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/tools"
)

// 这三个是三件工具在模型那边的名字。
//
// 源: packages/jobs/tool-jobs/src/index.ts:288,383,398
const (
	// OutputToolName 读一件作业的增量或者最终输出。
	OutputToolName = "job_output"
	// ListToolName 列出调用方看得见的那些作业。
	ListToolName = "job_list"
	// KillToolName 请求取消一件还在跑的作业。
	KillToolName = "job_kill"
)

// SectionName 和 SectionOrder 是那段跨调用指引在系统提示词里的位置：跟在 bash
// 那一段后面，排在各产品段落前面。
//
// 源: packages/jobs/tool-jobs/src/index.ts:283-284
const (
	SectionName  = "tool:jobs"
	SectionOrder = 106
)

// promptText 是那段跨调用指引，一个字都不许改译。
//
// 源: packages/jobs/tool-jobs/src/index.ts:285
const promptText = "Track every background job id you start. You are notified in-session when a job finishes " +
	"— do not busy-poll or sleep on one; keep working on independent steps and do not duplicate a running " +
	"job's work. Before giving a final answer, collect every still-relevant job with job_output (set wait: " +
	"true only when you are genuinely blocked on it), and job_kill jobs that stopped mattering."

// outputDescription 是 job_output 给模型看的说明。
//
// 源: packages/jobs/tool-jobs/src/index.ts:289-292
const outputDescription = "Read a background job. Stream jobs return only output since the previous read; " +
	"final-output jobs return their result after settlement. Every response ends with " +
	"`[status: ...]`. Reads are non-blocking unless `wait: true`, which waits up to the configured cap."

// listDescription 是 job_list 给模型看的说明。
//
// 源: packages/jobs/tool-jobs/src/index.ts:340
const listDescription = "List your background jobs (running and finished) with their ids, kinds, and statuses."

// killDescription 是 job_kill 给模型看的说明。
//
// 源: packages/jobs/tool-jobs/src/index.ts:359
const killDescription = "Request cancellation of a running background job by job id. Returns immediately; " +
	"the job settles as killed once its work actually stops."

// 这几条是三件工具的参数说明。
//
// 源: packages/jobs/tool-jobs/src/index.ts:296-298,362-363
const (
	jobIDDescription = "Job id returned by the tool that started the background work."
	waitDescription  = "Block until the job reaches a terminal status or the timeout expires. " +
		"A timed-out wait returns [status: running] and leaves the job alive."
	timeoutDescription = "Max wait in milliseconds (only meaningful with wait: true). " +
		"Defaults to the configured wait timeout; capped by the configured maximum."
	reasonDescription = "Optional short reason, recorded in the log and forwarded to the job."
)

// noJobs 是 job_list 一件都没列到时那句话。
//
// 源: packages/jobs/tool-jobs/src/index.ts:348
const noJobs = "(no background jobs)"

// 这两个是 job_kill 那份结果里 outcome 的取值。
//
// 源: packages/jobs/tool-jobs/src/index.ts:368-372,391-392
const (
	outcomeRequested       = "cancellation-requested"
	outcomeAlreadyFinished = "already-finished"
)

// Controller 是攥着那台作业注册表、并且知道怎么把这三件工具连同投递规矩一起装上
// 一个作用域的那个对象。
//
// 源: packages/jobs/tool-jobs/src/index.ts:205-222
type Controller struct {
	service     Service
	agentOf     func(agent *scope.Key) (agent.Agent, error)
	waitDefault time.Duration
	waitCap     time.Duration
	delivery    CompletionDelivery
	wakeBudget  int

	// mutex 罩着下面三张表：结算来自生产方那条协程，工具调用来自模型那条。
	mutex sync.Mutex
	// spentWakes 是这个插件在每个属主上开过几个回合——从那个属主上一次消费掉
	// 人写的输入算起。键是那个确切的 Agent，所以同一个会话里换了一个新实例，
	// 它拿到的是满额预算。
	//
	// 源: packages/jobs/tool-jobs/src/index.ts:212-215
	spentWakes map[agent.Agent]int
	// wakeCleanups 是每个属主身上挂的那一项清理的摘除函数，做法同
	// [github.com/snight1983/ds-harness-go/adapter/localjobs.Registry.ensureOwnerCleanup]：Go 没有 WeakMap，
	// 所以由属主自己的作用域负责把它那一行从表里删掉；攥住摘除函数是为了让本包
	// 拆除时能把这些跨协程的清理收回来。
	wakeCleanups map[agent.Agent]func(context.Context) error
	// outputLimits 是每次调用在派发前记下的那个字节预算，键是执行令牌。
	//
	// 源: packages/jobs/tool-jobs/src/index.ts:231
	outputLimits map[tools.ExecutionToken]int
}

// jobTargetArgs 是那两件指名一件作业的工具共用的那部分参数。
type jobTargetArgs struct {
	JobID string `json:"job_id"`
}

// outputArgs 是 job_output 的参数。
//
// 新增: TimeoutMS 是指针，因为「没写」和「写了 0」在这里不是一回事：没写取
// [Config.WaitTimeout]，写了 0 就是不等。类型取 float64 而不是整数，是为了让
// DSH 那边 `type: 'number'` 收得下的小数在这里同样收得下——一个被 Go 拒掉、
// 却被 DSH 接受的入参，会让同一个模型在两边表现不一样。
type outputArgs struct {
	JobID     string   `json:"job_id"`
	Wait      bool     `json:"wait"`
	TimeoutMS *float64 `json:"timeout_ms"`
}

// killArgs 是 job_kill 的参数。
type killArgs struct {
	JobID  string `json:"job_id"`
	Reason string `json:"reason"`
}

// outputResult 是 job_output 那份权威的结果值。
//
// 源: packages/jobs/tool-jobs/src/index.ts:304-313
type outputResult struct {
	Text string         `json:"text"`
	Job  PublicSnapshot `json:"job"`
}

// killOutcome 是 job_kill 那份权威的结果值。
//
// 源: packages/jobs/tool-jobs/src/index.ts:365-375
type killOutcome struct {
	Outcome string         `json:"outcome"`
	Job     PublicSnapshot `json:"job"`
}

// validateJobID 守住那条 schema 表达不出来的约束：非空。
//
// 源: packages/jobs/tool-jobs/src/index.ts:191-197
//
// 这句话是给模型看的，所以是英文；`%q` 排出来的引号和 DSH 那边 JSON.stringify
// 排出来的是同一份形状。
func validateJobID(value string) (jobs.JobID, error) {
	if value == "" {
		return "", fmt.Errorf("invalid job_id: expected a non-empty string, got %q", value)
	}
	return jobs.JobID(value), nil
}

// callerOf 把这次执行落在的那把钥匙换成那个活 agent，换不出来就是 nil。
//
// 新增: 和 [github.com/snight1983/ds-harness-go/feature/subagent/controltool.Controller.callerOf] 不同，这里
// **查不回来不是错**：这三件工具对一个无身份的调用方照样成立，它看得见的就是
// 那些无主作业。理由写在 [Config.AgentOf] 上。
func (c *Controller) callerOf(key *scope.Key) agent.Agent {
	if key == nil {
		return nil
	}
	caller, err := c.agentOf(key)
	if err != nil {
		return nil
	}
	return caller
}

// visibleOutputLimit 找出这次调用指着的那件作业自己的字节预算。
//
// 源: packages/jobs/tool-jobs/src/index.ts:184-189
//
// 只有那两件指名一件作业的工具有预算可言：job_list 交出去的是一份清单，
// 它不属于任何一件作业，也就没有哪件作业的上限管得着它。
//
// 新增: ctx 由调用方给。[tools.PreRule] 和 [tools.Definition.FinalizeContent]
// 这两条缝都是不收 ctx 的（照 DSH 那两条事件监听器的形状），所以那两处交的是
// [context.Background]——这次列举现在会失败（见 [jobs.Registry]），而它失败的后果
// 恰好就是这个函数本来就有的那条「没有预算」出口：整段内容按老办法整段让。
// 一次读不出账本的调用因此**不会**被这条旁路弄失败。
func (c *Controller) visibleOutputLimit(ctx context.Context, exec tools.Execution) (int, bool) {
	if exec.Name != OutputToolName && exec.Name != KillToolName {
		return 0, false
	}
	var target jobTargetArgs
	// 解不动或者没给 id 都当成「没有预算」：那种调用本来也会在执行体里被拒掉。
	if err := json.Unmarshal(exec.Arguments, &target); err != nil || target.JobID == "" {
		return 0, false
	}
	wanted := jobs.JobID(target.JobID)
	snapshots, err := c.service.List(ctx, c.callerOf(exec.Agent))
	if err != nil {
		return 0, false
	}
	for _, snapshot := range snapshots {
		if snapshot.ID == wanted {
			return snapshot.OutputLimitBytes, true
		}
	}
	return 0, false
}

// captureOutputLimit 是那条派发前规则：把预算记在这次执行的令牌下面。
//
// 源: packages/jobs/tool-jobs/src/index.ts:232-236
//
// 新增: DSH 用 `{prepend: true}` 把这条规则排到瀑布最外层，好让它在任何一条可能
// 拒掉调用的规则**之前**跑。Go 侧 [github.com/snight1983/ds-harness-go/tools.Runtime.PreExecute]
// 没有 prepend——顺序是「先全局、再从最远的祖先到 agent 自己，先登记的在外层」。
// 这不影响正确性：记不上的那次调用会在收尾时回头现查一遍（见
// [Controller.finalizeContent]），记账只是省掉那一次查。
func (c *Controller) captureOutputLimit(exec tools.Execution, next func() (tools.PreDecision, error)) (tools.PreDecision, error) {
	if maxBytes, ok := c.visibleOutputLimit(context.Background(), exec); ok {
		c.mutex.Lock()
		c.outputLimits[exec.Token] = maxBytes
		c.mutex.Unlock()
	}
	return next()
}

// takeOutputLimit 取走这次执行记下的预算，取不到就现查一遍。
//
// 源: packages/jobs/tool-jobs/src/index.ts:238-240
//
// 无论取没取到都要删：[github.com/snight1983/ds-harness-go/tools.Definition.FinalizeContent] 对
// 每一份规范化过的结果恰好被调一次，这一次就是这张表的摘除点。
func (c *Controller) takeOutputLimit(exec tools.Execution) (int, bool) {
	c.mutex.Lock()
	maxBytes, recorded := c.outputLimits[exec.Token]
	delete(c.outputLimits, exec.Token)
	c.mutex.Unlock()
	if recorded {
		return maxBytes, true
	}
	return c.visibleOutputLimit(context.Background(), exec)
}

// finalizeContent 把一次调用交给模型的那段内容收进这件作业自己的预算。
//
// 源: packages/jobs/tool-jobs/src/index.ts:237-255
//
// 交回 nil 表示不动它——那正是 DSH 那边 return undefined 的意思。
func (c *Controller) finalizeContent(exec tools.Execution, result tools.Result) llm.Content {
	maxBytes, ok := c.takeOutputLimit(exec)
	if !ok {
		return nil
	}
	if exec.Name == OutputToolName && !result.IsError {
		if content, suffix, matched := splitRenderedOutput(result); matched {
			return llm.Content{llm.TextBlock{Text: fitWithSuffix(content, suffix, maxBytes, outputOmitted)}}
		}
	}
	return boundSingleText(result.Content, maxBytes)
}

// splitRenderedOutput 认一份**还是默认渲染**的 job_output 结果，把它拆回「正文」
// 和「那行必须留住的状态」。
//
// 源: packages/jobs/tool-jobs/src/index.ts:241-253
//
// 本包自己拥有并按 schema 验过那个权威值，所以正文和状态行拆得开；但只有当眼前
// 这段内容确实就是照那个值渲染出来的，这个拆法才成立——中间要是有别的规则改过
// 内容，按老办法整段让就是唯一安全的做法。
func splitRenderedOutput(result tools.Result) (content, suffix string, matched bool) {
	var value outputResult
	if err := json.Unmarshal(result.Value, &value); err != nil {
		return "", "", false
	}
	body := value.Text
	if body == "" {
		body = noNewOutput
	}
	// 只削掉**一个**结尾换行，和渲染那边补上的那一个对得上。
	content = strings.TrimSuffix(body, "\n")
	suffix = "\n" + StatusLine(jobs.JobStatus(value.Job.Status), value.Job.Detail)
	text, single := rawSingleText(result.Content)
	if !single || text != content+suffix {
		return "", "", false
	}
	return content, suffix, true
}

// deliver 是那个完成监听器：一件作业结算了，把通知送到它属主手上。
//
// 源: packages/jobs/tool-jobs/src/index.ts:277-311（onJobDone 那一段）
//
// 用的是那个确切的生命周期属主，不是可复用的 id——一个能复用的 id 有可能指到
// 一个替补身上。已汇报的结算不再投一次：拆除带来的那些本来就带着「已汇报」。
func (c *Controller) deliver(snapshot jobs.Snapshot, owner agent.Agent) {
	if snapshot.Reported || owner == nil {
		return
	}
	message := llm.NewUserMessage(
		llm.Content{llm.TextBlock{Text: fitCompletionNotice(snapshot)}},
		llm.PluginSource{
			Plugin:  PluginName,
			Context: llm.NoticeContext{Summary: completionSummary(snapshot)},
		},
	)
	if c.delivery == DeliveryWakeup && owner.Status() == agent.StatusIdle && c.spendWake(owner) {
		owner.Followup(message)
		return
	}
	owner.Inject(message)
}

// spendWake 从这个属主的唤醒预算里扣一个回合，扣得动就返回 true。
//
// 源: packages/jobs/tool-jobs/src/index.ts:305-309
func (c *Controller) spendWake(owner agent.Agent) bool {
	// 先把清理挂上再记账：挂不上说明这个属主的作用域正在散，那它也不该被唤醒，
	// 而且那一行会一直留在表里没人删。理由同
	// [github.com/snight1983/ds-harness-go/adapter/localjobs.Registry.ensureOwnerCleanup] 里那句注释。
	if !c.ensureWakeCleanup(owner) {
		return false
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.spentWakes[owner] >= c.wakeBudget {
		return false
	}
	c.spentWakes[owner]++
	return true
}

// ensureWakeCleanup 保证这个属主身上挂着一项「散的时候把它那行记账删掉」的清理。
//
// 新增: 这是 DSH 那张 `WeakMap<Agent, number>` 在 Go 里的替身，做法和
// [github.com/snight1983/ds-harness-go/adapter/localjobs.Registry.ensureOwnerCleanup] 逐字相同。
func (c *Controller) ensureWakeCleanup(owner agent.Agent) bool {
	c.mutex.Lock()
	_, already := c.wakeCleanups[owner]
	c.mutex.Unlock()
	if already {
		return true
	}
	ownerScope := owner.Scope()
	if ownerScope == nil {
		return false
	}
	detach, err := ownerScope.Defer("jobstool.wakeBudget()", func(context.Context) error {
		c.mutex.Lock()
		delete(c.spentWakes, owner)
		delete(c.wakeCleanups, owner)
		c.mutex.Unlock()
		return nil
	})
	if err != nil {
		return false
	}
	// 挂成功了才记账：一个正在释放的作用域会拒绝新的清理。
	c.mutex.Lock()
	c.wakeCleanups[owner] = detach
	c.mutex.Unlock()
	return true
}

// refillWakeBudget 是那个收件箱观察者：属主消费掉一条人写的输入，预算清零。
//
// 源: packages/jobs/tool-jobs/src/index.ts:225-231
//
// 认领才是那条人写的输入真正进到一个步骤里的那一刻；本插件自己排进去的那条通知
// 不许把它刚花掉的预算补回来，所以这里只认 [github.com/snight1983/ds-harness-go/llm.SourceUser]。
func (c *Controller) refillWakeBudget(owner agent.Agent, message llm.Message, _ int) {
	if message.Source == nil || message.Source.SourceKind() != llm.SourceUser {
		return
	}
	c.mutex.Lock()
	delete(c.spentWakes, owner)
	c.mutex.Unlock()
}

// releaseWakeCleanups 把挂在各属主身上的那些清理全摘下来，并清空记账。
//
// 新增: DSH 那张 WeakMap 跟着插件对象一起被回收，没有这一步。Go 这边那些闭包
// 挂在**属主自己**的作用域上，活得比本包一次装配长，不摘就是一处泄漏。
func (c *Controller) releaseWakeCleanups(ctx context.Context) error {
	c.mutex.Lock()
	detachers := make([]func(context.Context) error, 0, len(c.wakeCleanups))
	for owner, detach := range c.wakeCleanups {
		detachers = append(detachers, detach)
		delete(c.wakeCleanups, owner)
		delete(c.spentWakes, owner)
	}
	c.mutex.Unlock()
	failures := make([]error, 0, len(detachers))
	for _, detach := range detachers {
		failures = append(failures, detach(ctx))
	}
	return errors.Join(failures...)
}

// presentTaskCall 是这三件通用作业控制在界面上那张待定的卡片。
//
// 源: packages/jobs/tool-jobs/src/index.ts:199-202
//
// 新增: DSH 的 rawInput 是 unknown，一个裸字符串直接就能放；Go 侧它是
// [encoding/json.RawMessage]，所以先排一遍，排不出去就留空——呈现是纯函数，
// 不许失败。空的 job_id 一律不放进卡片：那种调用会在执行体里被 [validateJobID]
// 拒掉，一张写着 `""` 的卡片只会让人以为参数是这么传的。
func presentTaskCall(title string, kind tools.CallKind, rawInput string) tools.CallView {
	view := tools.GenericCallView{Title: title, Kind: kind}
	if rawInput != "" {
		if encoded, err := json.Marshal(rawInput); err == nil {
			view.RawInput = encoded
		}
	}
	return view
}

// newOutputTool 造那件 job_output 工具。
//
// 源: packages/jobs/tool-jobs/src/index.ts:287-338
func (c *Controller) newOutputTool() *tools.Definition {
	closed := false
	jobSchema := publicJobSchema()
	return &tools.Definition{
		Name:        OutputToolName,
		Description: outputDescription,
		// 一次等到超时交回的是作业状态，不是一份 TOOL_TIMEOUT 错误，所以这件工具
		// 自己管期限，不用 [github.com/snight1983/ds-harness-go/tools.Definition.Timeout]。
		//
		// 源: packages/jobs/tool-jobs/src/index.ts:293-294
		Parameters: tools.Node{
			Type: tools.TypeObject,
			Properties: []tools.Property{
				{Name: "job_id", Schema: tools.Node{Type: tools.TypeString, Description: jobIDDescription}},
				{Name: "wait", Schema: tools.Node{Type: tools.TypeBoolean, Description: waitDescription}},
				{Name: "timeout_ms", Schema: tools.Node{Type: tools.TypeNumber, Description: timeoutDescription}},
			},
			Required: []string{"job_id"},
		},
		FinalizeContent: c.finalizeContent,
		Output: tools.OutputDefinition{
			Schema: tools.Node{
				Type: tools.TypeObject,
				Properties: []tools.Property{
					{Name: "text", Schema: tools.Node{Type: tools.TypeString}},
					{Name: "job", Schema: jobSchema},
				},
				Required:             []string{"text", "job"},
				AdditionalProperties: &closed,
			},
			Render: func(_ json.RawMessage, value json.RawMessage) (llm.Content, error) {
				var decoded outputResult
				if err := json.Unmarshal(value, &decoded); err != nil {
					return nil, err
				}
				body := decoded.Text
				if body == "" {
					body = noNewOutput
				}
				separator := "\n"
				if strings.HasSuffix(body, "\n") {
					separator = ""
				}
				return llm.Content{llm.TextBlock{
					Text: body + separator + StatusLine(jobs.JobStatus(decoded.Job.Status), decoded.Job.Detail),
				}}, nil
			},
		},
		Execute: c.readOutput,
		PresentCall: func(args json.RawMessage) tools.CallView {
			var target jobTargetArgs
			_ = json.Unmarshal(args, &target)
			return presentTaskCall("Read output from background job "+target.JobID, tools.CallRead, target.JobID)
		},
	}
}

// readOutput 是 job_output 的体。
//
// 源: packages/jobs/tool-jobs/src/index.ts:326-334
//
// 新增: DSH 把 `exec.signal` 传给 wait。Go 里那个取消口就是第一个参数 ctx，
// 工具运行时已经把调用方的期限和取消装在里面了。
func (c *Controller) readOutput(
	ctx context.Context,
	args json.RawMessage,
	exec *tools.RunContext,
) (json.RawMessage, error) {
	var input outputArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}
	id, err := validateJobID(input.JobID)
	if err != nil {
		return nil, err
	}
	caller := c.callerOf(execAgent(exec))
	if input.Wait {
		if _, err := c.service.Wait(ctx, id, c.waitFor(input.TimeoutMS), caller); err != nil {
			return nil, err
		}
	}
	read, err := c.service.Read(ctx, id, caller)
	if err != nil {
		return nil, err
	}
	return json.Marshal(outputResult{Text: read.Text, Job: publicJob(read.Snapshot)})
}

// waitFor 把模型给的那个毫秒数折成一段等待，没给就取默认，再夹到硬上限。
//
// 源: packages/jobs/tool-jobs/src/index.ts:328-329
func (c *Controller) waitFor(timeoutMS *float64) time.Duration {
	timeout := c.waitDefault
	if timeoutMS != nil {
		timeout = time.Duration(*timeoutMS * float64(time.Millisecond))
	}
	return min(timeout, c.waitCap)
}

// newListTool 造那件 job_list 工具。
//
// 源: packages/jobs/tool-jobs/src/index.ts:339-357
func (c *Controller) newListTool() *tools.Definition {
	jobSchema := publicJobSchema()
	return &tools.Definition{
		Name:        ListToolName,
		Description: listDescription,
		Parameters:  tools.Node{Type: tools.TypeObject},
		Output: tools.OutputDefinition{
			Schema: tools.Node{Type: tools.TypeArray, Items: &jobSchema},
			Render: func(_ json.RawMessage, value json.RawMessage) (llm.Content, error) {
				var decoded []PublicSnapshot
				if err := json.Unmarshal(value, &decoded); err != nil {
					return nil, err
				}
				if len(decoded) == 0 {
					return llm.Content{llm.TextBlock{Text: noJobs}}, nil
				}
				lines := make([]string, 0, len(decoded))
				for _, snapshot := range decoded {
					lines = append(lines, snapshot.ID+" ["+snapshot.Kind+"] "+snapshot.Status+" — "+snapshot.Label)
				}
				return llm.Content{llm.TextBlock{Text: strings.Join(lines, "\n")}}, nil
			},
		},
		Execute: c.listJobs,
		PresentCall: func(json.RawMessage) tools.CallView {
			return presentTaskCall("List background jobs", tools.CallRead, "")
		},
	}
}

// listJobs 是 job_list 的体。
//
// 源: packages/jobs/tool-jobs/src/index.ts:352-355
//
// 新增: 这份切片必须是 make 出来的，不能是 nil。一个 nil 切片排出去是 `null`，
// 而输出契约说的是数组——空清单在那份 schema 下会当场验不过。
func (c *Controller) listJobs(
	ctx context.Context,
	_ json.RawMessage,
	exec *tools.RunContext,
) (json.RawMessage, error) {
	snapshots, err := c.service.List(ctx, c.callerOf(execAgent(exec)))
	if err != nil {
		return nil, err
	}
	public := make([]PublicSnapshot, 0, len(snapshots))
	for _, snapshot := range snapshots {
		public = append(public, publicJob(snapshot))
	}
	return json.Marshal(public)
}

// newKillTool 造那件 job_kill 工具。
//
// 源: packages/jobs/tool-jobs/src/index.ts:358-401
func (c *Controller) newKillTool() *tools.Definition {
	closed := false
	jobSchema := publicJobSchema()
	requested, _ := json.Marshal(outcomeRequested)
	finished, _ := json.Marshal(outcomeAlreadyFinished)
	return &tools.Definition{
		Name:        KillToolName,
		Description: killDescription,
		Parameters: tools.Node{
			Type: tools.TypeObject,
			Properties: []tools.Property{
				{Name: "job_id", Schema: tools.Node{Type: tools.TypeString, Description: jobIDDescription}},
				{Name: "reason", Schema: tools.Node{Type: tools.TypeString, Description: reasonDescription}},
			},
			Required: []string{"job_id"},
		},
		FinalizeContent: c.finalizeContent,
		Output: tools.OutputDefinition{
			Schema: tools.Node{
				Type: tools.TypeObject,
				Properties: []tools.Property{
					{
						Name: "outcome",
						Schema: tools.Node{
							Type: tools.TypeString,
							Enum: []json.RawMessage{requested, finished},
						},
					},
					{Name: "job", Schema: jobSchema},
				},
				Required:             []string{"outcome", "job"},
				AdditionalProperties: &closed,
			},
			Render: func(_ json.RawMessage, value json.RawMessage) (llm.Content, error) {
				var decoded killOutcome
				if err := json.Unmarshal(value, &decoded); err != nil {
					return nil, err
				}
				if decoded.Outcome == outcomeAlreadyFinished {
					return llm.Content{llm.TextBlock{
						Text: "job " + decoded.Job.ID + " had already finished " +
							StatusLine(jobs.JobStatus(decoded.Job.Status), decoded.Job.Detail),
					}}, nil
				}
				return llm.Content{llm.TextBlock{
					Text: "requested cancellation of job " + decoded.Job.ID,
				}}, nil
			},
		},
		Execute: c.killJob,
		PresentCall: func(args json.RawMessage) tools.CallView {
			var target jobTargetArgs
			_ = json.Unmarshal(args, &target)
			return presentTaskCall("Kill background job "+target.JobID, tools.CallExecute, target.JobID)
		},
	}
}

// killJob 是 job_kill 的体。
//
// 源: packages/jobs/tool-jobs/src/index.ts:386-397
func (c *Controller) killJob(
	ctx context.Context,
	args json.RawMessage,
	exec *tools.RunContext,
) (json.RawMessage, error) {
	var input killArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}
	id, err := validateJobID(input.JobID)
	if err != nil {
		return nil, err
	}
	caller := c.callerOf(execAgent(exec))
	result, err := c.service.Kill(ctx, id, caller, input.Reason)
	if err != nil {
		return nil, err
	}
	// 取快照而不是读：快照说的是当下的状态，它不会把还没交出去的输出消费掉。
	snapshot, err := c.service.Get(ctx, id, caller)
	if err != nil {
		return nil, err
	}
	outcome := outcomeRequested
	if result == jobs.KillAlreadyFinished {
		outcome = outcomeAlreadyFinished
	}
	return json.Marshal(killOutcome{Outcome: outcome, Job: publicJob(snapshot)})
}

// execAgent 取出这次执行落在的那把作用域钥匙，没落在 agent 上就是 nil。
func execAgent(exec *tools.RunContext) *scope.Key {
	if exec == nil {
		return nil
	}
	return exec.Agent
}

// Install 把这三件工具、那条投递规矩、那段指引和生产方要的控制器一起装上一个
// 作用域，交回把它们一起摘下来的函数。
//
// 源: packages/jobs/tool-jobs/src/index.ts:204-401
//
// 次序照 DSH 的 apply：预算补给线 → 派发前记账 → 挂控制器 → 指引 → 完成监听器
// → 三件工具。中途失败就按反序摘干净——半装上去意味着模型手上有一件读得了作业、
// 却收不到任何完成通知的工具。
func (c *Controller) Install(
	ctx context.Context,
	owner *scope.Scope,
	deps Deps,
) (func(context.Context) error, error) {
	switch {
	case deps.Tools == nil:
		return nil, fmt.Errorf("jobstool: 需要一个工具运行时")
	case deps.Prompts == nil:
		return nil, fmt.Errorf("jobstool: 需要一个系统提示词注册表")
	case c.delivery == DeliveryWakeup && deps.Agents == nil:
		// quiet 之下没有东西花掉预算，也就没有东西需要把它补回来。
		return nil, fmt.Errorf("jobstool: wakeup 投递需要一条从 agent 注册表来的收件箱认领通知")
	}

	// 摘挂在属主身上的那些清理排在最前面，于是它在反序里跑在**最后**：那时候
	// 完成监听器已经摘掉了，不会再有新的清理被挂上来。
	installed := []func(context.Context) error{c.releaseWakeCleanups}
	undo := func(undoCtx context.Context) error {
		failures := make([]error, 0, len(installed))
		for index := len(installed) - 1; index >= 0; index-- {
			failures = append(failures, installed[index](undoCtx))
		}
		installed = nil
		return errors.Join(failures...)
	}
	// 摘的时候不带调用方的取消：ctx 已经废了也得把装上去的收回来。
	fail := func(what string, err error) (func(context.Context) error, error) {
		_ = undo(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("jobstool: 装%s失败：%w", what, err)
	}

	if c.delivery == DeliveryWakeup {
		remove, err := deps.Agents.OnInboxClaimed(ctx, owner, c.refillWakeBudget)
		if err != nil {
			return fail("唤醒预算的补给线", err)
		}
		installed = append(installed, remove)
	}

	remove, err := deps.Tools.PreExecute(ctx, owner, c.captureOutputLimit)
	if err != nil {
		return fail("派发前的预算记账", err)
	}
	installed = append(installed, remove)

	// 生产方要有控制器挂着才开得了工。
	remove, err = c.service.AttachController(ctx, owner, PluginName)
	if err != nil {
		return fail("作业控制器", err)
	}
	installed = append(installed, remove)

	remove, err = deps.Prompts.Section(ctx, owner, systemprompt.PromptSection{
		Name:  SectionName,
		Order: SectionOrder,
		Text:  systemprompt.StaticText(promptText),
	})
	if err != nil {
		return fail("后台作业指引", err)
	}
	installed = append(installed, remove)

	remove, err = c.service.OnJobDone(ctx, owner, c.deliver)
	if err != nil {
		return fail("完成监听器", err)
	}
	installed = append(installed, remove)

	for _, definition := range []*tools.Definition{c.newOutputTool(), c.newListTool(), c.newKillTool()} {
		dispose, err := deps.Tools.Register(ctx, owner, definition)
		if err != nil {
			return fail(definition.Name, err)
		}
		installed = append(installed, dispose)
	}
	return undo, nil
}
