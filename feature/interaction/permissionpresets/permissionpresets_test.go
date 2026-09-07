// 本文件验这个包的全部行为：一份配置在哪些地方被拒、旋钮怎么折回预设名、
// 两条写路径各写下去什么、一条新会话的初值怎么钉，以及那份默认选择怎么被用户段盖住。
//
// 源: packages/interaction/permission-presets/src/index.ts
//
// # 这些测试防的是什么错
//
//   - **用户选的那个名字被静默换成另一个**。两条预设捆着同一份旋钮值时，只看旋钮
//     是分不开它们的；[EventPreset] 就是为这件事存在的，一旦 derive 不再优先认它，
//     用户会看见自己刚点的那一项变成了旁边那一项。
//   - **一次切换写了两遍旋钮，或者一遍都没写**。写重了会在日志里留下一条没有意义的
//     翻转，写漏了则是界面显示切过去了、执行那一侧还按老策略拦人。
//   - **[CustomPreset] 变成了一个能切过去的目标**。它是派生值，一旦被写进
//     [EventPreset]，那条日志就再也解释不清自己想要什么。
//   - **表外的名字被放行**。它的症状出现在很远的地方——下一条新会话在 [Service.PinInitial]
//     里才报错，而那时已经没有线索指回那段设置了。
package permissionpresets

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/feature/interaction/userapproval"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// ---- 上线的那些字符串 ----

// TestOnTheWireNamesAreTheDSHNamesVerbatim 钉住那几个进介质的名字一个字符都没改。
//
// 源: packages/interaction/permission-presets/src/index.ts:45、73、76、238、257
//
// 它们分别是：日志里的事件类型、存下来那份文档里的段名、检查点行认的投影键、
// 用户敲的命令名，以及那个派生值本身。改掉其中任何一个，已经写下去的历史就换了读法。
func TestOnTheWireNamesAreTheDSHNamesVerbatim(t *testing.T) {
	t.Parallel()

	if string(EventPreset) != "permission/preset" {
		t.Errorf("事件类型变了：%q", EventPreset)
	}
	if string(SettingsNamespace) != "permission" {
		t.Errorf("设置命名空间变了：%q", SettingsNamespace)
	}
	if ProjectionKey != "permissions" {
		t.Errorf("投影键变了：%q", ProjectionKey)
	}
	if CommandName != "permission" {
		t.Errorf("命令名变了：%q", CommandName)
	}
	if CustomPreset != "custom" {
		t.Errorf("派生值变了：%q", CustomPreset)
	}
	if PackageName != "@deepseek-ai/dsh-permission-presets" {
		t.Errorf("包名变了：%q", PackageName)
	}
}

// ---- 配置 ----

// TestABadTableIsRejectedAtConstruction 把 [New] 拒绝的每一种配置各走一遍。
//
// 源: packages/interaction/permission-presets/src/index.ts:188-213
//
// 每一条都要在**这里**被拒：一张造得起来的坏表会一路活到某条会话真的去切预设，
// 那时报出来的错和它的成因隔着好几层。
func TestABadTableIsRejectedAtConstruction(t *testing.T) {
	t.Parallel()
	harness := newApproval(t, userapproval.PolicyAsk)

	cases := []struct {
		name   string
		config Config
		want   string
	}{
		{
			name:   "没有审批接缝",
			config: Config{Presets: testPresets()},
			want:   "审批接缝",
		},
		{
			name:   "空表",
			config: Config{Approval: harness.service},
			want:   "预设表不能是空的",
		},
		{
			name: "空名字",
			config: Config{Approval: harness.service, Presets: []Preset{
				{PresetSpec: PresetSpec{Approval: userapproval.PolicyAsk}},
			}},
			want: "预设名不能是空的",
		},
		{
			name: "占用了那个派生值",
			config: Config{Approval: harness.service, Presets: []Preset{
				{Name: CustomPreset, PresetSpec: PresetSpec{Approval: userapproval.PolicyAsk}},
			}},
			want: "不能拿来当表项名",
		},
		{
			name: "捆了一个词汇表外的策略",
			config: Config{Approval: harness.service, Presets: []Preset{
				{Name: "weird", PresetSpec: PresetSpec{Approval: "maybe"}},
			}},
			want: "不认识的审批策略",
		},
		{
			name: "名字重了",
			config: Config{Approval: harness.service, Presets: []Preset{
				{Name: "a", PresetSpec: PresetSpec{Approval: userapproval.PolicyAsk}},
				{Name: "a", PresetSpec: PresetSpec{Approval: userapproval.PolicyNever}},
			}},
			want: "出现了两次",
		},
		{
			name: "默认预设不在表里",
			config: Config{
				Approval: harness.service, Presets: testPresets(), DefaultPreset: "nope",
			},
			want: "不在表里",
		},
		{
			name: "推不出默认预设",
			config: Config{Approval: harness.service, Presets: []Preset{
				// 装配的审批默认值是 ask，这张表一条都不捆 ask。
				{Name: "trusted", PresetSpec: PresetSpec{Approval: userapproval.PolicyNever}},
			}},
			want: "请显式给一个 DefaultPreset",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := New(testCase.config)
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("该报配置不成立，拿到 %v", err)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("该说清是哪一条不成立（找 %q），拿到 %q", testCase.want, err)
			}
		})
	}
}

// TestAnOmittedDefaultFallsBackToTheKnobMatch 验留空的 DefaultPreset 走的是「配得上
// 装配默认旋钮值」那一条。
//
// 源: packages/interaction/permission-presets/src/index.ts:214-215
func TestAnOmittedDefaultFallsBackToTheKnobMatch(t *testing.T) {
	t.Parallel()

	// 装配默认是 ask，表里第一条捆 ask 的是 guarded。
	asking := newService(t, newApproval(t, userapproval.PolicyAsk), nil)
	if got := asking.DefaultPreset(); got != "guarded" {
		t.Fatalf("该推出 guarded，拿到 %q", got)
	}

	// 换成 never，同一张表推出的是 trusted。
	trusting := newService(t, newApproval(t, userapproval.PolicyNever), nil)
	if got := trusting.DefaultPreset(); got != "trusted" {
		t.Fatalf("该推出 trusted，拿到 %q", got)
	}
}

// TestNamesAreCopiedPerCall 验每个调用方拿到的是自己那一份表。
//
// 一个调用方排个序或者改一格，就会把别人看到的表也改了——而这两件事在界面代码里
// 都很常见。
func TestNamesAreCopiedPerCall(t *testing.T) {
	t.Parallel()
	service := newService(t, newApproval(t, userapproval.PolicyAsk), nil)

	names := service.Names()
	names[0] = "改过了"
	if service.Names()[0] != "guarded" {
		t.Fatal("改一份拿走的名字不该动到表本身")
	}
}

// ---- 反推 ----

// TestTheLastSelectionWinsWhenTwoPresetsShareKnobs 验用户那次选择在打平手时保得住。
//
// 源: packages/interaction/permission-presets/src/index.ts:312-325
//
// guarded 和 guarded-alias 捆着同一份旋钮值。只看旋钮的话，选了后者会被显示成前者。
func TestTheLastSelectionWinsWhenTwoPresetsShareKnobs(t *testing.T) {
	t.Parallel()
	service := newService(t, newApproval(t, userapproval.PolicyAsk), nil)

	log := &memoryLog{events: []sessionlog.Event{
		preset(0, "guarded-alias"),
		policy(1, userapproval.PolicyAsk),
	}}
	if got := service.Current(log); got != "guarded-alias" {
		t.Fatalf("该保住用户选的那个名字，拿到 %q", got)
	}

	// 旋钮后来被单独拨走了，那次选择就不再算数——它已经名不副实。
	log.events = append(log.events, policy(2, userapproval.PolicyNever))
	if got := service.Current(log); got != "trusted" {
		t.Fatalf("旋钮变了之后该改认 trusted，拿到 %q", got)
	}
}

// TestKnobsOutsideEveryPresetDeriveCustom 验配不上任何一条时交出那个派生值。
//
// 源: packages/interaction/permission-presets/src/index.ts:324
func TestKnobsOutsideEveryPresetDeriveCustom(t *testing.T) {
	t.Parallel()
	harness := newApproval(t, userapproval.PolicyAsk)
	service := newService(t, harness, func(config *Config) {
		config.Presets = []Preset{
			{Name: "trusted", PresetSpec: PresetSpec{Approval: userapproval.PolicyNever}},
		}
		config.DefaultPreset = "trusted"
	})

	// 这条会话把策略拨回了 ask，而这张表一条都不捆 ask。
	log := &memoryLog{events: []sessionlog.Event{policy(0, userapproval.PolicyAsk)}}
	if got := service.Current(log); got != CustomPreset {
		t.Fatalf("该交出那个派生值，拿到 %q", got)
	}
}

// TestAnUnreadablePresetEventKeepsThePreviousState 验一条坏负载不改动折出来的状态。
//
// 源: packages/interaction/permission-presets/src/index.ts:118-134
//
// 这个函数在读路径上，它的职责是「当前旋钮是什么」，不是「这条日志合不合法」。
func TestAnUnreadablePresetEventKeepsThePreviousState(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{`not json`, `{}`, `{"preset":""}`, `{"preset":42}`} {
		events := []sessionlog.Event{preset(0, "trusted"), presetEvent(1, bad)}
		if got := FoldKnobs(events).Preset; got != "trusted" {
			t.Fatalf("负载 %s 该被跳过、状态保持 trusted，拿到 %q", bad, got)
		}
	}
}

// TestASeedBoundaryIsFoldedOnce 验那道种子边界只算一次变更。
//
// 源: packages/interaction/permission-presets/src/index.ts:97、130-133
//
// 第二个返回值是投影注册表那道变更闸要的答案：一条不改动状态的事件不该让它
// 重算一次视图。
func TestASeedBoundaryIsFoldedOnce(t *testing.T) {
	t.Parallel()

	first, changed := applyKnob(KnobState{}, sessionlog.Event{Type: sessionlog.EventSessionEndSeed})
	if !changed || !first.Seeded {
		t.Fatalf("头一道种子边界该改动状态，拿到 %+v changed=%v", first, changed)
	}
	if _, again := applyKnob(first, sessionlog.Event{Type: sessionlog.EventSessionEndSeed}); again {
		t.Fatal("第二道种子边界不该再报改动")
	}
}

// ---- 界面读到的那一份 ----

// TestTheSelectListsTheTableAndOnlyAppendsCustomWhenItIsCurrent 验那份选项单的形状。
//
// 源: packages/interaction/permission-presets/src/index.ts:327-342
func TestTheSelectListsTheTableAndOnlyAppendsCustomWhenItIsCurrent(t *testing.T) {
	t.Parallel()
	service := newService(t, newApproval(t, userapproval.PolicyAsk), nil)

	matched := service.SelectFor(KnobState{Approval: userapproval.PolicyNever})
	if len(matched.Options) != 3 {
		t.Fatalf("配得上时不该追加那个派生值，拿到 %+v", matched.Options)
	}
	if matched.CurrentValue != "trusted" {
		t.Fatalf("当前值该是 trusted，拿到 %q", matched.CurrentValue)
	}
	// 没配显示名的那一条回落到表项名本身。
	if matched.Options[1].Name != "guarded-alias" {
		t.Fatalf("没配显示名该回落到表项名，拿到 %q", matched.Options[1].Name)
	}
	if matched.Options[0].Name != "Guarded" {
		t.Fatalf("配了显示名该用它，拿到 %q", matched.Options[0].Name)
	}

	// 换一张一条都配不上的表，那个派生值才追加在末尾。
	custom := newService(t, newApproval(t, userapproval.PolicyNever), func(config *Config) {
		config.Presets = []Preset{
			{Name: "trusted", PresetSpec: PresetSpec{Approval: userapproval.PolicyNever}},
		}
	}).SelectFor(KnobState{Approval: userapproval.PolicyAsk})
	if custom.CurrentValue != CustomPreset {
		t.Fatalf("当前值该是那个派生值，拿到 %q", custom.CurrentValue)
	}
	if len(custom.Options) != 2 || custom.Options[1].Value != CustomPreset {
		t.Fatalf("那个派生值该追加在末尾，拿到 %+v", custom.Options)
	}
}

// TestOptionOfResolvesBothTableEntriesAndTheDerivedValue 验单条选项的两种来源。
//
// 源: packages/interaction/permission-presets/src/index.ts:358-371
func TestOptionOfResolvesBothTableEntriesAndTheDerivedValue(t *testing.T) {
	t.Parallel()
	service := newService(t, newApproval(t, userapproval.PolicyAsk), nil)

	entry, err := service.OptionOf("guarded")
	if err != nil {
		t.Fatalf("查表项失败：%v", err)
	}
	if entry.Name != "Guarded" || entry.Description != "Ask before acting." {
		t.Fatalf("表项该带上它配的呈现，拿到 %+v", entry)
	}

	derived, err := service.OptionOf(CustomPreset)
	if err != nil {
		t.Fatalf("查那个派生值失败：%v", err)
	}
	if derived.Name != "Custom" {
		t.Fatalf("那个派生值该有自己的显示名，拿到 %+v", derived)
	}

	if _, err := service.OptionOf("nope"); !errors.Is(err, ErrUnknownPreset) {
		t.Fatalf("表外的名字该报不认识，拿到 %v", err)
	}
}

// ---- 两条写路径 ----

// TestSetWritesTheNameThenTheChangedKnob 验那条不通知模型的写路径。
//
// 源: packages/interaction/permission-presets/src/index.ts:383-396
func TestSetWritesTheNameThenTheChangedKnob(t *testing.T) {
	t.Parallel()
	harness := newApproval(t, userapproval.PolicyAsk)
	service := newService(t, harness, nil)

	if err := service.Set(harness.log, "trusted"); err != nil {
		t.Fatalf("切预设失败：%v", err)
	}
	want := []sessionlog.EventType{EventPreset, userapproval.EventPolicy}
	if got := harness.log.types(); !sameTypes(got, want) {
		t.Fatalf("该先记名字再写旋钮，拿到 %v", got)
	}
	if len(harness.notifications()) != 0 {
		t.Fatal("这条路不该惊动模型")
	}
}

// TestReselectingTheCurrentPresetWritesNothing 验重复选择是空操作。
//
// 源: packages/interaction/permission-presets/src/index.ts:388-394
//
// 一次「从 guarded 切到 guarded」写下去的是一条没有意义的翻转，还会白占模型上下文。
func TestReselectingTheCurrentPresetWritesNothing(t *testing.T) {
	t.Parallel()
	harness := newApproval(t, userapproval.PolicyAsk)
	service := newService(t, harness, nil)

	// 装配默认就是 guarded，这条日志一个字节都还没写。
	if err := service.Set(harness.log, "guarded"); err != nil {
		t.Fatalf("切预设失败：%v", err)
	}
	if got := harness.log.types(); len(got) != 0 {
		t.Fatalf("重复选择该什么都不写，拿到 %v", got)
	}
}

// TestSwitchingBetweenPresetsThatShareKnobsRecordsOnlyTheName 验捆包打平手那一次
// 只写名字、不动旋钮。
//
// 源: packages/interaction/permission-presets/src/index.ts:388-395
func TestSwitchingBetweenPresetsThatShareKnobsRecordsOnlyTheName(t *testing.T) {
	t.Parallel()
	harness := newApproval(t, userapproval.PolicyAsk)
	service := newService(t, harness, nil)

	if err := service.Set(harness.log, "guarded-alias"); err != nil {
		t.Fatalf("切预设失败：%v", err)
	}
	if got := harness.log.types(); !sameTypes(got, []sessionlog.EventType{EventPreset}) {
		t.Fatalf("旋钮没变时只该记名字，拿到 %v", got)
	}
}

// TestSwitchForNotifiesTheModel 验那条会通知模型的写路径。
//
// 源: packages/interaction/permission-presets/src/index.ts:271
func TestSwitchForNotifiesTheModel(t *testing.T) {
	t.Parallel()
	harness := newApproval(t, userapproval.PolicyAsk)
	service := newService(t, harness, nil)

	if err := service.SwitchFor(harness.key, "trusted"); err != nil {
		t.Fatalf("切预设失败：%v", err)
	}
	if got := len(harness.notifications()); got != 1 {
		t.Fatalf("该把这次切换排进下一次模型步，排了 %d 条", got)
	}
}

// TestSetForDoesNotNotifyTheModel 验那条不惊动模型的写路径。
//
// 源: packages/webhook/webhook/src/session.ts:153
//
// 一条刚建出来、还没跑过任何一步的会话用它：它没有「上一档」可言，一条通知模型的
// 切换只会凭空多出一句谁都没做过的事。
func TestSetForDoesNotNotifyTheModel(t *testing.T) {
	t.Parallel()
	harness := newApproval(t, userapproval.PolicyAsk)
	service := newService(t, harness, nil)

	if err := service.SetFor(harness.key, "trusted"); err != nil {
		t.Fatalf("钉档位失败：%v", err)
	}
	if got := service.Current(harness.log); got != "trusted" {
		t.Fatalf("该钉成 trusted，拿到 %q", got)
	}
	if got := len(harness.notifications()); got != 0 {
		t.Fatalf("这条路不该惊动模型，排了 %d 条", got)
	}
}

// TestSetForNeedsAWayToFindTheLog 验没接 LogOf 时那条路当场报错。
func TestSetForNeedsAWayToFindTheLog(t *testing.T) {
	t.Parallel()
	harness := newApproval(t, userapproval.PolicyAsk)
	service := newService(t, harness, func(config *Config) { config.LogOf = nil })

	if err := service.SetFor(harness.key, "trusted"); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("该报配置不成立，拿到 %v", err)
	}
}

// TestSwitchingToAnUnknownPresetWritesNothing 验表外的名字在日志变化之前就被拒。
//
// 源: packages/interaction/permission-presets/src/index.ts:384-385
func TestSwitchingToAnUnknownPresetWritesNothing(t *testing.T) {
	t.Parallel()
	harness := newApproval(t, userapproval.PolicyAsk)
	service := newService(t, harness, nil)

	// 那个派生值也在内：它不是一个能切过去的目标。
	for _, name := range []string{"nope", CustomPreset} {
		if err := service.Set(harness.log, name); !errors.Is(err, ErrUnknownPreset) {
			t.Fatalf("切到 %q 该报不认识，拿到 %v", name, err)
		}
	}
	if got := harness.log.types(); len(got) != 0 {
		t.Fatalf("被拒的切换不该留下任何字节，拿到 %v", got)
	}
}

// TestSwitchForNeedsAWayToFindTheLog 验没接 LogOf 时那条路当场报错。
func TestSwitchForNeedsAWayToFindTheLog(t *testing.T) {
	t.Parallel()
	harness := newApproval(t, userapproval.PolicyAsk)
	service := newService(t, harness, func(config *Config) { config.LogOf = nil })

	if err := service.SwitchFor(harness.key, "trusted"); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("该报配置不成立，拿到 %v", err)
	}
}

// TestAnAgentWithoutALogIsAConfigError 验 (nil, nil) 那种答复被当场说清楚。
//
// 一路往下走就是解引用 panic，而那时已经看不出是装配没接对了。
func TestAnAgentWithoutALogIsAConfigError(t *testing.T) {
	t.Parallel()
	harness := newApproval(t, userapproval.PolicyAsk)
	service := newService(t, harness, func(config *Config) {
		config.LogOf = func(*scope.Key) (Log, error) { return nil, nil }
	})

	if err := service.SwitchFor(harness.key, "trusted"); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("该报配置不成立，拿到 %v", err)
	}
}

// ---- 钉初值 ----

// TestPinInitialSeedsAFreshSession 验一条真正全新的会话拿到的是当下那份用户默认值。
//
// 源: packages/interaction/permission-presets/src/index.ts:398-415
func TestPinInitialSeedsAFreshSession(t *testing.T) {
	t.Parallel()
	harness := newApproval(t, userapproval.PolicyAsk)
	service := newService(t, harness, func(config *Config) { config.DefaultPreset = "trusted" })

	log := &memoryLog{}
	if err := service.PinInitial(log); err != nil {
		t.Fatalf("钉初值失败：%v", err)
	}
	want := []sessionlog.EventType{EventPreset, userapproval.EventPolicy}
	if got := log.types(); !sameTypes(got, want) {
		t.Fatalf("该把名字和旋钮都钉下去，拿到 %v", got)
	}
	if got := FoldKnobs(log.Events()); got.Preset != "trusted" || got.Approval != userapproval.PolicyNever {
		t.Fatalf("钉下去的该是那份默认选择，拿到 %+v", got)
	}
}

// TestPinInitialKeepsAnAlreadyWrittenSessionsKnobs 验一条写过一半的会话只补缺的那几条。
//
// 源: packages/interaction/permission-presets/src/index.ts:416-428
//
// 这条会话自己已经把策略拨到 never 了。把它按「默认是 guarded」重钉一遍，等于在
// 用户背后把权限收回去。
func TestPinInitialKeepsAnAlreadyWrittenSessionsKnobs(t *testing.T) {
	t.Parallel()
	harness := newApproval(t, userapproval.PolicyAsk)
	service := newService(t, harness, nil)

	log := &memoryLog{events: []sessionlog.Event{policy(0, userapproval.PolicyNever)}}
	if err := service.PinInitial(log); err != nil {
		t.Fatalf("钉初值失败：%v", err)
	}
	state := FoldKnobs(log.Events())
	if state.Approval != userapproval.PolicyNever {
		t.Fatalf("该保住这条会话自己的旋钮，拿到 %q", state.Approval)
	}
	// 缺的是那个名字，补上的该是旋钮配得上的那一条。
	if state.Preset != "trusted" {
		t.Fatalf("该补上配得上的那个名字，拿到 %q", state.Preset)
	}
}

// TestPinInitialFillsTheMissingKnobOnASeededSession 验一条种过子的会话补的是旋钮。
//
// 源: packages/interaction/permission-presets/src/index.ts:416-428
func TestPinInitialFillsTheMissingKnobOnASeededSession(t *testing.T) {
	t.Parallel()
	harness := newApproval(t, userapproval.PolicyAsk)
	service := newService(t, harness, nil)

	log := &memoryLog{events: []sessionlog.Event{{Seq: 0, Type: sessionlog.EventSessionEndSeed}}}
	if err := service.PinInitial(log); err != nil {
		t.Fatalf("钉初值失败：%v", err)
	}
	state := FoldKnobs(log.Events())
	if state.Approval != userapproval.PolicyAsk {
		t.Fatalf("该补上部署默认旋钮，拿到 %q", state.Approval)
	}
	if state.Preset != "guarded" {
		t.Fatalf("该补上配得上的那个名字，拿到 %q", state.Preset)
	}
}

// TestPinInitialWritesNoNameWhenNothingMatches 验配不上时不硬塞一个名字。
//
// 源: packages/interaction/permission-presets/src/index.ts:421-424
//
// [CustomPreset] 永远不会被写进 [EventPreset]。
func TestPinInitialWritesNoNameWhenNothingMatches(t *testing.T) {
	t.Parallel()
	harness := newApproval(t, userapproval.PolicyAsk)
	service := newService(t, harness, func(config *Config) {
		config.Presets = []Preset{
			{Name: "trusted", PresetSpec: PresetSpec{Approval: userapproval.PolicyNever}},
		}
		config.DefaultPreset = "trusted"
	})

	log := &memoryLog{events: []sessionlog.Event{policy(0, userapproval.PolicyAsk)}}
	if err := service.PinInitial(log); err != nil {
		t.Fatalf("钉初值失败：%v", err)
	}
	if got := log.types(); len(got) != 1 {
		t.Fatalf("配不上时不该写任何东西，拿到 %v", got)
	}
}

// ---- 设置 ----

// TestTheUserSectionOverridesTheComposedDefault 验用户段盖住装配那一份。
//
// 源: packages/interaction/permission-presets/src/index.ts:286-293
func TestTheUserSectionOverridesTheComposedDefault(t *testing.T) {
	t.Parallel()
	harness := newApproval(t, userapproval.PolicyAsk)
	provider := newSettings(t, map[string]any{"defaultPreset": "trusted"})
	service := newService(t, harness, func(config *Config) { config.Settings = provider })

	if got := service.DefaultPreset(); got != "trusted" {
		t.Fatalf("用户段该盖住装配那一份，拿到 %q", got)
	}
}

// TestATableOutsideNameIsRejectedAtTheSettingsBoundary 验一份指向表外名字的设置写不进去。
//
// 源: packages/interaction/permission-presets/src/index.ts:206-213
//
// 放过它，症状会出现在很远的地方：下一条新会话在 [Service.PinInitial] 里才报错。
func TestATableOutsideNameIsRejectedAtTheSettingsBoundary(t *testing.T) {
	t.Parallel()
	harness := newApproval(t, userapproval.PolicyAsk)
	provider := newSettings(t, nil)
	service := newService(t, harness, func(config *Config) { config.Settings = provider })

	err := provider.Update(context.Background(), SettingsNamespace,
		map[string]any{"defaultPreset": "nope"}, nil)
	if err == nil {
		t.Fatal("表外的名字该在这里就被拒")
	}
	if got := service.DefaultPreset(); got != "guarded" {
		t.Fatalf("被拒之后该还是装配那一份，拿到 %q", got)
	}

	if err := provider.Update(context.Background(), SettingsNamespace,
		map[string]any{"defaultPreset": ""}, nil); err == nil {
		t.Fatal("空的默认选择也该被拒")
	}
}

// TestUndoReturnsToTheComposedDefault 验撤销之后读到的是装配那一份。
//
// 源: packages/interaction/permission-presets/src/index.ts:188-276
func TestUndoReturnsToTheComposedDefault(t *testing.T) {
	t.Parallel()
	harness := newApproval(t, userapproval.PolicyAsk)
	provider := newSettings(t, map[string]any{"defaultPreset": "trusted"})

	service, undo, err := New(Config{
		Presets:  testPresets(),
		Approval: harness.service,
		Settings: provider,
	})
	if err != nil {
		t.Fatalf("造服务失败：%v", err)
	}
	if got := service.DefaultPreset(); got != "trusted" {
		t.Fatalf("撤销之前该读到用户那一份，拿到 %q", got)
	}

	undo()
	if got := service.DefaultPreset(); got != "guarded" {
		t.Fatalf("撤销之后该退回装配那一份，拿到 %q", got)
	}
	// 撤销是幂等的。
	undo()
}

// sameTypes 比两串事件类型。
func sameTypes(got, want []sessionlog.EventType) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
