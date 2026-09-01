// Package storage 定义存储中枢：一张具名后端表，加上挂在上面的若干数据形态设施。
//
// 对应 DSH 的 @deepseek-ai/dsh-storage（packages/storage/storage）。
//
// 源: packages/storage/storage/src/index.ts:1-6
//
// # 中枢自己不做任何 IO
//
// 这一层只做登记：名字 → 后端、形态名 → 设施。介质归后端拥有，语义归数据形态拥有
// （领域层是第一个数据形态）。中枢一旦自己动手读写，「谁拥有这份介质」就有了第二个答案。
//
// # 和 DSH 的主要差异：没有 cordis
//
// DSH 里 Storage 是一个 cordis Service，挂在 ctx.storage 上，形态靠 TypeScript 的
// 声明合并往 StorageForms 接口里加成员，于是 ctx.storage.domain 是一个有类型的属性。
// Go 既没有声明合并也没有上下文槽——中枢由调用方自己建、自己传。
//
// 形态那半由 [FormAs] 补上：泛型自由函数取代「合并出来的具名属性」。做成自由函数
// 而不是方法，是因为 Go 的方法不能带自己的类型参数。
package storage

import (
	"sort"
	"sync"
)

// mount 是一次挂载留下的那一条，令牌的作用同 [registration]。
type mount struct {
	facility any
	token    uint64
}

// Storage 是存储中枢。
//
// 源: packages/storage/storage/src/index.ts:43-93
//
// 零值不可用，必须经 [New] 建出来。
type Storage struct {
	// Backend 是具名后端表。多个后端并排挂着，谁用哪个由使用方的配置决定。
	Backend *BackendRegistry

	// 新增: 带锁，理由和 [BackendRegistry] 那把一样——挂载发生在装配期，
	// 解析发生在请求处理里，是两个不同的 goroutine。
	mutex     sync.RWMutex
	nextToken uint64
	forms     map[string]mount
}

// New 建一个空的中枢。
func New() *Storage {
	return &Storage{
		Backend: NewBackendRegistry(),
		forms:   map[string]mount{},
	}
}

// Mount 把一个数据形态设施挂到中枢上，返回卸载它的函数。
//
// 源: packages/storage/storage/src/index.ts:57-75
//
// 挂载是一个**效果**：返回的那个函数把这个形态摘掉。和注销后端一样，卸载**不负责**
// 关闭设施——设施的生命周期归挂它的那一方。
//
// 形态名已经挂过时返回 Code 为 [CodeDuplicateMount] 的 *[Error]。
//
// 新增: DSH 那边形态名是 StorageForms 这个接口的键，靠声明合并扩展，写错名字是编译错误。
// Go 里是一个普通字符串，写错只能等到 [Form] 解析不到——所以 [Form] 的错误信息里
// 会把已挂载的形态一并列出来，理由和 [BackendRegistry.Get] 相同。
func (s *Storage) Mount(form string, facility any) (func(), error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if _, exists := s.forms[form]; exists {
		return nil, newError(CodeDuplicateMount, "数据形态 %q 已经挂载过了", form)
	}
	s.nextToken++
	token := s.nextToken
	s.forms[form] = mount{facility: facility, token: token}

	return func() { s.unmount(form, token) }, nil
}

// unmount 只摘掉**这一次挂载**留下的那一条。
//
// 源: packages/storage/storage/src/index.ts:69-74
//
// 和 [BackendRegistry.unregister] 是同一道防护，防的也是同一件事，
// 认令牌不认设施本身的理由也一样——而这里更必须如此：设施的类型是 any，
// 调用方挂一个切片进来是完全合法的，而对切片做 == 会当场 panic。
func (s *Storage) unmount(form string, token uint64) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if current, exists := s.forms[form]; exists && current.token == token {
		delete(s.forms, form)
	}
}

// Form 解析一个已挂载的数据形态。
//
// 源: packages/storage/storage/src/index.ts:77-87
//
// 没挂载时返回 Code 为 [CodeFormNotMounted] 的 *[Error]，并把**已挂载的形态名**
// 一并列出来，理由同 [BackendRegistry.Get]。
func (s *Storage) Form(form string) (any, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	current, ok := s.forms[form]
	if !ok {
		return nil, newError(CodeFormNotMounted,
			"数据形态 %q 没有挂载（已挂载的：%s）", form, describeNames(s.sortedForms()))
	}
	return current.facility, nil
}

// Forms 返回已挂载的形态名，供诊断使用，按字典序排好（理由同 [BackendRegistry.Names]）。
func (s *Storage) Forms() []string {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	return s.sortedForms()
}

// sortedForms 假定调用方已经持有锁。
func (s *Storage) sortedForms() []string {
	names := make([]string, 0, len(s.forms))
	for name := range s.forms {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// FormAs 解析一个已挂载的数据形态，并把它断言成 T。
//
// 源: packages/storage/storage/src/index.ts:36-41（StorageForms）
//
// 新增: 这是 DSH 那个 `get domain()` 在 Go 里的等价物。那边靠声明合并让
// ctx.storage.domain 直接带上类型；Go 没有这个机制，所以由调用方在解析时报出它要的类型。
//
// 做成自由函数而不是 [Storage] 的方法，是因为 Go 的方法不能带自己的类型参数。
//
// 挂着的东西不是 T 时返回 Code 为 [CodeFormNotMounted] 的 *[Error]——和「压根没挂」
// 归成同一类，因为对调用方来说后果一样：它要的那个形态在这里拿不到。错误信息里会写清
// 实际挂的是什么类型，那是排查这类失败真正需要的东西。
func FormAs[T any](s *Storage, form string) (T, error) {
	var zero T

	facility, err := s.Form(form)
	if err != nil {
		return zero, err
	}

	typed, ok := facility.(T)
	if !ok {
		return zero, newError(CodeFormNotMounted,
			"数据形态 %q 挂的是 %T，不是调用方要的 %T", form, facility, zero)
	}
	return typed, nil
}
