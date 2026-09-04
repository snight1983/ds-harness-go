// 本文件的作用：工具注册表——谁注册了什么工具、某个 agent 看得见其中哪些、
// 以及一次调用在进执行体之前要过哪几道关的**登记**部分。
//
// 源: packages/core/tools/src/index.ts:648-1240
//
// 派发管线本身在 pipeline.go；这里只管「有什么」和「谁看得见」。
// 两者分开是因为可见性是一次纯粹的层遍历，而派发是一条带取消、带瀑布、
// 带外部审批的时序，混在一个文件里读的人得同时装着两套心智模型。
//
// # 作用域是这一切的骨架
//
// 一个进程里活着很多个 agent，agent 又挂在预设下面。工具的注册、限制、守卫
// 全都按这条链继承：预设上注册的工具，它下面每个 agent 都看得见；agent 自己
// 注册的工具只有它自己看得见，并且**盖住**同名的继承项。这条链由
// [scope.Layers] 提供，本包只是它的一个消费方。
//
// # 没有 PTC
//
// DSH 这个包有一大块是 PTC（alpha.3 之前叫 Code Mode）：把所有工具收拢成一个
// `run_code` 工具，模型写一段 TypeScript 或 Python 程序，程序在 Node 的
// worker thread 里跑，通过生成的 SDK 反过来调那些工具。整块不移，理由见
// docs/portmap/decisions.md 的「tools —— PTC（run_code）整块不移」。

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
)

// Guard 是一道单调的执行守卫：返回非空字符串就是拒绝，返回空串表示不表态。
//
// 源: packages/core/tools/src/index.ts:696-704（ToolGuard）
//
// 「单调」是这个类型唯一的设计点：守卫**只能拒绝，不能放行**。所以注册顺序
// 影响不了结果——先跑的守卫拒了，后跑的守卫没有任何办法把它改回允许。
// 可以放行的那种表态在 [PreRule] 那条瀑布上，它是可扩展的；守卫是最后一道闸。
type Guard func(exec Execution) string

// Restriction 是一个作用域对**继承来的**全局工具的过滤。
//
// 源: packages/core/tools/src/index.ts:669-678（ToolRestriction）
//
// 多条限制取交集，并且都不影响这个作用域**自己**注册的工具——见 [Runtime.view]
// 里那段说明，那条豁免是「按子 agent 发能力清单」这件事能成立的前提。
type Restriction struct {
	// Allow 是保留下来的全局工具名，给了就表示「只留这些」。
	Allow []string
	// Deny 是要摘掉的全局工具名。
	Deny []string
}

// compiledRestriction 是注册时就编译好的限制，供后续反复查表。
//
// 源: packages/core/tools/src/index.ts:684-687
type compiledRestriction struct {
	// allow 为 nil 表示这条限制没有 allow 这一半。
	allow map[string]struct{}
	// deny 为 nil 表示这条限制没有 deny 这一半。
	deny map[string]struct{}
}

// admits 说明这条限制放不放行某个全局工具名。
func (c compiledRestriction) admits(name string) bool {
	if c.allow != nil {
		if _, ok := c.allow[name]; !ok {
			return false
		}
	}
	if c.deny != nil {
		if _, ok := c.deny[name]; ok {
			return false
		}
	}
	return true
}

// toolLayer 是一个作用域在这张注册表里的全部贡献。
//
// 源: packages/core/tools/src/index.ts:713-757
//
// 新增: DSH 那边 pre-execute / execute / post-execute / result 四条是挂在 cordis
// 事件总线上的监听器，靠 `ctx.waterfall` 按作用域链分派。Go 这边没有那条总线，
// 它们和工具、限制、守卫一样落在这一层里，按同一条链遍历——参照
// feature/telemetry.Rule 立下的先例：cordis 的瀑布在 Go 里就是一串显式的
// 「拿到 next、可以不调」的函数。
type toolLayer struct {
	// tools 是这一层注册的工具，按插入顺序。
	tools *scope.NamedEntries[*Definition]
	// restrictions 是这一层对继承面的过滤，多条取交集。
	restrictions *scope.AnonymousEntries[compiledRestriction]
	// guards 是这一层的单调守卫。
	guards *scope.AnonymousEntries[Guard]
	// preRules 是这一层的执行前瀑布。
	preRules *scope.AnonymousEntries[PreRule]
	// dispatchRules 是这一层的绕派发瀑布。
	dispatchRules *scope.AnonymousEntries[DispatchRule]
	// postRules 是这一层的执行后瀑布。
	postRules *scope.AnonymousEntries[PostRule]
	// observers 是这一层的结果观察者。
	observers *scope.AnonymousEntries[ResultObserver]
}

// newToolLayer 造一层。scope 为 nil 表示这是全局层，只影响重名时的报错措辞。
//
// 源: packages/core/tools/src/index.ts:726-730
func newToolLayer(key *scope.Key) *toolLayer {
	return &toolLayer{
		tools: scope.NewNamedEntries[*Definition](func(name string) error {
			if key == nil {
				return fmt.Errorf("tools: 工具 %q 已经注册过了（想给某个 agent 单独换一份，改用那个 agent 的作用域注册）", name)
			}
			return fmt.Errorf("tools: 工具 %q 在这个作用域里已经注册过了", name)
		}),
		restrictions:  scope.NewAnonymousEntries[compiledRestriction](),
		guards:        scope.NewAnonymousEntries[Guard](),
		preRules:      scope.NewAnonymousEntries[PreRule](),
		dispatchRules: scope.NewAnonymousEntries[DispatchRule](),
		postRules:     scope.NewAnonymousEntries[PostRule](),
		observers:     scope.NewAnonymousEntries[ResultObserver](),
	}
}

// IsEmpty 表示这一层的每一张表都空了，[scope.Layers] 靠它回收空层。
//
// 源: packages/core/tools/src/index.ts:733-736
func (l *toolLayer) IsEmpty() bool {
	return l.tools.IsEmpty() && l.restrictions.IsEmpty() && l.guards.IsEmpty() &&
		l.preRules.IsEmpty() && l.dispatchRules.IsEmpty() && l.postRules.IsEmpty() &&
		l.observers.IsEmpty()
}

// admits 表示这一层的每一条限制都放行某个全局工具名。
//
// 源: packages/core/tools/src/index.ts:739-745
func (l *toolLayer) admits(name string) bool {
	for filter := range l.restrictions.Values() {
		if !filter.admits(name) {
			return false
		}
	}
	return true
}

// guardReason 给出这一层第一条守卫拒绝的理由，没有就是空串。
//
// 源: packages/core/tools/src/index.ts:748-754
func (l *toolLayer) guardReason(exec Execution) string {
	for guard := range l.guards.Values() {
		if reason := guard(exec); reason != "" {
			return reason
		}
	}
	return ""
}

// Options 是造一个 [Runtime] 的选项。
//
// 源: packages/core/tools/src/index.ts:651-670
//
// 新增: DSH 的 Config 只有 mode 和 maxParallelSubCalls 两项，两项都只服务
// Code Mode，随那一块一起不移。剩下的这两项在 DSH 那边是 cordis 从 ctx 上取的
// （`ctx.logger`、`ctx.get('approval')`），Go 里没有那个隐式容器，所以显式传进来。
type Options struct {
	// Approval 是审批接缝，可以为 nil。
	//
	// 为 nil 时，一次 [PreAsk] 裁决降级成拒绝——和 DSH 「没有 ApprovalService
	// 就退化成 deny」的历史行为一致。它是**用的时候才取**的可选后端，
	// 不是构造依赖：一个从不 ask 的部署不该因为没装审批通道就装配不起来。
	Approval Approval

	// Logger 用来报告结果观察者自己抛出来的错误，为 nil 时用 slog.Default()。
	Logger *slog.Logger

	// OnChange 在可见工具集发生变化时被调一次，可以为 nil。
	//
	// 源: packages/core/tools/src/index.ts:811-814（`ctx.emit('tools/change')`）
	//
	// 用它的是系统提示装配：工具清单变了，缓存下来的提示词就得重算。
	// 守卫的登记不触发它——守卫不改变**看得见什么**，只改变能不能跑。
	OnChange func()

	// MaxArgumentBytes 是一次调用的参数最多能有多少字节；零表示用
	// [DefaultMaxArgumentBytes]，负数表示不设限。
	//
	// 新增: 见 [ErrArgsTooLarge]。缺省值定得比任何一份真实工具参数都宽，
	// 所以现有部署感觉不到它——它挡的是「一个调用方把几十兆的载荷当参数递进来」
	// 那种形状，而那份载荷会被拷进执行对象、写进会话事件、再进模型历史，
	// 这一路上原本没有任何一处会拦它。
	//
	// 负数是明着关掉它的那个口子：一个宿主如果确实要让大载荷走这条路，
	// 那该是一次显式的决定，而不是把上限调成一个大得看不出意图的数字。
	MaxArgumentBytes int
}

// DefaultMaxArgumentBytes 是一次调用参数的缺省字节上限。
//
// 新增: 一兆。真实工具的参数是模型写的一小段 JSON，通常几百字节到几千字节；
// 最宽的那些（贴一整段代码进去的编辑类工具）也在十万字节这个量级。
// 一兆留了一个数量级的余量，同时把「几十兆的载荷」挡在外面。
const DefaultMaxArgumentBytes = 1 << 20

// Runtime 是工具注册表和派发管线。
//
// 源: packages/core/tools/src/index.ts:783-789
//
// 作用域注册盖住全局注册，一个可见性解析器同时喂给三件事：给模型看的 schema、
// 按名字查工具、以及派发时解析该跑哪一份定义。三者共用同一个解析器不是省事，
// 是**必须**：模型看到的清单和真正能跑的清单一旦分头算，就会出现「提示词里写着
// 有这个工具，调过去说不认识」这种自相矛盾。
type Runtime struct {
	// layers 是全局层加各作用域的覆盖层。
	layers *scope.Layers[*toolLayer]
	// approval 是审批接缝，可以为 nil。
	approval Approval
	// logger 用来报告观察者抛出来的错误。
	logger *slog.Logger
	// maxArgumentBytes 是一次调用参数的字节上限；非正数表示不设限。
	maxArgumentBytes int
}

// NewRuntime 造一个注册表。
//
// 源: packages/core/tools/src/index.ts:825-836
func NewRuntime(options Options) (*Runtime, error) {
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	onChange := options.OnChange
	layers, err := scope.NewLayers(
		func(key *scope.Key) (*toolLayer, error) { return newToolLayer(key), nil },
		func() error {
			if onChange != nil {
				onChange()
			}
			return nil
		},
	)
	if err != nil {
		// 走不到：scope.NewLayers 只在造全局层时失败，而上面那个构造函数不会失败。
		// 它是 scope 那一侧的签名，本包无权改；照实转出去比在这里吞掉它诚实。
		return nil, err
	}
	maxArgumentBytes := options.MaxArgumentBytes
	if maxArgumentBytes == 0 {
		maxArgumentBytes = DefaultMaxArgumentBytes
	}
	return &Runtime{
		layers:           layers,
		approval:         options.Approval,
		logger:           logger,
		maxArgumentBytes: maxArgumentBytes,
	}, nil
}

// ErrInvalidDefinition 表示一份工具定义本身就不合法，注册被拒。
var ErrInvalidDefinition = errors.New("tools: 工具定义不合法")

// Register 注册一个工具，返回撤销这次注册的函数。
//
// 源: packages/core/tools/src/index.ts:1020-1060
//
// owner 决定这次注册落在哪一层：[scope.NewRoot] 造的作用域没有身份，落全局层，
// 所有 agent 都看得见；有身份的作用域落它自己那一层，只有它和它下面的看得见。
//
// 定义在这里就验完，不留到调用时：一份 schema 不合法的工具如果能注册进去，
// 那这个错误要等到模型真的调它、甚至等到装配系统提示时才炸，而那时报错的位置
// 离写错的地方已经隔了很远。
func (r *Runtime) Register(ctx context.Context, owner *scope.Scope, definition *Definition) (func(context.Context) error, error) {
	if err := validateDefinition(definition); err != nil {
		return nil, err
	}
	return r.layers.Effect(ctx, owner, func(layer *toolLayer) (func(), error) {
		return layer.tools.Insert(definition.Name, definition)
	}, scope.EffectOptions{Label: "tools.Register()"})
}

// validateDefinition 检查一份定义自身是否成立。
//
// 源: packages/core/tools/src/index.ts:1025-1050
func validateDefinition(definition *Definition) error {
	if definition == nil {
		return fmt.Errorf("%w: 定义是 nil", ErrInvalidDefinition)
	}
	if definition.Name == "" {
		return fmt.Errorf("%w: 工具名不能为空", ErrInvalidDefinition)
	}
	if definition.Execute == nil {
		return fmt.Errorf("%w: 工具 %q 没有执行体", ErrInvalidDefinition, definition.Name)
	}
	if err := AssertObjectSchema(definition.Parameters); err != nil {
		return fmt.Errorf("%w: 工具 %q 的参数 schema 不成立：%w", ErrInvalidDefinition, definition.Name, err)
	}
	if definition.Output.Render == nil {
		return fmt.Errorf("%w: 工具 %q 必须给出 Output.Render", ErrInvalidDefinition, definition.Name)
	}
	if err := AssertSupportedSchema(definition.Output.Schema); err != nil {
		return fmt.Errorf("%w: 工具 %q 的输出 schema 不成立：%w", ErrInvalidDefinition, definition.Name, err)
	}
	if definition.Timeout < 0 {
		return fmt.Errorf("%w: 工具 %q 的 Timeout 不能是负数", ErrInvalidDefinition, definition.Name)
	}
	return nil
}

// ErrInvalidRestriction 表示一次限制的写法不成立。
var ErrInvalidRestriction = errors.New("tools: 限制不成立")

// Restrict 为调用方所在的作用域过滤继承来的全局工具，返回解除这次限制的函数。
//
// 源: packages/core/tools/src/index.ts:1069-1097
//
// 只能在有身份的作用域上用。一次全局的限制会盖住每一个 agent——那件事应该由
// 「不给它注册」来表达，而不是先注册再全局摘掉。
//
// 空过滤器被拒绝：`Restrict(Restriction{})` 什么也不做，而它出现的场合几乎总是
// 一份配置化出来的空结构体，静默放过就把一个配置错误变成了运行时的沉默。
func (r *Runtime) Restrict(ctx context.Context, owner *scope.Scope, filter Restriction) (func(context.Context) error, error) {
	if owner == nil || owner.Key() == nil {
		return nil, fmt.Errorf("%w: Restrict 需要一个有身份的作用域（agent 自己的那个）", ErrInvalidRestriction)
	}
	if filter.Allow == nil && filter.Deny == nil {
		return nil, fmt.Errorf("%w: 空过滤器是空操作，Allow 和 Deny 至少给一个", ErrInvalidRestriction)
	}
	compiled := compiledRestriction{}
	if filter.Allow != nil {
		compiled.allow = toSet(filter.Allow)
	}
	if filter.Deny != nil {
		compiled.deny = toSet(filter.Deny)
	}
	known := r.viewOf(owner.Key()).restrictableNames
	var unknown []string
	for _, name := range slices.Concat(filter.Allow, filter.Deny) {
		if _, ok := known[name]; !ok && !slices.Contains(unknown, name) {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return nil, fmt.Errorf("%w: 点到了不存在的全局工具 %s；现有的全局工具：%s",
			ErrInvalidRestriction, quoteList(unknown), quoteList(sortedKeys(known)))
	}
	return r.layers.Effect(ctx, owner, func(layer *toolLayer) (func(), error) {
		return layer.restrictions.Append(compiled), nil
	}, scope.EffectOptions{Label: "tools.Restrict()"})
}

// Guard 注册一道单调守卫，返回撤销它的函数。
//
// 源: packages/core/tools/src/index.ts:696-704（ToolGuard）
//
// 落在全局层的守卫管所有 agent，落在某个作用域的只管那条链下面的。
// 这次登记不发变更通知——守卫不改变工具的可见性，系统提示不用因此重算。
func (r *Runtime) Guard(ctx context.Context, owner *scope.Scope, guard Guard) (func(context.Context) error, error) {
	if guard == nil {
		return nil, errors.New("tools: 守卫不能是 nil")
	}
	return r.layers.Effect(ctx, owner, func(layer *toolLayer) (func(), error) {
		return layer.guards.Append(guard), nil
	}, scope.EffectOptions{Label: "tools.Guard()", Silent: true})
}

// guardReason 按「先全局、再从最远的祖先到自己」的顺序取第一条拒绝理由。
//
// 源: packages/core/tools/src/index.ts:1117-1126
func (r *Runtime) guardReason(exec Execution) string {
	if reason := r.layers.Global().guardReason(exec); reason != "" {
		return reason
	}
	if exec.Agent == nil {
		return ""
	}
	for _, layer := range r.layers.ChainLayers(exec.Agent) {
		if reason := layer.guardReason(exec); reason != "" {
			return reason
		}
	}
	return ""
}

// view 是一个作用域看到的完整注册表视图。
//
// 源: packages/core/tools/src/index.ts:690-699
type view struct {
	// visible 是过滤、遮盖之后真正看得见的工具。
	visible map[string]*Definition
	// order 是 visible 的稳定顺序：继承面在前（远祖先到近祖先），自己注册的在后。
	//
	// 新增: DSH 用的是 JS 的 Map，它自带插入顺序。Go 的 map 无序，而这个顺序
	// 是**对外可见**的——给模型的工具清单顺序会进提示词缓存的键，每次装配都换一个
	// 顺序等于每次都缓存未命中。所以顺序必须自己拿一个切片存住。
	order []string
	// knownNames 是过滤之前的全部能力名，供提示词顺序校验用。
	knownNames map[string]struct{}
	// restrictableNames 是当前一条限制可以点名的全局工具名。
	restrictableNames map[string]struct{}
}

// viewOf 一次层遍历解析出一个作用域需要的全部注册表事实。
//
// 源: packages/core/tools/src/index.ts:1148-1195
//
// visible 的算法是：先取**继承面**（全局层加链上每个祖先层，近的盖远的），
// 对它施加整条链上的全部限制；再把这个作用域**自己**注册的盖上去，不受限制影响。
//
// 「自己注册的不受限制影响」这条豁免是关键，它是「按子 agent 发能力清单」能成立的
// 前提：委派运行时会把子 agent 的上报工具、结构化输出工具注册进**子 agent 自己那一层**，
// 而一份点名「这个孩子能用哪些能力」的过滤器绝不能把它答话用的机器一起摘掉。
//
// DSH 早期把这条豁免读成「全局层豁免」而不是「自己那层豁免」，在工具还都挂在宿主
// 装配里时两者等价；等预设把工具搬到 agent 平面上，它们就变成了**祖先**贡献，
// 于是子 agent 的过滤器悄悄地什么也不再约束。这里按订正后的语义实现。
func (r *Runtime) viewOf(key *scope.Key) view {
	layers := r.layers.ChainLayers(key)
	// 这个作用域**自己**那一层：它是唯一一层里的注册属于「自己的」而不是「继承的」，
	// 而且在它还没贡献过任何东西之前根本不存在。
	own, hasOwn := r.layers.Peek(key)

	inherited := map[string]*Definition{}
	var inheritedOrder []string
	appendInherited := func(name string, definition *Definition) {
		if _, seen := inherited[name]; !seen {
			inheritedOrder = append(inheritedOrder, name)
		}
		inherited[name] = definition
	}
	for name, definition := range r.layers.Global().tools.All() {
		appendInherited(name, definition)
	}
	for _, layer := range layers {
		if hasOwn && layer == own {
			continue
		}
		for name, definition := range layer.tools.All() {
			appendInherited(name, definition)
		}
	}

	result := view{
		visible:           map[string]*Definition{},
		knownNames:        map[string]struct{}{},
		restrictableNames: map[string]struct{}{},
	}
	for _, name := range inheritedOrder {
		result.knownNames[name] = struct{}{}
		result.restrictableNames[name] = struct{}{}
		// 限制在整条链上取交集：链上任何一个作用域都可以为它下面的一切摘掉一个继承名。
		admitted := true
		for _, layer := range layers {
			if !layer.admits(name) {
				admitted = false
				break
			}
		}
		if admitted {
			result.visible[name] = inherited[name]
			result.order = append(result.order, name)
		}
	}
	if hasOwn {
		for name, definition := range own.tools.All() {
			result.knownNames[name] = struct{}{}
			if _, shadowed := result.visible[name]; !shadowed {
				result.order = append(result.order, name)
			}
			result.visible[name] = definition
		}
	}
	return result
}

// Get 按一个作用域看到的样子查一个工具。
//
// 源: packages/core/tools/src/index.ts:1205-1207
//
// 作用域注册盖住全局注册；一个被限制摘掉的全局工具在这里读出来就是「没有」。
// 呈现方要把调用它的那个 agent 传进来，卡片才会和真正跑的那份定义对上。
func (r *Runtime) Get(name string, key *scope.Key) (*Definition, bool) {
	definition, ok := r.viewOf(key).visible[name]
	return definition, ok
}

// Schemas 把一个作用域看得见的工具投影成给模型看的 schema。
//
// 源: packages/core/tools/src/index.ts:1226-1228
func (r *Runtime) Schemas(key *scope.Key) []llm.ToolSchema {
	current := r.viewOf(key)
	schemas := make([]llm.ToolSchema, 0, len(current.order))
	for _, name := range current.order {
		schemas = append(schemas, schemaOf(current.visible[name]))
	}
	return schemas
}

// KnownNames 是过滤之前的全部能力名，按字典序。
//
// 源: packages/core/tools/src/index.ts:1012-1014
//
// 用它的是提示词顺序校验：一个被限制摘掉的工具仍然是「认识的名字」，
// 在提示词里点它的名不算写错。
func (r *Runtime) KnownNames(key *scope.Key) []string {
	return sortedKeys(r.viewOf(key).knownNames)
}

// schemaOf 把一份定义投影成模型看得见的那几个字段。
//
// 源: packages/core/tools/src/index.ts:1210-1224
//
// 执行体和呈现钩子一个都不出现在这里：它们是本地的函数值，既不该也没法发给提供方。
func schemaOf(definition *Definition) llm.ToolSchema {
	parameters, err := json.Marshal(definition.Parameters)
	if err != nil {
		// 注册时 AssertObjectSchema 已经验过这份 schema 排得出来，走不到这里。
		parameters = []byte(`{"type":"object"}`)
	}
	return llm.ToolSchema{
		Name:        definition.Name,
		Description: definition.Description,
		Parameters:  parameters,
	}
}

// toSet 把一串名字变成集合。
func toSet(names []string) map[string]struct{} {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}
	return set
}

// sortedKeys 按字典序列出一个集合里的键。
func sortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// quoteList 把一串名字排成便于阅读的诊断文本。
func quoteList(names []string) string {
	if len(names) == 0 {
		return "（一个也没有）"
	}
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = fmt.Sprintf("%q", name)
	}
	return strings.Join(quoted, ", ")
}
