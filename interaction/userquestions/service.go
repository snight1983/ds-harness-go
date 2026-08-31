// 本文件的作用：那个提供方插槽、那道在请求离开进程之前把它拦下来的门、
// 以及这条能力的错误分类。
//
// 源: packages/interaction/user-questions/src/index.ts:27-141

package userquestions

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"ds-harness-go/core/scope"
)

// 这条能力的错误代号。它们是**上线的**分类：跨包的调用方按代号分流，不解析错误文本。
//
// 源: packages/interaction/user-questions/src/index.ts:42-48
const (
	// CodeDuplicateProvider 是「这个上下文里已经有一个活着的提供方了」。
	CodeDuplicateProvider = "DUPLICATE_PROVIDER"
	// CodeAskAborted 是「还没等到人回答就被取消了」。
	CodeAskAborted = "ASK_ABORTED"
	// CodeEmptyQuestions 是「一个问题都没有」。
	CodeEmptyQuestions = "EMPTY_QUESTIONS"
	// CodeCallerNotLive 是「给进来的这个 agent 不是那个活着的实例」。
	CodeCallerNotLive = "CALLER_NOT_LIVE"
	// CodeDelegatedCaller 是「这个 agent 活着，但它被另一个活 agent 拥有」。
	CodeDelegatedCaller = "DELEGATED_CALLER"
	// CodeBadIntent 是「这个呈现意图声称的东西和问题本身对不上」。
	CodeBadIntent = "BAD_INTENT"
	// CodeNoProvider 是「一个界面都没接上来」。
	CodeNoProvider = "NO_PROVIDER"
	// CodeAskCancelled 是「用户把这次提问撤掉了，改成自己说」。
	//
	// 本包自己从不报它——它由界面侧的 [Provider] 报，由调用方按代号认。它写在这里
	// 是因为这份分类是**跨包的**契约：报的一方和认的一方在两个包里，代号却必须是
	// 同一个字符串。
	CodeAskCancelled = "ASK_CANCELLED"
)

// Error 是这条能力的错误。
//
// 源: packages/interaction/user-questions/src/index.ts:42-48
//
// 新增: DSH 那边它继承 HarnessError，靠 instanceof 认。Go 里靠 errors.As 认这个
// 具体类型；ErrorName 和 ErrorCode 两个方法让 [ds-harness-go/core/tools] 那道
// 结果收敛能把它的身份原样抄进 Failure.Info。
type Error struct {
	// Code 是机器可读代号，取上面那几个常量之一。
	Code string
	// Message 是给人和给模型看的那句话。
	Message string
}

func (err *Error) Error() string { return err.Message }

// ErrorName 对应 DSH 的 Error.name。
func (err *Error) ErrorName() string { return "UserQuestionError" }

// ErrorCode 交出机器可读代号。
func (err *Error) ErrorCode() string { return err.Code }

// fail 造一条本包的错误。
func fail(code, message string) error { return &Error{Code: code, Message: message} }

// CallerStatus 是一个 agent 眼下能不能面对人。
//
// 新增: DSH 直接问 ctx.agents——「这是不是那个活着的实例」问 agents.get(id) === agent，
// 「它是不是被别人拥有」问 agents.roots().includes(agent)。Go 这边活 agent 注册表是
// 循环那一块的东西，工具层拿到的只是一个不透明的作用域键，所以这两问收敛成一个
// 三值答案，由装配方经 [Config.CallerStatus] 回答。
type CallerStatus int

const (
	// CallerUnknown 表示这个键在活 agent 注册表里认不出来——也包括根本没有那张表。
	//
	// 它是零值，所以一条没接上来的接缝给出的是**最紧**的那个答案：宁可拒掉一次
	// 合法的提问，也不能让一个没人能证实的调用方把问题摆到用户面前。
	CallerUnknown CallerStatus = iota
	// CallerRoot 表示它活着，而且是运行期的一个根。
	//
	// 定这条界线的是**运行期归属**，不是持久的会话血缘：一个带着血缘、但被当成
	// 新的运行期根恢复起来的会话，照常可以提问。
	CallerRoot
	// CallerDelegated 表示它活着，但被另一个活 agent 拥有。
	//
	// 一个被拥有的子 agent 背后没有人在看，问了就是永远等下去。
	CallerDelegated
)

// Provider 是界面侧那个真去问人的实现。
//
// 源: packages/interaction/user-questions/src/index.ts:37-40
type Provider interface {
	// Ask 把这些问题摆到人面前，等他回答。
	Ask(ctx context.Context, request Request) (Answer, error)
}

// Request 是一次要人回答的请求。
//
// 源: packages/interaction/user-questions/src/index.ts:27-35
type Request struct {
	// Questions 是要显示的那些问题。
	Questions []Item
	// Agent 是发起这次提问的那个 agent；来自工具调用时非 nil。
	//
	// 它在的时候才做那道活性求证：一个不带 agent 的请求（界面自己发起的、
	// 装配期的）没有可求证的对象，也就没有「代人受过」的风险。
	Agent *scope.Key
}

// Config 是这个服务的装配配置。
type Config struct {
	// CallerStatus 说明一个 agent 眼下能不能面对人。
	//
	// nil 表示这个装配里没有活 agent 注册表：那么任何**带着** agent 的请求都会被拒，
	// 和 DSH 里 ctx.get('agents') 拿到 undefined 是同一件事。不带 agent 的请求不受影响。
	CallerStatus func(agent *scope.Key) CallerStatus
}

// Service 是这条能力的接缝：一个活着的界面提供方，加上一个 Ask。
//
// 源: packages/interaction/user-questions/src/index.ts:50-141
type Service struct {
	callerStatus func(*scope.Key) CallerStatus

	// mutex 守住那个插槽。DSH 是单线程的 JS，Go 里注册和提问可以来自不同的
	// goroutine——一次热重载正好撞上一次提问，没有这把锁就是数据竞争。
	mutex    sync.Mutex
	provider Provider
}

// New 造一个服务。
func New(config Config) *Service {
	return &Service{callerStatus: config.CallerStatus}
}

// RegisterProvider 接上界面侧的提供方，返回注销函数。
//
// 源: packages/interaction/user-questions/src/index.ts:58-75
//
// 一个上下文里只准有一个活着的提供方，重复接入报 [CodeDuplicateProvider] 而不是
// 顶掉前一个：顶掉的话，一次正在等人回答的提问会永远等下去——它挂在那个被换走的
// 界面上，而新界面根本不知道有这回事。
//
// 交回来的注销函数可以调不止一次，第二次起是空操作。
func (s *Service) RegisterProvider(provider Provider) (func(), error) {
	if provider == nil {
		return nil, fail(CodeNoProvider, "user-questions: 接入的提供方不能是 nil")
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.provider != nil {
		return nil, fail(CodeDuplicateProvider, "a user-questions provider is already registered")
	}
	s.provider = provider
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mutex.Lock()
			defer s.mutex.Unlock()
			if s.provider == provider {
				s.provider = nil
			}
		})
	}, nil
}

// Ask 把这些问题交给活着的那个提供方，等人回答。
//
// 源: packages/interaction/user-questions/src/index.ts:77-140
//
// 那几道门按这个顺序走，而且**全部**走在把请求交出去之前：已经取消了、一个问题
// 都没有、调用方不是活着的根、意图和问题本身对不上、没有提供方。一个坏请求不许
// 在界面上留下任何痕迹。
func (s *Service) Ask(ctx context.Context, request Request) (Answer, error) {
	if err := ctx.Err(); err != nil {
		// 已经取消了就不必再惊动人。
		return Answer{}, fail(CodeAskAborted, "ask_user_question was aborted before the user answered")
	}
	if len(request.Questions) == 0 {
		return Answer{}, fail(CodeEmptyQuestions, "ask_user_question requires at least one question")
	}
	if err := s.checkCaller(request.Agent); err != nil {
		return Answer{}, err
	}
	if err := checkIntents(request.Questions); err != nil {
		return Answer{}, err
	}
	s.mutex.Lock()
	provider := s.provider
	s.mutex.Unlock()
	if provider == nil {
		return Answer{}, fail(CodeNoProvider, "no user-questions provider is registered")
	}
	return provider.Ask(ctx, request)
}

// checkCaller 求证这个调用方眼下能不能面对人。
//
// 源: packages/interaction/user-questions/src/index.ts:99-113
func (s *Service) checkCaller(agent *scope.Key) error {
	if agent == nil {
		return nil
	}
	status := CallerUnknown
	if s.callerStatus != nil {
		status = s.callerStatus(agent)
	}
	switch status {
	case CallerRoot:
		return nil
	case CallerDelegated:
		return fail(CodeDelegatedCaller,
			"human interaction is unavailable while the calling agent is owned by another live agent; "+
				"include the unresolved question or decision in the child agent's final result")
	default:
		return fail(CodeCallerNotLive,
			"human interaction requires the exact live calling agent when an agent is supplied")
	}
}

// checkIntents 查每一个呈现意图声称的东西和它自己那个问题对不对得上。
//
// 源: packages/interaction/user-questions/src/index.ts:114-135
//
// 一个意图断言了两件类型说不出来的事：它点名的那个同意标签是**这个问题自己的**
// 选项之一，以及一次计划评审带着它要评的那份计划。任何一条缺了，一个照着意图画的
// 界面就会把「提问方根本没给过的选项」或者「看不见的东西的同意键」摆到用户面前。
// 拦在提问方这一侧——错在这儿——而不是让每一个界面各拦一遍。
func checkIntents(questions []Item) error {
	for _, question := range questions {
		intent := question.Intent
		if intent == nil {
			continue
		}
		if !offers(question.Options, intent.ApproveLabel()) {
			return fail(CodeBadIntent, fmt.Sprintf(
				"question %s declares intent %s whose approve label %s names none of its options",
				question.ID, intent.Kind(), strconv.Quote(intent.ApproveLabel())))
		}
		if question.Detail == "" {
			return fail(CodeBadIntent, fmt.Sprintf(
				"question %s declares intent %s without the detail it reviews",
				question.ID, intent.Kind()))
		}
	}
	return nil
}

// offers 说明这些选项里有没有这个标签。
func offers(options []Option, label string) bool {
	for _, option := range options {
		if option.Label == label {
			return true
		}
	}
	return false
}

// CodeOf 取出一个错误的本包代号；不是本包的错误交出空串。
//
// 新增: DSH 那边调用方写 `cause instanceof UserQuestionError && cause.code === '...'`。
// Go 里这一步是 errors.As，写在这里一次，省得每一个调用方各写一遍那五行。
func CodeOf(err error) string {
	var typed *Error
	if errors.As(err, &typed) {
		return typed.Code
	}
	return ""
}
