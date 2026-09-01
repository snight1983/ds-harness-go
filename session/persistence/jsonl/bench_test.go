// 本文件的作用：JSONL 存储后端的压力基线——真的落在盘上，真的 fsync。
//
// 这一包是整条持久化链路上唯一碰 I/O 的地方，所以它的数字和上面几层不是一个
// 量级，也不该拿来和纯内存的基准比。量它是为了回答三个具体问题：
//
//   - **攒批到底值多少钱。** [persistence.WriteBehindOptions.MaxDelay] 的注释
//     说「一条一次 fsync 和一百条一次 fsync 差着两个数量级」。AppendBatch 这
//     一组就是那句话的实测：每事件成本从 batch=1 到 batch=100 应当掉一到两个
//     数量级。掉不下来，说明攒批窗口白等了。
//
//   - **续跑一段长会话要读多久。** Load 走的是崩溃恢复那条完整路径（逐行解、
//     验 seq 连续、断尾修复判定），不是裸读文件。
//
//   - **会话攒多了列表还快不快。** List 要打开根下每一份存档读它的头。
//
// 这些数字跟着盘走：同一份代码在机械盘、SSD 和 tmpfs 上能差两个数量级。跨机器
// 比绝对值没有意义，要比的是**同一台机器上的相对关系**和回归。

package jsonl

import (
	"context"
	"strconv"
	"testing"

	"github.com/snight1983/ds-harness-go/session"
)

// benchEvents 造一段 seq 从 from 起、长度为 count 的合法日志。
//
// 形状按回合排：turn/start、user、assistant、turn/end。存储那一侧要求 seq 连续
// 且回合是关掉的，所以不能随手拿同一条事件重复 count 次。
func benchEvents(tb testing.TB, from, count int) []session.Event {
	tb.Helper()

	events := make([]session.Event, 0, count)
	turn := from + 1
	for len(events) < count {
		seq := from + len(events)
		switch (len(events)) % 4 {
		case 0:
			events = append(events, turnStartEvent(tb, seq, seq+1, turn))
		case 1:
			events = append(events, userMessageEvent(tb, seq, seq+1, "问题"))
		case 2:
			events = append(events, assistantMessageEvent(tb, seq, seq+1, turn, 1, "回答"))
		case 3:
			events = append(events, turnEndEvent(tb, seq, seq+1, turn))
			turn++
		}
	}
	return events
}

// BenchmarkStoreAppendBatch 量的是每批 size 条事件的落盘成本。
//
// 读法：看 ns/op 除以 size。batch=1 那一档基本就是一次 fsync 的价钱，batch=100
// 把同一次 fsync 摊给一百条。两者的每事件成本之比，就是攒批窗口买到的东西。
func BenchmarkStoreAppendBatch(b *testing.B) {
	for _, size := range []int{1, 10, 100} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			ctx := context.Background()
			store, _ := newTestStore(b, Config{})
			meta := testMeta("bench-append", testCwd(b, "/bench"))
			mustCreate(b, store, meta)

			// 造事件是纯 CPU 的 JSON 排布，混进计时里会把 I/O 的信号冲淡，
			// 所以停表造、开表写。不能预造 b.N 批再一口气写：seq 必须连续
			// 递增，没法循环复用同一批。
			b.ReportAllocs()
			b.ResetTimer()
			for index := range b.N {
				b.StopTimer()
				batch := benchEvents(b, index*size, size)
				b.StartTimer()
				if err := store.Append(ctx, meta.ID, batch); err != nil {
					b.Fatalf("追加失败：%v", err)
				}
			}
		})
	}
}

// BenchmarkStoreLoad 量的是把一份长存档读回来的成本——续跑一个旧会话的等待下限。
//
// 走的是完整的恢复路径，不是裸读，所以这个数里含逐行解码、seq 连续性校验和断尾
// 判定。它按定义随日志长度线性。
func BenchmarkStoreLoad(b *testing.B) {
	for _, size := range []int{1_000, 10_000} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			ctx := context.Background()
			store, _ := newTestStore(b, Config{})
			meta := testMeta("bench-load", testCwd(b, "/bench"))
			mustCreate(b, store, meta)
			mustAppend(b, store, meta.ID, benchEvents(b, 0, size))

			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				loaded, err := store.Load(ctx, meta.ID)
				if err != nil {
					b.Fatalf("装载失败：%v", err)
				}
				if len(loaded.Events) != size {
					b.Fatalf("读回 %d 条，该是 %d 条", len(loaded.Events), size)
				}
			}
		})
	}
}

// BenchmarkBackendList 量的是列出根下全部会话的成本。它要逐份存档开一次文件读
// 它的头，所以随会话数线性——一个跑了很久的工作站上，这条决定「会话列表」有多
// 卡。
func BenchmarkBackendList(b *testing.B) {
	for _, count := range []int{10, 100, 1_000} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			ctx := context.Background()
			store, _ := newTestStore(b, Config{})
			cwd := testCwd(b, "/bench")
			for index := range count {
				meta := testMeta(session.SessionID("bench-"+strconv.Itoa(index)), cwd)
				mustCreate(b, store, meta)
				mustAppend(b, store, meta.ID, benchEvents(b, 0, 4))
			}
			backend := store.Backend()

			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				headers, err := backend.List(ctx)
				if err != nil {
					b.Fatalf("列举失败：%v", err)
				}
				if len(headers) != count {
					b.Fatalf("列出 %d 份，该是 %d 份", len(headers), count)
				}
			}
		})
	}
}
