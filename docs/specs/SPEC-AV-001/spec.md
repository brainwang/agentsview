# CodeFree Provider AgentLabel 修复与前端注册 规格说明书（Spec）

## 1. 元数据

| 项 | 内容 |
|---|---|
| Spec ID | SPEC-AV-001 |
| 工作项 ID | srdcloud-001 |
| 关联 PRD | docs/prd.md（AV-CODEFREE-LABEL v1.0.0） |
| 关联 FEAT | FEAT-001, FEAT-002 |
| AGENTS约束 | `AGENTS.md`（引用适用条款） |
| 作者 | srd18901883260 |
| 创建日期 | 2026-11-10 |
| 状态 | Draft |
| 版本号 | 1.0.0 |
| 产品类型 | 传统·L1 |
| 模板适配 | 核心骨架 |
| 关联人 | 产品负责人：srd18901883260；技术负责人：srd18901883260 |
| 原型版本 | 不涉及 |

> Gate 0 的 `docs/prd-index.md` 是需求追溯入口。项目只允许一份 `spec.md`，本文件原地演进。

## 1.1 FEAT映射表

| FEAT ID | 需求点 | Spec ID | FR列表 | 状态 | expect_fr |
|---|---|---|---|---|---|
| FEAT-001 | 移除 CodeFree provider AgentLabel 路径段提取 | SPEC-AV-001 | FR-001 | active | FR-001 |
| FEAT-002 | 前端注册 CodeFree 显示名 | SPEC-AV-001 | FR-002 | active | FR-002 |

> 必须覆盖Gate 0索引中归属本项目的全部FEAT；FR条款必须在本文件真实存在，FR增删时同一MR更新本表。

## 2. 范围声明

- In-Scope：CodeFree provider（`AgentCodeFree`）的 `AgentLabel` 赋值逻辑修改；前端 `KNOWN_AGENTS` 列表注册 `codefree` 条目。
- Out-of-Scope：`AgentCodefreeO`（OpenCode fork，独立 provider）的显示名逻辑；其他 provider 的 AgentLabel 处理；CodeFree provider 的会话解析、SQLite 读取和 token 统计等已有功能。

## 3. 业务上下文

### 3.1 干系人与角色

| 角色 | 诉求 | 使用场景 | 责任边界 |
|---|---|---|---|
| AgentsView 用户 | 在 UI 中看到 CodeFree 会话显示为 "CodeFree" 而非内部类型标识 | 浏览会话列表、查看会话详情 | 仅消费显示名，不参与 parser 逻辑 |
| AgentsView 开发者 | parser 层不暴露内部数字员工类型标识符作为显示名 | 维护 parser 和前端显示逻辑 | 负责 parser AgentLabel 赋值和前端 KNOWN_AGENTS 注册 |

### 3.2 业务流程

1. CodeFree IDE 产生会话数据，agent URI 包含路径段（如 `codefree://spec/...`、`codefree://sdd-dev-engineer/...`）。
2. AgentsView parser 读取会话时，不再从 URI 路径段提取 AgentLabel，将 `AgentLabel` 设为空字符串。
3. 前端渲染时，`KNOWN_AGENTS` 中 `codefree` 条目提供 label "CodeFree"，作为 fallback 显示名。

### 3.3 业务规则

- BR-001：CodeFree provider 的 agent URI 路径段（如 `spec`、`sdd-dev-engineer`、`explore`）是内部数字员工类型标识符，不是用户可见的显示名称，不得作为 `AgentLabel` 输出。
- BR-002：`AgentCodefreeO`（OpenCode fork）是独立 provider，有自己的 `KNOWN_AGENTS` 条目，不受本次变更影响。
- BR-003：`codefree` 和 `codefree-o` 在 `KNOWN_AGENTS` 中共存，互不干扰。

## 4. 功能需求与验收条件

### FR-001 移除 CodeFree provider AgentLabel 路径段提取（关联 FEAT-001）

- 功能描述：`internal/parser/codefree.go` 中不再从 agent URI 路径段提取 AgentLabel，`AgentLabel` 字段设为空字符串。
- 用户场景：AgentsView 开发者修改 parser 代码后，CodeFree 会话在 UI 中不再显示内部类型标识（如 "SPEC"）作为 provider 显示名。
- 验收条件：
  - AC-001-1：Given `codefree.go` 中 `codefreeAgentLabel` 函数已移除，When parser 加载 CodeFree 会话，Then `AgentLabel` 字段为空字符串（`""`）。
  - AC-001-2：Given `codefree.go` 不再导入 `path` 包，When 执行 `go vet ./internal/parser/`，Then 无未使用导入错误。
- 执行方式：【动态检测】

### FR-002 前端注册 CodeFree 显示名（关联 FEAT-002）

- 功能描述：`frontend/src/lib/utils/agents.ts` 的 `KNOWN_AGENTS` 列表包含 `codefree` 条目，label 为 "CodeFree"。
- 用户场景：AgentsView 用户在 UI 中浏览会话列表时，CodeFree 会话显示为 "CodeFree"。
- 验收条件：
  - AC-002-1：Given `KNOWN_AGENTS` 列表包含 `{ name: "codefree", color: "var(--accent-purple)", label: "CodeFree" }`，When 前端渲染 CodeFree 会话且 `AgentLabel` 为空，Then 显示名 fallback 为 "CodeFree"。
- 执行方式：【规范校验】

> FR编号采用 `FR-xxx`，AC编号采用 `AC-xxx-y`。确定性、视觉、统计性AC应给出自动化验证口径；人工裁决型AC须留下人工评审或抽检证据。当前试点Gate 4只执行已映射的本地单元测试，其他类型证据在Gate 5汇总。

## 5. 边界与异常

| 场景类型 | 场景描述 | 关联FR/AC | 预期行为（错误码/降级策略/提示信息） |
|---|---|---|---|
| 边界场景 | agent URI 不含路径段（如 `codefree://`） | FR-001/AC-001-1 | `AgentLabel` 为空字符串，前端 fallback 到 "CodeFree" |
| 边界场景 | `KNOWN_AGENTS` 中不存在 `codefree` 条目 | FR-002/AC-002-1 | 前端使用默认 fallback 逻辑显示 agent name |
| 异常场景 | `codefree.go` 中 `path` 包仍被导入 | FR-001/AC-001-2 | `go vet` 报告未使用导入错误 |

## 6. 非功能需求

| 类型 | 要求 | 指标/验收方式 | 关联FR/AC |
|---|---|---|---|
| 性能 | 不引入额外 URI 解析开销 | 移除 `codefreeAgentLabel` 函数后无路径段提取操作 | FR-001/AC-001-1 |
| 安全 | 不暴露内部数字员工类型标识符 | `AgentLabel` 为空，前端不显示内部路径段 | FR-001/AC-001-1 |
| 可用性 | 不影响 CodeFree provider 的会话解析和 SQLite 读取 | 现有测试 `TestLoadCodeFreeSession_*` 和 `TestCodeFreeProvider_Parse` 通过 | FR-001/AC-001-1 |

## 7. 外部接口

| 方法 | 路径 | 用途 | 归属AC |
|---|---|---|---|
| 不涉及 | 不涉及 | 本变更不新增或修改外部接口 | 不涉及 |

- 请求/响应示例：不涉及
- 契约文件：不涉及

## 8. 数据模型

- 核心实体定义：不涉及（不新增或修改数据库实体）
- 数据流说明：CodeFree 会话数据流不变 — parser 读取 SQLite 会话数据后，`AgentLabel` 字段从路径段提取改为空字符串，前端通过 `KNOWN_AGENTS` fallback 提供显示名

## 9. 约束与假设

- 技术约束：Go 标准库优先（AGENTS.md 约定）；不引入新依赖；`go fmt ./...` 和 `go vet ./...` 通过。
- 业务约束：`AgentCodefreeO` 是独立 provider，有自己的 `KNOWN_AGENTS` 条目，不受本次变更影响。
- 假设条件：前端 `KNOWN_AGENTS` 的 fallback 逻辑在 `AgentLabel` 为空时使用 `name` 匹配的 `label`；若该假设不成立，前端可能显示 agent name 而非 "CodeFree"。

## 10. 变更历史

| 日期 | 版本 | 变更内容 | 作者 |
|---|---|---|---|
| 2026-11-10 | 1.0.0 | 初始版本 | srd18901883260 |

> 传统·L1到此结束。

## 自检清单（评审辅助，不作Gate依据）

- [ ] 项目中只有一份规范命名的 `spec.md`，并原地修改。
- [ ] Spec ID、工作项ID、产品类型与模板适配合法。
- [ ] FEAT映射覆盖全部需求点且引用的FR真实存在。
- [ ] FR/AC编号唯一、稳定，每个FR至少包含一条AC。
- [ ] 确定性、视觉、统计性和人工裁决型AC均有对应验证口径。
- [ ] 残留待澄清标记为0。
- [ ] 不存在模糊用语和未完成占位符。
- [ ] 当前产品类型要求的扩展章节完整，未混用L2/L3章节。
- [ ] 变更历史已更新，未创建平行版本文件。
