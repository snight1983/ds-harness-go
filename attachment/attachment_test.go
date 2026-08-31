// 本文件验这个接缝对外的那份契约：一批图按什么次序被校验和提交、哪些失败在动存储之前
// 就被挡住、以及「调用方能改对」和「存储坏了」这条分界线画在哪儿。
//
// 源: packages/attachment/attachment/tests/index.spec.ts
// 源: packages/attachment/attachment/tests/admission.spec.ts
//
// 这个包不碰文件系统也不碰网络，所有用例都跑在一个记账用的假 Store 上。这不是在
// 「把要验的东西挖掉」——这个包真正拥有的东西就是**次序**和**分类**，
// 而次序恰恰只能靠记录调用顺序来验。字节怎么落盘是实现方的契约，不是这里的。

package attachment_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"ds-harness-go/attachment"
)

// testLimits 是所有用例共用的限额，取值照抄 DSH 的测试夹具。
//
// 源: packages/attachment/attachment/tests/index.spec.ts:16-23
//
// 这几个数字是**互相咬合**的，不能随便改：单张上限 4 字节、整批上限 5 字节，
// 于是两张 3 字节的图各自都合格、加起来却超标——这正好把「单张限额」和「整批限额」
// 分成两条独立的路，否则一条断言能同时被两条规则满足，就分不出是哪条在起作用了。
var testLimits = attachment.ImageLimits{
	MaxImageBytes:        4,
	MaxImagesPerMessage:  2,
	MaxMessageImageBytes: 5,
	MaxImagePixels:       4,
	MaxImageDimension:    2000,
	MediaTypes:           []attachment.MediaType{attachment.MediaTypePNG},
}

// errValidate 和 errSave 是假 Store 用来标记「我在这一步失败了」的哨兵。
// 用哨兵而不是每次现造一个 error，是为了断言能验**同一个值**原样传了出来，
// 而不只是「有个错误」。
var (
	errValidate = errors.New("这一张校验不通过")
	errSave     = errors.New("这一张写不进去")
)

// recordingStore 是一个只记账、不存字节的 Store。
//
// 源: packages/attachment/attachment/tests/index.spec.ts:25-73
//
// 它拿每张图的**第一个字节当身份**，于是调用顺序可以写成 "validate:1"、"save:2"
// 这样一串可直接比较的字符串——这个包要验的次序，就是这一串。
type recordingStore struct {
	// limits 每个假 Store 一份，个别用例改它不会影响别的用例。
	limits attachment.ImageLimits
	// calls 是按发生顺序记下来的每一次调用。
	calls []string
	// savedInputs 是 SaveImage 实际收到的入参，用来验字段有没有原样传过去。
	savedInputs []attachment.ImageInput
	// failValidateAt / failSaveAt 是「身份等于这个值时失败」，-1 表示从不失败。
	failValidateAt int
	failSaveAt     int
}

// newRecordingStore 造一个默认限额、从不失败的假 Store。
func newRecordingStore() *recordingStore {
	return &recordingStore{limits: testLimits, failValidateAt: -1, failSaveAt: -1}
}

// mark 取一张图的身份：第一个字节，空图算 0。
func mark(input attachment.ImageInput) int {
	if len(input.Data) == 0 {
		return 0
	}
	return int(input.Data[0])
}

func (s *recordingStore) ImageLimits() attachment.ImageLimits { return s.limits }

func (s *recordingStore) ValidateImage(_ context.Context, input attachment.ImageInput) error {
	value := mark(input)
	s.calls = append(s.calls, fmt.Sprintf("validate:%d", value))
	if value == s.failValidateAt {
		return errValidate
	}
	return nil
}

func (s *recordingStore) SaveImage(
	_ context.Context, input attachment.ImageInput,
) (attachment.ImageRef, error) {
	value := mark(input)
	s.calls = append(s.calls, fmt.Sprintf("save:%d", value))
	s.savedInputs = append(s.savedInputs, input)
	if value == s.failSaveAt {
		return attachment.ImageRef{}, errSave
	}
	return attachment.ImageRef{
		ID:        attachment.ID(fmt.Sprintf("sha256:%064d", value)),
		MediaType: input.MediaType,
		Bytes:     len(input.Data),
		Width:     1,
		Height:    1,
		Name:      input.Name,
	}, nil
}

func (s *recordingStore) ReadImage(
	_ context.Context, _ attachment.ImageRef,
) (attachment.StoredImage, error) {
	return attachment.StoredImage{}, errors.New("这个用例用不到 ReadImage")
}

// projectingStore 是一个**额外**具备派生请求图能力的假 Store。
//
// 源: packages/attachment/attachment/tests/index.spec.ts:55-72
//
// 它靠内嵌 recordingStore 拿到那三个必需方法，自己只多实现一个——这正好演示了
// 可选能力在 Go 里长什么样：多一个方法就多一种能力，不需要在基接口上开洞。
type projectingStore struct {
	*recordingStore
}

func (s *projectingStore) ReadImageRequest(
	_ context.Context, ref attachment.ImageRef, policy attachment.RequestPolicy,
) (attachment.RequestImage, error) {
	s.calls = append(s.calls, fmt.Sprintf("request:%s", ref.Name))
	return attachment.RequestImage{
		VariantID:  attachment.VariantID(fmt.Sprintf("sha256:%064d", policy.MaxPixels)),
		Attachment: ref,
		Data:       []byte{byte(ref.Bytes)},
		MediaType:  ref.MediaType,
		Bytes:      1,
		Width:      ref.Width,
		Height:     ref.Height,
		Depth:      attachment.DepthUChar,
		Space:      attachment.SpaceSRGB,
		HasAlpha:   false,
	}, nil
}

// image 造一张身份为 value 的单字节图。
func image(value int) attachment.ImageInput {
	return attachment.ImageInput{
		Data:      []byte{byte(value)},
		MediaType: attachment.MediaTypePNG,
		Name:      fmt.Sprintf("%d.png", value),
	}
}

// sameCalls 比调用序列。
func sameCalls(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// attachmentError 把错误拆成 *attachment.Error，不是这个类型就终止用例。
func attachmentError(t *testing.T, err error) *attachment.Error {
	t.Helper()
	var typed *attachment.Error
	if !errors.As(err, &typed) {
		t.Fatalf("该是 *attachment.Error，得到 %#v", err)
	}
	return typed
}

// TestSaveImagesValidatesEveryMemberBeforeCommittingAny 验三段次序：整批规则 → 逐张校验 → 逐张提交。
//
// 源: packages/attachment/attachment/tests/index.spec.ts:96-103
//
// 这条断言的是一整串顺序而不是「都被调过了」。区别在于：一个边校验边写的实现
// （validate:1 save:1 validate:2 save:2）会调到同样的方法同样的次数，只有顺序不同，
// 而那个实现正是这一层要挡住的——第二张不合格时，第一张已经落盘且永远无人引用。
func TestSaveImagesValidatesEveryMemberBeforeCommittingAny(t *testing.T) {
	t.Parallel()
	store := newRecordingStore()

	refs, err := attachment.SaveImages(context.Background(), store, []attachment.ImageInput{image(1), image(2)})
	if err != nil {
		t.Fatalf("SaveImages 失败：%v", err)
	}

	if !sameCalls(store.calls, "validate:1", "validate:2", "save:1", "save:2") {
		t.Fatalf("次序该是「先全部校验、再逐张提交」，得到 %v", store.calls)
	}
	if len(refs) != 2 || refs[0].Name != "1.png" || refs[1].Name != "2.png" {
		t.Errorf("引用该按输入顺序返回，得到 %+v", refs)
	}
}

// TestSaveImagesRejectsBatchRulesBeforeTouchingTheStore 验三条整批规则各自的码，且**一次都没调 Store**。
//
// 源: packages/attachment/attachment/tests/index.spec.ts:105-117
//
// 「一次都没调」是这条的重点：这三条光看输入就能判，判得出却仍然去解一遍栅格，
// 等于让一个超限的批次白白吃掉整批的解码开销——而超限批次恰恰是最容易被刷的那种请求。
//
// 三条的次序也被钉住：三张 1 字节的图同时满足「张数超了」，字节总和 3 却没超 5，
// 所以它只可能报 TOO_MANY_IMAGES；两张 3 字节的图张数没超、总和 6 超了 5，
// 所以它只可能报 IMAGES_TOO_LARGE。两条各自只有一条路能走通，顺序写反就有一条会挂。
func TestSaveImagesRejectsBatchRulesBeforeTouchingTheStore(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		inputs []attachment.ImageInput
		want   attachment.Code
	}{
		"张数超了": {
			[]attachment.ImageInput{image(1), image(2), image(3)},
			attachment.CodeTooManyImages,
		},
		"字节总和超了": {
			[]attachment.ImageInput{
				{Data: []byte{1, 2, 3}, MediaType: attachment.MediaTypePNG},
				{Data: []byte{4, 5, 6}, MediaType: attachment.MediaTypePNG},
			},
			attachment.CodeImagesTooLarge,
		},
		"这个部署不收这种格式": {
			[]attachment.ImageInput{{Data: []byte{1}, MediaType: attachment.MediaTypeJPEG}},
			attachment.CodeUnsupportedImageType,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store := newRecordingStore()

			refs, err := attachment.SaveImages(context.Background(), store, testCase.inputs)
			if err == nil {
				t.Fatalf("该失败")
			}
			if code := attachmentError(t, err).Code; code != testCase.want {
				t.Errorf("码该是 %s，得到 %s", testCase.want, code)
			}
			if refs != nil {
				t.Errorf("失败时不该返回任何引用，得到 %+v", refs)
			}
			if len(store.calls) != 0 {
				t.Errorf("整批规则判得出来就不该调 Store，得到 %v", store.calls)
			}
		})
	}
}

// TestSaveImagesStartsNoWritesWhenAnyMemberFailsValidation 验一张不合格就一张都不写。
//
// 源: packages/attachment/attachment/tests/index.spec.ts:119-126
//
// 失败的是**第二张**，所以断言里必须看到 validate:1 也发生过——只验「没有 save」的话，
// 一个在第一张就停下来的实现也能通过，而那不是这里要的语义。
func TestSaveImagesStartsNoWritesWhenAnyMemberFailsValidation(t *testing.T) {
	t.Parallel()
	store := newRecordingStore()
	store.failValidateAt = 2

	refs, err := attachment.SaveImages(context.Background(), store, []attachment.ImageInput{image(1), image(2)})

	if !errors.Is(err, errValidate) {
		t.Fatalf("该原样返回校验错误，得到 %v", err)
	}
	if !sameCalls(store.calls, "validate:1", "validate:2") {
		t.Errorf("整批校验该跑完且一次写都不该开始，得到 %v", store.calls)
	}
	if refs != nil {
		t.Errorf("失败时不该返回任何引用，得到 %+v", refs)
	}
}

// TestSaveImagesReturnsNoPartialReferences 验写到一半失败时，先前成功的那几张也不返回引用。
//
// 源: packages/attachment/attachment/tests/index.spec.ts:128-135
//
// 这一层**没有事务**：save:1 确实已经落盘了。它保证的是更弱但可兑现的一条——
// 一个部分成功的批次不交出任何引用，于是没有人能指向那些对象，它们等保留策略来收。
// 断言 refs 为 nil 就是在钉这一条：交出半批引用的实现会让一条消息带着
// 「本该有两张图但只有一张」的历史存下去，而那是不可逆的。
func TestSaveImagesReturnsNoPartialReferences(t *testing.T) {
	t.Parallel()
	store := newRecordingStore()
	store.failSaveAt = 2

	refs, err := attachment.SaveImages(context.Background(), store, []attachment.ImageInput{image(1), image(2)})

	if !errors.Is(err, errSave) {
		t.Fatalf("该原样返回写入错误，得到 %v", err)
	}
	if !sameCalls(store.calls, "validate:1", "validate:2", "save:1", "save:2") {
		t.Errorf("调用序列不对：%v", store.calls)
	}
	if refs != nil {
		t.Errorf("部分成功不该交出任何引用，得到 %+v", refs)
	}
}

// TestSaveImagesAcceptsAnEmptyBatch 验空批次是合法输入，且拿到的是空列表而不是 nil。
//
// 一条不带图的消息走的就是这条路。返回 nil 的话，调用方每次都要多写一条判空分支，
// 而漏写的那次会在 range 上悄悄什么都不做——不是崩，是静默的行为差异。
func TestSaveImagesAcceptsAnEmptyBatch(t *testing.T) {
	t.Parallel()
	store := newRecordingStore()

	refs, err := attachment.SaveImages(context.Background(), store, nil)
	if err != nil {
		t.Fatalf("空批次该成功：%v", err)
	}
	if refs == nil {
		t.Errorf("该返回空列表而不是 nil")
	}
	if len(refs) != 0 || len(store.calls) != 0 {
		t.Errorf("空批次不该产生任何引用或调用，得到 refs=%v calls=%v", refs, store.calls)
	}
}

// TestValidateImageBatchIsCallableByImplementations 验整批规则可以被实现方单独调。
//
// DSH 那边它是 protected，子类调得到。Go 里它是导出的包级函数，别的包里的实现方
// 照样调得到——这条断言的就是这个可达性，以及它不需要一个 Store 就能用。
func TestValidateImageBatchIsCallableByImplementations(t *testing.T) {
	t.Parallel()

	if err := attachment.ValidateImageBatch(testLimits, []attachment.ImageInput{image(1)}); err != nil {
		t.Errorf("合格的一批不该报错：%v", err)
	}
	err := attachment.ValidateImageBatch(testLimits, []attachment.ImageInput{image(1), image(2), image(3)})
	if code := attachmentError(t, err).Code; code != attachment.CodeTooManyImages {
		t.Errorf("码该是 %s，得到 %s", attachment.CodeTooManyImages, code)
	}
}

// TestReadImageRequestReportsUnsupportedProjection 验不具备派生能力时报的是哪个码，
// 以及**取消优先于不支持**。
//
// 源: packages/attachment/attachment/tests/index.spec.ts:138-149
//
// 后半条是次序断言，不是多余的：调用方已经走了的时候，「这个实现支持不支持」
// 根本不是它要知道的事。把两者写反的实现会在日志里堆出一串
// 「能力缺口」告警，而那个能力其实从来没有被真正需要过——一条追着假线索去查的告警。
func TestReadImageRequestReportsUnsupportedProjection(t *testing.T) {
	t.Parallel()
	store := newRecordingStore()
	policy := attachment.RequestPolicy{MaxPixels: 1, MaxBytes: 1}

	_, err := attachment.ReadImageRequest(context.Background(), store, attachment.ImageRef{}, policy)
	if err == nil {
		t.Fatalf("不具备派生能力时该失败")
	}
	if code := attachmentError(t, err).Code; code != attachment.CodeAttachmentProjectionUnsupported {
		t.Errorf("码该是 %s，得到 %s", attachment.CodeAttachmentProjectionUnsupported, code)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = attachment.ReadImageRequest(cancelled, store, attachment.ImageRef{}, policy)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消该优先于「不支持」，得到 %v", err)
	}
	var typed *attachment.Error
	if errors.As(err, &typed) {
		t.Errorf("一次正常的取消不该被报成能力缺口：%v", typed)
	}
}

// TestReadImageRequestUsesTheProjectorWhenPresent 验实现方满足可选能力时确实走了它。
//
// 源: packages/attachment/attachment/tests/index.spec.ts:55-72
//
// 只验「不支持那条路」的话，一个把类型断言写反、于是**永远**报不支持的实现也能通过。
func TestReadImageRequestUsesTheProjectorWhenPresent(t *testing.T) {
	t.Parallel()
	store := &projectingStore{recordingStore: newRecordingStore()}
	ref := attachment.ImageRef{Name: "1.png", Bytes: 7, Width: 3, Height: 4, MediaType: attachment.MediaTypePNG}

	got, err := attachment.ReadImageRequest(
		context.Background(), store, ref, attachment.RequestPolicy{MaxPixels: 9, MaxBytes: 1})
	if err != nil {
		t.Fatalf("具备派生能力时该成功：%v", err)
	}

	if !sameCalls(store.calls, "request:1.png") {
		t.Errorf("该把调用转给派生实现，得到 %v", store.calls)
	}
	if got.Attachment != ref || got.Width != 3 || got.Height != 4 {
		t.Errorf("该原样带回派生实现给的结果，得到 %+v", got)
	}
	if got.Depth != attachment.DepthUChar || got.Space != attachment.SpaceSRGB {
		t.Errorf("位深和色彩空间该原样带回，得到 %s/%s", got.Depth, got.Space)
	}
	// VariantID 覆盖策略：换一个策略必须换一个 VariantID，否则缓存会串味。
	other, err := attachment.ReadImageRequest(
		context.Background(), store, ref, attachment.RequestPolicy{MaxPixels: 10, MaxBytes: 1})
	if err != nil {
		t.Fatalf("第二次派生该成功：%v", err)
	}
	if other.VariantID == got.VariantID {
		t.Errorf("不同策略该给出不同的 VariantID，两次都是 %s", got.VariantID)
	}
}

// TestAdmitEncodedImagesDecodesEveryMemberThenDelegatesOneOrderedBatch 验线上入口的整条链路。
//
// 源: packages/attachment/attachment/tests/admission.spec.ts:24-35
//
// "AAAA" 是三个零字节的规范 base64。断言里同时看字节数和顺序，是为了钉住
// **解码结果原样进了 Store**：一个把 base64 串直接当字节传下去的实现，
// 长度会是 4 而不是 3。
func TestAdmitEncodedImagesDecodesEveryMemberThenDelegatesOneOrderedBatch(t *testing.T) {
	t.Parallel()
	store := newRecordingStore()
	// DSH 那边是把整个 saveImages 换成了 mock，所以限额在那条用例里根本不参与。
	// Go 这边走的是真的 SaveImages，限额必须放宽到能收下这一批，否则挡住这一批的
	// 是「整批字节超标」而不是本用例要验的那件事——testLimits 的整批上限是 5 字节，
	// 而两张 "AAAA" 解出来一共 6 字节。
	store.limits.MediaTypes = []attachment.MediaType{attachment.MediaTypePNG, attachment.MediaTypeJPEG}
	store.limits.MaxMessageImageBytes = 6

	refs, err := attachment.AdmitEncodedImages(context.Background(), store, []attachment.EncodedImage{
		{MediaType: attachment.MediaTypePNG, Data: "AAAA", Name: "first.png"},
		{MediaType: attachment.MediaTypeJPEG, Data: "AAAA", Name: "second.jpg"},
	})
	if err != nil {
		t.Fatalf("AdmitEncodedImages 失败：%v", err)
	}

	if len(store.savedInputs) != 2 {
		t.Fatalf("该提交两张，得到 %d 张", len(store.savedInputs))
	}
	for index, want := range []struct {
		name      string
		mediaType attachment.MediaType
		bytes     int
	}{
		{"first.png", attachment.MediaTypePNG, 3},
		{"second.jpg", attachment.MediaTypeJPEG, 3},
	} {
		got := store.savedInputs[index]
		if got.Name != want.name || got.MediaType != want.mediaType || len(got.Data) != want.bytes {
			t.Errorf("第 %d 张该是 %+v，得到 name=%q type=%s bytes=%d",
				index, want, got.Name, got.MediaType, len(got.Data))
		}
	}
	if len(refs) != 2 || refs[0].Name != "first.png" || refs[1].Name != "second.jpg" {
		t.Errorf("引用该按输入顺序返回，得到 %+v", refs)
	}
}

// TestAdmitEncodedImagesCarriesAnAbsentNameThrough 验没有显示名时不凭空造一个。
//
// 源: packages/attachment/attachment/tests/admission.spec.ts:37-43
//
// DSH 那边验的是「store 收到的对象上没有 name 这个键」。Go 里空串就是「没有」，
// 所以对应的断言是空串一路传到底、没被替换成文件名或者标识之类的东西——
// 显示名来自浏览器，是不可信输入，接缝无权替操作者编一个。
func TestAdmitEncodedImagesCarriesAnAbsentNameThrough(t *testing.T) {
	t.Parallel()
	store := newRecordingStore()

	refs, err := attachment.AdmitEncodedImages(context.Background(), store,
		[]attachment.EncodedImage{{MediaType: attachment.MediaTypePNG, Data: "AAAA"}})
	if err != nil {
		t.Fatalf("AdmitEncodedImages 失败：%v", err)
	}

	if store.savedInputs[0].Name != "" {
		t.Errorf("没有显示名就该保持空串，得到 %q", store.savedInputs[0].Name)
	}
	if refs[0].Name != "" {
		t.Errorf("引用上的显示名也该是空串，得到 %q", refs[0].Name)
	}
}

// TestAdmitEncodedImagesDelegatesAnEmptyBatch 验空批次一路传到底。
//
// 源: packages/attachment/attachment/tests/admission.spec.ts:45-49
func TestAdmitEncodedImagesDelegatesAnEmptyBatch(t *testing.T) {
	t.Parallel()
	store := newRecordingStore()

	refs, err := attachment.AdmitEncodedImages(context.Background(), store, nil)
	if err != nil {
		t.Fatalf("空批次该成功：%v", err)
	}
	if refs == nil || len(refs) != 0 {
		t.Errorf("该返回空列表，得到 %+v", refs)
	}
	if len(store.calls) != 0 {
		t.Errorf("空批次不该产生任何调用，得到 %v", store.calls)
	}
}

// TestAdmitEncodedImagesRejectsNonCanonicalBase64BeforeAnyStoreCall 验非规范 base64 的拒绝面。
//
// 源: packages/attachment/attachment/tests/admission.spec.ts:51-58
//
// DSH 只钉了三条（""、"AAA"、"!!!!"），因为它靠「解码再编码回去比一比」这个往返检查，
// 那三条足以说明往返在跑。Go 用的是 base64.StdEncoding.Strict() 加两条显式检查，
// 拒绝面是一条条拼出来的，所以这里必须逐条钉住——尤其是下面这三条，
// 少任何一条都会让一种更宽松的实现悄悄通过：
//
//   - ""：Strict 认为空串是合法的零字节编码，**只有**那条显式的空串检查能拒掉它。
//   - "AAAA\n" / "\nAAAA"：Go 的解码器**有意忽略** \r 和 \n（为了读 PEM 那类分行编码），
//     只有那条 ContainsAny 检查能拒掉它们。这是和 Node 往返检查差异最大的一处。
//   - "AB=="：尾部余位非零。它是 Strict() 相对普通解码器多拒的那一类，
//     换成非 Strict 的解码器这条会静默通过，而它正是「同一串字节有多种写法」的来源。
//
// 「一次 Store 都没调」同样是断言的一部分：一批里有一条坏的，整批就发不出去，
// 那就不该为它先动存储。
func TestAdmitEncodedImagesRejectsNonCanonicalBase64BeforeAnyStoreCall(t *testing.T) {
	t.Parallel()

	for name, data := range map[string]string{
		"空串":       "",
		"长度不是四的倍数": "AAA",
		"字母表外的字符":  "!!!!",
		"尾部余位非零":   "AB==",
		"末尾有换行":    "AAAA\n",
		"开头有换行":    "\nAAAA",
		"中间有回车":    "AA\rAA",
		"中间有空格":    "AA AA",
		"多余的填充":    "AAAA=",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store := newRecordingStore()

			refs, err := attachment.AdmitEncodedImages(context.Background(), store,
				[]attachment.EncodedImage{{MediaType: attachment.MediaTypePNG, Data: data}})
			if err == nil {
				t.Fatalf("%q 该被拒", data)
			}
			if code := attachmentError(t, err).Code; code != attachment.CodeInvalidImageBase64 {
				t.Errorf("码该是 %s，得到 %s", attachment.CodeInvalidImageBase64, code)
			}
			if refs != nil {
				t.Errorf("失败时不该返回任何引用，得到 %+v", refs)
			}
			if len(store.calls) != 0 {
				t.Errorf("载荷坏了就不该动存储，得到 %v", store.calls)
			}
		})
	}
}

// TestAdmitEncodedImagesAcceptsEveryCanonicalPaddingForm 验三种规范写法都收。
//
// 拒绝面收得太紧和放得太松一样是缺陷：把带填充的 "AA==" 判成非法，
// 等于让所有字节数不是 3 的倍数的图都传不上来——那是绝大多数图。
func TestAdmitEncodedImagesAcceptsEveryCanonicalPaddingForm(t *testing.T) {
	t.Parallel()

	for data, wantBytes := range map[string]int{"AAAA": 3, "AAA=": 2, "AA==": 1} {
		t.Run(data, func(t *testing.T) {
			t.Parallel()
			store := newRecordingStore()

			if _, err := attachment.AdmitEncodedImages(context.Background(), store,
				[]attachment.EncodedImage{{MediaType: attachment.MediaTypePNG, Data: data}}); err != nil {
				t.Fatalf("%q 是规范形，该被接受：%v", data, err)
			}
			if got := len(store.savedInputs[0].Data); got != wantBytes {
				t.Errorf("%q 该解出 %d 字节，得到 %d", data, wantBytes, got)
			}
		})
	}
}

// TestAdmitEncodedImagesPropagatesTheStoreRejectionUnchanged 验存储那边的拒绝原样往上传。
//
// 源: packages/attachment/attachment/tests/admission.spec.ts:60-65
//
// 准入这一层不重新包装、不改码：它只负责剥 base64。把存储的失败裹上一层
// 「准入失败」会让码从存储故障变成调用方可改正的错误，于是界面会去催操作者换图，
// 而磁盘满了这件事没有任何人看见。
func TestAdmitEncodedImagesPropagatesTheStoreRejectionUnchanged(t *testing.T) {
	t.Parallel()
	store := newRecordingStore()
	store.failSaveAt = 0 // "AAAA" 解出来是三个零字节，身份就是 0

	_, err := attachment.AdmitEncodedImages(context.Background(), store,
		[]attachment.EncodedImage{{MediaType: attachment.MediaTypePNG, Data: "AAAA"}})

	if !errors.Is(err, errSave) {
		t.Fatalf("该原样返回存储的错误，得到 %v", err)
	}
}

// foreignCodedError 是一个来自「别的包」的错误：它带同一套码，但不是 *attachment.Error。
//
// 源: packages/attachment/attachment/tests/admission.spec.ts:62
// 源: packages/attachment/attachment/tests/index.spec.ts:156
//
// 它存在的理由就是那两处：DSH 明确钉住一个外来错误只要带着码就算数。
// 这条不是可有可无的宽松——llm 那个包依赖本包，所以它的错误类型**不可能**
// 反过来继承本包的类型（会成环），而它抛出的图片准入失败必须能被同样地分类。
type foreignCodedError struct {
	code string
}

func (e *foreignCodedError) Error() string     { return "来自别的包的错误：" + e.code }
func (e *foreignCodedError) ErrorCode() string { return e.code }

// TestIsImageAdmissionErrorSeparatesCallerFixableFromStorageFaults 验那条分界线。
//
// 源: packages/attachment/attachment/tests/index.spec.ts:151-161
//
// 分界线的判据是**谁能让下一次尝试成功**：调用方换张图就能过的算准入失败，
// 换多少次都一样的算存储故障。界面按它决定是提示操作者改输入，还是报系统故障。
//
// 「不带码的普通错误算 false」这一条是安全偏向：判不出来就当存储故障。
// 反过来偏的话，一次磁盘故障会被显示成「你的图不行」，操作者会一直换图，
// 而真正的问题没有任何人看见。
func TestIsImageAdmissionErrorSeparatesCallerFixableFromStorageFaults(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		err  error
		want bool
	}{
		"本包的准入码": {&attachment.Error{Code: attachment.CodeInvalidImage, Message: "bad bytes"}, true},
		"本包的另一个准入码": {
			&attachment.Error{Code: attachment.CodeInvalidImageBase64, Message: "bad base64"}, true,
		},
		"整批规则的码":    {&attachment.Error{Code: attachment.CodeTooManyImages, Message: "too many"}, true},
		"外来错误带同一套码": {&foreignCodedError{code: string(attachment.CodeImageTooLarge)}, true},
		"存储对象坏了": {
			&attachment.Error{Code: attachment.CodeAttachmentCorrupt, Message: "corrupt object"}, false,
		},
		"写入失败": {
			&attachment.Error{Code: attachment.CodeAttachmentWriteFailed, Message: "disk failed"}, false,
		},
		"外来错误带不认识的码": {&foreignCodedError{code: "SOMETHING_ELSE"}, false},
		"不带码的普通错误":   {errors.New("unknown failure"), false},
		"nil":        {nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := attachment.IsImageAdmissionError(testCase.err); got != testCase.want {
				t.Errorf("IsImageAdmissionError(%v) 该是 %v，得到 %v", testCase.err, testCase.want, got)
			}
		})
	}
}

// TestIsImageAdmissionErrorLooksThroughWrapping 验它顺着包装链往下问。
//
// 新增: DSH 只看最外面那一层。Go 里用 fmt.Errorf("...: %w", err) 包一层是常态，
// 只看最外层等于让「路上有没有人包过一下」决定这个分类的结果——
// 而包装的人往往只是想加一句上下文，并不知道自己顺手改变了界面的行为。
func TestIsImageAdmissionErrorLooksThroughWrapping(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf("上传第 2 张时：%w",
		&attachment.Error{Code: attachment.CodeImageTooManyPixels, Message: "too many pixels"})
	if !attachment.IsImageAdmissionError(wrapped) {
		t.Errorf("包了一层之后该仍然认得出来")
	}

	wrappedFault := fmt.Errorf("上传第 2 张时：%w",
		&attachment.Error{Code: attachment.CodeAttachmentWriteFailed, Message: "disk failed"})
	if attachment.IsImageAdmissionError(wrappedFault) {
		t.Errorf("包了一层不该把存储故障变成准入失败")
	}
}

// TestErrorCarriesItsCauseAndCode 验错误自己那几件事：文本带码、底层原因问得到、码取得出来。
//
// 底层原因必须还在：排查一次上传失败靠的正是它。而 Message 里**不许**出现原始字节或
// 宿主机路径——这条是 DSH 在构造函数文档里写死的约束，理由是这段文字会跟着 RPC
// 一路送到客户端。这里能自动验的是「底层原因没有被塞进 Message」这一半。
func TestErrorCarriesItsCauseAndCode(t *testing.T) {
	t.Parallel()
	cause := errors.New("illegal base64 data at input byte 2")
	err := &attachment.Error{
		Code:    attachment.CodeInvalidImageBase64,
		Message: "Image upload is not canonical base64.",
		Err:     cause,
	}

	if !errors.Is(err, cause) {
		t.Errorf("该问得到底层原因")
	}
	if err.ErrorCode() != string(attachment.CodeInvalidImageBase64) {
		t.Errorf("ErrorCode 该是 %s，得到 %s", attachment.CodeInvalidImageBase64, err.ErrorCode())
	}
	if !strings.Contains(err.Error(), string(attachment.CodeInvalidImageBase64)) {
		t.Errorf("错误文本该带上码，得到 %q", err.Error())
	}

	bare := &attachment.Error{Code: attachment.CodeAttachmentNotFound, Message: "no such attachment"}
	if bare.Unwrap() != nil {
		t.Errorf("没有底层原因时该是 nil")
	}
	if strings.Contains(bare.Error(), "%!") {
		t.Errorf("没有底层原因时的文本不该有格式化残留，得到 %q", bare.Error())
	}
}

// TestStoreSatisfiesTheSeam 是编译期断言的运行期同伴：两个假 Store 各自满足哪些接口。
//
// 光有类型断言还不够——它只说明方法集对得上。这里额外验 projectingStore 确实**被认成**
// 具备可选能力，而 recordingStore 确实**没有**，因为 [attachment.ReadImageRequest]
// 的分派完全压在这个判断上。
func TestStoreSatisfiesTheSeam(t *testing.T) {
	t.Parallel()

	var plain attachment.Store = newRecordingStore()
	if _, ok := plain.(attachment.RequestImageProjector); ok {
		t.Errorf("不实现派生方法的 Store 不该被认成具备可选能力")
	}

	var capable attachment.Store = &projectingStore{recordingStore: newRecordingStore()}
	if _, ok := capable.(attachment.RequestImageProjector); !ok {
		t.Errorf("实现了派生方法的 Store 该被认成具备可选能力")
	}
}
