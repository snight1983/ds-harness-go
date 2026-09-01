// 本文件的作用：把回放装到一台**真的**运行时上再验一遍——分块从哪儿出来、
// 一条抛出来的流长什么样、取消在哪几处都掐得掉、提供方目录发现得到，
// 以及那台按会话分游标的机器在父子交错调用时各走各的。

package replay

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/llm"
)

// installFromCalls 拿若干次录下来的调用装一台回放运行时。
func installFromCalls(t *testing.T, calls ...[]llm.StreamChunk) (*llm.Runtime, *Handle) {
	t.Helper()
	dir := fixtureDir(t)
	return installReplay(t, Config{File: writeCalls(t, dir, "session.jsonl", "p", 1, calls...)})
}

// installFromSidecar 拿一份旁挂文件装一台回放运行时；主 JSONL 只有一行头。
func installFromSidecar(t *testing.T, document string) (*llm.Runtime, *Handle) {
	t.Helper()
	dir := fixtureDir(t)
	file := writeFile(t, filepath.Join(dir, "session.jsonl"), sessionJSONL(headerLine(t, "p", 1, 0)))
	sidecar := writeFile(t, filepath.Join(dir, "replay.override.json"), document)
	return installReplay(t, Config{File: file, OverrideFile: sidecar})
}

func TestInstallServesTheDerivedChunksBack(t *testing.T) {
	// 没给 m 登记过任何适配器——回放必须在走到那一层之前就把这次调用接住。
	runtime, _ := installFromCalls(t, textChunks())
	chunks, err := streamOnce(runtime, anonymousOptions())
	if err != nil {
		t.Fatalf("回放失败：%v", err)
	}
	if !reflect.DeepEqual(chunks, textChunks()) {
		t.Fatalf("放出来的分块不对：%+v", chunks)
	}
}

func TestInstallServesTheNthCallTheNthEntry(t *testing.T) {
	runtime, _ := installFromCalls(t, textChunks(), shortChunks("two"))
	first, err := streamOnce(runtime, anonymousOptions())
	if err != nil {
		t.Fatalf("第一次回放失败：%v", err)
	}
	second, err := streamOnce(runtime, anonymousOptions())
	if err != nil {
		t.Fatalf("第二次回放失败：%v", err)
	}
	if !reflect.DeepEqual(first, textChunks()) || !reflect.DeepEqual(second, shortChunks("two")) {
		t.Fatalf("两次调用拿到的不是各自那条：%+v / %+v", first, second)
	}
}

func TestInstallFailsLoudWhenTheScriptIsExhausted(t *testing.T) {
	runtime, _ := installFromCalls(t, textChunks())
	if _, err := streamOnce(runtime, anonymousOptions()); err != nil {
		t.Fatalf("第一次回放失败：%v", err)
	}
	_, err := streamOnce(runtime, anonymousOptions())
	if !errors.Is(err, ErrScriptExhausted) {
		t.Fatalf("要 ErrScriptExhausted，实际 %v", err)
	}
}

func TestInstallFailsLoudWhenMoreLiveSessionsCallThanWereRecorded(t *testing.T) {
	runtime, _ := installFromCalls(t, textChunks())
	if _, err := streamOnce(runtime, liveOptions("first")); err != nil {
		t.Fatalf("第一个会话回放失败：%v", err)
	}
	_, err := streamOnce(runtime, liveOptions("second"))
	if !errors.Is(err, ErrUnrecordedSession) {
		t.Fatalf("要 ErrUnrecordedSession，实际 %v", err)
	}
}

func TestInstallReplaysAThrowEntryAfterItsPrefixChunks(t *testing.T) {
	runtime, _ := installFromSidecar(t, `[{"kind":"throw",
		"chunks":[{"type":"block-start","index":0,"blockType":"text"}],
		"message":"unauthorized","code":"AUTH"}]`)
	chunks, err := streamOnce(runtime, anonymousOptions())
	if err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("要那个录下来的错误，实际 %v", err)
	}
	var failure *llm.Error
	if !errors.As(err, &failure) || failure.Failure.Code != "AUTH" {
		t.Fatalf("要一个带稳定失败码的 llm.Error，实际 %v", err)
	}
	if len(chunks) != 1 || chunks[0].ChunkType() != llm.ChunkBlockStart {
		t.Fatalf("抛出来之前那半截输出该照样交出去，实际 %+v", chunks)
	}
}

func TestInstallReplaysAHangEntryThatSurfacesCancellation(t *testing.T) {
	dir := fixtureDir(t)
	ready := filepath.Join(dir, "stream-ready")
	runtime, _ := installFromSidecar(t, `[{"kind":"hang","readyFile":`+mustJSON(t, ready)+`}]`)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sequence, err := runtime.Stream(ctx, anonymousOptions())
	if err != nil {
		t.Fatalf("建流失败：%v", err)
	}
	// 就绪标记一落下就取消：等的是那个文件而不是一段睡眠，于是这条用例不看时序。
	go func() {
		for {
			if _, statErr := os.Stat(ready); statErr == nil {
				cancel()
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	chunks, err := drain(sequence)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("要 context.Canceled，实际 %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("挂住之前那两块该先出来，实际 %+v", chunks)
	}
}

func TestInstallSurfacesAnAlreadyCancelledContextEverywhereItCan(t *testing.T) {
	cases := map[string]string{
		"正常吐完":  `[{"kind":"chunks","chunks":[{"type":"finish","reason":{"kind":"stop"}}]}]`,
		"抛出来那条": `[{"kind":"throw","chunks":[{"type":"block-start","index":0,"blockType":"text"}],"message":"boom","code":"X"}]`,
		"挂住那条":  `[{"kind":"hang"}]`,
	}
	for name, document := range cases {
		t.Run(name, func(t *testing.T) {
			runtime, _ := installFromSidecar(t, document)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := streamContext(ctx, runtime, anonymousOptions())
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("要 context.Canceled，实际 %v", err)
			}
		})
	}
}

func TestInstallWritesTheReadyMarkerFailureIntoTheStream(t *testing.T) {
	// 就绪标记落不下去是场景自己的配置问题，它必须从这条流上说出来。
	dir := fixtureDir(t)
	ready := filepath.Join(dir, "missing-dir", "stream-ready")
	runtime, _ := installFromSidecar(t, `[{"kind":"hang","readyFile":`+mustJSON(t, ready)+`}]`)
	_, err := streamOnce(runtime, anonymousOptions())
	if err == nil || !strings.Contains(err.Error(), "就绪标记") {
		t.Fatalf("要一句点名就绪标记的错，实际 %v", err)
	}
}

func TestInstallPacesChunkYields(t *testing.T) {
	// 节流只是一个拟真旋钮，所以这里只验它真的等了；等的中途取消得掉是下一条用例的事。
	paced, _ := installReplay(t, Config{
		File: writeCalls(t, fixtureDir(t), "session.jsonl", "p", 1, textChunks()),
		Pace: 10 * time.Millisecond,
	})
	begun := time.Now()
	if _, err := streamOnce(paced, anonymousOptions()); err != nil {
		t.Fatalf("节流回放失败：%v", err)
	}
	if elapsed := time.Since(begun); elapsed < 10*time.Millisecond {
		t.Fatalf("每一块之间该等一个节流间隔，实际总共才 %s", elapsed)
	}
}

func TestInstallCancelsPromptlyDuringAPaceWait(t *testing.T) {
	dir := fixtureDir(t)
	runtime, _ := installReplay(t, Config{
		File: writeCalls(t, dir, "session.jsonl", "p", 1, textChunks()),
		Pace: time.Hour,
	})
	ctx, cancel := context.WithCancel(context.Background())
	sequence, err := runtime.Stream(ctx, anonymousOptions())
	if err != nil {
		t.Fatalf("建流失败：%v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, drainErr := drain(sequence)
		done <- drainErr
	}()
	cancel()
	select {
	case drainErr := <-done:
		if !errors.Is(drainErr, context.Canceled) {
			t.Fatalf("要 context.Canceled，实际 %v", drainErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("节流等待中途取消该当场把流掐掉")
	}
}

func TestInstallStopsWhenTheConsumerBreaksEarly(t *testing.T) {
	runtime, _ := installFromCalls(t, textChunks())
	sequence, err := runtime.Stream(context.Background(), anonymousOptions())
	if err != nil {
		t.Fatalf("建流失败：%v", err)
	}
	count := 0
	for range sequence {
		count++
		break
	}
	if count != 1 {
		t.Fatalf("提前 break 该只取到一块，实际 %d", count)
	}
}

func TestInstallRegistersAReplayOnlyProviderCatalog(t *testing.T) {
	dir := fixtureDir(t)
	maxRetries := 2
	jitter := 0.0
	runtime, handle := installReplay(t, Config{
		File: writeCalls(t, dir, "session.jsonl", "p", 1, textChunks()),
		Providers: []ProviderConfig{
			{
				ID:   "deepseek",
				Name: "DeepSeek",
				RetryPolicy: &llm.RetryPolicyConfig{
					Mode:       llm.RetryNormal,
					MaxRetries: &maxRetries,
					Backoff: llm.BackoffConfig{
						InitialDelay: time.Millisecond,
						MaxDelay:     time.Millisecond,
						JitterRatio:  &jitter,
					},
				},
				Models: []ModelConfig{
					{
						ID:                     "flash",
						ContextWindow:          128_000,
						InputModalities:        []llm.ModelModality{llm.ModalityText, llm.ModalityImage},
						DefaultMaxTokens:       64_000,
						ReasoningEfforts:       []string{"off", "max"},
						DefaultReasoningEffort: "max",
					},
					{ID: "pro", Name: "Pro", Description: "Larger model"},
				},
			},
			{ID: "empty"},
		},
	})

	providers := runtime.ListProviders()
	want := []llm.ProviderInfo{{ID: "deepseek", Name: "DeepSeek"}, {ID: "empty", Name: "empty"}}
	if !reflect.DeepEqual(providers, want) {
		t.Fatalf("提供方目录不对：%+v", providers)
	}

	models, err := runtime.ListModels(context.Background(), "deepseek")
	if err != nil || len(models) != 2 || models[1].Name != "Pro" {
		t.Fatalf("模型清单不对：%+v / %v", models, err)
	}
	if empty, listErr := runtime.ListModels(context.Background(), "empty"); listErr != nil || len(empty) != 0 {
		t.Fatalf("空目录该交空清单，实际 %+v / %v", empty, listErr)
	}

	flash, err := runtime.ResolveModelInfo(context.Background(), "deepseek", "flash")
	if err != nil {
		t.Fatalf("解算 flash 失败：%v", err)
	}
	if flash.Context == nil || flash.Context.ContextWindow != 128_000 || flash.DefaultMaxTokens != 64_000 {
		t.Fatalf("flash 的容量与上限不对：%+v", flash)
	}
	if flash.Reasoning == nil || len(flash.Reasoning.Efforts) != 2 || flash.Reasoning.DefaultEffort != "max" {
		t.Fatalf("flash 的推理档位不对：%+v", flash.Reasoning)
	}

	// 一个没声明这些的模型不发布容量、上限和档位——「没给」和「给了个零」是两件事。
	pro, err := runtime.ResolveModelInfo(context.Background(), "deepseek", "pro")
	if err != nil || pro.Context != nil || pro.Reasoning != nil || pro.DefaultMaxTokens != 0 {
		t.Fatalf("pro 不该发布这些：%+v / %v", pro, err)
	}
	// 目录里没有的模型 id 照样解算得出来，交回的就是那个身份本身。
	unlisted, err := runtime.ResolveModelInfo(context.Background(), "deepseek", "unlisted")
	if err != nil || unlisted.ID != "unlisted" || unlisted.Context != nil {
		t.Fatalf("目录外的模型该只交回身份，实际 %+v / %v", unlisted, err)
	}
	unknown, err := runtime.ResolveModelInfo(context.Background(), "empty", "unlisted")
	if err != nil || unknown.Name != "unlisted" {
		t.Fatalf("没配模型的路线该只交回身份，实际 %+v / %v", unknown, err)
	}

	policy, err := runtime.ProviderRetryPolicy("deepseek")
	if err != nil || policy.MaxRetries != 2 || policy.InitialDelay != time.Millisecond {
		t.Fatalf("这条路线该发布它自己的策略：%+v / %v", policy, err)
	}
	fallback, err := runtime.ProviderRetryPolicy("empty")
	if err != nil || fallback.MaxRetries != 5 {
		t.Fatalf("没发布策略的路线该回落到运行时默认：%+v / %v", fallback, err)
	}

	chunks, err := streamOnce(runtime, llm.GenerateOptions{Provider: "deepseek", Model: "pro"})
	if err != nil || !reflect.DeepEqual(chunks, textChunks()) {
		t.Fatalf("走适配器那条路的回放不对：%+v / %v", chunks, err)
	}

	if err := handle.Dispose(context.Background()); err != nil {
		t.Fatalf("摘除失败：%v", err)
	}
	if left := runtime.ListProviders(); len(left) != 0 {
		t.Fatalf("摘除之后目录该空了，实际 %+v", left)
	}
}

func TestInstallNormalizesADispatchFailureOnTheAdapterPath(t *testing.T) {
	// 配了提供方目录时走的是适配器边界，而运行时把那一侧的失败归一成一个终止分块——
	// 兜底监听器那条路上同一件事是从 Stream 的第二个返回值出来的。
	dir := fixtureDir(t)
	runtime, _ := installReplay(t, Config{
		File:      writeCalls(t, dir, "session.jsonl", "p", 1, textChunks()),
		Providers: []ProviderConfig{{ID: "deepseek"}},
	})
	options := llm.GenerateOptions{Provider: "deepseek", Model: "m"}
	if _, err := streamOnce(runtime, options); err != nil {
		t.Fatalf("第一次回放失败：%v", err)
	}
	chunks, err := streamOnce(runtime, options)
	if err != nil {
		t.Fatalf("适配器那条路不该从第二个返回值报错：%v", err)
	}
	finish, ok := chunks[len(chunks)-1].(llm.FinishChunk)
	if !ok {
		t.Fatalf("最后一块该是 finish，实际 %+v", chunks)
	}
	failed, ok := finish.Reason.(llm.ErrorFinish)
	if !ok || !strings.Contains(failed.Failure.Message, "剧本用完了") {
		t.Fatalf("要一个说得清「剧本用完了」的终止分块，实际 %+v", finish.Reason)
	}
}

func TestInstallResolvesEveryPlaceholderAgainstTheLiveRequest(t *testing.T) {
	dir := fixtureDir(t)
	file := writeFile(t, filepath.Join(dir, "session.jsonl"), sessionJSONL(headerLine(t, "p", 1, 0)))
	sidecar := writeFile(t, filepath.Join(dir, "replay.override.json"),
		mustJSON(t, []Entry{ChunksEntry{Chunks: scriptedCall(`{"goal_id":"{{fromRequest:goal-[0-9a-z]+}}"}`)}}))
	runtime, _ := installReplay(t, Config{File: file, OverrideFile: sidecar})

	options := anonymousOptions()
	options.Messages = requestMessages()
	chunks, err := streamOnce(runtime, options)
	if err != nil {
		t.Fatalf("回放失败：%v", err)
	}
	delta, ok := chunks[1].(llm.ToolCallDeltaChunk)
	if !ok || delta.ArgumentsDelta != `{"goal_id":"goal-42ab"}` {
		t.Fatalf("占位符没拿活请求解算：%+v", chunks[1])
	}
}

func TestInstallSurfacesAnUnresolvablePlaceholderBeforeStreaming(t *testing.T) {
	dir := fixtureDir(t)
	file := writeFile(t, filepath.Join(dir, "session.jsonl"), sessionJSONL(headerLine(t, "p", 1, 0)))
	sidecar := writeFile(t, filepath.Join(dir, "replay.override.json"),
		mustJSON(t, []Entry{ChunksEntry{Chunks: scriptedCall(`{"goal_id":"{{fromRequest:task-[0-9]+}}"}`)}}))
	runtime, _ := installReplay(t, Config{File: file, OverrideFile: sidecar})
	options := anonymousOptions()
	options.Messages = requestMessages()
	if _, err := streamOnce(runtime, options); !errors.Is(err, ErrScriptedPlaceholder) {
		t.Fatalf("要 ErrScriptedPlaceholder，实际 %v", err)
	}
}

func TestInstallRoutesEachLiveSessionToItsOwnScriptByFirstCallOrder(t *testing.T) {
	dir := fixtureDir(t)
	parent := writeCalls(t, dir, "session.jsonl", "rec-parent", 100, textChunks(), shortChunks("a2"))
	child := writeCalls(t, dir, "session.1.jsonl", "rec-child", 200, shortChunks("child"), shortChunks("b2"))
	runtime, _ := installReplay(t, Config{File: parent, ChildFiles: []string{child}})

	// 交错着调：绑定看的是第一次调用的次序，此后各自推进各自的游标。
	steps := []struct {
		session llm.SessionID
		want    []llm.StreamChunk
	}{
		{"live-A", textChunks()},
		{"live-B", shortChunks("child")},
		{"live-A", shortChunks("a2")},
		{"live-B", shortChunks("b2")},
	}
	for index, step := range steps {
		chunks, err := streamOnce(runtime, liveOptions(step.session))
		if err != nil {
			t.Fatalf("第 %d 次回放失败：%v", index, err)
		}
		if !reflect.DeepEqual(chunks, step.want) {
			t.Fatalf("第 %d 次回放拿错了剧本：%+v", index, chunks)
		}
	}
}

func TestAssertConsumedPassesOnlyAfterEveryRecordedCallReplayed(t *testing.T) {
	runtime, handle := installFromCalls(t, textChunks(), shortChunks("two"))
	if _, err := streamOnce(runtime, liveOptions("live-underrun")); err != nil {
		t.Fatalf("回放失败：%v", err)
	}
	err := handle.AssertConsumed()
	if !errors.Is(err, ErrFixtureNotConsumed) || !strings.Contains(err.Error(), "live-underrun") {
		t.Fatalf("要一句点名那个会话的 ErrFixtureNotConsumed，实际 %v", err)
	}
	if !strings.Contains(err.Error(), "1/2") {
		t.Fatalf("要说清跑了几次录了几次，实际 %v", err)
	}
	if _, err := streamOnce(runtime, liveOptions("live-underrun")); err != nil {
		t.Fatalf("第二次回放失败：%v", err)
	}
	if err := handle.AssertConsumed(); err != nil {
		t.Fatalf("跑完了不该再报：%v", err)
	}
}

func TestAssertConsumedNamesTheAnonymousSession(t *testing.T) {
	runtime, handle := installFromCalls(t, textChunks(), shortChunks("two"))
	if _, err := streamOnce(runtime, anonymousOptions()); err != nil {
		t.Fatalf("回放失败：%v", err)
	}
	err := handle.AssertConsumed()
	if !errors.Is(err, ErrFixtureNotConsumed) || !strings.Contains(err.Error(), "匿名会话") {
		t.Fatalf("要一句点名匿名会话的 ErrFixtureNotConsumed，实际 %v", err)
	}
}

func TestAssertConsumedReportsScriptsNoLiveSessionEverBound(t *testing.T) {
	dir := fixtureDir(t)
	parent := writeCalls(t, dir, "session.jsonl", "p", 1, textChunks())
	child := writeCalls(t, dir, "session.1.jsonl", "c", 2, textChunks())
	runtime, handle := installReplay(t, Config{File: parent, ChildFiles: []string{child}})
	if _, err := streamOnce(runtime, liveOptions("live-parent")); err != nil {
		t.Fatalf("回放失败：%v", err)
	}
	err := handle.AssertConsumed()
	if !errors.Is(err, ErrFixtureNotConsumed) || !strings.Contains(err.Error(), "从没绑到活会话") {
		t.Fatalf("要一句说清有剧本没被绑上的 ErrFixtureNotConsumed，实际 %v", err)
	}
}

func TestInstallRejectsAConfigItCannotHonour(t *testing.T) {
	dir := fixtureDir(t)
	file := writeCalls(t, dir, "session.jsonl", "p", 1, textChunks())
	badRetries := -1
	cases := map[string]struct {
		runtime *llm.Runtime
		config  Config
		want    error
	}{
		"没给运行时": {nil, Config{File: file}, ErrInvalidConfig},
		"负数 Pace": {
			llm.NewRuntime(llm.RuntimeOptions{}), Config{File: file, Pace: -1}, ErrInvalidConfig,
		},
		"模态认不出来": {
			llm.NewRuntime(llm.RuntimeOptions{}),
			Config{File: file, Providers: []ProviderConfig{
				{ID: "m", Models: []ModelConfig{{ID: "m", InputModalities: []llm.ModelModality{"audio"}}}},
			}},
			ErrInvalidConfig,
		},
		"夹具不在": {
			llm.NewRuntime(llm.RuntimeOptions{}),
			Config{File: filepath.Join(dir, "absent.jsonl")}, ErrFixtureNotFound,
		},
		"旁挂文件里的 kind 不认识": {
			llm.NewRuntime(llm.RuntimeOptions{}),
			Config{
				File:         file,
				OverrideFile: writeFile(t, filepath.Join(dir, "bogus.json"), `[{"kind":"bogus"}]`),
			},
			ErrInvalidOverride,
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Install(context.Background(), testScope(t), testCase.runtime, testCase.config)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("要 %v，实际 %v", testCase.want, err)
			}
		})
	}

	// 一条坏掉的重试策略在装配这一步就被拒——那个接缝本身交不出错误。
	_, err := Install(context.Background(), testScope(t), llm.NewRuntime(llm.RuntimeOptions{}), Config{
		File: file,
		Providers: []ProviderConfig{
			{ID: "deepseek", RetryPolicy: &llm.RetryPolicyConfig{Mode: llm.RetryNormal, MaxRetries: &badRetries}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "deepseek") {
		t.Fatalf("要一句点名那条路线的策略错误，实际 %v", err)
	}
}

func TestInstallRejectsAnOwnerThatIsAlreadyDisposed(t *testing.T) {
	// 两条装法都要在挂不上作用域时把错交出来——装不上却当装上了，那份登记就没人撤了。
	dir := fixtureDir(t)
	file := writeCalls(t, dir, "session.jsonl", "p", 1, textChunks())
	for name, providers := range map[string][]ProviderConfig{
		"适配器那条路":   {{ID: "deepseek"}},
		"兜底监听器那条路": nil,
	} {
		t.Run(name, func(t *testing.T) {
			owner := scope.NewRoot()
			if err := owner.Dispose(context.Background()); err != nil {
				t.Fatalf("释放作用域失败：%v", err)
			}
			_, err := Install(context.Background(), owner, llm.NewRuntime(llm.RuntimeOptions{}),
				Config{File: file, Providers: providers})
			if err == nil {
				t.Fatal("作用域已经释放了，装配该失败")
			}
		})
	}
}

func TestReplayEntryFailsLoudOnAnEntryKindItDoesNotKnow(t *testing.T) {
	// 这条封闭联合只有本包实现得了，所以走到这一支只可能是本包自己漏了一个分支。
	_, err := drain(replayEntry(context.Background(), unknownEntry{}, 0))
	if err == nil || !strings.Contains(err.Error(), "不认识") {
		t.Fatalf("要一句「类型不认识」，实际 %v", err)
	}
}

// unknownEntry 是一条本包没有分支接得住的条目，只在上面那条用例里用。
type unknownEntry struct{}

func (unknownEntry) Kind() EntryKind { return "unknown" }

func (unknownEntry) sealedEntry() {}

func TestReplayEntryStopsWhenTheConsumerBreaksInsideAHang(t *testing.T) {
	// 挂住那条在等取消之前先吐两块；消费者在这两块的任何一块上走掉，它都必须当场停，
	// 而不是接着走到那个「等取消」上去挂死。
	for _, take := range []int{1, 2} {
		t.Run(strconv.Itoa(take), func(t *testing.T) {
			sequence := replayEntry(context.Background(), HangEntry{}, 0)
			count := 0
			for range sequence {
				count++
				if count == take {
					break
				}
			}
			if count != take {
				t.Fatalf("该只取到 %d 块，实际 %d", take, count)
			}
		})
	}
}

func TestPaceDelayWaitsOutTheIntervalAndYieldsCancellation(t *testing.T) {
	discard := func(llm.StreamChunk, error) bool { return true }
	if !paceDelay(context.Background(), time.Millisecond, discard) {
		t.Fatal("等满了该接着往下走")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var got error
	captured := func(_ llm.StreamChunk, err error) bool {
		got = err
		return true
	}
	if paceDelay(ctx, time.Hour, captured) {
		t.Fatal("取消了该把流掐掉")
	}
	if !errors.Is(got, context.Canceled) {
		t.Fatalf("要 context.Canceled，实际 %v", got)
	}
}

func TestAdapterAnswersForAProviderItWasNeverGiven(t *testing.T) {
	// 运行时只会把登记过的路线派到这台适配器上，所以这几手是防自己写错的；
	// 它们必须交回「没有」而不是造一份出来。
	empty := &adapter{providers: map[string]ProviderConfig{}}
	if _, published := empty.ProviderRetryPolicy("absent"); published {
		t.Fatal("没这条路线时不该发布策略")
	}
	models, err := empty.ListModels(context.Background(), "absent")
	if err != nil || models != nil {
		t.Fatalf("没这条路线时该交空清单，实际 %+v / %v", models, err)
	}
	resolved, err := empty.ResolveModel(context.Background(), "absent", "m")
	if err != nil || resolved.ID != "m" || resolved.Name != "m" {
		t.Fatalf("没这条路线时该只交回身份，实际 %+v / %v", resolved, err)
	}

	// 一条解算不出来的策略当成没发布：装配那一步已经把它挡在外面了，这里只保证
	// 万一漏进来也不会把一份半成品的策略发出去。
	badRetries := -1
	broken := &adapter{providers: map[string]ProviderConfig{
		"x": {ID: "x", RetryPolicy: &llm.RetryPolicyConfig{Mode: llm.RetryNormal, MaxRetries: &badRetries}},
	}}
	if _, published := broken.ProviderRetryPolicy("x"); published {
		t.Fatal("解算不出来的策略不该发布")
	}
}

func TestHandleDisposeIsSafeWithoutAnInstallation(t *testing.T) {
	if err := (&Handle{}).Dispose(context.Background()); err != nil {
		t.Fatalf("空句柄摘除不该报错：%v", err)
	}
}
