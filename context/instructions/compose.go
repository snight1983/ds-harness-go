// 本文件的作用：算出「这一步该给模型看的那条工作区上下文长什么样」，
// 并且把收件箱里排着的那几条对齐到这个结论上——多的删掉、旧的换掉、没有的补上。
//
// 源: packages/context/agent-instructions/src/index.ts:43-78、105-260、322-348

package instructions

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"

	"github.com/snight1983/ds-harness-go/core/agent"
	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
)

// ---- 认自己的消息 ----

// workspaceContexts 从一批消息里挑出本层产出的那些。
//
// 源: packages/context/agent-instructions/src/index.ts:60-62
func workspaceContexts(messages []llm.Message) []llm.Message {
	kept := make([]llm.Message, 0, len(messages))
	for _, message := range messages {
		if _, mine := ParseSource(message.Source); mine {
			kept = append(kept, message)
		}
	}
	return kept
}

// changesOf 把一批本层消息携带的迁移铺平。
//
// 源: packages/context/agent-instructions/src/state.ts:282-288（scopeMessages 那一段）
func changesOf(messages []llm.Message) []Change {
	var flattened []Change
	for _, message := range messages {
		if source, mine := ParseSource(message.Source); mine {
			flattened = append(flattened, source.Changes...)
		}
	}
	return flattened
}

// samePayload 判断两条消息带的是不是同一份东西：正文一样，来路也一样。
//
// 源: packages/context/agent-instructions/src/index.ts:64-67
//
// 新增: DSH 用 `isDeepStrictEqual`。Go 这边 [llm.Content] 的元素和
// [llm.MessageSource] 都是接口，`==` 对它们只在动态类型可比较时有意义，
// 而本层的来源里带着一段切片，直接比会 panic。排成字节再比是稳的，
// 先例是 session/surface.go 的 sameMessageSource。
//
// 身份（[llm.Message.ID]）**不参与**比较：一条重算出来的上下文和收件箱里排着的
// 那一条是两个不同的身份，而这里问的正是「它们说的是不是同一件事」。
func samePayload(left llm.Message, right llm.Message) bool {
	leftContent, err := json.Marshal(left.Content)
	if err != nil {
		return false
	}
	rightContent, err := json.Marshal(right.Content)
	if err != nil {
		return false
	}
	if !bytes.Equal(leftContent, rightContent) {
		return false
	}
	leftSource, err := json.Marshal(left.Source)
	if err != nil {
		return false
	}
	rightSource, err := json.Marshal(right.Source)
	if err != nil {
		return false
	}
	return bytes.Equal(leftSource, rightSource)
}

// ---- 从日志里读回状态 ----

// userMessageOf 把一条 user/message 事件解回它那条消息，解不出来就报不是。
func userMessageOf(event session.Event) (llm.Message, bool) {
	if event.Type != session.EventUserMessage {
		return llm.Message{}, false
	}
	data, err := session.DecodeData(event)
	if err != nil {
		return llm.Message{}, false
	}
	payload, ok := data.(session.UserMessageData)
	if !ok {
		// [session.DecodeData] 按事件类型分发，一条 user/message 只会得到
		// [session.UserMessageData]，所以这一支构造不出来。留着而不是断言掉，
		// 是因为一次分发错位在这里会静默地少认一条已发出的指令，
		// 然后把它当成新的重发一遍。
		return llm.Message{}, false
	}
	return payload.Message, true
}

// surfaceMessages 按模型可见的顺序交出这段会话表面上的那些用户消息。
//
// 新增: DSH 直接拿 `agent.session.events[seq]` 按下标取，因为它那边 seq 就是下标。
// Go 这边一段续跑的会话有 [coresession.Session.FirstLiveSeq]，下标和 seq 会错开，
// 所以先折一张按 seq 取的表。
func surfaceMessages(live *coresession.Session) []llm.Message {
	events := live.Events()
	bySeq := make(map[int]session.Event, len(events))
	for _, event := range events {
		bySeq[event.Seq] = event
	}
	nodes := live.SurfaceNodes()
	messages := make([]llm.Message, 0, len(nodes))
	for _, seq := range nodes {
		event, present := bySeq[seq]
		if !present {
			continue
		}
		if message, ok := userMessageOf(event); ok {
			messages = append(messages, message)
		}
	}
	return messages
}

// visibleChanges 算出「模型此刻看得见哪些指令迁移」，按可见顺序排。
//
// 源: packages/context/agent-instructions/src/state.ts:136-156
//
// 新增: DSH 把这件事做在 `reconcileInstructionContext` 里面。Go 的 [Reconcile]
// 不认识 Agent，所以它被推到了调用方，[ReconcileRequest.Effective] 就是这个结果
// （见那个字段的说明）。同一条作用域出现多次时由 [Reconcile] 自己按「后面覆盖前面、
// 位置按第一次出现算」折起来，和 JS 的 Map.set 一致。
func visibleChanges(live *coresession.Session, authority []llm.Message) []Change {
	nodes := live.SurfaceNodes()
	visible := make(map[int]struct{}, len(nodes))
	for _, seq := range nodes {
		visible[seq] = struct{}{}
	}
	var effective []Change
	// 按**日志顺序**走而不是按表面顺序走，和 DSH 逐字一致：一条被替换到别处的
	// 消息，它携带的迁移仍然是那一刻发生的那一次。
	for _, event := range live.Events() {
		if _, shown := visible[event.Seq]; !shown {
			continue
		}
		message, ok := userMessageOf(event)
		if !ok {
			continue
		}
		if source, mine := ParseSource(message.Source); mine {
			effective = append(effective, source.Changes...)
		}
	}
	effective = append(effective, changesOf(authority)...)
	return effective
}

// visibleBaselineSource 找出模型此刻看得见的那份基线来源，先看待提交的再看日志。
//
// 源: packages/context/agent-instructions/src/index.ts:43-58
//
// 两处都从后往前找：一段会话里可能有过好几份基线，算数的是最后那一份。
func visibleBaselineSource(live *coresession.Session, authority []llm.Message) (Source, bool) {
	for _, message := range slices.Backward(authority) {
		if source, mine := ParseSource(message.Source); mine && source.Baseline {
			return source, true
		}
	}
	for _, message := range slices.Backward(surfaceMessages(live)) {
		if source, mine := ParseSource(message.Source); mine && source.Baseline {
			return source, true
		}
	}
	return Source{}, false
}

// ---- 算出这一步该说什么 ----

// compose 算出这一步该给模型的那条工作区上下文；第二个返回值是 false 表示没话要说。
//
// 源: packages/context/agent-instructions/src/index.ts:105-222
func (i *installer) compose(
	ctx context.Context,
	live agent.Agent,
	claimed []llm.Message,
	pending []llm.Message,
	touchedPaths []string,
) (llm.Message, bool, error) {
	if err := ctx.Err(); err != nil {
		return llm.Message{}, false, err
	}
	// 新增: DSH 这里还判一次 !Number.isFinite，Go 的 int 没有无穷。
	if i.config.MaxBytes <= 0 {
		return llm.Message{}, false, nil
	}
	// 新增: DSH 在这里取 `ctx.get('fs')`，取不到就返回 undefined。本包的
	// [Deps.FS] 是装配时的必填项，所以那一支没有对应物。
	//
	// 没有触碰、而收件箱里已经排着一条时，直接复用它：这一次没有任何新事实，
	// 重算一遍只会把同一段文字换一个身份，白白让模型那边的缓存失效。
	if len(touchedPaths) == 0 && len(pending) > 0 {
		return pending[0], true, nil
	}

	state := i.stateFor(live.Session())
	state.composing.Lock()
	defer state.composing.Unlock()

	// 一个不属于任何工作区的会话没有根可走，这一层这一步就什么都不说。
	rawRoot, rooted, err := i.workspaceRoot(ctx, live.Session().Header().WorkspaceID)
	if err != nil {
		return llm.Message{}, false, err
	}
	if !rooted {
		return llm.Message{}, false, nil
	}
	workspaceRoot := absPath(rawRoot)
	projectRoot, err := FindProjectRoot(ctx, i.fsys, workspaceRoot, i.config.ProjectRootMarkers)
	if err != nil {
		return llm.Message{}, false, err
	}
	identity := WorkspaceBaselineIdentity(i.config, workspaceRoot, projectRoot)

	authority := slices.Clone(claimed)
	visibleBaseline, baselinePresent := visibleBaselineSource(live.Session(), authority)
	keepVisibleBaseline := baselinePresent && visibleBaseline.BaselineIdentity == identity

	var excluded map[string]struct{}
	excludedKnown := false
	if keepVisibleBaseline && state.prep != nil && state.prep.identity == identity {
		excluded = state.prep.excluded
		excludedKnown = true
	}

	var content llm.Content
	var changes []Change
	desiredBaseline := false
	var nextPrep *preparation

	if !baselinePresent || !keepVisibleBaseline || !excludedKnown {
		replacePrevious := baselinePresent && !keepVisibleBaseline
		loaded, present, err := LoadBaselineSet(ctx, i.fsys, i.config, workspaceRoot, projectRoot, replacePrevious)
		if err != nil {
			return llm.Message{}, false, err
		}

		baselineChanges, baselineVersions := BaselineState(loaded.Included)
		observedChanges, _ := BaselineState(loaded.Observed)
		// 被观察到、却没能留进基线的那些作用域，就是被去重和预算裁掉的那些。
		excludedScopes := make(map[string]struct{}, len(observedChanges))
		for _, change := range observedChanges {
			excludedScopes[change.Scope] = struct{}{}
		}
		for _, change := range baselineChanges {
			delete(excludedScopes, change.Scope)
		}
		excluded = excludedScopes
		nextPrep = &preparation{identity: identity, excluded: excludedScopes}
		for scope, version := range baselineVersions {
			state.versions[scope] = version
		}

		if !keepVisibleBaseline && present && loaded.Rendered.Text != "" {
			baselineContent := BaselineMessage(loaded.Rendered.Text).Content
			content = append(content, baselineContent...)

			replacementScopes := make(map[string]struct{}, len(baselineChanges))
			for _, change := range baselineChanges {
				replacementScopes[change.Scope] = struct{}{}
			}
			// 换掉一份口径不同的旧基线时，旧基线上那些新基线不再覆盖的作用域
			// 要明确告诉模型「没了」——不说的话它会一直以为那些指令还算数。
			var allChanges []Change
			if replacePrevious {
				for _, change := range visibleBaseline.Changes {
					if change.Action == ActionRemove {
						continue
					}
					if _, replaced := replacementScopes[change.Scope]; replaced {
						continue
					}
					allChanges = append(allChanges, Change{
						Action: ActionRemove, Scope: change.Scope, Path: change.Path,
					})
				}
			}
			allChanges = append(allChanges, baselineChanges...)
			changes = append(changes, allChanges...)

			// 这条合成出来的消息不进收件箱，只进 authority：它让这一轮里后面那次
			// 对账知道「基线已经在这条消息里发出去了」，从而只算它之后的差额。
			baselineSource, err := Source{
				Baseline:         true,
				BaselineIdentity: identity,
				Changes:          allChanges,
			}.MessageSource()
			if err != nil {
				return llm.Message{}, false, err
			}
			authority = append(authority, llm.NewUserMessage(baselineContent, baselineSource))
			desiredBaseline = true
		}
	}

	request := ReconcileRequest{
		Effective:             visibleChanges(live.Session(), authority),
		Versions:              state.versions,
		WorkspaceRoot:         workspaceRoot,
		ProjectRoot:           projectRoot,
		ScopeHints:            changesOf(pending),
		TouchedPaths:          touchedPaths,
		IncludeBaselineScopes: keepVisibleBaseline,
	}
	if keepVisibleBaseline {
		request.ExcludedBaselineScopes = excluded
	}
	reconciled, changed, err := Reconcile(ctx, i.fsys, i.config, request)
	if err != nil {
		return llm.Message{}, false, err
	}
	if changed {
		// [ContextMessage] 排出来的正文就是这一块，这里只要正文不要它那份来源——
		// 来源在下面和基线那部分合成一条。
		content = append(content, llm.TextBlock{Text: reconciled.Text})
		changes = append(changes, reconciled.Changes...)
		ApplyVersionUpdates(state.versions, reconciled.VersionUpdates)
	}

	if nextPrep != nil {
		state.prep = nextPrep
	}
	if len(content) == 0 {
		return llm.Message{}, false, nil
	}
	baselineIdentity := ""
	if desiredBaseline {
		baselineIdentity = identity
	}
	source, err := Source{
		Baseline:         desiredBaseline,
		BaselineIdentity: baselineIdentity,
		Changes:          changes,
	}.MessageSource()
	if err != nil {
		return llm.Message{}, false, err
	}
	return llm.NewUserMessage(content, source), true, nil
}

// ---- 把收件箱对齐到这个结论上 ----

// syncInbox 让收件箱里本层那几条恰好等于 desired：多的删掉、旧的换掉、没有的补上。
//
// 源: packages/context/agent-instructions/src/index.ts:224-248
//
// 新增: DSH 直接调 `agent.inbox.remove/replace/prepend`。Go 这边 [agent.Inbox]
// 不加锁、只当只读投影用（见 core/agent 的包文档），而本层的观察者跑在各自的
// 协程上，所以三条改动都走 [agent.Agent] 上那把锁下的同名方法。
func (i *installer) syncInbox(live agent.Agent, claimed []llm.Message, desired llm.Message, wanted bool) {
	pending := workspaceContexts(live.Inbox().NextStep())

	// 已经发出去过的就别再发一遍：一条内容和来路都一样的上下文重复进对话，
	// 模型会把它读成「这件事又发生了一次」。
	supplied := false
	if wanted {
		for _, message := range claimed {
			if samePayload(message, desired) {
				supplied = true
				break
			}
		}
		if !supplied {
			for _, message := range surfaceMessages(live.Session()) {
				if samePayload(message, desired) {
					supplied = true
					break
				}
			}
		}
	}
	if !wanted || supplied {
		for _, message := range pending {
			live.Remove(message.ID)
		}
		return
	}

	for _, message := range pending {
		if !samePayload(message, desired) {
			continue
		}
		// 排着的那条说的就是这件事，留住它自己那个身份：换一个身份等于让
		// 已经认过它的地方再认一次。
		for _, other := range pending {
			if other.ID != message.ID {
				live.Remove(other.ID)
			}
		}
		return
	}

	if len(pending) == 0 {
		live.Prepend(desired, agent.NextStep)
		return
	}
	// 原地换而不是「删了再插」：位置决定模型读到它的先后。
	live.Replace(pending[0].ID, desired)
	for _, message := range pending[1:] {
		live.Remove(message.ID)
	}
}

// composeAndSync 算一次再对齐一次，是异步投影那条路的整个身子。
//
// 源: packages/context/agent-instructions/src/index.ts:250-260
func (i *installer) composeAndSync(
	ctx context.Context,
	live agent.Agent,
	claimed []llm.Message,
	touchedPaths []string,
) error {
	pending := workspaceContexts(live.Inbox().NextStep())
	desired, wanted, err := i.compose(ctx, live, claimed, pending, touchedPaths)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	i.syncInbox(live, claimed, desired, wanted)
	return nil
}

// onPreStep 在每一个准备开跑的步骤上把工作区上下文折进去。
//
// 源: packages/context/agent-instructions/src/index.ts:322-348
func (i *installer) onPreStep(
	ctx context.Context,
	step agent.PreStep,
	next func(context.Context) (agent.PreStepDecision, error),
) (agent.PreStepDecision, error) {
	decision, err := next(ctx)
	if err != nil {
		return decision, err
	}
	live := step.Agent
	// 运行时里出不来这种提议，但这条规则挂在一个公开的瀑布上，别人喂什么进来
	// 不归本包管——不能因此空指针。
	if live == nil || live.Inbox() == nil {
		return decision, nil
	}

	if err := i.wait(ctx, i.stateFor(live.Session())); err != nil {
		return agent.RejectStep(), err
	}
	pending := workspaceContexts(live.Inbox().NextStep())
	desired, wanted, err := i.compose(ctx, live, step.Messages, pending, nil)
	if err != nil {
		return agent.RejectStep(), err
	}
	if err := ctx.Err(); err != nil {
		return agent.RejectStep(), err
	}

	// 一个空的第一步占的是一个不发请求的回合，把上下文塞进去会让它变成一次
	// 独立的模型调用。后面那些步骤是工具续跑，塞得进去。
	if !decision.Enter || (step.Step == 1 && len(decision.Messages) == 0) {
		i.syncInbox(live, step.Messages, desired, wanted)
		return decision, nil
	}

	// 这一步真要跑了，排着的那批就此了结：要么它作为 desired 跟着进去，
	// 要么它说的话已经被这一批盖住了。两种情况下都不该继续排着。
	for _, message := range pending {
		live.Remove(message.ID)
	}
	if !wanted {
		return decision, nil
	}
	for _, message := range decision.Messages {
		if samePayload(message, desired) {
			return decision, nil
		}
	}

	// 折在认领走的那批之后：直接的提示排在它前面，驱动补的运行期上下文排在它后面。
	lastClaimed := -1
	for index, message := range decision.Messages {
		if slices.ContainsFunc(step.Messages, func(claimed llm.Message) bool {
			return claimed.ID == message.ID
		}) {
			lastClaimed = index
		}
	}
	entered := slices.Insert(slices.Clone(decision.Messages), lastClaimed+1, desired)
	return agent.EnterStep(entered), nil
}
