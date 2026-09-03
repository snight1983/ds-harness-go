// 本文件的作用：这个包自己拥有的那条运行期不变量。
//
// 源: packages/storage/storage-domain/src/invariant.ts:1-67

package domain

import (
	"context"
	"fmt"

	"github.com/snight1983/ds-harness-go/invariants"
)

// RegisterInvariants 装上「变更事件的约定」这条检查，返回注销函数。
//
// 源: packages/storage/storage-domain/src/invariant.ts:12-67
//
// # 这条检查在查什么
//
// 一次变更通知必须同时满足四件事，缺一它说的话就不成立：
//
//  1. **设施还活着，而且这个域还开着。** 通知陈述的是一次已经落盘的变更，
//     域都关了就没有「变更进哪里」可言。
//  2. **事件指的那张表这个域真的声明过。** 指到一张不存在的表，
//     订阅者按表名分派就会静静地丢掉它。
//  3. **put 事件必须带着后端出的那张收据**（[Changed.Revision] 非空）**和一份非空的值**。
//  4. **deleted 事件不带值也不带收据**——删除不产生新的一版。
//
// 第 3 条是 domain.go 顶部第 2 条不变量（先等后端确认落盘、再发事件）在运行期的证据。
// 修订标识只可能来自后端确认落盘之后的那个返回值（见 [Table.store]），凑不出来：
// 次序一旦被换成「先发事件再落盘」，那时候手上还没有收据，第 3 条当场抓到。
//
// # 为什么不再去读一遍介质对答案
//
// 新增: DSH 那条检查读的是内存态——事件带的值必须等于此刻内存里那一份。
// 那个内存态没了（理由见 domain.go 开头第 1 条），照搬过来就得改成「读一次介质再比」，
// 而那样有两个问题，每一个单独都足以否掉它：
//
//   - **它会误报。** 介质是所有副本共用的。这次写落盘之后、检查读到之前，
//     别的副本完全可以合法地再写一次；读回来的字节和事件对不上，是**正常**的，
//     而检查会把它报成一次不变量破坏。一条会误报的检查比没有检查更糟——
//     它教会所有人忽略它。
//   - **它会把写链堵住。** 通知是在写链的槽位里同步发的（见 [ChangedListener]），
//     在订阅者里读介质等于给每一次写都串上一次数据库往返。
//
// 收据这条路两个问题都没有：它不碰介质，也不受别的副本影响。
//
// # 三个参数分别顶替了什么
//
// 新增: DSH 那边这是一个 cordis 插件（name / inject / apply 三件套），装配由容器负责。
// Go 里没有容器，装配就是调用方写的那一行，所以这三件事得显式递进来：
//
//   - registry 顶替 ctx.invariants；
//   - facility 顶替 ctx.on('domain/changed', ...) 和 ctx.storage.form('domain')；
//   - live 顶替「设施本身还在不在」那一问。
//
// live 由装配方给而不是本包自己判断，理由同本仓库 settings 和 credentials：
// 「这个设施还挂着吗」只有那个 New 出它、也负责关掉它的人知道。
func RegisterInvariants(
	ctx context.Context,
	registry *invariants.Registry,
	facility *Facility,
	live func() bool,
) (func(), error) {
	if registry == nil {
		return nil, fmt.Errorf("storage/domain: 注册不变量需要一个不变量注册表")
	}
	if facility == nil {
		return nil, fmt.Errorf("storage/domain: 注册不变量需要一个域设施")
	}
	if live == nil {
		return nil, fmt.Errorf("storage/domain: 注册不变量需要一个「设施还活着吗」的判据")
	}

	install := func(_ context.Context, scope *invariants.Scope, fail invariants.Fail) error {
		// 退订登记进 scope：注销这次注册时订阅必须跟着摘掉，
		// 否则一条不该再查的检查会继续在别人的写路径上抛。
		scope.Defer(facility.Subscribe(func(change Changed) {
			if !live() {
				fail(fmt.Sprintf("域 %q 的变更事件发出时，域设施已经不在了", change.Domain))
				return
			}
			domain, open := facility.Get(change.Domain)
			if !open {
				fail(fmt.Sprintf("域 %q 的变更事件发出时，这个域并没有开着", change.Domain))
				return
			}
			checkChange(domain, change, fail)
		}))
		return nil
	}

	return registry.Register(ctx, PackageName, install)
}

// checkChange 是四条里的后三条，按事件说的是全局槽还是一条记录分两路。
//
// 源: packages/storage/storage-domain/src/invariant.ts:35-63
//
// 全局槽用空表名表示，和 [Changed] 与 [RecordSlot] 是同一套约定。
func checkChange(domain *Domain, change Changed, fail invariants.Fail) {
	slot := fmt.Sprintf("域 %q 的表 %q 的记录 %q", change.Domain, change.Table, change.Key)
	if change.Table == "" {
		slot = fmt.Sprintf("域 %q 的全局值", change.Domain)
		if change.Operation == OperationDeleted {
			// 全局槽只会被写，不会被删——[Global] 上根本没有删除这个操作。
			fail(slot + "发出了一条删除事件，但全局槽根本删不掉")
			return
		}
	} else if _, declared := domain.tables[change.Table]; !declared {
		fail(fmt.Sprintf("域 %q 的变更事件指着表 %q，但这个域没有声明过它",
			change.Domain, change.Table))
		return
	}

	if change.Operation == OperationDeleted {
		if len(change.Value) != 0 {
			fail(slot + "的删除事件带上了值，但墓碑不带值")
		}
		if change.Revision != "" {
			fail(slot + "的删除事件带上了修订标识，但删除不产生新的一版")
		}
		return
	}

	if len(change.Value) == 0 {
		fail(slot + "的写入事件没带值")
	}
	// 收据为空说明这条事件发在后端确认落盘之前，理由见 [RegisterInvariants]。
	if change.Revision == "" {
		fail(slot + "的写入事件没带修订标识，说明它发在后端确认落盘之前")
	}
}
