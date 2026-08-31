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

**规模**：401 + 1569 + 6870 = 8840 行，裁决表里 20 + 79 + 243 = 342 个符号。
这是第 1 层最后剩下的三个包，裁完第 1 层就没有 PENDING 了。

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
- **组出那张图的那一半还没做**——`client/modules` 的 node 半（第 2 层往后，49 条待裁）。
  它扫描 `dsh.client` 入口、组出 `WebBootGraph`、交给 webserver 去渲染。到那个包时
  按这个标准验收。

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
| boot manifest 组图（`WebBootGraph`） | `client/modules` node 半（第 2 层往后，49 条待裁） |
| 插件 bundle 的路由与增量扫描 | `client/modules` node 半 |
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

## core/tools —— Code Mode 整块不移

**规模**：`code-mode.ts` 681 行、`py-types.ts` 818 行、`ts-types.ts` 293 行、
`types.ts` 58 行，加上 `index.ts` 里挂在它上面的 `Config` / `ToolPresentationMode` /
`CodeDispatchLog` 和一串再导出。这是 `core/tools` 里**唯一**一整块不移的东西，
包的其余部分（注册表、可见性、四条瀑布、审批接缝、JSON Schema 子集、呈现卡片）全都移了。

### 它在干什么

换一种把工具交给模型的方式。默认那种（`native`）是把每个可见工具的 schema 都发过去，
模型按名字一个个调。Code Mode 把它们**收拢成一个工具**：只发一个 `run_code`，外加一段
生成出来的 SDK 提示词——那段提示词是用模型看得懂的语言（TypeScript 或 Python）写的函数签名，
每个可见工具一条。模型于是不再一次调一个工具，而是**写一段程序**，程序里调那些函数，
调用经由桥接反过来走注册表的 `execute`。

`ts-types.ts` 和 `py-types.ts` 就是那两台 SDK 渲染机：把工具的 JSON Schema 译成
TypeScript 的 `interface` 或 Python 的 `TypedDict`。`Config.mode` 在 `native` / `code` /
`both` 之间选，`Config.maxParallelSubCalls` 管一段程序里并发子调用的上限。

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
`requireRuntime()`，拿不到就抛「配置错了」。把 Code Mode 移过来，得到的是一个
注册进去、发给模型、模型一调就报配置错误的工具。

同一条链上还有一个已经判过的邻居可以对照：`core/agent-tool-presentation` 判**不要**，
理由是「它在 `native` / `code` / `both` 之间选，而 `code` 那两档要 `ctx.codeRuntime`，
只剩 `native` 一个可选值」。本条和它是同一个前提的两处结论，方向一致。

### 能力落在哪儿

**不落，而且这次连「以后可能落」都没有。**

省 token 那件事在 Go 侧仍然可做，但落点不在这里：可见工具集本来就按作用域过滤
（`tools.Restriction`、`Runtime.Schemas`），发给模型的清单发多大是**装配时决定**的，
不需要换一种呈现方式。省往返那件事的落点是循环那一层（一轮里发多个工具调用），
也不在这个包里。

### 不移带来的一处行为差异——记在这里

DSH 的 `Register` 和 `Restrict` 都**保留** `run_code` 这个名字：注册它会抛
「reserved for the Code Mode presentation transport」，`Restrict` 点它的名也会抛
（`index.ts:1054-1055`、`1085-1086`）。

Go 这边**没有这个保留**。`run_code` 在 `tools.Runtime` 眼里就是一个普通名字，谁都能注册。
这不是遗漏：保留一个名字的唯一理由是它已经被某个内建传输占着，而这里没有那个传输，
保留下来只会变成一条谁也解释不清的禁令。写在这里是因为它是本包和 DSH 之间**唯一**
一处可观测的行为差异。

### 逐文件的裁法

| 文件 | 符号数 | 裁决 | 理由 |
|---|---|---|---|
| `src/code-mode.ts` | 6 | SKIP | `run_code` 传输本身，执行体要 `ctx.codeRuntime` |
| `src/ts-types.ts` | 3 | SKIP | TypeScript SDK 渲染机，只有 Code Mode 读它 |
| `src/py-types.ts` | 2 | SKIP | Python SDK 渲染机，同上 |
| `src/types.ts` | 2 | SKIP | code 派发事件的 wire 形状，没有事件源 |
| `index.ts` `Config` / `ToolPresentationMode` | 2 | SKIP | 两个字段都只服务 Code Mode |
| `index.ts` `CodeDispatchLog` | 1 | SKIP | code 子派发的日志形状 |
| `index.ts` 对上述的再导出 | 8 | SKIP | 随各自的本体 |

裁 SKIP 不裁 OUT_OF_SCOPE 是有意的：OUT_OF_SCOPE 留给产品外壳（Web UI、宿主进程、
打包、示例）。Code Mode 是**运行时能力**，只是前置条件在这个部署形态下不成立——
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
