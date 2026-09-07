// Package permissionpresets 把「权限旋钮」包成一张部署方自己起名的表，让用户按名字选，
// 而不是逐个去拨旋钮。
//
// 源: packages/interaction/permission-presets/src/index.ts:1-11
//
// 一次切换先把**选了哪个名字**记进日志（[EventPreset]），再把变了的旋钮通过它
// 各自那条正规写路径写下去。执行、给模型的那句陈述、以及回放，读的仍旧是旋钮自己
// 那一折——本包不在它们和旋钮之间插任何东西。读的那一面是 [ProjectionKey] 这个会话
// 投影，写的那一面是 [CommandName] 这条命令。
//
// # 为什么要单记一条「选了哪个名字」
//
// 两个预设可以捆同一份旋钮值。只看旋钮的话，这两个名字在界面上是分不开的，
// 用户选的那一下会被显示成另一个名字。[EventPreset] 是一条**只进日志、不进模型抄本**
// 的耐久事实，[Service.Current] 在旋钮仍然对得上的时候优先认它，用户那次选择因此保得住。
//
// # 这个 Go 版本只捆一个旋钮
//
// 新增: DSH 的 PresetSpec 捆两样——`sandbox`（沙箱模式）和 `approval`（审批策略），
// 而且它的构造函数在 `ctx.shell.sandboxMode === undefined` 时直接抛，因为「预设捆着
// 一个沙箱模式，把这个插件装在一个不约束的执行器上是配置错误」。
//
// 沙箱那一整支在本仓库的裁决里是范围外：sandbox/sandbox、sandbox/sandbox-local、
// sandbox/sandbox-policy、sandbox/sandbox-windows-acl、shell/bash-sandbox、
// shell/pwsh-sandbox 全部判为不需要。理由写在
// [github.com/snight1983/ds-harness-go/fs] 那里——一个恒返回「不限制」的能力位，
// 比没有这个位更容易让人误以为它在起作用。所以这里不留一个空的沙箱字段，
// [PresetSpec] 就只捆 [github.com/snight1983/ds-harness-go/feature/interaction/userapproval.Policy]
// 一样。
//
// 也没有把计划模式折进来当第二个旋钮：
// [github.com/snight1983/ds-harness-go/feature/plan/planmode] 那份包文档写明，沙箱模式和
// 审批策略各自独立地施加限制，它们**不读也不写**计划状态——DSH 是刻意把计划模式挡在
// 这个捆包外面的，跟着挡。
//
// 旋钮少了一个，这张表本身没有变得多余：它仍旧提供一份部署方起名、带标签和说明的
// 单子，一份钉进新会话的默认选择，一条在捆包打平手时保住用户意图的耐久事实，
// 一个界面读得到的投影，一条用户敲得到的命令，以及一条「日志里记着的名字必须还认得出来」
// 的检查。webhook 那一侧要的也正是这个可以拿名字指代的手柄。
//
// # 表本身归部署方
//
// 新增: DSH 那份 schema 给了两条默认项，`workspace-write`（workspace-write + ask）和
// `danger-full-access`（danger-full-access + never）——两个键都是**照沙箱模式起的名**。
// 沙箱那一支不在了，照抄这两个名字就是给用户看一个名叫「完全访问」、却根本不管文件
// 访问的选项。所以 Go 这边不带默认表，[Config.Presets] 必填。
//
// 新增: DSH 的表是 `Record<string, PresetSpec>`，顺序就是 `Object.keys` 的插入顺序，
// 而 `derive` 和界面选项都依赖这个顺序。Go 的 map 没有顺序，所以表是一张有序切片
// （[]Preset），名字排重在 [New] 里查。
//
// # 不做什么
//
//   - **不是一道闸。**本包一个权限判定都不做。它只是把旋钮值捆起来起个名，
//     真正拦人的是审批那一层自己。
//   - **不绕过旋钮自己的写路径。**变了的旋钮走
//     [github.com/snight1983/ds-harness-go/feature/interaction/userapproval.SetPolicy]
//     或者 [github.com/snight1983/ds-harness-go/feature/interaction/userapproval.Service.SwitchPolicy]，
//     本包不自己往日志里塞 approval/policy。
//   - **不管沙箱。**见上文，那一整支在本仓库范围外。
//   - **[CustomPreset] 不是一个可以切过去的目标。**旋钮值配不上任何一条表项时它作为
//     当前值出现，但它既不是切换目标，也永远不会被写进 [EventPreset]。
//   - **不自己找会话。**从一把 [github.com/snight1983/ds-harness-go/scope.Key] 到一条
//     会话日志的映射由装配方经 [Config.LogOf] 交进来。
//   - **不替装配方决定什么时候钉初值。**[Service.PinInitial] 是一个由装配方在会话
//     发布之前叫的方法，本包不订阅任何「会话建好了」的事件。
package permissionpresets
