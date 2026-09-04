// 本文件的作用：交给模型的那份作业投影——它去掉了哪些字段、它的 schema 长什么样、
// 以及那行到处都在用的状态行。
//
// 源: packages/jobs/tool-jobs/src/index.ts:55-107

package jobstool

import (
	"encoding/json"

	"github.com/snight1983/ds-harness-go/feature/jobs"
	"github.com/snight1983/ds-harness-go/tools"
)

// PublicSnapshot 是一件作业里可以放心交给模型自己写程序去读的那一部分：属主和
// 通知记账全部不给。
//
// 源: packages/jobs/tool-jobs/src/index.ts:54-63（PublicJobSnapshot）
//
// 新增: 时刻在 [github.com/snight1983/ds-harness-go/feature/jobs.Snapshot] 那边是 [time.Time]，到这里折回
// DSH 那种 epoch 毫秒整数——这一层是**线上格式**，模型看见的必须和 DSH 一模一样。
type PublicSnapshot struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Status string `json:"status"`
	// Detail 是那一类自己的状态细节，生产方没给就不出现。
	Detail string `json:"detail,omitempty"`
	// StartedAt 是登记时刻，epoch 毫秒。
	StartedAt int64 `json:"startedAt"`
	// FinishedAt 是落定时刻，还活着时不出现。
	//
	// 新增: 用指针而不是 omitempty 的零值：一份没落定的快照那边是
	// [time.Time] 的零值，折成毫秒是一个很大的负数，不是 0——拿 0 当「没有」
	// 会让一件 1970 年之外的任何作业都被当成已落定。
	FinishedAt *int64 `json:"finishedAt,omitempty"`
}

// publicJob 把注册表那份快照上的属主和通知记账摘掉。
//
// 源: packages/jobs/tool-jobs/src/index.ts:85-96
func publicJob(snapshot jobs.Snapshot) PublicSnapshot {
	public := PublicSnapshot{
		ID:        string(snapshot.ID),
		Kind:      string(snapshot.Kind),
		Label:     snapshot.Label,
		Status:    string(snapshot.Status),
		Detail:    snapshot.Detail,
		StartedAt: snapshot.StartedAt.UnixMilli(),
	}
	if !snapshot.FinishedAt.IsZero() {
		finished := snapshot.FinishedAt.UnixMilli()
		public.FinishedAt = &finished
	}
	return public
}

// statusNames 是 schema 里那份状态白名单，从 [github.com/snight1983/ds-harness-go/feature/jobs] 的常量取。
//
// 源: packages/jobs/tool-jobs/src/index.ts:74-78
//
// 不写字面量的理由同 [github.com/snight1983/ds-harness-go/feature/sessionquery/querytool] 那几张白名单：这套值
// 和注册表认得的那套必须是同一套，抄一遍就意味着以后加一种状态时这里会悄悄落下。
var statusNames = []jobs.JobStatus{
	jobs.StatusRunning,
	jobs.StatusStopping,
	jobs.StatusCompleted,
	jobs.StatusKilled,
	jobs.StatusFailed,
}

// publicJobSchema 是那份投影的输出契约，三件工具共用。
//
// 源: packages/jobs/tool-jobs/src/index.ts:66-83
func publicJobSchema() tools.Node {
	closed := false
	enum := make([]json.RawMessage, 0, len(statusNames))
	for _, status := range statusNames {
		// 这几个取值全是本包里的字符串常量，排不出去是不可能的。
		encoded, _ := json.Marshal(string(status))
		enum = append(enum, encoded)
	}
	return tools.Node{
		Type: tools.TypeObject,
		Properties: []tools.Property{
			{Name: "id", Schema: tools.Node{Type: tools.TypeString}},
			{Name: "kind", Schema: tools.Node{Type: tools.TypeString}},
			{Name: "label", Schema: tools.Node{Type: tools.TypeString}},
			{Name: "status", Schema: tools.Node{Type: tools.TypeString, Enum: enum}},
			{Name: "detail", Schema: tools.Node{Type: tools.TypeString}},
			{Name: "startedAt", Schema: tools.Node{Type: tools.TypeInteger}},
			{Name: "finishedAt", Schema: tools.Node{Type: tools.TypeInteger}},
		},
		Required:             []string{"id", "kind", "label", "status", "startedAt"},
		AdditionalProperties: &closed,
	}
}

// StatusLine 排出那行通用状态，生产方给了细节就带上。
//
// 源: packages/jobs/tool-jobs/src/index.ts:97-106（statusLine）
//
// 新增: DSH 收的是 `Pick<JobSnapshot, 'status' | 'detail'>`，靠结构化类型让注册表
// 那份快照和这份公开投影都能传进来。Go 没有结构化类型，硬造一个只有两个字段的
// 接口不如直接把那两个值摊开——调它的四处本来就分属两种快照。
func StatusLine(status jobs.JobStatus, detail string) string {
	if detail != "" {
		return "[status: " + string(status) + ", " + detail + "]"
	}
	return "[status: " + string(status) + "]"
}
