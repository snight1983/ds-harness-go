// 本文件的作用：请求图的投影几何——在一个总像素预算之内，保长宽比、向内取整地
// 算出一张图该缩到多大。
//
// 源: packages/attachment/attachment/src/request-projection.ts

package attachment

import "math"

// RequestImageDimensions 在总像素预算内算出保长宽比的整数宽高。
//
// 源: packages/attachment/attachment/src/request-projection.ts:13-36
//
// 向内取整，小图不放大：预算够时原样返回，这一条正是派生请求图那条快路的判据
// （尺寸没变、字节又在预算内，就没有任何理由重新编码一遍）。
//
// 那两个循环是必需的：按比例缩完再对短边四舍五入，宽乘高可能反过来比预算多出
// 一两个像素，于是长边一格一格退，直到真的落进预算里。
//
// 新增: DSH 把它放在契约包里，由附件提供方和请求定价两侧共用；这里照放在
// attachment 而不是 adapter/imagestore，理由一样——它是纯几何，用不着编码器。
func RequestImageDimensions(width, height, maxPixels int) (int, int) {
	scale := math.Min(1, math.Sqrt(float64(maxPixels)/(float64(width)*float64(height))))
	if scale == 1 {
		return width, height
	}
	if width >= height {
		projectedWidth := max(1, int(math.Floor(float64(width)*scale)))
		projectedHeight := max(1, int(math.Round(float64(projectedWidth)*float64(height)/float64(width))))
		for projectedWidth*projectedHeight > maxPixels && projectedWidth > 1 {
			projectedWidth--
			projectedHeight = max(1, int(math.Round(float64(projectedWidth)*float64(height)/float64(width))))
		}
		return projectedWidth, projectedHeight
	}
	projectedHeight := max(1, int(math.Floor(float64(height)*scale)))
	projectedWidth := max(1, int(math.Round(float64(projectedHeight)*float64(width)/float64(height))))
	for projectedWidth*projectedHeight > maxPixels && projectedHeight > 1 {
		projectedHeight--
		projectedWidth = max(1, int(math.Round(float64(projectedHeight)*float64(width)/float64(height))))
	}
	return projectedWidth, projectedHeight
}
