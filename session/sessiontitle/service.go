// 本文件的作用：那个往日志里写标题的服务。它管三件事：那个确定性的兜底标题
// 什么时候落地、一个可选的生成器什么时候被排期和被取代、以及一次用户改名怎么
// 把前两件事都钉住。
//
// 立场：**日志是唯一的真相**。这个服务不缓存任何标题，[Service.Get] 每次都从
// 日志现折（[FoldSnapshot]）。它自己那份可变状态只有并发账——版本号、排着的活、
// 跑着的活——那些东西一条都不进日志，重启之后从零开始，而标题本身一个字都不会丢。
//
// 源: packages/session/session-title/src/index.ts:261-791

package sessiontitle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
)

// ErrDisposed 表示服务已经关掉了，不再接活。
var ErrDisposed = errors.New("sessiontitle: 服务已经关掉了")

// ErrNotLive 表示传进来的会话在这个装配里已经不活了。
var ErrNotLive = errors.New("sessiontitle: 这个会话不活了")

// ErrStale 表示一次生成在收尾时发现自己已经被取代，结果不许落地。
//
// 新增: DSH 那边这几种情况抛的是不同文案的普通 Error。收成一个哨兵，是因为
// 调用方对它们的处理只有一种：这次白跑了，别记进日志、也别当成事故报警。
var ErrStale = errors.New("sessiontitle: 这次标题生成已经被取代")

// ErrBadProvider 表示一个生成器的登记或者它的产出不成立。
var ErrBadProvider = errors.New("sessiontitle: 标题生成器不成立")

// Service 是那个由日志托底的标题服务。
//
// 源: packages/session/session-title/src/index.ts:293-829（SessionTitleService）
//
// 零值不可用，用 [New] 建。它可以被多个 goroutine 同时使用。
type Service struct {
	config Config
	logger *slog.Logger

	// lifetime 是整个服务的寿命，[Service.Close] 取消它。每一次生成都挂在它
	// 下面，所以关服务会把在途的生成一起取消掉。
	lifetime context.Context
	stop     context.CancelCauseFunc

	// pending 是在途的自动生成 goroutine。[Service.Close] 等它们退干净。
	pending sync.WaitGroup

	mu sync.Mutex
	// disposed 在 [Service.Close] 之后为真。
	disposed bool
	// current 是当下那个唯一的生成器登记；nil 表示没有生成器。
	current *registration
	// work 是按会话记的并发账。它按会话 id 归档而不是按会话对象——Go 的接口
	// 值不保证可比较，拿它当 map 键会在某些实现上当场 panic。
	work map[session.SessionID]*workState
}

// registration 是一次确切的生成器登记世代。
//
// 源: packages/session/session-title/src/index.ts:226-230
//
// 「世代」这个词是认真的：同一个生成器注销再登记一次是**两个** registration，
// 而在途的活认的是自己出发时那一个。这样一次注销就能干净地把老活全部作废，
// 不需要给生成器本身加版本号。
type registration struct {
	provider Provider
	// closing 在注销开始之后为真：不再接新活，已经跑起来的等它自己退。
	closing bool
	// active 是这个登记名下在途的生成，注销时等它们退干净。
	active sync.WaitGroup
}

// pendingWork 是排着队、等它那份请求头落进日志的自动生成。
//
// 源: packages/session/session-title/src/index.ts:233-237
type pendingWork struct {
	registration *registration
	// revision 是这次活出发时占住的那个会话内版本号。
	revision int
	// throughSeq 钉死这次活的输入范围：只看 seq 不超过它的事件。
	throughSeq int
}

// activeWork 是当下唯一被允许为某个会话落地结果的那次生成。
//
// 源: packages/session/session-title/src/index.ts:240-243
type activeWork struct {
	pendingWork
	ctx    context.Context
	cancel context.CancelCauseFunc
	// stopWatch 解开挂在调用方 context 上的那个转发器；为 nil 表示没挂。
	stopWatch func() bool
}

// workState 是某一个活会话的可变并发账。
//
// 源: packages/session/session-title/src/index.ts:246-251
//
// 新增: DSH 那边还有一个 fallback?: Promise<...> 字段，用来给并发的
// ensureFallback 去重。Go 这边不需要：兜底的推导和追加是在服务这把锁里**一口气**
// 做完的，中间没有任何让出的机会，所以两个并发的调用天然被排成一前一后，
// 后到的那个会看见前一个已经写好的标题然后直接返回。
type workState struct {
	// revision 是这个会话上一路往上走的版本号，每次取代加一。
	revision int
	// pending 是排着的那次活；nil 表示没有。
	pending *pendingWork
	// active 是跑着的那次活；nil 表示没有。
	active *activeWork
}

// New 建一个标题服务。
//
// 源: packages/session/session-title/src/index.ts:276-342
//
// 新增: DSH 的构造函数还在 ctx 上登记那个投影单元、并且订阅三个事件。Go 这边
// 两件事都挪成了显式的入口（成例见 todo 那一包的三条分臂）：投影是
// [RegisterProjection]，三个订阅是 [Service.OnEvent]、[Service.OnMainRequest]、
// [Service.OnSessionDisposed]。装配方接哪几条自己定——一份只做离线回放的装配
// 一条都不接，[Service.Get] 照样管用。
func New(config Config) (*Service, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	lifetime, stop := context.WithCancelCause(context.Background())
	return &Service{
		config:   config,
		logger:   logger,
		lifetime: lifetime,
		stop:     stop,
		work:     map[session.SessionID]*workState{},
	}, nil
}

// Close 关掉这个服务：取消一切在途的生成，等它们退干净，再丢掉全部并发账。
//
// 源: packages/session/session-title/src/index.ts:292-302
//
// 它是幂等的。关掉之后每一个入口都返回 [ErrDisposed]，那几个钩子静静地什么都不做
// ——一个正在卸载的装配不该因为还有事件在路上而报错。
func (s *Service) Close() error {
	s.mu.Lock()
	if s.disposed {
		s.mu.Unlock()
		return nil
	}
	s.disposed = true
	if s.current != nil {
		s.current.closing = true
		s.current = nil
	}
	for _, state := range s.work {
		state.pending = nil
		if state.active != nil {
			state.active.cancel(ErrDisposed)
		}
	}
	s.mu.Unlock()

	s.stop(ErrDisposed)
	// 在锁外等：在途的生成收尾时要拿这把锁去清自己那一格。
	s.pending.Wait()

	s.mu.Lock()
	clear(s.work)
	s.mu.Unlock()
	return nil
}

// Get 从一个活着的或者回放出来的会话身上读最新那个标题。
//
// 源: packages/session/session-title/src/index.ts:349-351
//
// 第二个返回值为假表示这个会话还没有过标题。它不需要服务是活的，也不看任何
// 并发账——纯粹是一次折叠。
func (s *Service) Get(sess Session) (Snapshot, bool, error) {
	return FoldSnapshot(sess.Events())
}

// Rename 接受一个用户显式给的标题。
//
// 源: packages/session/session-title/src/index.ts:364-384
//
// 它追加一条来源是 [SourceUser] 的标题事件，而那**钉住**这个标题：在途的自动
// 生成当场被取代，后面再来的用户消息也不再排期。想解开只有一条路——一次显式的
// [Service.Refresh]。
//
// 标题洗完什么都不剩时返回 [ErrInvalidTitle]；那是这个方法唯一一个「怪输入」的
// 失败，其余（服务关了、会话不活了）都是别的哨兵。
func (s *Service) Rename(sess Session, title string) (Snapshot, error) {
	normalized := NormalizeSessionTitle(title, s.config.MaxTitleBytes)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkUsable(sess); err != nil {
		return Snapshot{}, err
	}
	if normalized == "" {
		return Snapshot{}, ErrInvalidTitle
	}
	s.supersede(s.stateFor(sess), errors.New("sessiontitle: 用户改名取代了自动生成"))
	if err := s.append(sess, EventData{
		Title:       normalized,
		MessageSeqs: nil,
		Source:      Source{Kind: SourceUser},
	}); err != nil {
		return Snapshot{}, err
	}
	snapshot, ok, err := s.Get(sess)
	if err != nil {
		return Snapshot{}, err
	}
	if !ok {
		// 上面刚追加成功，折不出来说明这条日志的读写两侧对不上。
		return Snapshot{}, errors.New("sessiontitle: 刚写下去的标题折不回来")
	}
	return snapshot, nil
}

// Refresh 显式地再跑一次生成器；没有登记生成器时就把那个兜底标题落地。
//
// 源: packages/session/session-title/src/index.ts:393-427
//
// 它是那把**解钉**的钥匙：一个被用户钉住的标题只有走这里才会被盖掉。所以没有
// 生成器的时候它也不是空操作——它会重新推一遍兜底标题然后盖上去。
//
// 第二个返回值为假表示这个会话里还没有任何够格当素材的文本。
//
// 新增: 这个方法是**同步**的（DSH 那边 await 的那次生成在这里就是一次直接调用），
// 所以调用方的 goroutine 会一直等到生成器返回。取消走 ctx。
func (s *Service) Refresh(ctx context.Context, sess Session) (Snapshot, bool, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, false, err
	}

	s.mu.Lock()
	if err := s.checkUsable(sess); err != nil {
		s.mu.Unlock()
		return Snapshot{}, false, err
	}

	events := sess.Events()
	messages := CollectMessages(events, -1)
	usable := s.current != nil && !s.current.closing && len(messages) > 0

	if !usable {
		snapshot, hasTitle, err := s.Get(sess)
		if err != nil {
			s.mu.Unlock()
			return Snapshot{}, false, err
		}
		// 解钉那条路：一个立着的用户标题不许让下面的 ensureFallback 短路成
		// 空操作，所以这里重新推一份兜底盖上去。
		if hasTitle && snapshot.Source.Kind == SourceUser && len(messages) > 0 {
			if err := s.appendFallback(sess, messages[0]); err != nil {
				s.mu.Unlock()
				return Snapshot{}, false, err
			}
			s.mu.Unlock()
			return s.Get(sess)
		}
		fallback, ok, err := s.ensureFallbackLocked(sess)
		s.mu.Unlock()
		if err != nil {
			return Snapshot{}, false, err
		}
		if err := ctx.Err(); err != nil {
			return Snapshot{}, false, err
		}
		return fallback, ok, nil
	}

	state := s.stateFor(sess)
	revision := s.supersede(state, errors.New("sessiontitle: 显式刷新取代了更早的生成"))
	work := s.activateLocked(state, pendingWork{
		registration: s.current,
		revision:     revision,
		throughSeq:   messages[len(messages)-1].Seq,
	}, ctx)
	s.mu.Unlock()

	route, err := routeOf(events)
	if err != nil {
		s.finish(sess, work)
		return Snapshot{}, false, err
	}
	return s.runProvider(sess, work, route)
}

// Register 登记那个唯一可选的标题生成器。
//
// 源: packages/session/session-title/src/index.ts:435-460
//
// 返回的注销函数会先把这个登记名下排着的活丢掉、跑着的活取消掉，然后**等它们
// 退干净**才返回。等这一下是必须的：不等的话，一次注销返回之后仍然可能有一个
// 旧生成器的结果落进日志。
//
// 注销函数是幂等的。
func (s *Service) Register(provider Provider) (func(), error) {
	if err := validateProvider(provider); err != nil {
		return nil, err
	}

	s.mu.Lock()
	if s.disposed {
		s.mu.Unlock()
		return nil, ErrDisposed
	}
	if s.current != nil {
		id := s.current.provider.ID()
		s.mu.Unlock()
		return nil, fmt.Errorf("%w：已经登记过生成器 %q 了", ErrBadProvider, id)
	}
	entry := &registration{provider: provider}
	s.current = entry
	s.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			cause := fmt.Errorf("sessiontitle: 生成器 %q 被注销了", provider.ID())
			s.mu.Lock()
			entry.closing = true
			for _, state := range s.work {
				if state.pending != nil && state.pending.registration == entry {
					state.pending = nil
				}
				if state.active != nil && state.active.registration == entry {
					state.active.cancel(cause)
				}
			}
			s.mu.Unlock()

			entry.active.Wait()

			s.mu.Lock()
			if s.current == entry {
				s.current = nil
			}
			s.mu.Unlock()
		})
	}, nil
}

// OnEvent 是「会话追加了一条事件」这条通知的入口。
//
// 源: packages/session/session-title/src/index.ts:320-331
//
// 装配方在每一条事件**提交之后**调它。本包只关心两种类型，别的静静地忽略。
//
// 它可能会往同一条日志上追加一条标题事件（那个兜底标题），所以
// [Session.Append] 上那条「不许同步地调回本服务」的约定在这里最要紧。
func (s *Service) OnEvent(sess Session, event session.Event) {
	switch event.Type {
	case session.EventUserMessage:
		s.onUserMessage(sess, event)
	case session.EventRequestHeader:
		s.onRequestHeader(sess, event)
	}
}

// OnMainRequest 是「主对话循环正要发一次请求」这条通知的入口。
//
// 源: packages/session/session-title/src/index.ts:332-335、503-516
//
// 它存在的理由很窄：一次**路由没变**的请求不会往日志里写新的 request/header，
// 于是 [Service.OnEvent] 那条路等不到自己要的东西，排着的活会一直排下去。这条
// 钩子补上那个缺口——它从请求本身读出路由，条件是那份请求头的折叠已经追上了
// （日志上最后一个步骤边界是一条 step/start，而且它比这次活钉的输入范围新）。
//
// 新增: DSH 那边这个钩子只拿 GenerateOptions，会话是拿 options.sessionId 去
// ctx.sessions 里查出来的。Go 这边会话由装配方直接递进来——它本来就要做那次
// 查找才知道该把这条通知发给谁，本包再要一份会话仓库是多余的。
func (s *Service) OnMainRequest(sess Session, options llm.GenerateOptions) {
	if options.SessionID == "" || !options.AgentLoop || sess == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.disposed {
		return
	}
	state, ok := s.work[sess.ID()]
	if !ok || state.pending == nil {
		return
	}

	events := sess.Events()
	boundary, hasBoundary := lastStepBoundary(events)
	if !hasBoundary || boundary.Type != session.EventStepStart || boundary.Seq <= state.pending.throughSeq {
		return
	}
	route, err := routeOf(events)
	if err != nil || route == nil || route.Provider != options.Provider || route.Model != options.Model {
		return
	}
	s.startPendingLocked(sess, state, ModelProvenance{Provider: options.Provider, Model: options.Model})
}

// OnSessionDisposed 是「一个会话被销毁了」这条通知的入口。
//
// 源: packages/session/session-title/src/index.ts:336-341
//
// 取消这个会话上跑着的活并丢掉它那一格并发账。不等在途的活退——那件事由
// [Service.Close] 和注销函数统一负责，这里只是让它们尽早知道自己白跑了。
func (s *Service) OnSessionDisposed(sess Session) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.work[sess.ID()]
	if !ok {
		return
	}
	if state.active != nil {
		state.active.cancel(errors.New("sessiontitle: 会话在标题生成期间被销毁了"))
	}
	delete(s.work, sess.ID())
}

// onUserMessage 给一条够格的用户消息排上兜底和（如果有生成器的话）自动生成。
//
// 源: packages/session/session-title/src/index.ts:463-487
func (s *Service) onUserMessage(sess Session, event session.Event) {
	if _, eligible := titleTextOf(event); !eligible {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.disposed {
		return
	}
	snapshot, hasTitle, err := s.Get(sess)
	if err != nil {
		s.logger.Warn("sessiontitle: 读不回当前标题，这条用户消息不排期",
			slog.String("session", string(sess.ID())), slog.Any("error", err))
		return
	}
	// 一次用户改名把标题钉住了：没有任何自动修订可以盖过它。
	if hasTitle && snapshot.Source.Kind == SourceUser {
		return
	}

	if s.current != nil && !s.current.closing {
		// 「第一条提示词」那一档要同时满足三件事：这不是一个分叉出来的会话
		// （分叉会话的第一条消息前面还压着一整段继承来的历史，拿它起名会得到
		// 一个和上下文对不上的名字）、这确实是第一条够格的消息、而且还没有
		// 任何标题。
		messages := CollectMessages(sess.Events(), event.Seq)
		schedule := s.current.provider.Automatic() == ModeAllPrompts ||
			(sess.Header().ParentSession == "" && len(messages) == 1 && !hasTitle)
		if schedule {
			state := s.stateFor(sess)
			revision := s.supersede(state, errors.New("sessiontitle: 更新的用户消息取代了标题生成"))
			state.pending = &pendingWork{registration: s.current, revision: revision, throughSeq: event.Seq}
		}
	}

	if _, _, err := s.ensureFallbackLocked(sess); err != nil {
		s.logger.Warn("sessiontitle: 兜底标题写不进去",
			slog.String("session", string(sess.ID())), slog.Any("error", err))
	}
}

// onRequestHeader 等到排着的活那份确切主请求路由落进日志之后才把它发出去。
//
// 源: packages/session/session-title/src/index.ts:490-500
//
// 为什么要等：生成器想知道主对话正在用哪条路由（好跟着用、或者有意避开）。
// 在请求头落定之前发出去，它读到的会是**上一次**请求的路由。
func (s *Service) onRequestHeader(sess Session, event session.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.disposed {
		return
	}
	state, ok := s.work[sess.ID()]
	if !ok || state.pending == nil || state.pending.throughSeq >= event.Seq {
		return
	}
	data, err := session.DecodeData(event)
	if err != nil {
		return
	}
	header, ok := data.(session.RequestHeaderData)
	if !ok {
		return
	}
	s.startPendingLocked(sess, state, ModelProvenance{
		Provider: header.Header.Config.Provider,
		Model:    header.Header.Config.Model,
	})
}

// startPendingLocked 消化一次排着的修订，把它的生成扔到后台去跑。
//
// 源: packages/session/session-title/src/index.ts:519-539
//
// 调用方必须持有 s.mu。
//
// 新增: DSH 那边这里把整件事推进一个 promise，进去之后再把「登记还是不是那一个」
// 「版本号还对不对」重新查一遍——因为那个 microtask 和这里之间隔着别的代码。
// Go 这边启用（占住 active 那一格）是在同一把锁里同步做完的，中间没有任何东西
// 能插进来，所以那几条重查在这一步是多余的。它们在**生成器返回之后**仍然必须
// 做一遍（见 [Service.assertCurrent]），那时候确实隔了很久。
func (s *Service) startPendingLocked(sess Session, state *workState, route ModelProvenance) {
	pending := *state.pending
	state.pending = nil
	work := s.activateLocked(state, pending, nil)

	s.pending.Add(1)
	pending.registration.active.Add(1)
	go func() {
		defer s.pending.Done()
		defer pending.registration.active.Done()

		if _, _, err := s.runProvider(sess, work, &route); err != nil {
			// 被取代和被取消都不是事故：那正是这套版本号机制该干的事。
			if errors.Is(err, ErrStale) || errors.Is(err, context.Canceled) || work.ctx.Err() != nil {
				return
			}
			s.logger.Warn("sessiontitle: 自动标题生成失败",
				slog.String("session", string(sess.ID())),
				slog.String("provider", string(pending.registration.provider.ID())),
				slog.Any("error", err))
		}
	}()
}

// runProvider 跑一次生成并接受它的结果。
//
// 源: packages/session/session-title/src/index.ts:552-584
//
// 三次「我还算数吗」的检查夹着两次真正花时间的动作（兜底落地、生成器调用），
// 每一次都可能因为被取代而当场退出。最后那次尤其要紧：它挡住的是一个跑了很久的
// 旧生成器把结果盖到一个更新的标题上面。
func (s *Service) runProvider(sess Session, work *activeWork, route *ModelProvenance) (Snapshot, bool, error) {
	defer s.finish(sess, work)

	if err := s.assertCurrent(sess, work); err != nil {
		return Snapshot{}, false, err
	}
	s.mu.Lock()
	_, _, fallbackErr := s.ensureFallbackLocked(sess)
	s.mu.Unlock()
	if fallbackErr != nil {
		return Snapshot{}, false, fallbackErr
	}
	if err := s.assertCurrent(sess, work); err != nil {
		return Snapshot{}, false, err
	}

	messages := CollectMessages(sess.Events(), work.throughSeq)
	result, err := work.registration.provider.Generate(work.ctx, ProviderRequest{
		Session:  sess,
		Messages: messages,
		Route:    route,
	})
	if err != nil {
		return Snapshot{}, false, err
	}

	accepted, err := s.validateResult(result, messages)
	if err != nil {
		return Snapshot{}, false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.assertCurrentLocked(sess, work); err != nil {
		return Snapshot{}, false, err
	}
	source := Source{Kind: SourceProvider, Provider: work.registration.provider.ID()}
	if accepted.Model != nil {
		model := *accepted.Model
		source.Model = &model
	}
	if err := s.append(sess, EventData{
		Title:       accepted.Title,
		MessageSeqs: accepted.MessageSeqs,
		Source:      source,
	}); err != nil {
		return Snapshot{}, false, err
	}
	return s.Get(sess)
}

// validateResult 校验并归一化一次生成的产出。
//
// 源: packages/session/session-title/src/index.ts:587-633
//
// 引的 seq 那一段的严格程度是有意的：必须非空、必须全部来自交给它的那份快照、
// 而且必须按快照里的**顺序严格递增**。这不是形式主义——MessageSeqs 是事后回答
// 「这个名字是从哪句话来的」的唯一凭据，一份乱填的清单会让那个问题永远答不上来，
// 而它坏掉的时候没有任何别的迹象。
//
// 新增: DSH 那边还要逐个字段判类型（结果是 unknown）。Go 的类型系统已经把那一整
// 段消掉了，剩下的只有值域上的检查。
func (s *Service) validateResult(result ProviderResult, messages []UserMessage) (ProviderResult, error) {
	title := NormalizeSessionTitle(result.Title, s.config.MaxTitleBytes)
	if title == "" {
		return ProviderResult{}, fmt.Errorf("%w：产出的标题洗完是空的", ErrBadProvider)
	}
	if len(result.MessageSeqs) == 0 {
		return ProviderResult{}, fmt.Errorf("%w：产出至少要指明一条来源消息的 seq", ErrBadProvider)
	}

	order := make(map[int]int, len(messages))
	for index, message := range messages {
		order[message.Seq] = index
	}
	seqs := make([]int, 0, len(result.MessageSeqs))
	previous := -1
	for _, seq := range result.MessageSeqs {
		index, known := order[seq]
		if seq < 0 || !known || index <= previous {
			return ProviderResult{}, fmt.Errorf(
				"%w：产出引的 messageSeqs 必须来自请求里那份快照、不许重复、而且要按顺序，坏在 %d",
				ErrBadProvider, seq)
		}
		seqs = append(seqs, seq)
		previous = index
	}

	accepted := ProviderResult{Title: title, MessageSeqs: seqs}
	if result.Model != nil {
		if result.Model.Provider == "" || result.Model.Model == "" {
			return ProviderResult{}, fmt.Errorf(
				"%w：产出带的 model 里 provider 和 model 都不许是空串", ErrBadProvider)
		}
		model := *result.Model
		accepted.Model = &model
	}
	return accepted, nil
}

// assertCurrent 判一次收尾时，它的生成器、版本、会话、取消信号是不是都还算数。
//
// 源: packages/session/session-title/src/index.ts:636-648
func (s *Service) assertCurrent(sess Session, work *activeWork) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.assertCurrentLocked(sess, work)
}

// assertCurrentLocked 是 [Service.assertCurrent] 的持锁版本。
func (s *Service) assertCurrentLocked(sess Session, work *activeWork) error {
	if s.disposed {
		return ErrDisposed
	}
	if err := work.ctx.Err(); err != nil {
		return fmt.Errorf("%w：%w", ErrStale, context.Cause(work.ctx))
	}
	state, ok := s.work[sess.ID()]
	if s.current != work.registration || !ok || state.active != work || state.revision != work.revision {
		return ErrStale
	}
	if !s.live(sess) {
		return fmt.Errorf("%w：%w", ErrStale, ErrNotLive)
	}
	return nil
}

// activateLocked 从一次固定的修订上建出并公布那次跑着的活。
//
// 源: packages/session/session-title/src/index.ts:651-663
//
// upstream 是调用方自己的 context（只有 [Service.Refresh] 会给）。它和服务寿命
// 一起决定这次活什么时候被取消。
//
// 新增: DSH 那边是 AbortSignal.any([...])——把三个信号合成一个。Go 的 context
// 只能有一个父，所以这里挂在服务寿命下面，再用 [context.AfterFunc] 把调用方那条
// 转发进来。stopWatch 在收尾时解开它，免得一个长命的调用方 context 攒着一堆
// 早就跑完的活的回调。
//
// 调用方必须持有 s.mu。
func (s *Service) activateLocked(state *workState, pending pendingWork, upstream context.Context) *activeWork {
	ctx, cancel := context.WithCancelCause(s.lifetime)
	work := &activeWork{pendingWork: pending, ctx: ctx, cancel: cancel}
	if upstream != nil {
		work.stopWatch = context.AfterFunc(upstream, func() { cancel(context.Cause(upstream)) })
	}
	state.active = work
	return work
}

// finish 收掉一次跑完的活：让出 active 那一格、解开转发器、释放 context。
func (s *Service) finish(sess Session, work *activeWork) {
	s.mu.Lock()
	if state, ok := s.work[sess.ID()]; ok && state.active == work {
		state.active = nil
	}
	s.mu.Unlock()

	if work.stopWatch != nil {
		work.stopWatch()
	}
	work.cancel(nil)
}

// supersede 取消更老的那次活，并占住这个会话的下一个版本号。
//
// 源: packages/session/session-title/src/index.ts:666-671
//
// 调用方必须持有 s.mu。
func (s *Service) supersede(state *workState, cause error) int {
	if state.active != nil {
		state.active.cancel(cause)
	}
	state.pending = nil
	state.revision++
	return state.revision
}

// stateFor 交出一个会话的可变并发账，没有就建一格。
//
// 源: packages/session/session-title/src/index.ts:674-681
//
// 调用方必须持有 s.mu。
func (s *Service) stateFor(sess Session) *workState {
	state, ok := s.work[sess.ID()]
	if !ok {
		state = &workState{}
		s.work[sess.ID()] = state
	}
	return state
}

// appendFallback 把那个确定性的兜底标题盖到当前立着的东西上面。
//
// 源: packages/session/session-title/src/index.ts:745-753
//
// 「盖上去」正是它的用途：它只被 [Service.Refresh] 的解钉那条路调，那条路要的
// 就是把一个用户钉住的标题换掉。推不出兜底（过完两道闸门是空的）就什么都不追加。
//
// 调用方必须持有 s.mu。
func (s *Service) appendFallback(sess Session, first UserMessage) error {
	title := FallbackSessionTitle(first.Text, s.config.FallbackMaxWords, s.config.FallbackMaxBytes)
	if title == "" {
		return nil
	}
	return s.append(sess, EventData{
		Title:       title,
		MessageSeqs: []int{first.Seq},
		Source:      Source{Kind: SourceFallback},
	})
}

// ensureFallbackLocked 在会话还没有任何标题时给它落一个兜底标题。
//
// 源: packages/session/session-title/src/index.ts:756-790
//
// 第二个返回值为假表示这个会话还推不出标题（没有够格的素材，或者素材洗完是空的）。
//
// 新增: DSH 那边这是一个 async 方法，中间隔着一个 microtask，所以它需要一整套
// 东西来兜住那个空档：一份按会话记的在途 promise 去重、一次进去之后重做的服务
// 存活检查、一次重做的会话活性检查、以及一次「进来之前有没有别人已经写了标题」
// 的重查。Go 这边推导和追加是在同一把锁里一口气做完的，那个空档根本不存在，
// 所以那四样全部消失，只剩下最前面那一次读。
//
// 调用方必须持有 s.mu。
func (s *Service) ensureFallbackLocked(sess Session) (Snapshot, bool, error) {
	if s.disposed {
		return Snapshot{}, false, ErrDisposed
	}
	current, hasTitle, err := s.Get(sess)
	if err != nil {
		return Snapshot{}, false, err
	}
	if hasTitle {
		return current, true, nil
	}
	messages := CollectMessages(sess.Events(), -1)
	if len(messages) == 0 {
		return Snapshot{}, false, nil
	}
	if err := s.appendFallback(sess, messages[0]); err != nil {
		return Snapshot{}, false, err
	}
	return s.Get(sess)
}

// append 校验一条标题负载并把它追加进日志。
//
// 校验走 [CheckEventData]：DSH 那边这条约束由 invariants 服务在事件发布之前拦下来，
// Go 这边没有那个拦截层，所以本包在自己每一次追加之前亲自过一遍。它挡的是这个
// 包自己的 bug——一条来源和引用对不上的标题事件一旦落进持久日志就再也改不了了。
//
// 调用方必须持有 s.mu。
func (s *Service) append(sess Session, data EventData) error {
	if err := CheckEventData(data); err != nil {
		return err
	}
	return sess.Append(EventSessionTitle, data)
}

// checkUsable 判服务还开着、而且这个会话还活着。
//
// 调用方必须持有 s.mu。
func (s *Service) checkUsable(sess Session) error {
	if s.disposed {
		return ErrDisposed
	}
	if !s.live(sess) {
		return fmt.Errorf("%w：%q", ErrNotLive, sess.ID())
	}
	return nil
}

// live 问装配方这个会话此刻还活不活；没给谓词就一律当成活的。
func (s *Service) live(sess Session) bool {
	return s.config.IsLive == nil || s.config.IsLive(sess)
}

// validateProvider 在登记之前拒掉一个不成立的生成器。
//
// 源: packages/session/session-title/src/index.ts:722-736
//
// 新增: DSH 那边还要判 id 是不是字符串、generate 是不是函数——那些是 TS 在
// 运行期补类型。Go 这边只剩下值域上的两条。
func validateProvider(provider Provider) error {
	if provider == nil {
		return fmt.Errorf("%w：生成器不许是 nil", ErrBadProvider)
	}
	if provider.ID() == "" {
		return fmt.Errorf("%w：生成器的 id 不许是空串", ErrBadProvider)
	}
	switch provider.Automatic() {
	case ModeFirstPrompt, ModeAllPrompts:
	default:
		return fmt.Errorf("%w：认不得的自动节奏 %q", ErrBadProvider, provider.Automatic())
	}
	return nil
}

// routeOf 从一条日志里折出当前那条主请求路由；nil 表示还没有记过任何一条。
//
// 源: packages/session/session-title/src/index.ts:424-425、510
//
// 新增: DSH 那边是 session.requestHeader()——一个会话自己缓着的折叠结果。本包的
// [Session] 接口有意不要那个方法：它是活会话那一层的优化，而这里每一次调用都
// 已经在做别的 O(日志) 的遍历了，多折一遍换掉接口上一个方法是划算的。
func routeOf(events []session.Event) (*ModelProvenance, error) {
	header, ok, err := session.FoldRequestHeader(events, session.EpochHeader{}, false)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return &ModelProvenance{Provider: header.Config.Provider, Model: header.Config.Model}, nil
}

// lastStepBoundary 找日志上最后一条步骤边界事件。
//
// 源: packages/session/session-title/src/index.ts:509
func lastStepBoundary(events []session.Event) (session.Event, bool) {
	for index := len(events) - 1; index >= 0; index-- {
		switch events[index].Type {
		case session.EventStepStart, session.EventStepEnd:
			return events[index], true
		}
	}
	return session.Event{}, false
}
