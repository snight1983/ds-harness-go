// 本文件的作用：这个包自己拥有的那条持久不变量——生命周期那一对必须在同一条日志里
// 靠 commandId 成双，而一条 command/done 指过去的那个权威事件必须真的指得到。
//
// 源: packages/interaction/commands/src/invariant.ts

package commands

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
// 源: packages/interaction/commands/src/invariant.ts:11
const PackageName = "@deepseek-ai/dsh-commands"

// Transition 是一条被接受的事件对配对状态的那一点改动。
//
// 验和改分成两步，和 [github.com/snight1983/ds-harness-go/interaction/userapproval.Trace] 的做法一致：
// 验的那一步是纯的，可以在事件真的提交之前先跑一遍，提交了才改状态。
type Transition struct {
	// ID 非空表示这条 command/run 要把这个号记成已经跑过。
	ID ID
}

// Trace 是一条日志走到当前为止、和命令生命周期有关的那点状态。
//
// 源: packages/interaction/commands/src/invariant.ts:22
type Trace struct {
	// runs 是已经见过 command/run 的那些配对号。
	runs map[ID]struct{}
	// seqs 记下走过的每一条事件的序号，值是「它是不是一条 command/* 生命周期事件」。
	//
	// 新增: DSH 直接按下标去索引 session.events 那个完整数组，因为它手上就有整条日志。
	// Go 这边这条轨迹是**流式**的（装载那一段和后续追加那一段接着走），手上没有数组，
	// 所以要自己记。只记一个 bool 而不是整条事件：sourceEventSeq 那三条检查要问的
	// 恰好只有「这个序号存在吗」和「它是不是生命周期事件」。
	//
	// 顺带把 DSH 那条 `sourceEvent?.seq !== source`（防数组下标和 seq 对不上）判掉了：
	// 这里本来就是按 seq 索引的。
	seqs map[int]bool
}

// NewTrace 造一条空的轨迹。
func NewTrace() *Trace {
	return &Trace{runs: map[ID]struct{}{}, seqs: map[int]bool{}}
}

// Runs 交出走到此刻见过多少个不同的配对号。
//
// 新增: DSH 那个 Set 摆在闭包里，测试够不着也不需要够得着。Go 这边 [Trace] 是一个
// 可以脱离注册表单独用的类型（离线校验一份日志），所以留一个只读的口子。
func (t *Trace) Runs() int { return len(t.runs) }

// Validate 验一条事件，交出它被接受之后要做的那点改动。
//
// 源: packages/interaction/commands/src/invariant.ts:23-47
//
// 新增: DSH 那边这个函数收一个 fail 回调。Go 这边返回**第一条**违例，好让它脱离
// 不变量注册表单独用；[RegisterInvariants] 只是把这个错误接到 [invariants.Fail] 上。
func (t *Trace) Validate(event session.Event) (Transition, error) {
	switch event.Type {
	case EventRun:
		var data RunData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return Transition{}, errors.New("command/run carries an unreadable payload")
		}
		if _, seen := t.runs[data.ID]; seen {
			return Transition{}, fmt.Errorf("command/run repeats commandId %s", strconv.Quote(string(data.ID)))
		}
		return Transition{ID: data.ID}, nil
	case EventDone:
		var data DoneData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return Transition{}, errors.New("command/done carries an unreadable payload")
		}
		if _, seen := t.runs[data.ID]; !seen {
			return Transition{}, fmt.Errorf(
				"command/done %s pairs no prior command/run in this log", strconv.Quote(string(data.ID)))
		}
		if data.SourceEventSeq != nil && !t.resolves(event, data) {
			return Transition{}, fmt.Errorf(
				"command/done %s has invalid sourceEventSeq %d",
				strconv.Quote(string(data.ID)), *data.SourceEventSeq)
		}
		return Transition{}, nil
	default:
		return Transition{}, nil
	}
}

// resolves 说明这条 command/done 指过去的那个权威事件指不指得到。
//
// 源: packages/interaction/commands/src/invariant.ts:37-46
//
// 四条一起成立才算数：这是一次成功、序号非负、排在这条 command/done 前面而且真的
// 在这条日志里、并且它自己不是一条 command/* 生命周期事件。最后一条挡的是「一条
// 命令记录把另一条命令记录当成自己的权威呈现」这种自指。
func (t *Trace) resolves(event session.Event, data DoneData) bool {
	source := *data.SourceEventSeq
	if data.Kind != ResultSuccess || source < 0 || source >= event.Seq {
		return false
	}
	lifecycle, exists := t.seqs[source]
	return exists && !lifecycle
}

// Apply 把一条已经验过、也已经提交了的事件算进这条轨迹。
//
// 源: packages/interaction/commands/src/invariant.ts:29-31
func (t *Trace) Apply(event session.Event, transition Transition) {
	t.seqs[event.Seq] = event.Type == EventRun || event.Type == EventDone
	if transition.ID != "" {
		t.runs[transition.ID] = struct{}{}
	}
}

// ValidateLog 把一整段日志走一遍，交出走完之后的轨迹或者第一条违例。
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

// RegisterInvariants 装上命令生命周期的配对检查，返回注销函数。
//
// 源: packages/interaction/commands/src/invariant.ts:20-65
//
// 两条胳膊，和 DSH 一样：装的时候把**已经装进来的**日志走一遍（一份历史里就带着
// 拆了对的生命周期的会话，必须在装载这一刻就响），然后订阅后续的追加。
//
// 新增: DSH 那两条胳膊都从 cordis 上拿。Go 里活会话服务是循环那一块的东西，本包在
// 第 4 块，所以这两条胳膊由装配方以函数交进来，做法和
// [github.com/snight1983/ds-harness-go/todo.RegisterInvariants] 逐字相同。
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
