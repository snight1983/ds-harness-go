// 本文件的作用：名册本身——解算一份预设、保证它那份常驻装载只有一份、把 agent 认到
// 它下面去，以及那三件本地创作的写。
//
// 源: packages/preset/agent-presets/src/index.ts

package agentpresets

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/snight1983/ds-harness-go/core/scope"
)

// SettingsNamespace 是那份带着用户所选默认预设的设置命名空间。
//
// 源: packages/preset/agent-presets/src/index.ts:54-55（SETTINGS_NAMESPACE）
const SettingsNamespace = "agent-presets"

// DefaultSource 是盖在 [Config.Default] 上的那一层用户设置。
//
// 源: packages/preset/agent-presets/src/index.ts:64-68（AgentPresetSettings）
//
// 新增: DSH 那边 AgentPresets 直接 inject 了 settings 服务，拿一个
// `SettingsScope<AgentPresetSettings>` 和那个服务本身。Go 这边换成这个两方法的接缝，
// 由装配方接到 [github.com/snight1983/ds-harness-go/settings] 上去。理由有两条：一是本包对设置只有
// 「读当下的默认」和「把我刚删掉的那个默认清掉」这两次触碰，接缝的宽度就该正好是二；
// 二是让名册在测试里不必先立起一台设置 Provider。
//
// 留 nil 表示这套部署没有用户设置这一层，[Roster.DefaultID] 直接读 [Config.Default]。
type DefaultSource interface {
	// Default 读用户这一层当下的默认预设；空串表示这一层没给。
	//
	// **每次调用都现读**、不缓存：设置文档是热重载的，所以改默认在下一个建出来的
	// 会话上生效，而每一个正在跑的会话留在它当初组装出来的那份预设上。
	Default() string
	// ClearDefault 把用户这一层的默认清掉，露出底下部署自己的那个默认。
	ClearDefault(ctx context.Context) error
}

// standingMount 是一份预设那一代常驻装载。
//
// 源: packages/preset/agent-presets/src/index.ts:562-570
type standingMount struct {
	// key 是 agent 认作父的那把键，也是这份组合的登记作用域。
	key *scope.Key
	// scope 是释放边界；留着是给整棵树拆解用的，绝不按会话释放。
	scope *scope.Scope
	// dispose 是这份组合的摘除函数。
	dispose func(context.Context) error
	// stamp 是这一代所依据的那份组合文件的戳。
	stamp compositionStamp
}

// Roster 是这套部署那些 agent 预设的名册。
//
// 源: packages/preset/agent-presets/src/index.ts:93-796（AgentPresets）
//
// 发现是**不记忆的**：[Roster.List] 和 [Roster.Resolve] 每次都重扫根，于是进程跑着
// 的时候创作出来的预设当场可见，而一份在选择器底下被删掉的预设在下一次读时消失。
//
// 新增: DSH 那边它是一个 cordis Service（`ctx.agentPresets`）。Go 没有服务容器，
// 所以它就是一个普通的值，由装配方持有并传下去。DSH 那三处模块级可变状态
// （mounts 这个 Set、两个 WeakMap）在这里全是这个值的字段——一台进程里立两个名册
// 不该互相看得见对方装了什么。
type Roster struct {
	config Config
	// roots 是发现和创作真正会扫的那些根，构造时算一次。
	//
	// 源: packages/preset/agent-presets/src/index.ts:99-104
	//
	// 只算一次，是因为一组根如果在 List() 和照着它答案走的 Copy() 之间变了，创作
	// 就会写进一个调用方从没见过的目录。
	roots []Root
	// defaults 是盖在 config.Default 上的那一层；nil 表示没有这一层。
	defaults DefaultSource
	// standing 是常驻作用域，每一份预设的装载都挂在它下面。
	//
	// 新增: DSH 那边是 selfCtx——服务自己那个**没被追踪**的上下文，因为经可追踪
	// 代理调进来的方法会看到 this.ctx 被换成调用方的上下文。Go 没有那层代理，
	// 所以这里就是名册自己建的一个根作用域，整棵树拆解时释放。
	standingRoot *scope.Scope

	mutex sync.Mutex
	// mounts 是按预设 id 排的那些常驻装载。
	//
	// 源: packages/preset/agent-presets/src/index.ts:252
	//
	// 单飞：两个 agent 抢同一份预设的第一次使用时共享同一份组合。装失败的那一代
	// **不留**，于是文件被修好之后下一个会话会重试；装成功的那一代一直服务到组合
	// **文件**明显变了为止——每一代记着自己那份文件戳，戳过期就为**之后**建出来的
	// 会话开下一代。已经认进去的会话留在它跑着的那一代上。
	mounts map[string]*standingMount
	// loading 是每一份预设正在进行的那次装载，让单飞不必占着 mutex 去读文件。
	loading map[string]*loadingMount
	// bindings 是这个名册组装过的那些 agent 的父绑定，按 agent 的作用域键排。
	//
	// 源: packages/preset/agent-presets/src/index.ts:260
	//
	// 绑定句柄是 [github.com/snight1983/ds-harness-go/core/scope] 里**唯一**的改链权力，攥在这里就让这个
	// 名册成为唯一能把一个 agent 从一份组合挪到另一份的东西。
	bindings map[*scope.Key]*scope.ParentBinding
}

// loadingMount 是一次正在进行的常驻装载，供后来者等。
type loadingMount struct {
	done   chan struct{}
	mount  *standingMount
	err    error
	failed bool
}

// ErrInvalidConfig 表示配置本身不成立，构造被拒。
var ErrInvalidConfig = errors.New("agentpresets: 配置不成立")

// New 立起一份名册。
//
// 源: packages/preset/agent-presets/src/index.ts:130-182
//
// defaults 可以是 nil，表示这套部署没有用户设置这一层。
func New(config Config, defaults DefaultSource) (*Roster, error) {
	if config.Default == "" {
		return nil, fmt.Errorf("%w: 需要一个默认预设 id", ErrInvalidConfig)
	}
	for index, root := range config.Roots {
		if root.Path == "" {
			return nil, fmt.Errorf("%w: 第 %d 个根没给路径", ErrInvalidConfig, index+1)
		}
		if root.Trust != TrustSystem && root.Trust != TrustUser {
			return nil, fmt.Errorf("%w: 第 %d 个根的信任 %q 不认识", ErrInvalidConfig, index+1, root.Trust)
		}
	}
	return &Roster{
		config:       config,
		roots:        config.resolvedRoots(),
		defaults:     defaults,
		standingRoot: scope.NewRoot(),
		mounts:       make(map[string]*standingMount),
		loading:      make(map[string]*loadingMount),
		bindings:     make(map[*scope.Key]*scope.ParentBinding),
	}, nil
}

// DefaultID 是调用方没点名字时装的那份预设 id。
//
// 源: packages/preset/agent-presets/src/index.ts:191-193
func (r *Roster) DefaultID() string {
	if r.defaults != nil {
		if chosen := r.defaults.Default(); chosen != "" {
			return chosen
		}
	}
	return r.config.Default
}

// Roots 是这份名册扫的那些根——**不是** config.Roots：是配置里的每一个按顺序，
// 然后是用户根。
//
// 源: packages/preset/agent-presets/src/index.ts:346-348
//
// 要回答「这套部署到底组不组装名册」，读这个、不要读配置字段，好让这件事只由
// 一处推导决定。
func (r *Roster) Roots() []Root {
	out := make([]Root, len(r.roots))
	copy(out, r.roots)
	return out
}

// Authorable 说这套部署有没有一个本地创作出来的预设去的根。
//
// 源: packages/preset/agent-presets/src/index.ts:351-353
func (r *Roster) Authorable() bool {
	for _, root := range r.roots {
		if root.Trust == TrustUser {
			return true
		}
	}
	return false
}

// List 是配置的那些根此刻供得出的每一份预设，同 id 靠前的根赢。
//
// 源: packages/preset/agent-presets/src/index.ts:199-201
func (r *Roster) List() ([]Preset, error) {
	return DiscoverPresets(r.roots)
}

// Resolve 按 id 解算一份预设；id 传空串表示用 [Roster.DefaultID]。
//
// 源: packages/preset/agent-presets/src/index.ts:213-221
//
// 一份**坏掉的**预设照样解算得出来——删它、读它、把它报出来，三件事都要这一行——
// 装载那几条路在解算**之后**才经 resolveMountable 拒了它。
func (r *Roster) Resolve(id string) (Preset, error) {
	wanted := id
	if wanted == "" {
		wanted = r.DefaultID()
	}
	presets, err := r.List()
	if err != nil {
		return Preset{}, err
	}
	for _, preset := range presets {
		if preset.ID == wanted {
			return preset, nil
		}
	}
	available := make([]string, 0, len(presets))
	for _, preset := range presets {
		available = append(available, preset.ID)
	}
	return Preset{}, &UnknownPresetError{PresetID: wanted, Available: available}
}

// resolveMountable 解算一份**马上要用来组装 agent** 的预设，坏掉的就拿发现给出的
// 那句理由拒了。
//
// 源: packages/preset/agent-presets/src/index.ts:233-239
//
// 在这里失败而不是掉进装载器里失败，让每一种装不了的形状得到同一个答案——幽灵目录、
// 解不动的 YAML、一行都没有的清单——而且不为一份发现早就读成不可用的组合花掉一次
// 装载尝试。
func (r *Roster) resolveMountable(id string) (Preset, error) {
	preset, err := r.Resolve(id)
	if err != nil {
		return Preset{}, err
	}
	if preset.Broken != "" {
		return Preset{}, &PresetMountError{PresetID: preset.ID, Reason: preset.Broken}
	}
	return preset, nil
}

// Mount 拿一份预设组装一个 agent：先保证那份预设的常驻装载在，再把这个 agent 的
// 作用域键认到它下面，于是那份装载的登记和监听器覆盖到这个 agent。
//
// 源: packages/preset/agent-presets/src/index.ts:275-288
//
// 在 agent 工厂建出这个 agent、而它**还没发布**的时候调；那一刻失败会把整次 agent
// 创建回滚掉，于是一份坏掉的预设绝不会留下一个组装了一半的会话。
//
// 新增: DSH 收一个 agentCtx 再从里面 scopeOf 出键，因此有一条「拒绝组装一个没有
// 作用域的上下文」的检查。Go 这边直接收键，那种情形写不出来，那条检查只剩 nil。
func (r *Roster) Mount(ctx context.Context, agentKey *scope.Key, id string) (Preset, error) {
	if agentKey == nil {
		return Preset{}, errors.New("agent-presets: 组装 agent 需要一把作用域键，那正是它认进预设的凭据")
	}
	preset, err := r.resolveMountable(id)
	if err != nil {
		return Preset{}, err
	}
	standing, err := r.ensureStanding(ctx, preset)
	if err != nil {
		return Preset{}, err
	}
	binding, err := scope.BindParent(agentKey, standing.key)
	if err != nil {
		return Preset{}, fmt.Errorf("agent-presets: 把 agent 认进预设 %s 失败：%w", preset.ID, err)
	}
	r.mutex.Lock()
	r.bindings[agentKey] = binding
	r.mutex.Unlock()
	return preset, nil
}

// ComposeFrom 把一个 agent 认到另一个 agent **已经在跑的那一份**常驻组合上。
//
// 源: packages/preset/agent-presets/src/index.ts:316-325
//
// 子 agent 就是这样继承父的能力的。它是一次**认亲**、不是一次装载：父那一代早就
// 组装好了，孩子拿到的正是那一个实例——同一批插件对象、同一批工具登记、同一批
// 提示词段落。按 id 把父那份预设重新解算一遍则不然：那会重读名册，而一份自父启动
// 之后被编辑过的组合文件会递给孩子**另一代**（父那段历史并不是在它之下产生的），
// 一份自那之后被删掉的预设更会让孩子直接失败、而父还好端端跑着。
//
// 同步，而且自己没有任何组装失败的可能——它不读名册、不装任何东西、不碰文件——
// 这正是它能用在建孩子那扇窗里的原因。
//
// 父要是一份预设都没认（一套不组装名册的部署），既不认亲也不报错：那里模型可见的
// 那些行本来就在宿主组合里，孩子透过全局层已经看得见它们。
func (r *Roster) ComposeFrom(agentKey, parentKey *scope.Key) (string, error) {
	if agentKey == nil {
		return "", errors.New("agent-presets: 组装 agent 需要一把作用域键，那正是它认进预设的凭据")
	}
	standing, joined := r.standingMountFor(parentKey)
	if !joined {
		return "", nil
	}
	binding, err := scope.BindParent(agentKey, standing.key)
	if err != nil {
		return "", fmt.Errorf("agent-presets: 把 agent 认进预设 %s 失败：%w", standing.presetID, err)
	}
	r.mutex.Lock()
	r.bindings[agentKey] = binding
	r.mutex.Unlock()
	return standing.presetID, nil
}

// ComposedPreset 是一个**活着的** agent 跑在哪一份预设上；空串表示它一份都没认。
//
// 源: packages/preset/agent-presets/src/index.ts:336-338
//
// 从活的作用域链上读，而不是从会话上读，于是一个**会话还没记下预设**的 agent 也
// 答得出来——比如一个持久化头正从父的组合里建出来的子 agent。
func (r *Roster) ComposedPreset(agentKey *scope.Key) string {
	mount, joined := r.standingMountFor(agentKey)
	if !joined {
		return ""
	}
	return mount.presetID
}

// joinedMount 是从一个已经认进去的 agent 那里找到的一份常驻装载。
type joinedMount struct {
	presetID string
	key      *scope.Key
}

// standingMountFor 找一个 agent 认进的那份常驻组合。
//
// 源: packages/preset/agent-presets/src/mount.ts:222-230
//
// agent 自己那把键的**父**就是它那份预设的常驻键，所以是拿那个父去比对，而不是从
// agent 往上走——那份装载并不在这个 agent 底下。一个一份预设都没认的 agent——一套
// 不组装名册的部署，或者一个还没认亲的子 agent——没有父链，答案是「没认」。
func (r *Roster) standingMountFor(agentKey *scope.Key) (joinedMount, bool) {
	standingKey := scope.ParentOf(agentKey)
	if standingKey == nil {
		return joinedMount{}, false
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	for id, mount := range r.mounts {
		if mount.key == standingKey {
			return joinedMount{presetID: id, key: standingKey}, true
		}
	}
	return joinedMount{}, false
}

// LiveMounts 是此刻还装着的那些预设组合，按预设 id。
//
// 源: packages/preset/agent-presets/src/mount.ts:162-177（livePresetMounts）
//
// 新增: DSH 那边它还顺手把已经拆掉的记录剪掉（pruneDisposedMounts），因为那些记录
// 是靠观察 fiber.uid 是不是 null 来判死的。这里一份装载只在 [Roster] 主动换代或者
// 整棵树拆解时消失，两条路都当场把它从表里删掉，所以没有需要剪的死记录。
func (r *Roster) LiveMounts() []Mount {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	live := make([]Mount, 0, len(r.mounts))
	for id, mount := range r.mounts {
		live = append(live, Mount{PresetID: id, Key: mount.key})
	}
	return live
}

// StandingKeyFor 是一份预设的常驻作用域键，给一个**手上没有 agent** 的宿主读者。
//
// 源: packages/preset/agent-presets/src/index.ts:485-488
//
// 一次冷的转写稿读取要拿会话记下的那份组合去解算工具的呈现器，而常驻装载让这件事
// 不必恢复任何东西就做得到：保证那份装载在，只是组装了插件，没起 agent、没起会话、
// 也没起任何一轮。
func (r *Roster) StandingKeyFor(ctx context.Context, id string) (*scope.Key, error) {
	preset, err := r.resolveMountable(id)
	if err != nil {
		return nil, err
	}
	standing, err := r.ensureStanding(ctx, preset)
	if err != nil {
		return nil, err
	}
	return standing.key, nil
}

// Recompose 把一个 agent 改认到另一份预设的常驻组合上。
//
// 源: packages/preset/agent-presets/src/index.ts:458-472
//
// 只在这个 agent **什么都还没产出**时合法：对话中途换掉工具，会留下一批新组合根本
// 做不出来的、已经记进日志的工具调用。那道检查归**调用方**——这个方法不读会话历史。
//
// 这次调换是一次父改链、不是一次卸载：常驻装载是共享且长驻的，所以旧那份留给它别的
// agent，而新那份在链**挪动之前**就已经保证在了。于是一份不认识或者用不了的预设报
// 错时这个 agent 原封不动——没有任何被拆掉一半、需要还原的状态。一个从没组装过的
// agent 没有什么可改链的：那次调换就是它的第一次认亲，也就正好是一次 Mount。
func (r *Roster) Recompose(ctx context.Context, agentKey *scope.Key, id string) (Preset, error) {
	if agentKey == nil {
		return Preset{}, errors.New("agent-presets: 改组装需要一把作用域键")
	}
	preset, err := r.resolveMountable(id)
	if err != nil {
		return Preset{}, err
	}
	standing, err := r.ensureStanding(ctx, preset)
	if err != nil {
		return Preset{}, err
	}
	r.mutex.Lock()
	binding, bound := r.bindings[agentKey]
	r.mutex.Unlock()
	if !bound {
		fresh, err := scope.BindParent(agentKey, standing.key)
		if err != nil {
			return Preset{}, fmt.Errorf("agent-presets: 把 agent 认进预设 %s 失败：%w", preset.ID, err)
		}
		r.mutex.Lock()
		r.bindings[agentKey] = fresh
		r.mutex.Unlock()
		return preset, nil
	}
	if err := binding.Rebind(standing.key); err != nil {
		return Preset{}, fmt.Errorf("agent-presets: 把 agent 改认到预设 %s 失败：%w", preset.ID, err)
	}
	return preset, nil
}

// Read 读一份预设的组合文本，原样交出。
//
// 源: packages/preset/agent-presets/src/index.ts:361-363
func (r *Roster) Read(id string) (string, error) {
	preset, err := r.Resolve(id)
	if err != nil {
		return "", err
	}
	return ReadComposition(preset)
}

// Copy 靠整份复制一份已有的预设，建出一份本地创作的。
//
// 源: packages/preset/agent-presets/src/index.ts:380-393
//
// 复制是**唯一**的创作写。组合文本从不越过这道接缝：源是按 id 点的，它那个目录照它
// 现在的样子复制过去，于是副本和它的源一样装得起来，而创作授不出名册本来没有的能力。
// 副本**不**为了验证而装一遍——一个今天装得起来的源产出的副本今天也装得起来。
//
// name 传空串表示不给名字，回落到显示 id。
func (r *Roster) Copy(from, id, name string) error {
	source, err := r.Resolve(from)
	if err != nil {
		return err
	}
	// 名册这道检查拒的是**任何一个根**供得出的 id——包括发出去的那些，因为一个和
	// 发出去的预设同名的用户目录会被它遮蔽。copyComposition 里那道磁盘检查只看得见
	// 可写根。
	presets, err := r.List()
	if err != nil {
		return err
	}
	for _, preset := range presets {
		if preset.ID == id {
			return &PresetExistsError{PresetID: id}
		}
	}
	if _, err := CopyComposition(r.roots, source, id, name); err != nil {
		return err
	}
	// 这个 id 底下要是还坐着一份装好的组合，那它只可能是**过期**的（它那份预设是在
	// Remove 之外被从磁盘上删掉的）；新预设不许继承它。每一个已经认进去的会话不管
	// 怎样都留在它跑着的那一代上。
	r.forgetMount(id)
	return nil
}

// Remove 删掉一份本地创作的预设。
//
// 源: packages/preset/agent-presets/src/index.ts:400-416
func (r *Roster) Remove(ctx context.Context, id string) error {
	preset, err := r.Resolve(id)
	if err != nil {
		return err
	}
	if err := DeleteComposition(r.roots, preset); err != nil {
		return err
	}
	// 跑在这份被删预设上的那些会话留着它们的常驻装载；只有新会话看到的名册里没有它了。
	r.forgetMount(id)
	// 存一个**还不存在**的默认是有意的——名册是一个活的目录，所以一个此刻不在的名字
	// 可能等到某个会话来要它的时候已经在了，那时 Resolve 报得出来。而一个这次调用
	// 刚刚删掉的默认不是那种情况：再没有任何东西会供出它，留着的话每一个没有明确
	// 点名的会话都起不来。把它清掉，露出底下部署自己的那个默认，这才是分层。
	if r.defaults == nil || r.defaults.Default() != id {
		return nil
	}
	return r.defaults.ClearDefault(ctx)
}

// forgetMount 把一个 id 底下那份装好的组合从表里摘掉，但**不释放**它。
//
// 源: packages/preset/agent-presets/src/index.ts:392, 404
//
// 不释放，是因为已经认进去的 agent 还跑在它上面（同 [Roster.ensureStanding] 里那条
// 换代的路）。它由 [Roster.Close] 在整棵树拆解时回收。
func (r *Roster) forgetMount(id string) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	delete(r.mounts, id)
}

// ensureStanding 解算（没有就单飞地建出）一份预设的常驻装载。
//
// 源: packages/preset/agent-presets/src/index.ts:491-534
func (r *Roster) ensureStanding(ctx context.Context, preset Preset) (*standingMount, error) {
	for {
		r.mutex.Lock()
		if mounted, ok := r.mounts[preset.ID]; ok {
			r.mutex.Unlock()
			// 文件是组合唯一的编辑者（创作只有复制和删除），所以戳就是那个发现编辑
			// 的东西：文件变了就在这里为这个和之后的会话开下一代。戳读不出来时**继续
			// 服务当前这一代**——一份装载必须熬得过它那个文件消失，为一次 stat 让会话
			// 失败是说不过去的。
			current, readable := readCompositionStamp(preset.Path)
			if !readable || sameStamp(mounted.stamp, current) {
				return mounted, nil
			}
			// TODO: 等最后一个认进这一代的 agent 走光之后回收它。那棵子树不是惰性的，
			// 而设置页那条创作流会把「一份组合变了」变成一次一存一次的事件。这需要在
			// standingMount 上记一个已认亲计数，在 Mount/ComposeFrom/Recompose 里加一、
			// 在 agent 的作用域键死掉时减一。
			r.mutex.Lock()
			// 带守卫的删除：一个和这里抢的调用方可能已经开出了下一代，把**那个**指针
			// 丢掉会分出第三代来。
			if r.mounts[preset.ID] == mounted {
				delete(r.mounts, preset.ID)
			}
			r.mutex.Unlock()
			continue
		}
		if pending, ok := r.loading[preset.ID]; ok {
			r.mutex.Unlock()
			<-pending.done
			if pending.failed {
				return nil, pending.err
			}
			// 装好的那一代已经进表了，回到开头顺便验一次戳。
			continue
		}
		pending := &loadingMount{done: make(chan struct{})}
		r.loading[preset.ID] = pending
		r.mutex.Unlock()

		mount, err := r.composeStanding(ctx, preset)

		r.mutex.Lock()
		delete(r.loading, preset.ID)
		if err == nil {
			r.mounts[preset.ID] = mount
		}
		r.mutex.Unlock()

		pending.mount, pending.err, pending.failed = mount, err, err != nil
		close(pending.done)
		if err != nil {
			// 装失败的那一代**不留**，于是一个之后的会话会重试一份文件已经被修好的预设。
			return nil, err
		}
		return mount, nil
	}
}

// composeStanding 真正建出一代常驻装载；失败时它自己收拾干净，什么都不留。
//
// 源: packages/preset/agent-presets/src/index.ts:513-531
func (r *Roster) composeStanding(ctx context.Context, preset Preset) (*standingMount, error) {
	key := scope.NewKey("agent-preset:" + preset.ID)
	standing, err := scope.New(key, scope.Options{Parent: r.standingRoot.Key()})
	if err != nil {
		return nil, fmt.Errorf("agent-presets: 建不出预设 %s 的常驻作用域：%w", preset.ID, err)
	}
	// 在读文件**之前**盖戳：一次和装载抢跑的编辑因此让戳显得过期、而不是悄悄显得
	// 当前，于是下一个会话会去换代，而不是信任一份比它的戳还老的组合。
	stamp, readable := readCompositionStamp(preset.Path)
	if !readable {
		_ = standing.Dispose(ctx)
		return nil, &PresetMountError{
			PresetID: preset.ID,
			Reason:   fmt.Sprintf("composition file is unreadable: %s", preset.Path),
		}
	}
	dispose, err := mountComposition(ctx, standing, preset, r.config.Composers)
	if err != nil {
		_ = standing.Dispose(ctx)
		return nil, err
	}
	return &standingMount{key: key, scope: standing, dispose: dispose, stamp: stamp}, nil
}

// Close 拆掉这份名册装起来的一切。
//
// 新增: DSH 那边这件事由 cordis 的整树卸载做，本包不写。Go 这边常驻作用域是这个值
// 自己建的，所以回收它的入口也得由它自己给——否则一台跑完就退出的进程还好，一台
// 反复立起名册的（测试就是）会漏掉每一代装载登记的效应。
//
// 摘除按**装的反序**跑，和一次失败装载的回滚同序。
func (r *Roster) Close(ctx context.Context) error {
	r.mutex.Lock()
	mounts := make([]*standingMount, 0, len(r.mounts))
	for _, mount := range r.mounts {
		mounts = append(mounts, mount)
	}
	r.mounts = make(map[string]*standingMount)
	r.bindings = make(map[*scope.Key]*scope.ParentBinding)
	r.mutex.Unlock()

	var errs []error
	for _, mount := range mounts {
		if mount.dispose != nil {
			if err := mount.dispose(ctx); err != nil {
				errs = append(errs, err)
			}
		}
		if err := mount.scope.Dispose(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if err := r.standingRoot.Dispose(ctx); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
