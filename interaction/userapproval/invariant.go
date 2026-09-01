// 本文件的作用：这个包自己拥有的那条持久不变量——审计那一对必须成双、必须被回合
// 圈住，而且两条 approval/* 事件里的封闭词汇表不许出现表外的值。
//
// 源: packages/interaction/user-approval/src/invariant.ts

package userapproval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/session"
)

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/interaction/user-approval/src/invariant.ts:9
const PackageName = "@deepseek-ai/dsh-user-approval"

// Transition 是一条被接受的事件对配对状态的那一点改动。
//
// 源: packages/interaction/user-approval/src/invariant.ts:17-19
//
// 验和改分成两步，和 [session.Trace] 的做法一致：验的那一步是纯的，可以在事件真的
// 提交之前先跑一遍（DSH 那边就是靠 internal/dispatch 做这件事的），提交了才改状态。
type Transition struct {
	// ID 是这条事件动到的那次询问；空串表示这条事件什么都没动。
	ID RequestID
	// Opens 为真表示这是一次提问（把 ID 记成未结算），否则是一次结算（把 ID 划掉）。
	Opens bool
}

// Trace 是一条日志走到当前为止、和审批有关的那点状态。
//
// 源: packages/interaction/user-approval/src/invariant.ts:21-24
type Trace struct {
	openTurn bool
	pending  map[RequestID]struct{}
}

// NewTrace 造一条空的轨迹：还没进任何回合，也没有未结算的询问。
func NewTrace() *Trace {
	return &Trace{pending: map[RequestID]struct{}{}}
}

// Pending 交出此刻还没结算的那些询问的条数。
//
// 新增: DSH 那个 Set 直接摆在闭包里，测试够不着也不需要够得着。Go 这边它是一个
// 可以脱离注册表单独用的类型（离线校验一份日志），所以留一个只读的口子，
// 好让调用方在走完一段日志之后问一句「还有没有挂着的」。
func (t *Trace) Pending() int { return len(t.pending) }

// Validate 验一条事件，交出它被接受之后要做的那点改动。
//
// 源: packages/interaction/user-approval/src/invariant.ts:26-50
//
// 新增: DSH 那边这个函数收一个 fail 回调、一条事件里能报几条就报几条。Go 这边返回
// **第一条**违例，和 [session.Trace.Validate] 一致：它因此可以脱离不变量注册表单独用，
// 而 [RegisterInvariants] 只是把这个错误接到 [invariants.Fail] 上。
//
// 回合起止在这里一并吃掉，因为「有没有开着的回合」正是前两条检查要问的事。
func (t *Trace) Validate(event session.Event) (Transition, error) {
	switch event.Type {
	case session.EventTurnStart, session.EventTurnEnd:
		return Transition{}, nil
	case EventAsked:
		var data AskedData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return Transition{}, errors.New("approval/asked carries an unreadable payload")
		}
		if !t.openTurn {
			return Transition{}, errors.New("approval/asked appended outside any open turn")
		}
		if data.ToolName == "" {
			return Transition{}, errors.New("approval/asked toolName must be non-empty")
		}
		if _, open := t.pending[data.ID]; open {
			return Transition{}, fmt.Errorf("approval/asked repeated open id %s", strconv.Quote(string(data.ID)))
		}
		return Transition{ID: data.ID, Opens: true}, nil
	case EventDecided:
		var data DecidedData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return Transition{}, errors.New("approval/decided carries an unreadable payload")
		}
		if !t.openTurn {
			return Transition{}, errors.New("approval/decided appended outside any open turn")
		}
		if _, open := t.pending[data.ID]; !open {
			return Transition{}, fmt.Errorf(
				"approval/decided has no matching approval/asked for id %s", strconv.Quote(string(data.ID)))
		}
		if !KnownOutcome(data.Outcome) {
			return Transition{}, fmt.Errorf(
				"approval/decided carries unknown outcome %s", strconv.Quote(string(data.Outcome)))
		}
		return Transition{ID: data.ID}, nil
	case EventPolicy:
		var data PolicyData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return Transition{}, errors.New("approval/policy carries an unreadable payload")
		}
		if !KnownPolicy(data.Policy) {
			return Transition{}, fmt.Errorf(
				"approval/policy carries unknown policy %s", strconv.Quote(string(data.Policy)))
		}
		return Transition{}, nil
	default:
		return Transition{}, nil
	}
}

// Apply 把一条已经验过、也已经提交了的事件算进这条轨迹。
//
// 源: packages/interaction/user-approval/src/invariant.ts:52-56, 80-88
//
// 回合起止在这里改，而不是在 [Trace.Validate] 里改：验的那一步是纯的。
func (t *Trace) Apply(event session.Event, transition Transition) {
	switch event.Type {
	case session.EventTurnStart:
		t.openTurn = true
	case session.EventTurnEnd:
		t.openTurn = false
	}
	if transition.ID == "" {
		return
	}
	if transition.Opens {
		t.pending[transition.ID] = struct{}{}
		return
	}
	delete(t.pending, transition.ID)
}

// ValidateLog 把一整段日志走一遍，交出走完之后的轨迹或者第一条违例。
//
// 源: packages/interaction/user-approval/src/invariant.ts:64-74（那段 seed）
func ValidateLog(events []session.Event) (*Trace, error) {
	trace := NewTrace()
	for _, event := range events {
		transition, err := trace.Validate(event)
		if err != nil {
			return nil, err
		}
		trace.Apply(event, transition)
	}
	return trace, nil
}

// RegisterInvariants 装上审批审计流的配对与词汇表检查，返回注销函数。
//
// 源: packages/interaction/user-approval/src/invariant.ts:58-111
//
// 两条胳膊，和 DSH 一样：装的时候把**已经装进来的**日志走一遍（一份历史里就带着
// 拆了对的审计的会话，必须在装载这一刻就响），然后订阅后续的追加。
//
// 新增: DSH 那两条胳膊都从 cordis 上拿——ctx.sessions.list() 取历史，
// ctx.on('internal/dispatch') 截住后来的。Go 里活会话服务是循环那一块的东西，
// 本包在第 4 块，所以这两条胳膊由装配方以函数交进来，做法和
// [github.com/snight1983/ds-harness-go/todo.RegisterInvariants] 逐字相同。
//
// 后续那条胳膊上的轨迹是**接着**装载时那条往下走的，所以一条 approval/decided 认得出
// 它那条写在装载之前的 approval/asked。DSH 靠一张按 Session 索引的 WeakMap 做同一件事。
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
		trace := NewTrace()
		check := func(event session.Event) {
			transition, err := trace.Validate(event)
			if err != nil {
				fail(err.Error())
				return
			}
			trace.Apply(event, transition)
		}
		for _, event := range loaded() {
			check(event)
		}
		scope.Defer(subscribe(check))
		return nil
	}

	return registry.Register(ctx, PackageName, install)
}
