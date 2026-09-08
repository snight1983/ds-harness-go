// 本文件的作用：让装配进来的贡献方各自认领模型请求的一个顶层字段，并在每次请求
// 发出去之前把它们这一次的取值收齐、折成请求选项。
//
// 源: packages/llm/deepseek-llm-api-extensions/src/index.ts、
// packages/llm/deepseek-llm-api-extensions/src/types.ts
//
// 新增: 上游那个包只服务 DeepSeek 官方适配器，字段名靠 TypeScript 的声明合并
// （`DeepSeekLlmApiExtensionMap`）登记，所以「谁拥有哪个字段」是编译期的事。Go 没有
// 声明合并，这件事只能落成一张运行期的表，于是**字段的归属要在登记那一刻就报冲突**
// ——两个贡献方抢同一个顶层键是装配写错了，而不是某一次请求的运行时故障。

package openaicompat

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/snight1983/ds-harness-go/llm"
)

// ExtensionFailedCode 是「某个贡献方没能备好它那些字段」这条失败的代号。
//
// 新增: 上游让准备阶段的异常直接冒出去（index.ts:108-114），调用方看到的是贡献方
// 自己的错误。这边归一成一条带代号的 [llm.Error]，理由和本包别处一样：调用方要能
// 分辨「这次请求还没发出去就黄了」和「上游把它拒了」，前者重试没有意义。
const ExtensionFailedCode = "EXTENSION_FAILED"

// extensionFieldRE 认一个合法的顶层字段名。
//
// 新增: 上游对字段名只要求「非空且没有首尾空白」（index.ts:84-86）。这边收得更紧，
// 因为落地手段不同：openai-go 的 [option.WithJSONSet] 收的是一条 sjson **路径**
// （requestoption.go:197-200），`a.b` 会被当成嵌套、`*` 和 `?` 会被当成通配。一个
// 带这些字符的字段名不会报错，只会安安静静地写到别的地方去。
var extensionFieldRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// reservedExtensionFields 是本适配器自己拼的那些顶层字段，贡献方认领不了。
//
// 新增: 上游没有这道闸——声明合并那张表里本来就只有插件自己加的键，撞不上官方字段。
// 这边字段名是运行期的字符串，一个认领了 `messages` 的贡献方会把整段对话历史换掉，
// 而且是在请求已经拼完之后换的，从任何一条诊断上都看不出来。
var reservedExtensionFields = []string{
	"max_tokens", "messages", "model", "reasoning_effort",
	"stop", "stream", "stream_options", "temperature", "tools",
}

// ExtensionRequest 是交给贡献方的那份「这次请求已经定下来的事实」。
//
// 源: packages/llm/deepseek-llm-api-extensions/src/types.ts:19-29
//
// 新增: 上游还带一个 AbortSignal，Go 这边取消走 Prepare 自己收的 ctx，不重复放一份。
type ExtensionRequest struct {
	// Provider 是这次请求落在哪条路由上。
	Provider string
	// Model 是那条路由上的模型 id。
	Model string
	// SessionID 是循环盖上来的会话身份，空串表示没盖。
	SessionID llm.SessionID
	// Purpose 是这次辅助调用的分类，空串表示这是一次普通的对话请求。
	Purpose llm.CallPurpose
	// Body 是这次请求在**追加扩展字段之前**已经排好的那份 JSON。
	//
	// 里面没有 `stream` 那一位——它由 SDK 在真正发出去的那一刻补上。
	//
	// 不要改它：改了不会影响发出去的那一份（真正的请求体由 SDK 从参数重新排），
	// 只会让排在后面的贡献方读到一份被动过的事实。
	Body json.RawMessage
}

// Extension 是一个贡献方的登记项。
//
// 源: packages/llm/deepseek-llm-api-extensions/src/types.ts:39-49
type Extension struct {
	// Contributor 是贡献方在诊断里的名字，冲突报错要靠它点名。
	Contributor string
	// Fields 是这个贡献方拥有的顶层字段名，至少一个。
	//
	// 新增: 上游一次登记只认领一个字段（index.ts:79-82），要两个就登记两次。这边
	// 收成一张列表，是因为归属报错要点名**贡献方**：登记两次的话，第二次撞车时
	// 第一次已经落进表里了，得再写一段回滚。列表让一次登记要么整个成，要么整个不成。
	Fields []string
	// Prepare 备好这一次请求里这个贡献方那些字段的取值。
	//
	// 交出的表只允许出现 [Extension.Fields] 里的键；少写几个（或者整个交 nil）
	// 表示这一次不出那些字段，对应上游那个 `undefined`（index.ts:118）。
	Prepare func(ctx context.Context, request ExtensionRequest) (map[string]any, error)
}

// ExtensionRegistry 是那张「顶层字段 → 贡献方」的归属表。
//
// 源: packages/llm/deepseek-llm-api-extensions/src/index.ts:66-130
//
// 零值不能用，走 [NewExtensionRegistry]。nil 指针是能用的，表示一个字段都没有——
// 装配方不关心这件事时就不必造一张空表。
type ExtensionRegistry struct {
	// mutex 守着下面两样。登记发生在装配期，而 Prepare 是每次请求都要跑的。
	mutex sync.RWMutex
	// owner 是字段名到贡献方名字的映射，只用来报冲突。
	owner map[string]string
	// entries 按登记次序排，Prepare 也按这个次序跑。
	entries []Extension
}

// NewExtensionRegistry 造一张空的归属表。
func NewExtensionRegistry() *ExtensionRegistry {
	return &ExtensionRegistry{owner: map[string]string{}}
}

// Register 把一个贡献方连同它认领的那些字段登记进来。
//
// 源: packages/llm/deepseek-llm-api-extensions/src/index.ts:79-99
//
// 任何一处不合法都整条拒掉，表一动不动：一个只登记了一半字段的贡献方比一个没登记的
// 更难查。
//
// 新增: 上游交回一个 disposer，登记跟着 cordis 的 effect 作用域走。本仓库的适配器
// 是装配期一次拼好的，没有那个作用域，所以只进不出——真需要撤销时再加，成例不缺。
func (r *ExtensionRegistry) Register(extension Extension) error {
	if r == nil {
		return fmt.Errorf("%w：往一张 nil 的扩展字段表里登记 %q", ErrInvalidConfig, extension.Contributor)
	}
	contributor := extension.Contributor
	if strings.TrimSpace(contributor) != contributor || contributor == "" {
		return fmt.Errorf("%w：扩展贡献方的名字不能是空的、也不能带首尾空白（%q）",
			ErrInvalidConfig, contributor)
	}
	if extension.Prepare == nil {
		return fmt.Errorf("%w：扩展贡献方 %q 没有 Prepare", ErrInvalidConfig, contributor)
	}
	if len(extension.Fields) == 0 {
		return fmt.Errorf("%w：扩展贡献方 %q 一个字段都没认领", ErrInvalidConfig, contributor)
	}

	r.mutex.Lock()
	defer r.mutex.Unlock()
	claimed := make(map[string]struct{}, len(extension.Fields))
	for _, field := range extension.Fields {
		if !extensionFieldRE.MatchString(field) {
			return fmt.Errorf("%w：扩展贡献方 %q 认领的字段名 %q 不是一个裸标识符",
				ErrInvalidConfig, contributor, field)
		}
		if slices.Contains(reservedExtensionFields, field) {
			return fmt.Errorf("%w：字段 %q 是本适配器自己拼的，扩展贡献方 %q 认领不了",
				ErrInvalidConfig, field, contributor)
		}
		if _, repeated := claimed[field]; repeated {
			return fmt.Errorf("%w：扩展贡献方 %q 把字段 %q 认领了两遍",
				ErrInvalidConfig, contributor, field)
		}
		if owner, taken := r.owner[field]; taken {
			return fmt.Errorf("%w：字段 %q 已经归扩展贡献方 %q 了，%q 认领不了",
				ErrInvalidConfig, field, owner, contributor)
		}
		claimed[field] = struct{}{}
	}

	entry := extension
	entry.Fields = slices.Clone(extension.Fields)
	for _, field := range entry.Fields {
		r.owner[field] = contributor
	}
	r.entries = append(r.entries, entry)
	return nil
}

// Len 是当下登记着的贡献方个数。nil 表是零个。
func (r *ExtensionRegistry) Len() int {
	if r == nil {
		return 0
	}
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	return len(r.entries)
}

// Prepare 按登记次序问一遍每个贡献方，把这一次要追加的顶层字段收齐。
//
// 源: packages/llm/deepseek-llm-api-extensions/src/index.ts:108-129
//
// 新增: 上游拿 Promise.all 并发问（index.ts:111-114）。这边按登记次序一个一个问：
// 贡献方之间没有并发能省下来的 IO（它们备的是自己手里已有的东西），而顺序跑让
// 「哪个贡献方先失败」在同一份装配上每次都一样。
func (r *ExtensionRegistry) Prepare(
	ctx context.Context,
	request ExtensionRequest,
) (map[string]any, error) {
	if r == nil {
		return nil, nil
	}
	r.mutex.RLock()
	entries := slices.Clone(r.entries)
	r.mutex.RUnlock()
	if len(entries) == 0 {
		return nil, nil
	}

	fields := make(map[string]any)
	for _, entry := range entries {
		contributed, err := entry.Prepare(ctx, request)
		if err != nil {
			return nil, llm.NewError(fmt.Sprintf(
				"extension contributor %q failed to prepare its request fields", entry.Contributor),
				ExtensionFailedCode, err)
		}
		// 按名字排序落下去，好让一个贡献方多交了两个字段时，报出来的是同一个。
		for _, field := range slices.Sorted(maps.Keys(contributed)) {
			if !slices.Contains(entry.Fields, field) {
				return nil, llm.NewError(fmt.Sprintf(
					"extension contributor %q returned unclaimed field %q", entry.Contributor, field),
					ExtensionFailedCode, nil)
			}
			fields[field] = contributed[field]
		}
	}
	return fields, nil
}

// prepareExtensions 为一次已经拼完的请求备好要追加的顶层字段。
//
// 源: packages/llm/deepseek-llm-api-extensions/src/index.ts:102-106
//
// 一个贡献方都没登记时连排一次请求体都省掉——排一份没人读的 JSON 是纯粹的浪费。
func (a *Adapter) prepareExtensions(
	ctx context.Context,
	options llm.GenerateOptions,
	params openai.ChatCompletionNewParams,
) (map[string]any, error) {
	if a.options.Extensions.Len() == 0 {
		return nil, nil
	}
	body, err := json.Marshal(params)
	if err != nil {
		return nil, llm.NewError(
			"the openai-compatible request could not be serialized for its extension contributors",
			ExtensionFailedCode, err)
	}
	return a.options.Extensions.Prepare(ctx, ExtensionRequest{
		Provider:  options.Provider,
		Model:     options.Model,
		SessionID: options.SessionID,
		Purpose:   options.Purpose,
		Body:      body,
	})
}

// extensionOptions 把备好的那些字段折成请求选项，按字段名排序。
//
// 排序是为了让同一份装配每次发出去的请求体键序一样——顺序本身不影响语义，但一份
// 每次都不同的请求体让抓包比对变得没法做。
func extensionOptions(fields map[string]any) []option.RequestOption {
	if len(fields) == 0 {
		return nil
	}
	options := make([]option.RequestOption, 0, len(fields))
	for _, field := range slices.Sorted(maps.Keys(fields)) {
		options = append(options, option.WithJSONSet(field, fields[field]))
	}
	return options
}
