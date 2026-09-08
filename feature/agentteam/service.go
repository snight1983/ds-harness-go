// 本文件的作用：这个包的门面——装配、那三张表的读写、以及「发起这次操作的是谁」
// 这道解算。
//
// 源: packages/experimental/agent-team/src/index.ts:57-120（TeamService 的装配那一段）

package agentteam

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/snight1983/ds-harness-go/feature/subagent"
	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/storage"
	"github.com/snight1983/ds-harness-go/storage/domain"
)

// 部署上限的默认值，与 DSH 逐个对齐。
//
// 源: packages/experimental/agent-team/src/index.ts:43-47
const (
	// DefaultMaxMembers 是一支团队默认最多几个队友。
	DefaultMaxMembers = 8
	// DefaultMaxTasks 是一块板上默认最多几条没删掉的任务。
	//
	// 这道上限在本包比在 DSH 那边更要紧：整块板是介质上的一条记录，板越大，
	// 两个副本同时改它撞条件写的机会越大，见 [CodeBoardContended]。
	DefaultMaxTasks = 256
	// DefaultMaxPendingMessages 是一个收件人默认最多压着几条没送到的消息。
	DefaultMaxPendingMessages = 64
	// DefaultMaxMessageBytes 是一条消息正文默认最多多少字节。
	DefaultMaxMessageBytes = 65536
)

// DefaultClaimTTL 是一次投递认领默认多久算掉队。
//
// 新增: DSH 没有这个概念——它的投递活在一个进程的内存里，进程没了消息就跟着没了。
// 本包的消息在介质上，抓着它的副本可能整个消失，所以要一段宽限期把它捡回来。
// 三十秒是照着「一次真的投递远快于此，而一次卡住的投递不该把收件箱堵上几分钟」定的。
const DefaultClaimTTL = 30 * time.Second

// Subagents 是本包用得着的那几件子 Agent 能力。
//
// 新增: 接口定义在使用方。[github.com/snight1983/ds-harness-go/feature/subagent.Runtime]
// 天然满足它，但本包只用这四个方法，收窄之后用例里递一个替身就够了，
// 不必装一整套可续运行期。
//
// **队友就是可续子 Agent**，本包不另起一套。差别只在称呼和授权：
// 子 Agent 那边是父子，这边是同侪；而同侪之间的打断仍旧凭队长那份祖先权
// （[subagent.AuthorityAncestor]），因为队友本来就都是队长起出来的。
type Subagents interface {
	// StartContinuable 起一个队友。
	//
	// 它的 [subagent.ContinuableStartSpec.ChildID] 允许调用方**预留**身份，
	// 这正好对上花名册那条 [PhaseProvisioning] 到 [PhaseActive] 的路：
	// 先把名字和身份占下来落盘，再去起会话，起不起得来都有记录。
	StartContinuable(ctx context.Context, spec subagent.ContinuableStartSpec) (subagent.ContinuableStart, error)
	// Followup 给一个队友排一个回合，对应 [DeliveryWakeup]。
	Followup(ctx context.Context, parent agent.Agent, childID sessionlog.SessionID, content llm.Content, options subagent.FollowupOptions) (llm.MessageID, error)
	// Interrupt 打断一个队友当下那段活动。
	Interrupt(targetSessionID sessionlog.SessionID, authority subagent.InterruptAuthority) error
}

// Agents 是「本副本上此刻有没有这个会话的活 agent」这一问。
//
// 新增: DSH 那边是一个进程内的 agent 注册表，问它等于问全世界。本包多副本，
// 所以这一问的答案只对**本副本**成立：问不到不等于那个队友死了，
// 它可能正活在另一台机器上。这个区别直接决定了 [DeliveryQuiet] 的处置，
// 见 [Service.dispatch]。
type Agents interface {
	// Agent 交出本副本上那个会话的活 agent；不在就是 false。
	Agent(id sessionlog.SessionID) (agent.Agent, bool)
}

// Config 是建一个 [Service] 需要的东西。
//
// 源: packages/experimental/agent-team/src/index.ts:60-67（TeamService.Config）
type Config struct {
	// Domain 是域设施，团队状态落在它上面。必填。
	//
	// 新增: DSH 那边团队状态是从队长会话日志折出来的一份内存投影，所以没有这个字段。
	Domain *domain.Facility

	// Subagents 是起队友、给队友排回合、打断队友用的那几件能力。必填。
	Subagents Subagents

	// Agents 是「这个会话此刻在不在本副本上」这一问。必填。
	Agents Agents

	// Replica 是本副本的身份，写进 [Message.Claimant]。留空用一个随机串。
	Replica string

	// MaxMembers 是一支团队最多几个队友，留空用 [DefaultMaxMembers]。
	MaxMembers int
	// MaxTasks 是一块板上最多几条没删掉的任务，留空用 [DefaultMaxTasks]。
	MaxTasks int
	// MaxPendingMessages 是一个收件人最多压着几条没送到的消息，
	// 留空用 [DefaultMaxPendingMessages]。
	MaxPendingMessages int
	// MaxMessageBytes 是一条消息正文最多多少字节，留空用 [DefaultMaxMessageBytes]。
	MaxMessageBytes int
	// ClaimTTL 是一次投递认领多久算掉队，留空用 [DefaultClaimTTL]。
	ClaimTTL time.Duration
	// PollInterval 是 [Service.Wait] 两次探团队状态之间隔多久，
	// 留空用 [DefaultPollInterval]。
	PollInterval time.Duration

	// NewMessageID 生成消息身份，留空用 uuid。
	NewMessageID func() string
	// Now 读时钟，留空用 time.Now。
	Now func() time.Time
	// Logger 记「一次投递没送成」这类事，留空用 slog.Default()。
	Logger *slog.Logger
}

// Service 是团队这条缝的门面。
//
// 源: packages/experimental/agent-team/src/index.ts:57-120
//
// 零值不可用，请用 [Open]。
type Service struct {
	facility  *domain.Facility
	domain    *domain.Domain
	rosters   *domain.Table[Roster]
	boards    *domain.Table[Board]
	messages  *domain.Table[Message]
	subagents Subagents
	agents    Agents
	replica   string
	config    limits
	newID     func() string
	clock     func() time.Time
	logger    *slog.Logger
}

// limits 是解算好的那几道上限。
type limits struct {
	MaxMembers         int
	MaxTasks           int
	MaxPendingMessages int
	MaxMessageBytes    int
	ClaimTTL           time.Duration
	PollInterval       time.Duration
}

// Open 打开这条缝：开域、接好那三张表。
//
// 新增: DSH 那边这条缝随 cordis 服务一起活，没有显式的打开。Go 里打开是显式的，
// 因为它要开一个域。
func Open(ctx context.Context, config Config) (*Service, error) {
	if config.Domain == nil {
		return nil, newError(CodeInvalidConfig, "打开团队服务需要一个域设施")
	}
	if config.Subagents == nil {
		// 少了它，一个队友根本起不起来，而花名册上会留下一行永远停在 provisioning。
		return nil, newError(CodeInvalidConfig, "打开团队服务需要一套子 Agent 能力")
	}
	if config.Agents == nil {
		// 少了它，安静投递没有收件箱可写，而它会被悄悄降级成唤醒——那会平白多起一个回合。
		return nil, newError(CodeInvalidConfig, "打开团队服务需要一个活 agent 的查询面")
	}
	service := &Service{
		facility:  config.Domain,
		subagents: config.Subagents,
		agents:    config.Agents,
		replica:   config.Replica,
		config: limits{
			MaxMembers:         positive(config.MaxMembers, DefaultMaxMembers),
			MaxTasks:           positive(config.MaxTasks, DefaultMaxTasks),
			MaxPendingMessages: positive(config.MaxPendingMessages, DefaultMaxPendingMessages),
			MaxMessageBytes:    positive(config.MaxMessageBytes, DefaultMaxMessageBytes),
			ClaimTTL:           config.ClaimTTL,
			PollInterval:       config.PollInterval,
		},
		newID:  config.NewMessageID,
		clock:  config.Now,
		logger: config.Logger,
	}
	if service.replica == "" {
		service.replica = uuid.NewString()
	}
	if service.config.ClaimTTL <= 0 {
		service.config.ClaimTTL = DefaultClaimTTL
	}
	if service.config.PollInterval <= 0 {
		service.config.PollInterval = DefaultPollInterval
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
		return nil, wrapError(CodeStoreFailed, err, "打不开团队域")
	}
	rosters, rosterErr := domain.TableOf[Roster](opened, RosterTable)
	boards, boardErr := domain.TableOf[Board](opened, BoardTable)
	messages, messageErr := domain.TableOf[Message](opened, MessageTable)
	for _, tableErr := range []error{rosterErr, boardErr, messageErr} {
		if tableErr == nil {
			continue
		}
		// 开出来的域这条路上没人再会用它，不关就是一个一直占着域名的句柄。
		if closeErr := opened.Close(ctx); closeErr != nil {
			service.logger.Warn("agentteam: 打开失败后关闭域也失败", slog.Any("error", closeErr))
		}
		return nil, wrapError(CodeStoreFailed, tableErr, "拿不到团队表")
	}
	service.domain = opened
	service.rosters = rosters
	service.boards = boards
	service.messages = messages
	return service, nil
}

// Close 关掉这条缝的域。
//
// 它**不**停任何一个队友：队友是子 Agent，寿命归子 Agent 运行期管；
// 一支团队的状态全在介质上，本副本关掉之后别的副本照样读得到。
func (s *Service) Close(ctx context.Context) error {
	if err := s.domain.Close(ctx); err != nil {
		return wrapError(CodeStoreFailed, err, "关团队域没成")
	}
	return nil
}

// positive 取一个正数配置，非正就用默认值。
func positive(value int, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

// ---- 那三张表的读写 ----

// roster 读一支团队的花名册。
func (s *Service) roster(ctx context.Context, team TeamID) (Roster, error) {
	roster, found, err := s.rosters.Get(ctx, string(team))
	if err != nil {
		return Roster{}, storeError(err, "读团队 %q 的花名册没成", team)
	}
	if !found {
		return Roster{}, newError(CodeTeamNotFound, "没有团队 %q", team)
	}
	return roster, nil
}

// board 读一支团队的任务板；还没建过就给一块空板。
//
// 空板不落盘：一支从来没建过任务的团队不该在介质上留一条记录，
// 而「读到空板」和「读到一块有零条任务的板」在每一条读路径上都是同一件事。
func (s *Service) board(ctx context.Context, team TeamID) (Board, error) {
	board, found, err := s.boards.Get(ctx, string(team))
	if err != nil {
		return Board{}, storeError(err, "读团队 %q 的任务板没成", team)
	}
	if !found {
		return Board{Team: team, NextTaskNumber: 1}, nil
	}
	return board, nil
}

// updateBoard 对一块板做一次条件写。
//
// 这是本包最要紧的一条路：读、算、写在一次
// [github.com/snight1983/ds-harness-go/storage/domain.Table.Update] 之内，
// 别的副本插进来就整个重跑一遍回调。撞穿重试上限报 [CodeBoardContended]——
// 那和「你手里那份任务过期了」（[CodeTaskStaleRevision]）是两回事：
// 前者该退避重来，后者该重读再来。
//
// 回调拿到的是一份深复制（[Board.Clone]），所以它可以放心改；重跑一遍时拿到的是
// 刚读回来的最新值，上一轮改的那些不会带过来。
func (s *Service) updateBoard(ctx context.Context, team TeamID, mutate func(Board) (Board, error)) (Board, error) {
	if _, found, err := s.boards.Get(ctx, string(team)); err != nil {
		return Board{}, storeError(err, "读团队 %q 的任务板没成", team)
	} else if !found {
		// 头一次建任务：先把空板落下去，再走条件写那条路。分两步不会丢改动——
		// Create 撞上别的副本说明板已经有了，接着 Update 就好。
		blank := Board{Team: team, NextTaskNumber: 1}
		if err := s.boards.Create(ctx, string(team), blank); err != nil && !isStorageCode(err, storage.CodeStaleRevision) {
			return Board{}, storeError(err, "建团队 %q 的任务板没成", team)
		}
	}
	next, err := s.boards.Update(ctx, string(team), func(board Board) (Board, error) {
		return mutate(board.Clone())
	})
	if err != nil {
		return Board{}, s.mutationError(err, "改团队 %q 的任务板没成", team)
	}
	return next, nil
}

// updateRoster 对一份花名册做一次条件写，语义同 [Service.updateBoard]。
func (s *Service) updateRoster(ctx context.Context, team TeamID, mutate func(Roster) (Roster, error)) (Roster, error) {
	next, err := s.rosters.Update(ctx, string(team), func(roster Roster) (Roster, error) {
		return mutate(roster.Clone())
	})
	if err != nil {
		return Roster{}, s.mutationError(err, "改团队 %q 的花名册没成", team)
	}
	return next, nil
}

// mutationError 把一次条件写的失败译成本包的分类。
//
// 回调自己报的错要原样交回去：它是业务判断（版本号对不上、没权限、成圈），
// 译成 [CodeStoreFailed] 会把一条给模型看的话变成一条介质故障。
func (s *Service) mutationError(err error, format string, args ...any) error {
	var teamErr *Error
	if errors.As(err, &teamErr) {
		return teamErr
	}
	var graphErr *GraphError
	if errors.As(err, &graphErr) {
		return newError(graphErr.Violation.Code(), "%s", graphErr.Message)
	}
	return storeError(err, format, args...)
}

// resolveActiveMember 把一个名字解算成一个在岗成员的会话身份。
//
// 源: packages/experimental/agent-team/src/roster.ts:40-60（resolveActiveMember）
//
// [LeadName] 解算成队长自己；别的名字必须在花名册上而且已经在岗——一个还在
// provisioning 的队友接不了任务，也收不了消息。
func resolveActiveMember(roster Roster, name string) (sessionlog.SessionID, error) {
	if name == LeadName {
		return sessionlog.SessionID(roster.Lead), nil
	}
	member, found := roster.Member(name)
	if !found {
		return "", newError(CodeMemberNotFound, "花名册上没有叫 %q 的", name)
	}
	if member.Phase != PhaseActive {
		return "", newError(CodeMemberNotFound, "队友 %q 还没在岗（%s）", name, member.Phase)
	}
	return member.ID, nil
}

// ResolveCaller 把一个会话身份解算成这次操作的发起人，见 [Caller]。
//
// 源: packages/experimental/agent-team/src/roster.ts:70-90（membership）
//
// 队长和队友都能发起绝大多数操作；差别只在几个动作上（起队友、改派任务），
// 那几处各自查 [CodeLeadRequired]。不在这支团队里的一律拒。
func (s *Service) ResolveCaller(ctx context.Context, team TeamID, id sessionlog.SessionID) (Caller, error) {
	roster, err := s.roster(ctx, team)
	if err != nil {
		return Caller{}, err
	}
	if string(id) == roster.Lead {
		return Caller{Team: team, ID: id, Name: LeadName, Lead: true}, nil
	}
	member, found := roster.MemberByID(string(id))
	if !found {
		return Caller{}, newError(CodeNotMember, "会话 %q 不属于团队 %q", id, team)
	}
	return Caller{Team: team, ID: id, Name: member.Name, Lead: false}, nil
}
