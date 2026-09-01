// 本文件的作用：同一个会话 id 底下的两份观察，什么算对得上。
//
// 源: packages/session-query/session-query/src/sources.ts

package sessionquery

import (
	"testing"

	"github.com/snight1983/ds-harness-go/session"
)

func TestAssertHeadersCompatibleComparesTheIdentityFields(t *testing.T) {
	t.Parallel()

	base := session.SessionHeader{
		Version: 1, ID: "s1", CreatedAt: 100, Cwd: "/work",
		ParentSession: "p1", SeedLength: 2, DelegationDepth: 1,
	}

	cases := map[string]struct {
		mutate     func(*session.SessionHeader)
		compatible bool
	}{
		"一模一样":       {mutate: func(*session.SessionHeader) {}, compatible: true},
		"展示分类不算身份":   {mutate: func(h *session.SessionHeader) { h.Origin = "cli" }, compatible: true},
		"预设可以在恢复时换掉": {mutate: func(h *session.SessionHeader) { h.AgentPreset = "别的" }, compatible: true},
		"格式版本不同":     {mutate: func(h *session.SessionHeader) { h.Version = 2 }},
		"id 不同":      {mutate: func(h *session.SessionHeader) { h.ID = "s2" }},
		"建会话时间不同":    {mutate: func(h *session.SessionHeader) { h.CreatedAt = 101 }},
		"工作目录不同":     {mutate: func(h *session.SessionHeader) { h.Cwd = "/别处" }},
		"父会话不同":      {mutate: func(h *session.SessionHeader) { h.ParentSession = "p2" }},
		"seed 长度不同":  {mutate: func(h *session.SessionHeader) { h.SeedLength = 3 }},
		"派发深度不同":     {mutate: func(h *session.SessionHeader) { h.DelegationDepth = 2 }},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			other := base
			testCase.mutate(&other)
			err := AssertHeadersCompatible(base, other)
			if testCase.compatible {
				if err != nil {
					t.Fatalf("这两份头本该对得上：%v", err)
				}
				return
			}
			requireCode(t, err, CodeSourceConflict)
		})
	}
}
