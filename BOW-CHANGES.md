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
