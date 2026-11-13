# AgentsView PRD

## 1. 元数据

| 项 | 内容 |
|---|---|
| PRD ID | AV-CODEFREE-LABEL |
| 版本 | 1.1.0 |
| 权威源 | repo |
| 工作项 | srdcloud-001 |
| 状态 | Draft |

## 2. 业务背景与目标

AgentsView 解析本地 AI agent 会话并展示在 Web UI 中。每个 provider 有一个显示名
（AgentLabel），用于在会话列表和详情中标识会话来源。CodeFree IDE provider
（`AgentCodeFree`）的 agent URI 包含路径段（如 `spec`、`sdd-dev-engineer`、
`explore`），这些路径段是内部数字员工类型标识符，不是用户可见的显示名称。

此前 `codefreeAgentLabel` 函数从 agent URI 路径段提取标签作为 `AgentLabel`，导致
UI 显示 "SPEC" 或 "EXPLORE" 等不友好的内部标识，而非 "CodeFree"。

目标：移除路径段提取逻辑，将 `AgentLabel` 留空，由前端 `KNOWN_AGENTS` 注册的
`codefree` 条目提供统一显示名 "CodeFree"。

## 3. 干系人与用户角色

| 角色 | 诉求 | 使用场景 |
|---|---|---|
| AgentsView 用户 | 在 UI 中看到 CodeFree 会话显示为 "CodeFree" 而非内部类型标识 | 浏览会话列表、查看会话详情 |
| AgentsView 开发者 | parser 层不暴露内部数字员工类型标识符作为显示名 | 维护 parser 和前端显示逻辑 |

## 4. 业务范围

- In-Scope：CodeFree provider（`AgentCodeFree`）的 `AgentLabel` 赋值逻辑和前端
  `KNOWN_AGENTS` 注册；CodeFree provider 的消息时间戳填充（从 `agenthost.log`
  提取真实 per-turn 时间戳，缺失时用相邻 turn 插值或 session 级时间兜底）。
- Out-of-Scope：`AgentCodefreeO`（OpenCode fork，独立 provider）的显示名逻辑；
  其他 provider 的 AgentLabel 处理；CodeFree provider 的 SQLite 读取和 token
  统计等已有功能；其他 provider 的时间戳处理。

## 5. 核心业务流程

1. CodeFree IDE 产生会话数据，agent URI 包含路径段（如
   `codefree://spec/...`、`codefree://sdd-dev-engineer/...`）。
2. AgentsView parser 读取会话，不再从 URI 路径段提取 AgentLabel，留空。
3. 前端渲染时，`KNOWN_AGENTS` 中 `codefree` 条目提供 label "CodeFree"，
   作为 fallback 显示名。
4. parser 加载会话时，扫描 `agentSessionData` 的兄弟目录 `logs/` 下的
   `agenthost.log` 文件，提取每个 turn 的 "Turn started" 时间戳。
5. 日志中找到的 turn 使用真实时间戳；缺失的 turn 在相邻已知 turn 之间线性
   插值；全部缺失时用 session 的 startTime 和 modifiedTime 均匀插值。

## 6. 功能需求列表

| FEAT编号 | 需求点 | 业务验收描述 | 归属候选 |
|---|---|---|---|
| FEAT-001 | 移除 CodeFree provider AgentLabel 路径段提取 | `internal/parser/codefree.go` 中不再从 agent URI 路径段提取 AgentLabel，`AgentLabel` 字段为空字符串 | internal/parser |
| FEAT-002 | 前端注册 CodeFree 显示名 | `frontend/src/lib/utils/agents.ts` 的 `KNOWN_AGENTS` 列表包含 `codefree` 条目，label 为 "CodeFree" | frontend |
| FEAT-003 | CodeFree 会话消息时间戳从 agenthost.log 提取 | parser 加载 CodeFree 会话时，从 `agenthost.log` 提取每个 turn 的真实开始时间戳；日志缺失时用相邻已知 turn 线性插值；全部缺失时用 session 级时间均匀插值 | internal/parser |

## 7. 非功能需求

- 不引入新依赖。
- 不改变 CodeFree provider 的会话解析、SQLite 读取和 token 统计行为。
- 不影响其他 provider 的 AgentLabel 逻辑。
- 日志扫描是 best-effort：单个日志文件读取失败不阻止会话解析。
- 时间戳填充不影响会话解析的正确性和完整性。

## 8. 约束与依赖

- `AgentCodefreeO`（OpenCode fork）是独立 provider，有自己的 `KNOWN_AGENTS`
  条目，不受本次变更影响。
- `codefree` 和 `codefree-o` 在 `KNOWN_AGENTS` 中共存。

> 本项目只保留这一份`docs/prd.md`并原地修改；可执行AC不得在PRD与Spec双写。

## 9. 变更历史

| 日期 | 版本 | 变更内容 | 作者 |
|---|---|---|---|
| 2026-11-10 | 1.0.0 | 初始版本：FEAT-001 AgentLabel 修复、FEAT-002 前端注册 | srd18901883260 |
| 2026-11-11 | 1.1.0 | 新增 FEAT-003：CodeFree 会话消息时间戳从 agenthost.log 提取 | srd18901883260 |
