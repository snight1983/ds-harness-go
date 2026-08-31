// 本文件的作用：内容块这个联合类型，它在介质上的样子，以及那一趟共用的递归。
//
// 源: packages/llm/llm/src/types.ts:53-110
// 源: packages/llm/llm/src/content.ts:30-41

package llm

import (
	"encoding/json"
	"fmt"

	"ds-harness-go/attachment"
)

// BlockType 是内容块的判别标签。
//
// 源: packages/llm/llm/src/types.ts:99-108
//
// 它不是封闭的：本包认识下面五个，读到别的会落进 [UnknownBlock]，理由见包文档。
type BlockType string

const (
	// BlockText 是给最终用户看的纯文本。
	BlockText BlockType = "text"
	// BlockReasoning 是推理／思考内容，和可见文本是两回事。
	BlockReasoning BlockType = "reasoning"
	// BlockImage 是一张持久栅格图的引用。
	BlockImage BlockType = "image"
	// BlockToolCall 是模型发起的一次工具调用。
	BlockToolCall BlockType = "tool-call"
	// BlockToolResult 是一次工具调用的结果，要回送给模型。
	BlockToolResult BlockType = "tool-result"
)

// ContentBlock 是模型内容里的一块。
//
// 源: packages/llm/llm/src/types.ts:109-110
//
// 新增: DSH 那边是从 ContentBlockMap 推出来的联合类型，插件可以往那个映射里
// 合并新条目。Go 没有声明合并，这里用「接口 + 未导出的封印方法」把变体封在包内。
// 封住是对的：DSH 自己在 ContentBlockMap 上写着「新的核心块必须连同适配器、
// 界面、压缩三处支持一起落地」——加一个块本来就是核心改动，不是插件能单独做的事。
//
// 但**读**的一侧不封：[UnmarshalContentBlock] 把不认识的块收进 [UnknownBlock]，
// 原样保管那段字节。于是一个旧版本读进一份新版本写的会话日志、再写回去，
// 不会把它读不懂的东西抹掉。
type ContentBlock interface {
	// BlockType 是这一块的判别标签。
	BlockType() BlockType

	// sealedContentBlock 把实现方封在本包内，见类型注释。
	sealedContentBlock()
}

// TextBlock 是给最终用户看的纯文本。
//
// 源: packages/llm/llm/src/types.ts:53-57
type TextBlock struct {
	Text string
}

// BlockType 实现 [ContentBlock]。
func (TextBlock) BlockType() BlockType { return BlockText }

func (TextBlock) sealedContentBlock() {}

// ReasoningBlock 是推理／思考内容，和可见文本分开。
//
// 源: packages/llm/llm/src/types.ts:59-63
type ReasoningBlock struct {
	Text string
}

// BlockType 实现 [ContentBlock]。
func (ReasoningBlock) BlockType() BlockType { return BlockReasoning }

func (ReasoningBlock) sealedContentBlock() {}

// ImageBlock 是一张持久栅格图的引用，在用户内容和助手内容里都合法。
//
// 源: packages/llm/llm/src/types.ts:65-75
//
// 它**故意不分角色**：助手侧的渲染是给以后留的余地——当前的生产适配器都声明
// 只输出文本，所以今天只有用户内容里带图。
type ImageBlock struct {
	// Attachment 是不可变的字节和内在显示元数据，由附件服务拥有。
	Attachment attachment.ImageRef
}

// BlockType 实现 [ContentBlock]。
func (ImageBlock) BlockType() BlockType { return BlockImage }

func (ImageBlock) sealedContentBlock() {}

// ToolCallBlock 是模型发起的一次工具调用。
//
// 源: packages/llm/llm/src/types.ts:77-85
type ToolCallBlock struct {
	// ID 是提供方签发的调用标识，和对应的工具结果靠它对上。
	ID CallID
	// Name 是被调用的工具名。
	Name string
	// Arguments 是模型产出的原始 JSON 串。
	//
	// 这里是 string 而不是解好的结构：它是**模型写的**，随时可能不是合法 JSON，
	// 而那件事该由工具那一侧在解析时报出来，不该让一条消息读不回来。
	Arguments string
}

// BlockType 实现 [ContentBlock]。
func (ToolCallBlock) BlockType() BlockType { return BlockToolCall }

func (ToolCallBlock) sealedContentBlock() {}

// ToolResultBlock 是一次工具调用的结果，回送给模型。
//
// 源: packages/llm/llm/src/types.ts:87-93
type ToolResultBlock struct {
	// ToolCallID 指回发起这次调用的那个 [ToolCallBlock.ID]。
	ToolCallID CallID
	// Content 是结果本身，它自己又是一串内容块——工具可以回图。
	Content Content
	// IsError 表示这次调用失败了。
	//
	// 新增: DSH 是 isError?: boolean。这里用普通的 bool：一次「没说是不是错误」
	// 的调用和一次「说了不是错误」的调用，对读它的人是同一件事。
	IsError bool
}

// BlockType 实现 [ContentBlock]。
func (ToolResultBlock) BlockType() BlockType { return BlockToolResult }

func (ToolResultBlock) sealedContentBlock() {}

// UnknownBlock 是一个本构建不认识的内容块，原样保管。
//
// 新增: DSH 没有这个类型。那边内容块联合是可合并扩展的，配的话术是
// 「switch 上认识的、不认识的落到 default」——一个不认识的块在 TS 里照样是个
// 结构完好的对象，转手写回去是逐字段精确的。
//
// Go 解不进接口，不做点什么的话只有两条路：读到不认识的标签就报错（那样一份
// 由更新版本写下的会话日志会整份打不开），或者丢掉它（那样旧版本读一遍再写一遍
// 就把它抹掉了）。两条都不能接受，所以留这个变体：它把原始字节收着，
// [UnknownBlock.MarshalJSON] 再原样吐回去，往返是逐字节精确的。
type UnknownBlock struct {
	// Kind 是这一块自称的类型。
	Kind BlockType
	// Raw 是这一块完整的原始 JSON。
	Raw json.RawMessage
}

// BlockType 实现 [ContentBlock]，给出这一块自称的类型。
func (b UnknownBlock) BlockType() BlockType { return b.Kind }

func (UnknownBlock) sealedContentBlock() {}

// Content 是一串内容块。
//
// 具名而不是直接用 []ContentBlock，是为了把 [Content.UnmarshalJSON] 挂上去：
// encoding/json 解不进接口，按判别标签分派这一步必须有人写。
type Content []ContentBlock

// Clone 深复制这串内容。
//
// 新增: DSH 用 structuredClone 加 deepFreeze，图的是发布出去的消息改不动。
// Go 的结构体赋值就是复制，但里面的切片不是，所以要有这一趟：
// 它把每一块里的切片（[ToolResultBlock.Content] 和 [UnknownBlock.Raw]）也复制一份。
func (c Content) Clone() Content {
	if c == nil {
		return nil
	}
	cloned := make(Content, len(c))
	for index, block := range c {
		switch typed := block.(type) {
		case ToolResultBlock:
			typed.Content = typed.Content.Clone()
			cloned[index] = typed
		case UnknownBlock:
			typed.Raw = append(json.RawMessage(nil), typed.Raw...)
			cloned[index] = typed
		default:
			// 其余四种里没有引用类型，值复制就是深复制。
			cloned[index] = block
		}
	}
	return cloned
}

// UnmarshalJSON 把一段 JSON 数组读回一串内容块。
func (c *Content) UnmarshalJSON(data []byte) error {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return fmt.Errorf("%w：%w", ErrMalformedValue, err)
	}
	if raws == nil {
		// JSON 里的 null 读回 nil，好让往返闭合：一条内容为 nil 的消息排出去是
		// null，读回来还得是 nil，而不是一个长度为零的切片。
		*c = nil
		return nil
	}

	blocks := make(Content, len(raws))
	for index, raw := range raws {
		block, err := UnmarshalContentBlock(raw)
		if err != nil {
			return err
		}
		blocks[index] = block
	}
	*c = blocks
	return nil
}

// UnmarshalContentBlock 把一段字节读回一个 [ContentBlock]。
//
// 不认识的标签收进 [UnknownBlock]，不报错，理由见那个类型的注释。
func UnmarshalContentBlock(data []byte) (ContentBlock, error) {
	var tagged struct {
		Type BlockType `json:"type"`
	}
	if err := json.Unmarshal(data, &tagged); err != nil {
		return nil, fmt.Errorf("%w：%w", ErrMalformedValue, err)
	}

	switch tagged.Type {
	case BlockText:
		var wire textBlockWire
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, fmt.Errorf("%w：%w", ErrMalformedValue, err)
		}
		return TextBlock{Text: wire.Text}, nil

	case BlockReasoning:
		var wire reasoningBlockWire
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, fmt.Errorf("%w：%w", ErrMalformedValue, err)
		}
		return ReasoningBlock{Text: wire.Text}, nil

	case BlockImage:
		var wire imageBlockWire
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, fmt.Errorf("%w：%w", ErrMalformedValue, err)
		}
		return ImageBlock{Attachment: wire.Attachment}, nil

	case BlockToolCall:
		var wire toolCallBlockWire
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, fmt.Errorf("%w：%w", ErrMalformedValue, err)
		}
		return ToolCallBlock{ID: wire.ID, Name: wire.Name, Arguments: wire.Arguments}, nil

	case BlockToolResult:
		var wire toolResultBlockWire
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, fmt.Errorf("%w：%w", ErrMalformedValue, err)
		}
		return ToolResultBlock{
			ToolCallID: wire.ToolCallID,
			Content:    wire.Content,
			IsError:    wire.IsError,
		}, nil

	default:
		return UnknownBlock{
			Kind: tagged.Type,
			Raw:  append(json.RawMessage(nil), data...),
		}, nil
	}
}

// ContentHasImage 判断这串内容里有没有图片块，会走进工具结果的嵌套内容。
//
// 源: packages/llm/llm/src/content.ts:30-41
//
// 这是所有图片策略（能力判定、纯文本序列化、压缩时的清点）共用的**同一趟**递归，
// 于是没有哪个调用方会在「嵌套多深算数」上悄悄跟别人不一致。
func ContentHasImage(content Content) bool {
	for _, block := range content {
		switch typed := block.(type) {
		case ImageBlock:
			return true
		case ToolResultBlock:
			if ContentHasImage(typed.Content) {
				return true
			}
		}
	}
	return false
}

// 下面是五种块在介质上的样子。
//
// 单独摆出来而不是给那五个结构体加 json 标签，理由和本仓库 credentials 里那条
// 一样：判别标签 type 不是块的字段，它是**类型自己**，由 [ContentBlock.BlockType]
// 给出。放进结构体的话，一个块就多了一个可以和自己的类型对不上的字段。
//
// 字段名照 DSH 写。那边这些是 TS 接口，JSON.stringify 直接就是这些名字，
// 改一个字都会让两侧的会话日志读不通。
type textBlockWire struct {
	Type BlockType `json:"type"`
	Text string    `json:"text"`
}

type reasoningBlockWire struct {
	Type BlockType `json:"type"`
	Text string    `json:"text"`
}

type imageBlockWire struct {
	Type       BlockType           `json:"type"`
	Attachment attachment.ImageRef `json:"attachment"`
}

type toolCallBlockWire struct {
	Type      BlockType `json:"type"`
	ID        CallID    `json:"id"`
	Name      string    `json:"name"`
	Arguments string    `json:"arguments"`
}

type toolResultBlockWire struct {
	Type       BlockType `json:"type"`
	ToolCallID CallID    `json:"toolCallId"`
	Content    Content   `json:"content"`
	IsError    bool      `json:"isError,omitempty"`
}

// MarshalJSON 把这一块连同判别标签一起排出去。
func (b TextBlock) MarshalJSON() ([]byte, error) {
	return json.Marshal(textBlockWire{Type: BlockText, Text: b.Text})
}

// MarshalJSON 把这一块连同判别标签一起排出去。
func (b ReasoningBlock) MarshalJSON() ([]byte, error) {
	return json.Marshal(reasoningBlockWire{Type: BlockReasoning, Text: b.Text})
}

// MarshalJSON 把这一块连同判别标签一起排出去。
func (b ImageBlock) MarshalJSON() ([]byte, error) {
	return json.Marshal(imageBlockWire{Type: BlockImage, Attachment: b.Attachment})
}

// MarshalJSON 把这一块连同判别标签一起排出去。
func (b ToolCallBlock) MarshalJSON() ([]byte, error) {
	return json.Marshal(toolCallBlockWire{
		Type:      BlockToolCall,
		ID:        b.ID,
		Name:      b.Name,
		Arguments: b.Arguments,
	})
}

// MarshalJSON 把这一块连同判别标签一起排出去。
func (b ToolResultBlock) MarshalJSON() ([]byte, error) {
	return json.Marshal(toolResultBlockWire{
		Type:       BlockToolResult,
		ToolCallID: b.ToolCallID,
		Content:    b.Content,
		IsError:    b.IsError,
	})
}

// MarshalJSON 把这一块原样吐回去。
//
// 空的 Raw 当场报错，不排成 `null`：一个没有原始字节的 [UnknownBlock] 只可能是
// 自己造出来的，而把它悄悄写成 null 会在日志里留下一个下次读回来是空的块。
func (b UnknownBlock) MarshalJSON() ([]byte, error) {
	if !json.Valid(b.Raw) {
		return nil, fmt.Errorf("%w：不认识的内容块没有原始字节", ErrMalformedValue)
	}
	return b.Raw, nil
}
