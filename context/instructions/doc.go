// Package instructions 是工作区指令这一层：把会话那份工作区的根到项目根之间每一级的
// AGENTS.md / CLAUDE.md 找出来读进来，按一个字节预算渲染成模型看得见的一段文字，
// 并且在会话往前走的过程中，把「这些文件后来变了」这件事增量地补上去。
//
// 源: packages/context/agent-instructions/src/index.ts
//
// # 这一层管什么
//
// 三件事，别的都不管：
//
//  1. **发现与基线。**从会话那份工作区的根往上走找到项目根（默认看 `.git`），
//     把项目根到工作区根这条链上每一级目录里的候选文件全部读出来，加上一份用户
//     全局指令，渲染成一段带 `<system-reminder>` 框的文字。这就是[LoadBaselineSet]。
//
//     新增: 那个起点在 DSH 是会话头上的 `cwd`，一条宿主机路径。本仓库的会话头记的
//     是一个不透明的工作区标识（见
//     [github.com/snight1983/ds-harness-go/session.SessionHeader.WorkspaceID]），
//     换成根路径这一步归装配方做，见 [Deps.WorkspaceRoot]。本包因此是**整个仓库里
//     唯一真的需要一棵能逐级上行的目录树**的地方——而它走的那棵树是
//     [fs.FileSystem] 命名空间里的，不是宿主机上的。
//
//  2. **增量对账。**会话开始之后这些文件还会被改、被删、被新建。
//     [Reconcile] 拿「模型现在看得见的那份状态」和「介质上现在的样子」比一遍，
//     只把差额渲染出来——没变的文件一个字都不重发。
//
//  3. **预算。**渲染永远落在一个字节上限里。超了就按「越具体越保留」的顺序
//     从最宽的那一头开始丢，丢到只剩一个还超就截断它，截断到零还超就只留一行说明。
//     丢了什么、截了多少，都写进那行说明里给模型看。
//
// 上面这三件事全部是可以直接调的纯函数加一个文件系统接缝，没有任何隐含的调度。
//
// 「什么时候去对账、结果挂到哪条消息上、谁来触发」是第四件事，由 [Install]
// 单独管（对应 DSH 的 `apply`）：它往 agent 的前置步骤、会话日志、工具结果和
// 处置这四条边上各挂一个观察者，此外不主动读任何文件。装不装它是装配方的选择，
// 不装的话上面那三件事照样能单独用。
//
// # 路径是斜杠分隔的世界绝对路径
//
// 本包一个 `path/filepath` 都不用，只用 `path`。理由和 [fs] 那条接缝一样：
// 路径说的是**执行世界**里的位置，不是本进程宿主机上的位置。一台 Windows 宿主机
// 驱动一个 Linux 容器时，`filepath.Join` 会拼出反斜杠，而那条路径要交给容器里的
// 后端去解析。所以本包收到的每条路径先过一遍 [absPath]：反斜杠换成斜杠、
// 不以斜杠开头的补上斜杠、再 `path.Clean`。
//
// 由此还少掉一样东西：DSH 的 `resolve(p)` 在路径是相对的时候会去拿
// `process.cwd()`，本包**没有这个概念**（见 docs/DESIGN.md 第二节：服务化，
// 没有「当前工作目录」）。一条相对路径在这里以世界根 `/` 为基准，
// 而不是以本进程的工作目录为基准。
//
// # 用户全局指令那一层怎么改的
//
// DSH 的用户全局指令落在 `$DSH_HOME/AGENTS.md`，路径由 `dsh-home-paths` 那个包
// 从本机 home 目录算出来，显示成 `~/.dsh/AGENTS.md` 或者 `$DSH_HOME/AGENTS.md`。
// 那个包整个是 OUT_OF_SCOPE 的（见 docs/DESIGN.md 第六节的删除清单）：
// 服务端没有「当前用户的 home」。
//
// 换成的是 [Config.UserGlobalRoot]：一条落在 [fs] 接缝里的绝对目录，
// 由装配方给，留空就表示这套部署没有用户全局这一层。显示路径固定是常数
// [UserGlobalDisplayPath]（`user-global/AGENTS.md`），不再随 home 变。
//
// 这一改顺带消掉了 DSH 里两处必须同步的特判：`scopeForDisplayPath` 原本要认
// `~/.dsh/AGENTS.md` 和 `$DSH_HOME/AGENTS.md` 这两个字面量，现在它就是一次
// `path.Dir`——因为 `user-global/AGENTS.md` 的目录名**本来就是** `user-global`，
// 也就是那个作用域常数本身。少一处特判，就少一处「显示路径变了但作用域没跟着变」
// 的机会，而那种不同步的后果是：一份用户全局指令能加载、却永远对不上账。
//
// # 这里和 DSH 不一样的地方
//
//  1. **没有 cordis，也没有 schemastery。**DSH 的 `Config` 是一份 schemastery
//     模式，默认值由 `.default()` 在运行期补。Go 这边是一个普通结构体加
//     [Config.Resolve]，默认值补在同一个地方。少掉的运行期校验（`z.number()`
//     这类）在 Go 里由类型系统承担。
//  2. **AbortSignal 换成 context.Context。**DSH 在每个 catch 里补一次
//     `signal?.throwIfAborted()`，为的是别把「被取消了」误吞成「介质暂时不可用」。
//     本包在每个吞错误的位置做同一件事：先看 `ctx.Err()`，非 nil 就**往上抛**，
//     只有真的是提供方故障才降级成 unavailable。这条区别是有后果的——
//     取消被吞掉的话，一次被取消的对账会被当成「文件都不见了」，
//     然后给模型发一批 remove。
//  3. **没有 node 那条回退路径。**DSH 的 `statFile` / `readBounded` 在没有
//     `ctx.fs` 时退回 `node:fs`，直接读宿主机。本包**只**走 [fs.FileSystem]，
//     那个参数是必需的。理由同上一节：宿主机的文件系统不是执行世界的文件系统，
//     而这一层读出来的东西是要进提示词的。
//  4. **判别联合换成 Go 的表达法。**`{kind:'present'|'absent'|'unavailable'}`
//     换成 [ProbeKind] 加 [ScopeProbe]；`Promise<T | undefined>` 换成
//     `(T, bool, error)`——「没有」和「出错了」在 DSH 那边都是 undefined，
//     在这里是两个不同的返回。
//  5. **WeakMap 那层缓存没有对应物。**DSH 的 `InstructionVersionCache` 是
//     `WeakMap<Session, Map<...>>`，靠会话对象被回收来清理。Go 这边版本表
//     由调用方持有并传进 [ReconcileRequest.Versions]，[Reconcile] **就地改它**。
//     谁的生命周期就归谁管，这比再造一个弱引用表诚实。
//  6. **顺序是显式的，不靠 map。**DSH 大量依赖 JS 的 Set/Map 保插入序，
//     而对账的输出顺序会一路影响到渲染顺序和预算怎么裁。Go 的 map 遍历是随机的，
//     所以本包凡是「要按插入序走一遍」的地方都用 [scopeSet] 和 [changeIndex]
//     这两个显式有序结构，而不是 map。不这么做的话，同样的输入会渲染出不同的
//     字节数，预算裁剪的结果跟着飘。
//  7. **`Source` 不是一个 llm.MessageSource 变体。**DSH 用 TypeScript 的声明合并
//     往 `MessageSourceMap` 上挂了一个 `'agent-instructions'`。Go 的 [llm]
//     把来源封成了封闭接口（见 llm 的包文档），插件挂不进去。本包给出的是
//     [Source] 这个普通结构体加它的 JSON 编解码，以及 [Source.MessageSource]
//     ——用 [llm.UnknownSource] 这个封闭联合**留出来的那个口子**原样携带它。
//     基线消息那条（DSH 的 `workspaceContextMessage`）本来就用的是
//     `{kind:'plugin'}`，所以 [BaselineMessage] 是直译。
//  8. **多了一条「agent 被处置」的观察者。**DSH 的 `apply` 挂三个钩子，
//     每段会话那点状态全放在以 Session 或者 Agent 对象为键的 WeakMap 里，
//     靠 JS 的垃圾回收清掉。Go 没有弱引用表，那些状态按会话标识存在一张普通 map 里，
//     就必须有人来删——[Agents.OnDisposed] 就是那条边。少了它，一段跑完的会话
//     会永远占着表里那一行。
//  9. **`invariant.ts` 不在本包。**它装的是一个空的不变量安装器，没有东西可注册。
//     裁决行留在 docs/portmap/portmap.tsv 里。
//
// # 覆盖率为什么到不了 99%
//
// docs/DESIGN.md 第九节给纯逻辑包定的线是 99%，有 I/O 的包低于这个数要在源码里
// 写明为什么。本包的用例覆盖到 97.8%，剩下的分支分成三类，都**不是**没写用例：
//
//  1. **取消和提供方故障撞在一起的那一刻。**上面第 2 条说的「先看 ctx.Err()」
//     在每一道接缝上都做了一遍：[statFile] 的 Resolve 之后和 Stat 之后各一次、
//     [existsAsMarker] 里两次、[readBounded] 的入口和 StreamText 失败之后各一次，
//     以及 [installer.compose] 和 [installer.onPreStep] 算完之后各一次。
//     其中有几处要求「提供方这一次调用失败」和「ctx 在这一次调用期间被取消」
//     同时成立，也就是要卡进两条相邻语句之间的那个窗口。假件注入不到那个时刻，
//     真后端上它是一个真实的竞态。这几次检查是**故意冗余**的：多做一次
//     `ctx.Err()` 的代价是一次原子读，漏做一次的代价是给模型发一批假的 remove。
//     [installer.stepIsOpen] 那句「重放期间有一条事件已经把答案定下来了」也在这一类。
//  2. **证明不可达、但留着当护栏的分支。**[AncestorChain] 走到世界根、
//     [RenderInstructionChanges] 的渲染器拿到一个不在自己那张表里的文件、
//     [renderInstructionContext] 在文件列表为空时还要再裁一轮，
//     以及 [Source.MessageSource] 和 [ContextMessage] 里 `json.Marshal` 失败
//     ——[Source] 只有 string、bool 和结构体切片，编码不可能失败。
//     [userMessageOf] 里那次「解出来不是 UserMessageData」的类型断言同理。
//     这几处各自在源码里就近写了为什么到不了。
//  3. **用例够不着的那几处，因为 [llm] 是封闭的。**samePayload 有四条
//     `json.Marshal` 失败的退路，可 [llm.ContentBlock] 和 [llm.MessageSource]
//     都是封闭接口（见 llm 的包文档），本包外面造不出一个编不出去的实现，
//     用例摆不出那个局面。同一个原因盖住 [installer.queue] 里那行「投影没跑成
//     就记一行警告」：投影唯一的失败来源是它那条命被掐掉，而那时按定义不该报警。
//     真正会走到这几处的是别的实现方哪天编码失败，那不是本包能造出来的输入。
package instructions
