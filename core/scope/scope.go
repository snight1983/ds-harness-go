// Package scope 提供「作用域」这个原语：一个不透明的身份、一条可嵌套的父子链、
// 一个按这条链路由事件的准入规则，以及一个把注册物的生命周期收在一起的所有权边界。
//
// 对应 DSH 的 @deepseek-ai/dsh-scope（packages/core/scope）。
//
// 源: packages/core/scope/src/index.ts:1-6
//
// # 它解决的问题
//
// 一个进程里同时活着很多个 agent，每个 agent 又可能挂在一个预设（preset）下面。
// 两件事需要按这个层级来决定：
//
//   - **注册物往下继承**：挂在预设上的工具、限制、系统提示片段，它下面每个 agent 都该看得到。
//   - **事件往上传播**：某个 agent 发出的事件，它自己的监听器要收到，它所在预设的监听器
//     也要收到——这正是「一个常驻的编排器能观察到它编排出来的每个 agent」的实现方式。
//
// 这两件事共用**同一条**父子链，方向相反。链本身是这个包的全部内容。
//
// # 一条硬规则：事件只往上走，绝不往下
//
// 挂在预设上的监听器收得到 agent 的事件；挂在 agent 上的监听器**收不到**预设的事件。
// 反过来的话，一个 agent 就会收到它兄弟的事件——而兄弟之间本来就该是互相看不见的。
// 这条方向性由 [Admits] 独家决定，见那里的说明。
//
// # 这里没有照抄的部分
//
// DSH 侧这个包大半篇幅在处理 cordis（它自研的依赖注入 / 插件框架）：Context 上挂标签、
// ctx.plugin() 起一个 fiber、ctx.effect() 登记副作用、Context.filter 这个分派钩子。
// 和 github.com/snight1983/ds-harness-go/invariants 里的判断一致，这些不照搬，换成 Go 里的直接对应物：
//
//   - Context 上的作用域标签 + 派生上下文继承 → [Scope] 自己持有 [Key]，没有隐式继承
//   - ctx.plugin() 起的 fiber              → [Scope] 自己的一摞 teardown，后进先出
//   - ctx.effect() 登记的副作用             → [Scope.Defer]，返回的 disposer 语义相同
//   - Context.filter 分派钩子               → [Carrier] 与 [Admits]，是纯判定函数
//   - WeakMap 侧挂的父链和载体键            → 直接做成字段，见下面 [Key] 的说明
//
// 被保留下来的是**行为**：单次绑定、成环拒绝、链的走法、准入方向、以及释放的幂等与顺序。
//
// # Go 必须自己负责的差异
//
// DSH 是单线程 JS，链和表都不需要并发保护。Go 里这些会被多个 goroutine 同时碰到，
// 所以父链用原子指针读、绑定用一把包级锁串起来（见 [BindParent]），
// [Scope] 和各张表各自带锁。这不是抄来的，是 Go 侧的必需品。
package scope

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// ErrKeyAlreadyBound 表示这个键已经有父作用域了。
//
// 源: packages/core/scope/src/index.ts:73-75
//
// 没有公开的改链路径：一个作用域的归属只能由**当初绑定它的那个人**来改，
// 而那个人手里握着 [BindParent] 返回的 [ParentBinding]。否则任何拿到键的代码
// 都能把一个 agent 从它的预设下面挪走，而链决定了谁看得到谁的事件。
var ErrKeyAlreadyBound = errors.New("scope: 这个作用域键已经绑过父作用域了（改链要用当初绑定时拿到的 ParentBinding）")

// ErrParentCycle 表示这次连接会让父链成环。
//
// 源: packages/core/scope/src/index.ts:56
//
// 每一个用到链的地方都要一路走到根（[ChainOf]、[Admits]、[Layers.ChainLayers]），
// 成了环就是死循环。所以环在**写入**的时候就必须被拦下来。
var ErrParentCycle = errors.New("scope: 这次连接会让作用域父链成环")

// ErrScopeDisposed 表示往一个已经释放的作用域上登记东西。
//
// 新增: DSH 那边由 cordis 的 fiber 状态兜着。Go 这边如果放行，登记进去的清理函数
// 永远不会被跑到——也就是那份资源静默泄漏，且没有任何症状。所以当场报错。
var ErrScopeDisposed = errors.New("scope: 作用域已经释放，不能再往上面登记")

// Key 是一个不透明的作用域身份，**按指针比较**。
//
// 源: packages/core/scope/src/index.ts:14-15（ScopeKey）
//
// 新增: DSH 那边 ScopeKey = object，任意对象都能当键（一个 Agent 常常拿自己当键），
// 因为 JS 的 === 就是身份比较。Go 这边做成一个具体的不透明结构体，用 *Key 传递，理由有二：
//
//   - 用 any 当键的话，两个字段相同的结构体值会**比较相等**，两个本该独立的作用域会串成
//     一个；而切片、map、函数这类不可比较的值塞进 map 会直接 panic。指针没有这两个问题。
//   - 更重要的是父链能直接做成字段。DSH 用 WeakMap 侧挂父链，是因为它没法往一个不属于
//     自己的对象上加字段；Go 这边键是我们自己的类型，就没有这个限制。于是那两张全局
//     WeakMap（父链、载体键）一张都不需要，既没有全局表要加锁，也没有生命周期问题。
//
// label 只用于诊断，**绝不参与身份判定**：两个 NewKey("agent") 是两个不同的作用域。
type Key struct {
	label string

	// parent 是外层作用域，nil 表示这是一条链的根。
	//
	// 用原子指针而不是「一个字段加一把锁」：读这个字段的最热路径是事件分派
	// （每分派一次就要走一遍链），而写只发生在作用域诞生或改链的时候。
	// 原子读没有锁竞争；写的那一侧由 bindMutex 串起来，见 linkParent。
	parent atomic.Pointer[Key]
}

// NewKey 造一个新的作用域身份。label 只用于诊断。
func NewKey(label string) *Key { return &Key{label: label} }

// Label 给出造这个键时写下的诊断名。
func (k *Key) Label() string {
	if k == nil {
		return ""
	}
	return k.label
}

// String 让键在错误信息和日志里可读。
//
// 带上地址是有意的：label 不参与身份判定，两个同名的键必须能在报错里区分开，
// 否则一条「父链成环」的信息会指向两个看起来一模一样的名字。
func (k *Key) String() string {
	if k == nil {
		return "scope.Key(nil)"
	}
	if k.label == "" {
		return fmt.Sprintf("scope.Key(%p)", k)
	}
	return fmt.Sprintf("scope.Key(%s@%p)", k.label, k)
}

// bindMutex 把所有的父链**写入**串起来。
//
// 新增: Go 侧独有。成环检查是「先走一遍链，再写一个字段」，这两步之间如果有别人也在写，
// 两次各自都通过了检查的绑定合起来就能造出一个环。父链的读走原子指针、不碰这把锁，
// 所以热路径（事件分派）不受影响。
var bindMutex sync.Mutex

// linkParent 是绑定和每一次改链共用的那次带成环检查的写入。
//
// 源: packages/core/scope/src/index.ts:53-59
//
// 调用方必须已经持有 bindMutex。
func linkParent(key, parent *Key) error {
	for cursor := parent; cursor != nil; cursor = cursor.parent.Load() {
		if cursor == key {
			return fmt.Errorf("%w：%v ← %v", ErrParentCycle, key, parent)
		}
	}
	key.parent.Store(parent)
	return nil
}

// ParentBinding 是改动某一个键的父链的**特权句柄**。
//
// 源: packages/core/scope/src/index.ts:41-51
//
// 它只由 [BindParent] 交给当初绑定的那一方。没有它就改不了链，于是一个作用域的归属
// 不会被任何拿得到键的代码挪动。
type ParentBinding struct {
	key *Key
}

// Rebind 把绑定的那个键改挂到另一个父作用域下，成环检查和当初绑定时一样。
//
// 源: packages/core/scope/src/index.ts:42-50
//
// 只有在**旧父作用域下产出的东西全都没有被留存**时改链才是合法的。
// 这个关系自己看不见一个会话记了什么，所以这一条由持有者保证，这里不查。
func (b *ParentBinding) Rebind(parent *Key) error {
	if b == nil || b.key == nil {
		return errors.New("scope: 空的 ParentBinding 不能用来改链")
	}
	if parent == nil {
		return errors.New("scope: 改链的目标父作用域不能是 nil")
	}

	bindMutex.Lock()
	defer bindMutex.Unlock()
	return linkParent(b.key, parent)
}

// BindParent 把 parent 绑成 key 的外层作用域，**一次**。
//
// 源: packages/core/scope/src/index.ts:61-82
//
// 已经有父作用域的键会被拒绝（[ErrKeyAlreadyBound]）；会让链成环的连接会被拒绝
// （[ErrParentCycle]），因为每一个用到链的地方都要一路走到根。
//
// 新增: DSH 那边这两种情况都是抛异常。Go 这边返回 error——链的归属是调用方传进来的
// 参数决定的，一个参数错误不该把整个进程 panic 掉。理由同 outputretention 的构造函数。
func BindParent(key, parent *Key) (*ParentBinding, error) {
	if key == nil || parent == nil {
		return nil, errors.New("scope: 绑定父作用域时键和父作用域都不能是 nil")
	}

	bindMutex.Lock()
	defer bindMutex.Unlock()

	if key.parent.Load() != nil {
		return nil, fmt.Errorf("%w：%v", ErrKeyAlreadyBound, key)
	}
	if err := linkParent(key, parent); err != nil {
		return nil, err
	}
	return &ParentBinding{key: key}, nil
}

// ParentOf 读一个键的外层作用域，根作用域返回 nil。
//
// 源: packages/core/scope/src/index.ts:84-91
func ParentOf(key *Key) *Key {
	if key == nil {
		return nil
	}
	return key.parent.Load()
}

// ChainOf 给出从一个键到它的根祖先那条链，**近的在前**：[key, 父, 祖父, …]。
//
// 源: packages/core/scope/src/index.ts:93-102
//
// key 为 nil 时是空链。
func ChainOf(key *Key) []*Key {
	var chain []*Key
	for cursor := key; cursor != nil; cursor = cursor.parent.Load() {
		chain = append(chain, cursor)
	}
	return chain
}

// Admits 判断一个带 listenerTag 标签的监听器，该不该收到发往 dispatchKey 的事件。
//
// 源: packages/core/scope/src/index.ts:158-181
//
// 三条规则，合起来就是这个包的核心：
//
//   - 没有标签的监听器收**所有**事件。它没有声明自己属于哪个作用域，也就没有理由被过滤掉。
//   - 带标签的监听器，标签等于分派键、**或者是分派键的任一祖先**时收到。
//     于是挂在预设上的一个监听器，能收到它下面每个 agent 的事件。
//   - 标签在分派键**下面**时收不到。事件只往上走，绝不往下——否则一个 agent 会收到
//     它兄弟的事件，而兄弟之间本来就该互相看不见。
//
// dispatchKey 为 nil 表示这次分派没有作用域，此时只有无标签的监听器收得到。
func Admits(listenerTag, dispatchKey *Key) bool {
	if listenerTag == nil {
		return true
	}
	for cursor := dispatchKey; cursor != nil; cursor = cursor.parent.Load() {
		if cursor == listenerTag {
			return true
		}
	}
	return false
}

// Carrier 是一个只管路由的事件接收者：它带着一个作用域键和一个可选的基础过滤器，
// 而**不暴露被承载对象的任何内容**。事件真正的载荷走事件参数。
//
// 源: packages/core/scope/src/index.ts:22-27, 158-185
//
// 类型参数记录被承载对象的类型，供分派侧做类型检查；subject 字段是非导出的，
// 所以拿到 Carrier 的人取不出它——这正是 DSH 那个 Scoped<T> 品牌类型要的效果，
// 在 Go 里由「非导出字段」直接得到，不需要品牌 symbol。
type Carrier[T any] struct {
	subject T
	key     *Key
	base    func(subject T, listenerTag *Key) bool
}

// Target 造一个只按作用域键路由的载体。
//
// 源: packages/core/scope/src/index.ts:158-185
//
// key 为 nil 表示这是一个无作用域的对象，此时只有无标签的监听器收得到。
func Target[T any](subject T, key *Key) *Carrier[T] {
	return &Carrier[T]{subject: subject, key: key}
}

// TargetFiltered 造一个保留了基础过滤器的载体。
//
// 源: packages/core/scope/src/index.ts:170-181
//
// 基础过滤器先跑，它否决了就直接不收，作用域规则不再参与——被承载对象自己那份
// 可见性判断优先级更高，作用域只是在它之上再收窄一层。
//
// 新增: DSH 那边基础过滤器是被承载对象上的一个方法，靠 filter.call(base, ctx) 保证
// 它的 this 仍然是那个对象（它自己的测试专门钉了这一条）。Go 没有 this，
// 所以把被承载对象作为第一个参数显式传给过滤器，效果相同而且没有隐式绑定。
func TargetFiltered[T any](subject T, key *Key, base func(subject T, listenerTag *Key) bool) *Carrier[T] {
	return &Carrier[T]{subject: subject, key: key, base: base}
}

// Key 给出这个载体的路由键，无作用域的载体返回 nil。
//
// 源: packages/core/scope/src/index.ts:14-15（ScopeKey）
func (c *Carrier[T]) Key() *Key {
	if c == nil {
		return nil
	}
	return c.key
}

// Admits 判断一个带 listenerTag 的监听器该不该收到经这个载体分派的事件。
//
// 源: packages/core/scope/src/index.ts:173-181
func (c *Carrier[T]) Admits(listenerTag *Key) bool {
	if c == nil {
		return false
	}
	if c.base != nil && !c.base(c.subject, listenerTag) {
		return false
	}
	return Admits(listenerTag, c.key)
}

// carrier 是一个非导出的标记方法，让 [AnyCarrier] 只可能由本包实现。
func (c *Carrier[T]) carrier() {}

// AnyCarrier 是不带类型参数的载体视图，供分派侧在不知道 T 的情况下问路由键。
//
// 源: packages/core/scope/src/index.ts:187-204
//
// 新增: DSH 那边 isScopeCarrier 查的是一张 WeakMap 里有没有这个对象。Go 这边
// 载体是本包自己的类型，一次类型断言就够了，不需要侧挂表。非导出的标记方法保证
// 外面的类型冒充不了载体。
type AnyCarrier interface {
	// Key 给出路由键，无作用域的载体返回 nil。
	Key() *Key
	carrier()
}

// IsCarrier 判断一个值是不是由 [Target] / [TargetFiltered] 造出来的载体。
//
// 源: packages/core/scope/src/index.ts:187-194
func IsCarrier(value any) bool {
	_, ok := value.(AnyCarrier)
	return ok
}

// CarrierKeyOf 读一个值的路由键。
//
// 源: packages/core/scope/src/index.ts:196-204
//
// 第二个返回值区分「是载体」和「不是载体」——**无作用域的载体也会返回 nil 键**，
// 光看键分不出这两种情况，而分派侧的检查恰恰要靠这个区分：一个本该带载体分派的事件
// 如果压根没带载体，那是个必须报出来的错误，而不是「它没有作用域」。
func CarrierKeyOf(value any) (key *Key, isCarrier bool) {
	carrier, ok := value.(AnyCarrier)
	if !ok {
		return nil, false
	}
	return carrier.Key(), true
}

// teardown 是登记在作用域上的一项清理。
type teardown struct {
	label string
	run   func(context.Context) error
}

// Scope 是一个作用域身份加上它的所有权边界：经它登记的清理会在它释放时一起跑掉。
//
// 源: packages/core/scope/src/index.ts:104-112, 129-147
//
// 新增: DSH 那边的 Scope 带一个 ctx（作用域化的 cordis 上下文）和两个释放入口——
// rawDispose 是 cordis 那个「精确的」disposer，dispose 是包了一层、能被重复调用且
// 共享同一次完成的版本。Go 这边只有一个 [Scope.Dispose]：它本身就是幂等且并发共享的，
// 所以 DSH 那个「原始 disposer 可能已经被别人取走了」的问题不存在，
// 也就不需要第二个入口。嵌进别人的拆解顺序时，直接把 Dispose 登记进去即可。
type Scope struct {
	key *Key

	mutex sync.Mutex
	// order 里的元素是 *teardown，按登记顺序排；释放时**倒着**跑。
	// 用链表而不是切片：一项清理在作用域释放之前被单独释放掉是常事
	// （见 [Scope.Defer] 返回的那个 disposer），链表删除是 O(1) 且不打乱其余顺序。
	order    *list.List
	disposed bool

	once sync.Once
	err  error
}

// Options 是造一个作用域时的可选项。
//
// 源: packages/core/scope/src/index.ts:123-127
type Options struct {
	// Parent 是外层作用域。非 nil 时在作用域可用之前就经 [BindParent] 绑好，
	// 绑定句柄留在内部——也就是说这个键的归属此后只能由这里改，外面改不动。
	Parent *Key
}

// New 造一个带身份的作用域。
//
// 源: packages/core/scope/src/index.ts:129-147
func New(key *Key, options Options) (*Scope, error) {
	if key == nil {
		return nil, errors.New("scope: 作用域键不能是 nil（无身份的所有权边界请用 NewRoot）")
	}
	if options.Parent != nil {
		if _, err := BindParent(key, options.Parent); err != nil {
			return nil, err
		}
	}
	return &Scope{key: key, order: list.New()}, nil
}

// NewRoot 造一个**没有身份**的作用域：它照样拥有登记在它上面的清理，
// 但不参与任何路由，往 [Layers] 上登记时落在全局层。
//
// 新增: DSH 那边这就是一个没被打过作用域标签的普通 cordis 上下文——那种上下文
// 在它那里随处可得。Go 这边没有隐式的上下文，所以给它一个显式的构造函数，
// 而不是让 New(nil, ...) 这种写法有特殊含义。
func NewRoot() *Scope {
	return &Scope{order: list.New()}
}

// Key 给出这个作用域的身份，[NewRoot] 造出来的返回 nil。
func (s *Scope) Key() *Key {
	if s == nil {
		return nil
	}
	return s.key
}

// Defer 往作用域上登记一项清理，返回一个**幂等的**、单独释放这一项的 disposer。
//
// 源: packages/core/scope/src/store.ts:219-266（对应 cordis 的 ctx.effect）
//
// 返回的 disposer 跑完这一项之后会把它从作用域上摘掉，所以作用域整体释放时不会再跑第二遍。
// 这正是 cordis 的 effect disposer 的语义。
//
// 作用域已经释放时返回 [ErrScopeDisposed]：静默接受的话这份清理永远跑不到，
// 也就是那份资源泄漏了，而且没有任何症状。
func (s *Scope) Defer(label string, run func(context.Context) error) (func(context.Context) error, error) {
	if run == nil {
		return nil, errors.New("scope: 登记的清理函数不能是 nil")
	}

	s.mutex.Lock()
	if s.disposed {
		s.mutex.Unlock()
		return nil, fmt.Errorf("%w：%s", ErrScopeDisposed, label)
	}
	element := s.order.PushBack(&teardown{label: label, run: run})
	s.mutex.Unlock()

	var once sync.Once
	var err error
	return func(ctx context.Context) error {
		once.Do(func() {
			s.mutex.Lock()
			// 作用域整体释放时会把链表清空，那时这个元素已经跑过了，这里就不该再跑一遍。
			// 用「元素还在不在链表里」来判断，而不是另设一个标志位。
			alive := element.Value != nil
			if alive {
				s.order.Remove(element)
				element.Value = nil
			}
			s.mutex.Unlock()
			if alive {
				err = run(ctx)
			}
		})
		return err
	}, nil
}

// Effects 给出这个作用域**当前还持有**的那些清理的标签，按登记顺序。
//
// 源: packages/core/scope/tests/store.spec.ts:195（cordis 的 ctx.fiber.getEffects()）
//
// 纯诊断用：回答「这个作用域现在还欠着什么没释放」。
func (s *Scope) Effects() []string {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	labels := make([]string, 0, s.order.Len())
	for element := s.order.Front(); element != nil; element = element.Next() {
		if item, ok := element.Value.(*teardown); ok {
			labels = append(labels, item.label)
		}
	}
	return labels
}

// Dispose 释放这个作用域拥有的全部清理，**后登记的先跑**。
//
// 源: packages/core/scope/src/index.ts:110-111, 114-118
//
// 后进先出是因为后登记的东西可能依赖先登记的：反过来跑会让一项清理在它依赖的
// 东西已经没了之后才执行。
//
// 幂等且并发共享：重复调用、以及并发调用，都等同一次完成并拿到同一个错误。
// 这对应 DSH 那个 quiesceFiber——只不过在 Go 里 sync.Once 本身就保证
// 「任何一次 Do 都要等到那唯一一次执行返回之后才返回」，不需要额外的等待逻辑。
//
// 并发调用时，第一个进来的那个 ctx 决定超时；其余调用方的 ctx 不参与。
// 所有清理都会被跑到，其中的错误用 errors.Join 合起来一起返回——
// 前一项失败就跳过后面的话，剩下的资源就全泄漏了。
func (s *Scope) Dispose(ctx context.Context) error {
	s.once.Do(func() {
		s.mutex.Lock()
		s.disposed = true
		pending := make([]*teardown, 0, s.order.Len())
		for element := s.order.Back(); element != nil; element = element.Prev() {
			if item, ok := element.Value.(*teardown); ok {
				pending = append(pending, item)
			}
			// 把每个元素标记成已跑过。只调 order.Init() 是不够的：那只重置链表的头尾，
			// 元素自己仍然认为它在这个链表里，于是一个在释放**之后**才被调到的单项
			// disposer 会去 Remove 一个已经不在链上的元素，把链表长度改成负数。
			element.Value = nil
		}
		s.order.Init()
		s.mutex.Unlock()

		var failures []error
		for _, item := range pending {
			if err := item.run(ctx); err != nil {
				failures = append(failures, fmt.Errorf("%s: %w", item.label, err))
			}
		}
		s.err = errors.Join(failures...)
	})
	return s.err
}
