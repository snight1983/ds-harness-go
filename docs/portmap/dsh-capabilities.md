# DSH 能力清单（机器抽取，勿手改）

每一行都来自包作者自己写下的内容，不经转述：
`description` 取自各包 `package.json`，`类/接口` 取自源码里的 `export class` / `export interface`。
由 `internal/devtools/capmap` 生成，重跑覆盖。

## 移植顺序（依赖拓扑分层）

第 0 层不依赖任何 DSH 包，是唯一能在不给别人的类型编存根的前提下动笔的地方。
边只取 `dependencies` 与 `peerDependencies`，不取 `devDependencies`（那里混着构建期工具）。

**第 0 层** — 47 个包 / 84113 行：client/hmr、client/locale、client/modules、client/store、client/ui-agent-preset、client/ui-approval、client/ui-attachment、client/ui-brand-official、client/ui-chat、client/ui-commands、client/ui-conversation、client/ui-deliverables、client/ui-directory-picker-browse、client/ui-directory-picker-native、client/ui-goal、client/ui-input-trigger、client/ui-jobs、client/ui-layout、client/ui-message-feedback、client/ui-model-selection、client/ui-permission-presets、client/ui-plan、client/ui-primitives、client/ui-reference、client/ui-renderer、client/ui-schedule、client/ui-session、client/ui-settings、client/ui-settings-general、client/ui-settings-models、client/ui-settings-plugin-inventory、client/ui-settings-plugins、client/ui-sidebar、client/ui-skill、client/ui-slots、client/ui-subagent、client/ui-theme、client/ui-tool、client/ui-trajectory、client/ui-user-questions、client/ui-workflow-run、client/ui-workspace、client/web、extensions/cordis-client-runner、extensions/ui-cordis、runtime-diagnostics/invariants、typert/registry

**第 1 层** — 27 个包 / 15659 行：boot/cmdline、code-runtime/code-runtime、code-runtime/code-runtime-python、core/scope、e2b/e2b、host/directory-picker、host/webserver、llm/deepseek-llm-api-extensions、storage/storage、subprocess/subprocess、subprocess/win32-process、test-support/llm-mock-server、typert/generator、typert/loader、typert/protocol、util/atomic-write、util/brand、util/crypto、util/deque、util/home-paths、util/launch-environment、util/native-command、util/output-retention、util/time、util/timeout、util/values、util/workspace-path

**第 2 层** — 15 个包 / 34019 行：api/gateway、attachment/attachment、credentials/credentials、e2b/subprocess-e2b、experimental/inspector、host/directory-picker-browse、host/directory-picker-native、identity/anonymous-user-id、llm/llm、sandbox/sandbox-windows-acl、session-query/session-log-export、storage/storage-domain、storage/storage-json、storage/storage-sqlite、subprocess/subprocess-local

**第 3 层** — 10 个包 / 14020 行：attachment/attachment-local、client/connection、core/session、core/system-prompt、credentials/authorization、credentials/credentials-local、host/directory-picker-auto、lsp/lsp、skill/skill、web/web

**第 4 层** — 16 个包 / 23697 行：api/remotes、boot/app-boot、code-runtime/code-runtime-worker-thread、experimental/webworker-runtime、host/frontend-static、preset/persona、sandbox/sandbox、session/session-log-deepseek、session/session-persistence、session/session-projection、settings/settings、skill/skill-badge、spill/spill、web/web-fetch-http、web/web-search-exa、web/web-search-perplexity

**第 5 层** — 13 个包 / 10346 行：core/agent、experimental/webworker-packer、feedback/message-feedback、fs/fs、sandbox/sandbox-local、session/session-persistence-jsonl、session/session-projection-cache、session/session-stats、session/session-turn-outline、settings/settings-file、shell/shell、spill/spill-local、workspace/workspace

**第 6 层** — 28 个包 / 23877 行：api/workspace-controller、context/file-reference、context/time-context、context/tmux-context、core/agent-default-model、e2b/fs-e2b、fs/fs-local、fs/fs-observation-policy、goal/goal、hooks/hook-protocol、interaction/commands、interaction/user-approval、interaction/user-questions、jobs/jobs、llm/llm-deepseek、llm/llm-pi-ai、llm/llm-retry、lsp/lsp-stdio、sandbox/sandbox-policy、session/session-telemetry、session/session-title、shell/bash-local、shell/pwsh-local、skill/skill-filesystem、terminal/terminal、test-support/loader-smoke、web/web-search-deepseek、workflow/workflow

**第 7 层** — 14 个包 / 15503 行：bundle/headless、compaction/compaction、core/tools、feedback/command-feedback、fs/fs-sandbox、goal/command-goal、goal/goal-round-driver、interaction/permission-presets、jobs/jobs-local、session/session-title-llm、shell/bash-sandbox、shell/pwsh-sandbox、terminal/terminal-bash、test-support/session-snapshot

**第 8 层** — 36 个包 / 28074 行：compaction/command-compact、context/agent-instructions、context/file-reference-local、core/agent-loop、core/agent-tool-presentation、extensions/cordis-host-runner、fs/tool-fs、fs/tool-fs-search、fs/tool-str-replace-editor、goal/tool-goal、guard/repeat-tool-reminder、guard/timeout-policy、hooks/hooks-codex、interaction/tool-ask-user、jobs/tool-jobs、llm/token-meter、lsp/tool-lsp、mcp/mcp-client、plan/plan-mode、preset/agent-presets、schedule/schedule、session/session-checkpoint-policy、session/session-telemetry-otel、session/session-title-all-prompts-llm、session/session-title-first-prompt-llm、shell/shell-env、shell/tool-bash-persistent、shell/tool-pwsh-persistent、skill/tool-skill、spill/spill-policy、terminal/tool-terminal、test-support/agent-loop-testkit、test-support/llm-replay、todo/tool-todo、web/tool-web、workflow/tool-workflow

**第 9 层** — 10 个包 / 14337 行：acp/acp、api/settings-controller、compaction/compaction-tool-result-pruner、extensions/tool-cordis、host/plugin-inventory、llm/plugin-package-inventory-deepseek、session-query/session-query、shell/tool-bash、shell/tool-pwsh、webhook/webhook

**第 10 层** — 7 个包 / 11331 行：bundle/acp-app、compaction/compaction-basic、context/session-reference、session-query/session-query-sqlite、session-query/tool-session-query、subagent/subagent、webhook/webhook-github

**第 11 层** — 12 个包 / 17231 行：api/session-controller、experimental/agent-team、hooks/hooks-claude-code、sdk/protocol、subagent/subagent-acp、subagent/subagent-claude-code、subagent/subagent-in-process-driver、subagent/tool-subagent、subagent/tool-subagent-control、subagent/tool-subagent-report、workflow/tool-ralph、workflow/workflow-worker-thread

**第 12 层** — 9 个包 / 6348 行：bundle/web-app、experimental/client-ui-agent-team、experimental/tool-agent-team、sdk/client、sdk/server、subagent/subagent-codex、subagent/subagent-fork-in-process、subagent/subagent-spawn-in-process、test-support/client-runtime

**第 13 层** — 5 个包 / 777 行：bundle/base、bundle/sdk-app、experimental/agent-team-profile、experimental/agent-team-web-profile、subagent/subagent-dsh-sdk

**第 14 层** — 1 个包 / 35 行：bundle/sdk-minimal


## acp

### acp/acp — 1854 行 / 8 文件

- npm: `@deepseek-ai/dsh-acp`
- 层: 9
- 自述: Automation-only Agent Client Protocol server for driving DeepSeek Harness agents over JSON-RPC stdio
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-attachment, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-mcp-client, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-token-meter, @deepseek-ai/dsh-user-approval, @deepseek-ai/schemastery
- README: --- description: "Automation-only Agent Client Protocol server for programmatic clients and maintainers driving DeepSeek Harness agents over JSON-RPC stdio." kind: "package-reference" ---
- 类: AcpContentError, AcpMcpConfigError, AcpModelConfigError, AcpModelControl, AcpSession
- 接口: AcpConfig, CreateAcpSessionOptions, ResumeAcpSessionOptions


## api

### api/gateway — 4381 行 / 13 文件

- npm: `@deepseek-ai/dsh-api-gateway`
- 层: 2
- 自述: Typert Remote Host dispatcher and Client API endpoint
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-deque, @deepseek-ai/dsh-timeout, @deepseek-ai/dsh-typert-protocol, @deepseek-ai/schemastery
- README: --- description: "Typed Client-to-Host calls and streams: dispatch, validation, cancellation, reconnection, and forwarded Host events." kind: "package-reference" ---
- 类: ClientRemoteEvents, RemoteJournalStream, RemoteSnapshotStream, RemoteStream, RemoteStreamCarrierError, RemoteStreamMuxClient, RemoteStreamMuxServer, TypertGatewayError, TypertGatewayService
- 接口: ClientRemote, Config, InvokeRemoteRequest, ProjectedRemoteEventRequest, RemoteEventCancellationFrame, RemoteEventEmitFrame, RemoteEventHostInfo, RemoteEventInvocationFrame, RemoteEventReadyFrame, RemoteEventRejection, RemoteEventResult, RemoteHostFacts, RemoteJournalStreamOptions, RemoteSnapshotStreamOptions, RemoteStreamFactory, RemoteStreamFailure, RemoteStreamItem, RemoteStreamOptions, TypertGateway, TypertGatewayFaultDetails, TypertGatewayWireStream, TypertRemoteEventContext, TypertRemoteEventFrame, TypertRemoteEventInvocation

### api/remotes — 412 行 / 6 文件

- npm: `@deepseek-ai/dsh-api-remotes`
- 层: 4
- 自述: Remote BFF assembly for application-selected Host capabilities
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-deque, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-session, @deepseek-ai/dsh-util-values
- README: --- description: "Application Remote assembly: selects typed Host capabilities and forwarded events for Client consumers." kind: "package-reference" ---

### api/session-controller — 7299 行 / 32 文件

- npm: `@deepseek-ai/dsh-api-session-controller`
- 层: 11
- 自述: Session Remote commands, cold reads, and live control transport
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-agent-default-model, @deepseek-ai/dsh-agent-presets, @deepseek-ai/dsh-api-gateway, @deepseek-ai/dsh-attachment, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-client-connection, @deepseek-ai/dsh-deque, @deepseek-ai/dsh-file-reference, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-jobs, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-native-command, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-session-projection-cache, @deepseek-ai/dsh-session-query, @deepseek-ai/dsh-session-title, @deepseek-ai/dsh-skill, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-typert-protocol, @deepseek-ai/dsh-typert-registry, @deepseek-ai/dsh-util-time, @deepseek-ai/dsh-util-workspace-path, @deepseek-ai/dsh-workspace, @deepseek-ai/schemastery
- README: --- description: "Host and Client session control: create, resume, prompt, follow history, and project live session state." kind: "package-reference" ---
- 类: ApiSessionAgentController, ApiSessionCwdConflict, ApiSessionList, ApiSessionNotFound, ApiSessionPresetConflict, ApiSessionSubagentOwnership, ClientSessions, MutableSessionEventSource, Notifier, ProjectionValueStore, Session, SessionCommandController, SessionControlController, SessionController, SessionCreateError, SessionEventStream, SessionFileReferences, SessionForkError, SessionHistoryController, SessionManager, SessionQueueMirror, SessionSkillCatalog
- 接口: AgentScopeHandle, BeginSubmissionInput, Config, ISession, ISessions, ModelCatalog, ModelCatalogFailure, ModelCatalogModel, ModelProviderGroup, ModelReasoning, ModelReasoningEffort, ModelSelection, ModelSelectionProjection, ModelSelectionProjectionState, PendingSubmission, PendingSubmissionImage, ProjectionsBaseline, ProjectionsFace, PromptError, QueuedMessage, SessionAttachmentRequest, SessionAttachmentValue, SessionBinding, SessionCancelRequest, SessionCancelValue, SessionChunkRun, SessionCommandsRemote, SessionControlBaseline, SessionControlStreamOptions, SessionControllerInternals, SessionCreateRequest, SessionCreateValue, SessionEventEntry, SessionEventStreamOptions, SessionEventWindow, SessionFollowRequest, SessionForkRequest, SessionForkValue, SessionJob, SessionListEntry, SessionListMetadata, SessionListRequest, SessionListSnapshot, SessionListState, SessionListValue, SessionOpenWorkspacePathRequest, SessionOpenWorkspacePathValue, SessionOptions, SessionPage, SessionPageRequest, SessionProjectionBaseline, SessionProjectionHints, SessionProjectionUpdate, SessionPromptRequest, SessionPromptValue, SessionQueuedItem, SessionRemotes, SessionRenameRequest, SessionRenameValue, SessionSearchItem, SessionSearchRequest, SessionSearchResultItem, SessionSearchValue, SessionSelectModelRequest, SessionSelectModelValue, SessionSnapshot, SessionSubagentsRemote, SessionSummary, SessionSummary, SessionUpdateQueueRequest, SessionUpdateQueueValue, SessionWireEvent, SkillEntry, SkillListRequest, SkillListValue, SubmissionHandle, TitledSessionSummary

### api/settings-controller — 560 行 / 4 文件

- npm: `@deepseek-ai/dsh-api-settings-controller`
- 层: 9
- 自述: Remote owner for the configuration surfaces over the settings-domain seams
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent-presets, @deepseek-ai/dsh-credentials, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-native-command, @deepseek-ai/dsh-session, @deepseek-ai/dsh-settings, @deepseek-ai/dsh-typert-protocol, @deepseek-ai/schemastery
- README: --- description: "Host Remote owner for settings and credential configuration surfaces, including redacted reads, writes, credential references, and native document opening." kind: "package-reference" ---
- 类: CredentialsController, SettingsController
- 接口: Config, SettingsControllerInternals, SettingsDocumentOpenValue

### api/workspace-controller — 1433 行 / 10 文件

- npm: `@deepseek-ai/dsh-api-workspace-controller`
- 层: 6
- 自述: Workspace Remote commands and reconnect-safe state transport
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-api-gateway, @deepseek-ai/dsh-client-connection, @deepseek-ai/dsh-deque, @deepseek-ai/dsh-host-directory-picker, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-storage-domain, @deepseek-ai/dsh-typert-protocol, @deepseek-ai/dsh-workspace
- README: --- description: "Host and Client workspace control: mutate workspace navigation and follow its complete projection." kind: "package-reference" ---
- 类: ClientWorkspaceModel, DirectoryPickerController, WorkspaceCommands, WorkspaceController, WorkspaceController, WorkspaceCreateError, WorkspaceFeed
- 接口: IWorkspaces, WorkspaceArchiveSessionRequest, WorkspaceArchiveValue, WorkspaceBaseline, WorkspaceCreateRequest, WorkspaceCreateValue, WorkspaceDeleteRequest, WorkspaceDeleteValue, WorkspaceFollowSink, WorkspaceInsertBeforeRequest, WorkspaceInsertSessionBeforeRequest, WorkspaceOrderValue, WorkspaceRenameRequest, WorkspaceSnapshot, WorkspaceSource, WorkspaceStateStreamOptions, WorkspaceValue, WorkspaceView


## attachment

### attachment/attachment — 484 行 / 7 文件

- npm: `@deepseek-ai/dsh-attachment`
- 层: 2
- 自述: Durable immutable attachment storage seam for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants
- README: --- description: "Durable image attachments for users and maintainers attaching, reusing, or debugging images in prompts and commands." kind: "package-reference" ---
- 类: AttachmentError, AttachmentStore
- 接口: EncodedImageAttachment, ImageAttachmentLimits, ImageAttachmentRef, ImageRequestPolicy, RequestImageAttachment, SaveImageAttachment, StoredImageAttachment

### attachment/attachment-local — 1189 行 / 8 文件

- npm: `@deepseek-ai/dsh-attachment-local`
- 层: 3
- 自述: Private content-addressed DSH_HOME attachment storage
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-attachment, @deepseek-ai/dsh-home-paths, @deepseek-ai/dsh-invariants, @deepseek-ai/schemastery
- README: --- description: "Local storage for your attached images below DSH_HOME, for users and maintainers choosing or debugging where image attachments are kept." kind: "package-reference" ---
- 类: CompressionLimiter, LocalAttachmentStore
- 接口: Config, DecodedImageLimits, DetectedImage, EncodedCandidate, EncodedImage, ExhaustedEncoding, NormalizationPolicy, NormalizedImage, PreparedImageFile


## boot

### boot/app-boot — 1756 行 / 4 文件

- npm: `@deepseek-ai/dsh-app-boot`
- 层: 4
- 自述: Shared boot glue for the app bins: .env loading, fail-loud Loader guards, snapshot-aware config resolution, and the Loader boot sequence
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-group, @deepseek-ai/cordis-plugin-hmr, @deepseek-ai/cordis-plugin-include, @deepseek-ai/cordis-plugin-loader, @deepseek-ai/dsh-atomic-write, @deepseek-ai/dsh-home-paths, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-launch-environment, @deepseek-ai/dsh-system-prompt
- README: --- description: "Shared Loader boot support for dsh profiles and the temporary Python SDK runtime: environment layers, patches, diagnostics, and configuration preview." kind: "package-library" ---
- 接口: ConfigDumpLayer, DshBundleManifest, DshManifestSection, DshProfileManifest, FailLoudProcess, Profile, ProfileLayer, ProfileManifest, ProfileModuleFallbackOptions, ProfileTemplate, UserPatchWatchOptions

### boot/cmdline — 269 行 / 2 文件

- npm: `@deepseek-ai/dsh-cmdline`
- 层: 1
- 自述: Immutable command-line handoff from a dsh launcher to any app plugin that injects cmdlineArgs
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-loader, @deepseek-ai/dsh-invariants
- README: --- description: "App-owned command lines for dsh app bins: your app parses its own flags, --help, and exit behavior from the launcher's remaining arguments." kind: "package-library" ---
- 接口: AppExit, AppReady, AppStdin, CmdlineArgs, CmdlineHost


## bundle

### bundle/acp-app — 76 行 / 2 文件

- npm: `@deepseek-ai/dsh-acp-app`
- 层: 10
- 自述: The dsh ACP profile bundle: automation-only JSON-RPC stdio and process lifecycle over dsh-base
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-acp, @deepseek-ai/dsh-cmdline, @deepseek-ai/dsh-invariants
- README: --- description: "Automation-only ACP stdio application profile for users and maintainers launching persistent harness agents." kind: "package-bundle" ---

### bundle/base — 37 行 / 2 文件

- npm: `@deepseek-ai/dsh-base`
- 层: 13
- 自述: The shared dsh core as a profile bundle: the first patch layer of base-backed profiles, inserting core rows over the empty profile root
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-hmr, @deepseek-ai/cordis-plugin-timer, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-agent-default-model, @deepseek-ai/dsh-agent-instructions, @deepseek-ai/dsh-agent-loop, @deepseek-ai/dsh-api-gateway, @deepseek-ai/dsh-attachment-local, @deepseek-ai/dsh-bash-sandbox, @deepseek-ai/dsh-command-compact, @deepseek-ai/dsh-command-feedback, @deepseek-ai/dsh-command-goal, @deepseek-ai/dsh-commands, @deepseek-ai/dsh-compaction-basic, @deepseek-ai/dsh-compaction-tool-result-pruner, @deepseek-ai/dsh-credentials-local, @deepseek-ai/dsh-deepseek-llm-api-extensions, @deepseek-ai/dsh-fs-local, @deepseek-ai/dsh-fs-observation-policy, @deepseek-ai/dsh-fs-sandbox, @deepseek-ai/dsh-goal, @deepseek-ai/dsh-goal-round-driver, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-jobs-local, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-llm-deepseek, @deepseek-ai/dsh-llm-pi-ai, @deepseek-ai/dsh-llm-retry, @deepseek-ai/dsh-permission-presets, @deepseek-ai/dsh-plan-mode, @deepseek-ai/dsh-plugin-package-inventory-deepseek, @deepseek-ai/dsh-pwsh-sandbox, @deepseek-ai/dsh-repeat-tool-reminder, @deepseek-ai/dsh-sandbox-local, @deepseek-ai/dsh-sandbox-policy, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-checkpoint-policy, @deepseek-ai/dsh-session-log-deepseek, @deepseek-ai/dsh-session-persistence-jsonl, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-session-projection-cache, @deepseek-ai/dsh-session-query-sqlite, @deepseek-ai/dsh-session-telemetry-otel, @deepseek-ai/dsh-session-title, @deepseek-ai/dsh-session-title-first-prompt-llm, @deepseek-ai/dsh-settings-file, @deepseek-ai/dsh-shell-env, @deepseek-ai/dsh-skill, @deepseek-ai/dsh-skill-badge, @deepseek-ai/dsh-skill-filesystem, @deepseek-ai/dsh-spill-local, @deepseek-ai/dsh-spill-policy, @deepseek-ai/dsh-storage, @deepseek-ai/dsh-storage-domain, @deepseek-ai/dsh-storage-json, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-subagent-fork-in-process, @deepseek-ai/dsh-subagent-spawn-in-process, @deepseek-ai/dsh-subprocess-local, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-token-meter, @deepseek-ai/dsh-tool-bash, @deepseek-ai/dsh-tool-call-timeout-policy, @deepseek-ai/dsh-tool-fs, @deepseek-ai/dsh-tool-fs-search, @deepseek-ai/dsh-tool-goal, @deepseek-ai/dsh-tool-jobs, @deepseek-ai/dsh-tool-pwsh, @deepseek-ai/dsh-tool-ralph, @deepseek-ai/dsh-tool-skill, @deepseek-ai/dsh-tool-str-replace-editor, @deepseek-ai/dsh-tool-subagent, @deepseek-ai/dsh-tool-subagent-control, @deepseek-ai/dsh-tool-subagent-report, @deepseek-ai/dsh-tool-todo, @deepseek-ai/dsh-tool-web, @deepseek-ai/dsh-tool-workflow, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-typert-loader, @deepseek-ai/dsh-typert-registry, @deepseek-ai/dsh-user-approval, @deepseek-ai/dsh-user-questions, @deepseek-ai/dsh-web, @deepseek-ai/dsh-web-fetch-http, @deepseek-ai/dsh-web-search-deepseek, @deepseek-ai/dsh-workflow-worker-thread
- README: --- description: "The shared dsh core: model access, tools, durable sessions, and safety defaults for every dsh --profile surface, for users composing or customizing a profile." kind: "package-bundle" ---

### bundle/headless — 309 行 / 3 文件

- npm: `@deepseek-ai/dsh-headless`
- 层: 7
- 自述: The dsh one-shot bundle: a direct core Agent/Session runner over dsh-base with no Host, HTTP, or browser layer
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-loader, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-agent-default-model, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-cmdline, @deepseek-ai/dsh-code-runtime-worker-thread, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-util-values, @deepseek-ai/schemastery
- README: --- description: "One-shot task mode for dsh: run a single task from the command line and get the final answer printed, for users scripting or automating dsh." kind: "package-bundle" ---
- 接口: Config, HeadlessStartupValues

### bundle/sdk-app — 90 行 / 2 文件

- npm: `@deepseek-ai/dsh-sdk-app`
- 层: 13
- 自述: The dsh SDK profile bundle: stdio JSON-RPC serving and process lifecycle over dsh-base
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-cmdline, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-sdk-jsonrpc-server, @deepseek-ai/schemastery
- README: --- description: "SDK stdio application profile for users and maintainers launching a JSON-RPC harness runtime." kind: "package-bundle" ---
- 接口: Config

### bundle/sdk-minimal — 35 行 / 2 文件

- npm: `@deepseek-ai/dsh-sdk-minimal`
- 层: 14
- 自述: The standalone minimal SDK profile bundle: JSON-RPC, one DeepSeek adapter, persistent shell, editor, and JSONL sessions
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-timer, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-agent-loop, @deepseek-ai/dsh-deepseek-llm-api-extensions, @deepseek-ai/dsh-fs-local, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-jobs-local, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-llm-deepseek, @deepseek-ai/dsh-llm-retry, @deepseek-ai/dsh-plugin-package-inventory-deepseek, @deepseek-ai/dsh-sandbox-local, @deepseek-ai/dsh-sandbox-policy, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-sdk-app, @deepseek-ai/dsh-sdk-jsonrpc-server, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-log-deepseek, @deepseek-ai/dsh-session-persistence-jsonl, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-session-title, @deepseek-ai/dsh-subprocess-local, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-terminal, @deepseek-ai/dsh-terminal-bash, @deepseek-ai/dsh-tool-bash-persistent, @deepseek-ai/dsh-tool-pwsh-persistent, @deepseek-ai/dsh-tool-str-replace-editor, @deepseek-ai/dsh-tools
- README: --- description: "Standalone two-tool SDK profile for users who need a minimal cross-platform coding agent without the shared base bundle." kind: "package-bundle" ---

### bundle/web-app — 426 行 / 3 文件

- npm: `@deepseek-ai/dsh-web-app`
- 层: 12
- 自述: The dsh browser-surface bundle: the web patch layer over dsh-base plus the runtime glue plugin (frontend dist serving, web-surface prompt, bash runtime variables, URL line)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-loader, @deepseek-ai/dsh-agent-presets, @deepseek-ai/dsh-api-remotes, @deepseek-ai/dsh-api-session-controller, @deepseek-ai/dsh-api-settings-controller, @deepseek-ai/dsh-api-workspace-controller, @deepseek-ai/dsh-app-boot, @deepseek-ai/dsh-client-connection, @deepseek-ai/dsh-client-hmr, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-modules, @deepseek-ai/dsh-client-ui-agent-preset, @deepseek-ai/dsh-client-ui-approval, @deepseek-ai/dsh-client-ui-attachment, @deepseek-ai/dsh-client-ui-brand-official, @deepseek-ai/dsh-client-ui-chat, @deepseek-ai/dsh-client-ui-commands, @deepseek-ai/dsh-client-ui-conversation, @deepseek-ai/dsh-client-ui-cordis, @deepseek-ai/dsh-client-ui-deliverables, @deepseek-ai/dsh-client-ui-directory-picker-browse, @deepseek-ai/dsh-client-ui-directory-picker-native, @deepseek-ai/dsh-client-ui-goal, @deepseek-ai/dsh-client-ui-input-trigger, @deepseek-ai/dsh-client-ui-jobs, @deepseek-ai/dsh-client-ui-layout, @deepseek-ai/dsh-client-ui-message-feedback, @deepseek-ai/dsh-client-ui-model-selection, @deepseek-ai/dsh-client-ui-permission-presets, @deepseek-ai/dsh-client-ui-plan, @deepseek-ai/dsh-client-ui-reference, @deepseek-ai/dsh-client-ui-renderer, @deepseek-ai/dsh-client-ui-schedule, @deepseek-ai/dsh-client-ui-session, @deepseek-ai/dsh-client-ui-settings, @deepseek-ai/dsh-client-ui-settings-general, @deepseek-ai/dsh-client-ui-settings-models, @deepseek-ai/dsh-client-ui-settings-plugin-inventory, @deepseek-ai/dsh-client-ui-settings-plugins, @deepseek-ai/dsh-client-ui-sidebar, @deepseek-ai/dsh-client-ui-skill, @deepseek-ai/dsh-client-ui-subagent, @deepseek-ai/dsh-client-ui-theme, @deepseek-ai/dsh-client-ui-tool, @deepseek-ai/dsh-client-ui-trajectory, @deepseek-ai/dsh-client-ui-user-questions, @deepseek-ai/dsh-client-ui-workflow-run, @deepseek-ai/dsh-client-ui-workspace, @deepseek-ai/dsh-cmdline, @deepseek-ai/dsh-code-runtime-worker-thread, @deepseek-ai/dsh-cordis-client-runner, @deepseek-ai/dsh-cordis-host-runner, @deepseek-ai/dsh-file-reference, @deepseek-ai/dsh-file-reference-local, @deepseek-ai/dsh-host-directory-picker-auto, @deepseek-ai/dsh-host-directory-picker-browse, @deepseek-ai/dsh-host-directory-picker-native, @deepseek-ai/dsh-host-frontend-static, @deepseek-ai/dsh-host-plugin-inventory, @deepseek-ai/dsh-host-webserver, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-launch-environment, @deepseek-ai/dsh-message-feedback, @deepseek-ai/dsh-session-log-export, @deepseek-ai/dsh-session-reference, @deepseek-ai/dsh-session-stats, @deepseek-ai/dsh-session-turn-outline, @deepseek-ai/dsh-shell-env, @deepseek-ai/dsh-subprocess, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tool-subagent, @deepseek-ai/dsh-web-frontend, @deepseek-ai/dsh-workspace, @deepseek-ai/schemastery
- README: --- description: "The browser GUI for dsh: interactive chat, model and settings management, and session history, for users running the dsh web surface." kind: "package-bundle" ---
- 接口: Config, WebRuntimeValues, WebStartupValues


## client

### client/connection — 5711 行 / 17 文件

- npm: `@deepseek-ai/dsh-client-connection`
- 层: 3
- 自述: Authenticated RPC transport, generation lifecycle, and browser fixture
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-credentials, @deepseek-ai/schemastery
- README: --- description: "Browser-host wire layer for the web GUI: Remote RPC, event-stream delivery with reconnect, exact Fetch routes, the /api HTTP bridge, and the browser-trust fence." kind: "package-reference" ---
- 类: BrowserAuth, ConnectionController, HostConnectionService
- 接口: ClientConnectionRpc, ClientRequest, ClientTransportHooks, ConnectionConfig, ConnectionConfig, ConnectionFetchHandler, ConnectionFetchRoute, ConnectionGeneration, ConnectionGenerationState, ConnectionHandle, ConnectionHostInfo, ConnectionIndexRequest, ConnectionIndexResponse, ConnectionLoop, ConnectionRpcFailure, ConnectionSinks, ConnectionStateSource, ConnectionTrustRequest, FetchHandler, FixtureOptions, FixtureWorld, HostConnectionFetch, HostConnectionHandle, HostConnectionRpc, RpcRequest, RpcResponse, ServerResponse

### client/hmr — 492 行 / 5 文件

- npm: `@deepseek-ai/dsh-client-hmr`
- 层: 0
- 自述: Dev-only hot-reload driver for script-loaded client entries: SSE rebuilt frames → invalidate/prefetch → fiber swap through the vendored Loader entry
- 依赖: @deepseek-ai/cordis, @deepseek-ai/schemastery
- README: --- description: "Development-only hot reload for browser client plugins: rebuilding a plugin bundle swaps the running plugin in place, for developers iterating on the web GUI." kind: "package-reference" ---
- 接口: Config

### client/locale — 896 行 / 11 文件

- npm: `@deepseek-ai/dsh-client-locale`
- 层: 0
- 自述: Locale plugin: Host-backed preference, extensible language catalog, browser fallback, and typed built-in dictionaries
- 依赖: @deepseek-ai/cordis, @deepseek-ai/schemastery
- README: --- description: "Localization for the web GUI: the zh/en preference, browser-derived fallback, typed namespace dictionaries, and the framework translation seat, for users and plugin authors." kind: "package-reference" ---
- 类: LocaleRuntime
- 接口: LanguageOptionRow, LanguageRegistration, LanguageRowInjected, LanguageRowState, LocaleDefinition, LocaleSettings, LocaleSnapshot

### client/modules — 1766 行 / 6 文件

- npm: `@deepseek-ai/dsh-client-modules`
- 层: 0
- 自述: Client module system, dual-face: node half composes the __DSH_BOOT__ entry graph (incremental dsh.client scan, bundle route, index tap, webPlugins service); browser half is the lazy-CJS module table the vendored cordis Loader consumes as its internal seam
- 依赖: @deepseek-ai/cordis
- README: --- description: "Client module system for the web GUI: the host composes the boot graph and serves plugin bundles, and the browser loads them lazily, for users and maintainers composing or debugging client plugins." kind: "package-reference" ---
- 类: ClientModuleRegistry, ClientModuleSystem
- 接口: BootManifest, BootModuleRow, BootPluginRow, ClientArtifactBaseline, ClientBootstrapModule, ClientBundleRegistration, ClientModuleCreateOptions, ClientModuleLoader, ClientModuleLoaderTarget, ClientModuleRecord, ClientModuleSystemOptions, DshWindow, WebBootBatch, WebBootEntry, WebBootGraph

### client/store — 430 行 / 4 文件

- npm: `@deepseek-ai/dsh-client-store`
- 层: 0
- 自述: React-free observable and snapshot-store contracts with the shared Zustand/Immer engine
- 依赖: @deepseek-ai/cordis
- README: --- description: "Observable browser state stores with explicit snapshots, subscriptions, and lifecycle ownership." kind: "package-library" ---
- 接口: EngineStoreHandle, EngineStoreInstance, ObservableSnapshot, SnapshotStore, StoreHandle, StoreInstance, StoreSpec

### client/ui-agent-preset — 1827 行 / 11 文件

- npm: `@deepseek-ai/dsh-client-ui-agent-preset`
- 层: 0
- 自述: Agent-preset surfaces: the default for later sessions, this session's seat, and the composition editor
- 依赖: @deepseek-ai/cordis
- README: --- description: "Agent-preset surfaces for the Web GUI: the default-preset setting, the new-session chip, the session-header label, and the preset roster management section; for users and maintainers of agent composition." kind: "package-reference" ---
- 类: AgentPresetSeatController, AgentPresetSectionController, AgentPresetSettingsController
- 接口: AgentPresetLabelInjected, AgentPresetOption, AgentPresetSeatInjected, AgentPresetSeatState, AgentPresetSectionInjected, AgentPresetSectionState, AgentPresetSettingsState, CopyDraft, PresetRow, PresetView

### client/ui-approval — 366 行 / 7 文件

- npm: `@deepseek-ai/dsh-client-ui-approval`
- 层: 0
- 自述: Approval composer takeover over the scoped Remote Event waterfall
- 依赖: @deepseek-ai/cordis
- README: --- description: "Browser approval UI that answers Host permission requests through the scoped interaction path." kind: "package-reference" ---
- 类: PendingApproval
- 接口: ApprovalDetailOwnerProps, ApprovalPresentationRequest

### client/ui-attachment — 760 行 / 11 文件

- npm: `@deepseek-ai/dsh-client-ui-attachment`
- 层: 0
- 自述: Dynamic attachment presentation plugin for conversation input, message-image, and trajectory image slots
- 依赖: @deepseek-ai/cordis
- README: --- description: "Attachment presentation for the conversation UI: draft-image rail, document drop target, history-image gallery, and original-image lightbox; for users and maintainers of the Web attachment experience." kind: "package-reference" ---
- 接口: AttachmentRailItem, AttachmentRailLabels, DropOverlayLabels, ImageLightboxLabels, MessageImageLabels

### client/ui-brand-official — 82 行 / 5 文件

- npm: `@deepseek-ai/dsh-client-ui-brand-official`
- 层: 0
- 自述: Official DeepSeek Harness brand occupants for the Web client's sidebar slots
- 依赖: @deepseek-ai/cordis
- README: --- description: "Official DeepSeek Harness brand occupants for the sidebar, active only in official builds; for users and maintainers choosing or replacing brand presentation." kind: "package-reference" ---

### client/ui-chat — 9167 行 / 70 文件

- npm: `@deepseek-ai/dsh-client-ui-chat`
- 层: 0
- 自述: Chat Conversation target, node definitions, renderers, and details surface
- 依赖: @deepseek-ai/cordis, @deepseek-ai/schemastery
- README: --- description: "Browser Chat target that renders Session conversation nodes, details, historical images, actions, localization, and scroll state." kind: "package-reference" ---
- 类: ChatSnapshotBuilder, PartialAccumulator, SteeringHistory, ToolCallTree, TranscriptViewPolicy
- 接口: AssistantActionOwnerProps, AssistantChatData, AssistantMarkdownProps, ChatConversationViewNode, ChatFileMentions, ChatLocationNodeIndex, ChatNodeDataMap, ChatNodeDataMap, ChatNodeOwnerProps, ChatNodeStore, ChatNodeTurnDataInjected, ChatScrollPosition, ChatSettings, ChatSnapshot, ChatStoreState, ChatTurnNavigationIndex, ChatViewInjected, CommandRowOwnerProps, ContextInjectionRowProps, ConversationContext, DetailsInjected, DetailsToolOwnerProps, DisplayFailure, GenericCommandCardProps, InboxState, LegacyConversationSlice, ManualCompactionChatData, MessageIconActionsProps, RetryChatData, RetryState, SelectionTarget, StatsLineProps, StepReading, SystemPromptRowProps, ToolChatData, TranscriptViewRowInjected, TurnMetrics, TurnNavigationItem, TurnProcessChatData, TurnProcessOwnerProps, TurnProcessSpec, TurnProcessViewEntry, TurnRailItem, TurnTailChatData, TurnTailOwnerProps, TurnTimePanelProps, TurnTokenUsage, TurnTokenUsageRoute, TurnUsagePanelProps

### client/ui-commands — 1380 行 / 10 文件

- npm: `@deepseek-ai/dsh-client-ui-commands`
- 层: 0
- 自述: Client command surface: global directory cache, '/' source, three command UI kinds, popupSelect registry
- 依赖: @deepseek-ai/cordis
- README: --- description: "Client command API for the Web GUI: the / command source, three dispatch kinds, the per-session command directory, and popupSelect registration for business packages; for users and maintainers of slash commands." kind: "package-reference" ---
- 类: CommandDirectory, CommandUiRuntime, PopupSelectController
- 接口: CommandContribution, CommandDecoration, CommandUiContract, PopupSelectDeps, PopupSelectInjected, PopupSpec, PopupState, SelectConfirmation, SelectOption

### client/ui-conversation — 9895 行 / 56 文件

- npm: `@deepseek-ai/dsh-client-ui-conversation`
- 层: 0
- 自述: Target-neutral Conversation assembly, shell, composer, queue, and view navigation
- 依赖: @deepseek-ai/cordis, @deepseek-ai/schemastery
- README: --- description: "Target-neutral conversation assembly and browser shell: event and view registries, per-session bindings, input state, slots, and temporary composer takeovers." kind: "package-reference" ---
- 类: ComposerBlockRegistry, ComposerSubmissionPolicy, ConversationController, ConversationDefinitionRegistry, ConversationEventRegistry, ConversationLocationIndex, ConversationNodeAssembler, ConversationViewRegistry, HistoricalImageCache, InputHub, ReferenceChipNode, SessionInputShell, SubmitMachine, TextRefNode, UiConversation, UnsupportedImageMediaTypeError
- 接口: AssistantMessageNode, AssistantProvenanceView, AssistantRequestConfig, AssistantTiming, BeginCommandRequest, BrowserIdentity, CommandClaim, CommandNode, CompactionSummaryNode, ComposerAttachment, ComposerAttachmentsOwnerProps, ComposerBarInjected, ComposerBarOwnerProps, ComposerBlock, ComposerBlocks, ComposerChainProps, ComposerContentEditableProps, ComposerKeyboard, ComposerKeymapHandlers, ComposerLayout, ComposerSegment, ConsumeTokenRequest, ContextMessageNode, ContextMeterProps, ContextOccupancy, ContextProvenanceView, ConvViewOwnerProps, ConversationBinding, ConversationContextReader, ConversationEventDefinitions, ConversationHeaderActionOwnerProps, ConversationHeaderLineageOwnerProps, ConversationInjected, ConversationLocationDataChange, ConversationLocationDataStore, ConversationMatchResult, ConversationNodeContext, ConversationNodeDefinition, ConversationPreviousContext, ConversationPromptSnapshot, ConversationSessionHeaderInjected, ConversationSessionInjected, ConversationSettings, ConversationSnapshot, ConversationStepDataMap, ConversationStoreState, ConversationTimelineSnapshot, ConversationTurnDataMap, ConversationViewBuilder, ConversationViewDefinition, ConversationViewDefinitions, ConversationViewNode, ConversationViewRequest, ConversationViewSnapshotMap, ConversationViewSnapshotStore, DecoratorPortalsProps, DetectSpan, EditSelection, EditorProjection, EmptyWorkspaceOwnerProps, EnterBehaviorRowInjected, HeroAgentPresetOwnerProps, HeroBrandMarkOwnerProps, HeroShellProps, IConversation, InputActions, InputControlOwnerProps, InputNotice, InputState, InputTarget, InputTriggerController, InputTriggerHit, InputZone, InsertReferenceRequest, InsertTextRequest, MessageImagesOwnerProps, Occurrence, PartialAssistant, PermissionSelectProps, PopupDismissFace, QueueDockInjected, ReferenceChipProps, ReferenceInsert, RequestInspectionSnapshot, RequestPromptChange, RequestPromptInspection, RunningToolCall, SessionInput, SessionInputDeps, SessionInputResolver, SteeringMessageNode, StepLocation, SubmitAttempt, SubmitImageAttachment, SubmitOutcome, SubmitSnapshot, TextRefRange, TodoPanelProps, TokenSpan, ToolResultNode, TurnErrorNode, TurnLocation, TurnMaxTokensNode, UnknownSurfaceNode, UserMessageNode, ViewTab

### client/ui-deliverables — 564 行 / 7 文件

- npm: `@deepseek-ai/dsh-client-ui-deliverables`
- 层: 0
- 自述: Produced-files turn tail and clickable final-response file references for Web
- 依赖: @deepseek-ai/cordis
- README: --- description: "Produced-files and clickable file references for the Web GUI: the deliverables row a finished turn ends with, and inline-code links in the closing prose; for users and maintainers of the deliverables experience." kind: "package-reference" ---
- 接口: DeliverablesTurnData, ProducedFilesInjected

### client/ui-directory-picker-browse — 1234 行 / 6 文件

- npm: `@deepseek-ai/dsh-client-ui-directory-picker-browse`
- 层: 0
- 自述: In-app directory browsing surface: the workspace directory-flow owner rendering the host's listing and creation primitives
- 依赖: @deepseek-ai/cordis
- README: --- description: "In-app directory-browsing surface: the Miller-column Select Workspace Directory dialog that fills workspace directory flows; for users and maintainers of the Web picking experience." kind: "package-reference" ---
- 接口: BrowseFlowInjected, DirectoryBrowserProps

### client/ui-directory-picker-native — 151 行 / 5 文件

- npm: `@deepseek-ai/dsh-client-ui-directory-picker-native`
- 层: 0
- 自述: Native directory-picker surface: the renderless workspace directory-flow occupant driving the host's OS chooser
- 依赖: @deepseek-ai/cordis
- README: --- description: "Native directory-picker surface: the browser half that drives the host OS chooser for workspace-directory flows; for users and maintainers choosing a picking interaction." kind: "package-reference" ---
- 接口: NativeFlowInjected

### client/ui-goal — 516 行 / 9 文件

- npm: `@deepseek-ai/dsh-client-ui-goal`
- 层: 0
- 自述: Session goal surface: GoalBar docked above the composer, read from the goal session projection
- 依赖: @deepseek-ai/cordis
- README: --- description: "Goal surface for the Web GUI: the composer-context strip that shows the current goal and edits, pauses, resumes, or clears it; for users and maintainers of the goal experience." kind: "package-reference" ---
- 接口: GoalBarActions, GoalBarProps, GoalCommandInputData, GoalLocalFailure

### client/ui-input-trigger — 1606 行 / 14 文件

- npm: `@deepseek-ai/dsh-client-ui-input-trigger`
- 层: 0
- 自述: Input trigger pipeline: '/' and '@' detection, candidate menu, pick routing to registered sources
- 依赖: @deepseek-ai/cordis
- README: --- description: "Input trigger pipeline for the Web GUI: / and @ detection under the caret, the grouped candidate menu, and pick routing to registered sources; for users and maintainers of slash commands and references." kind: "package-reference" ---
- 类: InputTriggerController, InputTriggerService
- 接口: CandidateRequest, ClientSessionContext, HeaderRequest, InputTriggerCandidate, InputTriggerControllerDeps, InputTriggerCrumb, InputTriggerPick, InputTriggerServiceContract, InputTriggerSource, MenuState, MenuViewInjected, ReferenceCodec, SourceRoster, SubmitEnvelope, TriggerGuard, TriggerHit

### client/ui-jobs — 314 行 / 6 文件

- npm: `@deepseek-ai/dsh-client-ui-jobs`
- 层: 0
- 自述: Session-header background-job list: live registry state mirrored from session/jobs frames
- 依赖: @deepseek-ai/cordis
- README: --- description: "Web background-job surface: the session-header action listing the jobs this session can see; for users and maintainers of the background-job experience." kind: "package-reference" ---

### client/ui-layout — 725 行 / 10 文件

- npm: `@deepseek-ai/dsh-client-ui-layout`
- 层: 0
- 自述: Shell plugin: three-column AppFrame with drag handles, ctx.layout viewing-state service (navigation + panels)
- 依赖: @deepseek-ai/cordis
- README: --- description: "Shell layout for the Web GUI: the three-column AppFrame with drag handles, concession behavior, the panel-geometry service, and theme presentation; for users and maintainers of the window chrome." kind: "package-reference" ---
- 类: LayoutController, ThemePresenter
- 接口: Columns, ConvOwnerProps, DetailsOwnerProps, DocumentTitleProps, ILayout, SidebarOwnerProps

### client/ui-message-feedback — 902 行 / 8 文件

- npm: `@deepseek-ai/dsh-client-ui-message-feedback`
- 层: 0
- 自述: Per-message feedback controls contributed to the assistant-message action strip, backed by the messageFeedback Host Remote
- 依赖: @deepseek-ai/cordis
- README: --- description: "Per-message feedback for the Web GUI: the Like/Dislike pair and optional note in the finalized assistant message's action row; for users and maintainers of the feedback experience." kind: "package-reference" ---
- 类: MessageFeedbackController
- 接口: MessageFeedbackInjected, MessageFeedbackView

### client/ui-model-selection — 1050 行 / 10 文件

- npm: `@deepseek-ai/dsh-client-ui-model-selection`
- 层: 0
- 自述: Model selection over the shared model catalog, Session projection, and session.selectModel
- 依赖: @deepseek-ai/cordis
- README: --- description: "Model selection for the Web GUI: the /model popup and the composer model seat over one per-session provider-grouped directory; for users and maintainers of model routing." kind: "package-reference" ---
- 类: ModelCatalogDirectory, ModelDirectory, ModelDirectoryResolver
- 接口: ModelCatalogState, ModelDirectoryState, ModelSelectInjected

### client/ui-permission-presets — 681 行 / 8 文件

- npm: `@deepseek-ai/dsh-client-ui-permission-presets`
- 层: 0
- 自述: Permission surfaces: a new-session default in General settings and a current-session /permission popup over the permissions projection
- 依赖: @deepseek-ai/cordis
- README: --- description: "Permission preset surfaces for the Web GUI: the General-settings default row and the /permission picker for the current session; for users and maintainers of permission policy." kind: "package-reference" ---
- 类: PermissionPresetSettingsController
- 接口: PermissionDefaultOption, PermissionRowInjected, PermissionSettingsState

### client/ui-plan — 206 行 / 6 文件

- npm: `@deepseek-ai/dsh-client-ui-plan`
- 层: 0
- 自述: Plan-mode composer control: the conversation.input.plan seat over the plan projection and the /plan command channel
- 依赖: @deepseek-ai/cordis
- README: --- description: "Plan-mode status chip for the Web GUI: the composer control that shows plan mode is on and turns it off; for users and maintainers of plan mode." kind: "package-reference" ---
- 接口: PlanChipInjected

### client/ui-primitives — 7710 行 / 51 文件

- npm: `@deepseek-ai/dsh-client-ui-primitives`
- 层: 0
- 自述: Pure React atoms for the dsh web UI: controls, icons, markdown, and JSON inspectors (zero cordis)
- 依赖: @deepseek-ai/cordis
- README: --- description: "Shared React UI atoms for the dsh web client: controls, icons, markdown and math rendering, and the terminal/read/diff/search/web output cards (zero cordis)." kind: "package-library" ---
- 类: IncrementalMarkdownParser, StreamingHighlightSession
- 接口: AnchoredPositionOptions, AnsiSpan, BrandWordmarkProps, CodeBlockProps, CopyFeedback, DiffBlockLabels, DiffBlockProps, DiffHunk, DisclosureRowProps, HeadTailCap, HighlightSpan, IconProps, IncrementalBlocks, JsonTreeLabels, JsonTreeProps, MarkdownCodeLabels, MarkdownFileMentions, MarkdownLabels, MarkdownPlainTextOptions, MarkdownRenderContext, MenuItem, MenuLabel, MenuSeparator, PointerGrace, PositionedBlock, ReadBlockLabels, ReadBlockLine, ReadBlockProps, ReferenceIconProps, ReferenceTargets, RelativeTime, RiskConfirmationProps, SearchBlockLabels, SearchBlockLineMatch, SearchFileGroup, SearchMatchesBlockProps, SearchPathsBlockProps, StreamingHighlightFrame, TerminalBlockLabels, TerminalBlockProps, WebBlockLabels, WebFetchBlockProps, WebSearchBlockProps, WebSourceView

### client/ui-reference — 327 行 / 5 文件

- npm: `@deepseek-ai/dsh-client-ui-reference`
- 层: 0
- 自述: Unified Web @file and @session reference source
- 依赖: @deepseek-ai/cordis
- README: --- description: "Web @file and @session reference source for the composer: candidates, ordering, and atomic inline references (unified file/session picking)." kind: "package-reference" ---

### client/ui-renderer — 1903 行 / 9 文件

- npm: `@deepseek-ai/dsh-client-ui-renderer`
- 层: 0
- 自述: Browser UI renderer: React slot bindings, ctx.uiRenderer, and the assembled application root
- 依赖: @deepseek-ai/cordis
- README: --- description: "Browser UI renderer: React slot bindings, ctx.uiRenderer, and the assembled application root for the dsh web client." kind: "package-reference" ---
- 类: SlotAssemblyError, SlotRegistry
- 接口: AssemblyDeps, RootOwnerProps, UiRendererService

### client/ui-schedule — 325 行 / 6 文件

- npm: `@deepseek-ai/dsh-client-ui-schedule`
- 层: 0
- 自述: Read-only active Schedule catalog in the Web Session header
- 依赖: @deepseek-ai/cordis
- README: --- description: "The read-only Web catalog for active Schedule reminders, for users choosing the surface and maintainers of its projection, timing, and accessibility behavior." kind: "package-reference" ---

### client/ui-session — 573 行 / 5 文件

- npm: `@deepseek-ai/dsh-client-ui-session`
- 层: 0
- 自述: Session Controller adapter for React and session-scoped slots
- 依赖: @deepseek-ai/cordis
- README: --- description: "React and Slot adapters for Session Controller lists, interaction state, and per-session context." kind: "package-reference" ---
- 类: UiSession
- 接口: SessionPendingInteractionBase, SessionPendingInteractionMap, SessionSourceContribution, SessionSourceDescriptor

### client/ui-settings — 1007 行 / 9 文件

- npm: `@deepseek-ai/dsh-client-ui-settings`
- 层: 0
- 自述: Settings domain base plugin: the settings-namespace scope service and the canonical settings slot-type contract
- 依赖: @deepseek-ai/cordis
- README: --- description: "Settings domain base plugin: the settings-namespace scope service, schema service, and the canonical settings slot-type contract for the dsh web client." kind: "package-reference" ---
- 类: SettingsDescribeMirror, SettingsSchemaService, SettingsScopeBinder, SettingsScopeController
- 接口: SettingsDescribeFace, SettingsDescribeView, SettingsGeneralItemOwnerProps, SettingsHeaderOwnerProps, SettingsMirrorSnapshot, SettingsOnboardingOwnerProps, SettingsPluginsTabOwnerProps, SettingsScope, SettingsScopeSnapshot, SettingsScopeSpec, SettingsSectionOwnerProps, SettingsTriggerOwnerProps

### client/ui-settings-general — 792 行 / 11 文件

- npm: `@deepseek-ai/dsh-client-ui-settings-general`
- 层: 0
- 自述: Settings ownerless-copy and product onboarding plugin: the General section, shell trigger/header chrome content, settings dictionaries, and the versioned welcome notice
- 依赖: @deepseek-ai/cordis, @deepseek-ai/schemastery
- README: --- description: "Settings shell, ownerless copy, and durable product-onboarding namespace for the dsh web client: the General section, trigger chrome, and onboarding ledger projection." kind: "package-reference" ---
- 类: SettingsDocumentStore
- 接口: SettingsDocumentActionInjected, SettingsDocumentState, SettingsOnboardingStep, SettingsSectionRow

### client/ui-settings-models — 3686 行 / 21 文件

- npm: `@deepseek-ai/dsh-client-ui-settings-models`
- 层: 0
- 自述: Models settings and shared product-onboarding dialogs over existing settings and credential joins
- 依赖: @deepseek-ai/cordis
- README: --- description: "Models settings and product-onboarding plugin for the dsh web client: provider rows, API-key management, model lists, and the DeepSeek first-run dialogs." kind: "package-reference" ---
- 类: ModelsSettingsStore, WelcomeNoticeStore
- 接口: CustomProviderCardProps, DeepSeekModelsEditorProps, DeepSeekModelsValidationFailure, DeepSeekOnboardingInjected, EditorFooterProps, ModelListEditorProps, ModelsFooterOwnerProps, ModelsOperations, ModelsSectionInjected, ModelsSettingsState, ProbeTarget, ProviderCardExtrasOwnerProps, ProviderDirectoryEntry, ProviderEditorProps, ProviderIdentity, ProviderRow, WelcomeNoticeInjected, WelcomeNoticeState

### client/ui-settings-plugin-inventory — 653 行 / 6 文件

- npm: `@deepseek-ai/dsh-client-ui-settings-plugin-inventory`
- 层: 0
- 自述: Read-only Cordis Loader inventory tab in Web Plugins settings
- 依赖: @deepseek-ai/cordis
- README: --- description: "Scope-grouped read-only plugin inventory tab in Web Plugins settings for the dsh web client: agent-preset compositions first, the global plane behind a disclosure, search across both." kind: "package-reference" ---
- 接口: PluginInventorySettingsTabInjected

### client/ui-settings-plugins — 2242 行 / 20 文件

- npm: `@deepseek-ai/dsh-client-ui-settings-plugins`
- 层: 0
- 自述: Plugins settings section with feature-owned tabs and configurable host-plane plugin cards
- 依赖: @deepseek-ai/cordis
- README: --- description: "Plugins settings section for the dsh web client: feature-owned tabs, the configurable host-plane plugin cards, and the settings.plugin.item extension point." kind: "package-reference" ---
- 类: AgentLoopCardController, BashCardController, CardForm, ConfigurablePluginsTabController, SubagentModelSelectionCardController, WebSearchCardController
- 接口: AgentLoopCardFace, AgentLoopCardState, AgentLoopSettings, AllowedSubagentModel, BashCardFace, BashCardState, BashSettings, CardActions, CardFieldSpec, CardFieldState, CardSecretSpec, CardShell, ConfigurablePluginsTabFace, ConfigurablePluginsTabState, FieldProps, PluginCardProps, PluginsSettingsSectionInjected, PluginsSettingsTabEntry, SettingsPluginItemOwnerProps, SubagentModelCandidate, SubagentModelSelectionCardFace, SubagentModelSelectionCardState, SubagentModelSelectionSettings, WebSearchCardFace, WebSearchCardState, WebSearchSettings

### client/ui-sidebar — 466 行 / 7 文件

- npm: `@deepseek-ai/dsh-client-ui-sidebar`
- 层: 0
- 自述: Sidebar plugin: session multi-level tree, search, grouping, state dots
- 依赖: @deepseek-ai/cordis
- README: --- description: "Sidebar shell plugin for the dsh web client: brand row, New Session action, collapse control, scroll-aware region seat, and bottom-pinned Settings seat." kind: "package-reference" ---
- 接口: SidebarBrandMarkOwnerProps, SidebarBrandNameOwnerProps, SidebarFooterActionOwnerProps, SidebarSectionOwnerProps, SidebarSettingsOwnerProps

### client/ui-skill — 434 行 / 6 文件

- npm: `@deepseek-ai/dsh-client-ui-skill`
- 层: 0
- 自述: Web skill references and the dedicated skill tool row
- 依赖: @deepseek-ai/cordis
- README: --- description: "Web skill references and the dedicated skill tool row for the dsh web client: the /-triggered skill source and the skill call card." kind: "package-reference" ---

### client/ui-slots — 1471 行 / 5 文件

- npm: `@deepseek-ai/dsh-client-ui-slots`
- 层: 0
- 自述: Slot registry pure core: SlotMap declaration merging, single register composition API, four-share props types, store-seat types, renderer install seam
- 依赖: @deepseek-ai/cordis
- README: --- description: "Slot registry pure core for the dsh web client: SlotMap declaration merging, the single register composition API, four-share props types, store seats, and the renderer install contract." kind: "package-library" ---
- 类: SlotCore, SlotOwnershipError, StaleAuthorizationError
- 接口: ChainRenderOpts, GlobalStandardProps, LiveSlotNode, LiveSlotOccupant, LocaleFace, LocaleNamespaceMap, RenderOpts, RenderOpts, RootStandardSourceContribution, ScopedStandardSourceBinding, SessionAreaProps, SessionMaybeStandardProps, SessionStandardProps, SlotEntryDef, SlotMap, SlotRenderer, SlotRendererHost, SlotScopeAdapter, StandardSourceBinding, StoreInstanceLike, StoredEntry

### client/ui-subagent — 1135 行 / 8 文件

- npm: `@deepseek-ai/dsh-client-ui-subagent`
- 层: 0
- 自述: Subagent conversation catalog, continuation routing UI, and '@' reference source
- 依赖: @deepseek-ai/cordis
- README: --- description: "Subagent conversation catalog, continuation routing UI, and '@' reference source for the dsh web client." kind: "package-reference" ---
- 接口: SubagentCatalogInjected, SubagentDescendantSummary, SubagentReadOnlyMatch

### client/ui-theme — 915 行 / 11 文件

- npm: `@deepseek-ai/dsh-client-ui-theme`
- 层: 0
- 自述: Theme plugin: Host bootstrap for the pre-plugin palette; DOM-free ThemeRuntime for light/dark/system state; --dsw-* token styles and Appearance settings row
- 依赖: @deepseek-ai/cordis, @deepseek-ai/schemastery
- README: --- description: "Theme and content-font-size settings for the dsh web client: --dsw-* token stylesheets, ThemeRuntime state, General settings rows, and the pre-plugin bootstrap." kind: "package-reference" ---
- 类: ThemeRuntime
- 接口: AppearanceRowInjected, AppearanceRowState, FontSizeRowInjected, FontSizeRowState, ThemeDefinition, ThemeSettings, ThemeSnapshot, ThemeTokenInspection, ThemeTokenModes

### client/ui-tool — 2601 行 / 29 文件

- npm: `@deepseek-ai/dsh-client-ui-tool`
- 层: 0
- 自述: Client Tool call-tree renderer and keyed per-tool presentation slot
- 依赖: @deepseek-ai/cordis
- README: --- description: "Client Tool presentation plugin for the dsh web client: whole-call tree composition, the keyed per-tool view slot, and the built-in atomic tool cards." kind: "package-reference" ---
- 接口: DiffCardModel, GenericToolCardProps, ParsedToolCall, PlanItemLike, PlanSummary, SearchCardModel, TerminalCardModel, ToolCallOwnerProps, ToolRowModel, ToolRowProps

### client/ui-trajectory — 8827 行 / 30 文件

- npm: `@deepseek-ai/dsh-client-ui-trajectory`
- 层: 0
- 自述: Trajectory event ledger with an interactive timing overview: pure-consumer plugin registering into the conversation ViewMap (no service)
- 依赖: @deepseek-ai/cordis
- README: --- description: "Trajectory view for the dsh web client: a turn-aware event ledger with an interactive timing overview, registered into the conversation view ring." kind: "package-reference" ---
- 类: TrajectorySearchIndex, TrajectorySnapshotBuilder
- 接口: AssistantMetricDetail, DisplayFailure, TrajectoryCellProps, TrajectoryConversationViewNode, TrajectoryGroupHeaderProps, TrajectoryGroupModel, TrajectoryLayoutInput, TrajectoryRequestHeaderState, TrajectorySnapshot, TrajectorySourceBlock, TrajectoryTableProps, TrajectoryTimeRange, TrajectoryTimelineModel, TrajectoryTimelineProps, TrajectoryTimelineSpan, TrajectoryTimelineTurnBoundary, TrajectoryToolbarProps, TrajectoryTurnHeaderProps, TrajectoryTurnModel, TrajectoryTurnProps, TrajectoryUsage, TrajectoryViewInjected, TrajectoryVirtualRow, TrajectoryVirtualRowEntry, VirtualizableTrajectoryRecord

### client/ui-user-questions — 1006 行 / 9 文件

- npm: `@deepseek-ai/dsh-client-ui-user-questions`
- 层: 0
- 自述: Web ask_user_question composer takeover and plan-review presentation UI
- 依赖: @deepseek-ai/cordis
- README: --- description: "Web ask_user_question feature for the dsh web client: the composer-takeover question UI and the plan-review approval card." kind: "package-reference" ---
- 类: PendingQuestion
- 接口: PlanReview, QuestionDraftAnswer, QuestionDraftProgress

### client/ui-workflow-run — 780 行 / 7 文件

- npm: `@deepseek-ai/dsh-client-ui-workflow-run`
- 层: 0
- 自述: Durable workflow-run Conversation Node and nested member disclosure for dsh web
- 依赖: @deepseek-ai/cordis
- README: --- description: "Durable workflow-run Conversation Node for the dsh web client: reconstructs top-level workflow runs as independent chat nodes with nested member disclosure." kind: "package-reference" ---
- 接口: WorkflowRunChatData, WorkflowRunInjected, WorkflowRunMemberData, WorkflowRunPhaseData

### client/ui-workspace — 3427 行 / 13 文件

- npm: `@deepseek-ai/dsh-client-ui-workspace`
- 层: 0
- 自述: Workspace picker plugin: one WorkspacePicker registered into the sidebar and empty-state workspace slots
- 依赖: @deepseek-ai/cordis
- README: --- description: "Shared Workspace browser and picker plugin for the dsh web client: grouped or flat session rows, add/rename/reorder, search, fork, archive, and the directory-flow picking hole." kind: "package-reference" ---
- 类: DirectoryBrowseError
- 接口: DirectoryFlowOwnerProps, GroupNode, RowDragProps, SearchResultNode, SearchResultSet, SessionNode, SubagentDescendantSummary, TreeView, UiWorkspace, WorkspacePickFlowProps

### client/web — 403 行 / 8 文件

- npm: `@deepseek-ai/dsh-client-web`
- 层: 0
- 自述: Web boot kernel: static module table, Cordis loader, framework-free boot page, and UI-renderer handoff
- 依赖: @deepseek-ai/cordis
- README: --- description: "Web boot kernel for the web GUI: two-stage boot of the client plugin tree, the framework-free boot page, and the shared module table, for users and maintainers composing or debugging the browser application." kind: "package-library" ---
- 类: AppWebEntry, BootPage


## code-runtime

### code-runtime/code-runtime — 294 行 / 3 文件

- npm: `@deepseek-ai/dsh-code-runtime`
- 层: 1
- 自述: Abstract code-execution seam (ctx.codeRuntime) for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: --- description: "Abstract code-execution seam (`ctx.codeRuntime`) for users and maintainers composing, consuming, or building a backend that runs one model-written program against host-provided bindings." kind: "package-reference" ---
- 类: CodeRuntime
- 接口: CodeBindingErrorClass, CodeBindingNamespace, CodeRunFailure, CodeRunRequest, CodeRunResult

### code-runtime/code-runtime-python — 722 行 / 4 文件

- npm: `@deepseek-ai/dsh-code-runtime-python`
- 层: 1
- 自述: CPython subprocess implementation of the DeepSeek Harness code-execution seam
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: --- description: "fd-3 wire protocol between a Node host and a CPython subprocess for users and maintainers building or debugging the Python code-execution backend." kind: "package-library" ---
- 接口: BootMessage

### code-runtime/code-runtime-worker-thread — 1725 行 / 8 文件

- npm: `@deepseek-ai/dsh-code-runtime-worker-thread`
- 层: 4
- 自述: Worker-thread implementation of the DeepSeek Harness code-execution seam
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-code-runtime, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-timeout, @deepseek-ai/dsh-util-values, @deepseek-ai/schemastery
- README: --- description: "Worker-thread code execution for users and maintainers composing, sizing, or debugging the shipped TypeScript backend that runs each program in a fresh Node worker." kind: "package-reference" ---
- 类: LogBuffer, WorkerThreadCodeRuntime
- 接口: BootstrapPort, Config, DoneMessage, PatchableStream, PendingCall, WorkerBootData


## compaction

### compaction/command-compact — 136 行 / 2 文件

- npm: `@deepseek-ai/dsh-command-compact`
- 层: 8
- 自述: Human-facing slash command for explicit session compaction
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-commands, @deepseek-ai/dsh-compaction, @deepseek-ai/dsh-invariants
- README: --- description: "The on-demand /compact command for interactive compositions: what it does, what you see, and how to mount it." kind: "package-reference" ---

### compaction/compaction — 805 行 / 7 文件

- npm: `@deepseek-ai/dsh-compaction`
- 层: 7
- 自述: Abstract compaction service seam (ctx.compaction) for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-commands, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session
- README: --- description: "Shared compaction contract for backend implementers and deployers: what conversation condensation does, when to use it, and how to build a backend." kind: "package-reference" ---
- 类: CompactionEngine, ManualCompactionError
- 接口: CompactionAgentContext, CompactionResult, ManualCompactAgentContext

### compaction/compaction-basic — 1632 行 / 6 文件

- npm: `@deepseek-ai/dsh-compaction-basic`
- 层: 10
- 自述: Token-meter-driven compaction policy and LLM summarization backend for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-commands, @deepseek-ai/dsh-compaction, @deepseek-ai/dsh-compaction-tool-result-pruner, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-token-meter, @deepseek-ai/dsh-util-values, @deepseek-ai/schemastery
- README: --- description: "Automatic conversation condensation for deployments choosing, tuning, or debugging how older history is summarized as token pressure builds." kind: "package-reference" ---
- 类: BasicCompactionEngine, TargetPressureConfigError
- 接口: BasicCompactionConfig, CompactionPolicyConfig, ModelCompactPolicyConfig, SummarizationInput

### compaction/compaction-tool-result-pruner — 331 行 / 4 文件

- npm: `@deepseek-ai/dsh-compaction-tool-result-pruner`
- 层: 9
- 自述: Replay-safe model-free head/middle/tail pruning for tool-result surface nodes
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-compaction, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-token-meter, @deepseek-ai/dsh-util-values, @deepseek-ai/schemastery
- README: --- description: "Tool-output trimming for deployments composing compaction: choosing size limits or debugging why oversized tool results get shortened." kind: "package-reference" ---
- 类: ToolResultPruner
- 接口: PruneResult, PrunedEntry, ResolvedConfig, ToolResultPruneConfig


## context

### context/agent-instructions — 1854 行 / 7 文件

- npm: `@deepseek-ai/dsh-agent-instructions`
- 层: 8
- 自述: Workspace context loader for AGENTS.md/CLAUDE.md instruction files
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-fs, @deepseek-ai/dsh-home-paths, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-util-values, @deepseek-ai/schemastery
- README: --- description: "Workspace-instruction context for users and maintainers enabling, sizing, or debugging AGENTS.md/CLAUDE.md loading and refresh." kind: "package-reference" ---
- 接口: AgentInstructionChange, AgentInstructionSource, ChangeRenderItem, Config, InstructionFile, InstructionVersionState, InstructionVersionUpdate, LoadedInstructionFile, ProbedInstructionFile, ReconciledInstructionContext, RenderedInstructionSet, RenderedWorkspaceContext, ResolvedConfig, ResolvedDiscoveryConfig, TruncatedInstruction

### context/file-reference — 143 行 / 4 文件

- npm: `@deepseek-ai/dsh-file-reference`
- 层: 6
- 自述: File-reference discovery contract and shared @file grammar
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants
- README: --- description: "File-reference discovery and @file mention grammar for host-backed UIs, for users and maintainers choosing the seam or pairing it with a provider." kind: "package-reference" ---
- 类: FileReferenceService
- 接口: ActiveAtToken, FileReferenceCandidate

### context/file-reference-local — 556 行 / 3 文件

- npm: `@deepseek-ai/dsh-file-reference-local`
- 层: 8
- 自述: Local-filesystem ctx.fileReferences provider with bounded fuzzy indexes
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-file-reference, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: --- description: "Local-workspace @file completion provider for users and maintainers enabling, sizing, or debugging ctx.fileReferences discovery." kind: "package-reference" ---
- 类: LocalFileReferenceService, WorkspaceFileSearch
- 接口: Config, FileSearchConfig

### context/session-reference — 847 行 / 7 文件

- npm: `@deepseek-ai/dsh-session-reference`
- 层: 10
- 自述: Cross-session snapshot references and durable untrusted model context (ctx.sessionReferenceResolver)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-compaction, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-output-retention, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-session-projection-cache, @deepseek-ai/dsh-session-query, @deepseek-ai/dsh-session-title, @deepseek-ai/dsh-typert-protocol, @deepseek-ai/dsh-util-values, @deepseek-ai/schemastery
- README: --- description: "Cross-session snapshot references and durable untrusted model context, for users and maintainers enabling or debugging ctx.sessionReferenceResolver." kind: "package-reference" ---
- 类: SessionReferenceError, SessionReferenceResolver
- 接口: Config, ParsedSessionReferenceText, PreparedReferencedMessage, ReferenceRetentionStats, ReferencedConversationItem, ReferencedSessionData, SessionReferenceCandidate, SessionReferenceInput, SessionReferenceMentionCandidate, SessionReferenceSource

### context/time-context — 555 行 / 5 文件

- npm: `@deepseek-ai/dsh-time-context`
- 层: 6
- 自述: Opt-in durable per-step context with the current time and elapsed time
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-util-values, @deepseek-ai/schemastery
- README: --- description: "Opt-in per-step clock context with the current time, browser zone, and elapsed time, for users and maintainers enabling or tuning the plugin." kind: "package-reference" ---
- 接口: Config

### context/tmux-context — 295 行 / 2 文件

- npm: `@deepseek-ai/dsh-tmux-context`
- 层: 6
- 自述: Opt-in durable per-step context with this agent's tmux pane and window location
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-shell, @deepseek-ai/schemastery
- README: --- description: "Opt-in per-turn tmux location context for users and maintainers enabling or tuning the agent's session, window, and pane awareness." kind: "package-reference" ---
- 接口: Config


## core

### core/agent — 1710 行 / 10 文件

- npm: `@deepseek-ai/dsh-agent`
- 层: 5
- 自述: Agent interface, registry, initiator scope, and event vocabulary for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-typert-protocol
- README: --- description: "The Agent handle, live registry, process-local initiator scope, and agent/* event vocabulary for plugins, UI, and orchestrators building or extending agents." kind: "package-reference" ---
- 类: AgentRegistry, Inbox
- 接口: Agent, AgentEventDispatch, AgentFactory, AgentHandle, AgentOptions, AgentSetupCommit, CancelOptions, ConsumedWork, CreateAgentOptions, InboxNotifications, ModelSelection, ModelSelectionRef, ResumeAgentOptions, TurnBoundaryProjection

### core/agent-default-model — 164 行 / 3 文件

- npm: `@deepseek-ai/dsh-agent-default-model`
- 层: 6
- 自述: Default model selection shared by Agent entry points
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-settings, @deepseek-ai/schemastery
- README: --- description: "The deployment default model selection for users and maintainers choosing, configuring, or debugging which model freshly created agents start on." kind: "package-reference" ---
- 类: AgentDefaultModelConfig
- 接口: AgentDefaultModelSettings, Config

### core/agent-loop — 1781 行 / 7 文件

- npm: `@deepseek-ai/dsh-agent-loop`
- 层: 8
- 自述: The concrete agent loop plugin for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-settings, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-util-values, @deepseek-ai/schemastery
- README: --- description: "The default agent driver for users and maintainers choosing, configuring, or debugging how agents are created and how turns and steps run." kind: "package-reference" ---
- 类: AgentLoop, ReactLoopAgent, RuntimeContextProjection
- 接口: AgentLoopSettings, Config, ConfiguredAgentIdentities, LauncherAgentIdentity

### core/agent-tool-presentation — 104 行 / 2 文件

- npm: `@deepseek-ai/dsh-agent-tool-presentation`
- 层: 8
- 自述: Agent-plane presentation selector: composes one agent's tools as PTC mode, native, or both
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: --- description: "The agent-plane presentation selector for users and maintainers choosing, configuring, or debugging which form of its tools an agent preset's models see." kind: "package-reference" ---
- 接口: Config

### core/scope — 589 行 / 5 文件

- npm: `@deepseek-ai/dsh-scope`
- 层: 1
- 自述: Scoped-context registration primitive (scope tags, scope-filtered event dispatch) for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: --- description: "The scoped-registration library for plugin authors and maintainers building registries or event surfaces that isolate contributions per agent or per group." kind: "package-library" ---
- 类: AnonymousEntries, NamedEntries, ScopedLayers
- 接口: CreateScopeOptions, Scope, ScopeLayer, ScopeParentBinding

### core/session — 3088 行 / 11 文件

- npm: `@deepseek-ai/dsh-session`
- 层: 3
- 自述: Event-sourced session store for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-util-values
- README: --- description: "The event-sourced session log and in-memory store for users and maintainers building, inspecting, or extending the durable record behind every agent interaction." kind: "package-reference" ---
- 类: Session, SessionForkError, SessionPreparation, SessionStore, SurfaceManager
- 接口: CreateSessionOptions, EpochHeader, RequestContext, RestoredSessionOptions, SessionEventMap, SessionHeader, SessionPreparationOptions, SessionSurface, SurfaceFoldReplacement, SurfaceFoldResult, SurfaceIntent, TurnEndReasonMap

### core/system-prompt — 674 行 / 2 文件

- npm: `@deepseek-ai/dsh-system-prompt`
- 层: 3
- 自述: System prompt assembly registry for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-scope, @deepseek-ai/schemastery
- README: --- description: "System-prompt assembly for users and maintainers adding prompt sections, variables, tool-schema sources, or configuring the model-facing prompt." kind: "package-reference" ---
- 类: SystemPrompt
- 接口: AssembleContext, AssembledContext, AssembledSection, Config, PromptAssembly, PromptContext, PromptSection, ToolProviderResult

### core/tools — 5640 行 / 10 文件

- npm: `@deepseek-ai/dsh-tools`
- 层: 7
- 自述: Tool registry and execution pipeline for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-code-runtime, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-session, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-user-approval, @deepseek-ai/dsh-util-values, @deepseek-ai/schemastery
- README: --- description: "The tool registry and execution pipeline for tool authors and maintainers registering, restricting, presenting, or debugging model-facing tools." kind: "package-reference" ---
- 类: CodeRunFailedError, JsonSchemaError, ToolArgsError, ToolNotFoundError, ToolOutputError, ToolRuntime
- 接口: ArrayValueSchemaSpec, BooleanValueSchemaSpec, Config, DefineToolOptions, DiffCallView, DiffResultView, FileDiff, FileLocation, GenericCallView, GenericResultView, IntegerValueSchemaSpec, JsonSchemaNode, JsonValueSchemaSpec, NullValueSchemaSpec, NumberValueSchemaSpec, ObjectValueSchemaSpec, OneOfValueSchemaSpec, ParameterJsonSchema, PtcDispatchEventData, PtcDispatchLog, PtcDispatchStartEventData, ReadFileLine, ReadResultView, RunCodeBridgeOptions, SearchFileMatches, SearchLineMatch, SearchMatchesResultView, SearchPathsResultView, StringValueSchemaSpec, TerminalCallView, TerminalResultView, ToolDefinition, ToolDispatchExecution, ToolErrorInfo, ToolExecution, ToolExecutionFailure, ToolExecutionInput, ToolExecutionSuccess, ToolFailure, ToolOutputDefinition, ToolRestriction, ToolResult, ToolRunContext, ToolRuntimeScheduler, ToolSdkSchema, ValueSchemaAnnotations, WebFetchResultView, WebSearchResultView, WebSource


## credentials

### credentials/authorization — 573 行 / 3 文件

- npm: `@deepseek-ai/dsh-authorization`
- 层: 3
- 自述: Authorization seam (ctx.authorization): plugin-owned flows that obtain a credential through a conversation with the human
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-credentials, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm
- README: --- description: "The authorization flow registry for users and maintainers who obtain credentials that configuration cannot supply, because getting one means a conversation with a human." kind: "package-reference" ---
- 类: AuthorizationDeclinedError, AuthorizationError, AuthorizationService
- 接口: AuthorizationEntry, AuthorizationFlow, AuthorizationInteraction, AuthorizationMethod, AuthorizationNotice, AuthorizationOutcome, AuthorizationPromptOption, AuthorizationRequest, AuthorizationSession

### credentials/credentials — 458 行 / 3 文件

- npm: `@deepseek-ai/dsh-credentials`
- 层: 2
- 自述: Abstract credential seam (ctx.credentials): settings carry references to secrets, providers own the values
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants
- README: --- description: "The credential seam for users and maintainers resolving, describing, or storing credentials — reference values and durable records — without putting secret values in configuration." kind: "package-reference" ---
- 类: CredentialProvider
- 接口: ApiKeyRecord, CredentialInfo, CredentialRecordEntry, CredentialRecordInfo, GrantRecord, ResolvedCredential

### credentials/credentials-local — 966 行 / 2 文件

- npm: `@deepseek-ai/dsh-credentials-local`
- 层: 3
- 自述: File-backed credentials provider ($DSH_HOME/.env under the live process environment) for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-atomic-write, @deepseek-ai/dsh-credentials, @deepseek-ai/dsh-home-paths, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-launch-environment, @deepseek-ai/schemastery
- README: --- description: "The file-backed credentials provider for users and maintainers choosing, configuring, or debugging the local credential store and its environment layering." kind: "package-reference" ---
- 类: LocalCredentialProvider
- 接口: Config, CredentialsDocument


## e2b

### e2b/e2b — 212 行 / 2 文件

- npm: `@deepseek-ai/dsh-e2b`
- 层: 1
- 自述: Shared E2B sandbox lifecycle for DeepSeek Harness provider adapters
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/schemastery
- README: --- description: "One shared remote Linux sandbox for E2B-backed file and command work: configuration, lifetime, and what happens at startup and shutdown." kind: "package-reference" ---
- 类: E2BRuntime
- 接口: Config

### e2b/fs-e2b — 612 行 / 2 文件

- npm: `@deepseek-ai/dsh-fs-e2b`
- 层: 6
- 自述: E2B filesystem implementation for DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-e2b, @deepseek-ai/dsh-fs, @deepseek-ai/dsh-invariants
- README: --- description: "File operations inside the shared remote sandbox: what the agent can do with files there, when to use it, and what to expect — for deployments and maintainers of the E2B family." kind: "package-reference" ---
- 类: E2BFileSystem

### e2b/subprocess-e2b — 1835 行 / 7 文件

- npm: `@deepseek-ai/dsh-subprocess-e2b`
- 层: 2
- 自述: E2B subprocess implementation for DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-e2b, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-subprocess, @deepseek-ai/dsh-timeout, @deepseek-ai/schemastery
- README: --- description: "Shell commands and terminals inside the shared remote sandbox: what the agent can run there, how output is handled, and what to expect — for deployments and maintainers of the E2B family." kind: "package-reference" ---
- 类: E2BBase64Decoder, E2BOutputReader, E2BSubprocessHandle, E2BSubprocessRuntime, E2BTerminalHandle
- 接口: Config


## experimental

### experimental/agent-team — 2476 行 / 16 文件

- npm: `@deepseek-ai/dsh-experimental-agent-team`
- 层: 11
- 自述: Implicit-root Agent Teams roster, durable peer mailbox, and shared task DAG
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-typert-protocol, @deepseek-ai/schemastery
- README: --- description: "Run a small team of named agents in one session: durable messages between members and a shared task board, for deployments composing the experimental Team plugins." kind: "package-reference" ---
- 类: TeamActivity, TeamError, TeamJournal, TeamMailbox, TeamRoster, TeamRuntimeLifecycle, TeamService, TeamTaskBoard, TeamTaskGraphError
- 接口: Config, CreateTeamTaskRequest, SendTeamMessageRequest, SendTeamMessageResult, SpawnTeammateRequest, SpawnTeammateResult, TeamMemberSnapshot, TeamMemberView, TeamMembership, TeamMessageSnapshot, TeamMessageSource, TeamProjectionState, TeamState, TeamTaskSnapshot, TeamTaskView, TeamView, TeamWaitResult, UpdateTeamTaskRequest

### experimental/agent-team-profile — 34 行 / 2 文件

- npm: `@deepseek-ai/dsh-experimental-agent-team-profile`
- 层: 13
- 自述: Private profile bundle enabling Agent Teams over dsh-base
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-experimental-agent-team, @deepseek-ai/dsh-experimental-tool-agent-team, @deepseek-ai/dsh-invariants
- README: --- description: "Private Agent Teams profile layer over dsh-base, for source-checkout users who want Team-scoped coordination tools while retaining one-shot delegation." kind: "package-bundle" ---

### experimental/agent-team-web-profile — 26 行 / 2 文件

- npm: `@deepseek-ai/dsh-experimental-agent-team-web-profile`
- 层: 13
- 自述: Private Web profile layer for Agent Teams Remote and UI plugins
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-experimental-client-ui-agent-team, @deepseek-ai/dsh-invariants
- README: --- description: "Add the experimental Agent Teams panel to a source-checkout Web profile after the Host Team layer." kind: "package-bundle" ---

### experimental/client-ui-agent-team — 654 行 / 7 文件

- npm: `@deepseek-ai/dsh-experimental-client-ui-agent-team`
- 层: 12
- 自述: Web Agent Teams roster, task board, and teammate navigation
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-api-remotes, @deepseek-ai/dsh-api-session-controller, @deepseek-ai/dsh-client-locale, @deepseek-ai/dsh-client-ui-conversation, @deepseek-ai/dsh-client-ui-primitives, @deepseek-ai/dsh-client-ui-renderer, @deepseek-ai/dsh-client-ui-session, @deepseek-ai/dsh-client-ui-slots, @deepseek-ai/dsh-experimental-agent-team, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-typert-protocol
- README: --- description: "Use and debug the experimental Web Agent Teams roster, shared task board, and teammate navigation panel." kind: "package-reference" ---
- 接口: TeamActionInjected

### experimental/inspector — 15637 行 / 150 文件

- npm: `@deepseek-ai/dsh-experimental-inspector`
- 层: 2
- 自述: Experimental cross-realm CDP hub for Host debugging and Client Runtime inspection
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-loader, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-client-modules, @deepseek-ai/dsh-host-webserver, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-util-crypto, @deepseek-ai/schemastery
- README: --- description: "Experimental Chrome DevTools inspection for Host and browser Client Cordis runtimes, including Console evaluation, Sources, Network capture, Elements trees, and a CDP-independent query API." kind: "package-reference" ---
- 类: CdpSession, ClientBridgeLifecycle, ClientBridgePublisher, ClientBridgeRpc, ClientConsoleBackend, ClientConsoleObserver, ClientInspectorRealm, ClientInspectorSource, ClientObjectStore, ClientRealmSource, ClientRuntimeBackend, ClientRuntimeExecutionError, ClientRuntimeExecutor, ClientRuntimeRemoteError, ClientRuntimeRouter, ClientScriptIdentity, ClientSourceBackend, ClientSourceCatalog, ClientSourceCatalogError, ClientSourceRemoteError, ClientSourceRouter, CordisDomBackend, CordisDomSession, CordisTreeCollector, CordisTreeStore, DebuggerDomainSession, DebuggerScriptRegistry, HostBridgePublisher, HostBridgeRpc, HostCdpBridgeUnavailableError, HostConsoleBackend, HostDebuggerBackend, HostInspectorRealm, HostInspectorSession, HostInspectorSource, HostNativeDomainSession, HostNotificationChannel, HostRuntimeBackend, HostSourceBackend, InspectorEndpoint, InspectorEventSourceParser, InspectorQueryConnection, InspectorQueryPeer, InspectorQueryRemoteError, InspectorQueryRouter, InspectorRealmRegistry, InspectorRealmSessionSet, InspectorSourceBuffer, InspectorSourceConnection, InspectorSourceRegistry, InspectorWorkerLifecycle, NetworkDomain, NetworkStore, RealmObjectGeneration, RealmObjectRegistry, RuntimeDomainSession, RuntimeObjectTable
- 接口: CapturedNetworkBody, CdpExecutionContextSelector, CdpGetPropertiesResult, CdpNotification, CdpRequest, CdpRuntimeCompletion, CdpRuntimeEvent, CdpTargetDescriptor, CdpTransport, ClientBridgeFrameHandlers, ClientConsoleCapability, ClientConsoleDisableFrame, ClientConsoleEnableFrame, ClientConsoleEventFrame, ClientRealmBridge, ClientRuntimeAwaitPromiseCommand, ClientRuntimeCallFunctionCommand, ClientRuntimeCancelFrame, ClientRuntimeCapability, ClientRuntimeError, ClientRuntimeEvaluateCommand, ClientRuntimeGetPropertiesCommand, ClientRuntimeGlobalLexicalScopeNamesCommand, ClientRuntimeLimits, ClientRuntimeObjectOptions, ClientRuntimeReleaseObjectCommand, ClientRuntimeReleaseObjectGroupCommand, ClientRuntimeRequestFrame, ClientRuntimeResponseAcknowledgedFrame, ClientRuntimeResponseFrame, ClientRuntimeSessionClosedFrame, ClientRuntimeTarget, ClientSourceAsset, ClientSourceError, ClientSourceRequestFrame, ClientSourceResponseFrame, ClientSourceSessionClosedFrame, ClientSourcesCapability, Config, ConsoleBackend, CordisContextTreeNode, CordisDomDocument, CordisDomNode, CordisFiberTreeNode, CordisInspectionTree, CordisRuntimeContext, CordisRuntimeFiber, CordisRuntimeRealm, CordisRuntimeSource, CordisRuntimeTree, CordisRuntimeTreeReader, CordisTreeGetQuery, CordisTreeGetResult, CordisTreeLimits, CordisTreeObjectRoute, CordisTreeSnapshot, CordisTreeSource, CordisTreeSourceSnapshot, CordisTreeStoreOptions, DebuggerBackend, DebuggerScriptRoute, FetchBodyChunkPayload, FetchCaptureOptions, FetchEndPayload, FetchErrorPayload, FetchIdentity, FetchObserver, FetchRequestBodyEndPayload, FetchResponsePayload, FetchStartPayload, HostBridgeFrameHandlers, HostPluginConfig, HostSourceOptions, IngestedInspectorRecord, InspectorClientBootstrap, InspectorConnection, InspectorEndpoint, InspectorEndpointInfo, InspectorEventSourceMessage, InspectorHandle, InspectorJsonObject, InspectorObjectReference, InspectorOptions, InspectorPublisher, InspectorQueryConnectionOptions, InspectorQueryError, InspectorQueryFrameIdentity, InspectorQueryPeerTransport, InspectorQueryRequestFrame, InspectorQueryRequester, InspectorQueryResponseFrame, InspectorQuerySender, InspectorRealm, InspectorRealmCapabilities, InspectorRealmDescriptor, InspectorRealmSession, InspectorRecordConsumer, InspectorRecordInput, InspectorService, InspectorService, InspectorService, InspectorSourceBufferOptions, InspectorSourceDescriptor, InspectorSourceView, InspectorSpec, InspectorStatePublisher, InspectorWorkerBoot, InspectorWorkerConfig, InspectorWorkerFailure, InspectorWorkerReady, InspectorWorkerRuntime, InspectorWorkerShutdown, InspectorWorkerStopped, NativeDomainBackend, NativeProtocolNotification, NetworkSink, NetworkStoreOptions, ParsedCallFunction, ParsedEvaluate, RuntimeAwaitPromiseRequest, RuntimeBackend, RuntimeBackendObjectReference, RuntimeCallFrame, RuntimeCallFrameEvaluationRequest, RuntimeCallFunctionRequest, RuntimeCompletion, RuntimeConsoleEvent, RuntimeDebuggerCallFrame, RuntimeDebuggerEnableRequest, RuntimeDebuggerLocation, RuntimeDebuggerResumeRequest, RuntimeDebuggerScope, RuntimeEvaluateRequest, RuntimeExceptionDetails, RuntimeExceptionEvent, RuntimeGetPropertiesRequest, RuntimeInternalPropertyDescriptor, RuntimeObjectPresentation, RuntimeObjectPreview, RuntimeObjectRoute, RuntimePrivatePropertyDescriptor, RuntimeProperties, RuntimePropertyDescriptor, RuntimePropertyPreview, RuntimeRemoteObject, RuntimeRemoteObjectDescriptor, RuntimeScript, RuntimeStackTrace, SourceAcceptedFrame, SourceAppendAcknowledgedFrame, SourceAppendFrame, SourceBackend, SourceCloseFrame, SourceConnection, SourceOpenFrame, SourceRejectedFrame, SourceReplaceFrame, SourceResnapshotFrame

### experimental/tool-agent-team — 436 行 / 2 文件

- npm: `@deepseek-ai/dsh-experimental-tool-agent-team`
- 层: 12
- 自述: Scoped model-facing Agent Teams tools over ctx.agentTeams
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-experimental-agent-team, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: --- description: "Ten tools that let the model create, message, and coordinate teammates, for compositions mounting the experimental Team plugins." kind: "package-reference" ---
- 接口: Config

### experimental/webworker-packer — 1129 行 / 8 文件

- npm: `@deepseek-ai/dsh-experimental-webworker-packer`
- 层: 5
- 自述: Build-time packer for the browser runtime's base VFS image and ordered data-overlay archives
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-include, @deepseek-ai/dsh-experimental-webworker-runtime, @deepseek-ai/dsh-home-paths, @deepseek-ai/dsh-invariants
- README: --- description: "Browser-worker VFS image packaging for maintainers building or debugging the experimental preview deployment." kind: "package-library" ---
- 接口: ConfigTree, ImageTree, PackOptions, PackOverlayResult, PackResult, PreviewFixture, TransformOutcome

### experimental/webworker-runtime — 13032 行 / 80 文件

- npm: `@deepseek-ai/dsh-experimental-webworker-runtime`
- 层: 4
- 自述: Browser-only harness runtime: in-memory VFS, module transform and loader, postMessage tunnel, and the dedicated Web Worker assembly, with the Node-compatibility layer that lets the host tree run unchanged
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-loader, @deepseek-ai/dsh-client-connection, @deepseek-ai/dsh-client-modules, @deepseek-ai/dsh-host-webserver, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-util-crypto
- README: --- description: "Browser-worker harness hosting for maintainers building or debugging the experimental Web preview runtime." kind: "package-library" ---
- 类: AsyncLocalStorage, Dirent, EventEmitter, FSWatcher, LandlockLauncherError, MemoryVfs, ReadStream, ServerResponse, Socket, StatWatcher, TunnelServer, WebSocket, WebSocketServer, WorkerChildProcess, WorkerModuleLoader, WorkerTunnel, WriteStream
- 接口: AlsCausality, AlsRuntime, AlsToken, BootPayload, Dir, ExpansionContext, FileHandle, FilesystemCallFrame, FilesystemReplyFrame, Hash, HostContext, LogExporter, LogMessage, LogRenderer, LoweredModule, MemoryVfsOptions, ParsedOptions, ParsedPath, PreviewFixtureManifest, PreviewFixtureManifestEntry, ProcessScope, ProcessShim, ProcessShimOptions, ProcessStartOptions, ReadStreamOptions, ResponseSink, RunningProcess, ShellDirent, ShellExitFrame, ShellFileSystem, ShellIo, ShellOutputFrame, ShellRunOptions, ShellRunOutcome, ShellSignalFrame, ShellStartFrame, ShellState, ShellStats, SyntheticExchange, TarEntry, TimerHandle, TunnelAbortFrame, TunnelInitFrame, TunnelPort, TunnelRequestFrame, TunnelResponseChunkFrame, TunnelResponseEndFrame, TunnelResponseErrorFrame, TunnelResponseFrame, TunnelResponseHeadFrame, TunnelSeams, TunnelServerOptions, TunnelStreamEndFrame, TunnelStreamErrorFrame, TunnelStreamItemFrame, TunnelStreamOpenFrame, Vfs, VfsBigIntStats, VfsDir, VfsDirent, VfsError, VfsFileHandle, VfsMutationSink, VfsOpenFile, VfsSeedOptions, VfsStatOptions, VfsStats, VfsWriteOptions, VirtualExecutable, VirtualExecutableDelegate, VirtualExecutableExit, WatchFileOptions, WatchOptions, WorkerHost, WorkerHostConnectOptions, WorkerHostConnection, WorkerHostOptions, WorkerHostSource, WorkerHostSourceOptions, WorkerInternalResolution, WorkerModuleLoaderOptions, WorkerProcessEntry, WorkerRequire, WorkerRequireResolve, WorkerSpawnOptions, WorkerSpawnSyncResult, WriteStreamOptions


## extensions

### extensions/cordis-client-runner — 5574 行 / 13 文件

- npm: `@deepseek-ai/dsh-cordis-client-runner`
- 层: 0
- 自述: Browser half of dynamic dual-half plugin packages: event subscription, closure evaluation, guard facade, and loader entries
- 依赖: @deepseek-ai/cordis
- README: --- description: "Browser half of dynamic Cordis packages for users and maintainers choosing, composing, or debugging how a page answers run requests and loads browser-half code." kind: "package-reference" ---
- 类: ClientCordisInspectRegistry, ClientTimerService, CordisRunOrchestrator, DynamicCordisPackageRunner, DynamicCordisStyles
- 接口: ApiParameter, ClientCordisInspectHost, ClientCordisInspectProviderRegistration, ClientCordisInspectQueryContext, ClientSlotEntry, ClientSlotOption, CordisErrorDetails, CordisObservable, CordisRunFailure, CordisRunHostSeam, CordisRunOrchestratorEnv, CordisRunRequest, CordisRunnerFace, CordisUserRunRequest, DynamicCordisClientHalf, DynamicCordisClosureEnv, DynamicCordisEvaluatedPlugin, DynamicCordisGuardEnv, DynamicCordisLivePackage, DynamicCordisRenderFailure, DynamicCordisRunnerEnv, DynamicCordisSlotLedgerRow, EventApiEntry, InheritedApiEntry, ServiceApiEntry, ServiceApiMethod, TypeApiEntry

### extensions/cordis-host-runner — 3387 行 / 8 文件

- npm: `@deepseek-ai/dsh-cordis-host-runner`
- 层: 8
- 自述: Dynamic package definition registry, host-half sandbox lifecycle, and invoke handler table for model-mounted dual-half packages
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-session, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-typert-protocol, @deepseek-ai/dsh-util-values, @deepseek-ai/schemastery
- README: --- description: "Host half of dynamic Cordis packages for agents and maintainers choosing, composing, or debugging the registry, sandbox, and run round trip." kind: "package-reference" ---
- 类: CordisInspectRegistryService, DynamicCordisRegistry, DynamicCordisRunnerService
- 接口: Config, CordisErrorDetails, CordisHalfState, CordisInspectMethodManifest, CordisInspectProviderManifest, CordisInspectProviderView, CordisInspectQueryRequest, CordisInspectQueryResolved, CordisInspectResolveAck, CordisRunDiagnostic, DynamicCordisClientSource, DynamicCordisDefineReceipt, DynamicCordisDefineRequest, DynamicCordisDefinition, DynamicCordisInventoryPackage, DynamicCordisInventoryRow, DynamicCordisPackage, DynamicCordisPackageInspection, DynamicCordisPendingRequest, DynamicCordisPlugin, DynamicCordisPluginInspection, DynamicCordisReference, DynamicCordisRenderFailure, DynamicCordisRequestResolved, DynamicCordisResolveAck, DynamicCordisRetracted, DynamicCordisRun, DynamicCordisRunAttempt, DynamicCordisRunRequest, DynamicCordisSnapshotRow, HostCordisInspectProviderRegistration, HostCordisInspectQueryContext

### extensions/tool-cordis — 7509 行 / 8 文件

- npm: `@deepseek-ai/dsh-tool-cordis`
- 层: 9
- 自述: Self-referential cordis toolset: inspect the live runtime, mount and dispose model-written plugins
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-cordis-host-runner, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-session, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools
- README: --- description: "Model-facing Cordis runtime tools for agents and maintainers choosing, composing, or debugging dynamic-package workflows." kind: "package-reference" ---
- 接口: ApiParameter, EventApiEntry, InheritedApiEntry, ServiceApiEntry, ServiceApiMethod, TypeApiEntry

### extensions/ui-cordis — 1687 行 / 16 文件

- npm: `@deepseek-ai/dsh-client-ui-cordis`
- 层: 0
- 自述: Cordis dynamic-plugin definition card: the keyed cordis_define tool row with its run/stop switch
- 依赖: @deepseek-ai/cordis
- README: --- description: "Cordis dynamic-plugin browser surfaces for users and maintainers choosing, composing, or debugging the panel, tool cards, and @pluginId input." kind: "package-reference" ---
- 类: CordisRunCardRegistry
- 接口: CordisActionCard, CordisCardFace, CordisDefineCard, CordisDynamicPort, CordisInventory, CordisInventorySnapshot, CordisPanelFace, CordisRunCard, CordisRunCardFace, CordisRunCardPointer, CordisRunCardStore, CordisToolViewOwnerProps


## feedback

### feedback/command-feedback — 138 行 / 2 文件

- npm: `@deepseek-ai/dsh-command-feedback`
- 层: 7
- 自述: Log-only session feedback producer and human-facing slash command
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-anonymous-user-id, @deepseek-ai/dsh-commands, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-telemetry
- README: --- description: "Free-text session feedback through a `/feedback` command, for users and maintainers choosing, composing, or debugging feedback capture." kind: "package-reference" ---

### feedback/message-feedback — 647 行 / 4 文件

- npm: `@deepseek-ai/dsh-message-feedback`
- 层: 5
- 自述: Lifecycle-bound per-message rating and note sidecar for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-storage-domain, @deepseek-ai/dsh-typert-protocol, @deepseek-ai/schemastery
- README: --- description: "Per-message ratings and notes for finalized assistant messages, for users and maintainers choosing, composing, or debugging the feedback service." kind: "package-reference" ---
- 类: MessageFeedbackService
- 接口: Config, MessageFeedbackDeleteRequest, MessageFeedbackDeleteValue, MessageFeedbackItem, MessageFeedbackListRequest, MessageFeedbackListValue, MessageFeedbackNoteBlank, MessageFeedbackNoteTooLarge, MessageFeedbackPutRequest, MessageFeedbackRejected, MessageFeedbackSessionNotFound, MessageFeedbackSuccess, MessageFeedbackTargetNotFound, MessageFeedbackVersionConflict


## fs

### fs/fs — 516 行 / 3 文件

- npm: `@deepseek-ai/dsh-fs`
- 层: 5
- 自述: Abstract filesystem capability seam (ctx.fs) for the DeepSeek Harness — vocabulary types, the FileSystem service (text IO + optional version-guarded atomic mutations), and the fs/* policy event vocabulary
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-sandbox
- README: --- description: "The ctx.fs filesystem service contract for deployments choosing or mounting a filesystem backend and developers implementing one." kind: "package-reference" ---
- 类: FileSystem, FsError
- 接口: FsDirEntry, FsEditOutcome, FsEditRequest, FsInfo, FsPathInfo, FsTarget, FsWriteOutcome

### fs/fs-local — 1214 行 / 4 文件

- npm: `@deepseek-ai/dsh-fs-local`
- 层: 6
- 自述: Local-filesystem implementation of the DeepSeek Harness filesystem seam (ctx.fs)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-fs, @deepseek-ai/dsh-invariants, @deepseek-ai/schemastery
- README: --- description: "The host-filesystem backend for ctx.fs for deployments and maintainers choosing or debugging local file access." kind: "package-reference" ---
- 类: LocalFileSystem
- 接口: Config, FsIoInternals, LocalDirEntry, LocalTarget, PathInfo, PathLinkInfo

### fs/fs-observation-policy — 189 行 / 3 文件

- npm: `@deepseek-ai/dsh-fs-observation-policy`
- 层: 6
- 自述: File-context policy plugin for the DeepSeek Harness — observed-state, read-before-edit, and version-guarded write/edit added over the ctx.fs provider seam through the fs/* event gate (no service API)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-fs, @deepseek-ai/dsh-invariants
- README: --- description: "The read-before-edit filesystem policy plugin for deployments and maintainers choosing or debugging guarded write and edit behavior." kind: "package-reference" ---
- 接口: FsObservationActor

### fs/fs-sandbox — 250 行 / 3 文件

- npm: `@deepseek-ai/dsh-fs-sandbox`
- 层: 7
- 自述: Sandbox-enforcing implementation of the DeepSeek Harness filesystem seam: fences write/edit by the per-call sandbox mode (read-only denies mutation, workspace-write contains it to the workspace + temp roots) while reads pass through
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-fs, @deepseek-ai/dsh-fs-local, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-sandbox, @deepseek-ai/dsh-sandbox-policy
- README: --- description: "The sandbox-enforcing ctx.fs backend for deployments and maintainers confining model file mutations to a session workspace." kind: "package-reference" ---
- 类: SandboxedFileSystem

### fs/tool-fs — 1568 行 / 12 文件

- npm: `@deepseek-ai/dsh-tool-fs`
- 层: 8
- 自述: Model-facing filesystem tools (read, write, edit) over the DeepSeek Harness filesystem seam (ctx.fs)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-attachment, @deepseek-ai/dsh-fs, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-sandbox, @deepseek-ai/dsh-sandbox-policy, @deepseek-ai/dsh-session, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-user-approval, @deepseek-ai/schemastery
- README: --- description: "The model-facing read, read_image, write, and edit tools for users and maintainers composing or debugging filesystem access for agents." kind: "package-reference" ---
- 类: FsSandboxController
- 接口: Config, EscalationSchemaFields, FileReadOutcome, FileTextLine, FsEscalationArgs, FsReadMeta, ImageReadValue, ReadToolCaps, ReadWindow, WindowResult

### fs/tool-fs-search — 1568 行 / 7 文件

- npm: `@deepseek-ai/dsh-tool-fs-search`
- 层: 8
- 自述: Model-facing filesystem discovery tools (glob, grep) backed by the packaged ripgrep binary (@vscode/ripgrep)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-output-retention, @deepseek-ai/dsh-session, @deepseek-ai/dsh-spill, @deepseek-ai/dsh-subprocess, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-timeout, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: --- description: "The model-facing glob and grep discovery tools for users and maintainers composing or debugging workspace search for agents." kind: "package-reference" ---
- 类: SearchError
- 接口: Config, GlobInput, GlobSample, GlobToolCaps, GrepInput, GrepMatch, GrepToolCaps, RipgrepRun

### fs/tool-str-replace-editor — 561 行 / 2 文件

- npm: `@deepseek-ai/dsh-tool-str-replace-editor`
- 层: 8
- 自述: Model-facing view, create, literal replace, and line insert tool over the Harness filesystem service
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-fs, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-sandbox, @deepseek-ai/dsh-sandbox-policy, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: --- description: "The standalone str_replace_editor tool over ctx.fs for users and maintainers composing Claude-Code-style file editing for agents." kind: "package-reference" ---
- 接口: Config


## goal

### goal/command-goal — 226 行 / 2 文件

- npm: `@deepseek-ai/dsh-command-goal`
- 层: 7
- 自述: Human-facing slash command for persisted same-session goals
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-commands, @deepseek-ai/dsh-goal, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm
- README: --- description: "The human-facing /goal slash command for users and maintainers choosing, composing, or debugging goal control in UI command planes." kind: "package-reference" ---

### goal/goal — 1358 行 / 8 文件

- npm: `@deepseek-ai/dsh-goal`
- 层: 6
- 自述: Event-sourced same-session goal state and lifecycle service for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-typert-protocol, @deepseek-ai/schemastery
- README: --- description: "The persisted same-session goal service for users and maintainers choosing, configuring, or debugging one durable completion objective per session." kind: "package-reference" ---
- 类: GoalError, GoalService
- 接口: Config, CreateGoalRequest, CreateGoalResult, EditGoalRequest, FoldedGoal, GoalBlockReason, GoalChanged, GoalClearChangeMeta, GoalFoldState, GoalMessageSource, GoalProjection, GoalProjectionState, GoalRef, GoalSnapshot, GoalSnapshotChangeMeta, GoalView, ResolvedConfig

### goal/goal-round-driver — 580 行 / 4 文件

- npm: `@deepseek-ai/dsh-goal-round-driver`
- 层: 7
- 自述: Race-fenced same-session goal-round driver
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-goal, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session
- README: --- description: "The same-session continuation driver for users and maintainers choosing, composing, or debugging automatic goal rounds." kind: "package-reference" ---

### goal/tool-goal — 525 行 / 4 文件

- npm: `@deepseek-ai/dsh-tool-goal`
- 层: 8
- 自述: Model-facing same-session goal tools with execution-time authority checks
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-goal, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: --- description: "The model-facing goal tools for users and maintainers choosing, composing, or debugging get_goal, create_goal, and update_goal." kind: "package-reference" ---
- 接口: Config, GoalToolExecution


## guard

### guard/repeat-tool-reminder — 263 行 / 2 文件

- npm: `@deepseek-ai/dsh-repeat-tool-reminder`
- 层: 8
- 自述: Repeat-tool-call guard plugin: advisory reminders when an agent loops on identical tool calls
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: --- description: "Advisory loop-hygiene guard that nudges the model out of identical tool-call loops, for users and maintainers choosing, configuring, or debugging the plugin." kind: "package-reference" ---
- 接口: Config

### guard/timeout-policy — 111 行 / 2 文件

- npm: `@deepseek-ai/dsh-tool-call-timeout-policy`
- 层: 8
- 自述: Tool-call timeout policy: a tools/execute wrapper that arms a per-tool deadline on exec.signal and returns TOOL_TIMEOUT when it wins
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-timeout, @deepseek-ai/dsh-tools
- README: --- description: "Cooperative time limit for cancellation-aware tool calls, mapping a settled timeout to a clear model error for users and maintainers choosing or debugging the plugin." kind: "package-reference" ---


## hooks

### hooks/hook-protocol — 854 行 / 9 文件

- npm: `@deepseek-ai/dsh-hook-protocol`
- 层: 6
- 自述: Shared Claude Code / Codex hook wire protocol: matcher engine, stdin/exit-code/stdout codec, multi-hook merge, and hook/* session events
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-shell
- README: --- description: "The shared hook rules behind the Claude Code and Codex bridges — what a hook can do and what happens when it runs — for users and maintainers of the hooks subsystem." kind: "package-library" ---
- 接口: CommandHook, DetachedRuns, HookInvocation, HookOutput, HookResultRecord, MatcherGroup, MergedHookOutcome, RunHookOptions, RunHookResult

### hooks/hooks-claude-code — 514 行 / 3 文件

- npm: `@deepseek-ai/dsh-hooks-claude-code`
- 层: 11
- 自述: Bridge plugin: run a Claude Code hooks.json / settings hook config on the DeepSeek Harness interception seams
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-hook-protocol, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: --- description: "Run your existing Claude Code hooks.json or settings hook config during agent runs — block prompts and tools, attach context, or force continuation — for users and maintainers of the bridge." kind: "package-reference" ---
- 接口: Config, ParsedClaudeConfig, SkippedHook, SubstitutionVars

### hooks/hooks-codex — 445 行 / 3 文件

- npm: `@deepseek-ai/dsh-hooks-codex`
- 层: 8
- 自述: Bridge plugin: run a Codex hooks.json hook config on the DeepSeek Harness interception seams
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-hook-protocol, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: --- description: "Run your existing Codex hooks.json hook config during agent runs — block prompts and tools, attach context, or force continuation — for users and maintainers of the bridge." kind: "package-reference" ---
- 接口: Config, ParsedCodexConfig, SkippedHook


## host

### host/directory-picker — 179 行 / 3 文件

- npm: `@deepseek-ai/dsh-host-directory-picker`
- 层: 1
- 自述: Abstract workspace-directory picking seam (ctx.directoryPicker) for the DeepSeek Harness web GUI host
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: --- description: "Workspace-directory picking seam for the web GUI host: the service contract, capability vocabulary, and error codes the native and browse backends implement." kind: "package-reference" ---
- 类: DirectoryPicker, DirectoryPickerError
- 接口: DirectoryEntry, DirectoryListing, DirectoryPickerBrowseCapability, DirectoryPickerCapabilities, DirectoryPickerNativeCapability

### host/directory-picker-auto — 219 行 / 4 文件

- npm: `@deepseek-ai/dsh-host-directory-picker-auto`
- 层: 3
- 自述: Adaptive chooser of the directory-picker seam: resolves the host situation at boot and mounts the native or browse backend for the DeepSeek Harness web GUI host
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-loader, @deepseek-ai/dsh-client-ui-directory-picker-browse, @deepseek-ai/dsh-client-ui-directory-picker-native, @deepseek-ai/dsh-host-directory-picker-browse, @deepseek-ai/dsh-host-directory-picker-native, @deepseek-ai/dsh-host-webserver, @deepseek-ai/dsh-invariants
- README: --- description: "Adaptive chooser of the directory-picker seam: resolves the web GUI host's situation once at boot and mounts the matching native or browse backend." kind: "package-reference" ---
- 接口: DirectoryPickerHostFacts

### host/directory-picker-browse — 364 行 / 3 文件

- npm: `@deepseek-ai/dsh-host-directory-picker-browse`
- 层: 2
- 自述: In-app browsing backend of the directory-picker seam (listing/creation primitives over the host filesystem)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-host-directory-picker, @deepseek-ai/dsh-invariants, @deepseek-ai/schemastery
- README: --- description: "In-app browsing backend of the directory-picker seam: one-level directory listing and child-directory creation for the web GUI host, serving remote clients too." kind: "package-reference" ---
- 类: BrowseDirectoryPicker
- 接口: Config, ListingCandidate

### host/directory-picker-native — 770 行 / 9 文件

- npm: `@deepseek-ai/dsh-host-directory-picker-native`
- 层: 2
- 自述: Native-OS-chooser backend of the directory-picker seam for the DeepSeek Harness web GUI host
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-host-directory-picker, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-native-command
- README: --- description: "Native-OS-chooser backend of the directory-picker seam: opens one platform chooser per pick for operators sitting at the web GUI host's display." kind: "package-reference" ---
- 类: NativeDirectoryPicker
- 接口: DirectoryPickerInternals, Win32DialogBindings, Win32DialogInternals, Win32DialogWorkerData, Win32DialogWorkerLike, Win32FolderDialog

### host/frontend-static — 177 行 / 2 文件

- npm: `@deepseek-ai/dsh-host-frontend-static`
- 层: 4
- 自述: SPA dist server for the Web shell: owns the webserver fallback seat, serving explicit index entries and static assets with traversal rejection and 404 misses
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-client-connection, @deepseek-ai/dsh-host-webserver, @deepseek-ai/dsh-invariants, @deepseek-ai/schemastery
- README: --- description: "SPA dist server for the Web shell: claims the webserver fallback seat and serves the built frontend with traversal rejection and SPA index fallback." kind: "package-reference" ---
- 接口: Config

### host/plugin-inventory — 182 行 / 3 文件

- npm: `@deepseek-ai/dsh-host-plugin-inventory`
- 层: 9
- 自述: Read-only Remote projection of current Cordis Loader plugin state
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-loader, @deepseek-ai/dsh-agent-presets, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-typert-protocol
- README: --- description: "Read-only projection of the current Cordis Loader plugin state with each agent preset's composition beside it: the pluginInventory service and its pluginInventory/list Remote for web GUI host clients." kind: "package-reference" ---
- 类: PluginInventoryGateway
- 接口: AgentPresetPluginGroup, AgentPresetPluginRow, PluginInventoryEntry, PluginInventorySnapshot

### host/webserver — 542 行 / 3 文件

- npm: `@deepseek-ai/dsh-host-webserver`
- 层: 1
- 自述: Web route-registration plugin: HTTP and upgrade routes, index transform taps, and static dist fallback; knows no harness concepts
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/schemastery
- README: --- description: "The web GUI host's HTTP server: named-route and upgrade registration, index transforms, and the single fallback seat that serves the Web shell's SPA dist." kind: "package-reference" ---
- 类: WebServer
- 接口: Config, WebRoute, WebUpgradeRoute


## identity

### identity/anonymous-user-id — 131 行 / 2 文件

- npm: `@deepseek-ai/dsh-anonymous-user-id`
- 层: 2
- 自述: Shared anonymous user identity for DeepSeek Harness telemetry and feedback correlation
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-home-paths, @deepseek-ai/dsh-invariants
- README: --- description: "Anonymous per-harness-home identity for users and maintainers tracing how telemetry, feedback acknowledgement, and DeepSeek provider requests correlate records." kind: "package-library" ---
- 接口: AnonymousUserIdOptions


## interaction

### interaction/commands — 662 行 / 4 文件

- npm: `@deepseek-ai/dsh-commands`
- 层: 6
- 自述: Plugin-owned human command registry for DeepSeek Harness UIs
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-attachment, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-session, @deepseek-ai/dsh-typert-protocol, @deepseek-ai/dsh-util-crypto
- README: --- description: "Human slash-command registry for interactive UIs: plugin-owned commands that run directly against an agent without creating a model message, for users and maintainers composing or extending command surfaces." kind: "package-reference" ---
- 类: CommandRuntime
- 接口: CommandDefinition, CommandDescriptor, CommandExecution, CommandInputDescriptor, CommandInvocation, CommandSourceMap, ParsedCommand

### interaction/permission-presets — 525 行 / 4 文件

- npm: `@deepseek-ai/dsh-permission-presets`
- 层: 7
- 自述: User-facing permission presets (ctx.permissionPresets) for the DeepSeek Harness: one product-level Permissions select bundling the sandbox-mode and approval-policy knobs, written through to their own session events
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-commands, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-sandbox, @deepseek-ai/dsh-sandbox-policy, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-settings, @deepseek-ai/dsh-shell, @deepseek-ai/dsh-user-approval, @deepseek-ai/schemastery
- README: --- description: "User-facing permission presets for users and maintainers choosing, configuring, or debugging the Permissions selector that bundles sandbox mode with an approval policy." kind: "package-reference" ---
- 类: PermissionPresetService
- 接口: Config, KnobState, PermissionSelect, PermissionSettings, PresetOption, PresetSpec

### interaction/tool-ask-user — 131 行 / 2 文件

- npm: `@deepseek-ai/dsh-tool-ask-user`
- 层: 8
- 自述: Model-facing ask_user_question tool over the ctx.userQuestions seam
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-user-questions
- README: --- description: "The model-facing ask_user_question tool over the user-questions seam, for users and maintainers composing or debugging interactive agent surfaces." kind: "package-reference" ---

### interaction/user-approval — 544 行 / 4 文件

- npm: `@deepseek-ai/dsh-user-approval`
- 层: 6
- 自述: User-approval seam (ctx.approval) for the DeepSeek Harness: one-shot permission decisions dispatched to composed answerers over the approval/request waterfall, fail-closed by default
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-session, @deepseek-ai/dsh-system-prompt, @deepseek-ai/schemastery
- README: --- description: "Channel-neutral one-shot approval seam for users and maintainers composing answerers, setting policy, or debugging fail-closed permission decisions." kind: "package-reference" ---
- 类: ApprovalService
- 接口: ApprovalRequest, ApprovalRequestEvent, Config

### interaction/user-questions — 275 行 / 3 文件

- npm: `@deepseek-ai/dsh-user-questions`
- 层: 6
- 自述: Abstract user-questions seam (ctx.userQuestions) for asking the human during agent runs
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-scope
- README: --- description: "Waterfall-based question and answer service for tools, permission plugins, local answerers, and Agent-scoped Web interactions." kind: "package-reference" ---
- 类: UserQuestionError, UserQuestionService
- 接口: AskUserQuestionAnswer, AskUserQuestionAnswerItem, AskUserQuestionItem, AskUserQuestionOption, AskUserQuestionRequest, AskUserQuestionRequestEvent


## jobs

### jobs/jobs — 424 行 / 4 文件

- npm: `@deepseek-ai/dsh-jobs`
- 层: 6
- 自述: Background job registry (ctx.jobs) for the DeepSeek Harness — shared ids, owner isolation, polling, cancellation, and completion listeners for long-running tool work
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session
- README: --- description: "The background-job registry contract for users and maintainers composing, implementing, or debugging background work: ids, ownership, lifecycle, and completion listeners." kind: "package-reference" ---
- 类: JobRegistry
- 接口: JobHooks, JobKindMap, JobOutcome, JobRead, JobSnapshot, JobStart

### jobs/jobs-local — 567 行 / 2 文件

- npm: `@deepseek-ai/dsh-jobs-local`
- 层: 7
- 自述: Process-local implementation of the DeepSeek Harness background job registry seam
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-jobs, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-timeout, @deepseek-ai/schemastery
- README: --- description: "The process-local background-job registry for users and maintainers composing, sizing, or debugging in-process jobs: per-owner admission, lifecycle, and teardown." kind: "package-reference" ---
- 类: LocalJobRegistry
- 接口: Config

### jobs/tool-jobs — 431 行 / 2 文件

- npm: `@deepseek-ai/dsh-tool-jobs`
- 层: 8
- 自述: Model-facing background job control tools (job_output, job_list, job_kill) over the ctx.jobs registry
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-jobs, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-output-retention, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: --- description: "The model-facing background-job controls for users and maintainers choosing, configuring, or debugging job_output, job_list, job_kill, and completion notices." kind: "package-reference" ---
- 接口: Config, PublicJobSnapshot


## llm

### llm/deepseek-llm-api-extensions — 218 行 / 3 文件

- npm: `@deepseek-ai/dsh-deepseek-llm-api-extensions`
- 层: 1
- 自述: Additive request-field registry for the official DeepSeek LLM API adapter
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: --- description: "Official DeepSeek request-extension registry for provider plugins contributing lifecycle-owned top-level API fields." kind: "package-reference" ---
- 类: DeepSeekLlmApiExtensionRegistry
- 接口: DeepSeekLlmApiExtensionMap, DeepSeekLlmApiExtensionProvider, DeepSeekLlmApiExtensionRequest, PreparedDeepSeekLlmApiExtension, PreparedDeepSeekLlmApiExtensions

### llm/llm — 3086 行 / 13 文件

- npm: `@deepseek-ai/dsh-llm`
- 层: 2
- 自述: Provider-neutral LLM service interface for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-timeout, @deepseek-ai/dsh-typert-protocol, @deepseek-ai/dsh-util-crypto, @deepseek-ai/dsh-util-values, @deepseek-ai/schemastery
- README: --- description: "The provider-neutral model-call service for users and maintainers streaming requests, registering provider adapters, or resolving model metadata." kind: "package-reference" ---
- 类: BlockAssembler, HarnessError, LlmAdapter, LlmError, LlmRuntime
- 接口: AdapterRegistrationHandle, AlwaysRetryPolicyConfig, AppIdentity, AssistantMessage, AssistantProvenance, BackoffConfig, ContentBlockMap, ContextSnapshotSection, DirectoryRegistrationHandle, FinishReasonMap, GenerateOptions, ImageAttachmentAccess, ImageBlock, LlmCallConfig, LlmCallConfigAdapterDefaults, LlmConfigurableProvider, LlmDiscoveredModel, LlmErrorOptions, LlmFailure, LlmImageRequestPrice, LlmImageRequestPricing, LlmModelContext, LlmModelDiscoveryOperation, LlmModelDiscoveryRequest, LlmModelInfo, LlmModelReasoningInfo, LlmProviderInfo, LlmReasoningEffortInfo, LlmResolvedModelInfo, Message, MessageSourceMap, ModelMessageSource, ModelModalityMap, NormalRetryPolicyConfig, PreparedAdapterCall, PreparedLlmCall, ReasoningBlock, ReplayEnvelope, RequestImageOffloadPolicy, ResolvedAlwaysRetryPolicy, ResolvedNormalRetryPolicy, ResolvedRetryBackoff, TextBlock, TokenUsage, ToolCallBlock, ToolMessageSource, ToolResultBlock, ToolResultMessage, ToolResultMessageInput, ToolSchema, UserMessage

### llm/llm-deepseek — 3180 行 / 13 文件

- npm: `@deepseek-ai/dsh-llm-deepseek`
- 层: 6
- 自述: DeepSeek chat-completions adapter for the DeepSeek Harness LLM seam
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-anonymous-user-id, @deepseek-ai/dsh-atomic-write, @deepseek-ai/dsh-attachment, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-credentials, @deepseek-ai/dsh-deepseek-llm-api-extensions, @deepseek-ai/dsh-fs, @deepseek-ai/dsh-home-paths, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-launch-environment, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-settings, @deepseek-ai/dsh-timeout, @deepseek-ai/dsh-util-values, @deepseek-ai/schemastery
- README: --- description: "The DeepSeek chat-completions adapter for users and maintainers configuring the deepseek-official route, thinking, and image input." kind: "package-reference" ---
- 类: DeepSeekAdapter, DeepSeekFileStore, DeepSeekFilesClient, DeepSeekFilesError, DeepSeekUploadIndex
- 接口: Config, DeepSeekAdapterOptions, DeepSeekCatalogModel, DeepSeekConnectionOptions, DeepSeekFileConnection, DeepSeekFileObject, DeepSeekFilePage, DeepSeekFilePolicy, DeepSeekFileReference, DeepSeekUploadRecord, ImageSerializationOptions, ImageWireLocation, RequestDefaults, UploadIndexCommit, WireAssistantMessage, WireChoice, WireChunk, WireDelta, WireError, WireFileContentPart, WireImageUrlContentPart, WireRequest, WireSystemMessage, WireTextContentPart, WireTool, WireToolCall, WireToolCallDelta, WireToolMessage, WireUsage, WireUserMessage

### llm/llm-pi-ai — 3817 行 / 12 文件

- npm: `@deepseek-ai/dsh-llm-pi-ai`
- 层: 6
- 自述: pi-ai-backed DeepSeek adapter for the DeepSeek Harness LLM seam (design-verification twin of dsh-llm-deepseek)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-attachment, @deepseek-ai/dsh-authorization, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-credentials, @deepseek-ai/dsh-fs, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-launch-environment, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-settings, @deepseek-ai/dsh-timeout, @deepseek-ai/dsh-util-values, @deepseek-ai/schemastery
- README: --- description: "The pi-ai-backed multi-provider adapter for users and maintainers routing the harness LLM service through pi-ai catalogs and hand-declared gateways." kind: "package-reference" ---
- 类: PiAiAdapter
- 接口: Config, PiAiAdapterOptions, PiAiAuthInjection, PiAiCompatProfile, PiAiModelProfile, PiAiProviderProfile, PiAiReplayResponse, PiImageRequestContext, ProviderSpec, ResolvedPiAiProviderProfile, RouteCatalog, RouteCatalogRequest

### llm/llm-retry — 552 行 / 6 文件

- npm: `@deepseek-ai/dsh-llm-retry`
- 层: 6
- 自述: Provider-routed LLM request retry policy for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-timeout, @deepseek-ai/schemastery
- README: --- description: "The retry executor for users and maintainers configuring provider-routed model-request recovery at durable agent-step boundaries." kind: "package-reference" ---
- 接口: LlmRetryStartedEventData, RetryInternals

### llm/plugin-package-inventory-deepseek — 245 行 / 3 文件

- npm: `@deepseek-ai/dsh-plugin-package-inventory-deepseek`
- 层: 9
- 自述: Active Loader-backed plugin package inventory for official DeepSeek LLM API requests
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-loader, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-agent-presets, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-deepseek-llm-api-extensions, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/schemastery
- README: --- description: "Active Loader package inventory metadata for deployments sending official DeepSeek requests." kind: "package-reference" ---
- 接口: Config, DeepSeekPluginPackageIdentity, DeepSeekPluginPackageInventoryExtension

### llm/token-meter — 1478 行 / 12 文件

- npm: `@deepseek-ai/dsh-token-meter`
- 层: 8
- 自述: Replay-aware token measurement service (ctx.tokenMeter) for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-compaction, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-llm-retry, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-util-values, @deepseek-ai/schemastery
- README: --- description: "Replay-aware token and context-pressure measurement for users and maintainers sizing prompts or building compaction and occupancy displays." kind: "package-reference" ---
- 类: TokenMeter
- 接口: ContextBreakdownProjection, ContextPressureProjection, MeterSurfaceNode, PricedSurface, ShadowPriceClaim, SurfaceTokenPlan, SurfaceTokensFold, TokenMeasurement, TokenSurfaceNode, TokenUsageProjection, TurnTokenUsage, TurnTokenUsageRoute


## lsp

### lsp/lsp — 339 行 / 4 文件

- npm: `@deepseek-ai/dsh-lsp`
- 层: 3
- 自述: Abstract LSP capability seam (ctx.lsp) for the DeepSeek Harness — language-server provider registry keyed by branded id and extension mapping, order-independent per-query selection, normalized definition/references/implementation/hover requests and results, and the LspError taxonomy
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm
- README: --- description: "The LSP capability seam (ctx.lsp): provider selection by file extension, four normalized code-navigation operations, and structured errors, for users and maintainers composing or extending code navigation." kind: "package-reference" ---
- 类: Lsp, LspError
- 接口: LspHover, LspLocation, LspPosition, LspProvider, LspProviderQuery, LspQueryRequest, LspRange, LspService

### lsp/lsp-stdio — 1664 行 / 9 文件

- npm: `@deepseek-ai/dsh-lsp-stdio`
- 层: 6
- 自述: Generic stdio language-server provider for the DeepSeek Harness LSP capability seam (ctx.lsp) — spawns configured servers, translates JSON-RPC, and serves transient-open goToDefinition/findReferences/goToImplementation/hover queries in the host filesystem namespace
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-fs, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-lsp, @deepseek-ai/dsh-subprocess, @deepseek-ai/dsh-timeout, @deepseek-ai/dsh-util-values, @deepseek-ai/schemastery
- README: --- description: "The stdio language-server provider for ctx.lsp: configured server commands, extension mappings, and bounded transient-open queries, for users and maintainers composing local code navigation." kind: "package-reference" ---
- 类: LspConnection, LspInstance, MessageDecoder
- 接口: Config, ConnectionSpec, HostSource, HostWorkspace, InstanceSpec, LspLocalServerConfig, WireHover, WireInitializeResult, WireLocation, WireLocationLink, WireMarkedStringObject, WireMarkupContent, WirePosition, WireRange, WireServerCapabilities, WireTextDocumentSyncOptions

### lsp/tool-lsp — 484 行 / 4 文件

- npm: `@deepseek-ai/dsh-tool-lsp`
- 层: 8
- 自述: Model-facing lsp tool over the DeepSeek Harness LSP capability seam (ctx.lsp) — one read-only tool with goToDefinition/findReferences/goToImplementation/hover operations, one-based UTF-16 cursor coordinates, bounded location rendering, and hover normalization
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-lsp, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-timeout, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-util-values, @deepseek-ai/schemastery
- README: --- description: "The model-facing lsp tool: four read-only code-navigation operations with one-based UTF-16 cursor coordinates, bounded results, and hover text, for users and maintainers composing model code navigation." kind: "package-reference" ---
- 接口: Config, LspToolArgs, LspToolInput


## mcp

### mcp/mcp-client — 1179 行 / 5 文件

- npm: `@deepseek-ai/dsh-mcp-client`
- 层: 8
- 自述: MCP client bridge: connects to MCP servers and registers their tools on ctx.tools
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-attachment, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-subprocess, @deepseek-ai/dsh-timeout, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: --- description: "MCP client bridge for deployments and maintainers choosing, configuring, or debugging connections to external MCP servers whose tools register on ctx.tools." kind: "package-reference" ---
- 接口: ConnectionHandle, ConnectionOutcome, ReconnectConfig, StdioConfig, StreamableHttpConfig, ToolBridgeOptions


## plan

### plan/plan-mode — 569 行 / 4 文件

- npm: `@deepseek-ai/dsh-plan-mode`
- 层: 8
- 自述: Logged per-agent plan mode with deployment guidance, a direct slash command, and a user-reviewed exit
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-commands, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-user-questions
- README: --- description: "Plan mode for users and maintainers choosing, configuring, or debugging the per-agent planning feature with deployment guidance, a /plan command, and a user-reviewed exit." kind: "package-reference" ---
- 类: PlanModeController
- 接口: PlanModeConfig, PlanProjection, PlanUnitState


## preset

### preset/agent-presets — 2495 行 / 12 文件

- npm: `@deepseek-ai/dsh-agent-presets`
- 层: 8
- 自述: Per-session agent composition from preset cordis.yml files for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-include, @deepseek-ai/cordis-plugin-loader, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-atomic-write, @deepseek-ai/dsh-home-paths, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-settings, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-typert-protocol, @deepseek-ai/schemastery
- README: --- description: "Per-session agent composition from preset cordis.yml files, for users and maintainers choosing, configuring, or debugging agent presets." kind: "package-reference" ---
- 类: AgentPresets
- 接口: AgentPreset, AgentPresetComposition, AgentPresetCompositionRow, AgentPresetDocument, AgentPresetRoster, AgentPresetRow, AgentPresetSettings, Config, PresetDisplaySource, PresetDisplayText, PresetMetadata, PresetMount, PresetRoot

### preset/persona — 95 行 / 2 文件

- npm: `@deepseek-ai/dsh-persona`
- 层: 4
- 自述: Composition-authored deployment persona section for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-system-prompt, @deepseek-ai/schemastery
- README: --- description: "The composable persona row presets mount to give one agent its own system-prompt persona, for users and maintainers configuring or debugging it." kind: "package-reference" ---
- 接口: Config


## runtime-diagnostics

### runtime-diagnostics/invariants — 230 行 / 2 文件

- npm: `@deepseek-ai/dsh-invariants`
- 层: 0
- 自述: Registry service for package-owned DeepSeek Harness runtime invariants
- 依赖: @deepseek-ai/cordis, @deepseek-ai/schemastery
- README: --- description: "Runtime invariant checks for live compositions: the registry service that runs package-owned checks, for users and maintainers choosing, configuring, or debugging them." kind: "package-reference" ---
- 类: InvariantError, InvariantRegistry
- 接口: Config, InvariantInstaller


## sandbox

### sandbox/sandbox — 452 行 / 4 文件

- npm: `@deepseek-ai/dsh-sandbox`
- 层: 4
- 自述: Abstract process-sandbox seam (ctx.sandbox) for the DeepSeek Harness: same-world confinement vocabulary and the SandboxProvider contract
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-util-values
- README: --- description: "The process-sandbox service contract for users and maintainers composing, using, or extending same-world subprocess confinement." kind: "package-reference" ---
- 类: SandboxProvider, SandboxUnavailableError
- 接口: ConfinedArgv, EscalationApproval, EscalationApprover, EscalationRequest, RunnerFailureRule, SandboxExecutionPolicy, SandboxPolicy

### sandbox/sandbox-local — 655 行 / 3 文件

- npm: `@deepseek-ai/dsh-sandbox-local`
- 层: 5
- 自述: Local process-sandbox backends for the DeepSeek Harness sandbox seam: bwrap, the npm-distributed landlock-run launcher, macOS Seatbelt, or the Windows ACL restricted-token runner — functionally probed, fail-closed
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-sandbox, @deepseek-ai/dsh-sandbox-windows-acl, @deepseek-ai/dsh-session, @deepseek-ai/dsh-util-values, @deepseek-ai/node-addon-landlock-run, @deepseek-ai/schemastery
- README: --- description: "Local per-platform sandbox backends for users and maintainers choosing, configuring, or debugging process confinement on Linux, macOS, or Windows." kind: "package-reference" ---
- 类: LocalSandboxProvider
- 接口: Config, SandboxInternals

### sandbox/sandbox-policy — 304 行 / 4 文件

- npm: `@deepseek-ai/dsh-sandbox-policy`
- 层: 6
- 自述: Per-call sandbox policy resolver and current model context: deployment fallbacks plus each session's mode and workspace root, shared by every enforcing capability family
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-sandbox, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-system-prompt, @deepseek-ai/schemastery
- README: --- description: "The shared per-call sandbox policy resolver and current model context for users and maintainers composing, configuring, or debugging file-effect policy across enforcing capabilities." kind: "package-reference" ---
- 类: SandboxPolicyService
- 接口: Config, SandboxPolicyRequest

### sandbox/sandbox-windows-acl — 1853 行 / 12 文件

- npm: `@deepseek-ai/dsh-sandbox-windows-acl`
- 层: 2
- 自述: Windows ACL write-restriction sandbox backend (restricted-token spawn with capability-SID write allowlist) for the DeepSeek Harness sandbox seam
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-win32-process
- README: --- description: "The Windows write-restriction sandbox backend for users and maintainers choosing, configuring, or debugging restricted-token process confinement on Windows." kind: "package-library" ---
- 类: AclSandbox, AclWriteGrant
- 接口: AclSandboxChild, AclSandboxChildResult, AclSandboxOptions, AclSandboxSpawnOptions, RestrictingSidSet, SpawnedInherited, SpawnedNative, Win32Bindings


## schedule

### schedule/schedule — 2157 行 / 11 文件

- npm: `@deepseek-ai/dsh-schedule`
- 层: 8
- 自述: Agent-scoped durable after, at, and fixed-rate reminders over the session event log
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-tools
- README: --- description: "Session-local durable reminders: the schedule_create, schedule_list, and schedule_delete tools and live-owner delivery, for users and maintainers choosing, configuring, or debugging the package." kind: "package-reference" ---
- 类: ScheduleInputError, ScheduleLogError, SchedulePersistenceError, ScheduleRuntime
- 接口: AfterScheduleRecord, AtScheduleRecord, CorruptScheduleLogError, EveryOccurrence, EveryScheduleDispatchChange, EveryScheduleRecord, FoldedSchedules, FrequencyTooHighError, InternalScheduleError, InvalidPromptError, InvalidRuleError, InvalidSelectorError, InvalidTimeZoneError, LocalAtInput, NotFutureError, OneShotScheduleDispatchChange, PersistenceUncertainError, ScheduleCreateChange, ScheduleDeleteChange, ScheduleProjectionState, TimeOutOfRangeError


## sdk

### sdk/client — 1201 行 / 7 文件

- npm: `@deepseek-ai/dsh-sdk-client`
- 层: 12
- 自述: TypeScript client SDK for driving a DeepSeek Harness runtime subprocess over stdio JSON-RPC: the DeepSeekHarness high-level turns API and the lower-level HarnessClient
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-sdk-protocol, @deepseek-ai/dsh-session
- README: --- description: "The TypeScript SDK client for callers that spawn a DeepSeek Harness runtime subprocess and drive agent turns over stdio JSON-RPC: the DeepSeekHarness run API and the lower-level HarnessClient." kind: "package-library" ---
- 类: DeepSeekHarness, HarnessClient, HarnessSession, RequestTimeoutError, SdkProtocolError, TransportClosedError
- 接口: DeepSeekHarnessOptions, DshNodeLaunch, HarnessClientOptions, HarnessNotification, NotificationSubscription, RunOptions, RunResult, RuntimeProcessOptions

### sdk/protocol — 456 行 / 4 文件

- npm: `@deepseek-ai/dsh-sdk-protocol`
- 层: 11
- 自述: Shared wire protocol for the DeepSeek Harness SDK runtime: the newline-delimited JSON-RPC stdio transport and the named request, result, and notification types spoken between the runtime server and SDK clients
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-subagent
- README: --- description: "The SDK wire protocol for client and server implementers: the newline-delimited JSON-RPC transport and the named request, result, and notification types spoken between a Harness runtime and its SDK clients." kind: "package-library" ---
- 类: JsonRpcLineTransport, JsonRpcResponseError
- 接口: HarnessSdkNotificationMap, HarnessSdkRequestMap, InitializeParams, InitializeResult, JsonRpcTransportPeer, SdkEncodedImageBlock, SessionEventNotification, SessionPromptParams, SessionPromptResult, SessionStatusNotification, SubagentFinishedNotification, SubagentStartedNotification

### sdk/server — 429 行 / 3 文件

- npm: `@deepseek-ai/dsh-sdk-jsonrpc-server`
- 层: 12
- 自述: Stdio JSON-RPC server plugin for out-of-process DeepSeek Harness SDK clients
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-attachment, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-llm-deepseek, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-sdk-protocol, @deepseek-ai/dsh-session, @deepseek-ai/dsh-subagent, @deepseek-ai/schemastery
- README: --- description: "The stdio JSON-RPC serving plugin for deployments that let out-of-process SDK clients open sessions and drive agents in a DeepSeek Harness runtime." kind: "package-reference" ---
- 类: HarnessSdkJsonRpcServer
- 接口: HarnessSdkJsonRpcServerOptions, JsonRpcConfig


## session

### session/session-checkpoint-policy — 113 行 / 2 文件

- npm: `@deepseek-ai/dsh-session-checkpoint-policy`
- 层: 8
- 自述: Semantic session durability checkpoints before model requests and tool side effects
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-tools
- README: --- description: "Semantic session durability checkpoints for users and maintainers deploying persisted agents that must not lose a model request or tool side effect on crash." kind: "package-reference" ---

### session/session-log-deepseek — 191 行 / 3 文件

- npm: `@deepseek-ai/dsh-session-log-deepseek`
- 层: 4
- 自述: Incremental lossless session-log request extension for the official DeepSeek LLM API
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-deepseek-llm-api-extensions, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/schemastery
- README: --- description: "Incremental canonical session-log upload for deployments enabling official DeepSeek request metadata." kind: "package-reference" ---
- 接口: Config, DeepSeekSessionLogExtension

### session/session-persistence — 2348 行 / 7 文件

- npm: `@deepseek-ai/dsh-session-persistence`
- 层: 4
- 自述: Abstract durable session persistence seam (ctx.sessionPersistence) for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-timeout, @deepseek-ai/dsh-util-values
- README: --- description: "The durable session-storage seam for users and maintainers choosing a persistence backend, resuming sessions, or building a backend against the shared service contract." kind: "package-reference" ---
- 类: PersistenceCoordinator, SessionFormatUnsupportedError, SessionPersistence, SessionPersistenceCorruptionError, SessionPersistenceNotFoundError, SessionPreparations, SessionWriteBehind
- 接口: PersistenceBackend, PersistenceCoordinatorOptions, PreparationLease, SessionInspection, SessionLocation, SessionPersistenceSnapshot, SessionPreparationReservation, SessionRawArtifact, SessionWriteBehindOptions, StoredPrefix, StoredSuffix

### session/session-persistence-jsonl — 1994 行 / 7 文件

- npm: `@deepseek-ai/dsh-session-persistence-jsonl`
- 层: 5
- 自述: JSONL durable session persistence backend for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/schemastery
- README: --- description: "The shipped JSONL session-persistence backend for deployments and maintainers choosing, configuring, or debugging per-session durable logs with optional Zstandard compression." kind: "package-reference" ---
- 类: JsonlSessionPersistence, NodePrivateZstdFrameDecoder, PublicZstdFrameDecoder, SessionLogScanner
- 接口: Config, HeaderLine, ZstdFrameDecoder, ZstdFrameRange, ZstdFrameScan

### session/session-projection — 734 行 / 3 文件

- npm: `@deepseek-ai/dsh-session-projection`
- 层: 4
- 自述: Session-projection seam: the merge-extensible projection type table, the provider contract, and the ctx.sessionProjections registry serving whole current values of log-derived per-session state
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session
- README: --- description: "The session-projection registry for developers serving whole current values of log-derived per-session state to client carriers, and for maintainers of the drive contract." kind: "package-reference" ---
- 类: SessionProjectionRegistry
- 接口: ProjectionCheckpointRow, ProjectionDefinition, ProjectionSnapshot, SessionProjectionMap, SessionProjectionStateMap

### session/session-projection-cache — 432 行 / 3 文件

- npm: `@deepseek-ai/dsh-session-projection-cache`
- 层: 5
- 自述: Persisted projection cache (ctx.sessionProjectionCache): durable per-session checkpoint records on the session_projcache storage domain (per-record layout), throttled write-behind, and the cached listing read
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-storage-domain, @deepseek-ai/dsh-util-values, @deepseek-ai/schemastery
- README: --- description: "The persisted session-projection cache for deployments and maintainers choosing, configuring, or debugging durable checkpoints, zero-I/O list reads, and accelerated cold projection folds." kind: "package-reference" ---
- 类: SessionProjectionCache
- 接口: Config

### session/session-stats — 346 行 / 5 文件

- npm: `@deepseek-ai/dsh-session-stats`
- 层: 5
- 自述: Whole-log conversation counts and wall times projection (sessionStats) for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection
- README: --- description: "Whole-log conversation counts and wall times for clients and maintainers choosing, composing, or debugging the sessionStats projection unit." kind: "package-reference" ---
- 接口: SessionStatsProjection

### session/session-telemetry — 528 行 / 3 文件

- npm: `@deepseek-ai/dsh-session-telemetry`
- 层: 6
- 自述: SessionTelemetryBackend seam for the DeepSeek Harness: session-event capture, projection, redaction, and handoff to a reporting backend
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session
- README: --- description: "Session-telemetry capture seam for deployments and backend authors choosing a reporting backend, mounting redaction rules, or implementing the backend contract." kind: "package-library" ---
- 类: SessionTelemetryBackend, SessionTelemetryCoordinator
- 接口: SessionTelemetryRecord, SessionTelemetrySink

### session/session-telemetry-otel — 332 行 / 2 文件

- npm: `@deepseek-ai/dsh-session-telemetry-otel`
- 层: 8
- 自述: OpenTelemetry backend for the DeepSeek Harness telemetry seam: hands captured session records to the OTel JS SDK's log pipeline
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-anonymous-user-id, @deepseek-ai/dsh-command-feedback, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-telemetry, @deepseek-ai/schemastery
- README: --- description: "OpenTelemetry session-telemetry backend for deployments choosing a mode, configuring the exporter, or tracing what leaves the machine." kind: "package-reference" ---
- 类: OpenTelemetrySessionBackend
- 接口: Config

### session/session-title — 1056 行 / 5 文件

- npm: `@deepseek-ai/dsh-session-title`
- 层: 6
- 自述: Log-backed session title service and provider registry for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-util-values, @deepseek-ai/schemastery
- README: --- description: "Log-backed session titles for users and maintainers choosing a title source, configuring the service, or debugging title state." kind: "package-reference" ---
- 类: SessionTitleInvalidError, SessionTitleService
- 接口: Config, SessionTitleEventData, SessionTitleModelProvenance, SessionTitleProvider, SessionTitleProviderRequest, SessionTitleProviderResult, SessionTitleSnapshot, SessionTitleUserMessage, TitleInputState

### session/session-title-all-prompts-llm — 66 行 / 2 文件

- npm: `@deepseek-ai/dsh-session-title-all-prompts-llm`
- 层: 8
- 自述: All-user-messages LLM provider plugin for DeepSeek Harness session titles
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-title, @deepseek-ai/dsh-session-title-llm, @deepseek-ai/schemastery
- README: --- description: "All-messages LLM session-title provider for users and maintainers choosing a title strategy or debugging automatic title generation." kind: "package-reference" ---

### session/session-title-first-prompt-llm — 70 行 / 2 文件

- npm: `@deepseek-ai/dsh-session-title-first-prompt-llm`
- 层: 8
- 自述: First-message LLM provider plugin for DeepSeek Harness session titles
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-title, @deepseek-ai/dsh-session-title-llm, @deepseek-ai/schemastery
- README: --- description: "First-message LLM session-title provider for users and maintainers choosing a title strategy or debugging automatic title generation." kind: "package-reference" ---

### session/session-title-llm — 325 行 / 2 文件

- npm: `@deepseek-ai/dsh-session-title-llm`
- 层: 7
- 自述: Shared LLM generation policy for DeepSeek Harness session-title providers
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-title, @deepseek-ai/dsh-timeout, @deepseek-ai/dsh-util-values, @deepseek-ai/schemastery
- README: --- description: "Shared model-backed title generation policy for users and maintainers configuring title providers or debugging auxiliary LLM requests." kind: "package-library" ---
- 接口: ResolvedSessionTitleLlmConfig, SessionTitleLlmConfig, SessionTitleLlmRequestEventData

### session/session-turn-outline — 258 行 / 5 文件

- npm: `@deepseek-ai/dsh-session-turn-outline`
- 层: 5
- 自述: Whole-log turn outline projection (turnOutline) for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection
- README: --- description: "Whole-log turn outline for clients and maintainers composing or debugging the turnOutline projection unit behind full-session turn navigation." kind: "package-reference" ---
- 接口: TurnOutlineEntry, TurnOutlineState


## session-query

### session-query/session-log-export — 955 行 / 9 文件

- npm: `@deepseek-ai/dsh-session-log-export`
- 层: 2
- 自述: Web Session-log export command and shared download dialog
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/schemastery
- README: --- description: "Web Session-log ZIP export: Host streaming, the authenticated download route, the Session Header action, and the /export command." kind: "package-reference" ---
- 类: SessionLogDownloadController
- 接口: Config, SessionLogDownloadDialogInjected, SessionLogDownloadEntry, SessionLogDownloadState, SessionLogExportDeps, SessionLogExportReady

### session-query/session-query — 1954 行 / 12 文件

- npm: `@deepseek-ai/dsh-session-query`
- 层: 9
- 自述: Combined session query service contract with concrete reads, traces, and filters
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-session-projection-cache, @deepseek-ai/dsh-session-title, @deepseek-ai/dsh-tool-todo
- README: --- description: "The unified session-history query service for consumers and backend authors: exact reads, relationship traces, and provider-independent filters over live and durable session logs." kind: "package-reference" ---
- 类: SessionCorpus, SessionObservationReader, SessionQueryEngine, SessionQueryError
- 接口: Config, LogicalSession, LogicalSessionSource, SessionEventReadRequest, SessionEventRecord, SessionEventSearchDocument, SessionEventSearchHit, SessionEventSearchPage, SessionEventSearchRequest, SessionEventTrace, SessionEventTraceObservation, SessionEventTraceRequest, SessionEventWindow, SessionLineageNode, SessionLogSnapshot, SessionObservation, SessionObservationOptions, SessionRecord, SessionResultRange, SessionSearchExecContext, SessionSearchHit, SessionSearchPage, SessionSearchRequest, SessionSurfaceSnapshot, SessionTitleObservation

### session-query/session-query-sqlite — 1783 行 / 4 文件

- npm: `@deepseek-ai/dsh-session-query-sqlite`
- 层: 10
- 自述: Concrete ctx.sessionQuery backend with SQLite FTS5 search
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-session-query, @deepseek-ai/schemastery
- README: --- description: "The SQLite FTS5 full-text search backend for session history, for deployments and maintainers choosing, configuring, or debugging full-text search over the query service." kind: "package-reference" ---
- 类: SqliteSessionQueryEngine
- 接口: Config, NormalizedEventRequest, NormalizedSessionRequest, QueryLimits, SqlWhere

### session-query/tool-session-query — 1450 行 / 7 文件

- npm: `@deepseek-ai/dsh-tool-session-query`
- 层: 10
- 自述: Workspace-authorized model-facing session history search, trace, and event read tools
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-session-query, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-timeout, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: --- description: "Workspace-authorized model-facing session history tools for agent developers and maintainers choosing, configuring, or debugging prior-session search, tracing, and event reads." kind: "package-reference" ---
- 接口: Config


## settings

### settings/settings — 1182 行 / 5 文件

- npm: `@deepseek-ai/dsh-settings`
- 层: 4
- 自述: Abstract user-settings seam (ctx.settings) for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-util-values, @deepseek-ai/schemastery
- README: --- description: "The user-settings service for plugin authors and maintainers registering configurable namespaces, reading resolved values, or wiring configuration surfaces." kind: "package-reference" ---
- 类: SettingsConflictError, SettingsProvider
- 接口: RedactedSecret, RedactedValue, SettingsDescribeOptions, SettingsDescribeValue, SettingsDescriptor, SettingsNamespaceView, SettingsRegisterOptions, SettingsScope, SettingsSecretView, SettingsSectionHooks

### settings/settings-file — 402 行 / 2 文件

- npm: `@deepseek-ai/dsh-settings-file`
- 层: 5
- 自述: File-backed settings provider (settings.yaml) for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-atomic-write, @deepseek-ai/dsh-home-paths, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-settings, @deepseek-ai/dsh-util-values, @deepseek-ai/schemastery
- README: --- description: "The file-backed settings provider for users and maintainers choosing, configuring, or debugging the YAML/JSON settings document and its hot reload." kind: "package-reference" ---
- 类: FileSettingsProvider
- 接口: Config


## shell

### shell/bash-local — 365 行 / 2 文件

- npm: `@deepseek-ai/dsh-bash-local`
- 层: 6
- 自述: Local-subprocess implementation of the DeepSeek Harness bash executor seam
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-settings, @deepseek-ai/dsh-shell, @deepseek-ai/dsh-subprocess, @deepseek-ai/dsh-timeout, @deepseek-ai/schemastery
- README: --- description: "The default POSIX Bash executor for deployments and maintainers choosing, configuring, or debugging unconfined command execution over the shell seam." kind: "package-reference" ---
- 类: LocalBashExecutor
- 接口: Config

### shell/bash-sandbox — 328 行 / 3 文件

- npm: `@deepseek-ai/dsh-bash-sandbox`
- 层: 7
- 自述: Sandbox-consuming implementation of the DeepSeek Harness bash executor seam (confines every command via ctx.sandbox, reports denial/enforcement result facts)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-bash-local, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-sandbox, @deepseek-ai/dsh-sandbox-policy, @deepseek-ai/dsh-shell
- README: --- description: "The sandbox-consuming Bash executor for deployments and maintainers choosing, configuring, or debugging confined command execution with denial and escalation facts." kind: "package-reference" ---
- 类: SandboxBashExecutor

### shell/pwsh-local — 474 行 / 3 文件

- npm: `@deepseek-ai/dsh-pwsh-local`
- 层: 6
- 自述: Local PowerShell implementation of the DeepSeek Harness bash executor seam
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-settings, @deepseek-ai/dsh-shell, @deepseek-ai/dsh-subprocess, @deepseek-ai/dsh-timeout, @deepseek-ai/schemastery
- README: --- description: "The local PowerShell executor for deployments and maintainers choosing, configuring, or debugging unconfined PowerShell command execution over the shell seam." kind: "package-reference" ---
- 类: PwshLocalExecutor
- 接口: Config

### shell/pwsh-sandbox — 339 行 / 3 文件

- npm: `@deepseek-ai/dsh-pwsh-sandbox`
- 层: 7
- 自述: Sandbox-consuming implementation of the DeepSeek Harness PowerShell executor seam (confines every command via ctx.sandbox, reports denial/enforcement result facts)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-pwsh-local, @deepseek-ai/dsh-sandbox, @deepseek-ai/dsh-sandbox-policy, @deepseek-ai/dsh-shell
- README: --- description: "The sandbox-consuming PowerShell executor for deployments and maintainers choosing, configuring, or debugging confined PowerShell command execution with denial facts." kind: "package-reference" ---
- 类: SandboxPwshExecutor

### shell/shell — 350 行 / 4 文件

- npm: `@deepseek-ai/dsh-shell`
- 层: 5
- 自述: Abstract bash executor seam (ctx.shell) for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-sandbox, @deepseek-ai/dsh-settings, @deepseek-ai/dsh-subprocess
- README: --- description: "The bash executor seam for developers and maintainers choosing, composing, or implementing command execution over ctx.shell." kind: "package-reference" ---
- 类: ShellExecutor
- 接口: ShellExecRequest, ShellExecSpec, ShellProcess, ShellProcessRead, ShellRunResult, ShellSandboxInfo

### shell/shell-env — 247 行 / 2 文件

- npm: `@deepseek-ai/dsh-shell-env`
- 层: 8
- 自述: Tool-independent managed DSH_* shell environment registry
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-home-paths, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-shell, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: --- description: "The managed DSH_* shell environment for users and maintainers choosing, configuring, or extending the environment every model shell call runs with." kind: "package-reference" ---
- 类: ShellEnvRegistry
- 接口: BashEnvContributor, BashEnvVariable, BashEnvVariableInfo, Config

### shell/tool-bash — 553 行 / 4 文件

- npm: `@deepseek-ai/dsh-tool-bash`
- 层: 9
- 自述: Model-facing bash tool with optional generic background-job and sandbox-escalation support
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-jobs, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-sandbox, @deepseek-ai/dsh-sandbox-policy, @deepseek-ai/dsh-shell, @deepseek-ai/dsh-shell-env, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-user-approval, @deepseek-ai/schemastery
- README: --- description: "The model-facing bash tool for users and maintainers choosing, configuring, or debugging one-shot command execution, background jobs, and sandbox escalation." kind: "package-reference" ---
- 接口: Config

### shell/tool-bash-persistent — 503 行 / 2 文件

- npm: `@deepseek-ai/dsh-tool-bash-persistent`
- 层: 8
- 自述: Model-facing owner-scoped persistent Bash tool backed by the Harness PTY service
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-terminal, @deepseek-ai/dsh-timeout, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: --- description: "The model-facing persistent bash tool for users and maintainers choosing, configuring, or debugging owner-scoped shell state that survives across calls." kind: "package-reference" ---
- 接口: Config

### shell/tool-pwsh — 618 行 / 4 文件

- npm: `@deepseek-ai/dsh-tool-pwsh`
- 层: 9
- 自述: Model-facing pwsh tool over the bash executor seam
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-jobs, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-sandbox, @deepseek-ai/dsh-sandbox-policy, @deepseek-ai/dsh-shell, @deepseek-ai/dsh-shell-env, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-user-approval, @deepseek-ai/schemastery
- README: --- description: "The model-facing pwsh tool for users and maintainers choosing, configuring, or debugging one-shot PowerShell execution, background jobs, and sandbox escalation on Windows." kind: "package-reference" ---
- 接口: Config, RenderablePwshResult

### shell/tool-pwsh-persistent — 545 行 / 2 文件

- npm: `@deepseek-ai/dsh-tool-pwsh-persistent`
- 层: 8
- 自述: Model-facing owner-scoped persistent PowerShell tool backed by the Harness PTY service
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-terminal, @deepseek-ai/dsh-timeout, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: --- description: "The model-facing persistent pwsh tool for users and maintainers choosing, configuring, or debugging owner-scoped PowerShell state that survives across calls." kind: "package-reference" ---
- 接口: Config


## skill

### skill/skill — 899 行 / 2 文件

- npm: `@deepseek-ai/dsh-skill`
- 层: 3
- 自述: Agent skill provider registry for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-util-values, @deepseek-ai/schemastery
- README: --- description: "The skill provider registry for users and maintainers choosing, configuring, or debugging how skills from any source are merged, resolved, and loaded." kind: "package-reference" ---
- 类: SkillRegistry
- 接口: Config, SkillCandidate, SkillCatalogSnapshot, SkillDefinition, SkillInvocationPolicy, SkillInvocationSource, SkillLookupOptions, SkillProvider, SkillProviderControl, SkillProviderObservation, SkillSummary, SkillViewOptions

### skill/skill-badge — 90 行 / 2 文件

- npm: `@deepseek-ai/dsh-skill-badge`
- 层: 4
- 自述: Bundled dsh badge skill provider for DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-skill
- README: --- description: "The bundled 'powered by dsh' badge skill for users and maintainers enabling, using, or debugging the optional badge provider." kind: "package-reference" ---

### skill/skill-filesystem — 1071 行 / 2 文件

- npm: `@deepseek-ai/dsh-skill-filesystem`
- 层: 6
- 自述: Local filesystem skill provider for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-fs, @deepseek-ai/dsh-home-paths, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-skill, @deepseek-ai/schemastery
- README: --- description: "The local filesystem skill provider for users and maintainers authoring local skills or configuring how project, custom, and user skill roots are discovered and watched." kind: "package-reference" ---
- 类: FileSystemSkillProvider
- 接口: Config

### skill/tool-skill — 460 行 / 2 文件

- npm: `@deepseek-ai/dsh-tool-skill`
- 层: 8
- 自述: Model-facing skill loading tool for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-skill, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: --- description: "The model-facing skill catalog and loader tool for users and maintainers understanding what agents see, or configuring the session skill catalog." kind: "package-reference" ---
- 接口: Config, SkillCatalogSource


## spill

### spill/spill — 161 行 / 3 文件

- npm: `@deepseek-ai/dsh-spill`
- 层: 4
- 自述: Abstract spill storage seam (ctx.spillStore) for the DeepSeek Harness — save oversized tool text and return a retrieval locator
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session
- README: --- description: "The spill storage service: how deployments and plugin authors save oversized tool text and get back a retrievable locator." kind: "package-reference" ---
- 类: SpillStore
- 接口: SaveTextSpill, SpillOwner, SpillRef, SpillSource

### spill/spill-local — 756 行 / 4 文件

- npm: `@deepseek-ai/dsh-spill-local`
- 层: 5
- 自述: Local-filesystem implementation of the DeepSeek Harness spill storage seam (private session-scoped files)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-spill, @deepseek-ai/schemastery
- README: --- description: "The local filesystem spill backend: how spilled tool output is saved to private session-scoped files and retrieved with read or grep." kind: "package-reference" ---
- 类: LocalSpillStore
- 接口: Config, SaveTextOptions, SavedText, SweepOptions, SweepRoot

### spill/spill-policy — 288 行 / 3 文件

- npm: `@deepseek-ai/dsh-spill-policy`
- 层: 8
- 自述: Tool-result spill policy for the DeepSeek Harness — replaces oversized plain-text tool results with a retained preview plus a spill-file path (no service API)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-output-retention, @deepseek-ai/dsh-session, @deepseek-ai/dsh-spill, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: --- description: "The tool-result spill policy: how deployments keep oversized plain-text tool results out of the model's context with a preview and a retrievable spill file." kind: "package-reference" ---
- 接口: Config, SpillPolicyExec


## storage

### storage/storage — 342 行 / 5 文件

- npm: `@deepseek-ai/dsh-storage`
- 层: 1
- 自述: Storage hub (ctx.storage): named backend registry plus mounted data-form facilities for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: --- description: "Storage hub (ctx.storage) for compositions and maintainers choosing, mounting, or debugging named storage backends and data-form facilities." kind: "package-reference" ---
- 类: BackendRegistry, Storage, StorageError
- 接口: KvFacet, KvUnit, KvUnitDescriptor, StorageBackend, StorageForms

### storage/storage-domain — 874 行 / 6 文件

- npm: `@deepseek-ai/dsh-storage-domain`
- 层: 2
- 自述: Domain data form (ctx.storage.domain): schema-validated, event-emitting KV domains over storage backends for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-storage, @deepseek-ai/schemastery
- README: --- description: "Domain data form (ctx.storageDomain) for hosts and maintainers choosing, mounting, or debugging schema-validated, change-emitting KV domains over storage backends." kind: "package-reference" ---
- 类: DomainError, DomainFacility, DomainImpl
- 接口: Config, Domain, DomainChangedBase, DomainChangedDeleted, DomainChangedPut, DomainErrorOptions, DomainGlobal, DomainGlobalSpec, DomainSpec, DomainTableSpec, InvalidRecordDetail, KvTable

### storage/storage-json — 750 行 / 6 文件

- npm: `@deepseek-ai/dsh-storage-json`
- 层: 2
- 自述: JSON file KV storage backend for the DeepSeek Harness storage hub
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-storage, @deepseek-ai/schemastery
- README: --- description: "JSON storage backend for hosts and maintainers choosing, configuring, or debugging whole-unit and per-record files under a configured root." kind: "package-reference" ---
- 类: JsonStorageBackend, PerRecordJsonUnit
- 接口: Config, UnitState

### storage/storage-sqlite — 475 行 / 4 文件

- npm: `@deepseek-ai/dsh-storage-sqlite`
- 层: 2
- 自述: SQLite storage backend (kv facet) for the DeepSeek Harness storage hub
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-storage, @deepseek-ai/schemastery
- README: --- description: "SQLite storage backend for hosts and maintainers choosing, configuring, or debugging document-per-row KV storage in one database file." kind: "package-reference" ---
- 类: SqliteKvUnit, SqliteStorageBackend
- 接口: Config


## subagent

### subagent/subagent — 5235 行 / 20 文件

- npm: `@deepseek-ai/dsh-subagent`
- 层: 10
- 自述: Abstract subagent seam (ctx.subagents): named-provider registry for delegating to child agents
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-agent-presets, @deepseek-ai/dsh-attachment, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-jobs, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-sandbox, @deepseek-ai/dsh-sandbox-policy, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-session-projection-cache, @deepseek-ai/dsh-session-query, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-typert-protocol, @deepseek-ai/dsh-user-approval, @deepseek-ai/dsh-util-time, @deepseek-ai/dsh-util-values
- README: --- description: "The subagent delegation seam for users and maintainers choosing a provider backend, composing delegation tools, or debugging child-agent runs." kind: "package-reference" ---
- 类: AssistantOutputFold, SubagentActivationSetupRegistry, SubagentContinuationManager, SubagentDepthError, SubagentError, SubagentRuntime
- 接口: ActivationObserver, ActivationTerminal, ChildComposition, ChildCreateInputs, ContinuableCreateRequest, ContinuableCreateSpec, ContinuableStart, ContinuableStartSpec, ContinuableSubagentDescriptorData, ContinuableSubagentDescriptorInput, CoordinatorMessageSource, DelegatedPolicyOverrides, OneShotSubagentDescriptorData, OneShotSubagentDescriptorInput, ResolvedSubagentStartRequest, RunResultSettlement, SubagentCapabilities, SubagentCatalog, SubagentFollowupOptions, SubagentInterruptReceipt, SubagentPromptReceipt, SubagentPromptRequest, SubagentProvider, SubagentReportMessageSource, SubagentReportOptions, SubagentResult, SubagentRun, SubagentRunEndInfo, SubagentRunInfo, SubagentSettledMessageSource, SubagentStartRequest, SubagentStopReasonMap, SubagentTimingProjection, SubprocessRunHandleParts, TimingState

### subagent/subagent-acp — 843 行 / 3 文件

- npm: `@deepseek-ai/dsh-subagent-acp`
- 层: 11
- 自述: Out-of-process ACP subagent backend: drives a child agent in a spawned subprocess over the Agent Client Protocol
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-subprocess, @deepseek-ai/dsh-timeout, @deepseek-ai/schemastery
- README: --- description: "The out-of-process ACP subagent backend for users and maintainers choosing a delegation provider, configuring a child ACP agent command, or debugging remote child runs." kind: "package-reference" ---
- 接口: AcpRunSpec, Config

### subagent/subagent-claude-code — 944 行 / 4 文件

- npm: `@deepseek-ai/dsh-subagent-claude-code`
- 层: 11
- 自述: One-shot Claude Code subagent provider over the official Agent SDK
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-subprocess, @deepseek-ai/dsh-timeout, @deepseek-ai/schemastery
- README: --- description: "The one-shot Claude Code subagent provider for users and maintainers choosing a product backend, installing a Profile bundle, or configuring an unattended Claude Code delegation." kind: "package-bundle" ---
- 类: ManagedClaudeCodeProcess
- 接口: ClaudeCodeRunSpec, Config

### subagent/subagent-codex — 1317 行 / 4 文件

- npm: `@deepseek-ai/dsh-subagent-codex`
- 层: 12
- 自述: One-shot Codex subagent provider over the official app-server protocol
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-sdk-protocol, @deepseek-ai/dsh-session, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-subprocess, @deepseek-ai/dsh-timeout, @deepseek-ai/schemastery
- README: --- description: "The one-shot Codex subagent provider for users and maintainers choosing a product backend, installing a Profile bundle, or configuring an unattended Codex delegation." kind: "package-bundle" ---
- 类: CodexAppServerWire
- 接口: CodexRunSpec, CodexWireFailureFacts, Config

### subagent/subagent-dsh-sdk — 590 行 / 3 文件

- npm: `@deepseek-ai/dsh-subagent-dsh-sdk`
- 层: 13
- 自述: Out-of-process SDK subagent backend: drives a child DeepSeek Harness runtime subprocess over stdio JSON-RPC through the TypeScript SDK client
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-sdk-client, @deepseek-ai/dsh-session, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-subprocess, @deepseek-ai/schemastery
- README: --- description: "The out-of-process SDK subagent backend for users and maintainers choosing a delegation provider, configuring a child Harness runtime command, or debugging remote child runs." kind: "package-reference" ---
- 接口: Config, SdkRunSpec

### subagent/subagent-fork-in-process — 131 行 / 2 文件

- npm: `@deepseek-ai/dsh-subagent-fork-in-process`
- 层: 12
- 自述: In-process fork subagent backend: runs a child agent seeded with a prefix of the parent's log
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-subagent-in-process-driver, @deepseek-ai/schemastery
- README: --- description: "In-process fork subagent backend for users and maintainers choosing, configuring, or debugging children seeded with the parent's completed turns." kind: "package-reference" ---
- 接口: Config

### subagent/subagent-in-process-driver — 406 行 / 3 文件

- npm: `@deepseek-ai/dsh-subagent-in-process-driver`
- 层: 11
- 自述: Shared in-process subagent run driver: drives a child agent on ctx.agents (used by the spawn and fork backends)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools
- README: --- description: "Shared in-process subagent run driver for maintainers and backend authors understanding or extending the spawn and fork run lifecycle." kind: "package-library" ---
- 接口: InProcessRunOptions, StructuredAttachment

### subagent/subagent-spawn-in-process — 100 行 / 2 文件

- npm: `@deepseek-ai/dsh-subagent-spawn-in-process`
- 层: 12
- 自述: In-process spawn subagent backend: runs a fresh child agent on ctx.agents
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-subagent-in-process-driver, @deepseek-ai/schemastery
- README: --- description: "In-process spawn subagent backend for users and maintainers choosing, configuring, or debugging fresh-child delegation." kind: "package-reference" ---
- 接口: Config

### subagent/tool-subagent — 1257 行 / 7 文件

- npm: `@deepseek-ai/dsh-tool-subagent`
- 层: 11
- 自述: Model-facing subagent delegation tool over the ctx.subagents seam
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-jobs, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-scope, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-settings, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: --- description: "Model-facing subagent delegation tool for users and maintainers configuring, composing, or debugging delegation over a subagent provider." kind: "package-reference" ---
- 类: SubagentModelSelectionConfig
- 接口: AllowedModelRoute, Config, Config, DelegationModelRequest, ModelSelectionPolicy, SubagentModelSelectionSettings

### subagent/tool-subagent-control — 343 行 / 3 文件

- npm: `@deepseek-ai/dsh-tool-subagent-control`
- 层: 11
- 自述: Globally named send_message, interrupt_agent, and list_agents tools over ctx.subagents continuations
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-util-values
- README: --- description: "Global send_message, interrupt_agent, and list_agents tools for users and maintainers composing or debugging continuable-child control." kind: "package-reference" ---

### subagent/tool-subagent-report — 170 行 / 2 文件

- npm: `@deepseek-ai/dsh-tool-subagent-report`
- 层: 11
- 自述: Child-scoped report tool over ctx.subagents continuations
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: --- description: "Child-scoped report tool for users and maintainers composing or debugging the child-to-parent return channel of continuable subagents." kind: "package-reference" ---
- 接口: Config


## subprocess

### subprocess/subprocess — 428 行 / 3 文件

- npm: `@deepseek-ai/dsh-subprocess`
- 层: 1
- 自述: Subprocess seam (ctx.subprocess) for the DeepSeek Harness — managed process groups, bounded spill-backed output, and escalated kills behind one abstract service
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: --- description: "The subprocess service (ctx.subprocess) for composition authors and capability consumers starting, observing, and terminating managed child processes and terminal sessions." kind: "package-reference" ---
- 类: SubprocessRuntime
- 接口: CollectedOutput, SubprocessCollect, SubprocessCollectedOutputs, SubprocessHandle, SubprocessOutcome, SubprocessOutputRead, SubprocessOutputReader, SubprocessSpawnSpec, SubprocessStdio, SubprocessTerminalForeground, SubprocessTerminalHandle, SubprocessTerminalSpawnSpec

### subprocess/subprocess-local — 1966 行 / 6 文件

- npm: `@deepseek-ai/dsh-subprocess-local`
- 层: 2
- 自述: Local-subprocess implementation of the DeepSeek Harness subprocess seam
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-subprocess, @deepseek-ai/dsh-timeout
- README: --- description: "The local host provider for the subprocess service: run managed process trees and real terminal sessions on the host machine." kind: "package-reference" ---
- 类: LocalSubprocessRuntime, LocalTerminalHandle, OutputCollector, WindowsProcessInspector
- 接口: LocalSubprocessHandle, ProcessEntry, ProcessIdentity, ProcessInspector, ProcessInspectorInternals, ProcessSnapshot, SpawnInternals, WindowsProcessInspectorInternals, WindowsProcessState

### subprocess/win32-process — 845 行 / 6 文件

- npm: `@deepseek-ai/dsh-win32-process`
- 层: 1
- 自述: Low-level Win32 process, stdio, and Job Object primitives for the DeepSeek Harness Windows sandbox
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: --- description: "Low-level Win32 process primitives for maintainers implementing or debugging the Windows ACL sandbox." kind: "package-library" ---
- 类: Win32Error
- 接口: ProcessInfoOutput, RestrictedProcessSpawnOptions, SpawnedJobProcess, SpawnedPipedProcess, StartupInfoInput, Win32BindingContext, Win32ProcessBindings


## terminal

### terminal/terminal — 683 行 / 3 文件

- npm: `@deepseek-ai/dsh-terminal`
- 层: 6
- 自述: Persistent PTY session seam for the DeepSeek Harness — owner-scoped ids, backend registry, interactive sends, reads, signals, and awaited cleanup
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants
- README: --- description: "Persistent terminal sessions for deployments and consumers choosing, composing, or extending the owner-scoped ctx.terminals service." kind: "package-reference" ---
- 类: TerminalBackendCleanupError, TerminalError, TerminalSessionService
- 接口: TerminalBackend, TerminalBackendSession, TerminalBackendSpawnSpec, TerminalReadRequest, TerminalReadResult, TerminalSendOperation, TerminalSendRead, TerminalSendRequest, TerminalSendResult, TerminalSessionSnapshot, TerminalSignalResult, TerminalSpawnRequest, TerminalSpawnResult

### terminal/terminal-bash — 1274 行 / 5 文件

- npm: `@deepseek-ai/dsh-terminal-bash`
- 层: 7
- 自述: Persistent shell PTY backend over the DeepSeek Harness subprocess terminal primitive
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-pwsh-local, @deepseek-ai/dsh-sandbox, @deepseek-ai/dsh-sandbox-policy, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-subprocess, @deepseek-ai/dsh-terminal, @deepseek-ai/schemastery
- README: --- description: "The shipped shell backend for persistent terminal sessions: interactive bash or pwsh under the shared sandbox policy, with readiness detection and bounded line-oriented output." kind: "package-reference" ---
- 类: BashTerminalBackend, LocalPtySession, TerminalSanitizer
- 接口: Config, SanitizedChunk

### terminal/tool-terminal — 608 行 / 3 文件

- npm: `@deepseek-ai/dsh-tool-terminal`
- 层: 8
- 自述: Six model-facing persistent PTY tools with owner isolation and generic background-job integration
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-jobs, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-output-retention, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-terminal, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: --- description: "Six model-facing persistent terminal tools with owner isolation, bounded results, and optional background sends for agents that need cross-call terminal state." kind: "package-reference" ---
- 接口: Config


## test-support

### test-support/agent-loop-testkit — 76 行 / 2 文件

- npm: `@deepseek-ai/dsh-agent-loop-testkit`
- 层: 8
- 自述: Shared prerequisite mounting for tests that exercise the concrete agent loop
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools
- README: --- description: "Shared service mounting for tests that exercise the concrete AgentLoop, for test authors wiring real loop prerequisites." kind: "package-library" ---
- 接口: AgentLoopTestDependenciesOptions

### test-support/client-runtime — 1654 行 / 12 文件

- npm: `@deepseek-ai/dsh-client-test-runtime`
- 层: 12
- 自述: jsdom slot test runtime: real Cordis Context + SlotRegistry + UI renderer with test-owned session/workspace doubles for feature specs
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-api-session-controller, @deepseek-ai/dsh-api-workspace-controller, @deepseek-ai/dsh-attachment, @deepseek-ai/dsh-client-connection, @deepseek-ai/dsh-client-store, @deepseek-ai/dsh-client-ui-chat, @deepseek-ai/dsh-client-ui-conversation, @deepseek-ai/dsh-client-ui-renderer, @deepseek-ai/dsh-client-ui-session, @deepseek-ai/dsh-client-ui-settings, @deepseek-ai/dsh-client-ui-slots, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-typert-protocol
- README: --- description: "jsdom slot test runtime for browser feature specs, for test authors exercising slots, stores, and rendering against production machinery." kind: "package-library" ---
- 类: FixtureSession, SlotTestRuntime, TestRemote, TestRoot, TestSessions, TestWorkspaces
- 接口: FeatureHandle, ScriptedNamespace, ScriptedSettingsRemote, SessionFixture, SlotView, StubSettingsScope

### test-support/llm-mock-server — 1044 行 / 5 文件

- npm: `@deepseek-ai/dsh-llm-mock-server`
- 层: 1
- 自述: Scriptable OpenAI-compatible HTTP/SSE fault server for LLM recovery tests
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: --- description: "Scriptable OpenAI-compatible fault server for testing LLM adapters and recovery policy without a provider key, for test authors and demos." kind: "package-library" ---
- 接口: MockLlmCliConfig, MockLlmRequestRecord, MockLlmServer, MockLlmServerOptions

### test-support/llm-replay — 1015 行 / 2 文件

- npm: `@deepseek-ai/dsh-llm-replay`
- 层: 8
- 自述: Replay LLM plugin: short-circuits llm/stream with model chunks reconstructed from a recorded session JSONL (keyless snapshot tests)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-compaction, @deepseek-ai/dsh-deepseek-llm-api-extensions, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-util-values
- README: --- description: "Keyless LLM replay plugin for snapshot tests, for test authors booting the real agent against recorded model transcripts." kind: "package-reference" ---
- 接口: Config, ReplayConfig, ReplayHandle, ReplayModelConfig, ReplayOverridePatch, ReplayProviderConfig, SessionScript

### test-support/loader-smoke — 352 行 / 3 文件

- npm: `@deepseek-ai/dsh-loader-smoke`
- 层: 6
- 自述: Shared subprocess and direct-agent harness for keyless real-Loader example smoke tests
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session
- README: --- description: "Shared subprocess and direct-agent harness for keyless example smoke tests, for test authors booting real Loader compositions." kind: "package-library" ---
- 接口: ExampleLaunch, ExampleLaunchOptions, FixtureTurnOptions, FixtureTurnResult, LoaderSmokeOptions, LoaderSmokeResult

### test-support/session-snapshot — 4197 行 / 9 文件

- npm: `@deepseek-ai/dsh-session-snapshot`
- 层: 7
- 自述: Session-log snapshot core with an ACP protocol adapter, expected-output normalization, and fixture invariants
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-include, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-loader-smoke, @deepseek-ai/dsh-session
- README: --- description: "Session-log snapshot support for keyless profile tests: manifests, identity redaction, normalization, workspace checks, and protocol adapters." kind: "package-library" ---
- 接口: AcpTestClient, AcpTestLaunchOptions, AgentUnderTest, CaptureWorkspaceSnapshotOptions, FixtureReplacement, HarvestedLog, InputScript, LaunchedAcpTestAgent, NamedSnapshotContent, NormalizeContext, NormalizeOptions, PermissionAnswer, RunOptions, RunResult, Scenario, SharedSnapshotClaim, SnapshotHeaderManifest, SnapshotInputAttachment, SnapshotInputManifest, SnapshotManifest, SnapshotReplayManifest, SnapshotSessionReference, SnapshotSuiteOptions, SnapshotWorkspaceManifest, ToolSchemasSnapshot, WorkspaceBinaryFileSnapshot, WorkspaceEmptyDirectorySnapshot, WorkspaceSymlinkSnapshot, WorkspaceTextFileSnapshot


## todo

### todo/tool-todo — 383 行 / 4 文件

- npm: `@deepseek-ai/dsh-tool-todo`
- 层: 8
- 自述: Model-facing todo_write tool over the DeepSeek Harness event-sourced session log
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-projection, @deepseek-ai/dsh-tools, @deepseek-ai/schemastery
- README: --- description: "The model-facing todo_write tool over the DeepSeek Harness session log: whole-list replacement, per-session ownership, and the todos projection, for users and maintainers choosing, configuring, or debugging the tool." kind: "package-reference" ---
- 接口: Config, TodoItem


## typert

### typert/generator — 6316 行 / 9 文件

- npm: `@deepseek-ai/dsh-typert-generator`
- 层: 1
- 自述: TypeScript project analyzer and model-driven Typert artifact generator
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: --- description: "The build-time Typert generator: source type analysis, compiler-independent models, and artifact emission for maintainers wiring Typert publication or consuming generated artifacts." kind: "package-library" ---
- 类: CordisCatalogProjector, FaceModelEmitter, TypeGraphRenderError, TypeGraphRenderer, TypertAnalysisError, TypertEmitError, WorkspaceAnalyzer, WorkspaceCaches, WorkspaceTypertGenerator
- 接口: AccessorMemberModel, CordisCatalogModel, CordisCatalogPolicy, CrossFaceLink, DiscoveredTypertPackage, DocumentationModel, EnumMemberModel, EventEntry, EventModel, ExportModel, FaceModel, InheritedEntry, InvocationModel, InvocationParameterModel, JsDocTagModel, MemberBase, MethodMemberModel, ModelEmitResult, ObjectModel, PackageModel, PackageRegistration, ParameterModel, ParsedConfig, PropertyMemberModel, RemoteBoundaryModel, RemoteModelEmitResult, RemoteTypeImportModel, SchemaModel, ServiceEntry, ServiceMethodEntry, ServiceModel, SignatureMemberModel, SignatureModel, SourceDeclarationModel, SourceLocation, TemplateSpanModel, TupleElementModel, TypeDeclarationModel, TypeDeclarationPartModel, TypeGraph, TypeParameterModel, TypertPluginOptions, WorkspaceAnalyzerOptions, WorkspaceEmitResult, WorkspaceModel, WorkspaceTypertGeneratorOptions

### typert/loader — 472 行 / 2 文件

- npm: `@deepseek-ai/dsh-typert-loader`
- 层: 1
- 自述: Loader integration for generated Typert package contributions
- 依赖: @deepseek-ai/cordis, @deepseek-ai/cordis-plugin-loader, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-typert-registry, @deepseek-ai/schemastery
- README: --- description: "Loader integration for generated Typert artifacts: how mounted packages automatically contribute their host-face reflection and schemas to the runtime registry." kind: "package-reference" ---
- 接口: Config

### typert/protocol — 1002 行 / 4 文件

- npm: `@deepseek-ai/dsh-typert-protocol`
- 层: 1
- 自述: Compiler-independent Remote metadata and Typert provider protocols
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: --- description: "The shared Typert Remote protocol: decorators, wire descriptors, codecs, and provider contracts used by business packages, generated artifacts, the Host Gateway, and the Client API." kind: "package-library" ---
- 类: RemoteError, TypertRemoteService
- 接口: InvocationDescriptor, InvocationParameterDescriptor, InvocationSourceLocation, RemoteErrorDetailsMap, RemoteMethodMarker, RemoteMethodOptions, TypertClientContextAdapter, TypertClientRemote, TypertContext, TypertContextAdapter, TypertContextMap, TypertContextRegistry, TypertGatewayBinding, TypertGatewayBindingOptions, TypertHostContextAdapter, TypertHostContextIdentity, TypertLocalRegistry, TypertLookup, TypertLookupDefinition, TypertLookupMap, TypertLookupProvider, TypertLookupRegistry, TypertRegistryChange, TypertRegistryContract, TypertRemoteContribution, TypertRemoteEventSelection, TypertRemoteMap, TypertRemoteNamespaceMap, TypertRemoteRegistry, TypertRemoteScopeMap, TypertSchema

### typert/registry — 929 行 / 6 文件

- npm: `@deepseek-ai/dsh-typert-registry`
- 层: 0
- 自述: Runtime registry for generated package reflection and Zod schemas
- 依赖: @deepseek-ai/cordis
- README: --- description: "The runtime Typert registry: stores generated package reflection, live Zod schemas, and Remote invocation descriptors, and resolves them for consumers." kind: "package-reference" ---
- 类: TypertRegistry
- 接口: TypertContribution, TypertDocTag, TypertDocumentation, TypertEventModel, TypertMemberModel, TypertObjectModel, TypertPackageFilter, TypertPackageModel, TypertPackageRecord, TypertSchema, TypertSchemaFilter, TypertSchemaRecord, TypertServiceModel, TypertTypeModel


## util

### util/atomic-write — 213 行 / 2 文件

- npm: `@deepseek-ai/dsh-atomic-write`
- 层: 1
- 自述: Zero-dependency atomic file replacement: exclusive-create random-suffix temp + rename carrying the caller-stated permissions (writeFileAtomic)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: --- description: "Atomic file replacement and cross-process writer locking for packages that must never leave partial, symlink-hijacked, or wider-permission content on disk." kind: "package-library" ---
- 接口: FileLockOptions, WriteFileAtomicOptions

### util/brand — 56 行 / 2 文件

- npm: `@deepseek-ai/dsh-brand`
- 层: 1
- 自述: Stateless branded-string primitives for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: --- description: "Nominal string types and stateless constructors for packages that own identifiers crossing package boundaries." kind: "package-library" ---

### util/crypto — 71 行 / 2 文件

- npm: `@deepseek-ai/dsh-util-crypto`
- 层: 1
- 自述: Zero-dependency browser-safe UUID and byte-encoding helpers
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: --- description: "Cross-runtime UUID generation for maintainers replacing secure-context-only crypto.randomUUID calls." kind: "package-library" ---

### util/deque — 161 行 / 3 文件

- npm: `@deepseek-ai/dsh-deque`
- 层: 1
- 自述: Zero-dependency circular deque with amortized constant-time end operations and bounded vacant storage
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: --- description: "Circular deque for Host and browser packages that need amortized constant-time queue operations, immediate release of removed entries, and bounded vacant storage." kind: "package-library" ---
- 类: Deque

### util/home-paths — 142 行 / 2 文件

- npm: `@deepseek-ai/dsh-home-paths`
- 层: 1
- 自述: Shared filesystem path helpers for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: --- description: "Shared resolution of the DeepSeek Harness home and user-data paths for packages that need one consistent root, tilde expansion, and stable watch paths." kind: "package-library" ---

### util/launch-environment — 154 行 / 2 文件

- npm: `@deepseek-ai/dsh-launch-environment`
- 层: 1
- 自述: Immutable DeepSeek Harness launch environment that records which layer supplied each value
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: --- description: "An immutable snapshot of this run's environment that remembers which layer supplied each value, for packages that must resolve user-facing values without trusting a flattened process.env." kind: "package-library" ---
- 接口: LaunchEnvironmentEntry, LaunchEnvironmentLayerInput, LaunchEnvironmentSnapshot

### util/native-command — 291 行 / 4 文件

- npm: `@deepseek-ai/dsh-native-command`
- 层: 1
- 自述: Host-native command and path-opening utilities with shell-free execution, cancellation, desktop detection, and WSL handoff
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: --- description: "Host-native command and path-opening utilities with shell-free execution, cancellation, desktop detection, and WSL path handoff." kind: "package-library" ---
- 接口: PathOpenerInternals

### util/output-retention — 473 行 / 2 文件

- npm: `@deepseek-ai/dsh-output-retention`
- 层: 1
- 自述: Zero-dependency bounded-retention primitive: ItemRetainer/TextRetainer + neutral notice helpers (what did we keep, what did we omit)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: --- description: "Bounded model-facing output for tools that must cap how much context they return: item and text retainers plus a standardized omission footer." kind: "package-library" ---
- 类: ItemRetainer, TextRetainer
- 接口: PushDecision, RetainedItems, RetainedText, RetentionNotice

### util/time — 63 行 / 2 文件

- npm: `@deepseek-ai/dsh-util-time`
- 层: 1
- 自述: Zero-dependency time vocabulary shared by wire boundaries: canonicalClientTimeZone (IANA zone validation and canonicalization only, no formatting)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: --- description: "IANA time-zone validation and canonicalization for maintainers accepting a caller-reported zone at a wire boundary." kind: "package-library" ---

### util/timeout — 220 行 / 2 文件

- npm: `@deepseek-ai/dsh-timeout`
- 层: 1
- 自述: Zero-dependency timeout/deadline primitive: clampTimeout, deadline, timeoutOf, TimeoutReason (timing + classification only, no termination)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: --- description: "Shared timeout arithmetic, deadline fusion, and timeout-versus-cancel classification for capabilities that clamp a caller's hint, arm a deadline, and must tell the two apart later." kind: "package-library" ---
- 类: TimeoutReason
- 接口: Deadline, IdleWatchdog

### util/values — 263 行 / 2 文件

- npm: `@deepseek-ai/dsh-util-values`
- 层: 1
- 自述: Duplicate-install-safe value primitives for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: --- description: "Lossless JSON validation, detached snapshots, deep freezing, structural equality, and exhaustive-union helpers for runtime packages." kind: "package-library" ---

### util/workspace-path — 78 行 / 2 文件

- npm: `@deepseek-ai/dsh-util-workspace-path`
- 层: 1
- 自述: Browser-safe Workspace path and display helpers
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants
- README: --- description: "Browser-safe Workspace path helpers for joining relative paths, abbreviating POSIX homes, and deriving display titles." kind: "package-library" ---


## web

### web/tool-web — 1020 行 / 5 文件

- npm: `@deepseek-ai/dsh-tool-web`
- 层: 8
- 自述: Model-facing web tools (web_search, web_fetch) over the DeepSeek Harness web capability seam (ctx.web)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-util-values, @deepseek-ai/dsh-web, @deepseek-ai/schemastery
- README: --- description: "The model-facing web tools (web_search, web_fetch) over ctx.web: how deployments enable, configure, and observe the search and fetch tools the model sees." kind: "package-reference" ---
- 接口: Config, WebFetchMeta, WebSearchMeta

### web/web — 362 行 / 3 文件

- npm: `@deepseek-ai/dsh-web`
- 层: 3
- 自述: Abstract web access capability seam (ctx.web) for the DeepSeek Harness — search/fetch provider registry, registration-order-independent selection, request/result vocabulary, and the WebError taxonomy
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/schemastery
- README: --- description: "The web access service (ctx.web): how deployments and plugin authors search the web and fetch URLs through interchangeable providers, with one selection policy and error vocabulary." kind: "package-reference" ---
- 类: WebError, WebRuntime
- 接口: WebFetchProvider, WebFetchRequest, WebFetchResult, WebRuntimeConfig, WebSearchProvider, WebSearchRequest, WebSearchResult, WebSearchSource

### web/web-fetch-http — 748 行 / 5 文件

- npm: `@deepseek-ai/dsh-web-fetch-http`
- 层: 4
- 自述: Anonymous public HTTP(S) fetch provider for the DeepSeek Harness web capability seam (ctx.web)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-timeout, @deepseek-ai/dsh-web, @deepseek-ai/schemastery
- README: --- description: "The anonymous public HTTP(S) fetch backend for ctx.web: how deployments mount bounded, safe URL retrieval with same-origin redirects and text-only decoding." kind: "package-reference" ---
- 类: HttpFetchProvider
- 接口: Config, HttpFetchLimits, PinnedResponse, PublicAddress

### web/web-search-deepseek — 590 行 / 4 文件

- npm: `@deepseek-ai/dsh-web-search-deepseek`
- 层: 6
- 自述: DeepSeek-backed search provider (native web_search via the Anthropic-compatible API) for the DeepSeek Harness web capability seam (ctx.web)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-credentials, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-launch-environment, @deepseek-ai/dsh-session, @deepseek-ai/dsh-settings, @deepseek-ai/dsh-web, @deepseek-ai/schemastery
- README: --- description: "The DeepSeek-backed search provider for ctx.web: how deployments mount native DeepSeek web search through the Anthropic-compatible Messages API, with per-search credential resolution." kind: "package-reference" ---
- 类: DeepSeekSearchProvider
- 接口: AnthropicError, AnthropicResponse, CitationLocation, Config, DeepSeekSearchLlmRequest, DeepSeekSearchProviderOptions, TextBlock, WebSearchResultItem, WebSearchToolResultBlock

### web/web-search-exa — 300 行 / 4 文件

- npm: `@deepseek-ai/dsh-web-search-exa`
- 层: 4
- 自述: Exa-backed search provider for the DeepSeek Harness web capability seam (ctx.web)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-launch-environment, @deepseek-ai/dsh-web, @deepseek-ai/schemastery
- README: --- description: "The Exa-backed search provider for ctx.web: how deployments mount vendor-native web search with portable snippets and publication dates." kind: "package-reference" ---
- 类: ExaSearchProvider
- 接口: Config, ExaError, ExaResult, ExaSearchProviderOptions, ExaSearchRequest, ExaSearchResponse

### web/web-search-perplexity — 294 行 / 4 文件

- npm: `@deepseek-ai/dsh-web-search-perplexity`
- 层: 4
- 自述: Perplexity-backed search provider for the DeepSeek Harness web capability seam (ctx.web)
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-launch-environment, @deepseek-ai/dsh-web, @deepseek-ai/schemastery
- README: --- description: "The Perplexity-backed search provider for ctx.web: how deployments mount OpenAI-compatible Perplexity search with generated answers and citations." kind: "package-reference" ---
- 类: PerplexitySearchProvider
- 接口: Config, PerplexityError, PerplexityRequest, PerplexityResponse, PerplexitySearchProviderOptions, PerplexitySearchResult


## webhook

### webhook/webhook — 531 行 / 5 文件

- npm: `@deepseek-ai/dsh-webhook`
- 层: 9
- 自述: Fire-and-forget webhook rule runtime that creates Workspace-backed DeepSeek Harness Sessions
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-agent-default-model, @deepseek-ai/dsh-agent-presets, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-permission-presets, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-title, @deepseek-ai/dsh-util-values, @deepseek-ai/dsh-workspace
- README: --- description: "Webhook rule runtime for maintainers registering trusted external-event policies that create Workspace Sessions." kind: "package-reference" ---
- 类: WebhookRuntime
- 接口: VerifiedWebhookDelivery, WebhookEventMap, WebhookModelSelection, WebhookRule, WebhookSessionRequest

### webhook/webhook-github — 308 行 / 5 文件

- npm: `@deepseek-ai/dsh-webhook-github`
- 层: 10
- 自述: Signed GitHub HTTP webhook adapter for the DeepSeek Harness webhook runtime
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-credentials, @deepseek-ai/dsh-host-webserver, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-util-values, @deepseek-ai/dsh-webhook, @deepseek-ai/schemastery
- README: --- description: "Signed GitHub webhook adapter for deployments routing authenticated JSON events into the webhook runtime." kind: "package-reference" ---
- 类: WebhookHttpError
- 接口: Config, GitHubWebhookEvent, GitHubWebhookHandlerConfig


## workflow

### workflow/tool-ralph — 507 行 / 2 文件

- npm: `@deepseek-ai/dsh-tool-ralph`
- 层: 11
- 自述: Model-facing fresh-agent Ralph loop over the workflow and subagent seams
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-workflow, @deepseek-ai/schemastery
- README: --- description: "The model-facing ralph tool: a fixed foreground fresh-agent loop toward one immutable objective, for users and maintainers choosing or configuring fresh-agent iteration." kind: "package-reference" ---
- 接口: Config

### workflow/tool-workflow — 565 行 / 3 文件

- npm: `@deepseek-ai/dsh-tool-workflow`
- 层: 8
- 自述: Model-facing workflow tool: run a JavaScript orchestration script over ctx.workflowEngine
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-system-prompt, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-workflow, @deepseek-ai/schemastery
- README: --- description: "The model-facing workflow tool: run a JavaScript orchestration script that fans out subagents, for users and maintainers choosing or configuring model-driven orchestration." kind: "package-reference" ---
- 接口: Config, ToolWorkflowAgentEndData, ToolWorkflowAgentStartData, ToolWorkflowRunEndData, ToolWorkflowRunStartData

### workflow/workflow — 519 行 / 4 文件

- npm: `@deepseek-ai/dsh-workflow`
- 层: 6
- 自述: Workflow capability seam: ctx.workflowEngine service, run vocabulary, and workflow/* events
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session
- README: --- description: "The workflow orchestration capability: run a model-written script that fans out subagents, for users and maintainers choosing or building on ctx.workflowEngine." kind: "package-reference" ---
- 类: WorkflowEngine, WorkflowError
- 接口: WorkflowAgentEndInfo, WorkflowAgentInfo, WorkflowMeta, WorkflowPhase, WorkflowResult, WorkflowResultInfo, WorkflowRun, WorkflowRunInfo, WorkflowStartRequest

### workflow/workflow-worker-thread — 2016 行 / 11 文件

- npm: `@deepseek-ai/dsh-workflow-worker-thread`
- 层: 11
- 自述: worker-thread workflow engine: executes model-written orchestration scripts off the host event loop, bridging agent() calls back to ctx.subagents
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-agent, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-llm, @deepseek-ai/dsh-session, @deepseek-ai/dsh-subagent, @deepseek-ai/dsh-tools, @deepseek-ai/dsh-util-values, @deepseek-ai/dsh-workflow, @deepseek-ai/schemastery
- README: --- description: "The worker-thread workflow engine: executes model-written orchestration scripts off the host event loop, for users and maintainers choosing or configuring execution isolation." kind: "package-reference" ---
- 类: MaterializeError, WorkerRun, WorkflowExecution
- 接口: ChildHandle, ChildPort, ChildResult, ChildStartRequest, Config, ExecutionObserver, HostToWorkerPayloads, WorkerInit, WorkerLimits, WorkerToHostPayloads


## workspace

### workspace/workspace — 1151 行 / 6 文件

- npm: `@deepseek-ai/dsh-workspace`
- 层: 5
- 自述: Workspace entity registry (ctx.workspaceRegistry): durable workspace records with validated session attachment over the domain data form for the DeepSeek Harness
- 依赖: @deepseek-ai/cordis, @deepseek-ai/dsh-brand, @deepseek-ai/dsh-invariants, @deepseek-ai/dsh-session, @deepseek-ai/dsh-session-persistence, @deepseek-ai/dsh-storage, @deepseek-ai/dsh-storage-domain, @deepseek-ai/dsh-typert-protocol
- README: --- description: "Workspace entity registry (ctx.workspaceRegistry) for hosts choosing, mounting, or debugging durable workspace records and header-validated session membership." kind: "package-reference" ---
- 类: WorkspaceEntity, WorkspaceMoveInvalidError, WorkspaceOrderInvalidError, WorkspaceRegistry, WorkspaceUnknownSessionError
- 接口: Workspace, WorkspaceEntityHost

