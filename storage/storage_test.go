// 本文件的作用：把 DSH 那份 registry.spec.ts 钉住的行为在 Go 侧重新钉一遍，
// 外加 Go 这边才需要钉的几条（形态解析的类型断言、名字校验、并发安全）。
//
// # 为什么全部写成外部测试包
//
// package storage_test 而不是 package storage：这样用例只能走导出的 API，
// 于是「这个包对外是不是够用」这件事由测试本身证明。共用一致性测试包 storagetest
// 反过来导入 storage，写成内部测试包会直接构成导入环。
//
// # 这个包的错法
//
// 全是**静默的**：一个过期的卸载函数把继任者摘掉、并发注册把 map 写坏、
// 形态解析拿到一个类型不对的东西。第一条和第三条不报错只是行为变了，
// 第二条在 Go 里是直接崩进程。每条都得有断言压着。
package storage_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/snight1983/ds-harness-go/storage"
	"github.com/snight1983/ds-harness-go/storage/storagetest"
)

// hasCode 判断一个错误是不是带某个码的 *storage.Error。
//
// 用 errors.As 而不是类型断言：调用方拿到的可能是被包过一层的错误。
func hasCode(err error, code storage.ErrorCode) bool {
	var typed *storage.Error
	return errors.As(err, &typed) && typed.Code == code
}

// TestKVBackendContract 用内存后端跑一遍共用一致性测试。
//
// 这一条同时证明两件事：内存后端确实守着契约，以及**那套契约测试不是空的**——
// 一份谁都能过的一致性测试和没有测试是一回事。
func TestKVBackendContract(t *testing.T) {
	t.Parallel()

	storagetest.RunKVBackendContract(t, "memory", func(t *testing.T) storagetest.Harness {
		medium := newMemoryMedium()
		return storagetest.Harness{
			Backend: newMemoryBackend(medium),
			// 重开：同一份介质上另开一个后端，等价于进程重启。
			Reopen: func() (storage.Backend, error) { return newMemoryBackend(medium), nil },
		}
	})
}

// TestRegistryRegistersResolvesAndDisposes 钉住注册表最基本的那一圈。
//
// 源: packages/storage/storage/tests/registry.spec.ts:9-18
func TestRegistryRegistersResolvesAndDisposes(t *testing.T) {
	t.Parallel()

	registry := storage.NewBackendRegistry()
	backend := newMemoryBackend(newMemoryMedium())

	dispose, err := registry.Register("json", backend)
	if err != nil {
		t.Fatalf("注册意外失败：%v", err)
	}

	got, err := registry.Get("json")
	if err != nil {
		t.Fatalf("解析意外失败：%v", err)
	}
	if got != storage.Backend(backend) {
		t.Error("解析出来的该是注册进去的那一个")
	}
	if names := registry.Names(); len(names) != 1 || names[0] != "json" {
		t.Errorf("该只列出 json，实际 %v", names)
	}

	dispose()

	if names := registry.Names(); len(names) != 0 {
		t.Errorf("注销之后该一个都不剩，实际 %v", names)
	}
	if _, err := registry.Get("json"); !hasCode(err, storage.CodeBackendNotFound) {
		t.Errorf("注销之后该报 %s，实际 %v", storage.CodeBackendNotFound, err)
	}
}

// TestRegistryRejectsDuplicateNames 钉住重名是**拒绝**，不是覆盖。
//
// 源: packages/storage/storage/tests/registry.spec.ts:20-24
//
// 覆盖的话，两个装配点各自注册了一个后端，其中一个的写会全部落到另一个介质上，
// 而两边都没有任何错误。
func TestRegistryRejectsDuplicateNames(t *testing.T) {
	t.Parallel()

	registry := storage.NewBackendRegistry()
	first := newMemoryBackend(newMemoryMedium())
	if _, err := registry.Register("json", first); err != nil {
		t.Fatalf("第一次注册意外失败：%v", err)
	}

	dispose, err := registry.Register("json", newMemoryBackend(newMemoryMedium()))
	if !hasCode(err, storage.CodeDuplicateBackend) {
		t.Fatalf("重名该报 %s，实际 %v", storage.CodeDuplicateBackend, err)
	}
	if dispose != nil {
		t.Error("注册失败时不该给出注销函数——拿到手的人会以为自己注册成功了")
	}

	// 先来的那个必须原封不动：被拒绝的注册不能有任何副作用。
	got, err := registry.Get("json")
	if err != nil {
		t.Fatalf("先注册的那个该还在：%v", err)
	}
	if got != storage.Backend(first) {
		t.Error("被拒绝的注册不该把先来的那个挤掉")
	}
}

// TestRegistryIgnoresAStaleDisposer 钉住过期的注销函数摘不掉继任者。
//
// 源: packages/storage/storage/tests/registry.spec.ts:50-68
//
// 重复 defer、重试路径上多走一遍——过期的注销函数被再调一次是很平常的事。
// 它要是把继任者摘掉了，之后所有解析都报「没注册」，看起来像后端根本没装上，
// 排查会从装配那头开始找，而那头是对的。
func TestRegistryIgnoresAStaleDisposer(t *testing.T) {
	t.Parallel()

	registry := storage.NewBackendRegistry()
	first := newMemoryBackend(newMemoryMedium())
	second := newMemoryBackend(newMemoryMedium())

	staleDispose, err := registry.Register("json", first)
	if err != nil {
		t.Fatalf("第一次注册意外失败：%v", err)
	}
	staleDispose()

	if _, err := registry.Register("json", second); err != nil {
		t.Fatalf("摘掉之后该能重新注册：%v", err)
	}
	staleDispose() // 过期的那个又被调了一次。

	got, err := registry.Get("json")
	if err != nil {
		t.Fatalf("继任者该还在：%v", err)
	}
	if got != storage.Backend(second) {
		t.Error("过期的注销函数把继任者摘掉了")
	}
}

// TestRegistryIgnoresARepeatedDisposer 钉住同一个注销函数调两次是安全的。
//
// 这是上一条的退化情形，但走的是另一条分支（名字底下已经空了），单独钉一条。
func TestRegistryIgnoresARepeatedDisposer(t *testing.T) {
	t.Parallel()

	registry := storage.NewBackendRegistry()
	dispose, err := registry.Register("json", newMemoryBackend(newMemoryMedium()))
	if err != nil {
		t.Fatalf("注册意外失败：%v", err)
	}

	dispose()
	dispose() // 不该 panic，也不该有任何别的效果。

	if names := registry.Names(); len(names) != 0 {
		t.Errorf("该一个都不剩，实际 %v", names)
	}
}

// TestRegistryNotFoundListsWhatIsRegistered 钉住诊断信息真的带上了已注册的名字。
//
// 源: packages/storage/storage/src/registry.ts:39-53
//
// 这类失败绝大多数是名字拼错或者装配顺序不对，而两者都能靠「实际有哪些」一眼看出来。
// 不带的话，读错误的人只知道自己要的那个没有，接下来只能去翻装配代码。
func TestRegistryNotFoundListsWhatIsRegistered(t *testing.T) {
	t.Parallel()

	registry := storage.NewBackendRegistry()
	for _, name := range []string{"sqlite", "json"} {
		if _, err := registry.Register(name, newMemoryBackend(newMemoryMedium())); err != nil {
			t.Fatalf("注册 %q 意外失败：%v", name, err)
		}
	}

	_, err := registry.Get("jsom")
	if !hasCode(err, storage.CodeBackendNotFound) {
		t.Fatalf("该报 %s，实际 %v", storage.CodeBackendNotFound, err)
	}
	for _, want := range []string{"json", "sqlite"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息里该列出已注册的 %q，实际 %q", want, err.Error())
		}
	}

	// 一个都没注册时也要说得通，不能是一段空白。
	empty := storage.NewBackendRegistry()
	if _, err := empty.Get("json"); !strings.Contains(err.Error(), "无") {
		t.Errorf("一个都没注册时该说清楚，实际 %q", err)
	}
}

// TestRegistryNamesComeBackSorted 钉住名字是排好序给出来的。
//
// Go 的 map 遍历顺序是**故意随机**的，直接给出去的话同一个进程两次调用的顺序都可能不一样,
// 诊断输出和测试断言都没法用。
func TestRegistryNamesComeBackSorted(t *testing.T) {
	t.Parallel()

	registry := storage.NewBackendRegistry()
	for _, name := range []string{"sqlite", "json", "memory"} {
		if _, err := registry.Register(name, newMemoryBackend(newMemoryMedium())); err != nil {
			t.Fatalf("注册 %q 意外失败：%v", name, err)
		}
	}

	want := []string{"json", "memory", "sqlite"}
	for round := range 5 {
		got := registry.Names()
		if len(got) != len(want) {
			t.Fatalf("第 %d 次该有 %d 个名字，实际 %v", round+1, len(want), got)
		}
		for index := range want {
			if got[index] != want[index] {
				t.Fatalf("第 %d 次的第 %d 个名字该是 %q，实际 %q", round+1, index, want[index], got[index])
			}
		}
	}
}

// TestRegistryDisposalDoesNotCloseTheBackend 钉住注销**不关**后端。
//
// 源: packages/storage/storage/src/registry.ts:17-37
//
// 这张表从来没有拿到过后端的所有权。替别人关掉一个它不拥有的东西，会让「谁负责关」
// 在两个地方各有一个答案，而两个答案迟早会不一致——通常表现为后端被关了两次，
// 或者拥有它的那一方还在往一个已经关掉的介质上写。
func TestRegistryDisposalDoesNotCloseTheBackend(t *testing.T) {
	t.Parallel()

	backend := newMemoryBackend(newMemoryMedium())
	registry := storage.NewBackendRegistry()
	dispose, err := registry.Register("memory", backend)
	if err != nil {
		t.Fatalf("注册意外失败：%v", err)
	}

	dispose()

	// 还开着就说明没被关：关掉的后端会拒绝打开单元。
	facet, ok := storage.KV(backend)
	if !ok {
		t.Fatal("内存后端该提供键值形态")
	}
	if _, err := facet.Open(context.Background(), storage.KVUnitDescriptor{
		Name: "still_open", Version: 1, Tables: []string{"alpha"},
	}); err != nil {
		t.Errorf("注销不该把后端也关掉，实际打不开单元了：%v", err)
	}
}

// TestRegistryIsSafeUnderConcurrentUse 钉住注册表能被并发用。
//
// 新增: DSH 那边是单线程的，一个 Map 就够。Go 这边注册发生在装配期、解析发生在
// 请求处理里，是两个不同的 goroutine——而 map 的并发读写在 Go 里是**直接 crash**，
// 不是读到旧值。这一条要配 -race 才有意义。
func TestRegistryIsSafeUnderConcurrentUse(t *testing.T) {
	t.Parallel()

	registry := storage.NewBackendRegistry()
	var group sync.WaitGroup
	for index := range 8 {
		group.Add(1)
		go func() {
			defer group.Done()

			name := "backend" + string(rune('a'+index))
			dispose, err := registry.Register(name, newMemoryBackend(newMemoryMedium()))
			if err != nil {
				t.Errorf("并发注册 %q 失败：%v", name, err)
				return
			}
			_, _ = registry.Get(name)
			_ = registry.Names()
			dispose()
		}()
	}
	group.Wait()

	if names := registry.Names(); len(names) != 0 {
		t.Errorf("全部注销之后该一个都不剩，实际 %v", names)
	}
}

// TestStorageMountsResolvesAndUnmountsAForm 钉住中枢那一圈，和注册表对称。
//
// 源: packages/storage/storage/tests/registry.spec.ts:33-48
func TestStorageMountsResolvesAndUnmountsAForm(t *testing.T) {
	t.Parallel()

	hub := storage.New()
	if hub.Backend == nil {
		t.Fatal("中枢建出来就该带一张后端表")
	}

	facility := &struct{ marker bool }{marker: true}
	dispose, err := hub.Mount("domain", facility)
	if err != nil {
		t.Fatalf("挂载意外失败：%v", err)
	}

	got, err := hub.Form("domain")
	if err != nil {
		t.Fatalf("解析意外失败：%v", err)
	}
	if got != any(facility) {
		t.Error("解析出来的该是挂上去的那一个")
	}
	if forms := hub.Forms(); len(forms) != 1 || forms[0] != "domain" {
		t.Errorf("该只列出 domain，实际 %v", forms)
	}

	if _, err := hub.Mount("domain", facility); !hasCode(err, storage.CodeDuplicateMount) {
		t.Errorf("重复挂载该报 %s，实际 %v", storage.CodeDuplicateMount, err)
	}

	dispose()

	if _, err := hub.Form("domain"); !hasCode(err, storage.CodeFormNotMounted) {
		t.Errorf("卸载之后该报 %s，实际 %v", storage.CodeFormNotMounted, err)
	}
	if forms := hub.Forms(); len(forms) != 0 {
		t.Errorf("卸载之后该一个都不剩，实际 %v", forms)
	}
}

// TestStorageIgnoresAStaleMountDisposer 钉住过期的卸载函数摘不掉继任者。
//
// 源: packages/storage/storage/tests/registry.spec.ts:50-60
func TestStorageIgnoresAStaleMountDisposer(t *testing.T) {
	t.Parallel()

	hub := storage.New()
	first := &struct{ n int }{n: 1}
	second := &struct{ n int }{n: 2}

	staleDispose, err := hub.Mount("domain", first)
	if err != nil {
		t.Fatalf("第一次挂载意外失败：%v", err)
	}
	staleDispose()

	if _, err := hub.Mount("domain", second); err != nil {
		t.Fatalf("摘掉之后该能重新挂：%v", err)
	}
	staleDispose()

	got, err := hub.Form("domain")
	if err != nil {
		t.Fatalf("继任者该还在：%v", err)
	}
	if got != any(second) {
		t.Error("过期的卸载函数把继任者摘掉了")
	}
}

// TestStorageMountAcceptsAnUncomparableFacility 钉住挂一个不可比较的设施不会炸。
//
// 新增: DSH 那边卸载时比的是设施对象本身（`forms.get(form) === facility`）。Go 里
// 照搬会出人命：设施的类型是 any，切片、map、函数都进得来，而对它们做 == 是
// 运行期 panic——一个**卸载函数**把整个进程炸掉，比它本该防的那个 bug 严重得多。
// 所以实现里认的是一次性令牌，不是对象身份。
func TestStorageMountAcceptsAnUncomparableFacility(t *testing.T) {
	t.Parallel()

	hub := storage.New()
	dispose, err := hub.Mount("slice_form", []string{"不可比较"})
	if err != nil {
		t.Fatalf("挂载意外失败：%v", err)
	}

	dispose() // 认令牌的话这里安然无恙；认对象身份的话这里 panic。

	if _, err := hub.Form("slice_form"); !hasCode(err, storage.CodeFormNotMounted) {
		t.Errorf("卸载之后该报 %s，实际 %v", storage.CodeFormNotMounted, err)
	}
}

// TestStorageNotMountedListsWhatIsMounted 钉住形态解析失败时也列出实际挂了什么。
func TestStorageNotMountedListsWhatIsMounted(t *testing.T) {
	t.Parallel()

	hub := storage.New()
	if _, err := hub.Mount("domain", &struct{}{}); err != nil {
		t.Fatalf("挂载意外失败：%v", err)
	}

	_, err := hub.Form("domian")
	if !hasCode(err, storage.CodeFormNotMounted) {
		t.Fatalf("该报 %s，实际 %v", storage.CodeFormNotMounted, err)
	}
	if !strings.Contains(err.Error(), "domain") {
		t.Errorf("错误信息里该列出已挂载的 domain，实际 %q", err)
	}

	if _, err := storage.New().Form("domain"); !strings.Contains(err.Error(), "无") {
		t.Errorf("一个都没挂时该说清楚，实际 %q", err)
	}
}

// domainFacility 是 [storage.FormAs] 用例里那个设施的类型。
type domainFacility interface{ Domain() string }

type demoDomain struct{ name string }

func (d demoDomain) Domain() string { return d.name }

// TestFormAsResolvesWithTheCallersType 钉住带类型的形态解析。
//
// 源: packages/storage/storage/src/index.ts:89-92
//
// 这是 DSH 那个 `get domain()` 在 Go 里的等价物：那边靠声明合并把类型带出来，
// 这边由调用方在解析时报出它要的类型。
func TestFormAsResolvesWithTheCallersType(t *testing.T) {
	t.Parallel()

	hub := storage.New()
	if _, err := hub.Mount("domain", demoDomain{name: "领域层"}); err != nil {
		t.Fatalf("挂载意外失败：%v", err)
	}

	got, err := storage.FormAs[domainFacility](hub, "domain")
	if err != nil {
		t.Fatalf("解析意外失败：%v", err)
	}
	if got.Domain() != "领域层" {
		t.Errorf("该解析出 %q，实际 %q", "领域层", got.Domain())
	}
}

// TestFormAsRejectsAMismatchedType 钉住挂的东西类型不对时是**报错**，不是给零值。
//
// 给零值的话，调用方会拿着一个 nil 接口走下去，在远处炸——而那个远处和真正的
// 装配错误之间已经隔了很多层。错误信息里要写清实际挂的是什么类型，那是排查这类
// 失败真正需要的东西。
func TestFormAsRejectsAMismatchedType(t *testing.T) {
	t.Parallel()

	hub := storage.New()
	if _, err := hub.Mount("domain", "这是个字符串，不是设施"); err != nil {
		t.Fatalf("挂载意外失败：%v", err)
	}

	got, err := storage.FormAs[domainFacility](hub, "domain")
	if !hasCode(err, storage.CodeFormNotMounted) {
		t.Fatalf("类型不对该报 %s，实际 %v", storage.CodeFormNotMounted, err)
	}
	if got != nil {
		t.Error("失败时该给零值")
	}
	if !strings.Contains(err.Error(), "string") {
		t.Errorf("错误信息里该写清实际挂的是什么类型，实际 %q", err)
	}
}

// TestFormAsPropagatesTheNotMountedFailure 钉住压根没挂时 FormAs 也报得出来。
func TestFormAsPropagatesTheNotMountedFailure(t *testing.T) {
	t.Parallel()

	if _, err := storage.FormAs[domainFacility](storage.New(), "domain"); !hasCode(err, storage.CodeFormNotMounted) {
		t.Errorf("没挂载该报 %s，实际 %v", storage.CodeFormNotMounted, err)
	}
}

// TestStorageIsSafeUnderConcurrentUse 钉住中枢能被并发用，理由同注册表那条。
func TestStorageIsSafeUnderConcurrentUse(t *testing.T) {
	t.Parallel()

	hub := storage.New()
	var group sync.WaitGroup
	for index := range 8 {
		group.Add(1)
		go func() {
			defer group.Done()

			form := "form" + string(rune('a'+index))
			dispose, err := hub.Mount(form, &struct{ n int }{n: index})
			if err != nil {
				t.Errorf("并发挂载 %q 失败：%v", form, err)
				return
			}
			_, _ = hub.Form(form)
			_ = hub.Forms()
			dispose()
		}()
	}
	group.Wait()

	if forms := hub.Forms(); len(forms) != 0 {
		t.Errorf("全部卸载之后该一个都不剩，实际 %v", forms)
	}
}

// TestKVReportsWhenABackendCannotServeTheForm 钉住「服务不了」不是一个错误码。
//
// 源: packages/storage/storage/src/backend.ts:12-16
//
// 和 DSH 一致：那边写的是 `backend.kv!.open(...)`，缺了就是当场的类型错误，
// 而 StorageErrorCode 那个封闭词汇里从来没有它。这里由第二个返回值回答。
func TestKVReportsWhenABackendCannotServeTheForm(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		backend storage.Backend
		want    bool
	}{
		{"提供得了", newMemoryBackend(newMemoryMedium()), true},
		{"压根没实现那个更宽的接口", bareBackend{}, false},
		// 新增: 满足接口但返回 nil，和不满足接口是同一件事。不查的话，调用方会拿着
		// 一个 nil 接口值走下去，在远处炸。
		{"满足接口但给了 nil", nilFacetBackend{}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			facet, ok := storage.KV(test.backend)
			if ok != test.want {
				t.Fatalf("该是 %v，实际 %v", test.want, ok)
			}
			if !ok && facet != nil {
				t.Error("服务不了时不该给出操作组")
			}
		})
	}
}

// bareBackend 只满足 [storage.Backend]，一种数据形态都不提供。
type bareBackend struct{}

func (bareBackend) Close(context.Context) error { return nil }

// nilFacetBackend 满足 [storage.KVProvider]，但把操作组给成了 nil。
type nilFacetBackend struct{}

func (nilFacetBackend) Close(context.Context) error { return nil }
func (nilFacetBackend) KV() storage.KVFacet         { return nil }

// TestDescriptorValidateRejectsMalformedShapes 钉住描述符校验放在共用的地方。
//
// 新增: DSH 把「必须匹配 UNIT_NAME_RE」写在注释里，由每个后端各自去查。那样两个后端
// 很容易查得不一样（一个查了表名一个没查），而查漏的后果是一个带斜杠的表名被当成
// 文件路径的一段——它会安静地写到另一个目录里去。
func TestDescriptorValidateRejectsMalformedShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		descriptor storage.KVUnitDescriptor
		wantOK     bool
	}{
		{"合法", storage.KVUnitDescriptor{Name: "unit_1", Version: 3, Tables: []string{"alpha", "beta"}}, true},
		{"没有表也是合法的", storage.KVUnitDescriptor{Name: "unit", Version: 0}, true},
		{"单元名是空的", storage.KVUnitDescriptor{Name: "", Version: 1}, false},
		{"单元名以数字开头", storage.KVUnitDescriptor{Name: "1unit", Version: 1}, false},
		{"单元名有大写", storage.KVUnitDescriptor{Name: "Unit", Version: 1}, false},
		{"单元名里有斜杠", storage.KVUnitDescriptor{Name: "a/b", Version: 1}, false},
		{"版本是负数", storage.KVUnitDescriptor{Name: "unit", Version: -1}, false},
		{"表名不合法", storage.KVUnitDescriptor{Name: "unit", Version: 1, Tables: []string{"Alpha"}}, false},
		{"表名重复", storage.KVUnitDescriptor{Name: "unit", Version: 1, Tables: []string{"a", "a"}}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.descriptor.Validate()
			if test.wantOK {
				if err != nil {
					t.Fatalf("该通过校验，实际 %v", err)
				}
				return
			}
			if !hasCode(err, storage.CodeMalformedMedium) {
				t.Fatalf("该报 %s，实际 %v", storage.CodeMalformedMedium, err)
			}
		})
	}
}

// TestValidUnitName 钉住名字规则本身。
//
// 这个形状同时要满足两件事：当文件名安全，以及不转义就能当 SQL 标识符的一段。
func TestValidUnitName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want bool
	}{
		{"a", true},
		{"unit_1", true},
		{"a1_b2", true},
		{"", false},
		{"1a", false},
		{"_a", false},
		{"A", false},
		{"a-b", false},
		{"a.b", false},
		{"a b", false},
		{"a/b", false},
	}
	for _, test := range tests {
		if got := storage.ValidUnitName(test.name); got != test.want {
			t.Errorf("ValidUnitName(%q) 该是 %v，实际 %v", test.name, test.want, got)
		}
	}
}

// TestErrorCarriesTheCodeAndKeepsTheCauseReachable 钉住失败类型该有的三件事。
//
// 调用方**照着 Code 分派**，不要去匹配 Message 里的字——文案改一次，
// 靠字符串匹配的分派就会静默失配。
func TestErrorCarriesTheCodeAndKeepsTheCauseReachable(t *testing.T) {
	t.Parallel()

	cause := errors.New("底下的文件系统报错")
	failure := &storage.Error{Code: storage.CodeMalformedMedium, Message: "单元读不出形状", Err: cause}

	if !hasCode(failure, storage.CodeMalformedMedium) {
		t.Error("该能用 errors.As 取出 *storage.Error 并读到 Code")
	}
	if !errors.Is(failure, cause) {
		t.Error("该能顺着 Unwrap 问出底层原因")
	}
	// 码要出现在文案里：往上抛的时候多半只抛一句 err.Error()。
	text := failure.Error()
	if !strings.Contains(text, string(storage.CodeMalformedMedium)) || !strings.Contains(text, "单元读不出形状") {
		t.Errorf("文案里该同时带上码和诊断细节，实际 %q", text)
	}
	// 没有底层原因也是合法的，此时 Unwrap 给 nil，文案照样要能出。
	if got := (&storage.Error{Code: storage.CodeClosed, Message: "关了"}).Error(); got == "" {
		t.Error("没带底层原因时也该有文案")
	}
}
