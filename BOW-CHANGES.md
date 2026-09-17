# BOW-CHANGES

本文件记录 agentsview 项目的功能变更。

## 20260529

### 1. OpenCode/Codefree-O 工具输出提取

**功能描述：**  
修复 OpenCode 和 Codefree-O 会话中工具调用输出无法显示的问题。之前 parser 只提取了 `state.input`，忽略了 `state.output` 字段，导致 Bash、Read 等工具的 output 在 GUI 中不显示。

**修改文件：**

- `internal/parser/opencode.go`
  - `openCodeToolState` 结构体增加 `Output` 字段
  - `extractOpenCodeToolCall()` 函数同时返回 `ParsedToolCall` 和 `ParsedToolResult`
  - `buildOpenCodeMessage()` 函数收集并返回 `toolResults` 到 `ParsedMessage`

- `internal/parser/opencode_test.go`
  - 新增 `TestParseOpenCodeDB_ToolOutput()` 测试：验证有 output 的 tool call
  - 新增 `TestParseOpenCodeDB_ToolNoOutput()` 测试：验证没有 output 的 tool call

- `internal/db/db.go`
  - `dataVersion` 从 27 升级到 28，触发全量重新同步

**影响范围：**
- OpenCode 会话的工具调用现在会显示 output
- Codefree-O 会话自动受益（复用 OpenCode parser）
- 需要重新同步现有会话以提取历史工具输出

---

### 2. HTML 导出增加结构化工具调用

**功能描述：**  
HTML 导出之前只显示消息文本内容，缺少结构化的工具调用信息（工具名称、输入参数、输出结果）。现在导出的 HTML 包含完整的工具调用块，以可折叠的 `<details>` 元素呈现。

**修改文件：**

- `internal/server/export.go`
  - 新增 `exportToolCall` 结构体，包含 Category、Name、InputHTML、Output、HasOutput 字段
  - `exportMessage` 结构体增加 `ToolCalls []exportToolCall` 字段
  - `generateExportHTML()` 函数遍历 `m.ToolCalls` 并填充结构化数据
  - 新增 `formatToolInputForExport()` 函数：将 InputJSON 格式化为缩进的 JSON
  - HTML 模板增加 tool call 渲染逻辑：使用 `<details>` 折叠展示 input 和 output
  - CSS 增加 `.tool-call-block`、`.tool-call-header`、`.tool-call-cat`、`.tool-call-name`、`.tool-call-body`、`.tool-call-section`、`.tool-call-label`、`.tool-call-pre` 样式

- `internal/server/export_test.go`
  - 新增 `TestGenerateExportHTML_ToolCalls()` 测试：验证 tool calls 正确导出到 HTML

**效果：**
- HTML 导出文件包含所有工具调用的输入参数和输出结果
- 工具调用以可折叠的 `<details>` 元素呈现，默认折叠
- 输入参数格式化为缩进的 JSON，输出结果保持原始格式

---

### 3. HTML 导出默认展开 Thinking 内容

**功能描述：**  
之前导出的 HTML 默认隐藏 Thinking blocks 和 thinking-only 消息（这些消息通常包含工具调用）。用户需要手动勾选 "Thinking" 复选框才能看到工具调用。现在默认展开 Thinking 内容，使所有工具调用立即可见。

**修改文件：**

- `internal/server/export.go`
  - HTML 模板中 `<input type="checkbox" id="thinking-toggle">` 增加 `checked` 属性

**效果：**
- 打开 HTML 导出文件后，Thinking blocks 默认显示
- thinking-only 消息（包含工具调用）默认显示
- 用户仍可以点击 "Thinking" 按钮隐藏这些内容
- 浏览器搜索功能可以直接找到工具调用内容

---

## 变更总结

| 功能 | 影响文件数 | 新增测试 | 需要重新同步 |
|------|-----------|---------|-------------|
| OpenCode 工具输出提取 | 3 | 2 | ✓ |
| HTML 导出工具调用 | 2 | 1 | ✗ |
| Thinking 默认展开 | 1 | 0 | ✗ |

**部署说明：**
1. 重新编译 agentsview：`go build -o agentsview.exe ./cmd/agentsview`
2. 重启服务，dataVersion 升级会自动触发全量重新同步
3. 重新导出 HTML 以查看新的工具调用块

## 20260917

将 bow-0.29.0 相对 v0.29.0 的定制功能合并到 bow-v0.43.0（适配 v0.43.0 重构后的 OpenCode-format provider 架构）。

### 1. 新增 Codefree-O Agent（OpenCode 格式变体）

**功能描述：**
Codefree-O 是 OpenCode 的一个 fork，存储布局相同但根目录不同（`.codefree-o/.local/share/storage/session`）且 SQLite 回退文件名为 `codefree.db`。本次将其作为 v0.43.0 `openCodeFormat` 架构下的一个新变体注册，复用 OpenCode 的发现、查找、指纹、解析代码，仅通过 relabel 改写 agent 标签和 ID 前缀（`codefree-o:`）。

**修改文件：**

- `internal/parser/types.go`
  - 新增 `AgentCodefreeO AgentType = "codefree-o"` 常量
  - `Registry` 中新增 Codefree-O 条目：`DefaultDirs: [".codefree-o/.local/share"]`、`IDPrefix: "codefree-o:"`、`WatchSubdirs: [storage/session, storage/message, storage/part]`、`WatchRootsFunc: ResolveCodefreeOWatchRoots`

- `internal/parser/discovery.go`
  - 新增 `codefreeOFmt = openCodeFormat{agent: AgentCodefreeO, dbName: "codefree.db", sessionSubdir: "session"}`
  - 新增包装函数：`ResolveCodefreeOSource`、`ResolveCodefreeOWatchRoots`、`CodefreeOSQLiteVirtualPath`、`ParseCodefreeOSQLiteVirtualPath`

- `internal/parser/codefreeo.go`（新增文件）
  - `ListCodefreeOSessionMeta`：复用 `ListOpenCodeSessionMeta` 并改写 VirtualPath
  - `CodefreeOSourceMtime`：复用 `openCodeSQLiteSessionMtime` / `openCodeStorageSessionMtime`
  - `relabelOpenCodeSessionAsCodefreeO`：将 `opencode:` 前缀改写为 `codefree-o:` 并设置 `Agent = AgentCodefreeO`

- `internal/parser/opencode_provider.go`
  - 新增 `newCodefreeOProviderFactory`
  - `openCodeProviderSpecForAgent` 新增 `case AgentCodefreeO`，绑定 `codefreeOFmt`、`CodefreeOSourceMtime`、`relabelOpenCodeSessionAsCodefreeO`

- `internal/parser/provider.go`
  - factory switch 新增 `case AgentCodefreeO: return newCodefreeOProviderFactory(def)`

- `internal/parser/provider_migration.go`
  - `providerMigrationModes` 映射新增 `AgentCodefreeO: ProviderMigrationProviderAuthoritative`

- `internal/sync/engine.go`
  - `isOpenCodeFormatAgent`、`isOpenCodeFormatStorageAgent`、`openCodeFormatDBName`、`resolveOpenCodeFormatSource`、`openCodeFormatSourceMtime` 全部新增 `AgentCodefreeO` 分支，使路径分类、指纹、调和逻辑与 Kilo/MiMoCode/Icodemate 一致

- `internal/parser/capabilities_sync_test.go`
  - `wantSync` 映射新增 `AgentCodefreeO` 条目（`UnchangedResultMTimeAndHash` + `FingerprintHashRequiredForFreshness`）

- `internal/parser/opencode_provider_test.go`
  - `TestOpenCodeFamilyVariantsHonorWatermarkListing` 的 agent 列表新增 `AgentCodefreeO`

- `internal/parser/provider_capabilities_test.go`
  - `TestProviderCapabilitiesChangedPathRelevanceMatchConsumers` 白名单新增 `AgentCodefreeO`

- `internal/parser/reconciliation_scope_test.go`
  - `TestOpenCodeFamilyPlansWidenTheirOwnContainers` 的 agent 列表新增 `AgentCodefreeO`

- `internal/parser/types_test.go`
  - `TestRegistryCompleteness` 的 `allTypes` 列表新增 `AgentCodefreeO`

- `frontend/src/lib/utils/agents.ts`
  - `KNOWN_AGENTS` 新增 `{ name: "codefree-o", color: "var(--accent-purple)", label: "Codefree-O" }`

- `frontend/src/lib/utils/agents.test.ts`
  - agent 名称列表新增 `"codefree-o"`

- `frontend/src/lib/utils/resume.ts`
  - `RESUME_AGENTS` 新增 `"codefree-o"` → `codefree-o --session <id>`

**影响范围：**
- Codefree-O 会话被发现、解析、显示为独立 agent，ID 前缀为 `codefree-o:`
- v0.43.0 的 `AgentDirSettings.svelte` 从 `parser.Registry` 动态读取 provider 列表，无需前端手动维护标签
- OpenCode-format 家族（OpenCode、Kilo、MiMoCode、Icodemate、Codefree-O）共享同一套发现/分类/调和逻辑

---

### 2. 禁用启动时自动更新检查

**功能描述：**
取消桌面端启动时的自动更新检查调用，以及前端 App 启动时的 `checkForUpdate` 调用。保留手动菜单触发更新检查的能力。

**修改文件：**

- `desktop/src-tauri/src/lib.rs`
  - 移除 `schedule_auto_update_check(app.handle().clone())` 调用（保留函数定义并加 `#[allow(dead_code)]`）

- `frontend/src/App.svelte`
  - 移除 `sync.checkForUpdate()` 调用

**影响范围：**
- 启动时不再自动检查更新，避免无网络或自托管场景下的延迟
- 用户仍可通过菜单手动触发更新检查

---

### 3. Tauri WebView2 导出下载修复

**功能描述：**
v0.43.0 的 `downloadAuthenticatedExport` 在本地连接（无 token）时使用 `window.open`，这在 Tauri 的 WebView2 中被阻止，导致本地桌面端无法下载导出文件。改为本地和远程连接都使用 `fetch`。

**修改文件：**

- `frontend/src/lib/api/client.ts`
  - `downloadAuthenticatedExport` 移除 `window.open` 分支，统一使用 `fetch`（有 token 时带 `authHeaders`，无 token 时不带）

**影响范围：**
- 桌面端（Tauri WebView2）本地连接下导出 HTML/Markdown 正常下载
- 远程连接行为不变

---

### 4. About 模态框新增 Bow 作者归属

**功能描述：**
在 About 模态框的作者行下方新增一行 "Bow (Codefree)" 归属。

**修改文件：**

- `frontend/src/lib/components/modals/AboutModal.svelte`
  - 在 "Kenn Software LLC" 作者行后新增空 label 行，值为 "Bow (Codefree)"

---

### 5. CI 工作流定制

**功能描述：**
禁用多个 CI 工作流的自动触发（push/PR/schedule），仅保留 `workflow_dispatch` 手动触发；新增独立的 `desktop-artifacts-bow.yml` 工作流，构建矩阵精简为 Windows + macOS aarch64，支持传入 `version` 输入。

**修改文件：**

- `.github/workflows/msys2-update-check.yml`
  - 注释掉 `schedule` cron 触发，保留 `workflow_dispatch`

- `.github/workflows/desktop-artifacts.yml`
  - 注释掉 `push` 自动触发
  - 注释掉 Linux 和 Linux (arm64) 矩阵项，仅保留 Windows

- `.github/workflows/desktop-artifacts-pr.yml`
  - 注释掉 `pull_request` 自动触发，改为 `workflow_dispatch`

- `.github/workflows/desktop-macos-main.yml`
  - 注释掉 `push` 自动触发，改为 `workflow_dispatch`

- `.github/workflows/desktop-artifacts-bow.yml`（新增文件）
  - 独立的 Desktop 构建工作流，仅 `workflow_dispatch` 触发
  - 矩阵：Windows（windows-latest, nsis）+ macOS aarch64（macos-15, app, aarch64-apple-darwin）
  - 支持 `version` 输入注入 `AGENTSVIEW_VERSION`
  - 复用 `.github/actions/build-desktop-artifact` action

---

### 6. .gitignore 新增条目

**功能描述：**
忽略 AI 工具配置目录和本地 Codefree 工作目录。

**修改文件：**

- `.gitignore`
  - 新增 `.opencode/`
  - 新增 `opencode.json`
  - 新增 `.codefree/`

---

### 7. 文档与 OpenSpec 归档

**功能描述：**
恢复 `BOW-CHANGES.md` 变更日志，以及 codefree-o agent 和 desktop 构建版本注入两个特性的 OpenSpec 规格文档，用于追溯定制功能的来源和设计依据。

**修改文件（新增）：**

- `BOW-CHANGES.md`
- `openspec/config.yaml`
- `openspec/specs/codefree-o-agent/spec.md`
- `openspec/specs/build-version-injection/spec.md`
- `openspec/changes/archive/2026-05-19-add-codefree-o-support/`（design.md、proposal.md、tasks.md、specs/codefree-o-agent/spec.md、.openspec.yaml）
- `openspec/changes/archive/2026-05-19-fix-desktop-artifact-version/`（design.md、proposal.md、tasks.md、specs/build-version-injection/spec.md、.openspec.yaml）

---

### 本次未迁移的 bow-0.29.0 定制（v0.43.0 已有等价实现或不再适用）

| bow-0.29.0 定制 | 未迁移原因 |
|------|------|
| OpenCode 工具输出提取（`ParsedToolResult`/`ToolResults`） | v0.43.0 的 `opencode.go` 已通过 `ResultEvents` 机制实现工具输出提取 |
| HTML 导出结构化工具调用块（`<details>` + `exportToolCall`） | v0.43.0 的 `export.go` 已通过 `toolBlockRe` 正则解析消息内容渲染工具块 |
| `db.go` dataVersion 27 → 28 | v0.43.0 已是 dataVersion 108，无需再 bump |
| `AGENTS.md` → `AGENTS.md.NA` 重命名 | v0.43.0 的 AGENTS.md 内容已演进，不再适用 |

---

## 变更总结

| 功能 | 影响文件数 | 新增测试 | 需要重新同步 |
|------|-----------|---------|-------------|
| Codefree-O Agent | 16 | 复用 OpenCode 家族测试 | ✗ |
| 禁用启动更新检查 | 2 | 0 | ✗ |
| Tauri WebView2 导出下载 | 1 | 0 | ✗ |
| About Bow 作者归属 | 1 | 0 | ✗ |
| CI 工作流定制 | 5 | 0 | ✗ |
| .gitignore 新增 | 1 | 0 | ✗ |
| 文档与 OpenSpec | 14 | 0 | ✗ |

**部署说明：**
1. 重新编译 agentsview：`go build -o agentsview.exe ./cmd/agentsview`
2. 重启服务，Codefree-O 会话将被自动发现并同步
3. 桌面端需重新构建以应用启动更新检查和导出下载的修改

