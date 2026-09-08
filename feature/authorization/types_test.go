package authorization

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/snight1983/ds-harness-go/invariants"
)

// TestPromptValidateRejectsIllegalCombinations 钉住把判别联合塌成一个结构体之后，
// 那几种非法组合仍然被挡住。
func TestPromptValidateRejectsIllegalCombinations(t *testing.T) {
	cases := []struct {
		name   string
		prompt Prompt
		ok     bool
	}{
		{"文本", Prompt{Kind: PromptText, Message: "问题"}, true},
		{"密文", Prompt{Kind: PromptSecret, Message: "问题", Placeholder: "sk-"}, true},
		{"单选", Prompt{Kind: PromptSelect, Message: "问题", Options: []PromptOption{{ID: "a", Label: "甲"}}}, true},
		{"没有正文", Prompt{Kind: PromptText}, false},
		{"文本带了选项", Prompt{Kind: PromptText, Message: "问题", Options: []PromptOption{{ID: "a"}}}, false},
		{"单选没有选项", Prompt{Kind: PromptSelect, Message: "问题"}, false},
		{"选项没有 id", Prompt{Kind: PromptSelect, Message: "问题", Options: []PromptOption{{Label: "甲"}}}, false},
		{"呈现方式不认得", Prompt{Kind: "hologram", Message: "问题"}, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.prompt.Validate()
			if testCase.ok && err != nil {
				t.Fatalf("该收下，实际 %v", err)
			}
			if !testCase.ok {
				if err == nil {
					t.Fatal("该拒掉")
				}
				if !errors.Is(err, CodeInvalidConfig) {
					t.Fatalf("该报 INVALID_CONFIG，实际 %v", err)
				}
			}
		})
	}
}

// TestFlowValidateRejectsIllegalRegistrations 钉住登记那一刻的入口检查。
func TestFlowValidateRejectsIllegalRegistrations(t *testing.T) {
	good := Flow{
		Key:     testKey,
		Label:   "演示令牌",
		Methods: []Method{{ID: "device", Label: "设备码"}},
		Run:     func(context.Context, Session) error { return nil },
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("一条正常的流程该收下，实际 %v", err)
	}

	cases := map[string]func(Flow) Flow{
		"没有凭据键":   func(f Flow) Flow { f.Key = ""; return f },
		"凭据键不合法":  func(f Flow) Flow { f.Key = "没有斜杠"; return f },
		"没有名字":    func(f Flow) Flow { f.Label = ""; return f },
		"一种方式都没有": func(f Flow) Flow { f.Methods = nil; return f },
		"方式没有 id": func(f Flow) Flow { f.Methods = []Method{{Label: "无名"}}; return f },
		"两种方式重名": func(f Flow) Flow {
			f.Methods = []Method{{ID: "device", Label: "甲"}, {ID: "device", Label: "乙"}}
			return f
		},
		"没有 Run": func(f Flow) Flow { f.Run = nil; return f },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			if err := mutate(good).Validate(); !errors.Is(err, CodeInvalidConfig) {
				t.Fatalf("该报 INVALID_CONFIG，实际 %v", err)
			}
		})
	}
}

// TestAttemptValidateRejectsUnusableRecords 钉住介质上那条记录的校验。
func TestAttemptValidateRejectsUnusableRecords(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	good := Attempt{
		ID:        "a-1",
		Key:       testKey,
		Method:    "device",
		Holder:    "alpha",
		HeldUntil: now.Add(time.Minute),
		StartedAt: now,
		UpdatedAt: now,
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("一条正常的记录该收下，实际 %v", err)
	}

	cases := map[string]func(Attempt) Attempt{
		"没有身份":   func(a Attempt) Attempt { a.ID = ""; return a },
		"没有凭据键":  func(a Attempt) Attempt { a.Key = ""; return a },
		"凭据键不合法": func(a Attempt) Attempt { a.Key = "没有斜杠"; return a },
		"没有方式":   func(a Attempt) Attempt { a.Method = ""; return a },
		"没有持有者":  func(a Attempt) Attempt { a.Holder = ""; return a },
		"没有租约到期": func(a Attempt) Attempt { a.HeldUntil = time.Time{}; return a },
		"没有起始时刻": func(a Attempt) Attempt { a.StartedAt = time.Time{}; return a },
		"没有更新时刻": func(a Attempt) Attempt { a.UpdatedAt = time.Time{}; return a },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			if err := mutate(good).Validate(); err == nil {
				t.Fatal("该拒掉")
			}
		})
	}
}

// TestAttemptHeldTreatsExpiryAsFree 钉住租约到期那一刻就算放开了。
func TestAttemptHeldTreatsExpiryAsFree(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	attempt := Attempt{HeldUntil: now}
	if attempt.Held(now) {
		t.Fatal("到期那一刻该算放开了")
	}
	if !attempt.Held(now.Add(-time.Nanosecond)) {
		t.Fatal("到期之前该算攥着")
	}
}

// TestValidateReleaseCatchesWedgedSlot 钉住那条不变量真的认得一个卡住的槽位。
//
// 源: packages/credentials/authorization/src/invariant.ts:18-40
func TestValidateReleaseCatchesWedgedSlot(t *testing.T) {
	settled := Settled{Key: testKey, Attempt: "a-1", Settlement: SettlementAuthorized}

	if err := ValidateRelease(settled, Entry{Key: testKey}, true); err != nil {
		t.Fatalf("一个放开了的槽位该过，实际 %v", err)
	}
	if err := ValidateRelease(settled, Entry{}, false); err != nil {
		t.Fatalf("读不到就不判，实际 %v", err)
	}
	if err := ValidateRelease(settled, Entry{Key: testKey, InFlight: true}, true); err == nil {
		t.Fatal("还攥着的槽位该被逮住")
	}
	if err := ValidateRelease(settled, Entry{Key: testKey, Stalled: true, Attempt: "a-1"}, true); err == nil {
		t.Fatal("活过了自己那次结算的尝试该被逮住")
	}
	if err := ValidateRelease(settled, Entry{Key: testKey, Stalled: true, Attempt: "a-2"}, true); err != nil {
		t.Fatalf("另一次尝试留下的记录不归这条不变量管，实际 %v", err)
	}
}

// TestRegisterInvariantsWatchesSettlement 钉住那条不变量真的挂上了结算这条路，
// 而且一次干净的结算不会把它惹响。
//
// 违例是以 panic 的形式沿栈上抛的（[invariants.Fail]），所以「没响」这件事由
// 这个用例不 panic 本身钉住。
func TestRegisterInvariantsWatchesSettlement(t *testing.T) {
	h := newHarness(t)
	service := h.open("alpha")
	h.register(service, func(context.Context, Session) error {
		h.creds.commit(testKey)
		return nil
	})

	registry, err := invariants.New(invariants.Config{})
	if err != nil {
		t.Fatalf("建不变量注册表不该失败：%v", err)
	}
	unregister, err := RegisterInvariants(t.Context(), registry, service)
	if err != nil {
		t.Fatalf("注册不变量不该失败：%v", err)
	}
	t.Cleanup(unregister)

	if _, err := service.Begin(t.Context(), Request{Key: testKey, Interaction: &scriptedInteraction{}}); err != nil {
		t.Fatalf("授权不该失败：%v", err)
	}
}

// TestSettledListenerViolationIsNotSwallowed 钉住那道兜旁观者的网不许兜住不变量违例。
//
// 这条很容易在改动里失手：结算那条路上确实要兜住一个炸了的旁观者，但
// [invariants.Fail] 是故意 panic 的，兜住它等于把自检关掉。
func TestSettledListenerViolationIsNotSwallowed(t *testing.T) {
	h := newHarness(t)
	service := h.open("alpha")
	h.register(service, func(context.Context, Session) error {
		h.creds.commit(testKey)
		return nil
	})

	service.OnSettled(func(Settled) {
		panic(&invariants.Error{PackageName: PackageName, Message: "演一次违例"})
	})

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("违例该一路抛出来")
		}
		var violation *invariants.Error
		if !errors.As(recovered.(error), &violation) {
			t.Fatalf("抛出来的该是一条违例，实际 %v", recovered)
		}
	}()
	_, _ = service.Begin(t.Context(), Request{Key: testKey, Interaction: &scriptedInteraction{}})
}

// TestRegisterInvariantsRejectsMissingCollaborators 钉住装配缺件当场说出来。
func TestRegisterInvariantsRejectsMissingCollaborators(t *testing.T) {
	if _, err := RegisterInvariants(t.Context(), nil, nil); !errors.Is(err, CodeInvalidConfig) {
		t.Fatalf("该报 INVALID_CONFIG，实际 %v", err)
	}
}

// TestOpenRejectsMissingCollaborators 钉住打开时缺件当场说出来。
func TestOpenRejectsMissingCollaborators(t *testing.T) {
	h := newHarness(t)
	if _, err := Open(t.Context(), Config{Credentials: h.creds}); !errors.Is(err, CodeInvalidConfig) {
		t.Fatalf("缺域设施该报 INVALID_CONFIG，实际 %v", err)
	}
}

// TestErrorUnwrapsToCause 钉住两条链是分开的：Code 说处置，Cause 说底下出了什么事。
func TestErrorUnwrapsToCause(t *testing.T) {
	cause := errors.New("介质掉线了")
	err := wrapError(CodeStoreFailed, cause, "写不下去")
	if !errors.Is(err, CodeStoreFailed) {
		t.Fatal("该认得自己的 Code")
	}
	if !errors.Is(err, cause) {
		t.Fatal("该一路穿到底层那条")
	}
	if errors.Is(err, CodeNoFlow) {
		t.Fatal("不该认别的 Code")
	}
}

// TestDeclinedIsRecognisable 钉住界面报「人拒绝了」的那个错认得出来。
func TestDeclinedIsRecognisable(t *testing.T) {
	if !errors.Is(Declined(""), CodeDeclined) {
		t.Fatal("Declined 该带 DECLINED 这个码")
	}
}
