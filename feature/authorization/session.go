// 本文件的作用：一次跑起来的尝试——交给流程的那个 [Session]、续租的那条心跳，
// 以及「凭据在这次尝试里被提交过」这一件事的观察。
//
// 源: packages/credentials/authorization/src/index.ts:300-437（那个 attempt 闭包）

package authorization

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/snight1983/ds-harness-go/storage/domain"
)

// run 是一次正在本副本上跑的尝试。
//
// 源: packages/credentials/authorization/src/index.ts:300-437
//
// 它同时是交给流程的那个 [Session]：DSH 那边是一个现造的对象字面量，Go 里让同一个
// 结构体实现接口，省掉一层转发。
type run struct {
	service     *Service
	interaction Interaction
	// id、key、method 是这次尝试的身份，认领之后就不再变。
	id     AttemptID
	key    keyString
	method string
	// resumed 是认领时读到的脚印，只读。
	resumed json.RawMessage

	// cancel 撤掉交给 [Flow.Run] 的那个 ctx。
	cancel context.CancelFunc

	mu sync.Mutex
	// committed 记这次尝试里观察到过一次凭据记录提交。
	//
	// 源: packages/credentials/authorization/src/index.ts:404-412（observed.committed）
	committed bool
	// declined 记人拒绝过某一个问题。
	//
	// 源: packages/credentials/authorization/src/index.ts:389-396（observed.declined）
	declined bool
	// withdrawn 记介质上那条记录被标了撤销。
	withdrawn bool
	// fenced 记这次尝试已经不归本副本了，见 [CodeTakenOver]。
	fenced bool
}

// keyString 是凭据键当记录键用时的那个字符串。
//
// 新增: 记录键在本包里就是凭据键本身。取一个别名是为了让「这里用的是键的字符串
// 形态」在签名上看得见——[storage] 的记录键允许是任意字符串
// （storage/backend.go:263），所以带斜杠的凭据键直接当键用是合法的。
type keyString = string

// Method 实现 [Session]。
func (r *run) Method() string { return r.method }

// Resumed 实现 [Session]。
func (r *run) Resumed() json.RawMessage { return r.resumed }

// Notify 实现 [Session]：发了就不管。
//
// 源: packages/credentials/authorization/src/index.ts:373-380
//
// 界面自己炸了不算这次尝试出事——一个渲染不出通告的页面丢的是这条通告。
func (r *run) Notify(notice Notice) {
	defer func() {
		if recovered := recover(); recovered != nil {
			r.service.warnf("凭据 %q 的授权通告把界面炸了：%v", r.key, recovered)
		}
	}()
	r.interaction.Notify(notice)
}

// Prompt 实现 [Session]。
//
// 源: packages/credentials/authorization/src/index.ts:381-400
//
// 三种收场分得开：人拒绝记在 [run.declined] 上并原样交回那个错（结算成
// [StatusCancelled]），这次尝试被撤或被接手当场返回，别的错一律读成界面坏了。
func (r *run) Prompt(ctx context.Context, prompt Prompt) (string, error) {
	if err := prompt.Validate(); err != nil {
		return "", err
	}
	if err := r.checkLive(); err != nil {
		return "", err
	}
	answer, err := r.interaction.Prompt(ctx, prompt)
	if err != nil {
		if errors.Is(err, CodeDeclined) {
			r.mu.Lock()
			r.declined = true
			r.mu.Unlock()
		}
		return "", err
	}
	return answer, nil
}

// Checkpoint 实现 [Session]：写脚印、续租、把撤销读回来，一次往返做完三件事。
//
// 新增: 本包独有，理由见 [Session.Checkpoint]。三件事合成一次
// [github.com/snight1983/ds-harness-go/storage/domain.Table.Update] 而不是三次读写，
// 是因为它们本来就该是原子的：一次「写完脚印才发现自己已经被接手了」会把脚印盖到
// 别人的记录上。
func (r *run) Checkpoint(ctx context.Context, progress json.RawMessage) error {
	if err := r.checkLive(); err != nil {
		return err
	}
	return r.renew(ctx, progress)
}

// checkLive 说这次尝试还该不该往下跑。
func (r *run) checkLive() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fenced {
		return newError(CodeTakenOver, "凭据 %q 的这次授权尝试已经归别的副本了", r.key)
	}
	if r.withdrawn {
		return newError(CodeDeclined, "凭据 %q 的这次授权尝试被撤了", r.key)
	}
	return nil
}

// renew 续一次租，顺带写脚印、顺带把撤销读回来。
//
// progress 为 nil 表示只续租不写脚印——心跳走的就是这条。
func (r *run) renew(ctx context.Context, progress json.RawMessage) error {
	now := r.service.now()
	var (
		fenced    bool
		withdrawn bool
	)
	_, err := r.service.table.Update(ctx, r.key, func(current Attempt) (Attempt, error) {
		fenced = current.ID != r.id || current.Holder != r.service.replica
		if fenced {
			return current, nil
		}
		withdrawn = current.Cancelled
		current.HeldUntil = now.Add(r.service.lease)
		current.UpdatedAt = now
		if progress != nil {
			current.Progress = progress
		}
		return current, nil
	})
	switch {
	case isDomainCode(err, domain.CodeMissingKey):
		// 记录没了：要么被 [Service.Cancel] 在没人攥着的时候收掉了，要么被别的
		// 副本接手之后结算掉了。两种都是「这条记录不再归我」，处置一样。
		r.markFenced()
		return newError(CodeTakenOver, "凭据 %q 的这次授权尝试已经不在了", r.key)
	case err != nil:
		return wrapError(CodeStoreFailed, err, "凭据 %q 的这次授权尝试续租没写下去", r.key)
	}
	if fenced {
		r.markFenced()
		return newError(CodeTakenOver, "凭据 %q 的这次授权尝试已经归别的副本了", r.key)
	}
	if withdrawn {
		r.markWithdrawn()
		return newError(CodeDeclined, "凭据 %q 的这次授权尝试被撤了", r.key)
	}
	return nil
}

// markFenced 记下「这次尝试不归本副本了」，并撤掉流程的 ctx。
func (r *run) markFenced() {
	r.mu.Lock()
	r.fenced = true
	r.mu.Unlock()
	r.cancel()
}

// markWithdrawn 记下「这次尝试被撤了」，并撤掉流程的 ctx。
func (r *run) markWithdrawn() {
	r.mu.Lock()
	r.withdrawn = true
	r.mu.Unlock()
	r.cancel()
}

// markCommitted 记下「这次尝试里观察到一次凭据记录提交」。
//
// 源: packages/credentials/authorization/src/index.ts:404-412
func (r *run) markCommitted() {
	r.mu.Lock()
	r.committed = true
	r.mu.Unlock()
}

// snapshot 读一份当下的观察结果。
func (r *run) snapshot() (committed, declined, withdrawn, fenced bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.committed, r.declined, r.withdrawn, r.fenced
}

// heartbeat 每隔租期的三分之一续一次租，直到 ctx 结束。
//
// 新增: 本包独有。三分之一是为了在一次续租失败之后还剩两次机会——租期内续不上
// 才算真的没了，而一次介质抖动不该让一次正在等人输入的授权被别人接手。
//
// 续租报出来的介质失败只记日志不停手：一次读不动的介质在下一个周期可能就好了，
// 而真的被接手是靠**读到别人的 Holder** 认出来的，不是靠读不动。
func (r *run) heartbeat(ctx context.Context) {
	interval := r.service.lease / 3
	if interval <= 0 {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			err := r.renew(ctx, nil)
			switch {
			case err == nil, errors.Is(err, CodeTakenOver), errors.Is(err, CodeDeclined):
				// 前两种已经在 renew 里撤过 ctx 了，下一轮 select 就退出。
			default:
				r.service.warnf("凭据 %q 的授权尝试续租没成：%v", r.key, err)
			}
		}
	}
}
