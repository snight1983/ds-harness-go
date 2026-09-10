// 本文件的作用：配置怎么验、计数怎么存怎么推进、到顶那句话怎么说，
// 以及这一层怎么挂到执行前瀑布和每步之前那条瀑布上。
//
// 新增: 整个包在 DSH 里没有对应物，理由见 doc.go。计数那部分的形状照
// feature/guard/repeattoolreminder 走——同一个仓库里两层做同一件事的记账，
// 形状不一样只会让读的人以为其中一个另有玄机。

package repeattoolcap

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"weak"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/tools"
)

// ErrInvalidConfig 表示配置本身不成立，构造被拒。
//
// 新增: 和 repeattoolreminder 同样的取舍——配置错了要响亮地失败，不许静默退回
// 「不限」。一份写着上限的配置表达的是「我要拦」，把它读成不拦，等于让一次配置
// 错误变成一道永远不会响的刹车。
var ErrInvalidConfig = errors.New("repeattoolcap: 配置不成立")

// Config 是这一层的配置。
type Config struct {
	// PerTool 是每个工具各自的连续调用上限，按工具名精确匹配。
	//
	// 没在这张表里的工具一次都不限，理由见包文档。每个上限都必须 >= 1：
	// 上限为 0 的工具一次都调不动，没人会那么要求，所以它一定是笔误。
	//
	// 新增: 这里不支持 `*` 通配，而 repeattoolreminder 的 Include/Exclude 支持。
	// 差别是有理由的：那一层是劝告，多劝一个工具没有代价，所以按模式圈一批很划算；
	// 这一层会拒绝调用，圈错一个就是一次线上故障，所以要求把名字一个一个写出来。
	PerTool map[string]int
}

// chain 是一个 agent 当下那条连续调用：上一次的工具名，和它连着出现的次数。
type chain struct {
	name  string
	count int
}

// Cap 是这一层的状态：验好的上限表，加上每个 agent 各自那条计数。
type Cap struct {
	perTool map[string]int

	mu sync.Mutex
	// chains 是每个 agent 的计数，键是**弱**引用。
	//
	// 新增: 和 repeattoolreminder 同样的理由——一个用完就不再有人引用的 agent
	// 不该因为这张表而留在内存里。键死掉的那些在下一次写入时被扫掉，见 advance。
	chains map[weak.Pointer[scope.Key]]chain
}

// New 验一份配置，造出这一层。
func New(config Config) (*Cap, error) {
	perTool := make(map[string]int, len(config.PerTool))
	for name, limit := range config.PerTool {
		if name == "" {
			return nil, fmt.Errorf("%w: PerTool 里有一个空的工具名", ErrInvalidConfig)
		}
		if limit < 1 {
			return nil, fmt.Errorf("%w: 工具 %q 的上限是 %d，必须 >= 1", ErrInvalidConfig, name, limit)
		}
		perTool[name] = limit
	}
	return &Cap{perTool: perTool, chains: map[weak.Pointer[scope.Key]]chain{}}, nil
}

// Install 把这一层挂到一个工具注册表的执行前瀑布上，返回撤销它的函数。
//
// owner 决定它管哪些 agent：[scope.NewRoot] 造的作用域没有身份，规则落全局层、
// 管所有 agent；有身份的作用域只管那条链下面的。
func (c *Cap) Install(
	ctx context.Context,
	runtime *tools.Runtime,
	owner *scope.Scope,
) (func(context.Context) error, error) {
	if runtime == nil {
		return nil, errors.New("repeattoolcap: 需要一个工具注册表")
	}
	rule := func(exec tools.Execution, next func() (tools.PreDecision, error)) (tools.PreDecision, error) {
		count, limit, capped := c.Observe(exec)
		if !capped {
			return next()
		}
		// 短路：不调 next，后面登记的规则一条都不跑。到顶这件事没有商量余地，
		// 让后面某一条把它改回放行，这道刹车就等于没有。
		return tools.PreDecision{Kind: tools.PreDeny, Reason: denyReason(exec.Name, count, limit)}, nil
	}
	return runtime.PreExecute(ctx, owner, rule)
}

// InstallStepNotice 把 [Cap.NoticeStep] 接到 agent 循环每步之前那条瀑布上，
// 返回撤销它的函数。
//
// 这条观察者只重置状态，不表态：它永远调 next，既不挂上下文也不否步骤。
//
// 只装 [Cap.Install] 也跑得动，代价是用户插话不再清零——那意味着用户明确要求
// 「再查一次」时，模型会撞在一道本该已经松开的闸上。
func (c *Cap) InstallStepNotice(
	ctx context.Context,
	agents *agent.Registry,
	owner *scope.Scope,
) (func(context.Context) error, error) {
	if agents == nil {
		return nil, errors.New("repeattoolcap: 需要一张 agent 注册表")
	}
	observer := func(
		ctx context.Context,
		step agent.PreStep,
		next func(context.Context) (agent.PreStepDecision, error),
	) (agent.PreStepDecision, error) {
		if step.Agent != nil {
			c.NoticeStep(step.Agent.Scope().Key(), step.Messages)
		}
		return next(ctx)
	}
	return agents.OnPreStep(ctx, owner, observer)
}

// Observe 为一次调用推进它那个 agent 的计数，并说明这次要不要拦。
//
// 交出的是这次调用的连续次数、这个工具的上限、以及「到顶了没有」。没配上限的
// 工具和没有身份的调用都不参与计数，直接判成不拦。
//
// 导出它是为了让「什么算到顶」这条规则能被单独测、也能被别的接线方式复用；
// [Cap.Install] 挂上去的那条规则做的就是调它一次。
func (c *Cap) Observe(exec tools.Execution) (count, limit int, capped bool) {
	// 直接调 Runtime.Execute 的调用方没有身份可以作为计数的键，也不在任何一条
	// 循环里——它不是这一层要防的东西。
	if exec.Agent == nil {
		return 0, 0, false
	}
	limit, tracked := c.perTool[exec.Name]
	if !tracked {
		return 0, 0, false
	}
	count = c.advance(exec.Agent, exec.Name)
	return count, limit, count > limit
}

// NoticeStep 是 agent 循环每走一步之前该调的那一下：用户插了话就把计数清零。
//
// 接线由 [Cap.InstallStepNotice] 做，导出它是为了让这条规则能被单独测。
func (c *Cap) NoticeStep(agent *scope.Key, messages []llm.Message) {
	if agent == nil {
		return
	}
	for _, message := range messages {
		if message.Source == nil || message.Source.SourceKind() != llm.SourceUser {
			continue
		}
		c.mu.Lock()
		delete(c.chains, weak.Make(agent))
		c.mu.Unlock()
		return
	}
}

// advance 把一个 agent 的计数推进一次，交出这次调用的连续次数。
//
// 顺手把键已经死掉的那些条目扫掉，理由和 repeattoolreminder 那边一样：表的规模是
// 活着的 agent 数，全表扫比维护一个调不准就会退化成泄漏的阈值便宜。
func (c *Cap) advance(agent *scope.Key, name string) int {
	handle := weak.Make(agent)
	c.mu.Lock()
	defer c.mu.Unlock()
	for existing := range c.chains {
		if existing != handle && existing.Value() == nil {
			delete(c.chains, existing)
		}
	}
	count := 1
	if previous, ok := c.chains[handle]; ok && previous.name == name {
		count = previous.count + 1
	}
	c.chains[handle] = chain{name: name, count: count}
	return count
}

// denyReason 是到顶那次调用给模型看的那句话。
//
// 保持英文，和 repeattoolreminder 的提醒同一个理由：这是发给模型的话，
// 不是给人读的日志。
//
// 三件事一句都不能少：拦了、为什么拦、以及接下来该干什么。只说「不许调」的话，
// 模型最常见的反应是换个参数再来一次——而它换得再多也还是会被拦，于是两边一起空转。
func denyReason(toolName string, count, limit int) string {
	return fmt.Sprintf(
		"Blocked: %s has been called %d times in a row, which is over the limit of %d "+
			"consecutive calls. Calling it again will be blocked as well. "+
			"Work with the results you already have: answer the user, use a different "+
			"tool, or explain what is still missing and why.",
		toolName, count, limit,
	)
}
