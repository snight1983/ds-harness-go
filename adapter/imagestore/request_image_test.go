// 本文件的作用：把派生请求图那条路钉住。
//
// # 这些测试防的是什么错
//
//   - **本来在预算里的图被重编了一遍**。原尺寸加原字节都合规时必须原样交出去，
//     重编只会掉画质，而绝大多数商品图和截图走的都是这一条。
//   - **超预算的图被原样送出去**。那边的模型路由会当场拒收，或者按超出的尺寸计费。
//   - **alpha 通道被 JPEG 吃掉**。一张抠好的商品图会平白多出一块黑底。
//   - **同样的输入给出不同的字节或不同的键**。变体标识是缓存和上传索引的键，
//     它一旦不确定，两次请求就会各自为政。
//   - **缓存形同虚设**。少了它，历史里每张图在每一轮都要重编一次。

package imagestore

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/attachment"
	"github.com/snight1983/ds-harness-go/fs"
)

// generous 是一份两项都够用的策略，用来走那条快路。
var generous = attachment.RequestPolicy{MaxPixels: 2048 * 2048, MaxBytes: 1 << 20}

// alphaPNGBytes 造一张真的带 alpha 平面的 PNG：右半边是半透明的。
func alphaPNGBytes(t *testing.T, width int, height int) []byte {
	t.Helper()
	raster := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			alpha := uint8(0xff)
			if x*2 >= width {
				alpha = 0x40
			}
			raster.Set(x, y, color.NRGBA{R: uint8(x * 7), G: uint8(y * 11), B: 0x90, A: alpha})
		}
	}
	buffer := &bytes.Buffer{}
	if err := png.Encode(buffer, raster); err != nil {
		t.Fatalf("编码带 alpha 的栅格：%v", err)
	}
	return buffer.Bytes()
}

// saved 把一份字节存进去，交回它的引用。
func saved(t *testing.T, store *Store, data []byte, mediaType attachment.MediaType) attachment.ImageRef {
	t.Helper()
	ref, err := store.SaveImage(context.Background(), attachment.ImageInput{Data: data, MediaType: mediaType})
	if err != nil {
		t.Fatalf("提交：%v", err)
	}
	return ref
}

// requestKey 是一个变体在介质上落到的那个键。
func requestKey(store *Store, variant attachment.VariantID) fs.TargetKey {
	return fs.TargetKey(store.requestPath(variant))
}

func TestRequestImageHandsBackTheSourceBytesWhenBothBudgetsAllow(t *testing.T) {
	store, medium := newStore(t)
	data := pngBytes(t, 8, 6)
	ref := saved(t, store, data, attachment.MediaTypePNG)

	got, err := attachment.ReadImageRequest(context.Background(), store, ref, generous)
	if err != nil {
		t.Fatalf("派生：%v", err)
	}
	if !bytes.Equal(got.Data, data) {
		t.Fatalf("预算够时该原样交出存储里那些字节，拿到 %d 字节", len(got.Data))
	}
	if got.MediaType != attachment.MediaTypePNG || got.Width != 8 || got.Height != 6 ||
		got.Bytes != len(data) || got.HasAlpha {
		t.Fatalf("请求版本记错了：%+v", got)
	}
	if got.Depth != attachment.DepthUChar || got.Space != attachment.SpaceSRGB {
		t.Fatalf("位深和色彩空间要 %s/%s，拿到 %s/%s",
			attachment.DepthUChar, attachment.SpaceSRGB, got.Depth, got.Space)
	}
	if got.Attachment != ref {
		t.Fatalf("派生自哪一张要原样带回，拿到 %+v", got.Attachment)
	}
	// 走快路的那一份就是对象树里的那一份，再存一遍是白占地方。
	if keys := medium.Keys(); len(keys) != 1 || keys[0] != objectKey(data) {
		t.Fatalf("快路不该写缓存，介质上有 %v", keys)
	}
}

func TestRequestImageShrinksAndReEncodesWhenThePixelBudgetIsSmaller(t *testing.T) {
	store, medium := newStore(t)
	data := pngBytes(t, 40, 30)
	ref := saved(t, store, data, attachment.MediaTypePNG)

	policy := attachment.RequestPolicy{MaxPixels: 300, MaxBytes: 1 << 20}
	got, err := attachment.ReadImageRequest(context.Background(), store, ref, policy)
	if err != nil {
		t.Fatalf("派生：%v", err)
	}
	if got.Width*got.Height > policy.MaxPixels {
		t.Fatalf("投影完还是 %d×%d，超出了 %d 像素的预算", got.Width, got.Height, policy.MaxPixels)
	}
	// 源是一张不带 alpha 的图，所以走的是 JPEG 那一档。
	if got.MediaType != attachment.MediaTypeJPEG {
		t.Fatalf("不带 alpha 的图该重编成 JPEG，拿到 %s", got.MediaType)
	}
	if got.Bytes != len(got.Data) || bytes.Equal(got.Data, data) {
		t.Fatalf("请求版本该是另一份字节：%d 字节", got.Bytes)
	}
	found, err := probe(got.Data)
	if err != nil {
		t.Fatalf("重编出来的字节该认得出：%v", err)
	}
	if found.width != got.Width || found.height != got.Height {
		t.Fatalf("报的尺寸 %d×%d 和字节里的 %d×%d 对不上",
			got.Width, got.Height, found.width, found.height)
	}

	key := requestKey(store, got.VariantID)
	if keys := medium.Keys(); len(keys) != 2 || !slicesContain(keys, key) {
		t.Fatalf("重编出来的那一份该按 %s 入缓存，介质上有 %v", key, keys)
	}
}

func TestRequestImageServesTheCachedVariantOnASecondRead(t *testing.T) {
	store, medium := newStore(t)
	ref := saved(t, store, pngBytes(t, 40, 30), attachment.MediaTypePNG)
	policy := attachment.RequestPolicy{MaxPixels: 300, MaxBytes: 1 << 20}

	first, err := attachment.ReadImageRequest(context.Background(), store, ref, policy)
	if err != nil {
		t.Fatalf("第一次派生：%v", err)
	}
	// 把缓存里那一份换成另一张认得出、尺寸又在界内的图。第二次拿到它，
	// 就证明这一次真的读了缓存，而不是又算了一遍。
	planted := pngBytes(t, first.Width, first.Height)
	medium.SeedBytes(requestKey(store, first.VariantID), planted)

	second, err := attachment.ReadImageRequest(context.Background(), store, ref, policy)
	if err != nil {
		t.Fatalf("第二次派生：%v", err)
	}
	if !bytes.Equal(second.Data, planted) {
		t.Fatalf("第二次该走缓存，拿到的却是另外 %d 字节", second.Bytes)
	}
	if second.VariantID != first.VariantID {
		t.Fatalf("同样的输入该给同一个变体标识：%s / %s", first.VariantID, second.VariantID)
	}
}

func TestRequestImageKeepsAlphaByFallingBackToPNG(t *testing.T) {
	store, _ := newStore(t)
	data := alphaPNGBytes(t, 40, 30)
	ref := saved(t, store, data, attachment.MediaTypePNG)

	got, err := attachment.ReadImageRequest(
		context.Background(), store, ref, attachment.RequestPolicy{MaxPixels: 300, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatalf("派生：%v", err)
	}
	if got.MediaType != attachment.MediaTypePNG || !got.HasAlpha {
		t.Fatalf("带 alpha 的图要留在 PNG 上，拿到 %s（hasAlpha=%v）", got.MediaType, got.HasAlpha)
	}
	if got.Width*got.Height > 300 {
		t.Fatalf("投影完还是 %d×%d，超出了预算", got.Width, got.Height)
	}
}

func TestRequestImageFallsBackToTheSmallestWhenNothingFits(t *testing.T) {
	store, medium := newStore(t)
	ref := saved(t, store, pngBytes(t, 60, 40), attachment.MediaTypePNG)

	// 一个字节的预算谁也满足不了，这时候要交出阶梯上最小的那一份，而不是报错。
	got, err := attachment.ReadImageRequest(
		context.Background(), store, ref, attachment.RequestPolicy{MaxPixels: 600, MaxBytes: 1})
	if err != nil {
		t.Fatalf("全都超预算时该交出最小的那一份，拿到错误：%v", err)
	}
	if got.Bytes == 0 {
		t.Fatalf("交出来的那一份是空的")
	}
	// 注定读不回来的字节不入缓存：读缓存那一侧唯一能自证的上限就是预算本身。
	if keys := medium.Keys(); len(keys) != 1 {
		t.Fatalf("超预算的那一份不该入缓存，介质上有 %v", keys)
	}
}

func TestRequestImageVariantIdentityCoversTheRoutePolicy(t *testing.T) {
	store, _ := newStore(t)
	ref := saved(t, store, pngBytes(t, 8, 6), attachment.MediaTypePNG)

	base, err := attachment.ReadImageRequest(context.Background(), store, ref, generous)
	if err != nil {
		t.Fatalf("派生：%v", err)
	}
	if !strings.HasPrefix(string(base.VariantID), idPrefix) {
		t.Fatalf("变体标识要长成 %s 加十六进制，拿到 %s", idPrefix, base.VariantID)
	}
	for _, other := range []attachment.RequestPolicy{
		{MaxPixels: generous.MaxPixels / 2, MaxBytes: generous.MaxBytes},
		{MaxPixels: generous.MaxPixels, MaxBytes: generous.MaxBytes / 2},
	} {
		got, err := attachment.ReadImageRequest(context.Background(), store, ref, other)
		if err != nil {
			t.Fatalf("按 %+v 派生：%v", other, err)
		}
		if got.VariantID == base.VariantID {
			t.Fatalf("换了策略 %+v 却还是同一个变体标识 %s", other, base.VariantID)
		}
	}
}

func TestRequestImageRejectsANonPositiveBudget(t *testing.T) {
	store, _ := newStore(t)
	ref := saved(t, store, pngBytes(t, 8, 6), attachment.MediaTypePNG)

	for _, policy := range []attachment.RequestPolicy{
		{MaxPixels: 0, MaxBytes: 1 << 20},
		{MaxPixels: 1024, MaxBytes: 0},
		{MaxPixels: -1, MaxBytes: -1},
	} {
		_, err := attachment.ReadImageRequest(context.Background(), store, ref, policy)
		requireCode(t, err, attachment.CodeInvalidAttachmentRef)
	}
}

func slicesContain(keys []fs.TargetKey, want fs.TargetKey) bool {
	for _, key := range keys {
		if key == want {
			return true
		}
	}
	return false
}
