// 本文件的作用：把一台 MCP 服务器报出来的工具造成本装置的工具定义，并且把一代
// 工具原子地换进工具注册表。
//
// 源: packages/mcp/mcp-client/src/tools.ts:120-361

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/llm"
)

// bridgeOptions 是造一代工具时要紧的那几个解算完的选项。
//
// 源: packages/mcp/mcp-client/src/tools.ts:29-35（ToolBridgeOptions）
type bridgeOptions struct {
	// serverName 是这台服务器的命名空间。
	serverName string
	// toolCallTimeout 是单次工具调用的超时。
	toolCallTimeout time.Duration
	// strictRegistration 为真时，换代阶段的注册冲突会让这次同步失败，
	// 而不是回滚到零个工具之后咽下去。
	//
	// 源: packages/mcp/mcp-client/src/tools.ts:31（registrationFailure: 'contain' | 'throw'）
	strictRegistration bool
	// admit 是图片准入接缝，可以为 nil。
	admit ImageAdmission
	// logger 记那条被咽下去的注册失败。
	logger *slog.Logger
}

// toolDisposers 是一代工具的全部注册。
//
// 源: packages/mcp/mcp-client/src/tools.ts:37（ToolDisposers）
//
// 新增: DSH 用一个 Map，靠 JS 的 Map 有插入序。Go 的 map 没有顺序，而这里顺序是
// 要紧的——注册和撤销都得按服务器报出来的顺序走，同一份工具清单才每次都得到
// 同一种结果。所以换成切片。
type toolDisposers []func(context.Context) error

// disposeAll 按注册的**逆序**撤销一代工具，把第一个错误交出去。
//
// 逆序是 Go 里成对资源的惯例；这一代里的注册彼此独立，所以顺序本身不影响结果，
// 但逆序让「撤到一半失败」这件事留下的痕迹和 [scope] 那边一致。
func (d toolDisposers) disposeAll(ctx context.Context) error {
	var first error
	for index := len(d) - 1; index >= 0; index-- {
		if err := d[index](ctx); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// preparedProjection 是为某一次确切的执行预备好的那份富投影。
//
// 源: packages/mcp/mcp-client/src/tools.ts:210-218
type preparedProjection struct {
	// value 是 execute 交出去的那份权威值，落地前不该被人改过。
	value json.RawMessage
	// fallback 是 Output.Render 那份同步投影，也就是收尾前**应当**看到的内容。
	fallback llm.Content
	// content 是带图的、或者明确拒绝了图的那份投影。
	content llm.Content
}

// projectionStore 是「一次执行 → 一份预备好的投影」这张表。
//
// 新增: DSH 用 `WeakMap<ToolExecution, PreparedProjection>`，键是执行对象的身份，
// 靠 GC 回收。Go 没有 WeakMap，但 [github.com/snight1983/ds-harness-go/core/tools.ExecutionToken] 本来
// 就是一个可比较、且全进程唯一的相关性标识，拿它当键就够了；泄漏由「读一次就删」
// 挡住——本包只在 Execute 里写，而管线保证对每一份规范化过的结果恰好调一次
// FinalizeContent，所以每一条要么被读走，要么随着这一代工具一起被撤销。
type projectionStore struct {
	// mutex 护住 entries；一个工具的兄弟调用是可以重叠的。
	mutex chan struct{}
	// entries 是那张表。
	entries map[tools.ExecutionToken]preparedProjection
}

// newProjectionStore 造一张空表。
func newProjectionStore() *projectionStore {
	return &projectionStore{
		mutex:   make(chan struct{}, 1),
		entries: map[tools.ExecutionToken]preparedProjection{},
	}
}

// stage 把一份投影记在这次执行名下。
func (s *projectionStore) stage(token tools.ExecutionToken, projection preparedProjection) {
	s.mutex <- struct{}{}
	defer func() { <-s.mutex }()
	s.entries[token] = projection
}

// take 取走这次执行那份投影，取不到就交出 false。
func (s *projectionStore) take(token tools.ExecutionToken) (preparedProjection, bool) {
	s.mutex <- struct{}{}
	defer func() { <-s.mutex }()
	projection, ok := s.entries[token]
	delete(s.entries, token)
	return projection, ok
}

// syncTools 把一台 MCP 服务器的工具清单同步进本装置的工具注册表。
//
// 源: packages/mcp/mcp-client/src/tools.ts:143-193
//
// 分两步换代：
//
//  1. **取**。把 tools/list 的分页抽干，先在内存里把下一代定义全部造好。这一步
//     失败（网络断了、清单里同一个名字出现两遍、某个工具的入参 schema 说不出口），
//     上一代注册原样留着。
//  2. **换**。先撤上一代，再注册下一代。这一步撞名只可能是别人占了这台服务器的
//     `mcp__<服务器名>__` 命名空间——那就把这一代已经注册进去的全部回滚，
//     让模型要么看见完整的一代、要么一个都看不见。
//
// 新增: DSH 那边「入参 schema 说不出口」是在第 2 步由 ctx.tools.register 抛出来的，
// 于是它走回滚那条路。Go 这边 schema 要先解成 [github.com/snight1983/ds-harness-go/core/tools.Node] 才造得出
// 定义，所以那件事提前到了第 1 步——按 DSH 自己写的分步意图（「凡是不碰注册表就
// 判得了的都在第 1 步判」），这是它本来就该在的位置，而且上一代因此活了下来。
func syncTools(
	ctx context.Context,
	session *sdk.ClientSession,
	registry *tools.Runtime,
	owner *scope.Scope,
	options bridgeOptions,
	previous toolDisposers,
) (toolDisposers, error) {
	// 第 1 步：取。
	var definitions []*tools.Definition
	seen := map[string]bool{}
	cursor := ""
	for {
		response, err := session.ListTools(ctx, &sdk.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, tool := range response.Tools {
			publicName := PublicToolName(options.serverName, tool.Name)
			if seen[publicName] {
				return nil, fmt.Errorf(
					"mcp-client(%s): server listed tool %q more than once — invalid tool list",
					options.serverName, tool.Name)
			}
			seen[publicName] = true
			definition, err := createDefinition(session, publicName, tool, options)
			if err != nil {
				return nil, err
			}
			definitions = append(definitions, definition)
		}
		cursor = response.NextCursor
		if cursor == "" {
			break
		}
	}

	// 第 2 步：换代。
	if err := previous.disposeAll(ctx); err != nil {
		return nil, err
	}
	var disposers toolDisposers
	for _, definition := range definitions {
		dispose, err := registry.Register(ctx, owner, definition)
		if err != nil {
			// 一个 `mcp__<服务器名>__` 打头的名字撞上了，只可能是别人占了这台服务器的
			// 命名空间。回滚，好让模型要么看见完整的一代、要么一个都看不见。
			if rollbackErr := disposers.disposeAll(ctx); rollbackErr != nil {
				options.logger.Error("mcp: 回滚这一代工具时又出错了",
					"serverName", options.serverName, "error", rollbackErr)
			}
			options.logger.Error(fmt.Sprintf(
				"mcp-client(%s): tool registration failed, no tools registered: %v", options.serverName, err))
			if options.strictRegistration {
				return nil, err
			}
			return nil, nil
		}
		disposers = append(disposers, dispose)
	}
	return disposers, nil
}

// createDefinition 造一个这一代局部的工具定义，连同它那份执行局部的富投影。
//
// 源: packages/mcp/mcp-client/src/tools.ts:244-272
func createDefinition(
	session *sdk.ClientSession,
	publicName string,
	tool *sdk.Tool,
	options bridgeOptions,
) (*tools.Definition, error) {
	parameters, err := parseInputSchema(tool, options.serverName)
	if err != nil {
		return nil, err
	}
	structured, hasStructured := supportedOutputSchema(tool.OutputSchema)
	projections := newProjectionStore()
	return &tools.Definition{
		Name:        publicName,
		Description: tool.Description,
		Parameters:  parameters,
		Output:      createOutput(tool.Name, structured, hasStructured),
		Execute:     createExecutor(session, tool.Name, options, projections),
		FinalizeContent: func(exec tools.Execution, result tools.Result) llm.Content {
			projection, ok := projections.take(exec.Token)
			if !ok {
				return nil
			}
			if result.IsError {
				return nil
			}
			// 这两条比对是那份富投影的**准入条件**：只有当落地的值和内容，恰好还是
			// execute 交出去、Render 渲染出来的那两样时，才把图换进去。中间任何一层
			// 包装改过它们，这份投影就说的不是同一件事了，宁可不换。
			if !jsonEqual(result.Value, projection.value) {
				return nil
			}
			if !reflect.DeepEqual(result.Content, projection.fallback) {
				return nil
			}
			return projection.content
		},
	}, nil
}

// parseInputSchema 把对方报的入参 schema 解成本装置说得出的那个子集。
//
// 新增: DSH 直接把 `tool.inputSchema` 当成 `Record<string, unknown>` 往下递，
// 由注册表在登记时验。Go 的 [github.com/snight1983/ds-harness-go/core/tools.Definition.Parameters] 是
// 一个 typed 的 Node，所以这里先解一遍——解不动就是「这个工具的入参说不出口」，
// 让整次同步失败，而不是造一个半截的定义。
func parseInputSchema(tool *sdk.Tool, serverName string) (tools.Node, error) {
	if tool.InputSchema == nil {
		// MCP 规范要求 inputSchema 必填，但这是网络信任边界。缺了就当成
		// 「一个不收参数的对象」——那正是一个没有属性的对象 schema 的意思。
		return tools.Node{Type: tools.TypeObject}, nil
	}
	raw, err := json.Marshal(tool.InputSchema)
	if err != nil {
		return tools.Node{}, fmt.Errorf(
			"mcp-client(%s): tool %q input schema is not JSON: %w", serverName, tool.Name, err)
	}
	node, err := tools.ParseSchema(raw)
	if err != nil {
		return tools.Node{}, fmt.Errorf(
			"mcp-client(%s): tool %q input schema is unsupported: %w", serverName, tool.Name, err)
	}
	return node, nil
}

// supportedOutputSchema 留下一份说得出口的结构化返回值 schema；说不出口的就当没有。
//
// 源: packages/mcp/mcp-client/src/tools.ts:221-229
//
// 当没有的后果是 structuredContent 那一格退回「任意 JSON」，而不是让这个工具注册不上。
// 一个报了本装置不认得的 schema 词汇的服务器，仍然应该是可用的。
func supportedOutputSchema(candidate any) (tools.Node, bool) {
	if candidate == nil {
		return tools.Node{}, false
	}
	raw, err := json.Marshal(candidate)
	if err != nil {
		return tools.Node{}, false
	}
	node, err := tools.ParseSchema(raw)
	if err != nil {
		return tools.Node{}, false
	}
	if err := tools.AssertSupportedSchema(node); err != nil {
		return tools.Node{}, false
	}
	return node, true
}

// createOutput 造那份权威的返回值契约，以及它那份同步的文本投影。
//
// 源: packages/mcp/mcp-client/src/tools.ts:275-291
func createOutput(rawName string, structured tools.Node, hasStructured bool) tools.OutputDefinition {
	deny := false
	required := []string{"content"}
	if hasStructured {
		required = append(required, "structuredContent")
	}
	return tools.OutputDefinition{
		Schema: tools.Node{
			Type: tools.TypeObject,
			Properties: []tools.Property{
				// items 是一个什么都不要求的节点：MCP 的内容块词汇表由对方定义，
				// 本装置只在投影时读它认得的那几个字段，不在这里替它立契约。
				{Name: "content", Schema: tools.Node{Type: tools.TypeArray, Items: &tools.Node{}}},
				{Name: "structuredContent", Schema: structured},
			},
			Required:             required,
			AdditionalProperties: &deny,
		},
		Render: func(_ json.RawMessage, value json.RawMessage) (llm.Content, error) {
			var result Result
			if err := json.Unmarshal(value, &result); err != nil {
				return nil, fmt.Errorf("mcp: 这份结果解不开：%w", err)
			}
			blocks := decodeRawBlocks(result.Content)
			return llm.Content{llm.TextBlock{Text: extractText(blocks, rawName)}}, nil
		},
	}
}

// decodeRawBlocks 把 [Result.Content] 里那几段 JSON 读回本包那份扁平记录。
//
// 新增: Render 是一个纯投影，它拿到的只有值本身（管线在重放时也会调它），
// 所以这里从 JSON 走一遍，而不是指望 execute 那边留下的中间状态。
func decodeRawBlocks(raw []json.RawMessage) []contentBlock {
	blocks := make([]contentBlock, 0, len(raw))
	for _, encoded := range raw {
		var wire struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			MIMEType string `json:"mimeType"`
			Name     string `json:"name"`
			URI      string `json:"uri"`
		}
		if err := json.Unmarshal(encoded, &wire); err != nil {
			// 不是对象的那一块：DSH 在这里落一句「期望一个对象」的诊断，本包同理。
			blocks = append(blocks, contentBlock{Type: unsupportedBlock})
			continue
		}
		blocks = append(blocks, contentBlock{
			Type:      wire.Type,
			Text:      wire.Text,
			MediaType: wire.MIMEType,
			Name:      wire.Name,
			URI:       wire.URI,
		})
	}
	return blocks
}

// createExecutor 造一个 MCP 工具的执行体。
//
// 源: packages/mcp/mcp-client/src/tools.ts:303-361
//
// 它闭包住的是**原名**，发出去的 tools/call 用的也是原名，公开名从不上线。
// 对方回 isError 时这里交回错误，好让管线按「工具失败」那条路给模型一份 isError 结果。
func createExecutor(
	session *sdk.ClientSession,
	rawName string,
	options bridgeOptions,
	projections *projectionStore,
) func(context.Context, json.RawMessage, *tools.RunContext) (json.RawMessage, error) {
	return func(ctx context.Context, args json.RawMessage, exec *tools.RunContext) (json.RawMessage, error) {
		// 模型不听话时参数可能是一个裸字符串或者 null。退回空对象，好让对方自己报出
		// 一条「少了哪个必填参数」的具体错误——那种错误模型学得会。
		arguments := args
		if !isJSONObject(arguments) {
			arguments = json.RawMessage("{}")
		}
		call, cancel := context.WithTimeout(ctx, options.toolCallTimeout)
		defer cancel()
		result, err := session.CallTool(call, &sdk.CallToolParams{Name: rawName, Arguments: arguments})
		if err != nil {
			return nil, err
		}

		structured, err := encodeStructured(result.StructuredContent)
		if err != nil {
			return nil, err
		}

		if result.Content == nil {
			// 对方一块内容都没给。DSH 在这里还认一种「遗留的 toolResult 形状」，
			// Go 的 SDK 把返回值 typed 化了，压根没有那个字段，所以只剩这一支。
			return noOutput(result.IsError, structured)
		}

		blocks, raw, err := normalizeContent(result.Content)
		if err != nil {
			return nil, err
		}
		text := extractText(blocks, rawName)
		if result.IsError {
			return nil, errors.New(text)
		}

		value := Result{Content: raw, StructuredContent: structured}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("mcp: 这份结果排不出去：%w", err)
		}
		if containsImage(blocks) {
			projections.stage(exec.Token, preparedProjection{
				value:    encoded,
				fallback: llm.Content{llm.TextBlock{Text: text}},
				content:  prepareImageProjection(ctx, options.admit, exec.Execution, blocks, rawName),
			})
		}
		return encoded, nil
	}
}

// noOutput 处理「对方一块内容都没给」这一支。
//
// 源: packages/mcp/mcp-client/src/tools.ts:322-334
func noOutput(isError bool, structured json.RawMessage) (json.RawMessage, error) {
	const text = "(no output)"
	if isError {
		return nil, errors.New(text)
	}
	synthesized, err := (&sdk.TextContent{Text: text}).MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("mcp: 这块补出来的文本排不出去：%w", err)
	}
	encoded, err := json.Marshal(Result{
		Content:           []json.RawMessage{synthesized},
		StructuredContent: structured,
	})
	if err != nil {
		return nil, fmt.Errorf("mcp: 这份结果排不出去：%w", err)
	}
	return encoded, nil
}

// encodeStructured 把对方那份结构化返回值排成 JSON，没有就交出 nil。
func encodeStructured(structured any) (json.RawMessage, error) {
	if structured == nil {
		return nil, nil
	}
	raw, err := json.Marshal(structured)
	if err != nil {
		return nil, fmt.Errorf("mcp: 这份 structuredContent 排不出去：%w", err)
	}
	return raw, nil
}

// isJSONObject 判一段 JSON 是不是一个对象。
func isJSONObject(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var probe map[string]json.RawMessage
	return json.Unmarshal(raw, &probe) == nil && probe != nil
}

// jsonEqual 判两段 JSON 说的是不是同一件事。
//
// 新增: 顶掉 DSH 的 isDeepStrictEqual。比字节是不行的：值在管线里走一趟可能被
// 重新排过（键序、空白都会变），而那两份说的仍然是同一件事。所以两边都解成
// 无类型的 Go 值再深比。
func jsonEqual(left, right json.RawMessage) bool {
	var leftValue, rightValue any
	if err := json.Unmarshal(left, &leftValue); err != nil {
		return false
	}
	if err := json.Unmarshal(right, &rightValue); err != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}
