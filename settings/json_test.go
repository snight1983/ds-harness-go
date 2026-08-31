// 本文件验解码之前那一层：脱钩校验、分层合并、按路径改。
//
// 源: packages/settings/settings/tests/settings.spec.ts:224-236,482-488,615-654,807-923

package settings

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

// TestCloneJSONShapedDetachesAndNormalizes 钉住脱钩和归一是同一步做完的。
//
// 源: packages/settings/settings/tests/settings.spec.ts:518-528
//
// 归一那一半尤其要钉：调用方手写的 1 是 int，从后端读回来的 1 是 float64。
// 不归一的话 [DeepEqualJSON] 会把一次「其实没变」判成变了。
func TestCloneJSONShapedDetachesAndNormalizes(t *testing.T) {
	t.Parallel()

	input := map[string]any{"n": 1, "nested": map[string]any{"list": []any{2}}}
	cloned, err := cloneJSONShaped("update", "core", input)
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}

	if _, isFloat := cloned["n"].(float64); !isFloat {
		t.Fatalf("数该归一成 float64，实际 %T", cloned["n"])
	}

	// 改输入不该影响已经拿到的那一份。
	input["n"] = 99
	input["nested"].(map[string]any)["list"] = []any{99}
	if cloned["n"].(float64) != 1 {
		t.Fatalf("顶层该脱钩，实际 %v", cloned["n"])
	}
	nested := cloned["nested"].(map[string]any)["list"].([]any)
	if nested[0].(float64) != 2 {
		t.Fatalf("嵌套也该脱钩，实际 %v", nested[0])
	}
}

// TestCloneJSONShapedRejectsWhatJSONCannotHold 逐条验 JSON 存不下的东西一律当场拒。
//
// 源: packages/settings/settings/tests/settings.spec.ts:482-488,615-646,917-923
//
// 这四种在 Go 里都由 json.Marshal 直接报错，所以本包一行遍历都不用写——
// 但正因为是白送的，更要有用例钉住：换一种序列化方式就会悄悄放它们进去。
func TestCloneJSONShapedRejectsWhatJSONCannotHold(t *testing.T) {
	t.Parallel()

	cycle := map[string]any{}
	cycle["self"] = cycle

	for name, input := range map[string]map[string]any{
		"函数":      {"fn": func() {}},
		"通道":      {"ch": make(chan int)},
		"环":       cycle,
		"NaN":     {"n": math.NaN()},
		"正无穷":     {"n": math.Inf(1)},
		"负无穷":     {"n": math.Inf(-1)},
		"复数":      {"c": complex(1, 2)},
		"嵌在深处的函数": {"a": map[string]any{"b": []any{func() {}}}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := cloneJSONShaped("update", "core", input); !errors.Is(err, ErrNotJSON) {
				t.Fatalf("该报 ErrNotJSON，实际 %v", err)
			}
		})
	}
}

// TestCloneJSONShapedTurnsANilMapIntoAnEmptySection 钉住 nil map 补成空段。
//
// nil map 编码成 "null"、解回来还是 nil，而后面所有环节都假定段是一个真 map。
// 不补的话 Replace(nil) 会在一个 nil 上写键而 panic。
func TestCloneJSONShapedTurnsANilMapIntoAnEmptySection(t *testing.T) {
	t.Parallel()

	cloned, err := cloneJSONShaped("replace", "core", nil)
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	if cloned == nil || len(cloned) != 0 {
		t.Fatalf("该是一个空段，实际 %#v", cloned)
	}
}

// TestCloneJSONShapedAcceptsOneObjectReferencedTwice 钉住共享不是环。
//
// 源: packages/settings/settings/tests/settings.spec.ts:647-654
//
// 同一个对象被引用两次是合法的 JSON 形状（展开成两份一样的子树），
// 只有真正成环才存不下。把前者也拒掉的话，一份用变量拼出来的配置会莫名其妙写不进去。
func TestCloneJSONShapedAcceptsOneObjectReferencedTwice(t *testing.T) {
	t.Parallel()

	shared := map[string]any{"x": float64(1)}
	cloned, err := cloneJSONShaped("update", "core", map[string]any{"a": shared, "b": shared})
	if err != nil {
		t.Fatalf("不该失败：%v", err)
	}
	if !DeepEqualJSON(cloned["a"], cloned["b"]) {
		t.Fatalf("两处该展开成一样的子树，实际 %#v", cloned)
	}
}

// TestMergeLayersMergesObjectsAndReplacesEverythingElse 钉住那条唯一的合并规则。
//
// 源: packages/settings/settings/tests/settings.spec.ts:224-236
//
// 数组那一条是重点：按下标合并会造出一个两边都没有的第三种排列，
// 而调用方在配置界面上看到的是自己刚提交的那个数组。
func TestMergeLayersMergesObjectsAndReplacesEverythingElse(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		under, over, want any
	}{
		"两个对象递归合并": {
			map[string]any{"a": map[string]any{"x": float64(1), "y": float64(2)}},
			map[string]any{"a": map[string]any{"y": float64(9)}},
			map[string]any{"a": map[string]any{"x": float64(1), "y": float64(9)}},
		},
		"数组整个替换": {
			map[string]any{"a": []any{float64(1), float64(2), float64(3)}},
			map[string]any{"a": []any{float64(9)}},
			map[string]any{"a": []any{float64(9)}},
		},
		"上层的 null 盖掉下层": {
			map[string]any{"a": float64(1)},
			map[string]any{"a": nil},
			map[string]any{"a": nil},
		},
		"下层没有的键直接落": {
			map[string]any{"a": float64(1)},
			map[string]any{"b": float64(2)},
			map[string]any{"a": float64(1), "b": float64(2)},
		},
		"对象被标量替换": {
			map[string]any{"a": map[string]any{"x": float64(1)}},
			map[string]any{"a": "flat"},
			map[string]any{"a": "flat"},
		},
		"标量被对象替换": {
			map[string]any{"a": "flat"},
			map[string]any{"a": map[string]any{"x": float64(1)}},
			map[string]any{"a": map[string]any{"x": float64(1)}},
		},
		"上层不是对象则整个替换": {
			map[string]any{"a": float64(1)},
			"whole",
			"whole",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := mergeLayers(testCase.under, testCase.over)
			if !DeepEqualJSON(got, testCase.want) {
				t.Fatalf("合并结果 %#v，该是 %#v", got, testCase.want)
			}
		})
	}
}

// TestMergeLayersDoesNotMutateEitherInput 钉住合并不改输入。
//
// 三层是各有各的作者的（见包文档），合并把某一层就地改掉的话，
// 一次读会把「装配方给的值」永久污染成「用户改过的值」。
func TestMergeLayersDoesNotMutateEitherInput(t *testing.T) {
	t.Parallel()

	under := map[string]any{"a": float64(1)}
	over := map[string]any{"b": float64(2)}
	_ = mergeLayers(under, over)

	if len(under) != 1 || len(over) != 1 {
		t.Fatalf("两边都不该被改，under=%#v over=%#v", under, over)
	}
}

// TestMergeSectionsNarrowsToASection 钉住段粒度的入口在两边都缺席时给一个空段而不是 nil。
func TestMergeSectionsNarrowsToASection(t *testing.T) {
	t.Parallel()

	merged := mergeSections(nil, nil)
	if merged == nil || len(merged) != 0 {
		t.Fatalf("该是空段，实际 %#v", merged)
	}
	if got := mergeSections(map[string]any{"a": float64(1)}, nil); !DeepEqualJSON(toAny(got), map[string]any{"a": float64(1)}) {
		t.Fatalf("nil 上层该表示「这一层不存在」，实际 %#v", got)
	}
}

// TestApplyPathOpEditsOnePlace 逐条验按路径编辑。
//
// 源: packages/settings/settings/tests/settings.spec.ts:845-900
func TestApplyPathOpEditsOnePlace(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		section map[string]any
		op      PathOp
		want    map[string]any
	}{
		"设一个顶层键": {
			map[string]any{"a": float64(1)},
			PathOp{Kind: PathOpSet, Path: []string{"b"}, Value: float64(2)},
			map[string]any{"a": float64(1), "b": float64(2)},
		},
		"摘一个顶层键": {
			map[string]any{"a": float64(1), "b": float64(2)},
			PathOp{Kind: PathOpUnset, Path: []string{"b"}},
			map[string]any{"a": float64(1)},
		},
		"嵌套 set 会把中间层建出来": {
			map[string]any{},
			PathOp{Kind: PathOpSet, Path: []string{"a", "b", "c"}, Value: "x"},
			map[string]any{"a": map[string]any{"b": map[string]any{"c": "x"}}},
		},
		"嵌套 set 不动兄弟键": {
			map[string]any{"a": map[string]any{"x": float64(1), "y": float64(2)}},
			PathOp{Kind: PathOpSet, Path: []string{"a", "y"}, Value: float64(9)},
			map[string]any{"a": map[string]any{"x": float64(1), "y": float64(9)}},
		},
		"路径不通的 unset 是空操作": {
			map[string]any{"a": float64(1)},
			PathOp{Kind: PathOpUnset, Path: []string{"nope", "deep"}},
			map[string]any{"a": float64(1)},
		},
		"中间层不是对象时 set 会盖掉它": {
			map[string]any{"a": "flat"},
			PathOp{Kind: PathOpSet, Path: []string{"a", "b"}, Value: "x"},
			map[string]any{"a": map[string]any{"b": "x"}},
		},
		"空路径 set 换整段": {
			map[string]any{"a": float64(1)},
			PathOp{Kind: PathOpSet, Path: nil, Value: map[string]any{"b": float64(2)}},
			map[string]any{"b": float64(2)},
		},
		"空路径 unset 清空整段": {
			map[string]any{"a": float64(1)},
			PathOp{Kind: PathOpUnset, Path: nil},
			map[string]any{},
		},
		"摘一个本来就没有的键": {
			map[string]any{"a": float64(1)},
			PathOp{Kind: PathOpUnset, Path: []string{"nope"}},
			map[string]any{"a": float64(1)},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			original := cloneForTest(testCase.section)
			got, err := applyPathOp(testCase.section, testCase.op)
			if err != nil {
				t.Fatalf("不该失败：%v", err)
			}
			if !DeepEqualJSON(toAny(got), toAny(testCase.want)) {
				t.Fatalf("结果 %#v，该是 %#v", got, testCase.want)
			}
			// 输入不被改动，这样一串 op 里前一个失败不会留下半成品。
			if !DeepEqualJSON(toAny(testCase.section), toAny(original)) {
				t.Fatalf("输入被改了：%#v，原来是 %#v", testCase.section, original)
			}
		})
	}
}

// TestApplyPathOpRefusesANonObjectAtTheSectionRoot 钉住段根只能是对象。
//
// 源: packages/settings/settings/tests/settings.spec.ts:894-900
func TestApplyPathOpRefusesANonObjectAtTheSectionRoot(t *testing.T) {
	t.Parallel()

	section := map[string]any{"a": float64(1)}
	if _, err := applyPathOp(section, PathOp{Kind: PathOpSet, Value: "flat"}); !errors.Is(err, ErrMalformedSection) {
		t.Fatalf("该报 ErrMalformedSection，实际 %v", err)
	}
	if !reflect.DeepEqual(section, map[string]any{"a": float64(1)}) {
		t.Fatalf("失败时不该动原段，实际 %#v", section)
	}
}
