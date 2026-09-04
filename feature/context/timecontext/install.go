// 本文件的作用：把这一层接到 agent 的步骤准入瀑布上——每一步开始之前，
// 在里层已经拟好的那批消息末尾补一条时间读数。
//
// 源: packages/context/time-context/src/index.ts:145-209

package timecontext

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
)

// Agents 是本包要的那张 agent 注册表。
//
// 源: packages/context/time-context/src/index.ts:24 (inject 'agents')
//
// 新增: DSH 从 cordis 容器里按名字取。Go 没有那个容器，所以摆成一个窄接口明着
// 传进来——窄到只剩本包真正用得着的那一条，好让测试能替换它，也好让读的人一眼
// 看见本包到底碰了注册表的哪个面。这一层只往步骤准入那条瀑布上挂一个观察者，
// 别的什么都不碰。
type Agents interface {
	// OnPreStep 把一个观察者挂到步骤准入那条瀑布上。
	OnPreStep(
		ctx context.Context,
		owner *scope.Scope,
		observer agent.PreStepObserver,
	) (func(context.Context) error, error)
}

// Deps 是装这一层要的那几样协作者。
//
// 源: packages/context/time-context/src/index.ts:24 (inject)
//
// 叫 Deps 不叫 Config，是因为 [Config] 这个名字已经归那两个配置项了；
// 成例是 plan/planmode。
type Deps struct {
	// Agents 是那张 agent 注册表，必填。
	Agents Agents
	// Logger 收那些只在本进程里有意义的诊断；不给就用 [slog.Default]。
	Logger *slog.Logger
	// Now 是墙上时钟的取样口；不给就用 [time.Now]。
	//
	// 留成可替换的是为了测试：这一层产出的每一个字节都挂在「此刻几点」上，
	// 不能替的话连「读数里写的就是采样那一刻」这条都断言不了。
	Now func() time.Time
}

// Install 把时间读数这条规则装到步骤准入上，交回把它摘下来的函数。
//
// 源: packages/context/time-context/src/index.ts:145-209
//
// owner 决定这条规则管哪些 agent，规矩和本仓库别处一样：[scope.NewRoot] 造出来的
// 作用域没有身份，落全局层管所有人；有身份的只管那条链下面的。
//
// config 在这里就验掉。DSH 把校验和时区解析写在 `apply` 开头、失败就让插件装不上；
// 这里是同一个性质——[Config.Resolve] 过不去就没有一份可用的配置，而不是留一个
// 「时区错着照样往提示词里写」的运行期。
func Install(
	ctx context.Context,
	owner *scope.Scope,
	config Config,
	deps Deps,
) (func(context.Context) error, error) {
	switch {
	case owner == nil:
		return nil, errors.New("timecontext: 需要一个持有这次登记的作用域")
	case deps.Agents == nil:
		return nil, errors.New("timecontext: 需要一个 agent 注册表")
	}
	resolved, err := config.Resolve()
	if err != nil {
		return nil, err
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}

	rule := &injector{config: resolved, logger: deps.Logger, now: deps.Now}
	remove, err := deps.Agents.OnPreStep(ctx, owner, rule.onPreStep)
	if err != nil {
		return nil, fmt.Errorf("timecontext: 装步骤准入观察者失败：%w", err)
	}
	return remove, nil
}

// injector 是这条规则的全部状态：一份验过的配置、一个日志口、一台时钟。
//
// 新增: DSH 的 `apply` 把这三样做成闭包变量。Go 里聚成一个值，好处是那个观察者
// 可以是一个有名字的方法——一条会在每个步骤上跑的规则值得能被单独指着说。
type injector struct {
	config ResolvedConfig
	logger *slog.Logger
	now    func() time.Time
}

// onPreStep 是挂在瀑布上的那条规则。
//
// 源: packages/context/time-context/src/index.ts:170-208
//
// 先调 next 再动手，次序和 DSH 一样，而且必须是这个次序：读数说的是「准备这一步
// 的那一刻」，而里层那些观察者完全可能把这一步整个否掉。先排出一条读数再发现
// 这步不进，那条读数要么白排、要么更糟——被塞进一个根本没发生的步骤里。
//
// 补在**末尾**同样照搬 DSH：里层交出来的那批消息里有用户刚说的话和运行期上下文，
// 时间读数是给它们做注脚的，排在它们前面等于让模型先读到注脚。
func (i *injector) onPreStep(
	ctx context.Context,
	step agent.PreStep,
	next func(context.Context) (agent.PreStepDecision, error),
) (agent.PreStepDecision, error) {
	decision, err := next(ctx)
	if err != nil {
		return decision, err
	}
	// 零值是拒绝（见 [agent.PreStepDecision]），拒了就没有「这一步」可言。
	// ctx 已经取消时也一样：这一步不会真的跑，补一条读数只会在日志里留下一条
	// 说着「正在准备第 N 步」、而第 N 步压根没开始的假话。
	if !decision.Enter || ctx.Err() != nil {
		return decision, nil
	}
	if step.Agent == nil {
		// 一个没有 agent 的步骤提议读不出日志，也就算不出基线。这在运行时里出不来，
		// 但这条规则挂在一个公开的瀑布上，别人喂什么进来不归本包管。
		return decision, nil
	}

	events := step.Agent.Session().Events()
	now := i.now()

	inject, err := ShouldInject(events, now, i.config.RefreshInterval)
	if err != nil {
		i.warn(step, "算不出该不该注入时间读数", err)
		return decision, nil
	}
	if !inject {
		return decision, nil
	}
	previous, hasPrevious, err := PreviousBaseline(events, step.Turn, step.Step)
	if err != nil {
		i.warn(step, "算不出时间读数的基线时刻", err)
		return decision, nil
	}

	text := RenderText(Reading{
		Now:         now,
		Turn:        step.Turn,
		Step:        step.Step,
		Previous:    previous,
		HasPrevious: hasPrevious,
	}, i.config.Location)

	// 复制一份再追加：里层交出来的那张切片可能还被别人拿着。
	messages := make([]llm.Message, 0, len(decision.Messages)+1)
	messages = append(messages, decision.Messages...)
	decision.Messages = append(messages,
		llm.NewUserMessage(llm.Content{llm.TextBlock{Text: text}}, ReadingSource(text)))
	return decision, nil
}

// warn 记一条「这一步没能注入」并放行。
//
// 新增: DSH 那两处读日志的调用直接抛，于是一条读不回来的历史事件会把整个步骤
// 准入带崩。这里改成记一行然后原样放行，理由是这一层的定位：它是给模型补上下文的，
// 补不上是**降级**，不是故障。让 agent 因为算不出「离上次过了多久」而停摆，
// 代价和收益完全不成比例。
//
// 更要紧的是不能退而求其次去注入一条基线可疑的读数：读数里那句 "Elapsed since…"
// 会被模型当成事实，而一个错的基线正是本包自己那条不变量要拦的东西——
// 宁可这一步没有读数，也不要一条说谎的。
func (i *injector) warn(step agent.PreStep, what string, err error) {
	i.logger.Warn("timecontext: "+what+"，这一步不注入",
		"session", string(step.Agent.ID()), "turn", step.Turn, "step", step.Step, "error", err)
}
