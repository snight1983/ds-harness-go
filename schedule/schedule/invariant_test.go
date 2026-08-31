// 本文件的作用：把那条整流不变量钉在它真会出错的边上——验的到底是一条事件还是
// 整条流、装的那一刻已经装载进来的流验不验、订阅那条胳膊注销之后还查不查，以及
// 三个协作者少给一个时是当场报错还是留一份半装上的检查。
//
// # 这些测试防的是什么错
//
//   - **把这条检查退化成一条事件一验**。schedule 的规矩全是跨事件的（id 不许重用、
//     delete 只许指向活着的记录、固定频率的 dispatch 必须带一个不早于当前
//     scheduledAt 的 acceptedAt），拿单条事件根本判不了。
//   - **装的时候不扫一遍已经装载进来的流**。一份历史里就带着坏改动的会话必须在装载
//     这一刻就响；等下一次追加才炸的话，那条坏改动早就改不掉了。
//   - **注销之后订阅还挂着**。一条不该再查的检查继续在别人的写路径上抛，等于让一个
//     已经卸掉的包还在否决别人的写入。
//   - **少给一个协作者却装上去了**。三个都是必需的，缺一个就只能报错——留一份查不
//     全的检查比没有检查更坏，它会在一个不完整的视角上误报。
//   - **seedLength 被忽略**。继承来的那一段不归这条流管，把它一起验会让一个分叉出来
//     的孩子替父那一段的历史背锅。

package schedule

import (
	"strings"
	"testing"

	"ds-harness-go/invariants"
	"ds-harness-go/session"
)

// registryOf 造一份什么都放行的不变量注册表。
func registryOf(t *testing.T) *invariants.Registry {
	t.Helper()
	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("造不变量注册表失败：%v", err)
	}
	return registry
}

// violationOf 跑一段会违反不变量的动作，交回它抛出来的那次违规。
//
// [invariants.Fail] 是 panic 走的，所以这里必须 recover——直接调用会把整个用例带走。
func violationOf(t *testing.T, action func()) *invariants.Error {
	t.Helper()
	var caught *invariants.Error
	func() {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			failure, ok := recovered.(*invariants.Error)
			if !ok {
				panic(recovered)
			}
			caught = failure
		}()
		action()
	}()
	if caught == nil {
		t.Fatal("本该抛出一次违规，却安然跑完了")
	}
	return caught
}

// ---- ValidateStream ----

func TestValidateStreamAcceptsAWellFormedStream(t *testing.T) {
	stream := Stream{Events: []session.Event{
		changeEvent(createJSON(atRecordJSON)),
		changeEvent(`{"version":1,"operation":"delete","id":"schedule-2"}`),
	}}
	if err := ValidateStream(stream); err != nil {
		t.Fatalf("这条流是干净的，却被拒了：%v", err)
	}
}

func TestValidateStreamRejectsACrossEventViolation(t *testing.T) {
	// 单看每一条事件都合法：两次 create 各自都是一条读得动的改动。坏在**它们放在
	// 一起**——同一个 id 被建了两次。这就是这条检查为什么收整条流。
	stream := Stream{Events: []session.Event{
		changeEvent(createJSON(atRecordJSON)),
		changeEvent(createJSON(atRecordJSON)),
	}}
	expectLogError(t, ValidateStream(stream), "重用 id 的流")
}

func TestValidateStreamHonorsSeedLength(t *testing.T) {
	// 前一半是从父那里继承来的。连着验会看到「同一个 id 建了两次」，只验后一半才是
	// 对的——这个孩子不拥有父那一段提醒。
	stream := Stream{
		Events: []session.Event{
			changeEvent(createJSON(atRecordJSON)),
			changeEvent(createJSON(atRecordJSON)),
		},
		SeedLength: 1,
	}
	if err := ValidateStream(stream); err != nil {
		t.Fatalf("继承来的那一段本该跳过：%v", err)
	}
}

// ---- RegisterInvariants ----

func TestRegisterInvariantsRequiresEveryCollaborator(t *testing.T) {
	loaded := func() []Stream { return nil }
	subscribe := func(func(Stream)) func() { return func() {} }
	cases := []struct {
		what      string
		registry  *invariants.Registry
		loaded    func() []Stream
		subscribe func(func(Stream)) func()
	}{
		{"少了注册表", nil, loaded, subscribe},
		{"少了已装载日志", registryOf(t), nil, subscribe},
		{"少了订阅", registryOf(t), loaded, nil},
	}
	for _, each := range cases {
		t.Run(each.what, func(t *testing.T) {
			unregister, err := RegisterInvariants(
				t.Context(), each.registry, each.loaded, each.subscribe)
			if err == nil {
				t.Fatal("本该报错，却装上去了")
			}
			if unregister != nil {
				t.Fatal("报错的那次注册不该交回一个注销函数")
			}
			if !strings.Contains(err.Error(), "schedule:") {
				t.Fatalf("报的是 %v", err)
			}
		})
	}
}

func TestRegisterInvariantsSweepsAlreadyLoadedStreams(t *testing.T) {
	// 装的那一刻就要把已经装载进来的流走一遍：一份历史里就带着坏改动的会话必须在
	// 这里响，而不是等下一次追加。
	registry := registryOf(t)
	loaded := func() []Stream {
		return []Stream{{Events: []session.Event{
			changeEvent(`{"version":1,"operation":"delete","id":"ghost"}`),
		}}}
	}
	violation := violationOf(t, func() {
		_, _ = RegisterInvariants(t.Context(), registry, loaded,
			func(func(Stream)) func() { return func() {} })
	})
	if violation.PackageName != PackageName {
		t.Fatalf("报违规的包名是 %q", violation.PackageName)
	}
	if !strings.Contains(violation.Message, "delete") {
		t.Fatalf("那条违规说的是 %q", violation.Message)
	}
}

func TestRegisterInvariantsChecksSubsequentStreams(t *testing.T) {
	registry := registryOf(t)
	var observer func(Stream)
	unregister, err := RegisterInvariants(t.Context(), registry,
		func() []Stream { return nil },
		func(check func(Stream)) func() {
			observer = check
			return func() { observer = nil }
		})
	if err != nil {
		t.Fatalf("装不变量失败：%v", err)
	}
	if observer == nil {
		t.Fatal("没有订阅后续的流")
	}
	// 干净的流照常放行。
	observer(Stream{Events: []session.Event{changeEvent(createJSON(atRecordJSON))}})
	// 坏的流当场抛。
	violationOf(t, func() {
		observer(Stream{Events: []session.Event{
			changeEvent(`{"version":1,"operation":"delete","id":"ghost"}`),
		}})
	})

	// 注销之后那条订阅必须真的退掉：留着等于让一个已经卸掉的包还在否决别人的写入。
	unregister()
	if observer != nil {
		t.Fatal("注销之后订阅还挂着")
	}
}
