## 1. Multi-path resolver

- [x] 1.1 Add `teleagentSQLiteDBPathsChecked(dir string) ([]string, error)`
  to `internal/parser/teleagent_sqlite.go` — returns all user DB paths
  under a configured root. Preserves the explicit-subdirectory override:
  if the root itself contains a `teleagent.db`, returns `[]string{thatPath}`
  without scanning subdirectories. Otherwise lists all subdirectories,
  sorts lexicographically, and returns every one that contains a
  `teleagent.db` via `teleagentDirectDBPathChecked`.
- [x] 1.2 Update `teleagentSQLiteDBPath` and `teleagentSQLiteDBPathChecked`
  in `internal/parser/teleagent_sqlite.go` to delegate to the new
  multi-path resolver and return the first path (or remove them if all
  callers are updated in task group 2). Keep `teleagentDirectDBPathChecked`
  unchanged — it is the per-directory check used by the new resolver.

## 2. Provider method updates

- [x] 2.1 Update `Discover` in `internal/parser/teleagent_provider.go`
  (line 307) — replace the single
  `dbPath, err := teleagentSQLiteDBPathChecked(root)` call with a loop
  over `teleagentSQLiteDBPathsChecked(root)`, calling `discoverRootDB`
  for each resolved DB path. Accumulate container and member sources
  across all DBs per root.
- [x] 2.2 Update `DiscoverEach` in `internal/parser/teleagent_provider.go`
  (line 338) — replace the single
  `dbPath, err := teleagentSQLiteDBPathChecked(root)` call with a loop
  over `teleagentSQLiteDBPathsChecked(root)`, opening each DB and
  streaming `ForEachSessionMeta` for each.
- [x] 2.3 Update `WatchPlan` in `internal/parser/teleagent_provider.go`
  (line 427) — replace the single
  `if dbPath := teleagentSQLiteDBPath(root)` check with a loop over all
  resolved DB paths, emitting one `WatchRoot` per DB (with the DB-file
  glob) plus the existing `users/` parent `WatchRoot` per root.
- [x] 2.4 Update `FindSource` in `internal/parser/teleagent_provider.go`
  (line 518) — in the raw-session-ID lookup loop (line 551), replace
  the single `teleagentSQLiteDBPathChecked(root)` call with a loop over
  `teleagentSQLiteDBPathsChecked(root)`, checking
  `TeleAgentSQLiteSessionExistsWithError` against each DB.

## 3. Provider unit tests

- [x] 3.1 Replace `TestTeleAgentProvider_FirstUserAutoDiscovery` in
  `internal/parser/teleagent_provider_test.go` (line 78) with
  `TestTeleAgentProvider_AllUsersDiscovered` — two user subdirectories
  (`v1_public_aaa`, `v1_public_bbb`) each with a `teleagent.db` and
  distinct sessions; assert `Discover` produces sources from BOTH
  user directories (both DBs' sessions appear, both container paths
  present).
- [x] 3.2 Update `TestTeleAgentProvider_WatchPlan` in
  `internal/parser/teleagent_provider_test.go` to use a root with two
  user subdirectories and assert the watch plan includes a `WatchRoot`
  for each resolved DB path (not just one).
- [x] 3.3 Verify `TestTeleAgentProvider_ExplicitSubdirectoryOverride`
  (line 102) still passes unchanged — a root pointing directly at a
  `users/<id>/` directory uses only that DB without scanning siblings.

## 4. Integration test

- [x] 4.1 Add `TestSyncTeleAgentSQLite_MultiUserSessionsDiscovered` to
  `internal/sync/teleagent_integration_test.go` — create two user
  subdirectories under `env.teleagentDir` (e.g. `v1_public_aaa`,
  `v1_public_bbb`), each with a `teleagent.db` containing distinct
  user sessions; after `SyncAll`, assert stored sessions from BOTH
  users are present in the archive.
- [x] 4.2 Verify existing `TestSyncTeleAgentSQLite_DisoversAndPersistsUserSessions`
  still passes — single-user fixture (`v1_public_testuser`) should
  still produce the same results.

## 5. Verification

- [x] 5.1 Run `go fmt ./...` and `go vet ./...` after all code changes
- [x] 5.2 Run `go test ./internal/parser/ -run TeleAgent -count=1` —
  all TeleAgent provider and SQLite tests pass
- [x] 5.3 Run `go test ./internal/sync/ -run TeleAgent -count=1` —
  all TeleAgent integration tests pass
