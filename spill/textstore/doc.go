// Package textstore 把 [github.com/snight1983/ds-harness-go/spill.Store] 这条接缝
// 实现在 [github.com/snight1983/ds-harness-go/fs.FileSystem] 上：一次写，
// 换回一个不会撞名、也不泄漏会话 id 的句柄。
//
// 新增: DSH 那边挂在这条缝上的是 spill-local（packages/spill/spill-local）。
// 它整包在本仓库的裁决里是范围外——它建在 `node:path`、`os.tmpdir()` 和
// 0600/0700 那几个权限位上，而这台服务没有硬盘、也没有目录这个概念。
// 但那只是**减法**：接缝仍然是空的，装配方无从装配。这个包补上加法，
// 做法和 [github.com/snight1983/ds-harness-go/attachment/imagestore] 那次一样——
// 换介质，不换语义。
//
// 所以本包的 `// 源:` 注释指向的是那个范围外的包：**起名和归拢这套办法是从它来的**，
// 换掉的只是它落到哪儿。分歧一律另起 `// 新增:` 写明。
//
// # 名字是派生出来的，不是拿来的
//
// 键长这样：
//
//	<Root>/session-<sha256(会话 id) 的前 12 位>/<16 位随机 hex>-<净化过的建议名>
//
// 三段各有各的活：
//
//   - 会话 id 取**哈希**而不是原文。它会一路走进
//     [github.com/snight1983/ds-harness-go/spill.Ref.Locator]，而那个句柄是要渲染
//     给模型看的——原文进去，会话 id 就顺着交给模型了。顺带还解决了第二件事：
//     一个会话 id 长什么样本包不知道，哈希之后它必然是 12 位十六进制。
//   - 随机那一段是**不撞名的唯一来源**。同一个会话里同一件工具连着外置两次，
//     建议名一模一样，靠名字本身分不开。
//   - 建议名只是给人看的尾巴。它一路来自工具名，而工具名可以由插件自定——
//     接缝上明写它「永远不是路径」。所以它先过 [safeName]：
//     只留 `[A-Za-z0-9._-]`，其余一律换成下划线。
//
// 净化之后**穿越在构造上就不成立**：分隔符全换掉了，而且这一段前面永远缀着
// 那 16 位随机 hex，所以它既不可能是 `.` 也不可能是 `..`。
//
// 新增: spill-local 的 encodeSegment 做的是**可逆**编码（`~XXXX` 转义，见
// packages/spill/spill-local/src/store.ts:55-68）。这里不要可逆：没有任何一条路
// 要从键反推回建议名，而不可逆的映射让净化后的名字读起来更接近原来那个。
// 撞名由随机那一段负责，不由这一段负责。
//
// # 撞了就报错，不覆盖
//
// 发布走 [github.com/snight1983/ds-harness-go/fs.CreateIfAbsent]。16 位随机 hex
// 撞上是 2^64 分之一，真撞上了那说明介质出了问题，而不是一次该被吞掉的意外——
// 静默覆盖会毁掉在先那份产物，而上层之所以敢把结果挪走，依据正是
// 「挪走的那份还在，一个字都没少」。
//
// # 取回说明必须由装配方给
//
// spill-local 那句取回说明是写死的（packages/spill/spill-local/src/index.ts:159），
// 因为它知道自己那套部署里有一件叫 read 的工具、而且句柄就是一条本机路径。
//
// 这两条在这里都不成立：本仓库不提供 read 工具（spill/policy 里那个名字只是
// 留出来的一个例外），句柄是什么形状取决于装的是哪条
// [github.com/snight1983/ds-harness-go/fs.FileSystem]。所以这段文字是
// [Config.RetrievalHint]，由装配方连同介质一起给。
//
// # 不做启动清扫
//
// spill-local 启动时扫一遍根，把超过 cleanupPeriodDays 的产物删掉
// （packages/spill/spill-local/src/cleanup.ts:274-362）。这一条**不做**，是决定：
//
//   - [github.com/snight1983/ds-harness-go/fs.FileSystem] 上没有「按修改时间扫一棵树」
//     这个原语。真要做就得逐层列目录再逐个 Stat，那在对象存储上是一次全量列举。
//   - 多副本部署下每个副本都会去扫同一棵树，而清扫和别的副本此刻正在写的产物之间
//     没有任何协调。
//
// 保留期在这条接缝上是**介质自己的事**：对象存储有生命周期规则，本地磁盘有
// 运维的定时任务。两者都比一次启动时的尽力而为更靠谱。
package textstore
