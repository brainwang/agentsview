package parser

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedCodeFreeSessionDB(
	t *testing.T,
	root, sessionID, stateJSON string,
) string {
	t.Helper()
	sessionDir := filepath.Join(root, sessionID)
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))
	dbPath := filepath.Join(sessionDir, codefreeSQLiteDBName)
	_, db := newCodeFreeSQLiteTestDBAt(t, dbPath)
	seedCodeFreeMetadata(t, db, codefreeStateKey, stateJSON)
	db.Close()
	return dbPath
}

func seedCodeFreeCatalogDB(
	t *testing.T,
	root string,
	entries []CodeFreeCatalogEntry,
) {
	t.Helper()
	catalogDir := filepath.Join(root, codefreeCatalogDir)
	require.NoError(t, os.MkdirAll(catalogDir, 0o755))
	catalogDBPath := filepath.Join(catalogDir, codefreeSQLiteDBName)
	_, db := newCodeFreeSQLiteTestDBAt(t, catalogDBPath)
	// Build catalog JSON.
	catalogJSON := `{"sessions":[`
	for i, e := range entries {
		if i > 0 {
			catalogJSON += ","
		}
		catalogJSON += `{"session":{"sessionId":"` + e.SessionID +
			`","startTime":` + strconv.FormatInt(e.StartTime, 10) +
			`,"modifiedTime":` + strconv.FormatInt(e.ModifiedTime, 10) +
			`,"summary":"` + e.Summary +
			`","workingDirectory":"` + e.WorkingDir +
			`"},"defaultAgentUri":"` + e.DefaultAgentURI + `"}`
	}
	catalogJSON += `]}`
	seedCodeFreeMetadata(t, db, codefreeCatalogKey, catalogJSON)
	db.Close()
}

func TestCodeFreeSourceSet_Discover(t *testing.T) {
	root := t.TempDir()
	seedCodeFreeCatalogDB(t, root, []CodeFreeCatalogEntry{
		{SessionID: "uuid-disc-1", StartTime: 1000, ModifiedTime: 2000, Summary: "Session 1", WorkingDir: "file:///tmp", DefaultAgentURI: "codefree-agent://builtin/spec"},
		{SessionID: "uuid-disc-2", StartTime: 3000, ModifiedTime: 4000, Summary: "Session 2", WorkingDir: "file:///tmp", DefaultAgentURI: "codefree-agent://builtin/explore"},
	})
	// Create session DBs.
	stateJSON := `{"session":{"sessionId":"x","startTime":1,"modifiedTime":2,"summary":"x","workingDirectory":""},"chats":[{"agent":{"uri":"codefree-agent://builtin/spec"}}],"completedTurns":[{"history":{"userMessage":{"text":"hi"},"parts":[{"type":"text","text":"hello"}]}}],"inFlightTurns":[]}`
	seedCodeFreeSessionDB(t, root, "uuid-disc-1", stateJSON)
	seedCodeFreeSessionDB(t, root, "uuid-disc-2", stateJSON)

	ss := newCodeFreeSourceSet([]string{root})
	sources, err := ss.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 2)
	// Sources should be sorted by sortJSONLSources.
	assert.Equal(t, "uuid-disc-1", sources[0].Key)
	assert.Equal(t, "uuid-disc-2", sources[1].Key)
	assert.Equal(t, AgentCodeFree, sources[0].Provider)
}

func TestCodeFreeSourceSet_Discover_SkipsMissingDB(t *testing.T) {
	root := t.TempDir()
	seedCodeFreeCatalogDB(t, root, []CodeFreeCatalogEntry{
		{SessionID: "uuid-exists", StartTime: 1, ModifiedTime: 2, Summary: "Exists", WorkingDir: "", DefaultAgentURI: "codefree-agent://builtin/spec"},
		{SessionID: "uuid-missing", StartTime: 1, ModifiedTime: 2, Summary: "Missing", WorkingDir: "", DefaultAgentURI: "codefree-agent://builtin/spec"},
	})
	stateJSON := `{"session":{"sessionId":"x","startTime":1,"modifiedTime":2,"summary":"x","workingDirectory":""},"chats":[{"agent":{"uri":"codefree-agent://builtin/spec"}}],"completedTurns":[{"history":{"userMessage":{"text":"hi"},"parts":[{"type":"text","text":"hello"}]}}],"inFlightTurns":[]}`
	seedCodeFreeSessionDB(t, root, "uuid-exists", stateJSON)
	// Do NOT create session DB for uuid-missing.

	ss := newCodeFreeSourceSet([]string{root})
	sources, err := ss.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1, "missing session DB must be skipped")
	assert.Equal(t, "uuid-exists", sources[0].Key)
}

func TestCodeFreeSourceSet_DiscoverEach(t *testing.T) {
	root := t.TempDir()
	seedCodeFreeCatalogDB(t, root, []CodeFreeCatalogEntry{
		{SessionID: "uuid-stream-1", StartTime: 1, ModifiedTime: 2, Summary: "S1", WorkingDir: "", DefaultAgentURI: "codefree-agent://builtin/spec"},
	})
	stateJSON := `{"session":{"sessionId":"x","startTime":1,"modifiedTime":2,"summary":"x","workingDirectory":""},"chats":[{"agent":{"uri":"codefree-agent://builtin/spec"}}],"completedTurns":[{"history":{"userMessage":{"text":"hi"},"parts":[{"type":"text","text":"hello"}]}}],"inFlightTurns":[]}`
	seedCodeFreeSessionDB(t, root, "uuid-stream-1", stateJSON)

	ss := newCodeFreeSourceSet([]string{root})
	var collected []SourceRef
	err := ss.DiscoverEach(context.Background(), func(src SourceRef) error {
		collected = append(collected, src)
		return nil
	})
	require.NoError(t, err)
	require.Len(t, collected, 1)
	assert.Equal(t, "uuid-stream-1", collected[0].Key)
}

func TestCodeFreeSourceSet_Fingerprint(t *testing.T) {
	root := t.TempDir()
	stateJSON := `{"session":{"sessionId":"x","startTime":1,"modifiedTime":2,"summary":"x","workingDirectory":""},"chats":[{"agent":{"uri":"codefree-agent://builtin/spec"}}],"completedTurns":[{"history":{"userMessage":{"text":"hi"},"parts":[{"type":"text","text":"hello"}]}}],"inFlightTurns":[]}`
	dbPath := seedCodeFreeSessionDB(t, root, "uuid-fp", stateJSON)
	virtualPath := CodeFreeVirtualPath(dbPath, "uuid-fp")

	ss := newCodeFreeSourceSet([]string{root})
	source := ss.newSourceRef(root, virtualPath, dbPath, "uuid-fp")
	fp, err := ss.Fingerprint(context.Background(), source)
	require.NoError(t, err)
	assert.NotEmpty(t, fp.Key)
	assert.Greater(t, fp.Size, int64(0))
	assert.Greater(t, fp.MTimeNS, int64(0))
}

func TestCodeFreeSourceSet_Fingerprint_MissingDB(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "uuid-gone", codefreeSQLiteDBName)
	virtualPath := CodeFreeVirtualPath(dbPath, "uuid-gone")
	ss := newCodeFreeSourceSet([]string{root})
	source := ss.newSourceRef(root, virtualPath, dbPath, "uuid-gone")
	fp, err := ss.Fingerprint(context.Background(), source)
	require.NoError(t, err)
	assert.NotEmpty(t, fp.Key)
	assert.Equal(t, int64(0), fp.Size, "missing DB must have zero size")
	assert.Equal(t, int64(0), fp.MTimeNS, "missing DB must have zero mtime")
}

func TestCodeFreeSourceSet_WatchPlan(t *testing.T) {
	root := t.TempDir()
	seedCodeFreeCatalogDB(t, root, []CodeFreeCatalogEntry{
		{SessionID: "uuid-watch", StartTime: 1, ModifiedTime: 2, Summary: "Watch", WorkingDir: "", DefaultAgentURI: "codefree-agent://builtin/spec"},
	})
	stateJSON := `{"session":{"sessionId":"x","startTime":1,"modifiedTime":2,"summary":"x","workingDirectory":""},"chats":[{"agent":{"uri":"codefree-agent://builtin/spec"}}],"completedTurns":[{"history":{"userMessage":{"text":"hi"},"parts":[{"type":"text","text":"hello"}]}}],"inFlightTurns":[]}`
	seedCodeFreeSessionDB(t, root, "uuid-watch", stateJSON)

	ss := newCodeFreeSourceSet([]string{root})
	plan, err := ss.WatchPlan(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, plan.Roots)
	// Should have: root watch, catalog DB watch, session DB watch.
	assert.GreaterOrEqual(t, len(plan.Roots), 3)
}

func TestCodeFreeSourceSet_FindSource_BySessionID(t *testing.T) {
	root := t.TempDir()
	stateJSON := `{"session":{"sessionId":"uuid-find","startTime":1,"modifiedTime":2,"summary":"Find","workingDirectory":""},"chats":[{"agent":{"uri":"codefree-agent://builtin/spec"}}],"completedTurns":[{"history":{"userMessage":{"text":"hi"},"parts":[{"type":"text","text":"hello"}]}}],"inFlightTurns":[]}`
	seedCodeFreeSessionDB(t, root, "uuid-find", stateJSON)

	ss := newCodeFreeSourceSet([]string{root})
	source, found, err := ss.FindSource(context.Background(), FindSourceRequest{
		RawSessionID: "uuid-find",
	})
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "uuid-find", source.Key)
}

func TestCodeFreeSourceSet_FindSource_NotFound(t *testing.T) {
	root := t.TempDir()
	ss := newCodeFreeSourceSet([]string{root})
	_, found, err := ss.FindSource(context.Background(), FindSourceRequest{
		RawSessionID: "uuid-nonexistent",
	})
	require.NoError(t, err)
	assert.False(t, found)
}

func TestCodeFreeSourceSet_SourcesForChangedPath(t *testing.T) {
	root := t.TempDir()
	stateJSON := `{"session":{"sessionId":"uuid-changed","startTime":1,"modifiedTime":2,"summary":"Changed","workingDirectory":""},"chats":[{"agent":{"uri":"codefree-agent://builtin/spec"}}],"completedTurns":[{"history":{"userMessage":{"text":"hi"},"parts":[{"type":"text","text":"hello"}]}}],"inFlightTurns":[]}`
	dbPath := seedCodeFreeSessionDB(t, root, "uuid-changed", stateJSON)

	ss := newCodeFreeSourceSet([]string{root})
	sources, err := ss.SourcesForChangedPath(context.Background(), ChangedPathRequest{
		Path: dbPath,
	})
	require.NoError(t, err)
	require.Len(t, sources, 1)
	assert.Equal(t, "uuid-changed", sources[0].Key)
}

func TestCodeFreeProvider_Parse(t *testing.T) {
	root := t.TempDir()
	stateJSON := `{"session":{"sessionId":"uuid-parse","startTime":1000,"modifiedTime":2000,"summary":"Parse test","workingDirectory":"file:///tmp"},"chats":[{"agent":{"uri":"codefree-agent://builtin/spec"}}],"completedTurns":[{"history":{"userMessage":{"text":"hello world"},"parts":[{"type":"text","text":"hi there"}]},"usage":{"inputTokens":50,"outputTokens":25,"model":"GLM-5.2"}}],"inFlightTurns":[]}`
	dbPath := seedCodeFreeSessionDB(t, root, "uuid-parse", stateJSON)
	virtualPath := CodeFreeVirtualPath(dbPath, "uuid-parse")

	def := AgentDef{Type: AgentCodeFree, IDPrefix: "codefree:"}
	factory := newCodeFreeProviderFactory(def)
	provider := factory.NewProvider(ProviderConfig{Roots: []string{root}})

	source := SourceRef{
		Provider:       AgentCodeFree,
		ConfiguredRoot: root,
		Key:            "uuid-parse",
		DisplayPath:    virtualPath,
		FingerprintKey: virtualPath,
		Opaque: codefreeSource{
			Root:      root,
			Path:      virtualPath,
			DBPath:    dbPath,
			SessionID: "uuid-parse",
			Kind:      codefreeSourceSQLiteSession,
		},
	}

	outcome, err := provider.Parse(context.Background(), ParseRequest{
		Source: source,
	})
	require.NoError(t, err)
	assert.True(t, outcome.ResultSetComplete)
	assert.True(t, outcome.ForceReplace)
	require.Len(t, outcome.Results, 1)
	sess := outcome.Results[0].Result.Session
	msgs := outcome.Results[0].Result.Messages
	assert.Equal(t, "codefree:uuid-parse", sess.ID)
	assert.Equal(t, "Parse test", sess.SessionName)
	require.Len(t, msgs, 2)
	assert.Equal(t, "hello world", msgs[0].Content)
	assert.Equal(t, "hi there", msgs[1].Content)
	assert.Equal(t, "GLM-5.2", msgs[1].Model)
}

func TestCodeFreeProvider_Parse_MissingDB(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "uuid-missing", codefreeSQLiteDBName)
	virtualPath := CodeFreeVirtualPath(dbPath, "uuid-missing")

	def := AgentDef{Type: AgentCodeFree, IDPrefix: "codefree:"}
	factory := newCodeFreeProviderFactory(def)
	provider := factory.NewProvider(ProviderConfig{Roots: []string{root}})

	source := SourceRef{
		Provider:       AgentCodeFree,
		ConfiguredRoot: root,
		Key:            "uuid-missing",
		DisplayPath:    virtualPath,
		FingerprintKey: virtualPath,
		Opaque: codefreeSource{
			Root:      root,
			Path:      virtualPath,
			DBPath:    dbPath,
			SessionID: "uuid-missing",
			Kind:      codefreeSourceSQLiteSession,
		},
	}

	outcome, err := provider.Parse(context.Background(), ParseRequest{
		Source: source,
	})
	require.NoError(t, err)
	assert.True(t, outcome.ResultSetComplete)
	assert.Equal(t, SkipNoSession, outcome.SkipReason,
		"missing DB must return SkipNoSession without ForceReplace")
	assert.False(t, outcome.ForceReplace)
}
