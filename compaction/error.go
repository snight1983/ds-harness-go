// 本文件的作用：本包会报的那几种错误。
//
// 新增: DSH 侧压缩这一层的失败分两路——`fail(字符串)` 给不变量，
// `throw new Error(字符串)` 给工具配对。两路调用方都只能看消息去分辨。
// Go 里错误是要被 errors.Is 分派的，所以这里按「读到之后该做什么」分类。

package compaction

import "errors"

var (
	// ErrMalformedEvent 表示一条 compaction/* 事件的负载读不回来。
	//
	// 该做的事：这份日志坏了，或者它是另一个构建写的而两边的负载形状对不上。
	ErrMalformedEvent = errors.New("compaction: 事件负载读不回来")

	// ErrInvariantViolated 表示日志违反了本包自己拥有的那几条不变量
	// （压缩括号没配对、归属的回合对不上、摘要和它自己的阴影范围对不上）。
	//
	// 该做的事：这是**写日志那一方**的缺陷。一次压缩会把一整段历史从表面上
	// 换掉，括号错位意味着换掉的不是它自己声称的那一段。
	ErrInvariantViolated = errors.New("compaction: 事件违反了本包的不变量")

	// ErrSurfaceCorrupt 表示表面层报出来的节点和日志对不上——某个 seq 在日志里
	// 找不到对应的事件，或者一条工具结果前面没有还开着的调用。
	//
	// 和 [ErrInvariantViolated] 分开，是因为两者指向的产出方不是同一个：
	// 不变量查的是压缩自己写下的那几条事件，这一条查的是别人交进来的那份表面。
	ErrSurfaceCorrupt = errors.New("compaction: 表面层和日志对不上")
)
