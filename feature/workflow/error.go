// 本文件的作用：这条接缝那种带码的失败，以及「这条错误该不该把脚本打断」这条判据。
//
// 源: packages/workflow/workflow/src/index.ts:102-148

package workflow

import (
	"errors"

	"github.com/snight1983/ds-harness-go/llm"
)

// 本接缝那些机器认得的致命失败码，一字不差照搬 DSH。
//
// 源: packages/workflow/workflow/src/index.ts:108-119
//
// 一个**普通的孩子失败**不在这张表里：它把脚本里那一项解成 null，运行照常往下跑。
const (
	// CodeScriptParse 是「脚本正文解析不了」。
	CodeScriptParse = "SCRIPT_PARSE"
	// CodeMetaInvalid 是「meta 那个块的形状不对」。
	CodeMetaInvalid = "META_INVALID"
	// CodeInvalidArgument 是「传给脚本 API 的某个参数不成立」。
	CodeInvalidArgument = "INVALID_ARGUMENT"
	// CodeUnsupportedOption 是「某个选项名这个引擎不认识」——多半是拼错了。
	CodeUnsupportedOption = "UNSUPPORTED_OPTION"
	// CodeUnsupportedSchema 是「要的那份结构化输出 schema 这个引擎支持不了」。
	CodeUnsupportedSchema = "UNSUPPORTED_SCHEMA"
	// CodeAgentCap 是「撞上了这次运行的子 agent 总数天花板」。
	CodeAgentCap = "AGENT_CAP"
	// CodeItemCap 是「撞上了某个组合子的单批条目天花板」。
	CodeItemCap = "ITEM_CAP"
	// CodeAgentStart 是「开孩子这件事本身在基础设施层面失败了」。
	CodeAgentStart = "AGENT_START"
	// CodeAgentResult 是「取孩子结果这件事在基础设施层面失败了」。
	CodeAgentResult = "AGENT_RESULT"
	// CodeResultUnserializable 是「脚本返回的值排不出边界」。
	CodeResultUnserializable = "RESULT_UNSERIALIZABLE"
	// CodeCancelled 是「这次运行被取消了」。
	CodeCancelled = "CANCELLED"
)

// fatalCodes 是上面那张表的集合形式，[IsFatal] 按它判。
var fatalCodes = map[string]struct{}{
	CodeScriptParse:          {},
	CodeMetaInvalid:          {},
	CodeInvalidArgument:      {},
	CodeUnsupportedOption:    {},
	CodeUnsupportedSchema:    {},
	CodeAgentCap:             {},
	CodeItemCap:              {},
	CodeAgentStart:           {},
	CodeAgentResult:          {},
	CodeResultUnserializable: {},
	CodeCancelled:            {},
}

// NewError 造一条工作流接缝的带码失败。cause 可以为 nil。
//
// 源: packages/workflow/workflow/src/index.ts:130-139（WorkflowError）
//
// 新增: DSH 那边 WorkflowError 是 HarnessError 的子类，只为把 name 改成
// 'WorkflowError'——那个字段在 JS 里是用来认错误来源的。Go 认错误靠 [errors.Is] /
// [errors.As] 和那个码，多派生一个类型只会让上游那句 errors.As(err, &target)
// （target 是 *llm.Error）失效。所以这里不新造类型，直接交
// [github.com/snight1983/ds-harness-go/llm.Error]，身份由码承担。做法和
// [github.com/snight1983/ds-harness-go/feature/subagent.NewError] 完全一样。
func NewError(message, code string, cause error) *llm.Error {
	return llm.NewError(message, code, cause)
}

// IsFatal 判一条错误该不该被组合子原样抛出去，而不是把那一项解成 null。
//
// 源: packages/workflow/workflow/src/index.ts:146-148（isFatalWorkflowError）
//
// 这条判据是给 parallel() / pipeline() 那类组合子用的：一个拼错的选项、一个撞上的
// 天花板必须**大声**把脚本打死，而每一项的那个 null 留给孩子失败和普通的阶段内
// 脚本错误。
//
// 新增: DSH 那边 WorkflowError 带一个 fatal 布尔字段，构造时默认 true。它自己那句
// 文档写明「Every WorkflowErrorCode is fatal；这个标志存在只是为了让每个 catch 点上
// 那个区分是显式的」，而整个仓库里没有一处生产代码传过 fatal: false，只有它自己的
// 单元测试传。Go 这边把判据直接落在**码的集合**上：一个只可能取一个值的字段会变成
// 第二份事实来源，而两份事实迟早会岔。要表达「这条错误不该打断脚本」，用一个不在
// 这张表里的码，别去翻一个开关。
func IsFatal(err error) bool {
	var carrier *llm.Error
	if !errors.As(err, &carrier) {
		return false
	}
	_, fatal := fatalCodes[carrier.Failure.Code]
	return fatal
}
