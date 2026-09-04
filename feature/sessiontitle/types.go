// 本文件的作用：标题这个域里的纯形状——那条落进日志的事件长什么样、一个标题
// 的归属怎么记、以及一个可选的标题生成器要满足什么合同。
//
// 这一层没有任何行为，也不碰会话。它单独成文的理由是：标题要跨进程传（客户端
// 列表行读的就是它），所以这些形状是**上线的**，改一个 JSON 键等于改已经写下去
// 的历史的读法。
//
// 源: packages/session/session-title/src/index.ts

package sessiontitle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/snight1983/ds-harness-go/sessionlog"
)

// EventSessionTitle 是这个包往会话日志里写的那条事件。
//
// 源: packages/session/session-title/src/index.ts:94-102
//
// 它是**只进日志**的：不上模型可见表面，也不进派生历史。模型不需要知道这次
// 对话在侧边栏里叫什么名字，把标题喂给它只会白花 token、还可能让它顺着标题跑偏。
const EventSessionTitle sessionlog.EventType = "session/title"

// EventTypes 是本包往会话日志里写的那几种事件类型。
//
// 新增: DSH 靠 `declare module` 把它合并进 SessionEventMap。Go 没有声明合并，
// [sessionlog.Vocabulary] 是个闭合的值，所以改成由本包交出这张单子，装配方自己拼
// （成例见 compaction.EventTypes）：
//
//	vocabulary := sessionlog.CoreVocabulary().With(sessiontitle.EventTypes()...)
//
// 不拼的话，一段带标题的日志会被 [sessionlog.CheckVocabulary] 判成
// 「有不认识的事件类型」而整个拒掉。
func EventTypes() []sessionlog.EventType {
	return []sessionlog.EventType{EventSessionTitle}
}

// ProviderID 标识一次标题生成器的登记。
//
// 源: packages/session/session-title/src/index.ts:28
//
// 新增: DSH 那边是 Branded<'SessionTitleProviderId'>，一个只在类型上存在的标签，
// 运行期就是普通字符串。Go 的具名字符串类型给的是同一件事，而且更硬——它在
// 运行期也是另一个类型。DSH 那个 SessionTitleProviderId() 加签函数随之消失：
// Go 里写 ProviderID("x") 就是转换。
type ProviderID string

// ModelProvenance 是产出一个标题的那条确切辅助模型路由。
//
// 源: packages/session/session-title/src/index.ts:40-45
//
// 记它是为了事后能答「这个名字是谁起的」：换了模型之后一批旧标题的风格突然
// 变了，靠它才分得清是模型换了还是提示词改了。
type ModelProvenance struct {
	// Provider 是登记过的那条 LLM 提供方路由。
	Provider string `json:"provider"`
	// Model 是那条路由上的模型 id。
	Model string `json:"model"`
}

// SourceKind 说的是一个标题是谁定的。
//
// 源: packages/session/session-title/src/index.ts:48-58
type SourceKind string

const (
	// SourceFallback 是那个确定性的兜底：直接截第一条人类消息的前几个词。
	SourceFallback SourceKind = "fallback"
	// SourceProvider 是某个登记过的生成器产出的。
	SourceProvider SourceKind = "provider"
	// SourceUser 是用户自己改的名字。
	//
	// 它**钉住**这个标题：从此自动生成不再排期，只有一次显式的
	// [Service.Refresh] 才解得开。
	SourceUser SourceKind = "user"
)

// Source 是一个被接受的标题的归属记录。
//
// 源: packages/session/session-title/src/index.ts:48-58
//
// 新增: DSH 那边是一个三支的可辨识联合。这里收成一个带 Kind 判别字段的结构体，
// 而不是按本仓库处理 TS 联合的一贯做法（封闭接口加变体，成例是
// [github.com/snight1983/ds-harness-go/llm.ContentBlock]）。理由是三支里有两支**一个字段都没有**，
// 只有 provider 那支带两个；为一个「三选一，其中两个是空壳」的值配一套类型
// 开关，每一处读 Kind 的地方都要先解一次包，而它们全都只想读那一个判别值。
//
// 两个可选字段带 omitempty，排出去的字节和 DSH 那三支逐字一致。
type Source struct {
	// Kind 是这个标题的来路。
	Kind SourceKind `json:"kind"`
	// Provider 是产出它的那个生成器，只在 Kind 是 [SourceProvider] 时有意义。
	Provider ProviderID `json:"provider,omitempty"`
	// Model 是产出它的那条模型路由；nil 表示这次生成没有过模型
	// （比如一个纯本地的、按规则拼出来的生成器）。
	Model *ModelProvenance `json:"model,omitempty"`
}

// Clone 交出一份不共享指针的拷贝。
//
// 源: packages/session/session-title/src/index.ts:140-153（copySessionTitleSource）
//
// DSH 那边的理由是 JS 对象按引用共享，一份从日志里读出来的来源交出去之后会被
// 收到它的人改掉。Go 这边结构体赋值就是复制，只有 Model 那个指针需要单独处理。
func (s Source) Clone() Source {
	if s.Model != nil {
		model := *s.Model
		s.Model = &model
	}
	return s
}

// EventData 是 [EventSessionTitle] 的负载。
//
// 源: packages/session/session-title/src/index.ts:61-68
type EventData struct {
	// Title 是归一化过的标题正文，非空。
	Title string `json:"title"`
	// MessageSeqs 是推出这个标题所用的那几条人类 user/message 的确切 seq。
	//
	// 它和 Source.Kind 之间有一条硬约束，见 [CheckEventData]：一次用户改名
	// 一条都不引，其余两种至少引一条。
	//
	// 新增: DSH 那边这个字段的类型是 number[]（可变数组），而同一份负载里的
	// source 是只读的。这里两者一视同仁，复制的责任落在 [FoldSnapshot] 上。
	MessageSeqs []int `json:"messageSeqs"`
	// Source 是这个标题的归属。
	Source Source `json:"source"`
}

// Snapshot 是折出来的最新标题，外加那条事件的信封事实。
//
// 源: packages/session/session-title/src/index.ts:71-76
type Snapshot struct {
	EventData
	// EventSeq 是最新那条 [EventSessionTitle] 的 seq。
	EventSeq int `json:"eventSeq"`
	// UpdatedAt 是最新那条 [EventSessionTitle] 的时间戳，Unix 纪元毫秒。
	UpdatedAt int64 `json:"updatedAt"`
}

// UserMessage 是暴露给标题生成器看的一条人类文本消息。
//
// 源: packages/session/session-title/src/index.ts:115-120
type UserMessage struct {
	// Seq 是它来自哪条 user/message 事件。
	Seq int `json:"seq"`
	// Text 是那条消息里全部文本块拼起来的原文，按块顺序、用换行连接。
	//
	// 注意它是**原文**，没有归一化过：归一化只用来判「这条消息有没有可用的
	// 文本」，交给生成器的必须是用户真正打的那些字，否则模型看到的是一段被
	// 折过空白的东西。
	Text string `json:"text"`
}

// AutomaticMode 是一个生成器自己拥有的自动排期节奏。
//
// 源: packages/session/session-title/src/index.ts:90-91（SessionTitleAutomaticMode）
type AutomaticMode string

const (
	// ModeFirstPrompt 只在会话的第一条人类消息上生成一次。
	ModeFirstPrompt AutomaticMode = "first-prompt"
	// ModeAllPrompts 每来一条人类消息都重新生成一次。
	ModeAllPrompts AutomaticMode = "all-prompts"
)

// ProviderRequest 是交给一次标题生成调用的那份不可变输入。
//
// 源: packages/session/session-title/src/index.ts:93-103（SessionTitleProviderRequest）
//
// 新增: DSH 那个 signal: AbortSignal 字段在这里不存在——按本仓库一贯的规矩，
// 取消走 [Provider.Generate] 的第一个 context.Context 参数。
type ProviderRequest struct {
	// Session 是正在被起名的那个活会话。
	Session Session
	// Messages 是到这次生成版本为止全部可用的人类消息。
	Messages []UserMessage
	// Route 是日志里当前那条主请求路由；nil 表示还没有记过任何一条。
	//
	// 生成器拿它来「跟着主对话用同一个模型」，或者反过来有意避开它。
	Route *ModelProvenance
}

// ProviderResult 是生成器交出来的、还没经过服务归一化和接受的产物。
//
// 源: packages/session/session-title/src/index.ts:105-113（SessionTitleProviderResult）
type ProviderResult struct {
	// Title 是提议的标题正文。
	Title string
	// MessageSeqs 是这个结果用到的、来自 ProviderRequest.Messages 的确切 seq。
	//
	// 服务会验它：必须非空、必须全部来自那份快照、而且必须按快照里的顺序
	// 严格递增。这条不是形式主义——它是事后追责的唯一凭据，一个乱填 seq 的
	// 生成器会让「这个名字是从哪句话来的」永远答不上来。
	MessageSeqs []int
	// Model 是这次生成用到的辅助模型路由；nil 表示没用模型。
	Model *ModelProvenance
}

// Provider 是登记进服务的那个唯一可选的异步标题实现。
//
// 源: packages/session/session-title/src/index.ts:115-127（SessionTitleProvider）
//
// 「唯一」是有意的：标题只有一个，两个生成器同时往里写只会互相盖掉。
type Provider interface {
	// ID 是记进标题里的那个稳定标识。
	ID() ProviderID
	// Automatic 说的是新的人类消息什么时候触发自动生成。
	Automatic() AutomaticMode
	// Generate 产出一次标题修订。
	//
	// ctx 会在被取代、生成器被注销、会话被销毁、服务被销毁、或者调用方自己
	// 取消时被取消。实现方必须把它一路传下去。
	Generate(ctx context.Context, request ProviderRequest) (ProviderResult, error)
}

// Config 是必填的兜底参数和标题长度上限。
//
// 源: packages/session/session-title/src/index.ts:54-62（Config）
type Config struct {
	// FallbackMaxWords 是兜底标题最多取几个空白分隔的词。
	FallbackMaxWords int
	// FallbackMaxBytes 是兜底标题最多占几个 UTF-8 字节。
	FallbackMaxBytes int
	// MaxTitleBytes 是**任何**被接受的标题最多占几个 UTF-8 字节。
	MaxTitleBytes int
	// IsLive 回答「我手上这个会话此刻还是它那个 id 名下活着的那一个吗」。
	//
	// 新增: DSH 那边这句话写成 `ctx.sessions.get(session.id) !== session`——
	// 拿 id 去会话仓库里查一次，再和手上这个对象比**引用**。Go 这边搬不动那个
	// 形状：Session 是接口，用 == 比两个接口值在动态类型不可比较时会当场 panic，
	// 而本包不该对实现方的具体类型提任何要求。
	//
	// 所以这件事被翻成一个谓词交给装配方：它手上有具体类型，比对怎么做它自己
	// 最清楚。本包只在两处问它——[Service.Rename] 和 [Service.Refresh] 的入口，
	// 以及每一次生成器结果落地之前——问的都是同一句话。
	//
	// 为 nil 表示装配方不提供活性检查，那样一律当成活的。这是一个真实的装配
	// （比如离线回放一份日志时根本没有会话仓库），所以它不是配置错误。
	IsLive func(Session) bool
	// Logger 是诊断日志；nil 取 [log/slog.Default]。
	//
	// 它只在一处用得上：自动生成（没有调用方接得住错误的那条路径）失败时把
	// 原因记下来。显式的 [Service.Rename] 和 [Service.Refresh] 一律返回错误，
	// 不写日志。
	Logger *slog.Logger
}

// ErrInvalidConfig 表示配置本身不成立，构造被拒。
var ErrInvalidConfig = errors.New("sessiontitle: 配置不成立")

// ErrInvalidTitle 表示一段用户输入的标题归一化之后什么都不剩。
//
// 源: packages/session/session-title/src/index.ts:80-88（SessionTitleInvalidError）
//
// 它是 [Service.Rename] 唯一一个「怪输入」的失败：把改名失败翻到线上去
// （`title-invalid`）的调用方靠 errors.Is 认它，而活性和销毁那些失败是普通错误。
//
// 新增: DSH 是一个 Error 子类加一个 name 字段。Go 里判别错误的惯用法是哨兵值加
// errors.Is，所以这里是一个哨兵，具体上下文由 fmt.Errorf 的 %w 裹在外面。
var ErrInvalidTitle = errors.New("sessiontitle: 标题里没有任何可见字符")

// Validate 检查配置是否成立。
//
// 源: packages/session/session-title/src/index.ts:279-289
//
// 三个上限必须是正整数——这正是 [TruncateTitleUtf8] 那边「≤0 表示不设上限」
// 这个约定的安全网所在：叶子函数放宽了，把关的地方就必须收紧，否则一份漏填的
// 配置会静悄悄地变成「标题不限长」，然后一整条用户消息被原样存进标题字段。
//
// 还有一条跨字段的约束：兜底的字节上限不能超过总的标题上限。违反它意味着
// 兜底能产出一个连自己都不接受的标题。
func (c Config) Validate() error {
	if c.FallbackMaxWords <= 0 {
		return fmt.Errorf("%w：FallbackMaxWords 必须是正整数，收到 %d", ErrInvalidConfig, c.FallbackMaxWords)
	}
	if c.FallbackMaxBytes <= 0 {
		return fmt.Errorf("%w：FallbackMaxBytes 必须是正整数，收到 %d", ErrInvalidConfig, c.FallbackMaxBytes)
	}
	if c.MaxTitleBytes <= 0 {
		return fmt.Errorf("%w：MaxTitleBytes 必须是正整数，收到 %d", ErrInvalidConfig, c.MaxTitleBytes)
	}
	if c.FallbackMaxBytes > c.MaxTitleBytes {
		return fmt.Errorf("%w：FallbackMaxBytes(%d) 不能超过 MaxTitleBytes(%d)",
			ErrInvalidConfig, c.FallbackMaxBytes, c.MaxTitleBytes)
	}
	return nil
}

// Session 是本包从一个活会话身上要用的全部东西。
//
// 新增: DSH 直接拿那个活着的 Session 对象。Go 里活会话是循环那一块的东西
// （见 docs/DESIGN.md 第八节），本包在第 6 块，所以这里只声明自己真正要用的
// 四件事（成例见 userapproval.Log 与 projection.SessionView）。
//
// **Append 会在服务持有自己那把锁的时候被调用**，所以它的实现不许反过来同步地
// 调回本服务的任何一个钩子（[Service.OnEvent] 那几个）——那会死锁。会话日志把
// 事件派发到监听者的那一步必须是异步的，或者至少不能回到这个包里来。
type Session interface {
	// ID 是这个会话的身份。
	ID() sessionlog.SessionID
	// Header 是这个会话不可变的存储元数据。
	//
	// 只用它一个字段：ParentSession——一个分叉出来的会话不走「第一条消息就
	// 起名」那条自动排期，因为它的第一条消息前面还压着一整段继承来的历史。
	Header() sessionlog.SessionHeader
	// Events 交出这条日志到目前为止的全部事件，按 seq 升序。
	//
	// 返回的切片只会被读，不会被本包改动或留存。
	Events() []sessionlog.Event
	// Append 往日志尾巴上追加一条事件。
	Append(kind sessionlog.EventType, data any) error
}

// CheckEventData 验一条标题事件负载上那条硬约束。
//
// 源: packages/session/session-title/src/invariant.ts:41-47（apply）
//
// 约束是：MessageSeqs 为空 **当且仅当** Source.Kind 是 [SourceUser]。一次自动
// 起名必须说得出自己读的是哪几句话，一次用户改名则一句都没读。
//
// 新增: DSH 那边这条是一个装进 invariants 服务的运行期不变量，靠拦截
// internal/dispatch 在事件发布**之前**把它拒掉。Go 这边它是一个普通的检查函数：
// 本包自己在每一次追加之前调它，装配方要是另有一套不变量框架，也拿得走它。
func CheckEventData(data EventData) error {
	if (len(data.MessageSeqs) == 0) != (data.Source.Kind == SourceUser) {
		requirement := "至少引一条"
		if data.Source.Kind == SourceUser {
			requirement = "一条都不许引"
		}
		return fmt.Errorf("sessiontitle: 来源是 %q 的标题事件%s消息 seq，实际引了 %d 条",
			data.Source.Kind, requirement, len(data.MessageSeqs))
	}
	return nil
}

// decodeEventData 把一条标题事件的负载读回来。
func decodeEventData(event sessionlog.Event) (EventData, error) {
	var data EventData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return EventData{}, fmt.Errorf("sessiontitle: 标题事件 %d 的负载读不回来：%w", event.Seq, err)
	}
	return data, nil
}
