// 本文件的作用：一次压缩事务的身份。
//
// 源: packages/compaction/compaction/src/brand.ts

package compaction

// ID 是一次压缩事务从头到尾共用的那个身份。
//
// 源: packages/compaction/compaction/src/brand.ts:4
//
// 一次压缩会落下 compaction/start、compaction/summary、那条替换用的检查点消息、
// 以及 compaction/end 四样东西，它们靠这个值串起来。取值由实现方自己铸，
// 本包只要求它非空——见 [NewCheckpointSource] 和 [Trace.Validate]。
//
// 新增: DSH 那边是 `Branded<'CompactionId'>` 加一个只做类型转换的同名构造函数，
// 因为 TS 的类型是结构化的，一个裸 string 会跟别的 string 混用而编译器不管。
// Go 的具名类型本来就是标称的，`ID("x")` 和一个 string 变量互相赋值编译期就报错，
// 所以那个构造函数在这里没有对应物。理由和 [llm.MessageID] 那几个逐字相同。
type ID string
