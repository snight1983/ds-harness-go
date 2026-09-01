// Package settings 是「用户设置」这条接缝：后端存**一份原始文档**，按命名空间切成一段一段；
// 使用方登记一个命名空间的形状，读到的是**解析值**——依次叠三层：类型默认值、
// 登记方的组装层 base、用户文档里那一段。
//
// 源: packages/settings/settings/src/index.ts:1-7
//
// # 为什么是三层而不是一层
//
// 这三层各有各的作者，谁都不能替谁做主：
//
//   - **类型默认值**由拥有这个命名空间的那个模块写死（「超时默认 30 秒」）；
//   - **base** 由装配方给（这次部署把它调成 60 秒）；
//   - **用户段**由用户在配置界面上改（我要 90 秒）。
//
// 压成一层的后果是「重置」这个动作没有落点：用户删掉自己那条覆盖之后，
// 该退回到装配方给的值还是模块自己的默认值，答不上来。
// [Provider.Describe] 把三层分开交出去，正是为了让配置界面能标出「哪些字段是用户改过的」
// （出现在 User 里的那些）和「重置回哪里」。
//
// # 修订号数的是原始段，不是解析值
//
// 源: packages/settings/settings/src/index.ts:712-723
//
// [Descriptor.Revision] 在**存下来的那一段**变了的时候加一，哪怕解析值一个字节没动。
// 这一条是有意和 [Provider.SubscribeUpdated] 的等值判断分开的：
// 存一条和 base 完全相同的覆盖，解析值确实没变，但文档的**含义**变了——
// 那个字段从「继承来的」变成了「用户钉死的」，而这正是配置界面必须重读的那种变化。
//
// # 并发写靠队列，冲突靠修订号
//
// 源: packages/settings/settings/src/index.ts:577-648
//
// 同一个命名空间上的写是**串行**的，每一次都从「轮到它的那一刻」的段上重新算。
// 队列只保证次序，它分不出「一个刚读完就写的调用方」和「一个拿着过期快照的调用方」——
// 后者由调用方带上 expectedRevision 自己声明，对不上就是 [ConflictError]。
//
// 所以修订号核对**发生在队列前端**，不是在调用时刻：在调用时刻核对的话，
// 一个排在前面的写把段改掉之后，后面这个基于旧快照的写照样会通过。
//
// # Go 侧的几处不同
//
// 新增: DSH 那边是 `abstract class SettingsProvider extends Service`：抽象的三个
// （writable / load / persist）留给子类，其余全部写好。Go 没有实现继承，
// 拆法和本仓库 credentials 包一样是「抽象的做接口、写好的做具体类型」，
// 但这里是反过来的：抽象那三个是 [Backend]，写好的那一大堆落在 [Provider] 这个
// 具体结构体上，后端从构造函数递进去。
//
// 新增: DSH 的 schema 是 schemastery 的运行期 schema 对象，一个东西干四件事
// （默认值、校验、给 UI 的序列化、role('secret') 标记）。Go 里这四件事各有各的现成办法，
// 不需要把那套运行期 schema 搬过来：
//
//   - 默认值：登记时递一个填好的 T 值（[Register] 的 defaults 参数）；
//   - 校验：`encoding/json` 解码到 T，类型不对当场失败，再加登记方自己的 Validate；
//   - 给 UI 的序列化：[Options.Schema] 原样带过去，本包**不解释**它。
//     发明一套 Go 的 schema 反射器不在这条接缝的职责里——它要做的是把登记方声明的
//     那份文档送到配置界面手上，不是替登记方生成它；
//   - secret 标记：结构体标签 `settings:"secret"`，见 [Redact]。
//
// 新增: DSH 的观察者是异步的（每个回调一条串行的 promise 链）。Go 这边是**同步**的，
// 和本仓库 credentials 包的监听器同一个取舍：同步调用天然就是提交顺序，
// 而且比那条 promise 链的保证更强；代价是一个慢观察者会拖住写入方，
// 所以「不许阻塞、不许回头写」写进了 [Watcher] 的约定里。
package settings

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
)

// namespacePattern 是 [Namespace] 的语法：小写短横线，和插件短名同一套写法。
//
// 源: packages/settings/settings/src/index.ts:19
var namespacePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// 这条接缝自己会返回的几种错误。做成哨兵值是为了让调用方用 errors.Is 判定，
// 而不是去匹配错误文案——文案是给人看的，不该被当成接口。
var (
	// ErrInvalidNamespace 表示这个字符串不是一个合法的命名空间名。
	//
	// 源: packages/settings/settings/src/index.ts:28
	ErrInvalidNamespace = errors.New("settings: 命名空间名不合法")

	// ErrAlreadyRegistered 表示这个命名空间已经有主了。
	//
	// 源: packages/settings/settings/src/index.ts:437
	//
	// 重复登记必须响：两个模块把同一段配置当成自己的，谁读到的都不完整，
	// 而症状是「我配了但它没生效」，从配置那头查不出来。
	ErrAlreadyRegistered = errors.New("settings: 命名空间已经登记过了")

	// ErrNotRegistered 表示这个命名空间当前没有登记方。
	//
	// 源: packages/settings/settings/src/index.ts:587
	ErrNotRegistered = errors.New("settings: 命名空间没有登记")

	// ErrReadOnly 表示这个后端不接受写入。
	//
	// 源: packages/settings/settings/src/index.ts:593
	ErrReadOnly = errors.New("settings: 后端是只读的")

	// ErrStopped 表示服务已经关了，不再接受新的写入。
	//
	// 源: packages/settings/settings/src/index.ts:590,614
	ErrStopped = errors.New("settings: 服务已经关闭")

	// ErrNotJSON 表示一次写入的输入里有 JSON 存不下的东西。
	//
	// 源: packages/settings/settings/src/index.ts:607-608
	ErrNotJSON = errors.New("settings: 写入内容必须是 JSON 能表示的数据")

	// ErrMalformedSection 表示存下来的那一段不是一个对象。
	//
	// 源: packages/settings/settings/src/index.ts:691
	ErrMalformedSection = errors.New("settings: 存下来的这一段必须是键值对象")

	// ErrConflict 是 [ConflictError] 的哨兵面，供 errors.Is 使用。
	//
	// 源: packages/settings/settings/src/index.ts:164-183
	ErrConflict = errors.New("settings: 命名空间在读到之后被改过了")
)

// Namespace 是一个已登记设置段的标称 id。
//
// 源: packages/settings/settings/src/types.ts:14-15（SettingsNamespace）
//
// 新增: DSH 那边是 Branded<'SettingsNamespace'>，也就是用类型技巧给 string 造一个
// 不可互换的别名。Go 的具名类型天生就是标称类型，这件事不需要技巧。
//
// 代价和本仓库 credentials.Ref 那边一样：Go 的类型转换 Namespace("随便什么")
// 是免费且不校验的。所以本包凡是拿命名空间当**键**用的地方都不假设它合法，
// 只有 [Register] 这个入口挡一道——一个非法名字进不来，也就不会有段属于它。
type Namespace string

// NewNamespace 把一个原始字符串校验成 [Namespace]。
//
// 源: packages/settings/settings/src/index.ts:21-31
func NewNamespace(value string) (Namespace, error) {
	if !namespacePattern.MatchString(value) {
		return "", fmt.Errorf("%w：%q 必须匹配 %s", ErrInvalidNamespace, value, namespacePattern)
	}
	return Namespace(value), nil
}

// Applies 说明一个命名空间的改动**什么时候**对它的拥有者生效。
//
// 源: packages/settings/settings/src/index.ts:45-46（SettingsApplies）
//
// 这是给配置界面看的：改完就生效的字段和改完要重启的字段，界面上得给出不同的提示，
// 否则用户会以为自己改了没用。
type Applies string

const (
	// AppliesLive 表示提交之后拥有者立刻就用上了新值。
	AppliesLive Applies = "live"
	// AppliesRestart 表示要等拥有者重新起来才生效。
	AppliesRestart Applies = "restart"
)

// Source 是一次已提交变更的来路。
//
// 源: packages/settings/settings/src/types.ts:17-18（SettingsUpdateSource）
type Source string

const (
	// SourceUpdate 表示这次变更是从本进程的写入方法进来的。
	SourceUpdate Source = "update"
	// SourceProvider 表示这次变更是后端观察到的外部改动（有人直接编辑了存储）。
	SourceProvider Source = "provider"
)

// ConflictError 是一次因为「读到之后又被人改过」而被拒的写入。
//
// 源: packages/settings/settings/src/index.ts:149-173（SettingsConflictError）
//
// 串行写队列只排次序，它分不出一个刚读完就写的调用方和一个拿着过期快照的调用方——
// 那正是这个错误报告的事。
//
// 新增: DSH 那边靠 `code = 'SETTINGS_CONFLICT'` 这个稳定机器码让协议层做映射。
// Go 里同一件事由类型本身完成：协议层 errors.As 到 *ConflictError 就拿到了
// Expected 和 Actual 两个数，不需要再解析字符串。
// Unwrap 到 [ErrConflict] 是给只想问「是不是冲突」的调用方留的。
type ConflictError struct {
	// Namespace 是被拒的那个命名空间。
	Namespace Namespace
	// Expected 是调用方声明它读到的那个修订号。
	Expected uint64
	// Actual 是此刻真正的修订号。
	Actual uint64
}

// Error 实现 error。
//
// 源: packages/settings/settings/src/index.ts:178
func (e *ConflictError) Error() string {
	return fmt.Sprintf("settings: 命名空间 %q 在读到之后被改过了（声明的修订号 %d，现在是 %d）",
		string(e.Namespace), e.Expected, e.Actual)
}

// Unwrap 让 errors.Is(err, ErrConflict) 成立。
func (e *ConflictError) Unwrap() error { return ErrConflict }

// DeepEqualJSON 是这条接缝**唯一**的变化判定：JSON 形状数据上的深比较。
//
// 源: packages/settings/settings/src/index.ts:137-157
//
// 导出它不是为了方便，是为了让不变量检查（见 [RegisterInvariants]）
// 校验的恰好就是实现自己用的那个关系。两边各写一份深比较的话，
// 检查会在两份实现有出入的地方误报或漏报，而那正是最难看出来的一类。
//
// 新增: 不用 reflect.DeepEqual。它在 JSON 形状上会给出错的答案：
// json.Unmarshal 出来的数都是 float64，而调用方手写的字面量可能是 int；
// reflect.DeepEqual(1, 1.0) 是 false，于是一次「其实没变」会被判成变了，
// 进而发出一次假的提交通知。这里只认 JSON 那几种形状，数一律按 float64 比。
func DeepEqualJSON(a, b any) bool {
	switch left := a.(type) {
	case nil:
		return b == nil
	case map[string]any:
		right, ok := b.(map[string]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for key, value := range left {
			other, exists := right[key]
			if !exists || !DeepEqualJSON(value, other) {
				return false
			}
		}
		return true
	case []any:
		right, ok := b.([]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for index, value := range left {
			if !DeepEqualJSON(value, right[index]) {
				return false
			}
		}
		return true
	default:
		// 新增: 先问可比性再用 ==。Go 里对一个动态类型是切片或 map 的接口值做 ==
		// 是运行期 panic——而这是一个导出函数，一次传错类型不该把进程炸掉。
		// 判不出来的一律算「不等」：多发一次提交通知是可以忍的，
		// 漏发一次会让观察者从此和存储对不上，而且它永远不会知道。
		if a == nil || b == nil {
			return a == b
		}
		if !reflect.TypeOf(a).Comparable() || !reflect.TypeOf(b).Comparable() {
			return false
		}
		return a == b
	}
}
