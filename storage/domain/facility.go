// Package domain 是存储中枢上的第一个数据形态：**领域**。
//
// 对应 DSH 的 @deepseek-ai/dsh-storage-domain（packages/storage/storage-domain）。
//
// 源: packages/storage/storage-domain/src/index.ts:1-44
//
// 一个域是一份**有声明的**持久化状态：若干张表加一个可选的全局单例槽，
// 打开时整份读进内存，之后读同步、写过一条链。下层的 storage 包只认
// 「不透明 JSON」，语义全部落在这一层：类型、校验、变更事件、生命周期。
//
// # 三条不变量
//
// 见 domain.go 顶部那段。整个包建在它们上面，本文件负责的是**打开**那一头：
// 从介质上读回来的每一条记录都要过一遍声明的校验，过不了就整个域打不开——
// 一个域要么完整可信，要么根本不给出来，不存在「大部分能用」的中间态。
//
// # 和 DSH 的主要差异：没有 cordis
//
// DSH 那边 DomainFacility 是一个 cordis 插件，靠 apply() 把自己 provide 到
// ctx.storage.domain 上，配置从 Config schema 来，日志从 ctx.logger 来。
// Go 里这些都由装配方显式递进来（见 [Config]），设施建出来之后挂不挂到中枢上、
// 挂在哪个形态名下，也是装配方的事——[FormName] 只是约定俗成的那个名字。
package domain

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/storage"
)

// FormName 是这个设施在存储中枢上约定的形态名。
//
// 源: packages/storage/storage-domain/src/index.ts:31-44
//
// 新增: DSH 那边这个名字是 StorageForms 接口的一个键，靠 TypeScript 的声明合并
// 加上去，于是 ctx.storage.domain 是一个有类型的属性、写错就是编译错误。
// Go 没有声明合并，形态名只能是一个普通字符串——所以把它定成常量，
// 挂载方和解析方引用同一个标识符，而不是各自敲一遍字面量。
const FormName = "domain"

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/storage/storage-domain/src/invariant.ts:8
const PackageName = "@deepseek-ai/dsh-storage-domain"

// Config 是建一个 [Facility] 需要的东西。
//
// 源: packages/storage/storage-domain/src/index.ts:46-62
type Config struct {
	// Storage 是存储中枢，域从它那里按名字解析后端。必填。
	Storage *storage.Storage

	// Backend 是默认后端名：[Config.Routes] 里没有单独指定的域都落在它上面。必填。
	//
	// 源: packages/storage/storage-domain/src/index.ts:48-52
	//
	// **没有缺省值**，和 DSH 一致。一个猜出来的默认后端意味着「数据存哪儿」
	// 这件事没人做过决定，而它决定了断电之后还剩下什么。
	Backend string

	// Routes 是逐域的后端覆盖：域名 → 后端名。可以为 nil。
	//
	// 源: packages/storage/storage-domain/src/index.ts:53-61
	//
	// 它存在的理由和 storage.BackendRegistry 是同一条：把某一类数据搬到另一份介质上
	// （放大的搬到对象存储、要事务的搬到数据库），应该只改一行配置，
	// 而不是改所有登记方的代码。
	Routes map[string]string

	// Logger 记「变更订阅者炸了但那次写是好的」这类事，留空用 slog.Default()。
	//
	// 留空**不是**丢弃：这里记的正是没人会主动去查、却必须留下痕迹的那类事。
	// 要静音的装配方显式递一个装着 slog.DiscardHandler 的 logger。
	Logger *slog.Logger
}

// changedSubscription 是一次订阅留下的那一条，id 用来精确退订。
//
// 新增: 订阅表是**切片**不是 map，和本仓库 settings.Provider 同源：
// Go 的 map 遍历顺序是故意随机的，而分发顺序随机会让「谁先看到这次变更」
// 每次运行都不一样，一个依赖顺序的 bug 于是变成偶发。
type changedSubscription struct {
	id       uint64
	listener ChangedListener
}

// Facility 是域设施：打开域、按名字找回域、广播变更。
//
// 源: packages/storage/storage-domain/src/index.ts:64-178
//
// 零值不可用，请用 [New]。
type Facility struct {
	storage *storage.Storage
	backend string
	routes  map[string]string
	logger  *slog.Logger

	// mutex 保护 domains。
	mutex sync.Mutex
	// domains 是「域名 → 已打开的域」，**nil 值表示这个名字正在被打开**。
	//
	// 新增: DSH 用两个集合——domains（开好的）和 reserved（占着名字的，
	// 在异步 open 开始之前就加进去）。两个集合的每一条摘除路径都得同时照顾到两边，
	// 漏一处就是一个永远占着的名字。Go 这边合成一张表，用 nil 当「正在打开」的哨兵：
	// 占名、开成、开砸、关掉各只动一处，摘不干净这件事在结构上就不成立。
	domains map[string]*Domain

	listenerMutex  sync.Mutex
	nextListenerID uint64
	listeners      []changedSubscription
}

// New 建一个域设施。
//
// 源: packages/storage/storage-domain/src/index.ts:194-220
//
// 建出来时**不碰介质**——设施只是登记和路由，真正打开单元是 [Facility.Open] 的事。
func New(config Config) (*Facility, error) {
	if config.Storage == nil {
		return nil, fmt.Errorf("storage/domain: 建设施需要一个存储中枢")
	}
	if config.Backend == "" {
		// 没有默认值：见 [Config.Backend]。
		return nil, fmt.Errorf("storage/domain: 建设施需要指定默认后端名")
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	// 路由表拷一份：装配方递进来的 map 之后被改掉的话，
	// 「这个域存在哪儿」会在运行途中变，而已经打开的域还连着旧后端。
	routes := make(map[string]string, len(config.Routes))
	for name, backend := range config.Routes {
		routes[name] = backend
	}
	return &Facility{
		storage: config.Storage,
		backend: config.Backend,
		routes:  routes,
		logger:  logger,
		domains: map[string]*Domain{},
	}, nil
}

// Open 按一份声明打开一个域：解析后端 → 打开单元 → 整份读进内存 → 逐条校验。
//
// 源: packages/storage/storage-domain/src/index.ts:84-156
//
// 返回时这个域的内存态已经和介质一致，读可以立刻开始。
//
// 失败的种类：声明本身不合法（见 [Spec.Validate]）；域名已经开着
// （[CodeAlreadyOpen]）；路由到的后端不提供键值形态（[CodeFacetUnsupported]）；
// 介质上某条记录过不了声明的校验（[CodeInvalidRecord]，带 [RecordSlot]）；
// 以及后端自己的失败——那些**原样穿过去**，仍然是 *storage.Error（见 [ErrorCode]）。
//
// **一条记录坏了整个域就打不开**，不是跳过它。跳过意味着交出一个「大部分对」的域，
// 而调用方没有任何办法知道自己少看了什么；随后一次针对那个键的写还会把坏数据覆盖掉，
// 于是连现场都没了。
//
// 生命周期归调用方：拿到的 *[Domain] 要自己 [Domain.Close]（见那里的说明）。
func (f *Facility) Open(ctx context.Context, spec Spec) (*Domain, error) {
	// 先验声明，再碰介质：一份配错的声明不会在介质上留下半个单元。
	if err := spec.Validate(); err != nil {
		return nil, err
	}

	if err := f.reserve(spec.Name); err != nil {
		return nil, err
	}
	opened := false
	defer func() {
		// 没开成就把名字让出来，让调用方改完能重来。
		if !opened {
			f.onClosed(spec.Name)
		}
	}()

	route := f.backend
	if override, ok := f.routes[spec.Name]; ok {
		route = override
	}
	backend, err := f.storage.Backend.Get(route)
	if err != nil {
		return nil, err
	}
	facet, ok := storage.KV(backend)
	if !ok {
		return nil, newError(CodeFacetUnsupported,
			"域 %q 路由到的后端 %q 不提供键值形态", spec.Name, route)
	}

	unit, err := facet.Open(ctx, spec.Descriptor())
	if err != nil {
		return nil, err
	}

	domain, err := f.load(ctx, spec, unit)
	if err != nil {
		// 单元已经开出来了，这条路上没人再会用它——不关就是一个泄漏的句柄，
		// 而且它占着这个单元名，下一次重试会在后端那一层撞上「没关就开第二次」。
		// 关闭本身的失败**不覆盖**原因：调用方要看的是记录为什么不合法，
		// 而不是清理时的次生错误。
		if closeErr := unit.Close(ctx); closeErr != nil {
			f.logger.Warn("storage/domain: 打开失败后释放单元也失败",
				slog.String("domain", spec.Name), slog.Any("error", closeErr))
		}
		return nil, err
	}

	f.mutex.Lock()
	f.domains[spec.Name] = domain
	f.mutex.Unlock()

	opened = true
	return domain, nil
}

// reserve 占住一个域名，占不住说明它已经开着（或者正在被别人打开）。
//
// 源: packages/storage/storage-domain/src/index.ts:85-90
//
// 「正在被打开」和「已经开好」在这里是同一件事：两者都意味着这个名字后面
// 已经有（或者即将有）一个持有介质的句柄，再开一个就是两份状态互相覆盖。
func (f *Facility) reserve(name string) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	if _, taken := f.domains[name]; taken {
		return newError(CodeAlreadyOpen, "域 %q 已经打开了", name)
	}
	f.domains[name] = nil
	return nil
}

// load 把单元的整份快照读进一个新建的 [Domain]，逐条过声明的校验。
//
// 源: packages/storage/storage-domain/src/index.ts:107-140
func (f *Facility) load(ctx context.Context, spec Spec, unit storage.KVUnit) (*Domain, error) {
	snapshot, err := unit.LoadAll(ctx)
	if err != nil {
		return nil, err
	}

	domain := &Domain{
		facility: f,
		spec:     spec,
		unit:     unit,
		tables:   make(map[string]*tableState, len(spec.Tables)),
	}

	for _, table := range spec.Tables {
		state := &tableState{spec: table, records: map[string]record{}}
		// 声明过但介质上一条都没有的表，快照里是一张空表而不是缺席
		// （见 storage.Snapshot.Tables）；这里对两种情况都走同一条路。
		for key, raw := range snapshot.Tables[table.name] {
			typed, decodeErr := table.decode(raw)
			if decodeErr != nil {
				return nil, invalidRecord(spec.Name, table.name, key, decodeErr)
			}
			state.records[key] = record{typed: typed, raw: raw}
		}
		domain.tables[table.name] = state
	}

	if spec.Global != nil {
		// 介质上的 null 就是「从来没写过」，此时供出声明里的初值。
		// 这条哨兵约定正是 [DefineGlobal] 要挡住「能编码成 null 的全局值」的原因。
		if isJSONNull(snapshot.Global) {
			domain.global = record{typed: spec.Global.initial, raw: spec.Global.initialRaw}
		} else {
			typed, decodeErr := spec.Global.decode(snapshot.Global)
			if decodeErr != nil {
				return nil, invalidRecord(spec.Name, "", "", decodeErr)
			}
			domain.global = record{typed: typed, raw: snapshot.Global}
		}
	}
	return domain, nil
}

// Get 按域名找回一个已经打开的域，**不带类型**。
//
// 源: packages/storage/storage-domain/src/index.ts:158-167
//
// 这是诊断面，配合 [Domain.RawRecord] / [Domain.RawGlobal] 使用；
// 拿类型化句柄走 [TableOf] / [GlobalOf]。
//
// 正在被打开（还没读完）的域**当作不存在**：它的内存态还没建完，
// 交出去等于让人读一个半截的域。
func (f *Facility) Get(name string) (*Domain, bool) {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	domain, ok := f.domains[name]
	return domain, ok && domain != nil
}

// Names 返回当前打开着的域名，按字典序排好，供诊断使用。
//
// 正在被打开的名字不算在内，理由同 [Facility.Get]。
func (f *Facility) Names() []string {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	names := make([]string, 0, len(f.domains))
	for name, domain := range f.domains {
		if domain != nil {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// CloseAll 关掉这个设施上还开着的所有域。
//
// 源: packages/storage/storage-domain/src/index.ts:169-177
//
// 这是**兜底**，不是常规路径：域的生命周期归拿到句柄的那一方，装配方卸载设施时
// 用它收掉漏关的那些。DSH 那边是 ctx.effect 的 dispose 回调，Go 里由装配方自己调。
//
// 每个域都会被试着关一次，失败不打断后面的——一个关不掉的域不该让其余的都留在那儿。
// 全部失败用 errors.Join 合起来返回。
func (f *Facility) CloseAll(ctx context.Context) error {
	f.mutex.Lock()
	open := make([]*Domain, 0, len(f.domains))
	for _, domain := range f.domains {
		if domain != nil {
			open = append(open, domain)
		}
	}
	f.mutex.Unlock()

	// 顺序固定：关闭会发事件（排队中的写会跑完），随机顺序会让事件序列不可复现。
	sort.Slice(open, func(i, j int) bool { return open[i].spec.Name < open[j].spec.Name })

	var failures []error
	for _, domain := range open {
		if err := domain.Close(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// onClosed 把一个域名让出来，可以重新打开。
//
// 源: packages/storage/storage-domain/src/index.ts:141-145
//
// 由 [Domain.runClose] 在拆解的最后一步调用，也由 [Facility.Open] 的失败路径调用。
func (f *Facility) onClosed(name string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	delete(f.domains, name)
}

// Subscribe 订阅这个设施上所有域的变更，返回退订函数。
//
// 源: packages/storage/storage-domain/src/events.ts:36-48
//
// 新增: DSH 那边订阅走 cordis 的全局事件总线（ctx.on('domain/changed', ...)）。
// Go 里没有那条总线，订阅落在设施上——一个设施就是一个域的集合，
// 那正是这条事件流天然的边界。
//
// 订阅者要守的两条见 [ChangedListener]：不许阻塞，不许回头写同一个域。
//
// 退订函数可以重复调用，第二次是空操作。
func (f *Facility) Subscribe(listener ChangedListener) func() {
	if listener == nil {
		return func() {}
	}
	f.listenerMutex.Lock()
	id := f.nextListenerID
	f.nextListenerID++
	f.listeners = append(f.listeners, changedSubscription{id: id, listener: listener})
	f.listenerMutex.Unlock()

	return func() {
		f.listenerMutex.Lock()
		defer f.listenerMutex.Unlock()
		for index, candidate := range f.listeners {
			if candidate.id == id {
				// 切满容量再 append，避免和正在分发的那份快照共用底层数组。
				f.listeners = append(f.listeners[:index:index], f.listeners[index+1:]...)
				return
			}
		}
	}
}

// emit 把一条已经落盘的变更广播给所有订阅者。
//
// 源: packages/storage/storage-domain/src/domain.ts:246-261
//
// 三条规则，和本仓库 settings.fanOut 同源：
//
//  1. **每一个订阅者都跑到。** 一个订阅者炸掉不许掐断后面的——变更已经提交了，
//     没跑到的那几个从此和介质不一致，而它们永远不会知道。
//  2. **普通失败只记日志。** 提交点已经过了，介质和内存都拿着新值，
//     一次已经成功的写不该因为旁边有人看崩了就变成失败。
//  3. **不变量违例例外：等所有订阅者都跑完之后重新抛出**，且只抛第一条。
//     不变量违例意味着程序写错了（见 invariants 包），它必须传到发起方手里。
func (f *Facility) emit(change Changed) {
	f.listenerMutex.Lock()
	// 先拷一份再分发：订阅者在回调里退订是合法的，而那会改到这张表。
	listeners := make([]ChangedListener, 0, len(f.listeners))
	for _, subscription := range f.listeners {
		listeners = append(listeners, subscription.listener)
	}
	f.listenerMutex.Unlock()

	var invariantFailure *invariants.Error

	for _, listener := range listeners {
		func() {
			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}
				if failure, isInvariant := recovered.(*invariants.Error); isInvariant {
					// 只留第一条：后面的多半是同一个原因的连锁反应，
					// 而抛出去的只能有一个，抛最早的那个离现场最近。
					if invariantFailure == nil {
						invariantFailure = failure
					}
					return
				}
				f.warnListenerFailure(change, recovered)
			}()
			listener(change)
		}()
	}

	if invariantFailure != nil {
		panic(invariantFailure)
	}
}

// warnListenerFailure 是订阅者失败时留下的那条诊断。
//
// 源: packages/storage/storage-domain/src/domain.ts:255-260
//
// **不记值**：记录里完全可能有敏感数据，而一个 panic 的载荷里也常带着刚读到的那一段。
// 域名、表名、键足够定位到是哪一次变更了。
func (f *Facility) warnListenerFailure(change Changed, recovered any) {
	f.logger.Warn("storage/domain: 一个变更订阅者失败了",
		slog.String("domain", change.Domain),
		slog.String("table", change.Table),
		slog.String("key", change.Key),
		slog.String("operation", string(change.Operation)),
		slog.Any("panic", recovered))
}

// describeNames 把一串名字排成错误信息里那一段，空的时候给一个说得通的词。
//
// 空列表直接拼出来是一段空白，读的人分不清「一个都没有」和「这句话写漏了」。
func describeNames(names []string) string {
	if len(names) == 0 {
		return "无"
	}
	return strings.Join(names, ", ")
}
