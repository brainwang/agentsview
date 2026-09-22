package parser

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

var _ Provider = (*teleagentProvider)(nil)

// teleagentProviderFactory builds a TeleAgent provider for the
// provider-factory registry. It mirrors kiroProviderFactory's shape.
type teleagentProviderFactory struct {
	def AgentDef
}

func newTeleAgentProviderFactory(def AgentDef) ProviderFactory {
	return teleagentProviderFactory{def: cloneAgentDef(def)}
}

func (f teleagentProviderFactory) Definition() AgentDef {
	return cloneAgentDef(f.def)
}

func (f teleagentProviderFactory) Capabilities() Capabilities {
	return teleagentProviderCapabilities()
}

func (f teleagentProviderFactory) NewProvider(cfg ProviderConfig) Provider {
	cfg = cfg.Clone()
	return &teleagentProvider{
		Def:     cloneAgentDef(f.def),
		Caps:    teleagentProviderCapabilities(),
		Config:  cfg,
		sources: newTeleAgentSourceSet(cfg.Roots),
	}
}

// teleagentProvider implements the Provider interface for TeleAgent.
// It mirrors kiroProvider's structure: a ProviderBase plus a source set
// that owns discovery, fingerprinting, and parse dispatch over the
// backing per-user SQLite archive.
type teleagentProvider struct {
	ProviderBase
	sources teleagentSourceSet
}

func (p *teleagentProvider) Discover(ctx context.Context) ([]SourceRef, error) {
	return p.sources.Discover(ctx)
}

func (p *teleagentProvider) DiscoverEach(
	ctx context.Context, yield func(SourceRef) error,
) error {
	return p.sources.DiscoverEach(ctx, yield)
}

func (p *teleagentProvider) WatchPlan(ctx context.Context) (WatchPlan, error) {
	return p.sources.WatchPlan(ctx)
}

func (p *teleagentProvider) SourcesForChangedPath(
	ctx context.Context, req ChangedPathRequest,
) ([]SourceRef, error) {
	return p.sources.SourcesForChangedPath(ctx, req)
}

func (p *teleagentProvider) StoredSourceHintScopes(
	req ChangedPathRequest,
) []StoredSourceHintScope {
	return p.sources.StoredSourceHintScopes(req)
}

func (p *teleagentProvider) FindSource(
	ctx context.Context, req FindSourceRequest,
) (SourceRef, bool, error) {
	req = ProviderFindRequestWithRawSessionID(p.Def, req)
	return p.sources.FindSource(ctx, req)
}

func (p *teleagentProvider) Fingerprint(
	ctx context.Context, source SourceRef,
) (SourceFingerprint, error) {
	return p.sources.Fingerprint(ctx, source)
}

// PersistentArchiveSource returns the backing DB path when the stored source
// is a TeleAgent virtual member under a configured root, so a vanished DB
// file preserves its stored sessions (the persistent-archive contract).
func (p *teleagentProvider) PersistentArchiveSource(
	path string, fullSessionID string,
) (string, bool) {
	rawSessionID := ProviderRawSessionIDFromFull(p.Def, fullSessionID)
	for _, root := range p.sources.roots {
		source, ok := p.sources.sourceRef(root, path, true)
		if !ok {
			continue
		}
		src, ok := p.sources.sourceFromRef(source)
		if ok && src.Kind == teleagentSourceSQLiteSession &&
			rawSessionID != "" && src.SessionID == rawSessionID {
			return src.DBPath, true
		}
	}
	return "", false
}

func (p *teleagentProvider) Parse(
	ctx context.Context, req ParseRequest,
) (ParseOutcome, error) {
	if err := ctx.Err(); err != nil {
		return ParseOutcome{}, err
	}
	src, ok := p.sources.sourceFromRef(req.Source)
	if !ok {
		return ParseOutcome{}, fmt.Errorf("teleagent source path unavailable")
	}
	machine := firstNonEmptyJSONLString(req.Machine, p.Config.Machine)
	switch src.Kind {
	case teleagentSourceSQLiteDB:
		return p.parseSQLiteDB(ctx, src, machine)
	case teleagentSourceSQLiteSession:
		return p.parseSQLiteSession(ctx, src, machine)
	default:
		return ParseOutcome{
			ResultSetComplete: true,
			SkipReason:        SkipUnsupportedSource,
		}, nil
	}
}

// parseSQLiteDB fans out every user session in the backing DB. A missing DB
// file preserves stored rows (persistent archive); a present DB with no user
// rows force-replaces stored rows so a vanished-row tombstone takes effect.
func (p *teleagentProvider) parseSQLiteDB(
	ctx context.Context, src teleagentSource, machine string,
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
	store, err := OpenTeleAgentSQLiteStore(src.DBPath)
	if err != nil {
		return ParseOutcome{}, err
	}
	defer store.Close()
	metas, err := store.ListSessionMeta()
	if err != nil {
		return ParseOutcome{}, err
	}
	results := make([]ParseResultOutcome, 0, len(metas))
	var sourceErrs []SourceError
	for _, meta := range metas {
		if src.SessionIDsSet {
			if _, ok := src.SessionIDs[meta.SessionID]; !ok {
				continue
			}
		}
		if err := ctx.Err(); err != nil {
			return ParseOutcome{}, err
		}
		sess, msgs, err := store.LoadSession(ctx, meta.SessionID, machine)
		if err != nil {
			sourceErrs = append(sourceErrs, SourceError{
				SourceKey:   meta.VirtualPath,
				DisplayPath: meta.VirtualPath,
				SessionID:   "teleagent:" + meta.SessionID,
				Err:         err,
				Retryable:   true,
			})
			continue
		}
		if sess == nil {
			continue
		}
		results = append(results, ParseResultOutcome{
			Result: ParseResult{
				Session:  *sess,
				Messages: msgs,
			},
			DataVersion: DataVersionCurrent,
		})
	}
	if len(results) == 0 && len(sourceErrs) == 0 {
		return ParseOutcome{
			ResultSetComplete: true,
			ForceReplace:      !src.SessionIDsSet,
			SkipReason:        SkipNoSession,
		}, nil
	}
	return ParseOutcome{
		Results:           results,
		SourceErrors:      sourceErrs,
		ResultSetComplete: true,
		ForceReplace:      !src.SessionIDsSet,
	}, nil
}

// parseSQLiteSession parses one virtual member. A missing DB file preserves
// stored rows (persistent archive: no ForceReplace); a missing session row in
// a present DB force-replaces (the row was genuinely removed); a successful
// parse returns DataVersionCurrent.
func (p *teleagentProvider) parseSQLiteSession(
	ctx context.Context, src teleagentSource, machine string,
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
	store, err := OpenTeleAgentSQLiteStore(src.DBPath)
	if err != nil {
		return ParseOutcome{}, err
	}
	defer store.Close()
	sess, msgs, err := store.LoadSession(ctx, src.SessionID, machine)
	if errors.Is(err, sql.ErrNoRows) {
		return ParseOutcome{
			ResultSetComplete: true,
			ForceReplace:      true,
			SkipReason:        SkipNoSession,
		}, nil
	}
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

// teleagentSourceKind distinguishes the container (whole DB) from a single
// virtual session source.
type teleagentSourceKind uint8

const (
	teleagentSourceSQLiteDB teleagentSourceKind = iota + 1
	teleagentSourceSQLiteSession
)

// teleagentSource mirrors kiroSource. Path is the SourceRef.Key /
// DisplayPath / FingerprintKey (the DB path for the container kind, the
// virtual path for the session kind). DBPath is the resolved backing DB.
// SessionIDs carries a selective member allowlist set by changed-path
// classification; SessionIDsSet marks it as populated; PreservedIDs lists
// session IDs whose winners were non-SQLite (always empty for TeleAgent,
// which only has the SQLite store, but kept for structural parity).
type teleagentSource struct {
	Root            string
	Path            string
	DBPath          string
	SessionID       string
	Kind            teleagentSourceKind
	SessionIDs      map[string]struct{}
	SessionIDsSet   bool
	SessionIDsTotal int
	PreservedIDs    []string
}

// teleagentSourceSet mirrors kiroSourceSet's structure: it owns the
// configured roots and the discovery / FindSource / Fingerprint /
// WatchPlan / SourcesForChangedPath / StoredSourceHintScopes surface for
// the TeleAgent SQLite container.
type teleagentSourceSet struct {
	roots   []string
	readDir func(string) ([]os.DirEntry, error)
}

func newTeleAgentSourceSet(roots []string) teleagentSourceSet {
	return teleagentSourceSet{
		roots:   cleanJSONLRoots(roots),
		readDir: os.ReadDir,
	}
}

// Discover enumerates all user sessions across all configured roots: one
// container source per backing DB plus one virtual member per user session.
// _SYS_ system-maintenance sessions are filtered at the SQL level.
func (s teleagentSourceSet) Discover(ctx context.Context) ([]SourceRef, error) {
	sources := make([]SourceRef, 0)
	seen := make(map[string]struct{})
	for _, root := range s.roots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dbPath, err := teleagentSQLiteDBPathChecked(root)
		if err != nil {
			return nil, err
		}
		if dbPath == "" {
			continue
		}
		containerSource, memberSources, err := s.discoverRootDB(root, dbPath)
		if err != nil {
			return nil, err
		}
		addJSONLSource(containerSource, &sources, seen)
		for _, member := range memberSources {
			addJSONLSource(member, &sources, seen)
		}
	}
	sortJSONLSources(sources)
	return sources, nil
}

// DiscoverEach streams the same set as Discover but yields each source as it
// is emitted, so streaming discovery never holds an archive-sized Go slice.
// Per Kiro's pattern, the container source is not emitted on the streaming
// path; only the per-session virtual members are streamed.
func (s teleagentSourceSet) DiscoverEach(
	ctx context.Context, yield func(SourceRef) error,
) error {
	for _, root := range s.roots {
		if err := ctx.Err(); err != nil {
			return err
		}
		dbPath, err := teleagentSQLiteDBPathChecked(root)
		if err != nil {
			return err
		}
		if dbPath == "" {
			continue
		}
		store, err := OpenTeleAgentSQLiteStore(dbPath)
		if err != nil {
			return err
		}
		err = store.ForEachSessionMeta(ctx, func(meta TeleAgentSQLiteSessionMeta) error {
			source := s.newMemberSourceRef(
				root, meta.VirtualPath, dbPath, meta.SessionID,
			)
			source.DiscoveryMTimeNS = meta.FileMtime
			return yield(source)
		})
		closeErr := store.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

// discoverRootDB enumerates one backing DB under one root: it lists every
// user session meta, builds the container SourceRef with the full member
// allowlist, and builds one virtual member SourceRef per session. Used by the
// non-streaming Discover path; DiscoverEach streams members directly.
func (s teleagentSourceSet) discoverRootDB(
	root, dbPath string,
) (SourceRef, []SourceRef, error) {
	metas, err := ListTeleAgentSQLiteSessionMeta(dbPath)
	if err != nil {
		return SourceRef{}, nil, fmt.Errorf(
			"list TeleAgent SQLite sessions in %s: %w", dbPath, err,
		)
	}
	container := s.newContainerSourceRef(root, dbPath)
	containerSrc, ok := s.sourceFromRef(container)
	if !ok {
		return SourceRef{}, nil, fmt.Errorf(
			"teleagent SQLite container source unavailable: %s", dbPath,
		)
	}
	containerSrc.SessionIDsTotal = len(metas)
	containerSrc.SessionIDs = make(map[string]struct{}, len(metas))
	members := make([]SourceRef, 0, len(metas))
	for _, meta := range metas {
		containerSrc.SessionIDs[meta.SessionID] = struct{}{}
		containerSrc.PreservedIDs = append(
			containerSrc.PreservedIDs, "teleagent:"+meta.SessionID,
		)
		member := s.newMemberSourceRef(
			root, meta.VirtualPath, dbPath, meta.SessionID,
		)
		member.DiscoveryMTimeNS = meta.FileMtime
		members = append(members, member)
	}
	sort.Strings(containerSrc.PreservedIDs)
	// TeleAgent has only one physical kind (SQLite), so every member passes
	// the container's membership filter; SessionIDsSet stays false because
	// the winner set equals the full set, mirroring Kiro's behavior when
	// SQLite is the only source for every member.
	if containerSrc.SessionIDsTotal > 0 &&
		len(containerSrc.SessionIDs) < containerSrc.SessionIDsTotal {
		containerSrc.SessionIDsSet = true
	} else {
		containerSrc.SessionIDs = nil
		containerSrc.SessionIDsSet = false
	}
	container.Opaque = containerSrc
	return container, members, nil
}

// WatchPlan declares a non-recursive watch on the resolved DB path plus a
// non-recursive watch on the users/ parent directory so the appearance of a
// new user subdirectory is detected.
func (s teleagentSourceSet) WatchPlan(context.Context) (WatchPlan, error) {
	roots := make([]WatchRoot, 0, len(s.roots)*2)
	for _, root := range s.roots {
		// Watch the configured root (the users/ parent) non-recursively so a
		// new user subdirectory triggers a re-scan. No IncludeGlobs: a new
		// subdirectory creation event must not be filtered out by a file-name
		// glob.
		roots = append(roots, WatchRoot{
			Path:        root,
			Recursive:   false,
			DebounceKey: string(AgentTeleAgent) + ":root:" + root,
		})
		// If auto-discovery resolves a backing DB under the root, watch the
		// DB file directly so a TeleAgent write triggers a re-sync even when
		// the users/ parent's directory mtime does not change. IncludeGlobs
		// covers the DB file plus its WAL/SHM siblings.
		if dbPath := teleagentSQLiteDBPath(root); dbPath != "" {
			roots = append(roots, WatchRoot{
				Path:         dbPath,
				Recursive:    false,
				IncludeGlobs: []string{teleagentSQLiteDBName, teleagentSQLiteDBName + "-*"},
				DebounceKey:  string(AgentTeleAgent) + ":db:" + dbPath,
			})
		}
	}
	return WatchPlan{Roots: roots}, nil
}

// SourcesForChangedPath classifies one changed path against the configured
// roots: a path naming the DB (or its WAL/SHM sibling) yields the container
// source plus tombstones for vanished members; a path naming a virtual
// member yields that single member source.
func (s teleagentSourceSet) SourcesForChangedPath(
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
		src, _ := s.sourceFromRef(source)
		sources := make([]SourceRef, 0, 2)
		if src.Kind == teleagentSourceSQLiteDB {
			sources = append(sources, source)
			tombstones, err := s.changedPathTombstones(root, source, req.StoredSourcePaths)
			if err != nil {
				return nil, err
			}
			sources = append(sources, tombstones...)
			return sources, nil
		}
		sources = append(sources, source)
		return sources, nil
	}
	return nil, nil
}

// StoredSourceHintScopes exposes the DB container path with
// IncludeVirtualMembers=true so the engine's stored-freshness page reads
// only that container's stored member rows when a DB-level event fires.
func (s teleagentSourceSet) StoredSourceHintScopes(
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
		switch src.Kind {
		case teleagentSourceSQLiteDB:
			return []StoredSourceHintScope{{
				Path: src.DBPath, IncludeVirtualMembers: true,
			}}
		case teleagentSourceSQLiteSession:
			return []StoredSourceHintScope{{Path: src.Path}}
		}
	}
	return nil
}

// FindSource resolves a stored path, fingerprint key, or raw session ID to a
// current source. Stored paths and fingerprint keys are tried first when the
// caller has them; raw session IDs hit the DB directly via
// TeleAgentSQLiteSessionExistsWithError.
func (s teleagentSourceSet) FindSource(
	ctx context.Context, req FindSourceRequest,
) (SourceRef, bool, error) {
	if err := ctx.Err(); err != nil {
		return SourceRef{}, false, err
	}
	var hinted []SourceRef
	for _, path := range []string{req.StoredFilePath, req.FingerprintKey} {
		if path == "" {
			continue
		}
		for _, root := range s.roots {
			if source, ok := s.sourceRef(root, path, true); ok {
				if req.RequireFreshSource && !teleagentSourceExists(source) {
					continue
				}
				if req.RawSessionID == "" {
					return source, true, nil
				}
				if sourceMatchesTeleAgentSession(source, req.RawSessionID) {
					if req.PreferStoredSource {
						return source, true, nil
					}
					hinted = append(hinted, source)
				}
			}
		}
	}
	if req.RawSessionID == "" {
		return SourceRef{}, false, nil
	}
	var candidates []SourceRef
	candidates = append(candidates, hinted...)
	for _, root := range s.roots {
		dbPath, err := teleagentSQLiteDBPathChecked(root)
		if err != nil {
			return SourceRef{}, false, err
		}
		if dbPath == "" {
			continue
		}
		exists, err := TeleAgentSQLiteSessionExistsWithError(dbPath, req.RawSessionID)
		if err != nil {
			return SourceRef{}, false, fmt.Errorf(
				"find TeleAgent SQLite session %s: %w", req.RawSessionID, err,
			)
		}
		if exists {
			candidates = append(candidates, s.newMemberSourceRef(
				root, TeleAgentSQLiteVirtualPath(dbPath, req.RawSessionID),
				dbPath, req.RawSessionID,
			))
		}
	}
	if source, ok := s.bestSource(candidates); ok {
		return source, true, nil
	}
	return SourceRef{}, false, nil
}

// Fingerprint returns the source freshness identity for one source. For a
// virtual session: MTimeNS is session.time_updated in ns, Size is the sum of
// length(part.data) for the session. For the container: MTimeNS is the
// composite DB+WAL mtime via sqliteDBCompositeMtime. A vanished DB file
// preserves stored rows: a Key-only fingerprint (zero Size and MTimeNS) is
// returned so the parse path skips without ForceReplace.
func (s teleagentSourceSet) Fingerprint(
	ctx context.Context, source SourceRef,
) (SourceFingerprint, error) {
	if err := ctx.Err(); err != nil {
		return SourceFingerprint{}, err
	}
	src, ok := s.sourceFromRef(source)
	if !ok {
		return SourceFingerprint{}, fmt.Errorf("teleagent source path unavailable")
	}
	key := firstNonEmptyJSONLString(source.FingerprintKey, source.Key, src.Path)
	if src.Kind == teleagentSourceSQLiteSession {
		if _, err := os.Stat(src.DBPath); err != nil {
			if os.IsNotExist(err) {
				return SourceFingerprint{Key: key}, nil
			}
			return SourceFingerprint{}, fmt.Errorf("stat %s: %w", src.DBPath, err)
		}
		store, err := OpenTeleAgentSQLiteStore(src.DBPath)
		if err != nil {
			return SourceFingerprint{}, err
		}
		defer store.Close()
		mtimeNS, partSize, err := teleagentSQLiteSessionFingerprint(
			store.db, src.SessionID,
		)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return SourceFingerprint{Key: key}, nil
			}
			return SourceFingerprint{}, err
		}
		return SourceFingerprint{
			Key:     key,
			Size:    partSize,
			MTimeNS: mtimeNS,
		}, nil
	}
	// Container fingerprint.
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

// newContainerSourceRef builds the container SourceRef for a backing DB.
func (s teleagentSourceSet) newContainerSourceRef(root, dbPath string) SourceRef {
	return SourceRef{
		Provider:       AgentTeleAgent,
		ConfiguredRoot: root,
		Key:            dbPath,
		DisplayPath:    dbPath,
		FingerprintKey: dbPath,
		Opaque: teleagentSource{
			Root:   root,
			Path:   dbPath,
			DBPath: dbPath,
			Kind:   teleagentSourceSQLiteDB,
		},
	}
}

// newMemberSourceRef builds one virtual member SourceRef for a session.
func (s teleagentSourceSet) newMemberSourceRef(
	root, virtualPath, dbPath, sessionID string,
) SourceRef {
	return SourceRef{
		Provider:       AgentTeleAgent,
		ConfiguredRoot: root,
		Key:            sessionID,
		DisplayPath:    virtualPath,
		FingerprintKey: virtualPath,
		Opaque: teleagentSource{
			Root:      root,
			Path:      virtualPath,
			DBPath:    dbPath,
			SessionID: sessionID,
			Kind:      teleagentSourceSQLiteSession,
		},
	}
}

// sourceRef resolves a stored path to a SourceRef. allowMissing relaxes the
// regular-file requirement so a deleted container (or its WAL/SHM sibling)
// still classifies for changed-path tombstones.
func (s teleagentSourceSet) sourceRef(
	root, path string, allowMissing bool,
) (SourceRef, bool) {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if dbPath, sessionID, ok := teleagentSQLiteVirtualPathParts(path); ok {
		if !teleagentDBUnderRoot(root, dbPath, !allowMissing) {
			return SourceRef{}, false
		}
		return s.newMemberSourceRef(root, path, dbPath, sessionID), true
	}
	if teleagentDBUnderRoot(root, path, !allowMissing) {
		return s.newContainerSourceRef(root, path), true
	}
	return SourceRef{}, false
}

// sourceRefForChangedPath classifies a changed path, accepting a missing DB
// file so a deletion (or a WAL/SHM sibling event) still resolves through
// sqliteContainerPathForEvent.
func (s teleagentSourceSet) sourceRefForChangedPath(
	root, path string,
) (SourceRef, bool) {
	if source, ok := s.sourceRef(root, path, false); ok {
		return source, true
	}
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if dbPath, sessionID, ok := teleagentSQLiteVirtualPathParts(path); ok {
		if !teleagentDBUnderRoot(root, dbPath, false) {
			return SourceRef{}, false
		}
		return s.newMemberSourceRef(root, path, dbPath, sessionID), true
	}
	if dbPath, ok := sqliteContainerPathForEvent(
		root, path, teleagentSQLiteDBName, true,
	); ok {
		if !teleagentDBUnderRoot(root, dbPath, false) {
			return SourceRef{}, false
		}
		return s.newContainerSourceRef(root, dbPath), true
	}
	return SourceRef{}, false
}

// sourceFromRef rebuilds a teleagentSource from a SourceRef's Opaque payload,
// or by re-classifying its display path / fingerprint key against the
// configured roots when Opaque is absent (e.g. a stored-only row).
func (s teleagentSourceSet) sourceFromRef(source SourceRef) (teleagentSource, bool) {
	switch src := source.Opaque.(type) {
	case teleagentSource:
		return src, src.Path != ""
	case *teleagentSource:
		if src != nil && src.Path != "" {
			return *src, true
		}
	}
	for _, candidate := range []string{source.DisplayPath, source.FingerprintKey, source.Key} {
		for _, root := range s.roots {
			if ref, ok := s.sourceRef(root, candidate, true); ok {
				if src, ok := ref.Opaque.(teleagentSource); ok {
					return src, true
				}
			}
		}
	}
	return teleagentSource{}, false
}

// changedPathTombstones emits one virtual member source for every stored
// TeleAgent SQLite member whose row is gone from a still-present database,
// matching Kiro's changedPathTombstones pattern. A vanished database file
// yields no tombstones, preserving the stored sessions.
func (s teleagentSourceSet) changedPathTombstones(
	root string, container SourceRef, storedPaths []string,
) ([]SourceRef, error) {
	src, ok := s.sourceFromRef(container)
	if !ok || src.Kind != teleagentSourceSQLiteDB || !IsRegularFile(src.DBPath) {
		return nil, nil
	}
	var tombstones []SourceRef
	seen := make(map[string]struct{})
	for _, stored := range storedPaths {
		ref, ok := s.sourceRef(root, stored, true)
		if !ok {
			continue
		}
		member, ok := ref.Opaque.(teleagentSource)
		if !ok || member.Kind != teleagentSourceSQLiteSession {
			continue
		}
		if !samePath(member.DBPath, src.DBPath) {
			continue
		}
		exists, err := TeleAgentSQLiteSessionExistsWithError(
			member.DBPath, member.SessionID,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"check TeleAgent SQLite session %s: %w", member.SessionID, err,
			)
		}
		if exists {
			continue
		}
		if _, dup := seen[member.Path]; dup {
			continue
		}
		seen[member.Path] = struct{}{}
		tombstones = append(tombstones, ref)
	}
	return tombstones, nil
}

// bestSource returns the highest-priority source from a list. With only one
// kind (SQLite), the first candidate wins; ties on DiscoveryMTimeNS fall back
// to path order so the result is deterministic.
func (s teleagentSourceSet) bestSource(sources []SourceRef) (SourceRef, bool) {
	if len(sources) == 0 {
		return SourceRef{}, false
	}
	best := sources[0]
	for _, source := range sources[1:] {
		if source.DiscoveryMTimeNS > best.DiscoveryMTimeNS ||
			(source.DiscoveryMTimeNS == best.DiscoveryMTimeNS &&
				source.Key < best.Key) {
			best = source
		}
	}
	return best, true
}

func teleagentSourceExists(source SourceRef) bool {
	src, ok := source.Opaque.(teleagentSource)
	if !ok {
		if ptr, ok := source.Opaque.(*teleagentSource); ok && ptr != nil {
			src = *ptr
		}
	}
	switch src.Kind {
	case teleagentSourceSQLiteSession:
		return TeleAgentSQLiteSessionExists(src.DBPath, src.SessionID)
	case teleagentSourceSQLiteDB:
		return IsRegularFile(src.DBPath)
	default:
		return IsRegularFile(src.Path)
	}
}

func sourceMatchesTeleAgentSession(source SourceRef, rawID string) bool {
	src, ok := source.Opaque.(teleagentSource)
	if !ok {
		if ptr, ok := source.Opaque.(*teleagentSource); ok && ptr != nil {
			src = *ptr
		}
	}
	if !ok {
		return false
	}
	return src.SessionID == rawID
}

// teleagentProviderCapabilities declares the provider's source, content, and
// sync capabilities. Mirrors Kiro: StreamingDiscovery, StoredSourceHints,
// MultiSessionSource, PerSessionErrors, ForceReplaceOnParse, and
// PersistentArchive are all CapabilitySupported; the sync semantics keep
// UnchangedResults at MTimeAndHash with FingerprintHashRequiredForFreshness
// true. WatchRoots is intentionally left unsupported — the provider
// implements WatchPlan (like Kiro), not the newer WatchRoots planner.
func teleagentProviderCapabilities() Capabilities {
	source := jsonlFileProviderSourceCapabilities()
	source.StreamingDiscovery = CapabilitySupported
	source.StoredSourceHints = CapabilitySupported
	source.MultiSessionSource = CapabilitySupported
	source.PerSessionErrors = CapabilitySupported
	source.ForceReplaceOnParse = CapabilitySupported
	source.PersistentArchive = CapabilitySupported
	return Capabilities{
		Source: source,
		Content: ContentCapabilities{
			FirstMessage:         CapabilitySupported,
			SessionName:          CapabilitySupported,
			Cwd:                  CapabilitySupported,
			Relationships:        CapabilitySupported,
			Subagents:            CapabilitySupported,
			Thinking:             CapabilitySupported,
			ToolCalls:            CapabilitySupported,
			ToolResults:          CapabilitySupported,
			ToolResultEvents:     CapabilitySupported,
			PerMessageTokenUsage: CapabilitySupported,
		},
		Sync: ProviderSyncSemantics{
			UnchangedResults:                    UnchangedResultMTimeAndHash,
			FingerprintHashRequiredForFreshness: true,
		},
	}
}
