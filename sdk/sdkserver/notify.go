// 本文件的作用：往对面发的那四条通知——每一条各自订阅运行时的哪条边、怎么翻成线上
// 的话、以及什么时候闭嘴不发。
//
// 源: packages/sdk/server/src/server.ts:70-103

package sdkserver

import (
	"context"
	"fmt"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/sdk/sdkprotocol"
	sessionlog "github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/subagent/subagent"
)

// subscribe 按 DSH 那个构造函数的次序挂上那四条订阅，逐条记进 disposers。
//
// 源: packages/sdk/server/src/server.ts:70-103
//
// 中途失败就把错误交出去，[Server.Install] 负责把已经挂上的那几条摘掉——那几条已经
// 记在 disposers 里了，[Server.releaseSubscriptions] 按反序摘得掉。
func (s *Server) subscribe(ctx context.Context, owner *scope.Scope) error {
	steps := []func() (func(context.Context) error, error){
		func() (func(context.Context) error, error) {
			return s.config.Sessions.OnEvent(ctx, owner, s.onSessionEvent)
		},
		func() (func(context.Context) error, error) {
			return s.config.Agents.OnStatus(ctx, owner, s.onAgentStatus)
		},
		func() (func(context.Context) error, error) {
			return s.config.Sessions.OnCreated(ctx, owner, s.onSessionCreated)
		},
		func() (func(context.Context) error, error) {
			return s.config.Subagents.OnEnd(ctx, owner, s.onSubagentEnd)
		},
	}
	for _, step := range steps {
		dispose, err := step()
		if err != nil {
			return fmt.Errorf("sdkserver: 挂订阅失败：%w", err)
		}
		s.mutex.Lock()
		s.disposers = append(s.disposers, dispose)
		s.mutex.Unlock()
	}
	return nil
}

// notify 把一条通知发出去，用的是 [Server.Install] 记下来的那个上下文。
//
// 新增: 运行时那几条边的观察者不带 [context.Context]，而
// [sdkprotocol.Peer.Notify] 要一个，所以这里读那份记下来的。发不出去只记一行：
// 通知本来就是单向的，一条发失败的通知改变不了运行时里已经发生的那件事，更不该
// 把发出这条边的那次追加或那次拆解带崩。
func (s *Server) notify(method string, params any) {
	s.mutex.Lock()
	ctx := s.notifyCtx
	s.mutex.Unlock()
	if ctx == nil {
		// 还没装上（或已经拆干净了）：这条线上没有对面可发。
		return
	}
	if err := s.config.Peer.Notify(ctx, method, params); err != nil {
		s.config.warn(fmt.Sprintf("sdkserver: 发 %s 通知失败：%v", method, err))
	}
}

// onSessionEvent 把一条会话日志事件原样转出去。
//
// 源: packages/sdk/server/src/server.ts:71-74
//
// 这条边覆盖运行时里的**每一个**会话，不只是这台服务器建出来的那些——订阅落在
// [Server.Install] 给的那个全局层作用域上，DSH 的行为也是如此。
func (s *Server) onSessionEvent(session *coresession.Session, event sessionlog.Event) {
	s.notify(sdkprotocol.MethodSessionEvent, sdkprotocol.SessionEventNotification{
		SessionID: string(session.ID()),
		Event:     event,
	})
}

// onAgentStatus 把一个活 agent 的整体状态转出去。
//
// 源: packages/sdk/server/src/server.ts:75-77
func (s *Server) onAgentStatus(live agent.Agent, status agent.Status) {
	s.notify(sdkprotocol.MethodSessionStatus, sdkprotocol.SessionStatusNotification{
		SessionID: string(live.ID()),
		Status:    wireStatus(status),
	})
}

// wireStatus 把运行时那两个状态翻成线上的两个。
//
// 源: packages/sdk/server/src/server.ts:76（`status` 直接上线）
//
// 新增: DSH 把运行时的状态字面量直接塞进负载，靠两边字符串恰好相同兑现兼容。Go 这边
// 两个类型是分开的（[agent.Status] 和 [sdkprotocol.AgentStatus]），所以这里必须写一次
// 转换。认不出来的状态一律报 running：一个说不出名字的状态**不是**空闲，把它报成空闲
// 会让对面以为这一轮结束了。
func wireStatus(status agent.Status) sdkprotocol.AgentStatus {
	if status == agent.StatusIdle {
		return sdkprotocol.AgentIdle
	}
	return sdkprotocol.AgentRunning
}

// onSessionCreated 在运行时内部新开一个**子**会话时告诉对面。
//
// 源: packages/sdk/server/src/server.ts:78-86
//
// 没有父会话的那些是顶层会话——包括这台服务器自己为 `session/prompt` 建出来的
// 那些——一条都不发：对面已经知道它自己开的那些会话。
func (s *Server) onSessionCreated(_ context.Context, session *coresession.Session) error {
	parent := session.Header().ParentSession
	if parent == "" {
		return nil
	}
	s.notify(sdkprotocol.MethodSubagentStarted, sdkprotocol.SubagentStartedNotification{
		ParentSessionID: string(parent),
		ChildSessionID:  string(session.ID()),
	})
	return nil
}

// onSubagentEnd 在一次**进程内**子 agent 跑完之后告诉对面它的结局。
//
// 源: packages/sdk/server/src/server.ts:87-103
//
// 只报进程内的孩子：那个 Local 标记是子 agent 服务在孩子拆解的过程中拍下来的事实，
// 光靠对得上的标识或者父子血缘**说明不了**这一次是本地跑的（DSH 那句注释）。
//
// 新增: parent 为 nil 时不发。DSH 从监听器的 `this` 里一定取得到那个派活的父，Go 这边
// 它是一个可以为 nil 的入参（见 [subagent.StartObserver]）——父的作用域先散了就是 nil。
// 这条通知的负载里 ParentSessionID 是必填的，凭空编一个会报出一段假的血缘，所以宁可
// 不发。
func (s *Server) onSubagentEnd(info subagent.RunEndInfo, parent agent.Agent) {
	if !info.Local || parent == nil {
		return
	}
	s.notify(sdkprotocol.MethodSubagentFinished, sdkprotocol.SubagentFinishedNotification{
		Provider:             info.Provider,
		AgentID:              string(info.ID),
		ParentSessionID:      string(parent.ID()),
		ChildSessionID:       string(info.ID),
		Status:               runStatus(info.StopReason, s.config.MaxTokensAsSuccess),
		StopReason:           info.StopReason,
		LastAssistantMessage: info.LastAssistantMessage,
	})
}

// runStatus 把一个停止原因折成线上那两档结论。
//
// 源: packages/sdk/server/src/server.ts:43-46
//
// 「做完了」永远是被接受的结果；「撞上输出上限」算不算，由部署口径说了算（见
// [Config.MaxTokensAsSuccess]）；其余一律是错误。
func runStatus(reason subagent.StopReason, maxTokensAsSuccess bool) sdkprotocol.RunStatus {
	switch {
	case reason == subagent.StopCompleted:
		return sdkprotocol.RunOK
	case reason == subagent.StopMaxTokens && maxTokensAsSuccess:
		return sdkprotocol.RunOK
	default:
		return sdkprotocol.RunError
	}
}
