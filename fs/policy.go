// 本文件是这条接缝上的三个策略通道：写前的守卫决定、编辑前的守卫决定、
// 以及一次权威观察的登记。
//
// 源: packages/fs/fs/src/index.ts:44-78

package fs

import (
	"context"
	"sync"

	"ds-harness-go/invariants"
)

// WriteIntentDecider 为**下一次** [FileSystem.WriteText] 决定带什么守卫。
//
// 源: packages/fs/fs/src/index.ts:50-58
//
// 返回 nil 表示「这次决定不归我」，把机会让给后面登记的决定方。
// 一个都没给出答案时，这次写是无条件的（见 [Policy.DecideWriteIntent]）。
//
// actor 是工具执行上下文，对这条接缝完全不透明——决定方拿它当键去查
// 「这个执行体观察过这个目标吗」。
//
// 新增: DSH 那边它的类型是 `object | undefined`。Go 没有「对象」这个类型，
// 用 any；nil 对应 undefined，表示没有可归属的执行体。
type WriteIntentDecider func(ctx context.Context, target Target, actor any) (WriteIntent, error)

// EditIntentDecider 为**下一次** [FileSystem.EditText] 决定带什么版本守卫，
// 条款同 [WriteIntentDecider]。
//
// 源: packages/fs/fs/src/index.ts:59-66
type EditIntentDecider func(ctx context.Context, target Target, actor any) (*EditIntent, error)

// ObservationListener 登记一次权威的在场或不在场观察。
//
// 源: packages/fs/fs/src/index.ts:67-76
//
// **它必须是同步的、并且真的把这件事记下来**：返回错误会让这次工具调用失败。
// 这一条和 credentials 包里那个「订阅者炸了只记日志」正好相反，是有理由的——
// 那边通知的是一件**已经提交**的事，收不到的人自己不一致；
// 这边登记的是一次观察，而后面那次带守卫的写会去查这条登记。
// 登记失败却让调用继续的话，那次写会以 [CodeNotObserved] 被拒，
// 而现场早就过去了，没有人还答得上来它为什么没被观察到。
//
// actor 为 nil 时这次登记记不下任何有用的东西（没有可归属的执行体），
// 但它仍然是合法的：一次没有归属的观察也是一次真实发生过的观察。
type ObservationListener func(target Target, observation Observation, actor any) error

// Policy 是那三张订阅表加上它们的分发规则。
//
// 源: packages/fs/fs/src/index.ts:44-78
//
// 新增: DSH 那边这三件事是 cordis 上声明的事件，由容器分发；
// 两个决定用 waterfall 模式，观察用 emit 模式。Go 没有那个容器，
// 于是这张表和这套规则落在本包的一个具体类型上——和 credentials 包里的
// Notifier 是同一个做法，理由也一样：订阅表是**状态**，接口存不下状态。
//
// 零值可用。
type Policy struct {
	// mutex 保护下面所有字段。
	//
	// 新增: DSH 是单线程 JS，订阅表不需要任何并发保护。Go 里订阅、退订、
	// 分发会来自不同的 goroutine，所以这一层是 Go 侧的必需品。
	mutex  sync.Mutex
	nextID uint64

	writeDeciders []writeDeciderSubscription
	editDeciders  []editDeciderSubscription
	observers     []observerSubscription

	// fail 是不变量检查的入口；nil 表示这条检查没装（见 [RegisterInvariants]）。
	//
	// 一个注册表里同一个包名只能预留一次，所以这里只需要一个槽。
	fail invariants.Fail
}

type writeDeciderSubscription struct {
	id     uint64
	decide WriteIntentDecider
}

type editDeciderSubscription struct {
	id     uint64
	decide EditIntentDecider
}

type observerSubscription struct {
	id     uint64
	listen ObservationListener
}

// SubscribeWriteIntent 登记一个写守卫决定方，返回退订函数。
//
// 源: packages/fs/fs/src/index.ts:50-58
//
// 新增: 订阅按登记顺序存在切片里而不是 map 里。Go 的 map 迭代顺序是随机的，
// 而这里「谁先被问到」直接决定了结果（第一个给出答案的人拥有这次决定），
// 用 map 会让同一份装配每次跑出不同的守卫。
func (p *Policy) SubscribeWriteIntent(decide WriteIntentDecider) func() {
	if decide == nil {
		return func() {}
	}

	p.mutex.Lock()
	id := p.nextID
	p.nextID++
	p.writeDeciders = append(p.writeDeciders, writeDeciderSubscription{id: id, decide: decide})
	p.mutex.Unlock()

	return func() {
		p.mutex.Lock()
		defer p.mutex.Unlock()
		for index, subscription := range p.writeDeciders {
			if subscription.id == id {
				p.writeDeciders = append(p.writeDeciders[:index:index], p.writeDeciders[index+1:]...)
				return
			}
		}
	}
}

// SubscribeEditIntent 登记一个编辑守卫决定方，条款同 [Policy.SubscribeWriteIntent]。
//
// 源: packages/fs/fs/src/index.ts:59-66
func (p *Policy) SubscribeEditIntent(decide EditIntentDecider) func() {
	if decide == nil {
		return func() {}
	}

	p.mutex.Lock()
	id := p.nextID
	p.nextID++
	p.editDeciders = append(p.editDeciders, editDeciderSubscription{id: id, decide: decide})
	p.mutex.Unlock()

	return func() {
		p.mutex.Lock()
		defer p.mutex.Unlock()
		for index, subscription := range p.editDeciders {
			if subscription.id == id {
				p.editDeciders = append(p.editDeciders[:index:index], p.editDeciders[index+1:]...)
				return
			}
		}
	}
}

// SubscribeObserved 登记一个观察记录方，条款同 [Policy.SubscribeWriteIntent]。
//
// 源: packages/fs/fs/src/index.ts:67-76
func (p *Policy) SubscribeObserved(listen ObservationListener) func() {
	if listen == nil {
		return func() {}
	}

	p.mutex.Lock()
	id := p.nextID
	p.nextID++
	p.observers = append(p.observers, observerSubscription{id: id, listen: listen})
	p.mutex.Unlock()

	return func() {
		p.mutex.Lock()
		defer p.mutex.Unlock()
		for index, subscription := range p.observers {
			if subscription.id == id {
				p.observers = append(p.observers[:index:index], p.observers[index+1:]...)
				return
			}
		}
	}
}

// DecideWriteIntent 问出下一次写该带的守卫；返回 nil 表示无条件写。
//
// 源: packages/fs/fs/src/index.ts:50-58
//
// **这是一个单槽决定，不是一次合成**：按登记顺序问下去，第一个给出非 nil 答案的
// 拥有这次决定，后面的不再被问到。合成是没有意义的——两个守卫合起来
// 不构成第三个守卫，它们只会互相覆盖。
//
// 任何一个决定方报错就地中止，**不退回无条件写**。这一条不留余地：
// 报错的那个可能正是本该给出守卫的人，接着往下问等于把一次该带守卫的写
// 悄悄变成了无条件的——而无条件的那次会成功，然后覆盖掉别人刚写的内容。
func (p *Policy) DecideWriteIntent(ctx context.Context, target Target, actor any) (WriteIntent, error) {
	p.checkTarget(target)

	p.mutex.Lock()
	deciders := make([]WriteIntentDecider, 0, len(p.writeDeciders))
	for _, subscription := range p.writeDeciders {
		deciders = append(deciders, subscription.decide)
	}
	p.mutex.Unlock()

	for _, decide := range deciders {
		intent, err := decide(ctx, target, actor)
		if err != nil {
			return nil, err
		}
		if intent != nil {
			return intent, nil
		}
	}
	return nil, nil
}

// DecideEditIntent 问出下一次编辑该带的版本守卫；返回 nil 表示无条件编辑。
//
// 源: packages/fs/fs/src/index.ts:59-66
//
// 条款和 [Policy.DecideWriteIntent] 逐条相同。
func (p *Policy) DecideEditIntent(ctx context.Context, target Target, actor any) (*EditIntent, error) {
	p.checkTarget(target)

	p.mutex.Lock()
	deciders := make([]EditIntentDecider, 0, len(p.editDeciders))
	for _, subscription := range p.editDeciders {
		deciders = append(deciders, subscription.decide)
	}
	p.mutex.Unlock()

	for _, decide := range deciders {
		intent, err := decide(ctx, target, actor)
		if err != nil {
			return nil, err
		}
		if intent != nil {
			return intent, nil
		}
	}
	return nil, nil
}

// NotifyObserved 把一次权威观察分发给每一个记录方。
//
// 源: packages/fs/fs/src/index.ts:67-76
//
// 和两个决定不同，这里是**扇出**：每一个记录方都会被调到，即使前面某一个报了错。
// 理由和 credentials 包里 fanOut 的第一条一样——这次观察是一件已经发生的事实，
// 一个记录方失败不该让其余几个从此缺一条记录，而它们永远不会知道自己缺了。
//
// 返回的是第一个错误。**它必须被调用方接住并让这次工具调用失败**，
// 理由见 [ObservationListener]。
func (p *Policy) NotifyObserved(target Target, observation Observation, actor any) error {
	p.checkTarget(target)
	p.checkObservation(observation)

	p.mutex.Lock()
	listeners := make([]ObservationListener, 0, len(p.observers))
	for _, subscription := range p.observers {
		listeners = append(listeners, subscription.listen)
	}
	p.mutex.Unlock()

	var first error
	for _, listen := range listeners {
		if err := listen(target, observation, actor); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// checkTarget 是这个包自己那条不变量的目标部分；没装检查时是空操作。
//
// 源: packages/fs/fs/src/invariant.ts:14-18
func (p *Policy) checkTarget(target Target) {
	fail := p.currentFail()
	if fail == nil {
		return
	}
	if len(target.TargetKey) == 0 {
		fail("文件系统事件的 targetKey 不能为空")
	}
	if len(target.DisplayPath) == 0 {
		fail("文件系统事件的 displayPath 不能为空")
	}
}

// checkObservation 是这条不变量的观察部分；没装检查时是空操作。
//
// 源: packages/fs/fs/src/invariant.ts:27-38
//
// 新增: DSH 在这里查的第二件事是「kind 必须是 present 或者 absent」。
// Go 侧那一条查不出来也不用查：[Observation] 是封印接口，本包外面造不出
// 第三种实现。取而代之的是一条 DSH 没有的检查——**观察本身不能是 nil**。
// TS 那边 undefined 到不了这里（类型上不允许），Go 的接口值可以是 nil。
func (p *Policy) checkObservation(observation Observation) {
	fail := p.currentFail()
	if fail == nil {
		return
	}
	if observation == nil {
		fail("fs/observed 必须带一次观察，不能是 nil")
		return
	}
	if version, present := observation.PresentVersion(); present && len(version) == 0 {
		fail("fs/observed 的在场观察必须带非空的版本")
	}
}

// currentFail 取出当前装着的检查入口。
func (p *Policy) currentFail() invariants.Fail {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.fail
}
