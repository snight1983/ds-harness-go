// 本文件的作用：一份预设的展示元数据——选择器上显示的那个名字和那句说明，
// 以及为什么读它失败一律降级成「没有元数据」而不是让发现整个失败。
//
// 源: packages/preset/agent-presets/src/metadata.ts

package agentpresets

import (
	"context"
	"math"
	"path"
	"strings"

	yaml "go.yaml.in/yaml/v3"

	"github.com/snight1983/ds-harness-go/fs"
)

// MetadataFile 是组合文件旁边那份可选的展示元数据。
//
// 源: packages/preset/agent-presets/src/metadata.ts:24-25（METADATA_FILE）
//
// 它单独一个文件，是因为组合本身是一个**顶层的插件行列表**——YAML 上没法在它旁边
// 再挂同级的键，而伪造一行元数据出来等于递给装载器一个要去装的东西。分开也让组合
// 保持它名字说的那样：一份纯粹的组合清单。
//
// 这个文件**只**装展示文字。id 是目录名、trust 来自它被发现时所在的那个根，两者
// 在这里都不可写——否则一份本地创作的预设就能自称是随部署发出去的。
const MetadataFile = "preset.yml"

// Metadata 是一份预设可以自己发布的那点展示文字。
//
// 源: packages/preset/agent-presets/src/metadata.ts:27-39（PresetMetadata）
type Metadata struct {
	// Name 是面向人的名字；缺席时回落到预设 id。
	Name string
	// Description 是「这是干什么用的」那一句。
	Description string
	// Order 是它在同组里的位次，小的在前。没声明的排在声明了的全部后面，
	// 然后按 id 排——于是发出去的那一套能按能力顺序读，创作出来的保持字母序。
	//
	// nil 表示没声明，理由同 [Preset.Order]。
	Order *float64
}

// text 把一个值折成一段去过两头空白的非空文字；别的一律给空串。
//
// 源: packages/preset/agent-presets/src/metadata.ts:42-46
func text(value any) string {
	str, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(str)
}

// number 把一个值折成一个有限的数；别的一律给 nil。
//
// 新增: yaml.v3 解进 any 时整数是 int、小数是 float64，两支都要认。DSH 那边
// JS 只有一种 number，所以只判一次 Number.isFinite。
func number(value any) *float64 {
	var out float64
	switch typed := value.(type) {
	case int:
		out = float64(typed)
	case int64:
		out = float64(typed)
	case float64:
		out = typed
	default:
		return nil
	}
	if math.IsInf(out, 0) || math.IsNaN(out) {
		return nil
	}
	return &out
}

// ReadMetadata 读一个预设目录的展示元数据。
//
// 源: packages/preset/agent-presets/src/metadata.ts:56-85
//
// 不在、解不动、形状不对，三种情形是同一个答案——空元数据——因为调用方渲染的是
// 一个选择器，不是一份诊断。这里**不返回 error**：一份展示文字读不出来的预设照样
// 装得起来，它只是显示自己的 id。
func ReadMetadata(ctx context.Context, fsys fs.FileSystem, directory string) Metadata {
	raw, err := readFile(ctx, fsys, path.Join(directory, MetadataFile))
	if err != nil {
		// 不在是常态：元数据是可选的，而每一份靠复制别人建出来的预设都不带它。
		return Metadata{}
	}
	var parsed any
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		// 坏掉的展示文字不值得让发现失败；选择器回落到 id，组合照样装得起来。
		return Metadata{}
	}
	record, ok := parsed.(map[string]any)
	if !ok {
		return Metadata{}
	}
	return Metadata{
		Name:        text(record["name"]),
		Description: text(record["description"]),
		Order:       number(record["order"]),
	}
}

// metadataDocument 是这份展示文字排到 YAML 上的样子。
//
// 三个字段都带 omitempty：缺席的字段整个不写出来，而不是写成空的——一份没有说明的
// 预设不该发出去一个读起来像「作者特意留白」的键。
type metadataDocument struct {
	Name        string   `yaml:"name,omitempty"`
	Description string   `yaml:"description,omitempty"`
	Order       *float64 `yaml:"order,omitempty"`
}

// RenderMetadata 把展示元数据排成那个文件的内容。
//
// 源: packages/preset/agent-presets/src/metadata.ts:95-105
//
// 第二个返回值为 false 表示没有任何东西要存——调用方据此把那个文件删掉，而不是
// 写一份空文档下去。
func RenderMetadata(metadata Metadata) (string, bool) {
	document := metadataDocument{
		Name:        strings.TrimSpace(metadata.Name),
		Description: strings.TrimSpace(metadata.Description),
		Order:       metadata.Order,
	}
	if document.Name == "" && document.Description == "" && document.Order == nil {
		return "", false
	}
	encoded, err := yaml.Marshal(document)
	if err != nil {
		// 不可达：三个字段都是 string / *float64。
		return "", false
	}
	return string(encoded), true
}
