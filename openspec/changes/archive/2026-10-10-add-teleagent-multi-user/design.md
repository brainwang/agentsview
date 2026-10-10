## Context

The `add-teleagent-support` change (in-flight, 41/42 tasks done) introduced a
TeleAgent provider modeled on Kiro's SQLite container architecture. Its design
D9 deliberately limited discovery to the lexicographically first
`users/<id>/` subdirectory, encoding this as a spec requirement and a
non-goal ("Multi-user support").

The provider architecture is already multi-DB-capable at the source-identity
layer: virtual source paths are `dbPath#sessionID`, so each user's DB is a
distinct container with globally unique member paths. The single bottleneck is
`teleagentSQLiteDBPathChecked` (`teleagent_sqlite.go:305`), which returns one
path. Every method that calls it — `Discover`, `DiscoverEach`, `WatchPlan`,
`FindSource`, `SourcesForChangedPath`, `StoredSourceHintScopes` — processes
exactly one DB per root.

See proposal.md for why this limitation must be lifted.

## Goals / Non-Goals

**Goals:**

- Read all user subdirectories under a configured `users/` root, not just the
  first.
- Preserve the explicit-subdirectory override: a root pointing at a specific
  `users/<id>/` directory uses only that DB.
- Keep session IDs non-scoped (`teleagent:<rawSessionID>`), matching Kiro's
  multi-root precedent.

**Non-Goals:**

- User-scoped session IDs (`teleagent:<userID>:<rawID>`) — rejected; raw IDs
  are high-entropy and collision across users is astronomically unlikely.
  User-scoping would also complicate `ParentSessionID` linking (parent_id is
  raw, not user-scoped).
- Per-user configuration (selecting which users to read) — all users under a
  root are read; filtering is the user's job via root configuration.
- LevelDB UI-title reconciliation or `deleted-session-ids.json` handling —
  unchanged from the original `add-teleagent-support` non-goals.

## Decisions

### D1: Multi-path resolver replaces single-path resolver

**Choice:** Replace `teleagentSQLiteDBPathChecked` (returns one `string`)
with `teleagentSQLiteDBPathsChecked` (returns `[]string`). The new function
preserves the explicit-subdirectory override: if the root itself contains a
`teleagent.db`, it returns `[]string{thatPath}` without scanning
subdirectories. Otherwise it lists all subdirectories, sorts them, and
returns every one that contains a `teleagent.db`.

**Rationale:** minimal blast radius. Every provider method that currently
calls the single-path resolver and processes one DB gains a `for _, dbPath :=
range dbPaths` loop. The per-DB methods (`discoverRootDB`, `parseSQLiteDB`,
`parseSQLiteSession`, `Fingerprint`, etc.) do not change — they already take
a single `dbPath` parameter.

**Alternatives considered:**
- Enumerate subdirectories lazily in `Discover`/`DiscoverEach` only: rejected
  — `WatchPlan` and `FindSource` also need the full DB list, so centralizing
  the resolver keeps the logic in one place.
- Store the resolved DB list on the `teleagentSourceSet` struct at
  construction time: rejected — roots can change between construction and
  discovery (new subdirectory appears), and the resolver is cheap (one
  `ReadDir` + stat per subdir).

### D2: Non-scoped session IDs — `teleagent:<rawID>`

**Choice:** session IDs remain `teleagent:<rawSessionID>` with no user ID
embedded, identical to the single-user implementation. `ParentSessionID`
remains `teleagent:<parent_id>` (raw parent_id).

**Rationale:** the user's explicit decision. TeleAgent session IDs are
high-entropy (timestamps + random suffixes observed in the real archive).
Cross-user collision is astronomically unlikely. Kiro uses the same
non-scoped pattern (`kiro:<rawID>`) across multiple roots and DBs without
issue. User-scoping would require a migration of existing stored rows and
would break `ParentSessionID` resolution (the `parent_id` column stores a
raw ID, not a user-scoped ID, so constructing `teleagent:<userID>:<parent_id>`
would require a reverse lookup to find the parent's user).

**Alternatives considered:**
- `teleagent:<userID>:<rawID>`: rejected per user decision — migration cost,
  ParentSessionID complexity, no practical benefit given ID entropy.

### D3: Watch plan watches all resolved DBs

**Choice:** `WatchPlan` iterates over all resolved DB paths per root and
emits one `WatchRoot` per DB (with the DB-file glob), plus one `WatchRoot`
for the `users/` parent (non-recursive, no glob) so new user subdirectories
trigger a re-scan.

**Rationale:** the existing `WatchPlan` already has this two-level structure
(parent watch + DB watch); it just emits one DB watch. The fix is a loop. The
parent watch is unchanged — it already detects new subdirectory creation;
after detection, the next `WatchPlan` call resolves the new DB and adds its
watch.

### D4: Explicit-subdirectory override unchanged

**Choice:** when the configured root directly contains a `teleagent.db`
(the explicit-override case), the multi-path resolver returns exactly that
one path and does not scan subdirectories. This preserves the existing
`TestTeleAgentProvider_ExplicitSubdirectoryOverride` test's behavior.

**Rationale:** the override case is for users who point the root at a specific
`users/<id>/` directory. Scanning siblings in that case would be surprising
and could pick up unrelated DBs.

## Risks / Trade-offs

- **[Session ID collision across users]** two users with the same raw
  session ID would produce the same archive row (`teleagent:<rawID>`), and
  the second sync would overwrite the first. → mitigated by ID entropy
  (high-entropy timestamps + random); accepted per user decision. If a
  collision is ever observed, user-scoped IDs can be added as a follow-up
  change with a migration.

- **[Discovery cost scales with user count]** each sync resolves all user DBs
  per root (one `ReadDir` + stat per subdir + one `ListSessionMeta` per DB).
  For a machine with N users, this is N DB opens per sync. → acceptable;
  TeleAgent is a desktop agent with typically 1-3 users per machine, and
  `ListSessionMeta` is a single SQL query per DB.

- **[Watch plan grows with user count]** each resolved DB adds one
  `WatchRoot`. With many users, the watcher tracks more file descriptors.
  → acceptable for desktop-scale user counts (1-3 typically); the watcher
  already handles multi-root providers like Kiro.

- **[Interaction with in-flight `add-teleagent-support` change]** the
  original change's D9, non-goal, spec requirement, and test all encode
  "first user only." This change's spec delta supersedes those. If both
  changes are archived, the delta operations compose correctly (REMOVE the
  first-user requirement, ADD the multi-user requirement). Implementation
  should land after `add-teleagent-support` is complete (41/42 tasks done).
