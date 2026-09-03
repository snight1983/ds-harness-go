// 本文件的作用：两种「头」——会话在存储里的元数据头，和一次请求发出时的请求头
// 快照，以及从日志里把后者折回来的那个纯函数。
//
// 源: packages/core/session/src/types.ts:58-228
// 源: packages/core/session/src/request-header.ts

package session

import (
	"bytes"
	"encoding/json"

	"github.com/snight1983/ds-harness-go/llm"
)

// WorkspaceID 标识一条工作区登记。
//
// 新增: 它是一个**不透明标识**，也就是工作区登记册里那一行的 id，不是一条路径。
// 消费方只许比相等，不许解析、不许拼接、不许从中推断任何位置。
//
// 类型住在本包而不是 workspace 包，是因为 [SessionHeader] 要带着它，而 workspace
// 认识 session、session 不认识 workspace。workspace 那边留的是一个类型别名，
// 两处说的是同一个东西。
type WorkspaceID string

// Origin 是一个会话的粗粒度出身分类。
type Origin string

// OriginSubagent 表示这个会话是作为子 agent 的孩子建出来的。
//
// 源: packages/core/session/src/types.ts:85
//
// 它是**展示用**的元数据，不是「这个孩子还能接着跑」的证明。
// 空串表示没给。
const OriginSubagent Origin = "subagent"

// SessionHeader 是一个会话不可变的存储元数据，放在对话事件日志之外。
//
// 源: packages/core/session/src/types.ts:53-94（SessionHeader）
type SessionHeader struct {
	// Version 是落盘格式版本，建会话时从 [FormatVersion] 盖上去。
	//
	// 持久化后端在装载时见到别的版本一律拒收，不做迁移，见 [FormatVersion]。
	Version int `json:"version"`
	// ID 是这个会话的标识。
	ID SessionID `json:"id"`
	// CreatedAt 是建会话时的 Unix 纪元毫秒。
	//
	// 新增: 和 [Event.Time] 一样是 int64，DSH 那一套安全整数检查随之消失。
	CreatedAt int64 `json:"createdAt"`
	// ParentSession 是这个会话分叉自哪个会话（seed 血统）；空串表示不是分叉来的。
	ParentSession SessionID `json:"parentSession,omitempty"`
	// SeedLength 是开头有多少条事件是通过 seed 继承来的。
	//
	// 把这条边界持久化下来，恢复和回放才分得清哪些是父会话的历史、
	// 哪些是这个孩子自己干的活。
	//
	// 新增: 0 就是「没给」，两者本来同义——一个 seed 长度为零的会话就是没有 seed。
	SeedLength int `json:"seedLength,omitempty"`
	// Origin 是这个会话的出身分类；空串表示没给。
	Origin Origin `json:"origin,omitempty"`
	// DelegationDepth 是派发深度：顶层会话是 0，子 agent 是父的深度加一。
	//
	// 持久化它，递归预算才能熬过重启和恢复——一个只活在运行期的深度会让
	// 一个恢复出来的孩子退回顶层。
	//
	// 新增: DSH 的注释自己就写了「absent (zero)」，缺失与零同义，所以是 int 不是指针。
	DelegationDepth int `json:"delegationDepth,omitempty"`
	// AgentPreset 是这个会话的 agent 是从哪个预设组装出来的；空串表示没给。
	//
	// 预设决定这个会话的工具和提示词，所以它必须持久：一次恢复要是换了另一份组装，
	// 重放出来的历史模型已经没法照着做了。
	AgentPreset string `json:"agentPreset,omitempty"`
	// WorkspaceID 是这个会话属于哪一条工作区登记；空串表示不属于任何工作区。
	//
	// 新增: DSH 这一项叫 `cwd`，装的是一条**宿主机**绝对工作目录。本仓库的服务端
	// 没有可用硬盘，也就不存在「目录」这个概念——存储全在数据库和对象存储里，
	// 为的是这个服务能分布式部署。一条路径隐含「有那么一台机器、有那么一个文件系统
	// 命名空间」，把它钉在会话头上就等于把这条会话钉死在一台机器上：换一台读不出来，
	// 没法迁移，没法多租户。
	//
	// 所以记的不是位置而是**归属**：一个不透明的工作区标识。判一条会话属不属于某个
	// 工作区，是这个 id 和那条登记的 id 比一次相等，不碰文件系统。工作区自己那条
	// 给人看的路径由 workspace 包保管，且只在 [github.com/snight1983/ds-harness-go/fs.FileSystem]
	// 那条接缝的命名空间里有意义。
	WorkspaceID WorkspaceID `json:"workspaceId,omitempty"`
}

// TodoStatus 是一条待办的生命周期状态。
type TodoStatus string

const (
	// TodoPending 是还没开始。
	TodoPending TodoStatus = "pending"
	// TodoInProgress 是正在做；并行的活可以同时标好几条。
	TodoInProgress TodoStatus = "in_progress"
	// TodoCompleted 是做完了。
	TodoCompleted TodoStatus = "completed"
)

// TodoItem 是 agent 待办清单里的一条，也是 [EventTodoWrite] 那份整表快照的单位。
//
// 源: packages/core/session/src/types.ts:179-194
//
// 有意做得很小：一行给人看的 Content 加一个三态 Status。没有 id、没有优先级——
// 每次写都是整份替换（最后写的那份生效），所以条目不需要稳定身份。
type TodoItem struct {
	// Content 是这件事是什么，一行祈使句，直接显示给人看。
	Content string `json:"content"`
	// Status 是它的生命周期状态。
	Status TodoStatus `json:"status"`
}

// EpochHeader 是派生历史之外的请求状态：调用配置、系统提示、工具表。
//
// 源: packages/core/session/src/types.ts:174-188（EpochHeader）
//
// 日志里最新的那份完整 [EventRequestHeader] 快照就是它。
// 规范形式下空的可选字段是缺失的，见 [CanonicalHeader]。
type EpochHeader struct {
	// Config 是这次对话的调用配置。
	Config llm.CallConfig
	// AdapterDefaults 记的是生效配置里哪几个字段是适配器按确切模型解析出来的。
	//
	// 新增: 这里是值不是指针。DSH 那边这个对象整个可选，而两个字段的类型又是
	// 可选的字面量真——也就是说「对象不在」「对象在但两个键都不在」表达的是
	// 同一件事（没有任何字段来自适配器）。Go 的零值结构体正好就是那个意思，
	// 介质上的缺失由 [EpochHeader.MarshalJSON] 负责。
	AdapterDefaults llm.CallConfigAdapterDefaults
	// System 是渲染好的系统提示；空串表示这是一次没有系统提示的请求。
	System string
	// Tools 是装配好的工具 schema；nil 或空表示这是一次没有工具的请求。
	Tools []llm.ToolSchema
}

// epochHeaderWire 是请求头在介质上的样子。
type epochHeaderWire struct {
	Config          llm.CallConfig                 `json:"config"`
	AdapterDefaults *llm.CallConfigAdapterDefaults `json:"adapterDefaults,omitempty"`
	System          string                         `json:"system,omitempty"`
	Tools           []llm.ToolSchema               `json:"tools,omitempty"`
}

// MarshalJSON 把请求头**按规范形式**排出去。
//
// 源: packages/core/session/src/request-header.ts:21-31
//
// 新增: DSH 把这件事放在 canonicalHeader 里，靠调用方记得先规范化再写。
// Go 这边排字节的动作只有这一处，规范化直接长在它身上：一份非规范的头
// 在介质上根本不存在，不需要一条「记得先调 canonicalHeader」的纪律。
// [CanonicalHeader] 仍然在，供在内存里比较之前对齐用。
func (h EpochHeader) MarshalJSON() ([]byte, error) {
	wire := epochHeaderWire{Config: h.Config, System: h.System}
	if h.AdapterDefaults.ReasoningEffort || h.AdapterDefaults.MaxTokens {
		defaults := h.AdapterDefaults
		wire.AdapterDefaults = &defaults
	}
	if len(h.Tools) > 0 {
		wire.Tools = h.Tools
	}
	return json.Marshal(wire)
}

// UnmarshalJSON 把一段字节读回一份请求头。
func (h *EpochHeader) UnmarshalJSON(data []byte) error {
	var wire epochHeaderWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return wrapMalformed("请求头读不回来", err)
	}
	h.Config = wire.Config
	h.AdapterDefaults = llm.CallConfigAdapterDefaults{}
	if wire.AdapterDefaults != nil {
		h.AdapterDefaults = *wire.AdapterDefaults
	}
	h.System = wire.System
	h.Tools = wire.Tools
	return nil
}

// CanonicalHeader 把一份请求头归到规范形式：空的系统提示和空的工具表变成缺失，
// 两个标记都是假时适配器默认整个丢掉。
//
// 源: packages/core/session/src/request-header.ts:21-31
//
// 新增: 在 Go 里它比 DSH 那边薄得多——「空串即缺失」「空切片即缺失」在
// [EpochHeader.MarshalJSON] 上已经由 omitempty 落实了，这里只剩下把空的工具表
// 收成 nil，好让内存里的两份头能直接比较。留着这个名字是因为 DSH 的调用点
// （日志、折叠、比较）都念它，移植过去的代码读起来对得上。
func CanonicalHeader(header EpochHeader) EpochHeader {
	if len(header.Tools) == 0 {
		header.Tools = nil
	}
	if !header.AdapterDefaults.ReasoningEffort && !header.AdapterDefaults.MaxTokens {
		header.AdapterDefaults = llm.CallConfigAdapterDefaults{}
	}
	return header
}

// HeaderEquals 按字段比较两份规范形式的请求头，工具表按顺序比。
//
// 源: packages/core/session/src/request-header.ts:44-54
//
// 这是循环用来判断「这次请求的头和上次一样吗（一样就不必再写一份快照）」的比较。
//
// 新增: DSH 比工具 schema 用的是 JSON.stringify 逐字符比。这里比的是
// [llm.ToolSchema] 的三个字段，其中 Parameters 用 bytes.Equal——它本来就是
// 一段被原样保管的字节，比它比 JSON.stringify 更严格也更直接：后者会因为
// 两次装配时键序不同而误判不等，而这里字节相同就是相同。
func HeaderEquals(a, b EpochHeader) bool {
	if !llm.CallConfigEquals(a.Config, b.Config) ||
		a.AdapterDefaults != b.AdapterDefaults ||
		a.System != b.System ||
		len(a.Tools) != len(b.Tools) {
		return false
	}
	for index, tool := range a.Tools {
		other := b.Tools[index]
		if tool.Name != other.Name ||
			tool.Description != other.Description ||
			!bytes.Equal(tool.Parameters, other.Parameters) {
			return false
		}
	}
	return true
}

// FoldRequestHeader 把一段日志（或它的任意前缀）里的请求头事件折成「最后一份
// 快照之后生效的那份头」。非头事件跳过。
//
// 源: packages/core/session/src/request-header.ts:65-71
//
// 这是离线重建的那条纯函数路径；活着的会话增量地跟着同一个折叠。
// from 是上一次折出来的状态，从头折就传 [EpochHeader] 的零值加 false。
// 第二个返回值为假表示这段日志里一条头事件都没有。
func FoldRequestHeader(events []Event, from EpochHeader, hasFrom bool) (EpochHeader, bool, error) {
	state, ok := from, hasFrom
	for _, event := range events {
		if event.Type != EventRequestHeader {
			continue
		}
		var data RequestHeaderData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return EpochHeader{}, false, wrapMalformed("请求头事件的负载读不回来", err)
		}
		state, ok = CanonicalHeader(data.Header), true
	}
	return state, ok, nil
}

// RequestContext 是一条已解析模型路由的注册期元数据。
//
// 源: packages/core/session/src/types.ts:190-198（RequestContext）
//
// 它不参与请求重建，也不参与请求头的相等判断，只在路由或容量变了时记一条。
type RequestContext struct {
	// Provider 是这份元数据属于哪个已注册的提供方路由。
	Provider string `json:"provider"`
	// Model 是这份元数据属于哪个提供方模型标识。
	Model string `json:"model"`
	// ContextWindow 是请求加响应的上下文上限 token 数；0 表示对方没公布。
	ContextWindow int `json:"contextWindow,omitempty"`
}

// RequestHeaderReason 说明一份请求头快照为什么被追加。
//
// 源: packages/core/session/src/types.ts:200-208（RequestHeaderReason）
type RequestHeaderReason string

const (
	// HeaderInitial 是这段日志的第一份头——一次新对话。
	HeaderInitial RequestHeaderReason = "initial"
	// HeaderResume 是某个循环实例在一段已经有头事件的日志上发的第一次请求
	//（进程重启、分叉 seed）。
	HeaderResume RequestHeaderReason = "resume"
	// HeaderChange 是后面某次请求用了一份不一样的头。
	HeaderChange RequestHeaderReason = "change"
)
