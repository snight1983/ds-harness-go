// 本文件的作用：**跨进程**子 agent 后端那一侧的词汇——围着一个在别的进程里的孩子、
// 把这条接缝自己那些契约守住的那几件东西：那份「一样能力都没有」的申报、时限参数
// 的校验、那次绝不报错的结果结清，以及标准的运行句柄发布。
//
// 源: packages/subagent/subagent/src/out-of-process.ts
//
// 后端把这几样和它自己那套线协议驱动拼起来；进程那套机器本身（起进程、洗环境变量、
// 按进程树拆解）属于 subprocess 那条接缝。

package subagent

import (
	"context"
	"fmt"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// MaxDiagnosticBytes 是 [Result.Diagnostic] 的 UTF-8 字节上限。
//
// 源: packages/subagent/subagent/src/out-of-process.ts:20
const MaxDiagnosticBytes = 4096

// diagnosticTruncationSuffix 是被截断时接在后面、看得见的那个标记。
//
// 源: packages/subagent/subagent/src/out-of-process.ts:22
const diagnosticTruncationSuffix = "\n[diagnostic truncated]"

// limitDiagnostic 在不劈开一个 UTF-8 序列的前提下，压住提供方写的那段失败细节。
//
// 源: packages/subagent/subagent/src/out-of-process.ts:31-42
//
// 新增: DSH 得先 TextEncoder 编成字节、退回码点边界、再 TextDecoder 解回来。
// Go 的 string 本来就是 UTF-8 字节，切一刀就行，退边界用 [utf8.RuneStart]。
func limitDiagnostic(diagnostic string) string {
	if len(diagnostic) <= MaxDiagnosticBytes {
		return diagnostic
	}
	prefix := MaxDiagnosticBytes - len(diagnosticTruncationSuffix)
	for prefix > 0 && !utf8.RuneStart(diagnostic[prefix]) {
		prefix--
	}
	return diagnostic[:prefix] + diagnosticTruncationSuffix
}

// NoStartCapabilities 是一个跨进程后端的能力申报：一样都没有。
//
// 源: packages/subagent/subagent/src/out-of-process.ts:51-63（NO_START_CAPABILITIES）
//
// 一个在别的进程里的孩子兑现不了那些由父来执行的开工期特性（OutputSchema／
// MaxDepth／ToolFilter／Persona），所以服务在 Start 跑起来之前就把要这些东西的
// 请求拒掉——绝不「先收下再忽略」。
//
// 新增: DSH 是一个 Object.freeze 的常量。Go 的结构体没有 freeze，一个包级变量
// 反而是可以被外面改掉的，所以做成函数：每次交回一份新的零值，改它谁也影响不到。
func NoStartCapabilities() Capabilities { return Capabilities{} }

// AssertPositiveTimeout 拒掉一个配出来的非正时限。
//
// 源: packages/subagent/subagent/src/out-of-process.ts:65-76（assertPositiveFinite）
//
// 它约束的是一次拆解或者收摊的等待：零和负数会把那次等待直接跳过或者卡死。
//
// 新增: DSH 那个值是毫秒数的 number，所以要查一次 Number.isFinite。Go 这边它是
// [time.Duration]，NaN 和 Infinity 都不存在，只剩「必须为正」这一条要验。
func AssertPositiveTimeout(prefix, name string, value time.Duration) error {
	if value <= 0 {
		return fmt.Errorf("%s：%s 必须是一个正的时长，实际 %s", prefix, name, value)
	}
	return nil
}

// 新增: DSH 这里还有 isEnterableDirectory／assertUsableCwd／validateConfiguredCwd／
// resolveChildCwd 四件东西：验一个宿主机目录当不当得了孩子进程的 cwd，验不过就在
// 起进程之前失败。本仓库整簇删掉了——服务端没有可用硬盘（见
// [sessionlog.SessionHeader.WorkspaceID]），一个孩子进程的宿主工作目录不是这条接缝
// 答得出来的事。真要起本机进程的后端自己知道该在本机的哪里落脚，那是它和
// subprocess 那条接缝之间的事，不该由一个跑在无盘服务上的通用词汇表来规定。

// RunResultSettlement 是 [SettleRunResult] 的那几样输入。
//
// 源: packages/subagent/subagent/src/out-of-process.ts:164-180（RunResultSettlement）
//
// 新增: DSH 这个结构里还有 signal 和 onAbort 两项，为的是在结清时
// removeEventListener——一个挂在 AbortSignal 上的监听器不摘掉就是泄漏。Go 的取消
// 是 ctx，没有要摘的监听器，那两项连同 finally 里那一句一起消失。
type RunResultSettlement struct {
	// Attempt 是这次回合尝试（通常在和本地取消赛跑），交回终止结果。必填。
	Attempt func(ctx context.Context) (Result, error)
	// CollectOutput 是取消或者失败赢下结清时，提供方拿得出来的那份快照。必填。
	CollectOutput func() llm.Content
	// CollectDiagnostic 是失败赢下结清时，那段安全的、提供方自己写的细节；
	// nil 表示不给。
	CollectDiagnostic func() string
	// Cancelled 说的是在这次尝试的结局被观察到之前，本地取消有没有先结清。必填。
	Cancelled func() bool
	// OnError 是一个被摊平成停止原因的失败的诊断出口；它自己 panic 会被兜住。
	// nil 表示不报。
	OnError func(err error, stopReason StopReason)
}

// SettleRunResult 按接缝契约结清一次跨进程运行的结果：发布之后 result **绝不报错**。
//
// 源: packages/subagent/subagent/src/out-of-process.ts:183-210
//
// 一次正常跑完的、或者失败了的尝试，在本地取消已经结清的情况下都算成 StopAborted；
// 别的失败经那个被兜住的诊断出口摊平成 StopError。
//
// 新增: 交回的只有 [Result]，没有 error——「绝不报错」在 Go 里就是签名上没有那一路，
// 而不是 DSH 那种「一个绝不 reject 的 Promise」的口头约定。
func SettleRunResult(ctx context.Context, parts RunResultSettlement) Result {
	result, err := parts.Attempt(ctx)
	if err == nil {
		if parts.Cancelled() {
			return Result{Output: parts.CollectOutput(), StopReason: StopAborted}
		}
		return result
	}
	// 接住「取消到达时那次失败已经排在队里」的情形。
	if parts.Cancelled() {
		return Result{Output: parts.CollectOutput(), StopReason: StopAborted}
	}
	// 把发布之后的传输失败摊平，同时把诊断留住。
	if parts.OnError != nil {
		func() {
			// 那个诊断出口不许把这次运行结果搅黄。
			defer func() { _ = recover() }()
			parts.OnError(err, StopError)
		}()
	}
	settled := Result{Output: parts.CollectOutput(), StopReason: StopError}
	if parts.CollectDiagnostic != nil {
		settled.Diagnostic = limitDiagnostic(parts.CollectDiagnostic())
	}
	return settled
}

// SubprocessRunParts 是 [NewSubprocessRun] 的那几样输入。
//
// 源: packages/subagent/subagent/src/out-of-process.ts:221-235（SubprocessRunHandleParts）
type SubprocessRunParts struct {
	// ID 是父作用域里的那个运行 id。
	ID sessionlog.SessionID
	// Result 是那份已经摊平、绝不报错的结果（接缝契约）。必填。
	//
	// 新增: DSH 这里是一个已经在跑的 Promise 字段，反复 await 天然拿到同一份值。
	// Go 里它是一个函数，所以「反复调交回同一份结果」这条契约归后端兑现——
	// [sync.OnceValues] 就是那个现成的轮子。这里**有意**不代它记忆：
	// 记忆要吞掉第一次调用者那个 ctx 的取消，然后把它派给后面每一个调用者。
	Result func(ctx context.Context) (Result, error)
	// RequestCancel 结清本地取消，好让 Result 不等孩子配合也能定下来。必填。
	RequestCancel func()
	// Teardown 把孩子进程拆到静止（那把梯子归后端所有）。必填。
	Teardown func(ctx context.Context) error
}

// subprocessRun 是一个跨进程孩子的接缝运行句柄。
type subprocessRun struct {
	parts SubprocessRunParts
	// once 保证那把拆解梯子只走一遍；后到的处置方等它走完、拿同一个结局。
	once     sync.Once
	disposal error
}

// NewSubprocessRun 发布一个跨进程孩子的接缝运行句柄。
//
// 源: packages/subagent/subagent/src/out-of-process.ts:237-259（subprocessRunHandle）
//
// Dispose 是幂等的（一次记下来的拆解）：先结清本地取消——这里**不假设**孩子会
// 配合——再等后端那次拆解走到真正的退出。
//
// 新增: DSH 还要在 dispose 里 removeEventListener 摘掉那个 abort 监听器。Go 的取消
// 是 ctx，没有那件事。
func NewSubprocessRun(parts SubprocessRunParts) Run {
	return &subprocessRun{parts: parts}
}

// ID 是父作用域里的运行 id。
func (r *subprocessRun) ID() sessionlog.SessionID { return r.parts.ID }

// LocalAgent 对一个跨进程运行恒为 nil。
//
// 源: packages/subagent/subagent/src/out-of-process.ts:240
func (r *subprocessRun) LocalAgent() agent.Agent { return nil }

// Result 等这次运行结清。
func (r *subprocessRun) Result(ctx context.Context) (Result, error) {
	return r.parts.Result(ctx)
}

// Dispose 取消剩下的活、等孩子静下来。可以重复调，交回同一个结局。
func (r *subprocessRun) Dispose(ctx context.Context) error {
	r.once.Do(func() {
		r.parts.RequestCancel()
		r.disposal = r.parts.Teardown(ctx)
	})
	return r.disposal
}
