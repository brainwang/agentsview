package parser

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// codefreeTurnTimestamps holds the start and end times for a single
// CodeFree turn, extracted from the agenthost.log file.
type codefreeTurnTimestamps struct {
	Started  time.Time
	Terminal time.Time // zero if not found
}

// codefreeLogTimestampMap maps "sessionID/turnId" to the turn's
// timestamps extracted from CodeFree agenthost.log files.
type codefreeLogTimestampMap map[string]codefreeTurnTimestamps

// codefreeLogTimestampKey builds the lookup key for a session+turn pair.
func codefreeLogTimestampKey(sessionID, turnID string) string {
	return sessionID + "/" + turnID
}

// codefreeLogTimestampRegex matches the two log lines we care about:
//
//	YYYY-MM-DD HH:MM:SS.mmm [info] [CodeFree] Turn started session=<id> turn=<turnId> ...
//	YYYY-MM-DD HH:MM:SS.mmm [info] [CodeFree] Turn terminal session=<id> turn=<turnId> ...
//
// The timestamp is the first 23 characters of the line. We match the
// "Turn started" / "Turn terminal" marker and capture session and turn
// IDs from the key=value pairs that follow.
var codefreeLogTurnStartedRe = regexp.MustCompile(
	`\[CodeFree\] Turn started session=(\S+) turn=(\S+)`,
)
var codefreeLogTurnTerminalRe = regexp.MustCompile(
	`\[CodeFree\] Turn terminal session=(\S+) turn=(\S+)`,
)

// codefreeLogTimestampLayout is the date/time format used at the start
// of every agenthost.log line: "2026-11-09 16:32:05.354".
const codefreeLogTimestampLayout = "2006-01-02 15:04:05.000"

// codefreeLogTimezone is the timezone used by CodeFree agenthost.log
// timestamps. The log writes local wall-clock time, which is GMT+8
// (Asia/Shanghai) in the deployment environment. time.Parse without a
// zone returns UTC, so we must re-interpret the parsed time in the
// correct zone and convert to UTC for storage.
var codefreeLogTimezone = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		// Fallback: fixed offset of +8 hours if the timezone
		// database is not available.
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}()

// loadCodeFreeLogTimestamps scans all agenthost.log files under the
// logs directory sibling to the agentSessionData root and builds a
// map of sessionID/turnId → turn timestamps.
//
// The logs directory layout is:
//
//	<appData>/CodeFree/agentSessionData/   ← provider root
//	<appData>/CodeFree/logs/<yyyyMMddTHHmmss>/agenthost.log
//
// Multiple log directories may exist (one per IDE launch). Historical
// directories may have been rotated out, so the map may be incomplete.
// The caller must handle missing entries with a fallback strategy.
//
// If the logs directory does not exist or no log files are found, an
// empty map and nil error are returned.
func loadCodeFreeLogTimestamps(root string) (codefreeLogTimestampMap, error) {
	logsDir := codefreeLogsDirForRoot(root)
	if logsDir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read codefree logs dir %s: %w", logsDir, err)
	}

	// Sort directories by name (ascending = oldest first). When the
	// same turn appears in multiple log files (e.g. started in one
	// session and terminal in a later one), we want deterministic
	// ordering: first-seen wins for started, last-seen wins for
	// terminal. In practice a turn's started and terminal almost
	// always appear in the same log file.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	m := make(codefreeLogTimestampMap)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		logPath := filepath.Join(logsDir, entry.Name(), "agenthost.log")
		if err := codefreeScanLogForTurnTimestamps(logPath, m); err != nil {
			// A single unreadable log file should not prevent
			// parsing. Skip it and continue with what we have.
			continue
		}
	}
	return m, nil
}

// codefreeLogsDirForRoot derives the logs directory path from the
// agentSessionData root. Returns "" if the root is empty or does not
// have a parent (should not happen in practice).
func codefreeLogsDirForRoot(root string) string {
	if root == "" {
		return ""
	}
	parent := filepath.Dir(root)
	return filepath.Join(parent, "logs")
}

// codefreeScanLogForTurnTimestamps reads one agenthost.log file and
// populates the map with Turn started / Turn terminal timestamps.
func codefreeScanLogForTurnTimestamps(
	logPath string, m codefreeLogTimestampMap,
) error {
	f, err := os.Open(logPath)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// agenthost.log lines can be long (they include settings scopes,
	// skill lists, etc.). Allow up to 1 MB per line.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		codefreeParseLogLineForTurnTimestamp(line, m)
	}
	return scanner.Err()
}

// codefreeParseLogLineForTurnTimestamp examines a single log line and
// updates the map if the line is a "Turn started" or "Turn terminal"
// entry. The timestamp is parsed from the first 23 characters of the
// line.
func codefreeParseLogLineForTurnTimestamp(
	line string, m codefreeLogTimestampMap,
) {
	// Fast path: skip lines that don't contain the marker.
	if !strings.Contains(line, "[CodeFree] Turn ") {
		return
	}

	// Extract timestamp from the beginning of the line.
	// Format: "2026-11-09 16:32:05.354 ..."
	if len(line) < 23 {
		return
	}
	ts, err := time.Parse(codefreeLogTimestampLayout, line[:23])
	if err != nil {
		return
	}
	// The log writes local wall-clock time (GMT+8). time.Parse
	// without a zone returns UTC, which would make the stored
	// timestamp 8 hours too late. Re-interpret the parsed
	// wall-clock components in the correct zone, then convert to
	// UTC for storage.
	ts = time.Date(
		ts.Year(), ts.Month(), ts.Day(),
		ts.Hour(), ts.Minute(), ts.Second(), ts.Nanosecond(),
		codefreeLogTimezone,
	).UTC()

	// Try "Turn started" first.
	if m2 := codefreeLogTurnStartedRe.FindStringSubmatch(line); m2 != nil {
		key := codefreeLogTimestampKey(m2[1], m2[2])
		entry := m[key] // zero value if absent
		// First-seen wins for started (oldest log dir is scanned
		// first). A duplicate in a later log dir is ignored.
		if entry.Started.IsZero() {
			entry.Started = ts
		}
		m[key] = entry
		return
	}

	// Try "Turn terminal".
	if m2 := codefreeLogTurnTerminalRe.FindStringSubmatch(line); m2 != nil {
		key := codefreeLogTimestampKey(m2[1], m2[2])
		entry := m[key] // zero value if absent
		// Last-seen wins for terminal (in case a turn's terminal
		// appears in a later log dir than its started).
		entry.Terminal = ts
		m[key] = entry
	}
}

// codefreeToolOutputDir is the subdirectory under a session's working
// directory where CodeFree writes tool call output files. Each terminal-
// type tool call produces a file named "<toolCallId>-run_in_terminal.txt".
const codefreeToolOutputDir = ".codefree" + string(filepath.Separator) + "tooloutput"

// codefreeToolOutputSuffix is the filename suffix for terminal tool call
// output files.
const codefreeToolOutputSuffix = "-run_in_terminal.txt"

// loadCodeFreeToolOutputTimestamps scans the working directory's
// .codefree/tooloutput/ folder for files named
// "<toolCallId>-run_in_terminal.txt" and uses their modification time
// as a fallback timestamp source for turns that lack an agenthost.log
// entry.
//
// The mtime of a tooloutput file approximates when the tool call was
// executed, which is close to the turn's start time. When a turn has
// multiple tool calls with output files, the earliest mtime is used
// (closest to the turn start).
//
// This function only returns entries for turns that have at least one
// matching tooloutput file. The caller should merge these into the log
// timestamp map, giving log timestamps priority.
//
// If workingDir is empty or the tooloutput directory does not exist,
// an empty map and nil error are returned.
func loadCodeFreeToolOutputTimestamps(
	workingDir, sessionID string, turns []gjson.Result,
) codefreeLogTimestampMap {
	if workingDir == "" || sessionID == "" || len(turns) == 0 {
		return nil
	}

	toolOutputDir := filepath.Join(workingDir, codefreeToolOutputDir)

	m := make(codefreeLogTimestampMap)

	for _, turn := range turns {
		turnID := turn.Get("history.turnId").Str
		if turnID == "" {
			continue
		}
		key := codefreeLogTimestampKey(sessionID, turnID)

		parts := turn.Get("history.parts")
		if !parts.IsArray() {
			continue
		}

		var earliest time.Time
		for _, part := range parts.Array() {
			if part.Get("type").Str != "toolCall" {
				continue
			}
			toolCallID := part.Get("toolCallId").Str
			if toolCallID == "" {
				continue
			}

			filename := toolCallID + codefreeToolOutputSuffix
			filePath := filepath.Join(toolOutputDir, filename)
			info, err := os.Stat(filePath)
			if err != nil {
				// File doesn't exist or is inaccessible — skip.
				continue
			}

			mtime := info.ModTime()
			if earliest.IsZero() || mtime.Before(earliest) {
				earliest = mtime
			}
		}

		if !earliest.IsZero() {
			// Only set Started; Terminal is left zero since we
			// can't determine turn end from tooloutput files.
			m[key] = codefreeTurnTimestamps{Started: earliest}
		}
	}

	if len(m) == 0 {
		return nil
	}
	return m
}

// mergeToolOutputTimestamps adds entries from the tooloutput map only
// for keys that do not already exist in the log map (log timestamps
// take priority). The log map is modified in place.
func mergeToolOutputTimestamps(
	logTS, toolOutputTS codefreeLogTimestampMap,
) codefreeLogTimestampMap {
	if len(toolOutputTS) == 0 {
		return logTS
	}
	if logTS == nil {
		logTS = make(codefreeLogTimestampMap)
	}
	for key, ts := range toolOutputTS {
		if existing, ok := logTS[key]; !ok || existing.Started.IsZero() {
			// Only fill if the log map doesn't already have a
			// valid Started timestamp for this turn.
			if !ok {
				logTS[key] = ts
			} else {
				// Key exists but Started is zero — fill in
				// the Started from tooloutput, preserve
				// any existing Terminal.
				existing.Started = ts.Started
				logTS[key] = existing
			}
		}
	}
	return logTS
}

// lookupTurnTimestamp returns the start timestamp for a turn from the
// log map. Returns (time, true) if found, (zero, false) otherwise.
func (m codefreeLogTimestampMap) lookupTurnTimestamp(
	sessionID, turnID string,
) (time.Time, bool) {
	if m == nil || turnID == "" {
		return time.Time{}, false
	}
	entry, ok := m[codefreeLogTimestampKey(sessionID, turnID)]
	if !ok || entry.Started.IsZero() {
		return time.Time{}, false
	}
	return entry.Started, true
}

// resolveTurnTimestamps builds the final per-turn timestamp list for a
// session's turns, using a three-tier strategy:
//
//  1. If a turn has a real timestamp in the log map, use it.
//  2. If not, interpolate from the nearest known neighbours. When both
//     a preceding and a following known turn exist, linearly interpolate
//     between their timestamps so that consecutive missing turns get
//     gradually increasing values rather than identical copies. When
//     only one side has a known turn (leading or trailing gap), carry
//     that timestamp forward/backward unchanged.
//  3. If no turn in the entire list has a log timestamp, fall back to
//     distributing the session duration evenly across all turns
//     (startTime + i * stepDuration). If the session duration is zero
//     or there is only one turn, every turn gets startTime.
//
// turnIDs is ordered: index 0 is the first completedTurn, followed by
// remaining completedTurns, then inFlightTurns. sessionStartTime and
// sessionEndTime come from the state JSON's session.startTime and
// session.modifiedTime.
func resolveTurnTimestamps(
	logTS codefreeLogTimestampMap,
	sessionID string,
	turnIDs []string,
	sessionStartTime, sessionEndTime time.Time,
) []time.Time {
	n := len(turnIDs)
	if n == 0 {
		return nil
	}

	result := make([]time.Time, n)

	// Pass 1: fill from log timestamps.
	knownCount := 0
	for i, tid := range turnIDs {
		if ts, ok := logTS.lookupTurnTimestamp(sessionID, tid); ok {
			result[i] = ts
			knownCount++
		}
	}

	// If every turn has a log timestamp, we're done.
	if knownCount == n {
		return result
	}

	// Pass 2: fill missing entries using neighbour-based interpolation.
	// For each gap of consecutive missing turns between two known
	// turns, linearly interpolate so the timestamps increase
	// gradually. For leading gaps (before the first known turn) and
	// trailing gaps (after the last known turn), carry the single
	// known neighbour's timestamp unchanged.
	if knownCount > 0 {
		// Identify the indices of known turns (from the log, not
		// filled values).
		knownIdxs := make([]int, 0, knownCount)
		for i, tid := range turnIDs {
			if _, ok := logTS.lookupTurnTimestamp(sessionID, tid); ok {
				knownIdxs = append(knownIdxs, i)
			}
		}

		firstKnown := knownIdxs[0]
		lastKnown := knownIdxs[len(knownIdxs)-1]

		// Leading gap: turns before the first known turn get the
		// first known timestamp.
		for i := 0; i < firstKnown; i++ {
			result[i] = result[firstKnown]
		}

		// Interior gaps: linearly interpolate between consecutive
		// known turns.
		for k := 0; k < len(knownIdxs)-1; k++ {
			lo := knownIdxs[k]
			hi := knownIdxs[k+1]
			gap := hi - lo // number of intervals (>= 2 when there are missing turns)
			if gap <= 1 {
				continue // adjacent, nothing to fill
			}
			loTS := result[lo]
			hiTS := result[hi]
			step := hiTS.Sub(loTS) / time.Duration(gap)
			for j := lo + 1; j < hi; j++ {
				result[j] = loTS.Add(time.Duration(j-lo) * step)
			}
		}

		// Trailing gap: turns after the last known turn get the
		// last known timestamp.
		for i := lastKnown + 1; i < n; i++ {
			result[i] = result[lastKnown]
		}

		return result
	}

	// Pass 3 (fallback): no log timestamps at all. Distribute session
	// duration evenly across all turns.
	var stepDuration time.Duration
	if n > 1 && sessionEndTime.After(sessionStartTime) {
		stepDuration = sessionEndTime.Sub(sessionStartTime) / time.Duration(n)
	}
	for i := 0; i < n; i++ {
		result[i] = sessionStartTime.Add(time.Duration(i) * stepDuration)
	}
	return result
}
