# 设计文档：CodeFree 会话读取功能

## 需求理解与设计目标

为 agentsview 新增 `AgentCodeFree` provider，解析 CodeFree IDE 的 `agentSessionData/` 目录下的会话数据。CodeFree 使用 SQLite `session.db` 文件存储会话状态，核心数据以 JSON 形式存储在 `session_metadata` 表的 `codefree.state.v1` 键中。与 TeleAgent 类似，CodeFree 是 DB-backed provider，但其数据模型与 TeleAgent 不同——CodeFree 不使用多表关系 schema，而是将完整会话状态序列化为单个 JSON 值。

### 设计目标

1. 复用 agentsview 现有的 Provider 接口和 sourceSet 模式（参照 TeleAgent）
2. 以 `__catalog__/session.db` 中的 `codefree.catalog.v1` 作为会话发现索引
3. 将 CodeFree 的 turn-based JSON 模型正确映射为 agentsview 的 message-based `ParsedSession`/`ParsedMessage` 模型
4. 支持 tool call、token usage、agent URI 等元数据的完整提取

## 技术选型

- **语言**：Go（项目现有语言）
- **SQLite 驱动**：`github.com/mattn/go-sqlite3`（项目已有依赖）
- **JSON 解析**：`github.com/tidwall/gjson`（项目已有依赖，用于动态 JSON 字段提取，与 TeleAgent parser 一致）
- **无新依赖**：所有需要的库已在 `go.mod` 中

## 总体架构设计

### 文件结构

参照 TeleAgent 的三文件模式：

```
internal/parser/
├── codefree_provider.go     # Provider 接口实现 + sourceSet（参照 teleagent_provider.go）
├── codefree_sqlite.go       # SQLite store + catalog 读取 + session 发现（参照 teleagent_sqlite.go）
├── codefree.go              # JSON 解析逻辑 + ParsedSession/ParsedMessage 构建（参照 teleagent.go）
```

### 注册点修改

参照 TeleAgent 在以下 4 个位置添加 CodeFree 注册：

1. **`types.go`** — `AgentType` 常量
2. **`types.go`** — `Registry` 切片
3. **`provider.go`** — `ProviderFactories()` switch
4. **`provider_migration.go`** — `providerMigrationModes` map

### 数据流

```
配置目录 (agentSessionData/)
        │
        ▼
    Discover()
        │  读取 __catalog__/session.db
        │  解析 codefree.catalog.v1
        │  对每个 sessionId 检查 <sessionId>/session.db 是否存在
        ▼
    SourceRef[] (每个会话一个 virtual member)
        │
        ▼
    Fingerprint()
        │  stat <sessionId>/session.db
        │  返回 mtime + size
        ▼
    Parse()
        │  打开 <sessionId>/session.db
        │  读取 session_metadata.codefree.state.v1
        │  解析 JSON → ParsedSession + ParsedMessage[]
        ▼
    ParseResult
```

## 模块/组件设计

### 1. codefree_provider.go

#### codefreeProviderFactory

```go
type codefreeProviderFactory struct {
    def AgentDef
}

func newCodeFreeProviderFactory(def AgentDef) ProviderFactory {
    return codefreeProviderFactory{def: cloneAgentDef(def)}
}
```

实现 `ProviderFactory` 接口的三个方法：`Definition()`、`Capabilities()`、`NewProvider(ProviderConfig)`。

#### codefreeProvider

```go
type codefreeProvider struct {
    ProviderBase
    sources codefreeSourceSet
}
```

实现 `Provider` 接口，委托给 `codefreeSourceSet` 处理所有源操作。参照 `teleagentProvider` 的结构，包括 `Discover`、`WatchPlan`、`SourcesForChangedPath`、`FindSource`、`Fingerprint`、`Parse`、`ParseIncremental`。

#### codefreeSourceSet

```go
type codefreeSourceSet struct {
    roots   []string
    readDir func(string) ([]os.DirEntry, error)
}
```

与 `teleagentSourceSet` 结构一致，但 discovery 逻辑不同：CodeFree 通过 catalog 索引发现会话，而非直接枚举数据库表行。

#### Source 类型

```go
type codefreeSourceKind uint8

const (
    codefreeSourceSQLiteSession codefreeSourceKind = iota + 1
)

type codefreeSource struct {
    Root      string
    Path      string         // 虚拟路径: <dbPath>#<sessionId>
    DBPath    string         // <root>/<sessionId>/session.db
    SessionID string         // UUID
    Kind      codefreeSourceKind
}
```

CodeFree 不需要 container source kind（TeleAgent 的 `teleagentSourceSQLiteDB`），因为每个会话有独立的 session.db 文件，不存在共享容器。

#### Capabilities

```go
func codefreeProviderCapabilities() Capabilities {
    source := jsonlFileProviderSourceCapabilities()
    source.StreamingDiscovery = CapabilitySupported
    source.StoredSourceHints = CapabilitySupported
    source.PerSessionErrors = CapabilitySupported
    source.ForceReplaceOnParse = CapabilitySupported
    // 不需要 PersistentArchive（每个会话独立 DB，不存在共享容器丢失场景）
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
```

### 2. codefree_sqlite.go

#### CodeFreeCatalogEntry

```go
type CodeFreeCatalogEntry struct {
    SessionID       string
    StartTime       int64   // 毫秒时间戳
    ModifiedTime    int64   // 毫秒时间戳
    Summary         string
    WorkingDir      string  // file:/// URI
    DefaultAgentURI string  // codefree-agent://builtin/spec
}
```

#### CodeFreeSQLiteStore

```go
type CodeFreeSQLiteStore struct {
    dbPath string
    db     *sql.DB
}

func OpenCodeFreeSQLiteStore(dbPath string) (*CodeFreeSQLiteStore, error)
```

只读打开 session.db，拒绝符号链接（与 TeleAgent 一致）。

#### 核心函数

```go
// 读取 __catalog__/session.db 中的 catalog
func ListCodeFreeCatalog(root string) ([]CodeFreeCatalogEntry, error)

// 读取 <sessionId>/session.db 中的 codefree.state.v1
func (s *CodeFreeSQLiteStore) LoadStateJSON() (string, error)

// 检查会话是否存在
func CodeFreeSessionExists(root, sessionID string) bool
```

#### Catalog 读取流程

1. 打开 `<root>/__catalog__/session.db`
2. `SELECT value FROM session_metadata WHERE key='codefree.catalog.v1'`
3. 解析 JSON，提取 `sessions` 数组
4. 对每个 session 条目，检查 `<root>/<sessionId>/session.db` 是否存在
5. 返回有效的 catalog 条目列表

#### 常量

```go
const codefreeSQLiteDBName = "session.db"
const codefreeCatalogDir  = "__catalog__"
const codefreeCatalogKey  = "codefree.catalog.v1"
const codefreeStateKey    = "codefree.state.v1"
```

### 3. codefree.go

#### JSON 结构定义

使用 `gjson` 进行动态字段提取，避免为完整的 CodeFree JSON 定义严格的 Go 结构体（与 TeleAgent 使用 gjson 的方式一致）。

```go
// codefreeState 对应 codefree.state.v1 JSON
// 顶层键: schemaVersion, session, chats, completedTurns, inFlightTurns
```

#### 核心解析函数

```go
func loadCodeFreeSession(
    ctx context.Context, db *sql.DB, dbPath, sessionID, machine string,
) (*ParsedSession, []ParsedMessage, error)
```

流程：
1. 从 `session_metadata` 表读取 `codefree.state.v1` 的值
2. 用 `gjson.Parse` 解析 JSON
3. 提取 session 元数据构建 `ParsedSession`
4. 遍历 `completedTurns` 构建 `ParsedMessage` 序列
5. 遍历 `inFlightTurns` 追加未完成的轮次（如有内容）
6. 计算 token 聚合值

#### 字段映射

**Session 元数据映射：**

| CodeFree 字段 | ParsedSession 字段 | 转换 |
|---|---|---|
| `session.sessionId` | `ID` | 添加 `codefree:` 前缀 |
| `session.summary` | `SessionName` | 直接映射 |
| `session.workingDirectory` | `Cwd` | URL 解码 `file:///` URI → 文件路径 |
| `session.startTime` | `StartedAt` | 毫秒时间戳 → `time.UnixMilli()` |
| `session.modifiedTime` | `EndedAt` | 毫秒时间戳 → `time.UnixMilli()` |
| `chats[0].agent.uri` | `AgentLabel` | 提取 URI 最后一段（如 `spec`） |
| — | `Agent` | `AgentCodeFree` |
| — | `SourceSessionID` | 原始 sessionId（无前缀） |

**消息映射（每个 completedTurn 产生多条 ParsedMessage）：**

| CodeFree 字段 | ParsedMessage 字段 | 转换 |
|---|---|---|
| `history.userMessage.text` | `Content` (RoleUser) | 直接映射 |
| `history.parts[type=text].text` | `Content` (RoleAssistant) | 合并所有 text parts |
| `history.parts[type=toolCall]` | `ToolCalls` (RoleAssistant) | 见工具调用映射 |
| `usage.model` | `Model` | 直接映射（如 "GLM-5.2"） |
| `usage.inputTokens` | `ContextTokens` | 直接映射 |
| `usage.outputTokens` | `OutputTokens` | 直接映射 |
| `usage.cacheReadTokens` | — | 记入 TokenUsage JSON |

**工具调用映射：**

| CodeFree toolCall 字段 | ParsedToolCall 字段 | 转换 |
|---|---|---|
| `toolCallId` | `ToolUseID` | 直接映射 |
| `toolId` | `ToolName` | 直接映射 |
| `input` | `InputJSON` | 直接映射（已是 JSON 字符串） |
| `displayName` | — | 可选：附加到 ToolName 或忽略 |
| `result.content` | `ResultEvents[].Content` | 序列化为文本 |
| `disposition` | `ResultEvents[].Status` | 直接映射 |

#### 消息构建顺序

每个 turn 按以下顺序生成消息：

1. **用户消息**（Ordinal 递增）：`history.userMessage.text` → `RoleUser` 消息
2. **助手消息**（Ordinal 递增）：遍历 `history.parts`：
   - `text` 类型 → 拼接为 `Content`
   - `toolCall` 类型 → 构建 `ParsedToolCall` 附加到 `ToolCalls`
   - 最终生成一个 `RoleAssistant` 消息，包含所有 text content 和 tool calls
3. 如果 turn 的 parts 只有 toolCall 没有 text，仍然生成一个 `RoleAssistant` 消息（Content 为空，HasToolUse 为 true）

#### URL 解码工作目录

CodeFree 的 `workingDirectory` 使用 `file:///` URI 格式且路径 URL 编码（如 `file:///d%3A/Temp/Download/sub-exam`）。

```go
func codefreeDecodeWorkingDir(uri string) string {
    // 1. 去掉 file:/// 前缀
    // 2. URL 解码（d%3A → d:）
    // 3. 处理 Windows 盘符（d:/ → d:\）
}
```

#### Agent URI 提取

```go
func codefreeAgentLabel(uri string) string {
    // codefree-agent://builtin/spec → "spec"
    // codefree-agent://builtin/craft → "craft"
    // codefree-agent://builtin/explore → "explore"
    // 提取最后一段路径
}
```

### 4. Discover 实现

```go
func (s codefreeSourceSet) Discover(ctx context.Context) ([]SourceRef, error) {
    sources := make([]SourceRef, 0)
    seen := make(map[string]struct{})
    for _, root := range s.roots {
        catalog, err := ListCodeFreeCatalog(root)
        if err != nil {
            return nil, err
        }
        for _, entry := range catalog {
            dbPath := filepath.Join(root, entry.SessionID, codefreeSQLiteDBName)
            if !IsRegularFile(dbPath) {
                continue  // 跳过不存在的会话
            }
            virtualPath := VirtualSourcePath(dbPath, entry.SessionID)
            source := s.newSourceRef(root, virtualPath, dbPath, entry.SessionID)
            source.DiscoveryMTimeNS = entry.ModifiedTime * int64(time.Millisecond)
            addJSONLSource(source, &sources, seen)
        }
    }
    sortJSONLSources(sources)
    return sources, nil
}
```

### 5. Fingerprint 实现

```go
func (s codefreeSourceSet) Fingerprint(
    ctx context.Context, source SourceRef,
) (SourceFingerprint, error) {
    src, ok := s.sourceFromRef(source)
    // ...
    info, err := os.Stat(src.DBPath)
    // ...
    fingerprint := SourceFingerprint{
        Key:     key,
        Size:    info.Size(),
        MTimeNS: info.ModTime().UnixNano(),
    }
    // 考虑 WAL 复合 mtime
    if compositeMtime, err := sqliteDBCompositeMtime(
        src.DBPath, sqliteDBJournalSuffixes,
    ); err == nil {
        fingerprint.MTimeNS = compositeMtime
    }
    return fingerprint, nil
}
```

### 6. WatchPlan 实现

```go
func (s codefreeSourceSet) WatchPlan(context.Context) (WatchPlan, error) {
    roots := make([]WatchRoot, 0)
    for _, root := range s.roots {
        // 监听根目录（非递归），检测新会话目录
        roots = append(roots, WatchRoot{
            Path:        root,
            Recursive:   false,
            DebounceKey: string(AgentCodeFree) + ":root:" + root,
        })
        // 监听 __catalog__/session.db
        catalogDB := filepath.Join(root, codefreeCatalogDir, codefreeSQLiteDBName)
        if IsRegularFile(catalogDB) {
            roots = append(roots, WatchRoot{
                Path:         catalogDB,
                Recursive:    false,
                IncludeGlobs: []string{codefreeSQLiteDBName, codefreeSQLiteDBName + "-*"},
                DebounceKey:  string(AgentCodeFree) + ":catalog:" + catalogDB,
            })
        }
        // 监听每个已发现会话的 session.db
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
```

## 数据模型

### CodeFree JSON 顶层结构

```
codefree.state.v1:
├── schemaVersion: int
├── session:
│   ├── sessionId: string (UUID)
│   ├── startTime: int (毫秒时间戳)
│   ├── modifiedTime: int (毫秒时间戳)
│   ├── summary: string
│   ├── workingDirectory: string (file:/// URI)
│   └── customization: object
├── chats: array
│   ├── chatId: string (ahp-chat://default/<base64>)
│   ├── model: { selectionId, selectedAtCatalogRevision }
│   ├── agent: { uri, revision }
│   └── subagent?: { parentChat, parentTurnId, toolCallId, title, agentName }
├── completedTurns: array
│   └── history:
│       ├── chatId: string
│       ├── turnId: string (request_<uuid>)
│       ├── userMessage: { text }
│       ├── parts: array
│       │   ├── { type: "text", text: string }
│       │   └── { type: "toolCall", toolId, displayName, input, disposition, result }
│       └── continuation: null
│   selection: { model, effectiveModel, agent }
│   usage: { inputTokens, outputTokens, cacheReadTokens, model, _meta }
└── inFlightTurns: array (同 completedTurns 结构)
```

### 映射到 agentsview 数据模型

- 一个 CodeFree **session** → 一个 `ParsedSession`
- 一个 CodeFree **turn** → 一条 `RoleUser` 消息 + 一条 `RoleAssistant` 消息（含 tool calls）
- 一个 CodeFree **text part** → 助手消息的 `Content` 的一部分
- 一个 CodeFree **toolCall part** → 助手消息的一个 `ParsedToolCall`

## API / 服务接口设计

### Provider 接口实现

CodeFree provider 实现 `Provider` 接口的所有方法，无需新增 API。外部调用方式与现有 provider 一致：

```go
// 注册
factory := newCodeFreeProviderFactory(def)
provider := factory.NewProvider(cfg)

// 发现
sources, _ := provider.Discover(ctx)

// 解析
outcome, _ := provider.Parse(ctx, ParseRequest{Source: sources[0]})
```

### 新增导出函数

```go
// codefree_sqlite.go
func OpenCodeFreeSQLiteStore(dbPath string) (*CodeFreeSQLiteStore, error)
func ListCodeFreeCatalog(root string) ([]CodeFreeCatalogEntry, error)
func CodeFreeSessionExists(root, sessionID string) bool
func CodeFreeVirtualPath(dbPath, sessionID string) string

// codefree.go
func LoadCodeFreeSession(ctx context.Context, dbPath, sessionID, machine string) (*ParsedSession, []ParsedMessage, error)
```

## 状态流转与交互流程

### 发现流程

```
root dir → __catalog__/session.db → codefree.catalog.v1 JSON
    → sessions[] → 每个 sessionId
    → 检查 <root>/<sessionId>/session.db 存在
    → 生成 SourceRef (virtual member)
```

### 解析流程

```
SourceRef → codefreeSource (从 Opaque 提取)
    → 打开 <root>/<sessionId>/session.db (只读)
    → SELECT value FROM session_metadata WHERE key='codefree.state.v1'
    → gjson 解析 JSON
    → 构建 ParsedSession (session 元数据)
    → 遍历 completedTurns:
        → 构建 RoleUser 消息 (userMessage.text)
        → 构建 RoleAssistant 消息 (parts: text + toolCall)
    → 遍历 inFlightTurns (如有)
    → 聚合 token 使用量
    → 返回 ParseResult{Session, Messages}
```

### 指纹流程

```
SourceRef → DBPath
    → os.Stat(DBPath)
    → Size + MTime (考虑 WAL 复合 mtime)
    → SourceFingerprint{Key, Size, MTimeNS}
```

## 错误处理

1. **catalog 不存在或不可读**：`Discover` 返回空列表 + 错误日志（不阻塞其他 root）
2. **session.db 不存在**：`Discover` 跳过该会话；`Parse` 返回 `SkipNoSession`
3. **JSON 解析失败**：`Parse` 返回 `SourceError`（Retryable=true），不崩溃
4. **缺失字段**：使用 gjson 的默认零值（空字符串、0），不报错
5. **空 turns**：创建 ParsedSession 但 MessageCount=0，如果没有任何内容则返回 `SkipNoSession`
6. **SQLite 锁定**：使用 `busy_timeout=3000` 与现有 provider 一致
7. **超长 JSON**：Go 的 `gjson` 和 `database/sql` 无 JSON 大小限制，600KB 不存在问题

## 性能与安全考虑

### 性能

- **只读打开**：所有 session.db 使用 `?mode=ro`，不产生写锁
- **按需打开**：每个 session.db 仅在 Fingerprint/Parse 时打开，用完即关
- **gjson 延迟解析**：只提取需要的字段，不反序列化整个 JSON 到 Go 结构体
- **catalog 缓存**：Discover 阶段读一次 catalog，不重复读取

### 安全

- **符号链接拒绝**：`OpenCodeFreeSQLiteStore` 拒绝符号链接路径（与 TeleAgent 一致）
- **路径注入**：sessionId 来自 catalog JSON，需验证其为有效 UUID 格式后再拼接路径
- **只读模式**：绝不以读写模式打开 CodeFree 的数据库

## 测试策略

### 单元测试

1. **`codefree_test.go`**：
   - 测试 JSON 解析：构造模拟 `codefree.state.v1` JSON，验证 ParsedSession 和 ParsedMessage 的字段映射
   - 测试 URL 解码工作目录
   - 测试 agent URI 提取
   - 测试空 turns / 空 parts / 缺失字段
   - 测试 toolCall 解析

2. **`codefree_sqlite_test.go`**：
   - 测试 catalog 读取：构造模拟 `__catalog__/session.db`
   - 测试 session 存在性检查
   - 测试 session.db 打开和状态读取

3. **测试数据**：
   - 使用 `testing-without-tautologies` skill 的原则构造有意义的测试数据
   - 不 mock parser 自身的逻辑，只 mock 外部 I/O（SQLite 文件）

### 集成测试

- 验证 `go build ./...` 编译通过
- 验证 `go vet ./...` 无新警告
- 验证 `go test ./internal/parser/...` 现有测试不受影响

## 注册清单

| 文件 | 修改内容 |
|---|---|
| `internal/parser/types.go` L91 | 添加 `AgentCodeFree AgentType = "codefree"` 常量 |
| `internal/parser/types.go` Registry | 添加 CodeFree 的 `AgentDef` 条目 |
| `internal/parser/provider.go` L1292 | 添加 `case AgentCodeFree: return newCodeFreeProviderFactory(def)` |
| `internal/parser/provider_migration.go` L90 | 添加 `AgentCodeFree: ProviderMigrationProviderAuthoritative` |
| `internal/parser/codefree_provider.go` | **新建** — Provider + sourceSet 实现 |
| `internal/parser/codefree_sqlite.go` | **新建** — SQLite store + catalog 读取 |
| `internal/parser/codefree.go` | **新建** — JSON 解析 + ParsedSession/ParsedMessage 构建 |
| `internal/parser/codefree_test.go` | **新建** — 单元测试 |

### Registry 条目设计

```go
{
    // CodeFree IDE is a local Electron-based AI coding assistant
    // (研发云 CodeFree). Session data is stored in per-session
    // SQLite databases under agentSessionData/<sessionId>/session.db.
    // The __catalog__/session.db index lists registered sessions.
    // The provider reads codefree.state.v1 from each session DB,
    // which contains the full conversation state as JSON.
    Type:        AgentCodeFree,
    DisplayName: "CodeFree",
    EnvVar:      "CODEFREE_DIR",
    ConfigKey:   "codefree_dirs",
    DefaultDirs: []string{
        // Windows
        "AppData/Roaming/CodeFree/agentSessionData",
        // macOS
        "Library/Application Support/CodeFree/agentSessionData",
        // Linux
        ".config/CodeFree/agentSessionData",
    },
    IDPrefix:    "codefree:",
    FileBased:   false,
},
```

## 风险与待确认事项

1. **catalog 覆盖度**：以 catalog 为准意味着未保存的会话（untitled）不会被导入。这是已确认的设计决策。
2. **跨平台路径**：macOS/Linux 的 CodeFree 数据目录路径未实际验证，基于 Electron 应用的通用模式推断。
3. **多 chat 会话**：当前设计将所有 turns（含子代理 turns）放在同一个 ParsedSession 中。如果后续需要区分子代理，可扩展为按 chatId 拆分。
4. **时间戳精度**：CodeFree 使用毫秒时间戳，agentsview 的 `time.Time` 可直接通过 `time.UnixMilli()` 转换，无精度损失。
5. **inFlightTurns 处理**：进行中的轮次可能有不完整的 parts，解析时需容忍缺失的 usage 和 selection 字段。
6. **toolCall result 大小**：工具调用结果（如 read_file）可能很大，agentsview 的存储层已有截断机制（`SafeTruncate`），parser 层不做额外截断。
