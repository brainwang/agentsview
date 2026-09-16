## Why

codefree-o 是基于 opencode 的修改版，使用完全相同的会话文件格式，但默认目录和 SQLite 数据库路径不同。当前 agentsview 不支持读取和显示 codefree-o 的会话数据。

## What Changes

- 新增 `codefree-o` 作为独立 agent 类型，注册到 AgentDef 列表
- 创建 `codefree_o.go`：源文件发现、路径解析、watch root 函数（复用 opencode 的解析逻辑）
- 在 sync engine 中添加 `AgentCodefreeO` 的 dispatch 分支（~15 处）
- 前端添加 codefree-o 的 agent 颜色、标签、恢复命令
- 设置 TOML 配置键 `codefree_o_dirs` 和环境变量 `CODEFREE_O_DIR`

## Capabilities

### New Capabilities
- `codefree-o-agent`: 将 codefree-o 注册为 agentsview 支持的 agent 类型，包括文件发现、会话解析、同步引擎 dispatch、前端展示的完整链路

### Modified Capabilities

无

## Impact

- `internal/parser/types.go` — 新增 `AgentCodefreeO` 常量和 Registry 条目
- `internal/parser/codefree_o.go` — 新文件，发现/源查找/路径解析函数
- `internal/sync/engine.go` — 约 15 处 switch/if 分支需添加 `AgentCodefreeO`
- `frontend/src/lib/utils/agents.ts` — 新增颜色和标签
- `frontend/src/lib/utils/resume.ts` — 新增恢复命令
- `frontend/src/lib/components/settings/AgentDirSettings.svelte` — 新增标签
