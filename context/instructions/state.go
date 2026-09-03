// 本文件的作用：落进会话日志里的那份指令状态是什么形状，以及拿它和介质上
// 此刻的样子对一遍账、只把差额渲染出来。
//
// 源: packages/context/agent-instructions/src/state.ts:1-433

package instructions

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/snight1983/ds-harness-go/fs"
	"github.com/snight1983/ds-harness-go/llm"
)

// Name 是这一层的产出方名字，落在消息来源里。
//
// 源: packages/context/agent-instructions/src/state.ts:34
const Name = "agent-instructions"

// sourceForm 是这份来源自称的信息形态。
//
// 源: packages/context/agent-instructions/src/state.ts:39-40
//
// 每一份工作区上下文携带的都是从文件里读出来的指令，所以它是个常数。
const sourceForm = "instructions"

// Source 是一份工作区上下文的持久事实：谁产出的、是不是基线、带了哪些迁移。
//
// 源: packages/context/agent-instructions/src/state.ts:36-46
//
// 新增: DSH 用 TypeScript 的声明合并把 `'agent-instructions'` 挂进
// `MessageSourceMap`。Go 的 [llm.MessageSource] 是封闭接口（理由见 llm 的包文档），
// 插件挂不进去。这里给出的是一个普通结构体加它的 JSON 编解码，
// 再靠 [llm.UnknownSource] 这个封闭联合**留出来的那个口子**原样携带它——
// 那个变体本来就是为「本构建的联合里没有的来源」准备的，而从 llm 包的角度看，
// 一个下游包定义的来源正是这种东西。
type Source struct {
	// Baseline 标记这是完整的启动/续会话基线，而不是后来的一次增量。
	Baseline bool
	// BaselineIdentity 是产出这份基线时的口径身份，见 [WorkspaceBaselineIdentity]。
	// 只有基线带它。
	BaselineIdentity string
	// Changes 是这一条消息携带的状态迁移。
	Changes []Change
}

// sourceJSON 是 [Source] 落到线上的形状，字段名和 DSH 逐字相同。
type sourceJSON struct {
	Kind             string   `json:"kind"`
	Form             string   `json:"form"`
	Baseline         bool     `json:"baseline,omitempty"`
	BaselineIdentity string   `json:"baselineIdentity,omitempty"`
	Changes          []Change `json:"changes"`
}

// MarshalJSON 让 [Source] 编成 DSH 那份形状。
func (s Source) MarshalJSON() ([]byte, error) {
	changes := s.Changes
	if changes == nil {
		// changes 在 DSH 那边是必填数组。编成 null 的话，读回来时
		// 「没有迁移」和「这个字段坏了」就长得一样了。
		changes = []Change{}
	}
	return json.Marshal(sourceJSON{
		Kind:             Name,
		Form:             sourceForm,
		Baseline:         s.Baseline,
		BaselineIdentity: s.BaselineIdentity,
		Changes:          changes,
	})
}

// UnmarshalJSON 把一份来源读回来，读不懂的迁移逐条丢掉而不是整份拒绝。
//
// 源: packages/context/agent-instructions/src/state.ts:100-127
//
// 宽进的理由：这些字节来自一份**已经写下的**会话日志，可能是别的版本写的。
// 整份拒绝会让一次本来能续上的会话读不出任何已知状态，
// 然后把全部指令当成新的重发一遍。
func (s *Source) UnmarshalJSON(data []byte) error {
	var raw struct {
		Kind     string            `json:"kind"`
		Baseline bool              `json:"baseline"`
		Identity string            `json:"baselineIdentity"`
		Changes  []json.RawMessage `json:"changes"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("工作区指令：来源不是一个 JSON 对象：%w", err)
	}
	if raw.Kind != Name {
		return fmt.Errorf("工作区指令：来源的 kind 是 %q，不是 %q", raw.Kind, Name)
	}
	s.Baseline = raw.Baseline
	s.BaselineIdentity = raw.Identity
	s.Changes = decodeChanges(raw.Changes)
	return nil
}

// decodeChanges 逐条读迁移，形状不对的那条跳过。
//
// 源: packages/context/agent-instructions/src/state.ts:112-127
func decodeChanges(raw []json.RawMessage) []Change {
	changes := make([]Change, 0, len(raw))
	for _, entry := range raw {
		var change Change
		if err := json.Unmarshal(entry, &change); err != nil {
			continue
		}
		if change.Action != ActionSet && change.Action != ActionReplace && change.Action != ActionRemove {
			continue
		}
		changes = append(changes, change)
	}
	return changes
}

// MessageSource 把这份来源包成一个 [llm.MessageSource]。
//
// 源: packages/context/agent-instructions/src/state.ts:81-86
func (s Source) MessageSource() (llm.MessageSource, error) {
	encoded, err := json.Marshal(s)
	if err != nil {
		// [Source] 里只有 string、bool 和结构体切片，编码不可能失败。
		// 留着这条是因为签名一旦不带 error，将来给 Source 加一个会编码失败的
		// 字段（比如 any）就得改所有调用方。
		return nil, fmt.Errorf("工作区指令：编码来源失败：%w", err)
	}
	return llm.UnknownSource{Kind: llm.SourceKind(Name), Raw: encoded}, nil
}

// ParseSource 从一个消息来源里把这份状态读回来；不是这一层产出的就返回 false。
//
// 源: packages/context/agent-instructions/src/state.ts:100-106
func ParseSource(source llm.MessageSource) (Source, bool) {
	unknown, ok := source.(llm.UnknownSource)
	if !ok || unknown.Kind != llm.SourceKind(Name) {
		return Source{}, false
	}
	var parsed Source
	if err := json.Unmarshal(unknown.Raw, &parsed); err != nil {
		return Source{}, false
	}
	return parsed, true
}

// ContextMessage 把一次对账的渲染结果做成一条用户角色消息。
//
// 源: packages/context/agent-instructions/src/state.ts:81-86
func ContextMessage(text string, changes []Change) (llm.Message, error) {
	source, err := Source{Changes: changes}.MessageSource()
	if err != nil {
		// 同 [Source.MessageSource]：这条编码不会失败，错误只是原样传下去。
		return llm.Message{}, err
	}
	return llm.NewUserMessage(llm.Content{llm.TextBlock{Text: text}}, source), nil
}

// BaselineMessage 把一份渲染好的基线做成一条用户角色消息。
//
// 源: packages/context/agent-instructions/src/state.ts:88-98
//
// 这一条用的是 [llm.PluginSource] 而不是 [Source]，和 DSH 一致：
// DSH 的 `workspaceContextMessage` 写的就是 `{kind:'plugin', plugin: name}`。
// 基线不带迁移列表——它**本身**就是完整状态，没有「相对什么的差额」这回事。
func BaselineMessage(text string) llm.Message {
	return llm.NewUserMessage(
		llm.Content{llm.TextBlock{Text: text}},
		llm.PluginSource{Plugin: Name},
	)
}

// VersionState 是一条作用域的元数据缓存；**指令原文有意不留**。
//
// 源: packages/context/agent-instructions/src/state.ts:54-64
//
// 留原文的话，这张表会随会话时长线性长大，而它唯一的用处是回答
// 「还是不是上次那份」——两个摘要就够了。
type VersionState struct {
	Path    string
	Version fs.Version
	Digest  string
	// TrimmedDigest 是去空白之后的身份（见 [TrimmedDigest]），
	// 用来在不重读兄弟文件的前提下压掉同目录重复。
	TrimmedDigest string
}

// VersionUpdate 是一次元数据缓存的迁移，绑在一条已渲染的变更上。
//
// 源: packages/context/agent-instructions/src/state.ts:66-73
type VersionUpdate struct {
	Change Change
	// State 为 nil 表示把这条作用域从缓存里删掉。
	State *VersionState
}

// sameChange 判断两条迁移是不是同一条。
//
// 源: packages/context/agent-instructions/src/state.ts:129-134
func sameChange(a Change, b Change) bool {
	return a.Action == b.Action && a.Scope == b.Scope && a.Path == b.Path && a.Digest == b.Digest
}

// BaselineState 把渲染后留下的基线文件折成「对账要用的状态」。
//
// 源: packages/context/agent-instructions/src/state.ts:158-188
//
// 第一个返回值是切片不是 map：它会被当成 [ReconcileRequest.Effective] 传下去，
// 而对账的输出顺序一路影响渲染顺序和预算怎么裁。JS 的 Map 保插入序，
// Go 的 map 不保，所以顺序在这里必须是显式的。
func BaselineState(files []LoadedFile) ([]Change, map[string]VersionState) {
	changes := make([]Change, 0, len(files))
	versions := make(map[string]VersionState, len(files))
	for _, file := range files {
		digest := ContentDigest(file.Content)
		change := Change{
			Action: ActionSet,
			Scope:  InstructionScopeKey(file.DisplayPath),
			Path:   file.DisplayPath,
			Digest: digest,
		}
		changes = append(changes, change)
		if file.Version != "" {
			versions[change.Scope] = VersionState{
				Path:          file.DisplayPath,
				Version:       file.Version,
				Digest:        digest,
				TrimmedDigest: TrimmedDigest(file.Content),
			}
		}
	}
	return changes, versions
}

// RetainedVersionUpdates 只留下那些「对应的迁移真被渲染出来了」的缓存更新。
//
// 源: packages/context/agent-instructions/src/state.ts:199-210
//
// 一条没渲染出来的迁移要是也把缓存改了，下一次对账会看见一个新鲜的缓存
// 和一个从没告诉过模型的状态，然后什么都不发——那份指令就永远丢了。
func RetainedVersionUpdates(updates []VersionUpdate, renderedChanges []Change) []VersionUpdate {
	retained := make([]VersionUpdate, 0, len(updates))
	for _, update := range updates {
		for _, change := range renderedChanges {
			if sameChange(update.Change, change) {
				retained = append(retained, update)
				break
			}
		}
	}
	return retained
}

// ApplyVersionUpdates 把缓存迁移落到调用方那张版本表上。
//
// 源: packages/context/agent-instructions/src/state.ts:212-230
//
// 新增: DSH 这张表藏在 `WeakMap<Session, Map<...>>` 里，靠会话对象被回收
// 来清理，所以那边还有一步「表空了就把会话这一项删掉」。Go 这边表由调用方持有，
// 那一步没有对应物：谁的生命周期就归谁管。
func ApplyVersionUpdates(versions map[string]VersionState, updates []VersionUpdate) {
	for _, update := range updates {
		if update.State == nil {
			delete(versions, update.Change.Scope)
			continue
		}
		versions[update.Change.Scope] = *update.State
	}
}

// relativeScope 把一个绝对目录换算成相对项目根的作用域名。
//
// 源: packages/context/agent-instructions/src/state.ts:232-235
func relativeScope(projectRoot string, dir string) string {
	scope := RelativeDisplay(projectRoot, dir)
	if scope == "" {
		return "."
	}
	return scope
}

// scopeSet 是一个按插入序遍历的字符串集合。
//
// 新增: DSH 用的是 JS 的 Set，它保插入序。Go 的 map 遍历顺序是随机的，
// 而这个集合的遍历顺序会一路决定渲染顺序、进而决定预算从哪一头开始裁。
// 用 map 的话，同样的输入会渲染出不同的结果。
type scopeSet struct {
	order []string
	seen  map[string]struct{}
}

func newScopeSet() *scopeSet {
	return &scopeSet{seen: map[string]struct{}{}}
}

func (s *scopeSet) add(scope string) {
	if _, already := s.seen[scope]; already {
		return
	}
	s.seen[scope] = struct{}{}
	s.order = append(s.order, scope)
}

func (s *scopeSet) has(scope string) bool {
	_, ok := s.seen[scope]
	return ok
}

func (s *scopeSet) all() []string { return s.order }

// changeIndex 是一张按 scope 索引、按**首次出现**排序的迁移表。
//
// 新增: 对应 DSH 的 `Map<string, AgentInstructionChange>`。JS 的 Map 在
// 覆盖一个已有键时**不会**把它挪到末尾，位置还是第一次插入的位置——
// 这里逐字照做，因为这个顺序会被 [Reconcile] 遍历到。
type changeIndex struct {
	order    []string
	byScope  map[string]Change
	hasScope map[string]struct{}
}

func newChangeIndex(changes []Change) *changeIndex {
	index := &changeIndex{byScope: map[string]Change{}, hasScope: map[string]struct{}{}}
	for _, change := range changes {
		if _, already := index.hasScope[change.Scope]; !already {
			index.hasScope[change.Scope] = struct{}{}
			index.order = append(index.order, change.Scope)
		}
		index.byScope[change.Scope] = change
	}
	return index
}

func (i *changeIndex) get(scope string) (Change, bool) {
	change, ok := i.byScope[scope]
	return change, ok
}

func (i *changeIndex) scopes() []string { return i.order }

// ReconcileRequest 是一次对账要知道的全部事实。
//
// 源: packages/context/agent-instructions/src/state.ts:246-259
//
// 新增: DSH 这里第一个参数是 `Agent`，从它身上取会话、表面层和工作目录，
// 再自己把「模型现在看得见哪些迁移」算出来（`visibleInstructionChanges`）。
// 本包不认识 Agent——那一层在 DESIGN.md 第八节的第 6 块。算可见状态需要的是
// 会话表面层，和指令这件事没有关系，所以它被推给了调用方：
// [ReconcileRequest.Effective] 就是那个结果。
type ReconcileRequest struct {
	// Effective 是模型此刻看得见的那些迁移，**按可见顺序**排。
	// 同一条作用域出现多次时后面的覆盖前面的，位置按第一次出现算。
	Effective []Change

	// Versions 是这个会话的作用域元数据表。[Reconcile] **会就地改它**：
	// 这张表和 DSH 那张一样是「活的」，探测过程里的删除和回滚都直接落在上面。
	// 已渲染那部分的提交另走 [VersionUpdate]，见 [RetainedVersionUpdates]。
	Versions map[string]VersionState

	// WorkspaceRoot 是这个会话那份工作区的根，一条 [fs.FileSystem] 命名空间里的
	// 绝对路径。
	//
	// 新增: DSH 这里是会话的宿主机工作目录。本仓库没有那样东西（见
	// [github.com/snight1983/ds-harness-go/session.SessionHeader.WorkspaceID]），
	// 这条路径由装配方从工作区标识换出来，见 [Deps.WorkspaceRoot]。
	WorkspaceRoot string

	// ProjectRoot 留空表示让 [Reconcile] 自己去找。
	//
	// TODO(frozen-project-root): 一个循环实例应该一直用基线那次算出来的根。
	// 标记文件被改动之后重算，会让已经写下的相对作用域键换一套含义。
	ProjectRoot string

	// ScopeHints 是待提交的那批迁移，用来把它们涉及的作用域也纳入这次对账。
	ScopeHints []Change

	// TouchedPaths 是这一步里被读写过的文件，用来临时纳入它们沿途的后代目录。
	TouchedPaths []string

	// IncludeBaselineScopes 决定基线那批作用域参不参加这次对账。
	IncludeBaselineScopes bool

	// ExcludedBaselineScopes 是明确不要的基线作用域；只查在不在，不遍历。
	ExcludedBaselineScopes map[string]struct{}
}

// Reconciled 是一次对账的产物。
//
// 源: packages/context/agent-instructions/src/state.ts:75-79
type Reconciled struct {
	// Text 是渲染好的、带框的那段文字。
	Text string
	// Changes 是**真被渲染进去**的那些迁移，落进会话日志的就是它们。
	Changes []Change
	// VersionUpdates 是配套的缓存迁移，已经按 Changes 筛过。
	VersionUpdates []VersionUpdate
}

// Reconcile 把模型看得见的状态和介质上此刻的样子对一遍，只把差额渲染出来。
//
// 源: packages/context/agent-instructions/src/state.ts:237-433
//
// 第二个返回值是 false 表示「这一次没有东西要说」：没有差额，或者差额一条都
// 没能装进预算里。后一种情况下**缓存也不提交**，下一次对账会重试，
// 而不是反复发一段只有账没有内容的文字。
func Reconcile(
	ctx context.Context,
	fsys fs.FileSystem,
	config ResolvedConfig,
	request ReconcileRequest,
) (Reconciled, bool, error) {
	effective := newChangeIndex(request.Effective)
	workspaceRoot := absPath(request.WorkspaceRoot)
	projectRoot := request.ProjectRoot
	if projectRoot == "" {
		found, err := FindProjectRoot(ctx, fsys, workspaceRoot, config.ProjectRootMarkers)
		if err != nil {
			return Reconciled{}, false, err
		}
		projectRoot = found
	}
	projectRoot = absPath(projectRoot)

	scopes, baselineScopes := collectScopes(config, request, effective, projectRoot, workspaceRoot)

	state := &reconcileState{
		versions:         request.Versions,
		seenAbsolute:     map[string]struct{}{},
		keptTrimmedByDir: map[string]map[string]struct{}{},
		budget:           newSourceBudget(config),
	}
	for _, group := range groupScopesByDirectory(scopes.all()) {
		if err := state.reconcileDirectory(ctx, fsys, config, request, effective,
			baselineScopes, projectRoot, group); err != nil {
			return Reconciled{}, false, err
		}
	}

	if len(state.items) == 0 {
		return Reconciled{}, false, nil
	}
	text, changes := RenderInstructionChanges(state.items, config.MaxBytes)
	// 预算极小时渲染出来的可能只有一行账，一条迁移都没代表进去。那种时候
	// 什么都不发、什么都不提交：没提交的版本会让下一次对账重试，
	// 而不是一遍遍发只有账的文字。
	if text == "" || len(changes) == 0 {
		return Reconciled{}, false, nil
	}
	return Reconciled{
		Text:           text,
		Changes:        changes,
		VersionUpdates: RetainedVersionUpdates(state.versionUpdates, changes),
	}, true, nil
}

// collectScopes 把这次要探的作用域凑齐，顺带算出基线那一批是哪些。
//
// 源: packages/context/agent-instructions/src/state.ts:269-299
func collectScopes(
	config ResolvedConfig,
	request ReconcileRequest,
	effective *changeIndex,
	projectRoot string,
	workspaceRoot string,
) (scopes *scopeSet, baselineScopes *scopeSet) {
	scopes = newScopeSet()
	baselineScopes = newScopeSet()

	addDirScopes := func(target *scopeSet, directory string) {
		for _, candidate := range config.InstructionFileCandidates {
			target.add(CandidateScopeKey(directory, candidate))
		}
		for _, candidate := range config.LocalInstructionFileCandidates {
			target.add(CandidateScopeKey(directory, candidate))
		}
	}
	addProjectScopes := func(target *scopeSet, dir string) {
		addDirScopes(target, relativeScope(projectRoot, dir))
	}

	baselineScopes.add(CandidateScopeKey(UserGlobalDirectory, UserGlobalFile))
	for _, dir := range AncestorChain(projectRoot, workspaceRoot) {
		addProjectScopes(baselineScopes, dir)
	}
	if request.IncludeBaselineScopes {
		for _, scope := range baselineScopes.all() {
			scopes.add(scope)
		}
	}

	for _, change := range request.ScopeHints {
		if !request.IncludeBaselineScopes && baselineScopes.has(change.Scope) {
			continue
		}
		scopes.add(change.Scope)
	}

	for _, scope := range effective.scopes() {
		if !request.IncludeBaselineScopes && baselineScopes.has(scope) {
			continue
		}
		directory, _ := DecodeScopeKey(scope)
		if directory == UserGlobalDirectory {
			// 用户全局那一层只有一个文件名，所以不按候选列表铺开。
			scopes.add(CandidateScopeKey(UserGlobalDirectory, UserGlobalFile))
			continue
		}
		addDirScopes(scopes, directory)
	}

	for _, touchedPath := range request.TouchedPaths {
		for _, dir := range DescendantDirsBetween(workspaceRoot, touchedPath) {
			addProjectScopes(scopes, dir)
		}
	}
	return scopes, baselineScopes
}

// directoryGroup 是同一个目录下的那几条作用域。
type directoryGroup struct {
	directory string
	scopes    []string
}

// groupScopesByDirectory 按目录把作用域分组，组的顺序按目录首次出现。
//
// 源: packages/context/agent-instructions/src/state.ts:324-330
//
// 分组是有语义的：同一个目录里的候选构成**一个去重权威组**，
// 组里任何一个探不出来，整组都要回滚（见 [reconcileState.reconcileDirectory]）。
func groupScopesByDirectory(scopes []string) []directoryGroup {
	var groups []directoryGroup
	index := map[string]int{}
	for _, scope := range scopes {
		directory, _ := DecodeScopeKey(scope)
		at, ok := index[directory]
		if !ok {
			index[directory] = len(groups)
			groups = append(groups, directoryGroup{directory: directory, scopes: []string{scope}})
			continue
		}
		groups[at].scopes = append(groups[at].scopes, scope)
	}
	return groups
}

// reconcileState 是一次对账过程中累积起来的东西。
type reconcileState struct {
	// versions 是调用方那张活的版本表，就地改。
	versions map[string]VersionState
	// seenAbsolute 挡掉同一个绝对路径被两条作用域各探一次。
	seenAbsolute map[string]struct{}
	// keptTrimmedByDir 是这一趟里每个目录已经留下的去空白身份。
	keptTrimmedByDir map[string]map[string]struct{}
	// budget 是这一趟读进内存的总字节预算，跨目录累计。
	budget *sourceBudget

	items          []ChangeRenderItem
	versionUpdates []VersionUpdate
}

// registerKeptTrimmed 记下一个去空白身份，返回它是不是重复的。
//
// 源: packages/context/agent-instructions/src/state.ts:307-316
func (s *reconcileState) registerKeptTrimmed(directory string, digest string) bool {
	digests, ok := s.keptTrimmedByDir[directory]
	if !ok {
		digests = map[string]struct{}{}
		s.keptTrimmedByDir[directory] = digests
	}
	if _, duplicate := digests[digest]; duplicate {
		return true
	}
	digests[digest] = struct{}{}
	return false
}

// pushRemoval 记一条「这份指令没了」的迁移。
//
// 源: packages/context/agent-instructions/src/state.ts:319-323
//
// 造出来的那个占位文件带一个 `removed:` 开头的绝对路径：渲染器按绝对路径
// 认迁移，而移除这一条**没有真实文件**，又必须和同一趟里别的项目区分开。
func (s *reconcileState) pushRemoval(scope string, filePath string) {
	change := Change{Action: ActionRemove, Scope: scope, Path: filePath}
	s.items = append(s.items, ChangeRenderItem{
		Change: change,
		File:   LoadedFile{AbsolutePath: "removed:" + scope, DisplayPath: filePath},
	})
	s.versionUpdates = append(s.versionUpdates, VersionUpdate{Change: change})
}

// reconcileDirectory 对一个目录下那几条作用域走一遍。
//
// 源: packages/context/agent-instructions/src/state.ts:331-422
func (s *reconcileState) reconcileDirectory(
	ctx context.Context,
	fsys fs.FileSystem,
	config ResolvedConfig,
	request ReconcileRequest,
	effective *changeIndex,
	baselineScopes *scopeSet,
	projectRoot string,
	group directoryGroup,
) error {
	probedScopes := make([]string, 0, len(group.scopes))
	for _, scope := range group.scopes {
		_, excluded := request.ExcludedBaselineScopes[scope]
		if excluded && baselineScopes.has(scope) {
			previous, had := effective.get(scope)
			if !had || previous.Action == ActionRemove {
				delete(s.versions, scope)
			} else {
				s.pushRemoval(scope, previous.Path)
			}
			continue
		}
		probedScopes = append(probedScopes, scope)
	}

	// 这四个是回滚点：这一组里任何一条探不出来，整组退回进组之前的样子。
	itemStart := len(s.items)
	versionUpdateStart := len(s.versionUpdates)
	var addedAbsolutePaths []string
	priorVersions := make(map[string]*VersionState, len(probedScopes))
	for _, scope := range probedScopes {
		if state, ok := s.versions[scope]; ok {
			saved := state
			priorVersions[scope] = &saved
		} else {
			priorVersions[scope] = nil
		}
	}

	for _, scope := range probedScopes {
		previous, had := effective.get(scope)
		probe, err := ProbeScopeInstruction(ctx, fsys, config, scope, projectRoot)
		if err != nil {
			return err
		}

		if probe.Kind == ProbeUnavailable {
			if !had || previous.Action == ActionRemove {
				continue
			}
			// 同一个目录里的候选是一个去重权威组。组里还活着的成员观察不到时，
			// 整组保持上一份好的状态：缓存热不热，绝不能决定一条兄弟迁移发不发。
			s.items = s.items[:itemStart]
			s.versionUpdates = s.versionUpdates[:versionUpdateStart]
			for candidateScope, prior := range priorVersions {
				if prior == nil {
					delete(s.versions, candidateScope)
					continue
				}
				s.versions[candidateScope] = *prior
			}
			for _, absolutePath := range addedAbsolutePaths {
				delete(s.seenAbsolute, absolutePath)
			}
			delete(s.keptTrimmedByDir, group.directory)
			return nil
		}

		if probe.Kind == ProbeAbsent {
			if !had || previous.Action == ActionRemove {
				delete(s.versions, scope)
			} else {
				s.pushRemoval(scope, previous.Path)
			}
			continue
		}

		probedFile := probe.File
		if _, already := s.seenAbsolute[probedFile.AbsolutePath]; already {
			continue
		}
		s.seenAbsolute[probedFile.AbsolutePath] = struct{}{}
		addedAbsolutePaths = append(addedAbsolutePaths, probedFile.AbsolutePath)

		cached, cachedOK := s.versions[scope]
		if cachedOK &&
			cached.Path == probedFile.DisplayPath &&
			cached.Version == probedFile.Version &&
			had &&
			previous.Action != ActionRemove &&
			previous.Path == cached.Path &&
			previous.Digest == cached.Digest {
			// 没变，而且上次已经渲染过：留着。但要是这个目录里更靠前的兄弟
			// 此刻的去空白内容和它一样，那这一份就成了要移除的重复。
			if s.registerKeptTrimmed(group.directory, cached.TrimmedDigest) {
				s.pushRemoval(scope, previous.Path)
			}
			continue
		}

		file, ok, err := ReadScopeInstruction(ctx, fsys, probedFile, config.MaxSourceBytes)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		// 总量记在这里而不是缓存命中那条路上：命中的那份没有重新读进内存，
		// 它不占这一趟的预算。
		if err := s.budget.take(file.DisplayPath, byteLength(file.Content)); err != nil {
			return err
		}
		currentDigest := ContentDigest(file.Content)
		trimmedDigest := TrimmedDigest(file.Content)
		if s.registerKeptTrimmed(group.directory, trimmedDigest) {
			// 一份内容不同的文件，去空白之后和这个目录里更靠前的那份一样：
			// 丢掉它，并且把之前渲染过的那份撤回来。
			if had && previous.Action != ActionRemove {
				s.pushRemoval(scope, previous.Path)
			} else {
				delete(s.versions, scope)
			}
			continue
		}

		nextVersion := VersionState{
			Path:          file.DisplayPath,
			Version:       probedFile.Version,
			Digest:        currentDigest,
			TrimmedDigest: trimmedDigest,
		}
		if had && previous.Action != ActionRemove &&
			previous.Path == file.DisplayPath && previous.Digest == currentDigest {
			// 版本令牌变了但内容没变（比如被原样重写了一遍）：
			// 只把缓存刷新到新令牌，不去打扰模型。
			s.versions[scope] = nextVersion
			continue
		}

		action := ActionReplace
		if !had || previous.Action == ActionRemove {
			action = ActionSet
		}
		change := Change{
			Action: action,
			Scope:  scope,
			Path:   file.DisplayPath,
			Digest: currentDigest,
		}
		s.items = append(s.items, ChangeRenderItem{Change: change, File: file})
		s.versionUpdates = append(s.versionUpdates, VersionUpdate{Change: change, State: &nextVersion})
	}
	return nil
}
