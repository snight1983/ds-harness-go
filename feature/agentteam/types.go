// 本文件的作用：团队的身份、落在介质上的那几个值、给出去的视图，以及入口处那几道
// 归一化。
//
// 源: packages/experimental/agent-team/src/types.ts
// 源: packages/experimental/agent-team/src/validation.ts

package agentteam

import (
	"regexp"
	"strings"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/sessionlog"
)

// TeamID 是一支团队的身份，就是队长那个会话的身份。
//
// 源: packages/experimental/agent-team/src/types.ts:7-16
//
// 团队不是单独建出来的：一个顶层会话一旦有了第一个队友，它就是一支团队的队长。
// 所以这里没有「建团队」这个动作，只有「往一个会话上挂第一个队友」。
type TeamID string

// TaskID 是一条共享任务在团队之内的身份，形如 "task-1"。
//
// 源: packages/experimental/agent-team/src/types.ts:18-27
type TaskID string

// MessageID 是一条队友之间的消息的身份。
//
// 源: packages/experimental/agent-team/src/types.ts:29-38
type MessageID string

// MemberPhase 是一个队友在花名册上的落盘状态。
//
// 源: packages/experimental/agent-team/src/types.ts:40-41
//
// 只有一条合法的走向：[PhaseProvisioning] 到 [PhaseActive] 或 [PhaseFailed]，
// 到了之后就不再动。先落一条 provisioning 再去起会话，是为了让「起到一半进程没了」
// 这件事在介质上留得下痕迹——否则重启之后既没有队友也没有失败记录，
// 那个半开的会话就成了孤儿。
type MemberPhase string

const (
	// PhaseProvisioning 是名字已经占下了，会话还没起来。
	PhaseProvisioning MemberPhase = "provisioning"
	// PhaseActive 是会话起来了，而且收下了开场那一问。
	PhaseActive MemberPhase = "active"
	// PhaseFailed 是这个队友没起来，原因记在 [Member.Failure] 里。
	PhaseFailed MemberPhase = "failed"
)

// MemberContext 说一个队友的会话是全新开的还是从队长那儿分出来的。
//
// 源: packages/experimental/agent-team/src/types.ts:50
type MemberContext string

const (
	// ContextFresh 是全新开一个会话，什么上文都不带。
	ContextFresh MemberContext = "fresh"
	// ContextFork 是从队长此刻的上文分一支出来。
	ContextFork MemberContext = "fork"
)

// Member 是花名册上一个队友落盘时的整份值。
//
// 源: packages/experimental/agent-team/src/types.ts:44-52（TeamMemberSnapshot）
//
// 名字、提供方、上文来源三样定下来就不许改：它们描述的是这个队友**是怎么起来的**，
// 而那件事已经发生过了。改它们等于让一份记录说一件没发生过的事。
type Member struct {
	// ID 是这个队友那个会话的身份。
	ID sessionlog.SessionID `json:"id"`
	// Name 是队友之间互相称呼用的名字，团队之内唯一。
	Name string `json:"name"`
	// Description 是这个队友负责什么，给别的队友看。
	Description string `json:"description"`
	// Provider 是起这个会话用的那个子 Agent 提供方。
	Provider string `json:"provider"`
	// Context 是全新开还是从队长分一支，见 [MemberContext]。
	Context MemberContext `json:"context"`
	// Phase 是此刻走到哪一步，见 [MemberPhase]。
	Phase MemberPhase `json:"phase"`
	// Failure 是没起来的原因，只在 [PhaseFailed] 时有。
	Failure string `json:"failure,omitempty"`
}

// MemberStatus 是花名册那一行给出去时的状态，比 [MemberPhase] 多两档运行期信息。
//
// 源: packages/experimental/agent-team/src/types.ts:56-65（TeamMemberView.status）
type MemberStatus string

const (
	// StatusRunning 是这个队友此刻正在跑一个回合。
	StatusRunning MemberStatus = "running"
	// StatusIdle 是会话活着，但此刻没在跑。
	StatusIdle MemberStatus = "idle"
	// StatusInactive 是记录在，但这个副本上没有活着的会话。
	//
	// 新增: DSH 只有「本进程有没有这个 agent」一种解释。本包多副本，所以这一档说的
	// 是「**本副本**看不见它」，不是「它死了」——它可能正活在另一台机器上。
	StatusInactive MemberStatus = "inactive"
	// StatusProvisioning 对应 [PhaseProvisioning]。
	StatusProvisioning MemberStatus = "provisioning"
	// StatusFailed 对应 [PhaseFailed]。
	StatusFailed MemberStatus = "failed"
)

// MemberRole 说一行是队长还是队友。
type MemberRole string

const (
	// RoleLead 是队长，也就是团队那个根会话。
	RoleLead MemberRole = "lead"
	// RoleTeammate 是被队长起出来的队友。
	RoleTeammate MemberRole = "teammate"
)

// LeadName 是队长在消息和任务里被称呼的那个名字。
//
// 源: packages/experimental/agent-team/src/roster.ts:205
//
// 它是保留字：[ValidateMemberName] 拒绝任何叫这个名字的队友，否则「发给 lead」
// 会指向两个人。
const LeadName = "lead"

// MemberView 是花名册上一行给出去时的样子。
//
// 源: packages/experimental/agent-team/src/types.ts:54-65（TeamMemberView）
type MemberView struct {
	// ID 是这个成员那个会话的身份。
	ID sessionlog.SessionID `json:"id"`
	// Name 是称呼用的名字；队长是 [LeadName]。
	Name string `json:"name"`
	// Role 是队长还是队友。
	Role MemberRole `json:"role"`
	// Status 是此刻的状态，见 [MemberStatus]。
	Status MemberStatus `json:"status"`
	// Description 是这个队友负责什么；队长没有。
	Description string `json:"description,omitempty"`
	// Provider 是起这个会话用的提供方；队长没有。
	Provider string `json:"provider,omitempty"`
	// Context 是全新开还是分支；队长没有。
	Context MemberContext `json:"context,omitempty"`
	// Diagnostics 是这一行上要说给人听的那几句话，比如没起来的原因。
	Diagnostics []string `json:"diagnostics,omitempty"`
}

// TaskStatus 是一条共享任务的落盘状态。
//
// 源: packages/experimental/agent-team/src/types.ts:67-68
type TaskStatus string

const (
	// TaskPending 是还没人认领。
	TaskPending TaskStatus = "pending"
	// TaskInProgress 是有人认领了正在做。
	TaskInProgress TaskStatus = "in_progress"
	// TaskCompleted 是做完了。
	TaskCompleted TaskStatus = "completed"
	// TaskDeleted 是删了。
	//
	// 删是打标记而不是把那一行拿走：任务身份是按序号发的，真拿走会让一条已经被别的
	// 任务引用过的身份重新可用，于是一次陈旧的请求会落到一条新任务上。
	TaskDeleted TaskStatus = "deleted"
)

// Task 是板上一条任务落盘时的整份值。
//
// 源: packages/experimental/agent-team/src/types.ts:70-79（TeamTaskSnapshot）
type Task struct {
	// ID 是这条任务的身份。
	ID TaskID `json:"id"`
	// Rev 是这条任务被改过几次，从 1 起数，每改一次加一。
	//
	// **这是给模型看的那一层版本号**，和
	// [github.com/snight1983/ds-harness-go/storage.Revision] 是两回事：
	//
	//	Task.Rev            模型手里那份任务有多旧。对不上报 [CodeTaskStaleRevision]，
	//	                    意思是「重读一遍再来」。判断在改板那次条件写的回调里做。
	//	storage.Revision    介质上**整块板**那条记录有多旧。只被
	//	                    [github.com/snight1983/ds-harness-go/storage/domain.Table.Update]
	//	                    自己用来挡别的副本，不外露；撞穿重试上限报的是
	//	                    [CodeBoardContended]。
	//
	// 两层混在一起会让模型收到「你的版本过期了」，而实际发生的是「板太挤」——
	// 前者该重读，后者该退避，处置正好相反。
	Rev int `json:"rev"`
	// Subject 是一句话说清这条任务是什么。
	Subject string `json:"subject"`
	// Description 是展开说。
	Description string `json:"description"`
	// Status 是此刻的状态。
	Status TaskStatus `json:"status"`
	// Owner 是认领它的那个成员的会话身份；没人认领就是空。
	Owner sessionlog.SessionID `json:"owner,omitempty"`
	// BlockedBy 是挡在它前面的那几条任务。
	//
	// **边住在任务值里面**，没有单独一张边表。理由是本包的板整块是介质上的一条记录：
	// 边放在里面之后，改一条任务就是对一把键的一次条件写，跨任务的原子性白拿——
	// 而 [github.com/snight1983/ds-harness-go/storage/domain] 本来不提供跨表事务。
	BlockedBy []TaskID `json:"blockedBy,omitempty"`
	// WriteScopes 是这条任务打算改哪几处，见 [NormalizeWriteScope]。
	WriteScopes []string `json:"writeScopes,omitempty"`
}

// Clone 深复制一条任务，好让改板那次回调不会改到原来那份。
func (t Task) Clone() Task {
	t.BlockedBy = append([]TaskID(nil), t.BlockedBy...)
	t.WriteScopes = append([]string(nil), t.WriteScopes...)
	return t
}

// TaskView 是一条任务给出去时的样子，比 [Task] 多三样算出来的。
//
// 源: packages/experimental/agent-team/src/types.ts:81-92（TeamTaskView）
type TaskView struct {
	// Task 是那条任务本身。
	Task
	// OwnerName 是认领人的名字，比会话身份好读。
	OwnerName string `json:"ownerName,omitempty"`
	// Ready 说它的前置是不是都做完了。
	Ready bool `json:"ready"`
	// ScopeWarnings 是「这条任务要改的地方和别的任务撞了」那几句话。
	//
	// 源: packages/experimental/agent-team/src/types.ts:91
	//
	// **只是提醒，不是锁**：它不拦任何一次改动，只把重叠说给模型听，让它自己去协调。
	// 做成锁需要一套跨副本的文件级租约，而那件事的成本远大于它挡住的问题。
	ScopeWarnings []string `json:"scopeWarnings,omitempty"`
}

// TeamView 是花名册加任务板此刻的样子。
//
// 源: packages/experimental/agent-team/src/types.ts:94-98（TeamView）
type TeamView struct {
	// Members 是花名册，队长排在第一行。
	Members []MemberView `json:"members"`
	// Tasks 是板上没删掉的那些任务。
	Tasks []TaskView `json:"tasks"`
}

// Delivery 说一条消息该怎么送到收件人跟前。
//
// 源: packages/experimental/agent-team/src/types.ts:105
//
// 这两档不是两条通道，是同一条通道上的一个字段，见 [DeliveryRule]。
type Delivery string

const (
	// DeliveryQuiet 是把消息放进收件人的收件箱，不吵醒它。
	//
	// 它下一次自己开口时会看见。适合「顺带说一句」。
	DeliveryQuiet Delivery = "quiet"
	// DeliveryWakeup 是给收件人排一个回合，让它立刻处理。
	//
	// 适合「你现在就得知道」。
	DeliveryWakeup Delivery = "wakeup"
)

// MessagePhase 是一条消息在介质上走到了哪一步。
//
// 新增: DSH 没有这个字段——它的消息只有两条事件（queued、delivered），中间那一段
// 「谁正在送」活在一个进程的内存里。本包多副本，两个副本可能同时抓起同一条消息去送，
// 所以中间那一步必须落盘：认领一条消息就是把它从 [MessageQueued] 条件写成
// [MessageClaimed]，抢输的那个拿 [CodeBoardContended] 退避。
type MessagePhase string

const (
	// MessageQueued 是排着队，还没人送。
	MessageQueued MessagePhase = "queued"
	// MessageClaimed 是某个副本抓起来正在送。
	//
	// 抓起来之后那个副本可能就没了，所以这一档带一个时刻（[Message.ClaimedAt]）：
	// 超过 [Config.ClaimTTL] 还停在这一档就退回 [MessageQueued]，让别人重送。
	// 成例是 [github.com/snight1983/ds-harness-go/adapter/domainjobs] 那边捡回
	// 掉队作业的做法。
	MessageClaimed MessagePhase = "claimed"
	// MessageDelivered 是收件人那边确认收到了。
	MessageDelivered MessagePhase = "delivered"
)

// Message 是一条队友之间的消息落盘时的整份值。
//
// 源: packages/experimental/agent-team/src/types.ts:100-108（TeamMessageSnapshot）
//
// **不承诺恰好一次**。承诺的是至少一次加上落账幂等：送成功之后要转
// [MessageDelivered]，而转之前先查这条消息是不是已经确认过了。真要恰好一次，
// 就得让「送到收件人」和「记下送到了」在同一个事务里，而收件人可能在另一台机器上。
type Message struct {
	// ID 是这条消息的身份。
	ID MessageID `json:"id"`
	// Team 是这条消息属于哪支团队。
	Team TeamID `json:"team"`
	// SenderID 是发信人那个会话的身份。
	SenderID sessionlog.SessionID `json:"senderId"`
	// SenderName 是发信人的名字，写进正文的抬头里。
	SenderName string `json:"senderName"`
	// TargetID 是收件人那个会话的身份。
	TargetID sessionlog.SessionID `json:"targetId"`
	// Delivery 是安静放进去还是排一个回合。
	Delivery Delivery `json:"delivery"`
	// Content 是消息正文。
	Content llm.Content `json:"content"`
	// Phase 是走到哪一步了，见 [MessagePhase]。
	Phase MessagePhase `json:"phase"`
	// Claimant 是此刻抓着它的那个副本；只在 [MessageClaimed] 时有。
	Claimant string `json:"claimant,omitempty"`
	// ClaimedAt 是抓起来的那个时刻，用来判断认领有没有过期。
	ClaimedAt int64 `json:"claimedAt,omitempty"`
	// QueuedAt 是排进队的时刻，用来定同一个收件人那几条的先后。
	QueuedAt int64 `json:"queuedAt"`
}

// MessageSource 是收件人那边为这条消息留下的来源标记。
//
// 源: packages/experimental/agent-team/src/types.ts:110-117（TeamMessageSource）
//
// 它是收件人日志里「这一条是队友发来的、谁发的、哪条消息」这句话的耐久形式。
// 送到没送到不看它——那由 [MessageTable] 上的 [MessagePhase] 说了算，见
// [Service.Deliver]。留着它是为了让读日志的一方（模型、排障的人、投影）认得出
// 这条用户消息不是人打的。
//
// 新增: DSH 那边它是 `{kind:'team-message', ...}`，一个自带判别键的来源对象。
// 本包这一侧 [llm.MessageSource] 是密封接口，包外只能骑 [llm.PluginSource]，
// 判别键因此挪到 [llm.PluginSource.Plugin]（值为 [MessageSourceKind]），
// 剩下四个字段进 [llm.PluginSource.Extra]。这不是风格选择：Extra 里出现 `kind`
// 这个键当场报错，因为排出去的对象会有两个同名键。做法与
// [github.com/snight1983/ds-harness-go/feature/compaction.NewCheckpointSource] 一致。
type MessageSource struct {
	// Team 是哪支团队。
	Team TeamID `json:"teamId"`
	// Message 是哪条消息。
	Message MessageID `json:"messageId"`
	// SenderID 是谁发的。
	SenderID sessionlog.SessionID `json:"senderId"`
	// SenderName 是发信人的名字。
	SenderName string `json:"senderName"`
}

// MessageSourceKind 是队友消息那条来源的插件名，见 [MessageSource]。
//
// 源: packages/experimental/agent-team/src/types.ts:111
const MessageSourceKind = "team-message"

// 入口处那几道长度关。数字与 DSH 逐个对齐，理由是它们要经工具的入参模式说给模型听，
// 两边不一致会让同一个提示词在两套实现上表现不同。
//
// 源: packages/experimental/agent-team/src/task-board.ts、roster.ts 各处 requiredText 调用
const (
	// MaxSubjectLength 是任务标题的字数上限。
	MaxSubjectLength = 200
	// MaxDescriptionLength 是任务正文的字数上限。
	MaxDescriptionLength = 16384
	// MaxMemberNameLength 是队友名字的字数上限。
	MaxMemberNameLength = 64
	// MaxMemberDescriptionLength 是队友职责说明的字数上限。
	MaxMemberDescriptionLength = 200
	// MaxProviderLength 是提供方标识的字数上限。
	MaxProviderLength = 200
)

// memberNamePattern 是队友名字的形状：小写字母数字，中间用单个连字符连。
//
// 源: packages/experimental/agent-team/src/roster.ts:455
//
// 收得这么紧是因为这个名字要被模型在自由文本里念出来（"告诉 reviewer 一声"），
// 一个带空格或大小写的名字会让「它说的是谁」变成一道猜谜。
var memberNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// driveLetterPattern 认得 "C:" 这种盘符开头。
var driveLetterPattern = regexp.MustCompile(`^[A-Za-z]:`)

// RequiredText 归一化一段人写的必填文本：去掉两头空白，不许空，不许超长。
//
// 源: packages/experimental/agent-team/src/validation.ts:11-19（requiredText）
//
// 上限按**符文**数而不是字节数算，和 DSH 的 String.length 取同一个口径的意思：
// 一句中文不该因为编码占得多就先超了。
func RequiredText(value string, field string, maxLength int) (string, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return "", newError(CodeInvalidArgument, "%s 不许是空的", field)
	}
	if len([]rune(text)) > maxLength {
		return "", newError(CodeInvalidArgument, "%s 超过了 %d 个字", field, maxLength)
	}
	return text, nil
}

// ValidateMemberName 校验一个队友的名字。
//
// 源: packages/experimental/agent-team/src/roster.ts:450-458
func ValidateMemberName(value string) (string, error) {
	name := strings.TrimSpace(value)
	if name == "" || len([]rune(name)) > MaxMemberNameLength || !memberNamePattern.MatchString(name) {
		return "", newError(CodeInvalidMemberName, "队友名字 %q 不合式：要小写字母数字，中间用单个连字符连，最多 %d 个字", value, MaxMemberNameLength)
	}
	if name == LeadName {
		return "", newError(CodeInvalidMemberName, "%q 是留给队长的名字，队友不能叫它", LeadName)
	}
	return name, nil
}

// NormalizeWriteScope 把一段人写的路径前缀归一化成一条干净的工作区相对路径。
//
// 源: packages/experimental/agent-team/src/validation.ts:21-34（writeScope）
//
// 反斜杠换成斜杠、去掉开头的 "./"、去掉结尾的斜杠；绝对路径、盘符开头、
// 空段、"." 和 ".." 一律拒。**它不是一把锁**，只是给别的队友看的一句提醒，
// 见 [TaskView.ScopeWarnings]。
func NormalizeWriteScope(value string) (string, error) {
	normalized := strings.ReplaceAll(value, `\`, "/")
	normalized = strings.TrimPrefix(normalized, "./")
	normalized = strings.TrimRight(normalized, "/")
	reject := func() (string, error) {
		return "", newError(CodeInvalidWriteScope, "写入范围 %q 不是一条干净的工作区相对路径", value)
	}
	if normalized == "" || strings.HasPrefix(normalized, "/") || driveLetterPattern.MatchString(normalized) {
		return reject()
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return reject()
		}
	}
	return normalized, nil
}

// ScopesOverlap 说两条写入范围是不是压在一起：要么一样，要么一条是另一条的上级目录。
//
// 源: packages/experimental/agent-team/src/task-board.ts（写入范围重叠提醒那一段）
//
// 比的是路径段而不是字符串前缀，否则 "src/app" 会被读成 "src/api" 的上级。
func ScopesOverlap(left string, right string) bool {
	switch {
	case left == right:
		return true
	case strings.HasPrefix(right, left+"/"):
		return true
	case strings.HasPrefix(left, right+"/"):
		return true
	default:
		return false
	}
}
