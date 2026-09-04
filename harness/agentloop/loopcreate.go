// 本文件的作用：造一个 Agent 那条路——先备好、再装上去、最后公布，新建和恢复
// 走的是同一条。
//
// 源: packages/core/agent-loop/src/index.ts:1-713

package agentloop

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// ---- 生命周期装配 ----

// preparedAgent 是备好但还没公布的那一套资源，共享同一份拆除。
//
// 源: packages/core/agent-loop/src/index.ts:205-214（PreparedAgent）
type preparedAgent struct {
	// agent 是造好的那个驱动。
	agent *ReactLoopAgent
	// life 在工厂卸载、调用方取消、或者拆除开始时取消——任何一次 setup 等待
	// 都拿它当尽头。
	life context.Context
	// publish 进两张注册表、公布、报会话开始。
	publish func(ctx context.Context, source agent.SessionStartSource) (agent.Handle, error)
	// dispose 是那条倒着走的拆除：停机器、退注册表、拆作用域。只跑一次。
	dispose func(ctx context.Context) error
}

// untangleKey 标记「这次调用是拆除自己在摘掉自己挂在 owner 上的那条登记」。
//
// 新增: DSH 那边解绑和执行是两件事，摘一条登记不会把它跑一遍。Go 这边
// [scope.Scope.Defer] 交出来的 disposer 是跑完再摘的，所以自摘必须能被认出来。
// 用 ctx 上的记号而不是一个布尔标志位，是因为它只跟着**我们自己发出的那一次**
// 调用走：一次并发到达的 owner 释放带的是它自己的 ctx，读不到这个记号，
// 于是照常排队等同一次静止，而不是被误当成自摘直接放行。
type untangleKey struct{}

// prepare 给一个新 agent 造出驱动、作用域和**唯一那一份**倒序拆除。
//
// 源: packages/core/agent-loop/src/index.ts:453-575
//
// 这份拆除在公布**之前**就登记进工厂和 owner 作用域，所以一次装到一半的卸载
// 会把已经建起来的东西全部回滚掉；life 把调用方的取消和生命周期拆除熔在一起，
// 供 setup 期间的等待使用。
//
// # 新增: DSH 那个 SessionPreparation 在这里没有对应物
//
// 源: packages/core/agent-loop/src/index.ts:583、589-597
//
// DSH 用 `using preparation = SessionPreparation.create(...)` 包住备好的会话，
// 靠 `Symbol.dispose` 保证一份没公布成的会话被释放掉。Go 这边不需要：
// [github.com/snight1983/ds-harness-go/harness/session.Store.Prepare] 只读存储那张表来铸身份，
// **一行都不写进去**，所以一个备好却没公布的会话不占任何东西，丢掉它就够了。
func (l *AgentLoop) prepare(
	ctx context.Context,
	owner *scope.Scope,
	id sessionlog.SessionID,
	options agent.Options,
	live *session.Session,
) (*preparedAgent, error) {
	if err := assertAgentOptions(options); err != nil {
		return nil, err
	}
	if !l.ownership.isActive() {
		return nil, errLoopNotActive
	}
	if err := abortCause(ctx, id); err != nil {
		return nil, err
	}

	// 停用熔的是三个主人，各自带自己的原因：调用方的取消、owner 作用域的拆除、
	// 以及工厂拆除。它登记在**任何资源存在之前**，且落在可变的槽位上，
	// 这样一次在作用域还没铸出来时到达的卸载找到的是一个能用的拆除函数，
	// 而不是一处泄漏。
	life, endLife := context.WithCancelCause(context.WithoutCancel(ctx))
	unfuse := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			endLife(abortCause(ctx, id))
		case <-l.ownership.teardown.Done():
			endLife(context.Cause(l.ownership.teardown))
		case <-unfuse:
		}
	}()

	var (
		machineMutex  sync.Mutex
		machine       *ReactLoopAgent
		detachSession func(context.Context) error
		detachAgent   func(context.Context) error
	)
	// ready 在机器造出来（或者造失败）之后关掉。一次并发到达的 owner 拆除
	// 靠它避开「机器还是 nil，什么都没停就往下拆」那个窗口。
	ready := make(chan struct{})
	var closeReady sync.Once

	var (
		untrack       func()
		unfollowOwner func(context.Context) error
		disposeOnce   sync.Once
		disposeErr    error
	)
	// 倒着拆，并且做成一次性的：每一个抢着拆的主人等的都是同一次静止。
	// 先停机器、等它退出、拆掉它那个作用域世界，再退两张注册表，最后收账。
	sharedDispose := func(disposeCtx context.Context) error {
		disposeOnce.Do(func() {
			endLife(fmt.Errorf("agent %q lifecycle disposed", string(id)))
			close(unfuse)

			<-ready
			machineMutex.Lock()
			current := machine
			machineMutex.Unlock()

			var failures []error
			if current != nil {
				// 拆除**就是**一次 disposed 因由的取消加上等它静止。此后再送进来的
				// 活儿是发送方的缺陷——注册表马上就要把这个 agent 丢掉了。
				current.Cancel(sessionlog.DisposedCancel{}, agent.CancelOptions{})
				if err := current.WhenIdle(disposeCtx); err != nil {
					failures = append(failures, err)
				}
				if err := current.Scope().Dispose(disposeCtx); err != nil {
					failures = append(failures, err)
				}
				l.forgetAgent(current)
			}
			if detachAgent != nil {
				if err := detachAgent(disposeCtx); err != nil {
					failures = append(failures, err)
				}
			}
			if detachSession != nil {
				if err := detachSession(disposeCtx); err != nil {
					failures = append(failures, err)
				}
			}
			untrack()
			// 摘掉 owner 上那条登记，否则一个长命的 owner 会一直攒着已经拆完的
			// agent 的闭包。[scope.Scope.Defer] 的 disposer 是**跑完再摘**的，
			// 所以这一下会把下面那个回调也叫一遍——用 disposeCtx 上的记号让它
			// 认出「这是拆除自己在摘自己」，否则它会再进一次 disposeOnce，
			// 变成在这次 Do 里面等这次 Do。
			if unfollowOwner != nil {
				if err := unfollowOwner(context.WithValue(
					disposeCtx, untangleKey{}, untangleKey{})); err != nil {
					failures = append(failures, err)
				}
			}
			disposeErr = errors.Join(failures...)
		})
		return disposeErr
	}
	untrack = l.ownership.track(sharedDispose)

	unfollowOwner, err := owner.Defer(fmt.Sprintf("agentLoop.lifecycle(%s)", string(id)),
		func(disposeCtx context.Context) error {
			// 只有上面那一下自摘会带着这个记号。owner 作用域自己释放时走的是
			// 它自己的 ctx，那一路照常拆，并且照常等到同一次静止。
			if disposeCtx.Value(untangleKey{}) != nil {
				return nil
			}
			endLife(fmt.Errorf("agent %q setup aborted: owner disposed during setup", string(id)))
			return sharedDispose(disposeCtx)
		})
	if err != nil {
		untrack()
		close(unfuse)
		endLife(err)
		closeReady.Do(func() { close(ready) })
		return nil, fmt.Errorf("harness/agentloop: 把 agent %q 的生命周期挂到 owner 上失败：%w", string(id), err)
	}

	assertLive := func() error {
		if life.Err() == nil {
			return nil
		}
		return context.Cause(life)
	}

	built, err := NewReactLoopAgent(life, l.deps, owner.Key(), id, options, live)
	machineMutex.Lock()
	machine = built
	machineMutex.Unlock()
	closeReady.Do(func() { close(ready) })
	if err != nil {
		_ = sharedDispose(ctx)
		return nil, err
	}
	if err := assertLive(); err != nil {
		_ = sharedDispose(ctx)
		return nil, err
	}

	publish := func(publishCtx context.Context, source agent.SessionStartSource) (agent.Handle, error) {
		if err := assertLive(); err != nil {
			return agent.Handle{}, err
		}
		// 会话的摘除挂在**这个 agent 自己**的作用域上：会话和循环是一条按次序
		// 拆的链，最后那几条事件必须在存储登记消失之前发布出去。
		detach, err := l.deps.Sessions.Enter(built.Scope(), live)
		if err != nil {
			return agent.Handle{}, err
		}
		detachSession = detach

		detach, err = l.deps.Agents.Enter(built, l.agentForScope(owner.Key()))
		if err != nil {
			return agent.Handle{}, err
		}
		detachAgent = detach

		if err := l.deps.Sessions.Announce(publishCtx, live); err != nil {
			return agent.Handle{}, err
		}
		if err := assertLive(); err != nil {
			return agent.Handle{}, err
		}
		// 先记进这张表再公布：一个同步的创建观察者装系统提示词时就该看得见
		// 这个 agent 的 provider／model。
		l.rememberAgent(built)
		if err := l.deps.Agents.Announce(publishCtx, built); err != nil {
			return agent.Handle{}, err
		}
		if err := assertLive(); err != nil {
			return agent.Handle{}, err
		}
		// 一个同步的公布／会话开始观察者可能已经开始拆了；机器此刻已经活着
		// （从会话开始这个扩展点投递是通的），所以这里只欠一次活性复查。
		if err := l.deps.Agents.ReportSessionStart(built, source); err != nil {
			return agent.Handle{}, err
		}
		if err := assertLive(); err != nil {
			return agent.Handle{}, err
		}
		return agent.Handle{Agent: built, Dispose: sharedDispose}, nil
	}
	return &preparedAgent{agent: built, life: life, publish: publish, dispose: sharedDispose}, nil
}

// ---- 对外入口 ----

// Create 在调用方给的身份上造一个 agent 和它的会话，归属于这个工厂自己的作用域。
//
// 源: packages/core/agent-loop/src/index.ts:580-587
//
// 配置驱动的那条路在进这个边界之前就把一个新铸的组合身份定好了。
func (l *AgentLoop) Create(
	ctx context.Context,
	id sessionlog.SessionID,
	options agent.Options,
	workspaceID sessionlog.WorkspaceID,
) (agent.Agent, error) {
	live, err := l.deps.Sessions.Prepare(id, session.CreateOptions{WorkspaceID: workspaceID})
	if err != nil {
		return nil, err
	}
	prepared, err := l.prepare(ctx, l.owner, id, options, live)
	if err != nil {
		return nil, err
	}
	handle, err := prepared.publish(ctx, agent.StartStartup)
	if err != nil {
		return nil, errors.Join(err, prepared.dispose(ctx))
	}
	return handle.Agent, nil
}

// CreateAgent 在调用方给的会话身份上造一个归它所有的 agent。
//
// 源: packages/core/agent-loop/src/index.ts:589-604
func (l *AgentLoop) CreateAgent(
	ctx context.Context,
	owner *scope.Scope,
	options agent.CreateOptions,
) (agent.Handle, error) {
	if owner == nil {
		return agent.Handle{}, errors.New("harness/agentloop: 造一个 agent 要有一个持有它的作用域")
	}
	live, err := l.deps.Sessions.Prepare(options.SessionID, session.CreateOptions{
		Seed:            options.Seed,
		BaseSeq:         options.BaseSeq,
		WorkspaceID:     options.WorkspaceID,
		ParentSession:   options.ParentSession,
		SeedLength:      options.SeedLength,
		Origin:          options.Origin,
		DelegationDepth: options.DelegationDepth,
		AgentPreset:     options.AgentPreset,
	})
	if err != nil {
		return agent.Handle{}, err
	}
	// 这条路的会话是活会话存储当场造的，提供方那边没有攥着任何待还的状态，
	// 所以准备期没有释放动作——DSH 那句 `SessionPreparation.create(...)`
	// 同样不带 release。裹一层是为了让公布那条路只有一个形状。
	return l.setupAndPublish(ctx, owner, options.SessionID,
		session.NewPreparation(live, session.PreparationOptions{}),
		options.AgentOptions, options.Setup, agent.StartStartup)
}

// setupAndPublish 围着一段到手的准备期备好一个 agent、跑完 setup、把它公布出去。
//
// 源: packages/core/agent-loop/src/index.ts:686-708
//
// 那段准备期在这里**结束**，无论公布成没成：DSH 写的是 `using ownedPreparation
// = preparation`，Go 的对应物就是下面这句 defer。提供方那份状态可能已经被公布
// 那一步接手走了，那时候释放是空操作——[session.Preparation.Release] 保证幂等，
// 所以这里不需要分两条路。
func (l *AgentLoop) setupAndPublish(
	ctx context.Context,
	owner *scope.Scope,
	id sessionlog.SessionID,
	preparation *session.Preparation,
	options agent.Options,
	setup agent.Setup,
	source agent.SessionStartSource,
) (agent.Handle, error) {
	defer preparation.Release()

	prepared, err := l.prepare(ctx, owner, id, options, preparation.Session())
	if err != nil {
		return agent.Handle{}, err
	}

	if setup != nil {
		commit, err := raceAbort(prepared.life, id, func() (func() error, error) {
			return setup(prepared.life, prepared.agent.Scope())
		}, nil)
		if err != nil {
			return agent.Handle{}, errors.Join(err, prepared.dispose(ctx))
		}
		if commit != nil {
			if err := commit(); err != nil {
				return agent.Handle{}, errors.Join(err, prepared.dispose(ctx))
			}
		}
	}

	handle, err := prepared.publish(ctx, source)
	if err != nil {
		return agent.Handle{}, errors.Join(err, prepared.dispose(ctx))
	}
	return handle, nil
}

// Resume 从配置好的那份持久化服务里续跑一个归调用方所有的 agent。
//
// 源: packages/core/agent-loop/src/index.ts:624-635
func (l *AgentLoop) Resume(
	ctx context.Context,
	owner *scope.Scope,
	options agent.ResumeOptions,
) (agent.Handle, error) {
	if l.config.Persistence == nil {
		return agent.Handle{}, errors.New(
			"cannot resume: session persistence is not configured (load a session persistence backend)")
	}
	return l.resumeWith(ctx, owner, l.config.Persistence, options)
}

// resumeWith 走一份显式的持久化句柄续跑，配置驱动那条延后的路用的就是它。
//
// 源: packages/core/agent-loop/src/index.ts:637-710
func (l *AgentLoop) resumeWith(
	ctx context.Context,
	owner *scope.Scope,
	persistence SessionPersistence,
	options agent.ResumeOptions,
) (agent.Handle, error) {
	if owner == nil {
		return agent.Handle{}, errors.New("harness/agentloop: 续跑一个 agent 要有一个持有它的作用域")
	}
	id := options.ResumeSessionID

	// 这次读取可能活得比它的主人还久：把它和调用方的取消、owner 作用域的拆除、
	// 以及工厂拆除三者一起竞速，这样一个永远不落定的后端扣不住这个身份。
	loadCtx, endLoad := context.WithCancelCause(context.WithoutCancel(ctx))
	defer endLoad(nil)
	stopFuse := make(chan struct{})
	defer close(stopFuse)
	go func() {
		select {
		case <-ctx.Done():
			endLoad(abortCause(ctx, id))
		case <-l.ownership.teardown.Done():
			endLoad(context.Cause(l.ownership.teardown))
		case <-stopFuse:
		}
	}()
	unfollowOwner, err := owner.Defer(fmt.Sprintf("agentLoop.resume-load(%s)", string(id)),
		func(context.Context) error {
			endLoad(fmt.Errorf("agent %q setup aborted: owner disposed during setup", string(id)))
			return nil
		})
	if err != nil {
		return agent.Handle{}, fmt.Errorf("harness/agentloop: 把 agent %q 的读取挂到 owner 上失败：%w", string(id), err)
	}

	// 一份在取消之后才到的准备期没人要了，但**不能**就这么扔掉：提供方那边
	// 还攥着一份预留，不还回去这个会话身份就一直被扣着，后面谁都续不了它。
	// 所以竞速输掉那一支也要走 Release——这正是 DSH 那个
	// `(abandoned) => { abandoned[Symbol.dispose]() }` 回调的作用。
	preparation, loadErr := raceAbort(loadCtx, id, func() (*session.Preparation, error) {
		return persistence.Prepare(loadCtx, id)
	}, func(abandoned *session.Preparation) { abandoned.Release() })
	unfollowErr := unfollowOwner(ctx)
	if loadErr != nil {
		return agent.Handle{}, errors.Join(loadErr, unfollowErr)
	}
	if unfollowErr != nil {
		preparation.Release()
		return agent.Handle{}, unfollowErr
	}
	if !l.ownership.isActive() {
		preparation.Release()
		return agent.Handle{}, errLoopNotActive
	}

	return l.setupAndPublish(ctx, owner, id, preparation,
		options.AgentOptions, options.Setup, agent.StartResume)
}
