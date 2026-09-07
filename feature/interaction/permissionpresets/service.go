// 本文件的作用：这张表本身——怎么验一份配置、怎么从旋钮值反推当前是哪个预设、
// 切一次预设都写下去哪些东西，以及一条新会话的初值怎么钉。
//
// 源: packages/interaction/permission-presets/src/index.ts:136-429

package permissionpresets

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/snight1983/ds-harness-go/feature/interaction/userapproval"
	"github.com/snight1983/ds-harness-go/scope"
	"github.com/snight1983/ds-harness-go/settings"
)

// ErrInvalidConfig 表示配置本身不成立，构造被拒。
var ErrInvalidConfig = errors.New("permissionpresets: 配置不成立")

// ErrUnknownPreset 表示这个名字不在表里。
//
// 源: packages/interaction/permission-presets/src/index.ts:352
var ErrUnknownPreset = errors.New("permissionpresets: 不认识这个预设")

// SettingsNamespace 是本包那个设置小节的命名空间。
//
// 源: packages/interaction/permission-presets/src/index.ts:76
var SettingsNamespace = mustNamespace("permission")

// mustNamespace 把一个**字面量**命名空间解出来，不合法就 panic。
//
// 新增: 理由和 [github.com/snight1983/ds-harness-go/harness/agentdefaultmodel] 那个同名
// 帮手逐字相同——入参是一个包级字面量，它不合法说明本包写错了。
func mustNamespace(value string) settings.Namespace {
	namespace, err := settings.NewNamespace(value)
	if err != nil {
		panic(fmt.Sprintf("permissionpresets: 命名空间字面量不合法：%v", err))
	}
	return namespace
}

// Log 是一条会话日志。
//
// 新增: 直接用审批那一层的同名接口，不另立一个一模一样的：本包写下去的每一条旋钮
// 都要经过
// [github.com/snight1983/ds-harness-go/feature/interaction/userapproval.SetPolicy]，
// 那个函数收的就是它。两边各声明一个结构相同的接口，Go 虽然接得上，但读的人得先
// 比对一遍才敢确定它们是同一件事。
type Log = userapproval.Log

// Settings 是存下来、也是装配出来的那一份「新会话用哪个预设」。
//
// 源: packages/interaction/permission-presets/src/index.ts:137-140
type Settings struct {
	// DefaultPreset 是钉进新建会话的那个预设名。
	DefaultPreset string `json:"defaultPreset"`
}

// Config 是这个服务的装配配置。
//
// 源: packages/interaction/permission-presets/src/index.ts:142-155
type Config struct {
	// Presets 是这份部署的预设表，按顺序排。必填。
	//
	// 名字要非空、不重复，也不许是 [CustomPreset]；每一条捆的审批策略都得在
	// 审批那一层的封闭词汇表里。理由见包文档：DSH 那两条默认项都是照沙箱模式
	// 起的名，沙箱那一支不在了，照抄就是骗人，所以这里不带默认表。
	Presets []Preset

	// DefaultPreset 是新会话的默认选择。留空时用「配得上装配默认旋钮值」的那一条；
	// 一条都配不上就在 [New] 当场拒。
	DefaultPreset string

	// Approval 是审批那条接缝。必填：本包捆的唯一那个旋钮由它拥有，读默认值、
	// 写切换都要走它。
	Approval *userapproval.Service

	// LogOf 从一把作用域钥匙找到它那条会话日志。
	//
	// 新增: 顶掉 DSH 的 `agent.session`，理由和
	// [github.com/snight1983/ds-harness-go/feature/interaction/userapproval.Config.LogOf]
	// 逐字相同。只有 [CommandName] 那条命令用得着它，所以它是**可选**的：
	// 不给就不登记那条命令（见 [Service.CommandDefinition]）。
	LogOf func(agent *scope.Key) (Log, error)

	// Settings 是可选的设置服务。
	//
	// 新增: DSH 那边这条接线是 `ctx.inject(['settings'], ...)`——没挂设置服务时整段
	// 登记根本不跑，装配那一份就是最终答案。Go 里「挂没挂」就是这个字段是不是 nil。
	Settings *settings.Provider
}

// Service 拥有这份部署的预设表和它那条写路径。
//
// 源: packages/interaction/permission-presets/src/index.ts:157-430
//
// 新增: DSH 那个类叫 PermissionPresetService，在 Go 里连上包名会变成
// permissionpresets.PermissionPresetService——结巴。按 Go 的习惯叫 Service，
// 对应它那个 `extends Service`。
type Service struct {
	presets  []Preset
	byName   map[string]PresetSpec
	approval *userapproval.Service
	logOf    func(*scope.Key) (Log, error)

	// entry 是装配给的那个默认预设，永远不变。没有设置服务、或者设置服务撤走之后，
	// 它就是答案。
	entry string

	// mutex 只护 section 一个字段，理由和
	// [github.com/snight1983/ds-harness-go/harness/agentdefaultmodel.Service] 逐字相同：
	// 撤登记的那一方和读默认值的那一方是两个 goroutine。
	mutex   sync.RWMutex
	section *settings.Scope[Settings]
}

// New 验一份配置，造出这个服务，并交出撤销函数。
//
// 源: packages/interaction/permission-presets/src/index.ts:188-276
//
// 撤销函数把这个服务退回到装配那一份默认预设，然后摘掉设置登记——退回是有意的，
// 理由和 [github.com/snight1983/ds-harness-go/harness/agentdefaultmodel.New] 逐字相同。
func New(config Config) (*Service, func(), error) {
	if config.Approval == nil {
		return nil, nil, fmt.Errorf("%w: 需要一条审批接缝", ErrInvalidConfig)
	}
	if len(config.Presets) == 0 {
		return nil, nil, fmt.Errorf("%w: 预设表不能是空的", ErrInvalidConfig)
	}
	byName := make(map[string]PresetSpec, len(config.Presets))
	presets := make([]Preset, 0, len(config.Presets))
	for _, preset := range config.Presets {
		switch {
		case preset.Name == "":
			return nil, nil, fmt.Errorf("%w: 预设名不能是空的", ErrInvalidConfig)
		case preset.Name == CustomPreset:
			return nil, nil, fmt.Errorf(
				"%w: %q 是留给「配不上任何一条」那个派生值的，不能拿来当表项名",
				ErrInvalidConfig, CustomPreset)
		case !userapproval.KnownPolicy(preset.Approval):
			return nil, nil, fmt.Errorf("%w: 预设 %q 捆着一个不认识的审批策略 %q",
				ErrInvalidConfig, preset.Name, preset.Approval)
		}
		if _, repeated := byName[preset.Name]; repeated {
			return nil, nil, fmt.Errorf("%w: 预设名 %q 出现了两次", ErrInvalidConfig, preset.Name)
		}
		byName[preset.Name] = preset.PresetSpec
		presets = append(presets, preset)
	}

	service := &Service{
		presets:  presets,
		byName:   byName,
		approval: config.Approval,
		logOf:    config.LogOf,
	}

	entry := config.DefaultPreset
	if entry == "" {
		// 装配默认值配得上哪一条，哪一条就是默认预设。
		entry = service.derive(KnobState{})
	}
	if entry == CustomPreset {
		return nil, nil, fmt.Errorf(
			"%w: 装配的审批默认值配不上任何一条预设，请显式给一个 DefaultPreset", ErrInvalidConfig)
	}
	if _, known := byName[entry]; !known {
		return nil, nil, fmt.Errorf("%w: 默认预设 %q 不在表里（表里有 %s）",
			ErrInvalidConfig, entry, strings.Join(service.Names(), "、"))
	}
	service.entry = entry

	if config.Settings == nil {
		return service, func() {}, nil
	}
	// 默认值那一层留空：这个包没有「类型自带的默认预设」这种东西，一份默认选择
	// 只可能来自装配或者用户。
	section, undo, err := settings.Register(config.Settings, SettingsNamespace, Settings{},
		&settings.Options[Settings]{
			Base:     map[string]any{"defaultPreset": entry},
			Applies:  settings.AppliesLive,
			Validate: service.validateSettings,
		})
	if err != nil {
		return nil, nil, fmt.Errorf("permissionpresets: 登记设置小节失败：%w", err)
	}
	service.section = section

	var once sync.Once
	return service, func() {
		once.Do(func() {
			service.mutex.Lock()
			service.section = nil
			service.mutex.Unlock()
			undo()
		})
	}, nil
}

// validateSettings 拒绝一份指向表外名字的默认选择。
//
// 源: packages/interaction/permission-presets/src/index.ts:206-213（那份 union schema）
//
// DSH 用 `z.union(presetChoices)` 把合法值锁死在表里。Go 没有那套运行期 schema，
// 同一件事在这里表达。放过一个表外的名字，症状会出现在很远的地方：下一条新会话在
// [Service.PinInitial] 里才报「不认识这个预设」，而那时已经没有线索指回这段设置。
func (s *Service) validateSettings(value Settings) error {
	if value.DefaultPreset == "" {
		return fmt.Errorf("permissionpresets: 默认预设不能是空的（表里有 %s）",
			strings.Join(s.Names(), "、"))
	}
	if _, known := s.byName[value.DefaultPreset]; !known {
		return fmt.Errorf("permissionpresets: 默认预设 %q 不在表里（表里有 %s）",
			value.DefaultPreset, strings.Join(s.Names(), "、"))
	}
	return nil
}

// Names 交出可切换的那些预设名，按表的顺序。
//
// 源: packages/interaction/permission-presets/src/index.ts:278-284
//
// 每次造一份新切片：一个调用方排个序或者改一格，就会把别人看到的表也改了。
func (s *Service) Names() []string {
	names := make([]string, 0, len(s.presets))
	for _, preset := range s.presets {
		names = append(names, preset.Name)
	}
	return names
}

// DefaultPreset 交出当下那份「新会话用哪个预设」。
//
// 源: packages/interaction/permission-presets/src/index.ts:286-293
//
// 每一次都重新读，不缓存：设置文档提交了一次改动之后，下一条新会话自然就看得见。
func (s *Service) DefaultPreset() string {
	s.mutex.RLock()
	section := s.section
	s.mutex.RUnlock()
	if section == nil {
		return s.entry
	}
	if stored := section.Get().DefaultPreset; stored != "" {
		return stored
	}
	return s.entry
}

// Resolve 查一条预设捆着的东西。
//
// 源: packages/interaction/permission-presets/src/index.ts:344-356
func (s *Service) Resolve(name string) (PresetSpec, error) {
	spec, known := s.byName[name]
	if !known {
		return PresetSpec{}, fmt.Errorf("%w：%q（表里有 %s）",
			ErrUnknownPreset, name, strings.Join(s.Names(), "、"))
	}
	return spec, nil
}

// Current 交出这条会话此刻生效的那个预设名。
//
// 源: packages/interaction/permission-presets/src/index.ts:301-310
func (s *Service) Current(log Log) string {
	return s.derive(FoldKnobs(log.Events()))
}

// derive 从一份折好的旋钮状态反推是哪个预设——[Service.Current] 和那个投影单元
// 共用的同一套算式。
//
// 源: packages/interaction/permission-presets/src/index.ts:312-325
//
// 仍然配得上的那次选择赢下「两条预设捆了同一份旋钮值」的平手；否则表里第一条配得上
// 的赢；一条都配不上就是 [CustomPreset]。
func (s *Service) derive(state KnobState) string {
	approval := state.Approval
	if approval == "" {
		approval = s.approval.DefaultPolicy()
	}
	if state.Preset != "" {
		if spec, known := s.byName[state.Preset]; known && spec.Approval == approval {
			return state.Preset
		}
	}
	for _, preset := range s.presets {
		if preset.Approval == approval {
			return preset.Name
		}
	}
	return CustomPreset
}

// SelectFor 把一份折好的旋钮状态摆成界面读的那份完整选项单。
//
// 源: packages/interaction/permission-presets/src/index.ts:327-342
//
// [CustomPreset] 只在它正是当前值的时候才追加在末尾。
func (s *Service) SelectFor(state KnobState) PermissionSelect {
	current := s.derive(state)
	options := make([]PresetOption, 0, len(s.presets)+1)
	for _, preset := range s.presets {
		options = append(options, option(preset.Name, preset.PresetSpec))
	}
	if current == CustomPreset {
		options = append(options, customOption())
	}
	return PermissionSelect{Options: options, CurrentValue: current}
}

// OptionOf 造一条表项（或者 [CustomPreset]）在界面上的样子。
//
// 源: packages/interaction/permission-presets/src/index.ts:358-371
func (s *Service) OptionOf(name string) (PresetOption, error) {
	if name == CustomPreset {
		return customOption(), nil
	}
	spec, err := s.Resolve(name)
	if err != nil {
		return PresetOption{}, err
	}
	return option(name, spec), nil
}

// option 造一条表项的选项；没配显示名就回落到表项名本身。
//
// 源: packages/interaction/permission-presets/src/index.ts:369-370
func option(name string, spec PresetSpec) PresetOption {
	label := spec.Label
	if label == "" {
		label = name
	}
	return PresetOption{Value: name, Name: label, Description: spec.Description}
}

// customOption 是那个派生值在界面上的样子。
//
// 源: packages/interaction/permission-presets/src/index.ts:366-367
//
// 这两句是给用户看的载荷，跟着 DSH 保持英文，和本仓库其余上线文案同一条界线。
// 「sandbox and approval」里的沙箱那一半跟着本包的旋钮一起去掉了。
func customOption() PresetOption {
	return PresetOption{
		Value:       CustomPreset,
		Name:        "Custom",
		Description: "Current approval settings do not match a preset.",
	}
}

// Set 记下一次预设切换，然后把变了的旋钮通过它自己那条写路径写下去。
//
// 源: packages/interaction/permission-presets/src/index.ts:373-381
//
// 再选一次当下生效的那个预设，什么都不写。
//
// 走的是
// [github.com/snight1983/ds-harness-go/feature/interaction/userapproval.SetPolicy]
// 这条不通知模型的路：初始化和纯日志侧的切换用它。要让模型知道这次切换，用
// [Service.SwitchFor]。
func (s *Service) Set(log Log, name string) error {
	return s.apply(log, name, func(policy userapproval.Policy) error {
		return userapproval.SetPolicy(log, policy)
	})
}

// SetFor 把一个活 agent 的预设钉上去，不惊动模型。
//
// 源: packages/webhook/webhook/src/session.ts:153（`ctx.permissionPresets.set(handle.agent.session, preset)`）
//
// 和 [Service.SwitchFor] 只差写路径那一条：一条刚建出来、还没跑过任何一步的会话
// 用这个——它没有「上一档」可言，一条通知模型的切换只会凭空多出一句谁都没做过的事。
//
// 新增: DSH 那边这一处直接把 `handle.agent.session` 交给 `set`。Go 里
// [Service.Set] 收的是一条 [Log]，而拿得到的是那个 agent 的作用域钥匙——中间那段
// 由 [Config.LogOf] 走通，和 [Service.SwitchFor] 同一条路。
func (s *Service) SetFor(agent *scope.Key, name string) error {
	log, err := s.resolveLog(agent)
	if err != nil {
		return err
	}
	return s.Set(log, name)
}

// SwitchFor 切一个活 agent 的预设，并把变了的审批策略通过
// [github.com/snight1983/ds-harness-go/feature/interaction/userapproval.Service.SwitchPolicy]
// 写下去——那条路会把这次切换排进它下一次模型步。
//
// 源: packages/interaction/permission-presets/src/index.ts:271（`this.ctx.approval.setPolicy(agent, policy)`）
//
// 新增: DSH 是命令处理器里的一行内联闭包。Go 这边它是一个方法：那条命令是可选的
// （[Config.LogOf] 不给就不登记），而一个装配方完全可能自己接一条界面上的切换按钮，
// 它要的正是这一条路。
func (s *Service) SwitchFor(agent *scope.Key, name string) error {
	log, err := s.resolveLog(agent)
	if err != nil {
		return err
	}
	return s.apply(log, name, func(policy userapproval.Policy) error {
		return s.approval.SwitchPolicy(agent, policy)
	})
}

// apply 用调用方挑的那条策略写路径落一次预设切换。
//
// 源: packages/interaction/permission-presets/src/index.ts:383-396
func (s *Service) apply(log Log, name string, setApproval func(userapproval.Policy) error) error {
	spec, err := s.Resolve(name)
	if err != nil {
		return err
	}
	if s.Current(log) != name {
		if err := log.Append(EventPreset, PresetData{Preset: name}); err != nil {
			return fmt.Errorf("permissionpresets: 这次预设选择写不进日志：%w", err)
		}
	}
	if spec.Approval == s.approval.PolicyFor(log) {
		return nil
	}
	return setApproval(spec.Approval)
}

// PinInitial 在一条会话被发布之前把缺的权限事实补齐。
//
// 源: packages/interaction/permission-presets/src/index.ts:398-429
//
// 一条真正全新的会话用当下那份用户默认值；一条种过子的、或者已经写过一半的会话
// 保住它自己生效的旋钮值，只补上缺的那几条耐久事实。
//
// 新增: DSH 在构造函数里挂 `ctx.on('session/created', ...)` 并把已有会话走一遍。
// Go 里活会话服务是循环那一块的东西，本包拿不到它，所以这是一个由装配方在会话
// 发布之前叫的方法（成例见
// [github.com/snight1983/ds-harness-go/feature/plan/planmode.Controller.OnSessionDisposed]）。
func (s *Service) PinInitial(log Log) error {
	state := FoldKnobs(log.Events())
	if state.Preset == "" && state.Approval == "" && !state.Seeded {
		name := s.DefaultPreset()
		spec, err := s.Resolve(name)
		if err != nil {
			return err
		}
		if err := log.Append(EventPreset, PresetData{Preset: name}); err != nil {
			return fmt.Errorf("permissionpresets: 初始预设写不进日志：%w", err)
		}
		return userapproval.SetPolicy(log, spec.Approval)
	}

	if state.Preset == "" {
		if effective := s.derive(state); effective != CustomPreset {
			if err := log.Append(EventPreset, PresetData{Preset: effective}); err != nil {
				return fmt.Errorf("permissionpresets: 初始预设写不进日志：%w", err)
			}
		}
	}
	if state.Approval == "" {
		return userapproval.SetPolicy(log, s.approval.DefaultPolicy())
	}
	return nil
}

// resolveLog 找出这个 agent 那条会话日志。
func (s *Service) resolveLog(agent *scope.Key) (Log, error) {
	if s.logOf == nil {
		return nil, fmt.Errorf("%w: 没有一条从作用域钥匙找到会话日志的路", ErrInvalidConfig)
	}
	log, err := s.logOf(agent)
	if err != nil {
		return nil, err
	}
	if log == nil {
		// 一条 (nil, nil) 的答复是装配方的 bug，而它往下走就是解引用 panic。
		return nil, fmt.Errorf("%w: 这个 agent 没有会话日志", ErrInvalidConfig)
	}
	return log, nil
}
