// 本文件的作用：从文件系统上把预设找出来，并且当场判它的**健康**——
// 一个组合文件缺了或者读不动的目录，报成一行坏掉的名册项，而不是跳过。
//
// 源: packages/preset/agent-presets/src/discovery.ts

package agentpresets

import (
	"cmp"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

// CompositionFile 是那份让一个目录成为预设的组合文件。
//
// 源: packages/preset/agent-presets/src/discovery.ts:26
//
// 名字保持和 DSH 逐字一致（`agent.cordis.yml`），这样两边的预设目录可以互换着看。
// 里面那份文档的形状也一样：一个顶层的插件行列表。**行里的 `name` 在 Go 这边指的是
// 一个组装器名字**，不是一个 npm 包名，理由见包文档。
const CompositionFile = "agent.cordis.yml"

// UserPresetDir 是放本地创作的预设的那个目录名。
//
// 源: packages/preset/agent-presets/src/discovery.ts:41
//
// 新增: DSH 把它拼在 harness home 底下（`dshHomePath(USER_PRESET_DIR)`）。这里只
// 导出这个约定的段名，拼成绝对路径是装配方的事——服务端没有「当前用户的 home」，
// 理由见包文档。装配方把拼好的那条路径填进 [Config.UserRoot]。
const UserPresetDir = ".agent-presets"

// compositionRow 是组合清单里的一行。
//
// 源: packages/preset/agent-presets/src/discovery.ts:66
type compositionRow struct {
	// Name 是这一行点的那个组装器；必填、非空。
	Name string `yaml:"name"`
	// Group 为真表示这一行是一个组，它的 Config 是一份嵌套的行列表。
	Group bool `yaml:"group"`
	// Disabled 为真表示这一行不装。
	Disabled bool `yaml:"disabled"`
	// Config 是交给组装器的那段配置；Group 为真时它是嵌套的行列表。
	Config yaml.Node `yaml:"config"`
}

// entryListProblem 说清 rows 为什么不是一份行列表，能是就给空串。
//
// 源: packages/preset/agent-presets/src/discovery.ts:55-76
//
// 一次**浅**的形状检查，刻意做得比装载器少：它不解算组装器名字、也不套用 config。
// 它抓的是那种让装载器连开始都开始不了的手改。它必须接受装载器接受的一切，所以
// 一行只被要求是一张带 `name` 的映射（组则递归进它自己那份列表）。
//
// at 是嵌套诊断用的行路径前缀，顶层是空串。
func entryListProblem(node *yaml.Node, at string) string {
	if node == nil || node.Kind == 0 {
		if at == "" {
			return "the composition must be a top-level list of plugin rows"
		}
		return fmt.Sprintf("group %s must hold a list of plugin rows", at)
	}
	// 一份 YAML 文档的根是 DocumentNode，真正的值在它唯一那个孩子上。
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return "the composition must be a top-level list of plugin rows"
		}
		return entryListProblem(node.Content[0], at)
	}
	if node.Kind != yaml.SequenceNode {
		if at == "" {
			return "the composition must be a top-level list of plugin rows"
		}
		return fmt.Sprintf("group %s must hold a list of plugin rows", at)
	}
	for index, row := range node.Content {
		label := fmt.Sprintf("row %d", index+1)
		if at != "" {
			label = fmt.Sprintf("%s row %d", at, index+1)
		}
		if row.Kind != yaml.MappingNode {
			return label + ` is not a plugin row (expected a map with a "name")`
		}
		var decoded compositionRow
		if err := row.Decode(&decoded); err != nil || decoded.Name == "" {
			return label + ` names no plugin (a "name" string is required)`
		}
		if decoded.Group {
			if nested := entryListProblem(&decoded.Config, label); nested != "" {
				return nested
			}
		}
	}
	return ""
}

// compositionProblem 说清 path 上那份组合为什么装不了，能装就给空串。
//
// 源: packages/preset/agent-presets/src/discovery.ts:86-106
func compositionProblem(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		// 调用方刚刚才 stat 过这个文件；此刻任何读失败——中间被删了、权限——
		// 和解不动是同一个答案。
		return "the composition file " + CompositionFile + " cannot be read"
	}
	var document yaml.Node
	if err := yaml.Unmarshal(content, &document); err != nil {
		// 只留第一行：yaml 的报错会带上多行的位置片段，而这句话显示在一张名册卡片上，
		// 不是在终端里。
		full := err.Error()
		if newline := strings.IndexByte(full, '\n'); newline >= 0 {
			full = full[:newline]
		}
		return "the composition is not valid YAML: " + full
	}
	return entryListProblem(&document, "")
}

// isFile 判一条路径上是不是一个存在的常规文件。
//
// 源: packages/preset/agent-presets/src/discovery.ts:113-122
func isFile(path string) bool {
	info, err := os.Stat(path)
	// 任何 stat 失败——不在、读不了、断链——都表示这个目录没有摆出一份组合，
	// 而那不是错误：这个目录只是不是一份预设。
	return err == nil && info.Mode().IsRegular()
}

// ScanRoot 扫一个根，找出它下面的预设目录。
//
// 源: packages/preset/agent-presets/src/discovery.ts:139-170
//
// 一个不存在的根交出零份预设而不是报错：用户根在第一份本地创作出现之前都不存在，
// 而点了一个没有任何根供得出的默认值，在解算那一步已经会当场炸。
//
// 每一个名字能当预设 id 用的目录都是一行名册项——组合缺了或者装不动时带上 Broken。
// 一个名字在 [IsPresetID] 之外的目录则**跳过**：没有任何副本能取到那个名字，所以它
// 什么都不挡；而把 `.DS_Store` 那个量级的残渣报成坏掉的预设，只会教会用户无视这个标记。
func ScanRoot(root Root) ([]Preset, error) {
	dir := filepath.Clean(root.Path)
	children, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("agent-presets: 读不了预设根 %s：%w", dir, err)
	}
	found := make([]Preset, 0, len(children))
	for _, child := range children {
		if !child.IsDir() || !IsPresetID(child.Name()) {
			continue
		}
		directory := filepath.Join(dir, child.Name())
		path := filepath.Join(directory, CompositionFile)
		broken := "the composition file " + CompositionFile +
			" is missing — the directory still occupies the id; delete it or restore the file"
		if isFile(path) {
			broken = compositionProblem(path)
		}
		// 只有展示文字，而且永不致命：一份元数据读不出来的预设照样装得起来，
		// 它只是显示自己的 id。
		metadata := ReadMetadata(directory)
		found = append(found, Preset{
			ID:          child.Name(),
			Trust:       root.Trust,
			Path:        path,
			Name:        metadata.Name,
			Description: metadata.Description,
			Order:       metadata.Order,
			Broken:      broken,
		})
	}
	// 声明了位次的在前，于是发出去的那一套按能力顺序读；其余回落到 id，
	// 让创作出来的那些保持稳定。
	slices.SortFunc(found, func(left, right Preset) int {
		if byOrder := cmp.Compare(orderOf(left), orderOf(right)); byOrder != 0 {
			return byOrder
		}
		return strings.Compare(left.ID, right.ID)
	})
	return found, nil
}

// orderOf 把一个没声明位次的预设折成正无穷，好和声明了的一起比。
func orderOf(preset Preset) float64 {
	if preset.Order == nil {
		return math.Inf(1)
	}
	return *preset.Order
}

// DiscoverPresets 按优先级顺序扫过每一个根。
//
// 源: packages/preset/agent-presets/src/discovery.ts:177-186
//
// 靠前的根赢下重名的 id。
func DiscoverPresets(roots []Root) ([]Preset, error) {
	seen := make(map[string]struct{}, len(roots)*4)
	var all []Preset
	for _, root := range roots {
		presets, err := ScanRoot(root)
		if err != nil {
			return nil, err
		}
		for _, preset := range presets {
			if _, already := seen[preset.ID]; already {
				continue
			}
			seen[preset.ID] = struct{}{}
			all = append(all, preset)
		}
	}
	return all, nil
}
