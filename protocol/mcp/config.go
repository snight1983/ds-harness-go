// 本文件的作用：一台 MCP 服务器的配置、它那份重连策略，以及把一份随手写的策略
// 解算成监督者真正会跑的那份策略的那一步。
//
// 源: packages/mcp/mcp-client/src/index.ts:47-128
// 源: packages/mcp/mcp-client/src/connection.ts:27-90

package mcp

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

// ErrInvalidConfig 表示配置本身不成立，连接被拒。
var ErrInvalidConfig = errors.New("mcp: 配置不成立")

// DefaultToolCallTimeout 是单次 MCP 工具调用的默认超时。
//
// 源: packages/mcp/mcp-client/src/index.ts:34
const DefaultToolCallTimeout = 60 * time.Second

// serverNamePattern 是一个合法服务器名的形状，卡在公开工具名的字符预算之下。
//
// 源: packages/mcp/mcp-client/src/index.ts:37
var serverNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// ReconnectConfig 是一台 MCP 服务器断线之后的自动重连策略。
//
// 源: packages/mcp/mcp-client/src/connection.ts:27-37（ReconnectConfig）
//
// 新增: DSH 那四个字段全是可选的，`enabled` 缺省为**真**。Go 的零值是 false，
// 照抄会让一份没填的配置把重连悄悄关掉，所以这里取反成 Disabled——零值就等于
// 「开着」，和 DSH 的默认行为对齐。做法和
// [github.com/snight1983/ds-harness-go/feature/interaction/commands.Definition.SkipInputRecord] 逐字相同。
// 三个数值字段的零值同样表示「没填」，由 [resolveReconnectPolicy] 补默认值。
type ReconnectConfig struct {
	// Disabled 为真时断了就不再重连。
	Disabled bool
	// InitialDelay 是第一次重连前等多久，每次连续失败翻一倍；零表示用默认值 500ms。
	InitialDelay time.Duration
	// MaxDelay 是退避的上限；它同时是「连上多久算这次断线事件结束」的稳定窗口。
	// 零表示用默认值 30s。
	MaxDelay time.Duration
	// MaxAttempts 是一次断线事件里连续失败多少次之后彻底放弃；零表示用默认值 10。
	MaxAttempts int
}

// 重连策略的三个默认值。
//
// 源: packages/mcp/mcp-client/src/connection.ts:40-45
const (
	defaultInitialDelay = 500 * time.Millisecond
	defaultMaxDelay     = 30 * time.Second
	defaultMaxAttempts  = 10
)

// ReconnectPolicy 是解算完的重连策略，监督者照着它跑。
//
// 源: packages/mcp/mcp-client/src/connection.ts:52-53（ResolvedReconnectPolicy）
type ReconnectPolicy struct {
	// Enabled 表示断了要不要重连。
	Enabled bool
	// InitialDelay 是第一次重连前等多久。
	InitialDelay time.Duration
	// MaxDelay 是退避上限，同时是稳定窗口。
	MaxDelay time.Duration
	// MaxAttempts 是一次断线事件里的失败预算。
	MaxAttempts int
}

// resolveReconnectPolicy 把一份随手写的重连配置解算成监督者跑的那份策略。
//
// 源: packages/mcp/mcp-client/src/connection.ts:65-90
//
// 每一条边界都在这里重判一遍，配错了就让**这一次连接**在建立之前失败。
//
// 新增: DSH 头一件事是遍历配置对象的键、拒绝表外的键——那是因为一个 JS 调用方
// 递得进任何东西。Go 的结构体写不出表外的字段，那一条不用验。
//
// 新增: DSH 还要求两个延迟不超过 MAX_TIMER_DELAY_MS。那是 JS 的 setTimeout 把延迟
// 截成 32 位有符号整数这一件实现细节，Go 的 [time.Timer] 没有这个坎，所以那条上界
// 在这里不存在。
func resolveReconnectPolicy(config ReconnectConfig, path string) (ReconnectPolicy, error) {
	policy := ReconnectPolicy{
		Enabled:      !config.Disabled,
		InitialDelay: config.InitialDelay,
		MaxDelay:     config.MaxDelay,
		MaxAttempts:  config.MaxAttempts,
	}
	if policy.InitialDelay == 0 {
		policy.InitialDelay = defaultInitialDelay
	}
	if policy.MaxDelay == 0 {
		policy.MaxDelay = defaultMaxDelay
	}
	if policy.MaxAttempts == 0 {
		policy.MaxAttempts = defaultMaxAttempts
	}
	if policy.InitialDelay < 0 {
		return ReconnectPolicy{}, fmt.Errorf("%w: %s.InitialDelay 不能是负数", ErrInvalidConfig, path)
	}
	if policy.MaxDelay < 0 {
		return ReconnectPolicy{}, fmt.Errorf("%w: %s.MaxDelay 不能是负数", ErrInvalidConfig, path)
	}
	if policy.InitialDelay > policy.MaxDelay {
		return ReconnectPolicy{}, fmt.Errorf("%w: %s.InitialDelay 不能大于 MaxDelay", ErrInvalidConfig, path)
	}
	if policy.MaxAttempts < 0 {
		return ReconnectPolicy{}, fmt.Errorf("%w: %s.MaxAttempts 不能是负数", ErrInvalidConfig, path)
	}
	return policy, nil
}

// Config 是一台走 Streamable HTTP 的 MCP 服务器的配置。
//
// 源: packages/mcp/mcp-client/src/index.ts:75-95（StreamableHttpConfig）
//
// 新增: DSH 这里是 StdioConfig | StreamableHttpConfig 的联合。stdio 那一支随子进程
// 那一块一起不移（见包注释），所以联合塌成了一个结构体。
type Config struct {
	// ServerName 是这台服务器在本地的稳定命名空间，公开工具名
	// `mcp__<ServerName>__<原名>` 拿它打头。必须匹配 `[A-Za-z0-9_-]{1,32}`，
	// 并且在同一个 [Host] 上唯一。
	ServerName string
	// URL 是 MCP 端点。
	URL string
	// Headers 是挂在每一次 MCP 请求上的额外请求头。
	Headers map[string]string
	// ToolCallTimeout 是单次工具调用的超时；零表示 [DefaultToolCallTimeout]。
	ToolCallTimeout time.Duration
	// FailOnStartupError 为真时，首次连接或者首次工具同步失败会让
	// [Host.Connect] 返回错误（连接已经清理干净）。
	FailOnStartupError bool
	// Reconnect 是断线之后的自动重连策略；零值表示全用默认值。
	Reconnect ReconnectConfig
}

// validate 验一份配置，并且把零值补成默认值。
func (c Config) validate() (Config, error) {
	if !serverNamePattern.MatchString(c.ServerName) {
		return Config{}, fmt.Errorf("%w: ServerName %q 必须匹配 %s",
			ErrInvalidConfig, c.ServerName, serverNamePattern)
	}
	if c.URL == "" {
		return Config{}, fmt.Errorf("%w: mcp-client(%s) 需要一个 URL", ErrInvalidConfig, c.ServerName)
	}
	if c.ToolCallTimeout < 0 {
		return Config{}, fmt.Errorf("%w: mcp-client(%s) 的 ToolCallTimeout 不能是负数",
			ErrInvalidConfig, c.ServerName)
	}
	if c.ToolCallTimeout == 0 {
		c.ToolCallTimeout = DefaultToolCallTimeout
	}
	return c, nil
}
