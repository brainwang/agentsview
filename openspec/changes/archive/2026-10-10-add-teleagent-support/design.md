## Context

TeleAgent is a local Electron desktop AI agent (TeleAI 星辰超级智能体)
that stores all session data in a per-user SQLite database at
`<data_dir>/users/<user_id>/teleagent.db`. The reference skill at
`ref/teleagent-task-share/` documents the export/import workflow and
references the underlying schema. A real archive on this machine
(`v1_public_1903020978903908355`, 246 sessions, 54MB) confirmed the
schema and surfaced the concrete data shapes the parser must handle.

agentsview already has 70+ agent providers. The closest precedents for
TeleAgent are:

| Aspect | Kiro | TeleAgent (new) |
|---|---|---|
| Storage | SQLite (`data.sqlite3`) + JSONL | SQLite only (`teleagent.db`) |
| Schema | 1 table, JSON blob per row | 3 normalized tables (session/message/part) |
| Tool calls | Separate `tool_result` message | Self-contained `tool` part (call + result) |
| Cwd | Embedded in JSON | `session.directory` column |
| Lineage | Implicit via cwd | Explicit `session.parent_id` → RelSubagent |
| Token usage | Per-message | Per-message + per-step (in `step-finish`) |
| System sessions | None | `_SYS_MEMORY_*` titles, must filter |

The Kiro provider (`kiro_sqlite.go` + `kiro_provider.go`) is the right
blueprint for the SQLite container architecture (read-only handle,
composite mtime fingerprint covering DB+WAL+SHM, virtual per-session
source paths, persistent-archive semantics that preserve stored rows
when the DB file vanishes). The RooCode/Codex `ResultEvents` pattern is
the right blueprint for self-contained tool parts (call + result packed
in one record, not split across user/assistant messages).

## Goals / Non-Goals

**Goals:**

- Register `teleagent` as a first-class agent type visible in the UI.
- Discover, parse, and archive all user sessions from the first user's
  `teleagent.db` (the explicit scope per the user's requirement).
- Filter `_SYS_MEMORY_DAILY_LOG_` and `_SYS_MEMORY_MERGE_` sessions so
  only real user work is surfaced (177 of 246 sessions in the sample
  are system maintenance noise).
- Capture per-message token usage (input, output, reasoning,
  cache.read, cache.write) and cost.
- Capture reasoning traces as `ThinkingText`.
- Capture tool calls with embedded results via the `ResultEvents`
  pattern (so termination classification can detect pending tool
  calls).
- Preserve subagent lineage via `ParentSessionID` + `RelSubagent` for
  the 13 `task`-tool-spawned sessions.
- Watch the SQLite DB file and re-sync on change.
- Auto-discover the first user subdirectory under a configured `users/`
  root, so out-of-the-box behavior matches "only need first user".
- Frontend displays TeleAgent sessions with a distinct color and label.

**Non-Goals:**

- Multi-user support (only the first `users/<id>/` subdirectory is
  read; the rest are ignored).
- LevelDB UI-title reconciliation (use `session.title` from the DB; a
  UI rename after creation will show the old name. Known limitation).
- `deleted-session-ids.json` soft-delete handling (the file does not
  exist for the sampled user; deferred until a user is observed to
  have one).
- `todo` table archiving (217 rows of in-session todo state; deferred).
- `file` part type (2 rows in the sample, user attachment references;
  deferred).
- Forward subagent linking from `task` tool call to child session ID
  (deferred — the child can find its parent via `parent_id`, which is
  sufficient for first-phase lineage).
- Incremental parse (full parse only for Phase 1, like Kiro SQLite).
- IM-channel system-prompt capture (15 messages carry a `system` field;
  treated as regular user messages per user direction).
- `step-finish` per-step token/cost rollup (message-level data is
  already complete; step-level data would double-count).

## Decisions

### D1: Provider architecture — follow Kiro, not codefree-o

**Choice:** Implement the full `Provider` interface with
`Discover`/`FindSource`/`Fingerprint`/`Parse`/`WatchPlan` methods, two
`SourceKind`s (`SQLiteDB` container + `SQLiteSession` virtual member),
and standard provider-factory registration.

**Rationale:** codefree-o uses a legacy dispatch pattern with bespoke
`syncCodefreeo` / `syncOneCodefreeo` methods and ~15 `case` branches in
`engine.go`. Kiro uses the modern Provider interface dispatch with only
narrow carve-outs for source-arbitration-across-roots. TeleAgent has no
JSONL fallback and no legacy layout, so it is simpler than Kiro and
needs no engine carve-outs beyond standard Provider dispatch.

**Alternatives considered:**
- codefree-o pattern: rejected — doubles the engine.go surface area and
  the codefree-o team has to mirror opencode parser changes.
- Generic SQLite container abstraction: rejected — premature; each
  SQLite-backed agent has different schemas and the abstraction would
  leak. Per-agent providers keep the schema knowledge in one file.

### D2: Two SourceKinds, like Kiro

**Choice:** `teleagentSourceSQLiteDB` (the container, lists all
sessions) and `teleagentSourceSQLiteSession` (one virtual source per
session, with `VirtualSourcePath(dbPath, sessionID)` as the
`DisplayPath`/`FingerprintKey`).

**Rationale:** mirrors Kiro exactly. The container source is what
discovery enumerates; the per-session virtual source is what
`Parse` operates on. `PersistentArchive` capability is set so a
vanished DB file preserves stored rows (only a vanished row triggers
`ForceReplace`).

### D3: Session filter — `title LIKE '_SYS_%' ESCAPE '\'`

**Choice:** skip any session whose `title` starts with `_SYS_` at both
discovery and parse time. Skip sessions with empty `title` too.

**Rationale:** the sample archive has 177 `_SYS_` sessions
(`_SYS_MEMORY_DAILY_LOG_` × 118, `_SYS_MEMORY_MERGE_` × 59) that are
background memory maintenance — no user-facing content, just internal
state. Filtering them keeps the UI focused on real work (69 of 246
sessions). The filter is purely prefix-based so future `_SYS_*` variants
are caught without code changes.

**Alternatives considered:**
- Filter by `parent_id IS NOT NULL`: rejected — 13 real USER sessions
  have `parent_id` set (subagent spawns) and must be kept.
- Filter by `directory = ''`: rejected — would also drop subagent
  sessions that legitimately have an empty directory.

### D4: Lineage — `parent_id` non-null → `RelSubagent`

**Choice:** for sessions that pass the `_SYS_` filter, set
`ParentSessionID = "teleagent:" + parent_id` and
`RelationshipType = RelSubagent` whenever `parent_id` is not null. No
heuristic to distinguish fork vs continuation (Claude/Codex need that;
TeleAgent does not).

**Rationale:** investigation of all 13 USER-with-parent sessions in the
sample showed every one is a `task`-tool subagent spawn — titles carry
`(@general subagent)` / `(@explore subagent)` / `(build)`, and the
`task` tool's `input.subagent_type` confirms the spawn. One child has a
dangling parent (`ses_skill_review_*` not in the session table); that
case is handled gracefully by agentsview (stored `ParentSessionID`
simply has no matching row, same as deleted-parent scenarios in other
providers).

### D5: Tool calls — embedded results via `ResultEvents`

**Choice:** for each `part.data.type == "tool"` part, emit one
`ParsedToolCall` with `ToolUseID = callID`, `ToolName = tool`,
`InputJSON = json(state.input)`, and one
`ParsedToolResultEvent{ToolUseID, Status: state.status,
Content: state.error or output}` appended to `tc.ResultEvents`.
`metadata.filepath` (when present) populates `ParsedToolCall.FilePath`.

**Rationale:** TeleAgent packs the tool call and its result in a single
`tool` part (unlike Claude/Kiro where `tool_result` is a separate user
message). The `ResultEvents` slice on `ParsedToolCall` is the canonical
place for embedded results — RooCode, Codex, Grok, and Copilot all use
this pattern. The termination classifier
(`hasOrphanedToolCall` in `termination.go`) already understands
`ResultEvents` with `Status != "running"` as resolution, so TeleAgent
sessions will classify correctly.

### D6: Tool-name taxonomy — prefix-family rules in the default clause

**Choice:** add exact-name mappings for the high-frequency tools
(`powershell`/`bash` → Bash, `read`/`write`/`edit`/`multiedit` →
known, `todowrite`/`todoread`/`question`/`online_search`/`webfetch`/
`image_understanding`/`ImageGen`/`report_final_files`/`memory_*`/
`skill*`/`session_status`/`teleai_claw_scan` → Tool, `task` → Task).
Add two prefix-family rules in the default clause:
`strings.HasPrefix(name, "cua-driver_")` → Tool and
`strings.HasPrefix(name, "playwright_browser_")` → Tool.

**Rationale:** enumerating all 17 `cua-driver_*` and 8
`playwright_browser_*` names would bloat `taxonomy.go`. The prefix
approach follows the existing `subagent` substring rule in the default
clause. The 47-name vocabulary is fixed by the TeleAgent client and
will not grow without a client release.

### D7: `finish` vocabulary normalization

**Choice:** normalize `message.data.finish` and `step-finish.reason`
`"tool-calls"` → `"tool_calls"` (single canonical form) before mapping
to `StopReason`. Map `"stop"` → `"stop"`, `"length"` → `"length"`,
`"error"` → `"error"`. Do NOT add `"stop"` to
`isAwaitingUserStopReason` in Phase 1 — TeleAgent's `"stop"` may or may
not mean "awaiting user" and the cost of a wrong "waiting" indicator is
worse than no indicator.

**Rationale:** the sample has both spellings in production
(`tool_calls` × 2384, `tool-calls` × 785), confirming a TeleAgent
client version transition. Normalizing avoids two code paths for the
same semantic.

### D8: Cwd — `session.directory`, not `message.data.path.cwd`

**Choice:** use `session.directory` as `ParsedSession.Cwd`. Ignore
`message.data.path.cwd` (observed empty in 833 of 903 assistant
messages; the 70 non-empty ones duplicate `session.directory`).

**Rationale:** `session.directory` is the authoritative per-session
working directory. `message.data.path.cwd` is per-message and
inconsistently populated — using it would produce a flaky Cwd.

### D9: Auto-discover first user subdirectory

**Choice:** configured roots point at the `users/` parent directory
(defaults: `~/.local/share/TeleAgent/users/`,
`~/Library/Application Support/TeleAgent/users/`, etc.). At discovery
time, the provider lists subdirectories of the configured root, picks
the first that contains a `teleagent.db` file, and uses that as the
backing DB. If multiple user subdirectories exist, only the first is
read.

**Rationale:** the user's explicit requirement is "only need to support
the first user". Auto-discovery makes out-of-the-box configuration
work (no need to manually find and configure the `v1_public_*`
subdirectory). The `users/` parent is stable across machines; the
`<user_id>` subdirectory is not.

**Alternatives considered:**
- Configure roots at the specific `users/<id>/` directory: rejected —
  fails out-of-the-box because `<id>` is not predictable.
- Configure roots at the `users/` parent and enumerate all users:
  rejected — explicitly out of scope per the user's requirement.

### D10: IM-channel messages treated as regular user messages

**Choice:** the 15 user messages carrying a `data.system` field (IM
channel system-prompt overrides) and `data.tools` field (per-tool
disable flags) are parsed as regular `RoleUser` messages with
`IsSystem = false`. The `system` and `tools` fields are ignored.

**Rationale:** per user direction. These are still user-initiated
turns, not system-injected notices, so `IsSystem = false` is correct.
Ignoring `system`/`tools` keeps the parser focused on user-visible
content.

## Risks / Trade-offs

- **[Schema drift]** TeleAgent is a shipping product; its schema may
  change in future client releases. The parser reads only the three
  core tables (`session`/`message`/`part`) and uses `json_extract` for
  JSON fields, so additive schema changes (new columns, new
  `part.data.type` values) will not break parsing — unknown part types
  are skipped. Breaking changes (renamed columns, restructured JSON)
  would require a parser update. → acceptable; the parser is isolated
  to `teleagent*.go` and the format is documented in
  `ref/teleagent-task-share/references/db-schema.md` for future
  reference.

- **[First-user assumption]** auto-discovery picks the first
  `users/<id>/` subdirectory lexicographically. If a machine has
  multiple users and the user cares about a non-first one, they must
  configure the specific subdirectory as the root. → documented in
  the spec; acceptable for Phase 1.

- **[_SYS_ filter is prefix-based]** a future TeleAgent release could
  introduce user-facing sessions whose titles happen to start with
  `_SYS_`. → unlikely given the `_SYS_MEMORY_*` naming convention is
  deliberate; if it happens, the filter can be tightened to the two
  known patterns.

- **[No incremental parse]** every sync re-parses the full session
  from the DB. For the sample archive (69 user sessions, max 1085
  parts in one session), this is fast. For much larger archives,
  incremental parse (like Codex's checkpoint seed) would be needed.
  → deferred to a future change; Kiro SQLite is also full-parse-only
  and works fine.

- **[Tool vocabulary hardcoded]** the 47 tool names are baked into
  `taxonomy.go`. New tools added by TeleAgent client releases will
  fall through to the `default` clause and be categorized as `"Other"`
  until the parser is updated. → acceptable; the `default → Other`
  fallback is graceful and the vocabulary changes only with client
  releases.

- **[No forward subagent linking]** the `task` tool call in the parent
  session is not linked forward to the child session ID (only the
  child → parent link via `parent_id` exists). UI inline-subagent
  rendering that depends on the forward link will not work for
  TeleAgent in Phase 1. → deferred; the parent → child link requires
  matching `task` tool `callID`s to child session IDs, which TeleAgent
  does not expose in the sampled data (task tool `output` is empty).
  The child → parent link is sufficient for grouping.
