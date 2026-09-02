# 重大裁决记录（人写的，可以改）

`portmap.tsv` 是逐个符号的裁决表，一行一个符号，写不下理由。
本文件放**整包级别的、值得单独复核的**判断：为什么整整一个包没有对应的 Go 代码。

每条都按同一个格式写：DSH 那个包在干什么、它为什么存在、Go 里那个前提还成不成立、
以及**能力落在哪儿**。最后一项是关键——裁成 GO_NATIVE 不等于这件事不用做了，
只等于它不在这个包里做。

---

## typert/generator —— 整包 GO_NATIVE / OUT_OF_SCOPE

**规模**：6251 行源码，是第 1 层里第二大的包，裁决表里 91 个符号。

### 它在干什么

一个**构建期**的工具。它用 TypeScript 编译器 API（`ts.Program` + 类型检查器）静态分析
DSH 自己的源码树，产出：

1. `lib/typert.host.js` / `lib/typert.client.js`——里面是 Zod schema 和一个 `TYPERT` 对象；
2. 配套的 `.d.ts`；
3. 仓库文档用的 Cordis 目录（`cordis-catalog.ts`，1065 行）。

### 它为什么存在

因为 **TypeScript 的类型在运行期是不存在的**。类型信息在编译时被擦掉，运行期想知道
「这个参数是什么形状」只能靠构建期先把它抄下来。这个包就是那台抄写机。

### Go 里这个前提不成立

Go **不擦除类型**。`reflect` 在运行期能拿到结构体的字段、字段类型、结构体标签、方法集。
DSH 要花 6251 行、外加一整个 TypeScript 编译器才能在构建期算出来的东西，Go 里是
标准库里现成的一次调用。

照搬它需要在 Go 里实现一个 TypeScript 解析器和类型检查器。那是「从头造轮子」的定义，
而且造出来之后**没有输入也没有输出**：ds-harness-go 是纯 Go，没有 TypeScript 源码可分析，
也没有 `.d.ts` 要生成。

`src/model.ts`（91 个符号里占 41 个）把这件事挑明了——它是一份**描述 TypeScript
这门语言本身**的数据模型：

- `KeywordTypeName`：`any` / `never` / `unknown` / `void` / `bigint` / `symbol`
- `TypeOperatorName`：`keyof` / `readonly` / `unique`
- 条件类型、映射类型、JSDoc、`.ts` 文件里的行列号

这些词在 Go 里没有指称对象。把它们译成 Go 类型，得到的是一个**用 Go 写的、描述
TypeScript 类型的库**，不是 ds-harness-go 的能力。

### 能力落在哪儿——查证过的

裁掉一个包之前得先确认没有东西真的靠它。查证结果：

- **运行期没有任何东西读那份文档元数据。** `toJSONSchema` 在整个仓库里除了它自己的
  定义和一份文档目录之外**零个调用点**（`typert/registry/src/service.ts:587`）。
  模型看到的工具 schema 走的是另一条路：`llm/llm/src/types.ts:333` 的
  `ToolSchema.parameters` 是一个普通的 `Record<string, unknown>`，由定义工具的人自己给，
  不是从 typert 推导出来的。
- **`typert/generator` 不是任何运行期包的依赖。** 全仓库只有两个消费者：
  `scripts/gen-cordis-catalog.ts`（生成仓库文档）和各包的 tsdown 构建配置。
- 真正被 `core/agent`、`core/session`、`api/gateway` 等 7 个包依赖的是
  **`typert/registry`**（第 2 层），不是这个生成器。

所以能力落点是：**`typert/registry` 的 Go 版本**。它要用 `reflect` + 结构体标签在运行期
回答生成器在构建期回答的那些问题（这个 key 的 schema 是什么、这个端点怎么调、
参数怎么校验）。到第 2 层时按这个标准验收，不是靠这份说明。

### 逐文件的裁法

| 文件 | 裁决 | 理由 |
|---|---|---|
| `src/model.ts` | GO_NATIVE | TypeScript 类型语法的数据模型，Go 用 `reflect` 直接看真类型 |
| `src/analyzer.ts` | GO_NATIVE | TS 编译器 API 的静态分析，Go 里对应 `reflect` 的运行期读取 |
| `src/emitter.ts` | GO_NATIVE | 生成 JS/`.d.ts`，Go 不需要生成代码就有类型 |
| `src/renderer.ts` | OUT_OF_SCOPE | 把模型渲染成仓库文档的文本 |
| `src/cordis-catalog.ts` | OUT_OF_SCOPE | DSH 仓库自己的文档分类法，不是框架能力 |
| `src/workspace.ts` | OUT_OF_SCOPE | 遍历 pnpm workspace 产出构建物 |
| `src/tsdown-plugin.ts` | OUT_OF_SCOPE | 打包器插件，Go 用 `go build` |
| `src/invariant.ts` | GO_NATIVE / SKIP | cordis 胶水，按已定的成例 |

### 想推翻这条的话

推翻它的证据应该长这样：**找到一个运行期调用点，它读的信息 Go 的 `reflect` 给不出来**
（比如源码里的 JSDoc 注释、泛型形参名、条件类型的展开）。上面查证的结论是没有这样的
调用点；真找到了，那就该在 Go 侧补一份「构建期生成、运行期读取」的元数据文件，
而不是移植 TypeScript 编译器。

---

## client/web + client/ui-slots + client/ui-primitives —— 三个包整包 OUT_OF_SCOPE

**规模**（按 alpha.3 重新量过）：739 + 2219 + 14153 = 17111 行，裁决表里
20 + 99 + 287 = 406 个符号。这三个包是 `client/*` 里最先逐符号看完的，
其余 42 个包在下一节。

### 它们在干什么

一条链上的三节，全部跑在**浏览器里**：

- **`client/ui-primitives`**——纯 React 原子组件。46 个 `.tsx` 加同名 `.module.css`：
  `Button` / `Modal` / `Menu` / `Tooltip` / `Toast` / `JsonTree` / `DiffBlock` /
  `TerminalBlock`、71 个图标、一套 markdown 渲染（含 katex）。非组件的那几个文件
  也全是浏览器 DOM 的活：`clipboard.ts`、`useAnchoredPosition.ts`、
  `useDismissOnOutsidePointer.ts`、`pointer-grace.ts`、`ansi.ts`（把终端转义序列
  渲染成带样式的 DOM）。
- **`client/ui-slots`**——插槽注册表。它回答的问题是「**哪个 React 组件填进哪个具名
  的界面窟窿**」：`SlotCore.register` 带优先级与遮蔽语义，`entriesOfSlot` 投影出
  某个槽当前该渲染的组件表，`store.ts` 是给组件用的 immer 草稿动作 + 选择器 hook，
  `renderer.ts` 是渲染器安装缝和 locale 绑定（让切语言时组件重渲）。
- **`client/web`**——浏览器启动内核。读 `window.__DSH_BOOT__`、建 cordis `Context`、
  跑 loader 激活各客户端入口、画一个无框架的加载页、最后把挂载点交给 UI 渲染器。
  `seed.ts` 把 `react` / `react-dom` / `react-dom/client` / `react/jsx-runtime` 这些
  实例塞进冻结的模块表，好让所有插件包共用同一份 React。

### Go 里这个前提不成立

这三个包的产物是**浏览器里的 DOM**。Go 侧没有 DOM、没有 React、没有 CSS module，
也不打算有——ds-harness-go 是一个给 Go 程序用的库，它的输出是数据结构和 HTTP 响应，
不是渲染树。把 `SlotCore` 译成 Go 会得到一个「用 Go 写的、注册 React 组件的注册表」，
而 Go 侧永远没有 React 组件可注册。这跟 `typert/generator` 是同一类判断：
译出来的东西**既没有输入也没有输出**。

### 查证过的两件事

裁掉整包之前查了两条最可能翻案的线索。

**其一：`window.__DSH_BOOT__` 这个 wire 契约不在 `client/web` 里。**
`client/web` 只是**读**它（`client/web/src/boot.ts:62`）。定义与产出都在别处：
`WebBootGraph` / `WebBootEntry` 的类型定义在
`client/modules/src/client/manifest.ts:66`，组图的是 `client/modules` 的 node 半
（`client/modules/src/index.ts:271` 把它作为一行 `global` 注入），把它渲进 HTML 的是
`host/webserver`（`host/webserver/tests/webserver.spec.ts:211-225` 钉住了
`globalThis["__DSH_BOOT__"] = ...` 这个渲染文本和它的 `<` 转义）。

所以 boot manifest **是**一个真的、Go 侧要产出的 wire 格式，但它的落点不在这三个包里。
它被劈成两半，两半都在移植范围内：

- **渲染成 HTML 的那一半已经做完了**——`host/webserver` 同属第 1 层，已裁 PORTED，
  Go 侧就是 `webserver.IndexInjection` + `webserver.RenderIndexInjections`。
  `__DSH_BOOT__` 对它而言只是一行 `kind: 'global'` 的注入，转义与顺序都已经在里面。
- **组出那张图的那一半也裁完了，结论是不移**——`client/modules` 现已全部落表（54 行，
  全部 OUT_OF_SCOPE）。按上面这个标准验收之后的结论是：它扫描 `dsh.client` 入口组出
  `WebBootGraph`，产物是给浏览器 loader 吃的 bundle 图，两端（`dsh.client` 入口声明、
  `window.__ModuleLoader__` 物化）都在浏览器侧；Go 侧没有要装载的浏览器 bundle。
  webserver 那一半要的只是「一行 `kind: 'global'` 的注入」这个契约，它已经在 Go 里。

`client/web` 是这个契约的**消费端**，消费端跑在浏览器里。

**其二：`ui-slots` / `ui-primitives` 没有任何服务端消费者。**
按 `package.json` 依赖查全仓库（数字已扣掉各自那份自我引用）：
`dsh-client-ui-slots` 有 36 个消费者，33 个在 `packages/client/*` 下；
`dsh-client-ui-primitives` 有 31 个，29 个在 `packages/client/*` 下。
落在 `client/` 之外的一共三个，逐个看过导入点，用的全是各自包的**浏览器半**：

| 包 | 用了哪个 | 用在哪 |
|---|---|---|
| `extensions/ui-cordis` | 两个都用 | `src/client/CordisPanel.tsx` 等 5 个 `.tsx` |
| `session-query/session-log-export` | 两个都用 | `src/client/Dialog.tsx`、`src/client/HeaderAction.tsx` |
| `test-support/client-runtime` | 只用 ui-slots | 名字里就写着 client |

一个 Node/服务端文件都没有。

### 能力落在哪儿

| 这三个包提供的 | 落点 |
|---|---|
| boot manifest 渲染进 HTML | `host/webserver`（同第 1 层，已 PORTED：`webserver.RenderIndexInjections`） |
| boot manifest 组图（`WebBootGraph`） | **不落**——`client/modules` node 半已裁 OUT_OF_SCOPE（54 行），产物是浏览器 bundle 图 |
| 插件 bundle 的路由与增量扫描 | **不落**——同上 |
| 组件注册、渲染、样式、图标、markdown | **不落**——Go 侧没有渲染面 |

### 逐文件的裁法

三个包统一按同一条线裁：`src/**` 全部 OUT_OF_SCOPE（浏览器渲染面），
`src/invariant.ts` 按已定的 cordis 成例（`name`/`inject` → GO_NATIVE，
空 installer 的 `apply` → SKIP），`tsdown.config.ts` → OUT_OF_SCOPE。

### 想推翻这条的话

推翻它的证据应该长这样：**找到一个在 Node 侧运行的文件（不在某个包的 `src/client/`
下）导入了 `ui-slots` 或 `ui-primitives` 的运行期导出**——那说明插槽注册表被当成了
一个与渲染无关的通用注册表，Go 侧就得有对应物。上面按 `package.json` + 导入点两层
查过，没有这样的文件。

另一种推翻方式是：**在 `client/web` 里找到一段不是浏览器专属的逻辑**。查证的结论是
没有——它的 10 个源文件里有 2 个是 CSS，其余全部触碰 `globalThis` / `HTMLElement` /
React 实例。

---

## client/* —— 四十五个包整支不移（四十四个浏览器面，加一个已解散的包）

**规模**：裁决表里 3330 行，分布在 45 个包、672 个文件上；上游还在的 44 个包合计
162487 行 TypeScript。裁决分布：`OUT_OF_SCOPE` 3321、`GO_NATIVE` 6、`SKIP` 3
（后两桶是 `client/web`、`client/ui-slots`、`client/ui-primitives` 三个包的
`src/invariant.ts`，按上一节已定的记法拆开落桶）。

上一节（`client/web + client/ui-slots + client/ui-primitives`）只覆盖了其中 3 个包、
406 行。这一节补齐剩下的 42 个包，并把已解散的 `client/runtime` 单独交代。

### 这一节修的是什么

`client/*` 的裁决方向从来没有争议——它是这套产品的 Web 表层，Go 侧没有 DOM。
有问题的是**理由的粒度**。返工前 3330 行只共用 **71 句**理由，其中最粗的几句是：

| 包 | 行数 | 返工前那一句 |
|---|---|---|
| `client/ui-chat` | 331 | 「聊天会话的渲染目标、节点定义、渲染器和详情面，源码里直接带 css-modules 声明。」 |
| `client/runtime` | 322 | 「SlotRegistry 与 SessionRuntime 拥有 Session，client-side 投影消费」 |
| `client/ui-conversation` | 573（2 句） | 「聊天视图、编辑器 dock 属 React 组件、键盘消息提交、图片拖放」 |
| `client/connection` | 265（2 句） | 「浏览器 HTTP POST 与双条只下行 WebSocket、Host 头信任栅栏」 |

另有 **465 行**的理由是「包级裁决 ／ alpha.3 新增符号，按本包已有的包级裁决处理：
同一道接缝、同一个理由」——那是一次自动补扫留下的尾巴，等于把「跟上一版一样」
当成了理由。这跟 `api/*` 那一节要修的是同一个毛病，而且这里量最大。

现在 3330 行有 **483 句**不同的理由。粒度和 `experimental/*` 那一节一致：
**大包按子树给理由，小包按文件给**。每一行的形状固定为三段——包级裁决（说清这个包
是什么、它的语义属主在哪）＋ 本处（这个文件/子树在包里担什么角色）＋ 指回本节。

### 四道跨包接缝（这是逐项重写才发现的）

按包名逐个核对 alpha.3 的目录之后，发现四处上一版快照没有的位移。它们不改裁决，
但改理由——「这个符号不见了」和「这个符号搬到了那里」不是一句话。

**其一：`client/runtime` 整包解散。** alpha.3 里没有这个目录，全仓也没有任何
`package.json` 还依赖 `dsh-client-runtime`。它的 44 个文件被拆到五处：

| 原位置 | 去处 |
|---|---|
| `sessions/{manager,service,session,lineage,notifier,projection-store,queue-mirror,remotes}.ts`、`ordered-baseline.ts`、`time-zone.ts`、`agents/scope.ts`、`contract/{session,sessions}.ts` | `api/session-controller/src/client/**` |
| `workspaces/**` | `api/workspace-controller/src/client/**` |
| `conversation-context`、`steering-history`、`tool-call-tree`、`partial` | `client/ui-chat/src/client/{model,conversation-nodes}/**` |
| 会话注册表、`context-provenance`、`request-inspection`、`conversation.ts` | `client/ui-conversation/src/client/{conversation,contract}/**` |
| `settings-scope` → `client/ui-settings`；`subagent-lineage` → `client/ui-subagent` | 各自包内 |

六个文件查不到接替者（`sessions-port`、`assistant-timing`、`failure-display`、
`pending`、`provide`、`workspaces/path`），这六个的理由里就直说查不到，不编。

**其二：`client/ui-conversation` 一劈为二。** 它的 `src/client/chat/**` 与
`src/client/conversation-nodes/**` 两棵子树整体搬去了新包 `client/ui-chat`
（逐文件核对过 40 处）。这就是 `ui-chat` 为什么在 alpha.3 里凭空多出 17948 行。

**其三、其四：两个组件级搬迁。** `reference/ReferenceIcon.tsx` → `client/ui-primitives`；
`skeleton/ApprovalPanel.tsx` → `client/ui-approval`；另外
`skeleton/DetailsPanel.tsx` 和 `chat/tool-node-reader.ts` → `client/ui-chat/src/client/details/`。

### 704 行 STALE 分两类，理由也分两句

`client/*` 有 704 行 `STALE:`（portcheck 对表里有、清单里没有的行的标记）。
按「文件还在不在」切开：

- **478 行**落在 alpha.3 里已经不存在的文件上。这类行点名接替它的位置
  （上面四道接缝的成果），或者明说没查到。
- **254 行**落在**还在的文件**上——文件没删，是这个导出从文件里删了。最集中的几处：
  `client/connection/src/client/index.ts` 60、`src/client/api.ts` 58、
  `client/ui-conversation/src/client/index.ts` 35、`contract/slots.ts` 21、
  `client/ui-slots/src/store.ts` 13、`client/ui-input-trigger/src/types.ts` 12。
  这类行的理由是「文件还在，这个导出已经不在它里面了；裁决不变，本行是上一版快照
  留下的记录」——不能跟上面那类混成一句。

### 为什么整支不移

44 个还在的包分成四种角色，四种都止步于同一个前提：

| 角色 | 例 | Go 侧为什么没有落点 |
|---|---|---|
| React 组件与 DOM 交互 | `ui-chat`、`ui-trajectory`、`ui-settings-*`、`ui-primitives` | 产物是浏览器里的渲染树。Go 侧没有 DOM、没有 React、没有 CSS module |
| 浏览器侧状态容器 | `store`、`ui-slots`、`ui-renderer` | Zustand/Immer 的不可变快照＋订阅模型，是为 React 重渲设计的。Go 侧的并发状态用 `sync` 与 channel |
| cordis 装配与模块加载 | `web`、`modules`、`hmr`、`locale` | Go 侧不移植 cordis 依赖注入，也没有要装载的浏览器 bundle |
| 浏览器到 Host 的那条传输 | `connection` | Go 对外的通道是 `sdk/sdkserver` 与 `acp/acp`，各有自己的线格式，都不经过 `/api` |

要紧的是第二列之外的那件事：**这些包画的东西，语义属主基本都已经在 Go 里了。**
这一节的理由逐条点了名，例如——

| 浏览器包 | 它画的东西，属主在 Go 的哪 |
|---|---|
| `ui-chat` 的节点树折叠 | `session/projection`、`session/turnoutline` |
| `ui-approval` 的审批 composer | `interaction/user-approval` |
| `ui-schedule` 的活动 Schedule 目录 | `schedule/schedule` |
| `ui-conversation` 的发送／取消／历史 | `core/agent`、`session/*` |
| `ui-settings-*` 的表单 | 各 settings 域包 |

所以裁掉的是**画法**，不是**能力**。这也是这些行落 `OUT_OF_SCOPE` 而不是 `SKIP`
的原因：能力在范围内且已经做了，不在范围内的只有那张浏览器表面。

### 想推翻这条的话

推翻的证据和上一节同形：**找到一个在 Node 侧运行的文件（不在任何包的 `src/client/`
下）依赖了某个 `client/*` 包的运行期导出**。上一节按 `package.json` ＋ 导入点两层查过
`ui-slots` / `ui-primitives`，没有这样的文件；`client/runtime` 这次也查了，全仓零依赖。

对 `client/connection` 还有一条单独的推翻路径：**如果 `/api` 那套线格式被定为
ds-harness-go 要对外提供的第三条通道**，那这个包的 wire 契约（而不是它的浏览器实现）
就得进范围。目前 Go 侧对外只承诺 `sdk/sdkserver` 与 `acp/acp` 两条。

---

## api/* —— 五个 BFF 包整包 SKIP（裁的是门面，不是门面后面的能力）

**规模**：`api/gateway` 4378 行、`api/remotes` 405 行、`api/session-controller` 7292 行、
`api/settings-controller` 560 行、`api/workspace-controller` 1426 行，合计 14061 行；
裁决表里 97 + 148 + 218 + 13 + 55 = 531 行。

这一节是**新写的**。此前这 531 行共用五句包级理由，其中 `api/remotes` 那句结尾是
「同上」——「上」在一张按包名排序的表里没有固定所指，等于没写。现在每一行的理由
按**文件**给，各自说自己那个文件在干什么、它包住的能力在 Go 侧落在哪个包。

### 它们在干什么

五个包合起来是 DSH 的 BFF 层：把已经存在的领域能力包成一层 RPC，交给浏览器。
分工是「一个物理承载 + 一份装配清单 + 三个领域属主」：

- **`api/gateway`** 是**物理承载**。它把 `@Remote` 装饰的方法解析成 cordis 服务容器上的
  调用，维护一条 WebSocket 多路复用流（`REMOTE_STREAM_MUX_PATH = '/api/remote.mux'`），
  定义四种下行帧、两个事件端点、以及请求 / 拒绝 / 结果的投影函数。它是一整套
  **自定义线格式**。
- **`api/remotes`** 是**装配清单**，本身不含任何能力。Host 侧把 18 条转发事件挂成
  cordis 监听器塞进一个拉取驱动的队列交给 Gateway；Client 侧把 12 个包的 `/remote`
  贡献项按序挂上、失败逆序回滚。它 148 行裁决里有 119 行落在
  `src/client/index.ts` 一个文件上，而那个文件几乎全是 `export type {}` 转口。
- **`api/session-controller`** / **`api/settings-controller`** / **`api/workspace-controller`**
  是三个**领域属主**：各自 `extends TypertRemoteService`，用 `@Remote` 方法把
  session / settings + credentials / workspace + directoryPicker 这几个命名空间挂上去，
  外加一整套跑在浏览器里的客户端半（Session 那个包的客户端半有 3900 多行）。

### Go 里这个前提不成立——但理由和 `client/*` 不是同一条

`client/*` 那三个包裁掉是因为**译出来的东西没有输入也没有输出**（Go 侧没有 React 组件
可注册）。`api/*` 不一样：它们包住的能力 Go 侧**全都有**。裁掉的理由是两条别的：

**其一，Go 侧的对外通道是另外两条，各有自己的线格式。**
`sdk/sdkprotocol` + `sdk/sdkserver` 是一条，`acp/acp` 是另一条，两条都已移植。
把 `api/gateway` 的帧格式再译一遍会得到**第三条**互不兼容的通道，而它没有客户端——
DSH 那一侧唯一说这套帧的客户端是 `client/*`，已整包裁掉。

**其二，`@Remote` 这个机制本身依赖 cordis 的服务容器和 TS 装饰器反射。**
一个 `TypertRemoteService` 子类不声明自己的方法表，Gateway 在运行时从装饰器元数据里
读出来。Go 没有这层反射，同一件事的 Go 形态就是一个显式的 handler 表——`sdkserver`
里已经是这个形态了。

**其三（针对客户端半），`src/client/**` 跑在浏览器里。**
`api/session-controller/src/client/` 有 20 个文件、3900 多行：观察量快照、React 相关的
投影仓、按「上台的会话」决定作用域生死的分阶段生命周期。这一半和 `client/*` 同类，
按同一条线裁。

### 一处刻意的分歧，记在这里

`api/session-controller/src/client/scope.ts:1-17` 那段注释写着它和 host 侧 `dsh-scope`
有三处**刻意**的分歧：过滤器挂在 actx 自己身上而不是一个独立的 carrier 对象；作用域键
是按值比较的 `SessionId` 而不是对象身份；作用域的是 Agent 的**身份**而不是一个活的
Agent 对象（一个冷会话的 host Agent 早就 dispose 了，而它的客户端 actx 还活着供翻历史）。

Go 侧 `core/scope` 移的是 **host 那一侧**。上面这三处分歧属于浏览器那一侧，没有对应物。
写在这里是因为它容易被读成「`core/scope` 少移了三个特性」——不是，它是另一个平面上的
另一份实现。

### alpha.3 在这五个包之间搬过一次家

`api/remotes/src/agent-lookup.ts` 整个文件在 alpha.3 里没有了，它搬进了
`api/session-controller/src/agent.ts`，导出前缀从 `ApiRemote*` 改成 `ApiSession*`：

| 上一版（`api/remotes/src/agent-lookup.ts`） | alpha.3（`api/session-controller/src/agent.ts`） |
|---|---|
| `ApiRemoteLookupError` | `ApiSessionAgentError`（`:60`，取值改成 `session/not-found` \| `session/agent-busy` \| `gateway/internal`） |
| `ApiRemoteAgentResult` | `ApiSessionAgentResult`（`:63`） |
| `ApiRemoteSessionNotFound` | `ApiSessionNotFound`（`:19`） |
| `ApiRemoteSubagentSessionOwnership` | `ApiSessionSubagentOwnership`（`:22`） |
| `hasApiRemoteSubagentOwner` | `hasApiSessionSubagentOwner`（`:79`） |
| `apiRemoteSubagentOwnershipError` | `apiSessionSubagentOwnershipError`（`:96`） |
| `inspectApiRemoteSession` | `inspectApiSession`（`:111`） |
| `createApiRemoteAgentResolver` | `ApiSessionAgentController`（`:135`，工厂函数改成了类） |
| `ApiRemoteAgentOptions` | **无后继**——构造参数收进了 `ApiSessionAgentController` 的私有 `agentOptions()`（`:483`） |

alpha.3 还在这批旁边新加了 `ApiSessionCwdConflict`（`:30`）与
`ApiSessionPresetConflict`（`:45`），上一版没有前身。

这九行留在裁决表上，`kind` 带 `STALE:` 前缀，每一行的 `note` 点名接替它的那一行。
记在这里是因为它容易被误读成「`api/remotes` 少裁了一个文件」——不是，是那个文件
换了包。裁决本身没变（仍是 SKIP，仍属这一节的包级裁决）。

### 能力落在哪儿——查证过的

| 门面后面的能力 | Go 落点 |
|---|---|
| Session 冷读、列表、搜索 | `session/persistence`（`Coordinator.List`）、`sessionquery` |
| Session 历史分页 | `sessionquery` + `session/persistence` |
| 逐会话整值投影（「更高 seq 覆盖」） | `session/projection`——**Go 移的是产出侧**，`unitCell.observedSeq` 折到最后一条事件；客户端那半是缓存 |
| Agent 激活 / 组合 / 模型选择 | `core/agent`、`preset/agentpresets`、`core/agentdefaultmodel` |
| Session 命令 | `interaction/commands` |
| 队列与作业状态 | `core/agent` 的 inbox、`jobs/jobs` |
| 文件引用发现 | `context/sessionref` |
| Skill 目录 | `skill` |
| 模型注册表 | `llm` |
| settings 的 update / replace / mutate 与 redact | `settings` |
| 凭据的单向写入 | `credentials` |
| Workspace 命令与状态 | `workspace` |
| 转发 Cordis 事件的瀑布 | 不落——Go 没有事件总线，同一件事是显式 Rule 瀑布（成例：`session/telemetry.Rule`） |
| WebSocket 多路复用流与自定义帧 | 不落——`sdk/sdkserver` 与 `acp/acp` 各有自己的线格式 |
| 打开本机目录 / 弹目录选择框 | 不落——需要桌面外壳，Go 这边没有 |
| 浏览器侧的对象层、观察量、投影仓 | 不落——同 `client/*` |

### 逐文件的裁法

五个包统一 SKIP，理由按文件给，逐条落在裁决表的 `note` 列里。分四类：

| 类 | 文件 | 理由的落点 |
|---|---|---|
| 物理承载 | `api/gateway/src/{index,stream-protocol,stream-server,types,remote-error-codes}.ts` | 各写各的；线格式那条落在 `stream-protocol.ts` |
| 装配清单 | `api/remotes/src/{index,remote-events,types}.ts` | 转发事件白名单与它的类型座位分开写 |
| 领域属主（Host 半） | 三个 controller 的 `src/*.ts` | 每个文件点名它包住的 Go 包 |
| 浏览器半 | 所有 `src/client/**` | 按 `client/*` 那条线裁，`scope.ts` 单独写三处分歧 |

`src/invariant.ts` 的三行**跟随本包的包级裁决**，也就是 SKIP。它是 cordis 装配的伴生插件——
`name` 是插件名常量、`inject` 是依赖声明、`apply` 是个空 installer——本身不含能力，
所以整包 SKIP 时它跟着 SKIP。这一点和 `client/web + ui-slots + ui-primitives` 那节不同：
那三个包的 `name`/`inject` 记的是 GO_NATIVE（cordis 装配常量在 Go 里退化成构造参数）、
空 `apply` 记的是 SKIP。两种记法都指向「不移植」，差别只在落哪个桶。
全表 257 个带 `src/invariant.ts` 的包里，**176 个三行统一跟随包级裁决，81 个拆开记**；
拆开记的那 81 个共用同一套记法——`inject` 一律 GO_NATIVE，`name` 记 GO_NATIVE 或 PORTED，
`apply` 记 PORTED 或 SKIP——它们是本仓真的移了的包，加上本文单独立过一节、
逐符号看过一遍的那几个（`client/web`、`client/ui-slots`、`client/ui-primitives`、
`typert/generator`、`host/plugin-inventory`、`test-support/loader-smoke`、
`test-support/acp-snapshot`、`session/session-persistence-jsonl`）。
不打算为了统一桶名再动一遍已定的行。
`tsdown.config.ts` → OUT_OF_SCOPE。

### 想推翻这条的话

推翻它的证据应该长这样：**指出一件 `sdk/sdkprotocol` 和 `acp/acp` 两条通道都表达不了、
而某个 `api/*` 的 `@Remote` 方法表达得了的事**。目前查下来最接近的是三条，都不成立：

1. **「转发事件的 waterfall 档能让远端接管 `next`」**——`api/remotes/src/index.ts:132`
   的 `forwardWaterfall` 把一次 cordis 瀑布的 `next` 调用权交到线上。Go 里没有那条总线，
   需要「远端能否决一步」的地方走的是 `interaction/userapproval` 的显式审批接缝。
2. **「基线 + 增量的重连协议」**——`api/gateway/src/client/snapshot-stream.ts`。
   Go 侧同一个语义由 `session/projection` 的整值投影承担：重连就重取整值，不需要一个
   独立的流层来对齐基线和增量。
3. **「一次 describe 最多 64 个引用的扇出上限」**——`api/settings-controller/src/credentials.ts:20`。
   这是**线上**才需要的界，因为一次已认证的请求不该启动无界的 provider 工作。
   Go 的 `credentials` 是进程内接缝，调用方就是宿主自己，没有这条界要设。

另一种推翻方式是：**这个仓库长出了一个浏览器前端**。那时 `api/gateway` 的线格式就有了
客户端，这一节要整节重写。

---

## experimental/* —— 八个包整包裁掉（六个 OUT_OF_SCOPE，两个 SKIP 且值得抄形状）

**规模**：`experimental/inspector` 15615 行、`experimental/webworker-runtime` 12925 行、
`experimental/agent-team` 2451 行、`experimental/webworker-packer` 1111 行、
`experimental/client-ui-agent-team` 657 行、`experimental/tool-agent-team` 436 行、
`experimental/agent-team-profile` 34 行、`experimental/agent-team-web-profile` 26 行
（都只数 `src/`），合计 33255 行；裁决表里 554 + 590 + 76 + 46 + 22 + 8 + 4 + 4 = 1304 行。

八个包的 `package.json` 全都写着 `"private": true`——上游自己不发布它们。这不构成裁掉的
理由（`host/*` 里也有 private 包被移了），但它解释了为什么这八个包的对外契约可以随时改：
**没有仓库外的消费方**，所以「照抄以保持兼容」这个动机在这里不存在。

这一节是**新写的**。此前的缺陷是可量的：`inspector` 的 554 行共用一句包级理由，
`webworker-runtime` 的 590 行共用另一句，`agent-team` 有 17 行挂着自动补扫的尾巴
「alpha.3 新增符号，按本包已有的包级裁决处理」。1304 行里真正互不相同的理由只有八句。
现在两个大包按**子树**给理由（`inspector` 12 个子树、`webworker-runtime` 11 个子树），
六个小包按**文件**给。

### 三组，三种裁法

| 组 | 包 | 裁决 | 一句话 |
| --- | --- | --- | --- |
| 浏览器宿主 | `webworker-runtime`、`webworker-packer` | OUT_OF_SCOPE | 把 Node 宿主搬进浏览器；Go 进程本身就是宿主 |
| 调试外壳 | `inspector` | OUT_OF_SCOPE | 跨 realm 的 CDP 中枢；Go 侧是 pprof 与 delve |
| 多 Agent 协作 | `agent-team`、`tool-agent-team` | SKIP（**抄形状**） | 全仓库唯一的持久 peer mailbox + 共享任务板 |
| 装配清单 | `agent-team-profile`、`agent-team-web-profile`、`client-ui-agent-team` | OUT_OF_SCOPE | bundle 与 React 组件树，两头都在范围外 |

前两组和第三组的分界要说清楚：**前两组是「Go 侧不需要」，第三组是「Go 侧还没有」**。
把它们写成同一句「experimental 不移」会把一条待办藏进一条裁决里。

### 浏览器宿主：`webworker-runtime` + `webworker-packer`

`webworker-runtime` 是「浏览器里的一套 Node 宿主」。它的 11 个子树各自补一块宿主缺口：

| 子树 | 行数 | 它补的缺口 |
| --- | --- | --- |
| `node/builtin_modules/implemented` | 3769 | 21 个真实现的 `node:*` 模块（`fs` 846 行最大） |
| `node/builtin_modules/mock` | 228 | 5 个显式失败的 stub（`net`、`vm`、`sqlite`、`dns`、`worker_threads`） |
| `node/external_packages` | 373 | 7 个 npm 外部包的替身（`koffi`、`node-pty`、`ripgrep`、`sharp`、`ws`…） |
| `node/globals` | 227 | `process`、`crypto.randomUUID`、Node 形状的定时器句柄 |
| `shell/*` | 2509 | 一个进程内 POSIX shell：解析、词展开、解释器、命令表、Landlock |
| `storage/*` | 1562 | 内存 VFS（`memory.ts` 1023 行）、ustar 归档、gzip 信封 |
| `transport/*` | 802 | page↔worker 的 postMessage 隧道与合成 HTTP |
| `module-system/*` | 622 | VFS 之上的 CommonJS 加载器与 POSIX 路径 |
| `compile/transform.ts` | 625 | 一次 acorn 解析把 ES 模块改写成隧道能跑的形状 |
| `polyfill/async-context` | 182 | `AsyncLocalStorage` 的显式切换垫片 |
| `client/*` | 958 | 页面那一半：隧道客户端、注入表解释、文件系统源选择 |

这一整套的输入是「一个只会跑在 Node 里的宿主树」，输出是「让它不改一行就能在浏览器里跑」。
Go 侧这两头都不存在：`os`、`os/exec`、`path/filepath`、`io/fs` 是标准库，`go build` 出的
可执行文件本身就是宿主。**这里没有需要填的缺口，所以译过来的代码没有调用方。**

`webworker-runtime/src/module-proxies.ts` 把这件事说得最直白——它自称是宿主树
「唯一的平台分叉点」。一个只为跨平台而存在的分叉点，在只有一个平台的仓库里没有位置。

`webworker-packer` 是它的构建期伙伴：`pack.ts`（673 行）把「一个组装好的 profile + 一份包
索引」压成 VFS 基础镜像，`rules.ts` 是 include/exclude 规则表，产物由 `dsh-pack-vfs-image`
命令发出。Go 的构建产物是单个可执行文件，没有需要预先物化的虚拟文件系统。

### 调试外壳：`inspector`

`inspector` 是跨 realm 的 Chrome DevTools 协议中枢，四个平面：

| 子树 | 行数 | 角色 |
| --- | --- | --- |
| `worker/cdp` | 3274 | 把 Runtime / Debugger / DOM / Network 四个 CDP 域投影给 DevTools 连接 |
| `worker/realms` | 1631 | 两种后端：Node 原生 inspector session（host）与浏览器 realm（client） |
| `worker/bridge` | 1225 | Worker 自有的 HTTP 发现端点、CDP 端点、Client 摄入端点 |
| `worker/inspection` | 1139 | 非 CDP 查询的仓库层：Cordis 树快照、fetch 观测、realm 注册 |
| `shared/bridge` | 2543 | 版本化线格式：27 个文件的帧、编解码与精确校验器 |
| `shared/cdp` | 582 | realm 中立的 Runtime / Console / Source / Debugger 类型 |
| `shared/cordis` | 850 | 从活的 Cordis 对象投影出一棵有界语义树 |
| `shared/network` | 137 | 全量 fetch 观测与 SSE 增量解析 |
| `host/bridge` + `host/cdp` + `host/inspection` | 1167 | Host 半：拥有 Worker、全量 `globalThis.fetch` 捕获 |
| `client/bridge` + `client/cdp` + `client/inspection` | 2352 | 浏览器半：Client realm 的 Runtime 执行器与对象句柄 |

Go 侧的对应物是标准库和现成工具：`net/http/pprof`、`runtime/trace`、delve。它们不需要一层
自定义线格式，也不需要「把两个 JavaScript realm 统一成一个 CDP target」这件事——Go 进程
只有一个 realm。`shared/cordis` 那 850 行投影的是 cordis 的 Context/Fiber 树；Go 侧没有
cordis 容器（见 `typert/generator` 一节与本表 `invariants` 的裁法），所以连投影的对象都没有。

`inspector` 里唯一值得单独记一句的是 `shared/bridge/version.ts`：**预发布版本的对端互相拒绝
任何其它版本号**，没有兼容窗口。这佐证了上面「private 包可以随时改契约」那句——它连自己的
线格式都不打算向后兼容。

### 多 Agent 协作：`agent-team` + `tool-agent-team`（**这两个是待办，不是不要**）

这是全仓库唯一带「持久 peer mailbox + 共享任务板」的多 Agent 协作原语，Go 侧**目前没有**
对应物。`subagent/*` 那 8 个包做的是父子关系（spawn / fork / report / control），不是
peer 之间的消息与共享任务：

| 上游文件 | 行数 | 形状里值得留的东西 |
| --- | --- | --- |
| `src/roster.ts` | 486 | 花名册状态机：`TeamMemberPhase = 'provisioning' \| 'active' \| 'failed'`（`types.ts:44`），加 roster 自己拥有的拆除 |
| `src/mailbox.ts` | 338 | 持久投递：`delivery: 'quiet' \| 'wakeup'` 两档（`types.ts:111`、`types.ts:163`），加确认与恢复 |
| `src/task-board.ts` | 297 | 共享任务 DAG 命令，写入靠 `expectedRevision` 做 CAS（`task-board.ts:119`） |
| `src/task-graph.ts` | 69 | 对当前任务快照做完整依赖校验 |
| `src/projection.ts` | 317 | Host 侧从已提交 Session 事件增量折出团队状态 |
| `src/journal.ts` | 73 | 团队事务串行化到 Lead Session 的那一条日志上 |
| `src/lifecycle.ts` | 87 | 共享的准入截止与有界收尾 |
| `src/activity.ts` | 87 | 一次性的变更等待者，与耐久投影分开 |

`tool-agent-team`（436 行）是它的模型侧工具面：发消息 / 领任务 / 改任务，改任务那条把
`expected_revision` 直接暴露给模型（`tool-agent-team/src/index.ts:378`）。

**裁决是 SKIP 而不是 PORTED，也不是 OUT_OF_SCOPE**，含义是：这一版不移，但它是待办而非
弃件。真要移的时候，形状抄这八个文件，实现跟着 Go 侧已有的接缝走——投影走
`session/projection`、日志走 `session/persistence`、子 Agent 供给走 `subagent/subagent`、
工具面走 `core/tools`。**不要**抄的是 cordis 服务声明合并（`index.ts` 里那段
`declare module '@deepseek-ai/cordis'`）和 `TypertRemoteService` 继承——前者 Go 没有这个
机制，后者是 `api/*` 一节裁掉的门面层。

### 装配清单与 Web 呈现

`agent-team-profile`（34 行）和 `agent-team-web-profile`（26 行）的运行时内容是各自的
`dsh.bundle.patch` 文档，`src/index.ts` 只有一句 `export {}`。bundle 是**发行形态**（哪些
插件装在一起），不是能力本身；Go 侧对应的是 `cmd/` 下选哪些包 import，成例是
`example/minimalhost`。

`client-ui-agent-team`（657 行）是 Web 端的花名册、任务板与队友导航，产物是 React 组件树
（`TeamAction.tsx` 425 行）加一份 locales。同 `client/*` 一节：Go 侧没有渲染面。

### 逐文件的裁法

| 类别 | 裁决 | 依据 |
| --- | --- | --- |
| 两个大包的实现文件 | OUT_OF_SCOPE | 按上表的子树给理由 |
| `agent-team`、`tool-agent-team` 的实现文件 | SKIP | 逐文件说形状里留什么 |
| 三个装配 / 呈现包的实现文件 | OUT_OF_SCOPE | 发行形态或浏览器渲染 |
| 各包的 `src/invariant.ts` | 跟随本包的包级裁决：六个 OUT_OF_SCOPE、两个 SKIP | 同 `api/*` 一节：整包不移时伴生插件跟着走，不拆桶 |
| 各包的 `tsdown.config.ts` | OUT_OF_SCOPE | 打包配置；Go 是 `go build` |

### 想推翻这条的话

只有一条会推翻：**要多 Agent 协作**。那时推翻的是 `agent-team` + `tool-agent-team` 那两句
SKIP，另外六个包不受影响——它们裁在「Go 侧不需要」上，和这个需求无关。

前两组要被推翻，前提是这个仓库要在浏览器里跑（`webworker-*`）或者要自建调试协议端点
（`inspector`）。这两件事都不是 agent 运行时的能力，也都各有 Go 侧现成替代。

---

## host/plugin-inventory —— 整包 OUT_OF_SCOPE

**规模**：120 行，裁决表里 10 个符号。第 2 层。

这条比前两条更需要写下来：前两条裁的是构建期工具和浏览器渲染面，**这一条裁的是一个
跑在服务端的 host 包**，而 host 包默认是要移植的。所以理由必须比「它是客户端的」更硬。

### 它在干什么

一个只读的 Remote 服务。`list()` 每次调用直接遍历 `ctx.loader.entries()`，跳过 group
条目，把每条投影成 `{entryId, moduleName, enabled, fiberPhase}`。它**刻意不做第二份
缓存**（`src/index.ts:50-53` 的注释说明了原因：Cordis 自己的 plugin/status 事件已经在
维护 `Entry.fiber` 和 `Fiber.state`，再存一份就是多一个要同步的生命周期事实）。
产物给设置面板用，回答「装了哪些插件、各自什么状态」。

### Go 里这个前提不成立

这个包的每一个字段都来自 cordis Loader：`entry.id`、`entry.options.name`、
`entry.options.group`、`entry.disabled`、`entry.fiber.state`。它自己不产生任何事实，
只做一次投影。所以问题不是「这个投影怎么用 Go 写」，而是**投影的输入在 Go 侧存不存在**。

不存在，两层原因：

1. **cordis 不移植**，这是已定的成例（裁决表里几十条 `Go 侧不移植 cordis 依赖注入框架`）。
   没有 Loader 就没有 entries。
2. 更根本的是，**Go 本身没有这个机制可借**。Loader 干的是运行期按模块说明符加载
   npm 模块、按需启停、并维护 Fiber 生命周期。Go 的 `plugin.Open` 不是它的对应物：
   只在 Linux/macOS 上可用、要求插件与主程序的工具链和编译参数完全一致、且**加载后
   无法卸载**——而 `enabled` 和 `unloading` 这两个字段的全部意义就是可以停。
   查证过：ds-harness-go 全仓库没有任何一处 `import "plugin"`。

ds-harness-go 的组件是编译期装进去的，由调用方一段同步代码装配（`New(...)` 的参数）。
少传一个编译就不过。这里没有「条目表」、没有 `enabled` 开关、也没有 Fiber 相位可读。
`list()` 的输入不存在。

### 查证过的两件事

**其一：裁掉它不是因为「Go 侧做不了 Remote 服务」。** 传输层是移植了的——
`typertprotocol` 里有 `RemoteRegistry`、`ClientRemote`、`Registries`。如果这个服务有
数据可报，它在 Go 侧是有地方安放的。它被裁掉的原因只有一个：这一个 Remote 没有数据源。
这个区分要写清楚，否则以后会被误读成「Remote 服务整类不移植」。

**其二：消费端只有一条链，且终点在浏览器里。** 全仓库搜 `pluginInventory` /
`PluginInventory`，除本包外的引用点是：`api/remotes` 转出 remote stub
（`src/client/index.ts:8,14,18,118`）→ `client/ui-settings-plugin-inventory` 的一个设置页
Tab（`PluginInventorySettingsTab.tsx`，带 `.module.css` 和 zh/en 词典）→
`bundle/web-app` 打进包里。没有第二个消费者，也没有任何服务端消费者。

`client/ui-settings-plugin-inventory` 目前 17 条仍是 PENDING（它在更后面的层）。
它整包在 `packages/client/` 下、产物是 React 组件，到那一层按浏览器渲染面的同一条线裁。
这里**不预先替它裁**，只记下这个预期，以免两处结论对不上时没人发现。

### 能力落在哪儿

**不落。** 这一条要说明白，因为「不落」在这份文件里是例外。

前两条裁决的能力都找到了落点（`typert/registry` 的 Go 版、`host/webserver` 的注入渲染）。
这一条没有，而且不是遗漏：**生产端和消费端两头都不在 Go 侧**——没有 Loader 就没有条目，
没有浏览器设置面板就没有读者。一个既没有输入也没有输出的投影，译出来是一个永远返回
空列表的方法。

一个容易误认的落点：`invariants.Registry` 也持有一张按包名的登记簿。**它不是这个东西**。
它记的是「哪个包注册了不变量检查器」，没有启停、没有生命周期相位、也不随模块加载变化。
把它当成 plugin inventory 的 Go 版，会让人以为 Go 侧有插件生命周期可查，而实际没有。

### 逐文件的裁法

| 文件 | 裁决 | 理由 |
|---|---|---|
| `src/types.ts`（4 个符号） | OUT_OF_SCOPE | 描述 cordis Loader 条目与 Fiber 相位的 wire 格式，Go 侧没有指称对象 |
| `src/index.ts`（3 个符号） | OUT_OF_SCOPE | Loader 条目的投影，输入不存在 |
| `src/invariant.ts` `name`/`inject` | GO_NATIVE | 按已定的 cordis 成例 |
| `src/invariant.ts` `apply` | SKIP | installer 是空的，按已定的 cordis 成例 |

### 想推翻这条的话

推翻它的证据应该长这样：**在 Go 侧找到（或确定要建）一张运行期可增删启停的组件表**
——不是编译期装配好的一组值，而是真的能在进程活着的时候加一个、停一个、并观察到它
处于加载中还是失败的登记簿。那时 `PluginInventorySnapshot` 这个形状就有了指称对象，
该在**拥有那张表的那个包**里给出对应的列举 API，而不是把这个包译过来。

反过来，如果 Go 侧永远是编译期装配，那这条不需要再复核。

---

## core/tools —— PTC（run_code）整块不移

**alpha.3 改了名字**：上一版这一块叫 Code Mode，文件是 `src/code-mode.ts`；
alpha.3 把文件改名成 `src/ptc.ts`，`Config.mode` 那一档从 `'code'` 改成 `'ptc'`，
`CodeDispatch*` 三个类型改成 `PtcDispatch*`，保留名的报错文本也跟着改了
（`index.ts:1046`、`1077`）。这一节整节按 alpha.3 重写过；裁决表里 `src/ptc.ts`
那五行、`src/index.ts:94` 与 `:350` 那三行也都是重新写的，**没有一行沿用旧文件那句**。
旧文件名下的六行留在表上，`kind` 带 `STALE:` 前缀，各自指向接替它的那一行。

**规模**：`ptc.ts` 678 行、`py-types.ts` 818 行、`ts-types.ts` 317 行、
`types.ts` 58 行，加上 `index.ts` 里挂在它上面的 `Config` / `ToolPresentationMode` /
`PtcDispatchLog` / `tools/ptc-dispatch-log` 瀑布和一串再导出。这是 `core/tools` 里**唯一**
一整块不移的东西，包的其余部分（注册表、可见性、四条瀑布、审批接缝、JSON Schema 子集、
呈现卡片）全都移了。

### 它在干什么

换一种把工具交给模型的方式。默认那种（`native`）是把每个可见工具的 schema 都发过去，
模型按名字一个个调。PTC 把它们**收拢成一个工具**：只发一个 `run_code`，外加一段
生成出来的 SDK 提示词——那段提示词是用模型看得懂的语言（TypeScript 或 Python）写的函数签名，
每个可见工具一条。模型于是不再一次调一个工具，而是**写一段程序**，程序里调那些函数，
调用经由桥接反过来走注册表的 `execute`。

`ts-types.ts` 和 `py-types.ts` 就是那两台 SDK 渲染机：把工具的 JSON Schema 译成
TypeScript 的 `interface` 或 Python 的 `TypedDict`。`Config.mode` 在 `native` / `ptc` /
`both` 之间选，`Config.maxParallelSubCalls` 管一段程序里并发子调用的上限（默认 10）。

### alpha.3 在它之上加了什么

三样，都是新的，都不能靠旧那句理由带过：

1. **嵌套派发的持久记账**。一段程序里的每一次子调用现在都在会话日志里留两条事件：
   `tool/code-dispatch-start`（`PtcDispatchStartEventData`）和 `tool/code-dispatch`
   （`PtcDispatchEventData`），按一个确定性的 `subCallId`（`<parent>:code:<n>`）配对。
   start 是在调度器**真正启动**这次调用时才追加的，不是提交时——所以一条 start 意味着
   工具流水线确实进过；在队列里被放弃的调用不留 start。只有最外层那份经过整理的结果进模型历史。
2. **子调用并发上限**。`maxParallelSubCalls` 经 `RunCodeBridgeOptions.maxParallel` 交给桥，
   按原生并发契约调度：只读调用可以并发到上限，会改东西的调用要等池子排干、独占地跑
   （`ptc.ts:351`、`:420`）。
3. **`mode: 'ptc'` 下直接调别的工具会失败**。`PTC_ONLY_INSTRUCTION`（`index.ts:51`）明说
   `run_code` 是唯一能直接调的工具，`index.ts:1316` 和 `:1432` 把不带 parent 的直接调用
   挡回去。上一版这一档只是「不发别的 schema」，alpha.3 变成了一道硬拒绝。

`RunCodeBridgeOptions`（`ptc.ts:266`）本身有一处手法值得单记：注册表把
`requireRuntime` / `peekRuntime` / `maxParallel` / `shapeDispatchLog` 四个闭包经构造函数
交给桥，而不是把这些操作放进公开服务 API——「只有属主才能铸出的操作以闭包形式流进来」。
这个手法在别处也用得上；它的三个消费点（代码运行时、子调用并发上限、派发日志瀑布流）
Go 侧一个都不存在。

### 它为什么存在

省 token，也省往返。二十个工具的完整 schema 是一大段提示词，而模型真正要用的往往是
其中三四个，还得一轮一轮地调。收拢成一段程序之后，提示词里只有一个 `run_code` 的 schema
加一份紧凑的 SDK 声明，而「读这个文件、按结果决定读哪个、再把两份拼起来」这种本来要三轮
的活，一段程序一轮就跑完了。

### Go 里这个前提不成立

程序得**有地方跑**。DSH 里跑它的是 `ctx.codeRuntime`——一个服务接缝，实现方是
`code-runtime/code-runtime-worker-thread`（Node 的 worker thread，剥掉类型后跑 TS）
和 `code-runtime/code-runtime-python`（CPython 子进程，fd 3 上的 JSON-lines 协议）。

这三个包**已经判过了**，理由记在 `docs/DESIGN.md`：执行那一整支九个纯接缝判不要，
因为「执行命令／代码／终端」的前置是**沙箱**，而服务端不提供沙箱，接缝一个实现方都挂不上
（`docs/DESIGN.md` 第 388-395 行）。裁决表里 `code-runtime/` 三个包共 78 个符号全部
OUT_OF_SCOPE，一条没漏。

所以这里不是一次新判断，是那次判断的**下游**：`run_code` 的执行体第一件事就是
`requireRuntime()`，拿不到就抛「配置错了」。把 PTC 移过来，得到的是一个
注册进去、发给模型、模型一调就报配置错误的工具。

同一条链上还有一个已经判过的邻居可以对照：`core/agent-tool-presentation` 判**不要**，
理由是「它在 `native` / `ptc` / `both` 之间选，而后两档要 `ctx.codeRuntime`，
只剩 `native` 一个可选值」。本条和它是同一个前提的两处结论，方向一致。

### 能力落在哪儿

**不落，而且这次连「以后可能落」都没有。**

省 token 那件事在 Go 侧仍然可做，但落点不在这里：可见工具集本来就按作用域过滤
（`tools.Restriction`、`Runtime.Schemas`），发给模型的清单发多大是**装配时决定**的，
不需要换一种呈现方式。省往返那件事的落点是循环那一层（一轮里发多个工具调用），
也不在这个包里。

### 不移带来的一处行为差异——记在这里

DSH 的 `Register` 和 `Restrict` 都**保留** `run_code` 这个名字：注册它会抛
「reserved for the PTC mode presentation transport」，`Restrict` 点它的名也会抛
（`index.ts:1045-1046`、`1076-1077`）。

Go 这边**没有这个保留**。`run_code` 在 `tools.Runtime` 眼里就是一个普通名字，谁都能注册。
这不是遗漏：保留一个名字的唯一理由是它已经被某个内建传输占着，而这里没有那个传输，
保留下来只会变成一条谁也解释不清的禁令。写在这里是因为它是本包和 DSH 之间**唯一**
一处可观测的行为差异。

### 逐文件的裁法

| 文件 | 符号数 | 裁决 | 逐行裁决落在哪 |
|---|---|---|---|
| `src/ptc.ts` | 5 | SKIP | 五行各自写过；`:293` `createRunCodeTool` 是这一批的总理由 |
| `src/ts-types.ts` | 3 | SKIP | 各写各的：`:13` 输入形状为什么多带 output、`:240` 类型投影、`:297` 提示词渲染机 |
| `src/py-types.ts` | 2 | SKIP | `:726` 为什么要有 Python 对偶、`:763` TypedDict 渲染与那条文档字符串位置的坑 |
| `src/types.ts` | 2 | SKIP | `PtcDispatch*` 两条事件载荷，Go 侧没有事件源 |
| `index.ts` `Config` / `ToolPresentationMode` | 2 | SKIP | `:644` `:647`，两项都只服务 PTC |
| `index.ts` `PtcDispatchLog` | 1 | SKIP | `:350`，子派发日志瀑布流看到的东西 |
| `index.ts` 对上述的再导出 | 8 | SKIP | 各自指向本体那一行 |
| `src/code-mode.ts` 等旧名 | 12 | SKIP（STALE） | 各自指向 alpha.3 里接替它的那一行 |

裁 SKIP 不裁 OUT_OF_SCOPE 是有意的：OUT_OF_SCOPE 留给产品外壳（Web UI、宿主进程、
打包、示例）。PTC 是**运行时能力**，只是前置条件在这个部署形态下不成立——
这个区别在以后复核时是有用的。

### 想推翻这条的话

推翻它的证据只有一样：**Go 侧出现了一个能跑不受信代码的沙箱**。那时该做的**不是**
把这个包译过来，而是先把 `code-runtime` 那个接缝和一个实现方立起来；这一块是它的
下游，等它有了再回头看。

在那之前，这条不需要再复核。

---

## test-support/loader-smoke —— 整包 OUT_OF_SCOPE

**规模**：342 行三个源文件，裁决表里 19 个符号。

这个包是**测试脚手架**，而同一层的另外三个（`llm-mock-server`、`llm-replay`、
`agent-loop-testkit`）都进来了，所以为什么只有它出去，得写清楚。

### 它在干什么

它是一台**子进程冒烟机**。`runLoaderSmoke` 造一个临时目录当 cwd，把一份
`cordis.yml` 放进去，用 execa 起一个新进程把这棵配置树真的装起来，喂空 stdin，
超时就 SIGKILL，最后核对退出码。配套的 `runFixtureTurn` 在那棵**已经装好的**
Loader 上下文里取 `ctx.get('agents')?.roots()`、要求恰好一个根 agent，然后
`agent.followup(message)` → `agent.whenIdle()` 驱一轮对话，顺带按 `turn/step`
累一份 `TokenUsage`。

`resolveExampleMode` / `resolveExampleLaunch` 决定用哪种方式起那个子进程：
`DSH_EXAMPLE_MODE=src` 就 tsx 直接跑 TypeScript（还要带上
`TSX_TSCONFIG_PATH`），`lib` 就用普通 Node 跑 `tsc` 编出来的产物。

### 为什么它和另外三个不一样

另外三个验的是**进程内的接缝**：`llm-mock-server` 演线路怎么坏，`llm-replay`
演模型说了什么，`agent-loop-testkit` 把一圈依赖挂到一个上下文上。这三件事在 Go
里都还是同样的事，只是换了个写法。

这一个验的是**装配本身**——「这份 YAML 描述的插件树，Loader 能不能把它装起来
并且不崩」。它的被测对象就是 Loader。而 Loader 不移植（裁决表里几十条
`Go 侧不移植 cordis 依赖注入框架`，以及 `host/plugin-inventory` 整包出去那一条
的同一个理由）。被测对象不在，测它的脚手架自然也不在。

`resolveExampleMode` / `resolveExampleLaunch` 那一支还多一层：**「跑源码还是跑
构建产物」这个二选一是 Node 工作区的产物**。TypeScript 要么现编要么预编，所以
才需要一个开关在两条启动路径之间挑。Go 编出来就是一个可执行文件，`go run` 和
`go build` 出来的东西行为一致，这个开关没有对应物。

`runFixtureTurn` 缺的**不是**驱一轮对话的能力：`Registry.Roots()`、
`Agent.Followup()`、`Agent.WhenIdle()` 在 `core/agent` 里都已经有了。它缺的是
那个**入参**——一份 Loader 装好、且里头恰好一个根 agent 的上下文。Go 的装配方
是写代码直接 `Create`，根 agent 是谁在调用点就已经写死了，不需要先装一棵树再
去里头找。

### 逐条

| 文件 | 符号数 | 裁决 | 理由 |
|---|---|---|---|
| `src/agent-turn.ts` | 3 | OUT_OF_SCOPE | 入参是一份 Loader 装好的上下文；驱对话那几道缝 Go 侧已经有了，缺的是那棵树 |
| `src/index.ts:19` 三条再导出 | 3 | OUT_OF_SCOPE | 随各自的本体 |
| `src/index.ts` 其余 | 10 | OUT_OF_SCOPE | 起子进程装 `cordis.yml`，加上 tsx/lib 的启动二选一 |
| `src/invariant.ts` `name` / `inject` | 2 | GO_NATIVE | cordis 插件身份与依赖声明，Go 靠显式入参 |
| `src/invariant.ts` `apply` | 1 | SKIP | 空装配器 |

裁 OUT_OF_SCOPE 不裁 SKIP，和 `host/plugin-inventory` 同理：这不是「运行时能力
的前置条件不成立」，而是**这一层根本不在移植范围里**。

### 想推翻这条的话

只有一样：**Go 侧长出了一个按配置文件装插件树的装配器**。目前没有这个计划——
Go 的装配方是写代码调函数，一棵树长什么样在编译期就定了。真到了那一天，该写的
也不是这个包的译本：一台 Go 的冒烟机会是 `os/exec` 起自己那个二进制，不需要
模式解算这一层。

---

## test-support/acp-snapshot —— 整包 OUT_OF_SCOPE

**规模**：3218 行六个源文件，裁决表里 83 个符号。这是 `test-support` 里最大的一个包。

### 它在干什么

一台**无 key 快照测试机**。给一份场景表和一个夹具目录，它铺出一整套 vitest 套件：
起一个真的 ACP agent 子进程、用 JSON-RPC 走 stdio 喂剧本、把 stdout 和落盘的
会话 JSONL 都捞回来、归一化掉所有易变值、再和committed 的黄金文件对。四层：

- `launcher.ts` —— 起子进程、接 SDK 客户端、收更新和 stderr、管关停。
- `harness.ts` —— 按 `input.json` 驱一个场景，收割父子两级 JSONL。
- `normalize.ts` —— 一堆纯函数，把捕获下来的面变成稳定文本。
- `suite.ts` —— `defineAcpSnapshotSuite`，在收集期把整棵 describe/it 树铺出来，
  外带 record / refresh 两种回写模式和一大堆夹具守卫。

### 三条各自独立的理由

**一、被测对象不在。** `launchAcpTestAgent` 起的是
`packages/examples/acp-demo/src/bin.ts`——那个包整包 SKIP（「示例应用，价值在它
记录的装配顺序，不在代码本身」）。没有那个二进制就没有子进程可起。注意
`acp/acp` 还在待裁、且排在第 9 块：那是**协议适配器**，不是一个能起起来的
agent 程序，补上它也不会补上这里缺的东西。

**二、起进程这一步的依赖已经出去了。** `launcher.ts:22` 直接 import
`@deepseek-ai/dsh-loader-smoke` 的 `resolveExampleLaunch`，而 `loader-smoke`
整包 OUT_OF_SCOPE（见上一节）。tsx 跑源码 / node 跑 `lib` 这个二选一是 Node
工作区的产物，Go 编出来就一个可执行文件。

**三、`suite.ts` 是 vitest 的形状，不是「快照」的形状。** 它在**模块级** import
vitest，靠收集期执行 `describe` / `it` 来铺树，还用 `it.concurrent`、
`expect(...).toMatchSnapshot`、以及 `DSH_SNAPSHOT=record|refresh` 切写回模式。
Go 的 `testing` 没有收集期——`TestXxx` 是编译期就定下来的函数，`t.Run` 在跑的
时候才展开。所以这不是「同一个东西换个写法」，而是两套不同的测试模型。

`normalize.ts` 那 12 个纯函数是唯一看着可移的一层，但它归一化的每一样东西都是
**这套 Node 夹具布局**的产物：Windows 的短/长路径别名、macOS 的 `/private`
前缀、`file:///` URI、tsx 的溢出目录名（`dsh-acp-snap-<hex>`）、以及
`stdout.expected.jsonl` / `session.<n>.jsonl` / `system-prompt.expected.md`
这套文件名约定。上面两层不在，这些代换就没有输入。

### 逐条

| 文件 | 符号数 | 裁决 | 理由 |
|---|---|---|---|
| `src/launcher.ts` | 4 | OUT_OF_SCOPE | 起的是一个 SKIP 掉的示例 bin，且走 loader-smoke 的启动解算 |
| `src/harness.ts` | 9 | OUT_OF_SCOPE | 同上，它是 launcher 的上层 |
| `src/normalize.ts` | 12 | OUT_OF_SCOPE | 归一化的输入全来自上面两层 |
| `src/suite.ts` | 26 | OUT_OF_SCOPE | 模块级 import vitest，收集期铺 describe/it 树 |
| `src/index.ts` 再导出 | 29 | OUT_OF_SCOPE | 随各自的本体 |
| `src/invariant.ts` `name` / `inject` | 2 | GO_NATIVE | cordis 插件身份与依赖声明，Go 靠显式入参 |
| `src/invariant.ts` `apply` | 1 | SKIP | 空装配器 |

### Go 侧对应的做法

真要给 Go 的 ACP 面加快照测试，写法是 `go test` 加一个 golden 文件助手
（`goldie` / `cupaloy` 这类现成包都带 `-update` 回写），被测对象直接在进程内
装配，不需要起子进程。这条正是「用 Go 现成的包，不要自己造轮子」——**这个包
本身就是一个轮子，而 Go 那边已经有了**。

### 想推翻这条的话

要 Go 侧先出现一个**可执行的 ACP agent 程序**（今天 `cmd/` 里只有
`llmmockserver`）。就算出现了，该写的也不是这个包的译本，而是上面那种
golden 文件套件；这一节到那时是用来说明「为什么不照抄」的，不是待办。

---

## acp/acp —— 两处逐项裁掉的东西（不是整包）

前面几节都是「整整一个包没有 Go 代码」。这一节不是：`acp/acp` 是**移过来的**，
两千行 Go 摆在那儿。记在这里的是这个包和上游之间**剩下的两处可观察差异**——
它们小到写不进那张表的一行理由里，又大到不写下来以后没人说得清是有意的还是漏的。

放在这份文件而不是那张裁决表里的理由：两条都不是「某个符号不移」，而是
「某个**行为**这一版不承诺」。

### 一、空会话不实体化：`ensureMaterialized` 没有对应实现

**上游那一行**：`packages/acp/acp/src/index.ts:228`，`session/new` 走完之后紧接着
`await persistence.ensureMaterialized(record.agent.session)`。

**它在干什么**：DSH 的持久化是**懒**的——`create(meta)` 一个字节都不写，第一次
`append` 才把头和第一批一起发布出去（`session-persistence-jsonl/README.md:71`）。
于是一条刚建出来、还没落过任何事件的会话在磁盘上不存在，也就不出现在
`list` 里。`ensureMaterialized` 是**唯一**一条补救路：它发布一个不带事件的头帧，
让一条空会话自己在耐久列举里露面，而不用虚构一个事件
（`session-persistence/src/coordinator.ts:653`）。

**Go 这边没有它**。`session/persistence.Backend` 上没有 `MaterializeHeader`,
`Coordinator` 上没有 `EnsureMaterialized`——`coordinator_chain.go:155` 那句
`materialized: false` 之后，把它翻成真的只有 `appendCore` 一条路。
`Coordinator.List` 直接转给 `backend.List`（`coordinator_prepare.go:144-146`），
而那份文档注释里已经把这个口径写明了：「一个建了但从没追加过的会话可能不在列举里」。

**因此的可观察差异**：通过 `session/new` 开出来的一条 ACP 会话，在它**第一条事件
落地之前**既列不出来也续不了跑——`session/list` 看不见它，`session/resume` 会
回一句「存档里没有」。上游那一行让它当场就能列、能续。

**这一版为什么不做**：补它要动的不是这个包。要加的是 `Backend` 接口上一个新方法
（每一个后端实现都得跟着长出来）、`Coordinator` 上一条新的串行化操作、以及
`materialized` 那个状态位的一条新的翻转路径。那是 `session/persistence` 的一次
接口扩张，不是 ACP 这一层能自己解决的事，塞进这条移植里只会让两个包同时半成品。

**实践上够不够用**：一条会话的第一条事件是 `session/prompt` 的第一步落下来的。
一个只是「开了一条会话就断线」的客户端确实会丢掉那条会话，但它本来也没有任何
内容可续。真正会被这一条咬到的是「开一条会话、马上换一个进程去列它」这种用法。

**想推翻这条的话**：`session/persistence` 上出现 `EnsureMaterialized` 就行。
那时 ACP 这边要改的只有一处：`newSession` 在 `adopt` 成功之后调它一次，
失败按 -32603 交回并回滚这次创建。

### 二、续跑时那份路由按回去得晚一步

**上游那一段**：`packages/acp/acp/src/session.ts:166-175`,`AcpSession.resume` 在
自己的 `selectionFor` 里就把日志里记着的那份路由读出来装上。

**Go 这边挪后了一步**，理由和做法都写在 `acp/acp/bridge.go` 的
`adoptLoggedSelection` 那个 `新增:` 块里，一句话是：[agent.Setup] 只收一个作用域，
拿不到那个正在被重建的会话（`core/agentloop/loop.go` 里 `setup(prepared.life,
prepared.agent.Scope())`），而那一刻会话还没公布、从存储里也取不到。所以
`ResumeSession` 先按**部署那份**路由把模型控制装上，等 `Resume` 交回句柄之后、
这条记录被记进桥的表**之前**，再从日志里那条请求头事件把真正记着的路由按回去
（`bridge.go:1192`）。

**因此的可观察差异**：那段空当里可能发生的唯一一件事，是这个 agent 自己接着跑一个
被打断的回合——那一步会走部署那份路由，而不是日志里那份。空当的长度是
「一次 `Resume` 返回」到「一次 map 赋值」之间，中间没有 I/O。

**这一版为什么不做**：抹掉这段空当要改的是 `agent.Setup` 的签名——让它收得到那个
正在重建的 agent。那个签名是 `core/agent` 和 `core/agentloop` 共有的约定，
为 ACP 这一处时序去改它，代价落在每一个 setup 实现上。

**已经守住的那一半**：日志里那份路由**会**被按回去，而且这条有测试
（`acp/acp/session_test.go` 里的 `TestResumeSessionAdoptsTheRouteRecordedInTheLog`）；
一个由适配器补出来的推理档位**不会**被当成一次显式选择留下
（`TestResumeSessionDropsAnAdapterSuppliedReasoningEffort`）。差异只在时序，不在结果。

**想推翻这条的话**：`agent.Setup` 长出「拿得到那个 agent」的能力就行。那时
`adoptLoggedSelection` 整个函数可以删掉，读日志那一步搬进 setup 里。

---

## session/session-persistence-jsonl —— 整包不落地

这个包曾经是**移过来的**：`session/persistence/jsonl` 十一个文件，明文那一档
从建库、追加、崩溃恢复到发布全都在，四个真的动盘上字节的恢复用例跑着。
后来整包删掉了，因此上游这个包的每一条符号在裁决表里都是 `SKIP`。

### 为什么删

两条，都写在 `docs/session-log-limit.md` 里：

1. **服务端不把 event 存在本机目录。** 一会话一份文件要求所有读写者看得见
   同一块盘，而本仓库的落点是一个多进程的服务端。
2. **日志要从最老的一头弹出 event（决定第 1、13 条）。** 顺序追加的文件上
   做不动这件事：弹掉头那一段就得重写整份存档，而那正好是这个后端它自己的崩溃
   安全性所依赖的那一条「已提交的那一段一个字节都不重写」。

现在本仓库自带的会话介质只有 `datastore/sessionstore` 一个：所有会话装在同一份介质里，
一个会话就是一条流，弹出是一句走主键的删除，一次写就是一个事务、因此压根不存在断尾。

### 那一档 zstd 物理编码一并没了

上一版这里记的是「裁掉的是一个物理编码档位，不是这个包」，并且为了把一个
已经装着 `.jsonl.zstd` 的根大声拒掉，特意留着 `Compression` 这个类型。
整包删掉之后这道拒绝也不存在了，但它防的那个失败模式也跟着没了：
本仓库再也不会去打开一个 DSH 写出来的本地根，所以也不会把它读成「一个会话都没有」。
**从 DSH 的本地根迁移历史这件事，本仓库不提供工具。**
