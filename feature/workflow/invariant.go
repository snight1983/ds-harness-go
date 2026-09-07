// 本文件的作用：这个包自己拥有的那条运行期不变量——六条边必须围着一次运行凑成
// 一份说得通的账。
//
// 源: packages/workflow/workflow/src/invariant.ts

package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/snight1983/ds-harness-go/invariants"
)

// PackageName 是这个包在不变量注册表里占的名字。
//
// 源: packages/workflow/workflow/src/invariant.ts:12
//
// 沿用 DSH 侧的包名字面量而不是换成 Go 的导入路径，理由同 fs、credentials、llm、
// subagent：注册表是按名字预留的，而这条约定的拥有者在两边是同一个模块。
const PackageName = "@deepseek-ai/dsh-workflow"

// runTrace 是一次运行在这份检查眼里的样子。
//
// 源: packages/workflow/workflow/src/invariant.ts:19-23
type runTrace struct {
	// meta 是这次运行 start 那一刻那份 meta 排出来的字节，后面每一条边都要对上它。
	meta string
	// open 是发过 agent-start、还没发 agent-end 的那些调用，按 Seq。
	open map[int]AgentInfo
	// seen 是一辈子用过的序号；已经结清的序号也不许再次开始。
	seen map[int]struct{}
	// starts 是这次运行一共发过多少条 agent-start。
	starts int
}

// lifecycleInvariant 是这条接缝那份检查的状态。
//
// 新增: DSH 分两拍——先在 cordis 的 internal/dispatch 钩子里验、把对象记进一个
// WeakSet 暂存，再在真正的监听器里认那个暂存并提交状态。那两拍是它那套事件系统逼
// 出来的（验必须跑在所有监听器之前，而提交必须跑在这条边真的发出去之后）。Go 这边
// 这份检查有一个**专属**的调用点，就在发射器兜底循环的前面（见 [Lifecycle.check]），
// 验和提交在同一次调用里一前一后，所以那个 WeakSet 暂存没有对应物。语义一样：验不过
// 就 panic，那一行提交压根走不到。
type lifecycleInvariant struct {
	fail invariants.Fail

	// mutex 守住下面那张表。fail 一律在锁**外面**叫——它是 panic，在临界区里抛会把
	// 这把锁永远留在锁着的状态（和 [github.com/snight1983/ds-harness-go/llm.Runtime]
	// 那处同一条规矩）。
	mutex sync.Mutex
	runs  map[RunID]*runTrace
}

// metaBytes 把一份 meta 排成一串用来比对的字节。
//
// 排不出去时退回一个带类型的 %#v。走不到：[Meta] 每一个字段都是 JSON 排得出去的
// 基本类型或者它们的切片，而这里也不该为了一条比对失败就把整次运行打死。
func metaBytes(meta Meta) string {
	encoded, err := json.Marshal(meta)
	if err != nil {
		return fmt.Sprintf("%#v", meta)
	}
	return string(encoded)
}

// traceFor 取出一次运行的账本，顺带验「这条边的身份没有跟 start 那一刻分岔」。
//
// 源: packages/workflow/workflow/src/invariant.ts:26-37
//
// 第二个返回值是那句违例；非空时调用方要在锁外把它抛出去，并且**不许**再往下走——
// 账本是 nil。
func (i *lifecycleInvariant) traceFor(info RunInfo) (*runTrace, string) {
	trace, known := i.runs[info.ID]
	if !known {
		return nil, fmt.Sprintf("workflow event has no matching workflow/start for run %q", string(info.ID))
	}
	if trace.meta != metaBytes(info.Meta) {
		return nil, fmt.Sprintf("workflow event meta diverges from workflow/start for run %q", string(info.ID))
	}
	return trace, ""
}

// runStarted 验一条 `workflow/start`，验过之后给这次运行开一本账。
//
// 源: packages/workflow/workflow/src/invariant.ts:70-78
func (i *lifecycleInvariant) runStarted(info RunInfo) {
	i.mutex.Lock()
	var violation string
	switch {
	case info.ID == "" || info.Meta.Name == "" || info.Meta.Description == "":
		violation = "workflow/start id, meta.name, and meta.description must be non-empty"
	default:
		if _, repeated := i.runs[info.ID]; repeated {
			violation = fmt.Sprintf("workflow/start repeated run id %q", string(info.ID))
		}
	}
	if violation == "" {
		i.runs[info.ID] = &runTrace{
			meta: metaBytes(info.Meta), open: map[int]AgentInfo{}, seen: map[int]struct{}{},
		}
	}
	i.mutex.Unlock()
	if violation != "" {
		i.fail(violation)
	}
}

// observed 是 `workflow/phase` 和 `workflow/log` 那两条边的检查：除了「这次运行的
// 身份对得上」之外它们没有别的约束。
//
// 源: packages/workflow/workflow/src/invariant.ts:79-81
func (i *lifecycleInvariant) observed(info RunInfo) {
	i.mutex.Lock()
	_, violation := i.traceFor(info)
	i.mutex.Unlock()
	if violation != "" {
		i.fail(violation)
	}
}

// agentStarted 验一条 `workflow/agent-start`，验过之后把这次调用记进账。
//
// 源: packages/workflow/workflow/src/invariant.ts:82-90
func (i *lifecycleInvariant) agentStarted(info RunInfo, child AgentInfo) {
	i.mutex.Lock()
	trace, violation := i.traceFor(info)
	if violation == "" {
		switch {
		case child.Seq < 1 || child.ChildID == "":
			violation = "workflow/agent-start seq must be positive and childId must be non-empty"
		default:
			if _, repeated := trace.seen[child.Seq]; repeated {
				violation = fmt.Sprintf("workflow/agent-start repeated seq %d", child.Seq)
			}
		}
	}
	if violation == "" {
		trace.open[child.Seq] = child
		trace.seen[child.Seq] = struct{}{}
		trace.starts++
	}
	i.mutex.Unlock()
	if violation != "" {
		i.fail(violation)
	}
}

// agentEnded 验一条 `workflow/agent-end`：它必须配得上一条发过的 agent-start，
// 身份逐字一致，结局是三个已知值之一。
//
// 源: packages/workflow/workflow/src/invariant.ts:40-48、91-98
func (i *lifecycleInvariant) agentEnded(info RunInfo, child AgentEndInfo) {
	i.mutex.Lock()
	trace, violation := i.traceFor(info)
	if violation == "" {
		start, paired := trace.open[child.Seq]
		switch {
		case !paired:
			violation = fmt.Sprintf("workflow/agent-end has no matching start for seq %d", child.Seq)
		case start.Label != child.Label || start.Phase != child.Phase || start.ChildID != child.ChildID:
			violation = fmt.Sprintf(
				"workflow/agent-end identity diverges from workflow/agent-start for seq %d", child.Seq)
		default:
			switch child.Outcome {
			case AgentCompleted, AgentFailed, AgentCancelled:
				delete(trace.open, child.Seq)
			default:
				violation = fmt.Sprintf("workflow/agent-end carries unknown outcome %q", string(child.Outcome))
			}
		}
	}
	i.mutex.Unlock()
	if violation != "" {
		i.fail(violation)
	}
}

// runEnded 验一条 `workflow/end`，验过之后把这本账销掉。
//
// 源: packages/workflow/workflow/src/invariant.ts:51-59、99-103
func (i *lifecycleInvariant) runEnded(info RunInfo, result ResultInfo) {
	i.mutex.Lock()
	trace, violation := i.traceFor(info)
	if violation == "" {
		switch {
		case len(trace.open) > 0:
			violation = fmt.Sprintf(
				"workflow/end has %d agent call(s) without workflow/agent-end", len(trace.open))
		case result.AgentsStarted < trace.starts:
			violation = "workflow/end agentsStarted must cover every observed agent start"
		case (result.StopReason == StopCompleted) == (result.Error != ""):
			violation = "workflow/end error must be absent exactly for completed runs"
		default:
			delete(i.runs, info.ID)
		}
	}
	i.mutex.Unlock()
	if violation != "" {
		i.fail(violation)
	}
}

// RegisterInvariants 装上本包那条检查，返回注销函数。
//
// 源: packages/workflow/workflow/src/invariant.ts:135-136（apply）
//
// # 这条检查在查什么
//
// 一句话：**六条边必须围着一次运行凑成一份说得通的账**。拆开是五件事。
//
// 一是运行的身份。start 那条边的运行 id、meta.name、meta.description 都得非空，
// 一个运行 id 不重开；除 start 以外每一条边都得配得上一条发过的 start，而且它带的
// meta 要和 start 那一刻逐字一致。防的是「一次运行说着说着换了身份」——观察方按 id
// 聚合，中途改名会让一次运行在界面上裂成两次。
//
// 二是子 agent 调用的配对。agent-start 的序号从 1 起、不重号、孩子 id 非空；
// agent-end 必须配得上一条 start，两边的标签、阶段、孩子 id 逐字一致，结局是那三个
// 已知值之一。
//
// 三是收尾时不许有挂着的调用：`workflow/end` 发出来的时候，每一条 agent-start 都得
// 已经有它的 agent-end。这一条直接兑现 [Lifecycle.EmitAgentEnd] 上那句「任何停止
// 路径上都恰好发一次」——引擎在终止那条路上偷懒不补 [AgentCancelled]，这里就红。
//
// 四是计数说得通：end 那条边报的 AgentsStarted 不能小于这份检查真的看见过的
// agent-start 条数。它可以**大于**：脚本侧还排着队等并发槽的调用也算数，而那些调用
// 从来不发边。
//
// 五是失败描述的有无跟着停止原因走：完成的运行不许带 Error，没完成的必须带。
// 防的是一次静默的失败——一个 stopReason 是 error 却没有一句话的结局，上游没法解释。
//
// # 装在哪一层
//
// 这份检查有一个专属的调用点，跑在生命周期发射器那圈兜底观察者**之前**（见
// [Lifecycle.check]）。它必须在外面：一条被 [Lifecycle.contain] 吞成一行警告日志的
// 不变量检查等于没有检查。
//
// # 一个 Lifecycle 只该被装一次
//
// 注册表按包名预留，同一个注册表上装第二次会直接失败。用两个注册表装同一个
// [Lifecycle] 的话，后装的会盖掉先装的——那是一次装配错误，本包不为它兜底。
func RegisterInvariants(
	ctx context.Context,
	registry *invariants.Registry,
	lifecycle *Lifecycle,
) (func(), error) {
	if registry == nil {
		return nil, fmt.Errorf("workflow: 注册不变量需要一个不变量注册表")
	}
	if lifecycle == nil {
		return nil, fmt.Errorf("workflow: 注册不变量需要一个生命周期发射器")
	}

	install := func(_ context.Context, scope *invariants.Scope, fail invariants.Fail) error {
		lifecycle.check.Store(&lifecycleInvariant{fail: fail, runs: map[RunID]*runTrace{}})
		// 摘掉这一步登记进 scope：注销之后，一条不该再查的检查必须停下来，
		// 否则它会继续在别人的派发路径上抛。
		scope.Defer(func() { lifecycle.check.Store(nil) })
		return nil
	}

	return registry.Register(ctx, PackageName, install)
}
