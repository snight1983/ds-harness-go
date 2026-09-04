// 本文件的作用：验那个默认压缩后端的三条入口——按压力压、超窗补救、人工压一次
// ——各自在什么时候动手、什么时候明确地不动手，以及失败怎么分类。

package basic

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/feature/compaction"
	coresession "github.com/snight1983/ds-harness-go/harness/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// # 这些测试防的是什么错
//
//   - 一段还没发过带路由请求的会话被当成「压力算不出来」的错误。它是一段刚开头的
//     会话的正常样子，报错的话每个步骤边界上都会吵一次。
//   - 压力那一路把「一次压缩还开着」和「窗口没配好」的报错次序调过来。开着的那把
//     锁才是这一刻真正拦住开工的事，配置问题下一个边界上照样报得出来。
//   - 超窗那一路照着平常的尾巴去留。提供方已经确认装不下了，留尾巴只会挑出更短
//     的一段、缩得更少，于是下一次请求接着超。
//   - 压完仍然在线上却安静地放过去。那意味着表面压缩这个手段已经不管用了，而
//     后果要到下一次请求被提供方拒掉才看得见，那时候已经查不到是这里没压下来。
//   - 剪枝之后已经降到线下，却还是发了一次总结调用。那是白花的一次请求。
//   - 人工那一路把「占不到空闲期」和「占到了但是做砸了」混成同一种失败。前者要
//     调用方稍后再来，后者要调用方看诊断。
//   - 人工那一路把「这次请求被取消了」裹成一个分了类的人工失败。调用方要的是
//     「是我取消的」这个事实。

// pressureMeter 是一台把「总量」排成脚本的假计量器。
//
// 它在 [meteredSurface] 上补出 [PressureMeter] 那一个方法：表面定价照旧现算，
// 总量按 totals 依次交出——压力那一路会在一轮里问好几次总量（进门一次、剪枝
// 之后一次、每压完一次再一次），把它排成脚本才说得清「第几次读到多少」。
type pressureMeter struct {
	*meteredSurface
	// totals 是每次问总量依次交出的读数；问超了就一直用最后那一个。
	totals []int
	// totalErrAt 是第几次问总量时失败，从 1 数起；0 表示一直不失败。
	totalErrAt int
	// totalErr 是那一次失败交回的错。
	totalErr error
	// totalCalls 是已经问过几次。
	totalCalls int
}

func (m *pressureMeter) TotalTokens(*coresession.Session) (int, error) {
	m.totalCalls++
	if m.totalErrAt == m.totalCalls {
		return 0, m.totalErr
	}
	index := m.totalCalls - 1
	if index >= len(m.totals) {
		index = len(m.totals) - 1
	}
	if index < 0 {
		return 0, nil
	}
	return m.totals[index], nil
}

// fixedModels 是一台照本宣科的假模型目录，顺便记下问过哪几条路由。
type fixedModels struct {
	info llm.ResolvedModelInfo
	err  error
	seen []string
}

func (m *fixedModels) ResolveModelInfo(_ context.Context, provider, model string,
) (llm.ResolvedModelInfo, error) {
	m.seen = append(m.seen, provider+"/"+model)
	return m.info, m.err
}

// shortPressureMeter 是一台只给表面头一个节点定价的假计量器。
//
// 定价清单和当前表面对不上是一条真的错，用它逼出挑区间那一步的失败。
type shortPressureMeter struct{ total int }

func (shortPressureMeter) PriceSurface(*coresession.Session) ([]PricedNode, error) {
	return []PricedNode{{Seq: 0, Tokens: 1}}, nil
}
func (shortPressureMeter) EstimateMessage(llm.Message) (int, error) { return 1, nil }

func (m shortPressureMeter) TotalTokens(*coresession.Session) (int, error) { return m.total, nil }

// brokenHeaderSession 排一段「最后那条请求头读不回来」的会话。
//
// 路由是从落库的请求信封上折出来的，所以这条坏事件让每一处读路由的地方都失败。
func brokenHeaderSession(t *testing.T, id string) *coresession.Session {
	t.Helper()

	live := liveSession(t, id, llm.CallConfig{Provider: "openai", Model: "gpt-x"})
	appendTo(t, live, sessionlog.Event{
		Type: sessionlog.EventRequestHeader,
		Data: json.RawMessage("[1,2]"),
	})
	return live
}

// modelsAt 排一台「这个模型的窗口就这么大」的假目录。
func modelsAt(window int) *fixedModels {
	return &fixedModels{info: llm.ResolvedModelInfo{
		Context: &llm.ModelContext{ContextWindow: window},
	}}
}

// idleMaintainer 是一台假的空闲期出借方。
type idleMaintainer struct {
	// busy 非 nil 时借不出来。
	busy error
	// revoke 为真时借出去的那段 ctx 一进门就已经取消了。
	revoke bool
	// before 在把空闲期交出去之前跑一遍，用来模拟「这次请求自己被取消了」。
	before func()
}

func (m idleMaintainer) RunMaintenance(ctx context.Context, task func(context.Context) error) error {
	if m.busy != nil {
		return m.busy
	}
	if m.before != nil {
		m.before()
	}
	if !m.revoke {
		// 这里不去另派生一条可取消的 ctx：派生出来的那条必须在 RunMaintenance
		// 返回**之前**收掉，而引擎正是在那之后才去看它，于是每一趟都会被读成
		// 「空闲期被收回了」。
		return task(ctx)
	}
	claimed, stop := context.WithCancel(ctx)
	stop()
	return task(claimed)
}

// engineConfig 解一份「压力线占一半、尾巴 50 个 token」的配置。
//
// 配合 [modelsAt] 的 1000 号窗口，压力线正好是 500——用例里那些总量读数都是
// 照着这个数排的。
func engineConfig(t *testing.T, tweak func(*Config)) ResolvedConfig {
	t.Helper()

	config := Config{PolicyConfig: PolicyConfig{
		ThresholdRatio: 0.5,
		RetainTokens:   intOf(50),
		MaxTokens:      512,
	}}
	if tweak != nil {
		tweak(&config)
	}
	resolved, err := config.Resolve()
	if err != nil {
		t.Fatalf("配置解不出来：%v", err)
	}
	return resolved
}

// engineDeps 排一套「表面每个节点 100、裹好的摘要 10」的引擎依赖。
func engineDeps(totals ...int) EngineDeps {
	return EngineDeps{
		Meter: &pressureMeter{
			meteredSurface: &meteredSurface{fallback: 100, estimate: 10},
			totals:         totals,
		},
		Models:    modelsAt(1000),
		Summarize: staticSummary("一份摘要"),
		NewID:     func() string { return "c-fixed" },
	}
}

// newTestEngine 造一个后端，配置和依赖都验过。
func newTestEngine(t *testing.T, config ResolvedConfig, deps EngineDeps) *Engine {
	t.Helper()

	engine, err := NewEngine(config, deps)
	if err != nil {
		t.Fatalf("造后端失败：%v", err)
	}
	return engine
}

// routedLog 排一段「带路由、一个开着的回合、四条表面消息」的会话。
func routedLog(t *testing.T) logSession {
	t.Helper()

	return openLog(t, llm.CallConfig{Provider: "openai", Model: "gpt-x"})
}

func TestNewEngine少了必填的那两样就拒(t *testing.T) {
	t.Parallel()

	config := engineConfig(t, nil)
	if _, err := NewEngine(config, EngineDeps{Summarize: staticSummary("x")}); err == nil {
		t.Fatal("没有计量器却造出来了")
	}
	if _, err := NewEngine(config, EngineDeps{Meter: &pressureMeter{
		meteredSurface: &meteredSurface{},
	}}); err == nil {
		t.Fatal("总结和流都没有却造出来了")
	}
}

func TestEngineConfig原样交回验过的那一份(t *testing.T) {
	t.Parallel()

	config := engineConfig(t, nil)
	engine := newTestEngine(t, config, engineDeps())
	if engine.Config().ThresholdRatio != config.ThresholdRatio {
		t.Fatalf("交回来的是 %+v", engine.Config())
	}
}

func TestCompactIfNeeded没有路由就什么都不做(t *testing.T) {
	t.Parallel()

	// 一段还没发过带路由请求的会话没有窗口，也就折算不出压力线。这不是错误，
	// 报错的话每个步骤边界上都会吵一次。
	for name, headers := range map[string][]llm.CallConfig{
		"一条请求头都没有": nil,
		"路由只有一半":   {{Provider: "openai"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			live := liveSession(t, "s-noroute", headers...)
			engine := newTestEngine(t, engineConfig(t, nil), engineDeps(9999))
			result, ok, err := engine.CompactIfNeeded(t.Context(),
				agentAt(live, "openai", "gpt-x"), compaction.TriggerPressure)
			if err != nil || ok {
				t.Fatalf("动手了：%+v / %v / %v", result, ok, err)
			}
		})
	}
}

func TestCompactIfNeeded不认识的触发原因是错(t *testing.T) {
	t.Parallel()

	// DSH 那边是 assertNever，靠封闭联合在编译期挡掉。Go 的具名字符串类型是开的，
	// 一个手写的触发原因进得来，所以这里必须是一条真的运行期失败。
	log := routedLog(t)
	engine := newTestEngine(t, engineConfig(t, nil), engineDeps(100))

	_, ok, err := engine.CompactIfNeeded(t.Context(),
		agentAt(log.live, "openai", "gpt-x"), compaction.Trigger("随手写的"))
	if err == nil || ok {
		t.Fatalf("放过去了：%v / %v", ok, err)
	}
	if !strings.Contains(err.Error(), "随手写的") {
		t.Fatalf("诊断没说是哪个触发原因：%v", err)
	}
}

func TestCompactIfNeeded请求头读不回来就原样交回(t *testing.T) {
	t.Parallel()

	// 路由是从落库的请求信封上折出来的。折不出来时不能当成「这段会话还没有路由」
	// ——那会把一段读坏了的日志静默地当成一段刚开头的会话。
	live := brokenHeaderSession(t, "s-broken")
	engine := newTestEngine(t, engineConfig(t, nil), engineDeps(600))

	if _, ok, err := engine.CompactIfNeeded(t.Context(),
		agentAt(live, "openai", "gpt-x"), compaction.TriggerPressure); err == nil || ok {
		t.Fatalf("放过去了：%v / %v", ok, err)
	}
}

func TestCompactPressure没到线就不动手(t *testing.T) {
	t.Parallel()

	log := routedLog(t)
	before := log.live.Seq()
	engine := newTestEngine(t, engineConfig(t, nil), engineDeps(499))

	_, ok, err := engine.CompactIfNeeded(t.Context(),
		agentAt(log.live, "openai", "gpt-x"), compaction.TriggerPressure)
	if err != nil || ok {
		t.Fatalf("动手了：%v / %v", ok, err)
	}
	if log.live.Seq() != before {
		t.Fatalf("日志从 %d 长到了 %d", before, log.live.Seq())
	}
}

func TestCompactPressure到线了就压一次(t *testing.T) {
	t.Parallel()

	log := routedLog(t)
	engine := newTestEngine(t, engineConfig(t, nil), engineDeps(600, 100))

	result, ok, err := engine.CompactIfNeeded(t.Context(),
		agentAt(log.live, "openai", "gpt-x"), compaction.TriggerPressure)
	if err != nil || !ok {
		t.Fatalf("没压：%v / %v", ok, err)
	}
	if len(result.ShadowedSeqs) == 0 {
		t.Fatalf("一个节点都没遮：%+v", result)
	}
	if count, _ := eventsByType(log.live.Events(), compaction.EventCompactionEnd); count != 1 {
		t.Fatalf("合上的括号有 %d 对", count)
	}
}

func TestCompactPressure模型目录问不出来就原样交回(t *testing.T) {
	t.Parallel()

	boom := errors.New("目录炸了")
	log := routedLog(t)
	deps := engineDeps(600)
	deps.Models = &fixedModels{err: boom}
	engine := newTestEngine(t, engineConfig(t, nil), deps)

	if _, _, err := engine.CompactIfNeeded(t.Context(),
		agentAt(log.live, "openai", "gpt-x"), compaction.TriggerPressure); !errors.Is(err, boom) {
		t.Fatalf("报的是 %v", err)
	}
}

func TestCompactPressure按落库的那条路由去问窗口(t *testing.T) {
	t.Parallel()

	// 压力要按**模型真的收到了什么**来判断，所以路由取的是最近一次落库的请求
	// 信封，不是 agent 选项上那个模型名。
	log := routedLog(t)
	deps := engineDeps(499)
	models := modelsAt(1000)
	deps.Models = models
	engine := newTestEngine(t, engineConfig(t, nil), deps)

	if _, _, err := engine.CompactIfNeeded(t.Context(),
		agentAt(log.live, "别的家", "别的模型"), compaction.TriggerPressure); err != nil {
		t.Fatalf("这一趟该成：%v", err)
	}
	if len(models.seen) != 1 || models.seen[0] != "openai/gpt-x" {
		t.Fatalf("问的是 %v", models.seen)
	}
}

func TestCompactPressure开着的压缩比配错的窗口先报出来(t *testing.T) {
	t.Parallel()

	// 一次压缩还开着的时候，就算窗口也没配好，先报出来的也该是那把没放的锁——
	// 它才是这一刻真正拦住开工的那件事。
	log := routedLog(t)
	appendTo(t, log.live, sessionlog.Event{
		Type: compaction.EventCompactionStart,
		Data: marshalPayload(t, compaction.StartData{CompactionID: "c-old", Turn: 1}),
	})
	deps := engineDeps(600)
	deps.Models = &fixedModels{}
	engine := newTestEngine(t, engineConfig(t, nil), deps)

	_, _, err := engine.CompactIfNeeded(t.Context(),
		agentAt(log.live, "openai", "gpt-x"), compaction.TriggerPressure)
	if err == nil {
		t.Fatal("放过去了")
	}
	var pressure *TargetPressureError
	if errors.As(err, &pressure) {
		t.Fatalf("先报的是配置问题：%v", err)
	}
}

func TestCompactPressure没有窗口是一条按路由去重的配置错(t *testing.T) {
	t.Parallel()

	log := routedLog(t)
	deps := engineDeps(600)
	deps.Models = &fixedModels{}
	engine := newTestEngine(t, engineConfig(t, nil), deps)

	_, _, err := engine.CompactIfNeeded(t.Context(),
		agentAt(log.live, "openai", "gpt-x"), compaction.TriggerPressure)
	var pressure *TargetPressureError
	if !errors.As(err, &pressure) || pressure.TargetKey != "openai/gpt-x" {
		t.Fatalf("报的是 %v", err)
	}
}

func TestCompactPressure保留比压力线还多也是配置错(t *testing.T) {
	t.Parallel()

	// 留的比压力线还多意味着压完一次仍然在线上，下一步又会去压——一个每步都做
	// 一次总结调用、却永远降不到线下的循环。
	log := routedLog(t)
	config := engineConfig(t, func(c *Config) { c.RetainTokens = intOf(900) })
	engine := newTestEngine(t, config, engineDeps(600))

	_, _, err := engine.CompactIfNeeded(t.Context(),
		agentAt(log.live, "openai", "gpt-x"), compaction.TriggerPressure)
	var pressure *TargetPressureError
	if !errors.As(err, &pressure) {
		t.Fatalf("报的是 %v", err)
	}
}

func TestCompactPressure剪枝之后降到线下就省掉总结(t *testing.T) {
	t.Parallel()

	// 剪枝不花钱也不过模型，先把它落地。光靠它就降到线下的话，这一步就省掉了
	// 一次总结调用。
	log := routedLog(t)
	pruned := 0
	deps := engineDeps(600, 100)
	deps.Prune = func(*coresession.Session) error { pruned++; return nil }
	deps.Summarize = failingSummary(errors.New("这一趟不该发总结"))
	engine := newTestEngine(t, engineConfig(t, nil), deps)

	_, ok, err := engine.CompactIfNeeded(t.Context(),
		agentAt(log.live, "openai", "gpt-x"), compaction.TriggerPressure)
	if err != nil || ok {
		t.Fatalf("还是压了：%v / %v", ok, err)
	}
	if pruned != 1 {
		t.Fatalf("剪枝跑了 %d 遍", pruned)
	}
}

func TestCompactPressure剪枝失败就停下(t *testing.T) {
	t.Parallel()

	boom := errors.New("剪枝炸了")
	log := routedLog(t)
	deps := engineDeps(600)
	deps.Prune = func(*coresession.Session) error { return boom }
	engine := newTestEngine(t, engineConfig(t, nil), deps)

	if _, _, err := engine.CompactIfNeeded(t.Context(),
		agentAt(log.live, "openai", "gpt-x"), compaction.TriggerPressure); !errors.Is(err, boom) {
		t.Fatalf("报的是 %v", err)
	}
}

func TestCompactPressure总量算不出来就停下(t *testing.T) {
	t.Parallel()

	// 进门那一次和剪枝之后那一次分开验：两处都要把失败原样交上去，
	// 否则一台坏了的计量器会被读成「没到线」。
	boom := errors.New("计量器炸了")
	for name, at := range map[string]int{"进门那一次": 1, "剪枝之后那一次": 2} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			log := routedLog(t)
			deps := engineDeps(600, 600)
			deps.Meter.(*pressureMeter).totalErrAt = at
			deps.Meter.(*pressureMeter).totalErr = boom
			deps.Prune = func(*coresession.Session) error { return nil }
			engine := newTestEngine(t, engineConfig(t, nil), deps)

			if _, _, err := engine.CompactIfNeeded(t.Context(),
				agentAt(log.live, "openai", "gpt-x"),
				compaction.TriggerPressure); !errors.Is(err, boom) {
				t.Fatalf("报的是 %v", err)
			}
		})
	}
}

func TestCompactPressure压完还在线上就再压一次(t *testing.T) {
	t.Parallel()

	log := routedLog(t)
	config := engineConfig(t, func(c *Config) { c.CompactionRetries = intOf(1) })
	engine := newTestEngine(t, config, engineDeps(600, 600, 100))

	if _, ok, err := engine.CompactIfNeeded(t.Context(),
		agentAt(log.live, "openai", "gpt-x"), compaction.TriggerPressure); err != nil || !ok {
		t.Fatalf("没压下来：%v / %v", ok, err)
	}
	if count, _ := eventsByType(log.live.Events(), compaction.EventCompactionEnd); count != 2 {
		t.Fatalf("合上的括号有 %d 对", count)
	}
}

func TestCompactPressure次数用完还在线上就报出来(t *testing.T) {
	t.Parallel()

	// 安静地放过去的后果是下一次请求被提供方拒掉，那时候已经查不到是这里没压下来。
	log := routedLog(t)
	config := engineConfig(t, func(c *Config) { c.CompactionRetries = intOf(0) })
	engine := newTestEngine(t, config, engineDeps(600, 600))

	_, ok, err := engine.CompactIfNeeded(t.Context(),
		agentAt(log.live, "openai", "gpt-x"), compaction.TriggerPressure)
	if err == nil || ok {
		t.Fatalf("放过去了：%v / %v", ok, err)
	}
	if !strings.Contains(err.Error(), "压力线") {
		t.Fatalf("诊断是 %v", err)
	}
}

func TestCompactPressure定价失败就停下(t *testing.T) {
	t.Parallel()

	boom := errors.New("定价炸了")
	log := routedLog(t)
	deps := engineDeps(600)
	deps.Meter.(*pressureMeter).priceErr = boom
	engine := newTestEngine(t, engineConfig(t, nil), deps)

	if _, _, err := engine.CompactIfNeeded(t.Context(),
		agentAt(log.live, "openai", "gpt-x"), compaction.TriggerPressure); !errors.Is(err, boom) {
		t.Fatalf("报的是 %v", err)
	}
}

func TestCompactPressure挑区间算不下去就停下(t *testing.T) {
	t.Parallel()

	log := routedLog(t)
	deps := engineDeps()
	deps.Meter = shortPressureMeter{total: 600}
	engine := newTestEngine(t, engineConfig(t, nil), deps)

	_, _, err := engine.CompactIfNeeded(t.Context(),
		agentAt(log.live, "openai", "gpt-x"), compaction.TriggerPressure)
	if !errors.Is(err, compaction.ErrSurfaceCorrupt) {
		t.Fatalf("报的是 %v", err)
	}
}

func TestCompactPressure压完之后的总量算不出来也停下(t *testing.T) {
	t.Parallel()

	boom := errors.New("计量器炸了")
	log := routedLog(t)
	deps := engineDeps(600)
	deps.Meter.(*pressureMeter).totalErrAt = 2
	deps.Meter.(*pressureMeter).totalErr = boom
	engine := newTestEngine(t, engineConfig(t, nil), deps)

	if _, _, err := engine.CompactIfNeeded(t.Context(),
		agentAt(log.live, "openai", "gpt-x"), compaction.TriggerPressure); !errors.Is(err, boom) {
		t.Fatalf("报的是 %v", err)
	}
}

func TestCompactPressure挑不出区间就什么都不做(t *testing.T) {
	t.Parallel()

	// 表面上只剩一个节点时，尾巴至少要留下它，于是一段都挑不出来。这不是错误。
	live := liveSession(t, "s-thin", llm.CallConfig{Provider: "openai", Model: "gpt-x"})
	appendTo(t, live, sessionlog.Event{
		Type: sessionlog.EventTurnStart,
		Data: marshalPayload(t, sessionlog.TurnStartData{Turn: 1}),
	})
	appendTo(t, live, surfaceMessage(t, sessionlog.EventUserMessage, sessionlog.UserMessageData{
		Message: llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "就一句"}}, llm.UserSource{}),
	}))
	before := live.Seq()
	engine := newTestEngine(t, engineConfig(t, nil), engineDeps(600))

	_, ok, err := engine.CompactIfNeeded(t.Context(),
		agentAt(live, "openai", "gpt-x"), compaction.TriggerPressure)
	if err != nil || ok {
		t.Fatalf("动手了：%v / %v", ok, err)
	}
	if live.Seq() != before {
		t.Fatalf("日志从 %d 长到了 %d", before, live.Seq())
	}
}

func TestCompactOverflow不看压力线也不留尾巴(t *testing.T) {
	t.Parallel()

	// 提供方已经确认这一份装不下了，按平常的尾巴去留只会挑出更短的一段、缩得
	// 更少，于是下一次请求接着超。压力线同理：这时候问「到线了没」没有意义。
	log := routedLog(t)
	deps := engineDeps()
	deps.Models = &fixedModels{err: errors.New("超窗这一路根本不该问目录")}
	engine := newTestEngine(t, engineConfig(t, nil), deps)

	result, ok, err := engine.CompactIfNeeded(t.Context(),
		agentAt(log.live, "openai", "gpt-x"), compaction.TriggerContextOverflow)
	if err != nil || !ok {
		t.Fatalf("没压：%v / %v", ok, err)
	}
	// 尾巴传 0 之后仍然至少留下最后一个节点，所以这一刀不会把表面清空。
	if len(result.ShadowedSeqs) != len(log.seqs)-1 {
		t.Fatalf("遮住了 %d 个节点", len(result.ShadowedSeqs))
	}
	if len(log.live.SurfaceNodes()) == 0 {
		t.Fatal("表面被清空了")
	}
}

func TestCompactOverflow剪枝和定价的失败都停下(t *testing.T) {
	t.Parallel()

	boom := errors.New("炸了")
	t.Run("剪枝", func(t *testing.T) {
		t.Parallel()

		log := routedLog(t)
		deps := engineDeps()
		deps.Prune = func(*coresession.Session) error { return boom }
		engine := newTestEngine(t, engineConfig(t, nil), deps)

		if _, _, err := engine.CompactIfNeeded(t.Context(),
			agentAt(log.live, "openai", "gpt-x"),
			compaction.TriggerContextOverflow); !errors.Is(err, boom) {
			t.Fatalf("报的是 %v", err)
		}
	})
	t.Run("定价", func(t *testing.T) {
		t.Parallel()

		log := routedLog(t)
		deps := engineDeps()
		deps.Meter.(*pressureMeter).priceErr = boom
		engine := newTestEngine(t, engineConfig(t, nil), deps)

		if _, _, err := engine.CompactIfNeeded(t.Context(),
			agentAt(log.live, "openai", "gpt-x"),
			compaction.TriggerContextOverflow); !errors.Is(err, boom) {
			t.Fatalf("报的是 %v", err)
		}
	})
}

func TestCompactOverflow挑不出区间就什么都不做(t *testing.T) {
	t.Parallel()

	live := liveSession(t, "s-empty", llm.CallConfig{Provider: "openai", Model: "gpt-x"})
	appendTo(t, live, sessionlog.Event{
		Type: sessionlog.EventTurnStart,
		Data: marshalPayload(t, sessionlog.TurnStartData{Turn: 1}),
	})
	before := live.Seq()
	engine := newTestEngine(t, engineConfig(t, nil), engineDeps())

	_, ok, err := engine.CompactIfNeeded(t.Context(),
		agentAt(live, "openai", "gpt-x"), compaction.TriggerContextOverflow)
	if err != nil || ok {
		t.Fatalf("动手了：%v / %v", ok, err)
	}
	if live.Seq() != before {
		t.Fatalf("日志从 %d 长到了 %d", before, live.Seq())
	}
}

func TestCompactRegion按给的两头压(t *testing.T) {
	t.Parallel()

	log := routedLog(t)
	engine := newTestEngine(t, engineConfig(t, nil), engineDeps())

	result, err := engine.CompactRegion(t.Context(), log.seqs[0], log.seqs[1],
		agentAt(log.live, "openai", "gpt-x"))
	if err != nil {
		t.Fatalf("这一趟该成：%v", err)
	}
	want := compaction.ShadowedRange{Start: log.seqs[0], End: log.seqs[1]}
	if result.ShadowedRange != want {
		t.Fatalf("压的是 %+v", result.ShadowedRange)
	}
}

func TestCompactNow占到空闲期就压一次并落检查点(t *testing.T) {
	t.Parallel()

	log := routedLog(t)
	closeTurn(t, log.live)
	flushed := 0
	deps := engineDeps()
	deps.Flush = func(context.Context, *coresession.Session) error { flushed++; return nil }
	engine := newTestEngine(t, engineConfig(t, nil), deps)

	result, ok, err := engine.CompactNow(t.Context(), compaction.ManualAgentContext{
		AgentContext: agentAt(log.live, "openai", "gpt-x"),
		Maintainer:   idleMaintainer{},
	}, "cmd-1")
	if err != nil || !ok {
		t.Fatalf("没压：%v / %v", ok, err)
	}
	if len(result.ShadowedSeqs) == 0 {
		t.Fatalf("一个节点都没遮：%+v", result)
	}
	if flushed != 1 {
		t.Fatalf("持久化检查点跑了 %d 遍", flushed)
	}
}

func TestCompactNow进门就已经取消了(t *testing.T) {
	t.Parallel()

	log := routedLog(t)
	closeTurn(t, log.live)
	engine := newTestEngine(t, engineConfig(t, nil), engineDeps())
	ctx, stop := context.WithCancel(t.Context())
	stop()

	if _, _, err := engine.CompactNow(ctx, compaction.ManualAgentContext{
		AgentContext: agentAt(log.live, "openai", "gpt-x"),
		Maintainer:   idleMaintainer{busy: errors.New("这一趟根本不该去占")},
	}, "cmd-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("报的是 %v", err)
	}
}

func TestCompactNow占不到空闲期就是占着(t *testing.T) {
	t.Parallel()

	// 「占不到」和「占到了但是做砸了」要分开：前者要调用方稍后再来，
	// 后者要调用方看诊断。
	log := routedLog(t)
	closeTurn(t, log.live)
	engine := newTestEngine(t, engineConfig(t, nil), engineDeps())

	_, _, err := engine.CompactNow(t.Context(), compaction.ManualAgentContext{
		AgentContext: agentAt(log.live, "openai", "gpt-x"),
		Maintainer:   idleMaintainer{busy: errors.New("已经有回合在驱动")},
	}, "cmd-1")
	var manual *compaction.ManualError
	if !errors.As(err, &manual) || manual.Code != compaction.ManualErrorBusy {
		t.Fatalf("报的是 %v", err)
	}
}

func TestCompactNow空闲期被收回就是取消(t *testing.T) {
	t.Parallel()

	log := routedLog(t)
	closeTurn(t, log.live)
	engine := newTestEngine(t, engineConfig(t, nil), engineDeps())

	_, _, err := engine.CompactNow(t.Context(), compaction.ManualAgentContext{
		AgentContext: agentAt(log.live, "openai", "gpt-x"),
		Maintainer:   idleMaintainer{revoke: true},
	}, "cmd-1")
	var manual *compaction.ManualError
	if !errors.As(err, &manual) || manual.Code != compaction.ManualErrorCancelled {
		t.Fatalf("报的是 %v", err)
	}
}

func TestCompactNow这次请求被取消就原样交回取消(t *testing.T) {
	t.Parallel()

	// 调用方要的是「是我取消的」这个事实，不是一个分了类的人工失败。
	log := routedLog(t)
	closeTurn(t, log.live)
	engine := newTestEngine(t, engineConfig(t, nil), engineDeps())
	ctx, stop := context.WithCancel(t.Context())

	_, _, err := engine.CompactNow(ctx, compaction.ManualAgentContext{
		AgentContext: agentAt(log.live, "openai", "gpt-x"),
		Maintainer:   idleMaintainer{revoke: true, before: stop},
	}, "cmd-1")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("报的是 %v", err)
	}
	var manual *compaction.ManualError
	if errors.As(err, &manual) {
		t.Fatalf("被裹成了人工失败：%v", err)
	}
}

func TestCompactNow做砸了就原样交回那条失败(t *testing.T) {
	t.Parallel()

	// 定价那一步和总结那一步分开验：前者在开括号之前、交回的就是原件，后者在
	// 括号里面、事务那一层已经给它分好了类。两条路上 [Engine.CompactNow] 都
	// **不再自己加一层**——它只在占不到空闲期和空闲期被收回这两处才分类，
	// 拿这两个码去盖掉下面那条分类是在报错人。
	boom := errors.New("炸了")
	for name, want := range map[string]struct {
		tweak func(*EngineDeps)
		code  compaction.ManualErrorCode
	}{
		"定价失败": {func(d *EngineDeps) { d.Meter.(*pressureMeter).priceErr = boom }, ""},
		"总结失败": {func(d *EngineDeps) { d.Summarize = failingSummary(boom) },
			compaction.ManualErrorSummary},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			log := routedLog(t)
			closeTurn(t, log.live)
			deps := engineDeps()
			want.tweak(&deps)
			engine := newTestEngine(t, engineConfig(t, nil), deps)

			_, _, err := engine.CompactNow(t.Context(), compaction.ManualAgentContext{
				AgentContext: agentAt(log.live, "openai", "gpt-x"),
				Maintainer:   idleMaintainer{},
			}, "cmd-1")
			if !errors.Is(err, boom) {
				t.Fatalf("报的是 %v", err)
			}
			var manual *compaction.ManualError
			if !errors.As(err, &manual) {
				if want.code != "" {
					t.Fatalf("分类丢了：%v", err)
				}
				return
			}
			if manual.Code != want.code {
				t.Fatalf("分类是 %q", manual.Code)
			}
		})
	}
}

func TestCompactNow挑不出区间就什么都不做(t *testing.T) {
	t.Parallel()

	live := liveSession(t, "s-manual-empty", llm.CallConfig{Provider: "openai", Model: "gpt-x"})
	before := live.Seq()
	engine := newTestEngine(t, engineConfig(t, nil), engineDeps())

	_, ok, err := engine.CompactNow(t.Context(), compaction.ManualAgentContext{
		AgentContext: agentAt(live, "openai", "gpt-x"),
		Maintainer:   idleMaintainer{},
	}, "cmd-1")
	if err != nil || ok {
		t.Fatalf("动手了：%v / %v", ok, err)
	}
	if live.Seq() != before {
		t.Fatalf("日志从 %d 长到了 %d", before, live.Seq())
	}
}

func TestEngine摘要按对话此刻的路由挑覆盖(t *testing.T) {
	t.Parallel()

	// 一份按模型配的策略常常连摘要用哪个模型一起配了，而那条覆盖是挂在**对话
	// 模型**上的，不是挂在摘要模型上的。
	log := routedLog(t)
	config := engineConfig(t, func(c *Config) {
		c.ModelPolicies = []ModelPolicyConfig{{
			Target:       Target{Provider: "openai", Model: "gpt-x"},
			PolicyConfig: PolicyConfig{MaxTokens: 111},
		}}
	})
	stream := textStream("一份摘要")
	deps := engineDeps()
	deps.Summarize = nil
	deps.Stream = stream
	engine := newTestEngine(t, config, deps)

	if _, err := engine.CompactRegion(t.Context(), log.seqs[0], log.seqs[1],
		agentAt(log.live, "openai", "gpt-x")); err != nil {
		t.Fatalf("这一趟该成：%v", err)
	}
	if stream.seen.MaxTokens != 111 {
		t.Fatalf("总结上限用成了 %d", stream.seen.MaxTokens)
	}
}

func TestEngine没发过请求就用agent自己那两个选项(t *testing.T) {
	t.Parallel()

	// 还没发过请求的会话没有信封，这时候用 agent 选项挑覆盖。它只影响挑哪条覆盖，
	// 不影响压力判定。
	log := openLog(t)
	config := engineConfig(t, func(c *Config) {
		c.ModelPolicies = []ModelPolicyConfig{{
			Target:       Target{Provider: "本地", Model: "小模型"},
			PolicyConfig: PolicyConfig{MaxTokens: 222},
		}}
	})
	stream := textStream("一份摘要")
	deps := engineDeps()
	deps.Summarize = nil
	deps.Stream = stream
	engine := newTestEngine(t, config, deps)

	if _, err := engine.CompactRegion(t.Context(), log.seqs[0], log.seqs[1],
		agentAt(log.live, "本地", "小模型")); err != nil {
		t.Fatalf("这一趟该成：%v", err)
	}
	if stream.seen.MaxTokens != 222 {
		t.Fatalf("总结上限用成了 %d", stream.seen.MaxTokens)
	}
}

func TestEngine路由认不出来就用默认策略(t *testing.T) {
	t.Parallel()

	log := openLog(t)
	config := engineConfig(t, func(c *Config) {
		c.MaxTokens = 333
		c.Summarization = &Target{Provider: "摘要家", Model: "摘要模型"}
	})
	stream := textStream("一份摘要")
	deps := engineDeps()
	deps.Summarize = nil
	deps.Stream = stream
	engine := newTestEngine(t, config, deps)

	if _, err := engine.CompactRegion(t.Context(), log.seqs[0], log.seqs[1],
		agentAt(log.live, "", "")); err != nil {
		t.Fatalf("这一趟该成：%v", err)
	}
	if stream.seen.MaxTokens != 333 {
		t.Fatalf("总结上限用成了 %d", stream.seen.MaxTokens)
	}
	if stream.seen.Provider != "摘要家" || stream.seen.Model != "摘要模型" {
		t.Fatalf("发给了 %s/%s", stream.seen.Provider, stream.seen.Model)
	}
}
