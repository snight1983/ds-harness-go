// Package subagenttool 是面向模型的那一件派发工具：把一件活交给**一个**点名的
// 子 agent 提供方。
//
// 源: packages/subagent/tool-subagent/src/index.ts
//
// 这个包自己不跑任何孩子。它做的是三件事：
//
//   - 跟着提供方的在与不在装上、摘掉那件工具；
//   - 按提供方那句「孩子看不看得到父的历史」改写给模型的措辞；
//   - 按配置把一次调用送上前台、一次性后台、或者可续后台这三条路里的一条。
//
// 前台调用收完结果**一定**处置这次运行。后台那条路由配置选：一次性走一件普通的
// 后台作业，可续走 [ds-harness-go/subagent/subagent.Runtime.StartContinuable]
// 并当场把那个耐久的孩子 id 交回去。
//
// # 一个装配挂一个提供方
//
// [Config.Provider] 是点名的，不是「随便找一个」。要同时露出 spawn 和 fork 两条路
// 就装两份，各自给一个不同的 [Config.ToolName]——那也是为什么重名会在装的时候
// 大声失败，而不是等到第一次派发。
//
// # 措辞跟着提供方走
//
// 一个 fork 出来的孩子已经看得见这段对话里那些已完成的回合，一个全新的孩子看不见。
// 对着一个 fork 说「它看不到这段对话，请把话说全」是**假话**，会让模型白白重述一遍
// 它其实已经知道的东西。所以那句措辞由
// [ds-harness-go/subagent/subagent.Provider.InheritsParentContext] 决定，
// 在装的那一刻定下来。
//
// # 新增
//
// 相对 DSH 有四处刻意的偏离，各自在源码里写了理由：
//
//   - cordis 的 `ctx.tools` / `ctx.subagents` / `ctx.systemPrompt` 注入换成
//     显式交进来的 [Deps]；缺谁编译期或者装的那一刻就知道，不必等运行期。
//   - `maxDepth: number | 'provider-managed'` 那个联合换成 [Config.MaxDepth]
//     加 [Config.ProviderManagedDepth]，理由见那两个字段自己的说明。
//   - `AggregateError` 换成 [errors.Join]。
//   - `ctx.get('jobs')` 那次可选取用换成 [Deps.Jobs]：不给它，一次性后台那条路
//     照样报同一句话，而前台和可续两条路不受影响。
package subagenttool
