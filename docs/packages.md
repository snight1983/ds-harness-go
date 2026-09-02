# Go 包文档映射

本表把每个可发布 Go 包映射到一篇主文档。`go run ./tools/doccheck` 会检查遗漏、重复、陈旧包、文件不存在、侧栏遗漏和本地链接失效。`go list ./...` 能看到但被 Git 明确忽略的临时目录不属于发布包，不进入本表。

| Go 包 | 主文档 |
|---|---|
| `github.com/snight1983/ds-harness-go/acp/acp` | [ACP 接入](modules/acp.md) |
| `github.com/snight1983/ds-harness-go/attachment` | [附件与图片](modules/attachment.md) |
| `github.com/snight1983/ds-harness-go/cmd/llmmockserver` | [LLM 测试与回放](modules/llm-testing.md) |
| `github.com/snight1983/ds-harness-go/compaction` | [上下文压缩](modules/compaction.md) |
| `github.com/snight1983/ds-harness-go/compaction/basic` | [上下文压缩](modules/compaction.md) |
| `github.com/snight1983/ds-harness-go/compaction/toolresultpruner` | [上下文压缩](modules/compaction.md) |
| `github.com/snight1983/ds-harness-go/context/instructions` | [运行时上下文](modules/context.md) |
| `github.com/snight1983/ds-harness-go/context/sessionref` | [运行时上下文](modules/context.md) |
| `github.com/snight1983/ds-harness-go/context/timecontext` | [运行时上下文](modules/context.md) |
| `github.com/snight1983/ds-harness-go/core/agent` | [Agent](modules/agent.md) |
| `github.com/snight1983/ds-harness-go/core/agentdefaultmodel` | [运行时设置](modules/settings.md) |
| `github.com/snight1983/ds-harness-go/core/agentloop` | [Agent Loop](modules/agentloop.md) |
| `github.com/snight1983/ds-harness-go/core/scope` | [Agent](modules/agent.md) |
| `github.com/snight1983/ds-harness-go/core/session` | [活会话](modules/core-session.md) |
| `github.com/snight1983/ds-harness-go/core/systemprompt` | [Skill、提示词与预设](modules/skill.md) |
| `github.com/snight1983/ds-harness-go/core/tools` | [Tools](modules/tools.md) |
| `github.com/snight1983/ds-harness-go/credentials` | [凭据](modules/credentials.md) |
| `github.com/snight1983/ds-harness-go/datastore` | [持久化抽象层](modules/datastore.md) |
| `github.com/snight1983/ds-harness-go/datastore/dbtest` | [持久化抽象层](modules/datastore.md) |
| `github.com/snight1983/ds-harness-go/datastore/kvstore` | [持久化抽象层](modules/datastore.md) |
| `github.com/snight1983/ds-harness-go/datastore/sessionstore` | [持久化抽象层](modules/datastore.md) |
| `github.com/snight1983/ds-harness-go/example/minimalhost` | [嵌入 Go 服务](embedding.md) |
| `github.com/snight1983/ds-harness-go/fs` | [文件系统](modules/filesystem.md) |
| `github.com/snight1983/ds-harness-go/fs/objectstore` | [文件系统](modules/filesystem.md) |
| `github.com/snight1983/ds-harness-go/goal/goal` | [长期目标](modules/goal.md) |
| `github.com/snight1983/ds-harness-go/goal/goalcommand` | [长期目标](modules/goal.md) |
| `github.com/snight1983/ds-harness-go/goal/goalrounddriver` | [长期目标](modules/goal.md) |
| `github.com/snight1983/ds-harness-go/goal/goaltool` | [长期目标](modules/goal.md) |
| `github.com/snight1983/ds-harness-go/guard/repeattoolreminder` | [运行时 Guard](modules/guards.md) |
| `github.com/snight1983/ds-harness-go/guard/timeoutpolicy` | [运行时 Guard](modules/guards.md) |
| `github.com/snight1983/ds-harness-go/interaction/askuser` | [用户交互](modules/interaction.md) |
| `github.com/snight1983/ds-harness-go/interaction/commands` | [用户交互](modules/interaction.md) |
| `github.com/snight1983/ds-harness-go/interaction/userapproval` | [用户交互](modules/interaction.md) |
| `github.com/snight1983/ds-harness-go/interaction/userquestions` | [用户交互](modules/interaction.md) |
| `github.com/snight1983/ds-harness-go/invariants` | [不变量诊断](modules/invariants.md) |
| `github.com/snight1983/ds-harness-go/jobs/jobs` | [后台作业](modules/jobs.md) |
| `github.com/snight1983/ds-harness-go/jobs/jobstool` | [后台作业](modules/jobs.md) |
| `github.com/snight1983/ds-harness-go/jobs/localjobs` | [后台作业](modules/jobs.md) |
| `github.com/snight1983/ds-harness-go/llm` | [LLM](modules/llm.md) |
| `github.com/snight1983/ds-harness-go/llm/llmretry` | [LLM](modules/llm.md) |
| `github.com/snight1983/ds-harness-go/llm/mockserver` | [LLM 测试与回放](modules/llm-testing.md) |
| `github.com/snight1983/ds-harness-go/llm/openaicompat` | [LLM](modules/llm.md) |
| `github.com/snight1983/ds-harness-go/llm/replay` | [LLM 测试与回放](modules/llm-testing.md) |
| `github.com/snight1983/ds-harness-go/llm/tokenmeter` | [LLM](modules/llm.md) |
| `github.com/snight1983/ds-harness-go/mcp` | [MCP 客户端](modules/mcp.md) |
| `github.com/snight1983/ds-harness-go/plan/planmode` | [计划与待办](modules/planning.md) |
| `github.com/snight1983/ds-harness-go/preset/agentpresets` | [Agent 预设与 Persona](modules/presets.md) |
| `github.com/snight1983/ds-harness-go/preset/persona` | [Agent 预设与 Persona](modules/presets.md) |
| `github.com/snight1983/ds-harness-go/preset/presetstore/localdir` | [Agent 预设与 Persona](modules/presets.md) |
| `github.com/snight1983/ds-harness-go/schedule/schedule` | [耐久提醒](modules/schedule.md) |
| `github.com/snight1983/ds-harness-go/sdk/sdkprotocol` | [SDK 协议与服务端](modules/sdk.md) |
| `github.com/snight1983/ds-harness-go/sdk/sdkserver` | [SDK 协议与服务端](modules/sdk.md) |
| `github.com/snight1983/ds-harness-go/session` | [Session](modules/session.md) |
| `github.com/snight1983/ds-harness-go/session/checkpointpolicy` | [Session](modules/session.md) |
| `github.com/snight1983/ds-harness-go/session/persistence` | [Session](modules/session.md) |
| `github.com/snight1983/ds-harness-go/session/projection` | [Session](modules/session.md) |
| `github.com/snight1983/ds-harness-go/session/projectioncache` | [Session](modules/session.md) |
| `github.com/snight1983/ds-harness-go/session/sessiontitle` | [Session](modules/session.md) |
| `github.com/snight1983/ds-harness-go/session/sessiontitlellm` | [Session](modules/session.md) |
| `github.com/snight1983/ds-harness-go/session/stats` | [Session](modules/session.md) |
| `github.com/snight1983/ds-harness-go/session/telemetry` | [Session](modules/session.md) |
| `github.com/snight1983/ds-harness-go/sessionquery` | [Session 查询](modules/sessionquery.md) |
| `github.com/snight1983/ds-harness-go/sessionquery/querytool` | [Session 查询](modules/sessionquery.md) |
| `github.com/snight1983/ds-harness-go/settings` | [运行时设置](modules/settings.md) |
| `github.com/snight1983/ds-harness-go/skill` | [Skill、提示词与预设](modules/skill.md) |
| `github.com/snight1983/ds-harness-go/skill/skilltool` | [Skill、提示词与预设](modules/skill.md) |
| `github.com/snight1983/ds-harness-go/spill` | [大结果外置](modules/spill.md) |
| `github.com/snight1983/ds-harness-go/spill/policy` | [大结果外置](modules/spill.md) |
| `github.com/snight1983/ds-harness-go/storage` | [存储、文件与附件](modules/storage.md) |
| `github.com/snight1983/ds-harness-go/storage/domain` | [存储、文件与附件](modules/storage.md) |
| `github.com/snight1983/ds-harness-go/storage/storagetest` | [存储、文件与附件](modules/storage.md) |
| `github.com/snight1983/ds-harness-go/subagent/controltool` | [多 Agent](modules/subagent.md) |
| `github.com/snight1983/ds-harness-go/subagent/forkinprocess` | [多 Agent](modules/subagent.md) |
| `github.com/snight1983/ds-harness-go/subagent/inprocessdriver` | [多 Agent](modules/subagent.md) |
| `github.com/snight1983/ds-harness-go/subagent/internal/providertest` | [多 Agent](modules/subagent.md) |
| `github.com/snight1983/ds-harness-go/subagent/reporttool` | [多 Agent](modules/subagent.md) |
| `github.com/snight1983/ds-harness-go/subagent/spawninprocess` | [多 Agent](modules/subagent.md) |
| `github.com/snight1983/ds-harness-go/subagent/subagent` | [多 Agent](modules/subagent.md) |
| `github.com/snight1983/ds-harness-go/subagent/subagenttool` | [多 Agent](modules/subagent.md) |
| `github.com/snight1983/ds-harness-go/todo` | [计划与待办](modules/planning.md) |
| `github.com/snight1983/ds-harness-go/tools/capmap` | [移植与文档门禁工具](modules/migration-tools.md) |
| `github.com/snight1983/ds-harness-go/tools/consumercheck` | [移植与文档门禁工具](modules/migration-tools.md) |
| `github.com/snight1983/ds-harness-go/tools/dbcheck` | [移植与文档门禁工具](modules/migration-tools.md) |
| `github.com/snight1983/ds-harness-go/tools/doccheck` | [移植与文档门禁工具](modules/migration-tools.md) |
| `github.com/snight1983/ds-harness-go/tools/internal/rulingtable` | [移植与文档门禁工具](modules/migration-tools.md) |
| `github.com/snight1983/ds-harness-go/tools/internal/toolpath` | [移植与文档门禁工具](modules/migration-tools.md) |
| `github.com/snight1983/ds-harness-go/tools/portcheck` | [移植与文档门禁工具](modules/migration-tools.md) |
| `github.com/snight1983/ds-harness-go/tools/portmap` | [移植与文档门禁工具](modules/migration-tools.md) |
| `github.com/snight1983/ds-harness-go/tools/rule` | [移植与文档门禁工具](modules/migration-tools.md) |
| `github.com/snight1983/ds-harness-go/util/outputretention` | [通用运行时工具](modules/utilities.md) |
| `github.com/snight1983/ds-harness-go/util/timeout` | [通用运行时工具](modules/utilities.md) |
| `github.com/snight1983/ds-harness-go/workflow/toolralph` | [Ralph 工作流](modules/ralph.md) |
| `github.com/snight1983/ds-harness-go/workspace` | [Workspace](modules/workspace.md) |
