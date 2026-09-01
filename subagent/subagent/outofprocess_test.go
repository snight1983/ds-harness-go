// 本文件的作用：跨进程后端那一侧词汇的测试——诊断截断、能力申报、时限与工作目录
// 这两道校验、那次绝不报错的结果结清，以及那个幂等的运行句柄。

package subagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/snight1983/ds-harness-go/llm"
)

// ---- 诊断截断 ----

func TestLimitDiagnosticLeavesAShortStringAlone(t *testing.T) {
	if limited := limitDiagnostic("放得下的东西"); limited != "放得下的东西" {
		t.Fatalf("放得下就该原样交回，实际 %q", limited)
	}
}

// 截断之后必须压得住上限，而且那个标记要看得见。
func TestLimitDiagnosticCapsAndMarksALongString(t *testing.T) {
	limited := limitDiagnostic(strings.Repeat("a", MaxDiagnosticBytes+1))
	if len(limited) > MaxDiagnosticBytes {
		t.Fatalf("截完该压在 %d 字节以内，实际 %d", MaxDiagnosticBytes, len(limited))
	}
	if !strings.HasSuffix(limited, diagnosticTruncationSuffix) {
		t.Fatalf("该带上那个看得见的标记，实际结尾是 %q", limited[len(limited)-len(diagnosticTruncationSuffix):])
	}
}

// 那一刀不许劈开一个 UTF-8 序列——三字节的字符正好让边界落不到整数刀口上。
func TestLimitDiagnosticNeverSplitsARune(t *testing.T) {
	limited := limitDiagnostic(strings.Repeat("世", MaxDiagnosticBytes))
	if !utf8.ValidString(limited) {
		t.Fatal("截完该仍旧是合法的 UTF-8")
	}
	if len(limited) > MaxDiagnosticBytes {
		t.Fatalf("截完该压在 %d 字节以内，实际 %d", MaxDiagnosticBytes, len(limited))
	}
}

// ---- 能力申报 ----

// 一个在别的进程里的孩子兑现不了任何开工期特性，而且交回来的那份谁改都影响不到别人。
func TestNoStartCapabilitiesDeclaresNothingAndIsNotShared(t *testing.T) {
	declared := NoStartCapabilities()
	if declared != (Capabilities{}) {
		t.Fatalf("跨进程后端该一样能力都不申报，实际 %#v", declared)
	}
	declared.Persona = true
	if NoStartCapabilities().Persona {
		t.Fatal("改一份申报不该动到下一次交回的那份")
	}
}

// ---- 时限 ----

func TestAssertPositiveTimeoutRejectsANonPositiveDuration(t *testing.T) {
	for name, value := range map[string]time.Duration{
		"零":  0,
		"负数": -time.Second,
	} {
		t.Run(name, func(t *testing.T) {
			if err := AssertPositiveTimeout("后端", "拆解时限", value); err == nil {
				t.Fatal("非正的时长该被拒")
			}
		})
	}
	if err := AssertPositiveTimeout("后端", "拆解时限", time.Second); err != nil {
		t.Fatalf("正的时长该收下，实际 %v", err)
	}
}

// ---- 工作目录 ----

func TestAssertUsableCwdRejectsARelativePath(t *testing.T) {
	if err := AssertUsableCwd("后端", "cwd", filepath.Join("relative", "path")); err == nil {
		t.Fatal("相对路径该被拒")
	}
}

func TestAssertUsableCwdRejectsAnythingThatIsNotAnEnterableDirectory(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("造文件失败：%v", err)
	}

	for name, path := range map[string]string{
		"不存在": filepath.Join(root, "从来没有过"),
		"是文件": file,
	} {
		t.Run(name, func(t *testing.T) {
			if err := AssertUsableCwd("后端", "cwd", path); err == nil {
				t.Fatal("这条路径当不了 cwd，该被拒")
			}
		})
	}
	if err := AssertUsableCwd("后端", "cwd", root); err != nil {
		t.Fatalf("一个现存的目录该收下，实际 %v", err)
	}
}

// 空串就是本仓库通行的「没给」，不是一次失败。
func TestValidateConfiguredCwdTreatsTheEmptyStringAsAbsent(t *testing.T) {
	resolved, err := ValidateConfiguredCwd("后端", "")
	if err != nil || resolved != "" {
		t.Fatalf("没给该交回空串，实际 %q err=%v", resolved, err)
	}
}

// 配置里那一项按宿主的启动目录解释，交回来的一定是绝对路径。
func TestValidateConfiguredCwdResolvesARelativeOverride(t *testing.T) {
	resolved, err := ValidateConfiguredCwd("后端", ".")
	if err != nil {
		t.Fatalf("当前目录该验得过，实际 %v", err)
	}
	if !filepath.IsAbs(resolved) {
		t.Fatalf("交回来的该是绝对路径，实际 %q", resolved)
	}
}

func TestValidateConfiguredCwdRejectsAnUnusableOverride(t *testing.T) {
	if _, err := ValidateConfiguredCwd("后端", filepath.Join(t.TempDir(), "从来没有过")); err == nil {
		t.Fatal("配了个不存在的目录该被拒")
	}
}

// 配了覆盖就用它——那一项在装载时已经验过，开工这一刻不重验。
func TestResolveChildCwdPrefersTheConfiguredOverride(t *testing.T) {
	resolved, err := ResolveChildCwd("后端", testAbsolutePath, t.TempDir())
	if err != nil {
		t.Fatalf("解算失败：%v", err)
	}
	if resolved != testAbsolutePath {
		t.Fatalf("该用配置里那一项，实际 %q", resolved)
	}
}

// 没配覆盖就用父会话那个工作区，而且在这里验。
func TestResolveChildCwdFallsBackToTheParentWorkspace(t *testing.T) {
	parent := t.TempDir()
	resolved, err := ResolveChildCwd("后端", "", parent)
	if err != nil {
		t.Fatalf("解算失败：%v", err)
	}
	if resolved != parent {
		t.Fatalf("该用父会话那个工作区，实际 %q", resolved)
	}

	if _, err := ResolveChildCwd("后端", "", filepath.Join(parent, "从来没有过")); err == nil {
		t.Fatal("父会话那个 cwd 立不住时该被拒")
	}
}

// 两样都没有时大声失败：退回宿主进程的 cwd 会把孩子悄悄绑在服务的启动目录上。
func TestResolveChildCwdRefusesToFallBackToTheHostProcess(t *testing.T) {
	if _, err := ResolveChildCwd("后端", "", ""); err == nil {
		t.Fatal("一个都没有时该失败，而不是退回宿主进程的 cwd")
	}
}

// ---- 结果结清 ----

func TestSettleRunResultPassesACleanAttemptThrough(t *testing.T) {
	settled := SettleRunResult(context.Background(), RunResultSettlement{
		Attempt: func(context.Context) (Result, error) {
			return Result{Output: textContent("跑完了"), StopReason: StopCompleted}, nil
		},
		CollectOutput: func() llm.Content { return textContent("兜底") },
		Cancelled:     func() bool { return false },
	})
	if settled.StopReason != StopCompleted || textOf(settled.Output) != "跑完了" {
		t.Fatalf("该原样交回那次尝试的结局，实际 %#v", settled)
	}
}

// 本地取消先结清了，那次尝试自己怎么结束的就不算数了——两种结局都摊成 StopAborted，
// 输出换成提供方当下拿得出来的那份快照。
func TestSettleRunResultReportsAbortWhenCancellationSettledFirst(t *testing.T) {
	for name, attempt := range map[string]func(context.Context) (Result, error){
		"尝试跑完了": func(context.Context) (Result, error) {
			return Result{Output: textContent("跑完了"), StopReason: StopCompleted}, nil
		},
		"尝试失败了": func(context.Context) (Result, error) {
			return Result{}, errors.New("传输断了")
		},
	} {
		t.Run(name, func(t *testing.T) {
			reported := false
			settled := SettleRunResult(context.Background(), RunResultSettlement{
				Attempt:       attempt,
				CollectOutput: func() llm.Content { return textContent("到此为止") },
				Cancelled:     func() bool { return true },
				OnError:       func(error, StopReason) { reported = true },
			})
			if settled.StopReason != StopAborted {
				t.Fatalf("该摊成 %s，实际 %s", StopAborted, settled.StopReason)
			}
			if textOf(settled.Output) != "到此为止" {
				t.Fatalf("该换成那份快照，实际 %q", textOf(settled.Output))
			}
			if reported {
				t.Fatal("被取消赢下的结清不该走那个诊断出口")
			}
		})
	}
}

// 发布之后的传输失败摊成 StopError，诊断留住并且照样受上限管。
func TestSettleRunResultFlattensAFailureIntoAnError(t *testing.T) {
	broken := errors.New("传输断了")
	var reportedErr error
	var reportedStop StopReason

	settled := SettleRunResult(context.Background(), RunResultSettlement{
		Attempt:           func(context.Context) (Result, error) { return Result{}, broken },
		CollectOutput:     func() llm.Content { return textContent("到此为止") },
		CollectDiagnostic: func() string { return strings.Repeat("b", MaxDiagnosticBytes+1) },
		Cancelled:         func() bool { return false },
		OnError: func(err error, stop StopReason) {
			reportedErr, reportedStop = err, stop
		},
	})

	if settled.StopReason != StopError {
		t.Fatalf("该摊成 %s，实际 %s", StopError, settled.StopReason)
	}
	if textOf(settled.Output) != "到此为止" {
		t.Fatalf("该带上那份快照，实际 %q", textOf(settled.Output))
	}
	if len(settled.Diagnostic) > MaxDiagnosticBytes {
		t.Fatalf("诊断该受上限管，实际 %d 字节", len(settled.Diagnostic))
	}
	if !errors.Is(reportedErr, broken) || reportedStop != StopError {
		t.Fatalf("那个诊断出口该收到原因和停止原因，实际 %v／%s", reportedErr, reportedStop)
	}
}

// 不给收集诊断的那一项就不带诊断，而不是带一段空字符串。
func TestSettleRunResultOmitsTheDiagnosticWhenNoneIsCollected(t *testing.T) {
	settled := SettleRunResult(context.Background(), RunResultSettlement{
		Attempt:       func(context.Context) (Result, error) { return Result{}, errors.New("断了") },
		CollectOutput: func() llm.Content { return nil },
		Cancelled:     func() bool { return false },
	})
	if settled.Diagnostic != "" {
		t.Fatalf("不给就该没有诊断，实际 %q", settled.Diagnostic)
	}
}

// 那个诊断出口自己炸了不许把这次运行结果搅黄。
func TestSettleRunResultContainsAPanickingErrorHook(t *testing.T) {
	settled := SettleRunResult(context.Background(), RunResultSettlement{
		Attempt:       func(context.Context) (Result, error) { return Result{}, errors.New("断了") },
		CollectOutput: func() llm.Content { return textContent("到此为止") },
		Cancelled:     func() bool { return false },
		OnError:       func(error, StopReason) { panic("诊断出口炸了") },
	})
	if settled.StopReason != StopError || textOf(settled.Output) != "到此为止" {
		t.Fatalf("诊断出口炸了不该动到这次结清，实际 %#v", settled)
	}
}

// ---- 运行句柄 ----

func TestSubprocessRunHasNoLocalAgent(t *testing.T) {
	run := NewSubprocessRun(SubprocessRunParts{
		ID:            "child",
		Result:        func(context.Context) (Result, error) { return Result{}, nil },
		RequestCancel: func() {},
		Teardown:      func(context.Context) error { return nil },
	})
	if run.ID() != "child" {
		t.Fatalf("该交回那个运行 id，实际 %q", run.ID())
	}
	if run.LocalAgent() != nil {
		t.Fatal("一个在别的进程里的孩子不该有本地 agent")
	}
}

func TestSubprocessRunDelegatesTheResult(t *testing.T) {
	run := NewSubprocessRun(SubprocessRunParts{
		ID: "child",
		Result: func(context.Context) (Result, error) {
			return Result{StopReason: StopCompleted, Output: textContent("跑完了")}, nil
		},
		RequestCancel: func() {},
		Teardown:      func(context.Context) error { return nil },
	})
	settled, err := run.Result(context.Background())
	if err != nil {
		t.Fatalf("取结果失败：%v", err)
	}
	if textOf(settled.Output) != "跑完了" {
		t.Fatalf("该原样转交，实际 %q", textOf(settled.Output))
	}
}

// 处置是幂等的：那把拆解梯子只走一遍，后到的处置方拿同一个结局。
func TestSubprocessRunDisposesExactlyOnce(t *testing.T) {
	var cancels, teardowns atomic.Int32
	broken := errors.New("拆不干净")
	run := NewSubprocessRun(SubprocessRunParts{
		ID:            "child",
		Result:        func(context.Context) (Result, error) { return Result{}, nil },
		RequestCancel: func() { cancels.Add(1) },
		Teardown: func(context.Context) error {
			teardowns.Add(1)
			return broken
		},
	})

	first := run.Dispose(context.Background())
	second := run.Dispose(context.Background())
	if !errors.Is(first, broken) || !errors.Is(second, broken) {
		t.Fatalf("两次都该拿到同一个结局，实际 %v／%v", first, second)
	}
	if cancels.Load() != 1 || teardowns.Load() != 1 {
		t.Fatalf("取消和拆解各该只走一遍，实际 %d／%d", cancels.Load(), teardowns.Load())
	}
}
