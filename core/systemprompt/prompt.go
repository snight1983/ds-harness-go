// 本文件的作用：提示词这条缝上的**值**——一次装配长什么样、一段贡献怎么求值、
// 以及装配好的东西怎么渲染成模型真正读到的文本。注册表本身在 registry.go。
//
// 源: packages/core/system-prompt/src/index.ts:42-336

package systemprompt

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/llm"
)

// PersonaSection 是部署方那份人设占用的段落名。
//
// 源: packages/core/system-prompt/src/index.ts:166-172（PERSONA_SECTION）
//
// 它导出出来，是因为一份组合可以**替换**这个槽位——一个 agent 预设用自己的人设
// 盖掉部署方的那份。两边报同一个段落名，才叫替换；名字对不上就变成了并存。
const PersonaSection = "deployment:persona"

// PersonaOrder 是人设槽位的次序，也是模型读到的第一段。
//
// 源: packages/core/system-prompt/src/index.ts:131
const PersonaOrder = 0

// ToolOrderRest 是 [Options].ToolOrder 里给「没列出来的工具」留的那个位置。
//
// 源: packages/core/system-prompt/src/index.ts:180-181（TOOL_ORDER_REST）
const ToolOrderRest = "<unlisted-tools>"

// harnessIdentitySection 是宿主自己那句身份声明占用的段落名。
//
// 源: packages/core/system-prompt/src/index.ts:359
const harnessIdentitySection = "harness:identity"

// harnessIdentityOrder 排在人设前面：宿主先说自己是什么，部署方再说它是谁。
//
// 源: packages/core/system-prompt/src/index.ts:360
const harnessIdentityOrder = -100

// harnessIdentityText 是那句身份声明的正文。
//
// 源: packages/core/system-prompt/src/index.ts:361
const harnessIdentityText = "You are an AI agent powered by DeepSeek Harness."

// variableNamePattern 是提示词变量名的文法，也就是两个花括号中间允许写什么。
//
// 源: packages/core/system-prompt/src/index.ts:134
var variableNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// groupAtPattern 匹配扫描位置上一整组 `{{...}}` 引用，合不合法后面再判。
//
// 源: packages/core/system-prompt/src/index.ts:137
var groupAtPattern = regexp.MustCompile(`^\{\{([^{}]*)\}\}`)

// AssembleContext 是一次装配的上下文。
//
// 源: packages/core/system-prompt/src/index.ts:41-50（AssembleContext）
//
// 新增: DSH 这个类型是**可合并扩展**的（插件用 declare module 往上加字段），而且
// 带一个 signal。Go 没有声明合并，所以它只剩 Scope 一个字段；signal 那一半是每个
// 提供方和每条规则第一个参数上的 [context.Context]。
type AssembleContext struct {
	// Scope 是这次装配算谁的：它自己那一层加上父链上的层参与进来，登记在那条链上的
	// 规则也参与进来。为 nil 时只有全局层和没挂作用域的规则参与。
	Scope *scope.Key
}

// TextProvider 求一段在每次装配时才定下来的文本。
//
// 源: packages/core/system-prompt/src/index.ts:65（`string | ((context) => string)`）
//
// 新增: DSH 那个字段是「一段字符串**或者**一个函数」的联合。Go 里只留函数这一种
// 形状，固定文本用 [StaticText] 包一下——一个字段比「两个字段里只许填一个」好懂，
// 也就没有「两个都填了算谁的」这种问题。
type TextProvider func(ctx context.Context, assemble AssembleContext) (string, error)

// StaticText 把一段固定文本包成 [TextProvider]。
func StaticText(text string) TextProvider {
	return func(context.Context, AssembleContext) (string, error) { return text, nil }
}

// PromptSection 是系统提示词里的一段贡献。
//
// 源: packages/core/system-prompt/src/index.ts:53-73
type PromptSection struct {
	// Name 是这段贡献的唯一名字，同一层里重名会报错。
	Name string
	// Order 决定拼接次序，从小到大。
	//
	// 约定：-100 是宿主身份，0 是部署方人设，工具指引用 100–199；其他负数也排在
	// 人设前面。
	//
	// 新增: DSH 是 number，所以它得自己查 Number.isFinite。Go 的 int 天生有限，
	// 那道检查就没了。小数次序在 DSH 里也没人用过，而这套约定留出的间隔是 100。
	Order int
	// Text 求这段贡献的正文。正文里可以引用 `{{变量}}`，插值在 [RenderPrompt] 里做。
	Text TextProvider
	// Complete 表示把这一段当作**整份**系统提示词。
	//
	// 装配照样会跑完那条协作瀑布，好让工具、上下文、变量都解出来，然后再把这一段
	// 原样放回去当唯一的段落。同时生效的完整段落多于一个，装配就失败。
	Complete bool
}

// PromptContext 是一份动态上下文贡献，最终落成一条持久的 user 角色快照。
//
// 源: packages/core/system-prompt/src/index.ts:76-84（PromptContext）
type PromptContext struct {
	// Name 是这份贡献的唯一名字，同一层里重名会报错。
	Name string
	// Order 决定拼接次序，从小到大。
	Order int
	// Text 求这份贡献的正文，求出空串就等于什么都没贡献。
	Text TextProvider
}

// AssembledSection 是一段已经求过值、但还没插值的段落。
//
// 源: packages/core/system-prompt/src/index.ts:86-92（AssembledSection）
type AssembledSection struct {
	// Name 是做出这段贡献的那个段落的名字。
	Name string
	// Text 是求出来的正文，变量还没替换。
	Text string
}

// AssembledContext 是一份已经求过值、但还没插值的动态上下文。
//
// 源: packages/core/system-prompt/src/index.ts:94-100（AssembledContext）
type AssembledContext struct {
	// Name 是做出这份贡献的那份上下文的名字。
	Name string
	// Text 是求出来的正文，变量还没替换。
	Text string
}

// ToolProviderResult 是一个工具提供方对这一次装配的贡献。
//
// 源: packages/core/system-prompt/src/index.ts:102-108（ToolProviderResult）
type ToolProviderResult struct {
	// Schemas 是这次真正报给模型的那些工具。
	Schemas []llm.ToolSchema
	// KnownNames 是**还没做限制之前**的那份名字全集，用来校验配置里的次序。
	//
	// 为 nil 时按 Schemas 里的名字算。它存在的理由是：一个在某个作用域里被藏起来
	// 的工具，名字仍然是「登记过的」——配置里提到它不该被判成写错了名字。
	KnownNames []string
}

// ToolProvider 报出这一次装配里看得见的工具。
//
// 源: packages/core/system-prompt/src/index.ts:429
type ToolProvider func(ctx context.Context, assemble AssembleContext) (ToolProviderResult, error)

// VariableProvider 求一个提示词变量在这一次装配里的值。
//
// 源: packages/core/system-prompt/src/index.ts:446
//
// 交回 nil 表示这个变量这次**没有值**。它和「没注册过」不是一回事：没注册过的名字
// 是写错了；注册过但没值，是这次装配给不出来——引用到它的那一段渲染时会失败，而
// 没人引用它就什么事都没有。
type VariableProvider func(ctx context.Context, assemble AssembleContext) (*string, error)

// PromptAssembly 是一次装配的产物：模型这一步的全部输入。
//
// 源: packages/core/system-prompt/src/index.ts:110-119（PromptAssembly）
//
// 段落和上下文都还没插值，工具已经排成最终次序。
//
// 新增: DSH 这个类型也是可合并扩展的，插件往上加字段。Go 里它就是这四个字段。
type PromptAssembly struct {
	// Sections 是排过序、求过值的段落。
	Sections []AssembledSection
	// Contexts 是排过序、求过值的动态上下文；被压制时是空的。
	Contexts []AssembledContext
	// Tools 是已经排成最终次序的工具。
	Tools []llm.ToolSchema
	// Variables 是这次装配的变量表。
	//
	// 值为 nil 表示这个变量注册过但这次没值——所以这里是 `map[string]*string`
	// 而不是 `map[string]string`：[interpolate] 要分得清「名字不存在」和
	// 「名字在、值没有」，两者报的错不一样。
	Variables map[string]*string
}

// RenderPrompt 把一次装配渲染成最终的系统提示词。
//
// 源: packages/core/system-prompt/src/index.ts:255-268（renderPrompt）
//
// 它做三件事：严格插值 `{{变量}}`、丢掉空段落、剩下的用空行连起来。写坏了的引用、
// 不认识的名字、以及这次没有值的变量，都会报错。一个后面再也没有 `}}` 的孤零零的
// `{{` 算普通散文；替换进去的值不会被再扫一遍。
func RenderPrompt(assembly PromptAssembly) (string, error) {
	var rendered []string
	for _, section := range assembly.Sections {
		text, err := interpolate(section.Name, section.Text, assembly.Variables, "section")
		if err != nil {
			return "", err
		}
		if text != "" {
			rendered = append(rendered, text)
		}
	}
	return strings.Join(rendered, "\n\n"), nil
}

// RenderContextSnapshot 渲染出完整的那份动态上下文快照。
//
// 源: packages/core/system-prompt/src/index.ts:270-277（renderContextSnapshot）
func RenderContextSnapshot(assembly PromptAssembly) (string, error) {
	sections, err := RenderContextSections(assembly)
	if err != nil {
		return "", err
	}
	return JoinContextSections(sections), nil
}

// JoinContextSections 把已经渲染好的那些贡献连成模型看的快照文本。
//
// 源: packages/core/system-prompt/src/index.ts:279-291（joinContextSections）
//
// 一个既要那些贡献、又要这段文本的调用方，渲染一次然后在这里连起来，就不必把每份
// 上下文插值两遍。
func JoinContextSections(sections []llm.ContextSnapshotSection) string {
	parts := make([]string, 0, len(sections))
	for _, section := range sections {
		parts = append(parts, section.Text)
	}
	body := strings.Join(parts, "\n\n")
	if body == "" {
		return ""
	}
	return "Current runtime context. This snapshot supersedes earlier runtime-context snapshots.\n\n" + body
}

// RenderContextSections 渲染那份快照，但**留着它是由谁贡献的**。
//
// 源: packages/core/system-prompt/src/index.ts:293-306（renderContextSections）
//
// [RenderContextSnapshot] 把这些连给模型看；一个要把快照展示出来的消费方用这份
// 结果把每一块归到贡献它的子系统头上，而不必回头去拆那段连好的散文。
func RenderContextSections(assembly PromptAssembly) ([]llm.ContextSnapshotSection, error) {
	var sections []llm.ContextSnapshotSection
	for _, entry := range assembly.Contexts {
		text, err := interpolate(entry.Name, entry.Text, assembly.Variables, "context")
		if err != nil {
			return nil, err
		}
		if text != "" {
			sections = append(sections, llm.ContextSnapshotSection{Name: entry.Name, Text: text})
		}
	}
	return sections, nil
}

// interpolate 给一段段落或者上下文插值，并把诊断归到贡献它的那一项头上。
//
// 源: packages/core/system-prompt/src/index.ts:258-290
func interpolate(name, text string, variables map[string]*string, kind string) (string, error) {
	var builder strings.Builder
	last := 0
	for {
		offset := strings.Index(text[last:], "{{")
		if offset < 0 {
			break
		}
		open := last + offset

		group := groupAtPattern.FindStringSubmatch(text[open:])
		if group == nil {
			// 后面还有一个 `}}`，这就是写坏了；否则它只是一段普通散文。
			if strings.Contains(text[open+2:], "}}") {
				return "", fmt.Errorf(
					"malformed prompt variable reference at %q… in %s %q (references are complete simple {{name}} groups)",
					truncateRunes(text[open:], 16), kind, name,
				)
			}
			builder.WriteString(text[last : open+2])
			last = open + 2
			continue
		}

		// `{{}}` 解出来是个空名字，它走的也是「写坏了」这一支。
		variable := group[1]
		if !variableNamePattern.MatchString(variable) {
			return "", fmt.Errorf(
				"malformed prompt variable reference \"{{%s}}\" in %s %q (variable names match %s)",
				variable, kind, name, variableNamePattern.String(),
			)
		}
		value, registered := variables[variable]
		if !registered {
			return "", fmt.Errorf(
				"unknown prompt variable \"{{%s}}\" in %s %q; registered variables: %s",
				variable, kind, name, knownVariableList(variables),
			)
		}
		if value == nil {
			return "", fmt.Errorf(
				"prompt variable \"{{%s}}\" has no value for this assembly (%s %q)",
				variable, kind, name,
			)
		}

		builder.WriteString(text[last:open])
		builder.WriteString(*value)
		last = open + len(group[0])
	}
	builder.WriteString(text[last:])
	return builder.String(), nil
}

// knownVariableList 是「不认识的变量」那条诊断里跟着的那份已注册名单。
//
// 源: packages/core/system-prompt/src/index.ts:283
//
// 新增: DSH 用 Object.keys 拿到的是插入顺序。Go 的 map 没有顺序，所以这里排序——
// 一条诊断里的名单每次跑出来都不一样，比顺序不对更难用。
func knownVariableList(variables map[string]*string) string {
	if len(variables) == 0 {
		return "(none)"
	}
	names := make([]string, 0, len(variables))
	for name := range variables {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// truncateRunes 截出前 limit 个字符，用在诊断里的那一小段摘录上。
//
// 新增: DSH 那边是 `text.slice(open, open + 16)`，按 UTF-16 码元切。Go 的字符串按
// 字节切会把一个多字节字符劈成两半，所以这里按字符（rune）截。
func truncateRunes(text string, limit int) string {
	count := 0
	for offset := range text {
		if count == limit {
			return text[:offset]
		}
		count++
	}
	return text
}

// validateToolOrder 查一份配置里的工具次序：不许重名，而且必须留出那个 rest 位置。
//
// 源: packages/core/system-prompt/src/index.ts:146-157
//
// 名字本身认不认得出来是**装配**时才查的，因为配置读进来的时候插件还没加载完。
func validateToolOrder(toolOrder []string) error {
	if toolOrder == nil {
		return nil
	}
	seen := map[string]struct{}{}
	for _, name := range toolOrder {
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("%w: toolOrder lists %q more than once", ErrInvalidConfig, name)
		}
		seen[name] = struct{}{}
	}
	if _, found := seen[ToolOrderRest]; !found {
		return fmt.Errorf(
			"%w: toolOrder must contain the %q rest entry (where unlisted tools are inserted)",
			ErrInvalidConfig, ToolOrderRest,
		)
	}
	return nil
}

// orderTools 按配置里的次序排工具，没列出来的按字典序插在 [ToolOrderRest] 那个位置。
//
// 源: packages/core/system-prompt/src/index.ts:164-179
//
// 配置里写了但根本不存在的名字会让装配失败；存在、只是在这个作用域里被藏起来的
// 名字可以缺席。
func orderTools(tools []llm.ToolSchema, toolOrder []string, knownNames map[string]struct{}) ([]llm.ToolSchema, error) {
	for _, tool := range tools {
		if tool.Name == ToolOrderRest {
			return nil, fmt.Errorf(
				"%w: tool provider returned reserved tool name %q (reserved for toolOrder's rest entry)",
				ErrInvalidAssembly, ToolOrderRest,
			)
		}
	}
	if toolOrder == nil {
		sortToolsByName(tools)
		return tools, nil
	}

	var unknown []string
	for _, name := range toolOrder {
		if name == ToolOrderRest {
			continue
		}
		if _, known := knownNames[name]; !known {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		plural := ""
		if len(unknown) > 1 {
			plural = "s"
		}
		quoted := make([]string, 0, len(unknown))
		for _, name := range unknown {
			quoted = append(quoted, fmt.Sprintf("%q", name))
		}
		return nil, fmt.Errorf(
			"%w: toolOrder lists unregistered tool%s %s; known tools: %s",
			ErrInvalidAssembly, plural, strings.Join(quoted, ", "), knownToolList(knownNames),
		)
	}

	listed := map[string]struct{}{}
	for _, name := range toolOrder {
		listed[name] = struct{}{}
	}
	var rest []llm.ToolSchema
	for _, tool := range tools {
		if _, isListed := listed[tool.Name]; !isListed {
			rest = append(rest, tool)
		}
	}
	sortToolsByName(rest)

	ordered := make([]llm.ToolSchema, 0, len(tools))
	for _, name := range toolOrder {
		if name == ToolOrderRest {
			ordered = append(ordered, rest...)
			continue
		}
		// 同一个名字可能有不止一份（不同提供方各报了一个），全都留下，按提供方顺序。
		for _, tool := range tools {
			if tool.Name == name {
				ordered = append(ordered, tool)
			}
		}
	}
	return ordered, nil
}

// knownToolList 是「配置里写了个不认识的名字」那条诊断里跟着的名单。
//
// 源: packages/core/system-prompt/src/index.ts:171
func knownToolList(knownNames map[string]struct{}) string {
	if len(knownNames) == 0 {
		return "(none)"
	}
	names := make([]string, 0, len(knownNames))
	for name := range knownNames {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// sortToolsByName 按名字的字典序**就地**排序，好让每台机器上排出来的次序一样。
//
// 源: packages/core/system-prompt/src/index.ts:183-185
//
// 新增: DSH 比的是 UTF-16 码元，Go 比的是 UTF-8 字节。两者只在名字里出现补充平面
// 字符时才会不同，而工具名在实践中是 ASCII。
func sortToolsByName(tools []llm.ToolSchema) {
	sort.SliceStable(tools, func(left, right int) bool {
		return tools[left].Name < tools[right].Name
	})
}
