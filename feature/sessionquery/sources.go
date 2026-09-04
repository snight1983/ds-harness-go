// 本文件的作用：同一个会话 id 底下的两份观察对不对得上。
//
// 源: packages/session-query/session-query/src/sources.ts

package sessionquery

import (
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// AssertHeadersCompatible 拒掉同一个逻辑会话源上互相矛盾的两份观察。
//
// 源: packages/session-query/session-query/src/sources.ts:11-26
//
// 比的是身份字段：格式版本、id、建会话时间、工作目录、父会话、seed 长度、
// 派发深度。对不上说明同一个 id 底下其实是两个不同的会话——那是配置事故
// （比如两个进程连着同一份介质各写各的），只能拒，不能挑一份用。
//
// 新增: 上游那个例子说的是「两个进程指着同一个存储目录」，因为它的会话存档是
// 目录里的文件。本仓库唯一的会话后端是
// [github.com/snight1983/ds-harness-go/adapter/datastore/sessionstore.Backend]，一份介质是**一个连接池加
// 一个命名空间**（见 docs/modules/datastore.md），事故的形状因此是同一个库、
// 同一个命名空间被两个进程各写各的，不是同一个目录。
//
// 新增: DSH 那张身份表里这一项是 `cwd`，一条宿主机工作目录。本仓库对应的是
// [sessionlog.SessionHeader.WorkspaceID]，一个不透明的工作区标识——记的东西从位置
// 换成了归属，但它仍然是身份的一部分：两条归在不同工作区的日志不可能是同一个会话。
//
// 新增: DSH 那边写的是 `(a.delegationDepth ?? 0) !== (b.delegationDepth ?? 0)`，
// 因为它的这个字段可缺失。[sessionlog.SessionHeader.DelegationDepth] 是 int，
// 缺失与零本来同义，那个 ?? 在这里消失了。Origin 和 AgentPreset 不在比较里，
// 和 DSH 一致：前者是展示分类，后者可以在恢复时换掉，两者都不是身份。
func AssertHeadersCompatible(a, b sessionlog.SessionHeader) error {
	if a.Version != b.Version ||
		a.ID != b.ID ||
		a.CreatedAt != b.CreatedAt ||
		a.WorkspaceID != b.WorkspaceID ||
		a.ParentSession != b.ParentSession ||
		a.SeedLength != b.SeedLength ||
		a.DelegationDepth != b.DelegationDepth {
		return fail(CodeSourceConflict, "会话 %q 的两份来源头对不上", a.ID)
	}
	return nil
}
