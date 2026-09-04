// 本文件的作用：把一份组合装到一个常驻作用域上——组装器名册是什么、一行怎么变成
// 一次安装、以及任何一行装不起来时整份怎么回滚。
//
// 源: packages/preset/agent-presets/src/mount.ts
//
// 这一整个文件是**换掉的那一半**，不是照抄的。DSH 那边它是 cordis 的动态模块装载器
// 加一套 Fiber 反射审计；Go 静态链接，运行期没有 import。理由和取代方案见包文档。

package agentpresets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/snight1983/ds-harness-go/fs"
	"github.com/snight1983/ds-harness-go/scope"

	yaml "go.yaml.in/yaml/v3"
)

// Composer 是组合清单里一行点得到的那种安装函数。
//
// 新增: 取代 DSH 那边 `import(name)` 出来的一个 cordis 插件。
//
// owner 是这份预设的**常驻作用域**，一次装载里每一行都装在同一个上面；把工具、
// 提示词段落、监听器登记到它上面，加入了这份预设的每个 agent 就都看得见。config
// 是这一行在 YAML 里带的那段配置，已经排成 JSON 字节（没带 config 时是 nil），
// 这样组装器可以用 encoding/json 解，和这个仓库其余地方一致。
//
// 交回的那个函数是这一行的摘除函数，装载失败时按反序被调。
type Composer func(ctx context.Context, owner *scope.Scope, config json.RawMessage) (func(context.Context) error, error)

// ComposerSet 是宿主在编译期登记进来的那些组装器，按名字。
//
// 新增: 一张普通的 map，不是一台带锁的全局注册表——「谁先注册」不该决定一份组合
// 装不装得起来。装配方把它当值组装好填进 [Config.Composers]，理由与
// [github.com/snight1983/ds-harness-go/sessionlog.Vocabulary] 那处相同。
type ComposerSet map[string]Composer

// ErrUnknownComposer 是「组合里有一行点了一个这套部署没登记的组装器」。
var ErrUnknownComposer = errors.New("agent-presets: 组合里点了一个不认识的组装器")

// flatRow 是摊平之后的一行，带着它在诊断里的位置。
type flatRow struct {
	// label 是这一行在诊断里的位置，比如 `row 2` 或者 `row 3 row 1`。
	label string
	// name 是这一行点的那个组装器。
	name string
	// config 是交给组装器的那段配置，排成 JSON；没带时是 nil。
	config json.RawMessage
}

// flattenRows 把一份组合摊成按装载顺序排的那些行。
//
// 新增: DSH 那边一个 `group: true` 的行会造出一个**服务领域**（isolate realm），
// 组里的行发布的服务只有组里看得见。Go 这边没有那层隐式领域——一次登记要显式地把
// 持有者作用域传进去——所以组在这里只是文档上的分节，摊平即可。它仍然被解析、
// 仍然要求它的 config 是一份合法的行列表（见 [entryListProblem]），于是一份 DSH
// 的组合文档在这边照样读得懂、坏了照样报得出来。
func flattenRows(node *yaml.Node, at string) ([]flatRow, error) {
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return nil, nil
		}
		return flattenRows(node.Content[0], at)
	}
	if node.Kind != yaml.SequenceNode {
		// 到不了这里：调用方先跑过 [entryListProblem]。
		return nil, fmt.Errorf("agent-presets: 组合不是一份行列表")
	}
	var rows []flatRow
	for index, item := range node.Content {
		label := fmt.Sprintf("row %d", index+1)
		if at != "" {
			label = fmt.Sprintf("%s row %d", at, index+1)
		}
		var decoded compositionRow
		if err := item.Decode(&decoded); err != nil {
			return nil, fmt.Errorf("agent-presets: %s 读不出来：%w", label, err)
		}
		if decoded.Disabled {
			continue
		}
		if decoded.Group {
			nested, err := flattenRows(&decoded.Config, label)
			if err != nil {
				return nil, err
			}
			rows = append(rows, nested...)
			continue
		}
		config, err := rowConfig(&decoded.Config)
		if err != nil {
			return nil, fmt.Errorf("agent-presets: %s (%s) 的 config 排不成 JSON：%w", label, decoded.Name, err)
		}
		rows = append(rows, flatRow{label: label, name: decoded.Name, config: config})
	}
	return rows, nil
}

// rowConfig 把一行的 config 从 YAML 折成 JSON 字节；没带 config 时交回 nil。
func rowConfig(node *yaml.Node) (json.RawMessage, error) {
	if node == nil || node.Kind == 0 {
		return nil, nil
	}
	var value any
	if err := node.Decode(&value); err != nil {
		return nil, err
	}
	if value == nil {
		return nil, nil
	}
	return json.Marshal(value)
}

// mountComposition 把 preset 那份组合装到 standing 上，交回整份的摘除函数。
//
// 源: packages/preset/agent-presets/src/mount.ts:332-381
//
// 任何一行装不起来，已经装上的那些按**反序**摘干净再报错——一次失败的装载不留下
// 任何东西。这是 DSH 那两道守卫（「没到可用状态的行」「发布进根领域的行」）在 Go 里
// 剩下的那一道：第二道在这个设计里违反不了，理由见包文档。
func mountComposition(
	ctx context.Context,
	fsys fs.FileSystem,
	standing *scope.Scope,
	preset Preset,
	composers ComposerSet,
) (func(context.Context) error, error) {
	content, err := readFile(ctx, fsys, preset.Path)
	if err != nil {
		return nil, &PresetMountError{
			PresetID: preset.ID,
			Reason:   fmt.Sprintf("the composition file cannot be read (%s)", preset.Path),
			Cause:    err,
		}
	}
	var document yaml.Node
	if err := yaml.Unmarshal(content, &document); err != nil {
		return nil, &PresetMountError{
			PresetID: preset.ID,
			Reason:   fmt.Sprintf("the composition is not valid YAML (%s)", preset.Path),
			Cause:    err,
		}
	}
	if problem := entryListProblem(&document, ""); problem != "" {
		return nil, &PresetMountError{
			PresetID: preset.ID,
			Reason:   fmt.Sprintf("%s (%s)", problem, preset.Path),
		}
	}
	rows, err := flattenRows(&document, "")
	if err != nil {
		return nil, &PresetMountError{PresetID: preset.ID, Reason: err.Error(), Cause: err}
	}

	var installed []func(context.Context) error
	unwind := func() {
		// 摘的时候不带调用方的取消：ctx 已经废了也得把装上去的收回来。
		undoCtx := context.WithoutCancel(ctx)
		for index := len(installed) - 1; index >= 0; index-- {
			_ = installed[index](undoCtx)
		}
	}
	// DSH 把几行的失败合成一个 AggregateError，而它自己那句消息一个行名都不带；
	// 这里换成「一行一句、逐行列出」，操作方拿到的是可以照着去改的东西。
	var failures []string
	for _, row := range rows {
		composer, known := composers[row.name]
		if !known {
			failures = append(failures, fmt.Sprintf("- %s (%s): no composer of that name is registered", row.label, row.name))
			continue
		}
		dispose, err := composer(ctx, standing, row.config)
		if err != nil {
			failures = append(failures, fmt.Sprintf("- %s (%s): %v", row.label, row.name, err))
			continue
		}
		if dispose != nil {
			installed = append(installed, dispose)
		}
	}
	if len(failures) > 0 {
		unwind()
		reason := fmt.Sprintf("%d row(s) did not activate:\n%s (%s)",
			len(failures), strings.Join(failures, "\n"), preset.Path)
		return nil, &PresetMountError{PresetID: preset.ID, Reason: reason, Cause: ErrUnknownComposer}
	}
	return func(undoCtx context.Context) error {
		var errs []error
		for index := len(installed) - 1; index >= 0; index-- {
			if err := installed[index](undoCtx); err != nil {
				errs = append(errs, err)
			}
		}
		installed = nil
		return errors.Join(errs...)
	}, nil
}

// Mount 是一份此刻还装着的常驻组合。
//
// 源: packages/preset/agent-presets/src/mount.ts:126-136（PresetMount）
//
// 新增: DSH 那边它带一个 Fiber，本包带的是常驻作用域和它的钥匙——agent 正是被认到
// 那把钥匙下面去的。
type Mount struct {
	// PresetID 是这个子树从哪一份预设组出来的。
	PresetID string
	// Key 是 agent 认作父的那把常驻钥匙，也是这份组合的登记作用域。
	Key *scope.Key
}

// readCompositionStamp 读一份组合文件此刻的身份戳；这个文件给不出身份时第二个
// 返回值是 false。
//
// 源: packages/preset/agent-presets/src/index.ts:546-555（readCompositionStamp）
//
// 新增: DSH 那边这个戳是 `statSync` 出来的「修改时间 + 大小」，一个只有本地文件系统
// 答得出的二元组。这里换成 [fs.Info.Version] 那个不透明的令牌——一个对象存储拿 ETag，
// 一份本地介质拿 stat 身份——因为这里唯一拿它做的事是「和上一次比一比」（见
// [Roster.ensureStanding]），压根不需要它可解释。
//
// 被删了、被换成了一个读不出内容的项、或者别的看不成的情形，对调用方是同一件事：
// 这个文件给不出可以拿来比的身份。
func readCompositionStamp(ctx context.Context, fsys fs.FileSystem, file string) (string, bool) {
	info, found, err := statPath(ctx, fsys, file)
	if err != nil || !found {
		return "", false
	}
	return string(info.Version), true
}
