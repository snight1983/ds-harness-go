// 本文件的作用：把「这个 agent 下一步用哪个模型」这一个可变的选择，同时接到提示词
// 装配和请求路由两个面上，让两边永远说的是同一个模型。
//
// 源: packages/core/agent/src/model-selection.ts:1-75

package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/snight1983/ds-harness-go/harness/systemprompt"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
)

// ModelSelection 是给一个活 agent 选定的那一整份「提供方 + 模型 + 推理档位」。
//
// 源: packages/core/agent/src/model-selection.ts:9-17（ModelSelection）
//
// ReasoningEffort 为空串表示没选档位，交回给提供方或者适配器自己的默认行为——这条
// 「空串即缺失」的约定是 [llm.CallConfig].ReasoningEffort 定下的，这里照用，好让
// 两边不必来回翻译。
type ModelSelection struct {
	// Provider 是登记过的那个提供方路由键。
	Provider string
	// Model 是那个提供方自己拥有的模型标识。
	Model string
	// ReasoningEffort 是适配器自己拥有的推理档位；空串表示没选。
	ReasoningEffort llm.ReasoningEffortID
}

// ModelSelectionRef 是那份可变的选择，外加「当下这个步骤进装配时抓下来的那一份」。
//
// 源: packages/core/agent/src/model-selection.ts:19-25（ModelSelectionRef）
//
// 两个字段而不是一个，是这套东西的全部要害：装配在委托下去**之前**把当下选中的
// 那一份抓下来，请求路由随后只认那一份抓拍。于是一次和步骤赛跑的切换要么整个落在
// 这一步、要么整个落在下一步，绝不会出现「提示词里写着 A、请求发给了 B」。
//
// 新增: DSH 是单线程 JS，这个 ref 就是一个裸对象。Go 里切换模型的那一方和跑步骤的
// 那一方是两个 goroutine——上面那句「和步骤赛跑」在 JS 里只是宏观时序，在 Go 里
// 是一次真的数据竞争，所以这里自带互斥锁。锁只护这两个字段，护不到别的地方；
// 观察者一律在锁外跑。
type ModelSelectionRef struct {
	mutex        sync.Mutex
	current      ModelSelection
	hasCurrent   bool
	assembled    ModelSelection
	hasAssembled bool
}

// NewModelSelectionRef 造一份还没选中任何模型的引用。
//
// 没选中时这套接线整个是透传的：装配不动变量，请求路由不动配置。
func NewModelSelectionRef() *ModelSelectionRef {
	return &ModelSelectionRef{}
}

// Select 把下一个进装配的步骤要用的模型换成 selection。
//
// 换的是「下一个」而不是「当下这个」：一个已经进了装配的步骤抓的是它进去那一刻的
// 那一份，见 [ModelSelectionRef] 上的说明。
func (r *ModelSelectionRef) Select(selection ModelSelection) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.current = selection
	r.hasCurrent = true
}

// Clear 撤掉当下的选择，让后面的步骤回到调用方自己那份配置。
//
// 它清不掉已经被某个在跑的步骤抓走的那一份抓拍——那一步该用什么在它进装配时就
// 定死了。
func (r *ModelSelectionRef) Clear() {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.current = ModelSelection{}
	r.hasCurrent = false
}

// Current 读出当下选中的那一份。第二个返回值为假表示没选。
//
// 新增: 这里不拿零值当「没选」，而另拿一位说，规矩和 [CurrentInitiator]、
// [Registry.Get] 一样。零值确实和任何一份合法选择都不撞车（空的提供方不是一个
// 能路由的东西），但那要靠「谁都不许 Select 一份空的」这条invariant撑着，
// 而多一个 bool 不需要任何人守规矩。
func (r *ModelSelectionRef) Current() (ModelSelection, bool) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.current, r.hasCurrent
}

// Assembled 读出当下这个步骤进装配时抓下来的那一份。第二个返回值为假表示那一刻
// 没选中任何模型。
//
// 请求路由认的就是这一份。还没有任何步骤进过装配时它也为假。
func (r *ModelSelectionRef) Assembled() (ModelSelection, bool) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.assembled, r.hasAssembled
}

// setAssembled 记下这一步抓到的那一份，只给装配那条规则用。
func (r *ModelSelectionRef) setAssembled(selection ModelSelection, present bool) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.assembled = selection
	r.hasAssembled = present
}

// InstallModelSelection 把一份选择接到 owner 这个作用域的提示词装配和请求路由上，
// 返回同时摘掉两处的那个函数。
//
// 源: packages/core/agent/src/model-selection.ts:27-75
//
// 装配那条规则在委托下去之前抓下当下的选择，回来之后把 provider 和 model 两个
// 提示词变量按抓到的那一份改写；请求那条瀑布随后把同一份抓拍按到 [llm.CallConfig]
// 上。没选中模型时两边都原样放行。
//
// owner 决定这两条登记落在哪一层，也就决定了它们看得见哪些 agent——按本仓库
// [github.com/snight1983/ds-harness-go/scope.Layers] 的一贯规矩，挂在某个 agent 作用域上的登记只
// 服务那个 agent 和它的子孙。这就是 DSH 那个 agentCtx 参数的意思。
//
// 新增: DSH 拿到的是一个 cordis 上下文，两条事件都从它身上挂；Go 里两个面分属两张
// 注册表，所以两张表都得显式传进来。这也让「这套接线依赖提示词那一层」变成一条
// 编译器看得见的边，而不是一个事件名。
func InstallModelSelection(
	ctx context.Context,
	owner *scope.Scope,
	agents *Registry,
	prompts *systemprompt.Registry,
	selection *ModelSelectionRef,
) (func(context.Context) error, error) {
	if agents == nil {
		return nil, fmt.Errorf("%w：InstallModelSelection 需要一张 agent 注册表", ErrInvalidRegistration)
	}
	if prompts == nil {
		return nil, fmt.Errorf("%w：InstallModelSelection 需要一张提示词注册表", ErrInvalidRegistration)
	}
	if selection == nil {
		return nil, fmt.Errorf("%w：InstallModelSelection 需要一份选择", ErrInvalidRegistration)
	}

	detachAssemble, err := prompts.OnAssemble(ctx, owner, func(
		ctx context.Context,
		assembly systemprompt.PromptAssembly,
		assemble systemprompt.AssembleContext,
		next func(systemprompt.PromptAssembly) (systemprompt.PromptAssembly, error),
	) (systemprompt.PromptAssembly, error) {
		// 抓拍必须在委托下去**之前**取：里层的规则可能自己跑得很久，那期间的一次
		// 切换该算下一步的。
		selected, present := selection.Current()
		assembled, err := next(assembly)
		if err != nil {
			return systemprompt.PromptAssembly{}, err
		}
		// 装配失败时不记抓拍：那一步根本没成形，请求路由也就轮不到它。
		selection.setAssembled(selected, present)
		if !present {
			return assembled, nil
		}
		// 复制一份变量表再改：交进来的那一份是装配算出来的，外层规则可能还留着
		// 它的引用，就地改会把改动漏到这条规则之外去。
		variables := make(map[string]*string, len(assembled.Variables)+2)
		for name, value := range assembled.Variables {
			variables[name] = value
		}
		provider := selected.Provider
		model := selected.Model
		variables["provider"] = &provider
		variables["model"] = &model
		assembled.Variables = variables
		return assembled, nil
	})
	if err != nil {
		return nil, err
	}

	detachRequest, err := agents.OnRequest(ctx, owner, func(
		ctx context.Context,
		request Request,
		next func(context.Context) (llm.CallConfig, error),
	) (llm.CallConfig, error) {
		resolved, err := next(ctx)
		if err != nil {
			return llm.CallConfig{}, err
		}
		selected, present := selection.Assembled()
		if !present {
			return resolved, nil
		}
		resolved.Provider = selected.Provider
		resolved.Model = selected.Model
		// DSH 分两步：先把继承来的推理档位删掉，再只在选中的那一份带档位时加回去。
		// Go 里空串就是「没选」，所以直接赋值一次就同时是那两支。
		resolved.ReasoningEffort = selected.ReasoningEffort
		return resolved, nil
	})
	if err != nil {
		// 第二条挂不上就把第一条撤了，别留下半套接线——那会让提示词说 A、请求还说
		// 调用方原本那一个。
		//
		// 测不到：两条登记的失败条件一模一样（观察者非 nil，宿主作用域还活着），
		// 所以第一条过了第二条就一定过。留着它是因为这个「一模一样」是当下两张
		// 注册表各自的实现细节，不是它们之间的约定。
		return nil, errors.Join(err, detachAssemble(ctx))
	}

	return func(ctx context.Context) error {
		return errors.Join(detachAssemble(ctx), detachRequest(ctx))
	}, nil
}
