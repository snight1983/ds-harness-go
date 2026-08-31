// 本文件的作用：把 DSH 那份 output-retention.spec.ts 钉住的行为在 Go 侧重新钉一遍，
// 外加 Go 这边才需要钉的几条（零值处置非法、构造函数报错而不是 panic、缓冲区复用）。
//
// # 这个包的错法
//
// 全是**算错数**，而算错的数会变成一句给模型看的谎话：说「丢了 3 个字节」而实际丢了 4 个，
// 或者一个漏填的丢弃元数据渲染成空子句，让 footer 看起来像是「什么都没丢」。
// 没有任何一处会报错或崩溃，所以每一条都得有断言压着。
//
// 另一半是 UTF-8 边界。切口上多留半个码点，输出里就多一个替换字符——而那个替换字符
// 是**这次切割自己造出来的**，上游根本没有。
package outputretention_test

import (
	"strconv"
	"strings"
	"testing"

	"ds-harness-go/util/outputretention"
)

// wantExact 断言丢弃元数据是「确切丢了 count 个」。
func wantExact(t *testing.T, got outputretention.Omitted, count int) {
	t.Helper()
	actual, ok := got.Count()
	if !ok {
		t.Fatalf("该是 exact 形态，实际 kind=%v", got.Kind())
	}
	if actual != count {
		t.Errorf("该丢了 %d 个，实际 %d 个", count, actual)
	}
}

// wantNone 断言丢弃元数据是「一个都没丢」。
func wantNone(t *testing.T, got outputretention.Omitted) {
	t.Helper()
	if got.Kind() != outputretention.OmittedKindNone {
		t.Errorf("该是 none 形态，实际 kind=%v", got.Kind())
	}
}

// newItems 造一个单元留存器，构造失败当场停。
func newItems[T any](t *testing.T, strategy outputretention.ItemRetentionStrategy) *outputretention.ItemRetainer[T] {
	t.Helper()
	retainer, err := outputretention.NewItemRetainer[T](strategy)
	if err != nil {
		t.Fatalf("造单元留存器意外失败：%v", err)
	}
	return retainer
}

// newText 造一个文本留存器，构造失败当场停。
func newText(t *testing.T, strategy outputretention.TextRetentionStrategy) *outputretention.TextRetainer {
	t.Helper()
	retainer, err := outputretention.NewTextRetainer(strategy)
	if err != nil {
		t.Fatalf("造文本留存器意外失败：%v", err)
	}
	return retainer
}

// TestItemRetainerKeepsTheHeadAndCountsTheRest 钉住 head 留存那一圈。
//
// 源: packages/util/output-retention/tests/output-retention.spec.ts:14-49
//
// 重点是**超过上限之后调用方还在推**：丢弃计数的精确性全靠这个，
// 调用方一到上限就不推了的话，最后那个数就是错的。
func TestItemRetainerKeepsTheHeadAndCountsTheRest(t *testing.T) {
	t.Parallel()

	retainer := newItems[string](t, outputretention.ItemHead(2))
	if got := retainer.Push("a"); !got.Kept || got.Truncated {
		t.Errorf("第 1 个该留下且尚未截断，实际 %+v", got)
	}
	if got := retainer.Push("b"); !got.Kept || got.Truncated {
		t.Errorf("第 2 个该留下且尚未截断，实际 %+v", got)
	}
	if got := retainer.Push("c"); got.Kept || !got.Truncated {
		t.Errorf("第 3 个该被丢掉且已截断，实际 %+v", got)
	}

	result := retainer.Finish()
	if len(result.Items) != 2 || result.Items[0] != "a" || result.Items[1] != "b" {
		t.Errorf("该留下 [a b]，实际 %v", result.Items)
	}
	if result.Kept != 2 {
		t.Errorf("Kept 该是 2，实际 %d", result.Kept)
	}
	// Seen 数的是**看见过的**，不是留下的——两者混起来的话丢弃计数就永远是 0。
	if result.Seen != 3 {
		t.Errorf("Seen 该是 3，实际 %d", result.Seen)
	}
	if !result.Truncated {
		t.Error("该标成截断了")
	}
	wantExact(t, result.Omitted, 1)
}

// TestItemRetainerReportsNoneWhenEverythingFits 钉住没超上限时是 none 而不是 exact 0。
//
// 源: packages/util/output-retention/tests/output-retention.spec.ts:29-37
//
// 报成 exact 0 的话，[outputretention.DescribeOmitted] 会印出 "Omitted 0 items."——
// 一句在什么都没丢的时候凭空出现的话。
func TestItemRetainerReportsNoneWhenEverythingFits(t *testing.T) {
	t.Parallel()

	retainer := newItems[int](t, outputretention.ItemHead(3))
	retainer.Push(1)
	retainer.Push(2)

	result := retainer.Finish()
	if len(result.Items) != 2 || result.Items[0] != 1 || result.Items[1] != 2 {
		t.Errorf("该留下 [1 2]，实际 %v", result.Items)
	}
	if result.Truncated {
		t.Error("没超上限不该标成截断")
	}
	wantNone(t, result.Omitted)
}

// TestItemRetainerWithZeroBudgetKeepsNothing 钉住上限为零是合法的，且每一个都记进丢弃。
//
// 源: packages/util/output-retention/tests/output-retention.spec.ts:51-59
func TestItemRetainerWithZeroBudgetKeepsNothing(t *testing.T) {
	t.Parallel()

	retainer := newItems[string](t, outputretention.ItemHead(0))
	if got := retainer.Push("a"); got.Kept || !got.Truncated {
		t.Errorf("上限为零时该被丢掉，实际 %+v", got)
	}

	result := retainer.Finish()
	if len(result.Items) != 0 {
		t.Errorf("该一个都不留，实际 %v", result.Items)
	}
	if result.Kept != 0 {
		t.Errorf("Kept 该是 0，实际 %d", result.Kept)
	}
	wantExact(t, result.Omitted, 1)
}

// TestItemRetentionStrategyValidate 钉住预算校验，以及零值策略被拒。
//
// 源: packages/util/output-retention/tests/output-retention.spec.ts:61-66
//
// 新增: DSH 那边还有一条「1.5 不是整数」，Go 这边预算的类型就是 int，那个分支没有
// 产出方。取而代之的是零值策略——DSH 靠可判别联合保证 kind 一定在，Go 的结构体
// 字面量可以只写一半，而一个漏填的策略会安静地把所有内容丢光。
func TestItemRetentionStrategyValidate(t *testing.T) {
	t.Parallel()

	if err := outputretention.ItemHead(0).Validate(); err != nil {
		t.Errorf("上限为零是合法的，实际 %v", err)
	}
	if err := outputretention.ItemHead(-1).Validate(); err == nil {
		t.Error("负数上限该被拒绝，实际通过了")
	}
	if err := (outputretention.ItemRetentionStrategy{}).Validate(); err == nil {
		t.Error("零值策略该被拒绝，实际通过了")
	}
	if _, err := outputretention.NewItemRetainer[string](outputretention.ItemHead(-1)); err == nil {
		t.Error("构造函数该把校验失败传出来，实际通过了")
	}
}

// TestTextRetainerHeadKeepsThePrefix 钉住 head 策略那一圈，包括「恰好填满」不算截断。
//
// 源: packages/util/output-retention/tests/output-retention.spec.ts:69-99
func TestTextRetainerHeadKeepsThePrefix(t *testing.T) {
	t.Parallel()

	retainer := newText(t, outputretention.TextHead(5))
	if got := retainer.PushString("abc"); !got.Kept || got.Truncated {
		t.Errorf("还没到上限，该全留下，实际 %+v", got)
	}
	// "de" 把上限**恰好**填满，一个字节都没丢，所以仍然是全留下。
	if got := retainer.PushString("de"); !got.Kept || got.Truncated {
		t.Errorf("恰好填满上限不该算丢，实际 %+v", got)
	}
	if got := retainer.PushString("fgh"); got.Kept || !got.Truncated {
		t.Errorf("超出上限该算丢，实际 %+v", got)
	}

	result := retainer.Finish()
	if result.Text != "abcde" {
		t.Errorf("该留下 abcde，实际 %q", result.Text)
	}
	if !result.Truncated {
		t.Error("该标成截断了")
	}
	wantExact(t, result.OmittedBytes, 3)
}

// TestTextRetainerFlagsAPartiallyDroppedChunk 钉住「这一块只留下一半」也算没全留下。
//
// 源: packages/util/output-retention/tests/output-retention.spec.ts:83-89
//
// Kept 的含义是「这一块**一个字节都没丢**」。放宽成「留下了一部分就算留下」的话，
// 调用方就没法靠这个回执知道自己该停下了。
func TestTextRetainerFlagsAPartiallyDroppedChunk(t *testing.T) {
	t.Parallel()

	retainer := newText(t, outputretention.TextHead(4))
	retainer.PushString("ab")
	// "cde" 跨在上限上：c、d 进得去，e 掉了。
	if got := retainer.PushString("cde"); got.Kept || !got.Truncated {
		t.Errorf("这一块掉了一个字节，不该算全留下，实际 %+v", got)
	}
	if text := retainer.Finish().Text; text != "abcd" {
		t.Errorf("该留下 abcd，实际 %q", text)
	}
}

// TestTextRetainerTailKeepsTheEnd 钉住 tail 策略，以及老块滑出窗口后被丢掉。
//
// 源: packages/util/output-retention/tests/output-retention.spec.ts:101-127
func TestTextRetainerTailKeepsTheEnd(t *testing.T) {
	t.Parallel()

	retainer := newText(t, outputretention.TextTail(4))
	if got := retainer.PushString("hello"); got.Kept || !got.Truncated {
		t.Errorf("第一块就超过了尾巴窗口，实际 %+v", got)
	}
	retainer.PushString("world")

	result := retainer.Finish()
	if result.Text != "orld" { // helloworld 的最后 4 个字节
		t.Errorf("该留下 orld，实际 %q", result.Text)
	}
	if !result.Truncated {
		t.Error("该标成截断了")
	}
	wantExact(t, result.OmittedBytes, 6)
}

// TestTextRetainerTailKeepsEverythingUnderTheCap 钉住没到上限时一个字节都不丢。
//
// 源: packages/util/output-retention/tests/output-retention.spec.ts:112-119
func TestTextRetainerTailKeepsEverythingUnderTheCap(t *testing.T) {
	t.Parallel()

	retainer := newText(t, outputretention.TextTail(100))
	retainer.PushString("short")

	result := retainer.Finish()
	if result.Text != "short" {
		t.Errorf("该原样留下，实际 %q", result.Text)
	}
	if result.Truncated {
		t.Error("没到上限不该标成截断")
	}
	wantNone(t, result.OmittedBytes)
}

// TestTextRetainerTailSlidesOldChunksOut 钉住尾巴是**滚动**的。
//
// 源: packages/util/output-retention/tests/output-retention.spec.ts:121-126
//
// 这一条同时守着内存上界：滚不动的话，一条长流会把整条都攒在内存里。
func TestTextRetainerTailSlidesOldChunksOut(t *testing.T) {
	t.Parallel()

	retainer := newText(t, outputretention.TextTail(3))
	for _, chunk := range []string{"11", "22", "33", "44"} {
		retainer.PushString(chunk)
	}
	if text := retainer.Finish().Text; text != "344" {
		t.Errorf("该只剩最后 3 个字节 344，实际 %q", text)
	}
}

// TestTextRetainerHeadTailOmitsTheMiddle 钉住两头都留、中间丢掉。
//
// 源: packages/util/output-retention/tests/output-retention.spec.ts:129-146
func TestTextRetainerHeadTailOmitsTheMiddle(t *testing.T) {
	t.Parallel()

	retainer := newText(t, outputretention.TextHeadTail(3, 3))
	retainer.PushString("abcdefghij") // 10 字节：头 abc、尾 hij、中间 defg 丢掉

	result := retainer.Finish()
	if result.Text != "abchij" {
		t.Errorf("该留下 abchij，实际 %q", result.Text)
	}
	if !result.Truncated {
		t.Error("该标成截断了")
	}
	wantExact(t, result.OmittedBytes, 4)
}

// TestTextRetainerHeadTailDoesNotDoubleCount 钉住头尾正好铺满整条流时**什么都没丢**。
//
// 源: packages/util/output-retention/tests/output-retention.spec.ts:139-146
//
// 算重的话会报出一个负数或者凭空的丢弃量——两头本来就是同一条流上相邻的两段。
func TestTextRetainerHeadTailDoesNotDoubleCount(t *testing.T) {
	t.Parallel()

	retainer := newText(t, outputretention.TextHeadTail(3, 3))
	retainer.PushString("abcdef") // 恰好 头 3 + 尾 3

	result := retainer.Finish()
	if result.Text != "abcdef" {
		t.Errorf("该原样留下，实际 %q", result.Text)
	}
	if result.Truncated {
		t.Error("什么都没丢，不该标成截断")
	}
	wantNone(t, result.OmittedBytes)
}

// TestTextRetainerKeepsACodepointSpanningTheArtificialSplit 钉住跨在头尾分界线上的码点
// 在**什么都没丢**的情况下必须完好。
//
// 源: packages/util/output-retention/tests/output-retention.spec.ts:148-160
//
// 头尾铺满整条流时，那道分界线是人为的：é 是 C3 A9 两个字节，头 1 字节 + 尾 3 字节
// 正好把 4 个字节全覆盖，而分界线落在 é 的**中间**。两段字节是相邻的，所以整个 éab
// 必须活下来——分开修边界或者分开解码的话，会在一个字节都没丢的情况下把 é 弄没。
func TestTextRetainerKeepsACodepointSpanningTheArtificialSplit(t *testing.T) {
	t.Parallel()

	retainer := newText(t, outputretention.TextHeadTail(1, 3))
	retainer.PushString("éab")

	result := retainer.Finish()
	if result.Text != "éab" {
		t.Errorf("该完整留下 éab，实际 %q", result.Text)
	}
	if result.Truncated {
		t.Error("什么都没丢，不该标成截断")
	}
	wantNone(t, result.OmittedBytes)
}

// TestTextRetainerTrimsBoundaryPartialsOnceTheMiddleIsOmitted 钉住真的丢了中间那段时，
// 两边就是**真实的**切口，各修各的边界。
//
// 源: packages/util/output-retention/tests/output-retention.spec.ts:162-172
func TestTextRetainerTrimsBoundaryPartialsOnceTheMiddleIsOmitted(t *testing.T) {
	t.Parallel()

	retainer := newText(t, outputretention.TextHeadTail(2, 2))
	retainer.PushString("a€€b") // 8 字节；头是 a + 半个 €，尾是 半个 € + b

	result := retainer.Finish()
	if !result.Truncated {
		t.Error("该标成截断了")
	}
	if strings.Contains(result.Text, "�") {
		t.Errorf("切割不该造出替换字符，实际 %q", result.Text)
	}
	if !strings.HasPrefix(result.Text, "a") || !strings.HasSuffix(result.Text, "b") {
		t.Errorf("该以 a 开头、以 b 结尾，实际 %q", result.Text)
	}
}

// TestTextRetainerZeroBudgets 钉住零预算与空流。
//
// 源: packages/util/output-retention/tests/output-retention.spec.ts:175-190
func TestTextRetainerZeroBudgets(t *testing.T) {
	t.Parallel()

	t.Run("head 上限为零时一个字节都不留", func(t *testing.T) {
		t.Parallel()

		retainer := newText(t, outputretention.TextHead(0))
		if got := retainer.PushString("x"); got.Kept || !got.Truncated {
			t.Errorf("上限为零时该被丢掉，实际 %+v", got)
		}
		result := retainer.Finish()
		if result.Text != "" {
			t.Errorf("该是空串，实际 %q", result.Text)
		}
		wantExact(t, result.OmittedBytes, 1)
	})

	t.Run("空流什么都不丢", func(t *testing.T) {
		t.Parallel()

		result := newText(t, outputretention.TextHeadTail(2, 2)).Finish()
		if result.Text != "" {
			t.Errorf("该是空串，实际 %q", result.Text)
		}
		if result.Truncated {
			t.Error("空流不该标成截断")
		}
		wantNone(t, result.OmittedBytes)
	})
}

// TestTextRetentionStrategyValidate 钉住三种策略各自的预算校验，以及零值被拒。
//
// 源: packages/util/output-retention/tests/output-retention.spec.ts:192-201
func TestTextRetentionStrategyValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		strategy outputretention.TextRetentionStrategy
		wantOK   bool
	}{
		{"head 零预算", outputretention.TextHead(0), true},
		{"head 负预算", outputretention.TextHead(-1), false},
		{"tail 负预算", outputretention.TextTail(-1), false},
		{"headTail 头是负数", outputretention.TextHeadTail(-1, 2), false},
		{"headTail 尾是负数", outputretention.TextHeadTail(2, -1), false},
		{"headTail 两个零", outputretention.TextHeadTail(0, 0), true},
		{"零值策略", outputretention.TextRetentionStrategy{}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.strategy.Validate()
			if test.wantOK && err != nil {
				t.Fatalf("该通过校验，实际 %v", err)
			}
			if !test.wantOK && err == nil {
				t.Fatal("该被拒绝，实际通过了")
			}
			if _, constructErr := outputretention.NewTextRetainer(test.strategy); (constructErr == nil) != test.wantOK {
				t.Errorf("构造函数该和 Validate 给出同样的结论，实际 %v", constructErr)
			}
		})
	}
}

// TestTextRetainerUTF8Boundaries 钉住每一处切口上的 UTF-8 边界处理。
//
// 源: packages/util/output-retention/tests/output-retention.spec.ts:204-318
//
// 每一行都是一种被切坏的方式。丢弃计数一律按**实际返回的字节**算——修边界会把半个
// 码点的那几个字节也丢掉，只按预算报会高估留下来的内容，那么据此写出的
// 「Omitted N bytes」就是一句谎话。
func TestTextRetainerUTF8Boundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		strategy    outputretention.TextRetentionStrategy
		chunk       []byte
		wantText    string
		wantOmitted int
	}{
		// € 是 E2 82 AC。头留 2 字节 = a + € 的首字节，那半个必须被砍掉而不是解成替换字符。
		// 丢弃量是 5 − 1 = 4，不是修边界之前那个预算 3。
		{"头切口砍掉半个三字节码点", outputretention.TextHead(2), []byte("a€b"), "a", 4},
		// 尾留 2 字节 = AC + b，AC 是 € 的中间一段，必须从开头砍掉。
		{"尾切口砍掉开头的续接字节", outputretention.TextTail(2), []byte("a€b"), "b", 4},
		// 两头各修一次，留下的只有 a 和 b 两个字节，所以是 8 − 2 = 6，不是预算的 4。
		{"头尾各修一次时计数按实际返回算", outputretention.TextHeadTail(2, 2), []byte("a€€b"), "ab", 6},
		// 正好放得下的整码点不能动。
		{"放得下的三字节码点原样留下", outputretention.TextHead(3), []byte("€x"), "€", 1},
		// é 是 C3 A9，头留 2 字节 = a + 首字节。
		{"头切口砍掉半个两字节码点", outputretention.TextHead(2), []byte("aé"), "a", 2},
		// 😀 是 F0 9F 98 80，头留 3 字节 = a + 前两个字节。
		{"头切口砍掉半个四字节码点", outputretention.TextHead(3), []byte("a😀"), "a", 4},
		{"放得下的四字节码点原样留下", outputretention.TextHead(4), []byte("😀x"), "😀", 1},
		// 结尾全是续接字节、往回够不着首字节：这不是一个可以砍的「不完整序列」，
		// 修边界的人要放手，把它留给解码器换成替换字符。这里只钉住它**没被当成半个码点吃掉**：
		// 丢的只有上限之外那个 z。
		{"结尾是一串孤立续接字节时原样留下", outputretention.TextHead(2), []byte{0x80, 0x80, 0x7a}, "�", 1},
		// 0xF8 不是合法的 UTF-8 首字节。修边界的人要认出「这不是首字节」并放手，
		// 而不是砍掉一个根本不存在的半截序列。
		{"结尾是非法首字节时原样留下", outputretention.TextHead(1), []byte{0xf8, 0x61}, "�", 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			retainer := newText(t, test.strategy)
			retainer.Push(test.chunk)

			result := retainer.Finish()
			if result.Text != test.wantText {
				t.Errorf("该留下 %q，实际 %q", test.wantText, result.Text)
			}
			wantExact(t, result.OmittedBytes, test.wantOmitted)
		})
	}
}

// TestTextRetainerNeverGluesACodepointAcrossTheGap 钉住绝不隔着丢掉的那段把两半凑成一个码点。
//
// 源: packages/util/output-retention/tests/output-retention.spec.ts:252-260
//
// 头切在一个 € 的中间、尾从另一个 € 的中间开始。两边各修各的，结果只可能是空——
// 凑起来的话会得到一个原文里**根本不存在**的字符。
func TestTextRetainerNeverGluesACodepointAcrossTheGap(t *testing.T) {
	t.Parallel()

	retainer := newText(t, outputretention.TextHeadTail(2, 2))
	retainer.PushString("€€€") // 9 字节

	result := retainer.Finish()
	if strings.Contains(result.Text, "�") {
		t.Errorf("切割不该造出替换字符，实际 %q", result.Text)
	}
	if result.Text != "" {
		t.Errorf("两边修完都是空的，实际 %q", result.Text)
	}
	if !result.Truncated {
		t.Error("该标成截断了")
	}
	wantExact(t, result.OmittedBytes, 9)
}

// TestTextRetainerAcceptsRawBytes 钉住裸字节和字符串两个入口给出同样的结果。
//
// 源: packages/util/output-retention/tests/output-retention.spec.ts:262-267
func TestTextRetainerAcceptsRawBytes(t *testing.T) {
	t.Parallel()

	retainer := newText(t, outputretention.TextHead(2))
	retainer.Push([]byte("xy"))
	retainer.Push([]byte("z"))
	if text := retainer.Finish().Text; text != "xy" {
		t.Errorf("该留下 xy，实际 %q", text)
	}
}

// TestTextRetainerCopiesTheCallerBuffer 钉住推进来的字节被**拷贝**下来了。
//
// 新增: DSH 存的是 subarray，也就是调用方那个缓冲区的视图。Go 里读流的标准写法是
// 建一个缓冲区然后循环复用它（bash 的 stdout 正是这么读的），存视图的话所有留下来的
// 块最后都指向同一段内存，全部变成最后一次读到的内容。而症状是**静默的**：
// 长度对、丢弃计数对，只有内容是错的。
func TestTextRetainerCopiesTheCallerBuffer(t *testing.T) {
	t.Parallel()

	buffer := []byte("ab")

	head := newText(t, outputretention.TextHead(4))
	tail := newText(t, outputretention.TextTail(4))
	head.Push(buffer)
	tail.Push(buffer)

	copy(buffer, "cd") // 调用方复用了同一个缓冲区

	head.Push(buffer)
	tail.Push(buffer)

	if text := head.Finish().Text; text != "abcd" {
		t.Errorf("前缀该记住第一次推进来的内容，实际 %q", text)
	}
	if text := tail.Finish().Text; text != "abcd" {
		t.Errorf("后缀该记住第一次推进来的内容，实际 %q", text)
	}
}

// TestDescribeOmitted 钉住那三句标准化文案，重点是 unknown **不印计数**。
//
// 源: packages/util/output-retention/tests/output-retention.spec.ts:320-333
//
// 这几句是给模型看的，所以是英文。unknown 印出一个数就是凭空的精确度——调用方压根
// 没给过那个数。
func TestDescribeOmitted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		omitted outputretention.Omitted
		unit    outputretention.Unit
		want    string
	}{
		{"确切计数", outputretention.OmittedExact(3), outputretention.UnitItems, "Omitted 3 items."},
		{"确切计数换个单位", outputretention.OmittedExact(12), outputretention.UnitBytes, "Omitted 12 bytes."},
		{"数不出来时不印计数", outputretention.OmittedUnknown(), outputretention.UnitLines, "More lines were omitted."},
		{"什么都没丢时是空串", outputretention.OmittedNone(), outputretention.UnitChars, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := outputretention.DescribeOmitted(test.omitted, test.unit)
			if err != nil {
				t.Fatalf("意外失败：%v", err)
			}
			if got != test.want {
				t.Errorf("该是 %q，实际 %q", test.want, got)
			}
		})
	}
}

// TestDescribeOmittedRejectsIncompleteInput 钉住漏填和非法单位是**报出来**的。
//
// 新增: DSH 那边靠穷尽的 switch，漏一个分支是编译错误。Go 这边零值和非法单位都进得来，
// 而两者渲染出来的分别是「空子句」和「Omitted 3 .」——前者让 footer 看起来像是什么都
// 没丢，后者是一句残句。都得当场报出来。
func TestDescribeOmittedRejectsIncompleteInput(t *testing.T) {
	t.Parallel()

	if _, err := outputretention.DescribeOmitted(outputretention.Omitted{}, outputretention.UnitItems); err == nil {
		t.Error("漏填的丢弃元数据该被拒绝，实际通过了")
	}
	if _, err := outputretention.DescribeOmitted(outputretention.OmittedExact(1), outputretention.Unit("行")); err == nil {
		t.Error("非法单位该被拒绝，实际通过了")
	}
}

// TestUnitAndNoticeStrategyValid 钉住那两个封闭词汇。
func TestUnitAndNoticeStrategyValid(t *testing.T) {
	t.Parallel()

	for _, unit := range []outputretention.Unit{
		outputretention.UnitItems, outputretention.UnitBytes,
		outputretention.UnitChars, outputretention.UnitLines,
	} {
		if !unit.Valid() {
			t.Errorf("%q 该是合法单位", unit)
		}
	}
	for _, unit := range []outputretention.Unit{"", "item", "Bytes", "字节"} {
		if unit.Valid() {
			t.Errorf("%q 不该被当成合法单位", unit)
		}
	}

	for _, strategy := range []outputretention.NoticeStrategy{
		outputretention.NoticeHead, outputretention.NoticeTail, outputretention.NoticeHeadTail,
	} {
		if !strategy.Valid() {
			t.Errorf("%q 该是合法策略名", strategy)
		}
	}
	for _, strategy := range []outputretention.NoticeStrategy{"", "headtail", "HEAD", "middle"} {
		if strategy.Valid() {
			t.Errorf("%q 不该被当成合法策略名", strategy)
		}
	}
}

// TestLimitCarriesItsShape 钉住上限两种形态各自的构造与取值。
func TestLimitCarriesItsShape(t *testing.T) {
	t.Parallel()

	count, ok := outputretention.LimitCount(100).Count()
	if !ok || count != 100 {
		t.Errorf("单数字上限该取得出 100，实际 %d（ok=%v）", count, ok)
	}
	if _, _, ok := outputretention.LimitCount(100).HeadTail(); ok {
		t.Error("单数字形态不该取得出头尾对")
	}

	head, tail, ok := outputretention.LimitHeadTail(2000, 1000).HeadTail()
	if !ok || head != 2000 || tail != 1000 {
		t.Errorf("头尾上限该取得出 2000/1000，实际 %d/%d（ok=%v）", head, tail, ok)
	}
	if _, ok := outputretention.LimitHeadTail(2000, 1000).Count(); ok {
		t.Error("头尾形态不该取得出单个数字")
	}

	if got := (outputretention.Limit{}).Kind(); got != outputretention.LimitKindUnset {
		t.Errorf("零值该是 unset，实际 %v", got)
	}
}

// TestFormatRetentionNotice 钉住 footer 的拼法：库那句标准文案 + 工具自己的恢复指引。
//
// 源: packages/util/output-retention/tests/output-retention.spec.ts:335-376
//
// 恢复的措辞**永远归工具**——只有工具知道该做什么动作。这个库只负责把两半用一个空格
// 连起来，并且哪一半是空的就不留多余的空格。
func TestFormatRetentionNotice(t *testing.T) {
	t.Parallel()

	notice := func(omitted outputretention.Omitted) outputretention.RetentionNotice {
		return outputretention.RetentionNotice{
			Scope:    "grep",
			Strategy: outputretention.NoticeHead,
			Unit:     outputretention.UnitItems,
			Limit:    outputretention.LimitCount(100),
			Kept:     100,
			Omitted:  omitted,
		}
	}

	t.Run("两半都在时用一个空格连起来", func(t *testing.T) {
		t.Parallel()

		got, err := outputretention.FormatRetentionNotice(
			notice(outputretention.OmittedExact(25)),
			func(n outputretention.RetentionNotice) string {
				return "Results capped at " + strconv.Itoa(n.Kept) + ". Narrow the pattern, path, or include to see more."
			},
		)
		if err != nil {
			t.Fatalf("意外失败：%v", err)
		}
		want := "Omitted 25 items. Results capped at 100. Narrow the pattern, path, or include to see more."
		if got != want {
			t.Errorf("该是 %q，实际 %q", want, got)
		}
	})

	t.Run("什么都没丢时只剩恢复指引", func(t *testing.T) {
		t.Parallel()

		got, err := outputretention.FormatRetentionNotice(
			notice(outputretention.OmittedNone()),
			func(outputretention.RetentionNotice) string { return "Recovery text." },
		)
		if err != nil {
			t.Fatalf("意外失败：%v", err)
		}
		if got != "Recovery text." {
			t.Errorf("不该留下多余的空格，实际 %q", got)
		}
	})

	t.Run("工具不给恢复指引时只剩标准文案", func(t *testing.T) {
		t.Parallel()

		got, err := outputretention.FormatRetentionNotice(
			notice(outputretention.OmittedExact(2)),
			func(outputretention.RetentionNotice) string { return "" },
		)
		if err != nil {
			t.Fatalf("意外失败：%v", err)
		}
		if got != "Omitted 2 items." {
			t.Errorf("不该留下多余的空格，实际 %q", got)
		}
	})

	t.Run("整个通知都交给恢复措辞函数", func(t *testing.T) {
		t.Parallel()

		got, err := outputretention.FormatRetentionNotice(
			outputretention.RetentionNotice{
				Scope:    "bash stdout",
				Strategy: outputretention.NoticeHeadTail,
				Unit:     outputretention.UnitBytes,
				Limit:    outputretention.LimitHeadTail(2000, 2000),
				Kept:     4000,
				Omitted:  outputretention.OmittedExact(500),
			},
			func(n outputretention.RetentionNotice) string {
				head, tail, ok := n.Limit.HeadTail()
				if !ok {
					return ""
				}
				return "Kept " + strconv.Itoa(head) + "B head + " + strconv.Itoa(tail) + "B tail."
			},
		)
		if err != nil {
			t.Fatalf("意外失败：%v", err)
		}
		want := "Omitted 500 bytes. Kept 2000B head + 2000B tail."
		if got != want {
			t.Errorf("该是 %q，实际 %q", want, got)
		}
	})
}

// TestRetentionNoticeValidateRejectsIncompleteNotices 钉住漏填的通知是**查出来**的。
//
// 新增: DSH 靠 TypeScript 的必填字段检查，漏一个是编译错误。Go 的结构体字面量可以
// 只写一半，而漏填的那几个字段每一个都会渲染出一句不实的 footer。
func TestRetentionNoticeValidateRejectsIncompleteNotices(t *testing.T) {
	t.Parallel()

	valid := outputretention.RetentionNotice{
		Strategy: outputretention.NoticeHead,
		Unit:     outputretention.UnitItems,
		Limit:    outputretention.LimitCount(10),
		Kept:     10,
		Omitted:  outputretention.OmittedNone(),
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("填齐的通知该通过，实际 %v", err)
	}
	// Scope 这个库自己不读，只转交给恢复措辞函数，所以空着也合法。
	if err := (outputretention.RetentionNotice{
		Strategy: outputretention.NoticeHead, Unit: outputretention.UnitItems,
		Limit: outputretention.LimitCount(10), Omitted: outputretention.OmittedNone(),
	}).Validate(); err != nil {
		t.Errorf("Scope 空着该是合法的，实际 %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*outputretention.RetentionNotice)
	}{
		{"策略名漏填", func(n *outputretention.RetentionNotice) { n.Strategy = "" }},
		{"策略名拼错", func(n *outputretention.RetentionNotice) { n.Strategy = "headtail" }},
		{"单位漏填", func(n *outputretention.RetentionNotice) { n.Unit = "" }},
		{"上限漏填", func(n *outputretention.RetentionNotice) { n.Limit = outputretention.Limit{} }},
		{"留下的数量是负数", func(n *outputretention.RetentionNotice) { n.Kept = -1 }},
		{"丢弃元数据漏填", func(n *outputretention.RetentionNotice) { n.Omitted = outputretention.Omitted{} }},
		{"丢弃的数量是负数", func(n *outputretention.RetentionNotice) {
			n.Omitted = outputretention.OmittedExact(-1)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			notice := valid
			test.mutate(&notice)
			if err := notice.Validate(); err == nil {
				t.Fatal("该被拒绝，实际通过了")
			}
			if _, err := outputretention.FormatRetentionNotice(notice,
				func(outputretention.RetentionNotice) string { return "" }); err == nil {
				t.Error("格式化该把校验失败传出来，实际通过了")
			}
		})
	}
}
