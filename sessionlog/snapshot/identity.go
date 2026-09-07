// 本文件的作用：把易变的不透明标识换成按类型编号的记号，并且**跨父子日志**
// 保住原来的相等关系。
//
// 源: packages/test-support/session-snapshot/src/identity.ts

package snapshot

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// IDKind 是一个标识的类别，也就是它换出来那个记号的前半截。
//
// 源: packages/test-support/session-snapshot/src/identity.ts:7
type IDKind string

const (
	// KindSession 是会话身份，取自每一份日志的头行。
	KindSession IDKind = "session"
	// KindMessage 是一条模型消息的身份。
	KindMessage IDKind = "message"
	// KindApproval 是一次审批往返的身份。
	KindApproval IDKind = "approval"
	// KindWorkflow 是一次子 agent 派发的 runId。
	KindWorkflow IDKind = "workflow"
	// KindCommand 是一次命令执行的 commandId。
	KindCommand IDKind = "command"
	// KindRetry 是一次模型重试的 retryId。
	KindRetry IDKind = "retry"
	// KindOther 是别的以 id 结尾的键，没有更细的类别。
	//
	// 新增: DSH 还有一类 `rpc`，认的是 ACP 子进程 stdout 上那条 JSON-RPC 帧号。
	// 那条转写稿连同起子进程的那一半一起没有移，所以这里没有它。
	KindOther IDKind = "id"
)

var (
	// uuidRE 是 UUID v4 的形状，也就是随机铸出来的标识长的样子。
	uuidRE = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	// canonicalTokenRE 认已经换过一轮的记号，它们原样留着并且占住自己那个序号。
	canonicalTokenRE = regexp.MustCompile(
		`^\{\{(session|message|approval|workflow|command|retry|id):([1-9][0-9]*)\}\}$`)
	// idKeyRE 认「这个键装的是一个标识」。
	idKeyRE = regexp.MustCompile(`(?:^id$|Id$|Ids$)`)
	// asMessageRE 认一句人话里嵌着的消息身份。
	//
	// 源: packages/subagent/tool-subagent-report/src/index.ts（"as message "）
	asMessageRE = regexp.MustCompile(`(?i)\bas message ([0-9a-f-]{36})\b`)
)

// 新增: DSH 还认一种 `{{sessionId}}` / `{{messageId}}` 的旧记号，那是它上一版
// 归一化器留下的存量夹具。本仓库没有那一版，也就没有那种存量。

// RedactIDs 把一个场景里那些易变的不透明标识换成按类型编号的记号。
//
// 源: packages/test-support/session-snapshot/src/identity.ts:44-128
//
// logs 是同一个场景的全部日志，父会话在最前。**必须一次全交进来**：编号是按
// 「第一次见到」的顺序在整个场景里连续发的，一份一份地压，父会话里的
// `{{session:1}}` 和孩子里指回来的那个就成了两个不相干的记号，而那条父子线正是
// 快照要验的东西。
//
// 一个值只在它看着像随机铸出来的（UUID 形状）时才换；已经是记号的原样留着，并且
// 把自己那个序号占住，于是重复压一份已经压过的日志不会把编号越挤越大。
func RedactIDs(logs [][]byte) ([][]byte, error) {
	parsed := make([][]any, len(logs))
	for index, log := range logs {
		records, err := decodeRecords(log)
		if err != nil {
			return nil, fmt.Errorf("snapshot: 第 %d 份日志：%w", index, err)
		}
		parsed[index] = records
	}

	table := newTokenTable()
	// 头行的 id 无条件认领：会话身份不一定长成 UUID（宿主自己起的名字也是合法的），
	// 而它恰恰是最该换掉的那一个。
	for _, records := range parsed {
		if len(records) == 0 {
			continue
		}
		header, ok := records[0].(map[string]any)
		if !ok {
			continue
		}
		table.claim(header["id"], KindSession, true)
	}
	for _, records := range parsed {
		for _, record := range records {
			table.collect(record, recordType(record))
		}
	}

	out := make([][]byte, len(logs))
	for index, records := range parsed {
		replaced := make([]any, len(records))
		for i, record := range records {
			replaced[i] = table.replace(record)
		}
		encoded, err := encodeRecords(replaced)
		if err != nil {
			return nil, fmt.Errorf("snapshot: 第 %d 份日志：%w", index, err)
		}
		out[index] = encoded
	}
	return out, nil
}

// tokenTable 是「原值 → 记号」那张表，外加每一类已经发到第几号。
type tokenTable struct {
	tokenByValue map[string]string
	nextByKind   map[IDKind]int
}

func newTokenTable() *tokenTable {
	return &tokenTable{tokenByValue: map[string]string{}, nextByKind: map[IDKind]int{}}
}

// claim 给一个值发一个记号；already 为真时不管它长什么样都发。
func (t *tokenTable) claim(value any, kind IDKind, always bool) {
	text, ok := value.(string)
	if !ok || text == "" {
		return
	}
	if _, seen := t.tokenByValue[text]; seen {
		return
	}
	if matched := canonicalTokenRE.FindStringSubmatch(text); matched != nil {
		// 已经是记号：原样留着，但要把它那个序号占住，否则下一个新值会撞上它。
		ordinal, err := strconv.Atoi(matched[2])
		if err != nil {
			return
		}
		existing := IDKind(matched[1])
		t.nextByKind[existing] = max(t.nextByKind[existing], ordinal)
		t.tokenByValue[text] = text
		return
	}
	if !always && !uuidRE.MatchString(text) {
		return
	}
	next := t.nextByKind[kind] + 1
	t.nextByKind[kind] = next
	t.tokenByValue[text] = fmt.Sprintf("{{%s:%d}}", kind, next)
}

// collect 走一遍一条记录，把它里面每一个标识都认领掉。
//
// kind 由**键名**决定，而不是由值长什么样决定：一条日志里 `runId` 和 `retryId`
// 装的都是 UUID，光看值分不出它们是两类东西。
func (t *tokenTable) collect(value any, recordKind string) {
	switch typed := value.(type) {
	case string:
		for _, matched := range asMessageRE.FindAllStringSubmatch(typed, -1) {
			t.claim(matched[1], KindMessage, false)
		}
	case []any:
		for _, item := range typed {
			t.collect(item, recordKind)
		}
	case map[string]any:
		if id, ok := messageID(typed); ok {
			t.claim(id, KindMessage, false)
		}
		for _, key := range sortedKeys(typed) {
			item := typed[key]
			switch {
			case key == "id" && (recordKind == "approval/asked" || recordKind == "approval/decided"):
				t.claim(item, KindApproval, false)
			case key == "commandId":
				t.claim(item, KindCommand, true)
			case key == "retryId":
				t.claim(item, KindRetry, false)
			case key == "runId":
				t.claim(item, KindWorkflow, false)
			case idKeyRE.MatchString(key):
				t.claim(item, KindOther, false)
			}
			t.collect(item, recordKind)
		}
	}
}

// replace 把一条记录里认领过的原值全换成它们的记号。
func (t *tokenTable) replace(value any) any {
	switch typed := value.(type) {
	case string:
		if token, ok := t.tokenByValue[typed]; ok {
			return token
		}
		// 整段对不上时按子串换：一个标识可能嵌在一句人话里。长的先换，
		// 免得一个短的把一个包着它的长值切成两半。
		out := typed
		for _, source := range t.replacementOrder() {
			out = strings.ReplaceAll(out, source, t.tokenByValue[source])
		}
		return out
	case []any:
		items := make([]any, len(typed))
		for index, item := range typed {
			items[index] = t.replace(item)
		}
		return items
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = t.replace(item)
		}
		return out
	default:
		return value
	}
}

// replacementOrder 是子串替换的次序：长的在前，同长按字典序，于是它是确定的。
func (t *tokenTable) replacementOrder() []string {
	sources := make([]string, 0, len(t.tokenByValue))
	for source := range t.tokenByValue {
		sources = append(sources, source)
	}
	slices.SortFunc(sources, func(left, right string) int {
		if diff := len(right) - len(left); diff != 0 {
			return diff
		}
		return strings.Compare(left, right)
	})
	return sources
}

// messageID 认一条 [github.com/snight1983/ds-harness-go/llm.Message]：四个字段
// 齐了才算，光有一个 id 不算。
//
// 源: packages/test-support/session-snapshot/src/identity.ts:28-35
func messageID(record map[string]any) (string, bool) {
	id, ok := record["id"].(string)
	if !ok {
		return "", false
	}
	if _, ok := record["role"].(string); !ok {
		return "", false
	}
	if _, ok := record["content"].([]any); !ok {
		return "", false
	}
	if _, ok := record["source"].(map[string]any); !ok {
		return "", false
	}
	return id, true
}

// recordType 读一条记录的事件类型；不是对象或者没这个字段时是空串。
func recordType(record any) string {
	object, ok := record.(map[string]any)
	if !ok {
		return ""
	}
	kind, _ := object["type"].(string)
	return kind
}

// decodeRecords 把一份 JSONL 拆成它那些记录。
//
// 新增: 数字走 [json.Number]，于是排回去还是原来那串字面量。默认那条
// float64 的路会把一个毫秒时间戳排成科学计数法，而快照是要逐字节比的。
func decodeRecords(log []byte) ([]any, error) {
	var records []any
	for index, line := range splitLines(log) {
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.UseNumber()
		var record any
		if err := decoder.Decode(&record); err != nil {
			return nil, fmt.Errorf("第 %d 行不是合法 JSON：%w", index+1, err)
		}
		records = append(records, record)
	}
	return records, nil
}

// encodeRecords 把一串记录排成一份 JSONL，末尾带换行。
func encodeRecords(records []any) ([]byte, error) {
	var out strings.Builder
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			return nil, fmt.Errorf("记录排不出去：%w", err)
		}
		out.Write(line)
		out.WriteByte('\n')
	}
	return []byte(out.String()), nil
}

// splitLines 切出一份 JSONL 里的非空行，顺带吃掉 CRLF 的那个回车。
func splitLines(log []byte) []string {
	var lines []string
	for _, raw := range strings.Split(string(log), "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

// sortedKeys 按字典序交出一张表的键。
//
// 新增: DSH 靠 JS 对象的插入顺序（也就是 JSON 里键出现的顺序）决定编号先后。
// Go 的 map 迭代顺序是随机的，照抄会让同一份日志压两次得到两套编号——那正好
// 毁掉本包存在的理由。所以改成字典序：编号和 DSH 那边对不上，但它是确定的。
func sortedKeys(record map[string]any) []string {
	keys := make([]string, 0, len(record))
	for key := range record {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
