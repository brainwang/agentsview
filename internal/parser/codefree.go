package parser

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// codefreeDecodeWorkingDir converts a CodeFree workingDirectory URI
// (e.g. "file:///d%3A/Temp/Download/sub-exam") into a native filesystem
// path (e.g. "d:\Temp\Download\sub-exam").
func codefreeDecodeWorkingDir(uri string) string {
	if uri == "" {
		return ""
	}
	rest := uri
	if strings.HasPrefix(rest, "file:///") {
		rest = rest[len("file:///"):]
	} else if strings.HasPrefix(rest, "file://") {
		rest = rest[len("file://"):]
	}
	decoded, err := url.QueryUnescape(rest)
	if err != nil {
		decoded = rest
	}
	// On Windows, convert forward slashes to backslashes so the Cwd
	// matches the native path convention used by other providers.
	return strings.ReplaceAll(decoded, "/", string(rune('\\')))
}

// loadCodeFreeSession reads the codefree.state.v1 JSON from a CodeFree
// session.db and builds a ParsedSession with its ParsedMessage sequence.
//
// Each completedTurn produces a RoleUser message (from history.userMessage.text)
// followed by a RoleAssistant message (from history.parts). inFlightTurns
// are appended if they carry content. An empty session (no turns, no
// user message text) returns nil to signal SkipNoSession.
//
// root is the agentSessionData directory path. It is used to locate the
// sibling logs/ directory for real per-turn timestamps from
// agenthost.log. If root is empty or logs are unavailable, timestamps
// fall back to session-level interpolation.
func loadCodeFreeSession(
	ctx context.Context,
	db *sql.DB,
	dbPath, sessionID, machine, root string,
) (*ParsedSession, []ParsedMessage, error) {
	if db == nil {
		return nil, nil, fmt.Errorf("codefree sqlite db is nil for %s", dbPath)
	}
	var stateJSON string
	err := db.QueryRow(
		"SELECT value FROM session_metadata WHERE key = ?",
		codefreeStateKey,
	).Scan(&stateJSON)
	if err == sql.ErrNoRows {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("query codefree state for %s: %w", sessionID, err)
	}
	if stateJSON == "" {
		return nil, nil, nil
	}

	// Try to load real per-turn timestamps from agenthost.log files.
	// This is best-effort: if logs are unavailable or the turn is not
	// found, the caller falls back to session-level interpolation.
	logTS, err := loadCodeFreeLogTimestamps(root)
	if err != nil {
		// Log loading failures are non-fatal; proceed with empty map.
		logTS = nil
	}

	return parseCodeFreeStateJSON(ctx, stateJSON, sessionID, machine, logTS)
}

// parseCodeFreeStateJSON parses the codefree.state.v1 JSON value into
// a ParsedSession and ParsedMessage slice.
//
// logTS holds real per-turn timestamps extracted from agenthost.log.
// Turns whose turnId appears in logTS use the logged start time.
// Turns without a log entry fall back to interpolated timestamps
// distributed evenly across the session duration.
func parseCodeFreeStateJSON(
	ctx context.Context,
	stateJSON, sessionID, machine string,
	logTS codefreeLogTimestampMap,
) (*ParsedSession, []ParsedMessage, error) {
	root := gjson.Parse(stateJSON)
	session := root.Get("session")
	if !session.Exists() {
		return nil, nil, nil
	}

	sess := &ParsedSession{
		ID:              "codefree:" + sessionID,
		SourceSessionID: sessionID,
		Agent:           AgentCodeFree,
		Machine:         machine,
		SessionName:     session.Get("summary").Str,
		Cwd:             codefreeDecodeWorkingDir(session.Get("workingDirectory").Str),
		StartedAt:       time.UnixMilli(session.Get("startTime").Int()),
		EndedAt:         time.UnixMilli(session.Get("modifiedTime").Int()),
	}

	// AgentLabel is intentionally left empty. The agent URI path segment
	// (e.g. "spec", "sdd-dev-engineer") is an internal digital-employee
	// type identifier, not a user-facing display name. Leaving it empty
	// lets the frontend fall back to the KNOWN_AGENTS label for the
	// "codefree" provider type (e.g. "CodeFree").

	var messages []ParsedMessage
	ordinal := 0

	// CodeFree state JSON does not carry per-turn or per-message
	// timestamps — only session-level startTime and modifiedTime.
	// We try to fill real per-turn timestamps from multiple sources,
	// resolved in priority order:
	//   1. Use agenthost.log timestamps for turns that have them.
	//   2. For turns without a log entry, try the modification time
	//      of tooloutput files (<cwd>/.codefree/tooloutput/
	//      <toolCallId>-run_in_terminal.txt) as a fallback.
	//   3. Fill remaining missing turns from the nearest known
	//      neighbour (linear interpolation).
	//   4. If no timestamps exist at all, distribute the session
	//      duration evenly across all turns as a last resort.
	totalCompleted := 0
	if completedTurns := root.Get("completedTurns"); completedTurns.IsArray() {
		totalCompleted = len(completedTurns.Array())
	}

	// Collect turnIds in order (completedTurns then inFlightTurns)
	// and resolve all timestamps up front.
	var allTurnIDs []string
	var allTurns []gjson.Result
	if completedTurns := root.Get("completedTurns"); completedTurns.IsArray() {
		for _, turn := range completedTurns.Array() {
			allTurnIDs = append(allTurnIDs, turn.Get("history.turnId").Str)
			allTurns = append(allTurns, turn)
		}
	}
	if inFlightTurns := root.Get("inFlightTurns"); inFlightTurns.IsArray() {
		for _, turn := range inFlightTurns.Array() {
			allTurnIDs = append(allTurnIDs, turn.Get("history.turnId").Str)
			allTurns = append(allTurns, turn)
		}
	}

	// Supplement log timestamps with tooloutput file mtimes for
	// turns that lack a log entry. The working directory (sess.Cwd)
	// is the session's workingDirectory decoded to a native path.
	// Log timestamps take priority; tooloutput mtime only fills gaps.
	if sess.Cwd != "" {
		toolOutputTS := loadCodeFreeToolOutputTimestamps(
			sess.Cwd, sessionID, allTurns,
		)
		logTS = mergeToolOutputTimestamps(logTS, toolOutputTS)
	}

	turnTimestamps := resolveTurnTimestamps(
		logTS, sessionID, allTurnIDs, sess.StartedAt, sess.EndedAt,
	)

	// Process completedTurns.
	completedTurns := root.Get("completedTurns")
	if completedTurns.IsArray() {
		for i, turn := range completedTurns.Array() {
			if err := contextErrEvery(ctx, i); err != nil {
				return nil, nil, err
			}
			turnTime := turnTimestamps[i]
			emitted := codefreeEmitTurnMessages(&messages, &ordinal, turn, turnTime)
			_ = emitted
		}
	}

	// Process inFlightTurns (may have incomplete content).
	inFlightTurns := root.Get("inFlightTurns")
	if inFlightTurns.IsArray() {
		for i, turn := range inFlightTurns.Array() {
			if err := contextErrEvery(ctx, i); err != nil {
				return nil, nil, err
			}
			turnTime := turnTimestamps[totalCompleted+i]
			codefreeEmitTurnMessages(&messages, &ordinal, turn, turnTime)
		}
	}

	if len(messages) == 0 {
		return nil, nil, nil
	}

	// Compute session-level aggregates.
	sess.MessageCount = len(messages)
	for _, m := range messages {
		if m.Role == RoleUser {
			sess.UserMessageCount++
		}
	}
	if sess.MessageCount > 0 {
		sess.FirstMessage = messages[0].Content
	}

	if err := accumulateMessageTokenUsageContext(ctx, sess, messages); err != nil {
		return nil, nil, err
	}

	return sess, messages, nil
}

// codefreeEmitTurnMessages emits user and assistant messages for one
// CodeFree turn. turnTime is the timestamp assigned to every message
// in this turn (CodeFree state JSON has no per-message timestamps, so
// the caller distributes the session duration across turns). Returns
// true if at least one message was emitted.
func codefreeEmitTurnMessages(
	messages *[]ParsedMessage,
	ordinal *int,
	turn gjson.Result,
	turnTime time.Time,
) bool {
	emitted := false
	history := turn.Get("history")

	// 1. User message.
	userText := history.Get("userMessage.text").Str
	if userText != "" {
		*messages = append(*messages, ParsedMessage{
			Ordinal:       *ordinal,
			Role:          RoleUser,
			Content:       userText,
			ContentLength: len(userText),
			Timestamp:     turnTime,
		})
		*ordinal++
		emitted = true
	}

	// 2. Assistant message from parts.
	parts := history.Get("parts")
	if parts.IsArray() {
		var textParts []string
		var toolCalls []ParsedToolCall
		for _, part := range parts.Array() {
			partType := part.Get("type").Str
			switch partType {
			case "text":
				t := part.Get("text").Str
				if t != "" {
					textParts = append(textParts, t)
				}
			case "toolCall":
				tc := codefreeParseToolCall(part)
				toolCalls = append(toolCalls, tc)
			}
		}

		content := strings.Join(textParts, "\n")
		if content != "" || len(toolCalls) > 0 {
			msg := ParsedMessage{
				Ordinal:       *ordinal,
				Role:          RoleAssistant,
				Content:       content,
				ContentLength: len(content),
				Timestamp:     turnTime,
			}
			if len(toolCalls) > 0 {
				msg.HasToolUse = true
				msg.ToolCalls = toolCalls
			}

			// Token usage from turn.usage.
			usage := turn.Get("usage")
			if usage.Exists() {
				msg.Model = usage.Get("model").Str
				inputTokens := int(usage.Get("inputTokens").Int())
				outputTokens := int(usage.Get("outputTokens").Int())
				if inputTokens > 0 {
					msg.ContextTokens = inputTokens
					msg.HasContextTokens = true
				}
				if outputTokens > 0 {
					msg.OutputTokens = outputTokens
					msg.HasOutputTokens = true
				}
			}

			*messages = append(*messages, msg)
			*ordinal++
			emitted = true
		}
	}

	return emitted
}

// codefreeParseToolCall builds a ParsedToolCall from a toolCall part.
func codefreeParseToolCall(part gjson.Result) ParsedToolCall {
	tc := ParsedToolCall{
		ToolUseID: part.Get("toolCallId").Str,
		ToolName:  part.Get("toolId").Str,
		InputJSON: part.Get("input").Raw,
	}

	// Result event from disposition + result.content.
	disposition := part.Get("disposition").Str
	result := part.Get("result")
	if result.Exists() {
		resultContent := result.Get("content").Str
		if resultContent == "" {
			// result.content may be an object/array; fall back to raw JSON.
			resultContent = result.Get("content").Raw
		}
		status := disposition
		if status == "" {
			status = "completed"
		}
		tc.ResultEvents = append(tc.ResultEvents, ParsedToolResultEvent{
			ToolUseID: tc.ToolUseID,
			Status:    status,
			Content:   resultContent,
		})
	} else if disposition != "" {
		tc.ResultEvents = append(tc.ResultEvents, ParsedToolResultEvent{
			ToolUseID: tc.ToolUseID,
			Status:    disposition,
		})
	}

	return tc
}
