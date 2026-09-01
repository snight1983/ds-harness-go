// 本文件的作用：一条事件是怎么进到模型可见的那条有序表面上的。
//
// 源: packages/core/session/src/types.ts:363-393

package session

import (
	"encoding/json"
	"fmt"
)

// SurfaceOpKind 是表面操作的判别标签。
type SurfaceOpKind string

const (
	// OpAppend 是加到表面末尾——用户、助手、工具三种消息的常规路径。
	OpAppend SurfaceOpKind = "append"
	// OpReplace 是用这一条盖掉表面上一段已有的节点。
	OpReplace SurfaceOpKind = "replace"
)

// SurfaceOp 说明一条事件是怎么进到表面上的。只有 [IsSurfaceEligibleType] 认的
// 那三种事件能带它。
//
// 源: packages/core/session/src/types.ts:346-361（SurfaceOp）
//
// 新增: DSH 那边是 `'append' | { op: 'replace'; start; end }`——一个字符串字面量
// 和一个对象的联合。Go 里用「接口 + 未导出的封印方法」把两个变体封在包内。
//
// 这个联合是**封闭**的，不留 Unknown 变体：[FormatVersion] 的注释里把
// 「SurfaceOp 的变体集合」明确列进了必须进位的结构性改动，所以一份带着第三种
// 操作的日志本来就该被版本检查先一步拦下。
type SurfaceOp interface {
	// SurfaceOpKind 是这个操作的判别标签。
	SurfaceOpKind() SurfaceOpKind

	// sealedSurfaceOp 把实现方封在本包内，见类型注释。
	sealedSurfaceOp()
}

// AppendOp 把这条事件加到表面末尾。
//
// 源: packages/core/session/src/types.ts:367-368
type AppendOp struct{}

// SurfaceOpKind 实现 [SurfaceOp]。
func (AppendOp) SurfaceOpKind() SurfaceOpKind { return OpAppend }

func (AppendOp) sealedSurfaceOp() {}

// MarshalJSON 把追加操作排成一个**裸字符串** "append"，不是对象。
//
// 这个不对称的介质形状是 DSH 定的，照抄：常规路径占了绝大多数事件，
// 每条省下一层对象。
func (AppendOp) MarshalJSON() ([]byte, error) { return []byte(`"append"`), nil }

// ReplaceOp 用这条事件盖掉表面上从 Start 到 End（两端都含）的那一段节点。
//
// 源: packages/core/session/src/types.ts:369-374
//
// 两端都必须是当前表面上真实存在的节点。Start 等于 End 就是替换单个节点。
// 这条事件的 [Event.SourceEventSeqs] 必须把每一个被盖掉的节点都列出来。
// 压缩在用它，任何要替换表面的产出方都可以用。
type ReplaceOp struct {
	// Start 是被替换区间的起始 seq（含）。
	Start int
	// End 是被替换区间的结束 seq（含）。
	End int
}

// SurfaceOpKind 实现 [SurfaceOp]。
func (ReplaceOp) SurfaceOpKind() SurfaceOpKind { return OpReplace }

func (ReplaceOp) sealedSurfaceOp() {}

// replaceOpWire 是替换操作在介质上的样子。
type replaceOpWire struct {
	Op    string `json:"op"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

// MarshalJSON 把替换操作排出去。
func (o ReplaceOp) MarshalJSON() ([]byte, error) {
	return json.Marshal(replaceOpWire{Op: string(OpReplace), Start: o.Start, End: o.End})
}

// UnmarshalSurfaceOp 把一段字节读回一个表面操作。
//
// 源: packages/core/session/src/surface.ts:172-208
//
// 分派看的是 JSON 值本身的形状：字符串只能是 "append"，对象必须**恰好**带
// op／start／end 三个键。「恰好」这件事照抄 DSH 的 isReplaceOp：多一个键说明
// 写的一方在表达一个本构建读不懂的东西，收下它等于把那部分意思悄悄丢掉。
func UnmarshalSurfaceOp(data []byte) (SurfaceOp, error) {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		if text != string(OpAppend) {
			return nil, fmt.Errorf("%w：表面操作是字符串时只能是 %q，实际 %q",
				ErrMalformedValue, OpAppend, text)
		}
		return AppendOp{}, nil
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("%w：表面操作既不是字符串也不是对象：%w", ErrMalformedValue, err)
	}
	if len(fields) != 3 ||
		fields["op"] == nil || fields["start"] == nil || fields["end"] == nil {
		return nil, fmt.Errorf("%w：替换操作必须恰好带 op、start、end 三个键", ErrMalformedValue)
	}
	// 键的集合上面已经逐个点过名了，这里只负责把三个值读成想要的类型。
	var wire replaceOpWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, fmt.Errorf("%w：替换操作读不回来：%w", ErrMalformedValue, err)
	}
	if wire.Op != string(OpReplace) {
		return nil, fmt.Errorf("%w：替换操作的 op 只能是 %q，实际 %q",
			ErrMalformedValue, OpReplace, wire.Op)
	}
	if wire.Start < 0 || wire.End < 0 {
		return nil, fmt.Errorf("%w：替换区间的两端必须是非负的 seq，实际 %d 到 %d",
			ErrMalformedValue, wire.Start, wire.End)
	}
	return ReplaceOp{Start: wire.Start, End: wire.End}, nil
}
