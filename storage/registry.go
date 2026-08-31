// 本文件的作用：存储中枢的具名后端表。
//
// 源: packages/storage/storage/src/registry.ts:1-4

package storage

import (
	"sort"
	"strings"
	"sync"
)

// registration 是一次注册留下的那一条。
//
// 新增: 除了后端本身还记一个**一次性令牌**，它是注销函数唯一认得的凭据。
// 详见 [BackendRegistry.unregister]。
type registration struct {
	backend Backend
	token   uint64
}

// BackendRegistry 是一张可变的「名字 → 后端」表。
//
// 源: packages/storage/storage/src/registry.ts:9-14
//
// 多个后端**并排挂着**，谁用哪个是使用方自己的配置（比如领域层的路由表），
// 从来不是中枢层面的全局选择。这一条是这张表存在的理由：做成全局单选的话，
// 「把某一类数据换到另一个后端上」就得动所有人。
//
// 新增: 带锁。DSH 那边是单线程的，一个 Map 就够了。Go 这边注册发生在装配期、
// 解析发生在请求处理里，是两个不同的 goroutine，不加锁就是数据竞争——
// 而 map 的并发读写在 Go 里是直接 crash，不是读到旧值。
type BackendRegistry struct {
	mutex     sync.RWMutex
	nextToken uint64
	backends  map[string]registration
}

// NewBackendRegistry 建一张空表。
func NewBackendRegistry() *BackendRegistry {
	return &BackendRegistry{backends: map[string]registration{}}
}

// Register 注册一个具名后端，返回注销它的函数。
//
// 源: packages/storage/storage/src/registry.ts:17-37
//
// 注册是一个**效果**：返回的那个函数把这个名字摘掉。
//
// 注销**不会**关闭后端——关闭是拥有它的那一方在注销之后自己做的事。
// 这个分工是有意的：这张表没有拿到过后端的所有权，替别人关掉一个它不拥有的东西，
// 会让「谁负责关」在两个地方各有一个答案。
//
// 名字已经被占时返回 Code 为 [CodeDuplicateBackend] 的 *[Error]。
func (r *BackendRegistry) Register(name string, backend Backend) (func(), error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if _, exists := r.backends[name]; exists {
		return nil, newError(CodeDuplicateBackend, "后端名 %q 已经注册过了", name)
	}
	r.nextToken++
	token := r.nextToken
	r.backends[name] = registration{backend: backend, token: token}

	return func() { r.unregister(name, token) }, nil
}

// unregister 只摘掉**这一次注册**留下的那一条。
//
// 源: packages/storage/storage/src/registry.ts:31-36
//
// 认令牌而不是认名字，是为了防「过期的注销函数」：注销之后又重新注册了一个新后端，
// 此时那个旧的注销函数要是再被调一次（重复 defer、重试路径上多走了一遍），
// 它会把**继任者**摘掉。而那之后所有解析都会报「没注册」，看起来像后端根本没装上，
// 排查会从装配那头开始找，找不到。
//
// 新增: DSH 那边比的是后端对象本身（`backends.get(name) === backend`）。Go 里不能
// 照搬：Backend 是接口，而对不可比较的动态类型（切片、map）做 == 是运行期 panic——
// 一个注销函数把进程炸掉，比它该防的那个 bug 还严重。令牌是 uint64，永远可比，
// 而且比对象身份更准：同一个后端先后注册两次，两个注销函数也是分得开的。
func (r *BackendRegistry) unregister(name string, token uint64) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if current, exists := r.backends[name]; exists && current.token == token {
		delete(r.backends, name)
	}
}

// Get 按名字解析一个后端。
//
// 源: packages/storage/storage/src/registry.ts:39-53
//
// 找不到时返回 Code 为 [CodeBackendNotFound] 的 *[Error]，并把**已经注册的名字**
// 一并列出来。列出来是有用的：这类失败绝大多数是名字拼错或者装配顺序不对，
// 而两者都能靠「实际有哪些」一眼看出来。
func (r *BackendRegistry) Get(name string) (Backend, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	current, ok := r.backends[name]
	if !ok {
		return nil, newError(CodeBackendNotFound,
			"后端 %q 没有注册（已注册的：%s）", name, describeNames(r.sortedNames()))
	}
	return current.backend, nil
}

// Names 返回已注册的后端名，供诊断使用。
//
// 源: packages/storage/storage/src/registry.ts:55-61
//
// 新增: 按字典序排好再给。Go 的 map 遍历顺序是**故意随机**的，直接给出去的话,
// 同一个进程两次调用给出的顺序都可能不一样——诊断输出和测试断言都没法用。
func (r *BackendRegistry) Names() []string {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	return r.sortedNames()
}

// sortedNames 假定调用方已经持有锁。
func (r *BackendRegistry) sortedNames() []string {
	names := make([]string, 0, len(r.backends))
	for name := range r.backends {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// describeNames 把一串名字排成错误信息里那一段，空的时候给一个说得通的词。
//
// 空列表直接拼出来是一段空白，读的人分不清「一个都没有」和「这句话写漏了」。
func describeNames(names []string) string {
	if len(names) == 0 {
		return "无"
	}
	return strings.Join(names, ", ")
}
