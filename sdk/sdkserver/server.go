// 本文件的作用：这台服务器本身——它攥着什么、三个请求各自怎么办、以及收摊时按
// 什么次序把自己建出来的东西拆掉。
//
// 源: packages/sdk/server/src/server.ts

package sdkserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"golang.org/x/sync/singleflight"

	"ds-harness-go/core/agent"
	"ds-harness-go/core/scope"
	"ds-harness-go/llm"
	"ds-harness-go/sdk/sdkprotocol"
	sessionlog "ds-harness-go/session"
)

// Server 是架在一套跑着的运行时和一条通道之间的那台 SDK 服务端。
//
// 源: packages/sdk/server/src/server.ts:53-64
//
// [Install] 之后它开始收运行时那四条边并且往对面转发；`shutdown` 之后它把自己
// 建出来的 agent、兜底挂上的适配器、以及那四条订阅全部拆掉，**一次**——之后再来的
// 请求都被拒。重新 initialize 不支持（DSH 那句 "reinitialization is unsupported"，
// server.ts:50-51）。
type Server struct {
	config Config

	// mutex 罩着下面这一整块可变状态。
	//
	// 那条通道的请求处理器是并发的（[sdkprotocol.NewLineTransport] 用的是
	// jsonrpc2.AsyncHandler），所以两次 `session/prompt` 真的会同时进来。
	mutex sync.Mutex
	// owner 是 [Install] 交进来的那个作用域，这台服务器建出来的 agent 挂在它上面。
	owner *scope.Scope
	// notifyCtx 是发通知用的上下文，见 [Server.Install] 上那条说明。
	notifyCtx context.Context
	// initialized 记的是那次握手办过了没有。
	initialized bool
	// cwd / provider / model / maxTokens 是握手记下来的那份路由，这条线上每一个
	// 会话都照它建。
	cwd       string
	provider  string
	model     string
	maxTokens int
	// sessions 是这台服务器自己建出来的那些 agent，按 SDK 那侧的会话标识。
	sessions map[string]agent.Handle
	// unmount 撤销 [MountAdapter] 那次兜底挂载，没挂过时为 nil。
	unmount func(context.Context) error
	// disposers 是那四条订阅的撤销函数，按装上的次序排。
	disposers []func(context.Context) error
	// shuttingDown 一置起来就不再接新会话；pending 是还在半路上的那些创建。
	shuttingDown bool
	pending      sync.WaitGroup

	// creations 让同一个会话标识上并发的几次创建合成一次。
	//
	// 新增: DSH 手搓了一张 `sessionCreations: Map<string, Promise<SessionRecord>>`，
	// 建之前查、建完了删（server.ts:203-216）。Go 里
	// [golang.org/x/sync/singleflight.Group] 就是这件事，而且它连「合成的那次里
	// 只有第一个调用方的 ctx 算数」这条也和 DSH 一致——那边复用的也是第一次那个
	// promise。
	creations singleflight.Group

	// shutdownOnce / shutdownErr 让收摊只真的跑一次，之后每次都交回同一个结论。
	//
	// 源: packages/sdk/server/src/server.ts:150-153（`shutdownTask ??=`）
	shutdownOnce sync.Once
	shutdownErr  error
}

// Install 把这台服务器接到运行时上：挂那四条订阅，并交回收摊的那个函数。
//
// 源: packages/sdk/server/src/server.ts:70-103, packages/sdk/server/src/index.ts:91-97
//
// owner 是这台服务器建出来的 agent 和那四条订阅共同的主人。交一个
// [scope.NewRoot] 造的作用域进来，那四条订阅落全局层，看得见整套运行时里的每一个
// 会话——这正是 DSH 的行为（那条通知流"覆盖运行时里的每一个会话，不只是 SDK 建出来
// 的那些"）。
//
// 新增: ctx 被记下来当发通知用的上下文。运行时那几条边的观察者本身不带
// [context.Context]（会话追加和 agent 状态两条是同步回调），而
// [sdkprotocol.Peer.Notify] 要一个；DSH 那边监听器也一样跑在任何一次请求之外。
// 记下来的这一个管的是「这台服务器还在不在岗」，不是任何一次转发的时限。
//
// 交回的函数就是 [Server.Shutdown]：DSH 那个 effect 的处置器做的正是这件事
// （index.ts:93-96 先 `server.shutdown()` 再关通道；关通道是通道主人的事）。
func (s *Server) Install(ctx context.Context, owner *scope.Scope) (func(context.Context) error, error) {
	if owner == nil {
		return nil, fmt.Errorf("sdkserver: 装一台 SDK 服务器需要一个作用域")
	}
	s.mutex.Lock()
	if s.owner != nil {
		s.mutex.Unlock()
		return nil, fmt.Errorf("sdkserver: 这台 SDK 服务器已经装上了")
	}
	s.owner = owner
	s.notifyCtx = ctx
	s.mutex.Unlock()

	if err := s.subscribe(ctx, owner); err != nil {
		// 装到一半就失败：已经挂上的那几条当场摘掉，别留下一条会往一台永远不会
		// 开工的服务器上转发的边。
		_ = s.releaseSubscriptions(ctx)
		s.mutex.Lock()
		s.owner = nil
		s.notifyCtx = nil
		s.mutex.Unlock()
		return nil, err
	}
	return s.Shutdown, nil
}

// Initialize 记下这条线上共用的那份路由，并交回线上稳定的服务端身份。
//
// 源: packages/sdk/server/src/server.ts:111-125
//
// 新增: DSH 那句 `Number.isSafeInteger` 在 Go 里由类型系统承担——[int] 本来就是
// 整数，所以只剩「给了就必须为正」这一条。
//
// 新增: 重来一次是错误。DSH 只在文档里写了"reinitialization is unsupported"
// （server.ts:50-51）却没拦，于是第二次握手会再挂一次兜底适配器、把第一次那个纤程
// 的引用覆盖掉，从此再也拆不掉。这里把那句文档变成一次当场的拒绝。
func (s *Server) Initialize(ctx context.Context, params sdkprotocol.InitializeParams) (sdkprotocol.InitializeResult, error) {
	if params.MaxTokens != nil && *params.MaxTokens <= 0 {
		return sdkprotocol.InitializeResult{}, fmt.Errorf("sdkserver: initialize 的 maxTokens 必须是正整数，给的是 %d", *params.MaxTokens)
	}
	cwd, err := filepath.Abs(params.Cwd)
	if err != nil {
		return sdkprotocol.InitializeResult{}, fmt.Errorf("sdkserver: 解不出 cwd 的绝对路径：%w", err)
	}

	s.mutex.Lock()
	if s.owner == nil {
		s.mutex.Unlock()
		return sdkprotocol.InitializeResult{}, fmt.Errorf("sdkserver: 这台 SDK 服务器还没装上")
	}
	if s.initialized {
		s.mutex.Unlock()
		return sdkprotocol.InitializeResult{}, fmt.Errorf("sdkserver: 这条线已经握过手了，重来一次不支持")
	}
	s.initialized = true
	s.cwd = cwd
	s.provider = params.Provider
	s.model = params.Model
	if params.MaxTokens != nil {
		s.maxTokens = *params.MaxTokens
	}
	s.mutex.Unlock()

	if !s.hasAdapterFor(params.Provider) {
		if s.config.MountAdapter == nil {
			return sdkprotocol.InitializeResult{}, fmt.Errorf("sdkserver: 没有适配器认领提供方 %q", params.Provider)
		}
		unmount, err := s.config.MountAdapter(ctx, params.Provider)
		if err != nil {
			return sdkprotocol.InitializeResult{}, fmt.Errorf("sdkserver: 给提供方 %q 兜底挂适配器失败：%w", params.Provider, err)
		}
		s.mutex.Lock()
		s.unmount = unmount
		s.mutex.Unlock()
	}
	return sdkprotocol.InitializeResult{
		ServerInfo: sdkprotocol.ServerInfo{Name: sdkprotocol.ServerName, Version: ServerVersion},
	}, nil
}

// hasAdapterFor 问「这个提供方有没有适配器认领」；没挂 LLM 服务时一律当作没有。
//
// 源: packages/sdk/server/src/server.ts:237-239
func (s *Server) hasAdapterFor(provider string) bool {
	if s.config.Providers == nil {
		return false
	}
	for _, entry := range s.config.Providers.ListProviders() {
		if entry.ID == provider {
			return true
		}
	}
	return false
}

// Prompt 把一轮用户输入排进一个会话，交回那条消息的身份。
//
// 源: packages/sdk/server/src/server.ts:132-143
//
// 它**只**说明这条消息进了队列，不说明这一轮跑出了什么——之后的动静从
// `session.event` 那条通知流里看。
func (s *Server) Prompt(ctx context.Context, params sdkprotocol.SessionPromptParams) (sdkprotocol.SessionPromptResult, error) {
	handle, err := s.getOrCreateSession(ctx, params.SessionID)
	if err != nil {
		return sdkprotocol.SessionPromptResult{}, err
	}
	// 只重载 agent 循环的那种重启会把循环里的 agent 拆掉，而这条会话记录活了下来；
	// 一个已经被拆掉的 agent 收下 Followup 之后什么都不会发生，所以投递之前先拿
	// 注册表验一遍这条记录指的还是不是活着的那一个。
	if live, ok := s.config.Agents.Get(handle.Agent.ID()); !ok || live != handle.Agent {
		return sdkprotocol.SessionPromptResult{}, fmt.Errorf("sdkserver: 这个会话的 agent 在服务器之外被拆掉了：%s", params.SessionID)
	}
	message := llm.NewUserMessage(params.ContentBlocks, llm.UserSource{})
	handle.Agent.Followup(message)
	return sdkprotocol.SessionPromptResult{MessageID: message.ID}, nil
}

// getOrCreateSession 交出一个会话标识对应的那个 agent，没有就建一个。
//
// 源: packages/sdk/server/src/server.ts:203-216
func (s *Server) getOrCreateSession(ctx context.Context, sessionID string) (agent.Handle, error) {
	s.mutex.Lock()
	switch {
	case s.owner == nil:
		s.mutex.Unlock()
		return agent.Handle{}, fmt.Errorf("sdkserver: 这台 SDK 服务器还没装上")
	case s.shuttingDown:
		s.mutex.Unlock()
		return agent.Handle{}, fmt.Errorf("sdkserver: 这台 SDK 服务器正在收摊")
	}
	if handle, live := s.sessions[sessionID]; live {
		s.mutex.Unlock()
		return handle, nil
	}
	// 上膛和查那个开关必须在同一段临界区里：分开的话，一次创建可能挤在「查完了
	// 还没收摊」和「收摊开始等」之间，于是收摊等不到它。
	s.pending.Add(1)
	s.mutex.Unlock()
	defer s.pending.Done()

	created, err, _ := s.creations.Do(sessionID, func() (any, error) {
		return s.createSession(ctx, sessionID)
	})
	if err != nil {
		return agent.Handle{}, err
	}
	return created.(agent.Handle), nil
}

// createSession 按握手记下的那份路由建一个 agent 和它的会话。
//
// 源: packages/sdk/server/src/server.ts:218-235
//
// 不组预设：这台服务器的组合把面向模型的那几行留在宿主平面上，所以这个 agent 从
// 全局层读它们。要配名册的部署得先在这里接上一份（DSH agent-presets 的 README
// "Composing a child agent" 那一节）。
func (s *Server) createSession(ctx context.Context, sessionID string) (agent.Handle, error) {
	s.mutex.Lock()
	owner, options := s.owner, agent.Options{Provider: s.provider, Model: s.model, MaxTokens: s.maxTokens}
	cwd := s.cwd
	s.mutex.Unlock()

	handle, err := s.config.Agents.Create(ctx, owner, agent.CreateOptions{
		SessionID:    sessionlog.SessionID(sessionID),
		Cwd:          cwd,
		AgentOptions: options,
	})
	if err != nil {
		return agent.Handle{}, fmt.Errorf("sdkserver: 建会话 %s 失败：%w", sessionID, err)
	}
	s.mutex.Lock()
	shuttingDown := s.shuttingDown
	if !shuttingDown {
		s.sessions[sessionID] = handle
	}
	s.mutex.Unlock()
	if !shuttingDown {
		return handle, nil
	}
	// 收摊已经把那张表清了，这一个再记进去就没人拆得掉它——所以当场就地拆掉。
	// 拆解要发出去的边不能在锁里跑，上面那段临界区因此在这之前就已经出来了。
	if err := handle.Dispose(ctx); err != nil {
		return agent.Handle{}, fmt.Errorf("sdkserver: 收摊途中建出来的会话 %s 拆不掉：%w", sessionID, err)
	}
	return agent.Handle{}, fmt.Errorf("sdkserver: 这台 SDK 服务器正在收摊")
}

// Shutdown 把这台服务器建出来的东西全部收掉，**只真的跑一次**。
//
// 源: packages/sdk/server/src/server.ts:150-181
//
// 运行时照常活着：拆掉的只有这台服务器自己建的 agent、它兜底挂上的适配器、以及
// 它挂的那四条订阅。
func (s *Server) Shutdown(ctx context.Context) error {
	s.shutdownOnce.Do(func() { s.shutdownErr = s.performShutdown(ctx) })
	return s.shutdownErr
}

// performShutdown 是收摊真正做的那几步。
//
// 源: packages/sdk/server/src/server.ts:155-181
//
// 次序是定死的：先把门关上（不再接新会话），等半路上那些创建落地——不等的话，
// 一个刚建出来的 agent 会错过这次拆解，从此没人拆得掉它——再摘订阅、拆 agent、
// 卸兜底适配器。
//
// 新增: DSH 把 agent 和适配器的拆解一起丢给 Promise.allSettled 并发跑，失败聚成
// 一个 AggregateError。Go 这边顺着来一遍，失败用 [errors.Join] 攒着：拆解本来就是
// I/O 少、次序敏感的收尾，并发省不下什么，却让"谁先拆完"变成不确定的。
func (s *Server) performShutdown(ctx context.Context) error {
	s.mutex.Lock()
	s.shuttingDown = true
	s.mutex.Unlock()
	s.pending.Wait()

	failures := []error{s.releaseSubscriptions(ctx)}

	s.mutex.Lock()
	handles := make([]agent.Handle, 0, len(s.sessions))
	for _, handle := range s.sessions {
		handles = append(handles, handle)
	}
	clear(s.sessions)
	unmount := s.unmount
	s.unmount = nil
	s.mutex.Unlock()

	for _, handle := range handles {
		if err := handle.Dispose(ctx); err != nil {
			failures = append(failures, fmt.Errorf("sdkserver: 拆会话 %s 失败：%w", handle.Agent.ID(), err))
		}
	}
	if unmount != nil {
		if err := unmount(ctx); err != nil {
			failures = append(failures, fmt.Errorf("sdkserver: 卸兜底适配器失败：%w", err))
		}
	}
	return errors.Join(failures...)
}

// releaseSubscriptions 按装上的**反**序摘掉那四条订阅。
func (s *Server) releaseSubscriptions(ctx context.Context) error {
	s.mutex.Lock()
	disposers := s.disposers
	s.disposers = nil
	s.mutex.Unlock()

	var failures []error
	for index := len(disposers) - 1; index >= 0; index-- {
		if err := disposers[index](ctx); err != nil {
			failures = append(failures, fmt.Errorf("sdkserver: 摘订阅失败：%w", err))
		}
	}
	return errors.Join(failures...)
}

// Handlers 交出把这台服务器接到一条 [sdkprotocol.LineTransport] 上的那套处理器。
//
// 源: packages/sdk/server/src/index.ts:76-89
//
// 通知那一支是 nil：这条协议上 SDK 只发请求，服务端只发通知，没有反过来的那一半。
//
// 新增: DSH 那个闭包还夹了两件事，都不在本包：`initialize` 之前先等 Loader 把整棵
// 树装完（Go 没有 Loader），以及 `shutdown` 的响应写出去之后拆根上下文并退出进程
// （理由见包文档）。
func (s *Server) Handlers() sdkprotocol.Handlers {
	return sdkprotocol.Handlers{Request: s.HandleRequest}
}

// HandleRequest 把一次进来的请求派给对应的那个方法。
//
// 源: packages/sdk/server/src/server.ts:190-201
//
// 新增: 认不出的方法名交回 [sdkprotocol.ErrMethodNotFound]，于是线上是 -32601。
// DSH 这里抛的是一个普通 Error，落到线上是 -32603——那会让客户端把「你这个版本
// 没有这个方法」当成「你这边炸了」，然后去重试一件永远不会成立的事。它自己那条
// 通道对"根本没装处理器"的情形回的就是 -32601（transport.ts:59），这里跟它对齐。
func (s *Server) HandleRequest(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case sdkprotocol.MethodInitialize:
		var decoded sdkprotocol.InitializeParams
		if err := json.Unmarshal(params, &decoded); err != nil {
			return nil, fmt.Errorf("sdkserver: initialize 的入参解不动：%w", err)
		}
		return s.Initialize(ctx, decoded)
	case sdkprotocol.MethodSessionPrompt:
		var decoded sdkprotocol.SessionPromptParams
		if err := json.Unmarshal(params, &decoded); err != nil {
			return nil, fmt.Errorf("sdkserver: session/prompt 的入参解不动：%w", err)
		}
		return s.Prompt(ctx, decoded)
	case sdkprotocol.MethodShutdown:
		if err := s.Shutdown(ctx); err != nil {
			return nil, err
		}
		// 这条路的结果在协议上写死是空对象。
		return struct{}{}, nil
	default:
		return nil, fmt.Errorf("%w: %s", sdkprotocol.ErrMethodNotFound, method)
	}
}
