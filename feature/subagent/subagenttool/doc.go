// Package subagenttool 是面向模型的那一件派发工具：把一件活交给**一个**点名的
// 子 agent 提供方。
//
// 源: packages/subagent/tool-subagent/src/index.ts
//
// 这个包自己不跑任何孩子。它做的是四件事：
//
//   - 跟着提供方的在与不在装上、摘掉那件工具；
//   - 按提供方那句「孩子看不看得到父的历史」改写给模型的措辞；
//   - 按配置把一次调用送上前台、一次性后台、或者可续后台这三条路里的一条；
//   - 开着 [Config.ModelSelectionSettings] 时，在一张宿主授权的路由表之内让模型
//     自己挑孩子跑在哪条 LLM 路由上。
//
// 前台调用收完结果**一定**处置这次运行。后台那条路由配置选：一次性走一件普通的
// 后台作业，可续走 [github.com/snight1983/ds-harness-go/feature/subagent.Runtime.StartContinuable]
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
// [github.com/snight1983/ds-harness-go/feature/subagent.Provider.InheritsParentContext] 决定，
// 在装的那一刻定下来。
//
// # 谁许孩子换路由
//
// [Config.ModelSelectionSettings] 关着的时候，孩子那条 LLM 路由由 [Config.AgentOptions]
// 在装配那一刻写死，工具 schema 里没有 provider、model、reasoning_effort 这三个参数。
// 开着的时候多出一张**授权表**：模型只能在表里那些确切的提供方／模型对之间挑，
// 挑之前可以用 [ListModelsToolName] 那件只读工具问一层一层的路由和推理档位。
//
// 那张表的来路只有三条，按先后顺序：这条会话自己记过的那一份最权威（一条恢复回来的
// 会话跑的是它当初被授的表，不是此刻的用户设置）；没记过而它是个孩子，就跟它父那份走；
// 两样都不是、且这条日志一条继承来的事件都没有，才去读
// [ModelSelectionService] 那份用户设置。挑中的那一份当场以
// [EventModelSelectionPolicy] 记进会话日志，**写一次就定了**。
//
// 装配方要自己把 [RegisterModelSelectionProjection] 登进投影注册表、把
// [ModelSelectionEventTypes] 并进那份事件词汇表——本包不代劳，理由是一套部署可能装
// 好几件派发工具，而同一张表上登两遍同一个键是错。
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
//
// # 不做什么
//
//   - **不跑孩子。**开工、等待、取消、结算全在
//     [github.com/snight1983/ds-harness-go/feature/subagent] 的运行时；本包只把一次调用送上前台、
//     一次性后台、可续后台这三条路里的一条。
//   - **不让模型挑子 agent 提供方。**[Config.Provider] 在装配那一刻就点名了；模型能挑的
//     只有孩子那条 LLM 路由，而且要开着 [Config.ModelSelectionSettings]、并落在那张
//     授权表之内。
//   - **不自己定那张授权表。**表来自 [ModelSelectionService] 那份用户设置、或者父会话
//     那一份；本包只负责挑一份、记一次、然后按它拒绝越界的挑选。
//   - **不校验目录成员资格。**一个不在 [ListModelsToolName] 清单里的模型 id 照样可能被
//     适配器收下——那份清单是参考性的，见
//     [github.com/snight1983/ds-harness-go/llm.ModelInfo]。真正的门是那张授权表。
//   - **不实现后台作业。**一次性后台那条路把活交给
//     [github.com/snight1983/ds-harness-go/feature/jobs]；排队、并发和取消都是那一包的规矩。
//   - **不管派完之后的事。**可续那条路当场把耐久的孩子 id 交回去，后续的投递、打断和
//     列举归 [github.com/snight1983/ds-harness-go/feature/subagent/controltool]。
package subagenttool
