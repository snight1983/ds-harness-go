// 本文件的作用：这个包自己拥有的那条持久不变量——一条 plan/mode 要长成什么样
// 才配进日志。
//
// 源: packages/plan/plan-mode/src/invariant.ts

package planmode

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/session"
)

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/plan/plan-mode/src/invariant.ts:9-10（name）
const PackageName = "@deepseek-ai/dsh-plan-mode"

// ValidateEvent 验一条事件里本包拥有的那些字段；不是 [EventMode] 就什么都不做。
//
// 源: packages/plan/plan-mode/src/invariant.ts:20-26
//
// 能查的只有负载形状这一条。[EventMode] 是一条独立的整值事件：回合之间选的那一下
// 在两个回合中间落盘，回合之内选的那一下在步骤边界落盘，所以它和回合之间**没有**
// 任何包含关系可查。
//
// 新增: DSH 那边这个函数收一个 `fail` 回调、违例时直接抛。Go 这边返回**第一条**
// 违例，和 [github.com/snight1983/ds-harness-go/todo.ValidateEvent] 一致：它因此可以脱离不变量注册表
// 单独用（离线校验一份日志、或者在写之前自己先验一遍），而 [RegisterInvariants]
// 只是把这个错误接到 [invariants.Fail] 上。
func ValidateEvent(event session.Event) error {
	if event.Type != EventMode {
		return nil
	}
	var payload struct {
		Active json.RawMessage `json:"active"`
	}
	if err := json.Unmarshal(event.Data, &payload); err != nil {
		return fmt.Errorf("plan/mode carries invalid active state %s; expected a boolean", quoteRaw(nil))
	}
	// 走 *bool 而不是 bool：Go 的 json 把 null 解进一个 bool 是**不报错**的（它什么
	// 都不做，留下零值 false），照写就等于把一条 active 是 null 的记录读成「关着」并
	// 放行。DSH 那边查的是 `typeof active !== 'boolean'`，null 在那里过不去。
	// [decodeMode] 出于同一个理由也走 *bool。
	var active *bool
	if err := json.Unmarshal(payload.Active, &active); err != nil || active == nil {
		return fmt.Errorf("plan/mode carries invalid active state %s; expected a boolean", quoteRaw(payload.Active))
	}
	return nil
}

// quoteRaw 把一段原始 JSON 写成能放进错误文本的样子。
//
// 报的是日志里那个**原样**的值：一条 active 是 42 的记录，说「invalid active
// state 42」比说「invalid active state false」更接近现场。
//
// 新增: 字段缺席时 DSH 那条模板串里的 JSON.stringify(undefined) 求出 undefined，
// 拼进去就是字面的 "undefined"。Go 这边写成 null，和
// [github.com/snight1983/ds-harness-go/todo] 那份同名帮手一致——"undefined" 是一句只有 JS 读者才认得的话。
func quoteRaw(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "null"
	}
	return string(raw)
}

// RegisterInvariants 装上计划模式那条形状检查，返回注销函数。
//
// 源: packages/plan/plan-mode/src/invariant.ts:29-48
//
// 两条胳膊，和 DSH 一样：装的时候把**已经装进来的**日志走一遍（一份历史里就带着
// 坏记录的会话，必须在装载这一刻就响，而不是等下一次追加），然后订阅后续的追加。
//
// 新增: DSH 那两条胳膊都从 cordis 上拿——ctx.sessions.list() 取历史，
// ctx.on('internal/dispatch') 截住后来的。Go 里活会话服务是循环那一块的东西，
// 本包拿不到它，所以这两条胳膊由装配方以函数交进来，做法和
// [github.com/snight1983/ds-harness-go/todo.RegisterInvariants] 逐字相同。
//
// subscribe 交回来的退订函数会登记进这次注册的 scope：注销之后，一条不该再查的
// 检查绝不许继续在别人的写路径上抛。
func RegisterInvariants(
	ctx context.Context,
	registry *invariants.Registry,
	loaded func() []session.Event,
	subscribe func(observer func(session.Event)) func(),
) (func(), error) {
	if registry == nil {
		return nil, fmt.Errorf("%w: 注册不变量需要一个不变量注册表", ErrInvalidConfig)
	}
	if loaded == nil {
		return nil, fmt.Errorf("%w: 注册不变量需要一条读出已装载日志的路", ErrInvalidConfig)
	}
	if subscribe == nil {
		return nil, fmt.Errorf("%w: 注册不变量需要一条订阅后续事件的路", ErrInvalidConfig)
	}

	install := func(_ context.Context, scope *invariants.Scope, fail invariants.Fail) error {
		for _, event := range loaded() {
			if err := ValidateEvent(event); err != nil {
				fail(err.Error())
			}
		}
		scope.Defer(subscribe(func(event session.Event) {
			if err := ValidateEvent(event); err != nil {
				fail(err.Error())
			}
		}))
		return nil
	}

	return registry.Register(ctx, PackageName, install)
}
