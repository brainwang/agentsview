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
func loadCodeFreeSession(
	ctx context.Context,
	db *sql.DB,
	dbPath, sessionID, machine string,
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
	return parseCodeFreeStateJSON(ctx, stateJSON, sessionID, machine)
}

// parseCodeFreeStateJSON parses the codefree.state.v1 JSON value into
// a ParsedSession and ParsedMessage slice.
func parseCodeFreeStateJSON(
	ctx context.Context,
	stateJSON, sessionID, machine string,
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

	// Process completedTurns.
	completedTurns := root.Get("completedTurns")
	if completedTurns.IsArray() {
		for i, turn := range completedTurns.Array() {
			if err := contextErrEvery(ctx, i); err != nil {
				return nil, nil, err
			}
			emitted := codefreeEmitTurnMessages(&messages, &ordinal, turn)
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
			codefreeEmitTurnMessages(&messages, &ordinal, turn)
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
// CodeFree turn. Returns true if at least one message was emitted.
func codefreeEmitTurnMessages(
	messages *[]ParsedMessage,
	ordinal *int,
	turn gjson.Result,
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
