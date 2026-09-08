// 本文件的作用：钉住请求图片计价这道接缝的三条路——适配器报价、适配器压根不实现
// 这件事、以及那条路由根本没登记。
//
// # 这些测试防的是什么错
//
//   - 一个不实现计价的适配器被当成「报了一份空计价」，于是每张图都被算成 0；
//   - 一条已经摘掉的路由让问价的一方拿到错误，而它量的是一段耐久历史——那段历史
//     当初走的路由此刻本来就可能不在了。

package llm

import (
	"testing"

	"github.com/snight1983/ds-harness-go/attachment"
)

// pricingAdapter 是一个报价的假适配器：它把每一次图片出现都按同一个价报回去。
type pricingAdapter struct {
	bareAdapter
	visualTokens int
	text         string
}

func (a *pricingAdapter) ImageRequestPricing(provider, model string) (ImageRequestPricing, bool) {
	if model == "silent" {
		return nil, false
	}
	return fixedPricing{visualTokens: a.visualTokens, text: a.text}, true
}

// fixedPricing 给每一次出现报同一个价。
type fixedPricing struct {
	visualTokens int
	text         string
}

func (p fixedPricing) PriceImages(images []attachment.ImageRef) []ImageRequestPrice {
	prices := make([]ImageRequestPrice, 0, len(images))
	for range images {
		prices = append(prices, ImageRequestPrice{VisualTokens: p.visualTokens, Text: p.text})
	}
	return prices
}

func TestImageRequestPricingComesFromTheRoutedAdapter(t *testing.T) {
	t.Parallel()

	runtime := newTestRuntime(t)
	registerFake(t, runtime, "p", &pricingAdapter{visualTokens: 85, text: "[image]"})

	pricing, ok := runtime.ImageRequestPricing("p", "m")
	if !ok {
		t.Fatal("这条路由的适配器报价了，该拿得到")
	}
	prices := pricing.PriceImages([]attachment.ImageRef{{}, {}})
	if len(prices) != 2 || prices[0].VisualTokens != 85 || prices[1].Text != "[image]" {
		t.Fatalf("报回来的价不对：%+v", prices)
	}
}

// 一条报价的适配器仍然可以对某个具体模型说「这条我不报」。
func TestImageRequestPricingCanDeclineOneRoute(t *testing.T) {
	t.Parallel()

	runtime := newTestRuntime(t)
	registerFake(t, runtime, "p", &pricingAdapter{visualTokens: 85})

	if _, ok := runtime.ImageRequestPricing("p", "silent"); ok {
		t.Fatal("适配器对这条路由不报价，不该拿得到")
	}
}

// 不实现这件可选的事的适配器就是不报价，而不是报了一份空的。
func TestAdapterWithoutPricingDoesNotReport(t *testing.T) {
	t.Parallel()

	if _, ok := AdapterImageRequestPricing(&bareAdapter{}, "p", "m"); ok {
		t.Fatal("不实现计价的适配器不该被当成报了价")
	}
}

// 认不得的提供方降级成「不报价」，不报错：问价的一方量的是一段耐久历史，
// 那段历史当初走的那条路由此刻可能已经摘掉了。
func TestUnknownProviderDegradesToNoPricing(t *testing.T) {
	t.Parallel()

	runtime := newTestRuntime(t)
	if _, ok := runtime.ImageRequestPricing("gone", "m"); ok {
		t.Fatal("一条没登记的路由不该报出价来")
	}
}
