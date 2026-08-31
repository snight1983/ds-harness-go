// 本文件验两套键空间的语法：什么字符串能当引用名、什么能当记录地址，
// 以及一个地址拆回两半之后还是不是原来那两半。
//
// 源: packages/credentials/credentials/tests/credentials.spec.ts:15-40

package credentials

import (
	"errors"
	"testing"
)

// TestNewRefBrandsPOSIXIdentifiers 钉住引用名的语法就是 POSIX shell 标识符。
//
// 源: packages/credentials/credentials/tests/credentials.spec.ts:16-20
func TestNewRefBrandsPOSIXIdentifiers(t *testing.T) {
	t.Parallel()

	for _, valid := range []string{"DEEPSEEK_API_KEY", "_private", "lower_case9"} {
		ref, err := NewRef(valid)
		if err != nil {
			t.Errorf("%q 本该是合法引用名，实际 %v", valid, err)
			continue
		}
		if string(ref) != valid {
			t.Errorf("引用名不该被改写，%q 变成了 %q", valid, string(ref))
		}
	}
}

// TestNewRefRejectsEveryOtherShape 逐条验不合语法的引用名。
//
// 源: packages/credentials/credentials/tests/credentials.spec.ts:22-26
//
// 每一条后面写的是它犯的是哪一款，因为这五条各自被正则的不同部分拒掉，
// 合并成一句「都不行」的话，正则被改松之后仍然会有几条碰巧通过。
func TestNewRefRejectsEveryOtherShape(t *testing.T) {
	t.Parallel()

	for name, invalid := range map[string]string{
		"空串":     "",
		"数字打头":   "9LEADING",
		"中间有连字符": "WITH-DASH",
		"中间有空格":  "WITH SPACE",
		"带冒号":    "ns:key",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := NewRef(invalid); !errors.Is(err, ErrInvalidRef) {
				t.Fatalf("该报 ErrInvalidRef，实际 %v", err)
			}
			if IsRefName(invalid) {
				t.Errorf("IsRefName 该和 NewRef 给出同一个答案，%q 上却说行", invalid)
			}
		})
	}
}

// TestIsKeySegmentAnswersWhatNewKeyWouldAccept 钉住谓词和构造函数是同一套语法。
//
// 源: packages/credentials/credentials/tests/credentials.spec.ts:29-40
//
// 两者分叉的后果很具体：消费方先问谓词、得到「行」，再去拼键却被拒，
// 于是它那条「语法外的单位读成没存过」的分支永远走不到。
func TestIsKeySegmentAnswersWhatNewKeyWouldAccept(t *testing.T) {
	t.Parallel()

	for _, valid := range []string{"llm-pi-ai", "openai-codex", "a", "z9"} {
		if !IsKeySegment(valid) {
			t.Errorf("%q 本该是合法的键段", valid)
		}
		if _, err := NewKey(valid, valid); err != nil {
			t.Errorf("%q 谓词说行，NewKey 却拒了：%v", valid, err)
		}
	}

	// 这几条是一份任意的配置字典键取得到、而记录 id 取不到的形状：
	// 消费方在这里问一句，而不是靠接住一个错误来知道。
	for _, invalid := range []string{"", "My_Proxy", "z.ai", "UPPER", "9leading", "a/b"} {
		if IsKeySegment(invalid) {
			t.Errorf("%q 本不该是合法的键段", invalid)
		}
		if _, err := NewKey(invalid, "ok"); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("scope=%q 该报 ErrInvalidKey，实际 %v", invalid, err)
		}
		if _, err := NewKey("ok", invalid); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("id=%q 该报 ErrInvalidKey，实际 %v", invalid, err)
		}
	}
}

// TestNewKeyJoinsTheTwoSegments 钉住拼出来的地址形状。
//
// 源: packages/credentials/credentials/src/index.ts:66-73
func TestNewKeyJoinsTheTwoSegments(t *testing.T) {
	t.Parallel()

	key, err := NewKey("llm-pi-ai", "openai-codex")
	if err != nil {
		t.Fatalf("NewKey 意外失败：%v", err)
	}
	if string(key) != "llm-pi-ai/openai-codex" {
		t.Errorf("地址该是 llm-pi-ai/openai-codex，实际 %q", string(key))
	}
}

// TestParseKeyIsTheReadHalfOfNewKey 钉住从磁盘读回来的地址走的是同一套校验。
//
// 源: packages/credentials/credentials/src/index.ts:82-89
func TestParseKeyIsTheReadHalfOfNewKey(t *testing.T) {
	t.Parallel()

	key, err := ParseKey("llm-pi-ai/openai-codex")
	if err != nil {
		t.Fatalf("ParseKey 意外失败：%v", err)
	}
	if string(key) != "llm-pi-ai/openai-codex" {
		t.Errorf("地址该原样留着，实际 %q", string(key))
	}

	for name, invalid := range map[string]string{
		// 没有分隔符：只有 strings.Cut 那一条拒得掉。
		"一段都没有": "no-slash",
		// 三段：只有「id 里还有斜杠」那一条拒得掉。取前两段的话，
		// 这个地址会静默指向另一条记录。
		"三段": "a/b/c",
		// 两段但段本身不合语法：只有 NewKey 里那次校验拒得掉。
		"段不合语法": "A/b",
		// 空串两条都占：先没有分隔符。
		"空串": "",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := ParseKey(invalid); !errors.Is(err, ErrInvalidKey) {
				t.Fatalf("该报 ErrInvalidKey，实际 %v", err)
			}
		})
	}
}

// TestKeyScopeAndIDSplitTheTwoHalves 钉住两个读取方法各取哪一半。
//
// 源: packages/credentials/credentials/src/index.ts:98-112
func TestKeyScopeAndIDSplitTheTwoHalves(t *testing.T) {
	t.Parallel()

	key, err := NewKey("llm-pi-ai", "openai-codex")
	if err != nil {
		t.Fatalf("NewKey 意外失败：%v", err)
	}
	if key.Scope() != "llm-pi-ai" {
		t.Errorf("Scope 该是 llm-pi-ai，实际 %q", key.Scope())
	}
	if key.ID() != "openai-codex" {
		t.Errorf("ID 该是 openai-codex，实际 %q", key.ID())
	}
}

// TestKeyReadersSurviveAnUnbrandedValue 钉住手工转出来的地址不会把读取方法搞崩。
//
// 新增: DSH 的品牌类型只能由两个都校验过的构造函数产出，所以它敢直接 slice。
// Go 的 Key("garbage") 是一次免费的类型转换，绕得过构造函数。
// 这条用例存在的理由是：这两个读取方法会被配置界面拿去展示一条**孤儿记录**，
// 而孤儿正是最有可能不合语法的那种地址——在展示错误的路径上再崩一次，
// 排查的人连那条坏地址长什么样都看不到。
func TestKeyReadersSurviveAnUnbrandedValue(t *testing.T) {
	t.Parallel()

	orphan := Key("garbage")
	if orphan.Scope() != "garbage" {
		t.Errorf("没有分隔符时 Scope 该给出整串，实际 %q", orphan.Scope())
	}
	if orphan.ID() != "" {
		t.Errorf("没有分隔符时 ID 该是空串，实际 %q", orphan.ID())
	}
}

// TestTheTwoGrammarsAreDisjoint 钉住一个地址永远不会被读成一个引用名，反之亦然。
//
// 新增: DSH 把这条写在 CredentialKey 的类型注释里（「`/` 也让这套语法和
// CredentialRef 不相交，于是两套键空间永远撞不上」），但没有用例压。
// 值得钉住，因为它是「两套键空间共用一个提供方」这件事能成立的**全部**依据：
// 哪天有人把 `/` 从某个正则里放进去，症状会是一边的写盖掉另一边的读。
func TestTheTwoGrammarsAreDisjoint(t *testing.T) {
	t.Parallel()

	key, err := NewKey("llm-pi-ai", "openai-codex")
	if err != nil {
		t.Fatalf("NewKey 意外失败：%v", err)
	}
	if IsRefName(string(key)) {
		t.Errorf("一个合法地址 %q 不该同时是一个合法引用名", string(key))
	}
	for _, ref := range []string{"DEEPSEEK_API_KEY", "_private", "lower_case9"} {
		if _, err := ParseKey(ref); err == nil {
			t.Errorf("一个合法引用名 %q 不该同时是一个合法地址", ref)
		}
	}
}
