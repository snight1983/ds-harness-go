// 本文件的作用：这条接缝的服务本体——登记、解析、写入、提交，以及后端要实现的那三件事。
//
// 源: packages/settings/settings/src/index.ts:344-812

package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strconv"
	"sync"

	"github.com/snight1983/ds-harness-go/invariants"
)

// Backend 是设置存储：一份按命名空间切段的原始文档，读得出来、写得进去。
//
// 源: packages/settings/settings/src/index.ts:388-423
//
// 新增: DSH 那边这三件事是 `abstract class SettingsProvider` 上的三个抽象成员，
// 其余四百多行写在同一个类里由子类继承。Go 没有实现继承，抽象的那三件落成这个接口，
// 写好的那些落在 [Provider] 上——分法和本仓库 credentials 包同源，只是方向相反：
// 那边抽象的多、写好的少，所以接口是提供方；这边抽象的少、写好的多，所以接口是后端。
//
// 新增: 三个方法都收 context.Context。DSH 是 Promise，没有取消。理由和本仓库
// attachment、credentials 记的那条一样：取消在 Go 里是传染的，接口上没有 ctx，
// 实现方内部就没有地方把取消交给数据库驱动或 HTTP 客户端——
// 一次连不上存储的 Persist 会把调用方的 goroutine 一直占着。
type Backend interface {
	// Writable 说明这个后端接不接受写入。
	//
	// 源: packages/settings/settings/src/index.ts:388-389
	//
	// 只读后端上的写必须当场拒绝，而不是写完了不生效：后者的症状是
	// 「我改了但它没变」，而用户没有任何理由怀疑是存储不收。
	Writable() bool

	// Load 读出当前的整份原始文档（命名空间 → 原始段）。
	//
	// 源: packages/settings/settings/src/index.ts:412-416
	//
	// 返回的文档必须是**脱钩**的：[Provider] 会把它当成自己的那一份存着。
	Load(ctx context.Context) (map[string]any, error)

	// Persist 持久化某一个命名空间**合并之后的完整用户段**。
	//
	// 源: packages/settings/settings/src/index.ts:418-423
	//
	// 收到的是整段而不是补丁：合并已经在 [Provider] 里按分层规则做完了，
	// 后端再合并一次的话，两套合并规则会在数组这类「整体替换」的值上给出不同答案。
	Persist(ctx context.Context, ns Namespace, section map[string]any) error
}

// Options 是登记一个命名空间时除了默认值之外的那些选项。
//
// 源: packages/settings/settings/src/index.ts:48-74（SettingsRegisterOptions）
type Options[T any] struct {
	// Base 是组装层，压在类型默认值之上、用户段之下。
	//
	// 源: packages/settings/settings/src/index.ts:38-39
	//
	// 它是这次部署／这个 profile 给的值。做成原始 map 而不是 T，是因为它通常直接来自
	// 一份配置文件里的一小段，本来就是原始数据；而且它按定义是**部分**的，
	// 用 T 表达不了「这个字段我没提」和「这个字段我设成零值」的区别。
	Base map[string]any

	// Applies 是拥有者声明的生效时机，缺省是 [AppliesLive]。
	//
	// 源: packages/settings/settings/src/index.ts:40-41
	Applies Applies

	// Schema 是登记方自带的、给配置界面用的形状描述，本包**原样搬运，不解释**。
	//
	// 新增: DSH 那边这个位置是 schemastery 的 `schema.toJSON()`。
	// 见包文档：把那套运行期 schema 搬进 Go 没有意义，而替登记方**生成**一份
	// schema 也不是这条接缝的活——它要做的是把登记方声明的那份文档送到界面手上。
	// 没有配置界面的登记方留空即可。
	Schema any

	// Validate 拒绝一个拥有者无法接受的解析值，用于类型本身表达不了的约束
	// （跨字段的要求，或者一个字段的合法性取决于另一个字段）。
	//
	// 源: packages/settings/settings/src/index.ts:42-61
	//
	// 在这里失败的是**那次写入**，所以调用方在 Update / Replace / Mutate 当场就知道了，
	// 而不是存下一个会悄悄让拥有者失能的值。
	//
	// 和类型分开是有意的：类型同时还是配置界面渲染的依据、也是一个缺席的段解析时走的路，
	// 把跨字段检查折进类型里会把这两件都改掉。
	//
	// 登记之后，一个存下来的段在这里失败会**保留上一个好值**并记一条日志，
	// 和解码失败一样——外部编辑的文档不该把一个跑着的拥有者拖死。
	// 而在登记那一刻还没有「上一个好值」，所以此时失败会让**登记本身**失败。
	Validate func(value T) error
}

// Descriptor 是一个已登记命名空间在配置界面眼里的样子。
//
// 源: packages/settings/settings/src/index.ts:76-102（SettingsDescriptor）
//
// 新增: Value / Base / User 三个都是**原始 JSON 形状**（map[string]any），
// 不是某个 Go 类型。配置界面渲染的是 JSON，它拿不到也用不上 T；
// 而 T 那一面由拥有者通过 [Scope.Get] 读，本来就是另一条路。
// 这样这三层还都能用同一个 [Redact] 和同一个 [DeepEqualJSON]。
type Descriptor struct {
	// Namespace 是这个命名空间。
	Namespace Namespace

	// Schema 是登记方递进来的那份形状描述，见 [Options.Schema]。
	Schema any

	// Value 是当前解析值。
	Value map[string]any

	// Revision 是读到这份描述时**原始用户段**的修订号。
	//
	// 源: packages/settings/settings/src/index.ts:74-78
	//
	// 把它原样带回写入方法，就能拒掉一次基于过期快照的写。
	Revision uint64

	// Base 是登记方的组装层（已脱钩），没声明时是 nil。
	Base map[string]any

	// User 是文档里存着的原始用户段（已脱钩），没有或者形状不对时是 nil。
	//
	// 源: packages/settings/settings/src/index.ts:81-85
	//
	// **一个字段出现在这里，就是它被用户覆盖过的标志**——配置界面靠这个标出
	// 哪些格子是用户改的，以及「重置」会退回到哪里。
	User map[string]any

	// Applies 是拥有者声明的生效时机。
	Applies Applies

	// Secrets 是类型声明的密钥位置，只有脱敏时才有。
	//
	// 源: packages/settings/settings/src/index.ts:88-89
	Secrets []Secret
}

// DescribeOptions 是 [Provider.Describe] 的选项。
//
// 源: packages/settings/settings/src/index.ts:104-112（SettingsDescribeOptions）
type DescribeOptions struct {
	// RedactSecrets 把 Value / Base / User 三层里的密钥字段摘掉，
	// 并在 [Descriptor.Secrets] 里列出它们的位置。
	//
	// **任何要过线的地方都必须打开它**；原样返回那条路只留给同进程内的配置界面。
	RedactSecrets bool
}

// Watcher 观察一个命名空间解析值的已提交变更。
//
// 源: packages/settings/settings/src/index.ts:104-115
//
// 新增: 同步且自持，和本仓库 credentials 包的监听器同一条约定：
// 它在变更**已经提交之后**被就地调用，不许阻塞，也不许回头去调这个服务的写方法。
//
// 同步换掉了 DSH 那条「每个回调一条串行 promise 链」：调用是就地完成的，
// 提交顺序就是调用顺序，比那条链的保证更强。代价是一个慢观察者会把整个服务的
// 提交路径拖住——这正是「不许阻塞」不是建议的原因。
type Watcher[T any] func(next, prev T)

// UpdatedListener 观察**任意**命名空间解析值的已提交变更。
//
// 源: packages/settings/settings/src/types.ts:20-35
//
// 和 [Watcher] 的分工：[Watcher] 是拥有者按自己的类型 T 收自己那一段；
// 这个是横跨全部命名空间的旁观者（不变量检查、审计、把变更转发出去的协议层），
// 所以值是原始 JSON 形状的，它本来就不知道每个命名空间的 T 是什么。
//
// 只在解析值**真的变了**的时候发。约束同 [Watcher]。
type UpdatedListener func(ns Namespace, next, prev map[string]any, source Source)

// DocumentListener 观察某个命名空间**原始用户段**的变更，无论解析值变没变。
//
// 源: packages/settings/settings/src/types.ts:37-48
//
// 它存在的理由见包文档「修订号数的是原始段」：配置界面必须知道一个字段从
// 「继承来的」变成了「用户钉死的」，也必须知道自己手上的修订号过期了。
type DocumentListener func(ns Namespace, revision uint64)

// registration 是一个活着的命名空间登记。
//
// 源: packages/settings/settings/src/index.ts:323-342
type registration struct {
	namespace Namespace
	declared  reflect.Type
	schema    any
	base      map[string]any
	applies   Applies

	// resolve 是在 [Register] 里把 T 固化掉之后剩下的那个函数：
	// 收一段原始用户段，给出解析后的 T 和它的原始投影。
	resolve func(section map[string]any) (typed any, raw map[string]any, err error)

	// writeMutex 就是这个命名空间的写队列。
	//
	// 新增: DSH 用一条 promise 链排队，还得特意 `previous.catch(() => undefined)`
	// 才能让一次失败的写不毒化后面所有人。Go 的互斥量天生没有这个问题：
	// 失败的那次解锁了就完事，队列里没有任何残留。
	//
	// 唯一的差别是 DSH 的链严格先进先出，而 Go 的互斥量不保证等待者的顺序。
	// 这一点不影响正确性：每一次写都从**轮到它的那一刻**的段上重新算，
	// 而两个真并发的调用之间本来就没有「谁先」可言。
	writeMutex sync.Mutex

	// 以下字段由 Provider.mutex 保护。
	typed         any
	raw           map[string]any
	revision      uint64
	nextWatcherID uint64
	watchers      []watcherEntry
}

// watcherEntry 是一个登记着的观察者。
//
// 新增: 存在切片里而不是 map 里，理由同本仓库 credentials.Notifier：
// Go 的 map 遍历顺序是随机的，用 map 会让通知顺序每次都不一样。
type watcherEntry struct {
	id     uint64
	notify func(next, prev any)
}

type updatedSubscription struct {
	id       uint64
	listener UpdatedListener
}

type documentSubscription struct {
	id       uint64
	listener DocumentListener
}

// Provider 是设置服务：DSH 抽象基类里已经写好的那四百多行，落在这里。
//
// 源: packages/settings/settings/src/index.ts:344-812
//
// 零值不可用，请用 [New]。
type Provider struct {
	backend Backend
	logger  *slog.Logger

	// commitMutex 把「换值 + 发通知」整段串起来。
	//
	// 新增: DSH 是单线程的，提交顺序等于代码顺序，不需要这把锁。
	// Go 这边写入来自请求处理的 goroutine、[Provider.Publish] 来自后端的监听
	// goroutine，两者会同时走到提交这一步。做成全局一把而不是每个命名空间一把，
	// 是因为通知的顺序是要给人看的：全局一把给出一个**全序**，
	// 而这条接缝的提交是低频的，那点并发度不值得拿顺序去换。
	//
	// 锁序：registration.writeMutex → commitMutex → mutex。
	// mutex 从不在调用用户代码或后端 I/O 的时候持有。
	commitMutex sync.Mutex

	mutex         sync.Mutex
	document      map[string]any
	order         []Namespace
	registrations map[Namespace]*registration
	stopped       bool

	listenerMutex   sync.Mutex
	nextListenerID  uint64
	updated         []updatedSubscription
	documentUpdated []documentSubscription
}

// New 建一个设置服务：读一次后端文档并发布它。
//
// 源: packages/settings/settings/src/index.ts:370-386
//
// 先读再交出去，而不是交出去之后再懒加载：登记方在拿到服务的下一行就会登记，
// 而登记要当场解析出一个值来。文档还没读进来的话，第一个登记方拿到的是默认值，
// 然后过一会儿被一次「变更」通知悄悄换掉——一个明明没人改过的启动过程里，
// 凭空多出一次变更。
//
// logger 留空用 slog.Default()——**不是**丢弃。这里记的是「存下来的段坏了，
// 保留上一个好值」和「某个观察者炸了但提交是好的」，正是没人会主动去查、
// 却必须留下痕迹的那类事。要静音的调用方显式递一个装着 slog.DiscardHandler 的 logger。
func New(ctx context.Context, backend Backend, logger *slog.Logger) (*Provider, error) {
	if backend == nil {
		return nil, fmt.Errorf("settings: 建服务需要一个后端")
	}
	if logger == nil {
		logger = slog.Default()
	}
	document, err := backend.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: 读后端文档失败：%w", err)
	}
	if document == nil {
		document = map[string]any{}
	}
	return &Provider{
		backend:       backend,
		logger:        logger,
		document:      document,
		registrations: map[Namespace]*registration{},
	}, nil
}

// Scope 是一个已登记命名空间的**拥有者**那一面：按 T 读、观察、写。
//
// 源: packages/settings/settings/src/index.ts:114-141（SettingsScope）
type Scope[T any] struct {
	provider     *Provider
	registration *registration
}

// Register 登记一个命名空间，返回它的拥有者句柄和注销函数。
//
// 源: packages/settings/settings/src/index.ts:425-470
//
// defaults 是**类型默认值**那一层：一个填好的 T。
// 它压在最底下，[Options.Base] 和用户段依次叠在上面。
//
// 登记当场就要解析出一个值来。存下来的那一段在这里解不开（形状不对、
// 或者过不了 [Options.Validate]）会让**登记失败**——这是类型第一次有机会审判它的地方，
// 而此时还没有「上一个好值」可以退。
//
// 新增: DSH 的登记是登记方 fiber 上的一个 effect，fiber 一拆命名空间就没了。
// Go 里没有那个容器，注销就是返回的这个函数，由登记方自己 defer。
// 它是幂等的，多调几次不会摘掉后来者——认的是这一次登记的那个对象。
//
// 新增: 这是包级泛型函数而不是 [Provider] 的方法，因为 Go 的方法不能带类型参数。
// T 在这里被固化成一个 reflect.Type 和一个 resolve 闭包存进登记项，之后全走非泛型的路。
func Register[T any](p *Provider, ns Namespace, defaults T, options *Options[T]) (*Scope[T], func(), error) {
	if p == nil {
		return nil, nil, fmt.Errorf("settings: 登记需要一个服务")
	}
	if _, err := NewNamespace(string(ns)); err != nil {
		return nil, nil, err
	}
	if options == nil {
		options = &Options[T]{}
	}

	defaultsRaw, err := toRawSection(defaults)
	if err != nil {
		return nil, nil, fmt.Errorf("settings: 命名空间 %q 的默认值不是 JSON 形状：%w", string(ns), err)
	}
	baseRaw, err := cloneOptionalSection(options.Base)
	if err != nil {
		return nil, nil, fmt.Errorf("settings: 命名空间 %q 的组装层不是 JSON 形状：%w", string(ns), err)
	}
	applies := options.Applies
	if applies == "" {
		applies = AppliesLive
	}
	validate := options.Validate

	// 三层叠好、解码成 T、跑一遍拥有者的检查，最后把 T 投影回原始形状。
	//
	// 源: packages/settings/settings/src/index.ts:696-710
	//
	// 投影回去是为了让后面所有的比较、脱敏、描述都在同一种形状上做。
	// 直接拿合并结果当解析值是不对的：它没经过 T 的审判，
	// 一个类型不接受的字段会以「解析值」的身份被发出去。
	resolve := func(section map[string]any) (any, map[string]any, error) {
		merged := mergeSections(mergeSections(defaultsRaw, baseRaw), section)
		var value T
		if err := decodeSection(merged, &value); err != nil {
			return nil, nil, err
		}
		if validate != nil {
			if err := validate(value); err != nil {
				return nil, nil, err
			}
		}
		raw, err := toRawSection(value)
		if err != nil {
			return nil, nil, err
		}
		return value, raw, nil
	}

	entry := &registration{
		namespace: ns,
		declared:  reflect.TypeFor[T](),
		schema:    options.Schema,
		base:      baseRaw,
		applies:   applies,
		resolve:   resolve,
	}

	p.mutex.Lock()
	if p.stopped {
		p.mutex.Unlock()
		return nil, nil, fmt.Errorf("%w：%q 不能登记", ErrStopped, string(ns))
	}
	if _, exists := p.registrations[ns]; exists {
		p.mutex.Unlock()
		return nil, nil, fmt.Errorf("%w：%q", ErrAlreadyRegistered, string(ns))
	}
	section, sectionErr := readSection(p.document, ns)
	p.mutex.Unlock()

	if sectionErr != nil {
		return nil, nil, sectionErr
	}
	typed, raw, err := resolve(section)
	if err != nil {
		return nil, nil, fmt.Errorf("settings: 命名空间 %q 存下来的段解析不了：%w", string(ns), err)
	}
	entry.typed, entry.raw = typed, raw

	p.mutex.Lock()
	if p.stopped {
		p.mutex.Unlock()
		return nil, nil, fmt.Errorf("%w：%q 不能登记", ErrStopped, string(ns))
	}
	if _, exists := p.registrations[ns]; exists {
		p.mutex.Unlock()
		return nil, nil, fmt.Errorf("%w：%q", ErrAlreadyRegistered, string(ns))
	}
	p.registrations[ns] = entry
	p.order = append(p.order, ns)
	p.mutex.Unlock()

	return &Scope[T]{provider: p, registration: entry}, func() { p.unregister(entry) }, nil
}

// unregister 只摘掉**这一次登记**留下的那一条。
//
// 认对象身份而不是认名字，理由和本仓库 storage.BackendRegistry 认令牌一样：
// 注销之后又登记了一个新的拥有者，此时那个旧的注销函数要是再被调一次
// （重复 defer、重试路径上多走了一遍），它会把继任者摘掉。
func (p *Provider) unregister(entry *registration) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if p.registrations[entry.namespace] != entry {
		return
	}
	delete(p.registrations, entry.namespace)
	for index, ns := range p.order {
		if ns == entry.namespace {
			p.order = append(p.order[:index:index], p.order[index+1:]...)
			break
		}
	}
}

// Namespace 返回这个句柄管着的命名空间。
func (s *Scope[T]) Namespace() Namespace { return s.registration.namespace }

// Get 读当前解析值：类型默认值、组装层、用户段依次叠出来的那个。
//
// 源: packages/settings/settings/src/index.ts:104-105,458
//
// 新增: DSH 用 deepFreeze 把交出去的快照冻住，防止拿到的人改坏别人手上的同一份。
// Go 没有冻结。T 是按值返回的，所以标量字段各是各的；但**里面的切片和 map 是共享的**，
// 改它们会影响所有读到过这个值的人。
//
// 没有在每次读的时候深拷贝一份，是权衡后的决定：解析值在两次提交之间是不变的，
// 而读会落在每一次操作的路径上，为一个「不该发生」的写法给所有读加一次序列化不划算。
// 约定和 Go 里任何一个返回配置的 Get 一样：**读到的值是只读的**。
func (s *Scope[T]) Get() T {
	s.provider.mutex.Lock()
	defer s.provider.mutex.Unlock()

	value, _ := s.registration.typed.(T)
	return value
}

// Watch 观察这个命名空间解析值的已提交变更，返回退订函数。
//
// 源: packages/settings/settings/src/index.ts:106-115,459-466
//
// 退订是同步且幂等的：它只从一张表里摘掉一项，不做 I/O，多调几次不会摘错别人。
// 退订返回之后就不会再有新的调用进来。
func (s *Scope[T]) Watch(watcher Watcher[T]) func() {
	if watcher == nil {
		return func() {}
	}
	entry := s.registration

	s.provider.mutex.Lock()
	id := entry.nextWatcherID
	entry.nextWatcherID++
	entry.watchers = append(entry.watchers, watcherEntry{
		id: id,
		notify: func(next, prev any) {
			nextValue, _ := next.(T)
			prevValue, _ := prev.(T)
			watcher(nextValue, prevValue)
		},
	})
	s.provider.mutex.Unlock()

	return func() {
		s.provider.mutex.Lock()
		defer s.provider.mutex.Unlock()
		for index, candidate := range entry.watchers {
			if candidate.id == id {
				entry.watchers = append(entry.watchers[:index:index], entry.watchers[index+1:]...)
				return
			}
		}
	}
}

// Update 把一份补丁并进这个命名空间的用户段并持久化。
//
// 源: packages/settings/settings/src/index.ts:116-121,467
func (s *Scope[T]) Update(ctx context.Context, patch map[string]any) error {
	return s.provider.Update(ctx, s.registration.namespace, patch, nil)
}

// Replace 整段替换这个命名空间的用户段。
//
// 源: packages/settings/settings/src/index.ts:122-128,468
func (s *Scope[T]) Replace(ctx context.Context, section map[string]any) error {
	return s.provider.Replace(ctx, s.registration.namespace, section, nil)
}

// Get 读一个已登记命名空间的解析值，第二个返回值说明它登记没登记。
//
// 源: packages/settings/settings/src/index.ts:514-521
//
// 给的是**原始 JSON 形状**，理由见 [Descriptor]：这一面服务的是不知道 T 是什么的
// 旁观者。拥有者按 T 读走 [Scope.Get]。
func (p *Provider) Get(ns Namespace) (map[string]any, bool) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	entry, exists := p.registrations[ns]
	if !exists {
		return nil, false
	}
	return entry.raw, true
}

// Describe 按登记顺序描述每一个已登记的命名空间。
//
// 源: packages/settings/settings/src/index.ts:472-512
//
// 组装层和原始用户段都一并给出来，这样一个表单才能标出哪些字段是用户覆盖的
// （出现在 [Descriptor.User] 里的那些），以及重置会退回到哪里。
func (p *Provider) Describe(options *DescribeOptions) []Descriptor {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	redact := options != nil && options.RedactSecrets
	descriptors := make([]Descriptor, 0, len(p.order))
	for _, ns := range p.order {
		// 这一条走不到，所以没有用例覆盖：p.order 和 p.registrations 由 p.mutex
		// 同一把锁一起增删（见 [Provider.Register] 与 [Provider.unregister]），
		// order 里的名字一定查得到登记项。留着是因为这里已经持着锁，
		// 万一哪天两边真的分了叉，跳过一行远好过在一个 nil 登记项上 panic 掉整个描述。
		entry, exists := p.registrations[ns]
		if !exists {
			continue
		}
		// 存下来的段形状不对时按「没有用户层」描述。这一段在发布时已经报过警、
		// 也已经保留了上一个好值；描述这一面必须是全函数，不能因为文档坏了就读不出来。
		//
		// 源: packages/settings/settings/src/index.ts:481-489
		user, err := readSection(p.document, ns)
		if err != nil {
			user = nil
		}
		descriptor := Descriptor{
			Namespace: ns,
			Schema:    entry.schema,
			Value:     entry.raw,
			Revision:  entry.revision,
			Base:      detachSection(entry.base),
			User:      detachSection(user),
			Applies:   entry.applies,
		}
		if redact {
			value := redactType(entry.declared, entry.raw)
			descriptor.Value, _ = value.Value.(map[string]any)
			descriptor.Secrets = value.Secrets
			if descriptor.Base != nil {
				descriptor.Base, _ = redactType(entry.declared, descriptor.Base).Value.(map[string]any)
			}
			if descriptor.User != nil {
				descriptor.User, _ = redactType(entry.declared, descriptor.User).Value.(map[string]any)
			}
		}
		descriptors = append(descriptors, descriptor)
	}
	return descriptors
}

// Update 把一份补丁并进某个命名空间的用户段，校验、持久化，然后提交并通知。
//
// 源: packages/settings/settings/src/index.ts:523-536
//
// expectedRevision 为 nil 表示不做版本核对；非 nil 且对不上时返回 *[ConflictError]。
//
// 新增: DSH 的 expectedRevision 是可选参数。Go 没有可选参数，用指针表达同一件事——
// 修订号 0 是一个合法值（刚登记时就是 0），不能拿某个数当「没提供」。
func (p *Provider) Update(ctx context.Context, ns Namespace, patch map[string]any, expectedRevision *uint64) error {
	return p.write(ctx, ns, "update", expectedRevision, func(current map[string]any) (map[string]any, error) {
		snapshot, err := cloneJSONShaped("update", ns, patch)
		if err != nil {
			return nil, err
		}
		return mergeSections(current, snapshot), nil
	})
}

// Replace 整段替换某个命名空间的用户段，校验、持久化，然后提交并通知。
//
// 源: packages/settings/settings/src/index.ts:538-550
//
// section 里没有的键退回到组装层和类型默认值——这是一条**只会合并的补丁表达不了**的路：
// Replace(空段) 就是把这个命名空间整个重置。
func (p *Provider) Replace(ctx context.Context, ns Namespace, section map[string]any, expectedRevision *uint64) error {
	return p.write(ctx, ns, "replace", expectedRevision, func(map[string]any) (map[string]any, error) {
		return cloneJSONShaped("replace", ns, section)
	})
}

// Mutate 按路径编辑某个命名空间的用户段，校验、持久化，然后提交并通知。
//
// 源: packages/settings/settings/src/index.ts:552-575
//
// 这些 op 作用在**轮到它们的那一刻**的段上，所以调用方不必复述自己没动过的字段——
// 更要紧的是，它删不掉自己从来没见过的字段。手上只有脱敏视图的调用方走这条路，
// [Provider.Replace] 留给整段重置。
func (p *Provider) Mutate(ctx context.Context, ns Namespace, ops []PathOp, expectedRevision *uint64) error {
	for _, op := range ops {
		if op.Kind != PathOpSet && op.Kind != PathOpUnset {
			return fmt.Errorf("settings: mutate %q 的动作只能是 %q 或 %q，收到 %q",
				string(ns), PathOpSet, PathOpUnset, op.Kind)
		}
	}
	return p.write(ctx, ns, "mutate", expectedRevision, func(current map[string]any) (map[string]any, error) {
		// 值也要过一遍 JSON 形状检查，理由和整段写入一样：存不下的东西写进去之后，
		// 下一次读回来会变成另一个值而中间不报错。包一层是为了一次走完全部 op。
		wrapper := map[string]any{}
		for index, op := range ops {
			wrapper[strconv.Itoa(index)] = op.Value
		}
		snapshot, err := cloneJSONShaped("mutate", ns, wrapper)
		if err != nil {
			return nil, err
		}
		section := current
		for index, op := range ops {
			op.Value = snapshot[strconv.Itoa(index)]
			section, err = applyPathOp(section, op)
			if err != nil {
				return nil, err
			}
		}
		return section, nil
	})
}

// write 是三条写入路径共用的那一段。
//
// 源: packages/settings/settings/src/index.ts:577-648
//
// 次序是有讲究的，逐条对齐 DSH：
//
//  1. **先拿这个命名空间的写锁。** 它就是那条队列。
//  2. **段从「此刻」读**，不是从调用方上次看到的那份。
//  3. **修订号核对在这里**，不在调用时刻——队列只排次序，
//     它分不出一个刚读完就写的调用方和一个拿着已被前一次写作废的快照的调用方。
//  4. **先解析再落盘。** 校验失败时什么都还没写下去。
//  5. **落盘成功就一定要认。** 缓存里的文档必须跟着改，
//     否则「存储里是新的、内存里是旧的」这种分叉没有任何一方看得见。
func (p *Provider) write(
	ctx context.Context,
	ns Namespace,
	verb string,
	expectedRevision *uint64,
	build func(current map[string]any) (map[string]any, error),
) error {
	p.mutex.Lock()
	entry, exists := p.registrations[ns]
	stopped := p.stopped
	p.mutex.Unlock()

	if !exists {
		return fmt.Errorf("%w：%q 不能 %s", ErrNotRegistered, string(ns), verb)
	}
	if stopped {
		return fmt.Errorf("%w：%q 不能 %s", ErrStopped, string(ns), verb)
	}
	if !p.backend.Writable() {
		return fmt.Errorf("%w：%q 不能 %s", ErrReadOnly, string(ns), verb)
	}

	entry.writeMutex.Lock()
	defer entry.writeMutex.Unlock()

	p.mutex.Lock()
	if p.stopped {
		p.mutex.Unlock()
		return fmt.Errorf("%w：排队中的 %q %s 没能执行", ErrStopped, string(ns), verb)
	}
	if p.registrations[ns] != entry {
		p.mutex.Unlock()
		return fmt.Errorf("%w：排队中的 %q %s 没能执行，登记已经注销", ErrNotRegistered, string(ns), verb)
	}
	current, sectionErr := readSection(p.document, ns)
	revision := entry.revision
	p.mutex.Unlock()

	if sectionErr != nil {
		return sectionErr
	}
	if expectedRevision != nil && *expectedRevision != revision {
		return &ConflictError{Namespace: ns, Expected: *expectedRevision, Actual: revision}
	}
	if current == nil {
		current = map[string]any{}
	}
	section, err := build(current)
	if err != nil {
		return err
	}
	typed, raw, err := entry.resolve(section)
	if err != nil {
		return fmt.Errorf("settings: %q 的这次 %s 解析不了：%w", string(ns), verb, err)
	}
	if err := p.backend.Persist(ctx, ns, section); err != nil {
		return fmt.Errorf("settings: %q 的这次 %s 落盘失败：%w", string(ns), verb, err)
	}

	p.commitMutex.Lock()
	defer p.commitMutex.Unlock()

	p.mutex.Lock()
	p.document[string(ns)] = section
	stillOwner := p.registrations[ns] == entry && !p.stopped
	p.mutex.Unlock()

	if !stillOwner {
		// 落盘已经发生了，文档也认了；但这次登记在落盘期间被注销（或者服务关了），
		// 通知不该再发给一个已经走掉的拥有者。
		return nil
	}
	p.bumpRevision(entry, current, section)
	p.commit(entry, typed, raw, SourceUpdate)
	return nil
}

// Publish 是后端的钩子：提交一份在存储上观察到的完整原始文档。
//
// 源: packages/settings/settings/src/index.ts:650-684
//
// 每一个已登记的命名空间都重新解析一遍。**某一段解不开时只影响那一段**：
// 它保留上一个好值并记一条日志，其余命名空间照常提交。
// 一份被人手工编辑坏了的文档，不该把所有跑着的拥有者一起拖死。
//
// 未登记的段原样留在文档里——某个还没装上的插件的配置不该因为一次发布就消失。
func (p *Provider) Publish(document map[string]any, source Source) {
	if document == nil {
		document = map[string]any{}
	}
	if source == "" {
		source = SourceProvider
	}

	p.commitMutex.Lock()
	defer p.commitMutex.Unlock()

	// 换文档**之前**先把每一段原来的样子读下来，这样下面的修订号才比得出
	// 「存的东西变没变」——一次外部编辑和一次本进程的写，在修订号上必须一样地动。
	//
	// 源: packages/settings/settings/src/index.ts:658-671
	p.mutex.Lock()
	entries := make([]*registration, 0, len(p.order))
	for _, ns := range p.order {
		if entry, exists := p.registrations[ns]; exists {
			entries = append(entries, entry)
		}
	}
	before := make(map[Namespace]map[string]any, len(entries))
	for _, entry := range entries {
		// 形状不对的段读不出一个「之前」；当成缺席，这样任何一个形状正确的
		// 后继都会把修订号推上去。
		section, err := readSection(p.document, entry.namespace)
		if err != nil {
			section = nil
		}
		before[entry.namespace] = section
	}
	p.document = document
	after := make(map[Namespace]map[string]any, len(entries))
	for _, entry := range entries {
		section, err := readSection(p.document, entry.namespace)
		if err != nil {
			section = nil
		}
		after[entry.namespace] = section
	}
	p.mutex.Unlock()

	for _, entry := range entries {
		typed, raw, err := entry.resolve(after[entry.namespace])
		if err != nil {
			p.logger.Warn("settings: 存下来的段解析不了，保留上一个好值",
				slog.String("namespace", string(entry.namespace)),
				slog.Any("error", err),
			)
			continue
		}
		p.bumpRevision(entry, before[entry.namespace], after[entry.namespace])
		p.commit(entry, typed, raw, source)
	}
}

// Close 关掉服务：不再收新的写，等在途的写走完。
//
// 源: packages/settings/settings/src/index.ts:376-384
//
// 拿一遍每个命名空间的写锁就是「等在途的写走完」——那把锁的整个生命周期
// 就是一次写从读段到发通知的全过程。
func (p *Provider) Close() {
	p.mutex.Lock()
	if p.stopped {
		p.mutex.Unlock()
		return
	}
	p.stopped = true
	entries := make([]*registration, 0, len(p.registrations))
	for _, entry := range p.registrations {
		entries = append(entries, entry)
	}
	p.mutex.Unlock()

	for _, entry := range entries {
		entry.writeMutex.Lock()
		entry.writeMutex.Unlock() //nolint:staticcheck // 拿一遍再放，就是「等在途的写走完」
	}
	p.commitMutex.Lock()
	p.commitMutex.Unlock() //nolint:staticcheck // 同上：等在途的通知发完
}

// SubscribeUpdated 订阅任意命名空间解析值的已提交变更，返回退订函数。
//
// 源: packages/settings/settings/src/types.ts:20-35
func (p *Provider) SubscribeUpdated(listener UpdatedListener) func() {
	if listener == nil {
		return func() {}
	}
	p.listenerMutex.Lock()
	id := p.nextListenerID
	p.nextListenerID++
	p.updated = append(p.updated, updatedSubscription{id: id, listener: listener})
	p.listenerMutex.Unlock()

	return func() {
		p.listenerMutex.Lock()
		defer p.listenerMutex.Unlock()
		for index, candidate := range p.updated {
			if candidate.id == id {
				p.updated = append(p.updated[:index:index], p.updated[index+1:]...)
				return
			}
		}
	}
}

// SubscribeDocumentUpdated 订阅原始用户段的变更，返回退订函数。
//
// 源: packages/settings/settings/src/types.ts:37-48
func (p *Provider) SubscribeDocumentUpdated(listener DocumentListener) func() {
	if listener == nil {
		return func() {}
	}
	p.listenerMutex.Lock()
	id := p.nextListenerID
	p.nextListenerID++
	p.documentUpdated = append(p.documentUpdated, documentSubscription{id: id, listener: listener})
	p.listenerMutex.Unlock()

	return func() {
		p.listenerMutex.Lock()
		defer p.listenerMutex.Unlock()
		for index, candidate := range p.documentUpdated {
			if candidate.id == id {
				p.documentUpdated = append(p.documentUpdated[:index:index], p.documentUpdated[index+1:]...)
				return
			}
		}
	}
}

// bumpRevision 在**原始段**变了的时候把修订号推上去并广播。
//
// 源: packages/settings/settings/src/index.ts:712-723
//
// 有意和 [Provider.commit] 的解析值等值判断分开，见包文档：
// 存一条和组装层完全相同的覆盖，解析值没变，但文档说的话变了——
// 那正是配置界面必须重读的那种变化。
//
// 调用方必须已经持有 commitMutex。
func (p *Provider) bumpRevision(entry *registration, before, after map[string]any) {
	if DeepEqualJSON(toAny(before), toAny(after)) {
		return
	}
	p.mutex.Lock()
	entry.revision++
	revision := entry.revision
	p.mutex.Unlock()

	p.listenerMutex.Lock()
	listeners := make([]DocumentListener, 0, len(p.documentUpdated))
	for _, subscription := range p.documentUpdated {
		listeners = append(listeners, subscription.listener)
	}
	p.listenerMutex.Unlock()

	fanOut(func(deliver func(func())) {
		for _, listener := range listeners {
			deliver(func() { listener(entry.namespace, revision) })
		}
	}, func(recovered any) {
		p.warnListenerFailure("settings/document-updated", entry.namespace, recovered)
	})
}

// commit 在解析值真的变了的时候换值、通知观察者、广播事件。
//
// 源: packages/settings/settings/src/index.ts:748-799
//
// 调用方必须已经持有 commitMutex——那把锁给出的全序就是这里的通知顺序。
func (p *Provider) commit(entry *registration, typed any, raw map[string]any, source Source) {
	p.mutex.Lock()
	previousTyped, previousRaw := entry.typed, entry.raw
	if DeepEqualJSON(toAny(raw), toAny(previousRaw)) {
		p.mutex.Unlock()
		return
	}
	entry.typed, entry.raw = typed, raw
	watchers := make([]func(next, prev any), 0, len(entry.watchers))
	for _, watcher := range entry.watchers {
		watchers = append(watchers, watcher.notify)
	}
	p.mutex.Unlock()

	fanOut(func(deliver func(func())) {
		for _, notify := range watchers {
			deliver(func() { notify(typed, previousTyped) })
		}
	}, func(recovered any) {
		p.warnWatcherFailure(entry.namespace, recovered)
	})

	p.listenerMutex.Lock()
	listeners := make([]UpdatedListener, 0, len(p.updated))
	for _, subscription := range p.updated {
		listeners = append(listeners, subscription.listener)
	}
	p.listenerMutex.Unlock()

	fanOut(func(deliver func(func())) {
		for _, listener := range listeners {
			deliver(func() { listener(entry.namespace, raw, previousRaw, source) })
		}
	}, func(recovered any) {
		p.warnListenerFailure("settings/updated", entry.namespace, recovered)
	})
}

// fanOut 是三处广播共用的那段兜底分发。
//
// 源: packages/settings/settings/src/index.ts:725-746,776-798
//
// 三条规则，逐条对齐（和本仓库 credentials.Notifier.fanOut 是同一套）：
//
//  1. **每一个订阅者都跑到。** 一个订阅者炸掉不许掐断后面的——变更已经提交了，
//     没跑到的那几个从此和存储不一致，而它们永远不会知道。
//  2. **普通失败只记日志。** 一次已经落盘的写不该因为有人在旁边看崩了就报失败；
//     更要紧的是提交路径同时也是后端的重载路径，一个坏观察者能把它整个卡死。
//  3. **不变量违例例外：等所有订阅者都跑完之后重新抛出**，且只抛第一条。
//     不变量违例意味着程序写错了（见 invariants 包），它必须传到发起方手里。
func fanOut(dispatch func(deliver func(func())), warn func(recovered any)) {
	var invariantFailure *invariants.Error

	dispatch(func(call func()) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			if failure, isInvariant := recovered.(*invariants.Error); isInvariant {
				// 只留第一条：后面的违例多半是同一个原因的连锁反应，
				// 而抛出去的只能有一个，抛最早的那个离现场最近。
				if invariantFailure == nil {
					invariantFailure = failure
				}
				return
			}
			warn(recovered)
		}()
		call()
	})

	if invariantFailure != nil {
		panic(invariantFailure)
	}
}

// warnWatcherFailure 是观察者失败时留下的那条诊断。
//
// 源: packages/settings/settings/src/index.ts:801-805
//
// **不记值**：设置里可能有密钥（见 [SecretTag]），而一个 panic 的载荷里
// 完全可能带着刚读到的那一段。命名空间名不是秘密，够定位了。
func (p *Provider) warnWatcherFailure(ns Namespace, recovered any) {
	p.logger.Warn("settings: 一个观察者处理变更时失败",
		slog.String("namespace", string(ns)),
		slog.Any("panic", recovered),
	)
}

// warnListenerFailure 是订阅者失败时留下的那条诊断，口径同 [Provider.warnWatcherFailure]。
//
// 源: packages/settings/settings/src/index.ts:807-811
func (p *Provider) warnListenerFailure(event string, ns Namespace, recovered any) {
	p.logger.Warn("settings: 一个订阅者处理事件时失败",
		slog.String("event", event),
		slog.String("namespace", string(ns)),
		slog.Any("panic", recovered),
	)
}

// readSection 读一个命名空间的原始用户段，段不是对象时报错。
//
// 源: packages/settings/settings/src/index.ts:686-694
//
// 段缺席返回 (nil, nil)：那是完全正常的状态，一个还没被用户改过的命名空间就是这样。
func readSection(document map[string]any, ns Namespace) (map[string]any, error) {
	raw, exists := document[string(ns)]
	if !exists || raw == nil {
		return nil, nil
	}
	section, isObject := raw.(map[string]any)
	if !isObject {
		return nil, fmt.Errorf("%w：%q", ErrMalformedSection, string(ns))
	}
	return section, nil
}

// detachSection 复制一份段交出去。
//
// 描述这一面交出去的东西会离开这个包，而 [Provider] 手上的那几份是共享的活数据；
// 不复制的话，一个配置界面顺手改一下自己拿到的 map，就把服务里的段改了。
func detachSection(section map[string]any) map[string]any {
	if section == nil {
		return nil
	}
	detached, err := cloneJSONShaped("describe", "", section)
	if err != nil {
		return nil
	}
	return detached
}

// cloneOptionalSection 脱钩一份可选的段；nil 原样返回，表示这一层不存在。
func cloneOptionalSection(section map[string]any) (map[string]any, error) {
	if section == nil {
		return nil, nil
	}
	return cloneJSONShaped("register", "", section)
}

// toRawSection 把一个 Go 值投影成原始 JSON 形状的段。
func toRawSection(value any) (map[string]any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var section map[string]any
	if err := json.Unmarshal(encoded, &section); err != nil {
		return nil, err
	}
	if section == nil {
		section = map[string]any{}
	}
	return section, nil
}

// decodeSection 把一段原始数据解码进一个具体类型。
//
// 这一步就是 DSH 那句 `schema(merged)`：把一个按构造无类型的合并结果，
// 交给类型审判一次。过不了就是这次写入不成立。
//
// 不开 DisallowUnknownFields：类型里没有的键要留着。存下来的文档可能带着一个
// 已经删掉的旧字段，或者一个更新版本才认识的新字段——把它们判成错误，
// 一次降级运行就会让整个命名空间解析不了。这一条和 [Redact] 保留未声明键是同一个理由。
func decodeSection(section map[string]any, target any) error {
	encoded, err := json.Marshal(section)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, target)
}

// toAny 把一个可能为 nil 的段抬成 any，好交给 [DeepEqualJSON]。
//
// 直接传一个类型为 map[string]any 的 nil 会被当成「一个空 map」而不是「没有」——
// 而「没有段」和「有一个空段」在修订号那一边是两件事：从没有到空，
// 存下来的东西确实变了。
func toAny(section map[string]any) any {
	if section == nil {
		return nil
	}
	return section
}
