## REMOVED Requirements

### Requirement: First-user auto-discovery

**Reason**: Replaced by "Multi-user discovery" requirement. The first-user-only
limitation silently drops every other user's sessions when a machine has
multiple TeleAgent user accounts.

**Migration**: No data migration needed. The provider's virtual source paths
(`dbPath#sessionID`) are already globally unique per DB, so stored rows from
previously-discovered first-user sessions keep their identity. On the next sync
after this change, additional user sessions appear as new rows; no existing
rows change.

## ADDED Requirements

### Requirement: Multi-user discovery

The provider SHALL discover and read all user subdirectories under a
configured `users/` root. Every subdirectory that contains a `teleagent.db`
file SHALL be read, not just the first.

#### Scenario: Single user directory
- **WHEN** a configured root is `~/.local/share/TeleAgent/users/` and
  contains one subdirectory `v1_public_<id>/` with a `teleagent.db`
- **THEN** the provider SHALL use
  `~/.local/share/TeleAgent/users/v1_public_<id>/teleagent.db` as the
  backing DB for all discovery and parse operations

#### Scenario: Multiple user directories
- **WHEN** a configured root contains multiple subdirectories each with a
  `teleagent.db`
- **THEN** the provider SHALL read every subdirectory's `teleagent.db` and
  SHALL discover sessions from all of them

#### Scenario: No user subdirectory
- **WHEN** a configured root contains no subdirectory with a `teleagent.db`
- **THEN** discovery SHALL return no sources and SHALL NOT error

#### Scenario: Explicit subdirectory override
- **WHEN** the configured root points directly at a specific
  `users/<id>/` directory (not the `users/` parent)
- **THEN** the provider SHALL detect the `teleagent.db` in that directory
  and use it, without scanning siblings

#### Scenario: Session identity across users
- **WHEN** two user subdirectories each contain a session with the same raw
  session ID
- **THEN** each session SHALL be stored with ID `teleagent:<rawSessionID>`
  (non-scoped — no user ID embedded), and the provider relies on raw
  session IDs being globally unique across users

## MODIFIED Requirements

### Requirement: File watcher support

The provider SHALL declare a watch plan that observes every backing SQLite
database file resolved under each configured root (and the `users/` parent
directory for subdirectory-level changes).

#### Scenario: DB file watch
- **WHEN** `WatchPlan` is called for a configured root that resolves to a
  `users/` parent containing user subdirectories each with `teleagent.db`
- **THEN** the watch plan SHALL include a non-recursive `WatchRoot` at
  every resolved `teleagent.db` path

#### Scenario: Users parent watch
- **AND** the watch plan SHALL include a `WatchRoot` at the `users/` parent
  directory so that the appearance of a new user subdirectory is detected

#### Scenario: Changed DB triggers re-sync
- **WHEN** any watched `teleagent.db` file is modified (TeleAgent writes a
  new turn)
- **THEN** the watcher SHALL trigger a re-sync of the affected sessions via
  `SourcesForChangedPath`, which SHALL return the container source and the
  changed member sources for that DB

#### Scenario: New user subdirectory detected
- **WHEN** a new user subdirectory with a `teleagent.db` appears under the
  watched `users/` parent
- **THEN** the watcher SHALL trigger a re-scan that discovers the new DB
  and its sessions on the next sync cycle
