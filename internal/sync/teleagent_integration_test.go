package sync_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/db"
	"go.kenn.io/agentsview/internal/parser"
)

// TestSyncTeleAgentSQLite_DisoversAndPersistsUserSessions confirms the sync
// engine discovers TeleAgent sessions from the backing SQLite DB, persists
// user sessions, and excludes _SYS_ system-maintenance sessions.
func TestSyncTeleAgentSQLite_DisoversAndPersistsUserSessions(t *testing.T) {
	env := setupSingleAgentTestEnv(t, parser.AgentTeleAgent)
	fixture := createTeleAgentSQLiteDB(t, env.teleagentDir)
	fixture.addSession(
		t, "ses_user1", "User task one", "/tmp/work", nil,
		1779012000000, 1779012030000,
	)
	fixture.addMessage(
		t, "msg_1", "ses_user1",
		`{"role":"user","model":{"modelID":"chat-pro"},"time":{"created":1779012000000}}`,
		1779012000000,
	)
	fixture.addTextPart(
		t, "prt_1", "ses_user1", "msg_1", "first prompt",
		1779012000000,
	)
	fixture.addSession(
		t, "ses_sys1", "_SYS_MEMORY_MERGE_ 2026/9/18", "/tmp/work", nil,
		1779012000000, 1779012040000,
	)
	fixture.addMessage(
		t, "msg_sys", "ses_sys1",
		`{"role":"user","time":{"created":1779012000000}}`,
		1779012000000,
	)
	fixture.addTextPart(
		t, "prt_sys", "ses_sys1", "msg_sys", "sys content",
		1779012000000,
	)

	stats := env.engine.SyncAll(context.Background(), nil)
	require.Positive(t, stats.TotalSessions, "sync should process sessions")
	require.Positive(t, stats.Synced, "sync should persist at least one session")

	// The user session is persisted.
	assertSessionState(t, env.db, "teleagent:ses_user1", func(sess *db.Session) {
		require.NotNil(t, sess.DisplayName)
		assert.Equal(t, "User task one", *sess.DisplayName)
		assert.Equal(t, "/tmp/work", sess.Cwd)
		assert.Equal(t, "teleagent", sess.Agent)
		require.NotNil(t, sess.FirstMessage)
		assert.Equal(t, "first prompt", *sess.FirstMessage)
	})

	// The _SYS_ session is excluded.
	got, err := env.db.GetSession(context.Background(), "teleagent:ses_sys1")
	require.NoError(t, err, "GetSession must not error for an excluded session")
	require.Nil(t, got, "_SYS_ session must not be persisted")
}

// TestSyncTeleAgentSQLite_SubagentLineagePersisted confirms a subagent
// session's stored row carries parent_session_id and relationship_type =
// "subagent".
func TestSyncTeleAgentSQLite_SubagentLineagePersisted(t *testing.T) {
	env := setupSingleAgentTestEnv(t, parser.AgentTeleAgent)
	fixture := createTeleAgentSQLiteDB(t, env.teleagentDir)
	parentID := "ses_parent"
	fixture.addSession(
		t, parentID, "Parent task", "/tmp/work", nil, 1, 2,
	)
	fixture.addSession(
		t, "ses_child", "Child task (@general subagent)", "/tmp/work", &parentID,
		3, 4,
	)
	fixture.addMessage(t, "msg_c", "ses_child", `{"role":"user","time":{"created":3}}`, 3)
	fixture.addTextPart(t, "prt_c", "ses_child", "msg_c", "child prompt", 3)

	env.engine.SyncAll(context.Background(), nil)

	sess, err := env.db.GetSession(context.Background(), "teleagent:ses_child")
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.NotNil(t, sess.ParentSessionID)
	assert.Equal(t, "teleagent:ses_parent", *sess.ParentSessionID,
		"subagent session's parent_session_id must be teleagent:<parent_id>")
	assert.Equal(t, "subagent", string(sess.RelationshipType),
		"subagent session's relationship_type must be \"subagent\"")
}

// TestSyncTeleAgentSQLite_TokenUsagePersisted confirms a parsed assistant
// message's token usage is queryable from the stored message rows.
func TestSyncTeleAgentSQLite_TokenUsagePersisted(t *testing.T) {
	env := setupSingleAgentTestEnv(t, parser.AgentTeleAgent)
	fixture := createTeleAgentSQLiteDB(t, env.teleagentDir)
	fixture.addSession(
		t, "ses_1", "Token task", "/tmp/work", nil,
		1779012000000, 1779012030000,
	)
	fixture.addMessage(
		t, "msg_1", "ses_1",
		`{"role":"assistant","modelID":"chat-pro","providerID":"NewApi","time":{"created":1779012001000},"cost":0.001,"tokens":{"total":120,"input":100,"output":20,"reasoning":5,"cache":{"read":30,"write":10}},"finish":"stop"}`,
		1779012001000,
	)
	fixture.addTextPart(
		t, "prt_1", "ses_1", "msg_1", "answer", 1779012001100,
	)

	env.engine.SyncAll(context.Background(), nil)

	msgs := fetchMessages(t, env.db, "teleagent:ses_1")
	require.Len(t, msgs, 1)
	assert.Greater(t, msgs[0].ContextTokens, 0, "context tokens should be persisted")
	assert.Greater(t, msgs[0].OutputTokens, 0, "output tokens should be persisted")
}

// TestSyncTeleAgentSQLite_VanishedDBPreservesRows confirms removing the
// backing DB file and re-syncing does not delete already-stored sessions
// (persistent-archive semantics).
func TestSyncTeleAgentSQLite_VanishedDBPreservesRows(t *testing.T) {
	env := setupSingleAgentTestEnv(t, parser.AgentTeleAgent)
	fixture := createTeleAgentSQLiteDB(t, env.teleagentDir)
	fixture.addSession(
		t, "ses_1", "Task", "/tmp/work", nil, 1, 2,
	)
	fixture.addMessage(t, "msg_1", "ses_1", `{"role":"user","time":{"created":1}}`, 1)
	fixture.addTextPart(t, "prt_1", "ses_1", "msg_1", "prompt", 1)

	stats := env.engine.SyncAll(context.Background(), nil)
	require.Positive(t, stats.Synced, "initial sync should persist the session")

	// Remove the DB file entirely. The fixture's db handle is still open;
	// close it first so the file can be removed on Windows.
	fixture.db.Close()
	fixture.db = nil
	require.NoError(t, os.RemoveAll(filepath.Dir(fixture.path)),
		"remove the user subdirectory (DB + any WAL/SHM)")

	// Re-sync. The stored session must survive the vanished backing DB.
	env.engine.SyncAll(context.Background(), nil)

	got, err := env.db.GetSession(context.Background(), "teleagent:ses_1")
	require.NoError(t, err, "GetSession must not error for a preserved session")
	require.NotNil(t, got, "stored session must survive a vanished backing DB")
}

// TestSyncTeleAgentSQLite_VanishedRowForceReplaces confirms deleting a
// session row (but keeping the DB file) and triggering a changed-path sync
// via SyncPathsContext tombstones the stored session (marks it
// source-missing). SyncPathsContext simulates a watcher event on the backing
// DB; the provider's changedPathTombstones emits a tombstone source for the
// vanished member, the engine parses it, and the provider returns
// SkipNoSession with ForceReplace because the DB file is present but the
// session row is gone. The session stays browsable but is marked
// source-missing, matching the Kiro/Windsurf persistent-archive pattern.
func TestSyncTeleAgentSQLite_VanishedRowForceReplaces(t *testing.T) {
	env := setupSingleAgentTestEnv(t, parser.AgentTeleAgent)
	fixture := createTeleAgentSQLiteDB(t, env.teleagentDir)
	fixture.addSession(
		t, "ses_surviving", "Surviving task", "/tmp/work", nil, 1, 2,
	)
	fixture.addMessage(
		t, "msg_s", "ses_surviving", `{"role":"user","time":{"created":1}}`, 1,
	)
	fixture.addTextPart(t, "prt_s", "ses_surviving", "msg_s", "surviving prompt", 1)
	fixture.addSession(
		t, "ses_doomed", "Doomed task", "/tmp/work", nil, 1, 3,
	)
	fixture.addMessage(
		t, "msg_d", "ses_doomed", `{"role":"user","time":{"created":1}}`, 1,
	)
	fixture.addTextPart(t, "prt_d", "ses_doomed", "msg_d", "doomed prompt", 1)

	stats := env.engine.SyncAll(context.Background(), nil)
	require.Positive(t, stats.Synced, "initial sync should persist sessions")

	// Confirm both sessions are stored before we delete one.
	for _, id := range []string{"teleagent:ses_surviving", "teleagent:ses_doomed"} {
		sess, err := env.db.GetSession(context.Background(), id)
		require.NoError(t, err)
		require.NotNil(t, sess, "session %s should be stored", id)
	}

	// Delete one session row but keep the DB file present.
	fixture.deleteSession(t, "ses_doomed")

	// Trigger a changed-path sync on the backing DB, simulating a watcher
	// event. The provider's SourcesForChangedPath classifies the DB-level
	// event and emits a tombstone for the vanished member; the engine
	// force-replaces the stored session (marks it source-missing).
	require.NoError(t, env.engine.SyncPathsContext(
		context.Background(), []string{fixture.path},
	))

	// The doomed session is marked source-missing but stays browsable.
	archived, err := env.db.GetSessionFull(
		context.Background(), "teleagent:ses_doomed",
	)
	require.NoError(t, err)
	require.NotNil(t, archived)
	assert.NotNil(t, archived.SourceMissingAt,
		"deleted session row must mark the stored session source-missing")

	// The surviving session is unaffected.
	surviving, err := env.db.GetSession(
		context.Background(), "teleagent:ses_surviving",
	)
	require.NoError(t, err)
	require.NotNil(t, surviving)
	assert.Nil(t, surviving.SourceMissingAt,
		"surviving session must not be marked source-missing")
}
