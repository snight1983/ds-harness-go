// 本文件验这条接缝最底下那几件事：命名空间的语法、变化判定、冲突错误的形状。
//
// 源: packages/settings/settings/tests/settings.spec.ts:78-86,308-320,950-958

package settings

import (
	"errors"
	"math"
	"testing"
)

// TestNewNamespaceBrandsKebabCase 钉住命名空间的语法就是小写短横线。
//
// 源: packages/settings/settings/tests/settings.spec.ts:79-86
func TestNewNamespaceBrandsKebabCase(t *testing.T) {
	t.Parallel()

	for _, valid := range []string{"core", "model-provider", "a", "z9", "x-1-y"} {
		ns, err := NewNamespace(valid)
		if err != nil {
			t.Errorf("%q 本该是合法命名空间，实际 %v", valid, err)
			continue
		}
		if string(ns) != valid {
			t.Errorf("命名空间不该被改写，%q 变成了 %q", valid, string(ns))
		}
	}
}

// TestNewNamespaceRejectsEveryOtherShape 逐条验不合语法的名字。
//
// 源: packages/settings/settings/tests/settings.spec.ts:83-85
//
// 每一条后面写的是它犯的是哪一款：合并成一句「都不行」的话，
// 正则被改松之后仍然会有几条碰巧通过。
func TestNewNamespaceRejectsEveryOtherShape(t *testing.T) {
	t.Parallel()

	for name, invalid := range map[string]string{
		"空串":    "",
		"大写":    "Core",
		"数字打头":  "9core",
		"短横线打头": "-core",
		"下划线":   "core_x",
		"带斜杠":   "core/x",
		"带空格":   "core x",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := NewNamespace(invalid); !errors.Is(err, ErrInvalidNamespace) {
				t.Fatalf("该报 ErrInvalidNamespace，实际 %v", err)
			}
		})
	}
}

// TestDeepEqualJSONComparesJSONShapes 逐条钉住这条接缝**唯一**的变化判定。
//
// 源: packages/settings/settings/tests/settings.spec.ts:308-320
//
// 「数按 float64 比」那两条是这里最要紧的：json.Unmarshal 出来的数都是 float64，
// 而调用方手写的字面量是 int。判错的后果是每次写入都发一条假的变更通知。
func TestDeepEqualJSONComparesJSONShapes(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		left, right any
		equal       bool
	}{
		"两个 nil":        {nil, nil, true},
		"nil 与零":        {nil, float64(0), false},
		"同形对象":          {map[string]any{"a": float64(1)}, map[string]any{"a": float64(1)}, true},
		"键序无关":          {map[string]any{"a": float64(1), "b": float64(2)}, map[string]any{"b": float64(2), "a": float64(1)}, true},
		"少一个键":          {map[string]any{"a": float64(1), "b": float64(2)}, map[string]any{"a": float64(1)}, false},
		"多一个键":          {map[string]any{"a": float64(1)}, map[string]any{"a": float64(1), "b": float64(2)}, false},
		"值不同":           {map[string]any{"a": float64(1)}, map[string]any{"a": float64(2)}, false},
		"对象与非对象":        {map[string]any{"a": float64(1)}, "a", false},
		"同形数组":          {[]any{float64(1), "x"}, []any{float64(1), "x"}, true},
		"数组顺序不同":        {[]any{float64(1), float64(2)}, []any{float64(2), float64(1)}, false},
		"数组长度不同":        {[]any{float64(1)}, []any{float64(1), float64(2)}, false},
		"数组与非数组":        {[]any{float64(1)}, map[string]any{"0": float64(1)}, false},
		"嵌套相同":          {map[string]any{"a": []any{map[string]any{"b": true}}}, map[string]any{"a": []any{map[string]any{"b": true}}}, true},
		"int 与 float64": {1, float64(1), false},
		"两个 float64":    {float64(1), float64(1), true},
		"字符串相同":         {"x", "x", true},
		"布尔不同":          {true, false, false},
		"左侧 nil 右侧有值":   {nil, "x", false},
		"右侧 nil 左侧是标量":  {"x", nil, false},
		"不可比的动态类型一律算不等": {[]string{"a"}, []string{"a"}, false},
		"一侧不可比":         {[]string{"a"}, "a", false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := DeepEqualJSON(testCase.left, testCase.right); got != testCase.equal {
				t.Fatalf("DeepEqualJSON(%#v, %#v) = %v，该是 %v",
					testCase.left, testCase.right, got, testCase.equal)
			}
		})
	}
}

// TestDeepEqualJSONDoesNotPanicOnIncomparableDynamicTypes 钉住那条兜底不是装饰。
//
// Go 里对一个动态类型是切片或 map 的接口值做 == 是运行期 panic。
// 这是一个导出函数，一次传错类型不该把进程炸掉——所以这条单独钉，
// 而不是混在上面那张表里：表里那两行验的是「答案是 false」，
// 这里验的是「根本没有 panic」。
func TestDeepEqualJSONDoesNotPanicOnIncomparableDynamicTypes(t *testing.T) {
	t.Parallel()

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("不该 panic，实际 %v", recovered)
		}
	}()

	_ = DeepEqualJSON(map[int]int{1: 1}, map[int]int{1: 1})
	_ = DeepEqualJSON(func() {}, func() {})
	_ = DeepEqualJSON([]float64{math.NaN()}, []float64{math.NaN()})
}

// TestConflictErrorCarriesBothRevisions 钉住冲突错误带着两个修订号，且能被 errors.Is 认出。
//
// 源: packages/settings/settings/tests/settings.spec.ts:950-958
//
// 两件事一起验是有理由的：协议层要 errors.As 拿两个数做映射，
// 而只想问「是不是冲突」的调用方走 errors.Is。少哪一条都会让一类调用方去解析文案。
func TestConflictErrorCarriesBothRevisions(t *testing.T) {
	t.Parallel()

	var err error = &ConflictError{Namespace: "core", Expected: 1, Actual: 3}

	if !errors.Is(err, ErrConflict) {
		t.Fatalf("该能被 errors.Is 认成 ErrConflict，实际 %v", err)
	}
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("该能被 errors.As 取成 *ConflictError，实际 %v", err)
	}
	if conflict.Namespace != "core" || conflict.Expected != 1 || conflict.Actual != 3 {
		t.Fatalf("三个字段该原样带着，实际 %+v", conflict)
	}
	if conflict.Error() == "" {
		t.Fatal("错误文案不该是空的")
	}
}
