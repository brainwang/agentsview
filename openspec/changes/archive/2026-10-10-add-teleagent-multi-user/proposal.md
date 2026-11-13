## Why

The `add-teleagent-support` change (in-flight, 41/42 tasks done) deliberately
limits discovery to the lexicographically first `users/<id>/` subdirectory
(design D9, spec requirement "First-user auto-discovery", non-goal "Multi-user
support"). TeleAgent is a multi-user desktop agent: a single machine can host
several user accounts under `users/`, each with its own `teleagent.db`. The
first-user limit silently drops every other user's sessions, so agentsview
shows an incomplete picture whenever more than one user exists.

The provider architecture is already multi-DB-capable — virtual source paths
are `dbPath#sessionID`, so each user's DB is a distinct, globally unique
container. The only bottleneck is `teleagentSQLiteDBPathChecked`, which returns
one path. Removing that bottleneck is a small, well-contained change.

## What Changes

- Replace `teleagentSQLiteDBPathChecked` (returns one DB path) with a new
  function that returns **all** user DB paths under a configured root.
- Update `Discover`, `DiscoverEach`, `WatchPlan`, `FindSource`,
  `SourcesForChangedPath`, and `StoredSourceHintScopes` to loop over all
  resolved DBs per root instead of the single first one.
- Keep session IDs non-scoped: `teleagent:<rawSessionID>` (no user ID
  embedded), matching Kiro's multi-root precedent and assuming raw session
  IDs are globally unique across users (high-entropy timestamps + random).
- Replace the spec requirement "First-user auto-discovery" with "Multi-user
  discovery" — all user subdirectories are read, not just the first.
- Replace `TestTeleAgentProvider_FirstUserAutoDiscovery` with
  `TestTeleAgentProvider_AllUsersDiscovered`, asserting every user
  subdirectory's DB is read.
- Preserve the explicit-subdirectory override: a root pointing directly at a
  `users/<id>/` directory still uses only that directory's DB (no sibling
  scan), unchanged.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `teleagent-agent`: The "First-user auto-discovery" requirement is replaced
  by a "Multi-user discovery" requirement. The provider SHALL read all user
  subdirectories under a configured `users/` root, not just the first. Session
  identity remains non-scoped (`teleagent:<rawID>`).

## Impact

- `internal/parser/teleagent_sqlite.go` — replace
  `teleagentSQLiteDBPathChecked` with a multi-path resolver; add a
  `teleagentSQLiteDBPathsChecked` helper returning `[]string`.
- `internal/parser/teleagent_provider.go` — update `Discover`,
  `DiscoverEach`, `WatchPlan`, `FindSource`, `SourcesForChangedPath`, and
  `StoredSourceHintScopes` to iterate over all resolved DBs per root.
- `internal/parser/teleagent_provider_test.go` — replace
  `TestTeleAgentProvider_FirstUserAutoDiscovery` with
  `TestTeleAgentProvider_AllUsersDiscovered`; update
  `TestTeleAgentProvider_WatchPlan` to assert all DBs are watched.
- `internal/sync/teleagent_integration_test.go` — add or update a test
  covering multi-user discovery (two user subdirectories, both produce stored
  sessions).
- `openspec/changes/add-teleagent-support/specs/teleagent-agent/spec.md` —
  not edited (that change is in-flight); the delta is captured in this
  change's spec delta.
- No frontend, config, or taxonomy changes — the `users/` root defaults,
  `TELEAGENT_DIR` env var, and `teleagent_dirs` config key remain unchanged.
