// Package sessionquery 是会话历史的读侧：精确读、关系追溯、纯谓词过滤，
// 以及一个可选的全文检索后端挂点。
//
// 源: packages/session-query/session-query/src/index.ts
//
// # 这一层管什么
//
// 只管四件事：
//
//  1. **一次观察里的一份逻辑会话是什么。**同一个会话 id 可能同时有一份活的
//     （在内存里正被推进）和一份落地的（在持久化后端里）。本包定义「活的优先」
//     这条选择规则，并且保证一次调用里的头和事件来自**同一次观察**——
//     不会出现头是活的、事件是落地的这种缝合物。
//  2. **一条事件在表面上处于什么位置。**current（模型现在还看得见）、
//     shadowed（被某次替换盖掉了）、log-only（压根不上表面）。
//  3. **哪些文字是可检索的语义文字。**结构性边界、流式分块、请求信封一律
//     不产出文字——它们的内容在别的事件里已经完整。
//  4. **不依赖任何检索后端的那部分行为。**精确读、过滤、追溯全部是纯计算，
//     没挂后端也能用。
//
// 全文检索**不在**这一层：本包只定义 [Searcher] 这个挂点和请求/结果的形状，
// 索引、排序、游标世代、查询执行全归实现方。没挂 [Searcher] 时两个检索方法
// 返回 [CodeSearchDisabled]，其余方法照常工作。
//
// # 活的优先，以及它为什么不是「谁新用谁」
//
// [Corpus.Load] 一旦认出目标是活的，就**根本不去问持久化**。这条不是性能
// 优化，是可用性契约：一个可选后端的故障不该让此刻在内存里的历史读不出来。
//
// 反过来，一份落地的记录被读出来之后，还要再问一次这个 id 是不是刚刚变活了
// （DSH 的 `attached` 那一步）。会话在 Load 期间被挂起来是平常的，
// 那时候落地的那份已经是旧的了。
//
// 两份观察都拿到时，[AssertHeadersCompatible] 拿身份字段对一遍。对不上说明
// 同一个 id 底下是两个不同的会话，那是配置事故，只能拒。
//
// # 这里和 DSH 不一样的地方
//
//  1. **没有 cordis。**DSH 是 `abstract class SessionQueryEngine extends Service`，
//     检索由子类继承实现，活会话表从 `ctx.sessions` 上取，持久化用
//     `ctx.inject(['sessionPersistence'])` 做可选注入。Go 这边全部改成显式构造：
//     [Options] 收活会话表、可选的持久化后端、可选的 [Searcher]。
//     继承换成组合的理由见 [Searcher]。
//  2. **AbortSignal 换成 context.Context。**DSH 每个方法末尾挂一个可选的
//     `signal`，内部反复 `signal?.throwIfAborted()`。Go 这边 ctx 是第一参数，
//     取消点落在同样那几个位置，抛出的错误码同样是 [CodeAborted]。
//  3. **过滤器的运行期类型检查全部消失。**DSH 的 `materializeSessionResultFilters`
//     里有大半是 `Array.isArray` / `typeof value !== 'string'` 这类检查——
//     它在防的是 TypeScript 类型被外部调用方绕过。Go 的封闭接口让那些状态
//     根本表达不出来，所以只剩下三件真事：复制一份切片、验区间、验封闭词汇。
//     见 [MaterializeSessionFilters]。
//  4. **判别联合换成 Go 的表达法。**过滤器是封好的接口加具名变体；
//     `{complete:true; root} | {complete:false; unresolvedParentId}` 换成
//     [LineageTrace] 上的一个 Complete 布尔加两个只在对应分支有效的字段；
//     `{status:'fulfilled'|'rejected'}` 换成 [ProjectionResult] 上的 Err。
//  5. **ProjectMany 是包级函数不是方法。**Go 的方法不能带类型参数，
//     而这个 API 的全部价值就在于「一次观察里折出任意一种投影」。
//     见 [ProjectMany]。
//  6. **文字提取会报错。**DSH 的 `extractSessionEventText` 拿到的是已经带好
//     类型的负载，读不坏。Go 这边负载是字节，解不回来说明日志坏了——
//     那是一件必须说出来的事，不是「这条没有可检索的文字」。
//     见 [ExtractEventText]。
//  7. **本包认识 DSH 的 switch 没有列举的几种取值。**Go 侧多出来的事件类型
//     （request/context、session/end-seed）、回合结束理由（blocked）和内容块
//     （image）在 DSH 那边都落在 default 分支上。本包把它们**显式列出来**
//     并给出同样的空结果，理由写在各自的位置：落在 default 上和被显式判成
//     「没有语义文字」在行为上一样，但只有后者能让下一个读代码的人知道
//     这是想清楚了的。
//  8. **ReadSession 的重放校验拆成了两个函数。**DSH 那边调 `Session.create`
//     把整份日志重放一遍，顺带建出一个活会话。Go 这边活会话类型在 DESIGN.md
//     第八节的第 6 块还没到，但校验的两半都已经有了：[session.ValidateLog]
//     验关系约束，[session.FoldSurface] 验表面层折不折得出来。两半都做，
//     少掉的只是「建出一个活会话」——这个方法本来也不需要它。
//     见 [Engine.ReadSession]。
//  9. **「有没有标题」由一个布尔说，不由零值说。**DSH 的
//     `SessionTitleObservation.title` 是可选字段，缺席就是「这个会话还没有过标题」。
//     Go 这边 [TitleObservation.Titled] 把这件事说出来，和
//     [github.com/snight1983/ds-harness-go/session/sessiontitle.FoldSnapshot] 的第二个返回值一致。
//     用一份零值快照表达「没有标题」会把它和「标题是空串」混成一件，
//     而那两件事在界面上不一样。见 [Engine.ReadTitleSnapshots]。
//  10. **seq 不是下标。**DSH 全篇拿 seq 直接索引事件数组，靠的是「日志从 0 开始
//     连续、一条不删」。本仓库的日志会从最老的一头弹出事件
//     （见 docs/session-log-limit.md），所以一份日志的起始 seq 是个变量：
//     定位一律先减起点、减完当场校验对上的还是同一条。落在起点之前的 seq
//     是**被弹掉了**，答 [CodeEventNotFound]；落在现存范围内却对不上，
//     才是这份日志坏了。见 [Engine.ReadEvent] 和 [TraceEvent]。
package sessionquery
