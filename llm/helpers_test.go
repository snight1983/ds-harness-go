// 本文件的作用：运行时那几组测试共用的东西——造作用域、造运行时，以及一个
// 每一件可选能力都能按需打开的假适配器。
//
// 假适配器写成一个**指针**类型是必须的，不是风格：[Runtime.forAdapter] 拿两个
// [Adapter] 接口值直接 == 比较（见那个函数的注释），一个带函数字段的结构体值
// 不可比较，那一比会 panic。

package llm

import (
	"context"
	"iter"
	"testing"

	"github.com/snight1983/ds-harness-go/core/scope"
)

// testScope 造一个用完自动释放的作用域。
func testScope(t *testing.T) *scope.Scope {
	t.Helper()
	owner := scope.NewRoot()
	t.Cleanup(func() { _ = owner.Dispose(context.Background()) })
	return owner
}

// newTestRuntime 造一个空运行时。
func newTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	return NewRuntime(RuntimeOptions{})
}

// fakeAdapter 是一个可选能力全靠字段开关的假适配器：某个钩子为 nil 就等于
// 这个适配器没实现那件可选的事，于是走运行时那份兜底。
//
// 它把五个可选接口全都实现了，靠钩子是不是 nil 来分支——因为 Go 的接口断言看的是
// 方法集，方法集是编译期定死的，没法在一个类型上按实例开关某个接口。所以「没实现」
// 这件事只能在方法体里表达，而运行时那五个 Adapter* 函数的兜底分支要另外用
// [bareAdapter] 去覆盖。
type fakeAdapter struct {
	providerInfo func(provider string) ProviderInfo
	retryPolicy  func(provider string) (ResolvedRetryPolicy, bool)
	listModels   func(ctx context.Context, provider string) ([]ModelInfo, error)
	resolveModel func(ctx context.Context, provider, model string) (ResolvedModelInfo, error)
	prepareCall  func(ctx context.Context, provider, model string) (PreparedAdapterCall, error)
	stream       func(ctx context.Context, options GenerateOptions) (iter.Seq2[StreamChunk, error], error)

	// seen 记下最后一次派发到适配器边界的那份请求，给「投影」「摘重放状态」两组
	// 用例读。
	seen GenerateOptions
}

func (a *fakeAdapter) Stream(ctx context.Context, options GenerateOptions) (iter.Seq2[StreamChunk, error], error) {
	a.seen = options
	if a.stream != nil {
		return a.stream(ctx, options)
	}
	return chunks(FinishChunk{Reason: StopFinish{}}), nil
}

func (a *fakeAdapter) ProviderInfo(provider string) ProviderInfo {
	if a.providerInfo != nil {
		return a.providerInfo(provider)
	}
	return ProviderInfo{ID: provider, Name: provider}
}

func (a *fakeAdapter) ProviderRetryPolicy(provider string) (ResolvedRetryPolicy, bool) {
	if a.retryPolicy != nil {
		return a.retryPolicy(provider)
	}
	return ResolvedRetryPolicy{}, false
}

func (a *fakeAdapter) ListModels(ctx context.Context, provider string) ([]ModelInfo, error) {
	if a.listModels != nil {
		return a.listModels(ctx, provider)
	}
	return nil, nil
}

func (a *fakeAdapter) ResolveModel(ctx context.Context, provider, model string) (ResolvedModelInfo, error) {
	if a.resolveModel != nil {
		return a.resolveModel(ctx, provider, model)
	}
	return ResolvedModelInfo{ModelInfo: ModelInfo{Provider: provider, ID: model, Name: model}}, nil
}

func (a *fakeAdapter) PrepareCall(ctx context.Context, provider, model string) (PreparedAdapterCall, error) {
	if a.prepareCall != nil {
		return a.prepareCall(ctx, provider, model)
	}
	resolved, err := a.ResolveModel(ctx, provider, model)
	if err != nil {
		return PreparedAdapterCall{}, err
	}
	return PreparedAdapterCall{Model: resolved, Stream: a.Stream}, nil
}

// bareAdapter 只实现 [Adapter] 那一个必须的方法，一件可选的事都不做。它存在的
// 唯一理由是覆盖那五个 Adapter* 函数的兜底分支——[fakeAdapter] 的方法集里那五个
// 可选方法都在，类型断言永远成立，兜底那几行它走不到。
type bareAdapter struct {
	stream func(ctx context.Context, options GenerateOptions) (iter.Seq2[StreamChunk, error], error)
}

func (a *bareAdapter) Stream(ctx context.Context, options GenerateOptions) (iter.Seq2[StreamChunk, error], error) {
	if a.stream != nil {
		return a.stream(ctx, options)
	}
	return chunks(FinishChunk{Reason: StopFinish{}}), nil
}

// resolveOnlyAdapter 实现 [Adapter] 和 [ModelResolver]，但**不**实现 [CallPreparer]。
//
// 它存在的唯一理由是走到 [AdapterPrepareCall] 兜底那一支里「解算失败」那一跳：
// [fakeAdapter] 的方法集里 PrepareCall 在，兜底根本进不去；[bareAdapter] 连
// ResolveModel 都没有，兜底走的是内联默认，那份默认不会失败。
type resolveOnlyAdapter struct {
	resolveModel func(ctx context.Context, provider, model string) (ResolvedModelInfo, error)
}

func (a *resolveOnlyAdapter) Stream(context.Context, GenerateOptions) (iter.Seq2[StreamChunk, error], error) {
	return chunks(FinishChunk{Reason: StopFinish{}}), nil
}

func (a *resolveOnlyAdapter) ResolveModel(ctx context.Context, provider, model string) (ResolvedModelInfo, error) {
	return a.resolveModel(ctx, provider, model)
}

// chunks 把几个分块包成一条一次性的流。
func chunks(items ...StreamChunk) iter.Seq2[StreamChunk, error] {
	return func(yield func(StreamChunk, error) bool) {
		for _, item := range items {
			if !yield(item, nil) {
				return
			}
		}
	}
}

// failingStream 造一条先吐几块、再交出一个错误的流。
func failingStream(err error, items ...StreamChunk) iter.Seq2[StreamChunk, error] {
	return func(yield func(StreamChunk, error) bool) {
		for _, item := range items {
			if !yield(item, nil) {
				return
			}
		}
		yield(nil, err)
	}
}

// drain 把一条流读干，交出读到的分块和第一个非 nil 的错误。
func drain(t *testing.T, stream iter.Seq2[StreamChunk, error]) ([]StreamChunk, error) {
	t.Helper()
	var collected []StreamChunk
	for chunk, err := range stream {
		if err != nil {
			return collected, err
		}
		collected = append(collected, chunk)
	}
	return collected, nil
}

// registerFake 把一个假适配器登记到一条路由上，用完由作用域自动摘掉。
func registerFake(t *testing.T, runtime *Runtime, provider string, adapter Adapter) *AdapterRegistration {
	t.Helper()
	handle, err := runtime.RegisterAdapter(t.Context(), testScope(t), []string{provider}, adapter)
	if err != nil {
		t.Fatalf("登记适配器不该失败：%v", err)
	}
	return handle
}

// finishOf 断言这条流的最后一块是 finish，并交出它的结束原因。
func finishOf(t *testing.T, collected []StreamChunk) FinishReason {
	t.Helper()
	if len(collected) == 0 {
		t.Fatal("流里一块都没有")
	}
	finish, ok := collected[len(collected)-1].(FinishChunk)
	if !ok {
		t.Fatalf("最后一块该是 finish，得到 %T", collected[len(collected)-1])
	}
	return finish.Reason
}

// failureOf 从一个终止结局里取出那份失败事实。
func failureOf(t *testing.T, reason FinishReason) Failure {
	t.Helper()
	switch typed := reason.(type) {
	case ErrorFinish:
		return typed.Failure
	case AbortedFinish:
		return typed.Failure
	default:
		t.Fatalf("该是一个带失败事实的结局，得到 %q", reason.FinishKind())
		return Failure{}
	}
}
