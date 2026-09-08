// 本文件的作用：这条缝的门面——登记流程、列举、起一次尝试、接手一次尝试、
// 撤一次尝试，以及一次尝试怎么结算。
//
// 源: packages/credentials/authorization/src/index.ts:173-437（AuthorizationService）

package authorization

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/snight1983/ds-harness-go/credentials"
	"github.com/snight1983/ds-harness-go/invariants"
	"github.com/snight1983/ds-harness-go/storage"
	"github.com/snight1983/ds-harness-go/storage/domain"
)

// DefaultLease 是一份租约的默认时长。
//
// 新增: 本包独有。三十秒是照着「一次续租失败还剩两次机会」定的（心跳每十秒一次）：
// 短到一个死掉的副本不会把一个键堵上几分钟，长到一次介质抖动不会把一次正在等人
// 输入的授权判给别人。
const DefaultLease = 30 * time.Second

// Credentials 是本包用得着的那几件凭据能力。
//
// 新增: DSH 那边直接依赖整个 credentials 服务。这里收成两个方法，理由和
// [github.com/snight1983/ds-harness-go/feature/webhook] 一样：一个只需要「看一眼记录
// 在不在」和「记录变了叫我一声」的包，不该迫使装配方递进来一整个能写的提供方。
//
// [credentials.Provider] 天然满足它。
type Credentials interface {
	// DescribeRecord 用来核实流程跑完之后那条记录真的在。
	//
	// 源: packages/credentials/authorization/src/index.ts:425-431
	DescribeRecord(ctx context.Context, key credentials.Key) (credentials.RecordInfo, error)
	// SubscribeRecord 用来在一次尝试之内观察到那条记录被提交过。
	//
	// 源: packages/credentials/authorization/src/index.ts:404-412
	SubscribeRecord(listener credentials.RecordListener) func()
}

// Settled 是一次尝试结算之后，报给没起这次尝试的那些旁观者的一条。
//
// 源: packages/credentials/authorization/src/index.ts:433-437（authorization/settled）
type Settled struct {
	// Key 是哪条凭据记录。
	Key credentials.Key
	// Attempt 是哪次尝试。
	//
	// 新增: DSH 那条事件里没有它，理由同 [Outcome.AttemptID]。
	Attempt AttemptID
	// Method 是这次尝试用的方式。
	Method string
	// Settlement 是怎么结束的，三种，见 [Settlement]。
	Settlement Settlement
}

// SettledListener 是一个 [Settled] 的接收者。
type SettledListener func(settled Settled)

// Config 是建一个 [Service] 需要的东西。
//
// 源: packages/credentials/authorization/src/index.ts:173-196
type Config struct {
	// Domain 是域设施，尝试记录落在它上面。必填。
	//
	// 新增: DSH 那边这里是一个内存 Map，所以没有这个字段。
	Domain *domain.Facility

	// Credentials 是核实提交用的那两件能力。必填。
	Credentials Credentials

	// Replica 是本副本的身份，写进 [Attempt.Holder]。留空用一个随机串。
	//
	// 新增: 本包独有。它只用来回答「这条记录是不是我攥着的」，所以随机串够用；
	// 装配方想在日志里认出是哪台机器，就自己传一个有意义的。
	Replica string

	// Lease 是一份租约的时长，留空用 [DefaultLease]。
	//
	// 新增: 本包独有，理由见 [Attempt.HeldUntil]。
	Lease time.Duration

	// NewAttemptID 生成尝试身份，留空用 uuid。
	NewAttemptID func() string

	// Now 读时钟，留空用 time.Now。
	Now func() time.Time

	// Logger 记「界面炸了」「续租没成」这类事，留空用 slog.Default()。
	//
	// 留空不是丢弃：这里记的正是没人会主动去查、却必须留下痕迹的那类事。
	Logger *slog.Logger
}

// Service 是授权这条缝的门面。
//
// 源: packages/credentials/authorization/src/index.ts:173-437
//
// 零值不可用，请用 [Open]。
type Service struct {
	facility    *domain.Facility
	domain      *domain.Domain
	table       *domain.Table[Attempt]
	credentials Credentials
	replica     string
	lease       time.Duration
	newID       func() string
	clock       func() time.Time
	logger      *slog.Logger

	mu sync.Mutex
	// flows 是登记册：一个凭据键上最多一条流程。
	//
	// 源: packages/credentials/authorization/src/index.ts:198-218
	flows map[credentials.Key]Flow
	// running 是此刻在本副本上跑着的那几次尝试，供 [Service.Cancel] 就地叫停。
	running map[credentials.Key]*run
	// listeners 是 [Service.OnSettled] 登记的那些旁观者。
	//
	// 新增: 是切片不是 map，理由同 domain 那边的订阅表：map 的遍历顺序是随机的，
	// 而分发顺序随机会让一个依赖顺序的 bug 变成偶发。
	listeners []settledSubscription
	nextID    uint64
	closed    bool
}

// settledSubscription 是一次结算订阅留下的那一条，id 用来精确退订。
type settledSubscription struct {
	id       uint64
	listener SettledListener
}

// Open 打开这条缝：开域、接好那张尝试表。
//
// 新增: DSH 那边这条缝随 cordis 服务一起活，没有显式的打开。Go 里打开是显式的，
// 因为它要开一个域。
func Open(ctx context.Context, config Config) (*Service, error) {
	if config.Domain == nil {
		return nil, newError(CodeInvalidConfig, "打开授权服务需要一个域设施")
	}
	if config.Credentials == nil {
		// 少了它，一次流程「跑完了」就无从核实，而一条没写下去的凭据会被报成成功。
		return nil, newError(CodeInvalidConfig, "打开授权服务需要一个凭据面")
	}
	service := &Service{
		facility:    config.Domain,
		credentials: config.Credentials,
		replica:     config.Replica,
		lease:       config.Lease,
		newID:       config.NewAttemptID,
		clock:       config.Now,
		logger:      config.Logger,
		flows:       map[credentials.Key]Flow{},
		running:     map[credentials.Key]*run{},
	}
	if service.replica == "" {
		service.replica = uuid.NewString()
	}
	if service.lease <= 0 {
		service.lease = DefaultLease
	}
	if service.newID == nil {
		service.newID = uuid.NewString
	}
	if service.clock == nil {
		service.clock = time.Now
	}
	if service.logger == nil {
		service.logger = slog.Default()
	}

	opened, err := config.Domain.Open(ctx, Spec())
	if err != nil {
		return nil, wrapError(CodeStoreFailed, err, "打不开授权域")
	}
	table, err := domain.TableOf[Attempt](opened, TableName)
	if err != nil {
		// 开出来的域这条路上没人再会用它，不关就是一个一直占着域名的句柄。
		if closeErr := opened.Close(ctx); closeErr != nil {
			service.logger.Warn("authorization: 打开失败后关闭域也失败", slog.Any("error", closeErr))
		}
		return nil, wrapError(CodeStoreFailed, err, "拿不到授权尝试表")
	}
	service.domain = opened
	service.table = table
	return service, nil
}

// Close 停收新请求，撤掉本副本上跑着的那几次尝试，然后关域。
//
// 撤掉**不等于**结算：被撤的那几次尝试各自走完自己的结算路，谁的记录归谁。
// 一次没来得及结算的尝试留在介质上，下一次启动由 [Service.Resume] 或
// [Service.Cancel] 处置——这正是 [CodeStalled] 存在的意义。
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	running := make([]*run, 0, len(s.running))
	for _, r := range s.running {
		running = append(running, r)
	}
	s.mu.Unlock()

	for _, r := range running {
		r.cancel()
	}
	if err := s.domain.Close(ctx); err != nil {
		return wrapError(CodeStoreFailed, err, "关授权域没成")
	}
	return nil
}

// RegisterFlow 登记一条流程，返回撤销登记的那个函数。
//
// 源: packages/credentials/authorization/src/index.ts:202-218（registerFlow）
//
// 一个凭据键上只许有一条流程：两条流程认领同一个键的话，一次 [Service.Begin]
// 就得靠登记顺序决定跑哪条，而登记顺序是装配顺序的副产品。
//
// 撤销登记**不**动介质上正跑着或者停着的尝试：那条记录是别人的钱，
// 由 [Service.Cancel] 处置。
func (s *Service) RegisterFlow(flow Flow) (func(), error) {
	if err := flow.Validate(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, newError(CodeInvalidConfig, "授权服务已经关了")
	}
	if _, duplicate := s.flows[flow.Key]; duplicate {
		return nil, newError(CodeDuplicateFlow, "凭据 %q 上已经登记过一条授权流程了", flow.Key)
	}
	s.flows[flow.Key] = flow
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if current, ok := s.flows[flow.Key]; ok && current.Key == flow.Key {
				delete(s.flows, flow.Key)
			}
		})
	}, nil
}

// OnSettled 登记一个旁观者，返回退订的那个函数。
//
// 源: packages/credentials/authorization/src/index.ts:433-437
//
// 它是**旁观**路：起这次尝试的那一位从 [Service.Begin] 的返回值就知道结果了，
// 这条路给的是没起它的那些人——所以只有这里才需要 [SettlementFailed]。
func (s *Service) OnSettled(listener SettledListener) func() {
	if listener == nil {
		return func() {}
	}
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	s.listeners = append(s.listeners, settledSubscription{id: id, listener: listener})
	s.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			for index, subscription := range s.listeners {
				if subscription.id == id {
					s.listeners = append(s.listeners[:index], s.listeners[index+1:]...)
					return
				}
			}
		})
	}
}

// List 列出所有已登记流程此刻的样子。
//
// 源: packages/credentials/authorization/src/index.ts:220-240（list）
//
// 次序按凭据键排，好让界面两次刷新看到的顺序一样。
func (s *Service) List(ctx context.Context) ([]Entry, error) {
	s.mu.Lock()
	flows := make([]Flow, 0, len(s.flows))
	for _, flow := range s.flows {
		flows = append(flows, flow)
	}
	s.mu.Unlock()

	sortFlows(flows)
	entries := make([]Entry, 0, len(flows))
	for _, flow := range flows {
		entry, err := s.describe(ctx, flow)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// Describe 说一条已登记流程此刻的样子；这个键上没有流程时第二个返回值为假。
//
// 源: packages/credentials/authorization/src/index.ts:242-252（describe）
func (s *Service) Describe(ctx context.Context, key credentials.Key) (Entry, bool, error) {
	flow, ok := s.flow(key)
	if !ok {
		return Entry{}, false, nil
	}
	entry, err := s.describe(ctx, flow)
	if err != nil {
		return Entry{}, false, err
	}
	return entry, true, nil
}

// describe 把一条流程和介质上那条记录合成一条 [Entry]。
func (s *Service) describe(ctx context.Context, flow Flow) (Entry, error) {
	entry := Entry{Key: flow.Key, Label: flow.Label, Methods: flow.Methods}
	attempt, found, err := s.table.Get(ctx, string(flow.Key))
	if err != nil {
		return Entry{}, wrapError(CodeStoreFailed, err, "读不到凭据 %q 的授权尝试", flow.Key)
	}
	if !found {
		return entry, nil
	}
	entry.Attempt = attempt.ID
	if attempt.Held(s.now()) {
		entry.InFlight = true
	} else {
		entry.Stalled = true
	}
	return entry, nil
}

// Begin 起一次新的授权尝试，跑完才返回。
//
// 源: packages/credentials/authorization/src/index.ts:254-330（begin）
//
// 这个键上已经有一次尝试就拒：正攥在谁手里是 [CodeAlreadyInFlight]，
// 停在那儿没人管是 [CodeStalled]。后一种不自动重起，理由见 [CodeStalled]。
func (s *Service) Begin(ctx context.Context, request Request) (Outcome, error) {
	flow, method, err := s.resolve(request.Key, request.Method)
	if err != nil {
		return Outcome{}, err
	}
	if request.Interaction == nil {
		return Outcome{}, newError(CodeInvalidConfig, "凭据 %q 的授权请求没有界面", request.Key)
	}
	// 源: packages/credentials/authorization/src/index.ts:290-295。调用方递进来的 ctx
	// 已经撤了就直接说撤了，**不占那个槽位**：把一个已经撤了的 ctx 交给
	// [Flow.Run]，等于指望每条流程在自己第一次等待之前都记得查一遍。
	if err := ctx.Err(); err != nil {
		return Outcome{Status: StatusCancelled}, nil
	}

	now := s.now()
	attempt := Attempt{
		ID:        AttemptID(s.newID()),
		Key:       request.Key,
		Method:    method,
		Holder:    s.replica,
		HeldUntil: now.Add(s.lease),
		StartedAt: now,
		UpdatedAt: now,
	}
	switch err := s.table.Create(ctx, string(request.Key), attempt); {
	case isStorageCode(err, storage.CodeStaleRevision):
		// 别人抢在前面把这条记录建出来了。它是忙还是停，读一眼才知道。
		return Outcome{}, s.occupied(ctx, request.Key)
	case err != nil:
		return Outcome{}, wrapError(CodeStoreFailed, err, "凭据 %q 的授权尝试记不下去", request.Key)
	}
	return s.execute(ctx, flow, attempt, request.Interaction)
}

// Resume 接着一次停在介质上的尝试往下干，跑完才返回。
//
// 新增: 本包独有。DSH 没有接手这回事，理由见 [CodeStalled]。
//
// 接手是**从头再跑一遍** [Flow.Run]，流程靠 [Session.Resumed] 读回自己上次留下的
// 脚印，然后直接跳到那一步。方式跟着记录走，不跟着这次请求走：一次「设备码」的
// 尝试被接成「浏览器回调」的话，上一次留下的脚印就全白留了。
func (s *Service) Resume(ctx context.Context, request ResumeRequest) (Outcome, error) {
	if request.Interaction == nil {
		return Outcome{}, newError(CodeInvalidConfig, "凭据 %q 的授权接手请求没有界面", request.Key)
	}
	flow, ok := s.flow(request.Key)
	if !ok {
		return Outcome{}, newError(CodeNoFlow, "没有流程认领凭据 %q", request.Key)
	}
	if err := ctx.Err(); err != nil {
		return Outcome{Status: StatusCancelled}, nil
	}

	now := s.now()
	claimed, err := s.table.Update(ctx, string(request.Key), func(current Attempt) (Attempt, error) {
		if request.AttemptID != "" && current.ID != request.AttemptID {
			return current, newError(CodeNoAttempt,
				"凭据 %q 上停着的是 %q，不是要接手的 %q", request.Key, current.ID, request.AttemptID)
		}
		if current.Held(now) {
			return current, newError(CodeAlreadyInFlight,
				"凭据 %q 的这次授权尝试正攥在别处", request.Key)
		}
		if current.Cancelled {
			// 一次被撤了又没人来收尾的尝试，接手只会立刻再撤一次。
			return current, newError(CodeNoAttempt, "凭据 %q 上那次授权尝试已经被撤了", request.Key)
		}
		current.Holder = s.replica
		current.HeldUntil = now.Add(s.lease)
		current.UpdatedAt = now
		return current, nil
	})
	switch {
	case isDomainCode(err, domain.CodeMissingKey):
		return Outcome{}, newError(CodeNoAttempt, "凭据 %q 上没有停着的授权尝试", request.Key)
	case isStorageCode(err, storage.CodeStaleRevision):
		// [domain.Table.Update] 的重试次数用完了：一直有人在改这条记录，
		// 那就是有人正攥着它。
		return Outcome{}, newError(CodeAlreadyInFlight, "凭据 %q 的这次授权尝试一直有人在改", request.Key)
	case err != nil:
		var typed *Error
		if errors.As(err, &typed) {
			return Outcome{}, typed
		}
		return Outcome{}, wrapError(CodeStoreFailed, err, "接不下凭据 %q 的授权尝试", request.Key)
	}
	if !flow.offers(claimed.Method) {
		// 记录上写的方式这条流程已经不提供了。硬跑下去等于让流程读一份它看不懂的
		// 脚印，所以这里直接拒，交给调用方 [Service.Cancel] 之后重起。
		return Outcome{}, newError(CodeUnknownMethod,
			"凭据 %q 的授权流程不再提供 %q 这种方式", request.Key, claimed.Method)
	}
	return s.execute(ctx, flow, claimed, request.Interaction)
}

// Cancel 撤一次尝试。
//
// 新增: 本包独有。DSH 那边撤销是调用方自己那个 AbortSignal 的事，因为起这次尝试
// 的那一位一定还在。落到介质上之后不是了：撤的那一位和跑的那一位可能不在同一个
// 进程里，所以撤销得**写下去**，见 [Attempt.Cancelled]。
//
// attemptID 为空串表示撤这个键上的那一次，不管它是哪一次。
//
// 没人攥着的时候（一次停着的尝试）就地收掉并结算成 [SettlementCancelled]；
// 有人攥着的时候只写下标记，由攥着它的那个副本自己结算。
func (s *Service) Cancel(ctx context.Context, key credentials.Key, attemptID AttemptID) error {
	now := s.now()
	var held bool
	marked, err := s.table.Update(ctx, string(key), func(current Attempt) (Attempt, error) {
		if attemptID != "" && current.ID != attemptID {
			return current, newError(CodeNoAttempt,
				"凭据 %q 上的是 %q，不是要撤的 %q", key, current.ID, attemptID)
		}
		held = current.Held(now)
		current.Cancelled = true
		current.UpdatedAt = now
		return current, nil
	})
	switch {
	case isDomainCode(err, domain.CodeMissingKey):
		return newError(CodeNoAttempt, "凭据 %q 上没有授权尝试", key)
	case err != nil:
		var typed *Error
		if errors.As(err, &typed) {
			return typed
		}
		return wrapError(CodeStoreFailed, err, "撤不掉凭据 %q 的授权尝试", key)
	}

	if local, ok := s.lookupRunning(key, marked.ID); ok {
		local.markWithdrawn()
	}
	if held {
		// 攥着它的那个副本会在下一次续租、发问或者留脚印时读到这个标记，
		// 然后自己走结算那条路。这里再动手就是两个人结算同一次尝试。
		return nil
	}
	// 没人攥着：这次尝试的结算没有别人会做，只能在这里做完。
	s.release(ctx, marked)
	s.emit(Settled{Key: key, Attempt: marked.ID, Method: marked.Method, Settlement: SettlementCancelled})
	return nil
}

// execute 跑一次已经攥在手里的尝试，直到它结算。
//
// 源: packages/credentials/authorization/src/index.ts:300-437（attempt）
func (s *Service) execute(
	ctx context.Context,
	flow Flow,
	attempt Attempt,
	interaction Interaction,
) (Outcome, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	r := &run{
		service:     s,
		interaction: interaction,
		id:          attempt.ID,
		key:         string(attempt.Key),
		method:      attempt.Method,
		resumed:     attempt.Progress,
		cancel:      cancel,
	}
	if err := s.track(r, attempt.Key); err != nil {
		s.release(ctx, attempt)
		return Outcome{}, err
	}
	defer s.untrack(attempt.Key, r)

	// 源: packages/credentials/authorization/src/index.ts:404-412。**在跑之前**就订上：
	// 一条在订阅生效之前提交的记录不算这次尝试的功劳。
	unsubscribe := s.credentials.SubscribeRecord(func(key credentials.Key) {
		if key == attempt.Key {
			r.markCommitted()
		}
	})
	defer unsubscribe()

	beat, stopBeat := context.WithCancel(runCtx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.heartbeat(beat)
	}()

	runErr := flow.Run(runCtx, r)
	stopBeat()
	<-done

	settlement, outcomeErr := s.classify(ctx, r, attempt, runErr)
	if settlement == "" {
		// 被接手了：这条记录已经是别人的，**不许结算也不许删**。
		return Outcome{}, outcomeErr
	}
	// 结算这一步不跟着调用方的 ctx 走：一个撤了自己 ctx 的调用方不该把这个键
	// 堵在介质上，那会让之后每一次 [Service.Begin] 都撞 [CodeStalled]。
	s.release(context.WithoutCancel(ctx), attempt)
	s.emit(Settled{
		Key:        attempt.Key,
		Attempt:    attempt.ID,
		Method:     attempt.Method,
		Settlement: settlement,
	})
	if outcomeErr != nil {
		return Outcome{}, outcomeErr
	}
	return Outcome{Status: Status(settlement), AttemptID: attempt.ID}, nil
}

// classify 判一次跑完的尝试算什么收场。
//
// 源: packages/credentials/authorization/src/index.ts:414-431
//
// 返回空的 [Settlement] 表示「不许结算」——只有被接手是这种。
func (s *Service) classify(
	ctx context.Context,
	r *run,
	attempt Attempt,
	runErr error,
) (Settlement, error) {
	committed, declined, withdrawn, fenced := r.snapshot()
	if fenced {
		return "", newError(CodeTakenOver, "凭据 %q 的这次授权尝试已经归别的副本了", attempt.Key)
	}
	if runErr != nil {
		switch {
		case declined || errors.Is(runErr, CodeDeclined):
			return SettlementCancelled, nil
		case withdrawn:
			return SettlementCancelled, nil
		case errors.Is(runErr, context.Canceled) && ctx.Err() != nil:
			// 调用方自己撤了。这是取消，不是故障。
			return SettlementCancelled, nil
		default:
			return SettlementFailed, runErr
		}
	}
	// 源: packages/credentials/authorization/src/index.ts:425-431。两条都要：
	// 尝试之内观察到过一次提交，而且尝试之后那条记录还在。少了前一条，一条早就
	// 配好的记录会让任何流程都「成功」；少了后一条，一次提交完又被删掉的记录
	// 会被报成配好了。
	if !committed {
		return SettlementFailed, newError(CodeNotCommitted,
			"凭据 %q 的授权流程跑完了，但这次尝试里没写下那条记录", attempt.Key)
	}
	info, err := s.credentials.DescribeRecord(ctx, attempt.Key)
	if err != nil {
		return SettlementFailed, wrapError(CodeStoreFailed, err, "核不了凭据 %q 写下去没有", attempt.Key)
	}
	if !info.Configured {
		return SettlementFailed, newError(CodeNotCommitted,
			"凭据 %q 的授权流程跑完了，但那条记录已经不在了", attempt.Key)
	}
	return SettlementAuthorized, nil
}

// release 把一次结算掉的尝试从介质上收走。
//
// 新增: 本包独有。[domain.Table.Delete] 是无条件的（domain.go:466），所以这里做不到
// 「只在这条记录还是我的时候才删」。可接受的理由是这条路只在本副本还攥着一份没
// 过期的租约时才走得到——租约一断，[run.heartbeat] 会把这次跑标成被接手，而被接手
// 的那条路根本不到这里。
func (s *Service) release(ctx context.Context, attempt Attempt) {
	current, found, err := s.table.Get(ctx, string(attempt.Key))
	if err != nil {
		s.warnf("凭据 %q 的授权尝试收尾时读不动：%v", attempt.Key, err)
		return
	}
	if !found {
		return
	}
	if current.ID != attempt.ID {
		// 已经是另一次尝试了，不是我的。
		return
	}
	if _, err := s.table.Delete(ctx, string(attempt.Key)); err != nil {
		// 删不掉的后果是这个键上留着一条停着的记录，下一次 [Service.Begin] 会撞
		// [CodeStalled]。这是可恢复的，所以不把它变成这次尝试的失败。
		s.warnf("凭据 %q 的授权尝试删不掉：%v", attempt.Key, err)
	}
}

// occupied 说一个已经被占住的键该报哪种失败。
func (s *Service) occupied(ctx context.Context, key credentials.Key) error {
	attempt, found, err := s.table.Get(ctx, string(key))
	if err != nil {
		return wrapError(CodeStoreFailed, err, "读不到凭据 %q 的授权尝试", key)
	}
	if !found {
		// 建的时候还在，读的时候没了。这一瞬结算掉了，让调用方再试一次。
		return newError(CodeAlreadyInFlight, "凭据 %q 的这次授权尝试刚刚才结算", key)
	}
	if attempt.Held(s.now()) {
		return newError(CodeAlreadyInFlight, "凭据 %q 的授权尝试正攥在别处", key)
	}
	return newError(CodeStalled, "凭据 %q 上停着一次没结算的授权尝试 %q", key, attempt.ID)
}

// resolve 把一次请求解析成「跑哪条流程、用哪种方式」。
//
// 源: packages/credentials/authorization/src/index.ts:276-288
func (s *Service) resolve(key credentials.Key, method string) (Flow, string, error) {
	flow, ok := s.flow(key)
	if !ok {
		return Flow{}, "", newError(CodeNoFlow, "没有流程认领凭据 %q", key)
	}
	if method == "" {
		// 源: packages/credentials/authorization/src/index.ts:282。一个都不点名就用
		// 最推荐的那一种，见 [Flow.Methods]。
		return flow, flow.Methods[0].ID, nil
	}
	if !flow.offers(method) {
		return Flow{}, "", newError(CodeUnknownMethod,
			"凭据 %q 的授权流程不提供 %q 这种方式", key, method)
	}
	return flow, method, nil
}

// flow 查一个键上登记的流程。
func (s *Service) flow(key credentials.Key) (Flow, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	flow, ok := s.flows[key]
	return flow, ok
}

// track 把一次跑登记进本副本的在跑表；这个键上已经有一次在跑就拒。
//
// 它挡的是**同一个副本内**的重入：介质那一关只挡得住两个副本，挡不住同一个进程
// 里两次 [Service.Begin] 之间那条读了又写的缝。
func (s *Service) track(r *run, key credentials.Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return newError(CodeInvalidConfig, "授权服务已经关了")
	}
	if _, busy := s.running[key]; busy {
		return newError(CodeAlreadyInFlight, "凭据 %q 的授权尝试正攥在本副本手里", key)
	}
	s.running[key] = r
	return nil
}

// untrack 把一次跑从在跑表里摘掉。
func (s *Service) untrack(key credentials.Key, r *run) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current, ok := s.running[key]; ok && current == r {
		delete(s.running, key)
	}
}

// lookupRunning 查本副本上跑着的那次尝试是不是这一次。
func (s *Service) lookupRunning(key credentials.Key, id AttemptID) (*run, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.running[key]
	if !ok || r.id != id {
		return nil, false
	}
	return r, true
}

// emit 把一次结算发给所有旁观者。
//
// 源: packages/credentials/authorization/src/index.ts:433-437
//
// 一个炸了的旁观者不影响别的旁观者，也不影响这次尝试的结果——它已经结算完了。
func (s *Service) emit(settled Settled) {
	s.mu.Lock()
	listeners := make([]settledSubscription, len(s.listeners))
	copy(listeners, s.listeners)
	s.mu.Unlock()

	for _, subscription := range listeners {
		s.deliver(subscription.listener, settled)
	}
}

// deliver 送一条给一个旁观者，兜住它自己炸的那种。
func (s *Service) deliver(listener SettledListener, settled Settled) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		// 新增: 一条不变量违例不许被这道网兜住。[invariants.Fail] 是**故意** panic 的：
		// 它说的是「程序写错了」，不是一个可以处置的运行期状况。把它降级成一行日志，
		// 等于把本包唯一的自检悄悄关掉。
		if violation, isViolation := recovered.(*invariants.Error); isViolation {
			panic(violation)
		}
		s.warnf("凭据 %q 的授权结算把一个旁观者炸了：%v", settled.Key, recovered)
	}()
	listener(settled)
}

// now 读一次时钟。
func (s *Service) now() time.Time { return s.clock() }

// warnf 记一条没人会主动去查、却必须留下痕迹的事。
func (s *Service) warnf(format string, args ...any) {
	s.logger.Warn("authorization: " + fmt.Sprintf(format, args...))
}

// isDomainCode 说这条错是不是域这一层的某一类。
//
// 新增: [domain.Error] 和 [storage.Error] 都是「errors.As 出来看 Code」那一套
// （domain/error.go:44-53），不像本包的 [Code] 自己就是哨兵。两个小函数把这个差别
// 挡在调用点之外。
func isDomainCode(err error, code domain.ErrorCode) bool {
	var typed *domain.Error
	return errors.As(err, &typed) && typed.Code == code
}

// isStorageCode 说这条错是不是后端那一层的某一类。
func isStorageCode(err error, code storage.ErrorCode) bool {
	var typed *storage.Error
	return errors.As(err, &typed) && typed.Code == code
}

// sortFlows 按凭据键排，好让界面两次刷新看到的顺序一样。
func sortFlows(flows []Flow) {
	slices.SortFunc(flows, func(left, right Flow) int {
		return strings.Compare(string(left.Key), string(right.Key))
	})
}
