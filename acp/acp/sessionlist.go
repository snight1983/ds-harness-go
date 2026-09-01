// 本文件的作用：`session/list` 那一页——不透明游标的编解码、两个目录算不算同一个，
// 以及「最新的排前面」那条定死的次序。
//
// 源: packages/acp/acp/src/index.ts:457-512, 526-535

package acp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"

	sessionlog "github.com/snight1983/ds-harness-go/session"
)

// sessionListEntry 是排序和翻页看得见的那份摘要。
//
// 源: packages/acp/acp/src/index.ts:316
type sessionListEntry struct {
	sessionID sessionlog.SessionID
	cwd       string
	createdAt int64
}

// sessionListCursor 是那条排序键：一个会话在「最新优先」这条序上的确切位置。
//
// 源: packages/acp/acp/src/index.ts:457-460
type sessionListCursor struct {
	createdAt int64
	sessionID string
}

// cursorChars 是一个 base64url 串里允许出现的字符。
//
// 源: packages/acp/acp/src/index.ts:476
var cursorChars = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// decodeSessionListCursor 解一条续页游标，一个字节都不多信。
//
// 源: packages/acp/acp/src/index.ts:473-496
//
// 第二个返回值为假表示这次请求没带游标，那就是要第一页。
//
// 那道「重新编一遍必须逐字相同」的检查是这里的要害：游标是这一端自己发出去的东西，
// 一个改过的游标不该被当成一个合法的位置接受——否则对面可以拿它翻出一条本不该给它
// 的记录。
//
// 新增: DSH 把 `JSON.parse` 解出来的数组直接再 `JSON.stringify` 一遍去比。Go 里解进
// `[]any` 会把数字变成 float64，再排出去可能带上小数点或者科学计数法，那条比较就永远
// 不成立了。所以这里先把两个元素按各自的类型解出来，再用**同样的类型**排回去。
func decodeSessionListCursor(value *string) (sessionListCursor, bool, error) {
	if value == nil {
		return sessionListCursor{}, false, nil
	}
	invalid := fmt.Errorf("session/list cursor is invalid")
	if !cursorChars.MatchString(*value) {
		return sessionListCursor{}, false, invalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(*value)
	if err != nil {
		return sessionListCursor{}, false, invalid
	}
	var fields []json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || len(fields) != 2 {
		return sessionListCursor{}, false, invalid
	}
	var createdAt int64
	var sessionID string
	if err := json.Unmarshal(fields[0], &createdAt); err != nil || createdAt < 0 {
		return sessionListCursor{}, false, invalid
	}
	if err := json.Unmarshal(fields[1], &sessionID); err != nil || sessionID == "" {
		return sessionListCursor{}, false, invalid
	}
	cursor := sessionListCursor{createdAt: createdAt, sessionID: sessionID}
	if encodeSessionListCursor(cursor) != *value {
		return sessionListCursor{}, false, invalid
	}
	return cursor, true, nil
}

// encodeSessionListCursor 把最后交出去的那个位置编成一个不透明的续页令牌。
//
// 源: packages/acp/acp/src/index.ts:498-501
func encodeSessionListCursor(cursor sessionListCursor) string {
	// 编一个两元素的数组不会失败。
	encoded, _ := json.Marshal([]any{cursor.createdAt, cursor.sessionID})
	return base64.RawURLEncoding.EncodeToString(encoded)
}

// isAfterSessionListCursor 判一条摘要在「最新优先」这条序上排不排在游标后面。
//
// 源: packages/acp/acp/src/index.ts:503-507
func isAfterSessionListCursor(entry sessionListEntry, cursor sessionListCursor) bool {
	if entry.createdAt != cursor.createdAt {
		return entry.createdAt < cursor.createdAt
	}
	return compareSessionIDs(string(entry.sessionID), cursor.sessionID) > 0
}

// compareSessionIDs 按稳定的 UTF-8 字节比两个不透明标识，不看进程的区域设置。
//
// 源: packages/acp/acp/src/index.ts:509-512
func compareSessionIDs(left, right string) int {
	return bytes.Compare([]byte(left), []byte(right))
}

// sortSessionList 定死那条次序：新的在前，同一时刻按标识的字节升序。
//
// 源: packages/acp/acp/src/index.ts:320
//
// 这条序必须是全序而且稳定的：翻页靠的是「排在游标后面」这个判据，两条分不出先后的
// 记录会让某一条永远翻不到、或者翻到两次。
func sortSessionList(entries []sessionListEntry) {
	sort.Slice(entries, func(left, right int) bool {
		if entries[left].createdAt != entries[right].createdAt {
			return entries[left].createdAt > entries[right].createdAt
		}
		return compareSessionIDs(string(entries[left].sessionID), string(entries[right].sessionID)) < 0
	})
}

// sameDirectory 按物理身份比两个存在的目录，读不动的路径按字面比。
//
// 源: packages/acp/acp/src/index.ts:526-535
//
// 一个已经落档的会话，它的工作目录可能早就不在了。那时退回字面比较——两条都规范化
// 之后逐字相同就算同一个。
//
// 新增: DSH 用 node:fs 的 realpath，Go 这边是 [path/filepath.EvalSymlinks]。两者在
// 「路径不存在就失败」这一点上一致，所以那条退路的触发条件也一致。字面那一支 DSH 用
// path.resolve，这里用 [path/filepath.Abs] 加 [path/filepath.Clean]——Abs 自己就带 Clean，
// 而 resolve 同时做的正是这两件事。
func sameDirectory(left, right string) bool {
	if left == "" {
		return false
	}
	realLeft, leftErr := filepath.EvalSymlinks(left)
	realRight, rightErr := filepath.EvalSymlinks(right)
	if leftErr == nil && rightErr == nil {
		return realLeft == realRight
	}
	absLeft, leftErr := filepath.Abs(left)
	absRight, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return absLeft == absRight
}
