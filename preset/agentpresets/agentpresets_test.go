// 本文件的作用：把这个包守的那几条钉住——坏掉的预设留在名单上但装不了、展示文字
// 坏了绝不致命、一份预设只装一次而且换代看文件戳、任何一行装不起来整份回滚、
// 创作只写 user 根而且只有整目录复制这一种写、会话跑在哪一份要读日志。
//
// 逐条对着 DSH 的 tests/agent-presets.spec.ts 那几组用例走，只是把 cordis 那台
// 动态装载器换成了这里的组装器名册。
//
// # 这些测试防的是什么错
//
//   - **一份坏掉的预设被藏起来**。它那个目录仍旧占着 id，而界面上没有任何东西可看、
//     可删，用户只会看到「这个名字用不了」而找不到原因。
//   - **一个读不出名字的预设整个装不起来**。展示不是能力，一个坏名字不该变成一个
//     起不来的 agent。
//   - **一次装到一半的失败留下了东西**。半份组合比一份都没有更坏：它装得起来但不完整。
//   - **两个 agent 抢同一份预设时各装了一份**。那些插件实例、工具登记、提示词段落
//     就存在了两遍，而它们是按会话键着的、本来就为一个共享世界写的。
//   - **一份组合文件改了之后老会话被换代波及**。已经认进去的会话必须留在它跑着的
//     那一代上，否则它那段历史模型已经没法照着做了。
//   - **创作能写到发出去的那一套上**。那会把「重置回一份已知的预设」变成同一个调用方
//     先前就能弄坏的东西。
//   - **重建只读创建头**。一个还空着时换过预设的会话会被按它**建出来时**那份组合
//     重建，而不是它那段历史真正产生于其下的那一份。
package agentpresets_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/preset/agentpresets"
	"github.com/snight1983/ds-harness-go/session"
)

// writePreset 在 root 底下摆出一份预设目录，composition 是它那份组合文件的内容。
//
// metadata 为空串时不写 preset.yml——一份靠复制别人建出来的预设正是这样。
func writePreset(t *testing.T, root, id, composition, metadata string) string {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("建不出预设目录：%v", err)
	}
	if composition != "" {
		path := filepath.Join(dir, agentpresets.CompositionFile)
		if err := os.WriteFile(path, []byte(composition), 0o600); err != nil {
			t.Fatalf("写不了组合文件：%v", err)
		}
	}
	if metadata != "" {
		path := filepath.Join(dir, agentpresets.MetadataFile)
		if err := os.WriteFile(path, []byte(metadata), 0o600); err != nil {
			t.Fatalf("写不了元数据：%v", err)
		}
	}
	return dir
}

// countingComposer 造一个记下自己被装了几次、被摘了几次的组装器。
func countingComposer(mounted, disposed *int) agentpresets.Composer {
	var mutex sync.Mutex
	return func(_ context.Context, _ *scope.Scope, _ json.RawMessage) (func(context.Context) error, error) {
		mutex.Lock()
		*mounted++
		mutex.Unlock()
		return func(context.Context) error {
			mutex.Lock()
			*disposed++
			mutex.Unlock()
			return nil
		}, nil
	}
}

// findPreset 从一份名单里挑出那个 id。
func findPreset(t *testing.T, presets []agentpresets.Preset, id string) agentpresets.Preset {
	t.Helper()
	for _, preset := range presets {
		if preset.ID == id {
			return preset
		}
	}
	t.Fatalf("名单里没有 %q", id)
	return agentpresets.Preset{}
}

func TestABrokenPresetStaysOnTheRosterCarryingItsReason(t *testing.T) {
	root := t.TempDir()
	writePreset(t, root, "good", "- name: demo\n", "")
	// 目录在、组合文件不在：这个目录仍旧占着 id。
	writePreset(t, root, "ghost", "", "")
	writePreset(t, root, "torn", "name: demo\n", "")
	writePreset(t, root, "garbled", "- name: demo\n  config: [\n", "")

	presets, err := agentpresets.DiscoverPresets([]agentpresets.Root{{Path: root, Trust: agentpresets.TrustSystem}})
	if err != nil {
		t.Fatalf("发现失败：%v", err)
	}
	if len(presets) != 4 {
		t.Fatalf("四个目录都该在名单上，得到 %d 份", len(presets))
	}
	if broken := findPreset(t, presets, "good").Broken; broken != "" {
		t.Fatalf("好的那份不该带理由，却带了 %q", broken)
	}
	if broken := findPreset(t, presets, "ghost").Broken; !strings.Contains(broken, "is missing") {
		t.Fatalf("幽灵目录的理由该说组合文件缺了，得到 %q", broken)
	}
	if broken := findPreset(t, presets, "torn").Broken; !strings.Contains(broken, "top-level list") {
		t.Fatalf("不是行列表的理由该这么说，得到 %q", broken)
	}
	if broken := findPreset(t, presets, "garbled").Broken; !strings.Contains(broken, "not valid YAML") {
		t.Fatalf("解不动的理由该这么说，得到 %q", broken)
	}
}

func TestADirectoryWhoseNameNoCopyCouldClaimIsNotAPresetSlot(t *testing.T) {
	root := t.TempDir()
	writePreset(t, root, "kept", "- name: demo\n", "")
	// 大写、下划线、点开头：没有任何副本能取到这些名字，所以它们什么都不挡。
	writePreset(t, root, ".DS_Store_dir", "", "")
	writePreset(t, root, "Upper", "", "")
	writePreset(t, root, "under_score", "", "")

	presets, err := agentpresets.DiscoverPresets([]agentpresets.Root{{Path: root, Trust: agentpresets.TrustSystem}})
	if err != nil {
		t.Fatalf("发现失败：%v", err)
	}
	if len(presets) != 1 || presets[0].ID != "kept" {
		t.Fatalf("只有 kept 该上名单，得到 %+v", presets)
	}
}

func TestAMissingRootSuppliesNoPresetsRatherThanFailing(t *testing.T) {
	presets, err := agentpresets.DiscoverPresets([]agentpresets.Root{
		{Path: filepath.Join(t.TempDir(), "never-written"), Trust: agentpresets.TrustUser},
	})
	if err != nil {
		t.Fatalf("一个不存在的根不该报错：%v", err)
	}
	if len(presets) != 0 {
		t.Fatalf("该是零份，得到 %d 份", len(presets))
	}
}

func TestDeclaredOrderSortsAheadAndTheRestFallBackToId(t *testing.T) {
	root := t.TempDir()
	writePreset(t, root, "zulu", "- name: demo\n", "order: 1\n")
	writePreset(t, root, "alpha", "- name: demo\n", "")
	writePreset(t, root, "bravo", "- name: demo\n", "")
	writePreset(t, root, "mike", "- name: demo\n", "order: 0\n")

	presets, err := agentpresets.DiscoverPresets([]agentpresets.Root{{Path: root, Trust: agentpresets.TrustSystem}})
	if err != nil {
		t.Fatalf("发现失败：%v", err)
	}
	var ids []string
	for _, preset := range presets {
		ids = append(ids, preset.ID)
	}
	want := []string{"mike", "zulu", "alpha", "bravo"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("排序不对：得到 %v，要 %v", ids, want)
	}
}

func TestAnEarlierRootWinsADuplicateId(t *testing.T) {
	shipped, authored := t.TempDir(), t.TempDir()
	writePreset(t, shipped, "shared", "- name: demo\n", "name: Shipped\n")
	writePreset(t, authored, "shared", "- name: demo\n", "name: Authored\n")

	presets, err := agentpresets.DiscoverPresets([]agentpresets.Root{
		{Path: shipped, Trust: agentpresets.TrustSystem},
		{Path: authored, Trust: agentpresets.TrustUser},
	})
	if err != nil {
		t.Fatalf("发现失败：%v", err)
	}
	if len(presets) != 1 {
		t.Fatalf("同 id 只该留一份，得到 %d 份", len(presets))
	}
	if presets[0].Name != "Shipped" || presets[0].Trust != agentpresets.TrustSystem {
		t.Fatalf("靠前的根该赢，得到 %+v", presets[0])
	}
}

func TestUnreadableDisplayTextIsNeverFatal(t *testing.T) {
	root := t.TempDir()
	// 解不动的元数据、以及一份不是映射的元数据：两种都回落到显示 id。
	writePreset(t, root, "broken-meta", "- name: demo\n", "name: [\n")
	writePreset(t, root, "list-meta", "- name: demo\n", "- not-a-map\n")

	presets, err := agentpresets.DiscoverPresets([]agentpresets.Root{{Path: root, Trust: agentpresets.TrustSystem}})
	if err != nil {
		t.Fatalf("发现失败：%v", err)
	}
	for _, preset := range presets {
		if preset.Broken != "" {
			t.Fatalf("%s 该照样装得起来，却带了理由 %q", preset.ID, preset.Broken)
		}
		if preset.DisplayName() != preset.ID {
			t.Fatalf("%s 该回落到显示 id，得到 %q", preset.ID, preset.DisplayName())
		}
	}
}

func TestRenderMetadataWritesNothingWhenThereIsNothingToStore(t *testing.T) {
	if _, has := agentpresets.RenderMetadata(agentpresets.Metadata{Name: "  ", Description: "\t"}); has {
		t.Fatal("全是空白时不该有东西要存")
	}
	rendered, has := agentpresets.RenderMetadata(agentpresets.Metadata{Description: "why"})
	if !has {
		t.Fatal("有说明时该有东西要存")
	}
	if strings.Contains(rendered, "name:") || strings.Contains(rendered, "order:") {
		t.Fatalf("缺席的字段该整个不写出来，得到 %q", rendered)
	}
}

// newRoster 立起一份只有一个 system 根的名册，用完自动收。
func newRoster(t *testing.T, root string, composers agentpresets.ComposerSet, defaultID string) *agentpresets.Roster {
	t.Helper()
	roster, err := agentpresets.New(agentpresets.Config{
		Default:   defaultID,
		Roots:     []agentpresets.Root{{Path: root, Trust: agentpresets.TrustSystem}},
		Composers: composers,
	}, nil)
	if err != nil {
		t.Fatalf("立不起名册：%v", err)
	}
	t.Cleanup(func() { _ = roster.Close(context.Background()) })
	return roster
}

func TestOnePresetMountsOnceAndEveryAgentNamingItJoinsThatOne(t *testing.T) {
	root := t.TempDir()
	writePreset(t, root, "demo", "- name: alpha\n- name: beta\n", "")
	var mounted, disposed int
	composer := countingComposer(&mounted, &disposed)
	roster := newRoster(t, root, agentpresets.ComposerSet{"alpha": composer, "beta": composer}, "demo")

	ctx := context.Background()
	first, second := scope.NewKey("agent-1"), scope.NewKey("agent-2")
	if _, err := roster.Mount(ctx, first, "demo"); err != nil {
		t.Fatalf("第一个 agent 装不上：%v", err)
	}
	if _, err := roster.Mount(ctx, second, "demo"); err != nil {
		t.Fatalf("第二个 agent 装不上：%v", err)
	}
	if mounted != 2 {
		t.Fatalf("两行只该各装一次，共 2 次，得到 %d 次", mounted)
	}
	if scope.ParentOf(first) != scope.ParentOf(second) {
		t.Fatal("两个 agent 该认到同一把常驻钥匙下")
	}
	if got := roster.ComposedPreset(first); got != "demo" {
		t.Fatalf("该答 demo，得到 %q", got)
	}
	if live := roster.LiveMounts(); len(live) != 1 || live[0].PresetID != "demo" {
		t.Fatalf("该只有一份常驻装载，得到 %+v", live)
	}
}

func TestARowThatCannotActivateRollsTheWholeMountBack(t *testing.T) {
	root := t.TempDir()
	writePreset(t, root, "demo", "- name: alpha\n- name: nobody\n", "")
	var mounted, disposed int
	roster := newRoster(t, root, agentpresets.ComposerSet{
		"alpha": countingComposer(&mounted, &disposed),
	}, "demo")

	_, err := roster.Mount(context.Background(), scope.NewKey("agent"), "demo")
	if err == nil {
		t.Fatal("点了一个不认识的组装器该失败")
	}
	if !errors.Is(err, agentpresets.ErrPresetMount) || !errors.Is(err, agentpresets.ErrUnknownComposer) {
		t.Fatalf("该同时认得上层分类和底下的原因，得到 %v", err)
	}
	if !strings.Contains(err.Error(), "row 2") || !strings.Contains(err.Error(), "nobody") {
		t.Fatalf("诊断该逐行指出是哪一行，得到 %q", err.Error())
	}
	if mounted != 1 || disposed != 1 {
		t.Fatalf("装上的那一行该被摘干净：装 %d 次、摘 %d 次", mounted, disposed)
	}
	if live := roster.LiveMounts(); len(live) != 0 {
		t.Fatalf("一次失败的装载不该留下任何东西，得到 %+v", live)
	}
}

func TestAFailedMountIsRetriedOnceTheFileIsFixed(t *testing.T) {
	root := t.TempDir()
	dir := writePreset(t, root, "demo", "- name: nobody\n", "")
	var mounted, disposed int
	roster := newRoster(t, root, agentpresets.ComposerSet{
		"alpha": countingComposer(&mounted, &disposed),
	}, "demo")

	ctx := context.Background()
	if _, err := roster.Mount(ctx, scope.NewKey("first"), "demo"); err == nil {
		t.Fatal("第一次该失败")
	}
	path := filepath.Join(dir, agentpresets.CompositionFile)
	if err := os.WriteFile(path, []byte("- name: alpha\n"), 0o600); err != nil {
		t.Fatalf("改不了组合文件：%v", err)
	}
	if _, err := roster.Mount(ctx, scope.NewKey("second"), "demo"); err != nil {
		t.Fatalf("文件修好之后该装得上：%v", err)
	}
	if mounted != 1 {
		t.Fatalf("修好之后该装上一行，得到 %d 次", mounted)
	}
}

func TestDisabledRowsAreSkippedAndGroupsFlatten(t *testing.T) {
	root := t.TempDir()
	writePreset(t, root, "demo", strings.Join([]string{
		"- name: alpha",
		"- name: alpha",
		"  disabled: true",
		"- name: shelf",
		"  group: true",
		"  config:",
		"    - name: alpha",
		"    - name: alpha",
		"",
	}, "\n"), "")
	var mounted, disposed int
	roster := newRoster(t, root, agentpresets.ComposerSet{
		"alpha": countingComposer(&mounted, &disposed),
	}, "demo")

	if _, err := roster.Mount(context.Background(), scope.NewKey("agent"), "demo"); err != nil {
		t.Fatalf("装不上：%v", err)
	}
	// 一行开着 + 一行关着（跳过）+ 组里两行摊平 = 3 次。组本身不点组装器。
	if mounted != 3 {
		t.Fatalf("该装 3 行，得到 %d 行", mounted)
	}
}

func TestARowsConfigReachesItsComposerAsJSON(t *testing.T) {
	root := t.TempDir()
	writePreset(t, root, "demo", "- name: alpha\n  config:\n    depth: 3\n    label: hi\n", "")
	var seen json.RawMessage
	roster := newRoster(t, root, agentpresets.ComposerSet{
		"alpha": func(_ context.Context, _ *scope.Scope, config json.RawMessage) (func(context.Context) error, error) {
			seen = config
			return nil, nil
		},
	}, "demo")

	if _, err := roster.Mount(context.Background(), scope.NewKey("agent"), "demo"); err != nil {
		t.Fatalf("装不上：%v", err)
	}
	var decoded struct {
		Depth int    `json:"depth"`
		Label string `json:"label"`
	}
	if err := json.Unmarshal(seen, &decoded); err != nil {
		t.Fatalf("config 该是一段能用 encoding/json 解的字节：%v", err)
	}
	if decoded.Depth != 3 || decoded.Label != "hi" {
		t.Fatalf("config 没原样传到，得到 %+v", decoded)
	}
}

func TestEditingTheCompositionStartsANewGenerationForLaterSessions(t *testing.T) {
	root := t.TempDir()
	dir := writePreset(t, root, "demo", "- name: alpha\n", "")
	var mounted, disposed int
	roster := newRoster(t, root, agentpresets.ComposerSet{
		"alpha": countingComposer(&mounted, &disposed),
	}, "demo")

	ctx := context.Background()
	early := scope.NewKey("early")
	if _, err := roster.Mount(ctx, early, "demo"); err != nil {
		t.Fatalf("装不上：%v", err)
	}
	earlyStanding := scope.ParentOf(early)

	path := filepath.Join(dir, agentpresets.CompositionFile)
	if err := os.WriteFile(path, []byte("- name: alpha\n- name: alpha\n"), 0o600); err != nil {
		t.Fatalf("改不了组合文件：%v", err)
	}

	late := scope.NewKey("late")
	if _, err := roster.Mount(ctx, late, "demo"); err != nil {
		t.Fatalf("换代之后装不上：%v", err)
	}
	if scope.ParentOf(late) == earlyStanding {
		t.Fatal("文件变了之后该开下一代")
	}
	if scope.ParentOf(early) != earlyStanding {
		t.Fatal("已经认进去的会话必须留在它跑着的那一代上")
	}
	if disposed != 0 {
		t.Fatalf("被取代的那一代不许在进程还活着时被摘掉，得到摘 %d 次", disposed)
	}
	if mounted != 3 {
		t.Fatalf("第一代一行、第二代两行，共 3 次，得到 %d 次", mounted)
	}
}

func TestABrokenPresetIsRefusedBeforeAnyMountIsAttempted(t *testing.T) {
	root := t.TempDir()
	writePreset(t, root, "ghost", "", "")
	roster := newRoster(t, root, agentpresets.ComposerSet{}, "ghost")

	if _, err := roster.Resolve("ghost"); err != nil {
		t.Fatalf("坏掉的预设照样该解算得出来（删它、读它都要这一行）：%v", err)
	}
	_, err := roster.Mount(context.Background(), scope.NewKey("agent"), "ghost")
	if !errors.Is(err, agentpresets.ErrPresetMount) {
		t.Fatalf("装它该被拒，得到 %v", err)
	}
	if !strings.Contains(err.Error(), "is missing") {
		t.Fatalf("该带上发现给的那句理由，得到 %q", err.Error())
	}
}

func TestAnUnknownIdIsADifferentErrorFromAnUnusableComposition(t *testing.T) {
	root := t.TempDir()
	writePreset(t, root, "demo", "- name: alpha\n", "")
	roster := newRoster(t, root, agentpresets.ComposerSet{}, "demo")

	_, err := roster.Resolve("nowhere")
	if !errors.Is(err, agentpresets.ErrUnknownPreset) {
		t.Fatalf("该是「名册里没这份」，得到 %v", err)
	}
	if errors.Is(err, agentpresets.ErrPresetMount) {
		t.Fatal("一次坏请求不该被读成一份坏预设")
	}
	var unknown *agentpresets.UnknownPresetError
	if !errors.As(err, &unknown) || len(unknown.Available) != 1 || unknown.Available[0] != "demo" {
		t.Fatalf("该带上名册确实供得出的那些，得到 %v", err)
	}
}

func TestComposeFromJoinsTheParentsExactGeneration(t *testing.T) {
	root := t.TempDir()
	dir := writePreset(t, root, "demo", "- name: alpha\n", "")
	var mounted, disposed int
	roster := newRoster(t, root, agentpresets.ComposerSet{
		"alpha": countingComposer(&mounted, &disposed),
	}, "demo")

	ctx := context.Background()
	parent := scope.NewKey("parent")
	if _, err := roster.Mount(ctx, parent, "demo"); err != nil {
		t.Fatalf("父装不上：%v", err)
	}
	// 父启动之后组合文件被编辑过：孩子仍然要拿到父那一代，不是新的那一代。
	path := filepath.Join(dir, agentpresets.CompositionFile)
	if err := os.WriteFile(path, []byte("- name: alpha\n- name: alpha\n"), 0o600); err != nil {
		t.Fatalf("改不了组合文件：%v", err)
	}

	child := scope.NewKey("child")
	joined, err := roster.ComposeFrom(child, parent)
	if err != nil {
		t.Fatalf("认亲失败：%v", err)
	}
	if joined != "demo" {
		t.Fatalf("该答 demo，得到 %q", joined)
	}
	if scope.ParentOf(child) != scope.ParentOf(parent) {
		t.Fatal("孩子该拿到父那一代，不是重新解算出来的一代")
	}
	if mounted != 1 {
		t.Fatalf("认亲不该装任何东西，得到 %d 次", mounted)
	}
}

func TestAParentThatJoinedNoPresetYieldsNoJoinAndNoError(t *testing.T) {
	root := t.TempDir()
	writePreset(t, root, "demo", "- name: alpha\n", "")
	roster := newRoster(t, root, agentpresets.ComposerSet{}, "demo")

	child := scope.NewKey("child")
	joined, err := roster.ComposeFrom(child, scope.NewKey("bare-parent"))
	if err != nil {
		t.Fatalf("一个没认预设的父不该报错：%v", err)
	}
	if joined != "" {
		t.Fatalf("该答「没认」，得到 %q", joined)
	}
	if scope.ParentOf(child) != nil {
		t.Fatal("既然没认，孩子也不该被绑到任何东西上")
	}
}

func TestRecomposeMovesTheLinkAndLeavesTheOldCompositionForItsOtherAgents(t *testing.T) {
	root := t.TempDir()
	writePreset(t, root, "one", "- name: alpha\n", "")
	writePreset(t, root, "two", "- name: alpha\n", "")
	var mounted, disposed int
	roster := newRoster(t, root, agentpresets.ComposerSet{
		"alpha": countingComposer(&mounted, &disposed),
	}, "one")

	ctx := context.Background()
	stay, move := scope.NewKey("stay"), scope.NewKey("move")
	if _, err := roster.Mount(ctx, stay, "one"); err != nil {
		t.Fatalf("装不上：%v", err)
	}
	if _, err := roster.Mount(ctx, move, "one"); err != nil {
		t.Fatalf("装不上：%v", err)
	}
	oldStanding := scope.ParentOf(stay)

	if _, err := roster.Recompose(ctx, move, "two"); err != nil {
		t.Fatalf("改组装失败：%v", err)
	}
	if scope.ParentOf(move) == oldStanding {
		t.Fatal("改组装该把链挪走")
	}
	if scope.ParentOf(stay) != oldStanding {
		t.Fatal("旧那份该留给它别的 agent")
	}
	if disposed != 0 {
		t.Fatalf("改组装不是一次卸载，得到摘 %d 次", disposed)
	}
	if roster.ComposedPreset(move) != "two" || roster.ComposedPreset(stay) != "one" {
		t.Fatal("两个 agent 各自跑在哪一份该分得清")
	}
}

func TestRecomposeToAnUnusablePresetLeavesTheAgentExactlyAsItWas(t *testing.T) {
	root := t.TempDir()
	writePreset(t, root, "one", "- name: alpha\n", "")
	writePreset(t, root, "ghost", "", "")
	var mounted, disposed int
	roster := newRoster(t, root, agentpresets.ComposerSet{
		"alpha": countingComposer(&mounted, &disposed),
	}, "one")

	ctx := context.Background()
	agent := scope.NewKey("agent")
	if _, err := roster.Mount(ctx, agent, "one"); err != nil {
		t.Fatalf("装不上：%v", err)
	}
	before := scope.ParentOf(agent)

	if _, err := roster.Recompose(ctx, agent, "ghost"); err == nil {
		t.Fatal("改到一份用不了的预设该失败")
	}
	if scope.ParentOf(agent) != before {
		t.Fatal("失败之后 agent 该原封不动")
	}
}

func TestRecomposingABareAgentIsItsFirstBind(t *testing.T) {
	root := t.TempDir()
	writePreset(t, root, "one", "- name: alpha\n", "")
	var mounted, disposed int
	roster := newRoster(t, root, agentpresets.ComposerSet{
		"alpha": countingComposer(&mounted, &disposed),
	}, "one")

	agent := scope.NewKey("bare")
	if _, err := roster.Recompose(context.Background(), agent, "one"); err != nil {
		t.Fatalf("一个从没组装过的 agent 该能改组装：%v", err)
	}
	if roster.ComposedPreset(agent) != "one" {
		t.Fatal("那次调换就是它的第一次认亲")
	}
}

func TestAColdReaderResolvesTheSameStandingRegistrationsWithNoAgent(t *testing.T) {
	root := t.TempDir()
	writePreset(t, root, "demo", "- name: alpha\n", "")
	var mounted, disposed int
	roster := newRoster(t, root, agentpresets.ComposerSet{
		"alpha": countingComposer(&mounted, &disposed),
	}, "demo")

	ctx := context.Background()
	agent := scope.NewKey("agent")
	if _, err := roster.Mount(ctx, agent, "demo"); err != nil {
		t.Fatalf("装不上：%v", err)
	}
	key, err := roster.StandingKeyFor(ctx, "demo")
	if err != nil {
		t.Fatalf("冷读拿不到常驻钥匙：%v", err)
	}
	if key != scope.ParentOf(agent) {
		t.Fatal("冷读该解算到同一批常驻登记")
	}
	if mounted != 1 {
		t.Fatalf("冷读不该再装一遍，得到 %d 次", mounted)
	}
}

// fixedDefault 是一层写死的用户默认，用来验分层。
type fixedDefault struct {
	value   string
	cleared bool
}

func (d *fixedDefault) Default() string { return d.value }

func (d *fixedDefault) ClearDefault(context.Context) error {
	d.value, d.cleared = "", true
	return nil
}

func TestTheUserDefaultLayersOverTheDeploymentDefault(t *testing.T) {
	root := t.TempDir()
	writePreset(t, root, "shipped", "- name: alpha\n", "")
	writePreset(t, root, "chosen", "- name: alpha\n", "")
	layer := &fixedDefault{value: "chosen"}
	roster, err := agentpresets.New(agentpresets.Config{
		Default:   "shipped",
		Roots:     []agentpresets.Root{{Path: root, Trust: agentpresets.TrustSystem}},
		Composers: agentpresets.ComposerSet{},
	}, layer)
	if err != nil {
		t.Fatalf("立不起名册：%v", err)
	}
	t.Cleanup(func() { _ = roster.Close(context.Background()) })

	if got := roster.DefaultID(); got != "chosen" {
		t.Fatalf("用户那一层该盖在上面，得到 %q", got)
	}
	layer.value = ""
	if got := roster.DefaultID(); got != "shipped" {
		t.Fatalf("那一层空了该露出底下的，得到 %q", got)
	}
}

func TestCopyingCarriesTheWholeDirectoryAndRewritesTheDisplayText(t *testing.T) {
	shipped, authored := t.TempDir(), t.TempDir()
	dir := writePreset(t, shipped, "source", "- name: alpha\n", "name: Source\ndescription: why\norder: 2\n")
	// 一份预设是它那个目录，不是那一个文件：子目录和素材也要跟过去。
	if err := os.MkdirAll(filepath.Join(dir, "skills", "nested"), 0o700); err != nil {
		t.Fatalf("建不出子目录：%v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skills", "nested", "asset.txt"), []byte("payload"), 0o600); err != nil {
		t.Fatalf("写不了素材：%v", err)
	}

	roster, err := agentpresets.New(agentpresets.Config{
		Default: "source",
		Roots: []agentpresets.Root{
			{Path: shipped, Trust: agentpresets.TrustSystem},
			{Path: authored, Trust: agentpresets.TrustUser},
		},
		Composers: agentpresets.ComposerSet{},
	}, nil)
	if err != nil {
		t.Fatalf("立不起名册：%v", err)
	}
	t.Cleanup(func() { _ = roster.Close(context.Background()) })

	if err := roster.Copy("source", "mine", ""); err != nil {
		t.Fatalf("复制失败：%v", err)
	}
	copied := filepath.Join(authored, "mine")
	asset, err := os.ReadFile(filepath.Join(copied, "skills", "nested", "asset.txt"))
	if err != nil || string(asset) != "payload" {
		t.Fatalf("素材该跟过去：%v / %q", err, asset)
	}
	metadata := agentpresets.ReadMetadata(copied)
	if metadata.Description != "why" {
		t.Fatalf("说明该留着，得到 %q", metadata.Description)
	}
	if metadata.Name != "" || metadata.Order != nil {
		t.Fatalf("名字和位次不该留，得到 %+v", metadata)
	}
}

func TestACopyWithNothingToPublishWritesNoMetadataFileAtAll(t *testing.T) {
	shipped, authored := t.TempDir(), t.TempDir()
	writePreset(t, shipped, "source", "- name: alpha\n", "order: 1\n")
	roster, err := agentpresets.New(agentpresets.Config{
		Default: "source",
		Roots: []agentpresets.Root{
			{Path: shipped, Trust: agentpresets.TrustSystem},
			{Path: authored, Trust: agentpresets.TrustUser},
		},
		Composers: agentpresets.ComposerSet{},
	}, nil)
	if err != nil {
		t.Fatalf("立不起名册：%v", err)
	}
	t.Cleanup(func() { _ = roster.Close(context.Background()) })

	if err := roster.Copy("source", "mine", ""); err != nil {
		t.Fatalf("复制失败：%v", err)
	}
	path := filepath.Join(authored, "mine", agentpresets.MetadataFile)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("什么都没得发布时那个文件该不在，得到 %v", err)
	}
}

func TestACopyNeverOverwritesAndTheIdIsAFence(t *testing.T) {
	shipped, authored := t.TempDir(), t.TempDir()
	writePreset(t, shipped, "source", "- name: alpha\n", "")
	writePreset(t, authored, "taken", "- name: alpha\n", "")
	roster, err := agentpresets.New(agentpresets.Config{
		Default: "source",
		Roots: []agentpresets.Root{
			{Path: shipped, Trust: agentpresets.TrustSystem},
			{Path: authored, Trust: agentpresets.TrustUser},
		},
		Composers: agentpresets.ComposerSet{},
	}, nil)
	if err != nil {
		t.Fatalf("立不起名册：%v", err)
	}
	t.Cleanup(func() { _ = roster.Close(context.Background()) })

	if err := roster.Copy("source", "taken", ""); !errors.Is(err, agentpresets.ErrPresetExists) {
		t.Fatalf("占了的 id 该被拒，得到 %v", err)
	}
	// 发出去的那一套也算占着——一个和它同名的用户目录会被它遮蔽。
	if err := roster.Copy("source", "source", ""); !errors.Is(err, agentpresets.ErrPresetExists) {
		t.Fatalf("发出去的 id 也该算占着，得到 %v", err)
	}
	for _, bad := range []string{"../escape", "Upper", "under_score", "", "-lead"} {
		if err := roster.Copy("source", bad, ""); !errors.Is(err, agentpresets.ErrInvalidPresetID) {
			t.Fatalf("id %q 该被围栏拦下，得到 %v", bad, err)
		}
	}
}

func TestAuthoringOnlyWritesTheUserRoot(t *testing.T) {
	shipped := t.TempDir()
	writePreset(t, shipped, "source", "- name: alpha\n", "")
	roster := newRoster(t, shipped, agentpresets.ComposerSet{}, "source")

	if roster.Authorable() {
		t.Fatal("没有 user 根时不该说自己能创作")
	}
	if err := roster.Copy("source", "mine", ""); !errors.Is(err, agentpresets.ErrPresetNotWritable) {
		t.Fatalf("没有可写根时复制该被拒，得到 %v", err)
	}
	if err := roster.Remove(context.Background(), "source"); !errors.Is(err, agentpresets.ErrPresetNotWritable) {
		t.Fatalf("发出去的预设不许删，得到 %v", err)
	}
	if _, err := os.Stat(filepath.Join(shipped, "source", agentpresets.CompositionFile)); err != nil {
		t.Fatalf("它该原封不动：%v", err)
	}
}

func TestRemovingTheUserDefaultClearsItSoTheDeploymentDefaultShowsThrough(t *testing.T) {
	shipped, authored := t.TempDir(), t.TempDir()
	writePreset(t, shipped, "shipped", "- name: alpha\n", "")
	writePreset(t, authored, "mine", "- name: alpha\n", "")
	layer := &fixedDefault{value: "mine"}
	roster, err := agentpresets.New(agentpresets.Config{
		Default: "shipped",
		Roots: []agentpresets.Root{
			{Path: shipped, Trust: agentpresets.TrustSystem},
			{Path: authored, Trust: agentpresets.TrustUser},
		},
		Composers: agentpresets.ComposerSet{},
	}, layer)
	if err != nil {
		t.Fatalf("立不起名册：%v", err)
	}
	t.Cleanup(func() { _ = roster.Close(context.Background()) })

	if err := roster.Remove(context.Background(), "mine"); err != nil {
		t.Fatalf("删自己创作的该成：%v", err)
	}
	if !layer.cleared {
		t.Fatal("刚删掉的那个默认该被清掉")
	}
	if got := roster.DefaultID(); got != "shipped" {
		t.Fatalf("该露出部署自己的默认，得到 %q", got)
	}
	if _, err := os.Stat(filepath.Join(authored, "mine")); !os.IsNotExist(err) {
		t.Fatalf("那个目录该没了，得到 %v", err)
	}
}

func TestReadHandsBackTheCompositionExactlyAsStored(t *testing.T) {
	root := t.TempDir()
	const composition = "- name: alpha\n  config:\n    depth: 3\n"
	writePreset(t, root, "demo", composition, "")
	roster := newRoster(t, root, agentpresets.ComposerSet{}, "demo")

	got, err := roster.Read("demo")
	if err != nil {
		t.Fatalf("读不出来：%v", err)
	}
	if got != composition {
		t.Fatalf("该原样交出，得到 %q", got)
	}
}

// selectedEvent 造一条 agent-preset/selected。
func selectedEvent(t *testing.T, seq int, id string) session.Event {
	t.Helper()
	data, err := json.Marshal(agentpresets.SelectedData{AgentPreset: id})
	if err != nil {
		t.Fatalf("排不出负载：%v", err)
	}
	return session.Event{Seq: seq, Type: agentpresets.EventPresetSelected, Data: data}
}

func TestTheSessionRunsOnTheNewestSelectionNotTheCreationHeader(t *testing.T) {
	header := session.SessionHeader{AgentPreset: "created-with"}
	events := []session.Event{
		{Seq: 1, Type: session.EventUserMessage},
		selectedEvent(t, 2, "switched-to"),
		{Seq: 3, Type: session.EventUserMessage},
		selectedEvent(t, 4, "switched-again"),
	}
	if got := agentpresets.ResolveSessionPreset(header, events); got != "switched-again" {
		t.Fatalf("最新的一次选择该算数，得到 %q", got)
	}
	if got := agentpresets.ResolveSessionPreset(header, nil); got != "created-with" {
		t.Fatalf("一条选择都没有时该回落到头，得到 %q", got)
	}
	if got := agentpresets.ResolveSessionPreset(session.SessionHeader{}, nil); got != "" {
		t.Fatalf("一份预设都不装的部署该答空串，得到 %q", got)
	}
}

func TestAnUnreadableSelectionFallsBackInsteadOfAnsweringNothing(t *testing.T) {
	header := session.SessionHeader{AgentPreset: "created-with"}
	events := []session.Event{
		selectedEvent(t, 1, "earlier"),
		{Seq: 2, Type: agentpresets.EventPresetSelected, Data: json.RawMessage(`{"agentPreset":`)},
	}
	if got := agentpresets.ResolveSessionPreset(header, events); got != "earlier" {
		t.Fatalf("读不回来的那条该被跳过，得到 %q", got)
	}
}

func TestTheVocabularyMustBeExtendedOrASwitchedLogIsRefused(t *testing.T) {
	events := []session.Event{selectedEvent(t, 1, "demo")}
	if err := session.CheckVocabulary(events, session.CoreVocabulary()); err == nil {
		t.Fatal("不拼这张单子的话，一段换过预设的日志该被拒")
	}
	extended := session.CoreVocabulary().With(agentpresets.EventTypes()...)
	if err := session.CheckVocabulary(events, extended); err != nil {
		t.Fatalf("拼上之后该认得：%v", err)
	}
}
