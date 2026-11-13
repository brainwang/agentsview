package parser

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// teleagent_session.go and teleagent.go together implement the TeleAgent
// (TeleAI 星辰超级智能体) parser. The SQLite store (teleagent_sqlite.go)
// owns the read-only handle; this file owns the 3-table session/message/part
// parse loop and the per-message builders.

// loadTeleAgentSession loads one TeleAgent session by joining the session,
// message, and part tables in chronological order. It returns
// (nil, nil, nil) when the session has zero messages with content, which the
// provider layer maps to a SkipNoSession outcome.
func loadTeleAgentSession(
	ctx context.Context, db *sql.DB, dbPath, sessionID, machine string,
) (*ParsedSession, []ParsedMessage, error) {
	if db == nil {
		return nil, nil, fmt.Errorf("teleagent sqlite store is closed")
	}
	sessRow, err := loadTeleAgentSessionRow(db, sessionID)
	if err != nil {
		return nil, nil, err
	}
	if sessRow == nil {
		return nil, nil, nil
	}

	messageRows, err := loadTeleAgentMessageRows(ctx, db, sessionID)
	if err != nil {
		return nil, nil, err
	}

	var messages []ParsedMessage
	var firstMessage string
	ordinal := 0
	for _, msgRow := range messageRows {
		if err := contextErrEvery(ctx, ordinal); err != nil {
			return nil, nil, err
		}
		parts, err := loadTeleAgentPartRows(ctx, db, msgRow.id)
		if err != nil {
			return nil, nil, err
		}
		data := gjson.Parse(msgRow.data)
		role := data.Get("role").Str
		var (
			msg     ParsedMessage
			emitted bool
		)
		switch role {
		case "user":
			msg, emitted = buildTeleAgentUserMessage(data, parts, ordinal)
		case "assistant":
			msg, emitted = buildTeleAgentAssistantMessage(data, parts, ordinal)
		default:
			emitted = false
		}
		if !emitted {
			continue
		}
		if firstMessage == "" && msg.Role == RoleUser && msg.Content != "" {
			firstMessage = truncate(
				strings.ReplaceAll(msg.Content, "\n", " "),
				300,
			)
		}
		messages = append(messages, msg)
		ordinal++
	}

	hasContent := false
	for _, msg := range messages {
		if msg.Content != "" || msg.HasToolUse || msg.HasThinking {
			hasContent = true
			break
		}
	}
	if !hasContent {
		return nil, nil, nil
	}

	sess := buildTeleAgentSession(sessRow, dbPath, machine, messages, firstMessage)
	if err := accumulateMessageTokenUsageContext(ctx, sess, messages); err != nil {
		return nil, nil, err
	}
	return sess, messages, nil
}

// teleagentSessionRow carries the columns of one session row that the parser
// reads. parent_id is a *string because NULL and the empty string both
// indicate a top-level session.
type teleagentSessionRow struct {
	id          string
	title       string
	directory   string
	parentID    *string
	timeCreated int64
	timeUpdated int64
}

func loadTeleAgentSessionRow(
	db *sql.DB, sessionID string,
) (*teleagentSessionRow, error) {
	var row teleagentSessionRow
	err := db.QueryRow(
		`SELECT id, title, directory, parent_id,
		        time_created, time_updated
		   FROM session
		  WHERE id = ?
		    AND title NOT LIKE '_SYS\_%' ESCAPE '\'
		    AND title != ''
		  LIMIT 1`,
		sessionID,
	).Scan(
		&row.id, &row.title, &row.directory, &row.parentID,
		&row.timeCreated, &row.timeUpdated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf(
			"loading teleagent session %s: %w", sessionID, err,
		)
	}
	return &row, nil
}

type teleagentMessageRow struct {
	id   string
	data string
}

func loadTeleAgentMessageRows(
	ctx context.Context, db *sql.DB, sessionID string,
) ([]teleagentMessageRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, data
		  FROM message
		 WHERE session_id = ?
		 ORDER BY time_created, id
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf(
			"loading teleagent messages for %s: %w", sessionID, err,
		)
	}
	defer rows.Close()
	var out []teleagentMessageRow
	for rows.Next() {
		var row teleagentMessageRow
		if err := rows.Scan(&row.id, &row.data); err != nil {
			return nil, fmt.Errorf(
				"scanning teleagent message: %w", err,
			)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

type teleagentPartRow struct {
	data string
}

func loadTeleAgentPartRows(
	ctx context.Context, db *sql.DB, messageID string,
) ([]teleagentPartRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT data
		  FROM part
		 WHERE message_id = ?
		 ORDER BY time_created, id
	`, messageID)
	if err != nil {
		return nil, fmt.Errorf(
			"loading teleagent parts for message %s: %w", messageID, err,
		)
	}
	defer rows.Close()
	var out []teleagentPartRow
	for rows.Next() {
		var row teleagentPartRow
		if err := rows.Scan(&row.data); err != nil {
			return nil, fmt.Errorf(
				"scanning teleagent part: %w", err,
			)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// buildTeleAgentSession constructs the ParsedSession from a session row.
func buildTeleAgentSession(
	row *teleagentSessionRow,
	dbPath, machine string,
	messages []ParsedMessage,
	firstMessage string,
) *ParsedSession {
	cwd := row.directory
	project := ExtractProjectFromCwdWithBranchContext(context.Background(), cwd, "")
	if project == "" {
		project = "unknown"
	}

	userCount := 0
	for _, msg := range messages {
		if msg.Role == RoleUser && msg.Content != "" {
			userCount++
		}
	}

	sess := &ParsedSession{
		ID:               "teleagent:" + row.id,
		Project:          project,
		Machine:          machine,
		Agent:            AgentTeleAgent,
		Cwd:              cwd,
		FirstMessage:     firstMessage,
		SessionName:      row.title,
		StartedAt:        time.UnixMilli(row.timeCreated).UTC(),
		EndedAt:          time.UnixMilli(row.timeUpdated).UTC(),
		MessageCount:     len(messages),
		UserMessageCount: userCount,
		File: FileInfo{
			Path:  TeleAgentSQLiteVirtualPath(dbPath, row.id),
			Mtime: row.timeUpdated * 1_000_000,
		},
	}

	if row.parentID != nil && *row.parentID != "" {
		sess.ParentSessionID = "teleagent:" + *row.parentID
		sess.RelationshipType = RelSubagent
	}

	return sess
}

// buildTeleAgentUserMessage builds a ParsedMessage from a user-role message.
// Messages with no text part produce no message — IM-channel system-prompt
// overrides (data.system / data.tools) are treated as regular user messages
// per design D10, so the text part is the content signal.
func buildTeleAgentUserMessage(
	data gjson.Result, parts []teleagentPartRow, ordinal int,
) (ParsedMessage, bool) {
	text := teleagentConcatPartText(parts, "text")
	text = strings.TrimSpace(text)
	if text == "" {
		return ParsedMessage{}, false
	}
	msg := ParsedMessage{
		Ordinal:       ordinal,
		Role:          RoleUser,
		Content:       text,
		ContentLength: len(text),
		Timestamp:     teleagentMessageTimestamp(data),
	}
	if model := data.Get("model.modelID").Str; model != "" {
		msg.Model = model
	}
	return msg, true
}

// buildTeleAgentAssistantMessage builds a ParsedMessage from an assistant-role
// message. reasoning parts feed ThinkingText, text parts feed Content (joined
// with "\n\n" when multiple), and each tool part becomes one ParsedToolCall
// with a single embedded ResultEvent (the RooCode/Codex pattern, since
// TeleAgent packs call + result in one part). step-start, step-finish,
// compaction, and file parts are metadata markers and are skipped.
func buildTeleAgentAssistantMessage(
	data gjson.Result, parts []teleagentPartRow, ordinal int,
) (ParsedMessage, bool) {
	msg := ParsedMessage{
		Ordinal:   ordinal,
		Role:      RoleAssistant,
		Timestamp: teleagentMessageTimestamp(data),
	}

	var thinkingParts []string
	var textParts []string
	for _, part := range parts {
		pdata := gjson.Parse(part.data)
		switch pdata.Get("type").Str {
		case "reasoning":
			if t := pdata.Get("text").Str; t != "" {
				thinkingParts = append(thinkingParts, t)
			}
		case "text":
			if t := pdata.Get("text").Str; t != "" {
				textParts = append(textParts, t)
			}
		case "tool":
			if tc, ok := buildTeleAgentToolCall(pdata); ok {
				msg.ToolCalls = append(msg.ToolCalls, tc)
				msg.HasToolUse = true
			}
		case "step-start", "step-finish", "compaction", "file":
			// Metadata markers — no user-visible content emitted.
		}
	}

	if len(thinkingParts) > 0 {
		msg.ThinkingText = strings.Join(thinkingParts, "\n\n")
		msg.HasThinking = true
	}
	if len(textParts) > 0 {
		msg.Content = strings.Join(textParts, "\n\n")
		msg.ContentLength = len(msg.Content)
	}

	if model := data.Get("modelID").Str; model != "" {
		msg.Model = model
	}
	if provider := data.Get("providerID").Str; provider != "" {
		msg.ProviderID = provider
	}
	teleagentApplyTokenUsage(data, &msg)
	msg.StopReason = teleagentNormalizeStopReason(data.Get("finish").Str)

	// An assistant turn with only metadata-marker parts and no text, thinking,
	// or tool content produces no message.
	if msg.Content == "" && !msg.HasThinking && !msg.HasToolUse {
		return ParsedMessage{}, false
	}
	return msg, true
}

// buildTeleAgentToolCall builds one ParsedToolCall with an embedded
// ParsedToolResultEvent. The event status comes from data.state.status
// ("completed", "error", "running"), and the content is data.state.error when
// status is "error" (so an error tool call surfaces its error rather than any
// output), otherwise data.output.
func buildTeleAgentToolCall(pdata gjson.Result) (ParsedToolCall, bool) {
	callID := pdata.Get("callID").Str
	tool := pdata.Get("tool").Str
	if callID == "" && tool == "" {
		return ParsedToolCall{}, false
	}
	state := pdata.Get("state")
	status := state.Get("status").Str
	content := pdata.Get("output").Str
	if status == "error" {
		if errText := state.Get("error").Str; errText != "" {
			content = errText
		}
	}
	if status == "" {
		status = "completed"
	}
	inputJSON := state.Get("input").Raw
	tc := ParsedToolCall{
		ToolUseID: callID,
		ToolName:  tool,
		Category:  NormalizeToolCategory(tool),
		InputJSON: inputJSON,
	}
	if fp := pdata.Get("metadata.filepath").Str; fp != "" {
		tc.FilePath = fp
	}
	tc.ResultEvents = append(tc.ResultEvents, ParsedToolResultEvent{
		ToolUseID: callID,
		Status:    status,
		Content:   content,
	})
	return tc, true
}

// teleagentConcatPartText concatenates the text field of every part whose
// type matches partType, in row order, with no separator. Used for the user
// message's single text part.
func teleagentConcatPartText(parts []teleagentPartRow, partType string) string {
	var b strings.Builder
	for _, part := range parts {
		pdata := gjson.Parse(part.data)
		if pdata.Get("type").Str != partType {
			continue
		}
		b.WriteString(pdata.Get("text").Str)
	}
	return b.String()
}

// teleagentMessageTimestamp reads data.time.created (milliseconds) and
// returns the UTC time. Returns the zero time when absent.
func teleagentMessageTimestamp(data gjson.Result) time.Time {
	createdMS := data.Get("time.created").Int()
	if createdMS <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(createdMS).UTC()
}

// teleagentNormalizeStopReason canonicalizes the dual spelling of the
// "tool calls" finish reason. TeleAgent shipped both "tool_calls" and
// "tool-calls" across client versions; both map to "tool_calls". Other
// finish values ("stop", "length", "error") pass through unchanged.
func teleagentNormalizeStopReason(finish string) string {
	if finish == "" {
		return ""
	}
	if finish == "tool-calls" {
		return "tool_calls"
	}
	return finish
}

// teleagentApplyTokenUsage reads data.tokens (input/output/reasoning +
// cache.read/cache.write) and data.cost, then populates the per-message token
// fields. ContextTokens is input + cache.read (the full prompt context);
// OutputTokens is output. The tokens JSON is preserved on TokenUsage for
// downstream cost/usage attribution. No fields are set when the tokens block
// is absent or null.
func teleagentApplyTokenUsage(data gjson.Result, msg *ParsedMessage) {
	tokens := data.Get("tokens")
	if !tokens.Exists() || tokens.Type == gjson.Null {
		return
	}
	if raw := tokens.Raw; raw != "" && raw != "null" {
		msg.TokenUsage = jsontext.Value(raw)
	}
	msg.ContextTokens = int(tokens.Get("input").Int() + tokens.Get("cache.read").Int())
	msg.OutputTokens = int(tokens.Get("output").Int())
	msg.HasContextTokens = true
	msg.HasOutputTokens = true
	msg.tokenPresenceKnown = true
}
