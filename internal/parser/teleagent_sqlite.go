package parser

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const teleagentSQLiteDBName = "teleagent.db"

// TeleAgentSQLiteSessionMeta is lightweight per-session metadata used to
// detect changed TeleAgent SQLite sessions without parsing the three-table
// schema on every sync.
type TeleAgentSQLiteSessionMeta struct {
	SessionID   string
	VirtualPath string
	FileMtime   int64
}

// TeleAgentSQLiteStore keeps one read-only SQLite handle open while a caller
// performs multiple TeleAgent session lookups from the same backing DB.
type TeleAgentSQLiteStore struct {
	dbPath string
	db     *sql.DB
}

// OpenTeleAgentSQLiteStore opens a read-only TeleAgent SQLite DB. It rejects
// symlinked DB paths so a malicious root cannot redirect reads outside the
// configured tree.
func OpenTeleAgentSQLiteStore(dbPath string) (*TeleAgentSQLiteStore, error) {
	info, err := os.Lstat(dbPath)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("teleagent sqlite db not found: %s", dbPath)
	}
	if err != nil {
		return nil, fmt.Errorf("stat teleagent sqlite db %s: %w", dbPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("refusing symlinked teleagent sqlite db: %s", dbPath)
	}
	db, err := openTeleAgentSQLiteDB(dbPath)
	if err != nil {
		return nil, err
	}
	return &TeleAgentSQLiteStore{dbPath: dbPath, db: db}, nil
}

// Close releases the underlying SQLite handle.
func (s *TeleAgentSQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// TeleAgentSQLiteVirtualPath gives each session inside the shared TeleAgent
// DB a stable source identity for the agentsview archive.
func TeleAgentSQLiteVirtualPath(dbPath, sessionID string) string {
	return VirtualSourcePath(dbPath, sessionID)
}

// teleagentSQLiteVirtualPathParts splits a virtual TeleAgent SQLite source
// path back into its database path and raw session ID.
func teleagentSQLiteVirtualPathParts(path string) (string, string, bool) {
	return ParseVirtualSourcePathForBase(path, teleagentSQLiteDBName)
}

// TeleAgentSQLiteSessionExists reports whether the backing DB has a row for
// sessionID that passes the _SYS_ filter.
func TeleAgentSQLiteSessionExists(dbPath, sessionID string) bool {
	exists, _ := TeleAgentSQLiteSessionExistsWithError(dbPath, sessionID)
	return exists
}

// TeleAgentSQLiteSessionExistsWithError reports whether the backing DB has a
// row for sessionID that passes the _SYS_ filter, returning any error.
func TeleAgentSQLiteSessionExistsWithError(
	dbPath, sessionID string,
) (bool, error) {
	if dbPath == "" || sessionID == "" {
		return false, nil
	}
	store, err := OpenTeleAgentSQLiteStore(dbPath)
	if err != nil {
		return false, err
	}
	defer store.Close()
	return store.SessionExistsWithError(sessionID)
}

// TeleAgentSQLiteSessionMetaForID returns metadata for one logical session
// without enumerating the rest of the database.
func TeleAgentSQLiteSessionMetaForID(
	dbPath, sessionID string,
) (TeleAgentSQLiteSessionMeta, bool, error) {
	if dbPath == "" || sessionID == "" {
		return TeleAgentSQLiteSessionMeta{}, false, nil
	}
	store, err := OpenTeleAgentSQLiteStore(dbPath)
	if err != nil {
		return TeleAgentSQLiteSessionMeta{}, false, err
	}
	defer store.Close()
	return store.SessionMetaForID(sessionID)
}

// ListTeleAgentSQLiteSessionMeta returns one metadata row per logical user
// session (the _SYS_ system-maintenance sessions are filtered at the SQL
// level).
func ListTeleAgentSQLiteSessionMeta(
	dbPath string,
) ([]TeleAgentSQLiteSessionMeta, error) {
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil, nil
	}
	store, err := OpenTeleAgentSQLiteStore(dbPath)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	return store.ListSessionMeta()
}

// ListSessionMeta returns one metadata row per logical user session using the
// store's existing SQLite handle.
func (s *TeleAgentSQLiteStore) ListSessionMeta() ([]TeleAgentSQLiteSessionMeta, error) {
	var metas []TeleAgentSQLiteSessionMeta
	err := s.ForEachSessionMeta(context.Background(), func(meta TeleAgentSQLiteSessionMeta) error {
		metas = append(metas, meta)
		return nil
	})
	return metas, err
}

// ForEachSessionMeta streams one metadata row per logical user session using
// the store's existing SQLite handle. _SYS_ system-maintenance sessions and
// sessions with an empty title are filtered at the SQL level; rows are ordered
// by time_updated descending so the most recently active sessions surface
// first.
func (s *TeleAgentSQLiteStore) ForEachSessionMeta(
	ctx context.Context, yield func(TeleAgentSQLiteSessionMeta) error,
) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("teleagent sqlite store is closed")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, time_updated
		  FROM session
		 WHERE title NOT LIKE '_SYS\_%' ESCAPE '\'
		   AND title != ''
		 ORDER BY time_updated DESC
	`)
	if err != nil {
		return fmt.Errorf("listing teleagent sqlite sessions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		var updatedAt int64
		if err := rows.Scan(&id, &updatedAt); err != nil {
			return fmt.Errorf("scanning teleagent sqlite session meta: %w", err)
		}
		if id == "" {
			continue
		}
		observeStreamingDiscoveryBuffer(ctx, 1)
		if err := yield(TeleAgentSQLiteSessionMeta{
			SessionID:   id,
			VirtualPath: TeleAgentSQLiteVirtualPath(s.dbPath, id),
			FileMtime:   updatedAt * 1_000_000,
		}); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return nil
}

// SessionExistsWithError reports whether the backing DB has a row for
// sessionID that passes the _SYS_ filter.
func (s *TeleAgentSQLiteStore) SessionExistsWithError(
	sessionID string,
) (bool, error) {
	if s == nil || s.db == nil || sessionID == "" {
		return false, nil
	}
	var found int
	err := s.db.QueryRow(
		`SELECT 1
		   FROM session
		  WHERE id = ?
		    AND title NOT LIKE '_SYS\_%' ESCAPE '\'
		    AND title != ''
		  LIMIT 1`,
		sessionID,
	).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// SessionMetaForID returns metadata for one logical session using the store's
// existing SQLite handle.
func (s *TeleAgentSQLiteStore) SessionMetaForID(
	sessionID string,
) (TeleAgentSQLiteSessionMeta, bool, error) {
	if s == nil || s.db == nil {
		return TeleAgentSQLiteSessionMeta{}, false, fmt.Errorf("teleagent sqlite store is closed")
	}
	var id string
	var updatedAt int64
	err := s.db.QueryRow(
		`SELECT id, time_updated
		   FROM session
		  WHERE id = ?
		    AND title NOT LIKE '_SYS\_%' ESCAPE '\'
		    AND title != ''
		  LIMIT 1`,
		sessionID,
	).Scan(&id, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return TeleAgentSQLiteSessionMeta{}, false, nil
	}
	if err != nil {
		return TeleAgentSQLiteSessionMeta{}, false, fmt.Errorf(
			"finding teleagent sqlite session meta: %w", err,
		)
	}
	return TeleAgentSQLiteSessionMeta{
		SessionID:   id,
		VirtualPath: TeleAgentSQLiteVirtualPath(s.dbPath, id),
		FileMtime:   updatedAt * 1_000_000,
	}, true, nil
}

// LoadSession loads one TeleAgent session by joining the session, message,
// and part tables in chronological order, producing one ParsedSession and a
// slice of ParsedMessage records.
func (s *TeleAgentSQLiteStore) LoadSession(
	ctx context.Context, sessionID, machine string,
) (*ParsedSession, []ParsedMessage, error) {
	if s == nil || s.db == nil {
		return nil, nil, fmt.Errorf("teleagent sqlite store is closed")
	}
	return loadTeleAgentSession(ctx, s.db, s.dbPath, sessionID, machine)
}

// teleagentSQLiteSessionFingerprint is the per-session freshness identity:
// session.time_updated (the row's own mtime in ms, converted to ns) plus the
// sum of length(part.data) across every part of the session (a content-size
// signal that changes when any part is added, edited, or removed). Returns
// (0, 0, sql.ErrNoRows) when the session row is absent or filtered by the
// _SYS_ filter so the provider can force-replace the stored rows.
func teleagentSQLiteSessionFingerprint(
	db *sql.DB, sessionID string,
) (timeUpdatedNS, partSize int64, err error) {
	var updatedAt int64
	err = db.QueryRow(
		`SELECT time_updated
		   FROM session
		  WHERE id = ?
		    AND title NOT LIKE '_SYS\_%' ESCAPE '\'
		    AND title != ''
		  LIMIT 1`,
		sessionID,
	).Scan(&updatedAt)
	if err != nil {
		return 0, 0, err
	}
	err = db.QueryRow(
		`SELECT COALESCE(SUM(LENGTH(data)), 0)
		   FROM part
		  WHERE session_id = ?`,
		sessionID,
	).Scan(&partSize)
	if err != nil {
		return 0, 0, err
	}
	return updatedAt * 1_000_000, partSize, nil
}

// teleagentSQLiteDBPath returns the TeleAgent DB path when the configured root
// contains one (either as a direct child for the explicit-subdirectory
// override, or under the first user subdirectory of a users/ parent).
func teleagentSQLiteDBPath(dir string) string {
	path, _ := teleagentSQLiteDBPathChecked(dir)
	return path
}

// teleagentSQLiteDBPathChecked resolves the TeleAgent DB path under a
// configured root. The root may either point directly at a directory
// containing a teleagent.db (the explicit-subdirectory override case), or at
// the users/ parent — in which case the first subdirectory (lexicographically)
// that contains a teleagent.db is selected, matching the "only need first
// user" requirement. Returns ("", nil) when no candidate is found.
func teleagentSQLiteDBPathChecked(dir string) (string, error) {
	if dir == "" {
		return "", nil
	}
	// Explicit-subdirectory override: the directory itself contains a
	// teleagent.db.
	if path := teleagentDirectDBPathChecked(dir); path != "" {
		return path, nil
	}
	// users/ parent: pick the first subdirectory (lexicographically) that
	// contains a teleagent.db.
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read teleagent users dir %s: %w", dir, err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		subdir := filepath.Join(dir, name)
		if path := teleagentDirectDBPathChecked(subdir); path != "" {
			return path, nil
		}
	}
	return "", nil
}

// teleagentDirectDBPathChecked returns the teleagent.db path when dir itself
// contains a regular (non-symlinked) teleagent.db that resolves under dir.
// The symlink safety check uses filepath.EvalSymlinks when available; on
// platforms where EvalSymlinks fails (observed on some Windows machines),
// it falls back to a raw-path relUnder check so discovery still works
// while still rejecting paths that escape the configured root.
func teleagentDirectDBPathChecked(dir string) string {
	if dir == "" {
		return ""
	}
	path := filepath.Join(dir, teleagentSQLiteDBName)
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return ""
	}
	resolvedDir, dirErr := filepath.EvalSymlinks(dir)
	if dirErr != nil {
		resolvedDir = dir
	}
	resolvedPath, pathErr := filepath.EvalSymlinks(path)
	if pathErr != nil {
		resolvedPath = path
	}
	if _, ok := relUnder(resolvedDir, resolvedPath); !ok {
		return ""
	}
	return path
}

// teleagentDBUnderRoot validates that the resolved DB path stays under the
// configured root via symlink resolution, mirroring kiroDBUnderRoot. The DB
// must sit at <root>/teleagent.db, <root>/<subdir>/teleagent.db, or deeper
// (the users/ parent case). requireRegular additionally requires the DB file
// to exist as a regular file. On platforms where filepath.EvalSymlinks fails
// (observed on some Windows machines), the check falls back to raw-path
// relUnder so discovery still works while still rejecting paths that escape
// the configured root.
func teleagentDBUnderRoot(root, dbPath string, requireRegular bool) bool {
	root = filepath.Clean(root)
	dbPath = filepath.Clean(dbPath)
	rel, ok := relUnder(root, dbPath)
	if !ok {
		return false
	}
	// The DB file must be named teleagent.db and must sit at least one
	// level under root (the users/ parent case: <root>/<subdir>/teleagent.db
	// or deeper) or directly under root (the explicit-subdirectory override
	// case: <root>/teleagent.db).
	if filepath.ToSlash(filepath.Base(rel)) != teleagentSQLiteDBName {
		return false
	}
	resolvedRoot, rootErr := filepath.EvalSymlinks(root)
	if rootErr != nil {
		resolvedRoot = root
	}
	resolvedDB, dbErr := filepath.EvalSymlinks(dbPath)
	if dbErr != nil {
		if requireRegular {
			// EvalSymlinks failed and we need a regular file; fall back to
			// a raw stat so a present DB file still validates when the
			// platform cannot resolve symlinks.
			return IsRegularFile(dbPath) && rootUnderRaw(resolvedRoot, dbPath)
		}
		_, statRootErr := os.Stat(root)
		return statRootErr == nil
	}
	if _, ok := relUnder(resolvedRoot, resolvedDB); !ok {
		return false
	}
	return !requireRegular || IsRegularFile(dbPath)
}

// rootUnderRaw reports whether path is root or sits under root using a
// raw filepath.Rel comparison (no symlink resolution). Used as a fallback
// when filepath.EvalSymlinks is unavailable.
func rootUnderRaw(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != "." && rel != ".." &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func openTeleAgentSQLiteDB(dbPath string) (*sql.DB, error) {
	db, err := openSQLiteReadOnly(dbPath, sqliteReadOptions{busyTimeoutMS: 3000})
	if err != nil {
		return nil, fmt.Errorf(
			"opening teleagent sqlite db %s: %w", dbPath, err,
		)
	}
	return db, nil
}
