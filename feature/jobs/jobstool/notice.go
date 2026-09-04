// 本文件的作用：把一条完成通知、一份工具输出，塞进生产方定的那个字节预算里——
// 一级一级往下让，让到只剩「去读 job_output」那一句为止。
//
// 源: packages/jobs/tool-jobs/src/index.ts:109-183

package jobstool

import (
	"strings"
	"unicode"

	"github.com/snight1983/ds-harness-go/feature/jobs"
	"github.com/snight1983/ds-harness-go/feature/outputretention"
	"github.com/snight1983/ds-harness-go/llm"
)

// noticeOmitted 是通知被截掉时补上的那一句。
//
// 源: packages/jobs/tool-jobs/src/index.ts:153
const noticeOmitted = "\n[notice truncated]"

// noticeAction 是让到最后也不能丢的那一句：读哪件东西才拿得到全文。
//
// 源: packages/jobs/tool-jobs/src/index.ts:149
const noticeAction = "\nDone; job_output."

// outputOmitted 是 job_output 的正文被截掉时补上的那一句。
//
// 源: packages/jobs/tool-jobs/src/index.ts:252
const outputOmitted = "\n[output truncated]"

// resultOmitted 是别的结果内容被截掉时补上的那一句。
//
// 源: packages/jobs/tool-jobs/src/index.ts:181
const resultOmitted = "\n[result truncated]"

// noNewOutput 是一次读到空的流该怎么说。
//
// 源: packages/jobs/tool-jobs/src/index.ts:246,325
const noNewOutput = "(no new output)"

// retainHead 留住前 maxBytes 个字节。
//
// 源: packages/jobs/tool-jobs/src/index.ts:117-121
//
// 造留存器唯一会失败的原因是负预算，而这里当场夹到零以上，所以那条错误在本包里
// 没有产出方——不写一条永远走不到的分支，理由见仓库里 100% 覆盖那条规矩。
func retainHead(text string, maxBytes int) string {
	retainer, _ := outputretention.NewTextRetainer(outputretention.TextHead(max(maxBytes, 0)))
	retainer.PushString(text)
	return retainer.Finish().Text
}

// retainTail 留住最后 maxBytes 个字节，理由同 [retainHead]。
//
// 源: packages/jobs/tool-jobs/src/index.ts:111-115
func retainTail(text string, maxBytes int) string {
	retainer, _ := outputretention.NewTextRetainer(outputretention.TextTail(max(maxBytes, 0)))
	retainer.PushString(text)
	return retainer.Finish().Text
}

// fitWithSuffix 把「正文 + 一个必须留住的后缀」塞进预算：正文让，后缀不让。
//
// 源: packages/jobs/tool-jobs/src/index.ts:123-135
//
// maxBytes 为 0 表示不设上限（[github.com/snight1983/ds-harness-go/feature/jobs.Snapshot.OutputLimitBytes]
// 的约定），对应 DSH 那个 `maxBytes === undefined`。
func fitWithSuffix(content, suffix string, maxBytes int, omitted string) string {
	complete := content + suffix
	if maxBytes <= 0 || len(complete) <= maxBytes {
		return complete
	}
	// 正文自己已经以那句提示收尾时不再补一句：那多半是生产方自己截过了，
	// 补第二句只会让模型以为被截了两回。
	marker := omitted
	if strings.HasSuffix(content, strings.TrimLeftFunc(omitted, unicode.IsSpace)) {
		marker = ""
	}
	fixed := marker + suffix
	if len(fixed) >= maxBytes {
		// 连「提示 + 后缀」都放不下，那就只保后缀那一头。
		return retainTail(fixed, maxBytes)
	}
	return retainTail(content, maxBytes-len(fixed)) + fixed
}

// completionSummary 是那条折叠的对话行上写的一句话：什么种类、什么标签、什么状态。
//
// 源: packages/jobs/tool-jobs/src/index.ts:137-144
func completionSummary(snapshot jobs.Snapshot) string {
	return llm.BoundContextSummary(
		string(snapshot.Kind) + " " + snapshot.Label + " " + StatusLine(snapshot.Status, snapshot.Detail))
}

// fitCompletionNotice 排出那条完成通知，并把它塞进这件作业自己的字节预算。
//
// 源: packages/jobs/tool-jobs/src/index.ts:146-167
//
// 让的次序是四级：整句 → 砍掉那段细节 → 只剩 id 和那一句动作 → 连 id 也砍。
// 每一级都保住 [noticeAction]：一条模型看不懂该去读什么的通知，等于一次它永远
// 不会知道的完成。
func fitCompletionNotice(snapshot jobs.Snapshot) string {
	prefix := "background job " + string(snapshot.ID)
	detail := " (" + string(snapshot.Kind) + ": " + snapshot.Label + ") finished " +
		StatusLine(snapshot.Status, snapshot.Detail)
	complete := prefix + detail + ". Read its output with job_output."
	maxBytes := snapshot.OutputLimitBytes
	if maxBytes <= 0 || len(complete) <= maxBytes {
		return complete
	}
	fixed := prefix + noticeOmitted + noticeAction
	if len(fixed) <= maxBytes {
		if len(fixed) == maxBytes {
			return fixed
		}
		return prefix + retainHead(detail, maxBytes-len(fixed)) + noticeOmitted + noticeAction
	}
	compact := prefix + noticeAction
	if len(compact) <= maxBytes {
		return compact
	}
	if len(noticeAction) >= maxBytes {
		return retainTail(noticeAction, maxBytes)
	}
	return retainHead(prefix, maxBytes-len(noticeAction)) + noticeAction
}

// rawSingleText 取出「恰好一块文本」那种内容里的那段字。不是这个形状时第二个
// 返回值为 false。
//
// 源: packages/jobs/tool-jobs/src/index.ts:169-174
func rawSingleText(content llm.Content) (string, bool) {
	if len(content) != 1 {
		return "", false
	}
	block, ok := content[0].(llm.TextBlock)
	if !ok {
		return "", false
	}
	return block.Text, true
}

// boundSingleText 把「恰好一块文本」那种内容收进预算，不是这个形状就不动它。
//
// 源: packages/jobs/tool-jobs/src/index.ts:176-183
func boundSingleText(content llm.Content, maxBytes int) llm.Content {
	text, ok := rawSingleText(content)
	if !ok {
		return nil
	}
	return llm.Content{llm.TextBlock{Text: fitWithSuffix(text, "", maxBytes, resultOmitted)}}
}
