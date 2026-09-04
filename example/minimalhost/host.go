// 本文件的作用：从一个**第三个包**里把最小闭环拼出来，让 harness 的导出面有一处
// 包外的编译期证据。
//
// 新增: DSH 没有对应物，理由见包文档。

package minimalhost

import (
	"context"

	"github.com/snight1983/ds-harness-go/harness"
)

// Options 是装这份最小闭环要决定的那几样，逐字就是 [harness.Options]。
//
// 用类型别名而不是重新声明一份：宿主从本包读到的字段名和它照着 harness 写时**必须**
// 是同一套，否则这份样例示范的就不是那个包。
type Options = harness.Options

// Host 是拼好之后手上握着的那几样东西，逐字就是 [harness.Harness]。
type Host = harness.Harness

// Assemble 按 docs/embedding.md 的顺序拼出一份最小闭环，并交出拆除函数。
//
// 它只是 [harness.New]。这一层转发不加任何东西，它的全部作用是证明**一个既不在
// harness 里、也不在那几个运行期包里的第三方包**，只靠导出的构造函数、导出的选项
// 字段和导出的接口，就能把这套组件拼起来——包内测试证明不了这件事。
func Assemble(ctx context.Context, options Options) (*Host, func(context.Context) error, error) {
	return harness.New(ctx, options)
}
