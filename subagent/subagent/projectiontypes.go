// 本文件的作用：子 agent 那两个投影发布给客户端看的词汇——纯数据，不带任何
// 运行时依赖。
//
// 源: packages/subagent/subagent/src/projection-types.ts

package subagent

// TimingActive 是一个还没走到 turn/end 的开着的回合，在同一刀切面上的两端。
//
// 源: packages/subagent/subagent/src/projection-types.ts:12-17
type TimingActive struct {
	// Since 是这个开着的回合的起点。
	Since int64 `json:"since"`
	// Through 是折进这一刀切面的最后一条事件的时间。
	Through int64 `json:"through"`
}

// TimingProjection 是一个有描述符的孩子会话那份耐久的活跃回合计时。
//
// 源: packages/subagent/subagent/src/projection-types.ts:7-18（SubagentTimingProjection）
type TimingProjection struct {
	// SettledMs 是这个孩子**自己**那条描述符之后、那些已完成回合累起来的毫秒数。
	SettledMs int64 `json:"settledMs"`
	// Active 是当下那个开着的回合；nil 表示没有开着的回合。
	Active *TimingActive `json:"active,omitempty"`
}

// IdentityProjection 是一个有描述符的子 agent 会话那份耐久身份：生命周期模式
// 加创建名，从那些 [EventDescriptor] 事件里按「最后一条算数」折出来。
//
// 源: packages/subagent/subagent/src/projection-types.ts:20-47（SubagentIdentityProjection）
//
// 新增: DSH 是一个按 mode 判别的联合，两支的唯一差别是「一次性那支的 label 可选、
// 可续那支必须有」——那正是 [DescriptorData] 的那条 label 强度规矩。Go 这边和
// 描述符本身保持同一种做法：一个结构体加一个 Mode 字段，两支的差别由
// [DescriptorData.Validate] 在折进来之前就守住了，投影只是把折出来的东西摆出去。
type IdentityProjection struct {
	// Mode 是这个孩子的生命周期模式。
	Mode DescriptorMode `json:"mode"`
	// Label 是描述符上那个耐久的创建名。ModeOneShot 时可以是空串。
	Label string `json:"label,omitempty"`
	// Seq 是这份身份折自的那条 [EventDescriptor] 事件的 seq。
	//
	// `Seq >= header.SeedLength` 证明这份身份来自孩子**自己**那段日志后缀——
	// 描述符一旦追加就不可改——而不是来自一份分叉种子里被回放的祖先描述符。
	Seq int `json:"seq"`
}
