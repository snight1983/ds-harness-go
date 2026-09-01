// 本文件的作用：这个包自己拥有的那条持久不变量——一份整表快照要长成什么样
// 才配进日志。
//
// 源: packages/todo/tool-todo/src/invariant.ts

package todo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/session"
)

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/todo/tool-todo/src/invariant.ts:7
const PackageName = "@deepseek-ai/dsh-tool-todo"

// ValidateEvent 验一条事件里本包拥有的那些字段；不是 [session.EventTodoWrite]
// 就什么都不做。
//
// 源: packages/todo/tool-todo/src/invariant.ts:24-45
//
// # 它故意不查什么
//
// 有几条在做，它一个字都不说。那是这件工具**当下**的部署策略
// （[Config.AllowParallelInProgress]），不是一条持久的形状规则：一份在允许并行
// 的时候写下的日志，在部署收紧策略之后必须仍然能回放。把不变量绑在当下的配置上，
// 等于让一段写下时完全合法的历史因为今天的选择而变得不合法。
//
// 新增: DSH 那边这个函数收一个 `fail` 回调、违例时直接抛。Go 这边返回**第一条**
// 违例，和 [session.Trace.Validate] 的做法一致：它因此可以脱离不变量注册表单独用
// （离线校验一份日志、或者在写之前自己先验一遍），而 [RegisterInvariants] 只是
// 把这个错误接到 [invariants.Fail] 上。
func ValidateEvent(event session.Event) error {
	if event.Type != session.EventTodoWrite {
		return nil
	}
	var payload struct {
		Todos json.RawMessage `json:"todos"`
	}
	if err := json.Unmarshal(event.Data, &payload); err != nil {
		return errors.New("todo/write todos must be an array")
	}
	return validateTodos(payload.Todos)
}

// validateTodos 验一份整表快照的形状。
//
// 源: packages/todo/tool-todo/src/invariant.ts:24-39
//
// 它按**原始 JSON**验而不是先解成 [session.TodoItem]，因为要分开报的正是那几种
// 解码会一并吃掉的坏法：不是数组、条目不是对象、content 不是字符串、状态不认识。
// 解成结构体再看，这四种会挤成同一句「读不回来」。
func validateTodos(raw json.RawMessage) error {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return errors.New("todo/write todos must be an array")
	}
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		var fields map[string]json.RawMessage
		// null 解出来是一张 nil 表且不报错，所以两个条件都要：一个挡住 42 和 "x"，
		// 另一个挡住 null。
		if err := json.Unmarshal(item, &fields); err != nil || fields == nil {
			return errors.New("todo/write entries must be objects")
		}
		content, err := decodeString(fields["content"])
		if err != nil || content == "" || strings.TrimSpace(content) != content {
			return errors.New("todo/write content must be non-empty and already trimmed")
		}
		if _, repeated := seen[content]; repeated {
			quoted, _ := json.Marshal(content)
			return fmt.Errorf("todo/write repeats content %s", quoted)
		}
		seen[content] = struct{}{}
		status, err := decodeString(fields["status"])
		if err != nil || !knownStatus(status) {
			return fmt.Errorf("todo/write carries unknown status %s", quoteRaw(fields["status"]))
		}
	}
	return nil
}

// decodeString 把一段原始 JSON 读成字符串，不是字符串就报错。
func decodeString(raw json.RawMessage) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return value, nil
}

// knownStatus 说明这个状态是不是三个合法取值之一。
func knownStatus(status string) bool {
	for _, known := range statuses {
		if string(known) == status {
			return true
		}
	}
	return false
}

// quoteRaw 把一段原始 JSON 写成能放进错误文本的样子。
//
// 报的是模型/日志里那个**原样**的值：一条 status 是 42 的记录，说「unknown
// status 42」比说「unknown status ""」更接近现场。字段缺席时那段原始 JSON 是
// nil，写成 null。
func quoteRaw(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "null"
	}
	return string(raw)
}

// RegisterInvariants 装上持久待办快照的形状检查，返回注销函数。
//
// 源: packages/todo/tool-todo/src/invariant.ts:97-103（apply）
//
// 两条胳膊，和 DSH 一样：装的时候把**已经装进来的**日志走一遍（一份历史里就带着
// 坏快照的会话，必须在装载这一刻就响，而不是等下一次追加），然后订阅后续的追加。
//
// 新增: DSH 那两条胳膊都从 cordis 上拿——ctx.sessions.list() 取历史，
// ctx.on('internal/dispatch') 截住后来的。Go 里活会话服务是循环那一块的东西
// （见 docs/DESIGN.md 第八节），本包在第 4 块，所以这两条胳膊由装配方以函数交进来，
// 做法和 [github.com/snight1983/ds-harness-go/workspace.RegisterInvariants] 收 facility 与 live 一致。
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
