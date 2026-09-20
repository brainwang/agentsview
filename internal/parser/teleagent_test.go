package parser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTeleAgentNormalizeStopReason is a table-driven test for the dual
// spelling normalization of message.data.finish. Both "tool_calls" and
// "tool-calls" (a TeleAgent client-version transition) map to the single
// canonical form "tool_calls"; "stop", "length", and "error" pass through;
// the empty string stays empty.
func TestTeleAgentNormalizeStopReason(t *testing.T) {
	cases := []struct {
		name   string
		finish string
		want   string
	}{
		{name: "tool_calls canonical", finish: "tool_calls", want: "tool_calls"},
		{name: "tool-calls hyphenated", finish: "tool-calls", want: "tool_calls"},
		{name: "stop", finish: "stop", want: "stop"},
		{name: "length", finish: "length", want: "length"},
		{name: "error", finish: "error", want: "error"},
		{name: "empty", finish: "", want: ""},
		{name: "unknown passes through", finish: "content_filter", want: "content_filter"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, teleagentNormalizeStopReason(tc.finish))
		})
	}
}

// TestTeleAgentCwdFallback confirms the session metadata builder falls back
// to the "unknown" project when the session.directory column is empty
// (subagent-spawned sessions often have this), matching Kiro's behavior.
func TestTeleAgentCwdFallback(t *testing.T) {
	cases := []struct {
		name      string
		directory string
		wantCwd   string
	}{
		{name: "present directory", directory: "/tmp/work", wantCwd: "/tmp/work"},
		{name: "empty directory", directory: "", wantCwd: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbPath, db := newTeleAgentSQLiteTestDB(t)
			seedTeleAgentSession(
				t, db, "ses_1", "Task", tc.directory, nil, 1, 2,
			)
			seedTeleAgentMessage(t, db, "msg_1", "ses_1",
				`{"role":"user","time":{"created":1}}`, 1)
			seedTeleAgentPart(t, db, "prt_1", "ses_1", "msg_1",
				`{"type":"text","text":"prompt"}`, 1)
			store := openTeleAgentTestStore(t, dbPath)
			sess, _, err := store.LoadSession(t.Context(), "ses_1", "m")
			require.NoError(t, err)
			require.NotNil(t, sess)
			assert.Equal(t, tc.wantCwd, sess.Cwd)
			// Project falls back to "unknown" when ExtractProjectFromCwd
			// returns empty (which it does for an empty cwd).
			if tc.directory == "" {
				assert.Equal(t, "unknown", sess.Project)
			}
		})
	}
}

// TestTeleAgentFirstMessageExtraction confirms the first non-empty user
// message content (truncated to 300 chars) becomes the session's
// FirstMessage, matching Kiro's behavior.
func TestTeleAgentFirstMessageExtraction(t *testing.T) {
	dbPath, db := newTeleAgentSQLiteTestDB(t)
	seedTeleAgentSession(
		t, db, "ses_1", "Task", "/tmp/work", nil, 1, 2,
	)
	seedTeleAgentMessage(t, db, "msg_1", "ses_1",
		`{"role":"user","time":{"created":1}}`, 1)
	seedTeleAgentPart(t, db, "prt_1", "ses_1", "msg_1",
		`{"type":"text","text":"first prompt"}`, 1)
	// A later user message must not override the first one.
	seedTeleAgentMessage(t, db, "msg_2", "ses_1",
		`{"role":"user","time":{"created":2}}`, 2)
	seedTeleAgentPart(t, db, "prt_2", "ses_1", "msg_2",
		`{"type":"text","text":"second prompt"}`, 2)

	store := openTeleAgentTestStore(t, dbPath)
	sess, _, err := store.LoadSession(t.Context(), "ses_1", "m")
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, "first prompt", sess.FirstMessage,
		"FirstMessage is the first non-empty user content")
	assert.Equal(t, 2, sess.UserMessageCount,
		"both user messages are counted")
}
