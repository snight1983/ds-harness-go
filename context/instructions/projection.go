// 本文件的作用：一次工具触碰从「某个文件被写过了」走到「模型收到一条指令变更」
// 之间那两道提交边界——先等这次调用的祖先都收完摊，再等它所在的那个耐久步骤关掉，
// 然后才准许一次异步投影去动收件箱。
//
// 源: packages/context/agent-instructions/src/index.ts:264-357

package instructions

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/snight1983/ds-harness-go/core/agent"
	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/session"
)

// touch 是一次「这个 agent 碰过这条路径」。
//
// 源: packages/context/agent-instructions/src/index.ts:85
type touch struct {
	agent agent.Agent
	path  string
}

// preparation 记住上一次装基线时算出来的那份口径：身份，加上被预算裁掉的作用域。
//
// 源: packages/context/agent-instructions/src/index.ts:82-85
//
// 被裁掉的那些必须记住，否则下一次对账会把它们当成「模型没见过的新文件」重发一遍，
// 而它们之所以不在，正是因为预算装不下。
type preparation struct {
	identity string
	excluded map[string]struct{}
}

// sessionState 是一段会话在本层的全部状态。
//
// 源: packages/context/agent-instructions/src/index.ts:81-103
type sessionState struct {
	// composing 把这段会话上的每一次 compose 串起来，**跨 I/O 持有**。
	//
	// 新增: DSH 靠单线程事件循环，两次 compose 之间不会真的交错到「一次读到
	// 另一次改到一半的版本表」。Go 这边前置步骤观察者和投影跑在各自的协程上，
	// 而 versions 和 prep 是被就地改的，所以要一把明着的锁。它护住的正好是
	// 下面这两个字段。
	composing sync.Mutex

	// versions 是这段会话的作用域元数据表，[Reconcile] 会就地改它。
	versions map[string]VersionState
	// prep 是上一次装基线留下的口径，没装过是 nil。
	prep *preparation

	// mutex 护住下面这几样，临界区里不做 I/O。
	mutex sync.Mutex
	// stepOpen 是「此刻有没有一个耐久步骤开着」，stepKnown 为假表示还没重放过。
	stepOpen  bool
	stepKnown bool
	// touches 是步骤开着期间攒下的触碰，等步骤关掉再一起放出去。
	touches []touch
	// tail 是投影队列的队尾：它被关掉表示排在它之前的投影全跑完了。nil 表示队列空。
	//
	// 新增: DSH 是一条 Promise 链（`projectionTails`）。Go 里没有 Promise，
	// 一个「跑完就关」的通道是同一件事：新来的投影拿走旧队尾、换上自己那个，
	// 开跑前先等旧的那个关掉。
	tail chan struct{}
}

// stateFor 取这段会话那份状态，没有就当场建一份。
func (i *installer) stateFor(live *coresession.Session) *sessionState {
	id := live.ID()
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if existing, present := i.sessions[id]; present {
		return existing
	}
	created := &sessionState{versions: map[string]VersionState{}}
	i.sessions[id] = created
	return created
}

// lookupState 只查不建。
//
// 新增: 会话事件观察者是**全局**装的，它看得见这个进程里每一段会话的每一条事件。
// 用 [installer.stateFor] 的话，一段从来没碰过指令的会话也会在表里留下一行，
// 而那一行要等它的 agent 被处置才删得掉——一段没有 agent 的会话就永远留在那里了。
// 只更新已经在场的那几份不会漏事：一份状态是被投影或者前置步骤建出来的，
// 而它建出来的第一件事就是把日志重放一遍算出步骤开着没有。
func (i *installer) lookupState(id session.SessionID) (*sessionState, bool) {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	existing, present := i.sessions[id]
	return existing, present
}

// ---- 投影队列 ----

// queue 把一次投影排到这段会话的队尾上。
//
// 源: packages/context/agent-instructions/src/index.ts:262-275
func (i *installer) queue(live agent.Agent, touchedPath string) {
	state := i.stateFor(live.Session())

	state.mutex.Lock()
	previous := state.tail
	current := make(chan struct{})
	state.tail = current
	state.mutex.Unlock()

	go func() {
		defer func() {
			// 先把自己从队尾上摘下来再关：反过来的话，一个等着的人会醒过来、
			// 重新读到这个已经关掉的通道、再等一次，然后一直空转。
			state.mutex.Lock()
			if state.tail == current {
				state.tail = nil
			}
			state.mutex.Unlock()
			close(current)
		}()

		if previous != nil {
			select {
			case <-previous:
			case <-i.lifetime.Done():
				return
			}
		}
		if err := i.composeAndSync(i.lifetime, live, nil, []string{touchedPath}); err != nil {
			// 整层已经被摘掉时不报：那不是故障，是收摊。
			if i.lifetime.Err() == nil {
				i.logger.Warn("刷新工作区指令失败",
					"会话", live.Session().ID(), "路径", touchedPath, "错误", err)
			}
		}
	}()
}

// wait 等这段会话上排着的投影全部跑完。
//
// 源: packages/context/agent-instructions/src/index.ts:277-280
//
// 循环而不是只等一次：等的过程中还可能有新的投影排进来，而前置步骤要看到的是
// 「它们都落地之后」的那个收件箱。
func (i *installer) wait(ctx context.Context, state *sessionState) error {
	for {
		state.mutex.Lock()
		current := state.tail
		state.mutex.Unlock()
		if current == nil {
			return nil
		}
		select {
		case <-current:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// ---- 步骤边界 ----

// stepIsOpen 回答「这段会话此刻有没有一个耐久步骤开着」，答过一次就记住。
//
// 源: packages/context/agent-instructions/src/index.ts:282-292
func (i *installer) stepIsOpen(state *sessionState, live *coresession.Session) bool {
	state.mutex.Lock()
	if state.stepKnown {
		known := state.stepOpen
		state.mutex.Unlock()
		return known
	}
	state.mutex.Unlock()

	// 重放不在锁里做：它要走一遍整段日志，而这把锁同时护着投影队尾。
	open := false
	for _, event := range live.Events() {
		switch event.Type {
		case session.EventStepStart:
			open = true
		case session.EventStepEnd, session.EventTurnEnd:
			open = false
		}
	}

	state.mutex.Lock()
	defer state.mutex.Unlock()
	// 重放期间可能有一条事件已经把答案定下来了，那一条比重放新。
	if state.stepKnown {
		return state.stepOpen
	}
	state.stepOpen = open
	state.stepKnown = true
	return open
}

// projectTouch 决定这次触碰是当场投影，还是攒到步骤关掉再说。
//
// 源: packages/context/agent-instructions/src/index.ts:294-303
func (i *installer) projectTouch(entry touch) {
	live := entry.agent.Session()
	state := i.stateFor(live)
	if !i.stepIsOpen(state, live) {
		i.queue(entry.agent, entry.path)
		return
	}
	state.mutex.Lock()
	state.touches = append(state.touches, entry)
	state.mutex.Unlock()
}

// onSessionEvent 跟住步骤边界，并在步骤关掉时把攒着的触碰放出去。
//
// 源: packages/context/agent-instructions/src/index.ts:305-320
func (i *installer) onSessionEvent(live *coresession.Session, event session.Event) {
	switch event.Type {
	case session.EventStepStart, session.EventStepEnd, session.EventTurnEnd:
	default:
		return
	}
	state, present := i.lookupState(live.ID())
	if !present {
		return
	}

	state.mutex.Lock()
	state.stepKnown = true
	state.stepOpen = event.Type == session.EventStepStart
	var released []touch
	if event.Type == session.EventStepEnd {
		released, state.touches = state.touches, nil
	}
	state.mutex.Unlock()

	for _, entry := range released {
		i.queue(entry.agent, entry.path)
	}
}

// ---- 工具结果 ----

// fileTouchToolNames 是那几个「碰了一个具体文件」的工具名。
//
// 源: packages/context/agent-instructions/src/index.ts:69
var fileTouchToolNames = map[string]struct{}{"read": {}, "write": {}, "edit": {}}

// filePathFromExecution 从一次调用的入参里取出它碰的那条路径。
//
// 源: packages/context/agent-instructions/src/index.ts:71-78
//
// 新增: DSH 逐项查 `typeof exec.arguments === 'object'`、`'file_path' in ...`、
// `typeof ... === 'string'`。Go 这边解进一个只有这一个字段的结构体，
// 三条查都由 [encoding/json] 承担：入参不是对象、或者 file_path 不是字符串，
// 都会让这一次解码失败，落到同一个「这次调用没碰文件」的结论上。
func filePathFromExecution(exec tools.Execution) (string, bool) {
	if _, watched := fileTouchToolNames[exec.Name]; !watched {
		return "", false
	}
	var arguments struct {
		FilePath *string `json:"file_path"`
	}
	if err := json.Unmarshal(exec.Arguments, &arguments); err != nil {
		return "", false
	}
	if arguments.FilePath == nil {
		return "", false
	}
	trimmed := strings.TrimSpace(*arguments.FilePath)
	if trimmed == "" {
		return "", false
	}
	return trimmed, true
}

// onToolResult 把一次调用碰过的路径沿着执行树往上并，到根上再放去投影。
//
// 源: packages/context/agent-instructions/src/index.ts:341-357
//
// 往上并那一步是必须的：一个复合工具的嵌套调用先收摊，那时外层还在跑，
// 而外层随时可能失败。等根上那一次收摊再投影，模型才不会看见一次被回滚掉的写。
func (i *installer) onToolResult(exec tools.Execution, result tools.Result) {
	i.mutex.Lock()
	touches := i.touches[exec.Token]
	delete(i.touches, exec.Token)
	i.mutex.Unlock()

	// 新增: DSH 这里还查一次 `!exec.signal.aborted`。Go 这边一次被取消的调用
	// 本来就会变成 IsError 为真的结果（见 core/tools 的包文档：一切失败都是结果），
	// 所以那一条被上一条吃掉了。
	if !result.IsError && exec.Agent != nil {
		if path, carries := filePathFromExecution(exec); carries {
			if live, err := i.agentOf(exec.Agent); err != nil {
				i.logger.Warn("工作区指令：这次工具调用的 agent 查不回来，丢掉这次触碰",
					"工具", exec.Name, "路径", path, "错误", err)
			} else {
				touches = append(touches, touch{agent: live, path: path})
			}
		}
	}

	if !exec.Parent.IsZero() {
		if len(touches) > 0 {
			i.mutex.Lock()
			i.touches[exec.Parent] = append(i.touches[exec.Parent], touches...)
			i.mutex.Unlock()
		}
		return
	}
	for _, entry := range touches {
		i.projectTouch(entry)
	}
}
