// 本文件的作用：验那次真的发出去的总结调用——请求信封摆成了上一次路由请求的
// 前缀、摘要路由按三档回落挑、以及回来的东西怎么被投影成一份安全摘要。

package basic

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/attachment"
	"github.com/snight1983/ds-harness-go/compaction"
	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
)

// # 这些测试防的是什么错
//
//   - 请求信封不再是上一次路由请求的前缀：系统提示词、工具表或者前面那串消息
//     被动了，或者那条总结指令没排在**最后**。那样提供方那边的前缀缓存整个作废，
//     每压一次都按全价重算一遍——而且**不会报错**，只会变贵。
//   - 摘要路由那三档回落的次序反了。配了摘要模型却仍然拿贵的那个去写摘要，
//     或者跳过请求头去用 agent 选项——后者是前缀缓存唯一对得上的那一个。
//   - 撞上生成上限被当成成功：一份被截断的检查点读起来是完整的，而它丢掉的正是
//     末尾那几节，模型会拿它当作全部历史。
//   - 图或者空白摘要漏进 [SummaryResult.Summary]。那份摘要会变成一条**落在表面上
//     的用户消息**，之后每一次请求都带着它。
//   - [SummaryResult.LLMStreamCall] 为真却没带 RawOutput：那条不变量在摘要事件
//     排出去的那一刻校验，在这里就得成立。
//   - [Streamer] 的签名飘了，于是 [llm.Runtime] 不再结构上满足它。

// 一个真的 LLM 运行时结构上就满足 [Streamer]，装配方直接填进去即可。
// 这一条是 [Streamer] 写成单方法接口的全部理由，所以钉在编译期。
var _ Streamer = (*llm.Runtime)(nil)

// scriptedStream 是一台照本子放分块的假流。
type scriptedStream struct {
	// chunks 是要按顺序放出去的分块。
	chunks []llm.StreamChunk
	// openErr 非 nil 时这次请求根本发不出去。
	openErr error
	// midErr 非 nil 时放完 chunks 再吐这一条错。
	midErr error
	// seen 记下最后一次收到的请求信封，给断言用。
	seen llm.GenerateOptions
	// calls 记这台流被叫了几次。
	calls int
}

func (s *scriptedStream) Stream(
	_ context.Context,
	options llm.GenerateOptions,
) (iter.Seq2[llm.StreamChunk, error], error) {
	s.calls++
	s.seen = options
	if s.openErr != nil {
		return nil, s.openErr
	}
	return func(yield func(llm.StreamChunk, error) bool) {
		for _, chunk := range s.chunks {
			if !yield(chunk, nil) {
				return
			}
		}
		if s.midErr != nil {
			yield(nil, s.midErr)
		}
	}, nil
}

// textStream 排一台放一块文本再正常收尾的流。
func textStream(text string) *scriptedStream {
	return &scriptedStream{chunks: []llm.StreamChunk{
		llm.BlockEndChunk{Index: 0, Block: llm.TextBlock{Text: text}},
		llm.FinishChunk{Reason: llm.StopFinish{}},
	}}
}

// summarizeWorkspaceID 是这些用例里那个会话归属的工作区登记。
//
// 新增: 原来这里是 filepath.Join(os.TempDir(), …)，为的是「在本机上确实绝对」。
// 会话头改用世界路径之后那条理由没有了：绝对性由纯 POSIX 的 [path.IsAbs] 判，
// 不跟着本机平台走，所以一个字面量在哪台机器上读都一样。
var summarizeWorkspaceID = session.WorkspaceID("ws-compaction-basic")

// liveSession 造一段游离会话；headers 里每一条都排一次请求头快照。
func liveSession(t *testing.T, id string, headers ...llm.CallConfig) *coresession.Session {
	t.Helper()

	sid := session.SessionID(id)
	live, err := coresession.NewSession(sid, coresession.Options{
		Header: &session.SessionHeader{ID: sid, WorkspaceID: summarizeWorkspaceID},
	})
	if err != nil {
		t.Fatalf("造会话失败：%v", err)
	}
	for _, config := range headers {
		event := session.Event{
			Type: session.EventRequestHeader,
			Data: marshalPayload(t, session.RequestHeaderData{
				Header: session.EpochHeader{Config: config},
				Reason: session.HeaderInitial,
			}),
		}
		if _, err := live.Append(event); err != nil {
			t.Fatalf("请求头写不进去：%v", err)
		}
	}
	return live
}

// agentAt 拼出压缩收的那一小片 agent。
func agentAt(live *coresession.Session, provider, model string) compaction.AgentContext {
	return compaction.AgentContext{Session: live, Provider: provider, Model: model}
}

func TestSummarizeWithLLM把总结指令排在最后(t *testing.T) {
	t.Parallel()

	// 这一条是整件事的地基：这次额外的调用必须**恰好是**上一次路由请求的一个
	// 前缀，只有末尾那条指令是新的。任何一处不一样，提供方那边的 KV 缓存作废。
	before := []llm.Message{
		llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "帮我改一下"}}, llm.UserSource{}),
		llm.NewAssistantMessage(llm.Content{llm.TextBlock{Text: "好"}}, llm.Provenance{}),
	}
	tools := []llm.ToolSchema{{Name: "read", Description: "读文件"}}
	stream := textStream("一份摘要")
	live := liveSession(t, "s-1")

	result, err := SummarizeWithLLM(t.Context(), stream, Policy{MaxTokens: 512}, SummarizationInput{
		System:   "你是一个助手",
		Tools:    tools,
		Messages: before,
	}, agentAt(live, "openai", "gpt-x"))
	if err != nil {
		t.Fatalf("这次总结该成：%v", err)
	}

	sent := stream.seen
	if sent.System != "你是一个助手" {
		t.Fatalf("系统提示词被动了：%q", sent.System)
	}
	if len(sent.Tools) != 1 || sent.Tools[0].Name != "read" {
		t.Fatalf("工具表被动了：%+v", sent.Tools)
	}
	if len(sent.Messages) != len(before)+1 {
		t.Fatalf("发出去 %d 条消息", len(sent.Messages))
	}
	for index, want := range before {
		if sent.Messages[index].ID != want.ID {
			t.Fatalf("第 %d 条不是原来那条", index)
		}
	}

	last := sent.Messages[len(sent.Messages)-1]
	if last.Role != llm.RoleUser {
		t.Fatalf("那条指令的角色是 %q", last.Role)
	}
	text, ok := last.Content[0].(llm.TextBlock)
	if !ok || text.Text != compactionInstruction {
		t.Fatalf("最后一条不是那条总结指令：%+v", last.Content)
	}
	source, ok := last.Source.(llm.PluginSource)
	if !ok || source.Plugin != compactionPlugin {
		t.Fatalf("那条指令的署名是 %+v", last.Source)
	}

	// 这三样决定这次调用怎么被记账、以及它属于哪段会话。
	if sent.Purpose != llm.PurposeCompaction {
		t.Fatalf("用途排成了 %q", sent.Purpose)
	}
	if sent.SessionID != llm.SessionID("s-1") {
		t.Fatalf("会话身份排成了 %q", sent.SessionID)
	}
	if sent.MaxTokens != 512 {
		t.Fatalf("生成上限排成了 %d", sent.MaxTokens)
	}

	if !result.LLMStreamCall || len(result.RawOutput) != 1 {
		t.Fatalf("这份结果没把原始输出带上：%+v", result)
	}
	if result.MaxTokens != 512 {
		t.Fatalf("回来的生成上限是 %d", result.MaxTokens)
	}
}

func TestSummarizeWithLLM不改调用方那份消息(t *testing.T) {
	t.Parallel()

	// 被遮的那一段还要原样落进 compaction/summary 的账里，就地追加一条指令
	// 等于把日志记的和模型看的搅到一起。
	before := []llm.Message{
		llm.NewUserMessage(llm.Content{llm.TextBlock{Text: "一句话"}}, llm.UserSource{}),
	}
	input := SummarizationInput{Messages: before}
	live := liveSession(t, "s-1")

	if _, err := SummarizeWithLLM(t.Context(), textStream("摘要"), Policy{},
		input, agentAt(live, "openai", "gpt-x")); err != nil {
		t.Fatalf("这次总结该成：%v", err)
	}
	if len(input.Messages) != 1 || len(before) != 1 {
		t.Fatalf("调用方那份被改成了 %d 条", len(input.Messages))
	}
}

func TestSummarizeWithLLM显式配的摘要路由最优先(t *testing.T) {
	t.Parallel()

	// 配摘要路由的全部意义就是「用一个更便宜的模型写摘要」，它压得住另外两档。
	stream := textStream("摘要")
	live := liveSession(t, "s-1", llm.CallConfig{Provider: "anthropic", Model: "big"})

	result, err := SummarizeWithLLM(t.Context(), stream,
		Policy{Summarization: Target{Provider: "openai", Model: "cheap"}},
		SummarizationInput{}, agentAt(live, "google", "other"))
	if err != nil {
		t.Fatalf("这次总结该成：%v", err)
	}
	if stream.seen.Provider != "openai" || stream.seen.Model != "cheap" {
		t.Fatalf("发去了 %q/%q", stream.seen.Provider, stream.seen.Model)
	}
	if result.Provider != "openai" || result.Model != "cheap" {
		t.Fatalf("记账记成了 %q/%q", result.Provider, result.Model)
	}
}

func TestSummarizeWithLLM没配就跟着最近那份请求头走(t *testing.T) {
	t.Parallel()

	// 第二档是**最近一次真的路由出去的那份请求头**——它也是前缀缓存唯一
	// 对得上的那一个，所以排在 agent 自己那份选项前面。
	stream := textStream("摘要")
	live := liveSession(t, "s-1",
		llm.CallConfig{Provider: "anthropic", Model: "old"},
		llm.CallConfig{Provider: "anthropic", Model: "new"})

	if _, err := SummarizeWithLLM(t.Context(), stream, Policy{}, SummarizationInput{},
		agentAt(live, "google", "other")); err != nil {
		t.Fatalf("这次总结该成：%v", err)
	}
	if stream.seen.Provider != "anthropic" || stream.seen.Model != "new" {
		t.Fatalf("发去了 %q/%q", stream.seen.Provider, stream.seen.Model)
	}
}

func TestSummarizeWithLLM请求头不全时退到agent那档(t *testing.T) {
	t.Parallel()

	// 一份两个字段没填全的请求头不算一条可用的路由：它指不出任何一个模型。
	stream := textStream("摘要")
	live := liveSession(t, "s-1", llm.CallConfig{Provider: "anthropic"})

	if _, err := SummarizeWithLLM(t.Context(), stream, Policy{}, SummarizationInput{},
		agentAt(live, "google", "flash")); err != nil {
		t.Fatalf("这次总结该成：%v", err)
	}
	if stream.seen.Provider != "google" || stream.seen.Model != "flash" {
		t.Fatalf("发去了 %q/%q", stream.seen.Provider, stream.seen.Model)
	}
}

func TestSummarizeWithLLM三档都空时压根不发(t *testing.T) {
	t.Parallel()

	// 挑不出模型时不能拿空的 provider/model 去撞适配器登记表——那条错误
	// 说的是「没有这个提供方」，读的人查不到真正的原因。
	stream := textStream("摘要")
	live := liveSession(t, "s-1")

	_, err := SummarizeWithLLM(t.Context(), stream, Policy{}, SummarizationInput{},
		agentAt(live, "", ""))
	if err == nil || !strings.Contains(err.Error(), "provider/model") {
		t.Fatalf("该报挑不出路由，实际是 %v", err)
	}
	if stream.calls != 0 {
		t.Fatalf("不该发出去，实际发了 %d 次", stream.calls)
	}
}

func TestSummarizeWithLLM请求发不出去时带上原因(t *testing.T) {
	t.Parallel()

	boom := errors.New("适配器登记表里没有这一条")
	stream := &scriptedStream{openErr: boom}
	live := liveSession(t, "s-1")

	_, err := SummarizeWithLLM(t.Context(), stream, Policy{}, SummarizationInput{},
		agentAt(live, "openai", "gpt-x"))
	if !errors.Is(err, boom) {
		t.Fatalf("原本那条失败查不下去了：%v", err)
	}
}

func TestSummarizeWithLLM这一路读断了就是失败(t *testing.T) {
	t.Parallel()

	// 读到一半断掉时手上那半截块是不完整的，拿它当摘要等于把一段截断的历史
	// 落到表面上。
	boom := errors.New("连接断了")
	stream := &scriptedStream{
		chunks: []llm.StreamChunk{llm.TextDeltaChunk{Index: 0, Text: "写了一半"}},
		midErr: boom,
	}
	live := liveSession(t, "s-1")

	_, err := SummarizeWithLLM(t.Context(), stream, Policy{}, SummarizationInput{},
		agentAt(live, "openai", "gpt-x"))
	if !errors.Is(err, boom) {
		t.Fatalf("原本那条失败查不下去了：%v", err)
	}
}

func TestSummarizeWithLLM撞上生成上限算失败(t *testing.T) {
	t.Parallel()

	// 一份被截断的检查点读起来是完整的，而它丢掉的正是末尾那几节。
	stream := &scriptedStream{chunks: []llm.StreamChunk{
		llm.BlockEndChunk{Index: 0, Block: llm.TextBlock{Text: "## Primary Request"}},
		llm.FinishChunk{Reason: llm.MaxTokensFinish{}},
	}}
	live := liveSession(t, "s-1")

	_, err := SummarizeWithLLM(t.Context(), stream, Policy{}, SummarizationInput{},
		agentAt(live, "openai", "gpt-x"))
	var failure *llm.Error
	if !errors.As(err, &failure) || failure.Failure.Code != "MAX_TOKENS" {
		t.Fatalf("该报撞上上限，实际是 %v", err)
	}
}

func TestSummarizeWithLLM把终止原因原样交回去(t *testing.T) {
	t.Parallel()

	// 一次被中止的请求要原样保留它的中止原因：上层靠它分出
	// [compaction.ManualErrorCancelled] 和 [compaction.ManualErrorSummary]。
	for name, finish := range map[string]llm.FinishReason{
		"出错":  llm.ErrorFinish{Failure: llm.Failure{Message: "上游 502", Code: "PROVIDER_ERROR"}},
		"被中止": llm.AbortedFinish{Failure: llm.Failure{Message: "用户取消", Code: "ABORTED"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			stream := &scriptedStream{chunks: []llm.StreamChunk{llm.FinishChunk{Reason: finish}}}
			live := liveSession(t, "s-1")

			_, err := SummarizeWithLLM(t.Context(), stream, Policy{}, SummarizationInput{},
				agentAt(live, "openai", "gpt-x"))
			var failure *llm.Error
			if !errors.As(err, &failure) {
				t.Fatalf("该交出一条 llm.Error，实际是 %v", err)
			}
			if failure.Failure.Code == "MAX_TOKENS" {
				t.Fatalf("原因被改写成了 %q", failure.Failure.Code)
			}
		})
	}
}

func TestSummarizeWithLLM输出装不起来时报错(t *testing.T) {
	t.Parallel()

	// 一块开了却没关掉、类型又攒不出来的内容意味着这一路的分块本身是坏的。
	// 把它当成「这次没有可见文本」会让一段真历史被换成一份残缺的摘要。
	stream := &scriptedStream{chunks: []llm.StreamChunk{
		llm.BlockStartChunk{Index: 0, BlockType: llm.BlockImage},
		llm.FinishChunk{Reason: llm.StopFinish{}},
	}}
	live := liveSession(t, "s-1")

	_, err := SummarizeWithLLM(t.Context(), stream, Policy{}, SummarizationInput{},
		agentAt(live, "openai", "gpt-x"))
	if err == nil || !strings.Contains(err.Error(), "装不起来") {
		t.Fatalf("该报装不起来，实际是 %v", err)
	}
}

func TestSummarizeWithLLM带图的输出整个拒掉(t *testing.T) {
	t.Parallel()

	// 那份摘要要变成一条**落在表面上的用户消息**，一张图在那个位置会被无限次
	// 重发，而且下游那些只认文字的路径会静默丢掉它。
	stream := &scriptedStream{chunks: []llm.StreamChunk{
		llm.BlockEndChunk{Index: 0, Block: llm.TextBlock{Text: "摘要"}},
		llm.BlockEndChunk{Index: 1, Block: llm.ImageBlock{Attachment: attachment.ImageRef{}}},
		llm.FinishChunk{Reason: llm.StopFinish{}},
	}}
	live := liveSession(t, "s-1")

	_, err := SummarizeWithLLM(t.Context(), stream, Policy{}, SummarizationInput{},
		agentAt(live, "openai", "gpt-x"))
	var failure *llm.Error
	if !errors.As(err, &failure) || failure.Failure.Code != "UNSUPPORTED_CONTENT" {
		t.Fatalf("该拒掉带图的输出，实际是 %v", err)
	}
}

func TestSummarizeWithLLM一个字都没有算失败(t *testing.T) {
	t.Parallel()

	// 一份全空白的检查点会把被遮的那一整段历史换成什么都没有。
	stream := &scriptedStream{chunks: []llm.StreamChunk{
		llm.BlockEndChunk{Index: 0, Block: llm.TextBlock{Text: "  \n\t "}},
		llm.FinishChunk{Reason: llm.StopFinish{}},
	}}
	live := liveSession(t, "s-1")

	_, err := SummarizeWithLLM(t.Context(), stream, Policy{}, SummarizationInput{},
		agentAt(live, "openai", "gpt-x"))
	if err == nil || !strings.Contains(err.Error(), "一个字的摘要都没产出") {
		t.Fatalf("该报一个字都没有，实际是 %v", err)
	}
}

func TestSummarizeWithLLM只把文字留进摘要(t *testing.T) {
	t.Parallel()

	// 推理内容是模型的草稿，工具调用在这个位置更是无从执行——两样都不该
	// 跟着摘要一路重发下去，但它们仍然要原样留在 RawOutput 里。
	stream := &scriptedStream{chunks: []llm.StreamChunk{
		llm.BlockEndChunk{Index: 0, Block: llm.ReasoningBlock{Text: "我先想想"}},
		llm.BlockEndChunk{Index: 1, Block: llm.TextBlock{Text: "## Primary Request"}},
		llm.FinishChunk{Reason: llm.StopFinish{}},
	}}
	live := liveSession(t, "s-1")

	result, err := SummarizeWithLLM(t.Context(), stream, Policy{}, SummarizationInput{},
		agentAt(live, "openai", "gpt-x"))
	if err != nil {
		t.Fatalf("这次总结该成：%v", err)
	}
	if len(result.Summary) != 1 {
		t.Fatalf("摘要留下了 %d 块：%+v", len(result.Summary), result.Summary)
	}
	if text := result.Summary[0].(llm.TextBlock); text.Text != "## Primary Request" {
		t.Fatalf("留下的是 %q", text.Text)
	}
	if len(result.RawOutput) != 2 {
		t.Fatalf("原始输出被裁成了 %d 块", len(result.RawOutput))
	}
}

func TestSummarizeWithLLM用量报了才记(t *testing.T) {
	t.Parallel()

	// 没报用量和报了一份全零的用量是两件事：后者会让计价那一层以为这次
	// 压缩不花钱。
	live := liveSession(t, "s-1")

	quiet, err := SummarizeWithLLM(t.Context(), textStream("摘要"), Policy{},
		SummarizationInput{}, agentAt(live, "openai", "gpt-x"))
	if err != nil {
		t.Fatalf("这次总结该成：%v", err)
	}
	if quiet.Usage != nil {
		t.Fatalf("没报用量却记了一份：%+v", quiet.Usage)
	}

	loud := &scriptedStream{chunks: []llm.StreamChunk{
		llm.BlockEndChunk{Index: 0, Block: llm.TextBlock{Text: "摘要"}},
		llm.UsageChunk{Usage: llm.TokenUsage{InputTokens: 900, OutputTokens: 120}},
		llm.FinishChunk{Reason: llm.StopFinish{}},
	}}
	result, err := SummarizeWithLLM(t.Context(), loud, Policy{}, SummarizationInput{},
		agentAt(live, "openai", "gpt-x"))
	if err != nil {
		t.Fatalf("这次总结该成：%v", err)
	}
	if result.Usage == nil || result.Usage.InputTokens != 900 || result.Usage.OutputTokens != 120 {
		t.Fatalf("用量记成了 %+v", result.Usage)
	}
}

func TestCompactionInstruction认得上一份检查点(t *testing.T) {
	t.Parallel()

	// 那条指令里必须带着 [summaryOpenTag]，下一次总结才认得出「上面那段是一份
	// 更早的检查点」——认不出来就会把它整段抄下去，一次压缩等于没压。
	if !strings.Contains(compactionInstruction, summaryOpenTag) {
		t.Fatal("那条指令里没有开标签")
	}
	// 它作为最后一条**用户消息**发出去，而不是另起一个总结专用的系统提示词，
	// 所以它得自己说清楚身份。
	if !strings.HasPrefix(compactionInstruction, "You are now acting as a compaction engine") {
		t.Fatalf("那条指令的开头被改了：%.60q", compactionInstruction)
	}
}
