// 本文件的作用：把 harness 的请求历史翻成 OpenAI 兼容线上协议的那份消息序列，
// 把工具 schema 一字不差地搬过去，并在图片负载超过路由上限时确定性地把最老的
// 那几张换成占位文字。
//
// 源: packages/llm/llm-pi-ai/src/context.ts

package openaicompat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared"
	"golang.org/x/sync/errgroup"

	"github.com/snight1983/ds-harness-go/attachment"
	"github.com/snight1983/ds-harness-go/llm"
)

// noToolOutput 是一条什么都没输出的工具结果在线上的写法。
//
// 源: packages/llm/llm-pi-ai/src/context.ts:163、277
//
// 线上协议要求一条 tool 消息**必须**有 content，而一次成功但没有输出的调用
// （比如一个只做副作用的工具）拿不出任何字节。空串会被一部分网关判成缺字段，
// 所以这里给一句模型读得懂的话，而不是留空。
const noToolOutput = "(no output)"

// requestContext 是从一次请求的历史里推出来的那两半：线上消息序列，以及工具声明。
//
// 源: packages/llm/llm-pi-ai/src/context.ts:127-134
//
// 新增: DSH 交出的是 pi-ai 的 Context，那里面 systemPrompt 是**和 messages 平行的
// 一个槽**。OpenAI 兼容协议没有这个槽——系统提示词就是消息序列最前面的一条
// system 消息，所以它在这里已经被折进 messages，不再是一个单独的字段。
type requestContext struct {
	// messages 是要发出去的完整消息序列，含最前面那条系统提示。
	messages []openai.ChatCompletionMessageParamUnion
	// tools 是工具声明；这次请求没声明工具时为 nil，于是请求体里整个 tools 字段
	// 不出现——一部分网关会把一个空数组当成「显式禁用工具」。
	tools []openai.ChatCompletionToolUnionParam
}

// flattenText 把一条消息里的文本块接起来。
//
// 源: packages/llm/llm-pi-ai/src/context.ts:21-26
func flattenText(message llm.Message) string {
	var builder strings.Builder
	for _, block := range message.Content {
		if text, ok := block.(llm.TextBlock); ok {
			builder.WriteString(text.Text)
		}
	}
	return builder.String()
}

// toolResultText 递归地把一条工具结果里的文本接起来。
//
// 源: packages/llm/llm-pi-ai/src/context.ts:29-34
//
// 要递归是因为一个工具可以把另一次调用的结果原样转发出来，那份内容会以嵌套的
// [llm.ToolResultBlock] 的形式留在里面；不递归的话那段文字在线上会整个消失。
func toolResultText(blocks llm.Content) string {
	var builder strings.Builder
	for _, block := range blocks {
		switch typed := block.(type) {
		case llm.TextBlock:
			builder.WriteString(typed.Text)
		case llm.ToolResultBlock:
			builder.WriteString(toolResultText(typed.Content))
		}
	}
	return builder.String()
}

// assertSupportedImageRoles 拒掉这条协议表示不了的图片位置。
//
// 源: packages/llm/llm-pi-ai/src/context.ts:36-46
//
// 在卸载之前拒，是因为卸载只会把**多出来**的那部分图换成文字：一条 assistant
// 消息里的图如果没超预算就会原样留到线上，然后被提供方在半个回合中间拒掉。
// 那时候消息已经落库了，会话会一直重复一次不可能成功的请求。
func assertSupportedImageRoles(messages []llm.Message) error {
	for _, message := range messages {
		if message.Role != llm.RoleUser && llm.ContentHasImage(message.Content) {
			return llm.NewError(
				fmt.Sprintf("openai-compatible chat completions cannot represent an image in an in-history %s message", message.Role),
				"UNSUPPORTED_CONTENT", nil)
		}
	}
	return nil
}

// imageParts 交出一张请求版本图在线上的那两块：先是把手文字，然后是字节本身。
//
// 源: packages/llm/llm-pi-ai/src/context.ts:58-67
//
// 把手文字必须紧挨在图前面：模型靠它把「这张图」和会话里别处提到的那个附件对上，
// 也靠它在这张图之后被卸成占位文字时仍然认得出说的是同一张。
func imageParts(version attachment.RequestImage) []openai.ChatCompletionContentPartUnionParam {
	return []openai.ChatCompletionContentPartUnionParam{
		openai.TextContentPart(llm.RequestImageHandleText(version)),
		openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
			// 线上协议只有 url 这一个口子收图，内联字节走的是 data URL。
			// 新增: DSH 那边 pi-ai 的 ImageContent 是 { data, mimeType } 两个字段，
			// 由那个库自己拼 data URL；这边没有那个库，所以拼在这里。
			URL: "data:" + string(version.MediaType) + ";base64," +
				base64.StdEncoding.EncodeToString(version.Data),
		}),
	}
}

// userParts 把一条用户消息的内容翻成线上的内容分块。
//
// 源: packages/llm/llm-pi-ai/src/context.ts:48-85
//
// images 是这次请求已经解算好的那些请求版本图，按附件 id 索引。
func userParts(
	blocks llm.Content,
	images map[attachment.ID]attachment.RequestImage,
) []openai.ChatCompletionContentPartUnionParam {
	var parts []openai.ChatCompletionContentPartUnionParam
	for _, block := range blocks {
		switch typed := block.(type) {
		case llm.TextBlock:
			// 空文本块不写：它在线上是一个什么都不说的分块，而一部分网关会拒掉
			// 内容为空串的分块。
			if typed.Text != "" {
				parts = append(parts, openai.TextContentPart(typed.Text))
			}
		case llm.ImageBlock:
			version, prepared := images[typed.Attachment.ID]
			if !prepared {
				// 走不到：第二趟卸载只会**移除**图，不会新增，所以留到这里的每一张
				// 都在第一趟里被读过了。真走到了也不能让这张图无声消失——退回到
				// 纯文本口径的那句描述，内容还在，只是模型看不到像素。
				parts = append(parts, openai.TextContentPart(llm.TextOnlyImageText(typed.Attachment)))
				continue
			}
			parts = append(parts, imageParts(version)...)
		case llm.ToolResultBlock:
			// 嵌套的工具结果：内容展平进当前这条消息，理由同 [toolResultText]。
			parts = append(parts, userParts(typed.Content, images)...)
		}
		// 别的块（推理、以后合并进来的新块）不是用户输入的词汇，不写。
		//
		// 源: packages/llm/llm-pi-ai/src/context.ts:78-80
	}
	return parts
}

// collapseText 在所有分块都是文本时把它们塌成一个字符串。
//
// 源: packages/llm/llm-pi-ai/src/context.ts:83
//
// 这不是省事：线上协议里 content 既可以是字符串也可以是分块数组，而相当一部分
// 本地推理服务器（以及老网关）的聊天模板只处理字符串那一支，收到一个只装着
// 一块文本的数组会拼出空提示词。发字符串是两边都认的那一种写法。
func collapseText(parts []openai.ChatCompletionContentPartUnionParam) (string, bool) {
	var builder strings.Builder
	for _, part := range parts {
		if part.OfText == nil {
			return "", false
		}
		builder.WriteString(part.OfText.Text)
	}
	return builder.String(), true
}

// userMessage 用一串内容分块造一条用户消息，全是文本时塌成字符串。
func userMessage(parts []openai.ChatCompletionContentPartUnionParam) openai.ChatCompletionMessageParamUnion {
	if text, only := collapseText(parts); only {
		return openai.UserMessage(text)
	}
	return openai.UserMessage(parts)
}

// toolResultParts 把一条工具结果拆成「tool 消息装得下的文字」和「装不下的图」。
//
// 源: packages/llm/llm-pi-ai/src/context.ts:270-282
//
// 新增: DSH 那边一条 pi-ai 的 toolResult 消息的 content 和用户消息是同一种东西，
// 图片可以直接留在里面。OpenAI 兼容协议不是：tool 消息的 content 只收字符串或者
// 纯文本分块数组，没有任何位置放得下一张图。所以这里把两者分开——文字留在
// tool 消息里（它才是和 tool_call_id 对上的那条），图另起一条 user 消息跟在后面。
// 丢掉图不是选项：一个截图工具的输出**就是**那张图。
func toolResultParts(
	blocks llm.Content,
	images map[attachment.ID]attachment.RequestImage,
) (text string, pictures []openai.ChatCompletionContentPartUnionParam) {
	var builder strings.Builder
	for _, block := range blocks {
		switch typed := block.(type) {
		case llm.TextBlock:
			builder.WriteString(typed.Text)
		case llm.ImageBlock:
			version, prepared := images[typed.Attachment.ID]
			if !prepared {
				builder.WriteString(llm.TextOnlyImageText(typed.Attachment))
				continue
			}
			pictures = append(pictures, imageParts(version)...)
		case llm.ToolResultBlock:
			nestedText, nestedPictures := toolResultParts(typed.Content, images)
			builder.WriteString(nestedText)
			pictures = append(pictures, nestedPictures...)
		}
	}
	return builder.String(), pictures
}

// collectImageRefs 按首次出现的次序收集内容里引到的每一张图，去重。
//
// 源: packages/llm/llm-pi-ai/src/context.ts:87-95
//
// 新增: DSH 用一个 Map 同时做去重和保序（JS 对象记得插入序）。Go 的 map 没有次序，
// 所以拆成一张 seen 表加一个切片。次序要稳，是因为它决定了读附件的并发次序，
// 而一份诊断里的读取失败该点名哪一张图，两次跑得给出同一个答案。
func collectImageRefs(
	blocks llm.Content,
	seen map[attachment.ID]struct{},
	ordered []attachment.ImageRef,
) []attachment.ImageRef {
	for _, block := range blocks {
		switch typed := block.(type) {
		case llm.ImageBlock:
			if _, duplicate := seen[typed.Attachment.ID]; duplicate {
				continue
			}
			seen[typed.Attachment.ID] = struct{}{}
			ordered = append(ordered, typed.Attachment)
		case llm.ToolResultBlock:
			ordered = collectImageRefs(typed.Content, seen, ordered)
		}
	}
	return ordered
}

// prepareRequestImages 把历史里引到的每一张图按这条路由的预算重新编码一遍。
//
// 源: packages/llm/llm-pi-ai/src/context.ts:97-114
//
// 新增: DSH 用 Promise.all 并发读。Go 这边用 [errgroup.Group]——它自带
// 「第一条错误就取消其余」的语义，正是 Promise.all 的行为，不必自己写一遍
// WaitGroup 加 error channel 加 context 取消。
func prepareRequestImages(
	ctx context.Context,
	messages []llm.Message,
	store attachment.Store,
	policy attachment.RequestPolicy,
) (map[attachment.ID]attachment.RequestImage, error) {
	seen := make(map[attachment.ID]struct{})
	var ordered []attachment.ImageRef
	for _, message := range messages {
		ordered = collectImageRefs(message.Content, seen, ordered)
	}
	if len(ordered) == 0 {
		return nil, nil
	}

	prepared := make([]attachment.RequestImage, len(ordered))
	group, groupCtx := errgroup.WithContext(ctx)
	for index, ref := range ordered {
		group.Go(func() error {
			version, err := attachment.ReadImageRequest(groupCtx, store, ref, policy)
			if err != nil {
				return err
			}
			// 每个 goroutine 只写自己那一格，不需要锁。
			prepared[index] = version
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}

	versions := make(map[attachment.ID]attachment.RequestImage, len(ordered))
	for index, ref := range ordered {
		versions[ref.ID] = prepared[index]
	}
	return versions, nil
}

// toolsOf 把请求里的工具 schema 翻成线上的工具声明。
//
// 源: packages/llm/llm-pi-ai/src/context.ts:116-124
//
// 新增: [llm.ToolSchema].Parameters 是一段 [json.RawMessage]，而 openai-go 的
// FunctionDefinitionParam.Parameters 是 map[string]any——解进那个 map 再排回去会
// 按字典序重排键，于是 required 的次序、properties 的次序都变了。那两处次序会被
// 一部分提供方原样回显，也会进提示词缓存的键，所以这里改用 [param.SetJSON]：
// 整条函数声明的 JSON 由本包自己拼，schema 那段字节原样穿过去。
func toolsOf(options llm.GenerateOptions) ([]openai.ChatCompletionToolUnionParam, error) {
	if len(options.Tools) == 0 {
		return nil, nil
	}
	tools := make([]openai.ChatCompletionToolUnionParam, 0, len(options.Tools))
	for _, tool := range options.Tools {
		payload := struct {
			Name        string          `json:"name"`
			Description string          `json:"description,omitempty"`
			Parameters  json.RawMessage `json:"parameters,omitempty"`
		}{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters}
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, llm.NewError(
				fmt.Sprintf("tool %q has an unserializable schema: %v", tool.Name, err),
				"UNSUPPORTED_CONTENT", err)
		}
		var function shared.FunctionDefinitionParam
		param.SetJSON(raw, &function)
		tools = append(tools, openai.ChatCompletionFunctionTool(function))
	}
	return tools, nil
}

// assistantMessage 把一条落库的助手消息翻成线上的那一条。
//
// 源: packages/llm/llm-pi-ai/src/replay.ts:224-249（DSH 的 toPiAssistant）
//
// 新增: DSH 在这里把重放信封里的每块签名贴回每一块内容上——那是 Anthropic 那条
// 协议要求原样回发的东西。OpenAI 兼容协议上一条助手历史消息只有 role/content/
// tool_calls 三样，全都从落库的内容里直接得到，所以这里**只读落库内容**，
// 重放状态只用来交出那句降级诊断，理由见 [ReplayStateOf]。
//
// 推理块整个不写：没有任何 OpenAI 兼容端点收得回自己吐出来的 reasoning_content，
// 发过去的下场是被忽略或者被判成非法字段。
//
// 第二个返回值为 false 表示这条消息在线上没有内容可发（既没有文字也没有工具调用，
// 比如一条只剩推理的助手回合）。这种消息不能发成一条空的 assistant——线上协议要求
// content 和 tool_calls 至少有一样，缺了会被拒。
func assistantMessage(
	message llm.Message,
	onReplayDegrade func(reason string),
) (openai.ChatCompletionMessageParamUnion, bool) {
	if _, _, degraded := ReplayStateOf(message); degraded != "" && onReplayDegrade != nil {
		onReplayDegrade(degraded)
	}

	var text strings.Builder
	var calls []openai.ChatCompletionMessageToolCallUnionParam
	for _, block := range message.Content {
		switch typed := block.(type) {
		case llm.TextBlock:
			text.WriteString(typed.Text)
		case llm.ToolCallBlock:
			calls = append(calls, openai.ChatCompletionMessageToolCallUnionParam{
				OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
					ID: string(typed.ID),
					Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
						Name: typed.Name,
						// Arguments 是模型写的那串原文，不解析也不重排：解一遍再
						// 排回去会改键序，而这串字节要和它对应的那条工具结果一起
						// 构成提供方眼里同一次调用。
						Arguments: typed.Arguments,
					},
				},
			})
		}
	}

	if text.Len() == 0 && len(calls) == 0 {
		return openai.ChatCompletionMessageParamUnion{}, false
	}
	assistant := openai.ChatCompletionAssistantMessageParam{ToolCalls: calls}
	if text.Len() > 0 {
		assistant.Content.OfString = param.NewOpt(text.String())
	}
	return openai.ChatCompletionMessageParamUnion{OfAssistant: &assistant}, true
}

// toolResultMessages 交出一条工具结果在线上占的那一条或两条消息。
//
// 源: packages/llm/llm-pi-ai/src/context.ts:270-282
//
// 新增: DSH 的 pi-ai toolResult 消息带 toolName 和 isError 两个字段，这边一个都没有。
// toolName 不需要：线上靠 tool_call_id 单独一个字段就把结果和调用对上了，所以
// DSH 那张「从前面的助手工具调用里把名字找回来」的表连同它的 'unknown' 兜底
// 一起消失。isError 没有槽：一次失败的调用，它的失败文字**就是**内容本身
// （[llm.NewToolResultMessage] 就是这么装的），模型读的是那段文字。
func toolResultMessages(
	result llm.ToolResultBlock,
	images map[attachment.ID]attachment.RequestImage,
) []openai.ChatCompletionMessageParamUnion {
	text, pictures := toolResultParts(result.Content, images)
	if text == "" && len(pictures) == 0 {
		text = noToolOutput
	}
	messages := []openai.ChatCompletionMessageParamUnion{
		openai.ToolMessage(text, string(result.ToolCallID)),
	}
	if len(pictures) > 0 {
		messages = append(messages, openai.UserMessage(pictures))
	}
	return messages
}

// convertHistory 把已经卸载完图的历史翻成线上消息序列。
//
// 源: packages/llm/llm-pi-ai/src/context.ts:242-283
//
// 新增: DSH 有两个几乎一样的循环——textOnlyContext（context.ts:136-171）和
// toPiContextWithImages 里那一段。两者的差别只有「图怎么处理」，而在这份重写里
// 那件事整个落在 images 这张表上：纯文本那条路进来时它是空的，而带图那条路
// 进来之前已经把每张图都读好了。于是合成这一个循环，两条路只在**进来之前**分岔。
func convertHistory(
	messages []llm.Message,
	images map[attachment.ID]attachment.RequestImage,
	onReplayDegrade func(reason string),
) []openai.ChatCompletionMessageParamUnion {
	converted := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case llm.RoleSystem:
			// 历史里的系统消息折成用户消息，好把它在序列里的位置保住。
			//
			// 源: packages/llm/llm-pi-ai/src/context.ts:246-251
			//
			// 新增: DSH 的理由是 pi-ai 只有一个 systemPrompt 槽。这条协议其实**收得下**
			// 中途的 system 消息，但收下不等于用得上：相当一部分本地推理服务器的
			// 聊天模板只认最前面那一条 system，多出来的要么被丢掉要么把模板拼坏。
			// 折成用户消息在每一种端点上的行为都是确定的，所以照旧折。
			converted = append(converted, openai.UserMessage(flattenText(message)))
		case llm.RoleAssistant:
			if assistant, sendable := assistantMessage(message, onReplayDegrade); sendable {
				converted = append(converted, assistant)
			}
		default:
			// 用户角色：常规内容合成一条，每条工具结果再各自成条。
			//
			// 源: packages/llm/llm-pi-ai/src/context.ts:261-282
			var regular llm.Content
			var results []llm.ToolResultBlock
			for _, block := range message.Content {
				if result, isResult := block.(llm.ToolResultBlock); isResult {
					results = append(results, result)
					continue
				}
				regular = append(regular, block)
			}
			// 一条只装着工具结果的消息不额外发一条空的用户消息；但一条什么都没有的
			// 用户消息仍然要发出去，否则这个回合在序列里整个消失，角色就不交替了。
			if len(regular) > 0 || len(results) == 0 {
				converted = append(converted, userMessage(userParts(regular, images)))
			}
			for _, result := range results {
				converted = append(converted, toolResultMessages(result, images)...)
			}
		}
	}
	return converted
}

// toContext 把一次请求的历史翻成线上的消息序列和工具声明。
//
// 源: packages/llm/llm-pi-ai/src/context.ts:181-216
//
// attachments 为 nil 选的是纯文本那条路：历史里出现任何一张图都会被拒，因为
// 没有附件服务就拿不到字节。带图那条路会先按 maxRequestImageBytes 把超出的那部分
// 图确定性地换成占位文字，再把剩下的按 imagePolicy 重新编码。
//
// maxRequestImageBytes 为 nil 表示不设上限，每张图都原样留着。imagePolicy 只在
// attachments 非 nil 时读，它的两个预算由 [ResolvedProviderProfile] 保证为正。
//
// onReplayDegrade 为 nil 表示不关心降级；非 nil 时每条带着一份用不了的重放状态的
// 助手消息都会把那句诊断交给它，理由见 [ReplayStateOf]。
func toContext(
	ctx context.Context,
	options llm.GenerateOptions,
	attachments attachment.Store,
	onReplayDegrade func(reason string),
	maxRequestImageBytes *int,
	imagePolicy attachment.RequestPolicy,
) (requestContext, error) {
	tools, err := toolsOf(options)
	if err != nil {
		return requestContext{}, err
	}

	var images map[attachment.ID]attachment.RequestImage
	history := options.Messages
	if attachments == nil {
		// 源: packages/llm/llm-pi-ai/src/context.ts:140-142
		for _, message := range options.Messages {
			if llm.ContentHasImage(message.Content) {
				return requestContext{}, llm.NewError(
					"image conversion requires the durable attachment service", "UNSUPPORTED_CONTENT", nil)
			}
		}
	} else {
		if err := assertSupportedImageRoles(options.Messages); err != nil {
			return requestContext{}, err
		}
		// 两趟卸载。第一趟只能按每张图**编码之前**的字节数估，因为还没读过它们；
		// 估的上界取 min(存下来的字节, 这条路由的单张上限)——重新编码只会变小，
		// 所以这个上界不会漏掉任何一张该留下的图。
		//
		// 源: packages/llm/llm-pi-ai/src/context.ts:229-241
		policy := llm.RequestImageOffloadPolicy{
			Representation: llm.RepresentationBase64,
			MaxBytes:       maxRequestImageBytes,
			// 步长为 1：这里没有「一次少几张」的道理，多卸一个字节都是白丢内容。
			ByteQuantum: 1,
			ByteLength: func(ref attachment.ImageRef) int {
				return min(ref.Bytes, imagePolicy.MaxBytes)
			},
		}
		requestMessages := llm.OffloadRequestImagesWithPolicy(options.Messages, policy)
		images, err = prepareRequestImages(ctx, requestMessages, attachments, imagePolicy)
		if err != nil {
			return requestContext{}, err
		}
		// 第二趟拿的是每张图编码完的**确切**字节数。要跑第二趟，是因为第一趟的
		// 上界可能比实际大不少（一张 1MiB 的 PNG 缩到 200KiB 是常事），只按上界
		// 卸会把明明塞得下的图白白扔掉。
		policy.ByteLength = func(ref attachment.ImageRef) int { return images[ref.ID].Bytes }
		history = llm.OffloadRequestImagesWithPolicy(requestMessages, policy)
	}

	messages := make([]openai.ChatCompletionMessageParamUnion, 0, len(history)+1)
	// 系统提示词是序列最前面那一条，不是一个平行的槽。
	//
	// 源: packages/llm/llm-pi-ai/src/context.ts:130
	//
	// 新增: 用的是 system 而不是 developer。developer 是 OpenAI 自家新模型上的角色，
	// 网关和本地推理服务器普遍不认，而这个适配器服务的正是后者。
	if options.System != "" {
		messages = append(messages, openai.SystemMessage(options.System))
	}
	messages = append(messages, convertHistory(history, images, onReplayDegrade)...)
	return requestContext{messages: messages, tools: tools}, nil
}
