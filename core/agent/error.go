// 本文件的作用：本包会报的那几种错误。
//
// 新增: DSH 侧全部是 `throw new Error(字符串)`，调用方分不出种类，只能看消息。
// Go 里错误是要被 errors.Is 分派的，所以这里按「读到之后该做什么」分类，
// 而不是按「哪个函数报的」分类。

package agent

import "errors"

var (
	// ErrMalformedEvent 表示一条 agent/inbox/spliced 事件的负载读不回来，
	// 或者排不出去。
	//
	// 该做的事：这份日志坏了或者写它的一方有缺陷，投影重建不出来。
	//
	// 源: packages/core/agent/src/inbox.ts:37（`invalid persisted inbox splice at ...`）
	ErrMalformedEvent = errors.New("agent: 收件箱事件的负载读不回来")

	// ErrInvalidSplice 表示一次收件箱改动的坐标落在清单外面，或者它会让两条
	// 消息带上同一个身份。
	//
	// 该做的事：调用方算错了下标，或者这段日志的收件箱记账自相矛盾。
	//
	// 源: packages/core/agent/src/inbox.ts:209、216
	ErrInvalidSplice = errors.New("agent: 收件箱改动不合法")

	// ErrNoFactory 表示还没有循环实现把自己的造法登记进来，就有人要造 agent 了。
	//
	// 该做的事：装配漏了——把循环那一层接上去。
	//
	// 源: packages/core/agent/src/index.ts:217（NO_FACTORY_MESSAGE）
	ErrNoFactory = errors.New("agent: 还没有登记过 agent 造法")

	// ErrFactoryAlreadySet 表示已经有一个造法在位了。
	//
	// 该做的事：装配重了——一个注册表只该有一个造法。
	//
	// 源: packages/core/agent/src/index.ts:388（`an agent factory is already registered`）
	ErrFactoryAlreadySet = errors.New("agent: 已经登记过一个 agent 造法")

	// ErrNoInitiator 表示当下这条调用链没有发起者，而调用方要求必须有。
	//
	// 该做的事：这条路契约上就在某个驱动之下，走到这里说明它被从别处直接调了。
	//
	// 源: packages/core/agent/src/index.ts:218（NO_INITIATOR_MESSAGE）
	ErrNoInitiator = errors.New("agent: 当下没有发起的 agent")

	// ErrAgentAlreadyExists 表示这个身份上已经有一个活 agent 了。
	//
	// 源: packages/core/agent/src/index.ts:487（`agent "..." is already registered`）
	ErrAgentAlreadyExists = errors.New("agent: 这个身份上已经有一个活 agent")

	// ErrAgentNotLive 表示交进来的这个 agent 不是这张表上那一份活的登记。
	//
	// 源: packages/core/agent/src/index.ts:548（`agent "..." is not live in this registry`）
	ErrAgentNotLive = errors.New("agent: 这不是本注册表里那一份活登记")

	// ErrAlreadyAnnounced 表示这份登记的创建公布已经开始过了。
	//
	// 源: packages/core/agent/src/index.ts:551（`agent "..." was already announced`）
	ErrAlreadyAnnounced = errors.New("agent: 这份登记已经公布过")

	// ErrIdentityMismatch 表示 agent 的身份和它那个会话的身份对不上。
	//
	// 该做的事：造它的那一方有缺陷——两个身份从来就该是同一个。
	//
	// 源: packages/core/agent/src/index.ts:481-483
	ErrIdentityMismatch = errors.New("agent: agent 身份和会话身份对不上")

	// ErrInvalidRegistration 表示一次观察者登记本身不合法（观察者是 nil，
	// 或者没给载体作用域）。
	ErrInvalidRegistration = errors.New("agent: 观察者登记不合法")

	// ErrStatusNoop 表示同一个状态被连着报了两次。
	//
	// 源: packages/core/agent/src/invariant.ts:20（`agent/status repeated ... (no-op transition)`）
	//
	// 新增: DSH 把这条交给 invariants 服务，在开发构建里 fail() 掉。本仓库没有那个
	// 服务（见包文档），改成 [Registry.Announce] 之外的一道自检：[Registry.Status]
	// 报状态时自己核对，撞上就交出这条错误。
	ErrStatusNoop = errors.New("agent: 同一个状态被连报了两次")
)
