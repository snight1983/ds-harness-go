// 本文件的作用：预设这套词汇——一份预设是什么、根是什么、信任从哪来，
// 以及「点了个不存在的名字」和「点了个装不起来的」为什么必须是两种错。
//
// 源: packages/preset/agent-presets/src/preset.ts

package agentpresets

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/snight1983/ds-harness-go/fs"
)

// Trust 记的是一份预设的组合从哪来。
//
// 源: packages/preset/agent-presets/src/preset.ts:8
//
// [TrustSystem] 是随部署发出去的；[TrustUser] 是本地创作的——人写的也好、agent
// 写的也好——所以它和 shell 访问是同一个量级的信任。
type Trust string

const (
	// TrustSystem 表示这份组合随部署一起发出来。
	TrustSystem Trust = "system"
	// TrustUser 表示这份组合是本地创作的。
	TrustUser Trust = "user"
)

// presetID 是一个预设目录名允许的形状。
//
// 源: packages/preset/agent-presets/src/preset.ts:18
//
// id 会变成一段路径，所以这是一道**围栏**而不是风格规矩：`..`、一个分隔符、
// 或者一个看起来像绝对路径的名字，都会把组合放到部署授权的那个根之外去。
// 发现阶段共用它：一个名字没有任何副本能取到的目录，不是一个预设槽位。
var presetID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// PresetIDPattern 是 [presetID] 那条文法的源文本，给诊断和文档用。
//
// 新增: DSH 直接导出那个 RegExp 对象，错误消息里 `String(PRESET_ID)` 把它印出来。
// Go 这边导出一个 *regexp.Regexp 会让调用方能拿它去匹配别的东西，而这条文法只在
// [IsPresetID] 这一个判据里成立，所以只导出文本。
const PresetIDPattern = `^[a-z0-9][a-z0-9-]*$`

// IsPresetID 判一个名字能不能当预设 id 用。
func IsPresetID(id string) bool { return presetID.MatchString(id) }

// Preset 是一个带着可装载组合的预设目录。
//
// 源: packages/preset/agent-presets/src/preset.ts:20-41（AgentPreset）
type Preset struct {
	// ID 是稳定标识，也就是这个预设目录的名字。
	ID string
	// Trust 是它被发现时所在的那个根记下的信任。
	Trust Trust
	// Path 是它那份组合文件的绝对路径。
	Path string
	// Name 是它自己的展示名；空串表示回落到 [Preset.ID]。
	Name string
	// Description 是它自己发布的那一句「这是干什么用的」。
	Description string
	// Order 是它在同组里声明的位置；没声明的排在声明了的后面。
	//
	// 新增: DSH 是可选的 number，缺席与 0 分得开。Go 的零值分不出来，而 0 是一个
	// 有意义的位次，所以这里用指针。nil 就是「没声明」。
	Order *float64
	// Broken 说的是这份预设为什么组不出会话，空串表示它能。
	//
	// 一份坏掉的预设**留在名单上**——藏起来的话它那个目录仍旧占着 id，而外面看不到
	// 任何可删的东西——但每一条装载路径都在最前面拿这句话拒了它，而不是让它掉进
	// 装载器深处才炸。
	Broken string
}

// DisplayName 是这份预设在界面上的名字：它自己发布的那个，没有就用 id。
func (p Preset) DisplayName() string {
	if p.Name == "" {
		return p.ID
	}
	return p.Name
}

// Root 是一个会被扫出预设子目录的目录。
//
// 源: packages/preset/agent-presets/src/preset.ts:43-49（PresetRoot）
type Root struct {
	// Path 是一个绝对目录，里面一个子目录一份预设。
	//
	// 新增: DSH 这里允许开头一个 `~`，由 dsh-home-paths 展开。那个包 OUT_OF_SCOPE
	// （理由见包文档），所以这里要求装配方给一条已经展开好的绝对路径。
	Path string
	// Trust 是在这个根下发现的每一份预设继承的信任。
	Trust Trust
}

// Config 是这套名册的配置：默认点哪一份、预设住在哪些地方。
//
// 源: packages/preset/agent-presets/src/preset.ts:52-62
type Config struct {
	// FileSystem 是预设内容住的地方；必填，理由见 content.go。
	//
	// 新增: DSH 那边没有这个字段——它就地 `node:fs`。这里必须由装配方交进来，
	// 因为一份预设住在哪儿是**部署**的事：服务化的接
	// [github.com/snight1983/ds-harness-go/fs/objectstore.Store]，将来挂外接硬盘就接
	// 一个本地后端，而本包两种都不认识——它只认这一个接口读得出什么、写得进什么。
	FileSystem fs.FileSystem
	// Default 是调用方没点名字时装的那个 id。装的时候找不到会当场炸。
	Default string
	// Roots 是按优先级排的那些根；靠前的根赢下重名的 id。
	Roots []Root
	// UserRoot 是本地创作出来的预设去的那个目录，附在 [Config.Roots] **之后**。
	//
	// 新增: DSH 那里是一个 bool（includeUserRoot），路径由 dsh-home-paths 算。
	// 这里换成一条由装配方给的绝对目录，空串表示这套部署不给本地创作留位置——
	// 那时 [Roster.Authorable] 为假，除非 Roots 里本来就有一个 user 根。
	UserRoot string

	// Composers 是宿主在编译期登记进来的那些组装器，组合清单里的一行按名字取一个。
	//
	// 新增: 取代 cordis 那台动态模块装载器，理由见包文档。留 nil 表示这套部署
	// 装不了任何一行——名册照样发现、照样读写，但每一次装载都会失败。
	Composers ComposerSet
}

// resolvedRoots 是发现和创作真正会扫的那些根：配置里的每一个按顺序，然后是用户根。
//
// 源: packages/preset/agent-presets/src/index.ts:96-105
//
// 只算一次。一组根如果在 List() 和照着它答案走的 Copy() 之间变了，创作就会写进
// 一个调用方从没见过的目录。**追加**而不是前插，是为了让靠前的配置根继续赢下重名，
// 于是一份发出去的预设仍旧遮蔽一个占了它名字的本地目录。
func (c Config) resolvedRoots() []Root {
	roots := make([]Root, 0, len(c.Roots)+1)
	roots = append(roots, c.Roots...)
	if c.UserRoot != "" {
		roots = append(roots, Root{Path: c.UserRoot, Trust: TrustUser})
	}
	return roots
}

// ErrUnknownPreset 是「配置里没有一个根供得出这个 id」。
//
// 源: packages/preset/agent-presets/src/index.ts:333-356（Roster.resolve）
//
// 上移: 上游没有这个词汇里的错误类型，它把「点了个不存在的名字」表达成 resolve()
// 里现场抛的一个带 `agent-preset/not-found` 码的 RemoteError。Go 没有那套跨进程
// 错误码，分类只能靠哨兵，所以它连同下面那个 [ErrPresetMount] 一起落在本文件的
// 词汇里——错误消息文本逐字照抄上游那一条。
//
// 和装载失败分开，因为这两件事对调用方意思不同：一个不认识的 id 是一次坏请求，
// 而一份用不了的组合是一份部署必须去修的坏预设。
var ErrUnknownPreset = errors.New("agent-presets: 名册里没有这份预设")

// UnknownPresetError 带上被点的那个 id 和名册确实供得出的那些。
type UnknownPresetError struct {
	// PresetID 是被请求的那个 id。
	PresetID string
	// Available 是名册确实供得出的那些 id，给调用方拿去建议。
	Available []string
}

func (e *UnknownPresetError) Error() string {
	available := strings.Join(e.Available, ", ")
	if available == "" {
		available = "none"
	}
	return fmt.Sprintf("agent-presets: preset %q not found (available: %s)", e.PresetID, available)
}

func (e *UnknownPresetError) Unwrap() error { return ErrUnknownPreset }

// ErrPresetMount 是「预设在，但它那份组合装不起来」。
//
// 源: packages/preset/agent-presets/src/mount.ts:425-431
//
// 上移: 同 [ErrUnknownPreset]，上游是抛 `agent-preset/invalid` 码的 RemoteError，
// 没有一个具名类型。上面引的是带 cause 的那一处（对应 [PresetMountError.Cause]）；
// 另一处在 index.ts:358-378（Roster.resolveMountable），那里拿发现阶段报的
// broken 理由提前拒掉，不带 cause。两处共用同一个码，Go 这边也共用这一个哨兵。
var ErrPresetMount = errors.New("agent-presets: 这份预设装不起来")

// PresetMountError 带上是哪一份、为什么，以及底下那个错。
type PresetMountError struct {
	// PresetID 是那份组合失败了的预设。
	PresetID string
	// Reason 是失败的原因，不带本包自己那截前缀。
	Reason string
	// Cause 是底下那个错，可以是 nil。
	Cause error
}

func (e *PresetMountError) Error() string {
	return fmt.Sprintf("agent-presets: preset %q failed to mount: %s", e.PresetID, e.Reason)
}

// Unwrap 交出两个：一个是分类用的哨兵，一个是底下那个错。
//
// 新增: Go 1.20 起 Unwrap() []error 让 errors.Is 同时认得上面那层分类和底下那个
// 具体原因。DSH 那边靠 `instanceof` 加 `cause` 两件事分开表达。
func (e *PresetMountError) Unwrap() []error {
	if e.Cause == nil {
		return []error{ErrPresetMount}
	}
	return []error{ErrPresetMount, e.Cause}
}
