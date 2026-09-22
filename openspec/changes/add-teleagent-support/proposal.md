## Why

TeleAgent (TeleAI 星辰超级智能体) is a local Electron desktop agent with a
multi-user SQLite archive at `~/.local/share/TeleAgent/users/<user_id>/teleagent.db`.
The database stores every session's turns across three normalized tables
(`session`, `message`, `part`) with rich per-message token usage, embedded
tool-call + result parts, reasoning traces, and subagent-spawn lineage via
`session.parent_id`. agentsview currently cannot read any of this data.

Investigation of the user's real archive (`v1_public_1903020978903908355`,
246 sessions / 4233 messages / 14527 parts / 54MB) confirmed the schema
matches the format reference under `ref/teleagent-task-share/` and surfaced
the concrete shapes needed to write the parser: the `part.data.type` enum
(`text`, `reasoning`, `tool`, `step-start`, `step-finish`, `compaction`,
`file`), the self-contained `tool` part structure (`callID` + `tool` +
`state.{status,input,error}` + `output` + `metadata.filepath`), the
`message.data` token/cost block, and the `_SYS_MEMORY_*` session filter
that excludes 177 of 246 rows as system maintenance noise.

## What Changes

- Register `teleagent` as a new agent type with a SQLite-backed provider
  modeled on Kiro's provider architecture.
- Add three new parser files: `teleagent.go` (parse logic),
  `teleagent_sqlite.go` (read-only SQLite store over the 3-table schema),
  `teleagent_provider.go` (Provider interface implementation with
  discovery, fingerprint, watch plan, find-source, and parse dispatch).
- Filter `_SYS_*` sessions at discovery and parse time so only user work
  surfaces (56 top-level + 13 subagent-spawned = 69 real sessions in the
  sample archive).
- Map `session.parent_id` to `ParentSessionID` + `RelSubagent` (TeleAgent
  uses `parent_id` exclusively for `task`-tool subagent spawns; no fork or
  continuation ambiguity like Claude/Codex).
- Map `message.data.tokens` (input/output/reasoning/cache.read/cache.write)
  and `cost` to `ParsedMessage.{TokenUsage, ContextTokens, OutputTokens}`
  and session rollups.
- Map `part.data` of type `text` → Content, `reasoning` → ThinkingText,
  `tool` → `ParsedToolCall` with embedded `ResultEvents` (RooCode/Codex
  pattern, since TeleAgent packs call + result in a single part).
- Add TeleAgent tool-name categories to `taxonomy.go` for the 47 distinct
  tool names observed (powershell/bash → Bash, read/write/edit/multiedit
  → known categories, cua-driver_* and playwright_browser_* → Tool via
  prefix-family rules, task → Task).
- Normalize the `finish` field's dual spelling
  (`"tool_calls"` / `"tool-calls"`) to a single canonical form before
  mapping to `StopReason`.
- Auto-discover the first user subdirectory under a configured `users/`
  root, so out-of-the-box behavior matches the "only need to support the
  first user" requirement.
- Frontend: add TeleAgent agent color, label, and (optionally) resume
  command.
- Configuration: add `TELEAGENT_DIR` env var and `teleagent_dirs` TOML
  key pointing at the `users/` parent directory.

## Capabilities

### New Capabilities

- `teleagent-agent`: Register TeleAgent as a first-class agent type in
  agentsview — SQLite-backed session discovery, multi-table parsing with
  embedded tool results, `_SYS_` session filtering, subagent lineage,
  per-message token usage, file watching, frontend display, and
  configuration.

### Modified Capabilities

None.

## Impact

- `internal/parser/types.go` — new `AgentTeleAgent` const and `Registry`
  entry (FileBased=false, custom DefaultDirs pointing at `users/` parent).
- `internal/parser/teleagent.go` — new file, parsing logic for the
  3-table session/message/part schema.
- `internal/parser/teleagent_sqlite.go` — new file, read-only SQLite
  store with per-session meta listing, row loading, and
  `VirtualSourcePath` integration.
- `internal/parser/teleagent_provider.go` — new file, Provider interface
  implementation (Discover/FindSource/Fingerprint/Parse/WatchPlan),
  modeled on `kiro_provider.go` with two SourceKinds
  (`teleagentSourceSQLiteDB` and `teleagentSourceSQLiteSession`).
- `internal/parser/teleagent_test.go` — new file, parser unit tests.
- `internal/parser/teleagent_provider_test.go` — new file, provider
  discovery/fingerprint tests.
- `internal/parser/teleagent_sqlite_test.go` — new file, SQLite store
  tests.
- `internal/parser/provider.go` — add `case AgentTeleAgent:` to
  `providerFactoryForDef`.
- `internal/parser/provider_migration.go` — add
  `AgentTeleAgent: ProviderMigrationProviderAuthoritative`.
- `internal/parser/taxonomy.go` — add TeleAgent tool-name mappings
  (exact names + `cua-driver_*` and `playwright_browser_*` prefix-family
  rules in the default clause).
- `internal/sync/engine.go` — minimal: TeleAgent flows through the
  standard Provider interface dispatch (no bespoke `syncTeleagent`
  methods, unlike the legacy codefree-o pattern). Possible narrow
  carve-outs for `directory`-like behaviors will follow the Kiro
  precedent if needed during implementation.
- `internal/sync/engine_integration_test.go` — add `teleagentDir` field
  to `testEnv`, register `parser.AgentTeleAgent` in
  `setupFocusedTestEnv`, mirror Kiro's SQLite fixture helper.
- `frontend/src/lib/utils/agents.ts` — add `teleagent` entry to
  `KNOWN_AGENTS` with a color and label `"TeleAgent"`.
- `frontend/src/lib/utils/resume.ts` — optionally add
  `teleagent --session <id>` if TeleAgent CLI supports resume (verify
  during implementation).
- `frontend/src/lib/components/settings/AgentDirSettings.svelte` — add
  `teleagent: "TeleAgent"` to `AGENT_LABELS`.
