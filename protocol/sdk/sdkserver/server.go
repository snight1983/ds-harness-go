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
	"sync"

	"golang.org/x/sync/singleflight"

	"github.com/snight1983/ds-harness-go/attachment"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/protocol/sdk/sdkprotocol"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// Server 是架在一套跑着的运行时和一条通道之间的那台 SDK 服务端。
//
// 源: packages/sdk/server/src/server.ts:70-297（HarnessSdkJsonRpcServer）
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
	// initialized 记的是那次握手**办成**了没有。
	//
	// 它只在握手全部走完之后才置起来，见 [Server.Initialize]。
	initialized bool
	// initializing 记的是此刻有没有一次握手在半路上。
	//
	// 新增: DSH 那边不需要它——一个 JS 的事件循环里 initialize 从头到尾没有别人插进
	// 来的机会。Go 这边那条通道的处理器是并发的（[sdkprotocol.NewLineTransport] 用
	// 的是 jsonrpc2.AsyncHandler），两次 `initialize` 真的会同时进来；只靠
	// initialized 挡不住它们——它要到最后才置起来，于是两边都会去挂一次兜底适配器，
	// 后挂上的那个把前一个的撤销函数覆盖掉，从此拆不掉。
	initializing bool
	// workspaceID / provider / model / reasoningEffort / maxTokens 是握手记下来的
	// 那份路由，这条线上每一个会话都照它建。
	workspaceID     sessionlog.WorkspaceID
	provider        string
	model           string
	reasoningEffort llm.ReasoningEffortID
	maxTokens       int
	// sessions 是这台服务器自己建出来的那些 agent，按 SDK 那侧的会话标识。
	sessions map[string]agent.Handle
	// unmounts 撤销 [MountAdapter] 那些兜底挂载，按挂上的次序排。
	//
	// 新增: DSH 是单独一个 `llmFiber`，因为它那条路只走得到一次。这里是一串：
	// 一次握手可以在挂完适配器之后才失败（那条路由解不开），而失败之后客户端换一份
	// 参数重来是允许的，于是同一条线上先后挂过两个适配器这件事是可能的。用单个槽位
	// 存的话，后挂上的会把前一个的撤销函数盖掉，那个适配器从此拆不掉。
	unmounts []func(context.Context) error
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

// Initialize 校验并记下这条线上共用的那份路由，并交回线上稳定的服务端身份。
//
// 源: packages/sdk/server/src/server.ts:130-169
//
// 次序是这个方法的**全部内容**，DSH 那边一模一样：先把入参验完，再挂或者确认适配器，
// 再把那条路由真解算一遍，**最后**才公布。任何一步失败，这台服务器都还停在握手之前——
// 于是客户端换一份参数重来就是了。反过来（先公布再挂适配器）会留下一台自称办好了、
// 而路由其实还没落地的服务器：那期间进来的 `session/prompt` 会拿一条空路由把
// agent 建出来，一步都跑不动。
//
// 新增: DSH 那句 `Number.isSafeInteger` 在 Go 里由类型系统承担——[int] 本来就是
// 整数，所以只剩「给了就必须为正」这一条。
//
// 新增: 重来一次是错误。DSH 只在文档里写了"reinitialization is unsupported"
// （server.ts:73）却没拦，于是第二次握手会再挂一次兜底适配器、把第一次那个纤程
// 的引用覆盖掉，从此再也拆不掉。这里把那句文档变成一次当场的拒绝。
func (s *Server) Initialize(ctx context.Context, params sdkprotocol.InitializeParams) (sdkprotocol.InitializeResult, error) {
	// 入参校验先做完，一次锁都不用碰：坏参数根本不该占住那扇门。
	if params.ReasoningEffort != nil && *params.ReasoningEffort == "" {
		return sdkprotocol.InitializeResult{}, fmt.Errorf("sdkserver: initialize 的 reasoningEffort 给了就不能是空串")
	}
	if params.MaxTokens != nil && *params.MaxTokens <= 0 {
		return sdkprotocol.InitializeResult{}, fmt.Errorf("sdkserver: initialize 的 maxTokens 必须是正整数，给的是 %d", *params.MaxTokens)
	}
	// 握手那条 cwd 到这里就换成一个工作区标识，一个字都不往下传，见 [WorkspaceLookup]。
	//
	// 新增: 原来这里还先验一遍它绝不绝对（更早还是一次 [path/filepath.Abs]，那会把
	// 相对路径锚到服务进程自己的启动目录上）。两道都没有了：cwd 长什么样是**客户端**
	// 那台机器上的事，服务端连它指着什么都不知道，更没有立场规定它的形状。
	workspaceID, err := s.workspaceOf(ctx, params.Cwd)
	if err != nil {
		return sdkprotocol.InitializeResult{}, err
	}
	var effort llm.ReasoningEffortID
	if params.ReasoningEffort != nil {
		effort = *params.ReasoningEffort
	}
	maxTokens := 0
	if params.MaxTokens != nil {
		maxTokens = *params.MaxTokens
	}

	if err := s.beginInitialize(); err != nil {
		return sdkprotocol.InitializeResult{}, err
	}
	// 失败就把那扇门还回去，让客户端换一份参数重来。办成了由 publishRoute 关上它。
	settled := false
	defer func() {
		if !settled {
			s.mutex.Lock()
			s.initializing = false
			s.mutex.Unlock()
		}
	}()

	if err := s.ensureAdapterFor(ctx, params.Provider); err != nil {
		return sdkprotocol.InitializeResult{}, err
	}
	// 这条路由必须真解算得开——上面那次适配器确认只说明「这个提供方有人认领」，
	// 说不出这个确切模型收不收这个推理档位。不在这里解，第一次 `session/prompt`
	// 才会撞上它，而那时错误已经落在一个会话的历史里了。
	//
	// 源: packages/sdk/server/src/server.ts:154-161
	if s.config.LLM == nil {
		return sdkprotocol.InitializeResult{}, fmt.Errorf(
			"sdkserver: 这条线上没挂 LLM 服务，解不出提供方 %q 的调用配置", params.Provider)
	}
	if _, err := s.config.LLM.ResolveCallConfig(ctx, llm.CallConfig{
		Provider:        params.Provider,
		Model:           params.Model,
		ReasoningEffort: effort,
		MaxTokens:       maxTokens,
	}); err != nil {
		return sdkprotocol.InitializeResult{}, fmt.Errorf(
			"sdkserver: 解不开提供方 %q 模型 %q 的调用配置：%w", params.Provider, params.Model, err)
	}

	s.publishRoute(workspaceID, params.Provider, params.Model, effort, maxTokens)
	settled = true
	return sdkprotocol.InitializeResult{
		ServerInfo: sdkprotocol.ServerInfo{Name: sdkprotocol.ServerName, Version: ServerVersion},
	}, nil
}

// beginInitialize 认领那扇只进得去一次的门。
//
// 已经握过手、或者有一次握手正在半路上，都当场拒；两者的说法分开，因为客户端
// 该做的事不一样——前者是它自己重复发了，后者是它发早了。
func (s *Server) beginInitialize() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	switch {
	case s.owner == nil:
		return fmt.Errorf("sdkserver: 这台 SDK 服务器还没装上")
	case s.shuttingDown:
		return fmt.Errorf("sdkserver: 这台 SDK 服务器正在收摊")
	case s.initialized:
		return fmt.Errorf("sdkserver: 这条线已经握过手了，重来一次不支持")
	case s.initializing:
		return fmt.Errorf("sdkserver: 这条线上已经有一次 initialize 在跑了")
	}
	s.initializing = true
	return nil
}

// workspaceOf 把握手报上来的那条 cwd 换成一个工作区标识。
//
// 新增: 见 [WorkspaceLookup]。没挂登记册、或者这条 cwd 没人认领，都给空工作区——
// 两者对这条线是同一件事：它建出来的会话不属于任何工作区。
func (s *Server) workspaceOf(ctx context.Context, cwd string) (sessionlog.WorkspaceID, error) {
	if s.config.Workspaces == nil {
		return "", nil
	}
	id, found, err := s.config.Workspaces.WorkspaceOf(ctx, cwd)
	if err != nil {
		return "", fmt.Errorf("sdkserver: 这条 cwd 换不出工作区：%w", err)
	}
	if !found {
		return "", nil
	}
	return id, nil
}

// publishRoute 把那份路由公布出去，这台服务器从这一刻起才算办好了。
func (s *Server) publishRoute(
	workspaceID sessionlog.WorkspaceID,
	provider, model string,
	effort llm.ReasoningEffortID,
	maxTokens int,
) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.workspaceID = workspaceID
	s.provider = provider
	s.model = model
	s.reasoningEffort = effort
	s.maxTokens = maxTokens
	s.initializing = false
	// 这一句必须是最后一句：它是别的请求看得见的那个开关。
	s.initialized = true
}

// ensureAdapterFor 确认这个提供方有适配器认领，没有就走那条兜底路。
//
// 源: packages/sdk/server/src/server.ts:150-153
func (s *Server) ensureAdapterFor(ctx context.Context, provider string) error {
	if s.hasAdapterFor(provider) {
		return nil
	}
	if s.config.MountAdapter == nil {
		return fmt.Errorf("sdkserver: 没有适配器认领提供方 %q", provider)
	}
	unmount, err := s.config.MountAdapter(ctx, provider)
	if err != nil {
		return fmt.Errorf("sdkserver: 给提供方 %q 兜底挂适配器失败：%w", provider, err)
	}
	if unmount == nil {
		// [MountAdapter] 允许交回 nil，那表示没什么要撤的。
		return nil
	}
	s.mutex.Lock()
	// 兜底适配器一挂上就记下来，哪怕这次握手后面还会失败：它已经装进运行时了，
	// 撤销函数丢掉就等于漏掉一次拆解。收摊照样卸得掉它。
	s.unmounts = append(s.unmounts, unmount)
	s.mutex.Unlock()
	return nil
}

// hasAdapterFor 问「这个提供方有没有适配器认领」；没挂 LLM 服务时一律当作没有。
//
// 源: packages/sdk/server/src/server.ts:294-296
func (s *Server) hasAdapterFor(provider string) bool {
	if s.config.LLM == nil {
		return false
	}
	for _, entry := range s.config.LLM.ListProviders() {
		if entry.ID == provider {
			return true
		}
	}
	return false
}

// Prompt 把一轮用户输入排进一个会话，交回那条消息的身份。
//
// 源: packages/sdk/server/src/server.ts:171-193
//
// 它**只**说明这条消息进了队列，不说明这一轮跑出了什么——之后的动静从
// `session.event` 那条通知流里看。
func (s *Server) Prompt(ctx context.Context, params sdkprotocol.SessionPromptParams) (sdkprotocol.SessionPromptResult, error) {
	// 握手之前一轮输入都不收：这条线上的路由还没定下来，此刻建出来的会话会带着
	// 一条空路由，一步都跑不动。
	//
	// 源: packages/sdk/server/src/server.ts:177
	s.mutex.Lock()
	ready := s.initialized
	s.mutex.Unlock()
	if !ready {
		return sdkprotocol.SessionPromptResult{}, fmt.Errorf("sdkserver: 这台 SDK 服务器还没握手")
	}

	handle, err := s.getOrCreateSession(ctx, params.SessionID)
	if err != nil {
		return sdkprotocol.SessionPromptResult{}, err
	}
	// 只重载 agent 循环的那种重启会把循环里的 agent 拆掉，而这条会话记录活了下来；
	// 一个已经被拆掉的 agent 收下 Followup 之后什么都不会发生，所以投递之前先拿
	// 注册表验一遍这条记录指的还是不是活着的那一个。
	if err := s.assertLiveAgent(handle, params.SessionID); err != nil {
		return sdkprotocol.SessionPromptResult{}, err
	}
	content, err := s.durablePromptContent(ctx, params.ContentBlocks)
	if err != nil {
		return sdkprotocol.SessionPromptResult{}, err
	}
	// 附件准入是一道会等 I/O 的边：这期间收摊、或者一次只重载循环的重启，都可能把
	// 上面攥住的那个 handle 摘掉。所以过了这道边要再验一遍。
	//
	// 源: packages/sdk/server/src/server.ts:184-186
	if err := s.assertLiveAgent(handle, params.SessionID); err != nil {
		return sdkprotocol.SessionPromptResult{}, err
	}
	message := llm.NewUserMessage(content, llm.UserSource{})
	handle.Agent.Followup(message)
	return sdkprotocol.SessionPromptResult{MessageID: message.ID}, nil
}

// assertLiveAgent 验这条会话记录指着的 agent 还是注册表里活着的那一个。
//
// 源: packages/sdk/server/src/server.ts:195-199
func (s *Server) assertLiveAgent(handle agent.Handle, sessionID string) error {
	if live, ok := s.config.Agents.Get(handle.Agent.ID()); !ok || live != handle.Agent {
		return fmt.Errorf("sdkserver: 这个会话的 agent 在服务器之外被拆掉了：%s", sessionID)
	}
	return nil
}

// durablePromptContent 把一轮输入里的内联图片准入成耐久引用，其余原样交回。
//
// 源: packages/sdk/server/src/server.ts:39-52（durablePromptContent）
//
// 一张图都没有时一次存储都不碰，这一点和 DSH 那句提前返回一样：绝大多数轮次是
// 纯文本的，不该为它们要求一个附件库。
//
// 整批一次准入而不是逐张——[attachment.AdmitEncodedImages] 走的是批次规则（张数、
// 字节总和、媒体类型），逐张送进去就绕开了「一条消息最多带几张图」那几条。
func (s *Server) durablePromptContent(
	ctx context.Context, blocks sdkprotocol.PromptContent,
) (llm.Content, error) {
	if blocks == nil {
		// nil 和长度为零分得开：往下这条消息的内容是原样落进会话日志的，
		// 一个空切片和「根本没给内容」在那份日志的往返上不是同一件事。
		return nil, nil
	}

	encoded := make([]attachment.EncodedImage, 0, len(blocks))
	for _, block := range blocks {
		if block.Encoded != nil {
			encoded = append(encoded, attachment.EncodedImage{
				MediaType: block.Encoded.MimeType,
				Data:      block.Encoded.Data,
			})
		}
	}

	content := make(llm.Content, 0, len(blocks))
	if len(encoded) == 0 {
		for _, block := range blocks {
			content = append(content, block.Durable)
		}
		return content, nil
	}

	if s.config.Attachments == nil {
		return nil, fmt.Errorf("sdkserver: 一轮带内联图片的输入需要一个附件库")
	}
	refs, err := attachment.AdmitEncodedImages(ctx, s.config.Attachments, encoded)
	if err != nil {
		return nil, fmt.Errorf("sdkserver: 内联图片准入失败：%w", err)
	}

	next := 0
	for _, block := range blocks {
		if block.Encoded == nil {
			content = append(content, block.Durable)
			continue
		}
		content = append(content, llm.ImageBlock{Attachment: refs[next]})
		next++
	}
	return content, nil
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
	owner, options := s.owner, agent.Options{
		Provider:        s.provider,
		Model:           s.model,
		ReasoningEffort: s.reasoningEffort,
		MaxTokens:       s.maxTokens,
	}
	workspaceID := s.workspaceID
	s.mutex.Unlock()

	handle, err := s.config.Agents.Create(ctx, owner, agent.CreateOptions{
		SessionID:    sessionlog.SessionID(sessionID),
		WorkspaceID:  workspaceID,
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
	unmounts := s.unmounts
	s.unmounts = nil
	s.mutex.Unlock()

	for _, handle := range handles {
		if err := handle.Dispose(ctx); err != nil {
			failures = append(failures, fmt.Errorf("sdkserver: 拆会话 %s 失败：%w", handle.Agent.ID(), err))
		}
	}
	// 反序卸：后挂上的那个可能压在先挂上的那个之上。
	for index := len(unmounts) - 1; index >= 0; index-- {
		if err := unmounts[index](ctx); err != nil {
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
