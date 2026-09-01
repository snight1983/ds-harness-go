// Package credentials 是「凭据引用」这条接缝：配置文件里只写**引用**（环境变量名），
// 真正的值和它的存储由提供方持有。
//
// 源: packages/credentials/credentials/src/index.ts:1-9
//
// # 为什么配置里放的是引用而不是值
//
// 消费方**每次操作都重新解析一遍**引用。这一条不是性能取舍，而是这条接缝存在的理由：
// 换掉一个密钥之后，下一次操作就用上了新值，不需要重启任何插件；
// 而配置界面能描述一个引用（配没配、来自哪层、能不能改），全程见不到值本身。
//
// # 两套键空间回答两个不同的问题
//
// 源: packages/credentials/credentials/src/index.ts:161-176
//
// [Ref] 回答「这个环境变量名背后是什么」。它是**分层**的：进程环境、提供方自己的存储、
// .env 文件，一层压一层。这一半有一条贯穿接缝的规则——**存下来的空值等于没有**：
// [Provider.Resolve] 跳过它，[Provider.Describe] 报未配置。少了这条，
// 一个被清空但没删掉的条目会冒充一个配好的密钥，而它的症状是「配置界面说配好了、
// 调用时报未授权」，最难查的那一类。
//
// [Key] 回答「这个插件为这个 id 持有什么凭据」。这一半**没有分层可言**——
// 一次授权换来的令牌没有「环境」可以读，所以记录在不在就是全部事实。
// 也因此 [Provider.ModifyRecord] 是唯一的写入路径：一次正确的写依赖当前值
// （刷新令牌是「读—决定—替换」，必须在一把锁里做完）。
//
// # 抽象类怎么变成了接口加一个通知器
//
// 新增: DSH 那边是 `abstract class CredentialProvider extends Service`：九个抽象方法
// 留给子类，另外三个（notifyUpdated / notifyRecordUpdated / fanOut）已经写好，
// 子类继承就自动拿到。Go 没有实现继承。
//
// 对应做法和本仓库 attachment 包一样是把两半拆开，但拆法不同：
// 抽象的九个是 [Provider] 接口；已经写好的那三个**不能**做成包级函数，
// 因为它们背后有状态（一张订阅表），而包级函数存不下状态。
// 所以它们落在 [Notifier] 这个具体结构体上，提供方**内嵌**它就自动拿到全部能力。
//
// 内嵌在这里是安全的，和 attachment 那边拒绝内嵌的理由并不冲突：attachment 要护住的是
// 一批图的**次序规则**——那是行为，子类覆盖掉就等于绕过校验，所以做成包级函数；
// 这里要共享的是一张活着的订阅表——那是状态，内嵌是 Go 里唯一自然的共享法。
//
// # 内嵌把「protected」变成了公开，这是有代价的
//
// DSH 的 notifyUpdated 是 protected，只有子类能调。Go 没有 protected，
// 内嵌会把 [Notifier.NotifyReferenceUpdated] 一起提升成提供方的公开方法，
// 于是任何消费方都能伪造一次通知。
//
// 本仓库已经有过同一处取舍并给了同一个答案，见 typert 里 ClientRemote.Dispatch
// 的注释：**这是提供方调的，不是消费方调的**；消费方用 [Observer] 订阅，永远不该调它。
// 靠文档而不是靠编译器，是因为把它藏起来的每一种写法都要求提供方多写一层转发，
// 而那层转发写漏了的症状是「变更提交了但没人收到通知」——比伪造通知更难查。
//
// # 关于 context
//
// 新增: DSH 的九个方法都是 Promise，没有 AbortSignal。Go 这边全部收 context.Context。
// 理由和 attachment 包记的那条一样：取消能力在 Go 里是传染的，接口方法上没有 ctx，
// 实现方内部再想把取消传给密钥管理服务的 HTTP 客户端或数据库驱动就没有来源。
// 一次连不上远端密钥库的 Resolve 会把调用方的 goroutine 一直占着，正是这条接缝
// 最容易出的问题。
package credentials

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// refPattern 是 [Ref] 的语法：POSIX shell 标识符。
//
// 源: packages/credentials/credentials/src/index.ts:16
var refPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// keySegmentPattern 是 [Key] 两段各自的语法。
//
// 源: packages/credentials/credentials/src/index.ts:18-19
//
// 它和 [refPattern] 之间**没有交集**这件事不是巧合：[Key] 的两段中间夹一个 `/`，
// 而 `/` 落在 [refPattern] 之外，于是一个 Key 永远不可能被误读成一个 Ref。
// 两套键空间共用一个提供方，撞在一起的后果是一边的写盖掉另一边的读。
var keySegmentPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// 这条接缝自己会返回的几种错误。做成哨兵值是为了让调用方用 errors.Is 判定，
// 而不是去匹配错误文案——文案是给人看的，不该被当成接口。
var (
	// ErrInvalidRef 表示这个字符串不是一个合法的凭据引用名。
	//
	// 源: packages/credentials/credentials/src/index.ts:28
	ErrInvalidRef = errors.New("credentials: 凭据引用名不合法")

	// ErrInvalidKey 表示这个字符串不是一个合法的凭据记录地址。
	//
	// 源: packages/credentials/credentials/src/index.ts:69,86
	ErrInvalidKey = errors.New("credentials: 凭据记录地址不合法")
)

// Ref 是一条凭据的标称引用：一个 POSIX 风格的环境变量名。
//
// 源: packages/credentials/credentials/src/types.ts:13-14（CredentialRef）
//
// 新增: DSH 那边是 Branded<'CredentialRef'>，也就是用类型技巧给 string 造一个
// 不可互换的别名。Go 的具名类型天生就是标称类型，这件事不需要技巧。
//
// 代价是 Go 的类型转换 Ref("随便什么") 是**免费且不校验**的，而 TS 的品牌只能由
// [NewRef] 加出来。所以本包里凡是读 Ref 内部结构的地方（见 [Key.Scope]）
// 都写成不会崩的全函数，不依赖「它一定是构造出来的」这个前提。
type Ref string

// Key 是一条持久凭据记录的标称地址：`<scope>/<id>`。
//
// 源: packages/credentials/credentials/src/types.ts:16-29（CredentialKey）
//
// scope 是**拥有这条记录的插件**的注册名，id 是那个插件自己的寻址单位
// （一个 LLM 适配器用它的路由键）。
//
// scope 取「拥有者」而不是「领域」是有意的：记录的载荷是按拥有者的格式写的。
// 取领域的话，两个服务同一家供应商的插件会去读对方的载荷；而一个已卸载插件留下的
// 孤儿记录，也再没有办法和一条还在用的记录区分开——配置界面只能把它当成正常凭据展示。
type Key string

// NewRef 把一个原始字符串校验成 [Ref]。
//
// 源: packages/credentials/credentials/src/index.ts:24-34（credentialRef）
//
// 新增: DSH 抛 TypeError，Go 返回 error。名字从别处来（供应商库自己的环境发现、
// 某个钩子的载荷）的调用方不该靠接住一个错误来判断，那种情况先问 [IsRefName]。
func NewRef(value string) (Ref, error) {
	if !IsRefName(value) {
		return "", fmt.Errorf("%w：%q 必须匹配 %s", ErrInvalidRef, value, refPattern)
	}
	return Ref(value), nil
}

// IsRefName 回答一个原始字符串**有没有可能**是一个引用名。
//
// 源: packages/credentials/credentials/src/index.ts:36-47（isCredentialRefName）
//
// 环境变量名从别处拿到的消费方在解析之前问这一句：语法之外的名字压根没有引用可以对应，
// 它该读成「没配置」，而不是读成一次抛出来的错误。两者的区别是后者会让一条
// 「这个供应商用环境里的默认凭据」的正常路径在日志里表现成故障。
func IsRefName(value string) bool {
	return refPattern.MatchString(value)
}

// IsKeySegment 回答一个原始字符串**有没有可能**当 [NewKey] 的一段。
//
// 源: packages/credentials/credentials/src/index.ts:49-60（isCredentialKeySegment）
//
// 理由同 [IsRefName]：寻址单位从别处来（一份配置字典的键、某个库自己的供应商 id）的
// 消费方在拼键之前问这一句。语法之外的单位不可能存过记录，它该读成「什么都没存」。
func IsKeySegment(value string) bool {
	return keySegmentPattern.MatchString(value)
}

// NewKey 把一个 scope 和一个 id 校验并拼成 [Key]。
//
// 源: packages/credentials/credentials/src/index.ts:62-76（credentialKey）
//
// scope 是拥有方插件的注册名（如 llm-pi-ai），id 是那个插件自己的寻址单位。
func NewKey(scope, id string) (Key, error) {
	for _, segment := range []string{scope, id} {
		if !keySegmentPattern.MatchString(segment) {
			return "", fmt.Errorf("%w：段 %q 必须匹配 %s", ErrInvalidKey, segment, keySegmentPattern)
		}
	}
	return Key(scope + "/" + id), nil
}

// ParseKey 把一个已经拼好的 `<scope>/<id>` 字符串校验成 [Key]。
//
// 源: packages/credentials/credentials/src/index.ts:78-92（parseCredentialKey）
//
// 这是 [NewKey] 的读取面，给「从磁盘上把键读回来」的提供方用。
// 分不出恰好两段就拒绝：三段的字符串取前两段会静默指向另一条记录。
func ParseKey(value string) (Key, error) {
	scope, id, found := strings.Cut(value, "/")
	if !found || strings.Contains(id, "/") {
		return "", fmt.Errorf("%w：%q 必须是 \"<scope>/<id>\"", ErrInvalidKey, value)
	}
	return NewKey(scope, id)
}

// Scope 取出这条记录的拥有方插件名。
//
// 源: packages/credentials/credentials/src/index.ts:94-105（credentialKeyScope）
//
// scope 指向一个当前没有注册的插件时，这条记录就是**孤儿**。配置界面必须照孤儿去报，
// 而不是当成一条能用的凭据——后者会让用户以为自己已经授权过了。
//
// 新增: DSH 是包级函数 credentialKeyScope(key)，Go 里做成方法，
// 因为 [Key] 是本包的具名类型，方法是 Go 表达「这是这个类型上的读取」的自然写法。
//
// 新增: DSH 敢直接 slice，因为品牌类型只有两个都校验过两段的构造函数。
// Go 的类型转换绕得过构造函数，所以这里用 strings.Cut 写成全函数：
// 一个手工转出来的 Key("garbage") 得到 "garbage" 和 ""，而不是一次下标越界的 panic。
func (k Key) Scope() string {
	scope, _, _ := strings.Cut(string(k), "/")
	return scope
}

// ID 取出拥有方插件自己的寻址单位——键里由那个插件挑的那一半。
//
// 源: packages/credentials/credentials/src/index.ts:107-115（credentialKeyId）
//
// 越界处理同 [Key.Scope]。
func (k Key) ID() string {
	_, id, _ := strings.Cut(string(k), "/")
	return id
}
