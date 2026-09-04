# Go 包文档映射

本表把每个可发布 Go 包映射到一篇主文档。`go run ./internal/devtools/doccheck` 会检查遗漏、重复、陈旧包、文件不存在、侧栏遗漏和本地链接失效。`go list ./...` 能看到但被 Git 明确忽略的临时目录不属于发布包，不进入本表。

| Go 包 | 主文档 |
|---|---|
| `github.com/snight1983/ds-harness-go/adapter/datastore/dbtest` | [持久化抽象层](modules/datastore.md) |
| `github.com/snight1983/ds-harness-go/adapter/datastore/kvstore` | [持久化抽象层](modules/datastore.md) |
| `github.com/snight1983/ds-harness-go/adapter/datastore/sessionstore` | [持久化抽象层](modules/datastore.md) |
| `github.com/snight1983/ds-harness-go/adapter/datastore` | [持久化抽象层](modules/datastore.md) |
| `github.com/snight1983/ds-harness-go/adapter/domainjobs` | [后台作业](modules/jobs.md) |
| `github.com/snight1983/ds-harness-go/adapter/imagestore` | [附件与图片](modules/attachment.md) |
| `github.com/snight1983/ds-harness-go/adapter/localjobs` | [后台作业](modules/jobs.md) |
| `github.com/snight1983/ds-harness-go/adapter/objectstore` | [文件系统](modules/filesystem.md) |
| `github.com/snight1983/ds-harness-go/adapter/openaicompat` | [LLM](modules/llm.md) |
| `github.com/snight1983/ds-harness-go/adapter/textstore` | [大结果外置](modules/spill.md) |
| `github.com/snight1983/ds-harness-go/attachment` | [附件与图片](modules/attachment.md) |
| `github.com/snight1983/ds-harness-go/cmd/llmmockserver` | [LLM 测试与回放](modules/llm-testing.md) |
| `github.com/snight1983/ds-harness-go/credentials` | [凭据](modules/credentials.md) |
| `github.com/snight1983/ds-harness-go/example/minimalhost` | [嵌入 Go 服务](embedding.md) |
| `github.com/snight1983/ds-harness-go/feature/checkpointpolicy` | [Session](modules/session.md) |
| `github.com/snight1983/ds-harness-go/feature/compaction/basic` | [上下文压缩](modules/compaction.md) |
| `github.com/snight1983/ds-harness-go/feature/compaction/toolresultpruner` | [上下文压缩](modules/compaction.md) |
| `github.com/snight1983/ds-harness-go/feature/compaction` | [上下文压缩](modules/compaction.md) |
| `github.com/snight1983/ds-harness-go/feature/context/instructions` | [运行时上下文](modules/context.md) |
| `github.com/snight1983/ds-harness-go/feature/context/sessionref` | [运行时上下文](modules/context.md) |
| `github.com/snight1983/ds-harness-go/feature/context/timecontext` | [运行时上下文](modules/context.md) |
| `github.com/snight1983/ds-harness-go/feature/goal/goalcommand` | [长期目标](modules/goal.md) |
| `github.com/snight1983/ds-harness-go/feature/goal/goalrounddriver` | [长期目标](modules/goal.md) |
| `github.com/snight1983/ds-harness-go/feature/goal/goaltool` | [长期目标](modules/goal.md) |
| `github.com/snight1983/ds-harness-go/feature/goal` | [长期目标](modules/goal.md) |
| `github.com/snight1983/ds-harness-go/feature/guard/repeattoolreminder` | [运行时 Guard](modules/guards.md) |
| `github.com/snight1983/ds-harness-go/feature/guard/timeoutpolicy` | [运行时 Guard](modules/guards.md) |
| `github.com/snight1983/ds-harness-go/feature/interaction/askuser` | [用户交互](modules/interaction.md) |
| `github.com/snight1983/ds-harness-go/feature/interaction/commands` | [用户交互](modules/interaction.md) |
| `github.com/snight1983/ds-harness-go/feature/interaction/userapproval` | [用户交互](modules/interaction.md) |
| `github.com/snight1983/ds-harness-go/feature/interaction/userquestions` | [用户交互](modules/interaction.md) |
| `github.com/snight1983/ds-harness-go/feature/jobs/jobstool` | [后台作业](modules/jobs.md) |
| `github.com/snight1983/ds-harness-go/feature/jobs` | [后台作业](modules/jobs.md) |
| `github.com/snight1983/ds-harness-go/feature/llmretry` | [LLM](modules/llm.md) |
| `github.com/snight1983/ds-harness-go/feature/outputretention` | [通用运行时工具](modules/utilities.md) |
| `github.com/snight1983/ds-harness-go/feature/persistence` | [Session](modules/session.md) |
| `github.com/snight1983/ds-harness-go/feature/plan/planmode` | [计划与待办](modules/planning.md) |
| `github.com/snight1983/ds-harness-go/feature/preset/agentpresets` | [Agent 预设与 Persona](modules/presets.md) |
| `github.com/snight1983/ds-harness-go/feature/preset/persona` | [Agent 预设与 Persona](modules/presets.md) |
| `github.com/snight1983/ds-harness-go/feature/projectioncache` | [Session](modules/session.md) |
| `github.com/snight1983/ds-harness-go/feature/replay` | [LLM 测试与回放](modules/llm-testing.md) |
| `github.com/snight1983/ds-harness-go/feature/schedule` | [耐久提醒](modules/schedule.md) |
| `github.com/snight1983/ds-harness-go/feature/sessionquery/querytool` | [Session 查询](modules/sessionquery.md) |
| `github.com/snight1983/ds-harness-go/feature/sessionquery` | [Session 查询](modules/sessionquery.md) |
| `github.com/snight1983/ds-harness-go/feature/sessionstats` | [Session](modules/session.md) |
| `github.com/snight1983/ds-harness-go/feature/sessiontitle/sessiontitlellm` | [Session](modules/session.md) |
| `github.com/snight1983/ds-harness-go/feature/sessiontitle` | [Session](modules/session.md) |
| `github.com/snight1983/ds-harness-go/feature/skill/skilltool` | [Skill、提示词与预设](modules/skill.md) |
| `github.com/snight1983/ds-harness-go/feature/skill` | [Skill、提示词与预设](modules/skill.md) |
| `github.com/snight1983/ds-harness-go/feature/spillpolicy` | [大结果外置](modules/spill.md) |
| `github.com/snight1983/ds-harness-go/feature/subagent/controltool` | [多 Agent](modules/subagent.md) |
| `github.com/snight1983/ds-harness-go/feature/subagent/forkinprocess` | [多 Agent](modules/subagent.md) |
| `github.com/snight1983/ds-harness-go/feature/subagent/inprocessdriver` | [多 Agent](modules/subagent.md) |
| `github.com/snight1983/ds-harness-go/feature/subagent/providertest` | [多 Agent](modules/subagent.md) |
| `github.com/snight1983/ds-harness-go/feature/subagent/reporttool` | [多 Agent](modules/subagent.md) |
| `github.com/snight1983/ds-harness-go/feature/subagent/spawninprocess` | [多 Agent](modules/subagent.md) |
| `github.com/snight1983/ds-harness-go/feature/subagent/subagenttool` | [多 Agent](modules/subagent.md) |
| `github.com/snight1983/ds-harness-go/feature/subagent` | [多 Agent](modules/subagent.md) |
| `github.com/snight1983/ds-harness-go/feature/telemetry` | [Session](modules/session.md) |
| `github.com/snight1983/ds-harness-go/feature/timeout` | [通用运行时工具](modules/utilities.md) |
| `github.com/snight1983/ds-harness-go/feature/todo` | [计划与待办](modules/planning.md) |
| `github.com/snight1983/ds-harness-go/feature/tokenmeter` | [LLM](modules/llm.md) |
| `github.com/snight1983/ds-harness-go/feature/workflow/toolralph` | [Ralph 工作流](modules/ralph.md) |
| `github.com/snight1983/ds-harness-go/feature/workspace` | [Workspace](modules/workspace.md) |
| `github.com/snight1983/ds-harness-go/fs/fstest` | [文件系统](modules/filesystem.md) |
| `github.com/snight1983/ds-harness-go/fs` | [文件系统](modules/filesystem.md) |
| `github.com/snight1983/ds-harness-go/harness/agent` | [Agent 控制面](modules/agent.md) |
| `github.com/snight1983/ds-harness-go/harness/agentdefaultmodel` | [部署级默认模型](modules/agentdefaultmodel.md) |
| `github.com/snight1983/ds-harness-go/harness/agentloop` | [Agent Loop](modules/agentloop.md) |
| `github.com/snight1983/ds-harness-go/harness/session` | [活会话](modules/livesession.md) |
| `github.com/snight1983/ds-harness-go/harness/systemprompt` | [系统提示词装配](modules/systemprompt.md) |
| `github.com/snight1983/ds-harness-go/harness` | [嵌入 Go 服务](embedding.md) |
| `github.com/snight1983/ds-harness-go/internal/devtools/capmap` | [移植与文档门禁工具](modules/migration-tools.md) |
| `github.com/snight1983/ds-harness-go/internal/devtools/consumercheck` | [移植与文档门禁工具](modules/migration-tools.md) |
| `github.com/snight1983/ds-harness-go/internal/devtools/dbcheck` | [移植与文档门禁工具](modules/migration-tools.md) |
| `github.com/snight1983/ds-harness-go/internal/devtools/doccheck` | [移植与文档门禁工具](modules/migration-tools.md) |
| `github.com/snight1983/ds-harness-go/internal/devtools/layercheck` | [移植与文档门禁工具](modules/migration-tools.md) |
| `github.com/snight1983/ds-harness-go/internal/devtools/oscheck` | [移植与文档门禁工具](modules/migration-tools.md) |
| `github.com/snight1983/ds-harness-go/internal/devtools/portcheck` | [移植与文档门禁工具](modules/migration-tools.md) |
| `github.com/snight1983/ds-harness-go/internal/devtools/portmap` | [移植与文档门禁工具](modules/migration-tools.md) |
| `github.com/snight1983/ds-harness-go/internal/devtools/rule` | [移植与文档门禁工具](modules/migration-tools.md) |
| `github.com/snight1983/ds-harness-go/internal/devtools/rulingtable` | [移植与文档门禁工具](modules/migration-tools.md) |
| `github.com/snight1983/ds-harness-go/internal/devtools/toolpath` | [移植与文档门禁工具](modules/migration-tools.md) |
| `github.com/snight1983/ds-harness-go/invariants` | [不变量诊断](modules/invariants.md) |
| `github.com/snight1983/ds-harness-go/llm/mockserver` | [LLM 测试与回放](modules/llm-testing.md) |
| `github.com/snight1983/ds-harness-go/llm` | [LLM](modules/llm.md) |
| `github.com/snight1983/ds-harness-go/protocol/acp` | [ACP 接入](modules/acp.md) |
| `github.com/snight1983/ds-harness-go/protocol/mcp` | [MCP 客户端](modules/mcp.md) |
| `github.com/snight1983/ds-harness-go/protocol/sdk/sdkprotocol` | [SDK 协议与服务端](modules/sdk.md) |
| `github.com/snight1983/ds-harness-go/protocol/sdk/sdkserver` | [SDK 协议与服务端](modules/sdk.md) |
| `github.com/snight1983/ds-harness-go/scope` | [作用域](modules/scope.md) |
| `github.com/snight1983/ds-harness-go/sessionlog/projection` | [Session](modules/session.md) |
| `github.com/snight1983/ds-harness-go/sessionlog` | [Session](modules/session.md) |
| `github.com/snight1983/ds-harness-go/settings` | [运行时设置](modules/settings.md) |
| `github.com/snight1983/ds-harness-go/spill` | [大结果外置](modules/spill.md) |
| `github.com/snight1983/ds-harness-go/storage/domain` | [存储、文件与附件](modules/storage.md) |
| `github.com/snight1983/ds-harness-go/storage/storagetest` | [存储、文件与附件](modules/storage.md) |
| `github.com/snight1983/ds-harness-go/storage` | [存储、文件与附件](modules/storage.md) |
| `github.com/snight1983/ds-harness-go/tools` | [Tools](modules/tools.md) |
