// 本文件的作用：把一条路由上写下来的那张模型清单落成真正拿去发请求的模型记录，
// 顺带挑出「这份配置显式选了每次请求的输出上限」的那几个模型。
//
// 源: packages/llm/llm-pi-ai/src/catalog.ts:532-893

package openaicompat

import (
	"fmt"
	"slices"

	"github.com/snight1983/ds-harness-go/llm"
)

// 推理档位。
//
// 源: packages/llm/llm-pi-ai/src/catalog.ts:69-84
//
// 新增: DSH 那七个取值来自 pi-ai 的 ModelThinkingLevel，还配了一张 Record 门禁表
// 让 pi-ai 升级增删档位时**编译失败**。这边没有那个上游可以漂移，所以门禁表连同
// 它的理由一起不需要，取值集合就写在这里。名字照抄 pi-ai 的拼法不是为了兼容它，
// 而是因为这几个词（off/low/medium/high）已经是 OpenAI 兼容端点上 reasoning_effort
// 的通用词汇，换一套拼法只会让写配置的人多记一层。
const (
	ThinkingOff     llm.ReasoningEffortID = "off"
	ThinkingMinimal llm.ReasoningEffortID = "minimal"
	ThinkingLow     llm.ReasoningEffortID = "low"
	ThinkingMedium  llm.ReasoningEffortID = "medium"
	ThinkingHigh    llm.ReasoningEffortID = "high"
	ThinkingXHigh   llm.ReasoningEffortID = "xhigh"
	ThinkingMax     llm.ReasoningEffortID = "max"
)

// thinkingLevels 是所有能声明的推理档位，按由弱到强的规范次序排着。
//
// 源: packages/llm/llm-pi-ai/src/catalog.ts:86-87
//
// 这个次序是**输出**次序：[ResolvedModel.ReasoningEfforts] 和它落进
// [llm.ModelReasoningInfo] 的那份清单都按它排。Go 的 map 没有次序，照配置里的
// 书写顺序又取不到（JSON/YAML 解出来的 map 同样无序），所以只有一张固定的表
// 能让两次解算给出**逐字一样**的档位清单——选择器上的排序、诊断里的枚举都指望它。
var thinkingLevels = []llm.ReasoningEffortID{
	ThinkingOff, ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh, ThinkingMax,
}

// ThinkingLevels 交出所有能声明的推理档位，按规范次序排着。
func ThinkingLevels() []llm.ReasoningEffortID { return slices.Clone(thinkingLevels) }

// isThinkingLevel 判一个档位名认不认得。
func isThinkingLevel(level llm.ReasoningEffortID) bool {
	return slices.Contains(thinkingLevels, level)
}

// ModelProfile 是配置里写下来的一条模型。
//
// 源: packages/llm/llm-pi-ai/src/catalog.ts:548-585（PiAiModelProfile）
//
// 新增: DSH 那份的每个字段都能不填、由内置目录里同 id 的那条兜底。这个重写没有
// 内置目录（见包注释），所以「不填」兜的是路由自己那几个 default*，兜不到别处去。
// `compat` 字段整条不要——它是 pi-ai 的线上兼容开关。
//
// 新增: 每个字段都带 json 标签，键名逐字照 DSH 的 schema（catalog.ts:533-568）。
// 这不是为了兼容一份 DSH 写下的配置，而是因为 [Config] 会原样登记成一个设置命名
// 空间，而 [github.com/snight1983/ds-harness-go/settings.Register] 是拿 encoding/json 把这个类型来回
// 过一遍的——没有标签的话，写配置的人在 settings.yaml 里要写的是 Go 的字段名
// （`ContextWindow`），而界面和文档里说的是另一套拼法。
type ModelProfile struct {
	// ID 是发给提供方、也是 [llm.GenerateOptions].Model 认的那个模型 id。
	ID string `json:"id"`
	// Name 是给选择器看的显示名；空表示用 ID。
	Name string `json:"name,omitempty"`
	// ContextWindow 是请求加响应合计的 token 上限；零表示没填，用路由的
	// [ProviderProfile.DefaultContextWindow]。
	ContextWindow int `json:"contextWindow,omitempty"`
	// MaxTokens 是这个模型的输出能力上限；零表示没填，用路由的
	// [ProviderProfile.DefaultMaxTokens]。
	//
	// 在这里**填了**一个值还有第二层意思：它同时成为这个模型每次请求的默认输出
	// 上限。从路由兜底来的那个值只是能力，不会变成请求默认值，理由见
	// [routeCatalog].configuredMaxTokens。
	MaxTokens int `json:"maxTokens,omitempty"`
	// Input 是这个模型收的请求模态；nil 或空表示没填，用路由的
	// [ProviderProfile.DefaultInput]。
	//
	// 源: packages/llm/llm-pi-ai/src/catalog.ts:64-66
	//
	// 空和不填是同一件事：一份空清单描述的是一个什么都不收的模型，那种模型
	// 一个请求都服务不了，所以它只可能是「这里没有答案」。
	Input []llm.ModelModality `json:"input,omitempty"`
	// ReasoningEfforts 是能选的那些推理档位：键是档位，值是它在线上的拼法。
	//
	// 源: packages/llm/llm-pi-ai/src/catalog.ts:560-567
	//
	// nil 表示这是一个不推理的模型。非 nil 但空会被拒——那不是「不推理」的写法，
	// 而是一份写了一半的声明。
	//
	// 只有 [ThinkingOff] 那一档允许把值留空，意思是「支持这一档，但什么都不发」，
	// 因为「不思考」在 OpenAI 兼容线上的正确表达就是不写 reasoning_effort 这个字段。
	//
	// 新增: DSH 是 `false | dict` 的联合：不填表示继承内置目录那条的能力，false
	// 表示「这是个不推理的模型」。没有内置目录之后这两支落到同一个结果上，于是
	// 塌成 nil 这一种写法。
	//
	// 新增: DSH 还把**没声明**的档位在 pi-ai 的 thinkingLevelMap 里钉成 null，
	// 因为 pi-ai 自己的缺省是不对称的（前五档缺省算支持、xhigh/max 缺省算不支持），
	// 写配置的人不该需要知道这件事。这边没有那个库，「没声明就是不提供」是本包
	// 唯一的规则，那张钉死用的表不需要。
	//
	// 新增: 这一个字段**不写** omitempty。nil 和「非 nil 但空」在这里是两件不同的
	// 事（前者是不推理，后者是一份写了一半的声明，会被拒），而 omitempty 会把
	// 一个空 map 整个略掉、再解回来就成了 nil——那正好把该被拒的那一种悄悄变成
	// 合法的另一种。
	ReasoningEfforts map[llm.ReasoningEffortID]string `json:"reasoningEfforts"`
}

// ReasoningEffort 是一档解算完的推理档位。
type ReasoningEffort struct {
	// ID 是 [llm.GenerateOptions].ReasoningEffort 认的那个档位标识。
	ID llm.ReasoningEffortID
	// Wire 是它在请求体 reasoning_effort 字段上的拼法；空串表示这一档不写这个字段。
	Wire string
}

// ResolvedModel 是一条落定的模型：发一次请求要知道的全在这里。
//
// 源: packages/llm/llm-pi-ai/src/catalog.ts（那段 entries.map 造出来的 Model）
type ResolvedModel struct {
	// Provider 是拥有它的那条路由键。
	Provider string
	// ID 是发给提供方的模型 id。
	ID string
	// Name 是显示名，保证非空。
	Name string
	// ContextWindow 是上下文容量，保证是正整数。
	ContextWindow int
	// MaxTokens 是输出能力上限，保证是正整数。
	MaxTokens int
	// Input 是收的请求模态，保证非空，并且是本包自己拥有的一份。
	Input []llm.ModelModality
	// ReasoningEfforts 是能选的档位，按 [ThinkingLevels] 的规范次序排着；
	// 空表示这个模型不推理。
	ReasoningEfforts []ReasoningEffort
}

// Clone 交出一份深复制，调用方改它不会动到这一份。
func (m ResolvedModel) Clone() ResolvedModel {
	m.Input = slices.Clone(m.Input)
	m.ReasoningEfforts = slices.Clone(m.ReasoningEfforts)
	return m
}

// SupportsImages 判这个模型收不收图。
func (m ResolvedModel) SupportsImages() bool {
	return slices.Contains(m.Input, llm.ModalityImage)
}

// Effort 按档位标识找出它的线上拼法。
func (m ResolvedModel) Effort(id llm.ReasoningEffortID) (ReasoningEffort, bool) {
	for _, effort := range m.ReasoningEfforts {
		if effort.ID == id {
			return effort, true
		}
	}
	return ReasoningEffort{}, false
}

// invalidRoute 报一条这次部署服务不了的路由，并点名是配置里的哪个键出的事。
//
// 源: packages/llm/llm-pi-ai/src/catalog.ts:601-603
func invalidRoute(provider, detail string) error {
	return fmt.Errorf("%w：提供方 %q %s", ErrInvalidConfig, provider, detail)
}

// routeCatalogRequest 是落一条路由的模型清单要读的那几件事。
//
// 源: packages/llm/llm-pi-ai/src/catalog.ts:596-616（RouteCatalogRequest）
type routeCatalogRequest struct {
	provider             string
	models               []ModelProfile
	defaultContextWindow int
	defaultMaxTokens     int
	defaultInput         []llm.ModelModality
}

// routeCatalog 是一条路由落完的模型清单，加上这份配置显式选下来的那些请求上限。
//
// 源: packages/llm/llm-pi-ai/src/catalog.ts:772-787（RouteCatalog）
type routeCatalog struct {
	// models 按配置里的书写次序排着。
	models []ResolvedModel
	// configuredMaxTokens 是这份配置**显式**写下的每次请求输出上限，按模型 id 索引。
	//
	// 它和 [ResolvedModel].MaxTokens 分开，是因为两者答的不是同一个问题：后者是
	// 这个模型的输出**能力**，而 [llm.ResolvedModelInfo].DefaultMaxTokens 是这次
	// 部署选下来的、要写进「自己没点上限的那些请求」的那个盖子。把能力当成请求
	// 默认值落下去，等于开始拿一个谁也没挑过的数字去封每一次请求。
	configuredMaxTokens map[string]int
}

// resolveRouteModels 把一条路由写下来的模型清单落成模型记录。
//
// 源: packages/llm/llm-pi-ai/src/catalog.ts:789-908（resolveRouteModels）
//
// 新增: DSH 头一段处理 modelOverrides（按 id 定制内置目录里的某一条）。没有内置
// 目录之后，那张表**每一条**都会撞上 DSH 自己那句「内置目录不描述这条路由」的
// 拒绝，所以整个字段连同它那五条检查一起不要。同理消失的还有 api（只有一条协议）、
// 每条模型的 baseUrl 兜底（路由的 baseURL 是必填的）、以及 compat 那三段。
func resolveRouteModels(request routeCatalogRequest) (routeCatalog, error) {
	provider := request.provider
	// 源: packages/llm/llm-pi-ai/src/catalog.ts:800-802
	//
	// DSH 这里说的是「不填就服务内置目录」。没有内置目录之后，不填就什么都不剩，
	// 所以这条从「可以不填」变成了必填，并且诊断得自己说清楚该怎么办。
	if len(request.models) == 0 {
		return routeCatalog{}, invalidRoute(provider,
			"没有列出任何模型；这个适配器不带内置模型目录，所以每条路由都要在 models 里把模型写全")
	}

	seen := make(map[string]struct{}, len(request.models))
	configuredMaxTokens := make(map[string]int)
	models := make([]ResolvedModel, 0, len(request.models))
	for _, entry := range request.models {
		// 源: packages/llm/llm-pi-ai/src/catalog.ts:838-840
		if entry.ID == "" {
			return routeCatalog{}, invalidRoute(provider, "有一条模型没有 id")
		}
		if _, duplicate := seen[entry.ID]; duplicate {
			return routeCatalog{}, invalidRoute(provider, fmt.Sprintf("把模型 %q 列了不止一次", entry.ID))
		}
		seen[entry.ID] = struct{}{}

		// 容量兜到路由自己那两个默认值上，所以一份只写了 id 的模型清单照样能服务。
		// 兜底值天生是个猜测，这正是它做成一个能配的路由字段、而不是埋在这里的
		// 一个常量的理由。
		//
		// 源: packages/llm/llm-pi-ai/src/catalog.ts:857-867
		contextWindow := entry.ContextWindow
		if contextWindow == 0 {
			contextWindow = request.defaultContextWindow
		}
		if contextWindow <= 0 {
			return routeCatalog{}, invalidRoute(provider,
				fmt.Sprintf("模型 %q 的 contextWindow 必须是正整数", entry.ID))
		}
		maxTokens := entry.MaxTokens
		if maxTokens == 0 {
			maxTokens = request.defaultMaxTokens
		}
		if maxTokens <= 0 {
			return routeCatalog{}, invalidRoute(provider,
				fmt.Sprintf("模型 %q 的 maxTokens 必须是正整数", entry.ID))
		}
		// 只有这份配置**点名**写下的那个值才算一次部署的选择；兜底来的是能力，
		// 不进请求默认值。
		//
		// 源: packages/llm/llm-pi-ai/src/catalog.ts:868-870
		if entry.MaxTokens != 0 {
			configuredMaxTokens[entry.ID] = entry.MaxTokens
		}

		efforts, err := resolveModelReasoning(provider, entry)
		if err != nil {
			return routeCatalog{}, err
		}

		name := entry.Name
		if name == "" {
			name = entry.ID
		}
		input := slices.Clone(entry.Input)
		if len(input) == 0 {
			input = slices.Clone(request.defaultInput)
		}
		models = append(models, ResolvedModel{
			Provider:         provider,
			ID:               entry.ID,
			Name:             name,
			ContextWindow:    contextWindow,
			MaxTokens:        maxTokens,
			Input:            input,
			ReasoningEfforts: efforts,
		})
	}
	return routeCatalog{models: models, configuredMaxTokens: configuredMaxTokens}, nil
}

// resolveModelReasoning 把一条模型声明的那些推理档位落定。
//
// 源: packages/llm/llm-pi-ai/src/catalog.ts:645-700
func resolveModelReasoning(provider string, entry ModelProfile) ([]ReasoningEffort, error) {
	if entry.ReasoningEfforts == nil {
		return nil, nil
	}
	// 源: packages/llm/llm-pi-ai/src/catalog.ts:668-672
	//
	// 一份空的声明既不是「继承」也不是「不推理」的拼法。DSH 那边 YAML 里
	// `reasoningEfforts:` 留空会走到 null、显式 `{}` 走到空字典，两条都拒；
	// Go 这边解出来都是一个长度为零的非 nil map，落在同一条上。
	if len(entry.ReasoningEfforts) == 0 {
		return nil, invalidRoute(provider, fmt.Sprintf(
			"模型 %q 的 reasoningEfforts 是空的；要么把能选的档位列出来，要么整个字段不写"+
				"（那表示这是个不推理的模型）", entry.ID))
	}

	// 先把认不认得这些档位名验掉。这一步 DSH 交给了 schemastery 的 z.union
	// （见 config.ts:278-281），Go 这边 map 的键是个自由字符串，没人替它挡。
	// 不验的话，一个把 `higth` 写错的人得到的是一个静静地少了一档的模型。
	for level := range entry.ReasoningEfforts {
		if !isThinkingLevel(level) {
			return nil, invalidRoute(provider, fmt.Sprintf(
				"模型 %q 的 reasoningEfforts 里有一个不认得的档位 %q；能写的是 %v",
				entry.ID, level, thinkingLevels))
		}
	}

	efforts := make([]ReasoningEffort, 0, len(entry.ReasoningEfforts))
	for _, level := range thinkingLevels {
		wire, declared := entry.ReasoningEfforts[level]
		if !declared {
			continue
		}
		// 源: packages/llm/llm-pi-ai/src/catalog.ts:678-687
		//
		// 新增: DSH 在这里分得出两种毛病——值是 null（YAML 里 `high:` 留空）
		// 和值是空字符串（`high: ''`），各给一条诊断。Go 的 map[..]string 收不下
		// null，两者都是空串，于是塌成这一条。文案把两种写法都点出来，好让人
		// 对得上自己写的是哪一种。
		if wire == "" && level != ThinkingOff {
			return nil, invalidRoute(provider, fmt.Sprintf(
				"模型 %q 的 reasoningEfforts.%s 没有给出线上拼法；只有 %q 那一档允许留空",
				entry.ID, level, ThinkingOff))
		}
		efforts = append(efforts, ReasoningEffort{ID: level, Wire: wire})
	}

	// 源: packages/llm/llm-pi-ai/src/catalog.ts:688-691
	//
	// 只有 off 一档的话，这个模型能选的「推理档位」里没有一档真的在推理——
	// 那和不推理是同一件事，但会在选择器上多出一个什么都不做的选项。
	onlyOff := true
	for _, effort := range efforts {
		if effort.ID != ThinkingOff {
			onlyOff = false
			break
		}
	}
	if onlyOff {
		return nil, invalidRoute(provider, fmt.Sprintf(
			"模型 %q 的 reasoningEfforts 除了 %q 之外没有别的档位；要么加一档真的在推理的，"+
				"要么整个字段不写（那表示这是个不推理的模型）", entry.ID, ThinkingOff))
	}
	return efforts, nil
}
