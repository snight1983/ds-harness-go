// 本文件的作用：这台注册表落在介质上的那张表——一条作业记录长什么样、怎么和
// [github.com/snight1983/ds-harness-go/jobs/jobs.Snapshot] 互相折算，以及这个域的静态声明。
//
// 新增: 整个文件都是本仓库自有的。DSH 那台注册表的账本就是一个进程里的一张 map
// （packages/jobs/jobs-local/src/index.ts:104-116），没有任何东西要落到介质上，
// 所以那边没有对应物。

package domainjobs

import (
	"fmt"
	"time"

	"github.com/snight1983/ds-harness-go/jobs/jobs"
	"github.com/snight1983/ds-harness-go/session"
	"github.com/snight1983/ds-harness-go/storage/domain"
)

// DomainName 是这台注册表占的域名。
const DomainName = "jobs"

// DomainVersion 是这个域的声明版本。介质上盖着的版本和它对不上就开不了，
// 那是 [github.com/snight1983/ds-harness-go/storage.CodeVersionMismatch] 的事。
const DomainVersion = 1

// TableName 是那张作业表的表名，键是 [jobs.JobID]。
const TableName = "jobs"

// Record 是一件作业在介质上的样子。
//
// 它是 [jobs.Snapshot] 加上「落定之后那份最终输出」。输出直接落在记录里而不是
// 外置：[jobs.Start.OutputLimitBytes] 已经把它的大小封住了，为一份有上限的短文本
// 再引一条外置存储的缝，是给装配方多加一样要接的东西而换不来什么。
//
// 字段用 json tag 明写名字：这张表是**跨副本**读的，两个副本上的这个结构体必须
// 折算出同一份字节，而 Go 的字段名一旦改动就会让老记录整片解不出来。tag 把那件事
// 钉在这里，改名字至少要先改到这一行。
type Record struct {
	ID               jobs.JobID        `json:"id"`
	Kind             jobs.JobKind      `json:"kind"`
	Runner           jobs.RunnerID     `json:"runner"`
	Label            string            `json:"label"`
	OutputLimitBytes int               `json:"outputLimitBytes"`
	OwnerSession     session.SessionID `json:"ownerSession"`
	Status           jobs.JobStatus    `json:"status"`
	Detail           string            `json:"detail"`
	StartedAt        time.Time         `json:"startedAt"`
	FinishedAt       time.Time         `json:"finishedAt"`
	Reported         bool              `json:"reported"`

	// Output 是落定之后那份幂等的最终输出，只有**没有**流式读取的那类作业会填。
	// 流式那类的中间过程归执行副本手里那只 [jobs.Hooks.ReadOutput] 所有，
	// 从来不进这张表——它是一条会被消费掉的游标，落进一份共享账本就没有意义了。
	Output string `json:"output"`
}

// Snapshot 把一条记录折成那份只读投影。
func (r Record) Snapshot() jobs.Snapshot {
	return jobs.Snapshot{
		ID:               r.ID,
		Kind:             r.Kind,
		Runner:           r.Runner,
		Label:            r.Label,
		OutputLimitBytes: r.OutputLimitBytes,
		OwnerSession:     r.OwnerSession,
		Status:           r.Status,
		Detail:           r.Detail,
		StartedAt:        r.StartedAt,
		FinishedAt:       r.FinishedAt,
		Reported:         r.Reported,
	}
}

// IsActive 说这条记录还占不占着那个属主的并发名额。
func (r Record) IsActive() bool {
	return r.Status == jobs.StatusRunning || r.Status == jobs.StatusStopping
}

// Validate 查这一条记录自己说不说得通。
//
// 查的是**形状**，不是权限：授权归 [Registry] 那道围墙，一条记录说不说得清自己
// 是谁、在谁那儿、什么时候起的什么时候完的，才是这里的事。
//
// 这条校验由域在**每一次读**上跑（见 [domain.DefineTable]），所以它同时也是
// 「别的副本写坏了一条记录」的那道拦网。
func (r Record) Validate() error {
	switch {
	case r.ID == "":
		return fmt.Errorf("作业记录没有 id")
	case r.Kind == "":
		return fmt.Errorf("作业 %q 没有种类", r.ID)
	case r.Runner == "":
		// 没有 runner 就没人判断得了自己能不能读它、停它，
		// 理由见 [jobs.RunnerID]。
		return fmt.Errorf("作业 %q 没说自己在哪个执行副本上", r.ID)
	case r.Label == "":
		return fmt.Errorf("作业 %q 没有标签", r.ID)
	case r.OutputLimitBytes < 0:
		return fmt.Errorf("作业 %q 的输出上限是负数 %d", r.ID, r.OutputLimitBytes)
	case r.Status == "":
		return fmt.Errorf("作业 %q 没有状态", r.ID)
	case r.StartedAt.IsZero():
		return fmt.Errorf("作业 %q 没盖开工时刻", r.ID)
	case r.Status.IsTerminal() != !r.FinishedAt.IsZero():
		// 「活着却盖了完成时刻」和「落定却没盖」是同一条：耗时按时刻算、死活按状态
		// 判，两种读法必须给出同一个答案。
		return fmt.Errorf("作业 %q 的完成时刻和状态 %q 对不上", r.ID, r.Status)
	case !r.FinishedAt.IsZero() && r.FinishedAt.Before(r.StartedAt):
		return fmt.Errorf("作业 %q 的完成时刻早于开工时刻", r.ID)
	}
	return nil
}

// Spec 是这个域的静态声明：一张作业表，没有全局槽。
//
// 没有全局槽是有意的：发号那件事不靠一个共享计数器，靠对那把键本身的一次条件写
// （见 [Registry.Start]）。多一个全局槽就多一处每次开工都要抢的热点。
func Spec() domain.Spec {
	return domain.Spec{
		Name:    DomainName,
		Version: DomainVersion,
		Tables: []domain.TableSpec{
			domain.DefineTable(TableName, func(record Record) error {
				return record.Validate()
			}),
		},
	}
}
