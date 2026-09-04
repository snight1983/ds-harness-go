// 本文件的作用：配置里预置的那几个 Agent 怎么起——新建还是恢复、起失败了报给谁，
// 以及同名的上一个还没排空干净时怎么等。
//
// 源: packages/core/agent-loop/src/index.ts:1-713

package agentloop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/google/uuid"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// startConfiguredAgents 把配置里那些 agent 起起来。
//
// 源: packages/core/agent-loop/src/index.ts:381-381（constructor 末尾那个循环）
func (l *AgentLoop) startConfiguredAgents(ctx context.Context) {
	for _, configured := range l.config.Agents {
		if configured.ResumeSessionID == "" {
			l.startFreshConfigured(ctx, configured)
			continue
		}
		l.startResumingConfigured(ctx, configured)
	}
}

// startFreshConfigured 起一个不续跑的配置项。
//
// 源: packages/core/agent-loop/src/index.ts:384-397（`resumeSessionId` 为空那一支）
func (l *AgentLoop) startFreshConfigured(ctx context.Context, configured ConfiguredAgent) {
	configuredID := configured.SessionID
	if configuredID == "" {
		configuredID = sessionlog.SessionID(fmt.Sprintf("%s-session-%s", configured.ID, uuid.NewString()))
	}

	// 只有**配置里明确给了身份**的那些项才走持久化：一个现铸的随机身份不可能
	// 已经落过地，去读它只会白等一次后端往返。
	if configured.SessionID == "" || l.config.Persistence == nil {
		if _, err := l.Create(ctx, configuredID, configured.Options, configured.WorkspaceID); err != nil {
			l.reportConfiguredStartupFailure(configured.ID, "restore", configuredID, err)
		}
		return
	}

	l.ownership.trackStartup(func() {
		if err := l.restoreOrCreateConfigured(ctx, configuredID, configured); err != nil {
			l.reportConfiguredStartupFailure(configured.ID, "restore", configuredID, err)
		}
	})
}

// startResumingConfigured 起一个要续跑的配置项。
//
// 源: packages/core/agent-loop/src/index.ts:398-410
//
// 新增: DSH 把这一支裹在 `ctx.effect(() => ctx.inject(['sessionPersistence'], ...))`
// 里——持久化服务**将来**挂上来的时候这段才跑，卸载时又停掉。Go 里没有服务动态
// 到场这件事：装配在构造这个工厂之前就定死了，所以持久化不在位是一个**永久**
// 状态，走和其他启动失败同一条通报路，而不是无限等下去。
func (l *AgentLoop) startResumingConfigured(ctx context.Context, configured ConfiguredAgent) {
	if l.config.Persistence == nil {
		l.reportConfiguredStartupFailure(configured.ID, "resume", configured.ResumeSessionID,
			errors.New("cannot resume: session persistence is not configured"))
		return
	}
	l.ownership.trackStartup(func() {
		_, err := l.resumeWith(ctx, l.owner, l.config.Persistence, agent.ResumeOptions{
			ResumeSessionID: configured.ResumeSessionID,
			AgentOptions:    configured.Options,
		})
		if err != nil {
			l.reportConfiguredStartupFailure(configured.ID, "resume", configured.ResumeSessionID, err)
		}
	})
}

// reportConfiguredStartupFailure 把一次兜住了的声明式启动失败通报给那些绑着这个身份的消费方。
//
// 源: packages/core/agent-loop/src/index.ts:384-404
//
// 工厂已经在拆了就不报：那次启动是被这次拆除自己取消掉的，报出去只会让消费方
// 把一次正常卸载当成故障。
func (l *AgentLoop) reportConfiguredStartupFailure(
	configID string,
	action string,
	sessionID sessionlog.SessionID,
	failure error,
) {
	if !l.ownership.isActive() {
		return
	}
	l.logger.Warn("harness/agentloop: 配置驱动的启动失败",
		slog.String("agent", configID),
		slog.String("action", action),
		slog.String("session", string(sessionID)),
		slog.Any("error", failure))

	for observer := range l.startFailed.Values() {
		l.notifyStartFailed(configID, observer, sessionID, failure)
	}
}

// notifyStartFailed 叫一个启动失败观察者，它自己 panic 了就记一条继续叫下一个。
//
// 源: packages/core/agent-loop/src/index.ts:396-403（那两个 try/catch）
//
// 一个观察者的事故不能把其余观察者落下：它们各自为这个身份缓着活儿，
// 少通知一个就是那些活儿永远等下去。
func (l *AgentLoop) notifyStartFailed(
	configID string,
	observer ConfigStartFailedObserver,
	sessionID sessionlog.SessionID,
	failure error,
) {
	defer func() {
		if recovered := recover(); recovered != nil {
			l.logger.Warn("harness/agentloop: 启动失败观察者 panic 了",
				slog.String("agent", configID),
				slog.String("session", string(sessionID)),
				slog.Any("panic", recovered))
		}
	}()
	observer(sessionID, failure)
}

// restoreOrCreateConfigured 在重挂时读回一个已经落地的确切配置身份，第一次用则新建它。
//
// 源: packages/core/agent-loop/src/index.ts:406-428
func (l *AgentLoop) restoreOrCreateConfigured(
	ctx context.Context,
	sessionID sessionlog.SessionID,
	configured ConfiguredAgent,
) error {
	if err := l.waitForDrainingConfiguredIdentity(ctx, sessionID); err != nil {
		return err
	}
	if !l.ownership.isActive() {
		return nil
	}

	_, resumeErr := l.resumeWith(ctx, l.owner, l.config.Persistence, agent.ResumeOptions{
		ResumeSessionID: sessionID,
		AgentOptions:    configured.Options,
	})
	if resumeErr == nil {
		return nil
	}
	if !l.ownership.isActive() {
		return nil
	}

	// 一次读取就是这个身份上的串行屏障——它把急切的写回和生命周期退休都排在
	// 自己后面。只有「存档确实不存在」才回落到第一次创建；损坏和后端故障
	// 照样吵。
	headers, err := l.config.Persistence.List(ctx)
	if err != nil {
		return errors.Join(resumeErr, err)
	}
	for _, header := range headers {
		if header.ID == sessionID {
			return resumeErr
		}
	}

	_, err = l.Create(ctx, sessionID, configured.Options, configured.WorkspaceID)
	return err
}

// waitForDrainingConfiguredIdentity 等一个正在排干的同名生命周期把注册表登记摘干净。
//
// 源: packages/core/agent-loop/src/index.ts:430-451
func (l *AgentLoop) waitForDrainingConfiguredIdentity(ctx context.Context, sessionID sessionlog.SessionID) error {
	// 只有还占着注册表的身份才值得等；一个活得好好的占用者是一次撞名，
	// 那由下面的创建／续跑自己报出来。
	occupied := func() bool {
		if _, live := l.deps.Agents.Get(sessionID); live {
			return true
		}
		_, live := l.deps.Sessions.Get(sessionID)
		return live
	}
	if !occupied() {
		return nil
	}

	released := make(chan struct{})
	var once sync.Once
	checkReleased := func() {
		if !occupied() {
			once.Do(func() { close(released) })
		}
	}

	unwatchAgent, err := l.deps.Agents.OnDisposed(ctx, l.owner, func(agent.Agent) { checkReleased() })
	if err != nil {
		return fmt.Errorf("harness/agentloop: 等身份 %q 排干时挂不上 agent 观察者：%w", string(sessionID), err)
	}
	unwatchSession, err := l.deps.Sessions.OnDisposed(ctx, l.owner, func(*session.Session) { checkReleased() })
	if err != nil {
		return errors.Join(
			fmt.Errorf("harness/agentloop: 等身份 %q 排干时挂不上会话观察者：%w", string(sessionID), err),
			unwatchAgent(ctx))
	}

	// 挂上观察者**之后**再查一次：这两步之间释放掉的话，那两条通知谁都收不到。
	checkReleased()
	l.ownership.waitWhileActive(released)
	return errors.Join(unwatchSession(ctx), unwatchAgent(ctx))
}
