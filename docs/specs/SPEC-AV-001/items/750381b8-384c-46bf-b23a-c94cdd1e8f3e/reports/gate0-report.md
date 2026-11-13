# Gate 0 报告 — 需求分析

## 报告身份

| 项 | 内容 |
|---|---|
| item_id | 750381b8-384c-46bf-b23a-c94cdd1e8f3e |
| work_item_id | srdcloud-001 |
| spec_id | SPEC-AV-001 |
| 生成日期 | 2026-11-10 |
| 生成者 | sdd-requirement-skill |

## 权威源模式

- 模式：`source: repo`（仓内模式）
- PRD 正文：`docs/prd.md`，版本 1.0.0
- PRD 索引：`docs/prd-index.md`

## 需求概述

修复 CodeFree provider（`AgentCodeFree`）的 AgentLabel 显示名问题。此前
`codefreeAgentLabel` 函数从 agent URI 路径段（如 `spec`、`explore`）提取标签
作为 `AgentLabel`，导致 UI 显示内部类型标识而非 "CodeFree"。修复方案：移除
路径段提取逻辑，`AgentLabel` 留空，由前端 `KNOWN_AGENTS` 注册的 `codefree`
条目提供统一显示名。

## FEAT 列表

| FEAT编号 | 需求点摘要 | 归属候选 | 澄清状态 | Spec指针 |
|---|---|---|---|---|
| FEAT-001 | 移除 CodeFree provider AgentLabel 路径段提取 | internal/parser | CLEAR | SPEC-AV-001/FR-001 |
| FEAT-002 | 前端注册 CodeFree 显示名 | frontend | CLEAR | SPEC-AV-001/FR-002 |

## 逐项检查

### G0-01 prd-index 的 PRD 定位、版本、FEAT 指针和归属候选完整

- 适用性：全适用
- 级别：MANDATORY
- 状态：PASS
- 证据：`docs/prd-index.md` 索引含 2 条目，每条包含 PRD ID（AV-CODEFREE-LABEL）、
  来源路径（docs/prd.md#FEAT-xxx）、PRD 版本（1.0.0）、FEAT 编号（FEAT-001/
  FEAT-002）、归属候选（internal/parser、frontend）、Spec 指针
  （SPEC-AV-001/FR-001、SPEC-AV-001/FR-002）。

### G0-02 PRD 权威源模式唯一且索引与权威源一致

- 适用性：全适用
- 级别：MANDATORY
- 状态：PASS
- 证据：权威源模式声明 `source: repo`，`prd_path: docs/prd.md`。索引 2 条
  FEAT 与 `docs/prd.md` 第 6 节功能需求列表的 FEAT-001、FEAT-002 一一对应，
  需求点摘要和归属候选一致。

### G0-03 全部需求点均有 FEAT 编号，归属候选无冲突

- 适用性：全适用
- 级别：MANDATORY
- 状态：PASS
- 证据：FEAT-001 归属 internal/parser，FEAT-002 归属 frontend，两个模块不
  同，无归属冲突。每个需求点有唯一 FEAT 编号。

### G0-04 索引与 PRD 残留 `[NEEDS CLARIFICATION]` 为 0

- 适用性：全适用
- 级别：MANDATORY
- 状态：PASS
- 证据：`docs/prd-index.md` 和 `docs/prd.md` 全文搜索无
  `[NEEDS CLARIFICATION]` 标记。所有需求点已明确。

### G0-05 工作项或 PRD 路径、链接和版本可访问

- 适用性：全适用
- 级别：MANDATORY
- 状态：PASS
- 证据：`docs/prd.md` 已创建于项目根目录，可正常读取。元数据版本 1.0.0，
  工作项 srdcloud-001。索引来源路径 `docs/prd.md#FEAT-001` 和
  `docs/prd.md#FEAT-002` 指向同一文件的有效锚点。

### G0-06 无模糊用语且术语与域清单一致

- 适用性：全适用
- 级别：MANDATORY
- 状态：PASS
- 证据：PRD 正文和索引均无"等、尽量、合理、大约、酌情"等模糊用语。术语
  CodeFree、AgentLabel、KNOWN_AGENTS、AgentCodeFree、AgentCodefreeO 在全文
  中使用一致。In-Scope 和 Out-of-Scope 边界明确。

### G0-07 范围、流程和需求点内部一致

- 适用性：全适用
- 级别：MANDATORY
- 状态：PASS
- 证据：In-Scope 限定 CodeFree provider 的 AgentLabel 和前端注册，
  Out-of-Scope 排除 AgentCodefreeO 和其他 provider。核心流程 3 步描述与
  FEAT-001（移除提取逻辑）和 FEAT-002（前端注册）一致。非功能需求约束
  （不引入新依赖、不改变解析行为）与范围声明一致。

### G0-08 场景、干系人、边界和异常完整

- 适用性：全适用
- 级别：MANDATORY
- 状态：PASS
- 证据：干系人 2 角色（用户、开发者），各有明确诉求和使用场景。边界明确
  排除 AgentCodefreeO（独立 provider，有自己的 KNOWN_AGENTS 条目）和其他
  provider 的 AgentLabel 处理。约束与依赖章节说明 codefree 和 codefree-o
  在 KNOWN_AGENTS 中共存。

## 文本完整性核验

- `docs/prd.md`：UTF-8 写入后读回，中文内容正确，无 `�`、无异常问号、无
  转码残片。
- `docs/prd-index.md`：UTF-8 写入后读回，中文内容正确，无 `�`、无异常
  问号、无转码残片。

## 汇总

| check_id | 级别 | 状态 |
|---|---|---|
| G0-01 | MANDATORY | PASS |
| G0-02 | MANDATORY | PASS |
| G0-03 | MANDATORY | PASS |
| G0-04 | MANDATORY | PASS |
| G0-05 | MANDATORY | PASS |
| G0-06 | MANDATORY | PASS |
| G0-07 | MANDATORY | PASS |
| G0-08 | MANDATORY | PASS |

**Gate 0 结论：PASS**

全部强制项通过。需求权威源模式明确（仓内模式），PRD 和索引已创建，2 个
FEAT 需求点已登记，无残留澄清标记，术语和范围一致。

## 产物清单

| 产物 | 路径 | 状态 |
|---|---|---|
| PRD 索引 | docs/prd-index.md | 已创建 |
| PRD 正文 | docs/prd.md | 已创建 |
| Gate 0 报告 | docs/specs/SPEC-AV-001/items/750381b8-384c-46bf-b23a-c94cdd1e8f3e/reports/gate0-report.md | 已生成 |
