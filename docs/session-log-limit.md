# event 存储改造

已定。2026-09-02。第 1–12 步全部已实现。

统一说法：`session.jsonl` 里第一行是 header，之后每一行一条 **event**。

## 为什么

1. event 无上限追加，开会话时整份读进内存（`session/persistence/jsonl/backend.go`
   的 `readPrefix` 读完整个文件再从第 0 字节解析）。
2. 服务端不能把 event 存在本地目录。

模型看不到这些 event 的大部分：压缩只处理拼给模型的那一串消息，窗口用到 80% 触发、尾巴
留 16%、更早的总结成一段（`feature/compaction/basic/config.go:17-22`、`region.go:61-71`）。
全量 event 的用途是审计，不是恢复。

## 定下来的

1. 按 event 条数限制，默认 1000 条。
2. 可传参覆盖。
3. 单独一个接口承担，不揉进现有写入路径。
4. 先进先出。
5. 暂时不提供对外的 CRUD 接口。
6. 服务端不能把 event 存在本地目录，迁到数据库。
7. event 全部进表，不拆对象存储。
8. jsonl 后端不保留，数据库是唯一后端。
9. 支持两种数据库：Postgres 和 SQLite。
10. 键值存储中枢那边也补一个 SQLite 后端。
11. 不加新的消息 ID 字段。`sessionlog.Event` 已有 `Seq`（会话内从 0 起、逐条加一），够用了。
12. `tool/result` 的字节上限不改，`feature/outputretention` 和 `spill` 保持原样。
13. **「日志的头部会被删」是权威前提。** 代码里任何和它冲突的旧断言全部改成服从它，
    没有例外。不是逐处打补丁——那些断言是同一条旧前提（只追加、永不删除）垮掉之后
    冒出来的症状。
14. 被弹掉的 event 对应的状态丢了就丢了。待办清单少几项，接受。
15. 按 seq 找不到 event 不再一律当错误。分两种：seq 比当前最小的还小，是被弹掉了，
    正常；seq 落在现有范围内却找不到，是日志真坏了，照旧报错。FIFO 弹的是连续的
    一头，这两种分得开。

第 9、10 条的理由：用哪个数据库是写服务的人的决定，不是库的决定。库这边只认
`database/sql`，连接和后端由装配方给。

## 现状（已核过的）

`feature/persistence/backend.go` 这道缝本来就是为多后端留的，数据库后端要填的位置都是
现成的：

| 能力 | 数据库后端怎么办 | 依据 |
| --- | --- | --- |
| `Backend`（必需五方法） | 全部实现 | `backend.go:44-94` |
| `SeekableBackend` | 实现得了，`WHERE seq >= ?` | `backend.go:103-117`，jsonl 实现不了 |
| `LocatingBackend` | 不实现 | `backend.go:126`：「把所有会话装进一个数据库的后端不实现它」 |
| `ClosableBackend` | 实现，收连接 | `backend.go:133`：「比如一个数据库句柄」 |
| `SupportsRawArtifacts()` | 返回 false | `store.go:34-47`，已有 `ErrRawArtifactsUnsupported` |
| `CommitRepair` 的 `torn` | 恒为 nil，`closers` 照走 | `backend.go:80-90`，撕裂尾巴是文件独有的 |

当时的 `storage/postgres` 是键值存储后端（key TEXT / value TEXT），不是会话后端，不能直接
拿来用。可复用的只有「驱动由装配方给一个已经建好的 `*sql.DB`」这个约定。（第 12 步之后
两者收进了同一层，共用同一份介质。）

## 原则

**总则：event 会被删除，这是既成事实，不是可选项。** 任何模块、任何方法只要还建立在
「日志是完整的」这条旧前提上，就是它自己错了。

凡是因为「头部被删掉了」而报错或者抛异常的地方，只有两种解释——**要么是使用方法错了，
要么是设计错了**。改那一处，不许反过来要求删除机制让步。

1. **起始 seq 是变量。** 任何地方不许假设它是 0。
2. **位置一律用 seq 表达，不用条数、不用下标。** 确实要下标的地方当场减起始 seq，
   而且减完要校验 `events[index].Seq == seq`。
3. **「空」不用 0 表示。** 旧代码拿 `cursor == 0` 当「还什么都没落盘」，新前提下起始 seq
   本身就可能不是 0。
4. **按 seq 找不到 event，先分两种再决定报不报错。** 小于起始 seq 是被弹掉了，正常；
   落在现有范围内是日志真坏了，报错。
5. **状态残缺不阻断读。** 被弹掉的 event 对应的状态丢了就丢了，读照常走完。

## 计划

顺序是有讲究的：第 1 到 6 步在删除机制**还没打开**的时候做，做完行为和现在一模一样，
现有测试全绿就说明没改坏。第 7 步才真正开始删。反过来做的话，每一处口径问题都会
表现成一个没有参照物的偶发 bug。

0. **改 seq 的产生点。** `harness/session` 的会话记一个 baseSeq，`Seq()` 和
   `event.Seq` 都改成 `baseSeq + len(log)`；起点由装配方**显式给**（`Options.BaseSeq`、
   `CreateOptions.BaseSeq`、`RestoreOptions.BaseSeq`），不从 `Seed[0].Seq` 推。
   连带 `harness/session/fork.go` 的分叉边界：它是个 seq，先减起点再当下标。
   不先做这一步，后面每一步都是在一个会撞号的地基上砌墙。
1. **起始 seq 进得来。** 存储侧交出「这份日志从哪个 seq 起」，`StoredPrefix` /
   `StoredSuffix` 带上它。这是后面五步的前提。
2. **拆 `cursor`。** `coordinator.go:110-111` 那一个字段现在同时是「落盘条数」和
   「下一条要写的 seq」，12 处引用一半当 seq 用（`coordinator_chain.go:204,217`、
   `coordinator_write.go:74,444`）、一半当下标用（`coordinator_write.go:85,203,313`、
   `coordinator_prepare.go:308`）。拆成两个字段，各归各。
   `coordinator_write.go:296` 的 `cursor == 0` 换成显式的「还没落过盘」。
3. **改拿 seq 当下标的两处。** `coordinator_chain.go:318-321`、`session/surface.go:430`
   （`FoldSurface` 收 baseSeq 参数，:424 注释跟着改）。
4. **改 projection 的地板和判据。** `checkpoint.go:71-89` 的地板、:163 的
   `baseSeq > 0`，都换成起始 seq。

   实现时改了一处：**地板留着 0，判据挪进 `Restore`。** `RestoreFloor` 跑在读**之前**，
   它问不出起点——要问就得先读整份，而它存在的全部理由就是不读整份。所以它交出来的 0
   读作「从存档现存的最前面读起」而不是「从 seq 0 读起」，这是一个**请求**；起点的实情
   由那次读答复（`StoredSuffix.BaseSeq`），调用方再把它当作 `Registry.Restore` 新增的
   第四个参数 `baseSeq` 传进去。判据因此从 `baseSeq > 0` 换成「这截尾巴是不是现存的
   全部」（`fromSeq <= baseSeq`），行的可用下界也从 `fromSeq-1` 换成
   `max(fromSeq, baseSeq)-1`。
5. **改 `replacementRange` 的两种找不到**（`sessionlog/surface.go`），按原则第 4 条。
6. **`projectioncache` 的作废条件**：缓存行记的 seq 落在被弹区间之内就作废，走它已有的
   回退路，重折起点按起始 seq。

   这一步由第 4 步的判据顺带办掉，不用另写作废逻辑：行的水位落在被弹区间里时，
   `RestoreFloor` 给出的地板也落在那儿，从它读回来的就是现存的全部，于是 `Restore` 里
   `row.Seq >= max(fromSeq, baseSeq)-1` 不成立、行被丢掉，每个单元在现存这一段上从
   `Init` 重折。这条路不报错、也**不多读一次**——那次读已经是整读了。
7. **写数据库后端。** 新包实现 `Backend` + `SeekableBackend` + `ClosableBackend`。
   - 两张表：会话头一张，event 一张，event 表主键 `(session_id, seq)`。
   - `AppendBatch` 一个事务：`materialized` 为假时头和第一批 event 一起提交（`backend.go:67-71`
     要求这一下是原子的）。
   - `Revision` 要来源限定（`revision.go:7-14`）：同一份没变过的日志观察多少次都是同一个值，
     且两个独立存储的令牌不许比出相等。
   - `List` 查头表。
   - 第一版不做迁移：没有旧表、没有旧数据，直接建表。

   实现最初落在 `feature/persistence/postgres`，第 12 步把它搬到了 `adapter/datastore/sessionstore`。
   有两处比计划里多出来的东西，都是这份介质
   逼出来的：头表上多一列 `next_seq`，因为一份被弹空的存档 `MIN(seq)` 是 NULL，答不出
   「下一条写在哪儿」，而恰恰是那时候调用方最需要它；读路一律开可重复读的只读事务，
   因为「头、event、令牌」是三句语句，缺省的 READ COMMITTED 会让交出去的令牌配着
   另一份日志，而 `StoredPrefix.Revision` 要求它「标识恰好这些值」。

   **这批用例要一个真的数据库才跑得到**。这一步当时只有 Postgres，所以没有
   `DSH_POSTGRES_DSN` 就整批跳过，CI 上用 `DSH_REQUIRE_POSTGRES` 把跳过变成失败。
   第 11 步补上 SQLite 之后跳过没了：缺省跑 SQLite，设了 DSN 才改跑 Postgres。
8. **加 event 条数上限。** 单独一个接口，FIFO，默认 1000、可配。删除机制到这一步才打开。
9. **删掉 jsonl 后端**，连带 `session/persistence/jsonl/path.go` 那套目录名编码
   （`encodeSegment`、`projectKeyMaxBytes = 251`、`noCwdDir`）。
   `format.go:235,368` 那两处随包消失，不用单独改。

   十八个文件、四千行，一个 Go 使用方都没有，所以删掉这件事本身没有连带修改。
   真正要收的是它留下的那些**指向**：裁决表上十八条 `PORTED` 的 `go_ref` 落了空
   （整包三十四行一并改成 `SKIP`，理由记在 `docs/portmap/decisions.md`）；CI 上
   两个 fuzz 目标随包消失；`docs/performance-baseline.md` 里那组走真 I/O 的数出自
   这个包，**留白而不是换一组**——换成另一份介质的数，会被当成现在这条路的参照。

10. **`sessionquery` 对齐。** 它现在读本地文件，要改走数据库后端。

    「读本地文件」说的是上游 DSH，不是这份移植：Go 这边一次文件系统调用都没有，
    它早就只认 `Persistence` 那个两方法的窄接口（`List` + `Inspect`，
    `feature/sessionquery/corpus.go:75-90`），而 `persistence.Store` 满足它——换数据库后端
    这件事在装配那一侧，本包一行不用改。

    真正要改的是这一步之前**没人查过的** seq 口径。上面「要改的地方」那一遍全仓审查
    漏了这个包，它有三处，都是同一条旧前提（seq 就是下标）：
    `eventAtSeq`（`tracing.go`）拿 seq 当下标；`TraceEvent` 的 `records[seq]`
    同样；`Engine.ReadEvent` 的窗口两端是 seq，却拿去当切片边界、还用 `0` 和
    `len-1` 去夹——起始 seq 非零时它切出一个起点大于终点的区间，**当场 panic**。
    三处都改成先减起点、减完校验（原则第 2 条）。

    连带纠正一条**从来就不成立**的注释：`tracing.go` 原先写着「analyzeEventLog
    已经验过 seq 连续」。`session.Trace.Validate`（`session/invariant.go:124`）
    验的是**严格递增**，不是连续，这两件事不一样。

    调用方那一侧不用分「被弹掉」和「日志坏了」两种错：seq 是谁给的就决定了答什么。
    用户点名一条不在的事件，答「找不到」——它是被弹掉了还是压根没有过，对提问的人
    是同一件事；而 `CurrentSurfaceEvents` 手上那些 seq 是 `FoldSurface` 从**这一份**
    日志上折出来的，找不到就只可能是日志坏了，照旧报错。
11. **补 SQLite 后端。** 决定第 9、10 条要的那两个 SQLite 实现。

    第 7 步没有顺手做掉，因为选驱动是个独立的决定，不该塞进「写数据库后端」里：
    `mattn/go-sqlite3` 要 cgo，而本仓库按 `CGO_ENABLED=0` 交叉编译（见 `CLAUDE.md`）；
    纯 Go 的 `modernc.org/sqlite` 不要 cgo，但它是一份很大的依赖。先定驱动再动手。
    排在最后是因为它不挡任何人：上面十步走完，Postgres 那条路已经整条通了。

    第 12 步之后这一步小了一圈：不再是「两个实现」，而是**加一个 `Dialect`**。
    会话存档和键值中枢共用同一份介质，方言背后就那么几处分歧（占位符、限定标识符、
    咨询锁、只读事务隔离级别、取大的函数）。

    落下来是 `datastore.SQLite`，驱动定的是纯 Go 那个 `modernc.org/sqlite`——
    要 cgo 的那个和本仓库的交叉编译约定直接冲突，没得选。

    有一处比计划里多出来的：SQLite 没有 `CREATE SCHEMA`，而它那个 `库名.表名` 里的
    库名只能是 `ATTACH` 上来的名字，那是**连接**上的状态——连接池里每条连接各自带着
    自己的一份，靠它就等于让「这条语句落在哪儿」取决于这次抓到了哪条连接，
    正是 `Dialect.Qualify` 那句话拒绝的东西。所以命名空间折进表名：`"ns.表名"` 是
    一张真的叫 `ns.表名` 的表。分隔符取点而不是下划线，因为下划线在合法名字里出现得了，
    `a` + `_` + `x_y` 和 `a_x` + `_` + `y` 会拼成同一个名字，两张本该互不相干的表
    塌成同一张。

    还有两件事**留在包外**：`busy_timeout` 和 `foreign_keys` 都是连接上的状态，
    不是语句里的东西，所以进不了 `Dialect`，由装配方在 DSN 上设。

    顺带把一个洞堵了：这一步之前 `adapter/datastore` 那几千行**一行也没跑过**——整批用例
    只在有 `DSH_POSTGRES_DSN` 时才执行，而开发机上从来没有。现在缺省落在一个临时
    目录里的库文件上，`go test ./...` 就整批跑；设 DSN 则同一批用例体换到 Postgres 上
    再跑一遍。两种都要过，因为一种方言自己跟自己是没有分歧的。
12. **把数据库整个收进一层。** 第 7 步和第 10 步走完之后，仓库里有两处各自开连接池、
    各自建表、各自拼 SQL 的代码：会话日志一处（`feature/persistence/postgres`），
    键值中枢一处（`storage/postgres`）。两处的形状是抄来抄去的，连注释里都写着
    「理由同另一个包里那个同名函数」。

    问题不是「重复」这么轻。抄出来的东西会分叉，而分叉只在换后端、换数据库、
    换部署的时候露头，那时候没人记得两边为什么不一样。更要紧的是**调用方凭什么
    知道下面是个数据库**——`session` 和 `storage` 的类型签名里出现 `*sql.DB`，
    等于把「后面用的是哪家库」写进了业务代码。

    所以数据库整个收在 `adapter/datastore` 底下：它只认两种不带领域含义的形状（记录集、
    日志集），业务包只声明自己那道业务接口，由 `adapter/datastore/kvstore` 和
    `adapter/datastore/sessionstore` 两个适配层去实现。依赖方向是反的——适配层认识业务接口，
    业务接口不认识适配层。原来那两个包删掉。

    **这件事光靠约定守不住**：只要有一个人在 `session` 里 import 了 `database/sql`，
    那道墙就没了，而且是悄悄没的。所以配一道跑得起来的门禁 `internal/devtools/dbcheck`，
    三条机械可判的规则——SQL 语句文本只准在 `datastore/` 底下；`database/sql` 和各家
    驱动只准在 `datastore/` 和 `cmd/` 底下；`adapter/datastore` 这个包只准被 `datastore/`、
    `cmd/`、`internal/devtools/` 引用。详见 `docs/modules/datastore.md`。

## 要改的地方

全仓审查过一遍，逐处读代码核过。下面按坏得多严重排。

### 零、seq 的产生点就写着「seq = 日志长度」（会撞号）

`harness/session/session.go:387` 是新事件拿 seq 的地方：

```go
event.Seq = len(s.log)
```

`Seq()`（:251）同样是 `len(s.log)`，注释里把它写成契约：「seq 是下一条事件的序号，
恒等于日志长度」。`:184` 的 `firstLiveSeq`、`:473-506` 的 `headerFoldSeq` /
`contextFoldSeq` 都建立在这条上。

续跑一份被弹过头部的日志：log 从 seq 500 起、手上 1000 条，下一条的 seq 会算成 1000
——而它应该是 1500。新写的事件直接撞掉已有的 500..1499，而且不报错。

下面那几条下标假设全是从这一条长出来的，所以它得**最先**改：会话记一个 baseSeq，
seq 改成 `baseSeq + len(log)`。

同一包的 `fork.go` 也在这条上：`forkSeed` 收的 boundary 是个 **seq**，却直接拿去
`events[boundary]`、`events[:boundary+1]`，还断言 `events[boundary].Seq == boundary`。
起点非零时它切错段，而且是**默默切错**。改成先减起点，并按原则第 4 条把「比起点小」
（被弹掉了）和「落在范围内却对不上」（日志坏了）分开报。分叉要的是那些事件本身、
不是从它们折出来的状态，所以这里没有「残缺着往下走」这条路。

### 一、「回退到从 0 重折」这条退路失效（死路）

`sessionlog/projection/checkpoint.go`：

- `RestoreFloor`（:71-89）在检查点行缺失或版本对不上时把地板拉到 0，意思是「这个键
  必须重折整份日志」。
- `Restore`（:163）的判据是 `!usable && baseSeq > 0` 才报 `ErrCheckpointUnusable`，
  背后是「`baseSeq == 0` 就是整读，整读一定折得出来」。

新前提下整读的 baseSeq 是起始 seq，恒大于 0。于是遇到一行不可用就必报错，而调用方
按错误提示「从 seq 0 重读」永远回不到 0——一条读不出来、也退不回去的死路。

改：地板和判据都换成「起始 seq」，不是 0。

### 二、拿条数当写入进度（静默重复写）

`feature/persistence/coordinator_write.go:276-281`：`cursor = len(stored.Events)`，
再拿这个数去切活会话手上那份种子 `seed[len(stored.Events):]`。

头部被弹掉之后条数变小，于是已经落盘的那一段会被当成「还没写」再追加一遍。不报错。

改：cursor 按 seq 算，不按条数。

### 三、拿 seq 当下标

- `feature/persistence/coordinator_chain.go:318-321`：`fromSeq >= len(events)` 判界、
  `events[fromSeq:]` 切片。改成减去起始 seq。
- `session/surface.go:430`：`FoldSurface` 调 `planSurfaceEvent(..., 0)` 写死 baseSeq；
  :424 的注释也写着「第一条的 seq 是 0」。改成收一个 baseSeq 参数——增量那条路
  （`SurfaceFolder`，:456）本来就有这个字段，只有整折这条写死了。

### 四、按 seq 找不到就当日志坏了

`sessionlog/surface.go` 的 `replacementRange`：压缩写的 `session/replace` 带着被遮住那些
event 的 seq，折的时候按 seq 定位，找不到就返回 `ErrSurfaceViolation`，整份会话打不开。
被遮住的正好是最老那批，也正好是 FIFO 先弹的那批。

改：按「定下来的」第 15 条分两种。

### 五、随 jsonl 一起消失，不用单独改

`session/persistence/jsonl/format.go:235`（注释声明交出的是「seq 从零开始连续的那一段」）
和 `:368`（`event.Seq != len(s.events)`）。这个包整个删掉。

### 六、只是文档措辞

`feature/persistence/store.go:58` 写的是「第一条事件的 seq 必须等于存档里的下一个 seq」。
这条契约本身在新前提下仍然成立，措辞里点明「下一个」是相对存档现有末尾，不是相对 0。

### 核过但不受影响

- `session/surface.go:265` `assertToolResultRewrite`：已经是 `shadowedSeqs[0] - baseSeq`，
  baseSeq 是参数。
- `feature/compaction/toolresultpruner/session.go:58` `baseSeqOf`：取 `events[0].Seq`，本来就是变量，
  而且 :70 还有 `events[index].Seq != seq` 兜底。
- `sessionlog/projection/doc.go:65`、`registry.go:313`：`sessionlog.Trace` 那一侧只保证严格递增，
  投影已经按 seq 比、不按下标切。
- `feature/projectioncache`：缓存行记着「算到 seq N 的状态」。N 落在被弹区间之内时那行
  作废，走它自己已有的回退路。作废之后的重折起点按第一条改。
