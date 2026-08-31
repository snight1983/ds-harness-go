// 本文件的作用：钉住落进会话日志的那份来源怎么编解码，以及对账在每一种
// 「介质上和模型以为的对不上」时各自会说什么、会不会说。

package instructions

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"ds-harness-go/fs"
	"ds-harness-go/llm"
)

func TestSource编成DSH那份形状(t *testing.T) {
	source := Source{
		Baseline:         true,
		BaselineIdentity: "口径",
		Changes: []Change{
			{Action: ActionSet, Scope: "s", Path: "AGENTS.md", Digest: "d"},
		},
	}

	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatalf("编码不该失败：%v", err)
	}
	text := string(encoded)
	requireContains(t, text, `"kind":"agent-instructions"`)
	requireContains(t, text, `"form":"instructions"`)
	requireContains(t, text, `"baseline":true`)
	requireContains(t, text, `"baselineIdentity":"口径"`)
	requireContains(t, text, `"action":"set"`)
}

// 迁移列表在 DSH 那边是必填数组。编成 null 的话，读回来时「没有迁移」
// 和「这个字段坏了」就长得一样了。
func TestSource没有迁移时编成空数组(t *testing.T) {
	encoded, err := json.Marshal(Source{})
	if err != nil {
		t.Fatalf("编码不该失败：%v", err)
	}

	requireContains(t, string(encoded), `"changes":[]`)
	requireNotContains(t, string(encoded), "null")
	// 不是基线时这两项一个字都不该占地方。
	requireNotContains(t, string(encoded), "baseline")
}

func TestSource往返(t *testing.T) {
	original := Source{
		Baseline:         true,
		BaselineIdentity: "口径",
		Changes: []Change{
			{Action: ActionSet, Scope: "s1", Path: "AGENTS.md", Digest: "d1"},
			{Action: ActionRemove, Scope: "s2", Path: "sub/AGENTS.md"},
		},
	}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("编码不该失败：%v", err)
	}

	var back Source
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatalf("解码不该失败：%v", err)
	}
	if back.Baseline != original.Baseline || back.BaselineIdentity != original.BaselineIdentity {
		t.Fatalf("往返之后变了：%+v", back)
	}
	if len(back.Changes) != 2 || !sameChange(back.Changes[0], original.Changes[0]) {
		t.Fatalf("迁移往返之后变了：%+v", back.Changes)
	}
	// 移除那一条本来就没有摘要，往返之后也不该凭空长出来。
	if back.Changes[1].Digest != "" {
		t.Fatalf("移除不该带摘要：%+v", back.Changes[1])
	}
}

func TestSource不是这一层产出的就报错(t *testing.T) {
	var source Source
	err := json.Unmarshal([]byte(`{"kind":"别人","changes":[]}`), &source)

	if err == nil {
		t.Fatal("别人的来源应当被拒绝")
	}
	requireContains(t, err.Error(), "agent-instructions")
}

// 来源整个不是一个 JSON 对象时报错。宽进只对**迁移列表里的单条**成立，
// 外面这一层认不出来就没有任何东西可以接着读了。
func TestSource整个不是对象时报错(t *testing.T) {
	var source Source
	if err := json.Unmarshal([]byte(`"一个字符串"`), &source); err == nil {
		t.Fatal("不是对象就该被拒绝")
	}
}

// 宽进：这些字节来自一份**已经写下的**会话日志，可能是别的版本写的。
// 整份拒绝会让一次本来能续上的会话读不出任何已知状态。
func TestSource读不懂的迁移逐条丢掉(t *testing.T) {
	raw := `{"kind":"agent-instructions","changes":[
		{"action":"set","scope":"s1","path":"a.md","digest":"d"},
		{"action":"未来才有的动作","scope":"s2","path":"b.md"},
		"根本不是一个对象",
		{"action":"remove","scope":"s3","path":"c.md"}
	]}`

	var source Source
	if err := json.Unmarshal([]byte(raw), &source); err != nil {
		t.Fatalf("整份不该被拒绝：%v", err)
	}
	if len(source.Changes) != 2 {
		t.Fatalf("应当只丢掉读不懂的那一条，实际是 %+v", source.Changes)
	}
	if source.Changes[0].Scope != "s1" || source.Changes[1].Scope != "s3" {
		t.Fatalf("留下来的顺序不对：%+v", source.Changes)
	}
}

func TestSource包成消息来源再读回来(t *testing.T) {
	original := Source{Changes: []Change{{Action: ActionSet, Scope: "s", Path: "a.md", Digest: "d"}}}

	messageSource, err := original.MessageSource()
	if err != nil {
		t.Fatalf("包装不该失败：%v", err)
	}
	if _, ok := messageSource.(llm.UnknownSource); !ok {
		t.Fatalf("应当靠 UnknownSource 那个口子携带，实际是 %T", messageSource)
	}

	back, ok := ParseSource(messageSource)
	if !ok {
		t.Fatal("应当读得回来")
	}
	if len(back.Changes) != 1 || !sameChange(back.Changes[0], original.Changes[0]) {
		t.Fatalf("往返之后变了：%+v", back.Changes)
	}
}

func TestParseSource别人的来源一律不认(t *testing.T) {
	cases := map[string]llm.MessageSource{
		"插件来源":       llm.PluginSource{Plugin: Name},
		"别的种类":       llm.UnknownSource{Kind: llm.SourceKind("别人"), Raw: json.RawMessage(`{}`)},
		"种类对但内容不是对象": llm.UnknownSource{Kind: llm.SourceKind(Name), Raw: json.RawMessage(`不是 JSON`)},
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			if _, ok := ParseSource(source); ok {
				t.Fatal("不该认")
			}
		})
	}
}

func TestContextMessage带着迁移(t *testing.T) {
	changes := []Change{{Action: ActionSet, Scope: "s", Path: "a.md", Digest: "d"}}

	message, err := ContextMessage("一段文字", changes)
	if err != nil {
		t.Fatalf("造消息不该失败：%v", err)
	}
	if message.Role != llm.RoleUser {
		t.Fatalf("应当是用户角色，实际是 %v", message.Role)
	}
	if text, ok := message.Content[0].(llm.TextBlock); !ok || text.Text != "一段文字" {
		t.Fatalf("内容不对：%+v", message.Content)
	}
	source, ok := ParseSource(message.Source)
	if !ok || len(source.Changes) != 1 {
		t.Fatalf("来源里应当带着那条迁移：%+v", source)
	}
	if source.Baseline {
		t.Fatal("增量不是基线")
	}
}

// 基线**本身**就是完整状态，没有「相对什么的差额」这回事，所以它不带迁移列表。
func TestBaselineMessage用插件来源(t *testing.T) {
	message := BaselineMessage("一份基线")

	plugin, ok := message.Source.(llm.PluginSource)
	if !ok || plugin.Plugin != Name {
		t.Fatalf("基线应当用插件来源，实际是 %#v", message.Source)
	}
}

func TestBaselineState折出迁移和版本表(t *testing.T) {
	files := []LoadedFile{
		{AbsolutePath: "/repo/AGENTS.md", DisplayPath: "AGENTS.md", Content: " 根规则 ", Version: "v1"},
		{AbsolutePath: "/repo/sub/AGENTS.md", DisplayPath: "sub/AGENTS.md", Content: "子规则"},
	}

	changes, versions := BaselineState(files)

	if len(changes) != 2 {
		t.Fatalf("每个文件一条迁移，实际是 %+v", changes)
	}
	if changes[0].Action != ActionSet || changes[0].Path != "AGENTS.md" {
		t.Fatalf("第一条迁移不对：%+v", changes[0])
	}
	if changes[0].Digest != ContentDigest(" 根规则 ") {
		t.Fatal("摘要应当按原文算，不是去空白之后的")
	}
	if changes[0].Scope != InstructionScopeKey("AGENTS.md") {
		t.Fatalf("作用域键不对：%s", changes[0].Scope)
	}
	// 版本报不出来的文件进不了版本表：没有令牌就没法回答「还是不是上次那份」。
	if len(versions) != 1 {
		t.Fatalf("只有带版本的那份该进表，实际是 %+v", versions)
	}
	state := versions[changes[0].Scope]
	if state.Version != "v1" || state.TrimmedDigest != TrimmedDigest(" 根规则 ") {
		t.Fatalf("版本状态不对：%+v", state)
	}
}

// 一条没渲染出来的迁移要是也把缓存改了，下一次对账会看见一个新鲜的缓存
// 和一个从没告诉过模型的状态，然后什么都不发——那份指令就永远丢了。
func TestRetainedVersionUpdates只留渲染出来的那几条(t *testing.T) {
	rendered := Change{Action: ActionSet, Scope: "s1", Path: "a.md", Digest: "d1"}
	dropped := Change{Action: ActionSet, Scope: "s2", Path: "b.md", Digest: "d2"}
	updates := []VersionUpdate{{Change: rendered}, {Change: dropped}}

	retained := RetainedVersionUpdates(updates, []Change{rendered})

	if len(retained) != 1 || retained[0].Change.Scope != "s1" {
		t.Fatalf("只该留下渲染出来的那一条：%+v", retained)
	}
}

// 摘要不一样就不是同一条迁移：那说明模型看见的内容和这次要提交的不是一份。
func TestRetainedVersionUpdates摘要不同就不算同一条(t *testing.T) {
	update := VersionUpdate{Change: Change{Action: ActionSet, Scope: "s", Path: "a.md", Digest: "新"}}
	rendered := Change{Action: ActionSet, Scope: "s", Path: "a.md", Digest: "旧"}

	if retained := RetainedVersionUpdates([]VersionUpdate{update}, []Change{rendered}); len(retained) != 0 {
		t.Fatalf("不该留下：%+v", retained)
	}
}

func TestApplyVersionUpdates写入和删除(t *testing.T) {
	versions := map[string]VersionState{"s2": {Path: "b.md"}}
	next := VersionState{Path: "a.md", Version: "v1"}

	ApplyVersionUpdates(versions, []VersionUpdate{
		{Change: Change{Scope: "s1"}, State: &next},
		{Change: Change{Scope: "s2"}},
	})

	if versions["s1"].Version != "v1" {
		t.Fatalf("s1 应当被写进去：%+v", versions)
	}
	if _, ok := versions["s2"]; ok {
		t.Fatalf("State 为 nil 表示删掉：%+v", versions)
	}
}

func TestRelativeScope根目录是一个点(t *testing.T) {
	if got := relativeScope("/repo", "/repo"); got != "." {
		t.Fatalf("根目录的作用域名应当是点，实际是 %q", got)
	}
	if got := relativeScope("/repo", "/repo/sub"); got != "sub" {
		t.Fatalf("子目录的作用域名不对：%q", got)
	}
}

// changeIndex 覆盖一个已有键时位置不变——这一条逐字照着 JS 的 Map 的语义，
// 因为这个顺序会一路决定渲染顺序。
func TestChangeIndex覆盖不改变位置(t *testing.T) {
	index := newChangeIndex([]Change{
		{Scope: "a", Path: "1"},
		{Scope: "b", Path: "2"},
		{Scope: "a", Path: "3"},
	})

	if !equalStrings(index.scopes(), []string{"a", "b"}) {
		t.Fatalf("顺序应当按首次出现：%v", index.scopes())
	}
	if change, _ := index.get("a"); change.Path != "3" {
		t.Fatalf("值应当是最后一次写进去的：%+v", change)
	}
}

// reconcileFixture 是对账用例的夹具：一个假文件系统，加一张活的版本表。
type reconcileFixture struct {
	t        *testing.T
	fsys     *fakeFS
	versions map[string]VersionState
	config   ResolvedConfig
}

func newReconcileFixture(t *testing.T) *reconcileFixture {
	t.Helper()
	return &reconcileFixture{
		t:        t,
		fsys:     newFakeFS().addDir("/repo/.git"),
		versions: map[string]VersionState{},
		config:   testConfig(),
	}
}

// run 跑一次对账，基线作用域默认参加。
func (f *reconcileFixture) run(request ReconcileRequest) (Reconciled, bool) {
	f.t.Helper()
	if request.Cwd == "" {
		request.Cwd = "/repo"
	}
	if request.ProjectRoot == "" {
		request.ProjectRoot = "/repo"
	}
	request.Versions = f.versions
	result, ok, err := Reconcile(f.t.Context(), f.fsys, f.config, request)
	if err != nil {
		f.t.Fatalf("对账不该失败：%v", err)
	}
	return result, ok
}

// seed 把一份文件当成「模型已经看过了」：造出对应的迁移和缓存。
func (f *reconcileFixture) seed(displayPath string, content string) Change {
	f.t.Helper()
	scope := InstructionScopeKey(displayPath)
	digest := ContentDigest(content)
	f.versions[scope] = VersionState{
		Path:          displayPath,
		Version:       fs.Version("v:" + digest),
		Digest:        digest,
		TrimmedDigest: TrimmedDigest(content),
	}
	return Change{Action: ActionSet, Scope: scope, Path: displayPath, Digest: digest}
}

func TestReconcile没有差额时什么都不说(t *testing.T) {
	fixture := newReconcileFixture(t)
	fixture.fsys.addFile("/repo/AGENTS.md", "规则")
	previous := fixture.seed("AGENTS.md", "规则")

	_, ok := fixture.run(ReconcileRequest{
		Effective:             []Change{previous},
		IncludeBaselineScopes: true,
	})

	if ok {
		t.Fatal("一切照旧时不该发任何东西")
	}
}

func TestReconcile新出现一份指令(t *testing.T) {
	fixture := newReconcileFixture(t)
	fixture.fsys.addFile("/repo/sub/AGENTS.md", "子规则")

	result, ok := fixture.run(ReconcileRequest{
		TouchedPaths: []string{"/repo/sub/file.go"},
	})

	if !ok {
		t.Fatal("新出现的指令应当被发出去")
	}
	if len(result.Changes) != 1 {
		t.Fatalf("应当正好一条迁移：%+v", result.Changes)
	}
	change := result.Changes[0]
	if change.Action != ActionSet || change.Path != "sub/AGENTS.md" {
		t.Fatalf("迁移不对：%+v", change)
	}
	if change.Digest != ContentDigest("子规则") {
		t.Fatalf("摘要不对：%s", change.Digest)
	}
	requireContains(t, result.Text, "Additional instructions from: sub/AGENTS.md")
	requireContains(t, result.Text, "子规则")
	if len(result.VersionUpdates) != 1 || result.VersionUpdates[0].State == nil {
		t.Fatalf("应当配一条写入缓存的更新：%+v", result.VersionUpdates)
	}
}

func TestReconcile内容变了发替换(t *testing.T) {
	fixture := newReconcileFixture(t)
	fixture.fsys.addFile("/repo/AGENTS.md", "旧规则")
	previous := fixture.seed("AGENTS.md", "旧规则")
	fixture.fsys.addFile("/repo/AGENTS.md", "新规则")

	result, ok := fixture.run(ReconcileRequest{
		Effective:             []Change{previous},
		IncludeBaselineScopes: true,
	})

	if !ok {
		t.Fatal("内容变了应当告诉模型")
	}
	if result.Changes[0].Action != ActionReplace {
		t.Fatalf("应当是一次替换：%+v", result.Changes[0])
	}
	requireContains(t, result.Text, "Updated instructions from: AGENTS.md")
	requireContains(t, result.Text, "新规则")
}

func TestReconcile文件没了发移除(t *testing.T) {
	fixture := newReconcileFixture(t)
	fixture.fsys.addFile("/repo/AGENTS.md", "规则")
	previous := fixture.seed("AGENTS.md", "规则")
	fixture.fsys.remove("/repo/AGENTS.md")

	result, ok := fixture.run(ReconcileRequest{
		Effective:             []Change{previous},
		IncludeBaselineScopes: true,
	})

	if !ok {
		t.Fatal("文件没了应当告诉模型")
	}
	change := result.Changes[0]
	if change.Action != ActionRemove || change.Path != "AGENTS.md" {
		t.Fatalf("应当是一次移除：%+v", change)
	}
	if change.Digest != "" {
		t.Fatal("移除那一条没有摘要")
	}
	if result.VersionUpdates[0].State != nil {
		t.Fatal("移除配的更新应当是「把这条作用域删掉」")
	}
	requireContains(t, result.Text, "Instructions removed: AGENTS.md")
}

// 模型从来没见过这条作用域，它不在也不该说什么——但缓存要清干净。
func TestReconcile没见过的作用域不在时只清缓存(t *testing.T) {
	fixture := newReconcileFixture(t)
	scope := InstructionScopeKey("AGENTS.md")
	fixture.versions[scope] = VersionState{Path: "AGENTS.md", Version: "v1"}

	_, ok := fixture.run(ReconcileRequest{IncludeBaselineScopes: true})

	if ok {
		t.Fatal("模型没见过的东西没了，不该发任何消息")
	}
	if _, still := fixture.versions[scope]; still {
		t.Fatal("缓存应当被就地清掉")
	}
}

// 版本令牌变了但内容没变（比如被原样重写了一遍）：只刷新缓存，不去打扰模型。
func TestReconcile只有版本变了时不打扰模型(t *testing.T) {
	fixture := newReconcileFixture(t)
	fixture.fsys.addFile("/repo/AGENTS.md", "规则")
	previous := fixture.seed("AGENTS.md", "规则")
	fixture.fsys.setVersion("/repo/AGENTS.md", "v:全新的令牌")

	_, ok := fixture.run(ReconcileRequest{
		Effective:             []Change{previous},
		IncludeBaselineScopes: true,
	})

	if ok {
		t.Fatal("内容没变就不该发消息")
	}
	if fixture.versions[previous.Scope].Version != "v:全新的令牌" {
		t.Fatalf("缓存应当被就地刷新到新令牌：%+v", fixture.versions[previous.Scope])
	}
}

// 同一个目录里的候选是一个去重权威组：组里还活着的成员观察不到时，
// 整组保持上一份好的状态——缓存热不热，绝不能决定一条兄弟迁移发不发。
func TestReconcile组里有一条问不出来时整组回滚(t *testing.T) {
	fixture := newReconcileFixture(t)
	fixture.fsys.addFile("/repo/AGENTS.md", "新来的规则")
	fixture.fsys.addFile("/repo/CLAUDE.md", "另一份")
	previous := fixture.seed("CLAUDE.md", "另一份")
	fixture.fsys.failStat("/repo/CLAUDE.md", errFake)

	_, ok := fixture.run(ReconcileRequest{
		Effective:             []Change{previous},
		IncludeBaselineScopes: true,
	})

	if ok {
		t.Fatal("整组应当回滚，连那条本来能发的兄弟迁移也不发")
	}
	if fixture.versions[previous.Scope].Path != "CLAUDE.md" {
		t.Fatalf("回滚之后缓存应当还是进组之前的样子：%+v", fixture.versions)
	}
}

// 问不出来的那条作用域上模型本来就没有指令，那就只跳过它，兄弟照发。
func TestReconcile问不出来但本来就没有时只跳过这一条(t *testing.T) {
	fixture := newReconcileFixture(t)
	fixture.fsys.addFile("/repo/AGENTS.md", "新来的规则")
	fixture.fsys.addFile("/repo/CLAUDE.md", "另一份").failStat("/repo/CLAUDE.md", errFake)

	result, ok := fixture.run(ReconcileRequest{IncludeBaselineScopes: true})

	if !ok {
		t.Fatal("兄弟那条应当照发")
	}
	if len(result.Changes) != 1 || result.Changes[0].Path != "AGENTS.md" {
		t.Fatalf("只该发 AGENTS.md 那一条：%+v", result.Changes)
	}
}

// 一份内容不同的文件，去空白之后和这个目录里更靠前的那份一样：丢掉它。
func TestReconcile同目录去空白重复的那份被丢掉(t *testing.T) {
	fixture := newReconcileFixture(t)
	fixture.fsys.addFile("/repo/AGENTS.md", "规则")
	fixture.fsys.addFile("/repo/CLAUDE.md", "规则\n")

	result, ok := fixture.run(ReconcileRequest{IncludeBaselineScopes: true})

	if !ok {
		t.Fatal("至少该发一条")
	}
	if len(result.Changes) != 1 || result.Changes[0].Path != "AGENTS.md" {
		t.Fatalf("只该留发现顺序最靠前的那一份：%+v", result.Changes)
	}
}

// 更靠前的兄弟此刻的内容和它一样了，那这一份就从「已经发过」变成「要撤回」。
func TestReconcile变成重复的那份被撤回(t *testing.T) {
	fixture := newReconcileFixture(t)
	fixture.fsys.addFile("/repo/AGENTS.md", "规则")
	fixture.fsys.addFile("/repo/CLAUDE.md", "本来不一样")
	first := fixture.seed("AGENTS.md", "规则")
	second := fixture.seed("CLAUDE.md", "本来不一样")
	fixture.fsys.addFile("/repo/CLAUDE.md", "规则")

	result, ok := fixture.run(ReconcileRequest{
		Effective:             []Change{first, second},
		IncludeBaselineScopes: true,
	})

	if !ok {
		t.Fatal("应当撤回那份重复的")
	}
	if len(result.Changes) != 1 {
		t.Fatalf("只该有一条迁移：%+v", result.Changes)
	}
	if result.Changes[0].Action != ActionRemove || result.Changes[0].Path != "CLAUDE.md" {
		t.Fatalf("应当是撤回 CLAUDE.md：%+v", result.Changes[0])
	}
}

// 两条**不同目录**的作用域落到同一个绝对路径上（用户全局根正好就是项目根）时，
// 只有先到的那条算数。同目录去重在这里帮不上忙——那两条作用域根本不同组。
func TestReconcile同一个绝对路径不发两遍(t *testing.T) {
	fixture := newReconcileFixture(t)
	fixture.fsys.addFile("/repo/AGENTS.md", "规则")
	fixture.config = Config{MaxBytes: 1 << 20, UserGlobalRoot: "/repo"}.Resolve()

	result, ok := fixture.run(ReconcileRequest{IncludeBaselineScopes: true})

	if !ok || len(result.Changes) != 1 {
		t.Fatalf("同一份文件只该发一次：%+v", result.Changes)
	}
	// 用户全局那一层排在最前面，所以留下来的是它。
	if result.Changes[0].Path != UserGlobalDisplayPath {
		t.Fatalf("应当留先到的那一条：%+v", result.Changes[0])
	}
	if fixture.fsys.resolveCalls["/repo/AGENTS.md"] < 2 {
		t.Fatal("两条作用域各探一次，去重发生在探测之后")
	}
}

func TestReconcile明确排除的基线作用域被撤回(t *testing.T) {
	fixture := newReconcileFixture(t)
	fixture.fsys.addFile("/repo/AGENTS.md", "规则")
	previous := fixture.seed("AGENTS.md", "规则")

	result, ok := fixture.run(ReconcileRequest{
		Effective:              []Change{previous},
		IncludeBaselineScopes:  true,
		ExcludedBaselineScopes: map[string]struct{}{previous.Scope: {}},
	})

	if !ok {
		t.Fatal("被排除的作用域上那份已发过的指令应当被撤回")
	}
	if result.Changes[0].Action != ActionRemove || result.Changes[0].Path != "AGENTS.md" {
		t.Fatalf("应当是一次移除：%+v", result.Changes[0])
	}
	if fixture.fsys.resolveCalls["/repo/AGENTS.md"] != 0 {
		t.Fatal("被排除的作用域不该被探测")
	}
}

// 基线那批作用域不参加时，基线**之外**的作用域照常参加：提示里点到的，
// 以及模型已经收到过指令的那些目录。
func TestReconcile不含基线作用域时非基线的照常参加(t *testing.T) {
	fixture := newReconcileFixture(t)
	fixture.fsys.addFile("/repo/hinted/AGENTS.md", "提示点到的")
	fixture.fsys.addFile("/repo/sent/AGENTS.md", "发过又改了的")
	fixture.fsys.addFile("/repo/AGENTS.md", "根上发过又改了的")
	previous := fixture.seed("sent/AGENTS.md", "发过的")
	// 根上那条也发过，但它属于基线那批，这一轮不参加就一个字都不提。
	baseline := fixture.seed("AGENTS.md", "根上发过的")

	result, ok := fixture.run(ReconcileRequest{
		Effective:  []Change{baseline, previous},
		ScopeHints: []Change{{Action: ActionSet, Scope: InstructionScopeKey("hinted/AGENTS.md"), Path: "hinted/AGENTS.md"}},
	})

	if !ok {
		t.Fatal("非基线的作用域应当照常参加")
	}
	if len(result.Changes) != 2 {
		t.Fatalf("两条都该发：%+v", result.Changes)
	}
	if result.Changes[0].Path != "hinted/AGENTS.md" || result.Changes[1].Path != "sent/AGENTS.md" {
		t.Fatalf("顺序不对：%+v", result.Changes)
	}
	if fixture.fsys.resolveCalls["/repo/AGENTS.md"] != 0 {
		t.Fatal("基线作用域仍然不该被探")
	}
}

// 模型已经收到过用户全局那份指令时，那条作用域按它自己那一个文件名参加，
// 不按候选列表在用户全局根下铺开。
func TestReconcile已发过的用户全局作用域按单个文件名参加(t *testing.T) {
	fixture := newReconcileFixture(t)
	fixture.config = Config{MaxBytes: 1 << 20, UserGlobalRoot: "/home/.dsh"}.Resolve()
	fixture.fsys.addFile("/home/.dsh/AGENTS.md", "全局规则")
	fixture.fsys.addFile("/home/.dsh/CLAUDE.md", "不该被看见的")
	scope := CandidateScopeKey(UserGlobalDirectory, UserGlobalFile)

	result, ok := fixture.run(ReconcileRequest{
		Effective:             []Change{{Action: ActionSet, Scope: scope, Path: UserGlobalDisplayPath, Digest: "旧"}},
		IncludeBaselineScopes: true,
	})

	if !ok {
		t.Fatal("全局那份变了应当发出去")
	}
	if len(result.Changes) != 1 || result.Changes[0].Path != UserGlobalDisplayPath {
		t.Fatalf("只该发用户全局那一条：%+v", result.Changes)
	}
	if fixture.fsys.resolveCalls["/home/.dsh/CLAUDE.md"] != 0 {
		t.Fatal("用户全局那一层不该按候选列表铺开")
	}
}

// 基线那批作用域不参加这次对账时，它们一条都不探——包括提示里点到的。
func TestReconcile不含基线作用域时跳过它们(t *testing.T) {
	fixture := newReconcileFixture(t)
	fixture.fsys.addFile("/repo/AGENTS.md", "规则")
	scope := InstructionScopeKey("AGENTS.md")

	_, ok := fixture.run(ReconcileRequest{
		ScopeHints: []Change{{Action: ActionSet, Scope: scope, Path: "AGENTS.md"}},
	})

	if ok {
		t.Fatal("基线作用域没参加，就不该有任何产出")
	}
	if fixture.fsys.resolveCalls["/repo/AGENTS.md"] != 0 {
		t.Fatal("不该探基线作用域")
	}
}

// 被排除的作用域上模型本来就没有指令时，只把缓存清干净，一个字都不说。
func TestReconcile被排除的作用域没发过时只清缓存(t *testing.T) {
	fixture := newReconcileFixture(t)
	fixture.fsys.addFile("/repo/AGENTS.md", "规则")
	scope := fixture.seed("AGENTS.md", "规则").Scope

	_, ok := fixture.run(ReconcileRequest{
		IncludeBaselineScopes:  true,
		ExcludedBaselineScopes: map[string]struct{}{scope: {}},
	})

	if ok {
		t.Fatal("没发过的东西被排除，不该说什么")
	}
	if _, still := fixture.versions[scope]; still {
		t.Fatal("缓存应当被就地清掉")
	}
}

// 两份**都没变**的兄弟，去空白之后本来就一样：靠后的那份要被撤回。
// 缓存全部命中这条路径上也得做同一件事，否则重复内容会一直留在模型那边。
func TestReconcile缓存全命中时同目录重复仍被撤回(t *testing.T) {
	fixture := newReconcileFixture(t)
	fixture.fsys.addFile("/repo/AGENTS.md", "规则")
	fixture.fsys.addFile("/repo/CLAUDE.md", "规则\n")
	first := fixture.seed("AGENTS.md", "规则")
	second := fixture.seed("CLAUDE.md", "规则\n")

	result, ok := fixture.run(ReconcileRequest{
		Effective:             []Change{first, second},
		IncludeBaselineScopes: true,
	})

	if !ok {
		t.Fatal("重复的那份应当被撤回")
	}
	if len(result.Changes) != 1 {
		t.Fatalf("只该有一条迁移：%+v", result.Changes)
	}
	if result.Changes[0].Action != ActionRemove || result.Changes[0].Path != "CLAUDE.md" {
		t.Fatalf("应当撤回 CLAUDE.md：%+v", result.Changes[0])
	}
}

// 超了单文件上限的那份读不出来，就跳过它——兄弟那条照发。
func TestReconcile读不出来的那条跳过兄弟照发(t *testing.T) {
	fixture := newReconcileFixture(t)
	fixture.fsys.addFile("/repo/AGENTS.md", strings.Repeat("超长", 100))
	fixture.fsys.addFile("/repo/CLAUDE.md", "短的")
	fixture.config = Config{MaxBytes: 1 << 20, MaxSourceBytes: 20}.Resolve()

	result, ok := fixture.run(ReconcileRequest{IncludeBaselineScopes: true})

	if !ok {
		t.Fatal("兄弟那条应当照发")
	}
	if len(result.Changes) != 1 || result.Changes[0].Path != "CLAUDE.md" {
		t.Fatalf("只该发 CLAUDE.md：%+v", result.Changes)
	}
}

// 预算小到一条迁移都代表不进去时，什么都不发、什么都不提交：
// 没提交的版本会让下一次对账重试，而不是一遍遍发只有账的文字。
func TestReconcile预算装不下任何迁移时什么都不提交(t *testing.T) {
	fixture := newReconcileFixture(t)
	fixture.fsys.addFile("/repo/AGENTS.md", strings.Repeat("规则", 200))
	fixture.config = Config{MaxBytes: 30}.Resolve()

	result, ok := fixture.run(ReconcileRequest{IncludeBaselineScopes: true})

	if ok {
		t.Fatalf("不该有产出：%+v", result)
	}
	if len(result.VersionUpdates) != 0 {
		t.Fatalf("什么都不该提交：%+v", result.VersionUpdates)
	}
}

func TestReconcile项目根留空时自己去找(t *testing.T) {
	fixture := newReconcileFixture(t)
	fixture.fsys.addFile("/repo/sub/AGENTS.md", "子规则")

	result, ok, err := Reconcile(t.Context(), fixture.fsys, fixture.config, ReconcileRequest{
		Versions:              fixture.versions,
		Cwd:                   "/repo/sub",
		IncludeBaselineScopes: true,
	})

	if err != nil || !ok {
		t.Fatalf("对账不该失败：ok=%v err=%v", ok, err)
	}
	// 根被找成了 /repo，所以显示路径是相对它的。
	if result.Changes[0].Path != "sub/AGENTS.md" {
		t.Fatalf("显示路径应当相对找到的那个根：%s", result.Changes[0].Path)
	}
}

func TestReconcile取消不降级(t *testing.T) {
	for name, projectRoot := range map[string]string{
		"探作用域时":     "/repo",
		"根留空要自己去找时": "",
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newReconcileFixture(t)
			fixture.fsys.addFile("/repo/AGENTS.md", "规则")
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			_, _, err := Reconcile(ctx, fixture.fsys, fixture.config, ReconcileRequest{
				Versions:              fixture.versions,
				Cwd:                   "/repo",
				ProjectRoot:           projectRoot,
				IncludeBaselineScopes: true,
			})

			if !errors.Is(err, context.Canceled) {
				t.Fatalf("取消必须原样报出来，实际是 %v", err)
			}
		})
	}
}

// 读一条作用域的过程中被取消：这条要一路报上去，不能退成「这份读不出来」。
func TestReconcile读的过程中被取消也报错(t *testing.T) {
	fixture := newReconcileFixture(t)
	fixture.fsys.addFile("/repo/AGENTS.md", "前半段后半段")
	fixture.fsys.chunkSize = 3
	ctx, cancel := context.WithCancel(t.Context())
	fixture.fsys.cancelAfterFirstChunk("/repo/AGENTS.md", cancel)

	_, _, err := Reconcile(ctx, fixture.fsys, fixture.config, ReconcileRequest{
		Versions:              fixture.versions,
		Cwd:                   "/repo",
		ProjectRoot:           "/repo",
		IncludeBaselineScopes: true,
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消必须原样报出来，实际是 %v", err)
	}
}

// 一整趟：基线装出来 → 折成状态 → 改一份文件 → 对账只发那一份的差额。
func TestReconcile接在基线后面只发差额(t *testing.T) {
	fsys := newFakeFS().
		addDir("/repo/.git").
		addFile("/repo/AGENTS.md", "根规则").
		addFile("/repo/sub/AGENTS.md", "子规则")
	config := testConfig()

	set, ok, err := LoadBaselineSet(t.Context(), fsys, config, "/repo/sub", "/repo", false)
	if err != nil || !ok {
		t.Fatalf("装基线不该失败：ok=%v err=%v", ok, err)
	}
	effective, versions := BaselineState(set.Included)

	fsys.addFile("/repo/sub/AGENTS.md", "改过的子规则")

	result, ok, err := Reconcile(t.Context(), fsys, config, ReconcileRequest{
		Effective:             effective,
		Versions:              versions,
		Cwd:                   "/repo/sub",
		ProjectRoot:           "/repo",
		IncludeBaselineScopes: true,
	})
	if err != nil || !ok {
		t.Fatalf("对账不该失败：ok=%v err=%v", ok, err)
	}
	if len(result.Changes) != 1 || result.Changes[0].Path != "sub/AGENTS.md" {
		t.Fatalf("只该发改过的那一份：%+v", result.Changes)
	}
	requireNotContains(t, result.Text, "根规则")

	ApplyVersionUpdates(versions, result.VersionUpdates)
	if versions[result.Changes[0].Scope].Digest != ContentDigest("改过的子规则") {
		t.Fatalf("提交之后缓存应当是新内容：%+v", versions)
	}
}
