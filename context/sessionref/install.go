// 本文件的作用：把这一层挂到 agent 的前置步骤上——一个步骤真要进去之前，
// 先把直接用户消息里的规范提及换成人能读的文本，再把它引用的那几个会话的快照
// 紧跟在它自己后面塞进去。
//
// 源: packages/context/session-reference/src/index.ts:106-148

package sessionref

import (
	"context"
	"fmt"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/llm"
)

// Agents 是本包要的那张 agent 注册表。
//
// 源: packages/context/session-reference/src/index.ts:106（ctx.on）
//
// 新增: DSH 从 cordis 容器里按事件名挂监听器。Go 没有那个容器，所以摆成一个
// 窄接口明着传进来——本包只碰前置步骤这一条边，写在类型上比写在文档里可靠。
// 先例是 context/instructions.Agents。
type Agents interface {
	// OnPreStep 是本层改写那批直接消息的地方。
	OnPreStep(ctx context.Context, owner *scope.Scope, observer agent.PreStepObserver) (func(context.Context) error, error)
}

// Deps 是装这一层要的那两样协作者。
//
// 源: packages/context/session-reference/src/index.ts:85-114
//
// 名字叫 Deps 不叫 Config，是因为 [Config] 已经被本包自己那份配置值占了；
// 先例是 context/instructions.Deps。
type Deps struct {
	// Agents 是那张 agent 注册表，必填。
	Agents Agents

	// Resolver 是干活的那个解析器，必填。
	//
	// 新增: DSH 那边 `SessionReferenceResolver` 一个类既是服务又是安装器，
	// 构造函数末尾顺手把钩子挂上。这里拆成两半：[NewResolver] 造出来的东西
	// 可以单独用（主机的自动补全就只要 [Resolver.MentionCandidates]，
	// 根本不需要 agent），而 [Install] 只负责把它接到前置步骤上。
	Resolver *Resolver
}

// installer 是这一次 [Install] 的全部状态。
//
// 它没有可变字段：本层每一次前置步骤都从收到的那批消息里现算，
// 不跨步骤记任何东西。这一点和 context/instructions 正相反，
// 那边要记住「模型已经看见过哪一份基线」。
type installer struct {
	resolver *Resolver
}

// Install 把跨会话引用这一层装上去，交回把它摘下来的函数。
//
// 源: packages/context/session-reference/src/index.ts:106-113
//
// 装上去之后不会主动读任何会话：这一层全部由前置步骤驱动，而且只在那批消息里
// 真的出现了规范提及时才会去读。
//
// 新增: DSH 那句 `ctx.on(..., { prepend: true })` 把自己插到监听器名单最前面，
// 也就是**最外层**。Go 的瀑布是「先登记的在外层」（见 [agent.PreStepObserver]），
// 表达不出「插队」，所以这件事落到装配方头上：这一层要比别的前置步骤观察者
// **先**装。次序是有后果的——最外层意味着它拿到的是所有人都表过态之后的那批消息，
// 于是别的层临时补进来的提及也会被换掉；装到里层的话，外面那些层看见的
// 就还是没换过的不透明记号。
func Install(ctx context.Context, owner *scope.Scope, deps Deps) (func(context.Context) error, error) {
	switch {
	case owner == nil:
		return nil, fail(CodeInvalidConfig, "会话引用：需要一个持有这次登记的作用域")
	case deps.Agents == nil:
		return nil, fail(CodeInvalidConfig, "会话引用：需要一张 agent 注册表")
	case deps.Resolver == nil:
		return nil, fail(CodeInvalidConfig, "会话引用：需要一个解析器")
	}
	install := &installer{resolver: deps.Resolver}
	undo, err := deps.Agents.OnPreStep(ctx, owner, install.onPreStep)
	if err != nil {
		return nil, wrap(CodeInvalidConfig, err, "会话引用：装前置步骤观察者失败")
	}
	return undo, nil
}

// onPreStep 在一个提议中的步骤上把那批直接消息改写掉。
//
// 源: packages/context/session-reference/src/index.ts:106-113
//
// 先调 next 再动手：这一层要改的是**最终会进去的那批消息**，而里面那几层
// 还可能把它换掉。被否掉的步骤原样交回去——一个不会进去的步骤没有消息要改写，
// 去读几个会话的表面纯属白花 I/O。
func (i *installer) onPreStep(
	ctx context.Context,
	step agent.PreStep,
	next func(context.Context) (agent.PreStepDecision, error),
) (agent.PreStepDecision, error) {
	decision, err := next(ctx)
	if err != nil || !decision.Enter {
		return decision, err
	}
	live := step.Agent
	// 运行时里出不来这种提议，但这条规则挂在一个公开的瀑布上，别人喂什么进来
	// 不归本包管——不能因此空指针。先例逐字同 context/instructions.onPreStep。
	if live == nil || live.Session() == nil {
		return decision, nil
	}
	prepared, err := i.prepareDirect(ctx, live, decision.Messages)
	if err != nil {
		// 否掉而不是放行：这一批消息里带着还没换掉的不透明记号，
		// 放进去等于让模型读一串 base64。一个没进去的步骤会被重新提议，
		// 而一条读错了的提示词是收不回来的。
		return agent.RejectStep(), err
	}
	return agent.EnterStep(prepared), nil
}

// prepareDirect 逐条过一遍那批消息：用户自己说的话里的提及换成可读文本，
// 它引用的那些会话的快照紧跟在它后面。
//
// 源: packages/context/session-reference/src/index.ts:124-148
//
// 「紧跟在后面」而不是全部堆到末尾：一批消息里可能有好几条各自引用了不同的会话，
// 堆到末尾的话，模型得自己猜哪份快照是哪句话要的。
//
// 新增: DSH 用 `Promise.all` 把这些消息并起来一起准备。Go 这边是顺着来一条条做——
// 并发在这里换不到什么：绝大多数步骤里带提及的消息只有一条，而顺序执行让
// 「第几条消息出的错」这件事直接落在调用栈上。
func (i *installer) prepareDirect(
	ctx context.Context,
	live agent.Agent,
	messages []llm.Message,
) ([]llm.Message, error) {
	target := Target{SessionID: live.Session().ID(), Cwd: live.Session().Header().Cwd}
	prepared := make([]llm.Message, 0, len(messages))
	for _, message := range messages {
		// 只碰用户自己说的话。别的层注入进来的东西里出现一段规范 URI，
		// 那多半是它在转述而不是在引用——照着解会让一次转述变成一次真的读。
		if message.Source == nil || message.Source.SourceKind() != llm.SourceUser {
			prepared = append(prepared, message)
			continue
		}
		content, references, err := stripMentions(message.Content)
		if err != nil {
			return nil, err
		}
		if len(references) == 0 {
			// 一条提及都没有时原样交回**原来那条消息**，而不是交回刚拼出来的
			// 那份等价正文：后者是一个新的切片，会让下游那些按身份认消息的地方
			// 白比一轮，也让这一层在什么都没做的时候留下痕迹。
			prepared = append(prepared, message)
			continue
		}
		resolved, err := i.resolver.Prepare(ctx, target, content, references)
		if err != nil {
			return nil, err
		}
		direct := message
		direct.Content = resolved.Content
		prepared = append(prepared, direct)
		// 解析出来的提及规范化之后至少剩一个，所以这里恒为真。
		//
		// 新增: DSH 在这里 throw，因为它那个 `additionalContext` 是可选字段，
		// 类型上证不出「有提及就一定有上下文」。Go 这边同样证不出，但退路不同：
		// 少一条上下文只是模型少看见一份材料，而把整个步骤否掉是把一个本来
		// 好好的回合掐死。宁可少发。
		if resolved.HasContext {
			prepared = append(prepared, resolved.AdditionalContext)
		}
	}
	return prepared, nil
}

// stripMentions 把一条消息正文里的规范提及换成可读的 `@标签`，并按出现顺序
// 交出它们指向的那些会话。
//
// 源: packages/context/session-reference/src/index.ts:132-137
//
// 非文本块原样留着：一张图片里不会有提及，而重排内容块会改变模型读到的次序。
func stripMentions(content llm.Content) (llm.Content, []Input, error) {
	rewritten := make(llm.Content, 0, len(content))
	var references []Input
	for _, block := range content {
		text, isText := block.(llm.TextBlock)
		if !isText {
			rewritten = append(rewritten, block)
			continue
		}
		parsed, err := ParseText(text.Text)
		if err != nil {
			return nil, nil, fmt.Errorf("会话引用：这条消息里的提及解不开：%w", err)
		}
		references = append(references, parsed.References...)
		rewritten = append(rewritten, llm.TextBlock{Text: parsed.Text})
	}
	return rewritten, references, nil
}
