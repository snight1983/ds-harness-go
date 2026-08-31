// 本文件的作用：验配置的补默认值与校验。

package timecontext

import (
	"errors"
	"testing"
	"time"
)

func Test空配置补出UTC(t *testing.T) {
	t.Parallel()

	resolved, err := Config{}.Resolve()
	if err != nil {
		t.Fatalf("空配置应当解得开，却报了 %v", err)
	}
	if resolved.TimeZone != DefaultTimeZone {
		t.Fatalf("时区名该是 %q，得到 %q", DefaultTimeZone, resolved.TimeZone)
	}
	if resolved.Location == nil || resolved.Location.String() != DefaultTimeZone {
		t.Fatalf("解析出来的时区该是 %q，得到 %v", DefaultTimeZone, resolved.Location)
	}
	if resolved.RefreshInterval != 0 {
		t.Fatalf("默认间隔该是零，得到 %s", resolved.RefreshInterval)
	}
}

func Test配置原样带过给定的时区与间隔(t *testing.T) {
	t.Parallel()

	resolved, err := Config{TimeZone: "Asia/Shanghai", RefreshInterval: 90 * time.Second}.Resolve()
	if err != nil {
		t.Fatalf("这份配置应当解得开，却报了 %v", err)
	}
	if resolved.TimeZone != "Asia/Shanghai" {
		t.Fatalf("时区名该是 Asia/Shanghai，得到 %q", resolved.TimeZone)
	}
	if resolved.Location.String() != "Asia/Shanghai" {
		t.Fatalf("解析出来的时区该是 Asia/Shanghai，得到 %q", resolved.Location.String())
	}
	if resolved.RefreshInterval != 90*time.Second {
		t.Fatalf("间隔该原样带过来，得到 %s", resolved.RefreshInterval)
	}
}

func Test负的刷新间隔被拒(t *testing.T) {
	t.Parallel()

	_, err := Config{RefreshInterval: -time.Second}.Resolve()
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("负间隔该报 ErrInvalidConfig，得到 %v", err)
	}
}

func Test时区写成Local被拒(t *testing.T) {
	t.Parallel()

	_, err := Config{TimeZone: "Local"}.Resolve()
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Local 该报 ErrInvalidConfig，得到 %v", err)
	}
}

func Test解不开的时区名被拒(t *testing.T) {
	t.Parallel()

	_, err := Config{TimeZone: "Mars/Olympus_Mons"}.Resolve()
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("不存在的时区该报 ErrInvalidConfig，得到 %v", err)
	}
}
