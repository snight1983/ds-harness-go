// 本文件的作用：多 agent 并存时那些共享结构的压力基线。
//
// 一台机器上会同时活着很多个 agent（子 agent 委派、多个会话并行），而注册表是
// 它们唯一的公共点：每一次状态变化、每一次收件箱插入、每一次登记和摘除都要过
// 它的锁。所以这里量的是**争用**，不是单个 agent 跑得快不快。
//
// 用的是 fakeAgent 而不是真的循环：真循环要连模型，那量出来的是 mock 服务的
// 延迟。要问的问题是「一百个 agent 同时活着的时候，注册表这一层会不会成为瓶颈」，
// 这个问题只跟注册表自己有关。
//
// 收件箱那一组是对照：它按设计**不加锁**（只被自己那个 agent 的循环碰，见
// [Inbox] 的注释），量它是为了给每一条消息进出待办的成本定个数，也为了在有人
// 哪天给它加锁时能看见代价。

package agent

import (
	"context"
	"strconv"
	"testing"

	"github.com/snight1983/ds-harness-go/llm"
	sessionlog "github.com/snight1983/ds-harness-go/session"
)

// benchAgentCounts 是三档并存 agent 数。1000 那一档超出真实用量，留着是为了让
// 注册表里任何一处线性扫描显形。
var benchAgentCounts = []int{10, 100, 1_000}

// benchPopulate 往表里塞 count 个已公布的 agent，交回它们和那张表。
func benchPopulate(tb testing.TB, count int) (*Registry, []*fakeAgent) {
	tb.Helper()
	registry := newRegistry(tb)
	agents := make([]*fakeAgent, count)
	for index := range count {
		agent := newFakeAgent(tb, "bench-"+strconv.Itoa(index), nil)
		live(tb, registry, agent, nil)
		agents[index] = agent
	}
	return registry, agents
}

// BenchmarkRegistryObserverFanoutConcurrent 量的是多 agent 同时往表上报事件时的争用。
//
// 这是最热的那条共享路径：每报一件事都要过表的锁去收观察者，再逐层派发。
// `b.RunParallel` 让 GOMAXPROCS 个 goroutine 同时报，ns/op 是每次上报的平均
// 成本——它随并存 agent 数增长就说明收观察者那一步在扫全表。
//
// 报的是收件箱插入而不是状态：状态那条路上有「同一个状态不许连报两次」的校验，
// 并发地满足它需要在 benchmark 里另加一层同步，而那层同步会盖住要量的争用。
func BenchmarkRegistryObserverFanoutConcurrent(b *testing.B) {
	for _, count := range benchAgentCounts {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			registry, agents := benchPopulate(b, count)
			message := text("通知")
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				// 每个 goroutine 走自己的游标，免得它们全撞在同一个 agent 上
				// ——那样量到的是一个 agent 的锁，不是这张表的锁。
				next := 0
				for pb.Next() {
					next++
					if err := registry.ReportInboxInserted(agents[next%len(agents)], message); err != nil {
						b.Errorf("上报收件箱插入失败：%v", err)
					}
				}
			})
		})
	}
}

// BenchmarkRegistryRegisterDetachConcurrent 量的是并发地登记和摘除。
//
// 子 agent 的委派会在一次回合里造出并销毁一批 agent，这条路径上写锁是必然的，
// 所以它的数字比读路径贵得多是正常的。要看的是它随并存数**别**增长。
func BenchmarkRegistryRegisterDetachConcurrent(b *testing.B) {
	for _, count := range benchAgentCounts {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			ctx := context.Background()
			registry, _ := benchPopulate(b, count)
			// 预造一小撮进出用的 agent 循环使用：造作用域和会话是纯 CPU 的
			// 活儿，不属于这条路径要量的东西；而按 b.N 预造会在时间制的
			// benchtime 下造出几百万个作用域，把内存吃光。
			churn := make([]*fakeAgent, 64)
			for index := range churn {
				churn[index] = newFakeAgent(b, "churn-"+strconv.Itoa(index), nil)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for index := range b.N {
				agent := churn[index%len(churn)]
				detach, err := registry.Register(ctx, agent, nil)
				if err != nil {
					b.Fatalf("登记失败：%v", err)
				}
				if err := detach(ctx); err != nil {
					b.Fatalf("摘除失败：%v", err)
				}
			}
		})
	}
}

// benchSink 收住那份只被取长度的名单。直接写 `len(registry.List())` 有被编译器
// 折成 `len(内部切片)` 的风险——那次拷贝会整个消失，量出来的是一个假数。包级变量
// 逃逸，编译器删不掉这次赋值。
var benchSink []Agent

// BenchmarkRegistryList 量的是列出全部活 agent 的成本。它每次都整份拷贝，所以
// 按定义线性；量它是为了让「轮询这张表」的调用方知道账单。
func BenchmarkRegistryList(b *testing.B) {
	for _, count := range benchAgentCounts {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			registry, _ := benchPopulate(b, count)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				benchSink = registry.List()
				if got := len(benchSink); got != count {
					b.Fatalf("列出 %d 个，该是 %d 个", got, count)
				}
			}
		})
	}
}

// BenchmarkInboxAppendClaim 量的是 size 条消息进待办再被整批认领走的来回。
//
// 走的是 next-step 那条清单：[Inbox.Claim] 每次把整条 next-step 带走，而
// next-turn 只带队首一条，所以只有 next-step 能把「一批」这件事量出来。真实
// 对应的是连着几次 steer 之后下一步把它们一起收走。
//
// 每一次 Append 都要往会话日志里写一条 inbox/spliced 事件，所以这个数里含一次
// JSON 排布和一次会话追加——它比「往切片上加一个元素」贵得多是设计如此，耐久的
// 事实永远在日志上。
func BenchmarkInboxAppendClaim(b *testing.B) {
	for _, size := range []int{1, 10, 100} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			inbox, _ := newInbox(b, InboxNotifications{})
			messages := make([]llm.Message, size)
			for index := range messages {
				messages[index] = text("待办 " + strconv.Itoa(index))
			}

			// 每一次进出都往会话日志上加 size+1 条事件，日志只增不改，所以在
			// 时间制的 benchtime 下要隔一段换一份新的，不然内存会被日志吃光。
			// 两万条是一个「已经跑了很久」的量。
			const logBudget = 20_000
			written := 0

			b.ReportAllocs()
			b.ResetTimer()
			for turn := range b.N {
				if written >= logBudget {
					b.StopTimer()
					inbox, _ = newInbox(b, InboxNotifications{})
					written = 0
					b.StartTimer()
				}
				for _, message := range messages {
					if err := inbox.Append(NextStep, message); err != nil {
						b.Fatalf("入待办失败：%v", err)
					}
				}
				claimed, err := inbox.Claim(NextStep, turn)
				if err != nil {
					b.Fatalf("认领失败：%v", err)
				}
				if len(claimed) != size {
					b.Fatalf("认领到 %d 条，该是 %d 条", len(claimed), size)
				}
				written += size + 1
			}
		})
	}
}

// BenchmarkInboxReplayOnResume 量的是续跑一个待办很长的 agent：从会话日志把
// 待办清单重放出来。这是 [NewInbox] 的冷启动成本，随日志里的 splice 条数线性。
func BenchmarkInboxReplayOnResume(b *testing.B) {
	for _, size := range []int{100, 1_000} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			seed := make([]sessionlog.Event, size)
			for index := range seed {
				seed[index] = sessionlog.Event{
					Seq:  index,
					Type: EventInboxSpliced,
					Data: data(b, SplicedData{
						Target:   NextTurn,
						Inserted: []llm.Message{text("待办 " + strconv.Itoa(index))},
					}),
				}
			}

			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				b.StopTimer()
				live := newFreeSession(b, "resume", seed)
				b.StartTimer()
				if _, err := NewInbox(live, InboxNotifications{}); err != nil {
					b.Fatalf("重放待办失败：%v", err)
				}
			}
		})
	}
}
