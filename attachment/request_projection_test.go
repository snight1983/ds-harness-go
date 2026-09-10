// 本文件的作用：把请求图的投影几何钉住。
//
// # 这些测试防的是什么错
//
//   - **小图被放大**。预算比图还大时必须原样返回，放大既不省字节也不增信息，
//     而「尺寸没变」正是派生那条快路的判据——一旦这里放大了，每张小图都要重编一遍。
//   - **四舍五入把结果顶出预算**。按比例缩完再对短边取整，宽乘高可能反过来比预算多，
//     那个退格循环就是为这一下准备的。少了它，一条按预算收费的路由会收到超预算的图。
//   - **横竖两向不对称**。同一张图转过来，投影出来的两条边该只是对调。

package attachment_test

import (
	"testing"

	"github.com/snight1983/ds-harness-go/attachment"
)

func TestRequestImageDimensions(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name                  string
		width, height, budget int
		wantWidth, wantHeight int
	}{
		{name: "预算够就原样", width: 100, height: 50, budget: 10_000, wantWidth: 100, wantHeight: 50},
		{name: "正好用满也原样", width: 100, height: 50, budget: 5_000, wantWidth: 100, wantHeight: 50},
		{name: "横图按长边缩", width: 4000, height: 3000, budget: 2048 * 2048, wantWidth: 2364, wantHeight: 1773},
		{name: "竖图对调", width: 3000, height: 4000, budget: 2048 * 2048, wantWidth: 1773, wantHeight: 2364},
		{name: "极扁的图要退到预算里", width: 100, height: 1, budget: 10, wantWidth: 10, wantHeight: 1},
		{name: "短边不会退到 0", width: 1000, height: 3, budget: 4, wantWidth: 4, wantHeight: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			width, height := attachment.RequestImageDimensions(testCase.width, testCase.height, testCase.budget)
			if width != testCase.wantWidth || height != testCase.wantHeight {
				t.Fatalf("要 %d×%d，拿到 %d×%d", testCase.wantWidth, testCase.wantHeight, width, height)
			}
			if width*height > testCase.budget {
				t.Fatalf("%d×%d 超出了 %d 像素的预算", width, height, testCase.budget)
			}
		})
	}
}
