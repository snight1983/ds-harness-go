// 本文件的作用：关停路径的压力基线。
//
// 这个仓库里所有东西的收摊都归到同一处：谁占了资源，谁就往自己那个作用域上
// [Scope.Defer] 一份清理，关停就是把作用域释放掉。一个活着的进程上，agent、
// 会话、观察者登记、通道、持久化编排器全都在往作用域上挂清理，所以「关停要多久」
// 这个问题等价于「释放一个挂了 N 份清理的作用域要多久」。
//
// 三件事分开量：
//
//   - Dispose：一次性释放 N 份清理，也就是进程收摊那一下。
//   - DeferAndCancel：单项 disposer 一个一个撤，也就是运行期里正常的进出。
//     它走的是链表摘除，代价该是常数——退化成线性的话，一个开开关关很频繁的
//     长命进程会越跑越慢。
//   - Churn：一边挂一边撤，稳态下作用域上始终挂着 N 份。这是真实形状。
//
// 清理函数本身是空的：真实的清理各干各的（关文件、停 goroutine），那些成本属于
// 各自那一层。这里量的是作用域这套记账本身。

package scope

import (
	"context"
	"strconv"
	"testing"
)

// benchTeardownCounts 是三档挂载量。10000 那一档超出真实进程，留着是为了让任何
// 一处二次方行为显形——释放路径要倒着走一遍链表，写错就是 O(N²)。
var benchTeardownCounts = []int{100, 1_000, 10_000}

// noop 是一份什么都不做的清理。
func noop(context.Context) error { return nil }

// BenchmarkScopeDispose 量的是一次性释放挂了 count 份清理的作用域。
//
// 这是进程收摊那一下的耗时下限。它该随 count 线性——超线性说明释放路径上有一处
// 按元素重新扫链表。
func BenchmarkScopeDispose(b *testing.B) {
	for _, count := range benchTeardownCounts {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				b.StopTimer()
				owner := NewRoot()
				for index := range count {
					if _, err := owner.Defer("bench-"+strconv.Itoa(index), noop); err != nil {
						b.Fatalf("登记清理失败：%v", err)
					}
				}
				b.StartTimer()
				if err := owner.Dispose(ctx); err != nil {
					b.Fatalf("释放失败：%v", err)
				}
			}
		})
	}
}

// BenchmarkScopeDeferAndCancel 量的是挂一份清理再单独撤掉它，作用域上另有
// count 份挂着当背景。
//
// 这是运行期的常态：一个 agent 起来又结束、一条通道开了又关，每一次都是一挂
// 一撤。ns/op 必须随 count 持平——它跟着背景量涨，说明摘除在扫链表。
func BenchmarkScopeDeferAndCancel(b *testing.B) {
	for _, count := range benchTeardownCounts {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			ctx := context.Background()
			owner := NewRoot()
			b.Cleanup(func() { _ = owner.Dispose(ctx) })
			for index := range count {
				if _, err := owner.Defer("背景-"+strconv.Itoa(index), noop); err != nil {
					b.Fatalf("登记背景清理失败：%v", err)
				}
			}

			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				cancel, err := owner.Defer("进出", noop)
				if err != nil {
					b.Fatalf("登记清理失败：%v", err)
				}
				if err := cancel(ctx); err != nil {
					b.Fatalf("撤销清理失败：%v", err)
				}
			}
		})
	}
}

// BenchmarkScopeDisposeAfterChurn 量的是「开开关关了很久之后」再收摊要多久。
//
// 先做 count 次一挂一撤，再挂 count 份留着不撤，然后释放。撤掉的那些必须真的从
// 链表上下来了——如果它们只是被打了标记还留在链上，这条的耗时会明显高于同档的
// [BenchmarkScopeDispose]，那就是一处泄漏。
func BenchmarkScopeDisposeAfterChurn(b *testing.B) {
	for _, count := range benchTeardownCounts {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				b.StopTimer()
				owner := NewRoot()
				for index := range count {
					cancel, err := owner.Defer("进出-"+strconv.Itoa(index), noop)
					if err != nil {
						b.Fatalf("登记清理失败：%v", err)
					}
					if err := cancel(ctx); err != nil {
						b.Fatalf("撤销清理失败：%v", err)
					}
				}
				for index := range count {
					if _, err := owner.Defer("留下-"+strconv.Itoa(index), noop); err != nil {
						b.Fatalf("登记清理失败：%v", err)
					}
				}
				b.StartTimer()
				if err := owner.Dispose(ctx); err != nil {
					b.Fatalf("释放失败：%v", err)
				}
			}
		})
	}
}
