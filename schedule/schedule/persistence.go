// 本文件的作用：本包对那道共享落盘屏障的用法——为什么每一次改动前后都要过一次，
// 以及过不去时报的那个错。
//
// 源: packages/schedule/schedule/src/persistence.ts

package schedule

import (
	"context"

	coresession "github.com/snight1983/ds-harness-go/core/session"
)

// Sessions 是本包要用的那一小块会话仓库能力。
//
// 新增: DSH 直接拿整个 `ctx.sessions`。这里只声明用得着的那一个方法——本包对会话
// 仓库的全部需求就是「把当前这段前缀落一次盘」，写成一个窄接口之后，测试里替一个
// 假的进来不需要造一台真仓库。
type Sessions interface {
	// Flush 跑一次共享的落盘检查点，交回「有没有监听者真的做了落盘的活儿」。
	Flush(ctx context.Context, session *coresession.Session) (bool, error)
}

// PersistenceError 是「没能证明当前这段活前缀到达了某个落盘监听者」。
//
// 源: packages/schedule/schedule/src/persistence.ts:6-16
//
// 它的正文是给运维看的，所以是中文：模型那一侧看到的是
// [CodePersistenceUncertain] 加上一句固定的英文（见 tools.go 里的
// persistenceError），那句话告诉模型该怎么办（重跑一次 schedule_list 再说），
// 而不是告诉它落盘为什么没成。
type PersistenceError struct {
	// cause 是屏障自己报的那个错，没有就是 nil。
	cause error
}

// Error 让它成为一个 error。
func (e *PersistenceError) Error() string { return "schedule: 落盘检查点没走完" }

// Unwrap 交出屏障报的那个原因。
func (e *PersistenceError) Unwrap() error { return e.cause }

// flushPersistence 要求走成一次共享落盘检查点。
//
// 源: packages/schedule/schedule/src/persistence.ts:18-31
//
// 「有监听者做了活儿」和「没人监听」在这里是两回事：没人监听时 Flush 交回 false，
// 那说明这次改动根本没有落盘的去处，本包宁可当场报不确定，也不能让模型以为一条
// 提醒已经存住了——它下一次开会话时会发现那条提醒不见了。
func flushPersistence(ctx context.Context, sessions Sessions, session *coresession.Session) error {
	flushed, err := sessions.Flush(ctx, session)
	if err != nil {
		return &PersistenceError{cause: err}
	}
	if !flushed {
		return &PersistenceError{}
	}
	return nil
}
