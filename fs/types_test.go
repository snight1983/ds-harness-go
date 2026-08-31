// 本文件验这条接缝的词汇里那几处**有行为**的地方：观察的两种、以及两个封印。
//
// 源: packages/fs/fs/tests/service.spec.ts:163-168
//
// 剩下的类型是纯数据，没有可验的行为——给一个结构体赋值再读出来，验的是
// Go 的赋值语义，不是这个包的契约。那种用例只会让「测试都过了」这件事更廉价。

package fs

import "testing"

// TestNamedTypesAreTheBrandFactories 钉住 DSH 那两个工厂函数在 Go 里就是类型转换。
//
// 源: packages/fs/fs/tests/service.spec.ts:163-168
//
// DSH 那条用例验的是 `FsTargetKey('k') === 'k'`——工厂函数在运行时是恒等的，
// 它存在的全部理由是 TS 没有具名字符串类型（types.ts:23 自己写明不做任何校验）。
// Go 有具名类型，所以那个函数就是转换本身，一行不用写。
//
// 代价也一并钉住：转换是免费的，挡不住任何一个裸串。所以后端拿到一个
// 不是自己发出去的 key 时该报 [CodeNotFound]，不是假定它一定合法。
func TestNamedTypesAreTheBrandFactories(t *testing.T) {
	t.Parallel()

	if string(TargetKey("k")) != "k" {
		t.Error("具名类型转换该是恒等的")
	}
	if string(Version("v")) != "v" {
		t.Error("具名类型转换该是恒等的")
	}
	// 编译得过就是那个代价本身：这里没有任何校验拦得住它。
	_ = TargetKey("随便什么")
}

// TestAnObservationIsEitherPresentWithAVersionOrAbsentWithNone 钉住这个和类型的两支。
//
// 源: packages/fs/fs/src/types.ts:47-54
//
// 不在场的观察**不带版本**，这一点是结构上保证的而不是约定的：
// [Absent] 根本没有那个字段。用「一个 bool 加一个 Version」的写法的话，
// 「不在场却带着版本」是可以写出来的，于是每个读它的人都得再判一次
// 这个版本这次算不算数。
func TestAnObservationIsEitherPresentWithAVersionOrAbsentWithNone(t *testing.T) {
	t.Parallel()

	var present Observation = Present{Version: Version("v1")}
	version, ok := present.PresentVersion()
	if !ok {
		t.Fatal("在场的观察该报 true")
	}
	if version != Version("v1") {
		t.Errorf("该带出 v1，实际 %q", version)
	}

	var absent Observation = Absent{}
	version, ok = absent.PresentVersion()
	if ok {
		t.Error("不在场的观察该报 false")
	}
	if version != "" {
		t.Errorf("不在场不该带出任何版本，实际 %q", version)
	}
}

// TestTheSealedSetsAreClosedAtCompileTime 把两个封印方法的意图写下来。
//
// 源: packages/fs/fs/src/types.ts:47-54,117-125
//
// 这两个方法没有行为，调它们什么也不会发生。这条用例存在的理由是别的：
// 它们是**编译期的锁**——本包外面写不出 sealedObservation / sealedWriteIntent，
// 于是 [Observation] 只可能是 [Present] 和 [Absent]，[WriteIntent] 只可能是
// [CreateIfAbsent] 和 [ReplaceIfVersion]。这正是 DSH 那两个判别联合的等价物。
//
// 把它们摆在这里，也是为了下一个读到「这两个方法从来没人调」的人不去删掉它们。
func TestTheSealedSetsAreClosedAtCompileTime(t *testing.T) {
	t.Parallel()

	Present{}.sealedObservation()
	Absent{}.sealedObservation()
	CreateIfAbsent{}.sealedWriteIntent()
	ReplaceIfVersion{}.sealedWriteIntent()
}
