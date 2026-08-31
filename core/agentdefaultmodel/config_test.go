// 本文件验这个包的全部行为：装配那一份怎么被用户段盖住、盖不住的时候退回哪里、
// 一份存坏了的选择在哪一步被拦下来，以及撤销之后读到的是什么。
//
// 源: packages/core/agent-default-model/src/index.ts:1-107

package agentdefaultmodel

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"ds-harness-go/core/agent"
	"ds-harness-go/settings"
)

// memoryBackend 是一份可写的、只活在内存里的设置后端。
type memoryBackend struct {
	mutex    sync.Mutex
	document map[string]any
}

func (b *memoryBackend) Writable() bool { return true }

func (b *memoryBackend) Load(context.Context) (map[string]any, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	// 脱钩地交出去：[settings.Provider] 会把这份文档当成自己那一份存着。
	copied := make(map[string]any, len(b.document))
	for key, value := range b.document {
		copied[key] = value
	}
	return copied, nil
}

func (b *memoryBackend) Persist(_ context.Context, ns settings.Namespace, section map[string]any) error {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if b.document == nil {
		b.document = map[string]any{}
	}
	b.document[string(ns)] = section
	return nil
}

// newSettings 造一个用内存后端的设置服务，stored 是开机时就躺在文档里的那一段。
func newSettings(t *testing.T, stored map[string]any) *settings.Provider {
	t.Helper()
	backend := &memoryBackend{}
	if stored != nil {
		backend.document = map[string]any{string(SettingsNamespace): stored}
	}
	provider, err := settings.New(context.Background(), backend,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("造设置服务失败：%v", err)
	}
	return provider
}

// newService 造一个装配成 provider/model 的服务，挂上（可能为 nil 的）设置服务。
func newService(t *testing.T, provider *settings.Provider) *Service {
	t.Helper()
	service, undo, err := New(Config{Provider: "甲", Model: "m-1", Settings: provider})
	if err != nil {
		t.Fatalf("造服务失败：%v", err)
	}
	t.Cleanup(undo)
	return service
}

// requireSelection 断言当下读到的那一份就是想要的那一份。
func requireSelection(t *testing.T, service *Service, want agent.ModelSelection) {
	t.Helper()
	if got := service.CurrentSelection(); got != want {
		t.Errorf("读到的选择不对：想要 %+v，实际 %+v", want, got)
	}
}

// ---- 名字 ----

// TestPackageNameIsTheDSHNameVerbatim 钉住这个包在注册表里占的名字一个字符都没改。
//
// 源: packages/core/agent-default-model/src/invariant.ts:14
//
// 注册表按名字预留名额，而这条约定的拥有者在 Go 和 DSH 两边是同一个模块。换个名字，
// 两边的诊断记录就对不上了——一条记在这个包名下的东西，在另一边查无此包。
func TestPackageNameIsTheDSHNameVerbatim(t *testing.T) {
	t.Parallel()

	if PackageName != "@deepseek-ai/dsh-agent-default-model" {
		t.Errorf("包名变了：%q", PackageName)
	}
}

// TestSettingsNamespaceIsTheDSHNamespaceVerbatim 钉住命名空间一个字符都没改。
//
// 源: packages/core/agent-default-model/src/index.ts:21
//
// 它是**存下来那份文档里的键**。改掉它，所有已经存在的用户设置会在一次升级之后
// 一声不响地失效——文档里那一段还在，只是再也没人来读了。
func TestSettingsNamespaceIsTheDSHNamespaceVerbatim(t *testing.T) {
	t.Parallel()

	if string(SettingsNamespace) != "agent-default-model" {
		t.Errorf("命名空间变了：%q", string(SettingsNamespace))
	}
}

// ---- 装配入口 ----

// TestNewRefusesACompositionEntryThatNamesNoModel 钉住装配时就缺了提供方或者模型
// 的话当场失败。
//
// 源: packages/core/agent-default-model/src/index.ts:65-68
//
// DSH 那两项是 schema 上的 `.required()`，装配阶段就挡住。放过一个空的提供方，
// 症状会出现在很远的地方：某个 agent 起来之后第一次发请求时才在路由那一层报
// 「查无此提供方」，而那时已经没有任何线索指回这一段装配。
func TestNewRefusesACompositionEntryThatNamesNoModel(t *testing.T) {
	t.Parallel()

	if _, _, err := New(Config{Model: "m-1"}); err == nil {
		t.Error("没有提供方该被拒")
	}
	if _, _, err := New(Config{Provider: "甲"}); err == nil {
		t.Error("没有模型该被拒")
	}
}

// TestADeploymentWithoutASettingsProviderReadsTheCompositionEntry 钉住没挂设置服务
// 的部署照样能用，读到的就是装配那一份。
//
// 源: packages/core/agent-default-model/src/index.ts:60-62
//
// DSH 那条接线是 `ctx.inject(['settings'], ...)`——没挂就整段不跑。这个包刻意不
// 依赖任何宿主，一个只嵌了循环、连设置存储都没有的部署必须照样选得出模型。
func TestADeploymentWithoutASettingsProviderReadsTheCompositionEntry(t *testing.T) {
	t.Parallel()

	service := newService(t, nil)
	requireSelection(t, service, agent.ModelSelection{Provider: "甲", Model: "m-1"})
}

// TestSavingWithoutASettingsProviderKeepsTheCompositionEntry 钉住没挂设置服务时
// 存一次**不报错**，而且装配那一份原封不动。
//
// 源: packages/core/agent-default-model/src/index.ts:99
//
// DSH 那一句是 `ctx.get('settings')?.replace(...)`：存不下来在这条路上不是故障，
// 而是这次部署本来就没有可写的存储。报错的话，每一个调用存的入口都得先去问一句
// 「这个部署有没有设置服务」，而那正是这个包该替它们扛掉的事。
func TestSavingWithoutASettingsProviderKeepsTheCompositionEntry(t *testing.T) {
	t.Parallel()

	service := newService(t, nil)
	if err := service.SaveSelection(context.Background(),
		agent.ModelSelection{Provider: "乙", Model: "m-2"}); err != nil {
		t.Fatalf("没有设置服务时存一次不该报错：%v", err)
	}
	requireSelection(t, service, agent.ModelSelection{Provider: "甲", Model: "m-1"})
}

// ---- 分层 ----

// TestAStoredSelectionOverridesTheCompositionEntry 钉住用户段压在装配那一层之上。
//
// 源: packages/core/agent-default-model/src/index.ts:76-81
//
// 装配那一份进的是**组装层**，用户段叠在它上面。反过来的话，用户在界面上改完模型，
// 下次开机又被部署配置盖回去——而界面上显示的还是他改的那个。
func TestAStoredSelectionOverridesTheCompositionEntry(t *testing.T) {
	t.Parallel()

	service := newService(t, newSettings(t, map[string]any{
		"provider": "乙", "model": "m-2", "reasoningEffort": "high",
	}))
	requireSelection(t, service, agent.ModelSelection{
		Provider: "乙", Model: "m-2", ReasoningEffort: "high",
	})
}

// TestAPartialStoredSectionOnlyOverridesWhatItNames 钉住用户段是**按键**盖上去的，
// 没提到的字段仍然来自装配那一份。
//
// 源: packages/core/agent-default-model/src/index.ts:76
//
// 用户在界面上只换了个模型，没碰提供方。整段替换语义会把提供方抹成空，于是这份
// 选择变成一个路由不出去的东西——而用户什么都没做错。
func TestAPartialStoredSectionOnlyOverridesWhatItNames(t *testing.T) {
	t.Parallel()

	service := newService(t, newSettings(t, map[string]any{"model": "m-2"}))
	requireSelection(t, service, agent.ModelSelection{Provider: "甲", Model: "m-2"})
}

// TestAStoredSectionThatNamesNoProviderFailsRegistration 钉住一份存坏了的选择在
// **登记那一刻**就把服务拦下来。
//
// 源: packages/core/agent-default-model/src/index.ts:35-36
//
// 登记时还没有「上一个好值」可以退（见 [settings.Options].Validate 的说明），
// 所以这里只能失败。装出一个读得出空提供方的服务更坏：它会一直跑，直到某个 agent
// 发第一次请求。
func TestAStoredSectionThatNamesNoProviderFailsRegistration(t *testing.T) {
	t.Parallel()

	_, _, err := New(Config{
		Provider: "甲", Model: "m-1",
		Settings: newSettings(t, map[string]any{"provider": ""}),
	})
	if err == nil {
		t.Fatal("存下来的段把提供方抹空了，登记该失败")
	}
}

// TestASecondServiceOnTheSameSettingsProviderIsRefused 钉住同一个设置服务上装第二个
// 会失败——命名空间是按名字独占的。
//
// 悄悄让第二个装上的话，两个服务会各自以为自己拥有这一段，而只有一个的写入算数。
func TestASecondServiceOnTheSameSettingsProviderIsRefused(t *testing.T) {
	t.Parallel()

	provider := newSettings(t, nil)
	newService(t, provider)

	_, _, err := New(Config{Provider: "乙", Model: "m-2", Settings: provider})
	if !errors.Is(err, settings.ErrAlreadyRegistered) {
		t.Errorf("第二次登记该报「已登记」，实际 %v", err)
	}
}

// ---- 读与写 ----

// TestCurrentSelectionSeesACommittedChangeWithoutAnyRebuild 钉住提交一次改动之后，
// 下一次读就看得见——中间没有任何东西需要重建。
//
// 源: packages/core/agent-default-model/src/index.ts:78-80
//
// DSH 在那个空的 onChange 上写明了这一点：所有消费方都从 currentSelection() 读，
// 所以没有任何登记级的事实需要跟着设置文档重建。这条用例钉的就是 Go 这边也真的
// 每次重读，而不是在构造时抓一份快照存着——抓快照的话，用户改完模型要重启才生效。
func TestCurrentSelectionSeesACommittedChangeWithoutAnyRebuild(t *testing.T) {
	t.Parallel()

	service := newService(t, newSettings(t, nil))
	requireSelection(t, service, agent.ModelSelection{Provider: "甲", Model: "m-1"})

	if err := service.SaveSelection(context.Background(),
		agent.ModelSelection{Provider: "乙", Model: "m-2", ReasoningEffort: "high"}); err != nil {
		t.Fatalf("存一次失败：%v", err)
	}
	requireSelection(t, service, agent.ModelSelection{
		Provider: "乙", Model: "m-2", ReasoningEffort: "high",
	})
}

// TestSaveSelectionReplacesTheWholeSection 钉住存的是**整段替换**，不是打补丁。
//
// 源: packages/core/agent-default-model/src/index.ts:99-103
//
// 交进来的是一份入口已经解析完的完整选择。打补丁的话，上一次存的推理档位会赖在
// 一份没有档位的新选择上——换了模型之后带着一个前一个模型才有的档位，而适配器
// 多半会直接拒收。
func TestSaveSelectionReplacesTheWholeSection(t *testing.T) {
	t.Parallel()

	service := newService(t, newSettings(t, nil))
	ctx := context.Background()
	if err := service.SaveSelection(ctx, agent.ModelSelection{
		Provider: "乙", Model: "m-2", ReasoningEffort: "high",
	}); err != nil {
		t.Fatalf("存第一次失败：%v", err)
	}
	if err := service.SaveSelection(ctx,
		agent.ModelSelection{Provider: "丙", Model: "m-3"}); err != nil {
		t.Fatalf("存第二次失败：%v", err)
	}
	requireSelection(t, service, agent.ModelSelection{Provider: "丙", Model: "m-3"})
}

// TestAnAbsentReasoningEffortIsTheEmptyString 钉住「没选档位」在这一路上从头到尾
// 都是空串，既不是零值指针也不是一个占位档位。
//
// 源: packages/core/agent-default-model/src/index.ts:53-55、102
//
// DSH 用 `undefined` 表达缺席，两处都靠展开运算符把那个键整个略掉。Go 这边
// [agent.ModelSelection] 已经定了「空串即缺失」，所以存的时候要把那个键**略掉**
// 而不是存一个空串：存空串的话，下游分不清「用户明确选了空」和「没选」，
// 而适配器收到一个空档位多半会当成一个非法值拒收。
func TestAnAbsentReasoningEffortIsTheEmptyString(t *testing.T) {
	t.Parallel()

	provider := newSettings(t, nil)
	service := newService(t, provider)
	if err := service.SaveSelection(context.Background(),
		agent.ModelSelection{Provider: "乙", Model: "m-2"}); err != nil {
		t.Fatalf("存一次失败：%v", err)
	}

	stored, registered := provider.Get(SettingsNamespace)
	if !registered {
		t.Fatal("这一段该是登记过的")
	}
	if _, present := stored["reasoningEffort"]; present {
		t.Errorf("没选档位时那个键该整个不在：%#v", stored)
	}
	requireSelection(t, service, agent.ModelSelection{Provider: "乙", Model: "m-2"})
}

// TestSaveSelectionRefusesASelectionThatNamesNoProvider 钉住存一份缺了提供方的选择
// 会**当场**失败，而且上一个好值原样留着。
//
// 源: packages/core/agent-default-model/src/index.ts:35-36
//
// 这条检查装在写入路径上（见 [settings.Options].Validate），所以调用方在 SaveSelection
// 返回时就知道了。存下去再说的话，坏值会在下次开机时把整个服务的登记拖失败——
// 一次远在天边的启动失败，而现场没有任何东西指回这一次写入。
func TestSaveSelectionRefusesASelectionThatNamesNoProvider(t *testing.T) {
	t.Parallel()

	service := newService(t, newSettings(t, nil))
	err := service.SaveSelection(context.Background(), agent.ModelSelection{Model: "m-2"})
	if err == nil {
		t.Fatal("缺了提供方的选择该被拒")
	}
	requireSelection(t, service, agent.ModelSelection{Provider: "甲", Model: "m-1"})
}

// ---- 撤销 ----

// TestUndoFallsBackToTheCompositionEntry 钉住撤销之后读到的是装配那一份，而不是
// 冻在最后读到的那个用户值上。
//
// 源: packages/settings/settings/src/index.ts:876-886
//
// DSH 在 installSettingsSection 的拆除里写明了同一件事：设置服务撤走之后，消费方
// 要**照装配时的样子**继续工作。冻在最后那个用户值上是更坏的一种：那个值来自一份
// 已经不在场的存储，而现场看起来像是装配写错了。
func TestUndoFallsBackToTheCompositionEntry(t *testing.T) {
	t.Parallel()

	service, undo, err := New(Config{
		Provider: "甲", Model: "m-1", Settings: newSettings(t, nil),
	})
	if err != nil {
		t.Fatalf("造服务失败：%v", err)
	}
	if err := service.SaveSelection(context.Background(),
		agent.ModelSelection{Provider: "乙", Model: "m-2", ReasoningEffort: "high"}); err != nil {
		t.Fatalf("存一次失败：%v", err)
	}
	requireSelection(t, service, agent.ModelSelection{
		Provider: "乙", Model: "m-2", ReasoningEffort: "high",
	})

	undo()
	requireSelection(t, service, agent.ModelSelection{Provider: "甲", Model: "m-1"})
	if err := service.SaveSelection(context.Background(),
		agent.ModelSelection{Provider: "丙", Model: "m-3"}); err != nil {
		t.Fatalf("撤销之后存一次该和没挂设置服务一样安静：%v", err)
	}
	requireSelection(t, service, agent.ModelSelection{Provider: "甲", Model: "m-1"})
}

// TestUndoIsIdempotentAndReleasesTheNamespace 钉住撤销多调几次没有副作用，而且
// 那个命名空间真的腾出来了。
//
// 撤销函数会被 defer、也会被重试路径再走一遍。第二次调用要是又摘一次，它摘掉的
// 就是**继任者**的登记——一个已经拆完的服务把活着的那个弄哑了。
func TestUndoIsIdempotentAndReleasesTheNamespace(t *testing.T) {
	t.Parallel()

	provider := newSettings(t, nil)
	_, undo, err := New(Config{Provider: "甲", Model: "m-1", Settings: provider})
	if err != nil {
		t.Fatalf("造服务失败：%v", err)
	}
	undo()
	undo()

	successor, undoSuccessor, err := New(Config{Provider: "乙", Model: "m-2", Settings: provider})
	if err != nil {
		t.Fatalf("腾出来之后该装得上继任者：%v", err)
	}
	t.Cleanup(undoSuccessor)

	// 再撤一次前任，看它会不会把继任者摘掉。
	undo()
	requireSelection(t, successor, agent.ModelSelection{Provider: "乙", Model: "m-2"})
	if err := successor.SaveSelection(context.Background(),
		agent.ModelSelection{Provider: "丙", Model: "m-3"}); err != nil {
		t.Fatalf("继任者该还写得动：%v", err)
	}
	requireSelection(t, successor, agent.ModelSelection{Provider: "丙", Model: "m-3"})
}

// TestReadingRacesWithUndo 钉住一边读一边撤不会撞车。
//
// 新增: DSH 是单线程 JS，那个 source 函数换来换去不需要同步。Go 里撤登记的那一方和
// 读选择的那一方本来就是两个 goroutine——每一个 agent 入口都会读它，而拆除发生在
// 另一条路上。这条用例在 -race 下跑才有意义。
func TestReadingRacesWithUndo(t *testing.T) {
	t.Parallel()

	service, undo, err := New(Config{
		Provider: "甲", Model: "m-1", Settings: newSettings(t, nil),
	})
	if err != nil {
		t.Fatalf("造服务失败：%v", err)
	}

	var waiting sync.WaitGroup
	waiting.Add(2)
	go func() {
		defer waiting.Done()
		for range 200 {
			// 两条路都必须读得出一份能路由的选择：撤销要么还没发生、要么已经
			// 整个发生了，中间没有一个提供方是空的瞬间。
			if service.CurrentSelection().Provider == "" {
				t.Error("任何时刻都该读得出一个提供方")
				return
			}
		}
	}()
	go func() {
		defer waiting.Done()
		undo()
	}()
	waiting.Wait()
}
