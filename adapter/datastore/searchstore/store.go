// 本文件的作用：这个检索后端本身——装配它要什么、它握着什么、怎么关掉。
//
// 新增: 整个文件都是本仓库自有的：DSH 那边这些是 cordis 的服务声明和注入，
// 本仓库没有容器，装配方把东西直接交进来。

package searchstore

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/snight1983/ds-harness-go/adapter/datastore"
	"github.com/snight1983/ds-harness-go/feature/persistence"
	"github.com/snight1983/ds-harness-go/feature/sessionquery"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// UnitVersion 是这个单元里的值的格式版本，盖在 datastore 的单元登记处。
//
// 新增: 它说的是「一条文档的五个字段是怎么从一条事件折出来的」。盖着别的号一律
// 拒绝：这套折法还没发布过，没有迁移这一说——真要改折法，整份索引重建一遍就是了，
// 它本来就是从会话日志推出来的派生物。
const UnitVersion = 1

// DefaultUnit 是 [Config.Unit] 留空时用的单元名。
const DefaultUnit = "session_search"

// 源: packages/session-query/session-query-sqlite/src/index.ts:76-80
const (
	// DefaultLimit 是请求没写页大小时，一页给几条。
	DefaultLimit = 20
	// DefaultMaxLimit 是一页最多能要几条。
	DefaultMaxLimit = 100
	// DefaultSnippetChars 是一段摘录最多几个字。
	DefaultSnippetChars = 240
)

// maxPushedGroups 是一次查找最多往库里塞几个会话名。
//
// 新增: DSH 那边是 SQLITE_PORTABLE_VARIABLE_LIMIT = 32766，因为它把整份语料的
// 元数据也写进了 SQLite，过滤器编成 SQL 就不必塞名字。本仓库的会话元数据留在
// 内存里（见 [Store.SearchSessions]），所以圈定哪些会话这件事有时要靠一串名字
// 传下去。超过这个数就不塞了，改成把库里排好的结果一页页取上来，在这边筛——
// 慢一点，但不会撞上绑定参数的上限，也不必让调用方去猜自己的过滤器有多宽。
const maxPushedGroups = 4000

// Persistence 是本包用得着的那一小块持久化后端能力。
//
// 新增: 只声明两个方法，[persistence.Store] 结构上天然满足它。窄接口是有理由的：
// 一个检索后端永远不该写会话，收一个只读接口就让「写不了」变成编译期事实。
type Persistence interface {
	// ListSnapshots 列举已落地的会话，各带一个便宜的变更令牌。对账全靠它。
	ListSnapshots(ctx context.Context) ([]persistence.Snapshot, error)
	// Inspect 读出一个已落地会话验过的逻辑视图。
	Inspect(ctx context.Context, id sessionlog.SessionID) (persistence.Inspection, error)
}

// 编译期确认：真正的持久化服务满足本包这个窄接口。
var _ Persistence = (persistence.Store)(nil)

// Config 是这个检索后端的部署配置。
type Config struct {
	// Medium 是介质的配置：连接池、方言、命名空间、池子的那几个数。
	//
	// 连接池**归这个后端所有**：[Store.Close] 会把它关掉。
	Medium datastore.Config

	// Unit 是索引落在哪个单元里，留空则是 [DefaultUnit]。
	//
	// 同一份介质里可以有别的单元（会话日志、键值中枢），它们互不相干。
	Unit string

	// Live 是活会话表，必填。
	//
	// 没有它这个后端只看得见已落地的那一半，而正在打字的那个会话恰恰是最常被
	// 搜的——那种「刚说过的话搜不到」不会被当成配置错误，只会被当成搜索坏了。
	Live sessionquery.LiveSessions

	// Persistence 是持久化后端；nil 表示这次装配只有活着的会话。
	Persistence Persistence

	// Limit 是请求没写页大小时给几条，0 走 [DefaultLimit]。
	Limit int

	// MaxLimit 是一页最多能要几条，0 走 [DefaultMaxLimit]。
	MaxLimit int

	// SnippetChars 是一段摘录最多几个字，0 走 [DefaultSnippetChars]。
	SnippetChars int

	// Logger 收那些只在本进程里有意义的诊断；nil 就用 [slog.Default]。
	Logger *slog.Logger
}

// Store 是把会话折成文档、再从文档里找回来的那个检索后端。
//
// 多个 goroutine 可以同时调它的两个检索方法。
type Store struct {
	medium *datastore.Medium
	docs   *datastore.DocUnit
	live   sessionquery.LiveSessions
	store  Persistence

	limit        int
	maxLimit     int
	snippetChars int
	logger       *slog.Logger

	// syncing 把对账串起来。两次查询同时发现同一个会话该重建时，让后到的那次
	// 等一等再看——两次都去重建不会写坏什么（整组换掉是原子的），但会白读一遍
	// 整份日志，而重建正是这条路上最贵的一步。
	syncing sync.Mutex
}

// 这一行钉住这个后端真的填满了那道缝。
var _ sessionquery.Searcher = (*Store)(nil)

// New 在一份介质上打开这个检索后端。
//
// 新增: 打不开就是打不开，当场说，不做成一个延迟到第一次检索才浮出来的东西。
func New(ctx context.Context, config Config) (*Store, error) {
	if config.Live == nil {
		return nil, fail(sessionquery.CodeInvalidConfig, "会话检索后端必须挂上活会话表")
	}
	if config.Limit < 0 || config.MaxLimit < 0 || config.SnippetChars < 0 {
		return nil, fail(sessionquery.CodeInvalidConfig, "会话检索后端的页大小和摘录长度不能是负数")
	}
	limit := orDefault(config.Limit, DefaultLimit)
	maxLimit := orDefault(config.MaxLimit, DefaultMaxLimit)
	if limit > maxLimit {
		return nil, fail(sessionquery.CodeInvalidConfig,
			"会话检索后端的默认页大小 %d 超过了上限 %d", limit, maxLimit)
	}
	unit := config.Unit
	if unit == "" {
		unit = DefaultUnit
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}

	medium, err := datastore.Open(ctx, config.Medium)
	if err != nil {
		return nil, wrap(sessionquery.CodeIndexFailed, err, "打不开会话检索的介质")
	}
	docs, err := medium.OpenDocs(ctx, datastore.DocSpec{Name: unit, Version: UnitVersion})
	if err != nil {
		// 单元开不起来就把介质还回去：不还的话，一次失败的构造会留下一个谁也
		// 够不着、也关不掉的连接池。
		_ = medium.Close(ctx)
		return nil, wrap(sessionquery.CodeIndexFailed, err, "打不开会话检索的单元 %q", unit)
	}
	return &Store{
		medium:       medium,
		docs:         docs,
		live:         config.Live,
		store:        config.Persistence,
		limit:        limit,
		maxLimit:     maxLimit,
		snippetChars: orDefault(config.SnippetChars, DefaultSnippetChars),
		logger:       logger,
	}, nil
}

// Close 释放介质。重复调用是空操作。
func (s *Store) Close(ctx context.Context) error {
	_ = s.docs.Close(ctx)
	if err := s.medium.Close(ctx); err != nil {
		return wrap(sessionquery.CodeIndexFailed, err, "关会话检索的介质失败")
	}
	return nil
}

// orDefault 是「留零就照缺省来」。
func orDefault(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

// fail 造一条带分类的失败。
//
// 新增: 本包报的每一条错都必须落在 [sessionquery.Code] 那套封闭的码里——那套码
// 是对外协议的一部分，一个后端自己发明一个码，调用方就路由不了。
func fail(code sessionquery.Code, format string, args ...any) error {
	return &sessionquery.Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// wrap 把一条底层错误裹进本包的分类里。
func wrap(code sessionquery.Code, cause error, format string, args ...any) error {
	return &sessionquery.Error{Code: code, Message: fmt.Sprintf(format, args...), Cause: cause}
}
