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
