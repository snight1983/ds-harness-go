# 移植审查发现

由 `go run ./internal/devtools/portcheck -mode audit` 生成，**不要手工编辑**——它每次会被整份覆盖。

这五份都不是判决，是**工作队列**：每一条要么改代码、要么在裁决表的 `note` 列写明为什么它是对的。

裁决表 11194 行，其中 PORTED 且填了 `go_ref` 的 1569 条。

## 一、kind 对不上（最强信号）

上游的声明类别和 `go_ref` 指向的 Go 声明类别不一致。四份里唯一接近「一定填错了」的一份。

| 上游包 | 上游符号 | go_ref | 问题 |
|---|---|---|---|
| acp/acp | `CreateAcpSessionOptions` | `acp.Bridge.NewSession` | 上游是 interface，Go 侧是 method（该是 type）（裁决表已有理由：新建一个 ACP 会话的构造输入（sessionId、cwd、mcpServers、agentOptions、fallbackSelection、signal、notify）。Go 侧没有这个打包结构：线上参数直接是第三方 SDK 的 wire.NewSessionRequest（github.com/coder/acp-go-sdk），由 acp.Bridge.NewSession（acp/acp/bridge.go:642）收下，validateSessionParams（:691）先拒掉 additionalDirectories 和 mcpServers 这两样本契约不支持的特性，再自己铸 sessionID 并组一个 agent.CreateOptions（:659-663）。四处不对等：mcpServers 上游是可用输入，Go 是硬拒；fallbackSelection 那条「先恢复日志里的路由、再退回部署配置」的选择在 Go 里不存在，provider/model 一律取 Bridge 配置里那一份；signal 变成第一个参数 ctx；notify 不是每次会话传进来的回调，而是桥自己的 Bridge.notify（:284）。） |
| acp/acp | `ResumeAcpSessionOptions` | `acp.Bridge.ResumeSession` | 上游是 interface，Go 侧是 method（该是 type）（裁决表已有理由：会话恢复后来补上了：Bridge.Initialize 声明 SessionCapabilities.Resume，Bridge.ResumeSession 读回已落盘的会话再交回句柄。原先记的「不声明 resume」已不成立，2026-09-04 改正。） |
| acp/acp | `apply` | `acp.Bridge` | 上游是 function，Go 侧是 type（该是 func/method）（裁决表已有理由：整座桥：五个协议方法、运行时那三条边、审批那条线、以及收摊那条次序敏感的路。DSH 那个 apply 里的闭包状态在 Go 里是 Bridge 的字段，装上去那一步是 acp.Bridge.Install。） |
| attachment/attachment | `ImageAdmissionErrorCode` | `attachment.imageAdmissionCodes` | 上游是 type，Go 侧是 var（该是 type）（裁决表已有理由：TS 里是那九个准入码的联合类型，只在编译期成立。Go 里对应的是一张集合，因为这组码真正的用途是 IsImageAdmissionError 在运行期查表分类，而不是约束某个字段的取值。） |
| attachment/attachment | `ImageAdmissionErrorCode` | `attachment.imageAdmissionCodes` | 上游是 reexport-type，Go 侧是 var（该是 type）（裁决表已有理由：桶文件转发，定义处见 src/error.ts:16。） |
| context/session-reference | `SessionReferenceResolver` | `sessionref.Install` | 上游是 class，Go 侧是 func（该是 type）（裁决表已有理由：服务在 NewResolver，接线在 Install；DSH 一个类兼两职，这里拆成两半） |
| core/session | `SurfaceEventType` | `sessionlog.IsSurfaceEligibleType` | 上游是 type，Go 侧是 func（该是 type）（裁决表已有理由：能上表面的那三个事件类型。DSH 用字面量子集表达它，于是编译期就能挡住「给一条边界事件挂 surfaceOp」；Go 的具名 string 类型分不出子集，这个子集在 Go 里是一个谓词，那条约束由 [sessionlog.SurfaceOpOf] 在读和写两侧各验一次——DSH 在运行期也验，Go 只是没有它那道编译期的第二层。） |
| core/session | `apply` | `sessionlog.Trace` | 上游是 const，Go 侧是 type（该是 const/var/func/method）（裁决表已有理由：DSH 的 apply 只做一件事：把 install 那份关系检查注册进 invariants 服务。检查本身在 Go 里是 [sessionlog.Trace] 这个不认识任何容器的普通值——回合与步骤的开合、seq 单调、在途调用的收尾，由 [sessionlog.ValidateLog] 折一遍整份日志或者 Trace.Validate 逐条推进。DSH 用 WeakMap 给活的 Session 挂旁路状态，Go 里谁拥有这个 Trace 谁自己拿着。把它挂上本仓库的不变量注册表是第 6 块的事。） |
| core/tools | `ObjectJsonSchema` | `tools.AssertObjectSchema` | 上游是 type，Go 侧是 func（该是 type）（裁决表已有理由：TS 那边是一个用来收窄的类型别名，Go 里没有类型收窄，同一件事由运行期断言 AssertObjectSchema 表达。） |
| llm/llm-pi-ai | `PiImageRequestContext` | `openaicompat.toContext` | 上游是 interface，Go 侧是 func（该是 type）（裁决表已有理由：把「确定性的请求图片」绑到一次工具执行世界上的那组输入。Go 侧没有这个打包结构，四个字段摊成 openaicompat.toContext（llm/openaicompat/context.go:473）的显式参数：attachments 对 attachment.Store（nil 选纯文本那一支，历史里有图就报 UNSUPPORTED_CONTENT）、maxRequestImageBytes 对 *int（nil 表示不设上限）、requestImagePolicy 对 attachment.RequestPolicy。第四个字段 resolveImageAccess 在 Go 里**一个对应物都没有**：上游那条线是 ImageAttachmentAccessResolver（packages/llm/llm/src/content.ts:19）把一份耐久图片引用解成当前工具执行世界里的一个只读路径，再由 requestImageHandleText（content.ts:88）把这个路径缀进给模型看的把手文本里，好让模型能用文件工具去读那份归一化后的原图。Go 的 llm.RequestImageHandleText（llm/image.go:49）只收 version 一个参数，输出恒为 "Image <id>; request preview WxHpx." 那一句的旧口径，既没有 access 分支也没有那句「可能被缩放或重编码」的免责。后果是模型只能看到内联进请求的那份缩略版本，拿不到原图的落盘位置，也就没法对它再做工具操作。要补的话入口是 llm.RequestImageHandleText 加一个可选的 access 参数，前置条件是先有一个从附件宿主路径映射到工具执行世界的接缝（attachment.Store 上目前没有 imageHostPath 这类方法）。） |
| preset/agent-presets | `mountPreset` | `agentpresets.Composer` | 上游是 function，Go 侧是 type（该是 func/method）（裁决表已有理由：本包唯一一处真正换掉的东西。DSH 靠 cordis Loader 在运行期按 YAML 里写的 npm 包名 import 并挂进 EntryTree；Go 静态链接，包名到实现的映射编译期就定死了。换成一张组装器名册（Composer / ComposerSet）：宿主在构建期登记具名安装器，YAML 的一行点名字、带一段 JSON 配置，mountComposition 照行装、失败按反序回滚。行的形状（name/group/disabled/config、组展开、禁用跳过）与 DSH 逐字一致。） |
| preset/agent-presets | `presetExists` | `agentpresets.PresetExistsError` | 上游是 function，Go 侧是 type（该是 func/method）（裁决表已有理由：上游是个类型守卫函数（判断一个错误是不是「这个 id 已经被占了」）；Go 侧是 errors.As 的目标类型加一个哨兵 ErrPresetExists（authoring.go:43-55），由 CreatePreset 在 authoring.go:221 抛出。判别方式换了（守卫函数换成 errors.As/errors.Is），能力是同一件。） |
| session-query/session-query | `SessionObservationReader` | `sessionquery.Corpus.Load` | 上游是 class，Go 侧是 method（该是 type）（裁决表已有理由：一次点观察：优先活会话，活的不在就退回持久化。上游那个类只为在构造函数里存一个 ctx，读这件事全在它的 read 方法上；Go 的 sessionquery.Corpus 本来就拿着同一批依赖，所以这个类没有对应物，能力落在 Corpus.Load（sessionquery/corpus.go:177）这个方法上——kind 从 class 变 method 是这个原因，不是填错格子。交出来的东西两边不一样：上游 read 返回一份 Disposable 的租约 SessionObservation，Go 返回一份脱离的拷贝 LogicalSession，那条取舍连同它的三条代价记在 src/observation.ts:14 那一行上；options 那个参数的去向记在 src/observation.ts:35 那一行。） |
| session/session-persistence | `SessionPersistenceNotFoundError` | `persistence.ErrSessionNotFound` | 上游是 class，Go 侧是 var（该是 type）（裁决表已有理由：Go 侧是哨兵错误 persistence.ErrSessionNotFound（session/persistence/error.go:21），判别用 errors.Is，成例见 isNotFound（coordinator_chain.go:351）。上游那个类把缺席的身份存在 sessionId 字段上；Go 的哨兵不带字段，身份由抛出点包进消息（coordinator_prepare.go:365 的 fmt.Errorf("%w: 会话 %q", ...)），所以身份仍在人眼能读的那一层，但拿不到结构化的 id。两边同样都把「没有这个存档」当成正常控制流而不是故障——backend.go:50 明确写了这一条。） |
| session/session-projection-cache | `checkpointIdentity` | `projectioncache.Identity` | 上游是 const，Go 侧是 type（该是 const/var/func/method）（裁决表已有理由：一条记录绑定的那段日志身份：同一个 id 底下区分两次生命周期的那几个不可变头字段。Go 里它是一个可比较的结构体，所以核对身份就是一次 ==。） |
| session/session-projection-cache | `checkpointRecord` | `projectioncache.Record` | 上游是 const，Go 侧是 type（该是 const/var/func/method）（裁决表已有理由：一个会话存下来的那条记录：绑定的日志身份加按投影键索引的检查点行。整条记录每次整块替换。） |
| session/session-projection-cache | `checkpointRow` | `projection.CheckpointRow` | 上游是 const，Go 侧是 type（该是 const/var/func/method）（裁决表已有理由：一行检查点的形状。它归投影注册表那一层（本包只是把它存下来），所以 Go 里类型就是 projection.CheckpointRow；zod 那几条取值范围（ver 非负、seq 不小于 -1）落在 projectioncache.ValidateRecord 里，写入前和读出后各验一次。） |
| session/session-title | `SessionTitleInvalidError` | `sessiontitle.ErrInvalidTitle` | 上游是 class，Go 侧是 var（该是 type）（裁决表已有理由：DSH 的自定义 Error 子类在 Go 里是一个哨兵值，调用方用 errors.Is 认它。） |
| storage/storage | `StorageForms` | `storage.FormAs` | 上游是 interface，Go 侧是 func（该是 type）（裁决表已有理由：上游是 export interface StorageForms {}——一个空的声明合并接入点：每个存储插件往它上面加一条字段，ctx.storage.domain 才带上类型。Go 没有声明合并，等价物是泛型自由函数 FormAs[T]，由调用方在解析时报出它要的类型。上游 interface 落成 Go func 是这个机制差异的直接结果，不是填错格子。） |
| subagent/subagent | `CoordinatorMessageSource` | `subagent.NewCoordinatorSource` | 上游是 interface，Go 侧是 func（该是 type）（裁决表已有理由：TS 是一个消息来源接口加它那些字段；Go 里来源统一是 llm.PluginSource，本包交出造它和读它的那两个函数（另一半是 SenderSessionIDOf）。） |
| subagent/subagent | `CoordinatorMessageSource` | `subagent.NewCoordinatorSource` | 上游是 reexport-type，Go 侧是 func（该是 type）（裁决表已有理由：桶文件转发，定义处见 src/continuation.ts:58。） |
| subagent/subagent | `SubagentError` | `subagent.NewError` | 上游是 class，Go 侧是 func（该是 type）（裁决表已有理由：本仓的带码失败统一是 llm.Error，NewError 是本包造它的那个口子。） |
| subagent/subagent | `SubagentReportMessageSource` | `subagent.NewReportSource` | 上游是 reexport-type，Go 侧是 func（该是 type）（裁决表已有理由：桶文件转发，定义处见 src/continuation.ts:67。） |
| subagent/subagent | `SubagentReportMessageSource` | `subagent.NewReportSource` | 上游是 interface，Go 侧是 func（该是 type）（裁决表已有理由：同 src/continuation.ts:58。） |
| subagent/subagent | `SubagentSettledMessageSource` | `subagent.NewSettledSource` | 上游是 interface，Go 侧是 func（该是 type）（裁决表已有理由：同 src/continuation.ts:58。） |
| subagent/subagent | `SubagentSettledMessageSource` | `subagent.NewSettledSource` | 上游是 reexport-type，Go 侧是 func（该是 type）（裁决表已有理由：桶文件转发，定义处见 src/continuation.ts:82。） |
| subagent/subagent-fork-in-process | `Config` | `forkinprocess.New` | 上游是 interface，Go 侧是 func（该是 type）（裁决表已有理由：只有 providerName 一个字段，Go 里它就是 New 的第一个形参；默认值改由 forkinprocess.DefaultProviderName 给。理由同 subagent/subagent-spawn-in-process 那条。） |
| subagent/subagent-spawn-in-process | `Config` | `spawninprocess.New` | 上游是 interface，Go 侧是 func（该是 type）（裁决表已有理由：只有 providerName 一个字段，Go 里它就是 New 的第一个形参。为一个字符串包一个结构体是 schemastery 逼出来的形状，不是 Go 的：那个默认值改由 spawninprocess.DefaultProviderName 给。） |
| test-support/llm-mock-server | `ConcreteMockLlmBehavior` | `mockserver.IsConcreteBehavior` | 上游是 type，Go 侧是 func（该是 type）（裁决表已有理由：TS 靠 Exclude 把 random 从类型里剔掉，Go 只有一种 Behavior，这条区分落成一个谓词，用在随机权重的校验上。） |
| workflow/workflow | `WorkflowError` | `workflow.NewError` | 上游是 class，Go 侧是 func（该是 type）（裁决表已有理由：上游是 HarnessError 的子类，派生出来只为把 name 改成 WorkflowError——那个字段在 JS 里是用来认错误来源的。Go 认错误靠 errors.Is / errors.As 加那个码，多派生一个类型反而会让上游那句 errors.As(err, &target)（target 是 *llm.Error）失配。所以这里不新造类型，构造函数直接交 llm.Error，身份由码承担；成例见 subagent.NewError。） |
| workflow/workflow | `WorkflowErrorCode` | `workflow.fatalCodes` | 上游是 type，Go 侧是 var（该是 type）（裁决表已有理由：十一个致命码的联合类型，只在 TS 编译期成立。Go 里码本身是 workflow.CodeScriptParse 那一组导出常量（取值逐条照抄，线上可见），而这个联合真正的用途是 workflow.IsFatal 在运行期判「该不该打断脚本」，所以落成一张集合。成例见 attachment.imageAdmissionCodes。集合不导出：判据的唯一入口是 IsFatal，把表也放出去等于让调用方能绕过它。） |
| workspace/workspace | `WorkspaceMoveInvalidError` | `workspace.CodeMoveInvalid` | 上游是 class，Go 侧是 const（该是 type）（裁决表已有理由：DSH 的三个具名错误类在 Go 里塌成 [workspace.Error] 上的一个分类码，用 errors.Is 分辨；见 workspace/error.go。） |
| workspace/workspace | `WorkspaceOrderInvalidError` | `workspace.CodeOrderInvalid` | 上游是 class，Go 侧是 const（该是 type）（裁决表已有理由：同 src/entity.ts:19：具名错误类塌成分类码。） |
| workspace/workspace | `WorkspaceUnknownSessionError` | `workspace.CodeUnknownSession` | 上游是 class，Go 侧是 const（该是 type）（裁决表已有理由：同 src/entity.ts:19：具名错误类塌成分类码。） |
| workspace/workspace | `realpathNormalize` | `fs.FileSystem` | 上游是 function，Go 侧是 type（该是 func/method）（裁决表已有理由：包内唯一的「同一性判据」，DSH 是一层 node:fs realpath 包装。Go 里这条判据换成 [fs.Target.TargetKey]——由 fs.FileSystem.Resolve 交出，因为远端后端的身份不是一条本地路径，而落盘的 TargetKey 跨进程稳定，比重新解析一遍更可靠。） |

## 二、go_ref 指向非导出符号

上游公开的能力，Go 侧下游调不到。合法的情形是「上游那个导出本来就只给它自己用」——但那句话必须写在裁决表的 `note` 列里，写不出来的就是真缺口。

| 上游包 | 上游符号 | go_ref | 说明 |
|---|---|---|---|
| attachment/attachment | `ImageAdmissionErrorCode` | `attachment.imageAdmissionCodes` | 非导出的一段：imageAdmissionCodes（裁决表已有理由：TS 里是那九个准入码的联合类型，只在编译期成立。Go 里对应的是一张集合，因为这组码真正的用途是 IsImageAdmissionError 在运行期查表分类，而不是约束某个字段的取值。） |
| attachment/attachment | `ImageAdmissionErrorCode` | `attachment.imageAdmissionCodes` | 非导出的一段：imageAdmissionCodes（裁决表已有理由：桶文件转发，定义处见 src/error.ts:16。） |
| context/session-reference | `stringifyTagSafeJson` | `sessionref.stringifyTagSafeJSON` | 非导出的一段：stringifyTagSafeJSON（裁决表已有理由：关掉 Go 自己的 HTML 转义，只做 DSH 做的那一件事，否则字节预算对不上） |
| goal/tool-goal | `completionAuthority` | `goaltool.Controller.completionAuthority` | 非导出的一段：completionAuthority（裁决表已有理由：同 requireDirectHuman：上游的 export 是 TS 跨文件可见性，不是公开面。Go 里挂成 Controller 的方法，因为它要读 Controller 上那份策略配置。） |
| goal/tool-goal | `goalToolExecution` | `goaltool.Controller.execution` | 非导出的一段：execution（裁决表已有理由：ctx.agents.currentInitiator() 换成挂在 ctx 上的 agent.CurrentInitiator。） |
| goal/tool-goal | `renderWrapupContext` | `goaltool.renderWrapupContext` | 非导出的一段：renderWrapupContext（裁决表已有理由：同 requireDirectHuman：上游的 export 是 TS 跨文件可见性。它是排收尾指令的纯函数，只被 tool.go 调用，不导出。） |
| goal/tool-goal | `requireDirectHuman` | `goaltool.Controller.requireDirectHuman` | 非导出的一段：requireDirectHuman（裁决表已有理由：上游 export 出来只为让 index.ts 跨文件调用，tool-goal 包外没有任何调用方（全仓 grep 确认）。Go 里包内跨文件天然共享符号，所以不导出。） |
| llm/llm-pi-ai | `PiImageRequestContext` | `openaicompat.toContext` | 非导出的一段：toContext（裁决表已有理由：把「确定性的请求图片」绑到一次工具执行世界上的那组输入。Go 侧没有这个打包结构，四个字段摊成 openaicompat.toContext（llm/openaicompat/context.go:473）的显式参数：attachments 对 attachment.Store（nil 选纯文本那一支，历史里有图就报 UNSUPPORTED_CONTENT）、maxRequestImageBytes 对 *int（nil 表示不设上限）、requestImagePolicy 对 attachment.RequestPolicy。第四个字段 resolveImageAccess 在 Go 里**一个对应物都没有**：上游那条线是 ImageAttachmentAccessResolver（packages/llm/llm/src/content.ts:19）把一份耐久图片引用解成当前工具执行世界里的一个只读路径，再由 requestImageHandleText（content.ts:88）把这个路径缀进给模型看的把手文本里，好让模型能用文件工具去读那份归一化后的原图。Go 的 llm.RequestImageHandleText（llm/image.go:49）只收 version 一个参数，输出恒为 "Image <id>; request preview WxHpx." 那一句的旧口径，既没有 access 分支也没有那句「可能被缩放或重编码」的免责。后果是模型只能看到内联进请求的那份缩略版本，拿不到原图的落盘位置，也就没法对它再做工具操作。要补的话入口是 llm.RequestImageHandleText 加一个可选的 access 参数，前置条件是先有一个从附件宿主路径映射到工具执行世界的接缝（attachment.Store 上目前没有 imageHostPath 这类方法）。） |
| llm/llm-pi-ai | `RouteCatalog` | `openaicompat.routeCatalog` | 非导出的一段：routeCatalog（裁决表已有理由：一条路由落定出来的模型表。DSH 把 configuredMaxTokens 单列成一张 map，因为 pi-ai 的 Model.maxTokens 说的是模型**能力**、而 harness 的 defaultMaxTokens 说的是这次部署愿意发的**请求上限**，两者不是一回事。这边同一个区分落在 ResolvedModel.MaxTokens 与 ResolvedProviderProfile.DefaultMaxTokens 上。） |
| llm/llm-pi-ai | `RouteCatalogRequest` | `openaicompat.routeCatalogRequest` | 非导出的一段：routeCatalogRequest（裁决表已有理由：模型落定这一步读的那份路由级事实。不导出：本包里它只是 resolveProfile 到 resolveRouteModels 之间的一次传参。modelOverrides/compat/api 三项随内置目录一起去掉，剩下的 provider/baseURL/models/defaultContextWindow/defaultMaxTokens/defaultInput 逐项对上。） |
| llm/llm-pi-ai | `buildProvider` | `openaicompat.newChatService` | 非导出的一段：newChatService（裁决表已有理由：为一条路由造出真正去发请求的那个东西。DSH 要在协议表里挑工厂、还要判「复用内置目录那个 provider 还是按显式协议重造」；这边只有一条协议，所以就是照着落定档案配一个 openai-go 的 ChatCompletionService：端点、请求头、超时。） |
| llm/llm-pi-ai | `mapStopReason` | `openaicompat.mapFinishReason` | 非导出的一段：mapFinishReason（裁决表已有理由：线上 finish_reason 翻成 harness 的终止原因。多一支 content_filter（pi-ai 的联合里没有，这条协议上是正经取值，且不可重试所以给了自己的码）；少一支「stop 且用量超窗口也算溢出」——一次产出了内容的 stop 是成功的响应，判成失败会让一个本来答完了的回合失败掉。） |
| llm/llm-pi-ai | `mapUsage` | `openaicompat.mapUsage` | 非导出的一段：mapUsage（裁决表已有理由：token 记账。这条协议上要自己减：prompt_tokens 是**含**缓存命中的总数，而 llm.TokenUsage 要求三者互不重叠。不减的话缓存命中被算两遍，长会话的输入统计虚高到离谱，而预算和压缩触发都照着它算。减完钳在 0。） |
| llm/llm-pi-ai | `resolveRouteModels` | `openaicompat.resolveRouteModels` | 非导出的一段：resolveRouteModels（裁决表已有理由：把写下来的模型表落成真正拿去发请求的模型记录。合并内置目录那一大半没有；留下的是逐条校验、按路由默认值兜底、以及**保持书写次序**——那是本包唯一一处不排序的次序，因为选择器上的顺序就是部署方写下来的顺序。） |
| llm/llm-pi-ai | `toPiAssistant` | `openaicompat.assistantMessage` | 非导出的一段：assistantMessage（裁决表已有理由：把一条落库的助手消息翻回线上格式。重放状态读不懂时降级成中立转换而不是失败，并通过 AdapterOptions.OnReplayDegrade 报一声——一条读不懂的信封不该让整个会话续不下去。） |
| llm/llm-pi-ai | `toPiContext` | `openaicompat.toContext` | 非导出的一段：toContext（裁决表已有理由：把 harness 的历史翻成线上请求。DSH 用两个重载分开「纯文本（同步）」和「要解耐久图片（异步）」；Go 里没有重载，也不需要——一个函数，attachments 为 nil 就走纯文本那一支，调用方拿到的都是同一个签名。图片总量超限时从最老的开始换成文本占位，这条照搬。） |
| llm/llm-pi-ai | `toPiContext` | `openaicompat.toContext` | 非导出的一段：toContext（裁决表已有理由：把 harness 的历史翻成线上请求。DSH 用两个重载分开「纯文本（同步）」和「要解耐久图片（异步）」；Go 里没有重载，也不需要——一个函数，attachments 为 nil 就走纯文本那一支，调用方拿到的都是同一个签名。图片总量超限时从最老的开始换成文本占位，这条照搬。） |
| llm/llm-pi-ai | `toPiContext` | `openaicompat.toContext` | 非导出的一段：toContext（裁决表已有理由：把 harness 的历史翻成线上请求。DSH 用两个重载分开「纯文本（同步）」和「要解耐久图片（异步）」；Go 里没有重载，也不需要——一个函数，attachments 为 nil 就走纯文本那一支，调用方拿到的都是同一个签名。图片总量超限时从最老的开始换成文本占位，这条照搬。） |
| llm/llm-pi-ai | `toStreamChunks` | `openaicompat.streamChunks` | 非导出的一段：streamChunks（裁决表已有理由：SSE 流翻成 harness 的分块序列。DSH 拿到的是 pi-ai 已经带边界的事件（text_start/text_delta/text_end），这条协议一条 delta 里只有 content／reasoning_content／tool_calls 三样，块的开始和结束得由「字段变了」自己推出来——openaicompat.blockAssembly 就是那次推导的状态。） |
| llm/token-meter | `SurfaceTokenFold` | `tokenmeter.surfaceTokenFold` | 非导出的一段：surfaceTokenFold（裁决表已有理由：服务那份逐节点折叠的结果。Go 里不导出：它是 TokenMeter 内部的账本形状，对外只出现在 Measurement.Nodes 里。） |
| llm/token-meter | `SurfaceTokenPlan` | `tokenmeter.surfaceTokenFold` | 非导出的一段：surfaceTokenFold（裁决表已有理由：折进一条表面事件之后的结果。不导出：上游那个 export 是 TS 的跨文件可见性，surface-fold.ts 之外只有同包的 index.ts:254 用它，token-meter 包外一个调用方都没有（全快照 grep 确认），而 Go 里包内跨文件天然共享符号。字段不是逐条对上的：DSH 的 plan 带 node 和 target（'append' 或者被替换的下标闭区间），留给 commitSurfaceTokens 就地改那张表；Go 这一步直接交出折完的整张新节点表（nodes 字段），于是提交就是 tokenmeter/meter.go:431-432 那两次赋值，没有第二个函数。「失败时调用方手上那份状态一个字节都没被动过」这条两边一样，DSH 靠 [...nodes] 加 splice 做到，Go 靠新分配。） |
| llm/token-meter | `SurfaceTokensFold` | `tokenmeter.surfaceTokensFold` | 非导出的一段：surfaceTokensFold（裁决表已有理由：投影那份 O(1) 折叠的结果。Go 里不导出，理由同 surfaceTokenFold。） |
| llm/token-meter | `contextBreakdownProjectionDefinition` | `tokenmeter.contextBreakdownDefinition` | 非导出的一段：contextBreakdownDefinition（裁决表已有理由：信封那两个数按请求头 last-wins，消息那个数骑在 O(1) 表面折叠上。读不回来的请求头保持原值而不是归零——归零会让界面显示成「这次请求没有系统提示、没带工具」，那比偏一点严重得多。） |
| llm/token-meter | `contextPressureProjectionDefinition` | `tokenmeter.contextPressureDefinition` | 非导出的一段：contextPressureDefinition（裁决表已有理由：占用只算提示词侧不含输出，所以一个回合流着的时候它不动。投影值回答的是**下一次**请求：采样值加上采样之后表面的带符号位移，钳在 0；一次压缩能让它当场掉下来，而压力自己看不见那件事，因为压缩不产生用量。） |
| llm/token-meter | `foldSurfaceProjection` | `tokenmeter.foldSurfaceProjection` | 非导出的一段：foldSurfaceProjection（裁决表已有理由：O(1) 的那份：投影状态要塞进一份能落盘的检查点，留不下整张节点表。两种失败有意做成不对称——没有认领单是协议落地之前的老日志，折 0 放过；有认领单但区间对不上是协议被用错了，报错。Definition.Apply 是全函数，所以另有一个 foldSurfaceProjectionLenient 把两种失败合并成同一种降级。两份折叠在每个事件边界上给出同一个总价，由 TestBothFoldsAgreeAtEveryEventBoundary 钉住。） |
| llm/token-meter | `foldSurfaceTokens` | `tokenmeter.foldSurfaceTokens` | 非导出的一段：foldSurfaceTokens（裁决表已有理由：O(表面) 的折叠：每个节点的价钱都留着，因为压缩那边挑下刀点全靠这张表。一条不上表面的事件折不进来要报错——静悄悄忽略会让调用方以为自己已经把它记进去了。失败时调用方手上那张节点表一个字节都不许被动过。） |
| llm/token-meter | `planSurfaceTokens` | `tokenmeter.foldSurfaceTokens` | 非导出的一段：foldSurfaceTokens（裁决表已有理由：把一条表面事件折进当前的节点表。不导出，理由同 SurfaceTokenPlan 那条。名字从 plan 改成 fold，是因为 Go 这一步不再是「先算计划、再提交」的两段式，见上一行。多一个 baseSeq 形参：一次替换的端点定位不到时 DSH 一律抛错，Go 先分两种——端点落在 baseSeq 之前是被 FIFO 弹掉的正常损耗（起点被弹就把区间收到表面最前端，起点终点都被弹就整个降级成一次追加），端点不小于 baseSeq 却仍不在表面上，才是这份节点表算的根本不是当前这个表面，那时候照旧当场断掉。这条降级照的是 sessionlog 那份表面折叠（surface.go 的 replacementRange）已经定下的先例。） |
| llm/token-meter | `tokenUsageProjectionDefinition` | `tokenmeter.tokenUsageDefinition` | 非导出的一段：tokenUsageDefinition（裁决表已有理由：同一个步骤重复报用量是**替换**不是叠加：流中途那条 usage 分块和它后面那条落定消息报的常常逐字相同，加两遍就把账翻倍了。） |
| mcp/mcp-client | `Config` | `mcp.Config.validate` | 非导出的一段：validate（裁决表已有理由：schema 式的运行时校验换成一个 validate 方法，错误都裹在 mcp.ErrInvalidConfig 上） |
| mcp/mcp-client | `RECONNECT_DEFAULTS` | `mcp.defaultInitialDelay` | 非导出的一段：defaultInitialDelay（裁决表已有理由：三个默认值在 Go 里是 mcp/config.go 里的三个常量：defaultInitialDelay / defaultMaxDelay / defaultMaxAttempts） |
| mcp/mcp-client | `ToolBridgeOptions` | `mcp.bridgeOptions` | 非导出的一段：bridgeOptions（裁决表已有理由：syncTools 那一串入参的袋子，只在 connection.go 与 tools.go 之间传。上游 export 是为了跨文件和自己的 spec 用，包外没有调用方，Go 里不导出。） |
| mcp/mcp-client | `ToolDisposers` | `mcp.toolDisposers` | 非导出的一段：toolDisposers（裁决表已有理由：DSH 用 Map 靠 JS 的插入序；Go 的 map 无序而这里顺序要紧，所以换成切片） |
| mcp/mcp-client | `createTransport` | `mcp.supervisor.transport` | 非导出的一段：supervisor、transport（裁决表已有理由：只移 Streamable HTTP 那一支；stdio 那一支要拉起子进程，OUT_OF_SCOPE，见 index.ts:50。自定义请求头换成一层 http.RoundTripper） |
| mcp/mcp-client | `resolveReconnectPolicy` | `mcp.resolveReconnectPolicy` | 非导出的一段：resolveReconnectPolicy（裁决表已有理由：上游 export 只为让 index.ts 和自己的 reconnect.spec.ts 跨文件用，mcp-client 包外没有调用方。Go 里包内跨文件天然共享、测试也在包内，所以不导出。） |
| mcp/mcp-client | `startConnection` | `mcp.startSupervisor` | 非导出的一段：startSupervisor（裁决表已有理由：同 resolveReconnectPolicy：上游的 export 是跨文件可见性，包外没有调用方。改名成 startSupervisor 是因为 Go 这边起的不是一个连接句柄，是一条看着重连的 goroutine，返回的 *supervisor 就是它的把手。） |
| mcp/mcp-client | `syncTools` | `mcp.syncTools` | 非导出的一段：syncTools（裁决表已有理由：「入参 schema 说不出口」从 DSH 的第 2 步提前到第 1 步：Go 要先把 schema 解成 tools.Node 才造得出定义，于是上一代注册在这种失败下活了下来） |
| plan/plan-mode | `PlanUnitState` | `planmode.unitState` | 非导出的一段：unitState（裁决表已有理由：Go 侧是 planmode.unitState（plan/planmode/projection.go:50），Active／Wanted／Running 三个字段逐字对应，两个 nullable 用指针且不带 omitempty，排出去的是显式 null。alpha.3 新加的第四个字段 activeAtLastHeader（request/header 落下时把当时的 active 记一份）Go 没有：同一件事由 planmode.modeAtLastHeader（plan/planmode/fold.go:100）在需要时现场扫一遍整条日志算出来，narration（controller.go:396）读的就是它，所以那条「最后一次告诉模型的是另一种模式才通知」的行为一致。差在两处：算它要一次全日志线性扫而不是读一个字段；以及只拿投影的客户端看不到这个事实。要补的话入口是 unitState 加一个 *bool 字段并在 EventRequestHeader 那一支写入，同时把 projectionStateVersion 从 2 抬到 3。） |
| plan/plan-mode | `resolveConfig` | `planmode.resolveSection` | 非导出的一段：resolveSection（裁决表已有理由：只被本包的 Controller 装配用到，plan-mode 包外没有调用方，所以不导出。改名是因为它在 Go 里只剩一件事：DSH 那三条检查里「section 不是字符串」和「有多余的键」都被 Go 的结构体在编译期挡掉了，运行期只剩「不能是空白」这一条。） |
| preset/agent-presets | `entryListProblem` | `agentpresets.entryListProblem` | 非导出的一段：entryListProblem（裁决表已有理由：一份组合清单的形状对不对，答一句人能读的话或者空串。不导出：上游那个 export 是 TS 的跨文件可见性，discovery.ts 之外只有同包的 composition-inventory.ts:173 用它，agent-presets 包外一个调用方都没有（全快照 grep 确认），而 Go 里包内跨文件天然共享符号。上游那句注释交代的理由——清单盘点那边读文件时会和编辑赛跑，必须按同一条规则判那份赛到的内容——在 Go 侧同样成立，所以它照旧只有一份实现。收的东西换了：DSH 收一份已经解析成 unknown 的值，Go 收 *yaml.Node（agentpresets/discovery.go:63），于是多一步把 DocumentNode 拆到它唯一那个孩子上；判不出问题时 DSH 答 undefined，Go 答空串。） |
| schedule/schedule | `flushSchedulePersistence` | `schedule.flushPersistence` | 非导出的一段：flushPersistence（裁决表已有理由：Go 里不导出：落盘屏障只在本包三件工具的前后走，没有包外调用方。） |
| schedule/schedule | `registerScheduleTools` | `schedule.registerTools` | 非导出的一段：registerTools（裁决表已有理由：桶文件转发，定义处见 src/tools.ts:299；Go 里不导出，由 install.go 内部调用。） |
| schedule/schedule | `registerScheduleTools` | `schedule.registerTools` | 非导出的一段：registerTools（裁决表已有理由：Go 里不导出：三件工具由 install.go 在每个根 agent 上装一次，没有包外调用方。） |
| schedule/schedule | `runScheduleTransaction` | `schedule.transactions.run` | 非导出的一段：transactions、run（裁决表已有理由：TS 用 WeakMap<Agent, Promise> 串行；Go 里换成一张按 agent 计数的互斥闸（transaction.go），行为一致但不靠 promise 链。） |
| session-query/tool-session-query | `operations` | `querytool.Controller.executeSessionSearch` | 非导出的一段：executeSessionSearch（裁决表已有理由：五趟活加 collectPages，全在 operations.go） |
| session-query/tool-session-query | `presentation` | `querytool.formatSessionSearch` | 非导出的一段：formatSessionSearch（裁决表已有理由：六个 format 加四张卡片，全在 presentation.go） |
| session-query/tool-session-query | `serviceBoundary` | `querytool.Controller.sanitize` | 非导出的一段：sanitize（裁决表已有理由：失败清洗门，配 safeFailures 白名单） |
| session-query/tool-session-query | `toolInput` | `querytool.sessionSearchParameters` | 非导出的一段：sessionSearchParameters（裁决表已有理由：五份参数与它们的清洗，散在 input.go 与 tools.go） |
| session-query/tool-session-query | `workspaceAccess` | `querytool.recordAuthorized` | 非导出的一段：recordAuthorized（裁决表已有理由：工作区授权判定，全在 access.go） |
| session/session-persistence | `SessionPreparationReservation` | `persistence.preparationReservation` | 非导出的一段：preparationReservation（裁决表已有理由：一份被独占持有的准备成果。不导出：它只在本包内部的写路径和准备路径之间传，对外露出去的是 persistence.Preparation。） |
| session/session-persistence | `SessionPreparations` | `persistence.preparations` | 非导出的一段：preparations（裁决表已有理由：准备池：冷读共享、独占预留、就绪条目按最近使用淘汰。不导出——它是编排器的内脏。DSH 靠 JS Map 的插入顺序当 LRU 队列，Go 的 map 没有顺序，所以另立一条 order 切片：那个顺序是语义，淘汰谁全看它。另外每一次状态转移都要拿锁，因为 Go 这边池子会被好几条 goroutine 同时碰。） |
| session/session-persistence | `observeQueuedAbort` | `persistence.awaitShared` | 非导出的一段：awaitShared（裁决表已有理由：等一件共享的活儿干完，中途允许这一个等待方自己走掉而不连累其余人。DSH 是给 AbortSignal 挂监听再拆掉；Go 里就是 select 两个通道，另加一句「两边都就绪时不看运气」——一件已经干完的活儿就是干完了。） |
| session/session-title | `titleProjectionDefinition` | `sessiontitle.projectionDefinition` | 非导出的一段：projectionDefinition（裁决表已有理由：标题那个投影单元。不导出：上游那个 export 是 TS 的跨文件可见性，index.ts 里它自己在 :337 登记自己，session-title 包外一个调用方都没有（全快照 grep 确认）。Go 里它是 sessiontitle/projection.go:62 的一个包内函数，装配方够得着的只有导出的 sessiontitle.RegisterProjection（projection.go:113）——登记这件事只留一个口子，是因为 DSH 那边登记裹在 ctx.inject(['sessionProjections']) 里、投影服务不在场就跳过，Go 没有那个容器，「在不在场」就是装配方手上有没有那张注册表，于是它必须是一次显式调用。） |
| session/session-turn-outline | `TurnOutlineState` | `turnoutline.outlineState` | 非导出的一段：outlineState（裁决表已有理由：折叠状态不导出：它里面那份草稿是宿主这一侧的账，视图交出去的只有 Turns（[]Entry）。包外拿得到的是投影注册表里那份视图，没有第二条路要看见这个结构体。） |
| subagent/subagent | `ActivationObserver` | `subagent.activationObserver` | 非导出的一段：activationObserver（裁决表已有理由：只在包内用，所以不导出。） |
| subagent/subagent | `ActivationTerminal` | `subagent.activationTerminal` | 非导出的一段：activationTerminal（裁决表已有理由：只在包内用，所以不导出。） |
| subagent/subagent | `LifecycleEmitter` | `subagent.lifecycleEmitter` | 非导出的一段：lifecycleEmitter（裁决表已有理由：只在包内用，所以不导出；对外那面是 Runtime.OnStart／OnEnd 这几条登记路。） |
| subagent/subagent | `TimingState` | `subagent.timingState` | 非导出的一段：timingState（裁决表已有理由：折叠状态只归这个单元自己，所以不导出。） |
| subagent/subagent | `createActivationObserver` | `subagent.newActivationObserver` | 非导出的一段：newActivationObserver（裁决表已有理由：同 createLifecycleEmitter：上游的 export 是跨文件可见性。它造的 *activationObserver 本身就不导出，构造器自然也不导出。） |
| subagent/subagent | `createLifecycleEmitter` | `subagent.newLifecycleEmitter` | 非导出的一段：newLifecycleEmitter（裁决表已有理由：上游 export 只为让 index.ts 跨文件调用，subagent 包外没有调用方。Go 里不导出，理由同 lifecycleEmitter 自己；对外那面是 Runtime.OnStart／OnEnd 这几条登记路。） |
| subagent/subagent | `observeRun` | `subagent.observeRun` | 非导出的一段：observeRun（裁决表已有理由：同 createLifecycleEmitter：上游的 export 是跨文件可见性。它把一次 Run 裹上生命周期播报，只由 Runtime 在起子 Agent 时调用，不导出。） |
| test-support/session-snapshot | `normalizeSessionLog` | `snapshot.flattenRecord` | 非导出的一段：flattenRecord（裁决表已有理由：上游 export 出来是给同包的 normalize.ts 和 suite.ts 跨文件调用；Go 里包内跨文件天然共享符号，对外只有 Normalize 一个入口就够了。多导出一个「压平一行」等于把内部的行格式当成 API 承诺出去。） |
| test-support/session-snapshot | `normalizeSessionLog` | `snapshot.flattenRecord` | 非导出的一段：flattenRecord（裁决表已有理由：上游 export 出来是给同包的 normalize.ts 和 suite.ts 跨文件调用；Go 里包内跨文件天然共享符号，对外只有 Normalize 一个入口就够了。多导出一个「压平一行」等于把内部的行格式当成 API 承诺出去。） |
| test-support/session-snapshot | `normalizeSessionSnapshot` | `snapshot.normalizeOne` | 非导出的一段：normalizeOne（裁决表已有理由：同上不导出。而且「压一份日志」对外本就没有意义：记号编号必须一次收一整个场景才保得住父子那条线，单独放出这个入口只会招人把那条线压断。） |
| test-support/session-snapshot | `normalizeSessionSnapshot` | `snapshot.normalizeOne` | 非导出的一段：normalizeOne（裁决表已有理由：同上不导出。而且「压一份日志」对外本就没有意义：记号编号必须一次收一整个场景才保得住父子那条线，单独放出这个入口只会招人把那条线压断。） |
| test-support/session-snapshot | `scrubRequestHeaders` | `snapshot.tokenizeRequestHeader` | 非导出的一段：tokenizeRequestHeader（裁决表已有理由：同 scrubSystemPrompts：三种开关组合在 Go 里由 Options 表达，函数不导出。） |
| test-support/session-snapshot | `scrubRequestHeaders` | `snapshot.tokenizeRequestHeader` | 非导出的一段：tokenizeRequestHeader（裁决表已有理由：同 scrubSystemPrompts：三种开关组合在 Go 里由 Options 表达，函数不导出。） |
| test-support/session-snapshot | `scrubSessionSnapshot` | `snapshot.repack` | 非导出的一段：repack（裁决表已有理由：它是 Normalize 内部的一步（换记号之后、交出去之前重打包并抹掉事件信封），不是一个独立的对外能力。） |
| test-support/session-snapshot | `scrubSessionSnapshot` | `snapshot.repack` | 非导出的一段：repack（裁决表已有理由：它是 Normalize 内部的一步（换记号之后、交出去之前重打包并抹掉事件信封），不是一个独立的对外能力。） |
| test-support/session-snapshot | `scrubSystemPrompts` | `snapshot.tokenizeRequestHeader` | 非导出的一段：tokenizeRequestHeader（裁决表已有理由：上游三个 scrub 函数（system／tools／两者）是同一段代码的三种开关组合，Go 里那三种组合由 Options 的两个字段表达，函数本身不必导出。） |
| test-support/session-snapshot | `scrubSystemPrompts` | `snapshot.tokenizeRequestHeader` | 非导出的一段：tokenizeRequestHeader（裁决表已有理由：上游三个 scrub 函数（system／tools／两者）是同一段代码的三种开关组合，Go 里那三种组合由 Options 的两个字段表达，函数本身不必导出。） |
| test-support/session-snapshot | `scrubToolSchemas` | `snapshot.tokenizeRequestHeader` | 非导出的一段：tokenizeRequestHeader（裁决表已有理由：同 scrubSystemPrompts：三种开关组合在 Go 里由 Options 表达，函数不导出。） |
| test-support/session-snapshot | `scrubToolSchemas` | `snapshot.tokenizeRequestHeader` | 非导出的一段：tokenizeRequestHeader（裁决表已有理由：同 scrubSystemPrompts：三种开关组合在 Go 里由 Options 表达，函数不导出。） |
| webhook/webhook | `createWebhookSession` | `webhook.Runtime.createSession` | 非导出的一段：createSession（裁决表已有理由：上游的 export 是跨文件可见性（index.ts 要调它）。Go 里它是 Runtime 的方法，进得去的唯一一条路是规则交回一份 SessionRequest，包外没有调用方，所以不导出。） |
| workflow/workflow | `WorkflowErrorCode` | `workflow.fatalCodes` | 非导出的一段：fatalCodes（裁决表已有理由：十一个致命码的联合类型，只在 TS 编译期成立。Go 里码本身是 workflow.CodeScriptParse 那一组导出常量（取值逐条照抄，线上可见），而这个联合真正的用途是 workflow.IsFatal 在运行期判「该不该打断脚本」，所以落成一张集合。成例见 attachment.imageAdmissionCodes。集合不导出：判据的唯一入口是 IsFatal，把表也放出去等于让调用方能绕过它。） |
| workspace/workspace | `WorkspaceEntity` | `workspace.entity` | 非导出的一段：entity（裁决表已有理由：[workspace.Workspace] 唯一的实现，只由登记册构造。） |
| workspace/workspace | `WorkspaceEntityHost` | `workspace.entityHost` | 非导出的一段：entityHost（裁决表已有理由：DSH 那边导出了但入口没再转发，消费方其实看不见它；Go 里一个包就是一层，不导出即可。） |

## 三、多条上游符号塌缩到同一个 go_ref

已排除 re-export（一个包同时声明并转出同一个类型是清单的形状，不是移植的问题）。

拿一个带判别字段的结构体接住一整族 TS 判别联合是本仓库的既定做法，所以**塌缩本身不是错**。要查的是：那个 Go 类型的注释里有没有写明并进来了哪几个上游形态、判别字段是什么、以及上游靠类型窄化保证的那些约束在 Go 里由谁来保证。

共 82 组 / 200 条。

| go_ref | 条数 | 并进来的上游符号 |
|---|---|---|
| `schedule.ToolError` | 11 | `CorruptScheduleLogError`(interface)、`FrequencyTooHighError`(interface)、`InternalScheduleError`(interface)、`InvalidPromptError`(interface)、`InvalidRuleError`(interface)、`InvalidSelectorError`(interface)、`InvalidTimeZoneError`(interface)、`NotFutureError`(interface)、`PersistenceUncertainError`(interface)、`ScheduleToolError`(type)、`TimeOutOfRangeError`(interface) |
| `schedule.Change` | 6 | `EveryScheduleDispatchChange`(interface)、`OneShotScheduleDispatchChange`(interface)、`ScheduleChange`(type)、`ScheduleCreateChange`(interface)、`ScheduleDeleteChange`(interface)、`ScheduleDispatchChange`(type) |
| `subagent.DescriptorData` | 6 | `ContinuableSubagentDescriptorData`(interface)、`ContinuableSubagentDescriptorInput`(interface)、`OneShotSubagentDescriptorData`(interface)、`OneShotSubagentDescriptorInput`(interface)、`SubagentDescriptorData`(type)、`SubagentDescriptorInput`(type) |
| `schedule.Record` | 5 | `AfterScheduleRecord`(interface)、`AtScheduleRecord`(interface)、`EveryScheduleRecord`(interface)、`OneShotScheduleRecord`(type)、`ScheduleRecord`(type) |
| `llm.Message` | 4 | `AssistantMessage`(interface)、`Message`(interface)、`ToolResultMessage`(interface)、`UserMessage`(interface) |
| `openaicompat.toContext` | 4 | `PiImageRequestContext`(interface)、`toPiContext`(function)、`toPiContext`(function)、`toPiContext`(function) |
| `agentpresets.PresetExistsError` | 3 | `PresetExistsError`(STALE:class)、`PresetExistsError`(STALE:reexport)、`presetExists`(function) |
| `agentpresets.ResolveSessionPreset` | 3 | `agentPresetProjectionDefinition`(const)、`resolveSessionPreset`(STALE:reexport)、`resolveSessionPreset`(STALE:function) |
| `goal.Change` | 3 | `GoalChangeMeta`(type)、`GoalClearChangeMeta`(interface)、`GoalSnapshotChangeMeta`(interface) |
| `llm.ResolvedRetryPolicy` | 3 | `ResolvedAlwaysRetryPolicy`(interface)、`ResolvedNormalRetryPolicy`(interface)、`ResolvedRetryPolicy`(type) |
| `llm.RetryPolicyConfig` | 3 | `AlwaysRetryPolicyConfig`(interface)、`NormalRetryPolicyConfig`(interface)、`RetryPolicyConfig`(type) |
| `sessiontitlellm.Config` | 3 | `Config`(type)、`Config`(type)、`SessionTitleLlmConfig`(interface) |
| `snapshot.tokenizeRequestHeader` | 3 | `scrubRequestHeaders`(function)、`scrubSystemPrompts`(function)、`scrubToolSchemas`(function) |
| `subagent.ListEntry` | 3 | `SubagentListEntry`(type)、`SubagentListEntry`(STALE:reexport-type)、`SubagentListEntry`(STALE:type) |
| `subagent.SnapshotDescriptor` | 3 | `snapshotSubagentDescriptor`(function)、`snapshotSubagentDescriptor`(function)、`snapshotSubagentDescriptor`(function) |
| `tools.Result` | 3 | `ToolExecutionFailure`(interface)、`ToolExecutionResult`(type)、`ToolExecutionSuccess`(interface) |
| `tools.ResultView` | 3 | `SearchResultView`(type)、`ToolResultView`(type)、`WebResultView`(type) |
| `tools.Runtime` | 3 | `ToolRuntime`(class)、`ToolRuntime`(default)、`ToolRuntimeScheduler`(interface) |
| `acp.AssistantBlockToACP` | 2 | `assistantBlockToAcp`(function)、`assistantUpdates`(function) |
| `acp.Bridge` | 2 | `AcpSession`(class)、`apply`(function) |
| `agent.Agent` | 2 | `Agent`(STALE:interface)、`Agent`(interface) |
| `agentpresets.InvalidPresetIDError` | 2 | `InvalidPresetIdError`(STALE:class)、`InvalidPresetIdError`(STALE:reexport) |
| `agentpresets.PresetMountError` | 2 | `PresetMountError`(STALE:reexport)、`PresetMountError`(STALE:class) |
| `agentpresets.PresetNotWritableError` | 2 | `PresetNotWritableError`(STALE:class)、`PresetNotWritableError`(STALE:reexport) |
| `agentpresets.UnknownPresetError` | 2 | `UnknownPresetError`(STALE:reexport)、`UnknownPresetError`(STALE:class) |
| `basic.Engine` | 2 | `BasicCompactionEngine`(class)、`BasicCompactionEngine`(default) |
| `compaction.Engine` | 2 | `CompactionEngine`(class)、`CompactionEngine`(default) |
| `credentials.Info` | 2 | `CredentialInfo`(STALE:interface)、`CredentialInfo`(interface) |
| `domain.Changed` | 2 | `DomainChanged`(type)、`DomainChangedBase`(interface) |
| `domain.Domain` | 2 | `Domain`(interface)、`DomainImpl`(class) |
| `fs.FileSystem` | 2 | `FileSystem`(class)、`realpathNormalize`(function) |
| `goal.Service` | 2 | `GoalService`(class)、`GoalService`(default) |
| `goalrounddriver.PluginName` | 2 | `name`(const)、`name`(const) |
| `instructions.Config.Resolve` | 2 | `resolveConfig`(function)、`resolveDiscoveryConfig`(function) |
| `instructions.ResolvedConfig` | 2 | `ResolvedConfig`(interface)、`ResolvedDiscoveryConfig`(interface) |
| `llm.CallID` | 2 | `CallId`(STALE:type)、`ToolCallId`(type) |
| `llm.ContentBlock` | 2 | `ContentBlock`(type)、`ContentBlockMap`(interface) |
| `llm.FinishReason` | 2 | `FinishReason`(type)、`FinishReasonMap`(interface) |
| `llm.MessageSource` | 2 | `MessageSource`(type)、`MessageSourceMap`(interface) |
| `llm.OffloadRequestImagesWithPolicy` | 2 | `offloadRequestImagesWithPolicy`(function)、`offloadedImagePrefixCount`(function) |
| `llm.Runtime` | 2 | `LlmRuntime`(class)、`LlmRuntime`(default) |
| `mcp.Config` | 2 | `Config`(type)、`StreamableHttpConfig`(interface) |
| `openaicompat.ModelProfile` | 2 | `PiAiModelProfile`(interface)、`PiAiReasoningEfforts`(type) |
| `permissionpresets.Service` | 2 | `PermissionPresetService`(class)、`PermissionPresetService`(default) |
| `projection.CheckpointRow` | 2 | `ProjectionCheckpointRow`(interface)、`checkpointRow`(const) |
| `projectioncache.Identity` | 2 | `CheckpointIdentity`(type)、`checkpointIdentity`(const) |
| `projectioncache.Record` | 2 | `CheckpointRecord`(type)、`checkpointRecord`(const) |
| `querytool.PackageName` | 2 | `name`(const)、`name`(const) |
| `replay.Config` | 2 | `Config`(interface)、`ReplayConfig`(interface) |
| `schedule.FoldEvents` | 2 | `applyScheduleChanges`(function)、`foldScheduleEvents`(function) |
| `sdkserver.Config` | 2 | `HarnessSdkJsonRpcServerOptions`(interface)、`JsonRpcConfig`(interface) |
| `sdkserver.Server` | 2 | `*`(star)、`HarnessSdkJsonRpcServer`(class) |
| `sessionlog.IsSurfaceEligibleType` | 2 | `SurfaceEventType`(type)、`isSurfaceEligibleType`(function) |
| `sessionlog.TodoItem` | 2 | `TodoItem`(STALE:interface)、`TodoItem`(interface) |
| `sessionlog.TurnEndCancelCause` | 2 | `AgentCancelCause`(type)、`TurnEndCancelCause`(type) |
| `sessionlog.TurnEndReason` | 2 | `TurnEndReason`(type)、`TurnEndReasonMap`(interface) |
| `sessionquery.ProjectionResult` | 2 | `LogicalProjectionResult`(type)、`SessionTitleObservationResult`(type) |
| `sessionref.Install` | 2 | `SessionReferenceResolver`(class)、`SessionReferenceResolver`(default) |
| `sessiontitle.EventData` | 2 | `SessionTitleEventData`(STALE:interface)、`SessionTitleEventData`(interface) |
| `sessiontitle.ModelProvenance` | 2 | `SessionTitleModelProvenance`(STALE:interface)、`SessionTitleModelProvenance`(interface) |
| `sessiontitle.ProviderID` | 2 | `SessionTitleProviderId`(type)、`SessionTitleProviderId`(type) |
| `sessiontitle.Snapshot` | 2 | `SessionTitleSnapshot`(STALE:interface)、`SessionTitleSnapshot`(interface) |
| `sessiontitle.Source` | 2 | `SessionTitleSource`(STALE:type)、`SessionTitleSource`(type) |
| `sessiontitle.UserMessage` | 2 | `SessionTitleUserMessage`(STALE:interface)、`SessionTitleUserMessage`(interface) |
| `settings.PathOp` | 2 | `SettingsPathOp`(type)、`SettingsPathOpView`(type) |
| `settings.Secret` | 2 | `RedactedSecret`(interface)、`SettingsSecretView`(interface) |
| `subagent.RegisterProjections` | 2 | `subagentIdentityProjectionDefinition`(const)、`subagentTimingProjectionDefinition`(const) |
| `tokenmeter.ContextBreakdownView` | 2 | `*`(star-type)、`ContextBreakdownProjection`(interface) |
| `tokenmeter.EstimateContent` | 2 | `estimateContent`(function)、`estimateStructuralBlock`(function) |
| `tokenmeter.SurfaceNode` | 2 | `MeterSurfaceNode`(interface)、`TokenSurfaceNode`(interface) |
| `tokenmeter.TokenUsageView` | 2 | `*`(star-type)、`TokenUsageProjection`(interface) |
| `tokenmeter.foldSurfaceTokens` | 2 | `foldSurfaceTokens`(STALE:function)、`planSurfaceTokens`(function) |
| `tokenmeter.surfaceTokenFold` | 2 | `SurfaceTokenFold`(STALE:interface)、`SurfaceTokenPlan`(interface) |
| `toolresultpruner.Pruner` | 2 | `ToolResultPruner`(class)、`ToolResultPruner`(default) |
| `tools.ApprovalRequest` | 2 | `ApprovalRequest`(interface)、`ApprovalRequestEvent`(interface) |
| `tools.AssertObjectSchema` | 2 | `ObjectJsonSchema`(type)、`assertObjectJsonSchema`(function) |
| `tools.ValidateValue` | 2 | `validateArgs`(function)、`validateJsonSchemaValue`(function) |
| `turnoutline.RegisterProjection` | 2 | `apply`(function)、`turnOutlineProjectionDefinition`(const) |
| `userquestions.Request` | 2 | `AskUserQuestionRequest`(interface)、`AskUserQuestionRequestEvent`(interface) |
| `webhook.Runtime` | 2 | `WebhookRuntime`(class)、`WebhookRuntime`(default) |
| `workflow.Engine` | 2 | `WorkflowEngine`(class)、`WorkflowEngine`(default) |
| `workspace.WorkspaceID` | 2 | `WorkspaceId`(type)、`WorkspaceId`(type) |

## 四、溯源密度偏低的包

全仓 4673 条 `// 源:` / 133207 行非测试代码 = **35.1 条/千行**。低于 25 条/千行的列在下面。

密度低不等于写错了，它只说明这段代码多半是照着记忆写的而不是照着源码写的——**这一份指的是该去哪儿细读，不是哪一行有 bug**。两类包已排除：本仓自造的 `internal/devtools/`，以及包文档里写了 `新增:` 且全包零条 `源:` 的包——后者已经在最显眼的地方交代过自己整份是新写的。

| 包 | 非测试行数 | `// 源:` | `// 新增:` | 条/千行 |
|---|---:|---:|---:|---:|
| fs/fstest | 485 | 1 | 2 | 2.1 |
| storage/storagetest | 1161 | 10 | 4 | 8.6 |
| adapter/domainjobs | 1463 | 13 | 13 | 8.9 |
| harness/harnesstest | 224 | 4 | 5 | 17.9 |
| sessionlog/snapshot | 1186 | 22 | 8 | 18.5 |
| feature/replay | 1639 | 39 | 15 | 23.8 |

## 五、包文档有毛病的包

这一份和前四份不一样：它跟移植准不准没关系，跟**下一个读这份代码的人**有关系。

本仓库的写法是每份文件顶上一条 `本文件的作用：…`，和 `package` 子句之间空一行隔开；包文档另写，多数落在 `doc.go` 里。少掉那个空行，Go 就把文件说明当成了整个包的文档——**编译器不会说话，`go doc` 也照样有输出，只是讲的是某一份文件而不是这个包**。这一种自己是看不出来的，只能靠这份报告。

全仓 106 个包，有毛病的 0 个。

没有发现。

