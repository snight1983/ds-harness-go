// 本文件的作用：验字符预算补默认值、验一遍这一步，该拒的都拒了，
// 以及那两处「零是有意义的」没有被默认值悄悄改写。

package toolresultpruner

import (
	"errors"
	"testing"
	"unicode/utf8"
)

// intOf 交出一个指向该值的指针，给那两个「零有意义」的字段用。
func intOf(value int) *int { return &value }

func TestDefaults交出的是那三档(t *testing.T) {
	t.Parallel()

	want := ResolvedConfig{
		ThresholdChars: DefaultThresholdChars,
		HeadChars:      DefaultHeadChars,
		TailChars:      DefaultTailChars,
	}
	if Defaults() != want {
		t.Fatalf("交出来的是 %+v", Defaults())
	}
	// 每次交出去的都是新的一份：调用方改了自己那份，下一个调用方拿到的不受影响。
	touched := Defaults()
	touched.HeadChars = 1
	if Defaults() != want {
		t.Fatalf("被改了：%+v", Defaults())
	}
}

func TestResolve什么都不给时补上默认值(t *testing.T) {
	t.Parallel()

	resolved, err := Config{}.Resolve()
	if err != nil {
		t.Fatalf("验不过：%v", err)
	}
	if resolved != Defaults() {
		t.Fatalf("补出来的是 %+v", resolved)
	}
}

func TestResolve只盖住说了的那几档(t *testing.T) {
	t.Parallel()

	resolved, err := Config{ThresholdChars: 20_000, HeadChars: intOf(20)}.Resolve()
	if err != nil {
		t.Fatalf("验不过：%v", err)
	}
	if resolved.ThresholdChars != 20_000 || resolved.HeadChars != 20 {
		t.Fatalf("说了的那两档是 %+v", resolved)
	}
	if resolved.TailChars != DefaultTailChars {
		t.Fatalf("没说的那一档变成了 %d", resolved.TailChars)
	}
}

func TestResolve有意义的零不会被默认值改写(t *testing.T) {
	t.Parallel()

	// 头尾都不留是明确的意思，而它们的默认值不是零——用零值当「没给」的话，
	// 这个意思会被静默改写成 4096 和 1024。
	resolved, err := Config{
		ThresholdChars: utf8.RuneCountInString(PruneMarker),
		HeadChars:      intOf(0),
		TailChars:      intOf(0),
	}.Resolve()
	if err != nil {
		t.Fatalf("验不过：%v", err)
	}
	if resolved.HeadChars != 0 || resolved.TailChars != 0 {
		t.Fatalf("头尾是 %+v", resolved)
	}
}

func TestResolve验不过的几种(t *testing.T) {
	t.Parallel()

	for name, config := range map[string]Config{
		"压力线是零":  {ThresholdChars: -1},
		"压力线是负的": {ThresholdChars: -5},
		"头是负的":   {HeadChars: intOf(-1)},
		"尾是负的":   {TailChars: intOf(-1)},
		// 砍完交出去的仍然超过压力线：下一趟又会去砍，而它已经砍无可砍了。
		"砍完还是超过压力线": {ThresholdChars: 50, HeadChars: intOf(20), TailChars: intOf(20)},
		// 默认的头尾加起来远超过一条自定义的小压力线，这一条同样要拦住。
		"只调小了压力线": {ThresholdChars: 10},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := config.Resolve(); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("报的是 %v", err)
			}
		})
	}
}

func TestResolve压力线恰好等于交出去的量算过(t *testing.T) {
	t.Parallel()

	// 边界上是允许的：砍完恰好压在线上，不会再触发下一趟。
	marker := utf8.RuneCountInString(PruneMarker)
	if _, err := (Config{
		ThresholdChars: marker + 7,
		HeadChars:      intOf(4),
		TailChars:      intOf(3),
	}).Resolve(); err != nil {
		t.Fatalf("拦了：%v", err)
	}
}
