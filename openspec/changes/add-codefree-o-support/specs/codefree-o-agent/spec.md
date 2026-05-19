# codefree-o-agent

## ADDED Requirements

### Requirement: Agent type registration
The system SHALL register `codefree-o` as a supported agent type in the parser registry with the type identifier `"codefree-o"`.

#### Scenario: Agent constant exists
- **WHEN** the parser package is initialized
- **THEN** `AgentCodefreeO` constant SHALL be defined with value `"codefree-o"`

#### Scenario: Registry entry exists
- **WHEN** the agent registry is queried by type `AgentCodefreeO`
- **THEN** the returned `AgentDef` SHALL have:
  - `DisplayName` set to `"Codefree-O"`
  - `EnvVar` set to `"CODEFREE_O_DIR"`
  - `ConfigKey` set to `"codefree_o_dirs"`
  - `DefaultDirs` containing `~/.codefree-o`
  - `IDPrefix` set to `"codefree-o:"`
  - `FileBased` set to `true`
  - `DiscoverFunc` non-nil
  - `FindSourceFunc` non-nil

#### Scenario: ID prefix matching
- **WHEN** a session ID starts with `"codefree-o:"`
- **THEN** `AgentByPrefix` SHALL return the codefree-o agent definition

### Requirement: Session file discovery
The system SHALL discover codefree-o session files from configured directories.

#### Scenario: File-backed storage discovery
- **WHEN** scanning a codefree-o root directory that contains `.local/share/storage/session/<project>/<session>.json`
- **THEN** those JSON files SHALL be discovered with agent type `AgentCodefreeO`

#### Scenario: SQLite database discovery
- **WHEN** scanning a codefree-o root directory that contains `.local/share/codefree.db`
- **THEN** sessions in that database SHALL be discovered and synchronized with agent type `AgentCodefreeO`

#### Scenario: Source file location
- **WHEN** `FindSourceFunc` is called with a codefree-o root and a raw session ID
- **THEN** it SHALL locate the session file in file-backed storage, or return the SQLite virtual path if stored in the database

### Requirement: Session parsing
The system SHALL parse codefree-o session files using the same parsing logic as opencode.

#### Scenario: File-backed session parsing
- **WHEN** a codefree-o file-backed session JSON is processed
- **THEN** the result SHALL contain the same `ParsedSession` and `ParsedMessage` data as if it were an opencode session with identical file content

#### Scenario: SQLite session parsing
- **WHEN** a codefree-o SQLite database session is processed
- **THEN** the system SHALL read from `<root>/.local/share/codefree.db` (not `<root>/opencode.db`)
- **THEN** the parsed session SHALL use the same SQLite schema as opencode

### Requirement: Sync engine dispatch
The sync engine SHALL handle codefree-o sessions in all relevant dispatch points.

#### Scenario: File-based sync processing
- **WHEN** a `DiscoveredFile` with agent `AgentCodefreeO` is processed
- **THEN** the engine SHALL route it to a dedicated processing path that delegates to opencode's parser functions

#### Scenario: DB-backed sync
- **WHEN** `SyncAll` runs and codefree-o directories are configured
- **THEN** codefree-o SQLite databases SHALL be synchronized in the DB-backed phase, after opencode and before Warp

#### Scenario: Single session re-sync
- **WHEN** a session with prefix `"codefree-o:"` triggers a single-session re-sync
- **THEN** the engine SHALL locate the source file and re-parse it

#### Scenario: Progress tracking
- **WHEN** codefree-o sessions are being synced
- **THEN** they SHALL be counted in `progressTotal` and reported via `onProgress`

### Requirement: File watcher support
The file watcher SHALL detect changes to codefree-o session files and trigger appropriate re-sync.

#### Scenario: Storage file change
- **WHEN** a JSON file under a codefree-o root's `.local/share/storage/session/` is modified
- **THEN** the watcher SHALL map the change to the corresponding session and trigger re-sync

#### Scenario: SQLite database change
- **WHEN** the `.local/share/codefree.db` file is modified
- **THEN** the watcher SHALL detect the change and trigger a DB-backed re-sync of affected sessions

### Requirement: Frontend agent display
The web UI SHALL display codefree-o sessions with appropriate labeling.

#### Scenario: Agent color and label
- **WHEN** a session with agent `"codefree-o"` is displayed in the UI
- **THEN** it SHALL use a designated color and the label `"Codefree-O"`

#### Scenario: Session filter
- **WHEN** the user filters sessions by agent
- **THEN** "codefree-o" SHALL appear as a filter option

### Requirement: Session resume
The system SHALL support generating a resume command for codefree-o sessions.

#### Scenario: Resume command
- **WHEN** the user clicks "Resume" on a codefree-o session
- **THEN** the generated command SHALL be `codefree-o --session <session-id>`

#### Scenario: Resume support flag
- **WHEN** checking if codefree-o supports resume
- **THEN** `supportsResume("codefree-o")` SHALL return `true`

### Requirement: Configuration
The system SHALL support configuring codefree-o directories through standard mechanisms.

#### Scenario: Default directory
- **WHEN** no explicit configuration is provided
- **THEN** the default directory SHALL be `~/.codefree-o` (resolved relative to the user's home directory)

#### Scenario: Environment variable override
- **WHEN** `CODEFREE_O_DIR` environment variable is set
- **THEN** it SHALL override the default directory for codefree-o agent

#### Scenario: Config file override
- **WHEN** `codefree_o_dirs` is set in the TOML config file
- **THEN** it SHALL override the default directory for codefree-o agent (unless env var is also set)

### Requirement: Source mtime
The system SHALL compute effective source mtime for codefree-o sessions.

#### Scenario: SQLite virtual path mtime
- **WHEN** computing mtime for a codefree-o SQLite virtual path (`codefree.db#sessionID`)
- **THEN** the system SHALL use `OpenCodeSourceMtime` to derive the mtime from the storage files (same logic as opencode)
