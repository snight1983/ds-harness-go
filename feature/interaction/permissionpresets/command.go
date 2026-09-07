// 本文件的作用：`/permission` 这条面向用户的命令——它怎么分叉、三种结局各回哪句话。
//
// 源: packages/interaction/permission-presets/src/index.ts:253-275

package permissionpresets

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/snight1983/ds-harness-go/feature/interaction/commands"
)

// CommandName 是这条命令登记的名字。
//
// 源: packages/interaction/permission-presets/src/index.ts:257
const CommandName = "permission"

// CommandDefinition 造 `/permission` 这条命令的定义。
//
// 源: packages/interaction/permission-presets/src/index.ts:256-274
//
// 这是网页端切预设走的那一条写路径：弹窗把用户挑的名字当成这一行提交上来。
//
// 新增: DSH 是构造函数里的 `ctx.inject(['commands'], ...)` 子节点——命令注册表在场
// 就登记。Go 这边「在不在场」由装配方决定叫不叫这个方法。[Config.LogOf] 没给时
// 它交回错误：这条命令的每一支都要读那个 agent 的会话日志，没有那条路它一句话都
// 答不出来。
//
// 说明文字里的「sandbox mode + 」跟着本包的旋钮一起去掉了，理由见包文档。
func (s *Service) CommandDefinition() (commands.Definition, error) {
	if s.logOf == nil {
		return commands.Definition{}, fmt.Errorf(
			"%w: 登记 /%s 需要一条从作用域钥匙找到会话日志的路", ErrInvalidConfig, CommandName)
	}
	return commands.Definition{
		Name:        CommandName,
		Description: "Switch the permission preset (approval policy)",
		Input:       &commands.InputDescriptor{Hint: "<preset>"},
		Handler:     s.runCommand,
	}, nil
}

// runCommand 跑一次 `/permission`。
//
// 源: packages/interaction/permission-presets/src/index.ts:263-273
//
// 空输入是一次查询，不是一次切换：这条命令是发现界面上唯一那条能问出「我现在是
// 哪个预设、还有哪些可选」的路。
//
// 三句回话里都不带命令名自己：渲染成 `名字 · 正文` 的那些界面（网页端那一行）
// 会读成 `permission · Permission preset: ...`。
func (s *Service) runCommand(_ context.Context, invocation commands.Invocation) (commands.Result, error) {
	if invocation.Agent == nil {
		return commands.Result{}, errors.New("permissionpresets: /permission 需要一个发起这条命令的 agent")
	}
	log, err := s.resolveLog(invocation.Agent)
	if err != nil {
		// 认不出这把钥匙不是用户能改的事（他敲的这条命令本身没毛病），是装配没接对，
		// 该一路抛给调用方而不是变成一条用户读的错误结果。
		return commands.Result{}, err
	}

	name := strings.TrimSpace(invocation.RawInput)
	if name == "" {
		return commands.Result{
			Kind: commands.ResultSuccess,
			Text: fmt.Sprintf("current preset %s (available: %s)",
				s.Current(log), strings.Join(s.Names(), ", ")),
		}, nil
	}
	if _, known := s.byName[name]; !known {
		// 一个打错的预设名是预期之内的失败，回错误结果而不是错误：日志上留下这次
		// 尝试，界面把这句话显示给用户，agent 循环不受影响。
		return commands.Result{
			Kind: commands.ResultError,
			Text: fmt.Sprintf("unknown preset %q (available: %s)", name, strings.Join(s.Names(), ", ")),
		}, nil
	}
	if err := s.SwitchFor(invocation.Agent, name); err != nil {
		return commands.Result{}, err
	}
	return commands.Result{Kind: commands.ResultSuccess, Text: "preset " + name}, nil
}
