// 本文件的作用：一份读回来的存档要过的那三道判据，它们的**次序**，以及补平和
// seed 覆盖那两个纯函数。
//
// 源: packages/session/session-persistence/src/coordinator.ts:874-900
// 源: packages/session/session-persistence/src/coordinator.ts:1044-1082

package persistence

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ds-harness-go/session"
)

func TestCheckStoredIdentity(t *testing.T) {
	t.Parallel()

	t.Run("对得上就放行", func(t *testing.T) {
		t.Parallel()

		if err := CheckStoredIdentity("s1", testHeader(t, "s1")); err != nil {
			t.Fatalf("不该报错：%v", err)
		}
	})

	t.Run("对不上就拦住", func(t *testing.T) {
		t.Parallel()

		err := CheckStoredIdentity("s1", testHeader(t, "s2"))
		if !errors.Is(err, ErrIdentityMismatch) {
			t.Fatalf("该报身份不符：%v", err)
		}
		// 两个身份都得出现，否则运维只知道「不对」不知道「哪儿不对」。
		if !strings.Contains(err.Error(), "s1") || !strings.Contains(err.Error(), "s2") {
			t.Fatalf("错误里该同时有请求的和头里写的：%q", err.Error())
		}
	})
}

func TestCheckStoredVersion(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		version int
		refuses bool
	}{
		"本构建的版本":   {version: session.FormatVersion},
		"比本构建新":    {version: session.FormatVersion + 1, refuses: true},
		"比本构建旧":    {version: session.FormatVersion - 1, refuses: true},
		"没写版本（零值）": {version: 0, refuses: session.FormatVersion != 0},
	}

	for name, item := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			meta := testHeader(t, "s1")
			meta.Version = item.version
			err := CheckStoredVersion(meta)

			if !item.refuses {
				if err != nil {
					t.Fatalf("不该拒：%v", err)
				}
				return
			}
			var refusal *FormatUnsupportedError
			if !errors.As(err, &refusal) {
				t.Fatalf("该给出一条格式拒绝：%v", err)
			}
			if refusal.Location != nil {
				t.Fatalf("这个纯函数不认识后端，不该自己填位置：%#v", refusal.Location)
			}
		})
	}
}

func TestCheckStoredVocabulary(t *testing.T) {
	t.Parallel()

	meta := testHeader(t, "s1")
	vocabulary := session.CoreVocabulary()

	t.Run("全认识就放行", func(t *testing.T) {
		t.Parallel()

		events := []session.Event{userEvent(t, 1, "hi")}
		if err := CheckStoredVocabulary(meta, events, vocabulary); err != nil {
			t.Fatalf("不该拒：%v", err)
		}
	})

	t.Run("不认识的必需事件拒整份日志", func(t *testing.T) {
		t.Parallel()

		events := []session.Event{{Type: "future/thing", Seq: 1}}
		err := CheckStoredVocabulary(meta, events, vocabulary)

		var refusal *FormatUnsupportedError
		if !errors.As(err, &refusal) {
			t.Fatalf("该给出一条格式拒绝：%v", err)
		}
		// 拒的是整份日志，不是那一条事件——静默跳过等于重建出一个错的会话。
		if !errors.Is(err, ErrFormatUnsupported) || errors.Is(err, ErrCorrupted) {
			t.Fatalf("这是格式拒绝，不是损坏：%v", err)
		}
	})

	t.Run("标了可跳过就跳过", func(t *testing.T) {
		t.Parallel()

		events := []session.Event{{Type: "future/thing", Seq: 1, Ignorable: true}}
		if err := CheckStoredVocabulary(meta, events, vocabulary); err != nil {
			t.Fatalf("标了可跳过就不该拒：%v", err)
		}
	})
}

func TestCheckStoredRunsTheChecksInOrder(t *testing.T) {
	t.Parallel()

	// 次序是这个函数的全部内容：三样同时不对时，报出来的必须是**第一道**。
	// 拿错了东西的时候说「版本不对」，或者版本不对的时候说「有个没见过的类型」，
	// 都是会把人带偏的话。
	meta := testHeader(t, "s2")
	meta.Version = session.FormatVersion + 1
	events := []session.Event{{Type: "future/thing", Seq: 1}}

	err := CheckStored(nil, "s1", meta, events, session.CoreVocabulary())
	if !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("身份必须排在最前：%v", err)
	}

	meta.ID = "s1"
	err = CheckStored(nil, "s1", meta, events, session.CoreVocabulary())
	if !errors.Is(err, ErrFormatUnsupported) {
		t.Fatalf("版本必须排在词汇前：%v", err)
	}
	if strings.Contains(err.Error(), "future/thing") {
		t.Fatalf("版本不对时不该去谈词汇：%q", err.Error())
	}

	meta.Version = session.FormatVersion
	err = CheckStored(nil, "s1", meta, events, session.CoreVocabulary())
	if !errors.Is(err, ErrFormatUnsupported) || !strings.Contains(err.Error(), "future/thing") {
		t.Fatalf("前两道过了才轮到词汇：%v", err)
	}

	// 三道全过就放行。
	good := []session.Event{userEvent(t, 1, "hi")}
	if err := CheckStored(nil, "s1", meta, good, session.CoreVocabulary()); err != nil {
		t.Fatalf("一份好存档不该被拦：%v", err)
	}
}

func TestCheckStoredLocatesTheRefusalWhenTheBackendKnowsTheWay(t *testing.T) {
	t.Parallel()

	meta := testHeader(t, "s1")
	meta.Version = session.FormatVersion + 1

	t.Run("认路的后端把路径补上去", func(t *testing.T) {
		t.Parallel()

		err := CheckStored(locatingStub{path: "/logs/s1.jsonl"}, "s1", meta, nil, session.CoreVocabulary())
		var refusal *FormatUnsupportedError
		if !errors.As(err, &refusal) || refusal.Location == nil {
			t.Fatalf("该带上位置：%v", err)
		}
		if refusal.Location.Path != "/logs/s1.jsonl" {
			t.Fatalf("路径不对：%q", refusal.Location.Path)
		}
	})

	t.Run("认路但这个会话没有独立存档", func(t *testing.T) {
		t.Parallel()

		err := CheckStored(locatingStub{}, "s1", meta, nil, session.CoreVocabulary())
		var refusal *FormatUnsupportedError
		if !errors.As(err, &refusal) {
			t.Fatalf("该给出一条格式拒绝：%v", err)
		}
		if refusal.Location != nil {
			t.Fatalf("后端说没有存档，就不该凭空造一个位置：%#v", refusal.Location)
		}
	})

	t.Run("不认路的后端原样上抛", func(t *testing.T) {
		t.Parallel()

		err := CheckStored(plainStub{}, "s1", meta, nil, session.CoreVocabulary())
		var refusal *FormatUnsupportedError
		if !errors.As(err, &refusal) || refusal.Location != nil {
			t.Fatalf("不认路就不该有位置：%v", err)
		}
	})

	t.Run("不是格式拒绝的错误不碰", func(t *testing.T) {
		t.Parallel()

		other := errors.New("别的毛病")
		if got := locateRefusal(locatingStub{path: "/x"}, meta, other); !errors.Is(got, other) {
			t.Fatalf("该原样返回：%v", got)
		}
	})
}

func TestBalanceStored(t *testing.T) {
	t.Parallel()

	t.Run("平衡的日志原样返回", func(t *testing.T) {
		t.Parallel()

		events := []session.Event{turnStart(t, 1, 0), turnEnd(t, 2, 0)}
		balanced, closers, err := BalanceStored(events)
		if err != nil {
			t.Fatalf("不该报错：%v", err)
		}
		if len(closers) != 0 {
			t.Fatalf("不该补东西：%#v", closers)
		}
		if len(balanced) != len(events) {
			t.Fatalf("原样返回：%d != %d", len(balanced), len(events))
		}
	})

	t.Run("断掉的尾巴补上收尾", func(t *testing.T) {
		t.Parallel()

		events := []session.Event{turnStart(t, 1, 0)}
		balanced, closers, err := BalanceStored(events)
		if err != nil {
			t.Fatalf("不该报错：%v", err)
		}
		if len(closers) == 0 {
			t.Fatalf("该补出收尾")
		}
		if len(balanced) != len(events)+len(closers) {
			t.Fatalf("补齐后该是原日志加收尾：%d", len(balanced))
		}
		if balanced[len(balanced)-1].Type != session.EventTurnEnd {
			t.Fatalf("最后一条该是回合结束：%v", balanced[len(balanced)-1].Type)
		}
	})

	t.Run("补两遍得到一样的东西", func(t *testing.T) {
		t.Parallel()

		// 补出来的事件是确定的，崩溃修复才敢重跑：一次修复写了一半又崩，
		// 下次进来必须补出**同样**的几条，而不是在日志上越垒越多。
		events := []session.Event{turnStart(t, 1, 0)}
		first, _, err := BalanceStored(events)
		if err != nil {
			t.Fatalf("不该报错：%v", err)
		}
		second, _, err := BalanceStored(events)
		if err != nil {
			t.Fatalf("不该报错：%v", err)
		}
		for index := range first {
			same, err := sameEventBytes(first[index], second[index])
			if err != nil {
				t.Fatalf("比不了：%v", err)
			}
			if !same {
				t.Fatalf("第 %d 条补两遍不一样", index)
			}
		}
	})

	t.Run("坏负载上抛", func(t *testing.T) {
		t.Parallel()

		events := []session.Event{{Type: session.EventTurnStart, Seq: 1, Data: []byte(`"不是对象"`)}}
		if _, _, err := BalanceStored(events); err == nil {
			t.Fatalf("坏负载该报错")
		}
	})
}

func TestSeedCoversPrefix(t *testing.T) {
	t.Parallel()

	a, b := userEvent(t, 1, "a"), userEvent(t, 2, "b")

	cases := map[string]struct {
		seed, prefix []session.Event
		covers       bool
	}{
		"逐条复现":       {seed: []session.Event{a, b}, prefix: []session.Event{a, b}, covers: true},
		"seed 更长":    {seed: []session.Event{a, b}, prefix: []session.Event{a}, covers: true},
		"空前缀":        {seed: []session.Event{a}, covers: true},
		"前缀比 seed 长": {seed: []session.Event{a}, prefix: []session.Event{a, b}},
		"某一条不一样":     {seed: []session.Event{a, a}, prefix: []session.Event{a, b}},
	}

	for name, item := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := SeedCoversPrefix(item.seed, item.prefix)
			if err != nil {
				t.Fatalf("不该报错：%v", err)
			}
			if got != item.covers {
				t.Fatalf("该是 %v，实际 %v", item.covers, got)
			}
		})
	}

	t.Run("排不出去的事件报错", func(t *testing.T) {
		t.Parallel()

		// Data 是 json.RawMessage，一段不合法的字节在排出去时才被发现。
		// 报错而不是当成「不一样」：那是一条坏事件，不是一次不匹配。
		broken := session.Event{Type: session.EventUserMessage, Seq: 1, Data: []byte("{")}
		if _, err := SeedCoversPrefix([]session.Event{broken}, []session.Event{a}); err == nil {
			t.Fatalf("坏事件该报错")
		}
		if _, err := SeedCoversPrefix([]session.Event{a}, []session.Event{broken}); err == nil {
			t.Fatalf("坏事件该报错")
		}
	})
}

// plainStub 是一个只实现 [Backend]、不认路的后端。
type plainStub struct{}

func (plainStub) Name() string { return "plain" }

func (plainStub) LoadStored(context.Context, session.SessionID) (StoredPrefix, error) {
	return StoredPrefix{}, ErrSessionNotFound
}

func (plainStub) ReadStoredRevision(context.Context, session.SessionID) (Revision, error) {
	return "", ErrSessionNotFound
}

func (plainStub) AppendBatch(context.Context, session.SessionHeader, []session.Event, bool) error {
	return nil
}

func (plainStub) CommitRepair(context.Context, session.SessionHeader, any, []session.Event) error {
	return nil
}

func (plainStub) List(context.Context) ([]session.SessionHeader, error) { return nil, nil }

// locatingStub 是一个认路的后端；path 为空表示这个会话没有独立存档。
type locatingStub struct {
	plainStub
	path string
}

func (s locatingStub) Locate(session.SessionHeader) (Location, bool) {
	if s.path == "" {
		return Location{}, false
	}
	return Location{Kind: "file", Path: s.path}, true
}
