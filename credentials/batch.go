// 本文件的作用：配置界面那条只读路径——一次问一批引用，带着它自己的语法关和扇出上限。
//
// 源: packages/api/settings-controller/src/credentials.ts

package credentials

import (
	"context"
	"errors"
	"fmt"
)

// MaxDescribeRefs 是一次 [DescribeRefs] 能问的引用数上限。
//
// 源: packages/api/settings-controller/src/credentials.ts:20（MAX_DESCRIBE_REFS）
//
// 一个配置界面问的是它自己那几行点名的引用，所以这个数远在任何真实界面之上；
// 它挡的是另一件事——一次通过了鉴权的请求，不该能让提供方开始一段没有上界的活。
const MaxDescribeRefs = 64

// ErrTooManyRefs 表示一次批量描述问的引用数超过了 [MaxDescribeRefs]。
//
// 源: packages/api/settings-controller/src/credentials.ts:24
var ErrTooManyRefs = errors.New("credentials: 一次批量描述问的引用太多")

// DescribeRefs 一次描述若干引用，给配置界面用——**不含任何值**。
//
// 源: packages/api/settings-controller/src/credentials.ts:82-90（describe）
//
// 做成一次而不是让调用方自己循环，是因为界面上那些行要一起落定：分成多次问，
// 先回来的行会先渲染，后回来的行在旁边空着，而它们描述的是同一个时刻的同一份配置。
//
// 三件事在问提供方**之前**做完，一件不过就一个引用都不问：
//
//   - 名字数不超过 [MaxDescribeRefs]，否则报 [ErrTooManyRefs]；
//   - 每个名字都过 [IsRefName]，有一个不过就整次拒绝，报 [ErrInvalidRef]；
//   - 重名折成一个。
//
// 语法这一关整次拒绝而不是跳过坏名字，是因为坏名字是调用方那边的错误。
// 跳过它会让界面上少一行，而少的那一行和「这个引用没配置」在界面上长得一样。
//
// 新增: 重名折叠是 Go 这边多出来的一步。DSH 那边把结果收进一个对象字面量，
// 重名自然被后写的盖掉，但提供方已经被问了两次。这里先折叠，问的次数就是
// 结果的条数。
//
// 新增: DSH 那边这个方法先要从 cordis 容器里取提供方，取不到报 `gateway/internal`。
// Go 没有那个容器，提供方是形参，装配方填不上是编译期或者装配期的事，
// 不是这里的一条运行期分支。
//
// 新增: DSH 那边每条结果还要过一次 projectCredentialInfo，逐字段照写一遍 [Info]，
// 因为 JS 的对象可以带着提供方自己加的可枚举属性一路序列化出去。[Info] 是结构体，
// 实现方加不进字段，那一步在 Go 里没有对应物。
//
// 提供方在任何一个引用上失败，整次就失败：一份缺了一行的描述，界面分不出
// 缺的那行是「没配置」还是「没问出来」。
func DescribeRefs(ctx context.Context, provider Provider, names []string) (map[Ref]Info, error) {
	if len(names) > MaxDescribeRefs {
		return nil, fmt.Errorf("%w：问了 %d 个，上限 %d", ErrTooManyRefs, len(names), MaxDescribeRefs)
	}
	refs := make([]Ref, 0, len(names))
	seen := make(map[Ref]struct{}, len(names))
	for _, name := range names {
		if !IsRefName(name) {
			return nil, fmt.Errorf("%w：%q 必须匹配 %s", ErrInvalidRef, name, refPattern)
		}
		ref := Ref(name)
		if _, duplicate := seen[ref]; duplicate {
			continue
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}

	described := make(map[Ref]Info, len(refs))
	for _, ref := range refs {
		info, err := provider.Describe(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("描述凭据引用 %q 失败：%w", ref, err)
		}
		described[ref] = info
	}
	return described, nil
}
