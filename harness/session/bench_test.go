// 本文件的作用：长会话的压力基线。
//
// 量的是**代价随日志长度怎么长**，不是某一次调用有多快。会话是事件溯源的，
// 日志只增不改，所以每一条读路径都有退化成 O(日志长度) 的可能；把 1k / 10k /
// 100k 三档并排跑出来，一条本该是常数的路径一旦变成线性，在这三行数字上一眼
// 就能看见。
//
// 判读方法：同一个 Benchmark 的三档 ns/op 应当**基本持平**的，是
// AppendOnLongLog 与 DeriveMessagesIncremental——它们是热路径，每回合都走。
// 允许随长度线性增长的，是 RestoreFromSeed、DeriveMessagesCold 和
// EventsSnapshot——它们按定义就要碰全量日志，量它们是为了给「续跑一段很长的
// 会话要等多久」定一个数。

package session

import (
	"strconv"
	"testing"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// benchLogSizes 是三档日志长度。100k 那一档已经远超真实会话，留着是为了让
// 线性项在噪声里显形——1k 和 10k 之间的差可能被内存分配的抖动盖住。
var benchLogSizes = []int{1_000, 10_000, 100_000}

// benchSink 收住那些只被取长度的返回值。
//
// 没有它，`len(session.Events())` 会被优化成 `len(内部切片)`——那次拷贝整个消失，
// 量出来是 4 ns / 0 allocs，而这个数是假的。包级变量逃逸，编译器删不掉赋值。
var benchSink []sessionlog.Event

// benchSeed 造一份长度为 size 的 seed，形状照着真实会话：一条用户消息之后跟
// 若干条助手消息和工具结果，外面裹着回合边界。
//
// 回合边界和分块这些**不上表面**的事件必须掺进去：派生历史只走表面节点，一份
// 全是表面事件的 seed 会让 DeriveMessages 看起来比实际便宜。
func benchSeed(tb testing.TB, size int) []sessionlog.Event {
	tb.Helper()
	events := make([]sessionlog.Event, 0, size)
	turn := 0
	for len(events) < size {
		turn++
		events = append(events, turnStart(turn))
		events = append(events, userEvent(tb, "第 "+strconv.Itoa(turn)+" 轮的问题"))
		for step := 1; step <= 3 && len(events) < size; step++ {
			events = append(events, assistantEvent(tb, turn, step, "第 "+strconv.Itoa(step)+" 步的回答"))
			events = append(events, toolResultEvent(tb, turn, step, llm.CallID("call-"+strconv.Itoa(step))))
		}
		events = append(events, turnEnd(turn))
	}
	return seedOf(events[:size]...)
}

// benchSession 造一个已经装着 size 条事件的游离会话。
func benchSession(tb testing.TB, size int) *Session {
	tb.Helper()
	session, err := NewSession("bench", Options{Seed: benchSeed(tb, size), Now: fixedClock()})
	if err != nil {
		tb.Fatalf("造会话失败：%v", err)
	}
	return session
}

// BenchmarkSessionAppendOnLongLog 量的是**稳态**追加：日志已经很长了，再追加
// 一条要多少钱。这个数必须随长度持平——追加是每一步都走的热路径，它一旦随日志
// 长度增长，长会话就会越跑越慢。
func BenchmarkSessionAppendOnLongLog(b *testing.B) {
	for _, size := range benchLogSizes {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			session := benchSession(b, size)
			event := userEvent(b, "追加")
			appended := 0
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				// 日志涨到两倍就换一份新的。不换的话 b.N 在时间制的
				// benchtime 下会到百万级，一条一条追下去能把内存吃光；
				// 而 [size, 2*size] 这个区间照样是「已经很长的日志」。
				if appended == size {
					b.StopTimer()
					session = benchSession(b, size)
					appended = 0
					b.StartTimer()
				}
				if _, err := session.Append(event); err != nil {
					b.Fatalf("追加失败：%v", err)
				}
				appended++
			}
		})
	}
}

// BenchmarkSessionDeriveMessagesIncremental 量的是稳态的增量折叠：追加一条再
// 派生一次。派生有缓存，只投影新表面节点，所以这个数也该随长度持平。
//
// 交回的切片每次都是新的（见 [Session.DeriveMessages] 的注释），所以这里必然
// 含一次 O(消息数) 的拷贝——这一项**是**线性的，是设计如此。把它和
// AppendOnLongLog 的差读成「那次 Clone 的钱」。
func BenchmarkSessionDeriveMessagesIncremental(b *testing.B) {
	for _, size := range benchLogSizes {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			session := benchSession(b, size)
			if _, err := session.DeriveMessages(); err != nil {
				b.Fatalf("预热派生失败：%v", err)
			}
			event := userEvent(b, "追加")
			appended := 0
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				// 同 [BenchmarkSessionAppendOnLongLog]：日志涨到两倍就换一份。
				if appended == size {
					b.StopTimer()
					session = benchSession(b, size)
					if _, err := session.DeriveMessages(); err != nil {
						b.Fatalf("预热派生失败：%v", err)
					}
					appended = 0
					b.StartTimer()
				}
				if _, err := session.Append(event); err != nil {
					b.Fatalf("追加失败：%v", err)
				}
				if _, err := session.DeriveMessages(); err != nil {
					b.Fatalf("派生失败：%v", err)
				}
				appended++
			}
		})
	}
}

// BenchmarkSessionDeriveMessagesCold 量的是冷启动那一次全量折叠：一个刚从存档
// 回放出来的会话，第一次要历史要等多久。这条按定义随长度线性。
func BenchmarkSessionDeriveMessagesCold(b *testing.B) {
	for _, size := range benchLogSizes {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			seed := benchSeed(b, size)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				b.StopTimer()
				session, err := NewSession("bench", Options{Seed: seed, Now: fixedClock()})
				if err != nil {
					b.Fatalf("造会话失败：%v", err)
				}
				b.StartTimer()
				if _, err := session.DeriveMessages(); err != nil {
					b.Fatalf("派生失败：%v", err)
				}
			}
		})
	}
}

// BenchmarkSessionRestoreFromSeed 量的是续跑一段长会话的构造成本：验 seed、
// 重建表面、补结束标记。这是「打开一个旧会话」的下限耗时。
func BenchmarkSessionRestoreFromSeed(b *testing.B) {
	for _, size := range benchLogSizes {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			seed := benchSeed(b, size)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := NewSession("bench", Options{Seed: seed, Now: fixedClock()}); err != nil {
					b.Fatalf("造会话失败：%v", err)
				}
			}
		})
	}
}

// BenchmarkSessionEventsSnapshot 量的是取一份日志快照的钱。持久化、压缩和调试
// 面都会调它，量它是为了让这些调用方知道在长会话上轮询有多贵。
//
// 量的是**快照失效之后**那一次：[Session.Events] 把整份拷贝记住了，同一份快照
// 在下一次追加之前会被反复交出，所以连着空调它量到的是缓存命中，不是拷贝。
// 循环里先追一条把缓存打掉，再取一次——这才是真实调用方的节奏（写一条、存一次）。
//
// 于是这个数里含一次 Append。要单看拷贝的钱，减掉
// [BenchmarkSessionAppendOnLongLog] 同一档的数字。这一条按定义随长度线性。
func BenchmarkSessionEventsSnapshot(b *testing.B) {
	for _, size := range benchLogSizes {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			session := benchSession(b, size)
			event := userEvent(b, "追加")
			appended := 0
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				// 同 [BenchmarkSessionAppendOnLongLog]：日志涨到两倍就换一份。
				if appended == size {
					b.StopTimer()
					session = benchSession(b, size)
					appended = 0
					b.StartTimer()
				}
				if _, err := session.Append(event); err != nil {
					b.Fatalf("追加失败：%v", err)
				}
				appended++
				benchSink = session.Events()
				if len(benchSink) == 0 {
					b.Fatal("快照是空的")
				}
			}
		})
	}
}
