package parser

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// codefreeToolCallTime holds the start and optional end time for a single
// tool call, extracted from CodeFree IDE log files.
type codefreeToolCallTime struct {
	Start time.Time // zero = unknown
	End   time.Time // zero = unknown (only code_search.operation_completed)
}

// codefreeToolCallTimeMap maps toolCallId to its time information.
type codefreeToolCallTimeMap map[string]codefreeToolCallTime

// --- Log line regexes for tool-call-level timestamps ---
//
// Four event types provide real tool-call timestamps:
//
//  1. renderer.log: "[AgentHost] Invoking client tool: <name> (toolCallId=<id>, ...)"
//  2. agenthost.log: "[code-search] event=code_search.dispatch_started ... toolCallId=<id> ..."
//  3. agenthost.log: "[code-search] event=code_search.operation_completed ... toolCallId=<id> ..."
//  4. agenthost.log: "[codeFree] sibling tool invokes overlapped: N calls in Xms (ids=id1,id2,...)"
//     and "[codeFree] independent read-only invokes overlapped: N calls in Xms (ids=...)"

var codefreeReInvokingClientTool = regexp.MustCompile(
	`\[AgentHost\] Invoking client tool: \S+ \(toolCallId=([^,)]+)`,
)

var codefreeReCodeSearchDispatch = regexp.MustCompile(
	`event=code_search\.dispatch_started\b.*?\btoolCallId=(\S+)`,
)

var codefreeReCodeSearchCompleted = regexp.MustCompile(
	`event=code_search\.operation_completed\b.*?\btoolCallId=(\S+)`,
)

var codefreeReOverlapIDs = regexp.MustCompile(
	`(?:sibling tool invokes overlapped|independent read-only invokes overlapped): \d+ calls in \d+ms \(ids=([^)]+)\)`,
)

var codefreeReSubagentCreate = regexp.MustCompile(
	`Creating subagent chat: \S+/(toolCallId=\S+?)\s`,
)

// codefreeReSubagentToolCallID extracts toolCallId from the
// "Creating subagent chat" line which embeds it in the URL path
// or as a toolCallId= parameter.
var codefreeReSubagentToolCallID = regexp.MustCompile(
	`toolCallId=(\S+)`,
)

// --- Timezone resolution ---

// resolveCodeFreeLogLocation determines the timezone to use when parsing
// CodeFree log timestamps. Log lines carry wall-clock time without a
// zone marker. The resolution uses a three-tier fallback:
//
//  1. Anchor calibration: compare the earliest log event wall-clock time
//     for this session against the session's epoch startTime. The offset
//     (rounded to 15-minute multiples) that minimises the residual is
//     used as a FixedZone. This works across timezones and machines.
//  2. time.Local: when CodeFree and agentsview run on the same machine,
//     the local timezone is the one used when the log was written.
//  3. Fixed +8 fallback: time.FixedZone("CST", 8*3600) for the known
//     deployment environment.
//
// All parameters are optional; missing ones cause fallback to the next
// tier. The returned location is never nil.
func resolveCodeFreeLogLocation(
	logsRoot, sessionID string,
	sessionStartTimeEpochMs int64,
) *time.Location {
	// Tier 1: anchor calibration.
	if logsRoot != "" && sessionID != "" && sessionStartTimeEpochMs > 0 {
		if loc := codefreeCalibrateTimezoneFromAnchor(logsRoot, sessionID, sessionStartTimeEpochMs); loc != nil {
			return loc
		}
	}

	// Tier 2: local timezone.
	// time.Local is set by the Go runtime from the OS. When CodeFree
	// and agentsview run on the same machine, this is correct.
	if time.Local != nil {
		return time.Local
	}

	// Tier 3: fixed +8 fallback.
	return time.FixedZone("CST", 8*3600)
}

// codefreeCalibrateTimezoneFromAnchor scans log files for the earliest
// event line that mentions sessionID, parses its wall-clock timestamp,
// and computes the offset between that wall-clock-as-UTC epoch and the
// real session startTime epoch. The offset is rounded to 15-minute
// multiples (all IANA offsets are). If the residual after rounding is
// ≤ 10 minutes, the offset is accepted and returned as a FixedZone.
func codefreeCalibrateTimezoneFromAnchor(
	logsRoot, sessionID string,
	sessionStartTimeEpochMs int64,
) *time.Location {
	logsDir := codefreeLogsDirForRoot(logsRoot)
	if logsDir == "" {
		return nil
	}
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return nil
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	var earliestWall time.Time
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Check agenthost.log for "Turn started session=<sid>" or
		// any line containing the sessionID.
		logPath := filepath.Join(logsDir, entry.Name(), "agenthost.log")
		if ts, ok := codefreeFindEarliestSessionEvent(logPath, sessionID); ok {
			if earliestWall.IsZero() || ts.Before(earliestWall) {
				earliestWall = ts
			}
		}
	}

	if earliestWall.IsZero() {
		return nil
	}

	// Compute offset: the wall-clock time was parsed as UTC by
	// time.Parse, but it was actually written in the local timezone.
	// If the real session start (epoch) is EARLIER than the
	// wall-clock-as-UTC epoch, the timezone is positive (east of UTC).
	// Example: wall-clock 08:05 parsed as UTC → epoch X.
	// Real start 00:05 UTC → epoch X - 8h.
	// offset = wallAsUTCMs - startTimeEpochMs = +8h → FixedZone(+8h).
	// This is the correct timezone offset to add when re-interpreting
	// the wall-clock components.
	wallAsUTCMs := earliestWall.UnixMilli()
	offsetMs := wallAsUTCMs - sessionStartTimeEpochMs

	// Sanity check: timezone offsets are in the range [-14h, +14h].
	// If the offset is outside this range, the session startTime epoch
	// is likely a test fixture or otherwise unreliable, and anchor
	// calibration should fail rather than produce a nonsensical zone.
	const maxOffsetMs = 14 * 60 * 60 * 1000
	if offsetMs > maxOffsetMs || offsetMs < -maxOffsetMs {
		return nil
	}

	// Round to 15-minute multiples (truncate toward zero).
	const fifteenMinMs = 15 * 60 * 1000
	roundedOffsetMs := (offsetMs / fifteenMinMs) * fifteenMinMs

	// Check residual.
	residualMs := offsetMs - roundedOffsetMs
	if residualMs < 0 {
		residualMs = -residualMs
	}
	if residualMs > 10*60*1000 {
		// Residual > 10 minutes: calibration failed.
		return nil
	}

	offsetSec := int(roundedOffsetMs / 1000)
	return time.FixedZone("", offsetSec)
}

// codefreeFindEarliestSessionEvent scans one log file for the earliest
// line containing sessionID and returns its wall-clock timestamp
// (parsed as UTC, i.e. the raw wall-clock components with UTC zone).
func codefreeFindEarliestSessionEvent(logPath, sessionID string) (time.Time, bool) {
	f, err := os.Open(logPath)
	if err != nil {
		return time.Time{}, false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var earliest time.Time
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, sessionID) {
			continue
		}
		if len(line) < 23 {
			continue
		}
		ts, err := time.Parse(codefreeLogTimestampLayout, line[:23])
		if err != nil {
			continue
		}
		// Keep ts as-is (UTC zone from time.Parse). We only care
		// about the wall-clock components.
		if earliest.IsZero() || ts.Before(earliest) {
			earliest = ts
		}
		// Early exit: we scan directories oldest-first, so the
		// first hit in the oldest log is very likely the earliest.
		// But to be safe we scan the full file.
	}

	return earliest, !earliest.IsZero()
}

// --- Tool-call timestamp loading ---

// loadCodeFreeToolCallTimestamps scans agenthost.log and window*/renderer.log
// files under the logs directory and builds a map of toolCallId → time.
//
// The same toolCallId may appear in multiple log files; the earliest Start
// and latest End are kept. Log directories are scanned oldest-first for
// deterministic results.
//
// loc is the timezone to use when parsing wall-clock timestamps. It must
// not be nil. All returned times are converted to UTC.
//
// If the logs directory does not exist or no log files are found, an empty
// map is returned (not nil).
func loadCodeFreeToolCallTimestamps(
	logsRoot string, loc *time.Location,
) codefreeToolCallTimeMap {
	m := make(codefreeToolCallTimeMap)

	logsDir := codefreeLogsDirForRoot(logsRoot)
	if logsDir == "" {
		return m
	}

	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return m
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirPath := filepath.Join(logsDir, entry.Name())

		// Scan agenthost.log for code_search events and overlap lines.
		agenthostPath := filepath.Join(dirPath, "agenthost.log")
		codefreeScanAgenthostForToolCalls(agenthostPath, loc, m)

		// Scan window*/renderer.log for "Invoking client tool" lines.
		windowEntries, err := os.ReadDir(dirPath)
		if err != nil {
			continue
		}
		for _, we := range windowEntries {
			if !we.IsDir() || !strings.HasPrefix(we.Name(), "window") {
				continue
			}
			rendererPath := filepath.Join(dirPath, we.Name(), "renderer.log")
			codefreeScanRendererForToolCalls(rendererPath, loc, m)
		}
	}

	return m
}

// codefreeScanAgenthostForToolCalls reads one agenthost.log and extracts
// tool-call timestamps from code_search events, overlap lines, and
// subagent creation lines.
func codefreeScanAgenthostForToolCalls(
	logPath string, loc *time.Location, m codefreeToolCallTimeMap,
) {
	f, err := os.Open(logPath)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		codefreeParseAgenthostLineForToolCalls(line, loc, m)
	}
}

// codefreeScanRendererForToolCalls reads one renderer.log and extracts
// tool-call timestamps from "Invoking client tool" lines.
func codefreeScanRendererForToolCalls(
	logPath string, loc *time.Location, m codefreeToolCallTimeMap,
) {
	f, err := os.Open(logPath)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		codefreeParseRendererLineForToolCalls(line, loc, m)
	}
}

// codefreeParseAgenthostLineForToolCalls examines a single agenthost.log
// line and updates the tool-call time map if the line matches one of the
// supported event patterns.
func codefreeParseAgenthostLineForToolCalls(
	line string, loc *time.Location, m codefreeToolCallTimeMap,
) {
	// Fast-path filters.
	if !strings.Contains(line, "code_search.") &&
		!strings.Contains(line, "invokes overlapped") &&
		!strings.Contains(line, "Creating subagent chat") {
		return
	}

	ts, ok := codefreeParseLogTimestamp(line, loc)
	if !ok {
		return
	}

	// code_search.dispatch_started → Start
	if strings.Contains(line, "code_search.dispatch_started") {
		if sm := codefreeReCodeSearchDispatch.FindStringSubmatch(line); sm != nil {
			codefreeMergeToolCallTime(m, sm[1], ts, time.Time{})
		}
		return
	}

	// code_search.operation_completed → End
	if strings.Contains(line, "code_search.operation_completed") {
		if sm := codefreeReCodeSearchCompleted.FindStringSubmatch(line); sm != nil {
			codefreeMergeToolCallTime(m, sm[1], time.Time{}, ts)
		}
		return
	}

	// Overlap lines: all IDs share the same timestamp.
	if strings.Contains(line, "invokes overlapped") {
		if sm := codefreeReOverlapIDs.FindStringSubmatch(line); sm != nil {
			ids := strings.Split(sm[1], ",")
			for _, id := range ids {
				id = strings.TrimSpace(id)
				if id != "" {
					codefreeMergeToolCallTime(m, id, ts, time.Time{})
				}
			}
		}
		return
	}

	// Subagent creation: toolCallId is in the line.
	if strings.Contains(line, "Creating subagent chat") {
		if sm := codefreeReSubagentToolCallID.FindStringSubmatch(line); sm != nil {
			codefreeMergeToolCallTime(m, sm[1], ts, time.Time{})
		}
	}
}

// codefreeParseRendererLineForToolCalls examines a single renderer.log
// line and updates the tool-call time map if the line is an
// "Invoking client tool" event.
func codefreeParseRendererLineForToolCalls(
	line string, loc *time.Location, m codefreeToolCallTimeMap,
) {
	if !strings.Contains(line, "Invoking client tool") {
		return
	}

	ts, ok := codefreeParseLogTimestamp(line, loc)
	if !ok {
		return
	}

	if sm := codefreeReInvokingClientTool.FindStringSubmatch(line); sm != nil {
		codefreeMergeToolCallTime(m, sm[1], ts, time.Time{})
	}
}

// codefreeParseLogTimestamp extracts the wall-clock timestamp from the
// first 23 characters of a log line, reinterprets it in the given
// location, and converts to UTC. Returns (zero, false) on failure.
func codefreeParseLogTimestamp(line string, loc *time.Location) (time.Time, bool) {
	if len(line) < 23 {
		return time.Time{}, false
	}
	ts, err := time.Parse(codefreeLogTimestampLayout, line[:23])
	if err != nil {
		return time.Time{}, false
	}
	// Re-interpret wall-clock components in the given location, then
	// convert to UTC for storage.
	return time.Date(
		ts.Year(), ts.Month(), ts.Day(),
		ts.Hour(), ts.Minute(), ts.Second(), ts.Nanosecond(),
		loc,
	).UTC(), true
}

// codefreeMergeToolCallTime merges a start or end timestamp into the map
// for the given toolCallId. For Start, the earliest value is kept. For
// End, the latest value is kept.
func codefreeMergeToolCallTime(
	m codefreeToolCallTimeMap,
	toolCallID string,
	start, end time.Time,
) {
	if toolCallID == "" {
		return
	}
	entry := m[toolCallID] // zero value if absent
	if !start.IsZero() {
		if entry.Start.IsZero() || start.Before(entry.Start) {
			entry.Start = start
		}
	}
	if !end.IsZero() {
		if entry.End.IsZero() || end.After(entry.End) {
			entry.End = end
		}
	}
	m[toolCallID] = entry
}

// --- Turn-level timestamp from tool-call map ---

// codefreeTurnStartFromToolCalls computes the earliest tool-call Start
// time from the map for the toolCallIds in a single turn. Returns
// (time, true) if at least one toolCall has a Start; (zero, false)
// otherwise.
func codefreeTurnStartFromToolCalls(
	turn gjson.Result, toolCallTS codefreeToolCallTimeMap,
) (time.Time, bool) {
	parts := turn.Get("history.parts")
	if !parts.IsArray() {
		return time.Time{}, false
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
		tc, ok := toolCallTS[toolCallID]
		if !ok || tc.Start.IsZero() {
			continue
		}
		if earliest.IsZero() || tc.Start.Before(earliest) {
			earliest = tc.Start
		}
	}

	return earliest, !earliest.IsZero()
}

// --- Part-level timestamp resolution within a turn ---

// codefreeResolvePartTimestamps assigns timestamps to each part in a turn,
// using tool-call-level timestamps where available and interpolating
// the rest within the turn's time span.
//
// turnStart and turnEnd define the turn's time boundaries. parts is the
// array of parts from history.parts. toolCallTS provides real timestamps
// for individual tool calls.
//
// The strategy:
//  1. For toolCall parts with a real Start in toolCallTS, use it.
//  2. For text parts and toolCall parts without a real timestamp,
//     interpolate between adjacent real anchors within the turn.
//  3. If no part has a real timestamp, use turnStart for all.
//
// All timestamps are clamped to [sessionStart, sessionEnd] and are
// guaranteed monotonically non-decreasing within the turn.
func codefreeResolvePartTimestamps(
	parts []gjson.Result,
	turnStart, turnEnd time.Time,
	toolCallTS codefreeToolCallTimeMap,
	sessionStart, sessionEnd time.Time,
) []time.Time {
	n := len(parts)
	if n == 0 {
		return nil
	}

	result := make([]time.Time, n)

	// Pass 1: fill from tool-call timestamps.
	type anchor struct {
		idx int
		ts  time.Time
	}
	var anchors []anchor
	for i, part := range parts {
		if part.Get("type").Str != "toolCall" {
			continue
		}
		toolCallID := part.Get("toolCallId").Str
		if toolCallID == "" {
			continue
		}
		tc, ok := toolCallTS[toolCallID]
		if !ok || tc.Start.IsZero() {
			continue
		}
		ts := codefreeClampTimestamp(tc.Start, sessionStart, sessionEnd)
		result[i] = ts
		anchors = append(anchors, anchor{i, ts})
	}

	if len(anchors) == 0 {
		// No real anchors: use turnStart for all parts.
		ts := codefreeClampTimestamp(turnStart, sessionStart, sessionEnd)
		for i := 0; i < n; i++ {
			result[i] = ts
		}
		return result
	}

	// Sort anchors by index (they should already be in order).
	sort.Slice(anchors, func(i, j int) bool { return anchors[i].idx < anchors[j].idx })

	// Leading gap: parts before the first anchor get the first anchor's time.
	first := anchors[0]
	for i := 0; i < first.idx; i++ {
		result[i] = first.ts
	}

	// Interior gaps: linearly interpolate between consecutive anchors.
	for k := 0; k < len(anchors)-1; k++ {
		lo := anchors[k]
		hi := anchors[k+1]
		gap := hi.idx - lo.idx
		if gap <= 1 {
			continue
		}
		step := hi.ts.Sub(lo.ts) / time.Duration(gap)
		for j := lo.idx + 1; j < hi.idx; j++ {
			result[j] = lo.ts.Add(time.Duration(j-lo.idx) * step)
		}
	}

	// Trailing gap: parts after the last anchor get the last anchor's time.
	last := anchors[len(anchors)-1]
	for i := last.idx + 1; i < n; i++ {
		result[i] = last.ts
	}

	// Ensure monotonic non-decreasing.
	for i := 1; i < n; i++ {
		if result[i].Before(result[i-1]) {
			result[i] = result[i-1]
		}
	}

	return result
}

// codefreeClampTimestamp clamps ts to the [lo, hi] range. If hi is zero
// or before lo, only the lower bound is enforced.
func codefreeClampTimestamp(ts, lo, hi time.Time) time.Time {
	if !lo.IsZero() && ts.Before(lo) {
		return lo
	}
	if !hi.IsZero() && ts.After(hi) {
		return hi
	}
	return ts
}
