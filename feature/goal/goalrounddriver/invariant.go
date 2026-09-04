// 本文件的作用：本包自己拥有的那条不变量——日志里任何一条自动续推消息，内容都必须
// 和它前面那段耐久目标状态重排出来的提示词逐字节相同。
//
// 源: packages/goal/goal-round-driver/src/invariant.ts

package goalrounddriver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/snight1983/ds-harness-go/feature/goal"
	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// ValidateStream 验一整条会话日志；守住了就交回 nil。
//
// 源: packages/goal/goal-round-driver/src/invariant.ts:47-61、63-77
//
// 它守的是一件很具体的事：**每一条自动续推消息都得是本包按当时那份目标状态写出来
// 的那一条**。有人事后往日志里塞一条带 goal 来源的假消息，或者本包自己哪天把提示词
// 悄悄改了却没跟着改这条不变量，两种情况都在这里响。
//
// 判定方式是重排一遍再逐字节比：这也是 [RenderRoundPrompt] 必须保持纯函数的原因。
//
// 新增: DSH 那个 install 收一个 fail 回调、就地报掉。这里交回错误，理由同
// [github.com/snight1983/ds-harness-go/feature/goal.ValidateStream]——它因此可以脱离注册表单独用，而
// [RegisterInvariants] 只是把这个错接到 [invariants.Fail] 上。
//
// 新增: 折叠是**增量**的，DSH 那边在检查已装载会话时对每条事件重折一遍前缀。
// 换成累加器只是把 O(n²) 变成 O(n)，判定结果一模一样：本包在验第 k 条事件时看到的
// 状态，和把前 k-1 条从头折一遍得到的完全相同（见 [goal.ApplyEvent]）。
func ValidateStream(events []sessionlog.Event) error {
	state := goal.EmptyFoldState()
	for _, event := range events {
		if err := validateEvent(state, event); err != nil {
			return err
		}
		if err := goal.ApplyEvent(state, event); err != nil {
			// 这条流本身就折不动。那不归本包管——[github.com/snight1983/ds-harness-go/feature/goal] 那条
			// 不变量会为同一条日志报出更准的话，这里放行免得同一件事响两遍。
			return nil
		}
	}
	return nil
}

// validateEvent 拿一条候选事件跟它前面那段耐久状态对一遍。
//
// 源: packages/goal/goal-round-driver/src/invariant.ts:47-61
//
// 三道闸依次放过：不是用户消息、读不回来、不是一次自动轮次。走到第四步才真验。
func validateEvent(prior *goal.FoldState, event sessionlog.Event) error {
	if event.Type != sessionlog.EventUserMessage {
		return nil
	}
	var data sessionlog.UserMessageData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return nil
	}
	source, ok := roundSource(data.Message.Source)
	if !ok {
		return nil
	}
	view, err := reconstructView(prior, source)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(data.Message.Content, RenderRoundPrompt(view, source.Round)) {
		return fmt.Errorf(
			"goalrounddriver: 第 %d 轮那条续推消息的内容不是本包排出来的那一条",
			source.Round,
		)
	}
	return nil
}

// reconstructView 从前面那段耐久状态重建这一轮当时该看到的那份视图。
//
// 源: packages/goal/goal-round-driver/src/invariant.ts:29-44
//
// 五条全得对上：那时候得有一个目标、它得是 active、身份和修订号得就是这条来源说的
// 那一份、这一轮得正好接在上一轮后面、而且它没有越过轮数上限。差一条就说明这条消息
// 不可能是本包在那个位置写出来的。
//
// 新增: DSH 还另外检查 createdAt/updatedAt 是不是 undefined。Go 这边它们是
// [goal.Folded] 上的 int64，只在 Goal 非 nil 时有意义——那个条件已经被第一条盖住了。
//
// 活化一律填 [goal.Armed]：它从不落盘，而能走到这一步的消息当时必然是点着的。
func reconstructView(prior *goal.FoldState, source goal.Source) (*goal.View, error) {
	folded := prior.Folded()
	if folded.Goal == nil || folded.Goal.Phase != goal.PhaseActive ||
		folded.Goal.ID != source.GoalID || folded.Goal.Revision != source.Revision ||
		source.Round != folded.RoundsStarted+1 || source.Round > folded.Goal.MaxGoalRounds {
		return nil, fmt.Errorf(
			"goalrounddriver: 第 %d 轮续推重建不出来——它前面那段耐久目标状态对不上",
			source.Round,
		)
	}
	return &goal.View{
		Snapshot:      *folded.Goal,
		RoundsStarted: folded.RoundsStarted,
		CreatedAt:     folded.CreatedAt,
		UpdatedAt:     folded.UpdatedAt,
		Activation:    goal.Armed,
	}, nil
}

// RegisterInvariants 装上这条不变量，返回注销函数。
//
// 源: packages/goal/goal-round-driver/src/invariant.ts:77-84
//
// 两条胳膊，形状和 [github.com/snight1983/ds-harness-go/feature/goal.RegisterInvariants] 逐字相同：装的时候
// 把已经装载进来的每一条流走一遍，然后订阅后续。装配方同样要负责在一条待写的
// user/message 落盘**之前**把它接在末尾交进来——少了这一步，一条伪造的续推会先进
// 日志再在下次装载时才炸，那时候已经改不掉了。
func RegisterInvariants(
	ctx context.Context,
	registry *invariants.Registry,
	loaded func() [][]sessionlog.Event,
	subscribe func(observer func([]sessionlog.Event)) func(),
) (func(), error) {
	switch {
	case registry == nil:
		return nil, errors.New("goalrounddriver: 注册不变量需要一个不变量注册表")
	case loaded == nil:
		return nil, errors.New("goalrounddriver: 注册不变量需要一条读出已装载日志的路")
	case subscribe == nil:
		return nil, errors.New("goalrounddriver: 注册不变量需要一条订阅后续流的路")
	}

	install := func(_ context.Context, scope *invariants.Scope, fail invariants.Fail) error {
		check := func(events []sessionlog.Event) {
			if err := ValidateStream(events); err != nil {
				fail(err.Error())
			}
		}
		for _, events := range loaded() {
			check(events)
		}
		scope.Defer(subscribe(check))
		return nil
	}

	return registry.Register(ctx, PackageName, install)
}
