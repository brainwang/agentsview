# 需求文档：CodeFree 会话读取功能

## 需求背景

agentsview 是一个将本地 AI agent 会话同步到 SQLite 并通过 Web UI 展示的工具。当前已支持多种 AI agent 格式（Claude、Codex、Cursor、TeleAgent、Kiro 等），但不支持 CodeFree IDE 的会话格式。

CodeFree IDE 是中国电信自主研发的研发云平台中的 AI 编程助手，其会话数据存储在 `AppData/Roaming/CodeFree/agentSessionData/` 目录下，每个会话以 UUID 命名的子目录中的 `session.db`（SQLite）文件存储。需要新增一个 parser，将 CodeFree 的会话数据解析为 agentsview 的标准 `ParsedSession`/`ParsedMessage` 模型，使其能被同步、展示和搜索。

## 功能范围

### 范围内

1. 新增 `AgentCodeFree` provider，支持发现和解析 CodeFree IDE 的会话数据
2. 解析 `agentSessionData/` 目录下的会话数据结构
3. 将 CodeFree 的会话格式映射为 agentsview 标准 `ParsedSession`/`ParsedMessage` 模型
4. 支持会话发现（Discover）、指纹计算（Fingerprint）、解析（Parse）全链路
5. 支持 tool call 消息的解析（CodeFree 的 `toolCall` part 类型）
6. 支持 token 使用量解析（inputTokens、outputTokens、cacheReadTokens、model）
7. 支持子代理（subagent）会话的标识
8. 在 agentsview 的 Web UI 中可选择 CodeFree 作为数据源

### 范围外

1. 不修改 CodeFree IDE 本身的数据格式或存储逻辑
2. 不支持写入/修改 CodeFree 会话数据（只读解析）
3. 不解析 Base64 编码目录和 untitled 目录中的文件变更元数据（这些目录不包含会话对话内容）
4. 不解析 `__catalog__` 目录之外的 changeset/diff 数据

## CodeFree 会话数据格式

### 目录结构

```
agentSessionData/
├── __catalog__/
│   └── session.db          # 全局目录索引
├── <UUID>/                  # 主会话目录（如 13d93fb4-8112-41e3-9b14-023242e8eae0）
│   └── session.db          # 会话数据（含 codefree.state.v1）
├── Y29kZWZyZWU6...         # Base64 编码目录（chat changeset，不含对话内容）
├── untitled-<UUID>/        # 未命名聊天 changeset（不含对话内容）
└── <Base64>-chatcmpl-tool-<id>/  # 子代理 changeset（不含对话内容）
```

### 数据模型

**`__catalog__/session.db`** — `session_metadata` 表中 `codefree.catalog.v1` 键：

```json
{
  "schemaVersion": 1,
  "sessions": [
    {
      "session": {
        "sessionId": "UUID",
        "startTime": 1793870827101,
        "modifiedTime": 1793870931978,
        "summary": "会话摘要",
        "workingDirectory": "file:///d%3A/Temp/Download/sub-exam",
        "customization": { "lastValidatedRevision": "..." }
      },
      "defaultAgentUri": "codefree-agent://builtin/spec"
    }
  ],
  "pendingSessionIds": []
}
```

**`<UUID>/session.db`** — `session_metadata` 表中 `codefree.state.v1` 键：

```json
{
  "schemaVersion": 1,
  "session": {
    "sessionId": "UUID",
    "startTime": 1793870827101,
    "modifiedTime": 1793870931978,
    "summary": "会话摘要",
    "workingDirectory": "file:///d%3A/Temp/Download/sub-exam",
    "customization": { "lastValidatedRevision": "..." }
  },
  "chats": [
    {
      "chatId": "ahp-chat://default/<base64>",
      "model": { "selectionId": "auto", "selectedAtCatalogRevision": "..." },
      "agent": { "uri": "codefree-agent://builtin/spec", "revision": "..." }
    }
  ],
  "completedTurns": [
    {
      "history": {
        "chatId": "ahp-chat://default/<base64>",
        "turnId": "request_<uuid>",
        "userMessage": { "text": "用户输入文本" },
        "parts": [
          { "type": "text", "text": "AI 回复文本" },
          { "type": "toolCall", "toolId": "list_dir", "displayName": "列出目录",
            "input": "{\"path\": \"...\"}", "disposition": "executed",
            "result": { "success": true, "content": [...] } }
        ],
        "continuation": null
      },
      "selection": {
        "model": { "selectionId": "auto" },
        "effectiveModel": { "id": "Default", "subService": "codefree-ide-agent-spec" },
        "agent": { "uri": "codefree-agent://builtin/spec" }
      },
      "usage": {
        "inputTokens": 37027,
        "outputTokens": 642,
        "cacheReadTokens": 8192,
        "model": "GLM-5.2",
        "_meta": { "totalCompletionTokens": 642 }
      }
    }
  ],
  "inFlightTurns": []
}
```

### Part 类型

- `text` — 文本消息，字段：`text`
- `toolCall` — 工具调用，字段：`toolId`、`displayName`、`input`（JSON 字符串）、`disposition`（executed/pending）、`result`（含 success、content）

### 时间戳格式

- `startTime`、`modifiedTime`：毫秒级 Unix 时间戳（JavaScript `Date.now()` 格式）

## 目标用户与使用场景

- **开发者**：使用 CodeFree IDE 进行 AI 辅助编程，希望在 agentsview 中查看、搜索和管理 CodeFree 会话历史
- **团队管理者**：通过 agentsview 统一查看团队成员使用多种 AI agent（包括 CodeFree）的会话记录

## 用户故事

1. 作为开发者，我希望 agentsview 能自动发现 `agentSessionData/` 目录下的 CodeFree 会话，这样我不需要手动导入
2. 作为开发者，我希望在 agentsview 的 Web UI 中能看到 CodeFree 会话列表，包括会话摘要、开始时间、工作目录
3. 作为开发者，我希望能查看 CodeFree 会话的完整对话内容，包括用户消息、AI 回复、工具调用
4. 作为开发者，我希望能看到每轮对话的 token 使用量和使用的模型名称
5. 作为开发者，我希望能搜索 CodeFree 会话中的内容

## 功能需求

### FR1: Provider 注册

- 新增 `AgentCodeFree` 类型到 `AgentType` 常量
- 在 `parser.Registry` 中添加 CodeFree 的 `AgentDef`，包括：
  - `DisplayName`: "CodeFree"
  - `DefaultDirs`: `["AppData/Roaming/CodeFree/agentSessionData"]`（相对于用户主目录）
  - `IDPrefix`: `"codefree:"`
  - `FileBased`: false（DB-backed）
- 在 `ProviderFactories()` switch 中添加 `AgentCodeFree` case
- 在 `providerMigrationModes` 中添加 `AgentCodeFree` 映射

### FR2: 会话发现（Discover）

- 扫描 `agentSessionData/` 目录
- 读取 `__catalog__/session.db` 中的 `codefree.catalog.v1` 获取已注册的会话列表
- 对每个 catalog 中的会话，检查对应的 `<sessionId>/session.db` 是否存在
- 为每个有效会话生成 `DiscoveredSource`，使用虚拟路径 `<dbPath>#<sessionId>`

### FR3: 指纹计算（Fingerprint）

- 基于 session.db 文件的修改时间（mtime）和文件大小计算指纹
- 或基于 `codefree.state.v1` JSON 中的 `modifiedTime` 字段

### FR4: 会话解析（Parse）

- 打开 `<UUID>/session.db`，读取 `session_metadata` 表中的 `codefree.state.v1` 键
- 解析 JSON，提取以下信息映射到 `ParsedSession`：
  - `sessionId` → Session ID（添加 `codefree:` 前缀）
  - `summary` → SessionName
  - `workingDirectory` → Cwd（解码 URI 为文件路径）
  - `startTime` → StartedAt
  - `modifiedTime` → EndedAt
  - `chats[0].agent.uri` → Agent（提取 agent 名称，如 "spec"）
  - `chats[0].model.selectionId` → Model
- 将 `completedTurns` 映射为 `ParsedMessage` 序列：
  - 每个 turn 的 `userMessage.text` → RoleUser 消息
  - 每个 turn 的 `parts` 中 `text` 类型 → RoleAssistant 消息
  - 每个 turn 的 `parts` 中 `toolCall` 类型 → RoleTool 消息（含工具名、输入、结果）
  - `usage.model` → 消息的 Model 字段
  - `usage.inputTokens`/`outputTokens` → 消息的 TokenUsage
  - `usage.cacheReadTokens` → ContextTokens
- `inFlightTurns` 中的未完成轮次也应被解析（如果存在内容）

### FR5: 工具调用解析

- `toolCall` part 的 `toolId` → 工具名称
- `toolCall` part 的 `displayName` → 工具显示名（如可用）
- `toolCall` part 的 `input`（JSON 字符串）→ 工具输入
- `toolCall` part 的 `result.content` → 工具输出内容
- `toolCall` part 的 `disposition` → 工具调用状态

### FR6: 增量解析（ParseIncremental）

- 支持 `ParseIncremental`，基于指纹变化检测是否需要重新解析
- 如果指纹未变化，返回未修改标记

### FR7: 文件监听（WatchPlan）

- 监听 `agentSessionData/` 目录变化
- 当 `__catalog__/session.db` 或某个 `<UUID>/session.db` 变化时，触发重新发现或重新解析

## 非功能需求

1. **只读访问**：所有 session.db 文件以只读模式打开（`?mode=ro`），不修改原始数据
2. **性能**：解析大型会话（629KB+ JSON）应在合理时间内完成
3. **健壮性**：处理 JSON 解析错误、缺失字段、空 turns 等异常情况不崩溃
4. **跨平台**：默认目录路径支持 Windows（`AppData/Roaming/CodeFree/agentSessionData`）和 macOS/Linux（`~/.config/CodeFree/agentSessionData` 或类似路径）
5. **并发安全**：多 goroutine 同时解析不同会话不应冲突
6. **无新依赖**：使用项目已有的 `github.com/mattn/go-sqlite3` 和标准库，不引入新依赖

## 验收标准

1. `go build ./...` 编译通过，无错误
2. `go vet ./...` 无新警告
3. `go fmt ./...` 格式正确
4. 配置 agentsview 指向 CodeFree 数据目录后，能发现 catalog 中列出的会话
5. 每个会话能正确解析出用户消息、AI 回复、工具调用
6. 会话元数据（摘要、时间、工作目录、模型名称）正确映射
7. Token 使用量正确解析
8. 现有 parser 测试不受影响（`go test ./internal/parser/...` 通过）
9. 新增 CodeFree parser 的单元测试覆盖核心解析逻辑

## 边界情况

1. **空会话**：`completedTurns` 为空数组的会话 — 应创建 ParsedSession 但 MessageCount 为 0
2. **无 catalog 的会话**：`__catalog__` 中未列出但 `<UUID>/session.db` 存在且有 `codefree.state.v1` — 当前方案以 catalog 为准，这类会话不会被导入
3. **catalog 中列出但 session.db 不存在**：应跳过并记录警告
4. **`inFlightTurns` 非空**：进行中的对话轮次，应尝试解析已有内容
5. **subagent chats**：会话中包含子代理聊天 — 当前方案将所有 turns 放在同一个 ParsedSession 中
6. **超长 JSON**：`codefree.state.v1` 可能超过 600KB — 需确保 JSON 解析不会 OOM
7. **URL 编码的工作目录**：`workingDirectory` 使用 `file:///` URI 格式且路径 URL 编码（如 `d%3A` → `d:`）— 需解码
8. **时间戳精度**：CodeFree 使用毫秒时间戳，agentsview 可能使用不同精度 — 需确认并正确转换
9. **未保存的 untitled 会话**：untitled 目录没有 `codefree.state.v1`，不在 catalog 中 — 不会被导入

## 风险与待确认事项

1. **catalog 覆盖度**：`__catalog__` 中只列出了已保存的会话。如果用户在 CodeFree 中有未保存但已有对话的会话，这些会话不会被导入。需要确认这是否是期望行为。
2. **跨平台路径**：CodeFree IDE 在 macOS/Linux 上的数据目录路径需要确认。当前只验证了 Windows 路径。
3. **Agent URI 映射**：`codefree-agent://builtin/spec` 中的 "spec" 应映射为 agentsview 的 Agent 字段。需确认所有可能的 agent URI 值（spec、craft、explore 等）。
4. **模型名称**：`usage.model` 字段值如 "GLM-5.2" 应直接作为 Model 字段。`selection.model.selectionId` 值如 "auto" 不是实际模型名，应使用 `usage.model`。
5. **Tool call 结果大小**：某些工具调用结果可能非常大（如 `read_file` 返回完整文件内容），需确保不会超出 agentsview 的存储限制。
6. **多 chat 会话**：一个会话可能有多个 chats（主聊天 + 子代理），当前方案将所有 turns 混在一起。如果需要区分，可能需要额外字段或拆分为多个会话。
