// 本文件的作用：验人工压缩那条可预期的失败在 Go 这一侧还能按分类分派。

package compaction

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestManualError按分类取出来(t *testing.T) {
	t.Parallel()

	// DSH 那边是一个 `extends Error` 的类，靠 instanceof 认。Go 这边调用方
	// 用 errors.As 取出 Code 再分派——所以包了几层之后还得取得出来。
	cause := errors.New("上游 502")
	wrapped := fmt.Errorf("这一步没做成：%w", NewManualError(ManualErrorSummary, "总结请求失败", cause))

	var manual *ManualError
	if !errors.As(wrapped, &manual) {
		t.Fatalf("取不出来：%v", wrapped)
	}
	if manual.Code != ManualErrorSummary {
		t.Fatalf("分类是 %q", manual.Code)
	}
	if manual.Message != "总结请求失败" {
		t.Fatalf("诊断被改写成了 %q", manual.Message)
	}
	if !errors.Is(wrapped, cause) {
		t.Fatal("原本那条失败查不下去了")
	}
}

func TestManualError没有原因时也能用(t *testing.T) {
	t.Parallel()

	manual := NewManualError(ManualErrorBusy, "已经有一次压缩占着", nil)
	if errors.Unwrap(manual) != nil {
		t.Fatal("凭空多出一条原因")
	}
	if !strings.Contains(manual.Error(), string(ManualErrorBusy)) {
		t.Fatalf("那句话里看不出分类：%s", manual.Error())
	}
	if !strings.Contains(manual.Error(), "已经有一次压缩占着") {
		t.Fatalf("那句话里看不出诊断：%s", manual.Error())
	}
}

func TestManualErrorCode是一张封闭的单子(t *testing.T) {
	t.Parallel()

	// 这六个取值会**原样进人工命令的结果**，上层照着它们写提示语。
	// 改动其中任何一个都是一次对外行为的变更。
	for got, want := range map[ManualErrorCode]string{
		ManualErrorBusy:        "busy",
		ManualErrorCancelled:   "cancelled",
		ManualErrorChanged:     "changed",
		ManualErrorSummary:     "summary",
		ManualErrorCommit:      "commit",
		ManualErrorPersistence: "persistence",
	} {
		if string(got) != want {
			t.Fatalf("分类排成了 %q，要的是 %q", got, want)
		}
	}
}

func TestTrigger是那两种(t *testing.T) {
	t.Parallel()

	if TriggerPressure != "pressure" || TriggerContextOverflow != "context-overflow" {
		t.Fatalf("触发原因是 %q 和 %q", TriggerPressure, TriggerContextOverflow)
	}
}
