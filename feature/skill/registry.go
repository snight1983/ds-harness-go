// 本文件的作用：技能注册表本身——谁能贡献技能、同名的怎么排、一个 agent 看得见
// 其中哪些、以及一份正文怎么按名字取出来。值那一侧在 skill.go。
//
// 源: packages/skill/skill/src/index.ts:278-868

package skill

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// ErrInvalidConfig 表示一份注册表配置本身就不成立。
var ErrInvalidConfig = errors.New("skill: 配置不成立")

// ErrInvalidSkill 表示一份技能（候选、运行期注册、或者读出来的正文）不合法。
//
// 源: packages/skill/skill/src/index.ts:708-768
var ErrInvalidSkill = errors.New("skill: 技能不合法")

// registration 是一次确切的提供方注册。
//
// 源: packages/skill/skill/src/index.ts:311-315（RegisteredProvider）
//
// 新增: DSH 拿提供方对象本身做身份比较（`layer.providers.get(name)?.provider === provider`），
// 为的是让一次迟到的失效通知认得出「表里现在这一份还是不是当初那一次注册」。
// Go 里接口值的 == 在动态类型不可比较时会 panic，而提供方是使用方给的任意类型——
// 本包不该因为对方用了一个含切片的结构体就崩掉。所以身份另立成这个指针。
type registration struct {
	// provider 是这次注册的提供方。
	provider Provider
	// order 是注册表范围内单调递增的注册序号，也就是一层之内 rank 相同时的第二排序键。
	order int
}

// indexedCandidate 是一条候选，外加定它在一层之内排第几的那几个序号。
//
// 源: packages/skill/skill/src/index.ts:301-308
type indexedCandidate struct {
	// candidate 是提供方报出来的那条目录项。
	candidate Candidate
	// provider 是持有它的提供方，取正文时找的就是它。
	provider Provider
	// providerOrder 是提供方的注册序号；运行期技能固定是 -1，所以它们排在所有提供方前面。
	providerOrder int
	// localOrder 是这条候选在它那个提供方报出来的清单里排第几。
	localOrder int
	// layer 是产出它的那一层，好让一次「读出来的正文过期了」的失效通知确认那次注册还活着。
	layer *layer
	// registration 是产出它的那一次提供方注册；运行期技能没有，为 nil。
	registration *registration
}

// layerCollectResult 是一层的发现结果。
//
// 源: packages/skill/skill/src/index.ts:317-320
type layerCollectResult struct {
	entries   []indexedCandidate
	cacheable bool
}

// collectResult 是合并完各层之后的发现结果。
//
// 源: packages/skill/skill/src/index.ts:322-325
type collectResult struct {
	// entries 按技能名索引。
	//
	// 新增: DSH 用 JS 的 Map，它自带插入顺序。Go 的 map 无序，但这里顺序**不是语义**：
	// 读出来之后要么按名字查（[Registry.Get]），要么整个按名字排序（[Registry.Snapshot]），
	// 两条路都用不上插入顺序。所以一张普通 map 就够了。
	entries map[string]indexedCandidate
	// cacheable 表示这次发现跑完了，可以进缓存。
	cacheable bool
}

// layer 是一个作用域在这张注册表里的全部贡献。
//
// 源: packages/skill/skill/src/index.ts:328-345（SkillLayer）
type layer struct {
	// providers 是这一层注册的提供方，按插入顺序。
	providers *scope.NamedEntries[*registration]
	// runtime 是这一层运行期注册的技能。
	//
	// 新增: DSH 是 `Map<string, SkillDefinition>`。这里用 [scope.NamedEntries]，
	// 一是它自带「只撤销这一次登记」的幂等 undo，二是同名先到先得这件事正好落在
	// 它的 Insert 上。存指针是为了让候选的 locator 指得住同一份定义。
	runtime *scope.NamedEntries[*Definition]
}

// newLayer 造一层。key 为 nil 表示这是全局层，只影响重名时的报错措辞。
//
// 源: packages/skill/skill/src/index.ts:334-338
func newLayer(key *scope.Key) *layer {
	return &layer{
		providers: scope.NewNamedEntries[*registration](func(name string) error {
			if key == nil {
				return fmt.Errorf("a skill provider named %q is already registered", name)
			}
			return fmt.Errorf("a skill provider named %q is already registered in this scope", name)
		}),
		runtime: scope.NewNamedEntries[*Definition](func(name string) error {
			return fmt.Errorf("runtime skill %q is already registered in this layer", name)
		}),
	}
}

// IsEmpty 表示这一层的每一张表都空了，[scope.Layers] 靠它回收空层。
//
// 源: packages/skill/skill/src/index.ts:341-343
func (l *layer) IsEmpty() bool {
	return l.providers.IsEmpty() && l.runtime.IsEmpty()
}

// Options 是造一个 [Registry] 的选项。
//
// 源: packages/skill/skill/src/index.ts:279-283（Config）
type Options struct {
	// CollectCacheMaxEntries 是缓存里最多留几份跑完的目录。
	//
	// 新增: 为 0 表示用默认值 128。DSH 那边 `collectCacheMaxEntries: 0` 会被
	// assertPositiveInteger 拒掉，因为它分得清「没给」和「给了 0」；Go 的零值分不清，
	// 所以 0 落到默认那一支，只有负数才是配错了。
	CollectCacheMaxEntries int

	// Logger 记那些被咽下去的提供方失败和被压掉的同名技能，为 nil 时用 [slog.Default]。
	Logger *slog.Logger

	// OnChange 在目录可能变了的时候被调一次，可以为 nil。
	//
	// 源: packages/skill/skill/src/index.ts:289-298（`skills/change` 事件）
	//
	// 新增: DSH 把每个监听器的失败都兜住，免得一个监听器把注册这件事否掉。Go 这边
	// 这里只有装配方给的**一个**回调，它自己抛出来的问题是它自己的 bug——和
	// tools 的 OnChange 一样直接调，不代它兜。
	OnChange func()
}

// Registry 是分层的技能注册表。
//
// 源: packages/skill/skill/src/index.ts:347-662（SkillRegistry）
//
// 注册落在调用方作用域那一层：宿主和仓库级插件落全局层，挂在某个 agent 预设常驻
// 组合里的插件落那个预设自己那层。读的时候把全局层和视角作用域这条链合起来，
// 近的那层整个盖住远的；rank 只在一层之内决定同名谁赢。
type Registry struct {
	// collectCacheMaxEntries 是缓存上限。
	collectCacheMaxEntries int
	// logger 记被咽下去的失败。
	logger *slog.Logger
	// onChange 是目录变更通知，可以为 nil。
	onChange func()
	// layers 是全局层加各作用域的覆盖层。
	layers *scope.Layers[*layer]

	// mutex 护住下面这一组状态。一次 collect 是真 I/O，几个消费方可以并发地读。
	mutex sync.Mutex
	// collectCache 是按 [Registry.collectCacheKey] 索引的目录缓存。
	collectCache map[string]map[string]indexedCandidate
	// cacheOrder 是缓存键的插入顺序，超上限时从最老的那头淘汰。
	//
	// 新增: DSH 靠 JS Map 的插入顺序取 `keys().next()`，那是 FIFO 不是 LRU。
	// Go 的 map 无序，所以顺序自己存一份，淘汰策略照抄 FIFO。
	cacheOrder []string
	// revision 每发生一次注册变动就加一，缓存键带着它，于是旧键再也命不中。
	revision int
	// nextProviderOrder 是下一次提供方注册拿到的序号。
	nextProviderOrder int
	// scopeIDs 给作用域键发缓存键里用的稳定编号。
	//
	// 新增: DSH 用 WeakMap，靠 GC 回收。Go 没有弱表，所以这是一张普通 map；
	// 它会把作用域键留住，但缓存本身有上限、而且每次注册变动都整个清空，
	// 所以留住的量跟着一起有界。
	scopeIDs map[*scope.Key]int
	// nextScopeID 是下一个作用域编号。
	nextScopeID int
}

// NewRegistry 验一份选项，造出这张注册表。
//
// 源: packages/skill/skill/src/index.ts:374-378
func NewRegistry(options Options) (*Registry, error) {
	maxEntries := options.CollectCacheMaxEntries
	if maxEntries == 0 {
		maxEntries = defaultCollectCacheMaxEntries
	}
	if maxEntries < 1 {
		return nil, fmt.Errorf("%w: CollectCacheMaxEntries 不能小于 1", ErrInvalidConfig)
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	registry := &Registry{
		collectCacheMaxEntries: maxEntries,
		logger:                 logger,
		onChange:               options.OnChange,
		collectCache:           map[string]map[string]indexedCandidate{},
		scopeIDs:               map[*scope.Key]int{},
		nextScopeID:            1,
	}
	layers, err := scope.NewLayers(
		func(key *scope.Key) (*layer, error) { return newLayer(key), nil },
		func() error {
			registry.invalidateCache()
			return nil
		},
	)
	if err != nil {
		// 走不到：scope.NewLayers 只在造全局层时失败，而上面那个构造函数不会失败。
		// 它是 scope 那一侧的签名，本包无权改；照实转出去比在这里吞掉它诚实。
		return nil, err
	}
	registry.layers = layers
	return registry, nil
}

// RegisterProvider 把一处技能来源登记进 owner 那一层，返回撤销这次登记的函数。
//
// 源: packages/skill/skill/src/index.ts:380-429
//
// create 是**同步**的：远端初始化、认证、发现这些事都该在提供方的 List 里做，
// 而不是在装配时把整个进程吊住。它拿到的 [ProviderControl] 是这一次确切注册的
// 生命周期和失效通知——注册撤销时那个 Context 取消，之后再调 Invalidate 什么都不会发生。
//
// 名字 "runtime" 是给 [Registry.Register] 那条路留的，提供方不许占。
func (r *Registry) RegisterProvider(
	ctx context.Context,
	owner *scope.Scope,
	create func(ProviderControl) (Provider, error),
) (func(context.Context) error, error) {
	if create == nil {
		return nil, fmt.Errorf("%w: RegisterProvider 需要一个构造函数", ErrInvalidConfig)
	}
	// 这条生命周期**不挂在 ctx 下面**：ctx 是「这一次注册调用」的上下文，注册返回之后
	// 它就该结束了；而这条生命周期要活到这次注册被撤销为止，两者不是一回事。
	lifecycle, cancel := context.WithCancelCause(context.Background())

	var (
		mutex   sync.Mutex
		placed  *layer
		current *registration
	)
	control := ProviderControl{
		Context: lifecycle,
		Invalidate: func() {
			mutex.Lock()
			target, live := placed, current
			mutex.Unlock()
			if target == nil || live == nil {
				return
			}
			// 确认表里现在这一份仍然是这一次注册。撤销之后同名的东西又登记了一遍时，
			// 这个迟到的失效通知说的已经不是同一件事了。
			if held, ok := target.providers.Get(live.provider.Name()); !ok || held != live {
				return
			}
			r.invalidateCache()
		},
	}

	provider, err := create(control)
	if err != nil {
		cancel(err)
		return nil, err
	}
	if provider == nil {
		err := fmt.Errorf("%w: 构造函数没有交出提供方", ErrInvalidConfig)
		cancel(err)
		return nil, err
	}
	name := provider.Name()
	if name == runtimeProvider {
		err := fmt.Errorf("%q is reserved for runtime skill registrations", runtimeProvider)
		cancel(err)
		return nil, err
	}

	r.mutex.Lock()
	order := r.nextProviderOrder
	r.nextProviderOrder++
	r.mutex.Unlock()

	entry := &registration{provider: provider, order: order}
	dispose, err := r.layers.Effect(ctx, owner, func(target *layer) (func(), error) {
		undo, insertErr := target.providers.Insert(name, entry)
		if insertErr != nil {
			return nil, insertErr
		}
		mutex.Lock()
		placed, current = target, entry
		mutex.Unlock()
		return func() {
			mutex.Lock()
			placed, current = nil, nil
			mutex.Unlock()
			undo()
			cancel(fmt.Errorf("skill provider %q disposed", name))
		}, nil
	}, scope.EffectOptions{Label: "skills.RegisterProvider()"})
	if err != nil {
		cancel(err)
		return nil, err
	}
	return dispose, nil
}

// Register 把一份运行期技能登记进 owner 那一层，返回撤销这次登记的函数。
//
// 源: packages/skill/skill/src/index.ts:431-461
//
// 一层之内同名先到先得：后来的那份记一条警告、拿到一个空操作的撤销函数，
// 所以它撤不掉赢家。
func (r *Registry) Register(
	ctx context.Context,
	owner *scope.Scope,
	skill Registration,
) (func(context.Context) error, error) {
	if err := validateRuntimeSkill(skill); err != nil {
		return nil, err
	}
	if owner == nil {
		return nil, fmt.Errorf("%w: Register 需要一个持有这次注册的作用域", ErrInvalidConfig)
	}
	// 先看一眼那一层里有没有同名的。用 Peek 而不是直接插，是因为「已经有了」这件事
	// 要换回一个**空操作**而不是错误，而走 Effect 会把那一层建出来。
	key := owner.Key()
	existing, hasLayer := r.layers.Global(), true
	if key != nil {
		existing, hasLayer = r.layers.Peek(key)
	}
	if hasLayer && existing.runtime.Has(skill.Name) {
		r.logger.Warn(fmt.Sprintf("runtime skill %q ignored because it is already registered", skill.Name))
		return func(context.Context) error { return nil }, nil
	}

	invocation := InvocationPolicy{ModelInvocable: true, UserInvocable: true}
	if skill.Invocation != nil {
		invocation = *skill.Invocation
	}
	providerName := skill.Provider
	if providerName == "" {
		providerName = runtimeProvider
	}
	definition := &Definition{
		Summary: Summary{
			Name:         skill.Name,
			Description:  skill.Description,
			WhenToUse:    skill.WhenToUse,
			Invocation:   invocation,
			Source:       skill.Source,
			Provider:     providerName,
			ResourceBase: skill.ResourceBase,
		},
		Content:  skill.Content,
		Path:     skill.Path,
		Metadata: skill.Metadata,
	}
	return r.layers.Effect(ctx, owner, func(target *layer) (func(), error) {
		return target.runtime.Insert(definition.Name, definition)
	}, scope.EffectOptions{Label: "skills.Register()"})
}

// List 列出一个视角看得见的、不区分调用方的技能摘要。
//
// 源: packages/skill/skill/src/index.ts:463-473
//
// 「不区分调用方」是说这里不施加 [IsModelInvocable] / [IsUserInvocable]——
// 那两条由消费方在自己那道边界上施加。
func (r *Registry) List(ctx context.Context, options ViewOptions) ([]Summary, error) {
	snapshot, err := r.Snapshot(ctx, options)
	if err != nil {
		return nil, err
	}
	return snapshot.Skills, nil
}

// Snapshot 观察当前目录，并说明这次发现是不是在一个稳定的 revision 里跑完的。
//
// 源: packages/skill/skill/src/index.ts:475-490
//
// [CatalogSnapshot].Complete 为假时消费方应当留着上一份好的，下一个请求边界再试。
func (r *Registry) Snapshot(ctx context.Context, options ViewOptions) (CatalogSnapshot, error) {
	collected, err := r.collect(ctx, options)
	if err != nil {
		return CatalogSnapshot{}, err
	}
	skills := make([]Summary, 0, len(collected.entries))
	for _, entry := range collected.entries {
		// 新增: DSH 有一个 toSummary 把那几个字段挑出来。Go 这边 [Candidate] 就是
		// 嵌了一个 [Summary]，挑字段这件事是编译期的，那个函数不需要。
		skills = append(skills, entry.candidate.Summary)
	}
	slices.SortFunc(skills, func(left, right Summary) int {
		return strings.Compare(left.Name, right.Name)
	})
	return CatalogSnapshot{Skills: skills, Complete: collected.cacheable}, nil
}

// Get 把赢家候选读成完整的技能正文，取不到就交回 nil。
//
// 源: packages/skill/skill/src/index.ts:492-518
//
// 取正文时把提供方自己那份不透明 locator 原样递回去。取消在选完之后**重新查一遍**
// （包括命中缓存那条路），并且和读正文本身赛跑——一个不合作的提供方不该把调用方吊死。
//
// 读出来的名字和候选对不上时，说明这份目录已经过期了：作废缓存，并且当作没找到。
func (r *Registry) Get(ctx context.Context, name string, options ViewOptions) (*Definition, error) {
	if !IsName(name) {
		return nil, nil
	}
	collected, err := r.collect(ctx, options)
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	match, found := collected.entries[name]
	if !found {
		return nil, nil
	}
	definition, err := waitWithCancel(ctx, func() (*Definition, error) {
		return match.provider.Get(ctx, match.candidate, options.LookupOptions)
	})
	if err != nil {
		return nil, err
	}
	if definition == nil {
		return nil, nil
	}
	if err := validateDefinition(*definition); err != nil {
		return nil, err
	}
	if definition.Name != match.candidate.Name {
		r.invalidateEntry(match)
		return nil, nil
	}
	return definition, nil
}

// collect 取一份目录，优先走缓存。
//
// 源: packages/skill/skill/src/index.ts:520-550
func (r *Registry) collect(ctx context.Context, options ViewOptions) (collectResult, error) {
	if ctx.Err() != nil {
		return collectResult{}, context.Cause(ctx)
	}
	for attempt := 1; ; attempt++ {
		// 作用域链是缓存键的一部分，而不是被当成不变量：一次空会话重组会把一个已有的
		// 作用域换个父亲挂上去，这件事根本不碰这张注册表——只有把链编进键里，
		// 下一次读才看得见新的预设。
		revision, key, cached, hit := r.probeCache(options)
		if hit {
			return collectResult{entries: cached, cacheable: true}, nil
		}

		result, err := r.collectFresh(ctx, options)
		if err != nil {
			return collectResult{}, err
		}
		if ctx.Err() != nil {
			return collectResult{}, context.Cause(ctx)
		}
		if revision != r.currentRevision() {
			// 发现期间有人改了注册表。重来一次；再撞上就把这批交出去但不许进缓存。
			if attempt < maxCollectAttempts {
				continue
			}
			return collectResult{entries: result.entries, cacheable: false}, nil
		}
		if result.cacheable {
			r.storeCache(key, result.entries)
		}
		return result, nil
	}
}

// collectFresh 问遍各层，把结果按遮蔽规则合起来。
//
// 源: packages/skill/skill/src/index.ts:552-566
//
// 先全局层，再按父链**远祖在前、本作用域在最后**叠上去，于是近的那层的同名项
// 替换掉远的——和工具注册表同一条遮蔽规则。rank 只在一层之内决定同名谁赢。
func (r *Registry) collectFresh(ctx context.Context, options ViewOptions) (collectResult, error) {
	targets := append([]*layer{r.layers.Global()}, r.layers.ChainLayers(options.Scope)...)
	merged := map[string]indexedCandidate{}
	cacheable := true
	for _, target := range targets {
		collected, err := r.collectLayer(ctx, target, options.LookupOptions)
		if err != nil {
			return collectResult{}, err
		}
		if !collected.cacheable {
			cacheable = false
		}
		for _, entry := range collected.entries {
			merged[entry.candidate.Name] = entry
		}
	}
	return collectResult{entries: merged, cacheable: cacheable}, nil
}

// collectLayer 把一层的候选排好序，同名只留第一个。
//
// 源: packages/skill/skill/src/index.ts:568-583
func (r *Registry) collectLayer(
	ctx context.Context,
	target *layer,
	options LookupOptions,
) (layerCollectResult, error) {
	collected, err := r.listLayerCandidates(ctx, target, options)
	if err != nil {
		return layerCollectResult{}, err
	}
	// (rank, providerOrder, localOrder) 三元组在一层之内是唯一的，所以这是一个全序，
	// 排序稳不稳定都得到同一个结果。
	slices.SortFunc(collected.entries, compareIndexedCandidates)
	seen := map[string]struct{}{}
	result := make([]indexedCandidate, 0, len(collected.entries))
	for _, entry := range collected.entries {
		name := entry.candidate.Name
		if _, duplicate := seen[name]; duplicate {
			r.logger.Warn(fmt.Sprintf(
				"skill %q from %s ignored because a higher-priority skill already exists",
				name, entry.candidate.Source))
			continue
		}
		seen[name] = struct{}{}
		result = append(result, entry)
	}
	return layerCollectResult{entries: result, cacheable: collected.cacheable}, nil
}

// listLayerCandidates 问出一层里的全部候选：先运行期技能，再每个提供方。
//
// 源: packages/skill/skill/src/index.ts:585-620
//
// 一个提供方报错只会让它自己被跳过，并且把这次发现标成不可缓存——别的提供方的
// 技能仍然是可用的，而一份少了东西的目录不该被记住。
func (r *Registry) listLayerCandidates(
	ctx context.Context,
	target *layer,
	options LookupOptions,
) (layerCollectResult, error) {
	if ctx.Err() != nil {
		return layerCollectResult{}, context.Cause(ctx)
	}
	var candidates []indexedCandidate
	cacheable := true

	var runtimeSkills []*Definition
	for definition := range target.runtime.Values() {
		runtimeSkills = append(runtimeSkills, definition)
	}
	slices.SortFunc(runtimeSkills, func(left, right *Definition) int {
		return strings.Compare(left.Name, right.Name)
	})
	for index, definition := range runtimeSkills {
		candidates = append(candidates, indexedCandidate{
			candidate:     runtimeCandidate(definition),
			provider:      runtimeSkillProvider{},
			providerOrder: -1,
			localOrder:    index,
			layer:         target,
		})
	}

	for held := range target.providers.Values() {
		observation, err := waitWithCancel(ctx, func() (Observation, error) {
			return held.provider.List(ctx, options)
		})
		if err != nil {
			if ctx.Err() != nil {
				return layerCollectResult{}, context.Cause(ctx)
			}
			cacheable = false
			r.logger.Warn(fmt.Sprintf("skill provider %q skipped: %v", held.provider.Name(), err))
			continue
		}
		if observation.Incomplete {
			cacheable = false
		}
		for localOrder, candidate := range observation.Candidates {
			if err := validateCandidate(candidate, held.provider.Name()); err != nil {
				return layerCollectResult{}, err
			}
			candidates = append(candidates, indexedCandidate{
				candidate:     candidate,
				provider:      held.provider,
				providerOrder: held.order,
				localOrder:    localOrder,
				layer:         target,
				registration:  held,
			})
		}
	}
	return layerCollectResult{entries: candidates, cacheable: cacheable}, nil
}

// probeCache 在一把锁里读出当前 revision、算出缓存键、并试着命中。
//
// 新增: DSH 是单线程的，读 revision、算键、查缓存这三件事之间不可能插进别人。
// Go 里得自己保证它们看到的是同一个瞬间，否则一次并发注册会让「用旧 revision 算出来的键」
// 命中一份「新 revision 才该有的目录」。
func (r *Registry) probeCache(options ViewOptions) (
	revision int,
	key string,
	cached map[string]indexedCandidate,
	hit bool,
) {
	chain := scope.ChainOf(options.Scope)

	r.mutex.Lock()
	defer r.mutex.Unlock()

	revision = r.revision
	key = r.collectCacheKeyLocked(options.WorkspaceID, chain, revision)
	cached, hit = r.collectCache[key]
	return revision, key, cached, hit
}

// currentRevision 读一眼当前 revision。
func (r *Registry) currentRevision() int {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.revision
}

// storeCache 记下一份跑完的目录，超上限就从最老的那头淘汰。
//
// 源: packages/skill/skill/src/index.ts:541-547
func (r *Registry) storeCache(key string, entries map[string]indexedCandidate) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if _, exists := r.collectCache[key]; !exists {
		r.cacheOrder = append(r.cacheOrder, key)
	}
	r.collectCache[key] = entries
	for len(r.cacheOrder) > r.collectCacheMaxEntries {
		oldest := r.cacheOrder[0]
		r.cacheOrder = r.cacheOrder[1:]
		delete(r.collectCache, oldest)
	}
}

// invalidateCache 让整份缓存作废，并通知消费方。
//
// 源: packages/skill/skill/src/index.ts:622-626
func (r *Registry) invalidateCache() {
	r.mutex.Lock()
	r.revision++
	r.collectCache = map[string]map[string]indexedCandidate{}
	r.cacheOrder = nil
	r.mutex.Unlock()

	// 通知在锁外发：回调是装配方的代码，它回头读这张注册表是完全正当的。
	if r.onChange != nil {
		r.onChange()
	}
}

// invalidateEntry 在读出来的正文过期之后作废缓存，前提是产出这条目录项的那一次
// 注册**现在还活着**。
//
// 源: packages/skill/skill/src/index.ts:628-632
//
// 一次读正文可以活得比它选中的那次提供方注册还久；那种情况下缓存早就因为撤销
// 而作废过了，这里再加一次 revision 只会白白打掉别人刚建好的缓存。
func (r *Registry) invalidateEntry(entry indexedCandidate) {
	if entry.registration == nil {
		return
	}
	if held, ok := entry.layer.providers.Get(entry.provider.Name()); ok && held == entry.registration {
		r.invalidateCache()
	}
}

// scopeIDLocked 给一个作用域键发一个稳定编号。调用方必须已经持有 r.mutex。
//
// 源: packages/skill/skill/src/index.ts:634-642
func (r *Registry) scopeIDLocked(key *scope.Key) int {
	if id, exists := r.scopeIDs[key]; exists {
		return id
	}
	id := r.nextScopeID
	r.nextScopeID++
	r.scopeIDs[key] = id
	return id
}

// collectCacheKeyLocked 把「归属工作区 + 作用域链 + revision」编成一个缓存键。
// 调用方必须已经持有 r.mutex。
//
// 源: packages/skill/skill/src/index.ts:644-646
//
// 新增: DSH 用 JSON.stringify。Go 这边手拼，因为这个键从不出本包、也从不落盘，
// 排一遍 JSON 是白花的钱。工作区标识带上长度前缀是为了不让分隔符在它里面出现时
// 把两个不同的键拼成同一串——标识是不透明的，本包不该假设它里面有什么字符。
func (r *Registry) collectCacheKeyLocked(workspaceID sessionlog.WorkspaceID, chain []*scope.Key, revision int) string {
	var builder strings.Builder
	builder.WriteString(strconv.Itoa(revision))
	builder.WriteByte('\x00')
	builder.WriteString(strconv.Itoa(len(workspaceID)))
	builder.WriteByte(':')
	builder.WriteString(string(workspaceID))
	for _, key := range chain {
		builder.WriteByte('\x00')
		builder.WriteString(strconv.Itoa(r.scopeIDLocked(key)))
	}
	return builder.String()
}

// runtimeSkillProvider 是运行期注册那批技能的合成提供方。
//
// 源: packages/skill/skill/src/index.ts:681-690
type runtimeSkillProvider struct{}

// Name 实现 [Provider]。
func (runtimeSkillProvider) Name() string { return runtimeProvider }

// List 实现 [Provider]，永远交回空。
//
// 走不到：运行期技能是注册表自己塞进候选里的，这个提供方只负责 Get。
func (runtimeSkillProvider) List(context.Context, LookupOptions) (Observation, error) {
	return Observation{}, nil
}

// Get 实现 [Provider]，把候选里那份 locator 原样还回去。
func (runtimeSkillProvider) Get(_ context.Context, candidate Candidate, _ LookupOptions) (*Definition, error) {
	definition, ok := candidate.Locator.(*Definition)
	if !ok {
		// 走不到：这个提供方只出现在 runtimeCandidate 造的候选上。
		return nil, fmt.Errorf("%w: 运行期候选 %q 的 locator 不是一份技能定义", ErrInvalidSkill, candidate.Name)
	}
	return definition, nil
}

// runtimeCandidate 把一份运行期技能排成一条候选。
//
// 源: packages/skill/skill/src/index.ts:692-706
func runtimeCandidate(definition *Definition) Candidate {
	return Candidate{
		Summary:  definition.Summary,
		Rank:     runtimeRank,
		Locator:  definition,
		Path:     definition.Path,
		Metadata: definition.Metadata,
	}
}

// validateCandidate 验一条提供方报出来的目录项。
//
// 源: packages/skill/skill/src/index.ts:708-740
//
// 新增: DSH 那一摞检查里，`typeof x !== 'string'`、rank 是不是有限数这类全部由 Go 的
// 编译期和类型管了。留下来的只有类型系统说不了的那几条。
func validateCandidate(candidate Candidate, providerName string) error {
	if !IsName(candidate.Name) {
		return fmt.Errorf("%w: skill provider %q returned invalid skill name %q",
			ErrInvalidSkill, providerName, candidate.Name)
	}
	if candidate.Description == "" {
		return fmt.Errorf("%w: skill provider %q returned skill %q without a description",
			ErrInvalidSkill, providerName, candidate.Name)
	}
	if candidate.Provider != providerName {
		return fmt.Errorf("%w: skill provider %q returned skill %q for provider %q",
			ErrInvalidSkill, providerName, candidate.Name, candidate.Provider)
	}
	return nil
}

// validateRuntimeSkill 验一份运行期注册。
//
// 源: packages/skill/skill/src/index.ts:742-746
func validateRuntimeSkill(skill Registration) error {
	if !IsName(skill.Name) {
		return fmt.Errorf("%w: invalid skill name %q", ErrInvalidSkill, skill.Name)
	}
	if skill.Description == "" {
		return fmt.Errorf("%w: skill %q requires a description", ErrInvalidSkill, skill.Name)
	}
	return nil
}

// validateDefinition 验一份从提供方那边读出来的正文。
//
// 源: packages/skill/skill/src/index.ts:748-768
//
// 提供方可能是一个远端注册中心或者一个使用方自己写的解析器，所以读出来的东西
// 和候选一样是不可信的，得再验一遍。
func validateDefinition(skill Definition) error {
	if !IsName(skill.Name) {
		return fmt.Errorf("%w: loaded skill has invalid name %q", ErrInvalidSkill, skill.Name)
	}
	if skill.Description == "" {
		return fmt.Errorf("%w: loaded skill %q requires a description", ErrInvalidSkill, skill.Name)
	}
	return nil
}

// compareIndexedCandidates 是一层之内的排序：rank → 提供方注册顺序 → 提供方自己报出来的顺序。
//
// 源: packages/skill/skill/src/index.ts:807-811
func compareIndexedCandidates(left, right indexedCandidate) int {
	if left.candidate.Rank != right.candidate.Rank {
		return left.candidate.Rank - right.candidate.Rank
	}
	if left.providerOrder != right.providerOrder {
		return left.providerOrder - right.providerOrder
	}
	return left.localOrder - right.localOrder
}

// waitWithCancel 让一次提供方调用和 ctx 的取消赛跑。
//
// 源: packages/skill/skill/src/index.ts:820-843（waitWithAbort）
//
// 保留它的理由和 DSH 自己写的一样：一个不合作的提供方不该把调用方吊死。手段换成
// Go 的写法——调用跑在一个 goroutine 里，取消时立刻交回；那个 goroutine 自己会随
// 提供方返回而结束，结果写进一个有缓冲的 channel，所以它也不会因为没人收而泄漏。
func waitWithCancel[T any](ctx context.Context, run func() (T, error)) (T, error) {
	var zero T
	if ctx.Err() != nil {
		return zero, context.Cause(ctx)
	}
	type outcome struct {
		value T
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		value, err := run()
		done <- outcome{value: value, err: err}
	}()
	select {
	case result := <-done:
		return result.value, result.err
	case <-ctx.Done():
		return zero, context.Cause(ctx)
	}
}
