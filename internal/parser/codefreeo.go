package parser

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
)

// CodefreeoSourceMode identifies the usable codefree-o storage
// backend found under a CODEFREE_O_DIR root.
type CodefreeoSourceMode string

const (
	CodefreeoSourceNone    CodefreeoSourceMode = ""
	CodefreeoSourceStorage CodefreeoSourceMode = "storage"
	CodefreeoSourceSQLite  CodefreeoSourceMode = "sqlite"
)

// CodefreeoSource describes the resolved storage backend for a
// codefree-o root.
type CodefreeoSource struct {
	Mode        CodefreeoSourceMode
	Root        string
	SessionRoot string
	DBPath      string
}

// ResolveCodefreeOSource detects whether a codefree-o root is using
// file-backed storage or SQLite storage.
func ResolveCodefreeOSource(root string) CodefreeoSource {
	if root == "" {
		return CodefreeoSource{}
	}

	localShare := filepath.Join(root, ".local", "share")
	sessionRoot := filepath.Join(localShare, "storage", "session")
	if info, err := os.Stat(sessionRoot); err == nil && info.IsDir() {
		return CodefreeoSource{
			Mode:        CodefreeoSourceStorage,
			Root:        root,
			SessionRoot: sessionRoot,
			DBPath:      filepath.Join(localShare, "codefree.db"),
		}
	} else if err != nil && !os.IsNotExist(err) {
		storageRoot := filepath.Join(localShare, "storage")
		if info, serr := os.Stat(storageRoot); serr == nil && info.IsDir() {
			return CodefreeoSource{
				Mode:        CodefreeoSourceStorage,
				Root:        root,
				SessionRoot: sessionRoot,
				DBPath:      filepath.Join(localShare, "codefree.db"),
			}
		}
	}

	dbPath := filepath.Join(localShare, "codefree.db")
	if info, err := os.Stat(dbPath); err == nil && !info.IsDir() {
		return CodefreeoSource{
			Mode:   CodefreeoSourceSQLite,
			Root:   root,
			DBPath: dbPath,
		}
	}

	return CodefreeoSource{Root: root}
}

// DiscoverCodefreeOSessions finds all file-backed codefree-o session
// JSON files under .local/share/storage/session.
func DiscoverCodefreeOSessions(root string) []DiscoveredFile {
	src := ResolveCodefreeOSource(root)
	if src.Mode != CodefreeoSourceStorage {
		return nil
	}

	var files []DiscoveredFile
	entries, err := os.ReadDir(src.SessionRoot)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if !isDirOrSymlink(entry, src.SessionRoot) {
			continue
		}
		projectDir := filepath.Join(src.SessionRoot, entry.Name())
		sessionEntries, err := os.ReadDir(projectDir)
		if err != nil {
			continue
		}
		for _, sessionEntry := range sessionEntries {
			if sessionEntry.IsDir() ||
				!strings.HasSuffix(sessionEntry.Name(), ".json") {
				continue
			}
			path := filepath.Join(projectDir, sessionEntry.Name())
			files = append(files, DiscoveredFile{
				Path:    path,
				Project: codefreeoSessionProject(path),
				Agent:   AgentCodefreeO,
			})
		}
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
	return files
}

// FindCodefreeOSourceFile locates a single codefree-o session source
// path or SQLite backing file by raw session ID.
func FindCodefreeOSourceFile(root, sessionID string) string {
	if !IsValidSessionID(sessionID) {
		return ""
	}

	src := ResolveCodefreeOSource(root)
	switch src.Mode {
	case CodefreeoSourceStorage:
		if entries, err := os.ReadDir(src.SessionRoot); err == nil {
			for _, entry := range entries {
				if !isDirOrSymlink(entry, src.SessionRoot) {
					continue
				}
				path := filepath.Join(
					src.SessionRoot, entry.Name(),
					sessionID+".json",
				)
				if info, err := os.Stat(path); err == nil &&
					!info.IsDir() {
					return path
				}
			}
		}
		if OpenCodeSQLiteSessionExists(src.DBPath, sessionID) {
			return CodefreeoSQLiteVirtualPath(
				src.DBPath, sessionID,
			)
		}
		return ""
	case CodefreeoSourceSQLite:
		if OpenCodeSQLiteSessionExists(src.DBPath, sessionID) {
			return CodefreeoSQLiteVirtualPath(
				src.DBPath, sessionID,
			)
		}
		return ""
	default:
		return ""
	}
}

// ResolveCodefreeOWatchRoots returns the directories that should be
// watched for live codefree-o updates under a configured root.
func ResolveCodefreeOWatchRoots(root string) []string {
	if root == "" {
		return nil
	}
	src := ResolveCodefreeOSource(root)
	switch src.Mode {
	case CodefreeoSourceStorage:
		if info, err := os.Stat(src.DBPath); err == nil &&
			!info.IsDir() {
			return []string{root}
		}
		return []string{filepath.Join(root, ".local", "share", "storage")}
	case CodefreeoSourceSQLite:
		return []string{root}
	}
	if info, err := os.Stat(root); err == nil && info.IsDir() {
		return []string{root}
	}
	return nil
}

// CodefreeoSQLiteVirtualPath builds a virtual path for a codefree-o
// session stored in the SQLite database.
func CodefreeoSQLiteVirtualPath(
	dbPath, sessionID string,
) string {
	return dbPath + "#" + sessionID
}

// ParseCodefreeoSQLiteVirtualPath extracts the database path and
// session ID from a codefree-o virtual path (codefree.db#<sessionID>).
func ParseCodefreeoSQLiteVirtualPath(
	sourcePath string,
) (dbPath, sessionID string, ok bool) {
	idx := strings.LastIndex(sourcePath, "#")
	if idx <= 0 || idx >= len(sourcePath)-1 {
		return "", "", false
	}
	dbPath = sourcePath[:idx]
	sessionID = sourcePath[idx+1:]
	if filepath.Base(dbPath) != "codefree.db" {
		return "", "", false
	}
	return dbPath, sessionID, true
}

// CodefreeoSourceMtime returns the mtime for a codefree-o source
// path (either a JSON file or a SQLite virtual path).
func CodefreeoSourceMtime(sourcePath string) (int64, error) {
	if sourcePath == "" {
		return 0, nil
	}
	if dbPath, sessionID, ok := ParseCodefreeoSQLiteVirtualPath(sourcePath); ok {
		return openCodeSQLiteSessionMtime(dbPath, sessionID)
	}
	return codefreeoStorageSessionMtime(sourcePath)
}

// CodefreeoStorageSessionIDs returns the set of session IDs with
// JSON files under .local/share/storage/session/*/ in the given root.
func CodefreeoStorageSessionIDs(root string) map[string]struct{} {
	src := ResolveCodefreeOSource(root)
	if src.Mode != CodefreeoSourceStorage {
		return nil
	}
	entries, err := os.ReadDir(src.SessionRoot)
	if err != nil {
		return nil
	}
	ids := make(map[string]struct{})
	for _, entry := range entries {
		if !isDirOrSymlink(entry, src.SessionRoot) {
			continue
		}
		projectDir := filepath.Join(src.SessionRoot, entry.Name())
		sessionEntries, err := os.ReadDir(projectDir)
		if err != nil {
			continue
		}
		for _, sessionEntry := range sessionEntries {
			name := sessionEntry.Name()
			if sessionEntry.IsDir() ||
				!strings.HasSuffix(name, ".json") {
				continue
			}
			id := strings.TrimSuffix(name, ".json")
			if id == "" {
				continue
			}
			ids[id] = struct{}{}
		}
	}
	return ids
}

func codefreeoSessionProject(path string) string {
	data, err := os.ReadFile(path)
	if err == nil {
		if cwd := gjson.GetBytes(data, "directory").Str; cwd != "" {
			if project := ExtractProjectFromCwd(cwd); project != "" {
				return project
			}
		}
	}

	if project := NormalizeName(filepath.Base(filepath.Dir(path))); project != "" {
		return project
	}
	return "unknown"
}

func codefreeoStorageSessionMtime(sessionPath string) (int64, error) {
	info, err := os.Stat(sessionPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	root := filepath.Dir(filepath.Dir(filepath.Dir(
		filepath.Dir(filepath.Dir(sessionPath)),
	)))
	sessionID := strings.TrimSuffix(
		filepath.Base(sessionPath), filepath.Ext(sessionPath),
	)
	fileMtime := info.ModTime().UnixNano()

	messageDir := filepath.Join(root, ".local", "share", "storage", "message", sessionID)
	fileMtime = max(fileMtime, statMtime(messageDir))
	msgEntries, err := os.ReadDir(messageDir)
	if err != nil {
		if os.IsNotExist(err) {
			return fileMtime, nil
		}
		return 0, err
	}
	for _, entry := range msgEntries {
		if entry.IsDir() ||
			!strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		fileMtime = max(fileMtime, mustEntryMtime(entry))
		messageID := strings.TrimSuffix(
			entry.Name(), filepath.Ext(entry.Name()),
		)
		partDir := filepath.Join(root, ".local", "share", "storage", "part", messageID)
		fileMtime = max(fileMtime, statMtime(partDir))
		partEntries, err := os.ReadDir(partDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, err
		}
		for _, partEntry := range partEntries {
			if partEntry.IsDir() ||
				!strings.HasSuffix(partEntry.Name(), ".json") {
				continue
			}
			fileMtime = max(fileMtime, mustEntryMtime(partEntry))
		}
	}

	return fileMtime, nil
}
