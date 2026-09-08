// 本文件的作用：这条缝上的词汇——一条流程提供哪几种方式、跑起来之后拿什么跟人说话、
// 一次尝试怎么算结束，以及界面那一侧要实现的两个方法。
//
// 源: packages/credentials/authorization/src/types.ts

package authorization

import (
	"context"
	"encoding/json"

	"github.com/snight1983/ds-harness-go/credentials"
)

// Method 是一条流程拿到凭据的一种方式，名字由这条流程自己起。
//
// 源: packages/credentials/authorization/src/types.ts:11-16（AuthorizationMethod）
type Method struct {
	// ID 是这条流程自己的方式标识；调用方挑中它时原样交回来。
	ID string
	// Label 是给人看的名字，供选择器渲染。
	Label string
}

// Notice 是一条正在跑的流程对着看它的人说的话，**永远不含密钥**。
//
// 源: packages/credentials/authorization/src/types.ts:18-26（AuthorizationNotice）
type Notice struct {
	// Message 是正在发生什么，或者人接下来该做什么。
	Message string
	// URL 是人得打开的那个页面；空串表示没有。
	//
	// 新增: DSH 是可选属性。Go 里用空串表达同一件事——一条空串的 URL 打不开，
	// 所以这个映射不丢信息。[Notice.Code] 同理。
	URL string
	// Code 是人得在那个页面上敲进去的短码；空串表示没有。
	Code string
}

// PromptKind 是一个问题该怎么呈现。
//
// 源: packages/credentials/authorization/src/types.ts:43-62（AuthorizationPrompt 的判别式）
type PromptKind string

const (
	// PromptText 是一行普通文本。
	PromptText PromptKind = "text"
	// PromptSecret 和 [PromptText] 只差在呈现上：界面把它打码，也不写进日志。
	PromptSecret PromptKind = "secret"
	// PromptSelect 是从 [Prompt.Options] 里挑一个，答案是那一项的 ID。
	PromptSelect PromptKind = "select"
)

// PromptOption 是一个 [PromptSelect] 问题里的一项。
//
// 源: packages/credentials/authorization/src/types.ts:29-36（AuthorizationPromptOption）
type PromptOption struct {
	// ID 是挑中这一项时交回的值。
	ID string
	// Label 是给人看的名字。
	Label string
	// Description 是渲染得下的界面才显示的补充说明。
	Description string
}

// Prompt 是一条流程自己答不了、必须问人的问题。
//
// 源: packages/credentials/authorization/src/types.ts:43-62（AuthorizationPrompt）
//
// 新增: DSH 那边是一个按 kind 判别的联合，三支的载荷不一样（select 带 options，
// 另两支带 placeholder）。Go 里判别联合的对应物是密封接口，但这三支的差别只有
// 一个字段有没有用上，塌成一个结构体读起来比三个类型加一次类型断言短得多；
// 合法组合由 [Prompt.Validate] 守着。
//
// DSH 那个 `signal?: AbortSignal` 没有对应物：它撤的是**这一问**而不是整次尝试
// （一次「敲短码 vs 浏览器回调」的竞速里，输的那一问要退场）。Go 里同一件事由
// 调用 [Session.Prompt] 时传进去的那个 ctx 表达，流程自己 WithCancel 就行，
// 不必在词表里给它留一个字段。
type Prompt struct {
	// Kind 是这个问题怎么呈现。
	Kind PromptKind
	// Message 是问题本身。
	Message string
	// Placeholder 是输入框里的灰字，只对 [PromptText] 和 [PromptSecret] 有意义。
	Placeholder string
	// Options 是可挑的那几项，只对 [PromptSelect] 有意义，而且不许是空的。
	Options []PromptOption
}

// Validate 验一个问题说不说得通。
func (p Prompt) Validate() error {
	if p.Message == "" {
		return newError(CodeInvalidConfig, "授权问题没有问题正文")
	}
	switch p.Kind {
	case PromptText, PromptSecret:
		if len(p.Options) != 0 {
			return newError(CodeInvalidConfig, "%q 问题带了选项", p.Kind)
		}
	case PromptSelect:
		if len(p.Options) == 0 {
			return newError(CodeInvalidConfig, "select 问题一个选项都没有")
		}
		for index, option := range p.Options {
			if option.ID == "" {
				return newError(CodeInvalidConfig, "select 问题第 %d 项没有 id", index)
			}
		}
	default:
		return newError(CodeInvalidConfig, "问题的呈现方式 %q 不在 text/secret/select 里", p.Kind)
	}
	return nil
}

// Status 是一次授权尝试**在它自己的调用方看来**怎么结束的。
//
// 源: packages/credentials/authorization/src/types.ts:65（AuthorizationStatus）
type Status string

const (
	// StatusAuthorized 表示凭据记录已经写下去、而且写完之后还在。
	StatusAuthorized Status = "authorized"
	// StatusCancelled 表示人拒绝了，或者调用方自己撤了。
	StatusCancelled Status = "cancelled"
)

// Settlement 是一次尝试**在旁观者看来**怎么结束的。
//
// 源: packages/credentials/authorization/src/types.ts:67-73（AuthorizationSettlement）
//
// 比 [Status] 多一个 failed：失败是以一个错误交回它自己的调用方的，所以只有在
// [Service.OnSettled] 这条旁观路上才需要一个名字——没起这次尝试的那一位，
// 没有别的办法把一次拒绝和一次故障分开。
type Settlement string

const (
	// SettlementAuthorized 同 [StatusAuthorized]。
	SettlementAuthorized Settlement = "authorized"
	// SettlementCancelled 同 [StatusCancelled]。
	SettlementCancelled Settlement = "cancelled"
	// SettlementFailed 是这次尝试坏了，它的调用方收到的是一个错误。
	SettlementFailed Settlement = "failed"
)

// Outcome 是一次 [Service.Begin] 或 [Service.Resume] 的结果。
//
// 源: packages/credentials/authorization/src/types.ts:75-79（AuthorizationOutcome）
type Outcome struct {
	// Status 是这次怎么结束的。
	Status Status
	// AttemptID 是这次尝试的身份。
	//
	// 新增: DSH 的结果里只有 status，因为它的尝试没有可以指认的身份——它活在
	// 一个 Promise 里。本包的尝试是介质上一条记录，调用方拿这个 id 去
	// [Service.Resume]。
	AttemptID AttemptID
}

// Entry 是一条已登记流程在界面上的样子：它授权什么，以及此刻忙不忙。
//
// 源: packages/credentials/authorization/src/types.ts:81-91（AuthorizationEntry）
type Entry struct {
	// Key 是这条流程写的那条凭据记录。
	Key credentials.Key
	// Label 是给人看的「在授权什么」。
	Label string
	// Methods 是这条流程提供的方式，最推荐的排在前面。
	Methods []Method
	// InFlight 表示此刻有一次尝试**正攥在某个副本手里**。
	InFlight bool
	// Stalled 表示介质上停着一次没结算的尝试，而没有副本攥着它。
	//
	// 新增: DSH 只有 inFlight 一个布尔，理由见 [CodeStalled]。界面上这两者
	// 要分开显示：忙是等一等，停是问人「接着上次还是重来」。
	Stalled bool
	// Attempt 是那次尝试的身份；两个布尔都为假时是空串。
	Attempt AttemptID
}

// Session 是一条跑起来的流程用来跟人说话、以及给自己留脚印的那一套。
//
// 源: packages/credentials/authorization/src/index.ts:86-108（AuthorizationSession）
//
// 每一件都限在这一次尝试上：流程既不知道也挑不了是哪个界面在听。
type Session interface {
	// Method 是调用方挑中的那个方式标识，一定是这条流程声明过的。
	//
	// 源: packages/credentials/authorization/src/index.ts:91-92
	Method() string

	// Resumed 是上一次 [Session.Checkpoint] 留下的脚印；这是头一回跑就是 nil。
	//
	// 新增: DSH 没有这个方法，也不可能有——它的流程是一个 async 闭包，进程没了
	// 闭包就没了，没有任何东西可以交回来。本包的流程被接手时是**从头再跑一遍**
	// [Flow.Run]，所以它需要一条路问「我上次走到哪儿了」，好直接跳到那一步。
	Resumed() json.RawMessage

	// Notify 报个进度，或者告诉人下一步该干什么。
	//
	// 源: packages/credentials/authorization/src/index.ts:94-100
	//
	// 发了就不管：渲染不出这条通告的界面（一个连接刚断的页面）丢的是这条通告，
	// 不是这次尝试。它不碰介质，所以也**不**顺带续租。
	Notify(notice Notice)

	// Prompt 问人一个流程自己答不了的问题。
	//
	// 源: packages/credentials/authorization/src/index.ts:101-107
	//
	// 返回人敲进来的文本，或者挑中那一项的 ID。人拒绝、这次尝试被撤、
	// 或者界面自己坏了，都以错误交回来。
	Prompt(ctx context.Context, prompt Prompt) (string, error)

	// Checkpoint 把这次尝试走到哪儿了记进介质。
	//
	// 新增: 本包独有，理由同 [Session.Resumed]。一次 OAuth 在把人支去浏览器
	// **之前**记下 state 和 verifier，接手的那个副本重跑 [Flow.Run] 时就能直接
	// 跳到轮询令牌那一步，而不是从头再要一次授权码。
	//
	// 它顺带续一次租约，也顺带把「这次尝试被撤了」读回来：撤了就返回错误。
	Checkpoint(ctx context.Context, progress json.RawMessage) error
}

// Flow 是一个插件关于「怎么拿到某一条凭据」的知识。
//
// 源: packages/credentials/authorization/src/index.ts:110-137（AuthorizationFlow）
//
// **写是流程自己做的**：[Flow.Run] 正常返回意味着 [Flow.Key] 那条记录已经在这次
// 尝试里经由凭据提供方提交过了，本包会去核实——尝试之内观察到过一次提交，尝试
// 之后它还在——核实过了才报成功。让流程自己写，是为了让一个用自己那套存储适配器
// 落盘的库仍然是唯一的写者，而不是把值誊回来再写第二遍。
//
// 新增: DSH 是一个带 run 方法的接口。Go 里是一个 Run 为函数字段的结构体，
// 写法与 [github.com/snight1983/ds-harness-go/feature/webhook.Rule] 一致：
// 登记方几乎总是就地写一个字面量，为它单开一个类型只是多一层。
type Flow struct {
	// Key 是这条流程写的那条凭据记录，它的 scope 就是拥有方插件名。
	Key credentials.Key
	// Label 是给人看的「在授权什么」。
	Label string
	// Methods 是提供的方式，最推荐的排在前面；调用方一个都不点名就用第一个。
	//
	// 新增: DSH 的类型是 `[Method, ...Method[]]`，也就是在类型上写死非空。
	// Go 的切片没有这个说法，所以同一条约束由 [Flow.Validate] 在登记那一刻查。
	Methods []Method
	// Run 跑一次尝试，把凭据拿到并提交下去。
	//
	// ctx 在这条登记被撤掉、这次尝试被 [Service.Cancel]、或者调用方自己撤了的
	// 时候取消。返回 nil 表示记录已经提交。
	Run func(ctx context.Context, session Session) error
}

// Validate 验一条流程的四个字段。
//
// 源: packages/credentials/authorization/src/index.ts:202-218（registerFlow 的入口检查）
func (f Flow) Validate() error {
	if f.Key == "" {
		return newError(CodeInvalidConfig, "授权流程没有凭据键")
	}
	if _, err := credentials.ParseKey(string(f.Key)); err != nil {
		return wrapError(CodeInvalidConfig, err, "授权流程的凭据键 %q 不合法", f.Key)
	}
	if f.Label == "" {
		return newError(CodeInvalidConfig, "凭据 %q 的授权流程没有名字", f.Key)
	}
	if len(f.Methods) == 0 {
		return newError(CodeInvalidConfig, "凭据 %q 的授权流程一种方式都没提供", f.Key)
	}
	seen := make(map[string]struct{}, len(f.Methods))
	for index, method := range f.Methods {
		if method.ID == "" {
			return newError(CodeInvalidConfig, "凭据 %q 的授权流程第 %d 种方式没有 id", f.Key, index)
		}
		if _, duplicate := seen[method.ID]; duplicate {
			return newError(CodeInvalidConfig, "凭据 %q 的授权流程有两种方式都叫 %q", f.Key, method.ID)
		}
		seen[method.ID] = struct{}{}
	}
	if f.Run == nil {
		return newError(CodeInvalidConfig, "凭据 %q 的授权流程没有 Run", f.Key)
	}
	return nil
}

// offers 说这条流程提不提供这个方式。
func (f Flow) offers(method string) bool {
	for _, candidate := range f.Methods {
		if candidate.ID == method {
			return true
		}
	}
	return false
}

// Interaction 是一次尝试的界面那一半。
//
// 源: packages/credentials/authorization/src/index.ts:139-159（AuthorizationInteraction）
//
// 跟着请求交进来而不是登记进来：起这次授权的那一位才是能跟人说上话的那一位——
// 问题因此正好落到问出它的那个页面上，而一个没人看的调用方交一个「一律拒绝」
// 的界面进来就行。
type Interaction interface {
	// Notify 渲染流程发来的一条通告。
	Notify(notice Notice)
	// Prompt 把问题摆到人面前，然后等。
	//
	// 人拒绝时返回一个 errors.Is(err, [CodeDeclined]) 认得的错（用 [Declined] 造）；
	// 别的错一律读成界面自己坏了，而不是一个答复。
	Prompt(ctx context.Context, prompt Prompt) (string, error)
}

// Request 是一次授权请求。
//
// 源: packages/credentials/authorization/src/index.ts:161-171（AuthorizationRequest）
//
// DSH 那个 `signal?: AbortSignal` 的对应物是 [Service.Begin] 的第一个 ctx。
type Request struct {
	// Key 是要授权的那条凭据记录，必须有一条流程认领它。
	Key credentials.Key
	// Method 挑这条流程的哪一种方式；空串用它的第一种。
	Method string
	// Interaction 是渲染这次尝试的通告和问题的那个界面。
	Interaction Interaction
}

// ResumeRequest 是一次「接着上次干」的请求。
//
// 新增: 本包独有。DSH 没有接手这回事，理由见 [CodeStalled]。
type ResumeRequest struct {
	// Key 是那条凭据记录。
	Key credentials.Key
	// AttemptID 是要接手的那次尝试；空串表示接手停在那儿的那一次，不管它是哪一次。
	//
	// 点名是为了挡住一次过期的接手：调用方手上那个 id 可能是几分钟前列出来的，
	// 而那次尝试早被别人接手、结算、又起了新的一次。空串留给
	// 「我就是要把这个键上停着的东西推完」那种运维口径。
	AttemptID AttemptID
	// Interaction 是这一次的界面，和上一次不必是同一个。
	Interaction Interaction
}
