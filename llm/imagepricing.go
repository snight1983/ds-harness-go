// 本文件的作用：请求图片的**提供方侧计价**这道接缝——一条确切路由上，每一张
// 留下来的图在线上到底值多少，以及随它一起发出去的那段模型可见文本是什么。
//
// 源: packages/llm/llm/src/types.ts:151-178、packages/llm/llm/src/index.ts:217-224, 651-662
//
// 它和 [RequestImageOffloadPolicy] 是两件事：那一边决定**哪几张图还留在请求里**，
// 这一边回答**留下来的那几张各值多少**。计价方只报视觉 token 和那段文本，
// 文本值多少由问的人用它自己那把尺子去量——不这么切的话，一份提供方计价就等于
// 顺手把文本的分词方式也定死了。

package llm

import "github.com/snight1983/ds-harness-go/attachment"

// ImageRequestPrice 是一张图在某一条确切路由的请求投影里的价。
//
// 源: packages/llm/llm/src/types.ts:159-163（LlmImageRequestPrice）
//
// 「一张图」指的是**一次出现**：同一张附件在同一次请求里出现两遍就是两条价。
type ImageRequestPrice struct {
	// VisualTokens 是这次出现留在请求里的那张图的提供方视觉 token 数。
	//
	// 这次出现只由文本代表（卸载占位、纯文本替换）时是 0。
	VisualTokens int
	// Text 是随这次出现一起发出去、或者代替它发出去的那段模型可见文本。
	//
	// 它由问价的一方用自己那把文本尺子去量，计价方不替它定分词方式。
	Text string
}

// ImageRequestPricing 是一条确切路由的请求图片计价。
//
// 源: packages/llm/llm/src/types.ts:165-178（LlmImageRequestPricing）
//
// 由那些「提供方真按视觉 token 收钱」的适配器实现。问它的人（token 计量器）
// 是在一次同步的测量里问的，所以实现**不许做 I/O**。
type ImageRequestPricing interface {
	// PriceImages 给一次请求投影里的每一次图片出现定价。
	//
	// 交回来的价必须和 images **按下标一一对齐**，条数也要一样。
	PriceImages(images []attachment.ImageRef) []ImageRequestPrice
}

// ImageRequestPricer 是一个适配器**可以**实现的：交出它某一条确切路由的图片计价。
//
// 源: packages/llm/llm/src/index.ts:221-224
//
// 第二个返回值为假表示这条路由不报价，等价于 DSH 那边返回 undefined；
// 问的人于是退回自己那套中性估算。
type ImageRequestPricer interface {
	// ImageRequestPricing 交出这条确切路由的图片计价。
	ImageRequestPricing(provider, model string) (ImageRequestPricing, bool)
}

// AdapterImageRequestPricing 问适配器要一条确切路由的图片计价，它答不上来就是不报价。
//
// 源: packages/llm/llm/src/index.ts:221-224
func AdapterImageRequestPricing(adapter Adapter, provider, model string) (ImageRequestPricing, bool) {
	if pricer, ok := adapter.(ImageRequestPricer); ok {
		return pricer.ImageRequestPricing(provider, model)
	}
	return nil, false
}

// ImageRequestPricing 解算某一条确切路由的图片计价。第二个返回值为假表示这条路由
// 没登记、或者登记了但不报价。
//
// 源: packages/llm/llm/src/index.ts:651-662
//
// 认不得的提供方在这里**降级**成「不报价」，不报错：问价的人量的是一段耐久历史，
// 而那段历史当初走的那条路由此刻可能已经摘掉了。这和 [Runtime.ProviderRetryPolicy]
// 那边「认不得就报 NO_ADAPTER」是有意分开的——那一边在派发前问，路由不在就发不出去。
func (r *Runtime) ImageRequestPricing(provider, model string) (ImageRequestPricing, bool) {
	registration, err := r.registration(provider)
	if err != nil {
		return nil, false
	}
	return AdapterImageRequestPricing(registration.adapter, provider, model)
}
