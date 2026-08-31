// 本文件的作用：这个包自己拥有的那两条运行期不变量——一条流式协议的文法，
// 和「一次已提交的拓扑变动留下的注册表是读得通的」。
//
// 源: packages/llm/llm/src/invariant.ts:1-112

package llm

import (
	"context"
	"fmt"
	"iter"

	"ds-harness-go/invariants"
)

// PackageName 是这个包在不变量注册表里占的名字。
//
// 源: packages/llm/llm/src/invariant.ts:7
//
// 沿用 DSH 侧的包名字面量而不是换成 Go 的导入路径，理由同 fs、credentials 两个包：
// 注册表是按名字预留的，而这条约定的拥有者在两边是同一个模块。
const PackageName = "@deepseek-ai/dsh-llm"

// RegisterInvariants 装上本包那两条检查，返回注销函数。
//
// 源: packages/llm/llm/src/invariant.ts:86-112
//
// # 这两条检查在查什么
//
// 第一条是**流式协议的文法**，见 [validateStream]：下标非负、增量只落在同类型的
// 开着的块上、一个下标不重复开、block-end 关的是开着的那一块且类型对得上、
// 用量最多一次、终止分块之后不再有别的分块、以及一条正常结束的流必须以终止分块
// 收尾。这些都不是「提供方返回了坏数据」——适配器要把提供方的毛病翻译成一个
// error 结局；违反这几条说明**适配器自己的翻译写错了**，也就是不变量该管的那类事。
//
// 第二条是拓扑：一次已提交的适配器变动通知发出去的时候，注册表必须是读得通的。
//
// # 第二条在 Go 里换了判据
//
// 新增: DSH 查的是「listProviders() 列出来的每一条，providerRetryPolicy() 都取得到」
// （invariant.ts:94-102），它防的是「服务已经从容器里摘掉了，但通知还在发」。
// Go 这边照抄不了也不该照抄：[Runtime.ListProviders] 和 [Runtime.ProviderRetryPolicy]
// 各取一次锁，两次之间另一个 goroutine 合法地释放掉一条路由是**正常并发**，不是违例，
// 照抄会得到一条随机误报的检查。
//
// 换上的判据是 Go 侧才有的那条：路由次序表和路由表必须一一对应。它是同一件事的
// Go 版本——DSH 的 Map 自己保插入顺序，Go 这边顺序是我另开的一个数组
// （见 [Runtime] 的字段注释），两张表一旦对不上，[Runtime.ListProviders] 就会
// 对着一个不存在的键解引用。这条查得准，因为它整个在一次临界区里判完。
//
// # 一个 Runtime 只该被装一次
//
// 注册表按包名预留，同一个注册表上装第二次会直接失败。用两个注册表装同一个
// [Runtime] 的话，后装的会盖掉先装的——那是一次装配错误，本包不为它兜底。
func RegisterInvariants(
	ctx context.Context,
	registry *invariants.Registry,
	runtime *Runtime,
) (func(), error) {
	if registry == nil {
		return nil, fmt.Errorf("llm: 注册不变量需要一个不变量注册表")
	}
	if runtime == nil {
		return nil, fmt.Errorf("llm: 注册不变量需要一个运行时")
	}

	install := func(_ context.Context, scope *invariants.Scope, fail invariants.Fail) error {
		runtime.mutex.Lock()
		runtime.fail = fail
		runtime.mutex.Unlock()

		// 摘掉这一步登记进 scope：注销之后，一条不该再查的检查必须停下来，
		// 否则它会继续在别人的流上抛。
		scope.Defer(func() {
			runtime.mutex.Lock()
			runtime.fail = nil
			runtime.mutex.Unlock()
		})
		return nil
	}

	return registry.Register(ctx, PackageName, install)
}

// registryViolation 判路由次序表和路由表对不对得上，对得上时交出空串。
//
// 源: packages/llm/llm/src/invariant.ts:89-103
//
// 调用方必须持有 runtime.mutex；它自己不报，因为 [invariants.Fail] 是 panic，
// 在临界区里抛会把锁永远留在锁着的状态。
func (r *Runtime) registryViolation() string {
	if len(r.adapterOrder) != len(r.adapters) {
		return fmt.Sprintf(
			"llm/adapters-updated fired with %d ordered routes but %d registered ones",
			len(r.adapterOrder), len(r.adapters))
	}
	for _, id := range r.adapterOrder {
		if _, present := r.adapters[id]; !present {
			return fmt.Sprintf(
				"llm/adapters-updated fired while provider %q has no readable registration", id)
		}
	}
	return ""
}

// validateStream 把一条流包起来，一边放行一边验它的文法。
//
// 源: packages/llm/llm/src/invariant.ts:35-84
//
// 它只**观察**：每一块原样交给下游，一块不改、一块不吞。验不过就报，而报是 panic，
// 所以下游根本收不到那一块——一条违反文法的流没有「继续读下去」这个选项。
//
// 新增: DSH 的 validateIndex 查的是 Number.isSafeInteger(index) && index >= 0。
// 前一半在 Go 里由类型管（int 就是整数，也不存在 2^53 那道坎），只剩后一半。
//
// 新增: DSH 那条流只会「正常结束」或者「抛出来」，Go 的 [iter.Seq2] 多一种走法：
// 一个非 nil 的错误。收到它就照原样放行然后收手——一条报错终止的流没有终止分块
// 是正常的，那一句「没有以终止分块收尾」不适用于它。
//
// 消费方提前 break 同样不报，理由一样：DSH 那边提前退出会让生成器从 yield 处
// 以 return 完成，最后那一句检查根本执行不到。
func validateStream(source iter.Seq2[StreamChunk, error], fail invariants.Fail) iter.Seq2[StreamChunk, error] {
	return func(yield func(StreamChunk, error) bool) {
		open := map[int]BlockType{}
		usageSeen := false
		finished := false

		for chunk, err := range source {
			if err != nil {
				yield(chunk, err)
				return
			}
			if finished {
				fail(fmt.Sprintf("LLM stream emitted %s after terminal finish", chunk.ChunkType()))
			}
			switch typed := chunk.(type) {
			case BlockStartChunk:
				validateChunkIndex(typed.Index, fail)
				if _, already := open[typed.Index]; already {
					fail(fmt.Sprintf("LLM stream repeated block-start index %d", typed.Index))
				}
				open[typed.Index] = typed.BlockType

			case TextDeltaChunk:
				validateDelta(open, typed.Index, BlockText, fail)

			case ReasoningDeltaChunk:
				validateDelta(open, typed.Index, BlockReasoning, fail)

			case ToolCallDeltaChunk:
				validateDelta(open, typed.Index, BlockToolCall, fail)

			case BlockEndChunk:
				validateChunkIndex(typed.Index, fail)
				blockType, present := open[typed.Index]
				if !present {
					fail(fmt.Sprintf("LLM stream block-end index %d has no open block", typed.Index))
				}
				if typed.Block == nil || typed.Block.BlockType() != blockType {
					fail(fmt.Sprintf("LLM stream block-end index %d closes %s, expected %s",
						typed.Index, blockTypeOf(typed.Block), blockType))
				}
				delete(open, typed.Index)

			case UsageChunk:
				if usageSeen {
					fail("LLM stream emitted usage more than once")
				}
				usageSeen = true

			case FinishChunk:
				kind := FinishStop
				if typed.Reason != nil {
					kind = typed.Reason.FinishKind()
				}
				// error 与 aborted 是**中途**停下来的两种结局，它们身上留着开着的块
				// 是这次中断本来的样子，不是文法错误。
				if len(open) > 0 && kind != FinishError && kind != FinishAborted {
					fail(fmt.Sprintf("LLM stream finished with %d open block(s)", len(open)))
				}
				finished = true
			}

			if !yield(chunk, nil) {
				return
			}
		}

		if !finished {
			fail("LLM stream ended without a terminal finish chunk")
		}
	}
}

// validateChunkIndex 要求一个块下标非负。
//
// 源: packages/llm/llm/src/invariant.ts:14-19
func validateChunkIndex(index int, fail invariants.Fail) {
	if index < 0 {
		fail(fmt.Sprintf("LLM stream block index must be non-negative, got %d", index))
	}
}

// validateDelta 要求一条增量落在一个开着的、类型对得上的块上。
//
// 源: packages/llm/llm/src/invariant.ts:21-33
func validateDelta(open map[int]BlockType, index int, expected BlockType, fail invariants.Fail) {
	validateChunkIndex(index, fail)
	actual, present := open[index]
	if !present || actual != expected {
		fail(fmt.Sprintf("%s delta at index %d requires an open %s block, got %s",
			expected, index, expected, describeOpenBlock(actual, present)))
	}
}

// describeOpenBlock 说清某个下标上此刻开着的是什么，没开着时说「没有」。
//
// 新增: DSH 那边是 String(actual)，一个 undefined 会印成 "undefined"。Go 的
// map 取不到时给的是零值空串，印出来是一对空引号，读的人看不出「没开着」和
// 「开着一块类型是空串的」的区别，所以这里把两者分开说。
func describeOpenBlock(actual BlockType, present bool) string {
	if !present {
		return "no open block"
	}
	return string(actual)
}

// blockTypeOf 说清一个 block-end 关的是什么类型，块为 nil 时说「没有」。
func blockTypeOf(block ContentBlock) string {
	if block == nil {
		return "no block"
	}
	return string(block.BlockType())
}
