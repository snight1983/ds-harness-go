// 本文件的作用：把这个包那三种失败的**外形**钉住——哪一句话给谁看、哪一个原因
// 藏在里面只能靠 [errors.Unwrap] 取、以及那份封闭码表怎么对上。
//
// # 这些测试防的是什么错
//
//   - **给运维的中文漏到模型那一侧**。[LogError] 和 [PersistenceError] 的正文里带着
//     日志内部的形状；模型只该看到一个码加一句固定的英文。这两句话的分工一旦被谁
//     顺手统一掉，泄漏是静悄悄发生的。
//   - **[InputError] 的原因被拼进给模型的话里**。cause 是诊断，不是这次失败面向模型
//     的那一部分，所以它不导出，只能 Unwrap。
//   - **码表被接错**。[LogError] 只可能是 [CodeCorruptLog]；接成别的，工具那一侧
//     那套按码分流的处理会静静走错一支。

package schedule

import (
	"errors"
	"testing"
)

func TestLogErrorCarriesTheCorruptLogCode(t *testing.T) {
	failure := &LogError{Reason: "delete 指向一个不活着的 id"}
	if failure.Code() != CodeCorruptLog {
		t.Fatalf("接的码是 %q", failure.Code())
	}
	if failure.Error() != failure.Reason {
		t.Fatalf("正文是 %q", failure.Error())
	}
}

func TestInputErrorKeepsItsCauseOutOfTheModelFacingMessage(t *testing.T) {
	cause := errors.New("加载 Asia/Shanghai 失败")
	failure := wrapInputError(CodeInvalidRule, "at must be a valid instant.", cause)
	if failure.Error() != "at must be a valid instant." {
		t.Fatalf("给模型的那句话是 %q", failure.Error())
	}
	if !errors.Is(failure, cause) {
		t.Fatal("那个原因取不回来了")
	}

	// 不带原因的那一种：Unwrap 交回 nil，而不是一个包着 nil 的壳。
	plain := newInputError(CodeInvalidPrompt, "prompt must be non-empty after trimming.")
	if errors.Unwrap(plain) != nil {
		t.Fatalf("不带原因的那种 Unwrap 出了 %v", errors.Unwrap(plain))
	}
}

func TestPersistenceErrorSaysTheSameThingWithOrWithoutACause(t *testing.T) {
	// 那句正文是固定的：屏障为什么没走完是**诊断**，只能 Unwrap 取。两种情况说同
	// 一句话，是为了让调用方没法靠正文去猜里面装的是什么。
	cause := errors.New("盘满了")
	withCause := &PersistenceError{cause: cause}
	plain := &PersistenceError{}
	if withCause.Error() != plain.Error() {
		t.Fatalf("两种情况说的不是同一句话：%q / %q", withCause.Error(), plain.Error())
	}
	if !errors.Is(withCause, cause) {
		t.Fatal("屏障报的那个原因取不回来了")
	}
	if errors.Unwrap(plain) != nil {
		t.Fatalf("没有原因的那种 Unwrap 出了 %v", errors.Unwrap(plain))
	}
}

func TestEventTypesAnnouncesExactlyTheOneOwnedType(t *testing.T) {
	// 装配方拿这份清单往会话词汇表里加。多报一个类型等于替别人认领，少报一个等于
	// 本包写下去的事件在校验那一层被拒。
	types := EventTypes()
	if len(types) != 1 || types[0] != EventChange {
		t.Fatalf("报出去的类型是 %v", types)
	}
}
