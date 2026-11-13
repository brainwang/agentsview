package parser

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tidwall/gjson"
)

const (
	codefreeSQLiteDBName = "session.db"
	codefreeCatalogDir   = "__catalog__"
	codefreeCatalogKey   = "codefree.catalog.v1"
	codefreeStateKey     = "codefree.state.v1"
)

// CodeFreeCatalogEntry is lightweight per-session metadata extracted from
// the __catalog__/session.db index.
type CodeFreeCatalogEntry struct {
	SessionID       string
	StartTime       int64 // millisecond timestamp
	ModifiedTime    int64 // millisecond timestamp
	Summary         string
	WorkingDir      string // file:/// URI
	DefaultAgentURI string // codefree-agent://builtin/spec
}

// CodeFreeSQLiteStore keeps one read-only SQLite handle open while a
// caller performs CodeFree session lookups from the same backing DB.
type CodeFreeSQLiteStore struct {
	dbPath string
	db     *sql.DB
}

// OpenCodeFreeSQLiteStore opens a read-only CodeFree session.db. It
// rejects symlinked DB paths so a malicious root cannot redirect reads
// outside the configured tree.
func OpenCodeFreeSQLiteStore(dbPath string) (*CodeFreeSQLiteStore, error) {
	info, err := os.Lstat(dbPath)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("codefree sqlite db not found: %s", dbPath)
	}
	if err != nil {
		return nil, fmt.Errorf("stat codefree sqlite db %s: %w", dbPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("refusing symlinked codefree sqlite db: %s", dbPath)
	}
	db, err := openCodeFreeSQLiteDB(dbPath)
	if err != nil {
		return nil, err
	}
	return &CodeFreeSQLiteStore{dbPath: dbPath, db: db}, nil
}

// Close releases the underlying SQLite handle.
func (s *CodeFreeSQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// openCodeFreeSQLiteDB opens a CodeFree session.db in read-only mode.
func openCodeFreeSQLiteDB(dbPath string) (*sql.DB, error) {
	return openSQLiteReadOnly(dbPath, sqliteReadOptions{busyTimeoutMS: 3000})
}

// LoadStateJSON reads the codefree.state.v1 value from the
// session_metadata table. Returns the raw JSON string.
func (s *CodeFreeSQLiteStore) LoadStateJSON() (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("codefree sqlite store is closed")
	}
	var value string
	err := s.db.QueryRow(
		"SELECT value FROM session_metadata WHERE key = ?",
		codefreeStateKey,
	).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("query codefree state: %w", err)
	}
	return value, nil
}

// CodeFreeVirtualPath gives each session inside its own session.db a
// stable source identity for the agentsview archive.
func CodeFreeVirtualPath(dbPath, sessionID string) string {
	return VirtualSourcePath(dbPath, sessionID)
}

// codefreeSQLiteVirtualPathParts splits a virtual CodeFree source path
// back into its database path and raw session ID.
func codefreeSQLiteVirtualPathParts(path string) (string, string, bool) {
	return ParseVirtualSourcePathForBase(path, codefreeSQLiteDBName)
}

// CodeFreeSessionExists reports whether the backing session.db exists
// for the given session ID under the root.
func CodeFreeSessionExists(root, sessionID string) bool {
	if root == "" || sessionID == "" {
		return false
	}
	dbPath := filepath.Join(root, sessionID, codefreeSQLiteDBName)
	return IsRegularFile(dbPath)
}

// codefreeDBUnderRoot reports whether dbPath is a regular file under
// root. requireExists controls whether a missing file is an error.
func codefreeDBUnderRoot(root, dbPath string, requireExists bool) bool {
	cleanRoot := filepath.Clean(root)
	cleanDB := filepath.Clean(dbPath)
	rel, err := filepath.Rel(cleanRoot, cleanDB)
	if err != nil || rel == "" || rel == "." || rel == ".." {
		return false
	}
	if rel[:1] == ".." || (len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator)) {
		return false
	}
	if requireExists && !IsRegularFile(cleanDB) {
		return false
	}
	return true
}

// ListCodeFreeCatalog reads the __catalog__/session.db under root and
// returns the list of registered sessions. Returns nil if the catalog
// DB does not exist.
func ListCodeFreeCatalog(root string) ([]CodeFreeCatalogEntry, error) {
	catalogDBPath := filepath.Join(root, codefreeCatalogDir, codefreeSQLiteDBName)
	if !IsRegularFile(catalogDBPath) {
		return nil, nil
	}
	store, err := OpenCodeFreeSQLiteStore(catalogDBPath)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	var value string
	err = store.db.QueryRow(
		"SELECT value FROM session_metadata WHERE key = ?",
		codefreeCatalogKey,
	).Scan(&value)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query codefree catalog: %w", err)
	}
	if value == "" {
		return nil, nil
	}
	return parseCodeFreeCatalog(value), nil
}

// parseCodeFreeCatalog extracts CodeFreeCatalogEntry items from the
// codefree.catalog.v1 JSON value.
func parseCodeFreeCatalog(jsonStr string) []CodeFreeCatalogEntry {
	catalog := gjson.Get(jsonStr, "sessions")
	if !catalog.IsArray() {
		return nil
	}
	entries := make([]CodeFreeCatalogEntry, 0, len(catalog.Array()))
	catalog.ForEach(func(_, entry gjson.Result) bool {
		sess := entry.Get("session")
		if !sess.Exists() {
			return true
		}
		entries = append(entries, CodeFreeCatalogEntry{
			SessionID:       sess.Get("sessionId").Str,
			StartTime:       sess.Get("startTime").Int(),
			ModifiedTime:    sess.Get("modifiedTime").Int(),
			Summary:         sess.Get("summary").Str,
			WorkingDir:      sess.Get("workingDirectory").Str,
			DefaultAgentURI: entry.Get("defaultAgentUri").Str,
		})
		return true
	})
	return entries
}

// codefreeSQLiteDBPathsChecked returns the session.db paths for all
// catalog entries whose session DB exists under root.
func codefreeSQLiteDBPathsChecked(root string) ([]string, error) {
	catalog, err := ListCodeFreeCatalog(root)
	if err != nil {
		return nil, err
	}
	var dbPaths []string
	for _, entry := range catalog {
		dbPath := filepath.Join(root, entry.SessionID, codefreeSQLiteDBName)
		if IsRegularFile(dbPath) {
			dbPaths = append(dbPaths, dbPath)
		}
	}
	return dbPaths, nil
}
