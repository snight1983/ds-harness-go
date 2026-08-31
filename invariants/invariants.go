// Package invariants 提供「包自己拥有的运行期不变量」注册表。
//
// 对应 DSH 的 @deepseek-ai/dsh-invariants（packages/runtime-diagnostics/invariants）。
//
// 它解决的问题是：一个模块的正确性约束（「seq 必须严格递增」「tool 调用和结果必须配对」）
// 该由谁来查。写在使用方那里，每个使用方都要重写一遍且各写各的；写进模块自身的主流程，
// 主流程就被诊断代码污染，而且生产环境想关也关不掉。这个注册表的答案是：**由拥有该约束
// 的包自己注册一段检查，注册表负责挑选、隔离、和出错时的归位**。所以主入口不依赖诊断，
// 诊断也不需要改主入口。
//
// # 这里没有照抄的部分
//
// DSH 侧这个包大半篇幅在处理 cordis（它自研的依赖注入 / 插件框架）：Service 基类、
// ctx.effect() 的副作用登记、子 fiber 的发布与回滚、installer.inject 声明依赖。
// 这些在 Go 里都有更直接的对应物，照搬只会造一个更差的轮子：
//
//   - Service 基类 + 插件生命周期 → 普通结构体 + New() + Close()
//   - installer.inject 声明依赖    → 闭包捕获它需要的东西，依赖由构造函数传入
//   - schemastery 的运行期 schema  → Config 结构体 + New() 里的显式校验
//   - 「既能 await 又能 call」的双形态返回值（index.ts:44-47 那个
//     PendingInvariantRegistration）→ Go 直接返回 (dispose func(), err error)。
//     那个 hack 存在的唯一原因是 JS 没法一次返回两种东西，Go 有。
//
// 被保留下来的是**行为**：挑选规则、名字预留、失败归属、以及失败时的原子回滚。
//
// # 一处 Go 必须自己负责的差异
//
// DSH 是单线程 JS，注册表内部不需要任何并发保护。Go 里这个注册表会被多个 goroutine 碰到，
// 所以下面用互斥锁保护登记簿。这不是抄来的，是 Go 侧的必需品。
package invariants

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode"
)

// Code 是不变量违例的稳定机器可读代号。
//
// 源: packages/runtime-diagnostics/invariants/src/index.ts:51-52
//
// 之所以要有一个稳定代号而不是只靠错误文案：文案会被改、会被翻译，
// 而上层（日志聚合、告警规则）需要一个不随文案漂移的抓手。
const Code = "INVARIANT"

// Error 是一次不变量违例，带着**违反了谁的约定**这个信息。
//
// 源: packages/runtime-diagnostics/invariants/src/index.ts:49-66
//
// PackageName 是这条检查的注册方。它很重要：不变量是在别人的操作过程中被观察到的，
// 报错现场那一层往往和约定的拥有者不是同一个包。不带归属的话，
// 一条「seq must strictly increase」没人知道该去找谁。
type Error struct {
	// PackageName 是注册这条检查的完整包名。
	PackageName string
	// Message 是被违反的约定本身，不含统一前缀。
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("invariant violated by %q: %s", e.PackageName, e.Message)
}

// Fail 报告一次违例。它不返回——违例意味着后面的代码不该继续跑。
//
// 源: packages/runtime-diagnostics/invariants/src/index.ts:24-29
//
// # 为什么是 panic 而不是 return error
//
// DSH 侧 fail 的类型是 `(message: string) => never`，它 throw，异常沿调用栈一路穿到
// 触发这次观察的那个人手里（tests/service.spec.ts:189-208 钉的就是这个：
// ctx.emit 外面用 try/catch 才接得住）。
//
// Go 里能做到「不返回、并且沿栈上抛」的只有 panic。更重要的是语义上它就该是 panic：
// 不变量违例是**程序写错了**，不是一个可以处理的运行期状况。如果 Fail 返回 error，
// 调用方就可以把它丢掉——而一个可以被丢掉的不变量检查等于没有检查。
//
// panic 的值一定是 *Error，接的人用 errors.As 或类型断言取回归属。
type Fail func(message string)

// Scope 是一次注册所拥有的清理登记处。
//
// 源: packages/runtime-diagnostics/invariants/src/index.ts:153-188
//
// 它对应 DSH 里那个「子 fiber」：installer 装上去的东西（事件监听、后台观察）
// 都登记在这里，注销时统一撤销。
//
// 它必须存在的理由是一条被测试钉死的行为（tests/service.spec.ts:246-261）：
// installer **跑到一半失败**时，它在失败之前已经装上的监听器不许留下。
// 如果 installer 只在成功时返回一个清理函数，那半途失败的那些就泄漏了。
type Scope struct {
	mutex    sync.Mutex
	cleanups []func()
}

// Defer 登记一个清理动作。注销时按登记的**逆序**执行，和 Go 的 defer 一致——
// 后装上的东西可能依赖先装上的，拆的时候必须反着来。
func (s *Scope) Defer(cleanup func()) {
	if cleanup == nil {
		return
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.cleanups = append(s.cleanups, cleanup)
}

// unwind 执行并清空全部清理动作。执行过一次之后再调用是空操作，
// 这样「注销」和「注册失败时回滚」可以共用同一条路径而不会拆两遍。
func (s *Scope) unwind() {
	s.mutex.Lock()
	pending := s.cleanups
	s.cleanups = nil
	s.mutex.Unlock()

	for i := len(pending) - 1; i >= 0; i-- {
		pending[i]()
	}
}

// Installer 装上一个包的检查。
//
// 源: packages/runtime-diagnostics/invariants/src/index.ts:31-42
//
// 参数 fail 已经绑定了注册方的包名，所以 installer 里只写「什么约定被违反了」，
// 不用也不该自己拼归属——自己拼就可能拼错成别人。
//
// 返回 error 表示这次装载没成功。此时 scope 里已经登记的清理动作会被全部执行，
// 包名预留也会释放，就像这次注册从没发生过。
//
// DSH 侧 installer 可以带一个 inject 属性声明它需要哪些服务（index.ts:40-41）。
// Go 里不需要：installer 是闭包，它需要什么就在构造时捕获什么，依赖关系由编译器管。
type Installer func(ctx context.Context, scope *Scope, fail Fail) error

// Config 是注册表的挑选规则。
//
// 源: packages/runtime-diagnostics/invariants/src/index.ts:14-22
//
// 三者的关系：Enabled 是总开关；Allowlist 为空表示全放行，非空则必须命中其一；
// Blocklist 命中即排除，且**优先级高于** Allowlist（两个列表写同一条模式时，结果是排除）。
type Config struct {
	// Enabled 是总开关。nil 表示启用。
	//
	// 用指针而不是 bool，是因为这份配置要能从 JSON / YAML 读进来，
	// 而 Go 的普通 bool 分不清「配置里写了 false」和「配置里根本没写这一项」。
	// DSH 的默认值是 true（index.ts:115 的 `config.enabled ?? true`），
	// 用普通 bool 的话零值就是 false，等于把默认值反过来了。
	Enabled *bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`

	// PackageAllowlist 是准入的包名正则。空表示全放行。
	PackageAllowlist []string `json:"package_allowlist,omitempty" yaml:"package_allowlist,omitempty"`

	// PackageBlocklist 是排除的包名正则，在 Allowlist 命中之后再筛一道。
	PackageBlocklist []string `json:"package_blocklist,omitempty" yaml:"package_blocklist,omitempty"`
}

// 注册表会返回的几种错误。做成哨兵值是为了让调用方能用 errors.Is 判定，
// 而不是去匹配错误文案——文案是给人看的，不该被当成接口。
var (
	// ErrRegistryClosed 表示注册表已经关掉了，不再接受新的注册。
	// 源: packages/runtime-diagnostics/invariants/tests/service.spec.ts:304-309
	ErrRegistryClosed = errors.New("invariants: 注册表已关闭")
	// ErrAlreadyRegistered 表示这个包名已经有一份活着的注册。
	// 源: packages/runtime-diagnostics/invariants/src/index.ts:140-142
	ErrAlreadyRegistered = errors.New("invariants: 该包名已经注册过")
	// ErrInvalidPackageName 表示包名是空的、带首尾空白、或者中间有空白字符。
	// 源: packages/runtime-diagnostics/invariants/src/index.ts:137-139
	ErrInvalidPackageName = errors.New("invariants: 包名必须非空且不含空白字符")
	// ErrInvalidConfig 表示挑选规则里有空白项、重复项或编译不了的正则。
	// 源: packages/runtime-diagnostics/invariants/src/index.ts:74-91
	ErrInvalidConfig = errors.New("invariants: 挑选规则不合法")
)

// Registry 是包级不变量的注册表。用 New 构造。
type Registry struct {
	enabled   bool
	allowlist []*regexp.Regexp
	blocklist []*regexp.Regexp

	// mutex 保护下面两个字段。
	//
	// 新增: DSH 是单线程 JS，注册表内部没有任何并发保护；Go 里注册和注销会来自
	// 不同的 goroutine，所以登记簿必须自己上锁。这一层不是抄来的。
	mutex   sync.Mutex
	closed  bool
	entries map[string]struct{}
}

// New 构造并校验一个注册表。
//
// 校验在这里一次做完而不是留到注册时，对应 DSH 的「畸形配置让服务启动失败」
// （tests/service.spec.ts:145-160）。理由是：一条编译不了的正则如果被静默忽略，
// 那么「我配了白名单」和「我的白名单没生效」在现象上完全一样，
// 而后者意味着一整批检查悄悄没跑。宁可起不来。
func New(config Config) (*Registry, error) {
	allowlist, err := compilePatterns("package_allowlist", config.PackageAllowlist)
	if err != nil {
		return nil, err
	}
	blocklist, err := compilePatterns("package_blocklist", config.PackageBlocklist)
	if err != nil {
		return nil, err
	}

	enabled := true
	if config.Enabled != nil {
		enabled = *config.Enabled
	}

	return &Registry{
		enabled:   enabled,
		allowlist: allowlist,
		blocklist: blocklist,
		entries:   map[string]struct{}{},
	}, nil
}

// compilePatterns 编译并校验一个筛选列表。
//
// 源: packages/runtime-diagnostics/invariants/src/index.ts:74-91
//
// 三种拒绝理由分开报，因为它们的修法不一样：空白项是配置写漏了，
// 重复项是配置写重了，编译不了是正则本身有问题。
//
// 新增: Go 的 regexp 是 RE2，不支持 JS 正则的前后瞻和反向引用。
// 一条用了前瞻的模式在这里会**编译失败并让启动中止**，而不是静默失效——
// 这个差异会响，不会藏，所以是可接受的。
func compilePatterns(field string, values []string) ([]*regexp.Regexp, error) {
	seen := make(map[string]struct{}, len(values))
	compiled := make([]*regexp.Regexp, 0, len(values))
	for _, value := range values {
		if value == "" || strings.TrimSpace(value) != value {
			return nil, fmt.Errorf("%w：%s 的每一项都必须非空且不带首尾空白，%q 不满足",
				ErrInvalidConfig, field, value)
		}
		if _, duplicated := seen[value]; duplicated {
			return nil, fmt.Errorf("%w：%s 里有重复的正则 %q",
				ErrInvalidConfig, field, value)
		}
		seen[value] = struct{}{}

		pattern, err := regexp.Compile(value)
		if err != nil {
			return nil, fmt.Errorf("%w：%s 里的正则 %q 编译不了：%w",
				ErrInvalidConfig, field, value, err)
		}
		compiled = append(compiled, pattern)
	}
	return compiled, nil
}

// selected 判断一个包名是否通过了挑选规则。
//
// 源: packages/runtime-diagnostics/invariants/src/index.ts:120-126
func (r *Registry) selected(packageName string) bool {
	if !r.enabled {
		return false
	}
	if len(r.allowlist) > 0 && !matchesAny(r.allowlist, packageName) {
		return false
	}
	return !matchesAny(r.blocklist, packageName)
}

func matchesAny(patterns []*regexp.Regexp, value string) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(value) {
			return true
		}
	}
	return false
}

// Register 登记一个包的不变量检查，返回注销函数。
//
// 源: packages/runtime-diagnostics/invariants/src/index.ts:128-197
//
// 三条容易被忽略但都是有意的行为：
//
//  1. **包名一律预留，即使挑选规则把这次检查关掉了。** 这样「检查被过滤掉了」
//     和「这个包还没注册」不会混淆，重复注册在开关关着的时候同样会报错
//     （tests/service.spec.ts:80-89）。
//
//  2. **装载失败时整体归位。** installer 在失败前已经登记进 scope 的清理动作会被执行，
//     包名预留会释放，就像这次注册没发生过。半装上的检查比没有检查更坏——
//     它会在一个不完整的视角上误报。
//
//  3. **注销之后可以用同一个包名重新注册。** DSH 那边这条是给热重载用的；
//     Go 侧没有热重载，但它同样是「注销确实拆干净了」的证据，所以行为保留。
//
// 返回的注销函数是幂等的，多调几次不会重复拆。
func (r *Registry) Register(ctx context.Context, packageName string, install Installer) (func(), error) {
	// DSH 那边分三个条件写（非空、首尾无空白、中间无 \s，index.ts:137），
	// 但「不含任何空白字符」已经把后两条都盖住了，所以这里合成一条。
	if packageName == "" || strings.ContainsFunc(packageName, unicode.IsSpace) {
		return nil, fmt.Errorf("%w：%q", ErrInvalidPackageName, packageName)
	}
	if install == nil {
		return nil, fmt.Errorf("%w：%s 没有提供 installer", ErrInvalidPackageName, packageName)
	}

	if err := r.reserve(packageName); err != nil {
		return nil, err
	}

	// 挑选规则把这次检查关掉了：名字照样占着，但一行检查都不装。
	if !r.selected(packageName) {
		return r.releaseOnce(packageName, nil), nil
	}

	scope := &Scope{}
	fail := Fail(func(message string) {
		panic(&Error{PackageName: packageName, Message: message})
	})

	if err := install(ctx, scope, fail); err != nil {
		scope.unwind()
		r.release(packageName)
		return nil, fmt.Errorf("invariants: %s 的不变量检查装载失败：%w", packageName, err)
	}

	return r.releaseOnce(packageName, scope), nil
}

// releaseOnce 造一个幂等的注销函数：先拆 scope，再释放包名预留。
//
// 顺序不能反。反过来的话，在拆的过程中包名已经空出来了，
// 另一个 goroutine 可以趁这个窗口用同一个名字注册进来，
// 于是新旧两份检查会短暂共存——这正是 DSH 那条
// 「预留持续到子 fiber 异步注销完成为止」（tests/service.spec.ts:226-244）要防的事。
func (r *Registry) releaseOnce(packageName string, scope *Scope) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			if scope != nil {
				scope.unwind()
			}
			r.release(packageName)
		})
	}
}

func (r *Registry) reserve(packageName string) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if r.closed {
		return fmt.Errorf("%w：%s", ErrRegistryClosed, packageName)
	}
	if _, taken := r.entries[packageName]; taken {
		return fmt.Errorf("%w：%s", ErrAlreadyRegistered, packageName)
	}
	r.entries[packageName] = struct{}{}
	return nil
}

func (r *Registry) release(packageName string) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	delete(r.entries, packageName)
}

// Registered 返回当前占着名额的包名，顺序不保证。用来做诊断和测试。
//
// 新增: DSH 侧没有这个方法，它的测试靠「再注册一次看报不报 already registered」
// 来间接观察预留状态。给一个直接的读法，是因为那种间接观察会**改变**被观察的东西——
// 试探性注册在成功时会真的占住名额。
func (r *Registry) Registered() []string {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	names := make([]string, 0, len(r.entries))
	for name := range r.entries {
		names = append(names, name)
	}
	return names
}

// Close 关掉注册表，之后 Register 一律返回 ErrRegistryClosed。
//
// 源: packages/runtime-diagnostics/invariants/tests/service.spec.ts:304-309
//
// 它**不**替已有的注册做注销：那些注销函数在注册方自己手里，
// 由谁装谁拆。注册表越权去拆，装的人就再也无法确定自己的清理动作何时跑过。
func (r *Registry) Close() {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.closed = true
}
