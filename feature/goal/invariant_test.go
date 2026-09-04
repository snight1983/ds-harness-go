// 本文件的作用：把那条整流不变量钉在它真会出错的边上——装的那一刻已经装载进来的
// 流验不验、订阅那条胳膊注销之后还查不查，以及三个协作者少给一个时是当场报错还是
// 留一份半装上的检查。
//
// # 这些测试防的是什么错
//
//   - **装的时候不扫一遍已经装载进来的流**。一份历史里就带着坏改动的会话必须在装载
//     这一刻就响；等下一次追加才炸的话，那条坏改动早就改不掉了。
//   - **注销之后订阅还挂着**。一条不该再查的检查继续在别人的写路径上抛，等于让一个
//     已经卸掉的包还在否决别人的写入。
//   - **少给一个协作者却装上去了**。三个都是必需的，缺一个就只能报错——留一份查不全
//     的检查比没有检查更坏，它会在一个不完整的视角上误报。
//   - **报违规时挂的是别人的包名**。[invariants.Error] 的包名是排查时唯一能指回来的
//     线索；挂错了，一次目标日志的违规会被当成别的包的毛病去查。

package goal

import (
	"strings"
	"testing"

	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/sessionlog"
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

// brokenStream 是一条一眼就该被拒的流：没有当前目标就 clear。
func brokenStream() []sessionlog.Event {
	return []sessionlog.Event{changeEvent(clearJSON(2, 20))}
}

func TestRegisterInvariantsRequiresEveryCollaborator(t *testing.T) {
	loaded := func() [][]sessionlog.Event { return nil }
	subscribe := func(func([]sessionlog.Event)) func() { return func() {} }
	cases := []struct {
		what      string
		registry  *invariants.Registry
		loaded    func() [][]sessionlog.Event
		subscribe func(func([]sessionlog.Event)) func()
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
			if !strings.Contains(err.Error(), "goal:") {
				t.Fatalf("报的是 %v", err)
			}
		})
	}
}

func TestRegisterInvariantsSweepsAlreadyLoadedStreams(t *testing.T) {
	// 装的那一刻就要把已经装载进来的流走一遍：一份历史里就带着坏改动的会话必须在
	// 这里响，而不是等下一次追加。
	registry := registryOf(t)
	violation := violationOf(t, func() {
		_, _ = RegisterInvariants(t.Context(), registry,
			func() [][]sessionlog.Event { return [][]sessionlog.Event{brokenStream()} },
			func(func([]sessionlog.Event)) func() { return func() {} })
	})
	if violation.PackageName != PackageName {
		t.Fatalf("报违规的包名是 %q，本该是 %q", violation.PackageName, PackageName)
	}
	if violation.Message == "" {
		t.Fatal("那条违规一句话都没带")
	}
}

func TestRegisterInvariantsChecksSubsequentStreams(t *testing.T) {
	registry := registryOf(t)
	var observer func([]sessionlog.Event)
	unregister, err := RegisterInvariants(t.Context(), registry,
		func() [][]sessionlog.Event { return nil },
		func(check func([]sessionlog.Event)) func() {
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
	observer([]sessionlog.Event{createEvent()})
	// 坏的流当场抛。
	violationOf(t, func() { observer(brokenStream()) })

	// 注销之后那条订阅必须真的退掉：留着等于让一个已经卸掉的包还在否决别人的写入。
	unregister()
	if observer != nil {
		t.Fatal("注销之后订阅还挂着")
	}
}
