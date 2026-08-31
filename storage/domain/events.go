// 本文件的作用：域的变更事件词汇。
//
// 源: packages/storage/storage-domain/src/events.ts:1-8
//
// 每一次持久化成功的写发一条，**发在后端确认落盘之后**，带的是新快照。
// 从不带旧值——要做差分的订阅者自己留着上一份快照，那是它的事；
// 让事件带上旧值等于逼所有订阅者为一个少数派需求付内存。

package domain

import "encoding/json"

// Operation 是一次变更的动作，是一个封闭集合。
//
// 源: packages/storage/storage-domain/src/events.ts:20-34
type Operation string

const (
	// OperationPut 表示一条记录（或者全局值）被写入或覆盖了。
	OperationPut Operation = "put"
	// OperationDeleted 表示一条记录被删掉了。墓碑不带值。
	OperationDeleted Operation = "deleted"
)

// Changed 是一次已经落盘的域变更。
//
// 源: packages/storage/storage-domain/src/events.ts:10-34
//
// 同一个域的事件按它那条写链的顺序到达。
//
// 新增: Value 是 json.RawMessage 而不是 DSH 那边的 unknown（解析后的值）。
// 理由是订阅者这一侧的类型信息：一个通用订阅者（跨进程推送、审计、不变量检查）
// 按定义不知道这个域的记录是什么 Go 类型，拿到一个 any 它什么也做不了；
// 而原始 JSON 是它真正能转发、能落日志、能比较的东西。
// 要类型化的值的那一方，手上有 [Table] 句柄，直接读就是。
type Changed struct {
	// Domain 是发生变更的域名。
	Domain string
	// Table 是表名；**全局槽的写为空串**。
	Table string
	// Key 是记录键；**全局槽的写为空串**。
	Key string
	// Operation 是这次变更的动作。
	Operation Operation
	// Value 是新值的 JSON 投影；[OperationDeleted] 时为 nil。
	Value json.RawMessage
}

// ChangedListener 是一个变更订阅者。
//
// 源: packages/storage/storage-domain/src/events.ts:36-48
//
// 新增: DSH 那边是 cordis 的 ctx.emit('domain/changed', ...)，全局事件总线。
// Go 里没有那条总线，订阅落在 [Facility.Subscribe] 上——一个设施就是一个域的集合，
// 那正是这条事件流的边界。
//
// # 订阅者要守的两条
//
//  1. **不许阻塞。** 通知是**在写链的槽位里同步发的**，一个慢订阅者会把这个域
//     后面所有的写堵住。这一条和本仓库 settings.Watcher 是同一个取舍：
//     同步分发天生就是提交顺序，代价就是这个。
//  2. **不许回头写同一个域。** 写链是一把不可重入的互斥量，在订阅者里
//     调 [Table.Put] 会当场死锁。要写就另起一个 goroutine。
type ChangedListener func(change Changed)
