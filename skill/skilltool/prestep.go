// 本文件的作用：挂在步骤准入上的两条胳膊——把用户明确调起的技能正文注进这一步，
// 以及把当前那份技能目录发布到模型可见的表面上。
//
// 源: packages/skill/tool-skill/src/index.ts:162-251

package skilltool

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/skill"
)

// gesturePattern 是那个 `/名字` 手势。
//
// 源: packages/skill/tool-skill/src/index.ts:409
//
// 一个两头被空白围住的 `/名字`（就是公开的技能名文法），落在句子里哪个位置都算——
// 和转写稿上那个名字装饰认的是同一个形状。第二个 `/`、或者任何非边界字符都会
// 让它不成立，于是路径（`/usr/bin`）和分数（`5/8`）进不来。
//
// 新增: Go 的 regexp 是 RE2，没有前瞻。DSH 那个 `(?=\s|$)` 后瞻在这里改写成
// 把右边界也吃进来的分组，再由 [invokedSkillNames] 负责在每次匹配之后**退回**
// 那个边界字符——不退的话 "/a /b" 里 `/a` 吃掉的那个空格正是 `/b` 要用的左边界，
// 第二个手势就认不出来了。
var gesturePattern = regexp.MustCompile(`(^|\s)/([a-z0-9]+(?:-[a-z0-9]+)*)(\s|$)`)

// invokedSkillNames 从这一步认领的那些消息里挑出 `/名字` 手势，按第一次出现的
// 顺序去重。
//
// 源: packages/skill/tool-skill/src/index.ts:410-430
//
// 只扫来源是「用户自己说的」的那些消息的文本块：别的来源伪造不出这个手势。
// 挑出来的名字**没有**对着注册表验过。
func invokedSkillNames(messages []llm.Message) []string {
	var names []string
	for _, message := range messages {
		if message.Source == nil || message.Source.SourceKind() != llm.SourceUser {
			continue
		}
		for _, block := range message.Content {
			text, isText := block.(llm.TextBlock)
			if !isText {
				continue
			}
			for _, name := range gestureNames(text.Text) {
				if !slices.Contains(names, name) {
					names = append(names, name)
				}
			}
		}
	}
	return names
}

// gestureNames 扫一段文本里的那些手势。
func gestureNames(text string) []string {
	var names []string
	for offset := 0; offset < len(text); {
		match := gesturePattern.FindStringSubmatchIndex(text[offset:])
		if match == nil {
			break
		}
		names = append(names, text[offset+match[4]:offset+match[5]])
		// 从右边界那个字符**之前**接着扫，理由见 [gesturePattern] 的注释。
		offset += match[6]
	}
	return names
}

// invocationPreStep 是「用户明确调起技能」那条胳膊。
//
// 源: packages/skill/tool-skill/src/index.ts:162-204
//
// 一条被认领的用户消息，第一行里出现 `/名字` 并且那个名字指向一份允许被人调起的
// 技能，就是一次确定无疑的读取手势。渲染出来的正文作为注入指令进这一步，**排在
// 所有其它注入之后**——背景在前（工作区规则、运行期策略、目录），模型必须照着做的
// 材料在最后、离它的回答最近。
//
// 登记顺序把这个位置钉死：这条胳膊登记在目录那条**之前**，而先登记的在瀑布外层，
// 于是它拿到的是里层已经把目录挂上去的那份消息表，它只管往后接。
//
// 这也是 `disable-model-invocation` 那类技能**唯一**的入口：目录和 skill 工具
// 从头到尾都看不见它们。
func (c *Controller) invocationPreStep(
	ctx context.Context,
	step agent.PreStep,
	next func(context.Context) (agent.PreStepDecision, error),
) (agent.PreStepDecision, error) {
	decision, err := next(ctx)
	if err != nil || !decision.Enter {
		return decision, err
	}
	names := invokedSkillNames(step.Messages)
	if len(names) == 0 {
		return decision, nil
	}
	if err := ctx.Err(); err != nil {
		return agent.PreStepDecision{}, err
	}
	options := c.viewOptions(step.Agent)
	var injections []llm.Message
	for _, name := range names {
		definition, err := c.skills.Get(ctx, name, options)
		if err != nil {
			return agent.PreStepDecision{}, err
		}
		if err := ctx.Err(); err != nil {
			return agent.PreStepDecision{}, err
		}
		// 认不出的名字、以及被人这一侧关掉的技能，都留在原地当普通散文：那个手势
		// 从来就不是这道边界认得的主张。这一查落在**读出来的那份定义**上，也就是
		// 真正产出注入内容的那一次查找——落在摘要上的话，两次查找之间的一次变更
		// 会让一份不许被人调的技能被注进去。
		if definition == nil || !skill.IsUserInvocable(definition.Summary) {
			continue
		}
		source, err := newInvocationSource(name)
		if err != nil {
			return agent.PreStepDecision{}, err
		}
		injections = append(injections, llm.NewUserMessage(
			llm.Content{llm.TextBlock{Text: skill.RenderContent(*definition)}}, source))
	}
	if len(injections) == 0 {
		return decision, nil
	}
	return agent.EnterStep(append(slices.Clone(decision.Messages), injections...)), nil
}

// newInvocationSource 把一次「用户明确调起」排成一条消息来源。
//
// 新增: [skill.InvocationSource] 在 Go 里只是一份约定的形状，理由见它的注释。
// 这里把它排进 [llm.PluginSource.Extra]，产出方名字取那个 kind，介质上的字节
// 和 DSH 的 `{kind:'skill-invocation', name, form:'instructions'}` 一致。
func newInvocationSource(name string) (llm.PluginSource, error) {
	extra, err := json.Marshal(skill.InvocationSource{Name: name})
	if err != nil {
		return llm.PluginSource{}, err
	}
	return llm.PluginSource{
		Plugin:  InvocationPlugin,
		Context: llm.InstructionsContext{},
		Extra:   extra,
	}, nil
}

// InvocationPlugin 是「用户明确调起技能」那条注入盖的产出方名字。
//
// 源: packages/skill/skill/src/index.ts:149（`kind: 'skill-invocation'`）
const InvocationPlugin = "skill-invocation"

// catalogPreStep 是「发布技能目录」那条胳膊。
//
// 源: packages/skill/tool-skill/src/index.ts:206-251
//
// 目录**跟着工具的可见性走**：只有当这个 agent 解算到的 `skill` 工具正是本包
// 登记的那一个时才发。比的是定义本身，不是拿名字再查一次——一个作用域里同名的
// 遮蔽工具不该顺带继承这份目录；而 register 是登记进**调用方**那个作用域的，
// 所以一个装在某个 agent 预设里的实例只为那一个 agent 登记，一次不带作用域的
// 查找理应什么都查不到。
func (c *Controller) catalogPreStep(
	ctx context.Context,
	step agent.PreStep,
	next func(context.Context) (agent.PreStepDecision, error),
) (agent.PreStepDecision, error) {
	decision, err := next(ctx)
	if err != nil || !decision.Enter {
		return decision, err
	}
	if err := ctx.Err(); err != nil {
		return agent.PreStepDecision{}, err
	}
	snapshot := skill.CatalogSnapshot{Complete: true}
	if c.toolVisible(step.Agent) {
		snapshot, err = c.skills.Snapshot(ctx, c.viewOptions(step.Agent))
		if err != nil {
			return agent.PreStepDecision{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return agent.PreStepDecision{}, err
	}
	// 一次没跑完的发现留着上一份好的，下一个请求边界再试：把一份残缺的目录
	// 当成「技能变少了」发出去，会让模型以为那些技能没了。
	if !snapshot.Complete {
		return decision, nil
	}

	var invocable []skill.Summary
	for _, summary := range snapshot.Skills {
		if skill.IsModelInvocable(summary) {
			invocable = append(invocable, summary)
		}
	}
	entries := catalogEntries(invocable, c.catalogDescriptionMaxLength)
	digest := digestCatalogEntries(entries)
	history := catalogHistory(step.Agent)
	existing, existingEntries, hasExisting := catalogMessageOn(decision.Messages)

	// 表面上那份目录已经是这一份了：不重发。这一步里如果有人已经挂了一条，
	// 那条就是多余的，撤掉。
	if history.visible && history.visibleDigest == digest {
		return withoutMessage(decision, existing, hasExisting), nil
	}
	// 这一步里已经挂着的那条正好就是要发的这一份：什么都不做。这条胳膊每个
	// 步骤都跑，同一个提议被重新过一遍时不该把那条消息换成一条新 id 的。
	if hasExisting && digestCatalogEntries(existingEntries) == digest {
		return decision, nil
	}
	// 从来没发布过、而且一条都没有：不发。一次会话如果整个过程中都没有技能，
	// 模型不该看见一份空清单，那只会占位置。
	if !history.published && len(entries) == 0 {
		return withoutMessage(decision, existing, hasExisting), nil
	}

	var catalog llm.Message
	if history.published {
		catalog, err = renderCatalogUpdate(entries)
	} else {
		catalog, err = renderCatalogMessage(entries)
	}
	if err != nil {
		return agent.PreStepDecision{}, err
	}
	if !hasExisting {
		return agent.EnterStep(append(slices.Clone(decision.Messages), catalog)), nil
	}
	replaced := slices.Clone(decision.Messages)
	for index, message := range replaced {
		if message.ID == existing.ID {
			replaced[index] = catalog
		}
	}
	return agent.EnterStep(replaced), nil
}

// withoutMessage 把这一步里那条多余的目录消息撤掉；没有就原样交回。
func withoutMessage(
	decision agent.PreStepDecision,
	target llm.Message,
	present bool,
) agent.PreStepDecision {
	if !present {
		return decision
	}
	kept := make([]llm.Message, 0, len(decision.Messages))
	for _, message := range decision.Messages {
		if message.ID != target.ID {
			kept = append(kept, message)
		}
	}
	return agent.EnterStep(kept)
}

// catalogRecord 是日志里那份目录发布史。
type catalogRecord struct {
	// visible 表示表面上现在还挂着一份读得懂的目录。
	visible bool
	// visibleDigest 是那一份的身份，只在 visible 为真时有意义。
	visibleDigest string
	// published 表示这次会话**曾经**发布过目录，哪怕它已经被盖掉了。
	published bool
}

// catalogHistory 从日志里读出这份发布史。
//
// 源: packages/skill/tool-skill/src/index.ts:361-377
//
// 从后往前扫，遇到的第一条**还在表面上**的目录就是当下那一份。published 记的是
// 另一件事：会话里出现过任何一条目录（哪怕被盖掉了），它决定下一份该用第一版
// 措辞还是替换版措辞——一次替换必须说出「早先那些清单全部作废」，而模型的上文里
// 确实读到过那些清单。
func catalogHistory(target agent.Agent) catalogRecord {
	if target == nil {
		return catalogRecord{}
	}
	sess := target.Session()
	if sess == nil {
		return catalogRecord{}
	}
	visible := map[int]struct{}{}
	for _, seq := range sess.SurfaceNodes() {
		visible[seq] = struct{}{}
	}
	events := sess.Events()
	record := catalogRecord{}
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if event.Type != session.EventUserMessage {
			continue
		}
		var data session.UserMessageData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			continue
		}
		entries, ok := catalogEntriesOf(data.Source)
		if !ok {
			continue
		}
		record.published = true
		if _, onSurface := visible[event.Seq]; onSurface {
			record.visible = true
			record.visibleDigest = digestCatalogEntries(entries)
			return record
		}
	}
	return record
}

// catalogMessageOn 找这一步已经挂着的那条目录消息。
//
// 源: packages/skill/tool-skill/src/index.ts:379-388
func catalogMessageOn(messages []llm.Message) (llm.Message, []CatalogEntry, bool) {
	for _, message := range messages {
		entries, ok := catalogEntriesOf(message.Source)
		if ok {
			return message, entries, true
		}
	}
	return llm.Message{}, nil, false
}

// toolVisible 判这个 agent 解算到的 skill 工具是不是本包登记的那一个。
func (c *Controller) toolVisible(target agent.Agent) bool {
	if target == nil || c.lookup == nil {
		return false
	}
	resolved, ok := c.lookup.Get(ToolName, target.Scope().Key())
	return ok && resolved == c.definition
}

// viewOptions 是以这个 agent 的视角读注册表时用的那份选项。
//
// 源: packages/skill/tool-skill/src/index.ts:133,186,222
//
// agent 自己就是那把作用域钥匙，于是这次查找解算出来的分层注册表，和这个 agent
// 的组合方式看到的**完全一样**。
func (c *Controller) viewOptions(target agent.Agent) skill.ViewOptions {
	if target == nil {
		return skill.ViewOptions{}
	}
	options := skill.ViewOptions{Scope: target.Scope().Key()}
	if sess := target.Session(); sess != nil {
		options.WorkspaceID = sess.Header().WorkspaceID
	}
	return options
}
