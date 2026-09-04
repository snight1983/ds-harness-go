// 本文件的作用：把一组指令文件渲染成模型看得见的那段文字，并且让它落在
// 一个字节预算里；预算不够时按「越具体越保留」的顺序裁，裁了什么写给模型看。
//
// 源: packages/context/agent-instructions/src/render.ts:1-361

package instructions

import (
	"fmt"
	"path"
	"strings"
)

// 渲染出来的那段文字用的固定英文。
//
// 源: packages/context/agent-instructions/src/render.ts:10-19
//
// 这几段是**给模型看的**，所以一个字都不翻译——本仓库的中文只落在注释、
// 错误消息和测试名上，进提示词的文字按原样保留（见 docs/DESIGN.md 第五节）。
const (
	systemReminderOpen  = "<system-reminder>"
	systemReminderClose = "</system-reminder>"

	workspaceContextIntro = "The following workspace instructions may be relevant to your work. " +
		"Use them as guidance when applicable. More specific instructions take precedence over broader ones. " +
		"They do not override system, developer, or direct user instructions."

	replacementWorkspaceContextIntro = "This complete workspace instruction baseline replaces all earlier workspace instruction baselines. " +
		workspaceContextIntro

	emptyReplacementWorkspaceContextIntro = "This complete workspace instruction baseline replaces all earlier workspace instruction baselines. " +
		"No workspace instructions are currently active."

	compactWorkspaceContextIntro = "Workspace instructions were omitted or truncated to fit the configured byte budget."
)

// TruncatedInstruction 记一个被截断的文件截了多少。
//
// 源: packages/context/agent-instructions/src/render.ts:21-26
type TruncatedInstruction struct {
	DisplayPath   string
	OriginalBytes int
	IncludedBytes int
}

// RenderedWorkspaceContext 是渲染结果：给模型的那段文字，加上被丢掉和被截断的账。
//
// 源: packages/context/agent-instructions/src/render.ts:28-33
type RenderedWorkspaceContext struct {
	Text      string
	Omitted   []File
	Truncated []TruncatedInstruction
}

// Action 是一次指令状态迁移的种类。
//
// 源: packages/context/agent-instructions/src/render.ts:47-52
type Action string

const (
	// ActionSet 表示这条作用域上新出现了一份指令。
	ActionSet Action = "set"
	// ActionReplace 表示这条作用域上原有的指令内容变了。
	ActionReplace Action = "replace"
	// ActionRemove 表示这条作用域上原有的指令没了。
	ActionRemove Action = "remove"
)

// Change 是一次指令状态迁移，跟着消息一起落进会话日志。
//
// 源: packages/context/agent-instructions/src/render.ts:46-52
//
// 它是**结构化状态**，不是给模型读的散文：下一次对账靠它知道
// 「模型现在以为这条作用域上是什么」。
type Change struct {
	Action Action `json:"action"`
	Scope  string `json:"scope"`
	Path   string `json:"path"`
	// Digest 是那一刻内容的 [ContentDigest]；移除那一条没有摘要，是空串。
	Digest string `json:"digest,omitempty"`
}

// ChangeRenderItem 把一次迁移和渲染它要用的内容配成一对。
//
// 源: packages/context/agent-instructions/src/render.ts:54-58
type ChangeRenderItem struct {
	Change Change
	File   LoadedFile
}

// renderStyle 是一次渲染的开场白和每一节怎么写。
//
// 源: packages/context/agent-instructions/src/render.ts:60-63
type renderStyle struct {
	intro   string
	section func(LoadedFile) string
}

// byteLength 给出一段文字的 UTF-8 字节数。
//
// 源: packages/context/agent-instructions/src/render.ts:65-67
//
// Go 的 string 本来就是 UTF-8 字节，所以这就是 len。留着这个名字是为了让
// 每一处预算计算都指向同一个概念：这里量的**永远是字节**，不是字符也不是码点。
func byteLength(value string) int { return len(value) }

// truncateUTF8 把一段文字截到不超过 maxBytes 个字节，且不切碎码点。
//
// 源: packages/context/agent-instructions/src/render.ts:69-79
func truncateUTF8(value string, maxBytes int) string {
	if byteLength(value) <= maxBytes {
		return value
	}
	end := maxBytes
	if end < 0 {
		end = 0
	}
	// 第一个被排除的字节要是一个 UTF-8 续字节，说明这一刀切在了某个码点中间。
	// 退回它的首字节，连首字节一起排除掉。
	for end > 0 && value[end]&0xc0 == 0x80 {
		end--
	}
	return value[:end]
}

// escapeInstructionFrameBody 把正文里的闭合标记打断，免得指令内容把框提前关掉。
//
// 源: packages/context/agent-instructions/src/render.ts:81-83
//
// 一份指令文件里正好写着这个闭合标记的话，不打断就等于让文件内容决定
// 「模型认为工作区指令到哪里为止」，后面的文字就跑到框外面去了。
func escapeInstructionFrameBody(body string) string {
	return strings.ReplaceAll(body, systemReminderClose, `<\/system-reminder>`)
}

// sectionText 是基线里一节的写法。
//
// 源: packages/context/agent-instructions/src/render.ts:85-87
func sectionText(file LoadedFile) string {
	return "Instructions from: " + file.DisplayPath + "\n\n" + file.Content
}

// UserGlobalDirectory 是那个唯一的用户全局作用域的目录名。
//
// 源: packages/context/agent-instructions/src/render.ts:89-90
const UserGlobalDirectory = "user-global"

// UserGlobalFile 是用户全局目录下那个唯一的指令文件名。
//
// 源: packages/context/agent-instructions/src/render.ts:92-98
//
// 发现（`<UserGlobalRoot>/<名字>`）和对账（用户全局作用域键里的候选名那一段）
// 都认这个名字，所以它只能有一处定义：两边一旦不一致，那份用户全局指令
// 会加载得出来，却永远对不上账。
const UserGlobalFile = "AGENTS.md"

// UserGlobalDisplayPath 是用户全局指令给模型看的那条固定路径。
//
// 新增: DSH 这条路径是 `~/.dsh/AGENTS.md` 或者 `$DSH_HOME/AGENTS.md`，
// 随本机 home 变。本包没有 home 这个概念（见包文档），所以它是一个常数。
// 常数化顺带让 [ScopeForDisplayPath] 塌成一次 path.Dir——因为这条路径的
// 目录名**就是** [UserGlobalDirectory]。
const UserGlobalDisplayPath = UserGlobalDirectory + "/" + UserGlobalFile

// ScopeForDisplayPath 从一条显示路径推出它的逻辑作用域目录。
//
// 源: packages/context/agent-instructions/src/render.ts:100-108
//
// 新增: DSH 在这里特判两个字面量把用户全局那条映射到 [UserGlobalDirectory]。
// 本包不用特判：[UserGlobalDisplayPath] 的目录名本来就是那个常数。
func ScopeForDisplayPath(displayPath string) string {
	return path.Dir(displayPath)
}

// scopeSeparator 把作用域键的两段隔开。
//
// 源: packages/context/agent-instructions/src/render.ts:110
//
// 用 NUL 是因为目录路径和文件名都不可能含它，所以这个编码不会有歧义。
const scopeSeparator = "\x00"

// CandidateScopeKey 把一个目录和一个候选文件名拼成对账用的作用域键。
//
// 源: packages/context/agent-instructions/src/render.ts:112-125
//
// 每个候选各自记账，所以键是「目录 + 候选名」而不是只有目录：同一个目录里的
// AGENTS.md 和 CLAUDE.md、基础文件和它的 .local 覆盖，在按作用域索引的那几张表里
// 必须互不相撞。
func CandidateScopeKey(directory string, candidateName string) string {
	return directory + scopeSeparator + candidateName
}

// InstructionScopeKey 给一个已加载文件算出它的作用域键。
//
// 源: packages/context/agent-instructions/src/render.ts:127-134
func InstructionScopeKey(displayPath string) string {
	return CandidateScopeKey(ScopeForDisplayPath(displayPath), path.Base(displayPath))
}

// DecodeScopeKey 把 [CandidateScopeKey] 编进去的两段拆回来。
//
// 源: packages/context/agent-instructions/src/render.ts:136-146
func DecodeScopeKey(scope string) (directory string, candidateName string) {
	separator := strings.Index(scope, scopeSeparator)
	if separator < 0 {
		// 每个作用域键都由 CandidateScopeKey 产出，那里一定插了分隔符。
		// 这一支只在有人手写了一个键的时候到得了，给它一个不会崩的解释。
		return scope, ""
	}
	return scope[:separator], scope[separator+len(scopeSeparator):]
}

// additionalSectionText 是增量里「新增了一份指令」那一节的写法。
//
// 源: packages/context/agent-instructions/src/render.ts:148-157
func additionalSectionText(file LoadedFile) string {
	scope := ScopeForDisplayPath(file.DisplayPath)
	return strings.Join([]string{
		"Additional instructions from: " + file.DisplayPath,
		"",
		"These instructions apply to work under `" + scope + "`. Use them as guidance when relevant; more specific instructions take precedence. They do not override system, developer, or direct user instructions.",
		"",
		file.Content,
	}, "\n")
}

// baselineRenderStyle 给基线挑开场白。
//
// 源: packages/context/agent-instructions/src/render.ts:159-169
//
// 新增: DSH 的 `replacePreviousBaseline` 是 `boolean | undefined` 三态，
// 但它只判 `!== true`，也就是 undefined 和 false 走同一条路。Go 这边一个
// bool 就完全等价，没有信息丢失。
func baselineRenderStyle(files []LoadedFile, replacePreviousBaseline bool) renderStyle {
	style := renderStyle{intro: workspaceContextIntro, section: sectionText}
	if !replacePreviousBaseline {
		return style
	}
	if len(files) == 0 {
		style.intro = emptyReplacementWorkspaceContextIntro
	} else {
		style.intro = replacementWorkspaceContextIntro
	}
	return style
}

// changedSectionText 是增量里一节的写法，按迁移种类分三种。
//
// 源: packages/context/agent-instructions/src/render.ts:171-184
func changedSectionText(item ChangeRenderItem) string {
	switch item.Change.Action {
	case ActionSet:
		return additionalSectionText(item.File)
	case ActionRemove:
		return "Instructions removed: " + item.Change.Path +
			"\n\nThe previously loaded instructions from this file no longer apply."
	default:
		return strings.Join([]string{
			"Updated instructions from: " + item.Change.Path,
			"",
			"This file changed after it was loaded. Use the following content instead of the previously loaded instructions from this file.",
			"",
			item.File.Content,
		}, "\n")
	}
}

// RenderInstructionChanges 渲染一批对账结果，并且只留下预算装得下的那几条迁移。
//
// 源: packages/context/agent-instructions/src/render.ts:186-213
//
// 返回的迁移**必须**是被渲染出来的那几条：把一条没渲染出来的迁移记进会话状态，
// 下一次对账就会以为模型已经知道了，然后永远不再发它。
func RenderInstructionChanges(items []ChangeRenderItem, maxBytes int) (string, []Change) {
	byAbsolutePath := make(map[string]ChangeRenderItem, len(items))
	files := make([]LoadedFile, 0, len(items))
	for _, item := range items {
		byAbsolutePath[item.File.AbsolutePath] = item
		files = append(files, item.File)
	}
	style := renderStyle{
		intro: "",
		section: func(file LoadedFile) string {
			item, ok := byAbsolutePath[file.AbsolutePath]
			if !ok {
				// 渲染器拿到的就是造这张表用的那些文件，到不了这里。
				return ""
			}
			// 传进来的 file 可能是被截断过的那一份，所以用它而不是表里那份。
			item.File = file
			return changedSectionText(item)
		},
	}
	rendered, represented := renderInstructionContext(files, maxBytes, style)

	representedPaths := make(map[string]struct{}, len(represented))
	for _, file := range represented {
		representedPaths[file.AbsolutePath] = struct{}{}
	}
	changes := make([]Change, 0, len(items))
	for _, item := range items {
		if _, ok := representedPaths[item.File.AbsolutePath]; ok {
			changes = append(changes, item.Change)
		}
	}
	return rendered.Text, changes
}

// markerText 写那行「预算多少、丢了什么、截了多少」的账。
//
// 源: packages/context/agent-instructions/src/render.ts:215-225
//
// 这行账是**给模型看的**：不写的话，模型会以为自己看见的就是全部工作区指令，
// 然后在一份被裁掉的规则上自信地做决定。
func markerText(maxBytes int, omitted []File, truncated []TruncatedInstruction) string {
	if len(omitted) == 0 && len(truncated) == 0 {
		return ""
	}
	parts := make([]string, 0, 2)
	if len(omitted) > 0 {
		names := make([]string, 0, len(omitted))
		for _, file := range omitted {
			names = append(names, file.DisplayPath)
		}
		parts = append(parts, "omitted "+strings.Join(names, ", "))
	}
	if len(truncated) > 0 {
		notes := make([]string, 0, len(truncated))
		for _, item := range truncated {
			notes = append(notes, fmt.Sprintf("%s from %d to %d bytes",
				item.DisplayPath, item.OriginalBytes, item.IncludedBytes))
		}
		parts = append(parts, "truncated "+strings.Join(notes, ", "))
	}
	return fmt.Sprintf("Workspace instruction budget %d bytes: %s", maxBytes, strings.Join(parts, "; "))
}

// buildInstructionText 把账、开场白和各节拼成最终那段带框的文字。
//
// 源: packages/context/agent-instructions/src/render.ts:227-243
func buildInstructionText(
	files []LoadedFile,
	maxBytes int,
	omitted []File,
	truncated []TruncatedInstruction,
	style renderStyle,
) string {
	blocks := make([]string, 0, len(files)+2)
	for _, block := range append([]string{markerText(maxBytes, omitted, truncated), style.intro}, sectionsOf(files, style)...) {
		if len(block) > 0 {
			blocks = append(blocks, block)
		}
	}
	// 框由产出方自己打进内容里：会话表面层原样投影上下文，不替谁包一层。
	return systemReminderOpen + "\n" +
		escapeInstructionFrameBody(strings.Join(blocks, "\n\n")) + "\n" +
		systemReminderClose
}

func sectionsOf(files []LoadedFile, style renderStyle) []string {
	sections := make([]string, 0, len(files))
	for _, file := range files {
		sections = append(sections, style.section(file))
	}
	return sections
}

// withTruncatedContent 复制一份内容被截到指定字节数的文件。
//
// 源: packages/context/agent-instructions/src/render.ts:245-247
func withTruncatedContent(file LoadedFile, includedBytes int) LoadedFile {
	file.Content = truncateUTF8(file.Content, includedBytes)
	return file
}

// truncateToFit 二分找出「截到多少字节还装得下」。
//
// 源: packages/context/agent-instructions/src/render.ts:249-273
//
// 二分而不是一次算出来，是因为装不装得下取决于**整段文字**的长度，
// 而那行账里写的字节数本身会随截断长度变化（101 比 99 多一个字符），
// 所以没有闭式解。
func truncateToFit(
	file LoadedFile,
	includedFiles []LoadedFile,
	maxBytes int,
	omitted []File,
	style renderStyle,
) LoadedFile {
	originalBytes := byteLength(file.Content)
	low, high := 0, originalBytes
	best := withTruncatedContent(file, 0)
	for low <= high {
		mid := (low + high) / 2
		candidate := withTruncatedContent(file, mid)
		truncated := []TruncatedInstruction{{
			DisplayPath:   file.DisplayPath,
			OriginalBytes: originalBytes,
			IncludedBytes: byteLength(candidate.Content),
		}}
		text := buildInstructionText(append(append([]LoadedFile{}, includedFiles...), candidate),
			maxBytes, omitted, truncated, style)
		if byteLength(text) <= maxBytes {
			best = candidate
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	return best
}

// renderInstructionContext 是预算裁剪的那段阶梯，从最好的往最差的退。
//
// 源: packages/context/agent-instructions/src/render.ts:275-332
//
// 顺序是：全装下 → 从最宽的一头开始整份丢 → 只留最具体的那一份并截断它
// （先用原开场白，装不下再换成短开场白）→ 只留一行账加一个标题 → 只留那行账。
// 「最具体的优先」这条贯穿始终：files 是从宽到窄排好的，越靠后越具体，
// 而具体的指令是对当前工作最相关的那一份。
//
// 第二个返回值是**语义上被这段文字代表了的**原始文件，它不是 Omitted 的补集：
// 一个被截断的文件既在这里也在 Truncated 里，而一个只剩标题的文件两边都不在。
// 一个本来就空的文件只要标题活着就算被代表——那个标题传达的正是
// 「这份指令存在，且没有内容」。
func renderInstructionContext(
	files []LoadedFile,
	maxBytes int,
	style renderStyle,
) (RenderedWorkspaceContext, []LoadedFile) {
	// 新增: DSH 还判一次 !Number.isFinite(maxBytes)。Go 的 int 没有无穷，
	// 那一半的判断在这里根本表达不出来。
	if maxBytes <= 0 {
		return RenderedWorkspaceContext{Text: "", Omitted: filesOf(files), Truncated: nil}, nil
	}

	fullText := buildInstructionText(files, maxBytes, nil, nil, style)
	if byteLength(fullText) <= maxBytes {
		return RenderedWorkspaceContext{Text: fullText}, files
	}

	for start := 1; start < len(files); start++ {
		included := files[start:]
		omitted := filesOf(files[:start])
		suffixText := buildInstructionText(included, maxBytes, omitted, nil, style)
		if byteLength(suffixText) <= maxBytes {
			return RenderedWorkspaceContext{Text: suffixText, Omitted: omitted}, included
		}
	}

	if len(files) == 0 {
		// 上面那次 fullText 是从一个非空结果算出来的，所以到不了这里。
		return RenderedWorkspaceContext{}, nil
	}
	mostSpecific := files[len(files)-1]
	omitted := filesOf(files[:len(files)-1])
	originalBytes := byteLength(mostSpecific.Content)

	compactStyle := style
	compactStyle.intro = compactWorkspaceContextIntro
	for _, candidateStyle := range []renderStyle{style, compactStyle} {
		truncatedFile := truncateToFit(mostSpecific, nil, maxBytes, omitted, candidateStyle)
		includedBytes := byteLength(truncatedFile.Content)
		truncated := []TruncatedInstruction{{
			DisplayPath:   mostSpecific.DisplayPath,
			OriginalBytes: originalBytes,
			IncludedBytes: includedBytes,
		}}
		text := buildInstructionText([]LoadedFile{truncatedFile}, maxBytes, omitted, truncated, candidateStyle)
		if byteLength(text) <= maxBytes {
			var represented []LoadedFile
			if includedBytes > 0 || originalBytes == 0 {
				represented = []LoadedFile{mostSpecific}
			}
			return RenderedWorkspaceContext{Text: text, Omitted: omitted, Truncated: truncated}, represented
		}
	}

	truncated := []TruncatedInstruction{{
		DisplayPath:   mostSpecific.DisplayPath,
		OriginalBytes: originalBytes,
		IncludedBytes: 0,
	}}
	// 到这里连框都放不下了，所以下面这两种退化写法**不带框**。
	compactNotice := escapeInstructionFrameBody(markerText(maxBytes, omitted, truncated))
	compactWithHeading := escapeInstructionFrameBody(
		compactNotice + "\n\n" + style.section(withTruncatedContent(mostSpecific, 0)))
	if byteLength(compactWithHeading) <= maxBytes {
		var represented []LoadedFile
		if originalBytes == 0 {
			represented = []LoadedFile{mostSpecific}
		}
		return RenderedWorkspaceContext{Text: compactWithHeading, Omitted: omitted, Truncated: truncated}, represented
	}
	text := compactNotice
	if byteLength(text) > maxBytes {
		text = truncateUTF8(text, maxBytes)
	}
	return RenderedWorkspaceContext{Text: text, Omitted: omitted, Truncated: truncated}, nil
}

// filesOf 把已加载文件降级成只有两条路径的形状。
func filesOf(loaded []LoadedFile) []File {
	if len(loaded) == 0 {
		return nil
	}
	files := make([]File, 0, len(loaded))
	for _, file := range loaded {
		files = append(files, File{AbsolutePath: file.AbsolutePath, DisplayPath: file.DisplayPath})
	}
	return files
}

// RenderWorkspaceInstructionSet 渲染一份基线，并交出语义上真被代表了的那些文件。
//
// 源: packages/context/agent-instructions/src/render.ts:334-348
//
// 第二个返回值是给会话状态用的：只有真被渲染进去的文件，才配在
// [BaselineState] 里记一条「模型已经知道了」。
func RenderWorkspaceInstructionSet(
	files []LoadedFile,
	maxBytes int,
	replacePreviousBaseline bool,
) (RenderedWorkspaceContext, []LoadedFile) {
	return renderInstructionContext(files, maxBytes, baselineRenderStyle(files, replacePreviousBaseline))
}

// RenderWorkspaceContext 渲染基线那条链，只要渲染结果。
//
// 源: packages/context/agent-instructions/src/render.ts:350-361
func RenderWorkspaceContext(
	files []LoadedFile,
	maxBytes int,
	replacePreviousBaseline bool,
) RenderedWorkspaceContext {
	rendered, _ := RenderWorkspaceInstructionSet(files, maxBytes, replacePreviousBaseline)
	return rendered
}
