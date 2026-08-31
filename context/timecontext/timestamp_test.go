// 本文件的作用：验时间戳的排法——偏移量、方括号里的名字、以及 UTC 上不出 `Z`。

package timecontext

import (
	"testing"
	"time"
)

func TestUTC上的时间戳排成带正负号的零偏移(t *testing.T) {
	t.Parallel()

	// Go 更常见的 Z07:00 在这里会排出单个 `Z`，那和 DSH 的字节对不上。
	got := FormatTimestamp(time.Date(2026, 3, 5, 14, 30, 9, 0, time.UTC), time.UTC)
	want := "2026-03-05T14:30:09+00:00[UTC]"
	if got != want {
		t.Fatalf("时间戳该是 %q，得到 %q", want, got)
	}
}

func Test时间戳按给定时区换算并写上它的名字(t *testing.T) {
	t.Parallel()

	shanghai := mustLoad(t, "Asia/Shanghai")
	got := FormatTimestamp(time.Date(2026, 3, 5, 14, 30, 9, 0, time.UTC), shanghai)
	want := "2026-03-05T22:30:09+08:00[Asia/Shanghai]"
	if got != want {
		t.Fatalf("时间戳该是 %q，得到 %q", want, got)
	}
}

func Test负偏移的时区排得出负号(t *testing.T) {
	t.Parallel()

	// 夏令时也一并验了：3 月 5 日的纽约还在 -05:00。
	newYork := mustLoad(t, "America/New_York")
	got := FormatTimestamp(time.Date(2026, 3, 5, 14, 30, 9, 0, time.UTC), newYork)
	want := "2026-03-05T09:30:09-05:00[America/New_York]"
	if got != want {
		t.Fatalf("时间戳该是 %q，得到 %q", want, got)
	}
}

func Test同一时区在夏令时前后排出不同的偏移(t *testing.T) {
	t.Parallel()

	newYork := mustLoad(t, "America/New_York")
	got := FormatTimestamp(time.Date(2026, 7, 5, 14, 30, 9, 0, time.UTC), newYork)
	want := "2026-07-05T10:30:09-04:00[America/New_York]"
	if got != want {
		t.Fatalf("时间戳该是 %q，得到 %q", want, got)
	}
}

func Test秒以下的部分被丢掉(t *testing.T) {
	t.Parallel()

	got := FormatTimestamp(time.Date(2026, 3, 5, 14, 30, 9, 987_000_000, time.UTC), time.UTC)
	want := "2026-03-05T14:30:09+00:00[UTC]"
	if got != want {
		t.Fatalf("时间戳该是 %q，得到 %q", want, got)
	}
}
