// 本文件的作用：钉住路由感知的表面定价——图片那一份换成路由报的价，别的一律不动。
//
// # 这些测试防的是什么错
//
//   - 一条不报价的路由让节点的价悄悄变了，于是所有提供方中立的部署都被牵连；
//   - 报回来的价数和问的次数对不上时被将就着用，那会让每个节点都配上别人的价；
//   - 图片之外的内容跟着被重估，于是同一段文字在两条路由上算出两个数；
//   - 对外那份节点丢掉固定估价，读的人再也分不清「贵是因为图贵」还是「它本来就长」。

package tokenmeter

import (
	"testing"

	"github.com/snight1983/ds-harness-go/attachment"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// countingPricing 给每一次出现报同一个价，并数一共被问了几次。
type countingPricing struct {
	visualTokens int
	text         string
	asked        int
	// short 为真时故意少报一条价。
	short bool
}

func (p *countingPricing) PriceImages(images []attachment.ImageRef) []llm.ImageRequestPrice {
	p.asked = len(images)
	count := len(images)
	if p.short && count > 0 {
		count--
	}
	prices := make([]llm.ImageRequestPrice, 0, count)
	for range count {
		prices = append(prices, llm.ImageRequestPrice{VisualTokens: p.visualTokens, Text: p.text})
	}
	return prices
}

// imageEvent 造一条带一张图和一段文字的用户消息。
func imageEvent(t *testing.T, id string, text string) sessionlog.Event {
	t.Helper()

	message := llm.Message{
		ID:   llm.MessageID(id),
		Role: llm.RoleUser,
		Content: llm.Content{
			llm.TextBlock{Text: text},
			llm.ImageBlock{Attachment: attachment.ImageRef{
				ID: attachment.ID(id), MediaType: "image/png", Bytes: 1024, Width: 32, Height: 32,
			}},
		},
		Source: llm.UserSource{},
	}
	return sessionlog.Event{
		Type:      sessionlog.EventUserMessage,
		Data:      mustJSON(t, sessionlog.UserMessageData{Message: message}),
		SurfaceOp: sessionlog.AppendOp{},
	}
}

// 没有计价时每个节点原样留着固定估价——这是绝大多数路由的情形，它必须是恒等变换。
func TestPriceSurfaceKeepsTheHeuristicWithoutPricing(t *testing.T) {
	t.Parallel()

	view := newSession(userEvent(t, "hello"), imageEvent(t, "i1", "看这张"))
	nodes, total := foldAll(t, view.events)

	priced, err := priceSurface(nodes, nil)
	if err != nil {
		t.Fatalf("不报价的路由该定得出价：%v", err)
	}
	if priced.surfaceTokens != total {
		t.Fatalf("不报价时总价该等于固定估价：想要 %d，实际 %d", total, priced.surfaceTokens)
	}
	for index, node := range priced.nodes {
		if node.Tokens != node.HeuristicTokens || node.Tokens != nodes[index].heuristicTokens {
			t.Fatalf("第 %d 个节点被动过了：%+v", index, node)
		}
	}
}

// 一条报价的路由只改图片那一份：没有图的节点一个 token 都不许动。
func TestPriceSurfaceReplacesOnlyTheImageShare(t *testing.T) {
	t.Parallel()

	view := newSession(userEvent(t, "hello"), imageEvent(t, "i1", "看这张"))
	nodes, _ := foldAll(t, view.events)

	pricing := &countingPricing{visualTokens: 1000, text: "[image]"}
	priced, err := priceSurface(nodes, pricing)
	if err != nil {
		t.Fatalf("该定得出价：%v", err)
	}
	if pricing.asked != 1 {
		t.Fatalf("表面上只有一次图片出现，却问了 %d 次", pricing.asked)
	}
	if priced.nodes[0].Tokens != nodes[0].heuristicTokens {
		t.Fatalf("没有图的节点不该被重估：%+v", priced.nodes[0])
	}
	if priced.nodes[1].Tokens <= priced.nodes[1].HeuristicTokens+900 {
		t.Fatalf("带图的节点该按路由那 1000 个视觉 token 重估：%+v", priced.nodes[1])
	}
	// 对外那份节点两个价都要带着，固定估价不许被路由价盖掉。
	if priced.nodes[1].HeuristicTokens != nodes[1].heuristicTokens {
		t.Fatalf("固定估价被盖掉了：%+v", priced.nodes[1])
	}
	if want := priced.nodes[0].Tokens + priced.nodes[1].Tokens; priced.surfaceTokens != want {
		t.Fatalf("总价该等于逐节点之和：想要 %d，实际 %d", want, priced.surfaceTokens)
	}
}

// 同一张图在同一次请求里出现两遍就是两条价，按出现算不按附件算。
func TestPriceSurfaceCountsEveryOccurrence(t *testing.T) {
	t.Parallel()

	view := newSession(imageEvent(t, "i1", "一"), imageEvent(t, "i1", "二"))
	nodes, _ := foldAll(t, view.events)

	pricing := &countingPricing{visualTokens: 7}
	if _, err := priceSurface(nodes, pricing); err != nil {
		t.Fatalf("该定得出价：%v", err)
	}
	if pricing.asked != 2 {
		t.Fatalf("同一张图出现两遍该问两次，实际问了 %d 次", pricing.asked)
	}
}

// 报回来的价数和问的次数对不上就当场失败：将就着用会让每个节点都配上别人的价。
func TestPriceSurfaceFailsOnAMisalignedAnswer(t *testing.T) {
	t.Parallel()

	view := newSession(imageEvent(t, "i1", "一"), imageEvent(t, "i2", "二"))
	nodes, _ := foldAll(t, view.events)

	if _, err := priceSurface(nodes, &countingPricing{visualTokens: 7, short: true}); err == nil {
		t.Fatal("少报一条价该当场失败")
	}
}

// 没交 llm 运行时的计量器一律用固定估价，而不是报错。
func TestMeasureWithoutAnLLMRuntimeKeepsTheHeuristic(t *testing.T) {
	t.Parallel()

	meter := New(nil)
	header := simpleHeader("你是助手")
	view := newSession(headerEvent(t, header), imageEvent(t, "i1", "看这张"))

	got, err := meter.Measure(view, nil)
	if err != nil {
		t.Fatalf("该量得出来：%v", err)
	}
	if len(got.Nodes) != 1 {
		t.Fatalf("表面上该只有一个节点：%+v", got.Nodes)
	}
	if got.Nodes[0].Tokens != got.Nodes[0].HeuristicTokens {
		t.Fatalf("没有 llm 运行时时不该重估：%+v", got.Nodes[0])
	}
}
