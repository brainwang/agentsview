package parser

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTeleAgentProviderSQLiteDBAt creates a TeleAgent SQLite DB at the given
// path. Used by tests that point a configured root directly at a specific
// user subdirectory (the explicit-subdirectory override case).
func newTeleAgentProviderSQLiteDBAt(
	t *testing.T, path string,
) (string, *sql.DB) {
	t.Helper()
	return newTeleAgentSQLiteTestDBAt(t, path)
}

// newTeleAgentProviderSQLiteDBUnderRoot creates a TeleAgent SQLite DB at
// <root>/<userSubdir>/teleagent.db, mirroring the production users/ parent
// layout, and returns the DB path plus an open handle for seeding.
func newTeleAgentProviderSQLiteDBUnderRoot(
	t *testing.T, root, userSubdir string,
) (string, *sql.DB) {
	t.Helper()
	userDir := filepath.Join(root, userSubdir)
	require.NoError(t, os.MkdirAll(userDir, 0o755), "mkdir user subdir")
	dbPath := filepath.Join(userDir, teleagentSQLiteDBName)
	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err, "open teleagent provider sqlite db")
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(teleagentSQLiteSchema)
	require.NoError(t, err, "create teleagent sqlite schema")
	return dbPath, db
}

func TestTeleAgentProvider_Discover(t *testing.T) {
	root := t.TempDir()
	dbPath, db := newTeleAgentProviderSQLiteDBUnderRoot(t, root, "v1_public_userA")
	seedTeleAgentSession(
		t, db, "ses_user1", "User task one", "/tmp/work", nil,
		1779012000000, 1779012030000,
	)
	seedTeleAgentSession(
		t, db, "ses_user2", "User task two", "/tmp/work", nil,
		1779012000000, 1779012040000,
	)
	seedTeleAgentSession(
		t, db, "ses_sys", "_SYS_MEMORY_MERGE_ 2026/9/18", "/tmp/work", nil,
		1779012000000, 1779012050000,
	)

	provider, ok := NewProvider(AgentTeleAgent, ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)

	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	// One container source plus one virtual member per user session.
	// _SYS_ sessions are filtered at the SQL level.
	require.Len(t, sources, 3, "1 container + 2 user sessions (sys filtered)")
	assert.Equal(t, dbPath, sources[0].DisplayPath,
		"container source is the DB path")
	for _, member := range sources[1:] {
		assert.Contains(t, member.DisplayPath, "#ses_user",
			"virtual member paths carry the session id after #")
	}
}

func TestTeleAgentProvider_FirstUserAutoDiscovery(t *testing.T) {
	root := t.TempDir()
	// Two user subdirectories each with a teleagent.db. The provider must
	// read only the lexicographically first.
	dbPathA, dbA := newTeleAgentProviderSQLiteDBUnderRoot(t, root, "v1_public_aaa")
	_, dbB := newTeleAgentProviderSQLiteDBUnderRoot(t, root, "v1_public_bbb")
	seedTeleAgentSession(t, dbA, "ses_a", "A task", "/tmp/a", nil, 1, 2)
	seedTeleAgentSession(t, dbB, "ses_b", "B task", "/tmp/b", nil, 1, 2)

	provider, ok := NewProvider(AgentTeleAgent, ProviderConfig{
		Roots: []string{root},
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, sources)
	for _, src := range sources {
		assert.Contains(t, src.DisplayPath, dbPathA,
			"only the lexicographically first user subdirectory is read")
		assert.NotContains(t, src.DisplayPath, "v1_public_bbb",
			"the second user subdirectory must not be read")
	}
}

func TestTeleAgentProvider_ExplicitSubdirectoryOverride(t *testing.T) {
	root := t.TempDir()
	userA := filepath.Join(root, "v1_public_aaa")
	userB := filepath.Join(root, "v1_public_bbb")
	require.NoError(t, os.MkdirAll(userA, 0o755))
	require.NoError(t, os.MkdirAll(userB, 0o755))
	// Drop a stray DB in userA so we can prove the override does not scan
	// siblings: when the root points at userB, userA's DB must be ignored.
	_, dbA := newTeleAgentProviderSQLiteDBAt(t, filepath.Join(userA, teleagentSQLiteDBName))
	seedTeleAgentSession(t, dbA, "ses_a", "A task", "/tmp/a", nil, 1, 2)
	dbPathB := filepath.Join(userB, teleagentSQLiteDBName)
	dbB, err := sql.Open("sqlite3", dbPathB)
	require.NoError(t, err)
	t.Cleanup(func() { dbB.Close() })
	_, err = dbB.Exec(teleagentSQLiteSchema)
	require.NoError(t, err)
	seedTeleAgentSession(t, dbB, "ses_b", "B task", "/tmp/b", nil, 1, 2)

	provider, ok := NewProvider(AgentTeleAgent, ProviderConfig{
		Roots: []string{userB},
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, sources)
	for _, src := range sources {
		assert.Contains(t, src.DisplayPath, dbPathB,
			"explicit subdirectory override uses that directory's DB")
		assert.NotContains(t, src.DisplayPath, "v1_public_aaa",
			"siblings of the explicit subdirectory are not scanned")
	}
}

func TestTeleAgentProvider_Fingerprint(t *testing.T) {
	root := t.TempDir()
	dbPath, db := newTeleAgentProviderSQLiteDBUnderRoot(t, root, "v1_public_user")
	seedTeleAgentSession(
		t, db, "ses_1", "Task", "/tmp/work", nil,
		1779012000000, 1779012030000,
	)
	seedTeleAgentMessage(t, db, "msg_1", "ses_1",
		`{"role":"user","time":{"created":1}}`, 1)
	seedTeleAgentPart(t, db, "prt_1", "ses_1", "msg_1",
		`{"type":"text","text":"hello world"}`, 1)

	provider, ok := NewProvider(AgentTeleAgent, ProviderConfig{
		Roots: []string{root},
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(sources), 2)

	// Find the virtual member source for ses_1.
	var member SourceRef
	for _, src := range sources {
		if s, ok := src.Opaque.(teleagentSource); ok &&
			s.Kind == teleagentSourceSQLiteSession &&
			s.SessionID == "ses_1" {
			member = src
			break
		}
	}
	require.NotEmpty(t, member.DisplayPath, "member source for ses_1 must be discovered")

	fp, err := provider.Fingerprint(context.Background(), member)
	require.NoError(t, err)
	assert.Equal(t, member.DisplayPath, fp.Key)
	assert.Equal(t, int64(1779012030000)*1_000_000, fp.MTimeNS,
		"session fingerprint MTimeNS = session.time_updated in ns")
	assert.Greater(t, fp.Size, int64(0),
		"session fingerprint Size = sum of length(part.data)")

	// Find the container source.
	var container SourceRef
	for _, src := range sources {
		if s, ok := src.Opaque.(teleagentSource); ok &&
			s.Kind == teleagentSourceSQLiteDB {
			container = src
			break
		}
	}
	require.NotEmpty(t, container.DisplayPath, "container source must be discovered")
	containerFP, err := provider.Fingerprint(context.Background(), container)
	require.NoError(t, err)
	assert.Equal(t, dbPath, containerFP.Key)
	// Composite mtime picks up the DB file's mtime at minimum.
	info, err := os.Stat(dbPath)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, containerFP.MTimeNS, info.ModTime().UnixNano())
}

func TestTeleAgentProvider_VanishedDBPreservesRows(t *testing.T) {
	root := t.TempDir()
	dbPath, db := newTeleAgentProviderSQLiteDBUnderRoot(t, root, "v1_public_user")
	seedTeleAgentSession(
		t, db, "ses_1", "Task", "/tmp/work", nil, 1, 2,
	)
	seedTeleAgentMessage(t, db, "msg_1", "ses_1",
		`{"role":"user","time":{"created":1}}`, 1)
	seedTeleAgentPart(t, db, "prt_1", "ses_1", "msg_1",
		`{"type":"text","text":"hello"}`, 1)
	db.Close()

	provider, ok := NewProvider(AgentTeleAgent, ProviderConfig{
		Roots: []string{root},
	})
	require.True(t, ok)
	member, ok, err := provider.FindSource(context.Background(), FindSourceRequest{
		RawSessionID: "ses_1",
	})
	require.NoError(t, err)
	require.True(t, ok)

	// Remove the DB file entirely.
	require.NoError(t, os.Remove(dbPath))

	outcome, err := provider.Parse(context.Background(), ParseRequest{
		Source: member,
	})
	require.NoError(t, err)
	assert.Equal(t, SkipNoSession, outcome.SkipReason,
		"vanished DB yields SkipNoSession")
	assert.False(t, outcome.ForceReplace,
		"vanished DB must NOT ForceReplace (persistent archive preserves rows)")
}

func TestTeleAgentProvider_VanishedRowForceReplaces(t *testing.T) {
	root := t.TempDir()
	dbPath, db := newTeleAgentProviderSQLiteDBUnderRoot(t, root, "v1_public_user")
	seedTeleAgentSession(
		t, db, "ses_1", "Task", "/tmp/work", nil, 1, 2,
	)
	seedTeleAgentMessage(t, db, "msg_1", "ses_1",
		`{"role":"user","time":{"created":1}}`, 1)
	seedTeleAgentPart(t, db, "prt_1", "ses_1", "msg_1",
		`{"type":"text","text":"hello"}`, 1)

	provider, ok := NewProvider(AgentTeleAgent, ProviderConfig{
		Roots: []string{root},
	})
	require.True(t, ok)
	member, ok, err := provider.FindSource(context.Background(), FindSourceRequest{
		RawSessionID: "ses_1",
	})
	require.NoError(t, err)
	require.True(t, ok)

	// Delete the session row but keep the DB file present.
	_, err = db.Exec("DELETE FROM session WHERE id = ?", "ses_1")
	require.NoError(t, err)
	_ = dbPath

	outcome, err := provider.Parse(context.Background(), ParseRequest{
		Source: member,
	})
	require.NoError(t, err)
	assert.Equal(t, SkipNoSession, outcome.SkipReason)
	assert.True(t, outcome.ForceReplace,
		"vanished row in a present DB must ForceReplace to tombstone the stored session")
}

func TestTeleAgentProvider_WatchPlan(t *testing.T) {
	root := t.TempDir()
	dbPath, _ := newTeleAgentProviderSQLiteDBUnderRoot(t, root, "v1_public_user")

	provider, ok := NewProvider(AgentTeleAgent, ProviderConfig{
		Roots: []string{root},
	})
	require.True(t, ok)
	plan, err := provider.WatchPlan(context.Background())
	require.NoError(t, err)
	require.Len(t, plan.Roots, 2, "watch plan has the users/ parent and the DB path")

	var hasRoot, hasDB bool
	for _, wr := range plan.Roots {
		if wr.Path == root {
			hasRoot = true
			assert.False(t, wr.Recursive,
				"users/ parent watch is non-recursive")
		}
		if wr.Path == dbPath {
			hasDB = true
			assert.False(t, wr.Recursive,
				"DB file watch is non-recursive")
		}
	}
	assert.True(t, hasRoot, "watch plan must include the users/ parent root")
	assert.True(t, hasDB, "watch plan must include the resolved DB path")
}

func TestTeleAgentProvider_ParseSession(t *testing.T) {
	root := t.TempDir()
	dbPath, db := newTeleAgentProviderSQLiteDBUnderRoot(t, root, "v1_public_user")
	seedTeleAgentSession(
		t, db, "ses_1", "Task", "/tmp/work", nil,
		1779012000000, 1779012030000,
	)
	seedTeleAgentMessage(t, db, "msg_1", "ses_1",
		`{"role":"user","model":{"modelID":"chat-pro"},"time":{"created":1779012000000}}`,
		1779012000000,
	)
	seedTeleAgentPart(t, db, "prt_1", "ses_1", "msg_1",
		`{"type":"text","text":"first prompt"}`,
		1779012000000,
	)

	provider, ok := NewProvider(AgentTeleAgent, ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	member, ok, err := provider.FindSource(context.Background(), FindSourceRequest{
		RawSessionID: "ses_1",
	})
	require.NoError(t, err)
	require.True(t, ok)

	outcome, err := provider.Parse(context.Background(), ParseRequest{
		Source: member,
	})
	require.NoError(t, err)
	require.True(t, outcome.ResultSetComplete)
	require.True(t, outcome.ForceReplace)
	require.Len(t, outcome.Results, 1)

	sess := outcome.Results[0].Result.Session
	assert.Equal(t, "teleagent:ses_1", sess.ID)
	assert.Equal(t, "Task", sess.SessionName)
	assert.Equal(t, "/tmp/work", sess.Cwd)
	assert.Equal(t, "devbox", sess.Machine)
	assert.Equal(t, AgentTeleAgent, sess.Agent)
	assert.Equal(t, "first prompt", sess.FirstMessage)
	assert.Equal(t, dbPath+"#ses_1", sess.File.Path)
	// Sanity check the mtime carries through to the parsed session.
	assert.Greater(t, sess.File.Mtime, int64(0))
}
