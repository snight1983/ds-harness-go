// 本文件的作用：这台注册表的装配面——它是谁、账本在哪、每个属主能同时开几件活儿，
// 以及问别的副本要状态时那个轮询的密度。
//
// 新增: 整个文件都是本仓库自有的，成因见 doc.go。字段的取名和默认值一路照
// [github.com/snight1983/ds-harness-go/adapter/localjobs.Config]，
// 这样两台实现在装配处是可以对着换的。

package domainjobs

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/snight1983/ds-harness-go/feature/jobs"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/storage/domain"
)

// defaultMaxConcurrentJobsPerOwner 是每个属主的活跃作业数默认上限。
//
// 源: packages/jobs/jobs-local/src/index.ts:28
const defaultMaxConcurrentJobsPerOwner = 10

// defaultForeignPollInterval 是等一件**别的副本**上的作业落定时，两次读账本之间隔多久。
//
// 新增: 本副本上的作业靠一条 channel 等，那是零轮询的。跨副本没有那条 channel——
// 两个进程之间唯一的共同点就是那张表，所以只能问。取 250 毫秒是因为
// [jobs.Registry.Wait] 的调用方是一件工具调用（见
// github.com/snight1983/ds-harness-go/feature/jobs/jobstool），它的等待预算按秒算，
// 四分之一秒的分辨率对它绰绰有余，而对账本又只是每秒四次读。
const defaultForeignPollInterval = 250 * time.Millisecond

// startAttempts 是发号撞车时重试几次。
//
// 新增: 见 [Registry.Start]。号是靠对那把键本身的一次条件写抢来的，撞车就是
// 「另一个副本刚好也在开这一类的活儿」。取 8 是因为每一轮都重读了一次当下的最大号，
// 连着八次被人抢在前面说明这一类正在被高频开工，此时报错让调用方重来，
// 比在这里无限转下去诚实。
const startAttempts = 8

// Agents 是这台注册表用得到的那一小块 agent 登记簿。
//
// 形状和 [github.com/snight1983/ds-harness-go/adapter/localjobs.Agents] 逐字相同：
// 属主清理必须挂在那个确切的活实例上，所以开工前要先核对交进来的就是登记着的那一个。
type Agents interface {
	// Get 按会话 id 找那个**当下登记着**的 agent 实例。
	Get(id sessionlog.SessionID) (agent.Agent, bool)
}

// Config 是这台注册表的装配面。
type Config struct {
	// Runner 是这个进程的执行副本标识，必填。
	//
	// 它盖在这个副本起的每一条记录上，别的副本靠它判断一件作业读不读得到、停不停
	// 得了（见 [jobs.RunnerID]）。两个副本用同一个值是**错的**：那样 A 会以为
	// B 起的作业在自己手里，然后在一张空的句柄表上查不到它。
	//
	// 值从部署里来（容器名、实例 id 之类），本包不解释它。
	Runner jobs.RunnerID

	// Facility 是那台域设施，账本就开在它上面，必填。
	//
	// 交进来的是设施而不是一个已经打开的域：这个包自己 [domain.Facility.Open]
	// 它那个域，声明因此归它自己所有（见 [Spec]），装配方不必知道表长什么样。
	Facility *domain.Facility

	// MaxConcurrentJobsPerOwner 是同一个属主（或者那个共用的无主桶）里 running
	// 加 stopping 的上限，0 表示用默认值 10。
	//
	// 这个数是**跨副本**算的：账本是共享的，所以一个属主在 A 上开了十件，
	// 在 B 上就一件都开不了了。这正是要的——上限护的是那个属主，不是那台机器。
	MaxConcurrentJobsPerOwner int

	// Agents 是 agent 登记簿。只有**有主**作业用得到它。
	// 为 nil 时无主作业照常能跑，有主作业开工即被拒。
	Agents Agents

	// ForeignPollInterval 是等别的副本上那件作业落定时两次读账本的间隔，
	// 0 表示用默认值 250 毫秒。
	ForeignPollInterval time.Duration

	// Now 是取时刻的那只手，为 nil 时用 [time.Now]。
	Now func() time.Time

	// Logger 用来报监听器自己抛出来的错误、生产方的契约违反，以及接管上一次进程
	// 留下的那些记录，为 nil 时用 [slog.Default]。
	Logger *slog.Logger
}

// resolve 把默认值填上并把那几条装配规矩查一遍。
func (c Config) resolve() (Config, error) {
	switch {
	case c.Runner == "":
		return Config{}, fmt.Errorf("domainjobs: 需要一个执行副本标识")
	case c.Facility == nil:
		return Config{}, fmt.Errorf("domainjobs: 需要一台域设施")
	case c.MaxConcurrentJobsPerOwner < 0:
		return Config{}, fmt.Errorf("domainjobs: 每属主并发上限不能是负数，收到 %d", c.MaxConcurrentJobsPerOwner)
	case c.ForeignPollInterval < 0:
		return Config{}, fmt.Errorf("domainjobs: 跨副本轮询间隔不能是负数，收到 %v", c.ForeignPollInterval)
	}
	if c.MaxConcurrentJobsPerOwner == 0 {
		c.MaxConcurrentJobsPerOwner = defaultMaxConcurrentJobsPerOwner
	}
	if c.ForeignPollInterval == 0 {
		c.ForeignPollInterval = defaultForeignPollInterval
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	return c, nil
}
