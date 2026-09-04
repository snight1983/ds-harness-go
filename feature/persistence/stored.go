// 本文件的作用：一份刚从后端读回来的存档要过的那几道判据，以及把它补平的动作。
//
// 源: packages/session/session-persistence/src/coordinator.ts:262-272
// 源: packages/session/session-persistence/src/coordinator.ts:874-900
// 源: packages/session/session-persistence/src/coordinator.ts:1044-1082

package persistence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/snight1983/ds-harness-go/sessionlog"
)

// CheckStoredIdentity 验一份读回来的头上写的会话身份是不是请求的那个。
//
// 源: packages/session/session-persistence/src/coordinator.ts:1078-1082
//
// 这道判据必须排在**发布任何状态之前**：后端要是把 a 的存档当成 b 的交出来，
// 而这里没拦住，b 就会顶着 a 的历史活过来。
func CheckStoredIdentity(id sessionlog.SessionID, meta sessionlog.SessionHeader) error {
	if meta.ID == id {
		return nil
	}
	return fmt.Errorf(
		"%w：要的是 %q，头里写的是 %q",
		ErrIdentityMismatch, string(id), string(meta.ID),
	)
}

// CheckStoredVersion 验一份读回来的头上的格式版本本构建读不读。
//
// 源: packages/session/session-persistence/src/coordinator.ts:1044-1049
//
// 拒绝理由分方向，见 [FormatVersionRefusal]。返回的错误是
// *[FormatUnsupportedError]，还没带存档位置——拿得到位置的那一层用
// [FormatUnsupportedError.WithLocation] 补上去。
func CheckStoredVersion(meta sessionlog.SessionHeader) error {
	if meta.Version == sessionlog.FormatVersion {
		return nil
	}
	return &FormatUnsupportedError{
		ID:     meta.ID,
		Reason: FormatVersionRefusal(meta.ID, meta.Version),
	}
}

// CheckStoredVocabulary 拒绝一份里面有本构建不认识、又没标可跳过的事件的日志。
//
// 源: packages/session/session-persistence/src/coordinator.ts:1051-1065
//
// 判据本身在 [sessionlog.CheckVocabulary]，这里做的是把它那条哨兵错误翻成
// 持久化层的说法：一条不认识的**必需**事件可能改变后面整段日志的解释方式，
// 静默跳过就等于重建出一个错的会话，所以拒的是**整份日志**，不是那一条事件。
//
// 别的错误（比如日志本身就读不回来）原样上抛：那不是「本构建读不了」，
// 那是坏了，两者给使用者的下一步动作不一样。
func CheckStoredVocabulary(
	meta sessionlog.SessionHeader,
	events []sessionlog.Event,
	vocabulary sessionlog.Vocabulary,
) error {
	err := sessionlog.CheckVocabulary(events, vocabulary)
	if err == nil {
		return nil
	}
	return &FormatUnsupportedError{
		ID: meta.ID,
		Reason: fmt.Sprintf(
			"会话 %q 的日志里有本运行时不认识、又没标可跳过的事件（%v），拒绝解释这份日志：它多半是更新的运行时写的",
			string(meta.ID), err,
		),
	}
}

// CheckStored 按次序跑完一份读回来的存档要过的三道判据：身份、格式版本、词汇。
//
// 源: packages/session/session-persistence/src/coordinator.ts:884-889
//
// 次序是有讲究的，不能换：身份不对就说明拿错了东西，那时候版本号是谁的都不
// 重要；版本不对就说明后面的结构本构建根本没法按今天的形状去解，
// 那时候「词汇里有个没见过的类型」是一句会误导人的话。
//
// backend 只用来给格式拒绝找那份原始日志的位置，可以是 nil。
func CheckStored(
	backend Backend,
	id sessionlog.SessionID,
	meta sessionlog.SessionHeader,
	events []sessionlog.Event,
	vocabulary sessionlog.Vocabulary,
) error {
	if err := CheckStoredIdentity(id, meta); err != nil {
		return err
	}
	if err := CheckStoredVersion(meta); err != nil {
		return locateRefusal(backend, meta, err)
	}
	if err := CheckStoredVocabulary(meta, events, vocabulary); err != nil {
		return locateRefusal(backend, meta, err)
	}
	return nil
}

// locateRefusal 给一条格式拒绝补上原始存档的位置——后端认路的话。
func locateRefusal(backend Backend, meta sessionlog.SessionHeader, err error) error {
	var refusal *FormatUnsupportedError
	if backend == nil || !errors.As(err, &refusal) {
		return err
	}
	location, ok := LocateWith(backend, meta)
	if !ok {
		return refusal
	}
	return refusal.WithLocation(location)
}

// BalanceStored 把一份读回来的日志补平：留下完整的中断事件，只补缺的那几条收尾。
//
// 源: packages/session/session-persistence/src/coordinator.ts:900-902
//
// 返回补齐后的整段日志和补出来的那几条。closers 为空表示这份日志本来就是平的。
// 一份平衡的日志走这里之后 balanced 和 events 内容相同。
//
// 补出来的那几条是**确定的**（见 [sessionlog.InterruptedTurnClosers]），
// 所以这个函数跑两遍得到同样的字节——崩溃修复因此可以重跑。
func BalanceStored(events []sessionlog.Event) (balanced, closers []sessionlog.Event, err error) {
	closers, err = sessionlog.InterruptedTurnClosers(events)
	if err != nil {
		return nil, nil, err
	}
	if len(closers) == 0 {
		return events, nil, nil
	}
	balanced = make([]sessionlog.Event, 0, len(events)+len(closers))
	balanced = append(balanced, events...)
	balanced = append(balanced, closers...)
	return balanced, closers, nil
}

// SeedCoversPrefix 判断一个活会话的 seed 是不是逐条复现了一份已落盘的前缀。
//
// 源: packages/session/session-persistence/src/coordinator.ts:264-272
//
// 这是认领活会话时那道判据：一个从存储恢复出来的会话，它的 seed 应该就是
// 存档里那段前缀本身。盖不住就说明这个活会话和那份存档不是一回事，
// 不能把后续的事件往那份存档上接。
//
// 新增: DSH 逐条比 JSON.stringify。这里比的是 [sessionlog.Event] 排出来的字节，
// 理由和 session 包测试里 assertSameEvents 写的一样：Data 是
// [json.RawMessage]，两条内容相同的事件底层不一定是同一段切片，
// 而排出来的字节相同就是相同。排不出去说明有一条事件本身就是坏的，报错。
func SeedCoversPrefix(seed, prefix []sessionlog.Event) (bool, error) {
	if len(prefix) > len(seed) {
		return false, nil
	}
	for index, event := range prefix {
		same, err := sameEventBytes(seed[index], event)
		if err != nil {
			return false, err
		}
		if !same {
			return false, nil
		}
	}
	return true, nil
}

// sameEventBytes 按排出来的字节比两条事件。
func sameEventBytes(a, b sessionlog.Event) (bool, error) {
	left, err := json.Marshal(a)
	if err != nil {
		return false, fmt.Errorf("seed 里第 %d 条事件排不出去：%w", a.Seq, err)
	}
	right, err := json.Marshal(b)
	if err != nil {
		return false, fmt.Errorf("存档里 seq %d 那条事件排不出去：%w", b.Seq, err)
	}
	return bytes.Equal(left, right), nil
}
