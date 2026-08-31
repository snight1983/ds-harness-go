// 本文件的作用：这一行的全部内容——它贡献的那段人设配置，以及把它装到一个提示词
// 注册表上的那一次登记。
//
// 源: packages/preset/persona/src/index.ts:34-68

package persona

import (
	"context"
	"errors"

	"ds-harness-go/core/scope"
	"ds-harness-go/core/systemprompt"
)

// Config 是这一行贡献的人设。
//
// 源: packages/preset/persona/src/index.ts:34-52
//
// 新增: DSH 那边还有一个同名的 schemastery 运行期 schema（`export const Config`），
// 负责校验和填默认值。Go 里结构体本身就是那份 schema，两个可选字段的默认值由零值
// 兑现，所以那个导出不移。
type Config struct {
	// Text 是渲染成 `deployment:persona` 那一段的人设正文。
	//
	// 它是一份模板：完整的 `{{...}}` 组会严格地按已注册的提示词变量插值（写错了名字
	// 就报错，不会悄悄留成原文）。正文是空串是**合法**的——空段落在渲染时被丢掉，
	// 和注册表本身对一份空人设的处理一样。
	Text string

	// Complete 把这段人设当作**整份**系统提示词，压掉其他所有段落。
	//
	// 源: packages/preset/persona/src/index.ts:41-42
	Complete bool

	// SuppressRuntimeContext 让这个 agent 作用域不要动态运行期上下文快照。
	//
	// 新增: DSH 是 `includeRuntimeContext?: boolean` 默认 true。Go 的零值是 false，
	// 所以名字取反——一个什么都没填的 Config 说的就是 DSH 那个默认行为。
	SuppressRuntimeContext bool
}

// Install 把这一行装到一个提示词注册表上，返回撤销它的函数。
//
// 源: packages/preset/persona/src/index.ts:60-68
//
// owner 决定这份人设算谁的：一个有身份的 agent 作用域会**遮蔽**掉部署方那份人设；
// 而注册表持有的那一层上已经有一份同名登记了，装在那里是同一层里重名，当场报错。
// 这是 DSH 那边一模一样的行为，两边都不必自己写这道检查——它是注册表分层规矩的
// 直接结果。
//
// 装失败的时候什么都不留下：这两次登记要么都成，要么一次都不算数。撤销函数按登记
// 的逆序撤。
//
// 新增: DSH 是先登记人设、再压制；这里反过来，先压制、再登记人设。理由是**失败要
// 干净**：人设那一次登记是会失败的（撞上注册表自己那份同名登记），压制那一次不会，
// 所以只有这个次序下「前一次成了、后一次砸了」才是真会发生的事，也才有办法把它撤
// 回去。DSH 不需要考虑这件事——它那两次登记都挂在 cordis 的 ctx 上，ctx 一没两边
// 一起没。两个次序装成功之后的结果一模一样。
func Install(
	ctx context.Context,
	registry *systemprompt.Registry,
	owner *scope.Scope,
	config Config,
) (func(context.Context) error, error) {
	if registry == nil {
		return nil, errors.New("persona: 需要一个提示词注册表")
	}

	var releaseSuppression func(context.Context) error
	if config.SuppressRuntimeContext {
		release, err := registry.SuppressRuntimeContext(ctx, owner)
		if err != nil {
			return nil, err
		}
		releaseSuppression = release
	}

	removeSection, err := registry.Section(ctx, owner, systemprompt.PromptSection{
		Name:     systemprompt.PersonaSection,
		Order:    systemprompt.PersonaOrder,
		Text:     systemprompt.StaticText(config.Text),
		Complete: config.Complete,
	})
	if err != nil {
		if releaseSuppression != nil {
			// 压制已经生效了，这一步砸了就得把它放开——否则调用方拿到的是一个错误，
			// 外加一份已经生效、却再也没人能撤销的压制。放开本身要是也砸了，两条
			// 都交出去：吞掉后一条会让「压制还在」这件事从诊断里整个消失。
			err = errors.Join(err, releaseSuppression(ctx))
		}
		return nil, err
	}

	if releaseSuppression == nil {
		return removeSection, nil
	}
	return func(undoCtx context.Context) error {
		return errors.Join(removeSection(undoCtx), releaseSuppression(undoCtx))
	}, nil
}
