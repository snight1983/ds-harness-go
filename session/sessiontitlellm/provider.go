// 本文件的作用：那个模型标题生成器本身——它怎么挑消息、怎么装帧、怎么发一次
// 辅助调用、以及怎么验模型吐回来的东西。
//
// 源: packages/session/session-title-llm/src/index.ts:145-294

package sessiontitlellm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"ds-harness-go/llm"
	"ds-harness-go/session/sessiontitle"
)

const (
	// FirstPromptID 是「只拿第一条人类消息起名」那一档的登记 id。
	//
	// 源: packages/session/session-title-first-prompt-llm/src/index.ts:11
	//
	// 它逐字照搬 DSH 那个插件名：这个 id 会被写进标题事件的 source.provider 里，
	// 改它等于改已经写下去的历史的读法。
	FirstPromptID sessiontitle.ProviderID = "session-title-first-prompt-llm"

	// AllPromptsID 是「每来一条消息都重起一次」那一档的登记 id。
	//
	// 源: packages/session/session-title-all-prompts-llm/src/index.ts:11
	AllPromptsID sessiontitle.ProviderID = "session-title-all-prompts-llm"
)

// Provider 是一个由模型产出标题的 [sessiontitle.Provider]。
//
// 源: packages/session/session-title-llm/src/index.ts:153-169
//
// 新增: DSH 那个 registerSessionTitleLlmProvider 顺手就把自己登记进 ctx.sessionTitle
// 了。Go 这边构造和登记是两件事——本包只造出这个值，装配方自己拿去
// [sessiontitle.Service.Register]，并且自己拿着那个注销函数。理由是本包不该知道
// 服务的生命周期，而登记的那一头需要在装配拆掉时把它摘下来。
type Provider struct {
	id             sessiontitle.ProviderID
	automatic      sessiontitle.AutomaticMode
	runtime        Streamer
	config         Config
	selectMessages MessageSelector
}

// New 造一个模型标题生成器。
//
// 源: packages/session/session-title-llm/src/index.ts:153-169
func New(
	id sessiontitle.ProviderID,
	automatic sessiontitle.AutomaticMode,
	runtime Streamer,
	config Config,
	selectMessages MessageSelector,
) (*Provider, error) {
	if id == "" {
		return nil, fmt.Errorf("%w：生成器 id 不能是空串", ErrInvalidConfig)
	}
	if automatic != sessiontitle.ModeFirstPrompt && automatic != sessiontitle.ModeAllPrompts {
		return nil, fmt.Errorf("%w：不认识的自动节奏 %q", ErrInvalidConfig, automatic)
	}
	if runtime == nil {
		return nil, fmt.Errorf("%w：模型运行时不能为 nil", ErrInvalidConfig)
	}
	if selectMessages == nil {
		return nil, fmt.Errorf("%w：挑消息那个函数不能为 nil", ErrInvalidConfig)
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Provider{
		id:             id,
		automatic:      automatic,
		runtime:        runtime,
		config:         config,
		selectMessages: selectMessages,
	}, nil
}

// NewFirstPrompt 造那一档「只拿第一条人类消息起名」的生成器。
//
// 源: packages/session/session-title-first-prompt-llm/src/index.ts:33-40
//
// 这一档配 [sessiontitle.ModeFirstPrompt]：会话在第一条消息上起一次名就定下来，
// 后面聊到哪儿名字都不变。
func NewFirstPrompt(runtime Streamer, config Config) (*Provider, error) {
	return New(FirstPromptID, sessiontitle.ModeFirstPrompt, runtime, config,
		func(messages []sessiontitle.UserMessage) []sessiontitle.UserMessage {
			if len(messages) == 0 {
				return nil
			}
			return messages[:1]
		})
}

// NewAllPrompts 造那一档「每来一条消息都拿全部消息重起一次」的生成器。
//
// 源: packages/session/session-title-all-prompts-llm/src/index.ts:33-35
//
// 这一档配 [sessiontitle.ModeAllPrompts]：话题跑偏之后名字跟着改，代价是每来一条
// 用户消息就多一次辅助调用。
func NewAllPrompts(runtime Streamer, config Config) (*Provider, error) {
	return New(AllPromptsID, sessiontitle.ModeAllPrompts, runtime, config,
		func(messages []sessiontitle.UserMessage) []sessiontitle.UserMessage { return messages })
}

// ID 实现 [sessiontitle.Provider]。
func (p *Provider) ID() sessiontitle.ProviderID { return p.id }

// Automatic 实现 [sessiontitle.Provider]。
func (p *Provider) Automatic() sessiontitle.AutomaticMode { return p.automatic }

// Generate 发一次辅助模型调用，把它的输出变成一个标题。
//
// 源: packages/session/session-title-llm/src/index.ts:229-294
func (p *Provider) Generate(
	ctx context.Context,
	request sessiontitle.ProviderRequest,
) (sessiontitle.ProviderResult, error) {
	if err := ctx.Err(); err != nil {
		return sessiontitle.ProviderResult{}, context.Cause(ctx)
	}

	selected := p.selectMessages(request.Messages)
	if len(selected) == 0 {
		return sessiontitle.ProviderResult{}, fmt.Errorf("sessiontitlellm: 至少要有一条源消息才起得了名")
	}
	framed, err := frameMessages(selected)
	if err != nil {
		return sessiontitle.ProviderResult{}, err
	}
	if len(framed) > p.config.MaxInputBytes {
		return sessiontitle.ProviderResult{}, fmt.Errorf(
			"sessiontitlellm: 装帧后的输入是 %d 字节，超过 MaxInputBytes %d",
			len(framed), p.config.MaxInputBytes)
	}
	route, err := p.resolveRoute(request)
	if err != nil {
		return sessiontitle.ProviderResult{}, err
	}

	messages := []llm.Message{llm.NewUserMessage(
		llm.Content{llm.TextBlock{Text: framed}},
		llm.PluginSource{Plugin: PluginName},
	)}
	system := p.systemPrompt()
	seqs := make([]int, 0, len(selected))
	for _, message := range selected {
		seqs = append(seqs, message.Seq)
	}

	// 期限从这里开始算，把它盖在派发和整趟读流上。
	callCtx, cancel := context.WithTimeoutCause(ctx, p.config.Timeout, ErrTimeout)
	defer cancel()

	// 记录落在派发**之前**：一次超时或者断流同样留得下这条记录，而那恰恰是最
	// 需要复盘的几次。
	if err := request.Session.Append(EventTitleLLMRequest, RequestEventData{
		TitleProvider: p.id,
		MessageSeqs:   seqs,
		Route:         route,
		System:        system,
		Messages:      messages,
		MaxTokens:     p.config.MaxOutputTokens,
	}); err != nil {
		return sessiontitle.ProviderResult{}, fmt.Errorf("sessiontitlellm: 请求记录写不进日志：%w", err)
	}

	text, err := p.collectText(callCtx, llm.GenerateOptions{
		Provider:  route.Provider,
		Model:     route.Model,
		Messages:  messages,
		System:    system,
		MaxTokens: p.config.MaxOutputTokens,
		SessionID: llm.SessionID(request.Session.ID()),
		Purpose:   llm.PurposeSessionTitle,
	})
	if err != nil {
		return sessiontitle.ProviderResult{}, err
	}

	// 这里**不设**字节上限：服务自己会按它那份 MaxTitleBytes 再压一次，本包
	// 在这儿多截一刀只会让两个上限有机会对不上。
	title := sessiontitle.NormalizeSessionTitle(text, 0)
	if title == "" {
		return sessiontitle.ProviderResult{}, fmt.Errorf("sessiontitlellm: 模型没吐出任何可用文本")
	}
	used := route
	return sessiontitle.ProviderResult{Title: title, MessageSeqs: seqs, Model: &used}, nil
}

// collectText 跑完一趟流，交出模型吐出来的全部文本。
//
// 源: packages/session/session-title-llm/src/index.ts:270-286
func (p *Provider) collectText(ctx context.Context, options llm.GenerateOptions) (string, error) {
	stream, err := p.runtime.Stream(ctx, options)
	if err != nil {
		return "", fmt.Errorf("sessiontitlellm: 辅助调用发不出去：%w", err)
	}

	assembler := llm.NewBlockAssembler()
	for chunk, chunkErr := range stream {
		if chunkErr != nil {
			return "", fmt.Errorf("sessiontitlellm: 读辅助调用的流失败：%w", chunkErr)
		}
		// 每一块都问一次期限：一条不停往外吐的流不会自己停下来。
		if ctx.Err() != nil {
			return "", context.Cause(ctx)
		}
		assembler.Push(chunk)
	}
	if ctx.Err() != nil {
		return "", context.Cause(ctx)
	}
	if err := finishError(assembler.Finish()); err != nil {
		return "", err
	}

	blocks, err := assembler.Blocks()
	if err != nil {
		return "", fmt.Errorf("sessiontitlellm: 辅助调用的输出装配不起来：%w", err)
	}
	var parts []string
	for _, block := range blocks {
		switch typed := block.(type) {
		case llm.TextBlock:
			parts = append(parts, typed.Text)
		case llm.ToolCallBlock:
			// 起名这件事没有工具可用。一个要调工具的模型说明这次请求被路由到了
			// 一个带工具的配置上，那条路上出来的任何东西都不该被当成标题。
			return "", fmt.Errorf("sessiontitlellm: 标题输出里不该有工具调用")
		}
	}
	return strings.Join(parts, " "), nil
}

// resolveRoute 定这次走哪条路由：显式配的那一对，或者日志里记着的那一条。
//
// 源: packages/session/session-title-llm/src/index.ts:172-183
func (p *Provider) resolveRoute(request sessiontitle.ProviderRequest) (sessiontitle.ModelProvenance, error) {
	if p.config.Provider != "" && p.config.Model != "" {
		return sessiontitle.ModelProvenance{Provider: p.config.Provider, Model: p.config.Model}, nil
	}
	if request.Route == nil {
		return sessiontitle.ModelProvenance{}, fmt.Errorf(
			"sessiontitlellm: 日志里还没有记过任何一条路由，请把 Provider 和 Model 一起配上")
	}
	return *request.Route, nil
}

// systemPrompt 是两档共用的那份系统提示词。
//
// 源: packages/session/session-title-llm/src/index.ts:186-193
//
// 它逐字照搬 DSH。提示词的措辞是被模型行为反复调出来的，任何一处「读起来更顺」
// 的改写都可能换来另一种输出，而那件事只有跑过才知道。
func (p *Provider) systemPrompt() string {
	return strings.Join([]string{
		"Create a concise title for an AI coding-assistant session from the supplied human messages.",
		"Return only the title on one line, **in plain text of natural language**, " +
			"with no quotes, prefix, explanation, Markdown, XML, or terminal control codes. No code is allowed.",
		"Use the language of the messages.",
		fmt.Sprintf("Aim for about %d words in non-CJK languages or %d CJK characters.",
			p.config.TargetWords, p.config.TargetCJKCharacters),
	}, "\n")
}

// frameMessages 把选中的那几条消息装成 JSON，好让用户的文本伪造不了结构。
//
// 源: packages/session/session-title-llm/src/index.ts:196-198
//
// 新增: 走 [encoding/json.Encoder] 并关掉 HTML 转义，而不是直接 json.Marshal。
// Go 的 Marshal 默认把 < > & 转成 < 这类转义，那是给「JSON 直接嵌进 HTML」
// 准备的自卫，在这里没有意义，只会让模型看到一堆和用户打的字对不上的转义序列。
// 关掉之后排出来的字节和 DSH 的 JSON.stringify 逐字一致。
func frameMessages(messages []sessiontitle.UserMessage) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(messages); err != nil {
		return "", fmt.Errorf("sessiontitlellm: 消息排不成 JSON：%w", err)
	}
	// Encode 会在末尾添一个换行，这里不要它。
	framed := strings.TrimSuffix(buffer.String(), "\n")
	return "Generate the session title from this JSON array of human messages:\n" + framed, nil
}

// finishError 把一个终止原因翻成这次辅助调用的失败；正常收尾时是 nil。
//
// 源: packages/session/session-title-llm/src/index.ts:201-218
func finishError(finish llm.FinishReason) error {
	switch typed := finish.(type) {
	case llm.StopFinish:
		return nil
	case llm.ErrorFinish:
		return &llm.Error{Failure: typed.Failure}
	case llm.AbortedFinish:
		return &llm.Error{Failure: typed.Failure}
	case llm.MaxTokensFinish:
		return fmt.Errorf("sessiontitlellm: 标题输出撞上了 MaxOutputTokens")
	case llm.ToolCallsFinish:
		return fmt.Errorf("sessiontitlellm: 标题模型意外地要调工具")
	default:
		return fmt.Errorf("sessiontitlellm: 不支持的结束原因 %q", finish.FinishKind())
	}
}
