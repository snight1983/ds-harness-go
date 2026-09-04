// 本文件的作用：提示词注册表本身——按作用域分层的那几张表、往表里放东西的那几个
// 方法、以及把它们装配成一次模型输入的 [Registry.Assemble]。值那一侧在 prompt.go。
//
// 源: packages/core/system-prompt/src/index.ts:294-545

package systemprompt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
)

// ErrInvalidConfig 是造这个注册表时配置就不成立。
var ErrInvalidConfig = errors.New("systemprompt: 配置不成立")

// ErrInvalidRegistration 是一次登记本身不合法（名字写错了之类）。
var ErrInvalidRegistration = errors.New("systemprompt: 登记不合法")

// ErrInvalidAssembly 是这一次装配凑不出一份说得通的模型输入。
var ErrInvalidAssembly = errors.New("systemprompt: 装配不成立")

// AssembleRule 是装配瀑布上的一环。
//
// 源: packages/core/system-prompt/src/index.ts:31（`system-prompt/assemble`）
//
// 新增: DSH 这是挂在 cordis 事件总线上、靠 ctx.waterfall 分派的监听器；Go 里就是
// 一串显式的「拿到 next、可以不调」的函数，和 tools 那四条瀑布同一个写法。
// 不调 next 就是**短路**：后面登记的规则一条都不会跑。
//
// 新增: DSH 的 next() 不带参数——那边的 assembly 是个可变对象，一条监听器改完它
// 再往下传。Go 的 [PromptAssembly] 是个结构体值，改了不会自己传出去，所以要往下
// 传的那一份得**显式交给 next**。
//
// 按作用域登记的规则只看得见那个作用域的装配；登记在全局层的看得见全部。顺序是
// 先全局、再从最远的祖先到作用域自己，先登记的在外层。
//
// 交回来的那份装配是权威的——只有一个例外：一段生效的 Complete 段落会在瀑布跑完
// 之后被放回去，所以一条规则**加不了也换不掉**那个作用域的系统提示词。
type AssembleRule func(
	ctx context.Context,
	assembly PromptAssembly,
	assemble AssembleContext,
	next func(PromptAssembly) (PromptAssembly, error),
) (PromptAssembly, error)

// promptLayer 是一个全局层或者作用域层持有的全部提示词登记。
//
// 源: packages/core/system-prompt/src/index.ts:302-336
type promptLayer struct {
	sections                  *scope.NamedEntries[PromptSection]
	contexts                  *scope.NamedEntries[PromptContext]
	runtimeContextSuppressors *scope.AnonymousEntries[struct{}]
	toolProviders             *scope.AnonymousEntries[ToolProvider]
	variables                 *scope.NamedEntries[VariableProvider]
	assembleRules             *scope.AnonymousEntries[AssembleRule]
}

// newPromptLayer 造一层，重名诊断按这一层归谁而定。
//
// 源: packages/core/system-prompt/src/index.ts:312-325
//
// 这几句诊断照抄 DSH 的英文原文：它们是登记方（也就是插件作者）读的，和 mcp、skill
// 那边的重名诊断同一条规矩。
func newPromptLayer(key *scope.Key) *promptLayer {
	duplicate := func(kind string) func(string) error {
		return func(name string) error {
			if key == nil {
				return fmt.Errorf(
					"%w: prompt %s %q is already registered (for a per-agent override, register through that agent's scope instead)",
					ErrInvalidRegistration, kind, name,
				)
			}
			return fmt.Errorf(
				"%w: prompt %s %q is already registered in this scope",
				ErrInvalidRegistration, kind, name,
			)
		}
	}
	return &promptLayer{
		sections:                  scope.NewNamedEntries[PromptSection](duplicate("section")),
		contexts:                  scope.NewNamedEntries[PromptContext](duplicate("context")),
		runtimeContextSuppressors: scope.NewAnonymousEntries[struct{}](),
		toolProviders:             scope.NewAnonymousEntries[ToolProvider](),
		variables:                 scope.NewNamedEntries[VariableProvider](duplicate("variable")),
		assembleRules:             scope.NewAnonymousEntries[AssembleRule](),
	}
}

// IsEmpty 表示这一层的每一张表都空了，[scope.Layers] 靠它回收空层。
//
// 源: packages/core/system-prompt/src/index.ts:328-335
func (l *promptLayer) IsEmpty() bool {
	return l.sections.IsEmpty() &&
		l.contexts.IsEmpty() &&
		l.runtimeContextSuppressors.IsEmpty() &&
		l.toolProviders.IsEmpty() &&
		l.variables.IsEmpty() &&
		l.assembleRules.IsEmpty()
}

// Options 是部署方自己写的那一小块系统提示词，加上装配的几条策略。
//
// 源: packages/core/system-prompt/src/index.ts:236-253（Config）、339-345
type Options struct {
	// OmitHarnessIdentity 表示不要在人设前面放那句宿主身份声明。
	//
	// 新增: DSH 是 `includeHarnessIdentity?: boolean` 默认 true。Go 的零值是 false，
	// 所以名字取反——一个什么都没填的 Options 说的就是 DSH 那个默认行为。
	OmitHarnessIdentity bool

	// SuppressRuntimeContext 表示这份部署不要动态运行期上下文快照。
	//
	// 新增: 同上，DSH 是 `includeRuntimeContext?: boolean` 默认 true。
	SuppressRuntimeContext bool

	// Persona 是部署方那份 order 0 的人设模板，可以是空串。
	//
	// 一个同名 `deployment:persona` 的作用域段落会盖住它；里面的 `{{变量}}` 引用是
	// 严格的（写错了名字就报错，不会悄悄留成原文）。
	Persona string

	// ToolOrder 是给模型看的工具次序，里面必须恰好出现一次 [ToolOrderRest]。
	//
	// 为 nil 表示不指定，按字典序排。**空的非 nil 切片不等于 nil**：它是一份明确
	// 写出来、但漏了 rest 那一项的次序，会在这里被判成配错了——这正是 DSH 那边
	// 特意不给 toolOrder 一个 `[]` 默认值的理由。
	//
	// 字段写错了在这里就失败，名字认不出来要等到装配时；某个作用域里被藏起来的
	// 名字在那个作用域可以缺席。
	ToolOrder []string

	// OnChange 在任何一处提示词登记发生变动时调用。
	//
	// 新增: DSH 那边是 cordis 事件 `system-prompt/change`。它**不按作用域过滤**：
	// 一次全局改动影响每一个作用域。
	OnChange func()
}

// Registry 是每一步模型请求之前那些提示词输入的注册表。
//
// 源: packages/core/system-prompt/src/index.ts:388-612（SystemPrompt）
//
// 新增: cordis 的 Service / ctx.systemPrompt / 插件名 / inject 全部不移，装配方
// 自己造一个拿着。分层和遮蔽是 [scope.Layers] 提供的，本包只是它的一个消费方。
type Registry struct {
	// toolOrder 是造出来时就验过的那份配置次序，nil 表示按字典序。
	toolOrder []string

	// layers 是全局层加上各个作用域层。
	//
	// 本类型没有自己的锁：toolOrder 造出来之后就不再变，分层表自己是并发安全的。
	layers *scope.Layers[*promptLayer]
}

// NewRegistry 造一个注册表，并把宿主身份和部署方人设这两段登记到 owner 那一层。
//
// 源: packages/core/system-prompt/src/index.ts:351-370
//
// 新增: DSH 的构造函数拿的是插件自己的 ctx，那两段固定登记就落在那个 ctx 的作用域
// 上；Go 里那个位置由 owner 显式传进来。装配方一般给一个 [scope.NewRoot] 造的根
// 作用域，于是它们落在全局层——和 DSH 一样。owner 释放时这两段跟着撤销。
func NewRegistry(ctx context.Context, owner *scope.Scope, options Options) (*Registry, error) {
	if owner == nil {
		return nil, fmt.Errorf("%w: NewRegistry 需要一个持有它的作用域", ErrInvalidConfig)
	}
	if err := validateToolOrder(options.ToolOrder); err != nil {
		return nil, err
	}

	registry := &Registry{toolOrder: options.ToolOrder}
	layers, err := scope.NewLayers(
		func(key *scope.Key) (*promptLayer, error) { return newPromptLayer(key), nil },
		func() error {
			if options.OnChange != nil {
				options.OnChange()
			}
			return nil
		},
	)
	if err != nil {
		// 走不到：scope.NewLayers 只在造全局层时失败，而上面那个构造函数不会失败。
		// 它是 scope 那一侧的签名，本包无权改；照实转出去比在这里吞掉它诚实。
		return nil, err
	}
	registry.layers = layers

	// 宿主自己那句身份声明和选哪个循环插件无关，所以放在这里而不是循环那边。
	if !options.OmitHarnessIdentity {
		if _, err := registry.Section(ctx, owner, PromptSection{
			Name:  harnessIdentitySection,
			Order: harnessIdentityOrder,
			Text:  StaticText(harnessIdentityText),
		}); err != nil {
			return nil, err
		}
	}
	// 人设那一段**无条件**登记，哪怕正文是空串：空段落在渲染时会被丢掉，而这个
	// 槽位存在本身就是 agent 预设能够替换它的前提。
	if _, err := registry.Section(ctx, owner, PromptSection{
		Name:  PersonaSection,
		Order: PersonaOrder,
		Text:  StaticText(options.Persona),
	}); err != nil {
		return nil, err
	}
	if options.SuppressRuntimeContext {
		if _, err := registry.SuppressRuntimeContext(ctx, owner); err != nil {
			// 走不到：这一步失败的条件和上面那两段登记完全一样（owner 已经不能再挂
			// 副作用了），所以真出事的时候上面就已经返回了。照实转出去比在这里假设
			// 它不会失败诚实。
			return nil, err
		}
	}
	return registry, nil
}

// Section 把一段提示词登记到 owner 那一层，返回撤销这次登记的函数。
//
// 源: packages/core/system-prompt/src/index.ts:381-390
//
// 作用域里的段落会盖住全局层里同名的那一段；同一层里重名会报错。登记和撤销都会
// 发变更通知。
func (r *Registry) Section(ctx context.Context, owner *scope.Scope, section PromptSection) (func(context.Context) error, error) {
	if section.Text == nil {
		return nil, fmt.Errorf("%w: 段落 %q 得给一份正文", ErrInvalidRegistration, section.Name)
	}
	return r.layers.Effect(ctx, owner, func(layer *promptLayer) (func(), error) {
		return layer.sections.Insert(section.Name, section)
	}, scope.EffectOptions{Label: "systemPrompt.Section()"})
}

// Context 把一份动态上下文登记到 owner 那一层，返回撤销这次登记的函数。
//
// 源: packages/core/system-prompt/src/index.ts:398-407
//
// 作用域里的会盖住全局层里同名的那一份。
func (r *Registry) Context(ctx context.Context, owner *scope.Scope, promptContext PromptContext) (func(context.Context) error, error) {
	if promptContext.Text == nil {
		return nil, fmt.Errorf("%w: 上下文 %q 得给一份正文", ErrInvalidRegistration, promptContext.Name)
	}
	return r.layers.Effect(ctx, owner, func(layer *promptLayer) (func(), error) {
		return layer.contexts.Insert(promptContext.Name, promptContext)
	}, scope.EffectOptions{Label: "systemPrompt.Context()"})
}

// SuppressRuntimeContext 在 owner 那一层压掉全部动态运行期上下文贡献。
//
// 源: packages/core/system-prompt/src/index.ts:415-421
//
// 它不动那些持有或者执行这些事实的服务，只是让它们这次不出现在提示里。压制可以
// 有多份，各自独立撤销。
func (r *Registry) SuppressRuntimeContext(ctx context.Context, owner *scope.Scope) (func(context.Context) error, error) {
	return r.layers.Effect(ctx, owner, func(layer *promptLayer) (func(), error) {
		return layer.runtimeContextSuppressors.Append(struct{}{}), nil
	}, scope.EffectOptions{Label: "systemPrompt.SuppressRuntimeContext()"})
}

// Tools 把一个工具提供方登记到 owner 那一层，返回撤销这次登记的函数。
//
// 源: packages/core/system-prompt/src/index.ts:429-435
//
// 全局的和对得上的作用域提供方都会贡献；报出保留名 [ToolOrderRest] 会让装配失败。
func (r *Registry) Tools(ctx context.Context, owner *scope.Scope, provider ToolProvider) (func(context.Context) error, error) {
	if provider == nil {
		return nil, fmt.Errorf("%w: Tools 需要一个提供方", ErrInvalidRegistration)
	}
	return r.layers.Effect(ctx, owner, func(layer *promptLayer) (func(), error) {
		return layer.toolProviders.Append(provider), nil
	}, scope.EffectOptions{Label: "systemPrompt.Tools()"})
}

// Variable 把一个提示词变量登记到 owner 那一层，返回撤销这次登记的函数。
//
// 源: packages/core/system-prompt/src/index.ts:446-455
//
// 作用域里的值盖住全局的；名字不合文法或者重名会报错。提供方可以交回 nil，但引用
// 到那个值的段落渲染时会失败。
func (r *Registry) Variable(ctx context.Context, owner *scope.Scope, name string, provider VariableProvider) (func(context.Context) error, error) {
	if !variableNamePattern.MatchString(name) {
		return nil, fmt.Errorf(
			"%w: invalid prompt variable name %q (must match %s)",
			ErrInvalidRegistration, name, variableNamePattern.String(),
		)
	}
	if provider == nil {
		return nil, fmt.Errorf("%w: 变量 %q 得给一个提供方", ErrInvalidRegistration, name)
	}
	return r.layers.Effect(ctx, owner, func(layer *promptLayer) (func(), error) {
		return layer.variables.Insert(name, provider)
	}, scope.EffectOptions{Label: "systemPrompt.Variable()"})
}

// OnAssemble 把一条规则挂到装配瀑布上，返回撤销它的函数。
//
// 源: packages/core/system-prompt/src/index.ts:31（`system-prompt/assemble`）
//
// 它不发变更通知：一条规则不改变**登记了什么**，只改变装配出来的东西长什么样。
func (r *Registry) OnAssemble(ctx context.Context, owner *scope.Scope, rule AssembleRule) (func(context.Context) error, error) {
	if rule == nil {
		return nil, fmt.Errorf("%w: OnAssemble 需要一条规则", ErrInvalidRegistration)
	}
	return r.layers.Effect(ctx, owner, func(layer *promptLayer) (func(), error) {
		return layer.assembleRules.Append(rule), nil
	}, scope.EffectOptions{Label: "systemPrompt.OnAssemble()", Silent: true})
}

// Assemble 把全局的和作用域链上的提供方装配成一次模型输入。
//
// 源: packages/core/system-prompt/src/index.ts:465-544
//
// 步骤是：解出变量（近的作用域盖远的）→ 合并段落和上下文 → 问遍工具提供方并把
// 参数 schema 摘出来 → 排成最终次序 → 跑装配瀑布。瀑布交回来的那份是权威的，只有
// 一个例外：一段生效的 Complete 段落会在之后被放回去当唯一的段落。
func (r *Registry) Assemble(ctx context.Context, assemble AssembleContext) (PromptAssembly, error) {
	if err := ctx.Err(); err != nil {
		return PromptAssembly{}, context.Cause(ctx)
	}

	key := assemble.Scope
	chain := r.layers.ChainLayers(key)

	runtimeContextSuppressed := !r.layers.Global().runtimeContextSuppressors.IsEmpty()
	for _, layer := range chain {
		if !layer.runtimeContextSuppressors.IsEmpty() {
			runtimeContextSuppressed = true
		}
	}

	variables, err := r.resolveVariables(ctx, chain, assemble)
	if err != nil {
		return PromptAssembly{}, err
	}

	tools, knownNames, err := r.collectTools(ctx, chain, assemble)
	if err != nil {
		return PromptAssembly{}, err
	}
	ordered, err := orderTools(tools, r.toolOrder, knownNames)
	if err != nil {
		return PromptAssembly{}, err
	}

	sections, completeSection, err := r.assembleSections(ctx, key, assemble)
	if err != nil {
		return PromptAssembly{}, err
	}

	var contexts []AssembledContext
	if !runtimeContextSuppressed {
		if contexts, err = r.assembleContexts(ctx, key, assemble); err != nil {
			return PromptAssembly{}, err
		}
	}

	assembly := PromptAssembly{
		Sections:  sections,
		Contexts:  contexts,
		Tools:     ordered,
		Variables: variables,
	}

	transformed, err := runAssembleRules(ctx, r.assembleRulesFor(key), assembly, assemble)
	if err != nil {
		return PromptAssembly{}, err
	}
	if completeSection == nil && !runtimeContextSuppressed {
		return transformed, nil
	}
	if completeSection != nil {
		transformed.Sections = []AssembledSection{*completeSection}
	}
	if runtimeContextSuppressed {
		transformed.Contexts = nil
	}
	return transformed, nil
}

// resolveVariables 解出这次装配的变量表：先全局，再从最远的祖先到近亲。
//
// 源: packages/core/system-prompt/src/index.ts:472-483
//
// 近的那一层后写，所以同名时近的赢。
func (r *Registry) resolveVariables(
	ctx context.Context,
	chain []*promptLayer,
	assemble AssembleContext,
) (map[string]*string, error) {
	variables := map[string]*string{}
	resolve := func(layer *promptLayer) error {
		for name, provider := range layer.variables.All() {
			value, err := provider(ctx, assemble)
			if err != nil {
				return fmt.Errorf("%w: 变量 %q 求值失败: %w", ErrInvalidAssembly, name, err)
			}
			variables[name] = value
		}
		return nil
	}
	if err := resolve(r.layers.Global()); err != nil {
		return nil, err
	}
	for _, layer := range chain {
		if err := resolve(layer); err != nil {
			return nil, err
		}
	}
	return variables, nil
}

// collectTools 问遍这次看得见的工具提供方，交出它们报的工具和那份名字全集。
//
// 源: packages/core/system-prompt/src/index.ts:489-505
func (r *Registry) collectTools(
	ctx context.Context,
	chain []*promptLayer,
	assemble AssembleContext,
) ([]llm.ToolSchema, map[string]struct{}, error) {
	var providers []ToolProvider
	for provider := range r.layers.Global().toolProviders.Values() {
		providers = append(providers, provider)
	}
	for _, layer := range chain {
		for provider := range layer.toolProviders.Values() {
			providers = append(providers, provider)
		}
	}

	var collected []llm.ToolSchema
	knownNames := map[string]struct{}{}
	for _, provider := range providers {
		result, err := provider(ctx, assemble)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: 工具提供方失败: %w", ErrInvalidAssembly, err)
		}
		for _, schema := range result.Schemas {
			// 把参数 schema 拷一份再交出去，对应 DSH 那个 structuredClone：一条装配
			// 规则改到的必须是这次装配的副本，而不是提供方自己留着的那份。
			collected = append(collected, llm.ToolSchema{
				Name:        schema.Name,
				Description: schema.Description,
				Parameters:  bytes.Clone(schema.Parameters),
			})
		}
		accepted := result.KnownNames
		if accepted == nil {
			for _, schema := range result.Schemas {
				knownNames[schema.Name] = struct{}{}
			}
			continue
		}
		for _, name := range accepted {
			knownNames[name] = struct{}{}
		}
	}
	return collected, knownNames, nil
}

// assembleSections 合并段落、按次序排好、逐段求值，并挑出那段生效的 Complete。
//
// 源: packages/core/system-prompt/src/index.ts:506-521
func (r *Registry) assembleSections(
	ctx context.Context,
	key *scope.Key,
	assemble AssembleContext,
) ([]AssembledSection, *AssembledSection, error) {
	merged := scope.MergeNamed(r.layers, key, func(layer *promptLayer) *scope.NamedEntries[PromptSection] {
		return layer.sections
	})
	definitions := make([]PromptSection, 0, len(merged))
	for _, entry := range merged {
		definitions = append(definitions, entry.Value)
	}
	sort.SliceStable(definitions, func(left, right int) bool {
		return definitions[left].Order < definitions[right].Order
	})

	var completeNames []string
	for _, section := range definitions {
		if section.Complete {
			completeNames = append(completeNames, fmt.Sprintf("%q", section.Name))
		}
	}
	if len(completeNames) > 1 {
		return nil, nil, fmt.Errorf(
			"%w: multiple complete prompt sections are active: %s",
			ErrInvalidAssembly, strings.Join(completeNames, ", "),
		)
	}

	var completeSection *AssembledSection
	sections := make([]AssembledSection, 0, len(definitions))
	for _, section := range definitions {
		text, err := section.Text(ctx, assemble)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: 段落 %q 求值失败: %w", ErrInvalidAssembly, section.Name, err)
		}
		assembled := AssembledSection{Name: section.Name, Text: text}
		sections = append(sections, assembled)
		if section.Complete {
			// 单独留一份副本，好在瀑布跑完之后原样放回去。
			kept := assembled
			completeSection = &kept
		}
	}
	return sections, completeSection, nil
}

// assembleContexts 合并动态上下文、按次序排好、逐份求值。
//
// 源: packages/core/system-prompt/src/index.ts:524-532
func (r *Registry) assembleContexts(
	ctx context.Context,
	key *scope.Key,
	assemble AssembleContext,
) ([]AssembledContext, error) {
	merged := scope.MergeNamed(r.layers, key, func(layer *promptLayer) *scope.NamedEntries[PromptContext] {
		return layer.contexts
	})
	definitions := make([]PromptContext, 0, len(merged))
	for _, entry := range merged {
		definitions = append(definitions, entry.Value)
	}
	sort.SliceStable(definitions, func(left, right int) bool {
		return definitions[left].Order < definitions[right].Order
	})

	contexts := make([]AssembledContext, 0, len(definitions))
	for _, entry := range definitions {
		text, err := entry.Text(ctx, assemble)
		if err != nil {
			return nil, fmt.Errorf("%w: 上下文 %q 求值失败: %w", ErrInvalidAssembly, entry.Name, err)
		}
		contexts = append(contexts, AssembledContext{Name: entry.Name, Text: text})
	}
	return contexts, nil
}

// assembleRulesFor 收齐这次装配看得见的那些规则：先全局，再从最远的祖先到近亲。
//
// 源: packages/core/system-prompt/src/index.ts:536（`scopeTarget(this, scope)`）
func (r *Registry) assembleRulesFor(key *scope.Key) []AssembleRule {
	var rules []AssembleRule
	for rule := range r.layers.Global().assembleRules.Values() {
		rules = append(rules, rule)
	}
	for _, layer := range r.layers.ChainLayers(key) {
		for rule := range layer.assembleRules.Values() {
			rules = append(rules, rule)
		}
	}
	return rules
}

// runAssembleRules 按顺序把那串规则套起来跑，最里面一层原样交回当前这份装配。
//
// 源: packages/core/system-prompt/src/index.ts:535-538
func runAssembleRules(
	ctx context.Context,
	rules []AssembleRule,
	assembly PromptAssembly,
	assemble AssembleContext,
) (PromptAssembly, error) {
	var step func(index int, current PromptAssembly) (PromptAssembly, error)
	step = func(index int, current PromptAssembly) (PromptAssembly, error) {
		if index >= len(rules) {
			return current, nil
		}
		return rules[index](ctx, current, assemble, func(next PromptAssembly) (PromptAssembly, error) {
			return step(index+1, next)
		})
	}
	return step(0, assembly)
}
