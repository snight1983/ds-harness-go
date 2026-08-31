// 本文件的作用：声明本身的用例——什么样的声明立得住，什么样的当场就该被拒。
//
// 这些检查跑在**碰介质之前**，所以它们挡下的每一样东西都不会在介质上留痕。
// 其中最要紧的一条是「全局初值不能编码成 JSON null」：null 是介质上
// 「从来没写过」的哨兵，一个能编成 null 的全局值会在重启时安静地退回初值，
// 全程没有任何一步报错——这是这个包里唯一一处**无声**的数据丢失，
// 所以它在声明期和写入期各挡一次，用例也各钉一条。

package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestValidateRejectsBadDomainNames 钉住域名规则。域名同时也是介质上那个单元的名字。
//
// 源: packages/storage/storage-domain/src/spec.ts:67-98
func TestValidateRejectsBadDomainNames(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "Notes", "1notes", "notes-1", "notes.a", "_notes"} {
		spec := Spec{Name: name, Tables: []TableSpec{DefineTable[note]("entries", nil)}}
		if err := spec.Validate(); err == nil {
			t.Fatalf("域名 %q 该被拒", name)
		}
	}
	for _, name := range []string{"notes", "n", "notes_1", "a0"} {
		spec := Spec{Name: name, Tables: []TableSpec{DefineTable[note]("entries", nil)}}
		if err := spec.Validate(); err != nil {
			t.Fatalf("域名 %q 该被接受：%v", name, err)
		}
	}
}

// TestValidateRejectsANegativeVersion 钉住版本号不能是负数。
//
// 版本号会盖到介质上，一个负数在那里没有任何说得通的含义。
func TestValidateRejectsANegativeVersion(t *testing.T) {
	t.Parallel()

	spec := Spec{Name: "notes", Version: -1}
	if err := spec.Validate(); err == nil {
		t.Fatal("负版本号该被拒")
	}
	// 0 是合法的：没标过版本的域就是第 0 版。
	if err := (Spec{Name: "notes"}).Validate(); err != nil {
		t.Fatalf("0 版该被接受：%v", err)
	}
}

// TestValidateRejectsBadAndDuplicateTableNames 钉住表名规则和重名。
//
// 源: packages/storage/storage-domain/src/spec.ts:67-98
//
// 重名尤其要挡：两张重名的表在快照里会塌成一张，而声明它的人以为有两张。
func TestValidateRejectsBadAndDuplicateTableNames(t *testing.T) {
	t.Parallel()

	bad := Spec{Name: "notes", Tables: []TableSpec{DefineTable[note]("Entries", nil)}}
	if err := bad.Validate(); err == nil {
		t.Fatal("不合法的表名该被拒")
	}

	duplicate := Spec{Name: "notes", Tables: []TableSpec{
		DefineTable[note]("entries", nil),
		DefineTable[note]("entries", nil),
	}}
	err := duplicate.Validate()
	if err == nil {
		t.Fatal("重名的表该被拒")
	}
	if !strings.Contains(err.Error(), "重复") {
		t.Fatalf("该说清是重名：%v", err)
	}
}

// TestValidateRejectsHandWrittenZeroValueSpecs 钉住绕过 Define* 直接写结构体字面量。
//
// 源: packages/storage/storage-domain/src/spec.ts:67-98
//
// 零值的 decode 是 nil，不挡的话它会在加载记录时空指针崩溃——那时候报的位置
// 是加载路径，而错误其实出在声明那一行。
func TestValidateRejectsHandWrittenZeroValueSpecs(t *testing.T) {
	t.Parallel()

	table := Spec{Name: "notes", Tables: []TableSpec{{name: "entries"}}}
	if err := table.Validate(); err == nil {
		t.Fatal("手写的零值 TableSpec 该被拒")
	}

	global := Spec{Name: "notes", Global: &GlobalSpec{}}
	if err := global.Validate(); err == nil {
		t.Fatal("手写的零值 GlobalSpec 该被拒")
	}
}

// TestAGlobalInitialThatEncodesToNullIsRefusedAtDeclaration 钉住哨兵冲突在声明期就响。
//
// 源: packages/storage/storage-domain/src/spec.ts:91-96
//
// 这是本包里唯一一处会**无声**丢数据的形状：存下去、重启、退回初值，一步不报错。
// 所以它必须在声明期就被挡住，而不是等到某次读回来发现值不对。
func TestAGlobalInitialThatEncodesToNullIsRefusedAtDeclaration(t *testing.T) {
	t.Parallel()

	spec := Spec{Name: "notes", Global: DefineGlobal[*preference](nil, nil)}
	err := spec.Validate()
	if err == nil {
		t.Fatal("能编码成 null 的初值该被拒")
	}
	if !strings.Contains(err.Error(), "null") {
		t.Fatalf("该说清是 null 哨兵冲突：%v", err)
	}
}

// TestAGlobalInitialThatFailsItsOwnValidationIsRefused 钉住初值也要过校验。
//
// 源: packages/storage/storage-domain/src/spec.ts:91-96
//
// 初值是**会被读到**的值：介质上还没写过时供出去的就是它。
func TestAGlobalInitialThatFailsItsOwnValidationIsRefused(t *testing.T) {
	t.Parallel()

	spec := Spec{Name: "notes", Global: DefineGlobal(preference{}, func(p preference) error {
		if p.Theme == "" {
			return errRejected
		}
		return nil
	})}
	err := spec.Validate()
	if err == nil {
		t.Fatal("过不了自己校验的初值该被拒")
	}
	if !errors.Is(err, errRejected) {
		t.Fatalf("底层原因该是校验函数给的那句：%v", err)
	}
}

// TestDescriptorProjectsTheSpecOntoTheBackend 钉住投影成后端描述符的那一步。
//
// 源: packages/storage/storage-domain/src/spec.ts:100-112
//
// 表名顺序跟着声明走（Tables 是切片不是 map，见 [Spec.Tables]）。
func TestDescriptorProjectsTheSpecOntoTheBackend(t *testing.T) {
	t.Parallel()

	descriptor := notesSpec().Descriptor()
	if descriptor.Name != "notes" || descriptor.Version != 1 {
		t.Fatalf("身份该原样带过去，实际 %+v", descriptor)
	}
	if !descriptor.HasGlobal {
		t.Fatal("声明了全局槽就该在描述符里标出来")
	}
	if len(descriptor.Tables) != 2 || descriptor.Tables[0] != "entries" || descriptor.Tables[1] != "drafts" {
		t.Fatalf("表名顺序该跟着声明走，实际 %v", descriptor.Tables)
	}

	// 没声明全局槽的域，描述符里也不该有。
	bare := Spec{Name: "notes", Tables: []TableSpec{DefineTable[note]("entries", nil)}}
	if bare.Descriptor().HasGlobal {
		t.Fatal("没声明全局槽就不该标 HasGlobal")
	}
}

// TestTableSpecReportsItsOwnName 钉住表名读得出来——错误信息和描述符都要它。
func TestTableSpecReportsItsOwnName(t *testing.T) {
	t.Parallel()

	if got := DefineTable[note]("entries", nil).Name(); got != "entries" {
		t.Fatalf("表名该是 entries，实际 %q", got)
	}
}

// TestEncodingRefusesAValueOfTheWrongType 钉住编码那一侧的类型断言不会 panic。
//
// 源: packages/storage/storage-domain/src/spec.ts:58-65
//
// 正常路径上走不到这里：[TableOf] / [GlobalOf] 已经核对过类型。仍然要有这条路，
// 是因为一个失败的断言会 panic 在写路径的中间，而那时候后端可能已经写下去了。
func TestEncodingRefusesAValueOfTheWrongType(t *testing.T) {
	t.Parallel()

	table := DefineTable[note]("entries", nil)
	if _, err := table.encode("我不是 note"); err == nil {
		t.Fatal("类型不对该报错而不是 panic")
	}

	global := DefineGlobal(preference{Theme: "light"}, nil)
	if _, err := global.encode(42); err == nil {
		t.Fatal("类型不对该报错而不是 panic")
	}
}

// TestDecodingRunsTheDeclaredValidation 钉住解码那一侧两类失败都拦得住。
func TestDecodingRunsTheDeclaredValidation(t *testing.T) {
	t.Parallel()

	table := DefineTable("entries", func(n note) error {
		if n.Title == "" {
			return errRejected
		}
		return nil
	})

	if _, err := table.decode(json.RawMessage(`{"title":`)); err == nil {
		t.Fatal("残缺的 JSON 该被拒")
	}
	if _, err := table.decode(json.RawMessage(`{"title":""}`)); !errors.Is(err, errRejected) {
		t.Fatalf("该是校验拦下的：%v", err)
	}
	value, err := table.decode(json.RawMessage(`{"title":"好的","count":3}`))
	if err != nil {
		t.Fatalf("合法记录不该被拒：%v", err)
	}
	if typed, ok := value.(note); !ok || typed.Count != 3 {
		t.Fatalf("解出来的该是 note，实际 %#v", value)
	}
}

// TestAGlobalInitialThatCannotBeMarshalledIsRefused 钉住编码本身砸掉也在声明期就响。
//
// 源: packages/storage/storage-domain/src/spec.ts:91-96
//
// 和「编出来是 null」是两件事：这里是 encoding/json 根本编不动这个类型
// （函数、通道、循环引用都属于这类）。放过去的话，第一次写才会失败，
// 而那时候声明已经在好几处被引用了。
func TestAGlobalInitialThatCannotBeMarshalledIsRefused(t *testing.T) {
	t.Parallel()

	spec := Spec{Name: "notes", Global: DefineGlobal(func() {}, nil)}
	err := spec.Validate()
	if err == nil {
		t.Fatal("编不动的初值该被拒")
	}
	if !strings.Contains(err.Error(), "初值不合法") {
		t.Fatalf("该说清是初值的问题：%v", err)
	}
}

// TestTheGlobalDecoderRunsBothChecks 钉住全局槽解码那两类失败。
//
// 这两条在读路径上是唯一挡住「介质上存着一个不合法全局值」的地方。
func TestTheGlobalDecoderRunsBothChecks(t *testing.T) {
	t.Parallel()

	spec := DefineGlobal(preference{Theme: "light"}, func(p preference) error {
		if p.Theme == "" {
			return errRejected
		}
		return nil
	})

	if _, err := spec.decode(json.RawMessage(`{"theme":`)); err == nil {
		t.Fatal("残缺的 JSON 该被拒")
	}
	if _, err := spec.decode(json.RawMessage(`{"theme":""}`)); !errors.Is(err, errRejected) {
		t.Fatalf("该是校验拦下的：%v", err)
	}
	value, err := spec.decode(json.RawMessage(`{"theme":"dark"}`))
	if err != nil {
		t.Fatalf("合法值不该被拒：%v", err)
	}
	if typed, ok := value.(preference); !ok || typed.Theme != "dark" {
		t.Fatalf("解出来的该是 preference，实际 %#v", value)
	}
}

// TestIsJSONNullTreatsEmptyAndLiteralNullAlike 钉住哨兵的判定口径。
//
// 后端可能给出 nil 切片，也可能给出四个字节的 "null"——两者在介质上是同一件事。
func TestIsJSONNullTreatsEmptyAndLiteralNullAlike(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"", "null", " null ", "\nnull\n"} {
		if !isJSONNull(json.RawMessage(raw)) {
			t.Fatalf("%q 该被当成哨兵", raw)
		}
	}
	for _, raw := range []string{"0", `""`, "{}", `{"theme":"light"}`, "[null]"} {
		if isJSONNull(json.RawMessage(raw)) {
			t.Fatalf("%q 不该被当成哨兵", raw)
		}
	}
	if !isJSONNull(nil) {
		t.Fatal("nil 该被当成哨兵")
	}
}
