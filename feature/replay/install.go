// 本文件的作用：把回放装到一台运行时上——按会话绑剧本、把一条条目派成流、
// 以及那台让「场景比录下来的调用少跑了几次」在拆解时变成一句话的消费检查。
//
// 源: packages/test-support/llm-replay/src/index.ts:583-800

package replay

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
)

// ErrScriptExhausted 是「这个会话要的调用次数比录下来的多」。
var ErrScriptExhausted = errors.New("llm-replay: 剧本用完了")

// ErrUnrecordedSession = 「来了一个没录过的会话」。
var ErrUnrecordedSession = errors.New("llm-replay: 来了一个没录过的会话")

// ErrFixtureNotConsumed 是「录下来的调用没被跑完」。
var ErrFixtureNotConsumed = errors.New("llm-replay: 夹具没被跑完")

// anonymousSession 是一次**不带**会话 id 的调用共用的那把键。
//
// 用一个正常会话 id 里出不来的值，于是它和任何真的 id 都撞不上。
const anonymousSession llm.SessionID = "\x00anon\x00"

// Handle 是 [Install] 交回来的句柄：摘除，加上那台跑完时的消费检查。
//
// 源: packages/test-support/llm-replay/src/index.ts:136-151（ReplayHandle）
type Handle struct {
	dispose func(context.Context) error

	mutex sync.Mutex
	// scripts 是按绑定次序排的那些剧本。
	scripts []SessionScript
	// bound 是活会话到它那份剧本游标的绑定。
	bound map[llm.SessionID]*cursor
	// order 是活会话第一次调用的先后，只为让诊断稳定。
	order []llm.SessionID
	// nextScript 是下一个还没被认领的剧本下标。
	nextScript int
}

// cursor 是一个活会话在它那份剧本上走到哪儿了。
type cursor struct {
	entries []Entry
	next    int
}

// Dispose 把登记的适配器或者那条兜底监听器摘掉。
func (h *Handle) Dispose(ctx context.Context) error {
	if h.dispose == nil {
		return nil
	}
	return h.dispose(ctx)
}

// AssertConsumed 检查每一份录下来的剧本都绑到了一个活会话上、且每一个绑上的游标都
// 把它那串条目走完了；不满足就交回一句说得清是谁的错。在场景拆解时调。
//
// 源: packages/test-support/llm-replay/src/index.ts:762-778
//
// 新增: DSH 那边它抛，Go 这边交回 error——调用方是测试，`if err := ...; err != nil
// { t.Fatal(err) }` 就是那句抛。
func (h *Handle) AssertConsumed() error {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	var problems []string
	if h.nextScript < len(h.scripts) {
		problems = append(problems,
			fmt.Sprintf("有 %d 份录下来的剧本从没绑到活会话上", len(h.scripts)-h.nextScript))
	}
	for _, id := range h.order {
		state := h.bound[id]
		if state.next >= len(state.entries) {
			continue
		}
		who := "那个匿名会话"
		if id != anonymousSession {
			who = fmt.Sprintf("会话 %s", id)
		}
		problems = append(problems,
			fmt.Sprintf("%s 只跑了 %d/%d 次录下来的调用", who, state.next, len(state.entries)))
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w：%s；这个场景驱动的模型调用比录下来的少",
		ErrFixtureNotConsumed, strings.Join(problems, "；"))
}

// claim 认领这次调用该放的那条条目。
//
// 绑定和推进游标都在**发起调用的那一刻**同步做完，而不是等到序列被取的时候——
// 「谁绑到哪份剧本」由调用次序定，一个懒到取值才推进的游标会让两次并发调用
// 认领同一条条目。
func (h *Handle) claim(id llm.SessionID) (Entry, error) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	state, bound := h.bound[id]
	if !bound {
		if h.nextScript >= len(h.scripts) {
			return nil, fmt.Errorf("%w：一次模型调用来自第 %d 个会话，而这个场景只录了 %d 个——重录一遍",
				ErrUnrecordedSession, h.nextScript+1, len(h.scripts))
		}
		state = &cursor{entries: h.scripts[h.nextScript].Entries}
		h.nextScript++
		h.bound[id] = state
		h.order = append(h.order, id)
	}
	index := state.next
	state.next++
	if index >= len(state.entries) {
		return nil, fmt.Errorf("%w：这个会话要第 %d 次模型调用，而它那份剧本只有 %d 次——重录一遍",
			ErrScriptExhausted, index+1, len(state.entries))
	}
	return state.entries[index], nil
}

// replayEntry 把一条条目派成一个流。
//
// 源: packages/test-support/llm-replay/src/index.ts:680-716
//
// 新增: 取消走 ctx，不走 AbortSignal。挂住那一支等的是 ctx.Done()、交出去的是
// ctx.Err()；运行时看见 ctx 已经取消就把它归一成一个 aborted 的终止分块
// （见 llm 那边的 adapterFailureChunk）。
func replayEntry(ctx context.Context, entry Entry, pace time.Duration) iter.Seq2[llm.StreamChunk, error] {
	return func(yield func(llm.StreamChunk, error) bool) {
		switch scripted := entry.(type) {
		case ChunksEntry:
			emitChunks(ctx, scripted.Chunks, pace, yield)
		case ThrowEntry:
			// 演的是 LLM 那条约定里**抛**的那一支：先把它抛出来之前流掉的那些块交出去
			// （于是循环看到它当初活着时看到的那半截输出），再交那个录下来的错误
			// （比如提供方的 401，或者吐了一半之后的 STREAM_CLOSED）。
			if !emitChunks(ctx, scripted.Chunks, pace, yield) {
				return
			}
			yield(nil, llm.NewError(scripted.Message, scripted.Code, nil))
		case HangEntry:
			// 演的是一条停住等取消的流：先吐一块，再等取消、把它交上去。
			if !yield(llm.BlockStartChunk{Index: 0, BlockType: llm.BlockText}, nil) {
				return
			}
			if !yield(llm.TextDeltaChunk{Index: 0, Text: "partial"}, nil) {
				return
			}
			if scripted.ReadyFile != "" {
				if err := os.WriteFile(scripted.ReadyFile, nil, 0o600); err != nil {
					yield(nil, fmt.Errorf("llm-replay: 落不下就绪标记 %s：%w", scripted.ReadyFile, err))
					return
				}
			}
			<-ctx.Done()
			yield(nil, ctx.Err())
		default:
			// 旁挂文件里的条目在走到这个封闭联合之前就验过了，所以这里只可能是
			// 本包自己漏了一个分支。
			yield(nil, fmt.Errorf("llm-replay: 回放条目的类型不认识：%T", entry))
		}
	}
}

// emitChunks 把一串分块按节流间隔交出去；第二个返回值为假表示这条流已经停了。
func emitChunks(
	ctx context.Context,
	chunks []llm.StreamChunk,
	pace time.Duration,
	yield func(llm.StreamChunk, error) bool,
) bool {
	for _, chunk := range chunks {
		// 配了节流时取消由 [paceDelay] 里那个 select 接住，这里就不再查一遍：两处都查
		// 的话先到的永远是这一处，那个 select 的取消支便只有在竞态下才走得到。
		if pace > 0 {
			if !paceDelay(ctx, pace, yield) {
				return false
			}
		} else if err := ctx.Err(); err != nil {
			yield(nil, err)
			return false
		}
		if !yield(chunk, nil) {
			return false
		}
	}
	return true
}

// paceDelay 在两块之间等一个节流间隔，取消一到就当场把流掐掉——
// 一条节流的回放必须和一口气吐完的那条一样快地取消得掉。
//
// 源: packages/test-support/llm-replay/src/index.ts:663-677
func paceDelay(ctx context.Context, pace time.Duration, yield func(llm.StreamChunk, error) bool) bool {
	timer := time.NewTimer(pace)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		yield(nil, ctx.Err())
		return false
	}
}

// adapter 是那台让一套配好的提供方目录不碰任何提供方 I/O 就发现得到的回放适配器。
//
// 源: packages/test-support/llm-replay/src/index.ts:583-657
type adapter struct {
	providers map[string]ProviderConfig
	stream    func(ctx context.Context, options llm.GenerateOptions) (iter.Seq2[llm.StreamChunk, error], error)
}

// Stream 实现 [github.com/snight1983/ds-harness-go/llm.Adapter]。
func (a *adapter) Stream(
	ctx context.Context,
	options llm.GenerateOptions,
) (iter.Seq2[llm.StreamChunk, error], error) {
	return a.stream(ctx, options)
}

// ProviderInfo 实现 [github.com/snight1983/ds-harness-go/llm.ProviderDescriber]。
func (a *adapter) ProviderInfo(provider string) llm.ProviderInfo {
	configured, known := a.providers[provider]
	if !known || configured.Name == "" {
		return llm.ProviderInfo{ID: provider, Name: provider}
	}
	return llm.ProviderInfo{ID: provider, Name: configured.Name}
}

// ProviderRetryPolicy 实现 [github.com/snight1983/ds-harness-go/llm.RetryPolicyOwner]。
//
// 策略解算不了时当作「这条路线没发布策略」：这个接缝交不出错误，而一份坏掉的
// 策略在 [Install] 那一步已经被拒过了，走到这里说明配置是好的。
func (a *adapter) ProviderRetryPolicy(provider string) (llm.ResolvedRetryPolicy, bool) {
	configured, known := a.providers[provider]
	if !known || configured.RetryPolicy == nil {
		return llm.ResolvedRetryPolicy{}, false
	}
	resolved, err := llm.ResolveRetryPolicy(configured.RetryPolicy,
		fmt.Sprintf("llm-replay: provider %q retryPolicy", provider))
	if err != nil {
		return llm.ResolvedRetryPolicy{}, false
	}
	return resolved, true
}

// ListModels 实现 [github.com/snight1983/ds-harness-go/llm.ModelLister]。
func (a *adapter) ListModels(_ context.Context, provider string) ([]llm.ModelInfo, error) {
	configured, known := a.providers[provider]
	if !known {
		return nil, nil
	}
	models := make([]llm.ModelInfo, 0, len(configured.Models))
	for _, model := range configured.Models {
		models = append(models, modelInfo(provider, model))
	}
	return models, nil
}

// ResolveModel 实现 [github.com/snight1983/ds-harness-go/llm.ModelResolver]。
//
// 这次问询和那份参考目录无关：一个目录里没有的模型 id 照样解算得出来，
// 交回的就是那个身份本身。
func (a *adapter) ResolveModel(_ context.Context, provider, model string) (llm.ResolvedModelInfo, error) {
	configured, known := a.providers[provider]
	if !known {
		return llm.ResolvedModelInfo{
			ModelInfo: llm.ModelInfo{Provider: provider, ID: model, Name: model},
		}, nil
	}
	for _, candidate := range configured.Models {
		if candidate.ID != model {
			continue
		}
		resolved := llm.ResolvedModelInfo{
			ModelInfo:        modelInfo(provider, candidate),
			DefaultMaxTokens: candidate.DefaultMaxTokens,
		}
		if candidate.ContextWindow != 0 {
			resolved.Context = &llm.ModelContext{ContextWindow: candidate.ContextWindow}
		}
		if len(candidate.ReasoningEfforts) > 0 {
			reasoning := llm.ModelReasoningInfo{
				Efforts:       make([]llm.ReasoningEffortInfo, 0, len(candidate.ReasoningEfforts)),
				DefaultEffort: llm.ReasoningEffortID(candidate.DefaultReasoningEffort),
			}
			for _, effort := range candidate.ReasoningEfforts {
				reasoning.Efforts = append(reasoning.Efforts,
					llm.ReasoningEffortInfo{ID: llm.ReasoningEffortID(effort), Name: effort})
			}
			resolved.Reasoning = &reasoning
		}
		return resolved, nil
	}
	return llm.ResolvedModelInfo{
		ModelInfo: llm.ModelInfo{Provider: provider, ID: model, Name: model},
	}, nil
}

// modelInfo 把一条配置里的模型变成它公告出去的样子。
func modelInfo(provider string, model ModelConfig) llm.ModelInfo {
	name := model.Name
	if name == "" {
		name = model.ID
	}
	info := llm.ModelInfo{
		Provider:    provider,
		ID:          model.ID,
		Name:        name,
		Description: model.Description,
	}
	if model.InputModalities != nil {
		info.InputModalities = append([]llm.ModelModality(nil), model.InputModalities...)
	}
	return info
}

// Install 把按会话定位的回放装到一台运行时上。
//
// 源: packages/test-support/llm-replay/src/index.ts:810-913（installLlmReplay）
//
// 一个刚见到的活会话认领下一份按次序排的录好剧本，此后在**发起调用的那一刻**同步
// 推进它自己的游标；不带 SessionID 的那些调用共用同一个匿名会话。配了非空提供方
// 目录时登记一个带路由的回放适配器，否则用一条兜底的 [github.com/snight1983/ds-harness-go/llm.StreamRule]
// 拦下所有请求。
//
// 新增: DSH 那边「来了一个没录过的会话」和「剧本用完了」两件事被推迟到返回的生成器
// 里才抛，因为 cordis 的监听器必须交回一个 AsyncIterable、不能抛。Go 的
// [github.com/snight1983/ds-harness-go/llm.Adapter.Stream] 本来就把「派发不出去」和「流走到一半失败」分成
// 两处，而这两件事都发生在一个分块都还没吐之前，所以它们就是第二个返回值。
func Install(ctx context.Context, owner *scope.Scope, runtime *llm.Runtime, config Config) (*Handle, error) {
	if runtime == nil {
		return nil, fmt.Errorf("%w：装回放需要一台 LLM 运行时", ErrInvalidConfig)
	}
	if config.Pace < 0 {
		return nil, fmt.Errorf("%w：Pace 不许是负数，实际 %s", ErrInvalidConfig, config.Pace)
	}
	if err := validateConfiguredModalities(config.Providers); err != nil {
		return nil, err
	}
	providers := make(map[string]ProviderConfig, len(config.Providers))
	routes := make([]string, 0, len(config.Providers))
	for _, provider := range config.Providers {
		if provider.RetryPolicy != nil {
			if _, err := llm.ResolveRetryPolicy(provider.RetryPolicy,
				fmt.Sprintf("llm-replay: provider %q retryPolicy", provider.ID)); err != nil {
				return nil, err
			}
		}
		providers[provider.ID] = provider
		routes = append(routes, provider.ID)
	}
	scripts, err := LoadSessionScripts(config)
	if err != nil {
		return nil, err
	}

	handle := &Handle{scripts: scripts, bound: make(map[llm.SessionID]*cursor)}
	replay := func(
		streamCtx context.Context,
		options llm.GenerateOptions,
	) (iter.Seq2[llm.StreamChunk, error], error) {
		id := options.SessionID
		if id == "" {
			id = anonymousSession
		}
		entry, claimErr := handle.claim(id)
		if claimErr != nil {
			return nil, claimErr
		}
		resolved, resolveErr := ResolveScriptedEntry(entry, options.Messages)
		if resolveErr != nil {
			return nil, resolveErr
		}
		return replayEntry(streamCtx, resolved, config.Pace), nil
	}

	if len(routes) > 0 {
		registration, registerErr := runtime.RegisterAdapter(ctx, owner, routes,
			&adapter{providers: providers, stream: replay})
		if registerErr != nil {
			return nil, registerErr
		}
		handle.dispose = registration.Release
		return handle, nil
	}
	remove, ruleErr := runtime.OnStream(ctx, owner, func(
		streamCtx context.Context,
		options llm.GenerateOptions,
		_ func(context.Context) (iter.Seq2[llm.StreamChunk, error], error),
	) (iter.Seq2[llm.StreamChunk, error], error) {
		return replay(streamCtx, options)
	})
	if ruleErr != nil {
		return nil, ruleErr
	}
	handle.dispose = remove
	return handle, nil
}
