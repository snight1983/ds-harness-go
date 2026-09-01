// 本文件的作用：攒批写这一层的压力基线，写出口是空的。
//
// 空写出口是有意的：真的 fsync 要一毫秒上下，会把这一层自己的开销整个盖住
// （盘那一侧的数字在 jsonl 那一包量）。这里量的是**协调本身**值多少钱——
// 入队、克隆事件、起定时器、拿锁、以及那道静默屏障的往返。
//
// 这个数是每一条流式增量分块都要付的固定成本。一次模型输出在几十毫秒里产出
// 上百条分块，所以 Enqueue 一旦退化到微秒级，长回合就会被持久化这一层拖住。

package persistence

import (
	"strconv"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/session"
)

// BenchmarkWriteBehindEnqueue 量的是稳态入队：攒批窗口开着，事件进队列就返回，
// 真正的写在后台。这是热路径上唯一同步的那一段。
func BenchmarkWriteBehindEnqueue(b *testing.B) {
	writer := NewWriteBehind(WriteBehindOptions{
		MaxDelay:                5 * time.Millisecond,
		Write:                   func([]session.Event) error { return nil },
		ReportBackgroundFailure: func(error) { b.Fatal("后台写不该失败") },
	})
	event := userEvent(b, 0, "分块")

	b.ReportAllocs()
	b.ResetTimer()
	for index := range b.N {
		event.Seq = index
		writer.Enqueue(event)
	}
	b.StopTimer()
	if err := writer.Flush(); err != nil {
		b.Fatalf("收尾冲刷失败：%v", err)
	}
}

// BenchmarkWriteBehindFlushBatch 量的是「攒 size 条再显式冲刷」这一整个来回，
// 也就是一次回合边界上的持久化屏障。
//
// 每次冲刷都要等那批写真的做完，所以这条量的是屏障的往返延迟，不是吞吐。
// ns/op 除以 size 得到每事件成本；size 越大它越该往下掉——掉不下来说明屏障的
// 固定开销压不住。
func BenchmarkWriteBehindFlushBatch(b *testing.B) {
	for _, size := range []int{1, 10, 100} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			writer := NewWriteBehind(WriteBehindOptions{
				MaxDelay:                5 * time.Millisecond,
				Write:                   func([]session.Event) error { return nil },
				ReportBackgroundFailure: func(error) { b.Fatal("后台写不该失败") },
			})
			event := userEvent(b, 0, "分块")

			b.ReportAllocs()
			b.ResetTimer()
			for index := range b.N {
				for offset := range size {
					event.Seq = index*size + offset
					writer.Enqueue(event)
				}
				if err := writer.Flush(); err != nil {
					b.Fatalf("冲刷失败：%v", err)
				}
			}
		})
	}
}
