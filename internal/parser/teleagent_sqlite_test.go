package parser

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// teleagentSQLiteSchema mirrors the production TeleAgent 3-table layout
// (session/message/part). The parser reads only the columns it needs; the
// rest of the production schema (project_id, slug, version, share_url, etc.)
// is omitted because the fixture only exercises the parse path.
const teleagentSQLiteSchema = `
CREATE TABLE session (
	id TEXT PRIMARY KEY,
	title TEXT NOT NULL,
	directory TEXT NOT NULL,
	parent_id TEXT,
	time_created INTEGER NOT NULL,
	time_updated INTEGER NOT NULL
);
CREATE TABLE message (
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL,
	data TEXT NOT NULL,
	time_created INTEGER NOT NULL,
	time_updated INTEGER NOT NULL
);
CREATE TABLE part (
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL,
	message_id TEXT NOT NULL,
	data TEXT NOT NULL,
	time_created INTEGER NOT NULL,
	time_updated INTEGER NOT NULL
);
`

func newTeleAgentSQLiteTestDB(t *testing.T) (string, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, teleagentSQLiteDBName)
	return newTeleAgentSQLiteTestDBAt(t, path)
}

func newTeleAgentSQLiteTestDBAt(
	t *testing.T, path string,
) (string, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err, "open teleagent sqlite test db")
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(teleagentSQLiteSchema)
	require.NoError(t, err, "create teleagent sqlite schema")
	return path, db
}

func seedTeleAgentSession(
	t *testing.T, db *sql.DB,
	id, title, directory string, parentID *string,
	timeCreated, timeUpdated int64,
) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO session
			(id, title, directory, parent_id, time_created, time_updated)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, title, directory, parentID, timeCreated, timeUpdated,
	)
	require.NoError(t, err, "seed teleagent session")
}

func seedTeleAgentMessage(
	t *testing.T, db *sql.DB,
	id, sessionID, data string, timeCreated int64,
) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO message (id, session_id, data, time_created, time_updated)
		 VALUES (?, ?, ?, ?, ?)`,
		id, sessionID, data, timeCreated, timeCreated,
	)
	require.NoError(t, err, "seed teleagent message")
}

func seedTeleAgentPart(
	t *testing.T, db *sql.DB,
	id, sessionID, messageID, data string, timeCreated int64,
) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO part (id, session_id, message_id, data, time_created, time_updated)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, sessionID, messageID, data, timeCreated, timeCreated,
	)
	require.NoError(t, err, "seed teleagent part")
}

func openTeleAgentTestStore(
	t *testing.T, dbPath string,
) *TeleAgentSQLiteStore {
	t.Helper()
	store, err := OpenTeleAgentSQLiteStore(dbPath)
	require.NoError(t, err, "open teleagent sqlite store")
	t.Cleanup(func() { store.Close() })
	return store
}

func TestOpenTeleAgentSQLiteStore_RejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	realPath := filepath.Join(dir, teleagentSQLiteDBName)
	_, db := newTeleAgentSQLiteTestDBAt(t, realPath)
	db.Close()
	linkPath := filepath.Join(dir, "link.db")
	err := os.Symlink(realPath, linkPath)
	if err != nil {
		// Creating a symlink requires admin privileges on Windows and
		// may be unavailable in sandboxed environments. Skip rather
		// than fail when the test cannot set up its fixture.
		t.Skipf("cannot create symlink for test fixture: %v", err)
	}
	_, err = OpenTeleAgentSQLiteStore(linkPath)
	require.Error(t, err, "symlinked teleagent db must be rejected")
}

func TestForEachSessionMeta_FiltersSysSessions(t *testing.T) {
	dbPath, db := newTeleAgentSQLiteTestDB(t)
	seedTeleAgentSession(
		t, db, "ses_user", "User task", "/tmp/work", nil,
		1779012000000, 1779012030000,
	)
	seedTeleAgentSession(
		t, db, "ses_sys", "_SYS_MEMORY_MERGE_ 2026/9/18", "/tmp/work", nil,
		1779012000000, 1779012040000,
	)
	store := openTeleAgentTestStore(t, dbPath)
	metas, err := store.ListSessionMeta()
	require.NoError(t, err)
	require.Len(t, metas, 1, "expected only the user session after _SYS_ filter")
	assert.Equal(t, "ses_user", metas[0].SessionID)
}

func TestForEachSessionMeta_FiltersEmptyTitle(t *testing.T) {
	dbPath, db := newTeleAgentSQLiteTestDB(t)
	seedTeleAgentSession(
		t, db, "ses_user", "User task", "/tmp/work", nil, 1, 2,
	)
	seedTeleAgentSession(
		t, db, "ses_empty", "", "/tmp/work", nil, 1, 3,
	)
	store := openTeleAgentTestStore(t, dbPath)
	metas, err := store.ListSessionMeta()
	require.NoError(t, err)
	require.Len(t, metas, 1, "expected only the titled session")
	assert.Equal(t, "ses_user", metas[0].SessionID)
}

func TestLoadSession_UserMessage(t *testing.T) {
	dbPath, db := newTeleAgentSQLiteTestDB(t)
	seedTeleAgentSession(
		t, db, "ses_1", "User task", `D:\Temp\Download\TeleAgent`, nil,
		1779012000000, 1779012030000,
	)
	seedTeleAgentMessage(t, db, "msg_1", "ses_1",
		`{"role":"user","model":{"providerID":"NewApi","modelID":"chat-pro"},"time":{"created":1779012000000}}`,
		1779012000000,
	)
	seedTeleAgentPart(t, db, "prt_1", "ses_1", "msg_1",
		`{"type":"text","text":"根据这段Dockers文档回答问题"}`,
		1779012000000,
	)
	store := openTeleAgentTestStore(t, dbPath)
	sess, msgs, err := store.LoadSession(context.Background(), "ses_1", "test-machine")
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.Len(t, msgs, 1)
	assert.Equal(t, RoleUser, msgs[0].Role)
	assert.Equal(t, "根据这段Dockers文档回答问题", msgs[0].Content)
	assert.Equal(t, "chat-pro", msgs[0].Model)
	assert.Equal(t, "User task", sess.SessionName)
	assert.Equal(t, `D:\Temp\Download\TeleAgent`, sess.Cwd)
	assert.Equal(t, "teleagent:ses_1", sess.ID)
	assert.Equal(t, AgentTeleAgent, sess.Agent)
	assert.Equal(t, "test-machine", sess.Machine)
	assert.Empty(t, sess.ParentSessionID)
}

func TestLoadSession_AssistantTextAndReasoning(t *testing.T) {
	dbPath, db := newTeleAgentSQLiteTestDB(t)
	seedTeleAgentSession(
		t, db, "ses_1", "Reasoning task", "/tmp/work", nil, 1, 2,
	)
	seedTeleAgentMessage(t, db, "msg_1", "ses_1",
		`{"role":"assistant","modelID":"chat-pro","providerID":"NewApi","time":{"created":1779012001000},"finish":"stop"}`,
		1779012001000,
	)
	seedTeleAgentPart(t, db, "prt_1", "ses_1", "msg_1",
		`{"type":"reasoning","text":"Step 1: analyze the request"}`,
		1779012001000,
	)
	seedTeleAgentPart(t, db, "prt_2", "ses_1", "msg_1",
		`{"type":"text","text":"Here is the answer."}`,
		1779012001100,
	)
	store := openTeleAgentTestStore(t, dbPath)
	_, msgs, err := store.LoadSession(context.Background(), "ses_1", "m")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, RoleAssistant, msgs[0].Role)
	assert.True(t, msgs[0].HasThinking)
	assert.Equal(t, "Step 1: analyze the request", msgs[0].ThinkingText)
	assert.Equal(t, "Here is the answer.", msgs[0].Content)
	assert.Equal(t, "chat-pro", msgs[0].Model)
	assert.Equal(t, "NewApi", msgs[0].ProviderID)
	assert.Equal(t, "stop", msgs[0].StopReason)
}

func TestLoadSession_ToolCallWithResult(t *testing.T) {
	dbPath, db := newTeleAgentSQLiteTestDB(t)
	seedTeleAgentSession(
		t, db, "ses_1", "Tool task", "/tmp/work", nil, 1, 2,
	)
	seedTeleAgentMessage(t, db, "msg_1", "ses_1",
		`{"role":"assistant","modelID":"chat-pro","time":{"created":1779012001000},"finish":"tool_calls"}`,
		1779012001000,
	)
	seedTeleAgentPart(t, db, "prt_1", "ses_1", "msg_1",
		`{"type":"tool","callID":"call_1","tool":"write","state":{"status":"completed","input":{"filePath":"/tmp/out.txt","content":"hi"}},"output":"Wrote 2 bytes","metadata":{"filepath":"D:\\tmp\\out.txt"}}`,
		1779012001100,
	)
	store := openTeleAgentTestStore(t, dbPath)
	_, msgs, err := store.LoadSession(context.Background(), "ses_1", "m")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.True(t, msgs[0].HasToolUse)
	require.Len(t, msgs[0].ToolCalls, 1)
	tc := msgs[0].ToolCalls[0]
	assert.Equal(t, "call_1", tc.ToolUseID)
	assert.Equal(t, "write", tc.ToolName)
	assert.Equal(t, "Write", tc.Category)
	assert.Equal(t, `D:\tmp\out.txt`, tc.FilePath)
	require.Len(t, tc.ResultEvents, 1)
	assert.Equal(t, "completed", tc.ResultEvents[0].Status)
	assert.Equal(t, "Wrote 2 bytes", tc.ResultEvents[0].Content)
}

func TestLoadSession_ToolCallWithError(t *testing.T) {
	dbPath, db := newTeleAgentSQLiteTestDB(t)
	seedTeleAgentSession(t, db, "ses_1", "Tool task", "/tmp/work", nil, 1, 2)
	seedTeleAgentMessage(t, db, "msg_1", "ses_1",
		`{"role":"assistant","modelID":"chat-pro","time":{"created":1779012001000},"finish":"tool_calls"}`,
		1779012001000,
	)
	seedTeleAgentPart(t, db, "prt_1", "ses_1", "msg_1",
		`{"type":"tool","callID":"call_1","tool":"bash","state":{"status":"error","error":"Error: permission denied","input":{"command":"ls"}},"output":"partial"}`,
		1779012001100,
	)
	store := openTeleAgentTestStore(t, dbPath)
	_, msgs, err := store.LoadSession(context.Background(), "ses_1", "m")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Len(t, msgs[0].ToolCalls, 1)
	require.Len(t, msgs[0].ToolCalls[0].ResultEvents, 1)
	ev := msgs[0].ToolCalls[0].ResultEvents[0]
	assert.Equal(t, "error", ev.Status)
	assert.Equal(t, "Error: permission denied", ev.Content,
		"error content must come from state.error, not output")
}

func TestLoadSession_ToolCallRunning(t *testing.T) {
	dbPath, db := newTeleAgentSQLiteTestDB(t)
	seedTeleAgentSession(t, db, "ses_1", "Tool task", "/tmp/work", nil, 1, 2)
	seedTeleAgentMessage(t, db, "msg_1", "ses_1",
		`{"role":"assistant","modelID":"chat-pro","time":{"created":1779012001000},"finish":"tool_calls"}`,
		1779012001000,
	)
	seedTeleAgentPart(t, db, "prt_1", "ses_1", "msg_1",
		`{"type":"tool","callID":"call_1","tool":"bash","state":{"status":"running","input":{"command":"sleep 10"}}}`,
		1779012001100,
	)
	store := openTeleAgentTestStore(t, dbPath)
	_, msgs, err := store.LoadSession(context.Background(), "ses_1", "m")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Len(t, msgs[0].ToolCalls, 1)
	require.Len(t, msgs[0].ToolCalls[0].ResultEvents, 1)
	assert.Equal(t, "running", msgs[0].ToolCalls[0].ResultEvents[0].Status)
}

func TestLoadSession_MultipleToolCallsInOneTurn(t *testing.T) {
	dbPath, db := newTeleAgentSQLiteTestDB(t)
	seedTeleAgentSession(t, db, "ses_1", "Tool task", "/tmp/work", nil, 1, 2)
	seedTeleAgentMessage(t, db, "msg_1", "ses_1",
		`{"role":"assistant","modelID":"chat-pro","time":{"created":1779012001000},"finish":"tool_calls"}`,
		1779012001000,
	)
	seedTeleAgentPart(t, db, "prt_1", "ses_1", "msg_1",
		`{"type":"tool","callID":"call_1","tool":"read","state":{"status":"completed","input":{"path":"/tmp/a"}},"output":"a"}`,
		1779012001100,
	)
	seedTeleAgentPart(t, db, "prt_2", "ses_1", "msg_1",
		`{"type":"tool","callID":"call_2","tool":"read","state":{"status":"completed","input":{"path":"/tmp/b"}},"output":"b"}`,
		1779012001200,
	)
	store := openTeleAgentTestStore(t, dbPath)
	_, msgs, err := store.LoadSession(context.Background(), "ses_1", "m")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.True(t, msgs[0].HasToolUse)
	require.Len(t, msgs[0].ToolCalls, 2)
	assert.Equal(t, "call_1", msgs[0].ToolCalls[0].ToolUseID)
	require.Len(t, msgs[0].ToolCalls[0].ResultEvents, 1)
	assert.Equal(t, "call_2", msgs[0].ToolCalls[1].ToolUseID)
	require.Len(t, msgs[0].ToolCalls[1].ResultEvents, 1)
}

func TestLoadSession_TokenUsage(t *testing.T) {
	dbPath, db := newTeleAgentSQLiteTestDB(t)
	seedTeleAgentSession(t, db, "ses_1", "Token task", "/tmp/work", nil, 1, 2)
	seedTeleAgentMessage(t, db, "msg_1", "ses_1",
		`{"role":"assistant","modelID":"chat-pro","time":{"created":1779012001000},"cost":0.001,"tokens":{"total":120,"input":100,"output":20,"reasoning":5,"cache":{"read":30,"write":10}},"finish":"stop"}`,
		1779012001000,
	)
	seedTeleAgentPart(t, db, "prt_1", "ses_1", "msg_1",
		`{"type":"text","text":"answer"}`,
		1779012001100,
	)
	store := openTeleAgentTestStore(t, dbPath)
	sess, msgs, err := store.LoadSession(context.Background(), "ses_1", "m")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.True(t, msgs[0].HasContextTokens)
	assert.True(t, msgs[0].HasOutputTokens)
	assert.Equal(t, 130, msgs[0].ContextTokens,
		"context tokens = input + cache.read")
	assert.Equal(t, 20, msgs[0].OutputTokens)
	// Session rollup: TotalOutputTokens sums per-message OutputTokens;
	// PeakContextTokens is the peak per-message ContextTokens.
	assert.True(t, sess.HasTotalOutputTokens)
	assert.Equal(t, 20, sess.TotalOutputTokens)
	assert.True(t, sess.HasPeakContextTokens)
	assert.Equal(t, 130, sess.PeakContextTokens)
}

func TestLoadSession_StopReasonNormalization(t *testing.T) {
	cases := []struct {
		name   string
		finish string
		want   string
	}{
		{name: "tool_calls", finish: "tool_calls", want: "tool_calls"},
		{name: "tool-calls", finish: "tool-calls", want: "tool_calls"},
		{name: "stop", finish: "stop", want: "stop"},
		{name: "length", finish: "length", want: "length"},
		{name: "error", finish: "error", want: "error"},
		{name: "empty", finish: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbPath, db := newTeleAgentSQLiteTestDB(t)
			seedTeleAgentSession(t, db, "ses_1", "Task", "/tmp/work", nil, 1, 2)
			finishField := ""
			if tc.finish != "" {
				finishField = `,"finish":"` + tc.finish + `"`
			}
			seedTeleAgentMessage(t, db, "msg_1", "ses_1",
				`{"role":"assistant","modelID":"chat-pro","time":{"created":1779012001000}`+finishField+`}`,
				1779012001000,
			)
			seedTeleAgentPart(t, db, "prt_1", "ses_1", "msg_1",
				`{"type":"text","text":"answer"}`,
				1779012001100,
			)
			store := openTeleAgentTestStore(t, dbPath)
			_, msgs, err := store.LoadSession(context.Background(), "ses_1", "m")
			require.NoError(t, err)
			require.Len(t, msgs, 1)
			assert.Equal(t, tc.want, msgs[0].StopReason)
		})
	}
}

func TestLoadSession_SubagentLineage(t *testing.T) {
	dbPath, db := newTeleAgentSQLiteTestDB(t)
	parentID := "ses_parent"
	seedTeleAgentSession(
		t, db, "ses_parent", "Parent task", "/tmp/work", nil, 1, 2,
	)
	seedTeleAgentSession(
		t, db, "ses_child", "Child task (@general subagent)", "/tmp/work", &parentID,
		3, 4,
	)
	seedTeleAgentMessage(t, db, "msg_1", "ses_child",
		`{"role":"user","time":{"created":3}}`, 3,
	)
	seedTeleAgentPart(t, db, "prt_1", "ses_1", "msg_1",
		`{"type":"text","text":"child prompt"}`, 3,
	)
	store := openTeleAgentTestStore(t, dbPath)
	sess, _, err := store.LoadSession(context.Background(), "ses_child", "m")
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, "teleagent:ses_parent", sess.ParentSessionID)
	assert.Equal(t, RelSubagent, sess.RelationshipType)
}

func TestLoadSession_EmptySession(t *testing.T) {
	dbPath, db := newTeleAgentSQLiteTestDB(t)
	seedTeleAgentSession(t, db, "ses_1", "Empty", "/tmp/work", nil, 1, 2)
	store := openTeleAgentTestStore(t, dbPath)
	sess, msgs, err := store.LoadSession(context.Background(), "ses_1", "m")
	require.NoError(t, err)
	assert.Nil(t, sess, "empty session must return nil ParsedSession")
	assert.Nil(t, msgs, "empty session must return nil messages")
}

func TestLoadSession_SkipsStepAndCompactionMarkers(t *testing.T) {
	dbPath, db := newTeleAgentSQLiteTestDB(t)
	seedTeleAgentSession(t, db, "ses_1", "Markers", "/tmp/work", nil, 1, 2)
	seedTeleAgentMessage(t, db, "msg_1", "ses_1",
		`{"role":"assistant","modelID":"chat-pro","time":{"created":1},"finish":"stop"}`,
		1,
	)
	for i, part := range []string{
		`{"type":"step-start"}`,
		`{"type":"text","text":"answer"}`,
		`{"type":"step-finish"}`,
		`{"type":"compaction"}`,
		`{"type":"file"}`,
	} {
		seedTeleAgentPart(t, db, "prt_"+strconv.Itoa(i+1), "ses_1", "msg_1", part, 1)
	}
	store := openTeleAgentTestStore(t, dbPath)
	_, msgs, err := store.LoadSession(context.Background(), "ses_1", "m")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "answer", msgs[0].Content,
		"only the text part's content must surface; markers are skipped")
}
