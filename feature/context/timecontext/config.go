// 本文件的作用：本包的哨兵错误，以及那两个配置项和它们的补默认值、校验规则。
//
// 源: packages/context/time-context/src/index.ts:20-38

package timecontext

import (
	"errors"
	"fmt"
	"time"
)

// PluginName 是这一层往会话日志里写读数时署的名字。
//
// 源: packages/context/time-context/src/index.ts:21
//
// 它落进 llm.PluginSource 的 Plugin 字段和快照分节的名字里，是**介质上的字节**：
// 改了它，历史日志里已有的读数就再也认不出是本包写的，不变量会把它们全判成
// 别人伪造的。所以取值和 DSH 一个字不差。
const PluginName = "time-context"

// DefaultTimeZone 是没给时区时用的那个。
//
// 新增: DSH 缺省用宿主机进程的时区（`resolvedOptions().timeZone`）。Go 这边
// 没有那个东西可用：[time.Local] 的 `String()` 是字面量 "Local"，不是一个
// IANA 名字，而这个名字要**原样进提示词**、还要被不变量重新解析。加上
// docs/DESIGN.md 第二节那条——服务端没有「当前用户」，也就没有「本机时区」
// 这个有意义的概念——缺省定成 UTC：它永远解析得开，而且是个真名字。
const DefaultTimeZone = "UTC"

// 本包会报的三类失败。做成哨兵值是为了让调用方用 errors.Is 判定，
// 而不是去匹配错误文案。
//
// 新增: DSH 那边这三类分别是 `TypeError`、解码时的自然抛出、和不变量的
// `fail()`。Go 里没有异常，三条路都要走返回值，所以给它们各一个可判定的头。
var (
	// ErrInvalidConfig 表示 [Config] 里的值不合法，或者时区解析不开。
	ErrInvalidConfig = errors.New("timecontext: 配置不合法")
	// ErrMalformedEvent 表示日志里某条事件的负载读不回来。
	ErrMalformedEvent = errors.New("timecontext: 事件负载读不回来")
	// ErrInvariantViolated 表示一条时间读数不满足本包自己拥有的那条不变量。
	ErrInvariantViolated = errors.New("timecontext: 时间读数违反了本包的不变量")
)

// Config 是这一层的两个配置项。
//
// 源: packages/context/time-context/src/index.ts:27-32
//
// 新增: DSH 的 `refreshIntervalMs` 是一个毫秒数，配上一整套
// `Number.isSafeInteger` 校验。Go 这边是 [time.Duration]：整数性和量纲都由
// 类型系统承担，运行期只剩「不许是负数」这一条。
type Config struct {
	// TimeZone 是读数显示用的 IANA 时区名；空取 [DefaultTimeZone]。
	//
	// 新增: DSH 这个字段的定位是「浏览器时区取不到时的兜底」，真正用的是
	// 每条用户消息上由 Host 带上来的 `clientTimeZone`。docs/DESIGN.md 第三节
	// 明写了这里的改法：**改成由消费方传时区**——服务端没有「当前用户的浏览器」。
	// 于是这个字段从兜底变成了唯一的来源。
	TimeZone string
	// RefreshInterval 是同一个会话里两次落库读数之间的最小间隔；
	// 零或负数表示每一个够格的步骤都注入。
	RefreshInterval time.Duration
}

// ResolvedConfig 是补完默认值、验过之后的那份配置，本包内部只读它。
//
// 新增: 和 context/sessionref 那份解析后配置同一个理由——构造它的唯一入口是
// [Config.Resolve]，一份没验过的配置在类型上就传不进来。这里还多带一个
// 解析好的 [time.Location]：时区名解析一次就够了，每条读数都去
// LoadLocation 一遍是白花的系统调用。
type ResolvedConfig struct {
	// TimeZone 是补完默认值之后的 IANA 时区名。
	TimeZone string
	// Location 是 TimeZone 解析出来的时区，非 nil。
	Location *time.Location
	// RefreshInterval 非负；零表示不做节流。
	RefreshInterval time.Duration
}

// Resolve 补上默认值并校验。
//
// 源: packages/context/time-context/src/index.ts:127-158
//
// DSH 在 `apply` 里做这两件事，失败就让插件装不上。Go 里没有那个装载时机，
// 所以挪到构造配置的地方——但性质一样：验不过就没有一份可用的配置，
// 而不是留一个「时区错着照样往提示词里写」的运行期。
func (c Config) Resolve() (ResolvedConfig, error) {
	if c.RefreshInterval < 0 {
		return ResolvedConfig{}, fmt.Errorf("%w：refreshInterval 不得为负，给的是 %s",
			ErrInvalidConfig, c.RefreshInterval)
	}

	name := c.TimeZone
	if name == "" {
		name = DefaultTimeZone
	}
	if name == "Local" {
		// [time.LoadLocation] 认这个名字并交出 [time.Local]，而它的 String()
		// 就是 "Local"。那三个字节会原样进提示词，模型读到的是一个它无法解释
		// 的时区名；不变量那边也再也重现不出同一条读数。理由同 [DefaultTimeZone]。
		return ResolvedConfig{}, fmt.Errorf("%w：时区必须是一个 IANA 名字，%q 说的是宿主机本地时区",
			ErrInvalidConfig, name)
	}

	location, err := time.LoadLocation(name)
	if err != nil {
		// Go 的时区库来自宿主机的 zoneinfo，容器里常常整个不存在。
		// 这一句要把修法说出来，否则一条 "unknown time zone" 会被当成配置写错了。
		return ResolvedConfig{}, fmt.Errorf(
			"%w：时区 %q 解析不开（部署里要带 zoneinfo，或者在 main 包 import _ \"time/tzdata\"）：%w",
			ErrInvalidConfig, name, err)
	}

	return ResolvedConfig{
		TimeZone:        name,
		Location:        location,
		RefreshInterval: c.RefreshInterval,
	}, nil
}
