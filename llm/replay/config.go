// 本文件的作用：配置这一侧——一份夹具从哪里读、一套只为回放存在的提供方目录长什么
// 样，以及父子两份剧本按什么次序排队。
//
// 源: packages/test-support/llm-replay/src/index.ts:47-160, 509-580, 805-865

package replay

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/session"
)

// ErrInvalidConfig 是「配置本身不成立」。
var ErrInvalidConfig = errors.New("llm-replay: 配置不成立")

// ErrFixtureNotFound 是「那份夹具不在」。
var ErrFixtureNotFound = errors.New("llm-replay: 夹具不在")

// ModelConfig 是一条只为回放存在的提供方目录里的一个模型。
//
// 源: packages/test-support/llm-replay/src/index.ts:49-82（ReplayModelConfig）
type ModelConfig struct {
	// ID 是回放请求用的那个模型 id。
	ID string
	// Name 是选择器上的显示名；空串回落到 ID。
	Name string
	// Description 是选择器上那句可选的说明。
	Description string
	// ContextWindow 是这条回放路线发布的上下文容量；0 表示不发布。
	ContextWindow int
	// InputModalities 是声明出来的输入模态，好让场景演得了能力闸门
	// （比如收图的 read_image）。nil 表示不声明。
	InputModalities []llm.ModelModality
	// DefaultMaxTokens 是调用方没给上限时这条回放路线落实进去的那个上限；
	// 0 表示不落实。它存在，是为了让回放重建出一份活目录当初造出来的请求头。
	DefaultMaxTokens int
	// ReasoningEfforts 是这条回放路线认的那些推理档位 id，按展示次序。
	ReasoningEfforts []string
	// DefaultReasoningEffort 是调用方没点档位时落实进去的那一档；
	// 它必须出现在 ReasoningEfforts 里，否则解算这条路线时被拒。
	DefaultReasoningEffort string
}

// ProviderConfig 是回放适配器发布的一条提供方路线。
//
// 源: packages/test-support/llm-replay/src/index.ts:84-94（ReplayProviderConfig）
type ProviderConfig struct {
	// ID 是回放请求用的那条提供方路由。
	ID string
	// Name 是选择器上的显示名；空串回落到 ID。
	Name string
	// Models 是暴露给那些要演发现的场景看的那些模型。
	Models []ModelConfig
	// RetryPolicy 是这条提供方自己拥有的重试策略，供装配出来的恢复类快照用；
	// nil 表示这条路线不发布策略。
	RetryPolicy *llm.RetryPolicyConfig
}

// Config 是解算好的回放配置。
//
// 源: packages/test-support/llm-replay/src/index.ts:918-934（Config）, 809-822
//
// 新增: DSH 分成插件的 Config 和解算好的 ReplayConfig 两个类型，因为 cordis 交进来
// 的是一份 schemastery 验过的插件配置，`apply` 再把环境变量的默认补进去。Go 没有
// 装载器，两者合成这一个类型，那一步补默认由 [ResolveFromEnv] 做。
type Config struct {
	// File 是**主**（父）会话 session.jsonl 夹具的路径。单会话场景里它是唯一那份
	// 日志；嵌套 agent 场景里它是父，孩子那些日志走 ChildFiles。
	File string
	// OverrideFile 是主会话那份旁挂文件：整份 [Entry] 数组是**替换**，
	// `{"patches": [...]}` 是**增补**（见 [OverrideDoc]）。它服务那些
	// assistant/chunk 表达不出来的单会话场景——一块都没吐就抛、取消挂住、注入的
	// 瞬时故障。普通场景和嵌套场景不给。
	OverrideFile string
	// ChildFiles 是另外那些录好的子会话日志（一个嵌套 agent 场景里那些 subagent
	// 会话）。每一份各自推导；全体按 createdAt 排，于是最早的父绑第一个活会话。
	// 单会话场景是空的。
	ChildFiles []string
	// Providers 是可选的提供方目录。非空时回放为这些路线登记一个适配器；空或者
	// 不给时保留那条不需要发现的场景用的兜底瀑布。
	Providers []ProviderConfig
	// Pace 是每一块之间的节流间隔，好让下游传输（比如浏览器看到的那条 SSE 复用）
	// 真的看到增量投递。它**只是**一个拟真旋钮——正确性绝不许依赖它。0 表示保持
	// 今天这种一口气吐完。取消在等待中途发生时和别处一样当场把流掐掉。
	//
	// 新增: DSH 是 `paceMs?: number` 加一道「非负整数」校验。Go 有
	// [time.Duration]，单位因此进了类型，那道「是不是整数」的校验随之消失，
	// 只剩「不许是负数」。
	Pace time.Duration
}

// SessionScript 是一份录好的会话：那些调用，加上给父子排队用的头字段。
//
// 源: packages/test-support/llm-replay/src/index.ts:153-169（SessionScript）
//
// 录下来的 id 只进诊断；活会话拿的是新铸的 id，靠**第一次调用的次序**绑。
type SessionScript struct {
	// RecordedID 是录下来的那个会话 id（只做诊断——活的那个不一样）。
	RecordedID session.SessionID
	// CreatedAt 是建会话时间，也是那把确定的排序键（父在子前）。
	CreatedAt int64
	// Entries 是按录下来的调用次序排的那些条目。
	Entries []Entry
	// Primary 表示这是不是那份**主**（父）会话。它拿来打破 CreatedAt 的平手——
	// 父总是发出第一次模型调用的那个。
	Primary bool
}

// LoadScript 读主会话那份剧本。
//
// 源: packages/test-support/llm-replay/src/index.ts:566-597（loadReplayScript）
//
// 有旁挂文件就用旁挂文件（整份替换，或者拿 `{patches}` 盖在推导出来的剧本上），
// 否则用从会话 JSONL 推出来的那一份（夹具不在就当场失败）。
func LoadScript(config Config) ([]Entry, error) {
	if config.OverrideFile == "" || !fileExists(config.OverrideFile) {
		return deriveScriptFromFile(config.File)
	}
	data, err := os.ReadFile(config.OverrideFile)
	if err != nil {
		return nil, fmt.Errorf("%w：读不了旁挂文件 %s：%w", ErrInvalidOverride, config.OverrideFile, err)
	}
	doc, err := readOverrideDoc(data, config.OverrideFile)
	if err != nil {
		return nil, err
	}
	if doc.Replacement != nil {
		return doc.Replacement, nil
	}
	script, err := deriveScriptFromFile(config.File)
	if err != nil {
		return nil, err
	}
	derived := len(script)
	seen := make(map[int]bool, len(doc.Patches))
	for _, patch := range doc.Patches {
		if patch.At > derived {
			return nil, fmt.Errorf("%w：补丁下标 %d 越界（推出来的剧本有 %d 次调用，等于长度时追加）：%s",
				ErrInvalidOverride, patch.At, derived, config.OverrideFile)
		}
		if seen[patch.At] {
			return nil, fmt.Errorf("%w：补丁下标 %d 重复了：%s",
				ErrInvalidOverride, patch.At, config.OverrideFile)
		}
		seen[patch.At] = true
		if patch.At == len(script) {
			script = append(script, patch.Entry)
			continue
		}
		script[patch.At] = patch.Entry
	}
	return script, nil
}

// fileExists 判一条路径上有没有东西。
//
// 任何 stat 失败在这里是同一件事：那份文件用不上。
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// deriveScriptFromFile 从会话 JSONL 推出主剧本，夹具不在时当场失败。
//
// 源: packages/test-support/llm-replay/src/index.ts:540-545
func deriveScriptFromFile(file string) ([]Entry, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("%w：%s——先把这个场景录一遍：%w", ErrFixtureNotFound, file, err)
	}
	events, err := ParseSessionLog(string(data))
	if err != nil {
		return nil, err
	}
	return DeriveScript(events)
}

// LoadSessionScripts 按绑定次序读出主剧本和那些子剧本。
//
// 源: packages/test-support/llm-replay/src/index.ts:607-646（loadSessionScripts）
//
// 子会话的推导从 seedLength 开始，于是继承过来的父分块绝不会被当成孩子自己的调用
// 回放一遍。
func LoadSessionScripts(config Config) ([]SessionScript, error) {
	entries, err := LoadScript(config)
	if err != nil {
		return nil, err
	}
	// 旁挂文件那条路换掉的是剧本、并不带头，所以头照旧从 JSONL 上读（它在的话），
	// 不在就用零值——于是一份只有旁挂文件、根本没有头的夹具仍旧排在最前面当主会话。
	var header session.SessionHeader
	if fileExists(config.File) {
		data, readErr := os.ReadFile(config.File)
		if readErr != nil {
			return nil, fmt.Errorf("%w：读不了 %s：%w", ErrFixtureNotFound, config.File, readErr)
		}
		header, err = ParseSessionHeader(string(data))
		if err != nil {
			return nil, err
		}
	}
	scripts := []SessionScript{{
		RecordedID: header.ID,
		CreatedAt:  header.CreatedAt,
		Entries:    entries,
		Primary:    true,
	}}

	var children []SessionScript
	for _, childFile := range config.ChildFiles {
		data, readErr := os.ReadFile(childFile)
		if readErr != nil {
			return nil, fmt.Errorf("%w：子会话夹具 %s 不在——把这个场景重录一遍：%w",
				ErrFixtureNotFound, childFile, readErr)
		}
		childHeader, headerErr := ParseSessionHeader(string(data))
		if headerErr != nil {
			return nil, headerErr
		}
		events, eventsErr := ParseSessionLog(string(data))
		if eventsErr != nil {
			return nil, eventsErr
		}
		// 只从孩子**自己**那些事件推：seed 边界**及其之后**的那些。
		if childHeader.SeedLength > len(events) {
			return nil, fmt.Errorf("%w：%s 的 seedLength %d 比它那 %d 条事件还长",
				ErrMalformedFixture, childFile, childHeader.SeedLength, len(events))
		}
		childEntries, deriveErr := DeriveScript(events[childHeader.SeedLength:])
		if deriveErr != nil {
			return nil, deriveErr
		}
		children = append(children, SessionScript{
			RecordedID: childHeader.ID,
			CreatedAt:  childHeader.CreatedAt,
			Entries:    childEntries,
		})
	}
	// 同步的孩子按建会话次序起；id 只用来把时间戳的平手定下来。
	// XXX(concurrent-subagents): 并发的孩子需要一个显式的「第几次调用」序数。
	slices.SortStableFunc(children, func(left, right SessionScript) int {
		if left.CreatedAt != right.CreatedAt {
			return cmp.Compare(left.CreatedAt, right.CreatedAt)
		}
		return strings.Compare(string(left.RecordedID), string(right.RecordedID))
	})
	return append(scripts, children...), nil
}

// 环境变量名，和 DSH 那三个一字不差。
//
// 源: packages/test-support/llm-replay/src/index.ts:843-847
const (
	// EnvFile 是主夹具路径的环境变量。
	EnvFile = "DSH_SNAPSHOT_FILE"
	// EnvOverride 是旁挂文件路径的环境变量。
	EnvOverride = "DSH_SNAPSHOT_OVERRIDE"
	// EnvChildFiles 是子会话日志清单的环境变量，用本平台的路径分隔符隔开。
	EnvChildFiles = "DSH_SNAPSHOT_CHILD_FILES"
)

// ResolveFromEnv 把一份配置里没给的字段从 DSH_SNAPSHOT_* 补上，并验一遍模态。
//
// 源: packages/test-support/llm-replay/src/index.ts:968-985（apply）
//
// 新增: DSH 那边这一步是 cordis 插件的 `apply`——它拿到 schemastery 验过的插件配置、
// 补上环境变量的默认、然后自己调 installLlmReplay。Go 没有装载器，所以补默认这件事
// 成了一个收一份 [Config]、交回补好的同一个类型的纯函数，装配方拿它的结果去调
// [Install]。分成两步，是为了让一个装配方能够完全不碰环境变量。
func ResolveFromEnv(config Config) (Config, error) {
	if config.File == "" {
		config.File = os.Getenv(EnvFile)
	}
	if config.File == "" {
		return Config{}, fmt.Errorf("%w：得给一个夹具路径（Config.File 或者 $%s）", ErrInvalidConfig, EnvFile)
	}
	if err := validateConfiguredModalities(config.Providers); err != nil {
		return Config{}, err
	}
	if config.Pace < 0 {
		return Config{}, fmt.Errorf("%w：Pace 不许是负数，实际 %s", ErrInvalidConfig, config.Pace)
	}
	if config.OverrideFile == "" {
		config.OverrideFile = os.Getenv(EnvOverride)
	}
	if config.ChildFiles == nil {
		if listed := os.Getenv(EnvChildFiles); listed != "" {
			config.ChildFiles = filepath.SplitList(listed)
		}
	}
	return config, nil
}

// validateConfiguredModalities 验目录里声明出来的那些模态。
//
// 源: packages/test-support/llm-replay/src/index.ts:824-840
//
// 新增: DSH 那道检查同时要挡住「根本不是数组」，因为它那份配置从 JSON 进来、
// 类型系统管不着。Go 这边字段类型就是 []ModelModality，只剩「值认不认识」要验。
func validateConfiguredModalities(providers []ProviderConfig) error {
	for _, provider := range providers {
		for _, model := range provider.Models {
			for _, modality := range model.InputModalities {
				if modality == llm.ModalityText || modality == llm.ModalityImage {
					continue
				}
				return fmt.Errorf("%w：提供方 %q 的模型 %q 的 inputModalities 只能是 text 和 image，实际有 %q",
					ErrInvalidConfig, provider.ID, model.ID, modality)
			}
		}
	}
	return nil
}
