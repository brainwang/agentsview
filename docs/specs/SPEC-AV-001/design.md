# CodeFree Provider AgentLabel 修复与前端注册 设计文档（Design）

## 1. 元数据

| 项 | 内容 |
|---|---|
| Spec ID | SPEC-AV-001 |
| 关联Spec | `docs/specs/SPEC-AV-001/spec.md` |
| 作者 | srd18901883260 |
| 创建日期 | 2026-11-10 |
| 状态 | Draft |
| 版本号 | 1.0.0 |
| 评审人 | srd18901883260 |

## 2. 设计目标

- 核心问题：CodeFree provider 的 `AgentLabel` 从 agent URI 路径段提取内部数字员工类型标识符（如 `spec`、`sdd-dev-engineer`），导致前端 UI 显示不友好的内部标识而非用户可读的 "CodeFree"。
- 设计范围：修改 `internal/parser/codefree.go` 中 `AgentLabel` 赋值逻辑（移除路径段提取，设为空字符串）；在 `frontend/src/lib/utils/agents.ts` 的 `KNOWN_AGENTS` 列表注册 `codefree` 条目，提供 label "CodeFree" 作为前端 fallback 显示名。
- 非设计范围：`AgentCodefreeO`（OpenCode fork）的显示名逻辑；CodeFree provider 的会话解析、SQLite 读取和 token 统计等已有功能；其他 provider 的 AgentLabel 处理。

## 3. 方案概述

- 高层方案：在 parser 层移除 `codefreeAgentLabel` 函数及其 `path` 包导入，将 `AgentLabel` 设为空字符串；在前端 `KNOWN_AGENTS` 列表新增 `codefree` 条目，利用现有 `agentLabel()` 函数的 fallback 机制（空 override 时按 name 匹配 KNOWN_AGENTS 的 label）提供用户可读显示名。
- 关键决策：不在 parser 层硬编码 "CodeFree" 作为 AgentLabel，而是留空并依赖前端 fallback。理由是 parser 层不应承担显示名职责，显示名属于前端关注点；且 `KNOWN_AGENTS` 已有成熟的 fallback 机制。详见 ADR-001。
- 方案对比：
  - 方案A（采纳）：移除路径段提取，AgentLabel 留空，前端 KNOWN_AGENTS fallback。parser 层职责清晰，前端统一管理显示名。
  - 方案B（未采纳）：在 parser 层硬编码 "CodeFree" 作为 AgentLabel。缺点是 parser 层承担显示名职责，与现有架构不一致（其他 provider 的 label 由前端管理），且 "CodeFree" 是显示文案，不应固化在 Go 代码中。

## 4. Spec覆盖关系

| FR/AC | 设计章节/模块 | 实现方式 | 验证方式 |
|---|---|---|---|
| FR-001/AC-001-1 | 第5.2节 Parser模块 | 移除 `codefreeAgentLabel` 函数，`AgentLabel` 设为空字符串 | 单元测试 `TestLoadCodeFreeSession_UserAndAssistant`、`TestCodeFreeProvider_Parse` 验证 AgentLabel 为空 |
| FR-001/AC-001-2 | 第5.2节 Parser模块 | 移除 `path` 包导入 | `go vet ./internal/parser/` 无未使用导入错误 |
| FR-002/AC-002-1 | 第5.2节 前端模块 | `KNOWN_AGENTS` 新增 `{ name: "codefree", color: "var(--accent-purple)", label: "CodeFree" }` | 前端代码审查确认条目存在；`agentLabel("codefree")` 返回 "CodeFree" |

## 5. 系统架构

### 5.1 架构图

本次变更不改变整体架构。变更涉及两个独立模块：

```
CodeFree IDE 会话数据 (SQLite)
    |
    v
internal/parser/codefree.go  (Parser层)
    |  AgentLabel: "" (空字符串，不再提取路径段)
    v
ParsedSession -> API -> Frontend
    |
    v
frontend/src/lib/utils/agents.ts  (前端层)
    |  agentLabel("codefree", "") -> KNOWN_AGENTS fallback -> "CodeFree"
    v
UI 显示: "CodeFree"
```

### 5.2 模块划分

| 模块/服务 | 职责 | 输入 | 输出 | 依赖 |
|---|---|---|---|---|
| `internal/parser/codefree.go` | 解析 CodeFree 会话数据，构建 ParsedSession | CodeFree SQLite 会话数据 | ParsedSession（AgentLabel 为空字符串） | `gjson`、`database/sql` |
| `frontend/src/lib/utils/agents.ts` | 提供 agent 显示名、颜色和 fallback 逻辑 | agent name、可选 override | 显示名字符串 | 无外部依赖 |

### 5.3 模块间交互

Parser 层产出 `ParsedSession`，其中 `AgentLabel` 为空字符串。前端通过 API 获取会话数据后，调用 `agentLabel(agent, override)` 函数：当 `override`（即 `AgentLabel`）为空时，按 `agent` name 在 `KNOWN_AGENTS` 中查找匹配条目，返回其 `label`。对于 `codefree` 条目，返回 "CodeFree"。

## 6. 数据模型

- 实体定义：不涉及（不新增或修改数据库实体）
- 数据库变更：不涉及
- 事务边界：不涉及
- 迁移方案：不涉及

## 7. 接口契约

不涉及接口新增或变更。本次变更仅修改 parser 内部赋值逻辑和前端静态列表，不改变 API 请求/响应结构。

- 请求/响应结构：不涉及
- 错误码：不涉及
- 兼容性：`AgentLabel` 从非空路径段变为空字符串，前端已有 fallback 机制处理空 override 的情况，向后兼容
- 契约文件：不涉及

## 8. 认证、授权、幂等与安全

- 认证：不涉及（本次变更不涉及认证逻辑）
- 授权：不涉及（本次变更不涉及授权逻辑）
- 幂等：不涉及（本次变更不涉及重复请求处理）
- 敏感数据：本次变更移除了从 agent URI 路径段提取内部数字员工类型标识符的逻辑，避免在 UI 中暴露内部类型标识（如 `spec`、`sdd-dev-engineer`），属于安全改善
- 审计：不涉及（本次变更不涉及审计逻辑）

## 9. 关键决策ADR

- 引用：`docs/specs/SPEC-AV-001/adrs/ADR-001.md`
- 判断依据：移除路径段提取并依赖前端 fallback 的方案涉及 parser 层与前端层的职责划分决策，需要记录替代方案和选择理由

## 10. 非功能需求设计

- 性能设计：移除 `codefreeAgentLabel` 函数后，parser 不再执行 URI 路径段提取操作，性能不退化，略有改善（减少一次 `path.Base` 调用和字符串处理）。
- 可用性设计：现有 CodeFree provider 的会话解析和 SQLite 读取功能不受影响，现有单元测试 `TestLoadCodeFreeSession_*` 和 `TestCodeFreeProvider_Parse` 验证功能完整性。
- 可观测性：不涉及（本次变更不涉及日志、指标或追踪变更）

## 11. 依赖与风险

| 风险/依赖 | 影响 | 缓解措施 | 负责人 |
|---|---|---|---|
| 前端 `KNOWN_AGENTS` fallback 逻辑假设 | 若 `agentLabel()` 函数在 override 为空时不使用 KNOWN_AGENTS fallback，则 CodeFree 会话可能显示 agent name 而非 "CodeFree" | 已确认 `agentLabel()` 函数逻辑：`override?.trim()` 为 falsy 时按 name 查找 KNOWN_AGENTS，假设成立 | srd18901883260 |
| `codefree` 和 `codefree-o` 在 KNOWN_AGENTS 中共存 | 两个条目使用相同颜色 `var(--accent-purple)`，可能影响视觉区分 | `codefree-o` 已有独立 label "Codefree-O"，且两者 name 不同，前端按 name 精确匹配，不会混淆 | srd18901883260 |

## 12. 回滚方案

- 触发条件：变更后 CodeFree 会话在 UI 中显示异常（如显示空字符串或 agent name 而非 "CodeFree"），或 parser 测试失败
- 回滚步骤：恢复 `codefreeAgentLabel` 函数和 `path` 包导入，恢复 `AgentLabel` 赋值为路径段提取；移除 `KNOWN_AGENTS` 中的 `codefree` 条目
- 数据恢复：不涉及数据变更，无需数据恢复

## 13. 验收标准

- [x] 技术方案覆盖全部适用FR和AC（FR-001/AC-001-1、AC-001-2、FR-002/AC-002-1）
- [x] 接口契约已明确不涉及
- [x] 数据模型、迁移和回滚方案完整或已明确不涉及
- [x] 认证、授权、幂等、敏感数据和审计方案完整或已明确不涉及
- [x] 风险、缓解措施和回滚方案已明确

## 14. AI专项设计（按产品类型启用）

- 模型选型论证：不适用（传统·L1）
- 评估方案：不适用
- 提示注入与内容安全：不适用
- 数据合规与备案：不适用
- 全程留痕与回放：不适用
- 日志安全：不适用
- 多智能体协同：不适用

## 15. 变更历史

| 日期 | 版本 | 变更内容 | 作者 |
|---|---|---|---|
| 2026-11-10 | 1.0.0 | 初始版本 | srd18901883260 |

## 16. 自检清单（评审辅助，不作Gate依据）

- [x] Spec ID与关联Spec路径有效且一致。
- [x] 设计目标和方案概述已完整填写，不以"简单实现"作为豁免理由。
- [x] Spec覆盖关系包含全部适用FR/AC，且引用真实存在。
- [x] 方案对比、关键决策及ADR引用完整，或已说明无需新增ADR的依据。
- [x] 数据模型、接口契约、认证授权、幂等、安全、依赖、风险和回滚均已填写或明确不涉及。
- [x] 验收方式与后续Tasks可执行、可验证。

> 本文件原地修改；整个项目只保留这一份`design.md`并与唯一`spec.md`同目录，禁止创建第二套或任何版本副本。
