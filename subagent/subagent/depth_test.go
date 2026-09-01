// 本文件的作用：派发深度这本账的测试——深度从哪儿读，以及哪种递归上限不成立。

package subagent

import (
	"errors"
	"testing"

	coresession "github.com/snight1983/ds-harness-go/core/session"
	"github.com/snight1983/ds-harness-go/session"
)

// agentAtDepth 造一个持久头里记着确切派发深度的假 agent。
//
// 深度只有会话头这一个来源（见 [DelegationDepthOf] 的说明），所以这里必须绕开
// newFakeAgent 自己去拼那份头。
func agentAtDepth(t *testing.T, id string, depth int) *fakeAgent {
	t.Helper()
	sessionID := session.SessionID(id)
	header := session.SessionHeader{ID: sessionID, Cwd: testAbsolutePath, DelegationDepth: depth}
	live, err := coresession.NewSession(sessionID, coresession.Options{Header: &header, Now: fixedClock()})
	if err != nil {
		t.Fatalf("造会话失败：%v", err)
	}
	return &fakeAgent{id: sessionID, scope: keyedScope(t, id, nil), session: live}
}

func TestDelegationDepthOfReadsTheDurableHeader(t *testing.T) {
	if depth := DelegationDepthOf(agentAtDepth(t, "top", 0)); depth != 0 {
		t.Fatalf("顶层的派发深度该是 0，实际 %d", depth)
	}
	if depth := DelegationDepthOf(agentAtDepth(t, "nested", 3)); depth != 3 {
		t.Fatalf("头里记着 3 就该读出 3，实际 %d", depth)
	}
}

func TestAssertMaxDepthAcceptsAbsentAndNonNegative(t *testing.T) {
	if err := AssertMaxDepth(nil); err != nil {
		t.Fatalf("不设上限该被接受，实际 %v", err)
	}
	for _, limit := range []int{0, 1, 7} {
		bound := limit
		if err := AssertMaxDepth(&bound); err != nil {
			t.Fatalf("上限 %d 该被接受，实际 %v", bound, err)
		}
	}
}

func TestAssertMaxDepthRejectsNegative(t *testing.T) {
	bound := -1
	err := AssertMaxDepth(&bound)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("负的上限该报请求不成立，实际 %v", err)
	}
}
