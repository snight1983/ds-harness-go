// 本文件的作用：那台对外服务——每个会话那份可丢弃的缓存怎么建怎么跟、一次改动
// 从校验到落盘到通告走了哪几步，以及「活化」这件从不落盘的事怎么跨过那次追加。
//
// 源: packages/goal/goal/src/index.ts:115-590

package goal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"weak"

	"github.com/google/uuid"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/session"
)

// Agents 是本包要用的那一小块 agent 注册表能力。
//
// 新增: DSH 靠 `static inject = ['agents']` 拿到整个注册表。Go 里只声明用得着的
// 那两个方法（成例见 [github.com/snight1983/ds-harness-go/schedule.Agents]）：一个回答「我手里这个
// agent 此刻还是注册表里那一个吗」，一个用来在会话生命周期起跑时把活化打回原形。
type Agents interface {
	// Get 按标识取此刻活着的那个 agent。
	Get(id session.SessionID) (agent.Agent, bool)
	// OnSessionStart 登记一个「会话生命周期开始了」的观察者。
	OnSessionStart(
		ctx context.Context,
		owner *scope.Scope,
		observer agent.SessionStartObserver,
	) (func(context.Context) error, error)
}

// ChangedObserver 是 `goal/changed` 那条边的观察者。
//
// 源: packages/goal/goal/src/domain.ts:105-115
//
// 只观察：这条边发在一次改动**已经落进日志之后**，观察者说什么都改变不了它，
// 所以没有错误通道，panic 也被逐个兜住。
type ChangedObserver func(owner agent.Agent, change Changed)

// changedLayer 是一个作用域在那张观察者表里的全部贡献。
//
// 新增: DSH 靠 cordis 的 `agentEvents(ctx, agent).emit(...)` 做作用域过滤派发，
// 本仓库统一换成 [github.com/snight1983/ds-harness-go/core/scope.Layers]——全局层加各作用域的覆盖层，
// 派发时按载体作用域的父链取并集（成例见 [github.com/snight1983/ds-harness-go/subagent.lifecycleLayer]）。
type changedLayer struct {
	changed *scope.AnonymousEntries[ChangedObserver]
}

// newChangedLayer 造一层。
func newChangedLayer() *changedLayer {
	return &changedLayer{changed: scope.NewAnonymousEntries[ChangedObserver]()}
}

// IsEmpty 表示这一层空了，[scope.Layers] 靠它回收空层。
func (l *changedLayer) IsEmpty() bool { return l.changed.IsEmpty() }

// Config 是造一台服务要的部署方选择。
//
// 源: packages/goal/goal/src/index.ts:170-174（Config）、186-188
type Config struct {
	// Agents 是 agent 注册表，必填。
	Agents Agents
	// DefaultMaxGoalRounds 是一次 create 没自带上限时用的轮数；0 表示按
	// [DefaultMaxGoalRounds]。
	//
	// 新增: DSH 那边是 `defaultMaxGoalRounds?: number`，缺省 256。Go 用零值当
	// 「没给」在这里不丢东西：0 本身是一个非法上限，两边都不是一个能生效的配置。
	DefaultMaxGoalRounds int
	// Now 是墙上时钟；nil 表示 [time.Now]。
	Now func() time.Time
	// Logger 是诊断落点；nil 表示 [slog.Default]。
	Logger *slog.Logger
}

// pendingActivation 是一次改动跨过那次追加时带着的活化意图。
//
// 源: packages/goal/goal/src/index.ts:132
//
// 它存在的理由是 [Service.sync] 那条「不是我写的 goal/change 一律打回 disarmed」
// 的规矩：本包自己刚写下的那一条必须认得出来，否则每一次 create/resume 都会在
// 它自己那次同步里把刚点亮的活化立刻掐掉。seq 就是那个身份。
type pendingActivation struct {
	seq        int
	activation Activation
}

// cache 是一个会话那份进程内的、可丢弃的目标投影。
//
// 源: packages/goal/goal/src/index.ts:127-133
//
// 「可丢弃」是它的立身之本：除了 activation 之外它手里没有任何一份日志里没有的
// 事实。而 activation 本来就不该被保住——一份新的缓存永远从 [Disarmed] 起步。
type cache struct {
	state       *FoldState
	activation  Activation
	observedSeq int
	pending     *pendingActivation
}

// Service 是那台由会话日志独家支撑的目标服务。
//
// 源: packages/goal/goal/src/index.ts:235-622（GoalService）
//
// 新增: DSH 是单线程的，本包在 Go 里会被多条协程同时叫到，所以每一次调用整个罩在
// 一把互斥锁下——「同步到最新、验一次跃迁、追加一条改动」这三步必须是原子的，
// 中间被另一次改动插进来会写下一条接不上的修订。
//
// 那条 `goal/changed` 通告**在锁外**发：一个观察者回头调 [Service.Get] 是完全
// 合理的用法，在锁内发会把它变成一次自锁。
type Service struct {
	agents               Agents
	defaultMaxGoalRounds int
	now                  func() time.Time
	logger               *slog.Logger

	layers *scope.Layers[*changedLayer]

	mutex sync.Mutex
	// caches 是每个会话那份缓存，键是**弱**引用。
	//
	// 新增: DSH 用 `WeakMap<Session, GoalCache>`。Go 里对应的是 weak.Pointer
	// （成例见 [github.com/snight1983/ds-harness-go/guard/repeattoolreminder]）——一个用完就不再有人
	// 引用的会话不该因为这张表而留在内存里。键必须是会话**对象**而不是它的标识：
	// 同一个标识被重新开起来是另一段生命周期，它绝不能继承上一段的活化。
	caches map[weak.Pointer[coresession.Session]]*cache
}

// New 造一台目标服务。
//
// 源: packages/goal/goal/src/index.ts:193-214
func New(config Config) (*Service, error) {
	if config.Agents == nil {
		return nil, errors.New("goal: 需要一个 agent 注册表")
	}
	defaultRounds := config.DefaultMaxGoalRounds
	if defaultRounds == 0 {
		defaultRounds = DefaultMaxGoalRounds
	}
	if _, err := resolveMaxGoalRounds(defaultRounds); err != nil {
		return nil, fmt.Errorf("goal: 部署方给的默认轮数上限不成立：%w", err)
	}
	layers, err := scope.NewLayers(
		func(*scope.Key) (*changedLayer, error) { return newChangedLayer(), nil },
		nil,
	)
	if err != nil {
		// 走不到：scope.NewLayers 只在造全局层时失败，而上面那个构造函数不会失败。
		return nil, err
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		agents:               config.Agents,
		defaultMaxGoalRounds: defaultRounds,
		now:                  now,
		logger:               logger,
		layers:               layers,
		caches:               map[weak.Pointer[coresession.Session]]*cache{},
	}, nil
}

// Install 把「会话生命周期起跑就把活化打回原形」那条边挂上，返回撤销它的函数。
//
// 源: packages/goal/goal/src/index.ts:198-200
//
// 这条边是 [Activation] 那句「从不落盘」真正的兑现处：续会话、分叉、换驱动都会
// 走到它，于是一个回放出来的 active 目标绝不会自己动起来——要动起来必须有人显式
// 再 [Service.Resume] 一次。
//
// 新增: DSH 在构造函数里就 `ctx.on(...)` 挂上了。Go 里拆成一个显式的方法：登记
// 需要一个 ctx 和一个持有它的作用域，而这两样构造的时候都还没有。
func (s *Service) Install(ctx context.Context, owner *scope.Scope) (func(context.Context) error, error) {
	return s.agents.OnSessionStart(ctx, owner, func(started agent.Agent, _ agent.SessionStartSource) {
		s.mutex.Lock()
		defer s.mutex.Unlock()
		entry, err := s.cacheFor(started.Session())
		if err != nil {
			s.logger.Warn("goal: 会话起跑时折不动这条目标日志", "agent", string(started.ID()), "err", err)
			return
		}
		entry.activation = Disarmed
	})
}

// OnChanged 登记一个 `goal/changed` 观察者，返回撤销这次登记的函数。
//
// 源: packages/goal/goal/src/domain.ts:105-115
//
// owner 决定这次登记落在哪一层：没有身份的作用域落全局层，看得见每一个 agent 的
// 改动；有身份的作用域只看得见它（或它的子孙）那条链上的。
func (s *Service) OnChanged(
	ctx context.Context,
	owner *scope.Scope,
	observer ChangedObserver,
) (func(context.Context) error, error) {
	if observer == nil {
		return nil, errors.New("goal: OnChanged 需要一个观察者")
	}
	return s.layers.Effect(ctx, owner, func(layer *changedLayer) (func(), error) {
		return layer.changed.Append(observer), nil
	}, scope.EffectOptions{Label: "goals.OnChanged()"})
}

// ---- 读 ----

// Get 读一个确切的活 agent 此刻的目标。
//
// 源: packages/goal/goal/src/index.ts:222-227
//
// 没有当前目标时交回 nil；agent 不是注册表里活着的那一个时交回
// [CodeAgentNotLive]。
func (s *Service) Get(owner agent.Agent) (*View, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	entry, err := s.prepare(owner)
	if err != nil {
		return nil, err
	}
	return s.view(entry), nil
}

// Disarm 收回进程内的续推资格，**不动**耐久阶段和修订号。
//
// 源: packages/goal/goal/src/index.ts:236-242
//
// 生命周期持有者在卸掉一个驱动之前走这条路；之后要再推起来，得由人重新授权一次
// [Service.Resume]，那一次才会记下新的活化边沿。
func (s *Service) Disarm(owner agent.Agent) (*View, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	entry, err := s.prepare(owner)
	if err != nil {
		return nil, err
	}
	entry.activation = Disarmed
	return s.view(entry), nil
}

// ---- 改 ----

// Create 建一个目标并且当场点亮它。
//
// 源: packages/goal/goal/src/index.ts:251-267
//
// 只有「一个都没有」和「上一个已经完成」这两种局面准建；别的阶段要么先
// [Service.Clear]，要么 [Service.Resume]。
func (s *Service) Create(owner agent.Agent, request CreateRequest) (*View, error) {
	spec, err := resolveCreateGoal(request, s.defaultMaxGoalRounds)
	if err != nil {
		return nil, err
	}
	changed, err := s.mutate(owner, func(entry *cache) (Change, Activation, error) {
		if current := entry.state.Goal; current != nil && current.Phase != PhaseComplete {
			return Change{}, "", newError(
				CodeAlreadyExists,
				"goal %q already exists with phase %q", current.ID, current.Phase,
			)
		}
		now := s.now().UnixMilli()
		return Change{
			Version:   ChangeVersion,
			Operation: OpCreate,
			Goal: Snapshot{
				Ref:           Ref{ID: ID("goal-" + uuid.NewString()), Revision: 1},
				Objective:     spec.objective,
				Phase:         PhaseActive,
				MaxGoalRounds: spec.maxGoalRounds,
			},
			RoundsStarted: 0,
			CreatedAt:     now,
			UpdatedAt:     now,
		}, Armed, nil
	})
	if err != nil {
		return nil, err
	}
	// 一次快照改动一定装上了当前目标：严格回放不放过任何别的结局，装不上的话
	// 上面那次 commit 早就报错了。
	return changed.Goal, nil
}

// Edit 换目标描述和／或轮数上限，一点都不碰阶段。
//
// 源: packages/goal/goal/src/index.ts:276-290
//
// 活化跟着原样留下：改一句描述不该把一个正在推进的目标停下，也不该把一个停住的
// 目标推起来。
func (s *Service) Edit(owner agent.Agent, ref Ref, request EditRequest) (*View, error) {
	changed, err := s.mutate(owner, func(entry *cache) (Change, Activation, error) {
		current, err := expectCurrent(entry, ref)
		if err != nil {
			return Change{}, "", err
		}
		if request.Objective == nil && request.MaxGoalRounds == nil {
			return Change{}, "", newError(
				CodeInvalidEdit, "goal edit requires objective and/or maxGoalRounds",
			)
		}
		next := current.detached()
		next.Revision = current.Revision + 1
		if request.Objective != nil {
			objective, err := resolveObjective(*request.Objective)
			if err != nil {
				return Change{}, "", err
			}
			next.Objective = objective
		}
		if request.MaxGoalRounds != nil {
			rounds, err := resolveMaxGoalRounds(*request.MaxGoalRounds)
			if err != nil {
				return Change{}, "", err
			}
			next.MaxGoalRounds = rounds
		}
		return s.snapshotChange(entry, OpEdit, next), entry.activation, nil
	})
	if err != nil {
		return nil, err
	}
	return changed.Goal, nil
}

// Pause 把一个 active 的目标停下，并且收回续推资格。
//
// 源: packages/goal/goal/src/index.ts:299-301
func (s *Service) Pause(owner agent.Agent, ref Ref) (*View, error) {
	return s.transition(owner, ref, OpPause, []Phase{PhaseActive}, PhasePaused, Disarmed)
}

// Resume 把一个停住的目标重新推起来，或者在一次会话起跑边沿之后把 active 的那个
// 重新点亮。
//
// 源: packages/goal/goal/src/index.ts:311-328
//
// 「已经 active 而且已经点亮」被当场拒掉：那说明调用方以为自己在恢复什么，其实
// 什么都没发生，而它还会白白吃掉一个修订号。
func (s *Service) Resume(owner agent.Agent, ref Ref) (*View, error) {
	changed, err := s.mutate(owner, func(entry *cache) (Change, Activation, error) {
		current, err := expectCurrent(entry, ref)
		if err != nil {
			return Change{}, "", err
		}
		resumable := []Phase{PhaseActive, PhasePaused, PhaseBlocked}
		if !allows(resumable, current.Phase) {
			return Change{}, "", transitionError(current, OpResume, resumable)
		}
		if current.Phase == PhaseActive && entry.activation == Armed {
			return Change{}, "", newError(
				CodeInvalidTransition, "goal %q is already active and armed", current.ID,
			)
		}
		if entry.state.RoundsStarted >= current.MaxGoalRounds {
			return Change{}, "", newError(
				CodeInvalidTransition,
				"goal %q exhausted %d goal rounds; increase maxGoalRounds before resuming",
				current.ID, current.MaxGoalRounds,
			)
		}
		return s.snapshotChange(entry, OpResume, withPhase(current, PhaseActive)), Armed, nil
	})
	if err != nil {
		return nil, err
	}
	return changed.Goal, nil
}

// Complete 把当前这个没完成的目标标记为完成，并且收回续推资格。
//
// 源: packages/goal/goal/src/index.ts:337-346
func (s *Service) Complete(owner agent.Agent, ref Ref) (*View, error) {
	return s.transition(
		owner, ref, OpComplete,
		[]Phase{PhaseActive, PhasePaused, PhaseBlocked}, PhaseComplete, Disarmed,
	)
}

// Block 把一个 active 的目标标记为撞墙，并且收回续推资格。
//
// 源: packages/goal/goal/src/index.ts:355-368
//
// reason 由拦下它的那一方给：码用来路由，那句话给人和模型看。
func (s *Service) Block(owner agent.Agent, ref Ref, reason BlockReason) (*View, error) {
	changed, err := s.mutate(owner, func(entry *cache) (Change, Activation, error) {
		current, err := expectCurrent(entry, ref)
		if err != nil {
			return Change{}, "", err
		}
		if current.Phase != PhaseActive {
			return Change{}, "", transitionError(current, OpBlock, []Phase{PhaseActive})
		}
		resolved, err := resolveBlockReason(reason)
		if err != nil {
			return Change{}, "", err
		}
		blocked := withPhase(current, PhaseBlocked)
		blocked.BlockedReason = &resolved
		return s.snapshotChange(entry, OpBlock, blocked), Disarmed, nil
	})
	if err != nil {
		return nil, err
	}
	return changed.Goal, nil
}

// Clear 清掉当前目标，留下一块带修订号的墓碑。
//
// 源: packages/goal/goal/src/index.ts:377-390
//
// 交回的那个 ref 的修订号比被清掉的那一份大一：历史因此接得上，而同一个 id 再也
// 建不出第二个（见 [ApplyChange] 里的 seenGoalIDs）。
func (s *Service) Clear(owner agent.Agent, ref Ref) (Ref, error) {
	changed, err := s.mutate(owner, func(entry *cache) (Change, Activation, error) {
		current, err := expectCurrent(entry, ref)
		if err != nil {
			return Change{}, "", err
		}
		return Change{
			Version:   ChangeVersion,
			Operation: OpClear,
			Cleared:   Ref{ID: current.ID, Revision: current.Revision + 1},
			ClearedAt: s.nextMutationTime(entry),
		}, Disarmed, nil
	})
	if err != nil {
		return Ref{}, err
	}
	return changed.Ref, nil
}

// ---- 内部 ----

// mutate 是每一次改动共用的那条路：上锁、同步、让调用方拼出改动、提交，然后**出锁**
// 再通告。
//
// 新增: DSH 在 commit 的末尾同步地发那条通告。这里挪到锁外，理由见 [Service]
// 的注释：一个观察者回头读一次目标是合理用法，在锁内发会把它变成一次自锁。
func (s *Service) mutate(
	owner agent.Agent,
	build func(entry *cache) (Change, Activation, error),
) (Changed, error) {
	s.mutex.Lock()
	changed, err := s.mutateLocked(owner, build)
	s.mutex.Unlock()
	if err != nil {
		return Changed{}, err
	}
	s.emitChanged(owner, changed)
	return changed, nil
}

// mutateLocked 是 [Service.mutate] 拿着锁的那一半。
func (s *Service) mutateLocked(
	owner agent.Agent,
	build func(entry *cache) (Change, Activation, error),
) (Changed, error) {
	entry, err := s.prepare(owner)
	if err != nil {
		return Changed{}, err
	}
	change, activation, err := build(entry)
	if err != nil {
		return Changed{}, err
	}
	return s.commit(owner, entry, change, activation)
}

// transition 是那两个「只换阶段」的动词共用的那一段。
//
// 源: packages/goal/goal/src/index.ts:461-473
func (s *Service) transition(
	owner agent.Agent,
	ref Ref,
	operation Operation,
	allowed []Phase,
	phase Phase,
	activation Activation,
) (*View, error) {
	changed, err := s.mutate(owner, func(entry *cache) (Change, Activation, error) {
		current, err := expectCurrent(entry, ref)
		if err != nil {
			return Change{}, "", err
		}
		if !allows(allowed, current.Phase) {
			return Change{}, "", transitionError(current, operation, allowed)
		}
		return s.snapshotChange(entry, operation, withPhase(current, phase)), activation, nil
	})
	if err != nil {
		return nil, err
	}
	return changed.Goal, nil
}

// prepare 解算并同步这次调用要用的那份缓存；调用方持有锁。
//
// 源: packages/goal/goal/src/index.ts:393-398
func (s *Service) prepare(owner agent.Agent) (*cache, error) {
	if err := s.assertLive(owner); err != nil {
		return nil, err
	}
	entry, err := s.cacheFor(owner.Session())
	if err != nil {
		return nil, err
	}
	if err := s.sync(owner.Session(), entry); err != nil {
		return nil, err
	}
	return entry, nil
}

// assertLive 要求手里这个 agent **就是**注册表里活着的那一个实例。
//
// 源: packages/goal/goal/src/index.ts:414-418
//
// 比对象而不是比标识：同一个会话被重新开起来之后，旧的那个 agent 标识照样对得上，
// 而往它身上写目标等于往一段已经不归它管的会话里写东西。
func (s *Service) assertLive(owner agent.Agent) error {
	if owner == nil {
		return newError(CodeAgentNotLive, "goal operations require an agent")
	}
	current, present := s.agents.Get(owner.ID())
	if !present || current != owner {
		return newError(CodeAgentNotLive, "agent %q is not live in this registry", owner.ID())
	}
	return nil
}

// cacheFor 取一个会话那份缓存，第一次见到就把整条日志折一遍；调用方持有锁。
//
// 源: packages/goal/goal/src/index.ts:421-434
//
// 新建的缓存活化一律是 [Disarmed]，哪怕折出来的阶段是 active：进程局部的续推
// 资格一次都不从日志里恢复。
func (s *Service) cacheFor(owned *coresession.Session) (*cache, error) {
	handle := weak.Make(owned)
	if existing, present := s.caches[handle]; present {
		return existing, nil
	}
	// 顺手把已经被回收的那些键扫掉。只在建新缓存时扫：读路径上一条都不建，
	// 每次调用都全表走一遍不值。
	for key := range s.caches {
		if key != handle && key.Value() == nil {
			delete(s.caches, key)
		}
	}
	events := owned.Events()
	state := EmptyFoldState()
	for _, event := range events {
		if err := ApplyEvent(state, event); err != nil {
			return nil, err
		}
	}
	created := &cache{state: state, activation: Disarmed, observedSeq: len(events)}
	s.caches[handle] = created
	return created, nil
}

// sync 把日志里还没看过的那一段吃进缓存，顺带定下活化；调用方持有锁。
//
// 源: packages/goal/goal/src/index.ts:437-447
//
// 那条「不是我写的 goal/change 一律打回 disarmed」的规矩是承重的：一次分叉、一次
// 外部工具写下的改动、一段被别的进程续上的日志，都不该让这个进程自己动起来。
// 认领自己那一条靠 [pendingActivation]。
func (s *Service) sync(owned *coresession.Session, entry *cache) error {
	events := owned.Events()
	for _, event := range events[entry.observedSeq:] {
		if err := ApplyEvent(entry.state, event); err != nil {
			return err
		}
		if event.Type == EventChange {
			entry.activation = Disarmed
			if entry.pending != nil && entry.pending.seq == event.Seq {
				entry.activation = entry.pending.activation
			}
		}
		entry.observedSeq++
	}
	return nil
}

// commit 把一次改动落进日志、同步进缓存，并拼出那条通告；调用方持有锁。
//
// 源: packages/goal/goal/src/index.ts:542-558
//
// pendingActivation 在追加**之前**就摆好：那一条事件会在这次追加里同步地发给
// 会话的观察者，而它们中的任何一个回头读一次目标都会走到 [Service.sync]。
// 摆晚了，那一次读看到的就是一个被误判成「别人写的」而打回 disarmed 的目标。
func (s *Service) commit(
	owner agent.Agent,
	entry *cache,
	change Change,
	activation Activation,
) (Changed, error) {
	data, err := change.MarshalJSON()
	if err != nil {
		return Changed{}, err
	}
	owned := owner.Session()
	entry.pending = &pendingActivation{seq: owned.Seq(), activation: activation}
	_, appendErr := owned.Append(session.Event{Type: EventChange, Data: data})
	if appendErr == nil {
		err = s.sync(owned, entry)
	}
	entry.pending = nil
	if appendErr != nil {
		return Changed{}, fmt.Errorf("goal: 写 goal/change 失败：%w", appendErr)
	}
	if err != nil {
		return Changed{}, err
	}
	return Changed{Operation: change.Operation, Ref: ChangeRef(change), Goal: s.view(entry)}, nil
}

// view 拼一份脱手的当前视图；没有当前目标就交回 nil。调用方持有锁。
//
// 源: packages/goal/goal/src/index.ts:561-577
func (s *Service) view(entry *cache) *View {
	if entry.state.Goal == nil {
		return nil
	}
	return &View{
		Snapshot:      entry.state.Goal.detached(),
		RoundsStarted: entry.state.RoundsStarted,
		CreatedAt:     entry.state.CreatedAt,
		UpdatedAt:     entry.state.UpdatedAt,
		Activation:    entry.activation,
	}
}

// snapshotChange 拼一次保住当前目标那几个派生量的快照改动；调用方持有锁。
//
// 源: packages/goal/goal/src/index.ts:484-539
func (s *Service) snapshotChange(entry *cache, operation Operation, goal Snapshot) Change {
	return Change{
		Version:       ChangeVersion,
		Operation:     operation,
		Goal:          goal,
		RoundsStarted: entry.state.RoundsStarted,
		CreatedAt:     entry.state.CreatedAt,
		UpdatedAt:     s.nextMutationTime(entry),
	}
}

// nextMutationTime 夹住墙上时钟往回走的那一段。
//
// 源: packages/goal/goal/src/index.ts:507-512
//
// 严格回放要求 updatedAt 不早于上一次（见 [validateSnapshotTransition]），所以
// 一次时钟回拨如果直接照抄，写下的那条改动会当场破掉本包自己的不变量。
func (s *Service) nextMutationTime(entry *cache) int64 {
	return max(s.now().UnixMilli(), entry.state.UpdatedAt)
}

// emitChanged 按载体作用域的父链把观察者收齐，全局层在前，然后逐个叫。
//
// 源: packages/goal/goal/src/index.ts:557
func (s *Service) emitChanged(owner agent.Agent, change Changed) {
	var key *scope.Key
	if ownerScope := owner.Scope(); ownerScope != nil {
		key = ownerScope.Key()
	}
	var observers []ChangedObserver
	for observer := range s.layers.Global().changed.Values() {
		observers = append(observers, observer)
	}
	if key != nil {
		for _, layer := range s.layers.ChainLayers(key) {
			for observer := range layer.changed.Values() {
				observers = append(observers, observer)
			}
		}
	}
	for _, observer := range observers {
		s.contain(owner, change, observer)
	}
}

// contain 跑一个观察者，把它的 panic 兜住记下来。
//
// 一条改动已经落进日志了：一个观察者炸掉不该把它撤回去，也不该饿着排在它后面的
// 同侪。
func (s *Service) contain(owner agent.Agent, change Changed, observer ChangedObserver) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.logger.Warn("goal: goal/changed 观察者 panic 了", "panic", recovered)
		}
	}()
	observer(owner, change)
}

// ---- 纯函数 ----

// expectCurrent 拿一个调用方交来的 ref 认领当前目标，过时或者没有就拒掉。
//
// 源: packages/goal/goal/src/index.ts:401-411
func expectCurrent(entry *cache, ref Ref) (*Snapshot, error) {
	current := entry.state.Goal
	if current == nil {
		return nil, newError(CodeNotFound, "no current goal")
	}
	if ref.ID != current.ID || ref.Revision != current.Revision {
		return nil, newError(
			CodeStaleRevision,
			"stale goal ref %q revision %d; current is %q revision %d",
			ref.ID, ref.Revision, current.ID, current.Revision,
		)
	}
	return current, nil
}

// withPhase 拼一个只换了阶段的下一个修订。
//
// 源: packages/goal/goal/src/index.ts:450-458
//
// 阻塞原因**不**带过来：只有 block 会重新装上一份，别的动词一律把它抹掉，
// 这正是「不是 blocked 就不许带 blockedReason」那条不变量在写这一侧的兑现。
func withPhase(current *Snapshot, phase Phase) Snapshot {
	return Snapshot{
		Ref:           Ref{ID: current.ID, Revision: current.Revision + 1},
		Objective:     current.Objective,
		Phase:         phase,
		MaxGoalRounds: current.MaxGoalRounds,
	}
}

// allows 问一个阶段在不在准许的那几个里。
func allows(allowed []Phase, phase Phase) bool {
	for _, each := range allowed {
		if each == phase {
			return true
		}
	}
	return false
}

// transitionError 拼那句稳定的「这次跃迁不成立」。
//
// 源: packages/goal/goal/src/index.ts:476-481
func transitionError(current *Snapshot, operation Operation, allowed []Phase) *Error {
	names := make([]string, 0, len(allowed))
	for _, phase := range allowed {
		names = append(names, string(phase))
	}
	return newError(
		CodeInvalidTransition,
		"cannot %s goal %q from phase %q; expected %s",
		operation, current.ID, current.Phase, strings.Join(names, " or "),
	)
}

// resolvedCreate 是一次验过、而且部署方默认值都已经落实了的 create 入参。
//
// 源: packages/goal/goal/src/index.ts:136-139
type resolvedCreate struct {
	objective     string
	maxGoalRounds int
}

// resolveCreateGoal 落实默认值并验一次 create 入参。
//
// 源: packages/goal/goal/src/index.ts:158-163
func resolveCreateGoal(request CreateRequest, defaultMaxGoalRounds int) (resolvedCreate, error) {
	objective, err := resolveObjective(request.Objective)
	if err != nil {
		return resolvedCreate{}, err
	}
	rounds := defaultMaxGoalRounds
	if request.MaxGoalRounds != nil {
		rounds = *request.MaxGoalRounds
	}
	maxGoalRounds, err := resolveMaxGoalRounds(rounds)
	if err != nil {
		return resolvedCreate{}, err
	}
	return resolvedCreate{objective: objective, maxGoalRounds: maxGoalRounds}, nil
}

// resolveMaxGoalRounds 验一个调用方看得见的轮数上限。
//
// 源: packages/goal/goal/src/index.ts:142-147
func resolveMaxGoalRounds(value int) (int, error) {
	if value < 1 || int64(value) > maxSafeInteger {
		return 0, newError(CodeInvalidMaxRounds, "maxGoalRounds must be a positive safe integer")
	}
	return value, nil
}

// resolveObjective 在域边界上验并规范化一句目标描述。
//
// 源: packages/goal/goal/src/index.ts:150-155
func resolveObjective(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", newError(CodeInvalidObjective, "goal objective must be a non-empty string")
	}
	return trimmed, nil
}

// resolveBlockReason 验并脱手一份由拦阻方给出的解释。
//
// 源: packages/goal/goal/src/index.ts:166-180
func resolveBlockReason(reason BlockReason) (BlockReason, error) {
	message := strings.TrimSpace(reason.Message)
	if !blockCodePattern.MatchString(reason.Code) || message == "" {
		return BlockReason{}, newError(
			CodeInvalidBlockReason,
			"goal block reason requires a lower-kebab-case code and a non-empty message",
		)
	}
	return BlockReason{Code: reason.Code, Message: message}, nil
}
