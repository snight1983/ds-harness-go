// Package spill 是「把一份撑爆上下文的工具结果挪到别处存着」这件事的接缝：
// 收下全文，换回一个面向模型的不透明句柄，外加一句「怎么把它取回来」的说明。
//
// 源: packages/spill/spill/src/index.ts:1-15
//
// # 这条接缝只有一个动作
//
// [Store] 上只有 [Store.SaveText]，别的一律不在这里：
//
//   - **留多少、留多久**是保留策略的事，在 feature/outputretention。
//   - **什么时候该外置、外置之后原地放什么话**是外置策略的事，在 feature/spillpolicy。
//   - **怎么把存进去的东西读回来**根本没有接口——句柄和取回说明由后端自己给，
//     模型照着说明去调别的工具（读文件、查检索）取，而不是回头来问本包。
//
// 最后一条是这个接缝最容易被误读的地方，所以写明：本包**不是**一个键值存储。
// 它是单向的——写进去，拿到一个能给模型看的名字。加一个 readText 出来，
// 这条接缝就变成了第二套存储，而后端已经各自有自己的读取通道了。
//
// # 句柄不许解析
//
// [Locator] 是后端自己定的字符串。本机后端可能给一条文件路径，
// 对象存储后端可能给一个 URI 或者对象键，数据库后端可能给一行的主键。
// 消费方只把它连同 [Ref.RetrievalHint] 一起渲染给模型看，**不解析、不拼接、不推断**。
//
// # 抽象类怎么变成了接口
//
// 新增: DSH 那边是 `abstract class SpillStore extends Service`，一个抽象方法，
// 子类当插件装进去就占住 ctx.spillStore 这个槽。Go 没有那个容器，
// 对应物就是一个只有一个方法的 [Store] 接口，由装配方自己建、自己传。
//
// 这里没有像 attachment 包那样再拆出一批包级函数：那边拆是因为基类上带着
// 已经写好的次序规则（先整批校验再逐个写），继承会让子类能覆盖掉它。
// 本包的基类是空的——一个抽象方法，零个已实现方法——没有要护住的规则，
// 所以接口就是全部。
//
// # 关于 context
//
// 新增: DSH 的 saveText 不收 AbortSignal。Go 这边收 context.Context，
// 理由同 attachment 包：取消能力在 Go 里是传染的，接口方法上没有 ctx，
// 实现方内部再想把取消传给 HTTP 客户端或数据库驱动就没有来源。
// 外置的正是「大到装不下」的那种结果，一次传不完又停不下来的上传，
// 恰恰是这个接缝最容易出的问题。
package spill

import (
	"context"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// Locator 是一份外置产物面向模型的不透明句柄。
//
// 源: packages/spill/spill/src/types.ts:18
//
// DSH 那边是 Branded<'SpillLocator'>，配一个恒等构造函数把裸字符串标记上去。
// Go 的具名 string 类型天生是标称类型：编译期不能和别的字符串互换，
// 运行期零成本，构造就是语言自带的转换 spill.Locator(s)——
// 那个恒等构造函数在这里不需要存在。
type Locator string

// Owner 说明一份外置产物存到谁名下。
//
// 源: packages/spill/spill/src/types.ts:37
//
// 会话 id 让后端能按产出它的会话归拢存储，但**模型看见的句柄始终是 [Locator]**，
// 不是这个 id。分叉出来的会话直接继承种子日志里已有的句柄：那些产物既不复制、
// 也不改归属；分叉之后新产生的外置才用子会话的 id。
type Owner struct {
	// SessionID 是产出这份产物的会话。
	SessionID sessionlog.SessionID
}

// Source 记下一份外置产物是哪件工具、哪一次调用产出的。
//
// 源: packages/spill/spill/src/types.ts:46
//
// 它是**纯描述性**的：后端拿它拼一个人能读懂的文件名、排查时能看出这块字节
// 从哪来。它**不参与访问控制**——判断谁能读这份产物是后端自己的事，
// 把这三个字段当成权限凭据用会让任何能编出一个 callID 的调用方读到别人的东西。
type Source struct {
	// ToolName 是结果被外置的那件工具（比如 web_fetch）。
	ToolName string
	// CallID 是这份结果所属的、由模型发起的那次调用。
	CallID llm.CallID
	// Label 是给这份产物起的一句短标签（比如 result）。
	Label string
}

// SaveText 是一次「把这段文本存成一份外置产物」的请求。
//
// 源: packages/spill/spill/src/types.ts:56
type SaveText struct {
	// Owner 是这份产物存到谁名下。
	Owner Owner
	// Source 是产出它的工具与调用。
	Source Source
	// SuggestedName 是调用方**建议**的基础名（比如 web_fetch.txt）。
	//
	// 后端用它之前必须先净化成单个安全的路径片段：它是一条提示，**永远不是路径**。
	// 这条约束照抄 DSH，理由是这个字段一路上来自工具名，而工具名可以由插件自定，
	// 直接当路径用就是一个目录穿越。
	SuggestedName string
	// Content 是要完整存下来的那段文本（UTF-8）。
	Content string
}

// Ref 是一份已经存好的外置产物：句柄、字节数、以及怎么取回它。
//
// 源: packages/spill/spill/src/types.ts:69
type Ref struct {
	// Locator 是面向模型的不透明句柄。
	Locator Locator
	// Bytes 是存下来的**精确**字节数。
	//
	// 它是精确值而不是估计值：上游的保留策略拿它算「省下了多少上下文」，
	// 而一个估出来的数会让那个决策悄悄地偏。
	Bytes int
	// RetrievalHint 是给模型看的取回说明。
	//
	// 它由后端产出，因为只有后端知道自己这套介质该怎么读回去。
	// 这段文字**面向模型**，所以保持英文，和本仓库其余面向模型的载荷同一条界线。
	RetrievalHint string
}

// Store 是外置存储的接缝：收下全文，换回一个 [Ref]。
//
// 源: packages/spill/spill/src/index.ts:45-56
//
// 每个实现方都必须守住的语义：
//
//   - [Store.SaveText] **逐字**存下整段 Content，返回不透明句柄、精确字节数，
//     和面向模型的取回说明。截断、压缩、转码都不行——上层之所以敢把结果挪走，
//     依据就是「挪走的那份还在，一个字都没少」。
//   - 存储按请求里的 [Owner] 归到会话名下；后端自己挑一个**非公开可读**的位置，
//     和一个由 SuggestedName **派生而来、但绝不等于它**的、不会撞名的名字。
//   - 真出了存储故障（没权限、盘满、后端不可达）就**返回错误**，不要自己降级。
//     怎么退让由调用方定——外置策略把一次失败当成尽力而为，原样保留内联结果。
//     后端在这里自作主张返回一个假句柄，模型就会照着它去取一份不存在的东西。
type Store interface {
	// SaveText 把 input.Content 存成一份归在会话名下的外置产物。
	//
	// 存不成就返回错误，调用方据此决定怎么退让。
	SaveText(ctx context.Context, input SaveText) (Ref, error)
}
