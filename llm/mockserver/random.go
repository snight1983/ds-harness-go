// 本文件的作用：可重放的伪随机，以及按权重挑一种具体行为。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:591-615

package mockserver

import (
	"crypto/rand"
	"encoding/binary"
)

// seededRandom 造一个 mulberry32 生成器，交出 [0, 1) 上的数。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:591-600
//
// 新增: 没有换成 math/rand/v2。那不是「Go 有现成的就别自己写」的场合——本函数
// 要保证的是**同一个种子在哪一边都选出同一串行为**：一份剧本在 DSH 下跑挂了，
// 拿着那个种子到这边来必须能原样重现，反过来也一样。换成 PCG 会把这条跨实现的
// 可重放性弄丢，而它正是种子这个概念存在的全部理由。
//
// mulberry32 本身是六行 uint32 算术。JS 那边为此要写满 >>> 0 和 Math.imul 来
// 把浮点数按回 32 位整数，Go 的 uint32 天生就是这个语义，所以这一版反而更短。
func seededRandom(seed uint32) func() float64 {
	state := seed
	return func() float64 {
		state += 0x6d2b79f5
		mixed := state
		mixed = (mixed ^ (mixed >> 15)) * (mixed | 1)
		mixed ^= mixed + (mixed^(mixed>>7))*(mixed|61)
		return float64(mixed^(mixed>>14)) / (1 << 32)
	}
}

// generateSeed 在调用方没给种子时生成一个。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:228
//
// 新增: 用 crypto/rand 而不是 math/rand。不是为了安全——种子不保护任何东西——
// 而是因为 math/rand 的全局源被同进程里别人播过种就会牵连到这里，而这颗种子
// 唯一的职责是让两次跑互不相同。
//
// 不返回 error：[crypto/rand.Read] 的契约是「从不返回错误，且一定填满」，真出
// 不来随机数时它自己就把进程崩掉了。为一条按标准库契约走不到的分支留一个 error，
// 会让它一路传染到 [resolveOptions] 和 [Start] 的签名上，而那一整条路径永远验
// 不到——一段永远跑不到的代码等于一段没验过的代码。
func generateSeed() uint32 {
	var raw [4]byte
	_, _ = rand.Read(raw[:])
	return binary.LittleEndian.Uint32(raw[:])
}

// chooseRandomBehavior 按权重挑一种具体行为。
//
// 源: packages/test-support/llm-mock-server/src/index.ts:602-615
//
// weights 里每一项的权重都为正（[resolveRandomWeights] 已经把零权重滤掉了），
// 所以在 draw 严格小于总权重的前提下循环一定会命中。[seededRandom] 交出的数
// 严格小于 1，乘上总权重之后仍严格小于总权重——理论上循环走不到底。
//
// 新增: TS 在循环后面留了一条 v8 ignore 标着的兜底，专挡浮点减法在上界留下的
// 舍入残渣。这里换个写法把那条分支消掉：循环只走前 n-1 项，谁都没命中就是最后
// 一项。行为完全一样，但不再有一段跑不到、因而也验不了的代码，而且不需要在
// 循环里每轮都判一次「是不是最后一项」。
func chooseRandomBehavior(weights []weightedBehavior, random func() float64) Behavior {
	total := 0.0
	for _, entry := range weights {
		total += entry.weight
	}
	draw := random() * total
	last := len(weights) - 1
	for _, entry := range weights[:last] {
		if draw < entry.weight {
			return entry.behavior
		}
		draw -= entry.weight
	}
	return weights[last].behavior
}
