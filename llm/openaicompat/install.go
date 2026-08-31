// 本文件的作用：把这个适配器装进一次组装——解算路由、按路由集合登记适配器、
// 公告哪些路由可以靠配置激活、挂上模型问询，并让这几件事跟着用户设置一起变。
//
// 源: packages/llm/llm-pi-ai/src/index.ts

package openaicompat

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sync"

	"ds-harness-go/attachment"
	"ds-harness-go/core/scope"
	"ds-harness-go/credentials"
	"ds-harness-go/llm"
	"ds-harness-go/settings"
)

// SettingsNamespace 是本包那个设置小节的命名空间。
//
// 源: packages/llm/llm-pi-ai/src/index.ts:90
//
// 新增: 名字跟着 [PluginName] 走，不叫 llm-pi-ai——它同时是可配置提供方目录里
// 每一条的设置地址（见 [directoryEntries]），指到一个这份代码里根本不存在的
// 插件名上，配置界面就编不了这些路由了。
var SettingsNamespace = mustNamespace(PluginName)

// mustNamespace 把一个**字面量**命名空间解出来，不合法就 panic。
//
// 新增: DSH 的 settingsNamespace() 在 TS 里就是一次品牌化转换，编译期定死。
// Go 里 [settings.NewNamespace] 会验一遍并返回错误，而这里的入参是一个包级字面量——
// 它不合法说明本包写错了，不是运行期可以恢复的情况。
func mustNamespace(value string) settings.Namespace {
	namespace, err := settings.NewNamespace(value)
	if err != nil {
		panic(fmt.Sprintf("%s: 命名空间字面量不合法：%v", PluginName, err))
	}
	return namespace
}

// Options 是装这个插件要给的东西。
//
// 源: packages/llm/llm-pi-ai/src/index.ts:141（那个 apply(ctx, config)）
//
// 新增: DSH 从 cordis 的万能上下文里按名取服务（ctx.llm、ctx.get('credentials')、
// ctx.get('attachments')、ctx.logger），"挂没挂"是一次运行期查表。Go 里没有那个
// 容器，每一样都由装配方显式交进来，"挂没挂"就是对应字段是不是 nil。
type Options struct {
	// Runtime 是要登记进去的那个 llm 注册表，必填。
	Runtime *llm.Runtime
	// Owner 是这几次登记的所有者作用域，必填。
	//
	// 新增: DSH 那边登记的寿命跟着 cordis 的插件 fiber 走，写插件的人不用交。
	// Go 里没有那个隐式容器，所以由装配方明说这次装配归谁管。
	Owner *scope.Scope

	// Config 是装配层那一份配置：这次部署／这个 profile 写下的路由表。
	//
	// 它作为 [settings.Options.Base] 压在用户段之下，所以用户改过的字段盖住它、
	// 没改过的仍然是它。挂了设置服务时不必在这里写任何东西——一份空配置就是
	// 待命姿态，等设置文档递来路由。
	Config Config

	// Settings 是可选的设置服务。
	//
	// 新增: DSH 那边是 installSettingsSection 里的 ctx.inject(['settings'], ...)
	// ——没挂设置服务时那一整段根本不跑，装配那一份就是最终答案。nil 是同一件事。
	Settings *settings.Provider

	// Credentials 是可选的凭据服务；nil 表示这次部署没有凭据面，
	// 那时进程环境**就是**整个凭据面（见 [installation.resolveAPIKey]）。
	Credentials credentials.Provider

	// Attachments 在请求那一刻解算那个可选的持久附件服务；nil 表示没有，
	// 于是历史里出现任何一张图都会被拒。
	//
	// 是个函数而不是一个值，理由和 DSH 那句 `() => ctx.get('attachments')` 相同：
	// 附件服务可能在这个插件装好之后才挂上去，每次请求重新问一遍才认得出来。
	Attachments func() attachment.Store

	// Identity 是每次请求都要带上的产品身份；零值表示 [llm.DefaultAppIdentity]。
	Identity llm.AppIdentity

	// Logger 是诊断日志；nil 取 [log/slog.Default]。
	Logger *slog.Logger
}

// routeFact 是注册表在登记那一刻捕获的、属于一条路由的那几件事。
//
// 源: packages/llm/llm-pi-ai/src/index.ts:97-108
//
// displayName 一起捎着，是因为注册表通过 providerInfo() 把它交给每一个选择器：
// 一次没有重新登记的改名，会让旧标签一直挂在界面上，直到某件不相干的事碰巧变了。
type routeFact struct {
	provider    string
	displayName string
	retryPolicy llm.ResolvedRetryPolicy
}

// registrationFacts 交出当下这份路由表在注册表眼里的样子，按路由键排着。
//
// 源: packages/llm/llm-pi-ai/src/index.ts:97-108
//
// 排序由 [Profiles.All] 保证（见 [Profiles] 上那条注释）：一份只是把键换了个
// 书写顺序的设置文档，不该被当成一次路由变更。
func registrationFacts(profiles Profiles) []routeFact {
	facts := make([]routeFact, 0, profiles.Len())
	profiles.All(func(provider string, profile ResolvedProviderProfile) bool {
		facts = append(facts, routeFact{
			provider:    provider,
			displayName: profile.DisplayName,
			retryPolicy: profile.RetryPolicy,
		})
		return true
	})
	return facts
}

// sameRouteFacts 判两份登记事实一不一样。
//
// 新增: DSH 用 deepEqualJson 比两个匿名对象。Go 这边 [routeFact] 里唯一不可比较的
// 是那份策略的 RetryableCodes 切片，所以手写一个比较而不是拖一次反射进来。
func sameRouteFacts(left, right []routeFact) bool {
	return slices.EqualFunc(left, right, func(a, b routeFact) bool {
		return a.provider == b.provider && a.displayName == b.displayName &&
			sameRetryPolicy(a.retryPolicy, b.retryPolicy)
	})
}

// sameRetryPolicy 判两份落定的重试策略一不一样。
func sameRetryPolicy(left, right llm.ResolvedRetryPolicy) bool {
	return left.Mode == right.Mode && left.MaxRetries == right.MaxRetries &&
		left.ResolvedRetryBackoff == right.ResolvedRetryBackoff &&
		slices.Equal(left.RetryableCodes, right.RetryableCodes)
}

// declared 是给 [llm.ConfigurableProvider].Declared 用的那个常真值的地址。
//
// 新增: 这个包**每一条**路由都是声明来的，所以这里只需要一个地址，
// 不必每条目录条目现分配一个 bool。理由见 [directoryEntries]。
var declared = true

// directoryEntries 交出可配置提供方目录：当下这份配置声明的每一条路由。
//
// 源: packages/llm/llm-pi-ai/src/index.ts:118-138
//
// 新增: DSH 那份是「已装上的内置目录 ∪ 当下的路由」的并集，declared 那一位据此
// 分辨一条路由是用户新加的、还是一条被用户改正过的自带路由。这个重写不带内置
// 目录（见包注释），并集塌成只剩后一半，而 declared 恒为真——这里没有任何一条
// 路由是适配器自带的。
//
// 新增: 也因此这份目录会是**空的**（一次待命的裸装配就没有任何路由）。DSH 走不到
// 那一步：它的内置目录从插件挂上的那一刻起就非空。空清单的处理见 [installation.ensureDirectory]。
func directoryEntries(profiles Profiles) []llm.ConfigurableProvider {
	entries := make([]llm.ConfigurableProvider, 0, profiles.Len())
	profiles.All(func(provider string, profile ResolvedProviderProfile) bool {
		entries = append(entries, llm.ConfigurableProvider{
			Provider:     provider,
			DisplayName:  profile.DisplayName,
			SettingsNs:   string(SettingsNamespace),
			SettingsPath: []string{"providers", provider},
			Declared:     &declared,
		})
		return true
	})
	return entries
}

// sameDirectoryEntries 判两份目录一不一样。
//
// 新增: 同 [sameRouteFacts]——[llm.ConfigurableProvider] 里有一个切片和一个指针，
// 用 == 比不了，而指针那一位要比的是**指向的值**（两次算出来的目录各自指着
// 同一个包级变量，但那是这个实现的巧合，不该被比较依赖）。
func sameDirectoryEntries(left, right []llm.ConfigurableProvider) bool {
	return slices.EqualFunc(left, right, func(a, b llm.ConfigurableProvider) bool {
		if a.Provider != b.Provider || a.DisplayName != b.DisplayName || a.SettingsNs != b.SettingsNs {
			return false
		}
		if !slices.Equal(a.SettingsPath, b.SettingsPath) {
			return false
		}
		switch {
		case a.Declared == nil && b.Declared == nil:
			return true
		case a.Declared == nil || b.Declared == nil:
			return false
		default:
			return *a.Declared == *b.Declared
		}
	})
}

// installation 是一次装配活着的那部分。
//
// 源: packages/llm/llm-pi-ai/src/index.ts:141-320（apply 里那一堆闭包变量）
type installation struct {
	runtime     *llm.Runtime
	owner       *scope.Scope
	credentials credentials.Provider
	logger      *slog.Logger

	// profileMutex 只护 profiles 一个字段，而且**绝不**在调用注册表的时候攥着。
	//
	// 新增: DSH 是单线程 JS，那份记忆化的路由表换来换去不需要同步。Go 这边读它的是
	// 每一次请求所在的 goroutine，换它的是设置服务提交那一次，不加锁就是一次真的
	// 数据竞争。分成两把锁而不是一把，是因为 [llm.Runtime.RegisterAdapter] 成功之后
	// 会广播一次「适配器变了」，而听众顺手问一句 [Adapter.ProviderInfo] 就会绕回来
	// 读这张表——同一把锁会当场死锁。
	profileMutex sync.RWMutex
	profiles     Profiles

	// swapMutex 把「重算路由表、对齐两处登记」这一整段串起来，好让两次并发的
	// 设置提交不会把 registeredFacts 和注册表真正攥着的那一份对不上。
	swapMutex       sync.Mutex
	instance        *Adapter
	registration    *llm.AdapterRegistration
	registeredFacts []routeFact
	directory       *llm.DirectoryRegistration
	directoryFacts  []llm.ConfigurableProvider
}

// currentProfiles 交出当下这份路由表，就是 [AdapterOptions].Profiles。
//
// 源: packages/llm/llm-pi-ai/src/index.ts:156-163
//
// 新增: DSH 在这里按原始快照的身份做惰性记忆化——它那个 current() 每次调用都会
// 交出一个新读到的对象，所以必须在这里认出「其实没变」。Go 这边 [settings.Scope.Get]
// 交出的是提交那一刻存下的那个值，两次提交之间**就是同一份**，而唯一能改它的
// 那次提交会同步叫醒观察者。所以解算挪到观察者里做一次，这里只是把结果读出来
// ——[Profiles] 用 == 比身份，这正是 [Adapter.current] 那份快照要的稳定性。
func (i *installation) currentProfiles() Profiles {
	i.profileMutex.RLock()
	defer i.profileMutex.RUnlock()
	return i.profiles
}

// setProfiles 换上一份新的路由表。
func (i *installation) setProfiles(profiles Profiles) {
	i.profileMutex.Lock()
	i.profiles = profiles
	i.profileMutex.Unlock()
}

// resolveAPIKey 解算一条已经落定的路由的凭据，就是 [AdapterOptions].ResolveAPIKey。
//
// 源: packages/llm/llm-pi-ai/src/index.ts:166-189
//
// 只有一条**根本没点名凭据**的路由才不认证（本地推理服务器正是这么跑的）。
// 一旦点了名，解不出来就必须响亮地失败：悄悄退回成不认证的话，请求会带着
// 这次部署根本没打算用的身份发出去——或者干脆不带身份，而两者都不是配置说的那件事。
//
// 新增: DSH 没挂凭据面时读的是 launchEnvironmentOf(ctx)（那个包记着进程**启动时**
// 那一份环境）。util/launch-environment 不在这次移植的范围里，所以这边读的是
// [os.Getenv]，也就是**当下**这一份。差别只在「进程跑起来之后有人改了自己的环境变量」
// 这一种情形，而 Go 里没有任何东西会在运行期改它。
func (i *installation) resolveAPIKey(
	ctx context.Context,
	provider string,
	profile ResolvedProviderProfile,
) (string, error) {
	ref := profile.APIKeyRef
	if ref == "" {
		return "", nil
	}
	var hit string
	if i.credentials != nil {
		resolved, configured, err := i.credentials.Resolve(ctx, ref)
		if err != nil {
			return "", err
		}
		if configured {
			hit = resolved.Value
		}
	} else {
		// 没有凭据面时，进程环境就是整个凭据面。
		hit = os.Getenv(string(ref))
	}
	if hit != "" {
		return llm.AssertUsableAPIKey(hit, PluginName, string(ref))
	}
	// 新增: DSH 那句末尾劝人「只有当这个提供方该走 pi-ai 自己的环境发现时才删掉
	// apiKeyEnv」。这边没有那条发现路径——删掉 apiKeyEnv 的意思是这条路由**不认证**
	// ——所以那半句换成这一句。
	return "", llm.NewError(fmt.Sprintf(
		"%s: no credential for provider route %q; its profile resolves %s, which is not set"+
			" — store %s through the credentials service (the web Models page writes it) or export it,"+
			" and remove apiKeyEnv only if this route should send no Authorization header at all",
		PluginName, provider, ref, ref), "MISSING_CREDENTIAL", nil)
}

// ensureRegistration 把适配器登记的那个路由集合对齐到 profiles。
//
// 源: packages/llm/llm-pi-ai/src/index.ts:261-283
//
// 注册表在登记那一刻捕获路由集合和每条路由的重试策略，所以这两样里任何一样变了
// 都得重新登记一次。换的那一下是原子的（同一个适配器实例，整份先验过）：一条和
// 别人撞了的路由只会让这次更新被拒，先前那些路由继续服务请求；而 registeredFacts
// 只在注册表**真的**收下新集合之后才往前走——于是改回一份能用的配置总能重新生效。
func (i *installation) ensureRegistration(ctx context.Context, profiles Profiles) error {
	facts := registrationFacts(profiles)
	if i.registeredFacts != nil && sameRouteFacts(facts, i.registeredFacts) {
		return nil
	}
	routes := profiles.Routes()
	if i.registration == nil {
		// 待命的裸装配：设置小节递来路由之前什么都不登记，一份空的设置小节
		// 也让它继续待命。
		//
		// 源: packages/llm/llm-pi-ai/src/index.ts:274-277
		if len(routes) == 0 {
			i.registeredFacts = facts
			return nil
		}
		registration, err := i.runtime.RegisterAdapter(ctx, i.owner, routes, i.adapter())
		if err != nil {
			return err
		}
		i.registration = registration
	} else if err := i.registration.Replace(routes); err != nil {
		return err
	}
	i.registeredFacts = facts
	return nil
}

// ensureDirectory 把可配置提供方目录对齐到 profiles。
//
// 源: packages/llm/llm-pi-ai/src/index.ts:219-233
//
// 原子替换，绝不「先撤再登」：一条别的适配器家族已经声明过的路由，否则会把这个
// 插件的整份目录晾在撤走状态、Models 页面空着。候选整份先验过，所以一次撞车只是
// 让先前那些条目继续服务，代价仅仅是一条诊断。
//
// 新增: 空清单要特殊对待。DSH 的目录从插件挂上那一刻起就非空（内置目录在），
// 而这边一次待命的裸装配一条路由都没有，[llm.Runtime.RegisterConfigurableProviders]
// 会拒掉空清单。所以「还没登记过 + 空清单」和适配器那一侧同样是待命：什么都不登。
// 已经登记过之后清单变空是另一回事——那时 Replace(nil) 合法，留下一次「活着但一条
// 都没有」的声明，而这正是「路由跟着 profiles 一起走」要的语义。
func (i *installation) ensureDirectory(ctx context.Context, profiles Profiles) error {
	entries := directoryEntries(profiles)
	if i.directoryFacts != nil && sameDirectoryEntries(entries, i.directoryFacts) {
		return nil
	}
	if i.directory == nil {
		if len(entries) == 0 {
			i.directoryFacts = entries
			return nil
		}
		directory, err := i.runtime.RegisterConfigurableProviders(ctx, i.owner, entries)
		if err != nil {
			return err
		}
		i.directory = directory
	} else if err := i.directory.Replace(entries); err != nil {
		return err
	}
	i.directoryFacts = entries
	return nil
}

// onChange 按一份新配置重算路由表，然后把两处登记对齐。
//
// 源: packages/llm/llm-pi-ai/src/index.ts:294-318
//
// 两处各自兜住自己的失败并留下诊断，理由 DSH 写得很清楚：assertServiceable 看不见
// llm 注册表，所以一份声称拥有别人路由的配置会被**成功存下**、只在这次换手时才失败；
// 没有自己的诊断的话，那次拒绝到运维手上是一句笼统的「设置：观察者失败了」，
// 既不点名是哪条路由，也不说为什么它没在服务。两种情况下先前那些路由都继续服务。
func (i *installation) onChange(ctx context.Context, config Config) {
	i.swapMutex.Lock()
	defer i.swapMutex.Unlock()

	profiles, err := ResolveProfiles(config.Providers)
	if err != nil {
		// 走不到这里：[AssertServiceable] 是这个命名空间的校验器，一份解不出来的
		// 配置在**被写下的地方**就被拒了，而设置层会为一个存下来却失败的段保留
		// 上一个好值。Go 这边解算会返回 error，所以这条分支必须有个名字——它只
		// 记一条诊断，然后什么都不动。
		i.logger.Error(PluginName+": 一份解不出来的配置到了观察者这里，保持上一份路由表不动",
			"err", err)
		return
	}
	i.setProfiles(profiles)
	if err := i.ensureRegistration(ctx, profiles); err != nil {
		i.logger.Error(PluginName+": 一次更新被拒，继续用上一次登记的那些路由", "err", err)
	}
	// 目录跟着注册表收下的那份 profiles 走，所以一条没能登记成功的路由不会被
	// 公告成可配置的。
	if err := i.ensureDirectory(ctx, profiles); err != nil {
		i.logger.Error(PluginName+": 一次更新被拒，继续用上一份可配置提供方目录", "err", err)
	}
}

// adapter 交出这次装配那个适配器实例。
//
// 它在 [Install] 里造好之后就不再变：DSH 也是同一个实例一路用到底，
// 配置改动换的是它读到的那张路由表，不是它自己。
func (i *installation) adapter() llm.Adapter { return i.instance }

// Install 把这个适配器装进一次组装，返回拆除函数。
//
// 源: packages/llm/llm-pi-ai/src/index.ts:141-320
//
// 装配那一份配置先解算、先登记（DSH 的 index.ts:164、234、284），然后设置小节才
// 登记上去——那一次登记会把用户段叠上来，所以紧接着要再对齐一次（DSH 的
// installSettingsSection 在 register 之后立刻调 onChange，见 settings/index.ts:887）。
//
// 新增: DSH 那个拆除器还有一支「设置服务撤走了、但这个插件还活着」的路径——它把
// 配置退回装配那一份、再重新判一次派生出来的东西（settings/index.ts:876-886）。
// Go 这边没有那条路径可走：设置登记的注销函数攥在**这个**拆除器手里，它跑起来
// 的时候整次装配都在收摊，那时候再去重新登记一遍路由，动的正是这次拆除要放掉的
// 那些资源——也就是 DSH 用 isUnloading(ctx) 挡掉的那一种。
func Install(ctx context.Context, options Options) (func(context.Context) error, error) {
	if options.Runtime == nil {
		return nil, fmt.Errorf("%w：Options.Runtime 不能是 nil", ErrInvalidConfig)
	}
	// 在这里拒，而不是等作用域那一层报「宿主作用域不能是 nil」：那句话说的是
	// scope 包内部的规矩，看到它的人得先弄明白这次登记的宿主是从哪儿来的。
	if options.Owner == nil {
		return nil, fmt.Errorf("%w：Options.Owner 不能是 nil", ErrInvalidConfig)
	}

	install := &installation{
		runtime:     options.Runtime,
		owner:       options.Owner,
		credentials: options.Credentials,
		logger:      options.Logger,
	}
	if install.logger == nil {
		install.logger = slog.Default()
	}

	// 装配那一份先解算一次。它同时是设置小节的组装层，所以一份这里就服务不了的
	// 配置压根不该让这次装配开始——留到设置登记那一步才报，诊断会指向「存下来的
	// 段解不开」，而毛病其实在装配参数里。
	profiles, err := ResolveProfiles(options.Config.Providers)
	if err != nil {
		return nil, err
	}
	install.setProfiles(profiles)

	adapter, err := NewAdapter(AdapterOptions{
		Profiles:           install.currentProfiles,
		ResolveAPIKey:      install.resolveAPIKey,
		ResolveAttachments: options.Attachments,
		OnReplayDegrade: func(provider, model, reason string) {
			// 源: packages/llm/llm-pi-ai/src/index.ts:200-205
			install.logger.Warn(fmt.Sprintf(
				"%s: unusable replay state on assistant history for route %q;"+
					" sending that message as provider-neutral content (%s)",
				PluginName, provider+"/"+model, reason))
		},
		Identity: options.Identity,
	})
	if err != nil {
		return nil, err
	}
	install.instance = adapter

	// 一次拆除要放掉的东西按登记的反序收，用一个栈记着；中途失败时也用它把已经
	// 登记上的那几样收干净，不然一次失败的 Install 会留下半套还在服务的路由。
	var undo []func(context.Context) error
	unwind := func(ctx context.Context) error {
		var first error
		for index := len(undo) - 1; index >= 0; index-- {
			if err := undo[index](ctx); err != nil && first == nil {
				first = err
			}
		}
		undo = nil
		return first
	}
	fail := func(err error) (func(context.Context) error, error) {
		_ = unwind(ctx)
		return nil, err
	}

	// 源: packages/llm/llm-pi-ai/src/index.ts:234、284
	if err := install.ensureDirectory(ctx, profiles); err != nil {
		return fail(err)
	}
	if install.directory != nil {
		undo = append(undo, install.directory.Release)
	}
	if err := install.ensureRegistration(ctx, profiles); err != nil {
		return fail(err)
	}
	if install.registration != nil {
		undo = append(undo, install.registration.Release)
	}

	// 问询端点是配置时对着一份草稿做的动作，所以它按整个命名空间登记、而不是按路由
	// ——界面正在添加的那个提供方还没有名字可点。它和路由集合无关：一次待命的裸装配
	// 同样提供这个动作，那正是「先问出有哪些模型、再据此写下第一条路由」的场景。
	//
	// 源: packages/llm/llm-pi-ai/src/index.ts:254
	//
	// 新增: DSH 还要往里灌一个 storedApiKey 回调。这边取存量密钥要的两样东西
	// （那条路由的配置、[AdapterOptions.ResolveAPIKey]）本来就都在适配器身上，
	// 所以直接把它那个方法登记上去，见 [Adapter.DiscoverModels]。
	releaseDiscovery, err := options.Runtime.RegisterModelDiscovery(
		ctx, options.Owner, string(SettingsNamespace), adapter.DiscoverModels)
	if err != nil {
		return fail(err)
	}
	undo = append(undo, releaseDiscovery)

	if options.Settings != nil {
		// 源: packages/llm/llm-pi-ai/src/index.ts:286-319
		//
		// 组装层交的是装配那一份配置的原始形状。拒掉一份服务不了的段就在它被写下的
		// 地方（[AssertServiceable]）：没有这一道，一份 schema 上过得去、适配器却
		// 服务不了的配置会被存下来，然后悄悄把这个命名空间里的每条路由都停掉。
		base, err := rawConfig(options.Config)
		if err != nil {
			return fail(err)
		}
		section, undoSection, err := settings.Register(options.Settings, SettingsNamespace, Config{},
			&settings.Options[Config]{
				Base:     base,
				Applies:  settings.AppliesLive,
				Validate: AssertServiceable,
			})
		if err != nil {
			return fail(fmt.Errorf("%s: 登记设置小节失败：%w", PluginName, err))
		}
		undo = append(undo, func(context.Context) error {
			undoSection()
			return nil
		})
		// 观察者先挂上、再对齐一次，不能反过来：中间那一小段里落地的一次提交，
		// 反过来做就没有人看见了。
		//
		// ctx 是装配那一刻的上下文。转交给它的两处登记只把它交给
		// [scope.Scope.Defer]，那边不读它——所以一次晚到的提交在这里用它是安全的，
		// 而它同时说清了这些登记归哪一次装配所有。
		unwatch := section.Watch(func(next, _ Config) { install.onChange(ctx, next) })
		undo = append(undo, func(context.Context) error {
			unwatch()
			return nil
		})
		// 登记那一刻用户段就已经叠上来了，所以立刻按解析值再对齐一次
		// （DSH 的 installSettingsSection 在 register 之后那句 onChange）。
		install.onChange(ctx, section.Get())
	}

	var once sync.Once
	return func(ctx context.Context) error {
		var err error
		once.Do(func() { err = unwind(ctx) })
		return err
	}, nil
}

// rawConfig 把装配那一份配置投影成设置层要的原始形状。
//
// 新增: DSH 把 entry 原样交给 base（TS 里它本来就是一个 JSON 对象）。Go 这边
// [settings.Options].Base 是 map[string]any，理由见那个字段——组装层按定义是
// **部分**的，用 T 表达不了「这个字段我没提」和「我把它设成零值」的区别。
// 这里过一遍 [Config] 自己的 json 标签，好让组装层和用户段用的是同一套键名；
// settings 包自己那个同名的投影是不导出的，而这一次转换正是 [Config] 的 json
// 标签存在的理由（见 [ProviderProfile] 上那条）。
func rawConfig(config Config) (map[string]any, error) {
	if len(config.Providers) == 0 {
		// 一份没有路由的装配配置不该在组装层留下一个空的 providers 键：那个键
		// 存在与否是配置界面用来标「这一层提没提过这个字段」的依据。
		return nil, nil
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("%s: 装配配置不是 JSON 形状：%w", PluginName, err)
	}
	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		return nil, fmt.Errorf("%s: 装配配置不是 JSON 形状：%w", PluginName, err)
	}
	return raw, nil
}
