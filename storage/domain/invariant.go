// 本文件的作用：这个包自己拥有的那条运行期不变量。
//
// 源: packages/storage/storage-domain/src/invariant.ts:1-67

package domain

import (
	"bytes"
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
// 一次变更通知必须同时满足三件事，缺一它说的话就不成立：
//
//  1. **设施还活着，而且这个域还开着。** 通知陈述的是一次已经落盘的变更，
//     域都关了就没有「变更进哪里」可言。
//  2. **put 事件带的值就是此刻的内存态。** 对不上意味着事件和状态分了叉，
//     而收到通知的那一方会照着事件里的值走——两边从此各说各话，且谁都不会发现。
//  3. **deleted 事件发出时那条记录真的不在了。** 一条还在的记录被宣布删除，
//     会让订阅者据此丢掉自己那份缓存，而下一次读又把它读回来。
//
// 这三条是 domain.go 顶部第 2 条不变量（先落盘、再改内存、再发事件）在运行期的证据：
// 次序一旦被换成「先发事件再改内存」，第 2 条和第 3 条会当场抓到。
//
// # 为什么比的是 JSON 字节而不是值
//
// 新增: DSH 那边比的是 `===`（同一个对象引用），因为它的事件里带的就是刚存进去的
// 那个解析后的对象。Go 这边事件带的是 [Changed.Value]，即 JSON 投影
// （理由见 [Changed]），而这条检查按定义不知道记录是什么 Go 类型——
// 它拿到的两边都是 json.RawMessage，所以比的是 bytes.Equal。
//
// 这个比法**不比 DSH 弱**：两边的字节都由同一次写里的同一个 encode 产出
// 并原样存下（见 [Table.store]），任何一处分叉都会体现在字节上。
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

// checkChange 是三条里的后两条，按事件说的是全局槽还是一条记录分两路。
//
// 源: packages/storage/storage-domain/src/invariant.ts:35-63
//
// 全局槽用空表名表示，和 [Changed] 与 [RecordSlot] 是同一套约定。
func checkChange(domain *Domain, change Changed, fail invariants.Fail) {
	if change.Table == "" {
		// 全局槽只会被写，不会被删——[Global] 上根本没有删除这个操作。
		current, err := domain.RawGlobal()
		if err != nil {
			fail(fmt.Sprintf("域 %q 的全局值变更事件发出时读不到当前值：%v", change.Domain, err))
			return
		}
		if !bytes.Equal(current, change.Value) {
			fail(fmt.Sprintf("域 %q 的全局值变更事件带的值和此刻的内存态对不上", change.Domain))
		}
		return
	}

	current, exists, err := domain.RawRecord(change.Table, change.Key)
	if err != nil {
		fail(fmt.Sprintf("域 %q 的表 %q 记录 %q 变更事件发出时读不到当前值：%v",
			change.Domain, change.Table, change.Key, err))
		return
	}

	if change.Operation == OperationDeleted {
		if exists {
			fail(fmt.Sprintf("域 %q 的表 %q 宣布记录 %q 被删了，但它还在内存里",
				change.Domain, change.Table, change.Key))
		}
		return
	}

	if !exists {
		fail(fmt.Sprintf("域 %q 的表 %q 宣布记录 %q 被写入，但内存里没有它",
			change.Domain, change.Table, change.Key))
		return
	}
	if !bytes.Equal(current, change.Value) {
		fail(fmt.Sprintf("域 %q 的表 %q 记录 %q 的变更事件带的值和此刻的内存态对不上",
			change.Domain, change.Table, change.Key))
	}
}
