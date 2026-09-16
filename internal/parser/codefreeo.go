package parser

import "strings"

// Codefree-O uses OpenCode's storage format, but is exposed as a
// distinct agent with the codefree-o: ID prefix. The OpenCode-format
// provider owns parsing and relabels results through
// relabelOpenCodeSessionAsCodefreeO.

func ListCodefreeOSessionMeta(dbPath string) ([]OpenCodeSessionMeta, error) {
	metas, err := ListOpenCodeSessionMeta(dbPath)
	if err != nil {
		return nil, err
	}
	for i := range metas {
		metas[i].VirtualPath = CodefreeOSQLiteVirtualPath(
			dbPath, metas[i].SessionID,
		)
	}
	return metas, nil
}

func CodefreeOSourceMtime(sourcePath string) (int64, error) {
	if sourcePath == "" {
		return 0, nil
	}
	if dbPath, sessionID, ok := parseOpenCodeFormatVirtualPath(
		codefreeOFmt.dbName, sourcePath,
	); ok {
		return openCodeSQLiteSessionMtime(dbPath, sessionID)
	}
	return openCodeStorageSessionMtime(sourcePath)
}

func relabelOpenCodeSessionAsCodefreeO(sess *ParsedSession) {
	sess.ID = strings.Replace(sess.ID, "opencode:", "codefree-o:", 1)
	if sess.ParentSessionID != "" {
		sess.ParentSessionID = strings.Replace(
			sess.ParentSessionID, "opencode:", "codefree-o:", 1,
		)
	}
	if sess.SourceSessionID != "" {
		sess.SourceSessionID = strings.Replace(
			sess.SourceSessionID, "opencode:", "codefree-o:", 1,
		)
	}
	sess.Agent = AgentCodefreeO
}
