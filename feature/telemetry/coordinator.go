// 本文件的作用：采集协调器——它从一个活会话身上要看什么、五个采集点各自
// 干什么、那个固定的分块投影、交接游标怎么往前走，以及记录是怎么拼出来的。
//
// 源: packages/session/session-telemetry/src/coordinator.ts

package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/snight1983/ds-harness-go/sessionlog"
)

// SessionView 是这个协调器从一个活会话身上要看的全部东西。
//
// 新增: DSH 收的是循环那一块（DESIGN.md 第八节第 6 块）的 Session 具体类型。
// 本包在第 2 块，所以按 Go 的办法收接口不收具体类型——第 6 块的活会话满足它，
// 而本包不必等到那时候才能写完、才能被测。同样的做法见
// [projection.SessionView] 和 projectioncache.LiveSession。
type SessionView interface {
	// ID 是这个会话的身份，游标和认领集合都按它归档。
	ID() sessionlog.SessionID

	// Events 是这个会话在内存里的完整日志，按 seq 升序。
	//
	// 返回的切片只会被读，不会被本包改动或留存。
	Events() []sessionlog.Event

	// Header 是这个会话不可变的存储元数据，身份属性从它上面取。
	Header() sessionlog.SessionHeader

	// FirstLiveSeq 是这次生命周期第一条自己产出的事件的 seq，
	// 也就是构造种子的长度。
	//
	// 没有交接游标时重放从它减一开始，**不是从 0 开始**：种子里的内容早就
	// 以另一个身份离开过进程（同一个 id 在上一个进程里恢复，或者父会话的流），
	// 再交一遍就是重复上报。
	FirstLiveSeq() int
}

// Options 是建一个 [Coordinator] 需要的东西。
//
// 源: packages/session/session-telemetry/src/coordinator.ts:66-72
type Options struct {
	// Sink 是收记录的那一方。必填。
	Sink Sink

	// Rules 是脱敏规则链，按从外到内的顺序。可以为空，那就没有任何脱敏。
	//
	// 构造时复制一份，之后改动调用方那个切片影响不到已经建好的协调器。
	Rules []Rule

	// Now 给出 ops 记录的时刻，Unix 纪元毫秒。留空用真时钟。
	//
	// 新增: DSH 直接调 Date.now()，于是那两条 ops 记录的 time 字段在测试里
	// 断言不了。ledger 记录用的是源事件自己的 [sessionlog.Event.Time]，不走这里。
	Now func() int64

	// Logger 记那几件被兜住的事：一条规则炸了、一条记录被扣下、
	// 接收器的 Emit 或 Shutdown 炸了。留空用 slog.Default()。
	//
	// 留空**不是**丢弃：这里记的正是没人会主动去查、却必须留下痕迹的那类事
	// ——症状全都是「上报里少了点东西」。
	Logger *slog.Logger
}

// Coordinator 是采集侧：它把会话事件投影成记录，过一遍脱敏，交给接收器。
//
// 源: packages/session/session-telemetry/src/coordinator.ts:60-259
//
// 零值不可用，用 [New] 建。它可以被多个 goroutine 同时使用。
//
// 新增: DSH 在构造函数里往 cordis 上订阅五个事件。Go 这边没有总线，
// 那五个采集点是五个方法，由装配方在同样那几个位置调，见本包文档第 1 条。
type Coordinator struct {
	sink   Sink
	rules  []Rule
	now    func() int64
	logger *slog.Logger

	// mu 罩住下面全部可变状态，**并且罩住 [Sink.Emit] 那一次调用**。
	//
	// 新增: DSH 是单线程的，不需要这个。Go 里同一个会话可能被多个 goroutine
	// 同时推进，而游标和「这个分块见过没有」都是读改写。锁一直持到 Emit 之后，
	// 换来的是「同一个会话的记录按 seq 顺序交出去」这条性质——Emit 按契约
	// 只是一次入队，持锁的时间是有界的。代价写在 [Sink.Emit] 的注释里：
	// 接收器不许回调协调器。
	mu sync.Mutex
	// adopted 是这个协调器认领了、并且还活着的会话。
	//
	// 它同时管两件事：认领去重，以及卸载时给还没退休的会话补 shutdown 记录。
	adopted map[sessionlog.SessionID]struct{}
	// cursor 是每个会话已经**交出去**的最高 seq。缺席表示「整段重新交」。
	//
	// 新增: DSH 用的是一张模块级的 WeakMap，它自己承认那是一次破例，理由是
	// cordis 没有 HMR 的状态交接接口。Go 没有 HMR，那个理由不存在。见本包文档第 3 条。
	cursor map[sessionlog.SessionID]int
	// chunkSeen 是每个会话里第一条分块已经交出去了的那些 "turn:step" 键。
	chunkSeen map[sessionlog.SessionID]map[string]struct{}
	closed    bool
}

// New 建一个采集协调器。
//
// 源: packages/session/session-telemetry/src/coordinator.ts:73-77
func New(options Options) (*Coordinator, error) {
	if options.Sink == nil {
		return nil, fmt.Errorf("feature/telemetry: 建协调器需要一个接收器")
	}

	now := options.Now
	if now == nil {
		now = func() int64 { return time.Now().UnixMilli() }
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Coordinator{
		sink:      options.Sink,
		rules:     slices.Clone(options.Rules),
		now:       now,
		logger:    logger,
		adopted:   map[sessionlog.SessionID]struct{}{},
		cursor:    map[sessionlog.SessionID]int{},
		chunkSeen: map[sessionlog.SessionID]map[string]struct{}{},
	}, nil
}

// Adopt 认领一个活会话：把它日志里游标之后的那一段交出去，之后靠
// [Coordinator.Observe] 逐条跟。重复认领是空操作。
//
// 源: packages/session/session-telemetry/src/coordinator.ts:150-154
//
// 由装配方在会话建出来时调，也在装配好之后扫一遍已经活着的会话时调——
// 后者对应 DSH 那句 `for (const session of ctx.sessions.list())`。
//
// 认领是 live 采集的入口。一个只要 on-demand 的装配方永远不调它，
// 只调 [Coordinator.CaptureSession]，见本包文档第 2 条。
func (c *Coordinator) Adopt(view SessionView) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	id := view.ID()
	if _, already := c.adopted[id]; already {
		c.mu.Unlock()
		return
	}
	c.adopted[id] = struct{}{}
	c.mu.Unlock()

	c.replay(view, 0, false)
}

// CaptureSession 把这个会话日志里游标之后的那一段交出去，不认领它。
//
// 源: packages/session/session-telemetry/src/coordinator.ts:122-134
//
// 这是 on-demand 采集的全部：脱敏是在**这次调用**里跑的，所以调用方在提出
// 要求之前手上没有任何记录副本，用的也是此刻挂着的那套规则。
//
// 每一条事件各自被兜住：一条被扣下，同一次重放里后面的照跑。
func (c *Coordinator) CaptureSession(view SessionView) { c.replay(view, 0, false) }

// CaptureThrough 同 [Coordinator.CaptureSession]，但停在 throughSeq 这一条（含）。
//
// 源: packages/session/session-telemetry/src/coordinator.ts:122-134
func (c *Coordinator) CaptureThrough(view SessionView, throughSeq int) {
	c.replay(view, throughSeq, true)
}

// Observe 把一条刚提交的事件交出去。
//
// 源: packages/session/session-telemetry/src/coordinator.ts:96-100
//
// 由持有活会话的那一层在事件**提交之后**调，也就是 DSH 那个 session/event
// 订阅。它同步跑完并且不返回错误：上报是尽力而为的观察，一次失败不该让
// 提交事件的那条路跟着失败。
func (c *Coordinator) Observe(view SessionView, event sessionlog.Event) {
	c.contain(view.ID(), "投影一条事件", func() error {
		c.mu.Lock()
		defer c.mu.Unlock()

		if c.closed {
			return nil
		}
		return c.captureEvent(view, event)
	})
}

// HintFlush 把「一个回合结束了」这个边界转给接收器可选的那个提示。
//
// 源: packages/session/session-telemetry/src/coordinator.ts:220-222
//
// 只对**已认领**的会话有效。接收器没实现 [Flusher] 时是空操作——大多数
// 接收器就该这样，让 SDK 自己的攒批节奏说了算。
//
// 它不返回任何东西，也不等任何东西：循环在回合结束时会并行等一批钩子，
// 上报绝不能是被等的那一个。
func (c *Coordinator) HintFlush(view SessionView) {
	id := view.ID()
	c.contain(id, "转交回合结束提示", func() error {
		c.mu.Lock()
		defer c.mu.Unlock()

		if c.closed {
			return nil
		}
		if _, adopted := c.adopted[id]; !adopted {
			return nil
		}
		flusher, ok := c.sink.(Flusher)
		if !ok {
			return nil
		}
		flusher.Flush()
		return nil
	})
}

// Retire 是一个会话自己走到头的那一刻：补一条 shutdown 运维记录，
// 然后把它从认领集合和两张表里删掉。
//
// 源: packages/session/session-telemetry/src/coordinator.ts:86-93
//
// 没被认领过的会话在这里是空操作——on-demand 采集不产生任何 ops 记录。
//
// 新增: DSH 的游标和分块表都是 WeakMap，条目跟着 Session 对象一起死。
// Go 这两张表按会话 id 归档，不在这里删就是泄漏。删掉之后同一个 id 再被
// 认领会从 [SessionView.FirstLiveSeq] 重新交起，和 DSH 拿到一个新 Session
// 对象时的行为一致。
func (c *Coordinator) Retire(view SessionView) {
	id := view.ID()
	c.contain(id, "补一条退休记录", func() error {
		c.mu.Lock()
		defer c.mu.Unlock()

		if c.closed {
			return nil
		}
		if _, adopted := c.adopted[id]; !adopted {
			return nil
		}
		delete(c.adopted, id)
		delete(c.cursor, id)
		delete(c.chunkSeen, id)
		return c.deliver(shutdownRecord(id, c.now()))
	})
}

// RelayError 把一次 agent 出错转成一条 agent-error 运维记录。
//
// 源: packages/session/session-telemetry/src/coordinator.ts:225-243
//
// 这是 DSH 那边唯一一条从活总线上转过来的信号——它在会话日志里没有家，
// 所以只能走 ops 通道。
func (c *Coordinator) RelayError(view SessionView, agentID string, turn, step int, err error) {
	id := view.ID()
	c.contain(id, "转发一次 agent 出错", func() error {
		name, message := errorDetail(err)
		// 两个字符串字段排不出错——[encoding/json] 对非法 UTF-8 是替换而不是报错，
		// 所以这里没有可失败的分支要挡。
		body, _ := json.Marshal(errorBody{Name: name, Message: message})

		c.mu.Lock()
		defer c.mu.Unlock()

		if c.closed {
			return nil
		}
		return c.deliver(Record{
			Channel:  ChannelOps,
			Time:     c.now(),
			Severity: SeverityError,
			Attributes: map[string]any{
				"telemetry.op": "agent-error",
				"session.id":   string(id),
				"agent.id":     agentID,
				"error.name":   name,
				"turn":         turn,
				"step":         step,
			},
			Body: body,
		})
	})
}

// Close 给还认领着的每个会话补一条 shutdown 记录，然后等接收器排空。
//
// 源: packages/session/session-telemetry/src/coordinator.ts:110-121
//
// 走到这里还认领着的会话，是一路活到整个应用关闭的那些，所以在接收器静默
// 之前先把它们的记录留下。接收器排空失败只**记一条警告**并原样返回——
// 由调用方决定要不要理它，但按契约它不该让装配方的卸载失败。
//
// 这之后所有采集点都变成空操作。可以重复调用，第二次起是空操作。
func (c *Coordinator) Close(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	// 排序是为了让产出可复现：Go 的 map 遍历顺序是随机的，
	// 一串顺序不定的记录进了测试断言就会随机地不一样。
	ids := slices.Sorted(maps.Keys(c.adopted))
	at := c.now()
	for _, id := range ids {
		if err := c.deliver(shutdownRecord(id, at)); err != nil {
			c.logger.Warn("feature/telemetry: 退休记录交不出去",
				slog.String("session", string(id)),
				slog.Any("error", err))
		}
	}
	clear(c.adopted)
	clear(c.cursor)
	clear(c.chunkSeen)
	c.mu.Unlock()

	err := c.sink.Shutdown(ctx)
	if err != nil {
		c.logger.Warn("feature/telemetry: 接收器排空失败", slog.Any("error", err))
	}
	return err
}

// replay 把日志里游标之后的那一段逐条交出去，bounded 为真时停在 throughSeq（含）。
//
// 源: packages/session/session-telemetry/src/coordinator.ts:135-146
//
// 兜是**逐条**的：一条被扣下，后面的照跑。
//
// 新增: DSH 在循环之前把游标读一次。这里每条事件重新读一次——它是单线程的，
// 两种写法等价；Go 里同一个会话可能正被别人推进，每条重读才不会拿一个
// 过期的游标去判「这条交过没有」。
func (c *Coordinator) replay(view SessionView, throughSeq int, bounded bool) {
	id := view.ID()
	for _, event := range view.Events() {
		if bounded && event.Seq > throughSeq {
			break
		}
		c.contain(id, "重放一条事件", func() error {
			c.mu.Lock()
			defer c.mu.Unlock()

			if c.closed {
				return nil
			}
			cursor, tracked := c.cursor[id]
			if !tracked {
				cursor = view.FirstLiveSeq() - 1
			}
			if event.Seq <= cursor {
				// 游标之下那一半：只喂分块投影，不再交一遍。于是一个重新
				// 认领的协调器丢掉的是同一批「步骤中途的分块」，和当初看着
				// 这个步骤开始的那个协调器丢的完全一样。
				c.track(id, event)
				return nil
			}
			return c.captureEvent(view, event)
		})
	}
}

// track 只喂分块投影，不交出去。
//
// 源: packages/session/session-telemetry/src/coordinator.ts:157-161
func (c *Coordinator) track(id sessionlog.SessionID, event sessionlog.Event) {
	if event.Type != sessionlog.EventAssistantChunk {
		return
	}
	key, ok := chunkKey(event)
	if !ok {
		return
	}
	c.markChunk(id, key)
}

// captureEvent 把一条事件投影、脱敏，然后交给接收器。调用方持着 [Coordinator.mu]。
//
// 源: packages/session/session-telemetry/src/coordinator.ts:164-190
func (c *Coordinator) captureEvent(view SessionView, event sessionlog.Event) error {
	id := view.ID()
	if event.Type == sessionlog.EventAssistantChunk {
		key, ok := chunkKey(event)
		if !ok {
			// 负载读不回来就拼不出这个键，也就无从判重。扣下它并记一条日志：
			// 这条分块的内容在这个步骤装配好的助手消息里是完整的，扣下丢掉的
			// 只是「这个步骤开始出字了」这一个信号，而放行等于让一次追加侧的
			// 缺陷把同一个步骤的每一条分块都送上去。
			return fmt.Errorf("assistant/chunk 的负载读不回来，拼不出投影键")
		}
		// 固定的分块投影：每个 (turn, step) 只有第一条走出去。被丢掉的那些
		// **不推进游标**，所以重新认领时会被确定性地再丢一遍。
		if !c.markChunk(id, key) {
			return nil
		}
	}

	record := Record{
		Channel:    ChannelLedger,
		Time:       event.Time,
		Severity:   severityOf(event),
		Attributes: identityOf(id, view.Header(), event),
		Body:       bytes.Clone(payloadOf(event)),
	}
	if err := c.deliver(record); err != nil {
		return err
	}
	// 游标标的是「交出去了」，不是「送到了」：再往下的投递是 SDK 的事。
	c.cursor[id] = event.Seq
	return nil
}

// deliver 把一条记录过一遍脱敏和校验，然后交给接收器。调用方持着 [Coordinator.mu]。
//
// 源: packages/session/session-telemetry/src/coordinator.ts:201-217
func (c *Coordinator) deliver(record Record) error {
	redacted, err := runRules(c.rules, record)
	if err != nil {
		return fmt.Errorf("脱敏规则扣下了这条记录：%w", err)
	}
	if err := redacted.Validate(); err != nil {
		return err
	}
	c.sink.Emit(redacted)
	return nil
}

// markChunk 记下这个 "turn:step" 的第一条分块，返回它是不是**这一次**才记上的。
func (c *Coordinator) markChunk(id sessionlog.SessionID, key string) bool {
	seen, tracked := c.chunkSeen[id]
	if !tracked {
		seen = map[string]struct{}{}
		c.chunkSeen[id] = seen
	}
	if _, already := seen[key]; already {
		return false
	}
	seen[key] = struct{}{}
	return true
}

// contain 跑一步采集，把它的失败关在里面。
//
// 源: packages/session/session-telemetry/src/coordinator.ts:252-258
//
// 新增: DSH 兜的是 cordis `emit` 遇错即停、会饿死后面的订阅者。Go 里没有
// 那条总线，兜的理由换成另一件事：采集同步跑在 agent 循环的事件路径上，
// 而规则和接收器都是部署方挂上来的代码——它们炸了不该把循环一起炸掉。
// 见本包文档第 6 条。
func (c *Coordinator) contain(id sessionlog.SessionID, what string, step func() error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			c.logger.Warn("feature/telemetry: 采集这一步炸了",
				slog.String("session", string(id)),
				slog.String("step", what),
				slog.Any("panic", recovered))
		}
	}()

	if err := step(); err != nil {
		c.logger.Warn("feature/telemetry: 采集这一步没走完",
			slog.String("session", string(id)),
			slog.String("step", what),
			slog.Any("error", err))
	}
}

// errorBody 是 agent-error 那条运维记录的负载形状。
type errorBody struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

// shutdownRecord 拼出一个会话的干净退出标记。
//
// 源: packages/session/session-telemetry/src/coordinator.ts:265-273
func shutdownRecord(id sessionlog.SessionID, at int64) Record {
	return Record{
		Channel:  ChannelOps,
		Time:     at,
		Severity: SeverityInfo,
		Attributes: map[string]any{
			"telemetry.op": "shutdown",
			"session.id":   string(id),
		},
		Body: json.RawMessage(`{"op":"shutdown"}`),
	}
}

// severityOf 把一条事件自己的结果位映成那个预先定好的告警级别。
//
// 源: packages/session/session-telemetry/src/coordinator.ts:276-291
//
// 只有两种事件说得出「我出问题了」。别的一律 info——包括这个构建根本没听说过
// 的类型：它们的结果语义归它们自己的主人，这里不替它们判。所以这个 switch
// 是**开放**的，没有穷尽性断言。
func severityOf(event sessionlog.Event) Severity {
	switch event.Type {
	case sessionlog.EventToolResult:
		var data sessionlog.ToolResultData
		if err := json.Unmarshal(payloadOf(event), &data); err != nil {
			return SeverityInfo
		}
		if block, ok := data.Message.ToolResult(); ok && block.IsError {
			return SeverityError
		}
		return SeverityInfo

	case sessionlog.EventTurnEnd:
		var data sessionlog.TurnEndData
		if err := json.Unmarshal(payloadOf(event), &data); err != nil {
			return SeverityInfo
		}
		if data.Reason != nil && data.Reason.TurnEndReasonKind() == sessionlog.ReasonError {
			return SeverityError
		}
		return SeverityInfo

	default:
		return SeverityInfo
	}
}

// identityOf 拼出那几个最少的身份属性：信封字段，加上会话头上自带的那几样事实。
//
// 源: packages/session/session-telemetry/src/coordinator.ts:302-317
//
// 新增: DSH 判的是 `!== undefined`。Go 这三个字段在介质上都带 omitempty，
// 「缺席」和「零值」在字节上本来就是同一件事，所以这里判零值——两侧排出来的
// 属性表逐字段一致。
func identityOf(id sessionlog.SessionID, header sessionlog.SessionHeader, event sessionlog.Event) map[string]any {
	attributes := map[string]any{
		"session.id": string(id),
		"event.type": string(event.Type),
		"event.seq":  event.Seq,
	}
	if header.WorkspaceID != "" {
		// 新增: DSH 这条属性叫 `session.cwd`，值是宿主机工作目录。本仓库这个字段记的
		// 是归属而不是位置（见 [sessionlog.SessionHeader.WorkspaceID]），属性名跟着改，
		// 免得接收端把它当成一条能在本机上打开的路径。
		attributes["session.workspace_id"] = string(header.WorkspaceID)
	}
	if header.ParentSession != "" {
		attributes["session.parent_id"] = string(header.ParentSession)
	}
	// 那道耐久的分叉边界：一条分叉出来的流从这里开始，它前面那一段在父会话的
	// 流里，接收方靠 (parent_id, seed_length) 把两段缝起来。
	if header.SeedLength != 0 {
		attributes["session.seed_length"] = header.SeedLength
	}
	return attributes
}

// errorDetail 把一个错误归一成运维记录里那两个稳定字段。
//
// 源: packages/session/session-telemetry/src/coordinator.ts:294-299
//
// 新增: DSH 取的是 `error.name`，JS 的 Error 自带那个字段。Go 的 error 没有
// 名字，最接近的东西是它的动态类型——同一类失败的类型是同一个，正好用来分组，
// 而这正是 name 在接收方那边的用途。
func errorDetail(err error) (name, message string) {
	if err == nil {
		return "nil", ""
	}
	return fmt.Sprintf("%T", err), err.Error()
}

// payloadOf 给出一条事件的负载，空负载当 `{}`。
//
// 和 [sessionlog.Event.MarshalJSON] 同一条规则：每条事件都有负载，空负载是 `{}`。
// 这里补齐一次，body 就永远是一段合法 JSON，[Record.Validate] 也就不会因为
// 一条本来没有负载的事件而把它扣下。
func payloadOf(event sessionlog.Event) json.RawMessage {
	if len(event.Data) == 0 {
		return json.RawMessage(`{}`)
	}
	return event.Data
}

// chunkKey 拼出一条分块事件的 "turn:step" 投影键，负载读不回来时第二个返回值是 false。
//
// 源: packages/session/session-telemetry/src/coordinator.ts:166
func chunkKey(event sessionlog.Event) (string, bool) {
	var data sessionlog.AssistantChunkData
	if err := json.Unmarshal(payloadOf(event), &data); err != nil {
		return "", false
	}
	return fmt.Sprintf("%d:%d", data.Turn, data.Step), true
}
