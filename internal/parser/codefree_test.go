package parser

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCodefreeDecodeWorkingDir(t *testing.T) {
	cases := []struct {
		name string
		uri  string
		want string
	}{
		{
			name: "windows drive letter encoded",
			uri:  "file:///d%3A/Temp/Download/sub-exam",
			want: `d:\Temp\Download\sub-exam`,
		},
		{
			name: "windows c drive",
			uri:  "file:///c%3A/Users/test/project",
			want: `c:\Users\test\project`,
		},
		{
			name: "unix path",
			uri:  "file:///home/user/project",
			want: `\home\user\project`,
		},
		{
			name: "empty",
			uri:  "",
			want: "",
		},
		{
			name: "no file prefix",
			uri:  "/tmp/work",
			want: `\tmp\work`,
		},
		{
			name: "file:// double slash",
			uri:  "file://localhost/tmp",
			want: `\localhost\tmp`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := codefreeDecodeWorkingDir(tc.uri)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestLoadCodeFreeSession_UserAndAssistant(t *testing.T) {
	dbPath, db := newCodeFreeSQLiteTestDB(t)
	stateJSON := `{
		"schemaVersion": 1,
		"session": {
			"sessionId": "uuid-001",
			"startTime": 1779012000000,
			"modifiedTime": 1779012030000,
			"summary": "Test session",
			"workingDirectory": "file:///d%3A/Temp/project"
		},
		"chats": [
			{"chatId": "chat-1", "agent": {"uri": "codefree-agent://builtin/spec"}}
		],
		"completedTurns": [
			{
				"history": {
					"chatId": "chat-1",
					"turnId": "request_001",
					"userMessage": {"text": "Hello, can you help me?"},
					"parts": [
						{"type": "text", "text": "Sure! I can help with that."}
					]
				},
				"selection": {"model": "GLM-5.2"},
				"usage": {"inputTokens": 100, "outputTokens": 50, "cacheReadTokens": 20, "model": "GLM-5.2"}
			}
		],
		"inFlightTurns": []
	}`
	seedCodeFreeMetadata(t, db, codefreeStateKey, stateJSON)
	db.Close()

	store, err := OpenCodeFreeSQLiteStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	sess, msgs, err := loadCodeFreeSession(
		context.Background(), store.db, dbPath, "uuid-001", "test-machine", "",
	)
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.Len(t, msgs, 2)

	// Session metadata.
	assert.Equal(t, "codefree:uuid-001", sess.ID)
	assert.Equal(t, "uuid-001", sess.SourceSessionID)
	assert.Equal(t, AgentCodeFree, sess.Agent)
	assert.Equal(t, "", sess.AgentLabel,
		"AgentLabel must be empty so the frontend falls back to the CodeFree label")
	assert.Equal(t, "Test session", sess.SessionName)
	assert.Equal(t, `d:\Temp\project`, sess.Cwd)
	assert.Equal(t, "test-machine", sess.Machine)
	assert.Equal(t, time.UnixMilli(1779012000000), sess.StartedAt)
	assert.Equal(t, time.UnixMilli(1779012030000), sess.EndedAt)
	assert.Equal(t, 2, sess.MessageCount)
	assert.Equal(t, 1, sess.UserMessageCount)
	assert.Equal(t, "Hello, can you help me?", sess.FirstMessage)

	// User message.
	assert.Equal(t, RoleUser, msgs[0].Role)
	assert.Equal(t, "Hello, can you help me?", msgs[0].Content)
	// Timestamp: single turn falls back to startTime.
	assert.Equal(t, time.UnixMilli(1779012000000), msgs[0].Timestamp)

	// Assistant message.
	assert.Equal(t, RoleAssistant, msgs[1].Role)
	assert.Equal(t, "Sure! I can help with that.", msgs[1].Content)
	assert.Equal(t, "GLM-5.2", msgs[1].Model)
	assert.True(t, msgs[1].HasContextTokens)
	assert.Equal(t, 100, msgs[1].ContextTokens)
	assert.True(t, msgs[1].HasOutputTokens)
	assert.Equal(t, 50, msgs[1].OutputTokens)
	assert.Equal(t, time.UnixMilli(1779012000000), msgs[1].Timestamp)

	// Session token rollup.
	assert.True(t, sess.HasTotalOutputTokens)
	assert.Equal(t, 50, sess.TotalOutputTokens)
	assert.True(t, sess.HasPeakContextTokens)
	assert.Equal(t, 100, sess.PeakContextTokens)
}

func TestLoadCodeFreeSession_ToolCall(t *testing.T) {
	dbPath, db := newCodeFreeSQLiteTestDB(t)
	stateJSON := `{
		"session": {
			"sessionId": "uuid-002",
			"startTime": 1000,
			"modifiedTime": 2000,
			"summary": "Tool session",
			"workingDirectory": "file:///tmp"
		},
		"chats": [{"agent": {"uri": "codefree-agent://builtin/spec"}}],
		"completedTurns": [
			{
				"history": {
					"userMessage": {"text": "Read the file"},
					"parts": [
						{"type": "toolCall", "toolCallId": "call_1", "toolId": "read_file", "displayName": "Read File", "input": {"filePath": "/tmp/test.go"}, "disposition": "completed", "result": {"content": "file contents here"}}
					]
				},
				"usage": {"inputTokens": 200, "outputTokens": 30, "model": "GLM-5.2"}
			}
		],
		"inFlightTurns": []
	}`
	seedCodeFreeMetadata(t, db, codefreeStateKey, stateJSON)
	db.Close()

	store, err := OpenCodeFreeSQLiteStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	sess, msgs, err := loadCodeFreeSession(
		context.Background(), store.db, dbPath, "uuid-002", "m", "",
	)
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.Len(t, msgs, 2)

	// Assistant message with tool call.
	assert.Equal(t, RoleAssistant, msgs[1].Role)
	assert.True(t, msgs[1].HasToolUse)
	require.Len(t, msgs[1].ToolCalls, 1)
	tc := msgs[1].ToolCalls[0]
	assert.Equal(t, "call_1", tc.ToolUseID)
	assert.Equal(t, "read_file", tc.ToolName)
	assert.NotEmpty(t, tc.InputJSON)
	require.Len(t, tc.ResultEvents, 1)
	assert.Equal(t, "completed", tc.ResultEvents[0].Status)
	assert.Equal(t, "file contents here", tc.ResultEvents[0].Content)
}

func TestLoadCodeFreeSession_MultipleTextParts(t *testing.T) {
	dbPath, db := newCodeFreeSQLiteTestDB(t)
	stateJSON := `{
		"session": {"sessionId": "uuid-003", "startTime": 1, "modifiedTime": 2, "summary": "Multi-text", "workingDirectory": ""},
		"chats": [{"agent": {"uri": "codefree-agent://builtin/spec"}}],
		"completedTurns": [
			{
				"history": {
					"userMessage": {"text": "Explain"},
					"parts": [
						{"type": "text", "text": "First part."},
						{"type": "text", "text": "Second part."}
					]
				},
				"usage": {"model": "GLM-5.2"}
			}
		],
		"inFlightTurns": []
	}`
	seedCodeFreeMetadata(t, db, codefreeStateKey, stateJSON)
	db.Close()

	store, err := OpenCodeFreeSQLiteStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	_, msgs, err := loadCodeFreeSession(
		context.Background(), store.db, dbPath, "uuid-003", "m", "",
	)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.Equal(t, "First part.\nSecond part.", msgs[1].Content)
}

func TestLoadCodeFreeSession_EmptyTurns(t *testing.T) {
	dbPath, db := newCodeFreeSQLiteTestDB(t)
	stateJSON := `{
		"session": {"sessionId": "uuid-empty", "startTime": 1, "modifiedTime": 2, "summary": "Empty", "workingDirectory": ""},
		"chats": [{"agent": {"uri": "codefree-agent://builtin/spec"}}],
		"completedTurns": [],
		"inFlightTurns": []
	}`
	seedCodeFreeMetadata(t, db, codefreeStateKey, stateJSON)
	db.Close()

	store, err := OpenCodeFreeSQLiteStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	sess, msgs, err := loadCodeFreeSession(
		context.Background(), store.db, dbPath, "uuid-empty", "m", "",
	)
	require.NoError(t, err)
	assert.Nil(t, sess, "empty turns must return nil session")
	assert.Nil(t, msgs)
}

func TestLoadCodeFreeSession_MissingStateKey(t *testing.T) {
	dbPath, db := newCodeFreeSQLiteTestDB(t)
	seedCodeFreeMetadata(t, db, "other.key", "value")
	db.Close()

	store, err := OpenCodeFreeSQLiteStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	sess, msgs, err := loadCodeFreeSession(
		context.Background(), store.db, dbPath, "uuid-missing", "m", "",
	)
	require.NoError(t, err)
	assert.Nil(t, sess)
	assert.Nil(t, msgs)
}

func TestLoadCodeFreeSession_EmptyUserMessageSkipped(t *testing.T) {
	dbPath, db := newCodeFreeSQLiteTestDB(t)
	stateJSON := `{
		"session": {"sessionId": "uuid-no-user", "startTime": 1, "modifiedTime": 2, "summary": "No user msg", "workingDirectory": ""},
		"chats": [{"agent": {"uri": "codefree-agent://builtin/spec"}}],
		"completedTurns": [
			{
				"history": {
					"userMessage": {"text": ""},
					"parts": [
						{"type": "text", "text": "Assistant response only."}
					]
				},
				"usage": {"model": "GLM-5.2"}
			}
		],
		"inFlightTurns": []
	}`
	seedCodeFreeMetadata(t, db, codefreeStateKey, stateJSON)
	db.Close()

	store, err := OpenCodeFreeSQLiteStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	_, msgs, err := loadCodeFreeSession(
		context.Background(), store.db, dbPath, "uuid-no-user", "m", "",
	)
	require.NoError(t, err)
	require.Len(t, msgs, 1, "empty user message must be skipped, only assistant emitted")
	assert.Equal(t, RoleAssistant, msgs[0].Role)
}

func TestLoadCodeFreeSession_InFlightTurns(t *testing.T) {
	dbPath, db := newCodeFreeSQLiteTestDB(t)
	stateJSON := `{
		"session": {"sessionId": "uuid-inflight", "startTime": 1, "modifiedTime": 2, "summary": "In-flight", "workingDirectory": ""},
		"chats": [{"agent": {"uri": "codefree-agent://builtin/spec"}}],
		"completedTurns": [
			{
				"history": {
					"userMessage": {"text": "First question"},
					"parts": [{"type": "text", "text": "First answer"}]
				},
				"usage": {"model": "GLM-5.2"}
			}
		],
		"inFlightTurns": [
			{
				"history": {
					"userMessage": {"text": "Second question (in flight)"},
					"parts": [{"type": "text", "text": "Partial answer"}]
				}
			}
		]
	}`
	seedCodeFreeMetadata(t, db, codefreeStateKey, stateJSON)
	db.Close()

	store, err := OpenCodeFreeSQLiteStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	sess, msgs, err := loadCodeFreeSession(
		context.Background(), store.db, dbPath, "uuid-inflight", "m", "",
	)
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.Len(t, msgs, 4, "2 completed + 2 in-flight messages")
	assert.Equal(t, "First question", msgs[0].Content)
	assert.Equal(t, "First answer", msgs[1].Content)
	assert.Equal(t, "Second question (in flight)", msgs[2].Content)
	assert.Equal(t, "Partial answer", msgs[3].Content)
}

func TestLoadCodeFreeSession_ToolCallNoResult(t *testing.T) {
	dbPath, db := newCodeFreeSQLiteTestDB(t)
	stateJSON := `{
		"session": {"sessionId": "uuid-noresult", "startTime": 1, "modifiedTime": 2, "summary": "No result", "workingDirectory": ""},
		"chats": [{"agent": {"uri": "codefree-agent://builtin/spec"}}],
		"completedTurns": [
			{
				"history": {
					"userMessage": {"text": "Do something"},
					"parts": [
						{"type": "toolCall", "toolCallId": "call_1", "toolId": "bash", "input": {"command": "ls"}}
					]
				}
			}
		],
		"inFlightTurns": []
	}`
	seedCodeFreeMetadata(t, db, codefreeStateKey, stateJSON)
	db.Close()

	store, err := OpenCodeFreeSQLiteStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	_, msgs, err := loadCodeFreeSession(
		context.Background(), store.db, dbPath, "uuid-noresult", "m", "",
	)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.True(t, msgs[1].HasToolUse)
	require.Len(t, msgs[1].ToolCalls, 1)
	assert.Equal(t, "bash", msgs[1].ToolCalls[0].ToolName)
	assert.Empty(t, msgs[1].ToolCalls[0].ResultEvents,
		"toolCall without result must have no result events")
}

func TestLoadCodeFreeSession_MissingSessionObject(t *testing.T) {
	dbPath, db := newCodeFreeSQLiteTestDB(t)
	stateJSON := `{"schemaVersion": 1, "chats": [], "completedTurns": []}`
	seedCodeFreeMetadata(t, db, codefreeStateKey, stateJSON)
	db.Close()

	store, err := OpenCodeFreeSQLiteStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	sess, _, err := loadCodeFreeSession(
		context.Background(), store.db, dbPath, "uuid-nosession", "m", "",
	)
	require.NoError(t, err)
	assert.Nil(t, sess, "missing session object must return nil")
}

func TestLoadCodeFreeSession_NilDB(t *testing.T) {
	sess, msgs, err := loadCodeFreeSession(
		context.Background(), nil, "", "uuid", "m", "",
	)
	require.Error(t, err)
	assert.Nil(t, sess)
	assert.Nil(t, msgs)
}

func TestLoadCodeFreeSession_MultipleTurns(t *testing.T) {
	dbPath, db := newCodeFreeSQLiteTestDB(t)
	stateJSON := `{
		"session": {"sessionId": "uuid-multi", "startTime": 1000, "modifiedTime": 5000, "summary": "Multi-turn", "workingDirectory": "file:///tmp"},
		"chats": [{"agent": {"uri": "codefree-agent://builtin/explore"}}],
		"completedTurns": [
			{
				"history": {"userMessage": {"text": "Q1"}, "parts": [{"type": "text", "text": "A1"}]},
				"usage": {"inputTokens": 10, "outputTokens": 5, "model": "GLM-5.2"}
			},
			{
				"history": {"userMessage": {"text": "Q2"}, "parts": [{"type": "text", "text": "A2"}]},
				"usage": {"inputTokens": 20, "outputTokens": 8, "model": "GLM-5.2"}
			},
			{
				"history": {"userMessage": {"text": "Q3"}, "parts": [{"type": "text", "text": "A3"}]},
				"usage": {"inputTokens": 30, "outputTokens": 12, "model": "GLM-5.2"}
			}
		],
		"inFlightTurns": []
	}`
	seedCodeFreeMetadata(t, db, codefreeStateKey, stateJSON)
	db.Close()

	store, err := OpenCodeFreeSQLiteStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	sess, msgs, err := loadCodeFreeSession(
		context.Background(), store.db, dbPath, "uuid-multi", "m", "",
	)
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.Len(t, msgs, 6, "3 turns x 2 messages each")
	assert.Equal(t, 3, sess.UserMessageCount)
	assert.Equal(t, 6, sess.MessageCount)
	assert.Equal(t, "Q1", sess.FirstMessage)
	// Token rollup: TotalOutput = 5+8+12 = 25, PeakContext = max(10,20,30) = 30.
	assert.Equal(t, 25, sess.TotalOutputTokens)
	assert.Equal(t, 30, sess.PeakContextTokens)
	assert.Equal(t, "", sess.AgentLabel)

	// Timestamps: 3 turns, startTime=1000ms, modifiedTime=5000ms.
	// stepDuration = (5000-1000)/3 = 1333.33ms per turn.
	// Turn 0: startTime + 0*step = 1000ms
	// Turn 1: startTime + 1*step = 2333.33ms
	// Turn 2: startTime + 2*step = 3666.67ms
	startMS := int64(1000)
	endMS := int64(5000)
	step := time.Duration(endMS-startMS) * time.Millisecond / 3
	assert.Equal(t, time.UnixMilli(startMS), msgs[0].Timestamp, "turn 0 user msg")
	assert.Equal(t, time.UnixMilli(startMS), msgs[1].Timestamp, "turn 0 assistant msg")
	assert.Equal(t, time.UnixMilli(startMS).Add(step), msgs[2].Timestamp, "turn 1 user msg")
	assert.Equal(t, time.UnixMilli(startMS).Add(step), msgs[3].Timestamp, "turn 1 assistant msg")
	assert.Equal(t, time.UnixMilli(startMS).Add(2*step), msgs[4].Timestamp, "turn 2 user msg")
	assert.Equal(t, time.UnixMilli(startMS).Add(2*step), msgs[5].Timestamp, "turn 2 assistant msg")
}

func TestParseCodeFreeStateJSON_TimestampConversion(t *testing.T) {
	dbPath, db := newCodeFreeSQLiteTestDB(t)
	stateJSON := `{
		"session": {"sessionId": "uuid-ts", "startTime": 1779012000000, "modifiedTime": 1779012099999, "summary": "TS", "workingDirectory": ""},
		"chats": [{"agent": {"uri": "codefree-agent://builtin/spec"}}],
		"completedTurns": [
			{"history": {"userMessage": {"text": "hi"}, "parts": [{"type": "text", "text": "hello"}]}}
		],
		"inFlightTurns": []
	}`
	seedCodeFreeMetadata(t, db, codefreeStateKey, stateJSON)
	db.Close()

	store, err := OpenCodeFreeSQLiteStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	sess, _, err := loadCodeFreeSession(
		context.Background(), store.db, dbPath, "uuid-ts", "m", "",
	)
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, time.UnixMilli(1779012000000), sess.StartedAt)
	assert.Equal(t, time.UnixMilli(1779012099999), sess.EndedAt)
}

// Verify that loadCodeFreeSession works through the provider's Parse path.
func TestCodeFreeProvider_ParsePath(t *testing.T) {
	root := t.TempDir()
	sessionID := "uuid-provider-test"
	sessionDir := filepath.Join(root, sessionID)
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))
	dbPath := filepath.Join(sessionDir, codefreeSQLiteDBName)
	_, db := newCodeFreeSQLiteTestDBAt(t, dbPath)
	stateJSON := `{
		"session": {"sessionId": "uuid-provider-test", "startTime": 1000, "modifiedTime": 2000, "summary": "Provider test", "workingDirectory": "file:///tmp"},
		"chats": [{"agent": {"uri": "codefree-agent://builtin/spec"}}],
		"completedTurns": [
			{"history": {"userMessage": {"text": "test"}, "parts": [{"type": "text", "text": "ok"}]}}
		],
		"inFlightTurns": []
	}`
	seedCodeFreeMetadata(t, db, codefreeStateKey, stateJSON)
	db.Close()

	store, err := OpenCodeFreeSQLiteStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	sess, msgs, err := loadCodeFreeSession(
		context.Background(), store.db, dbPath, sessionID, "machine-1", "",
	)
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, "codefree:uuid-provider-test", sess.ID)
	require.Len(t, msgs, 2)
}

func TestCodefreeLogTimestampMap_Parse(t *testing.T) {
	m := make(codefreeLogTimestampMap)
	lines := []string{
		"2026-11-09 16:32:05.354 [info] [CodeFree] Turn started session=sess-1 turn=request_aaa authRevision=5 stage=preflight",
		"2026-11-09 16:32:59.322 [info] [CodeFree] Turn terminal session=sess-1 turn=request_aaa authRevision=5 status=completed",
		"2026-11-09 16:35:03.247 [info] [CodeFree] Turn started session=sess-1 turn=request_bbb authRevision=5 stage=preflight",
		"2026-11-09 16:46:11.295 [info] [CodeFree] Turn started session=sess-2 turn=request_ccc authRevision=5 stage=preflight",
		"some other log line without turn info",
	}
	loc := codefreeLogTimezoneDefault()
	for _, line := range lines {
		codefreeParseLogLineForTurnTimestamp(line, loc, m)
	}

	// Turn aaa: started at 16:32:05.354 GMT+8, converted to 08:32:05.354 UTC.
	ts, ok := m.lookupTurnTimestamp("sess-1", "request_aaa")
	require.True(t, ok)
	assert.Equal(t, "2026-11-09 08:32:05.354", ts.Format(codefreeLogTimestampLayout))

	// Turn bbb: started at 16:35:03.247 GMT+8, converted to 08:35:03.247 UTC.
	ts, ok = m.lookupTurnTimestamp("sess-1", "request_bbb")
	require.True(t, ok)
	assert.Equal(t, "2026-11-09 08:35:03.247", ts.Format(codefreeLogTimestampLayout))

	// Turn ccc: different session, 16:46:11.295 GMT+8 → 08:46:11.295 UTC.
	ts, ok = m.lookupTurnTimestamp("sess-2", "request_ccc")
	require.True(t, ok)
	assert.Equal(t, "2026-11-09 08:46:11.295", ts.Format(codefreeLogTimestampLayout))

	// Unknown turn.
	_, ok = m.lookupTurnTimestamp("sess-1", "request_unknown")
	assert.False(t, ok)

	// Empty turnID.
	_, ok = m.lookupTurnTimestamp("sess-1", "")
	assert.False(t, ok)
}

func TestCodefreeLogTimestampMap_EmptyMap(t *testing.T) {
	var m codefreeLogTimestampMap // nil map
	_, ok := m.lookupTurnTimestamp("sess-1", "turn-1")
	assert.False(t, ok)
}

func TestLoadCodeFreeLogTimestamps_NoLogsDir(t *testing.T) {
	// A temp dir with no logs/ subdirectory should return an empty
	// map and no error.
	tmp := t.TempDir()
	root := filepath.Join(tmp, "agentSessionData")
	require.NoError(t, os.MkdirAll(root, 0o755))

	m, err := loadCodeFreeLogTimestamps(root, nil)
	require.NoError(t, err)
	assert.Empty(t, m)
}

func TestLoadCodeFreeLogTimestamps_WithLogFile(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "agentSessionData")
	require.NoError(t, os.MkdirAll(root, 0o755))

	// Create logs/<dir>/agenthost.log as a sibling of root.
	logDir := filepath.Join(tmp, "logs", "20261109T145816")
	require.NoError(t, os.MkdirAll(logDir, 0o755))
	logContent := `2026-11-09 16:32:05.354 [info] [CodeFree] Turn started session=sess-log turn=request_t1 authRevision=5 stage=preflight
2026-11-09 16:32:59.322 [info] [CodeFree] Turn terminal session=sess-log turn=request_t1 authRevision=5 status=completed
2026-11-09 16:35:03.247 [info] [CodeFree] Turn started session=sess-log turn=request_t2 authRevision=5 stage=preflight
`
	logPath := filepath.Join(logDir, "agenthost.log")
	require.NoError(t, os.WriteFile(logPath, []byte(logContent), 0o644))

	m, err := loadCodeFreeLogTimestamps(root, nil)
	require.NoError(t, err)
	require.Len(t, m, 2)

	ts, ok := m.lookupTurnTimestamp("sess-log", "request_t1")
	require.True(t, ok)
	// Log time is GMT+8 (16:32:05.354), stored as UTC (08:32:05.354).
	assert.Equal(t, "2026-11-09 08:32:05.354", ts.Format(codefreeLogTimestampLayout))
	assert.Equal(t, time.UTC, ts.Location())

	ts, ok = m.lookupTurnTimestamp("sess-log", "request_t2")
	require.True(t, ok)
	// Log time is GMT+8 (16:35:03.247), stored as UTC (08:35:03.247).
	assert.Equal(t, "2026-11-09 08:35:03.247", ts.Format(codefreeLogTimestampLayout))
	assert.Equal(t, time.UTC, ts.Location())
}

func TestLoadCodeFreeLogTimestamps_TimezoneConversion(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "agentSessionData")
	require.NoError(t, os.MkdirAll(root, 0o755))

	logDir := filepath.Join(tmp, "logs", "20261109T145816")
	require.NoError(t, os.MkdirAll(logDir, 0o755))
	// Log time 2026-11-09 16:32:05.354 is GMT+8 (Asia/Shanghai).
	// Expected UTC: 2026-11-09 08:32:05.354.
	logContent := "2026-11-09 16:32:05.354 [info] [CodeFree] Turn started session=sess-tz turn=request_tz1 authRevision=5 stage=preflight\n"
	require.NoError(t, os.WriteFile(
		filepath.Join(logDir, "agenthost.log"), []byte(logContent), 0o644,
	))

	m, err := loadCodeFreeLogTimestamps(root, nil)
	require.NoError(t, err)
	require.Len(t, m, 1)

	ts, ok := m.lookupTurnTimestamp("sess-tz", "request_tz1")
	require.True(t, ok)
	// The stored timestamp must be UTC, not the raw GMT+8 value.
	assert.Equal(t, time.UTC, ts.Location())
	assert.Equal(t, "2026-11-09 08:32:05.354", ts.Format(codefreeLogTimestampLayout),
		"GMT+8 16:32:05.354 must convert to UTC 08:32:05.354")
}

func TestLoadCodeFreeSession_WithLogTimestamps(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "agentSessionData")
	require.NoError(t, os.MkdirAll(root, 0o755))

	// Create a logs directory with a real turn timestamp.
	logDir := filepath.Join(tmp, "logs", "20261109T145816")
	require.NoError(t, os.MkdirAll(logDir, 0o755))
	logContent := "2026-11-09 16:32:05.354 [info] [CodeFree] Turn started session=uuid-log turn=request_log1 authRevision=5 stage=preflight\n"
	require.NoError(t, os.WriteFile(
		filepath.Join(logDir, "agenthost.log"), []byte(logContent), 0o644,
	))

	// Create a session DB with a turnId that matches the log.
	sessionDir := filepath.Join(root, "uuid-log")
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))
	dbPath := filepath.Join(sessionDir, codefreeSQLiteDBName)
	_, db := newCodeFreeSQLiteTestDBAt(t, dbPath)
	stateJSON := `{
		"session": {"sessionId": "uuid-log", "startTime": 1000, "modifiedTime": 9999999999999, "summary": "Log test", "workingDirectory": ""},
		"chats": [{"agent": {"uri": "codefree-agent://builtin/spec"}}],
		"completedTurns": [
			{"history": {"turnId": "request_log1", "userMessage": {"text": "hi"}, "parts": [{"type": "text", "text": "hello"}]}}
		],
		"inFlightTurns": []
	}`
	seedCodeFreeMetadata(t, db, codefreeStateKey, stateJSON)
	db.Close()

	store, err := OpenCodeFreeSQLiteStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	sess, msgs, err := loadCodeFreeSession(
		context.Background(), store.db, dbPath, "uuid-log", "m", root,
	)
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.Len(t, msgs, 2)

	// The timestamp should come from the log (2026-11-09 16:32:05.354
	// GMT+8), converted to UTC (08:32:05.354), not from the session-
	// level interpolation (which would be time.UnixMilli(1000) =
	// 1970-01-01 00:00:01).
	expectedLogTS := codefreeLogTimeToUTC(t, "2026-11-09 16:32:05.354")
	assert.Equal(t, expectedLogTS, msgs[0].Timestamp, "user message should use log timestamp (UTC)")
	assert.Equal(t, expectedLogTS, msgs[1].Timestamp, "assistant message should use log timestamp (UTC)")
}

func TestResolveTurnTimestamps_AllFromLog(t *testing.T) {
	ts0, _ := time.Parse(codefreeLogTimestampLayout, "2026-11-09 10:00:00.000")
	ts1, _ := time.Parse(codefreeLogTimestampLayout, "2026-11-09 10:05:00.000")
	ts2, _ := time.Parse(codefreeLogTimestampLayout, "2026-11-09 10:10:00.000")
	logTS := codefreeLogTimestampMap{
		"s/t1": {Started: ts0},
		"s/t2": {Started: ts1},
		"s/t3": {Started: ts2},
	}

	result := resolveTurnTimestamps(logTS, "s", []string{"t1", "t2", "t3"},
		time.UnixMilli(1000), time.UnixMilli(9999999999999))

	require.Len(t, result, 3)
	assert.Equal(t, "2026-11-09 10:00:00.000", result[0].Format(codefreeLogTimestampLayout))
	assert.Equal(t, "2026-11-09 10:05:00.000", result[1].Format(codefreeLogTimestampLayout))
	assert.Equal(t, "2026-11-09 10:10:00.000", result[2].Format(codefreeLogTimestampLayout))
}

func TestResolveTurnTimestamps_PartialLog_FillFromPreceding(t *testing.T) {
	// Turn 0 and 2 have log timestamps; turn 1 is missing.
	// Turn 1 should be linearly interpolated between ts0 and ts2.
	ts0, _ := time.Parse(codefreeLogTimestampLayout, "2026-11-09 10:00:00.000")
	ts2, _ := time.Parse(codefreeLogTimestampLayout, "2026-11-09 10:10:00.000")
	logTS := codefreeLogTimestampMap{
		"s/t0": {Started: ts0},
		"s/t2": {Started: ts2},
	}

	result := resolveTurnTimestamps(logTS, "s", []string{"t0", "t1", "t2"},
		time.UnixMilli(1000), time.UnixMilli(9999999999999))

	require.Len(t, result, 3)
	assert.Equal(t, ts0, result[0], "turn 0 from log")
	// Turn 1: linearly interpolated between ts0 and ts2 (midpoint).
	step := ts2.Sub(ts0) / 2
	assert.Equal(t, ts0.Add(step), result[1], "turn 1 should be interpolated between ts0 and ts2")
	assert.Equal(t, ts2, result[2], "turn 2 from log")
}

func TestResolveTurnTimestamps_PartialLog_FillFromFollowing(t *testing.T) {
	// Turn 0 is missing; turn 1 and 2 have log timestamps.
	// Turn 0 is a leading gap — should inherit from turn 1 (the first
	// known turn).
	ts1, _ := time.Parse(codefreeLogTimestampLayout, "2026-11-09 10:05:00.000")
	ts2, _ := time.Parse(codefreeLogTimestampLayout, "2026-11-09 10:10:00.000")
	logTS := codefreeLogTimestampMap{
		"s/t1": {Started: ts1},
		"s/t2": {Started: ts2},
	}

	result := resolveTurnTimestamps(logTS, "s", []string{"t0", "t1", "t2"},
		time.UnixMilli(1000), time.UnixMilli(9999999999999))

	require.Len(t, result, 3)
	// Turn 0: leading gap, carries the first known timestamp (ts1).
	assert.Equal(t, ts1, result[0], "turn 0 should inherit from following turn 1")
	assert.Equal(t, ts1, result[1], "turn 1 from log")
	assert.Equal(t, ts2, result[2], "turn 2 from log")
}

func TestResolveTurnTimestamps_PartialLog_MiddleGap(t *testing.T) {
	// Turns 0, 1, 4 have log timestamps; turns 2, 3 are missing.
	// Turns 2, 3 should be linearly interpolated between ts1 and ts4.
	ts0, _ := time.Parse(codefreeLogTimestampLayout, "2026-11-09 10:00:00.000")
	ts1, _ := time.Parse(codefreeLogTimestampLayout, "2026-11-09 10:05:00.000")
	ts4, _ := time.Parse(codefreeLogTimestampLayout, "2026-11-09 10:20:00.000")
	logTS := codefreeLogTimestampMap{
		"s/t0": {Started: ts0},
		"s/t1": {Started: ts1},
		"s/t4": {Started: ts4},
	}

	result := resolveTurnTimestamps(logTS, "s", []string{"t0", "t1", "t2", "t3", "t4"},
		time.UnixMilli(1000), time.UnixMilli(9999999999999))

	require.Len(t, result, 5)
	assert.Equal(t, ts0, result[0])
	assert.Equal(t, ts1, result[1])
	// Turns 2, 3: linearly interpolated between ts1 (index 1) and
	// ts4 (index 4). gap = 3, step = (ts4-ts1)/3.
	step := ts4.Sub(ts1) / 3
	assert.Equal(t, ts1.Add(1*step), result[2], "turn 2 interpolated between ts1 and ts4")
	assert.Equal(t, ts1.Add(2*step), result[3], "turn 3 interpolated between ts1 and ts4")
	assert.Equal(t, ts4, result[4])
}

func TestResolveTurnTimestamps_NoLog_FallbackInterpolation(t *testing.T) {
	// No log timestamps at all — should fall back to session-level
	// interpolation.
	start := time.UnixMilli(1000000) // 1970-01-01 00:16:40
	end := time.UnixMilli(5000000)   // 1970-01-01 01:23:20

	result := resolveTurnTimestamps(nil, "s", []string{"t0", "t1", "t2", "t3"},
		start, end)

	require.Len(t, result, 4)
	// stepDuration = (5000000-1000000)/4 = 1000000ms = 1000s
	stepDuration := end.Sub(start) / time.Duration(4)
	assert.Equal(t, start, result[0])
	assert.Equal(t, start.Add(1*stepDuration), result[1])
	assert.Equal(t, start.Add(2*stepDuration), result[2])
	assert.Equal(t, start.Add(3*stepDuration), result[3])
}

func TestResolveTurnTimestamps_NoLog_SingleTurn(t *testing.T) {
	// Single turn, no log — should get startTime.
	start := time.UnixMilli(1000000)
	result := resolveTurnTimestamps(nil, "s", []string{"t0"}, start, time.UnixMilli(5000000))
	require.Len(t, result, 1)
	assert.Equal(t, start, result[0])
}

func TestResolveTurnTimestamps_Empty(t *testing.T) {
	result := resolveTurnTimestamps(nil, "s", nil, time.Time{}, time.Time{})
	assert.Nil(t, result)
}

func TestLoadCodeFreeSession_PartialLogTimestamps(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "agentSessionData")
	require.NoError(t, os.MkdirAll(root, 0o755))

	// Create logs with timestamps for turn 0 and turn 2, but NOT turn 1.
	logDir := filepath.Join(tmp, "logs", "20261109T145816")
	require.NoError(t, os.MkdirAll(logDir, 0o755))
	logContent := `2026-11-09 16:32:05.354 [info] [CodeFree] Turn started session=uuid-partial turn=request_p0 authRevision=5 stage=preflight
2026-11-09 16:45:00.000 [info] [CodeFree] Turn started session=uuid-partial turn=request_p2 authRevision=5 stage=preflight
`
	require.NoError(t, os.WriteFile(
		filepath.Join(logDir, "agenthost.log"), []byte(logContent), 0o644,
	))

	// Create a session DB with 3 turns; turn 1 has no log entry.
	sessionDir := filepath.Join(root, "uuid-partial")
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))
	dbPath := filepath.Join(sessionDir, codefreeSQLiteDBName)
	_, db := newCodeFreeSQLiteTestDBAt(t, dbPath)
	stateJSON := `{
		"session": {"sessionId": "uuid-partial", "startTime": 1000, "modifiedTime": 9999999999999, "summary": "Partial log", "workingDirectory": ""},
		"chats": [{"agent": {"uri": "codefree-agent://builtin/spec"}}],
		"completedTurns": [
			{"history": {"turnId": "request_p0", "userMessage": {"text": "Q0"}, "parts": [{"type": "text", "text": "A0"}]}},
			{"history": {"turnId": "request_p1", "userMessage": {"text": "Q1"}, "parts": [{"type": "text", "text": "A1"}]}},
			{"history": {"turnId": "request_p2", "userMessage": {"text": "Q2"}, "parts": [{"type": "text", "text": "A2"}]}}
		],
		"inFlightTurns": []
	}`
	seedCodeFreeMetadata(t, db, codefreeStateKey, stateJSON)
	db.Close()

	store, err := OpenCodeFreeSQLiteStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	sess, msgs, err := loadCodeFreeSession(
		context.Background(), store.db, dbPath, "uuid-partial", "m", root,
	)
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.Len(t, msgs, 6, "3 turns x 2 messages")

	// Turn 0: from log → 16:32:05.354 GMT+8 → 08:32:05.354 UTC
	ts0 := codefreeLogTimeToUTC(t, "2026-11-09 16:32:05.354")
	// Turn 2: from log → 16:45:00.000 GMT+8 → 08:45:00.000 UTC
	ts2 := codefreeLogTimeToUTC(t, "2026-11-09 16:45:00.000")

	assert.Equal(t, ts0, msgs[0].Timestamp, "turn 0 user msg from log")
	assert.Equal(t, ts0, msgs[1].Timestamp, "turn 0 assistant msg from log")

	// Turn 1: no log → linearly interpolated between ts0 and ts2.
	step := ts2.Sub(ts0) / 2
	ts1Expected := ts0.Add(step)
	assert.Equal(t, ts1Expected, msgs[2].Timestamp, "turn 1 user msg interpolated between ts0 and ts2")
	assert.Equal(t, ts1Expected, msgs[3].Timestamp, "turn 1 assistant msg interpolated between ts0 and ts2")

	assert.Equal(t, ts2, msgs[4].Timestamp, "turn 2 user msg from log")
	assert.Equal(t, ts2, msgs[5].Timestamp, "turn 2 assistant msg from log")
}

// codefreeLogTimeToUTC parses a CodeFree agenthost.log timestamp string
// (wall-clock time in the default log timezone) and returns the
// equivalent UTC time. This mirrors the conversion in
// codefreeParseLogTimestamp: time.Parse returns UTC, so we must
// re-interpret the wall-clock components in the correct zone and then
// convert to UTC.
func codefreeLogTimeToUTC(t *testing.T, s string) time.Time {
	ts, err := time.Parse(codefreeLogTimestampLayout, s)
	require.NoError(t, err)
	loc := codefreeLogTimezoneDefault()
	return time.Date(
		ts.Year(), ts.Month(), ts.Day(),
		ts.Hour(), ts.Minute(), ts.Second(), ts.Nanosecond(),
		loc,
	).UTC()
}

// --- tool-call-level timestamp tests ---

func TestCodefreeParseLogTimestamp_Timezone(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600) // GMT+8

	// A wall-clock time of 08:05:13.159 in GMT+8 should be 00:05:13.159 UTC.
	ts, ok := codefreeParseLogTimestamp("2026-11-11 08:05:13.159 [info] some event", loc)
	require.True(t, ok)
	assert.Equal(t, "2026-11-11T00:05:13.159Z", ts.UTC().Format(time.RFC3339Nano))

	// Negative assertion: time.Parse without zone would give 08:05:13.159
	// "UTC" (wrong by 8 hours). ts (00:05 UTC) is 8 hours earlier than
	// raw (08:05 UTC), so raw.Sub(ts) == 8h.
	raw, err := time.Parse(codefreeLogTimestampLayout, "2026-11-11 08:05:13.159")
	require.NoError(t, err)
	assert.Equal(t, 8*time.Hour, raw.Sub(ts),
		"correctly parsed time should differ from naive UTC parse by exactly 8 hours")

	// Short line — should fail.
	_, ok = codefreeParseLogTimestamp("short", loc)
	assert.False(t, ok)

	// Bad timestamp — should fail.
	_, ok = codefreeParseLogTimestamp("not-a-timestamp at all here!!!!", loc)
	assert.False(t, ok)
}

func TestCodefreeCalibrateTimezoneFromAnchor(t *testing.T) {
	tmp := t.TempDir()
	// codefreeLogsDirForRoot(root) computes filepath.Dir(root)/"logs".
	// We pass an "agentSessionData" subdirectory so that
	// filepath.Dir(agentSessionData)/logs = tmp/logs.
	agentSessionData := filepath.Join(tmp, "agentSessionData")
	logsDir := filepath.Join(tmp, "logs", "20261111T080511")
	require.NoError(t, os.MkdirAll(logsDir, 0o755))

	// session.startTime epoch = 2026-11-11T00:05:11.722Z (UTC).
	// In GMT+8 wall-clock that is 08:05:11.722.
	// Log first event: 08:05:11.723 (1ms later).
	// offset = startTimeEpochMs - wallAsUTCMs = +8h (rounded to 15min multiples = +8h).
	// residual = 1ms, well within 10 minutes.
	startTimeEpochMs := time.Date(2026, 11, 11, 0, 5, 11, 722000000, time.UTC).UnixMilli()

	logContent := "2026-11-11 08:05:11.723 [info] [CodeFree] Turn started session=sess-anchor turn=req_abc\n"
	require.NoError(t, os.WriteFile(
		filepath.Join(logsDir, "agenthost.log"), []byte(logContent), 0o644,
	))

	loc := codefreeCalibrateTimezoneFromAnchor(agentSessionData, "sess-anchor", startTimeEpochMs)
	require.NotNil(t, loc, "anchor calibration should succeed")

	// The offset should be +8 hours. A wall-clock time of 08:05 in a
	// +8 zone is 00:05 UTC, which is 8 hours earlier than 08:05 UTC.
	wall := time.Date(2026, 11, 11, 8, 5, 11, 723000000, loc)
	wallUTC := time.Date(2026, 11, 11, 8, 5, 11, 723000000, time.UTC)
	offset := wallUTC.Sub(wall)
	assert.Equal(t, 8*time.Hour, offset, "calibrated offset should be +8 hours")

	// Verify: parsing 08:05:11.723 in this zone gives 00:05:11.723 UTC.
	ts, ok := codefreeParseLogTimestamp("2026-11-11 08:05:11.723 [info] event", loc)
	require.True(t, ok)
	assert.Equal(t, "2026-11-11T00:05:11.723Z", ts.UTC().Format(time.RFC3339Nano))
}

func TestCodefreeCalibrateTimezoneFromAnchor_ResidualTooLarge(t *testing.T) {
	tmp := t.TempDir()
	agentSessionData := filepath.Join(tmp, "agentSessionData")
	logsDir := filepath.Join(tmp, "logs", "20261111T080511")
	require.NoError(t, os.MkdirAll(logsDir, 0o755))

	// startTime epoch = 2026-11-11T00:07:11.722Z.
	// Log first event: 08:05:11.723 (wall-clock as UTC).
	// offset = wallAsUTCMs - startTimeEpochMs = 7h58m + 1ms.
	// Truncated to 15-min multiples = 7h45m.
	// residual = 13m + 1ms > 10min → calibration fails.
	startTimeEpochMs := time.Date(2026, 11, 11, 0, 7, 11, 722000000, time.UTC).UnixMilli()

	logContent := "2026-11-11 08:05:11.723 [info] [CodeFree] Turn started session=sess-bad turn=req_xyz\n"
	require.NoError(t, os.WriteFile(
		filepath.Join(logsDir, "agenthost.log"), []byte(logContent), 0o644,
	))

	loc := codefreeCalibrateTimezoneFromAnchor(agentSessionData, "sess-bad", startTimeEpochMs)
	assert.Nil(t, loc, "calibration should fail when residual > 10 minutes")
}

func TestLoadCodeFreeToolCallTimestamps_FourSources(t *testing.T) {
	tmp := t.TempDir()
	// codefreeLogsDirForRoot(root) computes filepath.Dir(root)/"logs".
	// Pass an "agentSessionData" subdirectory so logs are found at
	// tmp/logs/...
	agentSessionData := filepath.Join(tmp, "agentSessionData")
	logsDir := filepath.Join(tmp, "logs", "20261111T080500")
	require.NoError(t, os.MkdirAll(logsDir, 0o755))

	loc := time.FixedZone("CST", 8*3600)

	// Source 1: renderer.log "Invoking client tool"
	winDir := filepath.Join(logsDir, "window1")
	require.NoError(t, os.MkdirAll(winDir, 0o755))
	rendererContent := `2026-11-11 08:14:47.967 [info] [AgentHost] Invoking client tool: run_in_terminal (toolCallId=chatcmpl-tool-b65e37963593f3bb, runtimeToolName=run_in_terminal)
`
	require.NoError(t, os.WriteFile(
		filepath.Join(winDir, "renderer.log"), []byte(rendererContent), 0o644,
	))

	// Source 2+3: agenthost.log code_search.dispatch_started / operation_completed
	// Source 4: overlap lines
	agenthostContent := `2026-11-12 08:05:59.810 [info] [code-search] event=code_search.dispatch_started requestId=req_123 toolCallId=chatcmpl-tool-acaeaab3601404f9 status=running durationMs=0.00
2026-11-12 08:05:59.855 [info] [code-search] event=code_search.operation_completed requestId=req_123 toolCallId=chatcmpl-tool-acaeaab3601404f9 status=completed durationMs=42.36
2026-11-11 08:06:01.240 [info] [codeFree] sibling tool invokes overlapped: 3 calls in 47ms (ids=chatcmpl-tool-a370abc,chatcmpl-tool-b4acdef,chatcmpl-tool-89a3ghi)
`
	require.NoError(t, os.WriteFile(
		filepath.Join(logsDir, "agenthost.log"), []byte(agenthostContent), 0o644,
	))

	m := loadCodeFreeToolCallTimestamps(agentSessionData, loc)

	// Source 1: Invoking client tool.
	tc, ok := m["chatcmpl-tool-b65e37963593f3bb"]
	require.True(t, ok, "renderer.log toolCall should be found")
	assert.False(t, tc.Start.IsZero(), "Start should be set")
	expectedStart := time.Date(2026, 11, 11, 0, 14, 47, 967000000, time.UTC)
	assert.True(t, tc.Start.Equal(expectedStart),
		"renderer Start: got %v want %v", tc.Start, expectedStart)

	// Source 2+3: dispatch_started + operation_completed.
	tc, ok = m["chatcmpl-tool-acaeaab3601404f9"]
	require.True(t, ok, "code_search toolCall should be found")
	assert.False(t, tc.Start.IsZero(), "Start should be set from dispatch_started")
	assert.False(t, tc.End.IsZero(), "End should be set from operation_completed")
	expectedDispatch := time.Date(2026, 11, 12, 0, 5, 59, 810000000, time.UTC)
	expectedCompleted := time.Date(2026, 11, 12, 0, 5, 59, 855000000, time.UTC)
	assert.True(t, tc.Start.Equal(expectedDispatch),
		"dispatch Start: got %v want %v", tc.Start, expectedDispatch)
	assert.True(t, tc.End.Equal(expectedCompleted),
		"completed End: got %v want %v", tc.End, expectedCompleted)

	// Source 4: overlap line — all 3 IDs share the same timestamp.
	expectedOverlap := time.Date(2026, 11, 11, 0, 6, 1, 240000000, time.UTC)
	for _, id := range []string{"chatcmpl-tool-a370abc", "chatcmpl-tool-b4acdef", "chatcmpl-tool-89a3ghi"} {
		tc, ok := m[id]
		require.True(t, ok, "overlap ID %s should be found", id)
		assert.True(t, tc.Start.Equal(expectedOverlap),
			"overlap Start for %s: got %v want %v", id, tc.Start, expectedOverlap)
	}
}

func TestLoadCodeFreeToolCallTimestamps_MissingDir(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	m := loadCodeFreeToolCallTimestamps("/nonexistent/path", loc)
	assert.NotNil(t, m)
	assert.Empty(t, m)
}

func TestCodefreeResolvePartTimestamps_Interpolation(t *testing.T) {
	sessionStart := time.Date(2026, 11, 11, 0, 5, 0, 0, time.UTC)
	sessionEnd := time.Date(2026, 11, 11, 1, 0, 0, 0, time.UTC)
	turnStart := time.Date(2026, 11, 11, 0, 10, 0, 0, time.UTC)
	turnEnd := time.Date(2026, 11, 11, 0, 15, 0, 0, time.UTC)

	// 5 parts: text, toolCall(real), text, toolCall(no log), toolCall(real)
	partsJSON := `[
		{"type": "text", "text": "hello"},
		{"type": "toolCall", "toolCallId": "tc-real-1", "toolId": "run_in_terminal"},
		{"type": "text", "text": "working..."},
		{"type": "toolCall", "toolCallId": "tc-blind", "toolId": "read_file"},
		{"type": "toolCall", "toolCallId": "tc-real-2", "toolId": "edit_file"}
	]`
	parts := gjson.Parse(partsJSON).Array()

	toolCallTS := codefreeToolCallTimeMap{
		"tc-real-1": {Start: time.Date(2026, 11, 11, 0, 11, 0, 0, time.UTC)},
		"tc-real-2": {Start: time.Date(2026, 11, 11, 0, 14, 0, 0, time.UTC)},
	}

	result := codefreeResolvePartTimestamps(parts, turnStart, turnEnd, toolCallTS, sessionStart, sessionEnd)
	require.Len(t, result, 5)

	// Part 0 (text, no anchor before first real): gets first anchor time.
	assert.Equal(t, toolCallTS["tc-real-1"].Start, result[0])
	// Part 1 (toolCall, real): exact timestamp.
	assert.Equal(t, toolCallTS["tc-real-1"].Start, result[1])
	// Part 2 (text, between anchors): interpolated.
	// Anchors at index 1 (00:11) and index 4 (00:14), gap=3, step=1min.
	// Index 2 = 00:11 + 1min = 00:12.
	expected2 := time.Date(2026, 11, 11, 0, 12, 0, 0, time.UTC)
	assert.Equal(t, expected2, result[2])
	// Part 3 (toolCall, blind): interpolated.
	// Index 3 = 00:11 + 2min = 00:13.
	expected3 := time.Date(2026, 11, 11, 0, 13, 0, 0, time.UTC)
	assert.Equal(t, expected3, result[3])
	// Part 4 (toolCall, real): exact timestamp.
	assert.Equal(t, toolCallTS["tc-real-2"].Start, result[4])

	// Monotonic non-decreasing.
	for i := 1; i < len(result); i++ {
		assert.False(t, result[i].Before(result[i-1]),
			"result[%d] must not be before result[%d]", i, i-1)
	}
}

func TestCodefreeResolvePartTimestamps_NoAnchors(t *testing.T) {
	sessionStart := time.Date(2026, 11, 11, 0, 5, 0, 0, time.UTC)
	sessionEnd := time.Date(2026, 11, 11, 1, 0, 0, 0, time.UTC)
	turnStart := time.Date(2026, 11, 11, 0, 10, 0, 0, time.UTC)
	turnEnd := time.Date(2026, 11, 11, 0, 15, 0, 0, time.UTC)

	partsJSON := `[
		{"type": "text", "text": "hello"},
		{"type": "toolCall", "toolCallId": "tc-blind", "toolId": "read_file"}
	]`
	parts := gjson.Parse(partsJSON).Array()

	// No tool-call timestamps at all.
	toolCallTS := codefreeToolCallTimeMap{}

	result := codefreeResolvePartTimestamps(parts, turnStart, turnEnd, toolCallTS, sessionStart, sessionEnd)
	require.Len(t, result, 2)

	// All parts should get turnStart (clamped).
	assert.Equal(t, turnStart, result[0])
	assert.Equal(t, turnStart, result[1])
}

func TestCodefreeClampTimestamp(t *testing.T) {
	lo := time.Date(2026, 11, 11, 0, 5, 0, 0, time.UTC)
	hi := time.Date(2026, 11, 11, 1, 0, 0, 0, time.UTC)

	// Within range — unchanged.
	mid := time.Date(2026, 11, 11, 0, 30, 0, 0, time.UTC)
	assert.Equal(t, mid, codefreeClampTimestamp(mid, lo, hi))

	// Before lo — clamped to lo.
	before := time.Date(2026, 11, 11, 0, 1, 0, 0, time.UTC)
	assert.Equal(t, lo, codefreeClampTimestamp(before, lo, hi))

	// After hi — clamped to hi.
	after := time.Date(2026, 11, 11, 2, 0, 0, 0, time.UTC)
	assert.Equal(t, hi, codefreeClampTimestamp(after, lo, hi))

	// Zero hi — only lower bound enforced.
	assert.Equal(t, mid, codefreeClampTimestamp(mid, lo, time.Time{}))
	assert.Equal(t, lo, codefreeClampTimestamp(before, lo, time.Time{}))
}

func TestCodefreeTurnStartFromToolCalls(t *testing.T) {
	turnJSON := `{"history": {"turnId": "turn-1", "parts": [
		{"type": "text", "text": "hello"},
		{"type": "toolCall", "toolCallId": "tc-a", "toolId": "run_in_terminal"},
		{"type": "toolCall", "toolCallId": "tc-b", "toolId": "read_file"}
	]}}`
	turn := gjson.Parse(turnJSON)

	// No tool-call timestamps.
	_, ok := codefreeTurnStartFromToolCalls(turn, codefreeToolCallTimeMap{})
	assert.False(t, ok)

	// With timestamps — should return the earliest.
	tsA := time.Date(2026, 11, 11, 0, 10, 0, 0, time.UTC)
	tsB := time.Date(2026, 11, 11, 0, 9, 0, 0, time.UTC) // earlier
	toolCallTS := codefreeToolCallTimeMap{
		"tc-a": {Start: tsA},
		"tc-b": {Start: tsB},
	}
	result, ok := codefreeTurnStartFromToolCalls(turn, toolCallTS)
	require.True(t, ok)
	assert.Equal(t, tsB, result, "should return earliest tool-call Start")
}

func TestResolveCodeFreeLogLocation_Fallback(t *testing.T) {
	// No anchor info → should fall back to time.Local or fixed +8.
	loc := resolveCodeFreeLogLocation("", "", 0)
	require.NotNil(t, loc)
	// Verify it produces a valid offset by constructing a time in
	// this zone and comparing with UTC.
	wall := time.Date(2026, 11, 11, 12, 0, 0, 0, loc)
	wallUTC := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	offset := wall.Sub(wallUTC)
	assert.GreaterOrEqual(t, offset, -12*time.Hour)
	assert.LessOrEqual(t, offset, 14*time.Hour)
}
