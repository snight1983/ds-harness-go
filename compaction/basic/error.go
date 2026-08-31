// 本文件的作用：本包会报的两类失败——配置不合法，以及其中「某个路由的压力
// 参数配不出来」那一小类（它要单独认得出来，因为调用方对它只警告一次）。
//
// 源: packages/compaction/compaction-basic/src/config.ts:52-60

package basic

import (
	"errors"
	"fmt"
)

// ErrInvalidConfig 表示这一层的配置不合法。
//
// 新增: DSH 那边配置校验一律抛裸 Error，靠文案区分。Go 里没有异常，做成哨兵值
// 是为了让调用方用 errors.Is 判定，而不是去匹配错误文案。
// [TargetPressureError] 也裹在它下面——那是这一类里的一个子集，不是另一类。
var ErrInvalidConfig = errors.New("compaction/basic: 配置不合法")

// TargetPressureError 是「某个路由的压力参数算不出来」这一类配置失败。
//
// 源: packages/compaction/compaction-basic/src/config.ts:52-60
//
// 它单独有一个类型，是因为自动压缩那一侧会在**每一个步骤边界**上重算一次压力，
// 而配错了的参数每次都会以同样的方式失败。调用方按 [TargetPressureError.TargetKey]
// 记住已经警告过的路由，同一条配置错误只吵一次。
//
// 新增: DSH 是 `class TargetPressureConfigError extends Error`，靠 instanceof 认。
// Go 这边调用方用 errors.As 取出来读 TargetKey；同时它 Unwrap 成
// [ErrInvalidConfig]，所以「这是不是一条配置错误」仍然只需要一次 errors.Is。
type TargetPressureError struct {
	// TargetKey 是出问题的那条路由，形如 "provider/model"。
	TargetKey string
	// Message 是这条失败该怎么改的说明。
	Message string
}

// Error 交出这条失败的说明。
func (e *TargetPressureError) Error() string {
	return fmt.Sprintf("%s：%s", ErrInvalidConfig, e.Message)
}

// Unwrap 让这条失败同时满足 errors.Is(err, [ErrInvalidConfig])。
func (e *TargetPressureError) Unwrap() error { return ErrInvalidConfig }

// targetPressureFailure 造一条 [TargetPressureError]。
func targetPressureFailure(targetKey string, format string, args ...any) *TargetPressureError {
	return &TargetPressureError{TargetKey: targetKey, Message: fmt.Sprintf(format, args...)}
}

// configFailure 造一条普通的配置失败。
func configFailure(format string, args ...any) error {
	return fmt.Errorf("%w：%s", ErrInvalidConfig, fmt.Sprintf(format, args...))
}
