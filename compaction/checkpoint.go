// 本文件的作用：压缩检查点的出处标记——造一条、认一条、以及从一条里把身份取回来。
//
// 源: packages/compaction/compaction/src/checkpoint.ts

package compaction

import (
	"encoding/json"
	"fmt"

	"ds-harness-go/llm"
)

// CheckpointPlugin 是每个压缩后端给自己那条替换用户消息盖的产出方名字。
//
// 源: packages/compaction/compaction/src/checkpoint.ts:19
//
// 它和后端无关：不管是整段总结还是裁剪工具结果，落进日志的那条检查点消息
// 盖的都是这一个名字。判定这条消息是不是检查点，靠的正是这个名字，
// 所以它必须是常数而不是各后端自取——一个后端换个名字，
// 别的层就再也认不出它写下的检查点了。
const CheckpointPlugin = "compact"

// CheckpointSource 是一条压缩检查点消息带的那点出处。
//
// 源: packages/compaction/compaction/src/checkpoint.ts:22-25
//
// 新增: DSH 那边是 `{kind:'plugin', plugin:'compact'} & {compactionId, sourceCommandId?}`
// 一个交叉类型，两半摊在同一个对象上。Go 的结构体加不上字段，所以拆成两层：
// 这个类型记本包自己的两个字段，[NewCheckpointSource] 把它们塞进
// [llm.PluginSource.Extra]，介质上排出来的字节和 DSH 一模一样。
type CheckpointSource struct {
	// CompactionID 是拥有这条检查点的那次压缩事务。
	CompactionID ID
	// SourceCommandID 是发起这次压缩的那条人工命令；空表示不是人工发起的。
	//
	// 新增: DSH 用 `dsh-commands/brand` 的 `CommandId`。那个包归 tool 那一块
	// （docs/DESIGN.md 第八节第 4 块），从上下文这一块去 import 它会把移植顺序
	// 倒过来。这里用普通的 string：不变量对它的全部要求就是「一个非空的不透明
	// 字符串，一次事务里前后一致」，具名类型进来之后这里改个类型即可，
	// 介质上的字节不动。
	SourceCommandID string
}

// checkpointExtra 是这两个字段在介质上的样子，字段名和 DSH 一致。
type checkpointExtra struct {
	CompactionID    ID     `json:"compactionId"`
	SourceCommandID string `json:"sourceCommandId,omitempty"`
}

// NewCheckpointSource 造一条和某次压缩事务相关联的检查点出处。
//
// 源: packages/compaction/compaction/src/checkpoint.ts:33-42
//
// [CheckpointSource.CompactionID] 为空时报 [ErrInvariantViolated]，不是等到
// 不变量那一侧再说：一条身份为空的检查点落进持久日志之后，它属于哪次压缩就
// 再也查不出来了，而这条日志本身「读得回来」，不会有别的地方报警。
func NewCheckpointSource(checkpoint CheckpointSource) (llm.PluginSource, error) {
	if checkpoint.CompactionID == "" {
		return llm.PluginSource{}, fmt.Errorf("%w：压缩检查点的 compactionId 不能是空的",
			ErrInvariantViolated)
	}
	extra, err := json.Marshal(checkpointExtra{
		CompactionID:    checkpoint.CompactionID,
		SourceCommandID: checkpoint.SourceCommandID,
	})
	if err != nil {
		// 不可达：两个字段都是 string，排不出去的情况不存在。
		return llm.PluginSource{}, fmt.Errorf("%w：压缩检查点的出处排不出去：%w",
			ErrMalformedEvent, err)
	}
	return llm.PluginSource{Plugin: CheckpointPlugin, Extra: extra}, nil
}

// IsCheckpointSource 判断一条从表面用户消息上恢复出来的来源是不是压缩检查点。
//
// 源: packages/compaction/compaction/src/checkpoint.ts:44-51
//
// 只看产出方的名字，**不**看身份字段在不在。这是有意的，和 DSH 一致：
// 这个判定要在不变量之前跑——先认出「这是一条检查点」，才轮得到不变量去说
// 「它的身份字段缺了」。合并成一条的话，一条身份写坏的检查点会安静地不再是
// 检查点，于是那次压缩看起来只是少了一条替换消息。
func IsCheckpointSource(source llm.MessageSource) bool {
	plugin, ok := source.(llm.PluginSource)
	return ok && plugin.Plugin == CheckpointPlugin
}

// CheckpointSourceOf 把一条检查点来源上的身份取回来。
//
// 第二个返回值为假表示这条来源根本不是检查点（此时第三个返回值一定是 nil）。
// 是检查点但那几个字段读不回来时报 [ErrMalformedEvent]。
//
// 新增: DSH 那边靠交叉类型直接读 `source.compactionId`，读到 undefined 交给
// 不变量去报。Go 这一侧字段在 [llm.PluginSource.Extra] 里，要解一次，
// 于是「不是检查点」和「是检查点但坏了」变成两件必须分开的事。
func CheckpointSourceOf(source llm.MessageSource) (CheckpointSource, bool, error) {
	plugin, ok := source.(llm.PluginSource)
	if !ok || plugin.Plugin != CheckpointPlugin {
		return CheckpointSource{}, false, nil
	}
	if len(plugin.Extra) == 0 {
		return CheckpointSource{}, true, nil
	}
	var extra checkpointExtra
	if err := json.Unmarshal(plugin.Extra, &extra); err != nil {
		return CheckpointSource{}, true, fmt.Errorf("%w：压缩检查点的出处读不回来：%w",
			ErrMalformedEvent, err)
	}
	return CheckpointSource{
		CompactionID:    extra.CompactionID,
		SourceCommandID: extra.SourceCommandID,
	}, true, nil
}
