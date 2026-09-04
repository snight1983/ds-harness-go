// 本文件的作用：把「全局的那一份」和「每个作用域自己那一份」这两层叠在一起，
// 并且把每一次登记的撤销挂到登记者自己的作用域上。
//
// 源: packages/core/scope/src/store.ts:152-267
//
// 一个作用域感知的注册表（工具表、限制表、系统提示片段表……）长的都是同一个样子：
// 有一份大家共用的，外加若干份「只有某个作用域及其子孙看得到」的覆盖层。
// 这里只管这个叠放结构本身，注册表里装什么由使用方给的 L 决定。

package scope

import (
	"context"
	"errors"
	"sync"
)

// 这四条都是「调用方把必填的函数漏了」。
//
// 新增: DSH 靠 TypeScript 保证这些函数在场，Go 里 nil 进得来。不查的话，
// 前三条会在解引用时 panic，最后一条更隐蔽：action 成功了却没给撤销，
// 于是这次登记**永远撤不掉**，作用域释放时它还留在表里。
var (
	errNilCreateLayer  = errors.New("scope: createLayer 不能是 nil")
	errNilEffectOwner  = errors.New("scope: Effect 的宿主作用域不能是 nil")
	errNilEffectAction = errors.New("scope: Effect 的 action 不能是 nil")
	errNilEffectUndo   = errors.New("scope: Effect 的 action 必须返回撤销函数，不能是 nil")
)

// Layer 是一个作用域在某张注册表里的全部贡献。
//
// 源: packages/core/scope/src/store.ts:11-15
//
// 新增: 约束里多加了一个 comparable。回收一层的时候必须确认「map 里现在这一份
// **仍然是**当初创建的那一份」——否则一个迟到的撤销会把后来新建的同名层删掉
// （和 [NamedEntries.Insert] 里那个 undo 面对的是同一个问题）。这个身份比较要求
// L 可比较，写进约束就是编译期保证，而不是运行时 any 比较时才 panic。
type Layer interface {
	comparable
	// IsEmpty 表示这一层里**每一张表**都空了。
	//
	// 实现里不许回头调用 [Layers] 的任何方法：这个判断是在 Layers 的锁里做的，
	// 回调进去就是自己等自己。
	IsEmpty() bool
}

// Layers 持有一张注册表的全局层和各个作用域的覆盖层。
//
// 源: packages/core/scope/src/store.ts:152-267
//
// 三条不变式：
//
//   - **读永远不创建覆盖层**。[Layers.Peek]、[Layers.ChainLayers]、[MergeNamed]
//     只看已经存在的层。一次查询把层建出来的话，「这个作用域有没有自己的贡献」
//     这个问题就再也问不出真话了。
//   - **先拿到撤销，再对外通知**。[Layers.Effect] 里 action 返回的 undo 是先攥在手里的，
//     onChange 才跑；onChange 失败就用它原路退回去。
//   - **只回收整个空掉的层**。一层里有具名表也有匿名表，只有全空才删——删早了，
//     另一张表里还活着的登记就跟着没了。
type Layers[L Layer] struct {
	createLayer func(scope *Key) (L, error)
	onChange    func() error

	global L

	mutex  sync.Mutex
	scoped map[*Key]L
}

// NewLayers 造一套分层存储，并**当场**把全局层建出来。
//
// 源: packages/core/scope/src/store.ts:165-170
//
// 全局层是急切构造的：它一定会被用到（每一次合并都从它开始），而且早建早暴露
// createLayer 自身的错误——留到第一次登记时才炸，报错点离原因就远了。
//
// onChange 是「这张表变了」的通知。为 nil 表示不需要通知。
func NewLayers[L Layer](createLayer func(scope *Key) (L, error), onChange func() error) (*Layers[L], error) {
	if createLayer == nil {
		return nil, errNilCreateLayer
	}
	global, err := createLayer(nil)
	if err != nil {
		return nil, err
	}
	return &Layers[L]{
		createLayer: createLayer,
		onChange:    onChange,
		global:      global,
		scoped:      map[*Key]L{},
	}, nil
}

// Global 给出那一份大家共用的层。
//
// 源: packages/core/scope/src/store.ts:160-161
func (l *Layers[L]) Global() L { return l.global }

// Peek 读一个作用域**自己**那一层，没有就返回 false，且不会顺手建一个。
//
// 源: packages/core/scope/src/store.ts:172-183
//
// 这里是**故意不看父链**的：问「这个作用域自己贡献了什么」（它自己的限制、
// 它自己的守卫）的人，不该悄悄地把祖先的那份也拿走。要继承请用
// [Layers.ChainLayers] 或 [MergeNamed]。
func (l *Layers[L]) Peek(scope *Key) (L, bool) {
	if scope == nil {
		var zero L
		return zero, false
	}
	l.mutex.Lock()
	defer l.mutex.Unlock()

	layer, exists := l.scoped[scope]
	return layer, exists
}

// ChainLayers 给出父链上**已经存在**的那些层，**远祖在前、本作用域在最后**。
//
// 源: packages/core/scope/src/store.ts:185-199
//
// 顺序反过来是有意的：按这个顺序依次叠加，最近的那个作用域说了算。
// 链上没有覆盖层的那几级直接跳过。
func (l *Layers[L]) ChainLayers(scope *Key) []L {
	chain := ChainOf(scope)

	l.mutex.Lock()
	defer l.mutex.Unlock()

	layers := make([]L, 0, len(chain))
	// ChainOf 是近的在前，这里倒着走就是远祖在前。
	for index := len(chain) - 1; index >= 0; index-- {
		if layer, exists := l.scoped[chain[index]]; exists {
			layers = append(layers, layer)
		}
	}
	return layers
}

// NamedValue 是合并结果里的一项。
//
// 新增: DSH 那边 merge 返回一个 JS Map，天然带插入顺序。Go 的 map 没有顺序，
// 而这里顺序**就是语义**（工具的排列、提示片段的拼接顺序），所以返回切片。
type NamedValue[V any] struct {
	Name  string
	Value V
}

// MergeNamed 把全局层的具名表和父链上各层的具名表叠成一份有效视图。
//
// 源: packages/core/scope/src/store.ts:201-217
//
// 顺序：先是全局层自己的顺序，然后是父链上远祖到近亲。**同名的覆盖只改值，
// 不挪位置**——这一条来自 JS Map 的 set 语义，而它在这里是必须保留的行为：
// 位置决定了工具的排列和提示片段的先后，一个作用域覆盖掉某个名字的实现，
// 不该顺带把它挪到列表末尾去。
//
// 新增: 写成自由函数而不是方法，因为 Go 的方法不能再带自己的类型参数，
// 而值的类型 V 和层的类型 L 本来就是两回事。
func MergeNamed[L Layer, V any](layers *Layers[L], scope *Key, pick func(L) *NamedEntries[V]) []NamedValue[V] {
	var merged []NamedValue[V]
	index := map[string]int{}

	put := func(name string, value V) {
		if position, exists := index[name]; exists {
			merged[position].Value = value
			return
		}
		index[name] = len(merged)
		merged = append(merged, NamedValue[V]{Name: name, Value: value})
	}

	for name, value := range pick(layers.Global()).All() {
		put(name, value)
	}
	for _, layer := range layers.ChainLayers(scope) {
		for name, value := range pick(layer).All() {
			put(name, value)
		}
	}
	return merged
}

// EffectOptions 是一次 [Layers.Effect] 的选项。
//
// 源: packages/core/scope/src/store.ts:229
type EffectOptions struct {
	// Label 是登记到作用域上的诊断名，会出现在 [Scope.Effects] 里。
	Label string

	// Silent 表示这次改动不发 onChange 通知。
	//
	// 新增: DSH 那边是 `notify?: boolean` 默认 true。Go 的零值是 false，
	// 所以取反成 Silent——零值就等于「通知」，和 DSH 的默认行为对齐，
	// 而不是让一个漏填的选项把通知悄悄关掉。
	Silent bool
}

// Effect 把一次对某一层的改动，和它的撤销一起，挂到 owner 这个作用域上。
//
// 源: packages/core/scope/src/store.ts:219-266
//
// 落在哪一层由 owner 的身份决定：[NewRoot] 造的作用域没有身份，落在全局层；
// 有身份的作用域落在它自己那一层（没有就当场建一个）。
//
// 步骤是：取到层 → 跑 action 拿到 undo → 把撤销登记到 owner 上 → 发通知。
// 后面任何一步失败都会原路退回前面已经做过的，**不留半截状态**：
//
//   - action 失败：如果这一层是刚为它建的、而且现在还是空的，就把它删掉；
//     已经存在的层绝不因为一次失败的登记被删。
//   - 登记失败（owner 已经释放）：跑掉 undo，同样按上一条回收空层。
//   - onChange 失败：跑一遍**完整的**撤销（undo + 回收 + 再通知一次），
//     然后把 onChange 的错误报出去。
//
// ctx 只在最后那种回滚里用得上——undo 和 onChange 本身都是同步的，不收 ctx。
func (l *Layers[L]) Effect(
	ctx context.Context,
	owner *Scope,
	action func(layer L) (func(), error),
	options EffectOptions,
) (func(context.Context) error, error) {
	if owner == nil {
		return nil, errNilEffectOwner
	}
	if action == nil {
		return nil, errNilEffectAction
	}

	scope := owner.Key()

	layer, created, err := l.layerFor(scope)
	if err != nil {
		return nil, err
	}

	undo, err := action(layer)
	if err != nil {
		l.reclaimIfEmpty(scope, layer, created)
		return nil, err
	}
	if undo == nil {
		l.reclaimIfEmpty(scope, layer, created)
		return nil, errNilEffectUndo
	}

	dispose, err := owner.Defer(options.Label, func(context.Context) error {
		undo()
		// 撤销之后这一层可能空了，空了就回收。这条路径**永远允许**回收：
		// 依据是「现在空了」，和当初是不是我建的无关。
		l.reclaimIfEmpty(scope, layer, true)
		if options.Silent || l.onChange == nil {
			return nil
		}
		return l.onChange()
	})
	if err != nil {
		undo()
		l.reclaimIfEmpty(scope, layer, created)
		return nil, err
	}

	if !options.Silent && l.onChange != nil {
		if changeErr := l.onChange(); changeErr != nil {
			// 通知失败就当这次登记没发生过。撤销走的是同一个 dispose，
			// 所以回滚和正常释放是同一条代码路径，不会各走各的。
			_ = dispose(ctx)
			return nil, changeErr
		}
	}
	return dispose, nil
}

// layerFor 取出该作用域对应的层，没有就建一个，并说明这一次是不是新建的。
//
// 源: packages/core/scope/src/store.ts:234-247
func (l *Layers[L]) layerFor(scope *Key) (layer L, created bool, err error) {
	if scope == nil {
		return l.global, false, nil
	}

	l.mutex.Lock()
	defer l.mutex.Unlock()

	if existing, exists := l.scoped[scope]; exists {
		return existing, false, nil
	}
	fresh, err := l.createLayer(scope)
	if err != nil {
		var zero L
		return zero, false, err
	}
	l.scoped[scope] = fresh
	return fresh, true, nil
}

// reclaimIfEmpty 在一层空掉时把它删掉。
//
// 源: packages/core/scope/src/store.ts:253, 259
//
// permitted 是「这条路径**允许不允许**删」。撤销的路径永远允许；登记失败的路径
// 只在这一层是本次新建时才允许——一次失败的登记不该把一个**本来就存在**的层删掉，
// 哪怕它此刻碰巧是空的（那一层的空是别人的状态，不是这次失败造成的）。
//
// 允许之后还要确认 map 里现在这一份仍然是手上这一份：中间可能已经被别人撤销并重建过，
// 删错了会把别人活着的登记一起带走。
func (l *Layers[L]) reclaimIfEmpty(scope *Key, layer L, permitted bool) {
	if scope == nil || !permitted {
		return
	}

	l.mutex.Lock()
	defer l.mutex.Unlock()

	current, exists := l.scoped[scope]
	if !exists || current != layer {
		return
	}
	if !layer.IsEmpty() {
		return
	}
	delete(l.scoped, scope)
}
