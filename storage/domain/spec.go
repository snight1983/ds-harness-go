// 本文件的作用：域的**声明**——身份、版本、有没有全局槽、有哪几张表、每张表的记录是什么类型。
//
// 源: packages/storage/storage-domain/src/spec.ts:1-9
//
// 声明是一个域唯一的事实来源：类型那一面和运行期那一面（校验、投影成后端描述符）
// 都从它推出来。声明本身出错要在**碰介质之前**就响——一个名字不合法的域，
// 在打开的那一刻才失败的话，介质上可能已经留下了半个单元。
//
// # 和 DSH 的根本差异：没有 zod
//
// DSH 用 zod schema 描述记录：一个运行期对象同时干「校验」和「推导 TypeScript 类型」
// 两件事。Go 里这两件事是分开的，而且各自都有现成办法——类型就是类型，
// 校验是 encoding/json 解码加上登记方自己的一个 Validate 函数。
// 这和本仓库 settings 包的取舍是同一条，理由也一样：搬一套运行期 schema 过来，
// 等于在 Go 的类型系统旁边再立一个说法不完全一致的类型系统。

package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/snight1983/ds-harness-go/storage"
)

// GlobalSpec 是全局单例槽的声明：记录类型、第一次写之前供出去的那个初值、校验。
//
// 源: packages/storage/storage-domain/src/spec.ts:14-20
//
// 零值不可用，请用 [DefineGlobal]。
type GlobalSpec struct {
	// initial 是介质上还没有全局值时供出去的那个值，类型是登记方的 G。
	initial any
	// initialRaw 是 initial 的 JSON 投影，声明时就算好——读路径上不必再编一次。
	initialRaw json.RawMessage
	// decode 把介质上的原始 JSON 解成 G 并跑登记方的校验。
	decode func(json.RawMessage) (any, error)
	// encode 反过来：收一个 G，跑校验，编成 JSON。
	encode func(any) (json.RawMessage, error)
	// valueType 是 G 的反射类型，[GlobalOf] 拿它核对调用方要的类型对不对。
	valueType reflect.Type
	// defineErr 是声明期发现的问题，留到 [Spec.Validate] 一起报。
	defineErr error
}

// TableSpec 是一张表的声明：表名加记录类型。
//
// 源: packages/storage/storage-domain/src/spec.ts:22-32
//
// 零值不可用，请用 [DefineTable]。
//
// 新增: DSH 的 DomainTableSpec 带一个幽灵字段 __key，用来在编译期给键也定一个
// 品牌类型；表名则是外面那个 Record 的键。Go 里既没有幽灵类型也没有品牌类型，
// 表名就直接落在声明里，键一律是 string——它在介质上本来就是 string。
// 新增: 这里**没有** GlobalSpec 那个 defineErr 字段。声明一张表这件事本身不会失败——
// 它只是记下名字、类型和两个闭包。全局槽有 defineErr 是因为它多做一件会失败的事：
// 声明期就要把初值编成 JSON（好在读路径上不必再编），而那次编码可能砸。
type TableSpec struct {
	name      string
	valueType reflect.Type
	decode    func(json.RawMessage) (any, error)
	encode    func(any) (json.RawMessage, error)
}

// Name 是这张表的表名。
func (t TableSpec) Name() string { return t.name }

// DefineTable 声明一张表，V 是它每条记录的类型。
//
// 源: packages/storage/storage-domain/src/spec.ts:66-73（domainTable）
//
// validate 可以为 nil。它跑在两处：从介质读回来的每一条记录（这是 DSH 唯一跑校验的地方），
// 以及每一次写入。
//
// 新增: **写的时候也校验**。DSH 只在「读回来」这个持久化边界上校验，写是不查的。
// 那样一条过不了校验的记录能安静地写下去，直到下一次进程重启、整个域因为它打不开——
// 而那时候现场早没了，报的位置也不是写它的那一行。Go 这边多跑一次校验的代价是常数级的，
// 换掉的是一类只在重启时才现形的故障。
//
// 新增: 这是包级泛型函数而不是某个类型的方法，因为 Go 的方法不能带自己的类型参数。
// 同样的写法见本仓库 settings.Register 和 storage.FormAs。
func DefineTable[V any](name string, validate func(V) error) TableSpec {
	spec := TableSpec{name: name, valueType: reflect.TypeFor[V]()}
	spec.decode = func(raw json.RawMessage) (any, error) {
		var value V
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		if validate != nil {
			if err := validate(value); err != nil {
				return nil, err
			}
		}
		return value, nil
	}
	spec.encode = func(value any) (json.RawMessage, error) {
		// 断言必然成立：能走到这里的值只可能来自 Table[V]，而 [TableOf] 已经
		// 核对过 V 就是声明时的那个类型。仍然接住，是因为一个失败的断言
		// 会 panic 在写路径的中间，而那时候后端可能已经写下去了。
		typed, ok := value.(V)
		if !ok {
			return nil, fmt.Errorf("表 %q 的记录类型是 %s，这次给的是 %T", name, spec.valueType, value)
		}
		if validate != nil {
			if err := validate(typed); err != nil {
				return nil, err
			}
		}
		return json.Marshal(typed)
	}
	return spec
}

// DefineGlobal 声明全局单例槽，G 是它的类型，initial 是第一次写之前供出去的值。
//
// 源: packages/storage/storage-domain/src/spec.ts:14-20,91-96
//
// # 为什么这里要挡住 JSON null
//
// 后端把全局槽当成一段不透明 JSON 存，并且拿 **null 当「从来没写过」的哨兵**
// （见 storage.Snapshot.Global）。所以一个真的能编码成 null 的全局值存不住：
// 重新打开时它会被当成「没写过」，安静地退回 initial，而中间没有任何一步报错。
//
// DSH 的做法是拿 `schema.safeParse(null)` 问 schema「你收不收 null」。
// Go 没有运行期 schema 可问，改成**直接看值**：initial 编出来是 null 就当场拒绝，
// 之后每一次 [Global.Set] 也照查一遍。这比问 schema 更准——它挡的是真正会被存下去的
// 那个字节序列，而不是一个类型上的可能性。
//
// validate 可以为 nil；它对 initial 也生效，因为 initial 是会被读到的值。
func DefineGlobal[G any](initial G, validate func(G) error) *GlobalSpec {
	spec := &GlobalSpec{initial: initial, valueType: reflect.TypeFor[G]()}
	spec.decode = func(raw json.RawMessage) (any, error) {
		var value G
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		if validate != nil {
			if err := validate(value); err != nil {
				return nil, err
			}
		}
		return value, nil
	}
	spec.encode = func(value any) (json.RawMessage, error) {
		// 断言不成立的理由同 [DefineTable] 里那一处。
		typed, ok := value.(G)
		if !ok {
			return nil, fmt.Errorf("全局槽的类型是 %s，这次给的是 %T", spec.valueType, value)
		}
		if validate != nil {
			if err := validate(typed); err != nil {
				return nil, err
			}
		}
		raw, err := json.Marshal(typed)
		if err != nil {
			return nil, err
		}
		if isJSONNull(raw) {
			return nil, fmt.Errorf(
				"全局值不能编码成 JSON null：null 是介质上「从来没写过」的哨兵，存下去之后重新打开会退回初值")
		}
		return raw, nil
	}

	raw, err := spec.encode(initial)
	if err != nil {
		spec.defineErr = fmt.Errorf("全局槽的初值不合法：%w", err)
		return spec
	}
	spec.initialRaw = raw
	return spec
}

// Spec 是一个域的静态声明。
//
// 源: packages/storage/storage-domain/src/spec.ts:34-44
type Spec struct {
	// Name 是域名，必须满足 storage.ValidUnitName。它同时也是后端上那个单元的名字。
	Name string
	// Version 是域的格式版本；介质上盖着的版本号和它对不上时，打开会失败。
	Version int
	// Global 是可选的全局单例槽；不声明就留 nil。
	Global *GlobalSpec
	// Tables 是表声明。
	//
	// 新增: DSH 那边是 Record<string, DomainTableSpec>，表名是 map 的键。
	// 这里是切片，理由和本仓库其它几处一样：Go 的 map 遍历顺序是**故意随机**的，
	// 而表名要投影进后端描述符、要出现在错误信息里，随机顺序会让同一个声明
	// 每次跑出不一样的结果。切片顺序就是登记方写下的顺序。
	Tables []TableSpec
}

// Validate 检查这份声明本身立不立得住。
//
// 源: packages/storage/storage-domain/src/spec.ts:75-114（defineDomain）
//
// 它由 [Facility.Open] 在碰介质之前调用，所以一份配错的声明不会在介质上留下任何痕迹。
//
// 新增: DSH 的 defineDomain 是一个「验完原样返回」的恒等函数，在拥有它的那个模块
// 加载时就抛。Go 里没有模块加载期这个时机，改成一个方法，由打开路径强制调用——
// 时机晚了一点，但「碰介质之前」这条保证是一样的。
func (s Spec) Validate() error {
	if !storage.ValidUnitName(s.Name) {
		return fmt.Errorf("域名 %q 不合法：必须是小写字母开头，之后只能是小写字母、数字或下划线", s.Name)
	}
	if s.Version < 0 {
		return fmt.Errorf("域 %q 的版本号是 %d，不能是负数", s.Name, s.Version)
	}
	seen := make(map[string]struct{}, len(s.Tables))
	for _, table := range s.Tables {
		if !storage.ValidUnitName(table.name) {
			return fmt.Errorf("域 %q 的表名 %q 不合法：必须是小写字母开头，之后只能是小写字母、数字或下划线",
				s.Name, table.name)
		}
		if _, duplicate := seen[table.name]; duplicate {
			// 重名的表在快照里会塌成一张，而声明它的人以为有两张。
			return fmt.Errorf("域 %q 里的表名 %q 重复了", s.Name, table.name)
		}
		seen[table.name] = struct{}{}
		if table.decode == nil {
			// 零值 TableSpec：调用方绕过 [DefineTable] 直接写了结构体字面量。
			// 不挡的话它会在加载记录时空指针崩溃。
			return fmt.Errorf("域 %q 的表 %q 不是用 DefineTable 声明的", s.Name, table.name)
		}
	}
	if s.Global != nil {
		if s.Global.decode == nil {
			return fmt.Errorf("域 %q 的全局槽不是用 DefineGlobal 声明的", s.Name)
		}
		if s.Global.defineErr != nil {
			return fmt.Errorf("域 %q 的全局槽声明有问题：%w", s.Name, s.Global.defineErr)
		}
	}
	return nil
}

// Descriptor 把声明投影成后端要的那份单元描述符。
//
// 源: packages/storage/storage-domain/src/spec.ts:116-129（descriptorOf）
func (s Spec) Descriptor() storage.KVUnitDescriptor {
	tables := make([]string, 0, len(s.Tables))
	for _, table := range s.Tables {
		tables = append(tables, table.name)
	}
	return storage.KVUnitDescriptor{
		Name:      s.Name,
		Version:   s.Version,
		Tables:    tables,
		HasGlobal: s.Global != nil,
	}
}

// isJSONNull 判断一段 JSON 是不是字面量 null。
//
// 后端可能给出 nil 切片，也可能给出四个字节的 "null"——两者在介质上是同一件事，
// 都表示「从来没写过」。见 storage.Snapshot.Global。
func isJSONNull(raw json.RawMessage) bool {
	return len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
