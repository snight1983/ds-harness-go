// 本文件的作用：这一层的纯形状——那条只进日志的请求记录长什么样、一份部署策略
// 要填哪几项、以及本包认得的那几个哨兵。
//
// 源: packages/session/session-title-llm/src/index.ts:24-138

package sessiontitlellm

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"time"

	"ds-harness-go/llm"
	"ds-harness-go/session"
	"ds-harness-go/session/sessiontitle"
)

// EventTitleLLMRequest 是派发之前落下来的那条请求记录。
//
// 源: packages/session/session-title-llm/src/index.ts:40-45
//
// 它是**只进日志**的：不上模型可见表面，也不进派生历史。理由见包文档。
const EventTitleLLMRequest session.EventType = "session/title-llm-request"

// EventTypes 是本包往会话日志里写的那几种事件类型。
//
// 新增: 理由与 [sessiontitle.EventTypes] 逐字相同——Go 没有声明合并，
// [session.Vocabulary] 是个闭合的值，所以装配方要自己把它拼进去：
//
//	vocabulary := session.CoreVocabulary().
//		With(sessiontitle.EventTypes()...).
//		With(sessiontitlellm.EventTypes()...)
func EventTypes() []session.EventType {
	return []session.EventType{EventTitleLLMRequest}
}

// RequestEventData 是 [EventTitleLLMRequest] 的负载：模型这一次确切看到了什么。
//
// 源: packages/session/session-title-llm/src/index.ts:25-38
type RequestEventData struct {
	// TitleProvider 是发这次请求的那个登记过的标题生成器。
	TitleProvider sessiontitle.ProviderID `json:"titleProvider"`
	// MessageSeqs 是 Messages 里代表的那几条人类 user/message 的确切 seq。
	MessageSeqs []int `json:"messageSeqs"`
	// Route 是这次辅助调用确切走的那条路由。
	Route sessiontitle.ModelProvenance `json:"route"`
	// System 是这次用的系统提示词原文。
	System string `json:"system"`
	// Messages 是交给模型的那份确切消息表。
	Messages []llm.Message `json:"messages"`
	// MaxTokens 是这次辅助生成的输出 token 上限。
	MaxTokens int `json:"maxTokens"`
}

// TimeoutCode 是本包超时时挂在失败上的稳定码。
//
// 源: packages/session/session-title-llm/src/index.ts:48
const TimeoutCode = "SESSION_TITLE_TIMEOUT"

// PluginName 是装帧后那条用户消息的来源标记。
//
// 它让日志里那条消息一眼看得出不是人打的：来源是 plugin，插件名是本包。
const PluginName = "dsh-session-title-llm"

var (
	// ErrInvalidConfig 表示配置本身不成立，构造被拒。
	ErrInvalidConfig = errors.New("sessiontitlellm: 配置不成立")

	// ErrTimeout 是超时那次取消的因由，随身带着 [TimeoutCode]。
	//
	// 新增: DSH 那边 deadline() 拿这个码去 abort 那个 signal。Go 这边它是
	// context.WithTimeoutCause 的因由，调用方靠 errors.Is 认它，靠 errors.As
	// 取出 [llm.Error] 里那份带码的事实。
	ErrTimeout = llm.NewError("sessiontitlellm: 辅助标题请求超时", TimeoutCode, nil)
)

// Streamer 是本包从模型运行时身上要用的全部东西。
//
// 新增: DSH 从 ctx.llm 上拿整个运行时。这里只声明流式那一件事，理由见包文档。
// [ds-harness-go/llm.Runtime] 直接满足它。
type Streamer interface {
	// Stream 把一次模型调用按原始分块流出来。
	Stream(ctx context.Context, options llm.GenerateOptions) (iter.Seq2[llm.StreamChunk, error], error)
}

// Config 是一个模型标题生成器必填的部署策略。
//
// 源: packages/session/session-title-llm/src/index.ts:51-66
//
// 本包一个默认值都不给：这几项全都要么花钱（输出上限、超时），要么影响用户看到
// 的东西（长度目标），一个悄悄生效的默认值意味着没人为它做过决定。
type Config struct {
	// TargetWords 是非 CJK 语言下期望的词数。
	TargetWords int
	// TargetCJKCharacters 是中日韩语言下期望的字数。
	TargetCJKCharacters int
	// MaxInputBytes 是装帧完那段用户提示词最多占几个 UTF-8 字节。
	//
	// 超了直接不发：一次会把上下文撑爆的辅助调用，最好的结果也是白花钱。
	MaxInputBytes int
	// MaxOutputTokens 是这次辅助生成的输出 token 上限。
	MaxOutputTokens int
	// Timeout 是这次辅助请求端到端的期限。
	Timeout time.Duration
	// Provider 是显式指定的提供方路由，必须和 Model 成对出现。
	//
	// 两个都留空表示跟着日志里那条主请求路由走。
	Provider string
	// Model 是那条路由上的模型 id，必须和 Provider 成对出现。
	Model string
}

// Validate 检查配置是否成立。
//
// 源: packages/session/session-title-llm/src/index.ts:108-138
//
// 新增: DSH 那边还要挡「多出来的配置键」（unknown config key）——那是 JS 里一份
// 外来对象唯一能出的岔子。Go 的结构体字面量写不出不存在的字段，编译器已经把这
// 一整段消掉了。
func (c Config) Validate() error {
	for _, limit := range []struct {
		name  string
		value int
	}{
		{"TargetWords", c.TargetWords},
		{"TargetCJKCharacters", c.TargetCJKCharacters},
		{"MaxInputBytes", c.MaxInputBytes},
		{"MaxOutputTokens", c.MaxOutputTokens},
	} {
		if limit.value <= 0 {
			return fmt.Errorf("%w：%s 必须是正整数，收到 %d", ErrInvalidConfig, limit.name, limit.value)
		}
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("%w：Timeout 必须是正的，收到 %s", ErrInvalidConfig, c.Timeout)
	}
	// 路由必须成对：只填一半意味着另一半从日志里来，那样拼出来的是一条谁都没有
	// 决定过的路由——比如「用户配的提供方 + 主对话当前的模型」，这两者根本不保证
	// 对得上。
	if (c.Provider == "") != (c.Model == "") {
		return fmt.Errorf("%w：Provider 和 Model 必须一起给，收到 Provider=%q Model=%q",
			ErrInvalidConfig, c.Provider, c.Model)
	}
	return nil
}

// MessageSelector 从服务给的那份消息快照里挑出这一档要用的那几条。
//
// 源: packages/session/session-title-llm/src/index.ts:141-143
//
// 它必须挑出至少一条，否则这次生成会失败。挑出来的顺序和 seq 都要保持原样：
// 服务会拿它们去验产出引的 seq。
type MessageSelector func([]sessiontitle.UserMessage) []sessiontitle.UserMessage
