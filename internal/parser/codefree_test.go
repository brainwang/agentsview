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

func TestCodefreeAgentLabel(t *testing.T) {
	cases := []struct {
		name string
		uri  string
		want string
	}{
		{name: "spec", uri: "codefree-agent://builtin/spec", want: "spec"},
		{name: "explore", uri: "codefree-agent://builtin/explore", want: "explore"},
		{name: "craft", uri: "codefree-agent://builtin/craft", want: "craft"},
		{name: "empty", uri: "", want: ""},
		{name: "no path", uri: "codefree-agent://builtin", want: "builtin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := codefreeAgentLabel(tc.uri)
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
		context.Background(), store.db, dbPath, "uuid-001", "test-machine",
	)
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.Len(t, msgs, 2)

	// Session metadata.
	assert.Equal(t, "codefree:uuid-001", sess.ID)
	assert.Equal(t, "uuid-001", sess.SourceSessionID)
	assert.Equal(t, AgentCodeFree, sess.Agent)
	assert.Equal(t, "spec", sess.AgentLabel)
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

	// Assistant message.
	assert.Equal(t, RoleAssistant, msgs[1].Role)
	assert.Equal(t, "Sure! I can help with that.", msgs[1].Content)
	assert.Equal(t, "GLM-5.2", msgs[1].Model)
	assert.True(t, msgs[1].HasContextTokens)
	assert.Equal(t, 100, msgs[1].ContextTokens)
	assert.True(t, msgs[1].HasOutputTokens)
	assert.Equal(t, 50, msgs[1].OutputTokens)

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
		context.Background(), store.db, dbPath, "uuid-002", "m",
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
		context.Background(), store.db, dbPath, "uuid-003", "m",
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
		context.Background(), store.db, dbPath, "uuid-empty", "m",
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
		context.Background(), store.db, dbPath, "uuid-missing", "m",
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
		context.Background(), store.db, dbPath, "uuid-no-user", "m",
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
		context.Background(), store.db, dbPath, "uuid-inflight", "m",
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
		context.Background(), store.db, dbPath, "uuid-noresult", "m",
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
		context.Background(), store.db, dbPath, "uuid-nosession", "m",
	)
	require.NoError(t, err)
	assert.Nil(t, sess, "missing session object must return nil")
}

func TestLoadCodeFreeSession_NilDB(t *testing.T) {
	sess, msgs, err := loadCodeFreeSession(
		context.Background(), nil, "", "uuid", "m",
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
		context.Background(), store.db, dbPath, "uuid-multi", "m",
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
	assert.Equal(t, "explore", sess.AgentLabel)
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
		context.Background(), store.db, dbPath, "uuid-ts", "m",
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
		context.Background(), store.db, dbPath, sessionID, "machine-1",
	)
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, "codefree:uuid-provider-test", sess.ID)
	require.Len(t, msgs, 2)
}
