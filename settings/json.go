// 本文件的作用：这条接缝在「原始 JSON 形状数据」上做的四件事——脱钩、分层、按路径改、判形状。
//
// 源: packages/settings/settings/src/index.ts:185-312
//
// 这四件事都不认识 Go 的类型 T。它们跑在解码之前：文档、base、用户段三层都是
// 后端存下来或者调用方递进来的原始数据，要先叠好、算完，才谈得上解码成某个具体类型。

package settings

import (
	"encoding/json"
	"fmt"
)

// cloneJSONShaped 把一次写入的输入**脱钩并校验**成一份干净的 JSON 形状数据。
//
// 源: packages/settings/settings/src/index.ts:241-288
//
// 两件事一起做，缺一不可：
//
//  1. **脱钩。** 队列前端读到的必须是调用时刻的快照。直接把调用方的 map 排进队列的话，
//     它在排队期间还能接着改，而写下去的是改到一半的样子。
//  2. **校验。** 只有 JSON 存得下的数据才能进后端文档。存不下的东西写进去之后，
//     下一次重新读回来会变成另一个值，而中间没有任何一步报错。
//
// 新增: DSH 手写了一遍递归遍历（isPlainObject / describeRejected / visiting 环检测
// 三件套六十来行），因为 JS 的 structuredClone 会放 Date、Map、BigInt、环进来。
// Go 里这一整段是白送的：json.Marshal 遇到 chan、func、环、NaN、±Inf 直接报错，
// 再 Unmarshal 回来就是一份彻底脱钩的副本。多花一次序列化，换掉六十行只可能写错的代码。
//
// 顺带把整份数据**归一**成 json.Unmarshal 的形状（数一律 float64，对象一律
// map[string]any）。这一步是后面所有比较的前提：调用方手写的 1 和从后端读回来的 1
// 在 Go 里是 int 和 float64 两个不同的动态类型，不归一的话 [DeepEqualJSON]
// 会把一次「其实没变」判成变了。
//
// 代价写在这里：超过 2^53 的整数在这一步会丢精度。这是 JSON 本身的性质，
// DSH 侧同样如此（JS 的数就是 float64）——设置里放大整数 id 的调用方要自己转成字符串。
//
// 新增: DSH 会**跳过**值为 undefined 的对象条目（稀疏补丁语义：不提的键不动下层）。
// Go 里没有 undefined，一个键要么在 map 里要么不在；在 map 里而值为 nil 就是 JSON null，
// 那是一个**有意义**的值，会盖掉下层。不提就是不提，nil 就是 null，没有第三种。
func cloneJSONShaped(verb string, ns Namespace, input map[string]any) (map[string]any, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("%w：%s %q 时 %v", ErrNotJSON, verb, string(ns), err)
	}
	var detached map[string]any
	// 这一条走不到，所以没有用例覆盖：encoded 是上一行刚从一个 map[string]any 编出来的，
	// 只可能是一个 JSON 对象或者 "null"，两者都解得回 map[string]any。
	// 仍然留着，是因为忽略一个 error 比留一条死分支更糟——它拦的是
	// 「编出去和解回来这两侧哪天不再对称」，而那件事一旦发生必须当场炸而不是静默降级。
	if err := json.Unmarshal(encoded, &detached); err != nil {
		return nil, fmt.Errorf("%w：%s %q 时 %v", ErrNotJSON, verb, string(ns), err)
	}
	if detached == nil {
		// 输入是一个 nil map。它编码成 "null"，解回来还是 nil——
		// 而后面所有环节都假定段是一个真 map，所以在这里补成空段。
		detached = map[string]any{}
	}
	return detached, nil
}

// mergeLayers 把 over 叠到 under 上。
//
// 源: packages/settings/settings/src/index.ts:290-305
//
// 规则只有一条：**两边都是对象才递归合并，其余一律整个替换**。
// 数组也在「其余」里——数组是有序整体，按下标合并会造出一个两边都没有的第三种排列。
//
// 上层的 nil 表示 JSON null，它会盖掉下层。「这一层根本不存在」由调用方传一个
// 类型为 map[string]any 的 nil 来表达：nil map 断言成功、遍历零次，结果就是 under 的副本。
func mergeLayers(under, over any) any {
	underObject, underIsObject := under.(map[string]any)
	overObject, overIsObject := over.(map[string]any)
	if !underIsObject || !overIsObject {
		return over
	}
	merged := make(map[string]any, len(underObject)+len(overObject))
	for key, value := range underObject {
		merged[key] = value
	}
	for key, value := range overObject {
		if existing, exists := merged[key]; exists {
			merged[key] = mergeLayers(existing, value)
			continue
		}
		merged[key] = value
	}
	return merged
}

// mergeSections 是 [mergeLayers] 在「一整段」这个粒度上的入口，返回类型收窄成段本身。
//
// 断言必然成立，所以不必兜底：两个入参的静态类型都是 map[string]any，
// [mergeLayers] 里那两次类型断言因此都成立（哪怕值是 nil map，断言照样成立），
// 于是它一定走合并那一支，返回的是 make 出来的非 nil map。
func mergeSections(under, over map[string]any) map[string]any {
	merged, _ := mergeLayers(under, over).(map[string]any)
	return merged
}

// PathOpKind 是一次路径编辑的动作。
//
// 源: packages/settings/settings/src/index.ts:200-202
type PathOpKind string

const (
	// PathOpSet 在这个路径上写一个值，路径上缺的中间层会被建出来。
	PathOpSet PathOpKind = "set"
	// PathOpUnset 把这个路径上的键摘掉；路径本来就不通时是空操作。
	PathOpUnset PathOpKind = "unset"
)

// PathOp 是对一个命名空间用户段的一次**按路径**编辑。
//
// 源: packages/settings/settings/src/index.ts:192-202
//
// # 它为什么存在
//
// 给手上只有**残缺视图**的调用方用。配置界面读到的是 [Provider.Describe] 的脱敏结果，
// 那份结果按定义就不含 `settings:"secret"` 字段。这样的调用方要改一个字段时，
// 只能指名道姓地改那一个——拿脱敏文档重建一次整段 [Provider.Replace] 下去，
// 会把它从来没收到过的那些密钥统统删掉，而全程不报任何错。
//
// 新增: DSH 那边是判别联合（`{op:'set',path,value} | {op:'unset',path}`）。
// Go 没有和类型，做成带 Kind 的结构体是自然写法；代价是 unset 时 Value 字段仍然在，
// 此时它被忽略。
type PathOp struct {
	// Kind 是这次编辑的动作。
	Kind PathOpKind
	// Path 是从段根出发的路径。**空路径指的是段本身**。
	Path []string
	// Value 是 [PathOpSet] 要写下去的值；[PathOpUnset] 时忽略。
	Value any
}

// applyPathOp 把一次路径编辑作用在一份已脱钩的段上，返回新的段。
//
// 源: packages/settings/settings/src/index.ts:204-228
//
// 输入的段不被改动：每一层都复制一份再改，这样一串 op 里前一个失败不会留下半成品。
func applyPathOp(section map[string]any, op PathOp) (map[string]any, error) {
	if len(op.Path) == 0 {
		// 空路径指的是段本身。
		if op.Kind == PathOpUnset {
			return map[string]any{}, nil
		}
		replacement, isObject := op.Value.(map[string]any)
		if !isObject {
			return nil, fmt.Errorf("%w：按路径改写段根时值必须是一个键值对象", ErrMalformedSection)
		}
		return shallowCopy(replacement), nil
	}

	head, rest := op.Path[0], op.Path[1:]
	if len(rest) == 0 {
		next := shallowCopy(section)
		if op.Kind == PathOpSet {
			next[head] = op.Value
			return next, nil
		}
		delete(next, head)
		return next, nil
	}

	child, childIsObject := section[head].(map[string]any)
	if !childIsObject {
		// 路径不通：unset 的诉求已经满足了；set 要自己把中间层建出来。
		if op.Kind == PathOpUnset {
			return section, nil
		}
		child = map[string]any{}
	}
	grown, err := applyPathOp(child, PathOp{Kind: op.Kind, Path: rest, Value: op.Value})
	// 这一条走不到，所以没有用例覆盖：本函数唯一的出错点是「空路径 + 值不是对象」，
	// 而走到这里说明 rest 非空，递归下去的每一层路径也都非空。
	// 仍然接住，是因为忽略一个 error 比留一条死分支更糟——
	// 哪天上面那个分支多出第二个出错理由，这里不接就成了静默丢错。
	if err != nil {
		return nil, err
	}
	next := shallowCopy(section)
	next[head] = grown
	return next, nil
}

// shallowCopy 复制一层 map。段是一层层复制的，见 [applyPathOp]。
func shallowCopy(source map[string]any) map[string]any {
	copied := make(map[string]any, len(source))
	for key, value := range source {
		copied[key] = value
	}
	return copied
}
