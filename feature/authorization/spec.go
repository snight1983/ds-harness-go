// 本文件的作用：一次授权尝试落在介质上的样子，以及本包打开的那份 [domain.Spec]。
//
// 新增: DSH 那边没有对应文件——它的尝试活在一个 Promise 里，单飞槽位是一个内存
// Map（packages/credentials/authorization/src/index.ts:180-196）。落到介质上是本包
// 的分歧点，理由见包文档。

package authorization

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/snight1983/ds-harness-go/credentials"
	"github.com/snight1983/ds-harness-go/storage/domain"
)

// 域的身份：名字、格式版本、那张唯一的表。
//
// 版本从 1 起数：这个域没有 DSH 对应物，所以没有要对齐的历史版本号。
const (
	// DomainName 是这个域在存储中枢上的名字。
	DomainName = "authorization"
	// DomainVersion 是这个域的格式版本。
	DomainVersion = 1
	// TableName 是那张尝试记录表的名字。
	TableName = "attempts"
)

// AttemptID 是一次授权尝试的身份。
//
// 它由 [Config.NewAttemptID] 生成，只在本包内部有意义：调用方拿它去
// [Service.Resume] 或者 [Service.Cancel]，别的什么都做不了。
type AttemptID string

// Attempt 是一次授权尝试落盘时的样子。
//
// **记录键就是凭据键**，所以「一个凭据键上同时只有一次尝试」是介质自己保证的，
// 不需要另外一张索引：[domain.Table.Create] 在键已存在时返回
// storage.CodeStaleRevision，那一次 [Service.Begin] 就输了。
type Attempt struct {
	// ID 是这次尝试的身份。
	ID AttemptID `json:"id"`
	// Key 是这次尝试在授权哪条凭据记录，等于记录键。
	Key credentials.Key `json:"key"`
	// Method 是调用方挑中的那个方式标识。
	//
	// 它跟着记录走而不是跟着请求走：接手的那个副本重跑 [Flow.Run] 时得知道
	// 上一次挑的是哪种方式，否则一次「设备码」的尝试会被接成「浏览器回调」。
	Method string `json:"method"`
	// Holder 是此刻攥着这次尝试的那个副本，见 [Config.Replica]。
	Holder string `json:"holder"`
	// HeldUntil 是这份租约什么时候到期。
	//
	// 攥着它的副本每隔租期的三分之一续一次；过了这个时刻还没续上，就是那个副本
	// 没了，别的副本可以接手。用绝对时刻而不是「最后一次心跳 + 租期」，是为了让
	// 读的人不必知道租期是多少——一条记录自己说得清自己什么时候过期。
	HeldUntil time.Time `json:"heldUntil"`
	// Cancelled 是「有人撤了这次尝试」这件事写在介质上的样子。
	//
	// 跨副本的撤销只能这么传：撤的那一位和跑的那一位可能不在同一个进程里，
	// 没有别的通道。攥着它的那个副本在下一次 [Session.Prompt] 或
	// [Session.Checkpoint] 时读到它，然后停手。
	Cancelled bool `json:"cancelled"`
	// Progress 是流程自己留下的脚印，由 [Session.Checkpoint] 写，
	// [Session.Resumed] 读。本包不解释它的内容。
	Progress json.RawMessage `json:"progress,omitempty"`
	// StartedAt 是这次尝试起来的时刻。
	StartedAt time.Time `json:"startedAt"`
	// UpdatedAt 是最后一次落盘写入的时刻。
	UpdatedAt time.Time `json:"updatedAt"`
}

// Held 说这份租约在 now 这个时刻还活着没有。
//
// 边界取「到期即不再攥着」：一份刚好到期的租约让接手方等下一个时刻毫无意义，
// 而两边都判成「活着」会让接手永远轮不上。
func (a Attempt) Held(now time.Time) bool { return now.Before(a.HeldUntil) }

// Validate 校验一条尝试记录。
//
// 这里只查这一份值自己说不说得通。「这个凭据键上有没有流程认领」那类跨表检查
// 在 [Service] 里，因为域这一层的校验函数拿不到登记册。
func (a Attempt) Validate() error {
	if a.ID == "" {
		return fmt.Errorf("授权尝试没有身份")
	}
	if a.Key == "" {
		return fmt.Errorf("授权尝试没有凭据键")
	}
	if _, err := credentials.ParseKey(string(a.Key)); err != nil {
		return fmt.Errorf("授权尝试的凭据键 %q 不合法：%w", a.Key, err)
	}
	if a.Method == "" {
		return fmt.Errorf("授权尝试没有说用哪种方式")
	}
	if a.Holder == "" {
		return fmt.Errorf("授权尝试没有说归哪个副本")
	}
	if a.HeldUntil.IsZero() {
		return fmt.Errorf("授权尝试没有租约到期时刻")
	}
	if a.StartedAt.IsZero() {
		return fmt.Errorf("授权尝试没有起始时刻")
	}
	if a.UpdatedAt.IsZero() {
		return fmt.Errorf("授权尝试没有更新时刻")
	}
	return nil
}

// Spec 是授权这个域的静态声明：一张 attempts 表，没有全局槽。
//
// 没有全局槽是有意的：这个域的全部状态就是「此刻有哪几次尝试」，而那正好是表
// 自己。加一个全局槽只会多出一份要跟表对齐的东西。
//
// 新增: 是函数不是包级变量，理由同
// [github.com/snight1983/ds-harness-go/feature/workspace.Spec]——[domain.Spec]
// 里带着切片，一个包级变量会让所有调用方共用同一个底层数组。
func Spec() domain.Spec {
	return domain.Spec{
		Name:    DomainName,
		Version: DomainVersion,
		Tables: []domain.TableSpec{
			domain.DefineTable(TableName, func(attempt Attempt) error {
				return attempt.Validate()
			}),
		},
	}
}
