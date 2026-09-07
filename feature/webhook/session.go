// 本文件的作用：一条规则要了一个会话之后，那次有次序的创建事务——先把纯校验和
// 只读解析做完，再动介质；动介质那一段里任何一步失败，已经做过的按逆序退回去。
//
// 源: packages/webhook/webhook/src/session.ts

package webhook

import (
	"context"
	"fmt"
	"strings"

	"github.com/snight1983/ds-harness-go/harness/agent"
	"github.com/snight1983/ds-harness-go/llm"
	"github.com/snight1983/ds-harness-go/scope"
)

// resolvedRequest 是一份验过、也把模型那一段算完了的会话请求。
//
// 源: packages/webhook/webhook/src/session.ts:18-31（ResolvedWebhookSessionRequest）
type resolvedRequest struct {
	workspacePath    string
	title            string
	prompt           string
	agentPreset      string
	permissionPreset string
	selection        agent.ModelSelection
	options          agent.Options
}

// resolveRequest 验一份规则交回来的会话请求，并定下这次用哪条模型路由。
//
// 源: packages/webhook/webhook/src/session.ts:43-84
//
// 新增: DSH 那边这一段大半在做运行期类型检查（是不是对象、是不是非空字符串、
// 是不是正安全整数），因为 `run` 的返回值来自它自己写的插件、TS 的类型在运行期
// 不拦人。Go 的结构体已经把形状定死了，所以这里只剩下三件类型系统表达不了的事：
// 五个必填字段非空、MaxTokens 不为负、以及缺席时的默认路由。
func (r *Runtime) resolveRequest(request SessionRequest) (resolvedRequest, error) {
	fields := []struct {
		name  string
		value string
	}{
		{"workspacePath", request.WorkspacePath},
		{"title", request.Title},
		{"prompt", request.Prompt},
		{"agentPreset", request.AgentPreset},
		{"permissionPreset", request.PermissionPreset},
	}
	for _, field := range fields {
		if strings.TrimSpace(field.value) == "" {
			return resolvedRequest{}, fmt.Errorf("%w：%s 不能是空的", ErrInvalidRequest, field.name)
		}
	}

	resolved := resolvedRequest{
		workspacePath:    request.WorkspacePath,
		title:            request.Title,
		prompt:           request.Prompt,
		agentPreset:      request.AgentPreset,
		permissionPreset: request.PermissionPreset,
	}

	if request.Model == nil {
		// 缺席时连推理档位一起继承这份部署的默认选择——DSH 那边
		// `modelSelection = { ...selected }` 也是整份复制。
		selected := r.config.DefaultModel.CurrentSelection()
		resolved.selection = selected
		resolved.options = agent.Options{Provider: selected.Provider, Model: selected.Model}
		return resolved, nil
	}

	route := *request.Model
	if strings.TrimSpace(route.Provider) == "" {
		return resolvedRequest{}, fmt.Errorf("%w：model.provider 不能是空的", ErrInvalidRequest)
	}
	if strings.TrimSpace(route.Model) == "" {
		return resolvedRequest{}, fmt.Errorf("%w：model.model 不能是空的", ErrInvalidRequest)
	}
	if route.MaxTokens < 0 {
		return resolvedRequest{}, fmt.Errorf("%w：model.maxTokens 不能是负数", ErrInvalidRequest)
	}
	// 显式挑了路由就不带推理档位：那是部署默认那一份自己的东西，套到另一个模型上
	// 未必还成立。这一条和 DSH 的 `modelSelection = { provider, model }` 一致。
	resolved.selection = agent.ModelSelection{Provider: route.Provider, Model: route.Model}
	resolved.options = agent.Options{
		Provider:  route.Provider,
		Model:     route.Model,
		MaxTokens: route.MaxTokens,
	}
	return resolved, nil
}

// createSession 建、挂、起名、配好并放行一个普通的根会话。
//
// 源: packages/webhook/webhook/src/session.ts:120-182
//
// 提示词放行成功之后本包对这次操作的所有权就结束了：那个 agent 归 [Config.Owner]
// 管，此后和任何别的会话一样。
func (r *Runtime) createSession(
	ctx context.Context,
	delivery VerifiedDelivery,
	ruleID RuleID,
	request SessionRequest,
) error {
	resolved, err := r.resolveRequest(request)
	if err != nil {
		return err
	}
	// 先把三件只读的事做完，一个字节的介质都不碰：档位名认不认识、预设在不在、
	// 那份常驻组合装不装得起来。它们任何一件不成立，下面那串写就一步都不该开始。
	if _, err := r.config.Permissions.Resolve(resolved.permissionPreset); err != nil {
		return err
	}
	preset, err := r.config.AgentPresets.Resolve(ctx, resolved.agentPreset)
	if err != nil {
		return err
	}
	if _, err := r.config.AgentPresets.StandingKeyFor(ctx, preset.ID); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}

	// 标题给空串：工作区自己会回落到路径的最后一段。DSH 那边
	// `create(workspacePath)` 只有一个参数，是同一件事。
	workspace, err := r.config.Workspaces.Create(ctx, resolved.workspacePath, "")
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}

	sessionID := r.config.NewSessionID()
	handle, err := r.config.Agents.Create(ctx, r.config.Owner, agent.CreateOptions{
		SessionID:    sessionID,
		WorkspaceID:  workspace.ID(),
		AgentPreset:  preset.ID,
		AgentOptions: resolved.options,
		Setup: func(setupCtx context.Context, agentScope *scope.Scope) (func() error, error) {
			if _, err := r.config.AgentPresets.Mount(setupCtx, agentScope.Key(), preset.ID); err != nil {
				return nil, err
			}
			if err := r.installInitialSelection(setupCtx, agentScope, resolved.selection); err != nil {
				return nil, err
			}
			return nil, nil
		},
	})
	if err != nil {
		return err
	}

	attached := false
	err = func() error {
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}
		if err := workspace.AttachSession(ctx, sessionID); err != nil {
			return err
		}
		attached = true
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}
		if err := r.config.Permissions.SetFor(handle.Agent.Scope().Key(), resolved.permissionPreset); err != nil {
			return err
		}
		if err := r.config.Rename(handle.Agent, resolved.title); err != nil {
			return err
		}
		source, err := NewSource(Provenance{
			Provider:   delivery.Kind,
			Source:     delivery.Source,
			DeliveryID: delivery.DeliveryID,
			RuleID:     ruleID,
		}, fmt.Sprintf("%s webhook handled by %s", delivery.Kind, ruleID))
		if err != nil {
			return err
		}
		handle.Agent.Followup(llm.NewUserMessage(llm.Content{llm.TextBlock{Text: resolved.prompt}}, source))
		return nil
	}()
	if err == nil {
		return nil
	}

	// 逆序退回去。回滚本身再失败也不顶掉原来那个错，只另报一条——那个错才是这次
	// 操作真正的死因，把它换成「回滚失败」会让排查从第二现场开始。
	// 新增: 回滚不继承已经触发的投递取消，否则最需要清理时两步都会立即失败。
	cleanupCtx := context.WithoutCancel(ctx)
	if attached {
		if rollbackErr := workspace.DetachSession(cleanupCtx, sessionID); rollbackErr != nil {
			r.reportRollback(delivery, ruleID, fmt.Sprintf("会话 %q 的工作区摘除", sessionID), rollbackErr)
		}
	}
	if rollbackErr := handle.Dispose(cleanupCtx); rollbackErr != nil {
		r.reportRollback(delivery, ruleID, fmt.Sprintf("会话 %q 的 agent 处置", sessionID), rollbackErr)
	}
	return err
}

// reportRollback 另报一条回滚失败。
//
// 源: packages/webhook/webhook/src/session.ts:87-89
func (r *Runtime) reportRollback(delivery VerifiedDelivery, ruleID RuleID, subject string, err error) {
	r.config.Report(Failure{
		Kind:       delivery.Kind,
		Source:     delivery.Source,
		DeliveryID: delivery.DeliveryID,
		RuleID:     ruleID,
		Rollback:   subject,
		Err:        err,
	})
}

// installInitialSelection 让创建那一刻挑的推理档位一直生效，直到这条会话有了它
// 第一份耐久的请求头。
//
// 源: packages/webhook/webhook/src/session.ts:92-107
//
// 提供方和模型已经由 [agent.CreateOptions].AgentOptions 定死了，这条观察者补的只有
// 推理档位——那一项不在 [agent.Options] 上。第一份请求头落下之后，会话自己记的那份
// 就是权威，这条观察者从此原样放行。
//
// 新增: DSH 读的是 `agentCtx.agent`（作用域上那个 agent），Go 读的是
// [agent.Request].Agent（发这次请求的那一个）。两者只在「这个 agent 的子孙发请求」
// 时才可能不同，而子 agent 只能由一次工具调用生出来，那必然在第一次模型应答**之后**
// ——那时请求头早就落下了，两条路都是原样放行。
func (r *Runtime) installInitialSelection(
	ctx context.Context,
	agentScope *scope.Scope,
	selection agent.ModelSelection,
) error {
	_, err := r.config.Agents.OnRequest(ctx, agentScope, func(
		ctx context.Context,
		request agent.Request,
		next func(context.Context) (llm.CallConfig, error),
	) (llm.CallConfig, error) {
		resolved, err := next(ctx)
		if err != nil {
			return llm.CallConfig{}, err
		}
		if resolved.Provider != selection.Provider || resolved.Model != selection.Model {
			return resolved, nil
		}
		_, pinned, err := request.Agent.Session().RequestHeader()
		if err != nil {
			return llm.CallConfig{}, err
		}
		if pinned {
			return resolved, nil
		}
		// 空串就是「没选档位」，所以直接赋值一次同时是 DSH 那两支（先删掉继承来的，
		// 再只在选中的那一份带档位时加回去）。
		resolved.ReasoningEffort = selection.ReasoningEffort
		return resolved, nil
	})
	// 撤销函数丢掉不管：这条登记挂在 agentScope 上，跟着那个 agent 一起散。
	return err
}
