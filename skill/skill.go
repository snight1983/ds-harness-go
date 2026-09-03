// 本文件的作用：技能这条缝上的**值**——一份技能长什么样、谁能提供它、
// 以及一份正文怎么渲染成模型看的那个块。注册表本身在 registry.go。
//
// 源: packages/skill/skill/src/index.ts:20-277

package skill

import (
	"context"
	"regexp"
	"strings"

	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/session"
)

// namePattern 是技能名的公开文法：小写字母数字，用单个短横连起来。
//
// 源: packages/skill/skill/src/index.ts:20
var namePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

const (
	// defaultCollectCacheMaxEntries 是缓存里最多留几份完整的目录。
	//
	// 源: packages/skill/skill/src/index.ts:21
	defaultCollectCacheMaxEntries = 128
	// maxCollectAttempts 是一次读遇上并发改动时最多重来几次。
	//
	// 源: packages/skill/skill/src/index.ts:22
	maxCollectAttempts = 2
	// runtimeProvider 是运行期注册那条路占用的提供方名字，别人不许用。
	//
	// 源: packages/skill/skill/src/index.ts:23
	runtimeProvider = "runtime"
	// runtimeRank 是运行期注册的技能在一层之内的优先级。
	//
	// 源: packages/skill/skill/src/index.ts:24
	runtimeRank = 250
)

// BundledRank 是打包提供方和本地打包目录约定用的优先级。
//
// 源: packages/skill/skill/src/index.ts:27
const BundledRank = 600

// IsName 判一个字符串是不是合法的技能名。
//
// 源: packages/skill/skill/src/index.ts:34-36
func IsName(name string) bool {
	return namePattern.MatchString(name)
}

// Source 是一份技能的来源归属。它是给模型看的元数据，本身不决定优先级。
//
// 源: packages/skill/skill/src/index.ts:39
//
// 这个类型是**开放**的：DSH 写成 `... | (string & {})`，提供方可以报本包没列出来的
// 来源。Go 的定义类型正好是这个意思，下面那几个常量只是约定俗成的那几种。
type Source string

const (
	// SourceProjectDSH 是项目里 .dsh 目录下的技能。
	SourceProjectDSH Source = "project-dsh"
	// SourceProjectAgents 是项目里 agents 目录下的技能。
	SourceProjectAgents Source = "project-agents"
	// SourceRuntime 是运行期注册进来的技能。
	SourceRuntime Source = "runtime"
	// SourceUserDSH 是用户主目录 .dsh 下的技能。
	SourceUserDSH Source = "user-dsh"
	// SourceUserAgents 是用户主目录 agents 下的技能。
	SourceUserAgents Source = "user-agents"
	// SourceCustom 是装配方自己定义的来源。
	SourceCustom Source = "custom"
	// SourceBundled 是随发行包一起打进来的技能。
	SourceBundled Source = "bundled"
)

// ResourceBaseKind 是一份资源基址的判别标签。
//
// 源: packages/skill/skill/src/index.ts:42-45
type ResourceBaseKind string

const (
	// ResourceBaseDirectory 表示这份技能的相对路径按一个本地目录解。
	ResourceBaseDirectory ResourceBaseKind = "directory"
	// ResourceBaseURL 表示这份技能的相对路径按一个 URL 解。
	ResourceBaseURL ResourceBaseKind = "url"
	// ResourceBaseOpaque 表示提供方只能用一句话描述它的资源在哪。
	ResourceBaseOpaque ResourceBaseKind = "opaque"
)

// ResourceBase 是技能正文里那些相对引用该按什么来解。
//
// 源: packages/skill/skill/src/index.ts:42-45
//
// 这是一个**封闭**联合：DSH 在 renderResourceHint 的 default 分支上放了 assertNever，
// 摆明了「以后加一种就得让编译不过」。Go 这边靠一个未导出方法把实现方封在本包内，
// 加一种就得在这里加，[renderResourceHint] 那个 switch 也就跟着必须改。
type ResourceBase interface {
	// ResourceBaseKind 是这份基址的判别标签。
	ResourceBaseKind() ResourceBaseKind

	// sealedResourceBase 把实现方封在本包内。
	sealedResourceBase()
}

// DirectoryBase 是一个本地目录形式的资源基址。
//
// 源: packages/skill/skill/src/index.ts:43
type DirectoryBase struct {
	// Path 是那个目录的绝对路径。
	Path string
}

// ResourceBaseKind 实现 [ResourceBase]。
func (DirectoryBase) ResourceBaseKind() ResourceBaseKind { return ResourceBaseDirectory }

func (DirectoryBase) sealedResourceBase() {}

// URLBase 是一个 URL 形式的资源基址。
//
// 源: packages/skill/skill/src/index.ts:44
type URLBase struct {
	// URL 是那个基址。
	URL string
}

// ResourceBaseKind 实现 [ResourceBase]。
func (URLBase) ResourceBaseKind() ResourceBaseKind { return ResourceBaseURL }

func (URLBase) sealedResourceBase() {}

// OpaqueBase 是一份只说得出一句话的资源基址。
//
// 源: packages/skill/skill/src/index.ts:45
type OpaqueBase struct {
	// Description 是那句话，会原样出现在模型看到的文本里。
	Description string
}

// ResourceBaseKind 实现 [ResourceBase]。
func (OpaqueBase) ResourceBaseKind() ResourceBaseKind { return ResourceBaseOpaque }

func (OpaqueBase) sealedResourceBase() {}

// InvocationPolicy 是一份技能允许被谁调起来。
//
// 源: packages/skill/skill/src/index.ts:48-54
type InvocationPolicy struct {
	// ModelInvocable 表示模型看得见它、也能用 skill 工具把它读进来。
	ModelInvocable bool
	// UserInvocable 表示人可以用命令目录把它调起来。
	UserInvocable bool
}

// Summary 是一份技能的**不区分调用方**的元数据，也就是目录里的那一行。
//
// 源: packages/skill/skill/src/index.ts:56-72
type Summary struct {
	// Name 是那个小写短横形式的标识。
	Name string
	// Description 是给消费方路由用的一句短说明。
	Description string
	// WhenToUse 是额外的路由提示，空串表示没有。
	WhenToUse string
	// Invocation 是解算完的调用许可。
	Invocation InvocationPolicy
	// Source 是产出这份赢家技能的来源。
	Source Source
	// Provider 是持有这份技能正文的提供方。
	Provider string
	// ResourceBase 是相对资源的基址，为 nil 表示提供方没给。
	ResourceBase ResourceBase
}

// Candidate 是提供方报出来的一条目录项，注册表拿它合并、之后再拿它去取正文。
//
// 源: packages/skill/skill/src/index.ts:74-84
type Candidate struct {
	Summary

	// Rank 越小越先赢；它只在**一层之内**决定同名谁赢，跨层由遮蔽规则决定。
	Rank int
	// Locator 是提供方自己的不透明句柄，取正文时原样递回给它。
	Locator any
	// Path 是这份技能在磁盘上的绝对路径，提供方有才给。
	Path string
	// Metadata 是提供方从自己的 frontmatter 里解出来的额外元数据，可以为 nil。
	//
	// 它是**借来的只读值**：注册表原样递给消费方，从不改它，也请消费方别改。
	Metadata map[string]any
}

// Definition 是一份完整的技能，包括正文。
//
// 源: packages/skill/skill/src/index.ts:86-94
type Definition struct {
	Summary

	// Content 是那段 Markdown 指令正文，提供方特定的元数据已经摘掉了。
	Content string
	// Path 是这份技能在磁盘上的绝对路径，来自磁盘才有。
	Path string
	// Metadata 是从 frontmatter 里解出来的额外元数据，可以为 nil。
	Metadata map[string]any
}

// Registration 是一次运行期技能贡献。
//
// 源: packages/skill/skill/src/index.ts:96-102
type Registration struct {
	// Name 是那个小写短横形式的标识。
	Name string
	// Description 是给消费方路由用的一句短说明，不能是空串。
	Description string
	// WhenToUse 是额外的路由提示，空串表示没有。
	WhenToUse string
	// Source 是这份技能报出来的来源。
	Source Source
	// ResourceBase 是相对资源的基址，可以为 nil。
	ResourceBase ResourceBase
	// Content 是那段指令正文。
	Content string
	// Path 是绝对路径，可以是空串。
	Path string
	// Metadata 是额外元数据，可以为 nil。
	Metadata map[string]any

	// Invocation 为 nil 表示两条路都放行。
	//
	// 新增: DSH 是 `invocation?: SkillInvocationPolicy`，省掉等于两个都为真。
	// Go 的结构体零值是两个都为假，正好反过来，所以这里必须是指针——不然一次
	// 漏填会把这份技能对谁都藏起来，而且没有任何地方会报错。
	Invocation *InvocationPolicy
	// Provider 为空串表示用注册表自己那个运行期提供方。
	Provider string
}

// LookupOptions 是提供方做一次查找时要的上下文。
//
// 源: packages/skill/skill/src/index.ts:104-109
//
// 新增: DSH 这里还有一个 signal，Go 里那是第一个参数上的 [context.Context]。
type LookupOptions struct {
	// WorkspaceID 选的是这次查找算哪个工作区，空串表示不挑。
	//
	// 新增: DSH 这个字段叫 `cwd`，装的是一条宿主机工作目录。本仓库喂进来的是
	// [github.com/snight1983/ds-harness-go/session.SessionHeader.WorkspaceID]，
	// 一个不透明的归属标识，字段名跟着改。提供方只许拿它比相等，不许解析。
	WorkspaceID session.WorkspaceID
}

// ViewOptions 是读注册表时的选项：提供方那份查找上下文，加上看的人是谁。
//
// 源: packages/skill/skill/src/index.ts:117-120
type ViewOptions struct {
	LookupOptions

	// Scope 是看的人（一般就是发起这次读的 agent）；为 nil 只读全局层。
	Scope *scope.Key
}

// IsModelInvocable 判一份技能能不能报给模型、并且被模型读进来。
//
// 源: packages/skill/skill/src/index.ts:127-129
func IsModelInvocable(summary Summary) bool {
	return summary.Invocation.ModelInvocable
}

// IsUserInvocable 判一份技能能不能被人用命令调起来。
//
// 源: packages/skill/skill/src/index.ts:136-138
func IsUserInvocable(summary Summary) bool {
	return summary.Invocation.UserInvocable
}

// InvocationSource 是「用户明确调起了一份技能」这件事在会话日志里那份持久来源。
//
// 源: packages/skill/skill/src/index.ts:147-154
//
// 用户自己那句话走一条普通的用户消息，渲染出来的技能正文跟在后面、作为一段带着这个
// 来源的注入上下文——于是读日志的人靠元数据就知道那是一次注入，不必回头去解模型
// 看的那段文本。
//
// 新增: DSH 靠 declare module 把 'skill-invocation' 挂进 llm 的 MessageSourceMap。
// Go 的接口封在 llm 包里，外面加不了变体，所以这个类型只是一份**约定的形状**：
// 注入方把它排成 [github.com/snight1983/ds-harness-go/llm.PluginSource].Extra 那份不透明 JSON。
type InvocationSource struct {
	// Name 是被调起来的技能名，注入的那一侧已经验过它允许被人调。
	Name string
}

// RenderContent 把一份读进来的技能渲染成模型看的那个块。
//
// 源: packages/skill/skill/src/index.ts:171-184
//
// skill 工具的结果和「用户明确调起」那条注入路**逐字共用**这份输出，好让模型在两条
// 路上看见同一个 `<skill_content>` 形状。名字走一个转义过的属性；正文原样嵌进去——
// 技能是可信的本地内容，而用户自己输入的话留在这个包装外面。
func RenderContent(skill Definition) string {
	lines := []string{`<skill_content name="` + escapeAttr(skill.Name) + `">`, "<skill_resources>"}
	lines = append(lines, renderResourceHint(skill)...)
	lines = append(lines,
		"</skill_resources>",
		"",
		"<skill_instructions>",
		skill.Content,
		"</skill_instructions>",
		"</skill_content>",
	)
	return strings.Join(lines, "\n")
}

// renderResourceHint 是 `<skill_resources>` 里那两行。
//
// 源: packages/skill/skill/src/index.ts:186-211
func renderResourceHint(skill Definition) []string {
	const loadHint = "Load referenced resources only as needed."
	base := skill.ResourceBase
	if base == nil {
		return []string{
			`Resources for this skill are managed by provider "` + EscapeText(skill.Provider) + `".`,
			loadHint,
		}
	}
	switch typed := base.(type) {
	case DirectoryBase:
		return []string{
			"Base directory for this skill: " + EscapeText(typed.Path),
			"Resolve relative paths mentioned by this skill against the base directory before using them. " + loadHint,
		}
	case URLBase:
		return []string{
			"Base URL for this skill: " + EscapeText(typed.URL),
			"Resolve relative URLs mentioned by this skill against the base URL before using them. " + loadHint,
		}
	case OpaqueBase:
		return []string{
			"Resources for this skill: " + EscapeText(typed.Description),
			loadHint,
		}
	}
	// [ResourceBase] 是封闭的，所以这里走不到——但一个 Go 的 type switch 没有
	// 穷尽性检查，走不到的分支仍然得写点什么。给最保守的那份提示，而不是崩掉：
	// 一份认不出基址的技能仍然是可用的。
	return []string{
		`Resources for this skill are managed by provider "` + EscapeText(skill.Provider) + `".`,
		loadHint,
	}
}

// escapeAttr 转义一段要放进 XML 属性值里的文本。
//
// 源: packages/skill/skill/src/index.ts:213-215
func escapeAttr(value string) string {
	return strings.NewReplacer("&", "&amp;", `"`, "&quot;", "<", "&lt;").Replace(value)
}

// EscapeText 转义一段嵌在技能标记里的、给模型看的散文。
//
// 源: packages/skill/skill/src/index.ts:227-229
//
// 提供方给的文本不许开得了或者闭得了框架标签，否则一份技能的描述就能伪造出
// `</available_skills>` 这种边界。
func EscapeText(value string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(value)
}

// CatalogSnapshot 是一次目录观察，外加这次发现是不是在一个稳定的 revision 里跑完的。
//
// 源: packages/skill/skill/src/index.ts:232-237
type CatalogSnapshot struct {
	// Skills 是这次观察到的、排过序的、不区分调用方的摘要。
	Skills []Summary
	// Complete 表示每个注册着的提供方都跑完了、并且期间没有发生目录改动。
	//
	// 它和 [Observation].Incomplete 反着写，是因为两者的**默认值该是什么**不一样：
	// Observation 是提供方填给本包的输入，零值该等于 DSH 那个数组简写的意思
	// （完整）；这个是本包算出来交给消费方的输出，没有零值默认可言。
	Complete bool
}

// Observation 是一次提供方发现的结果。
//
// 源: packages/skill/skill/src/index.ts:240-245
type Observation struct {
	// Candidates 是这次发现拿到的候选。
	Candidates []Candidate
	// Incomplete 表示这次发现没跑完，这批候选可用但**不许进缓存**。
	//
	// 新增: DSH 那边这个字段叫 complete，而且 list() 还可以直接返回一个数组当作
	// 「complete: true」的简写。Go 只有一种形状，所以字段反过来叫，零值就等于那个
	// 简写的意思——一个 `return skill.Observation{Candidates: found}, nil` 的提供方
	// 说的是「我跑完了」，那正是绝大多数提供方要说的话。
	Incomplete bool
}

// Provider 是一处技能来源，比如本地目录或者一个远端注册中心。
//
// 源: packages/skill/skill/src/index.ts:248-268
type Provider interface {
	// Name 是这个提供方在注册表里的唯一名字。
	Name() string

	// List 报出当前查找上下文下可用的候选。
	//
	// 提供方插件是在装配时**同步**注册进来的；远端初始化、认证、发现这些事都在
	// 这个方法里做。ctx 取消时应当尽快返回。
	List(ctx context.Context, options LookupOptions) (Observation, error)

	// Get 把一条之前报出去的候选读成完整的技能正文。
	//
	// 交回 nil 表示这份技能已经读不出来了——那不是错误，是「它没了」。
	Get(ctx context.Context, candidate Candidate, options LookupOptions) (*Definition, error)
}

// ProviderControl 是一次提供方注册借到的生命周期和失效通知。
//
// 源: packages/skill/skill/src/index.ts:271-276
type ProviderControl struct {
	// Context 在这次注册失败、或者这一次确切的注册被撤销时取消。
	//
	// 新增: DSH 是一个 AbortSignal，取消原因用 abort(reason) 带出去；Go 这边原因
	// 走 [context.Cause]。
	Context context.Context

	// Invalidate 让已经缓存下来的目录作废并通知消费方，**只在这一次确切的注册
	// 还活着的时候**有效。撤销之后再调它什么都不会发生。
	Invalidate func()
}
