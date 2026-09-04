// 本文件的作用：那份发布给模型的技能目录——它在日志里持久的样子、它渲染出来的
// 样子、以及「这份目录和上一份是不是同一份」怎么判。
//
// 源: packages/skill/tool-skill/src/index.ts:28-58,254-394

package skilltool

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/snight1983/ds-harness-go/feature/skill"
	"github.com/snight1983/ds-harness-go/llm"
)

// CatalogEntry 是目录里的一条，也就是渲染出来的那一行背后的事实。
//
// 源: packages/skill/tool-skill/src/index.ts:40
type CatalogEntry struct {
	// Name 是技能名。
	Name string `json:"name"`
	// Description 是已经规范化、并且截到上限的那句说明（**没有**转义）。
	Description string `json:"description"`
}

// CatalogSource 是一条目录消息在日志里带的那点出处。
//
// 源: packages/skill/tool-skill/src/index.ts:34-41
//
// 目录是一份 catalog 形态的上下文，所以它把自己发布了哪些条目**记在散文旁边**：
// 一个要把这份清单显示出来的消费方不该回头去解 `<available_skills>` 那段文本——
// 那段框架是写给模型看的，它的形状随时可能为了模型的表现而改。
//
// 新增: DSH 那边这是 llm 的 MessageSourceMap 上一个自己的 kind。Go 里它排进
// [llm.PluginSource.Extra]，产出方名字是 [CatalogPlugin]，形态是
// [llm.CatalogContext]；介质上排出来的字节和 DSH 一致。理由见 [CatalogPlugin]。
type CatalogSource struct {
	// Update 为真表示这是一份**替换**目录，不是本次会话第一次发布。
	Update bool `json:"update,omitempty"`
	// Entries 正是这条消息发布的那些条目，按目录顺序。
	Entries []CatalogEntry `json:"entries"`
}

// newCatalogSource 把一份目录出处排成一条消息来源。
func newCatalogSource(source CatalogSource) (llm.PluginSource, error) {
	if source.Entries == nil {
		// 一份空目录也要排成 `[]` 而不是 `null`：读回来那一侧靠「是不是数组」
		// 判这条记录认不认得（见 [readCatalogEntries]），null 会让本包自己
		// 刚写下的目录在下一个步骤里认不出来。
		source.Entries = []CatalogEntry{}
	}
	extra, err := json.Marshal(source)
	if err != nil {
		return llm.PluginSource{}, err
	}
	return llm.PluginSource{Plugin: CatalogPlugin, Context: llm.CatalogContext{}, Extra: extra}, nil
}

// catalogEntriesOf 把一条消息来源上那份条目表取回来。
//
// 第二个返回值为假表示这条来源不是一份**读得懂的**本包目录——它可能根本不是
// 本包发的，也可能是本包发的但那几个字段读不回来。
//
// 源: packages/skill/tool-skill/src/index.ts:348-359（readCatalogEntries）
//
// 这两种情形在这里**不区分**，和 DSH 一致：日志可能是恢复的、分叉的、或者外部
// 写进来的，种子校验只保证 source 是个带非空 kind 的对象，没有任何一个按 kind
// 分的字段被查过。一条读不回来的记录当成「不是本包的目录」，而不是在步骤监听器
// 里抛错——那会让这个会话之后每一个回合都失败。
func catalogEntriesOf(source llm.MessageSource) ([]CatalogEntry, bool) {
	plugin, ok := source.(llm.PluginSource)
	if !ok || plugin.Plugin != CatalogPlugin || len(plugin.Extra) == 0 {
		return nil, false
	}
	// 先解成一张宽松的表再逐条查，而不是直接解进 [CatalogSource]：encoding/json
	// 对类型不符的字段会整体报错，但对**缺字段**则默默留零值，于是一条
	// `{"entries":[{"name":""}]}` 会被当成一条合法目录。DSH 那边是逐字段查的。
	var wire struct {
		Entries []struct {
			Name        *string `json:"name"`
			Description *string `json:"description"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(plugin.Extra, &wire); err != nil {
		return nil, false
	}
	if wire.Entries == nil {
		return nil, false
	}
	entries := make([]CatalogEntry, 0, len(wire.Entries))
	for _, entry := range wire.Entries {
		if entry.Name == nil || *entry.Name == "" || entry.Description == nil {
			return nil, false
		}
		entries = append(entries, CatalogEntry{Name: *entry.Name, Description: *entry.Description})
	}
	return entries, true
}

// catalogEntries 把一份技能摘要表折成目录条目。
//
// 源: packages/skill/tool-skill/src/index.ts:50-58
func catalogEntries(skills []skill.Summary, descriptionMaxLength int) []CatalogEntry {
	entries := make([]CatalogEntry, 0, len(skills))
	for _, summary := range skills {
		entries = append(entries, CatalogEntry{
			Name:        summary.Name,
			Description: catalogDescription(summary.Description, descriptionMaxLength),
		})
	}
	return entries
}

// catalogDescription 是目录发布出去的那句说明：空白折成单个空格、两头掐掉、
// 超长就截。**不**转义——转义属于渲染那一层，见 [renderCatalogEntries]。
//
// 源: packages/skill/tool-skill/src/index.ts:391-394
func catalogDescription(value string, maxLength int) string {
	normalized := strings.Join(strings.Fields(value), " ")
	// 新增: DSH 的 length 与 slice 按 UTF-16 码元算，Go 的 len 与切片按字节算。
	// 这里按**符文**算：按字节切会把一个多字节字符劈成两半，交给模型一段
	// 不合法的 UTF-8，而中文说明里每个字都是多字节的。
	runes := []rune(normalized)
	if len(runes) <= maxLength {
		return normalized
	}
	return string(runes[:maxLength-3]) + "..."
}

// digestCatalogEntries 算一份目录的身份。
//
// 源: packages/skill/tool-skill/src/index.ts:328-335
//
// 算的是那份持久的条目表，不是渲染出来的散文：变的是条目，而外面那圈
// `<system-reminder>` 框架是写给模型看的，不该由它来决定要不要重新发布。
func digestCatalogEntries(entries []CatalogEntry) string {
	// 每条各排一次 JSON，而不是拿某个分隔符拼起来：任何一个分隔符本身都是
	// 说明里的合法字符，只有加引号才把边界划准。
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		encoded, err := json.Marshal([]string{entry.Name, entry.Description})
		if err != nil {
			// 不可达：两个字段都是 string。
			continue
		}
		lines = append(lines, string(encoded))
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// renderCatalogEntries 是目录那几行面向模型的样子。
//
// 源: packages/skill/tool-skill/src/index.ts:319-321
//
// 伪 XML 的转义属于这层框架、不属于那份发布出去的事实，所以在这里做、从不存下去。
// 名字过了 [skill.IsName]，不含任何需要转义的字符。
func renderCatalogEntries(entries []CatalogEntry) []string {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		lines = append(lines, "- `"+entry.Name+"`: "+skill.EscapeText(entry.Description))
	}
	return lines
}

// renderCatalogMessage 造本次会话**第一份**目录消息。
//
// 源: packages/skill/tool-skill/src/index.ts:254-277
func renderCatalogMessage(entries []CatalogEntry) (llm.Message, error) {
	lines := []string{
		"<system-reminder>",
		"A skill is a reusable set of task-specific instructions. The following skills are available in this session:",
		"",
		"<available_skills>",
	}
	lines = append(lines, renderCatalogEntries(entries)...)
	lines = append(lines,
		"</available_skills>",
		"",
		"If the user names a skill, or the task clearly matches a skill's description, call the `skill` tool with the exact skill name before taking task actions. Load all applicable skills, then follow their full instructions. This catalog contains summaries only; do not infer or follow a skill's instructions until it has been loaded.",
		"A user may also invoke a skill directly; its <skill_content> block then appears in this conversation. Follow it, and do not call the `skill` tool again for that skill.",
		"</system-reminder>",
	)
	return catalogMessageOf(lines, CatalogSource{Entries: entries})
}

// renderCatalogUpdate 造一份**替换**目录消息。
//
// 源: packages/skill/tool-skill/src/index.ts:279-311
//
// 替换版和第一版分成两段文字，是因为它要多说一句「早先那些清单全部作废」。
// 一份变空了的目录还要再多说一句：模型不许再用早先清单里的名字。
func renderCatalogUpdate(entries []CatalogEntry) (llm.Message, error) {
	availability := []string{
		"Use only names in this replacement catalog. If the user names a listed skill, or the task clearly matches its description, call the `skill` tool with the exact name before acting.",
		"A user may also invoke a skill directly; its <skill_content> block then appears in this conversation. Follow it, and do not call the `skill` tool again for that skill.",
	}
	if len(entries) == 0 {
		availability = []string{
			"No skills are currently available through the `skill` tool. Do not use names from earlier skill catalogs.",
			"A user may still invoke a skill directly; its <skill_content> block then appears in this conversation. Follow it, and do not call the `skill` tool for it.",
		}
	}
	lines := []string{
		"<system-reminder>",
		"The available skill catalog changed. This complete catalog replaces every earlier available-skills list in this session:",
		"",
		"<available_skills>",
	}
	lines = append(lines, renderCatalogEntries(entries)...)
	lines = append(lines, "</available_skills>", "")
	lines = append(lines, availability...)
	lines = append(lines, "</system-reminder>")
	return catalogMessageOf(lines, CatalogSource{Update: true, Entries: entries})
}

// catalogMessageOf 把排好的那几行和那份出处折成一条用户消息。
func catalogMessageOf(lines []string, source CatalogSource) (llm.Message, error) {
	pluginSource, err := newCatalogSource(source)
	if err != nil {
		return llm.Message{}, err
	}
	content := llm.Content{llm.TextBlock{Text: strings.Join(lines, "\n")}}
	return llm.NewUserMessage(content, pluginSource), nil
}
