// 本文件的作用：这个包要接哪几样东西才转得起来，以及那个把它们攥在手上的控制器。
//
// 源: packages/skill/tool-skill/src/index.ts:24-27,60-79

package skilltool

import (
	"context"
	"fmt"

	"github.com/snight1983/ds-harness-go/core/agent"
	"github.com/snight1983/ds-harness-go/core/scope"
	"github.com/snight1983/ds-harness-go/core/tools"
	"github.com/snight1983/ds-harness-go/skill"
)

// PackageName 是这个包在不变量注册表里的名字，和 DSH 的包名保持一致。
//
// 源: packages/skill/tool-skill/src/invariant.ts:10
const PackageName = "@deepseek-ai/dsh-tool-skill"

// CatalogPlugin 是目录消息那条来源盖的产出方名字。
//
// 源: packages/skill/tool-skill/src/index.ts:35（`kind: 'skill-catalog'`）
//
// 新增: DSH 靠 declare module 把 'skill-catalog' 挂进 llm 的 MessageSourceMap，
// 于是那条来源的 kind 就是它。Go 的 [github.com/snight1983/ds-harness-go/llm.MessageSource] 封在 llm 包里，
// 外面加不了变体，所以目录消息走 [github.com/snight1983/ds-harness-go/llm.PluginSource]，产出方名字取这一个。
// 做法和 [github.com/snight1983/ds-harness-go/compaction.CheckpointPlugin] 逐字相同。
//
// 它必须是常数：认出「这是本包发布的目录」全靠这个名字，一次改名会让日志里
// 已经发布过的那些目录再也认不出来，于是每个步骤都会重发一份。
const CatalogPlugin = "skill-catalog"

// ToolName 是那件读技能正文的工具的名字。
//
// 源: packages/skill/tool-skill/src/index.ts:82
const ToolName = "skill"

// DefaultCatalogDescriptionMaxLength 是目录里一行说明的默认长度上限。
//
// 源: packages/skill/tool-skill/src/index.ts:27
const DefaultCatalogDescriptionMaxLength = 500

// MinCatalogDescriptionMaxLength 是那个上限本身的下界。
//
// 源: packages/skill/tool-skill/src/index.ts:79（assertPositiveInteger 的第三个参数）
//
// 3 不是随手取的：超长的说明被截成「前 n-3 个字 + ...」，n 小于 3 时那个减法
// 会切出一段负长度。
const MinCatalogDescriptionMaxLength = 3

// Catalog 是本包用得到的那一小块技能注册表。
//
// 新增: DSH 直接注入整个 skills 服务。这里只写出真正被调到的三个方法，装配方
// 交进来的 [github.com/snight1983/ds-harness-go/skill.Registry] 自然满足它。窄口子让「本包到底看得见
// 什么」从签名上一眼可读，测试里也不必替身一整台注册表。
type Catalog interface {
	// List 列出一个视角看得见的技能摘要。
	List(ctx context.Context, options skill.ViewOptions) ([]skill.Summary, error)
	// Snapshot 观察当前目录，并说明这次发现是不是在一个稳定的 revision 里跑完的。
	Snapshot(ctx context.Context, options skill.ViewOptions) (skill.CatalogSnapshot, error)
	// Get 把一份技能读成完整正文，取不到交回 nil。
	Get(ctx context.Context, name string, options skill.ViewOptions) (*skill.Definition, error)
}

// ToolLookup 是本包用来问「这个 agent 解算到的 skill 工具是哪一个」的口子。
//
// 新增: DSH 是 `ctx.tools.get(name, agent)`。这里把它收窄成一个方法，理由和
// [Catalog] 相同；[github.com/snight1983/ds-harness-go/core/tools.Runtime] 自然满足它。
type ToolLookup interface {
	// Get 按名字解算一个作用域看得见的那件工具。
	Get(name string, key *scope.Key) (*tools.Definition, bool)
}

// Config 是这个包的装配面。
//
// 源: packages/skill/tool-skill/src/index.ts:61-69
type Config struct {
	// Skills 是那台技能注册表，必填。
	Skills Catalog
	// AgentOf 从一把作用域钥匙找到那个 agent，必填。
	//
	// 新增: DSH 的 exec.agent 就是 agent 对象本身。Go 这边它是一把不透明的钥匙，
	// 所以由装配方交一条查回去的路，做法和 [github.com/snight1983/ds-harness-go/plan/planmode.Config.AgentOf]
	// 逐字相同。
	AgentOf func(agent *scope.Key) (agent.Agent, error)
	// CatalogDescriptionMaxLength 是目录里一行说明的长度上限；
	// 零取 [DefaultCatalogDescriptionMaxLength]，非零时不得小于
	// [MinCatalogDescriptionMaxLength]。
	CatalogDescriptionMaxLength int
}

// Controller 是这三条通路共用的那点状态。
type Controller struct {
	skills                      Catalog
	agentOf                     func(agent *scope.Key) (agent.Agent, error)
	catalogDescriptionMaxLength int

	// definition 是本包登记的那件 skill 工具，在 [New] 里造好一次。
	//
	// 它必须是**同一个指针**从头用到尾：目录那条胳膊靠「解算出来的工具是不是
	// 正好是这一个」来决定发不发目录（见 [Controller.catalogPreStep]）。每次
	// 现造一份的话那个判等永远为假，目录就再也发不出去了。
	definition *tools.Definition

	// lookup 是登记时记下来的工具注册表，目录那条胳膊靠它做上面那次解算。
	lookup ToolLookup
}

// New 造一个控制器。
//
// 源: packages/skill/tool-skill/src/index.ts:77-79
func New(config Config) (*Controller, error) {
	switch {
	case config.Skills == nil:
		return nil, fmt.Errorf("skilltool: 需要一台技能注册表")
	case config.AgentOf == nil:
		return nil, fmt.Errorf("skilltool: 需要一条从作用域钥匙找回 agent 的路")
	}
	length := config.CatalogDescriptionMaxLength
	if length == 0 {
		length = DefaultCatalogDescriptionMaxLength
	}
	if length < MinCatalogDescriptionMaxLength {
		return nil, fmt.Errorf("skilltool: CatalogDescriptionMaxLength 不得小于 %d，拿到 %d",
			MinCatalogDescriptionMaxLength, length)
	}

	controller := &Controller{
		skills:                      config.Skills,
		agentOf:                     config.AgentOf,
		catalogDescriptionMaxLength: length,
	}
	controller.definition = controller.newDefinition()
	return controller, nil
}
