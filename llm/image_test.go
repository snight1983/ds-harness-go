// 本文件验把持久的图片历史投影成一次具体请求装得下的样子：三段面向模型的占位文本、
// base64 记账、以及那个「拿掉哪几张」的确定性算法。
//
// 源: packages/llm/llm/src/content.ts:7-202

package llm

import (
	"encoding/base64"
	"testing"

	"ds-harness-go/attachment"
)

// intPointer 造一个指向给定上限的指针，用来表达 [RequestImageOffloadPolicy] 里
// 「给了这个上限」——而 nil 是「不限」。
func intPointer(value int) *int { return &value }

// refOfBytes 造一张只有字节数有意义的图，专门喂给那些只按大小记账的用例。
func refOfBytes(id attachment.ID, bytes int) attachment.ImageRef {
	return attachment.ImageRef{ID: id, MediaType: attachment.MediaTypePNG, Bytes: bytes}
}

// imagesInOneMessage 把若干张图装进一条用户消息，图的顺序就是历史顺序。
func imagesInOneMessage(refs ...attachment.ImageRef) []Message {
	content := make(Content, 0, len(refs))
	for _, ref := range refs {
		content = append(content, ImageBlock{Attachment: ref})
	}
	return []Message{{Role: RoleUser, Content: content}}
}

// countOffloadedImages 数一条历史里还剩几张图、被换掉了几张。
func countOffloadedImages(messages []Message) (images int, offloaded int) {
	var walk func(Content)
	walk = func(blocks Content) {
		for _, block := range blocks {
			switch typed := block.(type) {
			case ImageBlock:
				images++
			case TextBlock:
				if typed.Text == OffloadedImageText {
					offloaded++
				}
			case ToolResultBlock:
				walk(typed.Content)
			}
		}
	}
	for _, message := range messages {
		walk(message.Content)
	}
	return images, offloaded
}

// TestTextOnlyImageTextClampsInsteadOfPanicking 钉住摘要那两个下标被夹在长度以内。
//
// 源: packages/llm/llm/src/content.ts:11-19
//
// DSH 那边是 JS 的 slice，越界返回空串。Go 的切片越界是 panic——一个标识短了几个
// 字符就会炸掉整次请求装配。前两条钉住格式正常时和 DSH 逐字节一致，后三条钉住
// 格式不正常时给一段更短的摘要而不是崩。
func TestTextOnlyImageTextClampsInsteadOfPanicking(t *testing.T) {
	t.Parallel()

	for name, expectation := range map[string]struct {
		id     attachment.ID
		digest string
	}{
		"正常的标识":     {"sha256:0123456789abcdef", "01234567"},
		"摘要刚好八位":    {"sha256:01234567", "01234567"},
		"摘要不够八位":    {"sha256:ab", "ab"},
		"连前缀都不完整":   {"sha", ""},
		"标识是空的":     {"", ""},
		"前缀长度上一个字符": {"sha256:", ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			want := "[image omitted because this model accepts text only; attachment sha256:" +
				expectation.digest + "]"
			if got := TextOnlyImageText(attachment.ImageRef{ID: expectation.id}); got != want {
				t.Errorf("占位文本变了：\n想要 %s\n实际 %s", want, got)
			}
		})
	}
}

// TestTheModelFacingTextIsTheEnglishVerbatim 钉住两段占位文本一个字都没被翻译。
//
// 源: packages/llm/llm/src/content.ts:7-19
//
// 这两段话是**说给模型听的**：它要据此判断「这里本来有张图、旧的先被拿掉、
// 还需要就去重新读文件」。翻成中文等于悄悄改了一次提示词，而且改动发生在
// 一个看起来只是「本地化」的提交里，事后没人会去那儿找模型行为变化的原因。
func TestTheModelFacingTextIsTheEnglishVerbatim(t *testing.T) {
	t.Parallel()

	want := "[image omitted to keep the request within its image limit; older images are omitted " +
		"first. If this image is still needed, read its file again when a path is available; " +
		"otherwise ask the user to attach it again.]"
	if OffloadedImageText != want {
		t.Errorf("被拿掉的占位文本变了：\n想要 %s\n实际 %s", want, OffloadedImageText)
	}
}

// TestRequestImageHandleTextUsesTheVersionsOwnDimensions 钉住把手文本报的是**这个请求版本**的尺寸。
//
// 源: packages/llm/llm/src/content.ts:21-28
//
// 用例里主附件和请求版本的尺寸特意不一样：请求版本是缩放过的那一份，模型看到的
// 必须是它真正收到的那张图的像素数。取错成主附件的尺寸，模型会按一个它没见过的
// 分辨率去描述坐标。
func TestRequestImageHandleTextUsesTheVersionsOwnDimensions(t *testing.T) {
	t.Parallel()

	version := attachment.RequestImage{
		Attachment: attachment.ImageRef{ID: "sha256:abc", Width: 4096, Height: 4096},
		Width:      1024,
		Height:     768,
	}
	want := "Image sha256:abc; request image 1024x768px."
	if got := RequestImageHandleText(version); got != want {
		t.Errorf("把手文本变了：\n想要 %s\n实际 %s", want, got)
	}
}

// TestBase64LengthAgreesWithTheStandardLibrary 钉住本包那套整数算法和标准库算出来的长度一致。
//
// 源: packages/llm/llm/src/content.ts:43-46
//
// 这个长度是**记账用的**：它决定谁被拿掉。算多了会白扔图，算少了会让请求超限被
// 提供方拒掉。拿 [base64.Encoding.EncodedLen] 当参照，是因为它就是真正内联时的长度。
func TestBase64LengthAgreesWithTheStandardLibrary(t *testing.T) {
	t.Parallel()

	for _, bytes := range []int{0, 1, 2, 3, 4, 5, 6, 7, 100, 1023, 1024, 1 << 20} {
		if got, want := base64Length(bytes), base64.StdEncoding.EncodedLen(bytes); got != want {
			t.Errorf("%d 字节该编码成 %d 长，实际 %d", bytes, want, got)
		}
	}
	// 负数只可能来自一个坏掉的 ByteLength 回调。记成 0 而不是负数：
	// 负的长度会让总量算少，反过来让一次真的超限被判成没超。
	if got := base64Length(-1); got != 0 {
		t.Errorf("负字节数该记成 0，实际 %d", got)
	}
}

// TestCeilDivStaysInIntegers 钉住向上取整的整数除法，边界各一条。
//
// 源: packages/llm/llm/src/content.ts:43-46（DSH 那边是 Math.ceil 的浮点除法）
func TestCeilDivStaysInIntegers(t *testing.T) {
	t.Parallel()

	for name, expectation := range map[string]struct {
		dividend int
		divisor  int
		want     int
	}{
		"整除":    {6, 3, 2},
		"差一点":   {7, 3, 3},
		"刚过一点":  {4, 3, 2},
		"零":     {0, 3, 0},
		"负数记成零": {-5, 3, 0},
		"除数是一":  {5, 1, 5},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := ceilDiv(expectation.dividend, expectation.divisor); got != expectation.want {
				t.Errorf("该是 %d，实际 %d", expectation.want, got)
			}
		})
	}
}

// TestProjectImagesForTextModelReturnsTheSameHistoryWhenThereIsNoImage 钉住没图时一次分配都不做。
//
// 源: packages/llm/llm/src/content.ts:130-141
//
// 这是每一轮都会跑一遍的投影。一段几百条消息的纯文本历史被整个复制一遍，
// 复制出来的还和原件一模一样——比较的是切片头的地址，因为那正是「有没有复制」。
func TestProjectImagesForTextModelReturnsTheSameHistoryWhenThereIsNoImage(t *testing.T) {
	t.Parallel()

	messages := []Message{
		{Role: RoleUser, Content: Content{TextBlock{Text: "hi"}}},
		{Role: RoleAssistant, Content: Content{TextBlock{Text: "hello"}}},
	}
	got := ProjectImagesForTextModel(messages)
	if &got[0] != &messages[0] {
		t.Error("没图时该原样返回，不该复制一份")
	}
}

// TestProjectImagesForTextModelReachesImagesNestedInToolResults 钉住嵌在工具结果里的图也被换掉。
//
// 工具结果是唯一一个递归的块。一张图藏在里面而递归没接上，会让一个纯文本模型
// 收到一个它读不懂的图片块——提供方直接拒掉整次请求，而不是少看一张图。
func TestProjectImagesForTextModelReachesImagesNestedInToolResults(t *testing.T) {
	t.Parallel()

	messages := []Message{{
		Role: RoleUser,
		Content: Content{
			TextBlock{Text: "看这个"},
			ToolResultBlock{
				ToolCallID: "call-1",
				Content: Content{
					TextBlock{Text: "结果"},
					ImageBlock{Attachment: sampleRef()},
				},
			},
		},
	}}
	got := ProjectImagesForTextModel(messages)

	nested, ok := got[0].Content[1].(ToolResultBlock)
	if !ok {
		t.Fatalf("第二块该还是工具结果，实际 %#v", got[0].Content[1])
	}
	replaced, ok := nested.Content[1].(TextBlock)
	if !ok {
		t.Fatalf("嵌套的图该被换成文本，实际 %#v", nested.Content[1])
	}
	if replaced.Text != TextOnlyImageText(sampleRef()) {
		t.Errorf("换出来的文本不对：%s", replaced.Text)
	}

	// 原件不许被改动：它是**持久的**历史，投影只是这一次请求的临时形状。
	original, _ := messages[0].Content[1].(ToolResultBlock)
	if _, stillImage := original.Content[1].(ImageBlock); !stillImage {
		t.Error("原件里的图被改掉了")
	}
}

// TestProjectImagesForTextModelOnlyCopiesTheMessagesThatHaveImages 钉住那句
// 「没碰过的那条消息一次分配都不做」是**逐条**成立的，不只是整段历史都没图时成立。
//
// 一段长历史里往往只有一两条带图。逐条判断保住的是：其余几百条消息的内容切片
// 原封不动地进到投影里，提供方那边序列化出来的前缀也就一个字节都没变——
// 这正是提示词缓存复用的前提。顺便钉住图后面的块跟着搬过去，没被吃掉。
func TestProjectImagesForTextModelOnlyCopiesTheMessagesThatHaveImages(t *testing.T) {
	t.Parallel()

	messages := []Message{
		{Role: RoleUser, Content: Content{
			ImageBlock{Attachment: sampleRef()},
			TextBlock{Text: "图后面还有话"},
		}},
		{Role: RoleAssistant, Content: Content{TextBlock{Text: "这条一张图都没有"}}},
	}
	got := ProjectImagesForTextModel(messages)

	if len(got[0].Content) != 2 {
		t.Fatalf("带图那条该还是两块，得到 %d 块", len(got[0].Content))
	}
	if replaced, ok := got[0].Content[0].(TextBlock); !ok || replaced.Text != TextOnlyImageText(sampleRef()) {
		t.Errorf("第一块该被换成占位文本，实际 %#v", got[0].Content[0])
	}
	if trailing, ok := got[0].Content[1].(TextBlock); !ok || trailing.Text != "图后面还有话" {
		t.Errorf("图后面那块该原样搬过去，实际 %#v", got[0].Content[1])
	}
	if &got[1].Content[0] != &messages[1].Content[0] {
		t.Error("没图那条该原样带过去，不该复制一份内容")
	}
}

// TestOffloadOnlyCopiesTheMessagesItActuallyChanged 钉住换图那一侧同样是逐条判断：
// 没有被换掉任何一张图的消息交出的还是原来那个内容切片。理由与上一条逐字相同。
func TestOffloadOnlyCopiesTheMessagesItActuallyChanged(t *testing.T) {
	t.Parallel()

	messages := []Message{
		{Role: RoleUser, Content: Content{
			ImageBlock{Attachment: refOfBytes("old", 100)},
			ImageBlock{Attachment: refOfBytes("new", 100)},
		}},
		{Role: RoleAssistant, Content: Content{TextBlock{Text: "这条一张图都没有"}}},
	}
	got := OffloadRequestImagesWithPolicy(messages, RequestImageOffloadPolicy{
		Representation: RepresentationRaw,
		MaxBytes:       intPointer(100),
	})

	if images, offloaded := countOffloadedImages(got); images != 1 || offloaded != 1 {
		t.Fatalf("该还剩 1 张、换掉 1 张，实际剩 %d 换 %d", images, offloaded)
	}
	if &got[1].Content[0] != &messages[1].Content[0] {
		t.Error("没图那条该原样带过去，不该复制一份内容")
	}
}

// TestOffloadRequestImagesKeepsEverythingWhenThereIsNoLimit 钉住不限字节时一张都不拿掉。
//
// 源: packages/llm/llm/src/content.ts:143-161
func TestOffloadRequestImagesKeepsEverythingWhenThereIsNoLimit(t *testing.T) {
	t.Parallel()

	messages := imagesInOneMessage(refOfBytes("a", 1<<20), refOfBytes("b", 1<<20))
	got := OffloadRequestImages(messages, nil)
	if &got[0] != &messages[0] {
		t.Error("不限字节时该原样返回，不该复制一份")
	}
	if images, offloaded := countOffloadedImages(got); images != 2 || offloaded != 0 {
		t.Errorf("该还剩 2 张、换掉 0 张，实际剩 %d 换 %d", images, offloaded)
	}
}

// TestOffloadRequestImagesCountsTheBase64Payload 钉住默认那条路按**内联之后**的长度记账。
//
// 源: packages/llm/llm/src/content.ts:143-161
//
// 三个原始字节内联成四个字符。上限卡在 4：按原始字节算是 3+3=6 超 2，按 base64 算
// 是 4+4=8 超 4，两种口径拿掉的张数不同。记错口径就会在提供方那边超限。
func TestOffloadRequestImagesCountsTheBase64Payload(t *testing.T) {
	t.Parallel()

	messages := imagesInOneMessage(refOfBytes("old", 3), refOfBytes("new", 3))
	got := OffloadRequestImages(messages, intPointer(4))

	images, offloaded := countOffloadedImages(got)
	if images != 1 || offloaded != 1 {
		t.Fatalf("该还剩 1 张、换掉 1 张，实际剩 %d 换 %d", images, offloaded)
	}
	// 换掉的必须是**最旧**那张：留下的是最靠后的一张。
	kept, ok := got[0].Content[1].(ImageBlock)
	if !ok {
		t.Fatalf("第二块该还是图，实际 %#v", got[0].Content[1])
	}
	if kept.Attachment.ID != "new" {
		t.Errorf("留下的该是最新那张，实际 %q", kept.Attachment.ID)
	}
}

// TestTheOffloadedPrefixIsStableAcrossRuns 钉住同一段历史每次都投影成同一份请求。
//
// 源: packages/llm/llm/src/content.ts:163-202
//
// 这是提供方提示词缓存复用的**前提**：选谁被拿掉只由持久的消息顺序和附件元数据
// 决定，不看时间、不看随机数、不读被拿掉的字节。选择只要抖一下，每一轮的请求
// 前缀就都不一样，缓存整片作废。
func TestTheOffloadedPrefixIsStableAcrossRuns(t *testing.T) {
	t.Parallel()

	messages := imagesInOneMessage(
		refOfBytes("a", 3), refOfBytes("b", 3), refOfBytes("c", 3), refOfBytes("d", 3),
	)
	first := OffloadRequestImages(messages, intPointer(8))
	second := OffloadRequestImages(messages, intPointer(8))

	for index := range first[0].Content {
		left, right := first[0].Content[index], second[0].Content[index]
		if left != right {
			t.Errorf("第 %d 块两次投影不一样：%#v / %#v", index, left, right)
		}
	}
}

// TestTheDocumentedOffloadExampleHolds 钉住文档里那个例子。
//
// 源: packages/llm/llm/src/content.ts:163-202
//
// [OffloadRequestImagesWithPolicy] 的注释写着：129 张一兆的图、128 MiB 上限、
// 64 MiB 步长时，最旧的 65 张被拿掉，剩下 64 MiB。这个数字是**量化步长的全部意义**
// ——只超了 1 MiB 却整整拿掉 64 MiB 多一张，为的是让被拿掉的前缀在历史继续增长时
// 保持不变（直到总量超过 192 MiB）。注释里写着的数字只有配一条断言才算数。
func TestTheDocumentedOffloadExampleHolds(t *testing.T) {
	t.Parallel()

	const mebibyte = 1 << 20
	refs := make([]attachment.ImageRef, 129)
	for index := range refs {
		refs[index] = refOfBytes(attachment.ID(string(rune('a'+index%26))), mebibyte)
	}
	got := OffloadRequestImagesWithPolicy(imagesInOneMessage(refs...), RequestImageOffloadPolicy{
		Representation: RepresentationRaw,
		MaxBytes:       intPointer(128 * mebibyte),
		ByteQuantum:    64 * mebibyte,
	})

	images, offloaded := countOffloadedImages(got)
	if offloaded != 65 {
		t.Errorf("该拿掉 65 张，实际 %d", offloaded)
	}
	if images != 64 {
		t.Errorf("该还剩 64 张（正好 64 MiB），实际 %d", images)
	}
}

// TestTheByteQuantumAsymmetryIsDeliberate 钉住步长为 1 和大于 1 的停止判据不一样。
//
// 源: packages/llm/llm/src/content.ts:163-202
//
// 同一份输入、同一个移除目标（4 字节），步长 1 拿掉 1 张、步长 2 拿掉 2 张：
// 步长为 1 时移到「刚好够」就停，步长大于 1 时要移**过**目标才停。这个不对称是
// 照抄的，不是笔误——步长大于 1 意味着调用方要的是整块整块地移除，停在正好等于
// 目标的地方会留下一个不满一整块的尾巴。少了这条用例，后来的人会把它「顺手改齐」。
func TestTheByteQuantumAsymmetryIsDeliberate(t *testing.T) {
	t.Parallel()

	for name, expectation := range map[string]struct {
		quantum  int
		offloads int
	}{
		"步长为一移到刚好够":  {1, 1},
		"步长大于一要移过目标": {2, 2},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			messages := imagesInOneMessage(refOfBytes("a", 3), refOfBytes("b", 3))
			got := OffloadRequestImagesWithPolicy(messages, RequestImageOffloadPolicy{
				Representation: RepresentationBase64,
				MaxBytes:       intPointer(4),
				ByteQuantum:    expectation.quantum,
			})
			if _, offloaded := countOffloadedImages(got); offloaded != expectation.offloads {
				t.Errorf("该拿掉 %d 张，实际 %d", expectation.offloads, offloaded)
			}
		})
	}
}

// TestZeroMaxImagesMeansThisRouteTakesNoImageAtAll 钉住 0 张上限和「不限张数」是两件事。
//
// 源: packages/llm/llm/src/content.ts:48-62
//
// 这是 [RequestImageOffloadPolicy.MaxImages] 用指针的全部理由。把 0 当成「没给」，
// 一条明说自己一张图都收不了的路由会原样收到所有图。
func TestZeroMaxImagesMeansThisRouteTakesNoImageAtAll(t *testing.T) {
	t.Parallel()

	messages := imagesInOneMessage(refOfBytes("a", 10), refOfBytes("b", 10))

	none := OffloadRequestImagesWithPolicy(messages, RequestImageOffloadPolicy{
		Representation: RepresentationRaw,
		MaxImages:      intPointer(0),
	})
	if images, offloaded := countOffloadedImages(none); images != 0 || offloaded != 2 {
		t.Errorf("上限为零该把两张都换掉，实际剩 %d 换 %d", images, offloaded)
	}

	unlimited := OffloadRequestImagesWithPolicy(messages, RequestImageOffloadPolicy{
		Representation: RepresentationRaw,
	})
	if images, offloaded := countOffloadedImages(unlimited); images != 2 || offloaded != 0 {
		t.Errorf("不限张数该一张都不换，实际剩 %d 换 %d", images, offloaded)
	}
}

// TestTheCountQuantumRoundsUpTheNumberOfImagesRemoved 钉住张数也按步长量化。
//
// 五张图、上限 4 张、步长 3：只超 1 张，但要拿掉 3 张。理由和字节步长逐字相同。
func TestTheCountQuantumRoundsUpTheNumberOfImagesRemoved(t *testing.T) {
	t.Parallel()

	messages := imagesInOneMessage(
		refOfBytes("a", 1), refOfBytes("b", 1), refOfBytes("c", 1),
		refOfBytes("d", 1), refOfBytes("e", 1),
	)
	got := OffloadRequestImagesWithPolicy(messages, RequestImageOffloadPolicy{
		Representation: RepresentationRaw,
		MaxImages:      intPointer(4),
		CountQuantum:   3,
	})
	if _, offloaded := countOffloadedImages(got); offloaded != 3 {
		t.Errorf("该拿掉 3 张，实际 %d", offloaded)
	}
}

// TestByteLengthOverridesTheAttachmentsOwnSize 钉住回调给的长度盖过附件自己的字节数。
//
// 源: packages/llm/llm/src/content.ts:64-80
//
// 这个回调存在的理由是：一条路由发出去的不是原文件，而是它自己缩放/重编码过的
// 那一份。记账要按**真正发出去的那份**算。回调没被调用的话，一次超限会被判成没超。
func TestByteLengthOverridesTheAttachmentsOwnSize(t *testing.T) {
	t.Parallel()

	messages := imagesInOneMessage(refOfBytes("a", 1), refOfBytes("b", 1))
	got := OffloadRequestImagesWithPolicy(messages, RequestImageOffloadPolicy{
		Representation: RepresentationRaw,
		MaxBytes:       intPointer(150),
		ByteLength:     func(attachment.ImageRef) int { return 100 },
	})
	// 按附件自己的字节数算总共才 2 字节，不超；按回调算是 200，超 50。
	if _, offloaded := countOffloadedImages(got); offloaded != 1 {
		t.Errorf("该按回调的长度记账、拿掉 1 张，实际 %d", offloaded)
	}
}

// TestOffloadReachesImagesNestedInToolResults 钉住嵌在工具结果里的图既进记账也会被换掉。
//
// 只数不换、或者只换不数，都会让预算和实际发出去的内容对不上。
func TestOffloadReachesImagesNestedInToolResults(t *testing.T) {
	t.Parallel()

	messages := []Message{{
		Role: RoleUser,
		Content: Content{
			ToolResultBlock{
				ToolCallID: "call-1",
				Content:    Content{ImageBlock{Attachment: refOfBytes("nested", 100)}},
			},
			ImageBlock{Attachment: refOfBytes("top", 100)},
		},
	}}
	got := OffloadRequestImagesWithPolicy(messages, RequestImageOffloadPolicy{
		Representation: RepresentationRaw,
		MaxBytes:       intPointer(100),
	})

	images, offloaded := countOffloadedImages(got)
	if images != 1 || offloaded != 1 {
		t.Fatalf("该还剩 1 张、换掉 1 张，实际剩 %d 换 %d", images, offloaded)
	}
	// 最旧的那张在工具结果里面，所以被换掉的必须是嵌套的那张。
	nested, ok := got[0].Content[0].(ToolResultBlock)
	if !ok {
		t.Fatalf("第一块该还是工具结果，实际 %#v", got[0].Content[0])
	}
	if _, replaced := nested.Content[0].(TextBlock); !replaced {
		t.Errorf("嵌套的那张该被换掉，实际 %#v", nested.Content[0])
	}

	// 原件不许被改动。
	original, _ := messages[0].Content[0].(ToolResultBlock)
	if _, stillImage := original.Content[0].(ImageBlock); !stillImage {
		t.Error("原件里的图被改掉了")
	}
}
