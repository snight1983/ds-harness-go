// 本文件的作用：这个包自己拥有的那条关系不变量——一条 webhook 放行进来的提示词，
// 落进日志的那一刻，它那个会话必须已经归属在它自己那个工作区名下。
//
// 源: packages/webhook/webhook/src/invariant.ts

package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/webhook/webhook/src/invariant.ts:10
const PackageName = "@deepseek-ai/dsh-webhook"

// Ownership 是判这条不变量时那一刻的归属事实。
//
// 新增: DSH 在这条检查里现调 `ctx.workspaceRegistry.list()`，于是这条不变量
// 依赖整张工作区表。本包只要三个数，所以由调用方把它们取好交进来——好处有两个：
// [Workspaces] 那个接口因此只剩一个 Create 方法，而 [ValidateAdmission] 变成一个
// 纯函数，脱开不变量注册表也能单独用。
type Ownership struct {
	// SessionID 是这条日志属于哪个会话。
	SessionID sessionlog.SessionID
	// WorkspaceID 是它会话头上记的那个工作区。
	//
	// 新增: DSH 比的是 `session.header.cwd` 和工作区的 path 两条路径。Go 的会话头
	// 记的是一个不透明归属标识（见
	// [github.com/snight1983/ds-harness-go/sessionlog.SessionHeader.WorkspaceID]），
	// 所以这里比的是两个 id。
	WorkspaceID sessionlog.WorkspaceID
	// Owners 是此刻把这个会话记在自己账目里的那些工作区。
	Owners []sessionlog.WorkspaceID
}

// ValidateAdmission 验一条事件：如果它放行了 webhook 来的提示词，那这条会话必须
// 恰好归属在一个工作区名下，而且就是它会话头记的那一个。
//
// 源: packages/webhook/webhook/src/invariant.ts:18-40
//
// 不是一条 [github.com/snight1983/ds-harness-go/harness/agent.EventInboxSpliced]、
// 或者这一批插进去的消息里没有一条是本包发的，就什么都不做。
//
// 新增: DSH 那边这个函数收一个 fail 回调、违例时直接调它。Go 这边返回第一条违例，
// 理由和 [github.com/snight1983/ds-harness-go/feature/interaction/permissionpresets.Service.ValidateEvent]
// 逐字相同：它因此可以脱离不变量注册表单独用。
func ValidateAdmission(event sessionlog.Event, ownership Ownership) error {
	if !isAdmission(event) {
		return nil
	}
	if ownership.WorkspaceID == "" {
		return fmt.Errorf("webhook Session %s has no workspace", strconv.Quote(string(ownership.SessionID)))
	}
	if len(ownership.Owners) != 1 {
		return fmt.Errorf("webhook Session %s belongs to %d Workspaces at prompt admission",
			strconv.Quote(string(ownership.SessionID)), len(ownership.Owners))
	}
	if ownership.Owners[0] != ownership.WorkspaceID {
		return fmt.Errorf("webhook Session %s names workspace %s but is owned by %s",
			strconv.Quote(string(ownership.SessionID)),
			strconv.Quote(string(ownership.WorkspaceID)),
			strconv.Quote(string(ownership.Owners[0])))
	}
	return nil
}

// isAdmission 说这条事件是不是「放行了至少一条本包发的提示词」。
//
// 源: packages/webhook/webhook/src/invariant.ts:24-27
func isAdmission(event sessionlog.Event) bool {
	if event.Type != agent.EventInboxSpliced {
		return false
	}
	var data agent.SplicedData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		// 读不回来的负载不归这条不变量管：agent 那一层自己有
		// [github.com/snight1983/ds-harness-go/harness/agent.ErrMalformedEvent]，
		// 在这儿再报一遍只会让同一件事响两次、而且说的还不如那边准。
		return false
	}
	for _, message := range data.Inserted {
		if plugin, ok := message.Source.(llm.PluginSource); ok && plugin.Plugin == Plugin {
			return true
		}
	}
	return false
}

// RegisterInvariants 装上那条归属检查，返回注销函数。
//
// 源: packages/webhook/webhook/src/invariant.ts:22-48
//
// 两条胳膊，和 DSH 一样：装的时候把已经装进来的日志走一遍，然后订阅后续的追加。
// 装配那两条胳膊、以及那份归属事实，都由调用方以函数交进来，理由和
// [github.com/snight1983/ds-harness-go/feature/interaction/permissionpresets.RegisterInvariants]
// 逐字相同。
//
// ownershipOf 在**每一次**判定时现取一遍：一个会话被挂到别的工作区上不写这条日志，
// 拿装载那一刻的快照去判后来的事件，会把一次真的归属漂移放过去。
func RegisterInvariants(
	ctx context.Context,
	registry *invariants.Registry,
	ownershipOf func() (Ownership, error),
	loaded func() []sessionlog.Event,
	subscribe func(observer func(sessionlog.Event)) func(),
) (func(), error) {
	switch {
	case registry == nil:
		return nil, fmt.Errorf("%w：注册不变量需要一个不变量注册表", ErrInvalidConfig)
	case ownershipOf == nil:
		return nil, fmt.Errorf("%w：注册不变量需要一条读出当下归属事实的路", ErrInvalidConfig)
	case loaded == nil:
		return nil, fmt.Errorf("%w：注册不变量需要一条读出已装载日志的路", ErrInvalidConfig)
	case subscribe == nil:
		return nil, fmt.Errorf("%w：注册不变量需要一条订阅后续事件的路", ErrInvalidConfig)
	}

	install := func(_ context.Context, invariantScope *invariants.Scope, fail invariants.Fail) error {
		check := func(event sessionlog.Event) {
			// 归属事实在这里现取，但只在真的要判的时候取：绝大多数事件都不是
			// 一次 webhook 放行，为它们各跑一次归属查询是白花的。
			if !isAdmission(event) {
				return
			}
			ownership, err := ownershipOf()
			if err != nil {
				fail(fmt.Sprintf("webhook: 取不到这条会话的归属事实：%v", err))
				return
			}
			if err := ValidateAdmission(event, ownership); err != nil {
				fail(err.Error())
			}
		}
		for _, event := range loaded() {
			check(event)
		}
		invariantScope.Defer(subscribe(check))
		return nil
	}

	return registry.Register(ctx, PackageName, install)
}
