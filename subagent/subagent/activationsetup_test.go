// 本文件的作用：那张可续孩子装配登记表的测试——安装次序、安装方失败时的回滚、
// 撤销与提交之间那道竞争、孩子作用域处置时的清理，以及释放失败怎么报。

package subagent

import (
	"context"
	"errors"
	"testing"

	"github.com/snight1983/ds-harness-go/core/scope"
)

// childScope 造一个当孩子用的作用域，用完自动释放。
func childScope(t *testing.T, label string) *scope.Scope {
	t.Helper()
	return keyedScope(t, label, nil)
}

// noRelease 是一份什么都不用释放的贡献。
func noRelease(record func()) ActivationSetupContribution {
	return func(context.Context, *scope.Scope) (func(context.Context) error, error) {
		record()
		return nil, nil
	}
}

func TestActivationSetupRegistryRejectsANilContribution(t *testing.T) {
	if _, err := NewActivationSetupRegistry().Register(nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil 贡献该被拒，实际 %v", err)
	}
}

// 每一份活着的贡献都按登记次序装进孩子作用域。
func TestApplyInstallsEveryLiveContributionInOrder(t *testing.T) {
	registry := NewActivationSetupRegistry()
	var installed []string
	for _, label := range []string{"一", "二", "三"} {
		name := label
		if _, err := registry.Register(noRelease(func() { installed = append(installed, name) })); err != nil {
			t.Fatalf("登记贡献失败：%v", err)
		}
	}

	commit, err := registry.Apply(context.Background(), childScope(t, "child"))
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	if err := commit(); err != nil {
		t.Fatalf("提交失败：%v", err)
	}
	if len(installed) != 3 || installed[0] != "一" || installed[2] != "三" {
		t.Fatalf("该按登记次序装，实际 %#v", installed)
	}
}

// 一份贡献自己失败时，那次失败保持权威，而这一批已经装上的每一项都要回滚到。
func TestApplyRollsBackWhenAContributionFails(t *testing.T) {
	registry := NewActivationSetupRegistry()
	refusal := errors.New("装不上")
	released := 0
	if _, err := registry.Register(func(context.Context, *scope.Scope) (func(context.Context) error, error) {
		return func(context.Context) error { released++; return nil }, nil
	}); err != nil {
		t.Fatalf("登记贡献失败：%v", err)
	}
	if _, err := registry.Register(func(context.Context, *scope.Scope) (func(context.Context) error, error) {
		return nil, refusal
	}); err != nil {
		t.Fatalf("登记贡献失败：%v", err)
	}

	if _, err := registry.Apply(context.Background(), childScope(t, "child")); !errors.Is(err, refusal) {
		t.Fatalf("安装方那次失败该保持权威，实际 %v", err)
	}
	if released != 1 {
		t.Fatalf("先装上那项该被回滚，实际释放了 %d 次", released)
	}
}

// 撤销一份贡献会把它已经装出去的每一次安装都放掉。
func TestUnregisterReleasesItsInstallations(t *testing.T) {
	registry := NewActivationSetupRegistry()
	released := 0
	remove, err := registry.Register(func(context.Context, *scope.Scope) (func(context.Context) error, error) {
		return func(context.Context) error { released++; return nil }, nil
	})
	if err != nil {
		t.Fatalf("登记贡献失败：%v", err)
	}

	commit, err := registry.Apply(context.Background(), childScope(t, "child"))
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	if err := commit(); err != nil {
		t.Fatalf("提交失败：%v", err)
	}
	if err := remove(context.Background()); err != nil {
		t.Fatalf("撤销失败：%v", err)
	}
	if released != 1 {
		t.Fatalf("撤销该放掉那次安装，实际释放了 %d 次", released)
	}
	// 撤销是幂等的。
	if err := remove(context.Background()); err != nil {
		t.Fatalf("重复撤销该是空操作，实际 %v", err)
	}
	if released != 1 {
		t.Fatalf("重复撤销不该再放一次，实际释放了 %d 次", released)
	}
}

// 一份已经撤掉的贡献不会再被装进后来的孩子。
func TestApplySkipsARemovedContribution(t *testing.T) {
	registry := NewActivationSetupRegistry()
	installed := 0
	remove, err := registry.Register(noRelease(func() { installed++ }))
	if err != nil {
		t.Fatalf("登记贡献失败：%v", err)
	}
	if err := remove(context.Background()); err != nil {
		t.Fatalf("撤销失败：%v", err)
	}

	if _, err := registry.Apply(context.Background(), childScope(t, "child")); err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	if installed != 0 {
		t.Fatalf("撤掉的贡献不该再被装，实际装了 %d 次", installed)
	}
}

// 这个孩子还在建的时候有人撤了一份贡献：那一批装配作废，提交时报出来，孩子立不起来。
func TestCommitFailsWhenAContributionIsRevokedMidCreation(t *testing.T) {
	registry := NewActivationSetupRegistry()
	if _, err := registry.Register(noRelease(func() {})); err != nil {
		t.Fatalf("登记贡献失败：%v", err)
	}
	remove, err := registry.Register(noRelease(func() {}))
	if err != nil {
		t.Fatalf("登记贡献失败：%v", err)
	}

	commit, err := registry.Apply(context.Background(), childScope(t, "child"))
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	if err := remove(context.Background()); err != nil {
		t.Fatalf("撤销失败：%v", err)
	}
	if err := commit(); codeOf(err) != CodeActivationSetupRevoked {
		t.Fatalf("提交该报 %s，实际 %v", CodeActivationSetupRevoked, err)
	}
}

// 一个安装方在它那条安装记录存在之前就把自己撤了：那条漏网的记录要当场处置掉。
func TestApplyDisposesAnEscapedInstallation(t *testing.T) {
	registry := NewActivationSetupRegistry()
	released := 0
	var remove func(context.Context) error
	remove, err := registry.Register(func(ctx context.Context, _ *scope.Scope) (func(context.Context) error, error) {
		// 同步重入：这一下跑在 record 之前，所以这次安装一记上就已经是漏网的了。
		if err := remove(ctx); err != nil {
			t.Errorf("重入撤销失败：%v", err)
		}
		return func(context.Context) error { released++; return nil }, nil
	})
	if err != nil {
		t.Fatalf("登记贡献失败：%v", err)
	}

	if _, err := registry.Apply(context.Background(), childScope(t, "child")); err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	if released != 1 {
		t.Fatalf("漏网那次安装该被当场处置，实际释放了 %d 次", released)
	}
}

// 一份贡献在这一批装配跑到半路时被别人撤掉：它已经进了那份快照，但那道闸要重新
// 读一次撤销状态，所以它不会再被装进这个孩子。
func TestApplySkipsAContributionRevokedMidApply(t *testing.T) {
	registry := NewActivationSetupRegistry()
	installed := 0
	var removeSecond func(context.Context) error
	if _, err := registry.Register(func(ctx context.Context, _ *scope.Scope) (func(context.Context) error, error) {
		// 同步撤掉排在自己后面的那一份：那一份已经在快照里了，只有这道闸拦得住它。
		if err := removeSecond(ctx); err != nil {
			t.Errorf("撤销后一份失败：%v", err)
		}
		return nil, nil
	}); err != nil {
		t.Fatalf("登记贡献失败：%v", err)
	}
	remove, err := registry.Register(noRelease(func() { installed++ }))
	if err != nil {
		t.Fatalf("登记贡献失败：%v", err)
	}
	removeSecond = remove

	if _, err := registry.Apply(context.Background(), childScope(t, "child")); err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	if installed != 0 {
		t.Fatalf("跑到半路被撤掉的贡献不该再被装，实际装了 %d 次", installed)
	}
}

// 漏网那次安装当场处置时**自己**又放不掉：那条释放失败是这次装配的结论，不能咽掉。
func TestApplyReportsAFailedEscapedRelease(t *testing.T) {
	registry := NewActivationSetupRegistry()
	broken := errors.New("放不掉")
	var remove func(context.Context) error
	remove, err := registry.Register(func(ctx context.Context, _ *scope.Scope) (func(context.Context) error, error) {
		if err := remove(ctx); err != nil {
			t.Errorf("重入撤销失败：%v", err)
		}
		return func(context.Context) error { return broken }, nil
	})
	if err != nil {
		t.Fatalf("登记贡献失败：%v", err)
	}

	if _, err := registry.Apply(context.Background(), childScope(t, "child")); !errors.Is(err, broken) {
		t.Fatalf("漏网那项放不掉该被抛上来，实际 %v", err)
	}
}

// 一次已经当场放掉的漏网安装仍旧留在这一批里；后面有人装不上时那次回滚会再指到它，
// 而它只能放一次。
func TestRollbackDoesNotReleaseAnEscapedInstallationTwice(t *testing.T) {
	registry := NewActivationSetupRegistry()
	released := 0
	var remove func(context.Context) error
	remove, err := registry.Register(func(ctx context.Context, _ *scope.Scope) (func(context.Context) error, error) {
		if err := remove(ctx); err != nil {
			t.Errorf("重入撤销失败：%v", err)
		}
		return func(context.Context) error { released++; return nil }, nil
	})
	if err != nil {
		t.Fatalf("登记贡献失败：%v", err)
	}
	refusal := errors.New("装不上")
	if _, err := registry.Register(func(context.Context, *scope.Scope) (func(context.Context) error, error) {
		return nil, refusal
	}); err != nil {
		t.Fatalf("登记贡献失败：%v", err)
	}

	if _, err := registry.Apply(context.Background(), childScope(t, "child")); !errors.Is(err, refusal) {
		t.Fatalf("安装方那次失败该保持权威，实际 %v", err)
	}
	if released != 1 {
		t.Fatalf("该恰好放一次，实际 %d 次", released)
	}
}

// 孩子的作用域一处置，它名下剩余的每一次安装都跟着放掉。
func TestDisposingTheChildScopeReleasesItsInstallations(t *testing.T) {
	registry := NewActivationSetupRegistry()
	released := 0
	if _, err := registry.Register(func(context.Context, *scope.Scope) (func(context.Context) error, error) {
		return func(context.Context) error { released++; return nil }, nil
	}); err != nil {
		t.Fatalf("登记贡献失败：%v", err)
	}

	child, err := scope.New(scope.NewKey("child"), scope.Options{})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	commit, err := registry.Apply(context.Background(), child)
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	if err := commit(); err != nil {
		t.Fatalf("提交失败：%v", err)
	}
	if err := child.Dispose(context.Background()); err != nil {
		t.Fatalf("处置孩子作用域失败：%v", err)
	}
	if released != 1 {
		t.Fatalf("孩子一处置该放掉那次安装，实际释放了 %d 次", released)
	}
}

// 释放失败要整批放完之后才报，而且原因仍旧认得出来。
func TestReleaseFailuresAreReportedAfterEveryAttempt(t *testing.T) {
	registry := NewActivationSetupRegistry()
	broken := errors.New("放不掉")
	attempts := 0
	for range 2 {
		if _, err := registry.Register(func(context.Context, *scope.Scope) (func(context.Context) error, error) {
			return func(context.Context) error { attempts++; return broken }, nil
		}); err != nil {
			t.Fatalf("登记贡献失败：%v", err)
		}
	}

	child, err := scope.New(scope.NewKey("child"), scope.Options{})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	t.Cleanup(func() { _ = child.Dispose(context.Background()) })
	commit, err := registry.Apply(context.Background(), child)
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	if err := commit(); err != nil {
		t.Fatalf("提交失败：%v", err)
	}

	err = registry.releaseChild(context.Background(), child)
	if codeOf(err) != CodeActivationSetupReleaseFailed {
		t.Fatalf("该报 %s，实际 %v", CodeActivationSetupReleaseFailed, err)
	}
	if !errors.Is(err, broken) {
		t.Fatalf("拼起来的错误该仍旧认得出原因，实际 %v", err)
	}
	if attempts != 2 {
		t.Fatalf("两项都该试到，实际试了 %d 次", attempts)
	}
}

// 一次安装恰好处置一次：孩子作用域处置和撤销贡献都指向它，只能有一边真的放。
func TestAnInstallationIsReleasedExactlyOnce(t *testing.T) {
	registry := NewActivationSetupRegistry()
	released := 0
	remove, err := registry.Register(func(context.Context, *scope.Scope) (func(context.Context) error, error) {
		return func(context.Context) error { released++; return nil }, nil
	})
	if err != nil {
		t.Fatalf("登记贡献失败：%v", err)
	}

	child, err := scope.New(scope.NewKey("child"), scope.Options{})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	commit, err := registry.Apply(context.Background(), child)
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	if err := commit(); err != nil {
		t.Fatalf("提交失败：%v", err)
	}
	if err := child.Dispose(context.Background()); err != nil {
		t.Fatalf("处置孩子作用域失败：%v", err)
	}
	if err := remove(context.Background()); err != nil {
		t.Fatalf("撤销失败：%v", err)
	}
	if released != 1 {
		t.Fatalf("该恰好放一次，实际 %d 次", released)
	}
}

// 孩子那个作用域已经释放了，这一批清理再也跑不到，所以当场放掉别让它泄漏。
func TestApplyReleasesWhenTheChildScopeIsAlreadyDisposed(t *testing.T) {
	registry := NewActivationSetupRegistry()
	released := 0
	if _, err := registry.Register(func(context.Context, *scope.Scope) (func(context.Context) error, error) {
		return func(context.Context) error { released++; return nil }, nil
	}); err != nil {
		t.Fatalf("登记贡献失败：%v", err)
	}

	child, err := scope.New(scope.NewKey("child"), scope.Options{})
	if err != nil {
		t.Fatalf("造作用域失败：%v", err)
	}
	if err := child.Dispose(context.Background()); err != nil {
		t.Fatalf("处置作用域失败：%v", err)
	}

	if _, err := registry.Apply(context.Background(), child); err == nil {
		t.Fatal("往一个已经处置的孩子上装该失败")
	}
	if released != 1 {
		t.Fatalf("装上去的那项该当场放掉，实际释放了 %d 次", released)
	}
}
