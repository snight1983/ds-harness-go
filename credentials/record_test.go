// 本文件验持久记录在介质上的那一趟往返：排出去、读回来，还是不是同一条。
//
// 源: packages/credentials/credentials/src/types.ts:30-59
//
// 这一层压的是「判别标签是类型自己，不是一个可以填错的字段」。DSH 靠结构类型
// 免费得到这件事，Go 需要 [Record] 那个封印接口加上下面这几条用例。
// 三种读不回来的形状各有各的后果，所以逐条钉，不合并成一句「都得报错」。

package credentials

import (
	"encoding/json"
	"errors"
	"testing"
)

// TestKindIsTheTypeItself 钉住两个判别标签各归各的类型。
//
// 源: packages/credentials/credentials/src/types.ts:37-38,52-53
func TestKindIsTheTypeItself(t *testing.T) {
	t.Parallel()

	if got := (APIKeyRecord{}).Kind(); got != KindAPIKey {
		t.Errorf("api-key 记录的标签该是 %q，实际 %q", KindAPIKey, got)
	}
	if got := (GrantRecord{}).Kind(); got != KindGrant {
		t.Errorf("grant 记录的标签该是 %q，实际 %q", KindGrant, got)
	}
}

// TestAnAPIKeyRecordSurvivesTheRoundTrip 钉住 api-key 那一路的往返，三种填法各一遍。
//
// 源: packages/credentials/credentials/src/types.ts:30-43
//
// 两个字段都空的那一条尤其要钉：它不是一条坏记录，陈述的是「拥有方确认这条路由
// 用它自己的环境发现来认证」（见 [APIKeyRecord]）。读回来变成读不回来的话，
// 配置界面会把一件已经确认过的事报成未配置。
func TestAnAPIKeyRecordSurvivesTheRoundTrip(t *testing.T) {
	t.Parallel()

	for name, original := range map[string]APIKeyRecord{
		"只有密钥":  {Key: "sk-live"},
		"只有环境值": {Env: map[string]string{"AWS_PROFILE": "prod"}},
		"两样都有":  {Key: "sk-live", Env: map[string]string{"AWS_PROFILE": "prod"}},
		"两样都空":  {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(original)
			if err != nil {
				t.Fatalf("排出去不该失败：%v", err)
			}
			back, err := UnmarshalRecord(data)
			if err != nil {
				t.Fatalf("读回来不该失败：%v", err)
			}
			typed, ok := back.(APIKeyRecord)
			if !ok {
				t.Fatalf("读回来的该是 APIKeyRecord，实际 %#v", back)
			}
			if typed.Key != original.Key {
				t.Errorf("密钥该原样回来，%q 变成了 %q", original.Key, typed.Key)
			}
			if len(typed.Env) != len(original.Env) {
				t.Fatalf("环境值该原样回来，%v 变成了 %v", original.Env, typed.Env)
			}
			for key, want := range original.Env {
				if typed.Env[key] != want {
					t.Errorf("环境值 %q 该是 %q，实际 %q", key, want, typed.Env[key])
				}
			}
		})
	}
}

// TestTheDiscriminantIsWrittenEvenWhenEverythingElseIsEmpty 钉住标签不会被省略掉。
//
// 两个字段都带 omitempty，全空的记录排出来只剩下 kind 一项——正是这一项让它
// 读回来还是一条 api-key 记录，而不是一条标签认不出来的记录（见 [UnmarshalRecord]）。
func TestTheDiscriminantIsWrittenEvenWhenEverythingElseIsEmpty(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(APIKeyRecord{})
	if err != nil {
		t.Fatalf("排出去不该失败：%v", err)
	}
	if string(data) != `{"kind":"api-key"}` {
		t.Fatalf("全空的记录该只剩标签，实际 %s", data)
	}
}

// TestAGrantPayloadComesBackByteForByte 钉住载荷是逐字节保管的。
//
// 源: packages/credentials/credentials/src/types.ts:45-56
//
// 这条用例的观察点是「一位数字都没差」，不是「解出来相等」：解成 map[string]any
// 再排回去的话，下面那个大整数会被 float64 磨掉、键的顺序会变（见 [GrantRecord.Payload]）。
// 磨掉一位数字的令牌再也换不回访问权，且没有任何一次错误能解释它。
func TestAGrantPayloadComesBackByteForByte(t *testing.T) {
	t.Parallel()

	// 键故意不按字典序，整数故意超出 float64 能精确表示的范围。
	const payload = `{"z":"last","refresh":"rt-1","expires_at":9007199254740993}`

	data, err := json.Marshal(GrantRecord{Payload: json.RawMessage(payload)})
	if err != nil {
		t.Fatalf("排出去不该失败：%v", err)
	}
	back, err := UnmarshalRecord(data)
	if err != nil {
		t.Fatalf("读回来不该失败：%v", err)
	}
	typed, ok := back.(GrantRecord)
	if !ok {
		t.Fatalf("读回来的该是 GrantRecord，实际 %#v", back)
	}
	if string(typed.Payload) != payload {
		t.Fatalf("载荷该逐字节回来：\n want %s\n  got %s", payload, typed.Payload)
	}
}

// TestAnEmptyGrantPayloadIsRefusedOnTheWayOut 钉住空载荷在**写出去之前**就被拦下。
//
// 拦在这里而不是等读回来：一条排成 `null` 的记录写到介质上之后，
// 在拥有方看来就是一次静默的授权丢失——那时候原来的载荷已经没了。
func TestAnEmptyGrantPayloadIsRefusedOnTheWayOut(t *testing.T) {
	t.Parallel()

	for name, payload := range map[string]json.RawMessage{
		"nil":       nil,
		"空字节":       json.RawMessage(""),
		"不是合法 JSON": json.RawMessage(`{"refresh":`),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := json.Marshal(GrantRecord{Payload: payload}); !errors.Is(err, ErrMalformedRecord) {
				t.Fatalf("该报 ErrMalformedRecord，实际 %v", err)
			}
		})
	}
}

// TestUnmarshalRefusesUnreadableBytes 逐条验读不回来的形状（标签不认识那一种在下一条）。
//
// 两条的后果不同，所以不合并：
//   - 整段不是 JSON：介质本身坏了，或者读到了不是记录的东西；
//   - grant 但**没有** payload 这个字段：一条在场却什么都没保管的记录，
//     拥有方读到它会以为自己授权过。
//
// 显式写成 `"payload": null` 不在此列，它是合法的：DSH 那边 payload 是 unknown，
// 而 unknown 本来就容得下 null（见 [GrantRecord.Payload]）。这条接缝上也没有
// 「null 表示没写过」这种哨兵约定——有的是 storage/domain 的全局槽，不是这里。
func TestUnmarshalRefusesUnreadableBytes(t *testing.T) {
	t.Parallel()

	for name, raw := range map[string]string{
		"整段不是 JSON":  `不是 JSON`,
		"grant 没有载荷": `{"kind":"grant"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := UnmarshalRecord([]byte(raw)); !errors.Is(err, ErrMalformedRecord) {
				t.Fatalf("该报 ErrMalformedRecord，实际 %v", err)
			}
		})
	}
}

// TestUnmarshalRefusesAnUnknownDiscriminant 钉住不认识的标签不会被当成 api-key。
//
// 源: packages/credentials/credentials/src/types.ts:58-59
//
// 这一条是本文件里后果最重的：一条由更新版本的拥有方写下的记录，被旧版本读成
// 一条空的 api-key 记录之后，下一次 [Provider.ModifyRecord] 就会把它真的覆盖掉
// （见 [UnmarshalRecord]）。报错的话至少那条记录还在介质上。
func TestUnmarshalRefusesAnUnknownDiscriminant(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		`{"kind":"oauth-device"}`, // 更新版本写下的
		`{"kind":"apikey"}`,       // 少了连字符，正是标签字面量各写一遍会犯的那种错
		`{}`,                      // 压根没有标签
	} {
		if _, err := UnmarshalRecord([]byte(raw)); !errors.Is(err, ErrUnknownRecordKind) {
			t.Errorf("%s 该报 ErrUnknownRecordKind，实际 %v", raw, err)
		}
	}
}

// TestAMalformedAPIKeyBodyIsRefused 钉住标签认得出、但记录本体读不进去的那一支。
//
// 正常路径上走不到：写出去的那一面是同一个结构体。仍然要有这条路，
// 是因为介质上的字节可能来自手改的文件——把 env 写成一个字符串的话，
// 不报错就等于交出一条 env 被悄悄丢空的记录。
func TestAMalformedAPIKeyBodyIsRefused(t *testing.T) {
	t.Parallel()

	if _, err := UnmarshalRecord([]byte(`{"kind":"api-key","env":"不是一张表"}`)); !errors.Is(err, ErrMalformedRecord) {
		t.Fatalf("该报 ErrMalformedRecord，实际 %v", err)
	}
	if _, err := UnmarshalRecord([]byte(`{"kind":"grant","payload":{},"extra":`)); !errors.Is(err, ErrMalformedRecord) {
		t.Fatalf("残缺的 grant 该报 ErrMalformedRecord，实际 %v", err)
	}
}
