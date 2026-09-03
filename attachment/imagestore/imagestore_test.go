// 本文件的作用：把这台存储钉在「摘要是唯一判据」这件事上。
//
// # 这些测试防的是什么错
//
//   - **去重变成了覆盖**。同一批字节存两次必须落同一个键、而且只写一次。
//     真去写第二次的话，一次内容不同却撞了键的写就会静默地毁掉在先的那一份。
//   - **坏掉的字节被交了出去**。存储里那份被改过一个 bit，读回来必须报
//     [attachment.CodeAttachmentCorrupt]，不是把它当成好的交出去。
//   - **引用被当成了路径**。标识只认 `sha256:` 加 64 位小写十六进制，
//     别的一律拒——这条塌了，一个 `../` 就能读到对象树外面去。
//   - **准入次序和 attachment-local 分了岔**。一张同时犯几条的图，两边报的码
//     必须是同一个，否则同一份客户端在两种部署上看到的提示不一样。
//   - **显示名把客户端的本地路径带进了会话日志**。
//   - **WebP 认不出来**。接缝上声明了这个媒体类型，认不出等于这个类型是假的。

package imagestore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/attachment"
	"github.com/snight1983/ds-harness-go/fs"
	"github.com/snight1983/ds-harness-go/fs/fstest"
)

// root 是所有用例共用的对象树根。
const root = "/attachments/v1"

// newStore 装一台跑在内存文件系统上的存储，同时把那台文件系统交回去，
// 好让用例直接看介质上到底有什么。
func newStore(t *testing.T, tune ...func(*Config)) (*Store, *fstest.FS) {
	t.Helper()
	medium := fstest.New()
	config := Config{FS: medium, Root: root}
	for _, apply := range tune {
		apply(&config)
	}
	store, err := New(config)
	if err != nil {
		t.Fatalf("装配存储：%v", err)
	}
	return store, medium
}

// rasterBytes 造一张确定性的栅格，编码成 encode 指定的格式。
func rasterBytes(t *testing.T, width int, height int, encode func(*bytes.Buffer, image.Image) error) []byte {
	t.Helper()
	raster := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			raster.Set(x, y, color.RGBA{R: uint8(x * 20), G: uint8(y * 20), B: 0x40, A: 0xff})
		}
	}
	buffer := &bytes.Buffer{}
	if err := encode(buffer, raster); err != nil {
		t.Fatalf("编码栅格：%v", err)
	}
	return buffer.Bytes()
}

func pngBytes(t *testing.T, width int, height int) []byte {
	t.Helper()
	return rasterBytes(t, width, height, func(buffer *bytes.Buffer, raster image.Image) error {
		return png.Encode(buffer, raster)
	})
}

func jpegBytes(t *testing.T, width int, height int) []byte {
	t.Helper()
	return rasterBytes(t, width, height, func(buffer *bytes.Buffer, raster image.Image) error {
		return jpeg.Encode(buffer, raster, nil)
	})
}

func gifBytes(t *testing.T, width int, height int) []byte {
	t.Helper()
	return rasterBytes(t, width, height, func(buffer *bytes.Buffer, raster image.Image) error {
		return gif.Encode(buffer, raster, nil)
	})
}

// webpHeader 拼一段**只有文件头**的无损 WebP。
//
// Go 这边没有 WebP 编码器，所以造不出一张真解得开的 WebP。这段字节只够
// [image.DecodeConfig] 认出格式和宽高，那也正是这个包对 WebP 唯一的自有行为——
// 再往下的完整解码是 golang.org/x/image 的事，不是本包的。
func webpHeader(width int, height int) []byte {
	payload := make([]byte, 5)
	payload[0] = 0x2f // VP8L 签名
	binary.LittleEndian.PutUint32(payload[1:], uint32(width-1)|uint32(height-1)<<14)

	chunk := append([]byte("VP8L"), le32(len(payload))...)
	chunk = append(chunk, payload...)
	if len(payload)%2 == 1 {
		chunk = append(chunk, 0) // RIFF 的块要补齐到偶数长度
	}
	body := append([]byte("WEBP"), chunk...)
	return append(append([]byte("RIFF"), le32(len(body))...), body...)
}

func le32(value int) []byte {
	out := make([]byte, 4)
	binary.LittleEndian.PutUint32(out, uint32(value))
	return out
}

// objectKey 按本包声明的布局算出一份字节该落在哪个键上。用例自己重算一遍，
// 于是布局本身也被钉住了。
func objectKey(data []byte) fs.TargetKey {
	sum := sha256.Sum256(data)
	text := hex.EncodeToString(sum[:])
	return fs.TargetKey(root + "/objects/" + text[:2] + "/" + text)
}

// requireCode 断言 err 是一个带 want 这个码的 [attachment.Error]。
func requireCode(t *testing.T, err error, want attachment.Code) {
	t.Helper()
	var typed *attachment.Error
	if !errors.As(err, &typed) {
		t.Fatalf("要一个 attachment.Error，拿到 %v", err)
	}
	if typed.Code != want {
		t.Fatalf("码要 %s，拿到 %s（%s）", want, typed.Code, typed.Message)
	}
}

func TestSaveImageReportsTheStoredFactsAndLandsOnTheContentAddressedKey(t *testing.T) {
	store, medium := newStore(t)
	data := pngBytes(t, 4, 3)

	ref, err := store.SaveImage(context.Background(), attachment.ImageInput{
		Data: data, MediaType: attachment.MediaTypePNG, Name: "shot.png",
	})
	if err != nil {
		t.Fatalf("提交：%v", err)
	}

	sum := sha256.Sum256(data)
	if want := attachment.ID(idPrefix + hex.EncodeToString(sum[:])); ref.ID != want {
		t.Fatalf("标识要 %s，拿到 %s", want, ref.ID)
	}
	if ref.MediaType != attachment.MediaTypePNG || ref.Bytes != len(data) ||
		ref.Width != 4 || ref.Height != 3 || ref.Name != "shot.png" {
		t.Fatalf("引用记错了：%+v", ref)
	}
	if ref.OriginalDimensions != nil {
		t.Fatalf("本包不缩放，OriginalDimensions 该是 nil，拿到 %+v", ref.OriginalDimensions)
	}

	keys := medium.Keys()
	if len(keys) != 1 || keys[0] != objectKey(data) {
		t.Fatalf("介质上要且只要 %s，拿到 %v", objectKey(data), keys)
	}
}

func TestSaveImageDeduplicatesIdenticalBytesWithoutWritingAgain(t *testing.T) {
	store, medium := newStore(t)
	data := pngBytes(t, 4, 3)
	input := attachment.ImageInput{Data: data, MediaType: attachment.MediaTypePNG}

	first, err := store.SaveImage(context.Background(), input)
	if err != nil {
		t.Fatalf("第一次提交：%v", err)
	}
	target, err := medium.Resolve(context.Background(), string(objectKey(data)), "")
	if err != nil {
		t.Fatalf("解析对象：%v", err)
	}
	before, _, err := medium.Stat(context.Background(), target)
	if err != nil {
		t.Fatalf("看第一次写下的版本：%v", err)
	}

	second, err := store.SaveImage(context.Background(), input)
	if err != nil {
		t.Fatalf("第二次提交：%v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("同一批字节要同一个标识，拿到 %s 和 %s", first.ID, second.ID)
	}
	if keys := medium.Keys(); len(keys) != 1 {
		t.Fatalf("介质上要只有一个对象，拿到 %v", keys)
	}

	after, _, err := medium.Stat(context.Background(), target)
	if err != nil {
		t.Fatalf("看第二次之后的版本：%v", err)
	}
	// 版本变了就说明第二次真的写了一遍——那正是 CreateIfAbsent 该挡住的事。
	if after.Version != before.Version {
		t.Fatalf("第二次不该再写一遍，版本从 %s 变成了 %s", before.Version, after.Version)
	}
}

func TestSaveImageReportsCorruptWhenTheExistingObjectIsNotTheSameBytes(t *testing.T) {
	store, medium := newStore(t)
	data := pngBytes(t, 4, 3)

	// 在这个键上先种一份长度相同、内容不同的字节：一个撞了键却对不上摘要的对象，
	// 只可能是存储坏了或者被人动过。
	forged := bytes.Repeat([]byte{0x7f}, len(data))
	medium.SeedBytes(objectKey(data), forged)

	_, err := store.SaveImage(context.Background(), attachment.ImageInput{
		Data: data, MediaType: attachment.MediaTypePNG,
	})
	requireCode(t, err, attachment.CodeAttachmentCorrupt)
}

func TestReadImageRoundTripsEveryFormatThisPackageDecodes(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		mediaType attachment.MediaType
		data      func(*testing.T) []byte
	}{
		{"png", attachment.MediaTypePNG, func(t *testing.T) []byte { return pngBytes(t, 4, 3) }},
		{"jpeg", attachment.MediaTypeJPEG, func(t *testing.T) []byte { return jpegBytes(t, 4, 3) }},
		{"gif", attachment.MediaTypeGIF, func(t *testing.T) []byte { return gifBytes(t, 4, 3) }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store, _ := newStore(t)
			data := testCase.data(t)

			ref, err := store.SaveImage(context.Background(), attachment.ImageInput{
				Data: data, MediaType: testCase.mediaType,
			})
			if err != nil {
				t.Fatalf("提交：%v", err)
			}
			stored, err := store.ReadImage(context.Background(), ref)
			if err != nil {
				t.Fatalf("读回：%v", err)
			}
			if !bytes.Equal(stored.Data, data) {
				t.Fatalf("读回来的不是存进去的那些字节")
			}
			if stored.Ref != ref {
				t.Fatalf("引用要原样交回，拿到 %+v", stored.Ref)
			}
		})
	}
}

func TestReadImageDetectsASingleFlippedBit(t *testing.T) {
	store, medium := newStore(t)
	data := pngBytes(t, 4, 3)

	ref, err := store.SaveImage(context.Background(), attachment.ImageInput{
		Data: data, MediaType: attachment.MediaTypePNG,
	})
	if err != nil {
		t.Fatalf("提交：%v", err)
	}

	tampered := bytes.Clone(data)
	tampered[len(tampered)/2] ^= 0x01
	medium.SeedBytes(objectKey(data), tampered)

	_, err = store.ReadImage(context.Background(), ref)
	requireCode(t, err, attachment.CodeAttachmentCorrupt)
}

func TestReadImageDetectsAReferenceThatDisagreesWithTheBytes(t *testing.T) {
	store, _ := newStore(t)
	data := pngBytes(t, 4, 3)

	ref, err := store.SaveImage(context.Background(), attachment.ImageInput{
		Data: data, MediaType: attachment.MediaTypePNG,
	})
	if err != nil {
		t.Fatalf("提交：%v", err)
	}

	// 摘要仍然对得上，对不上的是引用记的那几个数——同样是坏的。
	ref.Width++
	_, err = store.ReadImage(context.Background(), ref)
	requireCode(t, err, attachment.CodeAttachmentCorrupt)
}

func TestReadImageReportsAMissingObject(t *testing.T) {
	store, _ := newStore(t)
	data := pngBytes(t, 4, 3)
	sum := sha256.Sum256(data)

	_, err := store.ReadImage(context.Background(), attachment.ImageRef{
		ID:        attachment.ID(idPrefix + hex.EncodeToString(sum[:])),
		MediaType: attachment.MediaTypePNG,
		Bytes:     len(data),
		Width:     4,
		Height:    3,
	})
	requireCode(t, err, attachment.CodeAttachmentNotFound)
}

func TestReadImageReportsCorruptWhenTheObjectIsLongerThanTheReference(t *testing.T) {
	store, medium := newStore(t)
	data := pngBytes(t, 4, 3)

	ref, err := store.SaveImage(context.Background(), attachment.ImageInput{
		Data: data, MediaType: attachment.MediaTypePNG,
	})
	if err != nil {
		t.Fatalf("提交：%v", err)
	}
	medium.SeedBytes(objectKey(data), append(bytes.Clone(data), 0x00))

	_, err = store.ReadImage(context.Background(), ref)
	requireCode(t, err, attachment.CodeAttachmentCorrupt)
}

func TestReadImageRejectsAnIdentifierItDidNotIssue(t *testing.T) {
	hexSum := strings.Repeat("a", 64)
	for _, testCase := range []struct {
		name string
		id   string
	}{
		{"空", ""},
		{"没有前缀", hexSum},
		{"只有前缀", idPrefix},
		{"短了一位", idPrefix + strings.Repeat("a", 63)},
		{"长了一位", idPrefix + strings.Repeat("a", 65)},
		{"大写十六进制", idPrefix + strings.ToUpper(hexSum)},
		{"非十六进制字符", idPrefix + strings.Repeat("a", 63) + "g"},
		{"一条路径", idPrefix + "../../etc/passwd"},
		{"带分隔符", idPrefix + strings.Repeat("a", 32) + "/" + strings.Repeat("a", 31)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store, _ := newStore(t)
			_, err := store.ReadImage(context.Background(), attachment.ImageRef{
				ID: attachment.ID(testCase.id), Bytes: 16,
			})
			requireCode(t, err, attachment.CodeInvalidAttachmentRef)
		})
	}
}

func TestReadImageRejectsANonPositiveByteCount(t *testing.T) {
	store, _ := newStore(t)
	_, err := store.ReadImage(context.Background(), attachment.ImageRef{
		ID: attachment.ID(idPrefix + strings.Repeat("a", 64)), Bytes: 0,
	})
	requireCode(t, err, attachment.CodeInvalidAttachmentRef)
}

func TestValidateImageReportsTheSameCodeAsAttachmentLocal(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		tune  func(*Config)
		input func(*testing.T) attachment.ImageInput
		want  attachment.Code
	}{
		{
			name: "字节数超限",
			tune: func(config *Config) { config.Limits.MaxImageBytes = 10 },
			input: func(t *testing.T) attachment.ImageInput {
				return attachment.ImageInput{Data: pngBytes(t, 4, 3), MediaType: attachment.MediaTypePNG}
			},
			want: attachment.CodeImageTooLarge,
		},
		{
			name: "空",
			input: func(*testing.T) attachment.ImageInput {
				return attachment.ImageInput{MediaType: attachment.MediaTypePNG}
			},
			want: attachment.CodeInvalidImage,
		},
		{
			name: "不是一张图",
			input: func(*testing.T) attachment.ImageInput {
				return attachment.ImageInput{
					Data: []byte("这不是图片，是一段文字"), MediaType: attachment.MediaTypePNG,
				}
			},
			want: attachment.CodeInvalidImage,
		},
		{
			name: "文件头好、数据段截断",
			input: func(t *testing.T) attachment.ImageInput {
				return attachment.ImageInput{
					Data: pngBytes(t, 4, 3)[:40], MediaType: attachment.MediaTypePNG,
				}
			},
			want: attachment.CodeInvalidImage,
		},
		{
			name: "像素数超限",
			tune: func(config *Config) { config.Limits.MaxImagePixels = 4 },
			input: func(t *testing.T) attachment.ImageInput {
				return attachment.ImageInput{Data: pngBytes(t, 4, 3), MediaType: attachment.MediaTypePNG}
			},
			want: attachment.CodeImageTooManyPixels,
		},
		{
			name: "边长超限",
			tune: func(config *Config) { config.Limits.MaxImageDimension = 3 },
			input: func(t *testing.T) attachment.ImageInput {
				return attachment.ImageInput{Data: pngBytes(t, 4, 3), MediaType: attachment.MediaTypePNG}
			},
			want: attachment.CodeImageDimensionTooLarge,
		},
		{
			name: "声称的类型对不上",
			input: func(t *testing.T) attachment.ImageInput {
				return attachment.ImageInput{Data: pngBytes(t, 4, 3), MediaType: attachment.MediaTypeJPEG}
			},
			want: attachment.CodeImageTypeMismatch,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			tune := []func(*Config){}
			if testCase.tune != nil {
				tune = append(tune, testCase.tune)
			}
			store, medium := newStore(t, tune...)
			input := testCase.input(t)

			requireCode(t, store.ValidateImage(context.Background(), input), testCase.want)

			// 同一条输入走提交也必须报同一个码，而且**一个字节都不落地**：
			// 一批图里有一张不合格时，前面那几张不该在存储里留下没人认领的对象。
			_, err := store.SaveImage(context.Background(), input)
			requireCode(t, err, testCase.want)
			if keys := medium.Keys(); len(keys) != 0 {
				t.Fatalf("被拒的图不该落地，介质上却有 %v", keys)
			}
		})
	}
}

func TestProbeRecognizesWebP(t *testing.T) {
	found, err := probe(webpHeader(6, 5))
	if err != nil {
		t.Fatalf("认 WebP 文件头：%v", err)
	}
	if found.mediaType != attachment.MediaTypeWebP || found.width != 6 || found.height != 5 {
		t.Fatalf("认错了：%+v", found)
	}
}

func TestImageLimitsAreDefaultedAndHandedOutAsACopy(t *testing.T) {
	store, _ := newStore(t)
	limits := store.ImageLimits()

	if limits.MaxImageBytes != defaultMaxImageBytes ||
		limits.MaxImagesPerMessage != defaultMaxImagesPerMessage ||
		limits.MaxMessageImageBytes != defaultMaxMessageImageBytes ||
		limits.MaxImagePixels != defaultMaxImagePixels ||
		limits.MaxImageDimension != defaultMaxImageDimension {
		t.Fatalf("默认限额没填上：%+v", limits)
	}
	if len(limits.MediaTypes) != len(decodable()) {
		t.Fatalf("默认媒体类型要那四种，拿到 %v", limits.MediaTypes)
	}

	limits.MediaTypes[0] = "image/tiff"
	if store.ImageLimits().MediaTypes[0] == "image/tiff" {
		t.Fatalf("交出去的限额被调用方改动了")
	}
}

func TestAnEmptyMediaTypeListAcceptsNothing(t *testing.T) {
	store, _ := newStore(t, func(config *Config) {
		config.Limits.MediaTypes = []attachment.MediaType{}
	})
	err := attachment.ValidateImageBatch(store.ImageLimits(), []attachment.ImageInput{
		{Data: pngBytes(t, 2, 2), MediaType: attachment.MediaTypePNG},
	})
	requireCode(t, err, attachment.CodeUnsupportedImageType)
}

func TestNewRefusesAnAssemblyItCannotHonour(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		config Config
	}{
		{"没有文件系统", Config{Root: root}},
		{"根是空的", Config{FS: fstest.New()}},
		{"根只有斜杠", Config{FS: fstest.New(), Root: "///"}},
		{"限额是负数", Config{
			FS: fstest.New(), Root: root,
			Limits: attachment.ImageLimits{MaxImagePixels: -1},
		}},
		{"解不出的媒体类型", Config{
			FS: fstest.New(), Root: root,
			Limits: attachment.ImageLimits{MediaTypes: []attachment.MediaType{"image/tiff"}},
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := New(testCase.config); err == nil {
				t.Fatalf("这份装配该被拒掉")
			}
		})
	}
}

func TestRootKeepsItsShapeWhateverTrailingSlashesItArrivesWith(t *testing.T) {
	medium := fstest.New()
	store, err := New(Config{FS: medium, Root: root + "//"})
	if err != nil {
		t.Fatalf("装配存储：%v", err)
	}
	data := pngBytes(t, 2, 2)
	if _, err := store.SaveImage(context.Background(), attachment.ImageInput{
		Data: data, MediaType: attachment.MediaTypePNG,
	}); err != nil {
		t.Fatalf("提交：%v", err)
	}
	if keys := medium.Keys(); len(keys) != 1 || keys[0] != objectKey(data) {
		t.Fatalf("键要 %s，拿到 %v", objectKey(data), keys)
	}
}

func TestDisplayNameStripsWhatMustNotReachTheSessionLog(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		value string
		want  string
	}{
		{"没有名字", "", ""},
		{"就是个叶名", "shot.png", "shot.png"},
		{"POSIX 路径", "/home/操作者/图/shot.png", "shot.png"},
		{"Windows 路径", `C:\Users\操作者\Pictures\shot.png`, "shot.png"},
		{"混着两种分隔符", `/tmp/a\b/c\shot.png`, "shot.png"},
		{"控制字符", "sh\x00o\x1ft\x7f.png", "shot.png"},
		{"前后空白", "  shot.png\t", "shot.png"},
		{"剥空了", "/tmp/", ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := displayName(testCase.value); got != testCase.want {
				t.Fatalf("要 %q，拿到 %q", testCase.want, got)
			}
		})
	}
}

func TestDisplayNameTruncatesOnARuneBoundary(t *testing.T) {
	// 每个汉字三字节：85 个是 255 字节，正好到上限；86 个就要在第 256 字节处截，
	// 而那一刀落在一个码点中间。
	got := displayName(strings.Repeat("图", 86))
	if got != strings.Repeat("图", 85) {
		t.Fatalf("截断落错了地方，拿到 %d 字节", len(got))
	}
}

func TestSaveImagesRefusesTheWholeBatchBeforeWritingAnything(t *testing.T) {
	store, medium := newStore(t)
	good := pngBytes(t, 4, 3)

	_, err := attachment.SaveImages(context.Background(), store, []attachment.ImageInput{
		{Data: good, MediaType: attachment.MediaTypePNG},
		{Data: []byte("这不是一张图"), MediaType: attachment.MediaTypePNG},
	})
	requireCode(t, err, attachment.CodeInvalidImage)
	if keys := medium.Keys(); len(keys) != 0 {
		t.Fatalf("整批被拒时不该有对象落地，介质上却有 %v", keys)
	}
}

func TestThisStoreCannotDeriveRequestImages(t *testing.T) {
	store, _ := newStore(t)
	_, err := attachment.ReadImageRequest(
		context.Background(), store, attachment.ImageRef{}, attachment.RequestPolicy{},
	)
	requireCode(t, err, attachment.CodeAttachmentProjectionUnsupported)
}

func TestCancellationIsHonouredOnEveryMethodThatTakesIt(t *testing.T) {
	store, _ := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	input := attachment.ImageInput{Data: pngBytes(t, 2, 2), MediaType: attachment.MediaTypePNG}
	if err := store.ValidateImage(ctx, input); !errors.Is(err, context.Canceled) {
		t.Fatalf("校验要认取消，拿到 %v", err)
	}
	if _, err := store.SaveImage(ctx, input); !errors.Is(err, context.Canceled) {
		t.Fatalf("提交要认取消，拿到 %v", err)
	}
	if _, err := store.ReadImage(ctx, attachment.ImageRef{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("读回要认取消，拿到 %v", err)
	}
}
