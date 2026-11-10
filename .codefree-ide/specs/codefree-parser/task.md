# 任务拆分：CodeFree 会话读取功能

## Task1: Provider 注册与基础框架

### 任务目标

在 agentsview 的 parser 注册体系中添加 `AgentCodeFree` 类型，创建 provider 基础框架文件，使项目能识别 CodeFree agent 并通过编译。

### 子任务

1. 在 `internal/parser/types.go` 的 `AgentType` 常量列表中添加 `AgentCodeFree AgentType = "codefree"`
2. 在 `internal/parser/types.go` 的 `Registry` 切片末尾添加 CodeFree 的 `AgentDef` 条目（参照 TeleAgent 条目结构）
3. 在 `internal/parser/provider.go` 的 `ProviderFactories()` switch 语句中添加 `case AgentCodeFree: return newCodeFreeProviderFactory(def)`
4. 在 `internal/parser/provider_migration.go` 的 `providerMigrationModes` map 中添加 `AgentCodeFree: ProviderMigrationProviderAuthoritative`
5. 创建 `internal/parser/codefree_provider.go`，实现 `codefreeProviderFactory`、`codefreeProvider`、`codefreeSourceSet`、`codefreeSource`、`codefreeSourceKind` 类型和 `codefreeProviderCapabilities()` 函数，以及 `newCodeFreeProviderFactory()` 构造函数。Provider 的各方法先返回空值或 `ErrUnsupportedProviderFeature`，确保编译通过
6. 创建 `internal/parser/codefree_sqlite.go`，定义 `CodeFreeSQLiteStore`、`CodeFreeCatalogEntry` 结构体和常量（`codefreeSQLiteDBName`、`codefreeCatalogDir`、`codefreeCatalogKey`、`codefreeStateKey`），实现 `OpenCodeFreeSQLiteStore()` 和 `Close()`
7. 创建 `internal/parser/codefree.go`，定义 `loadCodeFreeSession()` 函数签名（先返回 nil, nil, nil 占位）
8. 运行 `go build ./...` 确认编译通过
9. 运行 `go vet ./...` 确认无新警告

### 依赖关系

无（首个任务）

### 验收标准

- `go build ./...` 编译通过，无错误
- `go vet ./...` 无新警告
- `go fmt ./...` 格式正确
- `AgentCodeFree` 常量、Registry 条目、factory switch case、migration mode 均已添加
- 三个新文件已创建且编译通过

### 预估复杂度

中

---

## Task2: SQLite Store 与 Catalog 读取

### 任务目标

实现 CodeFree 的 SQLite 只读访问层和 catalog 索引读取逻辑，为 Discover 和 Parse 提供数据读取基础。

### 子任务

1. 在 `codefree_sqlite.go` 中实现 `openCodeFreeSQLiteDB()` — 使用 `openSQLiteReadOnly(path, sqliteReadOptions{busyTimeoutMS: 3000})` 打开数据库
2. 实现 `OpenCodeFreeSQLiteStore()` — 拒绝符号链接，调用 `openCodeFreeSQLiteDB()`，返回 store
3. 实现 `(s *CodeFreeSQLiteStore) LoadStateJSON() (string, error)` — 执行 `SELECT value FROM session_metadata WHERE key='codefree.state.v1'`，返回 JSON 字符串
4. 实现 `ListCodeFreeCatalog(root string) ([]CodeFreeCatalogEntry, error)`：
   - 打开 `<root>/__catalog__/session.db`
   - 读取 `codefree.catalog.v1` 键值
   - 用 gjson 解析 JSON，提取 `sessions` 数组
   - 对每个条目提取 sessionId、startTime、modifiedTime、summary、workingDirectory、defaultAgentUri
   - 返回 `[]CodeFreeCatalogEntry`
5. 实现 `CodeFreeSessionExists(root, sessionID string) bool` — 检查 `<root>/<sessionID>/session.db` 是否为常规文件
6. 实现 `CodeFreeVirtualPath(dbPath, sessionID string) string` — 调用 `VirtualSourcePath()`
7. 实现 `codefreeSQLiteVirtualPathParts(path string) (string, string, bool)` — 调用 `ParseVirtualSourcePathForBase()`
8. 实现 `codefreeSQLiteDBPathsChecked(root string) ([]string, error)` — 列出 root 下的 catalog 条目对应的 session.db 路径
9. 编写单元测试 `codefree_sqlite_test.go`：
   - 测试 catalog 读取（构造模拟 session.db）
   - 测试 session 存在性检查
   - 测试 LoadStateJSON
10. 运行 `go test ./internal/parser/... -run CodeFree` 确认测试通过

### 依赖关系

Task1

### 验收标准

- `ListCodeFreeCatalog` 能正确解析 catalog JSON 并返回会话列表
- `LoadStateJSON` 能正确读取 `codefree.state.v1` 键值
- `CodeFreeSessionExists` 正确判断 session.db 是否存在
- 单元测试覆盖核心函数
- `go test ./internal/parser/... -run CodeFree` 通过

### 预估复杂度

中

---

## Task3: JSON 解析与消息构建

### 任务目标

实现 CodeFree `codefree.state.v1` JSON 的完整解析逻辑，将 turn-based 会话数据映射为 agentsview 的 `ParsedSession` 和 `ParsedMessage` 模型。

### 子任务

1. 在 `codefree.go` 中实现 `codefreeDecodeWorkingDir(uri string) string`：
   - 去掉 `file:///` 前缀
   - URL 解码（`url.QueryUnescape` 或手动解码 `%3A` → `:` 等）
   - 处理 Windows 路径分隔符（`/` → `\`）
2. 实现 `codefreeAgentLabel(uri string) string`：
   - 从 `codefree-agent://builtin/spec` 提取 `spec`
   - 从 `codefree-agent://builtin/explore` 提取 `explore`
3. 实现 `loadCodeFreeSession(ctx, db, dbPath, sessionID, machine)`：
   - 从 `session_metadata` 读取 `codefree.state.v1`
   - 用 gjson 解析 JSON
   - 构建 `ParsedSession`：
     - `ID` = `"codefree:" + sessionId`
     - `SourceSessionID` = sessionId
     - `SessionName` = session.summary
     - `Cwd` = codefreeDecodeWorkingDir(session.workingDirectory)
     - `StartedAt` = `time.UnixMilli(session.startTime)`
     - `EndedAt` = `time.UnixMilli(session.modifiedTime)`
     - `Agent` = AgentCodeFree
     - `AgentLabel` = codefreeAgentLabel(chats[0].agent.uri)
     - `Machine` = machine
   - 遍历 `completedTurns` 构建消息序列
   - 遍历 `inFlightTurns` 追加未完成轮次（如有内容）
   - 计算 `MessageCount`、`UserMessageCount`、`FirstMessage`
   - 聚合 token 使用量（TotalOutputTokens、PeakContextTokens）
   - 调用 `accumulateMessageTokenUsageContext`
4. 实现 `buildCodeFreeUserMessage(turn gjson.Result, ordinal int) (ParsedMessage, bool)`：
   - `Role` = RoleUser
   - `Content` = turn.history.userMessage.text
   - 如果 text 为空，返回 emitted=false
5. 实现 `buildCodeFreeAssistantMessage(turn gjson.Result, ordinal int) (ParsedMessage, bool)`：
   - `Role` = RoleAssistant
   - `Content` = 合并所有 type=text 的 parts 的 text 字段
   - `Model` = turn.usage.model
   - `ContextTokens` = turn.usage.inputTokens
   - `OutputTokens` = turn.usage.outputTokens
   - `HasContextTokens` / `HasOutputTokens` = true
   - 遍历 type=toolCall 的 parts，构建 `ParsedToolCall`：
     - `ToolUseID` = toolCallId
     - `ToolName` = toolId
     - `InputJSON` = input
     - 构建 `ParsedToolResultEvent` 从 result.content
   - 如果 Content 为空且无 ToolCalls，返回 emitted=false
6. 实现 `buildCodeFreeSession(state gjson.Result, dbPath, machine string, messages []ParsedMessage, firstMessage string) *ParsedSession`
7. 编写单元测试 `codefree_test.go`：
   - 测试 `codefreeDecodeWorkingDir`（多种 URI 格式）
   - 测试 `codefreeAgentLabel`（多种 URI）
   - 测试 `loadCodeFreeSession` 使用模拟 JSON 数据
   - 测试空 turns、缺失字段、toolCall 解析
   - 测试时间戳转换
8. 运行 `go test ./internal/parser/... -run CodeFree` 确认测试通过

### 依赖关系

Task2

### 验收标准

- JSON 解析正确提取 session 元数据和消息序列
- 用户消息和助手消息正确构建
- toolCall 正确映射为 ParsedToolCall
- token 使用量正确解析
- URL 编码的工作目录正确解码
- agent URI 正确提取 label
- 空会话返回 nil（触发 SkipNoSession）
- 单元测试覆盖核心解析逻辑
- `go test ./internal/parser/... -run CodeFree` 通过

### 预估复杂度

高

---

## Task4: SourceSet 完整实现与 Provider 接线

### 任务目标

完善 `codefreeSourceSet` 的所有方法实现，将 provider 的各方法连接到 sourceSet，完成 Discover/Fingerprint/Parse/WatchPlan/FindSource/SourcesForChangedPath 全链路。

### 子任务

1. 在 `codefree_provider.go` 中实现 `codefreeSourceSet.Discover(ctx)`：
   - 遍历 roots，调用 `ListCodeFreeCatalog(root)`
   - 对每个 catalog 条目，检查 session.db 存在性
   - 生成 `SourceRef`（virtual member），设置 `DiscoveryMTimeNS`
   - 使用 `addJSONLSource` 去重，`sortJSONLSources` 排序
2. 实现 `codefreeSourceSet.DiscoverEach(ctx, yield)`：
   - 流式版本，逐个 yield SourceRef
3. 实现 `codefreeSourceSet.WatchPlan(ctx)`：
   - 监听根目录（非递归）
   - 监听 `__catalog__/session.db`
   - 监听每个已发现会话的 session.db
4. 实现 `codefreeSourceSet.Fingerprint(ctx, source)`：
   - stat session.db，返回 Size + MTimeNS
   - 考虑 WAL 复合 mtime（`sqliteDBCompositeMtime`）
   - 文件不存在时返回 Key-only fingerprint
5. 实现 `codefreeSourceSet.FindSource(ctx, req)`：
   - 通过 stored path / fingerprint key / raw session ID 查找
6. 实现 `codefreeSourceSet.SourcesForChangedPath(ctx, req)`：
   - 分类变更路径，返回受影响的 source
7. 实现 `codefreeSourceSet.sourceRef(root, path, allowMissing)` 和 `sourceFromRef(source)` 和 `newSourceRef(root, virtualPath, dbPath, sessionID)`
8. 完善 `codefreeProvider.Parse(ctx, req)`：
   - 从 SourceRef 提取 codefreeSource
   - 打开 session.db
   - 调用 `loadCodeFreeSession`
   - 返回 `ParseOutcome`（含 ForceReplace=true）
   - 文件不存在时返回 SkipNoSession
9. 完善 `codefreeProvider.ParseIncremental(ctx, req)`：
   - 基于 fingerprint 判断是否需要重新解析
10. 实现 `codefreeProvider.ResolveReconciliationScopes(ctx, req)`
11. 实现 `codefreeProvider.PersistentArchiveSource` 和 `StoredSourceHintScopes`（如需要）
12. 运行 `go build ./...` 确认编译通过
13. 运行 `go vet ./...` 确认无新警告
14. 运行 `go test ./internal/parser/...` 确认所有测试通过

### 依赖关系

Task3

### 验收标准

- `go build ./...` 编译通过
- `go vet ./...` 无新警告
- `go test ./internal/parser/...` 全部通过（含现有测试）
- Discover 能正确发现 catalog 中的会话
- Fingerprint 能正确计算 session.db 的指纹
- Parse 能正确解析会话并返回 ParseOutcome
- WatchPlan 能正确生成监听计划
- FindSource 能通过 session ID 查找会话

### 预估复杂度

高

---

## Task5: 集成验证与收尾

### 任务目标

全面验证 CodeFree provider 的编译、测试、格式和集成正确性，确保与现有代码无冲突。

### 子任务

1. 运行 `go fmt ./internal/parser/codefree*.go` 确认格式
2. 运行 `go build ./...` 确认全项目编译
3. 运行 `go vet ./...` 确认无警告
4. 运行 `go test ./internal/parser/... -count=1` 确认所有 parser 测试通过
5. 检查 `AgentDef` 的 `DefaultDirs` 是否覆盖 Windows/macOS/Linux 三平台路径
6. 检查 `IDPrefix` 为 `"codefree:"` 且与 `codefree-o:` 不冲突
7. 检查 capabilities 声明与实际实现一致
8. 确认无新增外部依赖（`go.mod` 未变化）
9. 运行 `go test ./... -count=1 -short` 确认项目整体无回归

### 依赖关系

Task4

### 验收标准

- `go build ./...` 通过
- `go vet ./...` 无新警告
- `go fmt` 格式正确
- `go test ./internal/parser/...` 全部通过
- `go test ./... -short` 无回归
- 无新增外部依赖
- AgentCodeFree 与 AgentCodefreeO 不冲突（不同 type、不同 prefix、不同路径）

### 预估复杂度

低
