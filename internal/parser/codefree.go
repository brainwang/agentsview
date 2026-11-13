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
	// A file:// authority or root ("file:///home/user/project",
	// "file://localhost/tmp") loses its leading separator when the scheme
	// prefix is stripped; restore it so the decoded path stays absolute.
	// Windows drive paths ("d:/...") already carry their own root form.
	if strings.HasPrefix(uri, "file://") &&
		!strings.HasPrefix(decoded, "/") &&
		!(len(decoded) >= 2 && decoded[1] == ':') {
		decoded = "/" + decoded
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

	// Extract session startTime epoch for timezone anchor calibration.
	startTimeEpochMs := gjson.Get(stateJSON, "session.startTime").Int()

	// Resolve the timezone for parsing log wall-clock timestamps.
	// Three-tier: anchor calibration → time.Local → fixed +8.
	loc := resolveCodeFreeLogLocation(root, sessionID, startTimeEpochMs)

	// Load turn-level timestamps from agenthost.log (Turn started /
	// Turn terminal lines).
	logTS, err := loadCodeFreeLogTimestamps(root, loc)
	if err != nil {
		logTS = nil
	}

	// Load tool-call-level timestamps from agenthost.log and
	// window*/renderer.log (four event types).
	toolCallTS := loadCodeFreeToolCallTimestamps(root, loc)

	return parseCodeFreeStateJSON(ctx, stateJSON, sessionID, machine, logTS, toolCallTS)
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
	toolCallTS codefreeToolCallTimeMap,
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
	// We resolve timestamps in priority order:
	//   1. Use agenthost.log Turn started/terminal timestamps.
	//   2. For turns without a log entry, try the earliest tool-call
	//      Start from the tool-call-level map.
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

	// Supplement log timestamps with tool-call-level timestamps for
	// turns that lack a log entry. The earliest tool-call Start in a
	// turn approximates the turn start time.
	if toolCallTS != nil && len(allTurns) > 0 {
		for i, turn := range allTurns {
			turnID := allTurnIDs[i]
			if turnID == "" {
				continue
			}
			key := codefreeLogTimestampKey(sessionID, turnID)
			if existing, ok := logTS[key]; ok && !existing.Started.IsZero() {
				continue // log timestamp already present
			}
			if ts, ok := codefreeTurnStartFromToolCalls(turn, toolCallTS); ok {
				if logTS == nil {
					logTS = make(codefreeLogTimestampMap)
				}
				entry := logTS[key]
				if entry.Started.IsZero() {
					entry.Started = ts
					logTS[key] = entry
				}
			}
		}
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
			turnStart := turnTimestamps[i]
			// Determine turn end: use log Terminal if available,
			// otherwise the next turn's start, otherwise session end.
			var turnEnd time.Time
			turnID := turn.Get("history.turnId").Str
			if turnID != "" {
				key := codefreeLogTimestampKey(sessionID, turnID)
				if entry, ok := logTS[key]; ok && !entry.Terminal.IsZero() {
					turnEnd = entry.Terminal
				}
			}
			if turnEnd.IsZero() && i+1 < len(turnTimestamps) {
				turnEnd = turnTimestamps[i+1]
			}
			if turnEnd.IsZero() || !turnEnd.After(turnStart) {
				turnEnd = sess.EndedAt
			}
			codefreeEmitTurnMessages(
				&messages, &ordinal, turn, turnStart, turnEnd,
				toolCallTS, sess.StartedAt, sess.EndedAt,
			)
		}
	}

	// Process inFlightTurns (may have incomplete content).
	inFlightTurns := root.Get("inFlightTurns")
	if inFlightTurns.IsArray() {
		for i, turn := range inFlightTurns.Array() {
			if err := contextErrEvery(ctx, i); err != nil {
				return nil, nil, err
			}
			turnStart := turnTimestamps[totalCompleted+i]
			var turnEnd time.Time
			if totalCompleted+i+1 < len(turnTimestamps) {
				turnEnd = turnTimestamps[totalCompleted+i+1]
			}
			if turnEnd.IsZero() || !turnEnd.After(turnStart) {
				turnEnd = sess.EndedAt
			}
			codefreeEmitTurnMessages(
				&messages, &ordinal, turn, turnStart, turnEnd,
				toolCallTS, sess.StartedAt, sess.EndedAt,
			)
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
// CodeFree turn. turnStart is the timestamp for the user message and
// the baseline for assistant parts. Part-level timestamps are resolved
// from tool-call-level data where available, with interpolation for
// parts without real timestamps. Returns true if at least one message
// was emitted.
func codefreeEmitTurnMessages(
	messages *[]ParsedMessage,
	ordinal *int,
	turn gjson.Result,
	turnStart, turnEnd time.Time,
	toolCallTS codefreeToolCallTimeMap,
	sessionStart, sessionEnd time.Time,
) bool {
	emitted := false
	history := turn.Get("history")

	// 1. User message — uses turn start time.
	userText := history.Get("userMessage.text").Str
	if userText != "" {
		*messages = append(*messages, ParsedMessage{
			Ordinal:       *ordinal,
			Role:          RoleUser,
			Content:       userText,
			ContentLength: len(userText),
			Timestamp:     codefreeClampTimestamp(turnStart, sessionStart, sessionEnd),
		})
		*ordinal++
		emitted = true
	}

	// 2. Assistant message from parts.
	parts := history.Get("parts")
	if parts.IsArray() {
		partsArr := parts.Array()
		var textParts []string
		var toolCalls []ParsedToolCall
		for _, part := range partsArr {
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
			// Resolve part-level timestamps for the assistant message.
			// The assistant message timestamp uses the first part's
			// resolved time (or turnStart if no parts have real
			// timestamps).
			partTimestamps := codefreeResolvePartTimestamps(
				partsArr, turnStart, turnEnd,
				toolCallTS, sessionStart, sessionEnd,
			)
			msgTS := turnStart
			if len(partTimestamps) > 0 {
				msgTS = partTimestamps[0]
			}
			msgTS = codefreeClampTimestamp(msgTS, sessionStart, sessionEnd)

			msg := ParsedMessage{
				Ordinal:       *ordinal,
				Role:          RoleAssistant,
				Content:       content,
				ContentLength: len(content),
				Timestamp:     msgTS,
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
