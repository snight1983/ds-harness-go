// 本文件的作用：把一份录下来的会话日志里那些「每跑一次都不一样、又和这一轮
// 发生了什么无关」的东西压平——墙上时钟、请求头里的大块文本、落盘批次留下的
// 打包边界。
//
// 源: packages/test-support/session-snapshot/src/normalize.ts

package snapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/snight1983/ds-harness-go/sessionlog"
)

// 请求头膨胀换掉之后留在原处的记号。
//
// 源: packages/test-support/session-snapshot/src/normalize.ts:21-22
const (
	systemToken = `"{{system}}"`
	toolsToken  = `"{{tools}}"`
)

// goalChangeType 的负载自己带着两个时钟，事件信封那一个归零管不到它们。
//
// 类型名在这里是字面量而不是常量引用：[github.com/snight1983/ds-harness-go/feature/goal]
// 在 feature 档上，本包在 contract 档，引不过来（见 docs/layers.tsv）。
//
// 新增: DSH 还单独归零 `hook/result` 的 durationMs。本仓库没有 hook 这一路。
const goalChangeType = "goal/change"

// Options 控制哪几段请求头膨胀原样留着。
//
// 源: packages/test-support/session-snapshot/src/normalize.ts:459-490
//
// 缺省两段都换成记号，因为系统提示词和工具 schema 加起来能占掉一份夹具的九成
// 篇幅，而它们每一份夹具里都一模一样。代价是那两段原文在快照里就没有了，所以
// 一个场景要指定**一份**夹具留着它们——那份夹具就是这两段的权威副本，改了提示词
// 或者加了工具，它是唯一会变红的地方。
type Options struct {
	// KeepSystem 留下系统提示词原文。
	KeepSystem bool
	// KeepTools 留下工具 schema 原文。
	KeepTools bool
}

// Normalize 把一个场景的全部会话日志压成可以提交的快照，父会话在最前。
//
// 源: packages/test-support/session-snapshot/src/normalize.ts:434-446（normalizeSessionSnapshots）
//
// **一次收一整个场景**，不是一份一份收：标识换出来的记号要在父子之间保持相等
// 关系，理由见 [RedactIDs]。
//
// 交出来的每一份仍旧是 JSONL：第 0 行是会话头，此后一行一条事件或者一行打包过的
// 分块，而且正文那些行**不带事件信封**（seq/time/seq0/time0 整个省掉）——留着它们
// 等于把一次落盘的批次边界也提交进仓库。读回来时由
// [github.com/snight1983/ds-harness-go/feature/replay.ParseSessionLog] 补回。
func Normalize(logs [][]byte, options Options) ([][]byte, error) {
	redacted, err := RedactIDs(logs)
	if err != nil {
		return nil, err
	}
	out := make([][]byte, len(redacted))
	for index, log := range redacted {
		normalized, err := normalizeOne(log, options)
		if err != nil {
			return nil, fmt.Errorf("snapshot: 第 %d 份日志：%w", index, err)
		}
		out[index] = normalized
	}
	return out, nil
}

func normalizeOne(log []byte, options Options) ([]byte, error) {
	lines := splitLines(log)
	if len(lines) == 0 {
		return nil, errors.New("一份日志至少要有会话头那一行")
	}

	header, err := decodeFields(lines[0])
	if err != nil {
		return nil, fmt.Errorf("会话头：%w", err)
	}
	if _, ok := header["createdAt"]; ok {
		header["createdAt"] = json.RawMessage("0")
	}
	headerLine, err := json.Marshal(header)
	if err != nil {
		return nil, fmt.Errorf("会话头排不出去：%w", err)
	}

	body := make([]json.RawMessage, 0, len(lines)-1)
	for index, line := range lines[1:] {
		fields, err := decodeFields(line)
		if err != nil {
			return nil, fmt.Errorf("第 %d 行：%w", index+2, err)
		}
		if err := flattenRecord(fields, options); err != nil {
			return nil, fmt.Errorf("第 %d 行：%w", index+2, err)
		}
		row, err := json.Marshal(fields)
		if err != nil {
			return nil, fmt.Errorf("第 %d 行排不出去：%w", index+2, err)
		}
		body = append(body, row)
	}

	repacked, err := repack(body)
	if err != nil {
		return nil, err
	}

	var out strings.Builder
	out.Write(headerLine)
	out.WriteByte('\n')
	for _, row := range repacked {
		out.Write(row)
		out.WriteByte('\n')
	}
	return []byte(out.String()), nil
}

// flattenRecord 就地压平一条正文记录里那些易变的字段。
//
// 源: packages/test-support/session-snapshot/src/normalize.ts:341-377（normalizeSessionLog）
func flattenRecord(fields map[string]json.RawMessage, options Options) error {
	kind := fieldString(fields, "type")
	if packedRowTypes[sessionlog.RowType(kind)] {
		// 打包行的时钟骑在 time0 上，成员之间的间隔骑在 data.dt 上。两样都要归零，
		// 否则重打包会把那些间隔原样再算回来。
		fields["time0"] = json.RawMessage("0")
		return zeroDeltas(fields)
	}
	fields["time"] = json.RawMessage("0")
	switch kind {
	case goalChangeType:
		return zeroPayloadClocks(fields, "createdAt", "updatedAt")
	case string(sessionlog.EventRequestHeader):
		return tokenizeRequestHeader(fields, options)
	}
	return nil
}

// zeroDeltas 把一行打包分块里成员之间那串间隔全归零。
func zeroDeltas(fields map[string]json.RawMessage) error {
	data, ok := fields["data"]
	if !ok {
		return nil
	}
	payload, err := decodeFields(string(data))
	if err != nil {
		return fmt.Errorf("打包行的负载：%w", err)
	}
	raw, ok := payload["dt"]
	if !ok {
		return nil
	}
	var deltas []json.RawMessage
	if err := json.Unmarshal(raw, &deltas); err != nil {
		return fmt.Errorf("打包行的 dt 不是一个数组：%w", err)
	}
	for index := range deltas {
		deltas[index] = json.RawMessage("0")
	}
	return replacePayload(fields, payload, "dt", deltas)
}

// zeroPayloadClocks 把一条事件负载里点名的那几个时钟归零。
func zeroPayloadClocks(fields map[string]json.RawMessage, keys ...string) error {
	data, ok := fields["data"]
	if !ok {
		return nil
	}
	payload, err := decodeFields(string(data))
	if err != nil {
		return fmt.Errorf("事件负载：%w", err)
	}
	touched := false
	for _, key := range keys {
		if _, ok := payload[key]; ok {
			payload[key] = json.RawMessage("0")
			touched = true
		}
	}
	if !touched {
		return nil
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("事件负载排不出去：%w", err)
	}
	fields["data"] = encoded
	return nil
}

// tokenizeRequestHeader 把一份请求头快照里那两段膨胀换成记号，字段本身留着。
//
// 源: packages/test-support/session-snapshot/src/normalize.ts:522-541
//
// 换的是**值**不是键：一份夹具因此仍旧看得出这次请求带没带系统提示、带没带工具，
// 只是看不出它们的正文。
func tokenizeRequestHeader(fields map[string]json.RawMessage, options Options) error {
	data, ok := fields["data"]
	if !ok {
		return nil
	}
	payload, err := decodeFields(string(data))
	if err != nil {
		return fmt.Errorf("请求头负载：%w", err)
	}
	raw, ok := payload["header"]
	if !ok {
		return nil
	}
	header, err := decodeFields(string(raw))
	if err != nil {
		return fmt.Errorf("请求头：%w", err)
	}
	touched := false
	if _, ok := header["system"]; ok && !options.KeepSystem {
		header["system"] = json.RawMessage(systemToken)
		touched = true
	}
	if _, ok := header["tools"]; ok && !options.KeepTools {
		header["tools"] = json.RawMessage(toolsToken)
		touched = true
	}
	if !touched {
		return nil
	}
	encoded, err := json.Marshal(header)
	if err != nil {
		return fmt.Errorf("请求头排不出去：%w", err)
	}
	return replaceRawPayload(fields, payload, "header", encoded)
}

// repack 把正文重新打包一次，于是一次落盘的批次边界不进快照。
//
// 源: packages/test-support/session-snapshot/src/normalize.ts:384-406（repackSessionSnapshot）
//
// 先把每一行展开回它那串事件、序号从头接一遍，再整段重新压。序号只是展开和
// 重压那两步要的锚，交出来的行**不带**它——所以一份被弹过头的日志（见
// docs/session-log-limit.md）压出来也没有问题：那些序号一个都不会出现在快照里。
func repack(body []json.RawMessage) ([]json.RawMessage, error) {
	var (
		events  []sessionlog.Event
		nextSeq int
	)
	for index, row := range body {
		fields, err := decodeFields(string(row))
		if err != nil {
			return nil, fmt.Errorf("第 %d 行：%w", index+2, err)
		}
		if packedRowTypes[sessionlog.RowType(fieldString(fields, "type"))] {
			fields["seq0"] = json.RawMessage(itoa(nextSeq))
		} else {
			fields["seq"] = json.RawMessage(itoa(nextSeq))
		}
		anchored, err := json.Marshal(fields)
		if err != nil {
			return nil, fmt.Errorf("第 %d 行排不出去：%w", index+2, err)
		}
		decoded, err := sessionlog.DecodeStorageRecord(anchored)
		if err != nil {
			return nil, fmt.Errorf("snapshot: 第 %d 行读不回来：%w", index+2, err)
		}
		events = append(events, decoded...)
		nextSeq += len(decoded)
	}

	rows, err := sessionlog.PackChunkRuns(events)
	if err != nil {
		return nil, fmt.Errorf("snapshot: 重打包失败：%w", err)
	}
	out := make([]json.RawMessage, 0, len(rows))
	for index, row := range rows {
		fields, err := decodeFields(string(row))
		if err != nil {
			return nil, fmt.Errorf("snapshot: 重打包出来的第 %d 行：%w", index+1, err)
		}
		delete(fields, "seq")
		delete(fields, "time")
		delete(fields, "seq0")
		delete(fields, "time0")
		projected, err := json.Marshal(fields)
		if err != nil {
			return nil, fmt.Errorf("snapshot: 重打包出来的第 %d 行排不出去：%w", index+1, err)
		}
		out = append(out, projected)
	}
	return out, nil
}

// packedRowTypes 是三种打包过的分块行标签，它们的信封用 seq0/time0 而不是 seq/time。
var packedRowTypes = map[sessionlog.RowType]bool{
	sessionlog.RowTextChunks:      true,
	sessionlog.RowReasoningChunks: true,
	sessionlog.RowToolCallChunks:  true,
}

func decodeFields(line string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &fields); err != nil {
		return nil, fmt.Errorf("必须是一个 JSON 对象：%w", err)
	}
	if fields == nil {
		return nil, errors.New("必须是一个 JSON 对象")
	}
	return fields, nil
}

func fieldString(fields map[string]json.RawMessage, key string) string {
	raw, ok := fields[key]
	if !ok {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value
}

func replacePayload(
	fields map[string]json.RawMessage,
	payload map[string]json.RawMessage,
	key string,
	value any,
) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("%s 排不出去：%w", key, err)
	}
	return replaceRawPayload(fields, payload, key, encoded)
}

func replaceRawPayload(
	fields map[string]json.RawMessage,
	payload map[string]json.RawMessage,
	key string,
	value json.RawMessage,
) error {
	payload[key] = value
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("负载排不出去：%w", err)
	}
	fields["data"] = encoded
	return nil
}

func itoa(value int) string {
	return strconv.Itoa(value)
}
