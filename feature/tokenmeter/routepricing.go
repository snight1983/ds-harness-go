// 本文件的作用：把折叠留下的那张固定尺子节点表，投到**一条确切路由**的请求上。
// 它做的只有一件事：把每一次图片出现的结构价，换成那条路由自己报的视觉 token
// 加上它实际发出去的那段模型可见文本。
//
// 路由不报价（多数适配器都不报）时每个节点原样留着固定估价，所以这一步对一条
// 提供方中立的路由是**完全透明**的。
//
// 源: packages/llm/token-meter/src/route-pricing.ts

package tokenmeter

import (
	"fmt"

	"github.com/snight1983/ds-harness-go/attachment"
	"github.com/snight1983/ds-harness-go/llm"
)

// pricedSurface 是一整张按某条路由定完价的表面：对外那份节点表，加上它们的合计。
//
// 源: packages/llm/token-meter/src/route-pricing.ts:17-22（PricedSurface）
type pricedSurface struct {
	// nodes 是逐节点的对外形态，路由价和固定估价两个都带着。
	nodes []SurfaceNode
	// surfaceTokens 是整张表面路由价的合计。
	surfaceTokens int
}

// priceSurface 按一条路由的请求图片计价，给一张有序表面定价。
//
// 源: packages/llm/token-meter/src/route-pricing.ts:32-68（priceSurface）
//
// pricing 为 nil 表示这条路由不报价，那就每个节点原样留着固定估价。
//
// 报回来的价数和问的次数**对不上就当场失败**：一次错位会让后面每一个节点都
// 配上别人的价，而那种错悄无声息，只会体现成一个偏掉的总量。
func priceSurface(nodes []meterNode, pricing llm.ImageRequestPricing) (pricedSurface, error) {
	var images []attachment.ImageRef
	if pricing != nil {
		for _, node := range nodes {
			images = append(images, node.images...)
		}
	}
	if pricing == nil || len(images) == 0 {
		surfaceTokens := 0
		publicNodes := make([]SurfaceNode, 0, len(nodes))
		for _, node := range nodes {
			surfaceTokens += node.heuristicTokens
			publicNodes = append(publicNodes, SurfaceNode{
				Seq:             node.seq,
				Tokens:          node.heuristicTokens,
				HeuristicTokens: node.heuristicTokens,
			})
		}
		return pricedSurface{nodes: publicNodes, surfaceTokens: surfaceTokens}, nil
	}

	prices := pricing.PriceImages(images)
	if len(prices) != len(images) {
		return pricedSurface{}, fmt.Errorf(
			"token 表面：这条路由的图片计价对 %d 次出现报了 %d 条价", len(images), len(prices))
	}

	cursor := 0
	surfaceTokens := 0
	publicNodes := make([]SurfaceNode, 0, len(nodes))
	for _, node := range nodes {
		tokens := node.heuristicTokens
		if len(node.images) > 0 {
			tokens = node.imageFreeTokens
			for range node.images {
				price := prices[cursor]
				cursor++
				textTokens, err := EstimateContent(llm.Content{llm.TextBlock{Text: price.Text}})
				if err != nil {
					return pricedSurface{}, err
				}
				tokens += price.VisualTokens + textTokens
			}
		}
		surfaceTokens += tokens
		publicNodes = append(publicNodes, SurfaceNode{
			Seq:             node.seq,
			Tokens:          tokens,
			HeuristicTokens: node.heuristicTokens,
		})
	}
	return pricedSurface{nodes: publicNodes, surfaceTokens: surfaceTokens}, nil
}
