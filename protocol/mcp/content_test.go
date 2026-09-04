// 本文件的作用：把本包里那些不需要一条真连接就判得了的行为全部钉住——公开名怎么算、
// 一列内容块怎么投影成模型看见的东西、图这一路的每一条拒绝理由、以及配置和退避的边界。
//
// 逐条对着 DSH 的 tests/tools.spec.ts 和 tests/connection.spec.ts 走，只是那边靠
// cordis 把整套服务装起来，这里直接调函数：这一层本来就是纯的。
//
// # 这些测试防的是什么错
//
//   - **两个不同的 MCP 身份塌成同一个公开名**。那样模型调 A 会打到 B 身上，而且
//     两台服务器谁先注册谁赢，症状随启动顺序变。
//   - **图这一路只降级坏的那一张**。模型会以为剩下的图就是它看见的全部，
//     于是照着半份证据下结论。
//   - **一次没有内容的结果投影成空**。模型看见空内容和看见「哪个工具没给东西」
//     是两回事，后者它才知道要不要换个工具。
//   - **退避算到溢出**。移位回绕之后是 0，那会变成不等就重连，把对面打垮。
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/snight1983/ds-harness-go/attachment"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/tools"
)

// ---------------------------------------------------------------- 公开名

func TestPublicToolNameCleanCase(t *testing.T) {
	got := PublicToolName("files", "read_file")
	if got != "mcp__files__read_file" {
		t.Fatalf("公开名不对：%q", got)
	}
}

func TestPublicToolNameRewritesInvalidCharacters(t *testing.T) {
	got := PublicToolName("files", "read.file")
	// 换过字符就一定补哈希，所以它不再等于那个「看起来对」的名字。
	if got == "mcp__files__read_file" {
		t.Fatalf("换过字符的名字没有补身份哈希：%q", got)
	}
	if !strings.HasPrefix(got, "mcp__files__read_file_") {
		t.Fatalf("前缀被改坏了：%q", got)
	}
	if len(got) != len("mcp__files__read_file")+1+hashLength {
		t.Fatalf("补哈希之后长度不对：%q（%d）", got, len(got))
	}
}

func TestPublicToolNameTruncatesLongNames(t *testing.T) {
	got := PublicToolName("files", strings.Repeat("a", 200))
	if len(got) != maxPublicNameLength {
		t.Fatalf("超长的名字没有截到上限：%d", len(got))
	}
	if invalidNameChars.MatchString(got) {
		t.Fatalf("截出来的名字里有非法字符：%q", got)
	}
}

func TestPublicToolNameIsInjectiveAcrossIdentities(t *testing.T) {
	// 这一对如果拼成一段字节再哈希，`a_b|c` 和 `a|b_c` 会撞上；分隔符用 NUL 就撞不上。
	// 两边都走改写那条路（名字里带点），所以比的是补出来的哈希。
	left := PublicToolName("a_b", "c.d")
	right := PublicToolName("a", "b_c.d")
	if left == right {
		t.Fatalf("两个不同的 MCP 身份塌成了同一个公开名：%q", left)
	}
}

func TestPublicToolNameIsDeterministic(t *testing.T) {
	first := PublicToolName("files", "read.file")
	second := PublicToolName("files", "read.file")
	if first != second {
		t.Fatalf("同一对身份算出了两个名字：%q / %q", first, second)
	}
}

// ---------------------------------------------------------------- 内容投影

// textOf 把一份投影摊成一列字符串，方便逐块比。
func textOf(t *testing.T, content llm.Content) []string {
	t.Helper()
	parts := make([]string, 0, len(content))
	for _, block := range content {
		text, ok := block.(llm.TextBlock)
		if !ok {
			t.Fatalf("这一块不是文本：%#v", block)
		}
		parts = append(parts, text.Text)
	}
	return parts
}

func TestProjectContentMergesAdjacentText(t *testing.T) {
	blocks := []contentBlock{
		{Type: blockText, Text: "第一行"},
		{Type: blockText, Text: "第二行"},
		// 空文本什么都不贡献：Go 这边分不出「空串」和「没有 text」，一律按没有走。
		{Type: blockText},
		{Type: blockText, Text: "第三行"},
	}
	got := textOf(t, projectContent(blocks, "demo", nil))
	if len(got) != 1 || got[0] != "第一行\n第二行\n第三行" {
		t.Fatalf("连着的文本没有合成一块：%#v", got)
	}
}

func TestProjectContentSplitsTextAroundImages(t *testing.T) {
	blocks := []contentBlock{
		{Type: blockText, Text: "前"},
		{Type: blockImage, MediaType: "image/png"},
		{Type: blockText, Text: "后"},
	}
	content := projectContent(blocks, "demo", func(_ contentBlock, index int) llm.ContentBlock {
		return llm.TextBlock{Text: "图" + string(rune('0'+index))}
	})
	got := textOf(t, content)
	want := []string{"前", "图1", "后"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("图没有在原来的位置把文本劈开：%#v", got)
	}
}

func TestProjectContentRendersEveryBlockKind(t *testing.T) {
	blocks := []contentBlock{
		{Type: blockResourceLink, Name: "配置", URI: "file:///a"},
		{Type: blockResourceLink, Name: "缺了 URI"},
		{Type: blockAudio, MediaType: "audio/wav"},
		{Type: blockAudio},
		{Type: blockResource},
		{Type: unsupportedBlock},
		{Type: "tool_use"},
	}
	got := textOf(t, projectContent(blocks, "demo", nil))
	want := []string{strings.Join([]string{
		"Resource link: 配置 (file:///a)",
		"[resource link unavailable: the MCP block is missing its name or URI]",
		"[audio result unsupported: audio/wav; raw audio data remains available to programmatic callers]",
		"[audio result unsupported: unknown media type; raw audio data remains available to programmatic callers]",
		"[embedded resource unsupported; raw resource data remains available to programmatic callers]",
		"[unsupported MCP content block: expected an object]",
		"[unsupported MCP content type: tool_use]",
	}, "\n")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("每一种块的诊断和 DSH 对不上：\n%s", strings.Join(got, "\n"))
	}
}

func TestProjectContentDefaultImageProjectorDegradesToText(t *testing.T) {
	blocks := []contentBlock{{Type: blockImage, MediaType: "image/png"}}
	got := textOf(t, projectContent(blocks, "demo", nil))
	want := []string{
		"[image unavailable: image/png; this result was not admitted to durable model context;" +
			" raw image data remains available to programmatic callers]",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("默认图投影不对：%#v", got)
	}
}

func TestProjectContentEmptyResultNamesTheTool(t *testing.T) {
	got := textOf(t, projectContent(nil, "demo", nil))
	if len(got) != 1 || got[0] != "(demo returned no model-visible content)" {
		t.Fatalf("空结果没有说出是哪个工具：%#v", got)
	}
}

func TestExtractTextJoinsEverything(t *testing.T) {
	blocks := []contentBlock{
		{Type: blockText, Text: "一"},
		{Type: blockImage, MediaType: "image/gif"},
		{Type: blockText, Text: "二"},
	}
	got := extractText(blocks, "demo")
	want := "一\n[image unavailable: image/gif; this result was not admitted to durable model context;" +
		" raw image data remains available to programmatic callers]\n二"
	if got != want {
		t.Fatalf("抽出来的文本不对：\n%s", got)
	}
}

func TestContainsImage(t *testing.T) {
	if containsImage([]contentBlock{{Type: blockText}}) {
		t.Fatal("没有图却说有")
	}
	if !containsImage([]contentBlock{{Type: blockText}, {Type: blockImage}}) {
		t.Fatal("有图却说没有")
	}
}

// ---------------------------------------------------------------- 规范化

func TestNormalizeContentReadsEveryTypedKind(t *testing.T) {
	blocks, raw, err := normalizeContent([]sdk.Content{
		&sdk.TextContent{Text: "文本"},
		&sdk.ImageContent{MIMEType: "image/png", Data: []byte{1, 2}},
		&sdk.AudioContent{MIMEType: "audio/wav", Data: []byte{3}},
		&sdk.ResourceLink{Name: "n", URI: "file:///u"},
		&sdk.EmbeddedResource{Resource: &sdk.ResourceContents{URI: "file:///r"}},
		&sdk.ToolUseContent{ID: "1", Name: "t"},
	})
	if err != nil {
		t.Fatalf("规范化失败：%v", err)
	}
	if len(blocks) != 6 || len(raw) != 6 {
		t.Fatalf("两份出来的长度对不上：%d / %d", len(blocks), len(raw))
	}
	want := []string{blockText, blockImage, blockAudio, blockResourceLink, blockResource, "tool_use"}
	for index, tag := range want {
		if blocks[index].Type != tag {
			t.Fatalf("第 %d 块的标签是 %q，应当是 %q", index, blocks[index].Type, tag)
		}
	}
	if blocks[0].Text != "文本" || blocks[1].MediaType != "image/png" ||
		string(blocks[1].Data) != "\x01\x02" || blocks[3].URI != "file:///u" {
		t.Fatalf("字段读错了：%#v", blocks)
	}
	// 原样序列化那一份要还能被读回同一个标签，程序化调用方靠的就是它。
	if wireType(raw[0]) != blockText {
		t.Fatalf("原始那一份的标签读不回来：%s", raw[0])
	}
}

func TestNormalizeContentEmptyIsNotNil(t *testing.T) {
	blocks, raw, err := normalizeContent(nil)
	if err != nil {
		t.Fatalf("规范化失败：%v", err)
	}
	if blocks == nil || raw == nil {
		t.Fatal("空批次出来的是 nil，序列化出去会变成 null")
	}
	encoded, err := json.Marshal(Result{Content: raw})
	if err != nil {
		t.Fatalf("排不出去：%v", err)
	}
	if string(encoded) != `{"content":[]}` {
		t.Fatalf("空内容排出来不是空列表：%s", encoded)
	}
}

func TestWireTypeFallsBackToUnknown(t *testing.T) {
	if got := wireType(json.RawMessage(`{`)); got != "unknown" {
		t.Fatalf("解不开的字节应当是 unknown：%q", got)
	}
	if got := wireType(json.RawMessage(`{"text":"x"}`)); got != "unknown" {
		t.Fatalf("没有 type 的对象应当是 unknown：%q", got)
	}
}

func TestDecodeRawBlocksMarksNonObjects(t *testing.T) {
	blocks := decodeRawBlocks([]json.RawMessage{
		json.RawMessage(`{"type":"text","text":"甲"}`),
		json.RawMessage(`"我不是对象"`),
		json.RawMessage(`{"type":"image","mimeType":"image/png"}`),
	})
	if len(blocks) != 3 {
		t.Fatalf("块数不对：%d", len(blocks))
	}
	if blocks[0].Text != "甲" || blocks[1].Type != unsupportedBlock || blocks[2].MediaType != "image/png" {
		t.Fatalf("读出来的三块不对：%#v", blocks)
	}
}

// ---------------------------------------------------------------- 图这一路

// fakeStore 是一道能按剧本拒绝的假附件仓库。
type fakeStore struct {
	// failValidate 非 nil 时，逐张校验那一步以它失败。
	failValidate error
	// failSave 非 nil 时，提交那一步以它失败。
	failSave error
	// saved 记下真的写进去的每一张图。
	saved []attachment.ImageInput
}

func (s *fakeStore) ImageLimits() attachment.ImageLimits {
	return attachment.ImageLimits{
		MaxImageBytes:        1 << 20,
		MaxImagesPerMessage:  8,
		MaxMessageImageBytes: 1 << 20,
		MaxImagePixels:       1 << 20,
		MaxImageDimension:    4096,
		MediaTypes: []attachment.MediaType{
			attachment.MediaTypePNG, attachment.MediaTypeJPEG,
			attachment.MediaTypeWebP, attachment.MediaTypeGIF,
		},
	}
}

func (s *fakeStore) ValidateImage(context.Context, attachment.ImageInput) error {
	return s.failValidate
}

func (s *fakeStore) SaveImage(_ context.Context, input attachment.ImageInput) (attachment.ImageRef, error) {
	if s.failSave != nil {
		return attachment.ImageRef{}, s.failSave
	}
	s.saved = append(s.saved, input)
	return attachment.ImageRef{
		ID:        attachment.ID(string(rune('a' + len(s.saved) - 1))),
		MediaType: input.MediaType,
		Bytes:     len(input.Data),
		Width:     1,
		Height:    1,
	}, nil
}

func (s *fakeStore) ReadImage(context.Context, attachment.ImageRef) (attachment.StoredImage, error) {
	return attachment.StoredImage{}, errors.New("测试里不读")
}

// admitting 造一个总是交出同一个仓库的准入接缝。
func admitting(store attachment.Store) ImageAdmission {
	return func(context.Context, tools.Execution) (attachment.Store, error) { return store, nil }
}

// twoImages 是两张能过校验的图，外加一句文本。
func twoImages() []contentBlock {
	return []contentBlock{
		{Type: blockText, Text: "说明"},
		{Type: blockImage, MediaType: "image/png", Data: []byte{1}},
		{Type: blockImage, MediaType: "image/jpeg", Data: []byte{2}},
	}
}

func TestPrepareImageProjectionAdmitsImages(t *testing.T) {
	store := &fakeStore{}
	content := prepareImageProjection(
		context.Background(), admitting(store), tools.Execution{}, twoImages(), "demo")
	if len(content) != 3 {
		t.Fatalf("投影出来的块数不对：%#v", content)
	}
	if text, ok := content[0].(llm.TextBlock); !ok || text.Text != "说明" {
		t.Fatalf("第一块不是那句说明：%#v", content[0])
	}
	first, ok := content[1].(llm.ImageBlock)
	if !ok {
		t.Fatalf("第二块不是图：%#v", content[1])
	}
	second, ok := content[2].(llm.ImageBlock)
	if !ok {
		t.Fatalf("第三块不是图：%#v", content[2])
	}
	// 顺序要紧：两张图必须按它们在结果里出现的顺序对上各自的引用。
	if first.Attachment.MediaType != attachment.MediaTypePNG ||
		second.Attachment.MediaType != attachment.MediaTypeJPEG {
		t.Fatalf("两张图的引用串位了：%v / %v", first.Attachment, second.Attachment)
	}
	if len(store.saved) != 2 {
		t.Fatalf("落盘的张数不对：%d", len(store.saved))
	}
}

func TestPrepareImageProjectionDegradesWholeBatchOnOneBadImage(t *testing.T) {
	blocks := twoImages()
	blocks[2].MediaType = "image/tiff"
	content := prepareImageProjection(
		context.Background(), admitting(&fakeStore{}), tools.Execution{}, blocks, "demo")
	got := textOf(t, content)
	// 一张坏的把整批都拖下去；好的那一张要说得出「是同一次结果里另一张坏了」，
	// 不然读日志的人会以为是这一张自己有问题。
	want := []string{
		"说明",
		"[image unavailable: image/png; another image in the same result was invalid;" +
			" raw image data remains available to programmatic callers]",
		"[image unavailable: image/tiff; the declared media type is not PNG, JPEG, WebP, or GIF;" +
			" raw image data remains available to programmatic callers]",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("整批降级的说法不对：\n%s", strings.Join(got, "\n"))
	}
}

// TestImageDiagnosticNamesAnEmptyMediaType 盯住「一块图连媒体类型都没报」这一支。
//
// 诊断文本是模型唯一看得见的线索，空着的那一格必须自己说出来，否则那句话读起来
// 像是「image unavailable: ; ...」，看的人分不清是没报还是报了个空串。
func TestImageDiagnosticNamesAnEmptyMediaType(t *testing.T) {
	blocks := []contentBlock{{Type: blockImage, Data: []byte{1}}}
	content := prepareImageProjection(
		context.Background(), admitting(&fakeStore{}), tools.Execution{}, blocks, "demo")
	want := []string{
		"[image unavailable: unknown media type; the declared media type is not PNG, JPEG, WebP, or GIF;" +
			" raw image data remains available to programmatic callers]",
	}
	if got := textOf(t, content); !reflect.DeepEqual(got, want) {
		t.Fatalf("没报媒体类型时那句诊断不对：\n%s", strings.Join(got, "\n"))
	}
}

// TestParseInputSchema 盯住入参 schema 那两条**只有不守规矩的服务器**才走得到的支。
//
// 这两支从一台 SDK 服务器那边到不了（AddTool 要求 inputSchema 是个根上为 object
// 的合法 schema），所以这里直接问这个函数。
func TestParseInputSchema(t *testing.T) {
	// 缺 inputSchema：当成「一个不收参数的对象」，而不是让整台服务器的同步失败。
	node, err := parseInputSchema(&sdk.Tool{Name: "silent"}, "files")
	if err != nil {
		t.Fatalf("缺 inputSchema 不该失败：%v", err)
	}
	if node.Type != tools.TypeObject || len(node.Properties) != 0 {
		t.Fatalf("补出来的 schema 不是空对象：%#v", node)
	}

	// 排不成 JSON 的 inputSchema：这台服务器说的话本包接不住，让这次同步失败。
	if _, err := parseInputSchema(&sdk.Tool{Name: "weird", InputSchema: make(chan int)}, "files"); err == nil {
		t.Fatal("排不成 JSON 的 schema 本该失败")
	} else if !strings.Contains(err.Error(), "input schema is not JSON") {
		t.Fatalf("失败的说法不对：%v", err)
	}
}

func TestPrepareImageProjectionRefusalReasons(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	cases := []struct {
		name   string
		ctx    context.Context
		admit  ImageAdmission
		reason string
	}{
		{
			name:   "没装仓库",
			ctx:    context.Background(),
			admit:  nil,
			reason: "no attachment store is mounted",
		},
		{
			name:   "接缝交出一对空",
			ctx:    context.Background(),
			admit:  func(context.Context, tools.Execution) (attachment.Store, error) { return nil, nil },
			reason: "no attachment store is mounted",
		},
		{
			name: "接缝自己拒绝",
			ctx:  context.Background(),
			admit: func(context.Context, tools.Execution) (attachment.Store, error) {
				return nil, errors.New(`model "x" does not declare image input`)
			},
			reason: `model "x" does not declare image input`,
		},
		{
			name:   "准入期间被撤回",
			ctx:    cancelled,
			admit:  admitting(&fakeStore{}),
			reason: "the tool call was canceled before image storage",
		},
		{
			name: "限额把这批挡了",
			ctx:  context.Background(),
			admit: admitting(&fakeStore{failValidate: &attachment.Error{
				Code:    attachment.CodeImageTooLarge,
				Message: "Image exceeds the configured byte limit.",
			}}),
			reason: "image admission rejected the result: Image exceeds the configured byte limit.",
		},
		{
			name:   "存储自己坏了",
			ctx:    context.Background(),
			admit:  admitting(&fakeStore{failSave: errors.New("磁盘满了")}),
			reason: "durable image storage rejected the result",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			content := prepareImageProjection(
				testCase.ctx, testCase.admit, tools.Execution{}, twoImages(), "demo")
			got := textOf(t, content)
			if len(got) != 3 {
				t.Fatalf("块数不对：%#v", got)
			}
			for _, index := range []int{1, 2} {
				if !strings.Contains(got[index], testCase.reason) {
					t.Fatalf("第 %d 块没说出理由 %q：\n%s", index, testCase.reason, got[index])
				}
			}
		})
	}
}

func TestDecodeImageRejectsUnknownMediaType(t *testing.T) {
	if _, err := decodeImage(contentBlock{MediaType: "image/tiff"}); err == nil {
		t.Fatal("tiff 应当被拒")
	}
	input, err := decodeImage(contentBlock{MediaType: "image/webp", Data: []byte{7}})
	if err != nil {
		t.Fatalf("webp 应当通过：%v", err)
	}
	if input.MediaType != attachment.MediaTypeWebP || string(input.Data) != "\x07" {
		t.Fatalf("解出来的输入不对：%#v", input)
	}
}

// ---------------------------------------------------------------- 输出契约

func TestSupportedOutputSchema(t *testing.T) {
	if _, ok := supportedOutputSchema(nil); ok {
		t.Fatal("没有 schema 却说有")
	}
	// 排不出去的值：一个函数。
	if _, ok := supportedOutputSchema(func() {}); ok {
		t.Fatal("排不出去的东西却说有")
	}
	// 解不动的 schema：根本不是一个对象。
	if _, ok := supportedOutputSchema("不是对象"); ok {
		t.Fatal("解不动的 schema 却说有")
	}
	// 解得动但说不出口：items 挂在 object 上。
	if _, ok := supportedOutputSchema(map[string]any{
		"type":  "object",
		"items": map[string]any{"type": "string"},
	}); ok {
		t.Fatal("说不出口的 schema 却说有")
	}
	node, ok := supportedOutputSchema(map[string]any{"type": "object"})
	if !ok || node.Type != tools.TypeObject {
		t.Fatalf("一份好 schema 被拒了：%#v", node)
	}
}

func TestCreateOutputSchemaShape(t *testing.T) {
	withStructured := createOutput("demo", tools.Node{Type: tools.TypeObject}, true)
	if !reflect.DeepEqual(withStructured.Schema.Required, []string{"content", "structuredContent"}) {
		t.Fatalf("有结构化返回值时必填项不对：%#v", withStructured.Schema.Required)
	}
	without := createOutput("demo", tools.Node{}, false)
	if !reflect.DeepEqual(without.Schema.Required, []string{"content"}) {
		t.Fatalf("没有结构化返回值时必填项不对：%#v", without.Schema.Required)
	}
	if without.Schema.AdditionalProperties == nil || *without.Schema.AdditionalProperties {
		t.Fatal("额外字段应当被禁掉")
	}
	// content 的 items 是一个什么都不要求的节点：内容块的词汇表由对方定义。
	items := without.Schema.Properties[0].Schema.Items
	if items == nil || !reflect.DeepEqual(*items, tools.Node{}) {
		t.Fatalf("content 的 items 不该有约束：%#v", items)
	}
}

func TestCreateOutputRenderProjectsText(t *testing.T) {
	output := createOutput("demo", tools.Node{}, false)
	value := json.RawMessage(`{"content":[{"type":"text","text":"甲"},{"type":"audio"}]}`)
	content, err := output.Render(nil, value)
	if err != nil {
		t.Fatalf("渲染失败：%v", err)
	}
	got := textOf(t, content)
	want := []string{"甲\n[audio result unsupported: unknown media type;" +
		" raw audio data remains available to programmatic callers]"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("渲染出来的文本不对：%#v", got)
	}
	if _, err := output.Render(nil, json.RawMessage(`{`)); err == nil {
		t.Fatal("解不开的值应当报错")
	}
}

func TestNoOutput(t *testing.T) {
	if _, err := noOutput(true, nil); err == nil || err.Error() != "(no output)" {
		t.Fatalf("isError 那一支应当交回 (no output)：%v", err)
	}
	encoded, err := noOutput(false, json.RawMessage(`{"a":1}`))
	if err != nil {
		t.Fatalf("补一块文本失败：%v", err)
	}
	var result Result
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatalf("解不开：%v", err)
	}
	if len(result.Content) != 1 || wireType(result.Content[0]) != blockText {
		t.Fatalf("补出来的不是一块文本：%s", encoded)
	}
	if string(result.StructuredContent) != `{"a":1}` {
		t.Fatalf("结构化返回值丢了：%s", encoded)
	}
}

func TestEncodeStructured(t *testing.T) {
	raw, err := encodeStructured(nil)
	if err != nil || raw != nil {
		t.Fatalf("没有结构化返回值时应当是 nil：%s / %v", raw, err)
	}
	if _, err := encodeStructured(func() {}); err == nil {
		t.Fatal("排不出去的值应当报错")
	}
	raw, err = encodeStructured(map[string]any{"a": 1})
	if err != nil || string(raw) != `{"a":1}` {
		t.Fatalf("排出来的不对：%s / %v", raw, err)
	}
}

func TestIsJSONObject(t *testing.T) {
	cases := map[string]bool{
		``:          false,
		`null`:      false,
		`"字符串"`:     false,
		`[]`:        false,
		`{}`:        true,
		`{"a":1}`:   true,
		`{不是 JSON}`: false,
	}
	for raw, want := range cases {
		if got := isJSONObject(json.RawMessage(raw)); got != want {
			t.Fatalf("isJSONObject(%q) = %v", raw, got)
		}
	}
}

func TestJSONEqualIgnoresKeyOrder(t *testing.T) {
	if !jsonEqual(json.RawMessage(`{"a":1,"b":2}`), json.RawMessage(`{"b":2, "a":1}`)) {
		t.Fatal("键序不同不该被当成两件事")
	}
	if jsonEqual(json.RawMessage(`{"a":1}`), json.RawMessage(`{"a":2}`)) {
		t.Fatal("值不同却说是同一件事")
	}
	if jsonEqual(json.RawMessage(`{`), json.RawMessage(`{}`)) {
		t.Fatal("解不开的一侧应当判不等")
	}
	if jsonEqual(json.RawMessage(`{}`), json.RawMessage(`}`)) {
		t.Fatal("解不开的另一侧应当判不等")
	}
}

// ---------------------------------------------------------------- 撤销与投影表

func TestToolDisposersDisposeInReverseOrder(t *testing.T) {
	var order []int
	disposers := toolDisposers{
		func(context.Context) error { order = append(order, 0); return nil },
		func(context.Context) error { order = append(order, 1); return errors.New("第二个坏了") },
		func(context.Context) error { order = append(order, 2); return errors.New("第三个也坏了") },
	}
	err := disposers.disposeAll(context.Background())
	if !reflect.DeepEqual(order, []int{2, 1, 0}) {
		t.Fatalf("撤销顺序不是逆序：%#v", order)
	}
	// 第一个错误指的是**撤销顺序上**第一个，也就是第三个注册的那一个。
	if err == nil || err.Error() != "第三个也坏了" {
		t.Fatalf("交出来的不是第一个错误：%v", err)
	}
	if err := (toolDisposers)(nil).disposeAll(context.Background()); err != nil {
		t.Fatalf("空的一代不该报错：%v", err)
	}
}

func TestProjectionStoreTakesOnce(t *testing.T) {
	store := newProjectionStore()
	var token tools.ExecutionToken
	if _, ok := store.take(token); ok {
		t.Fatal("没记过的执行不该取得到")
	}
	store.stage(token, preparedProjection{value: json.RawMessage(`1`)})
	got, ok := store.take(token)
	if !ok || string(got.value) != "1" {
		t.Fatalf("取出来的不对：%#v", got)
	}
	if _, ok := store.take(token); ok {
		t.Fatal("读一次就该删掉，不然这张表会一直长")
	}
}

// ---------------------------------------------------------------- 配置与退避

func TestConfigValidate(t *testing.T) {
	cases := []struct {
		name   string
		config Config
	}{
		{"服务器名是空的", Config{URL: "http://x"}},
		{"服务器名带点", Config{ServerName: "a.b", URL: "http://x"}},
		{"服务器名太长", Config{ServerName: strings.Repeat("a", 33), URL: "http://x"}},
		{"没有 URL", Config{ServerName: "files"}},
		{"超时是负数", Config{ServerName: "files", URL: "http://x", ToolCallTimeout: -time.Second}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := testCase.config.validate(); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("应当被拒：%v", err)
			}
		})
	}
	validated, err := Config{ServerName: "files", URL: "http://x"}.validate()
	if err != nil {
		t.Fatalf("一份好配置被拒了：%v", err)
	}
	if validated.ToolCallTimeout != DefaultToolCallTimeout {
		t.Fatalf("没补上默认超时：%v", validated.ToolCallTimeout)
	}
}

func TestResolveReconnectPolicyDefaults(t *testing.T) {
	policy, err := resolveReconnectPolicy(ReconnectConfig{}, "x")
	if err != nil {
		t.Fatalf("零值配置被拒了：%v", err)
	}
	// 零值必须等于「开着，全用默认值」——这是 Disabled 取反那一步要保住的东西。
	want := ReconnectPolicy{
		Enabled:      true,
		InitialDelay: defaultInitialDelay,
		MaxDelay:     defaultMaxDelay,
		MaxAttempts:  defaultMaxAttempts,
	}
	if policy != want {
		t.Fatalf("解算出来的策略不对：%#v", policy)
	}
	off, err := resolveReconnectPolicy(ReconnectConfig{Disabled: true}, "x")
	if err != nil || off.Enabled {
		t.Fatalf("关掉重连没生效：%#v / %v", off, err)
	}
}

func TestResolveReconnectPolicyRejectsBadBounds(t *testing.T) {
	cases := []struct {
		name   string
		config ReconnectConfig
	}{
		{"首次延迟是负数", ReconnectConfig{InitialDelay: -time.Second}},
		{"上限是负数", ReconnectConfig{MaxDelay: -time.Second}},
		{"首次延迟大于上限", ReconnectConfig{InitialDelay: time.Minute, MaxDelay: time.Second}},
		{"次数是负数", ReconnectConfig{MaxAttempts: -1}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := resolveReconnectPolicy(testCase.config, "x"); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("应当被拒：%v", err)
			}
		})
	}
}

func TestBackoffDoublesAndClamps(t *testing.T) {
	policy := ReconnectPolicy{InitialDelay: 500 * time.Millisecond, MaxDelay: 4 * time.Second}
	want := []time.Duration{
		500 * time.Millisecond,
		time.Second,
		2 * time.Second,
		4 * time.Second,
		4 * time.Second,
	}
	for index, expected := range want {
		if got := backoff(policy, index+1); got != expected {
			t.Fatalf("第 %d 次退避是 %v，应当是 %v", index+1, got, expected)
		}
	}
	// 移够 64 位在 Go 里是回绕成 0，也就是「不等就重连」。这一条防的正是那件事。
	if got := backoff(policy, 200); got != policy.MaxDelay {
		t.Fatalf("溢出之后没有夹回上限：%v", got)
	}
	// 恰好在移位边界上：63 位以内但已经变成负数。
	huge := ReconnectPolicy{InitialDelay: time.Duration(1) << 62, MaxDelay: time.Second}
	if got := backoff(huge, 3); got != time.Second {
		t.Fatalf("翻倍溢出之后没有夹回上限：%v", got)
	}
}
