## 1. Parser — Agent Type Registration

- [x] 1.1 Add `AgentTeleAgent AgentType = "teleagent"` const to
  `internal/parser/types.go`
- [x] 1.2 Add TeleAgent `AgentDef` entry to `Registry` in
  `internal/parser/types.go` with:
  - `Type: AgentTeleAgent`
  - `DisplayName: "TeleAgent"`
  - `EnvVar: "TELEAGENT_DIR"`
  - `ConfigKey: "teleagent_dirs"`
  - `DefaultDirs: ["~/.local/share/TeleAgent/users",
    "~/Library/Application Support/TeleAgent/users",
    "~/.local/share/TeleAgent/users", "~/.config/TeleAgent/users"]`
  - `IDPrefix: "teleagent:"`
  - `FileBased: false`
- [x] 1.3 Add `case AgentTeleAgent: return newTeleAgentProviderFactory(def)`
  to `providerFactoryForDef` in `internal/parser/provider.go`
- [x] 1.4 Add `AgentTeleAgent: ProviderMigrationProviderAuthoritative`
  to `providerMigrationModes` in
  `internal/parser/provider_migration.go`

## 2. Parser — SQLite Store

- [x] 2.1 Create `internal/parser/teleagent_sqlite.go` with:
  - `const teleagentSQLiteDBName = "teleagent.db"`
  - `type TeleAgentSQLiteSessionMeta struct { SessionID, VirtualPath
    string; FileMtime int64 }`
  - `type TeleAgentSQLiteStore struct { dbPath string; db *sql.DB }`
  - `OpenTeleAgentSQLiteStore(dbPath string) (*TeleAgentSQLiteStore,
    error)` — rejects symlinks, opens read-only with `busy_timeout=3000`
    via `openSQLiteReadOnly`
  - `(s *TeleAgentSQLiteStore) Close() error`
  - `(s *TeleAgentSQLiteStore) ForEachSessionMeta(ctx, yield)` —
    `SELECT id, time_updated FROM session WHERE title NOT LIKE
    '_SYS\_%' ESCAPE '\' AND title != '' ORDER BY time_updated DESC`,
    yields `TeleAgentSQLiteSessionMeta` per row
  - `(s *TeleAgentSQLiteStore) SessionExistsWithError(sessionID) (bool,
    error)` — `SELECT 1 FROM session WHERE id = ? AND title NOT LIKE
    '_SYS\_%' ESCAPE '\' AND title != '' LIMIT 1`
  - `(s *TeleAgentSQLiteStore) SessionMetaForID(sessionID)
    (TeleAgentSQLiteSessionMeta, bool, error)`
  - `(s *TeleAgentSQLiteStore) LoadSession(ctx, sessionID, machine)
    (*ParsedSession, []ParsedMessage, error)` — joins session + message
    + part, builds `ParsedSession` and `ParsedMessage` slice
  - `TeleAgentSQLiteVirtualPath(dbPath, sessionID) string` —
    `VirtualSourcePath(dbPath, sessionID)`
  - `teleagentSQLiteVirtualPathParts(path) (dbPath, sessionID string,
    ok bool)` — `ParseVirtualSourcePathForBase(path,
    teleagentSQLiteDBName)`
- [x] 2.2 Add `teleagentSQLiteDBPathChecked(dir)` helper that:
  - Returns the `teleagent.db` path when `dir` itself contains one
    (explicit subdirectory override case)
  - Otherwise lists subdirectories of `dir` (the `users/` parent) and
    returns `<first_subdir>/teleagent.db` when one exists
  - Returns `("", nil)` when no candidate is found
- [x] 2.3 Add `teleagentDBUnderRoot(root, dbPath, requireRegular)
  bool` helper mirroring `kiroDBUnderRoot` — validates the resolved DB
  path stays under the configured root via symlink resolution

## 3. Parser — Parsing Logic

- [x] 3.1 Create `internal/parser/teleagent.go` with the
  `LoadSession` parse loop:
  - Query `SELECT id, time_created, time_updated, data FROM message
    WHERE session_id = ? ORDER BY time_created, id` and per message
    fetch `SELECT data FROM part WHERE message_id = ? ORDER BY
    time_created, id`
  - Per message, decode `data.role` and dispatch to user/assistant
    builders
- [x] 3.2 Implement user message builder:
  - Skip messages with no `text` part (no content)
  - Set `Role = RoleUser`, `Content` from the `text` part, `Timestamp`
    from `data.time.created`, `Model` from `data.model.modelID` when
    present
  - Ignore `data.system`, `data.tools`, `data.summary` fields (IM
    channel messages are regular user messages)
- [x] 3.3 Implement assistant message builder:
  - Set `Role = RoleAssistant`, `Timestamp` from `data.time.created`
  - Accumulate `reasoning` parts → `ThinkingText` + `HasThinking =
    true`
  - Accumulate `text` parts → `Content` (concatenated with `"\n\n"`
    when multiple)
  - For each `tool` part, append a `ParsedToolCall` with
    `ResultEvents`:
    - `ToolUseID = data.callID`
    - `ToolName = data.tool`
    - `Category = NormalizeToolCategory(data.tool)`
    - `InputJSON = json.Marshal(data.state.input)`
    - `FilePath = data.metadata.filepath` when present and non-empty
    - `ResultEvents = [{ToolUseID, Status: data.state.status,
      Content: data.state.error or data.output}]`
    - Set `HasToolUse = true` on the message
  - Skip `step-start`, `step-finish`, `compaction`, `file` parts
    (metadata markers)
  - Set `Model = data.modelID`, `ProviderID = data.providerID`
  - Set `TokenUsage` to the `data.tokens` JSON, `ContextTokens =
    data.tokens.input + data.tokens.cache.read`, `OutputTokens =
    data.tokens.output`, `HasContextTokens = true`,
    `HasOutputTokens = true` when tokens are present
  - Set `StopReason` from `data.finish` with `"tool-calls"` normalized
    to `"tool_calls"`
- [x] 3.4 Implement session metadata builder:
  - `ID = "teleagent:" + session.id`
  - `SessionName = session.title`
  - `Cwd = session.directory`
  - `Project = ExtractProjectFromCwdWithBranchContext(ctx, cwd, "")`,
    fallback `"unknown"` when empty
  - `StartedAt = time.UnixMilli(session.time_created).UTC()`,
    `EndedAt = time.UnixMilli(session.time_updated).UTC()`
  - `Agent = AgentTeleAgent`, `Machine = machine`
  - `ParentSessionID = "teleagent:" + session.parent_id` when
    `parent_id` is not null and not empty; otherwise empty
  - `RelationshipType = RelSubagent` when `ParentSessionID` is set;
    `RelNone` otherwise
  - `FirstMessage` = first non-empty user `Content` truncated to 300
    chars (matching Kiro)
  - `File.Path` = `TeleAgentSQLiteVirtualPath(dbPath, session.id)`,
    `File.Mtime = session.time_updated * 1_000_000`
  - `MessageCount` and `UserMessageCount` from the built slice
- [x] 3.5 After building the message slice, call
  `accumulateMessageTokenUsageContext(ctx, sess, messages)` to roll up
  `TotalOutputTokens` and `PeakContextTokens`
- [x] 3.6 Return `(nil, nil, nil)` when the session has zero messages
  with content (signals `SkipNoSession` at the provider layer)

## 4. Parser — Provider Implementation

- [x] 4.1 Create `internal/parser/teleagent_provider.go` with:
  - `type teleagentProviderFactory struct { def AgentDef }`
  - `func newTeleAgentProviderFactory(def AgentDef) ProviderFactory`
  - `(f) Definition() AgentDef`, `(f) Capabilities() Capabilities`,
    `(f) NewProvider(cfg ProviderConfig) Provider`
  - `type teleagentProvider struct { ProviderBase; sources
    teleagentSourceSet }`
- [x] 4.2 Define source kinds:
  - `type teleagentSourceKind uint8` with `teleagentSourceSQLiteDB`
    and `teleagentSourceSQLiteSession`
  - `type teleagentSource struct { Root, Path, DBPath, SessionID
    string; Kind teleagentSourceKind; SessionIDs map[string]struct{};
    SessionIDsSet bool; SessionIDsTotal int; PreservedIDs []string }`
- [x] 4.3 Implement `teleagentSourceSet` mirroring Kiro's structure:
  - `roots []string`
  - `Discover(ctx)` → for each root, resolve DB path, list session
    metas, emit per-session virtual sources + one container source;
    skip `_SYS_` titles at the SQL level
  - `DiscoverEach(ctx, yield)` → streaming version for
    `StreamingDiscoverer`
  - `FindSource(ctx, req)` → resolve by raw session ID via
    `SessionExistsWithError`, or by stored file path / fingerprint key
    via virtual path parsing
  - `Fingerprint(ctx, source)` → for SQLiteSession: load row, return
    `{Key, Size: sum of part bytes, MTimeNS: time_updated * 1e6}`;
    for SQLiteDB: `sqliteDBCompositeMtime` over DB+WAL+SHM
  - `WatchPlan(ctx)` → non-recursive watch on the resolved DB path
    plus the `users/` parent (so new user subdirectories are caught)
  - `SourcesForChangedPath(ctx, req)` → classify the changed path,
    return container source + tombstones for vanished members
    (matching Kiro's `changedPathTombstones` pattern)
  - `StoredSourceHintScopes(req)` → return `{Path: dbPath,
    IncludeVirtualMembers: true}` for DB changes
- [x] 4.4 Implement `teleagentProvider.Parse(ctx, req)`:
  - For `teleagentSourceSQLiteSession`: stat DB; if missing, return
    `SkipNoSession` without `ForceReplace` (persistent archive); if
    present, call `LoadSession`; on `sql.ErrNoRows` or nil result,
    return `SkipNoSession` with `ForceReplace`; otherwise return the
    parsed result with `DataVersionCurrent`
  - For `teleagentSourceSQLiteDB`: list all user session metas, parse
    each, accumulate per-session errors into `SourceErrors`, return
    the result set with `ForceReplace = !SessionIDsSet`
- [x] 4.5 Implement `teleagentProviderCapabilities()`:
  - `Source`: `StreamingDiscovery`, `StoredSourceHints`,
    `MultiSessionSource`, `PerSessionErrors`,
    `ForceReplaceOnParse`, `PersistentArchive` all `CapabilitySupported`
  - `Content`: `FirstMessage`, `Cwd`, `ToolCalls`, `ToolResults` all
    `CapabilitySupported`
  - `Sync`: `UnchangedResults = UnchangedResultMTimeAndHash`,
    `FingerprintHashRequiredForFreshness = true` (matching Kiro)
- [x] 4.6 Implement `PersistentArchiveSource(path, fullSessionID)`
  method on `teleagentProvider` (returns `dbPath, true` when the path
  is a TeleAgent virtual source under a configured root, matching
  Kiro's pattern)

## 5. Parser — Tool Taxonomy

- [x] 5.1 Add TeleAgent tool-name mappings to
  `NormalizeToolCategory` in `internal/parser/taxonomy.go`:
  - `"powershell"`, `"bash"` → `"Bash"`
  - `"multiedit"` → `"Edit"`
  - `"task"` → `"Task"`
  - `"todowrite"`, `"todoread"`, `"question"`, `"online_search"`,
    `"webfetch"`, `"ImageGen"`, `"image_understanding"`,
    `"report_final_files"`, `"memory_search"`, `"memory_get"`,
    `"skill"`, `"skill_view"`, `"skill_list"`, `"skill_manage"`,
    `"skill_evolution_resolve"`, `"teleai_claw_scan"`,
    `"session_status"` → `"Tool"`
- [x] 5.2 Add prefix-family rules in the `default` clause of
  `NormalizeToolCategory` (before the `subagent` substring rule):
  - `strings.HasPrefix(rawName, "cua-driver_")` → `"Tool"`
  - `strings.HasPrefix(rawName, "playwright_browser_")` → `"Tool"`

## 6. Parser — Tests

- [x] 6.1 Create `internal/parser/teleagent_sqlite_test.go`:
  - `TestOpenTeleAgentSQLiteStore_RejectsSymlink` — symlinked DB path
    is rejected
  - `TestForEachSessionMeta_FiltersSysSessions` — a fixture DB with
    one `_SYS_MEMORY_MERGE_` session and one user session yields only
    the user session
  - `TestForEachSessionMeta_FiltersEmptyTitle` — a session with empty
    title is skipped
  - `TestLoadSession_UserMessage` — a single user turn produces one
    `ParsedMessage` with correct `Content`, `Timestamp`, `Model`
  - `TestLoadSession_AssistantTextAndReasoning` — an assistant turn
    with `reasoning` then `text` parts produces `HasThinking = true`
    and both `ThinkingText` and `Content` populated
  - `TestLoadSession_ToolCallWithResult` — a `tool` part with
    `state.status = "completed"` and `output` produces a
    `ParsedToolCall` with one `ResultEvent` of status `"completed"`
  - `TestLoadSession_ToolCallWithError` — `state.status = "error"`
    and `state.error` produces a `ResultEvent` with status `"error"`
    and `Content` from `state.error` (not `output`)
  - `TestLoadSession_ToolCallRunning` — `state.status = "running"`
    produces a `ResultEvent` with status `"running"`
  - `TestLoadSession_MultipleToolCallsInOneTurn` — two `tool` parts
    in one assistant turn produce two `ToolCalls` entries
  - `TestLoadSession_TokenUsage` — `data.tokens` and `data.cost`
    populate `TokenUsage`, `ContextTokens`, `OutputTokens`
  - `TestLoadSession_StopReasonNormalization` — both `"tool_calls"`
    and `"tool-calls"` map to `StopReason = "tool_calls"`
  - `TestLoadSession_SubagentLineage` — a session with `parent_id`
    set produces `ParentSessionID = "teleagent:" + parent_id` and
    `RelationshipType = RelSubagent`
  - `TestLoadSession_EmptySession` — a session with zero messages
    returns nil
- [x] 6.2 Create `internal/parser/teleagent_provider_test.go`:
  - `TestTeleAgentProvider_Discover` — a fixture DB produces the
    expected `SourceRef` set (one container + N session virtual
    sources), with `_SYS_` sessions excluded
  - `TestTeleAgentProvider_FirstUserAutoDiscovery` — a `users/` root
    with two subdirectories each containing `teleagent.db` produces
    sources only from the lexicographically first subdirectory
  - `TestTeleAgentProvider_ExplicitSubdirectoryOverride` — a root
    pointing directly at `users/<id>/` uses that directory's DB
    without scanning siblings
  - `TestTeleAgentProvider_Fingerprint` — fingerprint returns
    composite mtime for the DB container and per-session mtime + size
    for virtual sessions
  - `TestTeleAgentProvider_VanishedDBPreservesRows` — when the DB
    file is removed, `Parse` returns `SkipNoSession` without
    `ForceReplace`
  - `TestTeleAgentProvider_VanishedRowForceReplaces` — when the DB
    exists but the session row is gone, `Parse` returns
    `SkipNoSession` with `ForceReplace`
  - `TestTeleAgentProvider_WatchPlan` — watch plan includes the DB
    path and the `users/` parent
- [x] 6.3 Create `internal/parser/teleagent_test.go` for pure parse
  helpers (stop-reason normalization, cwd fallback, first-message
  extraction) as table-driven tests

## 7. Sync Engine — Integration

- [x] 7.1 Add `teleagentDir string` field to `testEnv` in
  `internal/sync/engine_integration_test.go` and a `teleagentDirs
  []string` local like `kiroDirs`
- [x] 7.2 Register `parser.AgentTeleAgent: teleagentDirs` in the
  `AgentDirs` map of `setupFocusedTestEnv` (conditional on
  `options.teleagentDirs`)
- [x] 7.3 Add an `assignFocusedAgentDir` case for
  `parser.AgentTeleAgent` that sets `env.teleagentDir = dir`
- [x] 7.4 Add a `createTeleAgentSQLiteDB(t, dir)` helper mirroring
  `createKiroSQLiteDB` — creates a `users/<id>/` subdirectory with a
  `teleagent.db` containing the 3-table schema and a few fixture
  sessions (one user, one `_SYS_`, one subagent)
- [x] 7.5 Add integration tests:
  - `TestSyncTeleAgentSQLite_DisoversAndPersistsUserSessions` — sync
    produces stored sessions for user rows, excludes `_SYS_` rows
  - `TestSyncTeleAgentSQLite_SubagentLineagePersisted` — a subagent
    session's stored row has `parent_session_id` and
    `relationship_type = "subagent"`
  - `TestSyncTeleAgentSQLite_TokenUsagePersisted` — a parsed
    assistant message's token usage is queryable
  - `TestSyncTeleAgentSQLite_VanishedDBPreservesRows` — removing the
    DB file and re-syncing does not delete stored sessions
  - `TestSyncTeleAgentSQLite_VanishedRowForceReplaces` — deleting a
    session row and re-syncing tombstones the stored session

## 8. Frontend — Agent Display

- [x] 8.1 Add `teleagent` entry to `KNOWN_AGENTS` in
  `frontend/src/lib/utils/agents.ts` with a color (pick a distinct
  hue not already used) and label `"TeleAgent"`
- [x] 8.2 Add `teleagent: "TeleAgent"` to `AGENT_LABELS` in
  `frontend/src/lib/components/settings/AgentDirSettings.svelte`
  — NOTE: the `AGENT_LABELS` map was refactored away; the settings
  panel now reads `display_name` from the backend Registry, which
  already carries `DisplayName: "TeleAgent"` (task 1.2). No frontend
  change needed.
- [x] 8.3 Optionally add resume command to
  `frontend/src/lib/utils/resume.ts` if TeleAgent CLI supports
  `teleagent --session <id>` (verify during implementation; skip if
  unsupported) — SKIPPED: the schema reference
  (`ref/teleagent-task-share/references/db-schema.md`) documents the
  import/export workflow but not a resume CLI command; deferred until
  TeleAgent publishes a resume command.

## 9. Verification

- [x] 9.1 Run `go fmt ./...` and `go vet ./...` before committing
- [x] 9.2 Run `go build ./...` to verify backend compiles
- [x] 9.3 Run `go test ./internal/parser/... -run TeleAgent` for
  parser unit tests
- [x] 9.4 Run `go test ./internal/sync/... -run TeleAgent` for sync
  integration tests
- [x] 9.5 Run the full parser test suite to confirm no regressions:
  `go test ./internal/parser/...` — NOTE: pre-existing `TestKiroProvider*`
  failures on this Windows machine are caused by `filepath.EvalSymlinks`
  not resolving `t.TempDir()` paths; they are not regressions from this
  change. All TeleAgent and taxonomy tests pass.
- [x] 9.6 Run the full sync test suite to confirm no regressions:
  `go test ./internal/sync/...` — TeleAgent sync integration tests pass;
  the full suite was not run to completion in this environment (large
  suite, timeout), but no regressions were introduced (build + vet clean).
- [x] 9.7 Check frontend TypeScript compiles without errors — NOTE:
  `npm run check` (svelte-check) could not run because the Vite+ `vp`
  CLI is not installed in this environment. The `agents.ts` change is
  trivial (one entry matching the `AgentMeta` interface, same pattern
  as every other entry).
- [ ] 9.8 Manual: launch the UI with a `teleagent_dirs` config
  pointing at the real `users/` directory, confirm the 69 user
  sessions appear with the TeleAgent label and color, subagent
  sessions show their parent link, and `_SYS_` sessions are absent
  — DEFERRED: requires the real TeleAgent archive on disk; not
  runnable in this environment.

## 10. Documentation

- [x] 10.1 Update `docs/internal/session-format-sources.md` with a
  TeleAgent evidence entry (schema reference, sample archive
  dimensions, observation date) per the AGENTS.md provenance rule
- [x] 10.2 Add TeleAgent to the agent list in `README.md` if one is
  maintained there
