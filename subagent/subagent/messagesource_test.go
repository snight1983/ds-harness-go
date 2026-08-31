// 本文件的作用：续接这条线上那三种消息归属的测试——各自的形态、共有的那一项，
// 以及「这是不是本包的来源」那道判别。

package subagent

import (
	"encoding/json"
	"errors"
	"testing"

	"ds-harness-go/llm"
)

func TestSourceFactoriesCarryTheirForm(t *testing.T) {
	coordinator, err := NewCoordinatorSource("parent")
	if err != nil {
		t.Fatalf("造协调方来源失败：%v", err)
	}
	report, err := NewReportSource("child")
	if err != nil {
		t.Fatalf("造汇报来源失败：%v", err)
	}
	settled, err := NewSettledSource("child", "孩子做完了")
	if err != nil {
		t.Fatalf("造结清来源失败：%v", err)
	}

	// 前两条是「另一个 agent 说的话」，所以是 relay。
	for name, source := range map[string]llm.PluginSource{
		CoordinatorPlugin: coordinator,
		ReportPlugin:      report,
	} {
		if _, relay := source.Context.(llm.RelayContext); !relay {
			t.Fatalf("%s 该是 relay 形态，实际 %#v", name, source.Context)
		}
	}
	notice, ok := settled.Context.(llm.NoticeContext)
	if !ok {
		t.Fatalf("结清陈述该是 notice 形态，实际 %#v", settled.Context)
	}
	if string(notice.Summary) != "孩子做完了" {
		t.Fatalf("那句一行陈述该原样留着，实际 %q", notice.Summary)
	}
}

// 汇报和结清**有意**是两个名字：并成一个会让一段抄本把孩子从没写过的话记在它头上。
func TestReportAndSettledAreDistinctPlugins(t *testing.T) {
	if ReportPlugin == SettledPlugin {
		t.Fatal("汇报和结清不该共用一个来源名")
	}
}

func TestSourceFactoriesRequireASender(t *testing.T) {
	if _, err := NewCoordinatorSource(""); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("没有发送方该被拒，实际 %v", err)
	}
	if _, err := NewReportSource(""); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("没有发送方该被拒，实际 %v", err)
	}
	if _, err := NewSettledSource("", "话"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("没有发送方该被拒，实际 %v", err)
	}
}

func TestSenderSessionIDOfReadsEachOfTheThree(t *testing.T) {
	for name, build := range map[string]func() (llm.PluginSource, error){
		CoordinatorPlugin: func() (llm.PluginSource, error) { return NewCoordinatorSource("发信人") },
		ReportPlugin:      func() (llm.PluginSource, error) { return NewReportSource("发信人") },
		SettledPlugin:     func() (llm.PluginSource, error) { return NewSettledSource("发信人", "话") },
	} {
		t.Run(name, func(t *testing.T) {
			source, err := build()
			if err != nil {
				t.Fatalf("造来源失败：%v", err)
			}
			sender, mine, err := SenderSessionIDOf(source)
			if err != nil || !mine || sender != "发信人" {
				t.Fatalf("该读出发信人，实际 sender=%q mine=%v err=%v", sender, mine, err)
			}
		})
	}
}

// 不是本包这三种的来源一律报「不是我的」，而不是报错。
func TestSenderSessionIDOfIgnoresOtherSources(t *testing.T) {
	for name, source := range map[string]llm.MessageSource{
		"别的插件": llm.PluginSource{Plugin: "别人", Context: llm.RelayContext{}},
		"内建来源": llm.UserSource{},
	} {
		t.Run(name, func(t *testing.T) {
			sender, mine, err := SenderSessionIDOf(source)
			if err != nil || mine || sender != "" {
				t.Fatalf("该报不是本包的来源，实际 sender=%q mine=%v err=%v", sender, mine, err)
			}
		})
	}
}

// 名字对上了、负载却读不出来是坏数据，不能悄悄当成「不是我的」。
func TestSenderSessionIDOfRejectsAnUnreadableExtra(t *testing.T) {
	source := llm.PluginSource{
		Plugin:  ReportPlugin,
		Context: llm.RelayContext{},
		Extra:   json.RawMessage(`{"senderSessionId":`),
	}
	if _, _, err := SenderSessionIDOf(source); err == nil {
		t.Fatal("读不出来的负载该报错")
	}
}
