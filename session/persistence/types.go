// 本文件的作用：读写两侧交换的那几个值——存档位置、原始存档、
// 逻辑视图、快照，以及后端交出来的物理前缀与后缀。
//
// 源: packages/session/session-persistence/src/index.ts:17-41
// 源: packages/session/session-persistence/src/index.ts:66-75
// 源: packages/session/session-persistence/src/coordinator.ts:86-110

package persistence

import "ds-harness-go/session"

// Location 是一个会话在某个后端里那份独立存档的位置。
//
// 源: packages/session/session-persistence/src/index.ts:66-75
//
// 它是一个**位置提示**，不是授权凭据：拿到它不代表有权读那个文件。
// 路径是绝对的，而且可以指向一份还没落地的存档。
type Location struct {
	// Kind 是后端自己给这类存档起的名字，比如 "jsonl"。
	Kind string
	// Path 是这个会话那份后端自有存档的绝对路径。
	Path string
}

// RawArtifact 是一个后端为某个会话写下的那份存档，逐字节原样。
//
// 源: packages/session/session-persistence/src/index.ts:33-41
//
// Content 是**原始文本**，不是从解出来的事件重新拼的，所以它保留了后端自己的
// 序列化选择（分块压行、键的顺序、换行方式）。要的就是这个保真度：
// 它是用来给人看、给人比对的，重拼一遍就看不出后端到底写了什么。
type RawArtifact struct {
	// Meta 是从这份存档自己的第一行解出来的会话头。
	Meta session.SessionHeader
	// Filename 是这份存档在盘上的基本文件名，不带任何物理编码后缀。
	Filename string
	// Content 是这份存档的全文，已经从后端的物理编码解出来（比如解压过的 JSONL）。
	Content string
}

// Inspection 是一个会话不可变的逻辑视图。
//
// 源: packages/session/session-persistence/src/index.ts:25-31
//
// 「逻辑」的意思是它已经过了校验、已经在内存里补齐了中途断掉的尾巴。
// 它可能和活着的、或者已经准备好的状态**共享**同一份底层数据，所以拿到它的
// 一方只能读，不许改。
type Inspection struct {
	// Meta 是已经验过的会话元数据。
	Meta session.SessionHeader
	// Events 是已经验过的、seq 连续的逻辑事件日志。
	Events []session.Event
}

// Snapshot 是一个会话的轻量身份：不读整份日志就能拿到的头加变更令牌。
//
// 源: packages/session/session-persistence/src/index.ts:17-23
type Snapshot struct {
	// Header 是这个已落地会话的元数据。
	Header session.SessionHeader
	// Revision 是这份日志当前的变更令牌，见 [Revision]。
	Revision Revision
}

// StoredPrefix 是一个后端交出来的物理前缀：头、连续的合法事件、
// 这一份前缀的变更令牌，以及一个可选的断尾标记。
//
// 源: packages/session/session-persistence/src/coordinator.ts:86-101
//
// Revision 标识的是**恰好这一份**脱离出来的前缀，必须和
// [Backend.ReadStoredRevision] 用同一套表示，否则「读一遍、再核对一遍令牌
// 看变没变」这个回合走不通。
type StoredPrefix struct {
	// Meta 是这份存档的头。
	Meta session.SessionHeader
	// Events 是这份存档里 seq 连续的那段合法事件。
	Events []session.Event
	// Revision 是恰好这一份前缀被观察到的变更令牌。
	Revision Revision
	// TornMarker 非 nil 表示后面还挂着一截写坏的尾巴，值是后端自己的截断凭据。
	//
	// 新增: DSH 把它做成类型参数 TornMarker，编排层声明自己完全不看它。
	// Go 里做成类型参数也行，但那个参数会一路传染到 [Backend]、[StoredPrefix]、
	// 以及每一个持有它们的字段上——而这个值的唯一用途就是被原样递回
	// [Backend.CommitRepair]。any 表达的正是「不看」这件事本身。
	TornMarker any
}

// StoredSuffix 是一个后端交出来的物理后缀：头，加上 seq 不小于某个水位的那些事件。
//
// 源: packages/session/session-persistence/src/coordinator.ts:103-110
//
// 这是**不改动**存储的读：没有截断、没有补写收尾，所以它不带断尾标记
// ——没有要修的东西。
type StoredSuffix struct {
	// Meta 是这份存档的头。
	Meta session.SessionHeader
	// Events 是 seq 不小于请求水位的那些事件。
	Events []session.Event
}
