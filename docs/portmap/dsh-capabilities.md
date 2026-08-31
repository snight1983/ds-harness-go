# DSH 能力清单（机器抽取，勿手改）

每一行都来自包作者自己写下的内容，不经转述：
`description` 取自各包 `package.json`，`类/接口` 取自源码里的 `export class` / `export interface`。
由 `tools/capmap` 生成，重跑覆盖。

## 移植顺序（依赖拓扑分层）

第 0 层不依赖任何 DSH 包，是唯一能在不给别人的类型编存根的前提下动笔的地方。
边只取 `dependencies` 与 `peerDependencies`，不取 `devDependencies`（那里混着构建期工具）。

**第 0 层** — 1 个包 / 230 行：runtime-diagnostics/invariants

**第 1 层** — 23 个包 / 24198 行：boot/cmdline、client/ui-primitives、client/ui-slots、client/web、code-runtime/code-runtime、code-runtime/code-runtime-python、core/scope、e2b/e2b、host/directory-picker、host/webserver、sandbox/sandbox-windows-acl、storage/storage、subprocess/subprocess、test-support/llm-mock-server、typert/generator、typert/protocol、util/atomic-write、util/brand、util/home-paths、util/launch-environment、util/native-command、util/output-retention、util/timeout

**第 2 层** — 15 个包 / 11007 行：attachment/attachment、client/modules、credentials/credentials、e2b/subprocess-e2b、host/directory-picker-browse、host/directory-picker-native、host/frontend-static、host/plugin-inventory、identity/anonymous-user-id、settings/settings、storage/storage-domain、storage/storage-json、storage/storage-sqlite、subprocess/subprocess-local、typert/registry

**第 3 层** — 6 个包 / 6517 行：attachment/attachment-local、client/hmr、credentials/credentials-local、llm/llm、settings/settings-file、typert/loader

**第 4 层** — 7 个包 / 8797 行：core/session、core/system-prompt、credentials/authorization、llm/llm-deepseek、lsp/lsp、skill/skill、web/web

**第 5 层** — 13 个包 / 12990 行：boot/app-boot、code-runtime/code-runtime-worker-thread、core/agent、llm/llm-pi-ai、preset/persona、sandbox/sandbox、session/session-persistence、session/session-projection、skill/skill-badge、spill/spill、web/web-fetch-http、web/web-search-exa、web/web-search-perplexity

**第 6 层** — 28 个包 / 18175 行：context/file-reference、context/time-context、core/agent-default-model、examples/jsonrpc-demo、feedback/message-feedback、fs/fs、goal/goal、interaction/commands、interaction/user-approval、interaction/user-questions、jobs/jobs、llm/llm-retry、preset/agent-presets、sandbox/sandbox-local、sandbox/sandbox-policy、session/session-persistence-jsonl、session/session-persistence-sqlite、session/session-projection-cache、session/session-stats、session/session-telemetry、session/session-title、shell/shell、spill/spill-local、terminal/terminal、test-support/loader-smoke、web/web-search-deepseek、workflow/workflow、workspace/workspace

**第 7 层** — 21 个包 / 21542 行：acp/acp、bundle/headless、compaction/compaction、context/tmux-context、core/tools、e2b/fs-e2b、feedback/command-feedback、fs/fs-local、fs/fs-observation-policy、goal/command-goal、goal/goal-round-driver、hooks/hook-protocol、interaction/permission-presets、jobs/jobs-local、lsp/lsp-stdio、session/session-title-llm、session-query/session-query、shell/bash-local、shell/pwsh-local、skill/skill-filesystem、test-support/acp-snapshot

**第 8 层** — 43 个包 / 35328 行：compaction/command-compact、context/agent-instructions、context/file-reference-local、context/session-reference、core/agent-loop、core/agent-tool-presentation、extensions/cordis-host-runner、fs/fs-sandbox、fs/tool-fs、fs/tool-fs-search、fs/tool-str-replace-editor、goal/tool-goal、guard/repeat-tool-reminder、guard/timeout-policy、hooks/hooks-codex、interaction/tool-ask-user、jobs/tool-jobs、llm/token-meter、lsp/tool-lsp、mcp/mcp-client、plan/plan-mode、schedule/schedule、session/session-checkpoint-policy、session/session-telemetry-otel、session/session-title-all-prompts-llm、session/session-title-first-prompt-llm、session-query/session-query-sqlite、session-query/tool-session-query、shell/bash-sandbox、shell/pwsh-sandbox、shell/shell-env、shell/tool-bash-persistent、shell/tool-pwsh-persistent、skill/tool-skill、spill/spill-policy、subagent/subagent、terminal/terminal-bash、terminal/tool-terminal、test-support/agent-loop-testkit、test-support/llm-replay、todo/tool-todo、web/tool-web、workflow/tool-workflow

**第 9 层** — 15 个包 / 16676 行：compaction/compaction-tool-result-pruner、experimental/agent-team、extensions/tool-cordis、hooks/hooks-claude-code、sdk/protocol、shell/tool-bash、shell/tool-pwsh、subagent/subagent-acp、subagent/subagent-claude-code、subagent/subagent-in-process-driver、subagent/tool-subagent、subagent/tool-subagent-control、subagent/tool-subagent-report、workflow/tool-ralph、workflow/workflow-worker-thread

**第 10 层** — 8 个包 / 5238 行：compaction/compaction-basic、examples/agent-spine-demo、experimental/tool-agent-team、sdk/client、sdk/server、subagent/subagent-codex、subagent/subagent-fork-in-process、subagent/subagent-spawn-in-process

**第 11 层** — 2 个包 / 600 行：examples/acp-demo、subagent/subagent-dsh-sdk

**定不了层（卷在依赖环里，需要人拆）** — 45 个：api/gateway、api/remotes、bundle/base、bundle/web-app、client/connection、client/locale、client/runtime、client/ui-agent-preset、client/ui-attachment、client/ui-brand-official、client/ui-commands、client/ui-conversation、client/ui-deliverables、client/ui-directory-picker-browse、client/ui-directory-picker-native、client/ui-goal、client/ui-input-trigger、client/ui-jobs、client/ui-layout、client/ui-message-feedback、client/ui-model-selection、client/ui-permission-presets、client/ui-plan、client/ui-reference、client/ui-renderer、client/ui-settings、client/ui-settings-general、client/ui-settings-models、client/ui-settings-plugin-inventory、client/ui-settings-plugins、client/ui-sidebar、client/ui-skill、client/ui-subagent、client/ui-theme、client/ui-tool、client/ui-trajectory、client/ui-user-questions、client/ui-workflow-run、client/ui-workspace、extensions/cordis-client-runner、extensions/ui-cordis、host/apiproxy、host/directory-picker-auto、session-query/session-log-export、test-support/client-runtime


## acp

### acp/acp — 847 行 / 4 文件

- npm: `@deepseek-ai/dsh-acp`
- 层: 7
- 自述: Automation-only Agent Client Protocol server for driving DeepSeek Harness agents over JSON-RPC stdio
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-attachment, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-user-approval, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: AcpContentError
- 接口: AcpConfig


## api

### api/gateway — 1406 行 / 5 文件

- npm: `@deepseek-ai/dsh-api-gateway`
- 层: -1
- 自述: Typert Remote Host dispatcher and Client API endpoint
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-client-connection, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-typert-protocol, @deepseek-ai/dsh-typert-registry
- README: English \| [中文](README.zh.md)
- 类: TypertGatewayError, TypertGatewayService
- 接口: InvokeRemoteRequest, TypertGateway

### api/remotes — 465 行 / 7 文件

- npm: `@deepseek-ai/dsh-api-remotes`
- 层: -1
- 自述: Remote BFF assembly and Host Agent/Session lookup policy
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-agent-presets, @deepseek-ai/dsh-api-gateway, @deepseek-ai/dsh-commands, @deepseek-ai/dsh-cordis-host-runner, @deepseek-ai/dsh-credentials, @deepseek-ai/dsh-file-reference, @deepseek-ai/dsh-goal, @deepseek-ai/dsh-host-plugin-inventory, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-message-feedback, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-session-reference, @deepseek-ai/dsh-settings, @deepseek-ai/dsh-typert-protocol, @deepseek-ai/dsh-typert-registry
- README: English \| [中文](README.zh.md)
- 类: ApiRemoteSessionNotFound, ApiRemoteSubagentSessionOwnership
- 接口: ApiRemoteAgentOptions


## attachment

### attachment/attachment — 385 行 / 6 文件

- npm: `@deepseek-ai/dsh-attachment`
- 层: 2
- 自述: Durable immutable attachment storage seam for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: AttachmentError, AttachmentStore
- 接口: EncodedImageAttachment, ImageAttachmentLimits, ImageAttachmentRef, ImageRequestPolicy, RequestImageAttachment, SaveImageAttachment, StoredImageAttachment

### attachment/attachment-local — 1270 行 / 8 文件

- npm: `@deepseek-ai/dsh-attachment-local`
- 层: 3
- 自述: Private content-addressed DSH_HOME attachment storage
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-attachment, @deepseek-ai/dsh-home-paths, @deepseek-ai/dsh-invariants, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: CompressionLimiter, LocalAttachmentStore
- 接口: Config, DecodedImageLimits, DetectedImage, EncodedCandidate, ExhaustedEncoding, NormalizationPolicy, NormalizedImage, PreparedImageFile


## boot

### boot/app-boot — 1298 行 / 4 文件

- npm: `@deepseek-ai/dsh-app-boot`
- 层: 5
- 自述: Shared boot glue for the app bins: .env loading, fail-loud Loader guards, snapshot-aware config resolution, and the Loader boot sequence
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-group, @deepseek-ai/cordis-plugin-hmr, @deepseek-ai/cordis-plugin-include, @deepseek-ai/cordis-plugin-loader, @deepseek-ai/dsh-home-paths, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-launch-environment, @deepseek-ai/dsh-system-prompt
- README: English \| [中文](README.zh.md)
- 接口: ConfigDumpLayer, DshBundleManifest, DshManifestSection, DshProfileManifest, FailLoudProcess, Profile, ProfileLayer, ProfileManifest, UserPatchWatchOptions

### boot/cmdline — 202 行 / 2 文件

- npm: `@deepseek-ai/dsh-cmdline`
- 层: 1
- 自述: Immutable command-line handoff from a dsh launcher to any app plugin that injects cmdlineArgs
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-loader, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 接口: AppExit, CmdlineArgs, CmdlineHost


## bundle

### bundle/base — 37 行 / 2 文件

- npm: `@deepseek-ai/dsh-base`
- 层: -1
- 自述: The shared dsh core as a profile bundle: every profile's first patch layer, inserting the base plugin rows over the empty profile root
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-hmr, @deepseek-ai/cordis-plugin-timer, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-agent-default-model, @deepseek-ai/dsh-agent-instructions, @deepseek-ai/dsh-agent-loop, @deepseek-ai/dsh-api-gateway, @deepseek-ai/dsh-attachment-local, @deepseek-ai/dsh-bash-sandbox, @deepseek-ai/dsh-command-compact, @deepseek-ai/dsh-command-feedback, @deepseek-ai/dsh-command-goal, @deepseek-ai/dsh-commands, @deepseek-ai/dsh-compaction-basic, @deepseek-ai/dsh-compaction-tool-result-pruner, @deepseek-ai/dsh-credentials-local, @deepseek-ai/dsh-fs-local, @deepseek-ai/dsh-fs-observation-policy, @deepseek-ai/dsh-fs-sandbox, @deepseek-ai/dsh-goal, @deepseek-ai/dsh-goal-round-driver, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-jobs-local, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-llm-deepseek, @deepseek-ai/dsh-llm-pi-ai, @deepseek-ai/dsh-llm-retry, @deepseek-ai/dsh-permission-presets, @deepseek-ai/dsh-plan-mode, @deepseek-ai/dsh-pwsh-sandbox, @deepseek-ai/dsh-repeat-tool-reminder, @deepseek-ai/dsh-sandbox-local, @deepseek-ai/dsh-sandbox-policy, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-checkpoint-policy, @deepseek-ai/dsh-session-persistence-jsonl, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-session-query-sqlite, @deepseek-ai/dsh-session-telemetry-otel, @deepseek-ai/dsh-session-title, @deepseek-ai/dsh-session-title-first-prompt-llm, @deepseek-ai/dsh-settings-file, @deepseek-ai/dsh-shell-env, @deepseek-ai/dsh-skill, @deepseek-ai/dsh-skill-badge, @deepseek-ai/dsh-skill-filesystem, @deepseek-ai/dsh-spill-local, @deepseek-ai/dsh-spill-policy, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-subagent-fork-in-process, @deepseek-ai/dsh-subagent-spawn-in-process, @deepseek-ai/dsh-subprocess-local, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-token-meter, @deepseek-ai/dsh-tool-bash, @deepseek-ai/dsh-tool-call-timeout-policy, @deepseek-ai/dsh-tool-fs, @deepseek-ai/dsh-tool-fs-search, @deepseek-ai/dsh-tool-goal, @deepseek-ai/dsh-tool-jobs, @deepseek-ai/dsh-tool-pwsh, @deepseek-ai/dsh-tool-ralph, @deepseek-ai/dsh-tool-skill, @deepseek-ai/dsh-tool-str-replace-editor, @deepseek-ai/dsh-tool-subagent, @deepseek-ai/dsh-tool-subagent-control, @deepseek-ai/dsh-tool-subagent-report, @deepseek-ai/dsh-tool-todo, @deepseek-ai/dsh-tool-web, @deepseek-ai/dsh-tool-workflow, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-typert-loader, @deepseek-ai/dsh-typert-registry, @deepseek-ai/dsh-user-approval, @deepseek-ai/dsh-user-questions, @deepseek-ai/dsh-web, @deepseek-ai/dsh-web-search-deepseek, @deepseek-ai/dsh-workflow-worker-thread
- README: English \| [中文](README.zh.md)

### bundle/headless — 237 行 / 3 文件

- npm: `@deepseek-ai/dsh-headless`
- 层: 7
- 自述: The dsh one-shot bundle: a direct core Agent/Session runner over dsh-base with no Host, HTTP, or browser layer
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-loader, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-agent-default-model, @deepseek-ai/dsh-cmdline, @deepseek-ai/dsh-code-runtime-worker-thread, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config, HeadlessStartupValues

### bundle/web-app — 409 行 / 3 文件

- npm: `@deepseek-ai/dsh-web-app`
- 层: -1
- 自述: The dsh browser-surface bundle: the web patch layer over dsh-base plus the runtime glue plugin (frontend dist serving, web-surface prompt, bash runtime variables, URL line)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-loader, @deepseek-ai/dsh-agent-presets, @deepseek-ai/dsh-api-remotes, @deepseek-ai/dsh-app-boot, @deepseek-ai/dsh-client-connection, @deepseek-ai/dsh-client-hmr, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-modules, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-agent-preset, @deepseek-ai/dsh-client-ui-attachment, @deepseek-ai/dsh-client-ui-brand-official, @deepseek-ai/dsh-client-ui-commands, @deepseek-ai/dsh-client-ui-conversation, @deepseek-ai/dsh-client-ui-cordis, @deepseek-ai/dsh-client-ui-deliverables, @deepseek-ai/dsh-client-ui-directory-picker-browse, @deepseek-ai/dsh-client-ui-directory-picker-native, @deepseek-ai/dsh-client-ui-goal, @deepseek-ai/dsh-client-ui-input-trigger, @deepseek-ai/dsh-client-ui-jobs, @deepseek-ai/dsh-client-ui-layout, @deepseek-ai/dsh-client-ui-message-feedback, @deepseek-ai/dsh-client-ui-model-selection, @deepseek-ai/dsh-client-ui-permission-presets, @deepseek-ai/dsh-client-ui-plan, @deepseek-ai/dsh-client-ui-reference, @deepseek-ai/dsh-client-ui-renderer, @deepseek-ai/dsh-client-ui-settings, @deepseek-ai/dsh-client-ui-settings-general, @deepseek-ai/dsh-client-ui-settings-models, @deepseek-ai/dsh-client-ui-settings-plugin-inventory, @deepseek-ai/dsh-client-ui-settings-plugins, @deepseek-ai/dsh-client-ui-sidebar, @deepseek-ai/dsh-client-ui-skill, @deepseek-ai/dsh-client-ui-subagent, @deepseek-ai/dsh-client-ui-theme, @deepseek-ai/dsh-client-ui-tool, @deepseek-ai/dsh-client-ui-trajectory, @deepseek-ai/dsh-client-ui-user-questions, @deepseek-ai/dsh-client-ui-workflow-run, @deepseek-ai/dsh-client-ui-workspace, @deepseek-ai/dsh-cmdline, @deepseek-ai/dsh-code-runtime-worker-thread, @deepseek-ai/dsh-cordis-client-runner, @deepseek-ai/dsh-cordis-host-runner, @deepseek-ai/dsh-file-reference, @deepseek-ai/dsh-file-reference-local, @deepseek-ai/dsh-host-apiproxy, @deepseek-ai/dsh-host-directory-picker-auto, @deepseek-ai/dsh-host-directory-picker-browse, @deepseek-ai/dsh-host-directory-picker-native, @deepseek-ai/dsh-host-frontend-static, @deepseek-ai/dsh-host-plugin-inventory, @deepseek-ai/dsh-host-webserver, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-launch-environment, @deepseek-ai/dsh-message-feedback, @deepseek-ai/dsh-session-log-export, @deepseek-ai/dsh-session-projection-cache, @deepseek-ai/dsh-session-reference, @deepseek-ai/dsh-session-stats, @deepseek-ai/dsh-shell-env, @deepseek-ai/dsh-storage, @deepseek-ai/dsh-storage-domain, @deepseek-ai/dsh-storage-json, @deepseek-ai/dsh-subprocess, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-web-frontend, @deepseek-ai/dsh-workspace, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config, WebRuntimeValues, WebStartupValues


## client

### client/connection — 4825 行 / 17 文件

- npm: `@deepseek-ai/dsh-client-connection`
- 层: -1
- 自述: Wire consumer layer: HTTP-up/WebSocket-down client, ConnectionController dual streams with reconnect, and fixture api
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-attachment, @deepseek-ai/dsh-commands, @deepseek-ai/dsh-host-apiproxy, @deepseek-ai/dsh-host-webserver, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: ConnectionController, FixtureApiClient, HostConnectionService, WebApiClient, WebSocketDownlinks
- 接口: ClientConnectionRpc, ClientTransportHooks, ConnectionConfig, ConnectionConfig, ConnectionHandle, ConnectionRpcHandlerOptions, ConnectionSinks, FetchHandler, FixtureOptions, FixtureWorld, HostConnectionHandle, HostConnectionRpc, HostDescriptionSource

### client/hmr — 450 行 / 5 文件

- npm: `@deepseek-ai/dsh-client-hmr`
- 层: 3
- 自述: Dev-only hot-reload driver for script-loaded client entries: SSE rebuilt frames → invalidate/prefetch → fiber swap through the vendored Loader entry
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-loader, @deepseek-ai/dsh-client-modules, @deepseek-ai/dsh-host-webserver, @deepseek-ai/dsh-invariants, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config

### client/locale — 713 行 / 11 文件

- npm: `@deepseek-ai/dsh-client-locale`
- 层: -1
- 自述: Locale plugin: Host-backed zh/en preference, browser-derived fallback, locale snapshots, and typed namespace dictionaries
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-api-remotes, @deepseek-ai/dsh-client-connection, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-settings, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-settings, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: LocaleRuntime
- 接口: LanguageOptionRow, LanguageRowInjected, LanguageRowState, LocaleDefinition, LocaleSettings, LocaleSnapshot

### client/modules — 1207 行 / 6 文件

- npm: `@deepseek-ai/dsh-client-modules`
- 层: 2
- 自述: Client module system, dual-face: node half composes the __DSH_BOOT__ entry graph (incremental dsh.client scan, bundle route, index tap, webPlugins service); browser half is the lazy-CJS module table the vendored cordis Loader consumes as its internal seam
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-loader, @deepseek-ai/dsh-host-webserver, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: ClientModuleRegistry, ClientModuleSystem
- 接口: BootManifest, BootModuleRow, BootPluginRow, ClientBootstrapModule, ClientBundleRegistration, ClientModuleCreateOptions, ClientModuleLoader, ClientModuleLoaderTarget, ClientModuleRecord, ClientModuleSystemOptions, DshWindow, WebBootEntry, WebBootGraph

### client/runtime — 9035 行 / 44 文件

- npm: `@deepseek-ai/dsh-client-runtime`
- 层: -1
- 自述: Client core services: SlotRegistry, SessionRuntime (scope tree + object layer)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-api-remotes, @deepseek-ai/dsh-attachment, @deepseek-ai/dsh-client-connection, @deepseek-ai/dsh-commands, @deepseek-ai/dsh-host-apiproxy, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-llm-retry, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-session-title, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-typert-protocol, @deepseek-ai/dsh-typert-registry
- README: English \| [中文](README.zh.md)
- 类: ConversationDefinitionRegistry, ConversationEventRegistry, ConversationLocationIndex, ConversationNodeAssembler, ConversationViewRegistry, DirectoryBrowseError, Notifier, PartialAccumulator, PendingWait, ProjectionValueStore, Session, SessionCreateError, SessionForkError, SessionManager, SessionProvideChannel, SessionQueueMirror, SessionRuntime, SlotRegistry, SteeringHistory, ToolCallTree, Workspace, WorkspaceCreateError, WorkspaceManager, WorkspaceRuntime
- 接口: AgentScopeHandle, AssistantMessageNode, AssistantProvenanceView, AssistantRequestConfig, AssistantStepMetadata, AssistantTiming, ChatConversationViewNode, ChatLocationNodeIndex, ChatNodeStore, ChatSnapshot, CommandNode, CompactionSummaryNode, ContextMessageNode, ContextProvenanceView, ConversationContext, ConversationContextReader, ConversationEventDefinitions, ConversationEventInput, ConversationLocationDataChange, ConversationLocationDataStore, ConversationMatch, ConversationMatchResult, ConversationNodeContext, ConversationNodeDefinition, ConversationPreviousContext, ConversationPromptSnapshot, ConversationRuntime, ConversationSnapshot, ConversationStepDataMap, ConversationTimelineSnapshot, ConversationTurnDataMap, ConversationViewBuilder, ConversationViewDefinition, ConversationViewDefinitions, ConversationViewNode, ConversationViewSnapshotMap, ConversationViewSnapshotStore, EngineStoreHandle, EngineStoreInstance, ISession, ISessions, IWorkspaces, LegacyConversationSlice, ObservableSnapshot, PartialAssistant, PendingPayloads, ProjectionsBaseline, ProjectionsFace, PromptError, QueuedMessage, RequestInspectionSnapshot, RequestPromptChange, RootOwnerProps, RunningToolCall, SessionBinding, SessionListEntry, SessionListSnapshot, SessionListState, SessionOptions, SessionProvideChannelHost, SessionProvideContribution, SessionProvideDescriptor, SessionSearchResultItem, SessionSummary, SessionsPort, SessionsPortList, SessionsPortSummary, SettingsScope, SettingsScopeSnapshot, SettingsScopeSpec, SnapshotStore, SteeringMessageNode, StepLocation, SubagentCatalogSnapshot, SubagentDescendantSummary, TitledSessionSummary, ToolResultNode, TurnErrorNode, TurnLocation, TurnMaxTokensNode, UnknownSurfaceNode, UserMessageNode, WorkspaceIntentSnapshot, WorkspaceListSnapshot, WorkspaceListState, WorkspaceSnapshot

### client/ui-agent-preset — 2070 行 / 13 文件

- npm: `@deepseek-ai/dsh-client-ui-agent-preset`
- 层: -1
- 自述: Agent-preset surfaces: the default for later sessions, this session's seat, and the composition editor
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-api-remotes, @deepseek-ai/dsh-client-connection, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-conversation, @deepseek-ai/dsh-client-ui-settings, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: AgentPresetSeatController, AgentPresetSectionController, AgentPresetSettingsController
- 接口: AgentPresetLabelInjected, AgentPresetOption, AgentPresetRowInjected, AgentPresetSeatInjected, AgentPresetSeatState, AgentPresetSectionInjected, AgentPresetSectionState, AgentPresetSettingsState, CopyDraft, PresetDisplaySource, PresetDisplayText, PresetMenuProps, PresetRow, PresetView, RosterPreset, RosterValue, SeatSessionSummary

### client/ui-attachment — 709 行 / 11 文件

- npm: `@deepseek-ai/dsh-client-ui-attachment`
- 层: -1
- 自述: Dynamic attachment presentation plugin for conversation input and message-image slots
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-attachment, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-conversation, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 接口: AttachmentRailItem, AttachmentRailLabels, DropOverlayLabels, ImageLightboxLabels, MessageImageLabels

### client/ui-brand-official — 85 行 / 5 文件

- npm: `@deepseek-ai/dsh-client-ui-brand-official`
- 层: -1
- 自述: Official DeepSeek Harness brand occupants for the Web client's sidebar and conversation Hero slots
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-conversation, @deepseek-ai/dsh-client-ui-sidebar, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)

### client/ui-commands — 1363 行 / 10 文件

- npm: `@deepseek-ai/dsh-client-ui-commands`
- 层: -1
- 自述: Client command surface: global directory cache, '/' source, three command UI kinds, popupSelect registry
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-api-remotes, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-conversation, @deepseek-ai/dsh-client-ui-input-trigger, @deepseek-ai/dsh-commands, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: CommandDirectory, CommandUiRuntime, PopupSelectController
- 接口: CommandContribution, CommandDecoration, CommandUiContract, PopupSelectDeps, PopupSelectInjected, PopupSpec, PopupState, SelectConfirmation, SelectOption

### client/ui-conversation — 11971 行 / 72 文件

- npm: `@deepseek-ai/dsh-client-ui-conversation`
- 层: -1
- 自述: Conversation domain: skeleton, ordered chat flow, composer with the Host-backed busy-Enter preference, and details host
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-api-remotes, @deepseek-ai/dsh-attachment, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-client-connection, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-input-trigger, @deepseek-ai/dsh-client-ui-layout, @deepseek-ai/dsh-client-ui-settings, @deepseek-ai/dsh-commands, @deepseek-ai/dsh-compaction, @deepseek-ai/dsh-goal, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm-retry, @deepseek-ai/dsh-permission-presets, @deepseek-ai/dsh-plan-mode, @deepseek-ai/dsh-session-stats, @deepseek-ai/dsh-settings, @deepseek-ai/dsh-token-meter, @deepseek-ai/dsh-tool-todo, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: ChatSnapshotBuilder, ComposerBlockRegistry, ComposerSubmissionPolicy, ConversationController, InputHub, InputMachine, PendingApproval, SessionInputShell, UnsupportedImageMediaTypeError
- 接口: AssistantActionOwnerProps, AssistantChatData, AssistantMarkdownProps, BrowserIdentity, ChatFileMentions, ChatNodeDataMap, ChatNodeOwnerProps, ChatNodeTurnDataInjected, ChatScrollPosition, ChatStoreState, ChatViewInjected, ChipRender, CommandRowOwnerProps, ComposerAttachment, ComposerAttachmentsOwnerProps, ComposerBarInjected, ComposerBarOwnerProps, ComposerBlock, ComposerBlocks, ComposerChainProps, ComposerKeyboard, ContextInjectionRowProps, ContextMeterProps, ConvViewOwnerProps, ConversationHeaderActionOwnerProps, ConversationHeaderLineageOwnerProps, ConversationInjected, ConversationSessionHeaderInjected, ConversationSessionInjected, ConversationSessionOwnerProps, ConversationSettings, DetailsInjected, DetailsToolOwnerProps, DraftDecorations, EditRange, EditSelection, EmptyWorkspaceOwnerProps, EnterBehaviorRowInjected, GenericCommandCardProps, HeroAgentPresetOwnerProps, HeroBrandMarkOwnerProps, HeroShellProps, IConversation, InboxState, InputActions, InputControlOwnerProps, InputMachineOptions, InputNotice, InputState, InputTarget, InputZone, ManualCompactionChatData, MessageIconActionsProps, MessageImagesOwnerProps, Occurrence, PasteAttemptState, PasteComponent, PermissionSelectProps, PopupDismissFace, QueueDockInjected, ReferenceIconProps, RetryChatData, RetryState, SelectionTarget, SessionInput, SessionInputDeps, SessionInputResolver, StatsLineProps, StepReading, SubmitAttempt, TextRefRange, TodoPanelProps, TokenRange, ToolChatData, TurnMetrics, TurnTailChatData, TurnTailOwnerProps, ViewTab

### client/ui-deliverables — 490 行 / 7 文件

- npm: `@deepseek-ai/dsh-client-ui-deliverables`
- 层: -1
- 自述: Produced-files turn tail and clickable final-response file references for Web
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-client-connection, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-conversation, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-system-prompt
- README: English \| [中文](README.zh.md)
- 接口: DeliverablesTurnData, ProducedFilesInjected

### client/ui-directory-picker-browse — 1221 行 / 6 文件

- npm: `@deepseek-ai/dsh-client-ui-directory-picker-browse`
- 层: -1
- 自述: In-app directory browsing surface: the workspace directory-flow owner rendering the host's listing and creation primitives
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-workspace, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 接口: BrowseFlowInjected, DirectoryBrowserProps

### client/ui-directory-picker-native — 149 行 / 5 文件

- npm: `@deepseek-ai/dsh-client-ui-directory-picker-native`
- 层: -1
- 自述: Native directory-picker surface: the renderless workspace directory-flow occupant driving the host's OS chooser
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-workspace, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 接口: NativeFlowInjected

### client/ui-goal — 498 行 / 9 文件

- npm: `@deepseek-ai/dsh-client-ui-goal`
- 层: -1
- 自述: Session goal surface: GoalBar docked above the composer, read from the goal session projection
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-api-remotes, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-conversation, @deepseek-ai/dsh-commands, @deepseek-ai/dsh-goal, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-typert-protocol
- README: English \| [中文](README.zh.md)
- 接口: GoalBarActions, GoalBarProps, GoalCommandInputData

### client/ui-input-trigger — 1451 行 / 14 文件

- npm: `@deepseek-ai/dsh-client-ui-input-trigger`
- 层: -1
- 自述: Input trigger pipeline: '/' and '@' detection, candidate menu, pick routing to registered sources
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-file-reference, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: InputTriggerController, InputTriggerService
- 接口: BeginCommandRequest, CandidateRequest, ClientSessionContext, CommandClaim, ConsumeTokenRequest, InputTriggerCandidate, InputTriggerControllerDeps, InputTriggerPick, InputTriggerServiceContract, InputTriggerSource, InsertReferenceRequest, InsertTextRequest, MenuState, MenuViewInjected, ReferenceCodec, ReferenceInsert, SourceRoster, SubmitEnvelope, SubmitImageAttachment, SubmitOutcome, TokenSpan, TriggerGuard, TriggerHit

### client/ui-jobs — 312 行 / 6 文件

- npm: `@deepseek-ai/dsh-client-ui-jobs`
- 层: -1
- 自述: Session-header background-job list: live registry state mirrored from session/jobs frames
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-conversation, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)

### client/ui-layout — 675 行 / 9 文件

- npm: `@deepseek-ai/dsh-client-ui-layout`
- 层: -1
- 自述: Shell plugin: three-column AppFrame with drag handles, ctx.layout viewing-state service (navigation + panels)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-theme, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: LayoutController, ThemePresenter
- 接口: Columns, ConvOwnerProps, DetailsOwnerProps, ILayout, SidebarOwnerProps

### client/ui-message-feedback — 939 行 / 8 文件

- npm: `@deepseek-ai/dsh-client-ui-message-feedback`
- 层: -1
- 自述: Per-message feedback controls contributed to the assistant-message action strip, backed by the messageFeedback Host Remote
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-api-remotes, @deepseek-ai/dsh-client-connection, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-conversation, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-message-feedback, @deepseek-ai/dsh-typert-protocol
- README: English \| [中文](README.zh.md)
- 类: MessageFeedbackController
- 接口: MessageFeedbackInjected, MessageFeedbackRemote, MessageFeedbackView

### client/ui-model-selection — 937 行 / 9 文件

- npm: `@deepseek-ai/dsh-client-ui-model-selection`
- 层: -1
- 自述: Model selection: the /model popupSelect over session.models / session.selectModel
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-api-remotes, @deepseek-ai/dsh-client-connection, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-commands, @deepseek-ai/dsh-client-ui-conversation, @deepseek-ai/dsh-client-ui-input-trigger, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: ModelDirectory, ModelDirectoryResolver
- 接口: ModelDirectoryState, ModelSelectInjected

### client/ui-permission-presets — 626 行 / 8 文件

- npm: `@deepseek-ai/dsh-client-ui-permission-presets`
- 层: -1
- 自述: Permission surfaces: a new-session default in General settings and a current-session /permission popup over the permissions projection
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-api-remotes, @deepseek-ai/dsh-client-connection, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-commands, @deepseek-ai/dsh-client-ui-input-trigger, @deepseek-ai/dsh-client-ui-settings, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-permission-presets
- README: English \| [中文](README.zh.md)
- 类: PermissionPresetSettingsController
- 接口: PermissionDefaultOption, PermissionRowInjected, PermissionSettingsState

### client/ui-plan — 201 行 / 6 文件

- npm: `@deepseek-ai/dsh-client-ui-plan`
- 层: -1
- 自述: Plan-mode composer control: the conversation.input.plan seat over the plan projection and the /plan command channel
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-api-remotes, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-conversation, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-plan-mode
- README: English \| [中文](README.zh.md)
- 接口: PlanChipInjected

### client/ui-primitives — 6870 行 / 46 文件

- npm: `@deepseek-ai/dsh-client-ui-primitives`
- 层: 1
- 自述: Pure React atoms for the dsh web UI: controls, icons, markdown, and JSON inspectors (zero cordis)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: IncrementalMarkdownParser
- 接口: AnchoredPositionOptions, AnsiSpan, BrandWordmarkProps, CodeBlockProps, CopyFeedback, DiffBlockProps, DiffHunk, DisclosureRowProps, HeadTailCap, HighlightSpan, IconProps, IncrementalBlocks, JsonTreeLabels, JsonTreeProps, MarkdownCodeLabels, MarkdownFileMentions, MarkdownPlainTextOptions, MarkdownRenderContext, MenuItem, MenuLabel, MenuSeparator, PointerGrace, PositionedBlock, ReadBlockLine, ReadBlockProps, ReferenceTargets, RiskConfirmationProps, SearchBlockLineMatch, SearchFileGroup, SearchMatchesBlockProps, SearchPathsBlockProps, TerminalBlockLabels, TerminalBlockProps, WebFetchBlockProps, WebSearchBlockProps, WebSourceView

### client/ui-reference — 215 行 / 5 文件

- npm: `@deepseek-ai/dsh-client-ui-reference`
- 层: -1
- 自述: Unified Web @file and @session reference source
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-api-remotes, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-input-trigger, @deepseek-ai/dsh-file-reference, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session-reference, @deepseek-ai/dsh-typert-protocol
- README: English \| [中文](README.zh.md)

### client/ui-renderer — 1280 行 / 9 文件

- npm: `@deepseek-ai/dsh-client-ui-renderer`
- 层: -1
- 自述: Browser UI renderer: React slot bindings, ctx.uiRenderer, and the assembled application root
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: SlotAssemblyError
- 接口: AssemblyDeps, DocumentTitleProps, SessionProviderProps, UiRendererService

### client/ui-settings — 907 行 / 8 文件

- npm: `@deepseek-ai/dsh-client-ui-settings`
- 层: -1
- 自述: Settings domain base plugin: the settings-namespace scope service and the canonical settings slot-type contract
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-api-remotes, @deepseek-ai/dsh-client-connection, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-settings, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: SettingsDescribeMirror, SettingsSchemaService, SettingsScopeBinder, SettingsScopeController
- 接口: SettingsDescribeFace, SettingsDescribeView, SettingsGeneralItemOwnerProps, SettingsHeaderOwnerProps, SettingsMirrorSnapshot, SettingsOnboardingOwnerProps, SettingsPluginsTabOwnerProps, SettingsSectionOwnerProps, SettingsTriggerOwnerProps

### client/ui-settings-general — 715 行 / 11 文件

- npm: `@deepseek-ai/dsh-client-ui-settings-general`
- 层: -1
- 自述: Settings ownerless-copy and product onboarding plugin: the General section, shell trigger/header chrome content, settings dictionaries, and the versioned welcome notice
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-api-remotes, @deepseek-ai/dsh-client-connection, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-settings, @deepseek-ai/dsh-client-ui-sidebar, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-settings, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: SettingsDocumentStore
- 接口: SettingsDocumentActionInjected, SettingsDocumentState, SettingsOnboardingStep, SettingsSectionRow

### client/ui-settings-models — 3449 行 / 19 文件

- npm: `@deepseek-ai/dsh-client-ui-settings-models`
- 层: -1
- 自述: Models settings and shared product-onboarding dialogs over existing settings and credential joins
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-api-remotes, @deepseek-ai/dsh-client-connection, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-settings, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: ModelsSettingsStore, WelcomeNoticeStore
- 接口: CustomProviderCardProps, DeepSeekModelsEditorProps, DeepSeekModelsValidationFailure, DeepSeekOnboardingInjected, EditorFooterProps, ModelListEditorProps, ModelsSectionInjected, ModelsSettingsState, ProbeTarget, ProviderEditorProps, ProviderIdentity, ProviderRow, WelcomeNoticeInjected, WelcomeNoticeState

### client/ui-settings-plugin-inventory — 319 行 / 6 文件

- npm: `@deepseek-ai/dsh-client-ui-settings-plugin-inventory`
- 层: -1
- 自述: Read-only Cordis Loader inventory tab in Web Plugins settings
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-api-remotes, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-settings, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 接口: PluginInventorySettingsTabInjected

### client/ui-settings-plugins — 1671 行 / 18 文件

- npm: `@deepseek-ai/dsh-client-ui-settings-plugins`
- 层: -1
- 自述: Plugins settings section with feature-owned tabs and configurable host-plane plugin cards
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-api-remotes, @deepseek-ai/dsh-client-connection, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-settings, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: AgentLoopCardController, BashCardController, CardForm, ConfigurablePluginsTabController, WebSearchCardController
- 接口: AgentLoopCardFace, AgentLoopCardState, AgentLoopSettings, BashCardFace, BashCardState, BashSettings, CardActions, CardFieldSpec, CardFieldState, CardSecretSpec, CardShell, ConfigurablePluginsTabFace, ConfigurablePluginsTabState, FieldProps, PluginCardProps, PluginsSettingsSectionInjected, PluginsSettingsTabEntry, SettingsPluginItemOwnerProps, WebSearchCardFace, WebSearchCardState, WebSearchSettings

### client/ui-sidebar — 445 行 / 7 文件

- npm: `@deepseek-ai/dsh-client-ui-sidebar`
- 层: -1
- 自述: Sidebar plugin: session multi-level tree, search, grouping, state dots
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-layout, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 接口: SidebarBrandMarkOwnerProps, SidebarBrandNameOwnerProps, SidebarFooterActionOwnerProps, SidebarSectionOwnerProps, SidebarSettingsOwnerProps

### client/ui-skill — 431 行 / 6 文件

- npm: `@deepseek-ai/dsh-client-ui-skill`
- 层: -1
- 自述: Web skill references and the dedicated skill tool row
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-api-remotes, @deepseek-ai/dsh-client-connection, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-input-trigger, @deepseek-ai/dsh-client-ui-tool, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)

### client/ui-slots — 1569 行 / 5 文件

- npm: `@deepseek-ai/dsh-client-ui-slots`
- 层: 1
- 自述: Slot registry pure core: SlotMap declaration merging, single register composition API, four-share props types, store-seat types, renderer install seam
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: SlotCore, SlotOwnershipError, StaleAuthorizationError
- 接口: ChainRenderOpts, GlobalStandardProps, HostObservable, LiveSlotNode, LiveSlotOccupant, LocaleFace, LocaleNamespaceMap, RenderOpts, RenderOpts, SessionAreaProps, SessionMaybeProvideInfo, SessionMaybeStandardProps, SessionProvideInfo, SessionStandardProps, SlotEntryDef, SlotMap, SlotRenderer, SlotRendererHost, StoreHandle, StoreInstance, StoreInstanceLike, StoreSpec, StoredEntry

### client/ui-subagent — 1087 行 / 7 文件

- npm: `@deepseek-ai/dsh-client-ui-subagent`
- 层: -1
- 自述: Subagent conversation catalog, continuation routing UI, and '@' reference source
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-conversation, @deepseek-ai/dsh-client-ui-input-trigger, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-token-meter
- README: English \| [中文](README.zh.md)
- 接口: SubagentCatalogInjected, SubagentReadOnlyMatch

### client/ui-theme — 721 行 / 10 文件

- npm: `@deepseek-ai/dsh-client-ui-theme`
- 层: -1
- 自述: Theme plugin: Host bootstrap for the pre-plugin palette; DOM-free ThemeRuntime for light/dark/system state; --dsw-* token styles and Appearance settings row
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-api-remotes, @deepseek-ai/dsh-client-connection, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-settings, @deepseek-ai/dsh-host-webserver, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-settings, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: ThemeRuntime
- 接口: AppearanceRowInjected, AppearanceRowState, ThemeDefinition, ThemeSettings, ThemeSnapshot, ThemeTokenInspection, ThemeTokenModes

### client/ui-tool — 2333 行 / 25 文件

- npm: `@deepseek-ai/dsh-client-ui-tool`
- 层: -1
- 自述: Client Tool call-tree renderer and keyed per-tool presentation slot
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-api-remotes, @deepseek-ai/dsh-client-connection, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-conversation, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 接口: DiffCardModel, GenericToolCardProps, PlanItemLike, PlanSummary, SearchCardModel, TerminalCardModel, ToolCallOwnerProps, ToolRowModel, ToolRowProps

### client/ui-trajectory — 7898 行 / 28 文件

- npm: `@deepseek-ai/dsh-client-ui-trajectory`
- 层: -1
- 自述: Trajectory event ledger with an interactive timing overview: pure-consumer plugin registering into the conversation ViewMap (no service)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-conversation, @deepseek-ai/dsh-compaction, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-tools
- README: English \| [中文](README.zh.md)
- 类: TrajectorySearchIndex, TrajectorySnapshotBuilder
- 接口: AssistantMetricDetail, TrajectoryCellProps, TrajectoryConversationViewNode, TrajectoryGroupHeaderProps, TrajectoryGroupModel, TrajectoryLayoutInput, TrajectoryRequestHeaderState, TrajectorySnapshot, TrajectorySourceBlock, TrajectoryTableProps, TrajectoryTimeRange, TrajectoryTimelineModel, TrajectoryTimelineProps, TrajectoryTimelineSpan, TrajectoryTimelineTurnBoundary, TrajectoryToolbarProps, TrajectoryTurnHeaderProps, TrajectoryTurnModel, TrajectoryTurnProps, TrajectoryUsage, TrajectoryViewInjected, TrajectoryVirtualRow, TrajectoryVirtualRowEntry, VirtualizableTrajectoryRecord

### client/ui-user-questions — 810 行 / 8 文件

- npm: `@deepseek-ai/dsh-client-ui-user-questions`
- 层: -1
- 自述: Web ask_user_question feature: host tool mount plus composer-takeover question UI
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-api-remotes, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-conversation, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: PendingQuestion
- 接口: PlanReview

### client/ui-workflow-run — 774 行 / 7 文件

- npm: `@deepseek-ai/dsh-client-ui-workflow-run`
- 层: -1
- 自述: Durable workflow-run Conversation Node and nested member disclosure for dsh web
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-conversation, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-tool-workflow, @deepseek-ai/dsh-workflow
- README: English \| [中文](README.zh.md)
- 接口: WorkflowRunChatData, WorkflowRunInjected, WorkflowRunMemberData, WorkflowRunPhaseData

### client/ui-workspace — 3020 行 / 11 文件

- npm: `@deepseek-ai/dsh-client-ui-workspace`
- 层: -1
- 自述: Workspace picker plugin: one WorkspacePicker registered into the sidebar and empty-state workspace slots
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-client-connection, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-conversation, @deepseek-ai/dsh-client-ui-sidebar, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 接口: DirectoryFlowOwnerProps, GroupNode, RelativeTime, RowDragProps, SearchResultNode, SearchResultSet, SessionNode, TreeView, WorkspacePickFlowProps

### client/web — 401 行 / 8 文件

- npm: `@deepseek-ai/dsh-client-web`
- 层: 1
- 自述: Web boot kernel: static module table, Cordis loader, framework-free boot page, and UI-renderer handoff
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: AppWebEntry, BootPage


## code-runtime

### code-runtime/code-runtime — 294 行 / 3 文件

- npm: `@deepseek-ai/dsh-code-runtime`
- 层: 1
- 自述: Abstract code-execution seam (ctx.codeRuntime) for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: CodeRuntime
- 接口: CodeBindingErrorClass, CodeBindingNamespace, CodeRunFailure, CodeRunRequest, CodeRunResult

### code-runtime/code-runtime-python — 722 行 / 4 文件

- npm: `@deepseek-ai/dsh-code-runtime-python`
- 层: 1
- 自述: CPython subprocess implementation of the DeepSeek Harness code-execution seam
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 接口: BootMessage

### code-runtime/code-runtime-worker-thread — 1725 行 / 8 文件

- npm: `@deepseek-ai/dsh-code-runtime-worker-thread`
- 层: 5
- 自述: Worker-thread implementation of the DeepSeek Harness code-execution seam
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-code-runtime, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-timeout, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: LogBuffer, WorkerThreadCodeRuntime
- 接口: BootstrapPort, Config, DoneMessage, PatchableStream, PendingCall, WorkerBootData


## compaction

### compaction/command-compact — 136 行 / 2 文件

- npm: `@deepseek-ai/dsh-command-compact`
- 层: 8
- 自述: Human-facing slash command for explicit session compaction
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-commands, @deepseek-ai/dsh-compaction, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)

### compaction/compaction — 805 行 / 7 文件

- npm: `@deepseek-ai/dsh-compaction`
- 层: 7
- 自述: Abstract compaction service seam (ctx.compaction) for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-commands, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session
- README: English \| [中文](README.zh.md)
- 类: CompactionEngine, ManualCompactionError
- 接口: CompactionAgentContext, CompactionResult, ManualCompactAgentContext

### compaction/compaction-basic — 1621 行 / 6 文件

- npm: `@deepseek-ai/dsh-compaction-basic`
- 层: 10
- 自述: Token-meter-driven compaction policy and LLM summarization backend for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-commands, @deepseek-ai/dsh-compaction, @deepseek-ai/dsh-compaction-tool-result-pruner, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-token-meter, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: BasicCompactionEngine, TargetPressureConfigError
- 接口: BasicCompactionConfig, CompactionPolicyConfig, ModelCompactPolicyConfig, SummarizationInput

### compaction/compaction-tool-result-pruner — 331 行 / 4 文件

- npm: `@deepseek-ai/dsh-compaction-tool-result-pruner`
- 层: 9
- 自述: Replay-safe model-free head/middle/tail pruning for tool-result surface nodes
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-compaction, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-token-meter, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: ToolResultPruner
- 接口: PruneResult, PrunedEntry, ResolvedConfig, ToolResultPruneConfig


## context

### context/agent-instructions — 1863 行 / 7 文件

- npm: `@deepseek-ai/dsh-agent-instructions`
- 层: 8
- 自述: Workspace context loader for AGENTS.md/CLAUDE.md instruction files
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-fs, @deepseek-ai/dsh-home-paths, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: AgentInstructionChange, AgentInstructionSource, ChangeRenderItem, Config, InstructionFile, InstructionVersionState, InstructionVersionUpdate, LoadedInstructionFile, ProbedInstructionFile, ReconciledInstructionContext, RenderedInstructionSet, RenderedWorkspaceContext, ResolvedConfig, ResolvedDiscoveryConfig, TruncatedInstruction

### context/file-reference — 161 行 / 4 文件

- npm: `@deepseek-ai/dsh-file-reference`
- 层: 6
- 自述: File-reference discovery contract and shared @file grammar
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-typert-protocol
- README: English \| [中文](README.zh.md)
- 类: FileReferenceService
- 接口: ActiveAtToken, FileReferenceCandidate

### context/file-reference-local — 464 行 / 3 文件

- npm: `@deepseek-ai/dsh-file-reference-local`
- 层: 8
- 自述: Local-filesystem ctx.fileReferences provider with bounded fuzzy indexes
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-file-reference, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: LocalFileReferenceService, WorkspaceFileSearch
- 接口: Config, FileSearchConfig

### context/session-reference — 807 行 / 7 文件

- npm: `@deepseek-ai/dsh-session-reference`
- 层: 8
- 自述: Cross-session snapshot references and durable untrusted model context (ctx.sessionReferenceResolver)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-compaction, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-output-retention, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-query, @deepseek-ai/dsh-typert-protocol, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: SessionReferenceError, SessionReferenceResolver
- 接口: Config, ParsedSessionReferenceText, PreparedReferencedMessage, ReferenceRetentionStats, ReferencedConversationItem, ReferencedSessionData, SessionReferenceCandidate, SessionReferenceInput, SessionReferenceMentionCandidate, SessionReferenceSource

### context/time-context — 545 行 / 5 文件

- npm: `@deepseek-ai/dsh-time-context`
- 层: 6
- 自述: Opt-in durable per-step context with the current time and elapsed time
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config

### context/tmux-context — 277 行 / 2 文件

- npm: `@deepseek-ai/dsh-tmux-context`
- 层: 7
- 自述: Opt-in durable per-step context with this agent's tmux pane and window location
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-shell, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config


## core

### core/agent — 1661 行 / 9 文件

- npm: `@deepseek-ai/dsh-agent`
- 层: 5
- 自述: Agent interface, registry, initiator scope, and event vocabulary for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-session, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-typert-protocol
- README: English \| [中文](README.zh.md)
- 类: AgentRegistry, Inbox
- 接口: Agent, AgentEventDispatch, AgentFactory, AgentHandle, AgentOptions, AgentSetupCommit, CancelOptions, ConsumedWork, CreateAgentOptions, InboxNotifications, ModelSelection, ModelSelectionRef, ResumeAgentOptions

### core/agent-default-model — 162 行 / 3 文件

- npm: `@deepseek-ai/dsh-agent-default-model`
- 层: 6
- 自述: Default model selection shared by Agent entry points
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-settings, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: AgentDefaultModelConfig
- 接口: AgentDefaultModelSettings, Config

### core/agent-loop — 1687 行 / 7 文件

- npm: `@deepseek-ai/dsh-agent-loop`
- 层: 8
- 自述: The concrete agent loop plugin for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-settings, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: AgentLoop, ReactLoopAgent, RuntimeContextProjection
- 接口: AgentLoopSettings, Config, ConfiguredAgentIdentities, LauncherAgentIdentity

### core/agent-tool-presentation — 104 行 / 2 文件

- npm: `@deepseek-ai/dsh-agent-tool-presentation`
- 层: 8
- 自述: Agent-plane presentation selector: composes one agent's tools as Code Mode, native, or both
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config

### core/scope — 588 行 / 5 文件

- npm: `@deepseek-ai/dsh-scope`
- 层: 1
- 自述: Scoped-context registration primitive (scope tags, scope-filtered event dispatch) for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: AnonymousEntries, NamedEntries, ScopedLayers
- 接口: CreateScopeOptions, Scope, ScopeLayer, ScopeParentBinding

### core/session — 3189 行 / 11 文件

- npm: `@deepseek-ai/dsh-session`
- 层: 4
- 自述: Event-sourced session store for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-typert-protocol
- README: English \| [中文](README.zh.md)
- 类: Session, SessionForkError, SessionPreparation, SessionStore, SurfaceManager
- 接口: CreateSessionOptions, EpochHeader, RequestContext, RestoredSessionOptions, SessionEventMap, SessionHeader, SessionPreparationOptions, SessionSurface, SurfaceFoldReplacement, SurfaceFoldResult, SurfaceIntent, TodoItem, TurnEndReasonMap

### core/system-prompt — 605 行 / 2 文件

- npm: `@deepseek-ai/dsh-system-prompt`
- 层: 4
- 自述: System prompt assembly registry for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-scope, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: SystemPrompt
- 接口: AssembleContext, AssembledContext, AssembledSection, Config, PromptAssembly, PromptContext, PromptSection, ToolProviderResult

### core/tools — 5628 行 / 10 文件

- npm: `@deepseek-ai/dsh-tools`
- 层: 7
- 自述: Tool registry and execution pipeline for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-code-runtime, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-session, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-user-approval, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: CodeRunFailedError, JsonSchemaError, ToolArgsError, ToolNotFoundError, ToolOutputError, ToolRuntime
- 接口: ArrayValueSchemaSpec, BooleanValueSchemaSpec, CodeDispatchEventData, CodeDispatchLog, CodeDispatchStartEventData, Config, DefineToolOptions, DiffCallView, DiffResultView, FileDiff, FileLocation, GenericCallView, GenericResultView, IntegerValueSchemaSpec, JsonSchemaNode, JsonValueSchemaSpec, NullValueSchemaSpec, NumberValueSchemaSpec, ObjectValueSchemaSpec, OneOfValueSchemaSpec, ParameterJsonSchema, ReadFileLine, ReadResultView, RunCodeBridgeOptions, SearchFileMatches, SearchLineMatch, SearchMatchesResultView, SearchPathsResultView, StringValueSchemaSpec, TerminalCallView, TerminalResultView, ToolDefinition, ToolDispatchExecution, ToolErrorInfo, ToolExecution, ToolExecutionFailure, ToolExecutionInput, ToolExecutionSuccess, ToolFailure, ToolOutputDefinition, ToolRestriction, ToolResult, ToolRunContext, ToolRuntimeScheduler, ToolSdkSchema, ValueSchemaAnnotations, WebFetchResultView, WebSearchResultView, WebSource


## credentials

### credentials/authorization — 573 行 / 3 文件

- npm: `@deepseek-ai/dsh-authorization`
- 层: 4
- 自述: Authorization seam (ctx.authorization): plugin-owned flows that obtain a credential through a conversation with the human
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-credentials, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm
- README: English \| [中文](README.zh.md)
- 类: AuthorizationDeclinedError, AuthorizationError, AuthorizationService
- 接口: AuthorizationEntry, AuthorizationFlow, AuthorizationInteraction, AuthorizationMethod, AuthorizationNotice, AuthorizationOutcome, AuthorizationPromptOption, AuthorizationRequest, AuthorizationSession

### credentials/credentials — 450 行 / 3 文件

- npm: `@deepseek-ai/dsh-credentials`
- 层: 2
- 自述: Abstract credential seam (ctx.credentials): settings carry references to secrets, providers own the values
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: CredentialProvider
- 接口: ApiKeyRecord, CredentialInfo, CredentialRecordEntry, CredentialRecordInfo, GrantRecord, ResolvedCredential

### credentials/credentials-local — 966 行 / 2 文件

- npm: `@deepseek-ai/dsh-credentials-local`
- 层: 3
- 自述: File-backed credentials provider ($DSH_HOME/.env under the live process environment) for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-atomic-write, @deepseek-ai/dsh-credentials, @deepseek-ai/dsh-home-paths, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-launch-environment, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: LocalCredentialProvider
- 接口: Config, CredentialsDocument


## e2b

### e2b/e2b — 212 行 / 2 文件

- npm: `@deepseek-ai/dsh-e2b`
- 层: 1
- 自述: Shared E2B sandbox lifecycle for DeepSeek Harness provider adapters
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: E2BRuntime
- 接口: Config

### e2b/fs-e2b — 612 行 / 2 文件

- npm: `@deepseek-ai/dsh-fs-e2b`
- 层: 7
- 自述: E2B filesystem implementation for DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-e2b, @deepseek-ai/dsh-fs, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: E2BFileSystem

### e2b/subprocess-e2b — 1835 行 / 7 文件

- npm: `@deepseek-ai/dsh-subprocess-e2b`
- 层: 2
- 自述: E2B subprocess implementation for DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-e2b, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-subprocess, @deepseek-ai/dsh-timeout, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: E2BBase64Decoder, E2BOutputReader, E2BSubprocessHandle, E2BSubprocessRuntime, E2BTerminalHandle
- 接口: Config


## examples

### examples/acp-demo — 225 行 / 4 文件

- npm: `@deepseek-ai/dsh-acp-demo`
- 层: 11
- 自述: ACP automation server app: agent spine + JSONL persistence + ACP transport, with a JSON-RPC stdio bin
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-include, @deepseek-ai/cordis-plugin-loader, @deepseek-ai/dsh-acp, @deepseek-ai/dsh-agent-instructions, @deepseek-ai/dsh-agent-spine-demo, @deepseek-ai/dsh-app-boot, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session-checkpoint-policy, @deepseek-ai/dsh-session-persistence-jsonl, @deepseek-ai/dsh-session-query, @deepseek-ai/dsh-session-query-sqlite, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config

### examples/agent-spine-demo — 295 行 / 2 文件

- npm: `@deepseek-ai/dsh-agent-spine-demo`
- 层: 10
- 自述: The default executor-less/UI-less agent spine with fallback session titles, provider-routed retry, and optional persisted goals
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-timer, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-agent-instructions, @deepseek-ai/dsh-agent-loop, @deepseek-ai/dsh-goal, @deepseek-ai/dsh-goal-round-driver, @deepseek-ai/dsh-home-paths, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-jobs-local, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-llm-retry, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-title, @deepseek-ai/dsh-shell-env, @deepseek-ai/dsh-skill, @deepseek-ai/dsh-skill-filesystem, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tool-bash, @deepseek-ai/dsh-tool-goal, @deepseek-ai/dsh-tool-jobs, @deepseek-ai/dsh-tool-skill, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config, GoalConfig, SkillConfig

### examples/jsonrpc-demo — 139 行 / 6 文件

- npm: `@deepseek-ai/dsh-sdk-jsonrpc-demo`
- 层: 6
- 自述: Bin that boots an external Cordis config for the stdio JSON-RPC SDK runtime
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-app-boot, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)


## experimental

### experimental/agent-team — 2352 行 / 15 文件

- npm: `@deepseek-ai/dsh-experimental-agent-team`
- 层: 9
- 自述: Implicit-root Agent Teams roster, durable peer mailbox, and shared task DAG
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-subagent, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: TeamActivity, TeamError, TeamJournal, TeamMailbox, TeamRoster, TeamRuntimeLifecycle, TeamService, TeamTaskBoard, TeamTaskGraphError
- 接口: Config, CreateTeamTaskRequest, SendTeamMessageRequest, SendTeamMessageResult, SpawnTeammateRequest, SpawnTeammateResult, TeamFoldState, TeamMemberSnapshot, TeamMemberView, TeamMembership, TeamMessageSnapshot, TeamMessageSource, TeamTaskSnapshot, TeamTaskView, TeamWaitResult, UpdateTeamTaskRequest

### experimental/tool-agent-team — 436 行 / 2 文件

- npm: `@deepseek-ai/dsh-experimental-tool-agent-team`
- 层: 10
- 自述: Scoped model-facing Agent Teams tools over ctx.agentTeams
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-experimental-agent-team, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config


## extensions

### extensions/cordis-client-runner — 5249 行 / 13 文件

- npm: `@deepseek-ai/dsh-cordis-client-runner`
- 层: -1
- 自述: Browser half of dynamic dual-half plugin packages: event subscription, closure evaluation, guard facade, and loader entries
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-loader, @deepseek-ai/dsh-api-remotes, @deepseek-ai/dsh-client-connection, @deepseek-ai/dsh-client-modules, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-theme, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: ClientCordisInspectRegistry, ClientTimerService, CordisRunOrchestrator, DynamicCordisPackageRunner, DynamicCordisStyles
- 接口: ApiParameter, ClientCordisInspectHost, ClientCordisInspectProviderRegistration, ClientCordisInspectQueryContext, ClientSlotEntry, ClientSlotOption, CordisErrorDetails, CordisObservable, CordisRunFailure, CordisRunHostSeam, CordisRunOrchestratorEnv, CordisRunRequest, CordisRunnerFace, CordisUserRunRequest, DynamicCordisClientHalf, DynamicCordisClosureEnv, DynamicCordisEvaluatedPlugin, DynamicCordisGuardEnv, DynamicCordisLivePackage, DynamicCordisRenderFailure, DynamicCordisRunnerEnv, DynamicCordisSlotLedgerRow, EventApiEntry, InheritedApiEntry, ServiceApiEntry, ServiceApiMethod, TypeApiEntry

### extensions/cordis-host-runner — 3387 行 / 8 文件

- npm: `@deepseek-ai/dsh-cordis-host-runner`
- 层: 8
- 自述: Dynamic package definition registry, host-half sandbox lifecycle, and invoke handler table for model-mounted dual-half packages
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-session, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-typert-protocol, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: CordisInspectRegistryService, DynamicCordisRegistry, DynamicCordisRunnerService
- 接口: Config, CordisErrorDetails, CordisHalfState, CordisInspectMethodManifest, CordisInspectProviderManifest, CordisInspectProviderView, CordisInspectQueryRequest, CordisInspectQueryResolved, CordisInspectResolveAck, CordisRunDiagnostic, DynamicCordisClientSource, DynamicCordisDefineReceipt, DynamicCordisDefineRequest, DynamicCordisDefinition, DynamicCordisInventoryPackage, DynamicCordisInventoryRow, DynamicCordisPackage, DynamicCordisPackageInspection, DynamicCordisPendingRequest, DynamicCordisPlugin, DynamicCordisPluginInspection, DynamicCordisReference, DynamicCordisRenderFailure, DynamicCordisRequestResolved, DynamicCordisResolveAck, DynamicCordisRetracted, DynamicCordisRun, DynamicCordisRunAttempt, DynamicCordisRunRequest, DynamicCordisSnapshotRow, HostCordisInspectProviderRegistration, HostCordisInspectQueryContext

### extensions/tool-cordis — 6392 行 / 8 文件

- npm: `@deepseek-ai/dsh-tool-cordis`
- 层: 9
- 自述: Self-referential cordis toolset: inspect the live runtime, mount and dispose model-written plugins
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-cordis-host-runner, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-session, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools
- README: English \| [中文](README.zh.md)
- 接口: ApiParameter, EventApiEntry, InheritedApiEntry, ServiceApiEntry, ServiceApiMethod, TypeApiEntry

### extensions/ui-cordis — 1682 行 / 16 文件

- npm: `@deepseek-ai/dsh-client-ui-cordis`
- 层: -1
- 自述: Cordis dynamic-plugin definition card: the keyed cordis_define tool row with its run/stop switch
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-api-remotes, @deepseek-ai/dsh-client-connection, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-input-trigger, @deepseek-ai/dsh-client-ui-sidebar, @deepseek-ai/dsh-client-ui-tool, @deepseek-ai/dsh-cordis-client-runner, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: CordisRunCardRegistry
- 接口: CordisActionCard, CordisCardFace, CordisDefineCard, CordisDynamicPort, CordisInventory, CordisInventorySnapshot, CordisPanelFace, CordisRunCard, CordisRunCardFace, CordisRunCardPointer, CordisRunCardStore, CordisToolViewOwnerProps


## feedback

### feedback/command-feedback — 138 行 / 2 文件

- npm: `@deepseek-ai/dsh-command-feedback`
- 层: 7
- 自述: Log-only session feedback producer and human-facing slash command
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-anonymous-user-id, @deepseek-ai/dsh-commands, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-telemetry
- README: English \| [中文](README.zh.md)

### feedback/message-feedback — 647 行 / 4 文件

- npm: `@deepseek-ai/dsh-message-feedback`
- 层: 6
- 自述: Lifecycle-bound per-message rating and note sidecar for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-storage-domain, @deepseek-ai/dsh-typert-protocol, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: MessageFeedbackService
- 接口: Config, MessageFeedbackDeleteRequest, MessageFeedbackDeleteValue, MessageFeedbackItem, MessageFeedbackListRequest, MessageFeedbackListValue, MessageFeedbackNoteBlank, MessageFeedbackNoteTooLarge, MessageFeedbackPutRequest, MessageFeedbackRejected, MessageFeedbackSessionNotFound, MessageFeedbackSuccess, MessageFeedbackTargetNotFound, MessageFeedbackVersionConflict


## fs

### fs/fs — 503 行 / 3 文件

- npm: `@deepseek-ai/dsh-fs`
- 层: 6
- 自述: Abstract filesystem capability seam (ctx.fs) for the DeepSeek Harness — vocabulary types, the FileSystem service (text IO + optional version-guarded atomic mutations), and the fs/* policy event vocabulary
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-sandbox
- README: English \| [中文](README.zh.md)
- 类: FileSystem, FsError
- 接口: FsDirEntry, FsEditOutcome, FsEditRequest, FsInfo, FsPathInfo, FsTarget, FsWriteOutcome

### fs/fs-local — 1210 行 / 4 文件

- npm: `@deepseek-ai/dsh-fs-local`
- 层: 7
- 自述: Local-filesystem implementation of the DeepSeek Harness filesystem seam (ctx.fs)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-fs, @deepseek-ai/dsh-invariants, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: LocalFileSystem
- 接口: Config, FsIoInternals, LocalDirEntry, LocalTarget, PathInfo, PathLinkInfo

### fs/fs-observation-policy — 189 行 / 3 文件

- npm: `@deepseek-ai/dsh-fs-observation-policy`
- 层: 7
- 自述: File-context policy plugin for the DeepSeek Harness — observed-state, read-before-edit, and version-guarded write/edit added over the ctx.fs provider seam through the fs/* event gate (no service API)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-fs, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 接口: FsObservationActor

### fs/fs-sandbox — 254 行 / 3 文件

- npm: `@deepseek-ai/dsh-fs-sandbox`
- 层: 8
- 自述: Sandbox-enforcing implementation of the DeepSeek Harness filesystem seam: fences write/edit by the per-call sandbox mode (read-only denies mutation, workspace-write contains it to the workspace + temp roots) while reads pass through
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-fs, @deepseek-ai/dsh-fs-local, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-sandbox, @deepseek-ai/dsh-sandbox-policy
- README: English \| [中文](README.zh.md)
- 类: SandboxedFileSystem

### fs/tool-fs — 1517 行 / 12 文件

- npm: `@deepseek-ai/dsh-tool-fs`
- 层: 8
- 自述: Model-facing filesystem tools (read, write, edit) over the DeepSeek Harness filesystem seam (ctx.fs)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-attachment, @deepseek-ai/dsh-fs, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-sandbox, @deepseek-ai/dsh-sandbox-policy, @deepseek-ai/dsh-session, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-user-approval, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: FsSandboxController
- 接口: Config, EscalationSchemaFields, FileReadOutcome, FileTextLine, FsEscalationArgs, FsReadMeta, ImageReadValue, ReadToolCaps, ReadWindow, WindowResult

### fs/tool-fs-search — 1566 行 / 7 文件

- npm: `@deepseek-ai/dsh-tool-fs-search`
- 层: 8
- 自述: Model-facing filesystem discovery tools (glob, grep) backed by the packaged ripgrep binary (@vscode/ripgrep)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-output-retention, @deepseek-ai/dsh-session, @deepseek-ai/dsh-spill, @deepseek-ai/dsh-subprocess, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-timeout, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: SearchError
- 接口: Config, GlobInput, GlobSample, GlobToolCaps, GrepInput, GrepMatch, GrepToolCaps, RipgrepRun

### fs/tool-str-replace-editor — 553 行 / 2 文件

- npm: `@deepseek-ai/dsh-tool-str-replace-editor`
- 层: 8
- 自述: Model-facing view, create, literal replace, and line insert tool over the Harness filesystem service
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-fs, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-sandbox, @deepseek-ai/dsh-sandbox-policy, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config


## goal

### goal/command-goal — 226 行 / 2 文件

- npm: `@deepseek-ai/dsh-command-goal`
- 层: 7
- 自述: Human-facing slash command for persisted same-session goals
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-commands, @deepseek-ai/dsh-goal, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm
- README: English \| [中文](README.zh.md)

### goal/goal — 1316 行 / 8 文件

- npm: `@deepseek-ai/dsh-goal`
- 层: 6
- 自述: Event-sourced same-session goal state and lifecycle service for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-typert-protocol, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: GoalError, GoalService
- 接口: Config, CreateGoalRequest, CreateGoalResult, EditGoalRequest, FoldedGoal, GoalBlockReason, GoalChanged, GoalClearChangeMeta, GoalFoldState, GoalMessageSource, GoalProjection, GoalRef, GoalSnapshot, GoalSnapshotChangeMeta, GoalView, ResolvedConfig

### goal/goal-round-driver — 580 行 / 4 文件

- npm: `@deepseek-ai/dsh-goal-round-driver`
- 层: 7
- 自述: Race-fenced same-session goal-round driver
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-goal, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session
- README: English \| [中文](README.zh.md)

### goal/tool-goal — 517 行 / 4 文件

- npm: `@deepseek-ai/dsh-tool-goal`
- 层: 8
- 自述: Model-facing same-session goal tools with execution-time authority checks
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-goal, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config, GoalToolExecution


## guard

### guard/repeat-tool-reminder — 263 行 / 2 文件

- npm: `@deepseek-ai/dsh-repeat-tool-reminder`
- 层: 8
- 自述: Repeat-tool-call guard plugin: advisory reminders when an agent loops on identical tool calls
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config

### guard/timeout-policy — 111 行 / 2 文件

- npm: `@deepseek-ai/dsh-tool-call-timeout-policy`
- 层: 8
- 自述: Tool-call timeout policy: a tools/execute wrapper that arms a per-tool deadline on exec.signal and returns TOOL_TIMEOUT when it wins
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-timeout, @deepseek-ai/dsh-tools
- README: English \| [中文](README.zh.md)


## hooks

### hooks/hook-protocol — 854 行 / 9 文件

- npm: `@deepseek-ai/dsh-hook-protocol`
- 层: 7
- 自述: Shared Claude Code / Codex hook wire protocol: matcher engine, stdin/exit-code/stdout codec, multi-hook merge, and hook/* session events
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-shell
- README: English \| [中文](README.zh.md)
- 接口: CommandHook, DetachedRuns, HookInvocation, HookOutput, HookResultRecord, MatcherGroup, MergedHookOutcome, RunHookOptions, RunHookResult

### hooks/hooks-claude-code — 514 行 / 3 文件

- npm: `@deepseek-ai/dsh-hooks-claude-code`
- 层: 9
- 自述: Bridge plugin: run a Claude Code hooks.json / settings hook config on the DeepSeek Harness interception seams
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-hook-protocol, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config, ParsedClaudeConfig, SkippedHook, SubstitutionVars

### hooks/hooks-codex — 445 行 / 3 文件

- npm: `@deepseek-ai/dsh-hooks-codex`
- 层: 8
- 自述: Bridge plugin: run a Codex hooks.json hook config on the DeepSeek Harness interception seams
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-hook-protocol, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config, ParsedCodexConfig, SkippedHook


## host

### host/apiproxy — 8470 行 / 42 文件

- npm: `@deepseek-ai/dsh-host-apiproxy`
- 层: -1
- 自述: API gateway: the ApiProxy contract (api/), the fetch carrier pair (fetch/), and the host-side gateway plugin providing ctx.apiProxy
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-agent-default-model, @deepseek-ai/dsh-agent-presets, @deepseek-ai/dsh-api-remotes, @deepseek-ai/dsh-attachment, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-commands, @deepseek-ai/dsh-cordis-host-runner, @deepseek-ai/dsh-credentials, @deepseek-ai/dsh-goal, @deepseek-ai/dsh-host-directory-picker, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-jobs, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-native-command, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-session-projection-cache, @deepseek-ai/dsh-session-query, @deepseek-ai/dsh-session-title, @deepseek-ai/dsh-settings, @deepseek-ai/dsh-skill, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-user-approval, @deepseek-ai/dsh-user-questions, @deepseek-ai/dsh-workspace, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: AbstractApiClient, ApiProxyService, InProcessApiClient
- 接口: AgentPresetEntry, AgentPresetsApi, ApiProxy, ApiProxyDefaults, ApprovalResponsePayload, ClientRequest, ClientResponse, Config, ConfigurableProviderView, CredentialView, CredentialsApi, DirectoryEntry, DirectoryListing, DiscoveredModelView, DownloadsApi, EventsApi, GoalRef, GoalsApi, HistoryEntry, HostApi, IApiClient, JobView, LlmApi, ModelCatalogFailure, ModelCatalogModel, ModelProviderGroup, ModelReasoning, ModelReasoningEffort, ModelSelection, PathOpenerInternals, QuestionResponsePayload, QueuedInboxItem, RpcErrorDetailsMap, RpcMethodMap, RpcRequest, RpcResponse, ServerRequest, ServerResponse, SessionListMetadata, SessionLogExportDeps, SessionLogExportReady, SessionModels, SessionProjectionsBlock, SessionSearchItem, SessionSummary, SessionsApi, SettingsApi, SettingsNamespaceView, SettingsSecretView, SkillEntry, SkillsApi, SubagentCatalog, SubagentInterruptReceipt, SubagentPromptReceipt, SubagentsApi, WorkspaceApi, WorkspaceView

### host/directory-picker — 168 行 / 2 文件

- npm: `@deepseek-ai/dsh-host-directory-picker`
- 层: 1
- 自述: Abstract workspace-directory picking seam (ctx.directoryPicker) for the DeepSeek Harness web GUI host
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: DirectoryPicker, DirectoryPickerError
- 接口: DirectoryEntry, DirectoryListing, DirectoryPickerBrowseCapability, DirectoryPickerCapabilities, DirectoryPickerNativeCapability

### host/directory-picker-auto — 220 行 / 4 文件

- npm: `@deepseek-ai/dsh-host-directory-picker-auto`
- 层: -1
- 自述: Adaptive chooser of the directory-picker seam: resolves the host situation at boot and mounts the native or browse backend for the DeepSeek Harness web GUI host
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-loader, @deepseek-ai/dsh-client-ui-directory-picker-browse, @deepseek-ai/dsh-client-ui-directory-picker-native, @deepseek-ai/dsh-host-directory-picker-browse, @deepseek-ai/dsh-host-directory-picker-native, @deepseek-ai/dsh-host-webserver, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 接口: DirectoryPickerHostFacts

### host/directory-picker-browse — 364 行 / 3 文件

- npm: `@deepseek-ai/dsh-host-directory-picker-browse`
- 层: 2
- 自述: In-app browsing backend of the directory-picker seam (listing/creation primitives over the host filesystem)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-host-directory-picker, @deepseek-ai/dsh-invariants, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: BrowseDirectoryPicker
- 接口: Config, ListingCandidate

### host/directory-picker-native — 768 行 / 9 文件

- npm: `@deepseek-ai/dsh-host-directory-picker-native`
- 层: 2
- 自述: Native-OS-chooser backend of the directory-picker seam for the DeepSeek Harness web GUI host
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-host-directory-picker, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-native-command
- README: English \| [中文](README.zh.md)
- 类: NativeDirectoryPicker
- 接口: DirectoryPickerInternals, Win32DialogBindings, Win32DialogInternals, Win32DialogWorkerData, Win32DialogWorkerLike, Win32FolderDialog

### host/frontend-static — 155 行 / 2 文件

- npm: `@deepseek-ai/dsh-host-frontend-static`
- 层: 2
- 自述: SPA dist server for the Web shell: owns the webserver fallback seat, serving explicit index entries and static assets with traversal rejection and 404 misses
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-host-webserver, @deepseek-ai/dsh-invariants, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config

### host/plugin-inventory — 120 行 / 3 文件

- npm: `@deepseek-ai/dsh-host-plugin-inventory`
- 层: 2
- 自述: Read-only Remote projection of current Cordis Loader plugin state
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-loader, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-typert-protocol
- README: English \| [中文](README.zh.md)
- 类: PluginInventoryGateway
- 接口: PluginInventoryEntry, PluginInventorySnapshot

### host/webserver — 466 行 / 3 文件

- npm: `@deepseek-ai/dsh-host-webserver`
- 层: 1
- 自述: Web route-registration plugin: HTTP and upgrade routes, index transform taps, and static dist fallback; knows no harness concepts
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: WebServer
- 接口: Config, WebRoute, WebUpgradeRoute


## identity

### identity/anonymous-user-id — 131 行 / 2 文件

- npm: `@deepseek-ai/dsh-anonymous-user-id`
- 层: 2
- 自述: Shared anonymous user identity for DeepSeek Harness telemetry and feedback correlation
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-home-paths, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 接口: AnonymousUserIdOptions


## interaction

### interaction/commands — 661 行 / 4 文件

- npm: `@deepseek-ai/dsh-commands`
- 层: 6
- 自述: Plugin-owned human command registry for DeepSeek Harness UIs
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-attachment, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-session, @deepseek-ai/dsh-typert-protocol
- README: English \| [中文](README.zh.md)
- 类: CommandRuntime
- 接口: CommandDefinition, CommandDescriptor, CommandExecution, CommandInputDescriptor, CommandInvocation, CommandSourceMap, ParsedCommand

### interaction/permission-presets — 542 行 / 4 文件

- npm: `@deepseek-ai/dsh-permission-presets`
- 层: 7
- 自述: User-facing permission presets (ctx.permissionPresets) for the DeepSeek Harness: one product-level Permissions select bundling the sandbox-mode and approval-policy knobs, written through to their own session events
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-commands, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-sandbox, @deepseek-ai/dsh-sandbox-policy, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-settings, @deepseek-ai/dsh-shell, @deepseek-ai/dsh-user-approval, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: PermissionPresetService
- 接口: Config, KnobState, PermissionSelect, PermissionSettings, PresetOption, PresetSpec

### interaction/tool-ask-user — 131 行 / 2 文件

- npm: `@deepseek-ai/dsh-tool-ask-user`
- 层: 8
- 自述: Model-facing ask_user_question tool over the ctx.userQuestions seam
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-user-questions
- README: English \| [中文](README.zh.md)

### interaction/user-approval — 517 行 / 4 文件

- npm: `@deepseek-ai/dsh-user-approval`
- 层: 6
- 自述: User-approval seam (ctx.approval) for the DeepSeek Harness: one-shot permission decisions dispatched to composed answerers over the approval/request waterfall, fail-closed by default
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-session, @deepseek-ai/dsh-system-prompt, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: ApprovalService
- 接口: ApprovalRequest, Config

### interaction/user-questions — 239 行 / 3 文件

- npm: `@deepseek-ai/dsh-user-questions`
- 层: 6
- 自述: Abstract user-questions seam (ctx.userQuestions) for asking the human during agent runs
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm
- README: English \| [中文](README.zh.md)
- 类: UserQuestionError, UserQuestionService
- 接口: AskUserQuestionAnswer, AskUserQuestionAnswerItem, AskUserQuestionItem, AskUserQuestionOption, AskUserQuestionRequest, UserQuestionProvider


## jobs

### jobs/jobs — 424 行 / 4 文件

- npm: `@deepseek-ai/dsh-jobs`
- 层: 6
- 自述: Background job registry (ctx.jobs) for the DeepSeek Harness — shared ids, owner isolation, polling, cancellation, and completion listeners for long-running tool work
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session
- README: English \| [中文](README.zh.md)
- 类: JobRegistry
- 接口: JobHooks, JobKindMap, JobOutcome, JobRead, JobSnapshot, JobStart

### jobs/jobs-local — 567 行 / 2 文件

- npm: `@deepseek-ai/dsh-jobs-local`
- 层: 7
- 自述: Process-local implementation of the DeepSeek Harness background job registry seam
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-jobs, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-timeout, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: LocalJobRegistry
- 接口: Config

### jobs/tool-jobs — 432 行 / 2 文件

- npm: `@deepseek-ai/dsh-tool-jobs`
- 层: 8
- 自述: Model-facing background job control tools (job_output, job_list, job_kill) over the ctx.jobs registry
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-jobs, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-output-retention, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config, PublicJobSnapshot


## llm

### llm/llm — 2958 行 / 14 文件

- npm: `@deepseek-ai/dsh-llm`
- 层: 3
- 自述: Provider-neutral LLM service interface for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-attachment, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-timeout, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: BlockAssembler, HarnessError, LlmAdapter, LlmError, LlmRuntime
- 接口: AdapterRegistrationHandle, AlwaysRetryPolicyConfig, AppIdentity, AssistantMessage, AssistantProvenance, BackoffConfig, ContentBlockMap, ContextSnapshotSection, DirectoryRegistrationHandle, FinishReasonMap, GenerateOptions, ImageBlock, LlmCallConfig, LlmCallConfigAdapterDefaults, LlmConfigurableProvider, LlmDiscoveredModel, LlmErrorOptions, LlmFailure, LlmModelContext, LlmModelDiscoveryRequest, LlmModelInfo, LlmModelReasoningInfo, LlmProviderInfo, LlmReasoningEffortInfo, LlmResolvedModelInfo, Message, MessageSourceMap, ModelMessageSource, ModelModalityMap, NormalRetryPolicyConfig, PreparedAdapterCall, PreparedLlmCall, ReasoningBlock, ReplayEnvelope, RequestImageOffloadPolicy, ResolvedAlwaysRetryPolicy, ResolvedNormalRetryPolicy, ResolvedRetryBackoff, TextBlock, TokenUsage, ToolCallBlock, ToolMessageSource, ToolResultBlock, ToolResultMessage, ToolResultMessageInput, ToolSchema, UserMessage

### llm/llm-deepseek — 2831 行 / 11 文件

- npm: `@deepseek-ai/dsh-llm-deepseek`
- 层: 4
- 自述: DeepSeek chat-completions adapter for the DeepSeek Harness LLM seam
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-anonymous-user-id, @deepseek-ai/dsh-atomic-write, @deepseek-ai/dsh-attachment, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-credentials, @deepseek-ai/dsh-home-paths, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-launch-environment, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-settings, @deepseek-ai/dsh-timeout, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: DeepSeekAdapter, DeepSeekFileStore, DeepSeekFilesClient, DeepSeekFilesError, DeepSeekUploadIndex
- 接口: Config, DeepSeekAdapterOptions, DeepSeekCatalogModel, DeepSeekConnectionOptions, DeepSeekFileConnection, DeepSeekFileObject, DeepSeekFilePage, DeepSeekFilePolicy, DeepSeekFileReference, DeepSeekUploadRecord, ImageSerializationOptions, ImageWireLocation, RequestDefaults, UploadIndexCommit, WireAssistantMessage, WireChoice, WireChunk, WireDelta, WireError, WireFileContentPart, WireImageUrlContentPart, WireRequest, WireSystemMessage, WireTextContentPart, WireTool, WireToolCall, WireToolCallDelta, WireToolMessage, WireUsage, WireUserMessage

### llm/llm-pi-ai — 3710 行 / 12 文件

- npm: `@deepseek-ai/dsh-llm-pi-ai`
- 层: 5
- 自述: pi-ai-backed DeepSeek adapter for the DeepSeek Harness LLM seam (design-verification twin of dsh-llm-deepseek)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-attachment, @deepseek-ai/dsh-authorization, @deepseek-ai/dsh-credentials, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-launch-environment, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-settings, @deepseek-ai/dsh-timeout, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: PiAiAdapter
- 接口: Config, PiAiAdapterOptions, PiAiAuthInjection, PiAiCompatProfile, PiAiModelProfile, PiAiProviderProfile, PiAiReplayResponse, ProviderSpec, ResolvedPiAiProviderProfile, RouteCatalog, RouteCatalogRequest

### llm/llm-retry — 519 行 / 6 文件

- npm: `@deepseek-ai/dsh-llm-retry`
- 层: 6
- 自述: Provider-routed LLM request retry policy for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-timeout, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: LlmRetryStartedEventData, RetryInternals

### llm/token-meter — 1027 行 / 10 文件

- npm: `@deepseek-ai/dsh-token-meter`
- 层: 8
- 自述: Replay-aware token measurement service (ctx.tokenMeter) for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-compaction, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: TokenMeter
- 接口: ContextBreakdownProjection, ContextPressureProjection, ShadowPriceClaim, SurfaceTokenFold, SurfaceTokensFold, TokenMeasurement, TokenSurfaceNode, TokenUsageProjection


## lsp

### lsp/lsp — 339 行 / 4 文件

- npm: `@deepseek-ai/dsh-lsp`
- 层: 4
- 自述: Abstract LSP capability seam (ctx.lsp) for the DeepSeek Harness — language-server provider registry keyed by branded id and extension mapping, order-independent per-query selection, normalized definition/references/implementation/hover requests and results, and the LspError taxonomy
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm
- README: English \| [中文](README.zh.md)
- 类: Lsp, LspError
- 接口: LspHover, LspLocation, LspPosition, LspProvider, LspProviderQuery, LspQueryRequest, LspRange, LspService

### lsp/lsp-stdio — 1664 行 / 9 文件

- npm: `@deepseek-ai/dsh-lsp-stdio`
- 层: 7
- 自述: Generic stdio language-server provider for the DeepSeek Harness LSP capability seam (ctx.lsp) — spawns configured servers, translates JSON-RPC, and serves transient-open goToDefinition/findReferences/goToImplementation/hover queries in the host filesystem namespace
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-fs, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-lsp, @deepseek-ai/dsh-subprocess, @deepseek-ai/dsh-timeout, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: LspConnection, LspInstance, MessageDecoder
- 接口: Config, ConnectionSpec, HostSource, HostWorkspace, InstanceSpec, LspLocalServerConfig, WireHover, WireInitializeResult, WireLocation, WireLocationLink, WireMarkedStringObject, WireMarkupContent, WirePosition, WireRange, WireServerCapabilities, WireTextDocumentSyncOptions

### lsp/tool-lsp — 483 行 / 4 文件

- npm: `@deepseek-ai/dsh-tool-lsp`
- 层: 8
- 自述: Model-facing lsp tool over the DeepSeek Harness LSP capability seam (ctx.lsp) — one read-only tool with goToDefinition/findReferences/goToImplementation/hover operations, one-based UTF-16 cursor coordinates, bounded location rendering, and hover normalization
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-lsp, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-timeout, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config, LspToolArgs, LspToolInput


## mcp

### mcp/mcp-client — 1171 行 / 5 文件

- npm: `@deepseek-ai/dsh-mcp-client`
- 层: 8
- 自述: MCP client bridge: connects to MCP servers and registers their tools on ctx.tools
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-attachment, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-subprocess, @deepseek-ai/dsh-timeout, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: ConnectionHandle, ConnectionOutcome, ReconnectConfig, StdioConfig, StreamableHttpConfig, ToolBridgeOptions


## plan

### plan/plan-mode — 601 行 / 4 文件

- npm: `@deepseek-ai/dsh-plan-mode`
- 层: 8
- 自述: Logged per-agent plan mode with deployment guidance, a direct slash command, and a user-reviewed exit
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-commands, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-user-questions
- README: English \| [中文](README.zh.md)
- 类: PlanModeController
- 接口: PlanModeConfig, PlanProjection


## preset

### preset/agent-presets — 1684 行 / 9 文件

- npm: `@deepseek-ai/dsh-agent-presets`
- 层: 6
- 自述: Per-session agent composition from preset cordis.yml files for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-include, @deepseek-ai/cordis-plugin-loader, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-atomic-write, @deepseek-ai/dsh-home-paths, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-session, @deepseek-ai/dsh-settings, @deepseek-ai/dsh-system-prompt, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: AgentPresets, InvalidPresetIdError, PresetExistsError, PresetMountError, PresetNotWritableError, UnknownPresetError
- 接口: AgentPreset, AgentPresetSettings, Config, PresetBearingSession, PresetMetadata, PresetMount, PresetRoot

### preset/persona — 99 行 / 2 文件

- npm: `@deepseek-ai/dsh-persona`
- 层: 5
- 自述: Composition-authored deployment persona section for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-system-prompt, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config


## runtime-diagnostics

### runtime-diagnostics/invariants — 230 行 / 2 文件

- npm: `@deepseek-ai/dsh-invariants`
- 层: 0
- 自述: Registry service for package-owned DeepSeek Harness runtime invariants
- 依赖: @deepseek-ai/cordis, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: InvariantError, InvariantRegistry
- 接口: Config, InvariantInstaller


## sandbox

### sandbox/sandbox — 452 行 / 4 文件

- npm: `@deepseek-ai/dsh-sandbox`
- 层: 5
- 自述: Abstract process-sandbox seam (ctx.sandbox) for the DeepSeek Harness: same-world confinement vocabulary and the SandboxProvider contract
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session
- README: English \| [中文](README.zh.md)
- 类: SandboxProvider, SandboxUnavailableError
- 接口: ConfinedArgv, EscalationApproval, EscalationApprover, EscalationRequest, RunnerFailureRule, SandboxExecutionPolicy, SandboxPolicy

### sandbox/sandbox-local — 655 行 / 3 文件

- npm: `@deepseek-ai/dsh-sandbox-local`
- 层: 6
- 自述: Local process-sandbox backends for the DeepSeek Harness sandbox seam: bwrap, the npm-distributed landlock-run launcher, macOS Seatbelt, or the Windows ACL restricted-token runner — functionally probed, fail-closed
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-sandbox, @deepseek-ai/dsh-sandbox-windows-acl, @deepseek-ai/dsh-session, @deepseek-ai/node-addon-landlock-run, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: LocalSandboxProvider
- 接口: Config, SandboxInternals

### sandbox/sandbox-policy — 292 行 / 4 文件

- npm: `@deepseek-ai/dsh-sandbox-policy`
- 层: 6
- 自述: Per-call sandbox policy resolver and current model context: deployment fallbacks plus each session's mode and workspace root, shared by every enforcing capability family
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-sandbox, @deepseek-ai/dsh-session, @deepseek-ai/dsh-system-prompt, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: SandboxPolicyService
- 接口: Config, SandboxPolicyRequest

### sandbox/sandbox-windows-acl — 2546 行 / 13 文件

- npm: `@deepseek-ai/dsh-sandbox-windows-acl`
- 层: 1
- 自述: Windows ACL write-restriction sandbox backend (restricted-token spawn with capability-SID write allowlist) for the DeepSeek Harness sandbox seam
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: AclSandbox, AclWriteGrant, Win32Error
- 接口: AclSandboxChild, AclSandboxChildResult, AclSandboxOptions, AclSandboxSpawnOptions, ProcessInfoOutput, RestrictingSidSet, SpawnedInherited, SpawnedNative, StartupInfoInput, Win32Bindings


## schedule

### schedule/schedule — 2028 行 / 9 文件

- npm: `@deepseek-ai/dsh-schedule`
- 层: 8
- 自述: Agent-scoped durable after, at, and fixed-rate reminders over the session event log
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-tools
- README: English \| [中文](README.zh.md)
- 类: ScheduleInputError, ScheduleLogError, SchedulePersistenceError, ScheduleRuntime
- 接口: AfterScheduleRecord, AtScheduleRecord, CorruptScheduleLogError, EveryOccurrence, EveryScheduleDispatchChange, EveryScheduleRecord, FoldedSchedules, FrequencyTooHighError, InternalScheduleError, InvalidPromptError, InvalidRuleError, InvalidSelectorError, InvalidTimeZoneError, LocalAtInput, NotFutureError, OneShotScheduleDispatchChange, PersistenceUncertainError, ScheduleCreateChange, ScheduleDeleteChange, TimeOutOfRangeError


## sdk

### sdk/client — 952 行 / 6 文件

- npm: `@deepseek-ai/dsh-sdk-client`
- 层: 10
- 自述: TypeScript client SDK for driving a DeepSeek Harness runtime subprocess over stdio JSON-RPC: the DeepSeekHarness high-level turns API and the lower-level HarnessClient
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-sdk-protocol, @deepseek-ai/dsh-session
- README: English \| [中文](README.zh.md)
- 类: DeepSeekHarness, HarnessClient, HarnessSession, RequestTimeoutError, SdkProtocolError, TransportClosedError
- 接口: DeepSeekHarnessOptions, HarnessClientOptions, HarnessNotification, NotificationSubscription, RunOptions, RunResult

### sdk/protocol — 440 行 / 4 文件

- npm: `@deepseek-ai/dsh-sdk-protocol`
- 层: 9
- 自述: Shared wire protocol for the DeepSeek Harness SDK runtime: the newline-delimited JSON-RPC stdio transport and the named request, result, and notification types spoken between the runtime server and SDK clients
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-subagent
- README: English \| [中文](README.zh.md)
- 类: JsonRpcLineTransport, JsonRpcResponseError
- 接口: HarnessSdkNotificationMap, HarnessSdkRequestMap, InitializeParams, InitializeResult, JsonRpcTransportPeer, SessionEventNotification, SessionPromptParams, SessionPromptResult, SessionStatusNotification, SubagentFinishedNotification, SubagentStartedNotification

### sdk/server — 368 行 / 3 文件

- npm: `@deepseek-ai/dsh-sdk-jsonrpc-server`
- 层: 10
- 自述: Stdio JSON-RPC server plugin for out-of-process DeepSeek Harness SDK clients
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-llm-deepseek, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-sdk-protocol, @deepseek-ai/dsh-session, @deepseek-ai/dsh-subagent, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: HarnessSdkJsonRpcServer
- 接口: HarnessSdkJsonRpcServerOptions, JsonRpcConfig


## session

### session/session-checkpoint-policy — 113 行 / 2 文件

- npm: `@deepseek-ai/dsh-session-checkpoint-policy`
- 层: 8
- 自述: Semantic session durability checkpoints before model requests and tool side effects
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-tools
- README: English \| [中文](README.zh.md)

### session/session-persistence — 2160 行 / 6 文件

- npm: `@deepseek-ai/dsh-session-persistence`
- 层: 5
- 自述: Abstract durable session persistence seam (ctx.sessionPersistence) for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-timeout
- README: English \| [中文](README.zh.md)
- 类: PersistenceCoordinator, SessionFormatUnsupportedError, SessionPersistence, SessionPersistenceCorruptionError, SessionPreparations, SessionWriteBehind
- 接口: PersistenceBackend, PersistenceCoordinatorOptions, SessionInspection, SessionLocation, SessionPersistenceSnapshot, SessionPreparationReservation, SessionRawArtifact, SessionWriteBehindOptions, StoredPrefix, StoredSuffix

### session/session-persistence-jsonl — 1939 行 / 7 文件

- npm: `@deepseek-ai/dsh-session-persistence-jsonl`
- 层: 6
- 自述: JSONL durable session persistence backend for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: JsonlSessionPersistence, NodePrivateZstdFrameDecoder, PublicZstdFrameDecoder, SessionLogScanner
- 接口: Config, HeaderLine, ZstdFrameDecoder, ZstdFrameRange, ZstdFrameScan

### session/session-persistence-sqlite — 1742 行 / 7 文件

- npm: `@deepseek-ai/dsh-session-persistence-sqlite`
- 层: 6
- 自述: SQLite durable session persistence with physical chunk-row packing
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: SqliteSessionPersistence, SqliteStore
- 接口: BoundRecord, Config, EventRow, SessionRow, SqliteStoreOptions

### session/session-projection — 559 行 / 3 文件

- npm: `@deepseek-ai/dsh-session-projection`
- 层: 5
- 自述: Session-projection seam: the merge-extensible projection type table, the provider contract, and the ctx.sessionProjections registry serving whole current values of log-derived per-session state
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session
- README: English \| [中文](README.zh.md)
- 类: SessionProjectionRegistry
- 接口: ProjectionCheckpointRow, ProjectionDefinition, ProjectionSnapshot, SessionProjectionMap, SessionProjectionStateMap

### session/session-projection-cache — 404 行 / 3 文件

- npm: `@deepseek-ai/dsh-session-projection-cache`
- 层: 6
- 自述: Persisted projection cache (ctx.sessionProjectionCache): durable per-session projection checkpoints over the domain data form, throttled write-behind, and the cold-read ladder (cache row + persistence tail replay)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-storage-domain, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: SessionProjectionCache
- 接口: Config

### session/session-stats — 329 行 / 5 文件

- npm: `@deepseek-ai/dsh-session-stats`
- 层: 6
- 自述: Whole-log conversation counts and wall times projection (sessionStats) for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection
- README: English \| [中文](README.zh.md)
- 接口: SessionStatsProjection

### session/session-telemetry — 529 行 / 3 文件

- npm: `@deepseek-ai/dsh-session-telemetry`
- 层: 6
- 自述: SessionTelemetryBackend seam for the DeepSeek Harness: session-event capture, projection, redaction, and handoff to a reporting backend
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session
- README: English \| [中文](README.zh.md)
- 类: SessionTelemetryBackend, SessionTelemetryCoordinator
- 接口: SessionTelemetryRecord, SessionTelemetrySink

### session/session-telemetry-otel — 332 行 / 2 文件

- npm: `@deepseek-ai/dsh-session-telemetry-otel`
- 层: 8
- 自述: OpenTelemetry backend for the DeepSeek Harness telemetry seam: hands captured session records to the OTel JS SDK's log pipeline
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-anonymous-user-id, @deepseek-ai/dsh-command-feedback, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-telemetry, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: OpenTelemetrySessionBackend
- 接口: Config

### session/session-title — 952 行 / 5 文件

- npm: `@deepseek-ai/dsh-session-title`
- 层: 6
- 自述: Log-backed session title service and provider registry for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: SessionTitleInvalidError, SessionTitleService
- 接口: Config, SessionTitleEventData, SessionTitleModelProvenance, SessionTitleProvider, SessionTitleProviderRequest, SessionTitleProviderResult, SessionTitleSnapshot, SessionTitleUserMessage

### session/session-title-all-prompts-llm — 66 行 / 2 文件

- npm: `@deepseek-ai/dsh-session-title-all-prompts-llm`
- 层: 8
- 自述: All-user-messages LLM provider plugin for DeepSeek Harness session titles
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-title, @deepseek-ai/dsh-session-title-llm, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)

### session/session-title-first-prompt-llm — 70 行 / 2 文件

- npm: `@deepseek-ai/dsh-session-title-first-prompt-llm`
- 层: 8
- 自述: First-message LLM provider plugin for DeepSeek Harness session titles
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-title, @deepseek-ai/dsh-session-title-llm, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)

### session/session-title-llm — 324 行 / 2 文件

- npm: `@deepseek-ai/dsh-session-title-llm`
- 层: 7
- 自述: Shared LLM generation policy for DeepSeek Harness session-title providers
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-title, @deepseek-ai/dsh-timeout, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: ResolvedSessionTitleLlmConfig, SessionTitleLlmConfig, SessionTitleLlmRequestEventData


## session-query

### session-query/session-log-export — 347 行 / 8 文件

- npm: `@deepseek-ai/dsh-session-log-export`
- 层: -1
- 自述: Web Session-log export command and shared download dialog
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-commands, @deepseek-ai/dsh-client-ui-conversation, @deepseek-ai/dsh-commands, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: SessionLogDownloadController
- 接口: SessionLogDownloadDialogInjected, SessionLogDownloadEntry, SessionLogDownloadState

### session-query/session-query — 1718 行 / 11 文件

- npm: `@deepseek-ai/dsh-session-query`
- 层: 7
- 自述: Combined session query service contract with concrete reads, traces, and filters
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-session-title
- README: English \| [中文](README.zh.md)
- 类: SessionCorpus, SessionQueryEngine, SessionQueryError
- 接口: Config, LogicalSession, LogicalSessionSource, SessionEventReadRequest, SessionEventRecord, SessionEventSearchDocument, SessionEventSearchHit, SessionEventSearchPage, SessionEventSearchRequest, SessionEventTrace, SessionEventTraceObservation, SessionEventTraceRequest, SessionEventWindow, SessionLineageNode, SessionLogSnapshot, SessionRecord, SessionResultRange, SessionSearchExecContext, SessionSearchHit, SessionSearchPage, SessionSearchRequest, SessionSurfaceSnapshot, SessionTitleObservation

### session-query/session-query-sqlite — 1783 行 / 4 文件

- npm: `@deepseek-ai/dsh-session-query-sqlite`
- 层: 8
- 自述: Concrete ctx.sessionQuery backend with SQLite FTS5 search
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-session-query, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: SqliteSessionQueryEngine
- 接口: Config, NormalizedEventRequest, NormalizedSessionRequest, QueryLimits, SqlWhere

### session-query/tool-session-query — 1444 行 / 7 文件

- npm: `@deepseek-ai/dsh-tool-session-query`
- 层: 8
- 自述: Workspace-authorized model-facing session history search, trace, and event read tools
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-query, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-timeout, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config


## settings

### settings/settings — 1131 行 / 5 文件

- npm: `@deepseek-ai/dsh-settings`
- 层: 2
- 自述: Abstract user-settings seam (ctx.settings) for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: SettingsConflictError, SettingsProvider
- 接口: RedactedSecret, RedactedValue, SettingsDescribeOptions, SettingsDescriptor, SettingsRegisterOptions, SettingsScope, SettingsSectionHooks

### settings/settings-file — 401 行 / 2 文件

- npm: `@deepseek-ai/dsh-settings-file`
- 层: 3
- 自述: File-backed settings provider (settings.yaml) for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-atomic-write, @deepseek-ai/dsh-home-paths, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-settings, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: FileSettingsProvider
- 接口: Config


## shell

### shell/bash-local — 363 行 / 2 文件

- npm: `@deepseek-ai/dsh-bash-local`
- 层: 7
- 自述: Local-subprocess implementation of the DeepSeek Harness bash executor seam
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-settings, @deepseek-ai/dsh-shell, @deepseek-ai/dsh-subprocess, @deepseek-ai/dsh-timeout, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: LocalBashExecutor
- 接口: Config

### shell/bash-sandbox — 328 行 / 3 文件

- npm: `@deepseek-ai/dsh-bash-sandbox`
- 层: 8
- 自述: Sandbox-consuming implementation of the DeepSeek Harness bash executor seam (confines every command via ctx.sandbox, reports denial/enforcement result facts)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-bash-local, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-sandbox, @deepseek-ai/dsh-sandbox-policy, @deepseek-ai/dsh-shell
- README: English \| [中文](README.zh.md)
- 类: SandboxBashExecutor

### shell/pwsh-local — 472 行 / 3 文件

- npm: `@deepseek-ai/dsh-pwsh-local`
- 层: 7
- 自述: Local PowerShell implementation of the DeepSeek Harness bash executor seam
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-settings, @deepseek-ai/dsh-shell, @deepseek-ai/dsh-subprocess, @deepseek-ai/dsh-timeout, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: PwshLocalExecutor
- 接口: Config

### shell/pwsh-sandbox — 339 行 / 3 文件

- npm: `@deepseek-ai/dsh-pwsh-sandbox`
- 层: 8
- 自述: Sandbox-consuming implementation of the DeepSeek Harness PowerShell executor seam (confines every command via ctx.sandbox, reports denial/enforcement result facts)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-pwsh-local, @deepseek-ai/dsh-sandbox, @deepseek-ai/dsh-sandbox-policy, @deepseek-ai/dsh-shell
- README: English \| [中文](README.zh.md)
- 类: SandboxPwshExecutor

### shell/shell — 350 行 / 4 文件

- npm: `@deepseek-ai/dsh-shell`
- 层: 6
- 自述: Abstract bash executor seam (ctx.shell) for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-sandbox, @deepseek-ai/dsh-settings, @deepseek-ai/dsh-subprocess
- README: English \| [中文](README.zh.md)
- 类: ShellExecutor
- 接口: ShellExecRequest, ShellExecSpec, ShellProcess, ShellProcessRead, ShellRunResult, ShellSandboxInfo

### shell/shell-env — 247 行 / 2 文件

- npm: `@deepseek-ai/dsh-shell-env`
- 层: 8
- 自述: Tool-independent managed DSH_* shell environment registry
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-home-paths, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-shell, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: ShellEnvRegistry
- 接口: BashEnvContributor, BashEnvVariable, BashEnvVariableInfo, Config

### shell/tool-bash — 554 行 / 4 文件

- npm: `@deepseek-ai/dsh-tool-bash`
- 层: 9
- 自述: Model-facing bash tool with optional generic background-job and sandbox-escalation support
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-jobs, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-sandbox, @deepseek-ai/dsh-sandbox-policy, @deepseek-ai/dsh-shell, @deepseek-ai/dsh-shell-env, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-user-approval, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config

### shell/tool-bash-persistent — 503 行 / 2 文件

- npm: `@deepseek-ai/dsh-tool-bash-persistent`
- 层: 8
- 自述: Model-facing owner-scoped persistent Bash tool backed by the Harness PTY service
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-terminal, @deepseek-ai/dsh-timeout, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config

### shell/tool-pwsh — 620 行 / 4 文件

- npm: `@deepseek-ai/dsh-tool-pwsh`
- 层: 9
- 自述: Model-facing pwsh tool over the bash executor seam
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-jobs, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-sandbox, @deepseek-ai/dsh-sandbox-policy, @deepseek-ai/dsh-shell, @deepseek-ai/dsh-shell-env, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-user-approval, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config, RenderablePwshResult

### shell/tool-pwsh-persistent — 545 行 / 2 文件

- npm: `@deepseek-ai/dsh-tool-pwsh-persistent`
- 层: 8
- 自述: Model-facing owner-scoped persistent PowerShell tool backed by the Harness PTY service
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-terminal, @deepseek-ai/dsh-timeout, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config


## skill

### skill/skill — 898 行 / 2 文件

- npm: `@deepseek-ai/dsh-skill`
- 层: 4
- 自述: Agent skill provider registry for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-scope, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: SkillRegistry
- 接口: Config, SkillCandidate, SkillCatalogSnapshot, SkillDefinition, SkillInvocationPolicy, SkillInvocationSource, SkillLookupOptions, SkillProvider, SkillProviderControl, SkillProviderObservation, SkillSummary, SkillViewOptions

### skill/skill-badge — 90 行 / 2 文件

- npm: `@deepseek-ai/dsh-skill-badge`
- 层: 5
- 自述: Bundled dsh badge skill provider for DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-skill
- README: English \| [中文](README.zh.md)

### skill/skill-filesystem — 1071 行 / 2 文件

- npm: `@deepseek-ai/dsh-skill-filesystem`
- 层: 7
- 自述: Local filesystem skill provider for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-fs, @deepseek-ai/dsh-home-paths, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-skill, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: FileSystemSkillProvider
- 接口: Config

### skill/tool-skill — 461 行 / 2 文件

- npm: `@deepseek-ai/dsh-tool-skill`
- 层: 8
- 自述: Model-facing skill loading tool for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-skill, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config, SkillCatalogSource


## spill

### spill/spill — 161 行 / 3 文件

- npm: `@deepseek-ai/dsh-spill`
- 层: 5
- 自述: Abstract spill storage seam (ctx.spillStore) for the DeepSeek Harness — save oversized tool text and return a retrieval locator
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session
- README: English \| [中文](README.zh.md)
- 类: SpillStore
- 接口: SaveTextSpill, SpillOwner, SpillRef, SpillSource

### spill/spill-local — 215 行 / 3 文件

- npm: `@deepseek-ai/dsh-spill-local`
- 层: 6
- 自述: Local-filesystem implementation of the DeepSeek Harness spill storage seam (private session-scoped files)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-spill, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: LocalSpillStore
- 接口: Config, SaveTextOptions, SavedText

### spill/spill-policy — 288 行 / 3 文件

- npm: `@deepseek-ai/dsh-spill-policy`
- 层: 8
- 自述: Tool-result spill policy for the DeepSeek Harness — replaces oversized plain-text tool results with a retained preview plus a spill-file path (no service API)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-output-retention, @deepseek-ai/dsh-session, @deepseek-ai/dsh-spill, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config, SpillPolicyExec


## storage

### storage/storage — 331 行 / 5 文件

- npm: `@deepseek-ai/dsh-storage`
- 层: 1
- 自述: Storage hub (ctx.storage): named backend registry plus mounted data-form facilities for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: BackendRegistry, Storage, StorageError
- 接口: KvFacet, KvUnit, KvUnitDescriptor, StorageBackend, StorageForms

### storage/storage-domain — 857 行 / 6 文件

- npm: `@deepseek-ai/dsh-storage-domain`
- 层: 2
- 自述: Domain data form (ctx.storage.domain): schema-validated, event-emitting KV domains over storage backends for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-storage, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: DomainError, DomainFacility, DomainImpl
- 接口: Config, Domain, DomainChangedBase, DomainChangedDeleted, DomainChangedPut, DomainErrorOptions, DomainGlobal, DomainGlobalSpec, DomainSpec, DomainTableSpec, InvalidRecordDetail, KvTable

### storage/storage-json — 424 行 / 5 文件

- npm: `@deepseek-ai/dsh-storage-json`
- 层: 2
- 自述: JSON file KV storage backend for the DeepSeek Harness storage hub
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-storage, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: JsonStorageBackend
- 接口: Config, UnitState

### storage/storage-sqlite — 476 行 / 4 文件

- npm: `@deepseek-ai/dsh-storage-sqlite`
- 层: 2
- 自述: SQLite storage backend (kv facet) for the DeepSeek Harness storage hub
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-storage, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: SqliteKvUnit, SqliteStorageBackend
- 接口: Config


## subagent

### subagent/subagent — 4684 行 / 18 文件

- npm: `@deepseek-ai/dsh-subagent`
- 层: 8
- 自述: Abstract subagent seam (ctx.subagents): named-provider registry for delegating to child agents
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-agent-presets, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-jobs, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-sandbox, @deepseek-ai/dsh-sandbox-policy, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-session-projection-cache, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-user-approval
- README: English \| [中文](README.zh.md)
- 类: AssistantOutputFold, SubagentActivationSetupRegistry, SubagentContinuationManager, SubagentDepthError, SubagentError, SubagentRuntime
- 接口: ActivationObserver, ActivationTerminal, ChildComposition, ChildCreateInputs, ContinuableCreateRequest, ContinuableCreateSpec, ContinuableStart, ContinuableStartSpec, ContinuableSubagentDescriptorData, ContinuableSubagentDescriptorInput, CoordinatorMessageSource, DelegatedPolicyOverrides, OneShotSubagentDescriptorData, OneShotSubagentDescriptorInput, ResolvedSubagentStartRequest, RunResultSettlement, SubagentCapabilities, SubagentFollowupOptions, SubagentProvider, SubagentReportMessageSource, SubagentReportOptions, SubagentResult, SubagentRun, SubagentRunEndInfo, SubagentRunInfo, SubagentSettledMessageSource, SubagentStartRequest, SubagentStopReasonMap, SubagentTimingProjection, SubprocessRunHandleParts, TimingState

### subagent/subagent-acp — 587 行 / 3 文件

- npm: `@deepseek-ai/dsh-subagent-acp`
- 层: 9
- 自述: Out-of-process ACP subagent backend: drives a child agent in a spawned subprocess over the Agent Client Protocol
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-subprocess, @deepseek-ai/dsh-timeout, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: AcpRunSpec, Config

### subagent/subagent-claude-code — 936 行 / 4 文件

- npm: `@deepseek-ai/dsh-subagent-claude-code`
- 层: 9
- 自述: One-shot Claude Code subagent provider over the official Agent SDK
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-subprocess, @deepseek-ai/dsh-timeout, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: ManagedClaudeCodeProcess
- 接口: ClaudeCodeRunSpec, Config

### subagent/subagent-codex — 1348 行 / 4 文件

- npm: `@deepseek-ai/dsh-subagent-codex`
- 层: 10
- 自述: One-shot Codex subagent provider over the official app-server protocol
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-sdk-protocol, @deepseek-ai/dsh-session, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-subprocess, @deepseek-ai/dsh-timeout, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: CodexAppServerWire
- 接口: CodexRunSpec, CodexWireFailureFacts, Config

### subagent/subagent-dsh-sdk — 375 行 / 3 文件

- npm: `@deepseek-ai/dsh-subagent-dsh-sdk`
- 层: 11
- 自述: Out-of-process SDK subagent backend: drives a child DeepSeek Harness runtime subprocess over stdio JSON-RPC through the TypeScript SDK client
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-sdk-client, @deepseek-ai/dsh-session, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-subprocess, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config, SdkRunSpec

### subagent/subagent-fork-in-process — 124 行 / 2 文件

- npm: `@deepseek-ai/dsh-subagent-fork-in-process`
- 层: 10
- 自述: In-process fork subagent backend: runs a child agent seeded with a prefix of the parent's log
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-subagent-in-process-driver, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config

### subagent/subagent-in-process-driver — 405 行 / 3 文件

- npm: `@deepseek-ai/dsh-subagent-in-process-driver`
- 层: 9
- 自述: Shared in-process subagent run driver: drives a child agent on ctx.agents (used by the spawn and fork backends)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools
- README: English \| [中文](README.zh.md)
- 接口: InProcessRunOptions, StructuredAttachment

### subagent/subagent-spawn-in-process — 94 行 / 2 文件

- npm: `@deepseek-ai/dsh-subagent-spawn-in-process`
- 层: 10
- 自述: In-process spawn subagent backend: runs a fresh child agent on ctx.agents
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-subagent-in-process-driver, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config

### subagent/tool-subagent — 506 行 / 2 文件

- npm: `@deepseek-ai/dsh-tool-subagent`
- 层: 9
- 自述: Model-facing subagent delegation tool over the ctx.subagents seam
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-jobs, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config

### subagent/tool-subagent-control — 342 行 / 3 文件

- npm: `@deepseek-ai/dsh-tool-subagent-control`
- 层: 9
- 自述: Globally named send_message, interrupt_agent, and list_agents tools over ctx.subagents continuations
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-tools
- README: English \| [中文](README.zh.md)

### subagent/tool-subagent-report — 172 行 / 2 文件

- npm: `@deepseek-ai/dsh-tool-subagent-report`
- 层: 9
- 自述: Child-scoped report tool over ctx.subagents continuations
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config


## subprocess

### subprocess/subprocess — 428 行 / 3 文件

- npm: `@deepseek-ai/dsh-subprocess`
- 层: 1
- 自述: Subprocess seam (ctx.subprocess) for the DeepSeek Harness — managed process groups, bounded spill-backed output, and escalated kills behind one abstract service
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: SubprocessRuntime
- 接口: CollectedOutput, SubprocessCollect, SubprocessCollectedOutputs, SubprocessHandle, SubprocessOutcome, SubprocessOutputRead, SubprocessOutputReader, SubprocessSpawnSpec, SubprocessStdio, SubprocessTerminalForeground, SubprocessTerminalHandle, SubprocessTerminalSpawnSpec

### subprocess/subprocess-local — 1792 行 / 6 文件

- npm: `@deepseek-ai/dsh-subprocess-local`
- 层: 2
- 自述: Local-subprocess implementation of the DeepSeek Harness subprocess seam
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-subprocess, @deepseek-ai/dsh-timeout
- README: English \| [中文](README.zh.md)
- 类: LocalSubprocessRuntime, LocalTerminalHandle, OutputCollector, WindowsProcessInspector
- 接口: LocalSubprocessHandle, ProcessEntry, ProcessIdentity, ProcessInspector, ProcessInspectorInternals, SpawnInternals, WindowsProcessInspectorInternals, WindowsProcessState


## terminal

### terminal/terminal — 683 行 / 3 文件

- npm: `@deepseek-ai/dsh-terminal`
- 层: 6
- 自述: Persistent PTY session seam for the DeepSeek Harness — owner-scoped ids, backend registry, interactive sends, reads, signals, and awaited cleanup
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: TerminalBackendCleanupError, TerminalError, TerminalSessionService
- 接口: TerminalBackend, TerminalBackendSession, TerminalBackendSpawnSpec, TerminalReadRequest, TerminalReadResult, TerminalSendOperation, TerminalSendRead, TerminalSendRequest, TerminalSendResult, TerminalSessionSnapshot, TerminalSignalResult, TerminalSpawnRequest, TerminalSpawnResult

### terminal/terminal-bash — 1116 行 / 5 文件

- npm: `@deepseek-ai/dsh-terminal-bash`
- 层: 8
- 自述: Persistent shell PTY backend over the DeepSeek Harness subprocess terminal primitive
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-pwsh-local, @deepseek-ai/dsh-sandbox, @deepseek-ai/dsh-sandbox-policy, @deepseek-ai/dsh-session, @deepseek-ai/dsh-subprocess, @deepseek-ai/dsh-terminal, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: BashTerminalBackend, LocalPtySession, TerminalSanitizer
- 接口: Config, SanitizedChunk

### terminal/tool-terminal — 606 行 / 3 文件

- npm: `@deepseek-ai/dsh-tool-terminal`
- 层: 8
- 自述: Six model-facing persistent PTY tools with owner isolation and generic background-job integration
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-jobs, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-output-retention, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-terminal, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config


## test-support

### test-support/acp-snapshot — 3218 行 / 6 文件

- npm: `@deepseek-ai/dsh-acp-snapshot`
- 层: 7
- 自述: ACP test kit: shared subprocess launcher, snapshot scenario harness, expected-output normalizers, and suite factory
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-loader-smoke, @deepseek-ai/dsh-session
- README: English \| [中文](README.zh.md)
- 接口: AcpTestLaunchOptions, AgentUnderTest, FixtureReplacement, HarvestedLog, InputScript, LaunchedAcpTestAgent, NamedSnapshotContent, NormalizeContext, NormalizeOptions, PermissionAnswer, RunOptions, RunResult, Scenario, SharedSnapshotClaim, SnapshotSuiteOptions, ToolSchemasSnapshot

### test-support/agent-loop-testkit — 76 行 / 2 文件

- npm: `@deepseek-ai/dsh-agent-loop-testkit`
- 层: 8
- 自述: Shared prerequisite mounting for tests that exercise the concrete agent loop
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools
- README: English \| [中文](README.zh.md)
- 接口: AgentLoopTestDependenciesOptions

### test-support/client-runtime — 1543 行 / 11 文件

- npm: `@deepseek-ai/dsh-client-test-runtime`
- 层: -1
- 自述: jsdom slot test runtime: real Cordis Context + SlotRegistry + UI renderer with test-owned session/workspace doubles for feature specs
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-client-runtime, @deepseek-ai/dsh-client-ui-renderer, @deepseek-ai/dsh-client-ui-slots, @deepseek-ai/dsh-host-apiproxy, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: FixtureSession, SlotTestRuntime, TestRemote, TestRoot, TestSessions, TestWorkspaces
- 接口: FeatureHandle, SessionFixture, SlotView, StubSettingsScope, TestSessionBinding

### test-support/llm-mock-server — 1044 行 / 5 文件

- npm: `@deepseek-ai/dsh-llm-mock-server`
- 层: 1
- 自述: Scriptable OpenAI-compatible HTTP/SSE fault server for LLM recovery tests
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 接口: MockLlmCliConfig, MockLlmRequestRecord, MockLlmServer, MockLlmServerOptions

### test-support/llm-replay — 889 行 / 2 文件

- npm: `@deepseek-ai/dsh-llm-replay`
- 层: 8
- 自述: Replay LLM plugin: short-circuits llm/stream with model chunks reconstructed from a recorded session JSONL (keyless snapshot tests)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-compaction, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session
- README: English \| [中文](README.zh.md)
- 接口: Config, ReplayConfig, ReplayHandle, ReplayModelConfig, ReplayOverridePatch, ReplayProviderConfig, SessionScript

### test-support/loader-smoke — 342 行 / 3 文件

- npm: `@deepseek-ai/dsh-loader-smoke`
- 层: 6
- 自述: Shared subprocess and direct-agent harness for keyless real-Loader example smoke tests
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session
- README: English \| [中文](README.zh.md)
- 接口: ExampleLaunch, ExampleLaunchOptions, FixtureTurnOptions, FixtureTurnResult, LoaderSmokeOptions, LoaderSmokeResult


## todo

### todo/tool-todo — 329 行 / 4 文件

- npm: `@deepseek-ai/dsh-tool-todo`
- 层: 8
- 自述: Model-facing todo_write tool over the DeepSeek Harness event-sourced session log
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config


## typert

### typert/generator — 6251 行 / 9 文件

- npm: `@deepseek-ai/dsh-typert-generator`
- 层: 1
- 自述: TypeScript project analyzer and model-driven Typert artifact generator
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: CordisCatalogProjector, FaceModelEmitter, TypeGraphRenderError, TypeGraphRenderer, TypertAnalysisError, TypertEmitError, WorkspaceAnalyzer, WorkspaceCaches, WorkspaceTypertGenerator
- 接口: AccessorMemberModel, CordisCatalogModel, CordisCatalogPolicy, CrossFaceLink, DiscoveredTypertPackage, DocumentationModel, EnumMemberModel, EventEntry, EventModel, ExportModel, FaceModel, InheritedEntry, InvocationModel, InvocationParameterModel, JsDocTagModel, MemberBase, MethodMemberModel, ModelEmitResult, ObjectModel, PackageModel, PackageRegistration, ParameterModel, ParsedConfig, PropertyMemberModel, RemoteBoundaryModel, RemoteModelEmitResult, RemoteTypeImportModel, SchemaModel, ServiceEntry, ServiceMethodEntry, ServiceModel, SignatureMemberModel, SignatureModel, SourceDeclarationModel, SourceLocation, TemplateSpanModel, TupleElementModel, TypeDeclarationModel, TypeDeclarationPartModel, TypeGraph, TypeParameterModel, TypertPluginOptions, WorkspaceAnalyzerOptions, WorkspaceEmitResult, WorkspaceModel

### typert/loader — 472 行 / 2 文件

- npm: `@deepseek-ai/dsh-typert-loader`
- 层: 3
- 自述: Loader integration for generated Typert package contributions
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-loader, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-typert-registry, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config

### typert/protocol — 801 行 / 3 文件

- npm: `@deepseek-ai/dsh-typert-protocol`
- 层: 1
- 自述: Compiler-independent Remote metadata and Typert provider protocols
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: TypertLookupFailure, TypertRemoteService
- 接口: InvocationDescriptor, InvocationParameterDescriptor, InvocationSourceLocation, RemoteFailure, RemoteMethodMarker, TypertClientContextBinder, TypertClientRemote, TypertContext, TypertContextMap, TypertContextRegistry, TypertGatewayBinding, TypertGatewayBindingOptions, TypertHostContextProvider, TypertLocalRegistry, TypertLookup, TypertLookupDefinition, TypertLookupMap, TypertLookupProvider, TypertLookupRegistry, TypertRegistryChange, TypertRegistryContract, TypertRemoteContribution, TypertRemoteEventSelection, TypertRemoteMap, TypertRemoteNamespaceMap, TypertRemoteRegistry, TypertRemoteScopeMap, TypertSchema

### typert/registry — 912 行 / 6 文件

- npm: `@deepseek-ai/dsh-typert-registry`
- 层: 2
- 自述: Runtime registry for generated package reflection and Zod schemas
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-typert-protocol
- README: English \| [中文](README.zh.md)
- 类: TypertRegistry
- 接口: TypertContribution, TypertDocTag, TypertDocumentation, TypertEventModel, TypertMemberModel, TypertObjectModel, TypertPackageFilter, TypertPackageModel, TypertPackageRecord, TypertSchema, TypertSchemaFilter, TypertSchemaRecord, TypertServiceModel, TypertTypeModel


## util

### util/atomic-write — 184 行 / 2 文件

- npm: `@deepseek-ai/dsh-atomic-write`
- 层: 1
- 自述: Zero-dependency atomic file replacement: exclusive-create random-suffix temp + rename carrying the caller-stated permissions (writeFileAtomic)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 接口: FileLockOptions, WriteFileAtomicOptions

### util/brand — 57 行 / 2 文件

- npm: `@deepseek-ai/dsh-brand`
- 层: 1
- 自述: Type-only Branded<B> nominal-typing primitive for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)

### util/home-paths — 142 行 / 2 文件

- npm: `@deepseek-ai/dsh-home-paths`
- 层: 1
- 自述: Shared filesystem path helpers for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)

### util/launch-environment — 154 行 / 2 文件

- npm: `@deepseek-ai/dsh-launch-environment`
- 层: 1
- 自述: Immutable DeepSeek Harness launch environment that records which layer supplied each value
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 接口: LaunchEnvironmentEntry, LaunchEnvironmentLayerInput, LaunchEnvironmentSnapshot

### util/native-command — 75 行 / 2 文件

- npm: `@deepseek-ai/dsh-native-command`
- 层: 1
- 自述: Zero-dependency no-shell execFile runner for host-native OS integrations: utf8 stdio capture, abort propagation, Windows hide
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)

### util/output-retention — 473 行 / 2 文件

- npm: `@deepseek-ai/dsh-output-retention`
- 层: 1
- 自述: Zero-dependency bounded-retention primitive: ItemRetainer/TextRetainer + neutral notice helpers (what did we keep, what did we omit)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: ItemRetainer, TextRetainer
- 接口: PushDecision, RetainedItems, RetainedText, RetentionNotice

### util/timeout — 220 行 / 2 文件

- npm: `@deepseek-ai/dsh-timeout`
- 层: 1
- 自述: Zero-dependency timeout/deadline primitive: clampTimeout, deadline, timeoutOf, TimeoutReason (timing + classification only, no termination)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: English \| [中文](README.zh.md)
- 类: TimeoutReason
- 接口: Deadline, IdleWatchdog


## web

### web/tool-web — 996 行 / 4 文件

- npm: `@deepseek-ai/dsh-tool-web`
- 层: 8
- 自述: Model-facing web tools (web_search, web_fetch) over the DeepSeek Harness web capability seam (ctx.web)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-web, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config, WebFetchMeta, WebSearchMeta

### web/web — 362 行 / 3 文件

- npm: `@deepseek-ai/dsh-web`
- 层: 4
- 自述: Abstract web access capability seam (ctx.web) for the DeepSeek Harness — search/fetch provider registry, registration-order-independent selection, request/result vocabulary, and the WebError taxonomy
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: WebError, WebRuntime
- 接口: WebFetchProvider, WebFetchRequest, WebFetchResult, WebRuntimeConfig, WebSearchProvider, WebSearchRequest, WebSearchResult, WebSearchSource

### web/web-fetch-http — 476 行 / 4 文件

- npm: `@deepseek-ai/dsh-web-fetch-http`
- 层: 5
- 自述: Anonymous public HTTP(S) fetch provider for the DeepSeek Harness web capability seam (ctx.web)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-timeout, @deepseek-ai/dsh-web, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: HttpFetchProvider
- 接口: Config, HttpFetchLimits

### web/web-search-deepseek — 565 行 / 4 文件

- npm: `@deepseek-ai/dsh-web-search-deepseek`
- 层: 6
- 自述: DeepSeek-backed search provider (native web_search via the Anthropic-compatible API) for the DeepSeek Harness web capability seam (ctx.web)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-credentials, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-launch-environment, @deepseek-ai/dsh-session, @deepseek-ai/dsh-settings, @deepseek-ai/dsh-web, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: DeepSeekSearchProvider
- 接口: AnthropicError, AnthropicResponse, CitationLocation, Config, DeepSeekSearchLlmRequest, DeepSeekSearchProviderOptions, TextBlock, WebSearchResultItem, WebSearchToolResultBlock

### web/web-search-exa — 303 行 / 4 文件

- npm: `@deepseek-ai/dsh-web-search-exa`
- 层: 5
- 自述: Exa-backed search provider for the DeepSeek Harness web capability seam (ctx.web)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-launch-environment, @deepseek-ai/dsh-web, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: ExaSearchProvider
- 接口: Config, ExaError, ExaResult, ExaSearchProviderOptions, ExaSearchRequest, ExaSearchResponse

### web/web-search-perplexity — 296 行 / 4 文件

- npm: `@deepseek-ai/dsh-web-search-perplexity`
- 层: 5
- 自述: Perplexity-backed search provider for the DeepSeek Harness web capability seam (ctx.web)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-launch-environment, @deepseek-ai/dsh-web, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: PerplexitySearchProvider
- 接口: Config, PerplexityError, PerplexityRequest, PerplexityResponse, PerplexitySearchProviderOptions, PerplexitySearchResult


## workflow

### workflow/tool-ralph — 509 行 / 2 文件

- npm: `@deepseek-ai/dsh-tool-ralph`
- 层: 9
- 自述: Model-facing fresh-agent Ralph loop over the workflow and subagent seams
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-workflow, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config

### workflow/tool-workflow — 566 行 / 3 文件

- npm: `@deepseek-ai/dsh-tool-workflow`
- 层: 8
- 自述: Model-facing workflow tool: run a JavaScript orchestration script over ctx.workflowEngine
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-workflow, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 接口: Config, ToolWorkflowAgentEndData, ToolWorkflowAgentStartData, ToolWorkflowRunEndData, ToolWorkflowRunStartData

### workflow/workflow — 519 行 / 4 文件

- npm: `@deepseek-ai/dsh-workflow`
- 层: 6
- 自述: Workflow capability seam: ctx.workflowEngine service, run vocabulary, and workflow/* events
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session
- README: English \| [中文](README.zh.md)
- 类: WorkflowEngine, WorkflowError
- 接口: WorkflowAgentEndInfo, WorkflowAgentInfo, WorkflowMeta, WorkflowPhase, WorkflowResult, WorkflowResultInfo, WorkflowRun, WorkflowRunInfo, WorkflowStartRequest

### workflow/workflow-worker-thread — 2016 行 / 11 文件

- npm: `@deepseek-ai/dsh-workflow-worker-thread`
- 层: 9
- 自述: worker-thread workflow engine: executes model-written orchestration scripts off the host event loop, bridging agent() calls back to ctx.subagents
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-workflow, @deepseek-ai/schemastery
- README: English \| [中文](README.zh.md)
- 类: MaterializeError, WorkerRun, WorkflowExecution
- 接口: ChildHandle, ChildPort, ChildResult, ChildStartRequest, Config, ExecutionObserver, HostToWorkerPayloads, WorkerInit, WorkerLimits, WorkerToHostPayloads


## workspace

### workspace/workspace — 1142 行 / 6 文件

- npm: `@deepseek-ai/dsh-workspace`
- 层: 6
- 自述: Workspace entity registry (ctx.workspaceRegistry): durable workspace records with validated session attachment over the domain data form for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-storage, @deepseek-ai/dsh-storage-domain
- README: English \| [中文](README.zh.md)
- 类: WorkspaceEntity, WorkspaceMoveInvalidError, WorkspaceOrderInvalidError, WorkspaceRegistry, WorkspaceUnknownSessionError
- 接口: Workspace, WorkspaceEntityHost

