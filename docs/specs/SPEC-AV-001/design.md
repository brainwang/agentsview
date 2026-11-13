# CodeFree Provider AgentLabel 修复、前端注册与消息时间戳提取 设计文档（Design）

## 1. 元数据

| 项 | 内容 |
|---|---|
| Spec ID | SPEC-AV-001 |
| 关联Spec | `docs/specs/SPEC-AV-001/spec.md` |
| 作者 | srd18901883260 |
| 创建日期 | 2026-11-10 |
| 状态 | Draft |
| 版本号 | 1.1.0 |
| 评审人 | srd18901883260 |

## 2. 设计目标

- 核心问题1：CodeFree provider 的 `AgentLabel` 从 agent URI 路径段提取内部数字员工类型标识符（如 `spec`、`sdd-dev-engineer`），导致前端 UI 显示不友好的内部标识而非用户可读的 "CodeFree"。
- 核心问题2：CodeFree 会话的 state JSON 只有 session 级 `startTime` 和 `modifiedTime`，没有 per-turn 或 per-message 时间戳，导致 UI 中所有消息显示同一个时间，无法区分消息先后顺序。
- 设计范围：修改 `internal/parser/codefree.go` 中 `AgentLabel` 赋值逻辑（移除路径段提取，设为空字符串）；在 `frontend/src/lib/utils/agents.ts` 的 `KNOWN_AGENTS` 列表注册 `codefree` 条目，提供 label "CodeFree" 作为前端 fallback 显示名；新增 `internal/parser/codefree_logs.go` 扫描 `agenthost.log` 提取 per-turn 真实时间戳，通过 `history.turnId` 匹配填充到消息，缺失时按三级策略兜底。
- 非设计范围：`AgentCodefreeO`（OpenCode fork）的显示名逻辑；CodeFree provider 的会话解析、SQLite 读取和 token 统计等已有功能；其他 provider 的 AgentLabel 和时间戳处理；前端时间戳显示格式化逻辑。

## 3. 方案概述

- 高层方案1（FR-001/FR-002）：在 parser 层移除 `codefreeAgentLabel` 函数及其 `path` 包导入，将 `AgentLabel` 设为空字符串；在前端 `KNOWN_AGENTS` 列表新增 `codefree` 条目，利用现有 `agentLabel()` 函数的 fallback 机制（空 override 时按 name 匹配 KNOWN_AGENTS 的 label）提供用户可读显示名。
- 高层方案2（FR-003）：新增独立模块 `codefree_logs.go`，在 parser 加载会话时扫描 `agentSessionData` 的兄弟目录 `logs/` 下的 `agenthost.log` 文件，用正则匹配 "Turn started" 行提取 `session=会话ID` 和 `turn=轮次ID` 字段及行首时间戳，构建 `sessionID/turnId` 到时间戳的映射。然后在 `codefree.go` 的 `parseCodeFreeStateJSON` 中收集所有 turn 的 `history.turnId`，调用 `resolveTurnTimestamps` 按三级策略填充：1) 日志真实时间戳；2) 相邻已知 turn 线性插值；3) session 级时间均匀插值。
- 关键决策1：不在 parser 层硬编码 "CodeFree" 作为 AgentLabel，而是留空并依赖前端 fallback。理由是 parser 层不应承担显示名职责，显示名属于前端关注点；且 `KNOWN_AGENTS` 已有成熟的 fallback 机制。详见 ADR-001。
- 关键决策2：时间戳填充采用三级兜底策略（日志真实值 → 相邻线性插值 → session 级均匀插值），而非仅用 session 级均匀分布或仅用日志值。理由是日志可能部分缺失或全部缺失，需要保证所有情况下都有合理的时间戳。详见 ADR-002。
- 方案对比（FR-003）：
  - 方案A（采纳）：三级兜底策略。日志有值用日志值，部分缺失用相邻线性插值，全部缺失用 session 级均匀插值。覆盖所有场景，时间戳精度从高到低优雅降级。
  - 方案B（未采纳）：仅用日志值，缺失时用 session startTime。缺点是部分缺失场景下大量 turn 挤在同一个时间点，无法区分先后。
  - 方案C（未采纳）：仅用 session 级均匀插值，不扫描日志。缺点是放弃真实时间戳精度，所有时间戳都是估算值。

## 4. Spec覆盖关系

| FR/AC | 设计章节/模块 | 实现方式 | 验证方式 |
|---|---|---|---|
| FR-001/AC-001-1 | 第5.2节 Parser模块 | 移除 `codefreeAgentLabel` 函数，`AgentLabel` 设为空字符串 | 单元测试 `TestLoadCodeFreeSession_UserAndAssistant`、`TestCodeFreeProvider_Parse` 验证 AgentLabel 为空 |
| FR-001/AC-001-2 | 第5.2节 Parser模块 | 移除 `path` 包导入 | `go vet ./internal/parser/` 无未使用导入错误 |
| FR-002/AC-002-1 | 第5.2节 前端模块 | `KNOWN_AGENTS` 新增 `{ name: "codefree", color: "var(--accent-purple)", label: "CodeFree" }` | 前端代码审查确认条目存在；`agentLabel("codefree")` 返回 "CodeFree" |
| FR-003/AC-003-1 | 第5.2节 日志扫描模块 | `loadCodeFreeLogTimestamps` 扫描 `agenthost.log`，正则匹配 "Turn started" 行提取时间戳，`resolveTurnTimestamps` Pass 1 使用日志真实时间戳 | 单元测试 `TestResolveTurnTimestamps_AllFromLog`、`TestResolveTurnTimestamps_PartialFromLog` 验证日志时间戳填充 |
| FR-003/AC-003-2 | 第5.2节 时间戳解析模块 | `resolveTurnTimestamps` Pass 2 对缺失 turn 在相邻已知 turn 之间线性插值 | 单元测试 `TestResolveTurnTimestamps_InteriorGap`、`TestResolveTurnTimestamps_LeadingGap`、`TestResolveTurnTimestamps_TrailingGap` 验证插值行为 |
| FR-003/AC-003-3 | 第5.2节 时间戳解析模块 | `resolveTurnTimestamps` Pass 3 在无日志时间戳时用 session startTime 和 modifiedTime 均匀插值 | 单元测试 `TestResolveTurnTimestamps_NoLogEvenDistribution`、`TestResolveTurnTimestamps_NoLogSingleTurn` 验证兜底策略 |

## 5. 系统架构

### 5.1 架构图

本次变更不改变整体架构。变更涉及三个独立模块：

```
CodeFree IDE 会话数据 (SQLite + agenthost.log)
    |
    +-- agentSessionData/<sessionID>/session.db
    |       |
    |       v
    |   internal/parser/codefree.go  (Parser层)
    |       |  AgentLabel: "" (空字符串)
    |       |  收集 history.turnId 列表
    |       |  调用 resolveTurnTimestamps 填充 per-turn 时间戳
    |       v
    |   ParsedSession + ParsedMessage[] (每条消息携带 Timestamp)
    |
    +-- logs/<yyyyMMddTHHmmss>/agenthost.log
            |
            v
        internal/parser/codefree_logs.go  (日志扫描层)
            |  loadCodeFreeLogTimestamps: 扫描所有日志目录
            |  正则匹配 "Turn started session=... turn=..." 行
            |  构建 sessionID/turnId -> timestamp 映射
            |  resolveTurnTimestamps: 三级策略填充
            v
        codefreeLogTimestampMap

ParsedSession -> API -> Frontend
    |
    v
frontend/src/lib/utils/agents.ts  (前端层)
    |  agentLabel("codefree", "") -> KNOWN_AGENTS fallback -> "CodeFree"
    |  消息列表按 Timestamp 显示时间
    v
UI 显示: "CodeFree" + per-turn 时间戳
```

### 5.2 模块划分

| 模块/服务 | 职责 | 输入 | 输出 | 依赖 |
|---|---|---|---|---|
| `internal/parser/codefree.go` | 解析 CodeFree 会话数据，构建 ParsedSession 和 ParsedMessage | CodeFree SQLite 会话数据、`codefreeLogTimestampMap` | ParsedSession（AgentLabel 为空字符串，消息携带 per-turn Timestamp） | `gjson`、`database/sql`、`codefree_logs.go` |
| `internal/parser/codefree_logs.go` | 扫描 `agenthost.log` 提取 per-turn 时间戳，三级策略填充 | `agentSessionData` root 路径、sessionID、turnIDs 列表、session 起止时间 | `codefreeLogTimestampMap`、`[]time.Time` | `bufio`、`regexp`、`os`、`path/filepath`、`sort`、`time` |
| `frontend/src/lib/utils/agents.ts` | 提供 agent 显示名、颜色和 fallback 逻辑 | agent name、可选 override | 显示名字符串 | 无外部依赖 |

### 5.3 模块间交互

**FR-001/FR-002 交互**：Parser 层产出 `ParsedSession`，其中 `AgentLabel` 为空字符串。前端通过 API 获取会话数据后，调用 `agentLabel(agent, override)` 函数：当 `override`（即 `AgentLabel`）为空时，按 `agent` name 在 `KNOWN_AGENTS` 中查找匹配条目，返回其 `label`。对于 `codefree` 条目，返回 "CodeFree"。

**FR-003 交互**：`codefreeProvider.parseSQLiteSession` 调用 `loadCodeFreeSession` 时传入 `src.Root`（`agentSessionData` 目录路径）。`loadCodeFreeSession` 在读取 state JSON 前调用 `loadCodeFreeLogTimestamps(root)` 扫描日志目录，获得 `codefreeLogTimestampMap`。随后 `parseCodeFreeStateJSON` 收集所有 turn 的 `history.turnId`（按 completedTurns 然后 inFlightTurns 顺序），调用 `resolveTurnTimestamps(logTS, sessionID, allTurnIDs, sess.StartedAt, sess.EndedAt)` 一次性解析全部 turn 的时间戳。每个 turn 的消息（user message + assistant message）共享该 turn 的时间戳。

**数据流**：
1. `codefreeProvider.Parse` -> `parseSQLiteSession` -> `loadCodeFreeSession(ctx, db, dbPath, sessionID, machine, root)`
2. `loadCodeFreeSession` -> `loadCodeFreeLogTimestamps(root)` -> 返回 `codefreeLogTimestampMap`
3. `loadCodeFreeSession` -> `parseCodeFreeStateJSON(ctx, stateJSON, sessionID, machine, logTS)`
4. `parseCodeFreeStateJSON` 收集 `allTurnIDs` -> `resolveTurnTimestamps(logTS, sessionID, allTurnIDs, startTime, endTime)` -> `[]time.Time`
5. 每个 turn 的消息使用对应索引的 `turnTime` 作为 `Timestamp`

## 6. 数据模型

- 实体定义：不涉及（不新增或修改数据库实体）
- 数据库变更：不涉及
- 事务边界：不涉及
- 迁移方案：不涉及
- 内存数据结构（FR-003）：
  - `codefreeTurnTimestamps`：持有单个 turn 的 `Started` 和 `Terminal` 时间戳（`Terminal` 可能为零值）
  - `codefreeLogTimestampMap`：`map[string]codefreeTurnTimestamps`，key 为 `sessionID/turnId`
  - `resolveTurnTimestamps` 返回 `[]time.Time`，索引与输入 `turnIDs` 切片一一对应

## 7. 接口契约

不涉及接口新增或变更。本次变更仅修改 parser 内部赋值逻辑和前端静态列表，不改变 API 请求/响应结构。

- 请求/响应结构：不涉及
- 错误码：不涉及
- 兼容性：
  - FR-001/FR-002：`AgentLabel` 从非空路径段变为空字符串，前端已有 fallback 机制处理空 override 的情况，向后兼容
  - FR-003：`ParsedMessage.Timestamp` 字段从 session 级统一时间变为 per-turn 精确时间戳，API 响应结构不变，前端按已有逻辑读取 `Timestamp` 字段，向后兼容。时间戳精度提升对前端透明，无需前端配合修改
- 契约文件：不涉及

## 8. 认证、授权、幂等与安全

- 认证：不涉及（本次变更不涉及认证逻辑）
- 授权：不涉及（本次变更不涉及授权逻辑）
- 幂等：不涉及（本次变更不涉及重复请求处理）
- 敏感数据：
  - FR-001/FR-002：本次变更移除了从 agent URI 路径段提取内部数字员工类型标识符的逻辑，避免在 UI 中暴露内部类型标识（如 `spec`、`sdd-dev-engineer`），属于安全改善
  - FR-003：日志扫描仅读取 `agenthost.log` 文件中的 "Turn started" 行，提取时间戳和 session/turn 标识符。不读取或解析日志中的其他内容（如用户输入、模型输出、错误堆栈），不将日志原始内容写入 ParsedSession 或 API 响应。日志文件路径通过 `agentSessionData` root 的兄弟目录推导，不接受外部传入的任意路径
- 审计：不涉及（本次变更不涉及审计逻辑）
- 日志文件读取安全（FR-003）：
  - 日志扫描是 best-effort 操作：单个日志文件读取失败不阻止会话解析，返回空映射并降级到 session 级均匀插值
  - 日志文件不存在或目录不可访问时静默跳过，不产生错误日志或 panic
  - 正则匹配仅提取时间戳和数字/字母标识符，不解析自由文本内容

## 9. 关键决策ADR

- ADR-001：`docs/specs/SPEC-AV-001/adrs/ADR-001.md`
  - 判断依据：移除路径段提取并依赖前端 fallback 的方案涉及 parser 层与前端层的职责划分决策，需要记录替代方案和选择理由
- ADR-002：`docs/specs/SPEC-AV-001/adrs/ADR-002.md`
  - 判断依据：时间戳填充采用三级兜底策略（日志真实值 → 相邻线性插值 → session 级均匀插值）涉及精度与鲁棒性的权衡决策，需要记录替代方案和选择理由

## 10. 非功能需求设计

- 性能设计：
  - FR-001/FR-002：移除 `codefreeAgentLabel` 函数后，parser 不再执行 URI 路径段提取操作，性能不退化，略有改善（减少一次 `path.Base` 调用和字符串处理）
  - FR-003：日志扫描在会话加载时执行一次，扫描 `logs/` 目录下所有 `agenthost.log` 文件。典型日志文件大小为数百 KB 到数 MB，正则匹配逐行处理，内存占用为映射表大小（与 turn 数量成正比，典型场景下不超过数百条目）。日志扫描不阻塞 SQLite 读取，两者顺序执行但各自独立。单个日志文件读取失败不阻止整体解析
- 可用性设计：现有 CodeFree provider 的会话解析和 SQLite 读取功能不受影响，现有单元测试 `TestLoadCodeFreeSession_*` 和 `TestCodeFreeProvider_Parse` 验证功能完整性。FR-003 的日志扫描是 best-effort，日志缺失时降级到插值策略，不影响会话解析成功率
- 可观测性：不涉及（本次变更不涉及日志、指标或追踪变更）

## 11. 依赖与风险

| 风险/依赖 | 影响 | 缓解措施 | 负责人 |
|---|---|---|---|
| 前端 `KNOWN_AGENTS` fallback 逻辑假设 | 若 `agentLabel()` 函数在 override 为空时不使用 KNOWN_AGENTS fallback，则 CodeFree 会话可能显示 agent name 而非 "CodeFree" | 已确认 `agentLabel()` 函数逻辑：`override?.trim()` 为 falsy 时按 name 查找 KNOWN_AGENTS，假设成立 | srd18901883260 |
| `codefree` 和 `codefree-o` 在 KNOWN_AGENTS 中共存 | 两个条目使用相同颜色 `var(--accent-purple)`，可能影响视觉区分 | `codefree-o` 已有独立 label "Codefree-O"，且两者 name 不同，前端按 name 精确匹配，不会混淆 | srd18901883260 |
| FR-003 日志文件格式变更 | 若 CodeFree 后续版本修改 `agenthost.log` 的 "Turn started" 行格式，正则匹配将失效 | 正则匹配容忍格式变化（使用灵活的空白和字段顺序匹配）；匹配失败时降级到 session 级均匀插值，不影响功能可用性 | srd18901883260 |
| FR-003 日志目录不存在或为空 | 新安装或清理日志后可能无日志文件 | `loadCodeFreeLogTimestamps` 返回空映射，`resolveTurnTimestamps` 降级到 session 级均匀插值，功能不受影响 | srd18901883260 |
| FR-003 turnId 在日志和 state JSON 间不匹配 | 日志中的 `turn=` 值与 state JSON 中 `history.turnId` 的类型或格式不一致 | 两者均为字符串类型，`codefreeLogTimestampMap` 的 key 为 `sessionID/turnId` 字符串拼接，匹配时统一转为字符串 | srd18901883260 |

## 12. 回滚方案

- FR-001/FR-002 回滚：
  - 触发条件：变更后 CodeFree 会话在 UI 中显示异常（如显示空字符串或 agent name 而非 "CodeFree"），或 parser 测试失败
  - 回滚步骤：恢复 `codefreeAgentLabel` 函数和 `path` 包导入，恢复 `AgentLabel` 赋值为路径段提取；移除 `KNOWN_AGENTS` 中的 `codefree` 条目
  - 数据恢复：不涉及数据变更，无需数据恢复
- FR-003 回滚：
  - 触发条件：变更后 CodeFree 会话消息时间戳显示异常（如全部为零值、时间顺序错乱、或日志扫描导致解析性能显著退化），或新增测试失败
  - 回滚步骤：移除 `codefree_logs.go` 文件；在 `codefree.go` 中移除 `loadCodeFreeLogTimestamps` 调用和 `resolveTurnTimestamps` 调用，恢复消息时间戳为 session 级统一时间（`sess.StartedAt`）；在 `codefree_provider.go` 中移除 `root` 参数传递
  - 数据恢复：不涉及数据变更，无需数据恢复

## 13. 验收标准

- [x] 技术方案覆盖全部适用FR和AC（FR-001/AC-001-1、AC-001-2、FR-002/AC-002-1、FR-003/AC-003-1、AC-003-2、AC-003-3）
- [x] 接口契约已明确不涉及，兼容性说明覆盖 FR-001/FR-002 和 FR-003
- [x] 数据模型、迁移和回滚方案完整或已明确不涉及
- [x] 认证、授权、幂等、敏感数据和审计方案完整或已明确不涉及，日志文件读取安全已补充
- [x] 风险、缓解措施和回滚方案已明确，覆盖 FR-001/FR-002 和 FR-003
- [x] ADR 引用完整：ADR-001 覆盖 FR-001/FR-002 职责划分决策，ADR-002 覆盖 FR-003 三级兜底策略决策

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
| 2026-11-11 | 1.1.0 | 新增 FR-003 消息时间戳提取设计：日志扫描模块、三级兜底策略、ADR-002、安全/性能/风险/回滚补充 | srd18901883260 |

## 16. 自检清单（评审辅助，不作Gate依据）

- [x] Spec ID与关联Spec路径有效且一致。
- [x] 设计目标和方案概述已完整填写，不以"简单实现"作为豁免理由。
- [x] Spec覆盖关系包含全部适用FR/AC（FR-001、FR-002、FR-003），且引用真实存在。
- [x] 方案对比、关键决策及ADR引用完整（ADR-001 + ADR-002），或已说明无需新增ADR的依据。
- [x] 数据模型、接口契约、认证授权、幂等、安全、依赖、风险和回滚均已填写或明确不涉及。
- [x] 验收方式与后续Tasks可执行、可验证。

> 本文件原地修改；整个项目只保留这一份`design.md`并与唯一`spec.md`同目录，禁止创建第二套或任何版本副本。
