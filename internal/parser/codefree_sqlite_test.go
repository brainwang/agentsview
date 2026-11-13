package parser

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// codefreeSQLiteSchema mirrors the CodeFree session_metadata key/value
// table layout used by the production session.db and catalog DB.
const codefreeSQLiteSchema = `
CREATE TABLE session_metadata (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`

func newCodeFreeSQLiteTestDB(t *testing.T) (string, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, codefreeSQLiteDBName)
	return newCodeFreeSQLiteTestDBAt(t, path)
}

func newCodeFreeSQLiteTestDBAt(
	t *testing.T, path string,
) (string, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err, "open codefree sqlite test db")
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(codefreeSQLiteSchema)
	require.NoError(t, err, "create codefree sqlite schema")
	return path, db
}

func seedCodeFreeMetadata(
	t *testing.T, db *sql.DB, key, value string,
) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO session_metadata (key, value) VALUES (?, ?)`,
		key, value,
	)
	require.NoError(t, err, "seed codefree metadata")
}

func openCodeFreeTestStore(
	t *testing.T, dbPath string,
) *CodeFreeSQLiteStore {
	t.Helper()
	store, err := OpenCodeFreeSQLiteStore(dbPath)
	require.NoError(t, err, "open codefree sqlite store")
	t.Cleanup(func() { store.Close() })
	return store
}

func TestOpenCodeFreeSQLiteStore_RejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	realPath := filepath.Join(dir, codefreeSQLiteDBName)
	_, db := newCodeFreeSQLiteTestDBAt(t, realPath)
	db.Close()
	linkPath := filepath.Join(dir, "link.db")
	err := os.Symlink(realPath, linkPath)
	if err != nil {
		t.Skipf("cannot create symlink for test fixture: %v", err)
	}
	_, err = OpenCodeFreeSQLiteStore(linkPath)
	require.Error(t, err, "symlinked codefree db must be rejected")
}

func TestOpenCodeFreeSQLiteStore_MissingFile(t *testing.T) {
	dir := t.TempDir()
	missingPath := filepath.Join(dir, "nonexistent.db")
	_, err := OpenCodeFreeSQLiteStore(missingPath)
	require.Error(t, err, "missing db must return error")
}

func TestLoadStateJSON_ReadsStateKey(t *testing.T) {
	dbPath, db := newCodeFreeSQLiteTestDB(t)
	stateJSON := `{"session":{"sessionId":"abc-123"},"chats":[]}`
	seedCodeFreeMetadata(t, db, codefreeStateKey, stateJSON)
	seedCodeFreeMetadata(t, db, "other.key", `{"unrelated":true}`)
	store := openCodeFreeTestStore(t, dbPath)
	got, err := store.LoadStateJSON()
	require.NoError(t, err)
	assert.Equal(t, stateJSON, got)
}

func TestLoadStateJSON_MissingKeyReturnsEmpty(t *testing.T) {
	dbPath, db := newCodeFreeSQLiteTestDB(t)
	seedCodeFreeMetadata(t, db, "other.key", `{"unrelated":true}`)
	store := openCodeFreeTestStore(t, dbPath)
	got, err := store.LoadStateJSON()
	require.NoError(t, err)
	assert.Empty(t, got, "missing state key must return empty string")
}

func TestParseCodeFreeCatalog_ValidJSON(t *testing.T) {
	jsonStr := `{
		"sessions": [
			{
				"session": {
					"sessionId": "uuid-001",
					"startTime": 1779012000000,
					"modifiedTime": 1779012030000,
					"summary": "Test session 1",
					"workingDirectory": "file:///d%3A/Temp/project"
				},
				"defaultAgentUri": "codefree-agent://builtin/spec"
			},
			{
				"session": {
					"sessionId": "uuid-002",
					"startTime": 1779013000000,
					"modifiedTime": 1779013030000,
					"summary": "Test session 2",
					"workingDirectory": "file:///c%3A/Users/test"
				},
				"defaultAgentUri": "codefree-agent://builtin/explore"
			}
		]
	}`
	entries := parseCodeFreeCatalog(jsonStr)
	require.Len(t, entries, 2)
	assert.Equal(t, "uuid-001", entries[0].SessionID)
	assert.Equal(t, int64(1779012000000), entries[0].StartTime)
	assert.Equal(t, int64(1779012030000), entries[0].ModifiedTime)
	assert.Equal(t, "Test session 1", entries[0].Summary)
	assert.Equal(t, "file:///d%3A/Temp/project", entries[0].WorkingDir)
	assert.Equal(t, "codefree-agent://builtin/spec", entries[0].DefaultAgentURI)
	assert.Equal(t, "uuid-002", entries[1].SessionID)
	assert.Equal(t, "codefree-agent://builtin/explore", entries[1].DefaultAgentURI)
}

func TestParseCodeFreeCatalog_EmptySessions(t *testing.T) {
	jsonStr := `{"sessions":[]}`
	entries := parseCodeFreeCatalog(jsonStr)
	assert.Empty(t, entries)
}

func TestParseCodeFreeCatalog_NoSessionsKey(t *testing.T) {
	jsonStr := `{"other":"data"}`
	entries := parseCodeFreeCatalog(jsonStr)
	assert.Nil(t, entries)
}

func TestParseCodeFreeCatalog_MalformedJSON(t *testing.T) {
	entries := parseCodeFreeCatalog(`{not valid json`)
	assert.Nil(t, entries)
}

func TestParseCodeFreeCatalog_EntryWithoutSession(t *testing.T) {
	jsonStr := `{"sessions":[{"defaultAgentUri":"codefree-agent://builtin/spec"}]}`
	entries := parseCodeFreeCatalog(jsonStr)
	assert.Empty(t, entries, "entry without session object must be skipped")
}

func TestListCodeFreeCatalog_ReadsCatalogDB(t *testing.T) {
	root := t.TempDir()
	catalogDir := filepath.Join(root, codefreeCatalogDir)
	require.NoError(t, os.MkdirAll(catalogDir, 0o755))
	catalogDBPath := filepath.Join(catalogDir, codefreeSQLiteDBName)
	_, db := newCodeFreeSQLiteTestDBAt(t, catalogDBPath)
	catalogJSON := `{"sessions":[{"session":{"sessionId":"cat-001","startTime":1000,"modifiedTime":2000,"summary":"Catalog session","workingDirectory":"file:///tmp"},"defaultAgentUri":"codefree-agent://builtin/spec"}]}`
	seedCodeFreeMetadata(t, db, codefreeCatalogKey, catalogJSON)
	db.Close()
	entries, err := ListCodeFreeCatalog(root)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "cat-001", entries[0].SessionID)
	assert.Equal(t, "Catalog session", entries[0].Summary)
}

func TestListCodeFreeCatalog_NoCatalogDB(t *testing.T) {
	root := t.TempDir()
	entries, err := ListCodeFreeCatalog(root)
	require.NoError(t, err)
	assert.Nil(t, entries, "missing catalog DB must return nil entries")
}

func TestListCodeFreeCatalog_MissingCatalogKey(t *testing.T) {
	root := t.TempDir()
	catalogDir := filepath.Join(root, codefreeCatalogDir)
	require.NoError(t, os.MkdirAll(catalogDir, 0o755))
	catalogDBPath := filepath.Join(catalogDir, codefreeSQLiteDBName)
	_, db := newCodeFreeSQLiteTestDBAt(t, catalogDBPath)
	seedCodeFreeMetadata(t, db, "other.key", "value")
	db.Close()
	entries, err := ListCodeFreeCatalog(root)
	require.NoError(t, err)
	assert.Nil(t, entries, "missing catalog key must return nil entries")
}

func TestCodeFreeSessionExists_ExistingDB(t *testing.T) {
	root := t.TempDir()
	sessionID := "uuid-exists"
	sessionDir := filepath.Join(root, sessionID)
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))
	dbPath := filepath.Join(sessionDir, codefreeSQLiteDBName)
	_, db := newCodeFreeSQLiteTestDBAt(t, dbPath)
	db.Close()
	assert.True(t, CodeFreeSessionExists(root, sessionID))
}

func TestCodeFreeSessionExists_MissingDB(t *testing.T) {
	root := t.TempDir()
	assert.False(t, CodeFreeSessionExists(root, "uuid-missing"))
}

func TestCodeFreeSessionExists_EmptyArgs(t *testing.T) {
	assert.False(t, CodeFreeSessionExists("", "uuid"))
	assert.False(t, CodeFreeSessionExists("/tmp", ""))
}

func TestCodeFreeVirtualPath_RoundTrip(t *testing.T) {
	dbPath := filepath.Join("root", "uuid-123", codefreeSQLiteDBName)
	sessionID := "uuid-123"
	vp := CodeFreeVirtualPath(dbPath, sessionID)
	assert.NotEmpty(t, vp)
	gotDB, gotID, ok := codefreeSQLiteVirtualPathParts(vp)
	require.True(t, ok)
	assert.Equal(t, dbPath, gotDB)
	assert.Equal(t, sessionID, gotID)
}

func TestCodeFreeDBUnderRoot_ValidPath(t *testing.T) {
	root := filepath.Clean(t.TempDir())
	dbPath := filepath.Join(root, "uuid-1", codefreeSQLiteDBName)
	assert.True(t, codefreeDBUnderRoot(root, dbPath, false))
}

func TestCodeFreeDBUnderRoot_OutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "..", "escape", codefreeSQLiteDBName)
	assert.False(t, codefreeDBUnderRoot(root, outside, false))
}

func TestCodeFreeDBUnderRoot_RequireExists(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "uuid-1", codefreeSQLiteDBName)
	assert.False(t, codefreeDBUnderRoot(root, dbPath, true),
		"requireExists must reject a missing file")
	assert.True(t, codefreeDBUnderRoot(root, dbPath, false),
		"requireExists=false must accept a missing file")
}
