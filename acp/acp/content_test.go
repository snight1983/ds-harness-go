// 本文件的作用：把线上内容那两道关——提示词进来怎么验怎么落、已提交的助手内容怎么
// 翻回线上去——以及回合结束理由那张映射表，逐条压一遍。

package acp

import (
	"context"
	"errors"
	"strings"
	"testing"

	wire "github.com/coder/acp-go-sdk"

	"github.com/snight1983/ds-harness-go/attachment"
	"github.com/snight1983/ds-harness-go/llm"
	sessionlog "github.com/snight1983/ds-harness-go/session"
)

// # 这些测试防的是什么错
//
//   - 一个非规范的 base64 串被解码器"宽容"地收下：`\r\n` 是 Go 的解码器**有意忽略**
//     的，而 DSH 那条正则拒它们，两侧收下的字节集合从此不同。
//   - 一句"我支持内联图"在说不清的时候被说了出去——客户端会照它发图，然后每一张都被拒。
//   - 资源链接的 uri 里那个 `&` 被转义成 `&`：两侧记下来的就不是同一个链接了。
//   - 图还没验完就先落盘，或者验完了却在一条早就换掉的路由上验的。
//   - 一批图里有一张写不进去，前面几张却已经排进了这条会话的消息。
//   - 一条只有空白的提示词被当成有效输入排进队。
//   - 一个转不动的助手块被静悄悄跳过，于是对面收到的是一条缺了中段的消息。

func TestDecodeImageRejectsUnknownMediaType(t *testing.T) {
	t.Parallel()
	_, err := decodeImage(&wire.ContentBlockImage{MimeType: "image/tiff", Data: base64Of([]byte{1})})
	assertInvalidContent(t, err, "mimeType")
}

func TestDecodeImageRejectsLineBreaksInsideBase64(t *testing.T) {
	t.Parallel()
	// Go 的解码器**有意忽略** \r 和 \n（为了读 PEM 那类分行编码），DSH 那条正则拒它们。
	// 少了那条显式检查，这一条就会被收下。
	encoded := base64Of([]byte{1, 2, 3, 4, 5, 6})
	broken := encoded[:4] + "\n" + encoded[4:]
	_, err := decodeImage(&wire.ContentBlockImage{MimeType: "image/png", Data: broken})
	assertInvalidContent(t, err, "canonical base64")
}

func TestDecodeImageRejectsNonCanonicalPadding(t *testing.T) {
	t.Parallel()
	_, err := decodeImage(&wire.ContentBlockImage{MimeType: "image/png", Data: "AQ="})
	assertInvalidContent(t, err, "canonical base64")
}

func TestDecodeImageKeepsTheDecodedBytes(t *testing.T) {
	t.Parallel()
	input, err := decodeImage(&wire.ContentBlockImage{MimeType: "image/jpeg", Data: base64Of([]byte{9, 8})})
	if err != nil {
		t.Fatalf("这张图该收下：%v", err)
	}
	if input.MediaType != attachment.MediaTypeJPEG || string(input.Data) != string([]byte{9, 8}) {
		t.Fatalf("解出来的图不对：%#v", input)
	}
}

func TestRouteOfPrefersTheLatestRequestHeader(t *testing.T) {
	t.Parallel()
	target := freeAgent(t, "built", "at-create")
	target.appendAll(t, logEvent(t, sessionlog.EventRequestHeader, sessionlog.RequestHeaderData{
		Header: sessionlog.EpochHeader{Config: llm.CallConfig{Provider: "live", Model: "now"}},
		Reason: sessionlog.HeaderInitial,
	}))
	provider, model, err := routeOf(target)
	if err != nil {
		t.Fatalf("读路由不该失败：%v", err)
	}
	if provider != "live" || model != "now" {
		t.Fatalf("该用日志里最后那份头，实际 %s/%s", provider, model)
	}
}

func TestRouteOfFallsBackToCreationOptions(t *testing.T) {
	t.Parallel()
	provider, model, err := routeOf(freeAgent(t, "built", "at-create"))
	if err != nil {
		t.Fatalf("读路由不该失败：%v", err)
	}
	if provider != "built" || model != "at-create" {
		t.Fatalf("没有头时该退回建出来那份选项，实际 %s/%s", provider, model)
	}
}

func TestRouteOfReportsAnUnreadableHeaderAsInternal(t *testing.T) {
	t.Parallel()
	// 一条形状不对的请求头折不出来。静悄悄退回建出来时那份选项，会把一张图送到一条
	// 早就换掉的路由上去，所以这里必须是一次失败而不是一次回落。
	target := freeAgent(t, "built", "at-create")
	// 会话只验 JSON 是否成立，一个合法的数组照收；折头那一步才发现形状不对。
	target.appendAll(t, sessionlog.Event{
		Type: sessionlog.EventRequestHeader, Data: []byte("[1,2,3]")})

	var failure *ContentError
	if _, _, err := routeOf(target); !errors.As(err, &failure) || failure.Kind != ContentInternal {
		t.Fatalf("折不出来的头该报 internal，实际 %v", err)
	}
	// 同一条失败必须原样穿过路由断言，不能在那里被改写成"路由解不出来"。
	err := assertImageRoute(context.Background(), imageModels(), target)
	if !errors.As(err, &failure) || failure.Kind != ContentInternal {
		t.Fatalf("路由断言该把这条失败原样交出去，实际 %v", err)
	}
}

func TestAssertImageRouteRejectsAnUnresolvableRoute(t *testing.T) {
	t.Parallel()
	err := assertImageRoute(context.Background(), imageModels(), freeAgent(t, "", ""))
	assertInvalidContent(t, err, "could not be resolved")
}

func TestAssertImageRouteRejectsAMissingModelService(t *testing.T) {
	t.Parallel()
	err := assertImageRoute(context.Background(), nil, freeAgent(t, "p", "m"))
	assertInvalidContent(t, err, "could not be resolved")
}

func TestAssertImageRouteReportsAFailedLookupAsInternal(t *testing.T) {
	t.Parallel()
	models := fakeModels{fail: errors.New("名册炸了")}
	err := assertImageRoute(context.Background(), models, freeAgent(t, "p", "m"))
	var failure *ContentError
	if !errors.As(err, &failure) || failure.Kind != ContentInternal {
		t.Fatalf("解算失败该报 internal，实际 %v", err)
	}
	if !errors.Is(err, models.fail) {
		t.Fatalf("底层原因该留在链上：%v", err)
	}
}

func TestAssertImageRouteRejectsAModelThatDoesNotDeclareImages(t *testing.T) {
	t.Parallel()
	// 一份显式给出、却没列图的模态清单是"不收图"；nil 是"不知道"。两种都不放行。
	for name, modalities := range map[string][]llm.ModelModality{
		"未声明":   nil,
		"只声明文本": {llm.ModalityText},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := assertImageRoute(
				context.Background(), fakeModels{modalities: modalities}, freeAgent(t, "p", "m"))
			assertInvalidContent(t, err, "does not declare image input")
		})
	}
}

func TestSupportsImagePromptsSaysNoWheneverItCannotBeSure(t *testing.T) {
	t.Parallel()
	store := &fakeStore{}
	cases := map[string]struct {
		store    attachment.Store
		models   ModelResolver
		provider string
		model    string
	}{
		"没挂附件存储":    {nil, imageModels(), "p", "m"},
		"没挂模型服务":    {store, nil, "p", "m"},
		"路由缺提供方":    {store, imageModels(), "", "m"},
		"路由缺模型":     {store, imageModels(), "p", ""},
		"部署不收这几种栅格": {&fakeStore{mediaTypes: []attachment.MediaType{}}, imageModels(), "p", "m"},
		"模型没声明收图":   {store, fakeModels{}, "p", "m"},
		"解算失败":      {store, fakeModels{fail: errors.New("炸")}, "p", "m"},
	}
	for name, each := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if SupportsImagePrompts(context.Background(), each.store, each.models, each.provider, each.model) {
				t.Fatal("说不清的时候该一律说没有")
			}
		})
	}
}

func TestSupportsImagePromptsSaysYesWhenEverythingLinesUp(t *testing.T) {
	t.Parallel()
	if !SupportsImagePrompts(context.Background(), &fakeStore{}, imageModels(), "p", "m") {
		t.Fatal("样样都齐时该如实声明支持")
	}
}

func TestResourceLinkTextKeepsQueryStringsIntact(t *testing.T) {
	t.Parallel()
	// encoding/json 默认把 & 转义成 &。资源链接的 uri 里带查询串是常事，
	// 一个 & 换了写法，两侧记下来的就不是同一个链接。
	rendered := resourceLinkText(&wire.ContentBlockResourceLink{
		Name: "报表",
		Uri:  "https://example.test/x?a=1&b=2",
	})
	if !strings.Contains(rendered, "a=1&b=2") {
		t.Fatalf("查询串被转义了：%s", rendered)
	}
	if !strings.Contains(rendered, `name="报表"`) {
		t.Fatalf("非 ASCII 名字不该被转义成 \\uXXXX：%s", rendered)
	}
}

func TestAdmitPromptJoinsTextAndResourceLinks(t *testing.T) {
	t.Parallel()
	content, err := AdmitPrompt(
		context.Background(), nil, nil, freeAgent(t, "p", "m"),
		[]wire.ContentBlock{
			wire.TextBlock("前"),
			{ResourceLink: &wire.ContentBlockResourceLink{Name: "n", Uri: "u"}},
			wire.TextBlock("后"),
		},
		false,
	)
	if err != nil {
		t.Fatalf("这条提示词该收下：%v", err)
	}
	if len(content) != 1 {
		t.Fatalf("相邻的文本该并成一块，实际 %d 块", len(content))
	}
	text, ok := content[0].(llm.TextBlock)
	if !ok || !strings.HasPrefix(text.Text, "前\n[resource_link") || !strings.HasSuffix(text.Text, "后") {
		t.Fatalf("拼出来的文本不对：%#v", content[0])
	}
}

func TestAdmitPromptRejectsContentOutsideTheContract(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		block wire.ContentBlock
		hint  string
	}{
		"音频":    {wire.ContentBlock{Audio: &wire.ContentBlockAudio{}}, "audio"},
		"内嵌资源":  {wire.ContentBlock{Resource: &wire.ContentBlockResource{}}, "embedded resource"},
		"认不出的块": {wire.ContentBlock{}, "unsupported ACP prompt content"},
	}
	for name, each := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := AdmitPrompt(
				context.Background(), nil, nil, freeAgent(t, "p", "m"),
				[]wire.ContentBlock{each.block}, true)
			assertInvalidContent(t, err, each.hint)
		})
	}
}

func TestAdmitPromptRejectsImagesThatWereNeverAdvertised(t *testing.T) {
	t.Parallel()
	_, err := AdmitPrompt(
		context.Background(), &fakeStore{}, imageModels(), freeAgent(t, "p", "m"),
		[]wire.ContentBlock{wire.ImageBlock(base64Of([]byte{1}), "image/png")}, false)
	assertInvalidContent(t, err, "not advertised")
}

func TestAdmitPromptRejectsImagesWithNoAttachmentStore(t *testing.T) {
	t.Parallel()
	_, err := AdmitPrompt(
		context.Background(), nil, imageModels(), freeAgent(t, "p", "m"),
		[]wire.ContentBlock{wire.ImageBlock(base64Of([]byte{1}), "image/png")}, true)
	assertInvalidContent(t, err, "no attachment store")
}

func TestAdmitPromptRejectsImagesOnARouteThatCannotTakeThem(t *testing.T) {
	t.Parallel()
	// 这个部署收图，但**当下这条路由**上的模型不收。声明是按建桥时那份路由算的，
	// 会话中途换过模型之后它就可能不再成立，所以每一批图都要按当下这条路由重验一遍。
	_, err := AdmitPrompt(
		context.Background(), &fakeStore{}, fakeModels{}, freeAgent(t, "p", "m"),
		[]wire.ContentBlock{wire.ImageBlock(base64Of([]byte{1}), "image/png")}, true)
	assertInvalidContent(t, err, "does not declare image input")
}

func TestAdmitPromptStopsWhenTheRequestIsCancelledMidBatch(t *testing.T) {
	t.Parallel()
	// 一批图写到一半客户端撤了：写进去那几张不再有主，这条提示词一个字都不该排进队。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &fakeStore{saveHook: cancel}
	_, err := AdmitPrompt(
		ctx, store, imageModels(), freeAgent(t, "p", "m"),
		[]wire.ContentBlock{wire.ImageBlock(base64Of([]byte{1}), "image/png")}, true)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消该原样交回去，实际 %v", err)
	}
}

func TestAdmitPromptAcceptsAPromptThatIsOnlyAnImage(t *testing.T) {
	t.Parallel()
	// 「空提示词」这条检查看的是有没有实质内容，不是有没有文字。一张图就是实质内容。
	content, err := AdmitPrompt(
		context.Background(), &fakeStore{}, imageModels(), freeAgent(t, "p", "m"),
		[]wire.ContentBlock{wire.ImageBlock(base64Of([]byte{1}), "image/png")}, true)
	if err != nil {
		t.Fatalf("只有一张图的提示词该收下：%v", err)
	}
	if len(content) != 1 {
		t.Fatalf("该只有一块，实际 %#v", content)
	}
	if _, ok := content[0].(llm.ImageBlock); !ok {
		t.Fatalf("那一块该是图：%#v", content[0])
	}
}

func TestAdmitPromptValidatesEveryBlockBeforeWritingAnyImage(t *testing.T) {
	t.Parallel()
	// 第二块是一张坏图。要是准入按块边走边写，第一张就已经进存储了。
	store := &fakeStore{}
	_, err := AdmitPrompt(
		context.Background(), store, imageModels(), freeAgent(t, "p", "m"),
		[]wire.ContentBlock{
			wire.ImageBlock(base64Of([]byte{1}), "image/png"),
			wire.ImageBlock("!!not base64!!", "image/png"),
		}, true)
	assertInvalidContent(t, err, "canonical base64")
	if len(store.saved) != 0 {
		t.Fatalf("一张都不该写进去，实际写了 %d 张", len(store.saved))
	}
}

func TestAdmitPromptStopsWhenTheRequestIsAlreadyCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := &fakeStore{}
	_, err := AdmitPrompt(
		ctx, store, imageModels(), freeAgent(t, "p", "m"),
		[]wire.ContentBlock{wire.ImageBlock(base64Of([]byte{1}), "image/png")}, true)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消该原样交回去，实际 %v", err)
	}
	if len(store.saved) != 0 {
		t.Fatalf("取消之后不该再写，实际写了 %d 张", len(store.saved))
	}
}

func TestAdmitPromptReportsAFailedImageWriteAsInternal(t *testing.T) {
	t.Parallel()
	store := &fakeStore{saveFail: errors.New("盘满了")}
	_, err := AdmitPrompt(
		context.Background(), store, imageModels(), freeAgent(t, "p", "m"),
		[]wire.ContentBlock{wire.ImageBlock(base64Of([]byte{1}), "image/png")}, true)
	var failure *ContentError
	if !errors.As(err, &failure) || failure.Kind != ContentInternal {
		t.Fatalf("写不进去该报 internal，实际 %v", err)
	}
	if strings.Contains(failure.Message, "盘满了") {
		t.Fatalf("底层原因不该上线：%s", failure.Message)
	}
}

func TestAdmitPromptPassesAdmissionErrorsThroughVerbatim(t *testing.T) {
	t.Parallel()
	// 准入错误是部署方写给操作者看的话，原样转给对面。
	store := &fakeStore{mediaTypes: []attachment.MediaType{attachment.MediaTypeGIF}}
	_, err := AdmitPrompt(
		context.Background(), store, imageModels(), freeAgent(t, "p", "m"),
		[]wire.ContentBlock{wire.ImageBlock(base64Of([]byte{1}), "image/png")}, true)
	var failure *ContentError
	if !errors.As(err, &failure) || failure.Kind != ContentInvalid {
		t.Fatalf("这是部署方定的拒绝，该报 invalid，实际 %v", err)
	}
	if !attachment.IsImageAdmissionError(errors.Unwrap(failure)) {
		t.Fatalf("底层该是一次准入拒绝：%v", failure.Err)
	}
}

func TestAdmitPromptKeepsBlockOrder(t *testing.T) {
	t.Parallel()
	content, err := AdmitPrompt(
		context.Background(), &fakeStore{}, imageModels(), freeAgent(t, "p", "m"),
		[]wire.ContentBlock{
			wire.TextBlock("前"),
			wire.ImageBlock(base64Of([]byte{1}), "image/png"),
			wire.TextBlock("后"),
		}, true)
	if err != nil {
		t.Fatalf("这条提示词该收下：%v", err)
	}
	if len(content) != 3 {
		t.Fatalf("该是文本、图、文本三块，实际 %#v", content)
	}
	if _, ok := content[1].(llm.ImageBlock); !ok {
		t.Fatalf("中间那块该是图：%#v", content[1])
	}
}

func TestAdmitPromptRejectsAnEmptyPrompt(t *testing.T) {
	t.Parallel()
	for name, blocks := range map[string][]wire.ContentBlock{
		"一个块都没有": {},
		"只有空白":   {wire.TextBlock("   \n\t")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := AdmitPrompt(
				context.Background(), nil, nil, freeAgent(t, "p", "m"), blocks, false)
			assertInvalidContent(t, err, "empty prompt")
		})
	}
}

func TestAssistantBlockToACPKeepsOffTheWireWhatIsNotAutomation(t *testing.T) {
	t.Parallel()
	for name, block := range map[string]llm.ContentBlock{
		"空文本": llm.TextBlock{},
		"推理":  llm.ReasoningBlock{Text: "想了想"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, onWire, err := AssistantBlockToACP(context.Background(), nil, block)
			if err != nil || onWire {
				t.Fatalf("这个块不该上线：onWire=%v err=%v", onWire, err)
			}
		})
	}
}

func TestAssistantBlockToACPSendsText(t *testing.T) {
	t.Parallel()
	converted, onWire, err := AssistantBlockToACP(context.Background(), nil, llm.TextBlock{Text: "喂"})
	if err != nil || !onWire {
		t.Fatalf("文本该上线：onWire=%v err=%v", onWire, err)
	}
	if converted.Text == nil || converted.Text.Text != "喂" {
		t.Fatalf("翻出来的不是那句话：%#v", converted)
	}
}

func TestAssistantBlockToACPRefusesAnImageWithNoStore(t *testing.T) {
	t.Parallel()
	_, _, err := AssistantBlockToACP(
		context.Background(), nil, llm.ImageBlock{Attachment: attachment.ImageRef{ID: "a"}})
	var failure *ContentError
	if !errors.As(err, &failure) || failure.Kind != ContentInternal {
		t.Fatalf("没挂存储该报 internal，实际 %v", err)
	}
}

func TestAssistantBlockToACPRefusesAnUnreadableImage(t *testing.T) {
	t.Parallel()
	// 读回来这一步本身就在验字节和引用对不对得上：读不回来意味着存储被改过或者坏了。
	store := &fakeStore{readFail: errors.New("对不上")}
	_, _, err := AssistantBlockToACP(
		context.Background(), store, llm.ImageBlock{Attachment: attachment.ImageRef{ID: "a"}})
	var failure *ContentError
	if !errors.As(err, &failure) || failure.Kind != ContentInternal {
		t.Fatalf("读不回来该报 internal，实际 %v", err)
	}
}

func TestAssistantBlockToACPInlinesAVerifiedImage(t *testing.T) {
	t.Parallel()
	converted, onWire, err := AssistantBlockToACP(
		context.Background(), &fakeStore{},
		llm.ImageBlock{Attachment: attachment.ImageRef{ID: "a", MediaType: attachment.MediaTypePNG}})
	if err != nil || !onWire {
		t.Fatalf("图该上线：onWire=%v err=%v", onWire, err)
	}
	if converted.Image == nil || converted.Image.Data != base64Of([]byte{7, 7}) {
		t.Fatalf("送出去的不是存下来那几个字节：%#v", converted.Image)
	}
}

func TestTurnEndToStopReasonNeverInventsACancellation(t *testing.T) {
	t.Parallel()
	// `cancelled` 在 ACP 上留给**显式的**客户端取消。一个说不出名字的结束理由报成取消，
	// 会让客户端认定"这一轮是我停的"。
	cases := map[string]struct {
		reason sessionlog.TurnEndReason
		want   wire.StopReason
	}{
		"没有理由":   {nil, wire.StopReasonEndTurn},
		"撞上输出上限": {sessionlog.MaxTokensTurnEnd{}, wire.StopReasonMaxTokens},
		"被打断":    {sessionlog.InterruptedTurnEnd{}, wire.StopReasonCancelled},
		"正常跑完":   {sessionlog.CompletedTurnEnd{}, wire.StopReasonEndTurn},
		"认不出的标签": {sessionlog.UnknownTurnEnd{Kind: "什么鬼"}, wire.StopReasonEndTurn},
	}
	for name, each := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := TurnEndToStopReason(each.reason); got != each.want {
				t.Fatalf("该是 %s，实际 %s", each.want, got)
			}
		})
	}
}

// assertInvalidContent 断言这是一次"客户端给错了东西"的内容失败，且那句说明里带着 hint。
func assertInvalidContent(t *testing.T, err error, hint string) {
	t.Helper()
	var failure *ContentError
	if !errors.As(err, &failure) {
		t.Fatalf("该是一次内容失败，实际 %v", err)
	}
	if failure.Kind != ContentInvalid {
		t.Fatalf("该报 invalid，实际 %s（%s）", failure.Kind, failure.Message)
	}
	if !strings.Contains(failure.Message, hint) {
		t.Fatalf("那句说明里该提到 %q，实际 %q", hint, failure.Message)
	}
}
