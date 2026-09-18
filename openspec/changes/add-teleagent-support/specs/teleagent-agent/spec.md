## Purpose

Register TeleAgent (TeleAI 星辰超级智能体) as a first-class agent type in
agentsview, backed by a per-user SQLite archive at
`<data_dir>/users/<user_id>/teleagent.db`. Covers auto-discovery of the
first user subdirectory, multi-table session parsing with embedded tool
results, `_SYS_` session filtering, subagent lineage, per-message token
usage, file watching, frontend display, and configuration.

## ADDED Requirements

### Requirement: Agent type registration

The system SHALL register `teleagent` as a supported agent type in the
parser registry with the type identifier `"teleagent"`.

#### Scenario: Agent constant exists
- **WHEN** the parser package is initialized
- **THEN** `AgentTeleAgent` constant SHALL be defined with value `"teleagent"`

#### Scenario: Registry entry exists
- **WHEN** the agent registry is queried by type `AgentTeleAgent`
- **THEN** the returned `AgentDef` SHALL have:
  - `DisplayName` set to `"TeleAgent"`
  - `EnvVar` set to `"TELEAGENT_DIR"`
  - `ConfigKey` set to `"teleagent_dirs"`
  - `DefaultDirs` containing platform-specific `TeleAgent/users` paths
    (Windows `.local/share/TeleAgent/users`, macOS
    `Library/Application Support/TeleAgent/users` and
    `.local/share/TeleAgent/users`, Linux
    `.local/share/TeleAgent/users` and `.config/TeleAgent/users`)
  - `IDPrefix` set to `"teleagent:"`
  - `FileBased` set to `false`

#### Scenario: Provider factory registration
- **WHEN** `providerFactoryForDef` is called with `AgentTeleAgent`
- **THEN** it SHALL return a non-nil `ProviderFactory` constructed by
  `newTeleAgentProviderFactory`

#### Scenario: Provider migration classification
- **WHEN** `providerMigrationModes` is consulted for `AgentTeleAgent`
- **THEN** the entry SHALL be `ProviderMigrationProviderAuthoritative`

#### Scenario: ID prefix matching
- **WHEN** a session ID starts with `"teleagent:"`
- **THEN** `AgentByPrefix` SHALL return the TeleAgent agent definition

### Requirement: First-user auto-discovery

The provider SHALL auto-discover the first user subdirectory under a
configured `users/` root and use its `teleagent.db` as the backing
archive. Only the first user subdirectory (lexicographically) that
contains a `teleagent.db` file SHALL be read.

#### Scenario: Single user directory
- **WHEN** a configured root is `~/.local/share/TeleAgent/users/` and
  contains one subdirectory `v1_public_<id>/` with a `teleagent.db`
- **THEN** the provider SHALL use
  `~/.local/share/TeleAgent/users/v1_public_<id>/teleagent.db` as the
  backing DB for all discovery and parse operations

#### Scenario: Multiple user directories
- **WHEN** a configured root contains multiple subdirectories each with
  a `teleagent.db`
- **THEN** the provider SHALL read only the lexicographically first
  subdirectory's `teleagent.db` and SHALL NOT read the others

#### Scenario: No user subdirectory
- **WHEN** a configured root contains no subdirectory with a
  `teleagent.db`
- **THEN** discovery SHALL return no sources and SHALL NOT error

#### Scenario: Explicit subdirectory override
- **WHEN** the configured root points directly at a specific
  `users/<id>/` directory (not the `users/` parent)
- **THEN** the provider SHALL detect the `teleagent.db` in that
  directory and use it, without scanning siblings

### Requirement: Session discovery and filtering

The provider SHALL discover sessions from the `session` table of the
backing SQLite database, excluding system maintenance sessions.

#### Scenario: User session discovery
- **WHEN** the `session` table contains a row with `title = "需求评分技能扩展支持多试卷"`
  and `parent_id IS NULL`
- **THEN** discovery SHALL emit a `SourceRef` with
  `Provider = AgentTeleAgent`, `DisplayPath` set to the virtual path
  `<dbPath>#<sessionID>`, and `DiscoveryMTimeNS` derived from
  `session.time_updated`

#### Scenario: Subagent session discovery
- **WHEN** the `session` table contains a row with
  `title = "Generate 18 SVG pages P01-P09 (@general subagent)"` and
  `parent_id` set to another session's id
- **THEN** discovery SHALL emit a `SourceRef` for it (subagent sessions
  are not filtered out)

#### Scenario: System session filtering
- **WHEN** the `session` table contains a row with
  `title = "_SYS_MEMORY_MERGE_ 2026/9/18 07:58:46"`
- **THEN** discovery SHALL NOT emit a `SourceRef` for it

#### Scenario: Empty title filtering
- **WHEN** the `session` table contains a row with `title = ""`
- **THEN** discovery SHALL NOT emit a `SourceRef` for it

#### Scenario: Container source emission
- **WHEN** discovery enumerates a backing DB with N user sessions
- **THEN** it SHALL also emit one container `SourceRef` with
  `Kind = teleagentSourceSQLiteDB` whose `Opaque` carries the full
  member set, mirroring Kiro's container-source pattern

### Requirement: Source fingerprint

The provider SHALL fingerprint each virtual session source by reading
the backing DB row's `time_updated` and the byte size of all parts for
that session, plus a composite mtime covering the DB, WAL, and SHM
sidecars.

#### Scenario: SQLite session fingerprint
- **WHEN** `Fingerprint` is called for a `teleagentSourceSQLiteSession`
  whose backing DB exists
- **THEN** the returned `SourceFingerprint` SHALL have:
  - `Key` set to the virtual source path
  - `MTimeNS` set to `session.time_updated` in nanoseconds
  - `Size` set to the sum of `length(part.data)` for all parts of the
    session

#### Scenario: Composite DB mtime
- **WHEN** the backing DB has a `-wal` sidecar with a newer mtime than
  the DB file
- **THEN** the fingerprint's `MTimeNS` SHALL reflect the composite mtime
  of DB + WAL + SHM (via `sqliteDBCompositeMtime`), not the DB file
  alone

#### Scenario: Vanished DB preserves stored rows
- **WHEN** `Fingerprint` is called for a source whose backing DB file
  no longer exists
- **THEN** the fingerprint SHALL return `Key` only with zero `Size` and
  `MTimeNS`, and the parse path SHALL skip with `SkipNoSession` WITHOUT
  `ForceReplace`, so stored rows are preserved (persistent-archive
  semantics, matching Kiro)

### Requirement: Session parsing

The provider SHALL parse each user session by joining the `session`,
`message`, and `part` tables in chronological order, producing one
`ParsedSession` and a slice of `ParsedMessage` records.

#### Scenario: Session metadata
- **WHEN** a session row with `id = "ses_..."`, `title = "需求评分..."`,
  `directory = "D:\Temp\Download\TeleAgent"`, `time_created` and
  `time_updated` set, `parent_id IS NULL` is parsed
- **THEN** the `ParsedSession` SHALL have:
  - `ID` = `"teleagent:ses_..."`
  - `SessionName` = `"需求评分..."`
  - `Cwd` = `"D:\Temp\Download\TeleAgent"`
  - `StartedAt` derived from `time_created` (ms → UTC)
  - `EndedAt` derived from `time_updated` (ms → UTC)
  - `Agent` = `AgentTeleAgent`
  - `ParentSessionID` empty
  - `RelationshipType` = `RelNone`
  - `File.Path` = virtual source path
  - `File.Mtime` = `time_updated` in nanoseconds

#### Scenario: Subagent session metadata
- **WHEN** a session row with `parent_id` set to another session's id
  is parsed
- **THEN** the `ParsedSession` SHALL have:
  - `ParentSessionID` = `"teleagent:" + parent_id`
  - `RelationshipType` = `RelSubagent`

#### Scenario: User message parsing
- **WHEN** a `message` row with `data.role = "user"` and a child `part`
  row with `data.type = "text"` and `data.text = "根据这段Dockers..."`
  is parsed
- **THEN** a `ParsedMessage` SHALL be emitted with:
  - `Role` = `RoleUser`
  - `Content` = `"根据这段Dockers..."`
  - `Timestamp` derived from `message.data.time.created`
  - `Model` from `message.data.model.modelID` when present

#### Scenario: Assistant message with text and reasoning
- **WHEN** a `message` row with `data.role = "assistant"` has child
  parts of type `reasoning` then `text`
- **THEN** the emitted `ParsedMessage` SHALL have:
  - `Role` = `RoleAssistant`
  - `ThinkingText` = the `reasoning` part's `text`
  - `HasThinking` = `true`
  - `Content` = the `text` part's `text`
  - `Model` from `message.data.modelID`
  - `ProviderID` from `message.data.providerID`
  - `StopReason` from `message.data.finish` with `"tool-calls"`
    normalized to `"tool_calls"`

#### Scenario: Assistant message with tool call
- **WHEN** an assistant message has a `part` with `data.type = "tool"`,
  `data.tool = "write"`, `data.callID = "call_..."`,
  `data.state.status = "completed"`, `data.state.input = {filePath,
  content}`, `data.output = "..."`, `data.metadata.filepath =
  "D:\..."`
- **THEN** the emitted `ParsedMessage` SHALL have:
  - `HasToolUse` = `true`
  - `ToolCalls` containing one `ParsedToolCall` with:
    - `ToolUseID` = `"call_..."`
    - `ToolName` = `"write"`
    - `Category` = `"Write"` (via `NormalizeToolCategory`)
    - `InputJSON` = JSON-encoded `state.input`
    - `FilePath` = `"D:\..."` (from `metadata.filepath`)
    - `ResultEvents` containing one `ParsedToolResultEvent` with:
      - `ToolUseID` = `"call_..."`
      - `Status` = `"completed"`
      - `Content` = the `output` string

#### Scenario: Tool call with error state
- **WHEN** a `tool` part has `data.state.status = "error"` and
  `data.state.error = "Error: ..."`
- **THEN** the `ParsedToolResultEvent` SHALL have:
  - `Status` = `"error"`
  - `Content` = the `state.error` string (NOT `output`)

#### Scenario: Tool call with running state
- **WHEN** a `tool` part has `data.state.status = "running"`
- **THEN** the `ParsedToolResultEvent` SHALL have `Status = "running"`
  and the termination classifier SHALL NOT treat the call as resolved

#### Scenario: Multiple tool calls in one assistant turn
- **WHEN** an assistant message has two `tool` parts within one
  `step-start` / `step-finish` bracket
- **THEN** the emitted `ParsedMessage` SHALL have `ToolCalls` with two
  entries, each with its own `ResultEvents`

#### Scenario: Skipped part types
- **WHEN** parts of type `step-start`, `step-finish`, `compaction`, or
  `file` are encountered
- **THEN** the parser SHALL NOT emit any `ParsedMessage` content for
  them (they are metadata markers, not user-visible content)

#### Scenario: Token usage capture
- **WHEN** an assistant message has `data.tokens = {total, input,
  output, reasoning, cache: {read, write}}` and `data.cost = N`
- **THEN** the emitted `ParsedMessage` SHALL have:
  - `TokenUsage` populated with the tokens JSON
  - `ContextTokens` = `input + cache.read`
  - `OutputTokens` = `output`
  - `HasContextTokens` = `true`
  - `HasOutputTokens` = `true`
- **AND** the `ParsedSession` SHALL accumulate `TotalOutputTokens` and
  `PeakContextTokens` via `accumulateMessageTokenUsage`

#### Scenario: Empty session
- **WHEN** a session has zero messages or no parts with content
- **THEN** parse SHALL return `SkipNoSession` with `ForceReplace` so
  any stored rows for the vanished content are replaced

### Requirement: Cwd resolution

The provider SHALL use `session.directory` as the `ParsedSession.Cwd`,
ignoring `message.data.path.cwd`.

#### Scenario: Directory present
- **WHEN** a session row has `directory = "D:\Temp\Download\TeleAgent"`
- **THEN** `ParsedSession.Cwd` SHALL be `"D:\Temp\Download\TeleAgent"`

#### Scenario: Directory empty
- **WHEN** a session row has `directory = ""` (subagent-spawned
  sessions often have this)
- **THEN** `ParsedSession.Cwd` SHALL be empty and `Project` SHALL fall
  back to `"unknown"` (matching Kiro's behavior)

### Requirement: Tool name categorization

`NormalizeToolCategory` SHALL recognize TeleAgent's tool vocabulary and
map each name to a normalized category.

#### Scenario: Shell tools
- **WHEN** `NormalizeToolCategory` is called with `"powershell"` or
  `"bash"`
- **THEN** it SHALL return `"Bash"`

#### Scenario: File editing tools
- **WHEN** called with `"multiedit"`
- **THEN** it SHALL return `"Edit"`
- **AND** when called with `"read"`, `"write"`, `"edit"`, `"grep"`,
  `"glob"`, it SHALL return the existing categories (`"Read"`,
  `"Write"`, `"Edit"`, `"Grep"`, `"Glob"`)

#### Scenario: Subagent delegation tool
- **WHEN** called with `"task"`
- **THEN** it SHALL return `"Task"`

#### Scenario: TeleAgent-specific tools mapped to Tool
- **WHEN** called with one of `"todowrite"`, `"todoread"`,
  `"question"`, `"online_search"`, `"webfetch"`, `"ImageGen"`,
  `"image_understanding"`, `"report_final_files"`, `"memory_search"`,
  `"memory_get"`, `"skill"`, `"skill_view"`, `"skill_list"`,
  `"skill_manage"`, `"skill_evolution_resolve"`,
  `"teleai_claw_scan"`, `"session_status"`
- **THEN** it SHALL return `"Tool"`

#### Scenario: cua-driver prefix family
- **WHEN** called with a name starting with `"cua-driver_"` (e.g.
  `"cua-driver_click"`, `"cua-driver_press_key"`)
- **THEN** it SHALL return `"Tool"`

#### Scenario: playwright_browser prefix family
- **WHEN** called with a name starting with `"playwright_browser_"`
  (e.g. `"playwright_browser_navigate"`,
  `"playwright_browser_click"`)
- **THEN** it SHALL return `"Tool"`

#### Scenario: Unknown tool fallback
- **WHEN** called with a name not matching any TeleAgent mapping and
  not matching the prefix families
- **THEN** it SHALL return `"Other"` (the existing default)

### Requirement: Stop reason normalization

The parser SHALL normalize the `finish` field's dual spelling before
mapping to `ParsedMessage.StopReason`.

#### Scenario: tool_calls spelling
- **WHEN** `message.data.finish = "tool_calls"`
- **THEN** `ParsedMessage.StopReason` SHALL be `"tool_calls"`

#### Scenario: tool-calls spelling
- **WHEN** `message.data.finish = "tool-calls"`
- **THEN** `ParsedMessage.StopReason` SHALL be `"tool_calls"` (single
  canonical form)

#### Scenario: stop finish
- **WHEN** `message.data.finish = "stop"`
- **THEN** `ParsedMessage.StopReason` SHALL be `"stop"`
- **AND** the parser SHALL NOT treat `"stop"` as
  `TerminationAwaitingUser` in Phase 1 (deferred pending confirmation
  of TeleAgent's semantics)

#### Scenario: length and error finish
- **WHEN** `message.data.finish` is `"length"` or `"error"`
- **THEN** `ParsedMessage.StopReason` SHALL preserve the value as-is

#### Scenario: missing finish
- **WHEN** `message.data.finish` is null or absent
- **THEN** `ParsedMessage.StopReason` SHALL be empty

### Requirement: File watcher support

The provider SHALL declare a watch plan that observes the backing
SQLite database file (and its `users/` parent directory for
subdirectory-level changes).

#### Scenario: DB file watch
- **WHEN** `WatchPlan` is called for a configured root that resolves to
  a `users/` parent containing one user subdirectory with `teleagent.db`
- **THEN** the watch plan SHALL include a non-recursive `WatchRoot` at
  the resolved `teleagent.db` path

#### Scenario: Users parent watch
- **AND** the watch plan SHALL include a `WatchRoot` at the `users/`
  parent directory so that the appearance of a new user subdirectory
  is detected

#### Scenario: Changed DB triggers re-sync
- **WHEN** the watched `teleagent.db` file is modified (TeleAgent writes
  a new turn)
- **THEN** the watcher SHALL trigger a re-sync of the affected
  sessions via `SourcesForChangedPath`, which SHALL return the
  container source and the changed member sources

### Requirement: Sync engine dispatch

The sync engine SHALL handle TeleAgent sessions through the standard
Provider interface dispatch, without bespoke `syncTeleagent` methods.

#### Scenario: Provider-based discovery
- **WHEN** `SyncAll` runs and TeleAgent directories are configured
- **THEN** the engine SHALL construct a TeleAgent provider via
  `ProviderFactories` and invoke its `Discover` method, like every
  other migrated provider

#### Scenario: Provider-based parse
- **WHEN** a discovered TeleAgent source is processed
- **THEN** the engine SHALL invoke the provider's `Parse` method and
  persist the returned `ParseResult` records, like every other
  migrated provider

#### Scenario: No bespoke engine methods
- **WHEN** the engine processes a TeleAgent source
- **THEN** it SHALL NOT call any `syncTeleagent` / `syncOneTeleagent`
  / `countOneTeleagentSessions` methods (those are the legacy
  codefree-o pattern and not used for TeleAgent)

#### Scenario: Single session re-sync
- **WHEN** a session with prefix `"teleagent:"` triggers a
  single-session re-sync
- **THEN** the engine SHALL locate the source via the provider's
  `FindSource` and re-parse it

#### Scenario: Progress tracking
- **WHEN** TeleAgent sessions are being synced
- **THEN** they SHALL be counted in `progressTotal` and reported via
  `onProgress`

### Requirement: Frontend agent display

The web UI SHALL display TeleAgent sessions with appropriate labeling.

#### Scenario: Agent color and label
- **WHEN** a session with agent `"teleagent"` is displayed in the UI
- **THEN** it SHALL use a designated color and the label `"TeleAgent"`

#### Scenario: Session filter
- **WHEN** the user filters sessions by agent
- **THEN** `"teleagent"` SHALL appear as a filter option

### Requirement: Configuration

The system SHALL support configuring TeleAgent directories through
standard mechanisms.

#### Scenario: Default directory
- **WHEN** no explicit configuration is provided
- **THEN** the default directories SHALL be the platform-specific
  `TeleAgent/users` paths under `$HOME` (Windows
  `.local/share/TeleAgent/users`, macOS
  `Library/Application Support/TeleAgent/users` and
  `.local/share/TeleAgent/users`, Linux
  `.local/share/TeleAgent/users` and `.config/TeleAgent/users`)

#### Scenario: Environment variable override
- **WHEN** the `TELEAGENT_DIR` environment variable is set
- **THEN** it SHALL override the default directories for the TeleAgent
  agent

#### Scenario: Config file override
- **WHEN** `teleagent_dirs` is set in the TOML config file
- **THEN** it SHALL override the default directories (unless
  `TELEAGENT_DIR` is also set)

#### Scenario: Settings UI label
- **WHEN** the user opens the agent directory settings panel
- **THEN** TeleAgent SHALL appear in `AGENT_LABELS` as
  `teleagent: "TeleAgent"`
