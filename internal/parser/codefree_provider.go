package parser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var _ Provider = (*codefreeProvider)(nil)

// codefreeProviderFactory builds a CodeFree provider for the
// provider-factory registry. It mirrors teleagentProviderFactory's shape.
type codefreeProviderFactory struct {
	def AgentDef
}

func newCodeFreeProviderFactory(def AgentDef) ProviderFactory {
	return codefreeProviderFactory{def: cloneAgentDef(def)}
}

func (f codefreeProviderFactory) Definition() AgentDef {
	return cloneAgentDef(f.def)
}

func (f codefreeProviderFactory) Capabilities() Capabilities {
	return codefreeProviderCapabilities()
}

func (f codefreeProviderFactory) NewProvider(cfg ProviderConfig) Provider {
	cfg = cfg.Clone()
	return &codefreeProvider{
		ProviderBase: ProviderBase{
			Def:    cloneAgentDef(f.def),
			Caps:   codefreeProviderCapabilities(),
			Config: cfg,
		},
		sources: newCodeFreeSourceSet(cfg.Roots),
	}
}

// codefreeProvider implements the Provider interface for CodeFree IDE.
// It mirrors teleagentProvider's structure: a ProviderBase plus a source
// set that owns discovery, fingerprinting, and parse dispatch over the
// per-session SQLite databases under agentSessionData/.
type codefreeProvider struct {
	ProviderBase
	sources codefreeSourceSet
}

func (p *codefreeProvider) Discover(ctx context.Context) ([]SourceRef, error) {
	return p.sources.Discover(ctx)
}

func (p *codefreeProvider) DiscoverEach(
	ctx context.Context, yield func(SourceRef) error,
) error {
	return p.sources.DiscoverEach(ctx, yield)
}

func (p *codefreeProvider) WatchPlan(ctx context.Context) (WatchPlan, error) {
	return p.sources.WatchPlan(ctx)
}

func (p *codefreeProvider) SourcesForChangedPath(
	ctx context.Context, req ChangedPathRequest,
) ([]SourceRef, error) {
	return p.sources.SourcesForChangedPath(ctx, req)
}

func (p *codefreeProvider) StoredSourceHintScopes(
	req ChangedPathRequest,
) []StoredSourceHintScope {
	return p.sources.StoredSourceHintScopes(req)
}

func (p *codefreeProvider) FindSource(
	ctx context.Context, req FindSourceRequest,
) (SourceRef, bool, error) {
	req = ProviderFindRequestWithRawSessionID(p.Def, req)
	return p.sources.FindSource(ctx, req)
}

func (p *codefreeProvider) Fingerprint(
	ctx context.Context, source SourceRef,
) (SourceFingerprint, error) {
	return p.sources.Fingerprint(ctx, source)
}

func (p *codefreeProvider) Parse(
	ctx context.Context, req ParseRequest,
) (ParseOutcome, error) {
	if err := ctx.Err(); err != nil {
		return ParseOutcome{}, err
	}
	src, ok := p.sources.sourceFromRef(req.Source)
	if !ok {
		return ParseOutcome{}, fmt.Errorf("codefree source path unavailable")
	}
	machine := firstNonEmptyJSONLString(req.Machine, p.Config.Machine)
	return p.parseSQLiteSession(ctx, src, machine)
}

// parseSQLiteSession parses one CodeFree session DB. A missing DB file
// preserves stored rows (no ForceReplace); a missing state key in a
// present DB force-replaces (the session was genuinely removed).
func (p *codefreeProvider) parseSQLiteSession(
	ctx context.Context, src codefreeSource, machine string,
) (ParseOutcome, error) {
	if _, err := os.Stat(src.DBPath); err != nil {
		if os.IsNotExist(err) {
			return ParseOutcome{
				ResultSetComplete: true,
				SkipReason:        SkipNoSession,
			}, nil
		}
		return ParseOutcome{}, fmt.Errorf("stat %s: %w", src.DBPath, err)
	}
	store, err := OpenCodeFreeSQLiteStore(src.DBPath)
	if err != nil {
		return ParseOutcome{}, err
	}
	defer store.Close()
	sess, msgs, err := loadCodeFreeSession(ctx, store.db, src.DBPath, src.SessionID, machine)
	if err != nil {
		return ParseOutcome{}, err
	}
	if sess == nil {
		return ParseOutcome{
			ResultSetComplete: true,
			ForceReplace:      true,
			SkipReason:        SkipNoSession,
		}, nil
	}
	return ParseOutcome{
		Results: []ParseResultOutcome{{
			Result: ParseResult{
				Session:  *sess,
				Messages: msgs,
			},
			DataVersion: DataVersionCurrent,
		}},
		ResultSetComplete: true,
		ForceReplace:      true,
	}, nil
}

// codefreeSourceKind distinguishes source kinds for the CodeFree provider.
// Unlike TeleAgent, CodeFree has no shared container kind: each session has
// its own session.db file.
type codefreeSourceKind uint8

const (
	codefreeSourceSQLiteSession codefreeSourceKind = iota + 1
)

// codefreeSource mirrors teleagentSource. Path is the SourceRef.Key /
// DisplayPath / FingerprintKey (the virtual path). DBPath is the resolved
// backing session.db. SessionID is the raw UUID from the catalog.
type codefreeSource struct {
	Root      string
	Path      string
	DBPath    string
	SessionID string
	Kind      codefreeSourceKind
}

// codefreeSourceSet mirrors teleagentSourceSet: it owns the configured
// roots and the discovery / FindSource / Fingerprint / WatchPlan /
// SourcesForChangedPath / StoredSourceHintScopes surface for the
// CodeFree per-session SQLite databases.
type codefreeSourceSet struct {
	roots   []string
	readDir func(string) ([]os.DirEntry, error)
}

func newCodeFreeSourceSet(roots []string) codefreeSourceSet {
	return codefreeSourceSet{
		roots:   cleanJSONLRoots(roots),
		readDir: os.ReadDir,
	}
}

// newSourceRef builds one virtual member SourceRef for a session.
func (s codefreeSourceSet) newSourceRef(
	root, virtualPath, dbPath, sessionID string,
) SourceRef {
	return SourceRef{
		Provider:       AgentCodeFree,
		ConfiguredRoot: root,
		Key:            sessionID,
		DisplayPath:    virtualPath,
		FingerprintKey: virtualPath,
		Opaque: codefreeSource{
			Root:      root,
			Path:      virtualPath,
			DBPath:    dbPath,
			SessionID: sessionID,
			Kind:      codefreeSourceSQLiteSession,
		},
	}
}

// sourceRef resolves a stored path to a SourceRef. allowMissing relaxes
// the regular-file requirement so a deleted DB still classifies for
// changed-path tombstones.
func (s codefreeSourceSet) sourceRef(
	root, path string, allowMissing bool,
) (SourceRef, bool) {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if dbPath, sessionID, ok := codefreeSQLiteVirtualPathParts(path); ok {
		if !codefreeDBUnderRoot(root, dbPath, !allowMissing) {
			return SourceRef{}, false
		}
		return s.newSourceRef(root, path, dbPath, sessionID), true
	}
	return SourceRef{}, false
}

// sourceFromRef rebuilds a codefreeSource from a SourceRef's Opaque
// payload, or by re-classifying its display path / fingerprint key
// against the configured roots when Opaque is absent.
func (s codefreeSourceSet) sourceFromRef(source SourceRef) (codefreeSource, bool) {
	switch src := source.Opaque.(type) {
	case codefreeSource:
		return src, src.Path != ""
	case *codefreeSource:
		if src != nil && src.Path != "" {
			return *src, true
		}
	}
	for _, candidate := range []string{source.DisplayPath, source.FingerprintKey, source.Key} {
		for _, root := range s.roots {
			if ref, ok := s.sourceRef(root, candidate, true); ok {
				if src, ok := ref.Opaque.(codefreeSource); ok {
					return src, true
				}
			}
		}
	}
	return codefreeSource{}, false
}

// Discover enumerates all CodeFree sessions across configured roots by
// reading each root's __catalog__/session.db catalog index.
func (s codefreeSourceSet) Discover(ctx context.Context) ([]SourceRef, error) {
	sources := make([]SourceRef, 0)
	seen := make(map[string]struct{})
	for _, root := range s.roots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		catalog, err := ListCodeFreeCatalog(root)
		if err != nil {
			return nil, err
		}
		for _, entry := range catalog {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			dbPath := filepath.Join(root, entry.SessionID, codefreeSQLiteDBName)
			if !IsRegularFile(dbPath) {
				continue
			}
			virtualPath := CodeFreeVirtualPath(dbPath, entry.SessionID)
			source := s.newSourceRef(root, virtualPath, dbPath, entry.SessionID)
			source.DiscoveryMTimeNS = entry.ModifiedTime * int64(time.Millisecond)
			addJSONLSource(source, &sources, seen)
		}
	}
	sortJSONLSources(sources)
	return sources, nil
}

// DiscoverEach streams the same set as Discover but yields each source
// as it is emitted, so streaming discovery never holds an archive-sized
// Go slice.
func (s codefreeSourceSet) DiscoverEach(
	ctx context.Context, yield func(SourceRef) error,
) error {
	for _, root := range s.roots {
		if err := ctx.Err(); err != nil {
			return err
		}
		catalog, err := ListCodeFreeCatalog(root)
		if err != nil {
			return err
		}
		for _, entry := range catalog {
			if err := ctx.Err(); err != nil {
				return err
			}
			dbPath := filepath.Join(root, entry.SessionID, codefreeSQLiteDBName)
			if !IsRegularFile(dbPath) {
				continue
			}
			virtualPath := CodeFreeVirtualPath(dbPath, entry.SessionID)
			source := s.newSourceRef(root, virtualPath, dbPath, entry.SessionID)
			source.DiscoveryMTimeNS = entry.ModifiedTime * int64(time.Millisecond)
			if err := yield(source); err != nil {
				return err
			}
		}
	}
	return nil
}

// WatchPlan declares a non-recursive watch on every configured root
// (to detect new session directories), on the catalog DB, and on each
// discovered session DB so writes trigger a re-sync.
func (s codefreeSourceSet) WatchPlan(context.Context) (WatchPlan, error) {
	roots := make([]WatchRoot, 0)
	for _, root := range s.roots {
		roots = append(roots, WatchRoot{
			Path:        root,
			Recursive:   false,
			DebounceKey: string(AgentCodeFree) + ":root:" + root,
		})
		catalogDB := filepath.Join(root, codefreeCatalogDir, codefreeSQLiteDBName)
		if IsRegularFile(catalogDB) {
			roots = append(roots, WatchRoot{
				Path:         catalogDB,
				Recursive:    false,
				IncludeGlobs: []string{codefreeSQLiteDBName, codefreeSQLiteDBName + "-*"},
				DebounceKey:  string(AgentCodeFree) + ":catalog:" + catalogDB,
			})
		}
		catalog, _ := ListCodeFreeCatalog(root)
		for _, entry := range catalog {
			dbPath := filepath.Join(root, entry.SessionID, codefreeSQLiteDBName)
			if IsRegularFile(dbPath) {
				roots = append(roots, WatchRoot{
					Path:         dbPath,
					Recursive:    false,
					IncludeGlobs: []string{codefreeSQLiteDBName, codefreeSQLiteDBName + "-*"},
					DebounceKey:  string(AgentCodeFree) + ":db:" + dbPath,
				})
			}
		}
	}
	return WatchPlan{Roots: roots}, nil
}

// SourcesForChangedPath classifies one changed path against the
// configured roots. A path naming a session DB (or its WAL/SHM sibling)
// yields the session source; a virtual member path yields that single
// session source.
func (s codefreeSourceSet) SourcesForChangedPath(
	ctx context.Context, req ChangedPathRequest,
) ([]SourceRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, root := range s.roots {
		source, ok := s.sourceRefForChangedPath(root, req.Path)
		if !ok {
			continue
		}
		return []SourceRef{source}, nil
	}
	return nil, nil
}

// StoredSourceHintScopes exposes the session DB path so the engine's
// stored-freshness page reads only that source's stored rows when a
// DB-level event fires.
func (s codefreeSourceSet) StoredSourceHintScopes(
	req ChangedPathRequest,
) []StoredSourceHintScope {
	for _, root := range s.roots {
		source, ok := s.sourceRefForChangedPath(root, req.Path)
		if !ok {
			continue
		}
		src, ok := s.sourceFromRef(source)
		if !ok {
			return nil
		}
		return []StoredSourceHintScope{{Path: src.Path}}
	}
	return nil
}

// FindSource resolves a stored path, fingerprint key, or raw session ID
// to a current source. Stored paths and fingerprint keys are tried
// first; raw session IDs hit the catalog directly.
func (s codefreeSourceSet) FindSource(
	ctx context.Context, req FindSourceRequest,
) (SourceRef, bool, error) {
	if err := ctx.Err(); err != nil {
		return SourceRef{}, false, err
	}
	for _, path := range []string{req.StoredFilePath, req.FingerprintKey} {
		if path == "" {
			continue
		}
		for _, root := range s.roots {
			if source, ok := s.sourceRef(root, path, true); ok {
				if req.RequireFreshSource && !codefreeSourceExists(source) {
					continue
				}
				if req.RawSessionID == "" {
					return source, true, nil
				}
				if codefreeSourceMatchesSession(source, req.RawSessionID) {
					return source, true, nil
				}
			}
		}
	}
	if req.RawSessionID == "" {
		return SourceRef{}, false, nil
	}
	for _, root := range s.roots {
		if CodeFreeSessionExists(root, req.RawSessionID) {
			dbPath := filepath.Join(root, req.RawSessionID, codefreeSQLiteDBName)
			virtualPath := CodeFreeVirtualPath(dbPath, req.RawSessionID)
			return s.newSourceRef(root, virtualPath, dbPath, req.RawSessionID), true, nil
		}
	}
	return SourceRef{}, false, nil
}

// Fingerprint returns the source freshness identity for one session
// source. MTimeNS is the composite DB+WAL mtime; Size is the DB file
// size. A vanished DB file returns a Key-only fingerprint so the parse
// path skips without ForceReplace.
func (s codefreeSourceSet) Fingerprint(
	ctx context.Context, source SourceRef,
) (SourceFingerprint, error) {
	if err := ctx.Err(); err != nil {
		return SourceFingerprint{}, err
	}
	src, ok := s.sourceFromRef(source)
	if !ok {
		return SourceFingerprint{}, fmt.Errorf("codefree source path unavailable")
	}
	key := firstNonEmptyJSONLString(source.FingerprintKey, source.Key, src.Path)
	info, err := os.Stat(src.DBPath)
	if err != nil {
		if os.IsNotExist(err) {
			return SourceFingerprint{Key: key}, nil
		}
		return SourceFingerprint{}, fmt.Errorf("stat %s: %w", src.DBPath, err)
	}
	if info.IsDir() {
		return SourceFingerprint{}, fmt.Errorf(
			"stat %s: source is a directory", src.DBPath,
		)
	}
	fingerprint := SourceFingerprint{
		Key:     key,
		Size:    info.Size(),
		MTimeNS: info.ModTime().UnixNano(),
	}
	if compositeMtime, err := sqliteDBCompositeMtime(
		src.DBPath, sqliteDBJournalSuffixes,
	); err == nil {
		fingerprint.MTimeNS = compositeMtime
	}
	return fingerprint, nil
}

// sourceRefForChangedPath classifies a changed path, accepting a
// missing DB file so a deletion (or a WAL/SHM sibling event) still
// resolves. CodeFree session DBs live at <root>/<sessionId>/session.db,
// so we check whether the changed path is a session.db (or its WAL/SHM
// sibling) under any session directory.
func (s codefreeSourceSet) sourceRefForChangedPath(
	root, path string,
) (SourceRef, bool) {
	if source, ok := s.sourceRef(root, path, false); ok {
		return source, true
	}
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	// Check if the path is a session.db or its WAL/SHM sibling under
	// <root>/<sessionId>/.
	rel, ok := relUnder(root, path)
	if !ok {
		return SourceRef{}, false
	}
	// rel is like "uuid-xxx/session.db" or "uuid-xxx/session.db-wal".
	relSlash := filepath.ToSlash(rel)
	parts := strings.SplitN(relSlash, "/", 2)
	if len(parts) != 2 {
		return SourceRef{}, false
	}
	sessionID := parts[0]
	fileName := parts[1]
	baseName := codefreeSQLiteDBName
	if fileName != baseName &&
		!strings.HasPrefix(fileName, baseName+"-") {
		return SourceRef{}, false
	}
	dbPath := filepath.Join(root, sessionID, codefreeSQLiteDBName)
	if !codefreeDBUnderRoot(root, dbPath, false) {
		return SourceRef{}, false
	}
	virtualPath := CodeFreeVirtualPath(dbPath, sessionID)
	return s.newSourceRef(root, virtualPath, dbPath, sessionID), true
}

// codefreeProviderCapabilities declares the provider's source, content,
// and sync capabilities. Mirrors TeleAgent but without PersistentArchive
// (each session has its own DB, so there is no shared container to lose).
func codefreeProviderCapabilities() Capabilities {
	source := jsonlFileProviderSourceCapabilities()
	source.StreamingDiscovery = CapabilitySupported
	source.StoredSourceHints = CapabilitySupported
	source.PerSessionErrors = CapabilitySupported
	source.ForceReplaceOnParse = CapabilitySupported
	return Capabilities{
		Source: source,
		Content: ContentCapabilities{
			FirstMessage:         CapabilitySupported,
			SessionName:          CapabilitySupported,
			Cwd:                  CapabilitySupported,
			Subagents:            CapabilitySupported,
			ToolCalls:            CapabilitySupported,
			ToolResults:          CapabilitySupported,
			PerMessageTokenUsage: CapabilitySupported,
		},
		Sync: ProviderSyncSemantics{
			UnchangedResults:                    UnchangedResultMTimeAndHash,
			FingerprintHashRequiredForFreshness: true,
		},
	}
}

// codefreeSourceExists reports whether the backing session DB for a
// source exists as a regular file.
func codefreeSourceExists(source SourceRef) bool {
	src, ok := source.Opaque.(codefreeSource)
	if !ok {
		if ptr, ok := source.Opaque.(*codefreeSource); ok && ptr != nil {
			src = *ptr
		}
	}
	if src.DBPath == "" {
		return false
	}
	return IsRegularFile(src.DBPath)
}

// codefreeSourceMatchesSession reports whether a source's session ID
// matches the given raw session ID.
func codefreeSourceMatchesSession(source SourceRef, sessionID string) bool {
	src, ok := source.Opaque.(codefreeSource)
	if !ok {
		if ptr, ok := source.Opaque.(*codefreeSource); ok && ptr != nil {
			src = *ptr
		}
	}
	return src.SessionID == sessionID
}
