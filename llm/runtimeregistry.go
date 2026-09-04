// 本文件的作用：往运行时里登记东西的那几条路——模型适配器、可配置提供方目录和
// 模型发现，各自的替换与撤销都在这里。
//
// 源: packages/llm/llm/src/index.ts:262-1026

package llm

import (
	"context"
	"fmt"
	"slices"

	"github.com/snight1983/ds-harness-go/scope"
)

// AdapterRegistration 是一次活着的适配器登记：能释放，也能原子地换掉整份路由名单。
//
// 源: packages/llm/llm/src/index.ts:277-299（AdapterRegistrationHandle）
type AdapterRegistration struct {
	runtime *Runtime
	adapter Adapter
	dispose func(context.Context) error

	// owned 是这次登记当下攥着的那些路由，Replace 会重写它。
	// released 说明释放跑过没有——owned 为空说明不了这件事，因为
	// Replace(nil) 合法地留下一次「活着但一条路由都没有」的登记。
	//
	// 这两个字段由 runtime.mutex 护着，和那三张表用同一把锁。
	owned    []string
	released bool
}

// RegisterAdapter 把一个适配器登记到给定的那些提供方路由上。
//
// 源: packages/llm/llm/src/index.ts:356-394
//
// 全有或者全无：任何一条路由已经有适配器了，整次登记以 DUPLICATE_ADAPTER 失败，
// 注册表一个字都不动。owner 释放时这次登记跟着释放。
func (r *Runtime) RegisterAdapter(
	ctx context.Context,
	owner *scope.Scope,
	providers []string,
	adapter Adapter,
) (*AdapterRegistration, error) {
	if owner == nil {
		return nil, fmt.Errorf("%w：RegisterAdapter 需要一个持有它的作用域", ErrInvalidConfig)
	}
	if adapter == nil {
		return nil, NewError("llm: RegisterAdapter 需要一个适配器", InvalidAdapterCode, nil)
	}
	if len(providers) == 0 {
		return nil, NewError("an adapter must register at least one provider", InvalidAdapterCode, nil)
	}
	handle := &AdapterRegistration{runtime: r, adapter: adapter}

	r.mutex.Lock()
	prepared, err := r.prepareRoutes(providers, adapter, nil)
	if err != nil {
		r.mutex.Unlock()
		return nil, err
	}
	r.commitRoutes(handle, prepared)
	r.mutex.Unlock()
	r.emitAdaptersUpdated()

	dispose, err := owner.Defer("llm.RegisterAdapter()", func(context.Context) error {
		handle.release()
		return nil
	})
	if err != nil {
		handle.release()
		return nil, err
	}
	handle.dispose = dispose
	return handle, nil
}

// Release 放掉这次登记当下攥着的每一条路由。重复调用没有额外效果。
//
// 源: packages/llm/llm/src/index.ts:267、375-380
func (h *AdapterRegistration) Release(ctx context.Context) error {
	return h.dispose(ctx)
}

// Replace 把这次登记的路由名单原子地换成 providers，适配器实例不变。
//
// 源: packages/llm/llm/src/index.ts:269-283、385-392
//
// 候选名单先整份验过——和别的适配器撞车、名字是空串、或者路由元数据不合格，
// 都会报错并且**原样留下当下那份名单**；换的那一下是一段临界区，所以没有任何
// 请求看得见中间的空档。这里空名单是合法的（一段被清空的配置持有零条路由、
// 但登记还活着），初次登记不行。
//
// 这次登记已经释放之后再调，报 REGISTRATION_DISPOSED：它的路由已经没了、
// 它的释放也已经跑过，此刻再放进去的东西不会有人负责摘出来。
func (h *AdapterRegistration) Replace(providers []string) error {
	r := h.runtime
	r.mutex.Lock()
	if h.released {
		r.mutex.Unlock()
		return NewError("a disposed adapter registration cannot replace its routes", RegistrationDisposedCode, nil)
	}
	prepared, err := r.prepareRoutes(providers, h.adapter, h.owned)
	if err != nil {
		r.mutex.Unlock()
		return err
	}
	r.commitRoutes(h, prepared)
	r.mutex.Unlock()
	r.emitAdaptersUpdated()
	return nil
}

// release 是释放那一下的本体，[AdapterRegistration.Release] 和 owner 释放共用它。
func (h *AdapterRegistration) release() {
	r := h.runtime
	r.mutex.Lock()
	if h.released {
		// 走不到，理由同 [DirectoryRegistration.withdraw] 里那一句：
		// 进这个函数的两条路都经由 scope 那份只跑一次的撤销。
		r.mutex.Unlock()
		return
	}
	h.released = true
	r.dropRoutes(h.owned)
	h.owned = nil
	r.mutex.Unlock()
	r.emitAdaptersUpdated()
}

// prepareRoutes 验一份候选路由名单，把这次登记已经攥着的那些当成可用。
//
// 源: packages/llm/llm/src/index.ts:396-423
//
// 一个字都不写：候选被拒之后注册表和进来时一模一样。
//
// 调用方必须持有 runtime.mutex。
func (r *Runtime) prepareRoutes(providers []string, adapter Adapter, owned []string) ([]*adapterRegistration, error) {
	unique := map[string]struct{}{}
	registrations := make([]*adapterRegistration, 0, len(providers))
	for _, provider := range providers {
		if provider == "" {
			return nil, NewError("adapter provider names must be non-empty", InvalidAdapterCode, nil)
		}
		_, seen := unique[provider]
		_, taken := r.adapters[provider]
		if seen || (taken && !slices.Contains(owned, provider)) {
			return nil, NewError(
				fmt.Sprintf("an adapter for provider %q is already registered", provider),
				DuplicateAdapterCode, nil)
		}
		info := AdapterProviderInfo(adapter, provider)
		if info.ID != provider || info.Name == "" {
			return nil, NewError(
				fmt.Sprintf("adapter metadata for provider %q must preserve its id and have a non-empty name", provider),
				InvalidAdapterCode, nil)
		}
		unique[provider] = struct{}{}
		retryPolicy, owns := AdapterRetryPolicy(adapter, provider)
		if !owns {
			resolved, err := ResolveRetryPolicy(nil, fmt.Sprintf("llm: provider %q retryPolicy", provider))
			if err != nil {
				// 走不到：nil 配置解算的就是那份普通默认，它不会不合法。
				return nil, err
			}
			retryPolicy = resolved
		}
		registrations = append(registrations, &adapterRegistration{
			adapter:     adapter,
			provider:    ProviderInfo{ID: info.ID, Name: info.Name},
			retryPolicy: retryPolicy,
		})
	}
	return registrations, nil
}

// commitRoutes 在一段临界区里把这次登记的路由换成备好的那些。
//
// 源: packages/llm/llm/src/index.ts:425-440
//
// 摘掉再放回去之间没有任何观察者能插进来看一眼。通知在锁外面发，所以这个函数
// 自己不发——调用方解锁之后调 [Runtime.emitAdaptersUpdated]。
//
// 调用方必须持有 runtime.mutex。
func (r *Runtime) commitRoutes(handle *AdapterRegistration, registrations []*adapterRegistration) {
	r.dropRoutes(handle.owned)
	handle.owned = handle.owned[:0]
	for _, registration := range registrations {
		id := registration.provider.ID
		r.adapters[id] = registration
		r.adapterOrder = append(r.adapterOrder, id)
		handle.owned = append(handle.owned, id)
	}
}

// dropRoutes 把这些路由从表和次序里一起摘掉。
//
// 调用方必须持有 runtime.mutex。
func (r *Runtime) dropRoutes(providers []string) {
	for _, provider := range providers {
		delete(r.adapters, provider)
	}
	r.adapterOrder = slices.DeleteFunc(r.adapterOrder, func(id string) bool {
		return slices.Contains(providers, id)
	})
}

// ListProviders 列出有适配器的那些提供方路由，按登记次序。
//
// 源: packages/llm/llm/src/index.ts:442-448
func (r *Runtime) ListProviders() []ProviderInfo {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	providers := make([]ProviderInfo, 0, len(r.adapterOrder))
	for _, id := range r.adapterOrder {
		providers = append(providers, r.adapters[id].provider)
	}
	return providers
}

// registration 取出一条路由上的活登记。
//
// 源: packages/llm/llm/src/index.ts:871-875
func (r *Runtime) registration(provider string) (*adapterRegistration, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	registration, present := r.adapters[provider]
	if !present {
		return nil, NewError(fmt.Sprintf("no adapter registered for provider %q", provider), NoAdapterCode, nil)
	}
	return registration, nil
}

// ---- 可配置提供方目录 ----

// DirectoryRegistration 是一次活着的可配置提供方声明，是
// [AdapterRegistration] 在目录那一侧的对应物。
//
// 源: packages/llm/llm/src/index.ts:301-320（DirectoryRegistrationHandle）
type DirectoryRegistration struct {
	runtime  *Runtime
	dispose  func(context.Context) error
	held     []ConfigurableProvider
	disposed bool
}

// RegisterConfigurableProviders 声明一批适配器插件可以靠配置激活的提供方路由。
//
// 源: packages/llm/llm/src/index.ts:450-511
//
// 全有或者全无：空清单、不合格的条目、或者任何一条已经被别人声明过的提供方，
// 都会报错，其余的一条也不会被登记。owner 释放时这次声明跟着撤销。
func (r *Runtime) RegisterConfigurableProviders(
	ctx context.Context,
	owner *scope.Scope,
	entries []ConfigurableProvider,
) (*DirectoryRegistration, error) {
	if owner == nil {
		return nil, fmt.Errorf("%w：RegisterConfigurableProviders 需要一个持有它的作用域", ErrInvalidConfig)
	}
	if len(entries) == 0 {
		return nil, NewError(
			"a configurable-provider registration must declare at least one provider",
			InvalidDirectoryCode, nil)
	}
	handle := &DirectoryRegistration{runtime: r}
	if err := handle.commit(entries); err != nil {
		return nil, err
	}
	dispose, err := owner.Defer("llm.RegisterConfigurableProviders()", func(context.Context) error {
		handle.withdraw()
		return nil
	})
	if err != nil {
		handle.withdraw()
		return nil, err
	}
	handle.dispose = dispose
	return handle, nil
}

// Release 撤走这次声明当下攥着的每一条目录条目。重复调用没有额外效果。
//
// 源: packages/llm/llm/src/index.ts:291、495-500
func (h *DirectoryRegistration) Release(ctx context.Context) error {
	return h.dispose(ctx)
}

// Replace 把这次声明的条目原子地换成 entries。
//
// 源: packages/llm/llm/src/index.ts:292-304、504-509
//
// 条款和 [AdapterRegistration.Replace] 完全一样：整份先验过，被拒就原样留着，
// 换的那一下是一段临界区，空清单在这里合法。已经撤销之后再调报
// REGISTRATION_DISPOSED。
func (h *DirectoryRegistration) Replace(entries []ConfigurableProvider) error {
	return h.commit(entries)
}

// commit 验一份候选目录条目，验完整份再公布。
//
// 源: packages/llm/llm/src/index.ts:460-488
//
// 整份没过之前一个字都不写，这正是让 Replace 成为一次**替换**、而不是一次
// 「先删后加、中间可能把目录晾空」的那条性质。
func (h *DirectoryRegistration) commit(candidates []ConfigurableProvider) error {
	r := h.runtime
	r.mutex.Lock()
	if h.disposed {
		r.mutex.Unlock()
		return NewError("this configurable-provider registration was disposed", RegistrationDisposedCode, nil)
	}
	own := map[string]struct{}{}
	for _, entry := range h.held {
		own[entry.Provider] = struct{}{}
	}
	detached := make([]ConfigurableProvider, 0, len(candidates))
	for _, entry := range candidates {
		if entry.Provider == "" || entry.DisplayName == "" || entry.SettingsNs == "" {
			r.mutex.Unlock()
			return NewError(
				"configurable providers need a non-empty provider, displayName, and settingsNs",
				InvalidDirectoryCode, nil)
		}
		if slices.Contains(entry.SettingsPath, "") {
			r.mutex.Unlock()
			return NewError(
				fmt.Sprintf("configurable provider %q has an empty settingsPath segment", entry.Provider),
				InvalidDirectoryCode, nil)
		}
		_, declared := r.directory[entry.Provider]
		_, mine := own[entry.Provider]
		duplicate := slices.ContainsFunc(detached, func(seen ConfigurableProvider) bool {
			return seen.Provider == entry.Provider
		})
		if (declared && !mine) || duplicate {
			r.mutex.Unlock()
			return NewError(
				fmt.Sprintf("configurable provider %q is already declared", entry.Provider),
				DuplicateDirectoryCode, nil)
		}
		detached = append(detached, entry.Clone())
	}
	r.dropDirectory(h.held)
	for _, entry := range detached {
		r.directory[entry.Provider] = entry
		r.directoryOrder = append(r.directoryOrder, entry.Provider)
	}
	h.held = detached
	r.mutex.Unlock()
	r.emitAdaptersUpdated()
	return nil
}

// withdraw 是撤销那一下的本体。
//
// 源: packages/llm/llm/src/index.ts:495-500
func (h *DirectoryRegistration) withdraw() {
	r := h.runtime
	r.mutex.Lock()
	if h.disposed {
		// 走不到：进这个函数的两条路——[DirectoryRegistration.Release] 和 owner
		// 释放——都经由 scope 那份只跑一次的撤销。这一句是第二道闸，
		// 防的是以后有人把它直接接到别处。
		r.mutex.Unlock()
		return
	}
	h.disposed = true
	r.dropDirectory(h.held)
	h.held = nil
	r.mutex.Unlock()
	r.emitAdaptersUpdated()
}

// dropDirectory 把这些条目从目录和它的次序里一起摘掉。
//
// 调用方必须持有 runtime.mutex。
func (r *Runtime) dropDirectory(entries []ConfigurableProvider) {
	if len(entries) == 0 {
		return
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		delete(r.directory, entry.Provider)
		names = append(names, entry.Provider)
	}
	r.directoryOrder = slices.DeleteFunc(r.directoryOrder, func(id string) bool {
		return slices.Contains(names, id)
	})
}

// ListConfigurableProviders 列出每一条被声明过的可配置提供方，按声明次序，
// 不管它当下有没有被登记。
//
// 源: packages/llm/llm/src/index.ts:513-519
func (r *Runtime) ListConfigurableProviders() []ConfigurableProvider {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	entries := make([]ConfigurableProvider, 0, len(r.directoryOrder))
	for _, id := range r.directoryOrder {
		entries = append(entries, r.directory[id].Clone())
	}
	return entries
}

// ---- 模型发现 ----

// RegisterModelDiscovery 表示这个插件愿意代表它拥有的那个设置命名空间去问询
// 提供方端点，返回撤销这次表态的函数。
//
// 源: packages/llm/llm/src/index.ts:521-548
//
// 键是命名空间而不是路由名，因为配置界面手上本来就攥着命名空间（从可配置提供方
// 目录来的），而一条**正在被添加**的提供方还没有名字可点。
func (r *Runtime) RegisterModelDiscovery(
	ctx context.Context,
	owner *scope.Scope,
	settingsNs string,
	discover ModelDiscovery,
) (func(context.Context) error, error) {
	if owner == nil {
		return nil, fmt.Errorf("%w：RegisterModelDiscovery 需要一个持有它的作用域", ErrInvalidConfig)
	}
	if settingsNs == "" {
		return nil, NewError("model discovery needs a non-empty settings namespace", InvalidDiscoveryCode, nil)
	}
	if discover == nil {
		return nil, NewError("model discovery needs a discover function", InvalidDiscoveryCode, nil)
	}
	r.mutex.Lock()
	if _, present := r.discoveries[settingsNs]; present {
		r.mutex.Unlock()
		return nil, NewError(
			fmt.Sprintf("model discovery for %q is already registered", settingsNs),
			DuplicateDiscoveryCode, nil)
	}
	r.discoveries[settingsNs] = discover
	r.mutex.Unlock()

	undo := func() {
		r.mutex.Lock()
		delete(r.discoveries, settingsNs)
		r.mutex.Unlock()
	}
	dispose, err := owner.Defer("llm.RegisterModelDiscovery()", func(context.Context) error {
		undo()
		return nil
	})
	if err != nil {
		undo()
		return nil, err
	}
	return dispose, nil
}

// DiscoverModels 问一个提供方端点它公告了哪些模型。
//
// 源: packages/llm/llm/src/index.ts:550-586
//
// 请求描述的是一份**草稿**、不是一条存下来的路由，所以这里既不读也不写设置和
// 凭据——两样都归调用方，而答复只是一份界面可以拿去让人采纳的候选元数据。
func (r *Runtime) DiscoverModels(
	ctx context.Context,
	settingsNs string,
	request ModelDiscoveryRequest,
) ([]DiscoveredModel, error) {
	r.mutex.Lock()
	discover, present := r.discoveries[settingsNs]
	r.mutex.Unlock()
	if !present {
		return nil, NewError(
			fmt.Sprintf("no model discovery is registered for %q", settingsNs),
			NoDiscoveryCode, nil)
	}
	// 两样里得有一样点明「要描述什么」：一条适配器认得的路由，或者一个可以去问的
	// 端点。两样都没有的话，这次问询没有对象。
	if request.Provider == "" && request.BaseURL == "" {
		return nil, NewError("model discovery needs a provider route or a baseURL", InvalidDiscoveryCode, nil)
	}
	discovered, err := discover(ctx, request)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	models := make([]DiscoveredModel, 0, len(discovered))
	for _, model := range discovered {
		if model.ID == "" {
			continue
		}
		if _, duplicate := seen[model.ID]; duplicate {
			continue
		}
		seen[model.ID] = struct{}{}
		models = append(models, model)
	}
	return models, nil
}
