// 本文件的作用：验读数正文那三行、耗时的排法，以及读数带的那个来源。

package timecontext

import (
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/llm"
)

func Test第一步的读数写着模型可见消息那个基线(t *testing.T) {
	t.Parallel()

	got := RenderText(Reading{
		Now:         at(3600),
		Turn:        1,
		Step:        1,
		Previous:    at(3570),
		HasPrevious: true,
	}, time.UTC)
	want := "Time sampled while preparing turn 1, step 1: 1970-01-01T01:00:00+00:00[UTC]\n" +
		"Time zone for this request: UTC. Interpret otherwise-unqualified dates and times in this zone.\n" +
		"Elapsed since the preceding model-visible message: 30s."
	if got != want {
		t.Fatalf("正文该是：\n%s\n实际是：\n%s", want, got)
	}
}

func Test后续步骤的读数写着步骤上下文那个基线(t *testing.T) {
	t.Parallel()

	got := RenderText(Reading{
		Now:         at(3600),
		Turn:        2,
		Step:        4,
		Previous:    at(3599),
		HasPrevious: true,
	}, time.UTC)
	want := "Time sampled while preparing turn 2, step 4: 1970-01-01T01:00:00+00:00[UTC]\n" +
		"Time zone for this request: UTC. Interpret otherwise-unqualified dates and times in this zone.\n" +
		"Elapsed since the preceding step context: 1s."
	if got != want {
		t.Fatalf("正文该是：\n%s\n实际是：\n%s", want, got)
	}
}

func Test没有基线时耗时写成unavailable(t *testing.T) {
	t.Parallel()

	got := RenderText(Reading{Now: at(0), Turn: 1, Step: 1}, time.UTC)
	want := "Time sampled while preparing turn 1, step 1: 1970-01-01T00:00:00+00:00[UTC]\n" +
		"Time zone for this request: UTC. Interpret otherwise-unqualified dates and times in this zone.\n" +
		"Elapsed since the preceding model-visible message: unavailable."
	if got != want {
		t.Fatalf("正文该是：\n%s\n实际是：\n%s", want, got)
	}
}

func Test第二行写的是配进来的那个时区(t *testing.T) {
	t.Parallel()

	shanghai := mustLoad(t, "Asia/Shanghai")
	got := RenderText(Reading{Now: at(0), Turn: 1, Step: 1}, shanghai)
	want := "Time sampled while preparing turn 1, step 1: 1970-01-01T08:00:00+08:00[Asia/Shanghai]\n" +
		"Time zone for this request: Asia/Shanghai. Interpret otherwise-unqualified dates and times in this zone.\n" +
		"Elapsed since the preceding model-visible message: unavailable."
	if got != want {
		t.Fatalf("正文该是：\n%s\n实际是：\n%s", want, got)
	}
}

func Test耗时的每一段只在非零时出现(t *testing.T) {
	t.Parallel()

	cases := []struct {
		elapsed time.Duration
		want    string
	}{
		{0, "0s"},
		{999 * time.Millisecond, "0s"},
		{30 * time.Second, "30s"},
		{90 * time.Second, "1m 30s"},
		{time.Hour, "1h 0s"},
		{time.Hour + 90*time.Second, "1h 1m 30s"},
		{25 * time.Hour, "1d 1h 0s"},
		{49*time.Hour + 61*time.Second, "2d 1h 1m 1s"},
		{-3 * time.Second, "0s"},
	}
	for _, one := range cases {
		if got := formatDuration(one.elapsed); got != one.want {
			t.Errorf("%s 该排成 %q，得到 %q", one.elapsed, one.want, got)
		}
	}
}

func Test读数的来源原样带上那段正文(t *testing.T) {
	t.Parallel()

	source := ReadingSource("读数正文")
	if source.Plugin != PluginName {
		t.Fatalf("署名该是 %q，得到 %q", PluginName, source.Plugin)
	}
	snapshot, ok := source.Context.(llm.SnapshotContext)
	if !ok {
		t.Fatalf("来源里的上下文形态该是快照，得到 %T", source.Context)
	}
	if len(snapshot.Sections) != 1 {
		t.Fatalf("快照该正好一节，得到 %d 节", len(snapshot.Sections))
	}
	if snapshot.Sections[0].Name != PluginName || snapshot.Sections[0].Text != "读数正文" {
		t.Fatalf("那一节该是 %q/%q，得到 %q/%q",
			PluginName, "读数正文", snapshot.Sections[0].Name, snapshot.Sections[0].Text)
	}
}
