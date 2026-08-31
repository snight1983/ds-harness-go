// 本文件走一遍整条接缝：拿内存提供方当被试，把两套键空间上的九个操作各跑一遍，
// 顺带验它们各自在什么时候发通知。
//
// 源: packages/credentials/credentials/tests/credentials.spec.ts:42-84
//
// 这里最要紧的一条是「存下来的空值等于没有」：解析跳过它、描述报未配置。
// 少了这一条的症状是配置界面说配好了、调用时却报未授权——最难查的那一类，
// 因为两边各自看都是对的。

package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// seamRef 是这几条用例统一使用的引用名。
//
// 源: packages/credentials/credentials/tests/credentials.spec.ts:7
const seamRef = Ref("DEEPSEEK_API_KEY")

// watch 挂上两套键空间的订阅，返回两个「到目前为止收到了什么」的读取函数。
//
// 订阅在**用例自己**手里而不是藏在夹具里：每条用例关心的通知次数不同，
// 而「这次操作发了几条」正是这些用例要钉的东西之一。
func watch(t *testing.T, provider Provider) (refs func() []Ref, keys func() []Key) {
	t.Helper()

	var seenRefs []Ref
	var seenKeys []Key
	t.Cleanup(provider.SubscribeReference(func(ref Ref) { seenRefs = append(seenRefs, ref) }))
	t.Cleanup(provider.SubscribeRecord(func(key Key) { seenKeys = append(seenKeys, key) }))

	return func() []Ref { return seenRefs }, func() []Key { return seenKeys }
}

// TestResolveGivesBackTheValueAndItsSource 钉住解析同时给出值和供出它的那一层。
//
// 源: packages/credentials/credentials/tests/credentials.spec.ts:43-47
//
// 来源层要一起给出来，是因为「这个值从哪来的」决定了改它该去改哪儿——
// 只给值的话，一次改了配置文件却不生效的排查只能靠猜。
func TestResolveGivesBackTheValueAndItsSource(t *testing.T) {
	t.Parallel()

	provider := newMemoryCredentials(quiet(), map[Ref]string{seamRef: "sk-seeded"})

	resolved, present, err := provider.Resolve(t.Context(), seamRef)
	if err != nil || !present {
		t.Fatalf("种下的引用该解析得出来：%v %v", present, err)
	}
	if resolved.Value != "sk-seeded" || resolved.Source != "memory" {
		t.Fatalf("值和来源层该一起给出，实际 %+v", resolved)
	}

	info, err := provider.Describe(t.Context(), seamRef)
	if err != nil {
		t.Fatalf("描述不该失败：%v", err)
	}
	if !info.Configured || info.Source != "memory" || !info.Writable {
		t.Fatalf("描述该说已配置、来自 memory、可写，实际 %+v", info)
	}
}

// TestAnEmptyStoredValueCountsAsAbsentEverywhere 钉住存下来的空值在**两条路上**都等于没有。
//
// 源: packages/credentials/credentials/tests/credentials.spec.ts:49-53
//
// 两条都验才有意义：只有解析跳过而描述照报已配置的话，配置界面会说配好了，
// 而每一次调用都报未授权，两边各自看都是对的。
func TestAnEmptyStoredValueCountsAsAbsentEverywhere(t *testing.T) {
	t.Parallel()

	provider := newMemoryCredentials(quiet(), map[Ref]string{seamRef: ""})

	if _, present, err := provider.Resolve(t.Context(), seamRef); present || err != nil {
		t.Fatalf("空值该解析不出来，实际 %v %v", present, err)
	}
	info, err := provider.Describe(t.Context(), seamRef)
	if err != nil {
		t.Fatalf("描述不该失败：%v", err)
	}
	if info.Configured {
		t.Fatalf("空值该报未配置，实际 %+v", info)
	}
	if info.Source != "" {
		t.Errorf("未配置时不该报出来源层，实际 %q", info.Source)
	}
}

// TestAnUnconfiguredReferenceIsNotAnError 钉住「没配」不是一次失败。
//
// 源: packages/credentials/credentials/src/index.ts:182-190
//
// 一个还没填的可选凭据是完全正常的状态，报成错误会让日志里堆满不是故障的故障
// （见 [Provider.Resolve]）。
func TestAnUnconfiguredReferenceIsNotAnError(t *testing.T) {
	t.Parallel()

	provider := newMemoryCredentials(quiet(), nil)

	resolved, present, err := provider.Resolve(t.Context(), seamRef)
	if err != nil {
		t.Fatalf("没配不该是错误：%v", err)
	}
	if present {
		t.Fatalf("没配该报不在，实际 %+v", resolved)
	}
}

// TestSetAndUnsetEachEmitTheCommittedChange 钉住写和删各发一条通知。
//
// 源: packages/credentials/credentials/tests/credentials.spec.ts:55-65
//
// 通知是**变更已经提交**之后才发的，所以这条用例把每一次通知都夹在
// 一次能观察到结果的读中间——只数通知条数的话，一个「先发通知再落盘」的实现
// 也照样通过。
func TestSetAndUnsetEachEmitTheCommittedChange(t *testing.T) {
	t.Parallel()

	provider := newMemoryCredentials(quiet(), nil)
	refs, _ := watch(t, provider)

	if err := provider.Set(t.Context(), seamRef, "sk-live"); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}
	resolved, present, err := provider.Resolve(t.Context(), seamRef)
	if err != nil || !present || resolved.Value != "sk-live" {
		t.Fatalf("写完该读得到新值，实际 %+v %v %v", resolved, present, err)
	}

	if err := provider.Unset(t.Context(), seamRef); err != nil {
		t.Fatalf("删不该失败：%v", err)
	}
	if _, present, err := provider.Resolve(t.Context(), seamRef); present || err != nil {
		t.Fatalf("删完该读不到，实际 %v %v", present, err)
	}

	if got := refs(); len(got) != 2 || got[0] != seamRef || got[1] != seamRef {
		t.Fatalf("该收到两条通知，各对应一次提交，实际 %v", got)
	}
}

// TestARefusedSetAndAnAbsentUnsetStaySilent 钉住没提交成的操作**不发**通知。
//
// 源: packages/credentials/credentials/tests/credentials.spec.ts:67-75
//
// 这是上一条的反面，也是通知这件事的全部意义：收到通知的一方会据此重新解析。
// 一次什么都没改的操作也发通知的话，那一方分不出「值变了」和「有人试了一下」，
// 于是它要么白读一遍，要么干脆开始忽略通知。
func TestARefusedSetAndAnAbsentUnsetStaySilent(t *testing.T) {
	t.Parallel()

	provider := newMemoryCredentials(quiet(), nil)
	refs, _ := watch(t, provider)

	if err := provider.Set(t.Context(), seamRef, ""); !errors.Is(err, errMemoryEmptyValue) {
		t.Fatalf("空值该被拒，实际 %v", err)
	}
	if err := provider.Unset(t.Context(), seamRef); err != nil {
		t.Fatalf("删一个不存在的该是空操作，实际 %v", err)
	}

	if got := refs(); len(got) != 0 {
		t.Fatalf("一条都不该发，实际 %v", got)
	}
}

// TestModifyRecordIsTheOnlyWritePathAndSeesTheCurrentValue 钉住读—改—写那一趟。
//
// 源: packages/credentials/credentials/src/index.ts:243-257
//
// mutate 拿到的 current 必须是**写入已经独占的那一刻**的记录：一次正确的写
// 依赖当前值，这正是刷新令牌轮换安全的前提（见 [Provider.ModifyRecord]）。
func TestModifyRecordIsTheOnlyWritePathAndSeesTheCurrentValue(t *testing.T) {
	t.Parallel()

	provider := newMemoryCredentials(quiet(), nil)
	_, keys := watch(t, provider)
	key := Key("llm-pi-ai/openai-codex")

	// 第一次：还没有记录，exists 该是 false，current 该是 nil。
	written, wrote, err := provider.ModifyRecord(t.Context(), key,
		func(_ context.Context, current Record, exists bool) (Record, bool, error) {
			if exists || current != nil {
				t.Errorf("第一次改该看到没有记录，实际 %v %#v", exists, current)
			}
			return GrantRecord{Payload: json.RawMessage(`{"refresh":"rt-1"}`)}, true, nil
		})
	if err != nil || !wrote {
		t.Fatalf("第一次写不该失败：%v %v", wrote, err)
	}
	if written.Kind() != KindGrant {
		t.Fatalf("写回来的该是 grant，实际 %q", written.Kind())
	}

	// 第二次：mutate 该看到刚写下去的那一条，并且能拿它算出下一条。
	_, _, err = provider.ModifyRecord(t.Context(), key,
		func(_ context.Context, current Record, exists bool) (Record, bool, error) {
			if !exists {
				t.Fatal("第二次改该看到上一次写下去的记录")
			}
			previous, ok := current.(GrantRecord)
			if !ok || string(previous.Payload) != `{"refresh":"rt-1"}` {
				t.Fatalf("该看到上一条的载荷，实际 %#v", current)
			}
			return GrantRecord{Payload: json.RawMessage(`{"refresh":"rt-2"}`)}, true, nil
		})
	if err != nil {
		t.Fatalf("第二次写不该失败：%v", err)
	}

	if got := keys(); len(got) != 2 || got[0] != key || got[1] != key {
		t.Fatalf("两次写各该发一条通知，实际 %v", got)
	}
}

// TestAMutatorThatDeclinesChangesNothingAndStaysSilent 钉住谢绝那一支。
//
// 源: packages/credentials/credentials/src/index.ts:243-257
//
// 「不改」和「把记录删空」在 DSH 那边都是 undefined，靠出现在参数还是返回值上分辨；
// Go 拆成了 exists 和 write 两个 bool（见 [Mutator]）。这条用例钉的正是那次拆分：
// 谢绝之后记录必须原封不动，且不发通知。
func TestAMutatorThatDeclinesChangesNothingAndStaysSilent(t *testing.T) {
	t.Parallel()

	provider := newMemoryCredentials(quiet(), nil)
	key := Key("llm-pi-ai/openai-codex")
	seed := APIKeyRecord{Key: "sk-seeded"}
	if _, _, err := provider.ModifyRecord(t.Context(), key, replaceWith(seed)); err != nil {
		t.Fatalf("种一条不该失败：%v", err)
	}

	_, keys := watch(t, provider)
	current, exists, err := provider.ModifyRecord(t.Context(), key,
		func(context.Context, Record, bool) (Record, bool, error) {
			// next 在 write 为 false 时被忽略：故意返回一条完全不同的记录，
			// 万一它被写下去了，下面那次读会当场看出来。
			return GrantRecord{Payload: json.RawMessage(`{"不该":"写进去"}`)}, false, nil
		})
	if err != nil {
		t.Fatalf("谢绝不该是失败：%v", err)
	}
	// 谢绝该原样交回当前那一条。
	expectAPIKey(t, current, exists, seed.Key)

	back, present, err := provider.ReadRecord(t.Context(), key)
	if err != nil {
		t.Fatalf("读不该失败：%v", err)
	}
	// 记录该原封不动——尤其不该是 mutate 返回的那条 grant。
	expectAPIKey(t, back, present, seed.Key)
	if got := keys(); len(got) != 0 {
		t.Fatalf("谢绝不该发通知，实际 %v", got)
	}
}

// TestAFailingMutatorLeavesTheRecordAlone 钉住 mutate 报错时什么都没写下去。
//
// 源: packages/credentials/credentials/src/index.ts:243-257
//
// mutate 里那一步常常是一次网络往返（拿刷新令牌换新令牌）。它砸掉之后
// 还把记录改了的话，手上那个仍然有效的旧令牌就没了。
func TestAFailingMutatorLeavesTheRecordAlone(t *testing.T) {
	t.Parallel()

	provider := newMemoryCredentials(quiet(), nil)
	key := Key("llm-pi-ai/openai-codex")
	seed := APIKeyRecord{Key: "sk-seeded"}
	if _, _, err := provider.ModifyRecord(t.Context(), key, replaceWith(seed)); err != nil {
		t.Fatalf("种一条不该失败：%v", err)
	}

	_, keys := watch(t, provider)
	refused := errors.New("换令牌那一步砸了")
	if _, _, err := provider.ModifyRecord(t.Context(), key,
		func(context.Context, Record, bool) (Record, bool, error) {
			return nil, true, refused
		}); !errors.Is(err, refused) {
		t.Fatalf("该把 mutate 的失败带出来：%v", err)
	}

	back, present, err := provider.ReadRecord(t.Context(), key)
	if err != nil {
		t.Fatalf("读不该失败：%v", err)
	}
	// 那个仍然有效的旧值必须还在。
	expectAPIKey(t, back, present, seed.Key)
	if got := keys(); len(got) != 0 {
		t.Fatalf("没写成不该发通知，实际 %v", got)
	}
}

// TestDescribeRecordCountsPresenceNotContent 钉住记录那一半按**在场**算已配置。
//
// 源: packages/credentials/credentials/src/index.ts:132-145
//
// 和引用那一半正好相反（那边空值等于没有）。一条既没有密钥也没有环境值的
// api-key 记录陈述的是「拥有方确认这条路由用环境发现认证」，那是已配置，
// 不是空白（见 [RecordInfo.Configured]）。两边口径不同这件事值得单独钉一条，
// 因为「统一一下」看上去总是像个改进。
func TestDescribeRecordCountsPresenceNotContent(t *testing.T) {
	t.Parallel()

	provider := newMemoryCredentials(quiet(), nil)
	key := Key("llm-pi-ai/openai-codex")

	info, err := provider.DescribeRecord(t.Context(), key)
	if err != nil {
		t.Fatalf("描述不该失败：%v", err)
	}
	if info.Configured || info.Kind != "" {
		t.Fatalf("没有记录该报未配置且不带标签，实际 %+v", info)
	}

	// 存一条两个字段都空的 api-key 记录。
	if _, _, err := provider.ModifyRecord(t.Context(), key, replaceWith(APIKeyRecord{})); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}
	info, err = provider.DescribeRecord(t.Context(), key)
	if err != nil {
		t.Fatalf("描述不该失败：%v", err)
	}
	if !info.Configured {
		t.Fatal("两个字段都空的记录仍然是**已配置**，见 RecordInfo.Configured")
	}
	if info.Kind != KindAPIKey {
		t.Errorf("该带上标签，实际 %q", info.Kind)
	}
}

// TestListRecordsGivesAddressesAndKindsOnly 钉住枚举出来的东西里没有值。
//
// 源: packages/credentials/credentials/src/index.ts:233-241
//
// 引用那一半没有枚举，因为设置的 schema 里就写着有哪些引用；记录这一半没有那条
// 发现路径——列不出来的界面既没法告诉用户他授权过什么，也找不出孤儿
// （见 [Provider.ListRecords]）。[RecordEntry] 只有地址和标签两个字段，
// 「不含值」这件事由类型本身保证，这里钉的是**列全了**。
func TestListRecordsGivesAddressesAndKindsOnly(t *testing.T) {
	t.Parallel()

	provider := newMemoryCredentials(quiet(), nil)
	first := Key("llm-pi-ai/openai-codex")
	second := Key("a-plugin/some-route")

	if entries, err := provider.ListRecords(t.Context()); err != nil || len(entries) != 0 {
		t.Fatalf("一条都没有时该是空的，实际 %v %v", entries, err)
	}

	if _, _, err := provider.ModifyRecord(t.Context(), first,
		replaceWith(GrantRecord{Payload: json.RawMessage(`{}`)})); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}
	if _, _, err := provider.ModifyRecord(t.Context(), second,
		replaceWith(APIKeyRecord{Key: "sk-live"})); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}

	entries, err := provider.ListRecords(t.Context())
	if err != nil {
		t.Fatalf("枚举不该失败：%v", err)
	}
	// 夹具按地址排序输出，见 memoryCredentials.ListRecords。
	want := []RecordEntry{{Key: second, Kind: KindAPIKey}, {Key: first, Kind: KindGrant}}
	if len(entries) != len(want) {
		t.Fatalf("该列出两条，实际 %v", entries)
	}
	for index, entry := range want {
		if entries[index] != entry {
			t.Fatalf("第 %d 条该是 %+v，实际 %+v", index, entry, entries[index])
		}
	}
}

// TestDeleteRecordRemovesOnceAndIsSilentTheSecondTime 钉住删除，以及删空气不发通知。
//
// 源: packages/credentials/credentials/src/index.ts:259-263
//
// 理由和 [TestARefusedSetAndAnAbsentUnsetStaySilent] 同一条：没提交成就不发通知。
func TestDeleteRecordRemovesOnceAndIsSilentTheSecondTime(t *testing.T) {
	t.Parallel()

	provider := newMemoryCredentials(quiet(), nil)
	key := Key("llm-pi-ai/openai-codex")
	if _, _, err := provider.ModifyRecord(t.Context(), key,
		replaceWith(APIKeyRecord{Key: "sk-live"})); err != nil {
		t.Fatalf("写不该失败：%v", err)
	}

	_, keys := watch(t, provider)
	if err := provider.DeleteRecord(t.Context(), key); err != nil {
		t.Fatalf("删不该失败：%v", err)
	}
	if _, present, err := provider.ReadRecord(t.Context(), key); present || err != nil {
		t.Fatalf("删完该读不到，实际 %v %v", present, err)
	}
	if err := provider.DeleteRecord(t.Context(), key); err != nil {
		t.Fatalf("删一个不存在的该是空操作，实际 %v", err)
	}

	if got := keys(); len(got) != 1 || got[0] != key {
		t.Fatalf("只该为真删掉的那一次发通知，实际 %v", got)
	}
}

// TestTheTwoKeySpacesShareOneProviderWithoutCollidingOnTheWire 钉住两半共用一个提供方也不串。
//
// 源: packages/credentials/credentials/src/types.ts:20-28
//
// 语法不相交这件事在 credentials_test.go 里已经钉了；这里钉的是它的**后果**：
// 同一个提供方上，两半各存各的，一边的写不会碰到另一边的读。
func TestTheTwoKeySpacesShareOneProviderWithoutCollidingOnTheWire(t *testing.T) {
	t.Parallel()

	provider := newMemoryCredentials(quiet(), nil)
	if err := provider.Set(t.Context(), seamRef, "sk-live"); err != nil {
		t.Fatalf("写引用不该失败：%v", err)
	}
	if _, _, err := provider.ModifyRecord(t.Context(), Key("llm-pi-ai/openai-codex"),
		replaceWith(APIKeyRecord{Key: "别的值"})); err != nil {
		t.Fatalf("写记录不该失败：%v", err)
	}

	resolved, present, err := provider.Resolve(t.Context(), seamRef)
	if err != nil || !present || resolved.Value != "sk-live" {
		t.Fatalf("引用那一半该没被动过，实际 %+v %v %v", resolved, present, err)
	}
	entries, err := provider.ListRecords(t.Context())
	if err != nil || len(entries) != 1 {
		t.Fatalf("记录那一半该只有一条，实际 %v %v", entries, err)
	}
}

// replaceWith 造一个「不看当前值，直接换成这一条」的 [Mutator]。
//
// 只给那些不关心读—改—写次序的用例用；关心的那两条各自写了自己的 mutate。
func replaceWith(record Record) Mutator {
	return func(context.Context, Record, bool) (Record, bool, error) {
		return record, true, nil
	}
}

// expectAPIKey 要求这条记录是一条 api-key 记录，且密钥是 key。
//
// 两条记录类型都带着引用类型的字段（一张 map、一段字节），用 == 比会 panic
// 而不是给出 false——所以比较这件事必须由用例这边显式写出来。
func expectAPIKey(t *testing.T, record Record, present bool, key string) {
	t.Helper()

	if !present {
		t.Fatalf("该有一条记录在，密钥是 %q", key)
	}
	typed, ok := record.(APIKeyRecord)
	if !ok {
		t.Fatalf("该是一条 api-key 记录，实际 %#v", record)
	}
	if typed.Key != key {
		t.Fatalf("密钥该是 %q，实际 %q", key, typed.Key)
	}
}
