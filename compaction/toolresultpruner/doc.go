// Package toolresultpruner 是**不问模型**的那个压缩后端：一条工具结果的正文超过
// 字符预算时，把中间那段换成一个固定标记，只留下开头和结尾。
//
// 源: packages/compaction/compaction-tool-result-pruner/src/index.ts
//
// # 这一层管什么
//
// [Config.Resolve] 验一份字符预算，[New] 用它造一个 [Pruner]，
// [Pruner.MeasureContent] 数码点，[Pruner.PruneContent] 砍。
// [PrunedEntry] 和 [PruneResult] 是一趟砍下来的账目。
//
// 不管的：**在会话上真的砍一遍**。那要一个能追加事件的活会话和一个计量器
// （每条被遮的节点要配一条 compaction/prune 记下它的估价），两样都归
// docs/DESIGN.md 第八节第 6 块，裁决仍然是 PENDING。
//
// # 为什么要有这一层
//
// 上下文被撑爆，最常见的单一原因不是对话本身长，而是**一条**工具结果太大：
// 一次 grep 打回来几万行、一个文件整个读进来。compaction/basic 那条路要发一次
// 总结请求，慢、花钱，而且结果不确定；而一条超大的工具输出里，真正有用的几乎
// 总是开头那几屏和结尾那几屏。
//
// 这一层因此有两个 compaction/basic 给不了的性质：
//
//   - **确定。**同一份输入砍出来永远是同一个结果，所以它在重放里原样复现得出来。
//   - **不花钱。**一次调用都不发。
//
// 代价是它只会砍工具结果，砍不了对话本身。两个后端是互补的，不是二选一。
//
// # 这里和 DSH 不一样的地方
//
//  1. **`codePointLength` 整个消失。**DSH 写 `Array.from(text).length` 而不是
//     `text.length`，是因为 JS 的字符串是 UTF-16 码元序列，`.length` 会把一个
//     增补平面的字符数成两个、`slice` 还会把代理对劈开砍出乱码。Go 的字符串是
//     UTF-8 字节序列，数码点是 [unicode/utf8.RuneCountInString]，
//     按码点切是 `[]rune(s)`——两件事标准库都有，不需要再造一个函数。
//
//  2. **HeadChars 和 TailChars 用指针。**这两个的**零是有意义的**——一个码点都不留
//     ——而默认值是 4096 和 1024。拿零当「没给」会把这个明确的意思静默改写成默认值。
//     ThresholdChars 不需要指针：合法取值是 ≥1，零本来就不在里面。
//
//  3. **拼错键的检查和 `Number.isInteger` 都走了。**DSH 那边配置解出来是 unknown，
//     一个 `threshold: 10` 会被静默忽略，所以它要维护一张 CONFIG_KEYS 白名单；
//     整数检查同理，JS 的 number 是浮点。Go 这一侧两件事都由类型担着。
//
//  4. **`deepFreeze` 和 `structuredClone` 都走了。**DSH 靠它们让一份解析后的配置
//     「脱离原对象且深不可变」。Go 的结构体值本来就是拷贝，[Defaults] 每次交出
//     一份新的，[Pruner] 把自己那份收成不可导出的字段加一个 [Pruner.Config] 读取方法。
//
//  5. **`pruneContent` 的「在预算之内」从 null 变成第二个返回值。**Go 里 nil 切片和
//     「空内容」分不开，而这两件事的意思完全不同：前者是**没砍**，原件原样留着；
//     后者是砍出了一条空的正文，那是一条要拒掉的结果。
//
//  6. **`extends Service` 变成一个普通的值。**DSH 构造时把自己注册进 cordis 上下文，
//     还声明 `static inject = ['tokenMeter']`。Go 里没有那个注册表，装配方自己拿着
//     这个值；计量器是 `pruneSession` 才用得上的（每条被遮节点要记一条估价），
//     跟着那一半一起留到第 6 块。
//
//  7. **`pruneSession` 那一半不在本包。**它要 `session.append` 分配 seq、
//     要 `ctx.tokenMeter.estimateMessage` 定价，两样都归第 6 块，
//     裁决仍然是 PENDING——和 compaction 包里 `CompactionEngine` 的处理一样。
//     不变量那一侧 DSH 装的是一个**空**安装器，照本仓库的成例整条 SKIP。
//
// # 覆盖率为什么到不了 99%
//
// docs/DESIGN.md 第九节给纯逻辑包定的线是 99%，低于这个数要在源码里写明为什么。
// 没覆盖到的是 [Pruner.PruneContent] 末尾那两条自检——给一份验过的预算它们报不出来，
// 而 [New] 是造 [Pruner] 的唯一入口，所以构造不出走到那里的输入。
// 留着而不是断言掉的理由写在那两行上面：它们守的正是上面那段下标算术，
// 而那段算错的后果是**静默**的。
package toolresultpruner
