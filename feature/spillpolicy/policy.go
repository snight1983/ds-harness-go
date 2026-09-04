// 本文件的作用：配置怎么验、这条执行后规则怎么判、以及替换文字怎么在上限之内拼出来。
//
// 源: packages/spill/spill-policy/src/index.ts:60-232

package spillpolicy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/snight1983/ds-harness-go/feature/outputretention"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/sessionlog"
	"github.com/snight1983/ds-harness-go/spill"
	"github.com/snight1983/ds-harness-go/tools"
)

// readToolName 是唯一一件不做面向模型外置的工具。
//
// 源: packages/spill/spill-policy/src/index.ts:206
//
// 理由是一个闭环：外置之后那段取回说明会让模型再去 read 一次那个句柄，
// 读回来的还是那份大文本，于是又被外置一次。跳过它就断了这个环。
const readToolName = "read"

// spillLabel 是这一次外置在产物里留下的用途标签。
//
// 源: packages/spill/spill-policy/src/index.ts:211
const spillLabel = "result"

// noticeJoinBytes 是预览和那句说明之间那个空行的字节数。
const noticeJoinBytes = len("\n\n")

// ErrInvalidConfig 表示配置本身不成立，构造被拒。
var ErrInvalidConfig = errors.New("policy: 配置不成立")

// Config 是这一层的配置。
//
// 源: packages/spill/spill-policy/src/index.ts:60-68
type Config struct {
	// MaxInlineBytes 是一份纯文本工具结果留在模型上下文里的字节上限（UTF-8）。
	//
	// 超过它的结果会被整段存走，换成一段由**同一个**预算切出来的替换文字。
	// 必须是非负数；0 是合法的，表示凡是有内容的结果都外置。
	//
	// 新增: DSH 允许省略它，省略就一条监听器都不装。Go 这边「不启用」由调用方
	// 不叫 [Policy.Install] 表达——见包文档。
	MaxInlineBytes int

	// Store 是存全文的后端。
	//
	// 新增: DSH 每次调用都去 ctx 里现取一次 spillStore，取不到就保留内联内容，
	// 因为 cordis 的服务是运行期来去的。Go 里它是一个显式的依赖：装的时候就得给，
	// 给 nil 是编程错误，不是一种部署状态。
	Store spill.Store

	// OwnerOf 把一次调用的 agent 映射成它所属的会话，这份产物就存在那个会话名下。
	//
	// 第二个返回值为 false 表示这次调用没有会话归属（比如一次直调），
	// 那么这次外置就不做——一份没有归属的产物，后端没法归拢，日后也没人认领。
	//
	// 新增: DSH 靠结构类型直接读 exec.agent.session.header.id。Go 这边
	// [tools.Execution.Agent] 是个不透明的作用域键，这条映射只有调用方知道。
	OwnerOf func(agent *scope.Key) (sessionlog.SessionID, bool)

	// Logger 用来记那几次尽力而为的退让，为 nil 时用 [slog.Default]。
	Logger *slog.Logger
}

// Policy 是验好的配置。
//
// 源: packages/spill/spill-policy/src/index.ts:110-122
type Policy struct {
	maxInlineBytes int
	store          spill.Store
	ownerOf        func(*scope.Key) (sessionlog.SessionID, bool)
	logger         *slog.Logger
}

// New 验一份配置，造出这一层。
//
// 源: packages/spill/spill-policy/src/index.ts:110-122
//
// 上限在**装载**的时候验，不是每次调用验：一个负数的上限会一路走到
// [outputretention.TextRetainer] 那里才报错，于是每一次结果超标的调用都变成失败。
// 一份写错的配置该让部署起不来，不该让工具挂掉。
func New(config Config) (*Policy, error) {
	if config.MaxInlineBytes < 0 {
		return nil, fmt.Errorf("%w: MaxInlineBytes 是 %d，必须是非负整数", ErrInvalidConfig, config.MaxInlineBytes)
	}
	if config.Store == nil {
		return nil, fmt.Errorf("%w: 需要一个外置存储后端", ErrInvalidConfig)
	}
	if config.OwnerOf == nil {
		return nil, fmt.Errorf("%w: 需要一条从 agent 到会话的映射", ErrInvalidConfig)
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Policy{
		maxInlineBytes: config.MaxInlineBytes,
		store:          config.Store,
		ownerOf:        config.OwnerOf,
		logger:         logger,
	}, nil
}

// Install 把这一层挂到一个工具注册表的执行后瀑布上，返回撤销它的函数。
//
// 源: packages/spill/spill-policy/src/index.ts:190-224
func (p *Policy) Install(ctx context.Context, runtime *tools.Runtime, owner *scope.Scope) (func(context.Context) error, error) {
	if runtime == nil {
		return nil, errors.New("policy: 需要一个工具注册表")
	}
	return runtime.PostExecute(ctx, owner, p.rule)
}

// rule 是挂在执行后瀑布上的那一环。
//
// 源: packages/spill/spill-policy/src/index.ts:190-224
func (p *Policy) rule(exec tools.Execution, result tools.Result, next func() (tools.PostDecision, error)) (tools.PostDecision, error) {
	// 先让下游把结果定下来，本层约束的是它定下来的那一份。
	decision, err := next()
	if err != nil {
		return decision, err
	}
	if !p.shapes(exec, decision) {
		return decision, nil
	}
	content := decision.Content
	if content == nil {
		content = result.Content
	}
	text, plain := flattenPlainText(content)
	if !plain || len(text) <= p.maxInlineBytes {
		return decision, nil
	}
	replaced, ok := p.spillReplacement(text, exec)
	if !ok {
		return decision, nil
	}
	return tools.PostDecision{
		Kind:    tools.PostAccept,
		Content: llm.Content{llm.TextBlock{Text: replaced}},
		// 下游挂的上下文原样留着：外置只换那段内容，不吃掉别人要说的话。
		AdditionalContexts: decision.AdditionalContexts,
	}, nil
}

// shapes 说明一份裁决轮不轮得到本层来换内容。
//
// 源: packages/spill/spill-policy/src/index.ts:196-198
func (p *Policy) shapes(exec tools.Execution, decision tools.PostDecision) bool {
	switch {
	// 被拦下的调用交出去的是纠正性反馈，那不是工具结果，不外置。
	case decision.Kind == tools.PostBlock:
		return false
	// 换了值的裁决要回注册表重新验、重新渲染；换值和换内容在同一次裁决里互斥。
	case decision.Value != nil:
		return false
	// 嵌套子派发的结果由它外层那次调用统一外置——单独换掉子结果，
	// 外层拿到的就是一份已经缺了一块的东西。
	case !exec.Parent.IsZero():
		return false
	case exec.Name == readToolName:
		return false
	}
	return true
}

// spillReplacement 把全文存走，拼出上限之内的替换文字。第二个返回值为 false 表示
// 这次不换，原样保留内联内容。
//
// 源: packages/spill/spill-policy/src/index.ts:125-188
func (p *Policy) spillReplacement(text string, exec tools.Execution) (string, bool) {
	sessionID, owned := p.ownerOf(exec.Agent)
	if !owned {
		p.logger.Warn("外置放弃：这次调用没有会话归属，保留内联内容", "tool", exec.Name)
		return "", false
	}
	// 背景 ctx：这次调用已经收敛了，见包文档。
	ref, err := p.store.SaveText(context.Background(), spill.SaveText{
		Owner:         spill.Owner{SessionID: sessionID},
		Source:        spill.Source{ToolName: exec.Name, CallID: exec.CallID, Label: spillLabel},
		SuggestedName: exec.Name + ".txt",
		Content:       text,
	})
	if err != nil {
		// 尽力而为：存储故障（没权限、盘满、后端不可达）绝不许让这次调用失败，
		// 也不许把内容藏起来。
		p.logger.Warn("外置放弃：存全文失败，保留内联内容", "tool", exec.Name, "err", err)
		return "", false
	}

	// 按最坏情况给那句说明留位置：拿「全丢」这个计数估它的长度。真实的丢弃数
	// 不会超过全文字节数，位数也就不会更多，所以留出来的一定够。见包文档。
	reserve := len(spillNotice(outputretention.OmittedExact(len(text)), ref)) + noticeJoinBytes
	previewText, omitted := preview(text, max(0, p.maxInlineBytes-reserve))
	notice := spillNotice(omitted, ref)
	replaced := notice
	if previewText != "" {
		replaced = previewText + "\n\n" + notice
	}
	if len(replaced) > p.maxInlineBytes {
		// 连那句说明本身都塞不下，换了就破了上限这个承诺。已经写下去的那份产物
		// 是个无害的孤儿，清理留给后端。
		p.logger.Warn("外置放弃：说明本身就超过了上限，保留内联内容", "tool", exec.Name)
		return "", false
	}
	return replaced, true
}

// flattenPlainText 把一份全是文本的内容拼成一个串；只要有一块不是文本就交出 false。
//
// 源: packages/spill/spill-policy/src/index.ts:83-90
//
// 只认纯文本是有意的：本层看得见的只是**最终渲染出来的文字**，不懂任何工具的内部形状。
// 一份带着图片或者别的块的结果，谁也说不清把它拍平成一段预览之后还剩下什么。
func flattenPlainText(content llm.Content) (string, bool) {
	var builder strings.Builder
	for _, block := range content {
		typed, ok := block.(llm.TextBlock)
		if !ok {
			return "", false
		}
		builder.WriteString(typed.Text)
	}
	return builder.String(), true
}

// preview 按 budget 个字节切出一段头尾预览，并给出丢了多少。
//
// 源: packages/spill/spill-policy/src/index.ts:93-100
//
// 预算对半分，多出来的那个字节给头：一份结果的开头通常比结尾更能说明它是什么。
func preview(text string, budget int) (string, outputretention.Omitted) {
	// budget 非负，所以这两个预算都合法，造留存器不会失败。
	retainer, _ := outputretention.NewTextRetainer(outputretention.TextHeadTail((budget+1)/2, budget/2))
	retainer.PushString(text)
	kept := retainer.Finish()
	return kept.Text, kept.OmittedBytes
}

// spillNotice 拼出那一行说明：丢了多少、全文存在哪、怎么取回来。
//
// 源: packages/spill/spill-policy/src/index.ts:103-106
//
// 这段文字是给模型看的，所以保持英文，和本仓库其余面向模型的载荷同一条界线。
func spillNotice(omitted outputretention.Omitted, ref spill.Ref) string {
	// omitted 由本包自己造，单位是四个合法值之一，描述不出错。
	omission, _ := outputretention.DescribeOmitted(omitted, outputretention.UnitBytes)
	return fmt.Sprintf("(%s Full formatted result stored at: %s. %s)", omission, ref.Locator, ref.RetrievalHint)
}
