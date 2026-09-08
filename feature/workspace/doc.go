// Package workspace 是**工作区登记册**：一批稳定的目录记录、一份稳定的展示次序，
// 以及经过会话头验证的会话归属。
//
// 源: packages/workspace/workspace/src/index.ts:1-5
//
// 一个工作区就是「一个目录 + 一个标题 + 一串按人手排定的会话」。它不是一个容器：
// 删掉一个工作区登记，目录还在、会话日志一条不少。它记的只是「这些会话属于那个目录」
// 这件事，外加一个人手排定的、活动量永远改不动的次序。
//
// # 归属要同时满足两件事
//
// 一个会话算不算这个工作区的，要**同时**满足：
//
//   - 它的 id 在这条记录的候选账目 [Record.SessionIDs] 里；
//   - 它的会话头 [session.SessionHeader.WorkspaceID] 就是这条记录的 id。
//
// 第二条是一次标识相等，**一次文件系统调用都不发**。它每次读
// [Workspace.SessionIDs] 时同步地过一遍（会话头索引在打开时和挂载活会话时建好）。
// 过不了的候选**不会**被立刻抹掉：那条会话头可能只是这一刻还没被列举到。
// 它们在下一次这个工作区发生真实写入时被顺手裁掉（见 [Workspace] 的写路径）。
//
// 新增: DSH 的第二条判据不是这样——它那边会话头上的字段叫 `cwd`，是一条**宿主机**
// 绝对工作目录，判据要先把它 realpath 一次、stat 一次确认是个存在的目录、
// 再拿解析出来的目标标识和 [Record.TargetKey] 比。本仓库的会话头记的不是位置而是
// 归属（理由见 [session.SessionHeader.WorkspaceID]：服务端没有可用硬盘，
// 存储全在数据库和对象存储里，一条宿主机路径把会话钉死在一台机器上），
// 那三步于是塌成一次相等。
//
// 这不只是省了几次 I/O。照搬 DSH 的写法在本仓库里是**恒为假**的：工作区那条路径
// 来自 [Registry.Create] 的入参，走 [fs.FileSystem]；会话那条来自宿主机。
// 同一个 [fs.FileSystem.Resolve] 收下两个宇宙的路径，在唯一的生产后端
// （[github.com/snight1983/ds-harness-go/adapter/objectstore.Store]）上，宿主那一条
// 换算出的键底下永远没有对象——不只是读时全被筛掉，[Workspace.AttachSession]
// 那道守卫会让每一次挂载都报 [CodeAttachRejected]。换成标识相等之后，
// 这条判据才第一次真正成立。
//
// # 路径的规范形由 fs 接缝拥有，不由本包拥有
//
// 新增: DSH 那边有一个 `paths.ts`，里面就一句 `node:fs/promises` 的 realpath，
// 自陈是「本包唯一的唯一性范式」。本仓库的裁决里本机文件系统整支不在范围内
// （DESIGN 第三节），[fs.FileSystem] 才是那条接缝，而它已经把同一件事写进了契约：
// 「同一个文件无论从哪条路径走到，[fs.FileSystem.Resolve] 给出的
// [fs.Target.TargetKey] 必须相同」。
//
// 所以本包不再自己做规范化，唯一性范式改成 [fs.TargetKey] 的相等。换来的是两件事：
//
//   - 唯一性对**远端后端**也成立。realpath 只在本机磁盘上有意义，
//     而对象存储后端上「同一个目录的两种拼法」得由后端自己回答。
//   - 展示和身份分开了。[fs.Target.DisplayPath] 是给人看的那一条，
//     [fs.TargetKey] 是拿来比的那一个，两者都存进记录，谁也不冒充谁。
//
// 代价也要写明：[fs.TargetKey] 是**存进介质**的，所以它对后端多了一条要求——
// 同一个目录的 key 必须跨进程重启保持不变。本机后端（realpath 那样的串）和
// 工作区 URI 都满足；一个每次现分配的临时 id 不满足，那样的后端配上来，
// 重启之后整册工作区会认不出自己的目录。
//
// # 可恢复的两次写
//
// 建一个工作区要改两处durable状态：表里那条记录，和全局槽里的次序。
// 两次写之间掉电，介质上就会出现「有记录没次序」或者反过来。本包照录 DSH 的办法：
// 在两者可能分叉**之前**先写一个 [PendingMutation] 标记，说明「正在建/正在删哪一个」。
// 启动时 [Registry.Open] 只完成**被标记明确点名**的那一次，其余任何次序与表的分叉
// 一律由 [Registry] 的状态校验当场报错——它绝不从一条记录的样子去猜它当初是怎么来的。
//
// # 和 DSH 的其它差异
//
// 新增: DSH 那边 WorkspaceRegistry 是一个 `extends Service` 的 cordis 插件，
// `static inject = ['storageDomain', 'sessionPersistence']`，启动逻辑写在
// `[Service.init]` 里。Go 里没有那个容器：依赖由装配方填进 [Config]，
// 启动是显式的 [Open]，停止是显式的 [Registry.Close]。
//
// 新增: DSH 的写操作靠一条 promise 链（`operationTail`）串起来，因为 JS 是单线程的，
// 那条链就是它的互斥。Go 里对应物是 [sync.Mutex]，见 [Registry] 里的说明。
//
// 新增: 时间戳在 DSH 里是 ISO-8601 字符串（`new Date().toISOString()`），
// 本包用 [time.Time]，写进 JSON 时是 RFC 3339（也就是同一套写法），一律取 UTC。
// 这么改是因为 bootstrap 那一段要拿创建时间**排序**，DSH 为此得现场
// `Date.parse` 一次；Go 里直接比就行，少一处可以解析失败的地方。
//
// 新增: `randomUUID()` 和 `new Date()` 这两处对外界的直接依赖，改成 [Config.NewID]
// 和 [Config.Now] 两个可注入的口子。理由是本包的核心行为——次序、时间戳推进、
// 崩溃恢复——不给定一个可控的 id 和时钟就测不了。留空各自回落到 uuid 和 [time.Now]。
//
// # 不做什么
//
//   - **不建目录，也不动代码仓库。**一条记录就是一条记录：删掉它，目录还在、会话
//     日志一条不少。
//   - **不做路径规范化。**唯一性范式是 [fs.TargetKey] 的相等，规范形由
//     [github.com/snight1983/ds-harness-go/fs] 那条接缝拥有。
//   - **不提供用户、团队或 ACL 模型。**它记的是「哪些会话属于哪个目录」加一个人手
//     排定的次序；谁能看、谁能改，不在这里回答。
//   - **不实现自己的存储。**记录与次序落在
//     [github.com/snight1983/ds-harness-go/storage/domain] 上；[Persistence] 只用来确认会话归属，
//     它不是本包的数据库。
//   - **不做断线重连的状态传输。**baseline 加增量那一套是**有意**不做：同一语义在本
//     仓库由 [github.com/snight1983/ds-harness-go/sessionlog/projection] 的整值投影
//     承担，重连就重取整值。本包只回答「现在是什么样」——那份答案是
//     [Workspace.Snapshot]，一次读回全部字段、彼此自洽。
//   - **不承诺跨工作区的一致读。**[Workspace.Snapshot] 只管一个工作区里的字段同属一个
//     时刻。连着读两个工作区仍然是两次读，中间可以夹着别人的写。要「整个登记册的
//     一份快照」，眼下没有这条路。
package workspace
