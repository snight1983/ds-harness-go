// 本文件的作用：**跨进程**子 agent 后端那一侧的词汇——围着一个在别的进程里的孩子、
// 把这条接缝自己那些契约守住的那几件东西：那份「一样能力都没有」的申报、时限参数
// 的校验、孩子工作目录的解算（配置覆盖，否则用发起派发的那个父会话的工作区）、
// 那次绝不报错的结果结清，以及标准的运行句柄发布。
//
// 源: packages/subagent/subagent/src/out-of-process.ts
//
// 后端把这几样和它自己那套线协议驱动拼起来；进程那套机器本身（起进程、洗环境变量、
// 按进程树拆解）属于 subprocess 那条接缝。

package subagent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"

	"ds-harness-go/core/agent"
	"ds-harness-go/llm"
	"ds-harness-go/session"
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
// 源: packages/subagent/subagent/src/out-of-process.ts:50-55
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
// 源: packages/subagent/subagent/src/out-of-process.ts:64-68
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

// isEnterableDirectory 判断一个路径是不是一个宿主**进得去**的现存目录。
//
// 源: packages/subagent/subagent/src/out-of-process.ts:75-86
//
// 那道「进得去」的探测很要紧：一个 mode-600 的目录 IsDir() 照样为真，而一个子进程
// 的 cwd 要的是执行（搜索）权限，否则起进程直接 EACCES。
//
// 新增: DSH 用 accessSync(path, X_OK) 探这一下。Go 的标准库没有 access(2)，
// golang.org/x/sys/unix 有、但只在 Unix 上有，而本仓库还要交叉编到 Windows。
// 这里改探 `path/.`：解析路径里的 `.` 这一段本身就要求对 path 有搜索权限，
// 所以一个 mode-600 的目录在这一步就会 EACCES。[os.Stat] 不 Clean 路径、
// 原样交给系统调用，这个技巧才成立——所以这里**有意**不用 [filepath.Join]，
// 它会把那个 `.` 消掉，探测也就退回成一次普通的 stat。
func isEnterableDirectory(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	// stat/access 在这里只会报文件系统访问类的错（ENOENT/EACCES/ENOTDIR/…），
	// 而它们每一个都意味着这个路径当不了孩子的 cwd。
	_, err = os.Stat(path + string(filepath.Separator) + ".")
	return err == nil
}

// AssertUsableCwd 判定一个 cwd 真的托得住这个孩子：是绝对路径（它同时是孩子的
// 工作区身份，一个相对路径会被重新锚到服务进程自己的启动目录上），而且是一个
// 现存的目录（在进程边界**之前**就失败，而不是变成一次含糊的 spawn ENOENT）。
//
// 源: packages/subagent/subagent/src/out-of-process.ts:98-106
func AssertUsableCwd(prefix, label, cwd string) error {
	if !filepath.IsAbs(cwd) {
		return fmt.Errorf("%s：%s 必须是绝对路径：%s", prefix, label, cwd)
	}
	if !isEnterableDirectory(cwd) {
		return fmt.Errorf("%s：%s 不是一个进得去的目录：%s", prefix, label, cwd)
	}
	return nil
}

// ValidateConfiguredCwd 在插件装载时把配置里那个 cwd 覆盖**验一次**：相对路径按
// 宿主的启动目录解释，而且必须是一个进得去的目录。空串表示配置里没给这一项，
// 那就交回空串。
//
// 源: packages/subagent/subagent/src/out-of-process.ts:118-124
//
// 新增: DSH 要把「没给」（undefined）和「给了个空串」分开，并且对空串**报错**——
// 因为 path.resolve 收到空串时会悄悄给出进程的 cwd，正好把这次解算要铲掉的那条
// 「退回启动目录」的兜底又请回来。Go 这边空串就是本仓库通行的「没给」，
// [filepath.Abs] 那一步压根走不到，那条错误也就没有对应物。
func ValidateConfiguredCwd(prefix, cwd string) (string, error) {
	if cwd == "" {
		return "", nil
	}
	absolute, err := filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("%s：配置里的 cwd 解不成绝对路径：%w", prefix, err)
	}
	if err := AssertUsableCwd(prefix, "配置里的 cwd", absolute); err != nil {
		return "", err
	}
	return absolute, nil
}

// ResolveChildCwd 在开工那一刻解算孩子的工作目录：配了覆盖就用它（装载时已经验过），
// 否则用父会话那个工作区 cwd（在这里验，那是它最早解得出来的地方）。
//
// 源: packages/subagent/subagent/src/out-of-process.ts:139-145
//
// 两样都没有时大声失败：退回宿主进程的 cwd 会把孩子悄悄绑在服务的启动目录上，
// 而不是发起派发的那个会话的工作区——一个服务进程服务很多会话，每个都有自己的 cwd。
func ResolveChildCwd(prefix, configured, parentCwd string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	if parentCwd == "" {
		return "", fmt.Errorf("%s：这个孩子没有工作目录——配一个 cwd，或者从一个有 cwd 的父会话派发", prefix)
	}
	if err := AssertUsableCwd(prefix, "父会话的 cwd", parentCwd); err != nil {
		return "", err
	}
	return parentCwd, nil
}

// RunResultSettlement 是 [SettleRunResult] 的那几样输入。
//
// 源: packages/subagent/subagent/src/out-of-process.ts:157-172
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
// 源: packages/subagent/subagent/src/out-of-process.ts:213-226
type SubprocessRunParts struct {
	// ID 是父作用域里的那个运行 id。
	ID session.SessionID
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
// 源: packages/subagent/subagent/src/out-of-process.ts:236-250
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
func (r *subprocessRun) ID() session.SessionID { return r.parts.ID }

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
