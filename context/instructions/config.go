// 本文件的作用：把调用方给的那份配置补齐成一份规范化配置，
// 以及把「这份基线是在什么口径下产出的」算成一个可比较的身份串。
//
// 源: packages/context/agent-instructions/src/config.ts:1-123

package instructions

import (
	"encoding/json"
	"slices"
	"strings"
)

// DefaultMaxSourceBytes 是单个指令文件默认的字节上限，超过它的文件整份不读。
//
// 源: packages/context/agent-instructions/src/config.ts:14
const DefaultMaxSourceBytes = 1 << 20 // 1_048_576

// 默认的根标记与候选文件名。
//
// 源: packages/context/agent-instructions/src/config.ts:11-13
//
// 这三个是变量不是常量，因为切片没有常量形式。每次取用都要 [slices.Clone]
// 一份再交出去——直接交出去的话，调用方对结果切片的任何一次写都会改到默认值本身，
// 而那种污染会跨会话生效。
var (
	defaultProjectRootMarkers             = []string{".git"}
	defaultInstructionFileCandidates      = []string{"AGENTS.md", "CLAUDE.md"}
	defaultLocalInstructionFileCandidates = []string{"AGENTS.local.md", "CLAUDE.local.md"}
)

// reservedPathSegments 是不能当候选文件名用的那几段。
//
// 源: packages/context/agent-instructions/src/config.ts:15
var reservedPathSegments = map[string]struct{}{"": {}, ".": {}, "..": {}}

// Config 是调用方给的那份工作区指令配置。
//
// 源: packages/context/agent-instructions/src/config.ts:17-46
//
// 除了 [Config.MaxBytes]，每一项留零值都表示「用默认值」，
// 补默认值这件事集中在 [Config.Resolve] 里做。
type Config struct {
	// UserGlobalRoot 是放用户全局指令的那个目录，里面固定叫 [UserGlobalFile]。
	//
	// 新增: DSH 这里是 `dshHome`，由 `dsh-home-paths` 从本机 home 目录算出来。
	// 那个包是 OUT_OF_SCOPE 的——服务端没有「当前用户的 home」。换成的是一条
	// 落在 [fs] 接缝里的绝对目录，由装配方给。**留空表示这套部署没有用户全局
	// 这一层**，发现阶段直接跳过它，而不是去探一个猜出来的路径。
	UserGlobalRoot string

	// ProjectRootMarkers 是从 cwd 往上走时用来认项目根的那些子项名字。
	//
	// 留 nil 用默认的 `.git`。这里认的是「子项在场」而不是「子项是目录」，
	// 因为 `.git` 在工作树里是目录、在 worktree 里是文件。
	ProjectRootMarkers []string

	// MaxBytes 是一次渲染（一份基线或者一批增量）的 UTF-8 字节上限。
	//
	// **没有默认值**：这一项说的是「这套部署愿意给工作区指令多少上下文预算」，
	// 猜一个数出来的后果是模型的上下文被一份没人决定过的配额吃掉。
	// 小于等于零表示关掉这一层。
	MaxBytes int

	// MaxSourceBytes 是单个指令文件读进来的字节上限，超过的整份忽略。
	//
	// 新增: 留零表示用 [DefaultMaxSourceBytes]。DSH 那边零和缺席是两件事——
	// 缺席取默认值，显式的零会在后面被 `<= 0` 挡掉。Go 的零值分不出这两种，
	// 而 DSH 的模式上本来就有 `.min(1)`，显式的零根本进不来，所以这里把零
	// 归给「没填」。要关掉这一层就填负数。
	MaxSourceBytes int

	// InstructionFileCandidates 是同一个目录里按序尝试的基础候选文件名。
	//
	// 在场的**全部**加载，同一个目录里去掉首尾空白之后内容重复的，
	// 只留发现顺序最靠前的那一个。留 nil 用默认的 AGENTS.md、CLAUDE.md。
	InstructionFileCandidates []string

	// LocalInstructionFileCandidates 是基础候选之后再叠一层的本地覆盖候选。
	//
	// 去重规则和基础候选共用一套（同目录、按去空白内容）。留 nil 用默认的
	// AGENTS.local.md、CLAUDE.local.md；给一个**非 nil 的空切片**表示关掉这一层。
	LocalInstructionFileCandidates []string
}

// ResolvedConfig 是补齐默认值之后的配置，发现、渲染和对账都拿它。
//
// 源: packages/context/agent-instructions/src/config.ts:48-60
//
// 新增: DSH 把它拆成 `ResolvedDiscoveryConfig`（发现阶段够用的那一半）
// 和继承它的 `ResolvedConfig`，因为 `DiscoverOptions` 里没有字节预算那两项。
// 这里合成一个：Go 侧的发现函数收的是显式参数而不是一个选项对象，
// 多一个只用于「这个函数少看两个字段」的类型，读的人还要多跟一次继承。
type ResolvedConfig struct {
	UserGlobalRoot                 string
	ProjectRootMarkers             []string
	InstructionFileCandidates      []string
	LocalInstructionFileCandidates []string
	MaxBytes                       int
	MaxSourceBytes                 int
}

// Resolve 补齐默认值，并且把不能当文件名用的候选筛掉。
//
// 源: packages/context/agent-instructions/src/config.ts:84-117
func (c Config) Resolve() ResolvedConfig {
	markers := c.ProjectRootMarkers
	if markers == nil {
		markers = slices.Clone(defaultProjectRootMarkers)
	}
	maxSourceBytes := c.MaxSourceBytes
	if maxSourceBytes == 0 {
		maxSourceBytes = DefaultMaxSourceBytes
	}
	return ResolvedConfig{
		UserGlobalRoot:                 c.UserGlobalRoot,
		ProjectRootMarkers:             markers,
		InstructionFileCandidates:      resolveCandidates(c.InstructionFileCandidates, defaultInstructionFileCandidates),
		LocalInstructionFileCandidates: resolveCandidates(c.LocalInstructionFileCandidates, defaultLocalInstructionFileCandidates),
		MaxBytes:                       c.MaxBytes,
		MaxSourceBytes:                 maxSourceBytes,
	}
}

// resolveCandidates 取默认值或者筛一遍调用方给的候选名。
//
// 源: packages/context/agent-instructions/src/config.ts:119-123
//
// 筛掉的是那些「不是同目录里的一个文件名」的东西：空串、`.`、`..`，
// 以及任何带路径分隔符的。候选名会被直接拼到目录后面，混进一段路径
// 就等于让配置能读到目录树上任意一个地方去。
//
// nil 和非 nil 的空切片是两件事：前者是「没填」，取默认值；
// 后者是「明确要求关掉这一层」，照办。这一点和 DSH 的 `candidates ?? [...]`
// 完全对应。
func resolveCandidates(candidates []string, fallback []string) []string {
	if candidates == nil {
		return slices.Clone(fallback)
	}
	// 结果永远非 nil：它会被 [WorkspaceBaselineIdentity] 编成 JSON，
	// 而 nil 切片编出来是 null、空切片编出来是 []。同一份配置在两次运行里
	// 编出不同的身份串，会让一次本来能续上的会话判成不兼容。
	kept := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if _, reserved := reservedPathSegments[candidate]; reserved {
			continue
		}
		if strings.ContainsAny(candidate, `/\`) {
			continue
		}
		kept = append(kept, candidate)
	}
	return kept
}

// WorkspaceBaselineIdentity 把一份基线的发现口径、优先级口径和预算口径
// 压成一个稳定的串，续会话时拿它判「上一份基线还能不能接着用」。
//
// 源: packages/context/agent-instructions/src/config.ts:62-82
//
// 项目根记的是**相对 cwd 的位置**而不是绝对路径：同一个仓库被挂在不同的
// 绝对路径下仍然是同一份基线，而根相对 cwd 挪了一层就不是了。
func WorkspaceBaselineIdentity(config ResolvedConfig, cwd string, projectRoot string) string {
	// 字段顺序就是 DSH 那个对象字面量的顺序，encoding/json 按结构体字段序输出。
	// 顺序变了串就变了，而串一变所有在途会话的基线都会被判成不兼容。
	payload := struct {
		ProjectRoot                    string   `json:"projectRoot"`
		ProjectRootMarkers             []string `json:"projectRootMarkers"`
		MaxBytes                       int      `json:"maxBytes"`
		MaxSourceBytes                 int      `json:"maxSourceBytes"`
		InstructionFileCandidates      []string `json:"instructionFileCandidates"`
		LocalInstructionFileCandidates []string `json:"localInstructionFileCandidates"`
	}{
		ProjectRoot:                    RelativeDisplay(cwd, projectRoot),
		ProjectRootMarkers:             config.ProjectRootMarkers,
		MaxBytes:                       config.MaxBytes,
		MaxSourceBytes:                 config.MaxSourceBytes,
		InstructionFileCandidates:      config.InstructionFileCandidates,
		LocalInstructionFileCandidates: config.LocalInstructionFileCandidates,
	}
	// 这个结构体里只有字符串、整数和字符串切片，json.Marshal 不可能失败。
	// 顺带一提：Go 默认会把尖括号和 & 转成 < 这类转义，JSON.stringify 不会，
	// 所以两边的串不是逐字节相同的。不要紧——这个串只和本构建产出的串比。
	encoded, _ := json.Marshal(payload)
	return string(encoded)
}
