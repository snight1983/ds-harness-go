// 本文件的作用：那张包内私有的登记表——每一个可续孩子还没公布的创建窗口里，
// 部署方要组装进去的那些能力。
//
// 源: packages/subagent/subagent/src/activation-setup-registry.ts
//
// 一份贡献给出一样归孩子作用域所有的能力，而不必教会续接管理器「都有哪些能力」。
// 管理器拥有驻留；这张表拥有「插件寿命 — 未公布的装配 — 活化处置」这三者之间的
// 那道接缝，于是没有哪一次安装能活得比它两个主人中的任何一个更久，也没有哪一份
// 已经被摘掉的贡献能在撤销报完成之后还装得进去。

package subagent

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"ds-harness-go/core/scope"
)

// ActivationSetupContribution 是往一个可续孩子还没公布的创建窗口里装的一样部署方
// 能力。它在公布之前同步组装，并交回**恰好这一次安装**的释放函数。
//
// 源: packages/subagent/subagent/src/activation-setup-registry.ts:26
//
// 交回 nil 的释放函数表示这次安装没有要释放的东西，是合法的。
//
// 新增: DSH 的签名是 `(childCtx) => () => void`，失败靠抛。Go 里失败是第二个返回值，
// 取消是第一个参数，释放函数也跟着本仓库的 disposer 形状收 ctx、交 error。
type ActivationSetupContribution func(
	ctx context.Context,
	childScope *scope.Scope,
) (release func(context.Context) error, err error)

// setupRegistration 是一份贡献活着的那次登记。
//
// 源: packages/subagent/subagent/src/activation-setup-registry.ts:29-33
type setupRegistration struct {
	contribution ActivationSetupContribution
	// removed 与 installations 都归 [ActivationSetupRegistry.mutex] 管。
	removed       bool
	installations map[*setupInstallation]struct{}
}

// setupInstallation 是一份贡献装进一个孩子上下文的那一次安装。
//
// 源: packages/subagent/subagent/src/activation-setup-registry.ts:36-43
type setupInstallation struct {
	registration *setupRegistration
	childScope   *scope.Scope
	release      func(context.Context) error
	// released 与 transaction 都归 [ActivationSetupRegistry.mutex] 管。
	released bool
	// transaction 在这个孩子驻留下来之前一直在；提交之后置 nil。
	transaction *setupTransaction
}

// setupTransaction 是一个孩子这一批装配。
//
// 源: packages/subagent/subagent/src/activation-setup-registry.ts:46-49
type setupTransaction struct {
	installations []*setupInstallation
	invalidated   bool
}

// ActivationSetupRegistry 拥有可续孩子的装配登记、安装、回滚、孩子清理，以及
// 对活着的登记立即撤销这几件事。
//
// 源: packages/subagent/subagent/src/activation-setup-registry.ts:60-183
type ActivationSetupRegistry struct {
	// registrations 是活着的那些贡献，按安装顺序。
	//
	// 新增: DSH 用一个 Set 表达「按插入顺序、可单独删掉一项」。Go 这边
	// [ds-harness-go/core/scope.AnonymousEntries] 正是这样东西，而且它的 Values
	// 本来就是快照语义——DSH 那句 `[...this.registrations]` 拷贝一份再遍历，
	// 为的就是同一件事（遍历中途有人撤销不能把这次遍历弄坏）。
	registrations *scope.AnonymousEntries[*setupRegistration]

	// mutex 管下面这张索引，以及登记与安装上那几个可变字段。
	//
	// 新增: DSH 是单线程的，一把锁都不需要。Go 里这张表被并发地起孩子的那几条
	// 路共用，所以簿记要上锁。**用户给的函数一律在锁外调**——贡献自己、以及释放
	// 函数——否则一次同步的重入（DSH 专门处理过的那种「装到一半自己把自己撤了」）
	// 就会死锁。
	mutex sync.Mutex
	// byChild 是孩子作用域到它那些活着的安装。
	byChild map[*scope.Scope]map[*setupInstallation]struct{}
}

// NewActivationSetupRegistry 造一张空表。
func NewActivationSetupRegistry() *ActivationSetupRegistry {
	return &ActivationSetupRegistry{
		registrations: scope.NewAnonymousEntries[*setupRegistration](),
		byChild:       map[*scope.Scope]map[*setupInstallation]struct{}{},
	}
}

// Register 登记一份贡献，返回一个幂等的撤销函数。
//
// 源: packages/subagent/subagent/src/activation-setup-registry.ts:72-83
//
// 撤销会把这份贡献已经装出去的每一次安装都试着释放一遍，全部试完之后才报错；
// 有任何一次释放失败就报 [CodeActivationSetupReleaseFailed]。
func (r *ActivationSetupRegistry) Register(
	contribution ActivationSetupContribution,
) (func(context.Context) error, error) {
	if contribution == nil {
		return nil, fmt.Errorf("%w：可续子 agent 的装配贡献不能是 nil", ErrInvalidRequest)
	}
	registration := &setupRegistration{
		contribution:  contribution,
		installations: map[*setupInstallation]struct{}{},
	}
	remove := r.registrations.Append(registration)
	var once sync.Once
	var undoErr error
	return func(ctx context.Context) error {
		once.Do(func() {
			r.mutex.Lock()
			if registration.removed {
				r.mutex.Unlock()
				return
			}
			// 先关门再处置：否则一次已经取过快照的 Apply 还能在「撤销报完成」
			// 之后把它装进去。
			registration.removed = true
			pending := make([]*setupInstallation, 0, len(registration.installations))
			for installation := range registration.installations {
				pending = append(pending, installation)
			}
			r.mutex.Unlock()
			remove()
			undoErr = r.releaseAll(ctx, pending, "摘掉贡献")
		})
		return undoErr
	}, nil
}

// Apply 把每一份活着的贡献装进一个还没公布的孩子作用域，交回公布 agent 时消费的
// 那个提交函数。
//
// 源: packages/subagent/subagent/src/activation-setup-registry.ts:90-139
//
// 新增: 它的签名**恰好**就是 [ds-harness-go/core/agent.Setup]，所以这个方法值可以
// 直接当成 CreateOptions.Setup 交出去，不需要中间再包一层。
func (r *ActivationSetupRegistry) Apply(
	ctx context.Context,
	childScope *scope.Scope,
) (func() error, error) {
	state := &setupTransaction{}
	for registration := range r.registrations.Values() {
		if r.isRemoved(registration) {
			// 只有「一份已经取进快照的登记被同步地重入撤销」才到得了这道闸。
			continue
		}
		release, err := registration.contribution(ctx, childScope)
		if err != nil {
			// 让安装方那次失败保持权威，但每一项回滚都要试到。
			_ = r.releaseAll(ctx, r.batch(state), "装配回滚")
			return nil, err
		}
		installation := &setupInstallation{
			registration: registration,
			childScope:   childScope,
			release:      release,
			transaction:  state,
		}
		// 一个安装方可以在它那条安装记录存在之前就把自己撤了。把那条漏网的记录
		// 处置掉，并且把这一批装配作废。
		if escaped := r.record(installation, state); escaped {
			if err := r.releaseOne(ctx, installation); err != nil {
				return nil, err
			}
		}
	}
	// 这个 disposer 有意丢掉：owner 就是孩子自己那个作用域，作用域一处置这项清理
	// 就跑到了，那正是 DSH 那次 childCtx.effect 的语义。
	if _, err := childScope.Defer("subagents.activationSetup()", func(ctx context.Context) error {
		return r.releaseChild(ctx, childScope)
	}); err != nil {
		// 孩子那个作用域已经释放了：这一批清理再也跑不到，所以当场放掉，
		// 别让它们无声无息地泄漏。
		_ = r.releaseAll(ctx, r.batch(state), "登记孩子清理")
		return nil, err
	}
	return func() error {
		r.mutex.Lock()
		defer r.mutex.Unlock()
		if state.invalidated {
			return NewError(
				"这个孩子正在建的时候，一份可续子 agent 装配贡献被撤销了；这个孩子没有立起来",
				CodeActivationSetupRevoked, nil,
			)
		}
		for _, installation := range state.installations {
			installation.transaction = nil
		}
		return nil
	}, nil
}

// isRemoved 在一份贡献可能已经把自己撤掉之后，重新读一次那个可变的撤销状态。
//
// 源: packages/subagent/subagent/src/activation-setup-registry.ts:52-54
func (r *ActivationSetupRegistry) isRemoved(registration *setupRegistration) bool {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return registration.removed
}

// batch 拍下这一批装配到目前为止的那些安装。
func (r *ActivationSetupRegistry) batch(state *setupTransaction) []*setupInstallation {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return append([]*setupInstallation(nil), state.installations...)
}

// record 把一次安装记进两张索引，并交回「这份登记是不是已经被撤掉了」。
func (r *ActivationSetupRegistry) record(installation *setupInstallation, state *setupTransaction) bool {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	installation.registration.installations[installation] = struct{}{}
	state.installations = append(state.installations, installation)
	indexed, found := r.byChild[installation.childScope]
	if !found {
		indexed = map[*setupInstallation]struct{}{}
		r.byChild[installation.childScope] = indexed
	}
	indexed[installation] = struct{}{}
	return installation.registration.removed
}

// releaseChild 放掉一个已经处置的孩子作用域名下剩余的每一次安装。
//
// 源: packages/subagent/subagent/src/activation-setup-registry.ts:142-145
func (r *ActivationSetupRegistry) releaseChild(ctx context.Context, childScope *scope.Scope) error {
	r.mutex.Lock()
	indexed := r.byChild[childScope]
	pending := make([]*setupInstallation, 0, len(indexed))
	for installation := range indexed {
		pending = append(pending, installation)
	}
	r.mutex.Unlock()
	return r.releaseAll(ctx, pending, "处置孩子作用域")
}

// releaseAll 把一批安装**整批放完**之后，才报那些释放失败。
//
// 源: packages/subagent/subagent/src/activation-setup-registry.ts:152-167
//
// 新增: DSH 把每个失败过一遍 errorChain 再用 '; ' 拼成一句话。Go 里
// [errors.Join] 就是这件事，而且拼出来的东西仍旧 [errors.Is] 得出每一个原因
// （成例见 [ds-harness-go/core/agentloop] 那次收尾）。
func (r *ActivationSetupRegistry) releaseAll(
	ctx context.Context,
	installations []*setupInstallation,
	during string,
) error {
	var failures []error
	for _, installation := range installations {
		if err := r.releaseOne(ctx, installation); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) == 0 {
		return nil
	}
	return NewError(
		fmt.Sprintf("可续子 agent 装配在%s时有 %d 项没能释放", during, len(failures)),
		CodeActivationSetupReleaseFailed, errors.Join(failures...),
	)
}

// releaseOne 把一次安装从两张索引上摘掉，并且**恰好处置一次**。
//
// 源: packages/subagent/subagent/src/activation-setup-registry.ts:170-182
//
// 「认领」这一下（判 released、置位、退索引、作废这一批）在锁里原子地做完，
// 那个释放函数本身在锁外调：它是外面给的代码，一次重入不该把这张表锁死。
func (r *ActivationSetupRegistry) releaseOne(ctx context.Context, installation *setupInstallation) error {
	r.mutex.Lock()
	if installation.released {
		r.mutex.Unlock()
		return nil
	}
	installation.released = true
	delete(installation.registration.installations, installation)
	if indexed, found := r.byChild[installation.childScope]; found {
		delete(indexed, installation)
		if len(indexed) == 0 {
			delete(r.byChild, installation.childScope)
		}
	}
	if installation.transaction != nil {
		installation.transaction.invalidated = true
	}
	release := installation.release
	r.mutex.Unlock()
	if release == nil {
		return nil
	}
	return release(ctx)
}
