## 1. Parser — Agent Type Registration

- [x] 1.1 Add `AgentCodefreeO AgentType = "codefree-o"` const to `internal/parser/types.go`
- [x] 1.2 Add codefree-o `AgentDef` entry to `Registry` in `internal/parser/types.go` with DisplayName, EnvVar, ConfigKey, DefaultDirs, IDPrefix, FileBased, WatchSubdirs

## 2. Parser — Source Discovery & Path Resolution

- [x] 2.1 Create `internal/parser/codefreeo.go` with `ResolveCodefreeOSource(root)` function (detect file vs SQLite mode)
- [x] 2.2 Implement `DiscoverCodefreeOSessions(root)` to scan `.local/share/storage/session/<project>/<session>.json`
- [x] 2.3 Implement `FindCodefreeOSourceFile(root, id)` to locate source by session ID
- [x] 2.4 Implement `ResolveCodefreeOWatchRoots(root)` for watcher directory setup
- [x] 2.5 Add SQLite virtual path helpers (`ParseCodefreeOSQLiteVirtualPath`, `CodefreeOSourceMtime`) mirroring opencode's

## 3. Sync Engine — DB-Backed Sync

- [x] 3.1 Add `codefreeoPendingSessionIDs(dir)` to list pending sessions from `codefree.db`
- [x] 3.2 Add `syncOneCodefreeo(ctx, dir)` to sync sessions from one codefree-o directory
- [x] 3.3 Add `syncCodefreeo(ctx)` to iterate over all configured codefree-o directories
- [x] 3.4 Add `countOneCodefreeoSessions(dir)` for progress tracking
- [x] 3.5 Add `syncSingleCodefreeo(sessionID)` for single-session re-sync

## 4. Sync Engine — Dispatch Integration

- [x] 4.1 Add codefree-o sync block in `syncAllLocked` (after Piebald block, before `LinkSubagentSessions`)
- [x] 4.2 Add `case parser.AgentCodefreeO` to `countDBBackedSessions`
- [x] 4.3 Add `case parser.AgentCodefreeO` to `countDBBackedProgressTotal`
- [x] 4.4 Add `case parser.AgentCodefreeO` to `SyncSingleSession` switch
- [x] 4.5 Add `case parser.AgentCodefreeO` to `processDiscoveredFile` switch
- [x] 4.6 Add `AgentCodefreeO` condition to `shouldCacheSkip`
- [x] 4.7 Add `AgentCodefreeO` condition to `discoveredFileMtime`
- [x] 4.8 Add `classifyCodefreeOPath` and dispatch from `classifyOnePath`

## 5. Frontend — Agent Display

- [x] 5.1 Add codefree-o entry to `KNOWN_AGENTS` in `frontend/src/lib/utils/agents.ts` with color and label `"Codefree-O"`
- [x] 5.2 Add resume command to `RESUME_AGENTS` in `frontend/src/lib/utils/resume.ts`: `codefree-o --session <id>`

## 6. Frontend — Settings

- [x] 6.1 Add `codefree-o: "Codefree-O"` entry to `AGENT_LABELS` in `frontend/src/lib/components/settings/AgentDirSettings.svelte`

## 7. Verification

- [x] 7.1 Run `go build ./...` to verify backend compiles
- [x] 7.2 Check frontend TypeScript compiles without errors
- [ ] 7.3 Test UI renders codefree-o sessions with correct label and color (manual)
- [ ] 7.4 Verify resume command generates `codefree-o --session <id>` (manual)

## 8. Build & Test

- [x] 8.1 Build to make sure no compile error
- [x] 8.2 Unit Test to make sure all cases pass
