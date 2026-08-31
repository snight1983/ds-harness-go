// 本文件的作用：这个包要接哪几样东西才转得起来，以及那个把它们攥在手上的控制器。
//
// 源: packages/session-query/tool-session-query/src/index.ts:17-56,124-137

package querytool

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"ds-harness-go/core/agent"
	"ds-harness-go/core/scope"
	"ds-harness-go/session"
	"ds-harness-go/sessionquery"
)

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/session-query/tool-session-query/src/invariant.ts:13
const PackageName = "@deepseek-ai/dsh-tool-session-query"

// DefaultMaxSearchResults 是一次检索最多交回多少条已授权的命中。
//
// 源: packages/session-query/tool-session-query/src/index.ts:23
const DefaultMaxSearchResults = 100

// DefaultSearchTimeout 是两件检索工具的默认协作式截止时间。
//
// 源: packages/session-query/tool-session-query/src/index.ts:26
const DefaultSearchTimeout = 30 * time.Second

// Service 是本包用得到的那一小块读引擎。
//
// 新增: DSH 直接注入整个 SessionQueryEngine。这里只写出真正被调到的那七个方法，
// 装配方交进来的 [ds-harness-go/sessionquery.Engine] 自然满足它。窄口子的好处是
// 测试里不必替身一整台引擎，也让「本包到底看得见什么」这件事从签名上一眼可读。
type Service interface {
	// FilterSessions 按会话元数据筛整份语料，授权就是靠它做的。
	FilterSessions(ctx context.Context, filters []sessionquery.SessionFilter) ([]sessionquery.Record, error)
	// SearchSessions 跨会话检索。
	SearchSessions(ctx context.Context, request sessionquery.SearchRequest) (sessionquery.SearchPage[sessionquery.SearchHit], error)
	// SearchEvents 在一个会话内检索。
	SearchEvents(ctx context.Context, request sessionquery.EventSearchRequest) (sessionquery.EventSearchPage, error)
	// TraceSession 追一个会话的血统。
	TraceSession(ctx context.Context, id session.SessionID) (sessionquery.LineageTrace, error)
	// TraceEvent 追一条事件的取代与派生关系。
	TraceEvent(ctx context.Context, request sessionquery.EventTraceRequest) (sessionquery.EventTraceObservation, error)
	// ReadEvent 精读一条事件，外带前后若干条的摘要。
	ReadEvent(ctx context.Context, request sessionquery.EventReadRequest) (sessionquery.EventWindow, error)
	// ReadTitleSnapshots 批量折出标题，列表行的显示名靠它。
	ReadTitleSnapshots(ctx context.Context, ids []session.SessionID) ([]sessionquery.ProjectionResult[sessionquery.TitleObservation], error)
}

// Config 是这个包的装配面。
//
// 源: packages/session-query/tool-session-query/src/index.ts:29-40
type Config struct {
	// Service 是那台读引擎，必填。
	Service Service
	// AgentOf 从一把作用域钥匙找到那个 agent，必填。
	//
	// 新增: DSH 的 exec.agent 就是 agent 对象本身。Go 这边它是一把不透明的钥匙，
	// 所以由装配方交一条查回去的路，做法和 [ds-harness-go/plan/planmode.Config.AgentOf]
	// 逐字相同。
	AgentOf func(agent *scope.Key) (agent.Agent, error)
	// MaxSearchResults 是一次检索最多交回多少条命中；零取 [DefaultMaxSearchResults]。
	MaxSearchResults int
	// SearchTimeout 是两件检索工具的截止时间；零取 [DefaultSearchTimeout]。
	//
	// 新增: DSH 自己在 invoke 外面包一个计时器。Go 这边它直接交给
	// [ds-harness-go/core/tools.Definition.Timeout]，由工具运行时统一执行——那件事
	// 本来就不该每个工具包自己再做一遍。DSH 那道 MAX_TIMER_DELAY_MS 上界随之消失：
	// 它是 setTimeout 的实现限制，Go 的 context 截止时间没有这个坎。
	SearchTimeout time.Duration
	// Logger 是被清洗掉的那些失败原文的去处；nil 用 [slog.Default]。
	Logger *slog.Logger
}

// Controller 是这五件工具共用的那点状态。
type Controller struct {
	service          Service
	agentOf          func(agent *scope.Key) (agent.Agent, error)
	maxSearchResults int
	searchTimeout    time.Duration
	logger           *slog.Logger
}

// New 造一个控制器。
//
// 源: packages/session-query/tool-session-query/src/index.ts:124-137
//
// 两个可调的数只查「不能是负数」：零表示没给、走默认值，这和本仓库其余包一致。
// DSH 那边 schemastery 已经把 min(1) 挡在外面，Go 这边没有那一层，所以自己查。
func New(config Config) (*Controller, error) {
	if config.Service == nil {
		return nil, fmt.Errorf("querytool: 需要一台会话读引擎")
	}
	if config.AgentOf == nil {
		return nil, fmt.Errorf("querytool: 需要一条从作用域钥匙找回 agent 的路")
	}
	if config.MaxSearchResults < 0 {
		return nil, fmt.Errorf("querytool: MaxSearchResults 不能是负数，拿到 %d", config.MaxSearchResults)
	}
	if config.SearchTimeout < 0 {
		return nil, fmt.Errorf("querytool: SearchTimeout 不能是负数，拿到 %s", config.SearchTimeout)
	}

	controller := &Controller{
		service:          config.Service,
		agentOf:          config.AgentOf,
		maxSearchResults: config.MaxSearchResults,
		searchTimeout:    config.SearchTimeout,
		logger:           config.Logger,
	}
	if controller.maxSearchResults == 0 {
		controller.maxSearchResults = DefaultMaxSearchResults
	}
	if controller.searchTimeout == 0 {
		controller.searchTimeout = DefaultSearchTimeout
	}
	if controller.logger == nil {
		controller.logger = slog.Default()
	}
	return controller, nil
}
