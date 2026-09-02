# 溯源重锚发现

由 `go run ./tools/portcheck -mode reanchor` 生成，**不要手工编辑**——它每次会被整份覆盖。

做法：给每条 `// 源:` 注释找一个锚点符号（注释自带的 `（名字）`、这条注释所文档化的 Go 声明经裁决表反查出的上游名、或退化的大小写不敏感匹配），去上游文件里按名字重新定位，算出真实跨度再和注释里写的比。跨度含紧邻上方的 JSDoc 块，终点靠括号配平。

溯源注释共 **4617** 条。

## 汇总

| 状态 | 条数 | 含义 |
|---|---:|---|
| `DRIFT` | 1120 | 既不相等也不包含，**真漂移** |
| `MOVED` | 3 | 锚点搬到了裁决表记的另一个文件里 |
| `NOT_FOUND` | 1765 | 锚点符号在该上游文件里找不到 |
| `AMBIGUOUS` | 151 | 同名声明在该文件里出现多次，定不了 |
| `NO_ANCHOR` | 577 | 判不出锚点（多为整段溯源、结构体字段上的注释） |
| `CONTAINS` | 82 | 引的范围完全包含算出来的（文件头部那种整体溯源，不算错） |
| `OK` | 919 | 引的范围和算出来的一致 |

### DRIFT 按 Go 顶层目录

| 顶层目录 | DRIFT |
|---|---:|
| core | 156 |
| session | 148 |
| subagent | 143 |
| llm | 128 |
| sessionquery | 80 |
| goal | 60 |
| compaction | 54 |
| skill | 44 |
| interaction | 34 |
| preset | 30 |
| jobs | 29 |
| context | 24 |
| mcp | 23 |
| schedule | 21 |
| acp | 18 |
| sdk | 18 |
| workspace | 17 |
| settings | 15 |
| workflow | 14 |
| spill | 12 |
| todo | 10 |
| guard | 9 |
| plan | 8 |
| attachment | 6 |
| storage | 6 |
| credentials | 5 |
| util | 5 |
| fs | 3 |

### DRIFT 按锚点可信度

**这张表比上面那张重要。** 一条漂移结论只和它的锚点一样可靠：锚点要是拿 Go 声明名硬凑的，「算出的范围」很可能算的是另一个符号，照着它改行号就是在编出处。`-fix` 只动「可自动改」那一档。

| 锚点来路 | DRIFT | 可自动改 |
|---|---:|---|
| 注释锚点 | 8 | 是 |
| 裁决表+路径一致 | 240 | 是 |
| 裁决表 | 81 | 否——只出报告，等人看 |
| Go 声明名 | 791 | 否——只出报告，等人看 |

DRIFT 里有 **413** 条的备注是「引的范围落在算出的范围之内」——那一类多半不是行号漂了，而是这条注释引的是某个大函数内部的一小段，而锚点只能从外层声明名反查出来，改成整个函数的跨度反而把它引丢了，所以 `-fix` 也不碰。

但其中 **53** 条是终点一模一样、起点只早了不超过 3 行——那是漏掉紧邻上方 JSDoc 抬头，不是引了内部片段，锚点够硬的话 `-fix` 会改。

把两道闸都过掉之后，`-fix` 实际会改 **0** 条。

## DRIFT（逐条）

引的范围既不等于也不包含算出来的范围。「可改」那一列为空的要人工逐条过。

共 1120 条。

| Go 位置 | 上游文件 | 引的范围 | 算出的范围 | 锚点符号 | 锚点来路 | 可改 | 备注 |
|---|---|---:|---:|---|---|:-:|---|
| `acp/acp/bridge.go:40` | packages/acp/acp/src/index.ts | 61-63 | 64-67 | `invalidParams` | Go 声明名 |  | - |
| `acp/acp/bridge.go:52` | packages/acp/acp/src/index.ts | 66-68 | 69-72 | `internalError` | Go 声明名 |  | - |
| `acp/acp/bridge.go:134` | packages/acp/acp/src/index.ts | 121-129 | 92-436 | `apply` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `acp/acp/bridge.go:175` | packages/acp/acp/src/index.ts | 222-285 | 92-436 | `apply` | 裁决表 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `acp/acp/bridge.go:280` | packages/acp/acp/src/index.ts | 148-156 | 124-133 | `notify` | Go 声明名 |  | - |
| `acp/acp/bridge.go:607` | packages/acp/acp/src/index.ts | 290-302 | 176-370 | `Initialize` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `acp/acp/bridge.go:628` | packages/acp/acp/src/index.ts | 304-306 | 192-370 | `Authenticate` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `acp/acp/bridge.go:637` | packages/acp/acp/src/index.ts | 308-333 | 196-370 | `NewSession` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `acp/acp/bridge.go:836` | packages/acp/acp/src/index.ts | 425-439 | 366-370 | `Cancel` | Go 声明名・放宽大小写 |  | - |
| `acp/acp/bridge.go:905` | packages/acp/acp/src/index.ts | 451-510 | 394-422 | `Quiesce` | Go 声明名・放宽大小写 |  | - |
| `acp/acp/codec.go:15` | packages/acp/acp/src/codec.ts | 14-34 | 9-34 | `turnEndToStopReason` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `acp/acp/config.go:25` | packages/acp/acp/src/invariant.ts | 10 | 12-13 | `name` | 裁决表 |  | - |
| `acp/acp/config.go:155` | packages/acp/acp/src/index.ts | 121-129 | 86-90 | `Config` | 裁决表 |  | - |
| `acp/acp/content.go:92` | packages/acp/acp/src/content.ts | 42-44 | 41-44 | `imageMediaType` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `acp/acp/content.go:103` | packages/acp/acp/src/content.ts | 47-60 | 46-60 | `decodeImage` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `acp/acp/content.go:159` | packages/acp/acp/src/content.ts | 63-80 | 62-79 | `assertImageRoute` | Go 声明名 |  | - |
| `acp/acp/content.go:228` | packages/acp/acp/src/content.ts | 108-110 | 106-109 | `resourceLinkText` | Go 声明名 |  | - |
| `acp/acp/invariant.go:21` | packages/acp/acp/src/invariant.ts | 28-29 | 23-29 | `apply` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `attachment/admission.go:15` | packages/attachment/attachment/src/admission.ts | 8-15 | 14-21 | `decodeBase64` | Go 声明名 |  | - |
| `attachment/attachment.go:123` | packages/attachment/attachment/src/index.ts | 53-75 | 56-78 | `ValidateImageBatch` | Go 声明名・放宽大小写 |  | - |
| `attachment/attachment.go:164` | packages/attachment/attachment/src/index.ts | 77-89 | 80-92 | `SaveImages` | Go 声明名・放宽大小写 |  | - |
| `attachment/attachment.go:194` | packages/attachment/attachment/src/index.ts | 110-129 | 124-143 | `ReadImageRequest` | Go 声明名・放宽大小写 |  | - |
| `attachment/types.go:171` | packages/attachment/attachment/src/types.ts | 90-91 | 110-111 | `Depth` | Go 声明名・放宽大小写 |  | - |
| `attachment/types.go:183` | packages/attachment/attachment/src/types.ts | 92-93 | 112-113 | `Space` | Go 声明名・放宽大小写 |  | - |
| `compaction/basic/config.go:38` | packages/compaction/compaction-basic/src/types.ts | 32-34 | 68 | `Target` | Go 声明名・放宽大小写 |  | - |
| `compaction/basic/config.go:53` | packages/compaction/compaction-basic/src/config.ts | 137 | 203 | `Key` | Go 声明名・放宽大小写 |  | - |
| `compaction/basic/config.go:208` | packages/compaction/compaction-basic/src/config.ts | 67-97 | 62-97 | `resolveConfig` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/basic/config.go:258` | packages/compaction/compaction-basic/src/config.ts | 105-125 | 99-125 | `resolveTargetPolicy` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/basic/config.go:287` | packages/compaction/compaction-basic/src/config.ts | 133-167 | 127-167 | `resolveCompactSpec` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/basic/config.go:321` | packages/compaction/compaction-basic/src/config.ts | 170-177 | 169-177 | `resolveRetention` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/basic/config.go:345` | packages/compaction/compaction-basic/src/config.ts | 180-191 | 179-191 | `validateRatioRetention` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/basic/config.go:359` | packages/compaction/compaction-basic/src/config.ts | 194-212 | 193-212 | `resolveModelPolicies` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/basic/config.go:388` | packages/compaction/compaction-basic/src/config.ts | 227-252 | 226-252 | `validatePolicy` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/basic/engine.go:112` | packages/compaction/compaction-basic/src/index.ts | 103-429 | 96-430 | `BasicCompactionEngine` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/basic/engine.go:151` | packages/compaction/compaction-basic/src/index.ts | 119-120 | 107-118 | `Config` | 裁决表 |  | - |
| `compaction/basic/engine.go:156` | packages/compaction/compaction-basic/src/index.ts | 258-332 | 249-333 | `CompactIfNeeded` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/basic/engine.go:330` | packages/compaction/compaction-basic/src/index.ts | 343-358 | 335-359 | `CompactRegion` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/basic/engine.go:346` | packages/compaction/compaction-basic/src/index.ts | 368-420 | 361-421 | `CompactNow` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/basic/engine.go:441` | packages/compaction/compaction-basic/src/index.ts | 349-357 | 335-359 | `compactRegion` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/basic/engine.go:512` | packages/compaction/compaction-basic/src/index.ts | 52-60 | 52-61 | `routedTarget` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/basic/engine.go:530` | packages/compaction/compaction-basic/src/index.ts | 62-71 | 63-72 | `conversationTarget` | Go 声明名 |  | - |
| `compaction/basic/install.go:325` | packages/compaction/compaction-basic/src/index.ts | 139-145 | 140-146 | `logResult` | Go 声明名 |  | - |
| `compaction/basic/region.go:32` | packages/compaction/compaction-basic/src/region.ts | 98-134 | 92-136 | `selectCompactableRange` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/basic/summarize.go:197` | packages/compaction/compaction-basic/src/summarizer.ts | 198-214 | 197-214 | `finishError` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/basic/summarize.go:217` | packages/compaction/compaction-basic/src/summarizer.ts | 217-224 | 216-224 | `summaryText` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/basic/summarizer.go:31` | packages/compaction/compaction-basic/src/summarizer.ts | 78-85 | 72-85 | `SummarizationInput` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/basic/summarizer.go:80` | packages/compaction/compaction-basic/src/summarizer.ts | 189-195 | 184-195 | `frameSummary` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/basic/transaction.go:72` | packages/compaction/compaction-basic/src/region.ts | 78-82 | 58-59 | `Stability` | Go 声明名・放宽大小写 |  | - |
| `compaction/basic/transaction.go:134` | packages/compaction/compaction-basic/src/region.ts | 33-39 | 32-39 | `surfaceSelection` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/basic/transaction.go:147` | packages/compaction/compaction-basic/src/region.ts | 42-47 | 41-49 | `preparedCompaction` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/basic/transaction.go:162` | packages/compaction/compaction-basic/src/region.ts | 49-51 | 51-53 | `summarizedCompaction` | Go 声明名・放宽大小写 |  | - |
| `compaction/basic/transaction.go:172` | packages/compaction/compaction-basic/src/region.ts | 78-82 | 79-84 | `stabilityCheck` | Go 声明名・放宽大小写 |  | - |
| `compaction/basic/transaction.go:182` | packages/compaction/compaction-basic/src/region.ts | 152-254 | 138-256 | `compactSurfaceRegion` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/basic/transaction.go:390` | packages/compaction/compaction-basic/src/region.ts | 315-336 | 316-338 | `validateSurfaceRegion` | Go 声明名 |  | - |
| `compaction/basic/transaction.go:445` | packages/compaction/compaction-basic/src/region.ts | 339-357 | 340-364 | `prepareCompaction` | Go 声明名 |  | - |
| `compaction/basic/transaction.go:498` | packages/compaction/compaction-basic/src/region.ts | 360-384 | 366-394 | `summarizeCompaction` | Go 声明名 |  | - |
| `compaction/basic/transaction.go:539` | packages/compaction/compaction-basic/src/region.ts | 387-396 | 396-406 | `assertWholeSurfaceUnchanged` | Go 声明名 |  | - |
| `compaction/basic/transaction.go:558` | packages/compaction/compaction-basic/src/region.ts | 398-424 | 408-434 | `assertSelectedSpanStable` | Go 声明名 |  | - |
| `compaction/basic/transaction.go:591` | packages/compaction/compaction-basic/src/region.ts | 427-478 | 436-488 | `commitCompactionBody` | Go 声明名 |  | - |
| `compaction/basic/transaction.go:656` | packages/compaction/compaction-basic/src/region.ts | 498-514 | 498-524 | `buildSummarizationInput` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/checkpoint.go:53` | packages/compaction/compaction/src/checkpoint.ts | 33-42 | 27-42 | `compactCheckpointSource` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/engine.go:51` | packages/compaction/compaction/src/index.ts | 41-57 | 36-57 | `ManualCompactionError` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/invariant.go:16` | packages/compaction/compaction/src/invariant.ts | 12 | 14-15 | `name` | 裁决表 |  | - |
| `compaction/invariant.go:39` | packages/compaction/compaction/src/invariant.ts | 27-30 | 300-306 | `apply` | 裁决表 |  | - |
| `compaction/invariant.go:341` | packages/compaction/compaction/src/invariant.ts | 214-238 | 300-306 | `Apply` | Go 声明名・放宽大小写 |  | - |
| `compaction/toolpairing.go:79` | packages/compaction/compaction/src/tool-pairing.ts | 117-119 | 109-119 | `toolPairingBalancedBefore` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/toolpairing.go:95` | packages/compaction/compaction/src/tool-pairing.ts | 129-131 | 121-131 | `toolPairingBalancedAfter` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/toolpairing.go:172` | packages/compaction/compaction/src/tool-pairing.ts | 100-107 | 99-107 | `cutBalance` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/toolpairing.go:183` | packages/compaction/compaction/src/tool-pairing.ts | 30-39 | 28-38 | `eventDelta` | Go 声明名 |  | - |
| `compaction/toolresultpruner/config.go:16` | packages/compaction/compaction-tool-result-pruner/src/config.ts | 6 | 6-7 | `PRUNE_MARKER` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/toolresultpruner/config.go:74` | packages/compaction/compaction-tool-result-pruner/src/config.ts | 10-13 | 9-14 | `DEFAULTS` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/toolresultpruner/config.go:88` | packages/compaction/compaction-tool-result-pruner/src/config.ts | 36-64 | 31-65 | `resolveConfig` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/toolresultpruner/pruner.go:50` | packages/compaction/compaction-tool-result-pruner/src/index.ts | 44 | 43-185 | `ToolResultPruner` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/toolresultpruner/pruner.go:65` | packages/compaction/compaction-tool-result-pruner/src/index.ts | 58-61 | 49-53 | `Config` | 裁决表 |  | - |
| `compaction/toolresultpruner/pruner.go:76` | packages/compaction/compaction-tool-result-pruner/src/index.ts | 56 | 49-53 | `Config` | 裁决表 |  | - |
| `compaction/toolresultpruner/pruner.go:84` | packages/compaction/compaction-tool-result-pruner/src/index.ts | 68-74 | 63-74 | `MeasureContent` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/toolresultpruner/pruner.go:100` | packages/compaction/compaction-tool-result-pruner/src/index.ts | 83-122 | 76-122 | `PruneContent` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `compaction/toolresultpruner/session.go:39` | packages/compaction/compaction-tool-result-pruner/src/index.ts | 136-183 | 124-184 | `PruneSession` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `context/instructions/compose.go:164` | packages/context/agent-instructions/src/index.ts | 43-58 | 46-62 | `visibleBaselineSource` | Go 声明名 |  | - |
| `context/instructions/compose.go:185` | packages/context/agent-instructions/src/index.ts | 105-222 | 107-224 | `compose` | Go 声明名 |  | - |
| `context/instructions/compose.go:350` | packages/context/agent-instructions/src/index.ts | 224-248 | 226-250 | `syncInbox` | Go 声明名 |  | - |
| `context/instructions/compose.go:411` | packages/context/agent-instructions/src/index.ts | 250-260 | 252-262 | `composeAndSync` | Go 声明名 |  | - |
| `context/instructions/projection.go:252` | packages/context/agent-instructions/src/index.ts | 71-78 | 75-81 | `filePathFromExecution` | Go 声明名 |  | - |
| `context/instructions/state.go:121` | packages/context/agent-instructions/src/state.ts | 81-86 | 49-51 | `MessageSourceMap` | 裁决表 |  | - |
| `context/sessionref/install.go:60` | packages/context/session-reference/src/index.ts | 106-113 | 79-329 | `SessionReferenceResolver` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `context/sessionref/resolver.go:179` | packages/context/session-reference/src/index.ts | 230-286 | 257-313 | `Prepare` | Go 声明名・放宽大小写 |  | - |
| `context/sessionref/resolver.go:316` | packages/context/session-reference/src/index.ts | 304-333 | 331-360 | `normalizeReferences` | Go 声明名 |  | - |
| `context/sessionref/resolver.go:349` | packages/context/session-reference/src/index.ts | 335-337 | 362-364 | `renderPrompt` | Go 声明名 |  | - |
| `context/sessionref/resolver.go:360` | packages/context/session-reference/src/index.ts | 339-343 | 372-376 | `candidateRank` | Go 声明名 |  | - |
| `context/sessionref/serialization.go:17` | packages/context/session-reference/src/serialization.ts | 8-12 | 3-12 | `stringifyTagSafeJson` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `context/sessionref/types.go:233` | packages/context/session-reference/src/types.ts | 47-57 | 46-62 | `SessionReferenceCandidate` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `context/sessionref/uri.go:185` | packages/context/session-reference/src/uri.ts | 90-92 | 89-90 | `escapeLabel` | Go 声明名 |  | - |
| `context/sessionref/uri.go:190` | packages/context/session-reference/src/uri.ts | 94-96 | 93-95 | `unescapeLabel` | Go 声明名 |  | - |
| `context/sessionref/uri.go:213` | packages/context/session-reference/src/uri.ts | 98-102 | 97-103 | `invalidURI` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `context/timecontext/config.go:15` | packages/context/time-context/src/index.ts | 21 | 22-23 | `name` | 裁决表 |  | - |
| `context/timecontext/install.go:57` | packages/context/time-context/src/index.ts | 145-209 | 119-219 | `apply` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `context/timecontext/invariant.go:20` | packages/context/time-context/src/invariant.ts | 12 | 22-23 | `name` | 裁决表 |  | - |
| `context/timecontext/invariant.go:45` | packages/context/time-context/src/invariant.ts | 28-68 | 27-68 | `PreparationPosition` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `context/timecontext/invariant.go:98` | packages/context/time-context/src/invariant.ts | 78-159 | 77-139 | `ValidateReading` | Go 声明名・放宽大小写 |  | - |
| `context/timecontext/render.go:49` | packages/context/time-context/src/index.ts | 110-125 | 90-105 | `RenderText` | Go 声明名・放宽大小写 |  | - |
| `context/timecontext/render.go:101` | packages/context/time-context/src/index.ts | 41-55 | 61-76 | `formatDuration` | Go 声明名 |  | - |
| `context/timecontext/timestamp.go:25` | packages/context/time-context/src/timestamp.ts | 31-37 | 24-37 | `formatTimestamp` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/agent/inbox.go:37` | packages/core/agent/src/inbox.ts | 24-25 | 24-220 | `Inbox` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/agent/inbox.go:203` | packages/core/agent/src/inbox.ts | 128-146 | 178-184 | `Splice` | Go 声明名・放宽大小写 |  | - |
| `core/agent/initiator.go:34` | packages/core/agent/src/index.ts | 329-343 | 320-335 | `WithInitiator` | Go 声明名・放宽大小写 |  | - |
| `core/agent/initiator.go:55` | packages/core/agent/src/index.ts | 346-358 | 337-350 | `WithoutInitiator` | Go 声明名・放宽大小写 |  | - |
| `core/agent/initiator.go:66` | packages/core/agent/src/index.ts | 300-312 | 292-304 | `CurrentInitiator` | Go 声明名・放宽大小写 |  | - |
| `core/agent/initiator.go:87` | packages/core/agent/src/index.ts | 314-326 | 306-318 | `RequireInitiator` | Go 声明名・放宽大小写 |  | - |
| `core/agent/registry.go:208` | packages/core/agent/src/index.ts | 244-298 | 235-696 | `AgentRegistry` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/agent/registry.go:418` | packages/core/agent/src/index.ts | 360-388 | 352-380 | `SetFactory` | Go 声明名・放宽大小写 |  | - |
| `core/agent/registry.go:454` | packages/core/agent/src/index.ts | 390-394 | 382-386 | `requireFactory` | Go 声明名 |  | - |
| `core/agent/registry.go:466` | packages/core/agent/src/index.ts | 396-415 | 388-407 | `Create` | Go 声明名・放宽大小写 |  | - |
| `core/agent/registry.go:496` | packages/core/agent/src/index.ts | 432-457 | 424-449 | `Register` | Go 声明名・放宽大小写 |  | - |
| `core/agent/registry.go:521` | packages/core/agent/src/index.ts | 459-509 | 451-501 | `Enter` | Go 声明名・放宽大小写 |  | - |
| `core/agent/registry.go:579` | packages/core/agent/src/index.ts | 490-508 | 486-499 | `detach` | Go 声明名 |  | - |
| `core/agent/registry.go:680` | packages/core/agent/src/index.ts | 527-540 | 519-532 | `emitDisposed` | Go 声明名 |  | - |
| `core/agent/registry.go:694` | packages/core/agent/src/index.ts | 578-585 | 570-577 | `Get` | Go 声明名・放宽大小写 |  | - |
| `core/agent/registry.go:708` | packages/core/agent/src/index.ts | 587-597 | 579-589 | `IsOwnedBy` | Go 声明名・放宽大小写 |  | - |
| `core/agent/registry.go:722` | packages/core/agent/src/index.ts | 599-605 | 591-597 | `List` | Go 声明名・放宽大小写 |  | - |
| `core/agent/registry.go:738` | packages/core/agent/src/index.ts | 607-617 | 599-609 | `Roots` | Go 声明名・放宽大小写 |  | - |
| `core/agent/runtime.go:19` | packages/core/agent/src/runtime-types.ts | 24-31 | 24-34 | `AgentOptions` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/agentdefaultmodel/config.go:94` | packages/core/agent-default-model/src/index.ts | 59-105 | 59-107 | `AgentDefaultModelConfig` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/agentdefaultmodel/config.go:116` | packages/core/agent-default-model/src/index.ts | 72-82 | 40-46 | `Config` | 裁决表 |  | - |
| `core/agentdefaultmodel/config.go:171` | packages/core/agent-default-model/src/index.ts | 84-90 | 86-92 | `CurrentSelection` | Go 声明名・放宽大小写 |  | - |
| `core/agentdefaultmodel/config.go:189` | packages/core/agent-default-model/src/index.ts | 92-104 | 94-106 | `SaveSelection` | Go 声明名・放宽大小写 |  | - |
| `core/agentdefaultmodel/invariant.go:9` | packages/core/agent-default-model/src/invariant.ts | 14 | 16-17 | `name` | 裁决表 |  | - |
| `core/agentloop/agent.go:67` | packages/core/agent-loop/src/agent.ts | 38-46 | 259 | `phase` | Go 声明名 |  | - |
| `core/agentloop/agent.go:350` | packages/core/agent-loop/src/agent.ts | 99-101 | 116 | `Status` | Go 声明名・放宽大小写 |  | - |
| `core/agentloop/agent.go:374` | packages/core/agent-loop/src/agent.ts | 113-120 | 122-129 | `Send` | Go 声明名・放宽大小写 |  | - |
| `core/agentloop/agent.go:403` | packages/core/agent-loop/src/agent.ts | 122-124 | 131-133 | `Followup` | Go 声明名・放宽大小写 |  | - |
| `core/agentloop/agent.go:408` | packages/core/agent-loop/src/agent.ts | 126-128 | 135-137 | `Steer` | Go 声明名・放宽大小写 |  | - |
| `core/agentloop/agent.go:413` | packages/core/agent-loop/src/agent.ts | 130-132 | 139-141 | `Inject` | Go 声明名・放宽大小写 |  | - |
| `core/agentloop/agent.go:466` | packages/core/agent-loop/src/agent.ts | 134-140 | 143-149 | `Cancel` | Go 声明名・放宽大小写 |  | - |
| `core/agentloop/agent.go:499` | packages/core/agent-loop/src/agent.ts | 142-162 | 151-171 | `RunMaintenance` | Go 声明名・放宽大小写 |  | - |
| `core/agentloop/agent.go:561` | packages/core/agent-loop/src/agent.ts | 164-193 | 173-202 | `wakeDriver` | Go 声明名 |  | - |
| `core/agentloop/agent.go:604` | packages/core/agent-loop/src/agent.ts | 195-200 | 204-209 | `WhenIdle` | Go 声明名・放宽大小写 |  | - |
| `core/agentloop/agent.go:648` | packages/core/agent-loop/src/agent.ts | 210-223 | 219-232 | `kick` | Go 声明名 |  | - |
| `core/agentloop/agent.go:857` | packages/core/agent-loop/src/agent.ts | 225-243 | 234-252 | `preStep` | Go 声明名 |  | - |
| `core/agentloop/agent.go:1189` | packages/core/agent-loop/src/agent.ts | 54-61 | 60-67 | `requestProposal` | Go 声明名 |  | - |
| `core/agentloop/agent.go:1210` | packages/core/agent-loop/src/agent.ts | 422-514 | 440-544 | `buildRequest` | Go 声明名 |  | - |
| `core/agentloop/invariant.go:191` | packages/core/agent-loop/src/invariant.ts | 45-52 | 44-49 | `headerMatches` | Go 声明名 |  | - |
| `core/agentloop/loop.go:55` | packages/core/agent-loop/src/index.ts | 213-233 | 269-290 | `applyLauncherIdentities` | Go 声明名 |  | - |
| `core/agentloop/loop.go:196` | packages/core/agent-loop/src/index.ts | 132-139 | 188-195 | `resolveMaxParallelToolCalls` | Go 声明名 |  | - |
| `core/agentloop/loop.go:209` | packages/core/agent-loop/src/index.ts | 141-147 | 197-203 | `assertAgentOptions` | Go 声明名 |  | - |
| `core/agentloop/loop.go:224` | packages/core/agent-loop/src/index.ts | 277-293 | 333-349 | `validateConfiguredAgents` | Go 声明名 |  | - |
| `core/agentloop/loop.go:314` | packages/core/agent-loop/src/index.ts | 55-57 | 110-112 | `isActive` | Go 声明名 |  | - |
| `core/agentloop/loop.go:323` | packages/core/agent-loop/src/index.ts | 59-63 | 114-118 | `track` | Go 声明名 |  | - |
| `core/agentloop/loop.go:343` | packages/core/agent-loop/src/index.ts | 65-70 | 120-125 | `trackStartup` | Go 声明名 |  | - |
| `core/agentloop/loop.go:357` | packages/core/agent-loop/src/index.ts | 77-79 | 132-135 | `waitWhileActive` | Go 声明名 |  | - |
| `core/agentloop/loop.go:368` | packages/core/agent-loop/src/index.ts | 81-89 | 560-583 | `dispose` | Go 声明名 |  | - |
| `core/agentloop/loop.go:396` | packages/core/agent-loop/src/index.ts | 92-130 | 148-162 | `raceAbort` | Go 声明名 |  | - |
| `core/agentloop/loop.go:504` | packages/core/agent-loop/src/index.ts | 318-382 | 310-328 | `Config` | 裁决表 |  | - |
| `core/agentloop/loop.go:640` | packages/core/agent-loop/src/index.ts | 330-334 | 190 | `maxParallelToolCalls` | Go 声明名 |  | - |
| `core/agentloop/loop.go:735` | packages/core/agent-loop/src/index.ts | 384-404 | 447-467 | `reportConfiguredStartupFailure` | Go 声明名 |  | - |
| `core/agentloop/loop.go:784` | packages/core/agent-loop/src/index.ts | 406-428 | 469-491 | `restoreOrCreateConfigured` | Go 声明名 |  | - |
| `core/agentloop/loop.go:827` | packages/core/agent-loop/src/index.ts | 430-451 | 493-514 | `waitForDrainingConfiguredIdentity` | Go 声明名 |  | - |
| `core/agentloop/loop.go:895` | packages/core/agent-loop/src/index.ts | 453-575 | 516-641 | `prepare` | Go 声明名 |  | - |
| `core/agentloop/loop.go:903` | packages/core/agent-loop/src/index.ts | 583 | 516-641 | `prepare` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/agentloop/loop.go:1118` | packages/core/agent-loop/src/index.ts | 580-587 | 643-661 | `Create` | Go 声明名・放宽大小写 |  | - |
| `core/agentloop/loop.go:1144` | packages/core/agent-loop/src/index.ts | 589-604 | 663-685 | `CreateAgent` | Go 声明名・放宽大小写 |  | - |
| `core/agentloop/loop.go:1171` | packages/core/agent-loop/src/index.ts | 606-622 | 687-708 | `setupAndPublish` | Go 声明名 |  | - |
| `core/agentloop/loop.go:1224` | packages/core/agent-loop/src/index.ts | 637-710 | 724-773 | `resumeWith` | Go 声明名 |  | - |
| `core/agentloop/runtimecontext.go:166` | packages/core/agent-loop/src/runtime-context.ts | 59-75 | 58-75 | `Project` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/agentloop/toolcalls.go:26` | packages/core/agent-loop/src/tool-calls.ts | 19-23 | 20-24 | `plannedCall` | Go 声明名・放宽大小写 |  | - |
| `core/agentloop/toolcalls.go:48` | packages/core/agent-loop/src/tool-calls.ts | 32-38 | 33-39 | `groupOutcome` | Go 声明名・放宽大小写 |  | - |
| `core/agentloop/toolcalls.go:78` | packages/core/agent-loop/src/tool-calls.ts | 59-101 | 41-102 | `executeToolCalls` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/agentloop/toolcalls.go:185` | packages/core/agent-loop/src/tool-calls.ts | 121-246 | 113-247 | `runGroup` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/agentloop/toolcalls.go:344` | packages/core/agent-loop/src/tool-calls.ts | 248-259 | 249-260 | `appendSkippedToolCall` | Go 声明名 |  | - |
| `core/agentloop/toolcalls.go:358` | packages/core/agent-loop/src/tool-calls.ts | 261-265 | 262-266 | `appendToolCall` | Go 声明名 |  | - |
| `core/agentloop/toolcalls.go:380` | packages/core/agent-loop/src/tool-calls.ts | 267-289 | 268-290 | `appendToolResult` | Go 声明名 |  | - |
| `core/session/fork.go:62` | packages/core/session/src/index.ts | 1072-1108 | 1065-1093 | `Fork` | Go 声明名・放宽大小写 |  | - |
| `core/session/preparation.go:23` | packages/core/session/src/preparation.ts | 14-48 | 14-49 | `SessionPreparation` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/session/preparation.go:57` | packages/core/session/src/preparation.ts | 41-47 | 10-11 | `Release` | Go 声明名・放宽大小写 |  | - |
| `core/session/session.go:21` | packages/core/session/src/index.ts | 471-479 | 261 | `Config` | 裁决表・放宽大小写 |  | - |
| `core/session/session.go:44` | packages/core/session/src/index.ts | 416-425 | 415-756 | `Session` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/session/session.go:211` | packages/core/session/src/index.ts | 444-446 | 912 | `ID` | Go 声明名・放宽大小写 |  | - |
| `core/session/session.go:228` | packages/core/session/src/index.ts | 452-470 | 448-470 | `FirstLiveSeq` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/session/session.go:247` | packages/core/session/src/index.ts | 564-566 | 231 | `Seq` | Go 声明名・放宽大小写 |  | - |
| `core/session/session.go:256` | packages/core/session/src/index.ts | 553-562 | 40-84 | `Events` | Go 声明名 |  | - |
| `core/session/session.go:305` | packages/core/session/src/index.ts | 568-657 | 567-653 | `Append` | Go 声明名・放宽大小写 |  | - |
| `core/session/session.go:464` | packages/core/session/src/index.ts | 664-687 | 660-678 | `RequestHeader` | Go 声明名・放宽大小写 |  | - |
| `core/session/session.go:486` | packages/core/session/src/index.ts | 689-706 | 684-697 | `RequestContext` | 裁决表・放宽大小写 |  | - |
| `core/session/session.go:513` | packages/core/session/src/index.ts | 708-748 | 706-745 | `DeriveMessages` | Go 声明名・放宽大小写 |  | - |
| `core/session/store.go:129` | packages/core/session/src/index.ts | 786-800 | 784-1153 | `SessionStore` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/session/store.go:366` | packages/core/session/src/index.ts | 847-902 | 841-887 | `Prepare` | Go 声明名・放宽大小写 |  | - |
| `core/session/store.go:437` | packages/core/session/src/index.ts | 904-947 | 889-945 | `Enter` | Go 声明名・放宽大小写 |  | - |
| `core/session/store.go:488` | packages/core/session/src/index.ts | 936-946 | 932-943 | `detach` | Go 声明名 |  | - |
| `core/session/store.go:541` | packages/core/session/src/index.ts | 960-1000 | 959-994 | `Announce` | Go 声明名・放宽大小写 |  | - |
| `core/session/store.go:591` | packages/core/session/src/index.ts | 1002-1011 | 996-1005 | `emitDisposed` | Go 声明名 |  | - |
| `core/session/store.go:621` | packages/core/session/src/index.ts | 1013-1050 | 1007-1037 | `Flush` | Go 声明名・放宽大小写 |  | - |
| `core/session/store.go:665` | packages/core/session/src/index.ts | 1060-1062 | 1048-1055 | `Get` | Go 声明名・放宽大小写 |  | - |
| `core/session/store.go:679` | packages/core/session/src/index.ts | 1064-1070 | 1057-1063 | `List` | Go 声明名・放宽大小写 |  | - |
| `core/session/store.go:696` | packages/core/session/src/index.ts | 1052-1058 | 1039-1046 | `liveEntryFor` | Go 声明名 |  | - |
| `core/session/validate.go:67` | packages/core/session/src/index.ts | 95-134 | 93-134 | `validateSessionHeader` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/session/validate.go:118` | packages/core/session/src/index.ts | 148-155 | 147-155 | `snapshotSessionHeader` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/systemprompt/prompt.go:91` | packages/core/system-prompt/src/index.ts | 53-73 | 52-74 | `PromptSection` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/systemprompt/prompt.go:159` | packages/core/system-prompt/src/index.ts | 429 | 348-349 | `ToolProvider` | Go 声明名 |  | - |
| `core/systemprompt/prompt.go:164` | packages/core/system-prompt/src/index.ts | 446 | 351-352 | `VariableProvider` | Go 声明名 |  | - |
| `core/systemprompt/prompt.go:265` | packages/core/system-prompt/src/index.ts | 258-290 | 308-346 | `interpolate` | Go 声明名 |  | - |
| `core/systemprompt/prompt.go:355` | packages/core/system-prompt/src/index.ts | 146-157 | 183-198 | `validateToolOrder` | Go 声明名 |  | - |
| `core/systemprompt/prompt.go:380` | packages/core/system-prompt/src/index.ts | 164-179 | 200-219 | `orderTools` | Go 声明名 |  | - |
| `core/systemprompt/registry.go:55` | packages/core/system-prompt/src/index.ts | 302-336 | 354-386 | `promptLayer` | Go 声明名・放宽大小写 |  | - |
| `core/systemprompt/registry.go:98` | packages/core/system-prompt/src/index.ts | 328-335 | 378-385 | `IsEmpty` | Go 声明名・放宽大小写 |  | - |
| `core/systemprompt/registry.go:226` | packages/core/system-prompt/src/index.ts | 381-390 | 424-441 | `Section` | Go 声明名・放宽大小写 |  | - |
| `core/systemprompt/registry.go:241` | packages/core/system-prompt/src/index.ts | 398-407 | 14-16 | `Context` | Go 声明名 |  | - |
| `core/systemprompt/registry.go:255` | packages/core/system-prompt/src/index.ts | 415-421 | 478-490 | `SuppressRuntimeContext` | Go 声明名・放宽大小写 |  | - |
| `core/systemprompt/registry.go:281` | packages/core/system-prompt/src/index.ts | 446-455 | 507-524 | `Variable` | Go 声明名・放宽大小写 |  | - |
| `core/systemprompt/registry.go:316` | packages/core/system-prompt/src/index.ts | 465-544 | 536-611 | `Assemble` | Go 声明名・放宽大小写 |  | - |
| `core/tools/jsonschema.go:436` | packages/core/tools/src/json-schema.ts | 65-73 | 61-74 | `JsonSchemaError` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/tools/jsonschema.go:549` | packages/core/tools/src/json-schema.ts | 227-370 | 226-376 | `checkSchemaNode` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/tools/jsonvalue.go:21` | packages/core/tools/src/json-schema.ts | 654-656 | 646-656 | `validateJsonSchemaValue` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/tools/jsonvalue.go:40` | packages/core/tools/src/json-schema.ts | 417-419 | 416-419 | `diagnosticPath` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/tools/jsonvalue.go:50` | packages/core/tools/src/json-schema.ts | 422-424 | 421-424 | `propertyPath` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/tools/jsonvalue.go:60` | packages/core/tools/src/json-schema.ts | 487-645 | 486-644 | `checkValue` | Go 声明名 |  | - |
| `core/tools/jsonvalue.go:187` | packages/core/tools/src/json-schema.ts | 479-489 | 474-484 | `checkScalarValue` | Go 声明名 |  | - |
| `core/tools/jsonvalue.go:203` | packages/core/tools/src/json-schema.ts | 175-177 | 174-177 | `isJSONNumber` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/tools/pipeline.go:184` | packages/core/tools/src/index.ts | 175 | 1722-1772 | `PostExecute` | Go 声明名・放宽大小写 |  | - |
| `core/tools/pipeline.go:238` | packages/core/tools/src/index.ts | 1691-1728 | 1684 | `Approval` | Go 声明名・放宽大小写 |  | - |
| `core/tools/pipeline.go:347` | packages/core/tools/src/index.ts | 1471-1481 | 1466-1469 | `gate` | Go 声明名 |  | - |
| `core/tools/pipeline.go:369` | packages/core/tools/src/index.ts | 1691-1728 | 1669-1720 | `serviceAsk` | Go 声明名 |  | - |
| `core/tools/pipeline.go:449` | packages/core/tools/src/index.ts | 1524-1560 | 1518-1551 | `dispatchToolBody` | Go 声明名 |  | - |
| `core/tools/pipeline.go:511` | packages/core/tools/src/index.ts | 1740-1781 | 1722-1772 | `postExecute` | Go 声明名 |  | - |
| `core/tools/pipeline.go:574` | packages/core/tools/src/index.ts | 1647-1653 | 1639-1645 | `applyFinalContent` | Go 声明名 |  | - |
| `core/tools/pipeline.go:604` | packages/core/tools/src/index.ts | 1655-1675 | 1647-1667 | `notifyResult` | Go 声明名 |  | - |
| `core/tools/pipeline.go:627` | packages/core/tools/src/index.ts | 1364-1451 | 1355-1442 | `createExecution` | Go 声明名 |  | - |
| `core/tools/pipeline.go:650` | packages/core/tools/src/index.ts | 1793-1822 | 1783-1814 | `createSuccessResult` | Go 声明名 |  | - |
| `core/tools/pipeline.go:722` | packages/core/tools/src/index.ts | 525-527 | 517-520 | `projectionError` | Go 声明名 |  | - |
| `core/tools/pipeline.go:732` | packages/core/tools/src/index.ts | 1826-1843 | 1816-1835 | `normalizeDispatchResult` | Go 声明名 |  | - |
| `core/tools/pipeline.go:785` | packages/core/tools/src/index.ts | 1275-1285 | 1260-1276 | `ExecutionMode` | Go 声明名・放宽大小写 |  | - |
| `core/tools/pipeline.go:813` | packages/core/tools/src/index.ts | 1515-1522 | 1508-1516 | `cancellationResult` | Go 声明名 |  | - |
| `core/tools/pipeline.go:888` | packages/core/tools/src/index.ts | 625-631 | 617-623 | `failureMessageFromContent` | Go 声明名 |  | - |
| `core/tools/presentation.go:24` | packages/core/tools/src/presentation.ts | 15 | 10-15 | `ToolCallKind` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/tools/presentation.go:40` | packages/core/tools/src/presentation.ts | 23-26 | 17-26 | `FileLocation` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/tools/presentation.go:52` | packages/core/tools/src/presentation.ts | 34-40 | 28-40 | `FileDiff` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/tools/presentation.go:65` | packages/core/tools/src/presentation.ts | 46 | 42-46 | `ToolCallView` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/tools/presentation.go:76` | packages/core/tools/src/presentation.ts | 53-75 | 48-75 | `GenericCallView` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/tools/presentation.go:101` | packages/core/tools/src/presentation.ts | 84-100 | 77-100 | `TerminalCallView` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/tools/presentation.go:122` | packages/core/tools/src/presentation.ts | 110-118 | 102-118 | `DiffCallView` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/tools/presentation.go:141` | packages/core/tools/src/presentation.ts | 127-130 | 120-130 | `ReadFileLine` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/tools/presentation.go:173` | packages/core/tools/src/presentation.ts | 146-155 | 142-155 | `GenericResultView` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/tools/presentation.go:191` | packages/core/tools/src/presentation.ts | 163-176 | 157-176 | `TerminalResultView` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/tools/presentation.go:213` | packages/core/tools/src/presentation.ts | 184-190 | 178-190 | `DiffResultView` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/tools/presentation.go:247` | packages/core/tools/src/presentation.ts | 216-231 | 208-231 | `SearchMatchesResultView` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/tools/presentation.go:269` | packages/core/tools/src/presentation.ts | 238-253 | 233-253 | `SearchPathsResultView` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/tools/presentation.go:289` | packages/core/tools/src/presentation.ts | 281-308 | 269-308 | `ReadResultView` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/tools/presentation.go:315` | packages/core/tools/src/presentation.ts | 319-328 | 310-328 | `WebSource` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/tools/presentation.go:326` | packages/core/tools/src/presentation.ts | 355-366 | 349-366 | `WebSearchResultView` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/tools/presentation.go:346` | packages/core/tools/src/presentation.ts | 374-389 | 368-389 | `WebFetchResultView` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/tools/runtime.go:88` | packages/core/tools/src/index.ts | 713-757 | 706-747 | `toolLayer` | Go 声明名・放宽大小写 |  | - |
| `core/tools/runtime.go:134` | packages/core/tools/src/index.ts | 733-736 | 724-728 | `IsEmpty` | Go 声明名・放宽大小写 |  | - |
| `core/tools/runtime.go:143` | packages/core/tools/src/index.ts | 739-745 | 730-737 | `admits` | Go 声明名 |  | - |
| `core/tools/runtime.go:167` | packages/core/tools/src/index.ts | 651-670 | 646-667 | `Config` | 裁决表 |  | - |
| `core/tools/runtime.go:194` | packages/core/tools/src/index.ts | 783-789 | 776-1854 | `ToolRuntime` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `core/tools/runtime.go:290` | packages/core/tools/src/index.ts | 1069-1097 | 1055-1089 | `Restrict` | Go 声明名・放宽大小写 |  | - |
| `core/tools/runtime.go:363` | packages/core/tools/src/index.ts | 690-699 | 973 | `view` | Go 声明名 |  | - |
| `core/tools/runtime.go:454` | packages/core/tools/src/index.ts | 1205-1207 | 1186-1197 | `Get` | Go 声明名・放宽大小写 |  | - |
| `core/tools/runtime.go:477` | packages/core/tools/src/index.ts | 1012-1014 | 1158 | `KnownNames` | Go 声明名・放宽大小写 |  | - |
| `core/tools/runtime.go:487` | packages/core/tools/src/index.ts | 1210-1224 | 1246-1258 | `schemaOf` | Go 声明名 |  | - |
| `credentials/invariant.go:16` | packages/credentials/credentials/src/invariant.ts | 9 | 11-12 | `name` | 裁决表 |  | - |
| `credentials/memory_provider_test.go:30` | packages/credentials/credentials/tests/memory.ts | 13-17 | 13-92 | `memoryCredentials` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `credentials/notifier.go:148` | packages/credentials/credentials/src/index.ts | 280-287 | 273-280 | `NotifyRecordUpdated` | Go 声明名・放宽大小写 |  | - |
| `credentials/notifier.go:166` | packages/credentials/credentials/src/index.ts | 289-313 | 285-306 | `fanOut` | Go 声明名 |  | - |
| `credentials/provider.go:114` | packages/credentials/credentials/src/index.ts | 177-263 | 154-314 | `CredentialProvider` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `fs/fs.go:68` | packages/fs/fs/src/index.ts | 80-250 | 80-263 | `FileSystem` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `fs/invariant.go:16` | packages/fs/fs/src/invariant.ts | 7 | 9-10 | `name` | 裁决表 |  | - |
| `fs/types.go:10` | packages/fs/fs/src/types.ts | 11-16 | 61-62 | `TargetKey` | Go 声明名・放宽大小写 |  | - |
| `goal/goal/fold.go:79` | packages/goal/goal/src/fold.ts | 40-49 | 36-49 | `emptyGoalFoldState` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `goal/goal/fold.go:227` | packages/goal/goal/src/fold.ts | 73-85 | 72-85 | `decodeBlockReason` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `goal/goal/fold.go:246` | packages/goal/goal/src/fold.ts | 88-115 | 87-115 | `decodeSnapshot` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `goal/goal/fold.go:297` | packages/goal/goal/src/fold.ts | 118-126 | 117-126 | `decodeRef` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `goal/goal/fold.go:316` | packages/goal/goal/src/fold.ts | 134-172 | 128-172 | `decodeGoalChange` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `goal/goal/fold.go:420` | packages/goal/goal/src/fold.ts | 186-190 | 185-190 | `requireSameDefinition` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `goal/goal/fold.go:430` | packages/goal/goal/src/fold.ts | 193-197 | 192-197 | `requireNextRevision` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `goal/goal/fold.go:448` | packages/goal/goal/src/fold.ts | 200-253 | 199-253 | `validateSnapshotTransition` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `goal/goal/fold.go:502` | packages/goal/goal/src/fold.ts | 260-264 | 255-264 | `goalChangeRef` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `goal/goal/fold.go:512` | packages/goal/goal/src/fold.ts | 271-306 | 266-306 | `applyGoalChange` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `goal/goal/fold.go:564` | packages/goal/goal/src/fold.ts | 313-332 | 308-332 | `applyGoalEvent` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `goal/goal/fold.go:604` | packages/goal/goal/src/fold.ts | 339-349 | 334-349 | `foldGoal` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `goal/goal/service.go:140` | packages/goal/goal/src/index.ts | 193-214 | 170-174 | `Config` | 裁决表 |  | - |
| `goal/goal/service.go:180` | packages/goal/goal/src/index.ts | 198-200 | 165-168 | `apply` | 裁决表 |  | - |
| `goal/goal/service.go:224` | packages/goal/goal/src/index.ts | 222-227 | 264-273 | `Get` | Go 声明名・放宽大小写 |  | - |
| `goal/goal/service.go:240` | packages/goal/goal/src/index.ts | 236-242 | 275-287 | `Disarm` | Go 声明名・放宽大小写 |  | - |
| `goal/goal/service.go:259` | packages/goal/goal/src/index.ts | 251-267 | 289-312 | `Create` | Go 声明名・放宽大小写 |  | - |
| `goal/goal/service.go:300` | packages/goal/goal/src/index.ts | 276-290 | 322-336 | `Edit` | Go 声明名・放宽大小写 |  | - |
| `goal/goal/service.go:341` | packages/goal/goal/src/index.ts | 299-301 | 345-347 | `Pause` | Go 声明名・放宽大小写 |  | - |
| `goal/goal/service.go:349` | packages/goal/goal/src/index.ts | 311-328 | 357-375 | `Resume` | Go 声明名・放宽大小写 |  | - |
| `goal/goal/service.go:385` | packages/goal/goal/src/index.ts | 337-346 | 384-393 | `Complete` | Go 声明名・放宽大小写 |  | - |
| `goal/goal/service.go:395` | packages/goal/goal/src/index.ts | 355-368 | 395-417 | `Block` | Go 声明名・放宽大小写 |  | - |
| `goal/goal/service.go:423` | packages/goal/goal/src/index.ts | 377-390 | 426-440 | `Clear` | Go 声明名・放宽大小写 |  | - |
| `goal/goal/service.go:485` | packages/goal/goal/src/index.ts | 461-473 | 499-513 | `transition` | Go 声明名 |  | - |
| `goal/goal/service.go:529` | packages/goal/goal/src/index.ts | 414-418 | 461-466 | `assertLive` | Go 声明名 |  | - |
| `goal/goal/service.go:600` | packages/goal/goal/src/index.ts | 542-558 | 579-597 | `commit` | Go 声明名 |  | - |
| `goal/goal/service.go:633` | packages/goal/goal/src/index.ts | 561-577 | 619 | `view` | Go 声明名 |  | - |
| `goal/goal/service.go:663` | packages/goal/goal/src/index.ts | 507-512 | 544-547 | `nextMutationTime` | Go 声明名 |  | - |
| `goal/goal/service.go:712` | packages/goal/goal/src/index.ts | 401-411 | 448-459 | `expectCurrent` | Go 声明名 |  | - |
| `goal/goal/service.go:730` | packages/goal/goal/src/index.ts | 450-458 | 488-497 | `withPhase` | Go 声明名 |  | - |
| `goal/goal/service.go:755` | packages/goal/goal/src/index.ts | 476-481 | 515-521 | `transitionError` | Go 声明名 |  | - |
| `goal/goal/service.go:778` | packages/goal/goal/src/index.ts | 158-163 | 210-216 | `resolveCreateGoal` | Go 声明名 |  | - |
| `goal/goal/service.go:797` | packages/goal/goal/src/index.ts | 142-147 | 194-200 | `resolveMaxGoalRounds` | Go 声明名 |  | - |
| `goal/goal/service.go:807` | packages/goal/goal/src/index.ts | 150-155 | 202-208 | `resolveObjective` | Go 声明名 |  | - |
| `goal/goal/service.go:818` | packages/goal/goal/src/index.ts | 166-180 | 218-233 | `resolveBlockReason` | Go 声明名 |  | - |
| `goal/goal/types.go:40` | packages/goal/goal/src/invariant.ts | 9 | 11-12 | `name` | 裁决表 |  | - |
| `goal/goal/types.go:74` | packages/goal/goal/src/types.ts | 16 | 20-21 | `ID` | Go 声明名・放宽大小写 |  | - |
| `goal/goal/types.go:286` | packages/goal/goal/src/types.ts | 91-100 | 85-100 | `GoalProjection` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `goal/goalcommand/config.go:20` | packages/goal/command-goal/src/invariant.ts | 10 | 12-13 | `name` | 裁决表 |  | - |
| `goal/goalcommand/config.go:96` | packages/goal/command-goal/src/index.ts | 189 | 188-196 | `apply` | 裁决表 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `goal/goalcommand/render.go:51` | packages/goal/command-goal/src/index.ts | 59-73 | 58-74 | `commandHint` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `goal/goalcommand/render.go:79` | packages/goal/command-goal/src/index.ts | 103-106 | 102-108 | `missingGoal` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `goal/goalrounddriver/driver.go:40` | packages/goal/goal-round-driver/src/index.ts | 22-26 | 21-26 | `roundIdentity` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `goal/goalrounddriver/driver.go:49` | packages/goal/goal-round-driver/src/index.ts | 29-35 | 28-35 | `roundAttempt` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `goal/goalrounddriver/driver.go:136` | packages/goal/goal-round-driver/src/index.ts | 208-241 | 207-241 | `requestDrive` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `goal/goalrounddriver/install.go:34` | packages/goal/goal-round-driver/src/invariant.ts | 10 | 12-13 | `name` | 裁决表 |  | - |
| `goal/goalrounddriver/install.go:117` | packages/goal/goal-round-driver/src/index.ts | 76-95 | 75-445 | `apply` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `goal/goalrounddriver/invariant.go:54` | packages/goal/goal-round-driver/src/invariant.ts | 47-61 | 45-58 | `validateEvent` | Go 声明名 |  | - |
| `goal/goalrounddriver/prompt.go:61` | packages/goal/goal-round-driver/src/prompt.ts | 12-26 | 6-26 | `renderGoalRoundPrompt` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `goal/goaltool/authority.go:148` | packages/goal/tool-goal/src/authority.ts | 70-74 | 74-83 | `hasDirectHumanInput` | Go 声明名 |  | - |
| `goal/goaltool/authority.go:167` | packages/goal/tool-goal/src/authority.ts | 77-83 | 85-92 | `isMatchingGoalRound` | Go 声明名 |  | - |
| `goal/goaltool/config.go:21` | packages/goal/tool-goal/src/invariant.ts | 10 | 12-13 | `name` | 裁决表 |  | - |
| `goal/goaltool/config.go:26` | packages/goal/tool-goal/src/index.ts | 22 | 21 | `name` | 裁决表 |  | - |
| `goal/goaltool/config.go:133` | packages/goal/tool-goal/src/index.ts | 126-132 | 185-337 | `apply` | 裁决表 |  | - |
| `goal/goaltool/tool.go:144` | packages/goal/tool-goal/src/index.ts | 113-123 | 111-122 | `guidance` | Go 声明名 |  | - |
| `goal/goaltool/tool.go:243` | packages/goal/tool-goal/src/index.ts | 157-173 | 155-172 | `goalValue` | Go 声明名 |  | - |
| `goal/goaltool/tool.go:364` | packages/goal/tool-goal/src/index.ts | 182-184 | 180-183 | `present` | Go 声明名 |  | - |
| `goal/goaltool/tool.go:381` | packages/goal/tool-goal/src/index.ts | 145-154 | 143-153 | `goalRef` | Go 声明名 |  | - |
| `goal/goaltool/tool.go:611` | packages/goal/tool-goal/src/index.ts | 260-263 | 259-262 | `replacements` | Go 声明名 |  | - |
| `goal/goaltool/wrapup.go:58` | packages/goal/tool-goal/src/wrapup.ts | 17-41 | 9-41 | `renderWrapupContext` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `guard/repeattoolreminder/reminder.go:82` | packages/guard/repeat-tool-reminder/src/index.ts | 152-155 | 196 | `chain` | Go 声明名 |  | - |
| `guard/repeattoolreminder/reminder.go:90` | packages/guard/repeat-tool-reminder/src/index.ts | 162-174 | 214 | `Reminder` | Go 声明名・放宽大小写 |  | - |
| `guard/repeattoolreminder/reminder.go:109` | packages/guard/repeat-tool-reminder/src/index.ts | 162-174 | 157-233 | `apply` | 裁决表 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `guard/repeattoolreminder/reminder.go:167` | packages/guard/repeat-tool-reminder/src/index.ts | 213-227 | 157-233 | `apply` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `guard/repeattoolreminder/reminder.go:196` | packages/guard/repeat-tool-reminder/src/index.ts | 189-211 | 181-207 | `Observe` | Go 声明名・放宽大小写 |  | - |
| `guard/repeattoolreminder/reminder.go:246` | packages/guard/repeat-tool-reminder/src/index.ts | 176-179 | 175-179 | `tracked` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `guard/repeattoolreminder/text.go:30` | packages/guard/repeat-tool-reminder/src/index.ts | 70-79 | 69-79 | `detailedReminder` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `guard/repeattoolreminder/text.go:79` | packages/guard/repeat-tool-reminder/src/index.ts | 118-121 | 113-121 | `previewArguments` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `guard/timeoutpolicy/policy.go:41` | packages/guard/timeout-policy/src/index.ts | 57-81 | 50-81 | `apply` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `interaction/askuser/invariant.go:9` | packages/interaction/tool-ask-user/src/invariant.ts | 10 | 12-13 | `name` | 裁决表 |  | - |
| `interaction/askuser/tool.go:242` | packages/interaction/tool-ask-user/src/index.ts | 78 | 78-100 | `render` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `interaction/askuser/tool.go:322` | packages/interaction/tool-ask-user/src/index.ts | 80-99 | 80-100 | `execute` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `interaction/commands/invariant.go:21` | packages/interaction/commands/src/invariant.ts | 11 | 13-14 | `name` | 裁决表 |  | - |
| `interaction/commands/invariant.go:35` | packages/interaction/commands/src/invariant.ts | 22 | 59-65 | `apply` | 裁决表 |  | - |
| `interaction/commands/invariant.go:117` | packages/interaction/commands/src/invariant.ts | 29-31 | 59-65 | `Apply` | Go 声明名・放宽大小写 |  | - |
| `interaction/commands/registry.go:52` | packages/interaction/commands/src/index.ts | 116-123 | 111-124 | `parseCommand` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `interaction/commands/registry.go:75` | packages/interaction/commands/src/index.ts | 34-51 | 34-52 | `CommandInvocation` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `interaction/commands/registry.go:104` | packages/interaction/commands/src/index.ts | 54-69 | 54-70 | `CommandDefinition` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `interaction/commands/registry.go:133` | packages/interaction/commands/src/index.ts | 85-102 | 85-103 | `commandLayer` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `interaction/commands/registry.go:202` | packages/interaction/commands/src/index.ts | 250-455 | 246-456 | `CommandRuntime` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `interaction/commands/registry.go:260` | packages/interaction/commands/src/index.ts | 270-277 | 266-278 | `Register` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `interaction/commands/registry.go:280` | packages/interaction/commands/src/index.ts | 284-290 | 286-291 | `List` | Go 声明名・放宽大小写 |  | - |
| `interaction/commands/registry.go:297` | packages/interaction/commands/src/index.ts | 298-300 | 293-301 | `Find` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `interaction/commands/registry.go:309` | packages/interaction/commands/src/index.ts | 328-396 | 330-397 | `Execute` | Go 声明名・放宽大小写 |  | - |
| `interaction/commands/registry.go:439` | packages/interaction/commands/src/index.ts | 146-167 | 146-168 | `withAbort` | 注释锚点 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `interaction/commands/registry.go:491` | packages/interaction/commands/src/index.ts | 347-356 | 348-357 | `settle` | Go 声明名 |  | - |
| `interaction/commands/registry.go:508` | packages/interaction/commands/src/index.ts | 399-408 | 399-409 | `settleThrown` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `interaction/commands/registry.go:545` | packages/interaction/commands/src/index.ts | 435-437 | 435-438 | `view` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `interaction/commands/registry.go:554` | packages/interaction/commands/src/index.ts | 440-454 | 440-455 | `notifyChange` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `interaction/commands/registry.go:583` | packages/interaction/commands/src/index.ts | 170-214 | 170-215 | `normalizeDefinition` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `interaction/commands/registry.go:619` | packages/interaction/commands/src/index.ts | 217-243 | 217-244 | `normalizeResult` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `interaction/commands/types.go:85` | packages/interaction/commands/src/types.ts | 42-47 | 36-47 | `CommandExecution` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `interaction/commands/types.go:190` | packages/interaction/commands/src/index.ts | 72-77 | 72-78 | `ParsedCommand` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `interaction/userapproval/invariant.go:21` | packages/interaction/user-approval/src/invariant.ts | 9 | 12-13 | `name` | 裁决表 |  | - |
| `interaction/userapproval/invariant.go:39` | packages/interaction/user-approval/src/invariant.ts | 21-24 | 105-111 | `apply` | 裁决表 |  | - |
| `interaction/userapproval/invariant.go:119` | packages/interaction/user-approval/src/invariant.ts | 52-56 | 105-111 | `Apply` | Go 声明名・放宽大小写 |  | - |
| `interaction/userapproval/invariant.go:141` | packages/interaction/user-approval/src/invariant.ts | 64-74 | 105-111 | `apply` | 裁决表 |  | - |
| `interaction/userapproval/service.go:26` | packages/interaction/user-approval/src/index.ts | 235 | 171-180 | `name` | 裁决表 |  | - |
| `interaction/userapproval/service.go:200` | packages/interaction/user-approval/src/index.ts | 288-296 | 254-261 | `OverrideOf` | Go 声明名・放宽大小写 |  | - |
| `interaction/userapproval/service.go:245` | packages/interaction/user-approval/src/index.ts | 239-276 | 204-241 | `Request` | Go 声明名・放宽大小写 |  | - |
| `interaction/userapproval/types.go:67` | packages/interaction/user-approval/src/index.ts | 81-82 | 46-47 | `Outcomes` | Go 声明名・放宽大小写 |  | - |
| `interaction/userapproval/types.go:231` | packages/interaction/user-approval/src/index.ts | 120-134 | 85-99 | `hasOpenTurn` | Go 声明名 |  | - |
| `interaction/userquestions/service.go:174` | packages/interaction/user-questions/src/index.ts | 77-140 | 70-151 | `Ask` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `jobs/jobs/invariant.go:21` | packages/jobs/jobs/src/invariant.ts | 8 | 11-12 | `name` | 裁决表 |  | - |
| `jobs/jobs/invariant.go:27` | packages/jobs/jobs/src/invariant.ts | 17-43 | 16-43 | `ValidateSnapshot` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `jobs/jobs/registry.go:30` | packages/jobs/jobs/src/index.ts | 62-177 | 35-177 | `JobRegistry` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `jobs/jobs/types.go:28` | packages/jobs/jobs/src/types.ts | 17 | 13-17 | `JobStatus` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `jobs/jobs/types.go:91` | packages/jobs/jobs/src/types.ts | 46-69 | 41-69 | `JobStart` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `jobs/jobs/types.go:139` | packages/jobs/jobs/src/types.ts | 97-128 | 93-128 | `JobSnapshot` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `jobs/jobs/types.go:185` | packages/jobs/jobs/src/types.ts | 146-149 | 142-149 | `JobDoneListener` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `jobs/jobs/types.go:193` | packages/jobs/jobs/src/types.ts | 160 | 151-160 | `JobsChangedListener` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `jobs/jobstool/config.go:22` | packages/jobs/tool-jobs/src/invariant.ts | 10 | 12-13 | `name` | 裁决表 |  | - |
| `jobs/jobstool/config.go:28` | packages/jobs/tool-jobs/src/index.ts | 21 | 20 | `name` | 裁决表 |  | - |
| `jobs/jobstool/config.go:161` | packages/jobs/tool-jobs/src/index.ts | 205-222 | 204-401 | `apply` | 裁决表 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `jobs/jobstool/notice.go:44` | packages/jobs/tool-jobs/src/index.ts | 117-121 | 116-120 | `retainHead` | Go 声明名 |  | - |
| `jobs/jobstool/notice.go:56` | packages/jobs/tool-jobs/src/index.ts | 111-115 | 110-114 | `retainTail` | Go 声明名 |  | - |
| `jobs/jobstool/notice.go:65` | packages/jobs/tool-jobs/src/index.ts | 123-135 | 122-134 | `fitWithSuffix` | Go 声明名 |  | - |
| `jobs/jobstool/notice.go:90` | packages/jobs/tool-jobs/src/index.ts | 137-144 | 136-143 | `completionSummary` | Go 声明名 |  | - |
| `jobs/jobstool/notice.go:98` | packages/jobs/tool-jobs/src/index.ts | 146-167 | 145-166 | `fitCompletionNotice` | Go 声明名 |  | - |
| `jobs/jobstool/notice.go:132` | packages/jobs/tool-jobs/src/index.ts | 169-174 | 168-173 | `rawSingleText` | Go 声明名 |  | - |
| `jobs/jobstool/notice.go:146` | packages/jobs/tool-jobs/src/index.ts | 176-183 | 175-182 | `boundSingleText` | Go 声明名 |  | - |
| `jobs/jobstool/snapshot.go:41` | packages/jobs/tool-jobs/src/index.ts | 85-96 | 84-95 | `publicJob` | Go 声明名 |  | - |
| `jobs/localjobs/invariant.go:16` | packages/jobs/jobs-local/src/invariant.ts | 10 | 12-13 | `name` | 裁决表 |  | - |
| `jobs/localjobs/registry.go:70` | packages/jobs/jobs-local/src/index.ts | 76-84 | 70-84 | `jobLayer` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `jobs/localjobs/registry.go:133` | packages/jobs/jobs-local/src/index.ts | 91 | 86-532 | `LocalJobRegistry` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `jobs/localjobs/registry.go:175` | packages/jobs/jobs-local/src/index.ts | 123-129 | 30-37 | `Config` | 裁决表 |  | - |
| `jobs/localjobs/registry.go:558` | packages/jobs/jobs-local/src/index.ts | 315-319 | 307-319 | `servesOwner` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `jobs/localjobs/registry.go:670` | packages/jobs/jobs-local/src/index.ts | 398-406 | 394-406 | `notifyChanged` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `jobs/localjobs/registry.go:703` | packages/jobs/jobs-local/src/index.ts | 416-440 | 408-440 | `settle` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `jobs/localjobs/registry.go:741` | packages/jobs/jobs-local/src/index.ts | 448-464 | 442-464 | `ensureOwnerCleanup` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `jobs/localjobs/registry.go:783` | packages/jobs/jobs-local/src/index.ts | 467-475 | 466-475 | `disposeOwned` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `jobs/localjobs/registry.go:881` | packages/jobs/jobs-local/src/index.ts | 507-531 | 502-531 | `cancelForTeardown` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/apikey.go:66` | packages/llm/llm/src/api-key.ts | 36-41 | 25-41 | `normalizeApiKey` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/assembler.go:17` | packages/llm/llm/src/assembler.ts | 15-23 | 16-24 | `partialBlock` | Go 声明名・放宽大小写 |  | - |
| `llm/assembler.go:80` | packages/llm/llm/src/assembler.ts | 44-95 | 45-96 | `Push` | Go 声明名・放宽大小写 |  | - |
| `llm/assembler.go:144` | packages/llm/llm/src/assembler.ts | 97-105 | 98-106 | `ensure` | Go 声明名 |  | - |
| `llm/assembler.go:189` | packages/llm/llm/src/assembler.ts | 129-149 | 130-150 | `assembled` | Go 声明名 |  | - |
| `llm/assembler.go:247` | packages/llm/llm/src/assembler.ts | 151-159 | 140 | `Blocks` | Go 声明名・放宽大小写 |  | - |
| `llm/assembler.go:260` | packages/llm/llm/src/assembler.ts | 161-178 | 162-179 | `InterruptedBlocks` | Go 声明名・放宽大小写 |  | - |
| `llm/assembler.go:329` | packages/llm/llm/src/assembler.ts | 199-206 | 200-207 | `Message` | 裁决表・放宽大小写 |  | - |
| `llm/attribution.go:19` | packages/llm/llm/src/attribution.ts | 25-32 | 18-32 | `AppIdentity` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/attribution.go:40` | packages/llm/llm/src/attribution.ts | 40-44 | 34-44 | `APP_IDENTITY` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/attribution.go:59` | packages/llm/llm/src/attribution.ts | 53-55 | 46-55 | `userAgent` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/attribution.go:72` | packages/llm/llm/src/attribution.ts | 64-67 | 57-68 | `attributionHeaders` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/error.go:45` | packages/llm/llm/src/index.ts | 84-117 | 82-120 | `LlmError` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/error.go:107` | packages/llm/llm/src/adapter-failure.ts | 16-28 | 10-28 | `normalizeLlmFailure` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/generate.go:10` | packages/llm/llm/src/types.ts | 370 | 418-422 | `SessionId` | 裁决表・放宽大小写 |  | - |
| `llm/image.go:16` | packages/llm/llm/src/content.ts | 7-9 | 99-114 | `OffloadedImageText` | Go 声明名・放宽大小写 |  | - |
| `llm/image.go:58` | packages/llm/llm/src/content.ts | 43-46 | 129-132 | `base64Length` | Go 声明名 |  | - |
| `llm/image.go:113` | packages/llm/llm/src/content.ts | 64-80 | 152-168 | `collectImageLengths` | Go 声明名 |  | - |
| `llm/image.go:136` | packages/llm/llm/src/content.ts | 82-106 | 170-195 | `replaceOldestImages` | Go 声明名 |  | - |
| `llm/image.go:180` | packages/llm/llm/src/content.ts | 108-128 | 197-217 | `replaceImagesForTextModel` | Go 声明名 |  | - |
| `llm/invariant.go:18` | packages/llm/llm/src/invariant.ts | 7 | 9-10 | `name` | 裁决表 |  | - |
| `llm/llmretry/history.go:137` | packages/llm/llm-retry/src/history.ts | 14 | 5-33 | `providerForOpenStep` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/llmretry/invariant.go:21` | packages/llm/llm-retry/src/invariant.ts | 11 | 13-14 | `name` | 裁决表 |  | - |
| `llm/llmretry/invariant.go:54` | packages/llm/llm-retry/src/invariant.ts | 21-171 | 168-174 | `apply` | 裁决表 |  | - |
| `llm/llmretry/invariant.go:317` | packages/llm/llm-retry/src/invariant.ts | 142-171 | 126-146 | `validateStarted` | Go 声明名 |  | - |
| `llm/llmretry/invariant.go:383` | packages/llm/llm-retry/src/invariant.ts | 52-56 | 168-174 | `Apply` | Go 声明名・放宽大小写 |  | - |
| `llm/llmretry/invariant.go:414` | packages/llm/llm-retry/src/invariant.ts | 173-174 | 168-174 | `apply` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/llmretry/retry.go:51` | packages/llm/llm-retry/src/index.ts | 39-41 | 39-43 | `RetryInternals` | 注释锚点 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/mockserver/behavior.go:115` | packages/test-support/llm-mock-server/src/index.ts | 16 | 15-41 | `MOCK_LLM_BEHAVIORS` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/mockserver/behavior.go:148` | packages/test-support/llm-mock-server/src/index.ts | 56-70 | 52-70 | `DEFAULT_MOCK_LLM_RANDOM_WEIGHTS` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/mockserver/cli.go:132` | packages/test-support/llm-mock-server/src/cli.ts | 147-213 | 140-213 | `parseMockLlmCliArgs` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/mockserver/exchange.go:184` | packages/test-support/llm-mock-server/src/index.ts | 508-511 | 64-70 | `stall` | Go 声明名 |  | - |
| `llm/openaicompat/adapter.go:44` | packages/llm/llm-pi-ai/src/adapter.ts | 73-101 | 73-104 | `PiAiAdapterOptions` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/openaicompat/adapter.go:77` | packages/llm/llm-pi-ai/src/adapter.ts | 216-221 | 214-420 | `PiAiAdapter` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/openaicompat/adapter.go:131` | packages/llm/llm-pi-ai/src/adapter.ts | 229-236 | 226-239 | `current` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/openaicompat/adapter.go:213` | packages/llm/llm-pi-ai/src/adapter.ts | 202-209 | 204-212 | `requestHeaders` | Go 声明名 |  | - |
| `llm/openaicompat/adapter.go:237` | packages/llm/llm-pi-ai/src/adapter.ts | 239-245 | 241-248 | `profileOf` | Go 声明名 |  | - |
| `llm/openaicompat/adapter.go:249` | packages/llm/llm-pi-ai/src/adapter.ts | 248-255 | 250-258 | `modelOf` | Go 声明名 |  | - |
| `llm/openaicompat/adapter.go:265` | packages/llm/llm-pi-ai/src/adapter.ts | 257-262 | 260-265 | `ProviderInfo` | Go 声明名・放宽大小写 |  | - |
| `llm/openaicompat/adapter.go:278` | packages/llm/llm-pi-ai/src/adapter.ts | 264-266 | 267-269 | `ProviderRetryPolicy` | Go 声明名・放宽大小写 |  | - |
| `llm/openaicompat/adapter.go:289` | packages/llm/llm-pi-ai/src/adapter.ts | 268-279 | 271-282 | `ListModels` | Go 声明名・放宽大小写 |  | - |
| `llm/openaicompat/adapter.go:309` | packages/llm/llm-pi-ai/src/adapter.ts | 281-290 | 284-293 | `ResolveModel` | Go 声明名・放宽大小写 |  | - |
| `llm/openaicompat/adapter.go:320` | packages/llm/llm-pi-ai/src/adapter.ts | 310-316 | 313-319 | `PrepareCall` | Go 声明名・放宽大小写 |  | - |
| `llm/openaicompat/adapter.go:374` | packages/llm/llm-pi-ai/src/adapter.ts | 350 | 353 | `containsImage` | Go 声明名 |  | - |
| `llm/openaicompat/catalog.go:289` | packages/llm/llm-pi-ai/src/catalog.ts | 645-700 | 645-716 | `resolveModelReasoning` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/openaicompat/config.go:25` | packages/llm/llm-pi-ai/src/config.ts | 54 | 45-54 | `DEFAULT_MAX_REQUEST_IMAGE_BYTES` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/openaicompat/config.go:56` | packages/llm/llm-pi-ai/src/config.ts | 76 | 66-76 | `DEFAULT_INPUT` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/openaicompat/config.go:67` | packages/llm/llm-pi-ai/src/config.ts | 88-176 | 87-179 | `PiAiProviderProfile` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/openaicompat/config.go:370` | packages/llm/llm-pi-ai/src/config.ts | 349-351 | 344-358 | `assertServiceable` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/openaicompat/config.go:382` | packages/llm/llm-pi-ai/src/config.ts | 379-465 | 378-472 | `resolveProfiles` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/openaicompat/context.go:51` | packages/llm/llm-pi-ai/src/context.ts | 21-26 | 21-27 | `flattenText` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/openaicompat/context.go:64` | packages/llm/llm-pi-ai/src/context.ts | 29-34 | 30-35 | `toolResultText` | Go 声明名 |  | - |
| `llm/openaicompat/context.go:83` | packages/llm/llm-pi-ai/src/context.ts | 36-46 | 37-47 | `assertSupportedImageRoles` | Go 声明名 |  | - |
| `llm/openaicompat/context.go:219` | packages/llm/llm-pi-ai/src/context.ts | 87-95 | 92-100 | `collectImageRefs` | Go 声明名 |  | - |
| `llm/openaicompat/context.go:246` | packages/llm/llm-pi-ai/src/context.ts | 97-114 | 102-119 | `prepareRequestImages` | Go 声明名 |  | - |
| `llm/openaicompat/context.go:292` | packages/llm/llm-pi-ai/src/context.ts | 116-124 | 121-129 | `toolsOf` | Go 声明名 |  | - |
| `llm/openaicompat/discovery.go:59` | packages/llm/llm-pi-ai/src/discovery.ts | 53-62 | 52-62 | `listingEntry` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/openaicompat/discovery.go:99` | packages/llm/llm-pi-ai/src/discovery.ts | 73-78 | 72-78 | `label` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/openaicompat/discovery.go:112` | packages/llm/llm-pi-ai/src/discovery.ts | 86-88 | 80-88 | `listingURL` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/openaicompat/discovery.go:158` | packages/llm/llm-pi-ai/src/discovery.ts | 138-162 | 133-162 | `readListing` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/openaicompat/discovery.go:195` | packages/llm/llm-pi-ai/src/discovery.ts | 172-181 | 164-181 | `usableProbeKey` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/openaicompat/discovery.go:270` | packages/llm/llm-pi-ai/src/discovery.ts | 195-284 | 183-284 | `discoverModels` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/openaicompat/install.go:107` | packages/llm/llm-pi-ai/src/index.ts | 97-108 | 94-110 | `registrationFacts` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/openaicompat/install.go:150` | packages/llm/llm-pi-ai/src/index.ts | 118-138 | 112-140 | `directoryEntries` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/openaicompat/install.go:251` | packages/llm/llm-pi-ai/src/index.ts | 166-189 | 168-191 | `resolveAPIKey` | Go 声明名・放宽大小写 |  | - |
| `llm/openaicompat/install.go:333` | packages/llm/llm-pi-ai/src/index.ts | 219-233 | 226-240 | `ensureDirectory` | Go 声明名 |  | - |
| `llm/openaicompat/install.go:368` | packages/llm/llm-pi-ai/src/index.ts | 294-318 | 305-330 | `onChange` | Go 声明名 |  | - |
| `llm/openaicompat/replay.go:69` | packages/llm/llm-pi-ai/src/replay.ts | 72-103 | 64-103 | `toPiReplayState` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/openaicompat/replay.go:102` | packages/llm/llm-pi-ai/src/replay.ts | 110-141 | 109-141 | `ReadReplayState` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/openaicompat/stream.go:37` | packages/llm/llm-pi-ai/src/stream.ts | 22-29 | 18-32 | `mapUsage` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/openaicompat/stream.go:163` | packages/llm/llm-pi-ai/src/stream.ts | 76-116 | 70-128 | `mapStopReason` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/replay/config.go:174` | packages/test-support/llm-replay/src/index.ts | 540-545 | 599-605 | `deriveScriptFromFile` | Go 声明名 |  | - |
| `llm/replay/install.go:131` | packages/test-support/llm-replay/src/index.ts | 680-716 | 34-47 | `replayEntry` | Go 声明名・放宽大小写 |  | - |
| `llm/replay/install.go:201` | packages/test-support/llm-replay/src/index.ts | 663-677 | 738-755 | `paceDelay` | Go 声明名 |  | - |
| `llm/replay/placeholder.go:32` | packages/test-support/llm-replay/src/index.ts | 328-340 | 348-361 | `collectStrings` | Go 声明名 |  | - |
| `llm/replay/placeholder.go:89` | packages/test-support/llm-replay/src/index.ts | 343-357 | 363-378 | `resolveFromRequest` | Go 声明名 |  | - |
| `llm/replay/placeholder.go:112` | packages/test-support/llm-replay/src/index.ts | 360-379 | 380-398 | `substituteString` | Go 声明名 |  | - |
| `llm/replay/placeholder.go:146` | packages/test-support/llm-replay/src/index.ts | 382-390 | 400-410 | `substituteValue` | Go 声明名 |  | - |
| `llm/replay/placeholder.go:204` | packages/test-support/llm-replay/src/index.ts | 412-418 | 412-433 | `resolveScriptedEntry` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/replay/script.go:34` | packages/test-support/llm-replay/src/index.ts | 37-45 | 34-47 | `ReplayEntry` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/replay/script.go:146` | packages/test-support/llm-replay/src/index.ts | 427-429 | 484-486 | `invalidOverride` | Go 声明名 |  | - |
| `llm/replay/script.go:403` | packages/test-support/llm-replay/src/index.ts | 431-441 | 488-498 | `readChunks` | Go 声明名 |  | - |
| `llm/replay/script.go:427` | packages/test-support/llm-replay/src/index.ts | 423-425 | 480-482 | `hasExactKeys` | Go 声明名 |  | - |
| `llm/replay/script.go:505` | packages/test-support/llm-replay/src/index.ts | 480-505 | 546-564 | `readOverrideDoc` | Go 声明名 |  | - |
| `llm/runtime.go:148` | packages/llm/llm/src/index.ts | 1013-1017 | 1079-1083 | `adapterRegistration` | Go 声明名・放宽大小写 |  | - |
| `llm/runtime.go:158` | packages/llm/llm/src/index.ts | 1019-1024 | 1085-1090 | `preparedDispatch` | Go 声明名・放宽大小写 |  | - |
| `llm/runtime.go:283` | packages/llm/llm/src/index.ts | 323-355 | 338-364 | `emitAdaptersUpdated` | Go 声明名 |  | - |
| `llm/runtime.go:353` | packages/llm/llm/src/index.ts | 356-394 | 372-409 | `RegisterAdapter` | Go 声明名・放宽大小写 |  | - |
| `llm/runtime.go:451` | packages/llm/llm/src/index.ts | 396-423 | 411-438 | `prepareRoutes` | Go 声明名 |  | - |
| `llm/runtime.go:497` | packages/llm/llm/src/index.ts | 425-440 | 440-455 | `commitRoutes` | Go 声明名 |  | - |
| `llm/runtime.go:528` | packages/llm/llm/src/index.ts | 442-448 | 462-464 | `ListProviders` | Go 声明名・放宽大小写 |  | - |
| `llm/runtime.go:567` | packages/llm/llm/src/index.ts | 450-511 | 466-527 | `RegisterConfigurableProviders` | Go 声明名・放宽大小写 |  | - |
| `llm/runtime.go:620` | packages/llm/llm/src/index.ts | 460-488 | 477-504 | `commit` | Go 声明名 |  | - |
| `llm/runtime.go:713` | packages/llm/llm/src/index.ts | 513-519 | 534-536 | `ListConfigurableProviders` | Go 声明名・放宽大小写 |  | - |
| `llm/runtime.go:729` | packages/llm/llm/src/index.ts | 521-548 | 538-568 | `RegisterModelDiscovery` | Go 声明名・放宽大小写 |  | - |
| `llm/runtime.go:776` | packages/llm/llm/src/index.ts | 550-586 | 570-610 | `discoverModels` | 裁决表 |  | - |
| `llm/runtime.go:860` | packages/llm/llm/src/index.ts | 637-652 | 703-718 | `ResolveModelInfo` | Go 声明名・放宽大小写 |  | - |
| `llm/runtime.go:869` | packages/llm/llm/src/index.ts | 654-661 | 720-727 | `resolveModelInfoFor` | Go 声明名 |  | - |
| `llm/runtime.go:884` | packages/llm/llm/src/index.ts | 663-754 | 729-820 | `normalizeModelInfo` | Go 声明名 |  | - |
| `llm/runtime.go:945` | packages/llm/llm/src/index.ts | 756-768 | 822-834 | `ResolveCallConfig` | Go 声明名・放宽大小写 |  | - |
| `llm/runtime.go:963` | packages/llm/llm/src/index.ts | 779-814 | 845-880 | `resolveCallWithInfo` | Go 声明名 |  | - |
| `llm/runtime.go:1028` | packages/llm/llm/src/index.ts | 159-160 | 429-430 | `RetryPolicy` | Go 声明名・放宽大小写 |  | - |
| `llm/runtime.go:1055` | packages/llm/llm/src/index.ts | 165-166 | 899-906 | `AdapterDefaults` | Go 声明名・放宽大小写 |  | - |
| `llm/runtime.go:1060` | packages/llm/llm/src/index.ts | 850-867 | 1003 | `Stream` | Go 声明名・放宽大小写 |  | - |
| `llm/runtime.go:1134` | packages/llm/llm/src/index.ts | 974-987 | 1003 | `Stream` | Go 声明名・放宽大小写 |  | - |
| `llm/runtime.go:1149` | packages/llm/llm/src/index.ts | 989-999 | 1055-1065 | `streamWithRegistration` | Go 声明名 |  | - |
| `llm/runtime.go:1327` | packages/llm/llm/src/index.ts | 877-891 | 943-957 | `forAdapter` | Go 声明名 |  | - |
| `llm/runtime.go:1368` | packages/llm/llm/src/index.ts | 1002-1011 | 1068-1077 | `adapterFailureChunk` | Go 声明名 |  | - |
| `llm/stream.go:43` | packages/llm/llm/src/types.ts | 132-141 | 127-149 | `TokenUsage` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/tokenmeter/breakdownprojection.go:45` | packages/llm/token-meter/src/breakdown-projection.ts | 55-85 | 44-87 | `contextBreakdownProjectionDefinition` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/tokenmeter/meter.go:26` | packages/llm/token-meter/src/index.ts | 28-32 | 35-48 | `measurementAnchor` | Go 声明名・放宽大小写 |  | - |
| `llm/tokenmeter/meter.go:60` | packages/llm/token-meter/src/index.ts | 34-41 | 50-56 | `replayState` | Go 声明名・放宽大小写 |  | - |
| `llm/tokenmeter/meter.go:161` | packages/llm/token-meter/src/index.ts | 116-147 | 113-177 | `Measure` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/tokenmeter/meter.go:217` | packages/llm/token-meter/src/index.ts | 155-157 | 186-194 | `estimateMessage` | 裁决表 |  | - |
| `llm/tokenmeter/meter.go:460` | packages/llm/token-meter/src/index.ts | 44-49 | 58-64 | `usageTokens` | Go 声明名 |  | - |
| `llm/tokenmeter/meter.go:472` | packages/llm/token-meter/src/index.ts | 52-58 | 66-73 | `optionalHeaderEquals` | Go 声明名 |  | - |
| `llm/tokenmeter/projection.go:10` | packages/llm/token-meter/src/projection.ts | 13-18 | 7-18 | `TokenUsageProjection` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/tokenmeter/projection.go:28` | packages/llm/token-meter/src/projection.ts | 30-48 | 20-48 | `ContextPressureProjection` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/tokenmeter/projection.go:53` | packages/llm/token-meter/src/projection.ts | 59-66 | 50-66 | `ContextBreakdownProjection` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/tokenmeter/types.go:53` | packages/llm/token-meter/src/types.ts | 37 | 36-53 | `TokenSurfaceNode` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/tokenmeter/usageprojection.go:53` | packages/llm/token-meter/src/usage-projection.ts | 54-63 | 64 | `tokenUsageState` | Go 声明名・放宽大小写 |  | - |
| `llm/tokenmeter/usageprojection.go:67` | packages/llm/token-meter/src/usage-projection.ts | 95-107 | 108 | `contextPressureState` | Go 声明名・放宽大小写 |  | - |
| `llm/tokenmeter/usageprojection.go:91` | packages/llm/token-meter/src/usage-projection.ts | 19-24 | 20-25 | `bucketsFrom` | Go 声明名 |  | - |
| `llm/tokenmeter/usageprojection.go:103` | packages/llm/token-meter/src/usage-projection.ts | 32-41 | 33-42 | `addReplacing` | Go 声明名 |  | - |
| `llm/tokenmeter/usageprojection.go:121` | packages/llm/token-meter/src/usage-projection.ts | 76-77 | 76-78 | `pressureFrom` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/tokenmeter/usageprojection.go:132` | packages/llm/token-meter/src/usage-projection.ts | 80-85 | 80-86 | `usageOf` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/tokenmeter/usageprojection.go:163` | packages/llm/token-meter/src/usage-projection.ts | 119-151 | 110-157 | `tokenUsageProjectionDefinition` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `llm/tokenmeter/usageprojection.go:208` | packages/llm/token-meter/src/usage-projection.ts | 174-219 | 159-225 | `contextPressureProjectionDefinition` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `mcp/bridge.go:45` | packages/mcp/mcp-client/src/tools.ts | 37 | 37-38 | `ToolDisposers` | 注释锚点 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `mcp/bridge.go:68` | packages/mcp/mcp-client/src/tools.ts | 210-218 | 211-219 | `preparedProjection` | Go 声明名・放宽大小写 |  | - |
| `mcp/bridge.go:118` | packages/mcp/mcp-client/src/tools.ts | 143-193 | 120-194 | `syncTools` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `mcp/bridge.go:198` | packages/mcp/mcp-client/src/tools.ts | 244-272 | 232-273 | `createDefinition` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `mcp/bridge.go:266` | packages/mcp/mcp-client/src/tools.ts | 221-229 | 221-230 | `supportedOutputSchema` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `mcp/bridge.go:290` | packages/mcp/mcp-client/src/tools.ts | 275-291 | 275-292 | `createOutput` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `mcp/bridge.go:352` | packages/mcp/mcp-client/src/tools.ts | 303-361 | 294-362 | `createExecutor` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `mcp/config.go:75` | packages/mcp/mcp-client/src/connection.ts | 65-90 | 55-90 | `resolveReconnectPolicy` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `mcp/connection.go:81` | packages/mcp/mcp-client/src/connection.ts | 123-311 | 114-351 | `startConnection` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `mcp/connection.go:131` | packages/mcp/mcp-client/src/connection.ts | 237-311 | 163-166 | `run` | Go 声明名 |  | - |
| `mcp/connection.go:184` | packages/mcp/mcp-client/src/connection.ts | 237-305 | 227-305 | `connectGeneration` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `mcp/connection.go:365` | packages/mcp/mcp-client/src/transport.ts | 44-48 | 25-50 | `createTransport` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `mcp/content.go:155` | packages/mcp/mcp-client/src/tools.ts | 509-559 | 505-560 | `projectContent` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `mcp/content.go:217` | packages/mcp/mcp-client/src/tools.ts | 497-502 | 490-503 | `extractText` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `mcp/content.go:234` | packages/mcp/mcp-client/src/tools.ts | 364-366 | 364-367 | `containsImage` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `mcp/content.go:246` | packages/mcp/mcp-client/src/tools.ts | 379-391 | 379-392 | `decodeImage` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `mcp/content.go:261` | packages/mcp/mcp-client/src/tools.ts | 423-426 | 423-427 | `imageDiagnostic` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `mcp/content.go:273` | packages/mcp/mcp-client/src/tools.ts | 433-487 | 429-488 | `prepareImageProjection` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `mcp/content.go:343` | packages/mcp/mcp-client/src/tools.ts | 399-420 | 394-421 | `resolveImageAdmission` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `mcp/host.go:20` | packages/mcp/mcp-client/src/tools.ts | 399-420 | 394-421 | `resolveImageAdmission` | 注释锚点 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `mcp/host.go:46` | packages/mcp/mcp-client/src/index.ts | 45 | 40-45 | `activeServerNames` | 注释锚点 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `mcp/host.go:98` | packages/mcp/mcp-client/src/index.ts | 139-181 | 138-188 | `apply` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `mcp/naming.go:30` | packages/mcp/mcp-client/src/tools.ts | 111-117 | 98-118 | `publicToolName` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `plan/planmode/controller.go:134` | packages/plan/plan-mode/src/index.ts | 215-217 | 136-163 | `apply` | 裁决表 |  | - |
| `plan/planmode/controller.go:157` | packages/plan/plan-mode/src/index.ts | 218-431 | 136-163 | `apply` | 裁决表 |  | - |
| `plan/planmode/controller.go:248` | packages/plan/plan-mode/src/index.ts | 440-444 | 383-394 | `Get` | Go 声明名・放宽大小写 |  | - |
| `plan/planmode/fold.go:76` | packages/plan/plan-mode/src/index.ts | 176-183 | 366-370 | `hasOpenTurn` | Go 声明名 |  | - |
| `plan/planmode/fold.go:115` | packages/plan/plan-mode/src/index.ts | 91-97 | 83-90 | `firstHeading` | Go 声明名 |  | - |
| `plan/planmode/invariant.go:24` | packages/plan/plan-mode/src/invariant.ts | 20-26 | 14-26 | `ValidateEvent` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `plan/planmode/types.go:61` | packages/plan/plan-mode/src/types.ts | 19-22 | 13-24 | `PlanProjection` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `plan/planmode/types.go:78` | packages/plan/plan-mode/src/index.ts | 462 | 251 | `Outcome` | Go 声明名・放宽大小写 |  | - |
| `preset/agentpresets/authoring.go:107` | packages/preset/agent-presets/src/authoring.ts | 83-93 | 73-84 | `occupied` | Go 声明名 |  | - |
| `preset/agentpresets/discovery.go:31` | packages/preset/agent-presets/src/discovery.ts | 41 | 39-51 | `USER_PRESET_DIR` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `preset/agentpresets/discovery.go:104` | packages/preset/agent-presets/src/discovery.ts | 86-106 | 216-257 | `compositionProblem` | Go 声明名 |  | - |
| `preset/agentpresets/discovery.go:127` | packages/preset/agent-presets/src/discovery.ts | 113-122 | 259-273 | `isFile` | Go 声明名 |  | - |
| `preset/agentpresets/invariant.go:20` | packages/preset/agent-presets/src/invariant.ts | 17 | 19-20 | `name` | 裁决表 |  | - |
| `preset/agentpresets/metadata.go:46` | packages/preset/agent-presets/src/metadata.ts | 42-46 | 41-46 | `text` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `preset/agentpresets/metadata.go:79` | packages/preset/agent-presets/src/metadata.ts | 56-85 | 48-85 | `readPresetMetadata` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `preset/agentpresets/metadata.go:118` | packages/preset/agent-presets/src/metadata.ts | 95-105 | 87-105 | `renderPresetMetadata` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `preset/agentpresets/mount.go:219` | packages/preset/agent-presets/src/index.ts | 537-543 | 806-816 | `compositionStamp` | Go 声明名 |  | - |
| `preset/agentpresets/mount.go:243` | packages/preset/agent-presets/src/index.ts | 558-560 | 818-821 | `sameStamp` | Go 声明名 |  | - |
| `preset/agentpresets/preset.go:17` | packages/preset/agent-presets/src/preset.ts | 8 | 3-8 | `PresetTrust` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `preset/agentpresets/preset.go:99` | packages/preset/agent-presets/src/preset.ts | 52-62 | 51-70 | `Config` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `preset/agentpresets/preset.go:121` | packages/preset/agent-presets/src/index.ts | 96-105 | 114-126 | `resolvedRoots` | Go 声明名 |  | - |
| `preset/agentpresets/roster.go:45` | packages/preset/agent-presets/src/index.ts | 562-570 | 823-831 | `standingMount` | Go 声明名・放宽大小写 |  | - |
| `preset/agentpresets/roster.go:120` | packages/preset/agent-presets/src/index.ts | 130-182 | 103-112 | `Config` | 裁决表 |  | - |
| `preset/agentpresets/roster.go:161` | packages/preset/agent-presets/src/index.ts | 346-348 | 106-112 | `Roots` | Go 声明名・放宽大小写 |  | - |
| `preset/agentpresets/roster.go:173` | packages/preset/agent-presets/src/index.ts | 351-353 | 272-273 | `Authorable` | Go 声明名・放宽大小写 |  | - |
| `preset/agentpresets/roster.go:185` | packages/preset/agent-presets/src/index.ts | 199-201 | 244-250 | `List` | Go 声明名・放宽大小写 |  | - |
| `preset/agentpresets/roster.go:192` | packages/preset/agent-presets/src/index.ts | 213-221 | 103-112 | `Config` | 裁决表 |  | - |
| `preset/agentpresets/roster.go:220` | packages/preset/agent-presets/src/index.ts | 233-239 | 358-378 | `resolveMountable` | Go 声明名 |  | - |
| `preset/agentpresets/roster.go:239` | packages/preset/agent-presets/src/index.ts | 275-288 | 316 | `Mount` | Go 声明名・放宽大小写 |  | - |
| `preset/agentpresets/roster.go:270` | packages/preset/agent-presets/src/index.ts | 316-325 | 429-464 | `ComposeFrom` | Go 声明名・放宽大小写 |  | - |
| `preset/agentpresets/roster.go:303` | packages/preset/agent-presets/src/index.ts | 336-338 | 466-477 | `ComposedPreset` | Go 声明名・放宽大小写 |  | - |
| `preset/agentpresets/roster.go:323` | packages/preset/agent-presets/src/mount.ts | 222-230 | 232-251 | `standingMountFor` | Go 声明名 |  | - |
| `preset/agentpresets/roster.go:362` | packages/preset/agent-presets/src/index.ts | 485-488 | 730-744 | `StandingKeyFor` | Go 声明名・放宽大小写 |  | - |
| `preset/agentpresets/roster.go:381` | packages/preset/agent-presets/src/index.ts | 458-472 | 627-672 | `Recompose` | Go 声明名・放宽大小写 |  | - |
| `preset/agentpresets/roster.go:423` | packages/preset/agent-presets/src/index.ts | 361-363 | 325 | `Read` | Go 声明名・放宽大小写 |  | - |
| `preset/agentpresets/roster.go:434` | packages/preset/agent-presets/src/index.ts | 380-393 | 525-553 | `Copy` | Go 声明名・放宽大小写 |  | - |
| `preset/agentpresets/roster.go:470` | packages/preset/agent-presets/src/index.ts | 400-416 | 571-593 | `Remove` | Go 声明名・放宽大小写 |  | - |
| `preset/agentpresets/roster.go:505` | packages/preset/agent-presets/src/index.ts | 491-534 | 746-795 | `ensureStanding` | Go 声明名 |  | - |
| `schedule/schedule/domain.go:194` | packages/schedule/schedule/src/domain.ts | 408-422 | 406-421 | `decodeAtRecord` | Go 声明名 |  | - |
| `schedule/schedule/domain.go:216` | packages/schedule/schedule/src/domain.ts | 424-447 | 423-447 | `decodeEveryRecord` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `schedule/schedule/domain.go:354` | packages/schedule/schedule/src/domain.ts | 558-571 | 555-567 | `dispatchedRecord` | Go 声明名 |  | - |
| `schedule/schedule/errors.go:28` | packages/schedule/schedule/src/domain.ts | 55-86 | 55-87 | `ScheduleInputError` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `schedule/schedule/instant.go:123` | packages/schedule/schedule/src/domain.ts | 184-207 | 189-210 | `futureInstant` | Go 声明名 |  | - |
| `schedule/schedule/runtime.go:66` | packages/schedule/schedule/src/runtime.ts | 29-32 | 34-69 | `dueDecision` | Go 声明名 |  | - |
| `schedule/schedule/runtime.go:84` | packages/schedule/schedule/src/runtime.ts | 34-69 | 220-228 | `decide` | Go 声明名 |  | - |
| `schedule/schedule/runtime.go:218` | packages/schedule/schedule/src/runtime.ts | 96-99 | 97-100 | `Start` | Go 声明名・放宽大小写 |  | - |
| `schedule/schedule/runtime.go:238` | packages/schedule/schedule/src/runtime.ts | 101-125 | 102-128 | `RequestDrive` | Go 声明名・放宽大小写 |  | - |
| `schedule/schedule/runtime.go:258` | packages/schedule/schedule/src/runtime.ts | 127-138 | 130-140 | `Dispose` | Go 声明名・放宽大小写 |  | - |
| `schedule/schedule/runtime.go:298` | packages/schedule/schedule/src/runtime.ts | 158-161 | 159-163 | `isLive` | Go 声明名 |  | - |
| `schedule/schedule/runtime.go:318` | packages/schedule/schedule/src/runtime.ts | 163-166 | 165-168 | `isRunnable` | Go 声明名 |  | - |
| `schedule/schedule/runtime.go:339` | packages/schedule/schedule/src/runtime.ts | 174-181 | 177-184 | `arm` | Go 声明名 |  | - |
| `schedule/schedule/runtime.go:353` | packages/schedule/schedule/src/runtime.ts | 183-201 | 186-203 | `waitForIdle` | Go 声明名 |  | - |
| `schedule/schedule/runtime.go:393` | packages/schedule/schedule/src/runtime.ts | 203-217 | 205-218 | `readFolded` | Go 声明名 |  | - |
| `schedule/schedule/timeparse.go:47` | packages/schedule/schedule/src/domain.ts | 156-161 | 156-162 | `groupNumber` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `schedule/schedule/timeparse.go:158` | packages/schedule/schedule/src/domain.ts | 277-305 | 272-298 | `parseLocalAt` | Go 声明名 |  | - |
| `schedule/schedule/timeparse.go:198` | packages/schedule/schedule/src/domain.ts | 332-381 | 332-382 | `resolveLocalInstant` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `schedule/schedule/tools.go:837` | packages/schedule/schedule/src/tools.ts | 299-467 | 291-467 | `registerScheduleTools` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `schedule/schedule/types.go:37` | packages/schedule/schedule/src/invariant.ts | 11 | 13-14 | `name` | 裁决表 |  | - |
| `schedule/schedule/types.go:42` | packages/schedule/schedule/src/index.ts | 35 | 35-36 | `name` | 裁决表 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `sdk/sdkprotocol/invariant.go:21` | packages/sdk/protocol/src/invariant.ts | 29 | 24-30 | `apply` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `sdk/sdkprotocol/transport.go:32` | packages/sdk/protocol/src/transport.ts | 34-49 | 30-49 | `JsonRpcTransportPeer` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `sdk/sdkprotocol/transport.go:76` | packages/sdk/protocol/src/transport.ts | 62-269 | 56-269 | `JsonRpcLineTransport` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `sdk/sdkprotocol/transport.go:117` | packages/sdk/protocol/src/transport.ts | 87-92 | 84-92 | `Close` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `sdk/sdkprotocol/transport.go:181` | packages/sdk/protocol/src/transport.ts | 272-274 | 271-274 | `objectParams` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `sdk/sdkprotocol/transport.go:242` | packages/sdk/protocol/src/transport.ts | 87-92 | 84-92 | `Close` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `sdk/sdkprotocol/types.go:53` | packages/sdk/protocol/src/types.ts | 16-25 | 15-27 | `InitializeParams` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `sdk/sdkprotocol/types.go:78` | packages/sdk/protocol/src/types.ts | 30 | 31-32 | `ServerInfo` | Go 声明名・放宽大小写 |  | - |
| `sdk/sdkserver/config.go:22` | packages/sdk/server/src/invariant.ts | 10 | 12-13 | `name` | 裁决表 |  | - |
| `sdk/sdkserver/invariant.go:21` | packages/sdk/server/src/invariant.ts | 28-29 | 23-29 | `apply` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `sdk/sdkserver/server.go:123` | packages/sdk/server/src/server.ts | 111-125 | 130-169 | `Initialize` | Go 声明名・放宽大小写 |  | - |
| `sdk/sdkserver/server.go:177` | packages/sdk/server/src/server.ts | 237-239 | 294-296 | `hasAdapterFor` | Go 声明名 |  | - |
| `sdk/sdkserver/server.go:192` | packages/sdk/server/src/server.ts | 132-143 | 171-193 | `Prompt` | Go 声明名・放宽大小写 |  | - |
| `sdk/sdkserver/server.go:214` | packages/sdk/server/src/server.ts | 203-216 | 259-272 | `getOrCreateSession` | Go 声明名 |  | - |
| `sdk/sdkserver/server.go:246` | packages/sdk/server/src/server.ts | 218-235 | 274-292 | `createSession` | Go 声明名 |  | - |
| `sdk/sdkserver/server.go:284` | packages/sdk/server/src/server.ts | 150-181 | 201-209 | `Shutdown` | Go 声明名・放宽大小写 |  | - |
| `sdk/sdkserver/server.go:295` | packages/sdk/server/src/server.ts | 155-181 | 211-237 | `performShutdown` | Go 声明名 |  | - |
| `sdk/sdkserver/server.go:366` | packages/sdk/server/src/server.ts | 190-201 | 239-257 | `HandleRequest` | Go 声明名・放宽大小写 |  | - |
| `session/checkpointpolicy/policy.go:24` | packages/session/session-checkpoint-policy/src/index.ts | 63-83 | 52-83 | `apply` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/chunkrow.go:67` | packages/core/session/src/chunk-rows.ts | 96-123 | 110-145 | `classify` | Go 声明名 |  | - |
| `session/chunkrow.go:150` | packages/core/session/src/chunk-rows.ts | 136-151 | 157-173 | `continues` | Go 声明名 |  | - |
| `session/chunkrow.go:207` | packages/core/session/src/chunk-rows.ts | 154-180 | 175-202 | `buildRow` | Go 声明名 |  | - |
| `session/chunkrow.go:340` | packages/core/session/src/chunk-rows.ts | 248-328 | 314-352 | `expandRow` | Go 声明名 |  | - |
| `session/header.go:162` | packages/core/session/src/request-header.ts | 21-31 | 14-31 | `canonicalHeader` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/header.go:180` | packages/core/session/src/request-header.ts | 44-54 | 38-54 | `headerEquals` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/header.go:209` | packages/core/session/src/request-header.ts | 65-71 | 56-71 | `foldRequestHeader` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/invariant.go:75` | packages/core/session/src/invariant.ts | 32-39 | 236 | `Transition` | Go 声明名・放宽大小写 |  | - |
| `session/invariant.go:91` | packages/core/session/src/invariant.ts | 42-52 | 41-52 | `requireOpenStep` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/invariant.go:294` | packages/core/session/src/invariant.ts | 169-187 | 243-249 | `Apply` | Go 声明名・放宽大小写 |  | - |
| `session/invariant.go:312` | packages/core/session/src/invariant.ts | 207-214 | 243-249 | `apply` | 裁决表 |  | - |
| `session/persistence/coordinator.go:29` | packages/session/session-persistence/src/coordinator.ts | 84-89 | 84-90 | `PersistenceCoordinatorOptions` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/persistence/coordinator.go:106` | packages/session/session-persistence/src/coordinator.ts | 218-235 | 221-239 | `sessionState` | Go 声明名・放宽大小写 |  | - |
| `session/persistence/coordinator.go:176` | packages/session/session-persistence/src/coordinator.ts | 588-1362 | 579-1443 | `PersistenceCoordinator` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/persistence/coordinator_chain.go:64` | packages/session/session-persistence/src/coordinator.ts | 1010-1033 | 1086-1115 | `serialize` | Go 声明名 |  | - |
| `session/persistence/coordinator_chain.go:93` | packages/session/session-persistence/src/coordinator.ts | 993-998 | 1074-1080 | `waitForRetirement` | Go 声明名 |  | - |
| `session/persistence/coordinator_chain.go:122` | packages/session/session-persistence/src/coordinator.ts | 632-643 | 633-647 | `Create` | Go 声明名・放宽大小写 |  | - |
| `session/persistence/coordinator_chain.go:136` | packages/session/session-persistence/src/coordinator.ts | 645-659 | 669-682 | `createCore` | Go 声明名 |  | - |
| `session/persistence/coordinator_chain.go:161` | packages/session/session-persistence/src/coordinator.ts | 665-680 | 686-704 | `Append` | Go 声明名・放宽大小写 |  | - |
| `session/persistence/coordinator_chain.go:179` | packages/session/session-persistence/src/coordinator.ts | 682-711 | 706-734 | `appendCore` | Go 声明名 |  | - |
| `session/persistence/coordinator_chain.go:225` | packages/session/session-persistence/src/coordinator.ts | 1036-1044 | 1117-1126 | `adopt` | Go 声明名 |  | - |
| `session/persistence/coordinator_chain.go:258` | packages/session/session-persistence/src/coordinator.ts | 832-838 | 903-921 | `ReadFrom` | Go 声明名・放宽大小写 |  | - |
| `session/persistence/coordinator_chain.go:283` | packages/session/session-persistence/src/coordinator.ts | 840-869 | 923-952 | `readFromCore` | Go 声明名 |  | - |
| `session/persistence/coordinator_chain.go:328` | packages/session/session-persistence/src/coordinator.ts | 872-888 | 954-971 | `readStoredPrefix` | Go 声明名 |  | - |
| `session/persistence/coordinator_prepare.go:19` | packages/session/session-persistence/src/coordinator.ts | 720-747 | 736-771 | `Prepare` | Go 声明名・放宽大小写 |  | - |
| `session/persistence/coordinator_prepare.go:69` | packages/session/session-persistence/src/coordinator.ts | 756-775 | 773-799 | `Load` | Go 声明名・放宽大小写 |  | - |
| `session/persistence/coordinator_prepare.go:102` | packages/session/session-persistence/src/coordinator.ts | 787-819 | 801-843 | `Inspect` | Go 声明名・放宽大小写 |  | - |
| `session/persistence/coordinator_prepare.go:231` | packages/session/session-persistence/src/coordinator.ts | 892-931 | 973-1013 | `prepareCore` | Go 声明名 |  | - |
| `session/persistence/coordinator_prepare.go:282` | packages/session/session-persistence/src/coordinator.ts | 934-963 | 1015-1045 | `commitPrepared` | Go 声明名 |  | - |
| `session/persistence/coordinator_prepare.go:328` | packages/session/session-persistence/src/coordinator.ts | 966-971 | 1047-1053 | `isPreparedSourceCurrent` | Go 声明名 |  | - |
| `session/persistence/coordinator_prepare.go:347` | packages/session/session-persistence/src/coordinator.ts | 974-985 | 1055-1067 | `loadLiveSnapshot` | Go 声明名 |  | - |
| `session/persistence/coordinator_prepare.go:384` | packages/session/session-persistence/src/coordinator.ts | 988-990 | 1069-1072 | `inspectLive` | Go 声明名 |  | - |
| `session/persistence/coordinator_write.go:18` | packages/session/session-persistence/src/coordinator.ts | 1164-1183 | 1244-1264 | `initFor` | Go 声明名 |  | - |
| `session/persistence/coordinator_write.go:57` | packages/session/session-persistence/src/coordinator.ts | 1186-1208 | 1266-1289 | `attachPrepared` | Go 声明名 |  | - |
| `session/persistence/coordinator_write.go:110` | packages/session/session-persistence/src/coordinator.ts | 1237-1294 | 1305-1375 | `onCreated` | Go 声明名 |  | - |
| `session/persistence/coordinator_write.go:228` | packages/session/session-persistence/src/coordinator.ts | 1302-1324 | 1377-1405 | `adoptLivePrefix` | Go 声明名 |  | - |
| `session/persistence/coordinator_write.go:288` | packages/session/session-persistence/src/coordinator.ts | 1215-1222 | 1291-1303 | `seedMatchesPersisted` | Go 声明名 |  | - |
| `session/persistence/coordinator_write.go:321` | packages/session/session-persistence/src/coordinator.ts | 1140-1151 | 1220-1232 | `retire` | Go 声明名 |  | - |
| `session/persistence/coordinator_write.go:355` | packages/session/session-persistence/src/coordinator.ts | 1154-1161 | 1234-1242 | `retireCore` | Go 声明名 |  | - |
| `session/persistence/coordinator_write.go:377` | packages/session/session-persistence/src/coordinator.ts | 1326-1338 | 1407-1419 | `flush` | Go 声明名 |  | - |
| `session/persistence/coordinator_write.go:420` | packages/session/session-persistence/src/coordinator.ts | 1355-1361 | 1435-1442 | `appendLiveBatch` | Go 声明名 |  | - |
| `session/persistence/preparations.go:78` | packages/session/session-persistence/src/preparations.ts | 14-22 | 14-23 | `preparationEntry` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/persistence/preparations.go:147` | packages/session/session-persistence/src/preparations.ts | 42-44 | 44-51 | `has` | Go 声明名 |  | - |
| `session/persistence/preparations.go:157` | packages/session/session-persistence/src/preparations.ts | 53-65 | 53-72 | `inspect` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/persistence/preparations.go:187` | packages/session/session-persistence/src/preparations.ts | 75-123 | 119-175 | `reserve` | Go 声明名 |  | - |
| `session/persistence/preparations.go:267` | packages/session/session-persistence/src/preparations.ts | 130-139 | 177-191 | `reservationFor` | Go 声明名 |  | - |
| `session/persistence/preparations.go:292` | packages/session/session-persistence/src/preparations.ts | 145-151 | 193-203 | `attach` | Go 声明名 |  | - |
| `session/persistence/preparations.go:306` | packages/session/session-persistence/src/preparations.ts | 157-161 | 205-213 | `discard` | Go 声明名 |  | - |
| `session/persistence/preparations.go:323` | packages/session/session-persistence/src/preparations.ts | 168-182 | 215-234 | `release` | Go 声明名 |  | - |
| `session/persistence/preparations.go:346` | packages/session/session-persistence/src/preparations.ts | 188-191 | 236-243 | `invalidate` | Go 声明名 |  | - |
| `session/persistence/preparations.go:371` | packages/session/session-persistence/src/preparations.ts | 199-205 | 245-257 | `discardReady` | Go 声明名 |  | - |
| `session/persistence/preparations.go:392` | packages/session/session-persistence/src/preparations.ts | 211-216 | 259-268 | `assertWritable` | Go 声明名 |  | - |
| `session/persistence/preparations.go:412` | packages/session/session-persistence/src/preparations.ts | 223-228 | 270-280 | `takeReady` | Go 声明名 |  | - |
| `session/persistence/preparations.go:428` | packages/session/session-persistence/src/preparations.ts | 230-264 | 282-317 | `entryFor` | Go 声明名 |  | - |
| `session/persistence/stored.go:145` | packages/session/session-persistence/src/coordinator.ts | 264-272 | 268-275 | `SeedCoversPrefix` | Go 声明名・放宽大小写 |  | - |
| `session/persistence/writebehind.go:45` | packages/session/session-persistence/src/write-behind.ts | 18-24 | 18-159 | `SessionWriteBehind` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/persistence/writebehind.go:183` | packages/session/session-persistence/src/write-behind.ts | 113-133 | 117-136 | `drainBarrier` | Go 声明名 |  | - |
| `session/persistence/writebehind.go:284` | packages/session/session-persistence/src/write-behind.ts | 108-111 | 108-115 | `continueAutomatic` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/persistence/writebehind.go:299` | packages/session/session-persistence/src/write-behind.ts | 135-155 | 138-158 | `startWrite` | Go 声明名 |  | - |
| `session/projection/checkpoint.go:62` | packages/session/session-projection/src/index.ts | 342-368 | 397-423 | `RestoreFloor` | Go 声明名・放宽大小写 |  | - |
| `session/projection/checkpoint.go:95` | packages/session/session-projection/src/index.ts | 370-396 | 425-457 | `ViewCheckpoint` | Go 声明名・放宽大小写 |  | - |
| `session/projection/checkpoint.go:129` | packages/session/session-projection/src/index.ts | 398-454 | 459-523 | `Restore` | Go 声明名・放宽大小写 |  | - |
| `session/projection/definition.go:37` | packages/session/session-projection/src/index.ts | 34-82 | 34-86 | `ProjectionDefinition` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/projection/definition.go:99` | packages/session/session-projection/src/index.ts | 128-136 | 132-140 | `erasedDefinition` | Go 声明名・放宽大小写 |  | - |
| `session/projection/definition.go:139` | packages/session/session-projection/src/index.ts | 138-143 | 142-149 | `unitCell` | Go 声明名・放宽大小写 |  | - |
| `session/projection/definition.go:150` | packages/session/session-projection/src/index.ts | 145-161 | 311 | `registration` | Go 声明名 |  | - |
| `session/projection/registry.go:130` | packages/session/session-projection/src/index.ts | 265-279 | 283-297 | `OnChanged` | Go 声明名・放宽大小写 |  | - |
| `session/projection/registry.go:151` | packages/session/session-projection/src/index.ts | 281-295 | 299-315 | `StateOf` | Go 声明名・放宽大小写 |  | - |
| `session/projection/registry.go:235` | packages/session/session-projection/src/index.ts | 473-494 | 625-662 | `Drive` | Go 声明名・放宽大小写 |  | - |
| `session/projection/registry.go:298` | packages/session/session-projection/src/index.ts | 456-461 | 579-588 | `buildCell` | Go 声明名 |  | - |
| `session/projectioncache/cache.go:95` | packages/session/session-projection-cache/src/index.ts | 54-60 | 60-66 | `dirtyState` | Go 声明名・放宽大小写 |  | - |
| `session/projectioncache/cache.go:112` | packages/session/session-projection-cache/src/index.ts | 71-287 | 68-311 | `SessionProjectionCache` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/projectioncache/cache.go:189` | packages/session/session-projection-cache/src/index.ts | 107-130 | 115-142 | `CachedSnapshot` | Go 声明名・放宽大小写 |  | - |
| `session/projectioncache/cache.go:222` | packages/session/session-projection-cache/src/index.ts | 154-196 | 195-215 | `ColdSnapshot` | Go 声明名・放宽大小写 |  | - |
| `session/projectioncache/cache.go:294` | packages/session/session-projection-cache/src/index.ts | 132-152 | 172-193 | `Write` | Go 声明名・放宽大小写 |  | - |
| `session/projectioncache/cache.go:436` | packages/session/session-projection-cache/src/index.ts | 253-262 | 286-295 | `markClean` | Go 声明名 |  | - |
| `session/projectioncache/cache.go:456` | packages/session/session-projection-cache/src/index.ts | 101-105 | 97-113 | `recordFor` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/projectioncache/cache.go:470` | packages/session/session-projection-cache/src/index.ts | 264-271 | 297-304 | `put` | Go 声明名 |  | - |
| `session/projectioncache/record.go:56` | packages/session/session-projection-cache/src/index.ts | 289-292 | 313-316 | `IdentityOf` | Go 声明名・放宽大小写 |  | - |
| `session/projectioncache/record.go:102` | packages/session/session-projection-cache/src/spec.ts | 66-70 | 62-74 | `projectionCacheDomainSpec` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/repair.go:45` | packages/core/session/src/repair.ts | 27-133 | 19-134 | `interruptedTurnClosers` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/sessiontitle/normalize.go:84` | packages/session/session-title/src/normalize.ts | 22-31 | 21-31 | `cleanTitleText` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/sessiontitle/normalize.go:110` | packages/session/session-title/src/normalize.ts | 39-51 | 33-51 | `truncateTitleUtf8` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/sessiontitle/normalize.go:137` | packages/session/session-title/src/normalize.ts | 59-61 | 53-61 | `normalizeSessionTitle` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/sessiontitle/normalize.go:147` | packages/session/session-title/src/normalize.ts | 70-74 | 63-74 | `fallbackSessionTitle` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/sessiontitle/service.go:122` | packages/session/session-title/src/index.ts | 276-342 | 54-62 | `Config` | 裁决表 |  | - |
| `session/sessiontitle/service.go:184` | packages/session/session-title/src/index.ts | 349-351 | 379-386 | `Get` | Go 声明名・放宽大小写 |  | - |
| `session/sessiontitle/service.go:194` | packages/session/session-title/src/index.ts | 364-384 | 388-419 | `Rename` | Go 声明名・放宽大小写 |  | - |
| `session/sessiontitle/service.go:235` | packages/session/session-title/src/index.ts | 393-427 | 421-461 | `Refresh` | Go 声明名・放宽大小写 |  | - |
| `session/sessiontitle/service.go:305` | packages/session/session-title/src/index.ts | 435-460 | 463-494 | `Register` | Go 声明名・放宽大小写 |  | - |
| `session/sessiontitle/service.go:377` | packages/session/session-title/src/index.ts | 332-335 | 536-550 | `OnMainRequest` | Go 声明名・放宽大小写 |  | - |
| `session/sessiontitle/service.go:437` | packages/session/session-title/src/index.ts | 463-487 | 496-521 | `onUserMessage` | Go 声明名 |  | - |
| `session/sessiontitle/service.go:483` | packages/session/session-title/src/index.ts | 490-500 | 523-534 | `onRequestHeader` | Go 声明名 |  | - |
| `session/sessiontitle/service.go:549` | packages/session/session-title/src/index.ts | 552-584 | 585-618 | `runProvider` | Go 声明名 |  | - |
| `session/sessiontitle/service.go:607` | packages/session/session-title/src/index.ts | 587-633 | 620-667 | `validateResult` | Go 声明名 |  | - |
| `session/sessiontitle/service.go:656` | packages/session/session-title/src/index.ts | 636-648 | 669-682 | `assertCurrent` | Go 声明名 |  | - |
| `session/sessiontitle/service.go:720` | packages/session/session-title/src/index.ts | 666-671 | 699-705 | `supersede` | Go 声明名 |  | - |
| `session/sessiontitle/service.go:734` | packages/session/session-title/src/index.ts | 674-681 | 707-715 | `stateFor` | Go 声明名 |  | - |
| `session/sessiontitle/service.go:748` | packages/session/session-title/src/index.ts | 745-753 | 776-791 | `appendFallback` | Go 声明名 |  | - |
| `session/sessiontitle/service.go:834` | packages/session/session-title/src/index.ts | 722-736 | 759-774 | `validateProvider` | Go 声明名 |  | - |
| `session/sessiontitle/types.go:121` | packages/session/session-title/src/index.ts | 61-68 | 71-77 | `SessionEventMap` | 裁决表 |  | - |
| `session/sessiontitle/types.go:139` | packages/session/session-title/src/index.ts | 71-76 | 415 | `Snapshot` | Go 声明名・放宽大小写 |  | - |
| `session/sessiontitlellm/provider.go:52` | packages/session/session-title-llm/src/index.ts | 153-169 | 146-170 | `registerSessionTitleLlmProvider` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/sessiontitlellm/provider.go:86` | packages/session/session-title-first-prompt-llm/src/index.ts | 33-40 | 29-40 | `apply` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/sessiontitlellm/provider.go:102` | packages/session/session-title-all-prompts-llm/src/index.ts | 33-35 | 29-36 | `apply` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/sessiontitlellm/provider.go:119` | packages/session/session-title-llm/src/index.ts | 229-294 | 221-295 | `generateSessionTitleWithLlm` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/sessiontitlellm/provider.go:243` | packages/session/session-title-llm/src/index.ts | 172-183 | 172-184 | `resolveRoute` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/sessiontitlellm/provider.go:257` | packages/session/session-title-llm/src/index.ts | 186-193 | 186-194 | `systemPrompt` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/sessiontitlellm/provider.go:274` | packages/session/session-title-llm/src/index.ts | 196-198 | 196-199 | `frameMessages` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/sessiontitlellm/provider.go:294` | packages/session/session-title-llm/src/index.ts | 201-218 | 201-219 | `finishError` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/sessiontitlellm/types.go:41` | packages/session/session-title-llm/src/index.ts | 25-38 | 25-39 | `SessionTitleLlmRequestEventData` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/sessiontitlellm/types.go:59` | packages/session/session-title-llm/src/index.ts | 48 | 48-49 | `SESSION_TITLE_TIMEOUT_CODE` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/sessiontitlellm/types.go:90` | packages/session/session-title-llm/src/index.ts | 51-66 | 51-67 | `SessionTitleLlmConfig` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/sessiontitlellm/types.go:117` | packages/session/session-title-llm/src/index.ts | 108-138 | 104-139 | `resolveSessionTitleLlmConfig` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/sessiontitlellm/types.go:151` | packages/session/session-title-llm/src/index.ts | 141-143 | 141-144 | `SessionTitleLlmMessageSelector` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/stats/stats.go:19` | packages/session/session-stats/src/projection.ts | 113 | 130-226 | `Key` | Go 声明名・放宽大小写 |  | - |
| `session/stats/stats.go:24` | packages/session/session-stats/src/projection.ts | 114 | 131-226 | `StateVersion` | Go 声明名・放宽大小写 |  | - |
| `session/stats/stats.go:29` | packages/session/session-stats/src/types.ts | 22-39 | 15-39 | `SessionStatsProjection` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/stats/stats.go:196` | packages/session/session-stats/src/projection.ts | 129-195 | 146-226 | `apply` | Go 声明名 |  | - |
| `session/stats/stats.go:321` | packages/session/session-stats/src/projection.ts | 105-109 | 173 | `outputTokens` | Go 声明名 |  | - |
| `session/surface.go:28` | packages/core/session/src/surface.ts | 26-28 | 21-28 | `isSurfaceEligibleType` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/surface.go:36` | packages/core/session/src/surface.ts | 35-38 | 30-38 | `isSurfaceEvent` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/surface.go:44` | packages/core/session/src/surface.ts | 51-55 | 40-55 | `isAppendSurfaceEvent` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/surface.go:60` | packages/core/session/src/surface.ts | 64-68 | 57-68 | `isReplacementSurfaceEvent` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/surface.go:70` | packages/core/session/src/surface.ts | 83-114 | 70-114 | `deriveEventMessage` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/surface.go:141` | packages/core/session/src/surface.ts | 185-208 | 184-208 | `SurfaceOpOf` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/surface.go:171` | packages/core/session/src/surface.ts | 211-243 | 210-243 | `assertProvenance` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/surface.go:231` | packages/core/session/src/surface.ts | 246-266 | 245-266 | `replacementRange` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/surface.go:252` | packages/core/session/src/surface.ts | 287-318 | 286-318 | `assertToolResultRewrite` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/surface.go:341` | packages/core/session/src/surface.ts | 321-347 | 320-347 | `planSurfaceEvent` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/surface.go:396` | packages/core/session/src/surface.ts | 362-379 | 361-379 | `applySurfacePlan` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/surface.go:422` | packages/core/session/src/surface.ts | 387-395 | 381-395 | `foldSurface` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/surface.go:466` | packages/core/session/src/surface.ts | 421-429 | 417-429 | `ValidateNext` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/telemetry/coordinator.go:77` | packages/session/session-telemetry/src/coordinator.ts | 60-259 | 45-268 | `SessionTelemetryCoordinator` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/telemetry/coordinator.go:142` | packages/session/session-telemetry/src/coordinator.ts | 150-154 | 151-170 | `Adopt` | Go 声明名・放宽大小写 |  | - |
| `session/telemetry/coordinator.go:168` | packages/session/session-telemetry/src/coordinator.ts | 122-134 | 128-149 | `CaptureSession` | Go 声明名・放宽大小写 |  | - |
| `session/telemetry/coordinator.go:204` | packages/session/session-telemetry/src/coordinator.ts | 220-222 | 223-226 | `HintFlush` | Go 声明名・放宽大小写 |  | - |
| `session/telemetry/coordinator.go:378` | packages/session/session-telemetry/src/coordinator.ts | 157-161 | 172-177 | `track` | Go 声明名 |  | - |
| `session/telemetry/coordinator.go:392` | packages/session/session-telemetry/src/coordinator.ts | 164-190 | 179-203 | `captureEvent` | Go 声明名 |  | - |
| `session/telemetry/coordinator.go:428` | packages/session/session-telemetry/src/coordinator.ts | 201-217 | 217-221 | `deliver` | Go 声明名 |  | - |
| `session/telemetry/coordinator.go:457` | packages/session/session-telemetry/src/coordinator.ts | 252-258 | 256-267 | `contain` | Go 声明名 |  | - |
| `session/telemetry/coordinator.go:489` | packages/session/session-telemetry/src/coordinator.ts | 265-273 | 270-282 | `shutdownRecord` | Go 声明名 |  | - |
| `session/telemetry/coordinator.go:505` | packages/session/session-telemetry/src/coordinator.ts | 276-291 | 284-297 | `severityOf` | Go 声明名 |  | - |
| `session/telemetry/coordinator.go:539` | packages/session/session-telemetry/src/coordinator.ts | 302-317 | 305-319 | `identityOf` | Go 声明名 |  | - |
| `session/telemetry/coordinator.go:566` | packages/session/session-telemetry/src/coordinator.ts | 294-299 | 299-303 | `errorDetail` | Go 声明名 |  | - |
| `session/telemetry/record.go:34` | packages/session/session-telemetry/src/index.ts | 47-54 | 47-55 | `SessionTelemetrySeverity` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `session/telemetry/record.go:172` | packages/session/session-telemetry/src/index.ts | 90-128 | 89-131 | `SessionTelemetrySink` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `sessionquery/corpus.go:93` | packages/session-query/session-query/src/corpus.ts | 31-51 | 31-221 | `SessionCorpus` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `sessionquery/documents.go:13` | packages/session-query/session-query/src/documents.ts | 15-27 | 9-27 | `buildSessionEventRecords` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `sessionquery/documents.go:37` | packages/session-query/session-query/src/documents.ts | 29-53 | 29-54 | `buildSessionEventSearchDocuments` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `sessionquery/documents.go:71` | packages/session-query/session-query/src/documents.ts | 55-73 | 56-74 | `classifySurface` | Go 声明名 |  | - |
| `sessionquery/engine.go:69` | packages/session-query/session-query/src/index.ts | 81-357 | 80-378 | `SessionQueryEngine` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `sessionquery/engine.go:103` | packages/session-query/session-query/src/index.ts | 129-136 | 150-157 | `ListSessions` | Go 声明名・放宽大小写 |  | - |
| `sessionquery/engine.go:110` | packages/session-query/session-query/src/index.ts | 138-151 | 159-172 | `ReadSession` | Go 声明名・放宽大小写 |  | - |
| `sessionquery/engine.go:134` | packages/session-query/session-query/src/index.ts | 153-165 | 174-186 | `FilterSessions` | Go 声明名・放宽大小写 |  | - |
| `sessionquery/engine.go:149` | packages/session-query/session-query/src/index.ts | 217-225 | 238-246 | `ListEvents` | Go 声明名・放宽大小写 |  | - |
| `sessionquery/engine.go:160` | packages/session-query/session-query/src/index.ts | 227-239 | 248-260 | `FilterEvents` | Go 声明名・放宽大小写 |  | - |
| `sessionquery/engine.go:183` | packages/session-query/session-query/src/index.ts | 257-270 | 278-291 | `ReadSurface` | Go 声明名・放宽大小写 |  | - |
| `sessionquery/engine.go:203` | packages/session-query/session-query/src/index.ts | 272-283 | 293-304 | `traceSession` | 裁决表 |  | - |
| `sessionquery/engine.go:217` | packages/session-query/session-query/src/index.ts | 285-299 | 306-320 | `traceEvent` | 裁决表 |  | - |
| `sessionquery/engine.go:269` | packages/session-query/session-query/src/index.ts | 107-116 | 128-137 | `SearchSessions` | Go 声明名・放宽大小写 |  | - |
| `sessionquery/engine.go:279` | packages/session-query/session-query/src/index.ts | 118-127 | 139-148 | `SearchEvents` | Go 声明名・放宽大小写 |  | - |
| `sessionquery/extraction.go:16` | packages/session-query/session-query/src/extraction.ts | 13-40 | 7-44 | `extractSessionEventText` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `sessionquery/extraction.go:73` | packages/session-query/session-query/src/extraction.ts | 42-58 | 46-62 | `turnEndText` | Go 声明名 |  | - |
| `sessionquery/extraction.go:101` | packages/session-query/session-query/src/extraction.ts | 62-64 | 66-68 | `contentText` | Go 声明名 |  | - |
| `sessionquery/extraction.go:112` | packages/session-query/session-query/src/extraction.ts | 66-81 | 70-85 | `blockText` | Go 声明名 |  | - |
| `sessionquery/extraction.go:144` | packages/session-query/session-query/src/extraction.ts | 83-85 | 87-89 | `joinText` | Go 声明名 |  | - |
| `sessionquery/filters.go:185` | packages/session-query/session-query/src/filters.ts | 123-141 | 120-138 | `sessionPredicate` | Go 声明名 |  | - |
| `sessionquery/filters.go:223` | packages/session-query/session-query/src/filters.ts | 143-166 | 140-162 | `eventPredicate` | Go 声明名 |  | - |
| `sessionquery/filters.go:266` | packages/session-query/session-query/src/filters.ts | 196-207 | 182-193 | `copyRange` | Go 声明名 |  | - |
| `sessionquery/filters.go:282` | packages/session-query/session-query/src/filters.ts | 227-240 | 215-226 | `validateRange` | Go 声明名 |  | - |
| `sessionquery/filters.go:314` | packages/session-query/session-query/src/filters.ts | 209-213 | 195-198 | `unknownFilter` | Go 声明名 |  | - |
| `sessionquery/querytool/access.go:35` | packages/session-query/tool-session-query/src/workspace-access.ts | 52-66 | 58-72 | `callerOf` | Go 声明名 |  | - |
| `sessionquery/querytool/access.go:53` | packages/session-query/tool-session-query/src/workspace-access.ts | 68-70 | 74-76 | `targetID` | Go 声明名・放宽大小写 |  | - |
| `sessionquery/querytool/access.go:63` | packages/session-query/tool-session-query/src/workspace-access.ts | 72-88 | 78-93 | `authorizeTarget` | Go 声明名 |  | - |
| `sessionquery/querytool/access.go:101` | packages/session-query/tool-session-query/src/workspace-access.ts | 94-97 | 99-102 | `headerAuthorized` | Go 声明名 |  | - |
| `sessionquery/querytool/access.go:115` | packages/session-query/tool-session-query/src/workspace-access.ts | 99-108 | 104-112 | `assertObservedTargetAuthorized` | Go 声明名 |  | - |
| `sessionquery/querytool/access.go:129` | packages/session-query/tool-session-query/src/workspace-access.ts | 110-134 | 114-138 | `authorizeSessionIDs` | Go 声明名・放宽大小写 |  | - |
| `sessionquery/querytool/access.go:180` | packages/session-query/tool-session-query/src/workspace-access.ts | 28-31 | 32-35 | `titleView` | Go 声明名・放宽大小写 |  | - |
| `sessionquery/querytool/access.go:195` | packages/session-query/tool-session-query/src/workspace-access.ts | 136-152 | 140-158 | `readTitles` | Go 声明名 |  | - |
| `sessionquery/querytool/access.go:236` | packages/session-query/tool-session-query/src/workspace-access.ts | 154-161 | 160-167 | `readTitle` | Go 声明名 |  | - |
| `sessionquery/querytool/access.go:247` | packages/session-query/tool-session-query/src/workspace-access.ts | 163-170 | 169-176 | `unavailableTitle` | Go 声明名 |  | - |
| `sessionquery/querytool/access.go:263` | packages/session-query/tool-session-query/src/workspace-access.ts | 232-236 | 240-244 | `titleText` | Go 声明名 |  | - |
| `sessionquery/querytool/access.go:273` | packages/session-query/tool-session-query/src/workspace-access.ts | 33-36 | 41-44 | `authorizedDescendant` | Go 声明名・放宽大小写 |  | - |
| `sessionquery/querytool/access.go:283` | packages/session-query/tool-session-query/src/workspace-access.ts | 172-201 | 178-208 | `authorizeDescendants` | Go 声明名 |  | - |
| `sessionquery/querytool/access.go:311` | packages/session-query/tool-session-query/src/workspace-access.ts | 38-42 | 52-56 | `descendantVisit` | Go 声明名・放宽大小写 |  | - |
| `sessionquery/querytool/access.go:321` | packages/session-query/tool-session-query/src/workspace-access.ts | 203-220 | 210-230 | `visitDescendants` | Go 声明名 |  | - |
| `sessionquery/querytool/access.go:343` | packages/session-query/tool-session-query/src/workspace-access.ts | 222-230 | 232-238 | `descendantIDs` | Go 声明名・放宽大小写 |  | - |
| `sessionquery/querytool/boundary.go:70` | packages/session-query/tool-session-query/src/service-boundary.ts | 89-94 | 92-97 | `unauthorizedTarget` | Go 声明名 |  | - |
| `sessionquery/querytool/boundary.go:77` | packages/session-query/tool-session-query/src/service-boundary.ts | 138-143 | 143-148 | `genericFailure` | Go 声明名 |  | - |
| `sessionquery/querytool/boundary.go:84` | packages/session-query/tool-session-query/src/service-boundary.ts | 96-112 | 99-114 | `call` | Go 声明名 |  | - |
| `sessionquery/querytool/config.go:92` | packages/session-query/tool-session-query/src/index.ts | 124-136 | 56-122 | `apply` | 裁决表 |  | - |
| `sessionquery/querytool/input.go:197` | packages/session-query/tool-session-query/src/input.ts | 127-143 | 126-141 | `normalizeQuery` | Go 声明名 |  | - |
| `sessionquery/querytool/input.go:215` | packages/session-query/tool-session-query/src/input.ts | 145-160 | 143-156 | `sequenceRange` | Go 声明名 |  | - |
| `sessionquery/querytool/input.go:240` | packages/session-query/tool-session-query/src/input.ts | 162-184 | 158-177 | `timestampRange` | Go 声明名 |  | - |
| `sessionquery/querytool/input.go:286` | packages/session-query/tool-session-query/src/input.ts | 189-193 | 182-186 | `exactTimestamp` | Go 声明名・放宽大小写 |  | - |
| `sessionquery/querytool/input.go:296` | packages/session-query/tool-session-query/src/input.ts | 195-227 | 188-221 | `parseISOTimestamp` | Go 声明名・放宽大小写 |  | - |
| `sessionquery/querytool/input.go:347` | packages/session-query/tool-session-query/src/input.ts | 229-239 | 223-234 | `compareTimestamps` | Go 声明名 |  | - |
| `sessionquery/querytool/input.go:381` | packages/session-query/tool-session-query/src/input.ts | 241-245 | 236-240 | `timestampLowerBound` | Go 声明名 |  | - |
| `sessionquery/querytool/input.go:399` | packages/session-query/tool-session-query/src/input.ts | 247-251 | 242-246 | `timestampUpperBound` | Go 声明名 |  | - |
| `sessionquery/querytool/input.go:409` | packages/session-query/tool-session-query/src/input.ts | 271-274 | 266-269 | `daysInMonth` | Go 声明名 |  | - |
| `sessionquery/querytool/input.go:440` | packages/session-query/tool-session-query/src/input.ts | 276-281 | 271-276 | `invalidRange` | Go 声明名 |  | - |
| `sessionquery/querytool/operations.go:137` | packages/session-query/tool-session-query/src/operations.ts | 116-166 | 116-168 | `executeEventSearch` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `sessionquery/querytool/operations.go:223` | packages/session-query/tool-session-query/src/operations.ts | 168-198 | 170-200 | `executeSessionTrace` | Go 声明名 |  | - |
| `sessionquery/querytool/operations.go:279` | packages/session-query/tool-session-query/src/operations.ts | 200-214 | 202-216 | `executeEventTrace` | Go 声明名 |  | - |
| `sessionquery/querytool/operations.go:316` | packages/session-query/tool-session-query/src/operations.ts | 216-237 | 218-239 | `executeEventRead` | Go 声明名 |  | - |
| `sessionquery/querytool/operations.go:371` | packages/session-query/tool-session-query/src/operations.ts | 239-272 | 241-274 | `collectPages` | Go 声明名 |  | - |
| `sessionquery/querytool/presentation.go:24` | packages/session-query/tool-session-query/src/operations.ts | 48-51 | 49-52 | `searchCollection` | Go 声明名・放宽大小写 |  | - |
| `sessionquery/querytool/presentation.go:112` | packages/session-query/tool-session-query/src/presentation.ts | 109-133 | 108-131 | `formatSessionTrace` | Go 声明名 |  | - |
| `sessionquery/querytool/presentation.go:152` | packages/session-query/tool-session-query/src/presentation.ts | 135-149 | 133-147 | `renderDescendants` | Go 声明名 |  | - |
| `sessionquery/querytool/presentation.go:171` | packages/session-query/tool-session-query/src/presentation.ts | 151-165 | 149-163 | `formatEventTrace` | Go 声明名 |  | - |
| `sessionquery/querytool/presentation.go:191` | packages/session-query/tool-session-query/src/presentation.ts | 167-192 | 165-188 | `formatEventRead` | Go 声明名 |  | - |
| `sessionquery/querytool/presentation.go:237` | packages/session-query/tool-session-query/src/presentation.ts | 194-199 | 190-194 | `formatNeighbor` | Go 声明名 |  | - |
| `sessionquery/querytool/presentation.go:260` | packages/session-query/tool-session-query/src/presentation.ts | 201-208 | 196-201 | `availabilityText` | Go 声明名 |  | - |
| `sessionquery/querytool/presentation.go:280` | packages/session-query/tool-session-query/src/presentation.ts | 210-212 | 203-205 | `seqList` | Go 声明名 |  | - |
| `sessionquery/querytool/presentation.go:294` | packages/session-query/tool-session-query/src/presentation.ts | 214-216 | 207-209 | `formatTime` | Go 声明名 |  | - |
| `sessionquery/querytool/presentation.go:304` | packages/session-query/tool-session-query/src/presentation.ts | 218-220 | 211-213 | `presentSessionSearchCall` | Go 声明名 |  | - |
| `sessionquery/querytool/presentation.go:313` | packages/session-query/tool-session-query/src/presentation.ts | 222-224 | 215-217 | `presentEventSearchCall` | Go 声明名 |  | - |
| `sessionquery/querytool/presentation.go:322` | packages/session-query/tool-session-query/src/presentation.ts | 226-234 | 219-226 | `presentSessionTraceCall` | Go 声明名 |  | - |
| `sessionquery/querytool/presentation.go:338` | packages/session-query/tool-session-query/src/presentation.ts | 236-250 | 228-241 | `presentEventTargetCall` | Go 声明名 |  | - |
| `sessionquery/sources.go:13` | packages/session-query/session-query/src/sources.ts | 11-26 | 6-26 | `assertSessionHeadersCompatible` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `sessionquery/titles.go:45` | packages/session-query/session-query/src/index.ts | 195-215 | 216-236 | `ReadTitleSnapshots` | Go 声明名・放宽大小写 |  | - |
| `sessionquery/titles.go:68` | packages/session-query/session-query/src/index.ts | 180-193 | 201-214 | `ReadTitleSnapshot` | Go 声明名・放宽大小写 |  | - |
| `sessionquery/titles.go:87` | packages/session-query/session-query/src/index.ts | 167-178 | 188-199 | `ReadTitle` | Go 声明名・放宽大小写 |  | - |
| `sessionquery/tracing.go:16` | packages/session-query/session-query/src/tracing.ts | 11-16 | 14-19 | `eventLogAnalysis` | Go 声明名・放宽大小写 |  | - |
| `sessionquery/tracing.go:107` | packages/session-query/session-query/src/tracing.ts | 113-172 | 107-173 | `traceSession` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `sessionquery/tracing.go:197` | packages/session-query/session-query/src/tracing.ts | 216-241 | 220-244 | `buildDescendants` | Go 声明名 |  | - |
| `settings/invariant.go:16` | packages/settings/settings/src/invariant.ts | 10 | 12-13 | `name` | 裁决表 |  | - |
| `settings/json.go:17` | packages/settings/settings/src/index.ts | 241-288 | 231-278 | `cloneJSONShaped` | Go 声明名・放宽大小写 |  | - |
| `settings/json.go:65` | packages/settings/settings/src/index.ts | 290-305 | 280-295 | `mergeLayers` | Go 声明名 |  | - |
| `settings/json.go:139` | packages/settings/settings/src/index.ts | 204-228 | 194-218 | `applyPathOp` | Go 声明名 |  | - |
| `settings/provider.go:158` | packages/settings/settings/src/index.ts | 104-115 | 449 | `Watcher` | Go 声明名・放宽大小写 |  | - |
| `settings/provider.go:241` | packages/settings/settings/src/index.ts | 344-812 | 327-855 | `SettingsProvider` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `settings/provider.go:316` | packages/settings/settings/src/index.ts | 425-470 | 408-459 | `Register` | Go 声明名・放宽大小写 |  | - |
| `settings/provider.go:540` | packages/settings/settings/src/index.ts | 472-512 | 498-538 | `Describe` | Go 声明名・放宽大小写 |  | - |
| `settings/provider.go:624` | packages/settings/settings/src/index.ts | 552-575 | 589-618 | `Mutate` | Go 声明名・放宽大小写 |  | - |
| `settings/provider.go:661` | packages/settings/settings/src/index.ts | 577-648 | 620-691 | `write` | Go 声明名 |  | - |
| `settings/provider.go:751` | packages/settings/settings/src/index.ts | 650-684 | 693-727 | `Publish` | Go 声明名・放宽大小写 |  | - |
| `settings/provider.go:894` | packages/settings/settings/src/index.ts | 712-723 | 755-766 | `bumpRevision` | Go 声明名 |  | - |
| `settings/provider.go:928` | packages/settings/settings/src/index.ts | 748-799 | 791-842 | `commit` | Go 声明名 |  | - |
| `settings/provider.go:1010` | packages/settings/settings/src/index.ts | 801-805 | 844-848 | `warnWatcherFailure` | Go 声明名 |  | - |
| `settings/provider.go:1023` | packages/settings/settings/src/index.ts | 807-811 | 850-854 | `warnListenerFailure` | Go 声明名 |  | - |
| `skill/registry.go:31` | packages/skill/skill/src/index.ts | 311-315 | 311-316 | `RegisteredProvider` | 注释锚点 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `skill/registry.go:46` | packages/skill/skill/src/index.ts | 301-308 | 302-309 | `indexedCandidate` | Go 声明名・放宽大小写 |  | - |
| `skill/registry.go:64` | packages/skill/skill/src/index.ts | 317-320 | 318-321 | `layerCollectResult` | Go 声明名・放宽大小写 |  | - |
| `skill/registry.go:72` | packages/skill/skill/src/index.ts | 322-325 | 323-326 | `collectResult` | Go 声明名・放宽大小写 |  | - |
| `skill/registry.go:117` | packages/skill/skill/src/index.ts | 341-343 | 341-344 | `IsEmpty` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `skill/registry.go:227` | packages/skill/skill/src/index.ts | 380-429 | 381-430 | `RegisterProvider` | Go 声明名・放宽大小写 |  | - |
| `skill/registry.go:317` | packages/skill/skill/src/index.ts | 431-461 | 432-462 | `Register` | Go 声明名・放宽大小写 |  | - |
| `skill/registry.go:387` | packages/skill/skill/src/index.ts | 475-490 | 476-491 | `Snapshot` | Go 声明名・放宽大小写 |  | - |
| `skill/registry.go:451` | packages/skill/skill/src/index.ts | 520-550 | 521-551 | `collect` | Go 声明名 |  | - |
| `skill/registry.go:488` | packages/skill/skill/src/index.ts | 552-566 | 553-567 | `collectFresh` | Go 声明名 |  | - |
| `skill/registry.go:513` | packages/skill/skill/src/index.ts | 568-583 | 569-584 | `collectLayer` | Go 声明名 |  | - |
| `skill/registry.go:544` | packages/skill/skill/src/index.ts | 585-620 | 586-621 | `listLayerCandidates` | Go 声明名 |  | - |
| `skill/registry.go:657` | packages/skill/skill/src/index.ts | 622-626 | 623-627 | `invalidateCache` | Go 声明名 |  | - |
| `skill/registry.go:674` | packages/skill/skill/src/index.ts | 628-632 | 629-633 | `invalidateEntry` | Go 声明名 |  | - |
| `skill/registry.go:749` | packages/skill/skill/src/index.ts | 692-706 | 693-707 | `runtimeCandidate` | Go 声明名 |  | - |
| `skill/registry.go:762` | packages/skill/skill/src/index.ts | 708-740 | 709-741 | `validateCandidate` | Go 声明名 |  | - |
| `skill/registry.go:784` | packages/skill/skill/src/index.ts | 742-746 | 743-747 | `validateRuntimeSkill` | Go 声明名 |  | - |
| `skill/registry.go:797` | packages/skill/skill/src/index.ts | 748-768 | 749-769 | `validateDefinition` | Go 声明名 |  | - |
| `skill/registry.go:813` | packages/skill/skill/src/index.ts | 807-811 | 808-812 | `compareIndexedCandidates` | Go 声明名 |  | - |
| `skill/skill.go:42` | packages/skill/skill/src/index.ts | 27 | 27-28 | `BUNDLED_SKILL_RANK` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `skill/skill.go:47` | packages/skill/skill/src/index.ts | 34-36 | 30-37 | `isSkillName` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `skill/skill.go:54` | packages/skill/skill/src/index.ts | 39 | 39-40 | `SkillSource` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `skill/skill.go:93` | packages/skill/skill/src/index.ts | 42-45 | 42-46 | `SkillResourceBase` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `skill/skill.go:240` | packages/skill/skill/src/index.ts | 104-109 | 104-110 | `SkillLookupOptions` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `skill/skill.go:250` | packages/skill/skill/src/index.ts | 117-120 | 112-121 | `SkillViewOptions` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `skill/skill.go:260` | packages/skill/skill/src/index.ts | 127-129 | 123-130 | `isModelInvocable` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `skill/skill.go:267` | packages/skill/skill/src/index.ts | 136-138 | 132-139 | `isUserInvocable` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `skill/skill.go:274` | packages/skill/skill/src/index.ts | 147-154 | 141-154 | `SkillInvocationSource` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `skill/skill.go:290` | packages/skill/skill/src/index.ts | 171-184 | 163-185 | `renderSkillContent` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `skill/skill.go:311` | packages/skill/skill/src/index.ts | 186-211 | 187-216 | `renderResourceHint` | Go 声明名 |  | - |
| `skill/skill.go:349` | packages/skill/skill/src/index.ts | 213-215 | 218-220 | `escapeAttr` | Go 声明名 |  | - |
| `skill/skill.go:356` | packages/skill/skill/src/index.ts | 227-229 | 222-230 | `escapeText` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `skill/skill.go:366` | packages/skill/skill/src/index.ts | 232-237 | 232-238 | `SkillCatalogSnapshot` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `skill/skill.go:380` | packages/skill/skill/src/index.ts | 240-245 | 240-246 | `SkillProviderObservation` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `skill/skill.go:395` | packages/skill/skill/src/index.ts | 248-268 | 248-269 | `SkillProvider` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `skill/skill.go:414` | packages/skill/skill/src/index.ts | 271-276 | 271-277 | `SkillProviderControl` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `skill/skilltool/catalog.go:30` | packages/skill/tool-skill/src/index.ts | 34-41 | 28-41 | `SkillCatalogSource` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `skill/skilltool/catalog.go:66` | packages/skill/tool-skill/src/index.ts | 348-359 | 337-359 | `readCatalogEntries` | 注释锚点 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `skill/skilltool/catalog.go:119` | packages/skill/tool-skill/src/index.ts | 391-394 | 390-393 | `catalogDescription` | Go 声明名 |  | - |
| `skill/skilltool/catalog.go:134` | packages/skill/tool-skill/src/index.ts | 328-335 | 323-335 | `digestCatalogEntries` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `skill/skilltool/catalog.go:156` | packages/skill/tool-skill/src/index.ts | 319-321 | 313-321 | `renderCatalogEntries` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `skill/skilltool/config.go:19` | packages/skill/tool-skill/src/invariant.ts | 10 | 12-13 | `name` | 裁决表 |  | - |
| `skill/skilltool/config.go:113` | packages/skill/tool-skill/src/index.ts | 77-79 | 71-252 | `apply` | 裁决表 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `skill/skilltool/tool.go:303` | packages/skill/tool-skill/src/index.ts | 161 | 71-252 | `apply` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `spill/policy/policy.go:86` | packages/spill/spill-policy/src/index.ts | 110-122 | 110-232 | `apply` | 裁决表 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `spill/policy/policy.go:115` | packages/spill/spill-policy/src/index.ts | 190-224 | 110-232 | `apply` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `spill/policy/policy.go:179` | packages/spill/spill-policy/src/index.ts | 125-188 | 123-188 | `spillReplacement` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `spill/policy/policy.go:220` | packages/spill/spill-policy/src/index.ts | 83-90 | 79-87 | `flattenPlainText` | Go 声明名 |  | - |
| `spill/policy/policy.go:238` | packages/spill/spill-policy/src/index.ts | 93-100 | 94-102 | `preview` | Go 声明名 |  | - |
| `spill/policy/policy.go:251` | packages/spill/spill-policy/src/index.ts | 103-106 | 104-108 | `spillNotice` | Go 声明名 |  | - |
| `spill/spill.go:54` | packages/spill/spill/src/types.ts | 18 | 70 | `Locator` | Go 声明名・放宽大小写 |  | - |
| `spill/spill.go:64` | packages/spill/spill/src/types.ts | 37 | 30-39 | `SpillOwner` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `spill/spill.go:76` | packages/spill/spill/src/types.ts | 46 | 41-53 | `SpillSource` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `spill/spill.go:92` | packages/spill/spill/src/types.ts | 56 | 55-66 | `SaveTextSpill` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `spill/spill.go:110` | packages/spill/spill/src/types.ts | 69 | 68-73 | `SpillRef` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `spill/spill.go:128` | packages/spill/spill/src/index.ts | 45-56 | 29-56 | `SpillStore` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `storage/backend.go:68` | packages/storage/storage/src/backend.ts | 12-16 | 18-19 | `KV` | Go 声明名・放宽大小写 |  | - |
| `storage/backend.go:111` | packages/storage/storage/src/backend.ts | 45-55 | 45-64 | `KvUnitDescriptor` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `storage/domain/facility.go:50` | packages/storage/storage-domain/src/invariant.ts | 8 | 18-19 | `name` | 裁决表 |  | - |
| `storage/domain/spec.go:179` | packages/storage/storage-domain/src/spec.ts | 34-44 | 34-52 | `DomainSpec` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `storage/postgres/backend.go:46` | packages/storage/storage-sqlite/src/index.ts | 50-55 | 159 | `Backend` | Go 声明名・放宽大小写 |  | - |
| `storage/registry.go:24` | packages/storage/storage/src/registry.ts | 9-14 | 9-62 | `BackendRegistry` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/controltool/control.go:24` | packages/subagent/tool-subagent-control/src/invariant.ts | 10 | 12-13 | `name` | 裁决表 |  | - |
| `subagent/controltool/control.go:132` | packages/subagent/tool-subagent-control/src/index.ts | 25 | 22-121 | `apply` | 裁决表 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/controltool/control.go:331` | packages/subagent/tool-subagent-control/src/index.ts | 25-120 | 22-121 | `apply` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/controltool/listagents.go:230` | packages/subagent/tool-subagent-control/src/list-agents.ts | 59-63 | 52-63 | `statusOf` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/controltool/listagents.go:247` | packages/subagent/tool-subagent-control/src/list-agents.ts | 66-85 | 65-85 | `project` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/controltool/listagents.go:450` | packages/subagent/tool-subagent-control/src/list-agents.ts | 91-192 | 87-192 | `apply` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/forkinprocess/provider.go:38` | packages/subagent/subagent-fork-in-process/src/index.ts | 48-54 | 40-54 | `completedTurnPrefix` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/forkinprocess/provider.go:84` | packages/subagent/subagent-fork-in-process/src/index.ts | 66 | 99-101 | `apply` | 裁决表 |  | - |
| `subagent/forkinprocess/provider.go:100` | packages/subagent/subagent-fork-in-process/src/index.ts | 62 | 63-69 | `Capabilities` | Go 声明名・放宽大小写 |  | - |
| `subagent/forkinprocess/provider.go:112` | packages/subagent/subagent-fork-in-process/src/index.ts | 64 | 71 | `InheritsParentContext` | Go 声明名・放宽大小写 |  | - |
| `subagent/forkinprocess/provider.go:117` | packages/subagent/subagent-fork-in-process/src/index.ts | 68-75 | 75-82 | `Start` | Go 声明名・放宽大小写 |  | - |
| `subagent/forkinprocess/provider.go:130` | packages/subagent/subagent-fork-in-process/src/index.ts | 83-89 | 90-96 | `PrepareContinuable` | Go 声明名・放宽大小写 |  | - |
| `subagent/inprocessdriver/driver.go:57` | packages/subagent/subagent-in-process-driver/src/index.ts | 100-142 | 92-149 | `startInProcessRun` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/inprocessdriver/driver.go:178` | packages/subagent/subagent-in-process-driver/src/index.ts | 78-88 | 79-90 | `attachDescriptorAppend` | Go 声明名 |  | - |
| `subagent/inprocessdriver/driver.go:228` | packages/subagent/subagent-in-process-driver/src/index.ts | 47-64 | 48-66 | `toStopReason` | Go 声明名 |  | - |
| `subagent/inprocessdriver/driver.go:295` | packages/subagent/subagent-in-process-driver/src/index.ts | 148-190 | 151-206 | `drivePublishedRun` | Go 声明名 |  | - |
| `subagent/inprocessdriver/driver.go:384` | packages/subagent/subagent-in-process-driver/src/index.ts | 186 | 175-190 | `Result` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/inprocessdriver/driver.go:397` | packages/subagent/subagent-in-process-driver/src/index.ts | 187-194 | 196-205 | `Dispose` | Go 声明名・放宽大小写 |  | - |
| `subagent/inprocessdriver/driver.go:415` | packages/subagent/subagent-in-process-driver/src/index.ts | 197-232 | 208-234 | `readResult` | Go 声明名 |  | - |
| `subagent/inprocessdriver/error.go:26` | packages/subagent/subagent-in-process-driver/src/index.ts | 73-76 | 74-77 | `prePublicationAbort` | Go 声明名 |  | - |
| `subagent/inprocessdriver/structured.go:111` | packages/subagent/subagent-in-process-driver/src/structured.ts | 33-38 | 62 | `Captured` | Go 声明名・放宽大小写 |  | - |
| `subagent/inprocessdriver/structured.go:198` | packages/subagent/subagent-in-process-driver/src/structured.ts | 49-142 | 41-142 | `attachStructuredRuntime` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/reporttool/tool.go:24` | packages/subagent/tool-subagent-report/src/invariant.ts | 10 | 12-13 | `name` | 裁决表 |  | - |
| `subagent/reporttool/tool.go:233` | packages/subagent/tool-subagent-report/src/index.ts | 94-103 | 92-102 | `execute` | Go 声明名 |  | - |
| `subagent/spawninprocess/provider.go:60` | packages/subagent/subagent-spawn-in-process/src/index.ts | 46 | 68-70 | `apply` | 裁决表 |  | - |
| `subagent/spawninprocess/provider.go:80` | packages/subagent/subagent-spawn-in-process/src/index.ts | 42 | 42-48 | `Capabilities` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/spawninprocess/provider.go:92` | packages/subagent/subagent-spawn-in-process/src/index.ts | 44 | 50 | `InheritsParentContext` | Go 声明名・放宽大小写 |  | - |
| `subagent/spawninprocess/provider.go:97` | packages/subagent/subagent-spawn-in-process/src/index.ts | 48-53 | 54-59 | `Start` | Go 声明名・放宽大小写 |  | - |
| `subagent/spawninprocess/provider.go:107` | packages/subagent/subagent-spawn-in-process/src/index.ts | 55-59 | 61-65 | `PrepareContinuable` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagent/activationsetup.go:25` | packages/subagent/subagent/src/activation-setup-registry.ts | 26 | 19-26 | `ContinuableSetupContribution` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/activationsetup.go:70` | packages/subagent/subagent/src/activation-setup-registry.ts | 60-183 | 56-183 | `SubagentActivationSetupRegistry` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/activationsetup.go:101` | packages/subagent/subagent/src/activation-setup-registry.ts | 72-83 | 66-83 | `Register` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/activationsetup.go:146` | packages/subagent/subagent/src/activation-setup-registry.ts | 90-139 | 85-139 | `Apply` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/activationsetup.go:208` | packages/subagent/subagent/src/activation-setup-registry.ts | 52-54 | 51-54 | `isRemoved` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/activationsetup.go:239` | packages/subagent/subagent/src/activation-setup-registry.ts | 142-145 | 141-145 | `releaseChild` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/activationsetup.go:253` | packages/subagent/subagent/src/activation-setup-registry.ts | 152-167 | 147-167 | `releaseAll` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/assistantoutput.go:24` | packages/subagent/subagent/src/assistant-output.ts | 22-58 | 16-59 | `AssistantOutputFold` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/assistantoutput.go:37` | packages/subagent/subagent/src/assistant-output.ts | 33-40 | 26-39 | `Push` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagent/assistantoutput.go:78` | packages/subagent/subagent/src/assistant-output.ts | 46-48 | 41-47 | `PushText` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagent/assistantoutput.go:88` | packages/subagent/subagent/src/assistant-output.ts | 55-59 | 49-58 | `Collect` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagent/assistantoutput.go:102` | packages/subagent/subagent/src/assistant-output.ts | 66-72 | 61-74 | `finalAssistantOutput` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/childagent.go:29` | packages/subagent/subagent/src/child-agent.ts | 31-36 | 31-37 | `SubagentDepthError` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/childagent.go:50` | packages/subagent/subagent/src/child-agent.ts | 48-57 | 39-58 | `resolveChildDepth` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/childlock.go:16` | packages/subagent/subagent/src/continuation.ts | 326-347 | 326-348 | `childLock` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/continuation.go:128` | packages/subagent/subagent/src/continuation.ts | 156-165 | 158-166 | `activationState` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagent/continuation.go:143` | packages/subagent/subagent/src/continuation.ts | 167-188 | 168-191 | `continuationHost` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagent/continuation.go:243` | packages/subagent/subagent/src/continuation.ts | 264-269 | 1096-1099 | `materialization` | Go 声明名 |  | - |
| `subagent/subagent/continuation.go:258` | packages/subagent/subagent/src/continuation.ts | 290-318 | 291-319 | `settlementSummary` | Go 声明名 |  | - |
| `subagent/subagent/continuationactivation.go:37` | packages/subagent/subagent/src/continuation.ts | 254-262 | 249-268 | `materializeInputs` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/continuationactivation.go:57` | packages/subagent/subagent/src/continuation.ts | 1532-1541 | 1595-1605 | `requirePersistence` | Go 声明名 |  | - |
| `subagent/subagent/continuationactivation.go:73` | packages/subagent/subagent/src/continuation.ts | 945-994 | 955-1018 | `coldResume` | Go 声明名 |  | - |
| `subagent/subagent/continuationactivation.go:157` | packages/subagent/subagent/src/continuation.ts | 1005-1020 | 1020-1053 | `submitMaterialized` | Go 声明名 |  | - |
| `subagent/subagent/continuationactivation.go:180` | packages/subagent/subagent/src/continuation.ts | 1028-1041 | 1086-1105 | `materialize` | Go 声明名 |  | - |
| `subagent/subagent/continuationactivation.go:206` | packages/subagent/subagent/src/continuation.ts | 1048-1138 | 1107-1202 | `materializeTracked` | Go 声明名 |  | - |
| `subagent/subagent/continuationactivation.go:389` | packages/subagent/subagent/src/continuation.ts | 1145-1154 | 1204-1218 | `rollbackUnpublished` | Go 声明名 |  | - |
| `subagent/subagent/continuationactivation.go:416` | packages/subagent/subagent/src/continuation.ts | 1162-1172 | 1220-1236 | `acquireOwnership` | Go 声明名 |  | - |
| `subagent/subagent/continuationactivation.go:436` | packages/subagent/subagent/src/continuation.ts | 1175-1179 | 1238-1243 | `releaseOwnership` | Go 声明名 |  | - |
| `subagent/subagent/continuationactivation.go:455` | packages/subagent/subagent/src/continuation.ts | 1182-1185 | 1245-1249 | `wake` | Go 声明名 |  | - |
| `subagent/subagent/continuationactivation.go:467` | packages/subagent/subagent/src/continuation.ts | 1192-1209 | 1251-1273 | `submit` | Go 声明名 |  | - |
| `subagent/subagent/continuationactivation.go:491` | packages/subagent/subagent/src/continuation.ts | 1218-1236 | 1275-1300 | `admitWaking` | Go 声明名 |  | - |
| `subagent/subagent/continuationactivation.go:511` | packages/subagent/subagent/src/continuation.ts | 1243-1266 | 1302-1330 | `submitAdmitted` | Go 声明名 |  | - |
| `subagent/subagent/continuationactivation.go:547` | packages/subagent/subagent/src/continuation.ts | 1273-1287 | 1332-1351 | `authorizeLineage` | Go 声明名 |  | - |
| `subagent/subagent/continuationactivation.go:572` | packages/subagent/subagent/src/continuation.ts | 1295-1330 | 1353-1394 | `watchSettlement` | Go 声明名 |  | - |
| `subagent/subagent/continuationactivation.go:662` | packages/subagent/subagent/src/continuation.ts | 1343-1352 | 1396-1416 | `dispose` | Go 声明名 |  | - |
| `subagent/subagent/continuationactivation.go:695` | packages/subagent/subagent/src/continuation.ts | 1359-1442 | 1418-1506 | `finishDisposal` | Go 声明名 |  | - |
| `subagent/subagent/continuationactivation.go:780` | packages/subagent/subagent/src/continuation.ts | 1462-1511 | 1508-1575 | `notifySettlement` | Go 声明名 |  | - |
| `subagent/subagent/continuationactivation.go:841` | packages/subagent/subagent/src/continuation.ts | 1519-1529 | 1577-1593 | `flushFinalState` | Go 声明名 |  | - |
| `subagent/subagent/continuationdrain.go:20` | packages/subagent/subagent/src/continuation.ts | 720-743 | 738-761 | `Drain` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagent/continuationdrain.go:62` | packages/subagent/subagent/src/continuation.ts | 745-806 | 763-823 | `DrainDescendants` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagent/continuationdrain.go:158` | packages/subagent/subagent/src/continuation.ts | 808-842 | 825-859 | `DrainChildren` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagent/continuationdrain.go:207` | packages/subagent/subagent/src/continuation.ts | 844-865 | 861-882 | `disposeRoots` | Go 声明名 |  | - |
| `subagent/subagent/continuationdrain.go:274` | packages/subagent/subagent/src/continuation.ts | 876-893 | 893-910 | `liveLineage` | Go 声明名 |  | - |
| `subagent/subagent/continuationdrain.go:337` | packages/subagent/subagent/src/continuation.ts | 911-921 | 928-938 | `assertAdmitting` | Go 声明名 |  | - |
| `subagent/subagent/continuationops.go:24` | packages/subagent/subagent/src/continuation.ts | 394-476 | 395-480 | `StartContinuable` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagent/continuationops.go:200` | packages/subagent/subagent/src/continuation.ts | 491-531 | 489-548 | `Followup` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/continuationops.go:279` | packages/subagent/subagent/src/continuation.ts | 533-594 | 550-611 | `Interrupt` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagent/continuationops.go:348` | packages/subagent/subagent/src/continuation.ts | 596-619 | 626-636 | `ReportFrom` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagent/continuationops.go:377` | packages/subagent/subagent/src/continuation.ts | 622-639 | 638-656 | `authorizeReporter` | Go 声明名 |  | - |
| `subagent/subagent/continuationops.go:403` | packages/subagent/subagent/src/continuation.ts | 642-653 | 658-670 | `resolveReportParent` | Go 声明名 |  | - |
| `subagent/subagent/continuationops.go:415` | packages/subagent/subagent/src/continuation.ts | 656-679 | 672-696 | `deliverReport` | Go 声明名 |  | - |
| `subagent/subagent/continuationops.go:449` | packages/subagent/subagent/src/continuation.ts | 682-701 | 698-718 | `sendWaking` | Go 声明名 |  | - |
| `subagent/subagent/continuationops.go:465` | packages/subagent/subagent/src/continuation.ts | 704-719 | 720-736 | `sendReport` | Go 声明名 |  | - |
| `subagent/subagent/depth.go:17` | packages/subagent/subagent/src/depth.ts | 26-36 | 18-36 | `delegationDepthOf` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/depth.go:34` | packages/subagent/subagent/src/depth.ts | 42-51 | 38-51 | `assertSubagentMaxDepth` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/descriptorseed.go:20` | packages/subagent/subagent/src/descriptor-seed.ts | 22-30 | 13-31 | `seedDescriptorTurn` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/invariant.go:18` | packages/subagent/subagent/src/invariant.ts | 7 | 9-10 | `name` | 裁决表 |  | - |
| `subagent/subagent/lifecycle.go:260` | packages/subagent/subagent/src/lifecycle.ts | 133-160 | 125-162 | `observeRun` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/lifecycle.go:299` | packages/subagent/subagent/src/lifecycle.ts | 30-35 | 27-36 | `ActivationTerminal` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/lifecycle.go:310` | packages/subagent/subagent/src/lifecycle.ts | 42-73 | 38-74 | `ActivationObserver` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/lifecycle.go:329` | packages/subagent/subagent/src/lifecycle.ts | 172-177 | 164-217 | `createActivationObserver` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/lifecycle.go:370` | packages/subagent/subagent/src/lifecycle.ts | 203-208 | 191-193 | `terminal` | Go 声明名 |  | - |
| `subagent/subagent/lifecycle.go:405` | packages/subagent/subagent/src/lifecycle.ts | 236-263 | 219-260 | `epochStopReason` | Go 声明名 |  | - |
| `subagent/subagent/listchildren.go:147` | packages/subagent/subagent/src/list-children.ts | 101 | 49 | `corpusRecord` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagent/listchildren.go:164` | packages/subagent/subagent/src/list-children.ts | 111-115 | 59-63 | `positionedCandidate` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagent/listchildren.go:259` | packages/subagent/subagent/src/list-children.ts | 184-241 | 130-192 | `prepareListing` | Go 声明名 |  | - |
| `subagent/subagent/listchildren.go:307` | packages/subagent/subagent/src/list-children.ts | 243-296 | 194-244 | `resolveCandidateRows` | Go 声明名 |  | - |
| `subagent/subagent/listchildren.go:367` | packages/subagent/subagent/src/list-children.ts | 298-332 | 246-280 | `descendantCandidates` | Go 声明名 |  | - |
| `subagent/subagent/listchildren.go:418` | packages/subagent/subagent/src/list-children.ts | 334-336 | 282-285 | `compareCorpusRecords` | Go 声明名 |  | - |
| `subagent/subagent/outofprocess.go:83` | packages/subagent/subagent/src/out-of-process.ts | 75-86 | 78-94 | `isEnterableDirectory` | Go 声明名 |  | - |
| `subagent/subagent/outofprocess.go:109` | packages/subagent/subagent/src/out-of-process.ts | 98-106 | 96-114 | `assertUsableCwd` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/outofprocess.go:124` | packages/subagent/subagent/src/out-of-process.ts | 118-124 | 116-132 | `validateConfiguredCwd` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/outofprocess.go:149` | packages/subagent/subagent/src/out-of-process.ts | 139-145 | 134-153 | `resolveChildCwd` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/outofprocess.go:190` | packages/subagent/subagent/src/out-of-process.ts | 183-210 | 182-219 | `settleRunResult` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/outofprocess.go:269` | packages/subagent/subagent/src/out-of-process.ts | 240 | 249-258 | `LocalAgent` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagent/projection.go:50` | packages/subagent/subagent/src/projection.ts | 113-116 | 108-111 | `identityState` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagent/runsettlement.go:20` | packages/subagent/subagent/src/run-settlement.ts | 14-19 | 13-19 | `finalText` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/runsettlement.go:36` | packages/subagent/subagent/src/run-settlement.ts | 22-27 | 21-27 | `failureDetail` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/runsettlement.go:47` | packages/subagent/subagent/src/run-settlement.ts | 34-49 | 29-53 | `runOutcome` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/runsettlement.go:65` | packages/subagent/subagent/src/run-settlement.ts | 57-70 | 55-75 | `settleRun` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/runtime.go:185` | packages/subagent/subagent/src/index.ts | 388-407 | 513-536 | `RegisterProvider` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagent/runtime.go:270` | packages/subagent/subagent/src/index.ts | 409-416 | 538-545 | `GetProvider` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagent/runtime.go:280` | packages/subagent/subagent/src/index.ts | 418-424 | 547-553 | `List` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagent/runtime.go:291` | packages/subagent/subagent/src/index.ts | 487-493 | 599-606 | `expectProvider` | Go 声明名 |  | - |
| `subagent/subagent/runtime.go:304` | packages/subagent/subagent/src/index.ts | 426-445 | 555-577 | `Start` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagent/runtime.go:346` | packages/subagent/subagent/src/index.ts | 508-513 | 631-648 | `assertCapabilities` | Go 声明名 |  | - |
| `subagent/subagent/runtime.go:381` | packages/subagent/subagent/src/index.ts | 495-504 | 608-617 | `requireContinuations` | Go 声明名 |  | - |
| `subagent/subagent/runtime.go:394` | packages/subagent/subagent/src/index.ts | 210-212 | 229-240 | `StartContinuable` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagent/runtime.go:408` | packages/subagent/subagent/src/index.ts | 214-236 | 242-264 | `Followup` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagent/runtime.go:430` | packages/subagent/subagent/src/index.ts | 238-256 | 266-283 | `Interrupt` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagent/runtime.go:445` | packages/subagent/subagent/src/index.ts | 258-273 | 285-302 | `ReportFrom` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagent/runtime.go:465` | packages/subagent/subagent/src/index.ts | 275-289 | 304-318 | `RegisterContinuableSetup` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagent/runtime.go:477` | packages/subagent/subagent/src/index.ts | 291-304 | 320-335 | `DrainContinuableDescendants` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagent/runtime.go:490` | packages/subagent/subagent/src/index.ts | 306-318 | 337-351 | `DrainContinuableChildren` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagent/runtime.go:510` | packages/subagent/subagent/src/index.ts | 320-359 | 353-372 | `listChildren` | 裁决表 |  | - |
| `subagent/subagent/runtime.go:519` | packages/subagent/subagent/src/index.ts | 361-378 | 374-391 | `listDescendants` | 裁决表 |  | - |
| `subagent/subagent/types.go:32` | packages/subagent/subagent/src/types.ts | 36-50 | 31-50 | `SubagentRunInfo` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/types.go:52` | packages/subagent/subagent/src/types.ts | 56-73 | 52-73 | `SubagentRunEndInfo` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/types.go:71` | packages/subagent/subagent/src/types.ts | 86-91 | 75-92 | `SubagentCapabilities` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/types.go:93` | packages/subagent/subagent/src/types.ts | 100-149 | 94-157 | `SubagentStartRequest` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/types.go:213` | packages/subagent/subagent/src/types.ts | 256-282 | 255-290 | `SubagentRun` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagent/types.go:244` | packages/subagent/subagent/src/types.ts | 292-331 | 292-346 | `SubagentProvider` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagenttool/config.go:23` | packages/subagent/tool-subagent/src/invariant.ts | 10 | 13-14 | `name` | 裁决表 |  | - |
| `subagent/subagenttool/config.go:200` | packages/subagent/tool-subagent/src/index.ts | 276-286 | 307-697 | `apply` | 裁决表 |  | - |
| `subagent/subagenttool/settlement.go:32` | packages/subagent/tool-subagent/src/index.ts | 125-142 | 155-173 | `stopReasonError` | Go 声明名 |  | - |
| `subagent/subagenttool/settlement.go:69` | packages/subagent/tool-subagent/src/index.ts | 152-164 | 175-195 | `withDiagnosticAndPartialText` | Go 声明名 |  | - |
| `subagent/subagenttool/settlement.go:87` | packages/subagent/tool-subagent/src/index.ts | 176-206 | 203-237 | `settleForegroundRun` | Go 声明名 |  | - |
| `subagent/subagenttool/settlement.go:131` | packages/subagent/tool-subagent/src/index.ts | 112-122 | 142-153 | `settleStart` | Go 声明名 |  | - |
| `subagent/subagenttool/tool.go:38` | packages/subagent/tool-subagent/src/index.ts | 276-476 | 543 | `Controller` | Go 声明名・放宽大小写 |  | - |
| `subagent/subagenttool/tool.go:112` | packages/subagent/tool-subagent/src/index.ts | 101-109 | 132-140 | `outputValueText` | Go 声明名 |  | - |
| `subagent/subagenttool/tool.go:444` | packages/subagent/tool-subagent/src/index.ts | 290-441 | 360-565 | `mount` | Go 声明名 |  | - |
| `subagent/subagenttool/tool.go:551` | packages/subagent/tool-subagent/src/index.ts | 443-475 | 307-697 | `apply` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `subagent/subagenttool/wording.go:12` | packages/subagent/tool-subagent/src/index.ts | 220 | 362 | `wording` | Go 声明名 |  | - |
| `subagent/subagenttool/wording.go:71` | packages/subagent/tool-subagent/src/index.ts | 220-245 | 239-276 | `providerWording` | Go 声明名 |  | - |
| `todo/invariant.go:21` | packages/todo/tool-todo/src/invariant.ts | 7 | 10-11 | `name` | 裁决表 |  | - |
| `todo/invariant.go:27` | packages/todo/tool-todo/src/invariant.ts | 24-45 | 53-58 | `ValidateEvent` | Go 声明名・放宽大小写 |  | - |
| `todo/invariant.go:55` | packages/todo/tool-todo/src/invariant.ts | 24-39 | 15-39 | `validateTodos` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `todo/tool.go:28` | packages/todo/tool-todo/src/index.ts | 26 | 25-26 | `statuses` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `todo/tool.go:103` | packages/todo/tool-todo/src/index.ts | 146-222 | 122-223 | `apply` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `todo/tool.go:113` | packages/todo/tool-todo/src/index.ts | 74-78 | 68-78 | `describe` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `todo/tool.go:149` | packages/todo/tool-todo/src/index.ts | 91-111 | 80-111 | `toTodoList` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `todo/tool.go:263` | packages/todo/tool-todo/src/index.ts | 201-204 | 198-222 | `render` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `todo/tool.go:276` | packages/todo/tool-todo/src/index.ts | 221 | 221-222 | `presentCall` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `todo/tool.go:293` | packages/todo/tool-todo/src/index.ts | 206-223 | 203-222 | `execute` | Go 声明名 |  | - |
| `util/timeout/timeout.go:48` | packages/util/timeout/src/index.ts | 12-22 | 8-22 | `TimeoutReason` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `util/timeout/timeout.go:79` | packages/util/timeout/src/index.ts | 45-55 | 33-55 | `clampTimeout` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `util/timeout/timeout.go:104` | packages/util/timeout/src/index.ts | 91-113 | 81-113 | `deadline` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `util/timeout/timeout.go:134` | packages/util/timeout/src/index.ts | 184-190 | 175-190 | `timeoutOf` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `util/timeout/timeout.go:185` | packages/util/timeout/src/index.ts | 126-131 | 115-173 | `idleWatchdog` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `workflow/toolralph/config.go:157` | packages/workflow/tool-ralph/src/index.ts | 186-205 | 402-477 | `apply` | 裁决表 |  | - |
| `workflow/toolralph/loop.go:79` | packages/workflow/tool-ralph/src/index.ts | 386-392 | 460 | `Error` | Go 声明名・放宽大小写 |  | - |
| `workflow/toolralph/loop.go:268` | packages/subagent/tool-subagent/src/index.ts | 125-142 | 155-173 | `stopReasonError` | Go 声明名 |  | - |
| `workflow/toolralph/render.go:20` | packages/workflow/tool-ralph/src/index.ts | 354-358 | 351-356 | `boundResult` | Go 声明名 |  | - |
| `workflow/toolralph/render.go:53` | packages/workflow/tool-ralph/src/index.ts | 362 | 360 | `rounds` | Go 声明名 |  | - |
| `workflow/toolralph/render.go:63` | packages/workflow/tool-ralph/src/index.ts | 361-376 | 358-374 | `renderResult` | Go 声明名 |  | - |
| `workflow/toolralph/render.go:92` | packages/workflow/tool-ralph/src/index.ts | 386-392 | 383-390 | `renderRoundFailure` | Go 声明名 |  | - |
| `workflow/toolralph/report.go:59` | packages/workflow/tool-ralph/src/index.ts | 91-102 | 89-100 | `reportSchema` | Go 声明名 |  | - |
| `workflow/toolralph/report.go:142` | packages/workflow/tool-ralph/src/index.ts | 112-149 | 244-278 | `readReport` | Go 声明名 |  | - |
| `workflow/toolralph/report.go:213` | packages/workflow/tool-ralph/src/index.ts | 116-143 | 110-147 | `validateReport` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `workflow/toolralph/tool.go:85` | packages/workflow/tool-ralph/src/index.ts | 208-217 | 205-215 | `resolveMaxRounds` | Go 声明名 |  | - |
| `workflow/toolralph/tool.go:106` | packages/workflow/tool-ralph/src/index.ts | 220-232 | 217-230 | `requireFreshProvider` | Go 声明名 |  | - |
| `workflow/toolralph/tool.go:197` | packages/workflow/tool-ralph/src/index.ts | 437-475 | 435-476 | `execute` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `workflow/toolralph/tool.go:291` | packages/workflow/tool-ralph/src/index.ts | 405-411 | 402-477 | `apply` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `workspace/entity.go:21` | packages/workspace/workspace/src/entity.ts | 34-63 | 29-63 | `WorkspaceEntityHost` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `workspace/error.go:54` | packages/workspace/workspace/src/index.ts | 45-53 | 41-53 | `WorkspaceUnknownSessionError` | 注释锚点 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `workspace/invariant.go:17` | packages/workspace/workspace/src/invariant.ts | 11 | 13-14 | `name` | 裁决表 |  | - |
| `workspace/registry.go:322` | packages/workspace/workspace/src/index.ts | 171-173 | 166-173 | `Get` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `workspace/registry.go:335` | packages/workspace/workspace/src/index.ts | 181-189 | 175-189 | `List` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `workspace/registry.go:361` | packages/workspace/workspace/src/index.ts | 199-201 | 191-201 | `Delete` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `workspace/registry.go:379` | packages/workspace/workspace/src/index.ts | 210-225 | 203-225 | `InsertBefore` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `workspace/registry.go:448` | packages/workspace/workspace/src/index.ts | 244-255 | 237-255 | `ArchiveSession` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `workspace/registry.go:476` | packages/workspace/workspace/src/index.ts | 277-283 | 270-283 | `ResolveByPath` | Go 声明名・放宽大小写 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `workspace/registry.go:654` | packages/workspace/workspace/src/index.ts | 408-424 | 403-424 | `recoverPendingMutation` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `workspace/registry.go:1143` | packages/workspace/workspace/src/index.ts | 263-268 | 257-268 | `sessionKnown` | Go 声明名 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `workspace/spec.go:70` | packages/workspace/workspace/src/spec.ts | 37-40 | 56-57 | `PendingMutation` | Go 声明名・放宽大小写 |  | - |
| `workspace/spec.go:124` | packages/workspace/workspace/src/spec.ts | 21-27 | 17-28 | `workspaceRecord` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `workspace/spec.go:174` | packages/workspace/workspace/src/spec.ts | 51-56 | 43-57 | `workspaceDomainState` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `workspace/spec.go:197` | packages/workspace/workspace/src/spec.ts | 67-75 | 62-76 | `workspaceDomainSpec` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `workspace/types.go:18` | packages/workspace/workspace/src/types.ts | 15 | 12-16 | `WorkspaceId` | 裁决表+路径一致 |  | 引的范围落在算出的范围之内，多半引的是该符号内部的一小段 |
| `workspace/types.go:29` | packages/workspace/workspace/src/types.ts | 103 | 105-111 | `Status` | Go 声明名・放宽大小写 |  | - |

## MOVED（逐条）

锚点在注释引的那个文件里没有，但在裁决表记的那个文件里唯一命中——上游搬了文件。这一档 `-fix` 会把路径和行号一起改；「上游文件」那一列写的是**改成**哪个。

共 3 条。

| Go 位置 | 上游文件 | 引的范围 | 算出的范围 | 锚点符号 | 锚点来路 | 可改 | 备注 |
|---|---|---:|---:|---|---|:-:|---|
| `core/tools/pipeline.go:208` | packages/interaction/user-approval/src/types.ts | 1706-1727 | 28-32 | `ApprovalOutcome` | 裁决表 | ✓ | 锚点在裁决表记的 packages/interaction/user-approval/src/types.ts 里找着了，上游搬了文件 |
| `core/tools/pipeline.go:224` | packages/interaction/user-approval/src/index.ts | 1700-1706 | 114-139 | `ApprovalRequest` | 裁决表 | ✓ | 锚点在裁决表记的 packages/interaction/user-approval/src/index.ts 里找着了，上游搬了文件 |
| `plan/planmode/projection.go:45` | packages/plan/plan-mode/src/types.ts | 146-152 | 26-36 | `PlanUnitState` | 裁决表 | ✓ | 锚点在裁决表记的 packages/plan/plan-mode/src/types.ts 里找着了，上游搬了文件 |

## NOT_FOUND（逐条）

锚点符号在引的那个上游文件里找不到。锚点来路是「Go 声明名」的那些**不说明任何问题**——Go 侧的构造器、哨兵错误、测试函数名上游本来就没有对应物。

共 1765 条。

| Go 位置 | 上游文件 | 引的范围 | 算出的范围 | 锚点符号 | 锚点来路 | 可改 | 备注 |
|---|---|---:|---:|---|---|:-:|---|
| `acp/acp/bridge.go:59` | packages/acp/acp/src/index.ts | 93-113 | — | `inflightPrompt` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `acp/acp/bridge.go:113` | packages/acp/acp/src/index.ts | 86-114 | — | `sessionRecord` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `acp/acp/bridge.go:217` | packages/acp/acp/src/index.ts | 222 | — | `subscribe` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `acp/acp/bridge.go:267` | packages/acp/acp/src/index.ts | 132-135 | — | `ownedRecordLocked` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `acp/acp/bridge.go:299` | packages/acp/acp/src/index.ts | 222-252 | — | `toolCallUpdate` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `acp/acp/bridge.go:354` | packages/acp/acp/src/index.ts | 227-244 | — | `scheduleDeliveryLocked` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `acp/acp/bridge.go:379` | packages/acp/acp/src/index.ts | 230-244 | — | `deliver` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `acp/acp/bridge.go:415` | packages/acp/acp/src/index.ts | 254-258 | — | `onInboxClaimed` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `acp/acp/bridge.go:433` | packages/acp/acp/src/index.ts | 260-266 | — | `onAgentError` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `acp/acp/bridge.go:459` | packages/acp/acp/src/index.ts | 271-285 | — | `answerApproval` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `acp/acp/bridge.go:509` | packages/acp/acp/src/index.ts | 169-216 | — | `settleAfterQuiescenceLocked` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `acp/acp/bridge.go:522` | packages/acp/acp/src/index.ts | 175-215 | — | `settle` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `acp/acp/bridge.go:576` | packages/acp/acp/src/index.ts | 201-207 | — | `promptStopReason` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `acp/acp/bridge.go:593` | packages/acp/acp/src/index.ts | 210-214 | — | `clearAndFinish` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `acp/acp/bridge.go:690` | packages/acp/acp/src/index.ts | 514-524 | — | `validateSessionParams` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `acp/acp/bridge.go:762` | packages/acp/acp/src/index.ts | 366-401 | — | `admit` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `acp/acp/bridge.go:810` | packages/acp/acp/src/index.ts | 369 | — | `isLive` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `acp/acp/bridge.go:818` | packages/acp/acp/src/index.ts | 409-417 | — | `mapAdmissionError` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `acp/acp/bridge.go:915` | packages/acp/acp/src/index.ts | 453-508 | — | `performQuiesce` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `acp/acp/config.go:54` | packages/acp/acp/src/index.ts | 150 | — | `JsonRpcTransportPeer` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `acp/acp/config.go:69` | packages/acp/acp/src/index.ts | 52-58 | — | `ContinuableDrain` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `acp/acp/config.go:82` | packages/acp/acp/src/index.ts | 271 | — | `ApprovalRegistrar` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `acp/acp/content.go:26` | packages/acp/acp/src/content.ts | 11-16 | — | `imageMediaTypes` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `acp/acp/content.go:79` | packages/acp/acp/src/content.ts | 67 | — | `ModelResolver` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `acp/acp/content.go:133` | packages/acp/acp/src/content.ts | 64-66 | — | `routeOf` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `attachment/attachment.go:109` | packages/attachment/attachment/src/index.ts | 110-129 | — | `RequestImageProjector` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `attachment/attachment_test.go:25` | packages/attachment/attachment/tests/index.spec.ts | 16-23 | — | `testLimits` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `attachment/attachment_test.go:116` | packages/attachment/attachment/tests/index.spec.ts | 55-72 | — | `projectingStore` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `attachment/attachment_test.go:176` | packages/attachment/attachment/tests/index.spec.ts | 96-103 | — | `TestSaveImagesValidatesEveryMemberBeforeCommittingAny` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `attachment/attachment_test.go:200` | packages/attachment/attachment/tests/index.spec.ts | 105-117 | — | `TestSaveImagesRejectsBatchRulesBeforeTouchingTheStore` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `attachment/attachment_test.go:254` | packages/attachment/attachment/tests/index.spec.ts | 119-126 | — | `TestSaveImagesStartsNoWritesWhenAnyMemberFailsValidation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `attachment/attachment_test.go:278` | packages/attachment/attachment/tests/index.spec.ts | 128-135 | — | `TestSaveImagesReturnsNoPartialReferences` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `attachment/attachment_test.go:341` | packages/attachment/attachment/tests/index.spec.ts | 138-149 | — | `TestReadImageRequestReportsUnsupportedProjection` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `attachment/attachment_test.go:373` | packages/attachment/attachment/tests/index.spec.ts | 55-72 | — | `TestReadImageRequestUsesTheProjectorWhenPresent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `attachment/attachment_test.go:409` | packages/attachment/attachment/tests/admission.spec.ts | 24-35 | — | `TestAdmitEncodedImagesDecodesEveryMemberThenDelegatesOneOrderedBatch` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `attachment/attachment_test.go:456` | packages/attachment/attachment/tests/admission.spec.ts | 37-43 | — | `TestAdmitEncodedImagesCarriesAnAbsentNameThrough` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `attachment/attachment_test.go:481` | packages/attachment/attachment/tests/admission.spec.ts | 45-49 | — | `TestAdmitEncodedImagesDelegatesAnEmptyBatch` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `attachment/attachment_test.go:500` | packages/attachment/attachment/tests/admission.spec.ts | 51-58 | — | `TestAdmitEncodedImagesRejectsNonCanonicalBase64BeforeAnyStoreCall` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `attachment/attachment_test.go:576` | packages/attachment/attachment/tests/admission.spec.ts | 60-65 | — | `TestAdmitEncodedImagesPropagatesTheStoreRejectionUnchanged` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `attachment/attachment_test.go:596` | packages/attachment/attachment/tests/admission.spec.ts | 62 | — | `foreignCodedError` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `attachment/attachment_test.go:597` | packages/attachment/attachment/tests/index.spec.ts | 156 | — | `foreignCodedError` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `attachment/attachment_test.go:611` | packages/attachment/attachment/tests/index.spec.ts | 151-161 | — | `TestIsImageAdmissionErrorSeparatesCallerFixableFromStorageFaults` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `attachment/error.go:73` | packages/attachment/attachment/src/error.ts | 28-29 | — | `ErrorCoder` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `attachment/types.go:53` | packages/attachment/attachment/src/types.ts | 28-31 | — | `Dimensions` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `cmd/llmmockserver/main.go:39` | packages/test-support/llm-mock-server/src/bin.ts | 21-25 | — | `unavailableRecord` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `cmd/llmmockserver/main.go:48` | packages/test-support/llm-mock-server/src/bin.ts | 32-36 | — | `readyRecord` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `cmd/llmmockserver/main.go:59` | packages/test-support/llm-mock-server/src/bin.ts | 12-49 | — | `runScheduleTransaction` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `cmd/llmmockserver/main.go:126` | packages/test-support/llm-mock-server/src/bin.ts | 43-44 | — | `exitCode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `cmd/llmmockserver/main_test.go:111` | packages/test-support/llm-mock-server/src/bin.ts | 14-17 | — | `TestRunPrintsUsageForHelp` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `cmd/llmmockserver/main_test.go:131` | packages/test-support/llm-mock-server/src/bin.ts | 18-20 | — | `TestRunRejectsBadArguments` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `cmd/llmmockserver/main_test.go:193` | packages/test-support/llm-mock-server/src/bin.ts | 32-44 | — | `TestRunAnnouncesReadyThenExitsOnSignal` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `cmd/llmmockserver/main_test.go:228` | packages/test-support/llm-mock-server/src/bin.ts | 38 | — | `TestRunForwardsTelemetryAsJSONL` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `cmd/llmmockserver/main_test.go:276` | packages/test-support/llm-mock-server/src/bin.ts | 21-31 | — | `TestRunAnnouncesUnavailableBeforeBinding` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `cmd/llmmockserver/main_test.go:354` | packages/test-support/llm-mock-server/src/bin.ts | 43-44 | — | `TestExitCodeFollowsTheShellConvention` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/config.go:424` | packages/compaction/compaction-basic/src/config.ts | 255-275 | — | `validateSummarization` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/config.go:440` | packages/compaction/compaction-basic/src/config.ts | 306-310 | — | `validateRatio` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/engine.go:20` | packages/compaction/compaction-basic/src/index.ts | 266-267 | — | `tokenMeter` | 注释锚点 |  | 锚点符号在该上游文件里找不到 |
| `compaction/basic/engine.go:41` | packages/compaction/compaction-basic/src/index.ts | 293 | — | `resolveModelInfo` | 注释锚点 |  | 锚点符号在该上游文件里找不到 |
| `compaction/basic/engine.go:57` | packages/compaction/compaction-basic/src/index.ts | 104 | — | `EngineDeps` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/engine.go:134` | packages/compaction/compaction-basic/src/index.ts | 126-130 | — | `NewEngine` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/engine.go:203` | packages/compaction/compaction-basic/src/index.ts | 283-291 | — | `compactOverflow` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/engine.go:233` | packages/compaction/compaction-basic/src/index.ts | 293-331 | — | `compactPressure` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/engine.go:401` | packages/compaction/compaction-basic/src/index.ts | 375-411 | — | `compactUnderClaim` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/engine.go:456` | packages/compaction/compaction-basic/src/index.ts | 423-428 | — | `regionDeps` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/engine.go:484` | packages/compaction/compaction-basic/src/index.ts | 241-244 | — | `conversationPolicy` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/install.go:26` | packages/compaction/compaction-basic/src/index.ts | 147 | — | `Agents` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/install.go:60` | packages/compaction/compaction-basic/src/index.ts | 104 | — | `InstallDeps` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/install.go:77` | packages/compaction/compaction-basic/src/index.ts | 122-124 | — | `installer` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/install.go:99` | packages/compaction/compaction-basic/src/index.ts | 129 | — | `SessionReferenceResolver` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `compaction/basic/install.go:150` | packages/compaction/compaction-basic/src/index.ts | 147-223 | — | `observe` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/install.go:200` | packages/compaction/compaction-basic/src/index.ts | 147-165 | — | `onPreStep` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/install.go:218` | packages/compaction/compaction-basic/src/index.ts | 151-162 | — | `compactForPressure` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/install.go:237` | packages/compaction/compaction-basic/src/index.ts | 167-169 | — | `onStatus` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/install.go:247` | packages/compaction/compaction-basic/src/index.ts | 173-177 | — | `toolCallUpdate` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `compaction/basic/install.go:264` | packages/compaction/compaction-basic/src/index.ts | 179-223 | — | `onRequestError` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/install.go:373` | packages/compaction/compaction-basic/src/index.ts | 153 | — | `agentContextOf` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/region.go:124` | packages/compaction/compaction-basic/src/region.ts | 517-550 | — | `InspectEntryState` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/region.go:186` | packages/compaction/compaction-basic/src/region.ts | 286-298 | — | `CheckInactive` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/summarize.go:21` | packages/compaction/compaction-basic/src/summarizer.ts | 151 | — | `stream` | 注释锚点 |  | 锚点符号在该上游文件里找不到 |
| `compaction/basic/summarize.go:34` | packages/compaction/compaction-basic/src/summarizer.ts | 157 | — | `compactionPlugin` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/summarize.go:42` | packages/compaction/compaction-basic/src/summarizer.ts | 31-66 | — | `compactionInstruction` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/summarize.go:169` | packages/compaction/compaction-basic/src/summarizer.ts | 122-140 | — | `summarizationTarget` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/summarizer.go:24` | packages/compaction/compaction-basic/src/summarizer.ts | 69-70 | — | `checkpointPreamble` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/transaction.go:62` | packages/compaction/compaction-basic/src/region.ts | 27-30 | — | `RegionDeps` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/transaction.go:89` | packages/compaction/compaction-basic/src/region.ts | 53-62 | — | `TransactionOptions` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/transaction.go:116` | packages/compaction/compaction-basic/src/region.ts | 75 | — | `errSurfaceChanged` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/transaction.go:320` | packages/compaction/compaction-basic/src/region.ts | 191-193 | — | `stabilityOf` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/transaction.go:339` | packages/compaction/compaction-basic/src/region.ts | 170-186 | — | `openBracket` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/basic/transaction.go:371` | packages/compaction/compaction-basic/src/region.ts | 257-277 | — | `manualFailure` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/checkpoint.go:16` | packages/compaction/compaction/src/checkpoint.ts | 19 | — | `CheckpointPlugin` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/engine.go:112` | packages/compaction/compaction/src/index.ts | 71-78 | — | `Maintainer` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/invariant.go:21` | packages/compaction/compaction/src/invariant.ts | 19-25 | — | `OpenCompaction` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/invariant.go:76` | packages/compaction/compaction/src/invariant.ts | 139-212 | — | `defineDomain` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `compaction/invariant.go:155` | packages/compaction/compaction/src/invariant.ts | 155-172 | — | `validateStart` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/invariant.go:184` | packages/compaction/compaction/src/invariant.ts | 174-198 | — | `validateSummary` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/invariant.go:232` | packages/compaction/compaction/src/invariant.ts | 200-212 | — | `validateEnd` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/invariant.go:294` | packages/compaction/compaction/src/invariant.ts | 42-54 | — | `requireOpen` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/invariant.go:377` | packages/compaction/compaction/src/invariant.ts | 76-93 | — | `orphanStartSeqs` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/invariant.go:399` | packages/compaction/compaction/src/invariant.ts | 38-40 | — | `requireID` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/toolpairing.go:17` | packages/compaction/compaction/src/tool-pairing.ts | 116-131 | — | `SurfaceView` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/toolpairing.go:53` | packages/compaction/compaction/src/tool-pairing.ts | 9-23 | — | `BalanceIndex` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/toolpairing.go:105` | packages/compaction/compaction/src/tool-pairing.ts | 76-97 | — | `sync` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/toolpairing.go:135` | packages/compaction/compaction/src/tool-pairing.ts | 53-74 | — | `extend` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/toolresultpruner/session.go:20` | packages/compaction/compaction-tool-result-pruner/src/index.ts | 46 | — | `Estimator` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/types.go:71` | packages/compaction/compaction/src/types.ts | 20-24 | — | `StartData` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/types.go:121` | packages/compaction/compaction/src/types.ts | 34-77 | — | `SummaryData` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/types.go:256` | packages/compaction/compaction/src/types.ts | 79-83 | — | `EndData` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `compaction/types.go:310` | packages/compaction/compaction/src/types.ts | 85-90 | — | `PruneData` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/compose.go:24` | packages/context/agent-instructions/src/index.ts | 60-62 | — | `workspaceContexts` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/compose.go:37` | packages/context/agent-instructions/src/state.ts | 282-288 | — | `changesOf` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/compose.go:50` | packages/context/agent-instructions/src/index.ts | 64-67 | — | `samePayload` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/compose.go:131` | packages/context/agent-instructions/src/state.ts | 136-156 | — | `visibleChanges` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/compose.go:432` | packages/context/agent-instructions/src/index.ts | 322-348 | — | `onPreStep` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/config.go:16` | packages/context/agent-instructions/src/config.ts | 14 | — | `DefaultMaxSourceBytes` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/config.go:34` | packages/context/agent-instructions/src/config.ts | 15 | — | `reservedPathSegments` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/config.go:127` | packages/context/agent-instructions/src/config.ts | 119-123 | — | `resolveCandidates` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/files.go:56` | packages/context/agent-instructions/src/files.ts | 36-40 | — | `discoveredFile` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/files.go:77` | packages/context/agent-instructions/src/files.ts | 73-77 | — | `ProbeKind` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/files.go:103` | packages/context/agent-instructions/src/files.ts | 79-88 | — | `statProbe` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/files.go:126` | packages/context/agent-instructions/src/files.ts | 222 | — | `resolvePath` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/files.go:585` | packages/context/agent-instructions/src/files.ts | 468-470 | — | `scopeDirectoryPath` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/install.go:26` | packages/context/agent-instructions/src/index.ts | 322 | — | `Agents` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/install.go:53` | packages/context/agent-instructions/src/index.ts | 350 | — | `tools` | 注释锚点 |  | 锚点符号在该上游文件里找不到 |
| `context/instructions/install.go:61` | packages/context/agent-instructions/src/index.ts | 80 | — | `Deps` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/install.go:95` | packages/context/agent-instructions/src/index.ts | 81-103 | — | `installer` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/install.go:201` | packages/context/agent-instructions/src/index.ts | 305-357 | — | `observe` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/install.go:247` | packages/context/agent-instructions/src/index.ts | 88-94 | — | `shutdown` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/projection.go:23` | packages/context/agent-instructions/src/index.ts | 85 | — | `touch` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/projection.go:31` | packages/context/agent-instructions/src/index.ts | 82-85 | — | `preparation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/projection.go:42` | packages/context/agent-instructions/src/index.ts | 81-103 | — | `sessionState` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/projection.go:103` | packages/context/agent-instructions/src/index.ts | 262-275 | — | `queue` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/projection.go:144` | packages/context/agent-instructions/src/index.ts | 277-280 | — | `wait` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/projection.go:217` | packages/context/agent-instructions/src/index.ts | 305-320 | — | `toolCallUpdate` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `context/instructions/projection.go:247` | packages/context/agent-instructions/src/index.ts | 69 | — | `fileTouchToolNames` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/projection.go:280` | packages/context/agent-instructions/src/index.ts | 341-357 | — | `onToolResult` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/render.go:177` | packages/context/agent-instructions/src/render.ts | 110 | — | `scopeSeparator` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/state.go:24` | packages/context/agent-instructions/src/state.ts | 39-40 | — | `sourceForm` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/state.go:77` | packages/context/agent-instructions/src/state.ts | 100-127 | — | `UnmarshalJSON` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/state.go:103` | packages/context/agent-instructions/src/state.ts | 112-127 | — | `decodeChanges` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/state.go:135` | packages/context/agent-instructions/src/state.ts | 100-106 | — | `ParseSource` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/state.go:150` | packages/context/agent-instructions/src/state.ts | 81-86 | — | `ContextMessage` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/state.go:200` | packages/context/agent-instructions/src/state.ts | 129-134 | — | `sameChange` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/state.go:344` | packages/context/agent-instructions/src/state.ts | 246-259 | — | `ReconcileRequest` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/state.go:453` | packages/context/agent-instructions/src/state.ts | 269-299 | — | `collectScopes` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/state.go:522` | packages/context/agent-instructions/src/state.ts | 324-330 | — | `groupScopesByDirectory` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/instructions/state.go:588` | packages/context/agent-instructions/src/state.ts | 331-422 | — | `reconcileDirectory` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/sessionref/install.go:20` | packages/context/session-reference/src/index.ts | 106 | — | `on` | 注释锚点 |  | 锚点符号在该上游文件里找不到 |
| `context/sessionref/install.go:32` | packages/context/session-reference/src/index.ts | 85-114 | — | `Deps` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/sessionref/install.go:90` | packages/context/session-reference/src/index.ts | 106-113 | — | `onPreStep` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/sessionref/install.go:123` | packages/context/session-reference/src/index.ts | 124-148 | — | `prepareDirect` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/sessionref/install.go:179` | packages/context/session-reference/src/index.ts | 132-137 | — | `stripMentions` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/sessionref/projection.go:297` | packages/context/session-reference/src/projection.ts | 140-142 | — | `contentText` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/sessionref/projection.go:313` | packages/context/session-reference/src/projection.ts | 163 | — | `omissionNotice` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/sessionref/resolver.go:20` | packages/context/session-reference/src/index.ts | 47-55 | — | `promptPrefix` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/sessionref/resolver.go:37` | packages/context/session-reference/src/index.ts | 56 | — | `promptSuffix` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/sessionref/resolver.go:78` | packages/context/session-reference/src/index.ts | 74-302 | — | `Resolver` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/sessionref/resolver.go:87` | packages/context/session-reference/src/index.ts | 85-114 | — | `NewResolver` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/sessionref/resolver.go:161` | packages/context/session-reference/src/index.ts | 208-228 | — | `MentionCandidates` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/sessionref/resolver.go:264` | packages/context/session-reference/src/index.ts | 174-177 | — | `rankRecords` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/sessionref/resolver.go:277` | packages/context/session-reference/src/index.ts | 179-191 | — | `readLabels` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/sessionref/resolver.go:306` | packages/context/session-reference/src/index.ts | 192-196 | — | `matches` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/sessionref/resolver.go:376` | packages/context/session-reference/src/index.ts | 345-370 | — | `checkCancelled` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/sessionref/types.go:18` | packages/context/session-reference/src/types.ts | 14 | — | `name` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `context/sessionref/types.go:23` | packages/context/session-reference/src/types.ts | 16 | — | `sourceForm` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/sessionref/types.go:31` | packages/context/session-reference/src/types.ts | 17 | — | `sourceVersion` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/sessionref/types.go:36` | packages/context/session-reference/src/types.ts | 18-29 | — | `Reference` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/sessionref/types.go:197` | packages/context/session-reference/src/index.ts | 269-280 | — | `MessageSource` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `context/sessionref/uri.go:41` | packages/context/session-reference/src/uri.ts | 24 | — | `base64URLPayload` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/sessionref/uri.go:101` | packages/context/session-reference/src/uri.ts | 70 | — | `mentionPattern` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/sessionref/uri.go:180` | packages/context/session-reference/src/uri.ts | 90-92 | — | `labelEscapes` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/timecontext/history.go:26` | packages/context/time-context/src/index.ts | 57-71 | — | `PrecedingMessageTime` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/timecontext/history.go:46` | packages/context/time-context/src/index.ts | 73-84 | — | `PrecedingStepContextTime` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/timecontext/history.go:77` | packages/context/time-context/src/index.ts | 86-96 | — | `LatestInjectionTime` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/timecontext/history.go:97` | packages/context/time-context/src/index.ts | 183-185 | — | `PreviousBaseline` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/timecontext/history.go:113` | packages/context/time-context/src/index.ts | 177-182 | — | `ShouldInject` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/timecontext/history.go:139` | packages/context/time-context/src/index.ts | 89-91 | — | `IsReadingEvent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/timecontext/install.go:22` | packages/context/time-context/src/index.ts | 24 | — | `Agents` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/timecontext/install.go:39` | packages/context/time-context/src/index.ts | 24 | — | `Deps` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/timecontext/install.go:108` | packages/context/time-context/src/index.ts | 170-208 | — | `onPreStep` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/timecontext/invariant.go:25` | packages/context/time-context/src/invariant.ts | 14-20 | — | `readingPattern` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/timecontext/render.go:81` | packages/context/time-context/src/index.ts | 204 | — | `ReadingSource` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `context/timecontext/timestamp.go:11` | packages/context/time-context/src/timestamp.ts | 11-21 | — | `timestampLayout` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/consumedwork_test.go:85` | packages/core/agent/src/consumed-work.ts | 33-58 | — | `TestFoldConsumedWorkClaimWithoutStep` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/error.go:17` | packages/core/agent/src/inbox.ts | 37 | — | `ErrMalformedEvent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/error.go:25` | packages/core/agent/src/inbox.ts | 209 | — | `ErrInvalidSplice` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/error.go:39` | packages/core/agent/src/index.ts | 388 | — | `ErrFactoryAlreadySet` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/error.go:51` | packages/core/agent/src/index.ts | 487 | — | `ErrAgentAlreadyExists` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/error.go:56` | packages/core/agent/src/index.ts | 548 | — | `ErrAgentNotLive` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/error.go:61` | packages/core/agent/src/index.ts | 551 | — | `ErrAlreadyAnnounced` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/error.go:68` | packages/core/agent/src/index.ts | 481-483 | — | `ErrIdentityMismatch` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/error.go:77` | packages/core/agent/src/invariant.ts | 20 | — | `agent` | 注释锚点 |  | 锚点符号在该上游文件里找不到 |
| `core/agent/inbox.go:59` | packages/core/agent/src/inbox.ts | 28-40 | — | `NewInbox` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/inbox.go:88` | packages/core/agent/src/inbox.ts | 42-45 | — | `NextTurn` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/inbox.go:97` | packages/core/agent/src/inbox.ts | 47-50 | — | `NextStep` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/observer.go:48` | packages/core/agent/src/runtime-types.ts | 179-186 | — | `InboxObserver` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/observer.go:72` | packages/core/agent/src/runtime-types.ts | 220-231 | — | `PreStep` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/observer.go:103` | packages/core/agent/src/runtime-types.ts | 232-244 | — | `AskUserQuestionRequest` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `core/agent/observer.go:125` | packages/core/agent/src/runtime-types.ts | 245-260 | — | `RequestFailure` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/observer.go:172` | packages/core/agent/src/runtime-types.ts | 279-290 | — | `TurnError` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/observer.go:194` | packages/core/agent/src/runtime-types.ts | 146-292 | — | `registryLayer` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/registry.go:235` | packages/core/agent/src/index.ts | 266-298 | — | `NewRegistry` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/registry.go:265` | packages/core/agent/src/runtime-types.ts | 148-159 | — | `OnCreated` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/registry.go:281` | packages/core/agent/src/runtime-types.ts | 160-168 | — | `OnDisposed` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/registry.go:293` | packages/core/agent/src/runtime-types.ts | 169-178 | — | `OnStatus` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/registry.go:308` | packages/core/agent/src/runtime-types.ts | 179-186 | — | `OnInboxInserted` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/registry.go:320` | packages/core/agent/src/runtime-types.ts | 198-205 | — | `OnInboxDiscarded` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/registry.go:332` | packages/core/agent/src/runtime-types.ts | 187-197 | — | `OnInboxClaimed` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/registry.go:344` | packages/core/agent/src/runtime-types.ts | 206-217 | — | `OnSessionStart` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/registry.go:356` | packages/core/agent/src/runtime-types.ts | 219-231 | — | `OnPreStep` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/registry.go:368` | packages/core/agent/src/runtime-types.ts | 232-244 | — | `OnRequest` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/registry.go:380` | packages/core/agent/src/runtime-types.ts | 245-260 | — | `OnRequestError` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/registry.go:392` | packages/core/agent/src/runtime-types.ts | 261-278 | — | `OnTurnStopping` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/registry.go:404` | packages/core/agent/src/runtime-types.ts | 279-290 | — | `OnError` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/registry.go:602` | packages/core/agent/src/index.ts | 511-525 | — | `detachEnteredLocked` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/registry.go:775` | packages/core/agent/src/runtime-types.ts | 169-178 | — | `ReportStatus` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/registry.go:777` | packages/core/agent/src/invariant.ts | 15-22 | — | `ReportStatus` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/registry.go:811` | packages/core/agent/src/runtime-types.ts | 179-186 | — | `ReportInboxInserted` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/registry.go:820` | packages/core/agent/src/runtime-types.ts | 198-205 | — | `ReportInboxDiscarded` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/registry.go:846` | packages/core/agent/src/runtime-types.ts | 187-197 | — | `ReportInboxClaimed` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/registry.go:863` | packages/core/agent/src/runtime-types.ts | 206-217 | — | `ReportSessionStart` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/registry.go:880` | packages/core/agent/src/runtime-types.ts | 279-290 | — | `ReportError` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/registry.go:899` | packages/core/agent/src/runtime-types.ts | 261-278 | — | `TurnStopping` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/registry.go:925` | packages/core/agent/src/runtime-types.ts | 219-231 | — | `ResolvePreStep` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/registry.go:954` | packages/core/agent/src/runtime-types.ts | 232-244 | — | `ResolveRequest` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/registry.go:983` | packages/core/agent/src/runtime-types.ts | 245-260 | — | `ResolveRequestError` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/registry.go:1014` | packages/core/agent/src/dispatch.ts | 107-149 | — | `collectObservers` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/registry.go:1064` | packages/core/agent/src/index.ts | 528-540 | — | `callNotify` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/types.go:30` | packages/core/agent/src/types.ts | 12-27 | — | `EventInboxSpliced` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agent/types.go:53` | packages/core/agent/src/types.ts | 19-25 | — | `SplicedData` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentdefaultmodel/config.go:60` | packages/core/agent-default-model/src/index.ts | 34-38 | — | `validateSettings` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentdefaultmodel/config_test.go:87` | packages/core/agent-default-model/src/invariant.ts | 14 | — | `TestPackageNameIsTheDSHNameVerbatim` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentdefaultmodel/config_test.go:101` | packages/core/agent-default-model/src/index.ts | 21 | — | `TestSettingsNamespaceIsTheDSHNamespaceVerbatim` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentdefaultmodel/config_test.go:118` | packages/core/agent-default-model/src/index.ts | 65-68 | — | `TestNewRefusesACompositionEntryThatNamesNoModel` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentdefaultmodel/config_test.go:137` | packages/core/agent-default-model/src/index.ts | 60-62 | — | `TestADeploymentWithoutASettingsProviderReadsTheCompositionEntry` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentdefaultmodel/config_test.go:151` | packages/core/agent-default-model/src/index.ts | 99 | — | `TestSavingWithoutASettingsProviderKeepsTheCompositionEntry` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentdefaultmodel/config_test.go:171` | packages/core/agent-default-model/src/index.ts | 76-81 | — | `TestAStoredSelectionOverridesTheCompositionEntry` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentdefaultmodel/config_test.go:189` | packages/core/agent-default-model/src/index.ts | 76 | — | `TestAPartialStoredSectionOnlyOverridesWhatItNames` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentdefaultmodel/config_test.go:203` | packages/core/agent-default-model/src/index.ts | 35-36 | — | `TestAStoredSectionThatNamesNoProviderFailsRegistration` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentdefaultmodel/config_test.go:241` | packages/core/agent-default-model/src/index.ts | 78-80 | — | `TestCurrentSelectionSeesACommittedChangeWithoutAnyRebuild` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentdefaultmodel/config_test.go:263` | packages/core/agent-default-model/src/index.ts | 99-103 | — | `TestSaveSelectionReplacesTheWholeSection` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentdefaultmodel/config_test.go:288` | packages/core/agent-default-model/src/index.ts | 53-55 | — | `TestAnAbsentReasoningEffortIsTheEmptyString` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentdefaultmodel/config_test.go:317` | packages/core/agent-default-model/src/index.ts | 35-36 | — | `TestSaveSelectionRefusesASelectionThatNamesNoProvider` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentdefaultmodel/config_test.go:338` | packages/settings/settings/src/index.ts | 876-886 | — | `TestUndoFallsBackToTheCompositionEntry` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent.go:53` | packages/core/agent-loop/src/agent.ts | 38-46 | — | `phaseKind` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent.go:95` | packages/core/agent-loop/src/agent.ts | 99-101 | — | `statusOf` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent.go:203` | packages/core/agent-loop/src/agent.ts | 80-97 | — | `NewReactLoopAgent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent.go:281` | packages/core/agent-loop/src/agent.ts | 92 | — | `lastTurnOf` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent.go:359` | packages/core/agent-loop/src/agent.ts | 103-111 | — | `setPhaseLocked` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent.go:633` | packages/core/agent-loop/src/agent.ts | 202-208 | — | `reportError` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent.go:756` | packages/core/agent-loop/src/agent.ts | 262-315 | — | `turnBody` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent.go:918` | packages/core/agent-loop/src/agent.ts | 281-293 | — | `runStep` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent.go:1067` | packages/core/agent-loop/src/agent.ts | 345-353 | — | `consumeStream` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent.go:1113` | packages/core/agent-loop/src/agent.ts | 355-369 | — | `appendInterrupted` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent.go:1142` | packages/core/agent-loop/src/agent.ts | 392-399 | — | `assembledMessage` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent.go:1175` | packages/core/agent-loop/src/agent.ts | 416 | — | `acceptToolContext` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent.go:1308` | packages/core/agent-loop/src/agent.ts | 483-489 | — | `foldRequestHeader` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent.go:1342` | packages/core/agent-loop/src/agent.ts | 491-502 | — | `foldRequestContext` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:438` | packages/core/agent-loop/src/agent.ts | 92 | — | `TestLastTurnOfReadsTheLatestTurnStart` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:506` | packages/core/agent-loop/src/agent.ts | 99-101 | — | `TestStatusOfProjectsTheRunningPhaseOnly` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:603` | packages/core/agent-loop/src/agent.ts | 54-61 | — | `TestRequestProposalStripsAdapterSuppliedDefaults` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:636` | packages/core/agent-loop/src/agent.ts | 122-132 | — | `TestSendTargetsTheRightInboxBoundary` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:816` | packages/core/agent-loop/src/agent.ts | 99-101 | — | `TestStatusReadsThePhaseThatIsLiveRightNow` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:856` | packages/core/agent-loop/src/agent.ts | 416 | — | `TestAToolsDeferredContextReachesTheNextStep` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:897` | packages/core/agent-loop/src/agent.ts | 418-421 | — | `TestAToolThatConcludesTheTurnStopsTheLoop` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:932` | packages/core/agent-loop/src/tool-calls.ts | 120-137 | — | `TestADeniedToolCallStillGetsItsResultOnTheLog` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:979` | packages/core/agent-loop/src/tool-calls.ts | 120-137 | — | `TestACallToAToolThatDoesNotExistComesBackAsAResult` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:1011` | packages/core/agent-loop/src/agent.ts | 113-120 | — | `TestSendAfterAnAbortRetargetsToTheNextTurn` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:1042` | packages/core/agent-loop/src/agent.ts | 134-140 | — | `TestCancelEmptiesTheInboxUnlessAskedToKeepIt` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:1073` | packages/core/agent-loop/src/agent.ts | 142-162 | — | `TestRunMaintenanceRefusesANilTaskAndABusyAgent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:1120` | packages/core/agent-loop/src/agent.ts | 164-193 | — | `TestRunMaintenanceReplaysTheWakeItHeldBack` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:1150` | packages/core/agent-loop/src/agent.ts | 195-200 | — | `TestWhenIdleReturnsAtOnceWhenNothingIsRunning` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:1298` | packages/core/agent-loop/src/agent.ts | 262-315 | — | `TestAVetoedPreStepClosesTheTurnAsBlocked` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:1331` | packages/core/agent-loop/src/agent.ts | 271-274 | — | `TestAnEmptyFirstStepClosesTheTurnWithoutAModelCall` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:1361` | packages/core/agent-loop/src/agent.ts | 296-299 | — | `TestMaxTokensStaysStickyAcrossLaterSteps` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:1395` | packages/core/agent-loop/src/agent.ts | 401-419 | — | `TestToolCallsDriveASecondStep` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:1430` | packages/core/agent-loop/src/agent.ts | 317-329 | — | `TestASecondTurnRunsWhileTheInboxStillHasWork` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:1467` | packages/core/agent-loop/src/agent.ts | 371-390 | — | `TestAFailedRequestClosesTheTurnWithTheFailure` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:1500` | packages/core/agent-loop/src/agent.ts | 371-390 | — | `TestARetryingObserverGetsASecondAttempt` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:1537` | packages/core/agent-loop/src/agent.ts | 371-390 | — | `TestTheAdaptersOwnRetryPolicyReachesTheRecoveryWaterfall` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:1578` | packages/core/agent-loop/src/agent.ts | 371-390 | — | `TestAnAbortedFinishFromTheModelEndsTheTurnAsAFailure` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:1622` | packages/core/agent-loop/src/agent.ts | 466-471 | — | `TestARouteWithoutAProviderOrModelFailsTheTurn` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:1649` | packages/core/agent-loop/src/agent.ts | 345-353 | — | `TestChunksAreLoggedBeforeTheAssemblerSeesThem` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:1686` | packages/core/agent-loop/src/agent.ts | 355-369 | — | `TestAnInterruptedStreamKeepsTheSafePrefix` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:1733` | packages/core/agent-loop/src/agent.ts | 392-399 | — | `TestUsageAndReplayStateRideAlongTheAssistantMessage` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:1781` | packages/core/agent-loop/src/agent.ts | 355-369 | — | `TestAnInterruptedMessageKeepsTheUsageAlreadyReported` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:1822` | packages/core/agent-loop/src/agent.ts | 357-359 | — | `TestAnInterruptedStreamWithNothingYetWritesNoMessage` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:1851` | packages/core/agent-loop/src/agent.ts | 483-489 | — | `TestTheHeaderAnchorIsWrittenOnceThenOnlyOnChange` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:1880` | packages/core/agent-loop/src/agent.ts | 483-489 | — | `TestTheFirstHeaderOnAnExistingLogIsAResume` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/agent_test.go:1919` | packages/core/agent-loop/src/agent.ts | 436-460 | — | `TestAResolvedAdapterStampsItsModelFactsOnTheLog` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/invariant.go:121` | packages/core/agent-loop/src/invariant.ts | 19-58 | — | `checkLoopRequest` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/invariant.go:179` | packages/core/agent-loop/src/invariant.ts | 31 | — | `hasStepStart` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/invariant.go:254` | packages/core/agent-loop/src/invariant.ts | 51 | — | `sameTools` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/invariant.go:291` | packages/core/agent-loop/src/invariant.ts | 41 | — | `sameJSON` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/invariant_test.go:171` | packages/core/agent-loop/src/invariant.ts | 11 | — | `TestPackageNameIsTheDSHNameVerbatim` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/invariant_test.go:240` | packages/core/agent-loop/src/invariant.ts | 20 | — | `TestInvariantIgnoresRequestsTheLoopDidNotBuild` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/invariant_test.go:262` | packages/core/agent-loop/src/invariant.ts | 26-28 | — | `TestLoopRequestMustCarryASessionID` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/invariant_test.go:276` | packages/core/agent-loop/src/invariant.ts | 29-30 | — | `TestLoopRequestMustCarryALiveSessionID` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/invariant_test.go:290` | packages/core/agent-loop/src/invariant.ts | 31-33 | — | `TestLoopRequestNeedsAStepStartInItsLog` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/invariant_test.go:312` | packages/core/agent-loop/src/invariant.ts | 35-38 | — | `TestLoopRequestNeedsARequestHeaderInItsLog` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/invariant_test.go:335` | packages/core/agent-loop/src/invariant.ts | 40-44 | — | `TestLoopRequestMustMatchTheDurableDerivation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/invariant_test.go:357` | packages/core/agent-loop/src/invariant.ts | 45-52 | — | `TestLoopRequestMustMatchTheFoldedHeader` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/invariant_test.go:449` | packages/core/agent-loop/src/invariant.ts | 51 | — | `TestInvariantTreatsNilAndEmptyToolListsAsTheSame` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/invariant_test.go:575` | packages/core/agent-loop/src/invariant.ts | 51 | — | `TestSameToolsComparesSchemaBytes` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop.go:120` | packages/core/agent-loop/src/index.ts | 260-271 | — | `ConfiguredAgent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop.go:171` | packages/core/agent-loop/src/index.ts | 28 | — | `SessionPersistence` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop.go:252` | packages/core/agent-loop/src/index.ts | 170-179 | — | `ConfigStartFailedObserver` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop.go:298` | packages/core/agent-loop/src/index.ts | 82 | — | `errLoopNotActive` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop.go:446` | packages/core/agent-loop/src/index.ts | 94-97 | — | `abortCause` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop.go:613` | packages/core/agent-loop/src/index.ts | 377-379 | — | `agentVariable` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop.go:660` | packages/core/agent-loop/src/index.ts | 160-179 | — | `OnConfigStartFailed` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop.go:672` | packages/core/agent-loop/src/index.ts | 381 | — | `startConfiguredAgents` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop.go:685` | packages/core/agent-loop/src/index.ts | 384-397 | — | `startFreshConfigured` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop.go:710` | packages/core/agent-loop/src/index.ts | 398-410 | — | `startResumingConfigured` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop.go:761` | packages/core/agent-loop/src/index.ts | 396-403 | — | `notifyStartFailed` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:218` | packages/core/agent-loop/src/index.ts | 213-215 | — | `TestApplyLauncherIdentitiesLeavesConfiguredIdentitiesAloneWhenNobodyIsNamed` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:232` | packages/core/agent-loop/src/index.ts | 216-232 | — | `TestApplyLauncherIdentitiesSwapsBothIdentityKeysTogether` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:280` | packages/core/agent-loop/src/index.ts | 132-139 | — | `TestResolveMaxParallelToolCallsTreatsZeroAsUnset` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:304` | packages/core/agent-loop/src/index.ts | 141-147 | — | `TestAssertAgentOptionsRejectsANegativeOutputCap` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:325` | packages/core/agent-loop/src/index.ts | 277-283 | — | `TestValidateConfiguredAgentsRejectsTwoIdentitiesOnOneEntry` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:343` | packages/core/agent-loop/src/index.ts | 284-291 | — | `TestValidateConfiguredAgentsRejectsADuplicateExactIdentity` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:394` | packages/core/agent-loop/src/index.ts | 94-97 | — | `TestAbortCauseStaysQuietOnALiveContext` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:406` | packages/core/agent-loop/src/index.ts | 95-96 | — | `TestAbortCauseNamesTheAgentWhenTheReasonIsPlain` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:427` | packages/core/agent-loop/src/index.ts | 95 | — | `TestAbortCausePassesARicherReasonThroughUntouched` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:444` | packages/core/agent-loop/src/index.ts | 99-118 | — | `TestRaceAbortHandsBackTheWorkThatWon` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:464` | packages/core/agent-loop/src/index.ts | 100-102 | — | `TestRaceAbortRefusesBeforeStartingWorkOnADeadContext` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:528` | packages/core/agent-loop/src/index.ts | 113 | — | `TestRaceAbortDoesNotReleaseAResultThatFailed` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:562` | packages/core/agent-loop/src/index.ts | 55-57 | — | `TestFactoryOwnershipStopsAcceptingOnceDisposed` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:584` | packages/core/agent-loop/src/index.ts | 85-88 | — | `TestFactoryOwnershipTearsDownEveryLiveAgentEvenWhenOneFails` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:623` | packages/core/agent-loop/src/index.ts | 59-63 | — | `TestFactoryOwnershipForgetsAnAgentThatAlreadyToreItselfDown` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:651` | packages/core/agent-loop/src/index.ts | 65-70 | — | `TestFactoryOwnershipWaitsForStartupWorkBeforeItFinishes` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:678` | packages/core/agent-loop/src/index.ts | 77-79 | — | `TestWaitWhileActiveStopsWaitingWhenTeardownBegins` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:777` | packages/core/agent-loop/src/index.ts | 213-233 | — | `TestNewLetsTheLauncherIdentityBeatTheConfiguredOne` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:800` | packages/core/agent-loop/src/index.ts | 336 | — | `TestNewInstallsItselfAsTheRegistryFactory` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:863` | packages/core/agent-loop/src/index.ts | 377-379 | — | `TestTheAgentVariablesReadFromTheAgentOnThatScope` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:906` | packages/core/agent-loop/src/index.ts | 377-379 | — | `TestTheAgentVariablesAreAbsentOffAnyAgent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:939` | packages/core/agent-loop/src/index.ts | 330-334 | — | `TestMaxParallelToolCallsLocksToTheStaticCapWithoutSettings` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:958` | packages/core/agent-loop/src/index.ts | 330-334 | — | `TestMaxParallelToolCallsReadsThroughSettingsEveryTime` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:983` | packages/core/agent-loop/src/index.ts | 340-347 | — | `TestSettingsRejectABadParallelCapBeforeItIsCommitted` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:1006` | packages/core/agent-loop/src/index.ts | 580-587 | — | `TestCreatePublishesIntoBothRegistries` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:1031` | packages/core/agent-loop/src/index.ts | 454-455 | — | `TestCreateRefusesAnUnrepresentableOutputCap` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:1049` | packages/core/agent-loop/src/index.ts | 606-620 | — | `TestCreateAgentRunsSetupAndItsCommitBeforePublishing` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:1093` | packages/core/agent-loop/src/index.ts | 611-615 | — | `TestCreateAgentRollsBackWhenSetupFails` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:1130` | packages/core/agent-loop/src/index.ts | 590-591 | — | `TestCreateAgentNeedsAnOwner` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:1147` | packages/core/agent-loop/src/index.ts | 496-540 | — | `TestDisposingAHandleClearsBothRegistriesAndTheScope` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:1187` | packages/core/agent-loop/src/index.ts | 456-458 | — | `TestCreateRefusesOnceTheFactoryIsGone` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:1247` | packages/core/agent-loop/src/index.ts | 625-630 | — | `TestResumeWithoutPersistenceSaysSo` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:1267` | packages/core/agent-loop/src/index.ts | 637-710 | — | `TestResumeRebuildsTheAgentOnThePersistedSession` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:1306` | packages/core/agent-loop/src/index.ts | 686-689 | — | `TestResumeHandsBackTheLoadFailure` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:1322` | packages/core/agent-loop/src/index.ts | 657-672 | — | `TestResumeStopsWaitingWhenTheCallerGivesUp` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:1363` | packages/core/agent-loop/src/index.ts | 385-388 | — | `TestAConfiguredAgentWithoutAnIdentityGetsAMintedOne` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:1391` | packages/core/agent-loop/src/index.ts | 390-393 | — | `TestAMintedIdentityNeverTouchesPersistence` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:1417` | packages/core/agent-loop/src/index.ts | 406-419 | — | `TestAConfiguredIdentityIsRestoredWhenItAlreadyLanded` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:1445` | packages/core/agent-loop/src/index.ts | 420-427 | — | `TestAConfiguredIdentityIsCreatedTheFirstTimeItIsUsed` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:1466` | packages/core/agent-loop/src/index.ts | 420-424 | — | `TestABrokenArchiveIsNotMistakenForAMissingOne` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:1494` | packages/core/agent-loop/src/index.ts | 398-404 | — | `TestAResumingEntryWithoutPersistenceIsReportedNotHung` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:1542` | packages/core/agent-loop/src/index.ts | 405-410 | — | `TestAConfiguredResumingEntryRebuildsOnTheArchivedSession` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:1567` | packages/core/agent-loop/src/index.ts | 405-410 | — | `TestAConfiguredResumeThatFailsIsReported` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:1625` | packages/core/agent-loop/src/index.ts | 430-451 | — | `TestARestoringConfiguredIdentityWaitsForTheDrainingOccupant` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:1672` | packages/core/agent-loop/src/index.ts | 430-433 | — | `TestAFreeConfiguredIdentityIsNotWaitedOn` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:1699` | packages/core/agent-loop/src/index.ts | 396-403 | — | `TestAPanickingObserverDoesNotStopTheRest` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/loop_test.go:1733` | packages/core/agent-loop/src/index.ts | 384-386 | — | `TestNoStartupFailureIsReportedOnceTheFactoryIsDisposing` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/runtimecontext.go:21` | packages/core/agent-loop/src/runtime-context.ts | 12 | — | `RuntimeContextSource` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/runtimecontext.go:31` | packages/core/agent-loop/src/runtime-context.ts | 13 | — | `runtimeContextCleared` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/runtimecontext.go:83` | packages/core/agent-loop/src/runtime-context.ts | 31-58 | — | `NewRuntimeContextProjection` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/runtimecontext.go:115` | packages/core/agent-loop/src/runtime-context.ts | 32-42 | — | `restore` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/runtimecontext.go:140` | packages/core/agent-loop/src/runtime-context.ts | 44-57 | — | `observe` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/runtimecontext.go:202` | packages/core/agent-loop/src/runtime-context.ts | 15-17 | — | `ownedRuntimeContext` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/runtimecontext.go:226` | packages/core/agent-loop/src/runtime-context.ts | 19-22 | — | `textOfMessage` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/runtimecontext_test.go:33` | packages/core/agent-loop/src/runtime-context.ts | 12 | — | `TestRuntimeContextSourceIsTheDSHStringVerbatim` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/runtimecontext_test.go:68` | packages/core/agent-loop/src/runtime-context.ts | 60-62 | — | `TestProjectionSaysNothingWhenThereNeverWasAnySnapshot` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/runtimecontext_test.go:84` | packages/core/agent-loop/src/runtime-context.ts | 66-74 | — | `TestProjectionProposesTheFirstSnapshot` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/runtimecontext_test.go:114` | packages/core/agent-loop/src/runtime-context.ts | 68-72 | — | `TestProjectionOmitsTheSnapshotFormWhenThereAreNoSections` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/runtimecontext_test.go:137` | packages/core/agent-loop/src/runtime-context.ts | 65 | — | `TestProjectionStaysSilentWhileTheSnapshotIsUnchanged` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/runtimecontext_test.go:165` | packages/core/agent-loop/src/runtime-context.ts | 13 | — | `TestProjectionAnnouncesThatEarlierSnapshotsNoLongerApply` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/runtimecontext_test.go:199` | packages/core/agent-loop/src/runtime-context.ts | 44-50 | — | `TestProjectionFollowsTheAuthoritativeLogNotItsOwnProposals` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/runtimecontext_test.go:257` | packages/core/agent-loop/src/runtime-context.ts | 31-42 | — | `TestProjectionRestoresTheRetainedSnapshotFromAnExistingLog` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/runtimecontext_test.go:278` | packages/core/agent-loop/src/runtime-context.ts | 33-41 | — | `TestProjectionRestoreStopsAtTheLastSnapshot` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/runtimecontext_test.go:302` | packages/core/agent-loop/src/runtime-context.ts | 36-40 | — | `TestProjectionRestoreRemembersASnapshotThatIsNoLongerOnTheSurface` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/runtimecontext_test.go:331` | packages/core/agent-loop/src/runtime-context.ts | 51-56 | — | `TestProjectionForgetsARetainedSnapshotThatAReplacementCovers` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/runtimecontext_test.go:357` | packages/core/agent-loop/src/runtime-context.ts | 53-56 | — | `TestProjectionKeepsARetainedSnapshotAnUnrelatedReplacementDidNotCover` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/runtimecontext_test.go:384` | packages/core/agent-loop/src/runtime-context.ts | 19-22 | — | `TestProjectionRefusesToReadTextOutOfAMultiBlockSnapshot` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/toolcalls.go:36` | packages/core/agent-loop/src/tool-calls.ts | 25-30 | — | `toolSlot` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/toolcalls.go:162` | packages/core/agent-loop/src/tool-calls.ts | 103-110 | — | `parseToolArguments` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/toolcalls_test.go:182` | packages/core/agent-loop/src/tool-calls.ts | 104-106 | — | `TestParseToolArgumentsTurnsNothingIntoAnEmptyObject` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/toolcalls_test.go:196` | packages/core/agent-loop/src/tool-calls.ts | 107-108 | — | `TestParseToolArgumentsPassesValidJSONThroughByteForByte` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/toolcalls_test.go:213` | packages/core/agent-loop/src/tool-calls.ts | 109 | — | `TestParseToolArgumentsWrapsUnparseableArgumentsAsAJSONString` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/toolcalls_test.go:253` | packages/core/agent-loop/src/tool-calls.ts | 64 | — | `TestExecuteToolCallsNeedsAnInitiator` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/toolcalls_test.go:273` | packages/core/agent-loop/src/tool-calls.ts | 262-289 | — | `TestExecuteToolCallsPairsEveryCallWithAResult` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/toolcalls_test.go:322` | packages/core/agent-loop/src/tool-calls.ts | 141-166 | — | `TestExecuteToolCallsCommitsInModelOrderEvenWhenDispatchFinishesOutOfOrder` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/toolcalls_test.go:375` | packages/core/agent-loop/src/tool-calls.ts | 180-183 | — | `TestExecuteToolCallsNeverExceedsTheParallelCap` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/toolcalls_test.go:416` | packages/core/agent-loop/src/tool-calls.ts | 130-137 | — | `TestExecuteToolCallsTreatsAnUndeclaredToolAsExclusive` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/toolcalls_test.go:460` | packages/core/agent-loop/src/tool-calls.ts | 157-160 | — | `TestExecuteToolCallsHandsCommittedContextsToTheSink` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/toolcalls_test.go:494` | packages/core/agent-loop/src/tool-calls.ts | 97-99 | — | `TestExecuteToolCallsReportsAResultThatConcludesTheTurn` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/toolcalls_test.go:523` | packages/core/agent-loop/src/tool-calls.ts | 248-259 | — | `TestExecuteToolCallsSynthesizesResultsForCallsItNeverStarted` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/toolcalls_test.go:574` | packages/core/agent-loop/src/tool-calls.ts | 188-233 | — | `TestExecuteToolCallsStopsStartingCallsOnceCancelled` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/agentloop/toolcalls_test.go:611` | packages/core/agent-loop/src/tool-calls.ts | 261-265 | — | `TestExecuteToolCallsRecordsTheModelArgumentsVerbatim` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/scope/entries.go:177` | packages/core/scope/src/store.ts | 82-88 | — | `All` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/scope/layers.go:72` | packages/core/scope/src/store.ts | 165-170 | — | `NewLayers` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/scope/layers.go:151` | packages/core/scope/src/store.ts | 201-217 | — | `MergeNamed` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/scope/layers.go:186` | packages/core/scope/src/store.ts | 229 | — | `EffectOptions` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/scope/layers.go:275` | packages/core/scope/src/store.ts | 234-247 | — | `layerFor` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/scope/layers.go:298` | packages/core/scope/src/store.ts | 253 | — | `reclaimIfEmpty` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/scope/scope.go:57` | packages/core/scope/src/index.ts | 73-75 | — | `ErrKeyAlreadyBound` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/scope/scope.go:66` | packages/core/scope/src/index.ts | 56 | — | `ErrParentCycle` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/scope/scope.go:137` | packages/core/scope/src/index.ts | 53-59 | — | `linkParent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/scope/scope.go:230` | packages/core/scope/src/index.ts | 158-181 | — | `Admits` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/scope/scope.go:278` | packages/core/scope/src/index.ts | 170-181 | — | `TargetFiltered` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/scope/scope.go:302` | packages/core/scope/src/index.ts | 173-181 | — | `Admits` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/scope/scope.go:318` | packages/core/scope/src/index.ts | 187-204 | — | `AnyCarrier` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/scope/scope.go:425` | packages/core/scope/src/store.ts | 219-266 | — | `Defer` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/scope/scope.go:468` | packages/core/scope/tests/store.spec.ts | 195 | — | `Effects` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/fork.go:96` | packages/core/session/src/index.ts | 1146-1155 | — | `ForkByID` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/fork.go:115` | packages/core/session/src/index.ts | 1090-1108 | — | `forkFrom` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/fork.go:148` | packages/core/session/src/index.ts | 1110-1144 | — | `forkSeed` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/fork.go:205` | packages/core/session/src/index.ts | 1132-1140 | — | `rejectOpenTurn` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/preparation.go:42` | packages/core/session/src/preparation.ts | 36-38 | — | `NewPreparation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/session.go:131` | packages/core/session/src/index.ts | 493-544 | — | `newSession` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/session.go:296` | packages/core/session/src/index.ts | 427-430 | — | `SurfaceReplaceGeneration` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/session.go:360` | packages/core/session/src/index.ts | 625-647 | — | `commit` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/session.go:406` | packages/core/session/src/index.ts | 650-656 | — | `finishPublishing` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/session.go:428` | packages/core/session/src/index.ts | 915-925 | — | `attach` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/store.go:61` | packages/core/session/src/index.ts | 37-93 | — | `storeLayer` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/store.go:158` | packages/core/session/src/index.ts | 795-806 | — | `NewStore` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/store.go:193` | packages/core/session/src/index.ts | 44-53 | — | `OnCreated` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/store.go:213` | packages/core/session/src/index.ts | 54-63 | — | `OnDisposed` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/store.go:229` | packages/core/session/src/index.ts | 64-77 | — | `OnEvent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/store.go:245` | packages/core/session/src/index.ts | 78-91 | — | `OnFlush` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/store.go:401` | packages/core/session/src/index.ts | 869-871 | — | `PrepareRestored` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/store.go:415` | packages/core/session/src/index.ts | 848-858 | — | `mintID` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/store.go:517` | packages/core/session/src/index.ts | 949-958 | — | `detachEnteredLocked` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/store.go:603` | packages/core/session/src/index.ts | 650-656 | — | `afterPublish` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/store.go:796` | packages/core/session/src/index.ts | 1035-1043 | — | `callFlushObserver` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/validate.go:53` | packages/core/session/src/index.ts | 215-217 | — | `legacyHeaderDelta` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/validate.go:62` | packages/core/session/src/index.ts | 367-371 | — | `legacyFallbackReason` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/validate.go:147` | packages/core/session/src/index.ts | 212-250 | — | `validateSeedEvent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/validate.go:181` | packages/core/session/src/index.ts | 252-277 | — | `validateCurrentLLMShape` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/validate.go:195` | packages/core/session/src/index.ts | 256-268 | — | `validateSeedRequestHeader` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/validate.go:211` | packages/core/session/src/index.ts | 280-298 | — | `validateAdapterDefaults` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/validate.go:230` | packages/core/session/src/index.ts | 300-360 | — | `validateMessageEventShape` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/validate.go:280` | packages/core/session/src/index.ts | 339-359 | — | `validateToolResultShape` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/validate.go:301` | packages/core/session/src/index.ts | 301-311 | — | `messageOf` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/session/validate.go:333` | packages/core/session/src/index.ts | 363-372 | — | `validateSupportedRequestHeader` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/systemprompt/prompt.go:29` | packages/core/system-prompt/src/index.ts | 131 | — | `PERSONA_ORDER` | 裁决表+路径一致 |  | 锚点符号在该上游文件里找不到 |
| `core/systemprompt/prompt.go:39` | packages/core/system-prompt/src/index.ts | 359 | — | `harnessIdentitySection` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/systemprompt/prompt.go:44` | packages/core/system-prompt/src/index.ts | 360 | — | `harnessIdentityOrder` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/systemprompt/prompt.go:49` | packages/core/system-prompt/src/index.ts | 361 | — | `harnessIdentityText` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/systemprompt/prompt.go:54` | packages/core/system-prompt/src/index.ts | 134 | — | `variableNamePattern` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/systemprompt/prompt.go:59` | packages/core/system-prompt/src/index.ts | 137 | — | `groupAtPattern` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/systemprompt/prompt.go:77` | packages/core/system-prompt/src/index.ts | 65 | — | `TextProvider` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/systemprompt/prompt.go:322` | packages/core/system-prompt/src/index.ts | 283 | — | `knownVariableList` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/systemprompt/prompt.go:452` | packages/core/system-prompt/src/index.ts | 171 | — | `knownToolList` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/systemprompt/prompt.go:467` | packages/core/system-prompt/src/index.ts | 183-185 | — | `sortToolsByName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/systemprompt/registry.go:31` | packages/core/system-prompt/src/index.ts | 31 | — | `AssembleRule` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/systemprompt/registry.go:67` | packages/core/system-prompt/src/index.ts | 312-325 | — | `newPromptLayer` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/systemprompt/registry.go:164` | packages/core/system-prompt/src/index.ts | 351-370 | — | `NewRegistry` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/systemprompt/registry.go:302` | packages/core/system-prompt/src/index.ts | 31 | — | `OnAssemble` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/systemprompt/registry.go:387` | packages/core/system-prompt/src/index.ts | 472-483 | — | `resolveVariables` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/systemprompt/registry.go:419` | packages/core/system-prompt/src/index.ts | 489-505 | — | `collectTools` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/systemprompt/registry.go:467` | packages/core/system-prompt/src/index.ts | 506-521 | — | `assembleSections` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/systemprompt/registry.go:517` | packages/core/system-prompt/src/index.ts | 524-532 | — | `assembleContexts` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/systemprompt/registry.go:547` | packages/core/system-prompt/src/index.ts | 536 | — | `assembleRulesFor` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/systemprompt/registry.go:563` | packages/core/system-prompt/src/index.ts | 535-538 | — | `runAssembleRules` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/definition.go:179` | packages/core/tools/src/index.ts | 1866-1868 | — | `newExecutionToken` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/jsonschema.go:38` | packages/core/tools/src/json-schema.ts | 87 | — | `schemaTypes` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/jsonschema.go:43` | packages/core/tools/src/json-schema.ts | 315-316 | — | `scalarTypes` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/jsonschema.go:217` | packages/core/tools/src/json-schema.ts | 257-273 | — | `ParseSchema` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/jsonschema.go:283` | packages/core/tools/src/json-schema.ts | 245-298 | — | `decodeSchemaNode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/jsonschema.go:381` | packages/core/tools/src/json-schema.ts | 303-306 | — | `typeViolation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/jsonschema.go:431` | packages/core/tools/src/json-schema.ts | 65-73 | — | `SchemaCode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/jsonschema.go:512` | packages/core/tools/src/json-schema.ts | 200 | — | `siblingKeywords` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/jsonschema.go:625` | packages/core/tools/src/json-schema.ts | 203-223 | — | `checkObjectSchema` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/jsonschema.go:647` | packages/core/tools/src/json-schema.ts | 341-359 | — | `checkScalarSchema` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/jsonschema.go:698` | packages/core/tools/src/json-schema.ts | 178-190 | — | `valueMatchesType` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/jsonvalue.go:126` | packages/core/tools/src/json-schema.ts | 553-580 | — | `checkObjectValue` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/jsonvalue.go:169` | packages/core/tools/src/json-schema.ts | 586-600 | — | `checkArrayValue` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/pipeline.go:54` | packages/core/tools/src/index.ts | 588-591 | — | `PreDecisionKind` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/pipeline.go:81` | packages/core/tools/src/index.ts | 597-600 | — | `PostDecisionKind` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/pipeline.go:158` | packages/core/tools/src/index.ts | 152 | — | `PreExecute` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/pipeline.go:172` | packages/core/tools/src/index.ts | 161 | — | `AroundDispatch` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/pipeline.go:196` | packages/core/tools/src/index.ts | 197 | — | `ObserveResult` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/pipeline.go:249` | packages/core/tools/src/index.ts | 424-441 | — | `StageKind` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/pipeline.go:485` | packages/core/tools/src/index.ts | 1546-1554 | — | `invokeBody` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/pipeline.go:766` | packages/core/tools/src/index.ts | 1846-1863 | — | `materialize` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/pipeline.go:826` | packages/core/tools/src/index.ts | 1477 | — | `collectRules` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/pipeline.go:845` | packages/core/tools/src/index.ts | 1869-1877 | — | `failureResult` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/pipeline.go:862` | packages/core/tools/src/index.ts | 1489-1497 | — | `denialResult` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/pipeline.go:875` | packages/core/tools/src/index.ts | 1746-1754 | — | `blockedResult` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/pipeline.go:916` | packages/core/tools/src/index.ts | 1919-1931 | — | `abortedResult` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/pipeline.go:923` | packages/core/tools/src/index.ts | 1923-1935 | — | `abortedBeforeDispatchResult` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/pipeline.go:931` | packages/core/agent-loop/src/tool-calls.ts | 249-259 | — | `AbortedBeforeDispatchResult` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/runtime.go:63` | packages/core/tools/src/index.ts | 684-687 | — | `compiledRestriction` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/runtime.go:114` | packages/core/tools/src/index.ts | 726-730 | — | `newToolLayer` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/runtime.go:211` | packages/core/tools/src/index.ts | 825-836 | — | `NewRuntime` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/runtime.go:259` | packages/core/tools/src/index.ts | 1025-1050 | — | `validateDefinition` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `core/tools/runtime.go:381` | packages/core/tools/src/index.ts | 1148-1195 | — | `viewOf` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/credentials.go:71` | packages/credentials/credentials/src/index.ts | 16 | — | `refPattern` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/credentials.go:76` | packages/credentials/credentials/src/index.ts | 18-19 | — | `keySegmentPattern` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/credentials.go:88` | packages/credentials/credentials/src/index.ts | 28 | — | `ErrInvalidRef` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/credentials.go:93` | packages/credentials/credentials/src/index.ts | 69 | — | `ErrInvalidKey` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/credentials_test.go:15` | packages/credentials/credentials/tests/credentials.spec.ts | 16-20 | — | `TestNewRefBrandsPOSIXIdentifiers` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/credentials_test.go:33` | packages/credentials/credentials/tests/credentials.spec.ts | 22-26 | — | `TestNewRefRejectsEveryOtherShape` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/credentials_test.go:62` | packages/credentials/credentials/tests/credentials.spec.ts | 29-40 | — | `TestIsKeySegmentAnswersWhatNewKeyWouldAccept` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/credentials_test.go:95` | packages/credentials/credentials/src/index.ts | 66-73 | — | `TestNewKeyJoinsTheTwoSegments` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/credentials_test.go:110` | packages/credentials/credentials/src/index.ts | 82-89 | — | `TestParseKeyIsTheReadHalfOfNewKey` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/credentials_test.go:145` | packages/credentials/credentials/src/index.ts | 98-112 | — | `TestKeyScopeAndIDSplitTheTwoHalves` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/invariant_test.go:45` | packages/credentials/credentials/src/invariant.ts | 16-38 | — | `TestRegisterInvariantsRequiresAllThreePieces` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/invariant_test.go:87` | packages/credentials/credentials/src/invariant.ts | 16-38 | — | `TestANotificationAfterTheServiceIsGoneIsAViolation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/invariant_test.go:125` | packages/credentials/credentials/src/invariant.ts | 16-38 | — | `TestTheRecordKeySpaceCarriesNoSuchCheck` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/invariant_test.go:143` | packages/credentials/credentials/src/invariant.ts | 16-38 | — | `TestUnregisteringAlsoDropsTheSubscription` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/memory_provider_test.go:22` | packages/credentials/credentials/tests/memory.ts | 45 | — | `errMemoryEmptyValue` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/memory_provider_test.go:43` | packages/credentials/credentials/tests/memory.ts | 21-24 | — | `newMemoryCredentials` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/notifier.go:16` | packages/credentials/credentials/src/types.ts | 75 | — | `RefListener` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/notifier.go:24` | packages/credentials/credentials/src/types.ts | 87 | — | `RecordListener` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/notifier.go:29` | packages/credentials/credentials/src/index.ts | 258-307 | — | `Notifier` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/notifier.go:124` | packages/credentials/credentials/src/index.ts | 265-278 | — | `NotifyReferenceUpdated` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/notifier_test.go:51` | packages/credentials/credentials/src/index.ts | 289-313 | — | `TestListenersRunInRegistrationOrder` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/notifier_test.go:138` | packages/credentials/credentials/src/types.ts | 61-88 | — | `TestTheTwoKeySpacesDoNotCrossTalk` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/notifier_test.go:177` | packages/credentials/credentials/src/index.ts | 289-313 | — | `TestAPanickingListenerDoesNotStopTheOthers` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/notifier_test.go:218` | packages/credentials/credentials/src/index.ts | 289-313 | — | `TestAnInvariantFailureIsRethrownAfterEveryListenerRan` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/notifier_test.go:265` | packages/credentials/credentials/src/index.ts | 309-313 | — | `TestListenerFailuresAreLoggedWithEventAndSubject` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/provider.go:21` | packages/credentials/credentials/src/index.ts | 122-130 | — | `CredentialInfo` | 裁决表+路径一致 |  | 锚点符号在该上游文件里找不到 |
| `credentials/provider.go:69` | packages/credentials/credentials/src/index.ts | 243-257 | — | `Mutator` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/provider.go:82` | packages/credentials/credentials/src/types.ts | 61-88 | — | `Observer` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/provider_test.go:21` | packages/credentials/credentials/tests/credentials.spec.ts | 7 | — | `seamRef` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/provider_test.go:41` | packages/credentials/credentials/tests/credentials.spec.ts | 43-47 | — | `TestResolveGivesBackTheValueAndItsSource` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/provider_test.go:69` | packages/credentials/credentials/tests/credentials.spec.ts | 49-53 | — | `TestAnEmptyStoredValueCountsAsAbsentEverywhere` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/provider_test.go:95` | packages/credentials/credentials/src/index.ts | 182-190 | — | `TestAnUnconfiguredReferenceIsNotAnError` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/provider_test.go:115` | packages/credentials/credentials/tests/credentials.spec.ts | 55-65 | — | `TestSetAndUnsetEachEmitTheCommittedChange` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/provider_test.go:148` | packages/credentials/credentials/tests/credentials.spec.ts | 67-75 | — | `TestARefusedSetAndAnAbsentUnsetStaySilent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/provider_test.go:173` | packages/credentials/credentials/src/index.ts | 243-257 | — | `TestModifyRecordIsTheOnlyWritePathAndSeesTheCurrentValue` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/provider_test.go:222` | packages/credentials/credentials/src/index.ts | 243-257 | — | `TestAMutatorThatDeclinesChangesNothingAndStaysSilent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/provider_test.go:263` | packages/credentials/credentials/src/index.ts | 243-257 | — | `TestAFailingMutatorLeavesTheRecordAlone` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/provider_test.go:299` | packages/credentials/credentials/src/index.ts | 132-145 | — | `TestDescribeRecordCountsPresenceNotContent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/provider_test.go:337` | packages/credentials/credentials/src/index.ts | 233-241 | — | `TestListRecordsGivesAddressesAndKindsOnly` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/provider_test.go:381` | packages/credentials/credentials/src/index.ts | 259-263 | — | `TestDeleteRecordRemovesOnceAndIsSilentTheSecondTime` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/provider_test.go:412` | packages/credentials/credentials/src/types.ts | 20-28 | — | `TestTheTwoKeySpacesShareOneProviderWithoutCollidingOnTheWire` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/record.go:24` | packages/credentials/credentials/src/types.ts | 37-38 | — | `RecordKind` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/record_test.go:19` | packages/credentials/credentials/src/types.ts | 37-38 | — | `TestKindIsTheTypeItself` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/record_test.go:33` | packages/credentials/credentials/src/types.ts | 30-43 | — | `TestAnAPIKeyRecordSurvivesTheRoundTrip` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/record_test.go:95` | packages/credentials/credentials/src/types.ts | 45-56 | — | `TestAGrantPayloadComesBackByteForByte` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `credentials/record_test.go:174` | packages/credentials/credentials/src/types.ts | 58-59 | — | `TestUnmarshalRefusesAnUnknownDiscriminant` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/error_test.go:16` | packages/fs/fs/tests/service.spec.ts | 171-176 | — | `TestErrorCarriesAStableCode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/error_test.go:36` | packages/fs/fs/tests/service.spec.ts | 178-183 | — | `TestErrorChainsItsUnderlyingCause` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/error_test.go:55` | packages/fs/fs/src/types.ts | 190-203 | — | `TestErrorIsRoutableThroughAWrappedChain` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/error_test.go:76` | packages/fs/fs/src/types.ts | 190-203 | — | `TestErrorSatisfiesTheStructuralCoder` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/error_test.go:104` | packages/fs/fs/src/types.ts | 170-188 | — | `TestTheErrorVocabularyIsExactlyTheThirteenDshCodes` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/fs_test.go:55` | packages/fs/fs/tests/service.spec.ts | 86-95 | — | `TestTheSeamServesThePrimitives` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/fs_test.go:85` | packages/fs/fs/src/index.ts | 107-116 | — | `TestResolveJoinsRelativePathsOntoTheGivenBase` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/fs_test.go:118` | packages/fs/fs/src/index.ts | 118-135 | — | `TestProcessPathAndTargetKeyAreDifferentThings` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/fs_test.go:140` | packages/fs/fs/tests/service.spec.ts | 31-33 | — | `TestContainsAnswersSelfAndDescendants` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/fs_test.go:162` | packages/fs/fs/tests/service.spec.ts | 111-120 | — | `TestStreamTextYieldsExactlyWhatReadTextReturns` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/fs_test.go:203` | packages/fs/fs/src/index.ts | 178-187 | — | `TestStreamTextStopsBetweenChunksWhenCancelled` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/fs_test.go:261` | packages/fs/fs/src/index.ts | 178-187 | — | `TestStreamTextRefusesWhatReadTextRefuses` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/fs_test.go:277` | packages/fs/fs/src/index.ts | 170-176 | — | `TestReadTextRefusesBinaryContent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/fs_test.go:294` | packages/fs/fs/tests/service.spec.ts | 122-130 | — | `TestReadBytesEnforcesTheByteCap` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/fs_test.go:337` | packages/fs/fs/tests/service.spec.ts | 132-144 | — | `TestListDirGivesChildTargetsInAStableOrderWithoutContent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/fs_test.go:382` | packages/fs/fs/tests/service.spec.ts | 146-151 | — | `TestStatReportsAbsenceWithoutAnError` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/fs_test.go:403` | packages/fs/fs/src/types.ts | 79-80 | — | `TestStatNeverReportsASymlink` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/fs_test.go:444` | packages/fs/fs/tests/service.spec.ts | 153-160 | — | `TestLstatSeesTheLinkItselfBeforeAnyResolution` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/fs_test.go:483` | packages/fs/fs/tests/service.spec.ts | 72-76 | — | `TestAnUnconditionalWriteCreatesThenReplaces` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/fs_test.go:523` | packages/fs/fs/src/types.ts | 127-144 | — | `TestAnEmptyFileIsRealContentNotAMissingBaseline` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/fs_test.go:551` | packages/fs/fs/src/types.ts | 117-125 | — | `TestCreateIfAbsentRefusesAnExistingTarget` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/fs_test.go:577` | packages/fs/fs/src/types.ts | 117-125 | — | `TestReplaceIfVersionRefusesAStaleOrAbsentTarget` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/fs_test.go:614` | packages/fs/fs/src/types.ts | 146-168 | — | `TestEditTextReplacesExactlyOneLiteralMatch` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/fs_test.go:638` | packages/fs/fs/src/types.ts | 146-154 | — | `TestEditTextRefusesZeroOrAmbiguousMatches` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/fs_test.go:671` | packages/fs/fs/src/index.ts | 230-249 | — | `TestEditTextChecksTheVersionBeforeMatching` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/fs_test.go:695` | packages/fs/fs/src/types.ts | 156-168 | — | `TestEditingAnAbsentTargetIsAStaleVersion` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/invariant_test.go:98` | packages/fs/fs/tests/invariant.spec.ts | 21-36 | — | `TestUsableIdentitiesPassOnAllThreeChannels` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/invariant_test.go:124` | packages/fs/fs/tests/invariant.spec.ts | 38-47 | — | `TestAnEmptyTargetKeyIsAViolationOnEveryChannel` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/invariant_test.go:150` | packages/fs/fs/tests/invariant.spec.ts | 44-47 | — | `TestAnEmptyDisplayPathIsAViolation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/invariant_test.go:167` | packages/fs/fs/tests/invariant.spec.ts | 48-50 | — | `TestAnEmptyPresentVersionIsAViolation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/invariant_test.go:185` | packages/fs/fs/src/invariant.ts | 27-38 | — | `TestANilObservationIsAViolation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/invariant_test.go:230` | packages/fs/fs/src/invariant.ts | 20-48 | — | `TestUnregisteringStopsTheCheck` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/policy.go:17` | packages/fs/fs/src/index.ts | 50-58 | — | `WriteIntentDecider` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/policy.go:32` | packages/fs/fs/src/index.ts | 59-66 | — | `EditIntentDecider` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/policy.go:37` | packages/fs/fs/src/index.ts | 67-76 | — | `ObservationListener` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/policy.go:52` | packages/fs/fs/src/index.ts | 44-78 | — | `ApprovalPolicy` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `fs/policy.go:95` | packages/fs/fs/src/index.ts | 50-58 | — | `SubscribeWriteIntent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/policy.go:125` | packages/fs/fs/src/index.ts | 59-66 | — | `SubscribeEditIntent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/policy.go:151` | packages/fs/fs/src/index.ts | 67-76 | — | `SubscribeObserved` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/policy.go:177` | packages/fs/fs/src/index.ts | 50-58 | — | `DecideWriteIntent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/policy.go:210` | packages/fs/fs/src/index.ts | 59-66 | — | `DecideEditIntent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/policy.go:237` | packages/fs/fs/src/index.ts | 67-76 | — | `NotifyObserved` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/policy.go:267` | packages/fs/fs/src/invariant.ts | 14-18 | — | `checkTarget` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/policy.go:283` | packages/fs/fs/src/invariant.ts | 27-38 | — | `checkObservation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/policy_test.go:72` | packages/fs/fs/src/index.ts | 50-66 | — | `TestTheFirstDeciderWithAnAnswerOwnsTheDecision` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/policy_test.go:109` | packages/fs/fs/src/index.ts | 50-58 | — | `TestTheDecisionOrderFollowsRegistrationOrder` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/policy_test.go:142` | packages/fs/fs/src/index.ts | 50-66 | — | `TestADeciderFailureAbortsInsteadOfFallingBackToUnconditional` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/policy_test.go:207` | packages/fs/fs/src/index.ts | 50-76 | — | `TestTheActorIsPassedThroughUntouched` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/policy_test.go:234` | packages/fs/fs/src/index.ts | 67-76 | — | `TestObservationsFanOutToEveryRecorderAndReportTheFirstFailure` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/types.go:62` | packages/fs/fs/src/types.ts | 53 | — | `Present` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/types.go:75` | packages/fs/fs/src/types.ts | 54 | — | `Absent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/types.go:103` | packages/fs/fs/src/types.ts | 79-80 | — | `EntryType` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/types.go:211` | packages/fs/fs/src/types.ts | 124 | — | `CreateIfAbsent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/types.go:222` | packages/fs/fs/src/types.ts | 125 | — | `ReplaceIfVersion` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/types.go:232` | packages/fs/fs/src/types.ts | 66 | — | `EditIntent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/types.go:248` | packages/fs/fs/src/types.ts | 129-130 | — | `WriteOperation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/types_test.go:14` | packages/fs/fs/tests/service.spec.ts | 163-168 | — | `TestNamedTypesAreTheBrandFactories` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/types_test.go:37` | packages/fs/fs/src/types.ts | 47-54 | — | `TestAnObservationIsEitherPresentWithAVersionOrAbsentWithNone` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `fs/types_test.go:67` | packages/fs/fs/src/types.ts | 47-54 | — | `TestTheSealedSetsAreClosedAtCompileTime` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goal/fold.go:23` | packages/goal/goal/src/fold.ts | 58 | — | `maxSafeInteger` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goal/fold.go:84` | packages/goal/goal/src/invariant.ts | 17-26 | — | `freezeMessage` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `goal/goal/fold.go:146` | packages/goal/goal/src/fold.ts | 52-54 | — | `decodeObject` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goal/fold.go:193` | packages/goal/goal/src/fold.ts | 57-62 | — | `decodePositiveInteger` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goal/fold.go:204` | packages/goal/goal/src/fold.ts | 65-70 | — | `decodeNonNegativeInteger` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goal/fold.go:396` | packages/goal/goal/src/fold.ts | 175-183 | — | `parseSourceStrict` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goal/invariant.go:18` | packages/goal/goal/src/invariant.ts | 29-37 | — | `ValidateStream` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goal/projection.go:17` | packages/goal/goal/src/index.ts | 206 | — | `ProjectionKey` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goal/projection.go:22` | packages/goal/goal/src/index.ts | 211 | — | `projectionStateVersion` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goal/service.go:44` | packages/goal/goal/src/domain.ts | 105-115 | — | `ChangedObserver` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goal/service.go:99` | packages/goal/goal/src/index.ts | 127-133 | — | `cache` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goal/service.go:203` | packages/goal/goal/src/domain.ts | 105-115 | — | `OnChanged` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goal/service.go:512` | packages/goal/goal/src/index.ts | 393-398 | — | `prepare` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goal/service.go:546` | packages/goal/goal/src/index.ts | 421-434 | — | `cacheFor` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goal/service.go:576` | packages/goal/goal/src/index.ts | 437-447 | — | `sync` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goal/service.go:649` | packages/goal/goal/src/index.ts | 484-539 | — | `snapshotChange` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goal/service.go:673` | packages/goal/goal/src/index.ts | 557 | — | `emitChanged` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goal/service.go:770` | packages/goal/goal/src/index.ts | 136-139 | — | `resolvedCreate` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goal/types.go:45` | packages/goal/goal/src/domain.ts | 47 | — | `SourceKind` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goal/types.go:50` | packages/goal/goal/src/domain.ts | 66 | — | `EventChange` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goal/types.go:150` | packages/goal/goal/src/fold.ts | 76 | — | `blockCodePattern` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goal/types.go:357` | packages/goal/goal/src/domain.ts | 24-32 | — | `snapshotChangeJSON` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goal/types.go:370` | packages/goal/goal/src/domain.ts | 35-41 | — | `clearChangeJSON` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goal/types.go:409` | packages/goal/goal/src/fold.ts | 16-23 | — | `snapshotOperations` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalcommand/command.go:29` | packages/goal/command-goal/src/index.ts | 119 | — | `attachmentRole` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalcommand/command.go:37` | packages/goal/command-goal/src/index.ts | 180 | — | `stateRejection` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalcommand/command.go:45` | packages/goal/command-goal/src/index.ts | 17-25 | — | `commandKind` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalcommand/command.go:67` | packages/goal/command-goal/src/index.ts | 17-25 | — | `parsedCommand` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalcommand/command.go:81` | packages/goal/command-goal/src/index.ts | 33-44 | — | `parseCommand` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalcommand/command.go:116` | packages/goal/command-goal/src/index.ts | 42 | — | `afterEdit` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalcommand/command.go:135` | packages/goal/command-goal/src/index.ts | 126-186 | — | `runScheduleTransaction` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `goal/goalcommand/command.go:173` | packages/goal/command-goal/src/index.ts | 133-184 | — | `dispatch` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalcommand/command.go:263` | packages/goal/command-goal/src/index.ts | 145-149 | — | `createGoal` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalcommand/command.go:282` | packages/goal/command-goal/src/index.ts | 112-121 | — | `submitAttachments` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalcommand/config.go:30` | packages/goal/command-goal/src/index.ts | 191 | — | `CommandName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalcommand/config.go:58` | packages/goal/command-goal/src/index.ts | 189 | — | `AcpConfig` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `goal/goalcommand/config.go:85` | packages/goal/command-goal/src/index.ts | 189-196 | — | `PlanModeController` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `goal/goalrounddriver/driver.go:26` | packages/goal/goal-round-driver/src/index.ts | 32 | — | `attemptPhase` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/driver.go:73` | packages/goal/goal-round-driver/src/index.ts | 37-46 | — | `driver` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/driver.go:155` | packages/goal/goal-round-driver/src/index.ts | 215-225 | — | `loop` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/driver.go:170` | packages/goal/goal-round-driver/src/index.ts | 71-73 | — | `warn` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/driver.go:177` | packages/goal/goal-round-driver/src/index.ts | 98 | — | `isLive` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/driver.go:261` | packages/goal/goal-round-driver/src/index.ts | 156-162 | — | `consumeAttempt` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/driver.go:281` | packages/goal/goal-round-driver/src/index.ts | 137-205 | — | `driveOnce` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/driver.go:323` | packages/goal/goal-round-driver/src/index.ts | 166-172 | — | `blockRounds` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/driver.go:339` | packages/goal/goal-round-driver/src/index.ts | 174-204 | — | `queueRound` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/driver.go:377` | packages/goal/goal-round-driver/src/index.ts | 193-204 | — | `failToQueue` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/driver.go:409` | packages/goal/goal-round-driver/src/index.ts | 48-51 | — | `roundSource` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/driver.go:423` | packages/goal/goal-round-driver/src/index.ts | 127-128 | — | `goalAside` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/driver.go:475` | packages/goal/goal-round-driver/src/index.ts | 259-277 | — | `onStatus` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/driver.go:503` | packages/goal/goal-round-driver/src/index.ts | 264-274 | — | `pauseUnfinished` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/driver.go:522` | packages/goal/goal-round-driver/src/index.ts | 253-258 | — | `onSessionStart` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/driver.go:536` | packages/goal/goal-round-driver/src/index.ts | 278-282 | — | `onGoalChanged` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/driver.go:550` | packages/goal/goal-round-driver/src/index.ts | 284-291 | — | `onInboxInserted` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/driver.go:574` | packages/goal/goal-round-driver/src/index.ts | 292-298 | — | `onInboxClaimed` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/driver.go:585` | packages/goal/goal-round-driver/src/index.ts | 299-305 | — | `onInboxDiscarded` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/driver.go:599` | packages/goal/goal-round-driver/src/index.ts | 307-331 | — | `toolCallUpdate` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `goal/goalrounddriver/driver.go:611` | packages/goal/goal-round-driver/src/index.ts | 312-316 | — | `onUserMessageEvent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/driver.go:629` | packages/goal/goal-round-driver/src/index.ts | 317-327 | — | `onTurnEndEvent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/driver.go:696` | packages/goal/goal-round-driver/src/index.ts | 363-367 | — | `dropReservation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/driver.go:751` | packages/goal/goal-round-driver/src/index.ts | 349-414 | — | `onPreStep` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/driver.go:808` | packages/goal/goal-round-driver/src/index.ts | 355-361 | — | `checkedValid` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/driver.go:823` | packages/goal/goal-round-driver/src/index.ts | 388-399 | — | `blockRejected` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/driver.go:852` | packages/goal/goal-round-driver/src/index.ts | 425-443 | — | `stop` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/driver.go:884` | packages/goal/goal-round-driver/src/index.ts | 308-310 | — | `owns` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/install.go:39` | packages/goal/goal-round-driver/src/index.ts | 19 | — | `Agents` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/install.go:63` | packages/goal/goal-round-driver/src/index.ts | 19 | — | `Goals` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/install.go:77` | packages/goal/goal-round-driver/src/index.ts | 19 | — | `Sessions` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/install.go:87` | packages/goal/goal-round-driver/src/index.ts | 19 | — | `AcpConfig` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `goal/goalrounddriver/install.go:101` | packages/goal/goal-round-driver/src/index.ts | 77 | — | `installation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/install.go:167` | packages/goal/goal-round-driver/src/index.ts | 246-414 | — | `observe` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/install.go:251` | packages/goal/goal-round-driver/src/index.ts | 80-94 | — | `driverFor` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/install.go:290` | packages/goal/goal-round-driver/src/index.ts | 252 | — | `onDisposed` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/install.go:306` | packages/goal/goal-round-driver/src/index.ts | 307-331 | — | `toolCallUpdate` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `goal/goalrounddriver/install.go:324` | packages/goal/goal-round-driver/src/index.ts | 349-414 | — | `onPreStep` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/install.go:335` | packages/goal/goal-round-driver/src/index.ts | 423-443 | — | `dispose` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/invariant.go:22` | packages/goal/goal-round-driver/src/invariant.ts | 47-61 | — | `ValidateStream` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/invariant.go:84` | packages/goal/goal-round-driver/src/invariant.ts | 29-44 | — | `reconstructView` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goalrounddriver/prompt.go:19` | packages/goal/goal-round-driver/src/prompt.ts | 19-25 | — | `roundInstruction` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/authority.go:52` | packages/goal/tool-goal/src/authority.ts | 20-22 | — | `AuthorityKind` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/authority.go:78` | packages/goal/tool-goal/src/authority.ts | 30-42 | — | `openTurn` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/config.go:31` | packages/goal/tool-goal/src/index.ts | 33 | — | `DefaultBlockedAfterConsecutiveRounds` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/config.go:120` | packages/goal/tool-goal/src/index.ts | 187-193 | — | `PlanModeController` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `goal/goaltool/error.go:25` | packages/goal/tool-goal/src/authority.ts | 25 | — | `CodeAuthorityRequired` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/error.go:32` | packages/goal/tool-goal/src/authority.ts | 53 | — | `CodeAgentRequired` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/error.go:37` | packages/goal/tool-goal/src/authority.ts | 35 | — | `CodeDriverRequired` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/error.go:42` | packages/goal/tool-goal/src/index.ts | 150 | — | `CodeInvalidUpdate` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/error.go:46` | packages/goal/tool-goal/src/index.ts | 304 | — | `CodeBlockThreshold` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/tool.go:59` | packages/goal/tool-goal/src/index.ts | 43 | — | `updateActions` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/tool.go:64` | packages/goal/tool-goal/src/index.ts | 310 | — | `blockCode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/tool.go:72` | packages/goal/tool-goal/src/index.ts | 45-49 | — | `createDescription` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/tool.go:80` | packages/goal/tool-goal/src/index.ts | 51-54 | — | `getDescription` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/tool.go:87` | packages/goal/tool-goal/src/index.ts | 236-239 | — | `updateDescription` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/tool.go:120` | packages/goal/goal/src/index.ts | 141-144 | — | `invalidMaxRounds` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/tool.go:155` | packages/goal/tool-goal/src/index.ts | 147 | — | `isSafeInteger` | 注释锚点 |  | 锚点符号在该上游文件里找不到 |
| `goal/goaltool/tool.go:218` | packages/goal/tool-goal/src/index.ts | 59-69 | — | `goalPayload` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/tool.go:231` | packages/goal/tool-goal/src/index.ts | 57-70 | — | `goalWire` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/tool.go:271` | packages/goal/tool-goal/src/index.ts | 72-110 | — | `goalValueSchema` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/tool.go:343` | packages/goal/tool-goal/src/index.ts | 176-179 | — | `goalOutput` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/tool.go:395` | packages/goal/tool-goal/src/index.ts | 195-205 | — | `newGetTool` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/tool.go:411` | packages/goal/tool-goal/src/index.ts | 200-203 | — | `readGoal` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/tool.go:433` | packages/goal/tool-goal/src/index.ts | 207-232 | — | `newCreateTool` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/tool.go:462` | packages/goal/tool-goal/src/index.ts | 222-229 | — | `createGoal` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/tool.go:496` | packages/goal/tool-goal/src/index.ts | 234-337 | — | `newUpdateTool` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/tool.go:542` | packages/goal/tool-goal/src/index.ts | 329 | — | `updateTitle` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/tool.go:562` | packages/goal/tool-goal/src/index.ts | 331-335 | — | `updateRawInput` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/tool.go:581` | packages/goal/tool-goal/src/index.ts | 257-326 | — | `runUpdate` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/tool.go:635` | packages/goal/tool-goal/src/index.ts | 264-271 | — | `runEdit` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/tool.go:660` | packages/goal/tool-goal/src/index.ts | 272-284 | — | `runSuspend` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/tool.go:690` | packages/goal/tool-goal/src/index.ts | 285-326 | — | `runWrapup` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/tool.go:738` | packages/goal/tool-goal/src/index.ts | 313-325 | — | `wrapupMessage` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/wrapup.go:21` | packages/goal/tool-goal/src/wrapup.ts | 22-27 | — | `completeInstruction` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `goal/goaltool/wrapup.go:31` | packages/goal/tool-goal/src/wrapup.ts | 32-38 | — | `blockedInstruction` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `guard/repeattoolreminder/reminder.go:24` | packages/guard/repeat-tool-reminder/src/index.ts | 57 | — | `pluginName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `guard/repeattoolreminder/reminder.go:42` | packages/guard/repeat-tool-reminder/src/index.ts | 46 | — | `defaultThresholds` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `guard/repeattoolreminder/reminder.go:47` | packages/guard/repeat-tool-reminder/src/index.ts | 128-146 | — | `ErrInvalidConfig` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `guard/repeattoolreminder/reminder.go:138` | packages/guard/repeat-tool-reminder/src/index.ts | 128-146 | — | `normalizeThresholds` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `guard/repeattoolreminder/reminder.go:226` | packages/guard/repeat-tool-reminder/src/index.ts | 229-232 | — | `NoticeStep` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `guard/repeattoolreminder/reminder.go:256` | packages/guard/repeat-tool-reminder/src/index.ts | 196-199 | — | `advance` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `guard/repeattoolreminder/text.go:19` | packages/guard/repeat-tool-reminder/src/index.ts | 63-67 | — | `gentleReminder` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `guard/repeattoolreminder/text.go:67` | packages/guard/repeat-tool-reminder/src/index.ts | 195 | — | `callKey` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `guard/repeattoolreminder/text.go:96` | packages/guard/repeat-tool-reminder/src/index.ts | 108-111 | — | `pattern` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `guard/timeoutpolicy/policy.go:34` | packages/guard/timeout-policy/src/index.ts | 47 | — | `errorName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `guard/timeoutpolicy/policy.go:85` | packages/guard/timeout-policy/src/index.ts | 44-51 | — | `timedOutResult` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/askuser/tool.go:22` | packages/interaction/tool-ask-user/src/index.ts | 21 | — | `ToolName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/askuser/tool.go:83` | packages/interaction/tool-ask-user/src/index.ts | 24-56 | — | `askArgs` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/askuser/tool.go:99` | packages/interaction/tool-ask-user/src/index.ts | 66-74 | — | `answerItem` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/askuser/tool.go:108` | packages/interaction/tool-ask-user/src/index.ts | 62-76 | — | `askValue` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/askuser/tool.go:122` | packages/interaction/tool-ask-user/src/index.ts | 28-55 | — | `questionNode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/askuser/tool.go:185` | packages/interaction/tool-ask-user/src/index.ts | 66-74 | — | `answerNode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/askuser/tool.go:204` | packages/interaction/tool-ask-user/src/index.ts | 20-100 | — | `definition` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/askuser/tool.go:276` | packages/interaction/tool-ask-user/src/index.ts | 82-90 | — | `toRequest` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/askuser/tool.go:305` | packages/interaction/tool-ask-user/src/index.ts | 92-98 | — | `toValue` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/commands/invariant.go:64` | packages/interaction/commands/src/invariant.ts | 23-47 | — | `defineDomain` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `interaction/commands/invariant.go:101` | packages/interaction/commands/src/invariant.ts | 37-46 | — | `resolves` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/commands/registry.go:38` | packages/interaction/commands/src/index.ts | 28 | — | `commandName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/commands/registry.go:43` | packages/interaction/commands/src/index.ts | 117 | — | `commandHead` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/commands/registry.go:165` | packages/interaction/commands/src/index.ts | 250-263 | — | `AgentOptions` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `interaction/commands/registry.go:187` | packages/interaction/commands/src/types.ts | 80 | — | `commands` | 注释锚点 |  | 锚点符号在该上游文件里找不到 |
| `interaction/commands/registry.go:377` | packages/interaction/commands/src/index.ts | 357-385 | — | `admit` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/commands/registry.go:533` | packages/interaction/commands/src/index.ts | 411-414 | — | `mint` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/commands/types.go:40` | packages/interaction/commands/src/types.ts | 27-34 | — | `ResultKind` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/commands/types.go:107` | packages/interaction/commands/src/types.ts | 65-70 | — | `SourceKind` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/commands/types.go:152` | packages/interaction/commands/src/types.ts | 96 | — | `RunData` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/commands/types.go:173` | packages/interaction/commands/src/types.ts | 103-108 | — | `DoneData` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/userapproval/invariant.go:59` | packages/interaction/user-approval/src/invariant.ts | 26-50 | — | `defineDomain` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `interaction/userapproval/service.go:163` | packages/interaction/user-approval/src/index.ts | 22-31 | — | `RegisterAnswerer` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/userapproval/service.go:207` | packages/interaction/user-approval/src/index.ts | 278-287 | — | `PolicyFor` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/userapproval/service.go:220` | packages/interaction/user-approval/src/index.ts | 219-237 | — | `SwitchPolicy` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/userapproval/service.go:333` | packages/interaction/user-approval/src/index.ts | 282-294 | — | `consult` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/userapproval/service.go:369` | packages/interaction/user-approval/src/index.ts | 283-284 | — | `answerers` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/userapproval/types.go:55` | packages/interaction/user-approval/src/index.ts | 143 | — | `KnownPolicy` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/userapproval/types.go:82` | packages/interaction/user-approval/src/index.ts | 290 | — | `KnownOutcome` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/userapproval/types.go:128` | packages/interaction/user-approval/src/index.ts | 44-49 | — | `AskedData` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/userapproval/types.go:148` | packages/interaction/user-approval/src/index.ts | 55-58 | — | `DecidedData` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/userapproval/types.go:158` | packages/interaction/user-approval/src/index.ts | 69-70 | — | `PolicySourceDelegation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/userapproval/types.go:163` | packages/interaction/user-approval/src/index.ts | 67-71 | — | `PolicyData` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/userapproval/types.go:189` | packages/interaction/user-approval/src/index.ts | 210-213 | — | `PolicyStatement` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/userquestions/question.go:46` | packages/interaction/user-questions/src/types.ts | 23-32 | — | `PlanReviewIntent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/userquestions/service.go:96` | packages/interaction/user-questions/src/index.ts | 37-40 | — | `UserQuestionProvider` | 裁决表+路径一致 |  | 锚点符号在该上游文件里找不到 |
| `interaction/userquestions/service.go:143` | packages/interaction/user-questions/src/index.ts | 58-75 | — | `RegisterProvider` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/userquestions/service.go:204` | packages/interaction/user-questions/src/index.ts | 99-113 | — | `checkCaller` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `interaction/userquestions/service.go:228` | packages/interaction/user-questions/src/index.ts | 114-135 | — | `checkIntents` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `invariants/invariants.go:87` | packages/runtime-diagnostics/invariants/src/index.ts | 153-188 | — | `Scope` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `invariants/invariants.go:164` | packages/runtime-diagnostics/invariants/tests/service.spec.ts | 304-309 | — | `ErrRegistryClosed` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `invariants/invariants.go:167` | packages/runtime-diagnostics/invariants/src/index.ts | 140-142 | — | `ErrAlreadyRegistered` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `invariants/invariants.go:170` | packages/runtime-diagnostics/invariants/src/index.ts | 137-139 | — | `ErrInvalidPackageName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `invariants/invariants.go:173` | packages/runtime-diagnostics/invariants/src/index.ts | 74-91 | — | `ErrInvalidConfig` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `invariants/invariants.go:384` | packages/runtime-diagnostics/invariants/tests/service.spec.ts | 304-309 | — | `Close` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobs/invariant.go:74` | packages/jobs/jobs/src/invariant.ts | 19-24 | — | `validateID` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobs/registry.go:18` | packages/jobs/jobs/src/index.ts | 120 | — | `KillResult` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobs/types.go:46` | packages/jobs/jobs/src/invariant.ts | 9 | — | `IsTerminal` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobs/types.go:64` | packages/jobs/jobs/src/types.ts | 24 | — | `KindBash` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobs/types.go:68` | packages/jobs/jobs/src/types.ts | 25 | — | `KindSubagent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobstool/notice.go:19` | packages/jobs/tool-jobs/src/index.ts | 153 | — | `noticeOmitted` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobstool/notice.go:24` | packages/jobs/tool-jobs/src/index.ts | 149 | — | `noticeAction` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobstool/notice.go:29` | packages/jobs/tool-jobs/src/index.ts | 252 | — | `outputOmitted` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobstool/notice.go:34` | packages/jobs/tool-jobs/src/index.ts | 181 | — | `resultOmitted` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobstool/notice.go:39` | packages/jobs/tool-jobs/src/index.ts | 246 | — | `noNewOutput` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobstool/snapshot.go:60` | packages/jobs/tool-jobs/src/index.ts | 74-78 | — | `statusNames` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobstool/snapshot.go:74` | packages/jobs/tool-jobs/src/index.ts | 66-83 | — | `publicJobSchema` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobstool/tool.go:49` | packages/jobs/tool-jobs/src/index.ts | 285 | — | `promptText` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobstool/tool.go:57` | packages/jobs/tool-jobs/src/index.ts | 289-292 | — | `outputDescription` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobstool/tool.go:64` | packages/jobs/tool-jobs/src/index.ts | 340 | — | `listDescription` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobstool/tool.go:69` | packages/jobs/tool-jobs/src/index.ts | 359 | — | `killDescription` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobstool/tool.go:87` | packages/jobs/tool-jobs/src/index.ts | 348 | — | `noJobs` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobstool/tool.go:101` | packages/jobs/tool-jobs/src/index.ts | 205-222 | — | `PlanModeController` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `jobs/jobstool/tool.go:154` | packages/jobs/tool-jobs/src/index.ts | 304-313 | — | `outputResult` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobstool/tool.go:162` | packages/jobs/tool-jobs/src/index.ts | 365-375 | — | `killOutcome` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobstool/tool.go:223` | packages/jobs/tool-jobs/src/index.ts | 232-236 | — | `captureOutputLimit` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobstool/tool.go:241` | packages/jobs/tool-jobs/src/index.ts | 238-240 | — | `takeOutputLimit` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobstool/tool.go:277` | packages/jobs/tool-jobs/src/index.ts | 241-253 | — | `splitRenderedOutput` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobstool/tool.go:303` | packages/jobs/tool-jobs/src/index.ts | 277-311 | — | `deliver` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobstool/tool.go:327` | packages/jobs/tool-jobs/src/index.ts | 305-309 | — | `spendWake` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobstool/tool.go:378` | packages/jobs/tool-jobs/src/index.ts | 225-231 | — | `refillWakeBudget` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobstool/tool.go:431` | packages/jobs/tool-jobs/src/index.ts | 287-338 | — | `newOutputTool` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobstool/tool.go:491` | packages/jobs/tool-jobs/src/index.ts | 326-334 | — | `readOutput` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobstool/tool.go:523` | packages/jobs/tool-jobs/src/index.ts | 328-329 | — | `waitFor` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobstool/tool.go:534` | packages/jobs/tool-jobs/src/index.ts | 339-357 | — | `newListTool` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobstool/tool.go:567` | packages/jobs/tool-jobs/src/index.ts | 352-355 | — | `listJobs` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobstool/tool.go:586` | packages/jobs/tool-jobs/src/index.ts | 358-401 | — | `newKillTool` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/jobstool/tool.go:647` | packages/jobs/tool-jobs/src/index.ts | 386-397 | — | `killJob` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/localjobs/registry.go:31` | packages/jobs/jobs-local/src/index.ts | 28 | — | `defaultMaxConcurrentJobsPerOwner` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/localjobs/registry.go:103` | packages/jobs/jobs-local/src/index.ts | 40-63 | — | `trackedJob` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/localjobs/registry.go:267` | packages/jobs/jobs-local/src/index.ts | 132-148 | — | `admit` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/localjobs/registry.go:313` | packages/jobs/jobs-local/src/index.ts | 178-185 | — | `collect` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/localjobs/registry.go:491` | packages/jobs/jobs-local/src/index.ts | 247-275 | — | `await` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/localjobs/registry.go:586` | packages/jobs/jobs-local/src/index.ts | 338-342 | — | `listenersFor` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/localjobs/registry.go:604` | packages/jobs/jobs-local/src/index.ts | 388-392 | — | `changedFor` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/localjobs/registry.go:622` | packages/jobs/jobs-local/src/index.ts | 345-360 | — | `reachLocked` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/localjobs/registry.go:646` | packages/jobs/jobs-local/src/index.ts | 363-377 | — | `snapshotOf` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/localjobs/registry.go:811` | packages/jobs/jobs-local/src/index.ts | 481-500 | — | `Dispose` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `jobs/localjobs/registry.go:923` | packages/jobs/jobs-local/src/index.ts | 470 | — | `awaitAll` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/adapter.go:46` | packages/llm/llm/src/index.ts | 192-199 | — | `ProviderDescriber` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/adapter.go:56` | packages/llm/llm/src/index.ts | 201-208 | — | `RetryPolicyOwner` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/adapter.go:66` | packages/llm/llm/src/index.ts | 210-219 | — | `ModelLister` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/adapter.go:77` | packages/llm/llm/src/index.ts | 221-236 | — | `ModelResolver` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/adapter.go:89` | packages/llm/llm/src/index.ts | 238-252 | — | `CallPreparer` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/adapter.go:114` | packages/llm/llm/src/index.ts | 197-199 | — | `AdapterProviderInfo` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/adapter.go:125` | packages/llm/llm/src/index.ts | 206-208 | — | `AdapterRetryPolicy` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/adapter.go:135` | packages/llm/llm/src/index.ts | 217-219 | — | `AdapterListModels` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/adapter.go:145` | packages/llm/llm/src/index.ts | 230-236 | — | `AdapterResolveModel` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/adapter.go:156` | packages/llm/llm/src/index.ts | 247-252 | — | `AdapterPrepareCall` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/apikey.go:51` | packages/llm/llm/src/api-key.ts | 15 | — | `legalAPIKeyRune` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/assembler.go:156` | packages/llm/llm/src/assembler.ts | 107-120 | — | `assembleBlock` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/assembler.go:294` | packages/llm/llm/src/assembler.ts | 180-183 | — | `Usage` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/assembler.go:301` | packages/llm/llm/src/assembler.ts | 185-188 | — | `Finish` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/assembler.go:313` | packages/llm/llm/src/assembler.ts | 190-197 | — | `ReplayState` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/assembler.go:324` | packages/llm/llm/src/assembler.ts | 204 | — | `AssemblerSource` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/attribution.go:31` | packages/llm/llm/src/attribution.ts | 16 | — | `AppIdentityVersion` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/config_test.go:31` | packages/llm/llm/src/call-config.ts | 41-59 | — | `TestCallConfigEqualsComparesEveryField` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/config_test.go:106` | packages/llm/llm/src/call-config.ts | 56-58 | — | `TestCallConfigEqualsTellsAbsentFromAnEmptyStopList` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/config_test.go:292` | packages/llm/llm/src/call-config.ts | 32-39 | — | `TestCallConfigAdapterDefaultsWireNamesFollowDSH` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/config_test.go:327` | packages/llm/llm/src/types.ts | 326-338 | — | `TestToolSchemaKeepsTheSchemaByteForByte` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/content_test.go:32` | packages/llm/llm/src/types.ts | 99-105 | — | `TestBlockTypeIsTheTypeItself` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/error.go:39` | packages/llm/llm/src/retry-policy.ts | 128 | — | `ErrInvalidConfig` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/error.go:69` | packages/llm/llm/src/index.ts | 93-117 | — | `SubagentError` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `llm/error.go:88` | packages/llm/llm/src/adapter-failure.ts | 72-77 | — | `Valid` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/error.go:138` | packages/llm/llm/src/adapter-failure.ts | 34 | — | `adapterFailureMessage` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/failurecode.go:53` | packages/llm/llm/src/error.ts | 50-55 | — | `structuredContextOverflow` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/failurecode.go:60` | packages/llm/llm/src/error.ts | 57-63 | — | `tooLargeForContext` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/failurecode.go:69` | packages/llm/llm/src/error.ts | 65-71 | — | `exceedsModelContext` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/failurecode.go:77` | packages/llm/llm/src/error.ts | 82 | — | `maxContextLength` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/failurecode.go:83` | packages/llm/llm/src/error.ts | 84 | — | `tooLongForModel` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/failurecode.go:88` | packages/llm/llm/src/error.ts | 94-100 | — | `quotaExhausted` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/generate.go:21` | packages/llm/llm/src/types.ts | 371-377 | — | `CallPurpose` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/generate.go:118` | packages/llm/llm/src/index.ts | 854 | — | `LlmCallConfig` | 裁决表+路径一致 |  | 锚点符号在该上游文件里找不到 |
| `llm/image.go:77` | packages/llm/llm/src/content.ts | 58-59 | — | `ImageRepresentation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/image.go:245` | packages/llm/llm/src/content.ts | 143-161 | — | `offloadRequestImages` | 裁决表+路径一致 |  | 锚点符号在该上游文件里找不到 |
| `llm/image_test.go:58` | packages/llm/llm/src/content.ts | 11-19 | — | `TestTextOnlyImageTextClampsInsteadOfPanicking` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/image_test.go:91` | packages/llm/llm/src/content.ts | 7-19 | — | `TestTheModelFacingTextIsTheEnglishVerbatim` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/image_test.go:109` | packages/llm/llm/src/content.ts | 21-28 | — | `TestRequestImageHandleTextUsesTheVersionsOwnDimensions` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/image_test.go:130` | packages/llm/llm/src/content.ts | 43-46 | — | `TestBase64LengthAgreesWithTheStandardLibrary` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/image_test.go:151` | packages/llm/llm/src/content.ts | 43-46 | — | `TestCeilDivStaysInIntegers` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/image_test.go:179` | packages/llm/llm/src/content.ts | 130-141 | — | `TestProjectImagesForTextModelReturnsTheSameHistoryWhenThereIsNoImage` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/image_test.go:296` | packages/llm/llm/src/content.ts | 143-161 | — | `TestOffloadRequestImagesKeepsEverythingWhenThereIsNoLimit` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/image_test.go:312` | packages/llm/llm/src/content.ts | 143-161 | — | `TestOffloadRequestImagesCountsTheBase64Payload` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/image_test.go:338` | packages/llm/llm/src/content.ts | 163-202 | — | `TestTheOffloadedPrefixIsStableAcrossRuns` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/image_test.go:362` | packages/llm/llm/src/content.ts | 163-202 | — | `TestTheDocumentedOffloadExampleHolds` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/image_test.go:393` | packages/llm/llm/src/content.ts | 163-202 | — | `TestTheByteQuantumAsymmetryIsDeliberate` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/image_test.go:427` | packages/llm/llm/src/content.ts | 48-62 | — | `TestZeroMaxImagesMeansThisRouteTakesNoImageAtAll` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/image_test.go:474` | packages/llm/llm/src/content.ts | 64-80 | — | `TestByteLengthOverridesTheAttachmentsOwnSize` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/invariant.go:87` | packages/llm/llm/src/invariant.ts | 89-103 | — | `registryViolation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/invariant.go:108` | packages/llm/llm/src/invariant.ts | 35-84 | — | `validateStream` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/invariant.go:197` | packages/llm/llm/src/invariant.ts | 14-19 | — | `validateChunkIndex` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/llmretry/history.go:113` | packages/llm/llm-retry/src/history.ts | 14-33 | — | `routedProvider` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/llmretry/history_test.go:13` | packages/llm/llm-retry/src/history.ts | 14-33 | — | `TestAnOpenStepReportsItsRoutedProvider` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/llmretry/history_test.go:38` | packages/llm/llm-retry/src/history.ts | 16-19 | — | `TestATurnEndAlsoClosesTheStep` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/llmretry/history_test.go:97` | packages/llm/llm-retry/src/history.ts | 24-31 | — | `TestTheProviderComesFromTheLastHeaderInTheWholeLog` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/llmretry/invariant.go:125` | packages/llm/llm-retry/src/invariant.ts | 52-56 | — | `Transition` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/llmretry/invariant.go:143` | packages/llm/llm-retry/src/invariant.ts | 26-171 | — | `defineDomain` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `llm/llmretry/invariant_test.go:38` | packages/llm/llm-retry/src/invariant.ts | 26-171 | — | `TestAWholeChainValidates` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/llmretry/invariant_test.go:328` | packages/llm/llm-retry/src/invariant.ts | 142-171 | — | `TestAStartedWithoutItsScheduleIsRejected` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/llmretry/invariant_test.go:403` | packages/llm/llm-retry/src/invariant.ts | 52-56 | — | `TestValidateLeavesTheTraceAlone` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/llmretry/invariant_test.go:434` | packages/llm/llm-retry/src/invariant.ts | 173-174 | — | `TestRegisteredInvariantsFireOnAlreadyLoadedHistory` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/llmretry/retry.go:67` | packages/llm/llm-retry/src/index.ts | 99-226 | — | `installation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/llmretry/types_test.go:18` | packages/llm/llm-retry/src/types.ts | 6-13 | — | `TestEventTypesAreTheDSHNamesVerbatim` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/llmretry/types_test.go:57` | packages/llm/llm-retry/src/types.ts | 15-40 | — | `TestMarshalRefusesANormalRetryWithoutAMaxRetries` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/llmretry/types_test.go:96` | packages/llm/llm-retry/src/types.ts | 15-40 | — | `TestAnAlwaysRetryHasNoMaxRetriesKeyOnTheWire` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/message.go:48` | packages/llm/llm/src/message.ts | 100-105 | — | `SourceKind` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/message.go:79` | packages/llm/llm/src/message.ts | 101 | — | `UserSource` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/message.go:89` | packages/llm/llm/src/message.ts | 102 | — | `PluginSource` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/message_test.go:17` | packages/llm/llm/src/message.ts | 100-105 | — | `TestSourceKindIsTheTypeItself` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/message_test.go:38` | packages/llm/llm/src/message.ts | 32-60 | — | `TestContextFormIsTheTypeItself` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/message_test.go:206` | packages/llm/llm/src/message.ts | 44-46 | — | `TestAnAbsentFormIsNotAnError` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/message_test.go:433` | packages/llm/llm/src/message.ts | 187-241 | — | `TestConstructorsPinTheNarrowing` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/message_test.go:508` | packages/llm/llm/src/message.ts | 145-156 | — | `TestTheReadSideRecoversTheNarrowing` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/message_test.go:643` | packages/llm/llm/src/message.ts | 114-123 | — | `TestBoundContextSummaryCountsRunes` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/behavior.go:75` | packages/test-support/llm-mock-server/src/index.ts | 83 | — | `BehaviorScriptExhausted` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/behavior.go:81` | packages/test-support/llm-mock-server/src/index.ts | 16-41 | — | `behaviorOrder` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/chunk.go:31` | packages/test-support/llm-mock-server/src/index.ts | 522 | — | `malformedChunk` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/cli.go:45` | packages/test-support/llm-mock-server/src/cli.ts | 30 | — | `ErrCLIHelp` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/cli.go:105` | packages/test-support/llm-mock-server/src/cli.ts | 119-138 | — | `cliStringOptions` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/cli.go:183` | packages/test-support/llm-mock-server/src/cli.ts | 152-212 | — | `buildCLIConfig` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/cli.go:310` | packages/test-support/llm-mock-server/src/cli.ts | 81-98 | — | `parseCLISequence` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/cli.go:348` | packages/test-support/llm-mock-server/src/cli.ts | 100-116 | — | `parseCLIRandomWeights` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/cli.go:374` | packages/test-support/llm-mock-server/src/cli.ts | 67-71 | — | `parseFloatArg` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/cli.go:388` | packages/test-support/llm-mock-server/src/cli.ts | 73-79 | — | `parseIntegerArg` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/cli_test.go:19` | packages/test-support/llm-mock-server/tests/cli.spec.ts | 8-11 | — | `TestCLIHelpNeedsNoSequence` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/cli_test.go:50` | packages/test-support/llm-mock-server/tests/cli.spec.ts | 13-55 | — | `TestCLIParsesEveryOption` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/cli_test.go:106` | packages/test-support/llm-mock-server/tests/cli.spec.ts | 57-78 | — | `TestCLIDefaults` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/cli_test.go:132` | packages/test-support/llm-mock-server/tests/cli.spec.ts | 72-78 | — | `TestCLIDefaultUnavailableInterval` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/cli_test.go:191` | packages/test-support/llm-mock-server/tests/cli.spec.ts | 80-100 | — | `TestCLIParsesWeightedRandomProfile` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/cli_test.go:232` | packages/test-support/llm-mock-server/tests/cli.spec.ts | 102-127 | — | `TestCLIRejectsInvalidArgv` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/event.go:46` | packages/test-support/llm-mock-server/src/index.ts | 80-86 | — | `RequestEvent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/event.go:62` | packages/test-support/llm-mock-server/src/index.ts | 80-86 | — | `MarshalJSON` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/event.go:87` | packages/test-support/llm-mock-server/src/index.ts | 87-94 | — | `ResultEvent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/event.go:105` | packages/test-support/llm-mock-server/src/index.ts | 87-94 | — | `MarshalJSON` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/exchange.go:160` | packages/test-support/llm-mock-server/src/index.ts | 473 | — | `hardClose` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/exchange.go:222` | packages/test-support/llm-mock-server/src/index.ts | 461-589 | — | `runScheduleTransaction` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `llm/mockserver/internal_test.go:20` | packages/test-support/llm-mock-server/src/index.ts | 306-311 | — | `TestSplitTextCountsCodePointsNotBytes` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/internal_test.go:55` | packages/test-support/llm-mock-server/src/index.ts | 299-304 | — | `TestSeededRandomIsReproducibleAndInRange` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/internal_test.go:83` | packages/test-support/llm-mock-server/src/index.ts | 602-615 | — | `TestChooseRandomBehaviorWalksTheWeights` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/internal_test.go:188` | packages/test-support/llm-mock-server/src/index.ts | 374-380 | — | `TestPauseReportsAClientThatAlreadyLeft` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/internal_test.go:224` | packages/test-support/llm-mock-server/src/index.ts | 434-437 | — | `TestStallReleasesWhenTheClientWalksAway` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/options.go:129` | packages/test-support/llm-mock-server/src/index.ts | 197-202 | — | `boundedDuration` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/options.go:232` | packages/test-support/llm-mock-server/src/index.ts | 253-266 | — | `resolveRandomWeights` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/options_test.go:17` | packages/test-support/llm-mock-server/tests/server.spec.ts | 335-354 | — | `TestStartRejectsInvalidOptions` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/random.go:36` | packages/test-support/llm-mock-server/src/index.ts | 228 | — | `generateSeed` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/server.go:194` | packages/test-support/llm-mock-server/src/index.ts | 648-698 | — | `ServeHTTP` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/mockserver/server.go:303` | packages/test-support/llm-mock-server/src/index.ts | 348-365 | — | `writeJSONError` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/modelinfo.go:235` | packages/llm/llm/src/index.ts | 314-317 | — | `ModelDiscovery` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/adapter.go:31` | packages/llm/llm-pi-ai/src/adapter.ts | 347 | — | `StreamIdleTimeoutCode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/adapter.go:350` | packages/llm/llm-pi-ai/src/adapter.ts | 155-166 | — | `resolveReasoning` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/adapter.go:392` | packages/llm/llm-pi-ai/src/adapter.ts | 322-411 | — | `streamWithSnapshot` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/adapter.go:577` | packages/llm/llm-pi-ai/src/adapter.ts | 400-407 | — | `failureOf` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/catalog.go:36` | packages/llm/llm-pi-ai/src/catalog.ts | 86-87 | — | `thinkingLevels` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/catalog.go:169` | packages/llm/llm-pi-ai/src/catalog.ts | 601-603 | — | `invalidRoute` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/config.go:409` | packages/llm/llm-pi-ai/src/config.ts | 387-462 | — | `resolveProfile` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/context.go:27` | packages/llm/llm-pi-ai/src/context.ts | 163 | — | `noToolOutput` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/context.go:36` | packages/llm/llm-pi-ai/src/context.ts | 127-134 | — | `requestContext` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/context.go:101` | packages/llm/llm-pi-ai/src/context.ts | 58-67 | — | `imageParts` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/context.go:120` | packages/llm/llm-pi-ai/src/context.ts | 48-85 | — | `userParts` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/context.go:159` | packages/llm/llm-pi-ai/src/context.ts | 83 | — | `collapseText` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/context.go:185` | packages/llm/llm-pi-ai/src/context.ts | 270-282 | — | `toolResultParts` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/context.go:380` | packages/llm/llm-pi-ai/src/context.ts | 270-282 | — | `toolResultMessages` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/context.go:406` | packages/llm/llm-pi-ai/src/context.ts | 242-283 | — | `convertHistory` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/discovery.go:33` | packages/llm/llm-pi-ai/src/discovery.ts | 38-41 | — | `listableAPI` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/discovery.go:43` | packages/llm/llm-pi-ai/src/discovery.ts | 50 | — | `maxDiscoveryResponseBytes` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/discovery.go:78` | packages/llm/llm-pi-ai/src/discovery.ts | 65-70 | — | `discoveryCapacity` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/discovery.go:123` | packages/llm/llm-pi-ai/src/discovery.ts | 98 | — | `discoveryFailed` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/discovery.go:130` | packages/llm/llm-pi-ai/src/discovery.ts | 96-131 | — | `readBoundedBody` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/discovery.go:216` | packages/llm/llm-pi-ai/src/discovery.ts | 212-218 | — | `discoveryEndpoint` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/discovery.go:237` | packages/llm/llm-pi-ai/src/discovery.ts | 240-241 | — | `discoveryAPIKey` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/install.go:26` | packages/llm/llm-pi-ai/src/index.ts | 90 | — | `AGENT_DEFAULT_MODEL_SETTINGS_NAMESPACE` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `llm/openaicompat/install.go:48` | packages/llm/llm-pi-ai/src/index.ts | 141 | — | `AgentOptions` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `llm/openaicompat/install.go:95` | packages/llm/llm-pi-ai/src/index.ts | 97-108 | — | `routeFact` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/install.go:200` | packages/llm/llm-pi-ai/src/index.ts | 141-320 | — | `installation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/install.go:229` | packages/llm/llm-pi-ai/src/index.ts | 156-163 | — | `currentProfiles` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/install.go:298` | packages/llm/llm-pi-ai/src/index.ts | 261-283 | — | `ensureRegistration` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/replay.go:18` | packages/llm/llm-pi-ai/src/replay.ts | 23 | — | `ReplayKind` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/replay.go:26` | packages/llm/llm-pi-ai/src/replay.ts | 24 | — | `ReplayVersion` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/replay.go:62` | packages/llm/llm-pi-ai/src/replay.ts | 121-123 | — | `replayStopReasons` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/replay.go:140` | packages/llm/llm-pi-ai/src/replay.ts | 237-249 | — | `ReplayStateOf` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/stream.go:63` | packages/llm/llm-pi-ai/src/stream.ts | 39-65 | — | `httpErrorCode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/stream.go:104` | packages/llm/llm-pi-ai/src/stream.ts | 49-63 | — | `transportWording` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/stream.go:115` | packages/llm/llm-pi-ai/src/stream.ts | 48 | — | `timeoutWording` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/stream.go:120` | packages/llm/llm-pi-ai/src/stream.ts | 39-65 | — | `classifyError` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/stream.go:306` | packages/llm/llm-pi-ai/src/stream.ts | 135-190 | — | `applyDelta` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/openaicompat/stream.go:376` | packages/llm/llm-pi-ai/src/stream.ts | 127-211 | — | `toStreamChunks` | 裁决表+路径一致 |  | 锚点符号在该上游文件里找不到 |
| `llm/replay/config.go:305` | packages/test-support/llm-replay/src/index.ts | 824-840 | — | `validateConfiguredModalities` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/replay/install.go:216` | packages/test-support/llm-replay/src/index.ts | 583-657 | — | `adapter` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/replay/script.go:153` | packages/test-support/llm-replay/src/index.ts | 29 | — | `packedRowTypes` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/replay/script.go:339` | packages/test-support/llm-replay/src/index.ts | 249-273 | — | `compactionEntry` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/replay/script.go:442` | packages/test-support/llm-replay/src/index.ts | 443-478 | — | `readEntry` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/retrypolicy.go:32` | packages/llm/llm/src/retry-policy.ts | 18-24 | — | `defaultRetryableCodes` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/retrypolicy.go:42` | packages/llm/llm/src/retry-policy.ts | 39 | — | `RetryMode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/retrypolicy.go:190` | packages/llm/llm/src/retry-policy.ts | 163-185 | — | `resolveNormal` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:34` | packages/llm/llm/src/index.ts | 373 | — | `InvalidAdapterCode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:39` | packages/llm/llm/src/index.ts | 407 | — | `DuplicateAdapterCode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:44` | packages/llm/llm/src/index.ts | 389 | — | `RegistrationDisposedCode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:49` | packages/llm/llm/src/index.ts | 473 | — | `InvalidDirectoryCode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:54` | packages/llm/llm/src/index.ts | 480 | — | `DuplicateDirectoryCode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:59` | packages/llm/llm/src/index.ts | 537 | — | `InvalidDiscoveryCode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:64` | packages/llm/llm/src/index.ts | 540 | — | `DuplicateDiscoveryCode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:69` | packages/llm/llm/src/index.ts | 565 | — | `NoDiscoveryCode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:74` | packages/llm/llm/src/index.ts | 873 | — | `NoAdapterCode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:79` | packages/llm/llm/src/index.ts | 623 | — | `InvalidCatalogCode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:84` | packages/llm/llm/src/index.ts | 681 | — | `InvalidModelInfoCode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:89` | packages/llm/llm/src/index.ts | 688 | — | `InvalidModelContextCode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:94` | packages/llm/llm/src/index.ts | 699 | — | `InvalidModelMaxTokensCode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:99` | packages/llm/llm/src/index.ts | 716 | — | `InvalidModelReasoningCode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:104` | packages/llm/llm/src/index.ts | 794 | — | `UnsupportedReasoningEffortCode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:110` | packages/llm/llm/src/index.ts | 852 | — | `InvalidPreparedCallCode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:115` | packages/llm/llm/src/index.ts | 1007 | — | `AbortedCode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:124` | packages/llm/llm/src/index.ts | 329 | — | `AdaptersUpdatedObserver` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:131` | packages/llm/llm/src/index.ts | 993-998 | — | `StreamRule` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:231` | packages/llm/llm/src/index.ts | 329 | — | `OnAdaptersUpdated` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:247` | packages/llm/llm/src/index.ts | 993-998 | — | `OnStream` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:398` | packages/llm/llm/src/index.ts | 267 | — | `Release` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:602` | packages/llm/llm/src/index.ts | 291 | — | `Release` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:675` | packages/llm/llm/src/index.ts | 495-500 | — | `withdraw` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:1033` | packages/llm/llm/src/index.ts | 161-162 | — | `LlmModelContext` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `llm/runtime.go:1184` | packages/llm/llm/src/index.ts | 893-972 | — | `adapterStream` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:1227` | packages/llm/llm/src/index.ts | 902-938 | — | `dispatchStream` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:1298` | packages/llm/llm/src/index.ts | 932 | — | `messagesHaveImage` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/runtime.go:1310` | packages/llm/llm/src/index.ts | 925-929 | — | `withCallConfig` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/stream.go:97` | packages/llm/llm/src/types.ts | 116-122 | — | `FinishKind` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/stream.go:279` | packages/llm/llm/src/types.ts | 312-324 | — | `ChunkType` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/stream_test.go:31` | packages/llm/llm/src/types.ts | 116-122 | — | `TestFinishKindIsTheReasonItself` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/stream_test.go:192` | packages/llm/llm/src/types.ts | 312-324 | — | `TestChunkTypeIsTheChunkItself` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/stream_test.go:399` | packages/client/ui-chat/src/client/conversation-nodes/event-projection.ts | 153-168 | — | `TestIsTokenDeltaOnlyCountsRealContent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/tokenmeter/breakdownprojection.go:18` | packages/llm/token-meter/src/breakdown-projection.ts | 56 | — | `ContextBreakdownProjectionKey` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/tokenmeter/breakdownprojection.go:23` | packages/llm/token-meter/src/breakdown-projection.ts | 57 | — | `contextBreakdownStateVersion` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/tokenmeter/breakdownprojection.go:71` | packages/llm/token-meter/src/breakdown-projection.ts | 60-80 | — | `applyContextBreakdown` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/tokenmeter/estimate.go:25` | packages/llm/token-meter/src/estimate.ts | 13 | — | `charsPerToken` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/tokenmeter/estimate.go:30` | packages/llm/token-meter/src/estimate.ts | 16 | — | `blockOverhead` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/tokenmeter/meter.go:48` | packages/llm/token-meter/src/index.ts | 38 | — | `stepMark` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/tokenmeter/meter.go:108` | packages/llm/token-meter/src/index.ts | 87-91 | — | `subagentIdentityProjectionDefinition` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `llm/tokenmeter/meter.go:227` | packages/llm/token-meter/src/index.ts | 160-181 | — | `sync` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/tokenmeter/meter.go:262` | packages/llm/token-meter/src/index.ts | 188-270 | — | `foldEvent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/tokenmeter/meter.go:391` | packages/llm/token-meter/src/index.ts | 277-310 | — | `estimateProviderAssistant` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/tokenmeter/types.go:20` | packages/llm/token-meter/src/types.ts | 19-22 | — | `BaselineKind` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/tokenmeter/usageprojection.go:18` | packages/llm/token-meter/src/usage-projection.ts | 120 | — | `TokenUsageProjectionKey` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/tokenmeter/usageprojection.go:23` | packages/llm/token-meter/src/usage-projection.ts | 175 | — | `ContextPressureProjectionKey` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/tokenmeter/usageprojection.go:28` | packages/llm/token-meter/src/usage-projection.ts | 121 | — | `tokenUsageStateVersion` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/tokenmeter/usageprojection.go:33` | packages/llm/token-meter/src/usage-projection.ts | 176 | — | `contextPressureStateVersion` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/tokenmeter/usageprojection.go:41` | packages/llm/token-meter/src/usage-projection.ts | 56-60 | — | `usageSample` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/tokenmeter/usageprojection.go:181` | packages/llm/token-meter/src/usage-projection.ts | 124-149 | — | `applyTokenUsage` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/tokenmeter/usageprojection.go:236` | packages/llm/token-meter/src/usage-projection.ts | 179-208 | — | `applyContextPressure` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `llm/tokenmeter/usageprojection.go:289` | packages/llm/token-meter/src/usage-projection.ts | 209-218 | — | `contextPressureViewOf` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `mcp/bridge.go:414` | packages/mcp/mcp-client/src/tools.ts | 322-334 | — | `noOutput` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `mcp/config.go:21` | packages/mcp/mcp-client/src/index.ts | 34 | — | `DefaultToolCallTimeout` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `mcp/config.go:26` | packages/mcp/mcp-client/src/index.ts | 37 | — | `serverNamePattern` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `mcp/connection.go:24` | packages/mcp/mcp-client/src/connection.ts | 50 | — | `generationCloseTimeout` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `mcp/connection.go:123` | packages/mcp/mcp-client/src/connection.ts | 327-349 | — | `stop` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `mcp/connection.go:231` | packages/mcp/mcp-client/src/connection.ts | 256-269 | — | `serve` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `mcp/connection.go:298` | packages/mcp/mcp-client/src/connection.ts | 192-225 | — | `planReconnect` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `mcp/connection.go:346` | packages/mcp/mcp-client/src/connection.ts | 216 | — | `backoff` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `mcp/content.go:39` | packages/mcp/mcp-client/src/tools.ts | 201-208 | — | `contentBlock` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `mcp/content.go:77` | packages/mcp/mcp-client/src/tools.ts | 525-527 | — | `unsupportedBlock` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `mcp/content.go:85` | packages/mcp/mcp-client/src/tools.ts | 61-66 | — | `imageMediaTypes` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `mcp/content.go:116` | packages/mcp/mcp-client/src/tools.ts | 201-208 | — | `normalizeBlock` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `mcp/content.go:150` | packages/mcp/mcp-client/src/tools.ts | 512-515 | — | `imageProjector` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `mcp/host.go:154` | packages/mcp/mcp-client/src/index.ts | 146-159 | — | `claim` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `mcp/host.go:177` | packages/mcp/mcp-client/src/connection.ts | 328-348 | — | `Close` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `mcp/naming.go:15` | packages/mcp/mcp-client/src/tools.ts | 49 | — | `maxPublicNameLength` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `mcp/naming.go:20` | packages/mcp/mcp-client/src/tools.ts | 55 | — | `hashLength` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `mcp/naming.go:25` | packages/mcp/mcp-client/src/tools.ts | 52 | — | `invalidNameChars` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/command.go:20` | packages/plan/plan-mode/src/index.ts | 302 | — | `offArgument` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/command.go:25` | packages/plan/plan-mode/src/index.ts | 296-339 | — | `commandDefinition` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/command.go:40` | packages/plan/plan-mode/src/index.ts | 300-338 | — | `runCommand` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/command.go:94` | packages/plan/plan-mode/src/index.ts | 306-320 | — | `offText` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/command.go:117` | packages/plan/plan-mode/src/index.ts | 325-328 | — | `commandContent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/controller.go:31` | packages/plan/plan-mode/src/index.ts | 244 | — | `sectionName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/controller.go:36` | packages/plan/plan-mode/src/index.ts | 245 | — | `sectionOrder` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/controller.go:96` | packages/plan/plan-mode/src/index.ts | 213 | — | `pendingIntent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/controller.go:324` | packages/plan/plan-mode/src/index.ts | 223-240 | — | `preStep` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/controller.go:414` | packages/plan/plan-mode/src/index.ts | 243-251 | — | `sectionText` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/fold.go:17` | packages/plan/plan-mode/src/index.ts | 93 | — | `headingPattern` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/fold.go:22` | packages/plan/plan-mode/src/index.ts | 129-138 | — | `foldPlanMode` | 裁决表+路径一致 |  | 锚点符号在该上游文件里找不到 |
| `plan/planmode/fold.go:29` | packages/plan/plan-mode/src/index.ts | 129-138 | — | `FoldModeUntil` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/fold.go:97` | packages/plan/plan-mode/src/index.ts | 186-195 | — | `modeAtLastHeader` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/projection.go:20` | packages/plan/plan-mode/src/index.ts | 263 | — | `ProjectionKey` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/projection.go:25` | packages/plan/plan-mode/src/index.ts | 290 | — | `projectionStateVersion` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/projection.go:34` | packages/plan/plan-mode/src/index.ts | 151 | — | `runningCommand` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/projection.go:94` | packages/plan/plan-mode/src/index.ts | 266-282 | — | `applyProjection` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/projection.go:142` | packages/plan/plan-mode/src/index.ts | 283-289 | — | `viewProjection` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/tool.go:23` | packages/plan/plan-mode/src/index.ts | 64 | — | `reviewID` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/tool.go:28` | packages/plan/plan-mode/src/index.ts | 65 | — | `approveLabel` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/tool.go:37` | packages/plan/plan-mode/src/index.ts | 66 | — | `keepPlanningLabel` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/tool.go:42` | packages/plan/plan-mode/src/index.ts | 74-78 | — | `exitDescription` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/tool.go:52` | packages/plan/plan-mode/src/index.ts | 364 | — | `planHeadingPattern` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/tool.go:67` | packages/plan/plan-mode/src/index.ts | 348-357 | — | `exitValue` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/tool.go:83` | packages/plan/plan-mode/src/index.ts | 342-430 | — | `exitDefinition` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/tool.go:119` | packages/plan/plan-mode/src/index.ts | 356 | — | `renderExit` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/tool.go:130` | packages/plan/plan-mode/src/index.ts | 419-424 | — | `presentExitCall` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/tool.go:149` | packages/plan/plan-mode/src/index.ts | 425-429 | — | `presentExitResult` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/tool.go:159` | packages/plan/plan-mode/src/index.ts | 358-418 | — | `executeExit` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/tool.go:229` | packages/plan/plan-mode/src/index.ts | 388-399 | — | `translateAskError` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/tool.go:245` | packages/plan/plan-mode/src/index.ts | 405-412 | — | `checkApproval` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/types.go:26` | packages/plan/plan-mode/src/index.ts | 297 | — | `CommandName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/types.go:31` | packages/plan/plan-mode/src/index.ts | 46-55 | — | `EventMode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `plan/planmode/types.go:53` | packages/plan/plan-mode/src/index.ts | 53 | — | `ModeData` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `preset/agentpresets/authoring.go:22` | packages/preset/agent-presets/src/authoring.ts | 23-33 | — | `InvalidPresetIdError` | 裁决表+路径一致 |  | 锚点符号在该上游文件里找不到 |
| `preset/agentpresets/authoring.go:62` | packages/preset/agent-presets/src/authoring.ts | 49-57 | — | `PresetNotWritableError` | 裁决表+路径一致 |  | 锚点符号在该上游文件里找不到 |
| `preset/agentpresets/authoring.go:117` | packages/preset/agent-presets/src/authoring.ts | 101-112 | — | `copyTree` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `preset/agentpresets/discovery.go:40` | packages/preset/agent-presets/src/discovery.ts | 66 | — | `compositionRow` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `preset/agentpresets/invariant.go:41` | packages/preset/agent-presets/src/invariant.ts | 60-71 | — | `checkJoined` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `preset/agentpresets/mount.go:122` | packages/preset/agent-presets/src/mount.ts | 332-381 | — | `mountComposition` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `preset/agentpresets/mount.go:229` | packages/preset/agent-presets/src/index.ts | 546-555 | — | `readCompositionStamp` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `preset/agentpresets/preset.go:32` | packages/preset/agent-presets/src/preset.ts | 18 | — | `presetID` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `preset/agentpresets/preset.go:168` | packages/preset/agent-presets/src/mount.ts | 425-431 | — | `ErrPresetMount` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `preset/agentpresets/roster.go:493` | packages/preset/agent-presets/src/index.ts | 392 | — | `forgetMount` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `preset/agentpresets/roster.go:566` | packages/preset/agent-presets/src/index.ts | 513-531 | — | `composeStanding` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `preset/agentpresets/session.go:16` | packages/preset/agent-presets/src/session.ts | 26 | — | `EventPresetSelected` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `preset/agentpresets/session.go:42` | packages/preset/agent-presets/src/session.ts | 26 | — | `SelectedData` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/domain.go:49` | packages/schedule/schedule/src/domain.ts | 110-112 | — | `decodeObject` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/domain.go:60` | packages/schedule/schedule/src/domain.ts | 119-124 | — | `exactKeys` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/domain.go:119` | packages/schedule/schedule/src/domain.ts | 390-393 | — | `decodePrompt` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/domain.go:142` | packages/schedule/schedule/src/domain.ts | 384-458 | — | `decodeRecord` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/domain.go:291` | packages/schedule/schedule/src/domain.ts | 487-503 | — | `decodeDispatchChange` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/domain.go:474` | packages/schedule/schedule/src/domain.ts | 651-654 | — | `normalizePrompt` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/domain.go:604` | packages/schedule/schedule/src/domain.ts | 795-797 | — | `DueEveryReminder` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/install.go:24` | packages/schedule/schedule/src/index.ts | 35 | — | `AcpConfig` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `schedule/schedule/install.go:53` | packages/schedule/schedule/src/index.ts | 41-42 | — | `installation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/install.go:125` | packages/schedule/schedule/src/index.ts | 45-67 | — | `onCreated` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/install.go:174` | packages/schedule/schedule/src/index.ts | 48-55 | — | `attach` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/install.go:221` | packages/schedule/schedule/src/index.ts | 51 | — | `usesSchedule` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/install.go:237` | packages/schedule/schedule/src/index.ts | 46 | — | `isRoot` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/install.go:249` | packages/schedule/schedule/src/index.ts | 56-64 | — | `tearDownOne` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/install.go:269` | packages/schedule/schedule/src/index.ts | 69-75 | — | `dispose` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/instant.go:16` | packages/schedule/schedule/src/domain.ts | 28 | — | `instantLayout` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/instant.go:24` | packages/schedule/schedule/src/domain.ts | 28 | — | `utcInstantPattern` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/instant.go:45` | packages/schedule/schedule/src/domain.ts | 198 | — | `FormatInstant` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/instant.go:55` | packages/schedule/schedule/src/domain.ts | 133-144 | — | `ParseInstant` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/instant.go:76` | packages/schedule/schedule/src/domain.ts | 146-154 | — | `calendarFields` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/instant.go:89` | packages/schedule/schedule/src/domain.ts | 308-329 | — | `fieldsOf` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/instant.go:104` | packages/schedule/schedule/src/domain.ts | 156-176 | — | `utcMillis` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/invariant.go:18` | packages/schedule/schedule/src/invariant.ts | 19 | — | `Stream` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/invariant.go:33` | packages/schedule/schedule/src/invariant.ts | 19-27 | — | `ValidateStream` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/runtime.go:52` | packages/schedule/schedule/src/runtime.ts | 29-32 | — | `decisionKind` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/runtime.go:280` | packages/schedule/schedule/src/runtime.ts | 140-146 | — | `loop` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/runtime.go:328` | packages/schedule/schedule/src/runtime.ts | 168-172 | — | `clearTimerLocked` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/runtime.go:386` | packages/schedule/schedule/src/runtime.ts | 71-74 | — | `warn` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/runtime.go:414` | packages/schedule/schedule/src/runtime.ts | 219-228 | — | `decideOrLog` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/runtime.go:497` | packages/schedule/schedule/src/runtime.ts | 260-311 | — | `dispatchUnderClaim` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/runtime.go:549` | packages/schedule/schedule/src/runtime.ts | 288-305 | — | `appendDispatch` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/timeparse.go:59` | packages/schedule/schedule/src/domain.ts | 178-180 | — | `fractionMillis` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/timeparse.go:73` | packages/schedule/schedule/src/domain.ts | 238-240 | — | `realCalendarTime` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/timeparse.go:189` | packages/schedule/schedule/src/domain.ts | 347 | — | `dstSampleDeltas` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/timeparse.go:261` | packages/schedule/schedule/src/domain.ts | 690-712 | — | `resolveAtInput` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/timeparse.go:285` | packages/schedule/schedule/src/domain.ts | 693-709 | — | `resolveLocalAtObject` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/tools.go:89` | packages/schedule/schedule/src/tools.ts | 38-44 | — | `sharedViewProperties` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/tools.go:112` | packages/schedule/schedule/src/tools.ts | 46-73 | — | `viewBranch` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/tools.go:137` | packages/schedule/schedule/src/tools.ts | 75 | — | `viewSchema` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/tools.go:165` | packages/schedule/schedule/src/tools.ts | 89-115 | — | `errorSchemas` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/tools.go:209` | packages/schedule/schedule/src/tools.ts | 117 | — | `createOutputSchema` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/tools.go:216` | packages/schedule/schedule/src/tools.ts | 118-123 | — | `listOutputSchema` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/tools.go:227` | packages/schedule/schedule/src/tools.ts | 124-145 | — | `deleteOutputSchema` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/tools.go:332` | packages/schedule/schedule/src/tools.ts | 216-228 | — | `toolErrorFrom` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/tools.go:350` | packages/schedule/schedule/src/tools.ts | 276 | — | `safeInteger` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/tools.go:382` | packages/schedule/schedule/src/tools.ts | 259-263 | — | `createArgKeys` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/tools.go:387` | packages/schedule/schedule/src/tools.ts | 267-271 | — | `selectorError` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/tools.go:397` | packages/schedule/schedule/src/tools.ts | 252-289 | — | `parseCreateArgs` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/tools.go:481` | packages/schedule/schedule/src/tools.ts | 299-304 | — | `toolSet` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/tools.go:493` | packages/schedule/schedule/src/tools.ts | 352 | — | `owns` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/tools.go:550` | packages/schedule/schedule/src/tools.ts | 186-196 | — | `serialize` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/tools.go:574` | packages/schedule/schedule/src/tools.ts | 317-397 | — | `newCreateTool` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/tools.go:618` | packages/schedule/schedule/src/tools.ts | 351-395 | — | `create` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/tools.go:671` | packages/schedule/schedule/src/tools.ts | 363-375 | — | `buildRecord` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/tools.go:691` | packages/schedule/schedule/src/tools.ts | 399-417 | — | `newListTool` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/tools.go:707` | packages/schedule/schedule/src/tools.ts | 404-414 | — | `list` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/tools.go:745` | packages/schedule/schedule/src/tools.ts | 419-455 | — | `newDeleteTool` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/tools.go:776` | packages/schedule/schedule/src/tools.ts | 426-452 | — | `delete` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/transaction.go:17` | packages/schedule/schedule/src/transaction.ts | 5 | — | `transactions` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/types.go:47` | packages/schedule/schedule/src/types.ts | 216-219 | — | `EventChange` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/types.go:150` | packages/schedule/schedule/src/types.ts | 13-50 | — | `afterWire` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `schedule/schedule/types.go:356` | packages/schedule/schedule/src/types.ts | 187-197 | — | `DomainErrorCode` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `schedule/schedule/types.go:406` | packages/schedule/schedule/src/types.ts | 208 | — | `CodeScheduleNotFound` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sdk/sdkprotocol/transport.go:49` | packages/sdk/protocol/src/transport.ts | 99-110 | — | `Handlers` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sdk/sdkprotocol/transport.go:71` | packages/sdk/protocol/src/transport.ts | 59 | — | `ErrMethodNotFound` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sdk/sdkprotocol/transport.go:91` | packages/sdk/protocol/src/transport.ts | 70-82 | — | `NewLineTransport` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sdk/sdkprotocol/transport.go:134` | packages/sdk/protocol/src/transport.ts | 226-238 | — | `dispatcher` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sdk/sdkprotocol/transport.go:195` | packages/sdk/protocol/src/transport.ts | 180-189 | — | `lineStream` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sdk/sdkprotocol/types.go:15` | packages/sdk/server/src/server.ts | 124 | — | `ServerName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sdk/sdkprotocol/types.go:124` | packages/sdk/protocol/src/types.ts | 63 | — | `AgentStatus` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sdk/sdkserver/config.go:32` | packages/sdk/server/src/server.ts | 124 | — | `ServerVersion` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sdk/sdkserver/config.go:38` | packages/sdk/server/src/server.ts | 237-239 | — | `ProviderLister` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sdk/sdkserver/config.go:52` | packages/sdk/server/src/server.ts | 120-123 | — | `MountAdapter` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sdk/sdkserver/config.go:129` | packages/sdk/server/src/server.ts | 65-69 | — | `Config` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `sdk/sdkserver/notify.go:22` | packages/sdk/server/src/server.ts | 70-103 | — | `subscribe` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sdk/sdkserver/notify.go:74` | packages/sdk/server/src/server.ts | 71-74 | — | `toolCallUpdate` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `sdk/sdkserver/notify.go:87` | packages/sdk/server/src/server.ts | 75-77 | — | `onAgentStatus` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sdk/sdkserver/notify.go:97` | packages/sdk/server/src/server.ts | 76 | — | `wireStatus` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sdk/sdkserver/notify.go:112` | packages/sdk/server/src/server.ts | 78-86 | — | `onSessionCreated` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sdk/sdkserver/notify.go:130` | packages/sdk/server/src/server.ts | 87-103 | — | `onSubagentEnd` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sdk/sdkserver/notify.go:156` | packages/sdk/server/src/server.ts | 43-46 | — | `runStatus` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sdk/sdkserver/server.go:81` | packages/sdk/server/src/server.ts | 70-103 | — | `SessionReferenceResolver` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `sdk/sdkserver/server.go:353` | packages/sdk/server/src/index.ts | 76-89 | — | `Handlers` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/checkpointpolicy/policy.go:92` | packages/session/session-checkpoint-policy/src/index.ts | 64-68 | — | `streamRule` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/checkpointpolicy/policy.go:118` | packages/session/session-checkpoint-policy/src/index.ts | 70-75 | — | `dispatchRule` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/checkpointpolicy/policy.go:148` | packages/session/session-checkpoint-policy/src/index.ts | 77-82 | — | `preStepRule` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/chunkrow.go:39` | packages/core/session/src/chunk-rows.ts | 77 | — | `minRun` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/eventdata.go:114` | packages/core/session/src/types.ts | 264 | — | `UserMessageData` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/eventdata.go:181` | packages/core/session/src/types.ts | 277 | — | `AssistantMessageData` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/eventdata.go:211` | packages/core/session/src/types.ts | 283 | — | `ToolCallData` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/eventdata.go:235` | packages/core/session/src/types.ts | 299 | — | `CorruptScheduleLogError` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `session/eventdata.go:245` | packages/core/session/src/types.ts | 295-301 | — | `ToolResultData` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/header.go:21` | packages/core/session/src/types.ts | 85 | — | `OriginSubagent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/header.go:82` | packages/core/session/src/types.ts | 179-194 | — | `TodoItem` | 裁决表+路径一致 |  | 锚点符号在该上游文件里找不到 |
| `session/header.go:125` | packages/core/session/src/request-header.ts | 21-31 | — | `MarshalJSON` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/invariant.go:43` | packages/core/session/src/invariant.ts | 198-205 | — | `NewTrace` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/invariant.go:114` | packages/core/session/src/invariant.ts | 55-166 | — | `defineDomain` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `session/invariant.go:280` | packages/core/session/src/invariant.ts | 138 | — | `isSyntheticNotStarted` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/backend.go:98` | packages/session/session-persistence/src/coordinator.ts | 146-172 | — | `SeekableBackend` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/backend.go:121` | packages/session/session-persistence/src/coordinator.ts | 200-206 | — | `LocatingBackend` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/backend.go:135` | packages/session/session-persistence/src/coordinator.ts | 208-213 | — | `ClosableBackend` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/coordinator.go:52` | packages/session/session-persistence/src/coordinator.ts | 600-604 | — | `sessions` | 注释锚点 |  | 锚点符号在该上游文件里找不到 |
| `session/persistence/coordinator.go:83` | packages/session/session-persistence/src/coordinator.ts | 589-604 | — | `CoordinatorDeps` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/coordinator.go:120` | packages/session/session-persistence/src/coordinator.ts | 237-241 | — | `liveState` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/coordinator.go:214` | packages/session/session-persistence/src/coordinator.ts | 589-620 | — | `NewCoordinator` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/coordinator.go:276` | packages/session/session-persistence/src/coordinator.ts | 1086-1137 | — | `SessionReferenceResolver` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `session/persistence/coordinator.go:338` | packages/session/session-persistence/src/coordinator.ts | 1091-1112 | — | `drain` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/coordinator.go:389` | packages/session/session-persistence/src/coordinator.ts | 1115-1117 | — | `onSessionCreated` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/coordinator.go:401` | packages/session/session-persistence/src/coordinator.ts | 1119-1121 | — | `toolCallUpdate` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `session/persistence/coordinator.go:408` | packages/session/session-persistence/src/coordinator.ts | 1123-1125 | — | `onSessionFlush` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/coordinator.go:415` | packages/session/session-persistence/src/coordinator.ts | 1127-1129 | — | `onSessionDisposed` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/coordinator_chain.go:19` | packages/session/session-persistence/src/coordinator.ts | 1010-1033 | — | `acquire` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/coordinator_chain.go:355` | packages/session/session-persistence/src/coordinator.ts | 900-930 | — | `wrapCorruption` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/coordinator_prepare.go:131` | packages/session/session-persistence/src/coordinator.ts | 791-818 | — | `inspectOnce` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/coordinator_prepare.go:184` | packages/session/session-persistence/src/coordinator.ts | 728 | — | `preparationLoaderFor` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/coordinator_prepare.go:207` | packages/session/session-persistence/src/coordinator.ts | 729 | — | `committerFor` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/coordinator_prepare.go:250` | packages/session/session-persistence/src/coordinator.ts | 895-923 | — | `buildPreparedSource` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/coordinator_write.go:159` | packages/session/session-persistence/src/coordinator.ts | 1239-1270 | — | `reconcileTracked` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/coordinator_write.go:396` | packages/session/session-persistence/src/coordinator.ts | 1341-1352 | — | `newWriteBehind` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/error.go:113` | packages/session/session-persistence/src/coordinator.ts | 1067-1074 | — | `WithLocation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/preparations.go:22` | packages/session/session-persistence/src/coordinator.ts | 562-586 | — | `preparedSource` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/preparations.go:45` | packages/session/session-persistence/src/preparations.ts | 78 | — | `commitResult` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/preparations.go:357` | packages/session/session-persistence/src/preparations.ts | 199 | — | `discardOutcome` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/preparations.go:473` | packages/session/session-persistence/src/preparations.ts | 266-274 | — | `makeReadyLocked` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/preparations.go:485` | packages/session/session-persistence/src/preparations.ts | 276-283 | — | `removeLocked` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/preparations.go:497` | packages/session/session-persistence/src/preparations.ts | 285-298 | — | `touchLocked` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/stored.go:20` | packages/session/session-persistence/src/coordinator.ts | 1078-1082 | — | `CheckStoredIdentity` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/stored.go:36` | packages/session/session-persistence/src/coordinator.ts | 1044-1049 | — | `CheckStoredVersion` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/stored.go:53` | packages/session/session-persistence/src/coordinator.ts | 1051-1065 | — | `CheckStoredVocabulary` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/stored.go:81` | packages/session/session-persistence/src/coordinator.ts | 884-889 | — | `CheckStored` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/stored.go:122` | packages/session/session-persistence/src/coordinator.ts | 900-902 | — | `BalanceStored` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/writebehind.go:105` | packages/session/session-persistence/src/write-behind.ts | 33-38 | — | `HasWork` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/writebehind.go:220` | packages/session/session-persistence/src/write-behind.ts | 80-83 | — | `armTimerLocked` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/persistence/writebehind.go:234` | packages/session/session-persistence/src/write-behind.ts | 85-90 | — | `cancelTimerLocked` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/projection/registry.go:286` | packages/session/session-projection/src/index.ts | 463-471 | — | `cellForLocked` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/projectioncache/cache.go:322` | packages/session/session-projection-cache/src/index.ts | 200-219 | — | `Observe` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/projectioncache/cache.go:363` | packages/session/session-projection-cache/src/index.ts | 225-229 | — | `Detach` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/projectioncache/cache.go:385` | packages/session/session-projection-cache/src/index.ts | 231-237 | — | `Close` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/projectioncache/cache.go:407` | packages/session/session-projection-cache/src/index.ts | 216-218 | — | `onInterval` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/projectioncache/cache.go:424` | packages/session/session-projection-cache/src/index.ts | 240-251 | — | `writeSoft` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/projectioncache/cache.go:477` | packages/session/session-projection-cache/src/index.ts | 273-280 | — | `putSoft` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/projectioncache/cache.go:490` | packages/session/session-projection-cache/src/index.ts | 176 | — | `lastSeq` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/projectioncache/record.go:19` | packages/session/session-projection-cache/src/spec.ts | 67 | — | `DomainName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/projectioncache/record.go:24` | packages/session/session-projection-cache/src/spec.ts | 68 | — | `DomainVersion` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/projectioncache/record.go:32` | packages/session/session-projection-cache/src/spec.ts | 69 | — | `TableName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/projectioncache/record.go:77` | packages/session/session-projection-cache/src/spec.ts | 24-28 | — | `ValidateRecord` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/sessiontitle/normalize.go:28` | packages/session/session-title/src/normalize.ts | 4 | — | `oscSequence` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/sessiontitle/normalize.go:44` | packages/session/session-title/src/normalize.ts | 6 | — | `csiSequence` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/sessiontitle/normalize.go:49` | packages/session/session-title/src/normalize.ts | 8 | — | `escSequence` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/sessiontitle/normalize.go:54` | packages/session/session-title/src/normalize.ts | 10 | — | `controlCharacter` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/sessiontitle/normalize.go:63` | packages/session/session-title/src/normalize.ts | 12 | — | `directionalControl` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/sessiontitle/projection.go:17` | packages/session/session-title/src/index.ts | 311 | — | `ProjectionKey` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/sessiontitle/projection.go:22` | packages/session/session-title/src/index.ts | 316 | — | `projectionStateVersion` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/sessiontitle/projection.go:43` | packages/session/session-title/src/index.ts | 315 | — | `TitleProjection` | 裁决表+路径一致 |  | 锚点符号在该上游文件里找不到 |
| `session/sessiontitle/projection.go:75` | packages/session/session-title/src/index.ts | 314 | — | `applyTitle` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/sessiontitle/projection.go:97` | packages/session/session-title/src/index.ts | 308-318 | — | `goalProjectionDefinition` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `session/sessiontitle/service.go:83` | packages/session/session-title/src/index.ts | 233-237 | — | `pendingWork` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/sessiontitle/service.go:94` | packages/session/session-title/src/index.ts | 240-243 | — | `activeWork` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/sessiontitle/service.go:105` | packages/session/session-title/src/index.ts | 246-251 | — | `workState` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/sessiontitle/service.go:149` | packages/session/session-title/src/index.ts | 292-302 | — | `Close` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/sessiontitle/service.go:360` | packages/session/session-title/src/index.ts | 320-331 | — | `OnEvent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/sessiontitle/service.go:417` | packages/session/session-title/src/index.ts | 336-341 | — | `OnSessionDisposed` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/sessiontitle/service.go:514` | packages/session/session-title/src/index.ts | 519-539 | — | `startPendingLocked` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/sessiontitle/service.go:683` | packages/session/session-title/src/index.ts | 651-663 | — | `activateLocked` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/sessiontitle/service.go:768` | packages/session/session-title/src/index.ts | 756-790 | — | `ensureFallbackLocked` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/sessiontitle/service.go:855` | packages/session/session-title/src/index.ts | 424-425 | — | `routeOf` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/sessiontitle/service.go:873` | packages/session/session-title/src/index.ts | 509 | — | `lastStepBoundary` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/sessiontitle/types.go:24` | packages/session/session-title/src/index.ts | 94-102 | — | `EventSessionTitle` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/sessiontitle/types.go:56` | packages/session/session-title/src/index.ts | 40-45 | — | `SessionTitleModelProvenance` | 裁决表+路径一致 |  | 锚点符号在该上游文件里找不到 |
| `session/sessiontitle/types.go:69` | packages/session/session-title/src/index.ts | 48-58 | — | `SourceKind` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/sessiontitle/types.go:150` | packages/session/session-title/src/index.ts | 115-120 | — | `SessionTitleUserMessage` | 裁决表+路径一致 |  | 锚点符号在该上游文件里找不到 |
| `session/sessiontitle/types.go:272` | packages/session/session-title/src/index.ts | 279-289 | — | `defineDomain` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `session/sessiontitlellm/provider.go:198` | packages/session/session-title-llm/src/index.ts | 270-286 | — | `collectText` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/sessiontitlellm/types.go:22` | packages/session/session-title-llm/src/index.ts | 40-45 | — | `EventTitleLLMRequest` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/stats/stats.go:83` | packages/session/session-stats/src/projection.ts | 56-63 | — | `ScheduleState` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `session/stats/stats.go:106` | packages/session/session-stats/src/projection.ts | 88-97 | — | `defineDomain` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `session/surface.go:19` | packages/core/session/src/surface.ts | 15-19 | — | `surfaceEventTypes` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/surfaceop.go:43` | packages/core/session/src/types.ts | 367-368 | — | `AppendOp` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/surfaceop.go:59` | packages/core/session/src/types.ts | 369-374 | — | `ReplaceOp` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/surfaceop.go:90` | packages/core/session/src/surface.ts | 172-208 | — | `UnmarshalSurfaceOp` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/telemetry/coordinator.go:51` | packages/session/session-telemetry/src/coordinator.ts | 66-72 | — | `AgentOptions` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `session/telemetry/coordinator.go:113` | packages/session/session-telemetry/src/coordinator.ts | 73-77 | — | `Config` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `session/telemetry/coordinator.go:178` | packages/session/session-telemetry/src/coordinator.ts | 122-134 | — | `CaptureThrough` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/telemetry/coordinator.go:185` | packages/session/session-telemetry/src/coordinator.ts | 96-100 | — | `Observe` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/telemetry/coordinator.go:235` | packages/session/session-telemetry/src/coordinator.ts | 86-93 | — | `Retire` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/telemetry/coordinator.go:264` | packages/session/session-telemetry/src/coordinator.ts | 225-243 | — | `RelayError` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/telemetry/coordinator.go:301` | packages/session/session-telemetry/src/coordinator.ts | 110-121 | — | `Close` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/telemetry/coordinator.go:340` | packages/session/session-telemetry/src/coordinator.ts | 135-146 | — | `replay` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/telemetry/coordinator.go:592` | packages/session/session-telemetry/src/coordinator.ts | 166 | — | `chunkKey` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/telemetry/record.go:194` | packages/session/session-telemetry/src/index.ts | 104-116 | — | `Flusher` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/telemetry/record.go:214` | packages/session/session-telemetry/src/index.ts | 22-42 | — | `Rule` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/turnend.go:178` | packages/core/session/src/types.ts | 296-311 | — | `UnmarshalTurnEndReason` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/turnend.go:240` | packages/core/session/src/types.ts | 293 | — | `CancelLegacy` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/turnend.go:338` | packages/core/session/src/types.ts | 277-294 | — | `UnmarshalTurnEndCancelCause` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/vocabulary.go:15` | packages/core/session/src/known-event-types.ts | 1-68 | — | `Vocabulary` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `session/vocabulary.go:91` | packages/core/session/src/known-event-types.ts | 1-30 | — | `CheckVocabulary` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/corpus.go:288` | packages/session-query/session-query/src/corpus.ts | 195-219 | — | `projectPending` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/corpus.go:344` | packages/session-query/session-query/src/corpus.ts | 167-194 | — | `resolvePending` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/corpus.go:426` | packages/session-query/session-query/src/corpus.ts | 259 | — | `abortedFailure` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/corpus.go:442` | packages/session-query/session-query/src/corpus.ts | 292-297 | — | `detach` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/corpus.go:466` | packages/session-query/session-query/src/corpus.ts | 133 | — | `uniqueIDs` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/corpus.go:482` | packages/session-query/session-query/src/corpus.ts | 299-301 | — | `compareRecords` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/engine.go:25` | packages/session-query/session-query/src/index.ts | 81-127 | — | `Searcher` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/engine.go:78` | packages/session-query/session-query/src/index.ts | 87-105 | — | `Config` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `sessionquery/engine.go:289` | packages/session-query/session-query/src/index.ts | 347-356 | — | `checkWindow` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/filters.go:302` | packages/session-query/session-query/src/filters.ts | 209-225 | — | `assertAllowed` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/querytool/access.go:190` | packages/session-query/tool-session-query/src/workspace-access.ts | 148 | — | `untitledText` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/querytool/boundary.go:18` | packages/session-query/tool-session-query/src/service-boundary.ts | 14-17 | — | `safeFailure` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/querytool/boundary.go:28` | packages/session-query/tool-session-query/src/service-boundary.ts | 21-87 | — | `safeFailures` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/querytool/boundary.go:65` | packages/session-query/tool-session-query/src/service-boundary.ts | 140 | — | `genericFailureText` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/querytool/error.go:28` | packages/session-query/tool-session-query/src/service-boundary.ts | 90 | — | `CodeUnauthorized` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/querytool/error.go:46` | packages/session-query/tool-session-query/src/service-boundary.ts | 139-142 | — | `CodeFailed` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/querytool/input.go:49` | packages/session-query/tool-session-query/src/input.ts | 36-43 | — | `eventSearchArgs` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/querytool/input.go:63` | packages/session-query/tool-session-query/src/input.ts | 84-86 | — | `sessionTargetArgs` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/querytool/input.go:70` | packages/session-query/tool-session-query/src/index.ts | 98-101 | — | `eventTargetArgs` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/querytool/input.go:78` | packages/session-query/tool-session-query/src/index.ts | 111-115 | — | `eventReadArgs` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/querytool/input.go:277` | packages/session-query/tool-session-query/src/input.ts | 186-187 | — | `isoTimestamp` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/querytool/input.go:447` | packages/session-query/tool-session-query/src/input.ts | 129-141 | — | `invalidQuery` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/querytool/input.go:454` | packages/session-query/tool-session-query/src/input.ts | 283-290 | — | `assertNonNegative` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/querytool/input.go:467` | packages/session-query/tool-session-query/src/input.ts | 292-299 | — | `assertNonEmpty` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/querytool/operations.go:424` | packages/session-query/tool-session-query/src/operations.ts | 128 | — | `lastStepStart` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/querytool/presentation.go:34` | packages/session-query/tool-session-query/src/presentation.ts | 76 | — | `capNotice` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/querytool/tools.go:26` | packages/session-query/tool-session-query/src/index.ts | 61 | — | `SectionName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/querytool/tools.go:31` | packages/session-query/tool-session-query/src/index.ts | 62 | — | `SectionOrder` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/querytool/tools.go:54` | packages/session-query/tool-session-query/src/index.ts | 52-55 | — | `promptText` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/querytool/tools.go:64` | packages/session-query/tool-session-query/src/index.ts | 47-50 | — | `textOutput` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/querytool/tools.go:75` | packages/session-query/tool-session-query/src/index.ts | 49 | — | `renderText` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/querytool/tools.go:86` | packages/session-query/tool-session-query/src/index.ts | 91 | — | `concurrencySafe` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/querytool/tools.go:94` | packages/session-query/tool-session-query/src/input.ts | 84-86 | — | `sessionIDParameter` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/querytool/tools.go:107` | packages/session-query/tool-session-query/src/index.ts | 101 | — | `seqParameter` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/querytool/tools.go:120` | packages/session-query/tool-session-query/src/input.ts | 54 | — | `availabilityNames` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/querytool/tools.go:131` | packages/session-query/tool-session-query/src/input.ts | 64 | — | `surfaceNames` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/querytool/tools.go:255` | packages/session-query/tool-session-query/src/index.ts | 66-122 | — | `definitions` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/tracing.go:148` | packages/session-query/session-query/src/tracing.ts | 128-146 | — | `traceAncestors` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/tracing.go:171` | packages/session-query/session-query/src/tracing.ts | 148-156 | — | `groupChildren` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `sessionquery/tracing.go:277` | packages/session-query/session-query/src/tracing.ts | 66-72 | — | `eventAtSeq` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/invariant_test.go:150` | packages/settings/settings/tests/invariant.spec.ts | 18-27 | — | `TestInvariantCatchesACommitAfterTheServiceIsGone` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/json.go:104` | packages/settings/settings/src/index.ts | 200-202 | — | `PathOpKind` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/json_test.go:16` | packages/settings/settings/tests/settings.spec.ts | 518-528 | — | `TestCloneJSONShapedDetachesAndNormalizes` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/json_test.go:47` | packages/settings/settings/tests/settings.spec.ts | 482-488 | — | `TestCloneJSONShapedRejectsWhatJSONCannotHold` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/json_test.go:95` | packages/settings/settings/tests/settings.spec.ts | 647-654 | — | `TestCloneJSONShapedAcceptsOneObjectReferencedTwice` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/json_test.go:114` | packages/settings/settings/tests/settings.spec.ts | 224-236 | — | `TestMergeLayersMergesObjectsAndReplacesEverythingElse` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/json_test.go:202` | packages/settings/settings/tests/settings.spec.ts | 845-900 | — | `TestApplyPathOpEditsOnePlace` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/json_test.go:278` | packages/settings/settings/tests/settings.spec.ts | 894-900 | — | `TestApplyPathOpRefusesANonObjectAtTheSectionRoot` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/memory_backend_test.go:25` | packages/settings/settings/tests/memory.ts | 11-31 | — | `memoryBackend` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/memory_backend_test.go:44` | packages/settings/settings/tests/memory.ts | 15 | — | `persistedCall` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider.go:21` | packages/settings/settings/src/index.ts | 388-423 | — | `PersistenceBackend` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `settings/provider.go:170` | packages/settings/settings/src/types.ts | 20-35 | — | `UpdatedListener` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider.go:181` | packages/settings/settings/src/types.ts | 37-48 | — | `DocumentListener` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider.go:274` | packages/settings/settings/src/index.ts | 370-386 | — | `Config` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `settings/provider.go:817` | packages/settings/settings/src/index.ts | 376-384 | — | `Close` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider.go:844` | packages/settings/settings/src/types.ts | 20-35 | — | `SubscribeUpdated` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider.go:869` | packages/settings/settings/src/types.ts | 37-48 | — | `SubscribeDocumentUpdated` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider.go:971` | packages/settings/settings/src/index.ts | 725-746 | — | `fanOut` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider.go:1034` | packages/settings/settings/src/index.ts | 686-694 | — | `readSection` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:20` | packages/settings/settings/tests/settings.spec.ts | 26-33 | — | `coreConfig` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:90` | packages/settings/settings/tests/settings.spec.ts | 89-97 | — | `TestRegisterResolvesDefaultsThenBaseThenUser` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:113` | packages/settings/settings/tests/settings.spec.ts | 123-153 | — | `TestRegisterFailsWhenTheStoredSectionIsUnserviceable` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:158` | packages/settings/settings/tests/settings.spec.ts | 136-142 | — | `TestRegisterRejectsADuplicateNamespaceLoud` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:190` | packages/settings/settings/tests/settings.spec.ts | 170-174 | — | `TestGetReadsNothingForAnUnregisteredNamespace` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:202` | packages/settings/settings/tests/settings.spec.ts | 184-210 | — | `TestUnregisterRemovesTheNamespaceAndIsIdempotent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:237` | packages/settings/settings/tests/settings.spec.ts | 212-223 | — | `TestUpdatePersistsTheUserSectionWithoutBakingInTheBaseLayer` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:263` | packages/settings/settings/tests/settings.spec.ts | 224-236 | — | `TestUpdateDeepMergesObjectsAndReplacesArrays` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:292` | packages/settings/settings/tests/settings.spec.ts | 237-255 | — | `TestUpdateCommitsNotifiesAndCarriesSourceUpdate` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:326` | packages/settings/settings/tests/settings.spec.ts | 256-268 | — | `TestUpdateRejectsAnInvalidPatchBeforePersistingAnything` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:357` | packages/settings/settings/tests/settings.spec.ts | 482-488 | — | `TestUpdateRejectsWhatJSONCannotHold` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:374` | packages/settings/settings/tests/settings.spec.ts | 518-528 | — | `TestUpdateSnapshotsThePatchAtCallTime` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:394` | packages/settings/settings/tests/settings.spec.ts | 294-307 | — | `TestWriteRejectsAnUnregisteredNamespaceAndAReadOnlyBackend` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:443` | packages/settings/settings/tests/settings.spec.ts | 376-390 | — | `TestReplaceRemovesOverridesWholesale` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:470` | packages/settings/settings/tests/settings.spec.ts | 830-844 | — | `TestMutateRemovesOneFieldWithoutTouchingASecretTheCallerNeverSaw` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:498` | packages/settings/settings/tests/settings.spec.ts | 845-855 | — | `TestMutateAppliesOpsInOrderAndRejectsAMalformedOne` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:539` | packages/settings/settings/tests/settings.spec.ts | 894-900 | — | `TestMutateRefusesANonObjectAtTheSectionRootLeavingTheStoredSectionAlone` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:557` | packages/settings/settings/tests/redact.spec.ts | 115-168 | — | `TestDescribeExposesEveryLayerAndRedactsOnDemand` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:625` | packages/settings/settings/tests/redact.spec.ts | 137-147 | — | `TestDescribeTreatsAMalformedStoredSectionAsHavingNoUserLayer` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:648` | packages/settings/settings/tests/settings.spec.ts | 530-580 | — | `TestPublishNotifiesWithSourceProviderAndKeepsTheLastGoodValuePerNamespace` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:690` | packages/settings/settings/tests/settings.spec.ts | 546-556 | — | `TestPublishStaysSilentWhenTheResolvedValueIsUnchanged` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:735` | packages/settings/settings/tests/settings.spec.ts | 967-994 | — | `TestRevisionTracksTheRawSectionNotTheResolvedValue` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:779` | packages/settings/settings/tests/settings.spec.ts | 995-1020 | — | `TestRevisionMovesForAnExternalEdit` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:803` | packages/settings/settings/tests/settings.spec.ts | 937-966 | — | `TestExpectedRevisionRefusesAStaleWrite` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:843` | packages/settings/settings/tests/settings.spec.ts | 333-343 | — | `TestConcurrentUpdatesSerializeSoNeitherPatchIsLost` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:881` | packages/settings/settings/tests/settings.spec.ts | 856-866 | — | `TestMutateReadsTheSectionAtTheFrontOfTheQueue` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:917` | packages/settings/settings/tests/settings.spec.ts | 676-685 | — | `TestWatchStopsAfterItsDisposerRuns` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:961` | packages/settings/settings/tests/settings.spec.ts | 344-355 | — | `TestAThrowingObserverIsContainedAndEveryOtherOneStillRuns` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:1009` | packages/settings/settings/tests/settings.spec.ts | 323-332 | — | `TestAnInvariantCodedFailurePropagatesAfterEveryObserverRan` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:1051` | packages/settings/settings/tests/settings.spec.ts | 443-460 | — | `TestCloseDrainsInFlightWritesAndRejectsLaterOnes` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/provider_test.go:1089` | packages/settings/settings/tests/settings.spec.ts | 404-442 | — | `TestAWriteQueuedBehindAnUnregistrationIsRejected` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/redact.go:87` | packages/settings/settings/src/redact.ts | 50-92 | — | `walkRedaction` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/redact.go:143` | packages/settings/settings/src/redact.ts | 57-72 | — | `walkStruct` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/redact_test.go:27` | packages/settings/settings/tests/redact.spec.ts | 9-18 | — | `redactAdapter` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/redact_test.go:37` | packages/settings/settings/tests/redact.spec.ts | 23-48 | — | `TestRedactStripsSecretsFromEveryContainer` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/redact_test.go:74` | packages/settings/settings/tests/redact.spec.ts | 50-57 | — | `TestRedactEnumeratesUnsetStructSlotsWithoutInventingContainers` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/redact_test.go:95` | packages/settings/settings/tests/redact.spec.ts | 59-68 | — | `TestRedactNeverMutatesTheInputAndPreservesUndeclaredKeys` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/redact_test.go:122` | packages/settings/settings/tests/redact.spec.ts | 70-80 | — | `TestRedactPassesMalformedContainersThrough` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/redact_test.go:146` | packages/settings/settings/tests/redact.spec.ts | 89-97 | — | `TestRedactDropsADictEntryWhoseWholeValueIsTheSecret` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/settings.go:74` | packages/settings/settings/src/index.ts | 19 | — | `namespacePattern` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/settings.go:82` | packages/settings/settings/src/index.ts | 28 | — | `ErrInvalidNamespace` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/settings.go:87` | packages/settings/settings/src/index.ts | 437 | — | `ErrAlreadyRegistered` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/settings.go:95` | packages/settings/settings/src/index.ts | 587 | — | `ErrNotRegistered` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/settings.go:100` | packages/settings/settings/src/index.ts | 593 | — | `ErrReadOnly` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/settings.go:105` | packages/settings/settings/src/index.ts | 590 | — | `ErrStopped` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/settings.go:110` | packages/settings/settings/src/index.ts | 607-608 | — | `ErrNotJSON` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/settings.go:115` | packages/settings/settings/src/index.ts | 691 | — | `ErrMalformedSection` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/settings.go:120` | packages/settings/settings/src/index.ts | 164-183 | — | `ErrConflict` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/settings.go:138` | packages/settings/settings/src/index.ts | 21-31 | — | `settingsNamespace` | 裁决表+路径一致 |  | 锚点符号在该上游文件里找不到 |
| `settings/settings.go:195` | packages/settings/settings/src/index.ts | 178 | — | `AttachmentError` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `settings/settings.go:206` | packages/settings/settings/src/index.ts | 137-157 | — | `deepEqualJson` | 裁决表+路径一致 |  | 锚点符号在该上游文件里找不到 |
| `settings/settings_test.go:15` | packages/settings/settings/tests/settings.spec.ts | 79-86 | — | `TestNewNamespaceBrandsKebabCase` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/settings_test.go:33` | packages/settings/settings/tests/settings.spec.ts | 83-85 | — | `TestNewNamespaceRejectsEveryOtherShape` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/settings_test.go:61` | packages/settings/settings/tests/settings.spec.ts | 308-320 | — | `TestDeepEqualJSONComparesJSONShapes` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `settings/settings_test.go:127` | packages/settings/settings/tests/settings.spec.ts | 950-958 | — | `TestConflictErrorCarriesBothRevisions` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/registry.go:26` | packages/skill/skill/src/index.ts | 708-768 | — | `ErrInvalidSkill` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/registry.go:100` | packages/skill/skill/src/index.ts | 334-338 | — | `newLayer` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/registry.go:188` | packages/skill/skill/src/index.ts | 374-378 | — | `NewRegistry` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/registry.go:639` | packages/skill/skill/src/index.ts | 541-547 | — | `storeCache` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/registry.go:689` | packages/skill/skill/src/index.ts | 634-642 | — | `scopeIDLocked` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/registry.go:703` | packages/skill/skill/src/index.ts | 644-646 | — | `collectCacheKeyLocked` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/registry.go:724` | packages/skill/skill/src/index.ts | 681-690 | — | `runtimeSkillProvider` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skill.go:18` | packages/skill/skill/src/index.ts | 20 | — | `namePattern` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skill.go:24` | packages/skill/skill/src/index.ts | 21 | — | `defaultCollectCacheMaxEntries` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skill.go:28` | packages/skill/skill/src/index.ts | 22 | — | `maxCollectAttempts` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skill.go:32` | packages/skill/skill/src/index.ts | 23 | — | `runtimeProvider` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skill.go:36` | packages/skill/skill/src/index.ts | 24 | — | `runtimeRank` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skill.go:79` | packages/skill/skill/src/index.ts | 42-45 | — | `ResourceBaseKind` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skill.go:108` | packages/skill/skill/src/index.ts | 43 | — | `DirectoryBase` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skill.go:121` | packages/skill/skill/src/index.ts | 44 | — | `URLBase` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skill.go:134` | packages/skill/skill/src/index.ts | 45 | — | `OpaqueBase` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skilltool/catalog.go:20` | packages/skill/tool-skill/src/index.ts | 40 | — | `CatalogEntry` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skilltool/catalog.go:104` | packages/skill/tool-skill/src/index.ts | 50-58 | — | `catalogEntries` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skilltool/config.go:24` | packages/skill/tool-skill/src/index.ts | 35 | — | `CatalogPlugin` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skilltool/config.go:37` | packages/skill/tool-skill/src/index.ts | 82 | — | `ToolName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skilltool/config.go:42` | packages/skill/tool-skill/src/index.ts | 27 | — | `DefaultCatalogDescriptionMaxLength` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skilltool/config.go:47` | packages/skill/tool-skill/src/index.ts | 79 | — | `MinCatalogDescriptionMaxLength` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skilltool/prestep.go:22` | packages/skill/tool-skill/src/index.ts | 409 | — | `gesturePattern` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skilltool/prestep.go:79` | packages/skill/tool-skill/src/index.ts | 162-204 | — | `invocationPreStep` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skilltool/prestep.go:156` | packages/skill/skill/src/index.ts | 149 | — | `InvocationPlugin` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skilltool/prestep.go:161` | packages/skill/tool-skill/src/index.ts | 206-251 | — | `catalogPreStep` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skilltool/prestep.go:319` | packages/skill/tool-skill/src/index.ts | 379-388 | — | `catalogMessageOn` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skilltool/prestep.go:341` | packages/skill/tool-skill/src/index.ts | 133 | — | `viewOptions` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skilltool/tool.go:31` | packages/skill/tool-skill/src/index.ts | 94-121 | — | `resourceBaseWire` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skilltool/tool.go:46` | packages/skill/tool-skill/src/index.ts | 148-155 | — | `toolOutput` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skilltool/tool.go:93` | packages/skill/tool-skill/src/index.ts | 94-121 | — | `resourceBaseSchema` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skilltool/tool.go:137` | packages/skill/tool-skill/src/index.ts | 81-160 | — | `newDefinition` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skilltool/tool.go:176` | packages/skill/tool-skill/src/index.ts | 125 | — | `renderSkillOutput` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skilltool/tool.go:198` | packages/skill/tool-skill/src/index.ts | 127-156 | — | `executeSkill` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skilltool/tool.go:257` | packages/skill/tool-skill/src/index.ts | 131-133 | — | `executionViewOptions` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `skill/skilltool/tool.go:279` | packages/skill/tool-skill/src/index.ts | 157-159 | — | `presentSkillCall` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `spill/policy/policy.go:24` | packages/spill/spill-policy/src/index.ts | 206 | — | `readToolName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `spill/policy/policy.go:32` | packages/spill/spill-policy/src/index.ts | 211 | — | `spillLabel` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `spill/policy/policy.go:76` | packages/spill/spill-policy/src/index.ts | 110-122 | — | `ApprovalPolicy` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `spill/policy/policy.go:125` | packages/spill/spill-policy/src/index.ts | 190-224 | — | `rule` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `spill/policy/policy.go:157` | packages/spill/spill-policy/src/index.ts | 196-198 | — | `shapes` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/backend.go:19` | packages/storage/storage/src/backend.ts | 9-10 | — | `unitNamePattern` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/backend.go:31` | packages/storage/storage/src/backend.ts | 9-10 | — | `ValidUnitName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/backend.go:54` | packages/storage/storage/src/backend.ts | 18-19 | — | `KVProvider` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/backend.go:155` | packages/storage/storage/src/backend.ts | 67-72 | — | `GoalSnapshot` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `storage/domain/domain.go:111` | packages/storage/storage-domain/src/index.ts | 158-167 | — | `RawRecord` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/domain.go:212` | packages/storage/storage-domain/src/domain.ts | 272-276 | — | `closedError` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/domain.go:250` | packages/storage/storage-domain/src/domain.ts | 104-108 | — | `TableOf` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/domain.go:487` | packages/storage/storage-domain/src/domain.ts | 92-94 | — | `GlobalOf` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/domain_test.go:158` | packages/storage/storage-domain/src/domain.ts | 307-313 | — | `TestAWriteIsDurableBeforeItIsVisible` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/domain_test.go:217` | packages/storage/storage-domain/src/domain.ts | 307-313 | — | `TestARejectedWriteChangesNothing` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/domain_test.go:255` | packages/storage/storage-domain/src/domain.ts | 315-330 | — | `TestARejectedDeleteChangesNothing` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/domain_test.go:289` | packages/storage/storage-domain/src/domain.ts | 315-330 | — | `TestDeletingAnAbsentKeyIsNotAChange` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/domain_test.go:344` | packages/storage/storage-domain/src/domain.ts | 332-346 | — | `TestUpdateIsAtomicAcrossConcurrentCallers` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/domain_test.go:383` | packages/storage/storage-domain/src/domain.ts | 332-346 | — | `TestUpdateOnAnAbsentKeyIsRefused` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/domain_test.go:421` | packages/storage/storage-domain/src/domain.ts | 307-313 | — | `TestUpdateChangesNothingWhenTheMediumRefuses` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/domain_test.go:459` | packages/storage/storage-domain/src/error.ts | 28-53 | — | `TestTheErrorTextCarriesBothTheReasonAndTheCode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/domain_test.go:511` | packages/storage/storage-domain/src/domain.ts | 50-61 | — | `TestKeysAndEntriesAreSortedSnapshots` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/domain_test.go:579` | packages/storage/storage-domain/src/domain.ts | 211-223 | — | `TestTableOfRefusesUnknownNamesAndWrongTypes` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/domain_test.go:619` | packages/storage/storage-domain/src/domain.ts | 20-34 | — | `TestTheGlobalStartsAtItsDeclaredInitialAndPersistsAfterSet` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/domain_test.go:712` | packages/storage/storage-domain/src/domain.ts | 226-244 | — | `TestCloseDrainsQueuedWritesAndRejectsLaterOnes` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/domain_test.go:791` | packages/storage/storage-domain/src/domain.ts | 110-118 | — | `TestCloseIsIdempotentAndFreesTheName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/domain_test.go:842` | packages/storage/storage-domain/src/domain.ts | 287-305 | — | `TestEveryReadRefusesAfterClose` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/domain_test.go:895` | packages/storage/storage-domain/src/index.ts | 158-167 | — | `TestRawReadsAreTheUntypedDiagnosticSurface` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/error.go:73` | packages/storage/storage-domain/src/index.ts | 180-192 | — | `invalidRecord` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/events.go:51` | packages/storage/storage-domain/src/events.ts | 36-48 | — | `JobsChangedListener` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `storage/domain/facility.go:40` | packages/storage/storage-domain/src/index.ts | 31-44 | — | `FormName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/facility.go:227` | packages/storage/storage-domain/src/index.ts | 85-90 | — | `reserve` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/facility.go:244` | packages/storage/storage-domain/src/index.ts | 107-140 | — | `load` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/facility.go:355` | packages/storage/storage-domain/src/index.ts | 141-145 | — | `onClosed` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/facility.go:367` | packages/storage/storage-domain/src/events.ts | 36-48 | — | `Subscribe` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/facility.go:401` | packages/storage/storage-domain/src/domain.ts | 246-261 | — | `emit` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/facility.go:450` | packages/storage/storage-domain/src/domain.ts | 255-260 | — | `warnListenerFailure` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/facility_test.go:25` | packages/storage/storage-domain/src/index.ts | 194-220 | — | `TestNewRequiresAHubAndABackendName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/facility_test.go:44` | packages/storage/storage-domain/src/index.ts | 53-61 | — | `TestTheRouteTableIsCopiedAtConstruction` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/facility_test.go:73` | packages/storage/storage-domain/src/index.ts | 91-98 | — | `TestRoutesOverrideTheDefaultBackend` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/facility_test.go:112` | packages/storage/storage-domain/src/index.ts | 85-90 | — | `TestOpeningTheSameNameTwiceIsRefused` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/facility_test.go:127` | packages/storage/storage-domain/src/index.ts | 99-106 | — | `TestOpeningRefusesABackendWithoutTheKeyValueFacet` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/facility_test.go:144` | packages/storage/storage-domain/src/error.ts | 6-12 | — | `TestOpeningFailsWhenTheRoutedBackendIsNotRegistered` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/facility_test.go:169` | packages/storage/storage-domain/src/index.ts | 169-172 | — | `TestABadSpecNeverTouchesTheMedium` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/facility_test.go:189` | packages/storage/storage-domain/src/index.ts | 107-140 | — | `TestOneBadRecordKeepsTheWholeDomainClosed` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/facility_test.go:225` | packages/storage/storage-domain/src/index.ts | 141-155 | — | `TestABadGlobalValueOnTheMediumKeepsTheDomainClosed` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/facility_test.go:268` | packages/storage/storage-domain/src/index.ts | 99-106 | — | `TestOpeningFailsWhenTheMediumIsStampedWithAnotherVersion` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/facility_test.go:295` | packages/storage/storage-domain/src/index.ts | 107-112 | — | `TestOpeningFailsWhenTheSnapshotCannotBeRead` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/facility_test.go:319` | packages/storage/storage-domain/src/index.ts | 141-145 | — | `TestAFailedOpenReleasesTheUnitAndTheName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/facility_test.go:355` | packages/storage/storage-domain/src/index.ts | 130-139 | — | `TestAFailedOpenSurfacesTheRecordErrorNotTheCleanupError` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/facility_test.go:383` | packages/storage/storage-domain/src/index.ts | 158-167 | — | `TestGetAndNamesOnlySeeFullyOpenedDomains` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/facility_test.go:421` | packages/storage/storage-domain/src/index.ts | 169-177 | — | `TestCloseAllClosesEveryDomainEvenIfOneFails` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/facility_test.go:466` | packages/storage/storage-domain/src/events.ts | 36-48 | — | `TestUnsubscribeStopsDeliveryAndIsIdempotent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/facility_test.go:516` | packages/storage/storage-domain/src/domain.ts | 246-261 | — | `TestAPanickingListenerDoesNotStopTheOthers` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/facility_test.go:551` | packages/storage/storage-domain/src/domain.ts | 246-261 | — | `TestAnInvariantFailureIsRethrownAfterEveryListenerRan` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/facility_test.go:600` | packages/storage/storage-domain/src/domain.ts | 255-260 | — | `TestListenerFailuresAreLoggedWithoutTheValue` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/invariant.go:93` | packages/storage/storage-domain/src/invariant.ts | 35-63 | — | `checkChange` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/invariant_test.go:73` | packages/storage/storage-domain/src/invariant.ts | 61-67 | — | `TestRegisterInvariantsRequiresAllThreePieces` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/invariant_test.go:124` | packages/storage/storage-domain/src/invariant.ts | 26-29 | — | `TestANotificationAfterTheFacilityIsGoneIsAViolation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/invariant_test.go:145` | packages/storage/storage-domain/src/invariant.ts | 26-29 | — | `TestANotificationForAClosedDomainIsAViolation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/invariant_test.go:159` | packages/storage/storage-domain/src/invariant.ts | 47-53 | — | `TestAPutEventCarryingTheWrongValueIsAViolation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/invariant_test.go:185` | packages/storage/storage-domain/src/invariant.ts | 47-53 | — | `TestAPutEventForAnAbsentRecordIsAViolation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/invariant_test.go:204` | packages/storage/storage-domain/src/invariant.ts | 39-45 | — | `TestADeleteEventForALivingRecordIsAViolation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/invariant_test.go:228` | packages/storage/storage-domain/src/invariant.ts | 30-35 | — | `TestAGlobalEventCarryingTheWrongValueIsAViolation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/invariant_test.go:252` | packages/storage/storage-domain/src/invariant.ts | 30-35 | — | `TestAGlobalEventOnADomainWithoutAGlobalSlotIsAViolation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/invariant_test.go:275` | packages/storage/storage-domain/src/invariant.ts | 37 | — | `TestARecordEventOnAnUndeclaredTableIsAViolation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/invariant_test.go:293` | packages/storage/storage-domain/src/invariant.ts | 58-59 | — | `TestUnregisteringTheInvariantAlsoDropsTheSubscription` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/spec.go:118` | packages/storage/storage-domain/src/spec.ts | 14-20 | — | `DefineGlobal` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/spec_test.go:20` | packages/storage/storage-domain/src/spec.ts | 67-98 | — | `TestValidateRejectsBadDomainNames` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/spec_test.go:56` | packages/storage/storage-domain/src/spec.ts | 67-98 | — | `TestValidateRejectsBadAndDuplicateTableNames` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/spec_test.go:82` | packages/storage/storage-domain/src/spec.ts | 67-98 | — | `TestValidateRejectsHandWrittenZeroValueSpecs` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/spec_test.go:102` | packages/storage/storage-domain/src/spec.ts | 91-96 | — | `TestAGlobalInitialThatEncodesToNullIsRefusedAtDeclaration` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/spec_test.go:121` | packages/storage/storage-domain/src/spec.ts | 91-96 | — | `TestAGlobalInitialThatFailsItsOwnValidationIsRefused` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/spec_test.go:144` | packages/storage/storage-domain/src/spec.ts | 100-112 | — | `TestDescriptorProjectsTheSpecOntoTheBackend` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/spec_test.go:179` | packages/storage/storage-domain/src/spec.ts | 58-65 | — | `TestEncodingRefusesAValueOfTheWrongType` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/domain/spec_test.go:225` | packages/storage/storage-domain/src/spec.ts | 91-96 | — | `TestAGlobalInitialThatCannotBeMarshalledIsRefused` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/postgres/backend.go:66` | packages/storage/storage-sqlite/src/index.ts | 64-73 | — | `Open` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/postgres/backend.go:129` | packages/storage/storage-sqlite/src/index.ts | 75-123 | — | `Open` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/postgres/backend.go:188` | packages/storage/storage-sqlite/src/index.ts | 98-123 | — | `materialize` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/postgres/schema.go:19` | packages/storage/storage-sqlite/src/schema.ts | 14-20 | — | `SchemaVersion` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/postgres/schema.go:74` | packages/storage/storage-sqlite/src/schema.ts | 52-108 | — | `ensureLayout` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/postgres/unit.go:21` | packages/storage/storage-sqlite/src/unit.ts | 27-42 | — | `kvUnit` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/registry.go:71` | packages/storage/storage/src/registry.ts | 31-36 | — | `unregister` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/storage.go:85` | packages/storage/storage/src/index.ts | 69-74 | — | `unmount` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/storage_test.go:55` | packages/storage/storage/tests/registry.spec.ts | 9-18 | — | `TestRegistryRegistersResolvesAndDisposes` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/storage_test.go:90` | packages/storage/storage/tests/registry.spec.ts | 20-24 | — | `TestRegistryRejectsDuplicateNames` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/storage_test.go:123` | packages/storage/storage/tests/registry.spec.ts | 50-68 | — | `TestRegistryIgnoresAStaleDisposer` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/storage_test.go:177` | packages/storage/storage/src/registry.ts | 39-53 | — | `TestRegistryNotFoundListsWhatIsRegistered` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/storage_test.go:238` | packages/storage/storage/src/registry.ts | 17-37 | — | `TestRegistryDisposalDoesNotCloseTheBackend` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/storage_test.go:302` | packages/storage/storage/tests/registry.spec.ts | 33-48 | — | `TestStorageMountsResolvesAndUnmountsAForm` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/storage_test.go:344` | packages/storage/storage/tests/registry.spec.ts | 50-60 | — | `TestStorageIgnoresAStaleMountDisposer` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/storage_test.go:425` | packages/storage/storage/src/index.ts | 89-92 | — | `TestFormAsResolvesWithTheCallersType` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/storage_test.go:511` | packages/storage/storage/src/backend.ts | 12-16 | — | `TestKVReportsWhenABackendCannotServeTheForm` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/storagetest/contract.go:51` | packages/storage/storage/tests/contract.ts | 20-25 | — | `contractDescriptor` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/storagetest/contract.go:94` | packages/storage/storage/tests/contract.ts | 34-41 | — | `runOpensMissingUnitAsEmpty` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/storagetest/contract.go:131` | packages/storage/storage/tests/contract.ts | 43-59 | — | `runRoundTripsAcrossReopen` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/storagetest/contract.go:170` | packages/storage/storage/tests/contract.ts | 61-72 | — | `runPutOverwritesAndDeleteIsIdempotent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/storagetest/contract.go:207` | packages/storage/storage/tests/contract.ts | 74-89 | — | `runRejectsVersionMismatch` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/storagetest/contract.go:245` | packages/storage/storage/tests/contract.ts | 91-100 | — | `runRejectsAfterClose` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `storage/storagetest/contract.go:287` | packages/storage/storage/src/backend.ts | 36-38 | — | `runRejectsDoubleOpen` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/controltool/control.go:29` | packages/subagent/tool-subagent-control/src/index.ts | 27 | — | `SendMessageTool` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/controltool/control.go:34` | packages/subagent/tool-subagent-control/src/index.ts | 80 | — | `InterruptTool` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/controltool/control.go:39` | packages/subagent/tool-subagent-control/src/index.ts | 28-33 | — | `sendMessageDescription` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/controltool/control.go:48` | packages/subagent/tool-subagent-control/src/index.ts | 38 | — | `subagentIDDescription` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/controltool/control.go:53` | packages/subagent/tool-subagent-control/src/index.ts | 43 | — | `messageDescription` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/controltool/control.go:58` | packages/subagent/tool-subagent-control/src/index.ts | 81-87 | — | `interruptDescription` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/controltool/control.go:67` | packages/subagent/tool-subagent-control/src/index.ts | 92 | — | `agentIDDescription` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/controltool/control.go:72` | packages/subagent/tool-subagent-control/src/index.ts | 63 | — | `missingSendMessageAgent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/controltool/control.go:77` | packages/subagent/tool-subagent-control/src/index.ts | 112 | — | `missingInterruptAgent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/controltool/control.go:103` | packages/subagent/tool-subagent-control/src/index.ts | 19 | — | `AcpConfig` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `subagent/controltool/control.go:185` | packages/subagent/tool-subagent-control/src/index.ts | 26-77 | — | `newSendMessage` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/controltool/control.go:232` | packages/subagent/tool-subagent-control/src/index.ts | 59-76 | — | `sendMessage` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/controltool/control.go:263` | packages/subagent/tool-subagent-control/src/index.ts | 79-119 | — | `newInterrupt` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/controltool/control.go:303` | packages/subagent/tool-subagent-control/src/index.ts | 108-118 | — | `interrupt` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/controltool/listagents.go:62` | packages/subagent/tool-subagent-control/src/list-agents.ts | 94-105 | — | `listAgentsDescription` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/controltool/listagents.go:76` | packages/subagent/tool-subagent-control/src/list-agents.ts | 110 | — | `scopeDescription` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/controltool/listagents.go:82` | packages/subagent/tool-subagent-control/src/list-agents.ts | 149 | — | `emptyListing` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/controltool/listagents.go:87` | packages/subagent/tool-subagent-control/src/list-agents.ts | 168 | — | `missingListAgent` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/controltool/listagents.go:111` | packages/subagent/tool-subagent-control/src/list-agents.ts | 18 | — | `ListConfig` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/controltool/listagents.go:140` | packages/subagent/tool-subagent-control/src/list-agents.ts | 91 | — | `NewListAgents` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/controltool/listagents.go:163` | packages/subagent/tool-subagent-control/src/list-agents.ts | 48-50 | — | `resolveScope` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/controltool/listagents.go:173` | packages/subagent/tool-subagent-control/src/list-agents.ts | 30-45 | — | `listedEntry` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/controltool/listagents.go:285` | packages/subagent/tool-subagent-control/src/list-agents.ts | 92-191 | — | `newDefinition` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/controltool/listagents.go:368` | packages/subagent/tool-subagent-control/src/list-agents.ts | 144-162 | — | `renderListing` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/controltool/listagents.go:400` | packages/subagent/tool-subagent-control/src/list-agents.ts | 164-190 | — | `list` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/forkinprocess/provider.go:29` | packages/subagent/subagent-fork-in-process/src/index.ts | 37 | — | `DefaultProviderName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/forkinprocess/provider.go:62` | packages/subagent/subagent-fork-in-process/src/index.ts | 61-90 | — | `CredentialProvider` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `subagent/inprocessdriver/driver.go:152` | packages/subagent/subagent-in-process-driver/src/index.ts | 118 | — | `seedWithDelegatedPolicies` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/inprocessdriver/driver.go:261` | packages/subagent/subagent-in-process-driver/src/index.ts | 148-190 | — | `inProcessRun` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/inprocessdriver/driver.go:360` | packages/subagent/subagent-in-process-driver/src/index.ts | 157-160 | — | `abort` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/inprocessdriver/structured.go:43` | packages/subagent/subagent-in-process-driver/src/structured.ts | 66-68 | — | `structuredOutputDescription` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/inprocessdriver/structured.go:50` | packages/subagent/subagent-in-process-driver/src/structured.ts | 83 | — | `structuredOutputRecorded` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/inprocessdriver/structured.go:55` | packages/subagent/subagent-in-process-driver/src/structured.ts | 101 | — | `structuredPromptSectionOrder` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/inprocessdriver/structured.go:128` | packages/subagent/subagent-in-process-driver/src/structured.ts | 93 | — | `stage` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/inprocessdriver/structured.go:137` | packages/subagent/subagent-in-process-driver/src/structured.ts | 116-139 | — | `commit` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/inprocessdriver/structured.go:181` | packages/subagent/subagent-in-process-driver/src/structured.ts | 77-82 | — | `recordedOutputSchema` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/reporttool/tool.go:29` | packages/subagent/tool-subagent-report/src/index.ts | 66 | — | `ToolName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/reporttool/tool.go:34` | packages/subagent/tool-subagent-report/src/index.ts | 55 | — | `SectionName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/reporttool/tool.go:39` | packages/subagent/tool-subagent-report/src/index.ts | 24 | — | `SectionOrder` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/reporttool/tool.go:50` | packages/subagent/tool-subagent-report/src/index.ts | 37 | — | `DefaultDelivery` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/reporttool/tool.go:58` | packages/subagent/tool-subagent-report/src/index.ts | 57-61 | — | `promptText` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/reporttool/tool.go:67` | packages/subagent/tool-subagent-report/src/index.ts | 67-73 | — | `toolDescription` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/reporttool/tool.go:77` | packages/subagent/tool-subagent-report/src/index.ts | 78 | — | `outputDescription` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/reporttool/tool.go:193` | packages/subagent/tool-subagent-report/src/index.ts | 65-104 | — | `newDefinition` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/spawninprocess/provider.go:28` | packages/subagent/subagent-spawn-in-process/src/index.ts | 31 | — | `DefaultProviderName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/spawninprocess/provider.go:36` | packages/subagent/subagent-spawn-in-process/src/index.ts | 41-60 | — | `CredentialProvider` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `subagent/subagent/activationsetup.go:38` | packages/subagent/subagent/src/activation-setup-registry.ts | 29-33 | — | `setupRegistration` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/activationsetup.go:48` | packages/subagent/subagent/src/activation-setup-registry.ts | 36-43 | — | `setupInstallation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/activationsetup.go:61` | packages/subagent/subagent/src/activation-setup-registry.ts | 46-49 | — | `setupTransaction` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/activationsetup.go:280` | packages/subagent/subagent/src/activation-setup-registry.ts | 170-182 | — | `releaseOne` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/childagent.go:170` | packages/subagent/subagent/src/child-agent.ts | 169 | — | `delegationContextOrder` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/continuation.go:157` | packages/subagent/subagent/src/continuation.ts | 245-251 | — | `disposalTx` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/continuation.go:318` | packages/subagent/subagent/src/continuation.ts | 429 | — | `continuationCancelled` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/continuation.go:368` | packages/subagent/subagent/src/continuation.ts | 372-392 | — | `NewContinuationManager` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/continuationactivation.go:292` | packages/subagent/subagent/src/continuation.ts | 1058-1063 | — | `seedWithDelegatedPolicies` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/continuationactivation.go:327` | packages/subagent/subagent/src/continuation.ts | 1105-1127 | — | `publishActivation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/continuationactivation.go:375` | packages/subagent/subagent/src/continuation.ts | 1116-1122 | — | `retire` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/continuationactivation.go:648` | packages/subagent/subagent/src/continuation.ts | 1343-1352 | — | `beginDisposalLocked` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/continuationactivation.go:678` | packages/subagent/subagent/src/continuation.ts | 1359-1369 | — | `startTeardown` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/continuationdrain.go:262` | packages/subagent/subagent/src/continuation.ts | 867-874 | — | `closingMembersLocked` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/continuationdrain.go:309` | packages/subagent/subagent/src/continuation.ts | 895-909 | — | `closingTeardown` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/continuationdrain.go:354` | packages/subagent/subagent/src/continuation.ts | 923-936 | — | `stateOfLocked` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/continuationops.go:167` | packages/subagent/subagent/src/continuation.ts | 449-459 | — | `assertPersistedIDAvailable` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/continuationops.go:238` | packages/subagent/subagent/src/continuation.ts | 507-528 | — | `followupOnce` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/descriptor.go:30` | packages/subagent/subagent/src/descriptor.ts | 29-41 | — | `EventDescriptor` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/descriptor.go:60` | packages/subagent/subagent/src/descriptor.ts | 56 | — | `DescriptorMode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/descriptor.go:106` | packages/subagent/subagent/src/descriptor.ts | 136-142 | — | `defineDomain` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `subagent/subagent/error.go:70` | packages/subagent/subagent/src/list-children.ts | 191-196 | — | `CodeControlProjectionsUnavailable` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/error.go:74` | packages/subagent/subagent/src/list-children.ts | 201-206 | — | `CodeControlSessionStoreUnavailable` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/invariant.go:27` | packages/subagent/subagent/src/invariant.ts | 23-29 | — | `lifecycleInvariant` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/invariant.go:47` | packages/subagent/subagent/src/invariant.ts | 32-39 | — | `providerAdded` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/invariant.go:71` | packages/subagent/subagent/src/invariant.ts | 40-45 | — | `providerRemoved` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/invariant.go:86` | packages/subagent/subagent/src/invariant.ts | 46-57 | — | `runStarted` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/invariant.go:113` | packages/subagent/subagent/src/invariant.ts | 14-20 | — | `runEnded` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/lifecycle.go:31` | packages/subagent/subagent/src/lifecycle.ts | 80 | — | `StartObserver` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/lifecycle.go:46` | packages/subagent/subagent/src/lifecycle.ts | 81 | — | `EndObserver` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/lifecycle.go:53` | packages/subagent/subagent/src/index.ts | 145-150 | — | `ProviderAddedObserver` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/lifecycle.go:62` | packages/subagent/subagent/src/lifecycle.ts | 82 | — | `ProviderRemovedObserver` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/lifecycle.go:184` | packages/subagent/subagent/src/lifecycle.ts | 110-120 | — | `contain` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/lifecycle.go:225` | packages/subagent/subagent/src/index.ts | 404 | — | `emitProviderAdded` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/listchildren.go:31` | packages/subagent/subagent/src/list-children.ts | 32 | — | `coldReadConcurrency` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/listchildren.go:68` | packages/subagent/subagent/src/list-children.ts | 80-83 | — | `DiagnosticUnsupported` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/listchildren.go:77` | packages/subagent/subagent/src/list-children.ts | 44-92 | — | `SubagentListEntry` | 裁决表+路径一致 |  | 锚点符号在该上游文件里找不到 |
| `subagent/subagent/listchildren.go:173` | packages/subagent/subagent/src/list-children.ts | 246 | — | `coldRead` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/listchildren.go:501` | packages/subagent/subagent/src/list-children.ts | 284-286 | — | `servedIdentity` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/listchildren.go:556` | packages/subagent/subagent/src/list-children.ts | 398-403 | — | `listingCancelled` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/messagesource.go:25` | packages/subagent/subagent/src/continuation.ts | 59 | — | `CoordinatorPlugin` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/messagesource.go:29` | packages/subagent/subagent/src/continuation.ts | 67 | — | `ReportPlugin` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/messagesource.go:33` | packages/subagent/subagent/src/continuation.ts | 83 | — | `SettledPlugin` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/messagesource.go:43` | packages/subagent/subagent/src/continuation.ts | 62 | — | `senderExtra` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/outofprocess.go:29` | packages/subagent/subagent/src/out-of-process.ts | 20 | — | `MaxDiagnosticBytes` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/outofprocess.go:34` | packages/subagent/subagent/src/out-of-process.ts | 22 | — | `diagnosticTruncationSuffix` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/outofprocess.go:39` | packages/subagent/subagent/src/out-of-process.ts | 31-42 | — | `limitDiagnostic` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/projection.go:17` | packages/subagent/subagent/src/projection.ts | 60 | — | `TimingProjectionKey` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/projection.go:21` | packages/subagent/subagent/src/projection.ts | 162 | — | `IdentityProjectionKey` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/projection.go:27` | packages/subagent/subagent/src/projection.ts | 110 | — | `projectionStateVersion` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/projection.go:102` | packages/subagent/subagent/src/projection.ts | 63-102 | — | `applyTiming` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/projection.go:160` | packages/subagent/subagent/src/projection.ts | 103-109 | — | `viewTiming` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/projection.go:167` | packages/subagent/subagent/src/projection.ts | 163-172 | — | `applyIdentity` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/projection.go:194` | packages/subagent/subagent/src/projection.ts | 178 | — | `viewIdentity` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/projectiontypes.go:10` | packages/subagent/subagent/src/projection-types.ts | 12-17 | — | `TimingActive` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/runtime.go:89` | packages/subagent/subagent/src/index.ts | 182-200 | — | `NewRuntime` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/runtime.go:129` | packages/subagent/subagent/src/index.ts | 151-160 | — | `OnStart` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/runtime.go:145` | packages/subagent/subagent/src/index.ts | 161-168 | — | `OnEnd` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/runtime.go:157` | packages/subagent/subagent/src/index.ts | 141-145 | — | `OnProviderAdded` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/runtime.go:171` | packages/subagent/subagent/src/index.ts | 146-150 | — | `OnProviderRemoved` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagent/types.go:273` | packages/subagent/subagent/src/types.ts | 330 | — | `ContinuablePreparer` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagenttool/config.go:33` | packages/subagent/tool-subagent/src/index.ts | 83 | — | `DefaultToolName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagenttool/config.go:38` | packages/subagent/tool-subagent/src/index.ts | 98 | — | `DefaultMaxDepth` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagenttool/config.go:44` | packages/subagent/tool-subagent/src/index.ts | 26 | — | `SectionOrder` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagenttool/settlement.go:55` | packages/subagent/tool-subagent/src/index.ts | 156-159 | — | `contentText` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagenttool/settlement.go:113` | packages/subagent/tool-subagent/src/index.ts | 178-192 | — | `collectForegroundRun` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagenttool/settlement.go:151` | packages/subagent/tool-subagent/src/index.ts | 118-120 | — | `isOnlyCancellation` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagenttool/tool.go:66` | packages/subagent/tool-subagent/src/index.ts | 316-335 | — | `delegationArgs` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagenttool/tool.go:78` | packages/subagent/tool-subagent/src/index.ts | 336-365 | — | `backgroundResult` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagenttool/tool.go:131` | packages/subagent/tool-subagent/src/index.ts | 255-274 | — | `resolveRunInBackground` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagenttool/tool.go:154` | packages/subagent/tool-subagent/src/index.ts | 385-394 | — | `startRequest` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagenttool/tool.go:169` | packages/subagent/tool-subagent/src/index.ts | 379-383 | — | `parentOf` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagenttool/tool.go:185` | packages/subagent/tool-subagent/src/index.ts | 378-439 | — | `delegate` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagenttool/tool.go:216` | packages/subagent/tool-subagent/src/index.ts | 398-408 | — | `startContinuable` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagenttool/tool.go:235` | packages/subagent/tool-subagent/src/index.ts | 409-431 | — | `startBackgroundJob` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagenttool/tool.go:287` | packages/subagent/tool-subagent/src/index.ts | 434-438 | — | `runForeground` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagenttool/tool.go:313` | packages/subagent/tool-subagent/src/index.ts | 186-191 | — | `marshalBlocks` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagenttool/tool.go:349` | packages/subagent/tool-subagent/src/index.ts | 337-365 | — | `outputSchema` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagenttool/tool.go:385` | packages/subagent/tool-subagent/src/index.ts | 300-440 | — | `newTool` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagenttool/tool.go:477` | packages/subagent/tool-subagent/src/index.ts | 452-456 | — | `unmount` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagenttool/tool.go:498` | packages/subagent/tool-subagent/src/index.ts | 449-451 | — | `providerAdded` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagenttool/tool.go:511` | packages/subagent/tool-subagent/src/index.ts | 452-456 | — | `providerRemoved` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagenttool/tool.go:523` | packages/subagent/tool-subagent/src/index.ts | 469 | — | `sectionName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagenttool/tool.go:530` | packages/subagent/tool-subagent/src/index.ts | 471-473 | — | `sectionTextFor` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagenttool/wording.go:66` | packages/subagent/tool-subagent/src/index.ts | 320 | — | `descriptionDescription` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagenttool/wording.go:85` | packages/subagent/tool-subagent/src/index.ts | 308-315 | — | `toolDescription` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagenttool/wording.go:99` | packages/subagent/tool-subagent/src/index.ts | 330-332 | — | `backgroundDescription` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `subagent/subagenttool/wording.go:109` | packages/subagent/tool-subagent/src/index.ts | 473 | — | `sectionText` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `todo/projection.go:17` | packages/todo/tool-todo/src/index.ts | 137 | — | `ProjectionKey` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `todo/projection.go:22` | packages/todo/tool-todo/src/index.ts | 146 | — | `projectionStateVersion` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `todo/projection.go:31` | packages/todo/tool-todo/src/index.ts | 135-148 | — | `goalProjectionDefinition` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `todo/projection.go:62` | packages/todo/tool-todo/src/index.ts | 140-144 | — | `applyProjection` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `todo/tool.go:23` | packages/todo/tool-todo/src/index.ts | 150 | — | `ToolName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `todo/tool.go:195` | packages/todo/tool-todo/src/index.ts | 157-169 | — | `itemNode` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `todo/tool.go:216` | packages/todo/tool-todo/src/index.ts | 147-221 | — | `definition` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `todo/tool.go:316` | packages/todo/tool-todo/src/index.ts | 214-221 | — | `countByStatus` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `util/outputretention/outputretention_test.go:64` | packages/util/output-retention/tests/output-retention.spec.ts | 14-49 | — | `TestItemRetainerKeepsTheHeadAndCountsTheRest` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `util/outputretention/outputretention_test.go:101` | packages/util/output-retention/tests/output-retention.spec.ts | 29-37 | — | `TestItemRetainerReportsNoneWhenEverythingFits` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `util/outputretention/outputretention_test.go:124` | packages/util/output-retention/tests/output-retention.spec.ts | 51-59 | — | `TestItemRetainerWithZeroBudgetKeepsNothing` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `util/outputretention/outputretention_test.go:145` | packages/util/output-retention/tests/output-retention.spec.ts | 61-66 | — | `TestItemRetentionStrategyValidate` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `util/outputretention/outputretention_test.go:169` | packages/util/output-retention/tests/output-retention.spec.ts | 69-99 | — | `TestTextRetainerHeadKeepsThePrefix` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `util/outputretention/outputretention_test.go:197` | packages/util/output-retention/tests/output-retention.spec.ts | 83-89 | — | `TestTextRetainerFlagsAPartiallyDroppedChunk` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `util/outputretention/outputretention_test.go:217` | packages/util/output-retention/tests/output-retention.spec.ts | 101-127 | — | `TestTextRetainerTailKeepsTheEnd` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `util/outputretention/outputretention_test.go:239` | packages/util/output-retention/tests/output-retention.spec.ts | 112-119 | — | `TestTextRetainerTailKeepsEverythingUnderTheCap` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `util/outputretention/outputretention_test.go:258` | packages/util/output-retention/tests/output-retention.spec.ts | 121-126 | — | `TestTextRetainerTailSlidesOldChunksOut` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `util/outputretention/outputretention_test.go:275` | packages/util/output-retention/tests/output-retention.spec.ts | 129-146 | — | `TestTextRetainerHeadTailOmitsTheMiddle` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `util/outputretention/outputretention_test.go:294` | packages/util/output-retention/tests/output-retention.spec.ts | 139-146 | — | `TestTextRetainerHeadTailDoesNotDoubleCount` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `util/outputretention/outputretention_test.go:316` | packages/util/output-retention/tests/output-retention.spec.ts | 148-160 | — | `TestTextRetainerKeepsACodepointSpanningTheArtificialSplit` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `util/outputretention/outputretention_test.go:340` | packages/util/output-retention/tests/output-retention.spec.ts | 162-172 | — | `TestTextRetainerTrimsBoundaryPartialsOnceTheMiddleIsOmitted` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `util/outputretention/outputretention_test.go:361` | packages/util/output-retention/tests/output-retention.spec.ts | 175-190 | — | `TestTextRetainerZeroBudgets` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `util/outputretention/outputretention_test.go:395` | packages/util/output-retention/tests/output-retention.spec.ts | 192-201 | — | `TestTextRetentionStrategyValidate` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `util/outputretention/outputretention_test.go:432` | packages/util/output-retention/tests/output-retention.spec.ts | 204-318 | — | `TestTextRetainerUTF8Boundaries` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `util/outputretention/outputretention_test.go:487` | packages/util/output-retention/tests/output-retention.spec.ts | 252-260 | — | `TestTextRetainerNeverGluesACodepointAcrossTheGap` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `util/outputretention/outputretention_test.go:512` | packages/util/output-retention/tests/output-retention.spec.ts | 262-267 | — | `TestTextRetainerAcceptsRawBytes` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `util/outputretention/outputretention_test.go:555` | packages/util/output-retention/tests/output-retention.spec.ts | 320-333 | — | `TestDescribeOmitted` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `util/outputretention/outputretention_test.go:663` | packages/util/output-retention/tests/output-retention.spec.ts | 335-376 | — | `TestFormatRetentionNotice` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `util/outputretention/retainer.go:77` | packages/util/output-retention/src/index.ts | 200-201 | — | `replacementChar` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `util/outputretention/types.go:38` | packages/util/output-retention/src/index.ts | 33-43 | — | `OmittedKind` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `util/outputretention/types.go:245` | packages/util/output-retention/src/index.ts | 112-127 | — | `NoticeStrategy` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `util/timeout/timeout.go:73` | packages/util/timeout/src/index.ts | 151 | — | `ErrDemandOutstanding` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `util/timeout/timeout.go:270` | packages/util/timeout/src/index.ts | 149-161 | — | `Receive` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/config.go:32` | packages/workflow/tool-ralph/src/index.ts | 413 | — | `ToolName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/config.go:37` | packages/workflow/tool-ralph/src/index.ts | 408 | — | `SectionName` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/config.go:42` | packages/workflow/tool-ralph/src/index.ts | 409 | — | `SectionOrder` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/config.go:47` | packages/workflow/tool-ralph/src/index.ts | 36 | — | `DefaultSubagentProvider` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/config.go:52` | packages/workflow/tool-ralph/src/index.ts | 37 | — | `DefaultMaxRounds` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/config.go:57` | packages/workflow/tool-ralph/src/index.ts | 38 | — | `DefaultMaxHandoffChars` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/config.go:63` | packages/workflow/tool-ralph/src/index.ts | 39 | — | `DefaultMaxResultChars` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/config.go:140` | packages/workflow/tool-ralph/src/index.ts | 42-47 | — | `PlanModeController` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `workflow/toolralph/loop.go:28` | packages/workflow/tool-ralph/src/index.ts | 59 | — | `SdkRunStatus` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `workflow/toolralph/loop.go:42` | packages/workflow/tool-ralph/src/index.ts | 61-65 | — | `runResult` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/loop.go:56` | packages/workflow/tool-ralph/src/index.ts | 67-71 | — | `roundFailure` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/loop.go:91` | packages/workflow/tool-ralph/src/index.ts | 154 | — | `firstRoundHandoff` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/loop.go:98` | packages/workflow/tool-ralph/src/index.ts | 155-162 | — | `roundPrompt` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/loop.go:131` | packages/workflow/tool-ralph/src/index.ts | 151-176 | — | `runLoop` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/loop.go:178` | packages/workflow/tool-ralph/src/index.ts | 163-171 | — | `runRound` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/loop.go:225` | packages/workflow/tool-ralph/src/index.ts | 163-170 | — | `settleRound` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/loop.go:251` | packages/workflow/tool-ralph/src/index.ts | 168-170 | — | `collectRound` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/loop.go:292` | packages/subagent/tool-subagent/src/index.ts | 152-160 | — | `withDiagnostic` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/render.go:15` | packages/workflow/tool-ralph/src/index.ts | 351 | — | `truncationNotice` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/render.go:43` | packages/workflow/tool-ralph/src/index.ts | 366 | — | `indentReport` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/report.go:21` | packages/workflow/tool-ralph/src/index.ts | 49 | — | `RoundStatus` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/report.go:35` | packages/workflow/tool-ralph/src/index.ts | 51-57 | — | `RoundReport` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/report.go:54` | packages/workflow/tool-ralph/src/index.ts | 249 | — | `reportKeys` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/report.go:130` | packages/workflow/tool-ralph/src/index.ts | 144 | — | `encodeReport` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/report.go:176` | packages/workflow/tool-ralph/src/index.ts | 113-115 | — | `reportFields` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/tool.go:25` | packages/workflow/tool-ralph/src/index.ts | 179-184 | — | `toolDescription` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/tool.go:40` | packages/workflow/tool-ralph/src/index.ts | 410 | — | `sectionText` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/tool.go:60` | packages/workflow/tool-ralph/src/index.ts | 75-78 | — | `callArgs` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/tool.go:71` | packages/workflow/tool-ralph/src/index.ts | 379-383 | — | `outputValue` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/tool.go:135` | packages/workflow/tool-ralph/src/index.ts | 438-441 | — | `parentOf` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/tool.go:153` | packages/workflow/tool-ralph/src/index.ts | 379-383 | — | `runResultSchema` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/tool.go:181` | packages/workflow/tool-ralph/src/index.ts | 427-431 | — | `outputSchema` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workflow/toolralph/tool.go:235` | packages/workflow/tool-ralph/src/index.ts | 410-476 | — | `newTool` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/entity.go:51` | packages/workspace/workspace/src/entity.ts | 66 | — | `errUnchanged` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/entity.go:76` | packages/workspace/workspace/src/entity.ts | 77-83 | — | `newEntity` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/entity.go:99` | packages/workspace/workspace/src/entity.ts | 85-87 | — | `Path` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/entity.go:104` | packages/workspace/workspace/src/entity.ts | 89-91 | — | `Title` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/entity.go:109` | packages/workspace/workspace/src/entity.ts | 93-95 | — | `CreatedAt` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/entity.go:114` | packages/workspace/workspace/src/entity.ts | 97-99 | — | `UpdatedAt` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/entity.go:129` | packages/workspace/workspace/src/entity.ts | 102 | — | `filterAccounted` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/entity.go:130` | packages/workspace/workspace/src/entity.ts | 207-209 | — | `filterAccounted` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/entity.go:182` | packages/workspace/workspace/src/entity.ts | 115-144 | — | `validateAttach` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/entity.go:254` | packages/workspace/workspace/src/entity.ts | 165-167 | — | `insertBeforeSession` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/error.go:34` | packages/workspace/workspace/src/index.ts | 634 | — | `CodeNotStarted` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/error.go:38` | packages/workspace/workspace/src/index.ts | 160-162 | — | `CodeInvalidPath` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/error.go:42` | packages/workspace/workspace/src/entity.ts | 116-143 | — | `CodeAttachRejected` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/error.go:61` | packages/workspace/workspace/src/index.ts | 413-416 | — | `CodeInconsistentState` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/error.go:75` | packages/workspace/workspace/src/index.ts | 45-64 | — | `AttachmentError` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `workspace/registry.go:28` | packages/workspace/workspace/src/index.ts | 93 | — | `AcpConfig` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `workspace/registry.go:140` | packages/workspace/workspace/src/index.ts | 119-140 | — | `Open` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/registry.go:202` | packages/workspace/workspace/src/index.ts | 122-139 | — | `start` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/registry.go:419` | packages/workspace/workspace/src/index.ts | 218-220 | — | `insertBeforeWorkspace` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/registry.go:498` | packages/workspace/workspace/src/index.ts | 159-162 | — | `resolveDirectory` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/registry.go:821` | packages/workspace/workspace/src/index.ts | 429-442 | — | `groupHeaders` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/registry.go:871` | packages/workspace/workspace/src/index.ts | 491-502 | — | `bootstrapOrder` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/registry.go:1174` | packages/workspace/workspace/src/index.ts | 648-657 | — | `enqueue` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/registry.go:1231` | packages/workspace/workspace/src/index.ts | 634 | — | `notStarted` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/registry.go:1238` | packages/workspace/workspace/src/index.ts | 286-288 | — | `entityByTargetKey` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/registry.go:1289` | packages/workspace/workspace/src/index.ts | 106 | — | `sessionTargetKey` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/registry.go:1302` | packages/workspace/workspace/src/index.ts | 108-111 | — | `rememberSessionTarget` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/registry.go:1360` | packages/workspace/workspace/src/index.ts | 290 | — | `defaultTitle` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/spec.go:58` | packages/workspace/workspace/src/spec.ts | 37-40 | — | `GoalOperation` | 裁决表 |  | 锚点符号在该上游文件里找不到 |
| `workspace/spec.go:111` | packages/workspace/workspace/src/spec.ts | 72 | — | `initialDomainState` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/types.go:144` | packages/workspace/workspace/src/index.ts | 264 | — | `LiveSessions` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |
| `workspace/types.go:164` | packages/workspace/workspace/src/index.ts | 93 | — | `Persistence` | Go 声明名 |  | 锚点是拿 Go 声明名硬凑的，找不到多半只说明这个名字上游没有对应物 |

## 其余状态

按要求只给数量，不逐条列。

- `AMBIGUOUS`：151 条——同名声明在该文件里出现多次，定不了
- `NO_ANCHOR`：577 条——判不出锚点（多为整段溯源、结构体字段上的注释）
- `CONTAINS`：82 条——引的范围完全包含算出来的（文件头部那种整体溯源，不算错）
- `OK`：919 条——引的范围和算出来的一致

