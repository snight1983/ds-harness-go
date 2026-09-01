// 本文件验这条接缝的行为本体：登记、分层解析、三条写入路径、发布、通知、关闭。
//
// 源: packages/settings/settings/tests/settings.spec.ts

package settings

import (
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/invariants"
)

// coreConfig 是贯穿本文件的夹具类型。
//
// 源: packages/settings/settings/tests/settings.spec.ts:26-33
type coreConfig struct {
	Timeout int      `json:"timeout"`
	Label   string   `json:"label"`
	Tags    []string `json:"tags"`
	Nested  struct {
		Depth int `json:"depth"`
	} `json:"nested"`
	APIKey string `json:"apiKey" settings:"secret"`
}

// quietLogger 是一个不往任何地方写的 logger。
//
// 用例里会**故意**制造观察者 panic 和坏段，那些都会走 Warn。
// 让它们打到测试输出上的话，一次正常的绿色跑动看起来像出了七八个问题。
func quietLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// boot 是绝大多数用例的开场：一个装着 document 的后端 + 一个服务。
func boot(t *testing.T, document map[string]any) (*memoryBackend, *Provider) {
	t.Helper()

	backend := newMemoryBackend(t, document)
	provider, err := New(t.Context(), backend, quietLogger())
	if err != nil {
		t.Fatalf("建服务不该失败：%v", err)
	}
	t.Cleanup(provider.Close)
	return backend, provider
}

// register 是 [Register] 在用例里的简写，带默认值和组装层。
func register(t *testing.T, p *Provider, ns Namespace, defaults coreConfig, options *Options[coreConfig]) *Scope[coreConfig] {
	t.Helper()

	scope, dispose, err := Register(p, ns, defaults, options)
	if err != nil {
		t.Fatalf("登记 %q 不该失败：%v", string(ns), err)
	}
	t.Cleanup(dispose)
	return scope
}

// TestNewRequiresABackend 钉住零值服务不可用这件事在构造时就响。
func TestNewRequiresABackend(t *testing.T) {
	t.Parallel()

	if _, err := New(t.Context(), nil, nil); err == nil {
		t.Fatal("没有后端该失败")
	}
}

// TestNewSurfacesALoadFailure 钉住读不出文档就不给服务。
//
// 交出一个「文档还没读进来」的服务的话，第一个登记方拿到的是默认值，
// 然后过一会儿被一次「变更」通知悄悄换掉——一个没人改过的启动过程里凭空多出一次变更。
func TestNewSurfacesALoadFailure(t *testing.T) {
	t.Parallel()

	backend := newMemoryBackend(t, nil)
	backend.loadErr = errBackendOffline

	if _, err := New(t.Context(), backend, nil); !errors.Is(err, errBackendOffline) {
		t.Fatalf("该把后端的失败带出来，实际 %v", err)
	}
}

// TestRegisterResolvesDefaultsThenBaseThenUser 钉住三层的叠放次序。
//
// 源: packages/settings/settings/tests/settings.spec.ts:89-97
//
// 这条是整个包存在的理由：三层各有各的作者，谁都不能替谁做主。
func TestRegisterResolvesDefaultsThenBaseThenUser(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, map[string]any{"core": map[string]any{"timeout": float64(90)}})
	scope := register(t, provider, "core",
		coreConfig{Timeout: 30, Label: "默认"},
		&Options[coreConfig]{Base: map[string]any{"timeout": float64(60), "label": "装配"}},
	)

	value := scope.Get()
	if value.Timeout != 90 {
		t.Errorf("用户段该压过组装层，timeout=%d", value.Timeout)
	}
	if value.Label != "装配" {
		t.Errorf("组装层该压过类型默认值，label=%q", value.Label)
	}
}

// TestRegisterFailsWhenTheStoredSectionIsUnserviceable 钉住登记那一刻的三种拒绝。
//
// 源: packages/settings/settings/tests/settings.spec.ts:123-153
//
// 登记时和登记后的处理是**相反**的，而这不是不一致：登记后有「上一个好值」可以退，
// 登记时没有。此时不失败的话，拥有者会拿到一个它自己都不接受的值当起点。
func TestRegisterFailsWhenTheStoredSectionIsUnserviceable(t *testing.T) {
	t.Parallel()

	t.Run("类型解不开", func(t *testing.T) {
		t.Parallel()

		_, provider := boot(t, map[string]any{"core": map[string]any{"timeout": "不是数"}})
		if _, _, err := Register(provider, "core", coreConfig{}, nil); err == nil {
			t.Fatal("存下来的段解不开该让登记失败")
		}
	})

	t.Run("段不是对象", func(t *testing.T) {
		t.Parallel()

		_, provider := boot(t, map[string]any{"core": "不是对象"})
		if _, _, err := Register(provider, "core", coreConfig{}, nil); !errors.Is(err, ErrMalformedSection) {
			t.Fatalf("该报 ErrMalformedSection，实际 %v", err)
		}
	})

	t.Run("过不了拥有者的检查", func(t *testing.T) {
		t.Parallel()

		_, provider := boot(t, map[string]any{"core": map[string]any{"timeout": float64(-1)}})
		_, _, err := Register(provider, "core", coreConfig{}, &Options[coreConfig]{
			Validate: func(value coreConfig) error {
				if value.Timeout < 0 {
					return errors.New("timeout 不能是负数")
				}
				return nil
			},
		})
		if err == nil {
			t.Fatal("过不了 Validate 该让登记失败")
		}
	})
}

// TestRegisterRejectsADuplicateNamespaceLoud 钉住重复登记必须响。
//
// 源: packages/settings/settings/tests/settings.spec.ts:136-142
//
// 静默接受的话，两个模块把同一段配置当成自己的，谁读到的都不完整，
// 而症状是「我配了但它没生效」——从配置那头永远查不出来。
func TestRegisterRejectsADuplicateNamespaceLoud(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, nil)
	register(t, provider, "core", coreConfig{}, nil)

	if _, _, err := Register(provider, "core", coreConfig{}, nil); !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("该报 ErrAlreadyRegistered，实际 %v", err)
	}
}

// TestRegisterRejectsAnInvalidNamespaceAndANilProvider 钉住入口那一道。
//
// 命名空间只在这里挡一道，本包别处凡是拿它当键用的地方都不假设它合法。
func TestRegisterRejectsAnInvalidNamespaceAndANilProvider(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, nil)
	if _, _, err := Register(provider, "Core", coreConfig{}, nil); !errors.Is(err, ErrInvalidNamespace) {
		t.Fatalf("该报 ErrInvalidNamespace，实际 %v", err)
	}
	if _, _, err := Register[coreConfig](nil, "core", coreConfig{}, nil); err == nil {
		t.Fatal("没有服务该失败")
	}
}

// TestGetReadsNothingForAnUnregisteredNamespace 钉住未登记读得出「没有」。
//
// 源: packages/settings/settings/tests/settings.spec.ts:170-174
func TestGetReadsNothingForAnUnregisteredNamespace(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, nil)
	if _, registered := provider.Get("core"); registered {
		t.Fatal("没登记的命名空间不该读出值")
	}
}

// TestUnregisterRemovesTheNamespaceAndIsIdempotent 钉住注销拆干净、且认对象身份。
//
// 源: packages/settings/settings/tests/settings.spec.ts:184-210
//
// 认身份那一半是防这件事：注销之后又登记了一个新拥有者，
// 此时旧的注销函数再被调一次（重复 defer、重试路径上多走一遍）会把继任者摘掉。
func TestUnregisterRemovesTheNamespaceAndIsIdempotent(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, nil)
	_, dispose, err := Register(provider, "core", coreConfig{Label: "第一个"}, nil)
	if err != nil {
		t.Fatalf("登记不该失败：%v", err)
	}

	dispose()
	if _, registered := provider.Get("core"); registered {
		t.Fatal("注销之后不该还读得出来")
	}

	_, _, err = Register(provider, "core", coreConfig{Label: "继任者"}, nil)
	if err != nil {
		t.Fatalf("注销之后该能用同一个名字重新登记：%v", err)
	}

	dispose() // 旧的注销函数又被调了一次。
	raw, registered := provider.Get("core")
	if !registered {
		t.Fatal("旧的注销函数把继任者摘掉了")
	}
	if raw["label"] != "继任者" {
		t.Fatalf("留下的该是继任者，实际 %#v", raw)
	}
}

// TestUpdatePersistsTheUserSectionWithoutBakingInTheBaseLayer 钉住存下去的只有用户那一层。
//
// 源: packages/settings/settings/tests/settings.spec.ts:212-223
//
// 把组装层烤进用户段的后果是「重置」永远退不回去了：那些值从此长得和用户改的一样，
// 而下一次部署换了组装层也压不动它们。
func TestUpdatePersistsTheUserSectionWithoutBakingInTheBaseLayer(t *testing.T) {
	t.Parallel()

	backend, provider := boot(t, nil)
	scope := register(t, provider, "core", coreConfig{Timeout: 30},
		&Options[coreConfig]{Base: map[string]any{"label": "装配"}})

	if err := scope.Update(t.Context(), map[string]any{"timeout": 90}); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}

	stored := backend.stored("core")
	if !DeepEqualJSON(toAny(stored), map[string]any{"timeout": float64(90)}) {
		t.Fatalf("存下去的该只有用户那一层，实际 %#v", stored)
	}
	if scope.Get().Label != "装配" {
		t.Fatal("组装层该还在解析值里")
	}
}

// TestUpdateDeepMergesObjectsAndReplacesArrays 钉住补丁的合并规则和分层是同一条。
//
// 源: packages/settings/settings/tests/settings.spec.ts:224-236
func TestUpdateDeepMergesObjectsAndReplacesArrays(t *testing.T) {
	t.Parallel()

	backend, provider := boot(t, map[string]any{"core": map[string]any{
		"nested": map[string]any{"depth": float64(1)},
		"tags":   []any{"a", "b", "c"},
		"label":  "留着",
	}})
	scope := register(t, provider, "core", coreConfig{}, nil)

	if err := scope.Update(t.Context(), map[string]any{"tags": []any{"z"}}); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}

	stored := backend.stored("core")
	if !DeepEqualJSON(stored["tags"], []any{"z"}) {
		t.Fatalf("数组该整个替换，实际 %#v", stored["tags"])
	}
	if !DeepEqualJSON(stored["nested"], map[string]any{"depth": float64(1)}) {
		t.Fatalf("没提到的嵌套对象该原样留着，实际 %#v", stored["nested"])
	}
	if stored["label"] != "留着" {
		t.Fatalf("没提到的键该原样留着，实际 %#v", stored["label"])
	}
}

// TestUpdateCommitsNotifiesAndCarriesSourceUpdate 钉住一次成功的写把三件事都做了。
//
// 源: packages/settings/settings/tests/settings.spec.ts:237-255
func TestUpdateCommitsNotifiesAndCarriesSourceUpdate(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, nil)
	scope := register(t, provider, "core", coreConfig{Timeout: 30}, nil)

	var seen []coreConfig
	defer scope.Watch(func(next, prev coreConfig) {
		seen = append(seen, next, prev)
	})()

	var sources []Source
	defer provider.SubscribeUpdated(func(_ Namespace, _, _ map[string]any, source Source) {
		sources = append(sources, source)
	})()

	if err := scope.Update(t.Context(), map[string]any{"timeout": 90}); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}

	if scope.Get().Timeout != 90 {
		t.Fatalf("解析值该换掉了，实际 %d", scope.Get().Timeout)
	}
	if len(seen) != 2 || seen[0].Timeout != 90 || seen[1].Timeout != 30 {
		t.Fatalf("观察者该收到 (新, 旧)，实际 %+v", seen)
	}
	if len(sources) != 1 || sources[0] != SourceUpdate {
		t.Fatalf("来路该是 %q，实际 %v", SourceUpdate, sources)
	}
}

// TestUpdateRejectsAnInvalidPatchBeforePersistingAnything 钉住校验在落盘之前。
//
// 源: packages/settings/settings/tests/settings.spec.ts:256-268
//
// 顺序反过来的话，一个拥有者无法接受的值会先躺进存储里——
// 而它下一次启动时会在登记那一步失败，症状是「服务起不来了」，
// 和刚才那次被拒的写之间没有任何看得见的联系。
func TestUpdateRejectsAnInvalidPatchBeforePersistingAnything(t *testing.T) {
	t.Parallel()

	backend, provider := boot(t, nil)
	scope := register(t, provider, "core", coreConfig{Timeout: 30}, &Options[coreConfig]{
		Validate: func(value coreConfig) error {
			if value.Timeout < 0 {
				return errors.New("timeout 不能是负数")
			}
			return nil
		},
	})

	if err := scope.Update(t.Context(), map[string]any{"timeout": -1}); err == nil {
		t.Fatal("过不了 Validate 的写该失败")
	}
	if len(backend.calls()) != 0 {
		t.Fatalf("什么都不该落盘，实际 %+v", backend.calls())
	}
	if scope.Get().Timeout != 30 {
		t.Fatal("解析值不该动")
	}
}

// TestUpdateRejectsWhatJSONCannotHold 钉住写入的内容必须存得下。
//
// 源: packages/settings/settings/tests/settings.spec.ts:482-488
func TestUpdateRejectsWhatJSONCannotHold(t *testing.T) {
	t.Parallel()

	backend, provider := boot(t, nil)
	scope := register(t, provider, "core", coreConfig{}, nil)

	if err := scope.Update(t.Context(), map[string]any{"fn": func() {}}); !errors.Is(err, ErrNotJSON) {
		t.Fatalf("该报 ErrNotJSON，实际 %v", err)
	}
	if len(backend.calls()) != 0 {
		t.Fatalf("什么都不该落盘，实际 %+v", backend.calls())
	}
}

// TestUpdateSnapshotsThePatchAtCallTime 钉住排队期间调用方再改自己那个 map 也没用。
//
// 源: packages/settings/settings/tests/settings.spec.ts:518-528
func TestUpdateSnapshotsThePatchAtCallTime(t *testing.T) {
	t.Parallel()

	backend, provider := boot(t, nil)
	scope := register(t, provider, "core", coreConfig{}, nil)

	patch := map[string]any{"label": "调用时"}
	if err := scope.Update(t.Context(), patch); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}
	patch["label"] = "写完之后"

	if backend.stored("core")["label"] != "调用时" {
		t.Fatalf("存下去的该是调用那一刻的样子，实际 %#v", backend.stored("core"))
	}
}

// TestWriteRejectsAnUnregisteredNamespaceAndAReadOnlyBackend 钉住两条前置拒绝。
//
// 源: packages/settings/settings/tests/settings.spec.ts:294-307
//
// 只读那一条必须在落盘之前响：写完了不生效的症状是「我改了但它没变」，
// 而用户没有任何理由怀疑是存储不收。
func TestWriteRejectsAnUnregisteredNamespaceAndAReadOnlyBackend(t *testing.T) {
	t.Parallel()

	backend, provider := boot(t, nil)
	scope := register(t, provider, "core", coreConfig{}, nil)

	if err := provider.Update(t.Context(), "other", map[string]any{}, nil); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("该报 ErrNotRegistered，实际 %v", err)
	}

	backend.mutex.Lock()
	backend.writable = false
	backend.mutex.Unlock()

	if err := scope.Update(t.Context(), map[string]any{"timeout": 1}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("该报 ErrReadOnly，实际 %v", err)
	}
	if len(backend.calls()) != 0 {
		t.Fatalf("只读后端上不该有落盘，实际 %+v", backend.calls())
	}
}

// TestWriteSurfacesAPersistFailureWithoutCommitting 钉住落盘失败时内存不许先认。
//
// 认了的话就是「存储里是旧的、内存里是新的」，而这种分叉没有任何一方看得见。
func TestWriteSurfacesAPersistFailureWithoutCommitting(t *testing.T) {
	t.Parallel()

	backend, provider := boot(t, nil)
	scope := register(t, provider, "core", coreConfig{Timeout: 30}, nil)

	backend.mutex.Lock()
	backend.persistErr = errBackendOffline
	backend.mutex.Unlock()

	if err := scope.Update(t.Context(), map[string]any{"timeout": 90}); !errors.Is(err, errBackendOffline) {
		t.Fatalf("该把后端的失败带出来，实际 %v", err)
	}
	if scope.Get().Timeout != 30 {
		t.Fatalf("落盘没成功就不该换值，实际 %d", scope.Get().Timeout)
	}
}

// TestReplaceRemovesOverridesWholesale 钉住整段替换能做到补丁做不到的事。
//
// 源: packages/settings/settings/tests/settings.spec.ts:376-390
//
// Replace(空段) 就是把这个命名空间整个重置回组装层和类型默认值——
// 只会合并的补丁永远表达不了「把这一条覆盖删掉」。
func TestReplaceRemovesOverridesWholesale(t *testing.T) {
	t.Parallel()

	backend, provider := boot(t, map[string]any{"core": map[string]any{
		"timeout": float64(90), "label": "用户",
	}})
	scope := register(t, provider, "core", coreConfig{Timeout: 30, Label: "默认"}, nil)

	if err := scope.Replace(t.Context(), map[string]any{}); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}

	value := scope.Get()
	if value.Timeout != 30 || value.Label != "默认" {
		t.Fatalf("该整个退回类型默认值，实际 %+v", value)
	}
	if len(backend.stored("core")) != 0 {
		t.Fatalf("存下去的该是空段，实际 %#v", backend.stored("core"))
	}
}

// TestMutateRemovesOneFieldWithoutTouchingASecretTheCallerNeverSaw 钉住 Mutate 存在的全部理由。
//
// 源: packages/settings/settings/tests/settings.spec.ts:830-844
//
// 配置界面手上是脱敏视图，里面按定义没有密钥。它拿那份视图去 Replace 的话，
// 会把自己从来没收到过的密钥统统删掉，而全程不报任何错。
func TestMutateRemovesOneFieldWithoutTouchingASecretTheCallerNeverSaw(t *testing.T) {
	t.Parallel()

	backend, provider := boot(t, map[string]any{"core": map[string]any{
		"apiKey": "sk-live", "label": "删我",
	}})
	register(t, provider, "core", coreConfig{}, nil)

	err := provider.Mutate(t.Context(), "core", []PathOp{{Kind: PathOpUnset, Path: []string{"label"}}}, nil)
	if err != nil {
		t.Fatalf("写不该失败：%v", err)
	}

	stored := backend.stored("core")
	if stored["apiKey"] != "sk-live" {
		t.Fatalf("密钥该原封不动，实际 %#v", stored)
	}
	if _, present := stored["label"]; present {
		t.Fatalf("指名要删的那个该没了，实际 %#v", stored)
	}
}

// TestMutateAppliesOpsInOrderAndRejectsAMalformedOne 钉住顺序和前置校验。
//
// 源: packages/settings/settings/tests/settings.spec.ts:845-855,908-923
func TestMutateAppliesOpsInOrderAndRejectsAMalformedOne(t *testing.T) {
	t.Parallel()

	backend, provider := boot(t, map[string]any{"core": map[string]any{"label": "旧"}})
	register(t, provider, "core", coreConfig{}, nil)

	ops := []PathOp{
		{Kind: PathOpSet, Path: []string{"label"}, Value: "新"},
		{Kind: PathOpUnset, Path: []string{"label"}},
		{Kind: PathOpSet, Path: []string{"label"}, Value: "最后"},
	}
	if err := provider.Mutate(t.Context(), "core", ops, nil); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}
	if backend.stored("core")["label"] != "最后" {
		t.Fatalf("op 该按顺序作用，实际 %#v", backend.stored("core"))
	}

	// 动作不合法的 op 在任何东西排队之前就被拒。
	before := len(backend.calls())
	if err := provider.Mutate(t.Context(), "core", []PathOp{{Kind: "delete"}}, nil); err == nil {
		t.Fatal("动作不合法该失败")
	}
	if len(backend.calls()) != before {
		t.Fatal("被拒的 mutate 不该落盘")
	}

	// 值存不下时同样不落盘。
	err := provider.Mutate(t.Context(), "core",
		[]PathOp{{Kind: PathOpSet, Path: []string{"x"}, Value: func() {}}}, nil)
	if !errors.Is(err, ErrNotJSON) {
		t.Fatalf("该报 ErrNotJSON，实际 %v", err)
	}
	if len(backend.calls()) != before {
		t.Fatal("被拒的 mutate 不该落盘")
	}
}

// TestMutateRefusesANonObjectAtTheSectionRootLeavingTheStoredSectionAlone 钉住段根的形状。
//
// 源: packages/settings/settings/tests/settings.spec.ts:894-900
func TestMutateRefusesANonObjectAtTheSectionRootLeavingTheStoredSectionAlone(t *testing.T) {
	t.Parallel()

	backend, provider := boot(t, map[string]any{"core": map[string]any{"label": "留着"}})
	register(t, provider, "core", coreConfig{}, nil)

	err := provider.Mutate(t.Context(), "core", []PathOp{{Kind: PathOpSet, Value: "不是对象"}}, nil)
	if !errors.Is(err, ErrMalformedSection) {
		t.Fatalf("该报 ErrMalformedSection，实际 %v", err)
	}
	if backend.stored("core")["label"] != "留着" {
		t.Fatalf("存下来的段不该动，实际 %#v", backend.stored("core"))
	}
}

// TestDescribeExposesEveryLayerAndRedactsOnDemand 钉住描述这一面给足了配置界面要的东西。
//
// 源: packages/settings/settings/tests/redact.spec.ts:115-168
//
// 三层分开给，配置界面才标得出「哪些字段是用户改过的」（出现在 User 里的那些）
// 和「重置会退回到哪里」。
func TestDescribeExposesEveryLayerAndRedactsOnDemand(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, map[string]any{"core": map[string]any{
		"timeout": float64(90), "apiKey": "sk-live",
	}})
	register(t, provider, "core", coreConfig{Timeout: 30},
		&Options[coreConfig]{Base: map[string]any{"label": "装配"}, Schema: "透传的 schema"})

	plain := provider.Describe(nil)
	if len(plain) != 1 {
		t.Fatalf("该描述出一个命名空间，实际 %d 个", len(plain))
	}
	if plain[0].Namespace != "core" || plain[0].Schema != "透传的 schema" || plain[0].Applies != AppliesLive {
		t.Fatalf("元信息不对：%+v", plain[0])
	}
	if plain[0].Value["apiKey"] != "sk-live" {
		t.Fatal("不脱敏时密钥该在")
	}
	if !DeepEqualJSON(toAny(plain[0].Base), map[string]any{"label": "装配"}) {
		t.Fatalf("组装层该原样给出，实际 %#v", plain[0].Base)
	}
	if !DeepEqualJSON(toAny(plain[0].User), map[string]any{"timeout": float64(90), "apiKey": "sk-live"}) {
		t.Fatalf("用户段该原样给出，实际 %#v", plain[0].User)
	}

	redacted := provider.Describe(&DescribeOptions{RedactSecrets: true})
	if _, leaked := redacted[0].Value["apiKey"]; leaked {
		t.Fatalf("解析值里的密钥该摘掉，实际 %#v", redacted[0].Value)
	}
	if _, leaked := redacted[0].User["apiKey"]; leaked {
		t.Fatalf("用户段里的密钥也该摘掉，实际 %#v", redacted[0].User)
	}
	if len(redacted[0].Secrets) == 0 {
		t.Fatal("该列出密钥位置")
	}
}

// TestDescribeDetachesTheLayersItHandsOut 钉住交出去的层是副本。
//
// 描述这一面交出去的东西会离开这个包，而服务手上的那几份是共享的活数据；
// 不复制的话，一个配置界面顺手改一下自己拿到的 map，就把服务里的段改了。
func TestDescribeDetachesTheLayersItHandsOut(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, map[string]any{"core": map[string]any{"label": "原始"}})
	scope := register(t, provider, "core", coreConfig{},
		&Options[coreConfig]{Base: map[string]any{"label": "装配"}})

	descriptor := provider.Describe(nil)[0]
	descriptor.User["label"] = "被外面改了"
	descriptor.Base["label"] = "也被改了"

	again := provider.Describe(nil)[0]
	if again.User["label"] != "原始" || again.Base["label"] != "装配" {
		t.Fatalf("服务里的层被外面改动影响了：%+v", again)
	}
	if scope.Get().Label != "原始" {
		t.Fatal("解析值也被影响了")
	}
}

// TestDescribeTreatsAMalformedStoredSectionAsHavingNoUserLayer 钉住描述是全函数。
//
// 源: packages/settings/settings/tests/redact.spec.ts:137-147
//
// 文档被人手工编辑坏了之后，配置界面必须还打得开——不然用户唯一能修它的地方
// 恰好因为它坏了而进不去。
func TestDescribeTreatsAMalformedStoredSectionAsHavingNoUserLayer(t *testing.T) {
	t.Parallel()

	backend, provider := boot(t, nil)
	register(t, provider, "core", coreConfig{Timeout: 30}, nil)

	backend.pushExternal(provider, map[string]any{"core": "不是对象"})

	descriptor := provider.Describe(nil)[0]
	if descriptor.User != nil {
		t.Fatalf("坏段该当成没有用户层，实际 %#v", descriptor.User)
	}
	if descriptor.Value["timeout"] != float64(30) {
		t.Fatalf("该保留上一个好值，实际 %#v", descriptor.Value)
	}
}

// TestPublishNotifiesWithSourceProviderAndKeepsTheLastGoodValuePerNamespace 钉住发布的两条规则。
//
// 源: packages/settings/settings/tests/settings.spec.ts:530-580
//
// 「只影响那一段」是重点：一份被人手工编辑坏了的文档，
// 不该把所有跑着的拥有者一起拖死。
func TestPublishNotifiesWithSourceProviderAndKeepsTheLastGoodValuePerNamespace(t *testing.T) {
	t.Parallel()

	backend, provider := boot(t, nil)
	core := register(t, provider, "core", coreConfig{Timeout: 30}, nil)
	other := register(t, provider, "other", coreConfig{Timeout: 30}, nil)

	var sources []Source
	defer provider.SubscribeUpdated(func(_ Namespace, _, _ map[string]any, source Source) {
		sources = append(sources, source)
	})()

	backend.pushExternal(provider, map[string]any{
		"core":  map[string]any{"timeout": "不是数"},
		"other": map[string]any{"timeout": float64(90)},
	})

	if core.Get().Timeout != 30 {
		t.Fatalf("坏段该保留上一个好值，实际 %d", core.Get().Timeout)
	}
	if other.Get().Timeout != 90 {
		t.Fatalf("好段该照常提交，实际 %d", other.Get().Timeout)
	}
	if len(sources) != 1 || sources[0] != SourceProvider {
		t.Fatalf("来路该是 %q，实际 %v", SourceProvider, sources)
	}

	// 存储变回合法之后能恢复。
	//
	// 源: packages/settings/settings/tests/settings.spec.ts:571-579
	backend.pushExternal(provider, map[string]any{"core": map[string]any{"timeout": float64(60)}})
	if core.Get().Timeout != 60 {
		t.Fatalf("段变回合法之后该恢复，实际 %d", core.Get().Timeout)
	}
}

// TestPublishStaysSilentWhenTheResolvedValueIsUnchanged 钉住等值就不通知。
//
// 源: packages/settings/settings/tests/settings.spec.ts:546-556
func TestPublishStaysSilentWhenTheResolvedValueIsUnchanged(t *testing.T) {
	t.Parallel()

	backend, provider := boot(t, map[string]any{"core": map[string]any{"timeout": float64(90)}})
	scope := register(t, provider, "core", coreConfig{}, nil)

	calls := 0
	defer scope.Watch(func(coreConfig, coreConfig) { calls++ })()

	backend.pushExternal(provider, map[string]any{"core": map[string]any{"timeout": float64(90)}})
	if calls != 0 {
		t.Fatalf("值没变不该通知，实际调了 %d 次", calls)
	}
}

// TestPublishLeavesUnregisteredSectionsInTheDocument 钉住未登记的段不会被发布吃掉。
//
// 某个还没装上的插件的配置不该因为一次发布就消失——而它消失之后，
// 那个插件下次装上时会拿到默认值，用户看到的是「我的配置自己没了」。
func TestPublishLeavesUnregisteredSectionsInTheDocument(t *testing.T) {
	t.Parallel()

	backend, provider := boot(t, nil)
	register(t, provider, "core", coreConfig{}, nil)

	backend.pushExternal(provider, map[string]any{
		"core":       map[string]any{"timeout": float64(1)},
		"not-loaded": map[string]any{"keep": true},
	})

	// 登记那个还没装上的插件，它该读到自己那一段。
	late := register(t, provider, "not-loaded", coreConfig{Label: "默认"}, nil)
	raw, _ := provider.Get("not-loaded")
	if raw == nil || late.Get().Label != "默认" {
		t.Fatalf("未登记的段该留在文档里，实际 %#v", raw)
	}
	descriptor := findDescriptor(t, provider, "not-loaded")
	if !DeepEqualJSON(toAny(descriptor.User), map[string]any{"keep": true}) {
		t.Fatalf("那一段该原样留着，实际 %#v", descriptor.User)
	}
}

// TestRevisionTracksTheRawSectionNotTheResolvedValue 钉住修订号数的是**存下来的东西**。
//
// 源: packages/settings/settings/tests/settings.spec.ts:967-994
//
// 这一条是有意和解析值等值判断分开的：存一条和组装层完全相同的覆盖，
// 解析值确实没变，但文档的**含义**变了——那个字段从「继承来的」变成了「用户钉死的」，
// 而这正是配置界面必须重读的那种变化。
func TestRevisionTracksTheRawSectionNotTheResolvedValue(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, nil)
	scope := register(t, provider, "core", coreConfig{Timeout: 30},
		&Options[coreConfig]{Base: map[string]any{"timeout": float64(60)}})

	var announced []uint64
	defer provider.SubscribeDocumentUpdated(func(_ Namespace, revision uint64) {
		announced = append(announced, revision)
	})()

	valueChanges := 0
	defer scope.Watch(func(coreConfig, coreConfig) { valueChanges++ })()

	// 存一条和组装层一模一样的覆盖：解析值没变，存下来的东西变了。
	if err := scope.Update(t.Context(), map[string]any{"timeout": 60}); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}
	if valueChanges != 0 {
		t.Fatalf("解析值没变，不该通知观察者，实际 %d 次", valueChanges)
	}
	if len(announced) != 1 || announced[0] != 1 {
		t.Fatalf("修订号该推到 1 并广播，实际 %v", announced)
	}

	// 再写一次一模一样的段：存下来的东西也没变，修订号不动。
	//
	// 源: packages/settings/settings/tests/settings.spec.ts:985-994
	if err := scope.Update(t.Context(), map[string]any{"timeout": 60}); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}
	if len(announced) != 1 {
		t.Fatalf("存下来的东西没变，修订号不该动，实际 %v", announced)
	}
}

// TestRevisionMovesForAnExternalEdit 钉住外部编辑和本进程的写在修订号上一样地动。
//
// 源: packages/settings/settings/tests/settings.spec.ts:995-1020
//
// 不一样的话，配置界面手上的修订号在一次外部编辑之后仍然「有效」，
// 于是它那次写会覆盖掉刚才那个人改的东西，而冲突检测一声不吭。
func TestRevisionMovesForAnExternalEdit(t *testing.T) {
	t.Parallel()

	backend, provider := boot(t, nil)
	register(t, provider, "core", coreConfig{}, nil)

	backend.pushExternal(provider, map[string]any{"core": map[string]any{"timeout": float64(90)}})
	if got := findDescriptor(t, provider, "core").Revision; got != 1 {
		t.Fatalf("外部编辑该把修订号推上去，实际 %d", got)
	}

	// 段变成不是对象也算变了：从一个合法段到「读不出来」，存下来的东西确实变了。
	backend.pushExternal(provider, map[string]any{"core": "不是对象"})
	if got := findDescriptor(t, provider, "core").Revision; got != 2 {
		t.Fatalf("变成坏段也该推修订号，实际 %d", got)
	}
}

// TestExpectedRevisionRefusesAStaleWrite 钉住冲突检测。
//
// 源: packages/settings/settings/tests/settings.spec.ts:937-966
func TestExpectedRevisionRefusesAStaleWrite(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, nil)
	scope := register(t, provider, "core", coreConfig{}, nil)

	stale := findDescriptor(t, provider, "core").Revision
	if err := scope.Update(t.Context(), map[string]any{"label": "赢家"}); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}

	err := provider.Update(t.Context(), "core", map[string]any{"label": "输家"}, &stale)
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("该报 *ConflictError，实际 %v", err)
	}
	if conflict.Expected != stale || conflict.Actual != stale+1 {
		t.Fatalf("两个修订号该带着，实际 %+v", conflict)
	}
	if scope.Get().Label != "赢家" {
		t.Fatalf("赢家该留在原地，实际 %q", scope.Get().Label)
	}

	// 不带期望的写照常通过——修订号 0 是合法值，所以「没提供」只能用 nil 表达。
	//
	// 源: packages/settings/settings/tests/settings.spec.ts:959-966
	if err := scope.Update(t.Context(), map[string]any{"label": "无期望"}); err != nil {
		t.Fatalf("不带期望的写不该失败：%v", err)
	}

	// 对得上的期望也照常通过。
	current := findDescriptor(t, provider, "core").Revision
	if err := provider.Update(t.Context(), "core", map[string]any{"label": "对得上"}, &current); err != nil {
		t.Fatalf("对得上的期望不该失败：%v", err)
	}
}

// TestConcurrentUpdatesSerializeSoNeitherPatchIsLost 钉住并发写不丢补丁。
//
// 源: packages/settings/settings/tests/settings.spec.ts:333-343
//
// 每一次写都从**轮到它的那一刻**的段上重新算，所以两个改不同字段的补丁
// 谁先谁后都无所谓，两个都在。读一次快照再各自整段写的话，后到的那个会盖掉前一个。
func TestConcurrentUpdatesSerializeSoNeitherPatchIsLost(t *testing.T) {
	t.Parallel()

	backend, provider := boot(t, nil)
	backend.mutex.Lock()
	backend.persistDelay = 5 * time.Millisecond
	backend.mutex.Unlock()

	scope := register(t, provider, "core", coreConfig{}, nil)

	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		if err := scope.Update(t.Context(), map[string]any{"label": "甲"}); err != nil {
			t.Errorf("甲这次写失败了：%v", err)
		}
	}()
	go func() {
		defer group.Done()
		if err := scope.Update(t.Context(), map[string]any{"timeout": 90}); err != nil {
			t.Errorf("乙这次写失败了：%v", err)
		}
	}()
	group.Wait()

	stored := backend.stored("core")
	if stored["label"] != "甲" || stored["timeout"] != float64(90) {
		t.Fatalf("两个补丁该都在，实际 %#v", stored)
	}
}

// TestMutateReadsTheSectionAtTheFrontOfTheQueue 钉住 op 作用在轮到它那一刻的段上。
//
// 源: packages/settings/settings/tests/settings.spec.ts:856-866
func TestMutateReadsTheSectionAtTheFrontOfTheQueue(t *testing.T) {
	t.Parallel()

	backend, provider := boot(t, nil)
	// 靠握手把「先写的那次已经占住队头」变成被等到的事实。原先是 sleep 2ms 去赌
	// 那个 goroutine 已经跑起来了——机器跑满时它可能还没被调度，mutate 就插到了
	// 前面，而那时这个用例问的问题已经不成立了。
	entered := make(chan struct{})
	gate := make(chan struct{})
	backend.mutex.Lock()
	backend.persistEntered = entered
	backend.persistGate = gate
	backend.mutex.Unlock()

	scope := register(t, provider, "core", coreConfig{}, nil)

	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		if err := scope.Update(t.Context(), map[string]any{"label": "先写的"}); err != nil {
			t.Errorf("写失败了：%v", err)
		}
	}()

	<-entered
	group.Add(1)
	go func() {
		defer group.Done()
		if err := provider.Mutate(t.Context(), "core",
			[]PathOp{{Kind: PathOpSet, Path: []string{"timeout"}, Value: 90}}, nil); err != nil {
			t.Errorf("mutate 失败了：%v", err)
		}
	}()
	close(gate)
	group.Wait()

	stored := backend.stored("core")
	if stored["label"] != "先写的" || stored["timeout"] != float64(90) {
		t.Fatalf("mutate 该看到排在它前面那次写的结果，实际 %#v", stored)
	}
}

// TestWatchStopsAfterItsDisposerRuns 钉住退订之后不再有调用进来。
//
// 源: packages/settings/settings/tests/settings.spec.ts:676-685
func TestWatchStopsAfterItsDisposerRuns(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, nil)
	scope := register(t, provider, "core", coreConfig{}, nil)

	calls := 0
	stop := scope.Watch(func(coreConfig, coreConfig) { calls++ })
	if err := scope.Update(t.Context(), map[string]any{"label": "一"}); err != nil {
		t.Fatalf("写失败了：%v", err)
	}
	stop()
	stop() // 幂等：多调几次不会摘错别人。
	if err := scope.Update(t.Context(), map[string]any{"label": "二"}); err != nil {
		t.Fatalf("写失败了：%v", err)
	}

	if calls != 1 {
		t.Fatalf("退订之后不该再被调，实际 %d 次", calls)
	}
}

// TestNilWatchersAndListenersAreNoOps 钉住递 nil 不会炸。
//
// 三个订阅入口都在导出面上，一个 nil 该被当成「不订阅」而不是在下一次提交时 panic——
// 那个 panic 会发生在别人的写路径上，离出错的现场隔着一整条调用栈。
func TestNilWatchersAndListenersAreNoOps(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, nil)
	scope := register(t, provider, "core", coreConfig{}, nil)

	scope.Watch(nil)()
	provider.SubscribeUpdated(nil)()
	provider.SubscribeDocumentUpdated(nil)()

	if err := scope.Update(t.Context(), map[string]any{"label": "x"}); err != nil {
		t.Fatalf("写失败了：%v", err)
	}
}

// TestAThrowingObserverIsContainedAndEveryOtherOneStillRuns 钉住兜底分发的前两条规则。
//
// 源: packages/settings/settings/tests/settings.spec.ts:344-355,392-403,686-701,1021-1031
//
// 一个订阅者炸掉不许掐断后面的：变更已经提交了，没跑到的那几个从此和存储不一致，
// 而它们永远不会知道。写入方也不该因为有人在旁边看崩了就收到一个失败。
func TestAThrowingObserverIsContainedAndEveryOtherOneStillRuns(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, nil)
	scope := register(t, provider, "core", coreConfig{}, nil)

	var order []string
	defer scope.Watch(func(coreConfig, coreConfig) {
		order = append(order, "炸的观察者")
		panic("观察者炸了")
	})()
	defer scope.Watch(func(coreConfig, coreConfig) { order = append(order, "后面的观察者") })()
	defer provider.SubscribeUpdated(func(Namespace, map[string]any, map[string]any, Source) {
		order = append(order, "炸的订阅者")
		panic("订阅者炸了")
	})()
	defer provider.SubscribeUpdated(func(Namespace, map[string]any, map[string]any, Source) {
		order = append(order, "后面的订阅者")
	})()
	defer provider.SubscribeDocumentUpdated(func(Namespace, uint64) {
		order = append(order, "炸的文档订阅者")
		panic("文档订阅者炸了")
	})()
	defer provider.SubscribeDocumentUpdated(func(Namespace, uint64) {
		order = append(order, "后面的文档订阅者")
	})()

	if err := scope.Update(t.Context(), map[string]any{"label": "x"}); err != nil {
		t.Fatalf("旁观者炸了不该让写失败，实际 %v", err)
	}
	if len(order) != 6 {
		t.Fatalf("六个订阅者都该跑到，实际 %v", order)
	}

	// 后续的提交照样活着。
	//
	// 源: packages/settings/settings/tests/settings.spec.ts:344-355
	if err := scope.Update(t.Context(), map[string]any{"label": "y"}); err != nil {
		t.Fatalf("后续的提交该照常，实际 %v", err)
	}
}

// TestAnInvariantCodedFailurePropagatesAfterEveryObserverRan 钉住兜底分发的第三条。
//
// 源: packages/settings/settings/tests/settings.spec.ts:323-332,1047-1057
//
// 不变量违例意味着程序写错了（见 invariants 包），它必须传到发起方手里；
// 但**还是要等所有订阅者都跑完**——否则一条违例会顺手把后面几个观察者也掐掉，
// 于是修完这条违例才会发现下面还藏着别的问题。
func TestAnInvariantCodedFailurePropagatesAfterEveryObserverRan(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, nil)
	scope := register(t, provider, "core", coreConfig{}, nil)

	ran := 0
	defer scope.Watch(func(coreConfig, coreConfig) {
		ran++
		panic(&invariants.Error{PackageName: PackageName, Message: "第一条"})
	})()
	defer scope.Watch(func(coreConfig, coreConfig) {
		ran++
		panic(&invariants.Error{PackageName: PackageName, Message: "第二条"})
	})()
	defer scope.Watch(func(coreConfig, coreConfig) { ran++ })()

	defer func() {
		recovered := recover()
		failure, isInvariant := recovered.(*invariants.Error)
		if !isInvariant {
			t.Fatalf("该重新抛出 *invariants.Error，实际 %v", recovered)
		}
		if failure.Message != "第一条" {
			t.Fatalf("该抛最早那条，实际 %q", failure.Message)
		}
		if ran != 3 {
			t.Fatalf("三个观察者都该跑到，实际 %d 个", ran)
		}
	}()

	_ = scope.Update(t.Context(), map[string]any{"label": "x"})
	t.Fatal("该抛出来才对")
}

// TestCloseDrainsInFlightWritesAndRejectsLaterOnes 钉住关闭的语义。
//
// 源: packages/settings/settings/tests/settings.spec.ts:443-460,489-499
func TestCloseDrainsInFlightWritesAndRejectsLaterOnes(t *testing.T) {
	t.Parallel()

	backend, provider := boot(t, nil)
	// 握手保证关闭发生在这次写的落盘中途。原先靠 sleep 2ms 去赌那个 goroutine
	// 已经起来了——赌输的时候关闭先到，在途那次写会被 ErrStopped 挡掉，用例红在
	// 一个它没打算问的问题上。
	entered := make(chan struct{})
	gate := make(chan struct{})
	backend.mutex.Lock()
	backend.persistEntered = entered
	backend.persistGate = gate
	backend.mutex.Unlock()

	scope := register(t, provider, "core", coreConfig{}, nil)

	var inFlight sync.WaitGroup
	inFlight.Add(1)
	go func() {
		defer inFlight.Done()
		if err := scope.Update(t.Context(), map[string]any{"label": "在途"}); err != nil {
			t.Errorf("在途那次写不该失败：%v", err)
		}
	}()

	<-entered
	close(gate)
	provider.Close()
	provider.Close() // 幂等
	inFlight.Wait()

	if backend.stored("core")["label"] != "在途" {
		t.Fatalf("在途的写该走完，实际 %#v", backend.stored("core"))
	}
	if err := scope.Update(t.Context(), map[string]any{"label": "太晚了"}); !errors.Is(err, ErrStopped) {
		t.Fatalf("关掉之后该报 ErrStopped，实际 %v", err)
	}
	if _, _, err := Register(provider, "late", coreConfig{}, nil); !errors.Is(err, ErrStopped) {
		t.Fatalf("关掉之后登记该报 ErrStopped，实际 %v", err)
	}
}

// TestAWriteQueuedBehindAnUnregistrationIsRejected 钉住排队期间拥有者走掉的那条路。
//
// 源: packages/settings/settings/tests/settings.spec.ts:404-442,500-517
//
// 拥有者已经走了，这次写的解析值就没有人会用；照写下去的话，
// 一段没有主人的配置会留在存储里，而下一个用这个名字登记的模块会莫名其妙读到它。
func TestAWriteQueuedBehindAnUnregistrationIsRejected(t *testing.T) {
	t.Parallel()

	backend, provider := boot(t, nil)
	// 用握手而不是 sleep 去摆这个时序。早先这里是「落盘停 10ms、注销方 sleep 2ms」，
	// 在跑满的机器上这 8ms 余量会翻过来：第一次写连通知一起走完了，注销才发生，
	// 于是 notified 是 1，用例红在一个根本没成立的前提上。
	entered := make(chan struct{})
	gate := make(chan struct{})
	backend.mutex.Lock()
	backend.persistEntered = entered
	backend.persistGate = gate
	backend.mutex.Unlock()

	scope, dispose, err := Register(provider, "core", coreConfig{}, nil)
	if err != nil {
		t.Fatalf("登记失败：%v", err)
	}

	notified := atomic.Int64{}
	scope.Watch(func(coreConfig, coreConfig) { notified.Add(1) })

	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		// 这次写拿到写锁之后会停在落盘里，等下面放行。
		_ = scope.Update(t.Context(), map[string]any{"label": "在途"})
	}()

	// 等到第一次写确实停在落盘里，再注销。这一步之后，「拥有者是在这次写落盘的
	// 中途走掉的」就是被保证的事实，不是赛出来的。
	<-entered
	dispose()

	group.Add(1)
	go func() {
		defer group.Done()
		// 注销之后排进来的这次写必须被拒。
		if err := scope.Update(t.Context(), map[string]any{"label": "太晚了"}); !errors.Is(err, ErrNotRegistered) {
			t.Errorf("注销之后该报 ErrNotRegistered，实际 %v", err)
		}
	}()

	close(gate)
	group.Wait()

	if notified.Load() != 0 {
		t.Fatalf("走掉的拥有者不该收到通知，实际 %d 次", notified.Load())
	}
}

// TestScopeNamespaceReportsWhatItOwns 钉住句柄认得自己那一段。
func TestScopeNamespaceReportsWhatItOwns(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, nil)
	scope := register(t, provider, "core", coreConfig{}, nil)

	if scope.Namespace() != "core" {
		t.Fatalf("该是 core，实际 %q", scope.Namespace())
	}
}

// TestAppliesDefaultsToLiveAndCarriesRestartThrough 钉住生效时机被原样带到描述那一面。
//
// 改完就生效的字段和改完要重启的字段，界面上得给出不同的提示，
// 否则用户会以为自己改了没用。
func TestAppliesDefaultsToLiveAndCarriesRestartThrough(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, nil)
	register(t, provider, "core", coreConfig{}, nil)
	register(t, provider, "boot", coreConfig{}, &Options[coreConfig]{Applies: AppliesRestart})

	if got := findDescriptor(t, provider, "core").Applies; got != AppliesLive {
		t.Fatalf("缺省该是 %q，实际 %q", AppliesLive, got)
	}
	if got := findDescriptor(t, provider, "boot").Applies; got != AppliesRestart {
		t.Fatalf("该是 %q，实际 %q", AppliesRestart, got)
	}
}

// TestRegisterRejectsANonJSONShapedDefaultOrBase 钉住两层的入口也过 JSON 形状检查。
//
// 存不下的东西一路带到解析里去的话，症状会晚得多：登记成功、读也正常，
// 直到某一次写触发了序列化才报错，而那次写和这个默认值没有任何关系。
func TestRegisterRejectsANonJSONShapedDefaultOrBase(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, nil)

	type bad struct {
		Fn func() `json:"fn"`
	}
	if _, _, err := Register(provider, "core", bad{Fn: func() {}}, nil); err == nil {
		t.Fatal("默认值存不下该失败")
	}
	_, _, err := Register(provider, "core", coreConfig{},
		&Options[coreConfig]{Base: map[string]any{"fn": func() {}}})
	if err == nil {
		t.Fatal("组装层存不下该失败")
	}
}

// TestDecodeSectionKeepsUnknownKeys 钉住类型里没有的键不会让整段解析不了。
//
// 存下来的文档可能带着一个已经删掉的旧字段，或者一个更新版本才认识的新字段。
// 把它们判成错误的话，一次降级运行会让整个命名空间解析不了——
// 而那个命名空间的拥有者根本没改过任何代码。
func TestDecodeSectionKeepsUnknownKeys(t *testing.T) {
	t.Parallel()

	_, provider := boot(t, map[string]any{"core": map[string]any{
		"timeout": float64(90),
		"从未见过的字段": "来自另一个版本",
	}})
	scope := register(t, provider, "core", coreConfig{}, nil)

	if scope.Get().Timeout != 90 {
		t.Fatalf("认识的字段该正常解析，实际 %d", scope.Get().Timeout)
	}
	descriptor := findDescriptor(t, provider, "core")
	if descriptor.User["从未见过的字段"] != "来自另一个版本" {
		t.Fatalf("不认识的字段该原样留在用户段里，实际 %#v", descriptor.User)
	}
}

// findDescriptor 从描述里挑出一个命名空间，找不到就让用例失败。
func findDescriptor(t *testing.T, p *Provider, ns Namespace) Descriptor {
	t.Helper()

	for _, descriptor := range p.Describe(nil) {
		if descriptor.Namespace == ns {
			return descriptor
		}
	}
	t.Fatalf("描述里没有 %q", string(ns))
	return Descriptor{}
}
